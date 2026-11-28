package files

import (
	"archive/tar"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/hashline"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

// upstream: packages/tui/src/tools/read.ts:splitPathAndSel
func TestSplitPathAndSelOneLetterAndWindowsDrive(t *testing.T) {
	cases := []struct {
		raw      string
		wantBase string
		wantSel  string
	}{
		{raw: "d:1-5", wantBase: "d", wantSel: "1-5"},
		{raw: "a:2-3", wantBase: "a", wantSel: "2-3"},
		{raw: "a:raw:2-3", wantBase: "a", wantSel: "raw:2-3"},
		{raw: "a:2-3:raw", wantBase: "a", wantSel: "2-3:raw"},
		{raw: "a:conflicts", wantBase: "a", wantSel: "conflicts"},
		{raw: `C:\foo\bar.go`, wantBase: `C:\foo\bar.go`, wantSel: ""},
		{raw: `C:\foo\bar.go:10-20`, wantBase: `C:\foo\bar.go`, wantSel: "10-20"},
		{raw: "C:/foo/bar.go", wantBase: "C:/foo/bar.go", wantSel: ""},
		{raw: "C:/foo/bar.go:10-20", wantBase: "C:/foo/bar.go", wantSel: "10-20"},
	}
	for _, tc := range cases {
		gotBase, gotSel := SplitPathAndSel(tc.raw)
		if gotBase != tc.wantBase || gotSel != tc.wantSel {
			t.Errorf("SplitPathAndSel(%q) = (%q, %q), want (%q, %q)", tc.raw, gotBase, gotSel, tc.wantBase, tc.wantSel)
		}
	}
}

// upstream: packages/utils/src/format.ts:106-122 (formatAge)
func TestFormatAgeBoundaries(t *testing.T) {
	const (
		sec  = time.Second
		min  = 60 * sec
		hour = 60 * min
		day  = 24 * hour
	)
	cases := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"negative", -5 * sec, ""},
		{"zero", 0, ""},
		{"subsecond", 500 * time.Millisecond, ""},
		{"1s", 1 * sec, "just now"},
		{"59s", 59 * sec, "just now"},
		{"60s", 60 * sec, "1m ago"},
		{"59m59s", 59*min + 59*sec, "59m ago"},
		{"1h", 1 * hour, "1h ago"},
		{"23h59m", 23*hour + 59*min, "23h ago"},
		{"1d", 1 * day, "1d ago"},
		{"6d", 6 * day, "6d ago"},
		{"7d_1w", 7 * day, "1w ago"},
		{"29d_4w", 29 * day, "4w ago"},
		{"30d_1mo", 30 * day, "1mo ago"},
		{"364d_12mo", 364 * day, "12mo ago"},
		{"365d_1y", 365 * day, "1y ago"},
		{"6y", 6 * 365 * day, "6y ago"},
		{"2500d_6y", 2500 * day, "6y ago"},
	}
	for _, tc := range cases {
		if got := formatAge(tc.d); got != tc.want {
			t.Errorf("formatAge(%s = %v) = %q, want %q", tc.name, tc.d, got, tc.want)
		}
	}
}

// upstream: packages/coding-agent/src/tools/read.ts:3195-3198,3601-3604
func TestURLBackedReadHeaderPathAndNoResourceNotes(t *testing.T) {
	urls.ResetForTests()
	t.Cleanup(urls.ResetForTests)

	cwd := t.TempDir()
	backingDir := t.TempDir()
	notePath := filepath.Join(backingDir, "note.md")
	if err := os.WriteFile(notePath, []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	histPath := filepath.Join(backingDir, "One.jsonl")
	if err := os.WriteFile(histPath, []byte("turn 1\nturn 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := urls.Register(&fakeFileHandler{scheme: "local", root: backingDir}); err != nil {
		t.Fatal(err)
	}
	if err := urls.Register(&fakeResourceHandler{
		scheme: "history",
		res: &urls.Resource{
			URL:         "history://One",
			Content:     "turn 1\nturn 2\n",
			ContentType: "text/plain",
			SourcePath:  histPath,
			Notes:       []string{"Source: live session"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := urls.Register(&fakeReadOnlyHandler{
		scheme: "agent",
		res: &urls.Resource{
			URL:     "agent://One",
			Content: "published output",
			Value:   true,
			Notes:   []string{"Source: agent registry"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	store := hashline.NewStore()

	// 1. Located file-backed URL (`local://note.md`): header names backing file path, not `local://note.md`.
	localRes, err := executeRead(store, cwd, "local://note.md")
	if err != nil {
		t.Fatalf("read local://note.md: %v", err)
	}
	firstLine, _, _ := strings.Cut(localRes.Content, "\n")
	wantPrefix := "[" + formatPathRelativeToCwd(notePath, cwd) + "#"
	if !strings.HasPrefix(firstLine, wantPrefix) {
		t.Fatalf("local:// header = %q, want prefix %q", firstLine, wantPrefix)
	}
	if strings.HasPrefix(firstLine, "[local://") {
		t.Fatalf("local:// header must not use URL scheme: %q", firstLine)
	}

	// 2. Virtual resource with SourcePath (`history://One`): header names SourcePath and omits Notes.
	histRes, err := executeRead(store, cwd, "history://One")
	if err != nil {
		t.Fatalf("read history://One: %v", err)
	}
	histFirst, _, _ := strings.Cut(histRes.Content, "\n")
	wantHistPrefix := "[" + formatPathRelativeToCwd(histPath, cwd) + "#"
	if !strings.HasPrefix(histFirst, wantHistPrefix) {
		t.Fatalf("history:// header = %q, want prefix %q", histFirst, wantHistPrefix)
	}
	if strings.Contains(histRes.Content, "Source: live session") {
		t.Fatalf("history:// read must not append resource notes:\n%s", histRes.Content)
	}

	// 3. Value resource (`agent://One`): returns content verbatim without appending Notes.
	agentRes, err := executeRead(store, cwd, "agent://One")
	if err != nil {
		t.Fatalf("read agent://One: %v", err)
	}
	if agentRes.Content != "published output" {
		t.Fatalf("agent://One content = %q, want %q", agentRes.Content, "published output")
	}
}

// upstream: packages/coding-agent/src/tools/read-archive.ts:311
func TestArchiveMissingMemberErrorFormat(t *testing.T) {
	dir := t.TempDir()
	tarPath := filepath.Join(dir, "pack.tar")
	f, err := os.Create(tarPath)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(f)
	body := []byte("present\n")
	if err := tw.WriteHeader(&tar.Header{Name: "pack/hello.txt", Mode: 0o644, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	store := hashline.NewStore()
	_, err = executeRead(store, dir, "pack.tar:pack/missing.txt")
	if err == nil {
		t.Fatalf("expected error for missing archive member")
	}
	want := "Path 'pack.tar:pack/missing.txt' not found inside archive"
	if err.Error() != want {
		t.Fatalf("missing archive member error = %q, want %q", err.Error(), want)
	}
}

// upstream: packages/coding-agent/src/tools/read.ts:3378-3419,3506-3539; conflict-protocol.ts
func TestReadRegistersConflictsAndResolvesViaConflictURL(t *testing.T) {
	urls.ResetForTests()
	urls.ResetConflictsForTests()
	t.Cleanup(func() {
		urls.ResetConflictsForTests()
		urls.ResetForTests()
	})
	if err := urls.Register(urls.ConflictHandler{}); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	sid := "sess-conflict-round3"
	t.Cleanup(func() { lifecycle.End(sid) })

	conflicted := strings.Join([]string{
		"header",
		"<<<<<<< HEAD",
		"ours-line",
		"=======",
		"theirs-line",
		">>>>>>> main",
		"middle",
		"<<<<<<< HEAD",
		"ours-2",
		"||||||| base",
		"base-2",
		"=======",
		"theirs-2",
		">>>>>>> main",
		"footer",
	}, "\n") + "\n"
	targetFile := filepath.Join(dir, "merged.txt")
	if err := os.WriteFile(targetFile, []byte(conflicted), 0o644); err != nil {
		t.Fatal(err)
	}

	ext := sdk.New("test-files-conflict-round3")
	Register(ext)
	host := startFilesHost(t, ext, dir, sid)

	// 1. Regular read registers both blocks and prints omp's warning footer with #1 and #2.
	res, errMsg := host.callTool("read", map[string]any{"path": "merged.txt"})
	if errMsg != "" || res.IsError {
		t.Fatalf("read merged.txt failed: err=%q res=%+v", errMsg, res)
	}
	for _, want := range []string{
		"⚠ 2 unresolved conflicts detected",
		"- ours = HEAD",
		"- theirs = main",
		"- base = base",
		"`conflict://<N>`",
		"──── #1  L2-6 ────",
		"──── #2  L8-14 ────",
	} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("read merged.txt missing %q in output:\n%s", want, res.Content)
		}
	}

	// 2. Reading conflict://1 and conflict://1/ours works against the registered history.
	c1, errMsg := host.callTool("read", map[string]any{"path": "conflict://1/ours"})
	if errMsg != "" || c1.IsError {
		t.Fatalf("read conflict://1/ours failed: err=%q res=%+v", errMsg, c1)
	}
	if !strings.Contains(c1.Content, "3:ours-line") {
		t.Fatalf("conflict://1/ours = %q, want line 3:ours-line", c1.Content)
	}

	// 3. Reading merged.txt:conflicts lists compact #1 and #2 summary.
	sumRes, errMsg := host.callTool("read", map[string]any{"path": "merged.txt:conflicts"})
	if errMsg != "" || sumRes.IsError {
		t.Fatalf("read merged.txt:conflicts failed: err=%q res=%+v", errMsg, sumRes)
	}
	for _, want := range []string{
		"⚠ 2 unresolved conflicts in merged.txt",
		"#1  L2-6",
		"#2  L8-14  (3-way)",
	} {
		if !strings.Contains(sumRes.Content, want) {
			t.Fatalf("merged.txt:conflicts missing %q in:\n%s", want, sumRes.Content)
		}
	}

	// 4. Writing conflict://1 splices the file on disk.
	wRes, errMsg := host.callTool("write", map[string]any{"path": "conflict://1", "content": "@theirs"})
	if errMsg != "" || wRes.IsError {
		t.Fatalf("write conflict://1 failed: err=%q res=%+v", errMsg, wRes)
	}
	afterBytes, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatal(err)
	}
	after := string(afterBytes)
	if !strings.Contains(after, "header\ntheirs-line\nmiddle\n") {
		t.Fatalf("expected conflict #1 spliced with @theirs, got:\n%s", after)
	}
	if !strings.Contains(after, "ours-2") {
		t.Fatalf("conflict #2 should remain untouched, got:\n%s", after)
	}
}
