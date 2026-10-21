package tintinweb_subagents

import (
	"regexp"
	"sort"
	"strings"
)

// Model resolution for a workflow's agent({ model }): an exact "provider/modelId" match, then a fuzzy match against
// the available models. upstream: src/model-resolver.ts resolveModel. (The settings lane ports the same file for the
// Agent tool; the names here carry a wf prefix so the two can be merged into one.)

// workflowModel is one model of the registry.
type workflowModel struct{ Provider, ID, Name string }

// workflowModelRegistry is the slice of the model registry resolution reads. upstream: ModelRegistry.
type workflowModelRegistry interface {
	// Find is the model registered under provider and id, or nil.
	Find(provider, id string) *workflowModel
	GetAll() []*workflowModel
	// GetAvailable is the models with auth configured; nil when the registry cannot tell (GetAll is used).
	GetAvailable() []*workflowModel
}

var wfDateStamp = regexp.MustCompile(`^\d{8}$`)
var wfPartSplit = regexp.MustCompile(`[\s\-/]+`)

// wfResolveModel resolves a model string: the found model, or the error text ("Model not found: ...").
func wfResolveModel(input string, registry workflowModelRegistry) (*workflowModel, string) {
	all := registry.GetAvailable()
	if all == nil {
		all = registry.GetAll()
	}
	available := map[string]bool{}
	for _, m := range all {
		available[strings.ToLower(m.Provider+"/"+m.ID)] = true
	}
	slash := strings.Index(input, "/")
	// 1. Exact match: "provider/modelId", only if available (has auth).
	if slash != -1 {
		if available[strings.ToLower(input)] {
			if found := registry.Find(input[:slash], input[slash+1:]); found != nil {
				return found, ""
			}
		}
	}
	// 2. Fuzzy match. Separators are normalized so "claude-haiku-4.5" and "claude-haiku-4-5" are one model.
	normalize := func(s string) string { return strings.ReplaceAll(strings.ToLower(s), ".", "-") }
	query := normalize(input)
	var best *workflowModel
	bestScore := 0.0
	for _, m := range all {
		id, name, full := normalize(m.ID), normalize(m.Name), normalize(m.Provider+"/"+m.ID)
		score := 0.0
		switch {
		case id == query || full == query:
			score = 100 // exact
		case strings.Contains(id, query) || strings.Contains(full, query):
			score = 60 + float64(wfUTF16Len(query))/float64(wfUTF16Len(id))*30 // substring, prefer tighter matches
		case strings.Contains(name, query):
			score = 40 + float64(wfUTF16Len(query))/float64(wfUTF16Len(name))*20
		default:
			// A trailing date-stamp token is optional, so a date-pinned config still matches an undated registry id.
			every := true
			for _, part := range wfPartSplit.Split(query, -1) {
				if !(wfDateStamp.MatchString(part) || strings.Contains(id, part) || strings.Contains(name, part) || strings.Contains(strings.ToLower(m.Provider), part)) {
					every = false
					break
				}
			}
			if every {
				score = 20
			}
		}
		if score > bestScore {
			bestScore, best = score, m
		}
	}
	if best != nil && bestScore >= 20 {
		if found := registry.Find(best.Provider, best.ID); found != nil {
			return found, ""
		}
	}
	// 3. Provider fallback: a "provider/modelId" query that did not match under the named provider retries against all.
	if slash != -1 {
		if bare, errText := wfResolveModel(input[slash+1:], registry); errText == "" {
			return bare, ""
		}
	}
	// 4. No match: list the available models.
	list := make([]string, len(all))
	for i, m := range all {
		list[i] = "  " + m.Provider + "/" + m.ID
	}
	sort.Strings(list)
	return nil, "Model not found: \"" + input + "\".\n\nAvailable models:\n" + strings.Join(list, "\n")
}

// wfDescribeModel is a model's two display forms: the short one for tight rows and the canonical one.
// upstream: model-resolver.ts describeModel.
func wfDescribeModel(m workflowModel) (modelName, modelID string) {
	name := m.Name
	if name == "" {
		name = m.ID
	}
	return strings.ToLower(regexp.MustCompile(`(?i)^Claude\s+`).ReplaceAllString(name, "")), m.Provider + "/" + m.ID
}

// workflowScopeInput and workflowScopeVerdict are the seam of the scopeModels policy (model-scope.ts checkModelScope),
// which the settings lane ports: the host asks, and acts on the verdict.
type workflowScopeInput struct {
	// Model is the model the spawn would run on (nil: the child inherits and none is known).
	Model *workflowModel
	// CallerSupplied: the SCRIPT named the model (not an agent file's model:), so it is refused rather than warned about.
	CallerSupplied bool
	AgentLabel     string
	ModelInput     string
}

// workflowScopeVerdict.Kind is "ok", "warn" or "error".
type workflowScopeVerdict struct{ Kind, Message string }
