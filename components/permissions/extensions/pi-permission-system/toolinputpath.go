package pi_permission_system

// Port of src/access-intent/tool-input-path.ts.

// GetPathBearingToolPath is input.path of a built-in path-bearing tool, ok false for any other tool or a missing path.
func GetPathBearingToolPath(toolName string, input any) (string, bool) {
	if ClassifyToolKind(toolName) != ToolKindPath {
		return "", false
	}
	return recordString(ToRecord(input), "path")
}

// ToolPathSource is what supplied a tool call's path.
type ToolPathSource string

const (
	// ToolPathConvention covers the built-in input.path / input.arguments.path shapes and the default for an unregistered tool.
	ToolPathConvention         ToolPathSource = "convention"
	ToolPathLocalExtractor     ToolPathSource = "local_extractor"
	ToolPathInheritedExtractor ToolPathSource = "inherited_extractor"
)

// ToolInputPathResult is a tool call's path together with what supplied it. HasPath false is the original's null path.
type ToolInputPathResult struct {
	Path    string
	HasPath bool
	Source  ToolPathSource
}

// ToolAccessExtractor returns the filesystem path a tool will access, ok false to decline (undefined).
type ToolAccessExtractor func(input *jsObject) (string, bool)

// RegistrationOrigin is which node's registration answered a lookup: "local" or "inherited".
type RegistrationOrigin string

const (
	RegistrationLocal     RegistrationOrigin = "local"
	RegistrationInherited RegistrationOrigin = "inherited"
)

// ResolvedToolAccessExtractor is an extractor together with the node whose registry supplied it.
type ResolvedToolAccessExtractor struct {
	Extractor ToolAccessExtractor
	Origin    RegistrationOrigin
}

// ToolAccessExtractorLookup is the read-only lookup the gate pipeline uses; nil result is undefined.
type ToolAccessExtractorLookup interface {
	Resolve(toolName string) *ResolvedToolAccessExtractor
}

// GetToolInputPath extracts the filesystem path a tool will access, for the cross-cutting path and external_directory gates.
// extractors may be nil.
func GetToolInputPath(toolName string, input any, extractors ToolAccessExtractorLookup) ToolInputPathResult {
	record := ToRecord(input)
	byConvention := func(p string, ok bool) ToolInputPathResult {
		return ToolInputPathResult{Path: p, HasPath: ok, Source: ToolPathConvention}
	}
	switch ClassifyToolKind(toolName) {
	case ToolKindBash:
		return byConvention("", false)
	case ToolKindPath:
		return byConvention(recordString(record, "path"))
	case ToolKindMcp:
		args, _ := record.get("arguments")
		return byConvention(recordString(ToRecord(args), "path"))
	}
	// skill, mcp-tool, extension
	if extractors != nil {
		if custom := extractors.Resolve(toolName); custom != nil {
			source := ToolPathLocalExtractor
			if custom.Origin == RegistrationInherited {
				source = ToolPathInheritedExtractor
			}
			p, ok := custom.Extractor(record)
			if ok {
				p, ok = GetNonEmptyString(p)
			}
			return ToolInputPathResult{Path: p, HasPath: ok, Source: source}
		}
	}
	return byConvention(recordString(record, "path"))
}
