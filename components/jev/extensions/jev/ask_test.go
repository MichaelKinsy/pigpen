package jev_test

import (
	"encoding/json"
	"strings"
	"testing"
)

// jev_ask (src/index.ts registerTool, toQuestion, renderAnswers).

func askHost(t *testing.T, body any, extra map[string]any) (*Host, *fakeJev) {
	t.Helper()
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(body))
	e.writeGlobal(t, jevConfig(srv, extra))
	return start(t, e, newHostState(), HostOptions{}), srv
}

func askResult(t *testing.T, h *Host, params map[string]any) (string, map[string]any) {
	t.Helper()
	h.adoptDynamicTools()
	raw, failure := h.Tool("jev_ask", params)
	if failure != "" {
		t.Fatalf("jev_ask failed: %s", failure)
	}
	var r struct {
		Content json.RawMessage `json:"content"`
		Details map[string]any  `json:"details"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("result %s: %v", raw, err)
	}
	var s string
	if json.Unmarshal(r.Content, &s) != nil {
		var blocks []struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(r.Content, &blocks)
		for _, b := range blocks {
			s += b.Text
		}
	}
	return s, r.Details
}

func TestAsk_RegisteredOnlyWhenEnabledWithAKey(t *testing.T) {
	e := newEnv(t)
	h := start(t, e, newHostState(), HostOptions{})
	if len(h.CallsTo("registerTool")) != 0 {
		t.Error("jev_ask registered while Jev is off")
	}
}

func TestAsk_RendersTypedAnswers(t *testing.T) {
	body := map[string]any{"model": "jev-test", "usage": map[string]any{"input_tokens": 12, "output_tokens": 3}, "answers": map[string]any{
		"relevant": noul(0.93),
		"label": map[string]any{"type": "choice", "choice": "bug", "confidence": 0.9,
			"probabilities": map[string]any{"feature": 0.1, "bug": 0.9}},
		"quality": score(1.75, 0.8),
	}}
	h, srv := askHost(t, body, nil)
	got, details := askResult(t, h, map[string]any{
		"state": "the diff",
		"questions": []any{
			map[string]any{"id": "relevant", "type": "noul", "instructions": "Is this relevant?"},
			map[string]any{"id": "label", "type": "choice", "instructions": "Which bucket?",
				"options": []any{map[string]any{"name": "bug", "description": "Defect"}, map[string]any{"name": "feature"}}},
			map[string]any{"id": "quality", "type": "score", "instructions": "How thorough?", "levels": []any{"Superficial", "Adequate", "Thorough"}},
		},
	})
	want := strings.Join([]string{
		"model jev-test",
		"relevant: yes 0.93  <- Is this relevant?",
		"label: bug (conf 0.90) [bug 0.90, feature 0.10]  <- Which bucket?",
		"quality: 1.75/3 (conf 0.80)  <- How thorough?",
		"tokens 12 in / 3 out",
	}, "\n")
	// Note: the legend in the score() fixture has four entries, so levels = 3.
	if got != want {
		t.Errorf("rendered:\n%s\nwant:\n%s", got, want)
	}
	if details["ok"] != true {
		t.Errorf("details = %v", details)
	}
	req := srv.first(t)
	if req.Body["state"] != "the diff" {
		t.Errorf("state = %v", req.Body["state"])
	}
	qs := req.Body["questions"].(map[string]any)
	label := qs["label"].(map[string]any)
	crit := label["criteria"].(map[string]any)
	if crit["bug"] != "Defect" || crit["feature"] != nil {
		t.Errorf("choice criteria = %v (option without description must be null)", crit)
	}
	if _, has := crit["feature"]; !has {
		t.Error("option without description must be sent as null, not omitted")
	}
	if lv := qs["quality"].(map[string]any)["criteria"].([]any); len(lv) != 3 {
		t.Errorf("levels = %v", lv)
	}
	noulQ := qs["relevant"].(map[string]any)
	if _, has := noulQ["criteria"]; has {
		t.Errorf("noul question sent criteria: %v", noulQ)
	}
	if got := strings.Join(questionOrder(req.Raw), ","); got != "relevant,label,quality" {
		t.Errorf("order = %s", got)
	}
}

func TestAsk_InvalidQuestionShapesAreExplainedWithoutARequest(t *testing.T) {
	h, srv := askHost(t, clearGate(), nil)
	cases := map[string]struct {
		q    map[string]any
		want string
	}{
		"choice without options": {map[string]any{"id": "c", "type": "choice", "instructions": "x"}, `jev_ask: question "c": choice needs at least one option`},
		"choice empty options":   {map[string]any{"id": "c", "type": "choice", "instructions": "x", "options": []any{}}, `jev_ask: question "c": choice needs at least one option`},
		"score one level":        {map[string]any{"id": "s", "type": "score", "instructions": "x", "levels": []any{"only"}}, `jev_ask: question "s": score needs at least two levels`},
		"score no levels":        {map[string]any{"id": "s", "type": "score", "instructions": "x"}, `jev_ask: question "s": score needs at least two levels`},
	}
	for name, c := range cases {
		got, details := askResult(t, h, map[string]any{"state": "s", "questions": []any{c.q}})
		if got != c.want || details["ok"] != false {
			t.Errorf("%s: got %q details %v, want %q", name, got, details, c.want)
		}
	}
	if srv.count() != 0 {
		t.Errorf("requests = %d", srv.count())
	}
}

func TestAsk_BlankInstructionsAndNoQuestionsAreRefused(t *testing.T) {
	h, srv := askHost(t, clearGate(), nil)
	got, _ := askResult(t, h, map[string]any{"state": "s", "questions": []any{map[string]any{"id": "a", "type": "noul", "instructions": "  "}}})
	if got != `jev_ask: question "a": instructions are required` {
		t.Errorf("blank instructions: %q", got)
	}
	got, _ = askResult(t, h, map[string]any{"state": "s", "questions": []any{}})
	if got != "jev_ask: no questions provided" {
		t.Errorf("no questions: %q", got)
	}
	if srv.count() != 0 {
		t.Errorf("requests = %d", srv.count())
	}
}

// C8: the original let a duplicate id silently replace the earlier question.
func TestCorrection_DuplicateQuestionIdIsRefused(t *testing.T) {
	h, srv := askHost(t, clearGate(), nil)
	q := map[string]any{"id": "a", "type": "noul", "instructions": "x"}
	got, _ := askResult(t, h, map[string]any{"state": "s", "questions": []any{q, q}})
	if got != `jev_ask: duplicate question id "a"` || srv.count() != 0 {
		t.Errorf("got %q, requests %d", got, srv.count())
	}
}

func TestAsk_ServerErrorIsReportedNotThrown(t *testing.T) {
	h, _ := askHost(t, "x", nil)
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, func(int, recordedRequest) jevReply { return jevReply{status: 401, body: "denied " + testKey} })
	e.writeGlobal(t, jevConfig(srv, nil))
	h = start(t, e, newHostState(), HostOptions{})
	got, details := askResult(t, h, map[string]any{"state": "s", "questions": []any{map[string]any{"id": "a", "type": "noul", "instructions": "x"}}})
	if !strings.HasPrefix(got, "jev_ask: ") || !strings.Contains(got, "401") || strings.Contains(got, testKey) || !strings.Contains(got, "[redacted]") || details["ok"] != false {
		t.Errorf("got %q %v", got, details)
	}
}

func TestAsk_MissingAnswerIsAnErrorNotAVerdict(t *testing.T) {
	h, _ := askHost(t, map[string]any{"model": "m", "answers": map[string]any{}}, nil)
	got, details := askResult(t, h, map[string]any{"state": "s", "questions": []any{map[string]any{"id": "a", "type": "noul", "instructions": "x"}}})
	if !strings.HasPrefix(got, "jev_ask: ") || !strings.Contains(got, `did not answer "a"`) || details["ok"] != false {
		t.Errorf("got %q %v", got, details)
	}
}

func TestAsk_AnswerForAnotherTypeIsAnError(t *testing.T) {
	h, _ := askHost(t, map[string]any{"model": "m", "answers": map[string]any{"a": score(1, 0.5)}}, nil)
	got, _ := askResult(t, h, map[string]any{"state": "s", "questions": []any{map[string]any{"id": "a", "type": "noul", "instructions": "x"}}})
	if !strings.Contains(got, `"a" is not a noul`) {
		t.Errorf("got %q", got)
	}
}

func TestAsk_HonoursOff(t *testing.T) {
	h, srv := askHost(t, clearGate(), nil)
	if failure := h.Command("jev", "off"); failure != "" {
		t.Fatal(failure)
	}
	got, details := askResult(t, h, map[string]any{"state": "s", "questions": []any{map[string]any{"id": "a", "type": "noul", "instructions": "x"}}})
	if srv.count() != 0 || details["ok"] != false || !strings.Contains(got, "off") {
		t.Errorf("jev_ask sent content while Jev is off: %q %v (%d requests)", got, details, srv.count())
	}
}

func TestAsk_ToolMetadata(t *testing.T) {
	h, _ := askHost(t, clearGate(), nil)
	calls := h.CallsTo("registerTool")
	if len(calls) != 1 {
		t.Fatalf("registerTool calls = %d", len(calls))
	}
	a := calls[0].Args
	if a["name"] != "jev_ask" || a["label"] != "Jev Ask" {
		t.Errorf("tool = %v", a)
	}
	mustContain(t, "description", a["description"].(string), "typed questions", "calibrated answers")
}

func TestCorrection_AskStateIsCappedAtMaxStateChars(t *testing.T) {
	h, srv := askHost(t, map[string]any{"model": "m", "answers": map[string]any{"a": noul(0.5)}}, map[string]any{"maxStateChars": 100})
	askResult(t, h, map[string]any{"state": strings.Repeat("s", 500), "questions": []any{map[string]any{"id": "a", "type": "noul", "instructions": "x"}}})
	got, _ := srv.first(t).Body["state"].(string)
	if got != strings.Repeat("s", 100)+"…[400 chars elided]" {
		t.Errorf("state = %q", got)
	}
}
