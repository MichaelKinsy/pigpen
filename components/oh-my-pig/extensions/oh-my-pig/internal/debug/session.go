package debug

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/dirs"
)

// upstream: packages/coding-agent/src/dap/session.ts

const (
	idleTimeout        = 10 * time.Minute
	cleanupInterval    = 30 * time.Second
	maxOutputBytes     = 128 * 1024
	stopCaptureTimeout = 5 * time.Second
)

var debugpyMissingModuleRe = regexp.MustCompile(`No module named ['"]?debugpy['"]?`)

// OutputSnapshot is returned by SessionManager.GetOutput.
type OutputSnapshot struct {
	Snapshot DapSessionSummary
	Output   string
}

type trackedTerminalProc struct {
	cmd  *exec.Cmd
	done chan struct{}
}

type dapSession struct {
	id                     string
	seqNum                 int
	adapter                DapResolvedAdapter
	cwd                    string
	program                string
	client                 dapClient
	status                 DapSessionStatus
	launchedAt             time.Time
	lastUsedAt             time.Time
	breakpoints            map[string][]DapBreakpointRecord
	functionBreakpoints    []DapFunctionBreakpointRecord
	instructionBreakpoints []DapInstructionBreakpoint
	dataBreakpoints        []DapDataBreakpoint
	bpMu                   sync.Mutex
	outputChunks           []string
	outputBytes            int
	outputBufferedBytes    int
	outputTruncated        bool
	stop                   DapStopLocation
	threads                []DapThread
	lastStackFrames        []DapStackFrame
	exitCode               *int
	capabilities           *DapCapabilities
	initializedSeen        bool
	initializedCh          chan struct{}
	initializedOnce        sync.Once
	needsConfigurationDone bool
	configurationDoneSent  bool
	parentSessionID        string
	childSessionIDs        map[string]struct{}
	port                   int
	debuggeePID            int
	terminalProcs          []*trackedTerminalProc
}
type treeOutcomeWaiter struct {
	id            uint64
	rootSessionID string
	ch            chan struct{}
	once          sync.Once
}

func (w *treeOutcomeWaiter) resolve() {
	w.once.Do(func() { close(w.ch) })
}

// DapSessionManager manages active DAP session trees for one PiG session.
type DapSessionManager struct {
	mu              sync.Mutex
	sessions        map[string]*dapSession
	activeSessionID string
	nextID          int
	nextWaiterID    uint64
	treeWaiters     map[uint64]*treeOutcomeWaiter
	stopCleanup     chan struct{}
	cleanupOnce     sync.Once

	// Test seams (default to real implementations).
	spawnFn        func(ctx context.Context, opts SpawnOptions) (dapClient, error)
	connectFn      func(ctx context.Context, opts ConnectOptions) (dapClient, error)
	spawnTermFn    func(args []string, cwd string, env []string) (pid int, stdout io.ReadCloser, proc *trackedTerminalProc, err error)
	idleTimeoutDur time.Duration
}

// NewSessionManager creates an isolated DapSessionManager with background idle cleanup.
func NewSessionManager() *DapSessionManager {
	m := &DapSessionManager{
		sessions:       make(map[string]*dapSession),
		treeWaiters:    make(map[uint64]*treeOutcomeWaiter),
		stopCleanup:    make(chan struct{}),
		idleTimeoutDur: idleTimeout,
	}
	m.spawnFn = func(ctx context.Context, opts SpawnOptions) (dapClient, error) {
		return SpawnDapClient(ctx, opts)
	}
	m.connectFn = func(ctx context.Context, opts ConnectOptions) (dapClient, error) {
		return ConnectDapClient(ctx, opts)
	}
	m.spawnTermFn = defaultSpawnRunInTerminal
	go m.runCleanupLoop()
	return m
}

func defaultSpawnRunInTerminal(args []string, cwd string, env []string) (int, io.ReadCloser, *trackedTerminalProc, error) {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = cwd
	cmd.Env = env
	configureProcessGroup(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, nil, nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdout.Close()
		return 0, nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		_ = stderr.Close()
		return 0, nil, nil, err
	}
	go func() {
		_, _ = io.Copy(io.Discard, stderr)
	}()
	tp := &trackedTerminalProc{cmd: cmd, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(tp.done)
	}()
	return cmd.Process.Pid, stdout, tp, nil
}

func (m *DapSessionManager) runCleanupLoop() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.stopCleanup:
			return
		case <-ticker.C:
			m.cleanupIdleSessions()
		}
	}
}

func (m *DapSessionManager) cleanupIdleSessions() {
	m.mu.Lock()
	now := time.Now()
	var stale []*dapSession
	for _, s := range m.sessions {
		if s.status == StatusTerminated || now.Sub(s.lastUsedAt) > m.idleTimeoutDur || !s.client.IsAlive() {
			stale = append(stale, s)
		}
	}
	m.mu.Unlock()
	for _, s := range stale {
		m.disposeSession(s)
	}
}

// Shutdown terminates all sessions and stops the cleanup goroutine.
func (m *DapSessionManager) Shutdown(ctx context.Context) {
	m.cleanupOnce.Do(func() { close(m.stopCleanup) })
	m.mu.Lock()
	var roots []*dapSession
	for _, s := range m.sessions {
		if s.parentSessionID == "" {
			roots = append(roots, s)
		}
	}
	if len(roots) == 0 {
		for _, s := range m.sessions {
			roots = append(roots, s)
		}
	}
	m.mu.Unlock()
	for _, root := range roots {
		m.terminateSessionTree(ctx, root, 2*time.Second)
	}
}

// GetActiveSession returns a summary of the currently focused session, or nil.
func (m *DapSessionManager) GetActiveSession() *DapSessionSummary {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.getActiveSessionLocked()
	if s == nil {
		return nil
	}
	sum := buildSummaryLocked(s)
	return &sum
}

// ListSessions returns summaries for every tracked session.
func (m *DapSessionManager) ListSessions() []DapSessionSummary {
	m.mu.Lock()
	defer m.mu.Unlock()
	ordered := make([]*dapSession, 0, len(m.sessions))
	for _, s := range m.sessions {
		ordered = append(ordered, s)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].seqNum < ordered[j].seqNum })
	out := make([]DapSessionSummary, len(ordered))
	for i, s := range ordered {
		out[i] = buildSummaryLocked(s)
	}
	return out
}

// GetCapabilities returns the active session's DAP capabilities, or nil.
func (m *DapSessionManager) GetCapabilities() *DapCapabilities {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.getActiveSessionLocked()
	if s == nil {
		return nil
	}
	return s.capabilities
}

func mapDebugpyMissingModule(adapterName string, err error) error {
	if adapterName != "debugpy" || err == nil {
		return nil
	}
	if !debugpyMissingModuleRe.MatchString(err.Error()) {
		return nil
	}
	return errors.New("adapter 'debugpy' is not available: install with 'pip install debugpy'")
}

func combineDapStartErrors(command string, startErr, configErr error) error {
	if startErr == nil {
		return configErr
	}
	if configErr == nil {
		return startErr
	}
	sm := startErr.Error()
	cm := configErr.Error()
	if sm == cm {
		return startErr
	}
	return fmt.Errorf("DAP %s failed: %s\nDAP configurationDone also failed: %s", command, sm, cm)
}

type startRequestResult struct {
	done chan struct{}
	err  error
}

func throwPreferredDapStartError(command string, sr *startRequestResult, configErr error) error {
	select {
	case <-sr.done:
	case <-time.After(50 * time.Millisecond):
	}
	select {
	case <-sr.done:
		if sr.err != nil {
			return combineDapStartErrors(command, sr.err, configErr)
		}
	default:
	}
	return configErr
}

// Launch starts a new debug adapter and launches the target program.
// upstream: packages/coding-agent/src/dap/session.ts:308-379
func (m *DapSessionManager) Launch(ctx context.Context, opts DapLaunchSessionOptions, timeout time.Duration) (DapSessionSummary, error) {
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}
	if err := m.ensureLaunchSlot(); err != nil {
		return DapSessionSummary{}, err
	}
	client, err := m.spawnFn(ctx, SpawnOptions{Adapter: opts.Adapter, Cwd: opts.Cwd})
	if err != nil {
		if mapped := mapDebugpyMissingModule(opts.Adapter.Name, err); mapped != nil {
			return DapSessionSummary{}, mapped
		}
		return DapSessionSummary{}, err
	}
	session := m.registerSession(client, opts.Adapter, opts.Cwd, opts.Program, "")

	fail := func(cause error) (DapSessionSummary, error) {
		m.disposeSession(session)
		if mapped := mapDebugpyMissingModule(opts.Adapter.Name, cause); mapped != nil {
			return DapSessionSummary{}, mapped
		}
		return DapSessionSummary{}, cause
	}

	caps, err := client.Initialize(ctx, buildInitializeArguments(opts.Adapter), timeout)
	if err != nil {
		return fail(err)
	}
	m.mu.Lock()
	session.capabilities = caps
	session.needsConfigurationDone = caps != nil && caps.SupportsConfigurationDoneRequest
	m.mu.Unlock()

	launchArgs := make(map[string]any)
	maps.Copy(launchArgs, opts.Adapter.LaunchDefaults)
	maps.Copy(launchArgs, opts.ExtraLaunchArguments)
	launchArgs["program"] = opts.Program
	launchArgs["cwd"] = opts.Cwd
	if opts.Args != nil {
		launchArgs["args"] = opts.Args
	}

	stopCap := min(timeout, stopCaptureTimeout)
	stopWaiter := m.prepareStopOutcome(session)

	// Enqueue launch synchronously BEFORE completeConfigurationHandshake so the
	// launch frame is guaranteed on the wire ahead of configurationDone
	// (upstream: dap/session.ts:341-350, dap/client.ts:495). Order-sensitive
	// adapters (e.g. gdb's launch.py, which defers launch until after
	// configurationDone and rejects configurationDone if no launch/attach is
	// pending) require this order.
	sr := &startRequestResult{done: make(chan struct{})}
	launchCall, startErr := client.StartRequest(ctx, "launch", launchArgs, timeout)
	if startErr != nil {
		m.cancelStopOutcome(stopWaiter)
		return fail(startErr)
	}
	go func() {
		_, sr.err = launchCall.Await(ctx)
		close(sr.done)
	}()

	if err := m.completeConfigurationHandshake(ctx, session, sr, timeout); err != nil {
		m.cancelStopOutcome(stopWaiter)
		return fail(throwPreferredDapStartError("launch", sr, err))
	}
	<-sr.done
	if sr.err != nil {
		m.cancelStopOutcome(stopWaiter)
		return fail(sr.err)
	}

	// Try to capture initial stopped state (e.g. stopOnEntry). Timeout or abort
	// here is swallowed and the summary is returned (upstream: dap/session.ts:357-371).
	resultSession := session
	stopTimer := time.NewTimer(stopCap)
	select {
	case <-stopWaiter.ch:
		stopTimer.Stop()
		m.cancelStopOutcome(stopWaiter)
		m.mu.Lock()
		active := m.getActiveSessionLocked()
		if active != nil && m.getRootSessionLocked(active).id == session.id {
			resultSession = active
		}
		isStopped := resultSession.status == StatusStopped
		m.mu.Unlock()
		if isStopped {
			m.fetchTopFrame(ctx, resultSession, stopCap)
		}
	case <-stopTimer.C:
		m.cancelStopOutcome(stopWaiter)
		m.mu.Lock()
		if session.initializedSeen && session.status == StatusLaunching {
			if session.configurationDoneSent {
				session.status = StatusRunning
			} else {
				session.status = StatusConfiguring
			}
		}
		m.mu.Unlock()
	case <-ctx.Done():
		stopTimer.Stop()
		m.cancelStopOutcome(stopWaiter)
		m.mu.Lock()
		if session.initializedSeen && session.status == StatusLaunching {
			if session.configurationDoneSent {
				session.status = StatusRunning
			} else {
				session.status = StatusConfiguring
			}
		}
		m.mu.Unlock()
	}

	m.mu.Lock()
	sum := buildSummaryLocked(resultSession)
	m.mu.Unlock()
	return sum, nil
}

// Attach starts a debug adapter and attaches to a running process or remote port.
// upstream: packages/coding-agent/src/dap/session.ts:381-446
func (m *DapSessionManager) Attach(ctx context.Context, opts DapAttachSessionOptions, timeout time.Duration) (DapSessionSummary, error) {
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}
	if err := m.ensureLaunchSlot(); err != nil {
		return DapSessionSummary{}, err
	}
	client, err := m.spawnFn(ctx, SpawnOptions{Adapter: opts.Adapter, Cwd: opts.Cwd})
	if err != nil {
		if mapped := mapDebugpyMissingModule(opts.Adapter.Name, err); mapped != nil {
			return DapSessionSummary{}, mapped
		}
		return DapSessionSummary{}, err
	}
	session := m.registerSession(client, opts.Adapter, opts.Cwd, "", "")

	fail := func(cause error) (DapSessionSummary, error) {
		m.disposeSession(session)
		if mapped := mapDebugpyMissingModule(opts.Adapter.Name, cause); mapped != nil {
			return DapSessionSummary{}, mapped
		}
		return DapSessionSummary{}, cause
	}

	caps, err := client.Initialize(ctx, buildInitializeArguments(opts.Adapter), timeout)
	if err != nil {
		return fail(err)
	}
	m.mu.Lock()
	session.capabilities = caps
	session.needsConfigurationDone = caps != nil && caps.SupportsConfigurationDoneRequest
	m.mu.Unlock()

	if skip, _ := opts.Adapter.AttachDefaults["skipAttachRequest"].(bool); skip {
		if err := m.completeConfigurationHandshake(ctx, session, nil, timeout); err != nil {
			return fail(err)
		}
		m.mu.Lock()
		sum := buildSummaryLocked(session)
		m.mu.Unlock()
		return sum, nil
	}

	attachArgs := make(map[string]any)
	maps.Copy(attachArgs, opts.Adapter.AttachDefaults)
	attachArgs["cwd"] = opts.Cwd
	if opts.PID != nil {
		attachArgs["pid"] = *opts.PID
		attachArgs["processId"] = *opts.PID
	}
	if opts.Port != nil {
		attachArgs["port"] = *opts.Port
	}
	if opts.Host != "" {
		attachArgs["host"] = opts.Host
	}

	stopCap := min(timeout, stopCaptureTimeout)
	stopWaiter := m.prepareStopOutcome(session)

	// Enqueue attach synchronously BEFORE completeConfigurationHandshake
	// (upstream: dap/session.ts:413-419).
	sr := &startRequestResult{done: make(chan struct{})}
	attachCall, startErr := client.StartRequest(ctx, "attach", attachArgs, timeout)
	if startErr != nil {
		m.cancelStopOutcome(stopWaiter)
		return fail(startErr)
	}
	go func() {
		_, sr.err = attachCall.Await(ctx)
		close(sr.done)
	}()

	if err := m.completeConfigurationHandshake(ctx, session, sr, timeout); err != nil {
		m.cancelStopOutcome(stopWaiter)
		return fail(throwPreferredDapStartError("attach", sr, err))
	}
	<-sr.done
	if sr.err != nil {
		m.cancelStopOutcome(stopWaiter)
		return fail(sr.err)
	}

	resultSession := session
	stopTimer := time.NewTimer(stopCap)
	select {
	case <-stopWaiter.ch:
		stopTimer.Stop()
		m.cancelStopOutcome(stopWaiter)
		m.mu.Lock()
		active := m.getActiveSessionLocked()
		if active != nil && m.getRootSessionLocked(active).id == session.id {
			resultSession = active
		}
		isStopped := resultSession.status == StatusStopped
		m.mu.Unlock()
		if isStopped {
			m.fetchTopFrame(ctx, resultSession, stopCap)
		}
	case <-stopTimer.C:
		m.cancelStopOutcome(stopWaiter)
		m.mu.Lock()
		if session.initializedSeen && session.status == StatusLaunching {
			if session.configurationDoneSent {
				session.status = StatusRunning
			} else {
				session.status = StatusConfiguring
			}
		}
		m.mu.Unlock()
	case <-ctx.Done():
		stopTimer.Stop()
		m.cancelStopOutcome(stopWaiter)
		m.mu.Lock()
		if session.initializedSeen && session.status == StatusLaunching {
			if session.configurationDoneSent {
				session.status = StatusRunning
			} else {
				session.status = StatusConfiguring
			}
		}
		m.mu.Unlock()
	}

	m.mu.Lock()
	sum := buildSummaryLocked(resultSession)
	m.mu.Unlock()
	return sum, nil
}

func (m *DapSessionManager) syncBreakpointTree(
	ctx context.Context,
	origin *dapSession,
	command string,
	args any,
	prepare func(s *dapSession),
	apply func(s *dapSession, bps []DapBreakpoint),
	timeout time.Duration,
) error {
	m.mu.Lock()
	allTree := m.getTreeSessionsLocked(origin)
	var live []*dapSession
	for _, s := range allTree {
		if s.status != StatusTerminated && s.client.IsAlive() {
			live = append(live, s)
			prepare(s)
		}
	}
	m.mu.Unlock()

	sendOne := func(target *dapSession) error {
		target.bpMu.Lock()
		defer target.bpMu.Unlock()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		raw, err := m.sendRequestWithConfig(ctx, target, command, args, timeout)
		if err != nil {
			return err
		}
		var resp struct {
			Breakpoints []DapBreakpoint `json:"breakpoints"`
		}
		if len(raw) > 0 && string(raw) != "null" {
			_ = json.Unmarshal(raw, &resp)
		}
		m.mu.Lock()
		apply(target, resp.Breakpoints)
		m.mu.Unlock()
		return nil
	}

	if err := sendOne(origin); err != nil {
		return err
	}

	var wg sync.WaitGroup
	for _, s := range live {
		if s == origin {
			continue
		}
		wg.Add(1)
		go func(target *dapSession) {
			defer wg.Done()
			_ = sendOne(target)
		}(s)
	}
	wg.Wait()
	return nil
}

// SetBreakpoint adds or updates a source breakpoint at file:line across the session tree.
func (m *DapSessionManager) SetBreakpoint(ctx context.Context, file string, line int, condition string, timeout time.Duration) (DapSessionSummary, []DapBreakpointRecord, string, error) {
	session, err := m.touchActiveSession()
	if err != nil {
		return DapSessionSummary{}, nil, "", err
	}
	sourcePath, _ := filepath.Abs(file)

	m.mu.Lock()
	root := m.getRootSessionLocked(session)
	var current []DapBreakpointRecord
	for _, entry := range root.breakpoints[sourcePath] {
		if entry.Line != line {
			current = append(current, entry)
		}
	}
	current = append(current, DapBreakpointRecord{Verified: false, Line: line, Condition: condition})
	sort.Slice(current, func(i, j int) bool { return current[i].Line < current[j].Line })
	m.mu.Unlock()

	reqBPs := make([]DapSourceBreakpoint, len(current))
	for i, e := range current {
		reqBPs[i] = DapSourceBreakpoint{Line: e.Line, Condition: e.Condition}
	}
	args := map[string]any{
		"source":      DapSource{Path: sourcePath, Name: filepath.Base(sourcePath)},
		"breakpoints": reqBPs,
	}

	err = m.syncBreakpointTree(
		ctx,
		session,
		"setBreakpoints",
		args,
		func(target *dapSession) {
			pending := make([]DapBreakpointRecord, len(current))
			copy(pending, current)
			for i := range pending {
				pending[i].Verified = false
			}
			target.breakpoints[sourcePath] = pending
		},
		func(target *dapSession, bps []DapBreakpoint) {
			target.breakpoints[sourcePath] = mapSourceBreakpoints(current, bps)
		},
		timeout,
	)
	if err != nil {
		return DapSessionSummary{}, nil, "", err
	}

	m.mu.Lock()
	sum := buildSummaryLocked(session)
	out := slices.Clone(session.breakpoints[sourcePath])
	m.mu.Unlock()
	return sum, out, sourcePath, nil
}

// RemoveBreakpoint removes a source breakpoint at file:line across the session tree.
func (m *DapSessionManager) RemoveBreakpoint(ctx context.Context, file string, line int, timeout time.Duration) (DapSessionSummary, []DapBreakpointRecord, string, error) {
	session, err := m.touchActiveSession()
	if err != nil {
		return DapSessionSummary{}, nil, "", err
	}
	sourcePath, _ := filepath.Abs(file)

	m.mu.Lock()
	root := m.getRootSessionLocked(session)
	var current []DapBreakpointRecord
	for _, entry := range root.breakpoints[sourcePath] {
		if entry.Line != line {
			current = append(current, entry)
		}
	}
	m.mu.Unlock()

	reqBPs := make([]DapSourceBreakpoint, len(current))
	for i, e := range current {
		reqBPs[i] = DapSourceBreakpoint{Line: e.Line, Condition: e.Condition}
	}
	args := map[string]any{
		"source":      DapSource{Path: sourcePath, Name: filepath.Base(sourcePath)},
		"breakpoints": reqBPs,
	}

	err = m.syncBreakpointTree(
		ctx,
		session,
		"setBreakpoints",
		args,
		func(target *dapSession) {
			if len(current) == 0 {
				delete(target.breakpoints, sourcePath)
			} else {
				pending := make([]DapBreakpointRecord, len(current))
				copy(pending, current)
				for i := range pending {
					pending[i].Verified = false
				}
				target.breakpoints[sourcePath] = pending
			}
		},
		func(target *dapSession, bps []DapBreakpoint) {
			if len(current) == 0 {
				delete(target.breakpoints, sourcePath)
			} else {
				target.breakpoints[sourcePath] = mapSourceBreakpoints(current, bps)
			}
		},
		timeout,
	)
	if err != nil {
		return DapSessionSummary{}, nil, "", err
	}

	m.mu.Lock()
	sum := buildSummaryLocked(session)
	out := slices.Clone(session.breakpoints[sourcePath])
	m.mu.Unlock()
	return sum, out, sourcePath, nil
}

// SetFunctionBreakpoint sets a function breakpoint across the session tree.
func (m *DapSessionManager) SetFunctionBreakpoint(ctx context.Context, name string, condition string, timeout time.Duration) (DapSessionSummary, []DapFunctionBreakpointRecord, error) {
	session, err := m.touchActiveSession()
	if err != nil {
		return DapSessionSummary{}, nil, err
	}
	m.mu.Lock()
	root := m.getRootSessionLocked(session)
	var current []DapFunctionBreakpointRecord
	for _, entry := range root.functionBreakpoints {
		if entry.Name != name {
			current = append(current, entry)
		}
	}
	current = append(current, DapFunctionBreakpointRecord{Verified: false, Name: name, Condition: condition})
	sort.Slice(current, func(i, j int) bool { return current[i].Name < current[j].Name })
	m.mu.Unlock()

	reqBPs := make([]DapFunctionBreakpoint, len(current))
	for i, e := range current {
		reqBPs[i] = DapFunctionBreakpoint{Name: e.Name, Condition: e.Condition}
	}
	args := map[string]any{"breakpoints": reqBPs}

	err = m.syncBreakpointTree(
		ctx,
		session,
		"setFunctionBreakpoints",
		args,
		func(target *dapSession) {
			pending := make([]DapFunctionBreakpointRecord, len(current))
			copy(pending, current)
			for i := range pending {
				pending[i].Verified = false
			}
			target.functionBreakpoints = pending
		},
		func(target *dapSession, bps []DapBreakpoint) {
			target.functionBreakpoints = mapFunctionBreakpoints(current, bps)
		},
		timeout,
	)
	if err != nil {
		return DapSessionSummary{}, nil, err
	}

	m.mu.Lock()
	sum := buildSummaryLocked(session)
	out := slices.Clone(session.functionBreakpoints)
	m.mu.Unlock()
	return sum, out, nil
}

// RemoveFunctionBreakpoint removes a function breakpoint across the session tree.
func (m *DapSessionManager) RemoveFunctionBreakpoint(ctx context.Context, name string, timeout time.Duration) (DapSessionSummary, []DapFunctionBreakpointRecord, error) {
	session, err := m.touchActiveSession()
	if err != nil {
		return DapSessionSummary{}, nil, err
	}
	m.mu.Lock()
	root := m.getRootSessionLocked(session)
	var current []DapFunctionBreakpointRecord
	for _, entry := range root.functionBreakpoints {
		if entry.Name != name {
			current = append(current, entry)
		}
	}
	m.mu.Unlock()

	reqBPs := make([]DapFunctionBreakpoint, len(current))
	for i, e := range current {
		reqBPs[i] = DapFunctionBreakpoint{Name: e.Name, Condition: e.Condition}
	}
	args := map[string]any{"breakpoints": reqBPs}

	err = m.syncBreakpointTree(
		ctx,
		session,
		"setFunctionBreakpoints",
		args,
		func(target *dapSession) {
			pending := make([]DapFunctionBreakpointRecord, len(current))
			copy(pending, current)
			for i := range pending {
				pending[i].Verified = false
			}
			target.functionBreakpoints = pending
		},
		func(target *dapSession, bps []DapBreakpoint) {
			target.functionBreakpoints = mapFunctionBreakpoints(current, bps)
		},
		timeout,
	)
	if err != nil {
		return DapSessionSummary{}, nil, err
	}

	m.mu.Lock()
	sum := buildSummaryLocked(session)
	out := slices.Clone(session.functionBreakpoints)
	m.mu.Unlock()
	return sum, out, nil
}

func intPtrEqual(a, b *int) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// SetInstructionBreakpoint sets an instruction breakpoint across the session tree.
func (m *DapSessionManager) SetInstructionBreakpoint(ctx context.Context, ref string, offset *int, condition, hitCondition string, timeout time.Duration) (DapSessionSummary, []DapInstructionBreakpointRecord, error) {
	session, err := m.touchActiveSession()
	if err != nil {
		return DapSessionSummary{}, nil, err
	}
	m.mu.Lock()
	root := m.getRootSessionLocked(session)
	var current []DapInstructionBreakpoint
	for _, entry := range root.instructionBreakpoints {
		if entry.InstructionReference != ref || !intPtrEqual(entry.Offset, offset) {
			current = append(current, entry)
		}
	}
	current = append(current, DapInstructionBreakpoint{
		InstructionReference: ref,
		Offset:               offset,
		Condition:            condition,
		HitCondition:         hitCondition,
	})
	sort.Slice(current, func(i, j int) bool {
		if current[i].InstructionReference != current[j].InstructionReference {
			return current[i].InstructionReference < current[j].InstructionReference
		}
		lo, ro := 0, 0
		if current[i].Offset != nil {
			lo = *current[i].Offset
		}
		if current[j].Offset != nil {
			ro = *current[j].Offset
		}
		return lo < ro
	})
	m.mu.Unlock()

	args := map[string]any{"breakpoints": current}
	var respBPs []DapBreakpoint
	err = m.syncBreakpointTree(
		ctx,
		session,
		"setInstructionBreakpoints",
		args,
		func(target *dapSession) {
			target.instructionBreakpoints = slices.Clone(current)
		},
		func(target *dapSession, bps []DapBreakpoint) {
			if target == session {
				respBPs = bps
			}
		},
		timeout,
	)
	if err != nil {
		return DapSessionSummary{}, nil, err
	}
	m.mu.Lock()
	sum := buildSummaryLocked(session)
	m.mu.Unlock()
	return sum, mapInstructionBreakpoints(current, respBPs), nil
}

// RemoveInstructionBreakpoint removes an instruction breakpoint across the session tree.
func (m *DapSessionManager) RemoveInstructionBreakpoint(ctx context.Context, ref string, offset *int, timeout time.Duration) (DapSessionSummary, []DapInstructionBreakpointRecord, error) {
	session, err := m.touchActiveSession()
	if err != nil {
		return DapSessionSummary{}, nil, err
	}
	m.mu.Lock()
	root := m.getRootSessionLocked(session)
	var current []DapInstructionBreakpoint
	for _, entry := range root.instructionBreakpoints {
		if entry.InstructionReference != ref {
			current = append(current, entry)
			continue
		}
		if offset != nil && !intPtrEqual(entry.Offset, offset) {
			current = append(current, entry)
		}
	}
	m.mu.Unlock()

	if current == nil {
		current = []DapInstructionBreakpoint{}
	}
	args := map[string]any{"breakpoints": current}
	var respBPs []DapBreakpoint
	err = m.syncBreakpointTree(
		ctx,
		session,
		"setInstructionBreakpoints",
		args,
		func(target *dapSession) {
			target.instructionBreakpoints = slices.Clone(current)
		},
		func(target *dapSession, bps []DapBreakpoint) {
			if target == session {
				respBPs = bps
			}
		},
		timeout,
	)
	if err != nil {
		return DapSessionSummary{}, nil, err
	}
	m.mu.Lock()
	sum := buildSummaryLocked(session)
	m.mu.Unlock()
	return sum, mapInstructionBreakpoints(current, respBPs), nil
}

// DataBreakpointInfo queries the adapter for a data breakpoint ID.
func (m *DapSessionManager) DataBreakpointInfo(ctx context.Context, name string, varRef *int, frameID *int, timeout time.Duration) (DapSessionSummary, DapDataBreakpointInfoResponse, error) {
	session, err := m.touchActiveSession()
	if err != nil {
		return DapSessionSummary{}, DapDataBreakpointInfoResponse{}, err
	}
	args := map[string]any{"name": name}
	if varRef != nil {
		args["variablesReference"] = *varRef
	}
	if frameID != nil {
		args["frameId"] = *frameID
	}
	raw, err := m.sendRequestWithConfig(ctx, session, "dataBreakpointInfo", args, timeout)
	if err != nil {
		return DapSessionSummary{}, DapDataBreakpointInfoResponse{}, err
	}
	var info DapDataBreakpointInfoResponse
	if len(raw) > 0 && string(raw) != "null" {
		_ = json.Unmarshal(raw, &info)
	}
	m.mu.Lock()
	sum := buildSummaryLocked(session)
	m.mu.Unlock()
	return sum, info, nil
}

// SetDataBreakpoint sets a data breakpoint across the session tree.
func (m *DapSessionManager) SetDataBreakpoint(ctx context.Context, dataID, accessType, condition, hitCondition string, timeout time.Duration) (DapSessionSummary, []DapDataBreakpointRecord, error) {
	session, err := m.touchActiveSession()
	if err != nil {
		return DapSessionSummary{}, nil, err
	}
	m.mu.Lock()
	root := m.getRootSessionLocked(session)
	var current []DapDataBreakpoint
	for _, entry := range root.dataBreakpoints {
		if entry.DataID != dataID {
			current = append(current, entry)
		}
	}
	current = append(current, DapDataBreakpoint{
		DataID:       dataID,
		AccessType:   accessType,
		Condition:    condition,
		HitCondition: hitCondition,
	})
	sort.Slice(current, func(i, j int) bool { return current[i].DataID < current[j].DataID })
	m.mu.Unlock()

	args := map[string]any{"breakpoints": current}
	var respBPs []DapBreakpoint
	err = m.syncBreakpointTree(
		ctx,
		session,
		"setDataBreakpoints",
		args,
		func(target *dapSession) {
			target.dataBreakpoints = slices.Clone(current)
		},
		func(target *dapSession, bps []DapBreakpoint) {
			if target == session {
				respBPs = bps
			}
		},
		timeout,
	)
	if err != nil {
		return DapSessionSummary{}, nil, err
	}
	m.mu.Lock()
	sum := buildSummaryLocked(session)
	m.mu.Unlock()
	return sum, mapDataBreakpoints(current, respBPs), nil
}

// RemoveDataBreakpoint removes a data breakpoint across the session tree.
func (m *DapSessionManager) RemoveDataBreakpoint(ctx context.Context, dataID string, timeout time.Duration) (DapSessionSummary, []DapDataBreakpointRecord, error) {
	session, err := m.touchActiveSession()
	if err != nil {
		return DapSessionSummary{}, nil, err
	}
	m.mu.Lock()
	root := m.getRootSessionLocked(session)
	var current []DapDataBreakpoint
	for _, entry := range root.dataBreakpoints {
		if entry.DataID != dataID {
			current = append(current, entry)
		}
	}
	m.mu.Unlock()

	if current == nil {
		current = []DapDataBreakpoint{}
	}
	args := map[string]any{"breakpoints": current}
	var respBPs []DapBreakpoint
	err = m.syncBreakpointTree(
		ctx,
		session,
		"setDataBreakpoints",
		args,
		func(target *dapSession) {
			target.dataBreakpoints = slices.Clone(current)
		},
		func(target *dapSession, bps []DapBreakpoint) {
			if target == session {
				respBPs = bps
			}
		},
		timeout,
	)
	if err != nil {
		return DapSessionSummary{}, nil, err
	}
	m.mu.Lock()
	sum := buildSummaryLocked(session)
	m.mu.Unlock()
	return sum, mapDataBreakpoints(current, respBPs), nil
}

// Disassemble requests disassembled instructions from the adapter.
func (m *DapSessionManager) Disassemble(ctx context.Context, memRef string, count int, offset, instrOffset *int, resolveSymbols *bool, timeout time.Duration) (DapSessionSummary, []DapDisassembledInstruction, error) {
	session, err := m.touchActiveSession()
	if err != nil {
		return DapSessionSummary{}, nil, err
	}
	args := map[string]any{
		"memoryReference":  memRef,
		"instructionCount": count,
	}
	if offset != nil {
		args["offset"] = *offset
	}
	if instrOffset != nil {
		args["instructionOffset"] = *instrOffset
	}
	if resolveSymbols != nil {
		args["resolveSymbols"] = *resolveSymbols
	}
	raw, err := m.sendRequestWithConfig(ctx, session, "disassemble", args, timeout)
	if err != nil {
		return DapSessionSummary{}, nil, err
	}
	var resp struct {
		Instructions []DapDisassembledInstruction `json:"instructions"`
	}
	if len(raw) > 0 && string(raw) != "null" {
		_ = json.Unmarshal(raw, &resp)
	}
	for i := range resp.Instructions {
		standardizeSource(resp.Instructions[i].Location)
	}
	m.mu.Lock()
	sum := buildSummaryLocked(session)
	m.mu.Unlock()
	return sum, resp.Instructions, nil
}

// ReadMemory reads bytes from target memory.
func (m *DapSessionManager) ReadMemory(ctx context.Context, memRef string, count int, offset *int, timeout time.Duration) (DapSessionSummary, DapReadMemoryResponse, error) {
	session, err := m.touchActiveSession()
	if err != nil {
		return DapSessionSummary{}, DapReadMemoryResponse{}, err
	}
	args := map[string]any{
		"memoryReference": memRef,
		"count":           count,
	}
	if offset != nil {
		args["offset"] = *offset
	}
	raw, err := m.sendRequestWithConfig(ctx, session, "readMemory", args, timeout)
	if err != nil {
		return DapSessionSummary{}, DapReadMemoryResponse{}, err
	}
	var resp DapReadMemoryResponse
	if len(raw) > 0 && string(raw) != "null" {
		_ = json.Unmarshal(raw, &resp)
	}
	if resp.Address == "" {
		resp.Address = memRef
	}
	m.mu.Lock()
	sum := buildSummaryLocked(session)
	m.mu.Unlock()
	return sum, resp, nil
}

// WriteMemory writes base64 bytes to target memory.
func (m *DapSessionManager) WriteMemory(ctx context.Context, memRef, data string, offset *int, allowPartial *bool, timeout time.Duration) (DapSessionSummary, DapWriteMemoryResponse, error) {
	session, err := m.touchActiveSession()
	if err != nil {
		return DapSessionSummary{}, DapWriteMemoryResponse{}, err
	}
	args := map[string]any{
		"memoryReference": memRef,
		"data":            data,
	}
	if offset != nil {
		args["offset"] = *offset
	}
	if allowPartial != nil {
		args["allowPartial"] = *allowPartial
	}
	raw, err := m.sendRequestWithConfig(ctx, session, "writeMemory", args, timeout)
	if err != nil {
		return DapSessionSummary{}, DapWriteMemoryResponse{}, err
	}
	var resp DapWriteMemoryResponse
	if len(raw) > 0 && string(raw) != "null" {
		_ = json.Unmarshal(raw, &resp)
	}
	m.mu.Lock()
	sum := buildSummaryLocked(session)
	m.mu.Unlock()
	return sum, resp, nil
}

// Modules requests loaded modules from the adapter.
func (m *DapSessionManager) Modules(ctx context.Context, startModule, moduleCount *int, timeout time.Duration) (DapSessionSummary, []DapModule, error) {
	session, err := m.touchActiveSession()
	if err != nil {
		return DapSessionSummary{}, nil, err
	}
	args := map[string]any{}
	if startModule != nil {
		args["startModule"] = *startModule
	}
	if moduleCount != nil {
		args["moduleCount"] = *moduleCount
	}
	raw, err := m.sendRequestWithConfig(ctx, session, "modules", args, timeout)
	if err != nil {
		return DapSessionSummary{}, nil, err
	}
	var resp struct {
		Modules []DapModule `json:"modules"`
	}
	if len(raw) > 0 && string(raw) != "null" {
		_ = json.Unmarshal(raw, &resp)
	}
	for i := range resp.Modules {
		resp.Modules[i].Path = dirs.StandardizeMacOSPath(resp.Modules[i].Path)
		resp.Modules[i].SymbolFilePath = dirs.StandardizeMacOSPath(resp.Modules[i].SymbolFilePath)
	}
	m.mu.Lock()
	sum := buildSummaryLocked(session)
	m.mu.Unlock()
	return sum, resp.Modules, nil
}

// LoadedSources requests loaded source files from the adapter.
func (m *DapSessionManager) LoadedSources(ctx context.Context, timeout time.Duration) (DapSessionSummary, []DapSource, error) {
	session, err := m.touchActiveSession()
	if err != nil {
		return DapSessionSummary{}, nil, err
	}
	raw, err := m.sendRequestWithConfig(ctx, session, "loadedSources", map[string]any{}, timeout)
	if err != nil {
		return DapSessionSummary{}, nil, err
	}
	var resp struct {
		Sources []DapSource `json:"sources"`
	}
	if len(raw) > 0 && string(raw) != "null" {
		_ = json.Unmarshal(raw, &resp)
	}
	for i := range resp.Sources {
		resp.Sources[i].Path = dirs.StandardizeMacOSPath(resp.Sources[i].Path)
	}
	m.mu.Lock()
	sum := buildSummaryLocked(session)
	m.mu.Unlock()
	return sum, resp.Sources, nil
}

// CustomRequest sends an arbitrary DAP request command.
func (m *DapSessionManager) CustomRequest(ctx context.Context, command string, args map[string]any, timeout time.Duration) (DapSessionSummary, any, error) {
	session, err := m.touchActiveSession()
	if err != nil {
		return DapSessionSummary{}, nil, err
	}
	raw, err := m.sendRequestWithConfig(ctx, session, command, args, timeout)
	if err != nil {
		return DapSessionSummary{}, nil, err
	}
	var body any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &body)
	}
	m.mu.Lock()
	sum := buildSummaryLocked(session)
	m.mu.Unlock()
	return sum, body, nil
}

// Continue resumes execution and waits for the next stop, exit, or timeout.
func (m *DapSessionManager) Continue(ctx context.Context, timeout time.Duration) (DapContinueOutcome, error) {
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}
	session, err := m.touchActiveSession()
	if err != nil {
		return DapContinueOutcome{}, err
	}
	threadID, err := m.resolveThreadID(ctx, session, timeout)
	if err != nil {
		return DapContinueOutcome{}, err
	}
	m.mu.Lock()
	session.stop = DapStopLocation{}
	session.lastStackFrames = nil
	session.status = StatusRunning
	m.mu.Unlock()

	waiter := m.prepareStopOutcome(session)
	if _, err := m.sendRequestWithConfig(ctx, session, "continue", map[string]any{"threadId": threadID}, timeout); err != nil {
		m.cancelStopOutcome(waiter)
		return DapContinueOutcome{}, err
	}
	return m.awaitStopOutcome(ctx, session, waiter, timeout)
}

// StepOver executes DAP `next`.
func (m *DapSessionManager) StepOver(ctx context.Context, timeout time.Duration) (DapContinueOutcome, error) {
	return m.step(ctx, "next", timeout)
}

// StepIn executes DAP `stepIn`.
func (m *DapSessionManager) StepIn(ctx context.Context, timeout time.Duration) (DapContinueOutcome, error) {
	return m.step(ctx, "stepIn", timeout)
}

// StepOut executes DAP `stepOut`.
func (m *DapSessionManager) StepOut(ctx context.Context, timeout time.Duration) (DapContinueOutcome, error) {
	return m.step(ctx, "stepOut", timeout)
}

func (m *DapSessionManager) step(ctx context.Context, command string, timeout time.Duration) (DapContinueOutcome, error) {
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}
	session, err := m.touchActiveSession()
	if err != nil {
		return DapContinueOutcome{}, err
	}
	threadID, err := m.resolveThreadID(ctx, session, timeout)
	if err != nil {
		return DapContinueOutcome{}, err
	}
	m.mu.Lock()
	session.stop = DapStopLocation{}
	session.lastStackFrames = nil
	session.status = StatusRunning
	m.mu.Unlock()

	waiter := m.prepareStopOutcome(session)
	if _, err := m.sendRequestWithConfig(ctx, session, command, map[string]any{"threadId": threadID}, timeout); err != nil {
		m.cancelStopOutcome(waiter)
		return DapContinueOutcome{}, err
	}
	return m.awaitStopOutcome(ctx, session, waiter, timeout)
}

// Pause interrupts a running debuggee.
// upstream: packages/coding-agent/src/dap/session.ts:950-987
func (m *DapSessionManager) Pause(ctx context.Context, timeout time.Duration) (DapSessionSummary, error) {
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}
	session, err := m.touchActiveSession()
	if err != nil {
		return DapSessionSummary{}, err
	}
	m.mu.Lock()
	if session.status == StatusStopped {
		sum := buildSummaryLocked(session)
		m.mu.Unlock()
		return sum, nil
	}
	m.mu.Unlock()

	threadID, err := m.resolveThreadID(ctx, session, timeout)
	if err != nil {
		return DapSessionSummary{}, err
	}

	waitCtx, cancelWait := context.WithCancel(ctx)
	defer cancelWait()
	stoppedCh := make(chan error, 1)
	go func() {
		_, werr := session.client.WaitForEvent(waitCtx, "stopped", nil, timeout)
		stoppedCh <- werr
	}()

	if _, err := m.sendRequestWithConfig(ctx, session, "pause", map[string]any{"threadId": threadID}, timeout); err != nil {
		if strings.TrimSpace(err.Error()) == "notStopped" {
			return DapSessionSummary{}, errors.New("The debug adapter refused to pause (notStopped): the target is still running.")
		}
		return DapSessionSummary{}, err
	}

	m.mu.Lock()
	alreadyStopped := session.status == StatusStopped
	m.mu.Unlock()
	if !alreadyStopped {
		select {
		case <-stoppedCh:
		case <-ctx.Done():
		}
	}
	// Upstream dap/session.ts:950-987 returns buildSummary(session) directly without fetchTopFrame.
	m.mu.Lock()
	sum := buildSummaryLocked(session)
	m.mu.Unlock()
	return sum, nil
}

// Threads aggregates live threads across the entire active session tree.
// upstream: packages/coding-agent/src/dap/session.ts:1001-1049
func (m *DapSessionManager) Threads(ctx context.Context, timeout time.Duration) (DapSessionSummary, []DapThread, error) {
	anchor, err := m.touchActiveSession()
	if err != nil {
		return DapSessionSummary{}, nil, err
	}
	m.mu.Lock()
	targets := m.liveTreeSessionsLocked(anchor)
	m.mu.Unlock()

	var merged []DapThread
	seen := make(map[string]struct{})
	for _, target := range targets {
		raw, rerr := m.sendRequestWithConfig(ctx, target, "threads", nil, timeout)
		if rerr != nil {
			if ctx.Err() != nil {
				return DapSessionSummary{}, nil, rerr
			}
			continue
		}
		var resp struct {
			Threads []DapThread `json:"threads"`
		}
		if len(raw) > 0 && string(raw) != "null" {
			_ = json.Unmarshal(raw, &resp)
		}
		m.mu.Lock()
		target.threads = resp.Threads
		m.mu.Unlock()

		for _, th := range resp.Threads {
			key := fmt.Sprintf("%s\x00%d", target.id, th.ID)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			merged = append(merged, th)
		}
	}

	m.mu.Lock()
	sum := buildSummaryLocked(anchor)
	m.mu.Unlock()
	return sum, merged, nil
}

// StackTrace requests stack frames for the active thread.
func (m *DapSessionManager) StackTrace(ctx context.Context, levels *int, timeout time.Duration) (DapSessionSummary, []DapStackFrame, error) {
	session, err := m.touchActiveSession()
	if err != nil {
		return DapSessionSummary{}, nil, err
	}
	threadID, err := m.resolveThreadID(ctx, session, timeout)
	if err != nil {
		return DapSessionSummary{}, nil, err
	}
	args := map[string]any{"threadId": threadID}
	if levels != nil {
		args["levels"] = *levels
	}
	raw, err := m.sendRequestWithConfig(ctx, session, "stackTrace", args, timeout)
	if err != nil {
		return DapSessionSummary{}, nil, err
	}
	var resp struct {
		StackFrames []DapStackFrame `json:"stackFrames"`
		TotalFrames *int            `json:"totalFrames"`
	}
	if len(raw) > 0 && string(raw) != "null" {
		_ = json.Unmarshal(raw, &resp)
	}
	for i := range resp.StackFrames {
		standardizeSource(resp.StackFrames[i].Source)
	}
	m.mu.Lock()
	session.lastStackFrames = resp.StackFrames
	if len(resp.StackFrames) > 0 {
		applyTopFrameLocked(session, &resp.StackFrames[0])
	}
	sum := buildSummaryLocked(session)
	m.mu.Unlock()
	return sum, resp.StackFrames, nil
}

// Scopes requests variable scopes for frameID (defaulting to the top stopped frame).
func (m *DapSessionManager) Scopes(ctx context.Context, frameID *int, timeout time.Duration) (DapSessionSummary, []DapScope, error) {
	session, err := m.touchActiveSession()
	if err != nil {
		return DapSessionSummary{}, nil, err
	}
	m.mu.Lock()
	resolved := frameID
	if resolved == nil {
		resolved = session.stop.FrameID
	}
	m.mu.Unlock()
	if resolved == nil {
		return DapSessionSummary{}, nil, errors.New("No active stack frame. Run stack_trace first or supply frame_id.")
	}
	raw, err := m.sendRequestWithConfig(ctx, session, "scopes", map[string]any{"frameId": *resolved}, timeout)
	if err != nil {
		return DapSessionSummary{}, nil, err
	}
	var resp struct {
		Scopes []DapScope `json:"scopes"`
	}
	if len(raw) > 0 && string(raw) != "null" {
		_ = json.Unmarshal(raw, &resp)
	}
	for i := range resp.Scopes {
		standardizeSource(resp.Scopes[i].Source)
	}
	m.mu.Lock()
	sum := buildSummaryLocked(session)
	m.mu.Unlock()
	return sum, resp.Scopes, nil
}

// Variables requests child variables for a variablesReference.
func (m *DapSessionManager) Variables(ctx context.Context, variableRef int, timeout time.Duration) (DapSessionSummary, []DapVariable, error) {
	session, err := m.touchActiveSession()
	if err != nil {
		return DapSessionSummary{}, nil, err
	}
	raw, err := m.sendRequestWithConfig(ctx, session, "variables", map[string]any{"variablesReference": variableRef}, timeout)
	if err != nil {
		return DapSessionSummary{}, nil, err
	}
	var resp struct {
		Variables []DapVariable `json:"variables"`
	}
	if len(raw) > 0 && string(raw) != "null" {
		_ = json.Unmarshal(raw, &resp)
	}
	m.mu.Lock()
	sum := buildSummaryLocked(session)
	m.mu.Unlock()
	return sum, resp.Variables, nil
}

// Evaluate evaluates an expression in the debugger.
func (m *DapSessionManager) Evaluate(ctx context.Context, expression, evalCtx string, frameID *int, timeout time.Duration) (DapSessionSummary, DapEvaluateResponse, error) {
	session, err := m.touchActiveSession()
	if err != nil {
		return DapSessionSummary{}, DapEvaluateResponse{}, err
	}
	m.mu.Lock()
	effectiveFrameID := frameID
	if effectiveFrameID == nil {
		effectiveFrameID = session.stop.FrameID
	}
	m.mu.Unlock()
	args := map[string]any{
		"expression": expression,
		"context":    evalCtx,
	}
	if effectiveFrameID != nil {
		args["frameId"] = *effectiveFrameID
	}
	raw, err := m.sendRequestWithConfig(ctx, session, "evaluate", args, timeout)
	if err != nil {
		return DapSessionSummary{}, DapEvaluateResponse{}, err
	}
	var resp DapEvaluateResponse
	if len(raw) > 0 && string(raw) != "null" {
		_ = json.Unmarshal(raw, &resp)
	}
	m.mu.Lock()
	sum := buildSummaryLocked(session)
	m.mu.Unlock()
	return sum, resp, nil
}

// GetOutput returns captured debuggee stdout/stderr/console output for the active session.
func (m *DapSessionManager) GetOutput(limitBytes int) (OutputSnapshot, error) {
	session, err := m.touchActiveSession()
	if err != nil {
		return OutputSnapshot{}, err
	}
	m.mu.Lock()
	output := strings.Join(session.outputChunks, "")
	if limitBytes > 0 && len(output) > limitBytes {
		output = output[len(output)-limitBytes:]
	}
	sum := buildSummaryLocked(session)
	m.mu.Unlock()
	return OutputSnapshot{Snapshot: sum, Output: output}, nil
}

// Terminate shuts down the active session tree and returns its final summary (or nil if none).
func (m *DapSessionManager) Terminate(ctx context.Context, timeout time.Duration) (*DapSessionSummary, error) {
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}
	m.mu.Lock()
	session := m.getActiveSessionLocked()
	if session == nil {
		m.mu.Unlock()
		return nil, nil
	}
	m.touchSessionAndAncestorsLocked(session)
	root := m.getRootSessionLocked(session)
	sum := buildSummaryLocked(session)
	m.mu.Unlock()

	m.terminateSessionTree(ctx, root, timeout)
	return &sum, nil
}

func (m *DapSessionManager) terminateSessionTree(ctx context.Context, session *dapSession, timeout time.Duration) {
	m.mu.Lock()
	session.status = StatusTerminated
	childIDs := make([]string, 0, len(session.childSessionIDs))
	for id := range session.childSessionIDs {
		childIDs = append(childIDs, id)
	}
	children := make([]*dapSession, 0, len(childIDs))
	for _, id := range childIDs {
		if ch, ok := m.sessions[id]; ok {
			children = append(children, ch)
		}
	}
	supportsTerminate := session.capabilities != nil && session.capabilities.SupportsTerminateRequest
	m.mu.Unlock()

	for _, ch := range children {
		m.terminateSessionTree(ctx, ch, timeout)
	}
	// Give terminate and disconnect the full call timeout (upstream: dap/session.ts:1163-1168).
	if supportsTerminate {
		_, _ = session.client.SendRequest(ctx, "terminate", nil, timeout)
	}
	_, _ = session.client.SendRequest(ctx, "disconnect", map[string]any{"terminateDebuggee": true}, timeout)
	m.disposeSession(session)
}

func (m *DapSessionManager) startChildSession(parent *dapSession, request string, configuration map[string]any, timeout time.Duration) error {
	if parent.adapter.ConnectMode != ConnectModeTCP || parent.port == 0 {
		return fmt.Errorf("DAP adapter %s cannot accept child session connections", parent.adapter.Name)
	}
	childCwd := parent.cwd
	if rawCwd, ok := configuration["cwd"].(string); ok && rawCwd != "" {
		if filepath.IsAbs(rawCwd) {
			childCwd = rawCwd
		} else {
			childCwd = filepath.Join(parent.cwd, rawCwd)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	client, err := m.connectFn(ctx, ConnectOptions{
		Adapter: parent.adapter,
		Cwd:     childCwd,
		Host:    "127.0.0.1",
		Port:    parent.port,
	})
	if err != nil {
		return err
	}
	prog, _ := configuration["program"].(string)
	child := m.registerSession(client, parent.adapter, childCwd, prog, parent.id)

	fail := func(cause error) error {
		m.disposeSession(child)
		return cause
	}

	caps, err := client.Initialize(ctx, buildInitializeArguments(parent.adapter), timeout)
	if err != nil {
		return fail(err)
	}
	m.mu.Lock()
	child.capabilities = caps
	child.needsConfigurationDone = caps != nil && caps.SupportsConfigurationDoneRequest
	m.mu.Unlock()

	cfgCopy := maps.Clone(configuration)
	if cfgCopy == nil {
		cfgCopy = map[string]any{}
	}
	cfgCopy["cwd"] = childCwd

	sr := &startRequestResult{done: make(chan struct{})}
	startCall, startErr := client.StartRequest(ctx, request, cfgCopy, timeout)
	if startErr != nil {
		return fail(startErr)
	}
	go func() {
		_, sr.err = startCall.Await(ctx)
		close(sr.done)
	}()

	if err := m.completeConfigurationHandshake(ctx, child, sr, timeout); err != nil {
		return fail(throwPreferredDapStartError(request, sr, err))
	}
	<-sr.done
	if sr.err != nil {
		return fail(sr.err)
	}
	return nil
}

func (m *DapSessionManager) applyRootBreakpointsToSession(ctx context.Context, session *dapSession, timeout time.Duration) {
	session.bpMu.Lock()
	defer session.bpMu.Unlock()
	m.mu.Lock()
	root := m.getRootSessionLocked(session)
	srcBPs := make(map[string][]DapBreakpointRecord, len(root.breakpoints))
	for k, v := range root.breakpoints {
		if _, already := session.breakpoints[k]; !already {
			srcBPs[k] = slices.Clone(v)
		}
	}
	fnBPs := slices.Clone(root.functionBreakpoints)
	instrBPs := slices.Clone(root.instructionBreakpoints)
	dataBPs := slices.Clone(root.dataBreakpoints)
	m.mu.Unlock()
	for sourcePath, entries := range srcBPs {
		reqBPs := make([]DapSourceBreakpoint, len(entries))
		for i, e := range entries {
			reqBPs[i] = DapSourceBreakpoint{Line: e.Line, Condition: e.Condition}
		}
		raw, err := session.client.SendRequest(ctx, "setBreakpoints", map[string]any{
			"source":      DapSource{Path: sourcePath, Name: filepath.Base(sourcePath)},
			"breakpoints": reqBPs,
		}, timeout)
		if err == nil {
			var resp struct {
				Breakpoints []DapBreakpoint `json:"breakpoints"`
			}
			_ = json.Unmarshal(raw, &resp)
			m.mu.Lock()
			session.breakpoints[sourcePath] = mapSourceBreakpoints(entries, resp.Breakpoints)
			m.mu.Unlock()
		}
	}
	if len(fnBPs) > 0 {
		reqBPs := make([]DapFunctionBreakpoint, len(fnBPs))
		for i, e := range fnBPs {
			reqBPs[i] = DapFunctionBreakpoint{Name: e.Name, Condition: e.Condition}
		}
		raw, err := session.client.SendRequest(ctx, "setFunctionBreakpoints", map[string]any{"breakpoints": reqBPs}, timeout)
		if err == nil {
			var resp struct {
				Breakpoints []DapBreakpoint `json:"breakpoints"`
			}
			_ = json.Unmarshal(raw, &resp)
			m.mu.Lock()
			session.functionBreakpoints = mapFunctionBreakpoints(fnBPs, resp.Breakpoints)
			m.mu.Unlock()
		}
	}
	if len(instrBPs) > 0 {
		if _, err := session.client.SendRequest(ctx, "setInstructionBreakpoints", map[string]any{"breakpoints": instrBPs}, timeout); err == nil {
			m.mu.Lock()
			session.instructionBreakpoints = slices.Clone(instrBPs)
			m.mu.Unlock()
		}
	}
	if len(dataBPs) > 0 {
		if _, err := session.client.SendRequest(ctx, "setDataBreakpoints", map[string]any{"breakpoints": dataBPs}, timeout); err == nil {
			m.mu.Lock()
			session.dataBreakpoints = slices.Clone(dataBPs)
			m.mu.Unlock()
		}
	}
}

func (m *DapSessionManager) ensureLaunchSlot() error {
	m.mu.Lock()
	var dead []*dapSession
	for _, s := range m.sessions {
		if s.status == StatusTerminated || !s.client.IsAlive() {
			dead = append(dead, s)
		}
	}
	m.mu.Unlock()
	for _, s := range dead {
		m.disposeSession(s)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sessions {
		if s.parentSessionID == "" {
			return fmt.Errorf("Debug session %s is still active. Terminate it before launching another.", s.id)
		}
	}
	return nil
}

func standardizeSource(src *DapSource) {
	if src != nil && src.Path != "" {
		src.Path = dirs.StandardizeMacOSPath(src.Path)
	}
}

func standardizeSourceCopy(src *DapSource) *DapSource {
	if src == nil {
		return nil
	}
	cp := *src
	if cp.Path != "" {
		cp.Path = dirs.StandardizeMacOSPath(cp.Path)
	}
	return &cp
}

func (m *DapSessionManager) registerSession(client dapClient, adapter DapResolvedAdapter, cwd, program, parentID string) *dapSession {
	m.mu.Lock()
	m.nextID++
	now := time.Now()
	s := &dapSession{
		id:              fmt.Sprintf("debug-%d", m.nextID),
		seqNum:          m.nextID,
		adapter:         adapter,
		cwd:             cwd,
		program:         program,
		client:          client,
		status:          StatusLaunching,
		launchedAt:      now,
		lastUsedAt:      now,
		breakpoints:     make(map[string][]DapBreakpointRecord),
		initializedCh:   make(chan struct{}),
		parentSessionID: parentID,
		childSessionIDs: make(map[string]struct{}),
		port:            client.Port(),
	}
	m.sessions[s.id] = s
	if parentID != "" {
		if p, ok := m.sessions[parentID]; ok {
			p.childSessionIDs[s.id] = struct{}{}
		}
	}
	// Focus follows stops, not registrations: a lazily-attached child must not
	// steal focus from a sibling already stopped at a breakpoint/entry.
	if !m.hasLiveStoppedActiveSessionLocked() {
		m.activeSessionID = s.id
	}
	m.mu.Unlock()

	client.OnReverseRequest("runInTerminal", func(rawArgs json.RawMessage) (any, error) {
		var args DapRunInTerminalArguments
		if len(rawArgs) > 0 {
			_ = json.Unmarshal(rawArgs, &args)
		}
		if len(args.Args) == 0 {
			return nil, errors.New("runInTerminal request did not include a command")
		}
		extra := make(map[string]string)
		for k, v := range args.Env {
			if v != nil {
				extra[k] = *v
			}
		}
		termCwd := s.cwd
		if args.Cwd != "" {
			if filepath.IsAbs(args.Cwd) {
				termCwd = args.Cwd
			} else {
				termCwd = filepath.Join(s.cwd, args.Cwd)
			}
		}
		pid, stdout, proc, err := m.spawnTermFn(args.Args, termCwd, buildAdapterEnv(extra))
		if err != nil {
			return nil, err
		}
		if proc != nil {
			m.mu.Lock()
			s.terminalProcs = append(s.terminalProcs, proc)
			m.mu.Unlock()
		}
		if stdout != nil {
			go m.drainTerminalStdout(stdout, s)
		}
		return DapRunInTerminalResponse{ProcessID: pid}, nil
	})

	client.OnReverseRequest("startDebugging", func(rawArgs json.RawMessage) (any, error) {
		var args DapStartDebuggingArguments
		if len(rawArgs) > 0 {
			_ = json.Unmarshal(rawArgs, &args)
		}
		req := "launch"
		if args.Request == "attach" {
			req = "attach"
		}
		if err := m.startChildSession(s, req, args.Configuration, defaultRequestTimeout); err != nil {
			return nil, err
		}
		return map[string]any{}, nil
	})

	client.OnEvent("output", func(body json.RawMessage, _ DapEventMessage) {
		var ev DapOutputEventBody
		if len(body) > 0 {
			_ = json.Unmarshal(body, &ev)
		}
		m.mu.Lock()
		truncateOutputLocked(s, ev.Output)
		m.mu.Unlock()
	})

	client.OnEvent("process", func(body json.RawMessage, _ DapEventMessage) {
		var ev struct {
			SystemProcessID int `json:"systemProcessId"`
		}
		if len(body) > 0 && json.Unmarshal(body, &ev) == nil && ev.SystemProcessID > 0 {
			m.mu.Lock()
			s.debuggeePID = ev.SystemProcessID
			m.mu.Unlock()
		}
	})
	client.OnEvent("initialized", func(_ json.RawMessage, _ DapEventMessage) {
		m.mu.Lock()
		s.initializedSeen = true
		if !s.configurationDoneSent && s.status == StatusLaunching {
			s.status = StatusConfiguring
		}
		m.mu.Unlock()
		s.initializedOnce.Do(func() { close(s.initializedCh) })
	})

	client.OnEvent("stopped", func(body json.RawMessage, _ DapEventMessage) {
		var ev DapStoppedEventBody
		if len(body) > 0 {
			_ = json.Unmarshal(body, &ev)
		}
		m.mu.Lock()
		s.status = StatusStopped
		s.stop = DapStopLocation{
			ThreadID:    ev.ThreadID,
			Reason:      ev.Reason,
			Description: ev.Description,
			Text:        ev.Text,
		}
		s.lastStackFrames = nil
		m.activeSessionID = s.id
		m.resolveTreeOutcomeLocked(s)
		m.mu.Unlock()
	})

	client.OnEvent("continued", func(body json.RawMessage, _ DapEventMessage) {
		var ev DapContinuedEventBody
		if len(body) > 0 {
			_ = json.Unmarshal(body, &ev)
		}
		m.mu.Lock()
		s.status = StatusRunning
		s.stop = DapStopLocation{ThreadID: ev.ThreadID}
		s.lastStackFrames = nil
		m.mu.Unlock()
	})

	client.OnEvent("exited", func(body json.RawMessage, _ DapEventMessage) {
		var ev DapExitedEventBody
		if len(body) > 0 {
			_ = json.Unmarshal(body, &ev)
		}
		m.mu.Lock()
		s.exitCode = ev.ExitCode
		s.status = StatusTerminated
		m.reactivateAfterTerminationLocked(s)
		m.resolveTreeOutcomeLocked(s)
		m.mu.Unlock()
	})

	client.OnEvent("terminated", func(_ json.RawMessage, _ DapEventMessage) {
		m.mu.Lock()
		s.status = StatusTerminated
		m.reactivateAfterTerminationLocked(s)
		m.resolveTreeOutcomeLocked(s)
		m.mu.Unlock()
	})

	go func() {
		<-client.Exited()
		m.mu.Lock()
		s.status = StatusTerminated
		m.reactivateAfterTerminationLocked(s)
		m.resolveTreeOutcomeLocked(s)
		m.mu.Unlock()
	}()

	return s
}

func (m *DapSessionManager) drainTerminalStdout(rc io.ReadCloser, session *dapSession) {
	defer func() { _ = rc.Close() }()
	buf := make([]byte, 16*1024)
	for {
		n, err := rc.Read(buf)
		if n > 0 {
			chunk := string(buf[:n])
			m.mu.Lock()
			truncateOutputLocked(session, chunk)
			m.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func buildInitializeArguments(adapter DapResolvedAdapter) DapInitializeArguments {
	return DapInitializeArguments{
		ClientID:                      "omp",
		ClientName:                    "omp",
		AdapterID:                     adapter.Name,
		Locale:                        "en-US",
		LinesStartAt1:                 true,
		ColumnsStartAt1:               true,
		PathFormat:                    "path",
		SupportsRunInTerminalRequest:  true,
		SupportsStartDebuggingRequest: true,
		SupportsMemoryReferences:      true,
		SupportsVariableType:          true,
		SupportsInvalidatedEvent:      true,
	}
}

func (m *DapSessionManager) completeConfigurationHandshake(ctx context.Context, session *dapSession, startReq *startRequestResult, timeout time.Duration) error {
	m.mu.Lock()
	if session.configurationDoneSent {
		m.mu.Unlock()
		return nil
	}
	needsConfig := session.needsConfigurationDone
	hasParent := session.parentSessionID != ""
	initCh := session.initializedCh
	m.mu.Unlock()

	if !needsConfig {
		if hasParent {
			m.applyRootBreakpointsToSession(ctx, session, timeout)
		}
		m.mu.Lock()
		if session.status == StatusLaunching || session.status == StatusConfiguring {
			session.status = StatusRunning
		}
		m.mu.Unlock()
		return nil
	}

	// Wait on the per-session initializedCh created at registerSession (before initialize)
	// so an initialized event can never be missed in a check-then-subscribe race.
	// Also wake immediately if the pending launch/attach request failed before
	// the adapter emitted `initialized` (e.g. Delve build/launch error where
	// dlv answers launch with success=false and never sends initialized).
	// Check initCh first: if `initialized` already arrived (e.g. right after
	// `initialize`), proceed straight to `configurationDone` even if `startDone`
	// has also settled, preserving omp's combined start+configurationDone errors.
	select {
	case <-initCh:
	default:
		var startDone <-chan struct{}
		if startReq != nil {
			startDone = startReq.done
		}
		timer := time.NewTimer(timeout)
		select {
		case <-initCh:
			timer.Stop()
		case <-startDone:
			timer.Stop()
			// Re-check initCh in case both became ready concurrently.
			select {
			case <-initCh:
			default:
				if startReq.err != nil {
					return nil
				}
				// Launch/attach succeeded without initialized having arrived yet; wait for initialized.
				timer2 := time.NewTimer(timeout)
				select {
				case <-initCh:
					timer2.Stop()
				case <-session.client.Exited():
					timer2.Stop()
					return nil
				case <-timer2.C:
					return nil
				case <-ctx.Done():
					timer2.Stop()
					return mapContextError(ctx.Err())
				}
			}
		case <-session.client.Exited():
			timer.Stop()
			// Adapter exited before sending initialized; let launch/attach surface the real error.
			return nil
		case <-timer.C:
			// Adapter did not send initialized within timeout; let launch/attach surface the real error.
			return nil
		case <-ctx.Done():
			timer.Stop()
			return mapContextError(ctx.Err())
		}
	}
	if hasParent {
		m.applyRootBreakpointsToSession(ctx, session, timeout)
	}
	if _, err := session.client.SendRequest(ctx, "configurationDone", map[string]any{}, timeout); err != nil {
		return err
	}
	m.mu.Lock()
	session.configurationDoneSent = true
	if session.status == StatusConfiguring {
		session.status = StatusRunning
	}
	m.mu.Unlock()
	return nil
}

func applyTopFrameLocked(session *dapSession, frame *DapStackFrame) {
	if frame == nil {
		return
	}
	id := frame.ID
	line := frame.Line
	col := frame.Column
	session.stop.FrameID = &id
	session.stop.FrameName = frame.Name
	session.stop.InstructionPointerReference = frame.InstructionPointerReference
	session.stop.Source = frame.Source
	session.stop.Line = &line
	session.stop.Column = &col
}

func (m *DapSessionManager) fetchTopFrame(ctx context.Context, session *dapSession, timeout time.Duration) {
	m.mu.Lock()
	tid := session.stop.ThreadID
	m.mu.Unlock()
	if tid == nil {
		return
	}
	raw, err := session.client.SendRequest(ctx, "stackTrace", map[string]any{
		"threadId": *tid,
		"levels":   1,
	}, timeout)
	if err != nil {
		return
	}
	var resp struct {
		StackFrames []DapStackFrame `json:"stackFrames"`
	}
	if len(raw) > 0 && string(raw) != "null" {
		_ = json.Unmarshal(raw, &resp)
	}
	for i := range resp.StackFrames {
		standardizeSource(resp.StackFrames[i].Source)
	}
	m.mu.Lock()
	session.lastStackFrames = resp.StackFrames
	if len(resp.StackFrames) > 0 {
		applyTopFrameLocked(session, &resp.StackFrames[0])
	}
	m.mu.Unlock()
}

func (m *DapSessionManager) prepareStopOutcome(session *dapSession) *treeOutcomeWaiter {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextWaiterID++
	w := &treeOutcomeWaiter{
		id:            m.nextWaiterID,
		rootSessionID: m.getRootSessionLocked(session).id,
		ch:            make(chan struct{}),
	}
	m.treeWaiters[w.id] = w
	return w
}

func (m *DapSessionManager) cancelStopOutcome(w *treeOutcomeWaiter) {
	m.mu.Lock()
	delete(m.treeWaiters, w.id)
	m.mu.Unlock()
}

func (m *DapSessionManager) awaitStopOutcome(ctx context.Context, session *dapSession, w *treeOutcomeWaiter, timeout time.Duration) (DapContinueOutcome, error) {
	defer m.cancelStopOutcome(w)
	// Single deadline source: ctx carries cancellation only (user abort), while
	// timer carries the per-call stop-wait timeout. On timeout we return the
	// "running / timedOut=true" outcome cleanly instead of racing a context
	// deadline against the adapter round trip.
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-w.ch:
		m.mu.Lock()
		active := m.getActiveSessionLocked()
		resultSession := session
		if active != nil && m.getRootSessionLocked(active).id == m.getRootSessionLocked(session).id {
			resultSession = active
		}
		isStopped := resultSession.status == StatusStopped
		m.mu.Unlock()
		if isStopped {
			m.fetchTopFrame(ctx, resultSession, min(timeout, stopCaptureTimeout))
		}
		m.mu.Lock()
		state := "running"
		if resultSession.status == StatusStopped {
			state = "stopped"
		} else if resultSession.status == StatusTerminated {
			state = "terminated"
		}
		sum := buildSummaryLocked(resultSession)
		m.mu.Unlock()
		return DapContinueOutcome{Snapshot: sum, State: state, TimedOut: false}, nil
	case <-timer.C:
		m.mu.Lock()
		active := m.getActiveSessionLocked()
		resultSession := session
		if active != nil && m.getRootSessionLocked(active).id == m.getRootSessionLocked(session).id {
			resultSession = active
		}
		sum := buildSummaryLocked(resultSession)
		timedOut := resultSession.status == StatusRunning
		m.mu.Unlock()
		return DapContinueOutcome{Snapshot: sum, State: "running", TimedOut: timedOut}, nil
	case <-ctx.Done():
		return DapContinueOutcome{}, mapContextError(ctx.Err())
	}
}

func (m *DapSessionManager) resolveThreadID(ctx context.Context, session *dapSession, timeout time.Duration) (int, error) {
	m.mu.Lock()
	if session.stop.ThreadID != nil {
		id := *session.stop.ThreadID
		m.mu.Unlock()
		return id, nil
	}
	if len(session.threads) > 0 {
		id := session.threads[0].ID
		m.mu.Unlock()
		return id, nil
	}
	m.mu.Unlock()

	raw, err := session.client.SendRequest(ctx, "threads", nil, timeout)
	if err != nil {
		return 0, err
	}
	var resp struct {
		Threads []DapThread `json:"threads"`
	}
	if len(raw) > 0 && string(raw) != "null" {
		_ = json.Unmarshal(raw, &resp)
	}
	m.mu.Lock()
	session.threads = resp.Threads
	m.mu.Unlock()
	if len(resp.Threads) == 0 {
		return 0, errors.New("Debugger reported no threads.")
	}
	return resp.Threads[0].ID, nil
}

func (m *DapSessionManager) sendRequestWithConfig(ctx context.Context, session *dapSession, command string, args any, timeout time.Duration) (json.RawMessage, error) {
	if err := m.ensureConfigurationDone(ctx, session, timeout); err != nil {
		return nil, err
	}
	body, err := session.client.SendRequest(ctx, command, args, timeout)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.touchSessionAndAncestorsLocked(session)
	m.mu.Unlock()
	return body, nil
}

func (m *DapSessionManager) ensureConfigurationDone(ctx context.Context, session *dapSession, timeout time.Duration) error {
	m.mu.Lock()
	if !session.needsConfigurationDone || session.configurationDoneSent {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()
	if _, err := session.client.SendRequest(ctx, "configurationDone", map[string]any{}, timeout); err != nil {
		return err
	}
	m.mu.Lock()
	session.configurationDoneSent = true
	if session.status == StatusConfiguring {
		session.status = StatusRunning
	}
	m.mu.Unlock()
	return nil
}

func mapSourceBreakpoints(input []DapBreakpointRecord, resp []DapBreakpoint) []DapBreakpointRecord {
	out := make([]DapBreakpointRecord, len(input))
	for i, entry := range input {
		rec := DapBreakpointRecord{
			Line:      entry.Line,
			Condition: entry.Condition,
		}
		if i < len(resp) {
			rec.ID = resp[i].ID
			rec.Verified = resp[i].Verified
			rec.Message = resp[i].Message
		}
		out[i] = rec
	}
	return out
}

func mapFunctionBreakpoints(input []DapFunctionBreakpointRecord, resp []DapBreakpoint) []DapFunctionBreakpointRecord {
	out := make([]DapFunctionBreakpointRecord, len(input))
	for i, entry := range input {
		rec := DapFunctionBreakpointRecord{
			Name:      entry.Name,
			Condition: entry.Condition,
		}
		if i < len(resp) {
			rec.ID = resp[i].ID
			rec.Verified = resp[i].Verified
			rec.Message = resp[i].Message
		}
		out[i] = rec
	}
	return out
}

func mapInstructionBreakpoints(input []DapInstructionBreakpoint, resp []DapBreakpoint) []DapInstructionBreakpointRecord {
	out := make([]DapInstructionBreakpointRecord, len(input))
	for i, entry := range input {
		rec := DapInstructionBreakpointRecord{
			InstructionReference: entry.InstructionReference,
			Offset:               entry.Offset,
			Condition:            entry.Condition,
			HitCondition:         entry.HitCondition,
		}
		if i < len(resp) {
			if resp[i].InstructionReference != "" {
				rec.InstructionReference = resp[i].InstructionReference
			}
			if resp[i].Offset != nil {
				rec.Offset = resp[i].Offset
			}
			rec.ID = resp[i].ID
			rec.Verified = resp[i].Verified
			rec.Message = resp[i].Message
		}
		out[i] = rec
	}
	return out
}

func mapDataBreakpoints(input []DapDataBreakpoint, resp []DapBreakpoint) []DapDataBreakpointRecord {
	out := make([]DapDataBreakpointRecord, len(input))
	for i, entry := range input {
		rec := DapDataBreakpointRecord{
			DataID:       entry.DataID,
			AccessType:   entry.AccessType,
			Condition:    entry.Condition,
			HitCondition: entry.HitCondition,
		}
		if i < len(resp) {
			rec.ID = resp[i].ID
			rec.Verified = resp[i].Verified
			rec.Message = resp[i].Message
		}
		out[i] = rec
	}
	return out
}

func truncateOutputLocked(session *dapSession, output string) {
	if output == "" {
		return
	}
	bytesLen := len(output)
	session.outputChunks = append(session.outputChunks, output)
	session.outputBytes += bytesLen
	session.outputBufferedBytes += bytesLen
	for len(session.outputChunks) > 1 {
		frontBytes := len(session.outputChunks[0])
		if session.outputBufferedBytes-frontBytes < maxOutputBytes {
			break
		}
		session.outputChunks = session.outputChunks[1:]
		session.outputBufferedBytes -= frontBytes
		session.outputTruncated = true
	}
	if session.outputBufferedBytes > maxOutputBytes {
		front := session.outputChunks[0]
		excess := session.outputBufferedBytes - maxOutputBytes
		if excess < len(front) {
			kept := front[excess:]
			session.outputChunks[0] = kept
			session.outputBufferedBytes += len(kept) - len(front)
		} else {
			session.outputChunks[0] = ""
			session.outputBufferedBytes -= len(front)
		}
		session.outputTruncated = true
	}
}

func buildSummaryLocked(s *dapSession) DapSessionSummary {
	bpCount := 0
	for _, entries := range s.breakpoints {
		bpCount += len(entries)
	}
	stopDesc := s.stop.Description
	if stopDesc == "" {
		stopDesc = s.stop.Text
	}
	var children []string
	if len(s.childSessionIDs) > 0 {
		children = make([]string, 0, len(s.childSessionIDs))
		for id := range s.childSessionIDs {
			children = append(children, id)
		}
		sort.Strings(children)
	}
	return DapSessionSummary{
		ID:                          s.id,
		Adapter:                     s.adapter.Name,
		Cwd:                         dirs.StandardizeMacOSPath(s.cwd),
		Program:                     dirs.StandardizeMacOSPath(s.program),
		Status:                      s.status,
		LaunchedAt:                  s.launchedAt.UTC().Format(time.RFC3339Nano),
		LastUsedAt:                  s.lastUsedAt.UTC().Format(time.RFC3339Nano),
		ThreadID:                    s.stop.ThreadID,
		FrameID:                     s.stop.FrameID,
		StopReason:                  s.stop.Reason,
		StopDescription:             stopDesc,
		FrameName:                   s.stop.FrameName,
		InstructionPointerReference: s.stop.InstructionPointerReference,
		Source:                      standardizeSourceCopy(s.stop.Source),
		Line:                        s.stop.Line,
		Column:                      s.stop.Column,
		BreakpointFiles:             len(s.breakpoints),
		BreakpointCount:             bpCount,
		FunctionBreakpointCount:     len(s.functionBreakpoints),
		OutputBytes:                 s.outputBytes,
		OutputTruncated:             s.outputTruncated,
		ExitCode:                    s.exitCode,
		NeedsConfigurationDone:      s.needsConfigurationDone && !s.configurationDoneSent,
		ParentSessionID:             s.parentSessionID,
		ChildSessionIDs:             children,
	}
}

func (m *DapSessionManager) touchActiveSession() (*dapSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.getActiveSessionLocked()
	if s == nil {
		return nil, errors.New("No active debug session. Launch or attach first.")
	}
	m.touchSessionAndAncestorsLocked(s)
	if s.status != StatusTerminated && !s.client.IsAlive() {
		s.status = StatusTerminated
	}
	return s, nil
}

func (m *DapSessionManager) getActiveSessionLocked() *dapSession {
	if m.activeSessionID == "" {
		return nil
	}
	s, ok := m.sessions[m.activeSessionID]
	if !ok {
		m.activeSessionID = ""
		return nil
	}
	return s
}

func (m *DapSessionManager) hasLiveStoppedActiveSessionLocked() bool {
	a := m.getActiveSessionLocked()
	return a != nil && a.status == StatusStopped && a.client.IsAlive()
}

func (m *DapSessionManager) getRootSessionLocked(s *dapSession) *dapSession {
	root := s
	for root.parentSessionID != "" {
		p, ok := m.sessions[root.parentSessionID]
		if !ok {
			break
		}
		root = p
	}
	return root
}

func (m *DapSessionManager) getTreeSessionsLocked(s *dapSession) []*dapSession {
	var sessions []*dapSession
	pending := []*dapSession{m.getRootSessionLocked(s)}
	for len(pending) > 0 {
		cur := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		sessions = append(sessions, cur)
		childIDs := make([]string, 0, len(cur.childSessionIDs))
		for id := range cur.childSessionIDs {
			childIDs = append(childIDs, id)
		}
		sort.Strings(childIDs)
		for _, id := range childIDs {
			if ch, ok := m.sessions[id]; ok {
				pending = append(pending, ch)
			}
		}
	}
	return sessions
}

func (m *DapSessionManager) liveTreeSessionsLocked(s *dapSession) []*dapSession {
	var live []*dapSession
	for _, cand := range m.getTreeSessionsLocked(s) {
		if cand.status != StatusTerminated && cand.client.IsAlive() {
			live = append(live, cand)
		}
	}
	if len(live) > 0 {
		return live
	}
	return []*dapSession{s}
}

func (m *DapSessionManager) touchSessionAndAncestorsLocked(s *dapSession) {
	now := time.Now()
	cur := s
	for cur != nil {
		cur.lastUsedAt = now
		if cur.parentSessionID == "" {
			break
		}
		cur = m.sessions[cur.parentSessionID]
	}
}

func (m *DapSessionManager) reactivateAfterTerminationLocked(s *dapSession) {
	if m.activeSessionID != s.id {
		return
	}
	var live []*dapSession
	for _, cand := range m.getTreeSessionsLocked(s) {
		if cand.status != StatusTerminated && cand.client.IsAlive() {
			live = append(live, cand)
		}
	}
	if len(live) == 0 {
		return
	}
	var replacement *dapSession
	for _, cand := range live {
		if cand.status == StatusStopped {
			replacement = cand
			break
		}
	}
	if replacement == nil {
		for _, cand := range live {
			if cand.parentSessionID != "" {
				replacement = cand
				break
			}
		}
	}
	if replacement == nil {
		replacement = live[0]
	}
	m.activeSessionID = replacement.id
}

func (m *DapSessionManager) resolveTreeOutcomeLocked(s *dapSession) {
	rootID := m.getRootSessionLocked(s).id
	for _, w := range m.treeWaiters {
		if w.rootSessionID == rootID {
			w.resolve()
		}
	}
}

func (m *DapSessionManager) disposeSession(session *dapSession) {
	m.mu.Lock()
	if _, exists := m.sessions[session.id]; !exists {
		m.mu.Unlock()
		return
	}
	childIDs := make([]string, 0, len(session.childSessionIDs))
	for id := range session.childSessionIDs {
		childIDs = append(childIDs, id)
	}
	children := make([]*dapSession, 0, len(childIDs))
	for _, id := range childIDs {
		if ch, ok := m.sessions[id]; ok {
			children = append(children, ch)
		}
	}
	m.mu.Unlock()

	for _, ch := range children {
		m.disposeSession(ch)
	}

	m.mu.Lock()
	delete(m.sessions, session.id)
	if session.parentSessionID != "" {
		if p, ok := m.sessions[session.parentSessionID]; ok {
			delete(p.childSessionIDs, session.id)
		}
	}
	if m.activeSessionID == session.id {
		if session.parentSessionID != "" {
			if p, ok := m.sessions[session.parentSessionID]; ok {
				m.activeSessionID = p.id
			} else {
				m.activeSessionID = ""
			}
		} else {
			m.activeSessionID = ""
		}
		if m.activeSessionID == "" {
			for id := range m.sessions {
				m.activeSessionID = id
				break
			}
		}
	}
	termProcs := session.terminalProcs
	session.terminalProcs = nil
	m.mu.Unlock()

	for _, tp := range termProcs {
		if tp == nil || tp.cmd == nil || tp.cmd.Process == nil {
			continue
		}
		select {
		case <-tp.done:
			// Already reaped; never signal a stale PID that may have been reused.
			continue
		default:
		}
		killProcessTree(tp.cmd.Process)
	}
	_ = session.client.Dispose()
}
