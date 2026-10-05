package sessioningest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	sessioningest "github.com/MichaelKinsy/pigpen/session-ingest"
)

// Layer-1 cases through the fake host: registration and the wire shape of a tool call.

func TestRegistersOnlyReadSession(t *testing.T) {
	h := StartHost(t, sessioningest.Extension(), HostOptions{})
	if len(h.tools) != 1 || !h.tools["read_session"] {
		t.Fatalf("tools = %v, want only read_session", h.tools)
	}
	if len(h.cmds) != 0 {
		t.Errorf("commands = %v, want none", h.cmds)
	}
}

func TestReadSessionThroughTheHost(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	body := strings.Join([]string{
		`{"type":"session","id":"abcdef0123456789","cwd":"/w","timestamp":"2026-01-01T00:00:00.000Z"}`,
		`{"type":"message","timestamp":"2026-01-01T00:00:01.000Z","message":{"role":"user","content":"hello there"}}`,
		`{"type":"message","timestamp":"2026-01-01T00:00:02.000Z","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	h := StartHost(t, sessioningest.Extension(), HostOptions{})
	raw, errText := h.Tool("read_session", map[string]any{"path": path, "mode": "toc"})
	if errText != "" {
		t.Fatalf("tool error: %s", errText)
	}
	if !strings.Contains(string(raw), "hello there") || !strings.Contains(string(raw), "Session TOC") {
		t.Fatalf("result = %s", raw)
	}
}

func TestReadSessionErrorsAreToolErrors(t *testing.T) {
	h := StartHost(t, sessioningest.Extension(), HostOptions{})
	_, errText := h.Tool("read_session", map[string]any{"path": filepath.Join(t.TempDir(), "missing.jsonl")})
	if errText == "" {
		t.Fatal("a missing file must be reported as a tool error")
	}
}

func TestSchemaAdvertisesOnlyImplementedModes(t *testing.T) {
	h := StartHost(t, sessioningest.Extension(), HostOptions{})
	for _, mode := range []string{"toc", "query", "slice", "turn", "tools", "stats"} {
		path := filepath.Join(t.TempDir(), "s.jsonl")
		if err := os.WriteFile(path, []byte(`{"type":"session","id":"x"}`+"\n"+`{"type":"message","message":{"role":"user","content":"a"}}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		params := map[string]any{"path": path, "mode": mode, "query": "a", "turn": 1}
		raw, errText := h.Tool("read_session", params)
		if strings.Contains(errText, "not implemented") || strings.Contains(string(raw), "not implemented") {
			t.Errorf("mode %s is advertised but not implemented: %s %s", mode, errText, raw)
		}
	}
}
