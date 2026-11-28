// Package tern ports omp's Tern terminal connections and their non-Tern
// fallbacks to PiG's Go extension SDK.
//
// Each connection checks for Tern the way omp does (`TERM_PROGRAM=tern`,
// `TERN_PANE_SOCKET` + `TERN_PANE`, or `TERN_BLOB_DIR`) and degrades to omp's
// non-Tern behaviour outside Tern. Terminal escapes are emitted only through
// PiG's SDK (`ctx.SetTitle`, `ctx.Bell`); `os.Stdout` is never written to.
package tern

import (
	"os"
	"runtime"
	"strings"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/ternwire"
)

// TerminalID mirrors omp's `TerminalId` from `packages/tui/src/terminal-capabilities.ts`.
// upstream: packages/tui/src/terminal-capabilities.ts:57
type TerminalID string

const (
	TermKitty     TerminalID = "kitty"
	TermGhostty   TerminalID = "ghostty"
	TermWezTerm   TerminalID = "wezterm"
	TermITerm2    TerminalID = "iterm2"
	TermVSCode    TerminalID = "vscode"
	TermAlacritty TerminalID = "alacritty"
	TermWarp      TerminalID = "warp"
	TermOrca      TerminalID = "orca"
	TermOtty      TerminalID = "otty"
	TermRio       TerminalID = "rio"
	TermTern      TerminalID = "tern"
	TermMonstar   TerminalID = "monstar"
	TermRex       TerminalID = "rex"
	TermBase      TerminalID = "base"
	TermTrueColor TerminalID = "trueColor"
)

// NotifyProtocol mirrors omp's `NotifyProtocol`.
// upstream: packages/tui/src/terminal-capabilities.ts:51
type NotifyProtocol string

const (
	NotifyBell  NotifyProtocol = "\x07"
	NotifyOsc99 NotifyProtocol = "\x1b]99;;"
	NotifyOsc9  NotifyProtocol = "\x1b]9;"
)

// EnvLookup is the environment lookup signature used for testable detection.
type EnvLookup func(key string) (string, bool)

func getenv(lookup EnvLookup, key string) string {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	v, _ := lookup(key)
	return v
}

func osLookupEnv(key string) (string, bool) {
	return os.LookupEnv(key)
}

// IsTernProgram reports whether `TERM_PROGRAM` is `"tern"` (case-insensitive).
// This is the check omp uses for Tern-only session reporting and unconditional
// progress reporting (because Tern panes also export `KITTY_WINDOW_ID`).
// upstream: packages/coding-agent/src/utils/title-generator.ts:819
// upstream: packages/coding-agent/src/modes/controllers/event-controller.ts:1220
// upstream: packages/tui/src/terminal.ts:79
func IsTernProgram() bool {
	return IsTernProgramEnv(os.LookupEnv)
}

// IsTernProgramEnv is IsTernProgram with an injectable environment lookup.
func IsTernProgramEnv(lookup EnvLookup) bool {
	return strings.EqualFold(strings.TrimSpace(getenv(lookup, "TERM_PROGRAM")), "tern")
}

// Pane returns the Tern daemon socket path and pane block ID from the process
// environment (`TERN_PANE_SOCKET` and `TERN_PANE`).
// upstream: packages/coding-agent/src/tools/browser/tern/kind.ts:23
func Pane() (socket string, block int, ok bool) {
	return ternwire.Pane()
}

// PaneFromEnv resolves `TERN_PANE_SOCKET` and `TERN_PANE` from lookup.
func PaneFromEnv(lookup EnvLookup) (socket string, block int, ok bool) {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	return ternwire.PaneFromEnv(lookup)
}

// HasBlobDir reports whether `TERN_BLOB_DIR` is set and non-empty (meaning Tern
// is local to this machine and supports path-based native `media` nodes).
// upstream: packages/tui/src/native/backend.ts:184
func HasBlobDir() bool {
	return HasBlobDirEnv(os.LookupEnv)
}

// HasBlobDirEnv is HasBlobDir with an injectable environment lookup.
func HasBlobDirEnv(lookup EnvLookup) bool {
	return strings.TrimSpace(getenv(lookup, "TERN_BLOB_DIR")) != ""
}

// IsWSL reports whether the process is running under WSL: `platform == "linux"`
// and `WSL_DISTRO_NAME` or `WSL_INTEROP` is set.
// upstream: packages/utils/src/env.ts:50-52
func IsWSL(lookup EnvLookup) bool {
	return IsWSLForPlatform(runtime.GOOS, lookup)
}

// IsWSLForPlatform is IsWSL with an explicit GOOS platform for deterministic tests.
// upstream: packages/utils/src/env.ts:50-52
func IsWSLForPlatform(goos string, lookup EnvLookup) bool {
	if goos != "linux" {
		return false
	}
	return getenv(lookup, "WSL_DISTRO_NAME") != "" || getenv(lookup, "WSL_INTEROP") != ""
}

// IsNotificationSuppressed reports whether `PI_NOTIFICATIONS` disables notifications
// (`"off"`, `"0"`, or `"false"`).
// upstream: packages/tui/src/terminal-capabilities.ts:210
func IsNotificationSuppressed(lookup EnvLookup) bool {
	v := getenv(lookup, "PI_NOTIFICATIONS")
	return v == "off" || v == "0" || v == "false"
}

// DetectTerminalID resolves terminal identity from environment markers, matching
// omp's `detectTerminalId`. Note that Tern panes may set `KITTY_WINDOW_ID` for
// Kitty protocol compatibility, in which case `DetectTerminalID` returns
// `TermKitty` just as omp does; callers that specifically care about Tern use
// `IsTernProgramEnv` or `PaneFromEnv`.
// upstream: packages/tui/src/terminal-capabilities.ts:658
func DetectTerminalID(lookup EnvLookup) TerminalID {
	if getenv(lookup, "KITTY_WINDOW_ID") != "" {
		return TermKitty
	}
	if getenv(lookup, "GHOSTTY_RESOURCES_DIR") != "" {
		return TermGhostty
	}
	if getenv(lookup, "WEZTERM_PANE") != "" {
		return TermWezTerm
	}
	if getenv(lookup, "ITERM_SESSION_ID") != "" {
		return TermITerm2
	}
	if getenv(lookup, "VSCODE_PID") != "" {
		return TermVSCode
	}
	if getenv(lookup, "ALACRITTY_WINDOW_ID") != "" {
		return TermAlacritty
	}

	if id, ok := terminalIDFromProgram(getenv(lookup, "TERM_PROGRAM")); ok {
		return id
	}

	if strings.EqualFold(getenv(lookup, "LC_TERMINAL"), "iTerm2") && !isInsideMultiplexer(lookup) {
		return TermITerm2
	}

	term := getenv(lookup, "TERM")
	if strings.Contains(strings.ToLower(term), "ghostty") {
		return TermGhostty
	}
	if strings.EqualFold(term, "monstar") {
		return TermMonstar
	}
	if strings.EqualFold(term, "xterm-rex") {
		return TermRex
	}

	colorTerm := getenv(lookup, "COLORTERM")
	if strings.EqualFold(colorTerm, "truecolor") || strings.EqualFold(colorTerm, "24bit") {
		return TermTrueColor
	}
	return TermBase
}

func terminalIDFromProgram(program string) (TerminalID, bool) {
	switch strings.ToLower(program) {
	case "kitty":
		return TermKitty, true
	case "ghostty":
		return TermGhostty, true
	case "wezterm":
		return TermWezTerm, true
	case "iterm.app", "iterm2":
		return TermITerm2, true
	case "vscode":
		return TermVSCode, true
	case "alacritty":
		return TermAlacritty, true
	case "warpterminal":
		return TermWarp, true
	case "orca":
		return TermOrca, true
	case "otty":
		return TermOtty, true
	case "rio":
		return TermRio, true
	case "tern":
		return TermTern, true
	case "monstar":
		return TermMonstar, true
	case "rex":
		return TermRex, true
	default:
		return "", false
	}
}

func isInsideMultiplexer(lookup EnvLookup) bool {
	if getenv(lookup, "TMUX") != "" || getenv(lookup, "STY") != "" || getenv(lookup, "ZELLIJ") != "" {
		return true
	}
	if getenv(lookup, "HERDR_ENV") == "1" || getenv(lookup, "HERDR_PANE_ID") != "" ||
		getenv(lookup, "HERDR_TAB_ID") != "" || getenv(lookup, "HERDR_WORKSPACE_ID") != "" {
		return true
	}
	if getenv(lookup, "CMUX_WORKSPACE_ID") != "" || getenv(lookup, "CMUX_SURFACE_ID") != "" ||
		getenv(lookup, "CMUX_REMOTE_TRANSPORT") != "" {
		return true
	}
	if getenv(lookup, "WMUX") == "1" || getenv(lookup, "WMUX_SURFACE_ID") != "" {
		return true
	}
	return false
}

// NotifyProtocolFor returns omp's static notification protocol for terminalID.
// upstream: packages/tui/src/terminal-capabilities.ts:591-655
func NotifyProtocolFor(id TerminalID) NotifyProtocol {
	switch id {
	case TermKitty, TermOtty, TermTern:
		return NotifyOsc99
	case TermGhostty, TermWezTerm, TermITerm2, TermWarp, TermMonstar:
		return NotifyOsc9
	default:
		return NotifyBell
	}
}
