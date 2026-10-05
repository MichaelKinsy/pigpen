package svc

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
	ahptypes "github.com/microsoft/agent-host-protocol/clients/go/ahptypes"
)

// Twins of upstream test/resource-watch-policy.test.ts: deterministic, no OS events or clocks.

const (
	cAdded   = ahptypes.ResourceChangeTypeAdded
	cUpdated = ahptypes.ResourceChangeTypeUpdated
	cDeleted = ahptypes.ResourceChangeTypeDeleted
)

func TestWatchPathScopeAndFilters(t *testing.T) {
	twin.Run(t, "resource-watch-policy", "normalizes descendants and rejects siblings and prefix lookalikes", func(t *testing.T) {
		root, _ := filepath.Abs("workspace")
		if rel, ok := RelativeWatchPath(root, root); !ok || rel != "" {
			t.Fatalf("root: %q %v", rel, ok)
		}
		if rel, ok := RelativeWatchPath(root, filepath.Join(root, "nested/../file.txt")); !ok || rel != "file.txt" {
			t.Fatalf("%q %v", rel, ok)
		}
		if rel, ok := RelativeWatchPath(root, filepath.Join(root, "nested/file.txt")); !ok || rel != "nested/file.txt" {
			t.Fatalf("%q %v", rel, ok)
		}
		for _, path := range []string{filepath.Join(root, ".."), filepath.Join(root, "../sibling/file.txt"), root + "-other/file.txt"} {
			if rel, ok := RelativeWatchPath(root, path); ok {
				t.Errorf("%s must be out of scope, got %q", path, rel)
			}
		}
	})

	twin.Run(t, "resource-watch-policy", "keeps the watched root visible regardless of filters", func(t *testing.T) {
		if IsExcluded("", []string{"**"}) {
			t.Error("the root must never be excluded")
		}
		if !MatchesPatterns("", []string{"*.md"}, []string{"**"}) {
			t.Error("the root must always match")
		}
	})

	twin.Run(t, "resource-watch-policy", "applies include alternatives and exclude precedence to every path", func(t *testing.T) {
		cases := []struct {
			path             string
			includes, exclud []string
			want             bool
		}{
			{"file.txt", nil, nil, true},
			{"nested/file.md", []string{"**/*.md"}, nil, true},
			{"nested/file.txt", []string{"**/*.md"}, nil, false},
			{"file.ts", []string{"**/*.md", "**/*.ts"}, nil, true},
			{"nested/file.md", []string{"**/*.md"}, []string{"nested/**"}, false},
			{"node_modules/pkg/index.js", nil, []string{"**/node_modules/**"}, false},
			{"nested/node_modules/pkg/index.js", nil, []string{"**/node_modules/**"}, false},
			{"node_modules", nil, []string{"**/node_modules/**"}, true},
			{".git/config", nil, []string{"**/.git/**"}, false},
			{"src/.hidden.ts", []string{"**/*.ts"}, nil, false},
			{"src/.hidden.ts", []string{"**/.*.ts"}, nil, true},
		}
		for _, c := range cases {
			if got := MatchesPatterns(c.path, c.includes, c.exclud); got != c.want {
				t.Errorf("%s includes=%v excludes=%v: got %v, want %v", c.path, c.includes, c.exclud, got, c.want)
			}
		}
	})

	twin.Run(t, "resource-watch-policy", "does not prune a directory just because its children alone match the include filter", func(t *testing.T) {
		if MatchesPatterns("src", []string{"**/*.ts"}, nil) {
			t.Error("src alone does not match **/*.ts")
		}
		if IsExcluded("src", nil) {
			t.Error("nothing is excluded without patterns")
		}
		if !MatchesPatterns("src/file.ts", []string{"**/*.ts"}, nil) {
			t.Error("src/file.ts must match")
		}
		if !IsExcluded("src/generated", []string{"**/generated"}) {
			t.Error("src/generated must be excluded")
		}
	})
}

func checkTransitions(t *testing.T, previous ahptypes.ResourceChangeType, expected [3]ahptypes.ResourceChangeType) {
	t.Helper()
	for i, change := range []ahptypes.ResourceChangeType{cAdded, cUpdated, cDeleted} {
		if got := MergeChange(previous, change); got != expected[i] {
			t.Errorf("%q then %s: got %q, want %q", previous, change, got, expected[i])
		}
	}
}

func TestWatchBatchTransitions(t *testing.T) {
	twin.Run(t, "resource-watch-policy", "coalesces empty followed by each native event type", func(t *testing.T) {
		checkTransitions(t, "", [3]ahptypes.ResourceChangeType{cAdded, cUpdated, cDeleted})
	})
	twin.Run(t, "resource-watch-policy", "coalesces added followed by each native event type", func(t *testing.T) {
		checkTransitions(t, cAdded, [3]ahptypes.ResourceChangeType{cAdded, cAdded, ""})
	})
	twin.Run(t, "resource-watch-policy", "coalesces updated followed by each native event type", func(t *testing.T) {
		checkTransitions(t, cUpdated, [3]ahptypes.ResourceChangeType{cUpdated, cUpdated, cDeleted})
	})
	twin.Run(t, "resource-watch-policy", "coalesces deleted followed by each native event type", func(t *testing.T) {
		checkTransitions(t, cDeleted, [3]ahptypes.ResourceChangeType{cUpdated, cUpdated, cDeleted})
	})

	twin.Run(t, "resource-watch-policy", "handles replacement and transient paths across longer sequences", func(t *testing.T) {
		cases := []struct {
			sequence []ahptypes.ResourceChangeType
			want     ahptypes.ResourceChangeType
		}{
			{[]ahptypes.ResourceChangeType{cAdded, cUpdated, cDeleted}, ""},
			{[]ahptypes.ResourceChangeType{cAdded, cDeleted, cAdded}, cAdded},
			{[]ahptypes.ResourceChangeType{cDeleted, cAdded, cDeleted}, cDeleted},
			{[]ahptypes.ResourceChangeType{cDeleted, cAdded, cDeleted, cAdded}, cUpdated},
			{[]ahptypes.ResourceChangeType{cUpdated, cDeleted, cAdded}, cUpdated},
		}
		for _, c := range cases {
			var got ahptypes.ResourceChangeType
			for _, change := range c.sequence {
				got = MergeChange(got, change)
			}
			if got != c.want {
				t.Errorf("%s: got %q, want %q", fmt.Sprint(c.sequence), got, c.want)
			}
		}
	})
}

func TestGlobMatcher(t *testing.T) {
	// Additions: the matcher is ours (Node's path.matchesGlob is not in the standard library).
	cases := []struct {
		path, pattern string
		want          bool
	}{
		{"a/b.ts", "a/*.ts", true}, {"a/b/c.ts", "a/*.ts", false}, {"a", "a/**", false}, {"a/b", "a/**", true},
		{"a/x/b", "a/**/b", true}, {"a/b", "a/**/b", true}, {"x.md", "*.{md,txt}", true}, {"x.go", "*.{md,txt}", false},
		{"file1", "file[0-9]", true}, {"filex", "file[!0-9]", true}, {"file1", "file[!0-9]", false}, {"ab", "a?", true},
		{".env", "*", false}, {".env", ".*", true}, {"a/.git/x", "a/**", false}, {"a[b", "a[b", true},
	}
	for _, c := range cases {
		if got := matchesGlob(c.path, c.pattern); got != c.want {
			t.Errorf("matchesGlob(%q, %q) = %v, want %v", c.path, c.pattern, got, c.want)
		}
	}
}
