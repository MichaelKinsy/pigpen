package testkit

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"testing"
)

// The published AHP JSON Schemas (microsoft/agent-host-protocol @ 296b25e, MIT) validate every
// message the host puts on the wire, as pi-ahp's test/support/schema.ts does with ajv. The
// reducers are total, so a structurally wrong action can become a silent no-op; the schema turns
// that into an explicit failure. The validator implements exactly the keywords those files use:
// type, const, enum, properties, required, additionalProperties, items, oneOf, allOf, contains
// (+min/maxContains), minimum and local $ref.
//
//go:embed schema/*.schema.json
var schemaFS embed.FS

type schemaDoc struct {
	root map[string]any
	defs map[string]any
}

var (
	schemaOnce sync.Once
	schemas    map[string]*schemaDoc
	schemaErr  error
)

// Two upstream workarounds pi-ahp carries in schema.ts are applied here too (see PORT.md):
// dangling "#/$defs/" refs in oneOf/anyOf/allOf are dropped, and SessionStatus (a bitset) keeps
// `type: number` without the closed enum.
// LoadSchemas loads (once) and returns any load error.
func LoadSchemas() error {
	schemaOnce.Do(loadSchemas)
	return schemaErr
}

func loadSchemas() {
	schemas = map[string]*schemaDoc{}
	for _, name := range []string{"state", "actions", "commands", "notifications", "errors"} {
		raw, err := schemaFS.ReadFile("schema/" + name + ".schema.json")
		if err != nil {
			schemaErr = err
			return
		}
		var root map[string]any
		if err := json.Unmarshal(raw, &root); err != nil {
			schemaErr = fmt.Errorf("%s: %w", name, err)
			return
		}
		stripDangling(root)
		defs, _ := root["$defs"].(map[string]any)
		if ss, ok := defs["SessionStatus"].(map[string]any); ok {
			if _, had := ss["enum"]; had {
				RelaxedBitsetEnums = append(RelaxedBitsetEnums, name+"#/$defs/SessionStatus")
			}
			delete(ss, "enum")
		}
		schemas[name] = &schemaDoc{root: root, defs: defs}
	}
}

// StrippedDanglingRefs and RelaxedBitsetEnums count how often the two workarounds were needed;
// the "upstream workarounds" twins fail once a re-sync brings schemas that no longer need them.
var (
	StrippedDanglingRefs int
	RelaxedBitsetEnums   []string
)

func stripDangling(n any) {
	switch v := n.(type) {
	case []any:
		for _, x := range v {
			stripDangling(x)
		}
	case map[string]any:
		for _, key := range []string{"oneOf", "anyOf", "allOf"} {
			if members, ok := v[key].([]any); ok {
				kept := make([]any, 0, len(members))
				for _, m := range members {
					if mm, ok := m.(map[string]any); ok {
						if ref, _ := mm["$ref"].(string); strings.HasSuffix(ref, "#/$defs/") {
							StrippedDanglingRefs++
							continue
						}
					}
					kept = append(kept, m)
				}
				v[key] = kept
			}
		}
		for _, x := range v {
			stripDangling(x)
		}
	}
}

// CheckSchema validates value (any JSON-marshalable Go value) against `$defs/<def>` of the named
// schema file (state, actions, commands, notifications, errors). It returns nil when it conforms.
func CheckSchema(schema, def string, value any) error {
	schemaOnce.Do(loadSchemas)
	if schemaErr != nil {
		return schemaErr
	}
	doc := schemas[schema]
	if doc == nil {
		return fmt.Errorf("no schema %q", schema)
	}
	target, ok := doc.defs[def]
	if !ok {
		return fmt.Errorf("schema %s has no $defs/%s", schema, def)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	var decoded any
	if err := dec.Decode(&decoded); err != nil {
		return err
	}
	var errs []string
	validate(doc, target, decoded, "", &errs)
	if len(errs) > 0 {
		sort.Strings(errs)
		return fmt.Errorf("%s#/$defs/%s validation failed:\n  %s\npayload: %s", schema, def, strings.Join(errs, "\n  "), raw)
	}
	return nil
}

// AssertValid fails the test when value does not conform.
func AssertValid(t testing.TB, schema, def string, value any, context ...string) {
	t.Helper()
	if err := CheckSchema(schema, def, value); err != nil {
		if len(context) > 0 {
			t.Fatalf("%s: %v", context[0], err)
		}
		t.Fatal(err)
	}
}

func typeOK(t string, v any) bool {
	switch t {
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "null":
		return v == nil
	case "number":
		_, ok := v.(float64)
		return ok
	case "integer":
		f, ok := v.(float64)
		return ok && f == math.Trunc(f)
	}
	return true
}

// validate appends a message per violation.
func validate(doc *schemaDoc, s any, v any, path string, errs *[]string) {
	sm, ok := s.(map[string]any)
	if !ok {
		return // boolean schemas: true accepts, false is unused
	}
	if ref, ok := sm["$ref"].(string); ok {
		name := strings.TrimPrefix(ref, "#/$defs/")
		target, found := doc.defs[name]
		if !found {
			*errs = append(*errs, fmt.Sprintf("%s: unresolved $ref %s", loc(path), ref))
			return
		}
		validate(doc, target, v, path, errs)
	}
	if t, ok := sm["type"]; ok {
		matched := false
		switch tt := t.(type) {
		case string:
			matched = typeOK(tt, v)
		case []any:
			for _, x := range tt {
				if s, _ := x.(string); typeOK(s, v) {
					matched = true
				}
			}
		}
		if !matched {
			*errs = append(*errs, fmt.Sprintf("%s: must be %v", loc(path), t))
			return
		}
	}
	if c, ok := sm["const"]; ok && !jsonEqual(c, v) {
		*errs = append(*errs, fmt.Sprintf("%s: must be equal to constant %v", loc(path), c))
	}
	if e, ok := sm["enum"].([]any); ok {
		found := false
		for _, x := range e {
			if jsonEqual(x, v) {
				found = true
			}
		}
		if !found {
			*errs = append(*errs, fmt.Sprintf("%s: must be one of %v", loc(path), e))
		}
	}
	if min, ok := sm["minimum"].(float64); ok {
		if f, isNum := v.(float64); isNum && f < min {
			*errs = append(*errs, fmt.Sprintf("%s: must be >= %v", loc(path), min))
		}
	}
	if obj, ok := v.(map[string]any); ok {
		if req, ok := sm["required"].([]any); ok {
			for _, r := range req {
				if name, _ := r.(string); name != "" {
					if _, present := obj[name]; !present {
						*errs = append(*errs, fmt.Sprintf("%s: missing required property %q", loc(path), name))
					}
				}
			}
		}
		props, _ := sm["properties"].(map[string]any)
		for name, sub := range props {
			if pv, present := obj[name]; present {
				validate(doc, sub, pv, path+"/"+name, errs)
			}
		}
		if ap, present := sm["additionalProperties"]; present {
			for name, pv := range obj {
				if _, declared := props[name]; declared {
					continue
				}
				switch a := ap.(type) {
				case bool:
					if !a {
						*errs = append(*errs, fmt.Sprintf("%s: unknown property %q", loc(path), name))
					}
				default:
					validate(doc, a, pv, path+"/"+name, errs)
				}
			}
		}
	}
	if arr, ok := v.([]any); ok {
		if items, ok := sm["items"]; ok {
			for i, x := range arr {
				validate(doc, items, x, fmt.Sprintf("%s/%d", path, i), errs)
			}
		}
		if contains, ok := sm["contains"]; ok {
			count := 0
			for _, x := range arr {
				var sub []string
				validate(doc, contains, x, path, &sub)
				if len(sub) == 0 {
					count++
				}
			}
			min := 1.0
			if m, ok := sm["minContains"].(float64); ok {
				min = m
			}
			if float64(count) < min {
				*errs = append(*errs, fmt.Sprintf("%s: must contain at least %v matching item(s)", loc(path), min))
			}
			if m, ok := sm["maxContains"].(float64); ok && float64(count) > m {
				*errs = append(*errs, fmt.Sprintf("%s: must contain at most %v matching item(s)", loc(path), m))
			}
		}
	}
	if members, ok := sm["allOf"].([]any); ok {
		for _, m := range members {
			validate(doc, m, v, path, errs)
		}
	}
	if members, ok := sm["oneOf"].([]any); ok {
		matches := 0
		var firstFail []string
		for _, m := range members {
			var sub []string
			validate(doc, m, v, path, &sub)
			if len(sub) == 0 {
				matches++
			} else if firstFail == nil {
				firstFail = sub
			}
		}
		if matches != 1 {
			detail := fmt.Sprintf("matched %d schemas", matches)
			if matches == 0 && len(members) > 0 {
				// The first member's reasons rarely help for a discriminated union; report the
				// member whose `const` discriminator matched, if any.
				detail += "; " + bestReason(doc, members, v, path)
			}
			*errs = append(*errs, fmt.Sprintf("%s: must match exactly one schema in oneOf (%s)", loc(path), detail))
		}
	}
}

// bestReason reports why the member that shares the value's discriminator failed.
func bestReason(doc *schemaDoc, members []any, v any, path string) string {
	obj, _ := v.(map[string]any)
	var fallback []string
	for _, m := range members {
		var sub []string
		validate(doc, m, v, path, &sub)
		if len(sub) == 0 {
			continue
		}
		if fallback == nil {
			fallback = sub
		}
		mm := resolve(doc, m)
		props, _ := mm["properties"].(map[string]any)
		for name, p := range props {
			pm, _ := p.(map[string]any)
			if c, ok := pm["const"]; ok && obj != nil && jsonEqual(c, obj[name]) {
				return strings.Join(sub, "; ")
			}
		}
	}
	return strings.Join(fallback, "; ")
}

func resolve(doc *schemaDoc, s any) map[string]any {
	sm, _ := s.(map[string]any)
	if ref, ok := sm["$ref"].(string); ok {
		if t, ok := doc.defs[strings.TrimPrefix(ref, "#/$defs/")].(map[string]any); ok {
			return t
		}
	}
	return sm
}

func loc(path string) string {
	if path == "" {
		return "/"
	}
	return path
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}
