package pi_permission_system

// Port of src/tool-input/tool-access-extractor-registry.ts: the registry of custom tool access-intent extractors, which lets a
// sibling extension declare the filesystem path a tool will access when its input shape is not the default `input.path`
// convention. One extractor per tool name; a duplicate registration fails (the original throws). The extractor, origin and lookup
// types live with the path layer (toolinputpath.go).

// ToolAccessExtractorRegistrar is the registration side of the extractor registry, mirroring the read-only
// ToolAccessExtractorLookup.
type ToolAccessExtractorRegistrar interface {
	Register(toolName string, extractor ToolAccessExtractor) (func(), error)
}

// ToolAccessExtractorRegistry is the persistent registry mapping tool names to custom access-intent extractors.
type ToolAccessExtractorRegistry struct {
	extractors map[string]ToolAccessExtractor
}

// Register registers an extractor for toolName. It fails if one is already registered for that name. The returned disposer
// removes the extractor; it is identity-guarded so a stale call cannot evict a later registration.
func (r *ToolAccessExtractorRegistry) Register(toolName string, e ToolAccessExtractor) (func(), error) {
	if r.extractors == nil {
		r.extractors = map[string]ToolAccessExtractor{}
	}
	if _, dup := r.extractors[toolName]; dup {
		return nil, errAlreadyRegistered("tool access extractor", toolName)
	}
	r.extractors[toolName] = e
	return func() {
		if r.extractors[toolName] == e {
			delete(r.extractors, toolName)
		}
	}, nil
}

// Resolve answers a local registration, or nil.
func (r *ToolAccessExtractorRegistry) Resolve(toolName string) *ResolvedToolAccessExtractor {
	if e, ok := r.extractors[toolName]; ok && e != nil {
		return &ResolvedToolAccessExtractor{Extractor: e, Origin: RegistrationLocal}
	}
	return nil
}
