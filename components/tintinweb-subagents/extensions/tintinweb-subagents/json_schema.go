package tintinweb_subagents

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"
)

// Validation of a caller-supplied JSON Schema (the `StructuredOutput` tool's input schema and the check of what the
// child sent). The original validates with typebox v1; this is a Go validator of the same JSON Schema keywords with
// typebox's English messages. upstream: src/workflow/json-schema.ts.

const (
	maxSchemaBytes     = 64 * 1024 // largest schema accepted, serialized
	maxReportedSchemaE = 5         // validation errors quoted back to the model
)

// compiledSchema is a schema that passed compileJSONSchema.
type compiledSchema struct {
	Schema map[string]any
}

// check is "", or a human-readable account of what is wrong with value. upstream: CompiledSchema.check.
func (c *compiledSchema) check(value any) (msg string, ok bool) {
	defer func() {
		if r := recover(); r != nil { // reported, never thrown: a value that trips the validator fails that call, not the run
			msg, ok = fmt.Sprintf("the value could not be validated: %v", r), false
		}
	}()
	v := &schemaValidator{root: c.Schema}
	errs := v.validate(c.Schema, normalizeJSON(value), "")
	if len(errs) == 0 {
		return "", true
	}
	var reported []string
	for _, e := range errs {
		where := "$"
		if e.path != "" {
			where = "$" + strings.ReplaceAll(e.path, "/", ".")
		}
		reported = append(reported, where+": "+e.message)
		if len(reported) >= maxReportedSchemaE {
			break
		}
	}
	return strings.Join(reported, "; "), false
}

// compileJSONSchema turns a script-supplied schema into something that can be checked against, or the reason it
// cannot be one. upstream: compileJsonSchema.
func compileJSONSchema(schema any) (*compiledSchema, string) {
	root, ok := schema.(map[string]any)
	if !ok {
		return nil, "agent() opts.schema must be a JSON Schema object."
	}
	if root["type"] != "object" {
		return nil, `agent() opts.schema must have ` + "`type: \"object\"`" + ` at its root — it becomes the tool's input schema, ` +
			"and a non-object root is not something a model can be asked to fill."
	}
	raw, err := json.Marshal(root)
	if err != nil {
		return nil, "agent() opts.schema must be JSON-serializable."
	}
	if n := len(utf16.Encode([]rune(string(raw)))); n > maxSchemaBytes {
		return nil, fmt.Sprintf("agent() opts.schema is too large (%d bytes; the limit is %d).", n, maxSchemaBytes)
	}
	c := &compiledSchema{Schema: root}
	// Smoke-tested here so a schema the validator cannot walk fails at the call that wrote it.
	if msg := c.smoke(); msg != "" {
		return nil, "agent() opts.schema is not a schema this runtime can validate: " + msg
	}
	return c, ""
}

// smoke walks the schema against an empty object; an unresolvable $ref or a pattern that does not compile is the
// failure.
func (c *compiledSchema) smoke() (problem string) {
	defer func() {
		if r := recover(); r != nil {
			problem = fmt.Sprint(r)
		}
	}()
	v := &schemaValidator{root: c.Schema, strict: true}
	v.walk(c.Schema, 0)
	v.validate(c.Schema, map[string]any{}, "")
	return ""
}

// walk visits every keyword of the schema once: a $ref must resolve and a pattern must compile, wherever they sit.
func (v *schemaValidator) walk(node any, depth int) {
	if depth > 64 {
		panic("schema nests too deeply")
	}
	switch n := node.(type) {
	case map[string]any:
		if ref, ok := n["$ref"].(string); ok {
			v.resolve(ref)
		}
		if p, ok := n["pattern"].(string); ok {
			if _, err := regexp.Compile(p); err != nil {
				panic("invalid pattern " + p)
			}
		}
		for _, child := range n {
			v.walk(child, depth+1)
		}
	case []any:
		for _, child := range n {
			v.walk(child, depth+1)
		}
	}
}

// normalizeJSON gives a value the shapes encoding/json decodes into (float64, []any, map[string]any), so values that
// arrive as ints or typed maps compare and type-check the same.
func normalizeJSON(v any) any {
	switch v.(type) {
	case nil, bool, string, float64, []any, map[string]any:
		return v
	}
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if json.Unmarshal(b, &out) != nil {
		return v
	}
	return out
}

type schemaError struct{ path, message string }

type schemaValidator struct {
	root   map[string]any
	strict bool // panic on what cannot be walked (the smoke test)
	depth  int
}

func jsType(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case float64:
		if x == math.Trunc(x) && !math.IsInf(x, 0) {
			return "integer"
		}
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return "unknown"
}

func typeMatches(want string, v any) bool {
	got := jsType(v)
	switch want {
	case "number":
		return got == "number" || got == "integer"
	case "object", "array", "string", "boolean", "null", "integer":
		return got == want
	}
	return false
}

func (v *schemaValidator) resolve(ref string) any {
	if !strings.HasPrefix(ref, "#") {
		panic(fmt.Sprintf("cannot resolve $ref %q", ref))
	}
	var cur any = v.root
	for _, part := range strings.Split(strings.TrimPrefix(strings.TrimPrefix(ref, "#"), "/"), "/") {
		if part == "" {
			continue
		}
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		switch c := cur.(type) {
		case map[string]any:
			cur = c[part]
		default:
			panic(fmt.Sprintf("cannot resolve $ref %q", ref))
		}
	}
	if cur == nil {
		panic(fmt.Sprintf("cannot resolve $ref %q", ref))
	}
	return cur
}

func num(v any) (float64, bool) { f, ok := v.(float64); return f, ok }

func (v *schemaValidator) validate(schema any, value any, path string) []schemaError {
	v.depth++
	defer func() { v.depth-- }()
	if v.depth > 64 {
		panic("schema nests too deeply")
	}
	switch s := schema.(type) {
	case bool:
		if s {
			return nil
		}
		return []schemaError{{path, "schema is false"}}
	case map[string]any:
		return v.validateObject(s, value, path)
	}
	return nil
}

func (v *schemaValidator) validateObject(s map[string]any, value any, path string) []schemaError {
	var errs []schemaError
	add := func(msg string) { errs = append(errs, schemaError{path, msg}) }
	if ref, ok := s["$ref"].(string); ok {
		errs = append(errs, v.validate(v.resolve(ref), value, path)...)
	}
	if t, ok := s["type"]; ok {
		var types []string
		switch tt := t.(type) {
		case string:
			types = []string{tt}
		case []any:
			for _, x := range tt {
				if str, ok := x.(string); ok {
					types = append(types, str)
				}
			}
		}
		matched := false
		for _, want := range types {
			if typeMatches(want, value) {
				matched = true
			}
		}
		if !matched && len(types) > 0 {
			if len(types) == 1 {
				add("must be " + types[0])
			} else {
				add("must be either " + strings.Join(types, " or "))
			}
		}
	}
	if enum, ok := s["enum"].([]any); ok {
		found := false
		for _, e := range enum {
			if reflect.DeepEqual(normalizeJSON(e), value) {
				found = true
			}
		}
		if !found {
			add("must be equal to one of the allowed values")
		}
	}
	if c, ok := s["const"]; ok && !reflect.DeepEqual(normalizeJSON(c), value) {
		add("must be equal to constant")
	}
	if all, ok := s["allOf"].([]any); ok {
		for _, sub := range all {
			errs = append(errs, v.validate(sub, value, path)...)
		}
	}
	if anyOf, ok := s["anyOf"].([]any); ok {
		n := 0
		for _, sub := range anyOf {
			if len(v.validate(sub, value, path)) == 0 {
				n++
			}
		}
		if n == 0 {
			add("must match a schema in anyOf")
		}
	}
	if oneOf, ok := s["oneOf"].([]any); ok {
		n := 0
		for _, sub := range oneOf {
			if len(v.validate(sub, value, path)) == 0 {
				n++
			}
		}
		if n != 1 {
			add("must match exactly one schema in oneOf")
		}
	}
	if not, ok := s["not"]; ok && len(v.validate(not, value, path)) == 0 {
		add("must not be valid")
	}
	switch x := value.(type) {
	case map[string]any:
		errs = append(errs, v.validateMap(s, x, path)...)
	case []any:
		errs = append(errs, v.validateArray(s, x, path)...)
	case string:
		length := len(utf16.Encode([]rune(x)))
		if m, ok := num(s["minLength"]); ok && float64(length) < m {
			add(fmt.Sprintf("must not have fewer than %s characters", numText(m)))
		}
		if m, ok := num(s["maxLength"]); ok && float64(length) > m {
			add(fmt.Sprintf("must not have more than %s characters", numText(m)))
		}
		if p, ok := s["pattern"].(string); ok {
			re, err := regexp.Compile(p)
			if err != nil {
				if v.strict {
					panic("invalid pattern " + p)
				}
			} else if !re.MatchString(x) {
				add(fmt.Sprintf("must match pattern %q", p))
			}
		}
	case float64:
		if m, ok := num(s["minimum"]); ok && x < m {
			add("must be >= " + numText(m))
		}
		if m, ok := num(s["maximum"]); ok && x > m {
			add("must be <= " + numText(m))
		}
		if m, ok := num(s["exclusiveMinimum"]); ok && x <= m {
			add("must be > " + numText(m))
		}
		if m, ok := num(s["exclusiveMaximum"]); ok && x >= m {
			add("must be < " + numText(m))
		}
		if m, ok := num(s["multipleOf"]); ok && m != 0 && math.Abs(math.Mod(x, m)) > 1e-9 && math.Abs(math.Abs(math.Mod(x, m))-math.Abs(m)) > 1e-9 {
			add("must be multiple of " + numText(m))
		}
	}
	return errs
}

func numText(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return fmt.Sprintf("%d", int64(f))
	}
	return fmt.Sprint(f)
}

func (v *schemaValidator) validateMap(s map[string]any, obj map[string]any, path string) []schemaError {
	var errs []schemaError
	add := func(msg string) { errs = append(errs, schemaError{path, msg}) }
	if req, ok := s["required"].([]any); ok {
		var missing []string
		for _, r := range req {
			if name, ok := r.(string); ok {
				if _, has := obj[name]; !has {
					missing = append(missing, name)
				}
			}
		}
		if len(missing) > 0 {
			add("must have required properties " + strings.Join(missing, ", "))
		}
	}
	if m, ok := num(s["minProperties"]); ok && float64(len(obj)) < m {
		add(fmt.Sprintf("must not have fewer than %s properties", numText(m)))
	}
	if m, ok := num(s["maxProperties"]); ok && float64(len(obj)) > m {
		add(fmt.Sprintf("must not have more than %s properties", numText(m)))
	}
	props, _ := s["properties"].(map[string]any)
	keys := sortedKeys(obj)
	extra := false
	for _, k := range keys {
		child := path + "/" + strings.ReplaceAll(strings.ReplaceAll(k, "~", "~0"), "/", "~1")
		sub, declared := props[k]
		if declared {
			errs = append(errs, v.validate(sub, obj[k], child)...)
		}
		matchedPattern := false
		if pp, ok := s["patternProperties"].(map[string]any); ok {
			for pat, psub := range pp {
				if re, err := regexp.Compile(pat); err == nil && re.MatchString(k) {
					matchedPattern = true
					errs = append(errs, v.validate(psub, obj[k], child)...)
				}
			}
		}
		if !declared && !matchedPattern {
			switch ap := s["additionalProperties"].(type) {
			case bool:
				if !ap {
					extra = true
				}
			case map[string]any:
				errs = append(errs, v.validate(ap, obj[k], child)...)
			}
		}
	}
	if extra {
		add("must not have additional properties")
	}
	return errs
}

func (v *schemaValidator) validateArray(s map[string]any, arr []any, path string) []schemaError {
	var errs []schemaError
	add := func(msg string) { errs = append(errs, schemaError{path, msg}) }
	if m, ok := num(s["minItems"]); ok && float64(len(arr)) < m {
		add(fmt.Sprintf("must not have fewer than %s items", numText(m)))
	}
	if m, ok := num(s["maxItems"]); ok && float64(len(arr)) > m {
		add(fmt.Sprintf("must not have more than %s items", numText(m)))
	}
	switch items := s["items"].(type) {
	case map[string]any, bool:
		for i, e := range arr {
			errs = append(errs, v.validate(items, e, fmt.Sprintf("%s/%d", path, i))...)
		}
	case []any:
		for i, e := range arr {
			if i < len(items) {
				errs = append(errs, v.validate(items[i], e, fmt.Sprintf("%s/%d", path, i))...)
			}
		}
	}
	if u, _ := s["uniqueItems"].(bool); u {
		for i := range arr {
			for j := i + 1; j < len(arr); j++ {
				if reflect.DeepEqual(arr[i], arr[j]) {
					add("must not have duplicate items")
					return errs
				}
			}
		}
	}
	return errs
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// insertion order is lost in a Go map; sorted order keeps the report deterministic
	sort.Strings(keys)
	return keys
}
