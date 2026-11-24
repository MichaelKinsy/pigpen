// Package shell ports omp's `bash` tool (foreground, async/auto-background jobs,
// named services, output truncation with artifact spill, process-group teardown).
package shell

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// upstream: packages/coding-agent/src/tools/bash.ts, packages/coding-agent/src/prompts/tools/bash.md

//go:embed bash.md
var bashDescription string

const (
	// DefaultTimeoutSec is omp's TOOL_TIMEOUTS.bash.default (300s).
	DefaultTimeoutSec = 300
	// MinTimeoutSec is omp's TOOL_TIMEOUTS.bash.min (1s).
	MinTimeoutSec = 1
	// MaxTimeoutSec is omp's TOOL_TIMEOUTS.bash.max (3600s).
	MaxTimeoutSec = 3600
	// DefaultServiceReadyTimeoutSec is omp's ready.timeout default (30s).
	DefaultServiceReadyTimeoutSec = 30
	// sessionShellBusyNotice matches omp's SESSION_SHELL_BUSY_NOTICE (bash.ts:448).
	sessionShellBusyNotice = "Ran in a temporary shell while the session shell is busy with another command: session variables and functions were unavailable, and nothing set here persists."
)

// Config controls runtime behavior of a BashTool instance. Zero values take
// omp's defaults.
type Config struct {
	AutoBackgroundEnabled   bool
	AutoBackgroundThreshold time.Duration
	AsyncEnabled            bool
	LaunchEnabled           bool
	MaxTimeoutSec           int
	SpillThreshold          int
	HeadBytes               int
	MaxColumns              int
	InterceptorEnabled      bool
	InterceptorRules        []InterceptorRule
	AvailableTools          []string
	// PersistentState, when true, persists shell variables, functions, aliases,
	// and shell options across calls per session while resetting cwd on every
	// call to the explicit `cwd` parameter or the session directory.
	PersistentState bool
	ArtifactDir     string
}

// DefaultConfig returns omp's default bash settings.
func DefaultConfig() Config {
	return Config{
		AutoBackgroundEnabled:   true,
		AutoBackgroundThreshold: DefaultAutoBackgroundThreshold,
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

// Runtime holds the process-wide registries exposed to a future `wait` tool and
// `proc://` / `artifact://` readers.
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

// DisposeSessions cleans up temporary shell state snapshot files.
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
}

// defaultRuntime is the package-level runtime wired by Register so sibling
// packages (`wait`, `proc://`, `artifact://`) can inspect live jobs, services,
// and spilled artifacts without holding an extension handle.
var defaultRuntime = NewRuntime("")

// DefaultRuntime returns the package-level Runtime registered with the extension.
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

// Register installs omp's `bash` tool and session shutdown cleanup on e.
// It performs no I/O and never panics.
func Register(e *sdk.Extension) {
	tool := NewBashTool(DefaultConfig(), defaultRuntime)
	e.RegisterTool(tool.Definition())
	e.OnSessionShutdown(func(ctx sdk.Context, _ map[string]any) (any, error) {
		disposeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = defaultRuntime.Jobs.Dispose(disposeCtx)
		_ = defaultRuntime.Services.Dispose(disposeCtx)
		defaultRuntime.DisposeSessions()
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

// Parameters returns omp's JSON Schema for the `bash` tool
// (bashSchemaWithAsyncAndService, upstream bash.ts:358).
func bashParametersSchema() sdk.Schema {
	timeoutDesc := fmt.Sprintf(
		"timeout in seconds; 0 disables the command deadline; nonzero values are clamped to %d-%d",
		MinTimeoutSec, MaxTimeoutSec,
	)
	readyProps := map[string]any{
		"log":     map[string]any{"type": "string"},
		"port":    map[string]any{"type": "number"},
		"host":    map[string]any{"type": "string"},
		"timeout": map[string]any{"type": "number"},
	}
	return sdk.Schema{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{"type": "string"},
			"timeout": map[string]any{"type": "number", "description": timeoutDesc},
			"cwd":     map[string]any{"type": "string"},
			"pty":     map[string]any{"type": "boolean"},
			"async":   map[string]any{"type": "boolean"},
			"name":    map[string]any{"type": "string", "maxLength": 48},
			"ready": map[string]any{
				"type":                 "object",
				"properties":           readyProps,
				"additionalProperties": false,
			},
		},
		"required":             []string{"command"},
		"additionalProperties": false,
	}
}

// Definition returns the sdk.ToolDefinition for omp's `bash` tool.
func (t *BashTool) Definition() sdk.ToolDefinition {
	return sdk.ToolDefinition{
		Name:          "bash",
		Label:         "Bash",
		Description:   strings.TrimSpace(bashDescription),
		Parameters:    bashParametersSchema(),
		ExecutionMode: "parallel",
		Execute:       t.Execute,
	}
}

type bashInput struct {
	Command    string
	HasTimeout bool
	Timeout    float64
	Cwd        string
	Name       string
	Ready      *ServiceReady
	Async      bool
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
	if b, ok := params["pty"].(bool); ok {
		in.Pty = b
	}
	if rawReady, ok := params["ready"].(map[string]any); ok {
		in.Ready = normalizeReady(rawReady)
	}
	return in, nil
}

// normalizeReady drops placeholder/empty readiness fields, returning nil when
// none survive. upstream: bash.ts:406
func normalizeReady(m map[string]any) *ServiceReady {
	if len(m) == 0 {
		return nil
	}
	var r ServiceReady
	hasAny := false
	if s, ok := m["log"].(string); ok {
		if trimmed := strings.TrimSpace(s); trimmed != "" {
			r.Log = trimmed
			hasAny = true
		}
	}
	if s, ok := m["host"].(string); ok {
		if trimmed := strings.TrimSpace(s); trimmed != "" {
			r.Host = trimmed
			hasAny = true
		}
	}
	if v, ok := m["port"].(float64); ok && !math.IsNaN(v) && !math.IsInf(v, 0) {
		p := int(v)
		r.Port = &p
		hasAny = true
	} else if v, ok := m["port"].(int); ok {
		p := v
		r.Port = &p
		hasAny = true
	}
	if v, ok := m["timeout"].(float64); ok && !math.IsNaN(v) && !math.IsInf(v, 0) {
		t := v
		r.Timeout = &t
		hasAny = true
	} else if v, ok := m["timeout"].(int); ok {
		t := float64(v)
		r.Timeout = &t
		hasAny = true
	}
	if !hasAny {
		return nil
	}
	return &r
}

// clampTimeout mirrors packages/coding-agent/src/tools/tool-timeouts.ts:34.
func clampTimeout(requestedSec int, maxTimeoutSec int) int {
	t := requestedSec
	if maxTimeoutSec > 0 && t > maxTimeoutSec {
		t = maxTimeoutSec
	}
	if t < MinTimeoutSec {
		t = MinTimeoutSec
	}
	if t > MaxTimeoutSec {
		t = MaxTimeoutSec
	}
	return t
}

func formatTimeoutClampNotice(requestedSec, effectiveSec, maxTimeoutSec int) string {
	if requestedSec == effectiveSec {
		return ""
	}
	limit := fmt.Sprintf("allowed range %d-%ds", MinTimeoutSec, MaxTimeoutSec)
	if maxTimeoutSec > 0 && effectiveSec == maxTimeoutSec && maxTimeoutSec < MaxTimeoutSec {
		limit = fmt.Sprintf("global tools.maxTimeout ceiling %ds", maxTimeoutSec)
	}
	return fmt.Sprintf("Timeout clamped to %ds (requested %ds; %s).", effectiveSec, requestedSec, limit)
}

// Execute runs omp's `bash` tool flow.
// upstream: packages/coding-agent/src/tools/bash.ts:963
func (t *BashTool) Execute(ctx sdk.Context, params map[string]any) (any, error) {
	in, err := parseBashInput(params)
	if err != nil {
		return nil, err
	}

	rawCommand := in.Command
	command := rawCommand
	cwdParam := in.Cwd

	// Extract a leading `cd <path> && ...` into cwd when the model ignores cwd.
	// upstream: bash.ts:988
	if cwdParam == "" {
		if cdPath, rest, ok := extractLeadingCdTarget(command); ok {
			cwdParam = cdPath
			command = rest
		}
	}

	var pendingNotices []string

	// Mode validation (upstream: bash.ts:1004-1016).
	if in.Name != "" {
		if !t.cfg.LaunchEnabled {
			return nil, sdk.NewToolError("Service launch is disabled in this session.")
		}
		if in.Async || in.HasTimeout {
			return nil, sdk.NewToolError("Service mode does not accept async or timeout; use ready.timeout for readiness.")
		}
	} else if in.Ready != nil && (in.Ready.Log != "" || (in.Ready.Port != nil && *in.Ready.Port != 0)) {
		pendingNotices = append(pendingNotices, "Ignored ready: service-only, and no service name was given.")
	}
	if in.Async && !t.cfg.AsyncEnabled {
		return nil, sdk.NewToolError("Async bash execution is disabled. Enable async.enabled to use async mode.")
	}

	// Bash interceptor check on both rawCommand and cwd-normalized command.
	// upstream: bash.ts:1021
	if t.cfg.InterceptorEnabled {
		rules := t.cfg.InterceptorRules
		if in.Name != "" {
			filtered := make([]InterceptorRule, 0, len(rules))
			for _, r := range rules {
				if r.Tool != "bash" {
					filtered = append(filtered, r)
				}
			}
			rules = filtered
		}
		toCheck := []string{command}
		if rawCommand != command {
			toCheck = []string{rawCommand, command}
		}
		for _, candidate := range toCheck {
			res := CheckBashInterception(candidate, t.cfg.AvailableTools, rules, rawCommand)
			if res.Block {
				return nil, sdk.NewToolError(res.Message)
			}
		}
	}
	// Resolve session working directory.
	var zeroCtx sdk.Context
	hasHostCtx := ctx != zeroCtx
	sessionKey := "default"
	hostCwd := ""
	if hasHostCtx {
		sessionKey = sessionKeyFromContext(ctx)
		hostCwd = ctx.Cwd()
	}
	if hostCwd == "" {
		if wd, wdErr := os.Getwd(); wdErr == nil {
			hostCwd = wd
		}
	}
	// Each call starts in the explicit `cwd` parameter if given, else the
	// session working directory. Working directory does NOT persist across calls.
	commandCwd := hostCwd
	if cwdParam != "" {
		commandCwd = resolveToCwd(cwdParam, hostCwd)
	}
	// Validate working directory exists and is a directory (upstream: bash.ts:1073).
	info, statErr := os.Stat(commandCwd)
	if statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return nil, sdk.NewToolError(fmt.Sprintf("Working directory does not exist: %s", commandCwd))
		}
		return nil, sdk.NewToolError(fmt.Sprintf("Working directory error: %v", statErr))
	}
	if !info.IsDir() {
		return nil, sdk.NewToolError(fmt.Sprintf("Working directory is not a directory: %s", commandCwd))
	}

	// Named service mode (upstream: bash.ts:1087).
	if in.Name != "" {
		return t.executeService(ctx, in.Name, command, commandCwd, in.Ready)
	}

	// Timeout resolution (upstream: bash.ts:1142).
	requestedTimeoutSec := DefaultTimeoutSec
	if in.HasTimeout {
		requestedTimeoutSec = int(math.Round(in.Timeout))
	}
	timeoutDisabled := in.HasTimeout && in.Timeout == 0
	var timeoutSec *int
	var timeoutDur *time.Duration
	if !timeoutDisabled {
		eff := clampTimeout(requestedTimeoutSec, t.cfg.MaxTimeoutSec)
		timeoutSec = &eff
		d := time.Duration(eff) * time.Second
		timeoutDur = &d
		if notice := formatTimeoutClampNotice(requestedTimeoutSec, eff, t.cfg.MaxTimeoutSec); notice != "" {
			pendingNotices = append(pendingNotices, notice)
		}
	}

	// Ensure the job manager delivers background completions back into the
	// conversation when a host connection is present.
	t.ensureDeliverySink(ctx)

	// Explicit async mode (upstream: bash.ts:1152).
	if in.Async {
		job, _, regErr := t.startManagedJob(ctx, managedJobParams{
			command:             command,
			commandCwd:          commandCwd,
			sessionKey:          sessionKey,
			useSessionState:     false,
			timeoutDur:          timeoutDur,
			timeoutSec:          timeoutSec,
			requestedTimeoutSec: requestedTimeoutSec,
			notices:             pendingNotices,
			foreground:          false,
		})
		if regErr != nil {
			return nil, sdk.NewToolError(regErr.Error())
		}
		return buildBackgroundStartResult(job.id, "", timeoutSec, requestedTimeoutSec, pendingNotices), nil
	}

	// Auto-background mode for non-PTY calls (upstream: bash.ts:1185).
	if t.cfg.AutoBackgroundEnabled && !in.Pty && !t.runtime.Jobs.AtCapacity() {
		threshold := t.cfg.AutoBackgroundThreshold
		if threshold == 0 {
			threshold = DefaultAutoBackgroundThreshold
		}
		waitBudget := resolveAutoBackgroundWait(threshold, timeoutDur)
		startBackgrounded := waitBudget == 0

		job, completionCh, regErr := t.startManagedJob(ctx, managedJobParams{
			command:             command,
			commandCwd:          commandCwd,
			sessionKey:          sessionKey,
			useSessionState:     !startBackgrounded,
			timeoutDur:          timeoutDur,
			timeoutSec:          timeoutSec,
			requestedTimeoutSec: requestedTimeoutSec,
			notices:             pendingNotices,
			foreground:          !startBackgrounded,
		})
		if regErr != nil {
			return nil, sdk.NewToolError(regErr.Error())
		}
		if startBackgrounded {
			return buildBackgroundStartResult(job.id, "", timeoutSec, requestedTimeoutSec, pendingNotices), nil
		}

		// Race job completion against the auto-background threshold and caller abort.
		// Timer is always stopped on return so fast commands never retain a timer.
		timer := time.NewTimer(waitBudget)
		defer timer.Stop()

		select {
		case settled := <-completionCh:
			t.runtime.Jobs.releaseForegroundJob(job.id)
			if settled.err != nil {
				return nil, settled.err
			}
			return settled.result, nil
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
			return buildBackgroundStartResult(job.id, latest, timeoutSec, requestedTimeoutSec, pendingNotices), nil
		}
	}

	// Direct foreground execution (upstream: bash.ts:1548).
	if in.Pty {
		// Wave 1 has no native PTY module; emit omp's exact fallback notice.
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

	var stateLoad, stateSave string
	var releaseState func(string)
	if t.cfg.PersistentState {
		var busy bool
		stateLoad, releaseState, busy = t.runtime.sessionState(sessionKey).acquire()
		if busy {
			pendingNotices = append(pendingNotices, sessionShellBusyNotice)
		} else {
			if tmp, tmpErr := os.CreateTemp("", "omp-shell-save-*"); tmpErr == nil {
				stateSave = tmp.Name()
				_ = tmp.Close()
			}
		}
	}

	start := time.Now()
	res, execErr := executeCommand(runCtx, ExecOptions{
		Command:        command,
		Cwd:            commandCwd,
		Timeout:        timeoutVal,
		SpillThreshold: t.cfg.SpillThreshold,
		HeadBytes:      t.cfg.HeadBytes,
		MaxColumns:     t.cfg.MaxColumns,
		ArtifactStore:  t.runtime.Artifacts,
		StateLoadFile:  stateLoad,
		StateSaveFile:  stateSave,
		OnPreview: func(text string) {
			if hasHostCtx {
				_ = ctx.OnUpdate(map[string]any{
					"content": text,
					"details": map[string]any{},
				})
			}
		},
	})
	wallMS := float64(time.Since(start).Microseconds()) / 1000.0
	if releaseState != nil {
		if execErr == nil && !res.Cancelled && !res.TimedOut {
			releaseState(stateSave)
		} else {
			releaseState("")
			if stateSave != "" {
				_ = os.Remove(stateSave)
			}
		}
	}
	if execErr != nil {
		return nil, sdk.NewToolError(execErr.Error())
	}

	return buildCompletedResult(res, timeoutSec, requestedTimeoutSec, pendingNotices, wallMS)
}

type managedJobParams struct {
	command             string
	commandCwd          string
	sessionKey          string
	useSessionState     bool
	timeoutDur          *time.Duration
	timeoutSec          *int
	requestedTimeoutSec int
	notices             []string
	foreground          bool
}

type managedJobSettlement struct {
	result sdk.ToolResult
	err    error
}

func (t *BashTool) startManagedJob(ctx sdk.Context, p managedJobParams) (*managedJob, <-chan managedJobSettlement, error) {
	label := p.command
	if len(label) > 120 {
		label = label[:117] + "..."
	}
	completionCh := make(chan managedJobSettlement, 1)
	notices := append([]string(nil), p.notices...)

	var timeoutVal time.Duration
	if p.timeoutDur != nil {
		timeoutVal = *p.timeoutDur
	}

	var zero sdk.Context
	hasHostCtx := ctx != zero

	job, err := t.runtime.Jobs.register("bash", label, p.command, p.commandCwd, p.foreground, func(
		runCtx context.Context,
		jobID string,
		reportProgress func(string),
		setPIDs func(func() []int),
	) (string, error) {
		var stateLoad, stateSave string
		var releaseState func(string)
		if t.cfg.PersistentState && p.useSessionState {
			var busy bool
			stateLoad, releaseState, busy = t.runtime.sessionState(p.sessionKey).acquire()
			if busy {
				notices = append(notices, sessionShellBusyNotice)
			} else {
				if tmp, tmpErr := os.CreateTemp("", "omp-shell-save-*"); tmpErr == nil {
					stateSave = tmp.Name()
					_ = tmp.Close()
				}
			}
		}

		start := time.Now()
		res, execErr := executeCommand(runCtx, ExecOptions{
			Command:        p.command,
			Cwd:            p.commandCwd,
			Timeout:        timeoutVal,
			SpillThreshold: t.cfg.SpillThreshold,
			HeadBytes:      t.cfg.HeadBytes,
			MaxColumns:     t.cfg.MaxColumns,
			ArtifactStore:  t.runtime.Artifacts,
			StateLoadFile:  stateLoad,
			StateSaveFile:  stateSave,
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
		wallMS := float64(time.Since(start).Microseconds()) / 1000.0
		if releaseState != nil {
			if execErr == nil && !res.Cancelled && !res.TimedOut {
				releaseState(stateSave)
			} else {
				releaseState("")
				if stateSave != "" {
					_ = os.Remove(stateSave)
				}
			}
		}
		if execErr != nil {
			toolErr := sdk.NewToolError(execErr.Error())
			completionCh <- managedJobSettlement{err: toolErr}
			return "", toolErr
		}

		toolRes, buildErr := buildCompletedResult(res, p.timeoutSec, p.requestedTimeoutSec, notices, wallMS)
		if buildErr != nil {
			completionCh <- managedJobSettlement{err: buildErr}
			return "", buildErr
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
// upstream: packages/coding-agent/src/tools/bash.ts:695
func buildCompletedResult(
	res ExecResult,
	timeoutSec *int,
	requestedTimeoutSec int,
	extraNotices []string,
	wallTimeMS float64,
) (sdk.ToolResult, error) {
	// Non-timeout cancellation propagates as a thrown ToolError (upstream: bash.ts:1594).
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
	wallTimeNoticeEmitted := wallTimeMS >= WallTimeNoticeMinMS
	if wallTimeNoticeEmitted {
		notices = append(notices, formatWallTimeNotice(wallTimeMS))
	}
	for _, n := range extraNotices {
		if n != "" {
			notices = append(notices, n)
		}
	}

	outputLines := []string{body}
	if len(notices) > 0 {
		if res.TimedOut && strings.HasPrefix(body, "[Command timed out") && strings.HasSuffix(body, "\n") {
			// For a timeout with no stdout/stderr, `body` is "[Command timed out after Ns]\n".
			// Join notices with a single newline so the result is:
			// "[Command timed out after Ns]\nWall time: X.XX seconds".
			outputLines = []string{strings.TrimSuffix(body, "\n")}
			outputLines = append(outputLines, notices...)
		} else {
			outputLines = append(outputLines, "")
			outputLines = append(outputLines, notices...)
		}
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
		details["timeoutSeconds"] = *timeoutSec
		if requestedTimeoutSec != *timeoutSec {
			details["requestedTimeoutSeconds"] = requestedTimeoutSec
		}
	}
	if failedExit {
		details["exitCode"] = *res.ExitCode
	}
	if res.Truncated {
		meta := map[string]any{
			"truncation": map[string]any{
				"totalLines":  res.TotalLines,
				"totalBytes":  res.TotalBytes,
				"outputLines": res.OutputLines,
				"outputBytes": res.OutputBytes,
				"artifactId":  res.ArtifactID,
			},
		}
		details["meta"] = meta
	}

	if res.TimedOut {
		details["timedOut"] = true
		msg := "Command timed out"
		if timeoutSec != nil {
			msg = fmt.Sprintf("Command timed out after %d seconds", *timeoutSec)
		}
		if !strings.HasPrefix(res.Output, "["+msg+"]\n") {
			outputLines = append(outputLines, "", "["+msg+"]")
		}
		text := strings.Join(outputLines, "\n") + formatSummaryNotice(res.OutputSummary)
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

	text := strings.Join(outputLines, "\n") + formatSummaryNotice(res.OutputSummary)
	return sdk.ToolResult{
		Content: text,
		Details: details,
		IsError: failedExit,
	}, nil
}

// buildBackgroundStartResult formats omp's background-start tool result.
// upstream: packages/coding-agent/src/tools/bash.ts:790
func buildBackgroundStartResult(
	jobID string,
	previewText string,
	timeoutSec *int,
	requestedTimeoutSec int,
	notices []string,
) sdk.ToolResult {
	details := map[string]any{
		"async": map[string]any{
			"state": "running",
			"jobId": jobID,
			"type":  "bash",
		},
	}
	if timeoutSec == nil {
		details["timeoutDisabled"] = true
	} else {
		details["timeoutSeconds"] = *timeoutSec
		if requestedTimeoutSec != *timeoutSec {
			details["requestedTimeoutSeconds"] = requestedTimeoutSec
		}
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
	lines = append(lines, formatBackgroundNotice(jobID, timeoutSec))
	return sdk.ToolResult{
		Content: strings.Join(lines, "\n"),
		Details: details,
	}
}

// ensureDeliverySink wires a default delivery callback that injects a finished
// background job's output into the conversation as a follow-up custom message
// via sdk.Context.SendMessage (TriggerTurn: true, DeliverAs: "followUp").
// If a custom sink was already installed (e.g. in tests), it is preserved.
func (t *BashTool) ensureDeliverySink(ctx sdk.Context) {
	var zero sdk.Context
	if ctx == zero {
		return
	}
	t.runtime.Jobs.mu.Lock()
	alreadySet := t.runtime.Jobs.onComplete != nil
	t.runtime.Jobs.mu.Unlock()
	if alreadySet {
		return
	}
	t.runtime.Jobs.SetDeliverySink(func(jobID string, text string, snap JobSnapshot) {
		trigger := true
		msg := FormatAsyncResultMessage(jobID, text)
		_ = ctx.SendMessage("async-result", msg, true, sdk.SendMessageOptions{
			TriggerTurn: &trigger,
			DeliverAs:   "followUp",
			Details: map[string]any{
				"jobs": []map[string]any{
					{
						"jobId":  jobID,
						"type":   snap.Type,
						"status": string(snap.Status),
						"label":  snap.Label,
					},
				},
			},
		})
	})
}

// FormatAsyncResultMessage renders omp's `<system-notice>` follow-up wrapper
// for a single completed background job.
// upstream: packages/coding-agent/src/prompts/tools/async-result.md
func FormatAsyncResultMessage(jobID, result string) string {
	return fmt.Sprintf("<system-notice>\nBackground job %s has completed. Resume your work using the result below.\n%s\n</system-notice>", jobID, result)
}

// executeService starts a named long-lived service, optionally waiting for its
// readiness probe (log regex and/or TCP port).
// upstream: packages/coding-agent/src/tools/bash.ts:1087, launch/services.ts
func (t *BashTool) executeService(ctx sdk.Context, name, command, cwd string, ready *ServiceReady) (any, error) {
	var logRe *regexp.Regexp
	if ready != nil && ready.Log != "" {
		var err error
		logRe, err = compileReadyRegex(ready.Log)
		if err != nil {
			return nil, sdk.NewToolError(fmt.Sprintf("Invalid ready.log regex: %v", err))
		}
	}

	t.runtime.Services.mu.Lock()
	if existing, ok := t.runtime.Services.services[name]; ok {
		existing.mu.Lock()
		active := existing.state == "starting" || existing.state == "running"
		existing.mu.Unlock()
		if active {
			t.runtime.Services.mu.Unlock()
			return nil, sdk.NewToolError(fmt.Sprintf("Service %q is already running.", name))
		}
	}

	shellPath, shellArgs, err := defaultShellConfig()
	if err != nil {
		t.runtime.Services.mu.Unlock()
		return nil, sdk.NewToolError(err.Error())
	}

	svcCtx, svcCancel := context.WithCancel(context.Background())
	logNotify := make(chan struct{}, 1)
	sink := NewOutputSink(OutputSinkOptions{
		SpillThreshold: t.cfg.SpillThreshold,
		HeadBytes:      t.cfg.HeadBytes,
		MaxColumns:     t.cfg.MaxColumns,
		ArtifactStore:  t.runtime.Artifacts,
		ArtifactPrefix: "service-" + name,
		OnPreview: func(string) {
			select {
			case logNotify <- struct{}{}:
			default:
			}
		},
	})

	args := append(append([]string(nil), shellArgs...), command)
	cmd := exec.Command(shellPath, args...)
	cmd.Dir = cwd
	configureProcessGroup(cmd)
	pr, pw, pipeErr := os.Pipe()
	if pipeErr != nil {
		svcCancel()
		sink.Dispose()
		t.runtime.Services.mu.Unlock()
		return nil, sdk.NewToolError(pipeErr.Error())
	}
	cmd.Stdout = pw
	cmd.Stderr = pw

	if startErr := cmd.Start(); startErr != nil {
		_ = pr.Close()
		_ = pw.Close()
		svcCancel()
		sink.Dispose()
		t.runtime.Services.mu.Unlock()
		return nil, sdk.NewToolError(startErr.Error())
	}
	_ = pw.Close()

	initialState := "running"
	if ready != nil && (ready.Log != "" || (ready.Port != nil && *ready.Port > 0)) {
		initialState = "starting"
	}

	svc := &managedService{
		name:      name,
		command:   command,
		cwd:       cwd,
		state:     initialState,
		pid:       cmd.Process.Pid,
		startTime: time.Now(),
		cancel:    svcCancel,
		done:      make(chan struct{}),
		sink:      sink,
		logNotify: logNotify,
	}
	t.runtime.Services.services[name] = svc
	t.runtime.Services.mu.Unlock()

	go func() {
		defer close(svc.done)
		readDone := make(chan struct{})
		go func() {
			defer close(readDone)
			buf := make([]byte, 16*1024)
			for {
				n, rErr := pr.Read(buf)
				if n > 0 {
					sink.Push(string(buf[:n]))
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
			_ = pr.Close()
		case <-time.After(250 * time.Millisecond):
			_ = pr.Close()
			<-readDone
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
		if svc.state != "stopped" {
			svc.state = "exited"
			svc.exitReason = fmt.Sprintf("exited with code %d", code)
		}
		svc.mu.Unlock()
		sink.Dispose()
	}()

	readyTimedOut := false
	hasReadyProbe := ready != nil && (logRe != nil || (ready.Port != nil && *ready.Port > 0))
	if hasReadyProbe {
		readyTimeoutSec := float64(DefaultServiceReadyTimeoutSec)
		if ready.Timeout != nil && *ready.Timeout > 0 {
			readyTimeoutSec = *ready.Timeout
		}
		host := ready.Host
		if host == "" {
			host = "127.0.0.1"
		}
		deadline := time.NewTimer(time.Duration(readyTimeoutSec * float64(time.Second)))
		defer deadline.Stop()
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()

		checkReady := func() bool {
			if logRe != nil {
				if !logRe.MatchString(sink.Preview()) {
					return false
				}
			}
			if ready.Port != nil && *ready.Port > 0 {
				conn, dialErr := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(*ready.Port)), 200*time.Millisecond)
				if dialErr != nil {
					return false
				}
				_ = conn.Close()
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
			svc.state = "running"
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
		timeoutVal := float64(DefaultServiceReadyTimeoutSec)
		if ready != nil && ready.Timeout != nil && *ready.Timeout > 0 {
			timeoutVal = *ready.Timeout
		}
		lines = append(lines, fmt.Sprintf("NOT ready — readiness timed out after %gs; process state: %s.", timeoutVal, snap.State))
	}
	if snap.ExitReason != "" {
		lines = append(lines, "Reason: "+snap.ExitReason)
	}
	if snap.Logs != "" {
		lines = append(lines, snap.Logs)
	}
	return sdk.ToolResult{
		Content: strings.Join(lines, "\n"),
		Details: map[string]any{
			"service": map[string]any{
				"name":     snap.Name,
				"state":    snap.State,
				"ready":    snap.Ready || snap.State == "running",
				"timedOut": readyTimedOut,
				"pid":      snap.PID,
			},
		},
	}, nil
}

func sessionKeyFromContext(ctx sdk.Context) string {
	if id, err := ctx.GetSessionID(); err == nil && id != "" {
		return id
	}
	if cwd := ctx.Cwd(); cwd != "" {
		return cwd
	}
	return "default"
}
