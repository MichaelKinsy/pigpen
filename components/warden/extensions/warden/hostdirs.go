package warden

import (
	"os"
	"path/filepath"
	"strings"
)

// DataDir is the directory holding warden's stores (loops, prefs, holds, learning, ...): the directory of the
// configuration file. It is the port's counterpart of upstream dirname(userConfigPath(dirs)), <agentDir>/pi-warden.
func DataDir(configHome string) string { return filepath.Dir(ConfigPath(configHome)) }

// ExpandHome expands a leading "~" or "~/" to the home directory (host-dirs.ts expandHome); the value is trimmed.
// defaultHostDirs and PI_CODING_AGENT_DIR are deliberately not ported here: a later lane owns host paths.
func ExpandHome(value string) string {
	v := strings.TrimSpace(value)
	if v == "~" || strings.HasPrefix(v, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return v
		}
		return filepath.Join(home, v[1:])
	}
	return v
}
