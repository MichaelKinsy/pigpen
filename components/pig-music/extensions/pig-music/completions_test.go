package pig_music

import (
	"sort"
	"strings"
	"testing"
)

func values(prefix string) []string {
	items, _ := completions(prefix)
	var out []string
	for _, it := range items {
		out = append(out, it.Value)
	}
	sort.Strings(out)
	return out
}

func TestCompletionsOfferEverySubcommandOnAnEmptyPrefix(t *testing.T) {
	got := strings.Join(values(""), " ")
	for _, w := range []string{"play", "pause", "resume", "toggle", "next", "prev", "vol", "now", "queue", "shuffle", "repeat", "stop", "settings", "setup", "doctor"} {
		if !strings.Contains(" "+got+" ", " "+w+" ") {
			t.Errorf("%q is not offered: %s", w, got)
		}
	}
	items, _ := completions("")
	for _, it := range items {
		if it.Description == "" {
			t.Errorf("%q has no description", it.Value)
		}
	}
}

func TestCompletionsNarrowByPrefix(t *testing.T) {
	if got := values("pa"); strings.Join(got, ",") != "pause" {
		t.Errorf("%v", got)
	}
	if got := values("s"); strings.Join(got, ",") != "settings,setup,shuffle,stop" {
		t.Errorf("%v", got)
	}
	if got := values("zzz"); len(got) != 0 {
		t.Errorf("%v", got)
	}
}

func TestCompletionsOfTheWordsAfterShuffleRepeatAndVol(t *testing.T) {
	if got := strings.Join(values("shuffle "), ","); got != "shuffle off,shuffle on" {
		t.Errorf("%s", got)
	}
	if got := strings.Join(values("repeat o"), ","); got != "repeat off,repeat one" {
		t.Errorf("%s", got)
	}
	if got := strings.Join(values("repeat "), ","); got != "repeat all,repeat off,repeat one" {
		t.Errorf("%s", got)
	}
	if got := values("vol "); len(got) < 3 || got[0] != "vol 0" && got[0] != "vol 100" {
		t.Errorf("%v", got)
	}
	if got := values("play something"); len(got) != 0 {
		t.Errorf("a query is not completed: %v", got)
	}
}

func TestAQueueLongerThanTheLimitIsCutAroundTheCurrentTrack(t *testing.T) {
	var lines []string
	for i := 1; i <= 30; i++ {
		m := " "
		if i == 15 {
			m = ">"
		}
		lines = append(lines, m+" "+string(rune('a'+i%26)))
	}
	out := shortQueue(strings.Join(lines, "\n"), 8)
	got := strings.Split(out, "\n")
	if len(got) != 9 || !strings.Contains(out, ">") || !strings.HasSuffix(got[8], "(22 more in the queue)") {
		t.Errorf("%d lines:\n%s", len(got), out)
	}
	if short := "  a\n> b"; shortQueue(short, 8) != short {
		t.Error("a short queue is left alone")
	}
}
