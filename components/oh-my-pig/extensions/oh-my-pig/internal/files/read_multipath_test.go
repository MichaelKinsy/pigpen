package files

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/hashline"
)

// Tests porting upstream:
// - packages/coding-agent/test/tools/glob-validate-paths.test.ts (splitDelimitedPathEntry)
// - packages/coding-agent/test/tools/grep-path-lists.test.ts (read delimited paths)
// - packages/coding-agent/test/tools/read-mixed-path-list.test.ts (mixed URL/local & ?q= lists)
// - packages/coding-agent/test/tools/read-raw-range.test.ts (suffix resolution with :raw)

func TestSplitDelimitedPathEntryUpstream(t *testing.T) {
	dir := t.TempDir()
	mustWrite := func(rel, body string) {
		t.Helper()
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("apps/a.txt", "a\n")
	mustWrite("packages/b.txt", "b\n")
	mustWrite("folder with spaces/file.txt", "spaces\n")
	mustWrite("a;b.md", "literal semi\n")

	got, err := splitDelimitedPathEntry("apps/a.txt, packages/b.txt", dir, nil)
	if err != nil || len(got) != 2 || got[0] != "apps/a.txt" || got[1] != "packages/b.txt" {
		t.Fatalf("comma split: got %v, err %v", got, err)
	}
	got, err = splitDelimitedPathEntry("apps/a.txt;packages/b.txt", dir, nil)
	if err != nil || len(got) != 2 || got[0] != "apps/a.txt" || got[1] != "packages/b.txt" {
		t.Fatalf("semicolon split: got %v, err %v", got, err)
	}
	got, err = splitDelimitedPathEntry("apps/a.txt packages/b.txt", dir, nil)
	if err != nil || len(got) != 2 || got[0] != "apps/a.txt" || got[1] != "packages/b.txt" {
		t.Fatalf("whitespace split: got %v, err %v", got, err)
	}
	got, err = splitDelimitedPathEntry("folder with spaces/file.txt", dir, nil)
	if err != nil || got != nil {
		t.Fatalf("existing spaced path should stay whole: got %v, err %v", got, err)
	}
	got, err = splitDelimitedPathEntry("src/{a,b}.txt", dir, nil)
	if err != nil || got != nil {
		t.Fatalf("brace group should stay whole: got %v, err %v", got, err)
	}
	got, err = splitDelimitedPathEntry("apps/a.txt\\,packages/b.txt", dir, nil)
	if err != nil || got != nil {
		t.Fatalf("escaped comma should stay whole: got %v, err %v", got, err)
	}
	got, err = splitDelimitedPathEntry("apps/a.txt\\;packages/b.txt", dir, nil)
	if err != nil || got != nil {
		t.Fatalf("escaped semicolon should stay whole: got %v, err %v", got, err)
	}
	got, err = splitDelimitedPathEntry("a;b.md:1-1", dir, nil)
	if err != nil || got != nil {
		t.Fatalf("literal semicolon file with selector should stay whole: got %v, err %v", got, err)
	}
	// All-missing semicolon list still splits; all-missing comma list stays nil.
	got, err = splitDelimitedPathEntry("missing1.txt;missing2.txt", dir, nil)
	if err != nil || len(got) != 2 {
		t.Fatalf("all-missing semicolon list should split: got %v, err %v", got, err)
	}
	got, err = splitDelimitedPathEntry("missing1.txt, missing2.txt", dir, nil)
	if err != nil || got != nil {
		t.Fatalf("all-missing comma list should stay nil: got %v, err %v", got, err)
	}
}

func TestReadDelimitedPaths(t *testing.T) {
	dir := t.TempDir()
	store := hashline.NewStore()
	mustWrite := func(rel, body string) {
		t.Helper()
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("apps/grep.txt", "shared-needle apps\nline2\n")
	mustWrite("packages/grep.txt", "shared-needle packages\n")
	mustWrite("phases/grep.txt", "shared-needle phases\n")
	mustWrite("a;b.md", "literal semicolon file\n")
	mustWrite("a", "plain a\n")
	mustWrite("b.md", "plain b\n")
	mustWrite("x.json", `{"a":"x-a","b":"x-b"}`+"\n")
	mustWrite("y.json", `{"a":"y-a","b":"y-b"}`+"\n")

	t.Run("comma list", func(t *testing.T) {
		res, err := executeRead(store, dir, "apps/grep.txt, packages/grep.txt")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(res.Content, "Note: interpreted as 2 paths: apps/grep.txt, packages/grep.txt") {
			t.Fatalf("missing notice: %s", res.Content)
		}
		if !strings.Contains(res.Content, "shared-needle apps") || !strings.Contains(res.Content, "shared-needle packages") {
			t.Fatalf("missing part content: %s", res.Content)
		}
		d := res.Details.(map[string]any)
		links := d["displayReadTargetLinks"].([]any)
		if len(links) != 2 || !strings.HasSuffix(links[0].(string), "apps/grep.txt") || !strings.HasSuffix(links[1].(string), "packages/grep.txt") {
			t.Fatalf("unexpected links: %#v", links)
		}
	})

	t.Run("nested mixed delimiters flatten targets and links", func(t *testing.T) {
		res, err := executeRead(store, dir, "apps/grep.txt, packages/grep.txt; phases/grep.txt")
		if err != nil {
			t.Fatal(err)
		}
		d := res.Details.(map[string]any)
		targets := d["displayReadTargets"].([]string)
		if len(targets) != 3 || targets[0] != "apps/grep.txt" || targets[1] != "packages/grep.txt" || targets[2] != "phases/grep.txt" {
			t.Fatalf("unexpected flattened targets: %#v", targets)
		}
		links := d["displayReadTargetLinks"].([]any)
		if len(links) != 3 {
			t.Fatalf("unexpected flattened links: %#v", links)
		}
	})

	t.Run("semicolon list with selectors", func(t *testing.T) {
		res, err := executeRead(store, dir, "apps/grep.txt:1-1;packages/grep.txt:raw")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(res.Content, "Note: interpreted as 2 paths: apps/grep.txt:1-1, packages/grep.txt:raw") {
			t.Fatalf("missing notice: %s", res.Content)
		}
		if !strings.Contains(res.Content, "shared-needle apps") || !strings.Contains(res.Content, "shared-needle packages\n") {
			t.Fatalf("missing selector content: %s", res.Content)
		}
	})

	t.Run("missing peer keeps readable part and nil link", func(t *testing.T) {
		res, err := executeRead(store, dir, "missing.txt, packages/grep.txt")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(res.Content, "[Could not read missing.txt: Path 'missing.txt' not found]") {
			t.Fatalf("missing error note: %s", res.Content)
		}
		d := res.Details.(map[string]any)
		notes := d["notes"].([]string)
		if len(notes) != 2 || notes[1] != "Could not read missing.txt: Path 'missing.txt' not found" {
			t.Fatalf("unexpected notes: %#v", notes)
		}
		links := d["displayReadTargetLinks"].([]any)
		if len(links) != 2 || links[0] != nil || !strings.HasSuffix(links[1].(string), "packages/grep.txt") {
			t.Fatalf("unexpected links: %#v", links)
		}
	})

	t.Run("literal semicolon filename wins over split", func(t *testing.T) {
		res, err := executeRead(store, dir, "a;b.md:1-1")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(res.Content, "interpreted as") || !strings.Contains(res.Content, "literal semicolon file") {
			t.Fatalf("literal semicolon file should win: %s", res.Content)
		}
	})

	t.Run("semicolon beats fuzzy suffix decoy", func(t *testing.T) {
		mustWrite("decoy/apps/grep.txt; packages/grep.txt", "fuzzy-decoy\n")
		res, err := executeRead(store, dir, "apps/grep.txt; packages/grep.txt")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(res.Content, "fuzzy-decoy") || !strings.Contains(res.Content, "interpreted as 2 paths") {
			t.Fatalf("semicolon list should beat suffix decoy: %s", res.Content)
		}
	})

	t.Run("json query list with braces", func(t *testing.T) {
		res, err := executeRead(store, dir, "x.json?q={a,b};y.json?q=.b")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(res.Content, "interpreted as 2 paths") || !strings.Contains(res.Content, "x-a") || !strings.Contains(res.Content, "y-b") || strings.Contains(res.Content, "y-a") {
			t.Fatalf("unexpected json query list output: %s", res.Content)
		}
	})
}

func TestReadSuffixResolution(t *testing.T) {
	dir := t.TempDir()
	store := hashline.NewStore()
	mustWrite := func(rel, body string) {
		t.Helper()
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("nested/recovered.txt", "A\nB\n")
	mustWrite("nested/recovered.json", `["A","B"]`)
	mustWrite("dup1/same.txt", "1\n")
	mustWrite("dup2/same.txt", "2\n")
	mustWrite("sub/onlydir/item.txt", "hello\n")
	mustWrite(".gitignore", "ignored_dir/\n")
	mustWrite("ignored_dir/secret.txt", "nope\n")

	// Create nested zip archive for suffix archive test.
	zipPath := filepath.Join(dir, "nested", "recovered.zip")
	zf, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	w, err := zw.Create("lines.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("A\nB\n"))
	_ = zw.Close()
	_ = zf.Close()

	t.Run("unique file suffix", func(t *testing.T) {
		res, err := executeRead(store, dir, "recovered.txt")
		if err != nil {
			t.Fatal(err)
		}
		wantPrefix := "[Path 'recovered.txt' not found; resolved to 'nested/recovered.txt' via suffix match]\n"
		if !strings.HasPrefix(res.Content, wantPrefix) {
			t.Fatalf("unexpected content: %q", res.Content)
		}
		d := res.Details.(map[string]any)
		sr := d["suffixResolution"].(map[string]any)
		if sr["from"] != "recovered.txt" || sr["to"] != "nested/recovered.txt" {
			t.Fatalf("unexpected suffixResolution: %#v", sr)
		}
	})

	t.Run("raw suffix suppresses banner and marks raw", func(t *testing.T) {
		res, err := executeRead(store, dir, "recovered.txt:2:raw")
		if err != nil {
			t.Fatal(err)
		}
		if res.Content != "B\n" {
			t.Fatalf("raw content should not include banner: %q", res.Content)
		}
		d := res.Details.(map[string]any)
		sr := d["suffixResolution"].(map[string]any)
		if sr["to"] != "nested/recovered.txt" || sr["raw"] != true {
			t.Fatalf("unexpected raw suffixResolution: %#v", sr)
		}
	})

	t.Run("archive suffix", func(t *testing.T) {
		res, err := executeRead(store, dir, "recovered.zip:lines.txt")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(res.Content, "via suffix match") || !strings.Contains(res.Content, "A\nB") {
			t.Fatalf("unexpected archive suffix content: %q", res.Content)
		}
		resRaw, err := executeRead(store, dir, "recovered.zip:lines.txt:2:raw")
		if err != nil {
			t.Fatal(err)
		}
		if resRaw.Content != "B\n" {
			t.Fatalf("raw archive suffix should not include banner: %q", resRaw.Content)
		}
	})

	t.Run("json suffix", func(t *testing.T) {
		res, err := executeRead(store, dir, "recovered.json?q=.[]&raw=true")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(res.Content, "via suffix match") || !strings.Contains(res.Content, "A\nB") {
			t.Fatalf("unexpected json suffix content: %q", res.Content)
		}
		resRaw, err := executeRead(store, dir, "recovered.json?q=.[]&raw=true:2:raw")
		if err != nil {
			t.Fatal(err)
		}
		if resRaw.Content != "B" {
			t.Fatalf("raw json suffix should not include banner: %q", resRaw.Content)
		}
	})

	t.Run("directory suffix", func(t *testing.T) {
		res, err := executeRead(store, dir, "onlydir")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(res.Content, "via suffix match") {
			t.Fatalf("directory listing should not prepend suffix banner: %q", res.Content)
		}
		d := res.Details.(map[string]any)
		sr := d["suffixResolution"].(map[string]any)
		if sr["from"] != "onlydir" || sr["to"] != "sub/onlydir" {
			t.Fatalf("unexpected dir suffixResolution: %#v", sr)
		}
	})

	t.Run("ambiguous suffix returns not found", func(t *testing.T) {
		_, err := executeRead(store, dir, "same.txt")
		if err == nil || err.Error() != "Path 'same.txt' not found" {
			t.Fatalf("expected Path 'same.txt' not found, got %v", err)
		}
	})

	t.Run("gitignored file is not suffix-matched", func(t *testing.T) {
		_, err := executeRead(store, dir, "secret.txt")
		if err == nil || err.Error() != "Path 'secret.txt' not found" {
			t.Fatalf("gitignored file should not match: got %v", err)
		}
	})
}
