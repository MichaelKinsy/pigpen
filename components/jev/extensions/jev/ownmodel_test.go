package jev_test

import (
	"encoding/json"
	"strings"
	"testing"
)

// The default judge: the model PiG is configured with, through the shared client's
// own-model backend (prompted JSON answers, validated strictly). No API key and no
// endpoint of ours: the host authenticates and calls the provider.

func gateAnswers(d, e, b float64, impactLevel int) string {
	probs := map[string]float64{"0": 0, "1": 0, "2": 0, "3": 0}
	probs[string(rune('0'+impactLevel))] = 1
	a, _ := json.Marshal(map[string]any{"answers": map[string]any{"destructive": d, "exfiltration": e, "beyond_scope": b, "impact": probs}})
	return string(a)
}

func outAnswers(leak float64, class string) string {
	probs := map[string]float64{"transient": 0, "environment": 0, "code_bug": 0, "permission": 0, "user_error": 0, "no_failure": 0}
	probs[class] = 1
	a, _ := json.Marshal(map[string]any{"answers": map[string]any{"leaks_secret": leak, "failure_class": probs}})
	return string(a)
}

type modelCalls struct{ models, requests []map[string]any }

func modelHost(t *testing.T, reply func(request map[string]any) []map[string]any, cfg map[string]any) (*Host, *modelCalls, *env) {
	t.Helper()
	e := newEnv(t)
	c := map[string]any{"enabled": true, "acknowledged": true, "display": "plain"}
	for k, v := range cfg {
		c[k] = v
	}
	e.writeGlobal(t, c)
	mc := &modelCalls{}
	hs := newHostState()
	h := start(t, e, hs, HostOptions{ModelStream: func(model, request map[string]any) []map[string]any {
		mc.models, mc.requests = append(mc.models, model), append(mc.requests, request)
		return reply(request)
	}})
	return h, mc, e
}

func TestOwnModel_GateJudgesWithTheSessionModel(t *testing.T) {
	h, mc, e := modelHost(t, func(map[string]any) []map[string]any { return ModelText(gateAnswers(0.99, 0.1, 0.1, 3)) }, nil)
	block, _ := h.toolCall("bash", bash("rm -rf src"))
	if block {
		t.Fatal("shadow mode blocked")
	}
	if !h.anyNotification("warning: jev shadow: bash - destructive 0.99, impact 3.00/3 at confidence 1.00") {
		t.Errorf("notifications = %q", h.notifications())
	}
	if len(mc.models) != 1 || mc.models[0]["provider"] != "acme" || (mc.models[0]["modelId"] != "judge-1" && mc.models[0]["id"] != "judge-1") {
		t.Errorf("the judge is not the session model: %v", mc.models)
	}
	all, _ := json.Marshal(mc.requests[0])
	mustContain(t, "model request", string(all), "rm -rf src", e.cwd, "Is this action destructive?")
}

func TestOwnModel_OutputJudgeAppendsTheNotice(t *testing.T) {
	h, _, _ := modelHost(t, func(map[string]any) []map[string]any { return ModelText(outAnswers(0.95, "no_failure")) }, nil)
	patched, none := h.toolResult("bash", bash("cat .env"), text("API_KEY=abc"), false)
	if none || !strings.Contains(patched[len(patched)-1], "containing a secret (0.95)") {
		t.Errorf("patched = %q", patched)
	}
}

func TestOwnModel_ConfiguredModelOverridesTheSessionModel(t *testing.T) {
	h, mc, _ := modelHost(t, func(map[string]any) []map[string]any { return ModelText(gateAnswers(0, 0, 0, 0)) }, map[string]any{"model": "other/cheap-1"})
	h.toolCall("bash", bash("ls"))
	if len(mc.models) != 1 || mc.models[0]["provider"] != "other" {
		t.Errorf("models = %v", mc.models)
	}
}

func TestOwnModel_MalformedAnswerFailsOpen(t *testing.T) {
	h, _, _ := modelHost(t, func(map[string]any) []map[string]any { return ModelText("I think this is fine.") }, nil)
	if block, _ := h.toolCall("bash", bash("rm -rf /")); block {
		t.Fatal("an unusable answer blocked a call")
	}
	if !h.anyNotification("(failing open)") || strings.Contains(h.lastStatus(), "clear") {
		t.Errorf("notifications %q status %q", h.notifications(), h.lastStatus())
	}
}

func TestOwnModel_ProviderErrorFailsOpen(t *testing.T) {
	h, _, _ := modelHost(t, func(map[string]any) []map[string]any { return ModelError("provider down") }, nil)
	if block, _ := h.toolCall("bash", bash("ls")); block {
		t.Fatal("blocked")
	}
	if !h.anyNotification("provider down") || h.lastStatus() != "jev: unavailable (failing open)" {
		t.Errorf("notifications %q status %q", h.notifications(), h.lastStatus())
	}
}

func TestOwnModel_JevAskUsesTheSessionModel(t *testing.T) {
	h, mc, _ := modelHost(t, func(map[string]any) []map[string]any {
		return ModelText(`{"answers":{"relevant":0.9}}`)
	}, nil)
	got, details := askResult(t, h, map[string]any{"state": "the diff", "questions": []any{map[string]any{"id": "relevant", "type": "noul", "instructions": "Is this relevant?"}}})
	if details["ok"] != true || !strings.Contains(got, "relevant: yes 0.90") || len(mc.models) != 1 {
		t.Errorf("got %q %v (%d model calls)", got, details, len(mc.models))
	}
}

func TestOwnModel_NoModelNoJudge(t *testing.T) {
	e := newEnv(t)
	e.writeGlobal(t, map[string]any{"enabled": true, "acknowledged": true, "display": "plain"})
	hs := newHostState()
	hs.model = map[string]any{}
	h := start(t, e, hs, HostOptions{})
	h.toolCall("bash", bash("ls"))
	if !h.anyNotification("needs a model") {
		t.Errorf("notifications = %q", h.notifications())
	}
}
