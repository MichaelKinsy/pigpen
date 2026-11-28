package commit

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

// upstream: packages/coding-agent/test/commit-selective-preview.test.ts

const previewHeader = "diff --git a/f b/f\n--- a/f\n+++ b/f\n"

func previewPatch(before, after string) string {
	return FormatTextPatch("f", &FileState{Mode: "100644", Text: before}, &FileState{Mode: "100644", Text: after})
}

func previewOf(before, after string, columns int) string {
	return FormatPatchPreview(previewPatch(before, after), columns)
}

func fillerWords(name string, count int) string {
	parts := make([]string, count)
	for i := range count {
		parts[i] = fmt.Sprintf("%s%d", name, i)
	}
	return strings.Join(parts, " ")
}

var rowSplitRE = regexp.MustCompile(`(\[-.*?-\]|\{\+.*?\+\}|…)`)

func standsFor(row, before, after string) bool {
	body := row[1:]
	locs := rowSplitRE.FindAllStringIndex(body, -1)
	var parts []string
	cur := 0
	for _, loc := range locs {
		if loc[0] > cur {
			parts = append(parts, body[cur:loc[0]])
		}
		parts = append(parts, body[loc[0]:loc[1]])
		cur = loc[1]
	}
	if cur < len(body) {
		parts = append(parts, body[cur:])
	}

	var from func(index, old, now int) bool
	from = func(index, old, now int) bool {
		if index == len(parts) {
			return old == len(before) && now == len(after)
		}
		part := parts[index]
		if part == "…" {
			for hidden := 1; old+hidden <= len(before) && now+hidden <= len(after) && before[old+hidden-1] == after[now+hidden-1]; hidden++ {
				if from(index+1, old+hidden, now+hidden) {
					return true
				}
			}
			return false
		}
		removed := strings.HasPrefix(part, "[-")
		added := strings.HasPrefix(part, "{+")
		text := part
		if removed || added {
			text = part[2 : len(part)-2]
		}
		if !added && !strings.HasPrefix(before[old:], text) {
			return false
		}
		if !removed && !strings.HasPrefix(after[now:], text) {
			return false
		}
		nextOld := old
		if !added {
			nextOld += len(text)
		}
		nextNow := now
		if !removed {
			nextNow += len(text)
		}
		return from(index+1, nextOld, nextNow)
	}
	return from(0, 0, 0)
}

// upstream: commit-selective-preview.test.ts: "prints a long line edited in place once, with its changes marked"
func TestPreviewLongLineEditedInPlace(t *testing.T) {
	line := func(sources, judge, end string) string {
		return fmt.Sprintf("Search covers %s recent projects, %sand recaps %s when the %s judge resolves %s%s\n",
			fillerWords("lead", 30), sources, fillerWords("mid", 30), judge, fillerWords("tail", 30), end)
	}
	before := "# Notes\n\nlimit = 10\n" + line("past sessions, ", "old", ".") + "retries = 2\n"
	after := "# Notes\n\nlimit = 20\n" + line("", "first usable", ". Recaps are optional.") + "retries = 3\n"

	want := previewHeader + "@@ -1,5 +1,5 @@\n # Notes\n \n-limit = 10\n+limit = 20\n" +
		"!…6 lead27 lead28 lead29 recent projects, [-past sessions, -]and recaps mid0 mid1 mid2 mid3 mid4 mid5… mid25 mid26 mid27 mid28 mid29 when the [-old -]{+first usable +}judge resolves tail0 tail1 tail2 tail3 t…ail24 tail25 tail26 tail27 tail28 tail29{+. Recaps are optional+}.\n" +
		"-retries = 2\n+retries = 3\n"
	got := previewOf(before, after, 768)
	if got != want {
		t.Fatalf("preview mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// upstream: commit-selective-preview.test.ts: "leaves a patch without a long line edited in place as it is"
func TestPreviewLeavesShortOrAddedPatchUnchanged(t *testing.T) {
	wide := fillerWords("word", 80)
	short := func(word string) string { return fillerWords("lead", 12) + " " + word + " " + fillerWords("tail", 12) }
	before := wide + "\nkept();\n" + short("old") + "\n"
	after := wide + "\n" + strings.Replace(wide, "word40", "term40", 1) + "\nkept();\n" + short("new") + "\n"

	if got, want := previewOf(before, after, 768), previewPatch(before, after); got != want {
		t.Fatalf("expected unchanged patch")
	}
}

// upstream: commit-selective-preview.test.ts: "finds a change deep inside a very long line"
func TestPreviewDeepInsideVeryLongLine(t *testing.T) {
	line := func(word string) string {
		return fillerWords("a", 60_000) + " " + word + " " + fillerWords("b", 40_000) + "\n"
	}
	got := strings.Split(previewOf(line("old"), line("new"), 768), "\n")[4]
	want := "!…9994 a59995 a59996 a59997 a59998 a59999 [-old-]{+new+} b0 b1 b2 b3 b4 b5 b6 b7 b8 b9 b10 b11 b…"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

// upstream: commit-selective-preview.test.ts: "keeps every change of a line in its row under any column cap"
func TestPreviewRespectsColumnCaps(t *testing.T) {
	line := func(word string) string {
		parts := make([]string, 8)
		for n := range 8 {
			parts[n] = fmt.Sprintf("%s %s%d", fillerWords(fmt.Sprintf("pad%dx", n), 12), word, n)
		}
		return strings.Join(parts, " ") + " " + fillerWords("end", 12) + "\n"
	}
	row := func(cols int) string {
		return strings.Split(previewOf(line("old"), line("new"), cols), "\n")[4]
	}
	if !strings.Contains(row(0), "pad0x7 pad0x8 pad0x9 pad0x10 pad0x11 [-old0-]{+new0+} pad1x0 pad1x1 pad1x2 pad1x3 pad1x4") {
		t.Fatalf("unexpected row(0): %q", row(0))
	}
	re := regexp.MustCompile(`\[-old\d-\]\{\+new\d\+\}`)
	for _, cols := range []int{768, 256} {
		r := row(cols)
		if len(r) > cols {
			t.Fatalf("row(%d) byte len %d > %d: %q", cols, len(r), cols, r)
		}
		if matches := re.FindAllString(r, -1); len(matches) != 8 {
			t.Fatalf("row(%d) had %d matches, want 8: %q", cols, len(matches), r)
		}
	}
}

// upstream: commit-selective-preview.test.ts: "leaves two lines whose changes cannot fit the column cap as the patch has them"
func TestPreviewChangesExceedColumnCap(t *testing.T) {
	before := fillerWords("lead", 40) + " " + fillerWords("old", 60) + " " + fillerWords("tail", 40) + "\n"
	after := fillerWords("lead", 40) + " " + fillerWords("new", 60) + " " + fillerWords("tail", 40) + "\n"
	if got, want := previewOf(before, after, 256), previewPatch(before, after); got != want {
		t.Fatalf("expected original patch when changes cannot fit column cap")
	}
}

// upstream: commit-selective-preview.test.ts: "leaves two long lines that share little as the patch has them"
func TestPreviewUnrelatedLongLines(t *testing.T) {
	before := fillerWords("alpha", 30) + " shared words here " + fillerWords("gamma", 30) + "\n"
	after := fillerWords("beta", 30) + " shared words here " + fillerWords("delta", 30) + "\n"
	for _, cols := range []int{768, 0} {
		if got, want := previewOf(before, after, cols), previewPatch(before, after); got != want {
			t.Fatalf("cols=%d: expected original patch", cols)
		}
	}
}

// upstream: commit-selective-preview.test.ts: "leaves text out between characters, never inside one"
func TestPreviewSurrogateSafeCut(t *testing.T) {
	faces := strings.Repeat("😀", 200)
	for _, pad := range []string{"", "."} {
		before := faces + pad + " old " + pad + faces + "\n"
		after := strings.Replace(before, "old", "new", 1)
		row := strings.Split(previewOf(before, after, 768), "\n")[4]
		if !strings.Contains(row, " [-old-]{+new+} ") {
			t.Fatalf("pad=%q: expected marked change in %q", pad, row)
		}
		if !utf8.ValidString(row) {
			t.Fatalf("pad=%q: row is not valid UTF-8", pad)
		}
	}
}

// upstream: commit-selective-preview.test.ts: "never shows text of the lines that reads as a mark"
func TestPreviewAmbiguousMarks(t *testing.T) {
	near := fillerWords("lead", 40) + " take items[-1] when oldName is set " + fillerWords("tail", 40) + "\n"
	got := strings.Split(previewOf(near, strings.Replace(near, "oldName", "newName", 1), 768), "\n")[4]
	want := "!…when [-oldName-]{+newName+} is s…"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}

	before := fillerWords("lead", 40) + " last = items[-1] or fallback " + fillerWords("tail", 40) + "\n"
	after := strings.Replace(before, " items[-1] or", "", 1)
	if previewOf(before, after, 768) != previewPatch(before, after) {
		t.Fatalf("expected fallback to original patch when removed text contains mark")
	}
}

// upstream: commit-selective-preview.test.ts: "keeps what the patch says about a missing final newline"
func TestPreviewMissingFinalNewline(t *testing.T) {
	before := fillerWords("lead", 40) + " old " + fillerWords("tail", 40)
	after := strings.Replace(before, "old", "new", 1)
	want := previewHeader + "@@ -1,1 +1,1 @@\n!…ad34 lead35 lead36 lead37 lead38 lead39 [-old-]{+new+} tail0 tail1 tail2 tail3 tail4 tail5 tai…\n\\ No newline at end of file\n"
	if got := previewOf(before, after, 768); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if previewOf(before, after+"\n", 768) != previewPatch(before, after+"\n") {
		t.Fatalf("expected original patch when only one side lacks final newline")
	}
}

// upstream: commit-selective-preview.test.ts: "shows exactly the two lines a row stands for, within the column cap"
func TestPreviewFuzzProperty(t *testing.T) {
	var state uint32 = 0x9e3779b9
	pick := func(count int) int {
		state = state*1664525 + 1013904223
		return int((uint64(state) * uint64(count)) >> 32)
	}
	words := []string{"alpha", "beta", "gamma", "past", "sessions,", "x", "=", ">=", "(", ");", "`judge`", "é", "日本語", "😀", "…"}
	marks := []string{"items[-1]", "[-", "-]", "{+", "+}", "[", "-", "]", "{", "+", "}", "!", "\t", "  "}
	rows := 0
	colsList := [3]int{768, 256, 0}
	for round := range 400 {
		vocab := words
		if round%2 != 0 {
			vocab = append(append([]string{}, words...), marks...)
		}
		nParts := 50 + pick(130)
		parts := make([]string, nParts)
		for i := range nParts {
			parts[i] = vocab[pick(len(vocab))]
		}
		before := strings.Join(parts, " ")
		for edits := 1 + pick(6); edits > 0; edits-- {
			if len(parts) == 0 {
				break
			}
			at := pick(len(parts))
			edit := pick(4)
			switch edit {
			case 0:
				del := min(1+pick(3), len(parts)-at)
				parts = append(parts[:at], parts[at+del:]...)
			case 1:
				parts = append(parts, "")
				copy(parts[at+1:], parts[at:])
				parts[at] = vocab[pick(len(vocab))]
			case 2:
				parts[at] = vocab[pick(len(vocab))]
			default:
				parts[at] += " "
			}
		}
		after := strings.Join(parts, " ")
		cols := colsList[round%3]
		split := strings.Split(previewOf(before+"\n", after+"\n", cols), "\n")
		if len(split) <= 4 || !strings.HasPrefix(split[4], "!") {
			continue
		}
		row := split[4]
		rows++
		if !standsFor(row, before, after) {
			t.Fatalf("round %d: row does not stand for before->after\nrow:    %q\nbefore: %q\nafter:  %q", round, row, before, after)
		}
		if utf16Len(row) > max(utf16Len(before), utf16Len(after)) {
			t.Fatalf("round %d: row longer than input", round)
		}
		if cols > 0 && len(row) > cols {
			t.Fatalf("round %d: row byte len %d > %d", round, len(row), cols)
		}
	}
	if rows <= 100 {
		t.Fatalf("expected > 100 folded rows, got %d", rows)
	}
}

// upstream: commit-selective-preview.test.ts: "stops aligning costly lines once a preview has spent its budget"
func TestPreviewWorkBudget(t *testing.T) {
	shared := strings.Repeat("kept words ", 1000)
	text := func(word string) string { return strings.Repeat(word+" "+shared+word+"\n", 32) }
	out := previewOf(text("old"), text("new"), 768)
	rows := strings.Count(out, "\n!")
	if rows <= 1 || rows >= 32 {
		t.Fatalf("expected 1 < rows < 32, got %d", rows)
	}
}
