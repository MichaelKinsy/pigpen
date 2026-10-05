package warden

import (
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"sync"
	"testing"
)

// Importing warden must not compile its regular expressions: the package is in every pig that selects it, on or
// off. The package's init (GODEBUG=inittrace=1, run in a child test binary) used to be about 7,000 allocations
// and 1 MB for the compiles.
func TestPackageInitDoesNotCompileRegexps(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "GODEBUG=inittrace=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child test binary: %v\n%s", err, out)
	}
	m := regexp.MustCompile(`init github\.com/MichaelKinsy/pigpen/warden @[\d.]+ ms, [\d.]+ ms clock, (\d+) bytes, (\d+) allocs`).FindSubmatch(out)
	if m == nil {
		t.Fatalf("no init line for the warden package in:\n%s", out)
	}
	bytes, _ := strconv.Atoi(string(m[1]))
	allocs, _ := strconv.Atoi(string(m[2]))
	if allocs > 1500 || bytes > 150_000 {
		t.Fatalf("package init allocated %d bytes in %d allocs; package-level expressions must be lazyRE, not compiled at start", bytes, allocs)
	}
}

func TestLazyRegexpCompilesOnFirstUseOnly(t *testing.T) {
	before := lazyCompiled.Load()
	l := lazyRE(`^a+b$`)
	if lazyCompiled.Load() != before {
		t.Fatal("creating a lazy expression must not compile it")
	}
	if !l.MatchString("aaab") || l.MatchString("b") || l.String() != `^a+b$` {
		t.Fatal("the lazy expression must match like the compiled one")
	}
	l.MatchString("ab")
	if got := lazyCompiled.Load() - before; got != 1 {
		t.Fatalf("expected one compilation across uses, got %d", got)
	}
}

// Compiling at first use moved a bad pattern's failure from package init (every pig that selects warden would
// refuse to start, so no test could miss it) to the first tool call that reaches the expression, which may be a
// rare path no other test takes. Every package-level expression must still compile.
func TestEveryLazyExpressionCompiles(t *testing.T) {
	all := lazyExpressions()
	if len(all) < 100 {
		t.Fatalf("expected the package's hundred-odd expressions to be registered, found %d", len(all))
	}
	for _, l := range all {
		if _, err := regexp.Compile(l.expr); err != nil {
			t.Errorf("%q does not compile: %v", l.expr, err)
		}
	}
}

// Tool calls are handled concurrently, so the first use of an expression can race with another: every caller must
// see the one compiled expression (run under -race).
func TestLazyRegexpConcurrentFirstUse(t *testing.T) {
	l := lazyRE(`^c+d$`)
	var wg sync.WaitGroup
	got := make([]*regexp.Regexp, 16)
	for i := range got {
		wg.Go(func() {
			if !l.MatchString("ccd") {
				t.Error("no match")
			}
			got[i] = l.get()
		})
	}
	wg.Wait()
	for _, re := range got {
		if re != got[0] {
			t.Fatal("concurrent first uses compiled more than one expression")
		}
	}
}
