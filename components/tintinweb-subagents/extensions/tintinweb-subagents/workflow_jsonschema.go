package tintinweb_subagents

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Validating a script-supplied JSON Schema (agent({ schema })). upstream: src/workflow/json-schema.ts.
//
// The original checks with the `typebox` package's Check/Errors; there is no such library in Go, so this is a validator
// of the JSON Schema keywords typebox implements, with typebox's English messages (typebox src/system/locale/en_US)
// and its keyword evaluation order. Patterns are Go (RE2) regular expressions: a lookahead or a back-reference is not
// a pattern this runtime can validate and the schema is refused at compile time (PORT.md).

const (
	workflowMaxSchemaBytes    = 64 * 1024 // largest schema accepted, serialized
	workflowMaxReportedErrors = 5         // validation errors quoted back to the model
	workflowMaxCheckDepth     = 128       // a value nested deeper than this is reported, not walked
)

// workflowCompiledSchema is a schema that can check values. Schema is the schema as given (the tool's parameters and the
// journal key); Check returns "" when the value matches, else the account of what is wrong. upstream: CompiledSchema.
type workflowCompiledSchema struct {
	Schema map[string]any
	// Raw is the schema as the script wrote it (key order kept), when it came from a script.
	Raw  string
	root map[string]any
}

// workflowSchemaCompilation is compileJsonSchema's result: Compiled, or Message when it was refused.
type workflowSchemaCompilation struct {
	OK       bool
	Compiled *workflowCompiledSchema
	Message  string
}

var workflowSchemaTypes = map[string]bool{"string": true, "number": true, "integer": true, "boolean": true, "object": true, "array": true, "null": true}

// compileWorkflowSchema turns a script-supplied schema into something that can check values. A schema whose root is not an
// object cannot be a tool's input schema, so it is refused up front. upstream: compileJsonSchema.
func compileWorkflowSchema(schema any) workflowSchemaCompilation {
	root, ok := schema.(map[string]any)
	if !ok || schema == nil {
		return workflowSchemaCompilation{Message: "agent() opts.schema must be a JSON Schema object."}
	}
	if root["type"] != "object" {
		return workflowSchemaCompilation{Message: `agent() opts.schema must have ` + "`type: \"object\"`" + ` at its root — it becomes the tool's input schema, ` +
			"and a non-object root is not something a model can be asked to fill."}
	}
	serialized, err := json.Marshal(root)
	if err != nil {
		return workflowSchemaCompilation{Message: "agent() opts.schema must be JSON-serializable."}
	}
	if len(serialized) > workflowMaxSchemaBytes {
		return workflowSchemaCompilation{Message: fmt.Sprintf("agent() opts.schema is too large (%d bytes; the limit is %d).", len(serialized), workflowMaxSchemaBytes)}
	}
	// Walked here so a schema the validator cannot use fails at the call that wrote it, not inside a child's tool handler.
	if err := checkWorkflowSchemaShape(root, root, 0); err != nil {
		return workflowSchemaCompilation{Message: "agent() opts.schema is not a schema this runtime can validate: " + err.Error()}
	}
	return workflowSchemaCompilation{OK: true, Compiled: &workflowCompiledSchema{Schema: root, root: root}}
}

func checkWorkflowSchemaShape(root, schema map[string]any, depth int) error {
	if depth > workflowMaxCheckDepth {
		return errors.New("the schema is nested too deeply")
	}
	switch t := schema["type"].(type) {
	case nil:
	case string:
		if !workflowSchemaTypes[t] {
			return fmt.Errorf("unknown type %q", t)
		}
	case []any:
		for _, e := range t {
			s, ok := e.(string)
			if !ok || !workflowSchemaTypes[s] {
				return fmt.Errorf("unknown type %v", e)
			}
		}
	default:
		return errors.New("type must be a string or an array of strings")
	}
	if p, ok := schema["pattern"].(string); ok {
		if _, err := regexp.Compile(p); err != nil {
			return fmt.Errorf("pattern %q: %v", p, err)
		}
	}
	if ref, ok := schema["$ref"].(string); ok {
		if _, err := resolveSchemaRef(root, ref); err != nil {
			return err
		}
	}
	each := func(v any) error {
		if sub, ok := v.(map[string]any); ok {
			return checkWorkflowSchemaShape(root, sub, depth+1)
		}
		return nil
	}
	for _, key := range []string{"properties", "patternProperties", "$defs", "definitions", "dependentSchemas"} {
		if m, ok := schema[key].(map[string]any); ok {
			for _, v := range m {
				if err := each(v); err != nil {
					return err
				}
			}
		}
	}
	if m, ok := schema["patternProperties"].(map[string]any); ok {
		for p := range m {
			if _, err := regexp.Compile(p); err != nil {
				return fmt.Errorf("pattern %q: %v", p, err)
			}
		}
	}
	for _, key := range []string{"items", "additionalProperties", "additionalItems", "contains", "not", "if", "then", "else", "propertyNames"} {
		if err := each(schema[key]); err != nil {
			return err
		}
	}
	for _, key := range []string{"allOf", "anyOf", "oneOf", "prefixItems"} {
		if list, ok := schema[key].([]any); ok {
			for _, v := range list {
				if err := each(v); err != nil {
					return err
				}
			}
		}
	}
	if list, ok := schema["items"].([]any); ok {
		for _, v := range list {
			if err := each(v); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolveSchemaRef follows a local JSON Pointer reference ("#", "#/$defs/x").
func resolveSchemaRef(root map[string]any, ref string) (map[string]any, error) {
	if ref == "#" {
		return root, nil
	}
	if !strings.HasPrefix(ref, "#/") {
		return nil, fmt.Errorf("$ref %q is not a local reference", ref)
	}
	var cur any = root
	for _, part := range strings.Split(ref[2:], "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		switch c := cur.(type) {
		case map[string]any:
			cur = c[part]
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(c) {
				return nil, fmt.Errorf("$ref %q does not resolve", ref)
			}
			cur = c[i]
		default:
			return nil, fmt.Errorf("$ref %q does not resolve", ref)
		}
	}
	m, ok := cur.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("$ref %q does not resolve", ref)
	}
	return m, nil
}

// Check is checkAgainst: "" when value matches, else what is wrong, each problem as `$.path: message` joined by "; ".
// It never panics, whatever it is handed. upstream: checkAgainst.
func (c *workflowCompiledSchema) Check(value any) (verdict string) {
	defer func() {
		if r := recover(); r != nil {
			verdict = fmt.Sprintf("the value could not be validated: %v", r)
		}
	}()
	v := &workflowSchemaValidator{root: c.root}
	v.walk(c.root, normalizeSchemaValue(value, 0), "", 0)
	if len(v.errs) == 0 {
		return ""
	}
	var reported []string
	for _, e := range v.errs {
		where := "$"
		if e.path != "" {
			where = "$" + strings.ReplaceAll(e.path, "/", ".")
		}
		reported = append(reported, where+": "+e.message)
		if len(reported) >= workflowMaxReportedErrors {
			break
		}
	}
	if len(reported) > 0 {
		return strings.Join(reported, "; ")
	}
	return "the value does not match the required schema"
}

// normalizeSchemaValue maps the Go values a script's JSON and a Go test can produce onto the JSON model the validator
// walks: nil, bool, float64, string, []any, map[string]any. Anything else is returned as is and matches no type.
func normalizeSchemaValue(v any, depth int) any {
	if depth > workflowMaxCheckDepth {
		panic("the value is nested too deeply")
	}
	switch x := v.(type) {
	case nil, bool, string, float64:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case float32:
		return float64(x)
	case json.Number:
		f, _ := x.Float64()
		return f
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = normalizeSchemaValue(e, depth+1)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = normalizeSchemaValue(e, depth+1)
		}
		return out
	}
	return schemaOpaqueValue{v}
}

// schemaOpaqueValue is a value outside the JSON model (a function, a channel, a struct): it is of no JSON type.
type schemaOpaqueValue struct{ v any }

type workflowSchemaError struct{ path, message string }

type workflowSchemaValidator struct {
	root map[string]any
	errs []workflowSchemaError
}

func (v *workflowSchemaValidator) valid(schema map[string]any, value any, path string, depth int) bool {
	sub := &workflowSchemaValidator{root: v.root}
	sub.walk(schema, value, path, depth)
	return len(sub.errs) == 0
}

func (v *workflowSchemaValidator) add(path, message string) {
	v.errs = append(v.errs, workflowSchemaError{path, message})
}

func wfJSNumber(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e21 {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

func schemaTypeMatches(t string, value any) bool {
	switch t {
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "null":
		return value == nil
	case "array":
		_, ok := value.([]any)
		return ok
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "number":
		f, ok := value.(float64)
		return ok && !math.IsNaN(f) && !math.IsInf(f, 0)
	case "integer":
		f, ok := value.(float64)
		return ok && !math.IsNaN(f) && !math.IsInf(f, 0) && f == math.Trunc(f)
	}
	return false
}

// walk evaluates every keyword of schema against value in typebox's order, recording each failure. upstream: typebox ErrorSchema.
func (v *workflowSchemaValidator) walk(schema map[string]any, value any, path string, depth int) {
	if depth > workflowMaxCheckDepth {
		panic("the value is nested too deeply")
	}
	if t, ok := schema["type"]; ok {
		var types []string
		switch tt := t.(type) {
		case string:
			types = []string{tt}
		case []any:
			for _, e := range tt {
				s, _ := e.(string)
				types = append(types, s)
			}
		}
		matched := false
		for _, s := range types {
			if schemaTypeMatches(s, value) {
				matched = true
			}
		}
		if !matched {
			if _, single := t.(string); single {
				v.add(path, "must be "+types[0])
			} else {
				v.add(path, "must be either "+strings.Join(types, " or "))
			}
		}
	}
	switch val := value.(type) {
	case map[string]any:
		v.object(schema, val, path, depth)
	case []any:
		v.array(schema, val, path, depth)
	case string:
		v.str(schema, val, path)
	case float64:
		v.number(schema, val, path)
	}
	if ref, ok := schema["$ref"].(string); ok {
		if target, err := resolveSchemaRef(v.root, ref); err == nil {
			v.walk(target, value, path, depth+1)
		}
	}
	if c, ok := schema["const"]; ok && !schemaJSONEqual(c, value) {
		v.add(path, "must be equal to constant")
	}
	if list, ok := schema["enum"].([]any); ok {
		found := false
		for _, e := range list {
			if schemaJSONEqual(e, value) {
				found = true
			}
		}
		if !found {
			v.add(path, "must be equal to one of the allowed values")
		}
	}
	if cond, ok := schema["if"].(map[string]any); ok {
		if v.valid(cond, value, path, depth+1) {
			if then, ok := schema["then"].(map[string]any); ok && !v.valid(then, value, path, depth+1) {
				v.add(path, `must match "then" schema`)
			}
		} else if els, ok := schema["else"].(map[string]any); ok && !v.valid(els, value, path, depth+1) {
			v.add(path, `must match "else" schema`)
		}
	}
	if n, ok := schema["not"].(map[string]any); ok && v.valid(n, value, path, depth+1) {
		v.add(path, "must not be valid")
	}
	if list, ok := schema["allOf"].([]any); ok {
		for _, e := range list {
			if sub, ok := e.(map[string]any); ok {
				v.walk(sub, value, path, depth+1)
			}
		}
	}
	if list, ok := schema["anyOf"].([]any); ok {
		found := false
		for _, e := range list {
			if sub, ok := e.(map[string]any); ok && v.valid(sub, value, path, depth+1) {
				found = true
			}
		}
		if !found {
			v.add(path, "must match a schema in anyOf")
		}
	}
	if list, ok := schema["oneOf"].([]any); ok {
		n := 0
		for _, e := range list {
			if sub, ok := e.(map[string]any); ok && v.valid(sub, value, path, depth+1) {
				n++
			}
		}
		if n != 1 {
			v.add(path, "must match exactly one schema in oneOf")
		}
	}
}

func wfSortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (v *workflowSchemaValidator) object(schema map[string]any, obj map[string]any, path string, depth int) {
	if req, ok := schema["required"].([]any); ok {
		var missing []string
		for _, e := range req {
			if key, ok := e.(string); ok {
				if _, has := obj[key]; !has {
					missing = append(missing, key)
				}
			}
		}
		if len(missing) > 0 {
			v.add(path, "must have required properties "+strings.Join(missing, ", "))
		}
	}
	props, _ := schema["properties"].(map[string]any)
	patterns, _ := schema["patternProperties"].(map[string]any)
	if ap, ok := schema["additionalProperties"]; ok {
		var extra []string
		for _, key := range wfSortedKeys(obj) {
			if _, known := props[key]; known {
				continue
			}
			matched := false
			for p := range patterns {
				if re, err := regexp.Compile(p); err == nil && re.MatchString(key) {
					matched = true
				}
			}
			if matched {
				continue
			}
			switch sub := ap.(type) {
			case bool:
				if !sub {
					extra = append(extra, key)
				}
			case map[string]any:
				if !v.valid(sub, obj[key], path+"/"+key, depth+1) {
					extra = append(extra, key)
				}
			}
		}
		if len(extra) > 0 {
			v.add(path, "must not have additional properties")
		}
	}
	if deps, ok := schema["dependentRequired"].(map[string]any); ok {
		for _, prop := range wfSortedKeys(deps) {
			if _, has := obj[prop]; !has {
				continue
			}
			var missing []string
			list, _ := deps[prop].([]any)
			for _, e := range list {
				if s, ok := e.(string); ok {
					if _, has := obj[s]; !has {
						missing = append(missing, s)
					}
				}
			}
			if len(missing) > 0 {
				names := make([]string, len(list))
				for i, e := range list {
					names[i], _ = e.(string)
				}
				v.add(path, fmt.Sprintf("must have properties %s when property %s is present", strings.Join(names, ", "), prop))
			}
		}
	}
	if deps, ok := schema["dependentSchemas"].(map[string]any); ok {
		for _, prop := range wfSortedKeys(deps) {
			if _, has := obj[prop]; has {
				if sub, ok := deps[prop].(map[string]any); ok {
					v.walk(sub, obj, path, depth+1)
				}
			}
		}
	}
	for _, p := range wfSortedKeys(patterns) {
		re, err := regexp.Compile(p)
		sub, isSchema := patterns[p].(map[string]any)
		if err != nil || !isSchema {
			continue
		}
		for _, key := range wfSortedKeys(obj) {
			if re.MatchString(key) {
				v.walk(sub, obj[key], path+"/"+key, depth+1)
			}
		}
	}
	for _, key := range wfSortedKeys(props) {
		sub, isSchema := props[key].(map[string]any)
		if val, has := obj[key]; has && isSchema {
			v.walk(sub, val, path+"/"+key, depth+1)
		}
	}
	if sub, ok := schema["propertyNames"].(map[string]any); ok {
		var bad []string
		for _, key := range wfSortedKeys(obj) {
			if !v.valid(sub, key, path, depth+1) {
				bad = append(bad, key)
			}
		}
		if len(bad) > 0 {
			v.add(path, "property names "+strings.Join(bad, ", ")+" are invalid")
		}
	}
	if n, ok := schema["minProperties"].(float64); ok && float64(len(obj)) < n {
		v.add(path, "must not have fewer than "+wfJSNumber(n)+" properties")
	}
	if n, ok := schema["maxProperties"].(float64); ok && float64(len(obj)) > n {
		v.add(path, "must not have more than "+wfJSNumber(n)+" properties")
	}
}

func (v *workflowSchemaValidator) array(schema map[string]any, arr []any, path string, depth int) {
	prefix, _ := schema["prefixItems"].([]any)
	if sub, ok := schema["contains"].(map[string]any); ok {
		found := false
		for i, e := range arr {
			if v.valid(sub, e, path+"/"+strconv.Itoa(i), depth+1) {
				found = true
			}
		}
		if !found {
			v.add(path, "must contain at least 1 valid item")
		}
	}
	switch items := schema["items"].(type) {
	case map[string]any:
		for i, e := range arr {
			if i < len(prefix) {
				continue
			}
			v.walk(items, e, path+"/"+strconv.Itoa(i), depth+1)
		}
	case []any: // the array form of items (draft-07 tuples)
		for i, e := range arr {
			if i < len(items) {
				if sub, ok := items[i].(map[string]any); ok {
					v.walk(sub, e, path+"/"+strconv.Itoa(i), depth+1)
				}
			} else if extra, ok := schema["additionalItems"].(map[string]any); ok {
				v.walk(extra, e, path+"/"+strconv.Itoa(i), depth+1)
			} else if b, ok := schema["additionalItems"].(bool); ok && !b {
				v.add(path, "must not have additional items")
				break
			}
		}
	case bool:
		if !items && len(arr) > len(prefix) {
			v.add(path, "schema is false")
		}
	}
	if n, ok := schema["maxItems"].(float64); ok && float64(len(arr)) > n {
		v.add(path, "must not have more than "+wfJSNumber(n)+" items")
	}
	if n, ok := schema["minItems"].(float64); ok && float64(len(arr)) < n {
		v.add(path, "must not have fewer than "+wfJSNumber(n)+" items")
	}
	for i, e := range prefix {
		if i >= len(arr) {
			break
		}
		if sub, ok := e.(map[string]any); ok {
			v.walk(sub, arr[i], path+"/"+strconv.Itoa(i), depth+1)
		}
	}
	if u, ok := schema["uniqueItems"].(bool); ok && u {
		for i := range arr {
			for j := i + 1; j < len(arr); j++ {
				if schemaJSONEqual(arr[i], arr[j]) {
					v.add(path, "must not have duplicate items")
					return
				}
			}
		}
	}
}

// wfUTF16Len is a string's length in UTF-16 code units, which is what JavaScript counts.
func wfUTF16Len(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

func (v *workflowSchemaValidator) str(schema map[string]any, s string, path string) {
	if n, ok := schema["maxLength"].(float64); ok && float64(wfUTF16Len(s)) > n {
		v.add(path, "must not have more than "+wfJSNumber(n)+" characters")
	}
	if n, ok := schema["minLength"].(float64); ok && float64(wfUTF16Len(s)) < n {
		v.add(path, "must not have fewer than "+wfJSNumber(n)+" characters")
	}
	if p, ok := schema["pattern"].(string); ok {
		if re, err := regexp.Compile(p); err == nil && !re.MatchString(s) {
			v.add(path, `must match pattern "`+p+`"`)
		}
	}
}

func (v *workflowSchemaValidator) number(schema map[string]any, f float64, path string) {
	if n, ok := schema["exclusiveMaximum"].(float64); ok && !(f < n) {
		v.add(path, "must be < "+wfJSNumber(n))
	}
	if n, ok := schema["exclusiveMinimum"].(float64); ok && !(f > n) {
		v.add(path, "must be > "+wfJSNumber(n))
	}
	if n, ok := schema["maximum"].(float64); ok && !(f <= n) {
		v.add(path, "must be <= "+wfJSNumber(n))
	}
	if n, ok := schema["minimum"].(float64); ok && !(f >= n) {
		v.add(path, "must be >= "+wfJSNumber(n))
	}
	if n, ok := schema["multipleOf"].(float64); ok && n != 0 && math.Mod(f, n) != 0 {
		v.add(path, "must be multiple of "+wfJSNumber(n))
	}
}

func schemaJSONEqual(a, b any) bool {
	return reflect.DeepEqual(normalizeSchemaValue(a, 0), normalizeSchemaValue(b, 0))
}
