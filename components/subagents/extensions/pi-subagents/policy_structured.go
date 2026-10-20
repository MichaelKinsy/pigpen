package pi_subagents

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Port of src/runs/shared/structured-output.ts.

const (
	missingStructuredOutputCallError        = "Missing structured_output call; this step has outputSchema and must finish by calling structured_output."
	missingStructuredAcceptanceReportError  = `Missing acceptanceReport in structured_output call; acceptance.report is "on".`
	structuredOutputRejectionError          = "structured_output was invoked but no valid output was captured."
	invalidStructuredOutputSchemaError      = "Structured output invocation was rejected: invalid outputSchema."
	structuredOutputValidatorUnavailableErr = "Structured output invocation was rejected: validator unavailable."
	maxStructuredOutputRejectionErrorBytes  = 4096
)

// assertJSONSchemaObject is the original's assertJsonSchemaObject: schema must be a JSON Schema object (not null, not an
// array). label names it in the error ("outputSchema" by default in the original).
func assertJSONSchemaObject(schema any, label string) error {
	if _, ok := asObject(schema); !ok {
		return fmt.Errorf("%s must be a JSON Schema object.", label)
	}
	return nil
}

func utf8Prefix(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	const suffix = "..."
	limit := maxBytes - len(suffix)
	n := 0
	for i, r := range value {
		if i+utf8.RuneLen(r) > limit {
			break
		}
		n = i + utf8.RuneLen(r)
	}
	return value[:n] + suffix
}

func messageText(content any) string {
	if s, ok := content.(string); ok {
		return s
	}
	parts, ok := asArray(content)
	if !ok {
		return ""
	}
	var texts []string
	for _, p := range parts {
		if o, ok := asObject(p); ok && o.vals["type"] == "text" {
			if t, ok := o.vals["text"].(string); ok {
				texts = append(texts, t)
			}
		}
	}
	return strings.Join(texts, "\n")
}

var (
	ansiEscape            = regexp.MustCompile("\x1b\\[[0-?]*[ -/]*[@-~]")
	controlChars          = regexp.MustCompile("[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]")
	invalidSchemaPattern  = regexp.MustCompile(`(?i)invalid outputSchema(?:\s*:|$)`)
	validatorUnavailable  = regexp.MustCompile(`(?i)failed to validate structured output:|cannot load typebox/compile`)
	rejectionDropLine     = regexp.MustCompile(`(?i)^(?:arguments?|parameters?|submitted value|input|outputSchema|schema|stack)\s*:`)
	rejectionAtLine       = regexp.MustCompile(`^at\s`)
	rejectionPayloadValue = regexp.MustCompile(`(?i)\b(received|actual|got)(?:\s+value)?\s*[:=]\s*(?:"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|\{[^\n]*\}|\[[^\n]*\]|\S+)`)
	lineSplit             = regexp.MustCompile(`\r?\n`)
)

func sanitizeStructuredOutputRejection(text string) string {
	clean := controlChars.ReplaceAllString(ansiEscape.ReplaceAllString(text, ""), "")
	if invalidSchemaPattern.MatchString(clean) {
		return invalidStructuredOutputSchemaError
	}
	if validatorUnavailable.MatchString(clean) {
		return structuredOutputValidatorUnavailableErr
	}
	const marker = "Structured output validation failed:"
	diagnostic := clean
	if i := strings.Index(clean, marker); i >= 0 {
		diagnostic = clean[i:]
	}
	var kept []string
	for _, line := range lineSplit.Split(diagnostic, -1) {
		t := jsTrim(line)
		if t != "" && !rejectionAtLine.MatchString(t) && !rejectionDropLine.MatchString(t) {
			kept = append(kept, line)
		}
	}
	sanitized := jsTrim(rejectionPayloadValue.ReplaceAllString(strings.Join(kept, "\n"), "${1}: [redacted]"))
	if sanitized == "" {
		return structuredOutputRejectionError
	}
	return sanitized
}

// formatStructuredOutputRejectionError is bounded evidence from the latest failed structured_output result of a transcript.
func formatStructuredOutputRejectionError(messages []any) string {
	ids := map[string]bool{}
	for _, m := range messages {
		o, ok := asObject(m)
		if !ok || o.vals["role"] != "assistant" {
			continue
		}
		parts, isArr := asArray(o.vals["content"])
		if !isArr {
			continue
		}
		for _, p := range parts {
			if c, ok := asObject(p); ok && c.vals["type"] == "toolCall" && c.vals["name"] == "structured_output" {
				if id, ok := c.vals["id"].(string); ok {
					ids[id] = true
				}
			}
		}
	}
	for i := len(messages) - 1; i >= 0; i-- {
		o, ok := asObject(messages[i])
		if !ok || o.vals["role"] != "toolResult" || o.vals["isError"] != true {
			continue
		}
		id, _ := o.vals["toolCallId"].(string)
		if o.vals["toolName"] != "structured_output" && !ids[id] {
			continue
		}
		return utf8Prefix(sanitizeStructuredOutputRejection(messageText(o.vals["content"])), maxStructuredOutputRejectionErrorBytes)
	}
	return structuredOutputRejectionError
}

var (
	schemaMapKeywords    = []string{"properties", "patternProperties", "$defs", "definitions", "dependentSchemas"}
	schemaSingleKeywords = []string{"additionalItems", "additionalProperties", "contains", "not", "propertyNames", "if", "then", "else", "unevaluatedItems", "unevaluatedProperties", "contentSchema"}
	schemaArrayKeywords  = []string{"allOf", "anyOf", "oneOf", "prefixItems"}
)

// rewriteLocalJSONPointerRefs prefixes the local `#/...` references of a schema that is nested under a wrapper (the tool's
// `value` property), leaving references inside a nested `$id` resource alone.
func rewriteLocalJSONPointerRefs(schema any, pointerPrefix string, inheritsWrapperResource bool) any {
	src, ok := asObject(schema)
	if !ok {
		return schema
	}
	out := src.clone()
	_, hasID := src.vals["$id"].(string)
	shares := inheritsWrapperResource && !hasID
	if shares {
		for _, kw := range []string{"$ref", "$dynamicRef", "$recursiveRef"} {
			ref, _ := src.vals[kw].(string)
			switch {
			case ref == "#":
				out.set(kw, pointerPrefix)
			case strings.HasPrefix(ref, "#/"):
				out.set(kw, pointerPrefix+ref[1:])
			}
		}
	}
	for _, kw := range schemaMapKeywords {
		entries, ok := asObject(src.vals[kw])
		if !ok {
			continue
		}
		m := newObject()
		for _, name := range entries.order() {
			m.set(name, rewriteLocalJSONPointerRefs(entries.vals[name], pointerPrefix, shares))
		}
		out.set(kw, m)
	}
	if items, isArr := asArray(src.vals["items"]); isArr {
		list := make([]any, len(items))
		for i, n := range items {
			list[i] = rewriteLocalJSONPointerRefs(n, pointerPrefix, shares)
		}
		out.set("items", list)
	} else if v, ok := src.vals["items"]; ok && !isUndefined(v) {
		out.set("items", rewriteLocalJSONPointerRefs(v, pointerPrefix, shares))
	}
	for _, kw := range schemaSingleKeywords {
		if v, ok := src.vals[kw]; ok && !isUndefined(v) {
			out.set(kw, rewriteLocalJSONPointerRefs(v, pointerPrefix, shares))
		}
	}
	for _, kw := range schemaArrayKeywords {
		if list, ok := asArray(src.vals[kw]); ok {
			l := make([]any, len(list))
			for i, n := range list {
				l[i] = rewriteLocalJSONPointerRefs(n, pointerPrefix, shares)
			}
			out.set(kw, l)
		}
	}
	if deps, ok := asObject(src.vals["dependencies"]); ok {
		m := newObject()
		for _, name := range deps.order() {
			if arr, isArr := asArray(deps.vals[name]); isArr {
				m.set(name, arr)
			} else {
				m.set(name, rewriteLocalJSONPointerRefs(deps.vals[name], pointerPrefix, shares))
			}
		}
		out.set("dependencies", m)
	}
	return out
}

// createStructuredOutputToolParameters is the parameter schema of the structured_output tool: the output schema under `value`
// (and an `acceptanceReport` object when the acceptance report is "optional" or "required").
func createStructuredOutputToolParameters(schema any, acceptanceReport string) *jsObject {
	props := newObject()
	props.set("value", rewriteLocalJSONPointerRefs(schema, "#/properties/value", true))
	if acceptanceReport != "" {
		r := newObject()
		r.set("type", "object")
		props.set("acceptanceReport", r)
	}
	required := []any{"value"}
	if acceptanceReport == "required" {
		required = append(required, "acceptanceReport")
	}
	o := newObject()
	o.set("type", "object")
	o.set("properties", props)
	o.set("required", required)
	o.set("additionalProperties", false)
	return o
}

// ---- validation with TypeBox's diagnostics -----------------------------------------------------------------------------

func jsonPointerTarget(root any, pointer string) any {
	if pointer == "#" {
		return root
	}
	if !strings.HasPrefix(pointer, "#/") {
		return nil
	}
	current := root
	for _, enc := range strings.Split(pointer[2:], "/") {
		key := strings.ReplaceAll(strings.ReplaceAll(enc, "~1", "/"), "~0", "~")
		if arr, ok := asArray(current); ok {
			i, err := strconv.Atoi(key)
			if err != nil || i < 0 || i >= len(arr) {
				return nil
			}
			current = arr[i]
			continue
		}
		o, ok := asObject(current)
		if !ok {
			return nil
		}
		current = o.vals[key]
	}
	return current
}

func resolveLocalRefs(root, node any) *jsObject {
	seen := map[*jsObject]bool{}
	for {
		rec, ok := asObject(node)
		if !ok || seen[rec] {
			return nil
		}
		seen[rec] = true
		ref, isStr := rec.vals["$ref"].(string)
		if !isStr || !strings.HasPrefix(ref, "#") {
			return rec
		}
		node = jsonPointerTarget(root, ref)
	}
}

// instanceSchemaStep is a step of the path from the root schema to the node a failing instance path selects.
type instanceSchemaStep struct {
	property bool
	key      string
	index    int
}

func traverseInstanceSchema(root any, instancePath string) (*jsObject, []instanceSchemaStep, bool) {
	node := resolveLocalRefs(root, root)
	var steps []instanceSchemaStep
	trimmed := strings.TrimPrefix(instancePath, "/")
	for _, enc := range strings.Split(trimmed, "/") {
		if enc == "" || node == nil {
			continue
		}
		key := strings.ReplaceAll(strings.ReplaceAll(enc, "~1", "/"), "~0", "~")
		if props, ok := asObject(node.vals["properties"]); ok && hasKey(props, key) {
			steps = append(steps, instanceSchemaStep{property: true, key: key})
			node = resolveLocalRefs(root, props.vals[key])
			continue
		}
		if regexp.MustCompile(`^(0|[1-9]\d*)$`).MatchString(key) {
			idx, _ := strconv.Atoi(key)
			var itemSchema any
			if pi, ok := asArray(node.vals["prefixItems"]); ok && idx < len(pi) {
				itemSchema = pi[idx]
			} else {
				itemSchema = node.vals["items"]
			}
			if itemSchema != nil && !isUndefined(itemSchema) {
				steps = append(steps, instanceSchemaStep{index: idx})
				node = resolveLocalRefs(root, itemSchema)
				continue
			}
		}
		return nil, nil, false
	}
	return node, steps, node != nil
}

func conditionalTarget(root any, schemaPath, instancePath string) (*jsObject, []instanceSchemaStep, bool) {
	node, steps, ok := traverseInstanceSchema(root, instancePath)
	if !ok {
		return nil, nil, false
	}
	pointer := schemaPath
	seen := map[string]bool{}
	for !seen[pointer] {
		seen[pointer] = true
		rec := resolveLocalRefs(root, jsonPointerTarget(root, pointer))
		if rec == nil {
			break
		}
		if v, ok := rec.vals["if"]; ok && !isUndefined(v) {
			return rec, steps, true
		}
		ref, isStr := rec.vals["$ref"].(string)
		if !isStr || !strings.HasPrefix(ref, "#") {
			break
		}
		pointer = ref
	}
	if v, ok := node.vals["if"]; ok && !isUndefined(v) {
		return node, steps, true
	}
	return nil, nil, false
}

func schemaErrorKey(e schemaError) string {
	p := e.Params
	if p == nil {
		p = newObject()
	}
	return marshalJSON([]any{e.Keyword, e.InstancePath, p, e.Message}, "")
}

func branchDiagnosticSchema(schema *jsObject, steps []instanceSchemaStep, branch any) *jsObject {
	diag := newObject()
	for _, kw := range []string{"$schema", "$id", "$defs", "definitions"} {
		if v, ok := schema.vals[kw]; ok && !isUndefined(v) {
			diag.set(kw, v)
		}
	}
	target := diag
	for _, step := range steps {
		nested := newObject()
		if !step.property {
			prefix := make([]any, step.index+1)
			for i := range prefix {
				prefix[i] = true
			}
			prefix[step.index] = nested
			target.set("prefixItems", prefix)
			target.set("items", true)
		} else {
			props := newObject()
			props.set(step.key, nested)
			target.set("properties", props)
		}
		target = nested
	}
	target.set("allOf", []any{branch})
	return diag
}

func expandConditionalError(schema *jsObject, value any, e schemaError) (*jsObject, []schemaError, bool) {
	var branch string
	if e.Params != nil {
		branch, _ = e.Params.vals["failingKeyword"].(string)
	}
	if e.Keyword != "if" || (branch != "then" && branch != "else") || e.SchemaPath == "" {
		return nil, nil, false
	}
	target, steps, ok := conditionalTarget(schema, e.SchemaPath, e.InstancePath)
	if !ok {
		return nil, nil, false
	}
	b, has := target.vals[branch]
	if !has || isUndefined(b) {
		return nil, nil, false
	}
	expanded := branchDiagnosticSchema(schema, steps, b)
	_, errs, _ := schemaErrors(expanded, value)
	if len(errs) == 0 {
		return nil, nil, false
	}
	return expanded, errs, true
}

func formatValidationError(e schemaError) []string {
	pathText := "root"
	if e.InstancePath != "" {
		pathText = strings.ReplaceAll(strings.TrimPrefix(e.InstancePath, "/"), "/", ".")
	}
	if e.Keyword == "required" && e.Params != nil {
		if req := anyList(e.Params.vals["requiredProperties"]); len(req) > 0 {
			out := make([]string, len(req))
			for i, p := range req {
				prop := jsToString(p)
				if pathText == "root" {
					out[i] = prop + ": is required"
				} else {
					out[i] = pathText + "." + prop + ": is required"
				}
			}
			return out
		}
	}
	return []string{pathText + ": " + e.Message}
}

func expandedErrorMessages(schema *jsObject, value any, e schemaError, limit int, seen map[string]bool) []string {
	if limit <= 0 {
		return nil
	}
	state := schemaErrorKey(e) + "\x00" + marshalJSON(schema, "")
	if seen[state] {
		return takeStrings(formatValidationError(e), limit)
	}
	seen[state] = true
	expanded, errs, ok := expandConditionalError(schema, value, e)
	if !ok {
		return takeStrings(formatValidationError(e), limit)
	}
	var messages []string
	for _, c := range errs {
		messages = append(messages, expandedErrorMessages(expanded, value, c, limit-len(messages), seen)...)
		if len(messages) >= limit {
			break
		}
	}
	return messages
}

func takeStrings(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// validateStructuredOutputValue validates value against schema; an invalid result carries the message the model sees (up to
// eight selected-branch diagnostics joined by "; ").
func validateStructuredOutputValue(schema any, value any) (valid bool, message string) {
	valid, errs, failure := schemaErrors(schema, value)
	if failure != "" {
		return false, "invalid outputSchema: " + failure
	}
	if valid {
		return true, ""
	}
	root, _ := asObject(schema)
	var messages []string
	unique := map[string]bool{}
	seen := map[string]bool{}
	for _, e := range errs {
		for _, m := range expandedErrorMessages(root, value, e, 8-len(messages), seen) {
			if !unique[m] {
				unique[m] = true
				messages = append(messages, m)
			}
		}
		if len(messages) >= 8 {
			break
		}
	}
	if len(messages) == 0 {
		return false, "schema validation failed"
	}
	return false, strings.Join(messages, "; ")
}

// ---- runtime files -----------------------------------------------------------------------------------------------------

// structuredOutputRuntime is where a child's structured output (and acceptance report) is captured for the host.
type structuredOutputRuntime struct {
	Schema                   any
	SchemaPath, OutputPath   string
	AcceptanceReportPath     string
	AcceptanceReportRequired bool
}

// createStructuredOutputRuntime makes the private temp directory of a run's capture files (acceptanceReport is "off",
// "optional" or "required"; "" is off).
func createStructuredOutputRuntime(schema any, baseDir, acceptanceReport string) (*structuredOutputRuntime, error) {
	if err := assertJSONSchemaObject(schema, "outputSchema"); err != nil {
		return nil, err
	}
	root := baseDir
	if root == "" {
		root = os.TempDir()
	}
	if err := os.MkdirAll(root, 0o777); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(root, "pi-subagent-structured-")
	if err != nil {
		return nil, err
	}
	rt := &structuredOutputRuntime{Schema: schema, SchemaPath: filepath.Join(dir, "schema.json"), OutputPath: filepath.Join(dir, "output.json")}
	if err := os.WriteFile(rt.SchemaPath, []byte(marshalJSON(schema, "")), 0o600); err != nil {
		return nil, err
	}
	if acceptanceReport != "" && acceptanceReport != "off" {
		rt.AcceptanceReportPath = filepath.Join(dir, "acceptance-report.json")
		rt.AcceptanceReportRequired = acceptanceReport == "required"
	}
	return rt, nil
}

// readStructuredOutput reads and validates the captured value (a missing capture is the original's missing-call error).
func readStructuredOutput(rt *structuredOutputRuntime) (value any, errMsg string) {
	data, err := os.ReadFile(rt.OutputPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, missingStructuredOutputCallError
	}
	var msg string
	if err == nil {
		value, msg = jsJSONParse(string(data))
		if msg != "" {
			err = errors.New(msg)
		}
	} else {
		err = errors.New(nodeFSErrorText(err, "open", rt.OutputPath))
	}
	if err != nil {
		return nil, "Failed to read structured output: " + err.Error()
	}
	if ok, m := validateStructuredOutputValue(rt.Schema, value); !ok {
		return nil, "Structured output validation failed: " + m
	}
	return value, ""
}

// readStructuredOutputAcceptanceReport reads the captured acceptance report ({} when there is none and it is not required).
func readStructuredOutputAcceptanceReport(rt *structuredOutputRuntime) (value any, errMsg string) {
	if rt.AcceptanceReportPath == "" {
		return nil, ""
	}
	data, err := os.ReadFile(rt.AcceptanceReportPath)
	if errors.Is(err, os.ErrNotExist) {
		if rt.AcceptanceReportRequired {
			return nil, missingStructuredAcceptanceReportError
		}
		return nil, ""
	}
	var msg string
	if err == nil {
		value, msg = jsJSONParse(string(data))
		if msg != "" {
			err = errors.New(msg)
		}
	} else {
		err = errors.New(nodeFSErrorText(err, "open", rt.AcceptanceReportPath))
	}
	if err != nil {
		return nil, "Failed to read structured output acceptance report: " + err.Error()
	}
	return value, ""
}

// createStructuredOutputFileCapture persists the structured output (and report) to the runtime's files.
func createStructuredOutputFileCapture(rt *structuredOutputRuntime) func(value, acceptanceReport any, hasReport bool) error {
	return func(value, report any, hasReport bool) error {
		if err := os.MkdirAll(filepath.Dir(rt.OutputPath), 0o777); err != nil {
			return err
		}
		if rt.AcceptanceReportPath != "" && hasReport {
			if err := os.MkdirAll(filepath.Dir(rt.AcceptanceReportPath), 0o777); err != nil {
				return err
			}
			if err := os.WriteFile(rt.AcceptanceReportPath, []byte(marshalJSON(report, "")), 0o600); err != nil {
				return err
			}
		} else if rt.AcceptanceReportPath != "" {
			_ = os.Remove(rt.AcceptanceReportPath)
		}
		return os.WriteFile(rt.OutputPath, []byte(marshalJSON(value, "")), 0o600)
	}
}

// clearStructuredOutputCaptures removes stale captures; the first failure is returned as the original words it.
func clearStructuredOutputCaptures(rt *structuredOutputRuntime) string {
	cleanupError := ""
	for _, p := range []string{rt.OutputPath, rt.AcceptanceReportPath} {
		if p == "" {
			continue
		}
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) && cleanupError == "" {
			cleanupError = fmt.Sprintf("Failed to clear stale structured output capture %s: %s", p, nodeFSErrorText(err, "unlink", p))
		}
	}
	return cleanupError
}

// cleanupStructuredOutputRuntime removes the capture directory (best effort).
func cleanupStructuredOutputRuntime(rt *structuredOutputRuntime) {
	if rt != nil {
		_ = os.RemoveAll(filepath.Dir(rt.SchemaPath))
	}
}
