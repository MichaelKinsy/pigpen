package mapper

// Shared display-title rules for live, catalogued and hydrated sessions (src/pi/session-title.ts).

const (
	// NewSessionTitle is the title of a session nobody has named or spoken in yet.
	NewSessionTitle      = "New Session"
	untitledSessionTitle = "Untitled session"
	titleFallbackLength  = 60
)

// FallbackSessionTitle is what pi displays when no explicit session name exists: the first user
// message, collapsed. ok is false when there is nothing to show.
func FallbackSessionTitle(text string) (string, bool) {
	collapsed := collapseSpace(text)
	if collapsed == "" {
		return "", false
	}
	if utf16Len(collapsed) > titleFallbackLength {
		return sliceUTF16(collapsed, 0, titleFallbackLength-1) + "…", true
	}
	return collapsed, true
}

// SessionDisplayTitle is the explicit name, else the first user message, else "Untitled session".
func SessionDisplayTitle(name, firstUserMessage string) string {
	if n := trimJS(name); n != "" {
		return n
	}
	if t, ok := FallbackSessionTitle(firstUserMessage); ok {
		return t
	}
	return untitledSessionTitle
}
