//go:build !windows

package pig_music

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/mpv"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// Pressing next a few times in a row, shuffling or editing the queue offers a new next entry each time. Each offer waits
// before resolving; by then only the entry that is still next is worth a yt-dlp run (3 s of CPU and network each): the
// earlier offers must not all run yt-dlp at once.
func TestOnlyTheEntryThatIsStillNextAfterTheDelayIsPrefetched(t *testing.T) {
	old := prefetchDelay
	prefetchDelay = 150 * time.Millisecond
	t.Cleanup(func() { prefetchDelay = old })
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	bin := filepath.Join(dir, "yt-dlp")
	script := "#!/bin/sh\nfor a in \"$@\"; do last=$a; done\necho \"$last\" >> " + calls + "\necho '{\"url\":\"https://rr1.googlevideo.com/x\"}'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := mpv.Config{YtdlPath: bin}
	a := &app{}
	a.prefetching(&cfg, music.PathsIn(filepath.Join(dir, "run")))
	if cfg.Prefetch == nil {
		t.Fatal("no prefetch")
	}
	var wg sync.WaitGroup
	ids := []string{"aaaaaaaaaaa", "bbbbbbbbbbb", "ccccccccccc", "ddddddddddd", "eeeeeeeeeee"}
	for _, id := range ids {
		wg.Add(1)
		go func() { defer wg.Done(); cfg.Prefetch("https://music.youtube.com/watch?v=" + id) }()
		time.Sleep(10 * time.Millisecond)
	}
	wg.Wait()
	got, _ := os.ReadFile(calls)
	lines := strings.Fields(string(got))
	if len(lines) != 1 || !strings.HasSuffix(lines[0], "eeeeeeeeeee") {
		t.Errorf("yt-dlp ran for %v, want only the last next entry", lines)
	}
}

// A stream mpv could not open drops the prefetched answer it came from.
func TestAStreamThatFailedDropsItsPrefetchedAnswer(t *testing.T) {
	old := prefetchDelay
	prefetchDelay = 0
	t.Cleanup(func() { prefetchDelay = old })
	dir := t.TempDir()
	bin := filepath.Join(dir, "yt-dlp")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho '{\"url\":\"https://rr1.googlevideo.com/x\"}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := mpv.Config{YtdlPath: bin}
	(&app{}).prefetching(&cfg, music.PathsIn(filepath.Join(dir, "run")))
	url := "https://music.youtube.com/watch?v=aaaaaaaaaaa"
	cfg.Prefetch(url)
	kept := filepath.Join(dir, "run", "ytdl", "aaaaaaaaaaa.json")
	if _, err := os.Stat(kept); err != nil {
		t.Fatalf("not prefetched: %v", err)
	}
	if cfg.StreamFailed == nil {
		t.Fatal("no StreamFailed")
	}
	cfg.StreamFailed(url)
	if _, err := os.Stat(kept); err == nil {
		t.Error("the refused answer is still kept")
	}
}
