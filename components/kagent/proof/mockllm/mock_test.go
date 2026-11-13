package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func mustConv(t *testing.T, api, js string) *conv {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(js), &m); err != nil {
		t.Fatal(err)
	}
	switch api {
	case "chat":
		return parseChat(m)
	case "responses":
		return parseResponses(m)
	}
	return parseAnthropic(m)
}

func TestBasketChat(t *testing.T) {
	tools := `"tools":[{"type":"function","function":{"name":"read","parameters":{}}},{"type":"function","function":{"name":"bash","parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}},{"type":"function","function":{"name":"mcp_x_kagent_probe","parameters":{}}}]`
	c := mustConv(t, "chat", `{"model":"m","messages":[{"role":"system","content":"s"},{"role":"user","content":"[basket] go"}],`+tools+`}`)
	r := plan(c)
	if r.Call == nil || r.Call.Name != "bash" || !strings.Contains(r.Call.Args, "basket-ok") {
		t.Fatalf("step1 %+v", r)
	}
	c = mustConv(t, "chat", `{"model":"m","messages":[{"role":"user","content":"[basket] go"},{"role":"assistant","tool_calls":[{"id":"1","function":{"name":"bash"}}]},{"role":"tool","tool_call_id":"1","content":"basket-ok\n"}],`+tools+`}`)
	r = plan(c)
	if r.Call == nil || r.Call.Name != "mcp_x_kagent_probe" || r.Call.Args != `{"note":"basket"}` {
		t.Fatalf("step2 %+v", r)
	}
	c = mustConv(t, "chat", `{"model":"m","messages":[{"role":"user","content":"[basket] go"},{"role":"tool","content":"basket-ok\n"},{"role":"tool","content":"probe:basket"}],`+tools+`}`)
	r = plan(c)
	want := "BASKET-DONE " + shaHex("basket-ok\nprobe:basket")
	if r.Text != want {
		t.Fatalf("final %q want %q", r.Text, want)
	}
}

func TestShellArgsShapes(t *testing.T) {
	cases := map[string]struct{ schema, want string }{
		"exec_command": {`{"properties":{"cmd":{"type":"string"},"yield_time_ms":{"type":"number"}},"required":["cmd"]}`, `"cmd"`},
		"shell":        {`{"properties":{"command":{"type":"array"}},"required":["command"]}`, `["bash","-lc"`},
		"Bash":         {`{"properties":{"command":{"type":"string"},"description":{"type":"string"}},"required":["command","description"]}`, `"description":""`},
	}
	for name, cs := range cases {
		var sch map[string]any
		json.Unmarshal([]byte(cs.schema), &sch)
		got := commandArgs(&toolDef{Name: name, Schema: sch}, shellCmd)
		if !strings.Contains(got, cs.want) {
			t.Errorf("%s: %s lacks %s", name, got, cs.want)
		}
	}
}

func TestApproveRecallEcho(t *testing.T) {
	tl := `"tools":[{"name":"mcp__kagent__kagent_probe","input_schema":{}},{"name":"Bash","input_schema":{}}]`
	c := mustConv(t, "anthropic", `{"model":"m","messages":[{"role":"user","content":"[approve] x"}],`+tl+`}`)
	if r := plan(c); r.Call == nil || r.Call.Name != "mcp__kagent__kagent_probe" {
		t.Fatalf("%+v", r)
	}
	c = mustConv(t, "anthropic", `{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"remember [m-77]"}]},{"role":"assistant","content":"ok"},{"role":"user","content":[{"type":"text","text":"<system-reminder>x [zz]</system-reminder>"},{"type":"text","text":"[recall]"}]}]}`)
	if r := plan(c); r.Text != "RECALL [m-77]" {
		t.Fatalf("%+v", r)
	}
	c = mustConv(t, "responses", `{"model":"m","input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}]}`)
	if r := plan(c); r.Text != "ECHO hello" {
		t.Fatalf("%+v", r)
	}
}

func TestResponsesNamespaceTool(t *testing.T) {
	c := mustConv(t, "responses", `{"model":"m","input":[{"role":"user","content":"[basket]"},{"type":"function_call","name":"exec_command","call_id":"1"},{"type":"function_call_output","call_id":"1","output":"basket-ok\n"}],
"tools":[{"type":"function","name":"exec_command","parameters":{}},{"type":"namespace","name":"mcp__kagent","tools":[{"type":"function","name":"kagent_probe","parameters":{}}]}]}`)
	r := plan(c)
	if r.Call == nil || r.Call.Name != "kagent_probe" || r.Call.Namespace != "mcp__kagent" {
		t.Fatalf("%+v", r)
	}
}

// request bodies for the three protocols: history = marker prompt, shell call + result, then tail.
// tail is spliced as extra JSON array items (already comma-led).
func bodies(tail map[string]string) map[string]string {
	chatTools := `"tools":[{"type":"function","function":{"name":"bash","parameters":{"properties":{"command":{"type":"string"}}}}},{"type":"function","function":{"name":"mcp_x_kagent_probe","parameters":{}}}]`
	respTools := `"tools":[{"type":"function","name":"exec_command","parameters":{"properties":{"cmd":{"type":"string"}},"required":["cmd"]}},{"type":"namespace","name":"mcp__kagent","tools":[{"type":"function","name":"kagent_probe","parameters":{}}]}]`
	antTools := `"tools":[{"name":"Bash","input_schema":{"properties":{"command":{"type":"string"}}}},{"name":"mcp__kagent__kagent_probe","input_schema":{}}]`
	return map[string]string{
		"chat":      `{"model":"m","messages":[{"role":"user","content":"[basket] go"},{"role":"assistant","tool_calls":[{"id":"1","function":{"name":"bash"}}]},{"role":"tool","tool_call_id":"1","content":"basket-ok\n"}` + tail["chat"] + `],` + chatTools + `}`,
		"responses": `{"model":"m","input":[{"role":"user","content":"[basket] go"},{"type":"function_call","name":"exec_command","call_id":"1"},{"type":"function_call_output","call_id":"1","output":"basket-ok\n"}` + tail["responses"] + `],` + respTools + `}`,
		"anthropic": `{"model":"m","messages":[{"role":"user","content":"[basket] go"},{"role":"assistant","content":[{"type":"tool_use","id":"1","name":"Bash","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"1","content":"basket-ok\n"}]}` + tail["anthropic"] + `],` + antTools + `}`,
	}
}

func TestNudgeAfterToolResult(t *testing.T) {
	nudge := map[string]string{
		"chat":      `,{"role":"user","content":"Continue."}`,
		"responses": `,{"role":"user","content":[{"type":"input_text","text":"Continue."}]}`,
		"anthropic": `,{"role":"user","content":"Continue."}`,
	}
	for api, js := range bodies(nudge) {
		r := plan(mustConv(t, api, js))
		if r.Call == nil || !strings.HasSuffix(r.Call.Name, "kagent_probe") {
			t.Errorf("%s: nudge after step 1 must continue to the probe call, got %+v", api, r)
		}
	}
	// text block next to the tool_result in one Anthropic user message is still the tool-result carrier
	c := mustConv(t, "anthropic", `{"model":"m","messages":[{"role":"user","content":"[basket] go"},{"role":"assistant","content":[{"type":"tool_use","id":"1","name":"Bash","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"1","content":"basket-ok\n"},{"type":"text","text":"Continue."}]}],"tools":[{"name":"mcp__kagent__kagent_probe","input_schema":{}}]}`)
	if r := plan(c); r.Call == nil || !strings.HasSuffix(r.Call.Name, "kagent_probe") {
		t.Errorf("merged nudge: %+v", r)
	}
	// two nudges in a row, after the second result, finish the scenario
	c = mustConv(t, "chat", `{"model":"m","messages":[{"role":"user","content":"[basket] go"},{"role":"tool","content":"basket-ok\n"},{"role":"tool","content":"probe:basket"},{"role":"user","content":"Continue."},{"role":"user","content":"Continue."}]}`)
	if r := plan(c); r.Text != "BASKET-DONE "+shaHex("basket-ok\nprobe:basket") {
		t.Errorf("double nudge: %+v", r)
	}
}

func TestMessageAfterCompletedScenarioIsNewPrompt(t *testing.T) {
	final := map[string]string{
		"chat":      `,{"role":"assistant","content":"BASKET-DONE abc"},{"role":"user","content":"hello there"}`,
		"responses": `,{"role":"assistant","content":[{"type":"output_text","text":"BASKET-DONE abc"}]},{"role":"user","content":"hello there"}`,
		"anthropic": `,{"role":"assistant","content":"BASKET-DONE abc"},{"role":"user","content":"hello there"}`,
	}
	for api, js := range bodies(final) {
		if r := plan(mustConv(t, api, js)); r.Text != "ECHO hello there" {
			t.Errorf("%s: unrelated message after BASKET-DONE must echo, got %+v", api, r)
		}
	}
	again := map[string]string{
		"chat":      `,{"role":"assistant","content":"BASKET-DONE abc"},{"role":"user","content":"[basket] again"}`,
		"responses": `,{"role":"assistant","content":[{"type":"output_text","text":"BASKET-DONE abc"}]},{"role":"user","content":"[basket] again"}`,
		"anthropic": `,{"role":"assistant","content":"BASKET-DONE abc"},{"role":"user","content":"[basket] again"}`,
	}
	for api, js := range bodies(again) {
		if r := plan(mustConv(t, api, js)); r.Call == nil || r.Step[:5] != "shell" {
			t.Errorf("%s: a new marker after BASKET-DONE restarts the scenario, got %+v", api, r)
		}
	}
	// a plain message right after the first user prompt, with no tool result yet, is not a nudge
	c := mustConv(t, "chat", `{"model":"m","messages":[{"role":"user","content":"[basket] go"},{"role":"user","content":"hello"}]}`)
	if r := plan(c); r.Text != "ECHO hello" {
		t.Errorf("no tool result yet: %+v", r)
	}
	// no marker anywhere: last user message
	c = mustConv(t, "chat", `{"model":"m","messages":[{"role":"user","content":"a"},{"role":"assistant","content":"b"},{"role":"user","content":"c"}]}`)
	if r := plan(c); r.Text != "ECHO c" {
		t.Errorf("fallback: %+v", r)
	}
	// recall after a completed scenario returns the first marker of the history; a plain message after it is a new prompt
	hist := `{"role":"user","content":"[m-1] [basket] go"},{"role":"assistant","content":"BASKET-DONE abc"},{"role":"user","content":"[recall]"}`
	if r := plan(mustConv(t, "chat", `{"model":"m","messages":[`+hist+`]}`)); r.Text != "RECALL [m-1]" {
		t.Errorf("recall: %+v", r)
	}
	if r := plan(mustConv(t, "chat", `{"model":"m","messages":[`+hist+`,{"role":"assistant","content":"RECALL [m-1]"},{"role":"user","content":"Continue."}]}`)); r.Text != "ECHO Continue." {
		t.Errorf("message after recall answer: %+v", r)
	}
}

func TestApprovalGatedProbeSelection(t *testing.T) {
	claude := `"tools":[{"name":"Bash","input_schema":{}},{"name":"mcp__mock-mcp__kagent_probe","input_schema":{}},{"name":"mcp__mock-mcp-approval__kagent_probe","input_schema":{}}]`
	// Codex: the same function name under two namespaces, approval server listed first on purpose
	codex := `"tools":[{"type":"function","name":"exec_command","parameters":{}},{"type":"namespace","name":"mcp__mock-mcp-approval","tools":[{"type":"function","name":"kagent_probe","parameters":{}}]},{"type":"namespace","name":"mcp__mock-mcp","tools":[{"type":"function","name":"kagent_probe","parameters":{}}]}]`
	// flattened duplicates with no namespace: the later one is taken for [approve]
	flat := `"tools":[{"type":"function","name":"exec_command","parameters":{}},{"type":"function","name":"kagent_probe","parameters":{}},{"type":"function","name":"kagent_probe","parameters":{}}]`
	cases := []struct {
		api, tools, prompt string
		results            string
		ns, name           string
		last               bool // flat case: expect the last candidate
	}{
		{"anthropic", claude, "[approve] go", "", "", "mcp__mock-mcp-approval__kagent_probe", false},
		{"anthropic", claude, "[basket] go", `,{"role":"user","content":[{"type":"tool_result","tool_use_id":"1","content":"ok"}]}`, "", "mcp__mock-mcp__kagent_probe", false},
		{"responses", codex, "[approve] go", "", "mcp__mock-mcp-approval", "kagent_probe", false},
		{"responses", codex, "[basket] go", `,{"type":"function_call_output","call_id":"1","output":"ok"}`, "mcp__mock-mcp", "kagent_probe", false},
	}
	for i, cs := range cases {
		var js string
		switch cs.api {
		case "anthropic":
			js = `{"model":"m","messages":[{"role":"user","content":"` + cs.prompt + `"}` + cs.results + `],` + cs.tools + `}`
		default:
			js = `{"model":"m","input":[{"role":"user","content":"` + cs.prompt + `"}` + cs.results + `],` + cs.tools + `}`
		}
		r := plan(mustConv(t, cs.api, js))
		if r.Call == nil || r.Call.Name != cs.name || r.Call.Namespace != cs.ns {
			t.Errorf("case %d: %+v", i, r)
		}
	}
	c := mustConv(t, "responses", `{"model":"m","input":[{"role":"user","content":"[approve] go"}],`+flat+`}`)
	if pt := c.probeTool(true); pt == nil || pt != &c.Tools[len(c.Tools)-1] {
		t.Errorf("flat duplicates: %+v", pt)
	}
	// both markers: the probe in step 2 is the gated one
	c = mustConv(t, "anthropic", `{"model":"m","messages":[{"role":"user","content":"[basket] [approve] go"},{"role":"user","content":[{"type":"tool_result","tool_use_id":"1","content":"ok"}]}],`+claude+`}`)
	if r := plan(c); r.Call == nil || r.Call.Name != "mcp__mock-mcp-approval__kagent_probe" {
		t.Errorf("basket+approve: %+v", r)
	}
}

// A rejected approval reaches the model as a tool result; the request log must carry that text so a run can show the
// reason arrived, and the logged text must reproduce the answer sha.
func TestLoggedResultsShowRejectionAndReproduceSha(t *testing.T) {
	tl := `"tools":[{"name":"mcp__kagent__kagent_probe","input_schema":{}}]`
	const rej = "Tool call was rejected by user. Reason: rejected by basket"
	c := mustConv(t, "anthropic", `{"model":"m","messages":[{"role":"user","content":"[run-1] [approve] go"},{"role":"assistant","content":[{"type":"tool_use","id":"1","name":"mcp__kagent__kagent_probe","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"1","content":"`+rej+`"}]}],`+tl+`}`)
	res, cut := c.loggedResults()
	if cut || !strings.Contains(res, "Reason: rejected by basket") {
		t.Fatalf("logged results %q cut=%v", res, cut)
	}
	if got, want := plan(c).Text, "APPROVE-DONE "+shaHex(res); got != want {
		t.Fatalf("answer %q, sha of logged results gives %q", got, want)
	}
	// before any tool result nothing is logged
	c = mustConv(t, "anthropic", `{"model":"m","messages":[{"role":"user","content":"[approve] go"}],`+tl+`}`)
	if res, _ := c.loggedResults(); res != "" {
		t.Fatalf("results %q", res)
	}
	// oversized results are cut and flagged
	big := strings.Repeat("x", maxLoggedResults+10)
	c = mustConv(t, "anthropic", `{"model":"m","messages":[{"role":"user","content":"[approve] go"},{"role":"user","content":[{"type":"tool_result","tool_use_id":"1","content":"`+big+`"}]}],`+tl+`}`)
	if res, cut := c.loggedResults(); !cut || len(res) != maxLoggedResults {
		t.Fatalf("len %d cut %v", len(res), cut)
	}
}

// End to end through the HTTP handler: the request log line of the step that answers a rejection carries the rejection text.
func TestRequestLogCarriesToolResults(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	srv := httptest.NewServer((&server{cfg: config{ChunkChars: 64}}).mux())
	body := `{"model":"m","messages":[{"role":"user","content":"[approve] go"},{"role":"assistant","content":[{"type":"tool_use","id":"1","name":"mcp__kagent__kagent_probe","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"1","content":"Tool call was rejected by user. Reason: because"}]}],"tools":[{"name":"mcp__kagent__kagent_probe","input_schema":{}}]}`
	resp, err := http.Post(srv.URL+"/v1/messages", "application/json", strings.NewReader(body))
	if err == nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	srv.Close()
	os.Stdout = old
	w.Close()
	logged, _ := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, ln := range strings.Split(string(logged), "\n") {
		var e map[string]any
		if json.Unmarshal([]byte(ln), &e) == nil && e["step"] == "final" {
			found = true
			if !strings.Contains(e["results"].(string), "Reason: because") || !strings.HasPrefix(e["text"].(string), "APPROVE-DONE ") {
				t.Fatalf("log line %v", e)
			}
		}
	}
	if !found {
		t.Fatalf("no final log line in %q", logged)
	}
}

func TestHoldMarkersBlockOnlyTheNamedCallWhileTheBudgetLasts(t *testing.T) {
	holds.Store(2)
	plain := mustConv(t, "chat", `{"model":"m","messages":[{"role":"user","content":"remember [m-1] ok"}]}`)
	if plain.shouldHold() {
		t.Fatal("a prompt without a hold marker was held")
	}
	first := mustConv(t, "chat", `{"model":"m","messages":[{"role":"user","content":"[hold] go"}]}`)
	if !first.shouldHold() {
		t.Fatal("[hold] did not hold its first model call")
	}
	after := `{"model":"m","messages":[{"role":"user","content":"[approve] [holdafter] go"}`
	if call := mustConv(t, "chat", after+`]}`); call.shouldHold() {
		t.Fatal("[holdafter] held the call before any tool result")
	}
	withResult := mustConv(t, "chat", after+`,{"role":"assistant","tool_calls":[{"id":"1","function":{"name":"bash"}}]},{"role":"tool","tool_call_id":"1","content":"x"}]}`)
	if !withResult.shouldHold() {
		t.Fatal("[holdafter] did not hold the call that follows a tool result")
	}
	if first.shouldHold() || withResult.shouldHold() {
		t.Fatal("the hold budget of 2 held a third call")
	}
}
