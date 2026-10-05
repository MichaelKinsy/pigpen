package typesafe

import (
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestDescribeRuntime_ReportsTheGoVersionPlatformAndArch(t *testing.T) {
	// Adapted: the upstream reports node/bun/deno/edge/browser; the Go runtime is always Go.
	twin(t, "runtime.test.ts | describeRuntime reports node with platform and arch here")
	got := describeRuntime()
	eq(t, got, "go/"+strings.TrimPrefix(runtime.Version(), "go")+" ("+runtime.GOOS+"; "+runtime.GOARCH+")")
	if !regexp.MustCompile(`^go/\d+\.\d+(\.\d+)? \(\w+; \w+\)$`).MatchString(got) {
		t.Fatalf("runtime %q", got)
	}
}

func TestDescribeRuntime_OtherRuntimes(t *testing.T) {
	skipTwin(t, "JavaScript runtime detection (Bun, Deno, edge, browser, unknown) has no Go counterpart",
		"runtime.test.ts | describeRuntime prefers Bun over node when both are present",
		"runtime.test.ts | describeRuntime recognizes Deno",
		"runtime.test.ts | describeRuntime recognizes edge runtimes by their markers",
		"runtime.test.ts | describeRuntime falls back to browser, then unknown")
}
