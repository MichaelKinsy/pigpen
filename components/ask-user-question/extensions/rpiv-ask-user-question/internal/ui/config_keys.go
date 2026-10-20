package ui

import "strings"

// The collapse-key constants and display formatter of config.ts that the view layer needs. The root package keeps
// its own copy for config loading (it cannot be imported from here).
const (
	// DefaultCollapseKey is the collapse and expand shortcut when collapseKey is missing or invalid.
	DefaultCollapseKey = "ctrl+]"
	// CollapseKeyOff disables the collapse shortcut entirely.
	CollapseKeyOff = "off"
)

// FormatKeySpecForDisplay renders a key spec for a hint: `ctrl+]` as `Ctrl+]`, `pageup` as `PageUp`. Matching
// always uses the raw lowercase spec, so never feed the result back into it. upstream: config.ts.
func FormatKeySpecForDisplay(spec string) string {
	compound := map[string]string{"pageup": "PageUp", "pagedown": "PageDown"}
	parts := strings.Split(spec, "+")
	for i, p := range parts {
		switch {
		case compound[p] != "":
			parts[i] = compound[p]
		case len([]rune(p)) <= 1:
			parts[i] = strings.ToUpper(p)
		default:
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "+")
}
