package music

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// MissingError names the programs the player needs and cannot find.
type MissingError struct {
	Names []string
	GOOS  string
}

func (e *MissingError) Error() string {
	var hints []string
	for _, n := range e.Names {
		hints = append(hints, installHint(n, e.GOOS))
	}
	return fmt.Sprintf("pig-music needs %s, which %s not installed. %s",
		strings.Join(e.Names, " and "), plural(len(e.Names), "is", "are"), strings.Join(hints, " "))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func installHint(name, goos string) string {
	switch {
	case name == "mpv" && goos == "darwin":
		return "Install mpv with `brew install mpv`."
	case name == "mpv":
		return "Install mpv with your package manager (for example `sudo apt-get install mpv`)."
	case name == "yt-dlp" && goos == "darwin":
		return "Install yt-dlp with `brew install yt-dlp`."
	case name == "yt-dlp":
		return "Install a current yt-dlp release (https://github.com/yt-dlp/yt-dlp#installation); the distribution packages are often too old for YouTube."
	}
	return "Install " + name + "."
}

// CheckDependencies reports the player's programs that cannot be found. Names
// default to "mpv" and "yt-dlp" when the paths are empty; a configured path must
// resolve as given. lookPath is exec.LookPath in production.
func CheckDependencies(mpvPath, ytdlpPath string, lookPath func(string) (string, error)) error {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	var missing []string
	for _, p := range []struct{ name, path string }{{"mpv", mpvPath}, {"yt-dlp", ytdlpPath}} {
		look := p.path
		if look == "" {
			look = p.name
		}
		if _, err := lookPath(look); err != nil {
			missing = append(missing, p.name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return &MissingError{Names: missing, GOOS: runtime.GOOS}
}
