package langsmith_test

// Tests added by the mutation proof (port/mutations.md) for the open survivors of the first pass: the settle and
// shutdown bookkeeping of extension.go, batching at shutdown, usage cost, the Gemini/Responses converters and the
// config file schema.

import (
	"os"
	"strings"
	"testing"

	langsmith "github.com/MichaelKinsy/pigpen/langsmith"
)

func (r *rig) rootIDs() []string {
	var out []string
	for _, p := range r.posts() {
		if p["name"] == "Pi agent run" {
			out = append(out, id(p))
		}
	}
	return out
}

func TestMutTwoStaleSettlesAreBothSkipped(t *testing.T) {
	r := newRig(t, rigOptions{})
	for i := 0; i < 3; i++ {
		r.fire("before_agent_start", `{"prompt":"p"}`)
		if i < 2 {
			r.fire("agent_end", `{"messages":[]}`)
		}
	}
	root3 := r.rootIDs()[2]
	// Two runs ended by the next prompt still owe their settles: both are consumed before the third run settles.
	r.fire("agent_settled", `{}`)
	r.fire("agent_settled", `{}`)
	if n := r.patchCount(root3); n != 0 {
		t.Fatalf("the third run was ended by a stale settle: %d PATCHes", n)
	}
	r.fire("agent_settled", `{}`)
	if n := r.patchCount(root3); n != 1 {
		t.Fatalf("the third settle ends the third run: %d PATCHes", n)
	}
}

func TestMutSettleWithoutAgentEndHasNoError(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.fire("before_agent_start", `{"prompt":"p"}`)
	r.fire("agent_settled", `{}`)
	p := r.patchOf(r.rootIDs()[0])
	if _, has := p["error"]; has || langsmith.Stringify(p["outputs"]) != `{"incomplete":true}` {
		t.Fatalf("a run settled without agent_end is incomplete, not failed: %v", p)
	}
}

func TestMutShutdownForgetsTheRun(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.fire("before_agent_start", `{"prompt":"p"}`)
	r.fire("session_shutdown", `{}`)
	before := len(r.ls.Calls())
	r.fire("turn_start", `{"turnIndex":0,"timestamp":1}`)
	r.fire("tool_execution_start", `{"toolCallId":"a","toolName":"bash","args":{}}`)
	r.fire("agent_settled", `{}`)
	if len(r.ls.Calls()) != before {
		t.Fatal("after session_shutdown no run is active: nothing is sent")
	}
	if f := r.h.Command("langsmith-tracing", ""); f != "" {
		t.Fatal(f)
	}
	if got := r.notifies(); len(got) != 1 || got[0][0] != "LangSmith tracing is enabled." {
		t.Fatalf("%v", got)
	}
}

func TestMutShutdownForgetsStaleSettles(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.fire("before_agent_start", `{"prompt":"one"}`)
	r.fire("agent_end", `{"messages":[]}`)
	r.fire("before_agent_start", `{"prompt":"two"}`) // one stale settle is owed
	r.fire("session_shutdown", `{}`)
	r.fire("before_agent_start", `{"prompt":"three"}`)
	root3 := r.rootIDs()[2]
	r.fire("agent_settled", `{}`)
	if n := r.patchCount(root3); n != 1 {
		t.Fatalf("shutdown resets the stale settle count, so this settle ends the new run: %d PATCHes", n)
	}
}

func TestMutShutdownAwaitsTheBatches(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, k := range []string{"LANGSMITH_API_KEY", "LANGCHAIN_API_KEY", "LANGSMITH_ENDPOINT", "LANGCHAIN_ENDPOINT", "LANGSMITH_RUNS_ENDPOINTS", "LANGSMITH_CONFIG_FILE", "LANGSMITH_PROFILE", "LANGSMITH_TRACING_MODE", "LANGSMITH_OTEL_ENABLED", "OTEL_ENABLED"} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
	ls := newMockLS(t)
	te, err := langsmith.NewTestExtension(langsmith.TestOptions{Env: map[string]string{"TRACE_TO_LANGSMITH": "true"}, Home: t.TempDir(), Cwd: t.TempDir(), ServerURL: ls.srv.URL, PiVersion: "1.5.0"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(te.Close)
	h := StartHost(t, te.Ext, HostOptions{OnCall: func(method string, _ map[string]any) (map[string]any, string) {
		if method == "getSessionID" {
			return map[string]any{"sessionId": "s"}, ""
		}
		return map[string]any{}, ""
	}})
	h.Fire("before_agent_start", withPromptOptions("before_agent_start", map[string]any{"prompt": "p"}))
	h.Fire("session_shutdown", map[string]any{})
	// The queued create and the shutdown update are sent (one multipart batch) before the hook returns.
	var sent bool
	for _, c := range ls.Calls() {
		if strings.HasSuffix(c.Path, "/runs/multipart") && strings.Contains(string(c.Body), `{"shutdown":true}`) {
			sent = true
		}
	}
	if !sent {
		t.Fatalf("session_shutdown must wait for the queued batches: %d requests", len(ls.Calls()))
	}
}

func TestMutNegativeCostIsKept(t *testing.T) {
	msg := jsonOf(t, `{"role":"assistant","usage":{"input":1,"output":2,"totalTokens":3,"cost":{"input":0,"output":0,"total":-1}}}`)
	got, err := langsmith.ExtractUsageMetadata(msg)
	if err != nil {
		t.Fatal(err)
	}
	if s := langsmith.Stringify(got); !strings.Contains(s, `"total_cost":-1`) {
		t.Fatalf("only a zero cost means pi has no pricing: %s", s)
	}
}

func TestMutResponsesUserMessageWithEmptyContentIsRebuilt(t *testing.T) {
	got, err := langsmith.ConvertProviderPayload(jsonAny(t, `{"input":[{"content":[],"role":"user"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if s := langsmith.Stringify(got); s != `{"messages":[{"role":"user","content":[]}]}` {
		t.Fatalf("%s", s)
	}
}

func TestMutConfigFileWithABadEnabledIsDroppedWhole(t *testing.T) {
	r := newRig(t, rigOptions{CwdConfig: `{"enabled":"yes","project":"from-bad-file"}`})
	if r.te.ProjectName != "pi-coding-agent" {
		t.Fatalf("a config file that fails the schema is ignored as a whole, got project %q", r.te.ProjectName)
	}
}
