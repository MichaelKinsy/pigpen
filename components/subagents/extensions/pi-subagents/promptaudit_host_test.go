package pi_subagents_test

import (
	"testing"

	sub "github.com/MichaelKinsy/pigpen/pi-subagents"
)

func TestPromptAuditProviderRouting(t *testing.T) {
	tw(t, "prompt-audit-provider-routing", "Prompt Audit rewrite uses the session model registry stream for complete providers", func(t *testing.T) {
		model := map[string]any{"id": "complete-provider-model", "name": "Complete provider model", "api": "custom-api", "provider": "complete-provider", "baseUrl": "https://opencode.ai", "reasoning": true}
		var out string
		var failure error
		var requests []map[string]any
		h := StartHost(t, sub.RewriteExtension(sub.RewriteInput{
			AuthoredTask: "original task", RuntimeAdditions: "runtime context", FinalEffectivePrompt: "runtime context\noriginal task", Guidance: "Make it concise.",
		}, &out, &failure), HostOptions{
			OnCall: func(method string, args map[string]any) (map[string]any, string) {
				switch method {
				case "getModel":
					return model, ""
				case "getModelAuth":
					return map[string]any{"ok": true, "apiKey": "complete-provider-key", "headers": map[string]any{"x-provider-auth": "present"}, "env": map[string]any{"COMPLETE_PROVIDER": "1"}}, ""
				case "getSessionID":
					return map[string]any{"sessionId": "prompt-audit-session"}, ""
				}
				return map[string]any{}, ""
			},
			ModelStream: func(m, request map[string]any) []map[string]any {
				requests = append(requests, request)
				return ModelText("rewritten task")
			},
		})
		h.write(map[string]any{"type": "notify", "notify": map[string]any{"method": "state_update", "args": map[string]any{"state": map[string]any{"model": model}}}})
		eq(t, h.Command("rewrite", ""), "", "command")
		if failure != nil {
			t.Fatal(failure)
		}
		eq(t, out, "rewritten task", "rewritten")
		eq(t, len(requests), 1, "one stream call")
		eq(t, requests[0]["apiKey"], any("complete-provider-key"), "apiKey")
		headers, _ := requests[0]["headers"].(map[string]any)
		eq(t, headers["x-opencode-session"], any("prompt-audit-session"), "session header")
		eq(t, headers["x-provider-auth"], any("present"), "auth header")
		eq(t, requests[0]["env"], any(map[string]any{"COMPLETE_PROVIDER": "1"}), "env")
	})
}
