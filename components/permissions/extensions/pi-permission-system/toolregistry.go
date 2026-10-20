package pi_permission_system

import "sort"

// Port of src/exposure/tool-registry.ts: reading Pi's tool registry.

// ToolRegistry is the narrow interface for the Pi tool API subset used by handler classes.
type ToolRegistry interface {
	// GetAll is every registered tool (pi.getAllTools(), ToolInfo records); kept defensively wide.
	GetAll() []any
	// GetActive is the currently active tool names (pi.getActiveTools()).
	GetActive() []string
	SetActive(names []string)
}

// RegisteredTools is the tool names and their guideline bullets, read from one pass over a registry.
type RegisteredTools struct {
	// Names are the resolvable tool names, in registry order.
	Names []string
	// GuidelinesByTool are the guideline bullets per tool; a tool declaring none has no entry.
	GuidelinesByTool map[string][]string
}

// ReadRegisteredTools reads a registry listing once, yielding both answers this package needs. getActive() returns bare names and
// getAll() returns ToolInfo records, so both are accepted: a listing carrying no guidelines simply produces an empty map.
func ReadRegisteredTools(tools []any) RegisteredTools {
	registered := RegisteredTools{Names: []string{}, GuidelinesByTool: map[string][]string{}}
	for _, tool := range tools {
		name, ok := GetToolNameFromValue(tool)
		if !ok {
			continue
		}
		registered.Names = append(registered.Names, name)
		if guidelines := GetToolPromptGuidelinesFromValue(tool); len(guidelines) > 0 {
			registered.GuidelinesByTool[name] = guidelines
		}
	}
	return registered
}

// ToolRegistrationCheckResult is the outcome of checking a requested tool name against the registered tools. Status is
// "missing-tool-name", "registered" or "unregistered"; the names are set for the latter two, and AvailableToolNames for
// "unregistered" alone.
type ToolRegistrationCheckResult struct {
	Status             string
	RequestedToolName  string
	NormalizedToolName string
	AvailableToolNames []string
}

// normalizeToolName is `aliases[toolName] || toolName`. Only an own alias counts: the original's plain-object lookup also
// answers the properties every object inherits (constructor, toString, ...), which is not a name, and none of them can make a tool
// registered that is not.
func normalizeToolName(toolName string, aliases map[string]string) string {
	if canonical := aliases[toolName]; canonical != "" {
		return canonical
	}
	return toolName
}

func buildReverseAliases(aliases map[string]string) map[string][]string {
	reverse := map[string][]string{}
	for _, alias := range sortedAliasKeys(aliases) {
		canonical := aliases[alias]
		existing := reverse[canonical]
		if !inStrings(existing, alias) {
			existing = append(existing, alias)
		}
		reverse[canonical] = existing
	}
	return reverse
}

// sortedAliasKeys is Object.entries order for the alias map; only the order of aliases inside one canonical list depends on it, and
// that list is read as a set.
func sortedAliasKeys(aliases map[string]string) []string {
	keys := make([]string, 0, len(aliases))
	for key := range aliases {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func addToolNameVariants(value string, names map[string]bool, aliases map[string]string, reverseAliases map[string][]string) {
	names[value] = true

	normalized := normalizeToolName(value, aliases)
	names[normalized] = true

	if canonicalFromAlias := aliases[value]; canonicalFromAlias != "" {
		names[canonicalFromAlias] = true
	}
	for _, alias := range reverseAliases[value] {
		names[alias] = true
	}
	for _, alias := range reverseAliases[normalized] {
		names[alias] = true
	}
}

// GetToolNameFromValue is the name of a tool value: a non-empty string itself, else the first non-empty string of its toolName,
// name or tool field.
func GetToolNameFromValue(value any) (string, bool) {
	if direct, ok := GetNonEmptyString(value); ok {
		return direct, true
	}
	record := ToRecord(value)
	for _, key := range []string{"toolName", "name", "tool"} {
		if name, ok := recordString(record, key); ok {
			return name, true
		}
	}
	return "", false
}

// GetToolPromptGuidelinesFromValue is the guideline bullets a registered tool contributes to the system prompt. Pi carries them per
// tool on ToolInfo.promptGuidelines and flattens them into one `Guidelines:` block when it builds the prompt. Reading them per tool
// is what lets this package rebuild that block for the allowed set alone. Kept defensively wide: anything that is not a non-empty
// string is dropped, and a value that is not an array of them yields no guidelines.
func GetToolPromptGuidelinesFromValue(value any) []string {
	guidelines, ok := ToRecord(value).vals["promptGuidelines"].([]any)
	if !ok {
		return []string{}
	}
	bullets := []string{}
	for _, entry := range guidelines {
		if bullet, ok := GetNonEmptyString(entry); ok {
			bullets = append(bullets, bullet)
		}
	}
	return bullets
}

// CheckRequestedToolRegistration checks a requested tool name against the registered tools, through the aliases (nil is none). A
// nil requestedToolName is the original's null.
func CheckRequestedToolRegistration(requestedToolName *string, registeredTools []any, aliases map[string]string) ToolRegistrationCheckResult {
	var requestedRaw any
	if requestedToolName != nil {
		requestedRaw = *requestedToolName
	}
	requested, ok := GetNonEmptyString(requestedRaw)
	if !ok {
		return ToolRegistrationCheckResult{Status: "missing-tool-name"}
	}

	normalizedToolName := normalizeToolName(requested, aliases)
	reverseAliases := buildReverseAliases(aliases)

	registeredLookup := map[string]bool{}
	availableSeen := map[string]bool{}
	available := []string{}
	for _, tool := range registeredTools {
		name, ok := GetToolNameFromValue(tool)
		if !ok {
			continue
		}
		if !availableSeen[name] {
			availableSeen[name] = true
			available = append(available, name)
		}
		addToolNameVariants(name, registeredLookup, aliases, reverseAliases)
	}

	if registeredLookup[requested] || registeredLookup[normalizedToolName] {
		return ToolRegistrationCheckResult{Status: "registered", RequestedToolName: requested, NormalizedToolName: normalizedToolName}
	}

	sort.SliceStable(available, func(i, j int) bool { return icuCompare(available[i], available[j]) < 0 })
	return ToolRegistrationCheckResult{
		Status: "unregistered", RequestedToolName: requested, NormalizedToolName: normalizedToolName, AvailableToolNames: available,
	}
}
