// Package paths resolves Hermes home and profile directories.
// Matches hermes-agent/hermes_constants.py:72-215.
package paths

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var validProfileRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ProfilePaths holds all standard Hermes directories for the active profile.
type ProfilePaths struct {
	RootHome      string // Base ~/.hermes (or HERMES_HOME if explicit)
	ProfileName   string // Active profile name ("default" when unprofiled)
	Home          string // Active profile root (RootHome or RootHome/profiles/<name>)
	ConfigFile    string // <Home>/config.yaml
	EnvFile       string // <Home>/.env
	MemoriesDir   string // <Home>/memories
	SkillsDir     string // <Home>/skills
	CronDir       string // <Home>/cron
	CronJobsFile  string // <Home>/cron/jobs.json
	ProfilesDir   string // <RootHome>/profiles
	ActiveProfile string // <RootHome>/active_profile
	SoulFile      string // <Home>/SOUL.md
	CacheDir      string // <Home>/cache
}

// ValidateProfileName returns true if name is safe as a profile directory name.
func ValidateProfileName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	return validProfileRe.MatchString(name)
}

// RootHermesHome resolves the base Hermes home directory from HERMES_HOME or $HOME/.hermes.
func RootHermesHome() string {
	if v := strings.TrimSpace(os.Getenv("HERMES_HOME")); v != "" {
		return filepath.Clean(v)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".", ".hermes")
	}
	return filepath.Join(home, ".hermes")
}

// ResolveProfilePaths resolves the active Hermes profile paths.
// Precedence: explicit profile arg > HERMES_PROFILE env > <RootHome>/active_profile > "default".
// If HERMES_HOME is explicitly set and no profile override is passed, Home == RootHome.
func ResolveProfilePaths(profileOverride string) ProfilePaths {
	root := RootHermesHome()
	explicitHome := strings.TrimSpace(os.Getenv("HERMES_HOME")) != ""
	profile := strings.TrimSpace(profileOverride)
	if profile == "" {
		profile = strings.TrimSpace(os.Getenv("HERMES_PROFILE"))
	}
	activeFile := filepath.Join(root, "active_profile")
	if profile == "" && !explicitHome {
		if raw, err := os.ReadFile(activeFile); err == nil {
			p := strings.TrimSpace(string(raw))
			if ValidateProfileName(p) {
				profile = p
			}
		}
	}
	home := root
	if profile == "" || profile == "default" {
		profile = "default"
	} else if ValidateProfileName(profile) {
		home = filepath.Join(root, "profiles", profile)
	} else {
		profile = "default"
	}
	return ProfilePaths{
		RootHome:      root,
		ProfileName:   profile,
		Home:          home,
		ConfigFile:    filepath.Join(home, "config.yaml"),
		EnvFile:       filepath.Join(home, ".env"),
		MemoriesDir:   filepath.Join(home, "memories"),
		SkillsDir:     filepath.Join(home, "skills"),
		CronDir:       filepath.Join(home, "cron"),
		CronJobsFile:  filepath.Join(home, "cron", "jobs.json"),
		ProfilesDir:   filepath.Join(root, "profiles"),
		ActiveProfile: activeFile,
		SoulFile:      filepath.Join(home, "SOUL.md"),
		CacheDir:      filepath.Join(home, "cache"),
	}
}
