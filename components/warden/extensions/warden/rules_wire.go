package warden

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The rules guard's wiring into the extension's hooks (src/extension.ts: tool_call 1460-1740, agent_start 1380,
// agent_end 2364-2410, session_start 985-1015). Each hook is one call from the shared handler files; the logic lives
// here so another lane's change to those files stays line-local.

// rulesState is the rules guard's per-extension state.
type rulesState struct {
	guard *RulesGuard

	mu  sync.Mutex
	log *RulesLog
	// snapshot is the working-tree baseline started at run start and awaited at run end; nil when none was started.
	snapshot *snapshotRun
	// judged are the project-relative paths a write, edit or literal shell write already judged during this run.
	judged map[string]bool
	// warnedMissing: the first-run "no project rules" notice was shown this session.
	warnedMissing bool
	// lastCalibration is the most recent /warden rules calibrate result; /warden rules tune reads its flagged rules.
	lastCalibration *Calibration
	// lastCheck is the most recent /warden rules check result of this session; /warden rules tune reads its flagged rules.
	lastCheck *RulesCheckResult
	// initRunning: /warden init or /warden rules tune has asked the agent to write rules; new user input waits.
	initRunning bool
}

// snapshotRun is one baseline snapshot in progress: done closes when result is set.
type snapshotRun struct {
	done   chan struct{}
	result SnapshotResult
}

func newRulesState() *rulesState {
	return &rulesState{guard: NewRulesGuard(), judged: map[string]bool{}}
}

// InitBusy reports whether /warden init or /warden rules tune is running: the input hook blocks new user messages
// with "pi-warden is generating rules. Please wait..." while it is.
func (w *warden) InitBusy() bool {
	w.rules.mu.Lock()
	defer w.rules.mu.Unlock()
	return w.rules.initRunning
}

// holdsRecordRules is the hold-feedback store's record of a rules verdict (holds.recordRules). The learning lane
// installs the real one; the default records nothing.
var holdsRecordRules = func(w *warden, v RulesVerdict) {}

// dataDirOf is warden's data folder (the directory of its config file), or "" when the config home is unknown.
func (w *warden) dataDirOf() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cfgPath == "" {
		return ""
	}
	return filepath.Dir(w.cfgPath)
}

// rulesSessionStart starts the session's rules state over and opens the project's verdict log.
func (w *warden) rulesSessionStart(ctx sdk.Context) {
	cfg := w.config(ctx)
	rs := w.rules
	rs.guard.Reset()
	sessionID, err := ctx.GetSessionID()
	if err != nil || sessionID == "" {
		sessionID = strconv.Itoa(os.Getpid())
	}
	var log *RulesLog
	if dir := w.dataDirOf(); dir != "" {
		log = NewRulesLog(ctx.Cwd(), dir, sessionID, RulesLogOptions{OnFailure: func(message string) {
			w.record(ctx, cfg, GuardRules, "rules log write failed", []string{message}, nil)
		}})
	}
	rs.mu.Lock()
	rs.log, rs.snapshot, rs.judged = log, nil, map[string]bool{}
	rs.warnedMissing, rs.initRunning, rs.lastCalibration, rs.lastCheck = false, false, nil, nil
	rs.mu.Unlock()
}

// agentStartWithRules is the agent_start handler: the run's own reset, then the turn rules' baseline snapshot.
func (w *warden) agentStartWithRules(ctx sdk.Context, data map[string]any) (any, error) {
	res, err := w.onAgentStart(ctx, data)
	w.rulesAgentStart(ctx)
	return res, err
}

// rulesTurnEnd forgets the turn's prejudgments.
func (w *warden) rulesTurnEnd() { w.rules.guard.TurnEnd() }

// rulesShutdown waits for the work still in flight: the requests, the baseline snapshot and the verdict log's queued
// writes, so nothing writes into the project or the data folder after the session is over.
func (w *warden) rulesShutdown() {
	rs := w.rules
	rs.guard.Wait()
	rs.mu.Lock()
	snapshot, log := rs.snapshot, rs.log
	rs.mu.Unlock()
	if snapshot != nil {
		<-snapshot.done
	}
	if log != nil {
		log.Flush()
	}
}

// rulesAgentStart starts the working-tree baseline that turn rules need: the snapshot runs in the background and is
// awaited at agent_end, so this hook never waits for git to hash the changed and untracked files. One trace line when a
// run with turn rules gets no baseline. With no turn rules a failed snapshot is silent: a project without them sees no
// change.
func (w *warden) rulesAgentStart(ctx sdk.Context) {
	cfg := w.config(ctx)
	rs := w.rules
	rs.mu.Lock()
	rs.judged, rs.snapshot = map[string]bool{}, nil
	if cfg.Enabled && cfg.Rules.Enabled {
		run := &snapshotRun{done: make(chan struct{})}
		rs.snapshot = run
		cwd := ctx.Cwd()
		go func() {
			run.result = snapshotTree(cwd)
			close(run.done)
		}()
	}
	rs.mu.Unlock()
}

// ---------------------------------------------------------------------------
// tool_call

// contentNote is a note that waits until the call proceeds: a held, denied or declined write never lands and drops it.
type contentNote struct {
	guard SteerGuard
	kind  SteerKind
	text  string
}

// callNotes collects what the agent is told about one tool call: the action notes, then the content notes (rules and
// slop findings), then the sensitive-path notes, sent as one steer once the call's fate is known.
type callNotes struct {
	guards []SteerGuard
	kinds  []SteerKind
	notes  []string
	// content notes say "just written", so they wait until the call proceeds. They go between the action notes and the
	// sensitive-path notes, the order of the combined message.
	content []func(proceeds bool) *contentNote
	// pathNotes ride with the combined steer for this call; their trace waits for the delivery result.
	pathNotes  []string
	pathTraces []func(delivered bool)
}

func (n *callNotes) addGuard(g SteerGuard) {
	for _, have := range n.guards {
		if have == g {
			return
		}
	}
	n.guards = append(n.guards, g)
}

// deliver sends the combined steer once the call's fate is known; proceeds is false when the call does not run.
func (w *warden) deliverCallNotes(ctx sdk.Context, cfg Config, n *callNotes, proceeds bool, notePath string) {
	for _, build := range n.content {
		note := build(proceeds)
		if note == nil {
			continue
		}
		n.addGuard(note.guard)
		n.kinds = append(n.kinds, note.kind)
		n.notes = append(n.notes, note.text)
	}
	n.content = nil
	n.notes = append(n.notes, n.pathNotes...)
	n.pathNotes = nil
	if len(n.notes) == 0 {
		return
	}
	delivered := w.steer(cfg, n.guards, ctx, strings.Join(n.notes, "\n\n"), SteerOptions{DeliverAs: "steer"})
	if delivered {
		w.watchSteer(ctx, cfg, n.kinds, SteerSubject{Path: notePath}, false)
	}
	for _, trace := range n.pathTraces {
		trace(delivered)
	}
	n.pathTraces = nil
	n.notes = nil
}

// rulesCheck is one rules request started beside the action request.
type rulesCheck struct {
	future *RulesFuture
	shell  *ShellWrite
}

// rulesPlan is what the rules guard does for one tool call.
type rulesPlan struct {
	checks []rulesCheck
	// shellSkips are the shell writes that are not judged, with the reason for the trace.
	shellSkips []ShellSkip
	// shellPath is the first literal shell write, the path a note about the call names when the call has none.
	shellPath string
}

// rulesFirstRunNotice is the first-run warning: no project rules file resolved, one time per session.
func (w *warden) rulesFirstRunNotice(ctx sdk.Context, cfg Config) {
	rs := w.rules
	rs.mu.Lock()
	done := rs.warnedMissing
	rs.mu.Unlock()
	if done || !cfg.Rules.Enabled {
		return
	}
	missing, fallbackSource := checkPiWardenMissing(ctx.Cwd(), cfg.Rules)
	if !missing || !ctx.HasUI() {
		return
	}
	rs.mu.Lock()
	rs.warnedMissing = true
	rs.mu.Unlock()
	// The tiers still in play, in resolution order: naming README/CLAUDE/AGENTS to someone who turned the fallback off
	// would send them to a document warden will not read.
	tiers := []string{RulesFile, "rules.files"}
	if cfg.Rules.Fallback {
		tiers = append(tiers, fallbackFiles...)
	}
	msg := "No rules file detected (" + strings.Join(tiers, ", ") + "). Run /warden init to create project-specific rules."
	if fallbackSource != "" {
		msg = "No pi-warden.md or rules.files entry here. Using " + fallbackSource + " as active fallback rules. Run /warden init to create project-specific rules."
	}
	ctx.Notify(msg, "warning")
}

// rulesStart starts the rules requests for a tool call beside the action request: the written content and the rule text
// go out in their own request, not inside the action's. A bash command that writes literal content to a file is judged
// as a `write` of that content, before it runs, so a heredoc gets the checks a `write` gets.
func (w *warden) rulesStart(ctx sdk.Context, cfg Config, call ToolCallRef, siblings []ToolCallRef, judge Judge) rulesPlan {
	var plan rulesPlan
	if !cfg.Rules.Enabled {
		return plan
	}
	cwd := ctx.Cwd()
	refs := make([]RulesCallRef, len(siblings))
	for i, s := range siblings {
		refs[i] = RulesCallRef{ID: s.ID, Tool: s.Tool, Input: s.Input}
	}
	options := RulesOptions{Ctx: newContext(), Cwd: cwd, Config: cfg.Rules, Judge: judge, Timeout: w.timeout(cfg)}
	var shell ShellWriteScan
	if call.Tool == "bash" {
		if command, ok := call.Input["command"].(string); ok {
			home, err := os.UserHomeDir()
			shell = shellWrites(command, ShellWritesOptions{Home: home, HasHome: err == nil})
		}
	}
	// Each file is one rules request, so a script that writes many files would start as many requests at once.
	files := mergeWrites(shell.Writes)
	judged := files
	if len(judged) > shellRulesChecks {
		judged = judged[:shellRulesChecks]
	}
	// Turn rules: a file these checks judge is not judged again at the end of the run.
	var seen []string
	if call.Tool == "write" || call.Tool == "edit" {
		inputPath, _ := call.Input["path"].(string)
		seen = []string{inputPath}
	} else {
		for _, f := range judged {
			seen = append(seen, f.Path)
		}
	}
	w.rules.mu.Lock()
	for _, p := range seen {
		if rel, ok := rulesProjectPath(p, cwd); ok {
			w.rules.judged[rel] = true
		}
	}
	w.rules.mu.Unlock()
	plan.shellSkips = append(plan.shellSkips, shell.Skips...)
	for _, f := range files[len(judged):] {
		plan.shellSkips = append(plan.shellSkips, ShellSkip{Path: f.Path, Reason: "only the first " + strconv.Itoa(shellRulesChecks) + " files a command writes are judged"})
	}
	if len(shell.Writes) > 0 {
		plan.shellPath = shell.Writes[0].Path
	}
	if call.Tool == "write" || call.Tool == "edit" {
		plan.checks = []rulesCheck{{future: w.rules.guard.Inspect(RulesCallRef{ID: call.ID, Tool: call.Tool, Input: call.Input}, refs, options)}}
		return plan
	}
	for i, f := range judged {
		write := f
		ref := RulesCallRef{ID: call.ID + "#write" + strconv.Itoa(i), Tool: "write", Input: map[string]any{"path": f.Path, "content": f.Content}}
		plan.checks = append(plan.checks, rulesCheck{shell: &write, future: w.rules.guard.Inspect(ref, refs, options)})
	}
	return plan
}

// rulesVerdicts awaits the rules requests and turns them into trace lines, log records and content notes; the sensitive
// paths of a write or edit become path notes. summary is the action guard's description of the call.
func (w *warden) rulesVerdicts(ctx sdk.Context, cfg Config, plan rulesPlan, tool string, summary ActionSummary, cn *callNotes) {
	if !cfg.Rules.Enabled {
		return
	}
	rs := w.rules
	for _, check := range plan.checks {
		rules := check.future.Wait()
		check := check
		// The trace names the shell form, and an append as an append: only the appended text was judged.
		var shellNote []string
		var line string
		if check.shell != nil {
			verb, via := "written to", check.shell.Via
			tokenTool := "bash write"
			if check.shell.Append {
				verb, tokenTool = "appended to (only the appended text is judged)", "bash append"
			}
			shellNote = []string{"from bash: " + via + " " + verb + " " + rules.Path}
			tokens := rulesTokens(rules, w.opts.Now())
			tokens["tool"] = tokenTool
			line = RenderTemplate(cfg.Widget.Rules, tokens)
		} else {
			line = formatRules(rules, cfg.Widget.Rules, w.opts.Now())
		}
		if rules.Source == "skipped" {
			w.record(ctx, cfg, GuardRules, line, append(append([]string{}, shellNote...), rules.Tool+" "+rules.Path+": "+rules.SkippedReason), nil)
			continue
		}
		w.bump(func(s *stats) { s.RuleChecks++ })
		if rules.Error != "" {
			w.noteError(ctx, rules.Error, rules.ErrorCode)
		}
		// Only a shown notice uses up the once-per-session flag.
		if ctx.HasUI() && cfg.Notices {
			if notice := rs.guard.CapNotice(rules); notice != "" {
				ctx.Notify(notice, "warning")
			}
		}
		holdsRecordRules(w, rules)
		// A later judgment below the threshold clears an earlier finding of the same rule on this path: one trace line,
		// no steer.
		if rules.Source == "typesafe" {
			rs.mu.Lock()
			log := rs.log
			rs.mu.Unlock()
			if log != nil {
				for _, observation := range log.Record(rules, cfg.Rules.Threshold, w.opts.Now(), cfg.Rules.SoftThreshold) {
					if observation.Cleared {
						w.record(ctx, cfg, GuardRules, "rules: "+observation.Name+" now clear on "+rules.Path,
							[]string{"clear: a later " + rules.Tool + " on this path scored " + toFixed(observation.Violation, 2) + ", below the " + jsNumberString(cfg.Rules.Threshold) + " threshold"}, nil)
					}
				}
			}
		}
		if len(rules.Findings) > 0 {
			w.bump(func(s *stats) { s.RuleViolations++ })
			if ctx.HasUI() && cfg.Notices {
				ctx.Notify("warden · rules · "+rules.Path+": "+findingList(rules.Findings), "warning")
			}
		}
		cn.content = append(cn.content, func(proceeds bool) *contentNote {
			hasRules := len(rules.Findings) > 0 || len(rules.SoftFindings) > 0
			told := ""
			if proceeds && hasRules && w.adaptiveSend(ctx, cfg, SteerKindRules) {
				told = rulesSteer(rules, rs.guard.Count(rules))
			}
			details := append(append([]string{}, shellNote...), rulesDetails(rules, told)...)
			if !proceeds && len(rules.Findings) > 0 {
				details = append(details, "agent not told: the write was held")
			}
			w.record(ctx, cfg, GuardRules, line, details, nil)
			if told == "" {
				return nil
			}
			return &contentNote{guard: SteerGuardRules, kind: SteerKindRules, text: told}
		})
	}
	if len(plan.shellSkips) > 0 {
		var paths, details []string
		for _, skip := range plan.shellSkips {
			where := ""
			if skip.Path != "" {
				paths = append(paths, skip.Path)
				where = " (" + skip.Path + ")"
			}
			details = append(details, "shell write not judged"+where+": "+skip.Reason)
		}
		shown := "file change"
		if len(paths) > 0 {
			shown = strings.Join(paths, ", ")
		}
		w.record(ctx, cfg, GuardRules, RenderTemplate(cfg.Widget.Rules, Tokens{"guard": "rules", "tool": "bash", "path": shown, "status": "skipped"}), details, nil)
	}
	if tool == "write" || tool == "edit" {
		notePath := ""
		if summary.Location == "inside_project" {
			notePath = summary.Path
		}
		hits := rs.guard.NotesFor(notePath, cfg.Rules.SensitivePaths)
		if len(hits) > 0 && summary.Path != "" && w.adaptiveSend(ctx, cfg, SteerKindSensitivePath) {
			w.bump(func(s *stats) { s.PathNotes++ })
			told := pathNoteSteer(summary.Path, hits)
			globs := make([]string, len(hits))
			for i, h := range hits {
				globs[i] = h.Glob
			}
			joined := strings.Join(globs, ", ")
			// A repeat or a spent steer budget records the note without the agent reading it again, so the trace waits for
			// the combined steer's delivery result instead of claiming the agent was told.
			cn.pathTraces = append(cn.pathTraces, func(delivered bool) {
				outcome := "steer recorded, not delivered (a repeat or the per-run budget): " + told
				if delivered {
					outcome = "agent told: " + told
				}
				w.record(ctx, cfg, GuardRules, RenderTemplate(cfg.Widget.Rules, Tokens{"tool": tool, "path": summary.Path, "violations": "sensitive path " + joined, "status": "note"}),
					[]string{tool + " " + summary.Path + " matches " + joined + " in rules.sensitivePaths", outcome}, nil)
				if delivered && ctx.HasUI() && cfg.Notices {
					ctx.Notify("warden · sensitive path · "+summary.Path+" ("+joined+"); the agent was given the note", "warning")
				}
			})
			cn.addGuard(SteerGuardRules)
			cn.kinds = append(cn.kinds, SteerKindSensitivePath)
			cn.pathNotes = append(cn.pathNotes, told)
		}
	}
}

func findingList(findings []RuleFinding) string {
	parts := make([]string, len(findings))
	for i, f := range findings {
		parts[i] = f.Name + " (" + toFixed(f.Violation, 2) + ")"
	}
	return strings.Join(parts, "; ")
}

// notePathOf is the path a combined steer names: the action's own, else the first literal shell write.
func notePathOf(summary ActionSummary, plan rulesPlan) string {
	if summary.Path != "" {
		return summary.Path
	}
	return plan.shellPath
}

// ---------------------------------------------------------------------------
// agent_end

// rulesAgentEnd is the end-of-run rules pass: one judgment against the whole diff, and the files no per-edit check saw.
// One steer for the findings, through the done-check's delivery; the verdicts land in the rules log like the rest.
func (w *warden) rulesAgentEnd(ctx sdk.Context, cfg Config, judge Judge) {
	rs := w.rules
	rs.mu.Lock()
	snapshot := rs.snapshot
	rs.snapshot = nil
	judged := map[string]bool{}
	for p := range rs.judged {
		judged[p] = true
	}
	log := rs.log
	rs.mu.Unlock()
	if !cfg.Rules.Enabled || snapshot == nil {
		return
	}
	// The run end waits for the snapshot the run start began; a run that ended first has been waiting since.
	<-snapshot.done
	base := snapshot.result
	set := rs.guard.Store.Load(ctx.Cwd(), cfg.Rules)
	if base.Tree == "" {
		if set != nil && len(turnRulesFor(set)) > 0 {
			w.record(ctx, cfg, GuardRules, "warden · rules · turn rules skipped this run: "+base.Reason, []string{"trigger: agent_end", "no working-tree snapshot: the end-of-run pass will not run this turn rules check"}, nil)
		}
		return
	}
	task, _ := w.latestUserPrompt(ctx)
	run := evaluateTurnRun(TurnRunOptions{Ctx: newContext(), Cwd: ctx.Cwd(), Config: cfg.Rules, Set: set, Judge: judge, Timeout: w.timeout(cfg), Task: task, StartTree: base.Tree, AlreadyJudged: judged})
	if run.Skipped != "" {
		w.record(ctx, cfg, GuardRules, "warden · rules · end-of-run pass skipped: "+run.Skipped, []string{"trigger: agent_end"}, nil)
	}
	// One trace line names the files the per-run cap left unjudged, like the skips of one command's shell writes.
	if len(run.Skips) > 0 {
		var paths, details []string
		for _, skip := range run.Skips {
			where := ""
			if skip.Path != "" {
				paths = append(paths, skip.Path)
				where = " (" + skip.Path + ")"
			}
			details = append(details, "unseen change not judged"+where+": "+skip.Reason)
		}
		shown := "file change"
		if len(paths) > 0 {
			shown = strings.Join(paths, ", ")
		}
		w.record(ctx, cfg, GuardRules, RenderTemplate(cfg.Widget.Rules, Tokens{"guard": "rules", "tool": "unseen change", "path": shown, "status": "skipped"}), details, nil)
	}
	counts := map[string]int{}
	hasFindings := false
	for _, verdict := range run.Verdicts {
		counts = rs.guard.Count(verdict)
		hasFindings = hasFindings || len(verdict.Findings) > 0
	}
	told := ""
	if hasFindings && w.adaptiveSend(ctx, cfg, SteerKindRules) {
		told = turnSteer(run.Verdicts, counts)
	}
	delivered := false
	if told != "" {
		delivered = w.steer(cfg, []SteerGuard{SteerGuardRules}, ctx, told, SteerOptions{DeliverAs: "followUp", TriggerTurn: true})
	}
	if delivered {
		w.watchSteer(ctx, cfg, []SteerKind{SteerKindRules}, SteerSubject{}, false)
	}
	for _, verdict := range run.Verdicts {
		tokens := rulesTokens(verdict, w.opts.Now())
		if verdict.Tool == "turn" {
			tokens["tool"], tokens["path"] = "turn", "the run's diff"
		} else {
			tokens["tool"] = "unseen change"
		}
		shown := RenderTemplate(cfg.Widget.Rules, tokens)
		var cuts []string
		if verdict.Tool == "turn" && len(run.Cuts) > 0 {
			cuts = []string{"diff caps cut: " + strings.Join(run.Cuts, "; ")}
		}
		if verdict.Source == "skipped" {
			w.record(ctx, cfg, GuardRules, shown, []string{verdict.Tool + " " + verdict.Path + ": " + verdict.SkippedReason}, nil)
			continue
		}
		w.bump(func(s *stats) { s.RuleChecks++ })
		if verdict.Error != "" {
			w.noteError(ctx, verdict.Error, verdict.ErrorCode)
		}
		if verdict.Source == "typesafe" && log != nil {
			for _, observation := range log.Record(verdict, cfg.Rules.Threshold, w.opts.Now(), cfg.Rules.SoftThreshold) {
				if observation.Cleared {
					w.record(ctx, cfg, GuardRules, "rules: "+observation.Name+" now clear on "+verdict.Path,
						[]string{"clear: a later judgment on this path scored " + toFixed(observation.Violation, 2) + ", below the " + jsNumberString(cfg.Rules.Threshold) + " threshold"}, nil)
				}
			}
		}
		if len(verdict.Findings) > 0 {
			w.bump(func(s *stats) { s.RuleViolations++ })
			if ctx.HasUI() && cfg.Notices {
				where := verdict.Path
				if verdict.Tool == "turn" {
					where = "this run's changes"
				}
				ctx.Notify("warden · rules · "+where+": "+findingList(verdict.Findings), "warning")
			}
		}
		agentTold := ""
		switch {
		case told != "" && delivered:
			agentTold = "agent told: " + told
		case told != "":
			agentTold = "steer recorded, not delivered (a repeat or the per-run budget): " + told
		}
		w.record(ctx, cfg, GuardRules, shown, append(cuts, rulesDetails(verdict, agentTold)...), nil)
	}
}

// rulesStatusLine is the `Rules:` line of /warden status.
func (w *warden) rulesStatusLine(ctx sdk.Context, cfg Config) string {
	if !cfg.Rules.Enabled {
		return "Rules: off."
	}
	text := w.rules.guard.Describe(ctx.Cwd(), cfg.Rules)
	if n := len(cfg.Rules.SensitivePaths); n > 0 {
		text += "; " + strconv.Itoa(n) + " sensitive " + plural(n, "path", "paths")
	}
	return "Rules: " + text + "."
}
