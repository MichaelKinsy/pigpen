package debug

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/dirs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/gate"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/intent"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/schemaorder"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/toolcmd"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

// upstream: packages/coding-agent/src/tools/debug.ts
// upstream: packages/coding-agent/src/prompts/tools/debug.md
// upstream: packages/coding-agent/src/tools/tool-timeouts.ts

//go:embed debug.md
var debugDescription string

const (
	// DefaultTimeoutSec is omp's TOOL_TIMEOUTS.debug.default (30s).
	DefaultTimeoutSec = 30
	// MinTimeoutSec is omp's TOOL_TIMEOUTS.debug.min (5s).
	MinTimeoutSec = 5
	// MaxTimeoutSec is omp's TOOL_TIMEOUTS.debug.max (300s).
	MaxTimeoutSec = 300

	untrustedProjectConfigNote = "Note: project DAP configuration and project-local binaries were skipped because this project is not trusted."
)

// Enabled reads boolean settings (notably "debug.enabled").
// Default returns def (omp's default for "debug.enabled" is true).
// The integrator assigns a real settings getter at wiring time.
var Enabled = func(ctx sdk.Context, key string, def bool) bool {
	return def
}

// Config controls runtime behavior of a DebugTool instance.
type Config struct {
	MaxTimeoutSec int
}

// clampDebugTimeout mirrors packages/coding-agent/src/tools/tool-timeouts.ts:34.
func clampDebugTimeout(requestedSec *float64, maxTimeoutSec int) int {
	t := float64(DefaultTimeoutSec)
	if requestedSec != nil && !math.IsNaN(*requestedSec) && !math.IsInf(*requestedSec, 0) {
		t = *requestedSec
	}
	if maxTimeoutSec > 0 && t > float64(maxTimeoutSec) {
		t = float64(maxTimeoutSec)
	}
	if t < float64(MinTimeoutSec) {
		t = float64(MinTimeoutSec)
	}
	if t > float64(MaxTimeoutSec) {
		t = float64(MaxTimeoutSec)
	}
	return int(math.Round(t))
}

// sessionRegistry holds one DapSessionManager (and one-shot untrusted notice flag)
// per PiG session ID so concurrent or sequential sessions in one process are
// completely isolated and lifecycle.End(sessionID) releases only that session.
type sessionRegistry struct {
	mu            sync.Mutex
	managers      map[string]*DapSessionManager
	untrustedNote map[string]bool
}

func newSessionRegistry() *sessionRegistry {
	return &sessionRegistry{
		managers:      make(map[string]*DapSessionManager),
		untrustedNote: make(map[string]bool),
	}
}

func (r *sessionRegistry) forKey(key string) *DapSessionManager {
	r.mu.Lock()
	defer r.mu.Unlock()
	if m, ok := r.managers[key]; ok {
		return m
	}
	m := NewSessionManager()
	r.managers[key] = m
	return m
}

// markUntrustedNoted records that the untrusted-project note has been shown for
// key and returns true on the first call for that session.
func (r *sessionRegistry) markUntrustedNoted(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.untrustedNote[key] {
		return false
	}
	r.untrustedNote[key] = true
	return true
}

func (r *sessionRegistry) shutdownKey(ctx context.Context, key string) {
	if key == "" {
		return
	}
	r.mu.Lock()
	m, ok := r.managers[key]
	if ok {
		delete(r.managers, key)
	}
	delete(r.untrustedNote, key)
	r.mu.Unlock()
	if ok {
		m.Shutdown(ctx)
	}
}

// defaultRegistry is the single package-level registry shared across all
// extension instances in the process (PiG creates a fresh extension instance
// per session, reload, /new, /resume, /fork, while lifecycle.OnEnd("debug", ...)
// keeps one callback per area).
var defaultRegistry = newSessionRegistry()

// DebugTool implements omp's `debug` tool on PiG's Go SDK.
type DebugTool struct {
	cfg      Config
	registry *sessionRegistry
	// singleManager, when non-nil, overrides per-session lookup (used by unit tests).
	singleManager *DapSessionManager

	// Adapter selection hooks (default to trust-aware package functions; overridable in tests).
	selectLaunchAdapter func(program, cwd, adapterName string, programKind LaunchProgramKind, includeProject bool) LaunchAdapterSelection
	selectAttachAdapter func(ctx context.Context, cwd, adapterName string, target DapAttachTarget, includeProject bool) *DapResolvedAdapter
}

// NewDebugTool constructs a DebugTool. When manager is nil and registry is nil,
// it uses an isolated registry unless attached to defaultRegistry in Register.
func NewDebugTool(cfg Config, manager *DapSessionManager) *DebugTool {
	return newDebugToolWithRegistry(cfg, newSessionRegistry(), manager)
}

func newDebugToolWithRegistry(cfg Config, reg *sessionRegistry, manager *DapSessionManager) *DebugTool {
	if reg == nil {
		reg = newSessionRegistry()
	}
	return &DebugTool{
		cfg:                 cfg,
		registry:            reg,
		singleManager:       manager,
		selectLaunchAdapter: selectLaunchAdapterWithTrust,
		selectAttachAdapter: selectAttachAdapterWithTrust,
	}
}

// resolveSessionID resolves the PiG session id strictly without falling back to
// cwd or a shared "default" bucket (an empty/unknown id never aliases another session).
func resolveSessionID(ctx sdk.Context) (string, error) {
	id, err := ctx.GetSessionID()
	if err != nil {
		return "", fmt.Errorf("failed to resolve session ID: %w", err)
	}
	if id == "" {
		return "", errors.New("session ID is unavailable")
	}
	return id, nil
}

func (t *DebugTool) managerFor(ctx sdk.Context) (string, *DapSessionManager, error) {
	if t.singleManager != nil {
		id, _ := ctx.GetSessionID()
		return id, t.singleManager, nil
	}
	id, err := resolveSessionID(ctx)
	if err != nil {
		return "", nil, err
	}
	return id, t.registry.forKey(id), nil
}

// Register installs omp's `debug` tool, declares it mountable with
// internal/toolcmd, registers per-session cleanup with internal/lifecycle, and
// connects session_shutdown to lifecycle.End(sessionID).
// It performs no I/O, never panics, and is safe to call repeatedly.
func Register(e *sdk.Extension) {
	tool := newDebugToolWithRegistry(Config{}, defaultRegistry, nil)
	debugDef := schemaorder.Apply(tool.Definition())
	e.RegisterTool(intent.Tool(debugDef))
	intent.RegisterSync(e, debugDef)

	toolcmd.Mountable("debug", func(ctx sdk.Context) bool {
		if Enabled != nil {
			return Enabled(ctx, "debug.enabled", true)
		}
		return true
	})

	lifecycle.OnEnd("debug", func(sessionID string) {
		if sessionID == "" {
			return
		}
		disposeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		defaultRegistry.shutdownKey(disposeCtx, sessionID)
	})

	e.OnSessionShutdown(func(ctx sdk.Context, _ map[string]any) (any, error) {
		if id, err := ctx.GetSessionID(); err == nil && id != "" {
			lifecycle.End(id)
		}
		return nil, nil
	})
}

// DebugParametersSchema returns omp's exact JSON Schema for the `debug` tool.
// upstream: packages/coding-agent/src/tools/debug.ts:69-138
func DebugParametersSchema() sdk.Schema {
	return sdk.Schema{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type": "string",
				"enum": AllDebugActions,
			},
			"program": map[string]any{
				"type":        "string",
				"description": "debug target path; Delve accepts Go package directories",
			},
			"args": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "program arguments",
			},
			"adapter": map[string]any{
				"type":        "string",
				"description": "configured adapter id (gdb, lldb-dap, debugpy, dlv, rdbg, or dap.json entry)",
			},
			"cwd": map[string]any{
				"type": "string",
			},
			"file": map[string]any{
				"type":        "string",
				"description": "source file",
			},
			"line": map[string]any{
				"type":        "number",
				"description": "source line",
			},
			"function": map[string]any{
				"type":        "string",
				"description": "function name",
			},
			"name": map[string]any{
				"type":        "string",
				"description": "variable or data name",
			},
			"condition": map[string]any{
				"type":        "string",
				"description": "breakpoint condition",
			},
			"hit_condition": map[string]any{
				"type": "string",
			},
			"expression": map[string]any{
				"type":        "string",
				"description": "expression to evaluate",
			},
			"context": map[string]any{
				"type":        "string",
				"description": "evaluate context: watch | repl | hover | variables | clipboard",
			},
			"frame_id": map[string]any{
				"type": "number",
			},
			"scope_id": map[string]any{
				"type":        "number",
				"description": "scope variables reference",
			},
			"variable_ref": map[string]any{
				"type":        "number",
				"description": "variable reference",
			},
			"pid": map[string]any{
				"type":        "number",
				"description": "process id for attach",
			},
			"port": map[string]any{
				"type":        "number",
				"description": "remote attach port",
			},
			"host": map[string]any{
				"type":        "string",
				"description": "remote attach host",
			},
			"levels": map[string]any{
				"type":        "number",
				"description": "max stack frames",
			},
			"memory_reference": map[string]any{
				"type":        "string",
				"description": "memory reference or address",
			},
			"instruction_reference": map[string]any{
				"type": "string",
			},
			"instruction_count": map[string]any{
				"type": "number",
			},
			"instruction_offset": map[string]any{
				"type": "number",
			},
			"count": map[string]any{
				"type":        "number",
				"description": "bytes to read",
			},
			"data": map[string]any{
				"type":        "string",
				"description": "base64 memory payload",
			},
			"data_id": map[string]any{
				"type":        "string",
				"description": "data breakpoint id",
			},
			"access_type": map[string]any{
				"type": "string",
				"enum": []string{"read", "write", "readWrite"},
			},
			"command": map[string]any{
				"type":        "string",
				"description": "custom dap request command",
			},
			"arguments": map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"additionalProperties": true,
				"description":          "custom request arguments",
			},
			"offset": map[string]any{
				"type": "number",
			},
			"resolve_symbols": map[string]any{
				"type": "boolean",
			},
			"allow_partial": map[string]any{
				"type": "boolean",
			},
			"start_module": map[string]any{
				"type": "number",
			},
			"module_count": map[string]any{
				"type": "number",
			},
			"timeout": map[string]any{
				"type":        "number",
				"description": "per-request timeout seconds",
			},
		},
		"required":             []string{"action"},
		"additionalProperties": false,
	}
}

// Definition returns the sdk.ToolDefinition for omp's `debug` tool.
func (t *DebugTool) Definition() sdk.ToolDefinition {
	defaultActive := false
	return sdk.ToolDefinition{
		Name:          "debug",
		Label:         "Debug",
		Description:   strings.TrimSpace(debugDescription),
		PromptSnippet: "Debug a running process with DAP (debugger adapter protocol)",
		Parameters:    DebugParametersSchema(),
		ExecutionMode: "sequential",
		Exposure:      sdk.ToolExposureCodemode,
		DefaultActive: &defaultActive,
		Execute:       t.Execute,
	}
}

func isAllSlashes(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] != '/' {
			return false
		}
	}
	return true
}

// resolveToCwd mirrors packages/coding-agent/src/tools/path-utils.ts:323-343:
// rejects registered internal URLs, aliases bare `/` (or `///`) to cwd, expands `~`,
// and resolves relative paths against cwd.
func resolveToCwd(target, cwd string) (string, error) {
	if target == "" {
		return cwd, nil
	}
	expanded := target
	if strings.HasPrefix(expanded, "~/") || expanded == "~" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			if expanded == "~" {
				expanded = home
			} else {
				expanded = filepath.Join(home, expanded[2:])
			}
		}
	}
	if urls.CanHandle(expanded) {
		scheme := urls.ExtractScheme(expanded)
		return "", sdk.NewToolError(fmt.Sprintf(
			`Path "%s" uses internal scheme "%s://" and must be resolved through the proper protocol handler, not as a filesystem path.`,
			target, scheme,
		))
	}
	if isAllSlashes(expanded) {
		return cwd, nil
	}
	if filepath.IsAbs(expanded) {
		return filepath.Clean(expanded), nil
	}
	return filepath.Clean(filepath.Join(cwd, expanded)), nil
}

func classifyLaunchProgram(program string) (LaunchProgramKind, error) {
	info, err := os.Stat(program)
	if err != nil {
		if os.IsNotExist(err) {
			return ProgramKindMissing, nil
		}
		return "", err
	}
	if info.IsDir() {
		return ProgramKindDirectory, nil
	}
	return ProgramKindFile, nil
}

func validateLaunchProgram(program, cwd string, kind LaunchProgramKind, adapter DapResolvedAdapter) error {
	if kind != ProgramKindDirectory || adapter.AcceptsDirectoryProgram {
		return nil
	}
	display := formatPathRelativeToCwd(program, cwd, true)
	return sdk.NewToolError(fmt.Sprintf(
		"launch program resolves to a directory: %s. Pass an executable file path or choose an adapter that supports package directories.",
		display,
	))
}

func getString(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func getIntPtr(m map[string]any, key string) *int {
	v, ok := m[key]
	if !ok || v == nil {
		return nil
	}
	switch n := v.(type) {
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return nil
		}
		iv := int(n)
		return &iv
	case int:
		iv := n
		return &iv
	case int64:
		iv := int(n)
		return &iv
	default:
		return nil
	}
}

func getFloatPtr(m map[string]any, key string) *float64 {
	v, ok := m[key]
	if !ok || v == nil {
		return nil
	}
	switch n := v.(type) {
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return nil
		}
		fv := n
		return &fv
	case int:
		fv := float64(n)
		return &fv
	case int64:
		fv := float64(n)
		return &fv
	default:
		return nil
	}
}

func getBoolPtr(m map[string]any, key string) *bool {
	v, ok := m[key].(bool)
	if !ok {
		return nil
	}
	return &v
}

func getStringSlice(m map[string]any, key string) []string {
	raw, exists := m[key]
	if !exists || raw == nil {
		return nil
	}
	if ss, ok := raw.([]string); ok {
		return ss
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, entry := range arr {
		if s, ok := entry.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func getMap(m map[string]any, key string) map[string]any {
	v, _ := m[key].(map[string]any)
	return v
}

func toolErr(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := err.(*sdk.ToolError); ok {
		return err
	}
	if errors.Is(err, context.Canceled) {
		return sdk.NewToolError(ErrOperationAborted.Error())
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return sdk.NewToolError("The operation timed out.")
	}
	return sdk.NewToolError(err.Error())
}

// isProjectTrusted queries PiG's project trust gate. Any error or false result
// is treated as untrusted (fail closed).
func isProjectTrusted(ctx sdk.Context) bool {
	trusted, err := ctx.IsProjectTrusted()
	return err == nil && trusted
}

// Execute dispatches one `debug` tool call across all 28 DAP actions.
// upstream: packages/coding-agent/src/tools/debug.ts:590-989
func (t *DebugTool) Execute(sdkCtx sdk.Context, params map[string]any) (any, error) {
	action := getString(params, "action")
	var zeroCtx sdk.Context
	if sdkCtx != zeroCtx && !IsReadonlyAction(action) {
		if err := gate.Check(sdkCtx, gate.Request{Tool: "debug", Op: gate.OpExec, Detail: action}); err != nil {
			return nil, toolErr(err)
		}
	}
	timeoutSec := clampDebugTimeout(getFloatPtr(params, "timeout"), t.cfg.MaxTimeoutSec)
	timeout := time.Duration(timeoutSec) * time.Second
	// own the timeout so a stop-wait never races a context deadline against an
	// adapter round trip and never leaks Go's "context deadline exceeded" text.
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-sdkCtx.Done():
			cancel()
		case <-runCtx.Done():
		}
	}()

	sessionCwd := sdkCtx.Cwd()
	if sessionCwd == "" {
		sessionCwd, _ = os.Getwd()
	}
	// Keep sessionCwd as handed by PiG so wire paths (launch program/cwd,
	// setBreakpoints source.path) match the real paths adapters like dlv record
	// in debug info. Display/details paths are standardized via dirs.StandardizeMacOSPath.
	sessionID, mgr, err := t.managerFor(sdkCtx)
	if err != nil {
		return nil, toolErr(err)
	}
	details := map[string]any{
		"action":  action,
		"success": true,
	}

	requireCapability := func(capName, desc string) error {
		if mgr.GetActiveSession() == nil {
			return sdk.NewToolError("No active debug session. Launch or attach first.")
		}
		if !mgr.GetCapabilities().Has(capName) {
			return sdk.NewToolError(fmt.Sprintf("Current adapter does not support %s", desc))
		}
		return nil
	}

	maybeUntrustedNote := func(cwd string, trusted bool) string {
		if trusted {
			return ""
		}
		if !hasProjectConfigFiles(cwd) && !hasProjectLocalBin(cwd) {
			return ""
		}
		noteKey := sessionID
		if noteKey == "" {
			noteKey = cwd
		}
		if t.registry.markUntrustedNoted(noteKey) {
			return untrustedProjectConfigNote
		}
		return ""
	}

	appendNote := func(text, note string) string {
		if note == "" {
			return text
		}
		return text + "\n" + note
	}

	switch action {
	case "launch":
		programArg := getString(params, "program")
		if programArg == "" {
			return nil, sdk.NewToolError("program is required for launch")
		}
		commandCwd := sessionCwd
		if rawCwd := getString(params, "cwd"); rawCwd != "" {
			resolvedCwd, err := resolveToCwd(rawCwd, sessionCwd)
			if err != nil {
				return nil, err
			}
			commandCwd = resolvedCwd
		}
		program, err := resolveToCwd(programArg, commandCwd)
		if err != nil {
			return nil, err
		}
		kind, err := classifyLaunchProgram(program)
		if err != nil {
			return nil, toolErr(err)
		}
		trusted := isProjectTrusted(sdkCtx)
		note := maybeUntrustedNote(commandCwd, trusted)

		adapterArg := getString(params, "adapter")
		selection := t.selectLaunchAdapter(program, commandCwd, adapterArg, kind, trusted)
		if selection.Kind == "unavailable" {
			return nil, sdk.NewToolError(appendNote(formatAdapterUnavailable(selection.AdapterName, selection.Command, commandCwd, trusted), note))
		}
		if selection.Kind == "none" || selection.Adapter == nil {
			return nil, sdk.NewToolError(appendNote(fmt.Sprintf("No debugger adapter available. Installed adapters: %s", getConfiguredAdapters(commandCwd, trusted)), note))
		}
		adapter := *selection.Adapter
		if err := validateLaunchProgram(program, commandCwd, kind, adapter); err != nil {
			return nil, err
		}
		extraLaunchArgs := ResolveLaunchOverrides(adapter, program, kind)
		snapshot, err := mgr.Launch(runCtx, DapLaunchSessionOptions{
			Adapter:              adapter,
			Program:              program,
			Args:                 getStringSlice(params, "args"),
			Cwd:                  commandCwd,
			ExtraLaunchArguments: extraLaunchArgs,
		}, timeout)
		if err != nil {
			return nil, toolErr(err)
		}
		details["snapshot"] = snapshot
		details["adapter"] = adapter.Name
		return sdk.ToolResult{
			Content: appendNote(strings.Join(FormatSessionSnapshot(snapshot), "\n"), note),
			Details: details,
		}, nil

	case "attach":
		pid := getIntPtr(params, "pid")
		port := getIntPtr(params, "port")
		host := getString(params, "host")
		adapterArg := getString(params, "adapter")
		if pid == nil && port == nil && adapterArg == "" {
			return nil, sdk.NewToolError("attach requires pid or port")
		}
		commandCwd := sessionCwd
		if rawCwd := getString(params, "cwd"); rawCwd != "" {
			resolvedCwd, err := resolveToCwd(rawCwd, sessionCwd)
			if err != nil {
				return nil, err
			}
			commandCwd = resolvedCwd
		}
		trusted := isProjectTrusted(sdkCtx)
		note := maybeUntrustedNote(commandCwd, trusted)

		adapter := t.selectAttachAdapter(runCtx, commandCwd, adapterArg, DapAttachTarget{
			PID:  pid,
			Port: port,
			Host: host,
		}, trusted)
		if adapter == nil {
			if adapterArg != "" {
				cmd := adapterArg
				if cfg, ok := getAdapterConfigsWithTrust(commandCwd, trusted)[adapterArg]; ok && cfg.Command != "" {
					cmd = cfg.Command
				}
				return nil, sdk.NewToolError(appendNote(formatAdapterUnavailable(adapterArg, cmd, commandCwd, trusted), note))
			}
			return nil, sdk.NewToolError(appendNote(fmt.Sprintf("No debugger adapter available. Installed adapters: %s", getConfiguredAdapters(commandCwd, trusted)), note))
		}
		snapshot, err := mgr.Attach(runCtx, DapAttachSessionOptions{
			Adapter: *adapter,
			Cwd:     commandCwd,
			PID:     pid,
			Port:    port,
			Host:    host,
		}, timeout)
		if err != nil {
			return nil, toolErr(err)
		}
		details["snapshot"] = snapshot
		details["adapter"] = adapter.Name
		return sdk.ToolResult{
			Content: appendNote(strings.Join(FormatSessionSnapshot(snapshot), "\n"), note),
			Details: details,
		}, nil

	case "set_breakpoint":
		if fn := getString(params, "function"); fn != "" {
			snap, bps, err := mgr.SetFunctionBreakpoint(runCtx, fn, getString(params, "condition"), timeout)
			if err != nil {
				return nil, toolErr(err)
			}
			details["snapshot"] = snap
			details["functionBreakpoints"] = bps
			return sdk.ToolResult{Content: formatFunctionBreakpoints(bps), Details: details}, nil
		}
		fileArg := getString(params, "file")
		line := getIntPtr(params, "line")
		if fileArg == "" || line == nil {
			return nil, sdk.NewToolError("set_breakpoint requires file+line or function")
		}
		file, err := resolveToCwd(fileArg, sessionCwd)
		if err != nil {
			return nil, err
		}
		snap, bps, sourcePath, err := mgr.SetBreakpoint(runCtx, file, *line, getString(params, "condition"), timeout)
		if err != nil {
			return nil, toolErr(err)
		}
		details["snapshot"] = snap
		details["breakpoints"] = bps
		return sdk.ToolResult{Content: formatBreakpoints(dirs.StandardizeMacOSPath(sourcePath), bps), Details: details}, nil

	case "remove_breakpoint":
		if fn := getString(params, "function"); fn != "" {
			snap, bps, err := mgr.RemoveFunctionBreakpoint(runCtx, fn, timeout)
			if err != nil {
				return nil, toolErr(err)
			}
			details["snapshot"] = snap
			details["functionBreakpoints"] = bps
			return sdk.ToolResult{Content: formatFunctionBreakpoints(bps), Details: details}, nil
		}
		fileArg := getString(params, "file")
		line := getIntPtr(params, "line")
		if fileArg == "" || line == nil {
			return nil, sdk.NewToolError("remove_breakpoint requires file+line or function")
		}
		file, err := resolveToCwd(fileArg, sessionCwd)
		if err != nil {
			return nil, err
		}
		snap, bps, sourcePath, err := mgr.RemoveBreakpoint(runCtx, file, *line, timeout)
		if err != nil {
			return nil, toolErr(err)
		}
		details["snapshot"] = snap
		details["breakpoints"] = bps
		return sdk.ToolResult{Content: formatBreakpoints(dirs.StandardizeMacOSPath(sourcePath), bps), Details: details}, nil

	case "set_instruction_breakpoint":
		if err := requireCapability("supportsInstructionBreakpoints", "instruction breakpoints"); err != nil {
			return nil, err
		}
		ref := getString(params, "instruction_reference")
		if ref == "" {
			return nil, sdk.NewToolError("instruction_reference is required for set_instruction_breakpoint")
		}
		snap, bps, err := mgr.SetInstructionBreakpoint(runCtx, ref, getIntPtr(params, "offset"), getString(params, "condition"), getString(params, "hit_condition"), timeout)
		if err != nil {
			return nil, toolErr(err)
		}
		details["snapshot"] = snap
		details["instructionBreakpoints"] = bps
		return sdk.ToolResult{Content: formatInstructionBreakpoints(bps), Details: details}, nil

	case "remove_instruction_breakpoint":
		if err := requireCapability("supportsInstructionBreakpoints", "instruction breakpoints"); err != nil {
			return nil, err
		}
		ref := getString(params, "instruction_reference")
		if ref == "" {
			return nil, sdk.NewToolError("instruction_reference is required for remove_instruction_breakpoint")
		}
		snap, bps, err := mgr.RemoveInstructionBreakpoint(runCtx, ref, getIntPtr(params, "offset"), timeout)
		if err != nil {
			return nil, toolErr(err)
		}
		details["snapshot"] = snap
		details["instructionBreakpoints"] = bps
		return sdk.ToolResult{Content: formatInstructionBreakpoints(bps), Details: details}, nil

	case "data_breakpoint_info":
		if err := requireCapability("supportsDataBreakpoints", "data breakpoints"); err != nil {
			return nil, err
		}
		name := getString(params, "name")
		if name == "" {
			return nil, sdk.NewToolError("name is required for data_breakpoint_info")
		}
		varRef := getIntPtr(params, "variable_ref")
		if varRef == nil {
			varRef = getIntPtr(params, "scope_id")
		}
		snap, info, err := mgr.DataBreakpointInfo(runCtx, name, varRef, getIntPtr(params, "frame_id"), timeout)
		if err != nil {
			return nil, toolErr(err)
		}
		details["snapshot"] = snap
		details["dataBreakpointInfo"] = info
		return sdk.ToolResult{Content: formatDataBreakpointInfo(info), Details: details}, nil

	case "set_data_breakpoint":
		if err := requireCapability("supportsDataBreakpoints", "data breakpoints"); err != nil {
			return nil, err
		}
		dataID := getString(params, "data_id")
		if dataID == "" {
			return nil, sdk.NewToolError("data_id is required for set_data_breakpoint")
		}
		snap, bps, err := mgr.SetDataBreakpoint(runCtx, dataID, getString(params, "access_type"), getString(params, "condition"), getString(params, "hit_condition"), timeout)
		if err != nil {
			return nil, toolErr(err)
		}
		details["snapshot"] = snap
		details["dataBreakpoints"] = bps
		return sdk.ToolResult{Content: formatDataBreakpoints(bps), Details: details}, nil

	case "remove_data_breakpoint":
		if err := requireCapability("supportsDataBreakpoints", "data breakpoints"); err != nil {
			return nil, err
		}
		dataID := getString(params, "data_id")
		if dataID == "" {
			return nil, sdk.NewToolError("data_id is required for remove_data_breakpoint")
		}
		snap, bps, err := mgr.RemoveDataBreakpoint(runCtx, dataID, timeout)
		if err != nil {
			return nil, toolErr(err)
		}
		details["snapshot"] = snap
		details["dataBreakpoints"] = bps
		return sdk.ToolResult{Content: formatDataBreakpoints(bps), Details: details}, nil

	case "continue":
		outcome, err := mgr.Continue(runCtx, timeout)
		if err != nil {
			return nil, toolErr(err)
		}
		details["snapshot"] = outcome.Snapshot
		details["state"] = outcome.State
		details["timedOut"] = outcome.TimedOut
		return sdk.ToolResult{Content: buildOutcomeText(outcome, timeoutSec, "Continue"), Details: details}, nil

	case "step_over":
		outcome, err := mgr.StepOver(runCtx, timeout)
		if err != nil {
			return nil, toolErr(err)
		}
		details["snapshot"] = outcome.Snapshot
		details["state"] = outcome.State
		details["timedOut"] = outcome.TimedOut
		return sdk.ToolResult{Content: buildOutcomeText(outcome, timeoutSec, "Step over"), Details: details}, nil

	case "step_in":
		outcome, err := mgr.StepIn(runCtx, timeout)
		if err != nil {
			return nil, toolErr(err)
		}
		details["snapshot"] = outcome.Snapshot
		details["state"] = outcome.State
		details["timedOut"] = outcome.TimedOut
		return sdk.ToolResult{Content: buildOutcomeText(outcome, timeoutSec, "Step in"), Details: details}, nil

	case "step_out":
		outcome, err := mgr.StepOut(runCtx, timeout)
		if err != nil {
			return nil, toolErr(err)
		}
		details["snapshot"] = outcome.Snapshot
		details["state"] = outcome.State
		details["timedOut"] = outcome.TimedOut
		return sdk.ToolResult{Content: buildOutcomeText(outcome, timeoutSec, "Step out"), Details: details}, nil

	case "pause":
		snap, err := mgr.Pause(runCtx, timeout)
		if err != nil {
			return nil, toolErr(err)
		}
		details["snapshot"] = snap
		lines := append(FormatSessionSnapshot(snap), "Program paused.")
		return sdk.ToolResult{Content: strings.Join(lines, "\n"), Details: details}, nil

	case "evaluate":
		expr := getString(params, "expression")
		if expr == "" {
			return nil, sdk.NewToolError("expression is required for evaluate")
		}
		evalCtx := getString(params, "context")
		if evalCtx == "" {
			evalCtx = "repl"
		}
		snap, evalResp, err := mgr.Evaluate(runCtx, expr, evalCtx, getIntPtr(params, "frame_id"), timeout)
		if err != nil {
			return nil, toolErr(err)
		}
		details["snapshot"] = snap
		details["evaluation"] = evalResp
		return sdk.ToolResult{Content: formatEvaluation(evalResp), Details: details}, nil

	case "stack_trace":
		snap, frames, err := mgr.StackTrace(runCtx, getIntPtr(params, "levels"), timeout)
		if err != nil {
			return nil, toolErr(err)
		}
		details["snapshot"] = snap
		details["stackFrames"] = frames
		return sdk.ToolResult{Content: formatStackFrames(frames), Details: details}, nil

	case "threads":
		snap, threads, err := mgr.Threads(runCtx, timeout)
		if err != nil {
			return nil, toolErr(err)
		}
		details["snapshot"] = snap
		details["threads"] = threads
		return sdk.ToolResult{Content: formatThreads(threads), Details: details}, nil

	case "scopes":
		snap, scopes, err := mgr.Scopes(runCtx, getIntPtr(params, "frame_id"), timeout)
		if err != nil {
			return nil, toolErr(err)
		}
		details["snapshot"] = snap
		details["scopes"] = scopes
		return sdk.ToolResult{Content: formatScopes(scopes), Details: details}, nil

	case "variables":
		varRef := getIntPtr(params, "variable_ref")
		if varRef == nil {
			varRef = getIntPtr(params, "scope_id")
		}
		if varRef == nil {
			return nil, sdk.NewToolError("variables requires variable_ref or scope_id")
		}
		snap, vars, err := mgr.Variables(runCtx, *varRef, timeout)
		if err != nil {
			return nil, toolErr(err)
		}
		details["snapshot"] = snap
		details["variables"] = vars
		return sdk.ToolResult{Content: formatVariables(vars), Details: details}, nil

	case "disassemble":
		if err := requireCapability("supportsDisassembleRequest", "disassembly"); err != nil {
			return nil, err
		}
		instrCount := getIntPtr(params, "instruction_count")
		if instrCount == nil {
			return nil, sdk.NewToolError("instruction_count is required for disassemble")
		}
		memRef := getString(params, "memory_reference")
		if memRef == "" {
			snap := mgr.GetActiveSession()
			if snap == nil {
				return nil, sdk.NewToolError("No active debug session. Launch or attach first.")
			}
			if snap.InstructionPointerReference == "" {
				return nil, sdk.NewToolError("disassemble requires memory_reference unless the current stop location has an instruction pointer reference")
			}
			memRef = snap.InstructionPointerReference
		}
		snap, insns, err := mgr.Disassemble(runCtx, memRef, *instrCount, getIntPtr(params, "offset"), getIntPtr(params, "instruction_offset"), getBoolPtr(params, "resolve_symbols"), timeout)
		if err != nil {
			return nil, toolErr(err)
		}
		details["snapshot"] = snap
		details["disassembly"] = insns
		return sdk.ToolResult{Content: formatDisassembly(insns), Details: details}, nil

	case "read_memory":
		if err := requireCapability("supportsReadMemoryRequest", "memory reads"); err != nil {
			return nil, err
		}
		memRef := getString(params, "memory_reference")
		if memRef == "" {
			return nil, sdk.NewToolError("memory_reference is required for read_memory")
		}
		count := getIntPtr(params, "count")
		if count == nil {
			return nil, sdk.NewToolError("count is required for read_memory")
		}
		snap, resp, err := mgr.ReadMemory(runCtx, memRef, *count, getIntPtr(params, "offset"), timeout)
		if err != nil {
			return nil, toolErr(err)
		}
		details["snapshot"] = snap
		details["memoryAddress"] = resp.Address
		if resp.Data != nil {
			details["memoryData"] = *resp.Data
		}
		if resp.UnreadableBytes != nil {
			details["unreadableBytes"] = *resp.UnreadableBytes
		}
		return sdk.ToolResult{Content: formatMemoryRead(resp.Address, resp.Data, resp.UnreadableBytes), Details: details}, nil

	case "write_memory":
		if err := requireCapability("supportsWriteMemoryRequest", "memory writes"); err != nil {
			return nil, err
		}
		memRef := getString(params, "memory_reference")
		if memRef == "" {
			return nil, sdk.NewToolError("memory_reference is required for write_memory")
		}
		data := getString(params, "data")
		if data == "" {
			return nil, sdk.NewToolError("data is required for write_memory")
		}
		snap, resp, err := mgr.WriteMemory(runCtx, memRef, data, getIntPtr(params, "offset"), getBoolPtr(params, "allow_partial"), timeout)
		if err != nil {
			return nil, toolErr(err)
		}
		details["snapshot"] = snap
		if resp.BytesWritten != nil {
			details["bytesWritten"] = *resp.BytesWritten
		}
		lines := []string{"Memory write completed."}
		if resp.BytesWritten != nil {
			lines = append(lines, fmt.Sprintf("Bytes written: %d", *resp.BytesWritten))
		}
		if resp.Offset != nil {
			lines = append(lines, fmt.Sprintf("Offset: %d", *resp.Offset))
		}
		return sdk.ToolResult{Content: strings.Join(lines, "\n"), Details: details}, nil

	case "modules":
		if err := requireCapability("supportsModulesRequest", "module introspection"); err != nil {
			return nil, err
		}
		snap, mods, err := mgr.Modules(runCtx, getIntPtr(params, "start_module"), getIntPtr(params, "module_count"), timeout)
		if err != nil {
			return nil, toolErr(err)
		}
		details["snapshot"] = snap
		details["modules"] = mods
		return sdk.ToolResult{Content: formatModules(mods), Details: details}, nil

	case "loaded_sources":
		if err := requireCapability("supportsLoadedSourcesRequest", "loaded sources"); err != nil {
			return nil, err
		}
		snap, sources, err := mgr.LoadedSources(runCtx, timeout)
		if err != nil {
			return nil, toolErr(err)
		}
		details["snapshot"] = snap
		details["sources"] = sources
		return sdk.ToolResult{Content: formatLoadedSources(sources), Details: details}, nil

	case "custom_request":
		cmd := getString(params, "command")
		if cmd == "" {
			return nil, sdk.NewToolError("command is required for custom_request")
		}
		snap, body, err := mgr.CustomRequest(runCtx, cmd, getMap(params, "arguments"), timeout)
		if err != nil {
			return nil, toolErr(err)
		}
		details["snapshot"] = snap
		details["customBody"] = body
		return sdk.ToolResult{Content: formatCustomResponse(cmd, body), Details: details}, nil

	case "output":
		outSnap, err := mgr.GetOutput(0)
		if err != nil {
			return nil, toolErr(err)
		}
		details["snapshot"] = outSnap.Snapshot
		details["output"] = outSnap.Output
		text := outSnap.Output
		if text == "" {
			text = "(no output captured)"
		}
		return sdk.ToolResult{Content: text, Details: details}, nil

	case "terminate":
		snap, err := mgr.Terminate(runCtx, timeout)
		if err != nil {
			return nil, toolErr(err)
		}
		if snap == nil {
			return sdk.ToolResult{Content: "No debug session to terminate.", Details: details}, nil
		}
		details["snapshot"] = *snap
		lines := append(FormatSessionSnapshot(*snap), "Debug session terminated.")
		return sdk.ToolResult{Content: strings.Join(lines, "\n"), Details: details}, nil

	case "sessions":
		sessions := mgr.ListSessions()
		details["sessions"] = sessions
		return sdk.ToolResult{Content: formatSessions(sessions), Details: details}, nil

	default:
		return nil, sdk.NewToolError(fmt.Sprintf("Unsupported debug action: %s", action))
	}
}
