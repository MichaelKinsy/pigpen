package pi_permission_system

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// Differential test of the authority layer against the original's own modules. testdata/authority_oracle.json is what
// port/probe/authority-probe.test.ts recorded from the unmodified original (port/record-authority-probe.sh, inputs from
// port/probe/gen-authority-cases.py): the dialog model over 7,200 event sequences, the select/input fallback over 975 scripted
// answers, the chain over 940 verdict stacks (including links that reject or answer undefined), bracketed-paste collapsing, the request options, notice and ui_prompt broadcast of 458
// asks, the prompter's review entries, 72 selections, buildUiPrompt, 368 inline-component scenarios (every frame at two widths, the
// render requests, tool expansion toggles, the terminal writes and the decision) and the fallback flow.

func authOracle(t *testing.T) *jsObject {
	t.Helper()
	data, err := os.ReadFile("testdata/authority_oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	v, err := parseJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	return v.(*jsObject)
}

func authCases(t *testing.T) *jsObject {
	t.Helper()
	data, err := os.ReadFile("../../port/probe/authority-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	v, err := parseJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	return v.(*jsObject)
}

func listOf(o *jsObject, key string) []any {
	l, _ := o.vals[key].([]any)
	return l
}

func optStr(o *jsObject, key string) *string {
	if s, ok := o.str(key); ok {
		return &s
	}
	return nil
}

func strList(v any) []string {
	var out []string
	for _, e := range v.([]any) {
		out = append(out, e.(string))
	}
	return out
}

// detailsFromJS builds the details of a case: a null member is absent, as every reader treats it.
func detailsFromJS(t *testing.T, o *jsObject) *PromptPermissionDetails {
	t.Helper()
	payload := AsPromptPayload(o.vals["payload"])
	if payload == nil {
		t.Fatalf("bad payload in %s", marshalJSON(o, ""))
	}
	d := &PromptPermissionDetails{
		RequestID: fwStrOf(o, "requestId"), Source: fwStrOf(o, "source"), AgentName: optStr(o, "agentName"), Payload: *payload,
		ToolCallID: optStr(o, "toolCallId"), ToolName: optStr(o, "toolName"), SkillName: optStr(o, "skillName"), Path: optStr(o, "path"),
		Command: optStr(o, "command"), Target: optStr(o, "target"), ToolInputPreview: optStr(o, "toolInputPreview"), SessionLabel: optStr(o, "sessionLabel"),
		Surface: optStr(o, "surface"), Value: optStr(o, "value"),
	}
	if f := o.obj("forwarding"); f != nil {
		d.Forwarding = &ForwardedAskProvenance{RequesterAgentName: optStr(f, "requesterAgentName"), RequesterSessionID: optStr(f, "requesterSessionId")}
	}
	if s := o.obj("sessionApproval"); s != nil {
		approval := &ForwardedSessionApproval{Grants: []ApprovalGrant{}}
		for _, g := range listOf(s, "grants") {
			go_ := g.(*jsObject)
			approval.Grants = append(approval.Grants, ApprovalGrant{Surface: fwStrOf(go_, "surface"), Pattern: fwStrOf(go_, "pattern")})
		}
		d.SessionApproval = approval
	}
	return d
}

// decisionJS is the decision as JSON.stringify writes it (undefined members dropped).
func decisionJS(d PermissionPromptDecision) *jsObject {
	o := newObject()
	o.set("approved", d.Approved)
	o.set("state", string(d.State))
	if d.DenialReason != nil {
		o.set("denialReason", *d.DenialReason)
	}
	if d.ConfirmationUnavailable {
		o.set("confirmationUnavailable", true)
	}
	if d.SessionGrantWidth != "" {
		o.set("sessionGrantWidth", string(d.SessionGrantWidth))
	}
	o.set("decidedBy", d.DecidedBy.JS())
	return o
}

// unattributedJS is an unattributed decision as JSON.stringify writes it.
func unattributedJS(d UnattributedDecision) *jsObject {
	o := newObject()
	o.set("approved", d.Approved)
	o.set("state", string(d.State))
	if d.DenialReason != nil {
		o.set("denialReason", *d.DenialReason)
	}
	if d.ConfirmationUnavailable {
		o.set("confirmationUnavailable", true)
	}
	if d.SessionGrantWidth != "" {
		o.set("sessionGrantWidth", string(d.SessionGrantWidth))
	}
	return o
}

func unattributedKey(d UnattributedDecision) string {
	reason, width, unavailable := "null", "null", "null"
	if d.DenialReason != nil {
		reason = marshalJSON(*d.DenialReason, "")
	}
	if d.SessionGrantWidth != "" {
		width = marshalJSON(string(d.SessionGrantWidth), "")
	}
	if d.ConfirmationUnavailable {
		unavailable = "true"
	}
	return fmt.Sprintf("[%t,%q,%s,%s,%s]", d.Approved, string(d.State), reason, width, unavailable)
}

func optionsJS(o *RequestPermissionOptions) any {
	if o == nil {
		return nil
	}
	out := newObject()
	if o.SessionLabel != nil {
		out.set("sessionLabel", *o.SessionLabel)
	}
	if o.SessionWidth != nil {
		out.set("sessionWidth", fwObj("label", o.SessionWidth.Label))
	}
	if o.SessionScope != nil {
		out.set("sessionScope", fwObj("subagentLabel", o.SessionScope.SubagentLabel, "servingSessionLabel", o.SessionScope.ServingSessionLabel))
	}
	return out
}

func optionsFromJS(v any) *RequestPermissionOptions {
	o, ok := v.(*jsObject)
	if !ok {
		return nil
	}
	out := &RequestPermissionOptions{SessionLabel: optStr(o, "sessionLabel")}
	if w := o.obj("sessionWidth"); w != nil {
		out.SessionWidth = &SessionWidthOption{Label: fwStrOf(w, "label")}
	}
	if s := o.obj("sessionScope"); s != nil {
		out.SessionScope = &SessionScopeOption{SubagentLabel: fwStrOf(s, "subagentLabel"), ServingSessionLabel: fwStrOf(s, "servingSessionLabel")}
	}
	return out
}

var oracleKeysets = map[string]DialogKeyBindings{
	"default": defaultDialogKeys(),
	"digits":  {ActionApprove: "1", ActionApproveSession: "2", ActionApproveSessionBoth: "3", ActionDeny: "4", ActionDenyWithReason: "5"},
	"swapped": {ActionApprove: "n", ActionApproveSession: "s", ActionApproveSessionBoth: "b", ActionDeny: "y", ActionDenyWithReason: "r"},
}

func TestAuthorityOracleModel(t *testing.T) {
	cases, want := listOf(authCases(t), "model"), listOf(authOracle(t), "model")
	if len(cases) < 7000 || len(cases) != len(want) {
		t.Fatalf("%d cases, %d recorded", len(cases), len(want))
	}
	for i, raw := range cases {
		c := raw.(*jsObject)
		cfg := c.obj("config")
		config := PromptModelConfig{SessionLabel: "Yes, for this session", Keys: oracleKeysets[fwStrOf(cfg, "keys")]}
		config.DoublePressToConfirm, _ = cfg.flag("double")
		config.WidthLabel = fwStrOf(cfg, "widthLabel")
		if s := cfg.obj("scope"); s != nil {
			config.SessionScope = &SessionScopeOption{SubagentLabel: fwStrOf(s, "subagentLabel"), ServingSessionLabel: fwStrOf(s, "servingSessionLabel")}
		}
		state := InitialPromptState(config)
		var got []string
		for _, e := range listOf(c, "events") {
			eo := e.(*jsObject)
			event := PromptEvent{Type: PromptEventType(fwStrOf(eo, "type")), Direction: fwStrOf(eo, "direction"), Action: PromptAction(fwStrOf(eo, "action")), Draft: fwStrOf(eo, "draft")}
			outcome := ReducePrompt(config, state, event)
			if outcome.Decided {
				got = append(got, "D|"+unattributedKey(outcome.Decision))
				break
			}
			state = outcome.State
			scopeServing := 0
			if state.ScopeServing {
				scopeServing = 1
			}
			got = append(got, strings.Join([]string{"R", string(state.Step), string(state.HighlightedAction), string(state.ArmedAction), state.Hint, state.ReasonError, fmt.Sprint(scopeServing), string(state.GrantWidth)}, "|"))
		}
		if fmt.Sprint(got) != fmt.Sprint(strList(want[i])) {
			t.Fatalf("case %d (%s): got %v, original %v", i, marshalJSON(c, ""), got, strList(want[i]))
		}
	}
}

func TestAuthorityOracleDialog(t *testing.T) {
	cases, want := listOf(authCases(t), "dialog"), listOf(authOracle(t), "dialog")
	if len(cases) < 900 || len(cases) != len(want) {
		t.Fatalf("%d cases, %d recorded", len(cases), len(want))
	}
	for i, raw := range cases {
		c, w := raw.(*jsObject), want[i].(*jsObject)
		var selects []dialogAnswer
		for _, s := range listOf(c, "selects") {
			if str, ok := s.(string); ok {
				selects = append(selects, answer(str))
			} else {
				selects = append(selects, undefinedAnswer)
			}
		}
		ui := &dialogUI{selects: selects}
		if in, ok := c.vals["input"].(string); ok {
			ui.input = answer(in)
		}
		decision, err := RequestPermissionDecisionFromUi(ui, "Title", "Message", optionsFromJS(c.vals["options"]))
		if err != nil {
			t.Fatal(err)
		}
		if got := unattributedKey(decision); got != fwStrOf(w, "decision") {
			t.Fatalf("case %d (%s): decision %s, original %s", i, marshalJSON(c, ""), got, fwStrOf(w, "decision"))
		}
		var gotCalls []any
		for j, title := range ui.selectTitles {
			opts := make([]any, len(ui.selectOpts[j]))
			for k, o := range ui.selectOpts[j] {
				opts[k] = o
			}
			gotCalls = append(gotCalls, fwObj("title", title, "options", opts))
		}
		wantCalls := listOf(w, "selects")
		if len(wantCalls) != len(gotCalls) || (len(gotCalls) > 0 && !fwEqual(gotCalls, wantCalls)) {
			t.Fatalf("case %d: select calls %s, original %s", i, marshalJSON(gotCalls, ""), marshalJSON(wantCalls, ""))
		}
		eq(t, ui.inputCalls, len(listOf(w, "inputs")), fmt.Sprintf("case %d input calls", i))
	}
}

func TestAuthorityOracleChain(t *testing.T) {
	cases, want := listOf(authCases(t), "chain"), listOf(authOracle(t), "chain")
	if len(cases) < 900 || len(cases) != len(want) {
		t.Fatalf("%d cases, %d recorded", len(cases), len(want))
	}
	for i, raw := range cases {
		c, w := raw.(*jsObject), want[i].(*jsObject)
		var links []*linkFake
		var named []NamedAuthorizer
		for _, l := range listOf(c, "links") {
			lo := l.(*jsObject)
			v := lo.obj("verdict")
			link := newLink(AuthorizerVerdict{Kind: fwStrOf(v, "kind"), Reason: optStr(v, "reason")}, fwStrOf(lo, "name"))
			if _, throws := v.str("throw"); throws {
				link.err = errors.New("boom")
			}
			links = append(links, link)
			named = append(named, link.named())
		}
		terminal := newTerminal(fwDecisionFromJS(c.obj("terminal")))
		composed := ComposeAuthorizerChain(named, terminal, &queryFake{}, &fwLogger{})
		decision, err := composed.Authorize(authDetails())
		rejected := err != nil
		if rejected && decision != (PermissionPromptDecision{}) {
			t.Fatalf("case %d: a rejected ask carries a decision %s", i, marshalJSON(decisionJS(decision), ""))
		}
		var consulted []any
		for _, l := range links {
			if len(l.calls) > 0 {
				consulted = append(consulted, l.name)
			}
		}
		var decisionOut any = decisionJS(decision)
		if rejected {
			decisionOut = nil
		}
		got := fwObj("decision", decisionOut, "rejected", rejected, "consulted", consulted, "terminalCalls", terminal.callCount(), "identity", composed == TerminalAuthorizer(terminal))
		if !fwEqual(got, w) {
			t.Fatalf("case %d (%s): got %s, original %s", i, marshalJSON(c, ""), marshalJSON(got, ""), marshalJSON(w, ""))
		}
	}
}

func TestAuthorityOraclePaste(t *testing.T) {
	cases, want := listOf(authCases(t), "paste"), listOf(authOracle(t), "paste")
	if len(cases) < 80 || len(cases) != len(want) {
		t.Fatalf("%d cases, %d recorded", len(cases), len(want))
	}
	for i, c := range cases {
		if got := CollapsePastedNewlines(c.(string)); got != want[i].(string) {
			t.Fatalf("case %d %q: got %q, original %q", i, c, got, want[i])
		}
	}
}

func TestAuthorityOracleLocal(t *testing.T) {
	cases, want := listOf(authCases(t), "local"), listOf(authOracle(t), "local")
	if len(cases) < 400 || len(cases) != len(want) {
		t.Fatalf("%d cases, %d recorded", len(cases), len(want))
	}
	for i, raw := range cases {
		c, w := raw.(*jsObject), want[i].(*jsObject)
		details := detailsFromJS(t, c.obj("details"))
		bus := &busFake{}
		var mu sync.Mutex
		var calls []any
		session := c.obj("session")
		auth := NewLocalUserAuthorizer(LocalUserAuthorizerDeps{
			UI: &promptUIFake{}, Mode: "tui", Events: bus, Dialogs: NewAskDialogQueue(),
			GetPromptPreferences: defaultPromptPreferences,
			RequestPermissionDecision: func(view PermissionPromptView, title string, _ PromptPayload, options *RequestPermissionOptions) (PermissionPromptDecision, error) {
				mu.Lock()
				calls = append(calls, fwObj("title", title, "notice", fwObj("title", view.Notice.Title, "body", view.Notice.Body), "options", optionsJS(options), "mode", view.Mode))
				mu.Unlock()
				return approvedByHuman, nil
			},
			DescribeSession: func() NotificationSession {
				return NotificationSession{Name: optStr(session, "name"), Cwd: fwStrOf(session, "cwd")}
			},
		})
		mustDecision(t)(auth.Authorize(details))
		var emitted []any
		for _, e := range bus.snapshot() {
			emitted = append(emitted, fwObj("channel", e.channel, "data", fwParse(t, marshalJSON(e.data, ""))))
		}
		got := fwObj("emitted", emitted, "calls", calls)
		if !fwEqual(got, w) {
			t.Fatalf("case %d (%s): got %s, original %s", i, marshalJSON(c, ""), marshalJSON(got, ""), marshalJSON(w, ""))
		}
	}
}

func TestAuthorityOraclePrompter(t *testing.T) {
	cases, want := listOf(authCases(t), "prompter"), listOf(authOracle(t), "prompter")
	if len(cases) < 150 || len(cases) != len(want) {
		t.Fatalf("%d cases, %d recorded", len(cases), len(want))
	}
	for i, raw := range cases {
		c := raw.(*jsObject)
		logger := &fwLogger{}
		terminal := newTerminal(fwDecisionFromJS(c.obj("decision")))
		mustDecision(t)(NewPermissionPrompter(PermissionPrompterDeps{Logger: logger}).Prompt(terminal, detailsFromJS(t, c.obj("details"))))
		var got []any
		for _, e := range logger.reviews() {
			got = append(got, fwObj("event", e.event, "details", e.details))
		}
		if !fwEqual(got, want[i]) {
			t.Fatalf("case %d (%s): got %s, original %s", i, marshalJSON(c, ""), marshalJSON(got, ""), marshalJSON(want[i], ""))
		}
	}
}

func TestAuthorityOracleSelection(t *testing.T) {
	cases, want := listOf(authCases(t), "selection"), listOf(authOracle(t), "selection")
	if len(cases) < 70 || len(cases) != len(want) {
		t.Fatalf("%d cases, %d recorded", len(cases), len(want))
	}
	for i, raw := range cases {
		c, w := raw.(*jsObject), want[i].(*jsObject)
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			clearHintEnv(t)
			if env, ok := c.str("env"); ok {
				t.Setenv("PI_SUBAGENT_PARENT_SESSION", env)
			}
			hasUI, _ := c.flag("hasUI")
			subagent, _ := c.flag("subagent")
			var s *servingFake
			switch v := c.vals["serving"].(type) {
			case bool:
				s = serving(v)
			default:
				s = &servingFake{}
			}
			env := newSelEnv(t, withDetector(subagent), withServing(s))
			if reg, ok := c.str("registry"); ok {
				env.deps.Registry = fwMakeSubagentRegistry("session-1", &SubagentSessionInfo{ParentSessionID: &reg})
			}
			ctx := selCtx(hasUI, func(c *AuthorityContext) {
				c.SessionManager = &fwSessionManager{sessionID: fwID("session-1"), sessionDir: fwID("/sessions/session-1")}
			})
			authority := selectOf(t, ctx, env.deps.AuthorizerSelectionDeps)
			kind := ""
			switch authority.Terminal.(type) {
			case *LocalUserAuthorizer:
				kind = "LocalUserAuthorizer"
			case *ParentAuthorizer:
				kind = "ParentAuthorizer"
			case DenyingAuthorizer:
				kind = "DenyingAuthorizer"
			}
			var relay any
			if authority.RelayTarget != nil {
				relay = fwObj("sessionId", authority.RelayTarget.SessionID, "source", string(authority.RelayTarget.Source))
			}
			var asked []any
			for _, a := range s.asked {
				asked = append(asked, fwObj("sessionId", a.SessionID, "source", string(a.Source)))
			}
			got := fwObj("terminal", kind, "adjudicatesLocally", authority.AdjudicatesLocally, "relayTarget", relay, "asked", asked)
			if !fwEqual(got, w) {
				t.Fatalf("got %s, original %s", marshalJSON(got, ""), marshalJSON(w, ""))
			}
		})
	}
}

func TestAuthorityOracleUiPrompt(t *testing.T) {
	cases, want := listOf(authCases(t), "uiPrompt"), listOf(authOracle(t), "uiPrompt")
	if len(cases) < 90 || len(cases) != len(want) {
		t.Fatalf("%d cases, %d recorded", len(cases), len(want))
	}
	for i, raw := range cases {
		c := raw.(*jsObject)
		payload := AsPromptPayload(c.vals["payload"])
		got := BuildUiPrompt(UiPromptInput{RequestID: fwStrOf(c, "requestId"), Source: fwStrOf(c, "source"), AgentName: optStr(c, "agentName"), Payload: *payload,
			ToolName: optStr(c, "toolName"), SkillName: optStr(c, "skillName"), Path: optStr(c, "path"), Command: optStr(c, "command"), Target: optStr(c, "target")})
		if !fwEqual(fwParse(t, marshalJSON(got.JS(), "")), want[i]) {
			t.Fatalf("case %d (%s): got %s, original %s", i, marshalJSON(c, ""), marshalJSON(got.JS(), ""), marshalJSON(want[i], ""))
		}
	}
}

// tagTheme marks every colored span with its theme token, so the frames prove which token styled what.
type tagTheme struct{}

func (tagTheme) Fg(color, text string) string { return "<" + color + ">" + text + "</>" }

type keyedExpand struct{ key string }

func (k keyedExpand) Matches(data, action string) bool {
	return action == "app.tools.expand" && data == k.key
}

func TestAuthorityOracleComponent(t *testing.T) {
	cases, want := listOf(authCases(t), "component"), listOf(authOracle(t), "component")
	if len(cases) < 250 || len(cases) != len(want) {
		t.Fatalf("%d cases, %d recorded", len(cases), len(want))
	}
	for i, raw := range cases {
		c, w := raw.(*jsObject), want[i].(*jsObject)
		pref := c.obj("pref")
		prefs := defaultPromptPreferences()
		prefs.DoublePressToConfirm, _ = pref.flag("double")
		prefs.DialogKeys = oracleKeysets[fwStrOf(pref, "keys")]
		if b := pref.obj("budget"); b != nil {
			rows, _ := b.num("maxRows")
			width, _ := b.num("fieldMaxWidth")
			prefs.Budget = RenderBudget{MaxRows: rows, FieldMaxWidth: width}
		}
		prefs.PromptNotifications = strList(c.vals["notifications"])
		notice := c.obj("notice")

		tuiRec := &recordingTUI{}
		ui := &promptUIFake{}
		decisions := make(chan UnattributedDecision, 8)
		var optionsSeen PromptCustomOptions
		var component PromptComponent
		mounted := make(chan struct{})
		ui.custom = func(factory PromptComponentFactory, options PromptCustomOptions) (UnattributedDecision, bool, error) {
			optionsSeen = options
			component = factory(tuiRec, tagTheme{}, keyedExpand{fwStrOf(c, "expandKey")}, func(d UnattributedDecision) { decisions <- d })
			close(mounted)
			d := <-decisions
			decisions <- d
			return d, true, nil
		}
		view := PermissionPromptView{PromptPreferences: prefs, Mode: "tui", UI: ui, Notice: PromptNotice{Title: fwStrOf(notice, "title"), Body: fwStrOf(notice, "body")}}
		payload := AsPromptPayload(c.vals["payloadJson"])
		go func() {
			_, _ = PresentInlinePermissionPrompt(view, "Permission Required", *payload, optionsFromJS(c.vals["options"]))
		}()
		select {
		case <-mounted:
		case <-time.After(5 * time.Second):
			t.Fatalf("case %d: never mounted", i)
		}

		widths := listOf(c, "widths")
		renderAll := func() []any {
			var all []any
			for _, wd := range widths {
				lines := component.Render(int(wd.(float64)))
				row := make([]any, len(lines))
				for k, l := range lines {
					row[k] = l
				}
				all = append(all, row)
			}
			return all
		}
		var frames []any
		frames = append(frames, fwObj("lines", renderAll()))
		var final any
		for _, k := range listOf(c, "keys") {
			tuiRec.mu.Lock()
			tuiRec.renders = 0
			tuiRec.mu.Unlock()
			setBefore := len(ui.setCalls)
			component.HandleInput(k.(string))
			step := newObject()
			tuiRec.mu.Lock()
			step.set("renders", float64(tuiRec.renders))
			tuiRec.mu.Unlock()
			sets := []any{}
			for _, s := range ui.setCalls[setBefore:] {
				sets = append(sets, s)
			}
			step.set("set", sets)
			select {
			case d := <-decisions:
				decisions <- d
				step.set("decision", unattributedJS(d))
				frames = append(frames, step)
				final = step.vals["decision"]
			default:
				step.set("lines", renderAll())
				frames = append(frames, step)
				continue
			}
			break
		}
		var writes []any
		for _, s := range tuiRec.written() {
			writes = append(writes, s)
		}
		got := fwObj("options", fwObj("overlay", optionsSeen.Overlay), "writes", writes, "frames", frames, "decision", final)
		if !fwEqual(got, w) {
			t.Fatalf("case %d (payload %s, keys %s): %s", i, fwStrOf(c, "payload"), marshalJSON(c.vals["keys"], ""), firstFrameDifference(got, w))
		}
	}
}

// firstFrameDifference names the first step whose frame differs.
func firstFrameDifference(got, want *jsObject) string {
	gf, wf := got.vals["frames"].([]any), want.vals["frames"].([]any)
	for i := 0; i < len(gf) && i < len(wf); i++ {
		if !fwEqual(gf[i], wf[i]) {
			return fmt.Sprintf("step %d: got %s, original %s", i, marshalJSON(gf[i], ""), marshalJSON(wf[i], ""))
		}
	}
	return fmt.Sprintf("frame counts %d vs %d, or options/writes/decision: got %s, original %s", len(gf), len(wf), marshalJSON(got, ""), marshalJSON(want, ""))
}

func TestAuthorityOracleFallback(t *testing.T) {
	cases, want := listOf(authCases(t), "fallback"), listOf(authOracle(t), "fallback")
	if len(cases) < 200 || len(cases) != len(want) {
		t.Fatalf("%d cases, %d recorded", len(cases), len(want))
	}
	for i, raw := range cases {
		c, w := raw.(*jsObject), want[i].(*jsObject)
		var selects []dialogAnswer
		for _, s := range listOf(c, "selects") {
			if str, ok := s.(string); ok {
				selects = append(selects, answer(str))
			} else {
				selects = append(selects, undefinedAnswer)
			}
		}
		ui := &promptUIFake{dialogUI: dialogUI{selects: selects, input: answer(fwStrOf(c, "input"))}}
		prefs := defaultPromptPreferences()
		if b := c.obj("budget"); b != nil {
			rows, _ := b.num("maxRows")
			width, _ := b.num("fieldMaxWidth")
			prefs.Budget = RenderBudget{MaxRows: rows, FieldMaxWidth: width}
		}
		view := PermissionPromptView{PromptPreferences: prefs, Mode: "rpc", UI: ui, Notice: PromptNotice{Title: "t", Body: "b"}}
		decision, err := RequestPermissionDecision(view, "Title", *AsPromptPayload(c.vals["payloadJson"]), optionsFromJS(c.vals["options"]))
		if err != nil {
			t.Fatal(err)
		}
		var calls []any
		for j, title := range ui.selectTitles {
			opts := make([]any, len(ui.selectOpts[j]))
			for k, o := range ui.selectOpts[j] {
				opts[k] = o
			}
			calls = append(calls, fwObj("title", title, "options", opts))
		}
		if !fwEqual(decisionJS(decision), w.vals["decision"]) || !fwEqual(calls, w.vals["selects"]) || ui.inputCalls != len(listOf(w, "inputs")) {
			t.Fatalf("case %d (%s): got %s %s, original %s", i, marshalJSON(c, ""), marshalJSON(decisionJS(decision), ""), marshalJSON(calls, ""), marshalJSON(w, ""))
		}
	}
}

// TestAuthorityOracleRegistry replays the disposer scenarios. The original's disposer compares function identity; Go functions are not
// comparable, so the port's disposer compares the registration. They agree whenever the later registration is a different function.
// Registering the very same function again and calling the first disposer a second time evicts it in the original and does not here:
// the port keeps the registration the stale call did not make (the guard's documented intent), the original's accident of identity
// cannot be reproduced. The recorded verdict of the original is asserted so a change in the original shows.
func TestAuthorityOracleRegistry(t *testing.T) {
	cases, want := listOf(authCases(t), "registry"), listOf(authOracle(t), "registry")
	if len(cases) != 2 || len(cases) != len(want) {
		t.Fatalf("%d cases, %d recorded", len(cases), len(want))
	}
	fns := map[string]AuthorizeFunc{
		"a": func(*PromptPermissionDetails, PermissionQuery, AuthorizerLog) (AuthorizerVerdict, error) {
			return AuthorizerVerdict{Kind: "defer"}, nil
		},
		"b": func(*PromptPermissionDetails, PermissionQuery, AuthorizerLog) (AuthorizerVerdict, error) {
			return AuthorizerVerdict{Kind: "deny"}, nil
		},
	}
	for i, raw := range cases {
		c, w := raw.(*jsObject), want[i].(*jsObject)
		r := &AuthorizerRegistry{}
		disposeFirst, err := r.Register("link", fns[fwStrOf(c, "first")])
		if err != nil {
			t.Fatal(err)
		}
		disposeFirst()
		if _, err := r.Register("link", fns[fwStrOf(c, "second")]); err != nil {
			t.Fatal(err)
		}
		disposeFirst()
		got := r.Get("link") != nil
		original, _ := w.flag("stillRegistered")
		sameFunction := fwStrOf(c, "first") == fwStrOf(c, "second")
		if sameFunction {
			if original || !got {
				t.Fatalf("case %d (%s): original stillRegistered=%v, port %v (expected: original evicts, port keeps)", i, fwStrOf(c, "name"), original, got)
			}
			continue
		}
		if got != original {
			t.Fatalf("case %d (%s): port %v, original %v", i, fwStrOf(c, "name"), got, original)
		}
	}
}
