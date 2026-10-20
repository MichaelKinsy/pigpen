package pi_permission_system

// Remaining lane-local stand-ins: the tool-input registries and the service surface (owner: the presentation and service lanes).

// ── types.ts ───────────────────────────────────────────────────────────────

// ── tool-input registries and service.ts ───────────────────────────────────

// ToolInputFormatter is a tool's preview formatter (tool-input-formatter-registry.ts), compared by identity like an extractor.
type ToolInputFormatter interface {
	Format(input *jsObject) (string, bool)
}

// ToolInputFormatterLookup is the read side of the formatter registry.
type ToolInputFormatterLookup interface {
	Get(toolName string) ToolInputFormatter
}

// ToolAccessExtractorRegistry is the node-local extractor registry.
type ToolAccessExtractorRegistry struct {
	extractors map[string]ToolAccessExtractor
}

// Register is register: one extractor per tool; the returned function unregisters it if it is still the registered one.
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

// Resolve answers a local registration.
func (r *ToolAccessExtractorRegistry) Resolve(toolName string) *ResolvedToolAccessExtractor {
	if e, ok := r.extractors[toolName]; ok && e != nil {
		return &ResolvedToolAccessExtractor{Extractor: e, Origin: RegistrationLocal}
	}
	return nil
}

// ToolInputFormatterRegistry is the node-local formatter registry.
type ToolInputFormatterRegistry struct{ formatters map[string]ToolInputFormatter }

// Register is register: one formatter per tool.
func (r *ToolInputFormatterRegistry) Register(toolName string, f ToolInputFormatter) (func(), error) {
	if r.formatters == nil {
		r.formatters = map[string]ToolInputFormatter{}
	}
	if _, dup := r.formatters[toolName]; dup {
		return nil, errAlreadyRegistered("tool input formatter", toolName)
	}
	r.formatters[toolName] = f
	return func() {
		if r.formatters[toolName] == f {
			delete(r.formatters, toolName)
		}
	}, nil
}

// Get answers the registered formatter, or nil.
func (r *ToolInputFormatterRegistry) Get(toolName string) ToolInputFormatter {
	return r.formatters[toolName]
}

type registrationError struct{ what, tool string }

func (e registrationError) Error() string {
	return "A " + e.what + " is already registered for '" + e.tool + "'."
}

func errAlreadyRegistered(what, tool string) error { return registrationError{what, tool} }

// PermissionsService is the part of service.ts PermissionsService the cross-node lookups read.
type PermissionsService interface {
	GetToolAccessExtractor(toolName string) ToolAccessExtractor
	GetToolInputFormatter(toolName string) ToolInputFormatter
}
