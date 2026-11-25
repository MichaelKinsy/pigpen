package lsp

import (
	"fmt"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// upstream: packages/coding-agent/src/lsp/settings.ts, packages/coding-agent/src/tools/tool-timeouts.ts

const (
	ToolTimeoutDefaultSec = 20
	ToolTimeoutMinSec     = 5
	ToolTimeoutMaxSec     = 300
)

// Settings mirrors omp's `lsp.*` settings with omp's exact defaults.
type Settings struct {
	Enabled                bool
	Lazy                   bool
	Shared                 bool
	FormatOnWrite          bool
	DiagnosticsOnWrite     bool
	DiagnosticsOnEdit      bool
	DiagnosticsDeduplicate bool
	RequestTimeoutSec      int
	ToolsMaxTimeoutSec     int
	SymbolPreset           string
}

// DefaultSettings returns omp's default `lsp.*` configuration.
func DefaultSettings() Settings {
	return Settings{
		Enabled:                true,
		Lazy:                   true,
		Shared:                 true,
		FormatOnWrite:          false,
		DiagnosticsOnWrite:     true,
		DiagnosticsOnEdit:      false,
		DiagnosticsDeduplicate: true,
		RequestTimeoutSec:      10,
		ToolsMaxTimeoutSec:     0,
		SymbolPreset:           "unicode",
	}
}

// SettingsReader is an optional hook the integrator wires to `internal/settings`
// so `lsp.*` keys edited via `cfg://` reach this package. When nil, readSettings
// reads PiG's host settings map via `ctx.GetSettings()`.
var (
	hooksMu        sync.RWMutex
	SettingsReader func(ctx sdk.Context) (map[string]any, error)
	// Setting reads a single setting key from `internal/settings` (e.g. "symbolPreset").
	// Optional hook wired by the integrator; when nil, falls back to SettingsReader / ctx.GetSettings().
	Setting             func(ctx sdk.Context, key string) any
	projectTrustChecker func(ctx sdk.Context) (bool, error)
	sessionIDResolver   func(ctx sdk.Context) (string, error)
)

// Enabled reports whether LSP is enabled for ctx (`lsp.enabled`, default true).
// Exposed for `internal/toolcmd` and the integrator to hide the `lsp` tool when
// LSP is disabled (matching omp's `tools/index.ts:810` and `lsp/tool.ts:345-347`).
func Enabled(ctx sdk.Context) bool {
	return readSettings(ctx).Enabled
}

func safeGetSettings(ctx sdk.Context) (settings sdk.Settings, err error) {
	defer func() {
		if r := recover(); r != nil {
			settings = nil
			err = fmt.Errorf("no host settings")
		}
	}()
	return ctx.GetSettings()
}

func readSettings(ctx sdk.Context) Settings {
	s := DefaultSettings()
	hooksMu.RLock()
	reader := SettingsReader
	settingFn := Setting
	hooksMu.RUnlock()

	var raw map[string]any
	var err error
	if reader != nil {
		raw, err = reader(ctx)
	} else {
		raw, err = safeGetSettings(ctx)
	}
	if err == nil && raw != nil {
		if v, ok := lookupBool(raw, "lsp.enabled"); ok {
			s.Enabled = v
		}
		if v, ok := lookupBool(raw, "lsp.lazy"); ok {
			s.Lazy = v
		}
		if v, ok := lookupBool(raw, "lsp.shared"); ok {
			s.Shared = v
		}
		if v, ok := lookupBool(raw, "lsp.formatOnWrite"); ok {
			s.FormatOnWrite = v
		}
		if v, ok := lookupBool(raw, "lsp.diagnosticsOnWrite"); ok {
			s.DiagnosticsOnWrite = v
		}
		if v, ok := lookupBool(raw, "lsp.diagnosticsOnEdit"); ok {
			s.DiagnosticsOnEdit = v
		}
		if v, ok := lookupBool(raw, "lsp.diagnosticsDeduplicate"); ok {
			s.DiagnosticsDeduplicate = v
		}
		if v, ok := lookupInt(raw, "lsp.requestTimeout"); ok && v > 0 {
			s.RequestTimeoutSec = v
		}
		if v, ok := lookupInt(raw, "tools.maxTimeout"); ok && v > 0 {
			s.ToolsMaxTimeoutSec = v
		}
		if v, ok := lookupString(raw, "symbolPreset"); ok && v != "" {
			s.SymbolPreset = v
		}
	}
	if settingFn != nil {
		if v, ok := settingFn(ctx, "symbolPreset").(string); ok && v != "" {
			s.SymbolPreset = v
		}
	}
	return s
}

// isProjectTrusted queries PiG's project trust status via ctx.IsProjectTrusted().
// A host error or missing host context fails closed (untrusted == false).
func isProjectTrusted(ctx sdk.Context) bool {
	hooksMu.RLock()
	checker := projectTrustChecker
	hooksMu.RUnlock()
	if checker != nil {
		trusted, err := checker(ctx)
		return err == nil && trusted
	}
	trusted, err := safeIsProjectTrusted(ctx)
	return err == nil && trusted
}

func safeIsProjectTrusted(ctx sdk.Context) (trusted bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			trusted = false
			err = fmt.Errorf("no host trust state")
		}
	}()
	return ctx.IsProjectTrusted()
}

// resolveSessionID returns the host session id, or "" if unavailable.
// Callers must never map "" to a shared default session.
func resolveSessionID(ctx sdk.Context) string {
	hooksMu.RLock()
	resolver := sessionIDResolver
	hooksMu.RUnlock()
	if resolver != nil {
		id, err := resolver(ctx)
		if err != nil {
			return ""
		}
		return id
	}
	id, err := safeSessionID(ctx)
	if err != nil {
		return ""
	}
	return id
}

func safeSessionID(ctx sdk.Context) (id string, err error) {
	defer func() {
		if r := recover(); r != nil {
			id = ""
			err = fmt.Errorf("no host session")
		}
	}()
	return ctx.SessionManager().GetSessionID()
}

// hasValidGateContext reports whether sdkCtx can safely be dereferenced by a
// gate policy (such as modes.planGatePolicy, which calls ctx.GetSessionID()).
// A zero sdk.Context has a nil ext pointer and panics on ctx.GetSessionID(), so
// it returns false and causes server-initiated workspace/applyEdit to be refused.
func hasValidGateContext(ctx sdk.Context) (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			ok = false
		}
	}()
	id, err := ctx.GetSessionID()
	return err == nil && strings.TrimSpace(id) != ""
}

func safeCwd(ctx sdk.Context) (cwd string) {
	defer func() {
		if r := recover(); r != nil {
			cwd = ""
		}
	}()
	return ctx.Cwd()
}

func lookupSetting(m map[string]any, dottedKey string) (any, bool) {
	if m == nil {
		return nil, false
	}
	if v, ok := m[dottedKey]; ok {
		return v, true
	}
	parts := strings.Split(dottedKey, ".")
	var cur any = m
	for _, p := range parts {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		next, exists := obj[p]
		if !exists {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

func lookupBool(m map[string]any, key string) (bool, bool) {
	v, ok := lookupSetting(m, key)
	if !ok {
		return false, false
	}
	b, ok := v.(bool)
	return b, ok
}

func lookupInt(m map[string]any, key string) (int, bool) {
	v, ok := lookupSetting(m, key)
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

func lookupString(m map[string]any, key string) (string, bool) {
	v, ok := lookupSetting(m, key)
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// clampTimeout mirrors packages/coding-agent/src/tools/tool-timeouts.ts:clampTimeout("lsp", ...).
func clampTimeout(raw *float64, maxTimeoutSec int) int {
	val := float64(ToolTimeoutDefaultSec)
	if raw != nil {
		val = *raw
	}
	if maxTimeoutSec > 0 && val > float64(maxTimeoutSec) {
		val = float64(maxTimeoutSec)
	}
	if val < float64(ToolTimeoutMinSec) {
		return ToolTimeoutMinSec
	}
	if val > float64(ToolTimeoutMaxSec) {
		return ToolTimeoutMaxSec
	}
	return int(val)
}

// lspTimeouts holds the resolved call and project-aware wait budgets in seconds.
// upstream: packages/coding-agent/src/tools/tool-timeouts.ts:resolveLspTimeouts
type lspTimeouts struct {
	call int
	wait int
}

func resolveLspTimeouts(requested *float64, requestTimeoutSec, maxTimeoutSec int) lspTimeouts {
	if requested != nil {
		sec := clampTimeout(requested, maxTimeoutSec)
		return lspTimeouts{call: sec, wait: sec}
	}
	callVal := float64(ToolTimeoutDefaultSec)
	if float64(requestTimeoutSec) > callVal {
		callVal = float64(requestTimeoutSec)
	}
	waitVal := float64(requestTimeoutSec)
	return lspTimeouts{
		call: clampTimeout(&callVal, maxTimeoutSec),
		wait: clampTimeout(&waitVal, maxTimeoutSec),
	}
}

// renderLspDescription renders lsp.md with the resolved timeout defaults.
func renderLspDescription(template string, s Settings) string {
	defaults := resolveLspTimeouts(nil, s.RequestTimeoutSec, s.ToolsMaxTimeoutSec)
	maxVal := float64(ToolTimeoutMaxSec)
	maxSec := clampTimeout(&maxVal, s.ToolsMaxTimeoutSec)
	out := strings.ReplaceAll(template, "{{defaultTimeoutSec}}", fmt.Sprintf("%d", defaults.call))
	out = strings.ReplaceAll(out, "{{defaultWaitSec}}", fmt.Sprintf("%d", defaults.wait))
	out = strings.ReplaceAll(out, "{{maxTimeoutSec}}", fmt.Sprintf("%d", maxSec))
	return strings.TrimSpace(out)
}
