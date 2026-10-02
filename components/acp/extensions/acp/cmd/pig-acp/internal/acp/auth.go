package acp

import (
	"os"
	"strings"
)

// PiSetupMethodID is the id of the terminal login method.
const PiSetupMethodID = "pi_terminal_login"

// TerminalLoginArgs are the arguments that make this executable start pig interactively. main
// adds --pig (and friends) so the login uses the same pig the adapter drives.
var TerminalLoginArgs = []string{"--terminal-login"}

// AuthMethods returns the advertised auth methods. Zed renders a banner from
// _meta["terminal-auth"]; the type/args/env shape is what registries require. Both are sent.
func AuthMethods(supportsTerminalAuthMeta bool) []map[string]any {
	args := append([]any{}, anySlice(TerminalLoginArgs)...)
	method := map[string]any{
		"id":          PiSetupMethodID,
		"name":        "Launch pig in the terminal",
		"description": "Start pig in an interactive terminal to configure API keys or login",
		"type":        "terminal",
		"args":        args,
		"env":         map[string]any{},
	}
	if supportsTerminalAuthMeta {
		cmd := "pig-acp"
		if exe, err := os.Executable(); err == nil && exe != "" {
			cmd = exe
		}
		method["_meta"] = map[string]any{"terminal-auth": map[string]any{
			"command": cmd, "args": append([]any{}, anySlice(TerminalLoginArgs)...), "label": "Launch pig",
		}}
	}
	return []map[string]any{method}
}

func anySlice(s []string) []any {
	out := make([]any, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
}

var authPatterns = []string{"api key", "apikey", "missing key", "no key", "not configured", "unauthorized", "authentication", "permission denied", "forbidden", "401", "403"}

// MaybeAuthRequiredError turns a missing-credentials message into an AUTH_REQUIRED error.
func MaybeAuthRequiredError(err error) *RequestError {
	if err == nil {
		return nil
	}
	s := strings.ToLower(err.Error())
	for _, p := range authPatterns {
		if strings.Contains(s, p) {
			return ErrAuthRequired(map[string]any{"authMethods": AuthMethods(true)}, "Configure an API key or log in with an OAuth provider.")
		}
	}
	return nil
}
