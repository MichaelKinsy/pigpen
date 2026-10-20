package powerline_footer

import (
	"testing"
	"time"
)

// Twin of port/oracle/tests/quote-reply.test.ts.

// fakeQuoteCtx is the ctx the original's test builds: mode "rpc", a branch, an editor text and notify.
type fakeQuoteCtx struct {
	mode          string
	branch        []map[string]any
	text          string
	notifications []string
	levels        []string
	selectOptions []string
	selectTitle   string
	selectResult  string
	selectOK      bool
	pickResult    string
	canCustom     bool
	picked        []quoteCandidate
}

func (f *fakeQuoteCtx) Mode() string                      { return f.mode }
func (f *fakeQuoteCtx) Branch() ([]map[string]any, error) { return f.branch, nil }
func (f *fakeQuoteCtx) GetEditorText() (string, error)    { return f.text, nil }
func (f *fakeQuoteCtx) SetEditorText(text string)         { f.text = text }
func (f *fakeQuoteCtx) Now() time.Time                    { return time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC) }
func (f *fakeQuoteCtx) CanCustom() bool                   { return f.canCustom }
func (f *fakeQuoteCtx) Notify(message, level string) {
	f.notifications = append(f.notifications, message)
	f.levels = append(f.levels, level)
}
func (f *fakeQuoteCtx) Select(title string, options []string) (string, bool, error) {
	f.selectTitle, f.selectOptions = title, options
	return f.selectResult, f.selectOK, nil
}
func (f *fakeQuoteCtx) Pick(candidates []quoteCandidate) (string, error) {
	f.picked = candidates
	return f.pickResult, nil
}

func messageEntry(id, timestamp, role string, content any) map[string]any {
	return map[string]any{"type": "message", "id": id, "timestamp": timestamp, "message": map[string]any{"role": role, "content": content}}
}

func textBlocks(texts ...string) []any {
	var blocks []any
	for _, text := range texts {
		blocks = append(blocks, map[string]any{"type": "text", "text": text})
	}
	return blocks
}

func TestQuoteReplyTwins(t *testing.T) {
	tw(t, "quote-reply", "reply quotes a matching message and preserves the current draft", func(t *testing.T) {
		ctx := &fakeQuoteCtx{mode: "rpc", text: "draft", branch: []map[string]any{
			messageEntry("old", "2026-08-23T00:00:00.000Z", "tool", textBlocks("ignore")),
			messageEntry("abc123", "2026-08-23T00:01:00.000Z", "user", textBlocks("Please inspect this.")),
		}}

		if err := reply("abc", ctx); err != nil {
			t.Fatal(err)
		}

		if want := "Quoted previous user message abc123 for reference only:\n\n> Please inspect this.\n\nMy reply:\ndraft"; ctx.text != want {
			t.Errorf("text = %q, want %q", ctx.text, want)
		}
		if len(ctx.notifications) != 1 || ctx.notifications[0] != "Inserted quote from user message abc123." {
			t.Errorf("notifications = %q", ctx.notifications)
		}
	})
}
