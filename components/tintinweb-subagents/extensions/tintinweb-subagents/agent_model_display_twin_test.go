package tintinweb_subagents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Twins of test/agent-model-display.test.ts: the model and thinking level the surfaces claim an agent ran with must
// be the ones it actually ran with.

var displayModels = []any{
	obj{"provider": "anthropic", "id": "claude-opus-4-6", "name": "Claude Opus 4.6"},
	obj{"provider": "anthropic", "id": "claude-haiku-4-5", "name": "Claude Haiku 4.5"},
}

// startModelRig is startRigIn with a parent session on a model of its own (so "inherited" is a real case) and the two
// models available.
func startModelRig(t *testing.T, cwd string) *rig {
	t.Helper()
	resetTypes()
	t.Cleanup(resetTypes)
	r := &rig{fleet: &fleet{}, extBus: map[string][]string{}, goBus: map[string][]func(any){}}
	e := sdk.New("tintinweb-subagents")
	r.app = newApp(e, r.fleet.start)
	r.app.register(e)
	r.Host = StartHost(t, e, HostOptions{Cwd: cwd, OnCallValue: func(method string, args map[string]any) (any, string) {
		switch method {
		case "getModelInfo":
			return obj{"id": "claude-opus-4-6", "name": "Claude Opus 4.6", "provider": "anthropic"}, ""
		case "getModelRegistryState":
			return obj{"models": displayModels, "providers": obj{"anthropic": obj{"configured": true}}}, ""
		}
		return r.answer(method, args)
	}})
	r.Fire("session_start", obj{"reason": "startup"})
	t.Cleanup(func() { r.app.mgr.abortAll() })
	return r
}

func pinnedAgent(t *testing.T, frontmatter string) string {
	t.Helper()
	cwd := t.TempDir()
	dir := filepath.Join(cwd, ".pi", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\ndescription: pins its own settings\n" + frontmatter + "---\n\nPinned.\n"
	if err := os.WriteFile(filepath.Join(dir, "pinned.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return cwd
}

// toolOutcome is a tool result as the host carries it.
type toolOutcome struct {
	Content string       `json:"content"`
	Details agentDetails `json:"details"`
}

func (r *rig) agentCall(params obj) toolOutcome {
	r.t.Helper()
	raw, failure := r.Host.Tool("Agent", params)
	if failure != "" {
		r.t.Fatalf("Agent failed: %s", failure)
	}
	var out toolOutcome
	if err := json.Unmarshal(raw, &out); err != nil {
		r.t.Fatalf("%s: %v", raw, err)
	}
	return out
}

// sessionReports is what a child session says about itself once pi has resolved it.
func sessionReports(provider, id, name, thinking string) func(cmd string) (obj, error) {
	return func(cmd string) (obj, error) {
		return obj{"data": obj{"model": obj{"provider": provider, "id": id, "name": name}, "thinkingLevel": thinking}}, nil
	}
}

// foregroundRun runs a foreground Agent call; while the child runs, onChild sees its UI state (before the session has
// reported) and then the session reports query's answers and the child finishes.
func foregroundRun(t *testing.T, r *rig, params obj, query func(string) (obj, error), onChild func(rec *agentRecord, ua *uiAgent)) toolOutcome {
	t.Helper()
	go func() {
		children := r.fleet.wait(t, 1)
		var rec *agentRecord
		var ua *uiAgent
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) && ua == nil {
			if recs := r.app.mgr.list(); len(recs) > 0 {
				rec = recs[0]
				r.app.ui.mu.Lock()
				ua = r.app.ui.agents[rec.ID]
				r.app.ui.mu.Unlock()
			}
			time.Sleep(2 * time.Millisecond)
		}
		if onChild != nil && ua != nil {
			onChild(rec, ua)
		}
		if ua != nil && query != nil {
			ua.refreshModel(func(cmd string) (map[string]any, error) { return query(cmd) })
		}
		children[0].finish("done")
	}()
	if params["run_in_background"] == nil {
		params["run_in_background"] = false
	}
	return r.agentCall(params)
}

func TestAgentModelDisplayTwins(t *testing.T) {
	const file = "agent-model-display"
	th := plainTheme()
	base := obj{"prompt": "go", "description": "d", "subagent_type": "general-purpose"}
	with := func(extra obj) obj {
		out := obj{}
		for k, v := range base {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	render := func(out toolOutcome, expanded bool) string {
		return strings.Join(renderAgentResult(out.Content, &out.Details, expanded, false, false, th, false, 200), "\n")
	}
	hasTag := func(t *testing.T, tags []string, want string) {
		t.Helper()
		for _, tag := range tags {
			if tag == want {
				return
			}
		}
		t.Errorf("tags %q lack %q", tags, want)
	}

	tw(t, file, "names the model even when the child inherited the parent's", func(t *testing.T) {
		// The old rule was "show it only when it differs from the parent", which left `thinking: high` attached to
		// nothing on the common path.
		r := startModelRig(t, t.TempDir())
		result := foregroundRun(t, r, with(nil), sessionReports("anthropic", "claude-opus-4-6", "Claude Opus 4.6", "high"), nil)
		eqStr(t, result.Details.ModelName, "opus 4.6")
		hasStr(t, render(result, false), "opus 4.6")
		hasStr(t, render(result, true), "opus 4.6")
	})
	tw(t, file, "names the inherited model while streaming, before a session exists", func(t *testing.T) {
		// The streaming row renders from the pre-session snapshot, which the old rule left empty for every agent that
		// inherited: the level had nothing to attach itself to.
		r := startModelRig(t, t.TempDir())
		var streamed agentDetails
		foregroundRun(t, r, with(nil), nil, func(rec *agentRecord, _ *uiAgent) {
			streamed = r.app.runningDetails(rec, rec.Type, "d", 0)
		})
		eqStr(t, streamed.ModelName, "opus 4.6")
		partial := strings.Join(renderAgentResult("", &streamed, false, true, false, th, false, 200), "\n")
		hasStr(t, partial, "opus 4.6")
	})
	tw(t, file, "keeps the twin label beside the model", func(t *testing.T) {
		// The mode label hangs off the agent type, not the invocation, so anything that rebuilds tags from the
		// invocation alone silently drops it.
		r := startModelRig(t, t.TempDir())
		result := foregroundRun(t, r, with(nil), sessionReports("anthropic", "claude-opus-4-6", "Claude Opus 4.6", "high"), nil)
		hasTag(t, result.Details.Tags, "twin")
		hasStr(t, render(result, false), "twin")
	})
	tw(t, file, "reports the session's level, and what was asked for, when pi clamps it", func(t *testing.T) {
		r := startModelRig(t, t.TempDir())
		result := foregroundRun(t, r, with(obj{"thinking": "max"}), sessionReports("anthropic", "claude-haiku-4-5", "Claude Haiku 4.5", "high"), nil)
		hasTag(t, result.Details.Tags, "thinking: high (asked max)")
	})
	// Asserted on the immediate background result, which renders BEFORE a session exists. That is the only place the two
	// causes of a mismatch are separable: a clamp cannot have happened yet, so "(asked max)" here can only come from the
	// agent file outranking the parameter.
	tw(t, file, "discloses a level an agent file pinned over the caller's (#182)", func(t *testing.T) {
		r := startModelRig(t, pinnedAgent(t, "thinking: low\n"))
		result := r.agentCall(obj{"prompt": "go", "description": "d", "subagent_type": "pinned", "thinking": "max", "run_in_background": true})
		hasTag(t, result.Details.Tags, "thinking: low (asked max)")
	})
	tw(t, file, "discloses a model an agent file pinned over the caller's (#182)", func(t *testing.T) {
		r := startModelRig(t, pinnedAgent(t, "model: anthropic/claude-haiku-4-5\n"))
		result := r.agentCall(obj{"prompt": "go", "description": "d", "subagent_type": "pinned", "model": "anthropic/claude-opus-4-6", "run_in_background": true})
		eqStr(t, result.Details.ModelName, "haiku 4.5 (asked anthropic/claude-opus-4-6)")
	})
	tw(t, file, "stays quiet when the caller's spelling names the model that won", func(t *testing.T) {
		// Model input is fuzzy: `"haiku"` and `"anthropic/claude-haiku-4-5"` are the same model, and the frontmatter did
		// not take anything away from the caller. Comparing the raw strings would print "haiku 4.5 (asked haiku)".
		r := startModelRig(t, pinnedAgent(t, "model: anthropic/claude-haiku-4-5\n"))
		result := r.agentCall(obj{"prompt": "go", "description": "d", "subagent_type": "pinned", "model": "haiku", "run_in_background": true})
		eqStr(t, result.Details.ModelName, "haiku 4.5")
	})
	tw(t, file, "discloses a spelling that names no available model at all", func(t *testing.T) {
		r := startModelRig(t, pinnedAgent(t, "model: anthropic/claude-haiku-4-5\n"))
		result := r.agentCall(obj{"prompt": "go", "description": "d", "subagent_type": "pinned", "model": "gpt-9", "run_in_background": true})
		eqStr(t, result.Details.ModelName, "haiku 4.5 (asked gpt-9)")
	})
	tw(t, file, "says nothing about a request that was honored", func(t *testing.T) {
		r := startModelRig(t, t.TempDir())
		result := foregroundRun(t, r, with(obj{"thinking": "high"}), sessionReports("anthropic", "claude-opus-4-6", "Claude Opus 4.6", "high"), nil)
		hasTag(t, result.Details.Tags, "thinking: high")
		lacksStr(t, render(result, false), "asked")
	})
	tskip(t, file, "renders the reopened session's settings, not the resume call's",
		"`resume` of a finished agent is refused by this port (gap G9: the port cannot reopen a session yet), so there is no resumed result to render")
}
