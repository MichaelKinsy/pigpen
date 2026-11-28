package files

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseArchivePathCandidatesExported(t *testing.T) {
	cands := ParseArchivePathCandidates("bundle.zip:src/foo.ts")
	if len(cands) != 1 {
		t.Fatalf("got %d candidates, want 1: %+v", len(cands), cands)
	}
	if cands[0].ArchivePath != "bundle.zip" || cands[0].SubPath != "src/foo.ts" {
		t.Fatalf("unexpected candidate: %+v", cands[0])
	}

	tarGz := ParseArchivePathCandidates("release.tar.gz:notes.md")
	if len(tarGz) == 0 || tarGz[0].ArchivePath != "release.tar.gz" || tarGz[0].SubPath != "notes.md" {
		t.Fatalf("unexpected tar.gz candidate: %+v", tarGz)
	}

	jar := ParseArchivePathCandidates("lib.jar:META-INF/MANIFEST.MF")
	if len(jar) != 1 || jar[0].ArchivePath != "lib.jar" || jar[0].SubPath != "META-INF/MANIFEST.MF" {
		t.Fatalf("unexpected jar candidate: %+v", jar)
	}

	if none := ParseArchivePathCandidates("plain.txt"); len(none) != 0 {
		t.Fatalf("expected no candidates for plain.txt, got %+v", none)
	}
}

func TestReadArchiveMemberZipAndTarGz(t *testing.T) {
	dir := t.TempDir()

	// Create ZIP archive.
	zipPath := filepath.Join(dir, "bundle.zip")
	zf, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	w, err := zw.Create("src/foo.ts")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("const alpha = 1;\n"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	_ = zf.Close()

	data, openErr, readErr := ReadArchiveMember(zipPath, "src/foo.ts")
	if openErr != nil || readErr != nil {
		t.Fatalf("unexpected errors: openErr=%v readErr=%v", openErr, readErr)
	}
	if string(data) != "const alpha = 1;\n" {
		t.Fatalf("got %q, want %q", string(data), "const alpha = 1;\n")
	}

	// Missing member in ZIP -> readErr.
	_, openErr, readErr = ReadArchiveMember(zipPath, "missing.txt")
	if openErr != nil || readErr == nil || !strings.Contains(readErr.Error(), "Archive file 'missing.txt' not found") {
		t.Fatalf("expected member-not-found readErr, got openErr=%v readErr=%v", openErr, readErr)
	}

	// Missing ZIP archive -> openErr.
	_, openErr, readErr = ReadArchiveMember(filepath.Join(dir, "nope.zip"), "a.txt")
	if openErr == nil || readErr != nil || !strings.Contains(openErr.Error(), "Invalid ZIP archive") {
		t.Fatalf("expected openErr for missing zip, got openErr=%v readErr=%v", openErr, readErr)
	}

	// Create TAR.GZ archive.
	tgzPath := filepath.Join(dir, "release.tar.gz")
	tf, err := os.Create(tgzPath)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(tf)
	tw := tar.NewWriter(gz)
	body := []byte("alpha feature\n")
	if err := tw.WriteHeader(&tar.Header{Name: "notes.md", Mode: 0o644, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gz.Close()
	_ = tf.Close()

	data, openErr, readErr = ReadArchiveMember(tgzPath, "notes.md")
	if openErr != nil || readErr != nil || string(data) != "alpha feature\n" {
		t.Fatalf("tar.gz read failed: data=%q openErr=%v readErr=%v", string(data), openErr, readErr)
	}

	// Unsupported stdlib format (.7z).
	sevenZ := filepath.Join(dir, "a.7z")
	if err := os.WriteFile(sevenZ, []byte("7z"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, openErr, readErr = ReadArchiveMember(sevenZ, "x.txt")
	if openErr == nil || readErr != nil || !strings.Contains(openErr.Error(), "Reading 7z archives is not supported") {
		t.Fatalf("expected unsupported format openErr, got openErr=%v readErr=%v", openErr, readErr)
	}
}
