package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/gate"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/intent"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/jobs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

// upstream: packages/coding-agent/src/tools/eval.ts,
//           packages/coding-agent/src/eval/kernel-session-registry.ts,
//           packages/coding-agent/src/eval/py/executor.ts,
//           packages/coding-agent/src/eval/js/executor.ts

const (
	// DefaultOutputMaxBytes caps inline eval output (matching omp's DEFAULT_MAX_BYTES = 50 KiB).
	DefaultOutputMaxBytes = 50 * 1024
	// liveUpdateInterval matches omp's LIVE_UPDATE_INTERVAL_MS (tools/eval.ts:181).
	liveUpdateInterval = 50 * time.Millisecond
)

// Setting retrieves an omp setting value for the active session (e.g.
// "eval.py", "eval.js", "eval.autoBackground.enabled",
// "eval.autoBackground.thresholdMs", "python.interpreter",
// "python.kernelMode", "tools.maxTimeout"). Assigned by the integrator from
// internal/settings.Any; when nil, readSessionSettings falls back to
// ctx.GetSettings() and env vars.
var Setting func(ctx sdk.Context, key string) any

// WriteSpill persists oversized eval output and returns an artifact URL or ID
// (e.g. "artifact://<id>" or "local://<N>.log"). Wired by the integrator in
// wiring.go to shell.WriteSessionSpill; when nil, oversized output truncates
// inline without creating an artifact file.
// upstream: packages/coding-agent/src/eval/executor-base.ts, packages/tui/src/tools/output-meta.ts
var WriteSpill func(ctx sdk.Context, label, content string) string

// SessionSettings holds resolved eval-related settings for one call.
// upstream: packages/coding-agent/src/eval/settings.ts:8-90
type SessionSettings struct {
	AllowPy                   bool
	AllowJS                   bool
	EvalToolsEnabled          bool
	AutoBackgroundEnabled     bool
	AutoBackgroundThresholdMs int
	PythonInterpreter         string
	PythonKernelMode          string // "session" | "per-call"
	MaxTimeout                float64
}

func clampAutoBackgroundThresholdMs(raw float64) int {
	if math.IsNaN(raw) || math.IsInf(raw, 0) {
		return DefaultAutoBackgroundThresholdMs
	}
	v := int(math.Floor(raw))
	if v < 0 {
		return 0
	}
	return v
}

func readSessionSettings(ctx sdk.Context) SessionSettings {
	s := SessionSettings{
		AllowPy:                   true,
		AllowJS:                   true,
		EvalToolsEnabled:          IsEvalToolsEnabled(ctx),
		AutoBackgroundEnabled:     false,
		AutoBackgroundThresholdMs: DefaultAutoBackgroundThresholdMs,
		PythonKernelMode:          "session",
	}
	if Setting != nil {
		if v, ok := settingAsBool(Setting(ctx, "eval.py")); ok {
			s.AllowPy = v
		}
		if v, ok := settingAsBool(Setting(ctx, "eval.js")); ok {
			s.AllowJS = v
		}
		if v, ok := settingAsBool(Setting(ctx, "eval.autoBackground.enabled")); ok {
			s.AutoBackgroundEnabled = v
		}
		if v, ok := settingAsFloat(Setting(ctx, "eval.autoBackground.thresholdMs")); ok {
			s.AutoBackgroundThresholdMs = clampAutoBackgroundThresholdMs(v)
		}
		if v, ok := settingAsString(Setting(ctx, "python.interpreter")); ok {
			s.PythonInterpreter = strings.TrimSpace(v)
		}
		if v, ok := settingAsString(Setting(ctx, "python.kernelMode")); ok && (v == "session" || v == "per-call") {
			s.PythonKernelMode = v
		}
		if v, ok := settingAsFloat(Setting(ctx, "tools.maxTimeout")); ok && v > 0 {
			s.MaxTimeout = v
		}
		return s
	}
	if v := strings.TrimSpace(os.Getenv("PI_PY")); v == "0" || strings.EqualFold(v, "false") {
		s.AllowPy = false
	}
	if v := strings.TrimSpace(os.Getenv("PI_JS")); v == "0" || strings.EqualFold(v, "false") {
		s.AllowJS = false
	}
	cfg, err := ctx.GetSettings()
	if err != nil || cfg == nil {
		return s
	}
	if v, ok := lookupBoolSetting(cfg, "eval.py"); ok && os.Getenv("PI_PY") == "" {
		s.AllowPy = v
	}
	if v, ok := lookupBoolSetting(cfg, "eval.js"); ok && os.Getenv("PI_JS") == "" {
		s.AllowJS = v
	}
	if v, ok := lookupBoolSetting(cfg, "eval.autoBackground.enabled"); ok {
		s.AutoBackgroundEnabled = v
	}
	if v, ok := lookupFloatSetting(cfg, "eval.autoBackground.thresholdMs"); ok {
		s.AutoBackgroundThresholdMs = clampAutoBackgroundThresholdMs(v)
	}
	if v, ok := lookupStringSetting(cfg, "python.interpreter"); ok {
		s.PythonInterpreter = strings.TrimSpace(v)
	}
	if v, ok := lookupStringSetting(cfg, "python.kernelMode"); ok && (v == "session" || v == "per-call") {
		s.PythonKernelMode = v
	}
	if v, ok := lookupFloatSetting(cfg, "tools.maxTimeout"); ok && v > 0 {
		s.MaxTimeout = v
	}
	return s
}

func settingAsBool(v any) (bool, bool) {
	b, ok := v.(bool)
	return b, ok
}

func settingAsString(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

func settingAsFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

func lookupSetting(cfg map[string]any, dotted string) (any, bool) {
	if v, ok := cfg[dotted]; ok {
		return v, true
	}
	parts := strings.Split(dotted, ".")
	var cur any = cfg
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[p]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func lookupBoolSetting(cfg map[string]any, key string) (bool, bool) {
	v, ok := lookupSetting(cfg, key)
	if !ok {
		return false, false
	}
	return settingAsBool(v)
}

func lookupStringSetting(cfg map[string]any, key string) (string, bool) {
	v, ok := lookupSetting(cfg, key)
	if !ok {
		return "", false
	}
	return settingAsString(v)
}

func lookupFloatSetting(cfg map[string]any, key string) (float64, bool) {
	v, ok := lookupSetting(cfg, key)
	if !ok {
		return 0, false
	}
	return settingAsFloat(v)
}

// Manager owns per-session Python and JS kernels and their lifecycle.
type Manager struct {
	mu                 sync.Mutex
	pySessions         map[string]*pySessionSlot
	jsSessions         map[string]*jsSessionSlot
	firstCallGuideSent map[string]map[string]bool
	evalJobs           *evalJobRegistry
	toolFallback       ToolExecutor

	// overrideTimeoutForTest lets unit tests use sub-second timeouts without changing the 1s schema clamp.
	overrideTimeoutForTest func(timeoutSec float64) time.Duration
}

type pySessionSlot struct {
	mu          sync.Mutex // serializes cell execution & lifecycle mutations
	kernelMu    sync.RWMutex
	kernel      *pythonKernel
	everStarted bool
	wasDead     bool
	running     atomic.Bool
}

func (s *pySessionSlot) liveKernel() *pythonKernel {
	s.kernelMu.RLock()
	k := s.kernel
	s.kernelMu.RUnlock()
	return k
}

func (s *pySessionSlot) setKernel(k *pythonKernel) {
	s.kernelMu.Lock()
	s.kernel = k
	s.kernelMu.Unlock()
}

type jsSessionSlot struct {
	mu          sync.Mutex // serializes cell execution & lifecycle mutations
	kernelMu    sync.RWMutex
	kernel      *jsKernel
	everStarted bool
	wasDead     bool
	running     atomic.Bool
}

func (s *jsSessionSlot) liveKernel() *jsKernel {
	s.kernelMu.RLock()
	k := s.kernel
	s.kernelMu.RUnlock()
	return k
}

func (s *jsSessionSlot) setKernel(k *jsKernel) {
	s.kernelMu.Lock()
	s.kernel = k
	s.kernelMu.Unlock()
}

// NewManager creates an isolated eval kernel Manager (used by Register and tests).
func NewManager() *Manager {
	return &Manager{
		pySessions:         make(map[string]*pySessionSlot),
		jsSessions:         make(map[string]*jsSessionSlot),
		firstCallGuideSent: make(map[string]map[string]bool),
		evalJobs:           newEvalJobRegistry(),
	}
}

func (m *Manager) jobRegistry() *evalJobRegistry {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.evalJobs == nil {
		m.evalJobs = newEvalJobRegistry()
	}
	return m.evalJobs
}

func (m *Manager) pySlot(key string) *pySessionSlot {
	m.mu.Lock()
	defer m.mu.Unlock()
	slot, ok := m.pySessions[key]
	if !ok {
		slot = &pySessionSlot{}
		m.pySessions[key] = slot
	}
	return slot
}

func (m *Manager) jsSlot(key string) *jsSessionSlot {
	m.mu.Lock()
	defer m.mu.Unlock()
	slot, ok := m.jsSessions[key]
	if !ok {
		slot = &jsSessionSlot{}
		m.jsSessions[key] = slot
	}
	return slot
}

// isSessionLangRunning reports whether any slot for sessionID+lang is currently executing a cell.
func (m *Manager) isSessionLangRunning(sessionID, lang string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch lang {
	case "py":
		prefix := "python:" + sessionID + "\x00"
		for k, slot := range m.pySessions {
			if strings.HasPrefix(k, prefix) && slot.running.Load() {
				return true
			}
		}
	case "js":
		if slot, ok := m.jsSessions["js:"+sessionID]; ok && slot.running.Load() {
			return true
		}
	}
	return false
}

// ShutdownSession stops and removes ONLY the kernels belonging to sessionID.
// Empty sessionID is a no-op (never shuts down other sessions).
func (m *Manager) ShutdownSession(sessionID string) {
	if sessionID == "" {
		return
	}
	if reg := m.jobRegistry(); reg != nil {
		reg.cancelSession(sessionID)
	}
	prefixPy := "python:" + sessionID + "\x00"
	keyJS := "js:" + sessionID

	m.mu.Lock()
	var pyToStop []*pySessionSlot
	for k, slot := range m.pySessions {
		if strings.HasPrefix(k, prefixPy) {
			pyToStop = append(pyToStop, slot)
			delete(m.pySessions, k)
		}
	}
	var jsToStop []*jsSessionSlot
	if slot, ok := m.jsSessions[keyJS]; ok {
		jsToStop = append(jsToStop, slot)
		delete(m.jsSessions, keyJS)
	}
	delete(m.firstCallGuideSent, sessionID)
	m.mu.Unlock()
	for _, slot := range pyToStop {
		slot.mu.Lock()
		k := slot.liveKernel()
		slot.setKernel(nil)
		slot.mu.Unlock()
		if k != nil {
			k.shutdown()
		}
	}
	for _, slot := range jsToStop {
		slot.mu.Lock()
		k := slot.liveKernel()
		slot.setKernel(nil)
		slot.mu.Unlock()
		if k != nil {
			k.shutdown()
		}
	}
}

// ShutdownAll stops every live kernel managed by m (for process teardown and tests only).
func (m *Manager) ShutdownAll() {
	if reg := m.jobRegistry(); reg != nil {
		reg.cancelAll()
	}
	m.mu.Lock()
	var pyToStop []*pySessionSlot
	for k, slot := range m.pySessions {
		pyToStop = append(pyToStop, slot)
		delete(m.pySessions, k)
	}
	var jsToStop []*jsSessionSlot
	for k, slot := range m.jsSessions {
		jsToStop = append(jsToStop, slot)
		delete(m.jsSessions, k)
	}
	clear(m.firstCallGuideSent)
	m.mu.Unlock()
	for _, slot := range pyToStop {
		slot.mu.Lock()
		k := slot.liveKernel()
		slot.setKernel(nil)
		slot.mu.Unlock()
		if k != nil {
			k.shutdown()
		}
	}
	for _, slot := range jsToStop {
		slot.mu.Lock()
		k := slot.liveKernel()
		slot.setKernel(nil)
		slot.mu.Unlock()
		if k != nil {
			k.shutdown()
		}
	}
}

// sessionIdentity resolves the session ID and optional session file/artifacts dir.
// Never fabricates a shared "default" session id when the host returns none.
func sessionIdentity(ctx sdk.Context) (sessionID, sessionFile, artifactsDir string, err error) {
	id, idErr := ctx.SessionManager().GetSessionID()
	if idErr != nil || strings.TrimSpace(id) == "" {
		return "", "", "", errors.New("eval requires a session id")
	}
	sessionID = id
	if sf, sfErr := ctx.SessionManager().GetSessionFile(); sfErr == nil && sf != nil && *sf != "" {
		sessionFile = *sf
		if dot := strings.LastIndexByte(sessionFile, '.'); dot > 0 {
			artifactsDir = sessionFile[:dot]
		}
	}
	return sessionID, sessionFile, artifactsDir, nil
}

func resolveEvalLocalRoots(ctx sdk.Context, cwd string) map[string]string {
	roots := map[string]string{}
	if urls.CanHandle("local://") {
		uCtx := urls.Context{SDK: ctx, Cwd: cwd}
		if loc, ok, err := urls.Locate(uCtx, "local://", urls.LocateOptions{Directory: true, LocalOnly: true}); err == nil && ok && loc != "" {
			roots["local"] = loc
		}
	}
	return roots
}

func normalizeCwd(cwd string) string {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	if abs, err := filepath.Abs(cwd); err == nil {
		return abs
	}
	return cwd
}

// Execute runs one eval tool call.
// upstream: packages/coding-agent/src/tools/eval.ts (#execute, #runCells)
func (m *Manager) Execute(ctx sdk.Context, params map[string]any) (sdk.ToolResult, error) {
	langRaw, ok := params["language"].(string)
	if !ok || (langRaw != "py" && langRaw != "js") {
		return sdk.ToolResult{}, fmt.Errorf(`Validation failed for tool "eval": language must be "py" or "js"`)
	}
	code, ok := params["code"].(string)
	if !ok {
		return sdk.ToolResult{}, fmt.Errorf(`Validation failed for tool "eval": code must be a string`)
	}
	title, _ := params["title"].(string)
	reset, _ := params["reset"].(bool)

	var rawTimeout *float64
	if tv, exists := params["timeout"]; exists && tv != nil {
		switch n := tv.(type) {
		case float64:
			rawTimeout = &n
		case int:
			f := float64(n)
			rawTimeout = &f
		default:
			return sdk.ToolResult{}, fmt.Errorf(`Validation failed for tool "eval": timeout must be a number`)
		}
	}

	detailLabel := title
	if detailLabel == "" {
		detailLabel = langRaw
	}
	if err := gate.Check(ctx, gate.Request{Tool: "eval", Op: gate.OpExec, Detail: detailLabel}); err != nil {
		return sdk.ToolResult{}, err
	}

	sessionID, sessionFile, artifactsDir, err := sessionIdentity(ctx)
	if err != nil {
		return sdk.ToolResult{}, err
	}

	settings := readSessionSettings(ctx)
	cwd := normalizeCwd(ctx.Cwd())

	cellLanguage := LangPython
	if langRaw == "js" {
		cellLanguage = LangJS
	}

	// Timeout clamping & notice (upstream: tools/eval.ts:490-502).
	cellTimeoutSec := clampEvalTimeout(rawTimeout, settings.MaxTimeout)
	var notice string
	if rawTimeout != nil && cellTimeoutSec != *rawTimeout {
		notice = fmt.Sprintf(
			"Eval timeout clamped from %ss to %ss.",
			formatTimeoutNumber(*rawTimeout),
			formatTimeoutNumber(cellTimeoutSec),
		)
		_ = ctx.OnUpdate(sdk.ToolResult{
			Content: notice,
			Details: EvalToolDetails{
				Language:  cellLanguage,
				Languages: []EvalLanguage{cellLanguage},
				Notice:    notice,
			},
		})
	}

	var cellTimeout time.Duration
	if cellTimeoutSec > 0 {
		if m.overrideTimeoutForTest != nil {
			cellTimeout = m.overrideTimeoutForTest(cellTimeoutSec)
		} else {
			cellTimeout = time.Duration(cellTimeoutSec * float64(time.Second))
		}
	}

	// Build caller cancellation context from sdk.Context.Done().
	reqCtx, reqCancel := context.WithCancel(context.Background())
	defer reqCancel()
	if done := ctx.Done(); done != nil {
		go func() {
			select {
			case <-done:
				reqCancel()
			case <-reqCtx.Done():
			}
		}()
	}
	if reqCtx.Err() != nil {
		return sdk.ToolResult{}, errors.New("Eval cell aborted")
	}

	// Check backend allowance & availability.
	switch cellLanguage {
	case LangPython:
		if !settings.AllowPy {
			return sdk.ToolResult{}, errors.New("Python backend is disabled (PI_PY=0 or eval.py = false).")
		}
		probeCtx := reqCtx
		var probeCancel context.CancelFunc
		if cellTimeout > 0 {
			probeCtx, probeCancel = context.WithTimeout(reqCtx, cellTimeout)
		}
		avail := checkPythonAvailability(probeCtx, cwd, settings.PythonInterpreter)
		if probeCancel != nil {
			probeCancel()
		}
		if !avail.OK {
			remedy := `Python backend is unavailable in this session. Provide a working Python 3.8+ on PATH (or set python.interpreter) to use language: "py".`
			if settings.AllowJS {
				remedy = `Python backend is unavailable in this session. Pass language: "js", or provide a working Python 3.8+ on PATH (or set python.interpreter).`
			}
			if avail.Reason != "" {
				return sdk.ToolResult{}, fmt.Errorf("%s\n%s", remedy, avail.Reason)
			}
			return sdk.ToolResult{}, errors.New(remedy)
		}
	case LangJS:
		if !settings.AllowJS {
			return sdk.ToolResult{}, errors.New("JavaScript backend is disabled (PI_JS=0 or eval.js = false).")
		}
		avail := probeJSRuntime(reqCtx)
		if !avail.OK {
			remedy := `JavaScript backend is unavailable in this session. Provide a working Bun or Node.js 22+ runtime on PATH to use language: "js".`
			if settings.AllowPy {
				remedy = `JavaScript backend is unavailable in this session. Pass language: "py", or provide a working Bun or Node.js 22+ runtime on PATH.`
			}
			if avail.Reason != "" {
				return sdk.ToolResult{}, fmt.Errorf("%s\n%s", remedy, avail.Reason)
			}
			return sdk.ToolResult{}, errors.New(remedy)
		}
	}

	// Resolve %load / %bun / %pip / %environment.
	prepared, err := prepareEvalSource(langRaw, code, cwd, ctx)
	if err != nil {
		return sdk.ToolResult{}, err
	}
	if len(prepared.Packages) > 0 {
		return sdk.ToolResult{}, errors.New("%bun add package installation is not ported in oh-my-pig; install project dependencies with `bash`.")
	}

	bridge, err := ensureBridgeServer()
	if err != nil {
		return sdk.ToolResult{}, err
	}

	localRoots := resolveEvalLocalRoots(ctx, cwd)
	enabledPreludes := EnabledPreludes(ctx)

	type cellOutcome struct {
		result sdk.ToolResult
		err    error
	}

	runCell := func(
		cellBaseCtx context.Context,
		onProgress func(text string, det EvalToolDetails),
		onRawChunk func(chunk string),
		onRawUpdate func(partial any),
	) (sdk.ToolResult, error) {
		// Arm IdleTimeout watchdog lazily via armTimeout so queued cells
		// do not time out while waiting on slot.mu.
		var idleMu sync.Mutex
		var idle *IdleTimeout
		var timedOut atomic.Bool
		armTimeout := func(base context.Context) (context.Context, func()) {
			if cellTimeout <= 0 {
				return base, func() {}
			}
			it := NewIdleTimeout(base, cellTimeout)
			idleMu.Lock()
			idle = it
			idleMu.Unlock()
			return it.Context(), func() {
				if it.TimedOut() {
					timedOut.Store(true)
				}
				idleMu.Lock()
				if idle == it {
					idle = nil
				}
				idleMu.Unlock()
				it.Dispose()
			}
		}
		runCtx := cellBaseCtx

		cellResult := EvalCellResult{
			Index:    0,
			Title:    title,
			Code:     code,
			Language: cellLanguage,
			Status:   "running",
		}

		var statusMu sync.Mutex
		liveBuf := newBoundedOutputBuffer(DefaultOutputMaxBytes)
		var replies []string
		var images []EvalImageDetail
		var views []EvalViewEntry

		// Throttled live updates (upstream: tools/eval.ts:181, 837-855).
		// updateSendMu serializes in-flight callbacks against stopUpdates so no
		// live update can be emitted after the cell settles.
		var updateTimer *time.Timer
		var updatesStopped bool
		var updateSendMu sync.Mutex
		buildSnapshot := func() (string, EvalToolDetails) {
			text := liveBuf.TailText()
			cellCopy := cellResult
			cellCopy.Output = text
			if len(cellResult.StatusEvents) > 0 {
				cellCopy.StatusEvents = append([]EvalStatusEvent(nil), cellResult.StatusEvents...)
			}
			det := EvalToolDetails{
				Language:  cellLanguage,
				Languages: []EvalLanguage{cellLanguage},
				Cells:     []EvalCellResult{cellCopy},
				Notice:    notice,
			}
			if len(images) > 0 {
				det.Images = append([]EvalImageDetail(nil), images...)
			}
			if len(replies) > 0 {
				det.Replies = append([]string(nil), replies...)
			}
			fullText := text
			if notice != "" {
				fullText = notice + "\n\n" + text
			}
			return fullText, det
		}
		sendUpdate := func() {
			updateSendMu.Lock()
			defer updateSendMu.Unlock()
			statusMu.Lock()
			if updatesStopped {
				statusMu.Unlock()
				return
			}
			updateTimer = nil
			text, det := buildSnapshot()
			statusMu.Unlock()
			if onProgress != nil {
				onProgress(text, det)
			}
		}
		scheduleUpdate := func() {
			statusMu.Lock()
			if updatesStopped || updateTimer != nil {
				statusMu.Unlock()
				return
			}
			updateTimer = time.AfterFunc(liveUpdateInterval, sendUpdate)
			statusMu.Unlock()
		}
		stopUpdates := func() {
			updateSendMu.Lock()
			defer updateSendMu.Unlock()
			statusMu.Lock()
			updatesStopped = true
			if updateTimer != nil {
				updateTimer.Stop()
				updateTimer = nil
			}
			statusMu.Unlock()
		}
		defer stopUpdates()

		onUpdate := func(partial any) {
			updateSendMu.Lock()
			defer updateSendMu.Unlock()
			statusMu.Lock()
			stopped := updatesStopped
			statusMu.Unlock()
			if stopped {
				return
			}
			if onRawUpdate != nil {
				onRawUpdate(partial)
			}
		}
		emitStatus := func(ev EvalStatusEvent) {
			if ev == nil {
				return
			}
			op, _ := ev["op"].(string)
			if op == EvalTimeoutPauseOp {
				idleMu.Lock()
				it := idle
				idleMu.Unlock()
				if it != nil {
					it.Pause()
				}
				return
			}
			if op == EvalTimeoutResumeOp {
				idleMu.Lock()
				it := idle
				idleMu.Unlock()
				if it != nil {
					it.Resume()
				}
				return
			}
			statusMu.Lock()
			recordStatusEvent(&cellResult, ev)
			statusMu.Unlock()
			scheduleUpdate()
		}

		onChunk := func(chunk string) {
			liveBuf.Append(chunk)
			if onRawChunk != nil {
				onRawChunk(chunk)
			}
			scheduleUpdate()
		}

		onDisplay := func(d DisplayOutput) {
			switch d.Type {
			case "status":
				emitStatus(d.Status)
			case "image":
				if d.Image != nil {
					statusMu.Lock()
					images = append(images, *d.Image)
					statusMu.Unlock()
					scheduleUpdate()
				}
			case "view":
				statusMu.Lock()
				if len(views) < MaxViewsPerCell {
					if raw, err := json.Marshal(d.View); err == nil && len(raw) <= MaxViewJSONChars {
						txt := strings.TrimSpace(sanitizeText(d.Text))
						if txt != "" {
							views = append(views, EvalViewEntry{Text: txt, View: d.View})
						}
					}
				}
				statusMu.Unlock()
			}
		}
		// Collect preludes with FirstCallDocumentation called during this cell so we can inject
		// omp's hidden `<system-notice id="prelude-guide">` once per conversation after the cell runs.
		// upstream: packages/coding-agent/src/eval/preludes.ts:219, session/session-tools.ts:2070-2141
		var firstCallMu sync.Mutex
		var firstCallCalled []Prelude
		firstCallSeen := map[string]bool{}
		onFirstCallPrelude := func(p Prelude) {
			if strings.TrimSpace(p.FirstCallDocumentation) == "" {
				return
			}
			firstCallMu.Lock()
			if !firstCallSeen[p.Name] {
				firstCallSeen[p.Name] = true
				firstCallCalled = append(firstCallCalled, p)
			}
			firstCallMu.Unlock()
		}

		start := time.Now()
		var kRes kernelResult
		var execErr error
		var restartNotice string

		switch cellLanguage {
		case LangPython:
			kRes, restartNotice, execErr = m.runPythonCell(
				runCtx, ctx, cwd, sessionID, sessionFile, artifactsDir,
				settings, localRoots, bridge, enabledPreludes,
				prepared, reset, int(cellTimeout/time.Millisecond),
				armTimeout, emitStatus, onUpdate, onFirstCallPrelude, onChunk, onDisplay,
			)
		case LangJS:
			kRes, restartNotice, execErr = m.runJSCell(
				runCtx, ctx, cwd, sessionID,
				localRoots, bridge, enabledPreludes,
				prepared, reset, int(cellTimeout/time.Millisecond),
				armTimeout, emitStatus, onUpdate, onFirstCallPrelude, onChunk, onDisplay,
			)
		}
		stopUpdates()
		m.emitFirstCallPreludeGuides(ctx, sessionID, firstCallCalled)
		durationMs := time.Since(start).Milliseconds()

		if execErr != nil {
			return sdk.ToolResult{}, execErr
		}

		if timedOut.Load() {
			kRes.TimedOut = true
			kRes.Cancelled = true
		}

		outputText := kRes.Output
		if restartNotice != "" {
			if outputText != "" {
				outputText = restartNotice + "\n" + outputText
			} else {
				outputText = restartNotice
			}
		}
		if kRes.TimedOut {
			if cellLanguage == LangPython {
				// upstream: packages/coding-agent/src/eval/executor-base.ts:578-583 +
				// packages/tui/src/tools/streaming-output.ts:1655,1668 — sink.dump(annotation)
				// prefixes `[${annotation}]\n` before the accumulated body.
				annotation := formatPyTimeoutAnnotation(cellTimeout, kRes.KernelKilled)
				outputText = "[" + annotation + "]\n" + outputText
			} else {
				// upstream: packages/coding-agent/src/eval/js/executor.ts:227-234 —
				// executeJs pushes annotation into outputSink then calls dump() without notice.
				annotation := formatJSTimeoutAnnotation(cellTimeout)
				if outputText != "" && !strings.HasSuffix(outputText, "\n") {
					outputText += "\n"
				}
				outputText += annotation
			}
		} else if kRes.DiedMidExec {
			// upstream: packages/coding-agent/src/eval/executor-base.ts:580-583 +
			// packages/tui/src/tools/streaming-output.ts:1655,1668 — sink.dump(annotation)
			// prefixes `[${annotation}]\n` before the accumulated body.
			annotation := formatKernelDiedAnnotation()
			outputText = "[" + annotation + "]\n" + outputText
		}

		runtimeOutput := strings.TrimSpace(outputText)
		if prepared.Environment != "" && !kRes.Cancelled && kRes.ExitCode != nil && *kRes.ExitCode == 0 {
			if runtimeOutput != "" {
				runtimeOutput += "\n"
			}
			runtimeOutput += fmt.Sprintf("Eval environment: %s.", prepared.Environment)
		}

		var outputMeta *OutputMeta
		var truncNotice string
		if kRes.Truncation != nil {
			var spillRef string
			if WriteSpill != nil {
				fullContent := kRes.FullOutput
				if fullContent == "" {
					fullContent = outputText
				}
				label := string(cellLanguage)
				if cellResult.Title != "" {
					label = cellResult.Title
				}
				if ref := strings.TrimSpace(WriteSpill(ctx, label, fullContent)); ref != "" {
					spillRef = ref
					kRes.Truncation.ArtifactID = normalizeArtifactID(ref)
					if kRes.Truncation.TotalBytes > len(fullContent) {
						kRes.Truncation.ArtifactElidedBytes = kRes.Truncation.TotalBytes - len(fullContent)
					}
				}
			}
			outputMeta = &OutputMeta{Truncation: kRes.Truncation}
			truncNotice = formatTruncationNotice(kRes.Truncation, spillRef)
		}
		statusMu.Lock()
		cellResult.Output = runtimeOutput
		cellResult.DurationMs = &durationMs
		cellResult.ExitCode = kRes.ExitCode
		if len(views) > 0 {
			cellResult.Views = views
		}
		finalImages := append([]EvalImageDetail(nil), images...)
		statusMu.Unlock()

		sdkImages := make([]sdk.ImageContent, 0, len(finalImages))
		for _, img := range finalImages {
			sdkImages = append(sdkImages, sdk.ImageContent{
				Data:     img.Data,
				MimeType: img.MimeType,
			})
		}

		failed := kRes.Cancelled || (kRes.ExitCode != nil && *kRes.ExitCode != 0)
		if failed {
			cellResult.Status = "error"
			var finalText string
			if kRes.Cancelled {
				finalText = runtimeOutput
				if finalText == "" {
					finalText = "Command aborted"
				}
			} else {
				exitLine := fmt.Sprintf("Command exited with code %d", *kRes.ExitCode)
				if runtimeOutput != "" {
					finalText = runtimeOutput + "\n\n" + exitLine
				} else {
					finalText = exitLine
				}
			}
			if notice != "" {
				finalText = notice + "\n\n" + finalText
			}
			if truncNotice != "" {
				finalText += truncNotice
			}
			details := EvalToolDetails{
				Language:  cellLanguage,
				Languages: []EvalLanguage{cellLanguage},
				Cells:     []EvalCellResult{cellResult},
				Images:    finalImages,
				Replies:   replies,
				IsError:   true,
				Meta:      outputMeta,
				Notice:    notice,
			}
			return sdk.ToolResult{
				Content: finalText,
				Images:  sdkImages,
				Details: details,
				IsError: true,
			}, nil
		}

		cellResult.Status = "complete"
		finalText := runtimeOutput
		if finalText == "" {
			if len(sdkImages) > 0 {
				suffix := "s"
				if len(sdkImages) == 1 {
					suffix = ""
				}
				finalText = fmt.Sprintf("(%d image%s; no text output)", len(sdkImages), suffix)
			} else {
				finalText = "(no output)"
			}
		}
		if notice != "" {
			finalText = notice + "\n\n" + finalText
		}
		if truncNotice != "" {
			finalText += truncNotice
		}
		details := EvalToolDetails{
			Language:  cellLanguage,
			Languages: []EvalLanguage{cellLanguage},
			Cells:     []EvalCellResult{cellResult},
			Images:    finalImages,
			Replies:   replies,
			Meta:      outputMeta,
			Notice:    notice,
		}
		return sdk.ToolResult{
			Content: finalText,
			Images:  sdkImages,
			Details: details,
		}, nil
	}

	// Default path (`eval.autoBackground.enabled == false`) or at running-job cap:
	// run inline identically with zero job allocation or overhead.
	// upstream: packages/coding-agent/src/tools/eval.ts:571-576
	reg := m.jobRegistry()
	if !settings.AutoBackgroundEnabled || reg.atCapacityForSession(sessionID) {
		return runCell(
			reqCtx,
			func(text string, det EvalToolDetails) {
				_ = ctx.OnUpdate(sdk.ToolResult{Content: text, Details: det})
			},
			nil,
			func(partial any) {
				_ = ctx.OnUpdate(partial)
			},
		)
	}

	// Auto-background path (`eval.autoBackground.enabled == true`).
	// upstream: packages/coding-agent/src/tools/eval.ts:578-711
	var timeoutSecPtr *float64
	if cellTimeoutSec > 0 {
		sec := cellTimeoutSec
		timeoutSecPtr = &sec
	}
	waitMs := resolveAutoBackgroundWaitMs(settings.AutoBackgroundThresholdMs, timeoutSecPtr)
	startBackgrounded := waitMs == 0

	label := formatEvalJobLabel(title, code)
	intentStr := intent.For(ctx, ctx.ToolCallID())

	jobCtx, jobCancel := context.WithCancel(context.Background())
	job, err := reg.createJob(sessionID, label, intentStr, startBackgrounded, jobCancel)
	if err != nil {
		jobCancel()
		return runCell(
			reqCtx,
			func(text string, det EvalToolDetails) {
				_ = ctx.OnUpdate(sdk.ToolResult{Content: text, Details: det})
			},
			nil,
			func(partial any) {
				_ = ctx.OnUpdate(partial)
			},
		)
	}
	if startBackgrounded {
		jobs.EnsureDeliveryListener()
		jobs.BindSessionIfMissing(sessionID, ctx)
		jobs.Add(sessionID, job)
	}

	completionCh := make(chan cellOutcome, 1)
	go func() {
		defer jobCancel()
		defer func() {
			if r := recover(); r != nil {
				panicErr := fmt.Errorf("eval cell panicked: %v", r)
				completionCh <- cellOutcome{err: panicErr}
				bg, released := job.settle("failed", "", panicErr.Error(), nil)
				reg.onJobSettled(job, bg, released)
			}
		}()
		res, runErr := runCell(
			jobCtx,
			func(text string, det EvalToolDetails) {
				if job.isReleased() {
					return
				}
				detCopy := det
				if job.isBackgrounded() {
					detCopy.Replies = nil
					detCopy.Async = &EvalAsyncDetail{
						State: "running",
						JobID: job.ID(),
						Type:  "eval",
					}
				}
				job.setProgress(text, &detCopy)
				if !job.isReleased() {
					_ = ctx.OnUpdate(sdk.ToolResult{Content: text, Details: detCopy})
				}
			},
			func(chunk string) {
				job.pushWidgetChunk(chunk)
			},
			func(partial any) {
				if !job.isBackgrounded() && !job.isReleased() {
					_ = ctx.OnUpdate(partial)
				}
			},
		)

		var finalDet *EvalToolDetails
		if d, ok := res.Details.(EvalToolDetails); ok {
			cp := d
			finalDet = &cp
		}

		status := "completed"
		resultText := res.Content
		errText := ""
		if runErr != nil {
			errText = runErr.Error()
			if jobCtx.Err() != nil {
				status = "cancelled"
			} else {
				status = "failed"
			}
		} else if res.IsError {
			errText = res.Content
			if errText == "" {
				errText = "Eval cell failed"
			}
			if jobCtx.Err() != nil {
				status = "cancelled"
			} else {
				status = "failed"
			}
		}

		// Hand the full result to the foreground waiter before settling the job.
		completionCh <- cellOutcome{result: res, err: runErr}
		bg, released := job.settle(status, resultText, errText, finalDet)
		if bg && !released {
			frameText := resultText
			if status != "completed" && errText != "" {
				frameText = errText
			}
			if finalDet != nil {
				detFrame := *finalDet
				detFrame.Replies = nil
				detFrame.Async = &EvalAsyncDetail{
					State: status,
					JobID: job.ID(),
					Type:  "eval",
				}
				_ = ctx.OnUpdate(sdk.ToolResult{Content: frameText, Details: detFrame})
			} else {
				_ = ctx.OnUpdate(sdk.ToolResult{
					Content: frameText,
					Details: EvalToolDetails{
						Language:  cellLanguage,
						Languages: []EvalLanguage{cellLanguage},
						Async: &EvalAsyncDetail{
							State: status,
							JobID: job.ID(),
							Type:  "eval",
						},
					},
				})
			}
		}
		reg.onJobSettled(job, bg, released)
	}()

	if startBackgrounded {
		return buildEvalBackgroundStartResult(job.ID(), cellLanguage, title, code, notice, notice, nil, timeoutSecPtr), nil
	}

	timer := time.NewTimer(time.Duration(waitMs) * time.Millisecond)
	defer timer.Stop()

	select {
	case outcome := <-completionCh:
		reg.releaseForeground(job)
		if outcome.err != nil {
			return sdk.ToolResult{}, outcome.err
		}
		return outcome.result, nil
	case <-reqCtx.Done():
		job.Cancel()
		reg.releaseForeground(job)
		preview, _ := job.latestPreview()
		if strings.TrimSpace(preview) == "" {
			preview = "Eval cell aborted"
		}
		return sdk.ToolResult{}, errors.New(preview)
	case <-timer.C:
		// Race guard: if the cell completed right as the timer fired, return inline.
		select {
		case outcome := <-completionCh:
			reg.releaseForeground(job)
			if outcome.err != nil {
				return sdk.ToolResult{}, outcome.err
			}
			return outcome.result, nil
		default:
		}
		reg.promoteToBackground(ctx, job)
		preview, latestDet := job.latestPreview()
		return buildEvalBackgroundStartResult(job.ID(), cellLanguage, title, code, notice, preview, latestDet, timeoutSecPtr), nil
	}
}

func buildManagedPyEnv(sessionFile, artifactsDir, bridgeSession string, bridge *bridgeServer, localRoots map[string]string) (map[string]string, map[string]any) {
	strEnv := map[string]string{}
	patch := map[string]any{
		"PI_SESSION_FILE":        nil,
		"PI_ARTIFACTS_DIR":       nil,
		"PI_TOOL_BRIDGE_URL":     nil,
		"PI_TOOL_BRIDGE_TOKEN":   nil,
		"PI_TOOL_BRIDGE_SESSION": nil,
		"PI_EVAL_LOCAL_ROOTS":    nil,
	}
	if sessionFile != "" {
		strEnv["PI_SESSION_FILE"] = sessionFile
		patch["PI_SESSION_FILE"] = sessionFile
	}
	if artifactsDir != "" {
		strEnv["PI_ARTIFACTS_DIR"] = artifactsDir
		patch["PI_ARTIFACTS_DIR"] = artifactsDir
	}
	if bridge != nil {
		strEnv["PI_TOOL_BRIDGE_URL"] = bridge.url
		strEnv["PI_TOOL_BRIDGE_TOKEN"] = bridge.token
		strEnv["PI_TOOL_BRIDGE_SESSION"] = bridgeSession
		patch["PI_TOOL_BRIDGE_URL"] = bridge.url
		patch["PI_TOOL_BRIDGE_TOKEN"] = bridge.token
		patch["PI_TOOL_BRIDGE_SESSION"] = bridgeSession
	}
	if len(localRoots) > 0 {
		if raw, err := json.Marshal(localRoots); err == nil {
			strEnv["PI_EVAL_LOCAL_ROOTS"] = string(raw)
			patch["PI_EVAL_LOCAL_ROOTS"] = string(raw)
		}
	}
	return strEnv, patch
}

func (m *Manager) runPythonCell(
	runCtx context.Context,
	sdkCtx sdk.Context,
	cwd, sessionID, sessionFile, artifactsDir string,
	settings SessionSettings,
	localRoots map[string]string,
	bridge *bridgeServer,
	preludes []Prelude,
	prepared PreparedEvalSource,
	reset bool,
	cellTimeoutMs int,
	armTimeout func(context.Context) (context.Context, func()),
	emitStatus func(EvalStatusEvent),
	onUpdate func(any),
	onFirstCallPrelude func(Prelude),
	onChunk func(string),
	onDisplay func(DisplayOutput),
) (kernelResult, string, error) {
	bridgeSession := "python:" + sessionID
	strEnv, envPatch := buildManagedPyEnv(sessionFile, artifactsDir, bridgeSession, bridge, localRoots)

	if settings.PythonKernelMode == "per-call" {
		if runCtx.Err() != nil {
			return kernelResult{Cancelled: true}, "", nil
		}
		if armTimeout != nil {
			var disarm func()
			runCtx, disarm = armTimeout(runCtx)
			defer disarm()
		}
		k, err := startPythonKernel(runCtx, cwd, settings.PythonInterpreter, strEnv)
		if err != nil {
			return kernelResult{}, "", err
		}
		defer k.shutdown()
		if err := k.syncPreludes(runCtx, preludes); err != nil {
			return kernelResult{}, "", err
		}
		runID := "py-" + newRandomID()
		unreg := bridge.register(bridgeSession, runID, &bridgeRun{
			sdkCtx:        sdkCtx,
			ctx:           runCtx,
			cwd:           cwd,
			sessionID:     sessionID,
			language:      "py",
			cellTimeoutMs: cellTimeoutMs,
			emitStatus:    emitStatus,
			onUpdate:      onUpdate,
			toolFallback:  m.toolFallback,
		})
		defer unreg()
		res, err := k.execRaw(runCtx, runID, prepared.Code, cwd, prepared.Filename, envPatch, false, onChunk, onDisplay)
		return res, "", err
	}

	slotKey := bridgeSession + "\x00" + cwd + "\x00" + settings.PythonInterpreter
	slot := m.pySlot(slotKey)
	slot.mu.Lock()
	defer slot.mu.Unlock()
	slot.running.Store(true)
	defer slot.running.Store(false)

	// Do not start or execute a cell that was already cancelled while queued on slot.mu.
	if runCtx.Err() != nil {
		return kernelResult{Cancelled: true}, "", nil
	}
	if armTimeout != nil {
		var disarm func()
		runCtx, disarm = armTimeout(runCtx)
		defer disarm()
	}
	var restartNotice string
	if reset {
		if k := slot.liveKernel(); k != nil {
			slot.setKernel(nil)
			k.shutdown()
		}
		slot.wasDead = false
	} else if k := slot.liveKernel(); k != nil && !k.isAlive() {
		slot.setKernel(nil)
		k.shutdown()
		slot.wasDead = true
	}

	if slot.liveKernel() == nil {
		k, err := startPythonKernel(runCtx, cwd, settings.PythonInterpreter, strEnv)
		if err != nil {
			return kernelResult{}, "", err
		}
		if slot.everStarted && slot.wasDead && !reset {
			restartNotice = formatKernelRestartNotice("Python kernel")
		}
		slot.setKernel(k)
		slot.everStarted = true
		slot.wasDead = false
	}
	if err := slot.kernel.syncPreludes(runCtx, preludes); err != nil {
		return kernelResult{}, restartNotice, err
	}

	runID := "py-" + newRandomID()
	unreg := bridge.register(bridgeSession, runID, &bridgeRun{
		sdkCtx:             sdkCtx,
		ctx:                runCtx,
		cwd:                cwd,
		sessionID:          sessionID,
		language:           "py",
		cellTimeoutMs:      cellTimeoutMs,
		emitStatus:         emitStatus,
		onUpdate:           onUpdate,
		onFirstCallPrelude: onFirstCallPrelude,
		toolFallback:       m.toolFallback,
		isLangRunning:      func(lang string) bool { return m.isSessionLangRunning(sessionID, lang) },
	})
	defer unreg()

	res, err := slot.kernel.execRaw(runCtx, runID, prepared.Code, cwd, prepared.Filename, envPatch, false, onChunk, onDisplay)
	if !slot.kernel.isAlive() {
		slot.wasDead = true
	}
	return res, restartNotice, err
}

func (m *Manager) runJSCell(
	runCtx context.Context,
	sdkCtx sdk.Context,
	cwd, sessionID string,
	localRoots map[string]string,
	bridge *bridgeServer,
	preludes []Prelude,
	prepared PreparedEvalSource,
	reset bool,
	cellTimeoutMs int,
	armTimeout func(context.Context) (context.Context, func()),
	emitStatus func(EvalStatusEvent),
	onUpdate func(any),
	onFirstCallPrelude func(Prelude),
	onChunk func(string),
	onDisplay func(DisplayOutput),
) (kernelResult, string, error) {
	bridgeSession := "js:" + sessionID
	slot := m.jsSlot(bridgeSession)
	slot.mu.Lock()
	defer slot.mu.Unlock()
	slot.running.Store(true)
	defer slot.running.Store(false)

	if runCtx.Err() != nil {
		return kernelResult{Cancelled: true}, "", nil
	}
	if armTimeout != nil {
		var disarm func()
		runCtx, disarm = armTimeout(runCtx)
		defer disarm()
	}
	var restartNotice string
	if reset {
		if k := slot.liveKernel(); k != nil {
			slot.setKernel(nil)
			k.shutdown()
		}
		slot.wasDead = false
	} else if k := slot.liveKernel(); k != nil && !k.isAlive() {
		slot.setKernel(nil)
		k.shutdown()
		slot.wasDead = true
	}

	if slot.liveKernel() == nil {
		k, err := startJSKernel(runCtx, cwd, bridgeSession, localRoots, bridge)
		if err != nil {
			return kernelResult{}, "", err
		}
		if slot.everStarted && slot.wasDead && !reset {
			// upstream: packages/coding-agent/src/eval/js/context-manager.ts:229 ("JS worker")
			restartNotice = formatKernelRestartNotice("JS worker")
		}
		slot.setKernel(k)
		slot.everStarted = true
		slot.wasDead = false
	}
	runID := "js-" + newRandomID()
	filename := prepared.Filename
	if filename == "" {
		// upstream: packages/coding-agent/src/eval/js/executor.ts:133
		filename = "js-cell-" + newUUIDv4() + ".js"
	}

	unreg := bridge.register(bridgeSession, runID, &bridgeRun{
		sdkCtx:             sdkCtx,
		ctx:                runCtx,
		cwd:                cwd,
		sessionID:          sessionID,
		language:           "js",
		cellTimeoutMs:      cellTimeoutMs,
		emitStatus:         emitStatus,
		onUpdate:           onUpdate,
		onFirstCallPrelude: onFirstCallPrelude,
		toolFallback:       m.toolFallback,
		isLangRunning:      func(lang string) bool { return m.isSessionLangRunning(sessionID, lang) },
	})
	defer unreg()

	res, err := slot.kernel.exec(runCtx, runID, prepared.Code, filename, cwd, localRoots, bridge, bridgeSession, preludes, onChunk, onDisplay)
	if !slot.kernel.isAlive() {
		slot.wasDead = true
	}
	return res, restartNotice, err
}

// emitFirstCallPreludeGuides sends omp's hidden `eval-prelude-guide` custom message once per
// session for each prelude with FirstCallDocumentation invoked during the cell.
// upstream: packages/coding-agent/src/session/session-tools.ts:2088-2141,
//
//	packages/coding-agent/src/prompts/tools/eval-prelude-first-call.md
func (m *Manager) emitFirstCallPreludeGuides(ctx sdk.Context, sessionID string, called []Prelude) {
	if len(called) == 0 || sessionID == "" {
		return
	}
	m.mu.Lock()
	if m.firstCallGuideSent == nil {
		m.firstCallGuideSent = make(map[string]map[string]bool)
	}
	sent := m.firstCallGuideSent[sessionID]
	if sent == nil {
		sent = make(map[string]bool)
		m.firstCallGuideSent[sessionID] = sent
	}
	var toSend []Prelude
	for _, p := range called {
		if sent[p.Name] {
			continue
		}
		if strings.TrimSpace(p.FirstCallDocumentation) == "" {
			continue
		}
		sent[p.Name] = true
		toSend = append(toSend, p)
	}
	m.mu.Unlock()
	if len(toSend) == 0 {
		return
	}
	var b strings.Builder
	b.WriteString(`<system-notice id="prelude-guide">` + "\n")
	names := make([]string, 0, len(toSend))
	for _, p := range toSend {
		names = append(names, p.Name)
		fmt.Fprintf(&b, "`%s` guide (sent once per conversation; full reference at `help://eval/%s`):\n%s\n",
			p.Name, p.Name, strings.TrimSpace(p.FirstCallDocumentation))
	}
	b.WriteString("</system-notice>")
	trigger := false
	_ = ctx.SendMessage("eval-prelude-guide", b.String(), false, sdk.SendMessageOptions{
		TriggerTurn: &trigger,
		DeliverAs:   "steer",
		Details:     map[string]any{"names": names},
	})
}
