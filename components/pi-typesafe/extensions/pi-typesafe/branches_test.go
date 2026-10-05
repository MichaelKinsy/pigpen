package pi_typesafe_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	pi_typesafe "github.com/MichaelKinsy/pigpen/components/pi-typesafe/extensions/pi-typesafe"
)

func (r *rig) lastLevel() string { r.mu.Lock(); defer r.mu.Unlock(); return r.levels[len(r.levels)-1] }

func TestStartupCalloutIsAWarningNotificationWithUI(t *testing.T) {
	t.Setenv("PI_TYPESAFE_ENABLED", "1")
	r := newRig(t)
	t.Setenv("PI_TYPESAFE_ENABLED", "1")
	before := r.noticeCount()
	r.startSession("startup")
	if r.noticeCount() != before+1 || r.lastLevel() != "warning" || len(r.messages) != 0 {
		t.Fatalf("notices=%v levels=%v messages=%v", r.notices, r.levels, r.messages)
	}
}

func TestStartupCalloutWithoutUIIsAStatusMessage(t *testing.T) {
	isolate(t)
	t.Setenv("PI_TYPESAFE_ENABLED", "1")
	no := false
	var notices, messages, types []string
	host := StartHost(t, pi_typesafe.New(pi_typesafe.Options{}), HostOptions{HasUI: &no, Mode: "print", OnCall: func(method string, args map[string]any) (map[string]any, string) {
		switch method {
		case "ui.notify":
			notices = append(notices, args["message"].(string))
		case "sendMessage":
			m := args["message"].(map[string]any)
			messages = append(messages, m["content"].(string))
			types = append(types, m["customType"].(string))
		}
		return nil, ""
	}})
	host.Fire("session_start", map[string]any{"reason": "startup"})
	if len(notices) != 0 || len(messages) != 1 || types[0] != "typesafe-status" || !strings.Contains(messages[0], "judgments are skipped") {
		t.Fatalf("notices=%v messages=%v", notices, messages)
	}
}

func TestForbiddenKeyIsCalledOutLikeARejectedOne(t *testing.T) {
	r := newRig(t)
	t.Setenv("TYPESAFE_API_KEY", "offline-test-key")
	t.Setenv("PI_TYPESAFE_ENABLED", "1")
	r.startSession("startup")
	r.respond = func(*http.Request) *http.Response { return jsonResponse(403, map[string]any{"error": "no"}) }
	before := r.noticeCount()
	if _, failure := r.tool(nil); !strings.Contains(failure, "HTTP 403") {
		t.Fatalf("failure = %q", failure)
	}
	said := strings.Join(r.noticesSince(before), "\n")
	if !strings.Contains(said, "TypeSafe is not authenticated (TypeSafe returned HTTP 403.") || !strings.Contains(said, "Judgments will fail until the key is fixed.") {
		t.Fatalf("said = %q", said)
	}
	// A busy service is not an authentication problem: no callout.
	r.respond = func(*http.Request) *http.Response { return jsonResponse(503, map[string]any{}) }
	before = r.noticeCount()
	_, _ = r.tool(nil)
	if r.noticeCount() != before {
		t.Fatalf("a 503 must not call out: %v", r.noticesSince(before))
	}
}

func TestLogoutForgetsTheAuthRecord(t *testing.T) {
	r := newRig(t)
	t.Setenv("TYPESAFE_API_KEY", "offline-test-key")
	t.Setenv("PI_TYPESAFE_ENABLED", "1")
	r.startSession("startup")
	if _, failure := r.tool(nil); failure != "" {
		t.Fatal(failure)
	}
	record := filepath.Join(r.agentDir, "pi-typesafe", "auth-state.json")
	if !fileExists(record) {
		t.Fatal("a successful request records the verification")
	}
	r.command("logout")
	if fileExists(record) {
		t.Fatal("logout must forget verification and degradation")
	}
}

func TestEnableRefusesAnUnusableStoredKey(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	r := newRig(t)
	stored := filepath.Join(r.agentDir, "pi-typesafe", "auth.json")
	if err := os.MkdirAll(filepath.Dir(stored), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stored, []byte(`{"apiKey":"stored-key-0123456789abcdef"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(stored, 0o644)
	prior := r.confirmations
	r.command("enable")
	if !strings.HasPrefix(r.lastNotice(), "The stored key cannot be used. Refusing to read") || r.lastLevel() != "warning" || r.confirmations != prior {
		t.Fatalf("notice = %s (%s)", r.lastNotice(), r.lastLevel())
	}
	r.command("setup")
	if !strings.Contains(r.lastNotice(), "Key unusable — Refusing to read") {
		t.Fatalf("setup = %s", r.lastNotice())
	}
}

func TestSampleTestAsksForConsentThenRecordsAnEntry(t *testing.T) {
	r := newRig(t)
	t.Setenv("TYPESAFE_API_KEY", "offline-test-key")
	r.respond = func(*http.Request) *http.Response {
		return jsonResponse(200, map[string]any{"model": "jev-test", "usage": map[string]any{"input_tokens": 9, "output_tokens": 0}, "answers": map[string]any{
			"category":    map[string]any{"type": "choice", "choice": "billing", "confidence": 1, "probabilities": map[string]any{"billing": 1, "technical": 0, "other": 0}},
			"urgent":      map[string]any{"type": "noul", "noul": 0.8},
			"frustration": map[string]any{"type": "score", "score": 1, "confidence": 1, "probabilities": map[string]any{"0": 0, "1": 1, "2": 0}, "legend": map[string]any{"0": "a", "1": "b", "2": "c"}},
		}})
	}
	r.command("test")
	if len(r.confirmTitles) != 1 || r.confirmTitles[0] != "Send this TypeSafe request?" || !strings.Contains(r.confirmBodies[0], "api.typesafe.ai") {
		t.Fatalf("confirmations = %v %v", r.confirmTitles, r.confirmBodies)
	}
	if len(r.entryTypes) != 1 || r.entryTypes[0] != "typesafe-result" || r.network.Load() != 1 {
		t.Fatalf("entries = %v network=%d", r.entryTypes, r.network.Load())
	}
	// The entry keeps the question order for the renderer.
	if !strings.Contains(string(r.entries[0]), `"order":["category","urgent","frustration"]`) {
		t.Fatalf("entry = %s", r.entries[0])
	}
}

func TestRenderersDrawCallPartialAndEntry(t *testing.T) {
	r := newRig(t)
	render := func(args map[string]any) string {
		raw, failure := r.host.roundTrip(map[string]any{"method": "render_tool", "tool": "typesafe_evaluate", "args": mustJSON(args)})
		if failure != "" {
			t.Fatal(failure)
		}
		return string(raw)
	}
	if got := render(map[string]any{"card": "a", "phase": "call", "width": 80, "args": map[string]any{"questions": map[string]any{"a": 1, "b": 2}}}); !strings.Contains(got, "TypeSafe · 2 questions · external request") {
		t.Errorf("call = %s", got)
	}
	if got := render(map[string]any{"card": "a", "phase": "result", "width": 80, "options": map[string]any{"isPartial": true}}); !strings.Contains(got, "TypeSafe · waiting for response") {
		t.Errorf("partial = %s", got)
	}
	if got := render(map[string]any{"card": "a", "phase": "result", "width": 80, "result": map[string]any{"content": []any{map[string]any{"type": "text", "text": "plain failure"}}}}); !strings.Contains(got, "plain failure") {
		t.Errorf("text fallback = %s", got)
	}
	raw, failure := r.host.roundTrip(map[string]any{"method": "render_entry", "tool": "typesafe-result", "args": mustJSON(map[string]any{"entry": map[string]any{}, "width": 80})})
	if failure != "" || !strings.Contains(string(raw), "TypeSafe · no result") {
		t.Errorf("empty entry = %s %s", raw, failure)
	}
}

func TestToolContentIsTheEvaluationJSONTheModelReads(t *testing.T) {
	r := newRig(t)
	t.Setenv("TYPESAFE_API_KEY", "offline-test-key")
	t.Setenv("PI_TYPESAFE_ENABLED", "1")
	r.startSession("startup")
	raw, failure := r.host.Tool("typesafe_evaluate", defaultParams)
	if failure != "" {
		t.Fatal(failure)
	}
	var result struct {
		Content string         `json:"content"`
		Details map[string]any `json:"details"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	var content map[string]any
	if err := json.Unmarshal([]byte(result.Content), &content); err != nil {
		t.Fatalf("content is not JSON: %q", result.Content)
	}
	if content["model"] != "jev-test" || content["elapsedMs"] == nil || content["order"] != nil || result.Details["order"] == nil {
		t.Fatalf("content = %s details = %v", result.Content, result.Details)
	}
}
