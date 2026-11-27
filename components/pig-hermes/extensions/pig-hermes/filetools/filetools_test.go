package filetools

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/config"
)

func TestRegister(t *testing.T) {
	ext := sdk.New("test-filetools")
	Register(ext, config.Default())
}

func TestReadFilePaginationTruncationBinaryAndNotebook(t *testing.T) {
	dir := t.TempDir()
	st := newState(config.Default())

	// 1. Basic read with 1-indexed <n>| formatting and pagination.
	p := filepath.Join(dir, "sample.txt")
	content := "alpha\nbeta\ngamma\ndelta\nepsilon\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	resAny, err := st.executeReadFile(sdk.Context{}, map[string]any{"path": p})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	res := resAny.(sdk.ToolResult)
	if res.IsError {
		t.Fatalf("expected success, got error: %s", res.Content)
	}
	want := "1|alpha\n2|beta\n3|gamma\n4|delta\n5|epsilon"
	if res.Content != want {
		t.Fatalf("got:\n%s\nwant:\n%s", res.Content, want)
	}

	// Offset + limit
	resAny, _ = st.executeReadFile(sdk.Context{}, map[string]any{"path": p, "offset": 2, "limit": 2})
	res = resAny.(sdk.ToolResult)
	if !strings.Contains(res.Content, "2|beta\n3|gamma") || !strings.Contains(res.Content, "use offset=4") {
		t.Fatalf("unexpected paged output: %s", res.Content)
	}

	// Negative offset (tail)
	resAny, _ = st.executeReadFile(sdk.Context{}, map[string]any{"path": p, "offset": -2})
	res = resAny.(sdk.ToolResult)
	if !strings.Contains(res.Content, "4|delta\n5|epsilon") {
		t.Fatalf("unexpected tail output: %s", res.Content)
	}

	// 2. Per-line and char-budget truncation.
	cfgSmall := config.Default()
	cfgSmall.MaxLineLength = 10
	cfgSmall.MaxReadChars = 40
	stSmall := newState(cfgSmall)
	longFile := filepath.Join(dir, "long.txt")
	_ = os.WriteFile(longFile, []byte("0123456789ABCDEFGH\nsecond line here\nthird line here\n"), 0o644)
	resAny, _ = stSmall.executeReadFile(sdk.Context{}, map[string]any{"path": longFile})
	res = resAny.(sdk.ToolResult)
	if !strings.Contains(res.Content, "1|0123456789... [truncated]") {
		t.Fatalf("expected per-line truncation, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "40-char budget") {
		t.Fatalf("expected char-budget truncation notice, got: %s", res.Content)
	}

	// 3. Binary detection (PNG magic bytes and NUL byte).
	pngPath := filepath.Join(dir, "img.png")
	_ = os.WriteFile(pngPath, []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), 0o644)
	resAny, _ = st.executeReadFile(sdk.Context{}, map[string]any{"path": pngPath})
	res = resAny.(sdk.ToolResult)
	if !res.IsError || !strings.Contains(res.Content, "PNG image data") {
		t.Fatalf("expected PNG binary refusal, got: %+v", res)
	}

	// Valid UTF-8 text starting with magic-byte prefixes ("BM", "MZ") must NOT be refused as binary.
	bmPath := filepath.Join(dir, "bm25.txt")
	_ = os.WriteFile(bmPath, []byte("BM25 ranking algorithm\nMZ header note\n"), 0o644)
	resAny, _ = st.executeReadFile(sdk.Context{}, map[string]any{"path": bmPath})
	res = resAny.(sdk.ToolResult)
	if res.IsError || !strings.Contains(res.Content, "1|BM25 ranking algorithm") {
		t.Fatalf("expected valid UTF-8 text with BM prefix to succeed, got: %+v", res)
	}

	// 4. .ipynb JSON cell extraction.
	nbPath := filepath.Join(dir, "demo.ipynb")
	nbJSON := `{
		"cells": [
			{"cell_type": "markdown", "source": ["# Title\n", "Hello"]},
			{"cell_type": "code", "execution_count": 1, "source": ["print(42)\n"], "outputs": [{"output_type": "stream", "name": "stdout", "text": ["42\n"]}]}
		]
	}`
	_ = os.WriteFile(nbPath, []byte(nbJSON), 0o644)
	resAny, _ = st.executeReadFile(sdk.Context{}, map[string]any{"path": nbPath})
	res = resAny.(sdk.ToolResult)
	if res.IsError {
		t.Fatalf("expected ipynb success, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "# Cell 1 (markdown)") || !strings.Contains(res.Content, "print(42)") || !strings.Contains(res.Content, "# Output (stdout):") {
		t.Fatalf("unexpected ipynb output:\n%s", res.Content)
	}

	// 5. Directory listing.
	resAny, _ = st.executeReadFile(sdk.Context{}, map[string]any{"path": dir})
	res = resAny.(sdk.ToolResult)
	if res.IsError || !strings.Contains(res.Content, "sample.txt") {
		t.Fatalf("unexpected directory listing: %+v", res)
	}
}

func TestWriteFileAtomicGuardsAndStaleness(t *testing.T) {
	dir := t.TempDir()
	st := newState(config.Default())

	// Nested parent directory creation + atomic write.
	target := filepath.Join(dir, "nested", "dir", "hello.txt")
	resAny, err := st.executeWriteFile(sdk.Context{}, map[string]any{
		"path":    target,
		"content": "line1\nline2\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	res := resAny.(sdk.ToolResult)
	if res.IsError {
		t.Fatalf("write_file failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "12 bytes (2 lines)") {
		t.Fatalf("unexpected write summary: %s", res.Content)
	}
	raw, _ := os.ReadFile(target)
	if string(raw) != "line1\nline2\n" {
		t.Fatalf("unexpected disk content: %q", string(raw))
	}

	// Sensitive path guard (.env refused when !Yolo and no UI).
	envPath := filepath.Join(dir, ".env")
	resAny, _ = st.executeWriteFile(sdk.Context{}, map[string]any{
		"path":    envPath,
		"content": "SECRET=1\n",
	})
	res = resAny.(sdk.ToolResult)
	if !res.IsError || !strings.Contains(res.Content, "sensitive path") {
		t.Fatalf("expected .env write refusal, got: %+v", res)
	}

	// Private key guard.
	keyPath := filepath.Join(dir, "server.key")
	resAny, _ = st.executeWriteFile(sdk.Context{}, map[string]any{
		"path":    keyPath,
		"content": "PRIVATE KEY",
	})
	res = resAny.(sdk.ToolResult)
	if !res.IsError || !strings.Contains(res.Content, "private key") {
		t.Fatalf("expected .key write refusal, got: %+v", res)
	}

	// Live SetYolo(true) toggle permits .env write without recreating state.
	yoloCfg := config.Default()
	stYolo := newState(yoloCfg)
	yoloCfg.SetYolo(true)
	resAny, _ = stYolo.executeWriteFile(sdk.Context{}, map[string]any{
		"path":    envPath,
		"content": "SECRET=1\n",
	})
	res = resAny.(sdk.ToolResult)
	if res.IsError {
		t.Fatalf("expected live yolo .env write to succeed, got: %s", res.Content)
	}

	// Staleness check: modify tracked file externally -> write_file refuses.
	_, _ = st.executeReadFile(sdk.Context{}, map[string]any{"path": target})
	future := time.Now().Add(5 * time.Second)
	_ = os.WriteFile(target, []byte("external edit\n"), 0o644)
	_ = os.Chtimes(target, future, future)

	resAny, _ = st.executeWriteFile(sdk.Context{}, map[string]any{
		"path":    target,
		"content": "overwrite",
	})
	res = resAny.(sdk.ToolResult)
	if !res.IsError || !strings.Contains(res.Content, "stale baseline") {
		t.Fatalf("expected stale baseline error, got: %+v", res)
	}
}

func TestPatchFuzzyStrategiesAndV4A(t *testing.T) {
	dir := t.TempDir()
	st := newState(config.Default())

	// 1. Exact, line_trimmed, whitespace_normalized, indentation_flexible.
	f := filepath.Join(dir, "code.go")
	orig := "package main\n\nfunc hello() {\n\tmsg := \"hi\"   \n\tprintln(msg)\n}\n"
	_ = os.WriteFile(f, []byte(orig), 0o644)

	// Indentation-flexible / line-trimmed match (different leading spaces, no trailing spaces).
	resAny, _ := st.executePatch(sdk.Context{}, map[string]any{
		"mode":       "replace",
		"path":       f,
		"old_string": "    msg := \"hi\"\n    println(msg)",
		"new_string": "\tmsg := \"hello\"\n\tprintln(msg)",
	})
	res := resAny.(sdk.ToolResult)
	if res.IsError {
		t.Fatalf("fuzzy patch failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "-	msg := \"hi\"") || !strings.Contains(res.Content, "+	msg := \"hello\"") {
		t.Fatalf("expected unified diff in output:\n%s", res.Content)
	}

	// Whitespace-normalized match.
	wsFile := filepath.Join(dir, "ws.txt")
	_ = os.WriteFile(wsFile, []byte("foo    bar\tbaz\n"), 0o644)
	resAny, _ = st.executePatch(sdk.Context{}, map[string]any{
		"path":       wsFile,
		"old_string": "foo bar baz",
		"new_string": "qux",
	})
	res = resAny.(sdk.ToolResult)
	if res.IsError {
		t.Fatalf("whitespace_normalized failed: %s", res.Content)
	}
	got, _ := os.ReadFile(wsFile)
	if string(got) != "qux\n" {
		t.Fatalf("got %q, want %q", string(got), "qux\n")
	}

	// replace_all=false rejects ambiguous matches; replace_all=true succeeds.
	ambigFile := filepath.Join(dir, "ambig.txt")
	_ = os.WriteFile(ambigFile, []byte("dup\nkeep\ndup\n"), 0o644)
	resAny, _ = st.executePatch(sdk.Context{}, map[string]any{
		"path":       ambigFile,
		"old_string": "dup",
		"new_string": "one",
	})
	res = resAny.(sdk.ToolResult)
	if !res.IsError || !strings.Contains(res.Content, "matched 2 locations") {
		t.Fatalf("expected ambiguity error, got: %+v", res)
	}
	resAny, _ = st.executePatch(sdk.Context{}, map[string]any{
		"path":        ambigFile,
		"old_string":  "dup",
		"new_string":  "one",
		"replace_all": true,
	})
	res = resAny.(sdk.ToolResult)
	if res.IsError {
		t.Fatalf("replace_all failed: %s", res.Content)
	}
	got, _ = os.ReadFile(ambigFile)
	if string(got) != "one\nkeep\none\n" {
		t.Fatalf("got %q", string(got))
	}

	// 2. V4A multi-file patch (Add, Update, Delete).
	aPath := filepath.Join(dir, "a.txt")
	delPath := filepath.Join(dir, "del.txt")
	addPath := filepath.Join(dir, "added.txt")
	_ = os.WriteFile(aPath, []byte("one\ntwo\nthree\n"), 0o644)
	_ = os.WriteFile(delPath, []byte("bye\n"), 0o644)

	v4a := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: " + aPath,
		"@@ context @@",
		" one",
		"-two",
		"+TWO",
		" three",
		"*** Add File: " + addPath,
		"+new line 1",
		"+new line 2",
		"*** Delete File: " + delPath,
		"*** End Patch",
	}, "\n")

	resAny, _ = st.executePatch(sdk.Context{}, map[string]any{
		"mode":  "patch",
		"patch": v4a,
	})
	res = resAny.(sdk.ToolResult)
	if res.IsError {
		t.Fatalf("V4A patch failed: %s", res.Content)
	}
	gotA, _ := os.ReadFile(aPath)
	if string(gotA) != "one\nTWO\nthree\n" {
		t.Fatalf("updated a.txt = %q", string(gotA))
	}
	gotAdd, _ := os.ReadFile(addPath)
	if string(gotAdd) != "new line 1\nnew line 2\n" {
		t.Fatalf("added.txt = %q", string(gotAdd))
	}
	if _, err := os.Stat(delPath); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be deleted", delPath)
	}
}

func TestPatchV4AAtomicPreValidation(t *testing.T) {
	dir := t.TempDir()
	st := newState(config.Default())

	aPath := filepath.Join(dir, "a.txt")
	existingPath := filepath.Join(dir, "existing.txt")
	delPath := filepath.Join(dir, "del.txt")
	_ = os.WriteFile(aPath, []byte("one\ntwo\nthree\n"), 0o644)
	_ = os.WriteFile(existingPath, []byte("keep me\n"), 0o644)
	_ = os.WriteFile(delPath, []byte("still here\n"), 0o644)

	// Update a.txt + Delete del.txt + Add existing.txt (must fail before ANY disk change).
	v4aAddExisting := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: " + aPath,
		"@@ context @@",
		" one",
		"-two",
		"+TWO",
		" three",
		"*** Delete File: " + delPath,
		"*** Add File: " + existingPath,
		"+overwrite attempt",
		"*** End Patch",
	}, "\n")

	resAny, _ := st.executePatch(sdk.Context{}, map[string]any{
		"mode":  "patch",
		"patch": v4aAddExisting,
	})
	res := resAny.(sdk.ToolResult)
	if !res.IsError || !strings.Contains(res.Content, "already exists") {
		t.Fatalf("expected Add existing failure, got: %+v", res)
	}
	if got, _ := os.ReadFile(aPath); string(got) != "one\ntwo\nthree\n" {
		t.Fatalf("a.txt was modified despite failed V4A patch: %q", string(got))
	}
	if got, _ := os.ReadFile(existingPath); string(got) != "keep me\n" {
		t.Fatalf("existing.txt was modified despite failed V4A patch: %q", string(got))
	}
	if _, err := os.Stat(delPath); err != nil {
		t.Fatalf("del.txt was deleted despite failed V4A patch: %v", err)
	}

	// Update a.txt + Move del.txt -> existing.txt (must fail before ANY disk change).
	v4aMoveExisting := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: " + aPath,
		"@@ context @@",
		" one",
		"-two",
		"+TWO",
		" three",
		"*** Move File: " + delPath + " -> " + existingPath,
		"*** End Patch",
	}, "\n")
	resAny, _ = st.executePatch(sdk.Context{}, map[string]any{
		"mode":  "patch",
		"patch": v4aMoveExisting,
	})
	res = resAny.(sdk.ToolResult)
	if !res.IsError || !strings.Contains(res.Content, "destination already exists") {
		t.Fatalf("expected Move destination already exists failure, got: %+v", res)
	}
	if got, _ := os.ReadFile(aPath); string(got) != "one\ntwo\nthree\n" {
		t.Fatalf("a.txt was modified despite failed Move V4A patch: %q", string(got))
	}

	// Update a.txt + Delete missing file (must fail before ANY disk change).
	v4aDelMissing := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: " + aPath,
		"@@ context @@",
		" one",
		"-two",
		"+TWO",
		" three",
		"*** Delete File: " + filepath.Join(dir, "missing.txt"),
		"*** End Patch",
	}, "\n")
	resAny, _ = st.executePatch(sdk.Context{}, map[string]any{
		"mode":  "patch",
		"patch": v4aDelMissing,
	})
	res = resAny.(sdk.ToolResult)
	if !res.IsError || !strings.Contains(res.Content, "does not exist") {
		t.Fatalf("expected Delete missing failure, got: %+v", res)
	}
	if got, _ := os.ReadFile(aPath); string(got) != "one\ntwo\nthree\n" {
		t.Fatalf("a.txt was modified despite failed Delete V4A patch: %q", string(got))
	}
}

func TestSearchFilesContentAndFilesAndFallback(t *testing.T) {
	dir := t.TempDir()
	st := newState(config.Default())

	_ = os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	_ = os.MkdirAll(filepath.Join(dir, "node_modules", "pkg"), 0o755)
	_ = os.MkdirAll(filepath.Join(dir, ".git"), 0o755)

	_ = os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n// needle here\nfunc main() {}\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "sub", "util.go"), []byte("package sub\n// needle again\n// needle twice\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("needle in txt\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "node_modules", "pkg", "ignored.js"), []byte("needle ignored\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("needle git\n"), 0o644)

	// Content search with file_glob.
	resAny, _ := st.executeSearchFiles(sdk.Context{}, map[string]any{
		"pattern":   "needle",
		"path":      dir,
		"file_glob": "*.go",
	})
	res := resAny.(sdk.ToolResult)
	if res.IsError {
		t.Fatalf("search_files failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "main.go") || !strings.Contains(res.Content, "util.go") || strings.Contains(res.Content, "notes.txt") || strings.Contains(res.Content, "node_modules") {
		t.Fatalf("unexpected content search results:\n%s", res.Content)
	}

	// Output mode: count.
	resAny, _ = st.executeSearchFiles(sdk.Context{}, map[string]any{
		"pattern":     "needle",
		"path":        dir,
		"file_glob":   "*.go",
		"output_mode": "count",
	})
	res = resAny.(sdk.ToolResult)
	if res.IsError || !strings.Contains(res.Content, ":2") {
		t.Fatalf("expected count output with :2, got:\n%s", res.Content)
	}

	// Output mode: files_only.
	resAny, _ = st.executeSearchFiles(sdk.Context{}, map[string]any{
		"pattern":     "needle",
		"path":        dir,
		"output_mode": "files_only",
	})
	res = resAny.(sdk.ToolResult)
	if res.IsError || strings.Contains(res.Content, "node_modules") || strings.Contains(res.Content, ".git") {
		t.Fatalf("unexpected files_only output:\n%s", res.Content)
	}

	// Target: files.
	resAny, _ = st.executeSearchFiles(sdk.Context{}, map[string]any{
		"pattern": "*.go",
		"target":  "files",
		"path":    dir,
	})
	res = resAny.(sdk.ToolResult)
	if res.IsError || !strings.Contains(res.Content, "main.go") || strings.Contains(res.Content, "notes.txt") {
		t.Fatalf("unexpected file search output:\n%s", res.Content)
	}

	// Explicitly exercise pure-Go content search fallback with context lines.
	res, err := searchContentPureGo(dir, "needle", "*.go", "content", 1, 0, 50)
	if err != nil || res.IsError {
		t.Fatalf("pure-Go search failed: err=%v res=%+v", err, res)
	}
	if !strings.Contains(res.Content, "package main") || strings.Contains(res.Content, "node_modules") {
		t.Fatalf("unexpected pure-Go search output:\n%s", res.Content)
	}
}

func TestReadTrackingRaceSafety(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "race.txt")
	_ = os.WriteFile(p, []byte("hello\nworld\n"), 0o644)

	st := newState(config.Default())
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 10 {
				_, _ = st.executeReadFile(sdk.Context{}, map[string]any{"path": p})
			}
		}()
	}
	wg.Wait()
}

func TestReadFileDetectsNULByteInASCIIAsBinary(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "nul.bin")
	if err := os.WriteFile(p, []byte("hello\x00world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := newState(config.Default())
	resAny, err := st.executeReadFile(sdk.Context{}, map[string]any{"path": p})
	if err != nil {
		t.Fatal(err)
	}
	res := resAny.(sdk.ToolResult)
	if !res.IsError || !strings.Contains(strings.ToLower(res.Content), "binary") {
		t.Fatalf("expected binary refusal for NUL-containing ASCII file, got isError=%v content=%q", res.IsError, res.Content)
	}
}
