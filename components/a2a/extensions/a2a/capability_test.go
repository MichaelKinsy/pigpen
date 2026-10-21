package a2aext

// Optional capabilities (gRPC and REST bindings, push notifications, A2A 0.3, extended agent card, URL file parts,
// OAuth2, mutual TLS, signed cards): off by default, advertised false and refused, and an explicit opt-in seam.
// Each is a tracked follow-up (port/PORT.md); switching one on without an implementation fails when the
// configuration is loaded, with the exact refusal sentence.

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
)

func TestEveryCapabilityKeyHasExactlyOneSpecAndOneFlag(t *testing.T) {
	typ := reflect.TypeOf(CapabilitiesConfig{})
	if typ.NumField() != len(capSpecs) {
		t.Fatalf("%d flags, %d specs", typ.NumField(), len(capSpecs))
	}
	for i := 0; i < typ.NumField(); i++ {
		key := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		var c CapabilitiesConfig
		reflect.ValueOf(&c).Elem().Field(i).SetBool(true)
		var on []string
		for _, spec := range capSpecs {
			if spec.on(c) {
				on = append(on, spec.key)
			}
		}
		if len(on) != 1 || on[0] != key {
			t.Errorf("flag %q switches on %v, want exactly itself", key, on)
		}
	}
}

func TestCapabilitiesAreOffByDefault(t *testing.T) {
	if set := (CapabilitiesConfig{}).enabledSet(); len(set) != 0 {
		t.Fatalf("enabled by default: %v", set)
	}
	dir := t.TempDir()
	writeConfig(t, dir, `{"listen":"127.0.0.1:5555","insecureNoAuth":true}`)
	cfg, err := LoadConfig(LoadOptions{ConfigHome: dir, Getenv: envFrom(nil)})
	if err != nil || cfg.Capabilities != (CapabilitiesConfig{}) {
		t.Fatalf("a configuration without the key: %+v %v", cfg.Capabilities, err)
	}
	s := startServer(t, serverConfig(), &scriptedWorker{})
	card := fetchCard(t, "http://"+s.Addr())
	c := card.Capabilities
	if !c.Streaming || c.PushNotifications || c.ExtendedAgentCard {
		t.Errorf("capabilities %+v", c)
	}
	if len(card.SupportedInterfaces) != 1 || card.SupportedInterfaces[0].ProtocolBinding != a2a.TransportProtocolJSONRPC {
		t.Errorf("interfaces %+v", card.SupportedInterfaces)
	}
	if len(card.Signatures) != 0 {
		t.Errorf("signatures %+v", card.Signatures)
	}
	if len(card.SecuritySchemes) != 1 || card.SecuritySchemes["bearer"] == nil {
		t.Errorf("only the bearer scheme is declared by default: %+v", card.SecuritySchemes)
	}
	// And the calls are refused with the specification's codes and the exact sentence.
	base := "http://" + s.Addr()
	for method, want := range map[string]struct {
		code    int
		feature string
	}{
		"CreateTaskPushNotificationConfig": {-32003, "push notifications"},
		"GetExtendedAgentCard":             {-32007, "an extended agent card"},
	} {
		_, out := rawRPC(t, base, tokenA, "1.0", method, map[string]any{"taskId": "t"})
		code, msg := rpcError(t, out)
		if code != want.code || msg != doesNotSupport+want.feature+" yet." && !strings.HasPrefix(msg, doesNotSupport+want.feature+" yet.") {
			t.Errorf("%s: %d %q", method, code, msg)
		}
	}
	_, out := rawRPC(t, base, tokenA, "0.3", "SendMessage", map[string]any{})
	if code, msg := rpcError(t, out); code != -32009 || !strings.HasPrefix(msg, doesNotSupport+"A2A protocol version 0.3 yet.") {
		t.Errorf("0.3: %d %q", code, msg)
	}
}

func TestEnablingACapabilityWithoutAnImplementationFailsAtConfigLoad(t *testing.T) {
	for _, spec := range capSpecs {
		dir := t.TempDir()
		writeConfig(t, dir, `{"listen":"127.0.0.1:5555","insecureNoAuth":true,"capabilities":{"`+spec.key+`":true}}`)
		_, err := LoadConfig(LoadOptions{ConfigHome: dir, Getenv: envFrom(nil)})
		want := doesNotSupport + spec.feature + " yet."
		if err == nil || err.Error() != want {
			t.Errorf("%s: LoadConfig error %v, want exactly %q", spec.key, err, want)
		}
		// The same with the listener off: a switched-on capability is a mistake wherever it is read.
		writeConfig(t, dir, `{"capabilities":{"`+spec.key+`":true}}`)
		if _, err := LoadConfig(LoadOptions{ConfigHome: dir, Getenv: envFrom(nil)}); err == nil || err.Error() != want {
			t.Errorf("%s with the listener off: %v", spec.key, err)
		}
		// Off explicitly is the default.
		writeConfig(t, dir, `{"listen":"127.0.0.1:5555","insecureNoAuth":true,"capabilities":{"`+spec.key+`":false}}`)
		if _, err := LoadConfig(LoadOptions{ConfigHome: dir, Getenv: envFrom(nil)}); err != nil {
			t.Errorf("%s false: %v", spec.key, err)
		}
		// NewServer says the same, for a configuration built in code.
		cfg := serverConfig()
		for i, s2 := range capSpecs {
			if s2.key == spec.key {
				reflect.ValueOf(&cfg.Capabilities).Elem().Field(i).SetBool(true)
			}
		}
		if _, err := NewServer(cfg, &scriptedWorker{}, serverEnv); err == nil || err.Error() != want {
			t.Errorf("%s: NewServer error %v", spec.key, err)
		}
	}
}

func TestCapabilitiesObjectRejectsUnknownKeysAndNonBooleans(t *testing.T) {
	for name, body := range map[string]string{
		"unknown key": `{"capabilities":{"telepathy":true}}`,
		"not boolean": `{"capabilities":{"grpc":"yes"}}`,
		"not object":  `{"capabilities":true}`,
	} {
		dir := t.TempDir()
		writeConfig(t, dir, body)
		if _, err := LoadConfig(LoadOptions{ConfigHome: dir, Getenv: envFrom(nil)}); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// fakeCapability stands in for an implementation: it advertises a flag, mounts a route and records both.
type fakeCapability struct {
	key     string
	mounted bool
	routes  string
}

func (f *fakeCapability) Key() string { return f.key }
func (f *fakeCapability) Advertise(card *a2a.AgentCard) {
	switch f.key {
	case "pushNotifications":
		card.Capabilities.PushNotifications = true
	case "extendedAgentCard":
		card.Capabilities.ExtendedAgentCard = true
	}
}
func (f *fakeCapability) Mount(mux *http.ServeMux, h a2asrv.RequestHandler) error {
	f.mounted = h != nil
	mux.HandleFunc("GET /fake-"+f.key, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("fake " + f.key)) })
	return nil
}

func TestARegisteredCapabilityIsAdvertisedMountedAndNotRefused(t *testing.T) {
	fake := &fakeCapability{key: "pushNotifications"}
	defer registerCapability(fake)()
	cfg := serverConfig()
	cfg.Capabilities.PushNotifications = true
	s := startServer(t, cfg, &scriptedWorker{})
	base := "http://" + s.Addr()
	if !fake.mounted {
		t.Error("the capability was not mounted")
	}
	card := fetchCard(t, base)
	if !card.Capabilities.PushNotifications || card.Capabilities.ExtendedAgentCard {
		t.Errorf("only the switched-on capability is advertised: %+v", card.Capabilities)
	}
	req, _ := http.NewRequest(http.MethodGet, base+"/fake-pushNotifications", nil)
	req.Header.Set("Authorization", "Bearer "+tokenA)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("the mounted route: %v %v", resp, err)
	}
	resp.Body.Close()
	// The guard no longer answers the push methods with the refusal; the handler does (a2a-go has no push store
	// here, so its own error comes back, not ours).
	_, out := rawRPC(t, base, tokenA, "1.0", "ListTaskPushNotificationConfigs", map[string]any{"taskId": "t"})
	if _, msg := rpcError(t, out); strings.HasPrefix(msg, doesNotSupport) {
		t.Errorf("the guard still refuses an enabled capability: %q", msg)
	}
	// A capability that stays off stays refused on the same server.
	_, out = rawRPC(t, base, tokenA, "1.0", "GetExtendedAgentCard", map[string]any{})
	if code, msg := rpcError(t, out); code != -32007 || !strings.HasPrefix(msg, doesNotSupport+"an extended agent card yet.") {
		t.Errorf("extended card: %d %q", code, msg)
	}
	// Registered but not switched on: nothing changes.
	s2 := startServer(t, serverConfig(), &scriptedWorker{})
	if fetchCard(t, "http://"+s2.Addr()).Capabilities.PushNotifications {
		t.Error("a registered capability that is not switched on was advertised")
	}
}

func TestProtocol03IsAcceptedOnlyWhenItsCapabilityIsOn(t *testing.T) {
	if err := checkVersion([]string{"0.3"}, false); err == nil || !strings.HasPrefix(err.Error(), doesNotSupport+"A2A protocol version 0.3 yet.") {
		t.Errorf("off: %v", err)
	}
	if err := checkVersion(nil, false); err == nil {
		t.Error("a missing header is protocol 0.3 and is refused while it is off")
	}
	if err := checkVersion([]string{"0.3"}, true); err != nil {
		t.Errorf("on: %v", err)
	}
	if err := checkVersion(nil, true); err != nil {
		t.Errorf("a missing header with 0.3 on: %v", err)
	}
	if err := checkVersion([]string{"2.0"}, true); err == nil {
		t.Error("another version is refused even with 0.3 on")
	}
}

func TestExtendedAgentCardIsNotRefusedByTheGuardWhenItsCapabilityIsOn(t *testing.T) {
	fake := &fakeCapability{key: "extendedAgentCard"}
	defer registerCapability(fake)()
	cfg := serverConfig()
	cfg.Capabilities.ExtendedAgentCard = true
	s := startServer(t, cfg, &scriptedWorker{})
	base := "http://" + s.Addr()
	if !fetchCard(t, base).Capabilities.ExtendedAgentCard {
		t.Error("the switched-on capability is advertised")
	}
	_, out := rawRPC(t, base, tokenA, "1.0", "GetExtendedAgentCard", map[string]any{})
	if _, msg := rpcError(t, out); strings.HasPrefix(msg, doesNotSupport) {
		t.Errorf("the guard still refuses an enabled capability: %q", msg)
	}
	// Push stays refused on the same server.
	_, out = rawRPC(t, base, tokenA, "1.0", "ListTaskPushNotificationConfigs", map[string]any{"taskId": "t"})
	if code, msg := rpcError(t, out); code != -32003 || !strings.HasPrefix(msg, doesNotSupport+"push notifications yet.") {
		t.Errorf("push: %d %q", code, msg)
	}
}
