package warden

import (
	"context"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The hold feedback loop and the learning store wired into the extension (extension.ts: holds, learningIds,
// noteOutcomes, settleRegret, the judge cooldown). Database and log writes run on one ordered background queue, as
// the original's un-awaited promises do, so a tool call never waits for a disk.

// serialQueue runs functions one after another on a goroutine that exists only while there is work.
type serialQueue struct {
	mu     sync.Mutex
	items  []func()
	active bool
}

func (q *serialQueue) do(f func()) {
	q.mu.Lock()
	q.items = append(q.items, f)
	if !q.active {
		q.active = true
		go q.run()
	}
	q.mu.Unlock()
}

func (q *serialQueue) run() {
	for {
		q.mu.Lock()
		if len(q.items) == 0 {
			q.active = false
			q.mu.Unlock()
			return
		}
		f := q.items[0]
		q.items = q.items[1:]
		q.mu.Unlock()
		f()
	}
}

// flush returns when everything queued before it has run.
func (q *serialQueue) flush() {
	done := make(chan struct{})
	q.do(func() { close(done) })
	<-done
}

// holdID is the database id of a hold, known once its insert ran; -1 when the insert failed.
type holdID struct {
	mu sync.Mutex
	id int64
}

func (h *holdID) set(id int64) { h.mu.Lock(); h.id = id; h.mu.Unlock() }
func (h *holdID) get() int64   { h.mu.Lock(); defer h.mu.Unlock(); return h.id }

// learningState is the session's hold feedback.
type learningState struct {
	mu     sync.Mutex
	ledger *HoldLedger
	log    *HoldLog
	// traceOf is the trace entry each record's outcome lands on, by record id.
	traceOf map[int]*TraceEntry
	// ids maps a ledger id to the database id of its insert.
	ids map[int]*holdID
	// regretCandidates are the allowed calls of the turn the user just replied to; the regret question about them
	// rides the next action request.
	regretCandidates []PreviousAction
	queue            serialQueue
	// logPending is the newest snapshot waiting for the log write; a newer snapshot replaces an unwritten older one,
	// since each is the whole state.
	logPending   []CallRecord
	logScheduled bool
	// schemaReady: this session has queued the schema and the retention prune.
	schemaReady bool
	cooldown    *JudgeCooldown
	// warnings are the learning module's console warnings, newest last (shown by /warden status).
	warnings []string
}

func newLearningState(now func() time.Time) *learningState {
	return &learningState{ledger: NewHoldLedger(), traceOf: map[int]*TraceEntry{}, ids: map[int]*holdID{}, cooldown: NewJudgeCooldown(now)}
}

func (l *learningState) warn(msg string) {
	l.mu.Lock()
	l.warnings = append(l.warnings, msg)
	if len(l.warnings) > 20 {
		l.warnings = l.warnings[len(l.warnings)-20:]
	}
	l.mu.Unlock()
}

// learning is the store at the configured database path, or nil when the data folder is unknown.
func (w *warden) learning() *Learning {
	dir := w.dataDirOf()
	if dir == "" {
		return nil
	}
	return NewLearning(LearningPath(dir, w.opts.Getenv), w.learn.warn)
}

// learnFlush waits for the queued database and log writes.
func (w *warden) learnFlush() { w.learn.queue.flush() }

// learnEnsure queues the schema and the retention prune once per session, and only for a warden that is on.
func (w *warden) learnEnsure(cfg Config) { w.learnEnsureWhen(cfg, cfg.Enabled) }

// learnEnsureWhen is learnEnsure with the decision made by the caller: `/warden recommend` asks for the history
// whether warden is on or not.
func (w *warden) learnEnsureWhen(cfg Config, on bool) {
	if !on {
		return
	}
	l := w.learn
	l.mu.Lock()
	done := l.schemaReady
	l.schemaReady = true
	l.mu.Unlock()
	if done {
		return
	}
	if store := w.learning(); store != nil {
		retention := cfg.Learning.RetentionDays
		l.queue.do(func() { store.InitSchema(retention) })
	}
}

// learnSessionStart starts the session's hold feedback over (extension.ts session_start).
func (w *warden) learnSessionStart(ctx sdk.Context) {
	cfg := w.config(ctx)
	l := w.learn
	l.cooldown.Reset()
	l.mu.Lock()
	l.schemaReady = false
	l.mu.Unlock()
	// A warden that is off touches no database: the schema (and the retention prune) is made on first use once it is on.
	w.learnEnsure(cfg)
	if a, ok := w.adaptor.(interface{ clear() }); ok {
		a.clear()
	}
	sessionID, err := ctx.GetSessionID()
	if err != nil || sessionID == "" {
		sessionID = strconv.Itoa(os.Getpid())
	}
	l.mu.Lock()
	l.ledger.Reset()
	l.traceOf = map[int]*TraceEntry{}
	l.ids = map[int]*holdID{}
	l.regretCandidates = nil
	l.log = nil
	if dir := w.dataDirOf(); dir != "" {
		l.log = NewHoldLog(HoldLogPath(dir, sessionID, w.opts.Now(), os.Getpid()))
	}
	l.mu.Unlock()
}

// previousActions are the regret candidates, for the action guard's regret question.
func (w *warden) previousActions() []PreviousAction {
	w.learn.mu.Lock()
	defer w.learn.mu.Unlock()
	return append([]PreviousAction(nil), w.learn.regretCandidates...)
}

// learnPromptArrived is before_agent_start: holds the user never approved are re-plans now, and last turn's allowed
// calls wait for the regret question (answered offline by a stop-word heuristic when there is no judge).
func (w *warden) learnPromptArrived(ctx sdk.Context, data map[string]any) {
	cfg := w.config(ctx)
	w.noteOutcomes(cfg, w.learn.ledger.PromptArrived(0))
	candidates := w.learn.ledger.Candidates()
	w.learn.mu.Lock()
	w.learn.regretCandidates = candidates
	w.learn.mu.Unlock()
	if len(candidates) > 0 && w.judgeFor(ctx, cfg) == nil {
		prompt, _ := data["prompt"].(string)
		w.settleRegret(cfg, RegretResult{Regretted: TextRegrets(prompt), Via: ViaText})
	}
}

// beforeAgentStartWithLearning runs the hold feedback's prompt hook, then the guard's own.
func (w *warden) beforeAgentStartWithLearning(ctx sdk.Context, data map[string]any) (any, error) {
	if w.config(ctx).Enabled {
		w.learnPromptArrived(ctx, data)
	}
	return w.onBeforeAgentStart(ctx, data)
}

// settleRegret: the user's reply was read for regret, by the judge or by the offline heuristic; the candidates are
// labelled once.
func (w *warden) settleRegret(cfg Config, r RegretResult) {
	w.learn.mu.Lock()
	w.learn.regretCandidates = nil
	w.learn.mu.Unlock()
	w.noteOutcomes(cfg, w.learn.ledger.Regret(r, 0))
}

// settleRegretFromVerdict labels the candidates from the regret answer that rode this call's judgment.
func (w *warden) settleRegretFromVerdict(cfg Config, v Verdict) {
	w.learn.mu.Lock()
	has := len(w.learn.regretCandidates) > 0
	w.learn.mu.Unlock()
	if has && v.Judgment != nil && v.Judgment.Regretted != nil {
		w.settleRegret(cfg, RegretResult{Regretted: RegretsAt(*v.Judgment.Regretted), Target: v.Judgment.RegretTarget, Probability: v.Judgment.Regretted, Via: ViaJev})
	}
}

// noteOutcomes: labels landed on earlier calls; their trace entries say so, the learning rows are labelled, and the
// session log is rewritten.
func (w *warden) noteOutcomes(cfg Config, records []*CallRecord) {
	l := w.learn
	store := w.learning()
	for _, item := range records {
		l.mu.Lock()
		entry := l.traceOf[item.ID]
		fut := l.ids[item.ID]
		l.mu.Unlock()
		if entry != nil {
			w.trace.Amend(entry, OutcomeNote(*item))
		}
		if fut != nil && store != nil && item.Outcome != OutcomePending {
			outcome := string(item.Outcome)
			l.queue.do(func() { store.RecordOutcome(fut.get(), outcome) })
		}
	}
	if cfg.Action.FeedbackLog {
		if all := l.ledger.Records(); len(all) > 0 {
			l.saveLog(all)
		}
	}
}

// saveLog writes the session log in the background; the newest snapshot wins.
func (l *learningState) saveLog(records []CallRecord) {
	l.mu.Lock()
	log := l.log
	if log == nil {
		l.mu.Unlock()
		return
	}
	l.logPending = records
	if l.logScheduled {
		l.mu.Unlock()
		return
	}
	l.logScheduled = true
	l.mu.Unlock()
	l.queue.do(func() {
		l.mu.Lock()
		recs := l.logPending
		l.logPending, l.logScheduled = nil, false
		l.mu.Unlock()
		log.Save(recs)
	})
}

// summarizeContext is the last eight context messages, redacted and clipped, for the learning row.
func summarizeContext(context []TaskMessage) string {
	if len(context) == 0 {
		return ""
	}
	if len(context) > 8 {
		context = context[len(context)-8:]
	}
	lines := make([]string, len(context))
	for i, m := range context {
		lines[i] = m.Role + ": " + Redact(utf16Slice(m.Text, 0, 200))
	}
	return strings.Join(lines, "\n")
}

// holdTrack is what one judged call needs to be tracked.
type holdTrack struct {
	verdict Verdict
	// entry is the verdict's trace entry; nil (a read-only call) tracks nothing.
	entry *TraceEntry
	mode  string
	held  bool
	// outcome and via are set when the confirm dialog or a deny rule decided; a steer-mode hold starts pending.
	outcome CallOutcome
	via     OutcomeVia
	conv    Conversation
	// agentReason is the steer the agent got, for a held call.
	agentReason string
}

// trackHold records what happens to a call as the label for its scores: a decision made in the dialog lands at
// once, a steer-mode hold waits for the user. It is extension.ts `track`.
func (w *warden) trackHold(ctx sdk.Context, cfg Config, t holdTrack) {
	if t.entry == nil {
		return
	}
	w.learnEnsure(cfg)
	l := w.learn
	item := l.ledger.Record(t.verdict, HoldOptions{Held: t.held, Mode: t.mode, Outcome: t.outcome, Via: t.via})
	ctxSummary := summarizeContext(t.conv.Context)
	preview := Redact(t.verdict.Summary.Command)
	if t.verdict.Summary.Command == "" {
		preview = Redact(t.verdict.Summary.Path)
	}
	hc := HoldContext{Plan: t.verdict.Plan, ContextSummary: ctxSummary, Preview: &preview}
	if t.conv.Task != "" {
		hc.Task = Redact(t.conv.Task)
	}
	if t.held {
		hc.AgentReason = t.agentReason
	}
	held := t.held
	rec := ToHoldRecord(HoldItem{At: item.At, Tool: item.Tool, Level: string(item.Level), Reasons: item.Reasons, Scores: item.Scores, Held: &held}, ctx.Cwd(), hc)
	fut := &holdID{id: -1}
	l.mu.Lock()
	l.traceOf[item.ID] = t.entry
	l.ids[item.ID] = fut
	if len(l.ids) > 1000 {
		// Prune to the most recent 500 so the map cannot grow for the life of a long session.
		for id := range l.ids {
			if id <= item.ID-500 {
				delete(l.ids, id)
			}
		}
	}
	l.mu.Unlock()
	// The id exists before noteOutcomes so that an outcome known at record time (the dialog's) is not lost to the race.
	if store := w.learning(); store != nil {
		l.queue.do(func() {
			id, err := store.RecordHold(rec)
			if err != nil {
				l.warn("pi-warden: recordHold failed: " + err.Error())
				id = -1
			}
			fut.set(id)
		})
	}
	if t.outcome != "" {
		w.noteOutcomes(cfg, []*CallRecord{item})
	}
}

// holdApproved: the user's reply released a pending hold of this tool (a retry that passed).
func (w *warden) holdApproved(cfg Config, tool string) {
	if released := w.learn.ledger.Approved(tool, ViaRetry, 0); released != nil {
		w.noteOutcomes(cfg, []*CallRecord{released})
	}
}

// learnMessageEnd settles the steers waiting for the agent's next two messages (extension.ts message_end).
func (w *warden) learnMessageEnd(ctx sdk.Context, data map[string]any) (any, error) {
	msg, _ := data["message"].(map[string]any)
	if role, _ := msg["role"].(string); role == "assistant" {
		w.settleSteers(w.config(ctx), w.adaptor.assistant(assistantView(msg["content"])))
	}
	return nil, nil
}

// learnShutdown waits for the queued writes.
func (w *warden) learnShutdown() { w.learnFlush() }

// installLearning wires the module's pieces into the guard state; newWarden calls it once.
func (w *warden) installLearning() {
	w.learn = newLearningState(func() time.Time { return w.opts.Now() })
	w.adaptor = newAdaptiveAdaptor(w.steerStatsPath, w.learn.warn)
	w.gc.previousActions = w.previousActions
	w.tools.holds = holdAdapter{w.learn.ledger} // the held calls the compaction appendix and warden_recall list
}

// steerStatsPath is resolved lazily: the data folder is known once the config has loaded.
func (w *warden) steerStatsPath() string {
	dir := w.dataDirOf()
	if dir == "" {
		return ""
	}
	return SteerStatsPath(dir, w.opts.Getenv)
}

// ---------------------------------------------------------------------------
// The judge cooldown (extension.ts `watched` and `tellCooldown`).

// cooldownJudge sees each failure and each success of the judge, so a dead backend costs one notice instead of one
// timeout per action.
type cooldownJudge struct {
	inner Judge
	w     *warden
	ctx   sdk.Context
	cfg   Config
}

func (j *cooldownJudge) Evaluate(ctx context.Context, r Request) (Evaluation, error) {
	res, err := j.inner.Evaluate(ctx, r)
	if err == nil {
		j.w.tellCooldown(j.ctx, j.cfg, j.w.learn.cooldown.Success())
		return res, nil
	}
	if kind := CooldownFailureKind(err, ctx); kind != "" {
		j.w.tellCooldown(j.ctx, j.cfg, j.w.learn.cooldown.Failure(kind, JudgeCooldownSettings{FailuresBeforeCooldown: j.cfg.Judge.FailuresBeforeCooldown, CooldownMs: j.cfg.Judge.CooldownMs}))
	}
	return res, err
}

// watchJudge puts the cooldown around a judge.
func (w *warden) watchJudge(ctx sdk.Context, cfg Config, inner Judge) Judge {
	if inner == nil {
		return nil
	}
	return &cooldownJudge{inner: inner, w: w, ctx: ctx, cfg: cfg}
}

// cooldownActive is true while judgments are paused; judgeFor then answers as with no judge at all, so no guard
// needs to know a cooldown exists.
func (w *warden) cooldownActive() bool {
	if w.learn.cooldown.Active() {
		w.bump(func(s *stats) { s.CooldownSkips++ })
		return true
	}
	return false
}

func (w *warden) tellCooldown(ctx sdk.Context, cfg Config, e *CooldownEvent) {
	if e == nil || !ctx.HasUI() {
		return
	}
	if e.Type == "recovered" {
		ctx.Notify("warden: judgments resumed.", "info")
		return
	}
	seconds := jsRound(float64(e.Ms) / 1000)
	remedy := ""
	switch {
	case e.Kind == FailureAuth && cfg.Backend == BackendTypeSafe:
		remedy = " Set TYPESAFE_API_KEY or run /warden enable."
	case e.Kind == FailureAuth || e.Kind == FailureConfiguration:
		remedy = " /warden status shows the backend setup."
	}
	ctx.Notify("warden: judgments paused for "+strconv.Itoa(seconds)+"s after a "+string(e.Kind)+" failure; pattern checks run alone until then."+remedy, "warning")
}

func init() {
	// The rules lane's hook (rules_wire.go): a rules verdict lands in the hold log as tool "rules".
	holdsRecordRules = func(w *warden, v RulesVerdict) {
		findings := make([]RulesFinding, 0, len(v.Findings))
		for _, f := range v.Findings {
			findings = append(findings, RulesFinding{Name: f.Name, Violation: f.Violation})
		}
		w.learn.ledger.RecordRules(RulesRecord{Source: v.Source, Findings: findings, Error: v.Error})
	}
}

// agentEndWithLearning: when no guarded call carried the regret question this run (the agent only replied), the offline
// heuristic reads the prompt at the end of the run (extension.ts agent_end); the guard's own pass follows.
func (w *warden) agentEndWithLearning(ctx sdk.Context, data map[string]any) (any, error) {
	if cfg := w.config(ctx); cfg.Enabled {
		w.learn.mu.Lock()
		pending := len(w.learn.regretCandidates) > 0
		w.learn.mu.Unlock()
		if pending {
			prompt, _ := w.latestUserPrompt(ctx)
			w.settleRegret(cfg, RegretResult{Regretted: TextRegrets(prompt), Via: ViaText})
		}
	}
	return w.onAgentEnd(ctx, data)
}

// holdAdapter is the tools lane's holdRecords: the held calls of the session with their outcomes (extension.ts
// evidenceSnapshot: the records with held true, their tool, excerpt and outcome).
type holdAdapter struct{ ledger *HoldLedger }

func (a holdAdapter) heldRecords() []HeldEntry {
	calls := a.ledger.HeldCalls()
	out := make([]HeldEntry, len(calls))
	for i, c := range calls {
		out[i] = HeldEntry{Tool: c.Tool, Preview: c.Preview, Outcome: c.Outcome}
	}
	return out
}
