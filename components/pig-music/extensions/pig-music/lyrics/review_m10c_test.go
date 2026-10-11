package lyrics

import (
	"context"
	"errors"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// A sheet from LRCLIB is anyone's upload. One line with many stamps and many word timings must not cost stamps x words of
// memory: the words a sheet keeps are bounded whatever the line holds.
func TestReviewManyStampsTimesManyWordsStaysBounded(t *testing.T) {
	stamps := strings.Repeat("[00:01.00]", 2000)
	words := strings.Repeat("<00:01.00>a ", 2000)
	l := ParseLRC(stamps + words)
	n := 0
	for _, ln := range l.Lines {
		n += len(ln.Words)
	}
	if n > MaxWords {
		t.Fatalf("%d timed words kept over %d lines (max %d)", n, len(l.Lines), MaxWords)
	}
	if len(l.Lines) > MaxLines {
		t.Fatalf("%d lines", len(l.Lines))
	}
}

// An [offset:] tag far out of range does not wrap the times around.
func TestReviewHugeOffsetDoesNotWrap(t *testing.T) {
	for _, tag := range []string{"[offset:9223372036854775807]", "[offset:-9223372036854775807]"} {
		l := ParseLRC(tag + "\n[00:10.00]a\n[00:20.00]b\n[00:30.00]c")
		for i, ln := range l.Lines {
			if ln.At < 0 || ln.At > time.Hour*24 {
				t.Errorf("%s: line %d at %v", tag, i, ln.At)
			}
			if i > 0 && ln.At < l.Lines[i-1].At {
				t.Errorf("%s: out of order %v", tag, l.Lines)
			}
		}
	}
}

func FuzzParseLRC(f *testing.F) {
	for _, s := range []string{
		"[00:01.00]hello\n[00:02.00]<00:02.00>big <00:02.50>world",
		"[offset:+500]\n[00:10.00]a\n[00:00.20]b",
		"[00:10.00][00:40.00]chorus\n[00:20.00]verse",
		"[ar:x]\n[ti:y]\nplain",
		"[999:59.999]<0:0>",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		l := ParseLRC(s)
		if len(l.Lines) > MaxLines {
			t.Fatalf("%d lines", len(l.Lines))
		}
		for i, ln := range l.Lines {
			if ln.At < 0 || (i > 0 && ln.At < l.Lines[i-1].At) {
				t.Fatalf("line %d at %v", i, ln.At)
			}
			if utf8.ValidString(s) && !utf8.ValidString(ln.Text) {
				t.Fatalf("invalid text %q", ln.Text)
			}
			for _, w := range ln.Words {
				if w.At < 0 {
					t.Fatalf("word at %v", w.At)
				}
			}
		}
		p := ParsePlain(s)
		if len(p.Lines) > MaxLines {
			t.Fatalf("%d plain lines", len(p.Lines))
		}
	})
}

// LRCLIB asking us to slow down (429) or refusing (403) is not "this song has no lyrics": it must not be remembered for a day.
func TestReviewARateLimitIsAnErrorAndIsNotRememberedAsNone(t *testing.T) {
	for _, code := range []int{429, 403} {
		f := newFake(t)
		f.status = code
		dir := t.TempDir()
		if _, err := svc(f, dir).Lyrics(context.Background(), example); err == nil || errors.Is(err, music.ErrNoLyrics) {
			t.Fatalf("HTTP %d: %v", code, err)
		}
		f.status = 0
		if l, err := svc(f, dir).Lyrics(context.Background(), example); err != nil || len(l.Lines) == 0 {
			t.Errorf("HTTP %d, then a good answer: %v %v", code, l, err)
		}
	}
}
