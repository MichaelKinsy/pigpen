package native

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Review of M9b: the one-shot helper (`pigmusic native ...`) is a protocol between two programs that are built and installed
// separately (PIG_MUSIC_SERVE and nativePath name any pigmusic), so it carries a version, names an older pigmusic as such,
// keeps the helper's own message, and stops a helper that floods instead of reading it to the time limit.

func reviewHelper(mode string, args []string) int {
	switch mode {
	case "helper-noversion": // a reply in the right shape but without the version: some other, older or newer, pigmusic
		fmt.Println(`{"entries":[{"VideoID":"dQw4w9WgXcQ","Title":"T"}]}`)
		return 0
	case "helper-old": // what a pigmusic from before M9b prints: it has no `native` command
		fmt.Fprintln(os.Stderr, `pigmusic: unknown command "native"`)
		fmt.Fprintln(os.Stderr, "\nusage: pigmusic <command> [arguments]")
		return 2
	case "helper-bigreply": // a valid document larger than the cap
		var entries []PlaylistEntry
		for i := 0; i < 60000; i++ {
			entries = append(entries, PlaylistEntry{VideoID: "dQw4w9WgXcQ", Title: strings.Repeat("t", 80)})
		}
		WriteHelperReply(os.Stdout, entries, nil, nil)
		return 0
	case "helper-endless":
		chunk := strings.Repeat("x", 64<<10)
		for {
			if _, err := fmt.Print(chunk); err != nil {
				return 1
			}
		}
	}
	return -1
}

func TestReviewHelperRefusesAReplyWithoutItsProtocolVersion(t *testing.T) {
	h := helperFor(t, "helper-noversion")
	_, err := h.Playlist(context.Background(), "PLabcdefghijklmnop")
	if err == nil || !strings.Contains(err.Error(), "version") || !strings.Contains(err.Error(), "pigmusic") {
		t.Fatalf("a reply without the helper version was accepted or not explained: %v", err)
	}
}

func TestReviewAnOlderPigmusicWithoutTheHelperIsNamedAsOlder(t *testing.T) {
	h := helperFor(t, "helper-old")
	_, err := h.Playlist(context.Background(), "PLabcdefghijklmnop")
	if err == nil || !strings.Contains(err.Error(), "older") || !strings.Contains(err.Error(), "go build") {
		t.Fatalf("an old pigmusic is not named as such with the way to rebuild it: %v", err)
	}
}

func TestReviewAFailingHelperKeepsItsOwnMessageRedacted(t *testing.T) {
	h := helperFor(t, "helper-error")
	_, err := h.Playlist(context.Background(), "PLabcdefghijklmnop")
	if err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("the helper's own (redacted) message was lost: %v", err)
	}
}

func TestReviewAHelperReplyOverTheCapIsRefused(t *testing.T) {
	h := helperFor(t, "helper-bigreply")
	_, err := h.Playlist(context.Background(), "PLabcdefghijklmnop")
	if err == nil {
		t.Fatal("a reply over the 4 MiB cap was read whole")
	}
}

func TestReviewAHelperThatFloodsIsStoppedAtTheCapNotAtTheTimeout(t *testing.T) {
	h := helperFor(t, "helper-endless")
	h.Timeout = 30 * time.Second
	start := time.Now()
	_, err := h.Playlist(context.Background(), "PLabcdefghijklmnop")
	if err == nil || !strings.Contains(err.Error(), "MiB") {
		t.Errorf("a flood is not reported as one: %v", err)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("a flooding helper ran for %v (to the time limit) after the cap was reached", took)
	}
}

// A configured path (nativePath, PIG_MUSIC_SERVE) that is relative but not a bare name resolves against the working
// directory, which is the project PiG was started in: a repository could ship its own ./bin/pigmusic there.
func TestReviewARelativeServePathIsNotTakenFromTheWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "pigmusic"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	for _, rel := range []string{"bin/pigmusic", "./bin/pigmusic"} {
		got, err := FindServeBinary(rel, func(string) string { return "" }, nil)
		if err == nil {
			t.Errorf("%q resolved to %q in the working directory", rel, got)
			continue
		}
		if !strings.Contains(err.Error(), "absolute") {
			t.Errorf("%q: the refusal does not say what to use: %v", rel, err)
		}
		got, err = FindServeBinary("", func(k string) string {
			if k == "PIG_MUSIC_SERVE" {
				return rel
			}
			return ""
		}, nil)
		if err == nil {
			t.Errorf("PIG_MUSIC_SERVE=%q resolved to %q in the working directory", rel, got)
		}
	}
	// an absolute path is still taken as given
	abs := filepath.Join(dir, "bin", "pigmusic")
	if got, err := FindServeBinary(abs, func(string) string { return "" }, nil); err != nil || got != abs {
		t.Errorf("absolute path: %q, %v", got, err)
	}
}
