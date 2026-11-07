package acp

import (
	"errors"
	"os"
	"strings"
)

// PiSetupMethodID is the id of the terminal login method.
const PiSetupMethodID = "pi_terminal_login"

// TerminalLoginArgs are the arguments that make this executable start pig interactively. main
// adds --pig (and friends) so the login uses the same pig the adapter drives.
var TerminalLoginArgs = []string{"--terminal-login"}

// AuthSupport is what the client said it can do for a terminal sign-in. ACP v1 lets an agent advertise a `terminal`
// authentication method only when the client advertised clientCapabilities.auth.terminal; Zed's older banner is driven
// by clientCapabilities._meta["terminal-auth"] instead.
type AuthSupport struct {
	// Terminal is clientCapabilities.auth.terminal == true.
	Terminal bool
	// ZedMeta is clientCapabilities._meta["terminal-auth"] == true.
	ZedMeta bool
}

// AuthSupportOf reads the sign-in capabilities from the initialize request's clientCapabilities.
func AuthSupportOf(caps map[string]any) AuthSupport {
	var s AuthSupport
	if auth, ok := caps["auth"].(map[string]any); ok {
		s.Terminal = auth["terminal"] == true
	}
	if meta, ok := caps["_meta"].(map[string]any); ok {
		s.ZedMeta = meta["terminal-auth"] == true
	}
	return s
}

// AuthMethods returns the auth methods this client can use, and none for a client that advertised neither capability:
// advertising a terminal method to a client that cannot run one is outside the protocol (ACP v1, Authentication,
// "An Agent may advertise a `terminal` method only when this capability is `true`"). With auth.terminal the method is
// the spec's `terminal` type (args and env); with Zed's _meta flag it also carries the `terminal-auth` banner
// description. A Zed-only client gets the method without a type, which ACP reads as the default `agent` type.
func AuthMethods(s AuthSupport) []map[string]any {
	if !s.Terminal && !s.ZedMeta {
		return []map[string]any{}
	}
	method := map[string]any{
		"id":          PiSetupMethodID,
		"name":        "Launch pig in the terminal",
		"description": "Start pig in an interactive terminal to configure API keys or login",
	}
	if s.Terminal {
		method["type"] = "terminal"
		method["args"] = anySlice(TerminalLoginArgs)
		method["env"] = map[string]any{}
	}
	if s.ZedMeta {
		cmd := "pig-acp"
		if exe, err := os.Executable(); err == nil && exe != "" {
			cmd = exe
		}
		method["_meta"] = map[string]any{"terminal-auth": map[string]any{
			"command": cmd, "args": anySlice(TerminalLoginArgs), "label": "Launch pig",
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

// IsAuthFailure reports whether err reads as missing or rejected credentials.
func IsAuthFailure(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, p := range authPatterns {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

// errAuthFailure marks a turn that failed for want of credentials. The session cannot know what the client can do
// about it; Agent.Prompt turns it into AuthRequiredError for the client that is connected.
var errAuthFailure = errors.New("authentication failed")

// authMessage is the human text of the sign-in error.
const authMessage = "Configure an API key or log in with an OAuth provider."

// noAuthMessage is the error for a client that can run no sign-in flow: ACP's -32000 "authentication required" is
// only meaningful with an advertised method (the remedy), so this client gets an ordinary internal error that says
// what to do outside the editor.
const noAuthMessage = "pig has no usable model or API key: none is configured, or the one configured was rejected. Run pig in a terminal and sign in (`/login`) or fix the provider API key, then try again."

// AuthRequiredError is the error for missing credentials, shaped to what the client can do about it: -32000 with the
// advertised methods, or, when none was advertised, an internal error that names the remedy.
func AuthRequiredError(s AuthSupport) *RequestError {
	methods := AuthMethods(s)
	if len(methods) == 0 {
		return ErrInternal(map[string]any{}, noAuthMessage)
	}
	return ErrAuthRequired(map[string]any{"authMethods": methods}, authMessage)
}

// MaybeAuthRequiredError turns a missing-credentials message into the error for this client, or nil for any other error.
func MaybeAuthRequiredError(err error, s AuthSupport) *RequestError {
	if !IsAuthFailure(err) {
		return nil
	}
	return AuthRequiredError(s)
}
