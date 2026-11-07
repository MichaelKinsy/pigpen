package posthorse

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func fsTemp(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func fsWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNodePathHelpers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX path semantics")
	}
	for _, tc := range []struct{ in, dir, base string }{
		{"/a/b/c", "/a/b", "c"}, {"/a/b/", "/a", "b"}, {"/a", "/", "a"}, {"/", "/", ""}, {"a", ".", "a"}, {"", ".", ""},
		{"/a/../b", "/a/..", "b"}, {"a/b//", "a", "b"}, {"//a", "//", "a"}, {"/a/b/..", "/a/b", ".."},
	} {
		if got := nodeDirname(tc.in); got != tc.dir {
			t.Errorf("nodeDirname(%q) = %q, want %q", tc.in, got, tc.dir)
		}
		if got := nodeBasename(tc.in); got != tc.base {
			t.Errorf("nodeBasename(%q) = %q, want %q", tc.in, got, tc.base)
		}
	}
}

func TestGitdirOf(t *testing.T) {
	for _, tc := range []struct {
		name, in string
		want     string
		ok       bool
	}{
		{"plain", "gitdir: /x/.git/worktrees/wt\n", "/x/.git/worktrees/wt", true},
		{"no space", "gitdir:/x", "/x", true},
		{"trailing whitespace trimmed", "gitdir:   /x \t \n", "/x", true},
		{"CRLF", "gitdir: /x\r\n", "/x", true},
		{"second line", "something\ngitdir: /y\n", "/y", true},
		{"CR only separates lines", "junk\rgitdir: /z\r", "/z", true},
		{"U+2028 separates lines", "junk\u2028gitdir: /w", "/w", true},
		{"whitespace may cross a line", "gitdir:\n/foo\n", "/foo", true},
		{"only whitespace backtracks one character", "gitdir:   ", " ", true},
		{"not at a line start", "x gitdir: /no", "", false},
		{"case sensitive", "GITDIR: /no", "", false},
		{"empty", "", "", false},
		{"inner spaces kept", "gitdir: /a b/c", "/a b/c", true},
		{"first directive wins", "gitdir: /one\ngitdir: /two", "/one", true},
	} {
		got, ok := gitdirOf(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: gitdirOf(%q) = %q,%v want %q,%v", tc.name, tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func collectLines(t *testing.T, content string, hwm int) []string {
	t.Helper()
	path := filepath.Join(fsTemp(t), "f")
	fsWrite(t, path, []byte(content))
	var lines []string
	for line, err := range lfLines(nil, path, hwm) {
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(line))
	}
	return lines
}

func TestLfLines(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		want          []string
	}{
		{"empty file", "", nil},
		{"one unterminated line", "abc", []string{"abc"}},
		{"terminated", "a\nb\n", []string{"a", "b"}},
		{"unterminated tail", "a\nb", []string{"a", "b"}},
		{"empty lines", "\n\nx\n", []string{"", "", "x"}},
		{"CRLF stays in the line", "a\r\nb\r\n", []string{"a\r", "b\r"}},
		{"a lone LF", "\n", []string{""}},
		{"multi-byte across chunks", "héllo wörld 😀\nsecond", []string{"héllo wörld 😀", "second"}},
	} {
		for _, hwm := range []int{1, 2, 3, 5, 64 * 1024} {
			got := collectLines(t, tc.content, hwm)
			if strings.Join(got, "|") != strings.Join(tc.want, "|") || len(got) != len(tc.want) {
				t.Errorf("%s (hwm %d): %q, want %q", tc.name, hwm, got, tc.want)
			}
		}
	}
}

func TestLfChunksMarkTerminatedSegments(t *testing.T) {
	path := filepath.Join(fsTemp(t), "f")
	fsWrite(t, path, []byte("ab\ncd"))
	var got []string
	for chunk, err := range lfChunks(nil, path, 4) {
		if err != nil {
			t.Fatal(err)
		}
		mark := "+"
		if chunk.end {
			mark = "$"
		}
		got = append(got, string(chunk.bytes)+mark)
	}
	// Reads of 4 bytes: "ab\nc" -> "ab$", "c+"; then "d" -> "d+"; the unterminated line closes at EOF with an empty end.
	if want := "ab$ c+ d+ $"; strings.Join(got, " ") != want {
		t.Errorf("chunks = %q, want %q", strings.Join(got, " "), want)
	}
}

func TestLfChunksReuseRecyclesTheBuffer(t *testing.T) {
	path := filepath.Join(fsTemp(t), "f")
	fsWrite(t, path, bytes.Repeat([]byte("x"), 10))
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var first []byte
	for chunk, err := range lfChunksFile(nil, f, path, 4, true) {
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = chunk.bytes
			continue
		}
		if &first[0] != &chunk.bytes[0] {
			t.Fatal("with reuse every read lands in the same buffer")
		}
		break
	}
}

func TestLfLinesErrors(t *testing.T) {
	missing := filepath.Join(fsTemp(t), "missing")
	for _, err := range lfLines(nil, missing, 0) {
		if err == nil || !strings.HasPrefix(err.Error(), "ENOENT: no such file or directory, open '"+missing+"'") {
			t.Errorf("open error = %v", err)
		}
	}
	path := filepath.Join(fsTemp(t), "f")
	fsWrite(t, path, []byte("a\nb\n"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	seen := 0
	for _, err := range lfLines(ctx, path, 0) {
		seen++
		var abort *abortError
		if !errors.As(err, &abort) || !errors.Is(err, context.Canceled) || err.Error() != "The operation was aborted" {
			t.Errorf("an aborted read throws the AbortError: %v", err)
		}
	}
	if seen != 1 {
		t.Errorf("the abort is yielded once, got %d", seen)
	}
}

func TestThrowIfAbortedKeepsACustomReason(t *testing.T) {
	if throwIfAborted(nil) != nil || throwIfAborted(context.Background()) != nil {
		t.Error("no signal, no abort")
	}
	reason := errors.New("because")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(reason)
	if err := throwIfAborted(ctx); err != reason {
		t.Errorf("a custom reason is thrown itself (error === reason): %v", err)
	}
}

func TestReadTextAndIsBinaryFile(t *testing.T) {
	dir := fsTemp(t)
	write := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		fsWrite(t, p, data)
		return p
	}
	text := write("text", []byte("héllo\n\xff\xe2\x82 end"))
	got, ok, err := readText(nil, text)
	if err != nil || !ok || got != "héllo\n\uFFFD\uFFFD end" {
		t.Errorf("readText = %q, %v, %v", got, ok, err)
	}
	if bin, err := isBinaryFile(text); err != nil || bin {
		t.Errorf("isBinaryFile(text) = %v, %v", bin, err)
	}
	early := write("early", append(bytes.Repeat([]byte("a"), 7999), 0))
	if _, ok, _ := readText(nil, early); ok {
		t.Error("a NUL inside the first 8,000 bytes marks binary content")
	}
	if bin, _ := isBinaryFile(early); !bin {
		t.Error("isBinaryFile agrees")
	}
	late := write("late", append(bytes.Repeat([]byte("a"), 8000), 0))
	if got, ok, _ := readText(nil, late); !ok || len(got) != 8001 {
		t.Errorf("a NUL after byte 8,000 is text: ok=%v len=%d", ok, len(got))
	}
	if bin, _ := isBinaryFile(late); bin {
		t.Error("isBinaryFile agrees")
	}
	bom := write("bom", []byte("\xef\xbb\xbfx"))
	if got, _, _ := readText(nil, bom); got != "\uFEFFx" {
		t.Errorf("a BOM is kept: %q", got)
	}
	if _, _, err := readText(nil, filepath.Join(dir, "none")); err == nil || !strings.Contains(err.Error(), "ENOENT") {
		t.Errorf("missing file: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := readText(ctx, text); !errors.Is(err, context.Canceled) {
		t.Errorf("aborted read: %v", err)
	}
}

func TestNodeFsError(t *testing.T) {
	_, err := os.Stat("/definitely/not/here")
	got := nodeFsError(err, "stat", "/definitely/not/here")
	var fsErr *fsError
	if !errors.As(got, &fsErr) || fsErr.Code != "ENOENT" || got.Error() != "ENOENT: no such file or directory, stat '/definitely/not/here'" {
		t.Errorf("nodeFsError = %v", got)
	}
	if !errors.Is(got, os.ErrNotExist) {
		t.Error("the Go error stays reachable")
	}
	if got := nodeFsErrorDest(syscall.EXDEV, "rename", "/a", "/b"); got.Error() != "EXDEV: cross-device link not permitted, rename '/a' -> '/b'" {
		t.Errorf("two-path error = %v", got)
	}
	if got := nodeFsError(syscall.EFBIG, "write", ""); got.Error() != "EFBIG: file too large, write" {
		t.Errorf("fd error without a path = %v", got)
	}
	plain := errors.New("other")
	if nodeFsError(plain, "open", "/x") != plain || nodeFsError(nil, "open", "/x") != nil {
		t.Error("non file system errors pass through")
	}
}

func TestResolveFileTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink privileges")
	}
	dir := fsTemp(t)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(dir, "real", "sub"), 0o755))
	fsWrite(t, filepath.Join(dir, "real", "note.md"), []byte("x"))
	must(os.Symlink(filepath.Join(dir, "real"), filepath.Join(dir, "alias")))
	must(os.Symlink("note.md", filepath.Join(dir, "real", "link.md")))
	must(os.Symlink("sub/../missing.md", filepath.Join(dir, "real", "dotdot.md")))
	must(os.Symlink("loop-b", filepath.Join(dir, "real", "loop-a")))
	must(os.Symlink("loop-a", filepath.Join(dir, "real", "loop-b")))
	for _, tc := range []struct{ in, want string }{
		{filepath.Join(dir, "alias", "note.md"), filepath.Join(dir, "real", "note.md")},
		{filepath.Join(dir, "alias", "link.md"), filepath.Join(dir, "real", "note.md")},
		{filepath.Join(dir, "alias", "new.md"), filepath.Join(dir, "real", "new.md")},
		{filepath.Join(dir, "alias") + "/", filepath.Join(dir, "real")},
		// A dangling link resolves to its target through the link's directory, ".." after "sub".
		{filepath.Join(dir, "real", "dotdot.md"), filepath.Join(dir, "real", "missing.md")},
	} {
		got, err := resolveFileTarget(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("resolveFileTarget(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
	if _, err := resolveFileTarget(filepath.Join(dir, "real", "loop-a")); err == nil || err.Error() != "Symlink cycle: "+filepath.Join(dir, "real", "loop-a") {
		t.Errorf("cycle: %v", err)
	}
	if _, err := resolveFileTarget(filepath.Join(dir, "missing") + "/"); err == nil || !strings.HasPrefix(err.Error(), "ENOENT: no such file or directory, stat") {
		t.Errorf("a trailing separator needs an existing directory: %v", err)
	}
	if _, err := resolveFileTarget(filepath.Join(dir, "real", "note.md", "child")); err == nil || err.Error() != "Not a directory: "+filepath.Join(dir, "real", "note.md") {
		t.Errorf("a file as parent: %v", err)
	}
}

func TestPublishFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions and links")
	}
	dir := fsTemp(t)
	target := filepath.Join(dir, "note.md")
	if err := publishFile(nil, target, "first"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(target); string(got) != "first" {
		t.Errorf("content = %q", got)
	}
	// Replacement keeps the mode of the replaced note.
	if err := os.Chmod(target, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := publishFile(nil, target, "second"); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(target)
	if got, _ := os.ReadFile(target); string(got) != "second" || info.Mode().Perm() != 0o640 {
		t.Errorf("replacement: %q, mode %v", got, info.Mode().Perm())
	}
	// A link is followed: the note behind it is replaced and the link stays.
	link := filepath.Join(dir, "link.md")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := publishFile(nil, link, "through link"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(target); string(got) != "through link" {
		t.Errorf("through link: %q", got)
	}
	if li, _ := os.Lstat(link); li.Mode()&os.ModeSymlink == 0 {
		t.Error("the link survives")
	}
	// A lone surrogate is written as U+FFFD, as Node encodes it.
	if err := publishFile(nil, target, "a"+u16string([]uint16{0xD83D})+"b"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(target); string(got) != "a\uFFFDb" {
		t.Errorf("lone surrogate: %q", got)
	}
	// A directory is refused and nothing is left behind.
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	err := publishFile(nil, sub, "x")
	var fsErr *fsError
	if !errors.As(err, &fsErr) || fsErr.Code != "EISDIR" || err.Error() != "Cannot publish to a non-regular file: "+sub {
		t.Errorf("directory target: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".posthorse-") {
			t.Errorf("temporary file left behind: %s", e.Name())
		}
	}
	// An aborted publication changes nothing.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := publishFile(ctx, target, "never"); !errors.Is(err, context.Canceled) {
		t.Errorf("aborted publish: %v", err)
	}
	if got, _ := os.ReadFile(target); string(got) == "never" {
		t.Error("an aborted publication must not write")
	}
}

func TestRandomUUIDShape(t *testing.T) {
	id, err := randomUUID()
	if err != nil || len(id) != 36 || id[8] != '-' || id[13] != '-' || id[14] != '4' || id[18] != '-' || strings.ContainsAny(id[19:20], "01234567") {
		t.Errorf("randomUUID = %q, %v", id, err)
	}
}

func BenchmarkLfLines(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "f")
	line := strings.Repeat("x", 200) + "\n"
	if err := os.WriteFile(path, []byte(strings.Repeat(line, 50_000)), 0o644); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(line) * 50_000))
	b.ReportAllocs()
	for range b.N {
		n := 0
		for _, err := range lfLines(nil, path, 0) {
			if err != nil {
				b.Fatal(err)
			}
			n++
		}
	}
}

func BenchmarkIndexLF(b *testing.B) {
	data := bytes.Repeat([]byte("x"), 1<<20)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for range b.N {
		_ = indexLF(data)
	}
}
