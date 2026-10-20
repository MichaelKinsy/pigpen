package bashparse

import (
	"sync"
	"testing"
)

// The parser reuses its working memory between parses (see internal/ts/arena.go) and is shared by
// concurrent callers. A tree a caller holds must not change when another parse runs, and parallel
// parses must give what a lone parse gives.

var reuseInputs = []string{
	"",
	"echo hi",
	"cd /repo && git status --short | head -20",
	"cat <<'EOF' > /tmp/x\nbody $(date)\nEOF\nls | wc -l",
	"if [ -f a ]; then echo \"é ü 😀\"; fi",
	"echo $(( 1 + ",
	"for f in *.ts; do wc -l \"$f\"; done | sort -n | tail -3",
	"echo 'unterminated",
	"a=1 b=2 cmd >out 2>&1 <<< \"x\" || exit 1",
	"echo \xff\xfe bad bytes",
}

func TestTreeSurvivesTheNextParse(t *testing.T) {
	cp, err := GetParser()
	if err != nil {
		t.Fatal(err)
	}
	gp, _ := GrammarParser()
	for _, held := range reuseInputs {
		g0, p0 := dumpWith(gp, cp, held)
		gt, pt := gp.Parse(held), cp.Parse(held)
		for _, other := range reuseInputs {
			gp.Parse(other)
			cp.Parse(other)
		}
		if gt == nil || pt == nil {
			t.Fatalf("%q: no tree", held)
		}
		if g := dumpTree(gt.Root, true); g != g0 {
			t.Errorf("%q: the grammar tree changed after other parses", held)
		}
		if p := dumpTree(pt.Root, false); p != p0 {
			t.Errorf("%q: the corrected tree changed after other parses", held)
		}
	}
}

func TestParallelParsesMatchSerialOnes(t *testing.T) {
	cp, err := GetParser()
	if err != nil {
		t.Fatal(err)
	}
	gp, _ := GrammarParser()
	want := make([][2]string, len(reuseInputs))
	for i, in := range reuseInputs {
		want[i][0], want[i][1] = dumpWith(gp, cp, in)
	}
	var wg sync.WaitGroup
	for w := 0; w < 12; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for round := 0; round < 30; round++ {
				i := (w + round) % len(reuseInputs)
				g, p := dumpWith(gp, cp, reuseInputs[i])
				if g != want[i][0] || p != want[i][1] {
					t.Errorf("worker %d: %q parsed differently in parallel", w, reuseInputs[i])
					return
				}
			}
		}(w)
	}
	wg.Wait()
}
