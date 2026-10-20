package pi_subagents

import (
	"math"
	"net"
	"net/mail"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

// A validator of JSON values against JSON Schema that reports errors as TypeBox 1.3.27 (`typebox/compile`'s Errors) does: the
// same keywords in the same order, the same instance and schema paths, the same en_US messages and the same limit of eight
// errors. The original's structured output and verify gates validate with it and show these messages to the model, so the
// wording and order are part of the behavior (checked against TypeBox in testdata/jsonschema_golden.json).

const maxSchemaErrors = 8

// schemaError is one TypeBox ValidationError (its message already localized).
type schemaError struct {
	Keyword      string
	SchemaPath   string
	InstancePath string
	Params       *jsObject
	Message      string
}

type schemaContext struct {
	errors []schemaError
	stack  []struct{ indices, keys map[any]bool }
}

func newSchemaContext() *schemaContext {
	c := &schemaContext{}
	c.push()
	return c
}

func (c *schemaContext) push() bool {
	c.stack = append(c.stack, struct{ indices, keys map[any]bool }{map[any]bool{}, map[any]bool{}})
	return true
}
func (c *schemaContext) pop() bool {
	if len(c.stack) > 0 {
		c.stack = c.stack[:len(c.stack)-1]
	}
	return true
}
func (c *schemaContext) top() struct{ indices, keys map[any]bool } { return c.stack[len(c.stack)-1] }
func (c *schemaContext) addIndex(i int) bool                       { c.top().indices[i] = true; return true }
func (c *schemaContext) addKey(k string) bool                      { c.top().keys[k] = true; return true }
func (c *schemaContext) merge(results ...*schemaContext) bool {
	for _, r := range results {
		for k := range r.top().indices {
			c.top().indices[k] = true
		}
		for k := range r.top().keys {
			c.top().keys[k] = true
		}
	}
	return true
}
func (c *schemaContext) atCapacity() bool { return len(c.errors) >= maxSchemaErrors }

// addError records an error unless at capacity; it always returns false (a failed check).
func (c *schemaContext) addError(e schemaError) bool {
	if !c.atCapacity() {
		if e.Message == "" {
			e.Message = schemaErrorMessage(e)
		}
		c.errors = append(c.errors, e)
	}
	return false
}

func params(kv ...any) *jsObject {
	o := newObject()
	for i := 0; i+1 < len(kv); i += 2 {
		o.set(kv[i].(string), kv[i+1])
	}
	return o
}

func schemaErrorMessage(e schemaError) string {
	p := e.Params
	get := func(k string) any {
		if p == nil {
			return nil
		}
		return p.vals[k]
	}
	join := func(v any) string {
		var parts []string
		for _, x := range anyList(v) {
			parts = append(parts, jsToString(x))
		}
		return strings.Join(parts, ", ")
	}
	switch e.Keyword {
	case "additionalProperties":
		return "must not have additional properties"
	case "anyOf":
		return "must match a schema in anyOf"
	case "boolean":
		return "schema is false"
	case "const":
		return "must be equal to constant"
	case "contains":
		return "must contain at least 1 valid item"
	case "dependencies", "dependentRequired":
		return "must have properties " + join(get("dependencies")) + " when property " + jsToString(get("property")) + " is present"
	case "enum":
		return "must be equal to one of the allowed values"
	case "exclusiveMaximum", "exclusiveMinimum", "maximum", "minimum":
		return "must be " + jsToString(get("comparison")) + " " + jsToString(get("limit"))
	case "format":
		return `must match format "` + jsToString(get("format")) + `"`
	case "if":
		return `must match "` + jsToString(get("failingKeyword")) + `" schema`
	case "maxItems":
		return "must not have more than " + jsToString(get("limit")) + " items"
	case "maxLength":
		return "must not have more than " + jsToString(get("limit")) + " characters"
	case "maxProperties":
		return "must not have more than " + jsToString(get("limit")) + " properties"
	case "minItems":
		return "must not have fewer than " + jsToString(get("limit")) + " items"
	case "minLength":
		return "must not have fewer than " + jsToString(get("limit")) + " characters"
	case "minProperties":
		return "must not have fewer than " + jsToString(get("limit")) + " properties"
	case "multipleOf":
		return "must be multiple of " + jsToString(get("multipleOf"))
	case "not":
		return "must not be valid"
	case "oneOf":
		return "must match exactly one schema in oneOf"
	case "pattern":
		return `must match pattern "` + jsToString(get("pattern")) + `"`
	case "propertyNames":
		return "property names " + join(get("propertyNames")) + " are invalid"
	case "required":
		return "must have required properties " + join(get("requiredProperties"))
	case "type":
		if s, ok := get("type").(string); ok {
			return "must be " + s
		}
		var names []string
		for _, x := range anyList(get("type")) {
			names = append(names, jsToString(x))
		}
		return "must be either " + strings.Join(names, " or ")
	case "unevaluatedItems":
		return "must not have unevaluated items"
	case "unevaluatedProperties":
		return "must not have unevaluated properties"
	case "uniqueItems":
		return "must not have duplicate items"
	}
	return "an unknown validation error occurred"
}

func anyList(v any) []any {
	a, _ := asArray(v)
	return a
}

// jsToString is String(value) for a decoded value (as template literals print it).
func jsToString(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case undef:
		return "undefined"
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			if e == nil {
				parts[i] = ""
			} else {
				parts[i] = jsToString(e)
			}
		}
		return strings.Join(parts, ",")
	case *jsObject, map[string]any:
		return "[object Object]"
	}
	if f, ok := asNumber(v); ok {
		return jsNumber(f)
	}
	return ""
}

// schemaWalker holds the root schema for $ref resolution.
type schemaWalker struct {
	root     any
	patterns map[string]*regexp.Regexp
	failed   string // a pattern or schema the walker could not use (reported as an invalid schema)
}

func (w *schemaWalker) regexp(pattern string) *regexp.Regexp {
	if re, ok := w.patterns[pattern]; ok {
		return re
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		if w.failed == "" {
			w.failed = "Invalid regular expression: /" + pattern + "/u: " + err.Error()
		}
		re = regexp.MustCompile(`$^`)
	}
	w.patterns[pattern] = re
	return re
}

// schemaErrors is TypeBox's Errors(schema, value): ok is whether the value is valid, errors at most eight of them (message
// "" and ok false from a schema the validator cannot use).
func schemaErrors(schema, value any) (valid bool, errors []schemaError, invalid string) {
	w := &schemaWalker{root: schema, patterns: map[string]*regexp.Regexp{}}
	ctx := newSchemaContext()
	valid = w.errorSchema(ctx, "#", "", schema, value)
	return valid, ctx.errors, w.failed
}

func isBoolean(v any) (bool, bool) { b, ok := v.(bool); return b, ok }

func isFiniteNumber(v any) (float64, bool) {
	f, ok := asNumber(v)
	return f, ok && !math.IsNaN(f) && !math.IsInf(f, 0)
}

func isObjectValue(v any) (*jsObject, bool) { return asObject(v) }

func typeNameMatches(name string, v any) bool {
	switch name {
	case "object":
		_, ok := asObject(v)
		return ok
	case "array":
		_, ok := asArray(v)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "integer":
		f, ok := isFiniteNumber(v)
		return ok && f == math.Trunc(f)
	case "number":
		_, ok := isFiniteNumber(v)
		return ok
	case "null":
		return v == nil
	case "string":
		_, ok := v.(string)
		return ok
	case "undefined", "void":
		return isUndefined(v) && v != nil
	}
	return true
}

func (w *schemaWalker) typeOK(typeValue, value any) bool {
	if names, ok := asArray(typeValue); ok {
		for _, n := range names {
			if s, ok := n.(string); ok && typeNameMatches(s, value) {
				return true
			}
		}
		return false
	}
	s, _ := typeValue.(string)
	return typeNameMatches(s, value)
}

func (w *schemaWalker) errorSchemaPush(ctx *schemaContext, schemaPath, instancePath string, schema, value any) bool {
	return ctx.push() && w.errorSchema(ctx, schemaPath, instancePath, schema, value) && ctx.pop()
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// errorSchema is ErrorSchema: every keyword is evaluated (a failure does not hide the others), then refine.
func (w *schemaWalker) errorSchema(ctx *schemaContext, schemaPath, instancePath string, schema, value any) bool {
	if ctx.atCapacity() {
		return false
	}
	if b, ok := isBoolean(schema); ok {
		if b {
			return true
		}
		return ctx.addError(schemaError{Keyword: "boolean", SchemaPath: schemaPath, InstancePath: instancePath, Params: params()})
	}
	s, ok := asObject(schema)
	if !ok {
		return true
	}
	has := func(k string) bool { v, ok := s.vals[k]; return ok && !isUndefined(v) }
	result := true
	and := func(r bool) { result = result && r }
	if has("type") {
		and(w.errorType(ctx, schemaPath, instancePath, s, value))
	}
	if obj, isObj := asObject(value); isObj {
		ok := true
		if _, hasReq := asArray(s.vals["required"]); hasReq {
			ok = w.errorRequired(ctx, schemaPath, instancePath, s, obj) && ok
		}
		if has("additionalProperties") {
			ok = w.errorAdditionalProperties(ctx, schemaPath, instancePath, s, obj) && ok
		}
		if _, o := asObject(s.vals["dependencies"]); o {
			ok = w.errorDependencies(ctx, schemaPath, instancePath, s, obj) && ok
		}
		if _, o := asObject(s.vals["dependentRequired"]); o {
			ok = w.errorDependentRequired(ctx, schemaPath, instancePath, s, obj) && ok
		}
		if _, o := asObject(s.vals["dependentSchemas"]); o {
			ok = w.errorDependentSchemas(ctx, schemaPath, instancePath, s, obj) && ok
		}
		if _, o := asObject(s.vals["patternProperties"]); o {
			ok = w.errorPatternProperties(ctx, schemaPath, instancePath, s, obj) && ok
		}
		if _, o := asObject(s.vals["properties"]); o {
			ok = w.errorProperties(ctx, schemaPath, instancePath, s, obj) && ok
		}
		if has("propertyNames") {
			ok = w.errorPropertyNames(ctx, schemaPath, instancePath, s, obj) && ok
		}
		if n, o := isFiniteNumber(s.vals["minProperties"]); o {
			if float64(len(obj.order())) < n {
				ok = ctx.addError(schemaError{Keyword: "minProperties", SchemaPath: schemaPath, InstancePath: instancePath, Params: params("limit", s.vals["minProperties"])}) && ok
			}
		}
		if n, o := isFiniteNumber(s.vals["maxProperties"]); o {
			if float64(len(obj.order())) > n {
				ok = ctx.addError(schemaError{Keyword: "maxProperties", SchemaPath: schemaPath, InstancePath: instancePath, Params: params("limit", s.vals["maxProperties"])}) && ok
			}
		}
		and(ok)
	}
	if arr, isArr := asArray(value); isArr {
		ok := true
		if _, o := asArray(s.vals["items"]); o && has("additionalItems") {
			ok = w.errorAdditionalItems(ctx, schemaPath, instancePath, s, arr) && ok
		}
		if has("contains") {
			ok = w.errorContains(ctx, schemaPath, instancePath, s, arr) && ok
		}
		if has("items") {
			ok = w.errorItems(ctx, schemaPath, instancePath, s, arr) && ok
		}
		if _, o := isFiniteNumber(s.vals["maxContains"]); o && has("contains") {
			ok = w.errorMaxContains(ctx, schemaPath, instancePath, s, arr) && ok
		}
		if n, o := isFiniteNumber(s.vals["maxItems"]); o && float64(len(arr)) > n {
			ok = ctx.addError(schemaError{Keyword: "maxItems", SchemaPath: schemaPath, InstancePath: instancePath, Params: params("limit", s.vals["maxItems"])}) && ok
		}
		if _, o := isFiniteNumber(s.vals["minContains"]); o && has("contains") {
			ok = w.errorMinContains(ctx, schemaPath, instancePath, s, arr) && ok
		}
		if n, o := isFiniteNumber(s.vals["minItems"]); o && float64(len(arr)) < n {
			ok = ctx.addError(schemaError{Keyword: "minItems", SchemaPath: schemaPath, InstancePath: instancePath, Params: params("limit", s.vals["minItems"])}) && ok
		}
		if _, o := asArray(s.vals["prefixItems"]); o {
			ok = w.errorPrefixItems(ctx, schemaPath, instancePath, s, arr) && ok
		}
		if u, o := s.vals["uniqueItems"].(bool); o && u {
			ok = w.errorUniqueItems(ctx, schemaPath, instancePath, arr) && ok
		}
		and(ok)
	}
	if str, isStr := value.(string); isStr {
		ok := true
		if n, o := isFiniteNumber(s.vals["maxLength"]); o && graphemeCount(str) > n {
			ok = ctx.addError(schemaError{Keyword: "maxLength", SchemaPath: schemaPath, InstancePath: instancePath, Params: params("limit", s.vals["maxLength"])}) && ok
		}
		if n, o := isFiniteNumber(s.vals["minLength"]); o && graphemeCount(str) < n {
			ok = ctx.addError(schemaError{Keyword: "minLength", SchemaPath: schemaPath, InstancePath: instancePath, Params: params("limit", s.vals["minLength"])}) && ok
		}
		if f, o := s.vals["format"].(string); o && !testFormat(f, str) {
			ok = ctx.addError(schemaError{Keyword: "format", SchemaPath: schemaPath, InstancePath: instancePath, Params: params("format", f)}) && ok
		}
		if p, o := s.vals["pattern"].(string); o && !w.regexp(p).MatchString(str) {
			ok = ctx.addError(schemaError{Keyword: "pattern", SchemaPath: schemaPath, InstancePath: instancePath, Params: params("pattern", p)}) && ok
		}
		and(ok)
	}
	if n, isNum := isFiniteNumber(value); isNum {
		ok := true
		cmp := func(keyword, comparison string, pass func(limit float64) bool) {
			if lim, o := isFiniteNumber(s.vals[keyword]); o && !pass(lim) {
				ok = ctx.addError(schemaError{Keyword: keyword, SchemaPath: schemaPath, InstancePath: instancePath, Params: params("comparison", comparison, "limit", s.vals[keyword])}) && ok
			}
		}
		cmp("exclusiveMaximum", "<", func(l float64) bool { return n < l })
		cmp("exclusiveMinimum", ">", func(l float64) bool { return n > l })
		cmp("maximum", "<=", func(l float64) bool { return n <= l })
		cmp("minimum", ">=", func(l float64) bool { return n >= l })
		if d, o := isFiniteNumber(s.vals["multipleOf"]); o && !isMultipleOf(n, d) {
			ok = ctx.addError(schemaError{Keyword: "multipleOf", SchemaPath: schemaPath, InstancePath: instancePath, Params: params("multipleOf", s.vals["multipleOf"])}) && ok
		}
		and(ok)
	}
	if ref, o := s.vals["$ref"].(string); o {
		and(w.errorRef(ctx, instancePath, ref, value))
	}
	if has("const") {
		if !schemaConstEqual(value, s.vals["const"]) {
			and(ctx.addError(schemaError{Keyword: "const", SchemaPath: schemaPath, InstancePath: instancePath, Params: params("allowedValue", s.vals["const"])}))
		}
	}
	if opts, o := asArray(s.vals["enum"]); o {
		found := false
		for _, opt := range opts {
			if schemaConstEqual(value, opt) {
				found = true
				break
			}
		}
		if !found {
			and(ctx.addError(schemaError{Keyword: "enum", SchemaPath: schemaPath, InstancePath: instancePath, Params: params("allowedValues", s.vals["enum"])}))
		}
	}
	if has("if") {
		and(w.errorIf(ctx, schemaPath, instancePath, s, value))
	}
	if has("not") {
		nc := newSchemaContext()
		if w.errorSchemaQuiet(nc, s.vals["not"], value) {
			and(ctx.addError(schemaError{Keyword: "not", SchemaPath: schemaPath, InstancePath: instancePath, Params: params()}))
		} else {
			ctx.merge(nc)
		}
	}
	if list, o := asArray(s.vals["allOf"]); o {
		and(w.errorAllOf(ctx, schemaPath, instancePath, list, value))
	}
	if list, o := asArray(s.vals["anyOf"]); o {
		and(w.errorAnyOf(ctx, schemaPath, instancePath, list, value))
	}
	if list, o := asArray(s.vals["oneOf"]); o {
		and(w.errorOneOf(ctx, schemaPath, instancePath, list, value))
	}
	if has("unevaluatedItems") {
		if arr, isArr := asArray(value); isArr {
			and(w.errorUnevaluatedItems(ctx, schemaPath, instancePath, s, arr))
		}
	}
	if has("unevaluatedProperties") {
		if obj, isObj := asObject(value); isObj {
			and(w.errorUnevaluatedProperties(ctx, schemaPath, instancePath, s, obj))
		}
	}
	return result
}

// errorSchemaQuiet is the Check of the original (no error output wanted): the schema's verdict.
func (w *schemaWalker) errorSchemaQuiet(ctx *schemaContext, schema, value any) bool {
	return w.errorSchema(ctx, "#", "", schema, value)
}

func (w *schemaWalker) errorType(ctx *schemaContext, sp, ip string, s *jsObject, value any) bool {
	return w.typeOK(s.vals["type"], value) || ctx.addError(schemaError{Keyword: "type", SchemaPath: sp, InstancePath: ip, Params: params("type", s.vals["type"])})
}

func hasKey(o *jsObject, k string) bool { _, ok := o.vals[k]; return ok }

func (w *schemaWalker) errorRequired(ctx *schemaContext, sp, ip string, s, obj *jsObject) bool {
	missing := []any{}
	for _, k := range anyList(s.vals["required"]) {
		if ks, ok := k.(string); ok && !hasKey(obj, ks) {
			missing = append(missing, ks)
		}
	}
	return len(missing) == 0 || ctx.addError(schemaError{Keyword: "required", SchemaPath: sp, InstancePath: ip, Params: params("requiredProperties", missing)})
}

func (w *schemaWalker) propertiesPattern(s *jsObject) *regexp.Regexp {
	var parts []string
	if props, ok := asObject(s.vals["properties"]); ok {
		for _, k := range props.order() {
			parts = append(parts, "^"+regexp.QuoteMeta(k)+"$")
		}
	}
	if pp, ok := asObject(s.vals["patternProperties"]); ok {
		parts = append(parts, pp.order()...)
	}
	if len(parts) == 0 {
		return regexp.MustCompile(`$^`)
	}
	return w.regexp(strings.Join(parts, "|"))
}

func (w *schemaWalker) errorAdditionalProperties(ctx *schemaContext, sp, ip string, s, obj *jsObject) bool {
	re := w.propertiesPattern(s)
	additional := []any{}
	for _, key := range obj.order() {
		ok := re.MatchString(key) || (w.errorSchemaPush(ctx, sp+"/additionalProperties", ip+"/"+key, s.vals["additionalProperties"], obj.vals[key]) && ctx.addKey(key))
		if !ok {
			additional = append(additional, key)
		}
	}
	return len(additional) == 0 || ctx.addError(schemaError{Keyword: "additionalProperties", SchemaPath: sp, InstancePath: ip, Params: params("additionalProperties", additional)})
}

func (w *schemaWalker) errorProperties(ctx *schemaContext, sp, ip string, s, obj *jsObject) bool {
	props, _ := asObject(s.vals["properties"])
	ok := true
	for _, key := range props.order() {
		if !hasKey(obj, key) {
			continue
		}
		r := w.errorSchemaPush(ctx, sp+"/properties/"+key, ip+"/"+key, props.vals[key], obj.vals[key]) && ctx.addKey(key)
		ok = r && ok
	}
	return ok
}

func (w *schemaWalker) errorPatternProperties(ctx *schemaContext, sp, ip string, s, obj *jsObject) bool {
	pp, _ := asObject(s.vals["patternProperties"])
	ok := true
	for _, pattern := range pp.order() {
		re := w.regexp(pattern)
		for _, key := range obj.order() {
			if !re.MatchString(key) {
				continue
			}
			r := w.errorSchemaPush(ctx, sp+"/patternProperties/"+pattern, ip+"/"+key, pp.vals[pattern], obj.vals[key]) && ctx.addKey(key)
			ok = r && ok
		}
	}
	return ok
}

func (w *schemaWalker) errorPropertyNames(ctx *schemaContext, sp, ip string, s, obj *jsObject) bool {
	bad := []any{}
	for _, key := range obj.order() {
		if !w.errorSchema(ctx, sp+"/propertyNames", ip+"/"+key, s.vals["propertyNames"], key) {
			bad = append(bad, key)
		}
	}
	return len(bad) == 0 || ctx.addError(schemaError{Keyword: "propertyNames", SchemaPath: sp, InstancePath: ip, Params: params("propertyNames", bad)})
}

func (w *schemaWalker) errorDependentRequired(ctx *schemaContext, sp, ip string, s, obj *jsObject) bool {
	if len(obj.order()) == 0 {
		return true
	}
	dr, _ := asObject(s.vals["dependentRequired"])
	ok := true
	for _, key := range dr.order() {
		if !hasKey(obj, key) {
			continue
		}
		deps := anyList(dr.vals[key])
		for _, d := range deps {
			ds, _ := d.(string)
			if !hasKey(obj, ds) {
				ok = ctx.addError(schemaError{Keyword: "dependentRequired", SchemaPath: sp, InstancePath: ip, Params: params("property", key, "dependencies", deps)}) && ok
			}
		}
	}
	return ok
}

func (w *schemaWalker) errorDependentSchemas(ctx *schemaContext, sp, ip string, s, obj *jsObject) bool {
	if len(obj.order()) == 0 {
		return true
	}
	ds, _ := asObject(s.vals["dependentSchemas"])
	ok := true
	for _, key := range ds.order() {
		if !hasKey(obj, key) {
			continue
		}
		ok = w.errorSchema(ctx, sp+"/dependentSchemas/"+key, ip, ds.vals[key], obj) && ok
	}
	return ok
}

func (w *schemaWalker) errorDependencies(ctx *schemaContext, sp, ip string, s, obj *jsObject) bool {
	if len(obj.order()) == 0 {
		return true
	}
	deps, _ := asObject(s.vals["dependencies"])
	ok := true
	for _, key := range deps.order() {
		if !hasKey(obj, key) {
			continue
		}
		if list, isArr := asArray(deps.vals[key]); isArr {
			for _, d := range list {
				ds, _ := d.(string)
				if !hasKey(obj, ds) {
					ok = ctx.addError(schemaError{Keyword: "dependencies", SchemaPath: sp, InstancePath: ip, Params: params("property", key, "dependencies", list)})
					break
				}
			}
		} else {
			ok = w.errorSchema(ctx, sp+"/dependencies/"+key, ip, deps.vals[key], obj) && ok
		}
	}
	return ok
}

func (w *schemaWalker) errorItems(ctx *schemaContext, sp, ip string, s *jsObject, arr []any) bool {
	if sized, isSized := asArray(s.vals["items"]); isSized {
		ok := true
		for i, sub := range sized {
			if len(arr) <= i {
				continue
			}
			r := w.errorSchemaPush(ctx, sp+"/items/"+strconv.Itoa(i), ip+"/"+strconv.Itoa(i), sub, arr[i]) && ctx.addIndex(i)
			ok = r && ok
		}
		return ok
	}
	offset := 0
	if pi, ok := asArray(s.vals["prefixItems"]); ok {
		offset = len(pi)
	}
	ok := true
	for i := offset; i < len(arr); i++ {
		r := w.errorSchemaPush(ctx, sp+"/items", ip+"/"+strconv.Itoa(i), s.vals["items"], arr[i]) && ctx.addIndex(i)
		ok = r && ok
	}
	return ok
}

func (w *schemaWalker) errorPrefixItems(ctx *schemaContext, sp, ip string, s *jsObject, arr []any) bool {
	if len(arr) == 0 {
		return true
	}
	ok := true
	for i, sub := range anyList(s.vals["prefixItems"]) {
		if len(arr) <= i {
			continue
		}
		r := w.errorSchemaPush(ctx, sp+"/prefixItems/"+strconv.Itoa(i), ip+"/"+strconv.Itoa(i), sub, arr[i]) && ctx.addIndex(i)
		ok = r && ok
	}
	return ok
}

func (w *schemaWalker) errorAdditionalItems(ctx *schemaContext, sp, ip string, s *jsObject, arr []any) bool {
	n := len(anyList(s.vals["items"]))
	for i, item := range arr {
		if i < n {
			continue
		}
		if !(w.errorSchemaPush(ctx, sp+"/additionalItems", ip+"/"+strconv.Itoa(i), s.vals["additionalItems"], item) && ctx.addIndex(i)) {
			return false
		}
	}
	return true
}

func (w *schemaWalker) checkContains(ctx *schemaContext, s *jsObject, arr []any) bool {
	if len(arr) == 0 {
		return false
	}
	some := false
	for i, item := range arr {
		if w.errorSchemaQuiet(newSchemaContext(), s.vals["contains"], item) && ctx.addIndex(i) {
			some = true
		}
	}
	return some
}

func (w *schemaWalker) countContains(s *jsObject, arr []any) float64 {
	n := 0
	for _, item := range arr {
		if w.errorSchemaQuiet(newSchemaContext(), s.vals["contains"], item) {
			n++
		}
	}
	return float64(n)
}

func (w *schemaWalker) errorContains(ctx *schemaContext, sp, ip string, s *jsObject, arr []any) bool {
	return w.checkContains(ctx, s, arr) || ctx.addError(schemaError{Keyword: "contains", SchemaPath: sp, InstancePath: ip, Params: params("minContains", 1.0)})
}

func (w *schemaWalker) errorMinContains(ctx *schemaContext, sp, ip string, s *jsObject, arr []any) bool {
	min, _ := isFiniteNumber(s.vals["minContains"])
	return w.countContains(s, arr) >= min || ctx.addError(schemaError{Keyword: "contains", SchemaPath: sp, InstancePath: ip, Params: params("minContains", s.vals["minContains"])})
}

func (w *schemaWalker) errorMaxContains(ctx *schemaContext, sp, ip string, s *jsObject, arr []any) bool {
	max, _ := isFiniteNumber(s.vals["maxContains"])
	var min any = 1.0
	if _, ok := isFiniteNumber(s.vals["minContains"]); ok {
		min = s.vals["minContains"]
	}
	return w.countContains(s, arr) <= max || ctx.addError(schemaError{Keyword: "contains", SchemaPath: sp, InstancePath: ip, Params: params("minContains", min, "maxContains", s.vals["maxContains"])})
}

func (w *schemaWalker) errorUniqueItems(ctx *schemaContext, sp, ip string, arr []any) bool {
	seen := map[string]bool{}
	dups := []any{}
	for i, v := range arr {
		h := canonicalJSON(v)
		if seen[h] {
			dups = append(dups, float64(i))
		} else {
			seen[h] = true
		}
	}
	return len(dups) == 0 || ctx.addError(schemaError{Keyword: "uniqueItems", SchemaPath: sp, InstancePath: ip, Params: params("duplicateItems", dups)})
}

// canonicalJSON is a structural key of a value: objects with their keys sorted.
func canonicalJSON(v any) string {
	switch x := v.(type) {
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = canonicalJSON(e)
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	if o, ok := asObject(v); ok {
		keys := append([]string{}, o.order()...)
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = marshalJSON(k, "") + ":" + canonicalJSON(o.vals[k])
		}
		return "{" + strings.Join(parts, ",") + "}"
	}
	return marshalJSON(v, "")
}

func schemaConstEqual(a, b any) bool {
	if x, ok := asArray(a); ok {
		y, ok := asArray(b)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !schemaConstEqual(x[i], y[i]) {
				return false
			}
		}
		return true
	}
	if x, ok := asObject(a); ok {
		y, ok := asObject(b)
		if !ok {
			return false
		}
		kx := x.order()
		if len(kx) != len(y.order()) {
			return false
		}
		for _, k := range kx {
			if !schemaConstEqual(x.vals[k], y.vals[k]) {
				return false
			}
		}
		return true
	}
	if _, ok := asArray(b); ok {
		return false
	}
	if _, ok := asObject(b); ok {
		return false
	}
	if fa, oka := asNumber(a); oka {
		fb, okb := asNumber(b)
		return okb && fa == fb
	}
	return a == b
}

func isMultipleOf(dividend, divisor float64) bool {
	const tolerance = 1e-10
	if dividend == math.Trunc(dividend) {
		if inv := 1 / divisor; math.Mod(inv, 1) == 0 {
			return true
		}
	}
	mod := math.Mod(dividend, divisor)
	return math.Min(math.Abs(mod), math.Min(math.Abs(mod-divisor), math.Abs(mod+divisor))) < tolerance
}

func (w *schemaWalker) errorIf(ctx *schemaContext, sp, ip string, s *jsObject, value any) bool {
	var thenS, elseS any = true, true
	if v, ok := s.vals["then"]; ok && !isUndefined(v) {
		thenS = v
	}
	if v, ok := s.vals["else"]; ok && !isUndefined(v) {
		elseS = v
	}
	trueCtx := newSchemaContext()
	var isIf bool
	if w.errorSchema(trueCtx, sp+"/if", ip, s.vals["if"], value) {
		isIf = w.errorSchema(trueCtx, sp+"/then", ip, thenS, value) || ctx.addError(schemaError{Keyword: "if", SchemaPath: sp, InstancePath: ip, Params: params("failingKeyword", "then")})
	} else {
		isIf = w.errorSchema(ctx, sp+"/else", ip, elseS, value) || ctx.addError(schemaError{Keyword: "if", SchemaPath: sp, InstancePath: ip, Params: params("failingKeyword", "else")})
	}
	if isIf {
		ctx.merge(trueCtx)
	}
	return isIf
}

func (w *schemaWalker) errorAllOf(ctx *schemaContext, sp, ip string, list []any, value any) bool {
	var failed []*schemaContext
	var passed []*schemaContext
	for i, sub := range list {
		nc := newSchemaContext()
		if w.errorSchema(nc, sp+"/allOf/"+strconv.Itoa(i), ip, sub, value) {
			passed = append(passed, nc)
		} else {
			failed = append(failed, nc)
		}
	}
	ok := len(passed) == len(list) && ctx.merge(passed...)
	if !ok {
		for _, f := range failed {
			for _, e := range f.errors {
				ctx.addError(e)
			}
		}
	}
	return ok
}

func (w *schemaWalker) errorAnyOf(ctx *schemaContext, sp, ip string, list []any, value any) bool {
	var failed, passed []*schemaContext
	for i, sub := range list {
		nc := newSchemaContext()
		if w.errorSchema(nc, sp+"/anyOf/"+strconv.Itoa(i), ip, sub, value) {
			passed = append(passed, nc)
		} else {
			failed = append(failed, nc)
		}
	}
	ok := len(passed) > 0 && ctx.merge(passed...)
	if !ok {
		for _, f := range failed {
			for _, e := range f.errors {
				ctx.addError(e)
			}
		}
	}
	return ok || ctx.addError(schemaError{Keyword: "anyOf", SchemaPath: sp, InstancePath: ip, Params: params()})
}

func (w *schemaWalker) errorOneOf(ctx *schemaContext, sp, ip string, list []any, value any) bool {
	var failed, passed []*schemaContext
	passing := []any{}
	for i, sub := range list {
		nc := newSchemaContext()
		if w.errorSchema(nc, sp+"/oneOf/"+strconv.Itoa(i), ip, sub, value) {
			passing = append(passing, float64(i))
			passed = append(passed, nc)
		} else {
			failed = append(failed, nc)
		}
	}
	ok := len(passed) == 1 && ctx.merge(passed...)
	if !ok && len(passing) == 0 {
		for _, f := range failed {
			for _, e := range f.errors {
				ctx.addError(e)
			}
		}
	}
	return ok || ctx.addError(schemaError{Keyword: "oneOf", SchemaPath: sp, InstancePath: ip, Params: params("passingSchemas", passing)})
}

func (w *schemaWalker) errorUnevaluatedProperties(ctx *schemaContext, sp, ip string, s, obj *jsObject) bool {
	keys := ctx.top().keys
	bad := []any{}
	for _, key := range obj.order() {
		nc := newSchemaContext()
		ok := keys[key] || (w.errorSchema(nc, sp, ip, s.vals["unevaluatedProperties"], obj.vals[key]) && ctx.addKey(key))
		if !ok {
			bad = append(bad, key)
		}
	}
	return len(bad) == 0 || ctx.addError(schemaError{Keyword: "unevaluatedProperties", SchemaPath: sp, InstancePath: ip, Params: params("unevaluatedProperties", bad)})
}

func (w *schemaWalker) errorUnevaluatedItems(ctx *schemaContext, sp, ip string, s *jsObject, arr []any) bool {
	indices := ctx.top().indices
	bad := []any{}
	for i, item := range arr {
		nc := newSchemaContext()
		ok := (indices[i] || w.errorSchema(nc, sp, ip, s.vals["unevaluatedItems"], item)) && ctx.addIndex(i)
		if !ok {
			bad = append(bad, float64(i))
		}
	}
	return len(bad) == 0 || ctx.addError(schemaError{Keyword: "unevaluatedItems", SchemaPath: sp, InstancePath: ip, Params: params("unevaluatedItems", bad)})
}

// errorRef validates against the target of a local $ref (a JSON pointer or anchor into the root schema); a reference that does
// not resolve validates against the false schema, as in TypeBox.
func (w *schemaWalker) errorRef(ctx *schemaContext, ip, ref string, value any) bool {
	target, ok := w.resolveRef(ref)
	if !ok {
		target = false
	}
	nc := newSchemaContext()
	result := w.errorSchema(nc, "#", ip, target, value)
	if result {
		ctx.merge(nc)
	} else {
		for _, e := range nc.errors {
			ctx.addError(e)
		}
	}
	return result
}

func (w *schemaWalker) resolveRef(ref string) (any, bool) {
	if ref == "#" {
		return w.root, true
	}
	if strings.HasPrefix(ref, "#/") {
		cur := w.root
		for _, enc := range strings.Split(ref[2:], "/") {
			dec, err := url.PathUnescape(enc)
			if err != nil {
				dec = enc
			}
			key := strings.ReplaceAll(strings.ReplaceAll(dec, "~1", "/"), "~0", "~")
			if arr, ok := asArray(cur); ok {
				i, err := strconv.Atoi(key)
				if err != nil || i < 0 || i >= len(arr) {
					return nil, false
				}
				cur = arr[i]
				continue
			}
			o, ok := asObject(cur)
			if !ok || !hasKey(o, key) {
				return nil, false
			}
			cur = o.vals[key]
		}
		return cur, true
	}
	if strings.HasPrefix(ref, "#") {
		if t := findAnchor(w.root, ref[1:]); t != nil {
			return t, true
		}
	}
	if t := findSchemaID(w.root, ref); t != nil {
		return t, true
	}
	return nil, false
}

func walkSchemas(node any, visit func(*jsObject) bool) any {
	switch x := node.(type) {
	case []any:
		for _, e := range x {
			if r := walkSchemas(e, visit); r != nil {
				return r
			}
		}
	default:
		if o, ok := asObject(node); ok {
			if visit(o) {
				return o
			}
			for _, k := range o.order() {
				if r := walkSchemas(o.vals[k], visit); r != nil {
					return r
				}
			}
		}
	}
	return nil
}

func findAnchor(root any, name string) any {
	return walkSchemas(root, func(o *jsObject) bool { return o.vals["$anchor"] == name })
}

func findSchemaID(root any, id string) any {
	return walkSchemas(root, func(o *jsObject) bool { return o.vals["$id"] == id })
}

// graphemeCount is TypeBox's grapheme counter (combining marks, variation selectors, ZWJ sequences and regional-indicator pairs
// extend a cluster), over UTF-16 code units.
func graphemeCount(s string) float64 {
	u := utf16.Encode([]rune(s))
	codePointAt := func(i int) int {
		c := int(u[i])
		if c >= 0xD800 && c <= 0xDBFF && i+1 < len(u) {
			if lo := int(u[i+1]); lo >= 0xDC00 && lo <= 0xDFFF {
				return (c-0xD800)<<10 + (lo - 0xDC00) + 0x10000
			}
		}
		return c
	}
	cpLen := func(cp int) int {
		if cp > 0xFFFF {
			return 2
		}
		return 1
	}
	between := func(v, lo, hi int) bool { return v >= lo && v <= hi }
	isMod := func(cp int) bool {
		return between(cp, 0x0300, 0x036F) || between(cp, 0x1AB0, 0x1AFF) || between(cp, 0x1DC0, 0x1DFF) || between(cp, 0xFE20, 0xFE2F) || between(cp, 0xFE00, 0xFE0F)
	}
	consume := func(i int) int {
		for i < len(u) {
			cp := codePointAt(i)
			if !isMod(cp) {
				break
			}
			i += cpLen(cp)
		}
		return i
	}
	count, i := 0, 0
	for i < len(u) {
		start := codePointAt(i)
		end := consume(i + cpLen(start))
		for end < len(u)-1 && codePointAt(end) == 0x200D {
			next := codePointAt(end + 1)
			end += 1 + cpLen(next)
			end = consume(end)
		}
		if between(start, 0x1F1E6, 0x1F1FF) && end < len(u) && between(codePointAt(end), 0x1F1E6, 0x1F1FF) {
			end += cpLen(codePointAt(end))
		}
		i = end
		count++
	}
	return float64(count)
}

var (
	uuidFormat     = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	dateFormat     = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})$`)
	timeFormat     = regexp.MustCompile(`(?i)^(\d{2}):(\d{2}):(\d{2})(\.\d+)?(z|[+-]\d{2}:\d{2})$`)
	hostnameFormat = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)
)

// testFormat is a format check for the common JSON Schema formats; a format the port does not know passes (TypeBox's registry
// would fail an unregistered name; the original's schemas use the common ones).
func testFormat(format, v string) bool {
	switch format {
	case "date":
		m := dateFormat.FindStringSubmatch(v)
		if m == nil {
			return false
		}
		_, err := time.Parse("2006-01-02", v)
		return err == nil
	case "time":
		return timeFormat.MatchString(v)
	case "date-time":
		_, err := time.Parse(time.RFC3339Nano, strings.ToUpper(strings.Replace(v, " ", "T", 0)))
		return err == nil
	case "email":
		a, err := mail.ParseAddress(v)
		return err == nil && a.Address == v
	case "uuid":
		return uuidFormat.MatchString(v)
	case "ipv4":
		ip := net.ParseIP(v)
		return ip != nil && ip.To4() != nil && !strings.Contains(v, ":")
	case "ipv6":
		ip := net.ParseIP(v)
		return ip != nil && strings.Contains(v, ":")
	case "hostname":
		return len(v) <= 253 && hostnameFormat.MatchString(v)
	case "uri", "url":
		u, err := url.Parse(v)
		return err == nil && u.Scheme != ""
	case "uri-reference":
		_, err := url.Parse(v)
		return err == nil
	case "regex":
		_, err := regexp.Compile(v)
		return err == nil
	case "json-pointer":
		return v == "" || strings.HasPrefix(v, "/")
	}
	return true
}
