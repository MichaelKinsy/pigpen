package files

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/hashline"
)

// Covers read selector forms (:N, :N-, :N-M, :N+K, :-N, comma lists, :raw).
func TestReadSelectorForms(t *testing.T) {
	dir := t.TempDir()
	var lines []string
	for i := 1; i <= 100; i++ {
		lines = append(lines, "line"+string(rune('0'+(i/100)%10))+string(rune('0'+(i/10)%10))+string(rune('0'+i%10)))
	}
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	store := hashline.NewStore()

	// Single range :50-55 adds 1 leading and 3 trailing context lines (49..58)
	res, err := executeRead(store, dir, "notes.txt:50-55")
	if err != nil {
		t.Fatalf(":50-55 err: %v", err)
	}
	if !strings.HasPrefix(res.Content, "[notes.txt#") {
		t.Fatalf("expected snapshot header, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "\n49:line049\n") || !strings.Contains(res.Content, "\n58:line058") {
		t.Fatalf("expected context lines 49..58, got:\n%s", res.Content)
	}
	if strings.Contains(res.Content, "\n48:") || strings.Contains(res.Content, "\n59:") {
		t.Fatalf("unexpected lines outside 49..58:\n%s", res.Content)
	}

	// Count selector :50+5 (lines 50..54 + context 49..57)
	res, err = executeRead(store, dir, "notes.txt:50+5")
	if err != nil {
		t.Fatalf(":50+5 err: %v", err)
	}
	if !strings.Contains(res.Content, "\n49:line049\n") || !strings.Contains(res.Content, "\n57:line057") {
		t.Fatalf(":50+5 missing expected context:\n%s", res.Content)
	}

	// Single line :98 (lines 97..100 with context) and open-ended :98- (lines 97..100 with context)
	for _, sel := range []string{"notes.txt:98", "notes.txt:98-"} {
		res, err = executeRead(store, dir, sel)
		if err != nil {
			t.Fatalf("%s err: %v", sel, err)
		}
		if !strings.Contains(res.Content, "\n97:line097\n") || !strings.Contains(res.Content, "\n100:line100") {
			t.Fatalf("%s unexpected output:\n%s", sel, res.Content)
		}
	}
	// Bare :50 is single line (49..53 with context, not through EOF)
	res, err = executeRead(store, dir, "notes.txt:50")
	if err != nil {
		t.Fatalf(":50 err: %v", err)
	}
	if !strings.Contains(res.Content, "\n49:line049\n50:line050\n") || !strings.Contains(res.Content, "\n53:line053") {
		t.Fatalf(":50 missing expected context:\n%s", res.Content)
	}
	if strings.Contains(res.Content, "\n54:line054") {
		t.Fatalf(":50 should not include line 54:\n%s", res.Content)
	}
	// Tail selector :-5 (lines 96..100 + leading context 95)
	res, err = executeRead(store, dir, "notes.txt:-5")
	if err != nil {
		t.Fatalf(":-5 err: %v", err)
	}
	if !strings.Contains(res.Content, "\n95:line095\n") || !strings.Contains(res.Content, "\n100:line100") {
		t.Fatalf(":-5 unexpected output:\n%s", res.Content)
	}

	// Comma list :5-6,99-100 and individual lines :19,59
	res, err = executeRead(store, dir, "notes.txt:5-6,99-100")
	if err != nil {
		t.Fatalf("comma range err: %v", err)
	}
	if !strings.Contains(res.Content, "\n5:line005\n6:line006\n…\n99:line099\n100:line100") {
		t.Fatalf("comma range unexpected:\n%s", res.Content)
	}

	res, err = executeRead(store, dir, "notes.txt:19,59")
	if err != nil {
		t.Fatalf("comma single err: %v", err)
	}
	if !strings.Contains(res.Content, "\n19:line019\n…\n59:line059") {
		t.Fatalf("comma single unexpected:\n%s", res.Content)
	}

	// :raw forms stay exact without header, line numbers, or context
	for _, sel := range []string{"notes.txt:2-4:raw", "notes.txt:raw:2-4"} {
		res, err = executeRead(store, dir, sel)
		if err != nil {
			t.Fatalf("%s err: %v", sel, err)
		}
		want := "line002\nline003\nline004\n"
		if res.Content != want {
			t.Fatalf("%s got %q, want %q", sel, res.Content, want)
		}
	}

	res, err = executeRead(store, dir, "notes.txt:raw")
	if err != nil {
		t.Fatalf(":raw err: %v", err)
	}
	if res.Content != content {
		t.Fatalf(":raw did not return verbatim content")
	}
}

// Covers read-then-edit using returned tag, stale-tag rejection, and invented-tag rejection.
func TestReadThenEditAndStaleTag(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	store := hashline.NewStore()
	readRes, err := executeRead(store, dir, "app.go")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	firstLine := strings.SplitN(readRes.Content, "\n", 2)[0]
	if !strings.HasPrefix(firstLine, "[app.go#") || !strings.HasSuffix(firstLine, "]") {
		t.Fatalf("unexpected header: %q", firstLine)
	}

	// Edit using the exact header returned by read.
	patch := firstLine + "\nPUT 4.=4:\n+\tprintln(\"hello\")\n"
	editRes, err := executeEdit(sdk.Context{}, store, dir, patch)
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	newHeader := strings.SplitN(editRes.Content, "\n", 2)[0]
	if newHeader == firstLine {
		t.Fatalf("expected new tag after edit, still got %s", newHeader)
	}
	if !strings.Contains(editRes.Content, "4:\tprintln(\"hello\")") {
		t.Fatalf("expected new-file preview in edit response:\n%s", editRes.Content)
	}

	// Reusing the stale tag from the earlier read must be rejected with omp's recognized-hash error.
	staleRes, err := executeEdit(sdk.Context{}, store, dir, firstLine+"\nPUT 4.=4:\n+\tprintln(\"again\")\n")
	if err != nil {
		t.Fatalf("expected nil Go error with IsError:true so details survive, got err=%v", err)
	}
	if !staleRes.IsError {
		t.Fatalf("expected stale tag IsError=true")
	}
	if !strings.Contains(staleRes.Content, "Edit rejected for app.go: file changed between read and edit.") {
		t.Fatalf("unexpected stale tag error: %v", staleRes.Content)
	}

	// Invented tag must be rejected with omp's unrecognized-hash error.
	invRes, err := executeEdit(sdk.Context{}, store, dir, "[app.go#DEAD]\nPUT 4.=4:\n+\tprintln(\"nope\")\n")
	if err != nil {
		t.Fatalf("expected nil Go error with IsError:true so details survive, got err=%v", err)
	}
	if !invRes.IsError {
		t.Fatalf("expected invented tag IsError=true")
	}
	if !strings.Contains(invRes.Content, "Edit rejected for app.go: hash #DEAD is not from this session.") {
		t.Fatalf("unexpected invented tag error: %v", invRes.Content)
	}
}

// Covers CRLF preservation and no-trailing-newline preservation across read and edit.
func TestCRLFAndNoTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	store := hashline.NewStore()

	// CRLF file: edit preserves CRLF
	crlfPath := filepath.Join(dir, "win.txt")
	if err := os.WriteFile(crlfPath, []byte("one\r\ntwo\r\nthree\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r1, err := executeRead(store, dir, "win.txt")
	if err != nil {
		t.Fatal(err)
	}
	h1 := strings.SplitN(r1.Content, "\n", 2)[0]
	if _, err := executeEdit(sdk.Context{}, store, dir, h1+"\nPUT 2.=2:\n+TWO\n"); err != nil {
		t.Fatalf("edit crlf: %v", err)
	}
	gotCRLF, _ := os.ReadFile(crlfPath)
	if string(gotCRLF) != "one\r\nTWO\r\nthree\r\n" {
		t.Fatalf("CRLF not preserved: %q", string(gotCRLF))
	}

	// File without trailing newline: edit preserves absence of trailing newline
	noNLPath := filepath.Join(dir, "nonl.txt")
	if err := os.WriteFile(noNLPath, []byte("first\nsecond"), 0o644); err != nil {
		t.Fatal(err)
	}
	r2, err := executeRead(store, dir, "nonl.txt")
	if err != nil {
		t.Fatal(err)
	}
	h2 := strings.SplitN(r2.Content, "\n", 2)[0]
	if _, err := executeEdit(sdk.Context{}, store, dir, h2+"\nPUT 2.=2:\n+SECOND\n"); err != nil {
		t.Fatalf("edit nonl: %v", err)
	}
	gotNoNL, _ := os.ReadFile(noNLPath)
	if string(gotNoNL) != "first\nSECOND" {
		t.Fatalf("no-trailing-newline not preserved: %q", string(gotNoNL))
	}
}

// Covers write parent creation, MV, REM, and paths outside cwd.
func TestWriteMoveDeleteAndOutsideCwd(t *testing.T) {
	workDir := t.TempDir()
	outsideDir := t.TempDir()
	store := hashline.NewStore()

	// Write creates parent directories and returns header + byte count.
	wRes, err := executeWrite(sdk.Context{}, store, workDir, "nested/sub/hello.txt", new("a\nb\n"))
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if !strings.Contains(wRes.Content, "Successfully wrote 4 bytes to nested/sub/hello.txt") {
		t.Fatalf("unexpected write output: %s", wRes.Content)
	}
	wHeader := strings.SplitN(wRes.Content, "\n", 2)[0]

	// MV renames file after edits.
	mvRes, err := executeEdit(sdk.Context{}, store, workDir, wHeader+"\nPUT 1.=1:\n+A\nMV nested/moved.txt\n")
	if err != nil {
		t.Fatalf("mv: %v", err)
	}
	if !strings.Contains(mvRes.Content, "Moved to ") || !strings.Contains(mvRes.Content, "nested/moved.txt") {
		t.Fatalf("unexpected mv output: %s", mvRes.Content)
	}
	if _, err := os.Stat(filepath.Join(workDir, "nested/sub/hello.txt")); !os.IsNotExist(err) {
		t.Fatalf("source file should be removed after MV")
	}
	mvHeader := strings.SplitN(mvRes.Content, "\n", 2)[0]

	// REM deletes file.
	remRes, err := executeEdit(sdk.Context{}, store, workDir, mvHeader+"\nREM\n")
	if err != nil {
		t.Fatalf("rem: %v", err)
	}
	if remRes.Content != "Deleted nested/moved.txt" {
		t.Fatalf("unexpected rem output: %q", remRes.Content)
	}

	// Path outside cwd: allowed and formatted as absolute POSIX path (upstream: path-utils.ts:320,501).
	outFile := filepath.Join(outsideDir, "ext.txt")
	if err := os.WriteFile(outFile, []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outRes, err := executeRead(store, workDir, outFile)
	if err != nil {
		t.Fatalf("read outside cwd: %v", err)
	}
	wantPrefix := "[" + filepath.ToSlash(outFile) + "#"
	if !strings.HasPrefix(outRes.Content, wantPrefix) {
		t.Fatalf("expected outside-cwd header starting with %q, got:\n%s", wantPrefix, outRes.Content)
	}
}

// Covers directory listing and raster PNG image reads.
func TestReadDirectoryAndImage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("yo"), 0o644); err != nil {
		t.Fatal(err)
	}

	store := hashline.NewStore()
	dirRes, err := executeRead(store, dir, ".")
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if !strings.Contains(dirRes.Content, "- a.txt") || !strings.Contains(dirRes.Content, "- sub/") || !strings.Contains(dirRes.Content, "- b.txt") {
		t.Fatalf("unexpected dir listing:\n%s", dirRes.Content)
	}

	// Minimal valid PNG header.
	pngBytes := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00}
	if err := os.WriteFile(filepath.Join(dir, "pic.png"), pngBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	imgRes, err := executeRead(store, dir, "pic.png")
	if err != nil {
		t.Fatalf("read image: %v", err)
	}
	if len(imgRes.Images) != 1 || imgRes.Images[0].MimeType != "image/png" {
		t.Fatalf("expected 1 png image, got %+v", imgRes.Images)
	}
	decoded, err := base64.StdEncoding.DecodeString(imgRes.Images[0].Data)
	if err != nil || !bytes.Equal(decoded, pngBytes) {
		t.Fatalf("image base64 roundtrip mismatch")
	}
}

func TestRegisterTools(t *testing.T) {
	e := sdk.New("test-files")
	Register(e)
	if readDescription == "" || writeDescription == "" || editDescription == "" {
		t.Fatalf("embedded descriptions must be non-empty")
	}
	if !strings.Contains(readDescription, "PDF/DOCX/PPTX/XLSX/EPUB") {
		t.Fatalf("read.md must advertise ported document conversion:\n%s", readDescription)
	}
	if strings.Contains(readDescription, "Bare code: declarations only") {
		t.Fatalf("read.md must not advertise unported sources:\n%s", readDescription)
	}
	if strings.Contains(writeDescription, "SQLite") {
		t.Fatalf("write.md must not advertise unported SQLite writes:\n%s", writeDescription)
	}
}
