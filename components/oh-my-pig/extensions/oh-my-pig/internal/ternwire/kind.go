package ternwire

import (
	"os"
	"strconv"
	"strings"
)

// Pane returns the Tern daemon socket path and pane block ID from the process
// environment (TERN_PANE_SOCKET and TERN_PANE).
// upstream: packages/coding-agent/src/tools/browser/tern/kind.ts:23
func Pane() (socket string, block int, ok bool) {
	return PaneFromEnv(os.LookupEnv)
}

// PaneFromEnv resolves TERN_PANE_SOCKET and TERN_PANE from lookup.
// upstream: packages/coding-agent/src/tools/browser/tern/kind.ts:23
func PaneFromEnv(lookup func(string) (string, bool)) (socket string, block int, ok bool) {
	rawSocket, _ := lookup("TERN_PANE_SOCKET")
	rawPane, _ := lookup("TERN_PANE")
	socket = strings.TrimSpace(rawSocket)
	paneStr := strings.TrimSpace(rawPane)
	if socket == "" || paneStr == "" {
		return "", 0, false
	}
	for i := range len(paneStr) {
		if paneStr[i] < '0' || paneStr[i] > '9' {
			return "", 0, false
		}
	}
	id, err := strconv.Atoi(paneStr)
	if err != nil || id < 0 {
		return "", 0, false
	}
	return socket, id, true
}

// truthyFlags mirrors upstream packages/utils/src/env.ts:548-563.
var truthyFlags = map[string]bool{
	"1":    true,
	"Y":    true,
	"y":    true,
	"TRUE": true,
	"true": true,
	"YES":  true,
	"yes":  true,
	"ON":   true,
	"on":   true,
}

// ParseFlag parses a boolean-ish env value using omp's exact truthy table;
// returns defaultVal when unset/empty, false for any other non-empty value.
// upstream: packages/utils/src/env.ts:548-563
func ParseFlag(raw string, defaultVal bool) bool {
	if raw == "" {
		return defaultVal
	}
	return truthyFlags[raw]
}

// ResolveKind resolves Tern browser availability from the browser.tern setting
// and PI_BROWSER_TERN override, plus TERN_PANE_SOCKET / TERN_PANE.
// upstream: packages/coding-agent/src/tools/browser/tern/kind.ts:43
func ResolveKind(settingEnabled bool, lookup func(string) (string, bool)) (socket string, block int, ok bool) {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	flagVal, _ := lookup("PI_BROWSER_TERN")
	if !ParseFlag(flagVal, settingEnabled) {
		return "", 0, false
	}
	return PaneFromEnv(lookup)
}
