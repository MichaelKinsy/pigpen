package acp

// The update notice: the original's buildUpdateNotice (agent.ts), a best-effort "New version
// available" line that session/new surfaces even under quietStartup. The original compares
// `pi --version` with `npm view @earendil-works/pi-coding-agent version` and tells the user to
// run `npm i -g`. Here the agent is PiG, which is published to npm as @pi-in-go/pig, so the lookup
// runs `npm view @pi-in-go/pig version` the same way (npm applies the user's registry, proxy and
// auth settings, and the companion carries no HTTP or TLS client; PiG users without npm get no
// notice, as pi-acp users without npm get none). PiG's own `pig update` checks a signed release
// manifest that only pig can verify and offers no check-only command, so the notice only points
// at it.
//
// It is skipped, with no process started for the lookup, under PiG's offline switches (see
// updateLookupOff).

import (
	"context"
	"math"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const updatePackage = "@pi-in-go/pig"

// updateLookupTimeout is the original's 800 ms budget for `npm view`, kept fast so
// session/new is not slowed down.
var updateLookupTimeout = 800 * time.Millisecond

var (
	semverRE    = regexp.MustCompile(`^\d+\.\d+\.\d+(?:[-+].+)?$`)
	semverSplit = regexp.MustCompile(`[.+-]`)
)

func isSemver(v string) bool { return semverRE.MatchString(v) }

// numberOf is JavaScript's Number(s) for the pieces compareSemver sees: empty is 0, a non-number
// is NaN (every comparison with it is false).
func numberOf(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nan
	}
	return f
}

var nan = math.NaN()

// compareSemver is the original's small x.y.z comparator: a pre-release or build suffix never
// makes a version greater. The original splits on "." and "-"; PiG's version carries build
// metadata ("0.5.1+1.3.0.1402973a"), so "+" splits too, or its patch number would read as NaN and
// every patch release would go unnoticed.
func compareSemver(a, b string) int {
	pa, pb := semverSplit.Split(a, -1), semverSplit.Split(b, -1)
	for i := 0; i < 3; i++ {
		da, db := 0.0, 0.0
		if i < len(pa) {
			da = numberOf(pa[i])
		}
		if i < len(pb) {
			db = numberOf(pb[i])
		}
		if da > db {
			return 1
		}
		if da < db {
			return -1
		}
	}
	return 0
}

// updateLookupOff reports whether PiG's offline switches ask for no startup network request:
// `--offline` among the pig arguments, PIG_OFFLINE (1, true or yes), PI_OFFLINE, or
// PI_SKIP_VERSION_CHECK, read as pig reads them.
func updateLookupOff(piArgs []string) bool {
	if os.Getenv("PI_SKIP_VERSION_CHECK") != "" || strings.TrimSpace(os.Getenv("PI_OFFLINE")) != "" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("PIG_OFFLINE"))) {
	case "1", "true", "yes":
		return true
	}
	for _, a := range piArgs {
		if a == "--offline" {
			return true
		}
	}
	return false
}

// latestPigVersion is what `npm view @pi-in-go/pig version` prints, or "" when npm is missing,
// fails or is too slow.
func latestPigVersion() string {
	ctx, cancel := context.WithTimeout(context.Background(), updateLookupTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "npm", "view", updatePackage, "version")
	// A wrapper script's child may hold the pipe after the kill; do not wait for it.
	cmd.WaitDelay = 100 * time.Millisecond
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return leadingV.ReplaceAllString(jsTrim(string(out)), "")
}

// BuildUpdateNotice returns the "New version available" line, or "" when there is nothing to say:
// the lookup is switched off, pig does not report a semver version, npm does not answer in
// time or prints no semver version, or the installed one is not older.
func BuildUpdateNotice(piCommand string, piArgs []string) string {
	if updateLookupOff(piArgs) {
		return ""
	}
	installed := pigVersionHeader(piCommand)
	if installed == "" || !isSemver(installed) {
		return ""
	}
	latest := latestPigVersion()
	if latest == "" || !isSemver(latest) {
		return ""
	}
	if compareSemver(latest, installed) <= 0 {
		return ""
	}
	// The part after "+" is the Pi release pig follows, not a PiG release.
	shown, _, _ := strings.Cut(installed, "+")
	return "New version available: v" + latest + " (installed v" + shown + "). Run: `pig update` (installed with npm: `npm i -g " + updatePackage + "`)"
}
