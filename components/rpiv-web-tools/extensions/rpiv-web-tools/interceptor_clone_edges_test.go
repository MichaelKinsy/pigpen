// SPDX-License-Identifier: MIT

package rpiv_web_tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The clone-rendering cases that need a filesystem shape of their own. upstream:
// providers/interceptors/github.test.ts and the generateCloneContent branches they drive.

func TestCloneRenderingEdges(t *testing.T) {
	tw(t, fGH, "root: works without a README", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		body := generateCloneContent(root, gitHubURLInfo{Owner: "o", Repo: "r", Type: githubURLRoot})
		if !strings.Contains(body, "## Structure") {
			t.Fatalf("the tree is still rendered, got:\n%s", body)
		}
		if strings.Contains(body, "## README.md") {
			t.Fatalf("no README section without one, got:\n%s", body)
		}
	})
	tw(t, fGH, "blob: returns binary message for file with null bytes", func(t *testing.T) {
		root := t.TempDir()
		// No binary extension, so only the content check can catch it.
		if err := os.WriteFile(filepath.Join(root, "blob.dat"), []byte("a\x00b"), 0o644); err != nil {
			t.Fatal(err)
		}
		body := generateCloneContent(root, gitHubURLInfo{
			Owner: "o", Repo: "r", Ref: "main", HasRef: true, Path: "blob.dat", HasPath: true, Type: githubURLBlob,
		})
		if !strings.Contains(body, "Binary file (dat,") {
			t.Fatalf("a NUL byte must mark it binary, got:\n%s", body)
		}
		if strings.Contains(body, "a\x00b") {
			t.Fatalf("binary bytes must not be printed, got:\n%s", body)
		}
	})
	tw(t, fGH, "blob: handles unreadable file (readFileSync catch path) via chmod 000", func(t *testing.T) {
		root := t.TempDir()
		secret := filepath.Join(root, "locked.txt")
		if err := os.WriteFile(secret, []byte("hidden"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(secret, 0o000); err != nil {
			t.Skipf("chmod is unavailable here: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(secret, 0o644) })
		if _, err := os.ReadFile(secret); err == nil {
			t.Skip("this user can read a 000 file, so the unreadable path cannot be exercised")
		}
		body := generateCloneContent(root, gitHubURLInfo{
			Owner: "o", Repo: "r", Ref: "main", HasRef: true, Path: "locked.txt", HasPath: true, Type: githubURLBlob,
		})
		if !strings.Contains(body, "Could not read `locked.txt` as UTF-8 text.") {
			t.Fatalf("the unreadable notice is missing, got:\n%s", body)
		}
	})
	t.Run("blob: a directory path renders as a listing", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "pkg", "a.go"), []byte("package pkg\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		body := generateCloneContent(root, gitHubURLInfo{
			Owner: "o", Repo: "r", Ref: "main", HasRef: true, Path: "pkg", HasPath: true, Type: githubURLBlob,
		})
		if !strings.Contains(body, "a.go") {
			t.Fatalf("a directory path must list its files, got:\n%s", body)
		}
	})
	t.Run("a path that escapes the clone is refused before it is read", func(t *testing.T) {
		root := t.TempDir()
		if _, ok := resolveWithinRepo(root, "../outside.txt"); ok {
			t.Fatal("a traversing path must be refused")
		}
		body := generateCloneContent(root, gitHubURLInfo{
			Owner: "o", Repo: "r", Ref: "main", HasRef: true, Path: "../outside.txt", HasPath: true, Type: githubURLBlob,
		})
		if !strings.Contains(body, "not found in clone") {
			t.Fatalf("an escaping path takes the fallback, got:\n%s", body)
		}
	})
}
