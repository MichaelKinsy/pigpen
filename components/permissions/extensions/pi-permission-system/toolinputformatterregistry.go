package pi_permission_system

// Port of src/tool-input/tool-input-formatter-registry.ts: the registry of custom tool-input preview formatters. One formatter per
// tool name; a duplicate registration fails (the original throws).

// ToolInputFormatter is a custom preview formatter for one tool's input; ok false is the original's `undefined` (decline). An
// implementation is a pointer (NewToolInputFormatter), so two registrations compare by identity as the original's functions do.
type ToolInputFormatter interface {
	Format(input *jsObject) (string, bool)
}

type funcToolInputFormatter struct {
	fn func(input *jsObject) (string, bool)
}

func (f *funcToolInputFormatter) Format(input *jsObject) (string, bool) { return f.fn(input) }

// NewToolInputFormatter wraps a function as a formatter with its own identity.
func NewToolInputFormatter(fn func(input *jsObject) (string, bool)) ToolInputFormatter {
	return &funcToolInputFormatter{fn: fn}
}

// ToolInputFormatterLookup is the read-only lookup the ToolPreviewFormatter uses: it exposes only the read side, not the
// registration surface. Get answers nil for no formatter.
type ToolInputFormatterLookup interface {
	Get(toolName string) ToolInputFormatter
}

// ToolInputFormatterRegistrar is the registration side of the formatter registry.
type ToolInputFormatterRegistrar interface {
	Register(toolName string, formatter ToolInputFormatter) (func(), error)
}

// ToolInputFormatterRegistry is the persistent registry mapping tool names to custom preview formatters.
type ToolInputFormatterRegistry struct{ formatters map[string]ToolInputFormatter }

// Register registers a formatter for toolName. It fails if one is already registered for that name, which keeps resolution
// deterministic. The returned disposer removes the formatter; it is identity-guarded, so a stale call cannot evict a later
// registration.
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
