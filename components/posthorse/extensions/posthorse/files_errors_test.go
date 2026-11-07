package posthorse

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func TestNodeFsErrorUnmappedErrnoIsUnknown(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("EDQUOT is errno 122 on Linux")
	}
	// libuv has no name for EDQUOT: Node's uvException falls back to ["UNKNOWN", "unknown error"].
	if got := nodeFsError(syscall.Errno(122), "write", "").Error(); got != "UNKNOWN: unknown error, write" {
		t.Errorf("EDQUOT = %q", got)
	}
	if got := nodeFsError(syscall.ENXIO, "open", "/p").Error(); got != "ENXIO: no such device or address, open '/p'" {
		t.Errorf("ENXIO = %q", got)
	}
	for errno, want := range map[syscall.Errno]string{
		syscall.EAGAIN: "EAGAIN: resource temporarily unavailable, read", syscall.ETXTBSY: "ETXTBSY: text file is busy, read",
		syscall.EOVERFLOW: "EOVERFLOW: value too large for defined data type, read", syscall.ENODEV: "ENODEV: no such device, read",
		syscall.EMLINK: "EMLINK: too many links, read", syscall.ENOTTY: "ENOTTY: inappropriate ioctl for device, read",
	} {
		if got := nodeFsError(errno, "read", "").Error(); got != want {
			t.Errorf("%v = %q, want %q", errno, got, want)
		}
	}
}

func TestAbortMessagesBySite(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// signal.throwIfAborted() throws the DOMException; fs operations given the signal throw the AbortError.
	if err := throwIfAborted(ctx); err == nil || err.Error() != "This operation was aborted" || !errors.Is(err, context.Canceled) {
		t.Errorf("throwIfAborted = %v", err)
	}
	if err := fsAborted(ctx); err == nil || err.Error() != "The operation was aborted" || !errors.Is(err, context.Canceled) {
		t.Errorf("fsAborted = %v", err)
	}
	if fsAborted(nil) != nil || fsAborted(context.Background()) != nil {
		t.Error("no abort, no error")
	}
}

func TestMkdirRecursive(t *testing.T) {
	dir := t.TempDir()
	deep := filepath.Join(dir, "a", "b", "c")
	if err := mkdirRecursive(deep); err != nil {
		t.Fatal(err)
	}
	if err := mkdirRecursive(deep); err != nil {
		t.Errorf("an existing directory is fine: %v", err)
	}
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := mkdirRecursive(file)
	if err == nil || err.Error() != "EEXIST: file already exists, mkdir '"+file+"'" {
		t.Errorf("a file at the path: %v", err)
	}
	err = mkdirRecursive(filepath.Join(file, "child"))
	if err == nil || !strings.HasPrefix(err.Error(), "ENOTDIR: not a directory, mkdir") {
		t.Errorf("a non-directory ancestor stays ENOTDIR: %v", err)
	}
}

func TestPackageRelativePathExcludesTheSamePath(t *testing.T) {
	if p, ok := packageRelativePath("/a/b", "/a/b"); ok {
		t.Errorf("same path: %q", p)
	}
	if p, ok := packageRelativePath("/a", "/a/b/c"); !ok || filepath.ToSlash(p) != "b/c" {
		t.Errorf("descendant: %q, %v", p, ok)
	}
}

func TestUTF8DecoderHoldsLeadShapedTails(t *testing.T) {
	var d utf8Decoder
	// F4 9F is lead-shaped (needs 4 bytes): Node holds it back whatever its validity.
	if got := d.write(nil, []byte{0xF4, 0x9F}); len(got) != 0 {
		t.Errorf("held: %q", got)
	}
	if got := string(d.end(nil)); got != "\uFFFD\uFFFD" {
		t.Errorf("end: %q", got)
	}
	// C2 F4 9F: the C2 is followed by a non-continuation, so only F4 9F is the tail.
	d = utf8Decoder{}
	if got := string(d.write(nil, []byte{0xC2, 0xF4, 0x9F})); got != "\uFFFD" {
		t.Errorf("C2 F4 9F write: %q", got)
	}
	if got := string(d.end(nil)); got != "\uFFFD\uFFFD" {
		t.Errorf("C2 F4 9F end: %q", got)
	}
	// A completed character is decoded apart from the rest.
	d = utf8Decoder{}
	_ = d.write(nil, []byte("a\xf0\x9f"))
	if got := string(d.write(nil, []byte("\x98\x80b"))); got != "😀b" {
		t.Errorf("completion: %q", got)
	}
}
