package jev_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func goSources(t *testing.T) map[string]string {
	t.Helper()
	files, _ := filepath.Glob("*.go")
	out := map[string]string{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out[f] = string(b)
	}
	return out
}

// The task: "use the model from PiG configuration, no hardcoded provider".
func TestNoHardcodedProviderOrEndpoint(t *testing.T) {
	for name, src := range goSources(t) {
		for _, bad := range []string{"api.typesafe.ai", "jev-latest", "api.openai.com", "api.anthropic.com", "openrouter.ai"} {
			if strings.Contains(src, bad) {
				t.Errorf("%s hardcodes %q", name, bad)
			}
		}
	}
}

// Public PiG Go SDK only: no PiG-internal imports, no Node.
func TestOnlyPublicSDKImports(t *testing.T) {
	for name, src := range goSources(t) {
		for _, line := range strings.Split(src, "\n") {
			l := strings.TrimSpace(line)
			if strings.HasPrefix(l, `"github.com/MichaelKinsy/PiG/`) && !strings.HasPrefix(l, `"github.com/MichaelKinsy/PiG/extensions/sdk`) {
				t.Errorf("%s imports %s", name, l)
			}
			if strings.Contains(l, "os/exec") {
				t.Errorf("%s starts a process (%s)", name, l)
			}
		}
	}
}

// Named gaps (Skill step 3: a gap is a named skipped test, never silent).

func TestUserRequestJoinsTextBlocksWithNewline_GAP(t *testing.T) {
	t.Skip("upstream joins a user message's text blocks with \\n (index.ts messageText); the Go SDK's BranchEntry flattens them with no separator; a prompt sent over RPC is one block, so no scenario differs")
}
