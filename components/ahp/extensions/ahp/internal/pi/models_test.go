package pi_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/pigpen/ahp/internal/pi"
	"github.com/MichaelKinsy/pigpen/ahp/internal/testkit"
	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
)

// Twins of upstream test/models.test.ts: mapping Pi's model catalogue onto protocol agent/model
// descriptions.

func testModel(mutate ...func(*pi.Model)) pi.Model {
	m := pi.Model{
		ID: "claude-sonnet-4", Name: "Claude Sonnet 4", API: "anthropic-messages", Provider: "anthropic",
		BaseURL: "https://api.anthropic.com", Reasoning: true, Input: []string{"text", "image"},
		Cost:          json.RawMessage(`{"input":3,"output":15,"cacheRead":0.3,"cacheWrite":3.75}`),
		ContextWindow: 200_000, MaxTokens: 16_384,
	}
	for _, f := range mutate {
		f(&m)
	}
	return m
}

func TestModelMapping(t *testing.T) {
	twin.Run(t, "models", "offers the standard thinking levels when no extended levels are declared", func(t *testing.T) {
		if got := pi.SupportedThinkingLevels(testModel()); !reflect.DeepEqual(got, []string{"off", "minimal", "low", "medium", "high"}) {
			t.Fatalf("levels = %v", got)
		}
	})

	twin.Run(t, "models", "offers only `off` for a model without reasoning", func(t *testing.T) {
		got := pi.SupportedThinkingLevels(testModel(func(m *pi.Model) { m.Reasoning = false }))
		if !reflect.DeepEqual(got, []string{"off"}) {
			t.Fatalf("levels = %v", got)
		}
	})

	twin.Run(t, "models", "drops unsupported levels and opts into extended levels exactly as pi does", func(t *testing.T) {
		m := testModel(func(m *pi.Model) {
			m.ThinkingLevelMap = map[string]json.RawMessage{"off": json.RawMessage("null"), "minimal": json.RawMessage("null"), "xhigh": json.RawMessage(`"xhigh"`), "max": json.RawMessage("null")}
		})
		if got := pi.SupportedThinkingLevels(m); !reflect.DeepEqual(got, []string{"low", "medium", "high", "xhigh"}) {
			t.Fatalf("levels = %v", got)
		}
	})

	twin.Run(t, "models", "uses pi's provider-qualified model ids on the wire", func(t *testing.T) {
		if got := pi.ModelSelectionID("anthropic", "claude-sonnet-4"); got != "anthropic/claude-sonnet-4" {
			t.Fatalf("id = %s", got)
		}
		m := testModel(func(m *pi.Model) { m.Provider, m.ID = "openrouter", "anthropic/claude-sonnet-4" })
		if got := m.SelectionID(); got != "openrouter/anthropic/claude-sonnet-4" {
			t.Fatalf("id = %s", got)
		}
	})

	twin.Run(t, "models", "resolves qualified ids without confusing providers", func(t *testing.T) {
		direct := testModel(func(m *pi.Model) { m.Provider, m.ID, m.Name = "openai", "gpt-6-astra", "GPT-6 Astra (API)" })
		codex := testModel(func(m *pi.Model) { m.Provider, m.ID, m.Name = "openai-codex", "gpt-6-astra", "GPT-6 Astra (Codex)" })
		models := []pi.Model{direct, codex}
		if got, ok := pi.FindModelBySelectionID(models, codex.SelectionID(), nil); !ok || got.Name != codex.Name {
			t.Fatalf("qualified lookup = %+v %v", got, ok)
		}
		if _, ok := pi.FindModelBySelectionID(models, "gpt-6-astra", nil); ok {
			t.Fatal("an ambiguous bare id must not resolve")
		}
		if got, ok := pi.FindModelBySelectionID(models, "gpt-6-astra", &codex); !ok || got.Name != codex.Name {
			t.Fatalf("bare id with the current model = %+v %v", got, ok)
		}
	})

	twin.Run(t, "models", "accepts an unambiguous legacy bare model id", func(t *testing.T) {
		only := testModel()
		other := testModel(func(m *pi.Model) { m.ID = "other-model" })
		if got, ok := pi.FindModelBySelectionID([]pi.Model{only, other}, only.ID, nil); !ok || got.ID != only.ID {
			t.Fatalf("got %+v %v", got, ok)
		}
	})

	twin.Run(t, "models", "exposes thinking level as a model configSchema", func(t *testing.T) {
		info := pi.ToSessionModelInfo(testModel())
		if info.ConfigSchema == nil {
			t.Fatal("no configSchema")
		}
		property, ok := info.ConfigSchema.Properties[pi.ThinkingConfigKey]
		if !ok || property.Type != "string" || string(*property.Default) != `"medium"` {
			t.Fatalf("property = %+v", property)
		}
		sameJSON(t, property.Enum, []string{"off", "minimal", "low", "medium", "high"}, "enum")
		if info.Id != "anthropic/claude-sonnet-4" || info.Provider != pi.Provider || info.MaxContextWindow == nil || *info.MaxContextWindow != 200_000 {
			t.Fatalf("info = %+v", info)
		}
	})

	twin.Run(t, "models", "maps pi input modalities onto the vision capability", func(t *testing.T) {
		if v := pi.ToSessionModelInfo(testModel()).SupportsVision; v == nil || !*v {
			t.Fatal("an image-capable model must support vision")
		}
		if v := pi.ToSessionModelInfo(testModel(func(m *pi.Model) { m.Input = []string{"text"} })).SupportsVision; v == nil || *v {
			t.Fatal("a text-only model must not support vision")
		}
	})

	twin.Run(t, "models", "omits the configSchema when there is nothing to choose", func(t *testing.T) {
		// A single-option picker is worse than no picker.
		if pi.ToSessionModelInfo(testModel(func(m *pi.Model) { m.Reasoning = false })).ConfigSchema != nil {
			t.Fatal("configSchema must be omitted")
		}
	})

	twin.Run(t, "models", "produces a schema-conforming AgentInfo", func(t *testing.T) {
		agent := pi.BuildAgentInfo([]pi.Model{testModel(), testModel(func(m *pi.Model) { m.ID, m.Name, m.Reasoning = "gpt-5", "GPT-5", false })})
		if agent.Provider != pi.Provider || len(agent.Models) != 2 {
			t.Fatalf("agent = %+v", agent)
		}
		// No capabilities declared: one chat, one working directory. Their absence is what tells a
		// client not to attempt those calls.
		if agent.Capabilities != nil || len(agent.ProtectedResources) != 0 || agent.Customizations != nil {
			t.Fatalf("agent declares more than it supports: %+v", agent)
		}
		testkit.AssertValid(t, "state", "AgentInfo", agent)
	})
}
