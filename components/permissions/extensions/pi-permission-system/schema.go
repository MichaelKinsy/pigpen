package pi_permission_system

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Port of the config file's reading in src/config/config-loader.ts (stripJsonComments, validateUnifiedConfig) and of the zod
// schema in src/config/config-schema.ts (unifiedConfigSchema): a file that breaks the schema anywhere is not used at all, and
// each violation is reported with the original's message (formatConfigIssues).

// stripJSONComments removes // and /* */ comments outside string literals ('...' and "..." with backslash escapes). A line
// comment keeps its newline; an unterminated comment runs to the end.
func stripJSONComments(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		switch {
		case s[i] == '/' && i+1 < len(s) && s[i+1] == '/':
			n := strings.IndexByte(s[i:], '\n')
			if n < 0 {
				return b.String()
			}
			b.WriteByte('\n')
			i += n + 1
		case s[i] == '/' && i+1 < len(s) && s[i+1] == '*':
			n := strings.Index(s[i+2:], "*/")
			if n < 0 {
				return b.String()
			}
			i += 2 + n + 2
		case s[i] == '"' || s[i] == '\'':
			quote, j, escaping := s[i], i+1, false
			for j < len(s) {
				c := s[j]
				j++
				if escaping {
					escaping = false
					continue
				}
				if c == '\\' {
					escaping = true
					continue
				}
				if c == quote {
					break
				}
			}
			b.WriteString(s[i:j])
			i = j
		default:
			b.WriteByte(s[i])
			i++
		}
	}
	return b.String()
}

// jsTypeName is the type zod names in "received ...".
func jsTypeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case []any:
		return "array"
	case *jsObject:
		return "object"
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	}
	return "unknown"
}

type schemaIssues struct{ msgs []string }

func (s *schemaIssues) at(path []string, msg string) {
	loc := "(root)"
	if len(path) > 0 {
		loc = strings.Join(path, ".")
	}
	s.msgs = append(s.msgs, "Invalid config value at '"+loc+"': "+msg)
}

func (s *schemaIssues) unrecognized(keys []string) {
	for _, k := range keys {
		s.msgs = append(s.msgs, "Unrecognized config key '"+k+"'.")
	}
}

func sub(path []string, k string) []string { return append(append([]string{}, path...), k) }

func expected(what string, v any) string {
	return "Invalid input: expected " + what + ", received " + jsTypeName(v)
}

func (s *schemaIssues) str(path []string, v any, min int) {
	t, ok := v.(string)
	switch {
	case !ok:
		s.at(path, expected("string", v))
	case min > 0 && len(utf16.Encode([]rune(t))) < min:
		s.at(path, "Too small: expected string to have >="+strconv.Itoa(min)+" characters")
	}
}

func (s *schemaIssues) boolean(path []string, v any) {
	if _, ok := v.(bool); !ok {
		s.at(path, expected("boolean", v))
	}
}

// positiveInt is z.number().int().min(1).
func (s *schemaIssues) positiveInt(path []string, v any) {
	f, ok := v.(float64)
	switch {
	case !ok, math.IsInf(f, 0):
		s.at(path, expected("number", v))
	case f != math.Trunc(f):
		s.at(path, "Invalid input: expected int, received number")
	case f < 1:
		s.at(path, "Too small: expected number to be >=1")
	}
}

func (s *schemaIssues) array(path []string, v any, each func(path []string, e any)) {
	a, ok := v.([]any)
	if !ok {
		s.at(path, expected("array", v))
		return
	}
	for i, e := range a {
		each(sub(path, strconv.Itoa(i)), e)
	}
}

// strictObject checks the known fields in their schema order, then reports the keys the schema does not name.
func (s *schemaIssues) strictObject(path []string, v any, fields []string, check func(field string, path []string, v any), required map[string]bool) {
	o, ok := v.(*jsObject)
	if !ok {
		s.at(path, expected("object", v))
		return
	}
	known := map[string]bool{}
	for _, f := range fields {
		known[f] = true
		if x, has := o.vals[f]; has {
			check(f, sub(path, f), x)
		} else if required[f] {
			s.at(sub(path, f), "Invalid input: expected string, received undefined")
		}
	}
	var extra []string
	for _, k := range o.order() {
		if !known[k] {
			extra = append(extra, k)
		}
	}
	s.unrecognized(extra)
}

var topLevelFields = []string{"$schema", "debugLog", "permissionReviewLog", "yoloMode", "doublePressToConfirm", "permissionDialogKeys",
	"promptNotifications", "forwardingTimeoutMs", "promptMaxRows", "promptFieldMaxWidth", "reviewLogFieldMaxWidth",
	"toolInputPreviewMaxLength", "toolTextSummaryMaxLength", "piInfrastructureReadPaths", "authorizerChain", "permission", "shellTools"}

var dialogKeyFields = []string{"approve", "approveSession", "approveSessionBoth", "deny", "denyWithReason"}

var notificationChannels = map[string]bool{"bell": true, "osc9": true, "osc777": true}

// permissionShapeKeys are the surfaces the schema names; every other key is a catch-all surface checked after them.
var permissionShapeKeys = []string{"*", "path", "path_read", "path_write", "external_directory", "external_directory_read",
	"external_directory_write", "bash", "mcp", "skill"}

var directionalSurfaceKeys = map[string]bool{"path_read": true, "path_write": true, "external_directory_read": true,
	"external_directory_write": true}

// validateUnifiedConfig returns the original's issue messages for a parsed config file, none when the schema accepts it.
func validateUnifiedConfig(v any) []string {
	s := &schemaIssues{}
	s.strictObject(nil, v, topLevelFields, func(field string, path []string, x any) {
		switch field {
		case "$schema":
			s.str(path, x, 0)
		case "debugLog", "permissionReviewLog", "yoloMode", "doublePressToConfirm":
			s.boolean(path, x)
		case "permissionDialogKeys":
			s.strictObject(path, x, dialogKeyFields, func(_ string, p []string, y any) { s.str(p, y, 0) }, nil)
		case "promptNotifications":
			s.array(path, x, func(p []string, e any) {
				if c, _ := e.(string); !notificationChannels[c] {
					s.at(p, `Invalid option: expected one of "bell"|"osc9"|"osc777"`)
				}
			})
		case "piInfrastructureReadPaths", "authorizerChain":
			s.array(path, x, func(p []string, e any) { s.str(p, e, 1) })
		case "permission":
			s.permission(path, x)
		case "shellTools":
			s.shellTools(path, x)
		default:
			s.positiveInt(path, x)
		}
	}, nil)
	return s.msgs
}

func (s *schemaIssues) shellTools(path []string, v any) {
	o, ok := v.(*jsObject)
	if !ok {
		s.at(path, expected("record", v))
		return
	}
	for _, k := range o.order() {
		if k == "" {
			s.at(sub(path, k), "Invalid key in record")
			continue
		}
		s.strictObject(sub(path, k), o.vals[k], []string{"commandArgument", "workdirArgument"},
			func(_ string, p []string, y any) { s.str(p, y, 1) }, map[string]bool{"commandArgument": true})
	}
}

// permission checks the flat permission map. A surface value is a state or a pattern map; zod reports a union that fails as
// "Invalid input" at the surface, except when the only fault in a pattern map is a deny reason longer than 500 UTF-16 units,
// which it reports at the reason. The surface-key checks run only when no surface failed outright.
func (s *schemaIssues) permission(path []string, v any) {
	o, ok := v.(*jsObject)
	if !ok {
		s.at(path, expected("object", v))
		return
	}
	order := []string{}
	inShape := map[string]bool{}
	for _, k := range permissionShapeKeys {
		inShape[k] = true
		if _, has := o.vals[k]; has {
			order = append(order, k)
		}
	}
	for _, k := range o.order() {
		if !inShape[k] {
			order = append(order, k)
		}
	}
	aborted := false
	for _, k := range order {
		long, ok := surfaceValueFaults(o.vals[k])
		if !ok {
			s.at(sub(path, k), "Invalid input")
			aborted = true
			continue
		}
		for _, pattern := range long {
			s.at(append(sub(path, k), pattern, "reason"), "Too big: expected string to have <=500 characters")
		}
	}
	if aborted {
		return
	}
	for _, k := range o.order() {
		switch {
		case k == "":
			s.at(sub(path, k), "A surface key must not be empty.")
		case (strings.HasPrefix(k, "path_") || strings.HasPrefix(k, "external_directory_")) && !directionalSurfaceKeys[k]:
			s.at(sub(path, k), `Unknown directional surface key "`+k+`". The legal spellings are path_read, path_write, `+
				"external_directory_read, external_directory_write.")
		}
	}
}

// schemaPermission is the permission map as the schema outputs it: the surfaces the schema names first, in its order, then the
// others in the file's order (JavaScript still puts array-index keys first), and no "__proto__" key, which an assignment in
// JavaScript does not create. The order decides which rule is the last to match a surface that a wildcard key also names.
func schemaPermission(o *jsObject) *jsObject {
	out := newObject()
	inShape := map[string]bool{}
	for _, k := range permissionShapeKeys {
		inShape[k] = true
		if v, has := o.vals[k]; has {
			out.set(k, withoutProto(v))
		}
	}
	for _, k := range o.order() {
		if !inShape[k] && k != "__proto__" {
			out.set(k, withoutProto(o.vals[k]))
		}
	}
	return out
}

// withoutProto drops a pattern map's "__proto__" key, as the schema's record output does.
func withoutProto(v any) any {
	m, ok := v.(*jsObject)
	if !ok || !m.has("__proto__") {
		return v
	}
	c := newObject()
	for _, k := range m.order() {
		if k != "__proto__" {
			c.set(k, m.vals[k])
		}
	}
	return c
}

// surfaceValueFaults reports whether a surface value fits the schema apart from over-long deny reasons (ok), and the patterns
// whose reason is too long.
func surfaceValueFaults(v any) (long []string, ok bool) {
	if st, isStr := v.(string); isStr {
		_, valid := isPermissionState(st)
		return nil, valid
	}
	m, isObj := v.(*jsObject)
	if !isObj {
		return nil, false
	}
	for _, pattern := range m.order() {
		if pattern == "" {
			return nil, false
		}
		switch x := m.vals[pattern].(type) {
		case string:
			if _, valid := isPermissionState(x); !valid {
				return nil, false
			}
		case *jsObject:
			if a, _ := x.vals["action"].(string); a != "deny" {
				return nil, false
			}
			for _, k := range x.order() {
				if k != "action" && k != "reason" {
					return nil, false
				}
			}
			if r, has := x.vals["reason"]; has {
				rs, isStr := r.(string)
				if !isStr {
					return nil, false
				}
				if len(utf16.Encode([]rune(rs))) > 500 {
					long = append(long, pattern)
				}
			}
		default:
			return nil, false
		}
	}
	return long, true
}
