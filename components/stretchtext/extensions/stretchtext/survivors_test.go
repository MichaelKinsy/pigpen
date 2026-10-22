package stretchtext

import (
	"fmt"
	"strings"
	"testing"
)

// Each test here pins a behavior that a mutation of port/mutations.json showed no other test reached.

func firstTarget(t *testing.T, reply string) Target {
	t.Helper()
	stretches := Stretches(Parse(reply))
	if len(stretches) == 0 {
		t.Fatalf("%q holds no stretch", reply)
	}
	return Target{Reply: reply, Stretch: stretches[0]}
}

func TestReadNoteNamesWhatTheReaderDidNotOpen(t *testing.T) {
	got := ReadNote([]string{"a"}, []string{"b", "c"})
	want := `Stretchtext: since your last reply the reader opened "a". They did not open "b", "c".`
	if got != want {
		t.Fatalf("ReadNote = %q, want %q", got, want)
	}
}

func TestEveryLetterIsHandedOutAndNoMore(t *testing.T) {
	var b strings.Builder
	for i := range 30 {
		fmt.Fprintf(&b, "{{s%d|long %d}} ", i, i)
	}
	reply := b.String()
	st := NewState()
	st.remember(reply, Parse(reply))
	lettering := st.letteringFor(ModeOpen)
	if len(lettering.ByLetter) != len(letters) {
		t.Fatalf("%d letters handed out, want %d", len(lettering.ByLetter), len(letters))
	}
	if _, ok := lettering.ByLetter[letters[len(letters)-1:]]; !ok {
		t.Fatalf("the last letter %q stands for no stretch", letters[len(letters)-1:])
	}
}

func TestLettersToCloseReachIntoAnOpenStretch(t *testing.T) {
	reply := "{{outer|outer long {{inner|inner long}} tail}}"
	segments := Parse(reply)
	st := NewState()
	st.remember(reply, segments)
	if got := len(st.reachable(reply, segments)); got != 1 {
		t.Fatalf("a closed stretch reaches %d stretches, want 1", got)
	}
	st.openStretch(firstTarget(t, reply), nameOf(firstTarget(t, reply)))
	if got := len(st.reachable(reply, segments)); got != 2 {
		t.Fatalf("an open stretch reaches %d stretches, want 2 (itself and the one inside)", got)
	}
}

func TestANestedStretchTakesTheNextColourNotItsParents(t *testing.T) {
	zero, six := 0, 6
	for _, c := range []struct {
		count  int
		parent *int
		want   int
	}{{0, nil, 0}, {0, &zero, 1}, {2, &six, 3}, {1, &zero, 1}} {
		if got := hueAfter(c.count, c.parent); got != c.want {
			t.Errorf("hueAfter(%d, %v) = %d, want %d", c.count, c.parent, got, c.want)
		}
	}
}

func TestOnlyTheTablesOwnAddressesResolve(t *testing.T) {
	reply := "{{a|long a}} {{b|long b}}"
	table := NewLinkTable()
	target := firstTarget(t, reply)
	table.Link(target, "a")
	for _, address := range []string{"stretchtext:0"} {
		if _, ok := table.Resolve(address); !ok {
			t.Errorf("%q does not resolve", address)
		}
	}
	for _, address := range []string{"stretchtext:1", "stretchtext:00", "stretchtext:+0", "stretchtext:-1", "stretchtext:", "stretchtext:x", "other:0", "0"} {
		if _, ok := table.Resolve(address); ok {
			t.Errorf("%q resolves", address)
		}
	}
}

// The original's SPAN expression: a run of backticks with no closing run of its own length backtracks to a shorter one, and `$$` opens display math from its own two characters.
func TestSpansAsTheOriginalsExpressionFindsThem(t *testing.T) {
	for _, c := range []struct {
		text string
		want int
	}{
		{"``x`", 2},
		{"$$$$", 4},
		{"$$a$$ b", 5},
		{"`a` b", 3},
		{"``a`` b", 5},
		{"$ a$", -1},
	} {
		if got := spanAt(c.text, 0); got != c.want {
			t.Errorf("spanAt(%q, 0) = %d, want %d", c.text, got, c.want)
		}
	}
}

func TestALongFormTakesAtMostAsLongAsTheCapToGrow(t *testing.T) {
	long := strings.Repeat("word ", 100)
	target := firstTarget(t, "{{s|"+long+"}}")
	if got := secondsFor(target); got != 1.4 {
		t.Fatalf("secondsFor a long form of %d characters = %v, want the 1.4 s cap", len(long), got)
	}
	short := firstTarget(t, "{{s|ab}}")
	if got := secondsFor(short); got <= 0.45 || got >= 0.5 {
		t.Fatalf("secondsFor a 2 character long form = %v, want 0.45 plus a small share of its length", got)
	}
}

func TestThePromptLineIsCutLikeSliceWithANegativeEnd(t *testing.T) {
	core := NewCore(RealClock(), func() Theme { return nil }, func() {})
	prompt := &LetterPrompt{core: core, mode: ModeOpen}
	full := prompt.Render(1000)
	if got := prompt.Render(-3); got != full[:len(full)-3] {
		t.Fatalf("Render(-3) = %q, want %q", got, full[:len(full)-3])
	}
	if got := prompt.Render(5); got != full[:5] {
		t.Fatalf("Render(5) = %q", got)
	}
	if got := prompt.Render(-1000); got != "" {
		t.Fatalf("Render(-1000) = %q, want empty", got)
	}
}
