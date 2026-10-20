package pi_subagents

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Port of src/extension/tool-description.ts: the description the `subagent` tool is registered with, by the extension config's
// toolDescriptionMode ("full", "compact", "custom"; absent is the default and carries the split prompt metadata). The texts are the
// original's, byte for byte.

const (
	customToolDescriptionFile     = "subagent-tool-description.md"
	customToolDescriptionMaxBytes = 50 * 1024

	agentSelectionGuidance          = `First call {action:"list",capabilities:true}: executable, non-disabled agents only; external-cli requires runner.available === true. Passive PATH/PATHEXT/X_OK is not authentication/version/launch proof; preflight is authoritative.`
	subagentFailureRecoveryGuidance = `Workflow, child launch, prompt runtime, extension load or child tooling failure is a lane infrastructure blocker. Stop; report exact failure, run/status and repo/cwd/worktree/branch/ref; verify clean worktree or capture partial diff before same-protocol retry or asking the owner. Never silently switch to interactive_shell, pi -ne, Codex/Claude/Cursor CLI or foreground/external mode: governed-workflow fallback requires explicit owner approval, not Pi core's generic pi -ne hint. Explicit foreground/CLI requests and work outside that protocol remain valid.`
	safetyGuidanceHead              = `SAFETY-CRITICAL SUBAGENT GUIDANCE:
• Direct parent execution is the default. Invoke subagents only when delegation is authorized by the operator's current request or applicable user/project instructions; task size, complexity, risk, tool-call count, or recipe fit do not independently authorize delegation.
• `
	safetyGuidanceMid = `
• `
	safetyGuidanceTail = `
• Omit action for execution. For an authorized delegated multi-step/parallel workflow: exactly one top-level subagent workflow call with async:true; children launch only inside it.
• Async follows asyncByDefault (normally true); async:false only to block the parent, not for final reviews/gates. Consume results at dependency barriers. Native async completion wakes this session: return control, no sleep/poll or bg_wait merely for a wake. bg_wait is for provider/detached work without native notification needing a same-turn result.
• Ordinary child subagents are not orchestrators; only configured fanout within depth/session limits. For an authorized delegated workflow, keep one writer per cwd/worktree and isolate concurrent writers. Use fresh-context read-only reviewers when independent review was requested, then parent synthesis/fixes. Oracle/advisor unknowns use supervisor dialogue; one-shot only when requested.
• Bind durable output on runs.run/runs.all, not task filename prose; return actual outputReference/outputPathMapping/artifactPaths, evidence and residual risks.
• children.list is workflow-only, not an exhaustive list of direct native children: resume only resumable rows. When an intended child's exact run id is known, inspect it with {action:"status",id}; if status identifies the candidate, attempt {action:"resume",id,message}. Resume authoritatively checks eligibility, may reject it, and otherwise detaches a follow-up/challenge with the stored agent/model/tool contract. Use a labeled same-role fallback only when no known candidate exists or resume rejects eligibility. Scripts await runs.run(newKey,{resume:runId,task}); continue from latest returned runId. Each distinct resume pass needs a new stable key; same-key reuse requires identical launch parameters.
• Named resources own authority; raw workflowScript/workflowScriptPath cannot use runs.host. Granted commands/relative outputs use workflow cwd, never per-step cwd.
• Inspect asyncId/asyncDir (status.json, events.jsonl, logs) with status/debug.run; control with interrupt/stop/resume/steer. Read {action:"guide",topic:"tool-reference"} for controls/evidence gates.`
	executionGuidance = `Delegate one child with {agent,task?}; otherwise choose exactly one of {workflowScript,args?}, {workflowScriptPath,args?} or {workflow,args}. agent/task exclude workflow inputs; task excludes action. agent may target management actions. action is management/control; validate accepts either script without launching. workflowScriptPath loads from request cwd before sandbox execution.
Scripts: JavaScript statement bodies with explicit return, top-level await, plain helpers/Promise chains; nested async function/arrow/method helpers are rejected. Await runs.run('key',{agent,task}) before .output; await runs.all([{key,agent,task},...]) for an ordered array, not a key map. Observe every stored run promise with direct await, Promise.race or Promise.all. Await/return runs.steer(key,message,options?) for a prior key, never raw run ids; queued/delivered/missed/failed receipts are not compliance proof.
Before advanced orchestration (runs.lanes, rolling fanout, mission state, handoffs), read {action:"guide",topic:"workflows"} or the pi-subagents skill. Raw-script sandboxes add deeply frozen args; all sandboxes provide runs, emit, console, JavaScript and enabled mission state, with no filesystem/shell/Pi tools/host globals. External CLI agents support native options only when their runner declares them; read guide tool-reference before passing model, structured output, acceptance/agentContract, tool budget, fast, fork context or skills/tools.
Model override: first call {action:"models"}; copy exact provider/id, not agent names. Thinking uses model suffix, not watchdog-only thinking.
Named resources: {workflow:'review',args:{task:'...'}} or {workflow:'run-ci',args:{command:'npm test'}}. Raw scripts also accept bounded plain-data args; raw-script args persist as evidence, so never include secrets. worktree:true requires clean source; baseRef defaults to HEAD at allocation or a supported named ref, never full 40/64-character commit IDs or revision expressions.`
	fullDescriptionDetails = `WORKFLOW DETAILS:
• runs.lanes([{key,stages:[{key,agent,task},{key,resume:'previous',task}]}]) runs first stages together, later stages sequentially per lane. Failures stay lane-local; only explicit structuredOutput.verdict === 'blocked' blocks a successful stage, never reviewer prose.
• Workflow child controls default onto runs.run/runs.all items; child fields override them. worktree:true isolates each child and returns handoff artifacts. usageBudget is shared across the workflow; already-running children are not stopped.
• Missions auto-attach unless mission:false; await state.get(key)/state.set(key,JSONValue) requires a mission. See guide topic missions. Omit acceptance for reviewer/read-only calls; acceptance.review.required requests independent writer review.
• Management discovery: list/get/models/guide; create/update/delete/eject/disable/enable/reset/refine; mission.*, schedule.*, watchdog.*, inspector.*, project.*, lane.status/recordMerge/recordSupersession; worktree.discard and plan-only worktree.cleanup; doctor and grant-spawn-budget. Use guide topics agents, missions, observability, tool-reference, configuration, models, watchdog or extension-api for exact action fields. Schedules take script inputs, not direct children; recipes live in the missions guide.`

	// SubagentToolPromptSnippet and the guideline below are registered with the tool only when no toolDescriptionMode is set.
	SubagentToolPromptSnippet = "For operator-requested delegation, use subagents; compose multi-child work in one workflow call."
)

// SubagentToolPromptGuidelines is SUBAGENT_TOOL_PROMPT_GUIDELINES.
var SubagentToolPromptGuidelines = []string{"Do not invoke subagents unless the operator requested delegation directly or through applicable instructions."}

// SubagentSafetyGuidance is SUBAGENT_SAFETY_GUIDANCE: it ends every description.
var SubagentSafetyGuidance = safetyGuidanceHead + agentSelectionGuidance + safetyGuidanceMid + subagentFailureRecoveryGuidance + safetyGuidanceTail

// DefaultSubagentToolDescription is DEFAULT_SUBAGENT_TOOL_DESCRIPTION; CompactSubagentToolDescription is the same.
var (
	DefaultSubagentToolDescription = executionGuidance + "\n\n" + SubagentSafetyGuidance
	CompactSubagentToolDescription = DefaultSubagentToolDescription
	FullSubagentToolDescription    = DefaultSubagentToolDescription + "\n\n" + fullDescriptionDetails
)

// ToolDescriptionOptions are the options of BuildSubagentToolDescription; zero values take the process's: the working
// directory, the user's agent directory and a warning on stderr.
type ToolDescriptionOptions struct {
	Cwd      string
	AgentDir string
	Warn     func(message string)
}

func (o ToolDescriptionOptions) warn(message string) {
	if o.Warn != nil {
		o.Warn("[pi-subagents] " + message)
		return
	}
	fmt.Fprintln(os.Stderr, "[pi-subagents] "+message)
}

func (o ToolDescriptionOptions) cwd() string {
	if o.Cwd != "" {
		return o.Cwd
	}
	d, _ := os.Getwd()
	return d
}

func (o ToolDescriptionOptions) agentDir() string {
	if o.AgentDir != "" {
		return o.AgentDir
	}
	return agentDir()
}

// BuildSubagentToolPromptMetadata is buildSubagentToolPromptMetadata: the split prompt metadata, only while no
// toolDescriptionMode is configured (mode is the raw config value, nil when absent).
func BuildSubagentToolPromptMetadata(mode any) (snippet string, guidelines []string) {
	if mode != nil {
		return "", nil
	}
	return SubagentToolPromptSnippet, SubagentToolPromptGuidelines
}

func resolveToolDescriptionMode(mode any, o ToolDescriptionOptions) string {
	if mode == nil {
		return "full"
	}
	if s, ok := mode.(string); ok && (s == "full" || s == "compact" || s == "custom") {
		return s
	}
	o.warn(fmt.Sprintf(`Ignoring invalid toolDescriptionMode %s; expected "full", "compact", or "custom".`, marshalJSON(mode, "")))
	return "full"
}

var customTemplatePlaceholder = regexp.MustCompile(`\{\{(\w+)\}\}`)

func renderCustomTemplate(template string, o ToolDescriptionOptions) string {
	variables := map[string]string{
		"fullDescription": FullSubagentToolDescription, "full": FullSubagentToolDescription,
		"compactDescription": CompactSubagentToolDescription, "compact": CompactSubagentToolDescription,
		"safetyGuidance": SubagentSafetyGuidance, "safety": SubagentSafetyGuidance,
		"agentDir": o.agentDir(), "projectConfigDir": projectConfigDir(o.cwd()),
	}
	return customTemplatePlaceholder.ReplaceAllStringFunc(template, func(raw string) string {
		name := customTemplatePlaceholder.FindStringSubmatch(raw)[1]
		if v, ok := variables[name]; ok {
			return v
		}
		o.warn(customToolDescriptionFile + ": unknown placeholder " + raw + " left unchanged.")
		return raw
	})
}

func loadCustomToolDescription(o ToolDescriptionOptions) string {
	for _, p := range []string{filepath.Join(projectConfigDir(o.cwd()), customToolDescriptionFile), filepath.Join(o.agentDir(), customToolDescriptionFile)} {
		st, err := os.Stat(p)
		if err != nil {
			if isNotExist(err) {
				continue
			}
			o.warn(fmt.Sprintf("Failed to inspect custom tool description '%s': %s", p, err.Error()))
			continue
		}
		if !st.Mode().IsRegular() {
			o.warn(fmt.Sprintf("Ignoring custom tool description '%s' because it is not a file.", p))
			continue
		}
		if st.Size() > customToolDescriptionMaxBytes {
			o.warn(fmt.Sprintf("Ignoring custom tool description '%s' because it is larger than %d bytes.", p, customToolDescriptionMaxBytes))
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			o.warn(fmt.Sprintf("Failed to read custom tool description '%s': %s", p, err.Error()))
			continue
		}
		template := jsTrim(string(data))
		if template == "" {
			o.warn(fmt.Sprintf("Ignoring empty custom tool description '%s'.", p))
			continue
		}
		rendered := jsTrim(renderCustomTemplate(template, o))
		if rendered == "" {
			o.warn(fmt.Sprintf("Ignoring custom tool description '%s' because it rendered empty.", p))
			continue
		}
		return rendered
	}
	return ""
}

// withMandatorySafetyGuidance keeps the safety guidance in a custom description once, last, whatever the custom text embeds.
func withMandatorySafetyGuidance(description string) string {
	var parts []string
	for _, part := range strings.Split(description, SubagentSafetyGuidance) {
		for _, p := range strings.Split(part, subagentFailureRecoveryGuidance) {
			if t := jsTrim(p); t != "" {
				parts = append(parts, t)
			}
		}
	}
	custom := strings.Join(parts, "\n\n")
	if custom == "" {
		return SubagentSafetyGuidance
	}
	return custom + "\n\n" + SubagentSafetyGuidance
}

// BuildSubagentToolDescription is buildSubagentToolDescription; mode is the raw toolDescriptionMode (nil when absent).
func BuildSubagentToolDescription(mode any, o ToolDescriptionOptions) string {
	if mode == nil {
		return DefaultSubagentToolDescription
	}
	switch resolveToolDescriptionMode(mode, o) {
	case "compact":
		return CompactSubagentToolDescription
	case "custom":
		if custom := loadCustomToolDescription(o); custom != "" {
			return withMandatorySafetyGuidance(custom)
		}
		o.warn(customToolDescriptionFile + ` was not found or valid for toolDescriptionMode "custom"; using full description.`)
	}
	return FullSubagentToolDescription
}

// configuredToolDescriptionMode reads toolDescriptionMode of the extension config (<agent dir>/extensions/subagent/config.json);
// nil when the file or the field is absent or the file is not a JSON object.
func configuredToolDescriptionMode() any {
	data, err := os.ReadFile(filepath.Join(agentDir(), "extensions", "subagent", "config.json"))
	if err != nil {
		return nil
	}
	v, err := parseJSON(data)
	if err != nil {
		return nil
	}
	o, ok := v.(*jsObject)
	if !ok {
		return nil
	}
	if m, ok := lpGet(o, "toolDescriptionMode"); ok {
		return m
	}
	return nil
}
