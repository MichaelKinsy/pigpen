package posthorse

// Smoke tests of the fixture itself (fixture_test.go, fixture_session_test.go). They run the fixture against a small
// probe extension defined here, so they prove the fixture's host answers, views, events, counters, cancellation and
// session model independently of the Posthorse port. Test code only.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// fxProbe is the probe extension's own record of the events it received.
type fxProbe struct {
	mu     sync.Mutex
	events []string
}

func (p *fxProbe) note(s string) {
	p.mu.Lock()
	p.events = append(p.events, s)
	p.mu.Unlock()
}

func fxIDs(entries []map[string]any) string {
	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i], _ = e["id"].(string)
	}
	return strings.Join(ids, ",")
}

// fxProbeExtension is a stand-in extension whose tool "probe" reads every host answer the fixture provides.
func fxProbeExtension(read policyReader) *sdk.Extension {
	p := &fxProbe{}
	e := sdk.New("fixture-probe")
	text := func(s string) (any, error) {
		return sdk.ToolResult{Content: s, Details: map[string]any{"kind": "new-context"}}, nil
	}
	probeSchema := sdk.Schema{
		"type": "object",
		"properties": map[string]any{
			"op": map[string]any{"type": "string"},
			"n":  map[string]any{"type": "number", "minimum": 0},
			"id": map[string]any{"type": "string"},
		},
		"required":             []any{"op"},
		"additionalProperties": false,
	}
	probeTool := func(ctx sdk.Context, params map[string]any) (any, error) {
		op, _ := params["op"].(string)
		sm := ctx.SessionManager()
		switch op {
		case "usage":
			u, err := ctx.GetContextUsage()
			if err != nil {
				return nil, err
			}
			if u == nil {
				return text("usage=undefined")
			}
			tokens, percent := "null", "null"
			if u.Tokens != nil {
				tokens = fmt.Sprint(*u.Tokens)
			}
			if u.Percent != nil {
				percent = fmt.Sprint(*u.Percent)
			}
			return text(fmt.Sprintf("usage=%s/%d/%s", tokens, u.ContextWindow, percent))
		case "policy":
			pol, err := read(ctx)
			if err != nil {
				return nil, err
			}
			return text(fmt.Sprintf("enabled=%v reserve=%d keep=%v", pol.enabled, pol.reserveTokens, pol.keepRecentTokens))
		case "prompt":
			s, err := ctx.GetSystemPrompt()
			if err != nil {
				return nil, err
			}
			return text(s)
		case "tools":
			active, err := ctx.GetActiveTools()
			if err != nil {
				return nil, err
			}
			all, err := ctx.GetAllTools()
			if err != nil {
				return nil, err
			}
			names := make([]string, len(all))
			for i, t := range all {
				names[i] = t.Name + ":" + t.Description
			}
			return text(strings.Join(active, ",") + "|" + strings.Join(names, ","))
		case "branch":
			b, err := sm.GetBranch(nil)
			if err != nil {
				return nil, err
			}
			return text(fxIDs(b))
		case "mirror":
			b, err := ctx.GetBranch()
			if err != nil {
				return nil, err
			}
			ids := make([]string, len(b))
			for i, x := range b {
				ids[i] = x.ID
			}
			return text(strings.Join(ids, ","))
		case "entry":
			id, _ := params["id"].(string)
			en, err := sm.GetEntry(id)
			if err != nil {
				return nil, err
			}
			if en == nil {
				return text("entry=null")
			}
			return text(fmt.Sprint("entry=", en["type"]))
		case "leaf":
			l, err := sm.GetLeafID()
			if err != nil {
				return nil, err
			}
			if l == nil {
				return text("leaf=null")
			}
			return text("leaf=" + *l)
		case "cwd":
			c, err := sm.GetCwd()
			if err != nil {
				return nil, err
			}
			return text("ctx=" + ctx.Cwd() + " sm=" + c)
		case "file":
			f, err := ctx.GetSessionFile()
			if err != nil {
				return nil, err
			}
			d, err := sm.GetSessionDir()
			if err != nil {
				return nil, err
			}
			if f == nil {
				return text("file=none dir=" + d)
			}
			return text("file=" + *f + " dir=" + d)
		case "pending":
			b, err := ctx.HasPendingMessages()
			if err != nil {
				return nil, err
			}
			return text(fmt.Sprint("pending=", b))
		case "projection":
			pr, err := sm.BuildSessionProjection()
			if err != nil {
				return nil, err
			}
			return text(fmt.Sprintf("projection=%d/%d", len(pr.Entries), len(pr.Messages)))
		case "send":
			err := ctx.SendCustomMessage(sdk.CustomMessage{CustomType: "probe-message", Content: "hello", Display: true, Details: map[string]any{"windowId": "w1"}}, sdk.SendMessageOptions{DeliverAs: "followUp"})
			if err != nil {
				return nil, err
			}
			if err := ctx.AppendEntry("probe-entry", map[string]any{"a": 1}); err != nil {
				return nil, err
			}
			return text("sent")
		case "wait":
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("cancelled: %s", ctx.CancellationReason())
			case <-time.After(5 * time.Second):
				return text("timeout")
			}
		case "mutate":
			path := filepath.Join(ctx.Cwd(), "target.md")
			_, err := sdk.WithFileMutationQueue(path, func() (int, error) { return 1, nil })
			if err != nil {
				return nil, err
			}
			return text("mutated")
		case "session":
			id, err := sm.GetSessionID()
			if err != nil {
				return nil, err
			}
			return text("session=" + id)
		case "settings":
			s, err := ctx.GetSettings()
			if err != nil {
				return nil, err
			}
			return text(fmt.Sprint("theme=", s["theme"]))
		case "events":
			p.mu.Lock()
			defer p.mu.Unlock()
			return text(strings.Join(p.events, ","))
		case "fail":
			return nil, errors.New("probe failure")
		case "fail-empty":
			return nil, errors.New("")
		}
		return nil, fmt.Errorf("unknown op %q", op)
	}
	e.Tool("probe", "Fixture probe tool", probeSchema, probeTool)
	e.Tool("notes", "Fixture notes alias", probeSchema, probeTool)
	e.Command("probe-cmd", "Fixture probe command", func(ctx sdk.Context, args string) error {
		if args == "boom" {
			return errors.New("command failure")
		}
		return nil
	})
	e.OnEvent(sdk.EventToolExecutionStart, func(_ sdk.Context, d map[string]any) (any, error) {
		p.note(fmt.Sprintf("start:%v:%v", d["toolName"], d["toolCallId"]))
		return nil, nil
	})
	e.OnEvent(sdk.EventToolExecutionEnd, func(_ sdk.Context, d map[string]any) (any, error) {
		res, _ := d["result"].(map[string]any)
		details, _ := res["details"].(map[string]any)
		p.note(fmt.Sprintf("end:%v:%v:%v", d["toolName"], d["isError"], details["kind"]))
		return nil, nil
	})
	// A second handler for one event: Fire reaches the first, FireAll both.
	e.OnEvent(sdk.EventSessionStart, func(sdk.Context, map[string]any) (any, error) {
		p.note("session_start:first")
		return nil, nil
	})
	e.OnEvent(sdk.EventSessionStart, func(sdk.Context, map[string]any) (any, error) {
		p.note("session_start:second")
		return nil, nil
	})
	e.OnEvent(sdk.EventTurnEnd, func(_ sdk.Context, d map[string]any) (any, error) {
		results, _ := d["toolResults"].([]any)
		msg, _ := d["message"].(map[string]any)
		return map[string]any{"entries": []any{}, "continue": len(results) > 0 && msg["stopReason"] == "toolUse"}, nil
	})
	e.OnEvent(sdk.EventContext, func(_ sdk.Context, d map[string]any) (any, error) {
		msgs, _ := d["messages"].([]any)
		var kept []any
		for _, m := range msgs {
			if mm, _ := m.(map[string]any); mm["drop"] != true {
				kept = append(kept, m)
			}
		}
		if len(kept) == len(msgs) {
			return nil, nil
		}
		return map[string]any{"messages": kept}, nil
	})
	e.OnEvent(sdk.EventBeforeAgentStart, func(_ sdk.Context, _ map[string]any) (any, error) {
		return map[string]any{"systemPrompt": "replaced"}, nil
	})
	e.OnEvent(sdk.EventContextWithSystem, func(_ sdk.Context, d map[string]any) (any, error) {
		msgs, _ := d["messages"].([]any)
		m, _ := msgs[0].(map[string]any)
		sections, _ := m["sections"].(map[string]any)
		sections["posthorse"] = "guidance-of-8c" // 14 UTF-16 units -> ceil(14/4) = 4 tokens
		return nil, nil
	})
	e.OnEvent(sdk.EventSessionBeforeCompact, func(ctx sdk.Context, d map[string]any) (any, error) {
		sm := ctx.SessionManager()
		leaf, err := sm.GetLeafID()
		if err != nil {
			return nil, err
		}
		pr, err := sm.BuildSessionProjection()
		if err != nil {
			return nil, err
		}
		_, branchErr := sm.GetBranch(nil)
		prep, _ := d["preparation"].(map[string]any)
		settings, _ := prep["settings"].(map[string]any)
		return map[string]any{"compaction": map[string]any{"summary": fmt.Sprintf("leaf=%s proj=%d branchErr=%v keep=%v reserve=%v", *leaf, len(pr.Messages), branchErr, settings["keepRecentTokens"], settings["reserveTokens"])}}, nil
	})
	return e
}

func fxProbeFixture(t *testing.T, opts ...fixOpt) *fixture {
	t.Helper()
	return newFixture(t, append([]fixOpt{withFactory(fxProbeExtension)}, opts...)...)
}

func TestFixtureHostSmokeCommandAndTool(t *testing.T) {
	// The fake-host copy needs a smoke test that calls a command and a tool first (Skill, step 3).
	f := fxProbeFixture(t)
	if msg := f.H().Command("probe-cmd", "ok"); msg != "" {
		t.Fatalf("command failed: %s", msg)
	}
	if msg := f.H().Command("probe-cmd", "boom"); !strings.Contains(msg, "command failure") {
		t.Fatalf("command error = %q", msg)
	}
	if got := f.Run("probe", rec{"op": "prompt"}).Text(); got != "You are a test assistant." {
		t.Fatalf("prompt = %q", got)
	}
	if names := f.ToolNames(); len(names) != 2 || names[0] != "probe" || names[1] != "notes" {
		t.Fatalf("tools = %v", names)
	}
}

func TestFixtureToolRunFiresExecutionEvents(t *testing.T) {
	f := fxProbeFixture(t)
	res := f.Run("probe", rec{"op": "prompt"})
	if res.Details["kind"] != "new-context" {
		t.Fatalf("details = %v", res.Details)
	}
	if got := f.Run("probe", rec{"op": "events"}).Text(); got != "start:probe:id,end:probe:false:new-context,start:probe:id" {
		t.Fatalf("events = %q", got)
	}
	// A throwing tool: the error text comes back and no tool_execution_end fires.
	_, failure := f.RunErr("probe", rec{"op": "fail"})
	if !strings.Contains(failure, "probe failure") {
		t.Fatalf("failure = %q", failure)
	}
	if got := f.Run("probe", rec{"op": "events"}).Text(); got != "start:probe:id,end:probe:false:new-context,start:probe:id,end:probe:false:new-context,start:probe:id,start:probe:id" {
		t.Fatalf("a failing tool must not fire tool_execution_end: %q", got)
	}
}

func TestFixtureEmptyErrorMessageIsAFailure(t *testing.T) {
	f := fxProbeFixture(t)
	res, message, failed := f.RunFailed("probe", rec{"op": "fail-empty"})
	if !failed || message != "" || res.Content != nil {
		t.Fatalf("RunFailed = %+v %q %v", res, message, failed)
	}
	if _, text := f.RunErr("probe", rec{"op": "fail-empty"}); text != fxEmptyError {
		t.Fatalf("RunErr must keep text != \"\" for a failure, got %q", text)
	}
	if _, message, failed := f.RunFailed("probe", rec{"op": "fail"}); !failed || message != "probe failure" {
		t.Fatalf("a failure with a message = %q %v", message, failed)
	}
	if _, message, failed := f.RunFailed("probe", rec{"op": "prompt"}); failed || message != "" {
		t.Fatalf("a success = %q %v", message, failed)
	}
}

func TestFixtureUsageViewsAndSet(t *testing.T) {
	f := fxProbeFixture(t)
	if got := f.Run("probe", rec{"op": "usage"}).Text(); got != "usage=1000/100000/1" {
		t.Fatalf("default usage = %q", got)
	}
	v := f.With(withUsage(200_000, 5_000))
	if got := v.Run("probe", rec{"op": "usage"}).Text(); got != "usage=5000/200000/2.5" {
		t.Fatalf("view usage = %q", got)
	}
	if got := f.Run("probe", rec{"op": "usage"}).Text(); got != "usage=1000/100000/1" {
		t.Fatalf("the view leaked into the base: %q", got)
	}
	f.Set(withUsageUnknown(100_000))
	if got := f.Run("probe", rec{"op": "usage"}).Text(); got != "usage=null/100000/null" {
		t.Fatalf("unknown usage = %q", got)
	}
	f.Set(withNoUsage())
	if got := f.Run("probe", rec{"op": "usage"}).Text(); got != "usage=undefined" {
		t.Fatalf("no usage = %q", got)
	}
	if got := f.With(withUsage(1000, 10)).With(withNoUsage()).Run("probe", rec{"op": "usage"}).Text(); got != "usage=undefined" {
		t.Fatalf("chained views apply in order: %q", got)
	}
}

func TestFixturePolicyReaderFollowsFixtureSettings(t *testing.T) {
	f := fxProbeFixture(t, withKeepRecent(20_000))
	if got := f.Run("probe", rec{"op": "policy"}).Text(); got != "enabled=true reserve=16384 keep=<nil>" {
		t.Fatalf("default policy (fixturePolicy drops keepRecentTokens) = %q", got)
	}
	if got := f.With(withReserve(64_000), withEnabled(false)).Run("probe", rec{"op": "policy"}).Text(); got != "enabled=false reserve=64000 keep=<nil>" {
		t.Fatalf("view policy = %q", got)
	}
}

func TestFixtureContextAnswers(t *testing.T) {
	f := fxProbeFixture(t)
	if got := f.Run("probe", rec{"op": "tools"}).Text(); got != "probe,notes|probe:Fixture probe tool,notes:Fixture notes alias" {
		t.Fatalf("tools = %q", got)
	}
	if got := f.With(withActiveTools()).Run("probe", rec{"op": "tools"}).Text(); got != "|probe:Fixture probe tool,notes:Fixture notes alias" {
		t.Fatalf("no active tools = %q", got)
	}
	if got := f.With(withPending(true)).Run("probe", rec{"op": "pending"}).Text(); got != "pending=true" {
		t.Fatalf("pending = %q", got)
	}
	if got := f.Run("probe", rec{"op": "pending"}).Text(); got != "pending=false" {
		t.Fatalf("pending default = %q", got)
	}
	if got := f.With(withSystemPrompt("custom")).Run("probe", rec{"op": "prompt"}).Text(); got != "custom" {
		t.Fatalf("prompt = %q", got)
	}
	dir := t.TempDir()
	if got := f.Run("probe", rec{"op": "file"}).Text(); got != "file=none dir="+os.TempDir() {
		t.Fatalf("default file/dir = %q", got)
	}
	if got := f.With(withSessionFile("/x/s.jsonl"), withSessionDir(dir)).Run("probe", rec{"op": "file"}).Text(); got != "file=/x/s.jsonl dir="+dir {
		t.Fatalf("file/dir override = %q", got)
	}
	if got := f.With(withHostAnswer("getContextUsage", func(map[string]any) (any, string) { return rec{"tokens": 7, "contextWindow": 70, "percent": 10.0}, "" })).Run("probe", rec{"op": "usage"}).Text(); got != "usage=7/70/10" {
		t.Fatalf("host answer override = %q", got)
	}
	if _, failure := f.With(withHostError("getContextUsage", "usage boom")).RunErr("probe", rec{"op": "usage"}); !strings.Contains(failure, "usage boom") {
		t.Fatalf("host error = %q", failure)
	}
}

func TestFixtureSessionReadsAndCounters(t *testing.T) {
	f := fxProbeFixture(t)
	s := f.Session()
	s.push(rec{"type": "message", "id": "a", "message": fxUser("one")})
	s.push(rec{"type": "message", "message": fxUser("two")})
	if got := f.Run("probe", rec{"op": "branch"}).Text(); got != "a,fixture-1" {
		t.Fatalf("branch = %q", got)
	}
	if got := s.getEntry("fixture-1")["parentId"]; got != "a" {
		t.Fatalf("implicit parentId = %v", got)
	}
	if got := f.Run("probe", rec{"op": "leaf"}).Text(); got != "leaf=fixture-1" {
		t.Fatalf("leaf = %q", got)
	}
	if got := f.Run("probe", rec{"op": "entry", "id": "a"}).Text(); got != "entry=message" {
		t.Fatalf("entry = %q", got)
	}
	if got := f.Run("probe", rec{"op": "entry", "id": "nope"}).Text(); got != "entry=null" {
		t.Fatalf("missing entry = %q", got)
	}
	if n := f.Reads("sessionRead.getEntry"); n != 2 {
		t.Fatalf("getEntry reads = %d", n)
	}
	if n := f.Reads("sessionRead"); n != 4 {
		t.Fatalf("sessionRead reads = %d", n)
	}
	f.ResetReads()
	if f.Reads("sessionRead.getBranch") != 0 {
		t.Fatalf("ResetReads")
	}
	if got := f.Run("probe", rec{"op": "projection"}).Text(); got != "projection=0/0" {
		t.Fatalf("an implicit session projects nothing by default: %q", got)
	}
	if got := f.With(withRealProjection()).Run("probe", rec{"op": "projection"}).Text(); got != "projection=2/2" {
		t.Fatalf("real projection = %q", got)
	}
	s.pop()
	if got := f.Run("probe", rec{"op": "branch"}).Text(); got != "a" {
		t.Fatalf("after pop = %q", got)
	}
	if _, failure := f.With(withHostError("sessionRead.getBranch", "no history")).RunErr("probe", rec{"op": "branch"}); !strings.Contains(failure, "no history") {
		t.Fatalf("branch error = %q", failure)
	}
}

func TestFixtureReplicatedLogFollowsTheSession(t *testing.T) {
	f := fxProbeFixture(t)
	s := f.Session()
	s.push(rec{"type": "custom", "id": "x"})
	if got := f.Run("probe", rec{"op": "mirror"}).Text(); got != "x" {
		t.Fatalf("mirror (first fetch) = %q", got)
	}
	s.push(rec{"type": "custom", "id": "y"})
	if got := f.Run("probe", rec{"op": "mirror"}).Text(); got != "x,y" {
		t.Fatalf("mirror after an append = %q", got)
	}
	s.pop()
	s.pop()
	s.push(rec{"type": "custom", "id": "z"})
	if got := f.Run("probe", rec{"op": "mirror"}).Text(); got != "z" {
		t.Fatalf("mirror after a rewrite = %q", got)
	}
	if n := len(f.CallsTo("watchSessionLog")); n == 0 {
		t.Fatalf("the replicated log was never fetched")
	}
}

func TestFixtureEventsFirstHandlerAndHelpers(t *testing.T) {
	f := fxProbeFixture(t)
	if n := f.HandlerCount("session_start"); n != 2 {
		t.Fatalf("session_start handlers = %d", n)
	}
	if res := f.Fire("session_start", rec{"reason": "startup"}); res != nil {
		t.Fatalf("session_start result = %v", res)
	}
	f.FireAll("session_start", rec{"reason": "startup"})
	if got := f.Run("probe", rec{"op": "events"}).Text(); !strings.HasPrefix(got, "session_start:first,session_start:first,session_start:second") {
		t.Fatalf("events = %q", got)
	}
	if res := f.Fire("no_such_event", nil); res != nil {
		t.Fatalf("unregistered event = %v", res)
	}
	if res := f.TurnEnd(); res["continue"] != false {
		t.Fatalf("turn end without tool results = %v", res)
	}
	if res := f.TurnEnd(rec{"toolName": "probe", "toolCallId": "id"}); res["continue"] != true {
		t.Fatalf("turn end with a tool result = %v", res)
	}
	if res := f.ContextEvent(fxUser("a"), fxAssistant("b")); res != nil {
		t.Fatalf("unchanged context = %v", res)
	}
	res := f.ContextEvent(fxUser("a"), rec{"role": "custom", "drop": true})
	msgs, _ := res["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("changed context = %v", res)
	}
	if got := f.GuidanceFor(); got != "guidance-of-8c" {
		t.Fatalf("guidance = %q", got)
	}
	pv := f.PageContext()
	if got := pv.Run("probe", rec{"op": "usage"}).Text(); got != "usage=996/100000/0.996" {
		t.Fatalf("page context = %q", got)
	}
	f.Set(withUsage(100_000, 2000))
	if got := pv.Run("probe", rec{"op": "usage"}).Text(); got != "usage=1996/100000/1.9959999999999998" { // JS: (1996 / 100000) * 100
		t.Fatalf("page context follows a later Set = %q", got)
	}
}

func TestFixtureBeforeAgentStartExposesTheSharedOptions(t *testing.T) {
	f := fxProbeFixture(t)
	sections := rec{"other": "Keep this policy"}
	out := f.BeforeAgentStart("Custom instructions", rec{"sections": sections, "customPrompt": "Custom instructions"})
	if out.Result["systemPrompt"] != "replaced" || out.Sections["other"] != "Keep this policy" || out.Options["customPrompt"] != "Custom instructions" {
		t.Fatalf("before_agent_start = %+v", out)
	}
	if wire := f.FireWire("turn_end", rec{"message": rec{"role": "assistant", "stopReason": "stop"}, "toolResults": []any{}, "entries": []any{}}); wire["_pigBoundaryResult"] == nil {
		t.Fatalf("FireWire keeps the wrapper: %v", wire)
	}
}

func TestFixtureAutomaticResult(t *testing.T) {
	f := fxProbeFixture(t, withKeepRecent(5))
	branch := []rec{
		{"type": "message", "id": "u", "message": fxUser("keep working")},
		{"type": "custom", "id": "c"},
		{"type": "message", "id": "a", "message": fxAssistant("ok")},
	}
	got := f.With(withReserve(1234)).AutomaticHandoff(branch)
	if want := "leaf=boundary proj=2 branchErr=sessionRead getBranch: Use compaction event history keep=0 reserve=1234"; !strings.Contains(got, "leaf=boundary proj=2") || !strings.Contains(got, "Use compaction event history") || !strings.HasSuffix(got, "keep=0 reserve=1234") {
		t.Fatalf("handoff = %q (want like %q)", got, want)
	}
	// The overrides are scoped to the call.
	if got := f.Run("probe", rec{"op": "leaf"}).Text(); got != "leaf=null" {
		t.Fatalf("leaf after the call = %q", got)
	}
	if got := f.AutomaticHandoff(branch); !strings.Contains(got, "proj=2") {
		t.Fatalf("default projection = %q", got)
	}
}

func TestFixtureMessagesAndAppendedAreRecorded(t *testing.T) {
	f := fxProbeFixture(t)
	f.Run("probe", rec{"op": "send"})
	msgs := f.Messages()
	if len(msgs) != 1 || msgs[0].CustomType != "probe-message" || msgs[0].Content != "hello" || !msgs[0].Display || msgs[0].WindowID() != "w1" || msgs[0].Options["deliverAs"] != "followUp" {
		t.Fatalf("messages = %+v", msgs)
	}
	if ap := f.Appended(); len(ap) != 1 || ap[0]["customType"] != "probe-entry" {
		t.Fatalf("appended = %v", ap)
	}
	if n := len(f.Session().getEntries()); n != 0 {
		t.Fatalf("appendEntry must not touch the session by default (setup() stubs it): %d", n)
	}
	g := fxProbeFixture(t, withAppendToSession())
	g.Run("probe", rec{"op": "send"})
	if es := g.Session().getEntries(); len(es) != 1 || es[0]["customType"] != "probe-entry" {
		t.Fatalf("withAppendToSession entries = %v", es)
	}
}

func TestFixtureCancelReachesTheToolContext(t *testing.T) {
	f := fxProbeFixture(t)
	h := f.StartRun("probe", rec{"op": "wait"})
	time.Sleep(50 * time.Millisecond) // the tool is parked on ctx.Done(); cancelling earlier is equally valid
	h.Cancel("owner stopped")
	_, failure := h.Wait()
	if !strings.Contains(failure, "cancelled: owner stopped") {
		t.Fatalf("failure = %q", failure)
	}
}

func TestFixtureViewCwdStartsAHostPerCwd(t *testing.T) {
	f := fxProbeFixture(t)
	other := t.TempDir()
	base := f.Run("probe", rec{"op": "cwd"}).Text()
	got := f.With(withCwd(other)).Run("probe", rec{"op": "cwd"}).Text()
	if want := "ctx=" + other + " sm=" + other; got != want {
		t.Fatalf("cwd view = %q, want %q", got, want)
	}
	if strings.Contains(base, other) {
		t.Fatalf("base cwd = %q", base)
	}
	first := f.NotesIn(other, rec{"op": "session"}).Text()
	if second := f.NotesIn(other, rec{"op": "session"}).Text(); first != second || !strings.HasPrefix(first, "session=fixture-session-") {
		t.Fatalf("NotesIn keeps one in-memory session per cwd: %q vs %q", first, second)
	}
	if got := f.NotesIn(other, rec{"op": "file"}).Text(); got != "file=none dir=" {
		t.Fatalf("an in-memory session has no file and dir \"\": %q", got)
	}
}

func TestFixtureSettingsAreReplicated(t *testing.T) {
	f := fxProbeFixture(t)
	if _, failure := f.RunErr("probe", rec{"op": "settings"}); !strings.Contains(failure, "no settings") {
		t.Fatalf("a host that sent no settings fails the read: %q", failure)
	}
	f.Set(withSettings(rec{"theme": "dark"}))
	if got := f.Run("probe", rec{"op": "settings"}).Text(); got != "theme=dark" {
		t.Fatalf("settings = %q", got)
	}
	f.Set(withSettings(rec{"theme": "light"}))
	if got := f.Run("probe", rec{"op": "settings"}).Text(); got != "theme=light" {
		t.Fatalf("settings update = %q", got)
	}
}

func TestFixtureFileMutationQueueIsAnswered(t *testing.T) {
	f := fxProbeFixture(t)
	f.Run("probe", rec{"op": "mutate"})
	log := f.MutationLog()
	if len(log) != 2 || !strings.HasPrefix(log[0], "enter ") || !strings.HasSuffix(log[0], "target.md") || strings.TrimPrefix(log[1], "exit ") != strings.TrimPrefix(log[0], "enter ") {
		t.Fatalf("mutation log = %v", log)
	}
}

func TestFixtureToolSchemaIsEnforced(t *testing.T) {
	// The schema check is what setup() asserts before every call; here it is exercised directly on the registered schema.
	f := fxProbeFixture(t)
	schema := f.g.tools[0].schema
	for _, params := range []rec{{"op": "usage"}, {"op": "usage", "n": 3}} {
		if err := fxSchemaCheck(schema, fxNormalize(map[string]any(params))); err != nil {
			t.Fatalf("%v should pass: %v", params, err)
		}
	}
	for _, params := range []rec{{}, {"op": 1}, {"op": "x", "n": -1}, {"op": "x", "extra": true}} {
		if err := fxSchemaCheck(schema, fxNormalize(map[string]any(params))); err == nil {
			t.Fatalf("%v should fail", params)
		}
	}
}

func TestFixtureSchemaCheckKeywords(t *testing.T) {
	cases := []struct {
		name   string
		schema map[string]any
		value  any
		ok     bool
	}{
		{"enum ok", map[string]any{"enum": []any{"a", "b"}}, "a", true},
		{"enum bad", map[string]any{"enum": []any{"a", "b"}}, "c", false},
		{"anyOf", map[string]any{"anyOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "number"}}}, true, false},
		{"integer", map[string]any{"type": "integer"}, 1.5, false},
		{"items", map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, []any{"a", 1.0}, false},
		{"minLength utf16", map[string]any{"type": "string", "minLength": 2.0}, "\U0001F600", true},
		{"pattern", map[string]any{"type": "string", "pattern": "^a+$"}, "aab", false},
		{"required", map[string]any{"type": "object", "required": []any{"x"}}, map[string]any{}, false},
		{"nullable", map[string]any{"anyOf": []any{map[string]any{"type": "number"}, map[string]any{"type": "null"}}}, nil, true},
	}
	for _, c := range cases {
		if err := fxSchemaCheck(c.schema, c.value); (err == nil) != c.ok {
			t.Errorf("%s: err = %v, want ok=%v", c.name, err, c.ok)
		}
	}
}

func TestFixtureResultAndDisplayChecks(t *testing.T) {
	good := []rec{
		{"kind": "context", "usage": rec{"tokens": nil, "contextWindow": 1.0, "percent": nil}, "rollover": "enabled", "rolloverAt": 5.0},
		{"kind": "notes-list", "count": 2.0, "page": rec{"offset": 0.0, "end": 1.0, "total": 2.0}, "more": true},
		{"kind": "note-read", "offset": 0.0, "end": 1.0, "total": 2.0},
		{"kind": "note-write"},
		{"kind": "history-search", "entries": []any{rec{"headerLength": 1.0, "length": 2.0}}, "more": false},
		{"kind": "history-read", "offset": 0.0, "end": 1.0, "total": 2.0, "headerLength": 3.0, "imageOffset": 0.0},
	}
	for _, d := range good {
		if err := fxCheckDisplay(d); err != nil {
			t.Errorf("%v: %v", d, err)
		}
	}
	bad := []rec{
		{"kind": "context", "rollover": "sideways"},
		{"kind": "notes-list"},
		{"kind": "history-read", "offset": 0.0, "end": 1.0, "total": 2.0},
		{"kind": "unknown"},
	}
	for _, d := range bad {
		if err := fxCheckDisplay(d); err == nil {
			t.Errorf("%v should be invalid", d)
		}
	}
	res, err := fxParseResult([]byte(`{"content":[{"type":"text","text":"a"},{"type":"image","data":"AA==","mimeType":"image/png"}],"details":{"kind":"note-write"}}`))
	if err != nil || fxCheckResult(res) != nil || res.Text() != "a\n" || len(res.Images()) != 1 {
		t.Fatalf("parse/check: %+v %v", res, err)
	}
	if res, _ := fxParseResult([]byte(`{"content":"plain"}`)); res.Text() != "plain" {
		t.Fatalf("string content = %+v", res)
	}
}

func TestFixtureFakePolicyIsAPolicyContext(t *testing.T) {
	tokens := 10
	var ctx policyContext = fakePolicy{Settings: compactionPolicy{enabled: true, reserveTokens: 1}, Usage: &contextUsage{tokens: &tokens, contextWindow: 100}, Prompt: "p"}
	if s, _ := ctx.getSystemPrompt(); s != "p" {
		t.Fatalf("prompt = %q", s)
	}
}

// ---- fake session ---------------------------------------------------------------------------------------------

func TestFixtureSessionTreeAndBranching(t *testing.T) {
	s := fxMemorySession(t.TempDir())
	if s.getLeafID() != nil || s.getSessionDir() != "" || s.getSessionFile() != "" {
		t.Fatalf("a fresh in-memory session has no leaf, dir or file")
	}
	a := s.appendCustomEntry("old", rec{})
	b := s.appendMessage(fxUser("hi"))
	if e := s.getEntry(b); e["parentId"] != a || e["id"] != b {
		t.Fatalf("entry b = %v", e)
	}
	if first := s.getEntry(a); first["parentId"] != nil {
		t.Fatalf("root parentId = %v", first["parentId"])
	}
	s.branch(a)
	c := s.appendMessage(fxUser("fork"))
	br := s.getBranch(nil)
	if len(br) != 2 || fxID(br[0]) != a || fxID(br[1]) != c {
		t.Fatalf("branch after fork = %v", br)
	}
	if kids := s.getChildren(&a); len(kids) != 2 {
		t.Fatalf("children of a = %v", kids)
	}
	if es := s.getEntries(); len(es) != 3 {
		t.Fatalf("entries = %d", len(es))
	}
	if tree := s.getTree(); len(tree) != 1 {
		t.Fatalf("tree roots = %d", len(tree))
	}
	s.resetLeaf()
	if len(s.getBranch(nil)) != 0 || s.getLeafID() != nil {
		t.Fatalf("resetLeaf")
	}
	if len(s.buildSessionProjection()["entries"].([]any)) != 0 {
		t.Fatalf("a reset leaf projects nothing")
	}
}

func TestFixtureSessionProjectionMatchesPi(t *testing.T) {
	s := fxMemorySession(t.TempDir())
	for i := 0; i < 5; i++ {
		s.appendCustomEntry("old", rec{})
	}
	window := s.appendCompaction("carry on", "", 100, rec{"posthorse": float64(1)})
	if e := s.getEntry(window); e["firstKeptEntryId"] != window {
		t.Fatalf("firstKeptEntryId ?? id: %v", e["firstKeptEntryId"])
	}
	reminder := s.appendCustomMessageEntry("posthorse-reminder", "Checkpoint now", true, rec{"windowId": window})
	kept := s.appendMessage(fxUser("kept"))
	s.appendCompaction("native summary", reminder, 100, nil)
	ctxEntries := s.buildContextEntries()
	// Context entries: the newest compaction, then everything from firstKept (reminder, kept) up to it, nothing after.
	var ids []string
	for _, e := range ctxEntries {
		ids = append(ids, fxID(e))
	}
	if len(ctxEntries) != 3 || ctxEntries[1]["id"] != reminder || ctxEntries[2]["id"] != kept {
		t.Fatalf("context entries = %v", ids)
	}
	p := s.buildSessionProjection()
	entries := p["entries"].([]any)
	if len(entries) != 3 {
		t.Fatalf("projected entries = %d", len(entries))
	}
	first := entries[0].(rec)["messages"].([]any)
	if len(first) != 1 || first[0].(rec)["role"] != "compactionSummary" || first[0].(rec)["summary"] != "native summary" {
		t.Fatalf("compaction summary message = %v", first)
	}
	second := entries[1].(rec)["messages"].([]any)[0].(rec)
	if second["role"] != "custom" || second["customType"] != "posthorse-reminder" || second["display"] != true {
		t.Fatalf("custom message projection = %v", second)
	}
	if n := len(p["messages"].([]any)); n != 3 {
		t.Fatalf("projected messages = %d", n)
	}
	if p["thinkingLevel"] != "off" || p["model"] != nil {
		t.Fatalf("settings = %v %v", p["thinkingLevel"], p["model"])
	}
}

func TestFixtureSessionContextEditsAndBranchSummary(t *testing.T) {
	s := fxMemorySession(t.TempDir())
	u := s.appendMessage(fxUser("secret"))
	a := s.appendMessage(fxAssistant([]any{fxText("answer")}))
	s.appendModelChange("p", "m")
	s.appendThinkingLevelChange("high")
	if _, err := s.appendContextEdit(u, rec{"content": "redacted"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.appendContextEdit(a, rec{"content": "short"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.appendContextEdit("missing", nil); err == nil {
		t.Fatalf("an unknown target must fail")
	}
	p := s.buildSessionProjection()
	msgs := p["messages"].([]any)
	if msgs[0].(rec)["content"] != "redacted" {
		t.Fatalf("user edit = %v", msgs[0])
	}
	blocks := msgs[1].(rec)["content"].([]any)
	if blocks[0].(rec)["text"] != "short" {
		t.Fatalf("assistant edit becomes a text block: %v", msgs[1])
	}
	if p["thinkingLevel"] != "high" || p["model"].(rec)["modelId"] != "m" {
		t.Fatalf("thinking/model = %v / %v", p["thinkingLevel"], p["model"])
	}
	// A null replacement removes the message.
	if _, err := s.appendContextEdit(u, nil); err != nil {
		t.Fatal(err)
	}
	if n := len(s.buildSessionProjection()["messages"].([]any)); n != 1 {
		t.Fatalf("a null edit omits the message: %d", n)
	}
	sum := s.branchWithSummary(u, "abandoned path")
	if e := s.getEntry(sum); e["fromId"] == nil || e["parentId"] != u {
		t.Fatalf("branch summary = %v", e)
	}
	last := s.buildSessionProjection()["messages"].([]any)
	if m := last[len(last)-1].(rec); m["role"] != "branchSummary" || m["summary"] != "abandoned path" {
		t.Fatalf("branch summary message = %v", m)
	}
}

func TestFixturePersistedSessionWritesPiLayout(t *testing.T) {
	dir := t.TempDir()
	s, err := fxPersistedSession(dir, filepath.Join(dir, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	s.appendCustomEntry("setup", rec{})
	if _, err := os.Stat(s.getSessionFile()); !os.IsNotExist(err) {
		t.Fatalf("the file must not exist before a user/assistant message: %v", err)
	}
	s.appendMessage(fxUser("hello"))
	s.appendMessage(fxAssistant("world"))
	header, entries, err := fxReadSession(s.getSessionFile())
	if err != nil || header["id"] != s.sessionID() || header["cwd"] != dir || len(entries) != 3 {
		t.Fatalf("file = %v %v %v", header, entries, err)
	}
	raw, _ := os.ReadFile(s.getSessionFile())
	if !strings.HasPrefix(string(raw), `{"type":"session","version":3,"id":"`) || !strings.HasSuffix(string(raw), "\n") {
		t.Fatalf("layout = %q", raw)
	}
	opened, err := fxOpenSession(s.getSessionFile(), "")
	if err != nil || len(opened.getEntries()) != 3 || *opened.getLeafID() != *s.getLeafID() {
		t.Fatalf("open = %v %v", opened, err)
	}
	forked, err := fxForkSession(s.getSessionFile(), filepath.Join(dir, "child"), filepath.Join(dir, "forks"))
	if err != nil {
		t.Fatal(err)
	}
	if h := forked.getHeader(); h["parentSession"] != s.getSessionFile() || h["cwd"] != filepath.Join(dir, "child") || h["id"] == s.sessionID() {
		t.Fatalf("fork header = %v", h)
	}
	if len(forked.getEntries()) != 3 || forked.getSessionDir() != filepath.Join(dir, "forks") {
		t.Fatalf("fork entries/dir = %d %q", len(forked.getEntries()), forked.getSessionDir())
	}
	forked.appendMessage(fxUser("more"))
	if _, entries, _ := fxReadSession(forked.getSessionFile()); len(entries) != 4 {
		t.Fatalf("an append to an opened session appends a line: %d", len(entries))
	}
}

func TestFixtureArchiveHelpers(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.jsonl")
	body := fxJSONL(rec{"type": "message", "id": "m1", "parentId": nil, "message": fxUser("x < y & z")})
	if err := fxWriteArchive(file, body); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(file)
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	wd, _ := os.Getwd()
	if lines[0] != fxStringify(rec{"type": "session", "id": file, "cwd": wd}) || len(lines) != 2 {
		t.Fatalf("archive = %q", raw)
	}
	if !strings.Contains(lines[1], "x < y & z") || !strings.HasPrefix(lines[1], `{"type":"message","id":"m1","parentId":null,`) {
		t.Fatalf("entry line (no HTML escaping, Pi key order) = %q", lines[1])
	}
	short, long := fxArchivedID("m1", file, false), fxArchivedID("m1", file, true)
	if !strings.HasPrefix(short, "m1@") || len(short) != len("m1@")+10 || !strings.HasPrefix(long, short) || len(long) != len("m1@")+43 {
		t.Fatalf("archived ids = %q / %q", short, long)
	}
	h := fxHeader("s1", dir, "/parent.jsonl")
	if h["parentSession"] != "/parent.jsonl" || fxHeader("s1", dir, "")["parentSession"] != nil {
		t.Fatalf("header = %v", h)
	}
	if e := fxBoundary("b", "carry", rec{"reason": "explicit"}); e["details"].(rec)["posthorse"] != float64(1) || e["details"].(rec)["reason"] != "explicit" || e["firstKeptEntryId"] != nil {
		t.Fatalf("boundary = %v", e)
	}
	if e := fxCompaction("c", "s", "k", nil); e["firstKeptEntryId"] != "k" || e["details"] != nil {
		t.Fatalf("compaction = %v", e)
	}
	if e := fxWithParent(fxMessageEntry("x", fxUser("y")), "p"); e["parentId"] != "p" {
		t.Fatalf("withParent = %v", e)
	}
}

// TestExtensionStartsOnTheFakeHost starts the real port on the fixture (kept out of the -run Fixture set: it depends on
// the port being complete): its four tools and the default policy reader (the fixture's compaction settings) answer
// through the fake host.
func TestExtensionStartsOnTheFakeHost(t *testing.T) {
	f := newFixture(t)
	want := map[string]bool{"new_context": true, "get_context_remaining": true, "notes": true, "history": true}
	for _, name := range f.ToolNames() {
		delete(want, name)
	}
	if len(want) != 0 {
		t.Fatalf("tools not registered by the extension: %v (have %v)", want, f.ToolNames())
	}
	res := f.Run("get_context_remaining", rec{})
	if !strings.Contains(res.Text(), "100,000") {
		t.Fatalf("get_context_remaining = %q", res.Text())
	}
}
