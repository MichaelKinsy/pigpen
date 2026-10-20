package pi_permission_system

import "strings"

// Port of src/config/dialog-keys.ts: the inline permission dialog's key bindings and how a config's overrides reconcile with them.

// PromptAction is one of the dialog's five decisions, named by what it does (the `permissionDialogKeys` config keys).
type PromptAction string

const (
	ActionApprove            PromptAction = "approve"
	ActionApproveSession     PromptAction = "approveSession"
	ActionApproveSessionBoth PromptAction = "approveSessionBoth"
	ActionDeny               PromptAction = "deny"
	ActionDenyWithReason     PromptAction = "denyWithReason"
)

// DialogKeyBindings is every action's bound character, complete. DialogKeyOverrides is what a config may say: any subset.
type DialogKeyBindings map[PromptAction]string
type DialogKeyOverrides map[PromptAction]string

// DialogKeyResolution is the bindings to use and one sentence per refused override.
type DialogKeyResolution struct {
	Keys   DialogKeyBindings
	Issues []string
}

// actionOrder is the order a config's overrides are considered.
var actionOrder = []PromptAction{ActionApprove, ActionApproveSession, ActionApproveSessionBoth, ActionDeny, ActionDenyWithReason}

func defaultDialogKeys() DialogKeyBindings {
	return DialogKeyBindings{ActionApprove: "y", ActionApproveSession: "s", ActionApproveSessionBoth: "b", ActionDeny: "n", ActionDenyWithReason: "r"}
}

// dialogKeySymbols is pi-tui's symbol keys minus `+`, which separates a modifier from its key in a key identifier.
const dialogKeySymbols = "`-=[]\\;',./!@#$%^&*()_|~{}:<>?"

const bindableDialogKeyCharacters = "abcdefghijklmnopqrstuvwxyz0123456789" + dialogKeySymbols

// isBindableDialogKey reports whether value is a single character the dialog can bind a decision to. Uppercase is excluded
// rather than normalized: pi-tui lowercases a key identifier.
func isBindableDialogKey(value string) bool {
	return len(value) == 1 && strings.Contains(bindableDialogKeyCharacters, value)
}

// reservedDialogKeys answer to navigation before any decision does.
var reservedDialogKeys = []string{"j", "k"}

// resolveDialogKeys reconciles a config's bindings against the defaults, tolerantly: an unusable, reserved or colliding override
// is refused and its decision keeps the shipped letter.
func resolveDialogKeys(overrides DialogKeyOverrides) DialogKeyResolution {
	issues := []string{}
	accepted := map[PromptAction]string{}
	defaults := defaultDialogKeys()
	for _, action := range actionOrder {
		value, ok := overrides[action]
		if !ok {
			continue
		}
		if !isBindableDialogKey(value) {
			issues = append(issues, dialogRefusal(action, value, "is not a bindable key. Use one lowercase letter, digit, or symbol"))
			continue
		}
		reserved := false
		for _, r := range reservedDialogKeys {
			reserved = reserved || r == value
		}
		if reserved {
			issues = append(issues, dialogRefusal(action, value, "is reserved for moving the dialog's highlight"))
			continue
		}
		accepted[action] = value
	}
	// Dropping an override restores its default, which can collide with a surviving override, so this settles.
	for round := len(actionOrder); round > 0; round-- {
		contested := contestedCharacters(accepted, defaults)
		if len(contested) == 0 {
			break
		}
		// Map iteration order in JavaScript is insertion order of `accepted`, which is the action order.
		for _, action := range actionOrder {
			value, ok := accepted[action]
			if !ok || !contested[value] {
				continue
			}
			delete(accepted, action)
			issues = append(issues, dialogRefusal(action, value, "is already bound to another decision"))
		}
	}
	keys := DialogKeyBindings{}
	for a, v := range defaults {
		keys[a] = v
	}
	for a, v := range accepted {
		keys[a] = v
	}
	return DialogKeyResolution{Keys: keys, Issues: issues}
}

func contestedCharacters(accepted map[PromptAction]string, defaults DialogKeyBindings) map[string]bool {
	seen := map[string]bool{}
	contested := map[string]bool{}
	for _, action := range actionOrder {
		value, ok := accepted[action]
		if !ok {
			value = defaults[action]
		}
		if seen[value] {
			contested[value] = true
		}
		seen[value] = true
	}
	return contested
}

func dialogRefusal(action PromptAction, value, why string) string {
	return "permissionDialogKeys." + string(action) + `: "` + value + `" ` + why + `; keeping the default "` + defaultDialogKeys()[action] + `".`
}
