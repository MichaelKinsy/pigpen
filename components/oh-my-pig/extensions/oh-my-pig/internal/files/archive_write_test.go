package files

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/gate"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/hashline"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

func buildTestZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := io.WriteString(w, body); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write zip %s: %v", path, err)
	}
}

func buildTestTar(t *testing.T, path string, gzipped bool, files map[string]string) {
	t.Helper()
	var buf bytes.Buffer
	var out io.Writer = &buf
	var gz *gzip.Writer
	if gzipped {
		gz = gzip.NewWriter(&buf)
		out = gz
	}
	tw := tar.NewWriter(out)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg,
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(body)),
		}); err != nil {
			t.Fatalf("tar header %s: %v", name, err)
		}
		if _, err := io.WriteString(tw, body); err != nil {
			t.Fatalf("tar write %s: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if gz != nil {
		if err := gz.Close(); err != nil {
			t.Fatalf("gzip close: %v", err)
		}
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write tar %s: %v", path, err)
	}
}

func TestWriteArchiveZipAndAliases(t *testing.T) {
	dir := t.TempDir()
	store := hashline.NewStore()

	// Test both .zip and ZIP-container aliases (.jar, .whl, .vsix).
	for _, ext := range []string{".zip", ".jar", ".whl", ".vsix"} {
		archivePath := filepath.Join(dir, "pkg"+ext)
		buildTestZip(t, archivePath, map[string]string{
			"src/old.txt": "original\n",
		})

		// Add new member
		res, err := executeWrite(sdk.Context{}, store, dir, "pkg"+ext+":added.txt", new("new data\n"))
		if err != nil {
			t.Fatalf("write new member (%s): %v", ext, err)
		}
		if res.Content != "Successfully wrote 9 bytes to pkg"+ext+":added.txt" {
			t.Fatalf("unexpected result (%s): %q", ext, res.Content)
		}

		// Replace existing member
		res, err = executeWrite(sdk.Context{}, store, dir, "pkg"+ext+":src/old.txt", new("updated\n"))
		if err != nil {
			t.Fatalf("replace member (%s): %v", ext, err)
		}
		if res.Content != "Successfully wrote 8 bytes to pkg"+ext+":src/old.txt" {
			t.Fatalf("unexpected replace result (%s): %q", ext, res.Content)
		}

		// Write nested member
		_, err = executeWrite(sdk.Context{}, store, dir, "pkg"+ext+":deep/nested/leaf.txt", new("leaf\n"))
		if err != nil {
			t.Fatalf("write nested member (%s): %v", ext, err)
		}

		// Verify archive structure, modes, and deterministic timestamp
		zr, err := zip.OpenReader(archivePath)
		if err != nil {
			t.Fatalf("reopen zip (%s): %v", ext, err)
		}
		got := map[string]string{}
		for _, f := range zr.File {
			if f.Mode().Perm() != 0o644 {
				t.Errorf("%s member %s mode = %o, want 0644", ext, f.Name, f.Mode().Perm())
			}
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("open %s: %v", f.Name, err)
			}
			b, _ := io.ReadAll(rc)
			_ = rc.Close()
			got[f.Name] = string(b)
		}
		_ = zr.Close()

		want := map[string]string{
			"src/old.txt":          "updated\n",
			"added.txt":            "new data\n",
			"deep/nested/leaf.txt": "leaf\n",
		}
		if len(got) != len(want) {
			t.Fatalf("%s member count = %d, want %d (%v)", ext, len(got), len(want), got)
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s member %s = %q, want %q", ext, k, got[k], v)
			}
		}

		// Verify read tool sees listing and member text
		listRes, err := executeRead(store, dir, "pkg"+ext)
		if err != nil {
			t.Fatalf("read listing (%s): %v", ext, err)
		}
		if listRes.Content != "added.txt (9B)\ndeep/\nsrc/" {
			t.Fatalf("read listing (%s) = %q", ext, listRes.Content)
		}
		memRes, err := executeRead(store, dir, "pkg"+ext+":deep/nested/leaf.txt")
		if err != nil {
			t.Fatalf("read member (%s): %v", ext, err)
		}
		if memRes.Content != "leaf" {
			t.Fatalf("read member (%s) = %q, want %q", ext, memRes.Content, "leaf")
		}
	}
}

func TestWriteArchiveTarAndTarGz(t *testing.T) {
	dir := t.TempDir()
	store := hashline.NewStore()

	for _, tc := range []struct {
		name    string
		gzipped bool
	}{
		{"bundle.tar", false},
		{"bundle.tar.gz", true},
		{"bundle.tgz", true},
	} {
		archivePath := filepath.Join(dir, tc.name)
		buildTestTar(t, archivePath, tc.gzipped, map[string]string{
			"pkg/a.txt": "orig\n",
		})

		if _, err := executeWrite(sdk.Context{}, store, dir, tc.name+":pkg/a.txt", new("replaced\n")); err != nil {
			t.Fatalf("replace %s: %v", tc.name, err)
		}
		if _, err := executeWrite(sdk.Context{}, store, dir, tc.name+":sub/deep/b.txt", new("nested\n")); err != nil {
			t.Fatalf("add nested %s: %v", tc.name, err)
		}

		raw, err := os.ReadFile(archivePath)
		if err != nil {
			t.Fatalf("read %s: %v", tc.name, err)
		}
		var r io.Reader = bytes.NewReader(raw)
		if tc.gzipped {
			if len(raw) < 2 || raw[0] != 0x1f || raw[1] != 0x8b {
				t.Fatalf("%s lost gzip magic after rewrite: %x", tc.name, raw[:min(4, len(raw))])
			}
			gz, err := gzip.NewReader(r)
			if err != nil {
				t.Fatalf("gzip open %s: %v", tc.name, err)
			}
			defer gz.Close()
			r = gz
		}

		tr := tar.NewReader(r)
		files := map[string]string{}
		dirsSeen := map[string]int64{}
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("tar next %s: %v", tc.name, err)
			}
			if !hdr.ModTime.IsZero() && hdr.ModTime.Unix() != 0 {
				t.Errorf("%s entry %s mtime = %v, want epoch 0", tc.name, hdr.Name, hdr.ModTime)
			}
			if hdr.Typeflag == tar.TypeDir {
				dirsSeen[hdr.Name] = hdr.Mode
				continue
			}
			if hdr.Mode != 0o644 {
				t.Errorf("%s file %s mode = %o, want 0644", tc.name, hdr.Name, hdr.Mode)
			}
			b, _ := io.ReadAll(tr)
			files[hdr.Name] = string(b)
		}
		if files["pkg/a.txt"] != "replaced\n" || files["sub/deep/b.txt"] != "nested\n" {
			t.Fatalf("%s unexpected files: %+v", tc.name, files)
		}
		for _, wantDir := range []string{"pkg/", "sub/", "sub/deep/"} {
			if dirsSeen[wantDir] != 0o755 {
				t.Errorf("%s missing or bad mode for synthesized dir %s: %o", tc.name, wantDir, dirsSeen[wantDir])
			}
		}
	}
}

func TestWriteArchiveCreatesMissingAndPreservesPlainArchiveFile(t *testing.T) {
	dir := t.TempDir()
	store := hashline.NewStore()

	// Writing a member into a non-existent archive creates parent dirs + archive.
	res, err := executeWrite(sdk.Context{}, store, dir, "nested/new.zip:hello.txt", new("hi\n"))
	if err != nil {
		t.Fatalf("create missing zip: %v", err)
	}
	if res.Content != "Successfully wrote 3 bytes to nested/new.zip:hello.txt" {
		t.Fatalf("unexpected result: %q", res.Content)
	}
	mem, err := executeRead(store, dir, "nested/new.zip:hello.txt")
	if err != nil || mem.Content != "hi" {
		t.Fatalf("read back created zip: res=%+v err=%v", mem, err)
	}

	// Writing a bare archive filename (no :member) writes a plain file, matching omp tools.test.ts:2260.
	res, err = executeWrite(sdk.Context{}, store, dir, "literal.zip", new("plain bytes\n"))
	if err != nil {
		t.Fatalf("plain literal.zip write: %v", err)
	}
	if !strings.Contains(res.Content, "Successfully wrote 12 bytes to literal.zip") {
		t.Fatalf("unexpected plain write result: %q", res.Content)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "literal.zip"))
	if err != nil || string(raw) != "plain bytes\n" {
		t.Fatalf("literal.zip bytes = %q, err=%v", string(raw), err)
	}
}

func TestWriteArchiveRefusals(t *testing.T) {
	dir := t.TempDir()
	store := hashline.NewStore()

	// Read-only formats in omp -> exact omp error text.
	readOnlyCases := []struct {
		path   string
		format string
	}{
		{"a.7z:x.txt", "7z"},
		{"a.rar:x.txt", "rar"},
		{"a.iso:x.txt", "iso"},
		{"a.cab:x.txt", "cab"},
		{"a.rpm:x.txt", "rpm"},
		{"a.tar.bz2:x.txt", "tar.bz2"},
		{"a.tar.xz:x.txt", "tar.xz"},
		{"a.gz:x.txt", "gz"},
	}
	for _, tc := range readOnlyCases {
		_, err := executeWrite(sdk.Context{}, store, dir, tc.path, new("x\n"))
		want := fmt.Sprintf("Writing entries inside %s archives is not supported (read-only format).", tc.format)
		if err == nil || err.Error() != want {
			t.Errorf("write %s err = %v, want %q", tc.path, err, want)
		}
	}

	// Formats omp writes but Go stdlib cannot -> clear stdlib-port refusal.
	for _, tc := range []struct {
		path   string
		format string
	}{
		{"a.tar.zst:x.txt", "tar.zst"},
		{"a.tzst:x.txt", "tar.zst"},
		{"app.asar:x.txt", "asar"},
	} {
		_, err := executeWrite(sdk.Context{}, store, dir, tc.path, new("x\n"))
		if err == nil || !strings.Contains(err.Error(), "Writing entries inside "+tc.format+" archives is not supported") {
			t.Errorf("write %s err = %v, want stdlib refusal for %s", tc.path, err, tc.format)
		}
		if _, statErr := os.Stat(filepath.Join(dir, strings.SplitN(tc.path, ":", 2)[0])); !os.IsNotExist(statErr) {
			t.Errorf("refused format %s should not create archive on disk", tc.path)
		}
	}

	// Invalid member subpaths
	for _, tc := range []struct {
		path string
		want string
	}{
		{"a.zip:../escape.txt", "Archive path cannot contain '..'"},
		{"a.zip:dir/", "Archive write path must target a file, not a directory"},
		{"a.zip:", "Archive write path must target a file inside the archive"},
	} {
		_, err := executeWrite(sdk.Context{}, store, dir, tc.path, new("x\n"))
		if err == nil || err.Error() != tc.want {
			t.Errorf("write %s err = %v, want %q", tc.path, err, tc.want)
		}
	}

	// Directory and plain-file archive parts
	if err := os.Mkdir(filepath.Join(dir, "dir.zip"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := executeWrite(sdk.Context{}, store, dir, "dir.zip:a.txt", new("x\n")); err == nil ||
		err.Error() != "Failed to read ZIP archive: Directories cannot be read like files" {
		t.Errorf("dir.zip err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plain.zip"), []byte("not a zip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := executeWrite(sdk.Context{}, store, dir, "plain.zip:a.txt", new("x\n")); err == nil ||
		err.Error() != "Invalid ZIP archive: missing end of central directory" {
		t.Errorf("plain.zip err = %v", err)
	}
}

func TestWriteArchiveAtomicityOnFailure(t *testing.T) {
	dir := t.TempDir()
	store := hashline.NewStore()

	// 1. Corrupt existing tar.gz fails read; original bytes stay byte-identical and no temp file leaks.
	corruptPath := filepath.Join(dir, "corrupt.tar.gz")
	origCorrupt := []byte("definitely not gzip bytes")
	if err := os.WriteFile(corruptPath, origCorrupt, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := executeWrite(sdk.Context{}, store, dir, "corrupt.tar.gz:a.txt", new("hello\n")); err == nil {
		t.Fatalf("expected error writing to corrupt tar.gz")
	}
	afterCorrupt, err := os.ReadFile(corruptPath)
	if err != nil || !bytes.Equal(afterCorrupt, origCorrupt) {
		t.Fatalf("corrupt archive was modified on failed write")
	}

	// 2. Valid tar archive where member conflicts with an existing file path (file 'a' vs 'a/b.txt')
	// fails during tar encoding; original archive remains byte-identical and temp file is removed.
	tarPath := filepath.Join(dir, "atomic.tar")
	buildTestTar(t, tarPath, false, map[string]string{"a": "i am a file\n"})
	origTar, err := os.ReadFile(tarPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = executeWrite(sdk.Context{}, store, dir, "atomic.tar:a/child.txt", new("conflict\n"))
	if err == nil || !strings.Contains(err.Error(), "is not a directory") {
		t.Fatalf("expected parent-is-file tar error, got %v", err)
	}
	afterTar, err := os.ReadFile(tarPath)
	if err != nil || !bytes.Equal(afterTar, origTar) {
		t.Fatalf("original tar was modified after failed encode")
	}

	// Ensure no leftover .tmp-* files in dir.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("leftover temp file after failed write: %s", e.Name())
		}
	}
}

func TestWriteArchiveGuards(t *testing.T) {
	dir := t.TempDir()
	store := hashline.NewStore()
	zipPath := filepath.Join(dir, "guarded.zip")
	longContent := strings.Repeat("line content here\n", 20)
	buildTestZip(t, zipPath, map[string]string{
		"doc.txt": longContent,
	})

	// 1. gate.Check blocks archive write and reports the archive path.
	gate.ResetForTests()
	t.Cleanup(gate.ResetForTests)
	var checkedPath string
	gate.SetPolicy(func(_ sdk.Context, req gate.Request) error {
		checkedPath = req.Path
		return fmt.Errorf("blocked by plan mode")
	})
	_, err := executeWrite(sdk.Context{}, store, dir, "guarded.zip:doc.txt", new("new\n"))
	gate.ResetForTests()
	if err == nil || !strings.Contains(err.Error(), "blocked by plan mode") {
		t.Fatalf("expected gate denial, got %v", err)
	}
	if checkedPath != zipPath {
		t.Fatalf("gate checked path = %q, want %q", checkedPath, zipPath)
	}

	// 2. Hashline [path#TAG] header unwrapping works on archive paths.
	res, err := executeWrite(sdk.Context{}, store, dir, "[guarded.zip:wrapped.txt#ABCD]", new("wrapped\n"))
	if err != nil {
		t.Fatalf("wrapped header archive write: %v", err)
	}
	if res.Content != "Successfully wrote 8 bytes to guarded.zip:wrapped.txt" {
		t.Fatalf("unexpected wrapped result: %q", res.Content)
	}

	// 3. Hashline display-prefix auto-stripping applies to archive writes.
	copied := "[guarded.zip:stripped.txt#1234]\n1:alpha\n2:beta\n"
	res, err = executeWrite(sdk.Context{}, store, dir, "guarded.zip:stripped.txt", new(copied))
	if err != nil {
		t.Fatalf("stripped prefix archive write: %v", err)
	}
	if !strings.Contains(res.Content, "Note: auto-stripped hashline display prefixes from content before writing.") {
		t.Fatalf("expected auto-strip note, got %q", res.Content)
	}
	readBack, err := executeRead(store, dir, "guarded.zip:stripped.txt")
	if err != nil || readBack.Content != "alpha\nbeta" {
		t.Fatalf("stripped member read back = %q, err=%v", readBack.Content, err)
	}

	// 4. Truncated-read projection guard blocks overwriting existing member with shorter truncated projection.
	truncProj := "line content here\n\n[Showing lines 1-1 of 20. Use :2- to continue]"
	_, err = executeWrite(sdk.Context{}, store, dir, "guarded.zip:doc.txt", new(truncProj))
	if err == nil || !strings.Contains(err.Error(), "incomplete read projection") {
		t.Fatalf("expected truncated read projection refusal, got %v", err)
	}

	// 5. Read-selector misfire guard blocks empty write to a missing selector-shaped archive member.
	_, err = executeWrite(sdk.Context{}, store, dir, "guarded.zip:missing.txt:1-10", new(""))
	if err == nil || !strings.Contains(err.Error(), "ends with a read-tool selector") {
		t.Fatalf("expected read-selector misfire refusal, got %v", err)
	}
}

func TestWriteArchiveConcurrentSerialization(t *testing.T) {
	dir := t.TempDir()
	store := hashline.NewStore()
	tarPath := filepath.Join(dir, "concurrent.tar")
	buildTestTar(t, tarPath, false, map[string]string{
		"a.txt": "old a\n",
		"b.txt": "old b\n",
	})

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := executeWrite(sdk.Context{}, store, dir, "concurrent.tar:a.txt", new("new a\n"))
		errs <- err
	}()
	go func() {
		defer wg.Done()
		_, err := executeWrite(sdk.Context{}, store, dir, "concurrent.tar:b.txt", new("new b\n"))
		errs <- err
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent archive write failed: %v", err)
		}
	}

	ra, err := executeRead(store, dir, "concurrent.tar:a.txt")
	if err != nil || ra.Content != "new a" {
		t.Fatalf("a.txt = %q, err=%v", ra.Content, err)
	}
	rb, err := executeRead(store, dir, "concurrent.tar:b.txt")
	if err != nil || rb.Content != "new b" {
		t.Fatalf("b.txt = %q, err=%v", rb.Content, err)
	}
}

func TestWriteConflictURIPrefixRecovery(t *testing.T) {
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
	store := hashline.NewStore()
	mergePath := filepath.Join(dir, "merge.txt")
	conflictText := "top\n<<<<<<< ours\nleft\n=======\nright\n>>>>>>> theirs\nbottom\n"
	if err := os.WriteFile(mergePath, []byte(conflictText), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := executeRead(store, dir, "merge.txt:conflicts"); err != nil {
		t.Fatalf("read :conflicts: %v", err)
	}

	// Write with erroneous `<file>:` prefix before `conflict://1`.
	res, err := executeWrite(sdk.Context{}, store, dir, "merge.txt:conflict://1", new("@theirs"))
	if err != nil {
		t.Fatalf("write merge.txt:conflict://1: %v", err)
	}
	wantNote := "Note: stripped erroneous 'merge.txt:' prefix from path; conflict URIs are global (use `conflict://1`, not `<file>:conflict://1`)."
	if !strings.Contains(res.Content, wantNote) {
		t.Fatalf("expected recovery note %q in result:\n%s", wantNote, res.Content)
	}
	if !strings.Contains(res.Content, "Resolved conflict #1") {
		t.Fatalf("expected conflict resolution text in result:\n%s", res.Content)
	}
	after, err := os.ReadFile(mergePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != "top\nright\nbottom\n" {
		t.Fatalf("merge.txt after conflict recovery = %q", string(after))
	}
}

func writeTarWithHeaders(t *testing.T, archivePath string, entries []struct {
	hdr  tar.Header
	body string
}) {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		h := e.hdr
		if h.Typeflag == tar.TypeReg || h.Typeflag == 0 {
			h.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatalf("tar header %s: %v", h.Name, err)
		}
		if len(e.body) > 0 {
			if _, err := io.WriteString(tw, e.body); err != nil {
				t.Fatalf("tar write %s: %v", h.Name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := os.WriteFile(archivePath, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write tar %s: %v", archivePath, err)
	}
}

func TestWriteArchiveTarLinksAndSpecialTypes(t *testing.T) {
	dir := t.TempDir()
	store := hashline.NewStore()

	tarPath := filepath.Join(dir, "links.tar")
	writeTarWithHeaders(t, tarPath, []struct {
		hdr  tar.Header
		body string
	}{
		{hdr: tar.Header{Name: "dir/", Typeflag: tar.TypeDir, Mode: 0o755}},
		{hdr: tar.Header{Name: "empty/", Typeflag: tar.TypeDir, Mode: 0o755}},
		{hdr: tar.Header{Name: "dir/orig.txt", Typeflag: tar.TypeReg, Mode: 0o644}, body: "target text\n"},
		{hdr: tar.Header{Name: "hard.txt", Typeflag: tar.TypeLink, Linkname: "dir/orig.txt"}},
		{hdr: tar.Header{Name: "sym.txt", Typeflag: tar.TypeSymlink, Linkname: "dir/orig.txt"}},
		{hdr: tar.Header{Name: "dirlink", Typeflag: tar.TypeSymlink, Linkname: "dir"}},
		{hdr: tar.Header{Name: "pipe", Typeflag: tar.TypeFifo, Mode: 0o644}},
		{hdr: tar.Header{Name: "chardev", Typeflag: tar.TypeChar, Mode: 0o600}},
	})

	if _, err := executeWrite(sdk.Context{}, store, dir, "links.tar:extra.txt", new("added\n")); err != nil {
		t.Fatalf("rewrite tar with links: %v", err)
	}

	// Inspect rewritten tar: hard.txt and sym.txt flattened to regular files with target bytes;
	// dirlink, empty/, pipe, and chardev dropped; dir/ synthesized for dir/orig.txt.
	raw, err := os.ReadFile(tarPath)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(bytes.NewReader(raw))
	gotFiles := map[string]string{}
	gotTypes := map[string]byte{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		gotTypes[hdr.Name] = hdr.Typeflag
		if hdr.Typeflag == tar.TypeReg {
			b, _ := io.ReadAll(tr)
			gotFiles[hdr.Name] = string(b)
		}
	}
	wantFiles := map[string]string{
		"dir/orig.txt": "target text\n",
		"hard.txt":     "target text\n",
		"sym.txt":      "target text\n",
		"extra.txt":    "added\n",
	}
	if len(gotFiles) != len(wantFiles) {
		t.Fatalf("rewritten files = %+v, want %+v", gotFiles, wantFiles)
	}
	for k, v := range wantFiles {
		if gotFiles[k] != v {
			t.Errorf("file %s = %q, want %q", k, gotFiles[k], v)
		}
		if gotTypes[k] != tar.TypeReg {
			t.Errorf("file %s type = %q, want regular", k, gotTypes[k])
		}
	}
	for _, absent := range []string{"dirlink", "dirlink/", "empty/", "pipe", "chardev"} {
		if _, ok := gotTypes[absent]; ok {
			t.Errorf("unexpected entry %q preserved in rewritten tar", absent)
		}
	}
}

func TestWriteArchiveTarBrokenLinkAndSparseRefusals(t *testing.T) {
	dir := t.TempDir()
	store := hashline.NewStore()

	// 1. Dangling symlink -> exact omp error.
	dangling := filepath.Join(dir, "dangling.tar")
	writeTarWithHeaders(t, dangling, []struct {
		hdr  tar.Header
		body string
	}{
		{hdr: tar.Header{Name: "bad.txt", Typeflag: tar.TypeSymlink, Linkname: "nowhere.txt"}},
	})
	if _, err := executeWrite(sdk.Context{}, store, dir, "dangling.tar:extra.txt", new("x\n")); err == nil ||
		err.Error() != "Archive symlink 'bad.txt' cannot be materialized from target 'nowhere.txt'" {
		t.Errorf("dangling symlink err = %v", err)
	}

	// 2. Hard link targeting missing member -> exact omp error.
	missingHard := filepath.Join(dir, "missing-hard.tar")
	writeTarWithHeaders(t, missingHard, []struct {
		hdr  tar.Header
		body string
	}{
		{hdr: tar.Header{Name: "badhard.txt", Typeflag: tar.TypeLink, Linkname: "ghost.txt"}},
	})
	if _, err := executeWrite(sdk.Context{}, store, dir, "missing-hard.tar:extra.txt", new("x\n")); err == nil ||
		err.Error() != "Archive hard link 'badhard.txt' targets missing member 'ghost.txt'" {
		t.Errorf("missing hard link err = %v", err)
	}

	// 3. Hard link targeting directory -> exact omp error.
	dirHard := filepath.Join(dir, "dir-hard.tar")
	writeTarWithHeaders(t, dirHard, []struct {
		hdr  tar.Header
		body string
	}{
		{hdr: tar.Header{Name: "sub/", Typeflag: tar.TypeDir, Mode: 0o755}},
		{hdr: tar.Header{Name: "badhard.txt", Typeflag: tar.TypeLink, Linkname: "sub"}},
	})
	if _, err := executeWrite(sdk.Context{}, store, dir, "dir-hard.tar:extra.txt", new("x\n")); err == nil ||
		err.Error() != "Archive hard link 'badhard.txt' targets directory 'sub'" {
		t.Errorf("dir hard link err = %v", err)
	}

	// 4. Hard link with cyclic target -> exact omp error.
	cycHard := filepath.Join(dir, "cyc-hard.tar")
	writeTarWithHeaders(t, cycHard, []struct {
		hdr  tar.Header
		body string
	}{
		{hdr: tar.Header{Name: "self.txt", Typeflag: tar.TypeLink, Linkname: "self.txt"}},
	})
	if _, err := executeWrite(sdk.Context{}, store, dir, "cyc-hard.tar:extra.txt", new("x\n")); err == nil ||
		err.Error() != "Archive hard link 'self.txt' has a cyclic target 'self.txt'" {
		t.Errorf("cyclic hard link err = %v", err)
	}

	// 5. Old-GNU sparse ('S') tar member -> exact omp error.
	sparseTar := filepath.Join(dir, "sparse.tar")
	var sparseBuf bytes.Buffer
	hdr := make([]byte, 512)
	copy(hdr[0:], "sparse.bin")
	copy(hdr[100:], "0000644\x00")
	copy(hdr[108:], "0000000\x00")
	copy(hdr[116:], "0000000\x00")
	copy(hdr[124:], "00000000000\x00")
	copy(hdr[136:], "00000000000\x00")
	for i := 148; i < 156; i++ {
		hdr[i] = ' '
	}
	hdr[156] = 'S'
	copy(hdr[257:], "ustar  \x00")
	copy(hdr[483:], "00000000000\x00")
	var cksum int
	for _, b := range hdr {
		cksum += int(b)
	}
	copy(hdr[148:], fmt.Sprintf("%06o\x00 ", cksum))
	sparseBuf.Write(hdr)
	sparseBuf.Write(make([]byte, 1024)) // terminating zero blocks
	if err := os.WriteFile(sparseTar, sparseBuf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := executeWrite(sdk.Context{}, store, dir, "sparse.tar:extra.txt", new("x\n")); err == nil ||
		err.Error() != "Archive member 'sparse.bin' is a sparse file and cannot be read" {
		t.Errorf("sparse tar err = %v", err)
	}
}

func writeZipWithCustomEntries(t *testing.T, archivePath string, entries []struct {
	name    string
	mode    uint32 // full Unix st_mode (e.g. 0o100644 or 0o120777)
	content string
}) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		hdr := &zip.FileHeader{
			Name:           e.name,
			CreatorVersion: (zipUnixHost << 8) | 20,
			ExternalAttrs:  e.mode << 16,
			Method:         zip.Store,
		}
		fw, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatalf("zip header %s: %v", e.name, err)
		}
		if _, err := io.WriteString(fw, e.content); err != nil {
			t.Fatalf("zip write %s: %v", e.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	if err := os.WriteFile(archivePath, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write zip %s: %v", archivePath, err)
	}
}

func TestWriteArchiveZipSymlinksAndUnsafeNames(t *testing.T) {
	dir := t.TempDir()
	store := hashline.NewStore()

	// 1. Valid file symlink flattened to regular file + unsafe ../escape.txt dropped.
	zlinks := filepath.Join(dir, "zlinks.zip")
	writeZipWithCustomEntries(t, zlinks, []struct {
		name    string
		mode    uint32
		content string
	}{
		{name: "dir/orig.txt", mode: 0o100644, content: "zip target\n"},
		{name: "sym.txt", mode: 0o120777, content: "dir/orig.txt"},
		{name: "../escape.txt", mode: 0o100644, content: "dropped\n"},
	})
	if _, err := executeWrite(sdk.Context{}, store, dir, "zlinks.zip:extra.txt", new("added\n")); err != nil {
		t.Fatalf("rewrite zlinks.zip: %v", err)
	}
	zr, err := zip.OpenReader(zlinks)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range zr.File {
		if (f.ExternalAttrs>>16)&zipFileTypeMask != 0o100000 {
			t.Errorf("rewritten zip entry %s file type = %o, want regular file 0100000", f.Name, (f.ExternalAttrs>>16)&zipFileTypeMask)
		}
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		got[f.Name] = string(b)
	}
	_ = zr.Close()
	if got["dir/orig.txt"] != "zip target\n" || got["sym.txt"] != "zip target\n" || got["extra.txt"] != "added\n" || len(got) != 3 {
		t.Fatalf("rewritten zlinks.zip members = %+v", got)
	}

	// 2. ZIP symlink to directory -> refused with omp error.
	zdir := filepath.Join(dir, "zdir.zip")
	writeZipWithCustomEntries(t, zdir, []struct {
		name    string
		mode    uint32
		content string
	}{
		{name: "dir/orig.txt", mode: 0o100644, content: "zip target\n"},
		{name: "dirlink", mode: 0o120777, content: "dir"},
	})
	if _, err := executeWrite(sdk.Context{}, store, dir, "zdir.zip:extra.txt", new("x\n")); err == nil ||
		err.Error() != "Archive path 'dirlink' is a directory" {
		t.Errorf("zdir.zip err = %v", err)
	}

	// 3. ZIP dangling symlink -> refused with omp error.
	zdangling := filepath.Join(dir, "zdangling.zip")
	writeZipWithCustomEntries(t, zdangling, []struct {
		name    string
		mode    uint32
		content string
	}{
		{name: "bad.txt", mode: 0o120777, content: "nowhere.txt"},
	})
	if _, err := executeWrite(sdk.Context{}, store, dir, "zdangling.zip:extra.txt", new("x\n")); err == nil ||
		err.Error() != "Archive file 'bad.txt' not found" {
		t.Errorf("zdangling.zip err = %v", err)
	}

	// 4. ZIP escaping symlink -> refused with omp error.
	zesc := filepath.Join(dir, "zesc.zip")
	writeZipWithCustomEntries(t, zesc, []struct {
		name    string
		mode    uint32
		content string
	}{
		{name: "esc.txt", mode: 0o120777, content: "../outside.txt"},
	})
	if _, err := executeWrite(sdk.Context{}, store, dir, "zesc.zip:extra.txt", new("x\n")); err == nil ||
		err.Error() != "Archive symlink 'esc.txt' cannot be materialized from target '../outside.txt'" {
		t.Errorf("zesc.zip err = %v", err)
	}
}

func TestWriteArchivePermissionsMatchUmask(t *testing.T) {
	dir := t.TempDir()
	store := hashline.NewStore()

	// Compare rewritten archive mode against a probe file created with 0o666 under the same umask.
	probePath := filepath.Join(dir, "probe.txt")
	pf, err := os.OpenFile(probePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o666)
	if err != nil {
		t.Fatal(err)
	}
	_ = pf.Close()
	probeInfo, err := os.Stat(probePath)
	if err != nil {
		t.Fatal(err)
	}
	wantMode := probeInfo.Mode().Perm()

	existing := filepath.Join(dir, "mode.zip")
	buildTestZip(t, existing, map[string]string{"a.txt": "a\n"})
	if err := os.Chmod(existing, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := executeWrite(sdk.Context{}, store, dir, "mode.zip:b.txt", new("b\n")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(existing)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != wantMode {
		t.Fatalf("rewritten archive mode = %04o, want umask-masked %04o (not 0600)", info.Mode().Perm(), wantMode)
	}

	fresh := filepath.Join(dir, "fresh.zip")
	if _, err := executeWrite(sdk.Context{}, store, dir, "fresh.zip:a.txt", new("a\n")); err != nil {
		t.Fatal(err)
	}
	freshInfo, err := os.Stat(fresh)
	if err != nil {
		t.Fatal(err)
	}
	if freshInfo.Mode().Perm() != wantMode {
		t.Fatalf("created archive mode = %04o, want umask-masked %04o", freshInfo.Mode().Perm(), wantMode)
	}
}
