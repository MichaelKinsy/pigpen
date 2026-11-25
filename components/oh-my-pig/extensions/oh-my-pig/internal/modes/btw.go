package modes

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// upstream: packages/coding-agent/src/session/btw-turn.ts
// upstream: packages/coding-agent/src/session/btw-history.ts
// upstream: packages/coding-agent/src/modes/controllers/btw-controller.ts
// upstream: packages/coding-agent/src/prompts/system/btw-user.md

// BtwRecord stores one completed `/btw` Q&A pair in a session-local sidecar
// file under the session's `local://` artifacts directory.
// upstream: packages/coding-agent/src/session/btw-history.ts:120
// ("Session-local sidecar storage; never reads or writes the main session journal.")
type BtwRecord struct {
	ID        string `json:"id"`
	Question  string `json:"question"`
	Answer    string `json:"answer"`
	CreatedAt int64  `json:"createdAt"`
}

// RenderBtwUserPrompt wraps a side question in omp's `<btw>` template.
// upstream: packages/coding-agent/src/prompts/system/btw-user.md
func RenderBtwUserPrompt(question string, inject bool) string {
	return renderTemplate(btwUserTemplate, templateData{
		Vars:  map[string]string{"question": question},
		Bools: map[string]bool{"inject": inject},
	})
}

func btwSidecarDir(ctx sdk.Context) string {
	root := resolveLocalRootDir(ctx)
	if root == "" {
		return ""
	}
	return filepath.Join(root, "btw")
}

func loadBtwSidecar(ctx sdk.Context) []BtwRecord {
	dir := btwSidecarDir(ctx)
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var records []BtwRecord
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var rec BtwRecord
		if json.Unmarshal(raw, &rec) == nil && rec.Question != "" {
			records = append(records, rec)
		}
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].CreatedAt != records[j].CreatedAt {
			return records[i].CreatedAt < records[j].CreatedAt
		}
		return records[i].ID < records[j].ID
	})
	return records
}

func saveBtwSidecar(ctx sdk.Context, rec BtwRecord) error {
	dir := btwSidecarDir(ctx)
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(filepath.Join(dir, rec.ID+".json"), raw, 0o600)
}

// handleBtwCommand implements `/btw [question]`.
// Without arguments it browses this session's BTW sidecar history; with a
// question it runs an ephemeral one-shot completion via `ctx.Complete` with
// full session branch context and without mutating the main session journal or
// interrupting an active turn.
// upstream: packages/coding-agent/src/slash-commands/builtin-lifecycle.ts:424-438
func (m *manager) handleBtwCommand(ctx sdk.Context, args string) error {
	question := strings.TrimSpace(args)
	if question == "" {
		return m.showBtwHistory(ctx)
	}

	modelInfo, err := ctx.GetModelInfo()
	if err != nil || modelInfo == nil || modelInfo.ID == "" {
		notifyError(ctx, "No active model available for /btw.")
		return nil
	}
	modelObj := map[string]any{
		"id":       modelInfo.ID,
		"provider": modelInfo.Provider,
	}
	if regModel, err := ctx.ModelRegistry().Find(modelInfo.Provider, modelInfo.ID); err == nil && regModel != nil {
		modelObj = regModel
	}

	var auth map[string]any
	if rawAuth, err := ctx.GetModelAuth(modelInfo.Provider, modelInfo.ID); err == nil && len(rawAuth) > 0 {
		_ = json.Unmarshal(rawAuth, &auth)
	}

	messages := buildBtwMessages(ctx, question)
	req := map[string]any{
		"messages":   messages,
		"toolChoice": "none",
	}
	if sysPrompt, err := ctx.GetSystemPrompt(); err == nil && sysPrompt != "" {
		req["systemPrompt"] = sysPrompt
	}

	raw, err := ctx.Complete(modelObj, req, auth)
	if err != nil {
		notifyError(ctx, fmt.Sprintf("/btw failed: %v", err))
		return nil
	}

	answer, errMsg := extractCompleteText(raw)
	if errMsg != "" {
		notifyError(ctx, fmt.Sprintf("/btw failed: %s", errMsg))
		return nil
	}
	if answer == "" {
		answer = "(empty response)"
	}

	now := m.now()
	rec := BtwRecord{
		ID:        fmt.Sprintf("btw-%d-%d", now, goalIDSeq.Add(1)),
		Question:  question,
		Answer:    answer,
		CreatedAt: now,
	}
	// Persist only in the session-local sidecar; never append to the main session journal.
	_ = saveBtwSidecar(ctx, rec)

	notifyInfo(ctx, fmt.Sprintf("BTW: %s\n\n%s", question, answer))
	return nil
}

func (m *manager) showBtwHistory(ctx sdk.Context) error {
	hist := loadBtwSidecar(ctx)
	if len(hist) == 0 {
		notifyInfo(ctx, "No /btw history in this session yet. Usage: /btw <question>")
		return nil
	}
	if !ctx.HasUI() {
		var b strings.Builder
		b.WriteString("BTW History:\n")
		for i, rec := range hist {
			fmt.Fprintf(&b, "%d. %s\n   %s\n", i+1, sanitizeSingleLine(rec.Question, 80), sanitizeSingleLine(rec.Answer, 120))
		}
		notifyInfo(ctx, strings.TrimRight(b.String(), "\n"))
		return nil
	}
	options := make([]string, len(hist))
	for i := range hist {
		idx := len(hist) - 1 - i
		options[i] = fmt.Sprintf("%d. %s", idx+1, sanitizeSingleLine(hist[idx].Question, 72))
	}
	pickedVal, ok, err := ctx.Select("BTW history", options)
	if err != nil || !ok {
		return err
	}
	picked, _ := pickedVal.(string)
	for i, opt := range options {
		if opt == picked {
			rec := hist[len(hist)-1-i]
			notifyInfo(ctx, fmt.Sprintf("BTW: %s\n\n%s", rec.Question, rec.Answer))
			break
		}
	}
	return nil
}

// buildBtwMessages assembles full conversation context from the session branch
// (including compaction summaries, user/assistant text, tool calls, and tool
// results) followed by the `<btw>` question prompt.
func buildBtwMessages(ctx sdk.Context, question string) []map[string]any {
	var messages []map[string]any
	if branch, err := ctx.GetBranch(); err == nil {
		for _, entry := range branch {
			switch entry.Type {
			case "compaction", "branch_summary":
				if strings.TrimSpace(entry.Content) != "" {
					messages = append(messages, map[string]any{
						"role":    "user",
						"content": []map[string]any{{"type": "text", "text": entry.Content}},
					})
				}
			case "message":
				switch entry.Role {
				case "user":
					if strings.TrimSpace(entry.Content) != "" {
						messages = append(messages, map[string]any{
							"role":    "user",
							"content": []map[string]any{{"type": "text", "text": entry.Content}},
						})
					}
				case "assistant":
					var parts []string
					if strings.TrimSpace(entry.Content) != "" {
						parts = append(parts, entry.Content)
					}
					for _, tc := range entry.ToolCalls {
						parts = append(parts, fmt.Sprintf("[tool_call %s(%s)]", tc.Name, sanitizeSingleLine(tc.Args, 200)))
					}
					if len(parts) > 0 {
						messages = append(messages, map[string]any{
							"role":    "assistant",
							"content": []map[string]any{{"type": "text", "text": strings.Join(parts, "\n")}},
						})
					}
				case "toolResult":
					if strings.TrimSpace(entry.Content) != "" {
						header := fmt.Sprintf("[tool_result %s]", entry.ToolName)
						if entry.IsError {
							header = fmt.Sprintf("[tool_result %s error]", entry.ToolName)
						}
						messages = append(messages, map[string]any{
							"role":    "user",
							"content": []map[string]any{{"type": "text", "text": header + "\n" + entry.Content}},
						})
					}
				}
			}
		}
	}
	messages = append(messages, map[string]any{
		"role":    "user",
		"content": []map[string]any{{"type": "text", "text": RenderBtwUserPrompt(question, false)}},
	})
	return messages
}

// extractCompleteText parses an assistant message returned by `ctx.Complete`.
func extractCompleteText(raw json.RawMessage) (text, errMsg string) {
	if len(raw) == 0 {
		return "", "empty response from provider"
	}
	var resp struct {
		StopReason   string `json:"stopReason"`
		Error        string `json:"error"`
		ErrorMessage string `json:"errorMessage"`
		Content      any    `json:"content"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", err.Error()
	}
	if resp.Error != "" {
		return "", resp.Error
	}
	if resp.ErrorMessage != "" {
		return "", resp.ErrorMessage
	}
	if resp.StopReason == "error" && resp.Content == nil {
		return "", "provider returned error"
	}
	switch c := resp.Content.(type) {
	case string:
		return strings.TrimSpace(c), ""
	case []any:
		var parts []string
		for _, item := range c {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if block["type"] == "text" {
				if t, ok := block["text"].(string); ok {
					parts = append(parts, t)
				}
			}
		}
		return strings.TrimSpace(strings.Join(parts, "")), ""
	}
	return "", ""
}
