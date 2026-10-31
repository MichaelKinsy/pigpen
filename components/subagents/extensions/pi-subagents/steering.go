package pi_subagents

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Steering (src/runs/foreground/async-steering-action.ts): a message sent to a
// running child. The original's children acknowledge delivery over their
// session bus; this port's children receive steering through the run's
// supervisor directory (the child's prompt carries the polling instruction),
// so a steer is scheduled, never falsely reported as delivered. The action's
// checks and receipts are the original's.

const steeringPreviewLimit = 2000

func init() {
	registerAction("steer", []string{"id", "runId", "message", "index", "mode", "steeringRecovery"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		return manageOutcome(steerAction(ctx, params))
	})
}

// steerDir is where steering messages for one run wait.
func steerDir(runDir string) string { return filepath.Join(supervisorDir(runDir), "steer") }

// steeringReceipt is the original's steeringReceipt: the status line, then the
// fenced message preview.
func steeringReceipt(message, receipt string) string {
	preview := steeringMessagePreview(message)
	longest := 2
	for _, run := range fenceRuns(preview) {
		if run > longest {
			longest = run
		}
	}
	fence := strings.Repeat("`", longest+1)
	return receipt + "\n\nMessage sent:\n" + fence + "text\n" + preview + "\n" + fence
}

// fenceRuns measures the length of every backtick run in the text.
func fenceRuns(text string) []int {
	var out []int
	run := 0
	for _, r := range text {
		if r == '`' {
			run++
			continue
		}
		if run > 0 {
			out = append(out, run)
			run = 0
		}
	}
	if run > 0 {
		out = append(out, run)
	}
	return out
}

// steeringMessagePreview is the original's: bounded message text.
func steeringMessagePreview(message string) string {
	return previewText(message, steeringPreviewLimit)
}

// steerAction is `subagent({action:"steer", id, message})`.
func steerAction(ctx sdk.Context, params map[string]any) manageResult {
	id := jsTrim(strParam(params, "id"))
	if id == "" {
		id = jsTrim(strParam(params, "runId"))
	}
	message := jsTrim(strParam(params, "message"))
	sessionID := sessionIDOf(ctx)
	if run := runs.resolveRun(id); run != nil {
		run.mu.Lock()
		sameSession := run.SessionID == sessionID || sessionID == "" || run.SessionID == ""
		state := run.State
		steps := len(run.Steps)
		runID := run.ID
		dir := run.Dir
		run.mu.Unlock()
		if !sameSession {
			return manageResult{fmt.Sprintf("Async run '%s' was not found in the active session.", id), true}
		}
		// A run blocked on a supervisor ask must be answered, not steered (the original).
		if asks := pendingSupervisorIDs(runID, sessionID); len(asks) > 0 {
			replies := []string{}
			for _, ask := range asks {
				replies = append(replies, fmt.Sprintf("subagent_supervisor({ \"action\": \"reply\", \"replyTo\": \"%s\", \"message\": \"<explicit answer>\" })", ask))
			}
			condition := "a pending supervisor ask"
			hint := ""
			if len(asks) > 1 {
				condition = "ambiguous pending supervisor asks"
				hint = " to the intended request ID"
			}
			return manageResult{fmt.Sprintf("Steering not delivered or queued: async run %s is blocked on %s. No reply or recovery was attempted. Reply explicitly%s:\n%s", runID, condition, hint, strings.Join(replies, "\n")), true}
		}
		if state != "running" && state != "queued" {
			return manageResult{fmt.Sprintf("Async run '%s' is not running or queued and cannot be steered.", runID), true}
		}
		if index, ok := intParam(params, "index"); ok {
			if index < 0 || index >= steps {
				return manageResult{fmt.Sprintf("Async run '%s' has %d children. Index %d is out of range.", runID, steps, index), true}
			}
			if steps == 1 && index != 0 {
				return manageResult{fmt.Sprintf("Async run '%s' has %d children. Index %d is out of range.", runID, steps, index), true}
			}
		}
		requestID := newRunID()
		payload, _ := json.Marshal(map[string]any{"type": "steer", "id": requestID, "ts": nowMs(), "message": message, "targetIndex": 0, "source": "steer-action"})
		if err := os.MkdirAll(steerDir(dir), 0o755); err != nil {
			return manageResult{fmt.Sprintf("Failed to queue steering for async run %s: %s", runID, err.Error()), true}
		}
		if err := os.WriteFile(filepath.Join(steerDir(dir), fmt.Sprintf("%013d-%s.json", nowMs(), requestID)), payload, 0o644); err != nil {
			return manageResult{fmt.Sprintf("Failed to queue steering for async run %s: %s", runID, err.Error()), true}
		}
		receipt := fmt.Sprintf("Steering scheduled for async run %s (request %s).", runID, requestID)
		return manageResult{text: steeringReceipt(message, receipt)}
	}
	// Not live in this process: a retained run in this session cannot be
	// steered without its runner, and a foreign or unknown id is reported so.
	for _, st := range diskRunsOfSession(sessionID) {
		if st.RunID == id {
			return manageResult{fmt.Sprintf("Async run '%s' is not running or queued and cannot be steered.", id), true}
		}
	}
	if sessionID != "" {
		return manageResult{fmt.Sprintf("Async run '%s' was not found in the active session.", id), true}
	}
	return manageResult{fmt.Sprintf("Async run '%s' was not found.", id), true}
}

// pendingSupervisorIDs lists a run's unanswered request ids.
func pendingSupervisorIDs(runID, sessionID string) []string {
	var ids []string
	for _, req := range discoverSupervisorRequests("", sessionID) {
		if req.RunID == runID {
			ids = append(ids, req.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

// diskRunsOfSession reads this session's retained run statuses.
func diskRunsOfSession(sessionID string) []*asyncStatusFile {
	entries, err := os.ReadDir(asyncRootDir())
	if err != nil {
		return nil
	}
	var out []*asyncStatusFile
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		st := readStatusFile(filepath.Join(asyncRootDir(), e.Name()))
		if st == nil {
			continue
		}
		if sessionID == "" || st.SessionID == sessionID {
			out = append(out, st)
		}
	}
	return out
}

// steerSlashCommand is `/subagents-steer <run-id> [--child <child-id>] <message>`.
func steerSlashCommand(ctx sdk.Context, args string) error {
	tokens := strings.Fields(args)
	usage := "Usage: /subagents-steer <run-id> [--child <child-id>] <message>"
	if len(tokens) == 0 || strings.HasPrefix(tokens[0], "--") {
		sendSlashText(ctx, usage)
		return nil
	}
	id := tokens[0]
	childID := ""
	messageStart := 1
	if len(tokens) > 2 && tokens[1] == "--child" && !strings.HasPrefix(tokens[2], "--") {
		childID = tokens[2]
		messageStart = 3
	}
	message := strings.Join(tokens[messageStart:], " ")
	if message == "" {
		sendSlashText(ctx, usage)
		return nil
	}
	params := map[string]any{"id": id, "message": message}
	if childID != "" {
		params["index"] = 0
	}
	r := steerAction(ctx, params)
	if r.isError {
		sendSlashText(ctx, r.text)
		return nil
	}
	sendSlashText(ctx, r.text)
	return nil
}
