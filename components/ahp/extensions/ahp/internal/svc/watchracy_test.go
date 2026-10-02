package svc

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A rewrite of equal size inside the file system's timestamp tick leaves the stat signature
// unchanged; the poller must then tell the two contents apart (git's racy-timestamp problem).
func TestRacyRewriteIsDetectedByContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(path, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	first := entryOf(info, path, "f.txt", nil, now)
	if first.hash == "" {
		t.Fatal("a file modified just now must carry a content digest")
	}

	// Same stat signature, new bytes: what a rewrite inside one clock tick looks like.
	if err := os.WriteFile(path, []byte("after!"), 0o600); err != nil {
		t.Fatal(err)
	}
	info2, _ := os.Lstat(path)
	second := entryOf(info2, path, "f.txt", map[string]watchEntry{"f.txt": {sig: etagOf(info2), hash: first.hash}}, now)
	if !second.changed(watchEntry{sig: etagOf(info2), hash: first.hash}) {
		t.Fatal("equal stat but different content was not reported as changed")
	}
	third := entryOf(info2, path, "f.txt", map[string]watchEntry{"f.txt": second}, now)
	if third.changed(second) {
		t.Fatal("unchanged content reported as changed")
	}

	// An old file is not hashed unless its previous scan was: no cost for settled trees.
	old := now.Add(2 * racyWindow)
	settled := entryOf(info2, path, "f.txt", nil, old)
	if settled.hash != "" || settled.cmp != "" {
		t.Fatal("a settled file must not be hashed")
	}
	// The scan after the window still compares once, then drops the digest.
	last := entryOf(info2, path, "f.txt", map[string]watchEntry{"f.txt": second}, old)
	if last.cmp == "" || last.hash != "" {
		t.Fatalf("closing scan: cmp=%q hash=%q", last.cmp, last.hash)
	}
	// Large files are compared by stat only.
	big := filepath.Join(filepath.Dir(path), "big")
	if err := os.WriteFile(big, make([]byte, racyMaxSize+1), 0o600); err != nil {
		t.Fatal(err)
	}
	bi, _ := os.Lstat(big)
	if e := entryOf(bi, big, "big", nil, time.Now()); e.hash != "" || e.cmp != "" {
		t.Fatal("a large file must not be hashed")
	}
}
