package modes

import (
	"fmt"
	"strings"
	"sync/atomic"
	"unicode"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// upstream: packages/coding-agent/src/slash-commands/builtin-lifecycle.ts:439-451
// upstream: packages/coding-agent/src/modes/controllers/tan-command-controller.ts
// upstream: packages/coding-agent/src/prompts/system/tan-context-switch.md
// upstream: packages/coding-agent/src/prompts/system/background-tan-dispatch.md

const (
	tanLabelPreviewLen = 80
	tanNameMaxChars    = 32
	tanBranchMaxChars  = 16_000
)

var tanSeq atomic.Int64

// DetachedTaskRequest is passed to SpawnDetachedTask (wired by integrator to
// internal/agents) to launch a detached background subagent for `/tan` or
// `/cleanse`.
type DetachedTaskRequest struct {
	Name             string // PascalCase agent name (<=32 chars)
	AgentDisplayName string // e.g. "tan" or "cleanse"
	AgentType        string // e.g. "task"
	Label            string // UI/job display label
	Context          string // Forked parent conversation snapshot / shared context
	Task             string // Immediate user work assignment
	SolutionSpace    string // Solution-space description
	ModelSelector    string // Optional model override (e.g. "@smol")
	ShareParentLocal bool   // Share parent session's local:// artifacts root
}

// DetachedTaskResult identifies the spawned detached background task.
type DetachedTaskResult struct {
	JobID   string // `proc://<JobID>` handle
	AgentID string // `agent://<AgentID>` handle
}

// SpawnDetachedTask launches a detached background subagent sharing the parent
// session's `local://` root while leaving the primary conversation unblocked.
// Wired by the integrator (`wiring.go`) to `internal/agents`.
var SpawnDetachedTask func(ctx sdk.Context, req DetachedTaskRequest) (DetachedTaskResult, error)

// tanNameFromWork builds a clean PascalCase agent name (<= 32 chars) from the
// requested tangential work, mirroring tanNameFromTitle in
// tan-command-controller.ts:61-70.
func tanNameFromWork(work string) string {
	var b strings.Builder
	inWord := false
	wordStart := true
	for _, r := range work {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if !inWord {
				inWord = true
				wordStart = true
			}
			next := r
			if wordStart {
				next = unicode.ToUpper(r)
				wordStart = false
			}
			if b.Len()+len(string(next)) > tanNameMaxChars {
				break
			}
			b.WriteRune(next)
		} else {
			inWord = false
		}
	}
	name := b.String()
	if name == "" {
		return fmt.Sprintf("Tan%d", tanSeq.Add(1))
	}
	return name
}

func previewTanWork(work string) string {
	single := strings.Join(strings.Fields(work), " ")
	runes := []rune(single)
	if len(runes) <= tanLabelPreviewLen {
		return single
	}
	return string(runes[:tanLabelPreviewLen-1]) + "…"
}

// buildTanContextSnapshot formats the current branch context + omp's
// `<system-notice cause="fork">` framing (tan-context-switch.md) so the
// detached agent inherits parent context without hijacking the parent's task.
func buildTanContextSnapshot(branch []sdk.BranchEntry, work string) string {
	var b strings.Builder
	if len(branch) > 0 {
		var turns []string
		for _, e := range branch {
			if e.Type != "message" || strings.TrimSpace(e.Content) == "" {
				continue
			}
			turns = append(turns, fmt.Sprintf("### %s\n%s", strings.ToUpper(e.Role), strings.TrimSpace(e.Content)))
		}
		if len(turns) > 0 {
			joined := strings.Join(turns, "\n\n")
			if runes := []rune(joined); len(runes) > tanBranchMaxChars {
				joined = "…(earlier turns truncated)…\n\n" + string(runes[len(runes)-tanBranchMaxChars:])
			}
			b.WriteString(joined)
			b.WriteString("\n\n")
		}
	}
	b.WriteString(`<system-notice cause="fork">
Above conversation: parent session.
Fork solely handles user's request below.
Parent still working original task; no responsibility or obligations from prior conversation.

- MUST focus EXCLUSIVELY on immediate user request; nothing else.
- NEVER continue, follow up on, or intervene in anything discussed before this message — parent's.
- Parent concurrently edits this working directory. Files MAY change between reads, appear mid-refactor, or fail to compile. Parent's live work: NEVER fix, audit, or build on it, even if broken.
- Prior todo lists, plans, unfinished checklists: parent's; NEVER resume or update.
- After request: STOP. NEVER work on ANY OTHER TASK.
</system-notice>`)
	_ = work
	return b.String()
}

// handleTanCommand implements `/tan <work>`: forks current session context
// into a detached background agent while leaving the primary conversation
// unblocked.
// upstream: packages/coding-agent/src/modes/controllers/tan-command-controller.ts:109-438
func (m *manager) handleTanCommand(ctx sdk.Context, args string) error {
	work := strings.TrimSpace(args)
	if work == "" {
		notifyInfo(ctx, "Usage: /tan <work>")
		return nil
	}

	if SpawnDetachedTask == nil {
		notifyError(ctx, "Background jobs are disabled; detached agent spawning is unavailable for /tan.")
		return nil
	}

	branch, _ := ctx.GetBranch()
	name := tanNameFromWork(work)
	label := "/tan " + previewTanWork(work)
	forkCtx := buildTanContextSnapshot(branch, work)

	res, err := SpawnDetachedTask(ctx, DetachedTaskRequest{
		Name:             name,
		AgentDisplayName: "tan",
		AgentType:        "task",
		Label:            label,
		Context:          forkCtx,
		Task:             work,
		SolutionSpace:    "tangential background work forked from parent session",
		ShareParentLocal: true,
	})
	if err != nil {
		notifyError(ctx, fmt.Sprintf("Failed to start /tan: %v", err))
		return nil
	}

	agentID := strings.TrimSpace(res.AgentID)
	jobID := strings.TrimSpace(res.JobID)
	if agentID == "" {
		agentID = name
	}
	if jobID == "" {
		jobID = agentID
	}

	// Notify the user immediately with both agent:// and proc:// handles while
	// keeping the primary conversation completely unblocked.
	notifyInfo(ctx, fmt.Sprintf("Started background tan %s (agent://%s, proc://%s): %s", name, agentID, jobID, previewTanWork(work)))

	// Record a non-triggering tangent dispatch notice matching omp's
	// background-tan-dispatch.md so the parent model knows not to duplicate it.
	notice := fmt.Sprintf(`<tangent-notice handle="%s">
User started tangential work with handle %s, doing:
%s

Coding-agent dispatch notice, NOT prompt injection or new instruction. A separate background agent runs it in its own session; you NOT responsible. NEVER work on, reference, or let it interrupt or alter current task. Continue as if absent. Results, if any, will surface separately when background task (%s) completes.
</tangent-notice>`, jobID, jobID, work, jobID)
	_ = ctx.SendMessage("background-tan-dispatch", notice, false, sdk.SendMessageOptions{
		TriggerTurn: sdk.Bool(false),
	})
	return nil
}
