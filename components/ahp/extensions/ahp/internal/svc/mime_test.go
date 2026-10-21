package svc

import (
	"sort"
	"testing"
)

// The answers of mime@4.1.1's getType (checked by running it), including its quirks: a bare file
// name is an extension, a dotted-only name is not, and many common source extensions have no type.
func TestMimeTypeIsGetType(t *testing.T) {
	for path, want := range map[string]string{
		"x.md": "text/markdown", "a/b/X.JSON": "application/json", "x.ts": "video/mp2t", "x.js": "text/javascript",
		"x.png": "image/png", "x.svg": "image/svg+xml", "x.c": "text/x-c", "x.rs": "application/rls-services+xml",
		"x.mjs": "text/javascript", "x.tar.gz": "application/gzip", "txt": "text/plain", "a/b.c/d.txt": "text/plain",
		"C:\\dir\\x.html": "text/html", "x.yaml": "text/yaml", "x.sh": "application/x-sh", "x.jsx": "text/jsx",
	} {
		if got, ok := MimeType(path); !ok || got != want {
			t.Errorf("MimeType(%q) = %q, %v; want %q", path, got, ok, want)
		}
	}
	for _, path := range []string{"a/txt", "a/md", "x.go", "x.py", "x.tsx", "x.rb", "a/Makefile", "a/.gitignore", "a.b/c", "x.", "", "x.unknownext"} {
		if got, ok := MimeType(path); ok {
			t.Errorf("MimeType(%q) = %q, want none", path, got)
		}
	}
	// A bare name is looked up as an extension; a leading-dot name only when it has no directory.
	if got, ok := MimeType("md"); !ok || got != "text/markdown" {
		t.Errorf("a bare name is an extension: %q, %v", got, ok)
	}
	if got := contentTypeOf("x.go", true); got != "text/plain" {
		t.Errorf("a text file without a type falls back to text/plain, got %q", got)
	}
	if got := contentTypeOf("x.go", false); got != "" {
		t.Errorf("without the fallback there is no type, got %q", got)
	}
}

// The table is sorted (binary search) and has no duplicates.
func TestMimeTableIsSorted(t *testing.T) {
	if !sort.SliceIsSorted(mimeTable[:], func(i, j int) bool { return mimeTable[i].ext < mimeTable[j].ext }) {
		t.Fatal("mimeTable is not sorted by extension")
	}
	for i := 1; i < len(mimeTable); i++ {
		if mimeTable[i].ext == mimeTable[i-1].ext {
			t.Fatalf("duplicate extension %q", mimeTable[i].ext)
		}
	}
	if len(mimeTable) != 1240 {
		t.Fatalf("mime@4.1.1 has 1240 extensions, table has %d", len(mimeTable))
	}
}
