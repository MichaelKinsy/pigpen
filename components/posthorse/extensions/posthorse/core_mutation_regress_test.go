package posthorse

// Regression tests that kill the mutants the first mutation run of lane A left alive
// (port/mutations/CoreLane.json): each pins one boundary or branch.

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestMutationJSUTF8BytesLoneLowThenLow(t *testing.T) {
	low := u16string([]uint16{0xDE00})
	if got := string(jsUTF8Bytes(low + low)); got != "\uFFFD\uFFFD" {
		t.Errorf("two low halves never join: %q", got)
	}
}

func TestMutationAssistantFailureAbortedWithoutMessage(t *testing.T) {
	if got := assistantFailure(rec{"role": "assistant", "stopReason": "aborted"}); got != "[aborted]" {
		t.Errorf("aborted with no error text: %q", got)
	}
}

func TestMutationBoundedBlockCountsUnitsOfTheHeader(t *testing.T) {
	header := "É😀" // 3 UTF-16 units, 6 bytes
	got := boundedBlock(header, strings.Repeat("x", 100), 30)
	if u16len(got) != 30 {
		t.Errorf("boundedBlock length = %d units, want 30", u16len(got))
	}
}

func TestMutationFreshPayloadImagesAreEstimated(t *testing.T) {
	p := budgetPolicy(100_000, 1000, 16_384, true)
	g := guidanceCost(t, p)
	prompt := (u16len(p.Prompt) + 3) / 4
	pending := []rec{{"role": "user", "content": []any{rec{"type": "image", "data": "d", "mimeType": "image/png"}}}}
	got, err := freshPayloadChars(p, 0, pending, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	if want := (83_617 - 1_000 - (prompt + g) - 0 - 1200) / 2 * 4; got != want {
		t.Errorf("freshPayloadChars with an image = %d, want %d", got, want)
	}
}

func TestMutationRefusalTextKeptWhenItFitsExactly(t *testing.T) {
	g := guidanceCost(t, budgetPolicy(100_000, 0, 16_384, true))
	message := "Too little context remains to read a page safely. Call new_context first, then retry with offset 5."
	fit := (u16len(message) + 3) / 4
	p := budgetPolicy(100_000, 83_617-g-100, 16_384, true) // 100 tokens of capacity
	pages := newPageReservations(noToolTokens(t))
	pages.accounting.pendingTokens = 100 - fit // remaining == the refusal's own cost
	_, err := pages.size(p, pageRequest{offset: 5})
	if err == nil || err.Error() != message {
		t.Errorf("a refusal that fits exactly keeps its text: %v", err)
	}
}

func TestMutationNotesEmptyPageAtOffsetZero(t *testing.T) {
	p := budgetPolicy(100_000, 1000, 16_384, true)
	pages := newPageReservations(noToolTokens(t))
	result, err := pages.notes(p, notesPage{kind: "notes-list"})
	if err != nil || result.Content != "" {
		t.Errorf("an empty page at offset 0 is not past the end: %q, %v", result.Content, err)
	}
}

func TestMutationGuidanceTargetsTheLastPosthorseSection(t *testing.T) {
	f := newFixture(t)
	res := f.contextLike("context_with_system", []rec{
		{"role": "system", "content": "", "sections": rec{"posthorse": "old"}, "timestamp": float64(0)},
		{"role": "system", "content": "", "sections": rec{"posthorse": "older"}, "timestamp": float64(0)},
	})
	list, _ := res["messages"].([]any)
	if len(list) != 2 {
		t.Fatalf("messages = %v", res)
	}
	first := list[0].(map[string]any)["sections"].(map[string]any)["posthorse"]
	second := list[1].(map[string]any)["sections"].(map[string]any)["posthorse"].(string)
	if first != "old" || !strings.HasPrefix(second, "<posthorse>\n") {
		t.Errorf("the last system message with a posthorse section is the one rewritten: %v / %q", first, second)
	}
}

func TestMutationAgentSettledForgetsTheForcedPrompt(t *testing.T) {
	f := newFixture(t)
	f.BeforeAgentStart("", rec{"sections": rec{}, "forceSystemPrompt": "Forced"})
	f.Fire("agent_settled", rec{})
	// Idle evaluation after the run: the guidance goes into the sections, not into a spliced message.
	if guidance := f.GuidanceFor(); !strings.Contains(guidance, "Automatic context management") {
		t.Errorf("guidance = %q", guidance)
	}
}

func TestMutationMigrationTargetFirstSuffixIsOne(t *testing.T) {
	moved := t.TempDir()
	original := nowMillis
	t.Cleanup(func() { nowMillis = original })
	nowMillis = func() int64 { return 777 }
	pnMkdir(t, moved, "from-checkout-777")
	got, err := migrationTarget(moved)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(moved, "from-checkout-777-1"); got != want {
		t.Errorf("migrationTarget = %q, want %q", got, want)
	}
}

func TestMutationLstatSyncMissingTreatsNotADirectoryAsMissing(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	pnWrite(t, file, "x")
	info, err := lstatSyncMissing(filepath.Join(file, "child"))
	if info != nil || err != nil {
		t.Errorf("ENOTDIR under a file is no entry for lstatSync({throwIfNoEntry:false}): %v, %v", info, err)
	}
}

func TestMutationLatin1TailKeepsExactlyTheOverlap(t *testing.T) {
	const chunk = 1024 * 1024
	needle := "NEEDLE-123" // 10 characters: 9 end the first read, 1 starts the next
	first := "a"
	line := strings.Repeat("x", chunk-9-len(first)-1) + needle + "yy"
	path := writeSessionFile(t, first, line)
	for _, encoding := range []string{"latin1", "utf8"} {
		got := scan(t, path, containsMatcher(needle, encoding, len(needle)-1))
		if len(got) != 2 || !got[1].matches {
			t.Errorf("%s: a needle split 9|1 needs the whole overlap in the tail", encoding)
		}
	}
}
