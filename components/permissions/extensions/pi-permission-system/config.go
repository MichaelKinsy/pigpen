package pi_permission_system

import (
	"os"
	"path/filepath"
)

// The extension id and the agent directory (the original reads it from Pi: getAgentDir()).

const extensionID = "pi-permission-system"

func agentDir() string {
	if o := jsTrim(os.Getenv("PI_CODING_AGENT_DIR")); o != "" {
		if filepath.IsAbs(o) {
			return filepath.Clean(o)
		}
		return filepath.Join(homeDir(), o)
	}
	return filepath.Join(homeDir(), ".pi", "agent")
}

func globalConfigPath() string {
	return filepath.Join(agentDir(), "extensions", extensionID, "config.json")
}

func tagged(sentence string) string { return "[" + extensionID + "] " + sentence }

// detectPermissiveBashFallback (config-loader.ts) warns when a top-level `*: allow` leaves bash without a policy of its own.
func detectPermissiveBashFallback(permission *jsObject) string {
	if permission == nil {
		return ""
	}
	if s, _ := permission.vals["*"].(string); s != "allow" {
		return ""
	}
	switch bash := permission.vals["bash"].(type) {
	case string:
		return ""
	case *jsObject:
		if _, has := bash.vals["*"]; has {
			return ""
		}
	}
	return "Permission config sets a permissive top-level '*': 'allow' with no 'bash' '*' policy, " +
		"so bash commands silently inherit 'allow'. Set an explicit 'bash' policy " +
		`(e.g. "bash": { "*": "ask" }) to gate bash commands.`
}
