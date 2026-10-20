package pi_permission_system

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

// Twins of test/authority/decision-source.test.ts, decision-resolution.test.ts, permission-dialog.test.ts and
// permission-prompt-decision.test.ts.

func srcUser(via string) DecisionSource { return DecisionSource{Kind: "user", Via: via} }

// nestSource wraps inner in depth nested forwarded frames.
func nestSource(depth int, inner DecisionSource) DecisionSource {
	source := inner
	for i := 0; i < depth; i++ {
		inner := source
		source = DecisionSource{Kind: "forwarded", ResponderSessionID: sp(fmt.Sprintf("session-%d", i)), Decision: &inner}
	}
	return source
}

func roundTrip(t *testing.T, v DecisionSource) *DecisionSource {
	t.Helper()
	parsed, err := parseJSON([]byte(marshalJSON(v.JS(), "")))
	if err != nil {
		t.Fatal(err)
	}
	return AsDecisionSource(parsed)
}

func TestDecisionSourceTwins(t *testing.T) {
	f := "authority/decision-source"
	variants := []DecisionSource{
		srcUser("dialog"), srcUser("select"),
		{Kind: "authorizer", Name: "model-judge", Verdict: "deny", Reason: sp("reads outside the project")},
		{Kind: "authorizer", Name: "model-judge", Verdict: "allow"},
		{Kind: "rule", Surface: "external_directory", Pattern: sp("/tmp/*"), Origin: sp("global")},
		{Kind: "rule", Surface: "bash"},
		{Kind: "session_approval", Surface: "external_directory", Pattern: sp("/tmp/*")},
		{Kind: "session_approval", Surface: "bash"},
		{Kind: "yolo", Pattern: sp("<opaque-bash-wrapper>")},
		{Kind: "yolo"},
		{Kind: "infrastructure_read"},
		{Kind: "unavailable", Reason: sp("Session 'abc' did not answer within 600s")},
		{Kind: "gate_error", Reason: sp("boom")},
		{Kind: "forwarded", ResponderSessionID: sp("019ff969-c34c-70be-9034-fae19c852932"), Decision: &DecisionSource{Kind: "user", Via: "dialog"}},
		{Kind: "forwarded"},
	}
	for _, v := range variants {
		j, _ := json.Marshal(v.JS())
		tw(t, f, "admits ${variant.kind} (${JSON.stringify(variant)})", func(t *testing.T) {
			got := roundTrip(t, v)
			if got == nil || !reflect.DeepEqual(*got, v) {
				t.Errorf("%s: got %+v", j, got)
			}
		})
	}

	// The upstream it.each over malformed values has no ledger title of its own (its titles are "rejects %s"); every row is run.
	malformed := map[string]string{
		"null": `null`, "a string": `"user"`, "an array": `[{"kind":"user","via":"dialog"}]`,
		"an unknown kind": `{"kind":"telepathy"}`, "a missing kind": `{"via":"dialog"}`,
		"an unknown user surface": `{"kind":"user","via":"smoke-signal"}`, "a missing user surface": `{"kind":"user"}`,
		"an unknown authorizer verdict":              `{"kind":"authorizer","name":"j","verdict":"defer","reason":null}`,
		"a missing authorizer name":                  `{"kind":"authorizer","verdict":"allow","reason":null}`,
		"a non-string authorizer name":               `{"kind":"authorizer","name":7,"verdict":"allow","reason":null}`,
		"a missing rule surface":                     `{"kind":"rule","pattern":null,"origin":null}`,
		"a non-nullable-string rule pattern":         `{"kind":"rule","surface":"bash","pattern":7,"origin":null}`,
		"a missing session_approval surface":         `{"kind":"session_approval","pattern":null}`,
		"a missing yolo pattern":                     `{"kind":"yolo"}`,
		"a missing unavailable reason":               `{"kind":"unavailable"}`,
		"a non-string gate_error reason":             `{"kind":"gate_error","reason":null}`,
		"a missing forwarded decision":               `{"kind":"forwarded","responderSessionId":"s"}`,
		"a non-nullable-string responderSessionId":   `{"kind":"forwarded","responderSessionId":7,"decision":null}`,
	}
	for label, raw := range malformed {
		t.Run("rejects "+label, func(t *testing.T) {
			if got := AsDecisionSource(fwParse(t, raw)); got != nil {
				t.Errorf("admitted %+v", got)
			}
		})
	}

	tw(t, f, "rejects the whole value when a nested decision is malformed", func(t *testing.T) {
		// All-or-nothing: a half-parsed provenance record would assert a decider that never decided.
		if got := AsDecisionSource(fwParse(t, `{"kind":"forwarded","responderSessionId":"session-1","decision":{"kind":"user","via":"smoke-signal"}}`)); got != nil {
			t.Errorf("admitted %+v", got)
		}
	})
	tw(t, f, "drops unknown properties rather than rejecting", func(t *testing.T) {
		got := AsDecisionSource(fwParse(t, `{"kind":"user","via":"dialog","clicks":2}`))
		if got == nil || !reflect.DeepEqual(*got, srcUser("dialog")) {
			t.Errorf("got %+v", got)
		}
	})
	tw(t, f, "admits nesting up to the bound", func(t *testing.T) {
		source := nestSource(MaxDecisionSourceDepth, srcUser("dialog"))
		got := roundTrip(t, source)
		if got == nil || !reflect.DeepEqual(*got, source) {
			t.Errorf("got %+v", got)
		}
	})
	tw(t, f, "rejects nesting past the bound", func(t *testing.T) {
		if got := roundTrip(t, nestSource(MaxDecisionSourceDepth+1, srcUser("dialog"))); got != nil {
			t.Errorf("admitted %+v", got)
		}
	})

	// effectiveDecider: a source that decided here (upstream it.each "returns a %s source unchanged").
	for _, s := range []DecisionSource{
		srcUser("dialog"),
		{Kind: "authorizer", Name: "model-judge", Verdict: "deny", Reason: sp("outside the project")},
		{Kind: "rule", Surface: "bash", Pattern: sp("*")},
		{Kind: "yolo"}, {Kind: "unavailable", Reason: sp("nobody answered")}, {Kind: "gate_error", Reason: sp("boom")},
	} {
		t.Run("returns a "+s.Kind+" source unchanged", func(t *testing.T) { eq(t, EffectiveDecider(s), s, "decider") })
	}
	tw(t, f, "unwraps one hop to the decider inside the responding session", func(t *testing.T) {
		inner := DecisionSource{Kind: "rule", Surface: "external_directory", Pattern: sp("/tmp/*"), Origin: sp("global")}
		eq(t, EffectiveDecider(nestSource(1, inner)), inner, "decider")
	})
	tw(t, f, "unwraps a relay hop to the innermost decider", func(t *testing.T) {
		inner := DecisionSource{Kind: "authorizer", Name: "model-judge", Verdict: "allow"}
		eq(t, EffectiveDecider(nestSource(2, inner)), inner, "decider")
	})
	tw(t, f, "returns the innermost hop when a relayed responder named no decider", func(t *testing.T) {
		inner := DecisionSource{Kind: "forwarded", ResponderSessionID: sp("grandparent-1")}
		eq(t, EffectiveDecider(nestSource(1, inner)), inner, "decider")
	})
	tw(t, f, "returns the hop itself when the responder named no decider", func(t *testing.T) {
		hop := DecisionSource{Kind: "forwarded", ResponderSessionID: sp("session-1")}
		eq(t, EffectiveDecider(hop), hop, "decider")
	})
	tw(t, f, "stops at the nesting bound rather than descending without limit", func(t *testing.T) {
		if got := EffectiveDecider(nestSource(MaxDecisionSourceDepth+2, srcUser("dialog"))).Kind; got != "forwarded" {
			t.Errorf("kind %q", got)
		}
	})
}

func TestDecisionResolutionTwins(t *testing.T) {
	f := "authority/decision-resolution"
	allowed, allowedForSession, refused := DecisionOutcome{Approved: true}, DecisionOutcome{Approved: true, ForSession: true}, DecisionOutcome{}
	check := func(t *testing.T, s DecisionSource, o DecisionOutcome, want PermissionDecisionResolution) {
		t.Helper()
		if got := ResolutionFor(s, o); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
	rule := DecisionSource{Kind: "rule", Surface: "bash", Pattern: sp("git *"), Origin: sp("project")}
	tw(t, f, "names a rule allow policy_allow", func(t *testing.T) { check(t, rule, allowed, "policy_allow") })
	tw(t, f, "names a rule denial policy_deny", func(t *testing.T) { check(t, rule, refused, "policy_deny") })
	tw(t, f, "names a session grant session_approved", func(t *testing.T) {
		check(t, DecisionSource{Kind: "session_approval", Surface: "bash", Pattern: sp("git *")}, allowed, "session_approved")
	})
	tw(t, f, "names an infrastructure read infrastructure_auto_allowed", func(t *testing.T) {
		check(t, DecisionSource{Kind: "infrastructure_read"}, allowed, "infrastructure_auto_allowed")
	})
	tw(t, f, "names a yolo grant auto_approved", func(t *testing.T) {
		check(t, DecisionSource{Kind: "yolo", Pattern: sp("<opaque-bash-wrapper>")}, allowed, "auto_approved")
	})
	human := srcUser("dialog")
	tw(t, f, "names a one-off approval user_approved", func(t *testing.T) { check(t, human, allowed, "user_approved") })
	tw(t, f, "names a session-scoped approval user_approved_for_session", func(t *testing.T) {
		check(t, human, allowedForSession, "user_approved_for_session")
	})
	tw(t, f, "names a denial user_denied", func(t *testing.T) { check(t, human, refused, "user_denied") })
	link := func(verdict string) DecisionSource {
		return DecisionSource{Kind: "authorizer", Name: "model-judge", Verdict: verdict}
	}
	tw(t, f, "names a link grant authorizer_allowed, not the user's approval", func(t *testing.T) {
		check(t, link("allow"), allowed, "authorizer_allowed")
	})
	tw(t, f, "names a link denial authorizer_denied, not the user's denial", func(t *testing.T) {
		check(t, link("deny"), refused, "authorizer_denied")
	})
	tw(t, f, "does not report a link grant as session-scoped", func(t *testing.T) {
		// A link grant is non-persistent by ADR 0007's envelope, so the caller's forSession bit cannot make one look like a standing
		// approval.
		check(t, link("allow"), allowedForSession, "authorizer_allowed")
	})
	tw(t, f, "names an unreachable authority confirmation_unavailable", func(t *testing.T) {
		check(t, DecisionSource{Kind: "unavailable", Reason: sp("no serving session")}, refused, "confirmation_unavailable")
	})
	tw(t, f, "names a failed gate gate_error", func(t *testing.T) {
		check(t, DecisionSource{Kind: "gate_error", Reason: sp("boom")}, refused, "gate_error")
	})
	forwarded := func(decision *DecisionSource) DecisionSource {
		return DecisionSource{Kind: "forwarded", ResponderSessionID: sp("parent-1"), Decision: decision}
	}
	tw(t, f, "names the parent's rule allow policy_allow, not the user's approval", func(t *testing.T) {
		check(t, forwarded(&DecisionSource{Kind: "rule", Surface: "external_directory", Pattern: sp("/tmp/*"), Origin: sp("global")}), allowed, "policy_allow")
	})
	tw(t, f, "names the parent's link denial authorizer_denied", func(t *testing.T) {
		check(t, forwarded(&DecisionSource{Kind: "authorizer", Name: "model-judge", Verdict: "deny", Reason: sp("reads outside the project")}), refused, "authorizer_denied")
	})
	tw(t, f, "still names the parent's human approval user_approved", func(t *testing.T) {
		check(t, forwarded(&human), allowed, "user_approved")
	})
	tw(t, f, "carries the grant scope through the hop", func(t *testing.T) {
		check(t, forwarded(&human), allowedForSession, "user_approved_for_session")
	})
	tw(t, f, "falls back to the user attribution when the responder named no decider", func(t *testing.T) {
		// A responder predating decision provenance sends none, so the hop is the only fact available.
		check(t, forwarded(nil), allowed, "user_approved")
		check(t, forwarded(nil), refused, "user_denied")
	})
}

// dialogUI is a PermissionDecisionUi double: scripted select answers (ok false is undefined), the one input answer, and the calls.
type dialogUI struct {
	selects      []dialogAnswer
	input        dialogAnswer
	selectTitles []string
	selectOpts   [][]string
	inputCalls   int
}

type dialogAnswer struct {
	value string
	ok    bool
}

func answer(s string) dialogAnswer { return dialogAnswer{s, true} }

var undefinedAnswer = dialogAnswer{}

func (u *dialogUI) Select(title string, options []string) (string, bool, error) {
	u.selectTitles = append(u.selectTitles, title)
	u.selectOpts = append(u.selectOpts, options)
	if len(u.selects) == 0 {
		return "", false, nil
	}
	a := u.selects[0]
	u.selects = u.selects[1:]
	return a.value, a.ok, nil
}

func (u *dialogUI) Input(string, string) (string, bool, error) {
	u.inputCalls++
	return u.input.value, u.input.ok, nil
}

func requestDialog(t *testing.T, ui *dialogUI, options *RequestPermissionOptions) UnattributedDecision {
	t.Helper()
	d, err := RequestPermissionDecisionFromUi(ui, "Title", "Message", options)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestPermissionDialogTwins(t *testing.T) {
	f := "authority/permission-dialog"
	for _, c := range []struct {
		title string
		v     any
		want  bool
	}{
		{"accepts approved", "approved", true}, {"accepts denied", "denied", true}, {"accepts denied_with_reason", "denied_with_reason", true},
		{"accepts approved_for_session", "approved_for_session", true},
		{"accepts approved_for_serving_session", "approved_for_serving_session", true},
		{"rejects unknown strings", "unknown", false},
	} {
		tw(t, f, c.title, func(t *testing.T) {
			if IsPermissionDecisionState(c.v) != c.want {
				t.Error("wrong answer")
			}
		})
	}
	tw(t, f, "rejects non-strings", func(t *testing.T) {
		if IsPermissionDecisionState(42.0) || IsPermissionDecisionState(nil) {
			t.Error("accepted a non-string")
		}
	})

	approved := UnattributedDecision{Approved: true, State: DecisionApproved}
	forSession := UnattributedDecision{Approved: true, State: DecisionApprovedForSession}
	denied := UnattributedDecision{State: DecisionDenied}
	tw(t, f, "returns approved when user selects Yes", func(t *testing.T) {
		eq(t, requestDialog(t, &dialogUI{selects: []dialogAnswer{answer("Yes")}}, nil), approved, "decision")
	})
	tw(t, f, "returns approved_for_session when user selects session option", func(t *testing.T) {
		eq(t, requestDialog(t, &dialogUI{selects: []dialogAnswer{answer("Yes, for this session")}}, nil), forSession, "decision")
	})
	tw(t, f, "returns denied when user selects No", func(t *testing.T) {
		eq(t, requestDialog(t, &dialogUI{selects: []dialogAnswer{answer("No")}}, nil), denied, "decision")
	})
	tw(t, f, "returns denied_with_reason when user provides reason", func(t *testing.T) {
		ui := &dialogUI{selects: []dialogAnswer{answer("No, provide reason")}, input: answer("not now")}
		eq(t, requestDialog(t, ui, nil), UnattributedDecision{State: DecisionDeniedWithReason, DenialReason: sp("not now")}, "decision")
	})
	tw(t, f, "returns denied when user selects deny-with-reason but gives empty input", func(t *testing.T) {
		ui := &dialogUI{selects: []dialogAnswer{answer("No, provide reason")}, input: answer("")}
		eq(t, requestDialog(t, ui, nil), denied, "decision")
	})
	tw(t, f, "returns denied when user dismisses dialog (undefined)", func(t *testing.T) {
		eq(t, requestDialog(t, &dialogUI{selects: []dialogAnswer{undefinedAnswer}}, nil), denied, "decision")
	})
	tw(t, f, "passes four options to ui.select", func(t *testing.T) {
		ui := &dialogUI{selects: []dialogAnswer{answer("Yes")}}
		requestDialog(t, ui, nil)
		eq(t, ui.selectOpts[0], []string{"Yes", "Yes, for this session", "No", "No, provide reason"}, "options")
		eq(t, ui.selectTitles[0], "Title\nMessage", "title")
	})
	tw(t, f, "uses custom sessionLabel when provided", func(t *testing.T) {
		ui := &dialogUI{selects: []dialogAnswer{answer("Yes")}}
		requestDialog(t, ui, &RequestPermissionOptions{SessionLabel: sp(`Yes, allow "git *" for this session`)})
		eq(t, ui.selectOpts[0][1], `Yes, allow "git *" for this session`, "label")
	})
	tw(t, f, "still returns approved_for_session when user selects the custom session label", func(t *testing.T) {
		label := `Yes, allow "git *" for this session`
		eq(t, requestDialog(t, &dialogUI{selects: []dialogAnswer{answer(label)}}, &RequestPermissionOptions{SessionLabel: &label}), forSession, "decision")
	})
	tw(t, f, "falls back to default session label when no options provided", func(t *testing.T) {
		ui := &dialogUI{selects: []dialogAnswer{answer("Yes")}}
		requestDialog(t, ui, nil)
		eq(t, ui.selectOpts[0][1], "Yes, for this session", "label")
	})

	width := &SessionWidthOption{Label: `Yes, allow reads and writes to "/tmp/*" for this session`}
	scope := &SessionScopeOption{SubagentLabel: "This subagent only", ServingSessionLabel: "The whole session"}
	family := UnattributedDecision{Approved: true, State: DecisionApprovedForSession, SessionGrantWidth: "family"}
	tw(t, f, "offers the width option after the session option", func(t *testing.T) {
		ui := &dialogUI{selects: []dialogAnswer{answer("Yes")}}
		requestDialog(t, ui, &RequestPermissionOptions{SessionLabel: sp(`Yes, allow writes to "/tmp/*" for this session`), SessionWidth: width})
		eq(t, ui.selectOpts[0], []string{"Yes", `Yes, allow writes to "/tmp/*" for this session`, width.Label, "No", "No, provide reason"}, "options")
	})
	tw(t, f, "returns the family width when the width option is chosen", func(t *testing.T) {
		eq(t, requestDialog(t, &dialogUI{selects: []dialogAnswer{answer(width.Label)}}, &RequestPermissionOptions{SessionWidth: width}), family, "decision")
	})
	tw(t, f, "leaves the plain session option at the proven width", func(t *testing.T) {
		eq(t, requestDialog(t, &dialogUI{selects: []dialogAnswer{answer("Yes, for this session")}}, &RequestPermissionOptions{SessionWidth: width}), forSession, "decision")
	})
	tw(t, f, "continues into the scope select from the width option", func(t *testing.T) {
		ui := &dialogUI{selects: []dialogAnswer{answer(width.Label), answer("The whole session")}}
		got := requestDialog(t, ui, &RequestPermissionOptions{SessionWidth: width, SessionScope: scope})
		eq(t, len(ui.selectOpts), 2, "selects")
		eq(t, got, UnattributedDecision{Approved: true, State: DecisionApprovedForServingSess, SessionGrantWidth: "family"}, "decision")
	})
	tw(t, f, "opens a second scope select after the session option is chosen", func(t *testing.T) {
		ui := &dialogUI{selects: []dialogAnswer{answer("Yes, for this session"), answer("This subagent only")}}
		requestDialog(t, ui, &RequestPermissionOptions{SessionScope: scope})
		eq(t, len(ui.selectOpts), 2, "selects")
		eq(t, ui.selectOpts[1], []string{"This subagent only", "The whole session"}, "scope options")
		eq(t, ui.selectTitles[1], "Title\nApply this session grant to:", "scope title")
	})
	tw(t, f, "maps the subagent scope to approved_for_session", func(t *testing.T) {
		ui := &dialogUI{selects: []dialogAnswer{answer("Yes, for this session"), answer("This subagent only")}}
		eq(t, requestDialog(t, ui, &RequestPermissionOptions{SessionScope: scope}), forSession, "decision")
	})
	tw(t, f, "maps the whole-session scope to approved_for_serving_session", func(t *testing.T) {
		ui := &dialogUI{selects: []dialogAnswer{answer("Yes, for this session"), answer("The whole session")}}
		eq(t, requestDialog(t, ui, &RequestPermissionOptions{SessionScope: scope}), UnattributedDecision{Approved: true, State: DecisionApprovedForServingSess}, "decision")
	})
	tw(t, f, "defaults to the least-privilege subagent scope when the scope select is cancelled", func(t *testing.T) {
		ui := &dialogUI{selects: []dialogAnswer{answer("Yes, for this session"), undefinedAnswer}}
		eq(t, requestDialog(t, ui, &RequestPermissionOptions{SessionScope: scope}), forSession, "decision")
	})
	tw(t, f, "does not open the scope select when the user picks plain Yes", func(t *testing.T) {
		ui := &dialogUI{selects: []dialogAnswer{answer("Yes")}}
		got := requestDialog(t, ui, &RequestPermissionOptions{SessionScope: scope})
		eq(t, len(ui.selectOpts), 1, "selects")
		eq(t, got, approved, "decision")
	})
	tw(t, f, "returns trimmed string for non-empty input", func(t *testing.T) {
		if got := NormalizePermissionDenialReason(sp("  reason  ")); got == nil || *got != "reason" {
			t.Errorf("got %v", got)
		}
	})
	tw(t, f, "returns undefined for empty string", func(t *testing.T) {
		if NormalizePermissionDenialReason(sp("")) != nil {
			t.Error("not undefined")
		}
	})
	tw(t, f, "returns undefined for non-string", func(t *testing.T) {
		// A non-string is a nil *string in the typed port: the only non-string value a caller can hold.
		if NormalizePermissionDenialReason(nil) != nil {
			t.Error("not undefined")
		}
	})
	tw(t, f, "returns denied_with_reason when reason provided", func(t *testing.T) {
		eq(t, CreateDeniedPermissionDecision(sp("nope")), UnattributedDecision{State: DecisionDeniedWithReason, DenialReason: sp("nope")}, "decision")
	})
	tw(t, f, "returns denied when no reason", func(t *testing.T) {
		eq(t, CreateDeniedPermissionDecision(nil), denied, "decision")
	})
}
