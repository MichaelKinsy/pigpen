package pi_permission_system

import "os"

// Port of src/config/extension-config.ts: the runtime knobs the extension reads after the unified config is normalized.

// PermissionSystemExtensionConfig is the runtime config. The four booleans always carry a value; the other fields are nil when the
// config does not name them, leaving the consumer's default.
type PermissionSystemExtensionConfig struct {
	DebugLog            bool
	PermissionReviewLog bool
	YoloMode            bool
	// DoublePressToConfirm requires a confirming second press of a decision hotkey in the inline TUI dialog. Defaults to true.
	DoublePressToConfirm bool
	// PiInfrastructureReadPaths are additional directories to auto-allow for reads as Pi infrastructure.
	PiInfrastructureReadPaths []string
	// ForwardingTimeoutMs is how long a subagent waits for the parent's answer to a forwarded ask. Defaults to 600000.
	ForwardingTimeoutMs *float64
	// PromptMaxRows is the rows a permission prompt renders before eliding its evidence. Defaults to 24.
	PromptMaxRows *float64
	// PromptFieldMaxWidth is the characters of any one field shown in a prompt. Defaults to 400.
	PromptFieldMaxWidth *float64
	// ReviewLogFieldMaxWidth is the characters of any one value written to the review log. Defaults to 1000.
	ReviewLogFieldMaxWidth *float64
	// PermissionDialogKeys are the dialog's hotkey bindings as configured; resolved at prompt time.
	PermissionDialogKeys DialogKeyOverrides
	// PromptNotifications are the terminal notifications the dialog emits as it opens; nil means none.
	PromptNotifications []string
	// ShellTools are the non-bash tools that carry shell semantics, keyed by tool name.
	ShellTools *jsObject
	// AuthorizerChain names the registered live-authority chain links to consult before the terminal authorizer.
	AuthorizerChain []string
}

// defaultExtensionConfig is DEFAULT_EXTENSION_CONFIG.
func defaultExtensionConfig() PermissionSystemExtensionConfig {
	return PermissionSystemExtensionConfig{PermissionReviewLog: true, DoublePressToConfirm: true}
}

var permissionPolicyKeys = map[string]bool{"defaultPolicy": true, "tools": true, "bash": true, "mcp": true, "skills": true, "special": true, "external_directory": true}

// detectMisplacedPermissionKeys returns the legacy permission-rule keys found at the top level of a raw config record.
func detectMisplacedPermissionKeys(raw *jsObject) []string {
	out := []string{}
	for _, k := range raw.order() {
		if permissionPolicyKeys[k] {
			out = append(out, k)
		}
	}
	return out
}

// normalizePermissionSystemConfig applies the runtime defaults to a merged unified config. The two deprecated preview caps are
// deliberately dropped: no runtime consumer may read one (ADR 0011 section 5, #745).
func normalizePermissionSystemConfig(raw UnifiedConfig) PermissionSystemExtensionConfig {
	return PermissionSystemExtensionConfig{
		DebugLog:                  raw.DebugLog != nil && *raw.DebugLog,
		PermissionReviewLog:       raw.PermissionReviewLog == nil || *raw.PermissionReviewLog,
		YoloMode:                  raw.YoloMode != nil && *raw.YoloMode,
		DoublePressToConfirm:      raw.DoublePressToConfirm == nil || *raw.DoublePressToConfirm,
		PiInfrastructureReadPaths: raw.PiInfrastructureReadPaths,
		ForwardingTimeoutMs:       raw.ForwardingTimeoutMs,
		PromptMaxRows:             raw.PromptMaxRows,
		PromptFieldMaxWidth:       raw.PromptFieldMaxWidth,
		ReviewLogFieldMaxWidth:    raw.ReviewLogFieldMaxWidth,
		PermissionDialogKeys:      raw.PermissionDialogKeys,
		PromptNotifications:       raw.PromptNotifications,
		ShellTools:                raw.ShellTools,
		AuthorizerChain:           raw.AuthorizerChain,
	}
}

func isYoloModeEnabled(c PermissionSystemExtensionConfig) bool { return c.YoloMode }

// ensurePermissionSystemLogsDirectory creates the logs directory owner-only and tightens one inherited from an earlier version.
// It returns "" on success, else the message the original reports.
func ensurePermissionSystemLogsDirectory(logsDir string) string {
	if err := os.MkdirAll(logsDir, OwnerOnlyDirectoryMode); err != nil {
		return "Failed to create permission-system log directory '" + logsDir + "': " + nodeFsMessage(err, "mkdir", logsDir)
	}
	_ = os.Chmod(logsDir, OwnerOnlyDirectoryMode) // best effort, as restrictExistingPathToOwner
	return ""
}

// mcpNameLess orders two server names as `right.length - left.length || left.localeCompare(right)` sorts them.
func mcpNameLess(a, b string) bool {
	if la, lb := utf16Len(a), utf16Len(b); la != lb {
		return la > lb
	}
	return icuCompare(a, b) < 0
}
