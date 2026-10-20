package pi_subagents

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// runs/shared/child-launch-plan.ts and child-lifecycle.ts.

// ── child-lifecycle.ts ──

// childLifecycleAction is what the observer of a child's events does about its final drain window.
type childLifecycleAction string

const (
	childStartDrain  childLifecycleAction = "start-drain"
	childCancelDrain childLifecycleAction = "cancel-drain"
	childNoAction    childLifecycleAction = "none"
)

type childLifecycleState struct{ CompactionRetryActive bool }

// projectChildLifecycle says, from one observed event, whether the observer starts (or must cancel) its final drain: the run is
// settling after agent_settled or a terminal assistant stop, and resumed work (a retry, a new turn) cancels it.
func projectChildLifecycle(event childEvent, terminalAssistantStop bool, state *childLifecycleState) childLifecycleAction {
	typ, _ := event["type"].(string)
	willRetry, _ := event["willRetry"].(bool)
	switch typ {
	case "compaction_end":
		if state != nil {
			state.CompactionRetryActive = willRetry
		}
		if willRetry {
			return childCancelDrain
		}
		return childNoAction
	case "agent_start", "auto_retry_start", "turn_start":
		if state != nil {
			state.CompactionRetryActive = false
		}
		return childCancelDrain
	case "agent_end":
		if !willRetry && state != nil {
			state.CompactionRetryActive = false
		}
		return childCancelDrain
	case "agent_settled":
		if state != nil && state.CompactionRetryActive {
			return childNoAction
		}
		return childStartDrain
	}
	if terminalAssistantStop {
		return childStartDrain
	}
	return childNoAction
}

// ── child-launch-plan.ts ──

// readsInput is a list of files to read, false for none, or unset.
type readsInput struct {
	Set, False bool
	Items      []string
}

// resolvedStepBehavior is the original's ResolvedStepBehavior.
type resolvedStepBehavior struct {
	Output       any // string path, or false
	OutputMode   string
	Reads        []string
	ReadsFalse   bool
	Progress     bool
	Skills       []string
	SkillsFalse  bool
	Model        string
	Fast         *bool
	OutputSchema any // nil: none
}

type childStepOverrides struct {
	Output       any // nil, a string or a bool
	OutputMode   string
	Reads        readsInput
	Progress     *bool
	Skills       SkillInput
	Model        string
	Fast         *bool
	OutputSchema any // nil: unset; false: disabled; a schema
}

type parallelOutputNamespace struct {
	StepIndex int
	TaskIndex *int
}

type childLaunchPlanInput struct {
	Agent                   AgentConfig
	StepOverrides           childStepOverrides
	Task, OriginalTask      string
	RunnerCwd, RuntimeCwd   string
	StepCwdInput            string
	BehaviorCwd, MachineCwd string
	ChainSkills             []string
	OutputBaseDir           string
	ParallelOutputNamespace *parallelOutputNamespace
	ResolvedBehavior        *resolvedStepBehavior
}

type childLaunchPlan struct {
	StepCwd, InstructionCwd, ReadExistenceCwd string
	Behavior                                  resolvedStepBehavior
	InheritedRelativeParallelOutput           bool
	NamespaceOutputPath                       bool
	OutputPath                                string
	SkillNames                                []string
}

func dedupeConcat(a, b []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range append(append([]string{}, a...), b...) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func resolveFullStepBehavior(a AgentConfig, o childStepOverrides, chainSkills []string) resolvedStepBehavior {
	b := resolvedStepBehavior{}
	if out := normalizeOutputOverride(o.Output); out != nil {
		b.Output = out
	} else if out := normalizeOutputOverride(a.Output); out != nil {
		b.Output = out
	} else {
		b.Output = false
	}
	switch {
	case o.Reads.Set && o.Reads.False:
		b.ReadsFalse = true
	case o.Reads.Set:
		b.Reads = append([]string{}, o.Reads.Items...)
	case a.DefaultReads != nil:
		b.Reads = append([]string{}, a.DefaultReads...)
	default:
		b.ReadsFalse = true
	}
	b.Progress = a.DefaultProgress
	if o.Progress != nil {
		b.Progress = *o.Progress
	}
	switch {
	case o.Skills.Set && o.Skills.False:
		b.SkillsFalse = true
	case o.Skills.Set:
		b.Skills = append([]string{}, o.Skills.Items...)
		if len(chainSkills) > 0 {
			b.Skills = dedupeConcat(b.Skills, chainSkills)
		}
	default:
		b.Skills = append([]string{}, a.Skills...)
		if len(chainSkills) > 0 {
			b.Skills = dedupeConcat(b.Skills, chainSkills)
		}
	}
	b.OutputMode = o.OutputMode
	if b.OutputMode == "" {
		b.OutputMode = a.OutputMode
	}
	if b.OutputMode == "" {
		b.OutputMode = "inline"
	}
	b.Model = o.Model
	if b.Model == "" {
		b.Model = a.Model
	}
	b.Fast = o.Fast
	if b.Fast == nil {
		b.Fast = a.Fast
	}
	b.OutputSchema = resolveEffectiveOutputSchema(a, o.OutputSchema)
	return b
}

func resolveTaskTextForFileUpdatePolicy(task, originalTask string) string {
	if task == "" {
		return originalTask
	}
	if originalTask != "" {
		return strings.ReplaceAll(task, "{task}", originalTask)
	}
	return task
}

var fileUpdatePolicyPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\breview[- ]only\b`),
	regexp.MustCompile(`(?i)\bread[- ]only\s+(?:review|audit|inspection|pass)\b`),
	regexp.MustCompile(`(?i)\b(?:no|without)\s+(?:file\s+)?edits?\b`),
	regexp.MustCompile(`(?i)\b(?:do not|don't|must not)\s+(?:edit|modify|write|touch)\b`),
	regexp.MustCompile(`(?i)\bleave\s+files?\s+unchanged\b`),
}

func taskDisallowsFileUpdates(task string) bool {
	for _, p := range fileUpdatePolicyPatterns {
		if p.MatchString(task) {
			return true
		}
	}
	return false
}

func suppressProgressForReadOnlyTask(b resolvedStepBehavior, task, originalTask string) resolvedStepBehavior {
	if b.Progress && taskDisallowsFileUpdates(resolveTaskTextForFileUpdatePolicy(task, originalTask)) {
		b.Progress = false
	}
	return b
}

func planChildLaunch(in childLaunchPlanInput) childLaunchPlan {
	stepCwd := in.MachineCwd
	if stepCwd == "" {
		stepCwd = resolveChildCwd(in.RunnerCwd, in.StepCwdInput)
	}
	instructionCwd := in.BehaviorCwd
	if instructionCwd == "" {
		instructionCwd = stepCwd
	}
	readExistenceCwd := instructionCwd
	if in.BehaviorCwd != "" {
		readExistenceCwd = stepCwd
	}
	base := resolveFullStepBehavior(in.Agent, in.StepOverrides, in.ChainSkills)
	if in.ResolvedBehavior != nil {
		base = *in.ResolvedBehavior
	}
	behavior := suppressProgressForReadOnlyTask(base, in.Task, in.OriginalTask)
	outStr, outIsStr := behavior.Output.(string)
	inherited := in.ParallelOutputNamespace != nil && in.StepOverrides.Output == nil && outIsStr && !filepath.IsAbs(outStr)
	if inherited && in.ParallelOutputNamespace.TaskIndex != nil {
		behavior.Output = filepath.Join("parallel-"+strconv.Itoa(in.ParallelOutputNamespace.StepIndex), strconv.Itoa(*in.ParallelOutputNamespace.TaskIndex)+"-"+in.Agent.Name, outStr)
	}
	plan := childLaunchPlan{StepCwd: stepCwd, InstructionCwd: instructionCwd, ReadExistenceCwd: readExistenceCwd, Behavior: behavior, InheritedRelativeParallelOutput: inherited,
		NamespaceOutputPath: inherited && in.ParallelOutputNamespace.TaskIndex == nil}
	if behavior.SkillsFalse {
		plan.SkillNames = []string{}
	} else {
		plan.SkillNames = behavior.Skills
	}
	plan.OutputPath = resolveSingleOutputPath(behavior.Output, in.RuntimeCwd, instructionCwd, in.OutputBaseDir)
	return plan
}
