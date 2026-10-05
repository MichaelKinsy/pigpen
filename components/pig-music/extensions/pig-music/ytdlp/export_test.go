//go:build unix

package ytdlp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// A yt-dlp that writes what the real one writes to the cookie file it is given, and records its arguments.
func fakeExporter(t *testing.T, body string) (bin, argsFile string) {
	t.Helper()
	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args")
	bin = filepath.Join(dir, "yt-dlp")
	script := "#!/bin/sh\necho \"$@\" > " + argsFile + "\n" + body + "\nexit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, argsFile
}

const writeCookies = `for a in "$@"; do case "$a" in /dev/fd/*) f="$a";; esac; done
head -c 27 "$f" > /dev/null || exit 3
printf '# Netscape HTTP Cookie File\n.youtube.com\tTRUE\t/\tTRUE\t4102444800\tSAPISID\tSAPISIDVALUE123\n.example.com\tTRUE\t/\tTRUE\t4102444800\tx\tNOTMINE\n' > "$f"`

func TestTheCookiesComeBackThroughAnFDAndNothingIsLeftOnDisk(t *testing.T) {
	bin, argsFile := fakeExporter(t, writeCookies)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("XDG_RUNTIME_DIR", tmp)
	jar, err := ExportCookies(context.Background(), bin, "firefox:/profile")
	if err != nil {
		t.Fatal(err)
	}
	if !jar.SignedIn() || strings.Contains(jar.Header(), "NOTMINE") {
		t.Errorf("jar %v", jar)
	}
	args, _ := os.ReadFile(argsFile)
	for _, want := range []string{"--ignore-config", "--cookies-from-browser firefox:/profile", "--cookies /dev/fd/3", "--skip-download", "file:///"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("args lack %q: %s", want, args)
		}
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Errorf("files left behind: %v", left)
	}
}

func TestAnExportWithNoCookiesIsAnErrorThatShowsNoValues(t *testing.T) {
	bin, _ := fakeExporter(t, `echo "ERROR: could not find firefox cookies database SAPISID=SECRETVALUE" >&2`)
	_, err := ExportCookies(context.Background(), bin, "firefox")
	if err == nil || strings.Contains(err.Error(), "SECRETVALUE") {
		t.Errorf("%v", err)
	}
}

func TestAnExportThatHangsIsStopped(t *testing.T) {
	bin, _ := fakeExporter(t, "sleep 30")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := ExportCookies(ctx, bin, "firefox"); err == nil || time.Since(start) > 5*time.Second {
		t.Errorf("%v after %v", err, time.Since(start))
	}
}

// With a real yt-dlp and a synthetic Firefox profile (made here, with fake cookies: no real browser is read), the export is
// what the account package needs.
func TestARealYtdlpExportsASyntheticFirefoxProfile(t *testing.T) {
	bin, err := exec.LookPath("yt-dlp")
	py, perr := exec.LookPath("python3")
	if err != nil || perr != nil {
		t.Skip("needs yt-dlp and python3")
	}
	profile := t.TempDir()
	mk := `import sqlite3,sys
c=sqlite3.connect(sys.argv[1]+"/cookies.sqlite")
c.execute("create table moz_cookies (id integer primary key, originAttributes text, name text, value text, host text, path text, expiry integer, lastAccessed integer, creationTime integer, isSecure integer, isHttpOnly integer, inBrowserElement integer, sameSite integer, rawSameSite integer, schemeMap integer)")
c.execute("insert into moz_cookies (name,value,host,path,expiry,isSecure,isHttpOnly) values ('SAPISID','FAKEVALUE','.youtube.com','/',4102444800,1,0)")
c.commit()`
	if out, err := exec.Command(py, "-c", mk, profile).CombinedOutput(); err != nil {
		t.Skipf("python could not make the profile: %v %s", err, out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	jar, err := ExportCookies(ctx, bin, "firefox:"+profile)
	if err != nil {
		t.Fatal(err)
	}
	if !jar.SignedIn() || !strings.Contains(jar.Header(), "SAPISID=FAKEVALUE") {
		t.Errorf("jar %v", jar)
	}
}

// yt-dlp copies the browser's cookie database (Firefox's holds the values in clear) into a directory of its own under
// TMPDIR while it reads it, and removes it when it is done; a run that is killed (the timeout) leaves it there. The export
// gives yt-dlp a private TMPDIR and removes it afterwards, whatever happened.
func TestTheBrowserDatabaseCopyOfAKilledExportIsNotLeftBehind(t *testing.T) {
	bin, _ := fakeExporter(t, `mkdir "$TMPDIR/yt_dlpcopy" && echo SECRETVALUE > "$TMPDIR/yt_dlpcopy/temporary.sqlite"; sleep 30`)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("XDG_RUNTIME_DIR", tmp)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if _, err := ExportCookies(ctx, bin, "firefox"); err == nil {
		t.Fatal("no error")
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Errorf("left behind: %v", left)
	}
}

// On Linux the file yt-dlp writes the cookies into is a memfd: it never has a name or a block on a disk, also where there is
// no runtime directory (a login without systemd, a container) and the temporary directory is on disk.
func TestOnLinuxTheCookieFileIsInMemory(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "android" {
		t.Skip("memfd is Linux's")
	}
	t.Setenv("XDG_RUNTIME_DIR", "")
	f, err := anonymousFile()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	link, err := os.Readlink(fmt.Sprintf("/proc/self/fd/%d", f.Fd()))
	if err != nil || !strings.HasPrefix(link, "/memfd:") {
		t.Errorf("the cookie file is %q (%v), not a memfd", link, err)
	}
}
