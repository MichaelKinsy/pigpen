//go:build unix

package ytdlp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// yt-dlp copies the browser's cookie database into a directory under TMPDIR while it reads it (Firefox's holds the values in
// clear). A library run gets a private TMPDIR that is removed whatever happened to the run.

// tmpProbe is a yt-dlp that writes a stand-in for that copy into its TMPDIR, tells where it was, then does what body says.
func tmpProbe(t *testing.T, where, body string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "yt-dlp")
	script := "#!/bin/sh\nmkdir \"$TMPDIR/yt_dlp_x\" && echo copy-of-cookie-db > \"$TMPDIR/yt_dlp_x/temporary.sqlite\"\n" +
		"echo \"$TMPDIR\" > '" + where + "'\n" + body + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func privateRig(t *testing.T) (runtime string) {
	runtime = t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	t.Setenv("TMPDIR", t.TempDir()) // the default one: it must stay untouched
	return runtime
}

func readWhere(t *testing.T, where string) string {
	b, err := os.ReadFile(where)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func TestALibraryRunKeepsYtdlpsDatabaseCopyInAPrivateDirectoryThatIsGoneAfterwards(t *testing.T) {
	runtime := privateRig(t)
	where := filepath.Join(t.TempDir(), "where")
	s := &Source{Bin: tmpProbe(t, where, `echo '{"entries":[]}'`), Cookies: granted("firefox")}
	if _, err := s.Library(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := readWhere(t, where)
	if filepath.Dir(got) != runtime || !strings.HasPrefix(filepath.Base(got), ".pm-ytdlp-") {
		t.Errorf("TMPDIR was %q, want a private directory in %s", got, runtime)
	}
	if _, err := os.Stat(got); !os.IsNotExist(err) {
		t.Errorf("the private directory is still there: %v", err)
	}
	if left, _ := os.ReadDir(os.Getenv("TMPDIR")); len(left) != 0 {
		t.Errorf("something reached the default TMPDIR: %v", left)
	}
}

func TestTheDirectoryIsPrivateWhileYtdlpRuns(t *testing.T) {
	privateRig(t)
	where := filepath.Join(t.TempDir(), "where")
	mode := filepath.Join(t.TempDir(), "mode")
	s := &Source{Bin: tmpProbe(t, where, `stat -c %a "$TMPDIR" > '`+mode+`' 2>/dev/null || stat -f %Lp "$TMPDIR" > '`+mode+`'; echo '{"entries":[]}'`), Cookies: granted("firefox")}
	if _, err := s.Library(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := readWhere(t, mode); got != "700" {
		t.Errorf("mode %s", got)
	}
}

func TestARunThatFailsOrIsKilledAtTheTimeoutLeavesNothingBehind(t *testing.T) {
	runtime := privateRig(t)
	for name, body := range map[string]string{"fails": "echo boom >&2; exit 1", "killed": "sleep 30"} {
		where := filepath.Join(t.TempDir(), name)
		s := &Source{Bin: tmpProbe(t, where, body), Cookies: granted("firefox"), LibraryTimeout: 500 * time.Millisecond}
		start := time.Now()
		if _, err := s.Library(context.Background()); err == nil {
			t.Errorf("%s: no error", name)
		}
		if time.Since(start) > 10*time.Second {
			t.Errorf("%s: took %v", name, time.Since(start))
		}
		if _, err := os.Stat(readWhere(t, where)); !os.IsNotExist(err) {
			t.Errorf("%s: the private directory stayed: %v", name, err)
		}
	}
	if left, _ := os.ReadDir(runtime); len(left) != 0 {
		t.Errorf("left in the runtime directory: %v", left)
	}
}

func TestEveryLibraryCallGetsItsOwnDirectory(t *testing.T) {
	privateRig(t)
	where := filepath.Join(t.TempDir(), "where")
	s := &Source{Bin: tmpProbe(t, where, `echo '{"entries":[{"id":"aaaaaaaaaaa","title":"t"}]}'`), Cookies: granted("firefox")}
	_, _ = s.Tracks(context.Background(), "PLroadtrip0000001")
	first := readWhere(t, where)
	s.Refresh()
	_, _ = s.Tracks(context.Background(), "PLroadtrip0000001")
	if second := readWhere(t, where); second == first {
		t.Errorf("the same directory twice: %s", first)
	}
}

func TestSearchAndDetailsRunsAreNotGivenAPrivateDirectoryTheyDoNotNeedOne(t *testing.T) {
	privateRig(t)
	where := filepath.Join(t.TempDir(), "where")
	bin := tmpProbe(t, where, `echo '{"entries":[]}'`)
	s := &Source{Bin: bin}
	_, _ = s.SearchViaYtdlp(context.Background(), "x", 3)
	if got := readWhere(t, where); strings.Contains(got, ".pm-ytdlp-") {
		t.Errorf("a cookie-less run got %q", got)
	}
	_ = music.Track{}
}
