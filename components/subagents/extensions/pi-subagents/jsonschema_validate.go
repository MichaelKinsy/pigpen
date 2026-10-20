package pi_subagents

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode/utf16"
)

// A JSON Schema validator for the schemas a workflow child or a collected fan-out carries (outputSchema, gate.schema, collect
// outputSchema): the structural keywords of drafts 7 to 2020-12, local `$ref`s, and no network. It reports ajv-style messages
// ("/items/0/path must be string"). Keywords it does not know are ignored, as JSON Schema says; `format` is annotation only.

// schemaViolations returns why value does not satisfy schema: nothing when it does.
func schemaViolations(schema any, value any) []string {
	v := &schemaValidator{root: schema}
	v.check(schema, value, "", 0)
	return v.errs
}

type schemaValidator struct {
	root any
	errs []string
}

func (v *schemaValidator) fail(path, format string, a ...any) {
	p := path
	if p == "" {
		p = "value"
	}
	v.errs = append(v.errs, p+" "+fmt.Sprintf(format, a...))
}

// matches reports whether value satisfies schema without recording the violations.
func (v *schemaValidator) matches(schema, value any, path string, depth int) bool {
	sub := &schemaValidator{root: v.root}
	sub.check(schema, value, path, depth)
	return len(sub.errs) == 0
}

func schemaType(value any) string {
	switch x := value.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case float64:
		if x == math.Trunc(x) {
			return "integer"
		}
		return "number"
	case []any:
		return "array"
	case *jsObject:
		return "object"
	}
	return "undefined"
}

func typeMatches(want, value any) bool {
	t := schemaType(value)
	w, _ := want.(string)
	return w == t || (w == "number" && t == "integer")
}

func jsonEqual(a, b any) bool { return stableJSON(a) == stableJSON(b) }

func (v *schemaValidator) resolveRef(ref string) (any, bool) {
	if ref == "#" {
		return v.root, true
	}
	if !strings.HasPrefix(ref, "#/") {
		return nil, false
	}
	cur := v.root
	for _, seg := range strings.Split(ref[2:], "/") {
		seg = strings.ReplaceAll(strings.ReplaceAll(seg, "~1", "/"), "~0", "~")
		switch c := cur.(type) {
		case *jsObject:
			next, ok := c.vals[seg]
			if !ok {
				return nil, false
			}
			cur = next
		case []any:
			var idx int
			if _, err := fmt.Sscanf(seg, "%d", &idx); err != nil || idx < 0 || idx >= len(c) {
				return nil, false
			}
			cur = c[idx]
		default:
			return nil, false
		}
	}
	return cur, true
}

func schemaNumber(o *jsObject, k string) (float64, bool) {
	f, ok := o.vals[k].(float64)
	return f, ok
}

func jsLength(s string) int { return len(utf16.Encode([]rune(s))) }

func (v *schemaValidator) check(schema, value any, path string, depth int) {
	if depth > 64 {
		return
	}
	switch s := schema.(type) {
	case bool:
		if !s {
			v.fail(path, "is not allowed")
		}
		return
	case *jsObject:
		v.checkObject(s, value, path, depth)
	}
}

func (v *schemaValidator) checkObject(s *jsObject, value any, path string, depth int) {
	if ref, ok := s.vals["$ref"].(string); ok {
		if target, found := v.resolveRef(ref); found {
			v.check(target, value, path, depth+1)
		}
	}
	if t, ok := s.vals["type"]; ok {
		okType := false
		var names []string
		switch tt := t.(type) {
		case string:
			okType, names = typeMatches(tt, value), []string{tt}
		case []any:
			for _, e := range tt {
				okType = okType || typeMatches(e, value)
				if n, ok := e.(string); ok {
					names = append(names, n)
				}
			}
		}
		if !okType {
			v.fail(path, "must be %s", strings.Join(names, ","))
			return
		}
	}
	if e, ok := s.vals["enum"].([]any); ok {
		found := false
		for _, c := range e {
			found = found || jsonEqual(c, value)
		}
		if !found {
			v.fail(path, "must be equal to one of the allowed values")
		}
	}
	if c, ok := s.vals["const"]; ok && !jsonEqual(c, value) {
		v.fail(path, "must be equal to constant")
	}
	switch x := value.(type) {
	case string:
		if n, ok := schemaNumber(s, "minLength"); ok && float64(utf16Count(x)) < n {
			v.fail(path, "must NOT have fewer than %s characters", jsNumber(n))
		}
		if n, ok := schemaNumber(s, "maxLength"); ok && float64(utf16Count(x)) > n {
			v.fail(path, "must NOT have more than %s characters", jsNumber(n))
		}
		if p, ok := s.vals["pattern"].(string); ok {
			if re, err := regexp.Compile(p); err == nil && !re.MatchString(x) {
				v.fail(path, `must match pattern "%s"`, p)
			}
		}
	case float64:
		if n, ok := schemaNumber(s, "minimum"); ok && x < n {
			v.fail(path, "must be >= %s", jsNumber(n))
		}
		if n, ok := schemaNumber(s, "maximum"); ok && x > n {
			v.fail(path, "must be <= %s", jsNumber(n))
		}
		if n, ok := schemaNumber(s, "exclusiveMinimum"); ok && x <= n {
			v.fail(path, "must be > %s", jsNumber(n))
		}
		if n, ok := schemaNumber(s, "exclusiveMaximum"); ok && x >= n {
			v.fail(path, "must be < %s", jsNumber(n))
		}
		if n, ok := schemaNumber(s, "multipleOf"); ok && n > 0 && math.Abs(x/n-math.Round(x/n)) > 1e-9 {
			v.fail(path, "must be multiple of %s", jsNumber(n))
		}
	case []any:
		if n, ok := schemaNumber(s, "minItems"); ok && float64(len(x)) < n {
			v.fail(path, "must NOT have fewer than %s items", jsNumber(n))
		}
		if n, ok := schemaNumber(s, "maxItems"); ok && float64(len(x)) > n {
			v.fail(path, "must NOT have more than %s items", jsNumber(n))
		}
		if u, ok := s.vals["uniqueItems"].(bool); ok && u {
			for i := range x {
				for j := i + 1; j < len(x); j++ {
					if jsonEqual(x[i], x[j]) {
						v.fail(path, "must NOT have duplicate items (items ## %d and %d are identical)", j, i)
					}
				}
			}
		}
		prefix := 0
		if pi, ok := s.vals["prefixItems"].([]any); ok {
			prefix = len(pi)
			for i, sub := range pi {
				if i < len(x) {
					v.check(sub, x[i], fmt.Sprintf("%s/%d", path, i), depth+1)
				}
			}
		}
		switch items := s.vals["items"].(type) {
		case []any: // draft-07 tuple form
			for i, sub := range items {
				if i < len(x) {
					v.check(sub, x[i], fmt.Sprintf("%s/%d", path, i), depth+1)
				}
			}
		case *jsObject, bool:
			for i := prefix; i < len(x); i++ {
				v.check(items, x[i], fmt.Sprintf("%s/%d", path, i), depth+1)
			}
		}
		if c, ok := s.vals["contains"]; ok {
			found := false
			for i := range x {
				found = found || v.matches(c, x[i], path, depth+1)
			}
			if !found {
				v.fail(path, "must contain at least 1 valid item(s)")
			}
		}
	case *jsObject:
		keys := x.order()
		if n, ok := schemaNumber(s, "minProperties"); ok && float64(len(keys)) < n {
			v.fail(path, "must NOT have fewer than %s properties", jsNumber(n))
		}
		if n, ok := schemaNumber(s, "maxProperties"); ok && float64(len(keys)) > n {
			v.fail(path, "must NOT have more than %s properties", jsNumber(n))
		}
		if req, ok := s.vals["required"].([]any); ok {
			for _, r := range req {
				if name, ok := r.(string); ok {
					if _, has := x.vals[name]; !has {
						v.fail(path, "must have required property '%s'", name)
					}
				}
			}
		}
		props := s.obj("properties")
		patterns := s.obj("patternProperties")
		for _, k := range keys {
			covered := false
			if props != nil {
				if sub, ok := props.vals[k]; ok {
					covered = true
					v.check(sub, x.vals[k], path+"/"+escapePointerSegment(k), depth+1)
				}
			}
			if patterns != nil {
				for _, p := range patterns.order() {
					if re, err := regexp.Compile(p); err == nil && re.MatchString(k) {
						covered = true
						v.check(patterns.vals[p], x.vals[k], path+"/"+escapePointerSegment(k), depth+1)
					}
				}
			}
			if !covered {
				switch ap := s.vals["additionalProperties"].(type) {
				case bool:
					if !ap {
						v.fail(path, "must NOT have additional properties")
					}
				case *jsObject:
					v.check(ap, x.vals[k], path+"/"+escapePointerSegment(k), depth+1)
				}
			}
		}
		if dep := s.obj("dependentRequired"); dep != nil {
			for _, k := range dep.order() {
				if _, has := x.vals[k]; !has {
					continue
				}
				if need, ok := dep.vals[k].([]any); ok {
					for _, n := range need {
						if name, ok := n.(string); ok {
							if _, has := x.vals[name]; !has {
								v.fail(path, "must have property %s when property %s is present", name, k)
							}
						}
					}
				}
			}
		}
	}
	if all, ok := s.vals["allOf"].([]any); ok {
		for _, sub := range all {
			v.check(sub, value, path, depth+1)
		}
	}
	if any_, ok := s.vals["anyOf"].([]any); ok {
		matched := false
		for _, sub := range any_ {
			matched = matched || v.matches(sub, value, path, depth+1)
		}
		if !matched {
			v.fail(path, "must match a schema in anyOf")
		}
	}
	if one, ok := s.vals["oneOf"].([]any); ok {
		n := 0
		for _, sub := range one {
			if v.matches(sub, value, path, depth+1) {
				n++
			}
		}
		if n != 1 {
			v.fail(path, "must match exactly one schema in oneOf")
		}
	}
	if not, ok := s.vals["not"]; ok && v.matches(not, value, path, depth+1) {
		v.fail(path, "must NOT be valid")
	}
	if cond, ok := s.vals["if"]; ok {
		if v.matches(cond, value, path, depth+1) {
			if then, ok := s.vals["then"]; ok {
				v.check(then, value, path, depth+1)
			}
		} else if els, ok := s.vals["else"]; ok {
			v.check(els, value, path, depth+1)
		}
	}
}

func utf16Count(s string) int { return jsLength(s) }

func escapePointerSegment(k string) string {
	return strings.ReplaceAll(strings.ReplaceAll(k, "~", "~0"), "/", "~1")
}
