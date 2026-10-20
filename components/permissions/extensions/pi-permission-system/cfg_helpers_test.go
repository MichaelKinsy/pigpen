package pi_permission_system

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func fptr(f float64) *float64 { return &f }
func bptr(b bool) *bool       { return &b }

// writeTestFile writes content at path, creating its directory.
func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// cfg builds a UnifiedConfig from JSON text the schema must accept.
func cfg(t *testing.T, text string) UnifiedConfig {
	t.Helper()
	r := parseUnifiedText(text, "/test.json")
	if len(r.Issues) > 0 {
		t.Fatalf("config %s: %v", text, r.Issues)
	}
	return r.Config
}

// permJSON is a config's permission map as compact JSON, "undefined" when absent.
func permJSON(p *jsObject) string {
	if p == nil {
		return "undefined"
	}
	return marshalJSON(p, "")
}

// loadedAt writes a config file with JSON text content and loads it.
func loadedAt(t *testing.T, content string) UnifiedConfigLoadResult {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	writeTestFile(t, p, content)
	return loadUnifiedConfig(p)
}

func containsAll(t *testing.T, s string, subs ...string) {
	t.Helper()
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			t.Errorf("%q does not contain %q", s, sub)
		}
	}
}

func isEmptyConfig(c UnifiedConfig) bool {
	return marshalJSON(c.toObject(), "") == "{}"
}

func sortedCopy(a []string) []string {
	out := append([]string{}, a...)
	sort.Strings(out)
	return out
}

func readTestdataFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// sameAsVendored checks a copy kept in this module against the vendored original, when the repository's port/oracle is present (a
// mutation run works in a copy of the module alone).
func sameAsVendored(t *testing.T, copyPath, vendoredRel string) {
	t.Helper()
	vendored, err := os.ReadFile(filepath.Join("..", "..", "port", "oracle", "packages", "pi-permission-system", vendoredRel))
	if err != nil {
		return
	}
	if string(vendored) != string(readTestdataFile(t, copyPath)) {
		t.Errorf("%s differs from the vendored original %s", copyPath, vendoredRel)
	}
}
