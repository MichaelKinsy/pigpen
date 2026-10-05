package jev_test

import (
	"strconv"
	"testing"
)

// Thresholds are inclusive (>=), each one, and the confidence floors too. The
// original's comparisons are all >=; a >= that became > would not show in the
// scenarios because their values sit well away from every threshold.

func TestGate_EachThresholdIsInclusive(t *testing.T) {
	cases := map[string]struct {
		body map[string]any
		want string
	}{
		"destructive":  {gateBody(0.9, 0, 0, 0, 0.9), "jev: destructive 0.90"},
		"exfiltration": {gateBody(0, 0.7, 0, 0, 0.9), "jev: exfiltration 0.70"},
		"beyond_scope": {gateBody(0, 0, 0.85, 0, 0.9), "jev: beyond_scope 0.85"},
		"impact":       {gateBody(0, 0, 0, 2.5, 0.9), "jev: impact 2.50/3 at confidence 0.90"},
		"impact conf":  {gateBody(0, 0, 0, 3, 0.5), "jev: impact 3.00/3 at confidence 0.50"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			t.Setenv("TYPESAFE_API_KEY", testKey)
			srv := newFakeJev(t, always(c.body))
			e.writeGlobal(t, jevConfig(srv, nil))
			h := start(t, e, newHostState(), HostOptions{})
			h.toolCall("bash", bash("x"))
			if got := h.lastStatus(); got != c.want {
				t.Errorf("status = %q, want %q", got, c.want)
			}
		})
	}
}

func TestGate_JustBelowEachThresholdIsClear(t *testing.T) {
	for name, body := range map[string]map[string]any{
		"destructive":  gateBody(0.89, 0, 0, 0, 0.9),
		"exfiltration": gateBody(0, 0.69, 0, 0, 0.9),
		"beyond_scope": gateBody(0, 0, 0.84, 0, 0.9),
		"impact":       gateBody(0, 0, 0, 2.49, 0.9),
	} {
		e := newEnv(t)
		t.Setenv("TYPESAFE_API_KEY", testKey)
		srv := newFakeJev(t, always(body))
		e.writeGlobal(t, jevConfig(srv, nil))
		h := start(t, e, newHostState(), HostOptions{})
		h.toolCall("bash", bash("x"))
		if got := h.lastStatus(); got != "jev: clear (shadow)" {
			t.Errorf("%s: status = %q", name, got)
		}
	}
}

func TestOutput_ClassConfidenceFloorIsInclusive(t *testing.T) {
	h, _ := outputHost(t, outBody(0.01, "environment", 0.6), nil)
	if _, none := h.toolResult("bash", bash("x"), text("boom"), true); none {
		t.Fatal("confidence exactly at output.minConfidence must give advice")
	}
}

func TestErrors_ChoiceOutsideItsOptionsIsAnError(t *testing.T) {
	h, _ := errorHost(t, always(outBody(0.01, "brand_new_class", 1.0)), nil)
	h.toolResult("bash", bash("x"), text("out"), false)
	if !h.anyNotification("not one of its options") || !h.anyNotification("(failing open)") {
		t.Errorf("notifications = %q", h.notifications())
	}
}

func TestCommand_CheckNeedsJevOn(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(clearGate()))
	cfg := jevConfig(srv, nil)
	delete(cfg, "enabled")
	e.writeGlobal(t, cfg)
	h := start(t, e, newHostState(), HostOptions{})
	h.Command("jev", "check some text")
	if srv.count() != 0 {
		t.Fatal("/jev check sent text while Jev is off")
	}
	mustContain(t, "note", lastNote(h), "off", "/jev on")
}

func TestCorrection_TypeSafeEnvironmentCannotRedirectTheKey(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	trusted := newFakeJev(t, always(clearGate()))
	attacker := newFakeJev(t, always(clearGate()))
	t.Setenv("TYPESAFE_BASE_URL", attacker.srv.URL)
	t.Setenv("TYPESAFE_DEFAULT_MODEL", "attacker-model")
	e.writeGlobal(t, jevConfig(trusted, nil))
	h := start(t, e, newHostState(), HostOptions{})
	h.toolCall("bash", bash("ls"))
	if attacker.count() != 0 || trusted.count() != 1 {
		t.Fatalf("attacker got %d requests, trusted got %d", attacker.count(), trusted.count())
	}
}

func TestGate_CacheHoldsSixtyFourVerdicts(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(clearGate()))
	e.writeGlobal(t, jevConfig(srv, nil))
	h := start(t, e, newHostState(), HostOptions{})
	for i := 0; i < 70; i++ {
		h.toolCall("bash", bash("cmd "+strconv.Itoa(i)))
	}
	if srv.count() != 70 {
		t.Fatalf("requests = %d", srv.count())
	}
	h.toolCall("bash", bash("cmd 69")) // recent: still cached
	if srv.count() != 70 {
		t.Errorf("a recent verdict was not cached: %d requests", srv.count())
	}
	h.toolCall("bash", bash("cmd 0")) // evicted beyond 64 entries
	if srv.count() != 71 {
		t.Errorf("the cache grew past its 64 entries: %d requests", srv.count())
	}
}

func TestGate_ExpiredVerdictIsJudgedAgain(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(clearGate()))
	e.writeGlobal(t, jevConfig(srv, map[string]any{"gate": map[string]any{"cacheSeconds": 1}}))
	h := start(t, e, newHostState(), HostOptions{})
	h.toolCall("bash", bash("ls"))
	sleepMs(1300)
	h.toolCall("bash", bash("ls"))
	if srv.count() != 2 {
		t.Errorf("requests = %d, want 2 after the window", srv.count())
	}
}

// The original's endpoint was the full request URL; the shared client wants the API
// root, so a trailing /v1/systemone (and slash) is accepted and removed (PORT.md D1).
func TestConfig_EndpointMayBeTheFullRequestURL(t *testing.T) {
	for _, suffix := range []string{"", "/", "/v1/systemone", "/v1/systemone/"} {
		e := newEnv(t)
		t.Setenv("TYPESAFE_API_KEY", testKey)
		srv := newFakeJev(t, always(clearGate()))
		cfg := jevConfig(srv, nil)
		cfg["endpoint"] = srv.srv.URL + suffix
		e.writeGlobal(t, cfg)
		h := start(t, e, newHostState(), HostOptions{})
		h.toolCall("bash", bash("ls"))
		if srv.count() != 1 || srv.first(t).Path != "/v1/systemone" {
			t.Errorf("endpoint %q: %d requests, path %q", suffix, srv.count(), func() string {
				if srv.count() == 0 {
					return ""
				}
				return srv.first(t).Path
			}())
		}
	}
}
