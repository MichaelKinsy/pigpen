package commit

import (
	"reflect"
	"strings"
	"testing"
)

// upstream: packages/coding-agent/test/commit-selective-attribution.test.ts

type testEdit struct {
	actor  string
	before *string
	after  *string
}

func strPtr(s string) *string { return &s }

type commitForResult struct {
	content  *string
	verdicts []string
	reasons  map[int]ReviewReason
}

func commitFor(t *testing.T, head *string, edits []testEdit, work *string, scope []string, headMoved bool) commitForResult {
	t.Helper()
	recorded := make([]RecordedEdit, len(edits))
	for i, e := range edits {
		recorded[i] = RecordedEdit{
			Actor:  e.actor,
			Before: ToLines(e.before),
			After:  ToLines(e.after),
		}
	}
	headLines := ToLines(head)
	workLines := ToLines(work)
	attr, err := Attribute(headLines, HistorySteps(headLines, recorded, workLines), workLines, headMoved)
	if err != nil {
		t.Fatalf("Attribute failed: %v", err)
	}
	scopeSet := make(map[string]bool, len(scope))
	for _, s := range scope {
		scopeSet[s] = true
	}
	cls := Classify(attr, scopeSet)
	selected := make(map[int]bool)
	verdicts := make([]string, len(attr.Ops))
	for i, op := range attr.Ops {
		if cls.Status[op.ID] == OpOwn {
			selected[op.ID] = true
		}
		verdicts[i] = string(cls.Status[op.ID]) + " " + op.Kind + op.Text
	}
	return commitForResult{
		content:  FromLines(BuildContent(attr, selected)),
		verdicts: verdicts,
		reasons:  cls.Reasons,
	}
}

const (
	testChangelog = "# Changelog\n## [Unreleased]\n### Fixed\n- old entry\n"
	testCode      = "def a():\n    return 1\n\n\ndef b():\n    return 2\n\n\ndef c():\n    return 3\n"
)

func assertStrPtrEq(t *testing.T, got, want *string) {
	t.Helper()
	if got == nil && want == nil {
		return
	}
	if got == nil || want == nil || *got != *want {
		var g, w any
		if got != nil {
			g = *got
		}
		if want != nil {
			w = *want
		}
		t.Fatalf("got %q, want %q", g, w)
	}
}

func assertContains(t *testing.T, slice []string, want string) {
	t.Helper()
	for _, s := range slice {
		if s == want {
			return
		}
	}
	t.Fatalf("slice %v does not contain %q", slice, want)
}

// upstream: commit-selective-attribution.test.ts: "keeps only the agent's changelog bullet when other agents added bullets next to it"
func TestAttributionChangelogBullet(t *testing.T) {
	foreignFirst := strings.Replace(testChangelog, "### Fixed\n", "### Fixed\n- Fixed theme flicker\n", 1)
	own := strings.Replace(foreignFirst, "### Fixed\n", "### Fixed\n- Fixed /commit picking foreign hunks\n", 1)
	work := strings.Replace(own, "- old entry\n", "- old entry\n- Fixed stale MCP list\n", 1)

	res := commitFor(t, strPtr(testChangelog), []testEdit{{actor: "Main", before: strPtr(foreignFirst), after: strPtr(own)}}, strPtr(work), []string{"Main"}, false)
	want := strings.Replace(testChangelog, "### Fixed\n", "### Fixed\n- Fixed /commit picking foreign hunks\n", 1)
	assertStrPtrEq(t, res.content, strPtr(want))
}

// upstream: commit-selective-attribution.test.ts: "keeps an insertion before/after a replacement when the replacement is not selected"
func TestAttributionInsertionAroundReplacement(t *testing.T) {
	cases := [][3]string{
		{"before", "check\nshared\nnew", "check\nshared\nold\nshared"},
		{"after", "new\ncheck\nshared", "old\nshared\ncheck\nshared"},
	}
	for _, tc := range cases {
		t.Run(tc[0], func(t *testing.T) {
			head := "begin\nold\nshared\nend"
			before := "begin\nnew\nend"
			work := "begin\n" + tc[1] + "\nend"
			edits := []testEdit{
				{actor: "Other", before: strPtr(head), after: strPtr(before)},
				{actor: "Main", before: strPtr(before), after: strPtr(work)},
			}
			committed := commitFor(t, strPtr(head), edits, strPtr(work), []string{"Main"}, false).content
			wantCommitted := "begin\n" + tc[2] + "\nend"
			assertStrPtrEq(t, committed, strPtr(wantCommitted))
			assertStrPtrEq(t, commitFor(t, strPtr(head), edits[1:], strPtr(work), []string{"Main"}, false).content, committed)
			assertStrPtrEq(t, commitFor(t, strPtr(head), edits, strPtr(work), []string{"Other"}, false).content, strPtr(before))
			assertStrPtrEq(t, commitFor(t, strPtr(head), edits, strPtr(work), []string{"Main", "Other"}, false).content, strPtr(work))
			assertStrPtrEq(t, commitFor(t, strPtr(head), edits, strPtr(work), nil, false).content, strPtr(head))

			replay := commitFor(t, committed, edits, strPtr(work), []string{"Main"}, false)
			wantReplayVerdicts := []string{"other -old", "other -shared", "other +new"}
			if !reflect.DeepEqual(replay.verdicts, wantReplayVerdicts) {
				t.Fatalf("replay verdicts = %v, want %v", replay.verdicts, wantReplayVerdicts)
			}
			assertStrPtrEq(t, replay.content, committed)

			remaining := commitFor(t, committed, edits, strPtr(work), []string{"Other"}, false)
			wantRemVerdicts := []string{"own -old", "own -shared", "own +new"}
			if !reflect.DeepEqual(remaining.verdicts, wantRemVerdicts) {
				t.Fatalf("remaining verdicts = %v, want %v", remaining.verdicts, wantRemVerdicts)
			}
			assertStrPtrEq(t, remaining.content, strPtr(work))
		})
	}
}

// upstream: commit-selective-attribution.test.ts: "places the rest of an insertion around a line already committed at the boundary"
func TestAttributionBoundaryCommittedLine(t *testing.T) {
	original := "begin\nold\nend"
	before := "begin\nnew\nend"
	work := "begin\nfirst\ncheck\nlast\nnew\nend"
	committed := "begin\ncheck\nold\nend"
	edits := []testEdit{
		{actor: "Earlier", before: nil, after: strPtr(committed)},
		{actor: "Other", before: strPtr(original), after: strPtr(before)},
		{actor: "Main", before: strPtr(before), after: strPtr(work)},
	}
	replay := commitFor(t, strPtr(committed), edits, strPtr(work), []string{"Main"}, false)
	assertStrPtrEq(t, replay.content, strPtr("begin\nfirst\ncheck\nlast\nold\nend"))
	wantVerdicts := []string{"own +first", "own +last", "other -old", "other +new"}
	if !reflect.DeepEqual(replay.verdicts, wantVerdicts) {
		t.Fatalf("verdicts = %v, want %v", replay.verdicts, wantVerdicts)
	}
}

// upstream: commit-selective-attribution.test.ts: "reviews an entire insertion between lines of someone else's replacement"
func TestAttributionPlacementReview(t *testing.T) {
	head := "begin\nold\nshared\nend"
	before := "begin\nfirst\nsecond\nend"
	work := "begin\nfirst\ncheck\nshared\nsecond\nend"
	edits := []testEdit{
		{actor: "Other", before: strPtr(head), after: strPtr(before)},
		{actor: "Main", before: strPtr(before), after: strPtr(work)},
	}
	res := commitFor(t, strPtr(head), edits, strPtr(work), []string{"Main"}, false)
	assertStrPtrEq(t, res.content, strPtr(head))
	assertContains(t, res.verdicts, "review +check")
	assertContains(t, res.verdicts, "review +shared")
	foundPlacement := false
	for _, r := range res.reasons {
		if r.Kind == "placement" && reflect.DeepEqual(r.With, []string{"Other"}) {
			foundPlacement = true
		}
	}
	if !foundPlacement {
		t.Fatalf("expected placement reason with Other, got %v", res.reasons)
	}
	assertStrPtrEq(t, commitFor(t, strPtr(head), edits, strPtr(work), []string{"Other"}, false).content, strPtr(before))
	assertStrPtrEq(t, commitFor(t, strPtr(head), edits, strPtr(work), []string{"Main", "Other"}, false).content, strPtr(work))
}

// upstream: commit-selective-attribution.test.ts: "does not infer authorship from the position of an unrecorded insertion"
func TestAttributionUnrecordedInsertion(t *testing.T) {
	head := "old"
	after := "first\nsecond"
	work := "first\ninserted\nsecond"
	res := commitFor(t, strPtr(head), []testEdit{{actor: "Main", before: strPtr(head), after: strPtr(after)}}, strPtr(work), []string{"Main"}, false)
	assertStrPtrEq(t, res.content, strPtr(after))
}

// upstream: commit-selective-attribution.test.ts: "leaves a foreign edit between the agent's edits out and holds back own lines rewritten later"
func TestAttributionForeignGapAndRewrite(t *testing.T) {
	firstAfter := strings.Replace(testCode, "return 1", "return 10", 1)
	secondBefore := strings.Replace(firstAfter, "return 3", "return 30  # foreign", 1)
	secondAfter := strings.Replace(secondBefore, "def b():\n    return 2", "def b():\n    log()\n    return 2", 1)
	work := strings.Replace(secondAfter, "return 10", "return 10 ", 1)

	res := commitFor(t, strPtr(testCode), []testEdit{
		{actor: "Main", before: strPtr(testCode), after: strPtr(firstAfter)},
		{actor: "Main", before: strPtr(secondBefore), after: strPtr(secondAfter)},
	}, strPtr(work), []string{"Main"}, false)

	assertContains(t, res.verdicts, "review -    return 1")
	assertContains(t, res.verdicts, "review +    return 10 ")
	want := strings.Replace(testCode, "def b():\n    return 2", "def b():\n    log()\n    return 2", 1)
	assertStrPtrEq(t, res.content, strPtr(want))
}

// upstream: commit-selective-attribution.test.ts: "history that predates HEAD"
func TestAttributionHistoryPredatesHead(t *testing.T) {
	created := "package demo\n\nfunc a() error {\n\treturn nil\n}\n\nfunc c() int {\n\treturn 3\n}\n"
	withB := strings.Replace(created, "\nfunc c", "\nfunc b() string {\n\treturn \"b\"\n}\n\nfunc c", 1)

	t.Run("keeps committed lines in place when HEAD equals a recorded snapshot", func(t *testing.T) {
		work := strings.Replace(withB, "\treturn 3\n", "\tlog()\n\treturn 3\n", 1)
		res := commitFor(t, strPtr(withB), []testEdit{
			{actor: "TxnFix", before: nil, after: strPtr(created)},
			{actor: "TxnFix", before: strPtr(created), after: strPtr(withB)},
			{actor: "Main", before: strPtr(withB), after: strPtr(work)},
		}, strPtr(work), []string{"Main"}, false)
		if !reflect.DeepEqual(res.verdicts, []string{"own +\tlog()"}) {
			t.Fatalf("verdicts = %v", res.verdicts)
		}
		assertStrPtrEq(t, res.content, strPtr(work))
	})

	t.Run("keeps a partial commit's lines in place and treats them as committed", func(t *testing.T) {
		mainLine := strings.Replace(created, "package demo\n", "package demo\n\nconst m = 1\n", 1)
		subLines := strings.Replace(mainLine, "\nfunc c", "\nfunc b() string {\n\treturn \"b\"\n}\n\nfunc c", 1)
		work := strings.Replace(subLines, "\treturn nil\n", "\treturn errA\n", 1)
		res := commitFor(t, strPtr(withB), []testEdit{
			{actor: "TxnFix", before: nil, after: strPtr(created)},
			{actor: "Main", before: strPtr(created), after: strPtr(mainLine)},
			{actor: "TxnFix", before: strPtr(mainLine), after: strPtr(subLines)},
			{actor: "Main", before: strPtr(subLines), after: strPtr(work)},
		}, strPtr(work), []string{"Main"}, false)
		for _, v := range res.verdicts {
			if !strings.HasPrefix(v, "own ") {
				t.Fatalf("unexpected non-own verdict: %q (all: %v)", v, res.verdicts)
			}
		}
		assertStrPtrEq(t, res.content, strPtr(work))
	})

	t.Run("does not duplicate a committed block an edit re-added past a repeated line", func(t *testing.T) {
		draft := "\nfn end() {\n\tif fill.is_none() {\n\t\treturn;\n\t}\n\tdone();\n}\n"
		final := strings.Replace(
			strings.Replace(draft, "\tif fill.is_none() {\n", "\tlet Some(fill) = fill else {\n", 1),
			"\t\treturn;\n\t}\n",
			"\t\treturn;\n\t};\n\tif fill.busy() {\n\t\treturn;\n\t}\n",
			1,
		)
		own := func(body string) string { return "const X: u8 = 1;\n" + body }
		work := strings.Replace(own(final), "\tdone();\n", "\tdone();\n\ttrace();\n", 1)
		res := commitFor(t, strPtr(final), []testEdit{
			{actor: "Main", before: strPtr(""), after: strPtr(own(draft))},
			{actor: "Main", before: strPtr(own(draft)), after: strPtr(own(final))},
			{actor: "Main", before: strPtr(own(final)), after: strPtr(work)},
		}, strPtr(work), []string{"Main"}, false)
		wantVerdicts := []string{"own +const X: u8 = 1;", "own +\ttrace();"}
		if !reflect.DeepEqual(res.verdicts, wantVerdicts) {
			t.Fatalf("verdicts = %v, want %v", res.verdicts, wantVerdicts)
		}
		assertStrPtrEq(t, res.content, strPtr(work))
	})
}

// upstream: commit-selective-attribution.test.ts: "puts an edit of another actor's uncommitted line in review instead of committing it half"
func TestAttributionEntangledUncommittedLine(t *testing.T) {
	foreign := strings.Replace(testCode, "return 2", "return 2\n    x = helper()", 1)
	own := strings.Replace(foreign, "helper()", "helper(strict=True)", 1)
	res := commitFor(t, strPtr(testCode), []testEdit{{actor: "Main", before: strPtr(foreign), after: strPtr(own)}}, strPtr(own), []string{"Main"}, false)
	if !reflect.DeepEqual(res.verdicts, []string{"review +    x = helper(strict=True)"}) {
		t.Fatalf("verdicts = %v", res.verdicts)
	}
	assertStrPtrEq(t, res.content, strPtr(testCode))
}

// upstream: commit-selective-attribution.test.ts: "deletes a whole function when the agent removed it" & "deletes the file only when every line's deletion is selected"
func TestAttributionDeletions(t *testing.T) {
	head := "a = 1\n\ndef f():\n    x = 1\n    return x\n\ndef g():\n    return 2\n"
	work := "a = 1\n\ndef g():\n    return 2\n"
	res := commitFor(t, strPtr(head), []testEdit{{actor: "Main", before: strPtr(head), after: strPtr(work)}}, strPtr(work), []string{"Main"}, false)
	assertStrPtrEq(t, res.content, strPtr(work))

	delRes := commitFor(t, strPtr("x\n"), []testEdit{{actor: "Main", before: strPtr("x\n"), after: nil}}, nil, []string{"Main"}, false)
	if delRes.content != nil {
		t.Fatalf("expected nil content for deleted file, got %q", *delRes.content)
	}
}

// upstream: commit-selective-attribution.test.ts: "never preselects a line the net change would then hold twice"
func TestAttributionHeadMovedReorder(t *testing.T) {
	cases := []struct {
		head  string
		steps []string
	}{
		{"x B x D A", []string{"B x x A", "Main: B A x x C", "Other: A x x C D B", "Main: A x C D B x"}},
		{"D B x x A", []string{"A B x x", "Main: B A x x", "Other: B A D x x", "Main: B D A x"}},
	}
	lines := func(text string) string { return strings.Join(strings.Split(text, " "), "\n") }
	tally := func(text *string) map[string]int {
		counts := make(map[string]int)
		for _, l := range ToLines(text) {
			counts[l]++
		}
		return counts
	}
	for _, tc := range cases {
		var edits []testEdit
		before := lines(tc.steps[0])
		for _, step := range tc.steps[1:] {
			actor, after, _ := strings.Cut(step, ": ")
			afterText := lines(after)
			edits = append(edits, testEdit{actor: actor, before: strPtr(before), after: strPtr(afterText)})
			before = afterText
		}
		headText := lines(tc.head)
		headCounts := tally(strPtr(headText))
		workCounts := tally(strPtr(before))
		committed := tally(commitFor(t, strPtr(headText), edits, strPtr(before), []string{"Main"}, true).content)
		for line, count := range committed {
			limit := max(headCounts[line], workCounts[line])
			if count > limit {
				t.Fatalf("HEAD %q: line %q count=%d > max(%d,%d)", tc.head, line, count, headCounts[line], workCounts[line])
			}
		}
	}
}

// upstream: commit-selective-attribution.test.ts: "keeps an unrelated insertion preselected when only a reviewed reorder also adds its text"
func TestAttributionUnrelatedInsertionDuringReorder(t *testing.T) {
	lines := func(text string) string { return strings.Join(strings.Split(text, " "), "\n") }
	snapshots := []string{
		"B x x A q q q q",
		"Main: B A x x C q q q q",
		"Other: A x x C D B q q q q",
		"Main: A x C D B x q q q q",
		"Main: A x C D B x q q C q q",
	}
	var edits []testEdit
	before := lines(snapshots[0])
	for _, step := range snapshots[1:] {
		actor, after, _ := strings.Cut(step, ": ")
		afterText := lines(after)
		edits = append(edits, testEdit{actor: actor, before: strPtr(before), after: strPtr(afterText)})
		before = afterText
	}
	res := commitFor(t, strPtr(lines("x B x D A q q q q")), edits, strPtr(before), []string{"Main"}, true)
	if len(res.verdicts) == 0 || res.verdicts[len(res.verdicts)-1] != "own +C" {
		t.Fatalf("expected last verdict 'own +C', got %v", res.verdicts)
	}
	for _, v := range res.verdicts[:len(res.verdicts)-1] {
		if !strings.HasPrefix(v, "review") {
			t.Fatalf("expected review verdict, got %q (all: %v)", v, res.verdicts)
		}
	}
}

// upstream: commit-selective-attribution.test.ts: "keeps the agent's lines selectable in a file that was only dirty before recording began"
func TestAttributionDirtyBeforeRecording(t *testing.T) {
	head := "\nc\n}"
	before := "\nc\nu"
	work := "\n}\nc\nu"
	res := commitFor(t, strPtr(head), []testEdit{{actor: "Main", before: strPtr(before), after: strPtr(work)}}, strPtr(work), []string{"Main"}, false)
	wantVerdicts := []string{"own +}", "other -}", "other +u"}
	if !reflect.DeepEqual(res.verdicts, wantVerdicts) {
		t.Fatalf("verdicts = %v, want %v", res.verdicts, wantVerdicts)
	}
	assertStrPtrEq(t, res.content, strPtr("\n}\nc\n}"))
}

// upstream: commit-selective-attribution.test.ts: "never preselects a line already committed when its twin shares one hunk"
func TestAttributionCommittedTwinSameHunk(t *testing.T) {
	edits := []testEdit{
		{actor: "Main", before: strPtr("A"), after: strPtr("A\nB")},
		{actor: "Other", before: strPtr("A\nB"), after: strPtr("B\nx")},
	}
	res := commitFor(t, strPtr("B"), edits, strPtr("B\nx"), []string{"Main"}, true)
	assertStrPtrEq(t, res.content, strPtr("B"))
}

// upstream: commit-selective-attribution.test.ts: "subagents"
func TestAttributionSubagents(t *testing.T) {
	mainAfter := strings.Replace(testCode, "return 1", "return 10", 1)
	subAfter := strings.Replace(mainAfter, "return 2", "return 20", 1)
	edits := []testEdit{
		{actor: "Main", before: strPtr(testCode), after: strPtr(mainAfter)},
		{actor: "ParserFix", before: strPtr(mainAfter), after: strPtr(subAfter)},
	}
	assertStrPtrEq(t, commitFor(t, strPtr(testCode), edits, strPtr(subAfter), []string{"ParserFix"}, false).content, strPtr(strings.Replace(testCode, "return 2", "return 20", 1)))
	assertStrPtrEq(t, commitFor(t, strPtr(testCode), edits, strPtr(subAfter), []string{"Main", "ParserFix"}, false).content, strPtr(subAfter))

	rewrite := strings.Replace(mainAfter, "return 10", "return 11", 1)
	chain := []testEdit{
		{actor: "Main", before: strPtr(testCode), after: strPtr(mainAfter)},
		{actor: "ParserFix", before: strPtr(mainAfter), after: strPtr(rewrite)},
	}
	assertStrPtrEq(t, commitFor(t, strPtr(testCode), chain, strPtr(rewrite), []string{"ParserFix"}, false).content, strPtr(testCode))
	assertStrPtrEq(t, commitFor(t, strPtr(testCode), chain, strPtr(rewrite), []string{"Main"}, false).content, strPtr(testCode))
	assertStrPtrEq(t, commitFor(t, strPtr(testCode), chain, strPtr(rewrite), []string{"Main", "ParserFix"}, false).content, strPtr(rewrite))
}
