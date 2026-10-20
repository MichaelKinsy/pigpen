package pi_permission_system

// Port of src/authority/forwarder-context.ts: the narrow context the forwarding subsystem reads. The session manager is the SDK's
// (`sdk.SessionManager` satisfies ForwarderSessionManager as it stands, and the SDK context's Cwd and HasUI fill the other fields),
// so production callers build a ForwarderContext from `ctx` without an adapter layer.

// SessionEntryView is a session entry as the active-agent helpers read it (a decoded JSON object).
type SessionEntryView = map[string]any

// SessionIdentityManager is the only session-manager reader ReadSessionID consumes.
type SessionIdentityManager interface {
	GetSessionID() (string, error)
}

// ForwarderSessionManager is the three session-manager readers getSessionId and the active-agent helpers use. An error is the
// original's throw.
type ForwarderSessionManager interface {
	GetSessionID() (string, error)
	GetSessionDir() (string, error)
	GetEntries() ([]SessionEntryView, error)
}

// PermissionDecisionUi is the dialog UI surface (permission-dialog.ts PermissionDecisionUi); ok false is the original's
// undefined (dismissed).
type PermissionDecisionUi interface {
	Select(title string, options []string) (choice string, ok bool, err error)
	Input(title, placeholder string) (value string, ok bool, err error)
}

// ForwarderContext is the narrow context the forwarding subsystem reads: the UI gate, the dialog UI, the working directory (stamped
// onto a forwarded request as the requester cwd) and the session manager.
type ForwarderContext struct {
	HasUI          bool
	UI             PermissionDecisionUi
	Cwd            string
	SessionManager ForwarderSessionManager
}

// GetCwd reads the current session cwd off ctx.
func GetCwd(ctx ForwarderContext) string { return ctx.Cwd }

// GetSessionID reads the current session id off ctx, falling back to "unknown" when the host throws or answers nothing usable.
func GetSessionID(ctx ForwarderContext) string {
	if ctx.SessionManager == nil {
		return "unknown"
	}
	if id, err := ctx.SessionManager.GetSessionID(); err == nil {
		if t := jsTrim(id); t != "" {
			return t
		}
	}
	return "unknown"
}
