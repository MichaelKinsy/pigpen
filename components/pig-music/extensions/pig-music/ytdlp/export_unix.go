//go:build unix

package ytdlp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/account"
	"github.com/MichaelKinsy/pigpen/pig-music/internal/lazyre"
	"github.com/MichaelKinsy/pigpen/pig-music/native"
)

// ExportCookies has yt-dlp read the browser's cookies (the same read, with the same consent and browser, as a library call)
// and hand them back in memory. yt-dlp writes its cookie jar to a path it is given, so it is given /dev/fd/3: an open file
// that was unlinked before yt-dlp started, which has no name and goes away when the descriptor closes. A pipe does not work
// (yt-dlp reads the file first, and blocks). The cookies sent to music.youtube.com are kept, in an account.Jar; the rest
// are dropped at once. The URL is one yt-dlp refuses before any network request ("file:" URLs are disabled), so the cookies
// go nowhere.
func ExportCookies(ctx context.Context, bin, spec string) (account.Jar, error) {
	f, err := anonymousFile()
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.WriteString("# Netscape HTTP Cookie File\n"); err != nil { // yt-dlp refuses an empty cookie file
		return nil, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	// yt-dlp copies the browser's cookie database into a directory of its own under TMPDIR while it reads it (Firefox's holds
	// the values in clear) and removes it when done; a run killed at the timeout would leave it. It gets a private TMPDIR,
	// removed here whatever happened.
	tmp, err := os.MkdirTemp(runtimeDir(), ".pm-ytdlp-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	ctx, cancel := context.WithTimeout(ctx, DefaultLibraryTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--ignore-config", "--no-warnings", "--cookies-from-browser", spec,
		"--cookies", "/dev/fd/3", "--skip-download", "--simulate", "file:///")
	cmd.Env = append(os.Environ(), "TMPDIR="+tmp)
	cmd.ExtraFiles = []*os.File{f}
	ownGroup(cmd)
	cmd.WaitDelay = 2 * time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &limitWriter{w: &stderr, left: 64 << 10}
	runErr := cmd.Run() // exit status 1 is expected: the URL is refused
	if ctx.Err() != nil {
		return nil, fmt.Errorf("yt-dlp did not export the cookies in time: %w", ctx.Err())
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	jar, err := account.ParseNetscape(io.LimitReader(f, 8<<20), time.Now())
	if err != nil {
		return nil, err
	}
	if len(jar) == 0 {
		var ee *exec.ExitError
		if runErr != nil && !errors.As(runErr, &ee) {
			return nil, fmt.Errorf("yt-dlp did not run: %w", runErr)
		}
		line, _, _ := strings.Cut(strings.TrimSpace(stderr.String()), "\n")
		return nil, fmt.Errorf("yt-dlp exported no YouTube cookies: %s", native.Redact(assignRe.ReplaceAllString(line, "$1=...")))
	}
	return jar, nil
}

// assignRe finds name=value words, whose values are hidden from the error text: a cookie value must not reach a message.
var assignRe = lazyre.New(`(\S+?)=\S+`)

// anonymousFile is an empty file with no name: on Linux a memfd (memory only), elsewhere a file made, then unlinked at once, in
// the runtime directory when there is one (memory on most systems), else the temporary directory (on macOS a per-user
// directory on disk: the unlinked file's blocks may reach the disk there).
func anonymousFile() (*os.File, error) {
	if f, err := memFile(); err == nil {
		return f, nil
	}
	f, err := os.CreateTemp(runtimeDir(), ".pm-")
	if err != nil {
		return nil, err
	}
	if err := os.Remove(f.Name()); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// runtimeDir is $XDG_RUNTIME_DIR when it names a directory (a tmpfs of the user's own on most Linux systems), else "" (the
// temporary directory).
func runtimeDir() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return ""
	}
	return dir
}
