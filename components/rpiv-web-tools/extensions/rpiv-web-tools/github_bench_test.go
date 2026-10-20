package rpiv_web_tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Benchmarks of the interceptor with a canned command runner: the cost of the port's own work (URL parsing, the
// flow, rendering), without a process. `go test -run xxx -bench GitHub -benchmem`.

func benchGhAPI(_ string, args []string) (string, error) {
	if args[0] == "--version" {
		return "gh version 2", nil
	}
	switch args[len(args)-1] {
	case ".size":
		return "400000\n", nil
	case ".default_branch":
		return "main\n", nil
	case ".tree[].path":
		var sb strings.Builder
		for i := range 300 {
			fmt.Fprintf(&sb, "src/file%03d.go\n", i)
		}
		return sb.String(), nil
	}
	return ghB64(strings.Repeat("# Readme line\n", 200)), nil
}

func BenchmarkGitHubParseURL(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		if parseGitHubURL("https://github.com/owner/repo/blob/main/src/pkg/file.go") == nil {
			b.Fatal("nil")
		}
	}
}

func BenchmarkGitHubAPIView(b *testing.B) {
	b.ReportAllocs()
	fe := &fakeExec{fn: benchGhAPI}
	ctx := withExec(context.Background(), fe.run)
	g := newGitHubInterceptor(githubUserOptions{})
	for range b.N {
		if g.intercept(ctx, "https://github.com/owner/repo", false) == nil {
			b.Fatal("nil")
		}
		fe.mu.Lock()
		fe.calls = fe.calls[:0]
		fe.mu.Unlock()
	}
}

func benchClone(b *testing.B) (*githubInterceptor, string) {
	dir := b.TempDir()
	for i := range 100 {
		p := filepath.Join(dir, fmt.Sprintf("d%02d", i%10), fmt.Sprintf("f%03d.go", i))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("package x\n"), 0o644)
	}
	os.WriteFile(filepath.Join(dir, "README.md"), []byte(strings.Repeat("# Readme line\n", 200)), 0o644)
	os.WriteFile(filepath.Join(dir, "big.txt"), []byte(strings.Repeat("line of text\n", 10000)), 0o644)
	g := newGitHubInterceptor(githubUserOptions{})
	res := dir
	g.seedCache("owner/repo", dir, &res)
	g.seedCache("owner/repo@main", dir, &res)
	return g, dir
}

func BenchmarkGitHubCloneRoot(b *testing.B) {
	b.ReportAllocs()
	g, _ := benchClone(b)
	ctx := context.Background()
	for range b.N {
		if g.intercept(ctx, "https://github.com/owner/repo", false) == nil {
			b.Fatal("nil")
		}
	}
}

func BenchmarkGitHubCloneBlob(b *testing.B) {
	b.ReportAllocs()
	g, _ := benchClone(b)
	ctx := context.Background()
	for range b.N {
		if g.intercept(ctx, "https://github.com/owner/repo/blob/main/big.txt", false) == nil {
			b.Fatal("nil")
		}
	}
}
