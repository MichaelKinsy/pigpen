package langsmith_test

// Tests added by the mutation proof (port/mutations.md). Each one kills mutants of extension.go that the twins and the
// hook tests above left alive: ordering and key order of the payloads (read from the raw request bytes, which the
// decoded-map helpers cannot see), state that must be cleared (an extra PATCH or POST shows when it is not), and the
// host-visible registration and factory text.

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	langsmith "github.com/MichaelKinsy/pigpen/langsmith"
)

// rawPatch is the raw body of the last PATCH of run id.
func (r *rig) rawPatch(id string) string {
	var out string
	for _, c := range r.ls.Calls() {
		if c.Method == "PATCH" && strings.HasSuffix(c.Path, "/runs/"+id) {
			out = string(c.Body)
		}
	}
	return out
}

// rawPost is the raw body of the POST of run id.
func (r *rig) rawPost(id string) string {
	for _, c := range r.ls.Calls() {
		if c.Method == "POST" && strings.HasSuffix(c.Path, "/runs") && strings.Contains(string(c.Body), `"id":"`+id+`"`) {
			return string(c.Body)
		}
	}
	return ""
}

func (r *rig) patchCount(id string) int {
	n := 0
	for _, x := range r.runs() {
		if x.Method == "PATCH" && x.ID == id {
			n++
		}
	}
	return n
}

func (r *rig) postCount(name string) int {
	n := 0
	for _, p := range r.posts() {
		if p["name"] == name {
			n++
		}
	}
	return n
}

// failCall makes the host answer the call `method` with an error.
func (r *rig) failCall(method, message string) {
	orig := r.h.opts.OnCall
	r.h.opts.OnCall = func(m string, a map[string]any) (map[string]any, string) {
		if m == method {
			return nil, message
		}
		return orig(m, a)
	}
}

func (r *rig) setSession(id string, fail bool) {
	r.mu.Lock()
	r.sessionID, r.noSession = id, fail
	r.mu.Unlock()
}

func TestMutRegisteredCommandDescription(t *testing.T) {
	te, err := langsmith.NewTestExtension(langsmith.TestOptions{Env: map[string]string{"TRACE_TO_LANGSMITH": "false"}, Home: t.TempDir(), Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	hostSide, extSide := net.Pipe()
	defer hostSide.Close()
	go func() { _ = te.Ext.RunWithConn(extSide) }()
	var hdr [4]byte
	if _, err := io.ReadFull(hostSide, hdr[:]); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, binary.BigEndian.Uint32(hdr[:]))
	if _, err := io.ReadFull(hostSide, data); err != nil {
		t.Fatal(err)
	}
	var frame struct {
		Register struct {
			Commands []struct{ Name, Description string } `json:"commands"`
		} `json:"register"`
	}
	if err := json.Unmarshal(data, &frame); err != nil {
		t.Fatalf("%v: %s", err, data)
	}
	cmds := frame.Register.Commands
	if len(cmds) != 1 || cmds[0].Name != "langsmith-tracing" || cmds[0].Description != "Show LangSmith tracing status for this Pi session" {
		t.Fatalf("registered commands %+v", cmds)
	}
}

func TestMutUserTurnCounterIsPerSession(t *testing.T) {
	r := newRig(t, rigOptions{SessionID: "A"})
	var turns []any
	for _, s := range []string{"A", "B", "A"} {
		r.setSession(s, false)
		r.fire("before_agent_start", `{"prompt":"p"}`)
		posts := r.posts()
		turns = append(turns, mdOf(posts[len(posts)-1])["turn_number"])
	}
	if got := langsmith.Stringify(turns); got != "[1,1,2]" {
		t.Fatalf("turn_number per session: %s", got)
	}
}

func TestMutModelInfoFailureFailsTheHooks(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.fire("before_agent_start", `{"prompt":"p"}`)
	r.failCall("getModelInfo", "no model registry")
	for _, ev := range []string{"turn_start", "before_provider_request"} {
		data := map[string]any{"turnIndex": 0, "payload": map[string]any{}}
		if f := r.h.fireErr(ev, data); !strings.Contains(f, "no model registry") {
			t.Errorf("%s: a host that cannot give the model fails the hook, got %q", ev, f)
		}
	}
}

func TestMutReplicaPatchFailureRejectsTheHook(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == "PATCH" {
			http.Error(w, "nope", 400)
			return
		}
		_, _ = io.WriteString(w, "{}")
	}))
	defer bad.Close()
	r := newRig(t, rigOptions{PiVersion: "0.1.0", Env: map[string]string{"LANGSMITH_PI_RUNS_ENDPOINTS": `[{"apiUrl":"` + bad.URL + `","apiKey":"rk"}]`}})
	r.fire("before_agent_start", `{"prompt":"p"}`)
	// With replicas configured a failed PATCH is not swallowed by the SDK (run_trees.js:752-797 has no catch): the hook fails.
	if f := r.h.fireErr("agent_end", map[string]any{"messages": []any{}}); !strings.Contains(f, "Received status [400]") {
		t.Fatalf("agent_end: %q", f)
	}
}

func TestMutReplicaEmptyFieldsFallBackToTheClient(t *testing.T) {
	r := newRig(t, rigOptions{Env: map[string]string{
		"LANGSMITH_PI_API_KEY":        "base-key",
		"LANGSMITH_PI_RUNS_ENDPOINTS": `[{"apiUrl":"","apiKey":"","projectName":""}]`,
	}})
	r.fire("before_agent_start", `{"prompt":"p"}`)
	var seen int
	for _, c := range r.ls.Calls() {
		if c.Method != "POST" {
			continue
		}
		seen++
		var body map[string]any
		_ = json.Unmarshal(c.Body, &body)
		if body["session_name"] != "pi-coding-agent" {
			t.Errorf("an empty projectName falls back to the configured project, got %v", body["session_name"])
		}
		if got := c.Header.Get("x-api-key"); got != "base-key" {
			t.Errorf("an empty apiKey falls back to the client's key, got %q", got)
		}
	}
	if seen != 1 {
		t.Fatalf("an empty apiUrl falls back to the client's endpoint: %d POSTs", seen)
	}
}

func TestMutReplicaUpdatesReachThePatch(t *testing.T) {
	mock := newMockLS(t)
	r := newRig(t, rigOptions{PiVersion: "0.1.0", Env: map[string]string{"LANGSMITH_PI_RUNS_ENDPOINTS": `[{"apiUrl":"` + mock.srv.URL + `","apiKey":"rk","updates":{"extra":{"metadata":{"u":1}}}}]`}})
	r.fire("before_agent_start", `{"prompt":"p"}`)
	r.fire("agent_end", `{"messages":[]}`)
	var patches int
	for _, c := range mock.Calls() {
		if c.Method == "PATCH" {
			patches++
			var body map[string]any
			_ = json.Unmarshal(c.Body, &body)
			extra, _ := body["extra"].(map[string]any)
			if langsmith.Stringify(extra["metadata"]) != `{"u":1}` {
				t.Errorf("the replica's updates are spread over the PATCH: %s", c.Body)
			}
		}
	}
	if patches != 1 {
		t.Fatalf("%d PATCHes", patches)
	}
}

func TestMutRootInputsKeyOrder(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.fire("before_agent_start", `{"prompt":"p","images":[1,2]}`)
	if raw := r.rawPost(id(r.postNamed("Pi agent run", 0))); !strings.Contains(raw, `"inputs":{"prompt":"p","imageCount":2}`) {
		t.Fatalf("%s", raw)
	}
}

func TestMutLoopResultErrorSurvivesTheNextPrompt(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.fire("before_agent_start", `{"prompt":"one"}`)
	first := id(r.postNamed("Pi agent run", 0))
	r.fire("agent_end", `{"messages":[{"role":"assistant","stopReason":"aborted","content":[]}]}`)
	r.fire("before_agent_start", `{"prompt":"two"}`)
	if got := r.patchOf(first)["error"]; got != "Assistant message was aborted" {
		t.Fatalf("the previous loop result keeps its error: %v", got)
	}
}

func TestMutFailedPromptStartLeavesNoActiveTrace(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.fire("before_agent_start", `{"prompt":"one"}`)
	r.setSession("", true)
	if f := r.h.fireErr("before_agent_start", map[string]any{"prompt": "two"}); !strings.Contains(f, "no session") {
		t.Fatalf("%q", f)
	}
	if f := r.h.Command("langsmith-tracing", ""); f != "" {
		t.Fatal(f)
	}
	if got := r.notifies(); len(got) != 1 || got[0][0] != "LangSmith tracing is enabled." {
		t.Fatalf("the replaced run is no longer active: %v", got)
	}
}

func TestMutPendingLlmStartsOnce(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.fire("before_agent_start", `{"prompt":"p"}`)
	r.fire("turn_start", `{"turnIndex":0,"timestamp":1}`)
	r.fire("before_provider_request", `{"payload":{"messages":[]}}`)
	r.fire("message_end", `{"message":`+stopMessage+`}`)
	r.fire("before_provider_request", `{"payload":{"messages":[{"role":"user","content":"held"}]}}`)
	r.fire("turn_start", `{"turnIndex":1,"timestamp":2}`)
	r.fire("agent_end", `{"messages":[]}`)
	r.fire("session_shutdown", `{}`)
	if n := r.postCount("anthropic"); n != 2 {
		t.Fatalf("each request starts one LLM run: %d", n)
	}
}

func TestMutPendingLlmFlushedByAgentEndIsNotStartedAgain(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.fire("before_agent_start", `{"prompt":"p"}`)
	r.fire("turn_start", `{"turnIndex":0,"timestamp":1}`)
	r.fire("before_provider_request", `{"payload":{"messages":[]}}`)
	r.fire("message_end", `{"message":`+stopMessage+`}`)
	r.fire("before_provider_request", `{"payload":{"messages":[{"role":"user","content":"held"}]}}`)
	r.fire("agent_end", `{"messages":[]}`)
	r.fire("session_shutdown", `{}`)
	if n := r.postCount("anthropic"); n != 2 {
		t.Fatalf("each request starts one LLM run: %d", n)
	}
}

func TestMutAssistantMessageWithoutAnLlmRunDefersNothing(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.fire("before_agent_start", `{"prompt":"p"}`)
	r.fire("turn_start", `{"turnIndex":0,"timestamp":1}`)
	r.fire("message_end", `{"message":`+stopMessage+`}`)
	r.fire("before_provider_request", `{"payload":{"messages":[]}}`)
	if n := r.postCount("anthropic"); n != 1 {
		t.Fatalf("an assistant message_end with no LLM run in flight must not defer the next request: %d LLM POSTs", n)
	}
}

func TestMutToolEndedOnceAndForgotten(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.fire("before_agent_start", `{"prompt":"p"}`)
	r.fire("tool_execution_start", `{"toolCallId":"a","toolName":"bash","args":{}}`)
	tool := id(r.postNamed("bash", 0))
	r.fire("tool_execution_end", `{"toolCallId":"a","result":{"content":[]}}`)
	r.fire("agent_end", `{"messages":[]}`)
	r.fire("tool_execution_end", `{"toolCallId":"a","result":{"content":[]}}`)
	if n := r.patchCount(tool); n != 1 {
		t.Fatalf("a finished tool run is patched once: %d", n)
	}
	// The tools still open at agent_end are forgotten with it.
	r.fire("before_agent_start", `{"prompt":"q"}`)
	r.fire("tool_execution_start", `{"toolCallId":"b","toolName":"read","args":{}}`)
	open := id(r.postNamed("read", 0))
	r.fire("agent_end", `{"messages":[]}`)
	r.fire("tool_execution_end", `{"toolCallId":"b","result":{"content":[]}}`)
	if n := r.patchCount(open); n != 1 {
		t.Fatalf("a tool run ended by agent_end is forgotten: %d PATCHes", n)
	}
	p := r.patchOf(open)
	if _, has := p["outputs"]; has || p["error"] != "Pi run ended before tool_execution_end" {
		t.Fatalf("an abandoned tool run has an error and no outputs: %v", p)
	}
}

func TestMutLlmRunEndedByTurnEndIsNotEndedAgain(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.fire("before_agent_start", `{"prompt":"p"}`)
	r.fire("turn_start", `{"turnIndex":0,"timestamp":1}`)
	r.fire("before_provider_request", `{"payload":{"messages":[]}}`)
	llm := id(r.postNamed("anthropic", 0))
	r.fire("turn_end", `{"turnIndex":0,"message":`+stopMessage+`,"toolResults":[]}`)
	r.fire("agent_end", `{"messages":[]}`)
	if n := r.patchCount(llm); n != 1 {
		t.Fatalf("%d PATCHes of the LLM run", n)
	}
}

func TestMutTurnEndOutputsKeyOrder(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.fire("before_agent_start", `{"prompt":"p"}`)
	r.fire("turn_start", `{"turnIndex":0,"timestamp":1}`)
	r.fire("turn_end", `{"turnIndex":0,"message":`+stopMessage+`,"toolResults":[{"x":1}]}`)
	raw := r.rawPatch(id(r.postNamed("Pi turn 0", 0)))
	if !strings.Contains(raw, `"outputs":{"messages":[`) || !strings.Contains(raw, `],"toolResults":[{"x":1}]}`) {
		t.Fatalf("%s", raw)
	}
}

func TestMutToolAfterTheTurnEndedHangsUnderTheRoot(t *testing.T) {
	for _, end := range []string{"turn_end", "agent_end"} {
		r := newRig(t, rigOptions{})
		r.fire("before_agent_start", `{"prompt":"p"}`)
		r.fire("turn_start", `{"turnIndex":0,"timestamp":1}`)
		root := id(r.postNamed("Pi agent run", 0))
		if end == "turn_end" {
			r.fire(end, `{"turnIndex":0,"message":`+stopMessage+`}`)
		} else {
			r.fire(end, `{"messages":[]}`)
		}
		r.fire("tool_execution_start", `{"toolCallId":"a","toolName":"bash","args":{}}`)
		if got := r.postNamed("bash", 0)["parent_run_id"]; got != root {
			t.Errorf("after %s the current turn is cleared, so the tool run hangs under the root: parent %v", end, got)
		}
	}
}

func TestMutAgentEndEndsRunsInOrder(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.fire("before_agent_start", `{"prompt":"p"}`)
	r.fire("turn_start", `{"turnIndex":0,"timestamp":1}`)
	r.fire("before_provider_request", `{"payload":{"messages":[]}}`)
	r.fire("tool_execution_start", `{"toolCallId":"a","toolName":"bash","args":{}}`)
	tool, llm, turn := id(r.postNamed("bash", 0)), id(r.postNamed("anthropic", 0)), id(r.postNamed("Pi turn 0", 0))
	r.fire("agent_end", `{"messages":[]}`)
	var order []string
	for _, x := range r.runs() {
		if x.Method == "PATCH" {
			order = append(order, x.ID)
		}
	}
	want := []string{tool, llm, turn}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("PATCH order %v, want tools, LLM, turns %v", order, want)
	}
}

func TestMutContextUsageKeyOrderAndNulls(t *testing.T) {
	for _, c := range []struct{ usage, want string }{
		{`{"tokens":1000,"contextWindow":2000,"percent":50}`, `"context_usage":{"tokens":1000,"contextWindow":2000,"percent":50}`},
		{`{"tokens":null,"contextWindow":2000,"percent":null}`, `"context_usage":{"tokens":null,"contextWindow":2000,"percent":null}`},
	} {
		r := newRig(t, rigOptions{PiVersion: "0.1.0"})
		r.setUsage(jsonOf(t, c.usage))
		r.fire("before_agent_start", `{"prompt":"p"}`)
		r.fire("agent_end", `{"messages":[]}`)
		if raw := r.rawPatch(id(r.postNamed("Pi agent run", 0))); !strings.Contains(raw, c.want) {
			t.Errorf("%s: %s", c.usage, raw)
		}
	}
}

func TestMutFactoryClientErrorNamesTheExtension(t *testing.T) {
	factoryEnv(t, map[string]string{"TRACE_TO_LANGSMITH": "true", "LANGSMITH_TRACING_SAMPLING_RATE": "2"})
	if got := factoryPanic(t); !strings.HasPrefix(got, "langsmith: LANGSMITH_TRACING_SAMPLING_RATE must be between 0 and 1") {
		t.Fatalf("%q", got)
	}
}

func TestMutFactorySummaryNamesTheFirstIssue(t *testing.T) {
	factoryEnv(t, map[string]string{"LANGSMITH_PI_METADATA": "null", "LANGSMITH_PI_RUNS_ENDPOINTS": "{}"})
	first, _, _ := strings.Cut(factoryPanic(t), "\n")
	if first != "langsmith: invalid configuration: metadata: Invalid input: expected record, received null" {
		t.Fatalf("%q", first)
	}
}

func TestMutReplicaEmptyKeySendsNoCredentialHeader(t *testing.T) {
	r := newRig(t, rigOptions{Env: map[string]string{"LANGSMITH_PI_RUNS_ENDPOINTS": `[{"apiUrl":"","apiKey":""}]`}})
	r.fire("before_agent_start", `{"prompt":"p"}`)
	var seen int
	for _, c := range r.ls.Calls() {
		if c.Method == "POST" {
			seen++
			if _, has := c.Header["X-Api-Key"]; has {
				t.Errorf("an empty replica apiKey means no override and the client has no key: no x-api-key header, got %q", c.Header["X-Api-Key"])
			}
		}
	}
	if seen != 1 {
		t.Fatalf("%d POSTs", seen)
	}
}
