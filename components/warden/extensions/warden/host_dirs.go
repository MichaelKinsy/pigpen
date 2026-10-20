package warden

import (
	"os"
	"path/filepath"
)

// HostDirs is where this host keeps warden's files (host-dirs.ts HostDirs). Upstream's pair is the agent directory
// (`~/.pi/agent`, `~/.omp/agent` on Oh My Pi) and the project config directory name (`.pi`, `.omp`). On PiG the port's
// own data does not live under the agent directory (D5): it lives under the PiG config home, `<PIG_HOME>/pigpen-warden`
// (ConfigPath, DataDir). So the first field is the config home, which is what Context.ConfigHome reports
// (PIG_HOME, else ~/.pig), and the second is PiG's project directory name: `.pig`, or `.pi` when the user shares Pi's
// directories (PIG_USE_PI_DIRS=1, the same switch PiG itself reads).
type HostDirs struct {
	// ConfigHome is the PiG config root.
	ConfigHome string
	// ConfigDirName is the per-project configuration directory name.
	ConfigDirName string
}

// DefaultHostDirs resolves the directories the way PiG would: PIG_HOME (a leading `~` expands, surrounding space is
// trimmed, blank is unset) else ~/.pig; `.pig`, or `.pi` under PIG_USE_PI_DIRS=1. getenv nil reads the process
// environment.
func DefaultHostDirs(getenv func(string) string) HostDirs {
	if getenv == nil {
		getenv = os.Getenv
	}
	dirs := HostDirs{ConfigDirName: ".pig"}
	if getenv("PIG_USE_PI_DIRS") == "1" {
		dirs.ConfigDirName = ".pi"
	}
	if configured := ExpandHome(getenv("PIG_HOME")); configured != "" {
		dirs.ConfigHome = configured
	} else if home, err := os.UserHomeDir(); err == nil {
		dirs.ConfigHome = filepath.Join(home, ".pig")
	}
	return dirs
}

// UserConfigPath is the user file under these directories (config.ts userConfigPath).
func (d HostDirs) UserConfigPath() string { return ConfigPath(d.ConfigHome) }

// ProjectConfigPath is the project file for a working directory (config.ts projectConfigPath).
func (d HostDirs) ProjectConfigPath(cwd string) string {
	return filepath.Join(cwd, d.ConfigDirName, ProjectConfigFile)
}
