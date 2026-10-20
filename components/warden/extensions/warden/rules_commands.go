package warden

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// `/warden rules` (list, check, audit, calibrate, tune), `/warden report` and `/warden init` (src/extension.ts
// 2578-2714 and 2860-2895).

var (
	reCommitsFlag = lazyRE(`(?:^|[` + jsSpaceClass + `])--commits[` + jsSpaceClass + `]+(\S+)`)
	reMaxFlag     = lazyRE(`(?:^|[` + jsSpaceClass + `])--max[` + jsSpaceClass + `]+(\S+)`)
	reYesFlag     = lazyRE(`(?:^|[` + jsSpaceClass + `])--yes(?:[` + jsSpaceClass + `]|$)`)
	reDaysFlag    = lazyRE(`--days[` + jsSpaceClass + `]+(\d+)`)
)

// judgeForCommand is the judge for a command that sends requests, or the sentence tail that says why none is available:
// " (the request budget is used up)" when the budget is spent, then "." and the reason (no consent, a backend that
// cannot start). Only the second return value is set when no judge is available.
func (w *warden) judgeForCommand(ctx sdk.Context, cfg Config) (Judge, string) {
	if judge := w.judgeFor(ctx, cfg); judge != nil {
		return judge, ""
	}
	w.mu.Lock()
	exhausted := w.budgetExhausted
	w.mu.Unlock()
	if exhausted {
		return nil, " (the request budget is used up)."
	}
	if !cfg.Enabled || cfg.Backend == BackendNone || cfg.Consent != cfg.Backend {
		remedy := "Run /warden enable."
		if !ctx.HasUI() {
			remedy = "Set PIGPEN_WARDEN_ENABLED=1 and PIGPEN_WARDEN_BACKEND=typesafe or own-model."
		}
		return nil, ". warden: Jev judgments are off (no consent). " + remedy
	}
	build := w.opts.NewJudge
	if build == nil {
		build = defaultJudge
	}
	if _, err := build(ctx, cfg, w.budget, w.opts.Getenv); err != nil {
		return nil, ". warden: Jev judgments are off (" + err.Error() + ")"
	}
	return nil, "."
}

// rulesCommand is `/warden rules [check|audit|calibrate|tune]`; tokens are the words after `rules`.
func (w *warden) rulesCommand(ctx sdk.Context, tokens []string) error {
	cfg := w.config(ctx)
	argument := ""
	if len(tokens) > 0 {
		argument = tokens[0]
	}
	rs := w.rules
	set := rs.guard.Store.Load(ctx.Cwd(), cfg.Rules)
	switch argument {
	case "audit":
		return w.rulesAudit(ctx, cfg, tokens[1:], set)
	case "check":
		judge, off := w.judgeForCommand(ctx, cfg)
		result := checkRules(RulesCheckOptions{Ctx: newContext(), Set: set, Judge: judge, Timeout: w.timeout(cfg)})
		if result.Source == "skipped" {
			// No key, no consent, or a spent budget: say why and send nothing.
			w.say(ctx, "Rules check sent nothing: Jev judgments are off"+off+" /warden rules shows the rule set locally.", "info")
			return nil
		}
		if result.Source == "typesafe" {
			rs.mu.Lock()
			rs.lastCheck = &result
			rs.mu.Unlock()
		}
		w.say(ctx, formatRulesCheck(result), "info")
		return nil
	case "calibrate":
		return w.rulesCalibrate(ctx, cfg, tokens, set)
	case "tune":
		return w.rulesTune(ctx, cfg)
	}
	w.say(ctx, rs.guard.Details(ctx.Cwd(), cfg.Rules), "info")
	return nil
}

func (w *warden) rulesAudit(ctx sdk.Context, cfg Config, tokens []string, set *RuleSet) error {
	parsed := parseRulesAuditArgs(tokens)
	if parsed.Error != "" {
		w.say(ctx, parsed.Error, "warning")
		return nil
	}
	judge, off := w.judgeForCommand(ctx, cfg)
	destination := w.targetOf(ctx, cfg.Backend)
	options := RulesAuditOptions{
		Ctx: newContext(), Cwd: ctx.Cwd(), Paths: parsed.Paths, Max: parsed.Max, Yes: parsed.Yes, Config: cfg.Rules, Set: set, Judge: judge,
		Timeout: w.timeout(cfg), Destination: destination,
	}
	if ctx.HasUI() {
		options.Confirm = func(title, body string) bool {
			ok, err := ctx.Confirm(title, body)
			return err == nil && ok
		}
	}
	result := runRulesAudit(options)
	switch result.Status {
	// No key, no consent, or a spent budget: say why and send nothing.
	case "no-judge":
		w.say(ctx, "Rules audit sent nothing: Jev judgments are off"+off, "info")
	case "no-files":
		w.say(ctx, "Rules audit sent nothing: "+result.Reason+".", "info")
	case "needs-yes":
		w.say(ctx, "Rules audit sent nothing: a headless run needs --yes before "+strconv.Itoa(result.Files)+" file samples go to "+destination+" ("+strconv.Itoa(result.LeftOut)+" more files left out at the --max "+strconv.Itoa(parsed.Max)+" cap).", "warning")
	case "cancelled":
		w.say(ctx, "Rules audit cancelled. Nothing was sent.", "info")
	case "done":
		w.say(ctx, formatRulesAudit(*result.Outcome), "info")
	}
	return nil
}

func (w *warden) rulesCalibrate(ctx sdk.Context, cfg Config, tokens []string, set *RuleSet) error {
	const usage = "Usage: /warden rules calibrate [--commits N] [--max N] [--yes]."
	rs := w.rules
	tail := strings.Join(tokens, " ")
	commitsMatch := reCommitsFlag.FindStringSubmatch(tail)
	maxMatch := reMaxFlag.FindStringSubmatch(tail)
	yes := reYesFlag.MatchString(tail)
	commits, max := calibrateDefaultCommits, calibrateDefaultMax
	if commitsMatch != nil {
		n, ok := positiveInteger(commitsMatch[1])
		if !ok {
			w.say(ctx, usage, "warning")
			return nil
		}
		commits = n
	}
	if maxMatch != nil {
		n, ok := positiveInteger(maxMatch[1])
		if !ok {
			w.say(ctx, usage, "warning")
			return nil
		}
		max = n
	}
	if set == nil {
		w.say(ctx, "No rules file detected. Run /warden init to create project-specific rules.", "info")
		return nil
	}
	if set.ProseOnly {
		w.say(ctx, "No rules to calibrate: "+strings.Join(set.Sources, ", ")+" has no rule-shaped sections, so the guard judges nothing there.", "info")
		return nil
	}
	if set.isAggregate() {
		w.say(ctx, "No separate rules to calibrate: "+strings.Join(set.Sources, ", ")+" has no rule headings, so the guard judges it as one document.", "info")
		return nil
	}
	judge, off := w.judgeForCommand(ctx, cfg)
	if judge == nil {
		w.say(ctx, "Rules calibrate sent nothing: Jev judgments are off"+off+" /warden rules shows the rule set locally.", "info")
		return nil
	}
	history, err := collectHistory(ctx.Cwd(), commits)
	if err != nil {
		w.say(ctx, "Rules calibrate could not read the git history: "+err.Error(), "warning")
		return nil
	}
	plan := planCalibration(history, PlanOptions{Set: set, Config: cfg.Rules, Cwd: ctx.Cwd(), MaxRequests: max})
	if plan.Requests == 0 {
		skipped := ""
		if n := len(plan.Skipped); n > 0 {
			skipped = " (" + strconv.Itoa(n) + " " + plural(n, "change", "changes") + " skipped)"
		}
		w.say(ctx, "Rules calibrate sent nothing: no changed file in the last "+strconv.Itoa(commits)+" non-merge "+plural(commits, "commit", "commits")+" is judgeable"+skipped+".", "info")
		return nil
	}
	// Nothing leaves the machine before this gate: the dialog shows the count and the redacted diffs, and a headless run
	// needs the explicit --yes.
	gate := calibrateGate(ctx.HasUI(), yes)
	if gate.Kind == "refuse" {
		w.say(ctx, gate.Reason, "warning")
		return nil
	}
	if gate.Kind == "confirm" {
		ok, err := ctx.Confirm("warden: send "+strconv.Itoa(plan.Requests)+" calibration "+plural(plan.Requests, "request", "requests")+"?", calibrationNotice(plan))
		if err != nil || !ok {
			w.say(ctx, "Cancelled. Nothing was sent.", "info")
			return nil
		}
	}
	result := calibrate(history, CalibrateOptions{PlanOptions: PlanOptions{Set: set, Config: cfg.Rules, Cwd: ctx.Cwd(), MaxRequests: max}, Ctx: newContext(), Judge: judge, Timeout: w.timeout(cfg)})
	rs.mu.Lock()
	log := rs.log
	if log == nil {
		if dir := w.dataDirOf(); dir != "" {
			log = NewRulesLog(ctx.Cwd(), dir, strconv.Itoa(os.Getpid()), RulesLogOptions{})
			rs.log = log
		}
	}
	rs.lastCalibration = &result
	rs.mu.Unlock()
	if log != nil {
		log.AppendRecords(result.Records)
	}
	w.say(ctx, formatCalibration(result), "info")
	return nil
}

// askAgent sends the session's agent one prompt as a user message and waits for its run to end. sendUserMessage throws
// when the agent is not idle, so a brief wait lets a just-closing confirm dialog finish first.
func (w *warden) askAgent(ctx sdk.Context, prompt string) error {
	for attempt := 0; attempt < 40; attempt++ {
		if idle, err := ctx.IsIdle(); err != nil || idle {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if err := ctx.SendUserMessage(prompt, ""); err != nil {
		return err
	}
	return ctx.WaitForIdle()
}

func (w *warden) setInitRunning(on bool) {
	w.rules.mu.Lock()
	w.rules.initRunning = on
	w.rules.mu.Unlock()
}

func (w *warden) rulesTune(ctx sdk.Context, cfg Config) error {
	rs := w.rules
	rs.mu.Lock()
	in := TuneInputs{Calibration: rs.lastCalibration, Check: rs.lastCheck}
	rs.mu.Unlock()
	if set := rs.guard.Store.Load(ctx.Cwd(), cfg.Rules); set != nil {
		in.Rules = set.Rules
	}
	request := tuneRequest(in)
	if request.Prompt == "" {
		w.say(ctx, request.Reason, "info")
		return nil
	}
	w.setInitRunning(true)
	sent := true
	if ctx.HasUI() {
		ctx.Notify("pi-warden: Sending the rules rewrite prompt...", "info")
	}
	if err := w.askAgent(ctx, request.Prompt); err != nil {
		sent = false
		w.say(ctx, "pi-warden rules tune failed: "+err.Error()+".", "error")
	}
	w.setInitRunning(false)
	if sent {
		w.say(ctx, "Rewrite prompt sent. The agent edits pi-warden.md with its own tools; review the changes.", "info")
	}
	return nil
}

// reportCommand is `/warden report [--days N]`.
func (w *warden) reportCommand(ctx sdk.Context, tokens []string) error {
	cfg := w.config(ctx)
	days := reportDefaultDays
	if m := reDaysFlag.FindStringSubmatch(strings.Join(tokens, " ")); m != nil {
		n, _ := strconv.Atoi(m[1])
		if n < 1 {
			w.say(ctx, "Usage: /warden report [--days N], where N is at least 1. Default: 30 days.", "warning")
			return nil
		}
		days = n
	}
	dir := w.dataDirOf()
	var all []RuleRecord
	if dir != "" {
		var err error
		if all, err = readRulesLog(rulesLogPath(ctx.Cwd(), dir)); err != nil {
			return err
		}
	}
	// Calibration replays carry source "calibrate"; the live verdict report counts only the live records and says how many
	// replays it set apart.
	replays := 0
	for _, r := range all {
		if r.Source == "calibrate" {
			replays++
		}
	}
	var current []NamedRule
	if set := w.rules.guard.Store.Load(ctx.Cwd(), cfg.Rules); set != nil {
		for _, rule := range set.Rules {
			current = append(current, NamedRule{ID: rule.ID, Name: rule.Name})
		}
	}
	text := formatRulesReport(buildRulesReport(all, ReportOptions{Days: days, Now: w.opts.Now(), CurrentRules: current, Source: "live"}))
	if replays > 0 {
		text += "\n" + strconv.Itoa(replays) + " calibration replay " + plural(replays, "record", "records") + " in this log, counted apart (source: calibrate)."
	}
	w.say(ctx, text, "info")
	return nil
}

// initCommand is `/warden init [--force]`: the agent writes the project's rules file.
func (w *warden) initCommand(ctx sdk.Context, tokens []string) error {
	target := filepath.Join(ctx.Cwd(), RulesFile)
	force := len(tokens) > 0 && tokens[0] == "--force"
	if fileExists(target) {
		if ctx.HasUI() && !force {
			ok, err := ctx.Confirm("pi-warden.md already exists. Overwrite with a fresh starter template?", "This replaces "+target+" with a generic starter. Your current rules will be lost.")
			if err != nil || !ok {
				w.say(ctx, "Cancelled. Existing pi-warden.md unchanged.", "info")
				return nil
			}
		} else if !force {
			w.say(ctx, "pi-warden.md already exists at "+target+". Pass --force to overwrite.", "warning")
			return nil
		}
	}
	prompt := buildInitPrompt(ctx.Cwd())
	w.setInitRunning(true)
	if ctx.HasUI() {
		ctx.Notify("pi-warden: Generating rules file...", "info")
	}
	if err := w.askAgent(ctx, prompt); err != nil {
		w.say(ctx, "pi-warden init failed: "+err.Error()+". Try creating pi-warden.md manually.", "error")
	}
	w.setInitRunning(false)
	if fileExists(target) {
		w.say(ctx, "pi-warden.md created. Review the rules and edit as needed.", "info")
	} else {
		w.say(ctx, "Agent did not create pi-warden.md. Create it manually or try /warden init again.", "info")
	}
	return nil
}
