package pi_permission_system

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// Differential test of the forwarding layer against the original's own functions. testdata/forwarding_oracle.json is what
// port/probe/forwarding-probe.test.ts recorded from the unmodified original (port/record-forwarding-probe.sh, inputs from
// port/probe/gen-forwarding-cases.py): tolerant reads of 195 request and 119 response files, the heartbeat states and sweep of 43
// records, 648 target resolutions, session id encoding, 168 decision resolutions, 153 delegation-envelope verdicts and 843 serving-node
// scenarios run end to end (response file bytes, the details escalated, the broadcast, what was recorded, every review-log entry).

func fwOracle(t *testing.T) *jsObject {
	t.Helper()
	data, err := os.ReadFile("testdata/forwarding_oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	v, err := parseJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	return v.(*jsObject)
}

func fwList(o *jsObject, key string) []*jsObject {
	raw, _ := o.vals[key].([]any)
	out := make([]*jsObject, len(raw))
	for i, e := range raw {
		out[i] = e.(*jsObject)
	}
	return out
}

func fwStrOf(o *jsObject, key string) string { s, _ := o.str(key); return s }

func TestForwardingOracleRequests(t *testing.T) {
	cases := fwList(fwOracle(t), "requests")
	if len(cases) < 150 {
		t.Fatalf("only %d cases", len(cases))
	}
	dir := t.TempDir()
	for i, c := range cases {
		raw := fwStrOf(c, "raw")
		path := filepath.Join(dir, "req.json")
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		got := ReadForwardedPermissionRequest(nil, path)
		want := c.vals["result"]
		if want == nil {
			if got != nil {
				t.Errorf("case %d %q: the original rejects it, the port reads %s", i, raw, marshalJSON(got.JS(), ""))
			}
			continue
		}
		if got == nil {
			t.Errorf("case %d %q: the original reads %s, the port rejects it", i, raw, marshalJSON(want, ""))
			continue
		}
		if !fwEqual(fwParse(t, marshalJSON(got.JS(), "")), want) {
			t.Errorf("case %d %q:\n got  %s\n want %s", i, raw, marshalJSON(got.JS(), ""), marshalJSON(want, ""))
		}
	}
}

func TestForwardingOracleResponses(t *testing.T) {
	cases := fwList(fwOracle(t), "responses")
	if len(cases) < 100 {
		t.Fatalf("only %d cases", len(cases))
	}
	dir := t.TempDir()
	for i, c := range cases {
		raw := fwStrOf(c, "raw")
		path := filepath.Join(dir, "res.json")
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		got := ReadForwardedPermissionResponse(nil, path)
		want := c.vals["result"]
		if want == nil {
			if got != nil {
				t.Errorf("case %d %q: the original rejects it, the port reads %s", i, raw, marshalJSON(got.JS(), ""))
			}
			continue
		}
		if got == nil {
			t.Errorf("case %d %q: the original reads %s, the port rejects it", i, raw, marshalJSON(want, ""))
			continue
		}
		gotJS := got.JS()
		if w, _ := want.(*jsObject).vals["respondedAt"].(string); w == "NOW" {
			if got.RespondedAt < 1e12 {
				t.Errorf("case %d %q: a response without a time must read the clock, not %v", i, raw, got.RespondedAt)
			}
			gotJS.set("respondedAt", "NOW") // the read time, which no two runs share
		}
		if !fwEqual(fwParse(t, marshalJSON(gotJS, "")), want) {
			t.Errorf("case %d %q:\n got  %s\n want %s", i, raw, marshalJSON(gotJS, ""), marshalJSON(want, ""))
		}
	}
}

func TestForwardingOracleHeartbeats(t *testing.T) {
	hb := fwOracle(t).obj("heartbeats")
	now, _ := hb.num("now")
	alive, _ := hb.num("alivePid")
	dir := filepath.Join(t.TempDir(), "hb")
	if err := os.MkdirAll(ServingHeartbeatDir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	newStore := func() *ServingHeartbeatStore {
		return NewServingHeartbeatStore(ServingHeartbeatStoreDeps{
			ForwardingDir: dir, Logger: &fwLogger{}, Now: func() int64 { return int64(now) }, PID: int(alive),
			IsProcessAlive: func(pid int) bool { return pid == int(alive) },
		})
	}
	store := newStore()
	for i, c := range fwList(hb, "states") {
		raw := fwStrOf(c, "raw")
		id := "h" + itoa(i)
		if err := os.WriteFile(ServingHeartbeatPath(dir, id), []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		if got, want := string(store.Read(id)), fwStrOf(c, "state"); got != want {
			t.Errorf("case %d %q: the port reads %s, the original %s", i, raw, got, want)
		}
	}
	ids := store.ServingIDs()
	sort.Strings(ids)
	var want []string
	for _, v := range hb.vals["servingIds"].([]any) {
		want = append(want, v.(string))
	}
	if strings.Join(ids, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("serving ids %q, the original %q", ids, want)
	}
	newStore().MarkServing("me")
	entries, _ := os.ReadDir(ServingHeartbeatDir(dir))
	var left []string
	for _, e := range entries {
		left = append(left, e.Name())
	}
	var wantLeft []string
	for _, v := range hb.vals["afterSweep"].([]any) {
		wantLeft = append(wantLeft, v.(string))
	}
	if strings.Join(left, "\x00") != strings.Join(wantLeft, "\x00") {
		t.Errorf("after the sweep %q, the original %q", left, wantLeft)
	}
}

func TestForwardingOracleTargets(t *testing.T) {
	cases := fwList(fwOracle(t), "targets")
	if len(cases) < 600 {
		t.Fatalf("only %d cases", len(cases))
	}
	for i, c := range cases {
		in := c.obj("case")
		registry := NewSubagentSessionRegistry()
		hasRegistry := in.vals["registry"] != nil
		if hasRegistry {
			reg := in.obj("registry")
			for _, id := range reg.keys {
				info := SubagentSessionInfo{}
				if parent, ok := reg.obj(id).str("parentSessionId"); ok {
					info.ParentSessionID = &parent
				}
				registry.Register(id, info)
			}
		}
		env := map[string]string{}
		envObj := in.obj("env")
		for _, k := range envObj.keys {
			env[k], _ = envObj.str(k)
		}
		isSubagent, _ := in.flag("isSubagent")
		opts := ResolveTargetOptions{IsSubagent: isSubagent, CurrentSessionID: fwStrOf(in, "currentSessionId"), Env: env, SessionID: fwStrOf(in, "sessionId")}
		if hasRegistry {
			opts.Registry = registry
		}
		got, ok := ResolvePermissionForwardingTarget(opts)
		want := c.vals["result"]
		switch {
		case want == nil && ok:
			t.Errorf("case %d %s: the original finds no target, the port %+v", i, marshalJSON(in, ""), got)
		case want != nil && !ok:
			t.Errorf("case %d %s: the original finds %s, the port none", i, marshalJSON(in, ""), marshalJSON(want, ""))
		case want != nil:
			w := want.(*jsObject)
			if got.SessionID != fwStrOf(w, "sessionId") || string(got.Source) != fwStrOf(w, "source") {
				t.Errorf("case %d %s: got %+v want %s", i, marshalJSON(in, ""), got, marshalJSON(want, ""))
			}
		}
	}
}

func TestForwardingOracleSessionIDs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the recorded locations are POSIX paths")
	}
	for _, c := range fwList(fwOracle(t), "sessionIds") {
		id := fwStrOf(c, "id")
		normalized, ok := NormalizePermissionForwardingSessionID(id)
		if want := c.vals["normalized"]; (want == nil) != !ok || (ok && want.(string) != normalized) {
			t.Errorf("%q: normalized %q/%v, the original %v", id, normalized, ok, want)
		}
		if got, want := EncodeSessionIDForPath(id), fwStrOf(c, "encoded"); got != want {
			t.Errorf("%q: encoded %q, the original %q", id, got, want)
		}
		location, err := CreatePermissionForwardingLocation("/root", id)
		want := c.obj("location")
		if msg, isErr := want.str("error"); isErr {
			if err == nil || err.Error() != msg {
				t.Errorf("%q: error %v, the original %q", id, err, msg)
			}
			continue
		}
		if err != nil || location.SessionID != fwStrOf(want, "sessionId") || location.SessionRootDir != fwStrOf(want, "sessionRootDir") ||
			location.RequestsDir != fwStrOf(want, "requestsDir") || location.ResponsesDir != fwStrOf(want, "responsesDir") || location.Label != fwStrOf(want, "label") {
			t.Errorf("%q: location %+v (%v), the original %s", id, location, err, marshalJSON(want, ""))
		}
	}
}

func TestForwardingOracleResolutions(t *testing.T) {
	cases := fwList(fwOracle(t), "resolutions")
	if len(cases) < 100 {
		t.Fatalf("only %d cases", len(cases))
	}
	for i, c := range cases {
		decided := AsDecisionSource(c.vals["decidedBy"])
		want := c.vals["decider"]
		if (want == nil) != (decided == nil) {
			t.Errorf("case %d %s: the guard disagrees (port %v, original %s)", i, marshalJSON(c.vals["decidedBy"], ""), decided, marshalJSON(want, ""))
			continue
		}
		if decided == nil {
			continue
		}
		if !fwEqual(decided.JS(), want) {
			t.Errorf("case %d: narrowed %s, the original %s", i, marshalJSON(decided.JS(), ""), marshalJSON(want, ""))
		}
		approved, _ := c.flag("approved")
		forSession, _ := c.flag("forSession")
		if got, w := string(ResolutionFor(*decided, DecisionOutcome{Approved: approved, ForSession: forSession})), fwStrOf(c, "resolution"); got != w {
			t.Errorf("case %d %s: resolution %s, the original %s", i, marshalJSON(want, ""), got, w)
		}
	}
}

func TestForwardingOracleEnvelope(t *testing.T) {
	cases := fwList(fwOracle(t), "envelope")
	if len(cases) < 100 {
		t.Fatalf("only %d cases", len(cases))
	}
	for i, c := range cases {
		details := &PromptPermissionDetails{RequestID: "x", Source: "tool_call"}
		if s, ok := c.str("displaySurface"); ok {
			details.Surface = &s
		}
		if s, ok := c.str("accessIntentSurface"); ok {
			details.AccessIntent = &ForwardedAccessFacts{Surface: s, MatchValues: []string{}}
		}
		verdict := AuthorizerVerdict{Kind: fwStrOf(c, "verdict")}
		if verdict.Kind == "deny" {
			verdict.Reason = fwSP("r")
		}
		enclosed := EncloseInDelegationEnvelope(func(*PromptPermissionDetails, PermissionQuery, AuthorizerLog) (AuthorizerVerdict, error) {
			return verdict, nil
		})
		got, _ := enclosed(details, nil, nil)
		want := c.obj("result")
		if got.Kind != fwStrOf(want, "kind") || (got.Reason != nil) != want.has("reason") {
			t.Errorf("case %d %s: got %+v want %s", i, marshalJSON(c, ""), got, marshalJSON(want, ""))
		}
	}
}

// ── the serving node, end to end ───────────────────────────────────────────

// fwDetailsJS is JSON.stringify(details) of the original's PromptPermissionDetails: the keys it sets, in its order.
func fwDetailsJS(d *PromptPermissionDetails) *jsObject {
	o := newObject()
	o.set("requestId", d.RequestID)
	o.set("source", d.Source)
	o.set("agentName", strOrNull(d.AgentName))
	o.set("payload", d.Payload.JS())
	o.set("surface", strOrNull(d.Surface))
	o.set("value", strOrNull(d.Value))
	if d.Forwarding != nil {
		f := newObject()
		f.set("requesterAgentName", strOrNull(d.Forwarding.RequesterAgentName))
		f.set("requesterSessionId", strOrNull(d.Forwarding.RequesterSessionID))
		o.set("forwarding", f)
	}
	if d.SessionApproval != nil {
		grants := make([]any, len(d.SessionApproval.Grants))
		for i, g := range d.SessionApproval.Grants {
			grants[i] = fwObj("surface", g.Surface, "pattern", g.Pattern)
		}
		o.set("sessionApproval", fwObj("grants", grants))
	}
	if d.AccessIntent != nil {
		o.set("accessIntent", d.AccessIntent.js())
	}
	return o
}

func fwDecisionFromJS(o *jsObject) PermissionPromptDecision {
	d := PermissionPromptDecision{}
	d.Approved, _ = o.flag("approved")
	d.State = PermissionDecisionState(fwStrOf(o, "state"))
	if r, ok := o.str("denialReason"); ok {
		d.DenialReason = &r
	}
	d.ConfirmationUnavailable, _ = o.flag("confirmationUnavailable")
	d.SessionGrantWidth = SessionGrantWidth(fwStrOf(o, "sessionGrantWidth"))
	d.DecidedBy = *AsDecisionSource(o.vals["decidedBy"])
	return d
}

var fwRespondedAt = regexp.MustCompile(`"respondedAt":\d+`)

func TestForwardingOracleServer(t *testing.T) {
	scenarios := fwList(fwOracle(t), "server")
	if len(scenarios) < 800 {
		t.Fatalf("only %d scenarios", len(scenarios))
	}
	for _, c := range scenarios {
		name := fwStrOf(c, "name")
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			forwardingRoot := filepath.Join(dir, "forwarding")
			location, err := CreatePermissionForwardingLocation(forwardingRoot, "parent-session")
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range []string{location.RequestsDir, location.ResponsesDir} {
				if err := os.MkdirAll(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for _, f := range fwList(c, "files") {
				if err := os.WriteFile(filepath.Join(location.RequestsDir, fwStrOf(f, "name")), []byte(fwStrOf(f, "raw")), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			logger := &fwLogger{}
			var policyCalls, escalated []any
			var recorded []any
			policy := &fwPolicyFake{fn: func(intent ForwardedAccessIntent) PermissionCheckResult {
				policyCalls = append(policyCalls, intent.JS())
				p := fwCheck("ask")
				if po := c.obj("policy"); po != nil {
					p.State = fwStrOf(po, "state")
					if v, ok := po.str("matchedPattern"); ok {
						p.MatchedPattern = &v
					}
					if v, ok := po.str("reason"); ok {
						p.Reason = &v
					}
					if v, ok := po.str("origin"); ok {
						p.Origin = &v
					}
				}
				return p
			}}
			esc := c.obj("escalator")
			escalator := &fwEscalatorFake{fn: func(d *PromptPermissionDetails) (PermissionPromptDecision, error) {
				escalated = append(escalated, fwDetailsJS(d))
				if msg, throws := esc.str("throw"); throws {
					return PermissionPromptDecision{}, errors.New(msg)
				}
				return fwDecisionFromJS(esc.obj("decision")), nil
			}}
			broadcaster := &fwBroadcasterFake{}
			recorder := &fwRecordingFake{record: func(a *SessionApproval) {
				grants := make([]any, len(a.Grants))
				for i, g := range a.Grants {
					grants[i] = fwObj("surface", g.Surface, "pattern", g.Pattern)
				}
				recorded = append(recorded, grants)
			}}
			NewForwardedRequestServer(ForwardedRequestServerDeps{
				ForwardingDir: forwardingRoot, Logger: logger, Policy: policy, Escalator: escalator, Broadcaster: broadcaster, Recorder: recorder,
			}).ProcessInbox(fwMakeForwarderContext(true, "parent-session"))

			want := c.obj("result")
			norm := func(s string) string { return strings.ReplaceAll(s, dir, "<DIR>") }

			// The response files, byte for byte (the read time aside).
			wantResponses := want.obj("responses")
			entries, _ := os.ReadDir(location.ResponsesDir)
			var gotNames []string
			for _, e := range entries {
				gotNames = append(gotNames, e.Name())
			}
			if strings.Join(gotNames, "\x00") != strings.Join(wantResponses.keys, "\x00") {
				t.Fatalf("response files %q, the original %q", gotNames, wantResponses.keys)
			}
			for _, n := range gotNames {
				raw, _ := os.ReadFile(filepath.Join(location.ResponsesDir, n))
				if got, w := fwRespondedAt.ReplaceAllString(string(raw), `"respondedAt":"NOW"`), fwStrOf(wantResponses, n); got != w {
					t.Errorf("%s:\n got  %s\n want %s", n, got, w)
				}
			}
			// What the drain left behind.
			var left []string
			if entries, err := os.ReadDir(location.RequestsDir); err == nil {
				for _, e := range entries {
					left = append(left, e.Name())
				}
			}
			var wantLeft []string
			if l, ok := want.vals["requestsLeft"].([]any); ok {
				for _, v := range l {
					wantLeft = append(wantLeft, v.(string))
				}
			}
			if strings.Join(left, "\x00") != strings.Join(wantLeft, "\x00") {
				t.Errorf("requests left %q, the original %q", left, wantLeft)
			}
			if b, _ := want.flag("responsesDirExists"); b != pathExists(location.ResponsesDir) {
				t.Errorf("responses dir exists %v, the original %v", pathExists(location.ResponsesDir), b)
			}
			if b, _ := want.flag("sessionRootExists"); b != pathExists(location.SessionRootDir) {
				t.Errorf("session root exists %v, the original %v", pathExists(location.SessionRootDir), b)
			}
			// The details escalated, the intents the policy saw, the broadcast and the recorded grants.
			fwCompareList(t, "escalated", norm, escalated, want.vals["escalated"])
			fwCompareList(t, "policyCalls", norm, policyCalls, want.vals["policyCalls"])
			fwCompareList(t, "recorded", norm, recorded, want.vals["recorded"])
			var broadcast []any
			for _, e := range broadcaster.events {
				b, _ := json.Marshal(e)
				v, _ := parseJSON(b)
				broadcast = append(broadcast, v)
			}
			// The event is the logging lane's struct, whose field order is its own: the bus carries JSON objects, so compare as objects.
			wantBroadcast, _ := want.vals["broadcast"].([]any)
			for i := range broadcast {
				broadcast[i] = fwCanon(broadcast[i])
			}
			for i := range wantBroadcast {
				wantBroadcast[i] = fwCanon(wantBroadcast[i])
			}
			fwCompareList(t, "broadcast", norm, broadcast, wantBroadcast)
			// Every review and debug entry, with its keys in order.
			toPairs := func(calls []fwLogCall) []any {
				out := make([]any, len(calls))
				for i, c := range calls {
					out[i] = []any{c.event, c.details}
				}
				return out
			}
			fwCompareList(t, "review", norm, toPairs(logger.reviews()), want.vals["review"])
			fwCompareList(t, "debug", norm, toPairs(logger.debugs()), want.vals["debug"])
		})
	}
}

type fwRecordingFake struct{ record func(*SessionApproval) }

func (r *fwRecordingFake) RecordSessionApproval(a *SessionApproval) { r.record(a) }

// fwCompareList compares the port's entries with the original's as ordered JSON text (key order included), after norm.
func fwCompareList(t *testing.T, what string, norm func(string) string, got []any, want any) {
	t.Helper()
	wantList, _ := want.([]any)
	if len(got) != len(wantList) {
		t.Errorf("%s: %d entries, the original %d\n got  %s\n want %s", what, len(got), len(wantList), norm(marshalJSON(got, "")), marshalJSON(wantList, ""))
		return
	}
	for i := range got {
		if g, w := norm(marshalJSON(got[i], "")), marshalJSON(wantList[i], ""); g != w {
			t.Errorf("%s[%d]:\n got  %s\n want %s", what, i, g, w)
		}
	}
}
