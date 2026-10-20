package powerline_footer

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Tests that pin behavior the first cdquote tests left unchecked. They use code of the cdquote slice only.

// pi-ai's uuidv7 raises its 42-bit sequence by exactly one per id (the rand_a/rand_b fields), so ids of one millisecond differ
// by one in that sequence, whatever the random tail.
func TestNewUUIDv7SequenceGrowsByOne(t *testing.T) {
	uuidMu.Lock()
	uuidState.lastTimestamp, uuidState.seeded = 0, false
	uuidMu.Unlock()
	seqOf := func(id string) uint64 {
		hex := strings.ReplaceAll(id, "-", "")
		field := func(from, to int) uint64 { v, _ := strconv.ParseUint(hex[from:to], 16, 64); return v }
		b6, b7, b8, b9, b10, b11 := field(12, 14), field(14, 16), field(16, 18), field(18, 20), field(20, 22), field(22, 24)
		return (b6&0x0f)<<38 | b7<<30 | (b8&0x3f)<<24 | b9<<16 | b10<<8 | b11
	}
	ms := time.Date(2026, 10, 20, 14, 39, 28, 0, time.UTC).UnixMilli()
	first, err := newUUIDv7(ms)
	if err != nil {
		t.Fatal(err)
	}
	prev := seqOf(first) >> 1
	for i := 0; i < 20; i++ {
		id, err := newUUIDv7(ms)
		if err != nil {
			t.Fatal(err)
		}
		got := seqOf(id) >> 1
		if got != prev+1 {
			t.Fatalf("id %d: sequence %d after %d", i, got, prev)
		}
		prev = got
	}
}

func TestReplyIdMatchesOnlyAPrefix(t *testing.T) {
	branch := []map[string]any{
		messageEntry("abc111", "2026-08-23T00:00:00.000Z", "user", "first"),
		messageEntry("zzabc2", "2026-08-23T00:01:00.000Z", "user", "second"),
	}
	ctx := &fakeQuoteCtx{mode: "rpc", text: "", branch: branch}
	if err := reply("abc", ctx); err != nil {
		t.Fatal(err)
	}
	if len(ctx.notifications) != 1 || ctx.notifications[0] != "Inserted quote from user message abc111." {
		t.Errorf("notes %q", ctx.notifications)
	}
}

// An RPC option shows at most 220 UTF-16 units of the message: 220 stay whole, 221 get the ellipsis.
func TestRPCOptionExcerptLimit(t *testing.T) {
	for n, want := range map[int]string{220: strings.Repeat("a", 220), 221: strings.Repeat("a", 219) + "…"} {
		ctx := &fakeQuoteCtx{mode: "rpc", branch: []map[string]any{messageEntry("id1", "2026-08-23T00:00:00.000Z", "user", strings.Repeat("a", n))}}
		if err := reply("", ctx); err != nil {
			t.Fatal(err)
		}
		if len(ctx.selectOptions) != 1 || ctx.selectOptions[0] != "id1 user: "+want {
			t.Errorf("%d chars: option %q", n, ctx.selectOptions)
		}
	}
}

// A message of another day in the same month still shows its date.
func TestFormatTimestampSameMonthOtherDay(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.Local)
	other := time.Date(2026, 10, 5, 9, 30, 0, 0, time.Local).UnixMilli()
	if got := formatTimestamp(other, now); got != "10/5/2026 09:30 AM" {
		t.Errorf("got %q", got)
	}
}

// Typing a character puts the selection back on the first match, as the original does.
func TestPickerTypingResetsTheSelectionToTheFirstMatch(t *testing.T) {
	var candidates []quoteCandidate
	for i := range 4 {
		candidates = append(candidates, quoteCandidate{ID: fmt.Sprintf("c%d", i), Role: "user", Text: "message", SearchText: fmt.Sprintf("c%d user message", i)})
	}
	p := newQuotePicker(candidates, pickerPlainTheme{}, time.Now, nil, nil)
	p.HandleInput("\x1b[B")
	p.HandleInput("\x1b[B") // third candidate selected
	p.HandleInput("m")       // every candidate still matches
	selected, _ := p.HandleInput("\r")
	if selected == nil || selected.ID != "c0" {
		t.Errorf("Enter after typing chose %+v, want the first match c0", selected)
	}
}

// A path that merely starts with a tilde is not the home directory.
func TestExpandHomePathNeedsTheSlash(t *testing.T) {
	for in, want := range map[string]string{"~": "/h", "~/x": "/h/x", "~x": "~x", "~~": "~~", "a/~/b": "a/~/b"} {
		if got := expandHomePath(in, "/h"); got != want {
			t.Errorf("expandHomePath(%q) = %q, want %q", in, got, want)
		}
	}
}
