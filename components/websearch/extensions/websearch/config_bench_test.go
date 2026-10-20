package websearch

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A representative web-search.json read through ReadConfigRoot, the call every tool and provider makes.
//
//	go test -run xxx -bench ReadConfigRoot -benchmem
func BenchmarkReadConfigRoot(b *testing.B) {
	dir := b.TempDir()
	b.Setenv("PIG_CODING_AGENT_DIR", dir)
	ResetCaches()
	b.Cleanup(ResetCaches)
	path := filepath.Join(dir, configFileName)
	cfg := `{"provider":"auto","serperApiKey":"k","tavilyApiKey":"t","fetch":{"timeoutMs":30000},"fetchRouting":{"order":["jina"]},"searchRouting":{"order":["exa","brave"]},"ssrf":{"allowedHosts":["example.com"]},"webSearch":{"allowedProviders":["exa","brave","tavily"]}}`
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		b.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(path, old, old); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := ReadConfigRoot(); err != nil {
			b.Fatal(err)
		}
	}
}
