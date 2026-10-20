package warden

import (
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The wiring of the three agent tools (warden_remember, warden_loops, warden_recall), the standing-preference
// injection, the open-loop lifecycle and the compaction evidence appendix (extension.ts 470-530, 1059-1087,
// 2171-2210, 2278-2295, 2459-2517, 2734-2752). The pure logic is in prefs*.go, loops.go, recall.go and compact.go.

const (
	prefsType = "pi-warden-prefs"
	loopsType = "pi-warden-loops"
)

// toolsState is the session state of this slice. warden holds one; every field is reset at session start.
type toolsState struct {
	mu sync.Mutex
	// scan is the one scan of the earlier sessions per session start, shared by the injection and `/warden prefs`.
	scan *PrefsScan
	// loopsReminded is the open-loop list the last end-of-run reminder named: the same unchanged list is not named again.
	loopsReminded string
	// search is the recall tool probe of this session (see searchToolFor).
	search *searchProbe
	// ledger and holds feed the compaction snapshot and warden_recall. The context saver and the hold feedback store
	// set them; nil is an empty ledger and no holds.
	ledger outputLedger
	holds  holdRecords
}

func (w *warden) resetToolsState() {
	w.tools.mu.Lock()
	w.tools.scan = nil
	w.tools.loopsReminded = ""
	w.tools.search = nil
	w.tools.mu.Unlock()
}

// dataDir is warden's data folder, where the loops and the lesson files live.
func (w *warden) dataDir(ctx sdk.Context) (string, bool) {
	home, err := ctx.ConfigHome()
	if err != nil {
		return "", false
	}
	return DataDir(home), true
}

func (w *warden) nowMs() int64 { return w.opts.Now().UnixMilli() }

// standingPrefs is the scan of the earlier sessions, read once per session start.
func (w *warden) standingPrefs(ctx sdk.Context) (*PrefsScan, error) {
	w.tools.mu.Lock()
	defer w.tools.mu.Unlock()
	if w.tools.scan != nil {
		return w.tools.scan, nil
	}
	manager := ctx.SessionManager()
	dir, err := manager.GetSessionDir()
	if err != nil {
		return nil, err
	}
	var scan *PrefsScan
	if dir == "" {
		// An in-memory session has no directory: nothing earlier to learn from.
		scan = &PrefsScan{Counts: emptyWordCounts(), Project: ctx.Cwd()}
	} else {
		exclude := ""
		if file, err := manager.GetSessionFile(); err == nil && file != nil {
			exclude = *file
		}
		scan = scanPreferences(ScanOptions{Dir: dir, Exclude: exclude, Cwd: ctx.Cwd(), Now: w.nowMs(), Git: w.opts.Git})
	}
	w.tools.scan = scan
	return scan, nil
}

// evaluatedPrefs is the scan, the project's lesson file, and every listed item with its status; read fresh so
// `forget` numbers match.
func (w *warden) evaluatedPrefs(ctx sdk.Context) (*PrefsScan, string, []PrefItem, error) {
	scan, err := w.standingPrefs(ctx)
	if err != nil {
		return nil, "", nil, err
	}
	dir, ok := w.dataDir(ctx)
	if !ok {
		return nil, "", nil, errNoDataDir
	}
	path := prefsStorePath(scan.Project, dir)
	store, err := readPrefsStore(path)
	if err != nil {
		return nil, "", nil, err
	}
	return scan, path, evaluatePrefs(scan, store, w.nowMs()), nil
}

type toolsError string

func (e toolsError) Error() string { return string(e) }

const errNoDataDir = toolsError("the PiG config directory could not be found")

// loopsFile is the session's loops file, or "" when the host gives the session no id (nothing to key it by).
func (w *warden) loopsFile(ctx sdk.Context) string {
	id, err := ctx.SessionManager().GetSessionID()
	if err != nil || id == "" {
		return ""
	}
	dir, ok := w.dataDir(ctx)
	if !ok {
		return ""
	}
	return loopsPath(ctx.Cwd(), id, dir)
}

// branchHasMessage reports whether the branch carries a custom message of this type, and the last one's content.
func (w *warden) customMessages(ctx sdk.Context, customType string) []string {
	entries, err := ctx.SessionManager().GetBranch(nil)
	if err != nil {
		return nil
	}
	var found []string
	for _, entry := range entries {
		if entry["type"] == "custom_message" && entry["customType"] == customType {
			content, _ := entry["content"].(string)
			found = append(found, content)
		}
	}
	return found
}

// sendContext sends one custom message that starts no turn.
func (w *warden) sendContext(ctx sdk.Context, customType, content string, display bool) {
	no := false
	_ = ctx.SendMessage(customType, content, display, sdk.SendMessageOptions{TriggerTurn: &no})
}

// toolsSessionStart is the session-start part of this slice: a fresh state, the standing-preference message, and on
// resume the list of the loops still open. TOOLS-LANE HOOK: called once at the end of onSessionStart.
func (w *warden) toolsSessionStart(ctx sdk.Context, data map[string]any) {
	w.resetToolsState()
	cfg := w.config(ctx)
	w.startSearchProbe(cfg)
	// A resumed or reloaded session already carries the message; sending it again would repeat it in the context.
	if cfg.Enabled && cfg.Prefs.Enabled && cfg.Prefs.Inject && len(w.customMessages(ctx, prefsType)) == 0 {
		// A failed scan or an unreadable lesson file injects nothing. The original only warns on the console; a Go
		// extension has none, so the failure is not surfaced here (`/warden prefs` reports it).
		if _, _, items, err := w.evaluatedPrefs(ctx); err == nil {
			if message := prefsMessage(items); message != "" {
				// Not a steer; one message per session; do not spend a steer unit.
				w.sendContext(ctx, prefsType, message, cfg.SteerVisible)
			}
		}
	}
	if reason, _ := data["reason"].(string); cfg.Enabled && reason == "resume" {
		w.announceLoops(ctx, cfg)
	}
}

// announceLoops: on resume, one message lists the loops still open, so a session picked up later knows what it
// promised. Not a steer. A branch whose last loops message says the same already has it.
func (w *warden) announceLoops(ctx sdk.Context, cfg Config) {
	file := w.loopsFile(ctx)
	if file == "" {
		return
	}
	store, err := readLoops(file)
	if err != nil {
		// The original only warns on the console; a Go extension has none, so the failure is not surfaced.
		return
	}
	open := openLoops(store)
	if len(open) == 0 {
		return
	}
	content := "Open loops from earlier in this session (warden_loops):\n" + formatOpenLoops(open) + "\nClose each with warden_loops done or drop once it is finished or no longer needed."
	if existing := w.customMessages(ctx, loopsType); len(existing) > 0 && existing[len(existing)-1] == content {
		return
	}
	w.sendContext(ctx, loopsType, content, cfg.SteerVisible)
}

// toolsAgentEnd is the end-of-run reminder of the open loops. TOOLS-LANE HOOK: called once from onAgentEnd.
//
// One short reminder when a run ends, for the next turn: it starts no turn of its own, counts against the steer
// budget as `loops`, and names the same unchanged list only once.
func (w *warden) toolsAgentEnd(ctx sdk.Context, cfg Config) {
	file := w.loopsFile(ctx)
	if file == "" {
		return
	}
	store, err := readLoops(file)
	if err != nil {
		w.record(ctx, cfg, GuardAction, "loops reminder error", []string{"reading the loops file failed: " + err.Error()}, nil)
		return
	}
	open := openLoops(store)
	fingerprint := loopsFingerprint(open)
	w.tools.mu.Lock()
	same := fingerprint == w.tools.loopsReminded
	w.tools.mu.Unlock()
	if len(open) == 0 || same {
		return
	}
	plural := "s"
	if len(open) == 1 {
		plural = ""
	}
	notice := "pi-warden: " + itoaInt(len(open)) + " open loop" + plural + " you promised in this session:\n" + formatOpenLoops(open) +
		"\nClose each with warden_loops done or drop once it is finished or no longer needed."
	if w.steerLoops(ctx, cfg, notice) {
		w.tools.mu.Lock()
		w.tools.loopsReminded = fingerprint
		w.tools.mu.Unlock()
	}
}

// steerLoops sends the end-of-run reminder as a `loops` steer for the next turn, when the adaptive steering lets
// that kind through, and watches whether the agent follows it.
func (w *warden) steerLoops(ctx sdk.Context, cfg Config, notice string) bool {
	if !w.adaptiveSend(ctx, cfg, SteerKindLoops) || !w.steer(cfg, []SteerGuard{SteerGuardLoops}, ctx, notice, SteerOptions{DeliverAs: "nextTurn"}) {
		return false
	}
	w.watchSteer(ctx, cfg, []SteerKind{SteerKindLoops}, SteerSubject{}, true)
	return true
}

// evidenceSnapshot is the session's evidence as the compaction appendix and warden_recall both read it.
func (w *warden) evidenceSnapshot(ctx sdk.Context, openLoopsText string) CompactSnapshot {
	w.mu.Lock()
	evidence := w.evidence
	var checks []compactCheck
	for i, c := range evidence.Checks {
		checks = append(checks, compactCheck{Command: c.Call, Passed: c.Passed, RunIndex: 1, IndexInRun: i})
	}
	var attempts []compactAttempt
	if w.window != nil {
		for _, a := range w.window.Attempts {
			attempts = append(attempts, compactAttempt{Key: a.Key, Call: a.Call, Failed: a.Failed, Output: a.Output})
		}
	}
	snapshotEvidence := *evidence
	snapshotEvidence.Checks = append([]Check(nil), evidence.Checks...)
	w.mu.Unlock()
	w.tools.mu.Lock()
	ledger, holds := w.tools.ledger, w.tools.holds
	w.tools.mu.Unlock()
	task, _ := w.latestUserPrompt(ctx)
	in := snapshotInput{Checks: checks, Attempts: attempts, Evidence: &snapshotEvidence, ActiveTask: task, Runs: 1, OpenLoops: openLoopsText}
	if ledger != nil {
		in.SavedOutputs = ledger.storedOutputs()
	}
	if holds != nil {
		in.Holds = holds.heldRecords()
	}
	return buildCompactSnapshot(in)
}

// onSessionCompact is the compaction evidence appendix: after compaction succeeds, send the evidence warden holds as
// one custom message so the agent can prefer saved paths over re-running commands. Not a steer: one message per
// compaction, and no steer unit is spent. An unreadable loops file costs the appendix its loops section, not the
// evidence.
func (w *warden) onSessionCompact(ctx sdk.Context, _ map[string]any) (any, error) {
	cfg := w.config(ctx)
	if !cfg.Enabled || !cfg.Context.CompactAppendix {
		return nil, nil
	}
	loopsText := ""
	if file := w.loopsFile(ctx); file != "" {
		if store, err := readLoops(file); err != nil {
			w.record(ctx, cfg, GuardAction, "loops read error", []string{"reading the loops file failed: " + err.Error()}, nil)
		} else {
			loopsText = formatOpenLoops(openLoops(store))
		}
	}
	appendix := compactAppendix(w.evidenceSnapshot(ctx, loopsText))
	if appendix == "" {
		return nil, nil
	}
	w.sendContext(ctx, compactEvidenceType, appendix, cfg.SteerVisible)
	return nil, nil
}

// searchProbe is the one probe of this session's search tool, started at session start and read by whoever
// builds an excerpt footer. Detection never runs inside a tool_result handler.
type searchProbe struct {
	done chan struct{}
	tool SearchTool
}

// startSearchProbe starts the probe when a footer can be needed: warden and the context saver are on. A configured
// tool is not probed.
func (w *warden) startSearchProbe(cfg Config) {
	if !cfg.Enabled || !cfg.Context.Enabled || (cfg.Context.RecallTool != "auto" && cfg.Context.RecallTool != "") {
		return
	}
	probe := &searchProbe{done: make(chan struct{})}
	w.tools.mu.Lock()
	w.tools.search = probe
	w.tools.mu.Unlock()
	go func() {
		defer close(probe.done)
		probe.tool = detectSearchTool("auto", probeTimeout)
	}()
}

// searchToolFor is the search command the recall footer names: the configured one, or the one the session's probe
// found. It waits for the probe (at most the chain's timeouts) when it is still running.
func (w *warden) searchToolFor(cfg Config) SearchTool {
	if cfg.Context.RecallTool != "auto" && cfg.Context.RecallTool != "" {
		return cfg.Context.RecallTool
	}
	w.tools.mu.Lock()
	probe := w.tools.search
	w.tools.mu.Unlock()
	if probe == nil {
		return detectSearchTool("auto", probeTimeout)
	}
	select {
	case <-probe.done:
	case <-time.After(time.Duration(len(searchChain)) * probeTimeout):
		return "none"
	}
	return probe.tool
}

// ---------------------------------------------------------------------------
// Tools.

const (
	rememberDescription = "Record a standing lesson for this project, so later sessions keep it. Use it after the user corrected you, after a mistake you had to undo, or when you learned a fact about this project that later sessions need (for example, a site blocks one browser and allows another); never for task notes, plans, or progress. The lesson is one instruction in a standing form (don't, never, always, stop, from now on, next time), names no ticket, branch, PR, or hash, and never skips a test, check, review, or confirmation. It reaches later sessions only once it is confirmed."
	recallDescription   = "Recall what was already tried in this session before you try it again: the failed attempts with their error lines, the last passing check and whether the code changed since, and the paths of saved full outputs (read those instead of re-running the command). Read-only; takes no arguments."
)

// loopsDescription is built from loopChars, like the original's template literal.
var loopsDescription = "Keep open loops for this session, so a long session never loses a promise. Whenever you promise to do something later (\"after CI passes I will bump the version\", \"I will come back to the flaky test\"), add a loop; mark it done when you did it, or drop it with a reason when it is no longer needed. Actions: add (text, at most " + itoaInt(loopChars) + " characters, and an optional when condition such as \"after CI passes\"), done (id), drop (id and reason), list. Open loops are shown again after compaction, at the end of a run, and on resume. They belong to this session only."

// toolParameters is a plain JSON Schema, as the original passes one.
func toolParameters(properties map[string]any, required []string) sdk.Schema {
	if required == nil {
		required = []string{}
	}
	return sdk.Schema{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}

func toolReply(text string) sdk.ToolResult { return sdk.ToolResult{Content: text} }

// registerWardenTools registers warden_remember, warden_loops and warden_recall. TOOLS-LANE HOOK: called once from New.
func (w *warden) registerWardenTools(e *sdk.Extension) {
	e.RegisterTool(sdk.ToolDefinition{
		Name: "warden_remember", Label: "warden remember", Description: rememberDescription,
		Parameters: toolParameters(map[string]any{"lesson": map[string]any{
			"type": "string", "maxLength": lessonChars,
			"description": "One standing instruction for this project, at most " + itoaInt(lessonChars) + " characters, e.g. \"Never X\" or \"Always Y\".",
		}}, []string{"lesson"}),
		Execute: w.rememberTool,
	})
	e.RegisterTool(sdk.ToolDefinition{
		Name: "warden_loops", Label: "warden loops", Description: loopsDescription,
		Parameters: toolParameters(map[string]any{
			"action": map[string]any{"type": "string", "enum": loopActions, "description": "add, done, drop, or list."},
			"text":   map[string]any{"type": "string", "maxLength": loopChars, "description": "For add: what you promised to do."},
			"when":   map[string]any{"type": "string", "maxLength": loopChars, "description": "For add, optional: the condition that makes it due."},
			"id":     map[string]any{"type": "integer", "minimum": 1, "description": "For done and drop: the loop number."},
			"reason": map[string]any{"type": "string", "maxLength": loopChars, "description": "For drop: why it is no longer needed."},
		}, []string{"action"}),
		Execute: w.loopsTool,
	})
	e.RegisterTool(sdk.ToolDefinition{
		Name: "warden_recall", Label: "warden recall", Description: recallDescription,
		Parameters: toolParameters(map[string]any{}, nil),
		Execute:    w.recallTool,
	})
}

func (w *warden) rememberTool(ctx sdk.Context, params map[string]any) (any, error) {
	cfg := w.config(ctx)
	if !cfg.Enabled || !cfg.Prefs.Enabled {
		off := "enabled"
		if cfg.Enabled {
			off = "prefs.enabled"
		}
		return toolReply("not recorded: standing preferences are off (" + off + " is false)"), nil
	}
	lesson, _ := params["lesson"].(string)
	session, err := ctx.SessionManager().GetSessionID()
	if err != nil {
		session = "unknown"
	}
	scan, err := w.standingPrefs(ctx)
	if err != nil {
		return nil, err
	}
	dir, ok := w.dataDir(ctx)
	if !ok {
		return nil, errNoDataDir
	}
	now := w.nowMs()
	reply, err := changePrefsStore(prefsStorePath(scan.Project, dir), func(store PrefsStore) (string, *PrefsStore) {
		return recordLesson(LessonInput{Lesson: lesson, Session: session, Now: now, Scan: scan, Store: store})
	})
	if err != nil {
		return nil, err
	}
	return toolReply(reply), nil
}

func (w *warden) loopsTool(ctx sdk.Context, params map[string]any) (any, error) {
	if !w.config(ctx).Enabled {
		return toolReply("not available: pi-warden is off (enabled is false)"), nil
	}
	file := w.loopsFile(ctx)
	if file == "" {
		return toolReply("not available: this session has no id to keep loops under"), nil
	}
	now := w.nowMs()
	reply, err := updateLoops(file, func(store LoopStore) loopReply { return applyLoopAction(store, loopInput(params), now) })
	if err != nil {
		return nil, err
	}
	return toolReply(reply), nil
}

func (w *warden) recallTool(ctx sdk.Context, _ map[string]any) (any, error) {
	if !w.config(ctx).Enabled {
		return toolReply("not available: pi-warden is off (enabled is false)"), nil
	}
	return toolReply(recallText(w.evidenceSnapshot(ctx, ""))), nil
}

// ---------------------------------------------------------------------------
// Commands.

// cmdLoops is `/warden loops`: every loop of the session, open first. TOOLS-LANE HOOK: called from the command switch.
func (w *warden) cmdLoops(ctx sdk.Context) {
	file := w.loopsFile(ctx)
	if file == "" {
		w.say(ctx, "No loops: this session has no id to keep them under.", "info")
		return
	}
	store, err := readLoops(file)
	if err != nil {
		w.say(ctx, err.Error(), "error")
		return
	}
	w.say(ctx, formatLoopsForUser(store), "info")
}

// cmdPrefs is `/warden prefs [forget <n>]`. TOOLS-LANE HOOK: called from the command switch.
func (w *warden) cmdPrefs(ctx sdk.Context, rest []string) {
	cfg := w.config(ctx)
	if !cfg.Enabled || !cfg.Prefs.Enabled {
		off := "enabled"
		if cfg.Enabled {
			off = "prefs.enabled"
		}
		w.say(ctx, "Standing preferences are off ("+off+" is false); no session files were read.", "info")
		return
	}
	scan, path, items, err := w.evaluatedPrefs(ctx)
	if err != nil {
		w.say(ctx, err.Error(), "error")
		return
	}
	if len(rest) > 0 && rest[0] == "forget" {
		var item *PrefItem
		if len(rest) > 1 {
			if n, ok := jsNumber(rest[1]); ok && n == float64(int(n)) && n >= 1 && int(n) <= len(items) {
				item = &items[int(n)-1]
			}
		}
		if item == nil {
			w.say(ctx, "Usage: /warden prefs forget <n>, where n is an item number from /warden prefs (1 to "+itoaInt(len(items))+").", "warning")
			return
		}
		now := w.nowMs()
		if _, err := changePrefsStore(path, func(store PrefsStore) (struct{}, *PrefsStore) {
			next := forgetPref(store, *item, now)
			return struct{}{}, &next
		}); err != nil {
			w.say(ctx, err.Error(), "error")
			return
		}
		w.say(ctx, "Forgotten for this project: \""+item.Text+"\". It is not listed or injected again.", "info")
		return
	}
	w.say(ctx, formatPrefs(scan.Scanned, scan.Directories, items), "info")
}
