package pi_typesafe_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	pi_typesafe "github.com/MichaelKinsy/pigpen/components/pi-typesafe/extensions/pi-typesafe"
)

func matches(pattern, s string) bool { return regexp.MustCompile(pattern).MatchString(s) }

func TestExtension(t *testing.T) {
	r := newRig(t)
	t.Setenv("TYPESAFE_API_KEY", "offline-test-key")
	storedPath := filepath.Join(r.agentDir, "pi-typesafe", "auth.json")

	tw(t, "extension", "Pi loads a tool, a slash command, and a result renderer without network calls", func(t *testing.T) {
		if r.network.Load() != 0 {
			t.Fatal("registering must not touch the network")
		}
		if !r.host.tools["typesafe_evaluate"] || !r.host.cmds["typesafe"] {
			t.Fatal("tool or command missing")
		}
		raw, failure := r.host.roundTrip(map[string]any{"method": "command_argument_completions", "tool": "typesafe", "args": json.RawMessage(`"pla"`)})
		if failure != "" || !strings.Contains(string(raw), `"playground"`) {
			t.Fatalf("completions = %s, %s", raw, failure)
		}
		guidelines := pi_typesafe.ToolGuidelines()
		found := false
		for _, g := range guidelines {
			found = found || matches(`one question per item per dimension`, g)
		}
		if !found {
			t.Error("the per-item guideline is missing")
		}
		// Models that never saw a payload author questions as an array; the guidelines must show one that actually validates.
		var example string
		for _, g := range guidelines {
			if strings.Contains(g, `"state":`) {
				example = g
			}
		}
		if example == "" || strings.Contains(example, "\n") || len(example) >= 1024 {
			t.Fatalf("the example must exist on one short line: %q", example)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(example[strings.Index(example, "{"):]), &payload); err != nil {
			t.Fatal(err)
		}
		var kinds []string
		for _, q := range payload["questions"].(map[string]any) {
			kinds = append(kinds, q.(map[string]any)["type"].(string))
		}
		if len(kinds) != 3 || !strings.Contains(strings.Join(sortStrings(kinds), ","), "choice,noul,score") {
			t.Errorf("kinds = %v", kinds)
		}
		if !matches(`named state field`, pi_typesafe.ToolDescription("typesafe")) {
			t.Error("the description must teach named state fields")
		}
	})
	tw(t, "extension", "default-disabled tool cannot submit data", func(t *testing.T) {
		if _, failure := r.tool(nil); !matches(`disabled`, failure) {
			t.Fatalf("failure = %q", failure)
		}
		if r.network.Load() != 0 {
			t.Fatal("a disabled tool must not submit")
		}
	})
	tw(t, "extension", "setup and status never display the API key", func(t *testing.T) {
		r.command("setup")
		r.command("status")
		found := false
		for _, n := range r.noticesSince(0) {
			found = found || strings.Contains(n, "TypeSafe key: TYPESAFE_API_KEY")
			if strings.Contains(n, "offline-test-key") {
				t.Fatalf("the key leaked: %s", n)
			}
		}
		if !found {
			t.Fatalf("notices = %v", r.noticesSince(0))
		}
	})
	tw(t, "extension", "status names the model the configured backend actually sends", func(t *testing.T) {
		r.command("status")
		if !strings.Contains(r.lastNotice(), "Model: jev-latest.") {
			t.Fatalf("status = %s", r.lastNotice())
		}
	})
	tw(t, "extension", "login refuses to shadow an environment key", func(t *testing.T) {
		r.command("login")
		if !strings.Contains(r.lastNotice(), "takes precedence") || r.modelList.Load() != 0 {
			t.Fatalf("notice = %s", r.lastNotice())
		}
	})
	tw(t, "extension", "declining consent keeps the tool disabled", func(t *testing.T) {
		r.confirmResult = false
		r.command("enable")
		if _, failure := r.tool(nil); !matches(`disabled`, failure) || r.network.Load() != 0 {
			t.Fatalf("failure = %q", failure)
		}
	})
	tw(t, "extension", "explicit consent enables the real registered tool and returns structured results", func(t *testing.T) {
		r.confirmResult = true
		r.command("enable")
		details, failure := r.tool(nil)
		if failure != "" || yes(details) != 0.9 || r.network.Load() != 1 || r.confirmations < 2 {
			t.Fatalf("details=%v failure=%q network=%d confirmations=%d", details, failure, r.network.Load(), r.confirmations)
		}
		// The renderer draws the result at every width without overflowing it.
		detailsJSON, _ := json.Marshal(details)
		for _, width := range []int{40, 80, 120} {
			raw, failure := r.host.roundTrip(map[string]any{"method": "render_tool", "tool": "typesafe_evaluate", "args": mustJSON(map[string]any{
				"card": "c1", "phase": "result", "width": width, "options": map[string]any{"expanded": true, "isPartial": false},
				"result": map[string]any{"content": []any{}, "details": json.RawMessage(detailsJSON)},
			})})
			if failure != "" {
				t.Fatalf("render failed: %s", failure)
			}
			var lines struct{ Lines []string }
			_ = json.Unmarshal(raw, &lines)
			for _, l := range lines.Lines {
				if w := len([]rune(l)); w > width {
					t.Errorf("width %d: line of %d cells: %q", width, w, l)
				}
			}
			if !strings.Contains(strings.Join(lines.Lines, "\n"), "P(yes)") {
				t.Errorf("width %d: no P(yes) in %v", width, lines.Lines)
			}
		}
	})
	tw(t, "extension", "disable stops future calls without resetting usage", func(t *testing.T) {
		r.command("disable")
		if _, failure := r.tool(nil); !matches(`disabled`, failure) {
			t.Fatalf("failure = %q", failure)
		}
		r.command("status")
		if !strings.Contains(r.lastNotice(), "1/20 attempts") {
			t.Fatalf("status = %s", r.lastNotice())
		}
	})
	tw(t, "extension", "invalid playground JSON and cancellation do not submit data", func(t *testing.T) {
		r.editorText, r.editorOK = `{"broken":`, true
		r.command("playground")
		if !strings.Contains(r.lastNotice(), "Invalid JSON") {
			t.Fatalf("notice = %s", r.lastNotice())
		}
		r.editorOK = false
		r.command("playground")
		if r.network.Load() != 1 {
			t.Fatalf("network = %d", r.network.Load())
		}
	})
	tw(t, "extension", "playground validates questions before requesting consent", func(t *testing.T) {
		r.editorText, r.editorOK = `{"state":"example","questions":{}}`, true
		prior := r.confirmations
		r.command("playground")
		if r.confirmations != prior || r.network.Load() != 1 || !strings.Contains(r.lastNotice(), "Invalid evaluation request") {
			t.Fatalf("confirmations=%d network=%d notice=%s", r.confirmations, r.network.Load(), r.lastNotice())
		}
	})
	tw(t, "extension", "test command requires confirmation and does not enable agent calls", func(t *testing.T) {
		r.confirmResult = false
		r.command("test")
		if r.network.Load() != 1 {
			t.Fatalf("network = %d", r.network.Load())
		}
		if _, failure := r.tool(nil); !matches(`disabled`, failure) {
			t.Fatalf("failure = %q", failure)
		}
	})
	tw(t, "extension", "login verifies, stores with owner-only permissions, and never echoes the key", func(t *testing.T) {
		t.Setenv("TYPESAFE_API_KEY", "")
		r.customResult = nil
		r.command("login")
		if !strings.Contains(r.lastNotice(), "cancelled") || fileExists(storedPath) {
			t.Fatalf("cancel: %s", r.lastNotice())
		}
		r.customResult = "nope"
		r.command("login")
		if !strings.Contains(r.lastNotice(), "does not look like") || r.modelList.Load() != 0 || fileExists(storedPath) {
			t.Fatalf("bad key: %s", r.lastNotice())
		}
		r.customResult = "ts_live_key_0123456789abcdef"
		r.command("login")
		if r.modelList.Load() != 1 || !strings.Contains(r.lastNotice(), "Key verified (1 model available)") {
			t.Fatalf("login: %s", r.lastNotice())
		}
		for _, n := range r.noticesSince(0) {
			if strings.Contains(n, "ts_live_key") {
				t.Fatalf("the key leaked: %s", n)
			}
		}
		if info, err := os.Stat(storedPath); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
			t.Fatalf("stored file: %v %v", info, err)
		}
		r.command("status")
		if !strings.Contains(r.lastNotice(), "TypeSafe key: /typesafe login") {
			t.Fatalf("status: %s", r.lastNotice())
		}
		r.command("setup")
		if !strings.Contains(r.lastNotice(), "configured via /typesafe login") {
			t.Fatalf("setup: %s", r.lastNotice())
		}
		// The stored key powers the real tool after consent.
		r.confirmResult = true
		r.command("enable")
		if _, failure := r.tool(nil); failure != "" || r.network.Load() != 2 {
			t.Fatalf("tool: %q network=%d", failure, r.network.Load())
		}
		r.command("logout")
		if fileExists(storedPath) {
			t.Fatal("logout must delete the key")
		}
		if _, failure := r.tool(nil); !matches(`disabled`, failure) {
			t.Fatalf("failure = %q", failure)
		}
		r.command("status")
		if !strings.Contains(r.lastNotice(), "TypeSafe key: missing") {
			t.Fatalf("status: %s", r.lastNotice())
		}
		t.Setenv("TYPESAFE_API_KEY", "offline-test-key")
	})
	tw(t, "extension", "new sessions reset opt-in; headless opt-in is explicit", func(t *testing.T) {
		r.startSession("new")
		if _, failure := r.tool(nil); !matches(`disabled`, failure) {
			t.Fatalf("failure = %q", failure)
		}
		t.Setenv("PI_TYPESAFE_ENABLED", "1")
		r.startSession("startup")
		if _, failure := r.tool(nil); failure != "" || r.network.Load() != 3 {
			t.Fatalf("failure=%q network=%d", failure, r.network.Load())
		}
		r.command("status")
		if !strings.Contains(r.lastNotice(), "1/20 attempts") {
			t.Fatalf("status: %s", r.lastNotice())
		}
	})
	tw(t, "extension", "an enabled session with no key announces that judgments are skipped", func(t *testing.T) {
		t.Setenv("TYPESAFE_API_KEY", "")
		t.Setenv("PI_TYPESAFE_ENABLED", "1")
		before := r.noticeCount()
		r.startSession("startup")
		said := strings.Join(r.noticesSince(before), "\n")
		if !strings.Contains(said, "judgments are skipped") || !strings.Contains(said, "TypeSafe key: missing") {
			t.Fatalf("said = %q", said)
		}
		// A key that appears later silences the next startup notice.
		t.Setenv("TYPESAFE_API_KEY", "offline-test-key")
		again := r.noticeCount()
		r.startSession("reload")
		for _, n := range r.noticesSince(again) {
			if strings.Contains(n, "judgments are skipped") {
				t.Fatalf("stale callout: %s", n)
			}
		}
	})
	tw(t, "extension", "a rejected key is called out once per session and shows up in status", func(t *testing.T) {
		t.Setenv("PI_TYPESAFE_ENABLED", "1")
		r.startSession("startup")
		before := r.noticeCount()
		r.respond = func(*http.Request) *http.Response {
			return jsonResponse(401, map[string]any{"error": map[string]any{"message": "invalid key"}})
		}
		defer func() { r.respond = nil }()
		for i := 0; i < 2; i++ {
			if _, failure := r.tool(nil); !matches(`HTTP 401`, failure) {
				t.Fatalf("failure = %q", failure)
			}
		}
		// Two failed calls, one callout: the reason is loud once, not once per call.
		count := 0
		for _, n := range r.noticesSince(before) {
			if strings.Contains(n, "not authenticated") {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("callouts = %d in %v", count, r.noticesSince(before))
		}
		r.command("status")
		last := r.lastNotice()
		if !strings.Contains(last, "was rejected") || !strings.Contains(last, "Today ") || !strings.Contains(last, "failed") {
			t.Fatalf("status = %s", last)
		}
	})
	tw(t, "extension", "the registered tool admits the same near-miss aliases as the library", func(t *testing.T) {
		t.Setenv("PI_TYPESAFE_ENABLED", "1")
		r.startSession("startup")
		before := r.network.Load()
		details, failure := r.tool(map[string]any{"state": "synthetic", "questions": map[string]any{"yes": map[string]any{"type": "noul", "instructions": "Is this synthetic?", "criteria": "Is this synthetic data?"}}})
		if failure != "" || r.network.Load() != before+1 || yes(details) != 0.9 {
			t.Fatalf("failure=%q details=%v", failure, details)
		}
	})
}

func sortStrings(in []string) []string {
	out := append([]string(nil), in...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
