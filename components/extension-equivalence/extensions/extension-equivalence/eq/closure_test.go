package eq

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An original split over ui.ts and src/ is not identified by index.ts alone: the golden header's hash must change when
// any file Pi loads changes (the entry, a module it reaches, the package.json), and only then.
func TestTSClosureHashCoversTheFilesTheOriginalLoads(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("index.ts", `import { ui } from "./ui.ts";\nimport { a } from "./src/a.ts";\nexport default () => ui + a;\n`)
	write("ui.ts", `import { card } from "./src/ui/card.ts";\nexport const ui = card;\n`)
	write("src/a.ts", "export const a = 1;\n")
	write("src/ui/card.ts", "export const card = 'c';\n")
	write("package.json", `{"name":"x","pi":{"extensions":["./index.ts"]}}`)
	write("src/unused.ts", "export const unused = 1;\n")
	write("test/a.test.ts", "// a test\n")
	write("README.md", "docs\n")
	entry := filepath.Join(dir, "index.ts")
	base, err := TSClosureHash(entry)
	if err != nil || !strings.HasPrefix(base, "sha256:") {
		t.Fatalf("closure hash: %q %v", base, err)
	}
	if again, _ := TSClosureHash(entry); again != base {
		t.Error("the closure hash is not deterministic")
	}
	single, err := hashExtension(entry)
	if err != nil || single == base {
		t.Errorf("the entry-file hash %q must stay what it was and differ from the closure hash %q (%v)", single, base, err)
	}
	for _, change := range []string{"index.ts", "ui.ts", "src/a.ts", "src/ui/card.ts", "package.json"} {
		p := filepath.Join(dir, filepath.FromSlash(change))
		old, _ := os.ReadFile(p)
		if err := os.WriteFile(p, append(old, ' '), 0o644); err != nil {
			t.Fatal(err)
		}
		if got, _ := TSClosureHash(entry); got == base {
			t.Errorf("a change to %s did not change the closure hash", change)
		}
		_ = os.WriteFile(p, old, 0o644)
	}
	for _, change := range []string{"src/unused.ts", "test/a.test.ts", "README.md"} {
		p := filepath.Join(dir, filepath.FromSlash(change))
		old, _ := os.ReadFile(p)
		_ = os.WriteFile(p, append(old, ' '), 0o644)
		if got, _ := TSClosureHash(entry); got != base {
			t.Errorf("a change to %s, which the original does not load, changed the closure hash", change)
		}
		_ = os.WriteFile(p, old, 0o644)
	}
	// An entry without a package.json beside it hashes its closure alone.
	lone := filepath.Join(t.TempDir(), "only.ts")
	if err := os.WriteFile(lone, []byte("export default () => 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if h, err := TSClosureHash(lone); err != nil || h == "" {
		t.Errorf("single file closure: %q %v", h, err)
	}
	if _, err := TSClosureHash(filepath.Join(dir, "missing.ts")); err == nil {
		t.Error("a missing entry was hashed")
	}
}

// The new header field is opt-in: a header recorded without it is byte for byte what it always was.
func TestHeaderExtensionFilesIsAbsentUnlessTheClosureWasHashed(t *testing.T) {
	var plain, closure bytes.Buffer
	tr := &Trace{Header: Header{Scenario: "s", Lane: OracleTS, Host: "pi 1.5.0", Extension: "sha256:abc", Normalizer: NormalizerVersion}}
	if err := tr.Write(&plain); err != nil {
		t.Fatal(err)
	}
	if want := `{"kind":"header","scenario":"s","lane":"pi-ts","host":"pi 1.5.0","extension":"sha256:abc","normalizer":"v3"}` + "\n"; plain.String() != want {
		t.Errorf("header\n got %s\nwant %s", plain.String(), want)
	}
	tr.Header.ExtensionFiles = ExtensionFilesClosure
	if err := tr.Write(&closure); err != nil {
		t.Fatal(err)
	}
	if want := `{"kind":"header","scenario":"s","lane":"pi-ts","host":"pi 1.5.0","extension":"sha256:abc","extensionFiles":"closure","normalizer":"v3"}` + "\n"; closure.String() != want {
		t.Errorf("closure header\n got %s\nwant %s", closure.String(), want)
	}
	p := filepath.Join(t.TempDir(), "t.jsonl")
	if err := os.WriteFile(p, closure.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	back, err := ReadTrace(p)
	if err != nil || back.Header.ExtensionFiles != "closure" {
		t.Errorf("round trip: %+v %v", back, err)
	}
	// A different header does not make two traces differ: the header is not compared.
	if d := Diff(&Trace{Header: tr.Header}, &Trace{Header: Header{Extension: "sha256:other"}}); d != nil {
		t.Errorf("headers were compared: %v", d)
	}
}
