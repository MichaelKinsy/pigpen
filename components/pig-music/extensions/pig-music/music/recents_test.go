package music

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func recentsIn(t *testing.T) *RecentsFile {
	t.Helper()
	clock := time.Unix(1000, 0)
	return &RecentsFile{Path: filepath.Join(t.TempDir(), "pig-music", "recents.json"), Now: func() time.Time {
		clock = clock.Add(time.Second)
		return clock
	}}
}

func describeRecents(rs []Recent) string {
	var out []string
	for _, r := range rs {
		if r.Track != nil {
			out = append(out, "t:"+r.Track.ID)
		} else {
			out = append(out, "q:"+r.Query)
		}
	}
	return strings.Join(out, " ")
}

// TestRecentsAreKeptNewestFirstAcrossRestarts: what was searched and played comes back, newest first, from a new RecentsFile on
// the same path (a new session), in a private file.
func TestRecentsAreKeptNewestFirstAcrossRestarts(t *testing.T) {
	f := recentsIn(t)
	if got, err := f.Load(); err != nil || len(got) != 0 {
		t.Fatalf("a missing file: %v, %v", got, err)
	}
	if _, err := os.Stat(f.Path); err == nil {
		t.Fatal("loading wrote the file")
	}
	song := Track{ID: "dQw4w9WgXcQ", Title: "Never Gonna Give You Up", Artists: []string{"Rick Astley"}, Duration: 213 * time.Second}
	for _, r := range []Recent{{Query: "night drive"}, {Track: &song}, {Query: "  yoasobi  "}} {
		if _, err := f.Add(r); err != nil {
			t.Fatal(err)
		}
	}
	again := &RecentsFile{Path: f.Path}
	got, err := again.Load()
	if err != nil {
		t.Fatal(err)
	}
	if d := describeRecents(got); d != "q:yoasobi t:dQw4w9WgXcQ q:night drive" {
		t.Errorf("history %s", d)
	}
	if got[1].Track.Title != song.Title || got[1].Track.Artists[0] != "Rick Astley" || got[1].Track.Duration != song.Duration || got[1].At == 0 {
		t.Errorf("the track's metadata: %+v", got[1])
	}
	info, err := os.Stat(f.Path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the file's mode: %v %v", info.Mode(), err)
	}
	if dir, _ := os.Stat(filepath.Dir(f.Path)); dir.Mode().Perm() != 0o700 {
		t.Errorf("the directory's mode: %v", dir.Mode())
	}
}

// TestTheSameSearchOrTrackMovesToTheTopInsteadOfRepeating: a search again (case and spaces aside) or a track played again is
// one entry, moved first.
func TestTheSameSearchOrTrackMovesToTheTopInsteadOfRepeating(t *testing.T) {
	f := recentsIn(t)
	a, b := Track{ID: "aaaaaaaaaaa", Title: "A"}, Track{ID: "bbbbbbbbbbb", Title: "B"}
	for _, r := range []Recent{{Query: "Night Drive"}, {Track: &a}, {Track: &b}, {Query: "night   drive"}, {Track: &a}} {
		if _, err := f.Add(r); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := f.Load()
	if d := describeRecents(got); d != "t:aaaaaaaaaaa q:night drive t:bbbbbbbbbbb" {
		t.Errorf("history %s", d)
	}
}

// TestTheHistoryKeepsTheLatestFiftyAndDropsWhatIsNotAnEntry: the oldest go past MaxRecents; an empty search or a track without an
// ID is not kept.
func TestTheHistoryKeepsTheLatestFiftyAndDropsWhatIsNotAnEntry(t *testing.T) {
	f := recentsIn(t)
	for i := range MaxRecents + 7 {
		if _, err := f.Add(Recent{Query: "q" + strings.Repeat("x", i)}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := f.Add(Recent{Query: "   "})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != MaxRecents || got[0].Query != "q"+strings.Repeat("x", MaxRecents+6) {
		t.Errorf("%d kept, first %q", len(got), got[0].Query)
	}
	if got, _ := f.Add(Recent{Track: &Track{Title: "no id"}}); len(got) != MaxRecents || got[0].Track != nil {
		t.Errorf("a track without an ID was kept")
	}
	long := strings.Repeat("ü", 300)
	got, _ = f.Add(Recent{Query: long})
	if n := len([]rune(got[0].Query)); n != 200 {
		t.Errorf("a long search is kept at %d runes", n)
	}
}

// TestClearForgetsEverything: the file goes, and an empty history is not an error.
func TestClearForgetsEverything(t *testing.T) {
	f := recentsIn(t)
	if _, err := f.Add(Recent{Query: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := f.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.Path); !os.IsNotExist(err) {
		t.Errorf("the file is still there: %v", err)
	}
	if got, err := f.Load(); err != nil || len(got) != 0 {
		t.Errorf("after Clear: %v %v", got, err)
	}
	if err := f.Clear(); err != nil {
		t.Errorf("clearing an empty history: %v", err)
	}
}

// TestADamagedOrForeignFileIsAnEmptyHistoryThatTheNextAddReplaces.
func TestADamagedOrForeignFileIsAnEmptyHistoryThatTheNextAddReplaces(t *testing.T) {
	for _, content := range []string{"{not json", `{"v":99,"items":[{"query":"x","at":1}]}`, strings.Repeat(" ", maxRecentsBytes+1)} {
		f := recentsIn(t)
		_ = os.MkdirAll(filepath.Dir(f.Path), 0o700)
		if err := os.WriteFile(f.Path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if got, err := f.Load(); err != nil || len(got) != 0 {
			t.Errorf("%.20q: %v %v", content, got, err)
		}
		got, err := f.Add(Recent{Query: "fresh"})
		if err != nil || describeRecents(got) != "q:fresh" {
			t.Errorf("%.20q: then %v %v", content, describeRecents(got), err)
		}
	}
}
