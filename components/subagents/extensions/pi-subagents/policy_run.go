package pi_subagents

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The run policy of a foreground single-agent run: the execution options of the subagent tool that gate and shape a run
// (output file and mode, artifacts, acceptance and gate, deadline, spawn budget, capability ceiling), applied around the child
// process the port starts. applyRunPolicy runs before the child, finishRun after it.
//
// Not run here, so refused by the registry (refuse.go): outputSchema (the child calls a `structured_output` tool; a pig child
// started with --no-extensions cannot host one, needs-CLI/SDK), toolBudget and toolTimeoutMs (they block or time out individual
// tool calls inside the child), usageBudget, quiet (schedules only).

func init() {
	registerRunParams("output", "outputMode", "artifacts", "acceptance", "gate", "agentContract", "timeoutMs", "maxRuntimeMs", "includeProgress")
	registerAction("grant-spawn-budget", []string{"additional"}, grantSpawnBudgetAction)
}

// asyncUnsupportedPolicyParam is the first policy option a background run does not apply ("" when none is set): the options
// shape the foreground run (applyRunPolicy), so a background run refuses them rather than ignore them.
func asyncUnsupportedPolicyParam(params map[string]any) string {
	for _, p := range []string{"output", "outputMode", "artifacts", "acceptance", "gate", "agentContract", "includeProgress"} {
		if v, ok := params[p]; defined(params, p) && !(p == "output" && ok && v == true) { // the public boundary defaults output to true
			return p
		}
	}
	return ""
}

var (
	runPolicyMu sync.Mutex
	// runState holds the spawn counters of the session (SubagentState.subagentSpawns).
	runState = &subagentState{}
	// activeRuns counts the children of this session that are queued or running.
	activeRuns atomic.Int32
	// loadExtensionConfig is the extension config the policy reads: the original's config.json (<agent dir>/extensions/subagent),
	// whose spawn limits and timeouts the environment variables (PI_SUBAGENT_MAX_SPAWNS_PER_SESSION and friends) override.
	loadExtensionConfig = readExtensionConfigFile
)

// readExtensionConfigFile reads the policy keys of config.json; a missing or malformed file is an empty config (the background
// run reports a malformed file itself).
func readExtensionConfigFile() extensionConfig {
	var raw map[string]any
	if readJSONFile(asyncConfigPath(), &raw) != nil {
		return extensionConfig{}
	}
	cfg := extensionConfig{MaxSubagentSpawnsPerSession: raw["maxSubagentSpawnsPerSession"], MaxSubagentSpawnsPerRun: raw["maxSubagentSpawnsPerRun"], ToolTimeoutMs: raw["toolTimeoutMs"], TimeoutMs: raw["timeoutMs"]}
	if s, ok := raw["defaultSubagentContext"].(string); ok && (s == "fresh" || s == "fork") {
		cfg.DefaultSubagentContext = s
	}
	cfg.IntercomBridge, cfg.Permissions = raw["intercomBridge"], raw["permissions"]
	return cfg
}

// sessionIDOf is the logical session id ("" when the host has none).
func sessionIDOf(ctx sdk.Context) string {
	id, err := ctx.GetSessionID()
	if err != nil {
		return ""
	}
	return id
}

// runPolicy is the resolved policy of one run.
type runPolicy struct {
	Agent        *AgentConfig // a copy with the policy's system prompt additions and tool limits
	Task         string       // the task the child gets (with the output instruction)
	OriginalTask string
	Cwd          string
	RunID        string

	TimeoutMs int

	OutputPath, OutputMode, ClaimPath string
	Snapshot                          *singleOutputSnapshot

	Acceptance     *resolvedAcceptance
	AgentContract  bool
	StagedBaseline string
	Mutations      trackedMutationSnapshot

	ArtifactsDir    string
	IncludeProgress bool
	Started         time.Time
}

// applyRunPolicy validates the policy options of a call and prepares the run; an error is the tool error text.
func applyRunPolicy(ctx sdk.Context, params map[string]any, a *AgentConfig, task, cwd string) (*runPolicy, error) {
	pol := &runPolicy{OriginalTask: task, Cwd: cwd, RunID: newUUID(), Started: time.Now()}
	agent := *a
	pol.Agent = &agent

	// capability ceiling: the agent must be allowed, its tools are narrowed to the allowed ones
	sessionID := sessionIDOf(ctx)
	ceiling := resolveSubagentCapabilityCeiling(sessionID, nil)
	if msg := capabilityCeilingAgentRestrictionMessage(a.Name, ceiling); msg != "" {
		return nil, errors.New(msg)
	}
	if ceiling != nil && ceiling.HasAllowedTools {
		if len(a.Tools) == 0 {
			agent.Tools = append([]string{}, ceiling.AllowedTools...)
		} else {
			agent.Tools = nil
			for _, t := range a.Tools {
				if contains(ceiling.AllowedTools, t) {
					agent.Tools = append(agent.Tools, t)
				}
			}
			if agent.Tools == nil {
				agent.Tools = []string{}
			}
		}
	}

	// acceptance and gate
	acceptanceInput, msg := normalizeGateAcceptance(params["gate"], params["acceptance"])
	if msg != "" {
		return nil, errors.New(msg)
	}
	if errs := validateAcceptanceInput(acceptanceInput, "acceptance"); len(errs) > 0 {
		return nil, errors.New(strings.Join(errs, " "))
	}

	// deadline: the call, then the agent, then the config, then 30 minutes
	cfg := loadExtensionConfig()
	def := resolveConfigDefaultTimeoutMs(cfg.TimeoutMs)
	if a.DefaultTimeoutMs != nil && *a.DefaultTimeoutMs > 0 {
		def = *a.DefaultTimeoutMs
	}
	to := resolveSingleAgentLaunchTimeout(params, false, def)
	if to.err != "" {
		return nil, errors.New(to.err)
	}
	pol.TimeoutMs = to.timeoutMs

	// output file and mode
	pol.OutputMode = a.OutputMode
	if m, ok := params["outputMode"].(string); ok && m != "" {
		pol.OutputMode = m
	}
	defaultOutput := a.Output
	outputPath, disabled, set := normalizeSingleOutputOverride(params["output"], defaultOutput, defaultOutput != "")
	if !set && !disabled && isUndefined(params["output"]) && defaultOutput != "" {
		outputPath, set = defaultOutput, true
	}
	if set && !disabled {
		var requestedCwd string
		if r, ok := params["cwd"].(string); ok {
			requestedCwd = r
		}
		pol.OutputPath = resolveSingleOutputPath(outputPath, ctxCwd(ctx, cwd), requestedCwd, "")
	}
	if msg := validateFileOnlyOutputMode(pol.OutputMode, pol.OutputPath, "Single run ("+a.Name+")"); msg != "" {
		return nil, errors.New(msg)
	}

	// spawn budget of the session
	getenv := osEnv
	runPolicyMu.Lock()
	_, spawnErr := reserveSpawnBudget(runState, cfg, sessionID, 1, getenv)
	runPolicyMu.Unlock()
	if spawnErr != "" {
		return nil, errors.New(spawnErr)
	}

	// agentContract: { version: 1 } is the original's compatibility behavior: no inferred acceptance, a report that is optional
	if v, ok := params["agentContract"]; ok && !isUndefined(v) {
		o, isObj := asObject(v)
		if ver, _ := asNumber(func() any {
			if isObj {
				return o.vals["version"]
			}
			return nil
		}()); !isObj || ver != 1 {
			return nil, errors.New("agentContract must be { version: 1 }.")
		}
		pol.AgentContract = true
	}

	// acceptance contract
	acc, err := resolveEffectiveAcceptance(acceptanceRun{Explicit: acceptanceInput, AgentName: a.Name, AcceptanceRole: a.AcceptanceRole, Task: task, Mode: "single", AgentContract: pol.AgentContract})
	if err != nil {
		return nil, err
	}
	pol.Acceptance = acc
	if acc.PreserveStagedIndex {
		b, err := captureStagedIndexBaseline(cwd)
		if err != nil {
			return nil, err
		}
		pol.StagedBaseline = b
	}
	prompt := formatAcceptancePrompt(acc, acceptancePromptOptions{ReportOptional: pol.AgentContract})

	var caps *outputInstructionCapabilities
	if len(agent.Tools) > 0 || agent.Tools != nil {
		caps = &outputInstructionCapabilities{Tools: agent.Tools, ToolsSet: true, McpDirectTools: agent.McpDirectTools}
	} else if len(agent.McpDirectTools) > 0 {
		caps = &outputInstructionCapabilities{McpDirectTools: agent.McpDirectTools}
	}
	pol.Task = injectSingleOutputInstruction(task, pol.OutputPath, caps)
	system := injectOutputPathSystemPrompt(agent.SystemPrompt, pol.OutputPath, caps)
	if prompt != "" {
		system += "\n" + prompt
	}
	agent.SystemPrompt = system

	if pol.OutputPath != "" {
		pol.Snapshot = captureSingleOutputSnapshot(pol.OutputPath)
		if c, err := resolveSingleOutputClaimPath(pol.OutputPath); err == nil {
			pol.ClaimPath = c
		}
	}
	pol.Mutations = snapshotTrackedMutations(cwd)

	// artifacts (on unless artifacts: false)
	if v, ok := params["artifacts"].(bool); !ok || v {
		sessionFile := ""
		if p, err := ctx.GetSessionFile(); err == nil && p != nil {
			sessionFile = *p
		}
		if dir, err := getArtifactsDir(sessionFile, cwd, "session"); err == nil {
			pol.ArtifactsDir = dir
		}
	}
	if b, ok := params["includeProgress"].(bool); ok {
		pol.IncludeProgress = b
	}
	return pol, nil
}

func ctxCwd(ctx sdk.Context, fallback string) string {
	if c := ctx.Cwd(); c != "" {
		return c
	}
	return fallback
}

// writeInputArtifact stores the task the child was given (best effort, as the original's artifacts).
func (p *runPolicy) writeInputArtifact() {
	if p.ArtifactsDir == "" {
		return
	}
	paths := getArtifactPaths(p.ArtifactsDir, p.RunID, p.Agent.Name, nil)
	_ = writeArtifact(paths.InputPath, p.Task)
}

// runOutcome is what the child gave back.
type runOutcome struct {
	Stdout string
	Err    error
}

func formatFailedSingleRunOutput(errText, displayOutput, outputArtifact string) string {
	if errText == "" {
		errText = "Failed"
	}
	lines := []string{errText}
	if out := jsTrim(displayOutput); out != "" && out != jsTrim(errText) {
		lines = append(lines, "", "Output:", out)
	}
	if outputArtifact != "" {
		if _, err := os.Stat(outputArtifact); err == nil {
			lines = append(lines, "", "Output artifact: "+outputArtifact)
		}
	}
	return strings.Join(lines, "\n")
}

// finishRun turns the child's outcome into the tool's result: the output file, the acceptance verdict, the artifacts.
func finishRun(pol *runPolicy, out runOutcome) (any, error) {
	paths := artifactPaths{}
	if pol.ArtifactsDir != "" {
		paths = getArtifactPaths(pol.ArtifactsDir, pol.RunID, pol.Agent.Name, nil)
	}
	writeOut := func(output, errText string) {
		if pol.ArtifactsDir == "" {
			return
		}
		_ = writeArtifact(paths.OutputPath, formatOutputArtifactContent(output, errText, "", paths.MetadataPath))
	}
	meta := func(exit int, errText string) {
		if pol.ArtifactsDir == "" {
			return
		}
		m := newObject()
		m.set("runId", pol.RunID)
		m.set("agent", pol.Agent.Name)
		m.set("exitCode", float64(exit))
		m.set("durationMs", float64(time.Since(pol.Started).Milliseconds()))
		if errText != "" {
			m.set("error", errText)
		}
		_ = writeMetadata(paths.MetadataPath, m)
	}
	var te *timeoutError
	switch {
	case errors.As(out.Err, &te):
		evidence := collectTrackedMutationEvidence(pol.Mutations, pol.Cwd)
		var outputMissing *bool
		if pol.OutputMode == "file-only" && pol.OutputPath != "" {
			if changed, known := hasSingleOutputChangedSinceSnapshot(pol.OutputPath, pol.Snapshot); known {
				missing := !changed
				outputMissing = &missing
			}
		}
		ap := paths
		recovery := buildTimeoutRecoverySummary(timeoutRecoveryInput{Termination: "timed-out", Evidence: evidence, RequiredOutputMissing: outputMissing, ArtifactPaths: artifactPathsPtr(pol.ArtifactsDir, ap)})
		text := te.Error() + "\n\n" + recovery.Message
		if partial := jsTrim(stripAcceptanceReport(te.Stdout)); partial != "" {
			text += "\n\nPartial output before timeout:\n" + partial
		}
		writeOut(te.Stdout, text)
		meta(1, text)
		return nil, sdk.NewToolError(text)
	case out.Err != nil:
		meta(1, out.Err.Error())
		writeOut(out.Stdout, out.Err.Error())
		return nil, sdk.NewToolError(out.Err.Error())
	}

	acceptanceOutput := out.Stdout
	fullOutput := stripAcceptanceReport(acceptanceOutput)
	var resolved resolvedSingleOutput
	var fileOutput *acceptanceFileOutput
	saveError := ""
	if pol.OutputPath != "" {
		resolved = resolveSingleOutput(pol.OutputPath, fullOutput, pol.Snapshot, pol.ClaimPath)
		if changed, known := hasSingleOutputChangedSinceSnapshot(pol.OutputPath, pol.Snapshot); known && changed && resolved.SavedPath != "" {
			fileOutput = &acceptanceFileOutput{Content: resolved.FullOutput, Path: pol.OutputPath, Authoritative: pol.OutputMode == "file-only", Durable: true}
		}
		fullOutput = stripAcceptanceReport(resolved.FullOutput)
		saveError = resolved.SaveError
		if resolved.Fatal {
			text := resolved.SaveError
			meta(1, text)
			return nil, sdk.NewToolError(text)
		}
	}
	var ref *savedOutputReference
	if resolved.SavedPath != "" {
		r := formatSavedOutputReference(resolved.SavedPath, fullOutput)
		ref = &r
	}

	ledger := evaluateAcceptance(acceptanceEval{Acceptance: pol.Acceptance, Output: acceptanceOutput, Cwd: pol.Cwd, StagedIndexBaseline: pol.StagedBaseline, FileOutput: fileOutput, ReportOptional: pol.AgentContract, ArtifactsDir: pol.ArtifactsDir, RunID: pol.RunID})
	failure := acceptanceFailureMessage(ledger)
	structured, hasStructured := typedVerifyOutput(ledger)
	failed := failure != "" && ledger.Explicit && !pol.AgentContract

	details := newObject()
	details.set("mode", "single")
	details.set("runId", pol.RunID)
	details.set("agent", pol.Agent.Name)
	details.set("outputMode", func() string {
		if pol.OutputMode == "" {
			return "inline"
		}
		return pol.OutputMode
	}())
	if pol.TimeoutMs > 0 {
		details.set("timeoutMs", float64(pol.TimeoutMs))
	}
	if ref != nil {
		r := newObject()
		r.set("path", ref.Path)
		r.set("bytes", float64(ref.Bytes))
		r.set("lines", float64(ref.Lines))
		r.set("message", ref.Message)
		details.set("outputReference", r)
		details.set("savedOutputPath", resolved.SavedPath)
	}
	if mapping, ok := outputPathMappingFromTask(pol.OriginalTask, resolved.SavedPath); ok {
		m := newObject()
		m.set("requestedPath", mapping.RequestedPath)
		m.set("savedPath", mapping.SavedPath)
		details.set("outputPathMapping", m)
	}
	if saveError != "" {
		details.set("outputSaveError", saveError)
	}
	details.set("acceptance", ledger.object())
	if hasStructured && !failed {
		details.set("structuredOutput", structured)
	}
	if pol.ArtifactsDir != "" {
		a := newObject()
		a.set("dir", pol.ArtifactsDir)
		a.set("outputPath", paths.OutputPath)
		a.set("inputPath", paths.InputPath)
		a.set("metadataPath", paths.MetadataPath)
		details.set("artifactPaths", a)
	}
	if pol.AgentContract {
		// the original's contract projections (agent-contract.ts attachContractProjections)
		ex := newObject()
		ex.set("status", map[bool]string{true: "failed", false: "completed"}[failed])
		ex.set("success", !failed)
		ex.set("exitCode", 0.0)
		details.set("execution", ex)
		rv := newObject()
		if ledger.ReviewResult != nil {
			rv.set("status", ledger.ReviewResult.Status)
		} else {
			rv.set("status", "not-requested")
		}
		details.set("review", rv)
		details.set("effects", newObject())
	}
	if pol.IncludeProgress {
		p := newObject()
		p.set("index", 0.0)
		p.set("agent", pol.Agent.Name)
		p.set("status", map[bool]string{true: "failed", false: "completed"}[failed])
		p.set("durationMs", float64(time.Since(pol.Started).Milliseconds()))
		details.set("progress", []any{p})
	}

	exit := 0
	if failed {
		exit = 1
	}
	display := finalizeSingleOutput(finalizeSingleOutputInput{FullOutput: fullOutput, OutputPath: pol.OutputPath, OutputMode: pol.OutputMode, ExitCode: exit, PreserveSavedOutput: failed && resolved.SavedPath != "", SavedPath: resolved.SavedPath, OutputReference: ref, SaveError: saveError})
	writeOut(fullOutput, failure)
	meta(exit, failure)
	if failed {
		text := formatFailedSingleRunOutput(failure, display.DisplayOutput, paths.OutputPath)
		return sdk.ToolResult{Content: text, IsError: true, Details: details}, nil
	}
	text := display.DisplayOutput
	if text == "" {
		text = "(no output)"
	}
	return explicitOK{Content: text, Details: details}, nil
}

func artifactPathsPtr(dir string, p artifactPaths) *artifactPaths {
	if dir == "" {
		return nil
	}
	return &p
}

func (l *acceptanceLedger) object() *jsObject {
	o := newObject()
	o.set("status", l.Status)
	o.set("evidenceStatus", l.EvidenceStatus)
	o.set("explicit", l.Explicit)
	o.set("inferredReason", anyStrings(l.InferredReason))
	checks := []any{}
	for _, c := range l.RuntimeChecks {
		co := newObject()
		co.set("id", c.ID)
		co.set("status", c.Status)
		co.set("message", c.Message)
		checks = append(checks, co)
	}
	o.set("runtimeChecks", checks)
	runs := []any{}
	for _, r := range l.VerifyRuns {
		runs = append(runs, r.object())
	}
	o.set("verifyRuns", runs)
	if l.ChildReport != nil {
		o.set("childReport", l.ChildReport)
	}
	if l.ChildReportParseError != "" {
		o.set("childReportParseError", l.ChildReportParseError)
	}
	if l.ReviewResult != nil {
		r := newObject()
		r.set("status", l.ReviewResult.Status)
		o.set("reviewResult", r)
	}
	return o
}

// activeRunStart and activeRunEnd count the children of this session (hasActiveSubagentChildren).
func activeRunStart() func() {
	activeRuns.Add(1)
	return func() { activeRuns.Add(-1) }
}

// grantSpawnBudgetAction is `subagent {action: "grant-spawn-budget", additional}`: the root interactive session, after the
// user's native confirmation (or the authority policy), raises the session's spawn cap by `additional`.
func grantSpawnBudgetAction(ctx sdk.Context, params map[string]any) (any, error) {
	fail := func(text string) (any, error) { return nil, sdk.NewToolError(text) }
	if !ctx.HasUI() || nestedBlocked(os.Getenv) != "" || isChildSession() {
		return fail("Action 'grant-spawn-budget' is available only from the root interactive parent session.")
	}
	sessionID := sessionIDOf(ctx)
	if sessionID == "" {
		return fail("Action 'grant-spawn-budget' requires an active parent session id.")
	}
	cfg := loadExtensionConfig()
	getenv := osEnv
	runPolicyMu.Lock()
	runState.CurrentSessionID = sessionID
	if activeRuns.Load() > 0 {
		runPolicyMu.Unlock()
		return fail("Spawn budget grants are rejected while current-session children are queued or running. Wait for them to settle, then retry the explicit grant.")
	}
	additional := any(nil)
	if v, ok := params["additional"]; ok && !isUndefined(v) {
		additional = v
	} else {
		additional = math.NaN()
	}
	preview, errMsg := preflightSpawnBudgetGrant(runState, cfg, sessionID, additional, getenv)
	runPolicyMu.Unlock()
	if errMsg != "" {
		return fail(errMsg)
	}
	authority := resolveAuthorityDecision("spawnBudgetGrant", nil)
	if authority == "forbid" {
		return fail("Authority policy forbids spawn budget grants.")
	}
	n, _ := asNumber(additional)
	confirmed := authority == "auto"
	if !confirmed {
		var err error
		confirmed, err = ctx.Confirm("Grant subagent spawn budget?", fmt.Sprintf("Add %d launches to this logical session?\n\n%s\n\nUsage is not reset. Compaction keeps the same budget; a new parent session starts a fresh one.", int(n), formatSpawnBudget(preview)))
		if err != nil {
			return fail("Spawn budget grant was not confirmed: " + err.Error())
		}
	}
	if !confirmed {
		return explicitOK{Content: "Spawn budget grant canceled; no capacity was added.", Details: map[string]any{"mode": "management", "results": []any{}}}, nil
	}
	runPolicyMu.Lock()
	defer runPolicyMu.Unlock()
	current := getSpawnBudgetSnapshot(runState, cfg, sessionID, getenv)
	if sessionIDOf(ctx) != sessionID || activeRuns.Load() > 0 || current.Used != preview.Used || current.Granted != preview.Granted {
		return fail("Spawn budget grant was not applied because the session, budget, or active-child state changed while confirmation was open.")
	}
	snap, errMsg := grantSpawnBudget(runState, cfg, sessionID, additional, time.Now().UnixMilli(), getenv)
	if errMsg != "" {
		return fail(errMsg)
	}
	return explicitOK{Content: fmt.Sprintf("Spawn budget grant applied: +%d. %s", int(n), formatSpawnBudget(snap)), Details: map[string]any{"mode": "management", "results": []any{}}}, nil
}

// isChildSession is true inside a child pig process of a subagent run (PIG_SUBAGENT or PIG_SUBAGENT_DEPTH set).
func isChildSession() bool {
	d, _ := subagentDepth(os.Getenv)
	return d > 0
}
