package frontend

import (
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Tern reads a pane's working directory from OSC 7, as a shell reports it:
// relative links in PiG's replies, the Files pane and a split opened from
// the pane then start where PiG works, not where it was launched. PiG
// reports its directory only in the footer, "~"-abbreviated, so the member
// expands that and reports it whenever it changes.

// reportCwd records the footer's directory as an absolute path and writes
// OSC 7 when it changed. It runs under the session lock. While PiG lends
// the terminal out, the report waits for the next change; the directory
// does not change meanwhile.
func (s *session) reportCwd(cwd string) {
	if cwd == s.cwdShown {
		return
	}
	s.cwdShown = cwd
	s.cwdAbs = absCwd(cwd, s.env.Getenv("HOME"))
	if s.cwdAbs == "" || s.suspended || s.closed || s.fellBack {
		return
	}
	_, _ = io.WriteString(s.out, "\x1b]7;"+fileURL(s.hostname, s.cwdAbs)+"\x07")
}

// absCwd expands a leading "~" with home; it returns "" for a directory
// that is still not absolute.
func absCwd(cwd, home string) string {
	switch {
	case cwd == "~":
		cwd = home
	case strings.HasPrefix(cwd, "~/"):
		if home == "" {
			return ""
		}
		cwd = filepath.Join(home, cwd[2:])
	}
	if !filepath.IsAbs(cwd) {
		return ""
	}
	return cwd
}

// fileURL is a file URL of an absolute path on host, its path escaped.
func fileURL(host, path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		// A Windows path: file://host/C:/dir.
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Host: host, Path: p}).String()
}

// hostname names this machine in OSC 7, or "" (a local file URL) when the
// system does not say.
func hostname() string {
	host, _ := os.Hostname()
	return host
}

// pathHref links a tool's path target to the file, so ⌘-click opens it in
// Tern: a relative path is resolved against PiG's working directory, and
// "" is returned when that is unknown. Tern reads a link without a scheme
// as a path after %XX-decoding it, so a literal "%" is escaped.
func (s *session) pathHref(target string) string {
	path := target
	if !filepath.IsAbs(path) {
		if expanded := absCwd(path, s.env.Getenv("HOME")); expanded != "" {
			path = expanded
		} else if s.cwdAbs != "" {
			path = filepath.Join(s.cwdAbs, path)
		} else {
			return ""
		}
	}
	return strings.ReplaceAll(path, "%", "%25")
}
