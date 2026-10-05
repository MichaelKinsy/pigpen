package doctor

import (
	"github.com/MichaelKinsy/pigpen/pig-music/internal/lazyre"
	"strconv"
	"strings"
	"time"
)

// Version is a dotted number: major.minor.patch, missing parts zero.
type Version [3]int

func (v Version) String() string {
	return strconv.Itoa(v[0]) + "." + strconv.Itoa(v[1]) + "." + strconv.Itoa(v[2])
}

// AtLeast reports v >= o.
func (v Version) AtLeast(o Version) bool {
	for i := range v {
		if v[i] != o[i] {
			return v[i] > o[i]
		}
	}
	return true
}

var dotted = lazyre.New(`(\d+)(?:\.(\d+))?(?:\.(\d+))?`)

// parseVersion reads the first dotted number in s ("v24.19.0", "mpv 0.37.0 Copyright", "deno 2.5.0 (stable)").
func parseVersion(s string) (Version, bool) {
	m := dotted.FindStringSubmatch(s)
	if m == nil {
		return Version{}, false
	}
	var v Version
	for i := 0; i < 3; i++ {
		if m[i+1] != "" {
			v[i], _ = strconv.Atoi(m[i+1])
		}
	}
	return v, true
}

// parseMPVVersion reads the first line of `mpv --version`: "mpv 0.37.0 Copyright ...", or "mpv v0.38.0-dev-...".
func parseMPVVersion(out string) (Version, bool) {
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	if !strings.HasPrefix(line, "mpv") {
		return Version{}, false
	}
	return parseVersion(strings.TrimPrefix(line, "mpv"))
}

var ytdlpDate = lazyre.New(`^(\d{4})\.(\d{2})\.(\d{2})`)

// parseYtdlpDate reads a yt-dlp version ("2026.08.19", "2026.08.19.232103" for a nightly) as its release date.
func parseYtdlpDate(version string) (time.Time, bool) {
	m := ytdlpDate.FindStringSubmatch(strings.TrimSpace(version))
	if m == nil {
		return time.Time{}, false
	}
	y, _ := strconv.Atoi(m[1])
	mo, _ := strconv.Atoi(m[2])
	d, _ := strconv.Atoi(m[3])
	return time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC), true
}

// Verbose is what `yt-dlp -v` says about itself.
type Verbose struct {
	// Version is the "yt-dlp version" line's version, such as "2026.08.19".
	Version string
	// Runtimes are the JavaScript runtimes yt-dlp found enabled, such as "deno-2.5.0"; empty when "none".
	Runtimes []string
	// EJS is the version of yt_dlp_ejs, or "" when the library is not there.
	EJS string
}

var (
	verboseVersion = lazyre.New(`(?m)\[debug\] yt-dlp version \S*?@?(\d{4}\.\d{2}\.\d{2}(?:\.\d+)?)`)
	verboseRuntime = lazyre.New(`(?m)\[debug\] JS runtimes: (.*)$`)
	verboseEJS     = lazyre.New(`yt_dlp_ejs-([0-9][^\s,]*)`)
)

// ParseVerbose reads the debug header `yt-dlp -v` prints before it looks at any URL.
func ParseVerbose(out string) Verbose {
	var v Verbose
	if m := verboseVersion.FindStringSubmatch(out); m != nil {
		v.Version = m[1]
	}
	if m := verboseRuntime.FindStringSubmatch(out); m != nil {
		for _, r := range strings.Split(m[1], ",") {
			if r = strings.TrimSpace(r); r != "" && r != "none" {
				v.Runtimes = append(v.Runtimes, r)
			}
		}
	}
	if m := verboseEJS.FindStringSubmatch(out); m != nil {
		v.EJS = m[1]
	}
	return v
}

// Runtime names a JavaScript runtime yt-dlp can use, and the oldest version that works.
type runtimeSpec struct {
	Name    string // what --js-runtimes calls it
	Program string
	Args    []string
	Min     Version
}

var runtimeSpecs = []runtimeSpec{
	{"deno", "deno", []string{"--version"}, Version{2, 3, 0}},
	{"node", "node", []string{"--version"}, Version{22, 0, 0}},
	{"quickjs", "qjs", []string{"--version"}, Version{0, 12, 0}},
}

// ParseVersion reads the first dotted number in s.
func ParseVersion(s string) (Version, bool) { return parseVersion(s) }
