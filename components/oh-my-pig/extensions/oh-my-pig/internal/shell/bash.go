// Package shell ports omp's `bash` tool (foreground, async/auto-background jobs,
// named services, notify regex watching, output sanitization & truncation with
// bounded local://N.log spill, CRITICAL_BASH_PATTERNS & bash.patterns veto,
// process-group teardown, and `proc://` URL handling) at omp v19.1.1 (31ffee513f).
package shell

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"io"
	"math"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/dirs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/gate"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/intent"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/jobs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/schemaorder"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

// upstream: packages/coding-agent/src/tools/bash.ts, packages/coding-agent/src/prompts/tools/bash.md

//go:embed bash.md
var bashDescription string

// CommandPreamble, when set by the integrator (e.g. `toolcmd.PreambleWithRelease`),
// returns bash code sourced into every bash run (sync, async, services) after
// saved session state and before the command, plus a release callback invoked
// when the call, background job, or service finishes to revoke its credential.
// Functions and variables defined by the preamble are excluded from the saved
// session snapshot and never written to spill files, logs, or tool output.
var CommandPreamble func(ctx sdk.Context) (preamble string, release func())

// DirectCommand, when set by the integrator (e.g. adapting `toolcmd.TryDirectWithOptions`),
// is consulted before spawning a shell; when handled is true, its output,
// exit code, and timeout/cancellation flags are pushed through OutputSink and
// formatted identically to a shell run.
var DirectCommand func(ctx sdk.Context, command string, cwd string, timeout time.Duration) (output string, exitCode int, timedOut bool, cancelled bool, handled bool)

// InvokedCommand, when set by the integrator (e.g. `toolcmd.InvokedCommand`),
// classifies without executing whether `command` invokes a mounted/special
// session tool command (using the same parser as DirectCommand plus segment
// scanning). Preferred over CommandNames re-parsing for `pty: true` pre-flight
// refusal (`tools/bash.ts:1900-1905`) so shell and toolcmd cannot disagree.
var InvokedCommand func(ctx sdk.Context, command string) (name string, ok bool)

// CommandNames, when set by the integrator (e.g. `toolcmd.CommandNames`),
// returns the names of mounted/special session tool commands so `bash` skips
// bash-interceptor rules on commands led by a session tool command and falls
// back for `pty: true` refusal when InvokedCommand is nil.
var CommandNames func(ctx sdk.Context) []string

// ToolCommandDispatches, when set by the integrator, drains and returns any
// tool-command dispatch records captured for `ctx` (DirectCommand or bridge
// calls) in omp's `ToolCommandDispatch` shape (`tool`, `mode`, `exitCode`,
// optional `args`, `inner`) so `details.toolCommands` and
// `details.toolCommandsSole` can be attached to `BashTool` results for native
// and terminal renderers (`tools/bash.ts:1345`).
var ToolCommandDispatches func(ctx sdk.Context) []map[string]any

// PrepareLoadout, when set by the integrator (e.g. `toolcmd.PrepareLoadout`),
// is invoked by the `bash` tool's `ToolDefinition.PrepareLoadout` callback so
// mounted session tool commands are hidden from the model's advertised tool
// declarations while remaining directly callable. When nil, `bash`'s
// `PrepareLoadout` returns nil (leaving the loadout unchanged).
// Integrator wiring: `shell.PrepareLoadout = toolcmd.PrepareLoadout`.
var PrepareLoadout sdk.ToolPrepareLoadoutFunc

const (
	// DefaultTimeoutSec is omp's TOOL_TIMEOUTS.bash.default (300s).
	DefaultTimeoutSec = 300
	// MinTimeoutSec is omp's TOOL_TIMEOUTS.bash.min (1s).
	MinTimeoutSec = 1
	// MaxTimeoutSec is omp's TOOL_TIMEOUTS.bash.max (3600s).
	MaxTimeoutSec = 3600
	// DefaultServiceReadyTimeoutSec is omp's ready.timeout default (30s).
	DefaultServiceReadyTimeoutSec = 30
	// MinServiceReadyTimeoutSec matches launch/services.ts:363 (0.05s).
	MinServiceReadyTimeoutSec = 0.05
	// MaxServiceReadyTimeoutSec matches launch/services.ts:363 (3600s).
	MaxServiceReadyTimeoutSec = 3600.0
)

var serviceNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,47}$`)

// Config controls runtime behavior of a BashTool instance. Zero values take
// omp's defaults:
//   - AutoBackgroundThreshold: 60s (`packages/coding-agent/src/async/auto-background.ts:9` DEFAULT_AUTO_BACKGROUND_THRESHOLD_MS)
//   - WallTimeNoticeMinMS: 1000ms (`packages/tui/src/tools/bash.ts:244` WALL_TIME_NOTICE_MIN_MS; < 0 disables in tests)
type Config struct {
	AutoBackgroundEnabled   bool
	AutoBackgroundThreshold time.Duration
	// WallTimeNoticeMinMS sets the wall-time notice threshold in milliseconds.
	// Zero uses omp's production default (`WallTimeNoticeMinMS`, 1000 ms);
	// a negative value disables the wall-time notice (used by tests not
	// testing the notice so slow/loaded CI machines never alter expected text).
	WallTimeNoticeMinMS float64
	AsyncEnabled        bool
	LaunchEnabled       bool
	MaxTimeoutSec       int
	SpillThreshold      int
	HeadBytes           int
	TailBytes           int
	MaxColumns          int
	InterceptorEnabled  bool
	InterceptorRules    []InterceptorRule
	AvailableTools      []string
	// PersistentState, when true, persists new/changed shell variables,
	// functions, aliases, and shell options across calls per session while
	// resetting cwd on every call to the explicit `cwd` parameter or the
	// session directory.
	PersistentState bool
	ArtifactDir     string
	// Now, when non-nil, overrides wall-clock time measurement for wall-time
	// notice tests so threshold boundary tests are deterministic regardless of
	// machine speed.
	Now func() time.Time
}

// DefaultConfig returns omp's default bash settings.
func DefaultConfig() Config {
	return Config{
		AutoBackgroundEnabled:   true,
		AutoBackgroundThreshold: DefaultAutoBackgroundThreshold,
		WallTimeNoticeMinMS:     WallTimeNoticeMinMS,
		AsyncEnabled:            true,
		LaunchEnabled:           true,
		MaxTimeoutSec:           0,
		SpillThreshold:          DefaultSpillThreshold,
		HeadBytes:               DefaultHeadBytes,
		MaxColumns:              DefaultMaxColumns,
		InterceptorEnabled:      false,
		InterceptorRules:        DefaultInterceptorRules,
		PersistentState:         true,
	}
}

func (t *BashTool) now() time.Time {
	if t.cfg.Now != nil {
		return t.cfg.Now()
	}
	return time.Now()
}

func (t *BashTool) wallTimeNoticeMinMS() float64 {
	if t.cfg.WallTimeNoticeMinMS == 0 {
		return WallTimeNoticeMinMS
	}
	return t.cfg.WallTimeNoticeMinMS
}

// Runtime holds the process-wide registries backing `bash` and `proc://`.
// All state inside Runtime is strictly keyed by sessionID so concurrent
// sessions (including subagents and /new, /resume, /fork, /reload) stay
// isolated and ending one session never disturbs another.
type Runtime struct {
	Jobs      *JobManager
	Services  *ServiceManager
	Artifacts *ArtifactStore

	mu       sync.Mutex
	sessions map[string]*sessionState
}

// NewRuntime constructs an isolated Runtime (used by Register and tests).
func NewRuntime(artifactDir string) *Runtime {
	return &Runtime{
		Jobs:      NewJobManager(JobManagerOptions{}),
		Services:  NewServiceManager(),
		Artifacts: NewArtifactStore(artifactDir),
		sessions:  make(map[string]*sessionState),
	}
}

func (r *Runtime) sessionState(sessionKey string) *sessionState {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.sessions[sessionKey]
	if !ok {
		st = &sessionState{}
		r.sessions[sessionKey] = st
	}
	return st
}

// DisposeSession releases ONLY sessionID's jobs, services, spawned process
// groups, state snapshot, and fallback spill artifacts, leaving the shared
// Runtime and all other sessions completely intact. Safe to call twice or for
// an unknown sessionID.
func (r *Runtime) DisposeSession(sessionID string) {
	if sessionID == "" {
		return
	}
	disposeCtx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()

	// Tear down jobs and services concurrently so they share the shutdown budget
	// instead of Jobs consuming the whole timeout before Services can SIGKILL.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = r.Jobs.DisposeSession(disposeCtx, sessionID)
	}()
	go func() {
		defer wg.Done()
		_ = r.Services.DisposeSession(disposeCtx, sessionID)
	}()
	wg.Wait()

	r.mu.Lock()
	st, ok := r.sessions[sessionID]
	delete(r.sessions, sessionID)
	r.mu.Unlock()
	if ok && st != nil {
		st.dispose()
	}
	clearInterceptorWarningsForSession(sessionID)
	r.Artifacts.RemoveSession(sessionID)
}

// DisposeSessions cleans up every session's snapshot and tracked process groups
// (used only by isolated test runtimes).
func (r *Runtime) DisposeSessions() {
	r.mu.Lock()
	states := make([]*sessionState, 0, len(r.sessions))
	for _, st := range r.sessions {
		states = append(states, st)
	}
	r.sessions = make(map[string]*sessionState)
	r.mu.Unlock()
	for _, st := range states {
		st.dispose()
	}
	r.Artifacts.DisposeAll()
}

var defaultRuntime = NewRuntime("")

// DefaultRuntime returns the package-level Runtime shared across extension instances.
func DefaultRuntime() *Runtime { return defaultRuntime }

// ListJobs returns snapshots of all visible background jobs in DefaultRuntime.
func ListJobs() []JobSnapshot { return defaultRuntime.Jobs.List() }

// GetJob returns a snapshot of a visible background job by ID in DefaultRuntime.
func GetJob(id string) (JobSnapshot, bool) { return defaultRuntime.Jobs.Get(id) }

// CancelJob cancels a running background job by ID in DefaultRuntime.
func CancelJob(id string) bool { return defaultRuntime.Jobs.Cancel(id) }

// WaitJob waits for a specific background job in DefaultRuntime to settle.
func WaitJob(ctx context.Context, id string) (JobSnapshot, bool, error) {
	return defaultRuntime.Jobs.Wait(ctx, id)
}

// WaitAnyJob waits for any visible running background job in DefaultRuntime to settle.
func WaitAnyJob(ctx context.Context) (JobSnapshot, bool, error) {
	return defaultRuntime.Jobs.WaitAny(ctx)
}

// ListServices returns snapshots of all named services in DefaultRuntime.
func ListServices() []ServiceSnapshot { return defaultRuntime.Services.List() }

// GetService returns a snapshot of a named service in DefaultRuntime.
func GetService(name string) (ServiceSnapshot, bool) { return defaultRuntime.Services.Get(name) }

// StopService stops a named service in DefaultRuntime.
func StopService(ctx context.Context, name string) (bool, error) {
	return defaultRuntime.Services.Stop(ctx, name)
}

// ArtifactPath returns the on-disk path for an artifact ID in DefaultRuntime.
func ArtifactPath(id string) (string, bool) { return defaultRuntime.Artifacts.Path(id) }

// Register installs omp's `bash` tool, `proc://` URL handler, `tool_call`
// critical/pattern veto, async result delivery listener, and per-session
// lifecycle cleanup on e. Safe to call repeatedly across fresh per-session
// extension instances: never double-registers process-wide listeners or resets
// shared state. Performs no I/O and never panics.
func Register(e *sdk.Extension) {
	jobs.EnsureDeliveryListener()
	// Keyed by area name in lifecycle: always reaches package-level defaultRuntime.
	lifecycle.OnEnd("shell", func(sessionID string) {
		defaultRuntime.DisposeSession(sessionID)
	})
	_ = urls.Register(NewProcProtocolHandler(defaultRuntime))

	tool := NewBashTool(DefaultConfig(), defaultRuntime)
	bashDef := schemaorder.Apply(tool.Definition())
	e.RegisterTool(intent.Tool(bashDef))
	intent.RegisterSync(e, bashDef)

	// Note: EventToolCall approval veto for bash (and every other tool) is
	// owned by internal/approval.Register; shell registers EvaluateBashToolDecision
	// with internal/approval via init() so there is only ONE EventToolCall veto.
	// See internal/approval/UPSTREAM.md and WIRING NEEDED.
	// delivery, and adjust the bash schema for headless sessions (omit `pty`
	// when no UI and services are disabled, matching tools/bash.ts:1035-1045).
	e.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		if sid := strictSessionID(ctx); sid != "" {
			jobs.BindSession(sid, ctx)
		}
		launchEnabled := readSettingBool(ctx, "launch.enabled", tool.cfg.LaunchEnabled)
		if !ctx.HasUI() && !launchEnabled {
			ctx.RegisterTool(intent.ToolForContext(ctx, schemaorder.Apply(tool.DefinitionForUI(false))))
		}
		return nil, nil
	})
	// End ONLY the shutting-down session via lifecycle.End (never dispose the
	// shared process-wide runtime or treat an empty session ID as "all").
	e.OnSessionShutdown(func(ctx sdk.Context, _ map[string]any) (any, error) {
		if sid := strictSessionID(ctx); sid != "" {
			lifecycle.End(sid)
		}
		return nil, nil
	})
}

// BashTool implements omp's `bash` tool on PiG's Go SDK.
type BashTool struct {
	cfg     Config
	runtime *Runtime
}

// NewBashTool constructs a BashTool with the given config and runtime.
func NewBashTool(cfg Config, rt *Runtime) *BashTool {
	if rt == nil {
		rt = NewRuntime(cfg.ArtifactDir)
	}
	return &BashTool{cfg: cfg, runtime: rt}
}

// Runtime returns the Runtime backing this BashTool.
func (t *BashTool) Runtime() *Runtime { return t.runtime }

// bashParametersSchema returns omp's JSON Schema for the `bash` tool at v19.1.1
// (bashSchemaWithAsyncAndService / bashSchemaWithAsyncNoPty, upstream bash.ts:477-518).
// When includePty is false (headless session without service launch), `pty` is omitted.
func bashParametersSchema(includePty bool) sdk.Schema {
	timeoutDesc := fmt.Sprintf(
		"timeout in seconds; 0 disables the command deadline; nonzero values are clamped to %d-%d",
		MinTimeoutSec, MaxTimeoutSec,
	)
	readyProps := map[string]any{
		"log":     map[string]any{"type": "string", "description": "regex an output line matches once the service is ready"},
		"port":    map[string]any{"type": "number", "description": "TCP port the service accepts connections on once ready"},
		"host":    map[string]any{"type": "string", "description": "host for the port probe; default localhost (127.0.0.1 and ::1)"},
		"timeout": map[string]any{"type": "number", "description": "seconds to wait for readiness; default 30"},
	}
	props := map[string]any{
		"command": map[string]any{"type": "string"},
		"timeout": map[string]any{"type": "number", "description": timeoutDesc},
		"cwd":     map[string]any{"type": "string"},
		"async":   map[string]any{"type": "boolean"},
		"notify":  map[string]any{"type": "string"},
		"name": map[string]any{
			"type":        "string",
			"pattern":     `^(?:\s*|[A-Za-z0-9][A-Za-z0-9._-]{0,47})$`,
			"description": "starts a long-lived supervised service under this name; omit for finite commands, async jobs included. 1-48 of [A-Za-z0-9._-]",
		},
		"ready": map[string]any{
			"type":                 "object",
			"description":          "service readiness; needs log or port",
			"properties":           readyProps,
			"additionalProperties": false,
		},
	}
	if includePty {
		props["pty"] = map[string]any{"type": "boolean"}
	}
	return sdk.Schema{
		"type":                 "object",
		"properties":           props,
		"required":             []string{"command"},
		"additionalProperties": false,
	}
}

// Definition returns the sdk.ToolDefinition for omp's `bash` tool.
func (t *BashTool) Definition() sdk.ToolDefinition {
	// Services always keep `pty` (the daemon/service gives them a terminal of
	// their own, tools/bash.ts:515); finite headless sessions omit `pty` when
	// launch is disabled.
	return t.DefinitionForUI(t.cfg.LaunchEnabled)
}

// DefinitionForUI returns the ToolDefinition tailored to whether `pty` should
// appear in the schema (upstream: tools/bash.ts:1035-1045).
func (t *BashTool) DefinitionForUI(includePty bool) sdk.ToolDefinition {
	return sdk.ToolDefinition{
		Name:          "bash",
		Label:         "Bash",
		Description:   strings.TrimSpace(bashDescription),
		Parameters:    bashParametersSchema(includePty),
		ExecutionMode: "parallel",
		Execute:       t.Execute,
		PrepareLoadout: func(loadout sdk.ToolLoadout) *sdk.ToolLoadoutChanges {
			if PrepareLoadout == nil {
				return nil
			}
			return PrepareLoadout(loadout)
		},
	}
}

type bashInput struct {
	Command    string
	HasTimeout bool
	Timeout    float64
	Cwd        string
	Name       string
	RawReady   map[string]any
	Ready      *ServiceReady
	Async      bool
	Notify     string
	HasPty     bool
	Pty        bool
}

func parseBashInput(params map[string]any) (bashInput, error) {
	var in bashInput
	rawCmd, ok := params["command"].(string)
	if !ok {
		return in, sdk.NewToolError("Missing required parameter: command")
	}
	in.Command = rawCmd
	if v, present := params["timeout"]; present && v != nil {
		switch n := v.(type) {
		case float64:
			if !math.IsNaN(n) && !math.IsInf(n, 0) {
				in.HasTimeout = true
				in.Timeout = n
			}
		case int:
			in.HasTimeout = true
			in.Timeout = float64(n)
		case int64:
			in.HasTimeout = true
			in.Timeout = float64(n)
		}
	}
	if s, ok := params["cwd"].(string); ok {
		in.Cwd = s
	}
	if s, ok := params["name"].(string); ok {
		in.Name = strings.TrimSpace(s)
	}
	if b, ok := params["async"].(bool); ok {
		in.Async = b
	}
	if s, ok := params["notify"].(string); ok {
		in.Notify = strings.TrimSpace(s)
	}
	if b, ok := params["pty"].(bool); ok {
		in.HasPty = true
		in.Pty = b
	}
	if rawReady, ok := params["ready"].(map[string]any); ok {
		in.RawReady = rawReady
		in.Ready = normalizeReady(rawReady)
	}
	return in, nil
}

// normalizeReady drops placeholder/empty readiness fields, returning nil when
// neither `log` nor a non-zero `port` survives. upstream: bash.ts:569
func normalizeReady(m map[string]any) *ServiceReady {
	if len(m) == 0 {
		return nil
	}
	var r ServiceReady
	if s, ok := m["log"].(string); ok {
		if trimmed := strings.TrimSpace(s); trimmed != "" {
			r.Log = trimmed
		}
	}
	if v, ok := m["port"].(float64); ok && !math.IsNaN(v) && !math.IsInf(v, 0) && v != 0 {
		p := int(v)
		r.Port = &p
	} else if v, ok := m["port"].(int); ok && v != 0 {
		p := v
		r.Port = &p
	}
	if r.Log == "" && r.Port == nil {
		return nil
	}
	if s, ok := m["host"].(string); ok {
		if trimmed := strings.TrimSpace(s); trimmed != "" {
			r.Host = trimmed
		}
	}
	if v, ok := m["timeout"].(float64); ok && !math.IsNaN(v) && !math.IsInf(v, 0) {
		t := v
		r.Timeout = &t
	} else if v, ok := m["timeout"].(int); ok {
		t := float64(v)
		r.Timeout = &t
	}
	return &r
}

func hasRawReadyHint(m map[string]any) bool {
	if len(m) == 0 {
		return false
	}
	if s, ok := m["host"].(string); ok && strings.TrimSpace(s) != "" {
		return true
	}
	switch v := m["timeout"].(type) {
	case float64:
		return !math.IsNaN(v) && !math.IsInf(v, 0)
	case int:
		return true
	}
	return false
}

// clampTimeoutFloat mirrors packages/coding-agent/src/tools/tool-timeouts.ts:34
// without rounding fractional seconds (upstream keeps the raw float).
func clampTimeoutFloat(requestedSec float64, maxTimeoutSec int) float64 {
	t := requestedSec
	if maxTimeoutSec > 0 && t > float64(maxTimeoutSec) {
		t = float64(maxTimeoutSec)
	}
	if t < float64(MinTimeoutSec) {
		t = float64(MinTimeoutSec)
	}
	if t > float64(MaxTimeoutSec) {
		t = float64(MaxTimeoutSec)
	}
	return t
}

func formatTimeoutClampNotice(requestedSec, effectiveSec float64, maxTimeoutSec int) string {
	if requestedSec == effectiveSec {
		return ""
	}
	limit := fmt.Sprintf("allowed range %d-%ds", MinTimeoutSec, MaxTimeoutSec)
	if maxTimeoutSec > 0 && effectiveSec == float64(maxTimeoutSec) && maxTimeoutSec < MaxTimeoutSec {
		limit = fmt.Sprintf("global tools.maxTimeout ceiling %ds", maxTimeoutSec)
	}
	return fmt.Sprintf("Timeout clamped to %ss (requested %ss; %s).",
		formatSecondsLabel(effectiveSec), formatSecondsLabel(requestedSec), limit)
}

func timeoutDetailValue(secs float64) any {
	if secs == float64(int(secs)) {
		return int(secs)
	}
	return secs
}

// Execute runs omp's `bash` tool flow at v19.1.1.
// upstream: packages/coding-agent/src/tools/bash.ts:1768
func (t *BashTool) Execute(ctx sdk.Context, params map[string]any) (any, error) {
	in, err := parseBashInput(params)
	if err != nil {
		return nil, err
	}

	var zeroCtx sdk.Context
	hasHostCtx := ctx != zeroCtx
	sessionKey := "default"
	sessionFile := ""
	toolCallID := ""
	hostCwd := ""
	if hasHostCtx {
		sessionKey = sessionKeyFromContext(ctx)
		// Bind only if no live session_start Context is already bound, so a
		// cancelled tool-call Context can never overwrite the session_start Context.
		jobs.BindSessionIfMissing(sessionKey, ctx)
		if sf, sfErr := ctx.GetSessionFile(); sfErr == nil && sf != nil {
			sessionFile = *sf
		}
		toolCallID = ctx.ToolCallID()
		hostCwd = ctx.Cwd()
	}
	if hostCwd == "" {
		if wd, wdErr := os.Getwd(); wdErr == nil {
			hostCwd = wd
		}
	}
	hostCwd = dirs.StandardizeMacOSPath(hostCwd)
	rawCommand := in.Command
	command := rawCommand
	cwdParam := in.Cwd
	hoistedShellCwd := false
	var cdSeparator string

	// Extract a leading `cd <path> && ...` or `cd <path>; ...` into cwd when the
	// model omits cwd. `;` hoists only onto an existing host directory.
	// Hoisted targets use resolveShellDirectory (`/` is the real root); explicit
	// tool `cwd` uses resolveToCwd (`/` aliases hostCwd).
	// upstream: bash.ts:1828-1842
	if cwdParam == "" {
		if cdPath, rest, sep, ok := extractLeadingCdTarget(command); ok {
			if sep == "&&" {
				cwdParam = cdPath
				command = rest
				hoistedShellCwd = true
			} else if sep == ";" {
				resolved := resolveShellDirectory(cdPath, hostCwd)
				if info, statErr := os.Stat(resolved); statErr == nil && info.IsDir() {
					cwdParam = cdPath
					command = rest
					hoistedShellCwd = true
					cdSeparator = ";"
				}
			}
		}
	}

	// Mode validation (upstream: bash.ts:1848-1894).
	asyncRequested := in.Async || (in.Notify != "" && in.Name == "")
	var pendingNotices []string

	// Resolve live session settings at call time matching omp's upstream units:
	//   - launch.enabled (bool, default true)
	//   - async.enabled (bool, default true)
	//   - bash.autoBackground.enabled (bool, default true)
	//   - bash.autoBackground.thresholdMs (number, ms, default 60000)
	//   - tools.artifactSpillThreshold (number, KB -> *1024 bytes, default 50 => 51200)
	//   - tools.artifactHeadBytes (number, KB -> *1024 bytes, default 20 => 20480; 0 disables)
	//   - tools.artifactTailBytes (number, KB -> *1024 bytes, default 20 => 20480)
	//   - tools.outputMaxColumns (number, bytes/line, default 768; 0 disables)
	// upstream: packages/coding-agent/src/tools/settings.ts:12-104,661,1051,1073;
	// packages/coding-agent/src/exec/settings.ts:142-319;
	// packages/coding-agent/src/tools/output-meta.ts:508-563;
	// packages/coding-agent/src/tools/bash.ts:1009-1013,1856,1877,2044,2098-2105
	launchEnabled := readSettingBool(ctx, "launch.enabled", t.cfg.LaunchEnabled)
	asyncEnabled := readSettingBool(ctx, "async.enabled", t.cfg.AsyncEnabled)
	autoBgEnabled := readSettingBool(ctx, "bash.autoBackground.enabled", t.cfg.AutoBackgroundEnabled)
	autoBgThreshold := t.cfg.AutoBackgroundThreshold
	if rawMs := readSettingFloat(ctx, "bash.autoBackground.thresholdMs", -1); rawMs >= 0 {
		autoBgThreshold = time.Duration(math.Floor(rawMs)) * time.Millisecond
	}
	spillThreshold := t.cfg.SpillThreshold
	if rawKB := readSettingFloat(ctx, "tools.artifactSpillThreshold", -1); rawKB > 0 {
		spillThreshold = kbToBytes(rawKB)
	}
	headBytes := t.cfg.HeadBytes
	if rawKB := readSettingFloat(ctx, "tools.artifactHeadBytes", -1); rawKB >= 0 {
		headBytes = kbToBytes(rawKB)
	}
	// tools.artifactTailBytes (KB, default 20): in omp OutputSink uses
	// tailLimit = spillThreshold - headLimit (streaming-output.ts:1022-1023,1269)
	// where default 50 KiB - 20 KiB = 30 KiB, while spillLargeResultToArtifact
	// (output-meta.ts:646) uses headBytes + tailBytes. Honour an explicit smaller
	// non-default tail cap when configured, without shrinking the default 50 KiB
	// OutputSink budget (50 - 20 = 30 KiB) when settings holds the default 20.
	tailBytes := t.cfg.TailBytes
	if rawKB := readSettingFloat(ctx, "tools.artifactTailBytes", -1); rawKB > 0 {
		if rawKB != 20 {
			tailBytes = kbToBytes(rawKB)
		}
	}
	maxColumns := t.cfg.MaxColumns
	if rawCols := readSettingFloat(ctx, "tools.outputMaxColumns", -1); rawCols >= 0 {
		maxColumns = int(math.Floor(rawCols))
	}

	if in.Name != "" {
		if !launchEnabled {
			return nil, sdk.NewToolError("Service launch is disabled in this session.")
		}
		if !serviceNameRE.MatchString(in.Name) {
			return nil, sdk.NewToolError("Service name must be 1-48 letters, numbers, dots, underscores, or hyphens")
		}
		if in.Ready == nil && (in.Async || in.HasTimeout) {
			return nil, sdk.NewToolError("`name` starts a long-lived service, which takes no async or timeout. Omit `name` for a finite command (async jobs included), or drop async/timeout to start a service.")
		}
		if in.HasTimeout {
			pendingNotices = append(pendingNotices, "Ignored timeout: service-only; readiness follows ready.timeout.")
		}
		if in.Async {
			pendingNotices = append(pendingNotices, "Ignored async: service-only; a service start is asynchronous already.")
		}
		if in.Ready == nil && hasRawReadyHint(in.RawReady) {
			pendingNotices = append(pendingNotices, "Ignored ready: it needs log or port; the service started without a readiness wait.")
		}
	} else if in.Ready != nil {
		pendingNotices = append(pendingNotices, "Ignored ready: service-only, and no service name was given.")
	}
	if in.Name == "" && asyncRequested && !asyncEnabled {
		return nil, sdk.NewToolError("Async bash execution is disabled. Enable async.enabled to use async mode.")
	}

	var notifyPattern *NotifyPattern
	var notifyNotice string
	if in.Notify != "" {
		var compErr error
		notifyPattern, compErr = CompileNotifyPattern(in.Notify)
		if compErr != nil {
			return nil, sdk.NewToolError(compErr.Error())
		}
		notifyNotice = fmt.Sprintf("Output lines matching /%s/ arrive as follow-ups while it runs.", in.Notify)
	}

	// Collect mounted/special tool command names for interceptor bypass and PTY refusal.
	// upstream: bash.ts:1895-1905
	var toolCmdSet map[string]bool
	if CommandNames != nil {
		if names := CommandNames(ctx); len(names) > 0 {
			toolCmdSet = make(map[string]bool, len(names))
			for _, n := range names {
				if n != "" {
					toolCmdSet[n] = true
				}
			}
		}
	}

	// Bash interceptor check on both rawCommand and cwd-normalized command.
	// upstream: bash.ts:815, 1897
	interceptorEnabled := t.cfg.InterceptorEnabled
	if v, ok := readSetting(ctx, "bashInterceptor.enabled").(bool); ok {
		interceptorEnabled = v
	}
	if interceptorEnabled {
		rules := t.cfg.InterceptorRules
		if parsed := parseInterceptorRules(readSessionSetting(ctx, "bashInterceptor.patterns")); len(parsed) > 0 {
			rules = parsed
		} else if parsed := parseInterceptorRules(readSetting(ctx, "bashInterceptor.patterns")); len(parsed) > 0 {
			rules = parsed
		}
		if msg := checkCommandInterception(ctx, rawCommand, command, in.Name, t.cfg.AvailableTools, rules, toolCmdSet); msg != "" {
			return nil, sdk.NewToolError(msg)
		}
	}
	// Checked BEFORE DirectCommand and before any spawn so nothing runs.
	if in.Pty {
		if invoked := detectInvokedToolCommand(ctx, rawCommand, command, toolCmdSet); invoked != "" {
			return nil, sdk.NewToolError(fmt.Sprintf("`%s` is a session tool command and cannot run in a PTY; run it without `pty`.", invoked))
		}
	}
	// Resolve and validate working directory (upstream: bash.ts:686, 1944-1975).
	commandCwd := hostCwd
	var ranCwd string
	if cwdParam != "" {
		if hoistedShellCwd {
			commandCwd = resolveShellDirectory(cwdParam, hostCwd)
		} else {
			commandCwd = resolveToCwd(cwdParam, hostCwd)
		}
		commandCwd = dirs.StandardizeMacOSPath(commandCwd)
		ranCwd = commandCwd
	}
	info, statErr := os.Stat(commandCwd)
	if statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return nil, sdk.NewToolError(fmt.Sprintf("Working directory does not exist: %s", commandCwd))
		}
		return nil, statErr
	}
	if !info.IsDir() {
		return nil, sdk.NewToolError(fmt.Sprintf("Working directory is not a directory: %s", commandCwd))
	}

	// Check restricting-mode gate (e.g. plan mode) AFTER resolving target and
	// BEFORE running anything (sync, async, direct, or service).
	if gateErr := gate.Check(ctx, gate.Request{
		Tool:   "bash",
		Op:     gate.OpExec,
		Detail: command,
	}); gateErr != nil {
		return nil, sdk.NewToolError(gateErr.Error())
	}

	// Optional shared-checkout git guard (bash.gitGuard, default false, operator authority).
	// Disabled by default: incurs zero overhead and spawns zero subprocesses when false.
	// upstream: packages/coding-agent/src/exec/settings.ts:155-166, crates/shell/src/git/guard.rs
	if readSettingBool(ctx, "bash.gitGuard", false) {
		guardCtx := context.Background()
		var cancelGuard context.CancelFunc
		if hasHostCtx && ctx.Done() != nil {
			guardCtx, cancelGuard = context.WithCancel(context.Background())
			go func() {
				select {
				case <-ctx.Done():
					cancelGuard()
				case <-guardCtx.Done():
				}
			}()
		}
		var sessState *sessionState
		if t.runtime != nil && sessionKey != "" {
			sessState = t.runtime.sessionState(sessionKey)
		}
		guardDec := checkBashGitGuardWithState(guardCtx, command, commandCwd, sessState)
		if cancelGuard != nil {
			cancelGuard()
		}
		if guardDec.Refusal != "" {
			return nil, sdk.NewToolError(guardDec.Refusal)
		}
		if len(guardDec.Notes) > 0 {
			pendingNotices = append(pendingNotices, guardDec.Notes...)
		}
	}

	stampCwd := func(res any) any {
		if ranCwd == "" {
			return res
		}
		tr, ok := res.(sdk.ToolResult)
		if !ok {
			return res
		}
		det, _ := tr.Details.(map[string]any)
		if det == nil {
			det = map[string]any{}
		}
		det["cwd"] = ranCwd
		if cdSeparator != "" {
			det["cdSeparator"] = cdSeparator
		}
		tr.Details = det
		return tr
	}

	// Resolve global maxTimeout setting if provided.
	maxTimeoutSec := t.cfg.MaxTimeoutSec
	if v, ok := readSetting(ctx, "tools.maxTimeout").(float64); ok && !math.IsNaN(v) && v >= 0 {
		maxTimeoutSec = int(v)
	} else if v, ok := readSetting(ctx, "tools.maxTimeout").(int); ok && v >= 0 {
		maxTimeoutSec = v
	}

	// Timeout resolution (upstream: bash.ts:2042). Computed before DirectCommand
	// so direct tool-command execution receives the effective command timeout.
	requestedTimeoutSec := float64(DefaultTimeoutSec)
	if in.HasTimeout {
		requestedTimeoutSec = in.Timeout
	}
	timeoutDisabled := in.HasTimeout && in.Timeout == 0
	var timeoutSec *float64
	var timeoutDur *time.Duration
	var directTimeout time.Duration
	if !timeoutDisabled {
		eff := clampTimeoutFloat(requestedTimeoutSec, maxTimeoutSec)
		timeoutSec = &eff
		d := time.Duration(eff * float64(time.Second))
		timeoutDur = &d
		directTimeout = d
	}

	// DirectCommand fast path for pure tool-command lines before spawning a shell.
	// Output is pushed through OutputSink so sanitization, 50 KB truncation,
	// column cap, and local://N.log spill behave identically to shell runs.
	// Note: `in.Pty` is already refused pre-execution above for any line
	// invoking a tool command; never invoke DirectCommand when `in.Pty` is true
	// so a misconfigured nil InvokedCommand/CommandNames can never execute a
	// tool command under `pty: true`.
	if in.Name == "" && !asyncRequested && !in.Pty && DirectCommand != nil {
		start := t.now()
		if out, exitCode, timedOut, cancelled, handled := DirectCommand(ctx, command, commandCwd, directTimeout); handled {
			wallMS := float64(t.now().Sub(start).Microseconds()) / 1000.0
			if timeoutSec != nil {
				if notice := formatTimeoutClampNotice(requestedTimeoutSec, *timeoutSec, maxTimeoutSec); notice != "" {
					pendingNotices = append(pendingNotices, notice)
				}
			}
			sink := NewOutputSink(OutputSinkOptions{
				SpillThreshold: spillThreshold,
				HeadBytes:      headBytes,
				TailBytes:      tailBytes,
				MaxColumns:     maxColumns,
				ArtifactStore:  t.runtime.Artifacts,
				SessionID:      sessionKey,
				SDKContext:     ctx,
			})
			sink.Push(out)
			var res ExecResult
			switch {
			case timedOut:
				notice := "Command timed out"
				if timeoutSec != nil {
					notice = fmt.Sprintf("Command timed out after %s seconds", formatSecondsLabel(*timeoutSec))
				}
				res = ExecResult{
					OutputSummary: sink.Dump(notice),
					Cancelled:     true,
					TimedOut:      true,
				}
			case cancelled:
				res = ExecResult{
					OutputSummary: sink.Dump("Command cancelled"),
					Cancelled:     true,
				}
			default:
				ec := exitCode
				res = ExecResult{
					OutputSummary: sink.Dump(""),
					ExitCode:      &ec,
				}
			}
			tr, buildErr := buildCompletedResultWithOptions(res, timeoutSec, requestedTimeoutSec, pendingNotices, wallMS, resolveInlineByteCapBudget(spillThreshold), t.wallTimeNoticeMinMS())
			if buildErr != nil {
				return nil, buildErr
			}
			tr = stampToolCommands(ctx, command, tr)
			return stampCwd(tr), nil
		}
	}

	// Acquire preamble + credential release callback once for this call.
	// Ownership transfers to the async job / service goroutine once started, or
	// is released immediately when this call finishes or fails to start.
	var preambleText string
	var preambleRelease func()
	if CommandPreamble != nil {
		var rawRelease func()
		preambleText, rawRelease = CommandPreamble(ctx)
		preambleRelease = onceFunc(rawRelease)
	} else {
		preambleRelease = func() {}
	}
	releaseOwned := true
	defer func() {
		if releaseOwned {
			preambleRelease()
		}
	}()

	// Load repo direnv diff (when bash.direnv != "off") before spawning a shell,
	// clamping the load timeout to any positive command deadline.
	// upstream: packages/coding-agent/src/exec/bash-executor.ts:267-297,770-780
	direnvSetting := resolveDirenvSetting(readSetting(ctx, "bash.direnv"))
	if resolveDirenvSetting(readSessionSetting(ctx, "bash.direnv")) == "off" || (hasHostCtx && !shellProjectTrusted(ctx)) {
		direnvSetting = "off"
	}
	direnvTimeoutMs := readSettingFloat(ctx, "bash.direnvLoadTimeoutMs", float64(DefaultDirenvTimeoutMs))
	direnvLoadTimeout := resolveDirenvLoadTimeout(direnvTimeoutMs, timeoutDur)
	direnvCtx := context.Background()
	if hasHostCtx && ctx.Done() != nil {
		var cancelDirenv context.CancelFunc
		direnvCtx, cancelDirenv = context.WithCancel(context.Background())
		go func() {
			select {
			case <-ctx.Done():
				cancelDirenv()
			case <-direnvCtx.Done():
			}
		}()
		defer cancelDirenv()
	}
	runCommand, direnvDiff := applyDirenvPreflight(direnvCtx, command, commandCwd, direnvSetting, direnvLoadTimeout)

	// Named service mode (upstream: bash.ts:1977).
	if in.Name != "" {
		usePTY := true
		if in.HasPty {
			usePTY = in.Pty
		}
		releaseOwned = false // executeService owns preambleRelease (releases on error or service exit)
		res, svcErr := t.executeService(ctx, serviceExecParams{
			sessionKey:      sessionKey,
			sessionFile:     sessionFile,
			toolCallID:      toolCallID,
			name:            in.Name,
			command:         runCommand,
			cwd:             commandCwd,
			pty:             usePTY,
			ready:           in.Ready,
			notify:          notifyPattern,
			notifyNotice:    notifyNotice,
			notices:         pendingNotices,
			preamble:        preambleText,
			preambleRelease: preambleRelease,
			spillThreshold:  spillThreshold,
			headBytes:       headBytes,
			tailBytes:       tailBytes,
			maxColumns:      maxColumns,
			direnvDiff:      direnvDiff,
		})
		if svcErr != nil {
			return nil, svcErr
		}
		return stampCwd(res), nil
	}

	if timeoutSec != nil {
		if notice := formatTimeoutClampNotice(requestedTimeoutSec, *timeoutSec, maxTimeoutSec); notice != "" {
			pendingNotices = append(pendingNotices, notice)
		}
	}

	// Explicit async mode (or `notify` without `name`, upstream: bash.ts:2052).
	if asyncRequested {
		job, _, regErr := t.startManagedJob(ctx, managedJobParams{
			command:             runCommand,
			displayCommand:      command,
			commandCwd:          commandCwd,
			sessionKey:          sessionKey,
			sessionFile:         sessionFile,
			toolCallID:          toolCallID,
			intent:              intent.For(ctx, toolCallID),
			useSessionState:     false,
			timeoutDur:          timeoutDur,
			timeoutSec:          timeoutSec,
			requestedTimeoutSec: requestedTimeoutSec,
			notices:             pendingNotices,
			foreground:          false,
			notify:              notifyPattern,
			preamble:            preambleText,
			preambleRelease:     preambleRelease,
			spillThreshold:      spillThreshold,
			headBytes:           headBytes,
			tailBytes:           tailBytes,
			maxColumns:          maxColumns,
			direnvDiff:          direnvDiff,
		})
		if regErr != nil {
			return nil, sdk.NewToolError(regErr.Error())
		}
		releaseOwned = false // job goroutine calls preambleRelease on finish
		startNotices := pendingNotices
		if notifyNotice != "" {
			startNotices = append(append([]string(nil), pendingNotices...), notifyNotice)
		}
		return stampCwd(buildBackgroundStartResult(job.id, "", timeoutSec, requestedTimeoutSec, startNotices)), nil
	}

	// Auto-background mode for non-PTY calls (upstream: bash.ts:2097).
	if autoBgEnabled && !in.Pty && !t.runtime.Jobs.AtCapacityForSession(sessionKey) {
		threshold := autoBgThreshold
		if threshold == 0 {
			threshold = DefaultAutoBackgroundThreshold
		}
		waitBudget := resolveAutoBackgroundWait(threshold, timeoutDur)
		startBackgrounded := waitBudget == 0

		job, completionCh, regErr := t.startManagedJob(ctx, managedJobParams{
			command:             runCommand,
			displayCommand:      command,
			commandCwd:          commandCwd,
			sessionKey:          sessionKey,
			sessionFile:         sessionFile,
			toolCallID:          toolCallID,
			intent:              intent.For(ctx, toolCallID),
			useSessionState:     !startBackgrounded,
			timeoutDur:          timeoutDur,
			timeoutSec:          timeoutSec,
			requestedTimeoutSec: requestedTimeoutSec,
			notices:             pendingNotices,
			foreground:          !startBackgrounded,
			preamble:            preambleText,
			preambleRelease:     preambleRelease,
			spillThreshold:      spillThreshold,
			headBytes:           headBytes,
			tailBytes:           tailBytes,
			maxColumns:          maxColumns,
			direnvDiff:          direnvDiff,
		})
		if regErr != nil {
			return nil, sdk.NewToolError(regErr.Error())
		}
		releaseOwned = false // job goroutine calls preambleRelease on finish
		if startBackgrounded {
			return stampCwd(buildBackgroundStartResult(job.id, "", timeoutSec, requestedTimeoutSec, pendingNotices)), nil
		}

		timer := time.NewTimer(waitBudget)
		defer timer.Stop()

		select {
		case settled := <-completionCh:
			t.runtime.Jobs.releaseForegroundJob(job.id)
			if settled.err != nil {
				return nil, settled.err
			}
			return stampCwd(settled.result), nil
		case <-ctx.Done():
			t.runtime.Jobs.Cancel(job.id)
			t.runtime.Jobs.releaseForegroundJob(job.id)
			latest := job.snapshot().ProgressText
			if latest == "" {
				latest = "Command aborted"
			}
			return nil, sdk.NewToolError(latest)
		case <-timer.C:
			t.runtime.Jobs.promoteToBackground(job.id)
			latest := job.snapshot().ProgressText
			return stampCwd(buildBackgroundStartResult(job.id, latest, timeoutSec, requestedTimeoutSec, pendingNotices)), nil
		}
	}

	// Direct foreground execution (upstream: bash.ts:2497).
	// For finite commands, there is no interactive frontend overlay forwarding
	// user keystrokes into a PTY yet, so always emit omp's exact fallback notice
	// and run without a terminal (preventing password/interactive prompts from
	// hanging until the deadline). Named services keep a real Unix PTY.
	if in.Pty {
		pendingNotices = append(pendingNotices, "pty requested but unavailable in this environment; ran without a terminal")
	}

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	if hasHostCtx {
		go func() {
			select {
			case <-ctx.Done():
				cancelRun()
			case <-runCtx.Done():
			}
		}()
	}

	var timeoutVal time.Duration
	if timeoutDur != nil {
		timeoutVal = *timeoutDur
	}

	var stateLoad, stateSave, stateNonce string
	var statePipeFD int
	var commitState func(bool, string, bool) string
	st := t.runtime.sessionState(sessionKey)
	if t.cfg.PersistentState {
		stateLoad, stateSave, stateNonce, statePipeFD, commitState = st.prepareRun()
	}

	preamblePath, cleanupPreamble, _ := writePreambleTempFile(preambleText)
	defer cleanupPreamble()

	start := t.now()
	res, execErr := executeCommand(runCtx, ExecOptions{
		Command:        runCommand,
		Cwd:            commandCwd,
		Env:            buildCommandEnvWithDirenv(sessionKey, sessionFile, toolCallID, false, direnvDiff),
		Timeout:        timeoutVal,
		SpillThreshold: spillThreshold,
		HeadBytes:      headBytes,
		TailBytes:      tailBytes,
		MaxColumns:     maxColumns,
		ArtifactStore:  t.runtime.Artifacts,
		SessionID:      sessionKey,
		SDKContext:     ctx,
		StateLoadFile:  stateLoad,
		StateSaveFile:  stateSave,
		StateNonce:     stateNonce,
		StatePipeFD:    statePipeFD,
		PreambleFile:   preamblePath,
		UsePTY:         false,
		OnSpawnPGID:    st.trackPGID,
		OnExitPGID:     st.releasePGID,
		OnPreview: func(text string) {
			if hasHostCtx {
				_ = ctx.OnUpdate(map[string]any{
					"content": text,
					"details": map[string]any{},
				})
			}
		},
	})
	wallMS := float64(t.now().Sub(start).Microseconds()) / 1000.0
	if commitState != nil {
		if stateNotice := commitState(execErr == nil && !res.Cancelled && !res.TimedOut, res.CapturedState, res.StateCaptureTrunc); stateNotice != "" {
			pendingNotices = append(pendingNotices, "["+stateNotice+"]")
		}
	}
	if execErr != nil {
		return nil, sdk.NewToolError(execErr.Error())
	}
	if len(res.ShellNotes) > 0 {
		pendingNotices = append(pendingNotices, res.ShellNotes...)
	}

	tr, buildErr := buildCompletedResultWithOptions(res, timeoutSec, requestedTimeoutSec, pendingNotices, wallMS, resolveInlineByteCapBudget(spillThreshold), t.wallTimeNoticeMinMS())
	if buildErr != nil {
		return nil, buildErr
	}
	tr = stampToolCommands(ctx, command, tr)
	return stampCwd(tr), nil
}

type managedJobParams struct {
	command             string
	displayCommand      string
	commandCwd          string
	sessionKey          string
	sessionFile         string
	toolCallID          string
	intent              string
	useSessionState     bool
	timeoutDur          *time.Duration
	timeoutSec          *float64
	requestedTimeoutSec float64
	notices             []string
	foreground          bool
	notify              *NotifyPattern
	preamble            string
	preambleRelease     func()
	spillThreshold      int
	headBytes           int
	tailBytes           int
	maxColumns          int
	direnvDiff          *DirenvExportDiff
}
type managedJobSettlement struct {
	result sdk.ToolResult
	err    error
}

func formatAsyncJobLabel(command string) string {
	collapsed := collapseSingleLine(command)
	if len(collapsed) <= 120 {
		return collapsed
	}
	return collapsed[:117] + "..."
}

func (t *BashTool) startManagedJob(ctx sdk.Context, p managedJobParams) (*managedJob, <-chan managedJobSettlement, error) {
	displayCmd := p.displayCommand
	if displayCmd == "" {
		displayCmd = p.command
	}
	label := formatAsyncJobLabel(displayCmd)
	completionCh := make(chan managedJobSettlement, 1)
	notices := append([]string(nil), p.notices...)

	var timeoutVal time.Duration
	if p.timeoutDur != nil {
		timeoutVal = *p.timeoutDur
	}

	var zero sdk.Context
	hasHostCtx := ctx != zero

	job, err := t.runtime.Jobs.register(p.sessionKey, "bash", label, displayCmd, p.commandCwd, p.intent, p.foreground, func(
		runCtx context.Context,
		jobID string,
		reportProgress func(string),
		setDetails func(map[string]any),
		setPIDs func(func() []int),
	) (string, error) {
		if p.preambleRelease != nil {
			defer p.preambleRelease()
		}
		var stateLoad, stateSave, stateNonce string
		var statePipeFD int
		var commitState func(bool, string, bool) string
		var cleanupSeed func()
		st := t.runtime.sessionState(p.sessionKey)
		if t.cfg.PersistentState {
			if p.useSessionState {
				// Foreground-started (including auto-backgrounded) jobs commit
				// their state delta back into the session when they settle, and
				// overlapping calls merge their deltas cleanly too.
				stateLoad, stateSave, stateNonce, statePipeFD, commitState = st.prepareRun()
			} else {
				// Explicit async jobs start from a read-only copy of session state.
				stateLoad, cleanupSeed = st.snapshotReadOnly()
			}
		}
		if cleanupSeed != nil {
			defer cleanupSeed()
		}

		preamblePath, cleanupPreamble, _ := writePreambleTempFile(p.preamble)
		defer cleanupPreamble()

		var notifier *OutputNotifier
		if p.notify != nil {
			notifier = NewOutputNotifier(p.notify, func(batch NotifyBatch) {
				running := true
				if j, ok := jobs.Get(p.sessionKey, jobID); ok {
					running = j.Status() == "running"
				}
				_ = jobs.DeliverNotify(p.sessionKey, []jobs.NotifyReport{{
					ID:      jobID,
					Type:    "bash",
					Command: displayCmd,
					Pattern: p.notify.Source,
					Lines:   batch.Lines,
					Omitted: batch.Omitted,
					Running: running,
					Stopped: batch.Stopped,
				}})
			}, OutputNotifierOptions{})
			defer notifier.Stop()
		}

		start := t.now()
		res, execErr := executeCommand(runCtx, ExecOptions{
			Command:        p.command,
			Cwd:            p.commandCwd,
			Env:            buildCommandEnvWithDirenv(p.sessionKey, p.sessionFile, p.toolCallID, false, p.direnvDiff),
			Timeout:        timeoutVal,
			SpillThreshold: p.spillThreshold,
			HeadBytes:      p.headBytes,
			TailBytes:      p.tailBytes,
			MaxColumns:     p.maxColumns,
			ArtifactStore:  t.runtime.Artifacts,
			SessionID:      p.sessionKey,
			SDKContext:     ctx,
			StateLoadFile:  stateLoad,
			StateSaveFile:  stateSave,
			StateNonce:     stateNonce,
			StatePipeFD:    statePipeFD,
			PreambleFile:   preamblePath,
			OnSpawnPGID:    st.trackPGID,
			OnExitPGID:     st.releasePGID,
			OnChunk: func(chunk string) {
				if jobRef := t.runtime.Jobs.jobByID(jobID); jobRef != nil {
					jobRef.pushWidgetChunk(chunk)
				}
				if notifier != nil {
					notifier.Push(chunk)
				}
			},
			OnPreview: func(text string) {
				reportProgress(text)
				if hasHostCtx {
					_ = ctx.OnUpdate(map[string]any{
						"content": text,
						"details": map[string]any{
							"async": map[string]any{
								"state": "running",
								"jobId": jobID,
								"type":  "bash",
							},
						},
					})
				}
			},
			OnStart: setPIDs,
		})
		wallMS := float64(t.now().Sub(start).Microseconds()) / 1000.0
		if notifier != nil {
			notifier.Stop()
		}
		if commitState != nil {
			if stateNotice := commitState(execErr == nil && !res.Cancelled && !res.TimedOut, res.CapturedState, res.StateCaptureTrunc); stateNotice != "" {
				notices = append(notices, "["+stateNotice+"]")
			}
		}
		if execErr != nil {
			toolErr := sdk.NewToolError(execErr.Error())
			completionCh <- managedJobSettlement{err: toolErr}
			return "", toolErr
		}
		if len(res.ShellNotes) > 0 {
			notices = append(notices, res.ShellNotes...)
		}

		toolRes, buildErr := buildCompletedResultWithOptions(res, p.timeoutSec, p.requestedTimeoutSec, notices, wallMS, resolveInlineByteCapBudget(p.spillThreshold), t.wallTimeNoticeMinMS())
		if buildErr != nil {
			completionCh <- managedJobSettlement{err: buildErr}
			return "", buildErr
		}
		toolRes = stampToolCommands(ctx, displayCmd, toolRes)
		if det, ok := toolRes.Details.(map[string]any); ok {
			setDetails(det)
		}
		completionCh <- managedJobSettlement{result: toolRes}
		if toolRes.IsError {
			return toolRes.Content, errors.New(toolRes.Content)
		}
		return toolRes.Content, nil
	})
	if err != nil {
		return nil, nil, err
	}
	return job, completionCh, nil
}

// buildCompletedResult formats omp's model-facing result text, details, and
// error status for a completed, timed-out, or cancelled execution.
// upstream: packages/coding-agent/src/tools/bash.ts:1278-1398
func buildCompletedResult(
	res ExecResult,
	timeoutSec *float64,
	requestedTimeoutSec float64,
	extraNotices []string,
	wallTimeMS float64,
) (sdk.ToolResult, error) {
	return buildCompletedResultWithCap(res, timeoutSec, requestedTimeoutSec, extraNotices, wallTimeMS, resolveInlineByteCapBudget(DefaultSpillThreshold))
}

func buildCompletedResultWithCap(
	res ExecResult,
	timeoutSec *float64,
	requestedTimeoutSec float64,
	extraNotices []string,
	wallTimeMS float64,
	inlineByteCap int,
) (sdk.ToolResult, error) {
	return buildCompletedResultWithOptions(res, timeoutSec, requestedTimeoutSec, extraNotices, wallTimeMS, inlineByteCap, WallTimeNoticeMinMS)
}

func buildCompletedResultWithOptions(
	res ExecResult,
	timeoutSec *float64,
	requestedTimeoutSec float64,
	extraNotices []string,
	wallTimeMS float64,
	inlineByteCap int,
	wallTimeMinMS float64,
) (sdk.ToolResult, error) {
	if res.Cancelled && !res.TimedOut {
		out := res.Output
		var msg string
		switch {
		case strings.HasPrefix(out, "[Command cancelled]"):
			msg = out
		case out != "":
			msg = out + "\n\n[Command aborted]"
		default:
			msg = "Command aborted"
		}
		if res.ArtifactError != "" {
			msg += "\n\n[" + formatArtifactErrorNotice(res.ArtifactError) + "]"
		}
		return sdk.ToolResult{}, sdk.NewToolError(msg)
	}

	failedExit := res.ExitCode != nil && *res.ExitCode != 0

	body := res.Output
	if body == "" {
		if res.ExitCode != nil && *res.ExitCode == 0 {
			body = "(no output, exit code 0)"
		} else {
			body = "(no output)"
		}
	}

	var notices []string
	wallTimeNoticeEmitted := wallTimeMinMS > 0 && wallTimeMS >= wallTimeMinMS
	if wallTimeNoticeEmitted {
		notices = append(notices, formatWallTimeNotice(wallTimeMS))
	}
	for _, n := range extraNotices {
		if n != "" {
			notices = append(notices, n)
		}
	}

	// Always separate notices from body with a blank line (`outputLines.push("", ...notices)`,
	// matching tools/bash.ts:1317).
	outputLines := []string{body}
	if len(notices) > 0 {
		outputLines = append(outputLines, "")
		outputLines = append(outputLines, notices...)
	}
	if failedExit {
		outputLines = append(outputLines, "", formatExitCodeNotice(*res.ExitCode))
	}

	details := map[string]any{
		"wallTimeNoticeEmitted": wallTimeNoticeEmitted,
		"wallTimeMs":            wallTimeMS,
	}
	if timeoutSec == nil {
		details["timeoutDisabled"] = true
	} else {
		details["timeoutSeconds"] = timeoutDetailValue(*timeoutSec)
		if requestedTimeoutSec != *timeoutSec {
			details["requestedTimeoutSeconds"] = timeoutDetailValue(requestedTimeoutSec)
		}
	}
	if failedExit {
		details["exitCode"] = *res.ExitCode
	}
	if meta := buildOutputMeta(res.OutputSummary); meta != nil {
		details["meta"] = meta
	}

	if res.TimedOut {
		details["timedOut"] = true
		msg := "Command timed out"
		if timeoutSec != nil {
			msg = fmt.Sprintf("Command timed out after %s seconds", formatSecondsLabel(*timeoutSec))
		}
		if body != "["+msg+"]" && !strings.HasPrefix(body, "["+msg+"]\n") {
			outputLines = append(outputLines, "", "["+msg+"]")
		}
		capped := enforceInlineByteCap(strings.Join(outputLines, "\n"), inlineByteCap, res.ArtifactURL)
		text := capped + formatSummaryNotice(res.OutputSummary)
		return sdk.ToolResult{
			Content: text,
			Details: details,
			IsError: true,
		}, nil
	}

	if res.ExitCode == nil {
		captureNotice := ""
		if res.ArtifactError != "" {
			captureNotice = "\n\n[" + formatArtifactErrorNotice(res.ArtifactError) + "]"
		}
		return sdk.ToolResult{}, sdk.NewToolError(strings.Join(outputLines, "\n") + "\n\nCommand failed: missing exit status" + captureNotice)
	}

	capped := enforceInlineByteCap(strings.Join(outputLines, "\n"), inlineByteCap, res.ArtifactURL)
	text := capped + formatSummaryNotice(res.OutputSummary)
	return sdk.ToolResult{
		Content: text,
		Details: details,
		IsError: failedExit,
	}, nil
}

// buildBackgroundStartResult formats omp's background-start tool result.
// upstream: packages/coding-agent/src/tools/bash.ts:1449
func buildBackgroundStartResult(
	jobID string,
	previewText string,
	timeoutSec *float64,
	requestedTimeoutSec float64,
	notices []string,
) sdk.ToolResult {
	details := map[string]any{
		"async": map[string]any{
			"state": "running",
			"jobId": jobID,
			"type":  "bash",
		},
	}
	var noticeTimeoutSec *int
	if timeoutSec == nil {
		details["timeoutDisabled"] = true
	} else {
		details["timeoutSeconds"] = timeoutDetailValue(*timeoutSec)
		if requestedTimeoutSec != *timeoutSec {
			details["requestedTimeoutSeconds"] = timeoutDetailValue(requestedTimeoutSec)
		}
		rounded := int(math.Round(*timeoutSec))
		noticeTimeoutSec = &rounded
	}
	var lines []string
	trimmedPreview := strings.TrimRight(previewText, " \t\r\n")
	if trimmedPreview != "" {
		lines = append(lines, trimmedPreview, "")
	}
	if len(notices) > 0 {
		lines = append(lines, notices...)
		lines = append(lines, "")
	}
	lines = append(lines, formatBackgroundNotice(jobID, noticeTimeoutSec))
	return sdk.ToolResult{
		Content: strings.Join(lines, "\n"),
		Details: details,
	}
}

// FormatAsyncResultMessage renders omp's `<system-notice>` follow-up wrapper
// for a single completed background job.
// upstream: packages/coding-agent/src/prompts/tools/async-result.md
func FormatAsyncResultMessage(jobID, result string) string {
	return jobs.FormatAsyncResultMessage(jobID, "", result)
}

type serviceExecParams struct {
	sessionKey      string
	sessionFile     string
	toolCallID      string
	name            string
	command         string
	cwd             string
	pty             bool
	ready           *ServiceReady
	notify          *NotifyPattern
	notifyNotice    string
	notices         []string
	preamble        string
	preambleRelease func()
	spillThreshold  int
	headBytes       int
	tailBytes       int
	maxColumns      int
	direnvDiff      *DirenvExportDiff
}

// executeService starts a named long-lived service (replacing any existing
// service of the same name in the same session, matching `replace: true` in
// launch/services.ts:383), registers it as a `jobs.KindService` job via
// `jobs.Add`, wires stdin (PTY with CR or pipe with LF), optional `notify`
// regex watching, and waits for its readiness probe (multiline log regex
// and/or TCP port). Unexpected exits deliver omp's `launch-completion` notice.
// upstream: packages/coding-agent/src/tools/bash.ts:1977, launch/services.ts:317
func (t *BashTool) executeService(ctx sdk.Context, p serviceExecParams) (any, error) {
	releaseOwned := true
	defer func() {
		if releaseOwned && p.preambleRelease != nil {
			p.preambleRelease()
		}
	}()
	if p.ready != nil && p.ready.Port != nil {
		if *p.ready.Port < 1 || *p.ready.Port > 65535 {
			return nil, sdk.NewToolError("ready.port must be an integer from 1 to 65535")
		}
	}
	var logRe *regexp.Regexp
	if p.ready != nil && p.ready.Log != "" {
		var err error
		logRe, err = compileReadyRegex(p.ready.Log)
		if err != nil {
			return nil, sdk.NewToolError(fmt.Sprintf("Invalid readiness regex: %v", err))
		}
	}

	// Replace any existing service with the same name in THIS session
	// (upstream: launch/services.ts:383 `replace: true`).
	t.runtime.Services.mu.Lock()
	existing := t.runtime.Services.services[p.sessionKey][p.name]
	t.runtime.Services.mu.Unlock()
	if existing != nil {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, _ = stopManagedService(stopCtx, existing)
		stopCancel()
		existing.cleanupLogFile()
	}

	shellPath, shellArgs, err := defaultShellConfig()
	if err != nil {
		return nil, sdk.NewToolError(err.Error())
	}

	// Seed service from read-only session state and CommandPreamble.
	stateLoad := ""
	cleanupSeed := func() {}
	if t.cfg.PersistentState {
		stateLoad, cleanupSeed = t.runtime.sessionState(p.sessionKey).snapshotReadOnly()
	}
	preamblePath, cleanupPreamble, _ := writePreambleTempFile(p.preamble)

	runCommand := p.command
	if isBashShell(shellPath) && (stateLoad != "" || preamblePath != "") {
		runCommand = wrapCommandWithState(p.command, stateLoad, "", preamblePath, "", false, 0)
	}
	svcCtx, svcCancel := context.WithCancel(context.Background())
	logNotify := make(chan struct{}, 1)

	var notifier *OutputNotifier
	if p.notify != nil {
		refillFalse := false
		notifier = NewOutputNotifier(p.notify, func(batch NotifyBatch) {
			running := false
			if j, ok := jobs.Get(p.sessionKey, p.name); ok {
				running = j.Status() == "running"
			}
			_ = jobs.DeliverNotify(p.sessionKey, []jobs.NotifyReport{{
				ID:      p.name,
				Type:    "service",
				Pattern: p.notify.Source,
				Lines:   batch.Lines,
				Omitted: batch.Omitted,
				Running: running,
				Stopped: batch.Stopped,
			}})
		}, OutputNotifierOptions{Refill: &refillFalse})
	}

	var logFile *os.File
	var logPath string
	if lf, lfErr := os.CreateTemp("", fmt.Sprintf("omp-service-%s-*.log", p.name)); lfErr == nil {
		_ = lf.Chmod(0o600)
		logFile = lf
		logPath = lf.Name()
	}

	// Construct svc before sink so OnChunk writes through svc.writeLogChunk
	// (bounded at DefaultServiceLogMaxBytes).
	initialState := "running"
	if p.ready != nil && (p.ready.Log != "" || (p.ready.Port != nil && *p.ready.Port > 0)) {
		initialState = "starting"
	}
	svc := &managedService{
		name:      p.name,
		sessionID: p.sessionKey,
		command:   p.command,
		cwd:       p.cwd,
		state:     initialState,
		mode:      "session",
		startTime: time.Now(),
		cancel:    svcCancel,
		done:      make(chan struct{}),
		logFile:   logFile,
		logPath:   logPath,
		logNotify: logNotify,
		notifier:  notifier,
	}

	sink := NewOutputSink(OutputSinkOptions{
		SpillThreshold: p.spillThreshold,
		HeadBytes:      p.headBytes,
		TailBytes:      p.tailBytes,
		MaxColumns:     p.maxColumns,
		ArtifactStore:  t.runtime.Artifacts,
		SessionID:      p.sessionKey,
		SDKContext:     ctx,
		OnChunk: func(chunk string) {
			svc.writeLogChunk(chunk)
			if notifier != nil {
				notifier.Push(chunk)
			}
		},
		OnPreview: func(string) {
			select {
			case logNotify <- struct{}{}:
			default:
			}
		},
	})
	svc.sink = sink

	args := append(append([]string(nil), shellArgs...), runCommand)
	cmd := exec.Command(shellPath, args...)
	cmd.Dir = p.cwd

	var (
		reader    *os.File
		childSide *os.File
		stdinW    io.WriteCloser
		usedPTY   bool
	)
	if p.pty && ptySupported && os.Getenv("PI_NO_PTY") != "1" {
		if ptmx, pts, ptyErr := openPTY(120, 40); ptyErr == nil {
			reader = ptmx
			childSide = pts
			stdinW = ptmx
			usedPTY = true
			configurePTYCommand(cmd, pts)
		}
	}
	if !usedPTY {
		configureProcessGroup(cmd)
		pr, pw, pipeErr := os.Pipe()
		if pipeErr != nil {
			svcCancel()
			cleanupPreamble()
			cleanupSeed()
			sink.Dispose()
			svc.cleanupLogFile()
			return nil, sdk.NewToolError(pipeErr.Error())
		}
		inR, inW, inErr := os.Pipe()
		if inErr != nil {
			_ = pr.Close()
			_ = pw.Close()
			svcCancel()
			cleanupPreamble()
			cleanupSeed()
			sink.Dispose()
			svc.cleanupLogFile()
			return nil, sdk.NewToolError(inErr.Error())
		}
		reader = pr
		childSide = pw
		stdinW = inW
		cmd.Stdin = inR
		cmd.Stdout = pw
		cmd.Stderr = pw
		defer func() { _ = inR.Close() }()
	}
	cmd.Env = buildCommandEnvWithDirenv(p.sessionKey, p.sessionFile, p.toolCallID, usedPTY, p.direnvDiff)

	if startErr := cmd.Start(); startErr != nil {
		_ = reader.Close()
		_ = childSide.Close()
		if !usedPTY && stdinW != nil {
			_ = stdinW.Close()
		}
		svcCancel()
		cleanupPreamble()
		cleanupSeed()
		sink.Dispose()
		svc.cleanupLogFile()
		return nil, sdk.NewToolError(startErr.Error())
	}
	_ = childSide.Close()

	svc.pid = cmd.Process.Pid
	attachProcessGroup(svc.pid)
	svc.usedPTY = usedPTY
	svc.stdin = stdinW

	t.runtime.Services.mu.Lock()
	if t.runtime.Services.services[p.sessionKey] == nil {
		t.runtime.Services.services[p.sessionKey] = make(map[string]*managedService)
	}
	t.runtime.Services.services[p.sessionKey][p.name] = svc
	t.runtime.Services.mu.Unlock()

	jobs.EnsureDeliveryListener()
	jobs.Add(p.sessionKey, svc)

	releaseOwned = false // service goroutine owns preambleRelease until service exits
	go func() {
		defer close(svc.done)
		defer func() {
			if p.preambleRelease != nil {
				p.preambleRelease()
			}
		}()
		defer cleanupPreamble()
		defer cleanupSeed()
		var acceptMu sync.Mutex
		acceptingChunks := true
		readDone := make(chan struct{})
		go func() {
			defer close(readDone)
			buf := make([]byte, 16*1024)
			for {
				n, rErr := reader.Read(buf)
				if n > 0 {
					acceptMu.Lock()
					ok := acceptingChunks
					acceptMu.Unlock()
					if ok {
						sink.Push(string(buf[:n]))
					}
				}
				if rErr != nil {
					return
				}
			}
		}()

		waitDone := make(chan error, 1)
		go func() {
			waitDone <- cmd.Wait()
		}()

		var waitErr error
		select {
		case waitErr = <-waitDone:
		case <-svcCtx.Done():
			// Stop accepting chunks BEFORE signalling the service process group
			// so shell termination messages caused by our own kill cannot leak
			// into captured service output.
			acceptMu.Lock()
			acceptingChunks = false
			acceptMu.Unlock()
			done := make(chan struct{})
			go func() {
				<-waitDone
				close(done)
			}()
			killProcessTreeGraceful(cmd.Process, done, termCleanupGrace)
			<-done
		}
		select {
		case <-readDone:
			_ = reader.Close()
		case <-time.After(250 * time.Millisecond):
			_ = reader.Close()
			<-readDone
		}
		if !usedPTY && stdinW != nil {
			_ = stdinW.Close()
		}

		var code int
		if waitErr != nil {
			var exitErr *exec.ExitError
			if errors.As(waitErr, &exitErr) {
				code = processExitCode(exitErr.ProcessState)
			}
		} else {
			code = processExitCode(cmd.ProcessState)
		}

		svc.mu.Lock()
		svc.endTime = time.Now()
		svc.exitCode = &code
		wasStopped := svc.stopRequested || svc.state == "stopped"
		if !wasStopped {
			if code != 0 {
				svc.state = "failed"
			} else {
				svc.state = "exited"
			}
			svc.exitReason = fmt.Sprintf("exited with code %d", code)
		}
		finalState := svc.state
		svc.stdin = nil
		if svc.logFile != nil {
			_ = svc.logFile.Close()
			svc.logFile = nil
		}
		svc.mu.Unlock()

		if notifier != nil {
			notifier.Finish()
		}
		sink.Dispose()

		// Notify the session when a supervised service exits on its own
		// (matching omp's launch-completion follow-up).
		if !wasStopped {
			_ = jobs.DeliverServiceExit(p.sessionKey, p.name, finalState, &code, procServiceSnapshotMap(svc))
		}
	}()

	readyTimedOut := false
	hasReadyProbe := p.ready != nil && (logRe != nil || (p.ready.Port != nil && *p.ready.Port > 0))
	effectiveReadyTimeoutSec := float64(DefaultServiceReadyTimeoutSec)
	if hasReadyProbe {
		if p.ready.Timeout != nil {
			effectiveReadyTimeoutSec = math.Max(MinServiceReadyTimeoutSec, math.Min(MaxServiceReadyTimeoutSec, *p.ready.Timeout))
		}
		deadline := time.NewTimer(time.Duration(effectiveReadyTimeoutSec * float64(time.Second)))
		defer deadline.Stop()
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()

		checkReady := func() bool {
			if logRe != nil {
				if !logRe.MatchString(sink.Preview()) {
					return false
				}
			}
			if p.ready.Port != nil && *p.ready.Port > 0 {
				if !probeReadyPort(p.ready.Host, *p.ready.Port) {
					return false
				}
			}
			return true
		}

		for !checkReady() {
			select {
			case <-svc.done:
				goto settled
			case <-ctx.Done():
				return nil, sdk.NewToolError("Command aborted")
			case <-deadline.C:
				readyTimedOut = true
				goto settled
			case <-logNotify:
			case <-ticker.C:
			}
		}
		svc.mu.Lock()
		if svc.state == "starting" {
			// upstream: daemon/units.ts:1927 (ready probe satisfied => "ready")
			svc.state = "ready"
			svc.readyAt = time.Now()
		}
		svc.mu.Unlock()
	}

settled:
	snap := svc.snapshot()
	header := fmt.Sprintf("%s: %s", snap.Name, snap.State)
	if snap.PID > 0 {
		header += fmt.Sprintf(" pid=%d", snap.PID)
	}
	if snap.Ready {
		header += " ready"
	}
	lines := []string{header}
	if readyTimedOut {
		lines = append(lines, fmt.Sprintf("NOT ready — readiness timed out after %ss; process state: %s.", formatSecondsLabel(effectiveReadyTimeoutSec), snap.State))
	}
	if snap.ExitReason != "" {
		lines = append(lines, "Reason: "+snap.ExitReason)
	}
	lines = append(lines, p.notices...)
	if p.notifyNotice != "" {
		lines = append(lines, p.notifyNotice)
	}
	if logTail := strings.TrimRight(snap.Logs, "\n"); logTail != "" {
		lines = append(lines, logTail)
	}
	svcDetails := map[string]any{
		"name":     snap.Name,
		"state":    snap.State,
		"ready":    snap.Ready || snap.State == "running" || snap.State == "ready",
		"timedOut": readyTimedOut,
	}
	if snap.PID > 0 {
		svcDetails["pid"] = snap.PID
	}
	return sdk.ToolResult{
		Content: strings.Join(lines, "\n"),
		Details: map[string]any{
			"service": svcDetails,
		},
	}, nil
}

// onceFunc wraps fn so it executes at most once and safely handles nil.
func onceFunc(fn func()) func() {
	if fn == nil {
		return func() {}
	}
	var once sync.Once
	return func() { once.Do(fn) }
}

// shellCommandPrefixWords mirrors SHELL_COMMAND_PREFIX_WORDS (tools/bash.ts:692).
var shellCommandPrefixWords = map[string]bool{
	"!":     true,
	"{":     true,
	"if":    true,
	"then":  true,
	"elif":  true,
	"else":  true,
	"do":    true,
	"while": true,
	"until": true,
	"time":  true,
}

// simpleCommandName mirrors simpleCommandName (tools/bash.ts:706).
func simpleCommandName(words []string) string {
	for _, w := range words {
		if !shellEnvAssignRE.MatchString(w) && !shellCommandPrefixWords[w] {
			return w
		}
	}
	return ""
}

// invokedToolCommand returns the first mounted/special tool command invoked by
// any simple command in `command` (tools/bash.ts:711).
func invokedToolCommand(command string, names map[string]bool) string {
	if len(names) == 0 {
		return ""
	}
	for _, words := range tokenizeShellSegments(command) {
		if name := simpleCommandName(words); name != "" && names[name] {
			return name
		}
	}
	return ""
}

// startsWithToolCommand reports whether the first simple command of `command`
// is a session tool command (tools/bash.ts:721).
func startsWithToolCommand(command string, names map[string]bool) bool {
	if len(names) == 0 {
		return false
	}
	segs := tokenizeShellSegments(command)
	if len(segs) == 0 || len(segs[0]) == 0 {
		return false
	}
	return names[segs[0][0]]
}

// detectInvokedToolCommand checks whether rawCommand or hoisted command invokes
// a session tool command, preferring the integrator-wired InvokedCommand hook
// (which shares toolcmd's exact TryDirect parser) and falling back to
// CommandNames segment scanning when InvokedCommand is nil.
func detectInvokedToolCommand(ctx sdk.Context, rawCommand, command string, toolCmdSet map[string]bool) string {
	candidates := []string{command}
	if rawCommand != "" && rawCommand != command {
		candidates = append([]string{rawCommand}, candidates...)
	}
	if InvokedCommand != nil {
		for _, c := range candidates {
			if name, ok := InvokedCommand(ctx, c); ok && name != "" {
				return name
			}
		}
	}
	for _, c := range candidates {
		if name := invokedToolCommand(c, toolCmdSet); name != "" {
			return name
		}
	}
	return ""
}

// stampToolCommands attaches `details.toolCommands` (and `details.toolCommandsSole`
// when the call was one sole executed tool command) when the integrator hook
// reports dispatches for this bash call.
// upstream: packages/coding-agent/src/tools/bash.ts:1345, packages/tui/src/tools/bash.ts:596-632
func stampToolCommands(ctx sdk.Context, command string, tr sdk.ToolResult) sdk.ToolResult {
	if ToolCommandDispatches == nil {
		return tr
	}
	dispatches := ToolCommandDispatches(ctx)
	if len(dispatches) == 0 {
		return tr
	}
	det, _ := tr.Details.(map[string]any)
	if det == nil {
		det = map[string]any{}
	}
	det["toolCommands"] = dispatches
	if len(dispatches) == 1 {
		mode, _ := dispatches[0]["mode"].(string)
		toolName, _ := dispatches[0]["tool"].(string)
		if mode == "execute" && isSoleToolCommandInvocation(command, toolName) {
			det["toolCommandsSole"] = true
		}
	}
	tr.Details = det
	return tr
}

// isSoleToolCommandInvocation ports packages/tui/src/tools/bash.ts:596-619.
func isSoleToolCommandInvocation(command, tool string) bool {
	line := strings.TrimSpace(command)
	if line == "" || tool == "" {
		return false
	}
	if line != tool && !strings.HasPrefix(line, tool+" ") && !strings.HasPrefix(line, tool+"\t") {
		return false
	}
	var quote byte
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if quote == '\'' {
			if ch == '\'' {
				quote = 0
			}
			continue
		}
		if ch == '\\' {
			i++
			continue
		}
		if quote == '"' {
			if ch == '"' {
				quote = 0
			} else if ch == '$' || ch == '`' {
				return false
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			continue
		}
		switch ch {
		case '|', '&', ';', '<', '>', '(', ')', '$', '`', '\n':
			return false
		}
	}
	return true
}

// probeReadyPort checks TCP connectivity; when host is empty or "localhost", it
// probes both 127.0.0.1 and ::1 matching omp's v19.1.1 default.
func probeReadyPort(host string, port int) bool {
	portStr := strconv.Itoa(port)
	var hosts []string
	if host == "" || strings.EqualFold(host, "localhost") {
		hosts = []string{"127.0.0.1", "::1"}
	} else {
		hosts = []string{host}
	}
	for _, h := range hosts {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(h, portStr), 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return true
		}
	}
	return false
}

// strictSessionID returns the session ID from ctx, or "" if unavailable.
// Never synthesizes a shared "default" ID so lifecycle cleanup never kills
// another session's work.
func strictSessionID(ctx sdk.Context) string {
	var zero sdk.Context
	if ctx == zero {
		return ""
	}
	if id, err := ctx.GetSessionID(); err == nil && id != "" {
		return id
	}
	return ""
}

func sessionKeyFromContext(ctx sdk.Context) string {
	if sid := strictSessionID(ctx); sid != "" {
		return sid
	}
	var zero sdk.Context
	if ctx != zero {
		if cwd := ctx.Cwd(); cwd != "" {
			return cwd
		}
	}
	return "default"
}

// shellProjectTrusted checks ctx.IsProjectTrusted fail-closed (matches lsp/debug trust gating).
func shellProjectTrusted(ctx sdk.Context) (trusted bool) {
	var zero sdk.Context
	if ctx == zero {
		return false
	}
	defer func() {
		if recover() != nil {
			trusted = false
		}
	}()
	ok, err := ctx.IsProjectTrusted()
	return err == nil && ok
}
