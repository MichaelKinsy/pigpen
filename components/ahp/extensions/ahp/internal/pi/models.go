package pi

import (
	"encoding/json"
	"strings"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"
)

// Mapping Pi's model catalogue onto protocol agent/model descriptions (port of src/pi/models.ts).

// ThinkingConfigKey is the model-config key that carries the reasoning effort.
const ThinkingConfigKey = "thinkingLevel"

// Model is a Pi model (pi-ai's Model): the fields the mapping reads. It is decoded from the JSON
// the SDK's model registry returns.
type Model struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	API      string `json:"api"`
	Provider string `json:"provider"`
	BaseURL  string `json:"baseUrl,omitempty"`
	// Reasoning reports whether the model can think.
	Reasoning bool     `json:"reasoning"`
	Input     []string `json:"input"`
	// Cost is passed through untouched as pricing metadata.
	Cost          json.RawMessage `json:"cost,omitempty"`
	ContextWindow int64           `json:"contextWindow"`
	MaxTokens     int64           `json:"maxTokens"`
	// ThinkingLevelMap maps a level to the provider's own value; null declares the level
	// unsupported, and absence is "not declared" (which matters for xhigh and max).
	ThinkingLevelMap map[string]json.RawMessage `json:"thinkingLevelMap,omitempty"`
}

// ModelFromMap decodes a model from the generic JSON the SDK hands over.
func ModelFromMap(m map[string]any) (Model, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return Model{}, err
	}
	var model Model
	return model, json.Unmarshal(raw, &model)
}

// ModelSelectionID is the wire id of a model: Pi's (provider, id) identity, qualified so two
// providers offering the same model id stay distinct.
func ModelSelectionID(provider, id string) string { return provider + "/" + id }

// SelectionID is the wire id of the model.
func (m Model) SelectionID() string { return ModelSelectionID(m.Provider, m.ID) }

// FindModelBySelectionID resolves a wire id. The qualified form is authoritative. A bare id
// (legacy clients) is accepted only when it is unambiguous, or when it names the model already in
// use, so a client can never silently land on the wrong provider's model.
func FindModelBySelectionID(models []Model, selectionID string, current *Model) (Model, bool) {
	for _, m := range models {
		if m.SelectionID() == selectionID {
			return m, true
		}
	}
	var legacy []Model
	for _, m := range models {
		if m.ID == selectionID {
			legacy = append(legacy, m)
		}
	}
	if len(legacy) == 1 {
		return legacy[0], true
	}
	if current != nil && current.ID == selectionID {
		for _, m := range legacy {
			if m.Provider == current.Provider {
				return m, true
			}
		}
	}
	return Model{}, false
}

var extendedThinkingLevels = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}

// SupportedThinkingLevels is pi-ai's getSupportedThinkingLevels: a model that cannot reason
// offers only "off"; a level mapped to null is unsupported; xhigh and max are opt-in (they must be
// mapped to something).
func SupportedThinkingLevels(m Model) []string {
	if !m.Reasoning {
		return []string{"off"}
	}
	var out []string
	for _, level := range extendedThinkingLevels {
		mapped, declared := m.ThinkingLevelMap[level]
		if declared && strings.TrimSpace(string(mapped)) == "null" {
			continue
		}
		if (level == "xhigh" || level == "max") && !declared {
			continue
		}
		out = append(out, level)
	}
	return out
}

// ClampThinkingLevel is pi-ai's clampThinkingLevel: the requested level if supported, else the
// nearest supported one (higher first, then lower).
func ClampThinkingLevel(m Model, level string) string {
	available := SupportedThinkingLevels(m)
	has := func(l string) bool {
		for _, a := range available {
			if a == l {
				return true
			}
		}
		return false
	}
	if has(level) {
		return level
	}
	index := -1
	for i, l := range extendedThinkingLevels {
		if l == level {
			index = i
		}
	}
	if index == -1 {
		if len(available) > 0 {
			return available[0]
		}
		return "off"
	}
	for i := index; i < len(extendedThinkingLevels); i++ {
		if has(extendedThinkingLevels[i]) {
			return extendedThinkingLevels[i]
		}
	}
	for i := index - 1; i >= 0; i-- {
		if has(extendedThinkingLevels[i]) {
			return extendedThinkingLevels[i]
		}
	}
	if len(available) > 0 {
		return available[0]
	}
	return "off"
}

func thinkingConfigSchema(m Model) *ahptypes.ConfigSchema {
	levels := SupportedThinkingLevels(m)
	// A model with only "off" has nothing to configure; omitting the schema keeps the client from
	// rendering a pointless single-option picker.
	if len(levels) <= 1 {
		return nil
	}
	def := levels[0]
	for _, l := range levels {
		if l == "medium" {
			def = "medium"
		}
	}
	enum := make([]json.RawMessage, len(levels))
	labels := make([]string, len(levels))
	for i, l := range levels {
		enum[i], _ = json.Marshal(l)
		labels[i] = strings.ToUpper(l[:1]) + l[1:]
	}
	defRaw := json.RawMessage(mustJSON(def))
	description := "How much reasoning effort the model spends before answering."
	return &ahptypes.ConfigSchema{Type: "object", Properties: map[string]ahptypes.ConfigPropertySchema{
		ThinkingConfigKey: {Type: "string", Title: "Thinking", Description: &description, Default: &defRaw, Enum: enum, EnumLabels: labels},
	}}
}

func mustJSON(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return raw
}

// ToSessionModelInfo describes a Pi model to a client.
func ToSessionModelInfo(m Model) ahptypes.SessionModelInfo {
	vision := false
	for _, in := range m.Input {
		vision = vision || in == "image"
	}
	ctx, out := m.ContextWindow, m.MaxTokens
	info := ahptypes.SessionModelInfo{
		Id: m.SelectionID(), Provider: Provider, Name: m.Name,
		MaxContextWindow: &ctx, MaxOutputTokens: &out, SupportsVision: &vision,
		ConfigSchema: thinkingConfigSchema(m),
		// _meta is the protocol's documented place for provider-specific extras; clients may
		// surface pricing but must not depend on it.
		Meta: map[string]json.RawMessage{"piProvider": mustJSON(m.Provider), "api": mustJSON(m.API)},
	}
	if len(m.Cost) > 0 {
		info.Meta["pricing"] = m.Cost
	}
	return info
}

// BuildAgentInfo describes this host's one agent. No capabilities are declared: one chat, one
// working directory; their absence is what tells a client not to attempt those calls.
func BuildAgentInfo(models []Model) ahptypes.AgentInfo {
	infos := make([]ahptypes.SessionModelInfo, 0, len(models))
	for _, m := range models {
		infos = append(infos, ToSessionModelInfo(m))
	}
	return ahptypes.AgentInfo{
		Provider: Provider, DisplayName: "pi", Description: "pi coding agent", Models: infos,
		ProtectedResources: []ahptypes.ProtectedResourceMetadata{},
	}
}
