package tintinweb_subagents

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The suggestion provider runs on the editor's input path: once per character typed after `@`. These pin that it does
// bounded work (no roster for text that cannot be a mention) and measure what it costs with a large roster.

func bigRoster(records, tombs, types int) (fakeRoster, []typeInfo) {
	var f fakeRoster
	for i := 0; i < records; i++ {
		status := "completed"
		if i%3 == 0 {
			status = "running"
		}
		f.records = append(f.records, mentionRecord{Handle: fmt.Sprintf("explore-%d", i), Type: "Explore", Description: "find flaky tests in the repository", Status: status, StartedAt: int64(i)})
	}
	for i := 0; i < tombs; i++ {
		f.tombs = append(f.tombs, tombstone{Handle: fmt.Sprintf("plan-%d", i), Type: "Plan", Description: "sketch the migration", SessionFile: "/s.jsonl", CompletedAt: timeMs(int64(i))})
	}
	var ts []typeInfo
	for i := 0; i < types; i++ {
		ts = append(ts, typeInfo{Name: fmt.Sprintf("type-%d", i), Description: "A custom agent. It does things. Many things."})
	}
	return f, ts
}

func TestProviderBuildsNoRosterForTextThatIsNotAMention(t *testing.T) {
	var built atomic.Int32
	cur := newBuiltIn()
	p := newMentionProvider(cur.p, func() []mentionTarget { built.Add(1); return nil }, func() bool { return true })
	for _, line := range []string{"", "hello world", "mail@ex", "@src/", "=@ex", "a @b/c"} {
		suggest(p, line)
	}
	eq(t, built.Load(), int32(0))
	suggest(p, "@ex")
	eq(t, built.Load(), int32(1))
}

func TestProviderAnswersWhenTheRosterPanics(t *testing.T) {
	cur := newBuiltIn()
	p := newMentionProvider(cur.p, func() []mentionTarget { panic("roster") }, func() bool { return true })
	out := captureStderr(t, func() {
		if got := suggest(p, "@ex"); got != fileSuggestions {
			t.Fatalf("got %#v", got)
		}
	})
	eq(t, strings.Contains(out, "mention suggestions"), true)
}

func TestSteerQueueRunsInOrderAndIsBounded(t *testing.T) {
	var q serialQueue
	var got []int
	done := make(chan struct{})
	block := make(chan struct{})
	q.do(func() { <-block })
	for i := 0; i < 5; i++ {
		i := i
		q.do(func() { got = append(got, i) })
	}
	q.do(func() { close(done) })
	close(block)
	<-done
	eq(t, got, []int{0, 1, 2, 3, 4})
	// Bounded: with the worker held, the queue refuses past maxQueuedSteers.
	hold := make(chan struct{})
	q.do(func() { <-hold })
	accepted := 0
	for i := 0; i < maxQueuedSteers+10; i++ {
		if q.do(func() {}) {
			accepted++
		}
	}
	close(hold)
	eq(t, accepted <= maxQueuedSteers, true)
	eq(t, accepted >= maxQueuedSteers-1, true)
}

func benchProvider(b *testing.B, records, tombs, types int, line string) {
	src, ts := bigRoster(records, tombs, types)
	cur := newBuiltIn()
	cur.result = func() (*sdk.AutocompleteSuggestions, error) { return nil, nil } // the host's own list is not what is measured
	p := newMentionProvider(cur.p, func() []mentionTarget { return mentionRoster(src, ts, nil) }, func() bool { return true })
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = p.GetSuggestions(sdk.Context{}, []string{line}, 0, len(line), false)
	}
}

// A typical session: a few agents and types.
func BenchmarkMentionSuggestionsTypical(b *testing.B) { benchProvider(b, 8, 4, 6, "@ex") }

// The worst a session can hold: every record kept for ten minutes, the tombstone bound, many agent files.
func BenchmarkMentionSuggestionsLarge(b *testing.B) { benchProvider(b, 500, maxTombstones, 50, "@e") }

// Ordinary typing, no `@`: the trigger test alone.
func BenchmarkMentionSuggestionsNoTrigger(b *testing.B) {
	benchProvider(b, 500, maxTombstones, 50, "please look at the failing build in the CI pipeline")
}

func BenchmarkParseMention(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, _ = parseMention("@explore find every retry marker in the repository and report")
	}
}

// The hook runs on every prompt the user submits. Text that is not a mention must cost no more than the parse.
func benchInput(b *testing.B, text string) {
	a := newApp(nil, nil)
	data := map[string]any{"text": text, "source": "interactive"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = a.handleInput(sdk.Context{}, data)
	}
}

func BenchmarkInputHookPlainText(b *testing.B) {
	benchInput(b, "please look at the failing build in the CI pipeline and tell me what changed")
}
func BenchmarkInputHookFilePath(b *testing.B) { benchInput(b, "@src/index.ts summarize this") }
