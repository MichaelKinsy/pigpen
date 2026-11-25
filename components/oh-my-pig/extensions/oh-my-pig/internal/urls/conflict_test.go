package urls

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// =============================================================================
// Conflict detect unit tests (test/tools/conflict-detect.test.ts)
// =============================================================================

func typedRepl(lines []string) conflictReplacement {
	return conflictReplacement{lines: lines, typedLeading: len(lines), typedTrailing: len(lines)}
}

func makeConflictEntry(id, startLine, sepLine, endLine int, oursLabel, theirsLabel string, ours, theirs []string) ConflictEntry {
	return ConflictEntry{
		ID:           id,
		AbsolutePath: "/abs/a.ts",
		DisplayPath:  "a.ts",
		ConflictBlock: ConflictBlock{
			StartLine:     startLine,
			SeparatorLine: sepLine,
			EndLine:       endLine,
			OursLabel:     oursLabel,
			TheirsLabel:   theirsLabel,
			OursLines:     ours,
			TheirsLines:   theirs,
		},
	}
}

// upstream: test/tools/conflict-detect.test.ts: "scanConflictLines" (9 cases)
func TestConflictScanLines(t *testing.T) {
	// 1. detects a 2-way conflict with correct line numbers and labels
	blocks := ScanConflictLines([]string{
		"line A", "<<<<<<< HEAD", "ours one", "ours two", "=======", "theirs one", ">>>>>>> feature/x", "line Z",
	}, 1)
	if len(blocks) != 1 || blocks[0].StartLine != 2 || blocks[0].SeparatorLine != 5 || blocks[0].EndLine != 7 ||
		blocks[0].BaseLine != 0 || blocks[0].OursLabel != "HEAD" || blocks[0].TheirsLabel != "feature/x" ||
		!reflect.DeepEqual(blocks[0].OursLines, []string{"ours one", "ours two"}) ||
		!reflect.DeepEqual(blocks[0].TheirsLines, []string{"theirs one"}) {
		t.Fatalf("2-way scan failed: %+v", blocks)
	}

	// 2. detects a 3-way diff3 conflict with base section
	blocks = ScanConflictLines([]string{
		"<<<<<<< HEAD", "ours", "||||||| merged common ancestor", "base", "=======", "theirs", ">>>>>>> branch",
	}, 1)
	if len(blocks) != 1 || blocks[0].BaseLine != 3 || blocks[0].BaseLabel != "merged common ancestor" ||
		!reflect.DeepEqual(blocks[0].BaseLines, []string{"base"}) {
		t.Fatalf("3-way scan failed: %+v", blocks)
	}

	// 3. offsets line numbers by firstLineNumber
	blocks = ScanConflictLines([]string{"<<<<<<<", "o", "=======", "t", ">>>>>>>"}, 100)
	if len(blocks) != 1 || blocks[0].StartLine != 100 || blocks[0].SeparatorLine != 102 || blocks[0].EndLine != 104 {
		t.Fatalf("offset scan failed: %+v", blocks)
	}

	// 4. returns multiple blocks in file order
	blocks = ScanConflictLines([]string{
		"<<<<<<< A", "o1", "=======", "t1", ">>>>>>> A", "middle", "<<<<<<< B", "o2", "=======", "t2", ">>>>>>> B",
	}, 1)
	if len(blocks) != 2 || blocks[0].OursLabel != "A" || blocks[1].OursLabel != "B" {
		t.Fatalf("multi-block scan failed: %+v", blocks)
	}

	// 5. ignores unclosed openers
	if got := ScanConflictLines([]string{"<<<<<<< HEAD", "ours", "=======", "theirs"}, 1); len(got) != 0 {
		t.Fatalf("unclosed opener should yield 0, got %+v", got)
	}

	// 6. ignores mis-shaped or indented marker lookalikes
	if got := ScanConflictLines([]string{
		" <<<<<<< HEAD", " =======", " >>>>>>> branch", "<<<<<<<x", "========", ">>>>>>>x", "const a = 1;",
	}, 1); len(got) != 0 {
		t.Fatalf("lookalikes should yield 0, got %+v", got)
	}

	// 7. accepts label-less markers
	blocks = ScanConflictLines([]string{"<<<<<<<", "ours", "=======", "theirs", ">>>>>>>"}, 1)
	if len(blocks) != 1 || blocks[0].OursLabel != "" || blocks[0].TheirsLabel != "" {
		t.Fatalf("label-less scan failed: %+v", blocks)
	}

	// 8. treats a re-opened `<<<<<<<` as a fresh block
	blocks = ScanConflictLines([]string{
		"<<<<<<< first", "stale ours", "<<<<<<< second", "good ours", "=======", "good theirs", ">>>>>>> end",
	}, 1)
	if len(blocks) != 1 || blocks[0].OursLabel != "second" || !reflect.DeepEqual(blocks[0].OursLines, []string{"good ours"}) {
		t.Fatalf("re-opened scan failed: %+v", blocks)
	}

	// 9. detects conflicts in CRLF files and stores LF-normalized sections
	blocks = ScanConflictLines([]string{"<<<<<<< HEAD\r", "ours\r", "=======\r", "theirs\r", ">>>>>>> feat\r"}, 1)
	if len(blocks) != 1 || blocks[0].OursLabel != "HEAD" || blocks[0].TheirsLabel != "feat" ||
		!reflect.DeepEqual(blocks[0].OursLines, []string{"ours"}) || !reflect.DeepEqual(blocks[0].TheirsLines, []string{"theirs"}) {
		t.Fatalf("CRLF scan failed: %+v", blocks)
	}
}

// upstream: test/tools/conflict-detect.test.ts: "ConflictHistory" (3 cases)
func TestConflictHistoryUnits(t *testing.T) {
	h := NewConflictHistory()
	e1 := h.Register("/abs/a.ts", "a.ts", ConflictBlock{StartLine: 10, SeparatorLine: 12, EndLine: 14, OursLines: []string{"o"}, TheirsLines: []string{"t"}})
	e2 := h.Register("/abs/b.ts", "b.ts", ConflictBlock{StartLine: 1, SeparatorLine: 3, EndLine: 5, OursLines: []string{"o2"}, TheirsLines: []string{"t2"}})
	if e1.ID != 1 || e2.ID != 2 {
		t.Fatalf("ids = %d, %d", e1.ID, e2.ID)
	}
	if got, ok := h.Get(1); !ok || got.AbsolutePath != "/abs/a.ts" {
		t.Fatalf("Get(1) = %+v, %v", got, ok)
	}
	if _, ok := h.Get(99); ok {
		t.Fatalf("Get(99) should be false")
	}

	// Re-registering identical block at same position reuses ID.
	e1Again := h.Register("/abs/a.ts", "a.ts", ConflictBlock{StartLine: 10, SeparatorLine: 12, EndLine: 14, OursLines: []string{"o"}, TheirsLines: []string{"t"}})
	if e1Again.ID != 1 {
		t.Fatalf("expected reused ID 1, got %d", e1Again.ID)
	}
	// Changed block at same position gets a fresh ID (never inherits stale ID).
	e1Changed := h.Register("/abs/a.ts", "a.ts", ConflictBlock{StartLine: 10, SeparatorLine: 12, EndLine: 14, OursLines: []string{"new-o"}, TheirsLines: []string{"t"}})
	if e1Changed.ID == 1 {
		t.Fatalf("changed block must not reuse ID 1")
	}

	h.InvalidatePath("/abs/a.ts")
	if _, ok := h.Get(e1Changed.ID); ok {
		t.Fatalf("InvalidatePath failed")
	}
	if _, ok := h.Get(2); !ok {
		t.Fatalf("InvalidatePath dropped unrelated file")
	}
}

// upstream: test/tools/conflict-detect.test.ts: "parseConflictUri" (8 cases) & "recoverConflictUriPrefix" (2 cases)
func TestConflictParseURIAndRecoverPrefix(t *testing.T) {
	if p, err := parseConflictURI("conflict://1/ours"); err != nil || p.id != 1 || p.scope != "ours" {
		t.Fatalf("conflict://1/ours = %+v %v", p, err)
	}
	if p, err := parseConflictURI("conflict://2/theirs"); err != nil || p.id != 2 || p.scope != "theirs" {
		t.Fatalf("conflict://2/theirs = %+v %v", p, err)
	}
	if p, err := parseConflictURI("conflict://3/base"); err != nil || p.id != 3 || p.scope != "base" {
		t.Fatalf("conflict://3/base = %+v %v", p, err)
	}
	if p, err := parseConflictURI("conflict://*"); err != nil || !p.wildcard {
		t.Fatalf("conflict://* = %+v %v", p, err)
	}
	for _, badScope := range []string{"conflict://1/both", "conflict://1/extras"} {
		if _, err := parseConflictURI(badScope); err == nil || !strings.Contains(err.Error(), "scope must be one of") {
			t.Errorf("parseConflictURI(%q) = %v, want scope error", badScope, err)
		}
	}
	if _, err := parseConflictURI("conflict://*/ours"); err == nil || !strings.Contains(err.Error(), "wildcard") {
		t.Errorf("expected wildcard error, got %v", err)
	}
	for _, bad := range []string{"conflict://0", "conflict://-1", "conflict://1.5", "conflict://abc", "conflict://1/extra", "src/foo.ts", "conflict://", "src/foo.ts:conflict://3"} {
		if _, err := parseConflictURI(bad); err == nil {
			t.Errorf("parseConflictURI(%q) expected error", bad)
		}
	}

	// RecoverConflictURIPrefix
	p, note := RecoverConflictURIPrefix("src/foo.ts:conflict://3")
	wantNote := "Note: stripped erroneous 'src/foo.ts:' prefix from path; conflict URIs are global (use `conflict://3`, not `<file>:conflict://3`)."
	if p != "conflict://3" || note != wantNote {
		t.Fatalf("RecoverConflictURIPrefix = (%q, %q)", p, note)
	}
	if p, _ := RecoverConflictURIPrefix("a.ts:conflict://2/theirs"); p != "conflict://2/theirs" {
		t.Fatalf("got %q", p)
	}
	if p, note := RecoverConflictURIPrefix("conflict://1"); p != "conflict://1" || note != "" {
		t.Fatalf("clean URI modified: (%q, %q)", p, note)
	}
}

// upstream: test/tools/conflict-detect.test.ts: "spliceConflict" (10 cases) & "spliceConflict boundary-echo repair" (7 cases)
func TestConflictSpliceAndBoundaryEcho(t *testing.T) {
	file := strings.Join([]string{"before", "<<<<<<< HEAD", "ours", "=======", "theirs", ">>>>>>> feat", "after", ""}, "\n")
	entry := makeConflictEntry(1, 2, 4, 6, "HEAD", "feat", []string{"ours"}, []string{"theirs"})

	// 1. replaces the marker region
	if got, _, _, err := spliceConflict(file, entry, typedRepl([]string{"resolved"})); err != nil || got != "before\nresolved\nafter\n" {
		t.Fatalf("single replace = %q %v", got, err)
	}
	// 2. multi-line replacement
	if got, _, _, err := spliceConflict(file, entry, typedRepl([]string{"alpha", "beta"})); err != nil || got != "before\nalpha\nbeta\nafter\n" {
		t.Fatalf("multi-line = %q %v", got, err)
	}
	// 3. single empty replacement line
	if got, _, _, err := spliceConflict(file, entry, typedRepl([]string{""})); err != nil || got != "before\n\nafter\n" {
		t.Fatalf("empty line = %q %v", got, err)
	}
	// 4. zero replacement lines removes region
	if got, _, _, err := spliceConflict(file, entry, typedRepl(nil)); err != nil || got != "before\nafter\n" {
		t.Fatalf("zero lines = %q %v", got, err)
	}
	// 5. relocates when earlier lines added
	shifted := "// new 1\n// new 2\n" + file
	if got, _, _, err := spliceConflict(shifted, entry, typedRepl([]string{"resolved"})); err != nil || got != "// new 1\n// new 2\nbefore\nresolved\nafter\n" {
		t.Fatalf("shifted = %q %v", got, err)
	}
	// 6 & 7. stale or short file rejected
	if _, _, _, err := spliceConflict("before\n// hand\nafter\n", entry, typedRepl([]string{"x"})); err == nil || !strings.Contains(err.Error(), "no longer present") {
		t.Fatalf("stale error = %v", err)
	}
	if _, _, _, err := spliceConflict("short\n", entry, typedRepl([]string{"x"})); err == nil || !strings.Contains(err.Error(), "no longer present") {
		t.Fatalf("short error = %v", err)
	}
	// 8, 9, 10. CRLF round-trip, no-EOF, zero-line no-EOF
	crlfFile := strings.Join([]string{"before", "<<<<<<< HEAD", "ours", "=======", "theirs", ">>>>>>> feat", "after", ""}, "\r\n")
	if got, _, _, err := spliceConflict(crlfFile, entry, typedRepl([]string{"alpha", "beta"})); err != nil || got != "before\r\nalpha\r\nbeta\r\nafter\r\n" {
		t.Fatalf("crlf = %q %v", got, err)
	}
	crlfNoEOF := strings.Join([]string{"before", "<<<<<<< HEAD", "ours", "=======", "theirs", ">>>>>>> feat"}, "\r\n")
	if got, _, _, err := spliceConflict(crlfNoEOF, entry, typedRepl([]string{"resolved"})); err != nil || got != "before\r\nresolved" {
		t.Fatalf("crlf no-eof = %q %v", got, err)
	}
	if got, _, _, err := spliceConflict(crlfNoEOF, entry, typedRepl(nil)); err != nil || got != "before" {
		t.Fatalf("crlf zero no-eof = %q %v", got, err)
	}

	// Boundary-echo repair:
	// a. drops multi-line trailing echo
	fnLines := []string{
		"const queue = [];", "<<<<<<< HEAD",
		"export function scheduleTask(task, priority = 0) {", "\tif (dupe(task)) {", "\t\treturn;", "\t}",
		"=======",
		"export function enqueueTask(task) {", "\tif (queued.has(task.id)) {", "\t\treturn;", "\t}",
		">>>>>>> feature", "\tqueue.push(task);", "}", "",
	}
	fnEntry := makeConflictEntry(1, 2, 7, 12, "HEAD", "feature", fnLines[2:6], fnLines[7:11])
	got, lead, trail, err := spliceConflict(strings.Join(fnLines, "\n"), fnEntry, typedRepl([]string{
		"export function scheduleTask(task, priority = 0) {", "\tif (queued.has(task.id)) {", "\t\treturn;", "\t}", "\tqueue.push(task);", "}",
	}))
	if err != nil || lead != 0 || trail != 2 || strings.Count(got, "queue.push(task);") != 1 {
		t.Fatalf("multi-line trailing echo failed: lead=%d trail=%d err=%v got=%q", lead, trail, err, got)
	}

	// b. drops single-line echo when it fixes delimiter balance (and CRLF variant)
	bodyLines := []string{
		"function nextDelay(a) {", "<<<<<<< HEAD", "\tconst delay = BASE * 2 ** a;", "\treturn Math.min(delay, 10_000);",
		"=======", "\tconst d = B * 2 ** a;", "\treturn Math.min(d, 30_000);", ">>>>>>> tune", "}", "",
	}
	bodyEntry := makeConflictEntry(1, 2, 5, 8, "HEAD", "tune", bodyLines[2:4], bodyLines[5:7])
	_, _, trail, err = spliceConflict(strings.Join(bodyLines, "\n"), bodyEntry, typedRepl([]string{"\tconst delay = BASE * 2 ** a;", "\treturn Math.min(delay, 30_000);", "}"}))
	if err != nil || trail != 1 {
		t.Fatalf("single-line delimiter balance echo failed: trail=%d err=%v", trail, err)
	}
	gotCRLF, _, trail, err := spliceConflict(strings.Join(bodyLines, "\r\n"), bodyEntry, typedRepl([]string{"\tconst delay = BASE * 2 ** a;", "\treturn Math.min(delay, 30_000);", "}"}))
	if err != nil || trail != 1 || !strings.HasSuffix(gotCRLF, "}\r\n") {
		t.Fatalf("CRLF single-line echo failed: trail=%d err=%v got=%q", trail, err, gotCRLF)
	}

	// c. keeps single-line echo when delimiter balance already consistent
	balFile := "start\n<<<<<<< HEAD\na\n=======\nb\n>>>>>>> x\ndone();\n"
	balEntry := makeConflictEntry(1, 2, 4, 6, "HEAD", "x", []string{"a"}, []string{"b"})
	got, _, trail, err = spliceConflict(balFile, balEntry, typedRepl([]string{"merged", "done();"}))
	if err != nil || trail != 0 || got != "start\nmerged\ndone();\ndone();\n" {
		t.Fatalf("keep consistent single echo failed: trail=%d got=%q", trail, got)
	}

	// d. drops multi-line leading echo
	leadFile := "// header\nconst queue = [];\n<<<<<<< HEAD\na\n=======\nb\n>>>>>>> x\ntail\n"
	leadEntry := makeConflictEntry(1, 3, 5, 7, "HEAD", "x", []string{"a"}, []string{"b"})
	got, lead, _, err = spliceConflict(leadFile, leadEntry, typedRepl([]string{"// header", "const queue = [];", "merged"}))
	if err != nil || lead != 2 || got != "// header\nconst queue = [];\nmerged\ntail\n" {
		t.Fatalf("leading echo failed: lead=%d got=%q", lead, got)
	}

	// e & f. keeps token-expanded lines echoing context, but drops typed lines around a token
	tokFile := "a();\nb();\n<<<<<<< HEAD\na();\nb();\nx\nc();\nd();\n=======\ny\n>>>>>>> t\nc();\nd();\n"
	tokEntry := makeConflictEntry(1, 3, 9, 11, "HEAD", "t", []string{"a();", "b();", "x", "c();", "d();"}, []string{"y"})
	repl, _ := expandConflictTokens("@ours", tokEntry)
	got, lead, trail, err = spliceConflict(tokFile, tokEntry, repl)
	if err != nil || lead+trail != 0 || got != "a();\nb();\na();\nb();\nx\nc();\nd();\nc();\nd();\n" {
		t.Fatalf("token echo keep failed: lead=%d trail=%d got=%q", lead, trail, got)
	}
	repl2, _ := expandConflictTokens("a();\nb();\n@ours\nc();\nd();", tokEntry)
	got, lead, trail, err = spliceConflict(tokFile, tokEntry, repl2)
	if err != nil || lead != 2 || trail != 2 || got != "a();\nb();\na();\nb();\nx\nc();\nd();\nc();\nd();\n" {
		t.Fatalf("typed around token echo failed: lead=%d trail=%d got=%q", lead, trail, got)
	}
}

// upstream: test/tools/conflict-detect.test.ts: "renderConflictRegion" (7 cases) & "expandContentTokens" (8 cases)
func TestConflictRenderAndExpandTokens(t *testing.T) {
	twoWay := makeConflictEntry(1, 10, 13, 15, "HEAD", "feature/x", []string{"ours-1", "ours-2"}, []string{"theirs-1"})
	threeWay := makeConflictEntry(2, 20, 24, 26, "HEAD", "feat", []string{"o"}, []string{"t"})
	threeWay.BaseLine = 22
	threeWay.BaseLabel = "common ancestor"
	threeWay.BaseLines = []string{"b"}

	lines, start, err := renderConflictRegion(twoWay, "")
	if err != nil || start != 10 || !reflect.DeepEqual(lines, []string{"<<<<<<< HEAD", "ours-1", "ours-2", "=======", "theirs-1", ">>>>>>> feature/x"}) {
		t.Fatalf("full 2-way render = %v %d %v", lines, start, err)
	}
	lines, start, err = renderConflictRegion(threeWay, "")
	if err != nil || start != 20 || !reflect.DeepEqual(lines, []string{"<<<<<<< HEAD", "o", "||||||| common ancestor", "b", "=======", "t", ">>>>>>> feat"}) {
		t.Fatalf("full 3-way render = %v %d %v", lines, start, err)
	}
	noLabels := makeConflictEntry(3, 1, 3, 5, "", "", []string{"o"}, []string{"t"})
	lines, _, _ = renderConflictRegion(noLabels, "")
	if lines[0] != "<<<<<<<" || lines[len(lines)-1] != ">>>>>>>" {
		t.Fatalf("no-label markers = %v", lines)
	}
	lines, start, _ = renderConflictRegion(twoWay, "ours")
	if start != 11 || !reflect.DeepEqual(lines, []string{"ours-1", "ours-2"}) {
		t.Fatalf("ours render = %v %d", lines, start)
	}
	lines, start, _ = renderConflictRegion(twoWay, "theirs")
	if start != 14 || !reflect.DeepEqual(lines, []string{"theirs-1"}) {
		t.Fatalf("theirs render = %v %d", lines, start)
	}
	lines, start, _ = renderConflictRegion(threeWay, "base")
	if start != 23 || !reflect.DeepEqual(lines, []string{"b"}) {
		t.Fatalf("base render = %v %d", lines, start)
	}
	if _, _, err := renderConflictRegion(twoWay, "base"); err == nil || !strings.Contains(err.Error(), "no base section") {
		t.Fatalf("2-way base should fail: %v", err)
	}

	// expandConflictTokens
	e := makeConflictEntry(1, 2, 4, 6, "HEAD", "feat", []string{"o1", "o2"}, []string{"t1"})
	if r, _ := expandConflictTokens("hand-written\nline\n", e); !reflect.DeepEqual(r.lines, []string{"hand-written", "line"}) {
		t.Fatalf("trailing newline: %v", r.lines)
	}
	if r, _ := expandConflictTokens("@ours", e); !reflect.DeepEqual(r.lines, []string{"o1", "o2"}) {
		t.Fatalf("@ours: %v", r.lines)
	}
	if r, _ := expandConflictTokens("@theirs", e); !reflect.DeepEqual(r.lines, []string{"t1"}) {
		t.Fatalf("@theirs: %v", r.lines)
	}
	if r, _ := expandConflictTokens("@both", e); !reflect.DeepEqual(r.lines, []string{"o1", "o2", "t1"}) {
		t.Fatalf("@both: %v", r.lines)
	}
	if r, _ := expandConflictTokens("// keep both\n@ours\n@theirs", e); !reflect.DeepEqual(r.lines, []string{"// keep both", "o1", "o2", "t1"}) {
		t.Fatalf("mixed: %v", r.lines)
	}
	if r, err := expandConflictTokens("@base", threeWay); err != nil || !reflect.DeepEqual(r.lines, []string{"b"}) {
		t.Fatalf("@base 3-way: %v %v", r.lines, err)
	}
	if _, err := expandConflictTokens("@base", e); err == nil {
		t.Fatalf("@base on 2-way should fail")
	}
	if r, _ := expandConflictTokens("const x = '@ours';", e); !reflect.DeepEqual(r.lines, []string{"const x = '@ours';"}) {
		t.Fatalf("inline token must stay literal: %v", r.lines)
	}
	if r, _ := expandConflictTokens("@ours\r\n@theirs\r\n", e); !reflect.DeepEqual(r.lines, []string{"o1", "o2", "t1"}) {
		t.Fatalf("CRLF tokens: %v", r.lines)
	}
	emptyEntry := makeConflictEntry(1, 2, 3, 4, "", "", nil, nil)
	if r, _ := expandConflictTokens("@theirs", emptyEntry); len(r.lines) != 0 {
		t.Fatalf("empty side token should give 0 lines, got %v", r.lines)
	}
	if r, _ := expandConflictTokens("", emptyEntry); !reflect.DeepEqual(r.lines, []string{""}) {
		t.Fatalf("empty typed content should give 1 empty line, got %v", r.lines)
	}
}

// =============================================================================
// Conflict integration & live-registry tests
// (test/tools/conflict-integration.test.ts, test/tools/conflict-live-registry.test.ts)
// =============================================================================

func registerFileConflicts(t *testing.T, c Context, absPath, displayPath string) []ConflictEntry {
	t.Helper()
	data, err := os.ReadFile(absPath)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.TrimPrefix(string(data), "\xef\xbb\xbf")
	blocks := ScanConflictLines(strings.Split(text, "\n"), 1)
	h := SessionConflictHistory(c)
	var out []ConflictEntry
	for _, b := range blocks {
		out = append(out, h.Register(absPath, displayPath, b))
	}
	return out
}

// upstream: test/tools/conflict-integration.test.ts: single-block splice, echo note, two blocks in either order, @both, empty side, @base 2-way, scoped write refused, stale file, hashline strip & snapshot header, capped not-found list
func TestConflictIntegration_SingleAndErrors(t *testing.T) {
	resetAll(t)
	RegisterExtension(nil)

	twoWay := "line 1\n<<<<<<< HEAD\noldApi(x)\n=======\nnewApi(x)\n>>>>>>> feature/x\nline N\n"
	twoBlocks := "<<<<<<< A\na-ours\n=======\na-theirs\n>>>>>>> A\nmiddle\n<<<<<<< B\nb-ours\n=======\nb-theirs\n>>>>>>> B\ntail\n"

	// Single-block splice & hashline prefix strip + snapshot header hook.
	tmp := t.TempDir()
	ctx := Context{Cwd: tmp}
	f1 := filepath.Join(tmp, "hashed.ts")
	_ = os.WriteFile(f1, []byte(twoWay), 0o644)
	registerFileConflicts(t, ctx, f1, "hashed.ts")

	// upstream: test/tools/conflict-integration.test.ts: "strips hashline display prefixes from replacement content when hashline mode is active"
	SetConflictWriteHooks(ConflictWriteHooks{
		OnFileWritten: func(_ Context, _ string, _, _ string) string { return "[hashed.ts#ABCD]" },
	})
	wres, err := Write(ctx, "conflict://1", "[hashed.ts#1a2b]\n42:cleanline\n")
	if err != nil || !strings.Contains(wres.Text, "[hashed.ts#ABCD]") || !strings.Contains(wres.Text, "auto-stripped hashline display prefixes") {
		t.Fatalf("hashline strip + snapshot failed: %+v %v", wres, err)
	}
	got, _ := os.ReadFile(f1)
	if string(got) != "line 1\ncleanline\nline N\n" {
		t.Fatalf("file after strip = %q", string(got))
	}
	SetConflictWriteHooks(ConflictWriteHooks{})
	// Echo trimming note on single write.
	fEcho := filepath.Join(tmp, "echo.ts")
	_ = os.WriteFile(fEcho, []byte("function f() {\n<<<<<<< HEAD\n\tours();\n=======\n\ttheirs();\n>>>>>>> feature/x\n\tdone();\n}\n"), 0o644)
	entries := registerFileConflicts(t, ctx, fEcho, "echo.ts")
	wres, err = Write(ctx, "conflict://2", "\tours();\n\ttheirs();\n\tdone();\n}\n")
	if err != nil || !strings.Contains(wres.Text, "dropped 2 content line(s)") || len(entries) != 1 {
		t.Fatalf("echo note write = (%+v, %v)", wres, err)
	}

	// Two blocks resolved in reverse order (#4 then #3).
	fTwo := filepath.Join(tmp, "two.ts")
	_ = os.WriteFile(fTwo, []byte(twoBlocks), 0o644)
	registerFileConflicts(t, ctx, fTwo, "two.ts")
	if _, err := Write(ctx, "conflict://4", "B-resolved\n"); err != nil {
		t.Fatalf("resolve #4 failed: %v", err)
	}
	if _, err := Write(ctx, "conflict://3", "A-resolved\n"); err != nil {
		t.Fatalf("resolve #3 failed: %v", err)
	}
	got, _ = os.ReadFile(fTwo)
	if string(got) != "A-resolved\nmiddle\nB-resolved\ntail\n" {
		t.Fatalf("two blocks = %q", string(got))
	}

	// @both, empty side, @base on 2-way, scoped write refused, stale file.
	fMisc := filepath.Join(tmp, "misc.ts")
	_ = os.WriteFile(fMisc, []byte(twoWay), 0o644)
	registerFileConflicts(t, ctx, fMisc, "misc.ts")
	if _, err := Write(ctx, "conflict://5/theirs", "x"); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("scoped write should fail: %v", err)
	}
	if _, err := Write(ctx, "conflict://5", "@base"); err == nil || !strings.Contains(err.Error(), "no base section") {
		t.Fatalf("@base on 2-way should fail: %v", err)
	}
	if _, err := Write(ctx, "conflict://5", "@both"); err != nil {
		t.Fatalf("@both failed: %v", err)
	}
	got, _ = os.ReadFile(fMisc)
	if string(got) != "line 1\noldApi(x)\nnewApi(x)\nline N\n" {
		t.Fatalf("@both file = %q", string(got))
	}

	// Empty theirs side -> splices 0 lines.
	fEmpty := filepath.Join(tmp, "empty.ts")
	_ = os.WriteFile(fEmpty, []byte("line 1\n<<<<<<< HEAD\nadded(x)\n=======\n>>>>>>> feature/x\nline N\n"), 0o644)
	registerFileConflicts(t, ctx, fEmpty, "empty.ts")
	if _, err := Write(ctx, "conflict://6", "@theirs"); err != nil {
		t.Fatalf("empty side failed: %v", err)
	}
	got, _ = os.ReadFile(fEmpty)
	if string(got) != "line 1\nline N\n" {
		t.Fatalf("empty side file = %q", string(got))
	}

	// Capped not-found list (+2 more when 10 blocks registered).
	ResetConflictsForTests()
	fTen := filepath.Join(tmp, "ten.ts")
	_ = os.WriteFile(fTen, []byte(strings.Repeat(twoBlocks, 5)), 0o644)
	registerFileConflicts(t, ctx, fTen, "ten.ts")
	if _, err := Resolve(ctx, "conflict://99"); err == nil || !strings.Contains(err.Error(), "Registered: #1, #2, #3, #4, #5, #6, #7, #8 (+2 more) in ten.ts.") {
		t.Fatalf("capped not-found error = %v", err)
	}
}

// upstream: tools/write-content.ts:71-124 — non-sequential numbered rows and plain [tool#x] headers must never be stripped.
func TestConflictHashlineStripN1(t *testing.T) {
	resetAll(t)
	RegisterExtension(nil)

	twoWay := "line 1\n<<<<<<< HEAD\noldApi(x)\n=======\nnewApi(x)\n>>>>>>> feature/x\nline N\n"
	tmp := t.TempDir()
	ctx := Context{Cwd: tmp}

	mustNotChange := []struct {
		name    string
		content string
	}{
		{"ports", "8080: web\n9090: metrics\n"},
		{"times", "12:30 standup\n14:00 review\n"},
		{"non_consecutive_bullets", "- 2024: release\n- 2026: plan\n"},
		{"toml_section_hash", "[tool#x]\nplain = 1\n"},
		{"non_sequential_with_header", "[file.txt#1a2b]\n10:first\n12:third\n"},
	}
	for _, tc := range mustNotChange {
		ResetConflictsForTests()
		fp := filepath.Join(tmp, tc.name+".txt")
		_ = os.WriteFile(fp, []byte(twoWay), 0o644)
		registerFileConflicts(t, ctx, fp, tc.name+".txt")
		wres, err := Write(ctx, "conflict://1", tc.content)
		if err != nil {
			t.Fatalf("%s write failed: %v", tc.name, err)
		}
		if strings.Contains(wres.Text, "auto-stripped") {
			t.Fatalf("%s must not be stripped, got: %s", tc.name, wres.Text)
		}
		gotBytes, _ := os.ReadFile(fp)
		want := "line 1\n" + tc.content + "line N\n"
		if string(gotBytes) != want {
			t.Fatalf("%s corrupted content:\ngot:  %q\nwant: %q", tc.name, string(gotBytes), want)
		}
	}

	// Loose header fallback: malformed `[loose.ts#badtag]` header IS dropped only when remaining rows strip.
	ResetConflictsForTests()
	fLoose := filepath.Join(tmp, "loose.ts")
	_ = os.WriteFile(fLoose, []byte(twoWay), 0o644)
	registerFileConflicts(t, ctx, fLoose, "loose.ts")
	wres, err := Write(ctx, "conflict://1", "[loose.ts#badtag]\n10:first\n11:second\n")
	if err != nil || !strings.Contains(wres.Text, "auto-stripped hashline display prefixes") {
		t.Fatalf("loose header fallback failed: %+v %v", wres, err)
	}
	gotLoose, _ := os.ReadFile(fLoose)
	if string(gotLoose) != "line 1\nfirst\nsecond\nline N\n" {
		t.Fatalf("loose header result = %q", string(gotLoose))
	}

	// Mode gate via StripContent hook: when hook declines stripping (non-hashline display mode),
	// sequential numbered lines stay verbatim.
	ResetConflictsForTests()
	SetConflictWriteHooks(ConflictWriteHooks{
		StripContent: func(_ Context, content string) (string, bool) { return content, false },
	})
	fNoMode := filepath.Join(tmp, "nomode.ts")
	_ = os.WriteFile(fNoMode, []byte(twoWay), 0o644)
	registerFileConflicts(t, ctx, fNoMode, "nomode.ts")
	wres, err = Write(ctx, "conflict://1", "10:first\n11:second\n")
	if err != nil || strings.Contains(wres.Text, "auto-stripped") {
		t.Fatalf("mode-gated write stripped unexpectedly: %+v %v", wres, err)
	}
	gotNoMode, _ := os.ReadFile(fNoMode)
	if string(gotNoMode) != "line 1\n10:first\n11:second\nline N\n" {
		t.Fatalf("mode-gated content = %q", string(gotNoMode))
	}
}

// upstream: test/tools/conflict-integration.test.ts: bulk uniform, per-id subset, malformed/literal directives, BOM preservation (3 cases), relocation
func TestConflictIntegration_BulkDirectivesAndBOM(t *testing.T) {
	resetAll(t)
	RegisterExtension(nil)

	twoBlocks := "<<<<<<< A\na-ours\n=======\na-theirs\n>>>>>>> A\nmiddle\n<<<<<<< B\nb-ours\n=======\nb-theirs\n>>>>>>> B\ntail\n"
	tmp := t.TempDir()
	ctx := Context{Cwd: tmp}

	// Nothing registered -> error.
	if _, err := Write(ctx, "conflict://*", "@ours"); err == nil || !strings.Contains(err.Error(), "nothing to resolve") {
		t.Fatalf("expected nothing to resolve, got %v", err)
	}

	// Six malformed/literal per-id directive cases must fail without mutating the file.
	fDir := filepath.Join(tmp, "directives.ts")
	_ = os.WriteFile(fDir, []byte(twoBlocks), 0o644)
	registerFileConflicts(t, ctx, fDir, "directives.ts")
	badDirectives := []struct {
		content string
		wantErr string
	}{
		{"1: @ours\n7: @theirs", "unknown conflict id(s) #7"},
		{"1: @ours\n2: combined line A\ncombined line B\n", "Malformed `conflict://*` per-id block"},
		{"1: @ours\n2: @mine", "only accepts the tokens @ours/@theirs/@base/@both"},
		{"1: foo\n2: bar", "only accepts the tokens @ours/@theirs/@base/@both"},
		{"1: const x = 1;\n   const y = 2;\n2: bar", "Malformed `conflict://*` per-id block"},
		{"40:#1 = literal\n45:#2 = literal", "Malformed `conflict://*` per-id block"},
	}
	for _, tc := range badDirectives {
		if _, err := Write(ctx, "conflict://*", tc.content); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("Write(conflict://*, %q) = %v, want %q", tc.content, err, tc.wantErr)
		}
		after, _ := os.ReadFile(fDir)
		if string(after) != twoBlocks {
			t.Fatalf("malformed directive mutated file")
		}
	}

	// Directive subset leaves unlisted ID registered.
	wres, err := Write(ctx, "conflict://*", "2: @ours")
	if err != nil || !strings.Contains(wres.Text, "still registered (#1)") {
		t.Fatalf("subset directive = (%+v, %v)", wres, err)
	}
	// Uniform bulk resolves remaining #1.
	wres, err = Write(ctx, "conflict://*", "@theirs")
	if err != nil || !strings.Contains(wres.Text, "Resolved 1 conflict") {
		t.Fatalf("uniform bulk = (%+v, %v)", wres, err)
	}
	after, _ := os.ReadFile(fDir)
	if string(after) != "a-theirs\nmiddle\nb-ours\ntail\n" {
		t.Fatalf("directives final = %q", string(after))
	}

	// BOM preservation on individual first-line conflicts and bulk resolve.
	ResetConflictsForTests()
	fBOM := filepath.Join(tmp, "bom-first.ts")
	_ = os.WriteFile(fBOM, []byte("\xef\xbb\xbf"+twoBlocks), 0o644)
	registerFileConflicts(t, ctx, fBOM, "bom-first.ts")
	if _, err := Write(ctx, "conflict://1", "@theirs"); err != nil {
		t.Fatalf("BOM #1 failed: %v", err)
	}
	got, _ := os.ReadFile(fBOM)
	if string(got) != "\xef\xbb\xbfa-theirs\nmiddle\n<<<<<<< B\nb-ours\n=======\nb-theirs\n>>>>>>> B\ntail\n" {
		t.Fatalf("BOM after #1 = %q", string(got))
	}
	if _, err := Write(ctx, "conflict://2", "@ours"); err != nil {
		t.Fatalf("BOM #2 failed: %v", err)
	}
	got, _ = os.ReadFile(fBOM)
	if string(got) != "\xef\xbb\xbfa-theirs\nmiddle\nb-ours\ntail\n" {
		t.Fatalf("BOM after #2 = %q", string(got))
	}

	// BOM preservation on bulk resolve.
	ResetConflictsForTests()
	fBOMBulk := filepath.Join(tmp, "bom-bulk.ts")
	_ = os.WriteFile(fBOMBulk, []byte("\xef\xbb\xbf"+twoBlocks), 0o644)
	registerFileConflicts(t, ctx, fBOMBulk, "bom-bulk.ts")
	if _, err := Write(ctx, "conflict://*", "@ours"); err != nil {
		t.Fatalf("BOM bulk failed: %v", err)
	}
	got, _ = os.ReadFile(fBOMBulk)
	if string(got) != "\xef\xbb\xbfa-ours\nmiddle\nb-ours\ntail\n" {
		t.Fatalf("BOM bulk = %q", string(got))
	}
}

// upstream: test/tools/conflict-live-registry.test.ts (5 live-reconciliation cases including BOM+CRLF+chunk-spanning)
func TestConflictLiveRegistry(t *testing.T) {
	resetAll(t)
	RegisterExtension(nil)

	conflict := "<<<<<<< HEAD\nours\n=======\ntheirs\n>>>>>>> topic\n"
	tmp := t.TempDir()
	ctx := Context{Cwd: tmp}

	// 1. Vanished markers outside tool retired before bulk directive.
	for _, name := range []string{"a.ts", "b.ts", "c.ts"} {
		fp := filepath.Join(tmp, name)
		_ = os.WriteFile(fp, []byte(conflict), 0o644)
		registerFileConflicts(t, ctx, fp, name)
	}
	_ = os.WriteFile(filepath.Join(tmp, "a.ts"), []byte("ours\n"), 0o644)
	wres, err := Write(ctx, "conflict://*", "2: @theirs")
	if err != nil || !strings.Contains(wres.Text, "Resolved 1 conflict across 1 file") || !strings.Contains(wres.Text, "1 unlisted conflict still registered (#3)") {
		t.Fatalf("vanished markers bulk = (%+v, %v)", wres, err)
	}
	if _, err := Resolve(ctx, "conflict://1"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected #1 retired, got %v", err)
	}

	// 2. Changed-in-place block retires ID instead of rebinding.
	ResetConflictsForTests()
	fChanged := filepath.Join(tmp, "changed.ts")
	_ = os.WriteFile(fChanged, []byte(conflict), 0o644)
	registerFileConflicts(t, ctx, fChanged, "changed.ts")
	_ = os.WriteFile(fChanged, []byte(strings.Replace(conflict, "ours\n", "new-ours\n", 1)), 0o644)
	if _, err := Write(ctx, "conflict://1", "@ours"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("changed block must retire id, got %v", err)
	}

	// 3. Shifted lines keep unchanged block's ID.
	ResetConflictsForTests()
	fShift := filepath.Join(tmp, "shift.ts")
	_ = os.WriteFile(fShift, []byte(conflict), 0o644)
	registerFileConflicts(t, ctx, fShift, "shift.ts")
	_ = os.WriteFile(fShift, []byte("new header\n"+conflict), 0o644)
	if _, err := Write(ctx, "conflict://1", "@theirs"); err != nil {
		t.Fatalf("shifted resolve failed: %v", err)
	}
	got, _ := os.ReadFile(fShift)
	if string(got) != "new header\ntheirs\n" {
		t.Fatalf("shifted file = %q", string(got))
	}

	// 4. Deleted file retires all its IDs.
	ResetConflictsForTests()
	fDel := filepath.Join(tmp, "deleted.ts")
	_ = os.WriteFile(fDel, []byte(conflict), 0o644)
	registerFileConflicts(t, ctx, fDel, "deleted.ts")
	_ = os.Remove(fDel)
	if _, err := Write(ctx, "conflict://*", "@ours"); err == nil || !strings.Contains(err.Error(), "nothing to resolve") {
		t.Fatalf("deleted file bulk = %v", err)
	}

	// 5. BOM-prefixed CRLF diff3 block spanning stream chunks (>64 KiB line) without final newline.
	ResetConflictsForTests()
	fFrame := filepath.Join(tmp, "framing.ts")
	lines := []string{"<<<<<<< HEAD", strings.Repeat("x", 64*1024+13), "||||||| ancestor", "base", "=======", "theirs", ">>>>>>> topic"}
	_ = os.WriteFile(fFrame, []byte("\xef\xbb\xbf"+strings.Join(lines, "\r\n")), 0o644)
	registerFileConflicts(t, ctx, fFrame, "framing.ts")
	_ = os.WriteFile(fFrame, []byte("\xef\xbb\xbfshifted\r\n"+strings.Join(lines, "\r\n")), 0o644)
	h := SessionConflictHistory(ctx)
	if err := reconcileFileOnDisk(h, fFrame); err != nil {
		t.Fatalf("streaming reconcile failed: %v", err)
	}
	entries := h.Entries()
	if len(entries) != 1 || entries[0].ID != 1 || entries[0].StartLine != 2 || entries[0].EndLine != 8 {
		t.Fatalf("framing reconcile entries = %+v, want ID=1 Start=2 End=8", entries)
	}
}
