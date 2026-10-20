package pi_permission_system

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// Differential test of the gate core against the original's real gate stack. testdata/gatecore-oracle.json is what
// port/probe/gatecore-probe.test.ts recorded from the unmodified original (port/record-gatecore-probe.sh, inputs from
// port/probe/gen-gatecore-cases.py): for each world (config and agent files, session rules, tools, skills, shellTools, yolo, the active
// agent, scripted prompt answers, a sequence of tool_call and input events) every answer, the prompts raised, the review-log entries,
// the decision events, the notifications and the final session rules. The port runs the same worlds through the same stack.

type gcDirs struct{ cwd, out, els, agent, cfg string }

func (d gcDirs) pairs() [][2]string {
	return [][2]string{{"$CWD", d.cwd}, {"$OUT", d.out}, {"$ELSE", d.els}, {"$AGENT", d.agent}, {"$CFG", d.cfg}}
}

func (d gcDirs) sub(text string) string {
	for _, p := range d.pairs() {
		text = strings.ReplaceAll(text, p[0], p[1])
	}
	return text
}

func (d gcDirs) scrub(text string) string {
	for _, p := range d.pairs() {
		text = strings.ReplaceAll(text, p[1], p[0])
	}
	return text
}

var gcRequestID = regexp.MustCompile(`perm-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

func gcMaskIDs(text string) string {
	ids := map[string]string{}
	return gcRequestID.ReplaceAllStringFunc(text, func(id string) string {
		if _, ok := ids[id]; !ok {
			ids[id] = fmt.Sprintf("REQ%d", len(ids)+1)
		}
		return ids[id]
	})
}

func gcDecision(o *jsObject) PermissionPromptDecision {
	d := PermissionPromptDecision{}
	d.Approved, _ = o.flag("approved")
	state, _ := o.str("state")
	d.State = PermissionDecisionState(state)
	if r, ok := o.str("denialReason"); ok {
		d.DenialReason = &r
	}
	d.ConfirmationUnavailable, _ = o.flag("confirmationUnavailable")
	if w, ok := o.str("sessionGrantWidth"); ok {
		d.SessionGrantWidth = SessionGrantWidth(w)
	}
	if by := AsDecisionSource(o.vals["decidedBy"]); by != nil {
		d.DecidedBy = *by
	}
	return d
}

// gcPromptJS is the prompt details as the original's escalate receives them, a JSON object (undefined fields left out).
func gcPromptJS(d *PromptPermissionDetails) *jsObject {
	o := newObject()
	o.set("requestId", d.RequestID)
	o.set("payload", d.Payload.JS())
	o.set("source", d.Source)
	o.set("agentName", strOrNull(d.AgentName))
	for _, f := range []struct {
		k string
		v *string
	}{{"toolCallId", d.ToolCallID}, {"toolName", d.ToolName}, {"skillName", d.SkillName}, {"path", d.Path}, {"command", d.Command}, {"target", d.Target},
		{"toolInputPreview", d.ToolInputPreview}, {"sessionLabel", d.SessionLabel}, {"surface", d.Surface}, {"value", d.Value}} {
		if f.v != nil {
			o.set(f.k, *f.v)
		}
	}
	if d.SessionApproval != nil {
		grants := []any{}
		for _, g := range d.SessionApproval.Grants {
			grants = append(grants, obj("surface", g.Surface, "pattern", g.Pattern))
		}
		o.set("sessionApproval", obj("grants", grants))
	}
	if d.AccessIntent != nil {
		mv := []any{}
		for _, v := range d.AccessIntent.MatchValues {
			mv = append(mv, v)
		}
		o.set("accessIntent", obj("surface", d.AccessIntent.Surface, "matchValues", mv, "boundaryValue", strOrNull(d.AccessIntent.BoundaryValue)))
	}
	return o
}

func gcOutcomeJS(o GateOutcome) *jsObject {
	if o.Action == "block" {
		return obj("action", "block", "reason", o.Reason)
	}
	return obj("action", "allow")
}

// gcRun runs one world through the port and returns the same record the probe wrote.
func gcRun(t *testing.T, raw *jsObject) (record *jsObject) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dirs := gcDirs{cwd: filepath.Join(base, "cwd"), out: filepath.Join(base, "out"), els: filepath.Join(base, "else"), agent: filepath.Join(base, "agent"), cfg: filepath.Join(base, "cfg")}
	for _, p := range dirs.pairs() {
		if err := os.MkdirAll(p[1], 0o777); err != nil {
			t.Fatal(err)
		}
	}
	v, err := parseJSON([]byte(dirs.sub(marshalJSON(raw, ""))))
	if err != nil {
		t.Fatal(err)
	}
	spec := v.(*jsObject)
	id, _ := spec.str("id")

	for _, f := range spec.vals["fs"].([]any) {
		fo := f.(*jsObject)
		path, _ := fo.str("path")
		kind, _ := fo.str("kind")
		if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
			t.Fatal(err)
		}
		switch kind {
		case "dir":
			err = os.MkdirAll(path, 0o777)
		case "file":
			err = os.WriteFile(path, []byte("x"), 0o666)
		default:
			target, _ := fo.str("target")
			err = os.Symlink(target, path)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	globalConfigPath, agentsDir := filepath.Join(dirs.cfg, "pi-permissions.jsonc"), filepath.Join(dirs.cfg, "agents")
	if err := os.MkdirAll(agentsDir, 0o777); err != nil {
		t.Fatal(err)
	}
	if cfg, ok := spec.str("config"); ok {
		if err := os.WriteFile(globalConfigPath, []byte(cfg), 0o666); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range spec.obj("agents").order() {
		text, _ := spec.obj("agents").str(name)
		if err := os.WriteFile(filepath.Join(agentsDir, name+".md"), []byte(text), 0o666); err != nil {
			t.Fatal(err)
		}
	}
	yolo, _ := spec.flag("yolo")
	manager := NewPermissionManager(PermissionManagerOptions{
		PolicyLoaderOptions: PolicyLoaderOptions{GlobalConfigPath: globalConfigPath, AgentsDir: agentsDir, McpServerNames: []string{}},
		IsYoloEnabled:       func() bool { return yolo },
	})
	rules := &SessionRules{}
	for _, r := range spec.vals["sessionRules"].([]any) {
		ro := r.(*jsObject)
		s, _ := ro.str("surface")
		p, _ := ro.str("pattern")
		rules.Approve(s, p)
	}
	cfg := defaultExtensionConfig()
	if st, ok := spec.vals["shellTools"].(*jsObject); ok {
		cfg.ShellTools = st
	}
	paths := ExtensionPaths{AgentDir: "/test/agent", SessionsDir: "/test/agent/sessions", SubagentSessionsDir: "/test/agent/subagent-sessions",
		ForwardingDir: "/test/agent/sessions/permission-forwarding", GlobalLogsDir: "/test/agent/logs",
		PiInfrastructureDirs: []string{dirs.agent, filepath.Join(dirs.agent, "git")}, PiInfrastructureExcludedDirs: []string{filepath.Join(dirs.agent, "logs")}}
	fwd := &fakeForwarding{}
	session := NewPermissionSession(paths, fwd, manager, rules, &fakeConfigStore{current: cfg}, &fakeAuthSelection{}, PathFlavorForPlatform(runtime.GOOS))
	skills := []SkillPromptEntry{}
	for _, s := range spec.vals["skills"].([]any) {
		so := s.(*jsObject)
		e := SkillPromptEntry{}
		e.Name, _ = so.str("name")
		e.Description, _ = so.str("description")
		e.Location, _ = so.str("location")
		e.State, _ = so.str("state")
		e.NormalizedLocation, _ = so.str("normalizedLocation")
		e.NormalizedBaseDir, _ = so.str("normalizedBaseDir")
		skills = append(skills, e)
	}
	session.SetActiveSkillEntries(skills)
	resolver := NewPermissionResolver(manager, rules)

	logger, events := &gfLogger{}, &gfEvents{}
	registry := &gfToolRegistry{}
	if tools, ok := spec.vals["tools"].([]any); ok {
		registry.all = tools
	} else {
		registry.all = []any{obj("name", "read"), obj("name", "bash")}
	}
	answers := spec.vals["prompts"].([]any)
	promptIndex := 0
	prompter := &gfEscalator{}
	prompter.fn = func(*PromptPermissionDetails) (PermissionPromptDecision, error) {
		i := promptIndex
		if i >= len(answers) {
			i = len(answers) - 1
		}
		promptIndex++
		if i < 0 {
			return PermissionPromptDecision{Approved: true, State: DecisionApproved, DecidedBy: gfHuman}, nil
		}
		return gcDecision(answers[i].(*jsObject)), nil
	}
	runner := NewGateRunner(resolver, rules, prompter, NewGateDecisionReporter(logger, events), func() bool { return yolo })
	handler := NewPermissionGateHandler(session, registry, NewToolCallGatePipeline(resolver, session, nil, nil),
		NewSkillInputGatePipeline(ResolverSkillInputInputs{Resolver: resolver}), runner)

	agent, hasAgent := spec.str("agent")
	var notes [][2]string
	outAnswers := []any{}
	for _, c := range spec.vals["calls"].([]any) {
		call := c.(*jsObject)
		hasUI := true
		if f, ok := call.flag("hasUI"); ok {
			hasUI = f
		}
		var entries []SessionEntryView
		if hasAgent {
			entries = []SessionEntryView{{"type": "custom", "customType": "active_agent", "data": map[string]any{"name": agent}}}
		}
		n := &fakeNotifier{}
		ctx := SessionContext{AuthorityContext: AuthorityContext{ForwarderContext: ForwarderContext{Cwd: dirs.cwd, HasUI: hasUI, SessionManager: gfSessionManager{entries: entries}}}, Notifier: n}
		answer := newObject()
		func() {
			defer func() {
				if r := recover(); r != nil {
					answer.set("throws", fmt.Sprint(r))
				}
			}()
			kind, _ := call.str("kind")
			if kind == "tool_call" {
				out, err := handler.HandleToolCall(call.vals["event"], ctx)
				if err != nil {
					answer.set("throws", err.Error())
					return
				}
				answer.set("result", gcOutcomeJS(out))
				return
			}
			text, _ := call.str("text")
			out, err := handler.HandleInput(text, ctx)
			if err != nil {
				answer.set("throws", err.Error())
				return
			}
			answer.set("result", obj("action", out.Action))
		}()
		outAnswers = append(outAnswers, answer)
		for _, m := range n.notified {
			notes = append(notes, m)
		}
	}
	prompts, reviews, decisions, notesJS, rulesJS := []any{}, []any{}, []any{}, []any{}, []any{}
	for i := range prompter.calls {
		prompts = append(prompts, gcPromptJS(&prompter.calls[i]))
	}
	for _, l := range logger.reviews {
		reviews = append(reviews, []any{l.event, l.details})
	}
	for _, d := range events.decisions() {
		b, err := jsonMarshalOrdered(d)
		if err != nil {
			t.Fatal(err)
		}
		decisions = append(decisions, []any{PermissionsDecisionChannel, b})
	}
	for _, n := range notes {
		notesJS = append(notesJS, []any{n[0], n[1]})
	}
	for _, r := range rules.GetRuleset() {
		ro := obj("surface", r.Surface, "pattern", r.Pattern, "action", r.Action)
		if r.Reason != nil {
			ro.set("reason", *r.Reason)
		}
		ro.set("layer", r.Layer)
		ro.set("origin", r.Origin)
		rulesJS = append(rulesJS, ro)
	}
	rec := obj("id", id, "answers", outAnswers, "prompts", prompts, "reviews", reviews, "decisions", decisions, "notes", notesJS, "rules", rulesJS)
	text := gcMaskIDs(dirs.scrub(marshalJSON(rec, "")))
	out, err := parseJSON([]byte(text))
	if err != nil {
		t.Fatalf("%s: %v", id, err)
	}
	return out.(*jsObject)
}

// jsonMarshalOrdered is the decision event as the ordered object its JSON is.
func jsonMarshalOrdered(d PermissionDecisionEvent) (*jsObject, error) {
	b, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	v, err := parseJSON(b)
	if err != nil {
		return nil, err
	}
	return v.(*jsObject), nil
}

// gcCanon is the JSON text of v with every object's keys sorted.
func gcCanon(v any) string {
	switch x := v.(type) {
	case *jsObject:
		keys := []string{}
		for _, k := range x.keys {
			if x.has(k) {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		parts := []string{}
		for _, k := range keys {
			parts = append(parts, marshalJSON(k, "")+":"+gcCanon(x.vals[k]))
		}
		return "{" + strings.Join(parts, ",") + "}"
	case []any:
		parts := []string{}
		for _, e := range x {
			parts = append(parts, gcCanon(e))
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	return marshalJSON(v, "")
}

func TestGateCoreOracle(t *testing.T) {
	read := func(name string) *jsObject {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		v, err := parseJSON(data)
		if err != nil {
			t.Fatal(err)
		}
		if o, ok := v.(*jsObject); ok {
			return o
		}
		return obj("cases", v)
	}
	cases := read("gatecore-cases.json").vals["cases"].([]any)
	oracle := map[string]*jsObject{}
	for _, c := range read("gatecore-oracle.json").vals["cases"].([]any) {
		co := c.(*jsObject)
		id, _ := co.str("id")
		oracle[id] = co
	}
	if len(oracle) != len(cases) {
		t.Fatalf("oracle has %d cases, the corpus %d", len(oracle), len(cases))
	}
	diffs, orderDiffs := 0, 0
	for _, c := range cases {
		co := c.(*jsObject)
		id, _ := co.str("id")
		got, want := gcRun(t, co), oracle[id]
		for _, part := range []string{"answers", "prompts", "reviews", "decisions", "notes", "rules"} {
			if gcCanon(got.vals[part]) != gcCanon(want.vals[part]) {
				diffs++
				if diffs <= 12 {
					t.Errorf("case %s, %s differ:\n  port:     %s\n  original: %s", id, part, marshalJSON(got.vals[part], ""), marshalJSON(want.vals[part], ""))
				}
			} else if (part == "reviews" || part == "decisions") && marshalJSON(got.vals[part], "") != marshalJSON(want.vals[part], "") {
				orderDiffs++
				if orderDiffs <= 3 {
					t.Errorf("case %s, %s differ in key order:\n  port:     %s\n  original: %s", id, part, marshalJSON(got.vals[part], ""), marshalJSON(want.vals[part], ""))
				}
			}
		}
	}
	if diffs+orderDiffs > 0 {
		t.Fatalf("%d content differences, %d key-order differences over %d cases", diffs, orderDiffs, len(cases))
	}
	t.Logf("%d worlds, zero differences", len(cases))
}
