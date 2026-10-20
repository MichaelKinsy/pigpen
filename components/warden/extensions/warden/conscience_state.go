package warden

import (
	"context"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The conscience's state machine (extension.ts, the conscience hooks): one assessment at each prompt, a
// generation counter that makes an assessment stale when the operator moves on, and the observations that follow
// a recommendation (the tool attempted, the skill file read, a failure that earns one re-assessment, one reminder
// at the end of the run). The assessment itself is assess (conscience.go); delivery is gated by the measured
// policy (conscience_load.go). Every hook is called once from the shared handlers and marked CONSCIENCE-LANE HOOK.

// conscienceType is the customType of the conscience's message; the original's, so a session that holds one reads
// the same on either.
const conscienceType = "pi-warden-conscience"

const (
	instructionNone     = "none"
	instructionQueued   = "queued"
	instructionSupplied = "instructions_supplied"
)

type capabilityRef struct{ kind, id string }

// conscienceState is the per-session and per-prompt state. It has its own lock: handlers that hold the warden's
// lock never reach it, and a judge request is never made with it held.
type conscienceState struct {
	mu                    sync.Mutex
	generation            int
	selected, pending     *capabilityRef
	triggerConsumed       bool
	assessments, nudges   int
	reminderSent          bool
	lastHash              string
	skills                []Skill
	loadedBytes           int
	instruction           string
	loadedNames           map[string]bool
	queuedRevision        int
	beforeAgentStartFired bool
	needsReassessment     bool
	globalIndex           *IndexFile
	projectIndex          *IndexFile
	indexNudged           bool
	indexRunning          bool
	indexWritePaths       []string
}

func (s *conscienceState) gen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.generation
}

// bumpGeneration makes every assessment in flight stale.
func (s *conscienceState) bumpGeneration() {
	s.mu.Lock()
	s.generation++
	s.mu.Unlock()
}

// conscienceOn: the conscience runs only when warden is on and the conscience is enabled in its configuration.
func conscienceOn(cfg Config) bool { return cfg.Enabled && cfg.Conscience.Enabled }

// jsToFixed is Number.prototype.toFixed: of the two candidates nearest the exact value the larger wins a tie.
func jsToFixed(x float64, digits int) string {
	r := new(big.Rat).SetFloat64(x)
	scale := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(digits)), nil))
	r.Mul(r, scale)
	neg := r.Sign() < 0
	if neg {
		r.Neg(r)
	}
	floor := new(big.Int).Quo(r.Num(), r.Denom())
	frac := new(big.Rat).Sub(r, new(big.Rat).SetInt(floor))
	if frac.Cmp(big.NewRat(1, 2)) >= 0 {
		floor.Add(floor, big.NewInt(1))
	}
	s := floor.String()
	if digits > 0 {
		for len(s) <= digits {
			s = "0" + s
		}
		s = s[:len(s)-digits] + "." + s[len(s)-digits:]
	}
	if neg && strings.Trim(s, "0.") != "" {
		s = "-" + s
	}
	return s
}

// ---------------------------------------------------------------------------
// Session start.

// conscienceSessionStart resets the session's state and loads the capability indexes (once per session; every
// /warden index run overwrites them). A reload or a new session makes assessments in flight stale.
func (w *warden) conscienceSessionStart(ctx sdk.Context) {
	idx := w.indexDirsOf()
	global := readIndex(idx.path("global", ""))
	project := readIndex(idx.path("project", ctx.Cwd()))
	s := &w.conscience
	s.mu.Lock()
	s.generation = 0
	s.globalIndex, s.projectIndex = global, project
	s.indexNudged, s.indexRunning, s.indexWritePaths = false, false, nil
	s.mu.Unlock()
}

// indexDirsOf says where this session's index files are.
func (w *warden) indexDirsOf() indexDirs {
	return indexDirs{Getenv: w.opts.Getenv, DataDir: w.dataDirOf()}
}

// ---------------------------------------------------------------------------
// Inputs of an assessment.

// skillsOf reads the skill catalog from a before_agent_start event's system prompt options.
func skillsOf(data map[string]any) []Skill {
	opts, _ := data["systemPromptOptions"].(map[string]any)
	raw, _ := opts["skills"].([]any)
	var out []Skill
	for _, r := range raw {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		s := Skill{}
		s.Name, _ = m["name"].(string)
		s.Description, _ = m["description"].(string)
		s.FilePath, _ = m["filePath"].(string)
		s.DisableModelInvocation, _ = m["disableModelInvocation"].(bool)
		out = append(out, s)
	}
	return out
}

func skillsFromHost(skills []sdk.SystemPromptSkill) []Skill {
	out := make([]Skill, len(skills))
	for i, s := range skills {
		out[i] = Skill{Name: s.Name, Description: s.Description, FilePath: s.FilePath, DisableModelInvocation: s.DisableModelInvocation}
	}
	return out
}

// capabilityTools is getAllTools as the conscience reads it: names and descriptions; a failed read is no tools.
func capabilityTools(ctx sdk.Context) []CapabilityTool {
	infos, err := ctx.GetAllTools()
	if err != nil {
		return nil
	}
	out := make([]CapabilityTool, len(infos))
	for i, t := range infos {
		out[i] = CapabilityTool{Name: t.Name, Description: t.Description}
	}
	return out
}

func skillNames(skills []Skill) []string {
	out := make([]string, len(skills))
	for i, s := range skills {
		out[i] = s.Name
	}
	return out
}

// spineOfBranch is the task spine of the session branch and the prompt the assessment is about.
func spineOfBranch(branch []sdk.BranchEntry, prompt string) *TaskSpine {
	var msgs []BranchMessage
	for _, e := range branch {
		if e.Type == "message" {
			msgs = append(msgs, BranchMessage{Type: "message", Role: e.Role, Text: e.Content})
		}
	}
	return TaskSpineOf(msgs, prompt)
}

// recentContext is up to four of the last six branch entries that are user or assistant text, redacted and cut to
// 500 characters each.
func recentContext(branch []sdk.BranchEntry) string {
	if len(branch) > 6 {
		branch = branch[len(branch)-6:]
	}
	var lines []string
	for _, e := range branch {
		if len(lines) >= 4 {
			break
		}
		if e.Type != "message" || (e.Role != "user" && e.Role != "assistant") || e.Content == "" {
			continue
		}
		lines = append(lines, e.Role+": "+utf16Slice(Redact(e.Content), 0, 500))
	}
	return strings.Join(lines, "\n")
}

// judgeModel remembers the model that answered a request: the activation gate compares it with the policy's. It is
// empty until the first answer, and an empty model never matches a policy, so the gate fails closed.
type judgeModel struct {
	inner Judge
	mu    sync.Mutex
	model string
}

func (j *judgeModel) Evaluate(ctx context.Context, req Request) (Evaluation, error) {
	ev, err := j.inner.Evaluate(ctx, req)
	if err == nil {
		j.mu.Lock()
		j.model = ev.Model
		j.mu.Unlock()
	}
	return ev, err
}

func (j *judgeModel) get() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.model
}

func wrapJudge(j Judge) (Judge, *judgeModel) {
	if j == nil {
		return nil, &judgeModel{}
	}
	jm := &judgeModel{inner: j}
	return jm, jm
}

// judgmentsOffReason says why no judge is available: consent, a budget spent, or a backend that could not start.
func (w *warden) judgmentsOffReason(cfg Config) string {
	// While the judge cooldown pauses judgments the extension names no reason, and assess says no_consent.
	if w.learn.cooldown.Active() {
		return ""
	}
	if cfg.Backend == BackendNone || cfg.Consent != cfg.Backend {
		return SkipNoConsent
	}
	w.mu.Lock()
	exhausted := w.budgetExhausted
	w.mu.Unlock()
	switch {
	case exhausted:
		return SkipBudget
	case cfg.Backend == BackendOwnModel:
		return SkipBadBackend
	}
	return SkipNoKey
}

func (w *warden) conscienceDeps(cfg Config, judge Judge) ConscienceDeps {
	w.conscience.mu.Lock()
	g, p := w.conscience.globalIndex, w.conscience.projectIndex
	w.conscience.mu.Unlock()
	return ConscienceDeps{Judge: judge, Config: cfg.Conscience, SharedTimeoutMs: cfg.TimeoutMs, GlobalIndex: g, ProjectIndex: p}
}

func capabilityName(c *Candidate) string {
	if c == nil {
		return "none"
	}
	return c.Kind + ":" + c.ID
}

func skipOrNone(r string) string {
	if r == "" {
		return "none"
	}
	return r
}

var reExplicitSkill = lazyRE(`/skill:([\w-]+)`)

// ---------------------------------------------------------------------------
// before_agent_start: the first assessment of a prompt, and its delivery.

// conscienceBeforeAgentStart assesses the operator's prompt against the session's skills and tools, and delivers
// the recommendation (or, in load mode, the skill's text) as the message the hook returns. CONSCIENCE-LANE HOOK:
// called once at the end of onBeforeAgentStart.
func (w *warden) conscienceBeforeAgentStart(ctx sdk.Context, data map[string]any) (any, error) {
	cfg := w.config(ctx)
	s := &w.conscience
	s.mu.Lock()
	s.beforeAgentStartFired = true
	s.mu.Unlock()
	if !conscienceOn(cfg) {
		return nil, nil
	}
	s.mu.Lock()
	myGeneration := s.generation
	s.selected, s.pending = nil, nil
	s.triggerConsumed, s.assessments, s.nudges, s.reminderSent = false, 0, 0, false
	s.lastHash, s.needsReassessment = "", false
	s.loadedBytes, s.instruction = 0, instructionNone
	s.loadedNames = map[string]bool{}
	s.queuedRevision++
	s.mu.Unlock()
	clearFileIdentityCache()

	judge, jm := wrapJudge(w.judgeFor(ctx, cfg))
	skills := skillsOf(data)
	s.mu.Lock()
	s.skills = skills
	s.mu.Unlock()
	tools := capabilityTools(ctx)
	prompt, _ := data["prompt"].(string)

	if m := reExplicitSkill.FindStringSubmatch(prompt); m != nil {
		w.record(ctx, cfg, GuardConscience, "explicit skill invocation: "+m[1], []string{"trigger: input", "explicit_skill: " + m[1]}, nil)
		return nil, nil
	}
	// The prompt leaves redacted and cut to 2000 characters (spec section 6 payload limit).
	redactedPrompt := utf16Slice(Redact(prompt), 0, 2000)
	var truncated []string
	if n := utf16Len(prompt); n > 2000 {
		truncated = []string{"prompt truncated from " + itoaInt(n) + " to 2000 chars"}
	}
	branch := w.branchOf(ctx)
	spine := spineOfBranch(branch, prompt)
	recent := recentContext(branch)
	w.conscienceIndexNudge(ctx, cfg, skills)

	effectiveTimeout := min(cfg.Conscience.TimeoutMs, cfg.TimeoutMs)
	start := time.Now()
	deps := w.conscienceDeps(cfg, judge)
	deps.JudgmentsOff = w.judgmentsOffReason(cfg)
	result := assess(context.Background(), redactedPrompt, recent, skills, tools, skillNames(skills), nil, deps, spine)
	if s.gen() != myGeneration {
		w.record(ctx, cfg, GuardConscience, "stale: generation changed during assessment",
			[]string{"trigger: before_agent_start", "generation: " + itoaInt(myGeneration) + " → " + itoaInt(s.gen()), "skipReason: stale"}, nil)
		return nil, nil
	}
	details := []string{"trigger: before_agent_start", "eligible: " + itoaInt(len(skills)) + " skills, " + itoaInt(len(tools)) + " tools", "requests: " + itoaInt(result.RequestCount), "questionHash: " + result.QuestionHash}
	if result.SkipReason != "" {
		details = append(details, "skipReason: "+result.SkipReason)
	}
	details = append(details, truncated...)
	skipTag := ""
	if result.SkipReason != "" {
		skipTag = ", " + result.SkipReason
	}
	w.record(ctx, cfg, GuardConscience,
		"assessed "+itoaInt(len(skills))+" skills + "+itoaInt(len(tools))+" tools → "+capabilityName(result.Selected)+" (P(useful)="+jsToFixed(result.Usefulness, 2)+", P(advance)="+jsToFixed(result.PAdvance, 2)+", "+itoaInt(result.ElapsedMs)+"ms"+skipTag+")",
		details, nil)
	s.mu.Lock()
	s.assessments++
	s.lastHash = result.QuestionHash
	if result.Selected != nil {
		s.selected = &capabilityRef{result.Selected.Kind, result.Selected.ID}
		s.pending = &capabilityRef{result.Selected.Kind, result.Selected.ID}
		s.triggerConsumed = false
	}
	s.mu.Unlock()
	if result.Selected != nil && result.Selected.Kind == "skill" {
		// The file's identity at selection time: a replacement before the load is noticed (rule 4).
		for _, sk := range skills {
			if sk.Name == result.Selected.ID {
				if sk.FilePath != "" {
					recordFileIdentity(sk.Name, sk.FilePath)
				}
				break
			}
		}
	}
	if result.Selected == nil {
		return nil, nil
	}
	if !w.budgetAvailable(cfg) {
		w.record(ctx, cfg, GuardConscience, "selected but budget exhausted", []string{"trigger: before_agent_start", "skipReason: budget"}, nil)
		return nil, nil
	}
	// The activation gate (spec section 7): no policy, or a policy that does not match the current hash and the model
	// that actually answered, means no delivery. Fail closed either way.
	model := jm.get()
	if !policyMatches(conscienceBetaPolicy, result.QuestionHash, model) {
		shown := model
		if shown == "" {
			shown = "none"
		}
		w.record(ctx, cfg, GuardConscience, "no policy for current hash/model", []string{"trigger: before_agent_start", "skipReason: no_policy", "hash: " + result.QuestionHash, "model: " + shown}, nil)
		return nil, nil
	}
	w.spendBudgetUnit()
	if result.Selected.Kind == "skill" && cfg.Conscience.Skills.Mode == "load" {
		s.mu.Lock()
		already := s.loadedNames[result.Selected.ID]
		s.mu.Unlock()
		if !already {
			if msg, ok := w.conscienceLoad(ctx, cfg, data, result, effectiveTimeout, start); ok {
				return msg, nil
			}
		}
	}
	// Recommend mode, or the fallback of a load that did not happen.
	content := "Consider using the \"" + result.Selected.ID + "\" " + result.Selected.Kind + ": " + result.Selected.Description
	return conscienceMessage(cfg, content), nil
}

func conscienceMessage(cfg Config, content string) map[string]any {
	return map[string]any{"message": map[string]any{"customType": conscienceType, "content": content, "display": cfg.SteerVisible}}
}

// conscienceLoad is load mode: read the selected skill's file and supply its text. It reports whether a message
// was made; a refused load is traced with its reason and the recommendation is sent instead.
func (w *warden) conscienceLoad(ctx sdk.Context, cfg Config, data map[string]any, result AssessmentResult, effectiveTimeout int, start time.Time) (any, bool) {
	s := &w.conscience
	s.mu.Lock()
	var skill *Skill
	for i := range s.skills {
		if s.skills[i].Name == result.Selected.ID {
			skill = &s.skills[i]
			break
		}
	}
	loaded := s.loadedBytes
	s.mu.Unlock()
	if skill == nil {
		return nil, false
	}
	var window *int
	if usage, err := ctx.GetContextUsage(); err == nil && usage != nil {
		window = &usage.ContextWindow
	}
	trusted, err := ctx.IsProjectTrusted()
	if err != nil {
		w.record(ctx, cfg, GuardConscience, "error: other", []string{"trigger: before_agent_start", "skipReason: error"}, nil)
		return nil, false
	}
	images, hasImages := data["images"]
	res := loadSkillBody(*skill, cfg.Conscience, LoadOpts{
		PathRules: cfg.Action.PathRules, ExemptRules: cfg.Action.ExemptRules, LoadedBytes: loaded,
		RemainingMs:  effectiveTimeout - int(time.Since(start).Milliseconds()),
		ConsentGiven: cfg.Backend != BackendNone && cfg.Consent == cfg.Backend, ProjectTrusted: trusted,
		CatalogName: skill.Name, CatalogDesc: skill.Description, ContextWindow: window, HasImages: hasImages && images != nil,
	})
	if res.Body != nil && *res.Body != "" {
		s.mu.Lock()
		s.loadedBytes += res.BytesLoaded
		s.loadedNames[result.Selected.ID] = true
		s.instruction = instructionQueued
		s.mu.Unlock()
		w.record(ctx, cfg, GuardConscience, "loaded: "+result.Selected.ID+" ("+itoaInt(res.BytesLoaded)+" bytes)", []string{"trigger: before_agent_start", "skipReason: none", "delivery: instructions_supplied"}, nil)
		return conscienceMessage(cfg, buildLoadMessage(res)), true
	}
	reason := res.SkipReason
	if reason == "" {
		reason = "undefined"
	}
	w.record(ctx, cfg, GuardConscience, "load failed: "+result.Selected.ID, []string{"trigger: before_agent_start", "skipReason: " + reason}, nil)
	return nil, false
}

// conscienceIndexNudge says once per session that the capability index is missing or stale.
func (w *warden) conscienceIndexNudge(ctx sdk.Context, cfg Config, skills []Skill) {
	s := &w.conscience
	const nudge = "pi-warden: run /warden index so the conscience recommends skills and tools from real descriptions."
	s.mu.Lock()
	nudged, global, project := s.indexNudged, s.globalIndex, s.projectIndex
	s.mu.Unlock()
	if nudged {
		return
	}
	say := func(line string, details []string) {
		s.mu.Lock()
		s.indexNudged = true
		s.mu.Unlock()
		if ctx.HasUI() {
			ctx.Notify(nudge, "info")
		} else {
			w.record(ctx, cfg, GuardConscience, line, details, nil)
		}
	}
	if global == nil || project == nil {
		say("nudge: index missing", []string{"trigger: before_agent_start", "nudgeReason: missing_index"})
		return
	}
	var missing []string
	for _, sk := range skills {
		if sk.DisableModelInvocation {
			continue
		}
		hash := "missing"
		if sk.FilePath != "" {
			hash = fileContentHash(sk.FilePath)
		}
		if findIndexEntry(project, sk.Name, hash) == nil && findIndexEntry(global, sk.Name, hash) == nil {
			missing = append(missing, sk.Name)
		}
	}
	if len(missing) > 0 {
		details := []string{"trigger: before_agent_start", "nudgeReason: stale_index"}
		for _, n := range missing {
			details = append(details, "unindexed: "+n)
		}
		say("nudge: "+itoaInt(len(missing))+" skill(s) unindexed", details)
	}
}

// ---------------------------------------------------------------------------
// message_start: delivery observed, and the queued prompt.

// conscienceMessageStart notes that a delivered instruction reached the conversation, and assesses a prompt that
// was queued while the agent was busy (it arrives without a before_agent_start). CONSCIENCE-LANE HOOK: the
// message_start handler.
func (w *warden) conscienceMessageStart(ctx sdk.Context, data map[string]any) (any, error) {
	msg, _ := data["message"].(map[string]any)
	role, _ := msg["role"].(string)
	cfg := w.config(ctx)
	s := &w.conscience
	if role == "assistant" {
		if custom, _ := msg["customType"].(string); custom == conscienceType {
			s.mu.Lock()
			queued, revision := s.instruction == instructionQueued, s.queuedRevision
			if queued && conscienceOn(cfg) {
				s.instruction = instructionSupplied
			}
			s.mu.Unlock()
			if queued && conscienceOn(cfg) {
				w.record(ctx, cfg, GuardConscience, "instructions_supplied: delivery observed", []string{"trigger: message_start", "revision: " + itoaInt(revision)}, nil)
			}
		}
		return nil, nil
	}
	if role != "user" {
		return nil, nil
	}
	s.mu.Lock()
	fired, skills := s.beforeAgentStartFired, s.skills
	s.mu.Unlock()
	if conscienceOn(cfg) && !fired {
		// A queued prompt: assessed against the last skill snapshot and the current tools; the result is traced only.
		judge := w.judgeFor(ctx, cfg)
		if judge != nil && len(skills) > 0 {
			myGeneration := s.gen()
			prompt, _ := msg["content"].(string)
			redacted := utf16Slice(Redact(prompt), 0, 2000)
			spine := spineOfBranch(w.branchOf(ctx), prompt)
			deps := w.conscienceDeps(cfg, judge)
			result := assess(context.Background(), redacted, "", skills, capabilityTools(ctx), skillNames(skills), nil, deps, spine)
			if s.gen() == myGeneration {
				w.record(ctx, cfg, GuardConscience, "queued prompt assessed → "+capabilityName(result.Selected)+" ("+skipOrNone(result.SkipReason)+")",
					[]string{"trigger: message_start", "origin: queued", "skipReason: " + skipOrNone(result.SkipReason)}, nil)
			}
		} else {
			w.record(ctx, cfg, GuardConscience, "queued prompt: no judge or no skills", []string{"trigger: message_start", "origin: queued", "skipReason: catalog_unavailable"}, nil)
		}
	}
	s.mu.Lock()
	s.beforeAgentStartFired = false
	s.mu.Unlock()
	return nil, nil
}

// ---------------------------------------------------------------------------
// turn_end: one re-assessment after a failure.

// conscienceTurnEnd re-assesses once when the selected capability failed. CONSCIENCE-LANE HOOK: called from the
// turn_end handler.
func (w *warden) conscienceTurnEnd(ctx sdk.Context) {
	cfg := w.config(ctx)
	s := &w.conscience
	s.mu.Lock()
	run := conscienceOn(cfg) && s.selected != nil && s.needsReassessment && s.assessments < cfg.Conscience.MaxAssessments
	myGeneration, skills := s.generation, s.skills
	s.mu.Unlock()
	if !run {
		return
	}
	judge := w.judgeFor(ctx, cfg)
	if judge == nil {
		return
	}
	prompt, _ := w.latestUserPrompt(ctx)
	deps := w.conscienceDeps(cfg, judge)
	result := assess(context.Background(), utf16Slice(Redact(prompt), 0, 2000), "", skills, capabilityTools(ctx), skillNames(skills), nil, deps, spineOfBranch(w.branchOf(ctx), prompt))
	if s.gen() != myGeneration {
		return
	}
	s.mu.Lock()
	s.assessments++
	s.needsReassessment = false
	if result.Selected != nil {
		s.selected = &capabilityRef{result.Selected.Kind, result.Selected.ID}
		s.lastHash = result.QuestionHash
	}
	count := s.assessments
	s.mu.Unlock()
	w.record(ctx, cfg, GuardConscience, "turn_end reassessment → "+capabilityName(result.Selected)+" ("+itoaInt(result.ElapsedMs)+"ms, "+skipOrNone(result.SkipReason)+")",
		[]string{"trigger: turn_end", "assessments: " + itoaInt(count) + "/" + itoaInt(cfg.Conscience.MaxAssessments), "skipReason: " + skipOrNone(result.SkipReason)}, nil)
}

// ---------------------------------------------------------------------------
// tool_call and tool_result: what the agent did with the recommendation.

// conscienceToolCall notes the agent reaching for the selected tool, or reading the selected skill's file.
// CONSCIENCE-LANE HOOK: called from onToolCall before the action guard's own filter.
func (w *warden) conscienceToolCall(ctx sdk.Context, cfg Config, tool string, input map[string]any) {
	if !cfg.Conscience.Enabled {
		return
	}
	s := &w.conscience
	s.mu.Lock()
	sel, consumed := s.selected, s.triggerConsumed
	s.mu.Unlock()
	if sel == nil || consumed {
		return
	}
	switch {
	case sel.kind == "tool" && tool == sel.id:
		s.mu.Lock()
		s.pending = &capabilityRef{"tool", tool}
		s.mu.Unlock()
		w.record(ctx, cfg, GuardConscience, "tool_attempted: "+tool, []string{"trigger: tool_call", "capability: " + sel.id}, nil)
	case sel.kind == "skill" && tool == "read":
		rawPath := ""
		if p, present := input["path"]; present && p != nil {
			rawPath = jsString(p)
		}
		if strings.Contains(rawPath, sel.id) {
			s.mu.Lock()
			s.pending = &capabilityRef{"skill", sel.id}
			s.mu.Unlock()
			w.record(ctx, cfg, GuardConscience, "read_observed: "+sel.id, []string{"trigger: tool_call", "path: " + Redact(rawPath)}, nil)
		}
	}
}

// conscienceToolResult settles the pending observation. A failure earns one re-assessment at the next turn end.
// CONSCIENCE-LANE HOOK: called from onToolResult.
func (w *warden) conscienceToolResult(ctx sdk.Context, cfg Config, tool string, failed bool) {
	if !cfg.Conscience.Enabled {
		return
	}
	s := &w.conscience
	s.mu.Lock()
	pending, consumed := s.pending, s.triggerConsumed
	s.mu.Unlock()
	if pending == nil || consumed {
		return
	}
	switch {
	case pending.kind == "tool" && tool == pending.id:
		s.mu.Lock()
		s.triggerConsumed = true
		if failed {
			s.needsReassessment = true
		}
		s.pending = nil
		s.mu.Unlock()
		line := "tool_succeeded: "
		if failed {
			line = "tool_failed: "
		}
		w.record(ctx, cfg, GuardConscience, line+tool, []string{"trigger: tool_result", "capability: " + pending.id}, nil)
	case pending.kind == "skill" && tool == "read":
		s.mu.Lock()
		s.triggerConsumed = true
		if failed {
			s.needsReassessment = true
		}
		s.pending = nil
		s.mu.Unlock()
		w.record(ctx, cfg, GuardConscience, "read_observed: "+pending.id, []string{"trigger: tool_result", "failed: " + strconv.FormatBool(failed)}, nil)
	}
}

// ---------------------------------------------------------------------------
// agent_end and agent_settled.

// conscienceAgentEnd sends one reminder when the run ended without answering, the capability was never used, and
// a fresh assessment still selects it through the same gate. CONSCIENCE-LANE HOOK: called from onAgentEnd after
// the loops reminder.
func (w *warden) conscienceAgentEnd(ctx sdk.Context, cfg Config, data map[string]any) {
	s := &w.conscience
	judge := Judge(nil)
	s.mu.Lock()
	candidate := cfg.Conscience.Enabled && s.selected != nil && !s.reminderSent && !s.triggerConsumed &&
		s.assessments < cfg.Conscience.MaxAssessments && s.nudges < cfg.Conscience.MaxNudges
	s.mu.Unlock()
	if !candidate {
		return
	}
	// A run that ended with a final text reply has answered: a reminder then revisits a finished answer.
	if final, ok := finalAssistantTextOf(data); ok && final != "" {
		return
	}
	if !w.budgetAvailable(cfg) {
		return
	}
	judge = w.judgeFor(ctx, cfg)
	if judge == nil {
		return
	}
	wrapped, jm := wrapJudge(judge)
	s.mu.Lock()
	myGeneration, skills, sel := s.generation, s.skills, *s.selected
	s.mu.Unlock()
	prompt, _ := w.latestUserPrompt(ctx)
	deps := w.conscienceDeps(cfg, wrapped)
	result := assess(context.Background(), utf16Slice(Redact(prompt), 0, 2000), "", skills, capabilityTools(ctx), skillNames(skills), nil, deps, spineOfBranch(w.branchOf(ctx), prompt))
	if s.gen() != myGeneration {
		return
	}
	s.mu.Lock()
	s.assessments++
	s.mu.Unlock()
	// The same activation gate as the first recommendation: the selection is set before that gate, so without this a
	// capability held for no_policy would be delivered here.
	if result.Selected == nil || result.Selected.Kind != sel.kind || result.Selected.ID != sel.id || result.Disposition == DispositionAwaitingUser ||
		!policyMatches(conscienceBetaPolicy, result.QuestionHash, jm.get()) || !w.adaptiveSend(ctx, cfg, SteerKindConscience) {
		return
	}
	s.mu.Lock()
	s.reminderSent = true
	s.nudges++
	s.mu.Unlock()
	w.spendBudgetUnit()
	msg := "Reminder: consider using the \"" + result.Selected.ID + "\" " + result.Selected.Kind + ". " + result.Selected.Description
	if w.steer(cfg, []SteerGuard{SteerGuardConscience}, ctx, msg, SteerOptions{DeliverAs: "followUp"}) {
		kind := "tool"
		if result.Selected.Kind == "skill" {
			kind = "skill"
		}
		w.watchSteer(ctx, cfg, []SteerKind{SteerKindConscience}, SteerSubject{Capability: &SteerCapability{Kind: kind, ID: result.Selected.ID}}, false)
	}
}

// finalAssistantTextOf is the text of the run's last assistant message, from the agent_end event.
func finalAssistantTextOf(data map[string]any) (string, bool) {
	var messages []map[string]any
	if raw, ok := data["messages"].([]any); ok {
		for _, m := range raw {
			if mm, ok := m.(map[string]any); ok {
				messages = append(messages, mm)
			}
		}
	}
	return FinalAssistantText(messages)
}

// conscienceSettled finalizes the prompt's trace line once the agent is idle. CONSCIENCE-LANE HOOK: the
// agent_settled handler.
func (w *warden) conscienceSettled(ctx sdk.Context) {
	cfg := w.config(ctx)
	s := &w.conscience
	s.mu.Lock()
	sel := s.selected
	status := "unresolved"
	if s.triggerConsumed {
		status = "consumed"
	} else if s.reminderSent {
		status = "reminded"
	}
	assessments, nudges, instruction := s.assessments, s.nudges, s.instruction
	s.mu.Unlock()
	if !conscienceOn(cfg) || sel == nil {
		return
	}
	w.record(ctx, cfg, GuardConscience, "settled: "+status+" ("+sel.kind+":"+sel.id+")",
		[]string{"trigger: agent_settled", "assessments: " + itoaInt(assessments), "nudges: " + itoaInt(nudges), "instructions: " + instruction}, nil)
}

// ---------------------------------------------------------------------------
// input.

// conscienceIndexBusy is true while /warden index runs: the supervision module swallows new input with a notice
// (extension.ts 2268). CONSCIENCE-LANE HOOK: registered with w.sup.blockInput.
func (w *warden) conscienceIndexBusy() bool {
	s := &w.conscience
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.indexRunning
}

// conscienceInputPass makes the assessments in flight stale when the operator types (extension.ts 2273).
// CONSCIENCE-LANE HOOK: registered with w.sup.onInputPass.
func (w *warden) conscienceInputPass(sdk.Context) { w.conscience.bumpGeneration() }
