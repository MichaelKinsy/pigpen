package pi_subagents

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// Port of src/runs/foreground/foreground-control.ts and the live prompt registry of src/runs/foreground/prompt-audit.ts: what a
// live foreground run exposes to the fleet views and the control actions (which child is current, its progress, how to interrupt,
// detach or steer it) and the prompts of its live children, kept apart from the control so the control can be serialized without
// them. Prompt Audit's rewrite of a task by a model (rewritePromptWithGuidance) is not part of it.

// foregroundSteerFunc steers a live child; its outcome state is "delivered", "queued" or "failed" with a reason.
type foregroundSteerFunc func(message, mode string) (state, reason string)

// agentProgress is the part of the original's AgentProgress the control copies (nil numbers are undefined).
type agentProgress struct {
	Index                int
	Agent                string
	SessionName          string
	Status               string
	Task                 string
	ActivityState        string
	LastActivityAt       *int64
	CurrentTool          string
	CurrentToolStartedAt *int64
	CurrentPath          string
	TurnCount            *int
	Tokens               *int
	InputTokens          *int
	OutputTokens         *int
	Window               *int
	WindowPeak           *int
	Model                string
	Thinking             string
	ToolCount            *int
}

// foregroundProgress is the progress fields a child and a run control both carry.
type foregroundProgress struct {
	SessionName          string `json:"sessionName,omitempty"`
	CurrentActivityState string `json:"currentActivityState,omitempty"`
	LastActivityAt       *int64 `json:"lastActivityAt,omitempty"`
	CurrentTool          string `json:"currentTool,omitempty"`
	CurrentToolStartedAt *int64 `json:"currentToolStartedAt,omitempty"`
	CurrentPath          string `json:"currentPath,omitempty"`
	TurnCount            *int   `json:"turnCount,omitempty"`
	Tokens               *int   `json:"tokens,omitempty"`
	InputTokens          *int   `json:"inputTokens,omitempty"`
	OutputTokens         *int   `json:"outputTokens,omitempty"`
	Window               *int   `json:"window,omitempty"`
	WindowPeak           *int   `json:"windowPeak,omitempty"`
	Model                string `json:"model,omitempty"`
	Thinking             string `json:"thinking,omitempty"`
	ToolCount            *int   `json:"toolCount,omitempty"`
}

// foregroundChildControl is the original's ForegroundChildControl.
type foregroundChildControl struct {
	Index       int    `json:"index"`
	Agent       string `json:"agent"`
	Description string `json:"description,omitempty"`
	StartedAt   int64  `json:"startedAt"`
	UpdatedAt   int64  `json:"updatedAt"`
	foregroundProgress
	Interrupt func() bool         `json:"-"`
	Detach    func() bool         `json:"-"`
	Steer     foregroundSteerFunc `json:"-"`
}

// foregroundRunControl is the original's ForegroundRunControl.
type foregroundRunControl struct {
	RunID            string `json:"runId"`
	Mode             string `json:"mode"`
	StartedAt        int64  `json:"startedAt"`
	UpdatedAt        int64  `json:"updatedAt"`
	CurrentAgent     string `json:"currentAgent,omitempty"`
	CurrentIndex     *int   `json:"currentIndex,omitempty"`
	Description      string `json:"description,omitempty"`
	SchedulingOwners *int   `json:"schedulingOwners,omitempty"`
	foregroundProgress
	ActiveChildren map[int]*foregroundChildControl `json:"activeChildren,omitempty"`
	Interrupt      func() bool                     `json:"-"`
	Detach         func() bool                     `json:"-"`
	Steer          foregroundSteerFunc             `json:"-"`
}

// promptAuditRerun is the original's PromptAuditRerunContract.
type promptAuditRerun struct{ Params map[string]any }

// livePromptAudit is the original's LivePromptAudit.
type livePromptAudit struct {
	AuthoredTask         string
	RuntimeAdditions     string
	FinalEffectivePrompt string
	Cwd                  string
	OutputPath           string
	Rerun                *promptAuditRerun
}

// beginForegroundChildInput is the original's BeginForegroundChildInput.
type beginForegroundChildInput struct {
	Index           int
	Agent           string
	Description     string
	AuthoredTask    string
	EffectivePrompt string
	Cwd             string
	OutputPath      string
	Rerun           *promptAuditRerun
	Model           string
	Thinking        string
	Interrupt       func() bool
	Detach          func() bool
	Steer           foregroundSteerFunc
}

// livePrompts holds the prompts of the live children of each run control (the original's WeakMap: an entry goes with its last child).
var (
	livePromptsMu sync.Mutex
	livePrompts   = map[*foregroundRunControl]map[int]*livePromptAudit{}
)

func nowMillis() int64 { return time.Now().UnixMilli() }

func copyProgress(target *foregroundChildControl, p *agentProgress) {
	if p == nil {
		return
	}
	target.SessionName = p.SessionName
	target.CurrentActivityState = p.ActivityState
	target.LastActivityAt = p.LastActivityAt
	target.CurrentTool = p.CurrentTool
	target.CurrentToolStartedAt = p.CurrentToolStartedAt
	target.CurrentPath = p.CurrentPath
	target.TurnCount = p.TurnCount
	target.Tokens = p.Tokens
	target.InputTokens = p.InputTokens
	target.OutputTokens = p.OutputTokens
	target.Window = p.Window
	target.WindowPeak = p.WindowPeak
	target.Model = p.Model
	target.Thinking = p.Thinking
	target.ToolCount = p.ToolCount
}

func syncCurrentChild(c *foregroundRunControl, child *foregroundChildControl) {
	c.CurrentAgent = child.Agent
	idx := child.Index
	c.CurrentIndex = &idx
	c.Description = child.Description
	c.foregroundProgress = child.foregroundProgress
	c.Interrupt = child.Interrupt
	c.Detach = child.Detach
	c.Steer = child.Steer
	c.UpdatedAt = child.UpdatedAt
}

func clearCurrentChild(c *foregroundRunControl) {
	c.CurrentAgent = ""
	c.CurrentIndex = nil
	c.foregroundProgress = foregroundProgress{}
	c.Interrupt, c.Detach, c.Steer = nil, nil, nil
}

func retainForegroundSchedulingOwner(c *foregroundRunControl) {
	n := 1
	if c.SchedulingOwners != nil {
		n = *c.SchedulingOwners + 1
	}
	c.SchedulingOwners = &n
	c.UpdatedAt = nowMillis()
}

func settleForegroundSchedulingOwner(c *foregroundRunControl) {
	n := 0
	if c.SchedulingOwners != nil {
		n = max(0, *c.SchedulingOwners-1)
	}
	c.SchedulingOwners = &n
	c.UpdatedAt = nowMillis()
}

func foregroundSchedulingSettled(c *foregroundRunControl) bool {
	return c.SchedulingOwners == nil || *c.SchedulingOwners == 0
}

func beginForegroundChild(c *foregroundRunControl, in beginForegroundChildInput) {
	now := nowMillis()
	child := &foregroundChildControl{Index: in.Index, Agent: in.Agent, Description: in.Description, StartedAt: now, UpdatedAt: now}
	child.Model, child.Thinking = in.Model, in.Thinking
	child.Interrupt = func() bool {
		if !in.Interrupt() {
			return false
		}
		child.CurrentActivityState = ""
		child.UpdatedAt = nowMillis()
		syncCurrentChild(c, child)
		return true
	}
	if in.Detach != nil {
		child.Detach = func() bool {
			if !in.Detach() {
				return false
			}
			child.CurrentActivityState = ""
			child.UpdatedAt = nowMillis()
			syncCurrentChild(c, child)
			return true
		}
	}
	child.Steer = in.Steer
	if c.ActiveChildren == nil {
		c.ActiveChildren = map[int]*foregroundChildControl{}
	}
	c.ActiveChildren[in.Index] = child
	registerLivePromptAudit(c, in.Index, in.AuthoredTask, in.EffectivePrompt, livePromptMetadata{Cwd: in.Cwd, OutputPath: in.OutputPath, Rerun: in.Rerun})
	syncCurrentChild(c, child)
}

func updateForegroundChild(c *foregroundRunControl, index int, p *agentProgress) {
	child := c.ActiveChildren[index]
	if child == nil {
		return
	}
	copyProgress(child, p)
	child.UpdatedAt = nowMillis()
	syncCurrentChild(c, child)
}

func finishForegroundChild(c *foregroundRunControl, index int) {
	removeLivePromptAudit(c, index)
	delete(c.ActiveChildren, index)
	if c.CurrentIndex != nil && *c.CurrentIndex == index {
		children := make([]*foregroundChildControl, 0, len(c.ActiveChildren))
		for _, ch := range c.ActiveChildren {
			children = append(children, ch)
		}
		// the most recently updated child becomes current, the lower index on a tie
		sort.Slice(children, func(i, j int) bool {
			if children[i].UpdatedAt != children[j].UpdatedAt {
				return children[i].UpdatedAt > children[j].UpdatedAt
			}
			return children[i].Index < children[j].Index
		})
		if len(children) > 0 {
			syncCurrentChild(c, children[0])
		} else {
			clearCurrentChild(c)
		}
	}
	c.UpdatedAt = nowMillis()
}

// ---- prompt-audit.ts: the live prompt registry --------------------------------------------------------------------------------

type livePromptMetadata struct {
	Cwd, OutputPath string
	Rerun           *promptAuditRerun
}

// promptRuntimeAdditions is what the effective prompt carries besides the authored task (before it and after it).
func promptRuntimeAdditions(authoredTask, effectivePrompt string) string {
	if authoredTask == "" {
		return effectivePrompt
	}
	at := strings.Index(effectivePrompt, authoredTask)
	if at < 0 {
		return "(runtime additions unavailable)"
	}
	before := jsTrim(effectivePrompt[:at])
	after := jsTrim(effectivePrompt[at+len(authoredTask):])
	var parts []string
	for _, s := range []string{before, after} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 {
		return "(none)"
	}
	out := parts[0]
	if len(parts) == 2 {
		out += "\n\n" + parts[1]
	}
	return out
}

func registerLivePromptAudit(c *foregroundRunControl, index int, authoredTask, effectivePrompt string, m livePromptMetadata) {
	livePromptsMu.Lock()
	defer livePromptsMu.Unlock()
	prompts := livePrompts[c]
	if prompts == nil {
		prompts = map[int]*livePromptAudit{}
		livePrompts[c] = prompts
	}
	prompts[index] = &livePromptAudit{AuthoredTask: authoredTask, RuntimeAdditions: promptRuntimeAdditions(authoredTask, effectivePrompt), FinalEffectivePrompt: effectivePrompt, Cwd: m.Cwd, OutputPath: m.OutputPath, Rerun: m.Rerun}
}

func updateLiveEffectivePrompt(c *foregroundRunControl, index int, effectivePrompt string) {
	livePromptsMu.Lock()
	defer livePromptsMu.Unlock()
	if p := livePrompts[c][index]; p != nil {
		p.FinalEffectivePrompt = effectivePrompt
		p.RuntimeAdditions = promptRuntimeAdditions(p.AuthoredTask, effectivePrompt)
	}
}

func getLivePromptAudit(c *foregroundRunControl, index int) *livePromptAudit {
	livePromptsMu.Lock()
	defer livePromptsMu.Unlock()
	return livePrompts[c][index]
}

func removeLivePromptAudit(c *foregroundRunControl, index int) {
	livePromptsMu.Lock()
	defer livePromptsMu.Unlock()
	prompts := livePrompts[c]
	if prompts == nil {
		return
	}
	delete(prompts, index)
	if len(prompts) == 0 {
		delete(livePrompts, c)
	}
}
