package toolcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// upstream: packages/coding-agent/src/exec/tool-cli.ts

// ToolCliInputLimitBytes is the shared stdin/@file safety ceiling (16 MiB),
// matching omp's TOOL_CLI_INPUT_LIMIT_BYTES.
// upstream: packages/coding-agent/src/exec/tool-cli.ts:49
const ToolCliInputLimitBytes = 16 * 1024 * 1024

// ToolCliUsageError is raised for command lines that do not match the tool's schema.
// upstream: packages/coding-agent/src/exec/tool-cli.ts:52-57
type ToolCliUsageError struct {
	Message string
}

func (e *ToolCliUsageError) Error() string { return e.Message }

// ErrAborted is wrapped when a tool CLI execution or argument read is cancelled.
var ErrAborted = errors.New("tool command aborted")

// ToolCliRequest is one invocation of a tool command in the shell.
// upstream: packages/coding-agent/src/exec/tool-cli.ts:5-14
type ToolCliRequest struct {
	Name  string
	Argv  []string
	Cwd   string
	Stdin string
}

// ToolCliResult is the shell-composable response of a tool command.
// upstream: packages/coding-agent/src/exec/tool-cli.ts:17-22
type ToolCliResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// ToolCliDefinition is the live definition of one tool command.
// upstream: packages/coding-agent/src/exec/tool-cli.ts:25-29
type ToolCliDefinition struct {
	Name        string
	Description string
	HasDesc     bool // when false and Description == "", defaults to "session tool"
	Schema      any  // map[string]any, json.RawMessage, or *orderedObj
}

// ToolCliHost supplies live tool definitions and executes parsed calls.
// upstream: packages/coding-agent/src/exec/tool-cli.ts:32-37
type ToolCliHost interface {
	Definition(ctx context.Context, name string) (ToolCliDefinition, error)
	Call(ctx context.Context, name string, args map[string]any) (ToolCliResult, error)
}

// ToolCliParseOptions configures one schema-derived argument parse.
// upstream: packages/coding-agent/src/exec/tool-cli.ts:40-46
type ToolCliParseOptions struct {
	Cwd        string
	Stdin      string
	ReadSource func(ctx context.Context, absolutePath string) (string, error)
}

// orderedObj preserves JSON property insertion order so help text and
// positional argument discovery match JavaScript's object iteration order.
type orderedObj struct {
	keys []string
	vals map[string]any
}

func newOrderedObj() *orderedObj {
	return &orderedObj{vals: make(map[string]any)}
}

func (o *orderedObj) get(key string) (any, bool) {
	if o == nil {
		return nil, false
	}
	v, ok := o.vals[key]
	return v, ok
}

func (o *orderedObj) set(key string, val any) {
	if _, exists := o.vals[key]; !exists {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = val
}

func (o *orderedObj) del(key string) {
	if _, exists := o.vals[key]; !exists {
		return
	}
	delete(o.vals, key)
	for i, k := range o.keys {
		if k == key {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

func (o *orderedObj) len() int {
	if o == nil {
		return 0
	}
	return len(o.keys)
}

// decodeOrderedJSON parses JSON preserving object key order as *orderedObj.
func decodeOrderedJSON(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	val, err := decodeOrderedValue(dec)
	if err != nil {
		return nil, err
	}
	// Ensure no trailing garbage.
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, errors.New("unexpected trailing JSON token")
		}
		return nil, err
	}
	return val, nil
}

func decodeOrderedValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			obj := newOrderedObj()
			for dec.More() {
				kTok, err := dec.Token()
				if err != nil {
					return nil, err
				}
				k, ok := kTok.(string)
				if !ok {
					return nil, errors.New("expected string object key")
				}
				v, err := decodeOrderedValue(dec)
				if err != nil {
					return nil, err
				}
				obj.set(k, v)
			}
			// Consume closing '}'
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return obj, nil
		case '[':
			arr := []any{}
			for dec.More() {
				v, err := decodeOrderedValue(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return arr, nil
		default:
			return nil, fmt.Errorf("unexpected delimiter %q", t)
		}
	case json.Number:
		return normalizeJSONNumber(t), nil
	default:
		return t, nil
	}
}

func normalizeJSONNumber(n json.Number) any {
	s := n.String()
	if !strings.ContainsAny(s, ".eE") {
		if iv, err := strconv.ParseInt(s, 10, 64); err == nil {
			return iv
		}
	}
	if fv, err := strconv.ParseFloat(s, 64); err == nil {
		return fv
	}
	return s
}

// toOrdered converts a Go schema representation (json.RawMessage, []byte,
// map[string]any, *orderedObj, etc.) into an *orderedObj tree.
func toOrdered(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case *orderedObj:
		return x
	case json.RawMessage:
		if len(bytes.TrimSpace(x)) == 0 {
			return nil
		}
		parsed, err := decodeOrderedJSON(x)
		if err != nil {
			return nil
		}
		return parsed
	case []byte:
		if len(bytes.TrimSpace(x)) == 0 {
			return nil
		}
		parsed, err := decodeOrderedJSON(x)
		if err != nil {
			return nil
		}
		return parsed
	case map[string]any:
		obj := newOrderedObj()
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			obj.set(k, toOrdered(x[k]))
		}
		return obj
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = toOrdered(item)
		}
		return out
	case []string:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = item
		}
		return out
	default:
		return x
	}
}

func cloneOrderedValue(v any) any {
	switch x := v.(type) {
	case *orderedObj:
		if x == nil {
			return nil
		}
		c := newOrderedObj()
		for _, k := range x.keys {
			c.set(k, cloneOrderedValue(x.vals[k]))
		}
		return c
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = cloneOrderedValue(item)
		}
		return out
	default:
		return v
	}
}

func materializeOrderedValue(v any) any {
	switch x := v.(type) {
	case *orderedObj:
		if x == nil {
			return nil
		}
		m := make(map[string]any, len(x.keys))
		for _, k := range x.keys {
			m[k] = materializeOrderedValue(x.vals[k])
		}
		return m
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = materializeOrderedValue(item)
		}
		return out
	default:
		return v
	}
}

type scalarKind string

const (
	scalarString   scalarKind = "string"
	scalarInteger  scalarKind = "integer"
	scalarNumber   scalarKind = "number"
	scalarBoolean  scalarKind = "boolean"
	scalarObject   scalarKind = "object"
	scalarFallback scalarKind = "fallback"
)

type schemaLeaf struct {
	path        []string
	kind        scalarKind
	values      []any
	hasValues   bool
	description string
	required    bool
	repeatable  bool
	positional  bool
	schema      *orderedObj
}

func schemaType(s *orderedObj) string {
	v, ok := s.get("type")
	if !ok {
		return ""
	}
	if str, ok := v.(string); ok {
		return str
	}
	if arr, ok := v.([]any); ok {
		for _, item := range arr {
			if str, ok := item.(string); ok && str != "null" {
				return str
			}
		}
	}
	return ""
}

func leafScalarKind(s *orderedObj) scalarKind {
	switch schemaType(s) {
	case "string":
		return scalarString
	case "integer":
		return scalarInteger
	case "number":
		return scalarNumber
	case "boolean":
		return scalarBoolean
	case "object":
		return scalarObject
	case "array":
		if items, ok := s.get("items"); ok {
			if itemObj, ok := items.(*orderedObj); ok {
				return leafScalarKind(itemObj)
			}
		}
		return scalarFallback
	default:
		return scalarFallback
	}
}

// protocolIntentDescription is omp's legacy tool-cli.ts:138 description string
// (kept only so `notrunc` is also stripped if a legacy protocol-intent schema
// is passed directly).
const protocolIntentDescription = "Short present-participle intent for this call."

func hasLegacyProtocolIntent(schema any) bool {
	root, ok := toOrdered(schema).(*orderedObj)
	if !ok || root == nil {
		return false
	}
	propsVal, ok := root.get("properties")
	if !ok {
		return false
	}
	props, ok := propsVal.(*orderedObj)
	if !ok || props == nil {
		return false
	}
	iVal, ok := props.get("i")
	if !ok {
		return false
	}
	iObj, ok := iVal.(*orderedObj)
	if !ok || iObj == nil {
		return false
	}
	desc, _ := iObj.get("description")
	return desc == protocolIntentDescription
}

// schemaHasHarnessIntent reports whether schema defines a top-level property
// named `i` (directly under `properties`, via `$ref`/`allOf`, or inside every
// non-null branch of a pure `anyOf`/`oneOf` root).
//
// In oh-my-pig, mounted tools share one wire schema with top-level calls where
// `internal/intent` injects `i` first (often `required`) and strips it before
// the tool's Execute. Decided from the schema alone without importing
// `internal/intent`.
func schemaHasHarnessIntent(schema any) bool {
	ordered := toOrdered(schema)
	return nodeHasTopLevelIntent(ordered, ordered, make(map[string]bool))
}

func nodeHasTopLevelIntent(root, node any, resolving map[string]bool) bool {
	s, ok := node.(*orderedObj)
	if !ok || s == nil {
		return false
	}
	if refVal, ok := s.get("$ref"); ok {
		if ref, ok := refVal.(string); ok && !resolving[ref] {
			if target, found := resolvePointer(root, ref); found {
				resolving[ref] = true
				has := nodeHasTopLevelIntent(root, target, resolving)
				delete(resolving, ref)
				if has {
					return true
				}
			}
		}
	}
	if propsVal, ok := s.get("properties"); ok {
		if props, ok := propsVal.(*orderedObj); ok && props != nil {
			if _, exists := props.get("i"); exists {
				return true
			}
			return false
		}
	}
	if allOfVal, ok := s.get("allOf"); ok {
		if arr, ok := allOfVal.([]any); ok {
			for _, part := range arr {
				if nodeHasTopLevelIntent(root, part, resolving) {
					return true
				}
			}
		}
	}
	// Pure union root (`anyOf` / `oneOf` with no own `properties`): true when
	// there is at least one non-null branch and every non-null branch defines `i`.
	for _, key := range []string{"anyOf", "oneOf"} {
		altVal, ok := s.get(key)
		if !ok {
			continue
		}
		arr, ok := altVal.([]any)
		if !ok || len(arr) == 0 {
			continue
		}
		checked := 0
		allHave := true
		for _, branch := range arr {
			if isNullSchema(branch) {
				continue
			}
			checked++
			if !nodeHasTopLevelIntent(root, branch, resolving) {
				allHave = false
				break
			}
		}
		if checked > 0 && allHave {
			return true
		}
	}
	return false
}

var arrayIndexRE = regexp.MustCompile(`^(?:0|[1-9]\d*)$`)

func resolvePointer(root any, ref string) (any, bool) {
	if ref == "#" {
		return root, true
	}
	if !strings.HasPrefix(ref, "#/") {
		return nil, false
	}
	val := root
	for _, encoded := range strings.Split(ref[2:], "/") {
		key := strings.ReplaceAll(strings.ReplaceAll(encoded, "~1", "/"), "~0", "~")
		switch cur := val.(type) {
		case *orderedObj:
			next, ok := cur.get(key)
			if !ok {
				return nil, false
			}
			val = next
		case []any:
			if !arrayIndexRE.MatchString(key) {
				return nil, false
			}
			idx, err := strconv.Atoi(key)
			if err != nil || idx < 0 || idx >= len(cur) {
				return nil, false
			}
			val = cur[idx]
		default:
			return nil, false
		}
	}
	return val, true
}

func jsonStringify(v any) string {
	b, err := json.Marshal(materializeOrderedValue(v))
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func uniqueValues(values []any) []any {
	var out []any
	var seen []string
	for _, v := range values {
		enc := jsonStringify(v)
		dup := false
		for _, s := range seen {
			if s == enc {
				dup = true
				break
			}
		}
		if !dup {
			seen = append(seen, enc)
			out = append(out, v)
		}
	}
	return out
}

func stringSlice(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func toAnySlice(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

func mergeRequired(target *orderedObj, val any, union bool) {
	arr, ok := val.([]any)
	if !ok {
		return
	}
	incoming := stringSlice(arr)
	if !union {
		target.set("required", toAnySlice(incoming))
		return
	}
	existingRaw, _ := target.get("required")
	existing := stringSlice(existingRaw)
	seen := make(map[string]bool, len(existing)+len(incoming))
	merged := make([]string, 0, len(existing)+len(incoming))
	for _, name := range existing {
		if !seen[name] {
			seen[name] = true
			merged = append(merged, name)
		}
	}
	for _, name := range incoming {
		if !seen[name] {
			seen[name] = true
			merged = append(merged, name)
		}
	}
	target.set("required", toAnySlice(merged))
}

func mergeProperties(target *orderedObj, val any) {
	src, ok := val.(*orderedObj)
	if !ok || src == nil {
		return
	}
	var props *orderedObj
	if existing, ok := target.get("properties"); ok {
		if obj, ok := existing.(*orderedObj); ok && obj != nil {
			props = obj
		}
	}
	if props == nil {
		props = newOrderedObj()
	}
	for _, k := range src.keys {
		props.set(k, src.vals[k])
	}
	target.set("properties", props)
}

func mergeSchema(target, source *orderedObj, unionRequired bool) {
	for _, k := range source.keys {
		v := source.vals[k]
		switch k {
		case "required":
			mergeRequired(target, v, unionRequired)
		case "properties":
			mergeProperties(target, v)
		default:
			target.set(k, v)
		}
	}
}

func isNullSchema(v any) bool {
	s, ok := v.(*orderedObj)
	if !ok || s == nil {
		return false
	}
	if schemaType(s) == "null" {
		return true
	}
	if enumVal, ok := s.get("enum"); ok {
		if arr, ok := enumVal.([]any); ok && len(arr) > 0 {
			for _, item := range arr {
				if item != nil {
					return false
				}
			}
			return true
		}
	}
	return false
}

func combineAlternatives(alternatives []*orderedObj) *orderedObj {
	combined := newOrderedObj()
	var types []string
	for _, alt := range alternatives {
		if t := schemaType(alt); t != "" {
			types = append(types, t)
		}
	}
	if len(types) > 0 {
		allSame := true
		for _, t := range types[1:] {
			if t != types[0] {
				allSame = false
				break
			}
		}
		if allSame {
			combined.set("type", types[0])
		}
	}

	var enumCandidates []any
	for _, alt := range alternatives {
		if ev, ok := alt.get("enum"); ok {
			if arr, ok := ev.([]any); ok {
				enumCandidates = append(enumCandidates, arr...)
				continue
			}
		}
		if cv, ok := alt.get("const"); ok {
			enumCandidates = append(enumCandidates, cv)
		}
	}
	if enumValues := uniqueValues(enumCandidates); len(enumValues) > 0 {
		combined.set("enum", enumValues)
	}

	props := newOrderedObj()
	for _, alt := range alternatives {
		pv, ok := alt.get("properties")
		if !ok {
			continue
		}
		ap, ok := pv.(*orderedObj)
		if !ok || ap == nil {
			continue
		}
		for _, name := range ap.keys {
			child := ap.vals[name]
			existing, hasExisting := props.get(name)
			existingObj, eOK := existing.(*orderedObj)
			childObj, cOK := child.(*orderedObj)
			if hasExisting && eOK && cOK && existingObj != nil && childObj != nil {
				props.set(name, combineAlternatives([]*orderedObj{existingObj, childObj}))
			} else {
				props.set(name, child)
			}
		}
	}
	if props.len() > 0 {
		combined.set("type", "object")
		combined.set("properties", props)
		reqSets := make([]map[string]bool, len(alternatives))
		var firstOrder []string
		for i, alt := range alternatives {
			reqRaw, _ := alt.get("required")
			ss := stringSlice(reqRaw)
			set := make(map[string]bool, len(ss))
			for _, name := range ss {
				if i == 0 && !set[name] {
					firstOrder = append(firstOrder, name)
				}
				set[name] = true
			}
			reqSets[i] = set
		}
		var intersection []string
		for _, name := range firstOrder {
			inAll := true
			for _, set := range reqSets[1:] {
				if !set[name] {
					inAll = false
					break
				}
			}
			if inAll {
				intersection = append(intersection, name)
			}
		}
		if len(intersection) > 0 {
			combined.set("required", toAnySlice(intersection))
		}
	}
	return combined
}

func normalizeNode(root, schema any, resolving map[string]bool) any {
	s, ok := schema.(*orderedObj)
	if !ok || s == nil {
		return schema
	}
	result := newOrderedObj()
	if refVal, ok := s.get("$ref"); ok {
		if ref, ok := refVal.(string); ok && !resolving[ref] {
			if target, found := resolvePointer(root, ref); found {
				resolving[ref] = true
				normalized := normalizeNode(root, target, resolving)
				delete(resolving, ref)
				if normObj, ok := normalized.(*orderedObj); ok && normObj != nil {
					mergeSchema(result, normObj, true)
				}
			}
		}
	}
	if allOfVal, ok := s.get("allOf"); ok {
		if arr, ok := allOfVal.([]any); ok {
			for _, part := range arr {
				normalized := normalizeNode(root, part, resolving)
				if normObj, ok := normalized.(*orderedObj); ok && normObj != nil {
					mergeSchema(result, normObj, true)
				}
			}
		}
	}
	for _, key := range []string{"anyOf", "oneOf"} {
		altVal, ok := s.get(key)
		if !ok {
			continue
		}
		arr, ok := altVal.([]any)
		if !ok {
			continue
		}
		var alternatives []*orderedObj
		for _, part := range arr {
			normalized := normalizeNode(root, part, resolving)
			if isNullSchema(normalized) {
				continue
			}
			if normObj, ok := normalized.(*orderedObj); ok && normObj != nil {
				alternatives = append(alternatives, normObj)
			}
		}
		if len(alternatives) == 1 {
			mergeSchema(result, alternatives[0], false)
		} else if len(alternatives) > 1 {
			mergeSchema(result, combineAlternatives(alternatives), false)
		}
	}
	for _, key := range s.keys {
		if key == "$ref" || key == "allOf" || key == "anyOf" || key == "oneOf" || key == "$defs" || key == "definitions" {
			continue
		}
		val := s.vals[key]
		normalized := val
		if key == "properties" {
			if propObj, ok := val.(*orderedObj); ok && propObj != nil {
				props := newOrderedObj()
				for _, name := range propObj.keys {
					props.set(name, normalizeNode(root, propObj.vals[name], resolving))
				}
				normalized = props
			}
		} else if key == "items" {
			normalized = normalizeNode(root, val, resolving)
		}
		switch key {
		case "required":
			mergeRequired(result, normalized, true)
		case "properties":
			mergeProperties(result, normalized)
		default:
			result.set(key, normalized)
		}
	}
	return result
}

func normalizeToolCliSchemaOrdered(schema any) *orderedObj {
	ordered := toOrdered(schema)
	normalized := normalizeNode(ordered, ordered, make(map[string]bool))
	out, ok := normalized.(*orderedObj)
	if !ok || out == nil {
		out = newOrderedObj()
	}
	stripIntent := schemaHasHarnessIntent(ordered)
	stripNotrunc := hasLegacyProtocolIntent(ordered)
	if stripIntent || stripNotrunc {
		if propsVal, ok := out.get("properties"); ok {
			if props, ok := propsVal.(*orderedObj); ok && props != nil {
				if stripIntent {
					props.del("i")
				}
				if stripNotrunc {
					props.del("notrunc")
				}
			}
		}
		if reqVal, ok := out.get("required"); ok {
			reqs := stringSlice(reqVal)
			filtered := make([]string, 0, len(reqs))
			for _, name := range reqs {
				if (stripIntent && name == "i") || (stripNotrunc && name == "notrunc") {
					continue
				}
				filtered = append(filtered, name)
			}
			if len(filtered) == 0 {
				out.del("required")
			} else {
				out.set("required", toAnySlice(filtered))
			}
		}
	}
	return out
}

// NormalizeToolCliSchema resolves local $refs and folds schema combinators into
// the effective CLI shape.
// upstream: packages/coding-agent/src/exec/tool-cli.ts:307-320
func NormalizeToolCliSchema(schema any) map[string]any {
	mat := materializeOrderedValue(normalizeToolCliSchemaOrdered(schema))
	if m, ok := mat.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func collectLeaves(schema *orderedObj, parentRequired bool, propPath []string, leaves *[]*schemaLeaf) {
	propsVal, ok := schema.get("properties")
	if !ok {
		return
	}
	props, ok := propsVal.(*orderedObj)
	if !ok || props == nil {
		return
	}
	reqRaw, _ := schema.get("required")
	reqSet := make(map[string]bool)
	for _, name := range stringSlice(reqRaw) {
		reqSet[name] = true
	}
	for _, name := range props.keys {
		if len(propPath) == 0 && (name == "help" || name == "json") {
			continue
		}
		rawChild, ok := props.vals[name].(*orderedObj)
		if !ok || rawChild == nil {
			continue
		}
		requiredHere := parentRequired && reqSet[name]
		nextPath := append(append([]string(nil), propPath...), name)
		if childPropsVal, hasChildProps := rawChild.get("properties"); hasChildProps {
			if childProps, ok := childPropsVal.(*orderedObj); ok && childProps != nil {
				collectLeaves(rawChild, requiredHere, nextPath, leaves)
				continue
			}
		}
		repeatable := schemaType(rawChild) == "array"
		itemSchema := rawChild
		if repeatable {
			if iv, ok := rawChild.get("items"); ok {
				if ioObj, ok := iv.(*orderedObj); ok && ioObj != nil {
					itemSchema = ioObj
				}
			}
		}
		var values []any
		hasValues := false
		if ev, ok := itemSchema.get("enum"); ok {
			if arr, ok := ev.([]any); ok {
				values = arr
				hasValues = true
			}
		}
		kind := leafScalarKind(rawChild)
		desc, _ := rawChild.get("description")
		descStr, _ := desc.(string)
		positional := len(nextPath) == 1 && requiredHere && !repeatable &&
			(hasValues || kind == scalarString || kind == scalarInteger || kind == scalarNumber)
		*leaves = append(*leaves, &schemaLeaf{
			path:        nextPath,
			kind:        kind,
			values:      values,
			hasValues:   hasValues,
			description: descStr,
			required:    requiredHere,
			repeatable:  repeatable,
			positional:  positional,
			schema:      rawChild,
		})
	}
}

func schemaLeaves(schema *orderedObj) []*schemaLeaf {
	var leaves []*schemaLeaf
	collectLeaves(schema, true, nil, &leaves)
	reqRaw, _ := schema.get("required")
	rootRequired := stringSlice(reqRaw)
	reqIndex := func(name string) int {
		for i, r := range rootRequired {
			if r == name {
				return i
			}
		}
		return -1
	}
	type indexed struct {
		leaf  *schemaLeaf
		index int
	}
	items := make([]indexed, len(leaves))
	for i, l := range leaves {
		items[i] = indexed{leaf: l, index: i}
	}
	sort.SliceStable(items, func(i, j int) bool {
		li := math.MaxInt
		if items[i].leaf.positional {
			li = reqIndex(items[i].leaf.path[0])
		}
		ri := math.MaxInt
		if items[j].leaf.positional {
			ri = reqIndex(items[j].leaf.path[0])
		}
		if li != ri {
			return li < ri
		}
		return items[i].index < items[j].index
	})
	out := make([]*schemaLeaf, len(items))
	for i, it := range items {
		out[i] = it.leaf
	}
	return out
}

func flagName(propPath []string) string {
	parts := make([]string, len(propPath))
	for i, seg := range propPath {
		parts[i] = strings.ReplaceAll(seg, "_", "-")
	}
	return strings.Join(parts, ".")
}

func unambiguousCliLeaves(leaves []*schemaLeaf) (filtered []*schemaLeaf, ambiguousFields []string) {
	ownersBySpelling := make(map[string]map[*schemaLeaf]bool)
	addOwner := func(spelling string, leaf *schemaLeaf) {
		owners := ownersBySpelling[spelling]
		if owners == nil {
			owners = make(map[*schemaLeaf]bool)
			ownersBySpelling[spelling] = owners
		}
		owners[leaf] = true
	}
	for _, leaf := range leaves {
		flag := flagName(leaf.path)
		addOwner(flag, leaf)
		if !leaf.repeatable && leaf.kind == scalarBoolean && !leaf.hasValues {
			addOwner("no-"+flag, leaf)
		}
	}
	ambiguous := make(map[*schemaLeaf]bool)
	for _, owners := range ownersBySpelling {
		if len(owners) > 1 {
			for leaf := range owners {
				ambiguous[leaf] = true
			}
		}
	}
	for _, leaf := range leaves {
		if !ambiguous[leaf] {
			filtered = append(filtered, leaf)
			continue
		}
		parts := make([]string, len(leaf.path))
		for i, field := range leaf.path {
			b, _ := json.Marshal(field)
			parts[i] = string(b)
		}
		ambiguousFields = append(ambiguousFields, strings.Join(parts, "."))
	}
	return filtered, ambiguousFields
}

func formatEnum(values []any) string {
	parts := make([]string, len(values))
	for i, v := range values {
		if s, ok := v.(string); ok {
			parts[i] = s
		} else {
			parts[i] = jsonStringify(v)
		}
	}
	return "{" + strings.Join(parts, "|") + "}"
}

func valueUsage(leaf *schemaLeaf) string {
	suffix := ""
	if leaf.repeatable {
		suffix = "..."
	}
	if leaf.hasValues {
		return formatEnum(leaf.values) + suffix
	}
	usage := map[scalarKind]string{
		scalarString:   "<STRING>",
		scalarInteger:  "<INTEGER>",
		scalarNumber:   "<NUMBER>",
		scalarBoolean:  "<BOOLEAN>",
		scalarObject:   "<JSON_OBJECT>",
		scalarFallback: "<JSON>",
	}
	return usage[leaf.kind] + suffix
}

// RenderToolHelp renders complete command help directly from the live JSON schema.
// upstream: packages/coding-agent/src/exec/tool-cli.ts:430-471
func RenderToolHelp(def ToolCliDefinition) string {
	normalized := normalizeToolCliSchemaOrdered(def.Schema)
	leaves, ambiguousFields := unambiguousCliLeaves(schemaLeaves(normalized))
	flagNames := make(map[string]bool, len(leaves))
	for _, leaf := range leaves {
		flagNames[flagName(leaf.path)] = true
	}
	var reservedFields []string
	if propsVal, ok := normalized.get("properties"); ok {
		if props, ok := propsVal.(*orderedObj); ok && props != nil {
			for _, name := range []string{"help", "json"} {
				if _, exists := props.get(name); exists {
					reservedFields = append(reservedFields, name)
				}
			}
		}
	}

	desc := def.Description
	if !def.HasDesc && desc == "" {
		desc = "session tool"
	}
	var b strings.Builder
	b.WriteString(def.Name)
	b.WriteString(" — ")
	b.WriteString(desc)
	b.WriteString("\n\nUsage:\n  ")
	b.WriteString(def.Name)
	var positionals []*schemaLeaf
	for _, leaf := range leaves {
		if leaf.positional {
			positionals = append(positionals, leaf)
			b.WriteString(" <")
			b.WriteString(leaf.path[0])
			b.WriteByte('>')
		}
	}
	b.WriteString(" [OPTIONS] [@FILE] [-]\n")
	if len(positionals) > 0 {
		b.WriteString("\nArguments:\n")
		for _, leaf := range positionals {
			b.WriteString("  <")
			b.WriteString(leaf.path[0])
			b.WriteString("> ")
			b.WriteString(valueUsage(leaf))
			if leaf.description != "" {
				b.WriteString("  ")
				b.WriteString(leaf.description)
			}
			b.WriteByte('\n')
		}
	}
	if len(leaves) > 0 {
		b.WriteString("\nOptions:\n")
		for _, leaf := range leaves {
			flag := flagName(leaf.path)
			b.WriteString("  --")
			b.WriteString(flag)
			if !leaf.repeatable && leaf.kind == scalarBoolean && !leaf.hasValues {
				if flagNames["no-"+flag] {
					b.WriteString(" / --")
					b.WriteString(flag)
					b.WriteString("=<BOOLEAN>")
				} else {
					b.WriteString(" / --no-")
					b.WriteString(flag)
				}
			} else {
				b.WriteByte(' ')
				b.WriteString(valueUsage(leaf))
			}
			if leaf.description != "" {
				b.WriteString("  ")
				b.WriteString(leaf.description)
			}
			if leaf.required {
				b.WriteString("  (required)")
			}
			if leaf.repeatable {
				b.WriteString("  (repeatable)")
			}
			b.WriteByte('\n')
		}
	}
	if len(reservedFields) > 0 {
		b.WriteString("\nReserved schema fields:\n  ")
		b.WriteString(strings.Join(reservedFields, ", "))
		b.WriteString("  Set through -j/--json; their flag names belong to the command line.\n")
	}
	if len(ambiguousFields) > 0 {
		b.WriteString("\nAmbiguous schema fields:\n  ")
		b.WriteString(strings.Join(ambiguousFields, ", "))
		b.WriteString("  Set through -j/--json; their derived flag names collide.\n")
	}
	b.WriteString("  -j, --json <JSON>  Merge one raw JSON object.\n")
	b.WriteString("  @FILE             Merge a JSON object from FILE, or bind its text to the next argument.\n")
	b.WriteString("  -                 Same as @FILE, read from stdin.\n")
	b.WriteString("  -h, --help        Show this help.\n")
	return b.String()
}

func valueAt(root *orderedObj, propPath []string) (any, bool) {
	var cur any = root
	for _, seg := range propPath {
		obj, ok := cur.(*orderedObj)
		if !ok || obj == nil {
			return nil, false
		}
		next, exists := obj.get(seg)
		if !exists {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

func insertValue(root *orderedObj, propPath []string, value any, repeatable bool) {
	if len(propPath) == 0 {
		return
	}
	target := root
	for _, parent := range propPath[:len(propPath)-1] {
		child, exists := target.get(parent)
		if exists {
			if childObj, ok := child.(*orderedObj); ok && childObj != nil {
				target = childObj
				continue
			}
		}
		nested := newOrderedObj()
		target.set(parent, nested)
		target = nested
	}
	last := propPath[len(propPath)-1]
	cloned := cloneOrderedValue(value)
	if repeatable {
		if existing, exists := target.get(last); exists {
			if arr, ok := existing.([]any); ok {
				target.set(last, append(arr, cloned))
				return
			}
		}
		target.set(last, []any{cloned})
	} else {
		target.set(last, cloned)
	}
}

func deepMerge(target, source *orderedObj) {
	for _, name := range source.keys {
		val := source.vals[name]
		if existing, exists := target.get(name); exists {
			existingObj, eOK := existing.(*orderedObj)
			valObj, vOK := val.(*orderedObj)
			if eOK && vOK && existingObj != nil && valObj != nil {
				deepMerge(existingObj, valObj)
				continue
			}
		}
		target.set(name, cloneOrderedValue(val))
	}
}

func stripOneNewline(text string) string {
	if strings.HasSuffix(text, "\r\n") {
		return text[:len(text)-2]
	}
	if strings.HasSuffix(text, "\n") {
		return text[:len(text)-1]
	}
	return text
}

func readBoundedSource(ctx context.Context, absolutePath string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("%w: %v", ErrAborted, err)
	}
	f, err := os.Open(absolutePath)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	// Close the file descriptor if ctx is cancelled so blocking reads (e.g.
	// FIFOs or slow filesystems) unblock promptly instead of hanging shutdown.
	stopClose := context.AfterFunc(ctx, func() {
		_ = f.Close()
	})
	defer stopClose()

	var buf bytes.Buffer
	chunk := make([]byte, 64*1024)
	total := 0
	for {
		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("%w: %v", ErrAborted, err)
		}
		n, rErr := f.Read(chunk)
		if n > 0 {
			total += n
			if total > ToolCliInputLimitBytes {
				return "", &ToolCliUsageError{Message: fmt.Sprintf("argument source exceeds %d-byte limit", ToolCliInputLimitBytes)}
			}
			buf.Write(chunk[:n])
		}
		if rErr == io.EOF {
			break
		}
		if rErr != nil {
			if err := ctx.Err(); err != nil {
				return "", fmt.Errorf("%w: %v", ErrAborted, err)
			}
			return "", rErr
		}
	}
	return buf.String(), nil
}

type argSource struct {
	origin string
	text   string
}

func readArgumentSource(ctx context.Context, argument string, opts ToolCliParseOptions) (*argSource, error) {
	if argument == "-" {
		if len(opts.Stdin) > ToolCliInputLimitBytes {
			return nil, &ToolCliUsageError{Message: fmt.Sprintf("argument source exceeds %d-byte limit", ToolCliInputLimitBytes)}
		}
		return &argSource{origin: "<stdin>", text: opts.Stdin}, nil
	}
	if !strings.HasPrefix(argument, "@") || len(argument) == 1 {
		return nil, nil
	}
	rawPath := argument[1:]
	readFn := opts.ReadSource
	if readFn == nil {
		readFn = readBoundedSource
	}
	absPath := rawPath
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(opts.Cwd, rawPath)
	}
	text, err := readFn(ctx, absPath)
	if err != nil {
		var usageErr *ToolCliUsageError
		if errors.As(err, &usageErr) {
			return nil, usageErr
		}
		if errors.Is(err, ErrAborted) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			return nil, fmt.Errorf("%w: %v", ErrAborted, err)
		}
		return nil, &ToolCliUsageError{Message: fmt.Sprintf("failed to read argument source `%s`: %s", rawPath, err.Error())}
	}
	return &argSource{origin: rawPath, text: text}, nil
}

var integerLiteralRE = regexp.MustCompile(`^-?(?:0|[1-9]\d*)$`)

const maxSafeInteger = 1<<53 - 1

func coerce(leaf *schemaLeaf, raw, source string) (any, error) {
	if leaf.hasValues {
		for _, v := range leaf.values {
			var rep string
			if s, ok := v.(string); ok {
				rep = s
			} else {
				rep = jsonStringify(v)
			}
			if rep == raw {
				return v, nil
			}
		}
		return nil, &ToolCliUsageError{Message: fmt.Sprintf("invalid value for `%s`; expected an enum member", source)}
	}
	kindSchema := leaf.schema
	if leaf.repeatable {
		if iv, ok := leaf.schema.get("items"); ok {
			if ioObj, ok := iv.(*orderedObj); ok && ioObj != nil {
				kindSchema = ioObj
			}
		}
	}
	switch leafScalarKind(kindSchema) {
	case scalarString:
		return raw, nil
	case scalarInteger:
		if integerLiteralRE.MatchString(raw) {
			if iv, err := strconv.ParseInt(raw, 10, 64); err == nil && iv >= -maxSafeInteger && iv <= maxSafeInteger {
				return iv, nil
			}
		}
		return nil, &ToolCliUsageError{Message: fmt.Sprintf("invalid value for `%s`; expected an integer", source)}
	case scalarNumber:
		if fv, ok := parseJSNumber(raw); ok {
			return fv, nil
		}
		return nil, &ToolCliUsageError{Message: fmt.Sprintf("invalid value for `%s`; expected a number", source)}
	case scalarBoolean:
		if raw == "true" {
			return true, nil
		}
		if raw == "false" {
			return false, nil
		}
		return nil, &ToolCliUsageError{Message: fmt.Sprintf("invalid value for `%s`; expected true or false", source)}
	case scalarObject:
		if parsed, err := decodeOrderedJSON([]byte(raw)); err == nil {
			if obj, ok := parsed.(*orderedObj); ok && obj != nil {
				return cloneOrderedValue(obj), nil
			}
		}
		return nil, &ToolCliUsageError{Message: fmt.Sprintf("invalid value for `%s`; expected a JSON object", source)}
	default: // scalarFallback
		if parsed, err := decodeOrderedJSON([]byte(raw)); err == nil {
			return cloneOrderedValue(parsed), nil
		}
		return raw, nil
	}
}

// parseJSNumber parses a numeric string like JavaScript's `Number(raw)` with
// `Number.isFinite(value) && raw.trim() !== ""` (tool-cli.ts:587-590): trims
// whitespace, supports 0x/0b/0o integer literals and decimal/scientific floats,
// and rejects NaN/Infinity/underscores.
func parseJSNumber(raw string) (float64, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.ContainsRune(trimmed, '_') {
		return 0, false
	}
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "0x") || strings.HasPrefix(lower, "0b") || strings.HasPrefix(lower, "0o") {
		if uv, err := strconv.ParseUint(trimmed, 0, 64); err == nil {
			return float64(uv), true
		}
		return 0, false
	}
	fv, err := strconv.ParseFloat(trimmed, 64)
	if err != nil || math.IsNaN(fv) || math.IsInf(fv, 0) {
		return 0, false
	}
	return fv, true
}

func nextPositional(leaves []*schemaLeaf, output, overrides *orderedObj) *schemaLeaf {
	for _, leaf := range leaves {
		if !leaf.positional {
			continue
		}
		if _, inOut := output.get(leaf.path[0]); inOut {
			continue
		}
		if _, inOver := overrides.get(leaf.path[0]); inOver {
			continue
		}
		return leaf
	}
	return nil
}

func soleObjectTarget(leaves []*schemaLeaf) *schemaLeaf {
	var required []*schemaLeaf
	for _, leaf := range leaves {
		if leaf.required {
			required = append(required, leaf)
		}
	}
	if len(required) == 1 && len(required[0].path) == 1 && required[0].kind == scalarObject {
		return required[0]
	}
	return nil
}

func bindLiteral(leaves []*schemaLeaf, output, overrides *orderedObj, raw, origin string) error {
	leaf := nextPositional(leaves, output, overrides)
	if leaf == nil {
		return &ToolCliUsageError{Message: fmt.Sprintf("unexpected argument `%s`; every positional is already set", origin)}
	}
	val, err := coerce(leaf, raw, origin)
	if err != nil {
		return err
	}
	insertValue(output, leaf.path, val, leaf.repeatable)
	return nil
}

func bindSource(leaves []*schemaLeaf, output, overrides *orderedObj, text, origin string) error {
	parsed, err := decodeOrderedJSON([]byte(text))
	if err == nil {
		if obj, ok := parsed.(*orderedObj); ok && obj != nil {
			target := soleObjectTarget(leaves)
			if target != nil {
				key := target.path[0]
				_, inOut := output.get(key)
				_, inOver := overrides.get(key)
				_, inParsed := obj.get(key)
				if !inOut && !inOver && !inParsed {
					insertValue(output, target.path, obj, false)
					return nil
				}
			}
			deepMerge(output, obj)
			return nil
		}
	}
	if nextPositional(leaves, output, overrides) == nil {
		return &ToolCliUsageError{Message: fmt.Sprintf("`%s` is not a JSON object and no positional remains for its text", origin)}
	}
	return bindLiteral(leaves, output, overrides, stripOneNewline(text), origin)
}

func validateRequired(schema *orderedObj, value any, propPath []string) error {
	obj, ok := value.(*orderedObj)
	if !ok || obj == nil {
		return nil
	}
	if reqRaw, hasReq := schema.get("required"); hasReq {
		for _, name := range stringSlice(reqRaw) {
			if _, exists := obj.get(name); !exists {
				full := append(append([]string(nil), propPath...), name)
				return &ToolCliUsageError{Message: fmt.Sprintf("missing required argument `%s`", strings.Join(full, "."))}
			}
		}
	}
	propsVal, ok := schema.get("properties")
	if !ok {
		return nil
	}
	props, ok := propsVal.(*orderedObj)
	if !ok || props == nil {
		return nil
	}
	for _, name := range props.keys {
		childVal, exists := obj.get(name)
		if !exists {
			continue
		}
		if childSchema, ok := props.vals[name].(*orderedObj); ok && childSchema != nil {
			nextPath := append(append([]string(nil), propPath...), name)
			if err := validateRequired(childSchema, childVal, nextPath); err != nil {
				return err
			}
		}
	}
	return nil
}

// ParseToolArgs binds shell arguments entirely from a JSON schema.
// upstream: packages/coding-agent/src/exec/tool-cli.ts:689-773
func ParseToolArgs(ctx context.Context, schema any, argv []string, opts ToolCliParseOptions) (map[string]any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	normalized := normalizeToolCliSchemaOrdered(schema)
	leaves, _ := unambiguousCliLeaves(schemaLeaves(normalized))
	output := newOrderedObj()
	overrides := newOrderedObj()
	optionsEnded := false

	for index := 0; index < len(argv); index++ {
		argument := argv[index]
		if optionsEnded {
			if err := bindLiteral(leaves, output, overrides, argument, argument); err != nil {
				return nil, err
			}
			continue
		}
		if argument == "--" {
			optionsEnded = true
			continue
		}
		if argument == "-" || (strings.HasPrefix(argument, "@") && len(argument) > 1) {
			source, err := readArgumentSource(ctx, argument, opts)
			if err != nil {
				return nil, err
			}
			if err := bindSource(leaves, output, overrides, source.text, source.origin); err != nil {
				return nil, err
			}
			continue
		}
		if argument == "-j" || argument == "--json" || strings.HasPrefix(argument, "--json=") {
			var raw string
			if strings.HasPrefix(argument, "--json=") {
				raw = argument[len("--json="):]
			} else {
				index++
				if index >= len(argv) {
					return nil, &ToolCliUsageError{Message: fmt.Sprintf("flag `%s` requires a value", argument)}
				}
				raw = argv[index]
			}
			source, err := readArgumentSource(ctx, raw, opts)
			if err != nil {
				return nil, err
			}
			jsonText := raw
			if source != nil {
				jsonText = source.text
			}
			parsed, parseErr := decodeOrderedJSON([]byte(jsonText))
			if parseErr != nil {
				return nil, &ToolCliUsageError{Message: fmt.Sprintf("invalid JSON for `--json`: %s", parseErr.Error())}
			}
			parsedObj, ok := parsed.(*orderedObj)
			if !ok || parsedObj == nil {
				return nil, &ToolCliUsageError{Message: "`--json` must be a JSON object"}
			}
			deepMerge(output, parsedObj)
			continue
		}
		if !strings.HasPrefix(argument, "--") {
			if strings.HasPrefix(argument, "-") {
				return nil, &ToolCliUsageError{Message: fmt.Sprintf("unknown flag `%s`", argument)}
			}
			if err := bindLiteral(leaves, output, overrides, argument, argument); err != nil {
				return nil, err
			}
			continue
		}

		eq := strings.IndexByte(argument, '=')
		var rawFlag string
		var inline string
		hasInline := eq >= 0
		if hasInline {
			rawFlag = argument[2:eq]
			inline = argument[eq+1:]
		} else {
			rawFlag = argument[2:]
		}

		findLeaf := func(name string) *schemaLeaf {
			for _, candidate := range leaves {
				if flagName(candidate.path) == name {
					return candidate
				}
			}
			return nil
		}

		leaf := findLeaf(rawFlag)
		negative := false
		if leaf == nil && strings.HasPrefix(rawFlag, "no-") {
			negative = true
			rawFlag = rawFlag[3:]
			leaf = findLeaf(rawFlag)
		}
		if leaf == nil {
			return nil, &ToolCliUsageError{Message: fmt.Sprintf("unknown flag `%s`", argument)}
		}
		if !leaf.repeatable && leaf.kind == scalarBoolean && !leaf.hasValues {
			if negative && hasInline {
				return nil, &ToolCliUsageError{Message: fmt.Sprintf("invalid value for `%s`; expected a flag without a value", argument)}
			}
			var val any
			if !hasInline {
				val = !negative
			} else {
				var err error
				val, err = coerce(leaf, inline, argument)
				if err != nil {
					return nil, err
				}
			}
			insertValue(overrides, leaf.path, val, false)
			continue
		}
		if negative {
			return nil, &ToolCliUsageError{Message: fmt.Sprintf("unknown flag `%s`", argument)}
		}
		var raw string
		if hasInline {
			raw = inline
		} else {
			index++
			if index >= len(argv) {
				return nil, &ToolCliUsageError{Message: fmt.Sprintf("flag `%s` requires a value", argument)}
			}
			raw = argv[index]
		}
		source, err := readArgumentSource(ctx, raw, opts)
		if err != nil {
			return nil, err
		}
		valStr := raw
		if source != nil {
			valStr = stripOneNewline(source.text)
		}
		coerced, err := coerce(leaf, valStr, argument)
		if err != nil {
			return nil, err
		}
		insertValue(overrides, leaf.path, coerced, leaf.repeatable)
	}

	for _, leaf := range leaves {
		if val, exists := valueAt(overrides, leaf.path); exists {
			insertValue(output, leaf.path, val, false)
		}
	}
	if err := validateRequired(normalized, output, nil); err != nil {
		return nil, err
	}
	materialized, ok := materializeOrderedValue(output).(map[string]any)
	if !ok || materialized == nil {
		return nil, &ToolCliUsageError{Message: "arguments must be a JSON object"}
	}
	return materialized, nil
}

func cliErrorResult(name, message string, exitCode int) ToolCliResult {
	return ToolCliResult{Stdout: "", Stderr: fmt.Sprintf("%s: %s\n", name, message), ExitCode: exitCode}
}

// RunToolCli executes one tool command invocation against a live host.
// On cancellation (ErrAborted / context cancellation), it returns the abort
// error instead of wrapping it as an exit-1 result, matching omp's runToolCli.
// upstream: packages/coding-agent/src/exec/tool-cli.ts:780-810
func RunToolCli(ctx context.Context, req ToolCliRequest, host ToolCliHost) (ToolCliResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	name := req.Name
	argv := req.Argv

	hasHelp := false
	for _, a := range argv {
		if a == "-h" || a == "--help" {
			hasHelp = true
			break
		}
	}
	if hasHelp {
		if len(argv) != 1 {
			return cliErrorResult(name, "--help cannot be combined with invocation arguments", 2), nil
		}
		def, err := host.Definition(ctx, name)
		if err != nil {
			if isAbortError(ctx, err) {
				return ToolCliResult{}, err
			}
			return cliErrorResult(name, err.Error(), 1), nil
		}
		return ToolCliResult{Stdout: RenderToolHelp(def), Stderr: "", ExitCode: 0}, nil
	}

	def, err := host.Definition(ctx, name)
	if err != nil {
		if isAbortError(ctx, err) {
			return ToolCliResult{}, err
		}
		return cliErrorResult(name, err.Error(), 1), nil
	}
	args, err := ParseToolArgs(ctx, def.Schema, argv, ToolCliParseOptions{
		Cwd:   req.Cwd,
		Stdin: req.Stdin,
	})
	if err != nil {
		if isAbortError(ctx, err) {
			return ToolCliResult{}, err
		}
		var usageErr *ToolCliUsageError
		if errors.As(err, &usageErr) {
			return cliErrorResult(name, fmt.Sprintf("%s\n%s: run `%s --help`", usageErr.Message, name, name), 2), nil
		}
		return cliErrorResult(name, err.Error(), 1), nil
	}
	if schemaHasHarnessIntent(def.Schema) {
		args["i"] = ""
	}
	res, err := host.Call(ctx, name, args)
	if err != nil {
		if isAbortError(ctx, err) {
			return ToolCliResult{}, err
		}
		var usageErr *ToolCliUsageError
		if errors.As(err, &usageErr) {
			return cliErrorResult(name, fmt.Sprintf("%s\n%s: run `%s --help`", usageErr.Message, name, name), 2), nil
		}
		return cliErrorResult(name, err.Error(), 1), nil
	}
	return res, nil
}

func isAbortError(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrAborted) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return ctx != nil && ctx.Err() != nil
}
