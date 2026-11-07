package langsmith_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	langsmith "github.com/MichaelKinsy/pigpen/langsmith"
)

// Hook-level tests of the state machine of src/index.ts beyond what upstream's replays reach.

// fireErr delivers an event like Host.Fire but returns the handler's failure instead of failing the test.
func (h *Host) fireErr(event string, data map[string]any) string {
	h.t.Helper()
	h.mu.Lock()
	id, ok := h.handlers[event]
	h.mu.Unlock()
	if !ok {
		return "no handler for " + event
	}
	if data == nil {
		data = map[string]any{}
	}
	data = withPromptOptions(event, data)
	data["type"] = event
	args, _ := json.Marshal(data)
	_, failure := h.roundTrip(map[string]any{"method": "event", "event": event, "handler_id": id, "args": json.RawMessage(args)})
	return failure
}

// runs returns the decoded POST and PATCH bodies in arrival order, as ("POST"|"PATCH", body).
type lsRun struct {
	Method string
	ID     string
	Body   map[string]any
}

func (r *rig) runs() []lsRun {
	var out []lsRun
	for _, c := range r.ls.Calls() {
		var body map[string]any
		if json.Unmarshal(c.Body, &body) != nil {
			continue
		}
		path := strings.TrimPrefix(c.Path, "/api/v1")
		switch {
		case c.Method == "POST" && path == "/runs":
			id, _ := body["id"].(string)
			out = append(out, lsRun{"POST", id, body})
		case c.Method == "PATCH" && strings.HasPrefix(path, "/runs/"):
			out = append(out, lsRun{"PATCH", strings.TrimPrefix(path, "/runs/"), body})
		}
	}
	return out
}

func (r *rig) posts() []map[string]any {
	var out []map[string]any
	for _, x := range r.runs() {
		if x.Method == "POST" {
			out = append(out, x.Body)
		}
	}
	return out
}

// patchOf is the last PATCH of the run with the given id.
func (r *rig) patchOf(id string) map[string]any {
	var out map[string]any
	for _, x := range r.runs() {
		if x.Method == "PATCH" && x.ID == id {
			out = x.Body
		}
	}
	return out
}

func (r *rig) postNamed(prefix string, n int) map[string]any {
	r.t.Helper()
	for _, p := range r.posts() {
		if name, _ := p["name"].(string); strings.HasPrefix(name, prefix) {
			if n == 0 {
				return p
			}
			n--
		}
	}
	r.t.Fatalf("no POST named %q (#%d)", prefix, n)
	return nil
}

func id(m map[string]any) string { s, _ := m["id"].(string); return s }

func mdOf(m map[string]any) map[string]any {
	extra, _ := m["extra"].(map[string]any)
	md, _ := extra["metadata"].(map[string]any)
	return md
}

func (r *rig) fire(event, data string) {
	r.t.Helper()
	r.h.Fire(event, withPromptOptions(event, jsonOf(r.t, data)))
}

// withPromptOptions adds the `systemPromptOptions` Pi sends with before_agent_start and the SDK requires.
func withPromptOptions(event string, data map[string]any) map[string]any {
	if event == "before_agent_start" {
		if _, ok := data["systemPromptOptions"]; !ok {
			data["systemPromptOptions"] = map[string]any{"cwd": "/w"}
		}
	}
	return data
}

const stopMessage = `{"role":"assistant","stopReason":"stop","content":[{"type":"text","text":"ok"}],"usage":{"input":1,"output":2,"totalTokens":3}}`

func TestSessionStartStatusAndCommand(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.fire("session_start", `{"reason":"startup"}`)
	if got := r.statuses(); len(got) != 1 || got[0] != "langsmith=LangSmith: tracing" {
		t.Fatalf("%v", got)
	}
	if f := r.h.Command("langsmith-tracing", ""); f != "" {
		t.Fatal(f)
	}
	r.fire("before_agent_start", `{"prompt":"p"}`)
	root := r.postNamed("Pi agent run", 0)
	if f := r.h.Command("langsmith-tracing", ""); f != "" {
		t.Fatal(f)
	}
	want := [][2]string{
		{"LangSmith tracing is enabled.", "info"},
		{"LangSmith tracing is enabled (active trace " + id(root) + ").", "info"},
	}
	if got := r.notifies(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
	if got := r.statuses(); got[len(got)-1] != "langsmith=LangSmith: tracing run" {
		t.Fatalf("%v", got)
	}
}

func TestDisabledRegistersOnlyStatusAndCommand(t *testing.T) {
	for _, env := range []map[string]string{{"TRACE_TO_LANGSMITH": "false"}, {"TRACE_TO_LANGSMITH": ""}, {"TRACE_TO_LANGSMITH": "maybe"}} {
		r := newRig(t, rigOptions{Env: env})
		for _, ev := range []string{"before_agent_start", "session_compact", "turn_start", "before_provider_request", "message_end", "tool_execution_start", "tool_execution_update", "tool_execution_end", "turn_end", "agent_end", "agent_settled", "session_shutdown"} {
			if r.h.Registered(ev) {
				t.Errorf("%v: %s must not be registered while disabled", env, ev)
			}
		}
		r.fire("session_start", `{}`)
		if f := r.h.Command("langsmith-tracing", ""); f != "" {
			t.Fatal(f)
		}
		if got := r.statuses(); len(got) != 1 || got[0] != "langsmith=LangSmith: disabled" {
			t.Fatalf("%v", got)
		}
		want := "LangSmith tracing is disabled. Set TRACE_TO_LANGSMITH=true and configure LANGSMITH_PI_API_KEY (or LANGSMITH_PI_ENDPOINT for self-hosted) to enable it."
		if got := r.notifies(); len(got) != 1 || got[0] != [2]string{want, "warning"} {
			t.Fatalf("%v", got)
		}
		if len(r.ls.Calls()) != 0 {
			t.Fatal("a disabled extension sends nothing")
		}
	}
}

func TestEnabledRegistersEveryHook(t *testing.T) {
	r := newRig(t, rigOptions{})
	for _, ev := range []string{"session_start", "before_agent_start", "session_compact", "turn_start", "before_provider_request", "message_end", "tool_execution_start", "tool_execution_update", "tool_execution_end", "turn_end", "agent_end", "agent_settled", "session_shutdown"} {
		if !r.h.Registered(ev) {
			t.Errorf("%s is not registered", ev)
		}
	}
}

func TestEnabledFromConfigFile(t *testing.T) {
	r := newRig(t, rigOptions{Env: map[string]string{"TRACE_TO_LANGSMITH": ""}, CwdConfig: `{"enabled":true,"project":"from-file"}`})
	r.fire("before_agent_start", `{"prompt":"p"}`)
	if got := r.postNamed("Pi agent run", 0)["session_name"]; got != "from-file" {
		t.Fatalf("session_name %v", got)
	}
}

func TestConfigDefaultProjectApplies(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.fire("before_agent_start", `{"prompt":"p"}`)
	if got := r.postNamed("Pi agent run", 0)["session_name"]; got != "pi-coding-agent" {
		t.Fatalf("session_name %v", got)
	}
}

func TestRootRunsAreNewTraces(t *testing.T) {
	r := newRig(t, rigOptions{})
	for i := 0; i < 2; i++ {
		r.fire("before_agent_start", `{"prompt":"p"}`)
	}
	for i := 0; i < 2; i++ {
		root := r.postNamed("Pi agent run", i)
		if _, has := root["parent_run_id"]; has {
			t.Errorf("root %d has a parent", i)
		}
		if root["trace_id"] != root["id"] {
			t.Errorf("root %d: trace_id %v id %v", i, root["trace_id"], root["id"])
		}
	}
}

func TestRootRunPayload(t *testing.T) {
	r := newRig(t, rigOptions{Env: map[string]string{"LANGSMITH_PI_METADATA": `{"ls_integration":"custom","team":"x"}`, "LANGSMITH_PI_PROJECT": "proj"}})
	r.fire("before_agent_start", `{"prompt":"hello","images":[{"a":1},{"b":2}]}`)
	root := r.postNamed("Pi agent run", 0)
	if root["run_type"] != "chain" || root["session_name"] != "proj" {
		t.Fatalf("%v", root)
	}
	if got := langsmith.Stringify(root["inputs"]); got != `{"imageCount":2,"prompt":"hello"}` {
		t.Fatalf("%s", got)
	}
	if tags, _ := json.Marshal(root["tags"]); string(tags) != `["pi","coding-agent"]` {
		t.Fatalf("%s", tags)
	}
	md := mdOf(root)
	if md["ls_integration"] != "custom" || md["team"] != "x" || md["ls_agent_type"] != "root" || md["thread_id"] != "sess_test" {
		t.Fatalf("config metadata overrides the coding-agent metadata: %v", md)
	}
}

func TestImageCountIsTheJSLength(t *testing.T) {
	for in, want := range map[string]float64{`{"prompt":"p"}`: 0, `{"prompt":"p","images":[]}`: 0, `{"prompt":"p","images":null}`: 0, `{"prompt":"p","images":"abc"}`: 3, `{"prompt":"p","images":"😀"}`: 2, `{"prompt":"p","images":5}`: 0} {
		r := newRig(t, rigOptions{})
		r.fire("before_agent_start", in)
		inputs, _ := r.postNamed("Pi agent run", 0)["inputs"].(map[string]any)
		if inputs["imageCount"] != want {
			t.Errorf("%s: imageCount %v, want %v", in, inputs["imageCount"], want)
		}
	}
}

func TestReplicasReceiveTheRuns(t *testing.T) {
	// The replica points at the loopback endpoint of the rig under test.
	mock := newMockLS(t)
	r2 := newRig(t, rigOptions{Env: map[string]string{"LANGSMITH_PI_RUNS_ENDPOINTS": `[{"apiUrl":"` + mock.srv.URL + `","apiKey":"rk","projectName":"replica-proj","updates":{"extra":{"metadata":{"u":1}}}}]`}})
	r2.fire("before_agent_start", `{"prompt":"p"}`)
	r2.fire("agent_end", `{"messages":[{"role":"assistant","stopReason":"stop","content":[]}]}`)
	r2.fire("agent_settled", `{}`)
	r2.flush()
	var posts int
	for _, c := range mock.Calls() {
		if c.Method == "POST" {
			posts++
			var body map[string]any
			_ = json.Unmarshal(c.Body, &body)
			if body["session_name"] != "replica-proj" {
				t.Errorf("replica session_name %v", body["session_name"])
			}
			if c.Header.Get("x-api-key") != "rk" {
				t.Errorf("replica api key %q", c.Header.Get("x-api-key"))
			}
		}
	}
	if posts != 1 {
		t.Fatalf("replica POSTs: %d", posts)
	}
	for _, c := range r2.ls.Calls() {
		if c.Method == "POST" {
			t.Fatal("with replicas configured only the replicas receive runs")
		}
	}
}

func TestInterruptedByNextRun(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.fire("before_agent_start", `{"prompt":"one"}`)
	first := id(r.postNamed("Pi agent run", 0))
	r.fire("before_agent_start", `{"prompt":"two"}`)
	p := r.patchOf(first)
	if p["error"] != "Trace replaced by a new Pi run" || langsmith.Stringify(p["outputs"]) != `{"interruptedByNextRun":true}` {
		t.Fatalf("%v", p)
	}
	if got := r.statuses(); got[len(got)-1] != "langsmith=LangSmith: tracing run" {
		t.Fatalf("%v", got)
	}
}

func TestStaleSettleIsSkippedAndNextSettleEndsTheRun(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.fire("before_agent_start", `{"prompt":"one"}`)
	first := id(r.postNamed("Pi agent run", 0))
	r.fire("agent_end", `{"messages":[{"role":"assistant","stopReason":"stop","content":[]}]}`)
	// The next prompt starts before the first one's settle arrives: the first ends with its real result.
	r.fire("before_agent_start", `{"prompt":"two"}`)
	p := r.patchOf(first)
	if _, has := p["error"]; has {
		t.Fatalf("the first run keeps its loop result: %v", p)
	}
	if _, has := p["outputs"].(map[string]any)["messages"]; !has {
		t.Fatalf("%v", p)
	}
	second := id(r.postNamed("Pi agent run", 1))
	r.fire("agent_settled", `{}`) // the late one
	if r.patchOf(second) != nil {
		t.Fatal("the late settle must not end the second run")
	}
	r.fire("agent_end", `{"messages":[{"role":"assistant","stopReason":"aborted","content":[]}]}`)
	r.fire("agent_settled", `{}`)
	p2 := r.patchOf(second)
	if p2["error"] != "Assistant message was aborted" {
		t.Fatalf("%v", p2)
	}
	if got := r.statuses(); got[len(got)-1] != "langsmith=LangSmith: traced" {
		t.Fatalf("%v", got)
	}
	// With nothing active a settle does nothing.
	n := len(r.ls.Calls())
	r.fire("agent_settled", `{}`)
	if len(r.ls.Calls()) != n {
		t.Fatal("a settle without a run sends nothing")
	}
}

func TestSettleWithoutAgentEndIsIncomplete(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.fire("before_agent_start", `{"prompt":"one"}`)
	r.fire("agent_settled", `{}`)
	p := r.patchOf(id(r.postNamed("Pi agent run", 0)))
	if langsmith.Stringify(p["outputs"]) != `{"incomplete":true}` {
		t.Fatalf("%v", p)
	}
}

func TestAgentEndWithoutSettledSupportEndsTheRunAtOnce(t *testing.T) {
	r := newRig(t, rigOptions{PiVersion: "0.79.0"})
	r.setUsage(map[string]any{"tokens": 10, "contextWindow": 100, "percent": 10})
	r.fire("before_agent_start", `{"prompt":"one"}`)
	r.fire("agent_end", `{"messages":[{"role":"user","content":"u"},{"role":"assistant","stopReason":"error","errorMessage":"boom","content":[]}]}`)
	p := r.patchOf(id(r.postNamed("Pi agent run", 0)))
	if p["error"] != "boom" {
		t.Fatalf("%v", p)
	}
	outputs := p["outputs"].(map[string]any)
	if langsmith.Stringify(outputs["context_usage"]) != `{"contextWindow":100,"percent":10,"tokens":10}` {
		t.Fatalf("%v", outputs["context_usage"])
	}
	if got := r.statuses(); got[len(got)-1] != "langsmith=LangSmith: traced" {
		t.Fatalf("%v", got)
	}
	// Ended and cleared: a second agent_end is a no-op.
	n := len(r.ls.Calls())
	r.fire("agent_end", `{"messages":[]}`)
	if len(r.ls.Calls()) != n {
		t.Fatal("no active run")
	}
}

func TestAgentEndErrorBranches(t *testing.T) {
	for _, c := range []struct{ messages, wantErr string }{
		{`[]`, ""},
		{`[{"role":"assistant","stopReason":"stop","content":[]}]`, ""},
		{`[{"role":"assistant","stopReason":"error","content":[]}]`, ""},
		{`[{"role":"assistant","stopReason":"error","errorMessage":"e1","content":[]}]`, "e1"},
		{`[{"role":"assistant","stopReason":"aborted","content":[]}]`, "Assistant message was aborted"},
		{`[{"role":"user","stopReason":"error","errorMessage":"no","content":"x"}]`, ""},
	} {
		r := newRig(t, rigOptions{PiVersion: "0.1.0"})
		r.fire("before_agent_start", `{"prompt":"one"}`)
		r.fire("agent_end", `{"messages":`+c.messages+`}`)
		p := r.patchOf(id(r.postNamed("Pi agent run", 0)))
		got, _ := p["error"].(string)
		if got != c.wantErr {
			t.Errorf("%s: error %q, want %q", c.messages, got, c.wantErr)
		}
	}
}

func TestOutstandingToolsAndLlmEndWithAgentEnd(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.fire("before_agent_start", `{"prompt":"p"}`)
	r.fire("turn_start", `{"turnIndex":0,"timestamp":5}`)
	r.fire("before_provider_request", `{"payload":{"messages":[{"role":"user","content":"hi"}]}}`)
	r.fire("tool_execution_start", `{"toolCallId":"a","toolName":"bash","args":{}}`)
	r.fire("tool_execution_start", `{"toolCallId":"b","toolName":"read","args":{}}`)
	r.fire("agent_end", `{"messages":[]}`)
	for _, name := range []string{"bash", "read"} {
		if got := r.patchOf(id(r.postNamed(name, 0)))["error"]; got != "Pi run ended before tool_execution_end" {
			t.Errorf("%s: %v", name, got)
		}
	}
	llm := id(r.postNamed("anthropic", 0))
	if got := r.patchOf(llm)["error"]; got != "Pi run ended before LLM message finalized" {
		t.Fatalf("%v", got)
	}
	turn := r.patchOf(id(r.postNamed("Pi turn 0", 0)))
	if langsmith.Stringify(turn["outputs"]) != `{"incomplete":true}` {
		t.Fatalf("%v", turn)
	}
}

func TestDeferredLlmStartsInTheNextTurn(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.fire("before_agent_start", `{"prompt":"p"}`)
	r.fire("turn_start", `{"turnIndex":0,"timestamp":1}`)
	r.fire("before_provider_request", `{"payload":{"messages":[{"role":"user","content":"hi"}]}}`)
	r.fire("message_end", `{"message":`+stopMessage+`}`)
	// A request after the assistant message belongs to the next turn: it is held until turn_start.
	r.fire("before_provider_request", `{"payload":{"messages":[{"role":"user","content":"again"}]}}`)
	var names []string
	for _, p := range r.posts() {
		names = append(names, p["name"].(string))
	}
	if fmt.Sprint(names) != "[Pi agent run Pi turn 0 anthropic]" {
		t.Fatalf("%v", names)
	}
	r.fire("turn_start", `{"turnIndex":1,"timestamp":2}`)
	names = nil
	for _, p := range r.posts() {
		names = append(names, p["name"].(string))
	}
	if fmt.Sprint(names) != "[Pi agent run Pi turn 0 anthropic Pi turn 1 anthropic]" {
		t.Fatalf("%v", names)
	}
	if r.postNamed("anthropic", 1)["parent_run_id"] != id(r.postNamed("Pi turn 1", 0)) {
		t.Fatal("the held request starts under the turn that started")
	}
}

func TestPendingLlmIsStartedByAgentEndAndShutdown(t *testing.T) {
	for _, event := range []string{"agent_end", "session_shutdown"} {
		r := newRig(t, rigOptions{})
		r.fire("before_agent_start", `{"prompt":"p"}`)
		r.fire("turn_start", `{"turnIndex":0,"timestamp":1}`)
		r.fire("before_provider_request", `{"payload":{"messages":[]}}`)
		r.fire("message_end", `{"message":`+stopMessage+`}`)
		r.fire("before_provider_request", `{"payload":{"messages":[{"role":"user","content":"held"}]}}`)
		if len(r.posts()) != 3 {
			t.Fatalf("%s: %d posts before", event, len(r.posts()))
		}
		r.fire(event, `{"messages":[]}`)
		held := r.postNamed("anthropic", 1)
		// The turn is still current (no turn_end): the held request goes under it.
		if held["parent_run_id"] != id(r.postNamed("Pi turn 0", 0)) {
			t.Errorf("%s: parent %v", event, held["parent_run_id"])
		}
		wantErr := "Pi run ended before LLM message finalized"
		if event == "session_shutdown" {
			wantErr = "Pi session shut down before LLM message finalized"
		}
		if got := r.patchOf(id(held))["error"]; got != wantErr {
			t.Errorf("%s: %v", event, got)
		}
	}
}

func TestShutdownEndsTheRunAndClearsStatus(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.fire("session_start", `{}`)
	r.fire("before_agent_start", `{"prompt":"p"}`)
	r.fire("session_shutdown", `{"reason":"quit"}`)
	root := r.patchOf(id(r.postNamed("Pi agent run", 0)))
	if root["error"] != "Pi session shut down before run completed" || langsmith.Stringify(root["outputs"]) != `{"shutdown":true}` {
		t.Fatalf("%v", root)
	}
	calls := r.h.CallsTo("ui.setStatus")
	last := calls[len(calls)-1]
	if _, has := last.Args["text"]; has || last.Args["key"] != "langsmith" {
		t.Fatalf("the status must be cleared (setStatus with undefined): %v", last.Args)
	}
	// Everything was sent before the handler returned.
	if len(r.patchRunsCount()) == 0 {
		t.Fatal("pending batches were not awaited")
	}
}

func (r *rig) patchRunsCount() []lsRun {
	var out []lsRun
	for _, x := range r.runs() {
		if x.Method == "PATCH" {
			out = append(out, x)
		}
	}
	return out
}

func TestShutdownAfterAgentEndKeepsTheLoopResult(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.fire("before_agent_start", `{"prompt":"p"}`)
	r.fire("agent_end", `{"messages":[{"role":"assistant","stopReason":"stop","content":[]}]}`)
	r.fire("session_shutdown", `{}`)
	root := r.patchOf(id(r.postNamed("Pi agent run", 0)))
	if _, has := root["error"]; has {
		t.Fatalf("%v", root)
	}
	if _, has := root["outputs"].(map[string]any)["messages"]; !has {
		t.Fatalf("%v", root)
	}
}

func TestMessageEndBranches(t *testing.T) {
	setup := func() *rig {
		r := newRig(t, rigOptions{})
		r.fire("before_agent_start", `{"prompt":"p"}`)
		r.fire("turn_start", `{"turnIndex":0,"timestamp":1}`)
		r.fire("before_provider_request", `{"payload":{"messages":[]}}`)
		return r
	}
	for _, c := range []struct{ message, wantErr string }{
		{`{"role":"assistant","stopReason":"error","errorMessage":"quota","content":[]}`, "quota"},
		{`{"role":"assistant","stopReason":"error","content":[]}`, "Assistant message ended with stopReason=error"},
		{`{"role":"assistant","stopReason":"error","errorMessage":null,"content":[]}`, "Assistant message ended with stopReason=error"},
		{`{"role":"assistant","stopReason":"toolUse","content":[]}`, ""},
	} {
		r := setup()
		r.fire("message_end", `{"message":`+c.message+`}`)
		p := r.patchOf(id(r.postNamed("anthropic", 0)))
		got, _ := p["error"].(string)
		if got != c.wantErr {
			t.Errorf("%s: error %q want %q", c.message, got, c.wantErr)
		}
		md := mdOf(p)
		if md["stop_reason"] == nil {
			t.Errorf("%s: stop_reason missing: %v", c.message, md)
		}
	}
	// Not an assistant message, or no LLM run open: nothing is sent.
	r := setup()
	n := len(r.ls.Calls())
	r.fire("message_end", `{"message":{"role":"user","content":"x"}}`)
	if len(r.ls.Calls()) != n {
		t.Fatal("a user message_end sends nothing")
	}
	r.fire("message_end", `{"message":`+stopMessage+`}`)
	n = len(r.ls.Calls())
	r.fire("message_end", `{"message":`+stopMessage+`}`)
	if len(r.ls.Calls()) != n {
		t.Fatal("without an open LLM run nothing is sent")
	}
}

func TestToolRunLifecycle(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.fire("before_agent_start", `{"prompt":"p"}`)
	r.fire("turn_start", `{"turnIndex":0,"timestamp":1}`)
	r.fire("tool_execution_start", `{"toolCallId":"c1","toolName":"bash","args":{"command":"ls"}}`)
	r.fire("tool_execution_update", `{"toolCallId":"c1","partialResult":{"content":"par"}}`)
	r.fire("tool_execution_update", `{"toolCallId":"unknown","partialResult":1}`)
	r.fire("tool_execution_end", `{"toolCallId":"c1","result":{"content":"out","details":{"a":1}},"isError":true}`)
	r.fire("tool_execution_end", `{"toolCallId":"c1","result":"again","isError":false}`) // unknown now: ignored
	tool := r.postNamed("bash", 0)
	if tool["parent_run_id"] != id(r.postNamed("Pi turn 0", 0)) || tool["run_type"] != "tool" {
		t.Fatalf("%v", tool)
	}
	if got := langsmith.Stringify(tool["inputs"]); got != `{"args":{"command":"ls"},"toolCallId":"c1"}` {
		t.Fatalf("%s", got)
	}
	md := mdOf(tool)
	if md["toolName"] != "bash" || md["toolCallId"] != "c1" {
		t.Fatalf("%v", md)
	}
	p := r.patchOf(id(tool))
	if p["error"] != "Tool execution failed" {
		t.Fatalf("%v", p)
	}
	if got := langsmith.Stringify(p["outputs"]); got != `{"output":{"content":"out","details":{"a":1},"role":"tool"}}` {
		t.Fatalf("%s", got)
	}
	events, _ := p["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("events %v", p["events"])
	}
	ev := events[0].(map[string]any)
	if ev["name"] != "tool_update" || langsmith.Stringify(ev["kwargs"]) != `{"partialResult":{"content":"par"}}` {
		t.Fatalf("%v", ev)
	}
}

func TestToolRunFallsBackToTheRootWithoutATurn(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.fire("before_agent_start", `{"prompt":"p"}`)
	r.fire("tool_execution_start", `{"toolCallId":"c1","toolName":"bash","args":{}}`)
	r.fire("before_provider_request", `{"payload":{"messages":[]}}`)
	root := id(r.postNamed("Pi agent run", 0))
	if r.postNamed("bash", 0)["parent_run_id"] != root || r.postNamed("anthropic", 0)["parent_run_id"] != root {
		t.Fatal("without a turn, tool and llm runs hang under the root")
	}
}

func TestTurnEndFindsTheTurnByIndexOrCurrent(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.fire("before_agent_start", `{"prompt":"p"}`)
	r.fire("turn_start", `{"turnIndex":0,"timestamp":1}`)
	r.fire("turn_start", `{"turnIndex":1,"timestamp":2}`)
	// turn_end for index 0 ends turn 0 (by index) and leaves turn 1 current.
	r.fire("turn_end", `{"turnIndex":0,"message":`+stopMessage+`,"toolResults":[{"x":1}]}`)
	t0 := r.patchOf(id(r.postNamed("Pi turn 0", 0)))
	if t0 == nil || langsmith.Stringify(t0["outputs"]) == "" {
		t.Fatal("turn 0 not ended")
	}
	if r.patchOf(id(r.postNamed("Pi turn 1", 0))) != nil {
		t.Fatal("turn 1 must stay open")
	}
	// An unknown index falls back to the current turn (1).
	r.fire("turn_end", `{"turnIndex":9,"message":`+stopMessage+`,"toolResults":[]}`)
	if r.patchOf(id(r.postNamed("Pi turn 1", 0))) == nil {
		t.Fatal("the current turn is the fallback")
	}
	if got := langsmith.Stringify(t0["outputs"]); !strings.Contains(got, `"toolResults":[{"x":1}]`) {
		t.Fatalf("%s", got)
	}
}

func TestTurnStartMetadata(t *testing.T) {
	r := newRig(t, rigOptions{Model: map[string]any{"id": "m", "name": "İSTANBUL ΑΣ", "provider": "prov"}})
	r.fire("before_agent_start", `{"prompt":"p"}`)
	r.fire("turn_start", `{"turnIndex":3,"timestamp":1791559432089}`)
	turn := r.postNamed("Pi turn 3", 0)
	md := mdOf(turn)
	if md["ls_provider"] != "prov" || md["ls_model_name"] != "i\u0307stanbul ας" || md["timestamp"] != float64(1791559432089) || md["pi_loop_index"] != float64(3) {
		t.Fatalf("%v", md)
	}
	if langsmith.Stringify(turn["inputs"]) != `{"turnIndex":3}` {
		t.Fatalf("%v", turn["inputs"])
	}
}

func TestProviderRequestNaming(t *testing.T) {
	for _, c := range []struct {
		model map[string]any
		name  string
		md    map[string]any
	}{
		{map[string]any{"id": "m", "name": "GPT", "provider": "openai"}, "openai", map[string]any{"ls_provider": "openai", "ls_model_name": "gpt"}},
		{map[string]any{}, "provider request", map[string]any{}},
		{map[string]any{"id": "m", "provider": "x"}, "x", map[string]any{"ls_provider": "x"}},
	} {
		r := newRig(t, rigOptions{Model: c.model})
		r.fire("before_agent_start", `{"prompt":"p"}`)
		r.fire("before_provider_request", `{"payload":{"model":"m"}}`)
		llm := r.postNamed(c.name, 0)
		md := mdOf(llm)
		for k, v := range c.md {
			if md[k] != v {
				t.Errorf("%v: %s = %v, want %v", c.model, k, md[k], v)
			}
		}
		if len(c.md) == 0 {
			if _, has := md["ls_provider"]; has {
				t.Errorf("no model: ls_provider must be undefined: %v", md)
			}
		}
	}
}

func TestCompactionRun(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.fire("session_compact", `{"reason":"manual","willRetry":false,"compactionEntry":{"summary":"s","tokensBefore":5}}`)
	if len(r.ls.Calls()) != 0 {
		t.Fatal("a compaction without an active trace sends nothing")
	}
	r.fire("before_agent_start", `{"prompt":"p"}`)
	r.fire("session_compact", `{"reason":"manual","willRetry":false,"compactionEntry":{"summary":"s","tokensBefore":5}}`)
	run := r.postNamed("Context Compaction (manual)", 0)
	if run["run_type"] != "chain" || mdOf(run)["ls_agent_type"] != "compaction" || run["parent_run_id"] != id(r.postNamed("Pi agent run", 0)) {
		t.Fatalf("%v", run)
	}
	if got := langsmith.Stringify(run["inputs"]); got != `{"reason":"manual","willRetry":false}` {
		t.Fatalf("%s", got)
	}
	if got := langsmith.Stringify(r.patchOf(id(run))["outputs"]); got != `{"summary":"s","tokensBefore":5}` {
		t.Fatalf("%s", got)
	}
}

func TestHookErrorsAreReturned(t *testing.T) {
	r := newRig(t, rigOptions{NoSession: true})
	if f := r.h.fireErr("before_agent_start", map[string]any{"prompt": "p"}); !strings.Contains(f, "no session") {
		t.Fatalf("a host that cannot name the session fails the hook: %q", f)
	}
	r2 := newRig(t, rigOptions{})
	r2.fire("before_agent_start", `{"prompt":"p"}`)
	if f := r2.h.fireErr("message_end", map[string]any{}); !strings.Contains(f, "Cannot read properties of undefined (reading 'role')") {
		t.Fatalf("message_end without a message: %q", f)
	}
	if f := r2.h.fireErr("agent_end", map[string]any{}); !strings.Contains(f, "(reading 'at')") {
		t.Fatalf("agent_end without messages: %q", f)
	}
	if f := r2.h.fireErr("turn_end", map[string]any{"turnIndex": 0}); f == "" {
		t.Fatal("turn_end without a message must fail like convertMessages([undefined])")
	}
	if f := r2.h.fireErr("session_compact", map[string]any{"reason": "x"}); !strings.Contains(f, "reading 'summary'") {
		t.Fatalf("%q", f)
	}
	r2.fire("turn_start", `{"turnIndex":0,"timestamp":1}`)
	if f := r2.h.fireErr("before_provider_request", map[string]any{"payload": jsonAny(t, `{"system":"s","messages":[null]}`)}); !strings.Contains(f, "(reading 'role')") {
		t.Fatalf("a null message makes convertProviderPayload throw: %q", f)
	}
}

func TestHooksWithoutAnActiveRunAreNoOps(t *testing.T) {
	r := newRig(t, rigOptions{})
	for ev, data := range map[string]string{
		"session_compact": `{"reason":"x","compactionEntry":{}}`, "turn_start": `{"turnIndex":0}`, "before_provider_request": `{"payload":{}}`,
		"message_end": `{"message":` + stopMessage + `}`, "tool_execution_start": `{"toolCallId":"a","toolName":"b"}`, "tool_execution_update": `{"toolCallId":"a"}`,
		"tool_execution_end": `{"toolCallId":"a"}`, "turn_end": `{"turnIndex":0,"message":` + stopMessage + `}`, "agent_end": `{"messages":[]}`, "agent_settled": `{}`,
	} {
		r.fire(ev, data)
	}
	if len(r.ls.Calls()) != 0 || len(r.h.CallsTo("ui.setStatus")) != 0 {
		t.Fatal("no active run: nothing happens")
	}
}

func TestConflictingRunsEndpointsRejectTheHook(t *testing.T) {
	// The SDK throws on LANGSMITH_RUNS_ENDPOINTS together with LANGSMITH_ENDPOINT; the rejection reaches the host as a
	// hook failure and no run is created.
	r := newRig(t, rigOptions{})
	t.Setenv("LANGSMITH_RUNS_ENDPOINTS", `{"https://a.example":"k"}`)
	t.Setenv("LANGSMITH_ENDPOINT", "https://b.example")
	if f := r.h.fireErr("before_agent_start", map[string]any{"prompt": "p"}); !strings.Contains(f, "You cannot provide both LANGSMITH_ENDPOINT") {
		t.Fatalf("got %q", f)
	}
	if len(r.ls.Calls()) != 0 {
		t.Fatal("no run may be sent")
	}
}
