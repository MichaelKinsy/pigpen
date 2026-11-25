// Package schemaorder reorders PiG tool parameter JSON Schemas to match omp
// v19.1.1's exact wire-level key order without altering schema content.
//
// Plain Go maps serialize alphabetically under encoding/json, whereas omp's
// TypeScript/ArkType schemas preserve JS object property insertion order.
// Models are sensitive to schema key order because it is part of the prompt.
// Apply converts a ToolDefinition's Parameters to the PiG SDK's ordered form
// (`~order` on the root map, `*sdk.OrderedObject` on nested objects), matching
// omp's captured wire schemas recursively while preserving every port key and
// value verbatim.
package schemaorder

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"sort"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

//go:embed omp_schemas.json
var ompSchemasJSON []byte

// schemaOrderKey is the root-map key that sdk.schemaRoot.MarshalJSON reads to
// serialize root keys in declared order (and omits from the wire JSON).
// upstream sdk: extensions/sdk/type_schema.go:70
const schemaOrderKey = "~order"

// orderNode is the recursive key-order tree parsed from an omp JSON Schema.
// Each JSON object node records its own key order plus child orderNodes for
// nested objects and array elements (e.g., `properties`, `items`, `anyOf`,
// `oneOf`). Primitives and empty arrays leave an entry nil.
type orderNode struct {
	keys     []string
	children map[string]*orderNode
	elements []*orderNode
}

// OmpEntry holds one tool's captured/derived schema metadata from omp_schemas.json.
type OmpEntry struct {
	Source         string
	DescriptionLen int
	RawParameters  json.RawMessage
	order          *orderNode
}

var (
	loadOnce    sync.Once
	loadedOrder map[string]*OmpEntry
	loadErr     error
)

func ensureLoaded() (map[string]*OmpEntry, error) {
	loadOnce.Do(func() {
		loadedOrder, loadErr = parseSchemasFile(ompSchemasJSON)
	})
	return loadedOrder, loadErr
}

// Entries returns a shallow copy of the loaded omp schema entries keyed by tool name.
func Entries() map[string]*OmpEntry {
	m, err := ensureLoaded()
	if err != nil {
		panic(fmt.Sprintf("schemaorder: corrupt embedded omp_schemas.json: %v", err))
	}
	out := make(map[string]*OmpEntry, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Apply returns a shallow copy of def whose Parameters are converted to the
// PiG SDK's ordered representation matching omp's wire key order recursively.
//
// Keys present in omp's schema come first in omp's exact order; any keys only
// the port has follow in sorted order. Nothing is added, removed, or modified
// in value (`enum` arrays and all scalar/content fields are untouched).
//
// If def.Name is absent from the embedded omp schema table or def.Parameters
// is nil, def is returned unchanged.
//
// Apply composes with internal/intent: `intent.Tool(schemaorder.Apply(def))`
// places `i` first in `properties` followed by omp's property order, and keeps
// the root keys in omp's order via `~order`.
func Apply(def sdk.ToolDefinition) sdk.ToolDefinition {
	if def.Parameters == nil {
		return def
	}
	entries, err := ensureLoaded()
	if err != nil {
		panic(fmt.Sprintf("schemaorder: corrupt embedded omp_schemas.json: %v", err))
	}
	entry, ok := entries[def.Name]
	if !ok || entry.order == nil {
		return def
	}
	def.Parameters = applyRoot(def.Parameters, entry.order)
	return def
}

// applyRoot converts a root sdk.Schema map so sdk.schemaRoot.MarshalJSON writes
// its keys in omp's order (via `~order`), and recursively converts nested
// objects into `*sdk.OrderedObject`.
func applyRoot(schema sdk.Schema, ref *orderNode) sdk.Schema {
	keys, vals := objectMembers(schema)
	orderedKeys := mergeKeyOrder(keys, ref)

	out := make(sdk.Schema, len(orderedKeys)+1)
	for _, k := range orderedKeys {
		var childRef *orderNode
		if ref != nil {
			childRef = ref.children[k]
		}
		out[k] = applyValue(vals[k], childRef, k)
	}
	out[schemaOrderKey] = orderedKeys
	return out
}

// applyValue recursively converts maps/OrderedObjects/TSchemas into
// *sdk.OrderedObject matching ref's key order. Arrays of schemas (e.g., anyOf,
// oneOf, allOf, prefixItems) have their object elements reordered too; `enum`
// and `required` arrays are left untouched.
func applyValue(val any, ref *orderNode, parentKey string) any {
	if val == nil {
		return nil
	}
	// Never reorder inside `enum` or `required`: their values are data arrays,
	// not schema containers.
	if parentKey == "enum" || parentKey == "required" {
		return val
	}
	if keys, vals, isObj := tryObjectMembers(val); isObj {
		orderedKeys := mergeKeyOrder(keys, ref)
		pairs := make([]any, 0, len(orderedKeys)*2)
		for _, k := range orderedKeys {
			var childRef *orderNode
			if ref != nil {
				childRef = ref.children[k]
			}
			pairs = append(pairs, k, applyValue(vals[k], childRef, k))
		}
		return sdk.NewOrderedObject(pairs...)
	}
	// elements themselves are schema objects (e.g. anyOf / oneOf / allOf).
	if slice, ok := toAnySlice(val); ok {
		return applySlice(slice, ref, parentKey)
	}

	return val
}

func applySlice(slice []any, ref *orderNode, parentKey string) []any {
	out := make([]any, len(slice))
	var refElems []*orderNode
	if ref != nil {
		refElems = ref.elements
	}
	for i, elem := range slice {
		var elemRef *orderNode
		if len(refElems) > 0 {
			elemRef = matchElementRef(elem, i, refElems)
		}
		out[i] = applyValue(elem, elemRef, "")
	}
	return out
}

// matchElementRef picks the best matching orderNode from refElems for an array
// element (important for `anyOf`/`oneOf` branches where the port might list
// branches in a different order than omp, or have a matching `type` branch).
func matchElementRef(elem any, idx int, refElems []*orderNode) *orderNode {
	keys, vals, isObj := tryObjectMembers(elem)
	if !isObj {
		if idx < len(refElems) {
			return refElems[idx]
		}
		return nil
	}
	// First prefer exact index if its key set overlaps well (and type matches if both specify type).
	if idx < len(refElems) && refElems[idx] != nil && branchCompatible(keys, vals, refElems[idx]) {
		return refElems[idx]
	}
	// Otherwise find the ref element with the largest key overlap.
	bestScore := 0
	var best *orderNode
	for _, cand := range refElems {
		if cand == nil {
			continue
		}
		score := keyOverlap(keys, cand.keys)
		if score > bestScore {
			bestScore = score
			best = cand
		}
	}
	if best != nil {
		return best
	}
	if idx < len(refElems) {
		return refElems[idx]
	}
	return nil
}

func branchCompatible(keys []string, vals map[string]any, cand *orderNode) bool {
	if len(cand.keys) == 0 {
		return false
	}
	return keyOverlap(keys, cand.keys) > 0
}

func keyOverlap(a, b []string) int {
	n := 0
	for _, k := range a {
		if slices.Contains(b, k) {
			n++
		}
	}
	return n
}

// mergeKeyOrder orders `presentKeys` with keys present in `ref.keys` first (in
// ref's exact order), followed by any extra port-only keys in sorted order.
// If ref has no key order, presentKeys are kept in their existing order (which
// is already sorted for plain Go maps, or authored order for OrderedObject/TSchema).
func mergeKeyOrder(presentKeys []string, ref *orderNode) []string {
	if ref == nil || len(ref.keys) == 0 {
		return slices.Clone(presentKeys)
	}
	present := make(map[string]struct{}, len(presentKeys))
	for _, k := range presentKeys {
		present[k] = struct{}{}
	}
	ordered := make([]string, 0, len(presentKeys))
	seen := make(map[string]struct{}, len(presentKeys))
	for _, k := range ref.keys {
		if _, ok := present[k]; ok {
			ordered = append(ordered, k)
			seen[k] = struct{}{}
		}
	}
	extra := make([]string, 0, len(presentKeys)-len(ordered))
	for _, k := range presentKeys {
		if _, ok := seen[k]; !ok {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	ordered = append(ordered, extra...)
	return ordered
}

// objectMembers extracts keys and values from a map/OrderedObject/TSchema.
// For plain Go maps (including sdk.Schema), `~order` is honored if present and
// never included as an enumerable key; remaining keys are sorted.
func objectMembers(v any) ([]string, map[string]any) {
	keys, vals, _ := tryObjectMembers(v)
	return keys, vals
}

func tryObjectMembers(v any) ([]string, map[string]any, bool) {
	switch obj := v.(type) {
	case *sdk.OrderedObject:
		if obj == nil {
			return nil, nil, false
		}
		keys := obj.Keys()
		vals := make(map[string]any, len(keys))
		for _, k := range keys {
			val, _ := obj.Get(k)
			vals[k] = val
		}
		return keys, vals, true
	case *sdk.TSchema:
		if obj == nil {
			return nil, nil, false
		}
		keys := obj.Keys()
		vals := make(map[string]any, len(keys))
		for _, k := range keys {
			val, _ := obj.Get(k)
			vals[k] = val
		}
		return keys, vals, true
	case map[string]any:
		if obj == nil {
			return nil, nil, false
		}
		return mapMembers(obj), obj, true
	default:
		// Handle typed maps with string keys (e.g. map[string]string) if any tool uses them.
		rv := reflect.ValueOf(v)
		if rv.IsValid() && rv.Kind() == reflect.Map && rv.Type().Key().Kind() == reflect.String {
			if rv.IsNil() {
				return nil, nil, false
			}
			keys := make([]string, 0, rv.Len())
			vals := make(map[string]any, rv.Len())
			iter := rv.MapRange()
			for iter.Next() {
				k := iter.Key().String()
				if k == schemaOrderKey {
					continue
				}
				keys = append(keys, k)
				vals[k] = iter.Value().Interface()
			}
			sort.Strings(keys)
			return keys, vals, true
		}
		return nil, nil, false
	}
}

func mapMembers(m map[string]any) []string {
	declared, _ := m[schemaOrderKey].([]string)
	keys := make([]string, 0, len(m))
	seen := make(map[string]struct{}, len(m))
	for _, k := range declared {
		if k == schemaOrderKey {
			continue
		}
		if _, ok := m[k]; ok {
			keys = append(keys, k)
			seen[k] = struct{}{}
		}
	}
	rest := make([]string, 0, len(m)-len(keys))
	for k := range m {
		if k == schemaOrderKey {
			continue
		}
		if _, ok := seen[k]; !ok {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	return append(keys, rest...)
}

func toAnySlice(v any) ([]any, bool) {
	switch s := v.(type) {
	case []any:
		return s, true
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
		// Leave []string, []int, etc. untouched as typed primitive slices so
		// `enum` / `type: ["number","null"]` / `required` keep their exact Go types.
		return nil, false
	}
}

// parseSchemasFile parses omp_schemas.json while preserving the exact JSON
// key order of every tool's `parameters` schema.
func parseSchemasFile(data []byte) (map[string]*OmpEntry, error) {
	var raw map[string]struct {
		Source         string          `json:"source"`
		DescriptionLen int             `json:"description_len"`
		Parameters     json.RawMessage `json:"parameters"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := make(map[string]*OmpEntry, len(raw))
	for name, rec := range raw {
		node, err := parseOrderTree(rec.Parameters)
		if err != nil {
			return nil, fmt.Errorf("tool %q: %w", name, err)
		}
		out[name] = &OmpEntry{
			Source:         rec.Source,
			DescriptionLen: rec.DescriptionLen,
			RawParameters:  rec.Parameters,
			order:          node,
		}
	}
	return out, nil
}

// parseOrderTree streams JSON tokens from data and builds an orderNode tree
// recording the exact key order of every JSON object and element trees of arrays.
func parseOrderTree(data []byte) (*orderNode, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	return parseOrderValue(dec, tok)
}

func parseOrderValue(dec *json.Decoder, tok json.Token) (*orderNode, error) {
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil, nil
	}
	switch delim {
	case '{':
		node := &orderNode{children: map[string]*orderNode{}}
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyTok.(string)
			if !ok {
				return nil, fmt.Errorf("expected string key, got %T", keyTok)
			}
			valTok, err := dec.Token()
			if err != nil {
				return nil, err
			}
			child, err := parseOrderValue(dec, valTok)
			if err != nil {
				return nil, err
			}
			if _, exists := node.children[key]; !exists {
				node.keys = append(node.keys, key)
			}
			node.children[key] = child
		}
		endTok, err := dec.Token()
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		if d, ok := endTok.(json.Delim); !ok || d != '}' {
			return nil, fmt.Errorf("expected '}', got %v", endTok)
		}
		return node, nil
	case '[':
		node := &orderNode{}
		hasObjChild := false
		for dec.More() {
			elemTok, err := dec.Token()
			if err != nil {
				return nil, err
			}
			child, err := parseOrderValue(dec, elemTok)
			if err != nil {
				return nil, err
			}
			if child != nil {
				hasObjChild = true
			}
			node.elements = append(node.elements, child)
		}
		endTok, err := dec.Token()
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		if d, ok := endTok.(json.Delim); !ok || d != ']' {
			return nil, fmt.Errorf("expected ']', got %v", endTok)
		}
		if !hasObjChild {
			return nil, nil
		}
		return node, nil
	default:
		return nil, fmt.Errorf("unexpected delimiter %q", delim)
	}
}
