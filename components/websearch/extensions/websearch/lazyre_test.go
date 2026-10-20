package websearch

import "testing"

// Every expression that the package declares is compiled lazily, so a pattern with a syntax error would only show at
// the first use; this compiles them all.
func TestLazyExpressionsCompile(t *testing.T) {
	dfdPartials()
	dfdNavREs()
	lazyREMu.Lock()
	all := append([]*lazyRE(nil), lazyREAll...)
	lazyREMu.Unlock()
	if len(all) < 300 {
		t.Fatalf("%d lazy expressions registered, the package declares more than 300", len(all))
	}
	for _, l := range all {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("pattern %q (flags %q) does not compile: %v", l.pattern, l.flags, r)
				}
			}()
			l.get()
		}()
	}
}

func TestLazyExpressionBehavesLikeTheCompiledOne(t *testing.T) {
	re := jsRE(`^a.c$`, "i")
	if !re.MatchString("ABC") || re.MatchString("a\nc") {
		t.Fatal("a JavaScript pattern is translated when it is first used")
	}
	if jsRE(`^a.c$`, "i") != re {
		t.Fatal("the same pattern and flags are one expression")
	}
}
