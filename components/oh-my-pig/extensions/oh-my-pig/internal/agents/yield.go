package agents

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// upstream: packages/coding-agent/src/tools/yield.ts,
// packages/tui/src/tools/task-yield-assembly.ts,
// packages/coding-agent/src/task/workpool-yield.ts

const (
	SubagentWarningSchemaOverridden = "SYSTEM WARNING: Subagent exhausted schema-retry budget; result was accepted despite failing the output schema."
	SubagentWarningNullYield        = "SYSTEM WARNING: Subagent called yield with null data."
	SubagentWarningMissingYield     = "SYSTEM WARNING: Subagent exited without calling yield tool after 3 reminders."
	SubagentWarningEmptyMessage     = "SYSTEM WARNING: Subagent ended without a final message."
)

// WorkPoolYieldItem identifies one item in a workpool worker's batch.
// upstream: packages/coding-agent/src/task/workpool-yield.ts:2
type WorkPoolYieldItem struct {
	ID    string
	Index int
}

// BuildWorkPoolOutputSchema creates the per-batch schema for workpool turns.
// upstream: packages/coding-agent/src/task/workpool-yield.ts:8
func BuildWorkPoolOutputSchema(items []WorkPoolYieldItem) map[string]any {
	props := make(map[string]any, len(items))
	req := make([]string, 0, len(items))
	for _, it := range items {
		props[it.ID] = map[string]any{}
		req = append(req, it.ID)
	}
	return map[string]any{
		"type":                 "object",
		"properties":           props,
		"required":             req,
		"additionalProperties": false,
	}
}

// UsesYield reports whether a subagent run uses the `yield` tool.
// upstream: packages/coding-agent/src/task/executor.ts:764
func UsesYield(outputSchema any) bool {
	return outputSchema != nil
}

// AssembledYieldResult is the folded output of a run's yield calls.
// upstream: packages/tui/src/tools/task-yield-assembly.ts:12
type AssembledYieldResult struct {
	Data             any
	SchemaOverridden bool
	MissingData      bool
}

func isIncrementalYieldType(t any) ([]string, bool) {
	switch tv := t.(type) {
	case []string:
		if len(tv) > 0 {
			return tv, true
		}
	case []any:
		if len(tv) == 0 {
			return nil, false
		}
		out := make([]string, 0, len(tv))
		for _, item := range tv {
			if s, ok := item.(string); ok {
				if trimmed := strings.TrimSpace(s); trimmed != "" {
					out = append(out, trimmed)
				}
			}
		}
		return out, true
	}
	return nil, false
}

// AssembleYieldResult folds terminal and incremental yield calls into one payload.
// sectionShapes maps top-level field names to "array" or "scalar".
// upstream: packages/tui/src/tools/task-yield-assembly.ts:66
func AssembleYieldResult(yieldItems []YieldItem, sectionShapes map[string]string) *AssembledYieldResult {
	if len(yieldItems) == 0 {
		return nil
	}

	var terminal *YieldItem
	for i := len(yieldItems) - 1; i >= 0; i-- {
		if _, inc := isIncrementalYieldType(yieldItems[i].Type); !inc {
			cp := yieldItems[i]
			terminal = &cp
			break
		}
	}

	sections := map[string]any{}
	sectionCounts := map[string]int{}
	overriddenScalars := map[string]bool{}
	schemaOverridden := false
	missingData := false
	hasSections := false

	for _, item := range yieldItems {
		if item.Status == "aborted" {
			continue
		}
		labels, inc := isIncrementalYieldType(item.Type)
		if !inc {
			continue
		}
		if !item.HasData || item.Data == nil {
			missingData = true
		}
		if len(labels) == 0 && item.SchemaOverridden {
			schemaOverridden = true
		}
		for _, label := range labels {
			shape := sectionShapes[label]
			count := sectionCounts[label]
			switch {
			case shape == "scalar":
				sections[label] = item.Data
				if item.SchemaOverridden {
					overriddenScalars[label] = true
				} else {
					delete(overriddenScalars, label)
				}
			case count == 0:
				if shape == "array" {
					sections[label] = []any{item.Data}
				} else {
					sections[label] = item.Data
				}
				if item.SchemaOverridden {
					schemaOverridden = true
				}
			default:
				if existing, ok := sections[label].([]any); ok {
					sections[label] = append(existing, item.Data)
				} else {
					sections[label] = []any{sections[label], item.Data}
				}
				if item.SchemaOverridden {
					schemaOverridden = true
				}
			}
			sectionCounts[label] = count + 1
			hasSections = true
		}
	}

	if terminal != nil && terminal.HasData {
		return &AssembledYieldResult{
			Data:             terminal.Data,
			SchemaOverridden: terminal.SchemaOverridden,
			MissingData:      terminal.Data == nil,
		}
	}

	if hasSections {
		return &AssembledYieldResult{
			Data:             sections,
			SchemaOverridden: schemaOverridden || len(overriddenScalars) > 0,
			MissingData:      missingData,
		}
	}

	if terminal == nil {
		return nil
	}
	return &AssembledYieldResult{
		Data:             terminal.Data,
		SchemaOverridden: terminal.SchemaOverridden,
		MissingData:      true,
	}
}

// ExtractSectionShapes inspects a JSON Schema or JTD output schema and reports
// which top-level properties are arrays vs scalars.
func ExtractSectionShapes(schema any) map[string]string {
	m, ok := schema.(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]string{}
	scanProps := func(raw any) {
		props, ok := raw.(map[string]any)
		if !ok {
			return
		}
		for k, v := range props {
			pm, ok := v.(map[string]any)
			if !ok {
				continue
			}
			if _, hasElements := pm["elements"]; hasElements {
				out[k] = "array"
			} else if t, _ := pm["type"].(string); t == "array" {
				out[k] = "array"
			} else {
				out[k] = "scalar"
			}
		}
	}
	scanProps(m["properties"])
	scanProps(m["optionalProperties"])
	if len(out) == 0 {
		return nil
	}
	return out
}

// ValidateOutputAgainstSchema validates value against simple JTD / JSON Schema
// object required-fields and top-level type constraints.
func ValidateOutputAgainstSchema(schema any, value any) (missing []string, errMsg string) {
	m, ok := schema.(map[string]any)
	if !ok {
		return nil, ""
	}
	// JTD form: `properties` keys are required; `optionalProperties` keys are optional.
	// JSON Schema form: `required` array lists required keys when `type: "object"`.
	requiredKeys := map[string]bool{}
	if reqList, ok := m["required"].([]any); ok {
		for _, item := range reqList {
			if s, ok := item.(string); ok {
				requiredKeys[s] = true
			}
		}
	} else if reqList, ok := m["required"].([]string); ok {
		for _, s := range reqList {
			requiredKeys[s] = true
		}
	} else if _, hasOpt := m["optionalProperties"]; hasOpt || m["type"] == nil {
		if props, ok := m["properties"].(map[string]any); ok {
			for k := range props {
				requiredKeys[k] = true
			}
		}
	}

	if len(requiredKeys) > 0 || m["type"] == "object" {
		obj, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Sprintf("expected object, got %T", value)
		}
		for k := range requiredKeys {
			if _, present := obj[k]; !present {
				missing = append(missing, k)
			}
		}
		if len(missing) > 0 {
			return missing, "missing required fields: " + strings.Join(missing, ", ")
		}
	}
	return nil, ""
}

// yieldToolDescription renders prompts/tools/yield.md for either normal or workpool mode.
func yieldToolDescription(hasOutputSchema, workPool bool) string {
	if workPool {
		return strings.TrimSpace(`Submit ONE workpool item at a time as ` + "`{ key, data }`" + ` or ` + "`{ key, error }`" + `:
- ` + "`key`" + `: its 1-based number.
- ` + "`data`" + `: the self-contained outcome/evidence value.
- ` + "`error`" + `: a failure reason.

The result tells you which keys remain. The final key ends the turn automatically.
NEVER submit multiple items together.`)
	}
	base := `Submit subagent output: ` + "`{ data: <your output> }`" + ` for success, ` + "`{ error: \"message\" }`" + ` for failure.
Never both; never a bare payload outside ` + "`data`" + `.

Omit ` + "`type`" + ` for the usual single terminal result.
Pass ` + "`type: [\"<field>\"]`" + ` with ` + "`data`" + ` to submit an incremental, non-terminal section that accumulates.
A data-less ` + "`type: \"result\"`" + ` finalizes previously submitted sections; it is invalid when none were submitted.`
	if hasOutputSchema {
		base += "\nThe terminal `data` MUST be the full object matching the output schema."
	}
	return base
}

var placeholderYieldErrors = map[string]bool{
	"ignore":         true,
	"ignored":        true,
	"n/a":            true,
	"none":           true,
	"not applicable": true,
	"null":           true,
	"placeholder":    true,
	"undefined":      true,
	"unused":         true,
}

// nonStructuralStrictSchemaKeys mirrors omp's `NON_STRUCTURAL_SCHEMA_KEYS`
// stripped during strict-mode sanitization (`packages/ai/src/utils/schema/fields.ts:98-140`).
var nonStructuralStrictSchemaKeys = map[string]bool{
	"format":                true,
	"pattern":               true,
	"minLength":             true,
	"maxLength":             true,
	"minimum":               true,
	"maximum":               true,
	"exclusiveMinimum":      true,
	"exclusiveMaximum":      true,
	"minItems":              true,
	"maxItems":              true,
	"uniqueItems":           true,
	"multipleOf":            true,
	"$schema":               true,
	"examples":              true,
	"default":               true,
	"title":                 true,
	"$comment":              true,
	"if":                    true,
	"then":                  true,
	"else":                  true,
	"not":                   true,
	"unevaluatedProperties": true,
	"unevaluatedItems":      true,
	"patternProperties":     true,
	"minProperties":         true,
	"maxProperties":         true,
	"propertyNames":         true,
	"dependentRequired":     true,
	"dependentSchemas":      true,
	"dependencies":          true,
	"contentEncoding":       true,
	"contentMediaType":      true,
	"contentSchema":         true,
	"deprecated":            true,
	"readOnly":              true,
	"writeOnly":             true,
	"$id":                   true,
	"$anchor":               true,
}

var yieldJTDPrimitiveToJSONType = map[string]string{
	"boolean":   "boolean",
	"string":    "string",
	"timestamp": "string",
	"float32":   "number",
	"float64":   "number",
	"int8":      "integer",
	"uint8":     "integer",
	"int16":     "integer",
	"uint16":    "integer",
	"int32":     "integer",
	"uint32":    "integer",
}

var yieldJTDOnlyPrimitiveTypes = map[string]bool{
	"timestamp": true,
	"float32":   true,
	"float64":   true,
	"int8":      true,
	"uint8":     true,
	"int16":     true,
	"uint16":    true,
	"int32":     true,
	"uint32":    true,
}

var yieldJTDKeywords = map[string]bool{
	"definitions":          true,
	"metadata":             true,
	"nullable":             true,
	"ref":                  true,
	"type":                 true,
	"enum":                 true,
	"elements":             true,
	"properties":           true,
	"optionalProperties":   true,
	"additionalProperties": true,
	"values":               true,
	"discriminator":        true,
	"mapping":              true,
}

func isYieldJTDDocument(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	for k := range m {
		if !yieldJTDKeywords[k] {
			return false
		}
	}
	if rawT, hasT := m["type"]; hasT {
		s, ok := rawT.(string)
		if !ok {
			return false
		}
		if _, known := yieldJTDPrimitiveToJSONType[s]; !known {
			return false
		}
	}
	if rawEnum, hasE := m["enum"]; hasE {
		switch ev := rawEnum.(type) {
		case []any:
			for _, item := range ev {
				if _, ok := item.(string); !ok {
					return false
				}
			}
		case []string:
		default:
			return false
		}
	}
	if elems, has := m["elements"]; has && !isYieldJTDDocument(elems) {
		return false
	}
	if vals, has := m["values"]; has && !isYieldJTDDocument(vals) {
		return false
	}
	for _, mk := range []string{"properties", "optionalProperties", "definitions", "mapping"} {
		sub, has := m[mk]
		if !has {
			continue
		}
		sm, ok := sub.(map[string]any)
		if !ok {
			return false
		}
		for _, child := range sm {
			if !isYieldJTDDocument(child) {
				return false
			}
		}
	}
	return true
}

func isYieldJTDSchema(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	for _, k := range []string{"elements", "values", "optionalProperties", "discriminator", "ref"} {
		if _, has := m[k]; has {
			return true
		}
	}
	if t, ok := m["type"].(string); ok && yieldJTDOnlyPrimitiveTypes[t] {
		return true
	}
	if _, hasProps := m["properties"]; hasProps {
		if _, hasType := m["type"]; !hasType {
			return isYieldJTDDocument(m)
		}
	}
	return false
}

// convertYieldJTDToJSONSchema converts a JTD schema to JSON Schema, matching
// omp's `jtdToJsonSchema` (`tools/jtd-to-json-schema.ts:37-138,365-371`).
func convertYieldJTDToJSONSchema(schema any) any {
	m, ok := schema.(map[string]any)
	if !ok {
		return schema
	}
	if !isYieldJTDSchema(m) {
		return m
	}
	return convertYieldJTDNode(m)
}

func convertYieldJTDNode(schema any) map[string]any {
	m, ok := schema.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	var converted map[string]any
	switch {
	case m["enum"] != nil:
		converted = map[string]any{"enum": m["enum"]}
	case m["elements"] != nil:
		converted = map[string]any{"type": "array", "items": convertYieldJTDNode(m["elements"])}
	case m["type"] != nil:
		t, _ := m["type"].(string)
		if jt, known := yieldJTDPrimitiveToJSONType[t]; known {
			converted = map[string]any{"type": jt}
		} else {
			converted = map[string]any{"type": t}
		}
	case m["values"] != nil:
		converted = map[string]any{"type": "object", "additionalProperties": convertYieldJTDNode(m["values"])}
	case m["properties"] != nil || m["optionalProperties"] != nil:
		props := map[string]any{}
		var req []string
		if pm, ok := m["properties"].(map[string]any); ok {
			keys := make([]string, 0, len(pm))
			for k := range pm {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				props[k] = convertYieldJTDNode(pm[k])
				req = append(req, k)
			}
		}
		if om, ok := m["optionalProperties"].(map[string]any); ok {
			for k, v := range om {
				props[k] = convertYieldJTDNode(v)
			}
		}
		converted = map[string]any{
			"type":                 "object",
			"properties":           props,
			"additionalProperties": false,
		}
		if len(req) > 0 {
			converted["required"] = req
		}
	case m["ref"] != nil:
		if r, ok := m["ref"].(string); ok {
			converted = map[string]any{"$ref": "#/$defs/" + r}
		} else {
			converted = map[string]any{}
		}
	default:
		converted = map[string]any{}
	}
	if b, ok := m["nullable"].(bool); ok && b {
		return map[string]any{"anyOf": []any{converted, map[string]any{"type": "null"}}}
	}
	return converted
}

// yieldOrderedHintObject converts a JSON Schema map into an `*sdk.OrderedObject`
// that marshals standard JSON Schema keywords in omp's canonical authored order
// (`type`, `properties`, `items`, `required`, `additionalProperties`, `anyOf`, ...)
// rather than Go's alphabetical map sort.
func yieldOrderedHintObject(v any) any {
	switch tv := v.(type) {
	case map[string]any:
		if len(tv) == 0 {
			return sdk.NewOrderedObject()
		}
		preferred := []string{"type", "description", "properties", "items", "required", "additionalProperties", "enum", "anyOf", "oneOf", "allOf"}
		used := make(map[string]bool, len(tv))
		pairs := make([]any, 0, len(tv)*2)
		for _, k := range preferred {
			if val, ok := tv[k]; ok {
				used[k] = true
				if k == "properties" {
					if pm, ok := val.(map[string]any); ok {
						pairs = append(pairs, k, yieldOrderedPropsMap(pm))
						continue
					}
				}
				pairs = append(pairs, k, yieldOrderedHintObject(val))
			}
		}
		rest := make([]string, 0, len(tv))
		for k := range tv {
			if !used[k] {
				rest = append(rest, k)
			}
		}
		sort.Strings(rest)
		for _, k := range rest {
			pairs = append(pairs, k, yieldOrderedHintObject(tv[k]))
		}
		return sdk.NewOrderedObject(pairs...)
	case []any:
		out := make([]any, len(tv))
		for i, item := range tv {
			out[i] = yieldOrderedHintObject(item)
		}
		return out
	default:
		return v
	}
}

func yieldOrderedPropsMap(pm map[string]any) *sdk.OrderedObject {
	keys := make([]string, 0, len(pm))
	for k := range pm {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]any, 0, len(keys)*2)
	for _, k := range keys {
		pairs = append(pairs, k, yieldOrderedHintObject(pm[k]))
	}
	return sdk.NewOrderedObject(pairs...)
}

// formatYieldSchema matches omp's `formatSchema` in `tools/yield.ts:44-52`.
// When schema is a JSON string, omp parses it via `normalizeSchema` and passes
// the resulting object (or raw string if invalid) to `formatSchema`. Here we
// preserve JSON Schema keyword order (`type, properties, required, ...`) when
// formatting a Go map (which otherwise sorts alphabetically).
func formatYieldSchema(schema any) string {
	if schema == nil {
		return "No schema provided."
	}
	if s, ok := schema.(string); ok {
		return s
	}
	b, err := json.MarshalIndent(yieldOrderedHintObject(schema), "", "  ")
	if err != nil {
		return "[unserializable schema]"
	}
	return string(b)
}

// sanitizeYieldSchemaForStrictMode mirrors omp's `sanitizeSchemaForStrictMode`
// (`packages/ai/src/utils/schema/normalize.ts:2157-2394`) for `yield`'s `data`
// schema: strips non-structural keys and `additionalProperties`, recurses into
// `properties`/`items`/`anyOf`/`oneOf`/`allOf`, and writes `type` last.
func sanitizeYieldSchemaForStrictMode(schema map[string]any) *sdk.OrderedObject {
	pairs := make([]any, 0, len(schema)*2)
	keys := make([]string, 0, len(schema))
	for k := range schema {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var typeVal string
	hasType := false
	if t, ok := schema["type"].(string); ok {
		typeVal = t
		hasType = true
	}
	for _, k := range keys {
		if nonStructuralStrictSchemaKeys[k] || k == "type" || k == "const" || k == "nullable" || k == "additionalProperties" {
			continue
		}
		v := schema[k]
		switch k {
		case "properties":
			if pm, ok := v.(map[string]any); ok {
				pkeys := make([]string, 0, len(pm))
				for pk := range pm {
					pkeys = append(pkeys, pk)
				}
				sort.Strings(pkeys)
				ppairs := make([]any, 0, len(pkeys)*2)
				for _, pk := range pkeys {
					if child, ok := pm[pk].(map[string]any); ok {
						ppairs = append(ppairs, pk, sanitizeYieldSchemaForStrictMode(child))
					} else {
						ppairs = append(ppairs, pk, pm[pk])
					}
				}
				pairs = append(pairs, "properties", sdk.NewOrderedObject(ppairs...))
				continue
			}
		case "items":
			if child, ok := v.(map[string]any); ok {
				pairs = append(pairs, "items", sanitizeYieldSchemaForStrictMode(child))
				continue
			}
		case "anyOf", "oneOf", "allOf":
			if arr, ok := v.([]any); ok {
				out := make([]any, len(arr))
				for i, entry := range arr {
					if child, ok := entry.(map[string]any); ok {
						out[i] = sanitizeYieldSchemaForStrictMode(child)
					} else {
						out[i] = entry
					}
				}
				pairs = append(pairs, k, out)
				continue
			}
		}
		pairs = append(pairs, k, v)
	}
	if hasType {
		pairs = append(pairs, "type", typeVal)
	} else if _, hasProps := schema["properties"].(map[string]any); hasProps {
		pairs = append(pairs, "type", "object")
	} else if _, hasItems := schema["items"]; hasItems {
		pairs = append(pairs, "type", "array")
	}
	return sdk.NewOrderedObject(pairs...)
}

// withYieldSectionVariants mirrors omp's `withSectionVariants`
// (`tools/yield.ts:204-230`): for an object schema with `properties`, wraps
// the full object plus each unique property schema (and array `items` schema)
// in `{description, anyOf: [...]}` so incremental section yields validate.
func withYieldSectionVariants(fullObj *sdk.OrderedObject, description string) *sdk.OrderedObject {
	tVal, _ := fullObj.Get("type")
	if tVal != "object" {
		return cloneOrderedWithDescription(fullObj, description)
	}
	rawProps, hasProps := fullObj.Get("properties")
	if !hasProps {
		return cloneOrderedWithDescription(fullObj, description)
	}
	propObj, ok := rawProps.(*sdk.OrderedObject)
	if !ok || propObj == nil {
		return cloneOrderedWithDescription(fullObj, description)
	}
	branches := make([]any, 0, len(propObj.Keys())+1)
	seen := make(map[string]bool)
	add := func(s any) {
		if s == nil {
			return
		}
		b, err := json.Marshal(s)
		if err != nil {
			return
		}
		key := string(b)
		if seen[key] {
			return
		}
		seen[key] = true
		branches = append(branches, s)
	}
	add(fullObj)
	for _, name := range propObj.Keys() {
		prop, _ := propObj.Get(name)
		add(prop)
		if po, ok := prop.(*sdk.OrderedObject); ok && po != nil {
			if pt, _ := po.Get("type"); pt == "array" {
				if items, hasItems := po.Get("items"); hasItems {
					add(items)
				}
			}
		}
	}
	if len(branches) <= 1 {
		return cloneOrderedWithDescription(fullObj, description)
	}
	return sdk.NewOrderedObject("description", description, "anyOf", branches)
}

func cloneOrderedWithDescription(obj *sdk.OrderedObject, description string) *sdk.OrderedObject {
	pairs := make([]any, 0, len(obj.Keys())*2+2)
	for _, k := range obj.Keys() {
		if k == "description" {
			continue
		}
		v, _ := obj.Get(k)
		pairs = append(pairs, k, v)
	}
	pairs = append(pairs, "description", description)
	return sdk.NewOrderedObject(pairs...)
}

// dereferenceYieldSchema inlines local `#/$defs/<Name>` and `#/definitions/<Name>`
// pointers and strips `$defs`/`definitions` from the root, matching omp's
// `dereferenceJsonSchema` (`packages/ai/src/utils/schema/dereference.ts:123-131`).
func dereferenceYieldSchema(schema map[string]any) map[string]any {
	_, hasDefs := schema["$defs"]
	_, hasDefinitions := schema["definitions"]
	if !hasDefs && !hasDefinitions {
		return schema
	}
	var walk func(v any, visiting map[string]bool) any
	resolveRef := func(ref string) map[string]any {
		var defsKey, name string
		if strings.HasPrefix(ref, "#/$defs/") {
			defsKey = "$defs"
			name = strings.TrimPrefix(ref, "#/$defs/")
		} else if strings.HasPrefix(ref, "#/definitions/") {
			defsKey = "definitions"
			name = strings.TrimPrefix(ref, "#/definitions/")
		} else {
			return nil
		}
		if strings.Contains(name, "/") {
			return nil
		}
		defs, _ := schema[defsKey].(map[string]any)
		if defs == nil {
			return nil
		}
		target, _ := defs[name].(map[string]any)
		return target
	}
	walk = func(v any, visiting map[string]bool) any {
		switch tv := v.(type) {
		case map[string]any:
			if ref, ok := tv["$ref"].(string); ok {
				if visiting[ref] {
					return map[string]any{}
				}
				target := resolveRef(ref)
				if target == nil {
					return tv
				}
				visiting[ref] = true
				inlined, _ := walk(target, visiting).(map[string]any)
				delete(visiting, ref)
				if inlined == nil {
					return map[string]any{}
				}
				merged := make(map[string]any, len(inlined)+len(tv))
				for k, val := range inlined {
					merged[k] = val
				}
				for k, val := range tv {
					if k == "$ref" || k == "$defs" || k == "definitions" {
						continue
					}
					merged[k] = walk(val, visiting)
				}
				return merged
			}
			out := make(map[string]any, len(tv))
			for k, val := range tv {
				if k == "$defs" || k == "definitions" {
					continue
				}
				out[k] = walk(val, visiting)
			}
			return out
		case []any:
			out := make([]any, len(tv))
			for i, item := range tv {
				out[i] = walk(item, visiting)
			}
			return out
		default:
			return v
		}
	}
	res, _ := walk(schema, map[string]bool{}).(map[string]any)
	if res == nil {
		return schema
	}
	return res
}

// buildYieldDataSchema builds the `data` property schema for `yield`, matching
// omp's `YieldTool` constructor (`tools/yield.ts:363-415`):
//   - `nil` (no schema): loose object with "Structured JSON output (no schema specified)"
//   - `false`: loose object with invalid-schema description
//   - `true` (unconstrained): `{ description: "Structured output matching the schema:\ntrue" }`
//   - object/JTD/JSON-string schema: converts JTD to JSON Schema, dereferences
//     local `$ref`, sanitizes for strict mode, and adds `withSectionVariants`
//     (`anyOf: [fullObject, ...propertySchemas]`) alongside the formatted hint.
func buildYieldDataSchema(outputSchema any) any {
	if outputSchema == nil {
		return map[string]any{
			"type":                 "object",
			"additionalProperties": true,
			"description":          "Structured JSON output (no schema specified)",
		}
	}
	normalized := outputSchema
	if s, ok := outputSchema.(string); ok {
		var parsed any
		if err := json.Unmarshal([]byte(s), &parsed); err != nil {
			return map[string]any{
				"type":                 "object",
				"additionalProperties": true,
				"description":          "Structured JSON output (output schema invalid; accepting unconstrained object): " + err.Error(),
			}
		}
		normalized = parsed
	}
	if b, ok := normalized.(bool); ok {
		if !b {
			return map[string]any{
				"type":                 "object",
				"additionalProperties": true,
				"description":          "Structured JSON output (output schema invalid; accepting unconstrained object): boolean false schema rejects all outputs",
			}
		}
		return map[string]any{
			"description": "Structured output matching the schema:\ntrue",
		}
	}
	jsonSchema := convertYieldJTDToJSONSchema(normalized)
	hint := formatYieldSchema(jsonSchema)
	desc := "Structured output matching the schema:\n" + hint
	schemaMap, ok := jsonSchema.(map[string]any)
	if !ok {
		return map[string]any{
			"description": desc,
		}
	}
	derefed := dereferenceYieldSchema(schemaMap)
	sanitized := sanitizeYieldSchemaForStrictMode(derefed)
	return withYieldSectionVariants(sanitized, desc)
}

// assertSupportedYieldRootFields mirrors omp's `assertSupportedRootFields`
// (`tools/yield.ts:265-276`), rejecting unknown non-nil root fields that
// would otherwise be dropped silently.
func assertSupportedYieldRootFields(params sdk.Schema, args map[string]any) string {
	declared, _ := params["properties"].(map[string]any)
	var unsupported []string
	for k, v := range args {
		if v == nil {
			continue
		}
		if _, ok := declared[k]; !ok {
			unsupported = append(unsupported, fmt.Sprintf("%q", k))
		}
	}
	if len(unsupported) == 0 {
		return ""
	}
	sort.Strings(unsupported)
	var allowed []string
	for k := range declared {
		allowed = append(allowed, fmt.Sprintf("%q", k))
	}
	sort.Strings(allowed)
	return fmt.Sprintf(
		`yield arguments carry unsupported root field(s) %s; nothing was submitted. Allowed root fields: %s. Put the entire output inside `+"`data`"+`. Submit success as {"data":<your output>} or failure as {"error":"message"}.`,
		strings.Join(unsupported, ", "),
		strings.Join(allowed, ", "),
	)
}

// BuildYieldToolDefinition constructs the `yield` CustomTool passed into a structured
// child session or workpool worker.
// upstream: packages/coding-agent/src/tools/yield.ts:232-295,353-439
func BuildYieldToolDefinition(outputSchema any, workPoolItems []WorkPoolYieldItem, onYield func(item YieldItem, terminal bool)) sdk.ToolDefinition {
	isWorkPool := len(workPoolItems) > 0
	var params sdk.Schema
	if isWorkPool {
		keys := make([]int, len(workPoolItems))
		for i, it := range workPoolItems {
			keys[i] = it.Index
		}
		params = sdk.Schema{
			"type":                 "object",
			"additionalProperties": false,
			"description":          "submit one workpool item outcome",
			"properties": map[string]any{
				"key": map[string]any{
					"enum":        keys,
					"description": "1-based workpool item number",
				},
				"data":  map[string]any{"description": "Self-contained outcome and evidence for this item"},
				"error": map[string]any{"type": "string", "description": "Failure reason for this item"},
			},
			"required": []string{"key"},
		}
	} else {
		params = sdk.Schema{
			"type":                 "object",
			"additionalProperties": false,
			"description":          "submit data or error",
			"properties": map[string]any{
				"type": map[string]any{
					"anyOf": []any{
						map[string]any{"type": "string"},
						map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "string"}},
					},
					"description": "Optional result type. A non-empty string array labels an incremental section; a string is terminal and, without data, finalizes the submitted sections.",
				},
				"data":  buildYieldDataSchema(outputSchema),
				"error": map[string]any{"type": "string", "description": "Failure reason; mutually exclusive with data"},
			},
			"required": []string{},
		}
	}

	var mu sync.Mutex
	submittedKeys := map[int]bool{}
	hasSections := false

	return sdk.ToolDefinition{
		Name:        "yield",
		Label:       "Yield",
		Description: yieldToolDescription(outputSchema != nil, isWorkPool),
		Parameters:  params,
		Execute: func(_ sdk.Context, args map[string]any) (any, error) {
			mu.Lock()
			defer mu.Unlock()

			if msg := assertSupportedYieldRootFields(params, args); msg != "" {
				return sdk.ToolResult{Content: msg, IsError: true}, nil
			}

			if isWorkPool {
				rawKey, ok := args["key"]
				if !ok {
					return sdk.ToolResult{Content: "yield requires integer `key` (1-based item number).", IsError: true}, nil
				}
				var key int
				switch k := rawKey.(type) {
				case float64:
					key = int(k)
				case int:
					key = k
				default:
					return sdk.ToolResult{Content: "yield `key` must be a 1-based integer.", IsError: true}, nil
				}
				var matched *WorkPoolYieldItem
				for i := range workPoolItems {
					if workPoolItems[i].Index == key {
						matched = &workPoolItems[i]
						break
					}
				}
				if matched == nil {
					return sdk.ToolResult{Content: fmt.Sprintf("unknown workpool key %d.", key), IsError: true}, nil
				}
				if submittedKeys[key] {
					return sdk.ToolResult{Content: fmt.Sprintf("key %d already submitted.", key), IsError: true}, nil
				}
				dataVal, hasData := args["data"]
				errStr, _ := args["error"].(string)
				errStr = strings.TrimSpace(errStr)
				if hasData && dataVal != nil && errStr != "" && placeholderYieldErrors[strings.ToLower(errStr)] {
					errStr = ""
				}
				if hasData && dataVal != nil && errStr != "" {
					return sdk.ToolResult{Content: "Cannot submit both `data` and `error` for the same key.", IsError: true}, nil
				}
				if (!hasData || dataVal == nil) && errStr == "" {
					return sdk.ToolResult{Content: "Submit either `{ key, data }` or `{ key, error }`.", IsError: true}, nil
				}
				submittedKeys[key] = true
				var remaining []string
				for _, it := range workPoolItems {
					if !submittedKeys[it.Index] {
						remaining = append(remaining, itoa(int64(it.Index)))
					}
				}
				complete := len(remaining) == 0
				item := YieldItem{
					Data:     dataVal,
					HasData:  hasData && dataVal != nil,
					Status:   "success",
					Error:    errStr,
					Type:     []string{matched.ID},
					Complete: complete,
				}
				if errStr != "" {
					item.Data = map[string]any{"error": errStr}
					item.HasData = true
				}
				if onYield != nil {
					onYield(item, complete)
				}
				msg := fmt.Sprintf("Recorded item %d (%s).", key, matched.ID)
				if complete {
					msg += " All items complete."
				} else {
					msg += " Remaining keys: " + strings.Join(remaining, ", ")
				}
				return sdk.ToolResult{
					Content: msg,
					Details: map[string]any{
						"data":     item.Data,
						"status":   item.Status,
						"error":    item.Error,
						"type":     item.Type,
						"complete": complete,
					},
				}, nil
			}

			// Normal subagent yield.
			dataVal, hasData := args["data"]
			errStr, _ := args["error"].(string)
			errStr = strings.TrimSpace(errStr)
			if hasData && dataVal != nil && errStr != "" && placeholderYieldErrors[strings.ToLower(errStr)] {
				errStr = ""
			}
			if hasData && dataVal != nil && errStr != "" {
				return sdk.ToolResult{
					Content: `Submit success as {"data":<your output>} or failure as {"error":"message"}, never both.`,
					IsError: true,
				}, nil
			}
			rawType := args["type"]
			labels, isIncremental := isIncrementalYieldType(rawType)
			var typeVal any
			if isIncremental {
				typeVal = labels
			} else if s, ok := rawType.(string); ok && strings.TrimSpace(s) != "" {
				typeVal = strings.TrimSpace(s)
			}

			if errStr != "" {
				item := YieldItem{Status: "aborted", Error: errStr, Type: typeVal}
				if onYield != nil {
					onYield(item, true)
				}
				return sdk.ToolResult{
					Content: "Aborted result recorded.",
					Details: map[string]any{"status": "aborted", "error": errStr, "type": typeVal},
				}, nil
			}

			if isIncremental {
				if !hasData {
					return sdk.ToolResult{Content: "Incremental yield requires `data`.", IsError: true}, nil
				}
				hasSections = true
				item := YieldItem{Data: dataVal, HasData: true, Status: "success", Type: labels}
				if onYield != nil {
					onYield(item, false)
				}
				return sdk.ToolResult{
					Content: fmt.Sprintf("Section [%s] recorded.", strings.Join(labels, ", ")),
					Details: map[string]any{"data": dataVal, "status": "success", "type": labels},
				}, nil
			}

			if !hasData {
				if typeVal != nil && hasSections {
					item := YieldItem{Status: "success", Type: typeVal}
					if onYield != nil {
						onYield(item, true)
					}
					return sdk.ToolResult{
						Content: "Result finalized.",
						Details: map[string]any{"status": "success", "type": typeVal},
					}, nil
				}
				return sdk.ToolResult{
					Content: `Submit success as {"data":<your output>} or failure as {"error":"message"}.`,
					IsError: true,
				}, nil
			}

			item := YieldItem{Data: dataVal, HasData: true, Status: "success", Type: typeVal}
			if onYield != nil {
				onYield(item, true)
			}
			return sdk.ToolResult{
				Content: "Result submitted.",
				Details: map[string]any{"data": dataVal, "status": "success", "type": typeVal},
			}, nil
		},
	}
}

// FinalizeSubprocessOutputResult is the finalized text, exit code, and structured metadata
// for a subagent run.
// upstream: packages/coding-agent/src/task/executor.ts:744
type FinalizeSubprocessOutputResult struct {
	RawOutput        string
	ExitCode         int
	Stderr           string
	AbortedViaYield  bool
	HasYield         bool
	StructuredOutput *StructuredSubagentOutput
}

// FinalizeSubprocessOutput resolves yield payloads or final assistant prose into the
// subagent's settled output and exit status.
// upstream: packages/coding-agent/src/task/executor.ts:793
func FinalizeSubprocessOutput(
	rawOutput string,
	exitCode int,
	stderr string,
	doneAborted bool,
	signalAborted bool,
	yieldItems []YieldItem,
	outputSchema any,
	schemaMode string,
	schemaSource string,
) FinalizeSubprocessOutputResult {
	if schemaMode == "" {
		schemaMode = "permissive"
	}
	if schemaSource == "" {
		if outputSchema == nil {
			schemaSource = "none"
		} else {
			schemaSource = "session"
		}
	}
	includeStructured := schemaSource != "none"
	hasYield := len(yieldItems) > 0
	hadFailureBeforeYield := exitCode != 0 && strings.TrimSpace(stderr) != ""
	res := FinalizeSubprocessOutputResult{
		RawOutput: rawOutput,
		ExitCode:  exitCode,
		Stderr:    stderr,
		HasYield:  hasYield,
	}

	if hasYield {
		last := yieldItems[len(yieldItems)-1]
		if last.Status == "aborted" {
			res.AbortedViaYield = true
			res.ExitCode = 0
			if last.Error != "" {
				res.Stderr = last.Error
			} else {
				res.Stderr = "Subagent aborted task"
			}
			b, _ := json.MarshalIndent(map[string]any{"aborted": true, "error": last.Error}, "", "  ")
			res.RawOutput = string(b)
			return res
		}

		assembled := AssembleYieldResult(yieldItems, ExtractSectionShapes(outputSchema))
		if assembled == nil || assembled.MissingData {
			if rawOutput != "" {
				res.RawOutput = SubagentWarningNullYield + "\n\n" + rawOutput
			} else {
				res.RawOutput = SubagentWarningNullYield
			}
			if includeStructured {
				res.StructuredOutput = &StructuredSubagentOutput{
					Source: schemaSource,
					Mode:   schemaMode,
					Status: "invalid",
					Error:  SubagentWarningNullYield,
				}
				if schemaMode == "strict" {
					res.ExitCode = 1
					res.Stderr = "schema_violation: " + SubagentWarningNullYield
				}
			}
			return res
		}

		missing, valErr := ValidateOutputAgainstSchema(outputSchema, assembled.Data)
		if includeStructured {
			if valErr != "" {
				res.StructuredOutput = &StructuredSubagentOutput{
					Source:  schemaSource,
					Mode:    schemaMode,
					Status:  "invalid",
					Data:    assembled.Data,
					HasData: true,
					Error:   valErr,
				}
			} else {
				res.StructuredOutput = &StructuredSubagentOutput{
					Source:  schemaSource,
					Mode:    schemaMode,
					Status:  "valid",
					Data:    assembled.Data,
					HasData: true,
				}
			}
		}
		if valErr != "" && (schemaMode == "strict" || !assembled.SchemaOverridden) {
			payload := map[string]any{
				"error":           "schema_violation",
				"message":         valErr,
				"missingRequired": missing,
				"data":            assembled.Data,
			}
			b, _ := json.MarshalIndent(payload, "", "  ")
			res.RawOutput = string(b)
			res.Stderr = "schema_violation: " + valErr
			res.ExitCode = 1
			return res
		}

		b, err := json.MarshalIndent(assembled.Data, "", "  ")
		if err != nil {
			res.RawOutput = fmt.Sprintf(`{"error":%q}`, "Failed to serialize yield data: "+err.Error())
		} else {
			res.RawOutput = string(b)
		}
		if !hadFailureBeforeYield {
			res.ExitCode = 0
			if assembled.SchemaOverridden {
				res.Stderr = SubagentWarningSchemaOverridden
			} else if res.StructuredOutput != nil {
				res.Stderr = res.StructuredOutput.Error
			} else {
				res.Stderr = ""
			}
		} else if res.Stderr == "" {
			res.Stderr = "Subagent failed after yielding a result."
		}
		return res
	}

	if !UsesYield(outputSchema) {
		if res.ExitCode == 0 && strings.TrimSpace(res.RawOutput) == "" {
			res.RawOutput = SubagentWarningEmptyMessage
			res.Stderr = SubagentWarningEmptyMessage
			res.ExitCode = 1
		}
		return res
	}

	// Structured run ended without calling yield: try JSON fallback on raw output.
	allowFallback := res.ExitCode == 0 && !doneAborted && !signalAborted
	if allowFallback && strings.TrimSpace(res.RawOutput) != "" {
		var parsed any
		if err := json.Unmarshal([]byte(strings.TrimSpace(res.RawOutput)), &parsed); err == nil {
			if m, ok := parsed.(map[string]any); ok {
				if inner, has := m["data"]; has {
					parsed = inner
				}
			}
			_, valErr := ValidateOutputAgainstSchema(outputSchema, parsed)
			if valErr == "" {
				if includeStructured {
					res.StructuredOutput = &StructuredSubagentOutput{
						Source:  schemaSource,
						Mode:    schemaMode,
						Status:  "valid",
						Data:    parsed,
						HasData: true,
					}
				}
				if b, err := json.MarshalIndent(parsed, "", "  "); err == nil {
					res.RawOutput = string(b)
				}
				return res
			}
		}
	}

	if allowFallback {
		if res.RawOutput != "" {
			res.RawOutput = SubagentWarningMissingYield + "\n\n" + res.RawOutput
		} else {
			res.RawOutput = SubagentWarningMissingYield
		}
		if includeStructured {
			res.StructuredOutput = &StructuredSubagentOutput{
				Source: schemaSource,
				Mode:   schemaMode,
				Status: "invalid",
				Error:  SubagentWarningMissingYield,
			}
			if schemaMode == "strict" {
				res.ExitCode = 1
				res.Stderr = SubagentWarningMissingYield
			}
		}
	}
	return res
}
