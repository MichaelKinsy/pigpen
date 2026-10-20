package warden

import (
	"fmt"
	"strings"
	"testing"
)

func runawayConfig() RunawayConfig { return DefaultConfig().Runaway }

// loopParagraphs: the observed runaway, an intent line and the same command block, over and over, with no tool call.
func loopParagraphs(times int) string {
	parts := make([]string, times)
	for i := range parts {
		parts[i] = "Stop. PR green. Merge. Executing:\n\n```bash\ngh pr merge 1234 --merge\n```"
	}
	return strings.Join(parts, "\n\n")
}

func runOn(times int) string {
	return strings.Repeat("All checks passed on the PR — merging and watching the dev deploy to confirm the fix lands.", times)
}

func deltaEvent(kind StreamKind, d string) StreamEvent {
	return StreamEvent{Type: string(kind) + "_delta", Delta: &d}
}

// streamText feeds text to the monitor in token-sized pieces and returns the first verdict.
func streamText(m *RunawayMonitor, kind StreamKind, text string, cfg RunawayConfig) *RunawayVerdict {
	m.Feed(StreamEvent{Type: string(kind) + "_start"})
	units := []rune(text)
	for i := 0; i < len(units); i += 7 {
		end := i + 7
		if end > len(units) {
			end = len(units)
		}
		if due := m.Feed(deltaEvent(kind, string(units[i:end]))); due != "" {
			if v := m.Check(due, cfg); v != nil {
				return v
			}
		}
	}
	return nil
}

// twin: tests/runaway.test.ts:26
func TestRepeatedBlockCountsIdenticalNormalisedParagraphs(t *testing.T) {
	if got := RepeatedBlock("```\nx\n```\n\n```\nx\n```\n\n```\nx\n```", BlockMinChars); got.Count != 0 || got.Block != "" {
		t.Fatalf("code fences alone are too short to count: %+v", got)
	}
	found := RepeatedBlock("Running the  whole suite now.\n\nrunning THE whole suite now.\n\nSomething else entirely here.", BlockMinChars)
	if found.Count != 2 || found.Block != "running the whole suite now." {
		t.Fatalf("case and whitespace are normalised: %+v", found)
	}
	if got := RepeatedBlock(loopParagraphs(6), BlockMinChars).Count; got != 6 {
		t.Fatalf("count %d", got)
	}
}

// twin: tests/runaway.test.ts:34
func TestRepeatedTailCountsBackToBackUnitsInRunOnText(t *testing.T) {
	tail := func(s string) RepeatSignal { return RepeatedTail(s, PhraseChars, BlockMinChars) }
	if tail("short").Count != 0 {
		t.Fatal("needs two windows of text")
	}
	found := tail(runOn(5))
	if found.Count != 5 || !strings.HasPrefix(found.Block, "all checks passed on the pr") {
		t.Fatalf("%+v", found)
	}
	if got := tail(strings.Repeat("a", 50) + strings.Repeat("b", 200) + strings.Repeat("c", 60)).Count; got != 1 {
		t.Fatalf("a phrase that occurs once is not a repeat: %d", got)
	}
	if got := tail(runOn(4) + "All checks passed on the PR — merging and").Count; got != 4 {
		t.Fatalf("a unit cut off mid-stream still counts the complete ones before it: %d", got)
	}
	items := make([]string, 12)
	for i := range items {
		items[i] = fmt.Sprintf("Item %d is documented in the section below, together with its defaults.", i)
	}
	if got := tail(strings.Join(items, " ")).Count; got != 1 {
		t.Fatalf("a shared suffix is not a repeated unit: %d", got)
	}
	if got := tail("Header row here.\n" + strings.Repeat("|---", 40)).Count; got != 1 {
		t.Fatalf("units shorter than a paragraph are ignored: %d", got)
	}
}

// twin: tests/runaway.test.ts:47
func TestFindRepeatsPicksTheStrongerSignal(t *testing.T) {
	if got := FindRepeats(loopParagraphs(5)).Signal; got != "block" {
		t.Fatal(got)
	}
	if got := FindRepeats(runOn(6)).Signal; got != "phrase" {
		t.Fatal(got)
	}
	if got := FindRepeats("One paragraph.\n\nAnother paragraph, quite different from the first one.").Count; got != 1 {
		t.Fatal(got)
	}
}

// twin: tests/runaway.test.ts:53
func TestOrdinaryRepliesWithTablesListsAndRepeatedCodeLinesAreNeverStopped(t *testing.T) {
	rows := []string{"| a | b | c | d | e |", "|---|---|---|---|---|"}
	for i := 0; i < 12; i++ {
		rows = append(rows, fmt.Sprintf("| %d | — | — | — | — |", i))
	}
	table := strings.Join(rows, "\n")
	var code []string
	for i := 0; i < 10; i++ {
		code = append(code, fmt.Sprintf("  expect(result[%d]).toBe(true);\n  expect(result[%d]).toBeDefined();", i, i))
	}
	codeBlock := "```ts\n" + strings.Join(code, "\n") + "\n```"
	var list []string
	for i := 0; i < 8; i++ {
		list = append(list, fmt.Sprintf("- step %d: run the check and record the result", i))
	}
	reply := "Here is the summary.\n\n" + table + "\n\n" + codeBlock + "\n\n" + strings.Join(list, "\n") + "\n\nDone."
	if utf16Len(reply) <= runawayConfig().MinChars {
		t.Fatal("reply too short for the check to run")
	}
	if v := streamText(NewRunawayMonitor(), StreamText, reply, runawayConfig()); v != nil {
		t.Fatalf("stopped an ordinary reply: %+v", v)
	}
	if FindRepeats(reply).Count != 1 {
		t.Fatal("count")
	}
}

// twin: tests/runaway.test.ts:63
func TestTheMonitorStopsALoopingReplyOnce(t *testing.T) {
	m := NewRunawayMonitor()
	m.Feed(StreamEvent{Type: "start"})
	v := streamText(m, StreamText, loopParagraphs(30), runawayConfig())
	if v == nil {
		t.Fatal("the loop is caught")
	}
	if v.Kind != StreamText || v.Signal != "block" || v.Count < 4 || v.Count > 8 {
		t.Fatalf("stopped at %+v", v)
	}
	if v.Chars >= utf16Len(loopParagraphs(8)) {
		t.Fatalf("stopped after %d chars", v.Chars)
	}
	if !m.Stopped() {
		t.Fatal("not stopped")
	}
	if got := m.Feed(deltaEvent(StreamText, strings.Repeat("x", CheckEvery))); got != StreamText {
		t.Fatalf("feed still reports growth: %q", got)
	}
	if m.Check(StreamText, runawayConfig()) != nil {
		t.Fatal("a stopped message yields no second verdict")
	}
	m.Feed(StreamEvent{Type: "start"})
	if m.Stopped() || m.Streamed(StreamText) != "" {
		t.Fatal("a new assistant message starts clean")
	}
}

// twin: tests/runaway.test.ts:81
func TestRunOnRepetitionWithoutParagraphBreaksIsCaughtByTheTrailingPhrase(t *testing.T) {
	v := streamText(NewRunawayMonitor(), StreamText, runOn(40), runawayConfig())
	if v == nil || v.Signal != "phrase" || v.Count < 4 {
		t.Fatalf("%+v", v)
	}
}

// twin: tests/runaway.test.ts:88
func TestThinkingHasItsOwnHigherThreshold(t *testing.T) {
	m := NewRunawayMonitor()
	if streamText(m, StreamThinking, loopParagraphs(8), runawayConfig()) != nil {
		t.Fatal("8 repeats in thinking is code drafting, not a runaway")
	}
	if streamText(m, StreamText, "A single ordinary sentence that says what happens next in the plan.", runawayConfig()) != nil {
		t.Fatal("ordinary text stopped")
	}
	v := streamText(m, StreamThinking, loopParagraphs(12), runawayConfig())
	if v == nil || v.Kind != StreamThinking || v.Count < 10 {
		t.Fatalf("%+v", v)
	}
}

// twin: tests/runaway.test.ts:98
func TestMinCharsAndRepeatsAreHonoured(t *testing.T) {
	strict := runawayConfig()
	strict.Repeats, strict.MinChars = 2, 50
	if streamText(NewRunawayMonitor(), StreamText, loopParagraphs(3), strict) != nil {
		t.Fatal("a message shorter than one check window is never judged")
	}
	if v := streamText(NewRunawayMonitor(), StreamText, loopParagraphs(6), strict); v == nil || v.Count < 2 {
		t.Fatalf("%+v", v)
	}
	lenient := runawayConfig()
	lenient.Repeats = 40
	if streamText(NewRunawayMonitor(), StreamText, loopParagraphs(30), lenient) != nil {
		t.Fatal("lenient")
	}
	tall := runawayConfig()
	tall.MinChars = 100000
	if streamText(NewRunawayMonitor(), StreamText, loopParagraphs(30), tall) != nil {
		t.Fatal("tall")
	}
}

// twin: tests/runaway.test.ts:110
func TestNudgeAndWidgetLineNameTheRepeatAndTheOutcome(t *testing.T) {
	v := RunawayVerdict{Kind: StreamText, Count: 7, Block: "stop. pr green. merge. executing:", Chars: 2816, Signal: "block"}
	recovery := RunawayNudge(v, true)
	for _, want := range []string{"repeated 7 times", "no tool was called", "call the tool with it now"} {
		if !strings.Contains(recovery, want) {
			t.Fatalf("recovery %q lacks %q", recovery, want)
		}
	}
	if halt := RunawayNudge(v, false); !strings.Contains(halt, "not restarted. Wait for the user") {
		t.Fatal(halt)
	}
	tmpl := DefaultConfig().Widget.Runaway
	if got := FormatRunaway(v, true, tmpl); got != "warden · runaway · text · 7× repeated · 2816 chars · block · stopped, recovering" {
		t.Fatal(got)
	}
	if got := FormatRunaway(v, false, tmpl); got != "warden · runaway · text · 7× repeated · 2816 chars · block · stopped" {
		t.Fatal(got)
	}
}

func TestRepeatedTailIgnoresShortUnitsInNonASCIIText(t *testing.T) {
	// The unit "ab é " is five characters: shorter than a paragraph, so it counts once however often it repeats.
	if got := RepeatedTail(strings.Repeat("ab é ", 40), PhraseChars, BlockMinChars); got.Count != 1 {
		t.Fatalf("non-ASCII text takes the UTF-16 path: %+v", got)
	}
	if got := RepeatedTail(strings.Repeat("ab c ", 40), PhraseChars, BlockMinChars); got.Count != 1 {
		t.Fatalf("ASCII text takes the byte path: %+v", got)
	}
	if got := RepeatedTail(strings.Repeat("All checks passed on the PR — merging and watching. ", 6), PhraseChars, BlockMinChars); got.Count < 5 {
		t.Fatalf("a long unit counts: %+v", got)
	}
}
