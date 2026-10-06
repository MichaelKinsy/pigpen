// SPDX-License-Identifier: MIT

package rpiv_web_tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fGitHubClone = "providers/interceptors/github"

// repoFixture builds a small clone on disk so the rendering paths have a real tree to walk.
func repoFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mkdir := func(rel string) {
		if err := os.MkdirAll(filepath.Join(root, rel), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(rel, body string) {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mkdir("src")
	mkdir("docs")
	mkdir("node_modules/pkg")
	write("README.md", "# Fixture\n\nA readme.\n")
	write("src/main.go", "package main\n")
	write("src/lib.go", "package main\n")
	write("docs/guide.md", "guide\n")
	write("logo.png", "\x89PNG\x00\x00binary")
	write("node_modules/pkg/index.js", "module.exports = {}\n")
	return root
}

func TestGitHubTreeRendering(t *testing.T) {
	root := repoFixture(t)

	tw(t, fGitHubClone, "root: skips NOISE_DIRS in tree output", func(t *testing.T) {
		tree := buildTree(root)
		if strings.Contains(tree, "node_modules/") && !strings.Contains(tree, "node_modules/  [skipped]") {
			t.Fatalf("a noise directory must be marked skipped, got:\n%s", tree)
		}
		if !strings.Contains(tree, "src/") || !strings.Contains(tree, "docs/") {
			t.Fatalf("real directories are missing, got:\n%s", tree)
		}
		if strings.Contains(tree, ".git") {
			t.Fatalf(".git must never appear, got:\n%s", tree)
		}
	})
	tw(t, fGitHubClone, "root: buildTree marks outside-repo symlink as skipped", func(t *testing.T) {
		outside := t.TempDir()
		if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("s"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "escape.txt")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		tree := buildTree(root)
		if !strings.Contains(tree, "escape.txt  [outside repo skipped]") {
			t.Fatalf("an escaping symlink must be marked, got:\n%s", tree)
		}
	})
	tw(t, fGitHubClone, "buildTree truncation at MAX_TREE_ENTRIES (>200 files)", func(t *testing.T) {
		big := t.TempDir()
		for i := 0; i < maxTreeEntries+20; i++ {
			if err := os.WriteFile(filepath.Join(big, "f"+itoa(i)), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		tree := buildTree(big)
		if !strings.Contains(tree, "... (truncated at 200 entries)") {
			t.Fatalf("the truncation line is missing, got the tail:\n%s", tree[len(tree)-120:])
		}
	})
	t.Run("refuses a directory listing that escapes the root", func(t *testing.T) {
		eq(t, buildDirListing(root, "../.."), "(path escapes repository root)", "escape notice")
	})
	t.Run("lists a directory with sizes", func(t *testing.T) {
		listing := buildDirListing(root, "src")
		if !strings.Contains(listing, "  main.go  (") || !strings.Contains(listing, "  lib.go  (") {
			t.Fatalf("files must be listed with a size, got:\n%s", listing)
		}
	})
}

func TestGitHubContentRendering(t *testing.T) {
	root := repoFixture(t)

	tw(t, fGitHubClone, "root: returns file tree + README", func(t *testing.T) {
		body := generateCloneContent(root, gitHubURLInfo{Owner: "o", Repo: "r", Type: githubURLRoot})
		if !strings.Contains(body, "Repository cloned to: "+root) {
			t.Fatalf("the clone path is announced, got:\n%s", body)
		}
		if !strings.Contains(body, "## Structure") || !strings.Contains(body, "## README.md") {
			t.Fatalf("the root view carries the tree and the readme, got:\n%s", body)
		}
		if !strings.Contains(body, "Use `read` and `bash` tools") {
			t.Fatalf("the exploration hint is missing, got:\n%s", body)
		}
	})
	tw(t, fGitHubClone, "blob: falls back to repo root when file not found in clone", func(t *testing.T) {
		body := generateCloneContent(root, gitHubURLInfo{
			Owner: "o", Repo: "r", Ref: "main", HasRef: true, Path: "nope", HasPath: true, Type: githubURLBlob,
		})
		if !strings.Contains(body, "Path `nope` not found in clone. Showing repository root instead.") {
			t.Fatalf("the not-found notice is missing, got:\n%s", body)
		}
		if !strings.Contains(body, "## Structure") {
			t.Fatalf("the root tree must be shown instead, got:\n%s", body)
		}
	})
	tw(t, fGitHubClone, "blob: returns file content", func(t *testing.T) {
		body := generateCloneContent(root, gitHubURLInfo{
			Owner: "o", Repo: "r", Ref: "main", HasRef: true, Path: "src/main.go", HasPath: true, Type: githubURLBlob,
		})
		if !strings.Contains(body, "## src/main.go") || !strings.Contains(body, "package main") {
			t.Fatalf("the file view is missing, got:\n%s", body)
		}
	})
	tw(t, fGitHubClone, "blob: returns binary message for known binary extension (.png)", func(t *testing.T) {
		body := generateCloneContent(root, gitHubURLInfo{
			Owner: "o", Repo: "r", Ref: "main", HasRef: true, Path: "logo.png", HasPath: true, Type: githubURLBlob,
		})
		if !strings.Contains(body, "Binary file (png, ") || !strings.Contains(body, "Use `read` or `bash`") {
			t.Fatalf("the binary notice is wrong, got:\n%s", body)
		}
	})
	t.Run("tree: lists the directory", func(t *testing.T) {
		body := generateCloneContent(root, gitHubURLInfo{
			Owner: "o", Repo: "r", Ref: "main", HasRef: true, Path: "docs", HasPath: true, Type: githubURLTree,
		})
		if !strings.Contains(body, "## docs") || !strings.Contains(body, "guide.md") {
			t.Fatalf("the directory view is wrong, got:\n%s", body)
		}
	})
	tw(t, fGitHubClone, "root: truncates README at 8K chars", func(t *testing.T) {
		body, ok := readReadme(root)
		if !ok || !strings.Contains(body, "# Fixture") {
			t.Fatalf("the readme must be found, got %q ok=%v", body, ok)
		}
		long := t.TempDir()
		if err := os.WriteFile(filepath.Join(long, "README.md"), []byte(strings.Repeat("x", readmeCharLimit+50)), 0o644); err != nil {
			t.Fatal(err)
		}
		body, ok = readReadme(long)
		if !ok || !strings.HasSuffix(body, "[README truncated at 8K chars]") {
			t.Fatalf("a long readme is truncated, got ok=%v", ok)
		}
	})
}

func TestGitHubAPIRendering(t *testing.T) {
	t.Run("truncates the recursive tree at 200 entries", func(t *testing.T) {
		paths := make([]string, 0, 250)
		for i := 0; i < 250; i++ {
			paths = append(paths, "src/f"+itoa(i)+".go")
		}
		rendered, ok := treeViaAPI(strings.Join(paths, "\n"))
		if !ok {
			t.Fatal("the tree must render")
		}
		if !strings.Contains(rendered, "... (250 total entries)") {
			t.Fatalf("the total-count line is missing, got the tail:\n%s", rendered[len(rendered)-60:])
		}
	})
	t.Run("returns nothing for an empty tree", func(t *testing.T) {
		if _, ok := treeViaAPI("   "); ok {
			t.Fatal("an empty listing must render as absent")
		}
	})
	t.Run("decodes a base64 README and truncates it", func(t *testing.T) {
		// "hello" base64-encoded.
		text, ok := decodeBase64Content("aGVsbG8=", 8192)
		if !ok || text != "hello" {
			t.Fatalf("decoded %q ok=%v", text, ok)
		}
		if _, ok := decodeBase64Content("", 8192); ok {
			t.Fatal("an empty payload must decode as absent")
		}
		if _, ok := decodeBase64Content("not base64!!", 8192); ok {
			t.Fatal("an undecodable payload must decode as absent")
		}
	})
	t.Run("converts the API's kilobyte size to megabytes", func(t *testing.T) {
		eq(t, repoSizeMB(350*1024), 350.0, "350 MB")
		eq(t, repoSizeMB(1024), 1.0, "1 MB")
	})
	t.Run("names a clone cache entry by owner, repo and ref", func(t *testing.T) {
		eq(t, cloneCacheKey("o", "r", "", false), "o/r", "no ref")
		eq(t, cloneCacheKey("o", "r", "main", true), "o/r@main", "with ref")
		eq(t, cloneDir("/clones", "o", "r", "", false), filepath.Join("/clones", "o", "r"), "dir without ref")
		eq(t, cloneDir("/clones", "o", "r", "main", true), filepath.Join("/clones", "o", "r@main"), "dir with ref")
	})
	tw(t, fGitHubClone, "root: formatFileSize shows bytes for tiny files and KB for medium files", func(t *testing.T) {
		eq(t, formatFileSize(512), "512 B", "bytes")
		eq(t, formatFileSize(2048), "2.0 KB", "kilobytes")
		eq(t, formatFileSize(3*1024*1024), "3.0 MB", "megabytes")
	})
	t.Run("detects a binary file by extension and by content", func(t *testing.T) {
		root := t.TempDir()
		noExt := filepath.Join(root, "blob")
		if err := os.WriteFile(noExt, []byte("a\x00b"), 0o644); err != nil {
			t.Fatal(err)
		}
		eq(t, isBinaryFile(noExt), true, "a NUL byte marks it binary")
		text := filepath.Join(root, "a.txt")
		if err := os.WriteFile(text, []byte("plain"), 0o644); err != nil {
			t.Fatal(err)
		}
		eq(t, isBinaryFile(text), false, "text is not binary")
		eq(t, isBinaryFile(filepath.Join(root, "x.png")), true, "the extension alone is enough")
	})
}
