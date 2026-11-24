package hashline

import (
	"strings"
	"testing"
)

// upstream: crates/pi-edit/src/store.rs: "file_hash strips trailing spaces/tabs/CRs"
func TestFileHashNormalization(t *testing.T) {
	base := "hello\nworld\n"
	withTrailing := "hello   \t\r\nworld \r\n"
	if got, want := FileHash(withTrailing), FileHash(base); got != want {
		t.Fatalf("FileHash trailing whitespace mismatch: got %s, want %s", got, want)
	}
	if len(FileHash(base)) != 4 {
		t.Fatalf("expected 4-hex tag, got %q", FileHash(base))
	}
	if FileHash("a\n") == FileHash("b\n") {
		t.Fatalf("distinct files unexpectedly collided")
	}
}

// upstream: crates/pi-edit/src/text.rs: "detect_line_ending and restore_line_endings"
func TestLineEndingsAndBOM(t *testing.T) {
	bom, rest := StripBOM(BOM + "a\r\nb\r\n")
	if bom != BOM {
		t.Fatalf("expected BOM")
	}
	if DetectLineEnding(rest) != LineEndingCRLF {
		t.Fatalf("expected CRLF")
	}
	norm := NormalizeToLF(rest)
	if norm != "a\nb\n" {
		t.Fatalf("unexpected normalized: %q", norm)
	}
	if got := RestoreLineEndings(norm, LineEndingCRLF); got != "a\r\nb\r\n" {
		t.Fatalf("unexpected restored: %q", got)
	}
}

// upstream: crates/pi-edit/tests/hashline_parse.rs: "parse all hashline ops"
func TestParseAllOps(t *testing.T) {
	patch := strings.Join([]string{
		"[foo.go#1A2B]",
		"PUT 1.=2:",
		"+line1",
		"+line2",
		"PUT 5*:",
		"+func f() {}",
		"PUT <1:",
		"+// header",
		"PUT >3:",
		"+// after 3",
		"PUT >$:",
		"+// eof",
		"PUT >5*:",
		"+// after block",
		"CUT 7.=8 @saved",
		"CUT 10*",
		"PUT <1 @saved",
		"PUT >2",
		"PUT >$ @saved",
		"PUT 3.=4 @saved",
		"PUT 6* @saved",
		"MV \"sub/new name.go\"",
		"[bar.go#3C4D]",
		"REM",
	}, "\n")

	sections, err := ParseInput(patch)
	if err != nil {
		t.Fatalf("ParseInput failed: %v", err)
	}
	if len(sections) != 2 {
		t.Fatalf("expected 2 sections, got %d", len(sections))
	}
	if sections[0].Path != "foo.go" || sections[0].FileHash != "1A2B" || !sections[0].HasHash {
		t.Fatalf("unexpected section 0 header: %+v", sections[0])
	}
	if sections[0].FileOp.Kind != FileOpMove || sections[0].FileOp.Dest != "sub/new name.go" {
		t.Fatalf("unexpected move op: %+v", sections[0].FileOp)
	}
	if len(sections[0].Ops) != 13 {
		t.Fatalf("expected 13 ops, got %d", len(sections[0].Ops))
	}
	if sections[1].FileOp.Kind != FileOpDelete {
		t.Fatalf("expected REM on section 1")
	}
}

// upstream: crates/pi-edit/tests/hashline_apply.rs: "apply range, gap, block, cut, and paste ops"
func TestApplyOps(t *testing.T) {
	src := "alpha\nbeta\ngamma\ndelta\n"
	clip := &Clipboard{}

	// Replace range + insert before + insert after + insert EOF
	patch := "[f.txt#0000]\nPUT <1:\n+head\nPUT 2.=3:\n+BETA\nPUT >4:\n+after4\nPUT >$:\n+tail\n"
	secs, err := ParseInput(patch)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	res, err := ApplyOps(src, "f.txt", secs[0].Ops, clip)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := "head\nalpha\nBETA\ndelta\nafter4\ntail\n"
	if res.Content != want {
		t.Fatalf("got:\n%q\nwant:\n%q", res.Content, want)
	}

	// CUT and anonymous paste
	patch2 := "[f.txt#0000]\nCUT 1.=2\nPUT >$\n"
	secs2, err := ParseInput(patch2)
	if err != nil {
		t.Fatalf("parse2: %v", err)
	}
	res2, err := ApplyOps("a\nb\nc\n", "f.txt", secs2[0].Ops, &Clipboard{})
	if err != nil {
		t.Fatalf("apply2: %v", err)
	}
	if res2.Content != "c\na\nb\n" {
		t.Fatalf("cut+paste got %q, want %q", res2.Content, "c\na\nb\n")
	}

	// Named register paste over range
	clip3 := &Clipboard{}
	secs3, _ := ParseInput("[f.txt#0000]\nCUT 1.=1 @r\nPUT 3.=3 @r\n")
	res3, err := ApplyOps("one\ntwo\nthree\n", "f.txt", secs3[0].Ops, clip3)
	if err != nil {
		t.Fatalf("apply3: %v", err)
	}
	if res3.Content != "two\none\n" {
		t.Fatalf("named paste over range got %q, want %q", res3.Content, "two\none\n")
	}
}

// upstream: crates/pi-edit/tests/hashline_apply.rs: "block resolution for brace, python, and markdown"
func TestBlockOps(t *testing.T) {
	// Go/brace block
	goSrc := "package p\n\nfunc a() {\n\tprintln(1)\n}\n\nfunc b() {}\n"
	secs, err := ParseInput("[a.go#0000]\nPUT 3*:\n+func a() {\n+\tprintln(2)\n+}\n")
	if err != nil {
		t.Fatalf("parse go: %v", err)
	}
	res, err := ApplyOps(goSrc, "a.go", secs[0].Ops, &Clipboard{})
	if err != nil {
		t.Fatalf("apply go: %v", err)
	}
	if !strings.Contains(res.Content, "println(2)") || !strings.Contains(res.Content, "func b()") {
		t.Fatalf("unexpected go block result:\n%s", res.Content)
	}

	// Python indent block with decorator
	pySrc := "@cache\ndef greet(name):\n    print(name)\n\nx = 1\n"
	secsPy, err := ParseInput("[g.py#0000]\nPUT 1*:\n+def greet():\n+    pass\n")
	if err != nil {
		t.Fatalf("parse py: %v", err)
	}
	resPy, err := ApplyOps(pySrc, "g.py", secsPy[0].Ops, &Clipboard{})
	if err != nil {
		t.Fatalf("apply py: %v", err)
	}
	if resPy.Content != "def greet():\n    pass\n\nx = 1\n" {
		t.Fatalf("unexpected py result: %q", resPy.Content)
	}

	// Markdown heading block + PUT >N*:
	mdSrc := "# Title\n\n## Sub\ntext\n\n# Next\nmore\n"
	secsMd, err := ParseInput("[doc.md#0000]\nPUT >1*:\n+\n+## Inserted\n")
	if err != nil {
		t.Fatalf("parse md: %v", err)
	}
	resMd, err := ApplyOps(mdSrc, "doc.md", secsMd[0].Ops, &Clipboard{})
	if err != nil {
		t.Fatalf("apply md: %v", err)
	}
	if !strings.Contains(resMd.Content, "## Inserted\n\n# Next") {
		t.Fatalf("unexpected md result: %q", resMd.Content)
	}
}

// upstream: crates/pi-edit/src/modes/hashline/mismatch.rs: "format_mismatch_message"
func TestMismatchMessage(t *testing.T) {
	msg := FormatMismatchMessage(MismatchDetails{
		Path:             "foo.go",
		ExpectedFileHash: "1111",
		ActualFileHash:   "2222",
		HashRecognized:   true,
	})
	if !strings.Contains(msg, "Edit rejected for foo.go: file changed between read and edit.") {
		t.Fatalf("unexpected recognized mismatch: %s", msg)
	}

	msgUnrec := FormatMismatchMessage(MismatchDetails{
		Path:             "foo.go",
		ExpectedFileHash: "AAAA",
		ActualFileHash:   "2222",
		HashRecognized:   false,
		TagOriginPaths:   []string{"/other/bar.go"},
	})
	if !strings.Contains(msgUnrec, "hash #AAAA is not from this session.") ||
		!strings.Contains(msgUnrec, "Hash #AAAA was issued in this session for /other/bar.go.") {
		t.Fatalf("unexpected unrecognized mismatch: %s", msgUnrec)
	}
}
