package lyrics

import (
	"testing"
	"time"
)

func ms(n int) time.Duration { return time.Duration(n) * time.Millisecond }

func TestSyncedLinesAreParsedAndOrdered(t *testing.T) {
	l := ParseLRC("[ar:Someone]\n[00:12.50] second line\n[00:05.00]first line\n[01:02.345]third\n")
	if !l.Synced || len(l.Lines) != 3 {
		t.Fatalf("%+v", l)
	}
	want := []struct {
		at   time.Duration
		text string
	}{{ms(5000), "first line"}, {ms(12500), "second line"}, {ms(62345), "third"}}
	for i, w := range want {
		if l.Lines[i].At != w.at || l.Lines[i].Text != w.text {
			t.Errorf("%d: %+v", i, l.Lines[i])
		}
	}
	if l.Lines[0].End != ms(12500) || l.Lines[2].End != 0 {
		t.Errorf("ends %v %v", l.Lines[0].End, l.Lines[2].End)
	}
}

func TestOneLineWithSeveralTimestampsIsRepeated(t *testing.T) {
	l := ParseLRC("[00:10.00][00:40.00]chorus\n[00:20.00]verse")
	if len(l.Lines) != 3 || l.Lines[0].Text != "chorus" || l.Lines[2].Text != "chorus" || l.Lines[2].At != ms(40000) {
		t.Errorf("%+v", l.Lines)
	}
}

func TestTheOffsetTagMovesEveryLine(t *testing.T) {
	l := ParseLRC("[offset:+500]\n[00:10.00]a\n[00:00.20]b")
	if l.Lines[0].At != 0 || l.Lines[1].At != ms(9500) { // a timestamp cannot go below zero
		t.Errorf("%+v", l.Lines)
	}
}

func TestEnhancedLRCGivesWordTimings(t *testing.T) {
	l := ParseLRC("[00:10.00]<00:10.00>Hello <00:10.50>big <00:11.00>world")
	if len(l.Lines) != 1 {
		t.Fatalf("%+v", l)
	}
	ln := l.Lines[0]
	if ln.Text != "Hello big world" || len(ln.Words) != 3 || ln.Words[1].Text != "big " || ln.Words[1].At != ms(10500) {
		t.Errorf("%+v", ln)
	}
}

func TestAnEmptyStampedLineIsAGapNotText(t *testing.T) {
	l := ParseLRC("[00:01.00]a\n[00:05.00]\n[00:09.00]b")
	if len(l.Lines) != 3 || l.Lines[1].Text != "" {
		t.Errorf("%+v", l.Lines)
	}
}

func TestPlainTextIsASheetWithNoTimes(t *testing.T) {
	l := ParsePlain("one\n\ntwo \r\nthree\n")
	if l.Synced || len(l.Lines) != 4 || l.Lines[1].Text != "" || l.Lines[2].Text != "two" {
		t.Errorf("%+v", l)
	}
}

func TestTextWithNoTimestampsIsNotSynced(t *testing.T) {
	if l := ParseLRC("just words\nmore words"); l.Synced || len(l.Lines) != 0 {
		t.Errorf("%+v", l)
	}
}

func TestLRCOfHostileLengthIsBounded(t *testing.T) {
	long := ""
	for i := 0; i < 20000; i++ {
		long += "[00:01.00]x\n"
	}
	if l := ParseLRC(long); len(l.Lines) > MaxLines {
		t.Errorf("%d lines", len(l.Lines))
	}
}
