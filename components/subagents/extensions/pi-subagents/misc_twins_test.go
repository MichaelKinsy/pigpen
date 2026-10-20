package pi_subagents

import (
	"math"
	"strings"
	"testing"
)

func TestAgentContractTwin(t *testing.T) {
	tw(t, "agent-contract", "does not mark interrupted runs successful", func(t *testing.T) {
		eq(t, buildExecutionProjection(runProjectionInput{ExitCode: 0, Interrupted: true}), executionProjection{Status: "paused", Success: false, ExitCode: 0, Interrupted: true}, "projection")
	})
}

func TestAgentStreamOptionsTwin(t *testing.T) {
	tskip(t, "agent-stream-options", "supports the Pi 0.81 and 0.84 constructor option names", "checks that a pi-agent-core Agent constructor receives the stream function under both of its option names (streamFunction and streamFn); the Go port streams through the host model registry (rewritePromptWithGuidance), which has no Agent constructor")
}

func TestRuntimeAcknowledgedExtensionsTwin(t *testing.T) {
	const f = "runtime-acknowledged-extensions"
	tw(t, f, "validates opaque ids without path-like content", func(t *testing.T) {
		eq(t, isRuntimeAcknowledgedExtensionID("com.example-extension:v1"), true, "valid")
		for _, bad := range []any{"", "has space", "../secret", "path/to/ext", `path\to\ext`, strings.Repeat("x", 129)} {
			eq(t, isRuntimeAcknowledgedExtensionID(bad), false, bad.(string))
		}
		eq(t, isRuntimeAcknowledgedExtensionID(float64(1)), false, "non string")
	})
	tw(t, f, "deduplicates, caps ids, and counts omitted valid ids", func(t *testing.T) {
		ids := []any{"dup", "dup"}
		for i := 0; i < 35; i++ {
			ids = append(ids, "ext-"+strconvItoa(i))
		}
		ids = append(ids, "bad id")
		p := projectRuntimeAcknowledgedExtensions(ids)
		eq(t, p.Source, "child-runtime", "source")
		eq(t, len(p.IDs), 32, "ids")
		eq(t, p.IDs[0], "dup", "first")
		eq(t, p.Omitted, 4, "omitted")
		for _, id := range p.IDs {
			eq(t, id == "bad id", false, "bad id")
		}
	})
	tw(t, f, "sanitizes persisted projections and rejects invalid shapes", func(t *testing.T) {
		got := sanitizeRuntimeAcknowledgedExtensions(js(t, `{"version":1,"source":"child-runtime","ids":["ok","ok","bad/path"],"omitted":2}`))
		eq(t, got, &runtimeAcknowledgedExtensions{Version: 1, Source: "child-runtime", IDs: []string{"ok"}, Omitted: 2}, "sanitized")
		eq(t, sanitizeRuntimeAcknowledgedExtensions(js(t, `{"version":1,"source":"launch-resolved","ids":["ok"]}`)) == nil, true, "source")
		eq(t, sanitizeRuntimeAcknowledgedExtensions(js(t, `{"version":1,"source":"child-runtime","ids":["bad/path"]}`)) == nil, true, "ids")
	})
}

func TestFastModeExtensionTwin(t *testing.T) {
	const f = "fast-mode-extension"
	tw(t, f, "returns the rewritten provider payload without a wrapper", func(t *testing.T) {
		payload := js(t, `{"model":"gpt-5.6-luna","input":[{"role":"user","content":"test"}]}`)
		got := rewriteFastModeProviderRequest(payload)
		eq(t, jsonString(got), `{"model":"gpt-5.6-luna","input":[{"role":"user","content":"test"}],"service_tier":"priority"}`, "payload")
		eq(t, jsonString(payload), `{"model":"gpt-5.6-luna","input":[{"role":"user","content":"test"}]}`, "the caller's payload is untouched")
	})
	tw(t, f, "preserves a non-object provider payload", func(t *testing.T) {
		eq(t, rewriteFastModeProviderRequest("request"), any("request"), "string payload")
	})
}

func TestExtensionBindingsTwin(t *testing.T) {
	const f = "extension-bindings"
	tw(t, f, "canonicalizes bounded namespaced JSON and isolates it from caller mutation", func(t *testing.T) {
		role := "coder"
		input := map[string]any{"shepherd.dispatch/1": map[string]any{"writeScope": []any{"src/a.ts"}, "role": role}}
		n, err := normalizeExtensionBindings(input)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, n.json, `{"shepherd.dispatch/1":{"role":"coder","writeScope":["src/a.ts"]}}`, "canonical JSON")
		input["shepherd.dispatch/1"].(map[string]any)["role"] = "reviewer"
		eq(t, strings.Contains(n.json, "reviewer"), false, "isolated")
		eq(t, strings.Contains(marshalJSON(n.value, ""), "reviewer"), false, "isolated value")
	})
	tw(t, f, "rejects malformed, oversized, and unsafe values", func(t *testing.T) {
		fail := func(input any, re string) {
			t.Helper()
			_, err := normalizeExtensionBindings(input)
			if err == nil {
				t.Errorf("accepted, want /%s/", re)
				return
			}
			polMatches(t, err.Error(), re)
		}
		fail(map[string]any{"invalid": true}, `namespace`)
		many := map[string]any{}
		for i := 0; i <= maxExtensionBindingNamespaces; i++ {
			many["pkg"+strconvItoa(i)+"/1"] = true
		}
		fail(many, `at most`)
		fail(map[string]any{"pkg/1": strings.Repeat("x", maxExtensionBindingsBytes)}, `maximum`)
		fail(map[string]any{"pkg/1": math.NaN()}, `finite`)
		inner := map[string]any{}
		cyclic := map[string]any{"pkg/1": inner}
		inner["self"] = cyclic
		fail(cyclic, `cycles`)
		// The original's last case defines an accessor property (a getter) and expects "data property"; decoded JSON and Go
		// maps have no accessors, so that input cannot be built here.
	})
	tw(t, f, "sets canonical child env for the runner host and clears it when omitted", func(t *testing.T) {
		omitted := extensionBindingsChildEnv("runner", nil)
		v, has := omitted[extensionBindingsEnv]
		eq(t, has, true, "key present")
		eq(t, v == nil, true, "cleared")
		n, _ := normalizeExtensionBindings(js(t, `{"child/1":{"z":1,"a":2}}`))
		eq(t, *extensionBindingsChildEnv("runner", n)[extensionBindingsEnv], `{"child/1":{"a":2,"z":1}}`, "runner env")
		n, _ = normalizeExtensionBindings(js(t, `{"child/1":true}`))
		eq(t, len(extensionBindingsChildEnv("parent", n)), 0, "the parent host never writes child environment values")
	})
	tw(t, f, "removes ambient bindings from external runner environments", func(t *testing.T) {
		eq(t, omitExtensionBindingsEnv(map[string]string{"KEEP_ME": "yes", extensionBindingsEnv: "secret"}), map[string]string{"KEEP_ME": "yes"}, "env")
	})
	tw(t, f, "changes launch provenance only when the binding changes", func(t *testing.T) {
		task := "task"
		base := launchBindingInput{DefinitionDigest: "definition", Task: &task}
		omitted := launchBindingDigest(base)
		with := func(j string) string {
			n, err := normalizeExtensionBindings(js(t, j))
			if err != nil {
				t.Fatal(err)
			}
			b := base
			b.ExtensionBindings = n
			return launchBindingDigest(b)
		}
		first, second := with(`{"policy/1":{"role":"coder"}}`), with(`{"policy/1":{"role":"reviewer"}}`)
		eq(t, first != omitted, true, "binding changes the digest")
		eq(t, first != second, true, "value changes the digest")
		eq(t, with(`{"policy/1":{"role":"coder"}}`), first, "stable")
	})
}

func TestExtensionLifecycleTwins(t *testing.T) {
	tskip(t, "extension-context", "clears a context replaced by session reload and returns no UI result", "wraps a Node ExtensionContext whose hasUI getter throws the host's stale-context error after session replacement; the Go SDK has no stale context object, a Context call after a session change returns a host error instead")
	tskip(t, "extension-context", "does not hide non-stale context errors", "same: the cached JavaScript UI context and its stale-context error class do not exist in the Go SDK")
	tskip(t, "extension-lazy-loading", "imports the extension entry without loading the executor or Fleet TUI", "asserts which TypeScript modules Node's module loader (registerHooks) loads when the entry is imported; a Go binary links every package statically and has no lazy module graph")
	tskip(t, "extension-maintenance-lifecycle", "starts session-owned maintenance at session_start and cleans it at shutdown", "counts the setTimeout/setInterval handles of the original's async-retention, wait-subscription and inspector maintenance by patching the Node timer globals; the port has no such timers or inspector plugins (background runs and terminal inspectors are refused)")
}

func TestPromptAuditTwin(t *testing.T) {
	tw(t, "prompt-audit-optional-peers", "ordinary prompt audit works without optional Pi peers", func(t *testing.T) {
		// The original copies prompt-audit.ts into an isolated tree without the Pi packages and imports it; a Go binary has
		// no peers to be missing, so the registry behavior is what remains.
		control := &foregroundRunControl{}
		registerLivePromptAudit(control, 0, "authored", "before authored after", livePromptMetadata{})
		eq(t, *getLivePromptAudit(control, 0), livePromptAudit{AuthoredTask: "authored", RuntimeAdditions: "before\n\nafter", FinalEffectivePrompt: "before authored after"}, "audit")
		updateLiveEffectivePrompt(control, 0, "next authored tail")
		eq(t, getLivePromptAudit(control, 0).RuntimeAdditions, "next\n\ntail", "updated")
		removeLivePromptAudit(control, 0)
		eq(t, getLivePromptAudit(control, 0) == nil, true, "removed")
	})
}
