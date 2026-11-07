package posthorse

import (
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func TestMessageTextToolIdentityAndCalls(t *testing.T) {
	if got := toolIdentity(nil, nil); got != "tool" {
		t.Errorf("toolIdentity(nil, nil) = %q", got)
	}
	if got := toolIdentity(new("notes"), new("ns")); got != `notes (namespace: "ns")` {
		t.Errorf("toolIdentity with namespace = %q", got)
	}
	call, ok := toolCallOf(rec{"type": "toolCall", "id": "c1", "name": "history", "arguments": rec{"op": "read"}, "executionArguments": nil, "async": true})
	if !ok || *call.id != "c1" || !call.async || !call.hasExecutionArguments || call.namespace != nil {
		t.Fatalf("toolCallOf = %+v, %v", call, ok)
	}
	if got := executionArgumentsText(call); got != "\nExecution arguments: null" {
		t.Errorf("a null execution argument is not undefined: %q", got)
	}
	call, _ = toolCallOf(rec{"type": "toolCall"})
	if executionArgumentsText(call) != "" || call.hasArguments {
		t.Errorf("absent members are undefined: %+v", call)
	}
	if _, ok := toolCallOf(rec{"type": "text"}); ok {
		t.Error("a text block is not a tool call")
	}
	if _, ok := toolCallOf("nope"); ok {
		t.Error("a string is not a tool call")
	}
	calls := toolCalls([]any{rec{"type": "toolCall", "name": "a"}, "x", rec{"type": "text"}, rec{"type": "toolCall", "name": "b"}})
	if len(calls) != 2 || *calls[1].name != "b" {
		t.Errorf("toolCalls = %+v", calls)
	}
	if toolCalls("not an array") != nil {
		t.Error("non-array content has no calls")
	}
}

func TestTextOf(t *testing.T) {
	if got := textOf(rec{"content": "plain"}); got != "plain" {
		t.Errorf("string content: %q", got)
	}
	message := rec{"content": []any{
		rec{"type": "text", "text": "hello"},
		rec{"type": "thinking", "thinking": "hmm"},
		rec{"type": "text", "text": ""},
		rec{"type": "image", "data": "d", "mimeType": "image/png"},
		rec{"type": "toolCall", "id": "c1", "name": "notes", "arguments": rec{"b": 1.0, "a": "x"}, "executionArguments": rec{"z": true}},
		rec{"type": "toolCall", "name": "bare"},
		"junk",
	}}
	want := "hello\nhmm\nnotes {\"a\":\"x\",\"b\":1}\nCall ID: c1\nExecution arguments: {\"z\":true}\nbare {}"
	if got := textOf(message); got != want {
		t.Errorf("textOf:\n got %q\nwant %q", got, want)
	}
	if got := textOf(rec{"content": 5.0}); got != "" {
		t.Errorf("number content: %q", got)
	}
}

func TestAssistantFailure(t *testing.T) {
	for _, tc := range []struct {
		message rec
		want    string
	}{
		{rec{"role": "user", "stopReason": "error"}, ""},
		{rec{"role": "assistant", "stopReason": "stop"}, ""},
		{rec{"role": "assistant", "stopReason": "error"}, "[error]"},
		{rec{"role": "assistant", "stopReason": "aborted", "errorMessage": "cancelled"}, "[aborted] cancelled"},
		{rec{"role": "assistant", "errorMessage": "boom"}, "[error] boom"},
		{rec{"role": "assistant", "stopReason": "length", "errorMessage": "x"}, "[length] x"},
	} {
		if got := assistantFailure(tc.message); got != tc.want {
			t.Errorf("assistantFailure(%v) = %q, want %q", tc.message, got, tc.want)
		}
	}
}

func TestImagesAndSummary(t *testing.T) {
	content := []any{rec{"type": "image", "data": "a", "mimeType": "image/png"}, rec{"type": "image", "data": "b"}, rec{"type": "image", "data": "c", "mimeType": ""}, rec{"type": "image", "data": "d", "mimeType": "image/png"}}
	images := imagesOf(content)
	if len(images) != 3 {
		t.Fatalf("imagesOf = %+v", images)
	}
	if got := imageSummary(images); got != "[3 images: image/png, unknown type]" {
		t.Errorf("imageSummary = %q", got)
	}
	if got := imageSummary(images[:1]); got != "[1 image: image/png]" {
		t.Errorf("singular summary = %q", got)
	}
	if imageSummary(nil) != "" {
		t.Error("no images, no summary")
	}
}

func TestSafeJsonStringify(t *testing.T) {
	for _, tc := range []struct {
		in   any
		want string
	}{
		{nil, "null"}, {jsUndef, "undefined"}, {"a\"b", `"a\"b"`}, {rec{"x": []any{1.0, "<"}}, `{"x":[1,"<"]}`}, {func() {}, "[unserializable]"},
	} {
		if got := safeJsonStringify(tc.in); got != tc.want {
			t.Errorf("safeJsonStringify(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRecoveryTools(t *testing.T) {
	for _, name := range []string{"history", "notes", "new_context"} {
		if !isRecoveryTool(name) {
			t.Errorf("%s is a recovery tool", name)
		}
	}
	if isRecoveryTool("edit") || isRecoveryTool(nil) || isRecoveryTool(5.0) {
		t.Error("only the three Posthorse tools recover")
	}
	if !isRecoveryCall(rec{"type": "toolCall", "name": "notes"}) || isRecoveryCall(rec{"type": "toolCall"}) || isRecoveryCall(rec{"type": "text", "name": "notes"}) {
		t.Error("isRecoveryCall")
	}
}

func TestShortKey(t *testing.T) {
	// sha256("") = e3b0c442 98fc1c14 ... : base64url starts 47DEQpj8HB.
	if got := shortKey(""); got != "47DEQpj8HB" {
		t.Errorf("shortKey(\"\") = %q", got)
	}
	if shortKey("a") == shortKey("b") || len(shortKey("anything")) != 10 {
		t.Error("shortKey is ten distinguishing characters")
	}
	// A lone surrogate hashes as U+FFFD, rejoined halves as the astral character.
	lone := u16string([]uint16{0xD83D})
	if shortKey(lone) != shortKey("\uFFFD") {
		t.Error("a lone surrogate hashes as U+FFFD")
	}
	if shortKey(lone+u16string([]uint16{0xDE00})) != shortKey("😀") {
		t.Error("rejoined halves hash as the character")
	}
}

func TestSourceTag(t *testing.T) {
	if sourceTag("session.jsonl") != "" || sourceTag("sub/session.jsonl") != " [subagent]" {
		t.Error("nested session files are subagent runs")
	}
}

func TestExcerptMarkerLength(t *testing.T) {
	if u16len(excerptMarker) != excerptMarkerLength {
		t.Errorf("marker length %d, constant %d", u16len(excerptMarker), excerptMarkerLength)
	}
}

func TestExcerpt(t *testing.T) {
	long := strings.Repeat("a", 30) + strings.Repeat("b", 30)
	for _, tc := range []struct {
		limit int
		want  string
	}{
		{100, long},
		{60, long},
		{20, long[:20]},
		{10, long[:10]},
		{0, ""},
		{30, long[:5] + excerptMarker + long[len(long)-5:]},
		{21, long[:0] + excerptMarker[:0] + long[:0] + excerptMarker + long[len(long)-1:]},
	} {
		if got := excerpt(long, tc.limit); got != tc.want {
			t.Errorf("excerpt(limit %d) = %q, want %q", tc.limit, got, tc.want)
		}
	}
	// The cut counts code units: it can split an astral character, as slice does.
	got := excerpt(strings.Repeat("😀", 20), 25)
	if u16len(got) != 25 {
		t.Errorf("excerpt length in units = %d, want 25", u16len(got))
	}
}

func TestExcerptAroundAndBoundedBlock(t *testing.T) {
	text := "0123456789"
	for _, tc := range []struct {
		index, before, length int
		want                  string
	}{
		{5, 2, 4, "…3456…"}, {1, 5, 4, "0123…"}, {8, 1, 10, "…789"}, {0, 0, 99, "0123456789"},
	} {
		if got := excerptAround(text, tc.index, tc.before, tc.length); got != tc.want {
			t.Errorf("excerptAround(%d,%d,%d) = %q, want %q", tc.index, tc.before, tc.length, got, tc.want)
		}
	}
	if got := boundedBlock("H", "body", 100); got != "H\nbody" {
		t.Errorf("boundedBlock = %q", got)
	}
	if got := boundedBlock("HEADER", "body", 6); got != "HEADER" {
		t.Errorf("a limit with no room keeps only the header: %q", got)
	}
	if got := boundedBlock("H", strings.Repeat("x", 100), 30); u16len(got) != 30 {
		t.Errorf("boundedBlock length = %d, want 30", u16len(got))
	}
}

func TestRequireValue(t *testing.T) {
	if got, err := requireValue("x", true, "path", "read"); got != "x" || err != nil {
		t.Errorf("present: %q, %v", got, err)
	}
	for _, present := range []bool{true, false} {
		_, err := requireValue("", present, "path", "read")
		if err == nil || err.Error() != `"path" is required for op "read".` {
			t.Errorf("missing value: %v", err)
		}
	}
}

func TestTextResult(t *testing.T) {
	result := textResult("t", []imageLike{{data: "d", mimeType: "image/png"}}, nil)
	if result.Content != "t" || len(result.Images) != 1 || result.Images[0] != (sdk.ImageContent{Data: "d", MimeType: "image/png"}) {
		t.Errorf("textResult = %+v", result)
	}
	if result.Details != nil {
		t.Error("a nil display leaves details undefined, not a typed nil")
	}
	if textResult("t", nil, displayKind("context")).Details == nil {
		t.Error("a display is carried")
	}
}

func TestIsRecordAndReminderType(t *testing.T) {
	if !isRecord(rec{}) || isRecord(nil) || isRecord([]any{}) || isRecord("x") {
		t.Error("isRecord")
	}
	if !isReminderType("posthorse-reminder") || isReminderType("other") || isReminderType(nil) {
		t.Error("isReminderType")
	}
}

func TestDynHelpers(t *testing.T) {
	m := rec{"s": "x", "n": 2.0, "b": true, "r": rec{"k": rec{"deep": "v"}}, "a": []any{1.0}}
	if s, ok := str(m, "s"); !ok || s != "x" {
		t.Error("str")
	}
	if _, ok := str(m, "n"); ok {
		t.Error("str on a number")
	}
	if n, ok := num(m, "n"); !ok || n != 2 {
		t.Error("num")
	}
	if b, ok := boolean(m, "b"); !ok || !b {
		t.Error("boolean")
	}
	if _, ok := recOf(m["r"]); !ok {
		t.Error("recOf")
	}
	if _, ok := recOf(m["a"]); ok {
		t.Error("recOf on an array")
	}
	if a, ok := arrOf(m["a"]); !ok || len(a) != 1 {
		t.Error("arrOf")
	}
	if getPath(m, "r", "k", "deep") != "v" || getPath(m, "r", "x", "deep") != nil || getPath(m, "s", "deep") != nil {
		t.Error("getPath")
	}
}

func BenchmarkTextOf(b *testing.B) {
	message := rec{"content": []any{
		rec{"type": "text", "text": strings.Repeat("hello ", 200)},
		rec{"type": "toolCall", "id": "c1", "name": "notes", "arguments": rec{"op": "read", "path": "a.md"}},
	}}
	b.ReportAllocs()
	for range b.N {
		_ = textOf(message)
	}
}

func BenchmarkExcerpt(b *testing.B) {
	text := strings.Repeat("abcdéf😀h", 4096)
	b.ReportAllocs()
	for range b.N {
		_ = excerpt(text, 2000)
	}
}

func BenchmarkShortKey(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		_ = shortKey("/home/user/.pi/agent/sessions/--project--/2026-01-01T00-00-00-000Z_abcdef.jsonl")
	}
}
