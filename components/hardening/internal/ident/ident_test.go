package ident

import (
	"strings"
	"testing"
)

func TestPackage(t *testing.T) {
	for _, ok := range []string{"a", "websearch", "pig-doctor", "a2a", strings.Repeat("a", 32)} {
		if !Package(ok) {
			t.Errorf("Package(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", "A", "1a", "-a", "a_b", "a b", "a/b", "a\n", "a.b", strings.Repeat("a", 33), "é"} {
		if Package(bad) {
			t.Errorf("Package(%q) = true", bad)
		}
	}
}

func TestTool(t *testing.T) {
	for _, ok := range []string{"bash", "web_search", "a.b-c", "A1", strings.Repeat("x", 64)} {
		if !Tool(ok) {
			t.Errorf("Tool(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", "a b", "a/b", "a:b", "a\"b", "a\nb", "a\x00", strings.Repeat("x", 65), "é", "a,b"} {
		if Tool(bad) {
			t.Errorf("Tool(%q) = true", bad)
		}
	}
}
