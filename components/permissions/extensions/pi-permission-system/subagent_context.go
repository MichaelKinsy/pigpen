package pi_permission_system

import "os"

// Port of src/authority/subagent-context.ts: is this session a subagent child? Three signals, in order: the in-process registry,
// the environment hints process-based subagent extensions set, and the session directory sitting under a known subagent root.

// SubagentPathFlavor is the part of the paths lane's PathFlavor (src/path/path-flavor.ts) that detection reads: the platform's
// path.normalize (`flavor.impl.normalize`), its comparison case fold, and the path.relative-based containment test.
// `PathFlavor` satisfies it once it gains `Normalize(string) string` (one line over `Impl.Normalize`).
type SubagentPathFlavor interface {
	Normalize(path string) string
	Fold(value string) string
	IsWithin(pathValue, directory string) bool
}

// SubagentDetectionSessionManager is the only session-manager readers detection consumes. An error is the original's throw.
type SubagentDetectionSessionManager interface {
	GetSessionID() (string, error)
	GetSessionDir() (string, error)
}

// SubagentDetectionContext is the narrow context for subagent detection. A ForwarderContext's SessionManager satisfies it.
type SubagentDetectionContext struct {
	SessionManager SubagentDetectionSessionManager
}

// ReadSessionID is session/session-identity.ts readSessionId: the session id the node belongs to, or false when the host exposes
// none (absent, empty, or an error).
func ReadSessionID(m SessionIdentityManager) (string, bool) {
	if m == nil {
		return "", false
	}
	id, err := m.GetSessionID()
	if err != nil || id == "" {
		return "", false
	}
	return id, true
}

// NormalizeFilesystemPath is normalizeFilesystemPath: the flavor's normalization, then its comparison fold.
func NormalizeFilesystemPath(pathValue string, flavor SubagentPathFlavor) string {
	return flavor.Fold(flavor.Normalize(pathValue))
}

// IsRegisteredSubagentChild reports whether ctx belongs to an in-process subagent child registered in registry by its session id.
// An unreachable session id names no registration, so it is not a child.
func IsRegisteredSubagentChild(ctx SubagentDetectionContext, registry *SubagentSessionRegistry) bool {
	sessionID, ok := ReadSessionID(ctx.SessionManager)
	return ok && registry.Has(sessionID)
}

// IsSubagentExecutionContext is isSubagentExecutionContext. registry may be nil. The error is a failing GetSessionDir, which the
// original lets propagate.
func IsSubagentExecutionContext(ctx SubagentDetectionContext, subagentSessionsDir string, flavor SubagentPathFlavor, registry *SubagentSessionRegistry) (bool, error) {
	// 1. Explicit registry: in-process subagent extensions register by child session id before bindExtensions(); checked first so it
	// takes priority over heuristics.
	if registry != nil && IsRegisteredSubagentChild(ctx, registry) {
		return true, nil
	}

	sessionDir, err := ctx.SessionManager.GetSessionDir()
	if err != nil {
		return false, err
	}

	// 2. Env vars: process-based subagent extensions (nicobailon/pi-subagents, HazAT/pi-interactive-subagents, pi-agent-router, ...).
	for _, key := range SubagentEnvHintKeys {
		if jsTrim(os.Getenv(key)) != "" {
			return true, nil
		}
	}

	// 3. Filesystem path: fallback heuristic for extensions that store sessions under a known subagent root directory.
	if sessionDir == "" {
		return false, nil
	}
	normalizedSessionDir := NormalizeFilesystemPath(sessionDir, flavor)
	normalizedSubagentRoot := NormalizeFilesystemPath(subagentSessionsDir, flavor)
	return flavor.IsWithin(normalizedSessionDir, normalizedSubagentRoot), nil
}
