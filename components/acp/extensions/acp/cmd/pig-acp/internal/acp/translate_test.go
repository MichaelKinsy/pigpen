package acp

// Twins of test/unit/pi-tools.test.ts, prompt-to-pi-message.test.ts, pi-messages.test.ts.

import (
	"encoding/base64"
	"regexp"
	"strings"
	"testing"
)

func TestPiTools(t *testing.T) {
	tw(t, "unit/pi-tools", "toolResultToText: extracts text from content blocks", func(t *testing.T) {
		got := ToolResultToText(map[string]any{"content": []any{
			map[string]any{"type": "text", "text": "hello"}, map[string]any{"type": "text", "text": " world"}}})
		if got != "hello world" {
			t.Errorf("got %q", got)
		}
	})
	tw(t, "unit/pi-tools", "toolResultToText: prefers details.diff when present", func(t *testing.T) {
		got := ToolResultToText(map[string]any{
			"content": []any{map[string]any{"type": "text", "text": "Successfully replaced 2 block(s) in a.txt."}},
			"details": map[string]any{"diff": "--- a\n+++ b\n"}})
		if got != "--- a\n+++ b\n" {
			t.Errorf("got %q", got)
		}
	})
	tw(t, "unit/pi-tools", "toolResultToText: falls back to JSON", func(t *testing.T) {
		got := ToolResultToText(map[string]any{"a": 1})
		if !regexp.MustCompile(`"a": 1`).MatchString(got) {
			t.Errorf("got %q", got)
		}
	})
	tw(t, "unit/pi-tools", "toolResultToText: extracts bash stdout/stderr from details", func(t *testing.T) {
		got := ToolResultToText(map[string]any{"details": map[string]any{"stdout": "ok\n", "stderr": "warn\n", "exitCode": 0}})
		for _, want := range []string{"ok", "stderr:", "warn", "exit code: 0"} {
			if !strings.Contains(got, want) {
				t.Errorf("%q missing %q", got, want)
			}
		}
	})
}

func TestPromptToPiMessage(t *testing.T) {
	tw(t, "unit/prompt-to-pi-message", "promptToPiMessage: concatenates text and resource links", func(t *testing.T) {
		msg, images := PromptToPiMessage([]ContentBlock{
			{"type": "text", "text": "Hello"},
			{"type": "resource_link", "uri": "file:///tmp/foo.txt", "name": "foo"},
			{"type": "text", "text": " world"}})
		if msg != "Hello\n[Context] file:///tmp/foo.txt world" || len(images) != 0 {
			t.Errorf("msg=%q images=%v", msg, images)
		}
	})
	tw(t, "unit/prompt-to-pi-message", "promptToPiMessage: includes embedded resource text as marker", func(t *testing.T) {
		msg, images := PromptToPiMessage([]ContentBlock{{"type": "resource", "resource": map[string]any{"uri": "file:///tmp/a.txt", "mimeType": "text/plain", "text": "hi"}}})
		if msg != "\n[Embedded Context] file:///tmp/a.txt (text/plain)\nhi" || len(images) != 0 {
			t.Errorf("msg=%q", msg)
		}
	})
	tw(t, "unit/prompt-to-pi-message", "promptToPiMessage: includes embedded resource blob as marker", func(t *testing.T) {
		blob := base64.StdEncoding.EncodeToString([]byte("xyz"))
		msg, images := PromptToPiMessage([]ContentBlock{{"type": "resource", "resource": map[string]any{"uri": "file:///tmp/a.bin", "mimeType": "application/octet-stream", "blob": blob}}})
		if msg != "\n[Embedded Context] file:///tmp/a.bin (application/octet-stream, 3 bytes)" || len(images) != 0 {
			t.Errorf("msg=%q", msg)
		}
	})
	tw(t, "unit/prompt-to-pi-message", "promptToPiMessage: includes audio as marker", func(t *testing.T) {
		data := base64.StdEncoding.EncodeToString([]byte("abc"))
		msg, images := PromptToPiMessage([]ContentBlock{{"type": "audio", "mimeType": "audio/wav", "data": data}})
		// Renamed identity: the marker names pig-acp, where the original names pi-acp.
		if msg != "\n[Audio] (audio/wav, 3 bytes) not supported by pig-acp" || len(images) != 0 {
			t.Errorf("msg=%q", msg)
		}
	})
	tw(t, "unit/prompt-to-pi-message", "promptToPiMessage: maps image to pi image content", func(t *testing.T) {
		b64 := base64.StdEncoding.EncodeToString([]byte("abc"))
		msg, images := PromptToPiMessage([]ContentBlock{{"type": "text", "text": "see"}, {"type": "image", "mimeType": "image/png", "data": b64, "uri": "img-1"}})
		if msg != "see" || len(images) != 1 {
			t.Fatalf("msg=%q images=%v", msg, images)
		}
		jsonEqual(t, images[0], map[string]any{"type": "image", "mimeType": "image/png", "data": b64})
	})
}

func TestPiMessages(t *testing.T) {
	tw(t, "unit/pi-messages", "normalizePiMessageText: supports string", func(t *testing.T) {
		if NormalizePiMessageText("hello") != "hello" {
			t.Fail()
		}
	})
	tw(t, "unit/pi-messages", "normalizePiMessageText: joins text blocks", func(t *testing.T) {
		got := NormalizePiMessageText([]any{
			map[string]any{"type": "text", "text": "a"}, map[string]any{"type": "text", "text": "b"}, map[string]any{"type": "not_text", "x": 1}})
		if got != "ab" {
			t.Errorf("got %q", got)
		}
	})
	tw(t, "unit/pi-messages", "normalizePiAssistantText: joins only text blocks", func(t *testing.T) {
		got := NormalizePiAssistantText([]any{
			map[string]any{"type": "text", "text": "hi"}, map[string]any{"type": "thinking", "text": "..."}, map[string]any{"type": "text", "text": "!"}})
		if got != "hi!" {
			t.Errorf("got %q", got)
		}
	})
}
