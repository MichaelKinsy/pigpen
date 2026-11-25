package intent

import (
	"maps"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// unionKeys are the alternation keywords into which `InjectSchema` recurses
// for a pure union root (no own `properties`). `allOf` is intentionally absent:
// its members are sub-constraints, not alternatives.
// upstream: packages/agent/src/agent-loop.ts:988, 1028-1046
var unionKeys = []string{"anyOf", "oneOf"}

// InjectSchema returns a shallow-cloned schema with the `i` field injected as
// the FIRST property (encoded via `*sdk.OrderedObject` so the wire JSON emits
// `"i"` first regardless of Go map iteration order). When `mode == ModeRequire`,
// `"i"` is also appended to `required` if not already present. When `description`
// is empty, `i` is injected bare (`{"type":"string"}`), matching omp's
// `pruneDescriptions` bare injection.
//
// For a pure union root (`anyOf`/`oneOf` with no own `properties`), `i` is
// injected into each alternative branch instead of adding sibling root
// `properties`/`required` (which would collide with each closed branch's
// `additionalProperties: false`).
//
// If the object node already contains `i` in `properties`, `i` is reordered to
// the front and made required in `ModeRequire`, preserving its existing schema
// definition. Note that `Tool` checks `SchemaDefinesProperty` BEFORE calling
// `InjectSchema` so tool-owned `i` properties are left completely untouched.
//
// upstream: packages/agent/src/agent-loop.ts:1017-1072 (`injectIntentIntoSchema`)
func InjectSchema(schema sdk.Schema, mode Mode, description string) sdk.Schema {
	if schema == nil || mode == ModeOmit {
		return schema
	}
	injected := injectNode(schema, mode, description)
	if out, ok := injected.(sdk.Schema); ok {
		return out
	}
	if out, ok := injected.(map[string]any); ok {
		return sdk.Schema(out)
	}
	return schema
}

func injectNode(node any, mode Mode, description string) any {
	switch v := node.(type) {
	case *sdk.TSchema:
		if v == nil {
			return node
		}
		return injectObjectMap(v.Schema(), mode, description, true)
	case *sdk.OrderedObject:
		if v == nil {
			return node
		}
		return injectOrderedObject(v, mode, description)
	case map[string]any:
		if v == nil {
			return node
		}
		return injectObjectMap(v, mode, description, false)
	default:
		return node
	}
}

func injectObjectMap(schema map[string]any, mode Mode, description string, returnOrdered bool) any {
	propsVal, hasPropsKey := schema["properties"]
	hasOwnProps := isObjectSchemaMap(propsVal) && hasPropsKey

	// Pure union root (`anyOf` / `oneOf` with no own `properties`): push `i` into
	// every alternative branch. upstream: agent-loop.ts:1037-1046.
	if !hasOwnProps {
		for _, uk := range unionKeys {
			rawVariants, ok := schema[uk]
			if !ok {
				continue
			}
			variants, isArr := toAnySlice(rawVariants)
			if !isArr {
				continue
			}
			nextVariants := make([]any, len(variants))
			for i, variant := range variants {
				nextVariants[i] = injectNode(variant, mode, description)
			}
			out := maps.Clone(schema)
			out[uk] = nextVariants
			if returnOrdered {
				return mapToOrderedObject(out)
			}
			return out
		}
	}

	required := extractStringSlice(schema["required"])
	propKeys, propVals := objectMembers(propsVal)

	if _, exists := propVals[Field]; exists {
		needsReorder := len(propKeys) == 0 || propKeys[0] != Field
		needsRequired := mode == ModeRequire && !slices.Contains(required, Field)
		if !needsReorder && !needsRequired {
			if returnOrdered {
				return mapToOrderedObject(schema)
			}
			return schema
		}
		out := maps.Clone(schema)
		if needsReorder {
			out["properties"] = buildPropertiesWithIntentFirst(propKeys, propVals, propVals[Field])
		}
		if needsRequired {
			out["required"] = append(slices.Clone(required), Field)
		}
		if returnOrdered {
			return mapToOrderedObject(out)
		}
		return out
	}

	var intentProp any
	if description == "" {
		intentProp = sdk.NewOrderedObject("type", "string")
	} else {
		intentProp = sdk.NewOrderedObject("type", "string", "description", description)
	}

	out := maps.Clone(schema)
	out["properties"] = buildPropertiesWithIntentFirst(propKeys, propVals, intentProp)
	if mode == ModeRequire {
		out["required"] = append(slices.Clone(required), Field)
	}
	if returnOrdered {
		return mapToOrderedObject(out)
	}
	return out
}

func injectOrderedObject(obj *sdk.OrderedObject, mode Mode, description string) *sdk.OrderedObject {
	propsVal, hasPropsKey := obj.Get("properties")
	hasOwnProps := hasPropsKey && isObjectSchemaMap(propsVal)

	if !hasOwnProps {
		for _, uk := range unionKeys {
			rawVariants, ok := obj.Get(uk)
			if !ok {
				continue
			}
			variants, isArr := toAnySlice(rawVariants)
			if !isArr {
				continue
			}
			nextVariants := make([]any, len(variants))
			for i, variant := range variants {
				nextVariants[i] = injectNode(variant, mode, description)
			}
			return cloneOrderedObjectWith(obj, map[string]any{uk: nextVariants})
		}
	}

	reqVal, _ := obj.Get("required")
	required := extractStringSlice(reqVal)
	propKeys, propVals := objectMembers(propsVal)

	if existing, exists := propVals[Field]; exists {
		needsReorder := len(propKeys) == 0 || propKeys[0] != Field
		needsRequired := mode == ModeRequire && !slices.Contains(required, Field)
		if !needsReorder && !needsRequired {
			return obj
		}
		updates := map[string]any{}
		if needsReorder {
			updates["properties"] = buildPropertiesWithIntentFirst(propKeys, propVals, existing)
		}
		if needsRequired {
			updates["required"] = append(slices.Clone(required), Field)
		}
		return cloneOrderedObjectWith(obj, updates)
	}

	var intentProp any
	if description == "" {
		intentProp = sdk.NewOrderedObject("type", "string")
	} else {
		intentProp = sdk.NewOrderedObject("type", "string", "description", description)
	}

	updates := map[string]any{
		"properties": buildPropertiesWithIntentFirst(propKeys, propVals, intentProp),
	}
	if mode == ModeRequire {
		updates["required"] = append(slices.Clone(required), Field)
	}
	return cloneOrderedObjectWith(obj, updates)
}

func buildPropertiesWithIntentFirst(origKeys []string, origVals map[string]any, intentProp any) *sdk.OrderedObject {
	pairs := make([]any, 0, (len(origKeys)+1)*2)
	pairs = append(pairs, Field, intentProp)
	for _, k := range origKeys {
		if k == Field {
			continue
		}
		pairs = append(pairs, k, origVals[k])
	}
	return sdk.NewOrderedObject(pairs...)
}

func cloneOrderedObjectWith(obj *sdk.OrderedObject, updates map[string]any) *sdk.OrderedObject {
	keys := obj.Keys()
	pairs := make([]any, 0, (len(keys)+len(updates))*2)
	seen := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		seen[k] = struct{}{}
		if v, updated := updates[k]; updated {
			pairs = append(pairs, k, v)
		} else {
			v, _ := obj.Get(k)
			pairs = append(pairs, k, v)
		}
	}
	extra := make([]string, 0, len(updates))
	for k := range updates {
		if _, ok := seen[k]; !ok {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	for _, k := range extra {
		pairs = append(pairs, k, updates[k])
	}
	return sdk.NewOrderedObject(pairs...)
}

func mapToOrderedObject(m map[string]any) *sdk.OrderedObject {
	if m == nil {
		return sdk.NewOrderedObject()
	}
	// Preserve `~order` recorded by `sdk.TSchema.Schema()` if present, omitting
	// the internal marker key itself.
	declared, _ := m["~order"].([]string)
	pairs := make([]any, 0, len(m)*2)
	seen := make(map[string]struct{}, len(m))
	for _, k := range declared {
		if k == "~order" {
			continue
		}
		if v, ok := m[k]; ok {
			seen[k] = struct{}{}
			pairs = append(pairs, k, v)
		}
	}
	rest := make([]string, 0, len(m))
	for k := range m {
		if k == "~order" {
			continue
		}
		if _, ok := seen[k]; !ok {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	for _, k := range rest {
		pairs = append(pairs, k, m[k])
	}
	return sdk.NewOrderedObject(pairs...)
}

func isObjectSchemaMap(v any) bool {
	switch obj := v.(type) {
	case *sdk.OrderedObject:
		return obj != nil
	case *sdk.TSchema:
		return obj != nil
	case map[string]any:
		return obj != nil
	default:
		return false
	}
}

// objectMembers returns the keys (in authored order for *sdk.OrderedObject /
// *sdk.TSchema, or sorted for Go maps) and value map of a JSON-object-like value.
func objectMembers(v any) ([]string, map[string]any) {
	switch obj := v.(type) {
	case *sdk.OrderedObject:
		if obj == nil {
			return nil, nil
		}
		keys := obj.Keys()
		vals := make(map[string]any, len(keys))
		for _, k := range keys {
			val, _ := obj.Get(k)
			vals[k] = val
		}
		return keys, vals
	case *sdk.TSchema:
		if obj == nil {
			return nil, nil
		}
		keys := obj.Keys()
		vals := make(map[string]any, len(keys))
		for _, k := range keys {
			val, _ := obj.Get(k)
			vals[k] = val
		}
		return keys, vals
	case map[string]any:
		if obj == nil {
			return nil, nil
		}
		keys := make([]string, 0, len(obj))
		for k := range obj {
			if k == "~order" {
				continue
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return keys, obj
	default:
		return nil, nil
	}
}

func toAnySlice(v any) ([]any, bool) {
	if v == nil {
		return nil, false
	}
	switch s := v.(type) {
	case []any:
		return s, true
	case []string:
		out := make([]any, len(s))
		for i, item := range s {
			out[i] = item
		}
		return out, true
	case []map[string]any:
		out := make([]any, len(s))
		for i, item := range s {
			out[i] = item
		}
		return out, true
	case []*sdk.OrderedObject:
		out := make([]any, len(s))
		for i, item := range s {
			out[i] = item
		}
		return out, true
	case []*sdk.TSchema:
		out := make([]any, len(s))
		for i, item := range s {
			out[i] = item
		}
		return out, true
	default:
		rv := reflect.ValueOf(v)
		if rv.Kind() == reflect.Slice {
			out := make([]any, rv.Len())
			for i := range rv.Len() {
				out[i] = rv.Index(i).Interface()
			}
			return out, true
		}
		return nil, false
	}
}

func extractStringSlice(v any) []string {
	switch s := v.(type) {
	case []string:
		return slices.Clone(s)
	case []any:
		out := make([]string, 0, len(s))
		for _, item := range s {
			if str, ok := item.(string); ok {
				out = append(out, str)
			}
		}
		return out
	default:
		return nil
	}
}

// SchemaDefinesProperty reports whether any same-instance schema declaration or
// constraint owns property name `key`, mirroring omp's `schemaDefinesProperty`.
//
// upstream: packages/ai/src/utils/schema/json-schema-validator.ts:180-326
func SchemaDefinesProperty(schema any, key string) bool {
	if schema == nil || key == "" {
		return false
	}
	root := schema
	visited := map[uintptr]uint8{}
	return visitDefinesProperty(schema, key, root, "positive", visited)
}

const (
	ctxPositive  = "positive"
	ctxNegative  = "negative"
	ctxPredicate = "predicate"
	maxRefDepth  = 64
)

func visitDefinesProperty(node any, key string, root any, ctx string, visited map[uintptr]uint8) bool {
	if !isObjectSchemaMap(node) {
		return false
	}
	if ptr := pointerID(node); ptr != 0 {
		var bit uint8 = 1
		switch ctx {
		case ctxNegative:
			bit = 2
		case ctxPredicate:
			bit = 4
		}
		if visited[ptr]&bit != 0 {
			return false
		}
		visited[ptr] |= bit
	}

	_, members := objectMembers(node)
	if len(members) == 0 {
		return false
	}

	if constVal, ok := members["const"]; ok && isObjectSchemaMap(constVal) {
		_, cm := objectMembers(constVal)
		if _, has := cm[key]; has {
			return true
		}
	}
	if enumVal, ok := members["enum"]; ok {
		if items, isArr := toAnySlice(enumVal); isArr {
			for _, item := range items {
				if isObjectSchemaMap(item) {
					_, im := objectMembers(item)
					if _, has := im[key]; has {
						return true
					}
				}
			}
		}
	}
	if propsVal, ok := members["properties"]; ok && isObjectSchemaMap(propsVal) {
		_, pm := objectMembers(propsVal)
		if propSchema, has := pm[key]; has {
			// upstream: json-schema-validator.ts:257-259 — return immediately
			// so `properties: { [key]: false }` in a positive context answers
			// false without falling through to later keywords.
			return !isFalseBool(propSchema) || ctx != ctxPositive
		}
	}
	if reqVal, ok := members["required"]; ok {
		if slices.Contains(extractStringSlice(reqVal), key) {
			return true
		}
	}
	if propNames, ok := members["propertyNames"]; ok && propNames != nil {
		addProps, hasAdd := members["additionalProperties"]
		addFalse := hasAdd && isFalseBool(addProps)
		if !addFalse && matchesPropertyNameSchema(propNames, key, root, 0) {
			return true
		}
	}
	if patVal, ok := members["patternProperties"]; ok && isObjectSchemaMap(patVal) {
		_, patMap := objectMembers(patVal)
		for pattern, patSchema := range patMap {
			re, err := regexp.Compile(pattern)
			if err != nil {
				continue
			}
			if re.MatchString(key) {
				if !isFalseBool(patSchema) {
					return true
				}
			}
		}
	}
	if isObjectSchemaMap(members["unevaluatedProperties"]) {
		return true
	}
	if isObjectSchemaMap(members["additionalProperties"]) {
		return true
	}
	for _, kw := range []string{"anyOf", "oneOf", "allOf"} {
		if branches, ok := toAnySlice(members[kw]); ok {
			for _, branch := range branches {
				if visitDefinesProperty(branch, key, root, ctx, visited) {
					return true
				}
			}
		}
	}
	if ifNode, ok := members["if"]; ok {
		if visitDefinesProperty(ifNode, key, root, ctxPredicate, visited) {
			return true
		}
	}
	for _, kw := range []string{"then", "else"} {
		if sub, ok := members[kw]; ok {
			if visitDefinesProperty(sub, key, root, ctx, visited) {
				return true
			}
		}
	}
	if notNode, ok := members["not"]; ok {
		if ctx != ctxPositive || !requiresOnlyPropertyPresence(notNode, key, root, 0) {
			nextCtx := ctxPositive
			switch ctx {
			case ctxPredicate:
				nextCtx = ctxPredicate
			case ctxPositive:
				nextCtx = ctxNegative
			}
			if visitDefinesProperty(notNode, key, root, nextCtx, visited) {
				return true
			}
		}
	}
	if depSchemas, ok := members["dependentSchemas"]; ok && isObjectSchemaMap(depSchemas) {
		_, dm := objectMembers(depSchemas)
		if _, has := dm[key]; has {
			return true
		}
		for _, dep := range dm {
			if visitDefinesProperty(dep, key, root, ctx, visited) {
				return true
			}
		}
	}
	if depReq, ok := members["dependentRequired"]; ok && isObjectSchemaMap(depReq) {
		_, dm := objectMembers(depReq)
		if _, has := dm[key]; has {
			return true
		}
		for _, dep := range dm {
			if slices.Contains(extractStringSlice(dep), key) {
				return true
			}
		}
	}
	if deps, ok := members["dependencies"]; ok && isObjectSchemaMap(deps) {
		_, dm := objectMembers(deps)
		if _, has := dm[key]; has {
			return true
		}
		for _, dep := range dm {
			if slices.Contains(extractStringSlice(dep), key) {
				return true
			}
			if visitDefinesProperty(dep, key, root, ctx, visited) {
				return true
			}
		}
	}
	if ref, ok := members["$ref"].(string); ok {
		if resolved, found := resolveLocalRef(root, ref); found {
			if visitDefinesProperty(resolved, key, root, ctx, visited) {
				return true
			}
		}
	}
	return false
}

func requiresOnlyPropertyPresence(node any, key string, root any, depth int) bool {
	if !isObjectSchemaMap(node) || depth >= maxRefDepth {
		return false
	}
	keys, members := objectMembers(node)
	for _, kw := range keys {
		switch kw {
		case "required", "type", "$ref", "description", "title", "$comment":
		default:
			return false
		}
	}
	if refVal, hasRef := members["$ref"]; hasRef {
		ref, ok := refVal.(string)
		if !ok || members["required"] != nil || members["type"] != nil {
			return false
		}
		resolved, found := resolveLocalRef(root, ref)
		if !found {
			return false
		}
		return requiresOnlyPropertyPresence(resolved, key, root, depth+1)
	}
	if t, hasType := members["type"]; hasType && t != "object" {
		return false
	}
	reqVal, hasReq := members["required"]
	if !hasReq {
		return false
	}
	reqList, ok := strictStringSlice(reqVal)
	return ok && slices.Contains(reqList, key)
}

func strictStringSlice(v any) ([]string, bool) {
	switch s := v.(type) {
	case []string:
		return s, true
	case []any:
		out := make([]string, 0, len(s))
		for _, item := range s {
			str, ok := item.(string)
			if !ok {
				return nil, false
			}
			out = append(out, str)
		}
		return out, true
	default:
		return nil, false
	}
}

func matchesPropertyNameSchema(schema any, key string, root any, depth int) bool {
	if depth >= maxRefDepth {
		return false
	}
	if b, ok := schema.(bool); ok {
		return b
	}
	if !isObjectSchemaMap(schema) {
		return false
	}
	_, m := objectMembers(schema)
	if ref, ok := m["$ref"].(string); ok {
		if resolved, found := resolveLocalRef(root, ref); found {
			if !matchesPropertyNameSchema(resolved, key, root, depth+1) {
				return false
			}
		}
	}
	if c, has := m["const"]; has {
		s, ok := c.(string)
		if !ok || s != key {
			return false
		}
	}
	if e, has := m["enum"]; has {
		if items, ok := toAnySlice(e); ok {
			found := false
			for _, item := range items {
				if s, ok := item.(string); ok && s == key {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
	}
	if p, ok := m["pattern"].(string); ok {
		re, err := regexp.Compile(p)
		if err != nil || !re.MatchString(key) {
			return false
		}
	}
	return true
}

// resolveLocalRef resolves a `#/path/to/node` JSON Pointer against root,
// walking both object members and array elements (upstream:
// packages/ai/src/utils/schema/json-schema-validator.ts:168-177).
func resolveLocalRef(root any, ref string) (any, bool) {
	if ref == "#" {
		return root, true
	}
	if !strings.HasPrefix(ref, "#/") {
		return nil, false
	}
	current := root
	for _, rawToken := range strings.Split(ref[2:], "/") {
		token := strings.ReplaceAll(strings.ReplaceAll(rawToken, "~1", "/"), "~0", "~")
		if isObjectSchemaMap(current) {
			_, m := objectMembers(current)
			next, ok := m[token]
			if !ok {
				return nil, false
			}
			current = next
			continue
		}
		if arr, ok := toAnySlice(current); ok {
			idx, err := strconv.Atoi(token)
			if err != nil || idx < 0 || idx >= len(arr) {
				return nil, false
			}
			current = arr[idx]
			continue
		}
		return nil, false
	}
	return current, true
}

func isFalseBool(v any) bool {
	b, ok := v.(bool)
	return ok && !b
}

func pointerID(v any) uintptr {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Map, reflect.Pointer:
		return rv.Pointer()
	default:
		return 0
	}
}
