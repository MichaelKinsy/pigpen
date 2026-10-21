package pi_subagents_test

import (
	"fmt"
	"strings"
	"testing"

	sub "github.com/MichaelKinsy/pigpen/pi-subagents"
)

// The prompt rewrite stops when the registry says the model's auth failed, with the provider, the model and the registry's reason.
func TestPromptRewriteReportsAFailedModelAuth(t *testing.T) {
	model := map[string]any{"id": "complete-provider-model", "name": "Complete provider model", "api": "custom-api", "provider": "complete-provider", "baseUrl": "https://example.com"}
	var out string
	var failure error
	requests := 0
	h := StartHost(t, sub.RewriteExtension(sub.RewriteInput{
		AuthoredTask: "original task", RuntimeAdditions: "runtime context", FinalEffectivePrompt: "runtime context\noriginal task", Guidance: "Make it concise.",
	}, &out, &failure), HostOptions{
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			switch method {
			case "getModel":
				return model, ""
			case "getModelAuth":
				return map[string]any{"ok": false, "error": "no key configured"}, ""
			}
			return map[string]any{}, ""
		},
		ModelStream: func(m, request map[string]any) []map[string]any {
			requests++
			return ModelText("rewritten task")
		},
	})
	h.write(map[string]any{"type": "notify", "notify": map[string]any{"method": "state_update", "args": map[string]any{"state": map[string]any{"model": model}}}})
	eq(t, h.Command("rewrite", ""), "", "command")
	if failure == nil {
		t.Fatalf("no failure, rewrote %q", out)
	}
	eq(t, failure.Error(), "Prompt redo model auth failed for complete-provider/complete-provider-model: no key configured", "failure")
	eq(t, requests, 0, "no model call")
}

// /subagents-guide takes at most one topic word.
func TestGuideCommandRefusesMoreThanOneWord(t *testing.T) {
	withTempHome(t)
	h := StartHost(t, Extension(), HostOptions{Cwd: tmp(t)})
	eq(t, h.Command("subagents-guide", "models extra"), "", "command")
	seen := false
	for _, c := range h.Calls() {
		seen = seen || strings.Contains(fmt.Sprint(c.Args), "Usage: /subagents-guide [topic]")
	}
	eq(t, seen, true, "the usage line is notified")
}
