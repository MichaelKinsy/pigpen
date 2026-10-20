package websearch

import "strings"

// Port of summary-model-scope.ts: how a configured provider/model pair is resolved against the
// host's model registry and its enabled-model scope.

// modelLookup is the part of the registry that model resolution reads.
type modelLookup interface {
	Find(provider, id string) map[string]any
	GetAvailable() ([]map[string]any, error)
}

// modelIs reports whether model has the exact provider and id.
func modelIs(model map[string]any, provider, id string) bool {
	p, _ := model["provider"].(string)
	i, _ := model["id"].(string)
	return p == provider && i == id
}

// FindModelWithProviderRouting resolves a model through its native provider or a provider that
// routes that model id (summary-model-scope.ts:19-35). The direct registry fallback preserves
// explicit resolution when the availability snapshot lacks the configured model. When several
// routers expose the same id, the available-model order picks the route. It returns nil when
// nothing matches.
func FindModelWithProviderRouting(registry modelLookup, provider, id string) (map[string]any, error) {
	available, err := registry.GetAvailable()
	if err != nil {
		return nil, err
	}
	for _, model := range available {
		if modelIs(model, provider, id) {
			return model, nil
		}
	}
	routedID := provider + "/" + id
	for _, model := range available {
		if mid, _ := model["id"].(string); mid == routedID {
			return model, nil
		}
	}
	return registry.Find(provider, id), nil
}

// IsModelInScope applies Pi's enabled-model scope (summary-model-scope.ts:42-44): the host
// resolves `--models` and `enabledModels` into ctx.scopedModels, and an empty scope allows every
// available model.
func IsModelInScope(model map[string]any, scoped []map[string]any) bool {
	if len(scoped) == 0 {
		return true
	}
	provider, _ := model["provider"].(string)
	id, _ := model["id"].(string)
	for _, entry := range scoped {
		if modelIs(entry, provider, id) {
			return true
		}
	}
	return false
}

var thinkingLevels = map[string]bool{"off": true, "minimal": true, "low": true, "medium": true, "high": true, "xhigh": true, "max": true}

// SplitThinkingSuffix strips a trailing `:level` thinking suffix (summary-model-scope.ts:46-53).
// Any other colon stays in the value, so ids such as `nvidia/nemotron:free` survive. The level is
// "" when there is none.
func SplitThinkingSuffix(value string) (rest, level string) {
	index := strings.LastIndex(value, ":")
	if index < 0 {
		return value, ""
	}
	if suffix := value[index+1:]; thinkingLevels[suffix] {
		return value[:index], suffix
	}
	return value, ""
}
