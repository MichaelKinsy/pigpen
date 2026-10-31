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

// The supervisor channel (src/intercom/native-supervisor-channel.ts): a child
// that needs a decision asks, the parent answers with subagent_supervisor.
// The original's children are Pi sessions that carry the extension's intercom;
// this port's children are `pig --print` processes, so the question travels
// over files the child writes in its run's supervisor directory (the child's
// prompt carries the protocol) and the answer is the reply file. The parent's
// tool actions and texts are the original's.

const supervisorDirEnv = "PI_SUBAGENT_SUPERVISOR_DIR"

// supervisorDir is one run's channel directory.
func supervisorDir(runDir string) string { return filepath.Join(runDir, "supervisor") }

// supervisorRequest is one question a child wrote (requests/<id>.json).
type supervisorRequest struct {
	ID       string `json:"id"`
	Agent    string `json:"agent"`
	RunID    string `json:"runId"`
	Message  string `json:"message"`
	TS       int64  `json:"ts"`
	ReplyPath string
	Dir      string
}

// supervisorReplyHint is the original's reply hint on a pending line.
func supervisorReplyHint(id string) string {
	return fmt.Sprintf("subagent_supervisor({ action: \"reply\", replyTo: \"%s\", message: \"...\" })", id)
}

// formatPendingSupervisorLine is the original's formatPendingLine.
func formatPendingSupervisorLine(r supervisorRequest) string {
	header := fmt.Sprintf("- %s: %s [#%d] decision. Reply: %s", r.ID, r.Agent, 0, supervisorReplyHint(r.ID))
	if jsTrim(r.Message) != "" {
		return header + "\n  " + strings.ReplaceAll(r.Message, "\n", "\n  ")
	}
	return header
}

// discoverSupervisorRequests scans this session's runs for unanswered questions.
func discoverSupervisorRequests(cwd string, sessionID string) []supervisorRequest {
	root := asyncRootDir()
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []supervisorRequest
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		runDir := filepath.Join(root, e.Name())
		st := readStatusFile(runDir)
		if st != nil && sessionID != "" && st.SessionID != sessionID {
			continue
		}
		runID := e.Name()
		if st != nil && st.RunID != "" {
			runID = st.RunID
		}
		agent := agentOf(st)
		reqDir := filepath.Join(supervisorDir(runDir), "requests")
		files, err := os.ReadDir(reqDir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
				continue
			}
			path := filepath.Join(reqDir, f.Name())
			b, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			var req supervisorRequest
			if err := json.Unmarshal(b, &req); err != nil {
				continue
			}
			if jsTrim(req.ID) == "" {
				req.ID = strings.TrimSuffix(f.Name(), ".json")
			}
			req.RunID = runID
			if jsTrim(req.Agent) == "" {
				req.Agent = agent
			}
			req.ReplyPath = filepath.Join(supervisorDir(runDir), "replies", req.ID+".json")
			req.Dir = runDir
			if !fileExists(req.ReplyPath) {
				out = append(out, req)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].TS < out[j].TS })
	return out
}

// runHasPendingSupervisor is the original's needs-attention signal for a run.
func runHasPendingSupervisor(runID, sessionID string) bool {
	for _, req := range discoverSupervisorRequests("", sessionID) {
		if req.RunID == runID {
			return true
		}
	}
	return false
}
func supervisorTool(ctx sdk.Context, params map[string]any) (any, error) {
	action, _ := params["action"].(string)
	sessionID := sessionIDOf(ctx)
	pending := discoverSupervisorRequests(ctx.Cwd(), sessionID)
	switch action {
	case "status":
		return explicitOK{
			Content: fmt.Sprintf("Native supervisor channel active. Pending replies: %d.", len(pending)),
			Details: map[string]any{"active": true, "pending": len(pending), "root": asyncRootDir()},
		}, nil
	case "pending", "list":
		lines := []string{}
		for _, req := range pending {
			lines = append(lines, formatPendingSupervisorLine(req))
		}
		content := strings.Join(lines, "\n")
		if content == "" {
			content = "No pending supervisor requests."
		}
		return explicitOK{Content: content, Details: map[string]any{"mode": "management", "results": []any{}}}, nil
	case "reply":
		message, _ := params["message"].(string)
		replyTo, _ := params["replyTo"].(string)
		to, _ := params["to"].(string)
		var target *supervisorRequest
		if jsTrim(replyTo) != "" {
			for i := range pending {
				if pending[i].ID == replyTo {
					target = &pending[i]
					break
				}
			}
			if target == nil {
				return nil, sdk.NewToolError(fmt.Sprintf("No pending supervisor request found for replyTo '%s'.", replyTo))
			}
		} else {
			matches := pending
			if needle := strings.ToLower(jsTrim(to)); needle != "" {
				matches = nil
				for i := range pending {
					if strings.HasPrefix(strings.ToLower(pending[i].ID), needle) || strings.ToLower(pending[i].Agent) == needle {
						matches = append(matches, pending[i])
					}
				}
			}
			if len(matches) == 0 {
				return nil, sdk.NewToolError("No pending supervisor requests to reply to.")
			}
			if len(matches) > 1 {
				ids := []string{}
				for _, m := range matches {
					ids = append(ids, m.ID)
				}
				return nil, sdk.NewToolError(fmt.Sprintf("Ambiguous supervisor request target; replyTo one of: %s", strings.Join(ids, ", ")))
			}
			target = &matches[0]
		}
		if err := os.MkdirAll(filepath.Dir(target.ReplyPath), 0o755); err != nil {
			return nil, sdk.NewToolError(err.Error())
		}
		payload, _ := json.Marshal(map[string]any{"message": message, "ts": nowMs()})
		if err := os.WriteFile(target.ReplyPath, payload, 0o644); err != nil {
			return nil, sdk.NewToolError(err.Error())
		}
		return explicitOK{
			Content: fmt.Sprintf("Replied to supervisor request %s.", target.ID),
			Details: map[string]any{"replyTo": target.ID, "runId": target.RunID, "agent": target.Agent},
		}, nil
	}
	return nil, sdk.NewToolError(fmt.Sprintf("Unsupported supervisor action: %s", action))
}

// supervisorPromptInstructions is what a child's stdin carries about its
// channel: how to ask, where the reply lands, and where steering arrives.
func supervisorPromptInstructions(channelDir string) string {
	return "\n\nSupervisor channel: if you need a decision from your supervisor before you can continue, write " +
		"{\"id\":\"<a new unique id>\",\"message\":\"<your question>\"} to " + filepath.Join(channelDir, "requests") +
		" as <id>.json, then keep working on what you can; the reply arrives as " + filepath.Join(channelDir, "replies") +
		"<id>.json whose content field is the answer. The supervisor may also write steering messages to " +
		filepath.Join(channelDir, "steer") +
		" as <id>.json with a message field; check that directory for new files before you finish and follow any guidance. " +
		"If a decision is required and no reply arrives, report the blocked task."
}
