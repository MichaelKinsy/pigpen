package pi_subagents

import (
	"fmt"
	"net/url"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Prompt Audit (src/runs/foreground/prompt-audit.ts and src/shared/opencode-session-headers.ts): the rewrite of an authored task
// from human guidance (the live prompts of a foreground run are policy_foreground.go).

// opencodeSessionHeaders are the session-routing headers OpenCode needs for internal subagent model calls; nil for every
// other provider so their requests stay byte-identical.
func opencodeSessionHeaders(provider, baseURL, sessionID string) map[string]any {
	if sessionID == "" {
		return nil
	}
	if provider != "opencode" && provider != "opencode-go" {
		u, err := url.Parse(baseURL)
		if err != nil || u.Hostname() != "opencode.ai" {
			return nil
		}
	}
	return map[string]any{"x-opencode-session": sessionID, "x-opencode-client": "pi"}
}

// rewriteInput is the input of rewritePromptWithGuidance.
type rewriteInput struct {
	AuthoredTask, RuntimeAdditions, FinalEffectivePrompt, Guidance string
}

const rewriteSystemPrompt = "Rewrite one subagent authored task from Prompt Audit context.\n" +
	"Return only the revised authored task text.\n" +
	"Do not add Markdown fences, explanations, labels, or commentary.\n" +
	"Preserve the original intent unless the guidance explicitly changes it.\n" +
	"Do not include runtime additions unless they are needed as ordinary task context."

func assistantText(message map[string]any) string {
	var b strings.Builder
	switch c := message["content"].(type) {
	case string:
		return jsTrim(c)
	case []any:
		for _, item := range c {
			if m, ok := item.(map[string]any); ok && m["type"] == "text" {
				if s, ok := m["text"].(string); ok {
					b.WriteString(s)
				}
			}
		}
	}
	return jsTrim(b.String())
}

// rewritePromptWithGuidance asks the session's model, through the host's model registry, to rewrite an authored task.
func rewritePromptWithGuidance(ctx sdk.Context, in rewriteInput) (string, error) {
	registry := ctx.ModelRegistry()
	var model map[string]any
	if ctx.Model() != "" {
		model = registry.Find(ctx.ModelProvider(), ctx.Model())
	}
	if model == nil {
		return "", fmt.Errorf("Prompt redo needs the current session model to rewrite the authored task.")
	}
	provider, _ := model["provider"].(string)
	id, _ := model["id"].(string)
	auth, err := registry.GetApiKeyAndHeaders(model)
	if err != nil {
		return "", err
	}
	if ok, has := auth["ok"].(bool); has && !ok {
		return "", fmt.Errorf("Prompt redo model auth failed for %s/%s: %v", provider, id, auth["error"])
	}
	options := map[string]any{}
	if key, _ := auth["apiKey"].(string); key != "" {
		options["apiKey"] = key
	}
	if env, ok := auth["env"].(map[string]any); ok && env != nil {
		options["env"] = env
	}
	sessionID, _ := ctx.GetSessionID()
	baseURL, _ := model["baseUrl"].(string)
	headers := opencodeSessionHeaders(provider, baseURL, sessionID)
	if headers == nil {
		headers = map[string]any{}
	}
	if h, ok := auth["headers"].(map[string]any); ok {
		for k, v := range h {
			headers[k] = v
		}
	}
	options["headers"] = headers
	prompt := strings.Join([]string{
		"Guidance from the human:", jsTrim(in.Guidance), "",
		"Authored task:", in.AuthoredTask, "",
		"Runtime additions for context only:", in.RuntimeAdditions, "",
		"Final effective prompt for context only:", in.FinalEffectivePrompt,
	}, "\n")
	request := map[string]any{
		"systemPrompt": rewriteSystemPrompt,
		"messages":     []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": prompt}}}},
	}
	message := registry.StreamSimple(model, request, options).Result()
	if message["stopReason"] == "error" || message["stopReason"] == "aborted" {
		reason, _ := message["errorMessage"].(string)
		return "", fmt.Errorf("%s", reason)
	}
	rewritten := assistantText(message)
	if rewritten == "" {
		return "", fmt.Errorf("Prompt redo rewrite returned an empty prompt.")
	}
	return rewritten, nil
}
