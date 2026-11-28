package files

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/gate"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/hashline"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

// upstream: packages/coding-agent/test/write-line-endings.test.ts: "writes CRLF when replacing a CRLF file with LF content"
func TestWriteLineEndingsAndBOM(t *testing.T) {
	dir := t.TempDir()
	store := hashline.NewStore()

	// CRLF preserved + notice.
	batPath := filepath.Join(dir, "build.bat")
	if err := os.WriteFile(batPath, []byte("@echo off\r\necho old\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := executeWrite(sdk.Context{}, store, dir, "build.bat", new("@echo off\necho new\n"))
	if err != nil {
		t.Fatalf("write build.bat: %v", err)
	}
	got, _ := os.ReadFile(batPath)
	if string(got) != "@echo off\r\necho new\r\n" {
		t.Fatalf("CRLF not preserved: %q", string(got))
	}
	if !strings.Contains(res.Content, "Successfully wrote 21 bytes to build.bat\nNote: kept the replaced file's CRLF line endings.") {
		t.Fatalf("missing CRLF note:\n%s", res.Content)
	}

	// upstream: packages/coding-agent/test/write-line-endings.test.ts: "keeps both CRLF and the BOM, and the returned tag edits the file"
	csPath := filepath.Join(dir, "Program.cs")
	if err := os.WriteFile(csPath, []byte("\uFEFFclass A {}\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res2, err := executeWrite(sdk.Context{}, store, dir, "Program.cs", new("class A {}\nclass B {}\n"))
	if err != nil {
		t.Fatalf("write Program.cs: %v", err)
	}
	got2, _ := os.ReadFile(csPath)
	if string(got2) != "\uFEFFclass A {}\r\nclass B {}\r\n" {
		t.Fatalf("CRLF+BOM not preserved: %q", string(got2))
	}
	if !strings.Contains(res2.Content, "Note: kept the replaced file's CRLF line endings and UTF-8 BOM.") {
		t.Fatalf("missing CRLF+BOM note:\n%s", res2.Content)
	}

	// upstream: packages/coding-agent/test/write-line-endings.test.ts: "writes content that already has carriage returns as given"
	mixedPath := filepath.Join(dir, "crlf.txt")
	if err := os.WriteFile(mixedPath, []byte("a\r\nb\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res3, err := executeWrite(sdk.Context{}, store, dir, "crlf.txt", new("x\ny\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	got3, _ := os.ReadFile(mixedPath)
	if string(got3) != "x\ny\r\n" || strings.Contains(res3.Content, "Note: kept") {
		t.Fatalf("expected verbatim content when input has CR: %q / %s", string(got3), res3.Content)
	}
}

// upstream: packages/coding-agent/test/write-shebang-chmod.test.ts: "marks files starting with #! as executable and flags the result"
func TestWriteShebangChmod(t *testing.T) {
	dir := t.TempDir()
	store := hashline.NewStore()

	// Plain file stays non-executable.
	// upstream: packages/coding-agent/test/write-shebang-chmod.test.ts: "does not chmod files without a shebang"
	plainRes, err := executeWrite(sdk.Context{}, store, dir, "data.txt", new("no shebang here\n"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plainRes.Content, "[Notice: Made executable via chmod +x]") {
		t.Fatalf("unexpected executable notice for plain file")
	}

	// Rust inner attribute #![ must not chmod.
	// upstream: packages/coding-agent/test/write-shebang-chmod.test.ts: "preserves mode for Rust inner attributes without an executable notice"
	rsRes, err := executeWrite(sdk.Context{}, store, dir, "lib.rs", new("#![allow(dead_code)]\nfn main() {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rsRes.Content, "[Notice: Made executable via chmod +x]") {
		t.Fatalf("unexpected executable notice for Rust inner attribute")
	}

	if runtime.GOOS == "windows" {
		return
	}

	shRes, err := executeWrite(sdk.Context{}, store, dir, "run.sh", new("#!/bin/sh\necho hi\n"))
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 != 0o111 {
		t.Fatalf("expected 0111 execute bits, got %o", info.Mode().Perm())
	}
	if !strings.Contains(shRes.Content, "[Notice: Made executable via chmod +x]") {
		t.Fatalf("expected executable notice, got:\n%s", shRes.Content)
	}
	if m, ok := shRes.Details.(map[string]any); !ok || m["madeExecutable"] != true {
		t.Fatalf("expected madeExecutable=true in details, got %#v", shRes.Details)
	}
}

// upstream: packages/coding-agent/test/write-read-projection.test.ts: "rejects a shorter Showing lines notice projection without changing the file"
func TestWriteTruncatedReadProjectionGuard(t *testing.T) {
	dir := t.TempDir()
	store := hashline.NewStore()

	var origLines []string
	for i := 1; i <= 60; i++ {
		origLines = append(origLines, fmt.Sprintf("line %d", i))
	}
	original := strings.Join(origLines, "\n") + "\n"
	p := filepath.Join(dir, "sample.txt")
	if err := os.WriteFile(p, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	notices := []string{
		"[Showing lines 1-20 of 60. Use :21- to continue]",
		"[Showing lines 1-20 of 60 (50.0KB limit). Use :21- to continue]",
		"[40 more lines in notebook. Use :21- to continue]",
		"[More lines in file (120.0KB total; not scanned to EOF). Use :21- to continue]",
		"[...30ln elided; re-read needed ranges, e.g. sample.txt:11-20,31-40]",
	}
	for _, notice := range notices {
		partial := strings.Join(origLines[:20], "\n") + "\n" + notice + "\n"
		_, err := executeWrite(sdk.Context{}, store, dir, "sample.txt", &partial)
		if err == nil || !strings.Contains(err.Error(), "incomplete read projection") {
			t.Fatalf("expected incomplete read projection refusal for %q, got %v", notice, err)
		}
		got, _ := os.ReadFile(p)
		if string(got) != original {
			t.Fatalf("file was modified despite guard for %q", notice)
		}
	}

	// Column-cap notice refuses at any length.
	colPartial := strings.Join(origLines, "\n") + "\n\n[Some lines truncated to 768 chars. Use :raw:1-1 to read line 1 whole]\n"
	_, err := executeWrite(sdk.Context{}, store, dir, "sample.txt", &colPartial)
	if err == nil || !strings.Contains(err.Error(), "omp read column-cap notice") {
		t.Fatalf("expected column-cap refusal, got %v", err)
	}
}

// upstream: packages/coding-agent/test/write-hashline-header.test.ts: "writes numbered data verbatim but still strips copied read rows"
func TestWriteHashlinePrefixStripping(t *testing.T) {
	dir := t.TempDir()
	store := hashline.NewStore()

	copied := "[demo.txt#1234]\n1:alpha\n2:beta\n3:gamma"
	res, err := executeWrite(sdk.Context{}, store, dir, "demo.txt", &copied)
	if err != nil {
		t.Fatalf("write copied hashline: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "demo.txt"))
	if string(got) != "alpha\nbeta\ngamma" {
		t.Fatalf("expected stripped content, got %q", string(got))
	}
	if !strings.Contains(res.Content, "Note: auto-stripped hashline display prefixes from content before writing.") {
		t.Fatalf("missing strip note:\n%s", res.Content)
	}

	// Legitimate numbered content without header or matching snapshot is NOT stripped.
	legit := "1:first item\n2:second item\n"
	res2, err := executeWrite(sdk.Context{}, store, dir, "notes.txt", &legit)
	if err != nil {
		t.Fatal(err)
	}
	got2, _ := os.ReadFile(filepath.Join(dir, "notes.txt"))
	if string(got2) != legit || strings.Contains(res2.Content, "auto-stripped") {
		t.Fatalf("should not strip unevidenced numbered lines: %q", string(got2))
	}
}

// upstream: packages/coding-agent/test/tools/auto-generated-guard.test.ts: "detects canonical TypeScript generated header"
func TestWriteAutoGeneratedGuard(t *testing.T) {
	dir := t.TempDir()
	store := hashline.NewStore()

	// Marker in leading comment blocks overwrite.
	genPath := filepath.Join(dir, "types.go")
	if err := os.WriteFile(genPath, []byte("// Code generated by stringer; DO NOT EDIT.\npackage p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := executeWrite(sdk.Context{}, store, dir, "types.go", new("package p\n"))
	if err == nil || !strings.Contains(err.Error(), "Cannot modify auto-generated file") {
		t.Fatalf("expected auto-generated refusal, got %v", err)
	}

	// Generated filename (*.pb.go) blocks overwrite.
	pbPath := filepath.Join(dir, "api.pb.go")
	if err := os.WriteFile(pbPath, []byte("package api\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = executeWrite(sdk.Context{}, store, dir, "api.pb.go", new("package api\n// edited\n"))
	if err == nil || !strings.Contains(err.Error(), "Cannot modify auto-generated file") {
		t.Fatalf("expected auto-generated filename refusal, got %v", err)
	}
}

// upstream: packages/coding-agent/test/write-read-selector-misfire.test.ts: "fails closed on a missing selector-suffixed target with empty content and points at read()"
func TestWriteReadSelectorMisfireAndURI(t *testing.T) {
	dir := t.TempDir()
	store := hashline.NewStore()

	// Empty write to missing selector-suffixed path refused.
	target := "src/LoraSelector.tsx:1-260:raw"
	_, err := executeWrite(sdk.Context{}, store, dir, target, new(""))
	if err == nil || !strings.Contains(err.Error(), "read-tool selector ':1-260:raw'") {
		t.Fatalf("expected selector misfire refusal, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, target)); !os.IsNotExist(statErr) {
		t.Fatalf("should not have created %s", target)
	}

	// Semicolon-joined selector list refused even with non-empty content.
	// upstream: packages/coding-agent/test/write-read-selector-misfire.test.ts: "rejects a semicolon-joined selector list with non-empty content and creates nothing"
	listTarget := "a.txt:1-2;b/c.txt:3-4"
	_, err = executeWrite(sdk.Context{}, store, dir, listTarget, new("{}"))
	if err == nil || !strings.Contains(err.Error(), "semicolon-joined list of 2 read-tool selectors") {
		t.Fatalf("expected selector list misfire refusal, got %v", err)
	}

	// URI-like near-miss refused.
	// upstream: packages/coding-agent/src/tools/write.ts:91
	_, err = executeWrite(sdk.Context{}, store, dir, "bogus://foo.txt", new("hi"))
	if err == nil || !strings.Contains(err.Error(), "Unknown URI-like write target") {
		t.Fatalf("expected URI-like refusal, got %v", err)
	}

	// Leading '@' preserved on relative scoped paths (@scope/pkg.txt).
	// upstream: packages/coding-agent/src/tools/path-utils.ts:75
	_, err = executeWrite(sdk.Context{}, store, dir, "@scope/pkg.txt", new("ok"))
	if err != nil {
		t.Fatalf("write @scope/pkg.txt: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "@scope", "pkg.txt")); err != nil {
		t.Fatalf("@scope/pkg.txt should keep leading @: %v", err)
	}
}

// upstream: packages/coding-agent/test/read-column-truncation-snapshot.test.ts: "snapshot keeps untruncated content for a full-file read with long lines"
func TestReadDefaultLimitColumnCapAndDetails(t *testing.T) {
	dir := t.TempDir()
	store := hashline.NewStore()

	// 1. Bare read pages at 300 lines (read.defaultLimit).
	var many []string
	for i := 1; i <= 500; i++ {
		many = append(many, fmt.Sprintf("row %d", i))
	}
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(strings.Join(many, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := executeRead(store, dir, "big.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "[Showing lines 1-300 of 500. Use :301- to continue]") {
		t.Fatalf("expected 300-line default limit notice, got tail:\n%s", res.Content[max(0, len(res.Content)-200):])
	}
	if m, ok := res.Details.(map[string]any); !ok || m["totalLines"] != 500 || m["rowPrefixes"] != true || m["displayContent"] == nil {
		t.Fatalf("missing expected read Details: %#v", res.Details)
	} else {
		if _, hasResolved := m["resolvedPath"]; hasResolved {
			t.Fatalf("plain file read must not set resolvedPath, got %#v", m["resolvedPath"])
		}
		topTrunc, _ := m["truncation"].(map[string]any)
		if topTrunc == nil || topTrunc["truncated"] != true || topTrunc["truncatedBy"] != "lines" || topTrunc["outputLines"] != 300 || topTrunc["totalLines"] != 500 {
			t.Fatalf("missing or wrong top-level details.truncation: %#v", m["truncation"])
		}
		meta, _ := m["meta"].(map[string]any)
		trunc, _ := meta["truncation"].(map[string]any)
		if trunc == nil || trunc["truncatedBy"] != "lines" || trunc["outputLines"] != 300 || trunc["totalLines"] != 500 {
			t.Fatalf("missing or wrong details.meta.truncation: %#v", meta)
		}
	}

	// 2. Column truncation at 768 chars keeps untruncated content in snapshot and emits notice.
	longLine := strings.Repeat("x", 1000)
	fullText := "first\n" + longLine + "\nthird\n"
	widePath := filepath.Join(dir, "wide.txt")
	if err := os.WriteFile(widePath, []byte(fullText), 0o644); err != nil {
		t.Fatal(err)
	}
	wRes, err := executeRead(store, dir, "wide.txt")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(wRes.Content, longLine) || !strings.Contains(wRes.Content, "…") {
		t.Fatalf("expected column-truncated display")
	}
	if !strings.Contains(wRes.Content, "[Some lines truncated to 768 chars. Use :raw:2-2 to read line 2 whole]") {
		t.Fatalf("missing column-cap notice:\n%s", wRes.Content)
	}
	snap, ok := store.Head(widePath)
	if !ok || snap.Text != fullText {
		t.Fatalf("snapshot must hold untruncated content, got ok=%v len=%d", ok, len(snap.Text))
	}
	if snap.SeenLines[2] {
		t.Fatalf("column-cut line 2 must not be marked fully seen")
	}

	// 3. Missing file uses omp's `Path '...' not found` and diagnoses :line:col.
	_, err = executeRead(store, dir, "nope.txt")
	if err == nil || err.Error() != "Path 'nope.txt' not found" {
		t.Fatalf("expected \"Path 'nope.txt' not found\", got %v", err)
	}
	_, err = executeRead(store, dir, "wide.txt:1:5")
	if err == nil || !strings.Contains(err.Error(), "Invalid line selector ':1:5'") {
		t.Fatalf("expected :line:col diagnosis, got %v", err)
	}

	// 4. Non-ASCII archive filename (U+023A 'Ⱥ' lowercases to 3-byte 'ⱥ') does not panic.
	nonASCIIZip := filepath.Join(dir, "Ⱥ.zip")
	zf, err := os.Create(nonASCIIZip)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	fw, _ := zw.Create("hi.txt")
	_, _ = fw.Write([]byte("ok\n"))
	_ = zw.Close()
	_ = zf.Close()

	zRes, err := executeRead(store, dir, "Ⱥ.zip:hi.txt:raw")
	if err != nil || zRes.Content != "ok\n" {
		t.Fatalf("non-ASCII zip failed: err=%v content=%q", err, zRes.Content)
	}
}

// Covers gate.Check refusal, located URL identity on write, and multi-session Register/lifecycle.End isolation.
func TestGatePolicyAndMultiSessionRegister(t *testing.T) {
	gate.ResetForTests()
	t.Cleanup(gate.ResetForTests)
	urls.ResetForTests()
	t.Cleanup(urls.ResetForTests)

	dir := t.TempDir()
	store := hashline.NewStore()

	// Register a file-backed URL scheme and verify write preserves URL identity in header and result.
	fh := &fakeFileHandler{scheme: "planlocal", root: dir}
	if err := urls.Register(fh); err != nil {
		t.Fatal(err)
	}
	wres, err := executeWrite(sdk.Context{}, store, dir, "planlocal://plan.md", new("# Plan\n"))
	if err != nil {
		t.Fatalf("write planlocal: %v", err)
	}
	if !strings.HasPrefix(wres.Content, "[planlocal://plan.md#") || !strings.Contains(wres.Content, "Successfully wrote 7 bytes to planlocal://plan.md") {
		t.Fatalf("expected URL identity preserved in write output, got:\n%s", wres.Content)
	}

	// Install a gate policy that refuses writes and verify no file is created or modified.
	var seenReq gate.Request
	gate.SetPolicy(func(ctx sdk.Context, r gate.Request) error {
		seenReq = r
		return fmt.Errorf("Plan mode: the working tree is read-only.")
	})
	_, err = executeWrite(sdk.Context{}, store, dir, "blocked.txt", new("nope"))
	if err == nil || !strings.Contains(err.Error(), "Plan mode: the working tree is read-only.") {
		t.Fatalf("expected gate refusal, got %v", err)
	}
	if seenReq.Tool != "write" || seenReq.Op != gate.OpWrite || seenReq.Path != filepath.Join(dir, "blocked.txt") {
		t.Fatalf("unexpected gate request: %+v", seenReq)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "blocked.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("blocked.txt should not exist when gate refuses")
	}

	// File-backed URL passes both Path and URL to gate.Check.
	_, err = executeWrite(sdk.Context{}, store, dir, "planlocal://blocked.md", new("nope"))
	if err == nil || seenReq.URL != "planlocal://blocked.md" || seenReq.Path != filepath.Join(dir, "blocked.md") {
		t.Fatalf("expected URL+Path in gate request, got err=%v req=%+v", err, seenReq)
	}

	// Two sessions in one process: Register called twice on separate Extension instances;
	// lifecycle.End releases only the ending session's store, is safe to call twice, and leaves
	// the other session's store intact.
	gate.ResetForTests()
	lifecycle.ResetForTests()
	t.Cleanup(lifecycle.ResetForTests)

	ext1 := sdk.New("session-1")
	ext2 := sdk.New("session-2")
	Register(ext1)
	Register(ext2)
	s1 := hashline.StoreForSession("sess-A")
	s2 := hashline.StoreForSession("sess-B")
	if s1 == s2 {
		t.Fatalf("distinct sessions must not share the same store")
	}
	fA := filepath.Join(dir, "a.txt")
	fB := filepath.Join(dir, "b.txt")
	s1.RecordSnapshot(fA, "alpha\n")
	s2.RecordSnapshot(fB, "beta\n")

	// End sess-A twice (must be idempotent) and verify sess-A is released while sess-B stays intact.
	lifecycle.End("sess-A")
	lifecycle.End("sess-A")
	if _, ok := hashline.StoreForSession("sess-A").Head(fA); ok {
		t.Fatalf("expected sess-A store to be released after lifecycle.End")
	}
	if snap, ok := hashline.StoreForSession("sess-B").Head(fB); !ok || snap.Text != "beta\n" {
		t.Fatalf("sess-B store should remain intact after sess-A ends, got ok=%v", ok)
	}
	lifecycle.End("sess-B")

	// Session 1 writes a file; session 2 reads it cleanly.
	if _, err := executeWrite(sdk.Context{}, store, dir, "shared.txt", new("from-s1\n")); err != nil {
		t.Fatal(err)
	}
	rRes, err := executeRead(store, dir, "shared.txt:raw")
	if err != nil || rRes.Content != "from-s1\n" {
		t.Fatalf("session 2 read failed: err=%v content=%q", err, rRes.Content)
	}
}

// upstream: packages/coding-agent/test/tools/read-directory-range.test.ts: "returns a slice of the listing for `:start-end`"
func TestReadDirectoryRangesAndLiteralColon(t *testing.T) {
	dir := t.TempDir()
	store := hashline.NewStore()
	for i := 1; i <= 60; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file-%03d.txt", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Full listing
	fullRes, err := executeRead(store, dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	if m, ok := fullRes.Details.(map[string]any); !ok || m["isDirectory"] != true {
		t.Fatalf("expected isDirectory=true, got %#v", fullRes.Details)
	}
	if !strings.Contains(fullRes.Content, "file-001.txt") || !strings.Contains(fullRes.Content, "file-060.txt") {
		t.Fatalf("full dir listing missing entries")
	}

	// Slice :30-40 prints `:41-` continuation hint
	sliceRes, err := executeRead(store, dir, dir+":30-40")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sliceRes.Content, "more lines in listing") || !strings.Contains(sliceRes.Content, "Use :41- to continue") {
		t.Fatalf("expected :41- continuation hint in directory slice, got:\n%s", sliceRes.Content)
	}

	// Tail :-5 matches last 5 lines of full listing
	fullLines := strings.Split(fullRes.Content, "\n")
	tailRes, err := executeRead(store, dir, dir+":-5")
	if err != nil {
		t.Fatal(err)
	}
	if tailRes.Content != strings.Join(fullLines[len(fullLines)-5:], "\n") {
		t.Fatalf("dir tail mismatch:\ngot:\n%s\nwant:\n%s", tailRes.Content, strings.Join(fullLines[len(fullLines)-5:], "\n"))
	}
	// One-letter relative directory and file with selectors (`d:1-5`, `a:2-3`) must not be
	// mistaken for Windows drive prefixes (`C:\...`, `C:/...`).
	subDir := filepath.Join(dir, "d")
	if err := os.Mkdir(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 10; i++ {
		if err := os.WriteFile(filepath.Join(subDir, fmt.Sprintf("f%02d.txt", i)), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	oneLetterDirRes, err := executeRead(store, dir, "d:1-5")
	if err != nil {
		t.Fatalf("read d:1-5 failed: %v", err)
	}
	if !strings.Contains(oneLetterDirRes.Content, "more lines in listing") {
		t.Fatalf("expected paged listing for d:1-5, got:\n%s", oneLetterDirRes.Content)
	}
	if err := os.WriteFile(filepath.Join(dir, "a"), []byte("line1\nline2\nline3\nline4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oneLetterFileRes, err := executeRead(store, dir, "a:2-3:raw")
	if err != nil {
		t.Fatalf("read a:2-3:raw failed: %v", err)
	}
	if oneLetterFileRes.Content != "line2\nline3\n" {
		t.Fatalf("read a:2-3:raw = %q, want %q", oneLetterFileRes.Content, "line2\nline3\n")
	}

	// upstream: packages/coding-agent/test/tools/path-literal-colon-selector.test.ts: "keeps the raw path intact when a literal colon file exists on disk"
	if runtime.GOOS != "windows" {
		literal := "test:1-2"
		if err := os.WriteFile(filepath.Join(dir, literal), []byte("literal-body\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		litRes, err := executeRead(store, dir, literal)
		if err != nil {
			t.Fatalf("read literal colon file: %v", err)
		}
		if !strings.Contains(litRes.Content, "1:literal-body") {
			t.Fatalf("expected literal colon file content, got:\n%s", litRes.Content)
		}
	}
}
