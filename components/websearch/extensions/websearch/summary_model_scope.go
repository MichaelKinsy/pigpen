package websearch

// Port of summary-model-scope.ts. Stub: nothing resolves yet.

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

func FindModelWithProviderRouting(_ modelLookup, _, _ string) (map[string]any, error) {
	return nil, nil
}

func IsModelInScope(_ map[string]any, _ []map[string]any) bool { return false }

func SplitThinkingSuffix(value string) (rest, level string) { return value, "" }
