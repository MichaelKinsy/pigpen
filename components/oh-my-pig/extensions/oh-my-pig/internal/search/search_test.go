package search

import (
	"context"
	"fmt"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/hashline"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

type searchRunner func(ctx context.Context, cwd string, params map[string]any) (any, error)

func runToolText(t *testing.T, fn searchRunner, cwd string, params map[string]any) string {
	t.Helper()
	res, err := fn(context.Background(), cwd, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tr, ok := res.(sdk.ToolResult)
	if !ok {
		t.Fatalf("expected sdk.ToolResult, got %T", res)
	}
	return tr.Content
}

func TestRegister(t *testing.T) {
	ext := sdk.New("test-search")
	Register(ext)
}

func TestFileHashMatchesOmp(t *testing.T) {
	h1 := hashline.FileHash("needle one\nfiller\n")
	h2 := hashline.FileHash("needle one   \t\r\nfiller\n")
	h3 := hashline.FileHash("needle uno\nfiller\n")
	if len(h1) != 4 {
		t.Fatalf("expected 4-hex tag, got %q", h1)
	}
	if h1 != h2 {
		t.Fatalf("expected trailing whitespace invariance: %q != %q", h1, h2)
	}
	if h1 == h3 {
		t.Fatalf("expected different content to produce different tag: %q == %q", h1, h3)
	}
}

// upstream: grep-snapshot-tags.test.ts: "re-mints the tag when a file is rewritten with same-length content between searches"
func TestGrepSnapshotTagsReMinted(t *testing.T) {
	dir := t.TempDir()
	fileA := filepath.Join(dir, "a.txt")
	writeFile(t, fileA, "needle one\nfiller\n")
	writeFile(t, filepath.Join(dir, "b.txt"), "needle two\n")

	tagRe := regexp.MustCompile(`(?im)^#+ a\.txt#([0-9A-F]{4})$`)
	firstText := runToolText(t, runGrep, dir, map[string]any{
		"pattern": "needle",
		"path":    ".",
	})
	m1 := tagRe.FindStringSubmatch(firstText)
	if len(m1) < 2 {
		t.Fatalf("expected snapshot tag for a.txt in:\n%s", firstText)
	}

	writeFile(t, fileA, "needle uno\nfiller\n")
	secondText := runToolText(t, runGrep, dir, map[string]any{
		"pattern": "needle",
		"path":    ".",
	})
	m2 := tagRe.FindStringSubmatch(secondText)
	if len(m2) < 2 {
		t.Fatalf("expected snapshot tag for a.txt in:\n%s", secondText)
	}
	if m1[1] == m2[1] {
		t.Fatalf("expected tag to change after rewrite: both %q", m1[1])
	}
}

// upstream: grep-pagination.test.ts: "pages files 20 at a time and caps an overflowing file's matches"
func TestGrepPagination(t *testing.T) {
	dir := t.TempDir()
	for i := range 25 {
		writeFile(t, filepath.Join(dir, fmt.Sprintf("f%02d.txt", i)), "needle\n")
	}
	var hotLines []string
	for i := range 30 {
		hotLines = append(hotLines, fmt.Sprintf("needle %d", i))
	}
	writeFile(t, filepath.Join(dir, "hot.txt"), strings.Join(hotLines, "\n")+"\n")

	page1 := runToolText(t, runGrep, dir, map[string]any{
		"pattern": "needle",
		"path":    ".",
	})
	if !strings.Contains(page1, "Showing files 1-20 of 26") || !strings.Contains(page1, "Use skip=20") {
		t.Fatalf("expected pagination notice in page 1:\n%s", page1)
	}

	page2 := runToolText(t, runGrep, dir, map[string]any{
		"pattern": "needle",
		"path":    ".",
		"skip":    float64(20),
	})
	if !strings.Contains(page2, "needle 19") {
		t.Fatalf("expected hot.txt match 19 in page 2:\n%s", page2)
	}
	if strings.Contains(page2, "needle 20") {
		t.Fatalf("expected hot.txt to be capped at 20 matches:\n%s", page2)
	}
	if strings.Contains(page2, "skip=") {
		t.Fatalf("did not expect skip= on last page:\n%s", page2)
	}

	pastEnd := runToolText(t, runGrep, dir, map[string]any{
		"pattern": "needle",
		"path":    ".",
		"skip":    float64(50),
	})
	if !strings.Contains(pastEnd, "No more results") {
		t.Fatalf("expected past-the-end message, got:\n%s", pastEnd)
	}
}

// upstream: grep-path-lists.test.ts: multi-path `;` list and `!glob` exclusions
func TestGrepMultiPathAndExclusions(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "apps", "a.ts"), "shared-needle apps\n")
	writeFile(t, filepath.Join(dir, "packages", "p.ts"), "shared-needle packages\n")
	writeFile(t, filepath.Join(dir, "packages", "p.test.ts"), "shared-needle test\n")
	writeFile(t, filepath.Join(dir, "other", "o.ts"), "shared-needle other\n")

	text := runToolText(t, runGrep, dir, map[string]any{
		"pattern": "shared-needle",
		"path":    "apps;packages;!**/*.test.ts",
	})
	if !strings.Contains(text, "shared-needle apps") || !strings.Contains(text, "shared-needle packages") {
		t.Fatalf("expected matches from apps and packages:\n%s", text)
	}
	if strings.Contains(text, "shared-needle test") {
		t.Fatalf("expected *.test.ts to be excluded:\n%s", text)
	}
	if strings.Contains(text, "shared-needle other") {
		t.Fatalf("expected other/ not to be searched:\n%s", text)
	}
}

func TestGrepGitignoreAndHidden(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".gitignore"), "ignored.txt\nbuild/\n")
	writeFile(t, filepath.Join(dir, "kept.txt"), "marker\n")
	writeFile(t, filepath.Join(dir, "ignored.txt"), "marker\n")
	writeFile(t, filepath.Join(dir, "build", "out.txt"), "marker\n")
	writeFile(t, filepath.Join(dir, ".hidden.txt"), "marker\n")

	// Default: gitignore=true, hidden=true
	withIgnore := runToolText(t, runGrep, dir, map[string]any{
		"pattern": "marker",
		"path":    ".",
	})
	if !strings.Contains(withIgnore, "kept.txt") || !strings.Contains(withIgnore, ".hidden.txt") {
		t.Fatalf("expected kept.txt and .hidden.txt:\n%s", withIgnore)
	}
	if strings.Contains(withIgnore, "ignored.txt") || strings.Contains(withIgnore, "out.txt") {
		t.Fatalf("expected gitignored files to be skipped:\n%s", withIgnore)
	}

	// gitignore=false includes ignored files
	noIgnore := runToolText(t, runGrep, dir, map[string]any{
		"pattern":   "marker",
		"path":      ".",
		"gitignore": false,
	})
	if !strings.Contains(noIgnore, "ignored.txt") || !strings.Contains(noIgnore, "out.txt") {
		t.Fatalf("expected gitignored files when gitignore=false:\n%s", noIgnore)
	}
}

func TestGrepCaseFlag(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "HelloWorld\nhelloworld\n")

	cs := runToolText(t, runGrep, dir, map[string]any{
		"pattern": "HelloWorld",
		"path":    ".",
	})
	if strings.Contains(cs, "helloworld") {
		t.Fatalf("expected case-sensitive match by default:\n%s", cs)
	}

	ci := runToolText(t, runGrep, dir, map[string]any{
		"pattern": "HelloWorld",
		"path":    ".",
		"case":    false,
	})
	if !strings.Contains(ci, "HelloWorld") || !strings.Contains(ci, "helloworld") {
		t.Fatalf("expected case-insensitive match when case=false:\n%s", ci)
	}
}

func TestGrepLineRangeSelector(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "lines.txt"), "hit 1\nhit 2\nhit 3\nhit 4\nhit 5\n")

	text := runToolText(t, runGrep, dir, map[string]any{
		"pattern": "hit",
		"path":    "lines.txt:2-4",
	})
	if strings.Contains(text, "hit 1") || strings.Contains(text, "hit 5") {
		t.Fatalf("expected only lines 2-4:\n%s", text)
	}
	if !strings.Contains(text, "hit 2") || !strings.Contains(text, "hit 4") {
		t.Fatalf("expected lines 2-4 to match:\n%s", text)
	}
}

func TestGrepCrossLinePattern(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "multi.txt"), "alpha\nbeta\ngamma\n")

	text := runToolText(t, runGrep, dir, map[string]any{
		"pattern": `alpha\nbeta`,
		"path":    ".",
	})
	if !strings.Contains(text, "alpha") {
		t.Fatalf("expected cross-line match:\n%s", text)
	}
}

func TestGrepUnsupportedPCRE2Regex(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "foobar\n")

	_, err := runGrep(context.Background(), dir, map[string]any{
		"pattern": `foo(?=bar)`,
		"path":    ".",
	})
	if err == nil {
		t.Fatal("expected error for lookahead regex")
	}
	if !strings.Contains(err.Error(), "RE2") && !strings.Contains(err.Error(), "Invalid regex") {
		t.Fatalf("expected clear RE2/Invalid regex error, got: %v", err)
	}
}

// upstream: glob.test.ts: "rejects bare root search path /"
func TestGlobRejectsRootPath(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"/", "//"} {
		_, err := runGlob(context.Background(), dir, map[string]any{"path": p})
		if err == nil || err.Error() != "Searching from root directory '/' is not allowed" {
			t.Fatalf("path %q: expected root rejection, got %v", p, err)
		}
	}
}

func TestGlobGitignoreHiddenLimitAndMtime(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".gitignore"), "ignored.log\n")
	writeFile(t, filepath.Join(dir, "ignored.log"), "x")
	writeFile(t, filepath.Join(dir, ".dotfile"), "x")
	oldFile := filepath.Join(dir, "old.go")
	newFile := filepath.Join(dir, "new.go")
	writeFile(t, oldFile, "package a")
	writeFile(t, newFile, "package b")
	t1 := time.Now().Add(-2 * time.Hour)
	t2 := time.Now().Add(-1 * time.Minute)
	_ = os.Chtimes(oldFile, t1, t1)
	_ = os.Chtimes(newFile, t2, t2)

	// Default: hidden=true, gitignore=true
	text := runToolText(t, runGlob, dir, map[string]any{"path": "."})
	if strings.Contains(text, "ignored.log") {
		t.Fatalf("expected ignored.log to be excluded:\n%s", text)
	}
	if !strings.Contains(text, ".dotfile") {
		t.Fatalf("expected .dotfile to be included by default:\n%s", text)
	}
	if strings.Index(text, "new.go") > strings.Index(text, "old.go") {
		t.Fatalf("expected newest-first ordering (new.go before old.go):\n%s", text)
	}

	// hidden=false
	noHidden := runToolText(t, runGlob, dir, map[string]any{"path": ".", "hidden": false})
	if strings.Contains(noHidden, ".dotfile") {
		t.Fatalf("expected .dotfile to be excluded when hidden=false:\n%s", noHidden)
	}

	// limit=1
	limited := runToolText(t, runGlob, dir, map[string]any{"path": ".", "limit": float64(1)})
	if strings.Contains(limited, "old.go") {
		t.Fatalf("expected limit=1 to cap results:\n%s", limited)
	}

	// limit > 200 clamp notice
	clamped := runToolText(t, runGlob, dir, map[string]any{"path": ".", "limit": float64(500)})
	if !strings.Contains(clamped, "Requested limit 500 clamped to the max of 200") {
		t.Fatalf("expected clamp notice:\n%s", clamped)
	}
}

func TestFindLexical(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "auth", "token.go"), "package auth\nfunc ValidateToken(tok string) bool { return tok != \"\" }\n")
	writeFile(t, filepath.Join(dir, "util", "math.go"), "package util\nfunc Add(a, b int) int { return a + b }\n")

	text := runToolText(t, runFind, dir, map[string]any{
		"query":         "validate authentication token",
		"grep_keywords": []any{"ValidateToken"},
		"path":          ".",
	})
	if !strings.Contains(text, "token.go") {
		t.Fatalf("expected token.go in find results:\n%s", text)
	}
	if strings.Contains(text, "math.go") {
		t.Fatalf("did not expect math.go in find results:\n%s", text)
	}
}

// upstream: glob.test.ts + output-meta.ts: resultLimit footer with doubled suggestion and hard-cap suppression
func TestGlobLimitFooterNotice(t *testing.T) {
	dir := t.TempDir()
	for i := range 10 {
		writeFile(t, filepath.Join(dir, "a", fmt.Sprintf("f%02d.txt", i)), "x\n")
	}

	// limit=8 with 10 files -> doubled suggestion limit=16
	got8 := runToolText(t, runGlob, dir, map[string]any{
		"path":  "a/**/*.txt",
		"limit": float64(8),
	})
	want8 := "\n\n[8 results limit reached. Use limit=16 for more]"
	if !strings.HasSuffix(got8, want8) {
		t.Fatalf("expected suffix %q, got:\n%s", want8, got8)
	}

	// limit=150 -> suggestion capped at MAX_LIMIT (200)
	dirBig := t.TempDir()
	for i := range 205 {
		writeFile(t, filepath.Join(dirBig, fmt.Sprintf("f%03d.txt", i)), "x\n")
	}
	got150 := runToolText(t, runGlob, dirBig, map[string]any{
		"path":  "*.txt",
		"limit": float64(150),
	})
	want150 := "\n\n[150 results limit reached. Use limit=200 for more]"
	if !strings.HasSuffix(got150, want150) {
		t.Fatalf("expected suffix %q, got:\n%s", want150, got150)
	}

	// limit=200 (hard cap) -> bare reached notice without Use limit=
	got200 := runToolText(t, runGlob, dirBig, map[string]any{
		"path":  "*.txt",
		"limit": float64(200),
	})
	want200 := "\n\n[200 results limit reached]"
	if !strings.HasSuffix(got200, want200) {
		t.Fatalf("expected suffix %q, got:\n%s", want200, got200)
	}
}

// upstream: grep.ts:840-842,908-909,1072 + output-meta.ts:343
func TestGrepExactNotices(t *testing.T) {
	dir := t.TempDir()
	for i := range 22 {
		writeFile(t, filepath.Join(dir, fmt.Sprintf("f%02d.txt", i)), "needle\n")
	}

	page1 := runToolText(t, runGrep, dir, map[string]any{"pattern": "needle", "path": "."})
	wantPage1 := "\n\nShowing files 1-20 of 22. Use skip=20 for the next page, or narrow paths/pattern."
	if !strings.HasSuffix(page1, wantPage1) {
		t.Fatalf("expected exact pagination footer %q, got:\n%s", wantPage1, page1)
	}

	pastEnd := runToolText(t, runGrep, dir, map[string]any{"pattern": "needle", "path": ".", "skip": float64(30)})
	wantPastEnd := "No more results (22 files total; skip=30 is past the end)"
	if pastEnd != wantPastEnd {
		t.Fatalf("expected exact past-end %q, got %q", wantPastEnd, pastEnd)
	}

	// Column truncation footer
	dirLong := t.TempDir()
	writeFile(t, filepath.Join(dirLong, "long.txt"), "needle "+strings.Repeat("x", 600)+"\n")
	longOut := runToolText(t, runGrep, dirLong, map[string]any{"pattern": "needle", "path": "long.txt"})
	wantLong := "\n\n[Some lines truncated to 500 bytes]"
	if !strings.HasSuffix(longOut, wantLong) {
		t.Fatalf("expected exact column-truncation footer %q, got:\n%s", wantLong, longOut)
	}
}
