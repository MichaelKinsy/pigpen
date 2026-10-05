package art

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestTheDiscIsAsBigAsAskedAndMadeOfBrailleOnly(t *testing.T) {
	for _, size := range [][2]int{{8, 4}, {16, 8}, {30, 15}} {
		lines := Disc(3, size[0], size[1])
		if len(lines) != size[1] {
			t.Fatalf("%v: %d lines", size, len(lines))
		}
		for _, l := range lines {
			if ansi.StringWidth(l) != size[0] {
				t.Errorf("%v: %q is %d wide", size, l, ansi.StringWidth(l))
			}
			for _, r := range l {
				if r != ' ' && (r < 0x2800 || r > 0x28FF) {
					t.Errorf("%v: %q is not braille", size, string(r))
				}
			}
			if strings.Contains(l, "\x1b") {
				t.Errorf("the disc must not carry escapes: %q", l)
			}
		}
	}
}

func TestTheDiscSpinsAndComesRoundAgain(t *testing.T) {
	w, h := 16, 8
	first := strings.Join(Disc(0, w, h), "\n")
	seen := map[string]bool{first: true}
	for f := 1; f < DiscFrames; f++ {
		seen[strings.Join(Disc(f, w, h), "\n")] = true
	}
	if len(seen) < DiscFrames/2 {
		t.Errorf("only %d different pictures in %d frames: it does not look like it is turning", len(seen), DiscFrames)
	}
	if again := strings.Join(Disc(DiscFrames, w, h), "\n"); again != first {
		t.Error("a full turn should return to the first picture")
	}
	if neg := strings.Join(Disc(-DiscFrames, w, h), "\n"); neg != first {
		t.Error("a negative frame should wrap")
	}
}

func TestTheDiscHasARimAndHoleAndIsNotBlank(t *testing.T) {
	lines := Disc(0, 16, 8)
	blank := 0
	for _, l := range lines {
		if strings.TrimSpace(strings.ReplaceAll(l, "\u2800", " ")) == "" {
			blank++
		}
	}
	if blank == len(lines) || blank > 0 && blank == len(lines)-1 {
		t.Errorf("the disc is nearly empty:\n%s", strings.Join(lines, "\n"))
	}
	// the top row reaches the rim: something is drawn on the first and the last row
	if strings.TrimLeft(lines[0], "\u2800 ") == "" || strings.TrimLeft(lines[len(lines)-1], "\u2800 ") == "" {
		t.Errorf("the rim should touch the first and last rows:\n%s", strings.Join(lines, "\n"))
	}
}

func TestTooSmallToDrawADiscIsNothing(t *testing.T) {
	if Disc(0, 3, 4) != nil || Disc(0, 8, 1) != nil || Disc(0, 0, 0) != nil {
		t.Error("expected nil for sizes too small to read as a disc")
	}
}
