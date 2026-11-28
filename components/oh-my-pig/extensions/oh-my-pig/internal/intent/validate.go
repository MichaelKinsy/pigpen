package intent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf16"
	"unicode/utf8"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// LenientTools lists tools that own their argument validation in omp
// (`readonly lenientArgValidation = true`). Upstream still runs
// `validateToolArguments` with `keepUnknownRootKeys: true` and passes the
// NORMALIZED arguments on success; only on validation failure does the tool
// receive its raw arguments (minus internal sentinels).
//
// upstream:
//   - packages/agent/src/tool-arguments.ts:16-42
//   - packages/coding-agent/src/tools/glob.ts:207
//   - packages/coding-agent/src/tools/todo.ts:696
//   - packages/coding-agent/src/tools/ask.ts:429
//   - packages/coding-agent/src/task/index.ts:748
//   - packages/coding-agent/src/tools/yield.ts:319
var (
	lenientMu    sync.RWMutex
	lenientTools = map[string]bool{
		"glob":  true,
		"todo":  true,
		"ask":   true,
		"task":  true,
		"yield": true,
	}
)

// SetLenientArgValidation opts toolName into or out of omp's
// `lenientArgValidation` behavior.
//
// Safe to call from init() or Register().
func SetLenientArgValidation(toolName string, lenient bool) {
	if toolName == "" {
		return
	}
	lenientMu.Lock()
	if lenient {
		lenientTools[toolName] = true
	} else {
		delete(lenientTools, toolName)
	}
	lenientMu.Unlock()
}

// IsLenientArgValidation reports whether toolName has omp's
// `lenientArgValidation` enabled.
func IsLenientArgValidation(toolName string) bool {
	if toolName == "" {
		return false
	}
	lenientMu.RLock()
	ok := lenientTools[toolName]
	lenientMu.RUnlock()
	return ok
}

// withoutSentinels strips omp's internal `__parseError` / `__rawJson` keys
// without mutating args.
// upstream: packages/agent/src/tool-arguments.ts:44-51
func withoutSentinels(args map[string]any) map[string]any {
	if args == nil {
		return nil
	}
	_, hasErr := args["__parseError"]
	_, hasRaw := args["__rawJson"]
	if !hasErr && !hasRaw {
		return args
	}
	stripped := make(map[string]any, len(args))
	for k, v := range args {
		if k == "__parseError" || k == "__rawJson" {
			continue
		}
		stripped[k] = v
	}
	return stripped
}

// validateOpts mirrors omp's ValidateToolArgumentsOptions.
// upstream: packages/ai/src/utils/validation.ts:2036-2044
type validateOpts struct {
	keepUnknownRootKeys bool
}

// Constants mirroring packages/ai/src/utils/validation.ts.
const (
	// upstream: validation.ts:2019
	maxErrorArgStringLength = 256
	// upstream: validation.ts:2001
	maxCoercionPasses = 5
	// upstream: validation.ts:374
	maxHealDistance = 3
	// upstream: validation.ts:420
	maxNestedJSONStringParseDepth = 3
	// upstream: validation.ts:1177
	maxKeyDecodeDepth = 3
	// upstream: validation.ts:1486
	maxFlattenedIndex = 100_000
	// upstream: validation.ts:1849-1853
	spillKeyOpen    = "<arg_key>"
	spillKeyClose   = "</arg_key>"
	spillValueOpen  = "<arg_value>"
	spillValueClose = "</arg_value>"
	spillToolClose  = "</tool_call>"
)

var (
	// upstream: validation.ts:47
	jsonNumberPattern = regexp.MustCompile(`^[+-]?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?$`)
	// upstream: validation.ts:50
	numericStringPattern = regexp.MustCompile(`^[+-]?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?$`)
	// upstream: validation.ts:457
	jsonArrayNumPrefixPattern = regexp.MustCompile(`^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?(?:\s*(?:,|\]|$))`)
	// upstream: validation.ts:1500-1501
	flattenedIdentRE = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*`)
	flattenedIndexRE = regexp.MustCompile(`^\[(\d+)\]`)
	// upstream: validation.ts:1855
	spillKeyPattern = regexp.MustCompile(`^[\w.$-]{1,128}$`)
	// upstream: errors.ts:232, 234
	errorCodeNumRE     = regexp.MustCompile(`^-?\d+(?:\.\d+)?n?$`)
	errorCodeLitPartRE = regexp.MustCompile(`^".*"$|^-?\d+(?:\.\d+)?n?$|^(?:true|false|null|undefined)$`)
)

// upstream: validation.ts:1059-1070
var identifierStringKeys = map[string]bool{
	"path":      true,
	"paths":     true,
	"file":      true,
	"file_path": true,
	"filePath":  true,
	"filepath":  true,
	"url":       true,
	"uri":       true,
	"title":     true,
	"label":     true,
}

// upstream: validation.ts:1072-1080
var contentCarryingKeys = map[string]bool{
	"content": true,
	"input":   true,
	"body":    true,
	"text":    true,
	"command": true,
	"code":    true,
	"payload": true,
}

// ============================================================================
// Type Coercion Utilities (upstream: validation.ts:56-527)
// ============================================================================

// upstream: validation.ts:56-77
func matchesExpectedType(value any, expectedTypes []string) bool {
	for _, t := range expectedTypes {
		if matchesSchemaType(value, t) {
			return true
		}
	}
	return false
}

// upstream: validation.ts:79-99
func tryParseNumberString(value string, expectedTypes []string) (any, bool) {
	if !slices.Contains(expectedTypes, "number") && !slices.Contains(expectedTypes, "integer") {
		return value, false
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || !numericStringPattern.MatchString(trimmed) {
		return value, false
	}
	parsed, err := strconv.ParseFloat(trimmed, 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return value, false
	}
	if !matchesExpectedType(parsed, expectedTypes) {
		return value, false
	}
	return parsed, true
}

// upstream: validation.ts:101-130
func tryCoerceBoolean(value any, expectedTypes []string) (any, bool) {
	if !slices.Contains(expectedTypes, "boolean") {
		return value, false
	}
	if f, ok := toFloat(value); ok {
		if f == 0 {
			return false, true
		}
		if f == 1 {
			return true, true
		}
		return value, false
	}
	s, ok := value.(string)
	if !ok {
		return value, false
	}
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "on":
		return true, true
	case "false", "0", "no", "off":
		return false, true
	default:
		return value, false
	}
}

// upstream: validation.ts:132-140
func tryCoerceBooleanToNumber(value any, expectedTypes []string) (any, bool) {
	if !slices.Contains(expectedTypes, "number") && !slices.Contains(expectedTypes, "integer") {
		return value, false
	}
	b, ok := value.(bool)
	if !ok {
		return value, false
	}
	if b {
		return float64(1), true
	}
	return float64(0), true
}

// upstream: validation.ts:142-170
func tryCoerceString(value any, expectedTypes []string, allowLossy bool) (any, bool) {
	if !slices.Contains(expectedTypes, "string") || value == nil {
		return value, false
	}
	if _, isStr := value.(string); isStr {
		return value, false
	}
	switch v := value.(type) {
	case []any, map[string]any:
		if !allowLossy {
			return value, false
		}
		s, ok := jsJSONStringifyCompact(v)
		if !ok {
			return value, false
		}
		return s, true
	case float64:
		return jsNumberToString(v), true
	case int:
		return strconv.Itoa(v), true
	case int64:
		return strconv.FormatInt(v, 10), true
	case bool:
		if v {
			return "true", true
		}
		return "false", true
	default:
		return value, false
	}
}

// upstream: validation.ts:177-195
func tryCoerceForExpectedTypes(value any, expectedTypes []string, allowLossy bool) (any, bool) {
	if s, ok := value.(string); ok {
		if parsed, changed := tryParseJSONForTypes(s, expectedTypes, 0); changed {
			return parsed, true
		}
		return tryCoerceBoolean(s, expectedTypes)
	}
	if coerced, changed := tryCoerceBoolean(value, expectedTypes); changed {
		return coerced, true
	}
	if coerced, changed := tryCoerceBooleanToNumber(value, expectedTypes); changed {
		return coerced, true
	}
	return tryCoerceString(value, expectedTypes, allowLossy)
}

// upstream: validation.ts:197-261
func tryParseLeadingJSONContainer(value string) (any, bool) {
	if len(value) == 0 {
		return nil, false
	}
	firstChar := value[0]
	var closingChar byte
	if firstChar == '{' {
		closingChar = '}'
	} else if firstChar == '[' {
		closingChar = ']'
	} else {
		return nil, false
	}

	depth := 0
	inString := false
	escaped := false

	for index := range len(value) {
		ch := value[index]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		if ch == '"' {
			inString = true
			continue
		}
		if ch == firstChar {
			depth++
			continue
		}
		if ch != closingChar {
			continue
		}
		depth--
		if depth != 0 {
			continue
		}

		prefix := value[:index+1]
		if parsed, ok := unmarshalJSONStrict(prefix); ok {
			return parsed, true
		}
		cleaned := cleanLiteralEscapes(prefix)
		if cleaned != prefix {
			if parsed, ok := unmarshalJSONStrict(cleaned); ok {
				return parsed, true
			}
		}
		escapedControls := escapeRawControlsInJSONStrings(prefix)
		if escapedControls != prefix {
			if parsed, ok := unmarshalJSONStrict(escapedControls); ok {
				return parsed, true
			}
		}
		return tryHealMalformedJSON(prefix)
	}
	return nil, false
}

// upstream: validation.ts:268-304
func cleanLiteralEscapes(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	inString := false
	for i := 0; i < len(value); {
		ch := value[i]
		if inString {
			if ch == '\\' && i+1 < len(value) {
				b.WriteByte(ch)
				b.WriteByte(value[i+1])
				i += 2
				continue
			}
			if ch == '"' {
				inString = false
			}
			b.WriteByte(ch)
			i++
			continue
		}
		if ch == '"' {
			inString = true
			b.WriteByte(ch)
			i++
			continue
		}
		if ch == '\\' && i+1 < len(value) {
			next := value[i+1]
			if next == 'n' || next == 't' || next == 'r' {
				b.WriteByte(' ')
				i += 2
				continue
			}
		}
		b.WriteByte(ch)
		i++
	}
	return b.String()
}

// upstream: validation.ts:315-371
func escapeRawControlsInJSONStrings(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	inString := false
	escaped := false
	changed := false
	for i := range len(value) {
		ch := value[i]
		if inString {
			if escaped {
				b.WriteByte(ch)
				escaped = false
				continue
			}
			if ch == '\\' {
				b.WriteByte(ch)
				escaped = true
				continue
			}
			if ch == '"' {
				b.WriteByte(ch)
				inString = false
				continue
			}
			if ch < 0x20 {
				changed = true
				switch ch {
				case '\n':
					b.WriteString(`\n`)
				case '\r':
					b.WriteString(`\r`)
				case '\t':
					b.WriteString(`\t`)
				case '\b':
					b.WriteString(`\b`)
				case '\f':
					b.WriteString(`\f`)
				default:
					fmt.Fprintf(&b, `\u%04x`, ch)
				}
				continue
			}
			b.WriteByte(ch)
			continue
		}
		if ch == '"' {
			inString = true
		}
		b.WriteByte(ch)
	}
	if !changed {
		return value
	}
	return b.String()
}

var bracketChars = []byte{'[', ']', '{', '}'}

// upstream: validation.ts:387-418
func tryHealMalformedJSON(value string) (any, bool) {
	if parsed, ok := unmarshalJSONStrict(value); ok {
		return parsed, true
	}
	tailStart := max(0, len(value)-(maxHealDistance*2+1))
	for i := tailStart; i < len(value); i++ {
		candidate := value[:i] + value[i+1:]
		if parsed, ok := unmarshalJSONStrict(candidate); ok {
			return parsed, true
		}
	}
	for i := tailStart; i < len(value); i++ {
		orig := value[i]
		for _, repl := range bracketChars {
			if repl == orig {
				continue
			}
			candidate := value[:i] + string(repl) + value[i+1:]
			if parsed, ok := unmarshalJSONStrict(candidate); ok {
				return parsed, true
			}
		}
	}
	return nil, false
}

// upstream: validation.ts:422-438
func acceptParsedJSONForTypes(parsed any, source string, expectedTypes []string, depth int) (any, bool) {
	if parsed == nil && strings.TrimSpace(source) == "null" {
		return nil, true
	}
	if matchesExpectedType(parsed, expectedTypes) {
		return parsed, true
	}
	if s, ok := parsed.(string); ok && !slices.Contains(expectedTypes, "string") && depth < maxNestedJSONStringParseDepth {
		return tryParseJSONForTypes(s, expectedTypes, depth+1)
	}
	return source, false
}

// upstream: validation.ts:440-459
func looksLikeJSONContainerString(value any) bool {
	s, ok := value.(string)
	if !ok {
		return false
	}
	trimmed := strings.TrimLeft(s, " \t\r\n")
	if strings.HasPrefix(trimmed, "{") {
		body := trimmed[1:]
		bodyTrim := strings.TrimLeft(body, " \t\r\n")
		return strings.HasPrefix(bodyTrim, `"`) || strings.Contains(body, ":") || strings.HasPrefix(bodyTrim, "}")
	}
	if !strings.HasPrefix(trimmed, "[") {
		return false
	}
	firstItem := strings.TrimLeft(trimmed[1:], " \t\r\n")
	return strings.HasPrefix(firstItem, "{") ||
		strings.HasPrefix(firstItem, "[") ||
		strings.HasPrefix(firstItem, `"`) ||
		strings.HasPrefix(firstItem, "]") ||
		strings.HasPrefix(firstItem, "true") ||
		strings.HasPrefix(firstItem, "false") ||
		strings.HasPrefix(firstItem, "null") ||
		jsonArrayNumPrefixPattern.MatchString(firstItem)
}

// upstream: validation.ts:474-527
func tryParseJSONForTypes(value string, expectedTypes []string, depth int) (any, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return value, false
	}
	if num, changed := tryParseNumberString(trimmed, expectedTypes); changed {
		return num, true
	}

	looksJSONObject := strings.HasPrefix(trimmed, "{") && looksLikeJSONContainerString(trimmed)
	looksJSONArray := strings.HasPrefix(trimmed, "[") && looksLikeJSONContainerString(trimmed)
	looksJSONString := strings.HasPrefix(trimmed, `"`) && !slices.Contains(expectedTypes, "string")
	looksJSONLiteral := trimmed == "true" || trimmed == "false" || trimmed == "null" || jsonNumberPattern.MatchString(trimmed)

	if !looksJSONObject && !looksJSONArray && !looksJSONString && !looksJSONLiteral {
		return value, false
	}

	if parsed, ok := unmarshalJSONStrict(trimmed); ok {
		if accepted, changed := acceptParsedJSONForTypes(parsed, trimmed, expectedTypes, depth); changed {
			return accepted, true
		}
		return value, false
	}

	if looksJSONObject || looksJSONArray {
		escapedControls := escapeRawControlsInJSONStrings(trimmed)
		if escapedControls != trimmed {
			if parsed, ok := unmarshalJSONStrict(escapedControls); ok {
				if accepted, changed := acceptParsedJSONForTypes(parsed, escapedControls, expectedTypes, depth); changed {
					return accepted, true
				}
			}
		}
		if leading, ok := tryParseLeadingJSONContainer(trimmed); ok {
			if accepted, changed := acceptParsedJSONForTypes(leading, trimmed, expectedTypes, depth); changed {
				return accepted, true
			}
		}
		if healed, ok := tryHealMalformedJSON(trimmed); ok {
			if accepted, changed := acceptParsedJSONForTypes(healed, trimmed, expectedTypes, depth); changed {
				return accepted, true
			}
		}
	}
	return value, false
}

func unmarshalJSONStrict(s string) (any, bool) {
	var parsed any
	if err := json.Unmarshal([]byte(s), &parsed); err != nil {
		return nil, false
	}
	return parsed, true
}

// ============================================================================
// JSON Pointer Utilities (upstream: validation.ts:537-660)
// ============================================================================

// upstream: validation.ts:538-541
func pathToPointer(path []any) string {
	if len(path) == 0 {
		return ""
	}
	var b strings.Builder
	for _, seg := range path {
		b.WriteByte('/')
		s := fmt.Sprint(seg)
		s = strings.ReplaceAll(s, "~", "~0")
		s = strings.ReplaceAll(s, "/", "~1")
		b.WriteString(s)
	}
	return b.String()
}

// upstream: validation.ts:547-553
func decodeJSONPointer(pointer string) []string {
	if pointer == "" {
		return nil
	}
	parts := strings.Split(pointer, "/")[1:]
	for i, seg := range parts {
		seg = strings.ReplaceAll(seg, "~1", "/")
		seg = strings.ReplaceAll(seg, "~0", "~")
		parts[i] = seg
	}
	return parts
}

// upstream: validation.ts:559-577
func getValueAtPointer(root any, pointer string) (any, bool) {
	if pointer == "" {
		return root, true
	}
	segments := decodeJSONPointer(pointer)
	current := root
	for _, seg := range segments {
		if current == nil {
			return nil, false
		}
		switch c := current.(type) {
		case []any:
			idx, err := strconv.Atoi(seg)
			if err != nil || idx < 0 || idx >= len(c) {
				return nil, false
			}
			current = c[idx]
		case map[string]any:
			v, ok := c[seg]
			if !ok {
				return nil, false
			}
			current = v
		default:
			return nil, false
		}
	}
	return current, true
}

// upstream: validation.ts:584-615
func setValueAtPointer(root any, pointer string, value any) any {
	if pointer == "" {
		return value
	}
	segments := decodeJSONPointer(pointer)
	current := root
	for i := range len(segments) - 1 {
		if current == nil {
			return root
		}
		seg := segments[i]
		switch c := current.(type) {
		case []any:
			idx, err := strconv.Atoi(seg)
			if err != nil || idx < 0 || idx >= len(c) {
				return root
			}
			current = c[idx]
		case map[string]any:
			v, ok := c[seg]
			if !ok {
				return root
			}
			current = v
		default:
			return root
		}
	}
	last := segments[len(segments)-1]
	switch c := current.(type) {
	case []any:
		idx, err := strconv.Atoi(last)
		if err != nil || idx < 0 || idx >= len(c) {
			return root
		}
		c[idx] = value
	case map[string]any:
		c[last] = value
	}
	return root
}

// upstream: validation.ts:624-660
func deleteValueAtPointer(root any, pointer string) (any, bool) {
	if pointer == "" {
		return root, false
	}
	segments := decodeJSONPointer(pointer)
	if len(segments) == 0 {
		return root, false
	}
	return deleteAtSegment(root, segments, 0)
}

func deleteAtSegment(node any, segments []string, depth int) (any, bool) {
	seg := segments[depth]
	isLeaf := depth == len(segments)-1
	switch c := node.(type) {
	case []any:
		idx, err := strconv.Atoi(seg)
		if err != nil || idx < 0 || idx >= len(c) {
			return node, false
		}
		if isLeaf {
			next := slices.Clone(c)
			next = slices.Delete(next, idx, idx+1)
			return next, true
		}
		child, changed := deleteAtSegment(c[idx], segments, depth+1)
		if !changed {
			return node, false
		}
		next := slices.Clone(c)
		next[idx] = child
		return next, true
	case map[string]any:
		if _, ok := c[seg]; !ok {
			return node, false
		}
		if isLeaf {
			next := make(map[string]any, len(c)-1)
			for k, v := range c {
				if k != seg {
					next[k] = v
				}
			}
			return next, true
		}
		child, changed := deleteAtSegment(c[seg], segments, depth+1)
		if !changed {
			return node, false
		}
		next := make(map[string]any, len(c))
		for k, v := range c {
			next[k] = v
		}
		next[seg] = child
		return next, true
	default:
		return node, false
	}
}

// ============================================================================
// Pre-validation Normalization Passes (upstream: validation.ts:671-1641)
// ============================================================================

// upstream: json-schema-validator.ts:77-109
func isTagSelectedBranch(branch any, value any) bool {
	bm := schemaNodeMap(branch)
	vm, ok := value.(map[string]any)
	if bm == nil || !ok {
		return false
	}
	propsNode, hasProps := bm["properties"]
	if !hasProps || !isObjectSchemaMap(propsNode) {
		return false
	}
	_, props := objectMembers(propsNode)
	matched := false
	for k, rawProp := range props {
		pm := schemaNodeMap(rawProp)
		if pm == nil {
			continue
		}
		constVal, hasConst := pm["const"]
		enumVals, hasEnum := toAnySlice(pm["enum"])
		if !hasConst && !hasEnum {
			continue
		}
		cand, present := vm[k]
		if !present {
			return false
		}
		if hasConst {
			if !jsonEqual(cand, constVal) {
				return false
			}
		} else if !sliceContainsJSON(enumVals, cand) {
			return false
		}
		matched = true
	}
	return matched
}

func getTagSelectedUnionBranch(branches []any, value any) any {
	var selected any
	for _, br := range branches {
		if !isTagSelectedBranch(br, value) {
			continue
		}
		if selected != nil {
			return nil
		}
		selected = br
	}
	return selected
}

func getTagSelectedUnionBranchIndex(branches []any, value any) int {
	selected := -1
	for i, br := range branches {
		if !isTagSelectedBranch(br, value) {
			continue
		}
		if selected != -1 {
			return -1
		}
		selected = i
	}
	return selected
}

// upstream: validation.ts:671-673
func branchMatchesSchema(branch map[string]any, value any, root map[string]any) bool {
	if branch == nil {
		return true
	}
	return isValidNodeInRoot(branch, value, root)
}

// upstream: validation.ts:675-908
func normalizeOptionalNullsForSchema(
	schema map[string]any,
	value any,
	isRoot bool,
	root map[string]any,
	insideContent bool,
	speculativeUnion bool,
) (any, bool) {
	if value == nil || schema == nil {
		return value, false
	}

	normalizeAnyOfLike := func(keyword string) (any, bool) {
		branches, ok := toAnySlice(schema[keyword])
		if !ok || len(branches) == 0 {
			return value, false
		}
		for _, br := range branches {
			bm := schemaNodeMap(br)
			if bm == nil || !branchMatchesSchema(bm, value, root) {
				continue
			}
			nv, ch := normalizeOptionalNullsForSchema(bm, value, isRoot, root, insideContent, speculativeUnion)
			if branchMatchesSchema(bm, nv, root) && branchMatchesSchema(schema, nv, root) {
				return nv, ch
			}
		}

		selectedIdx := getTagSelectedUnionBranchIndex(branches, value)
		for idx, br := range branches {
			bm := schemaNodeMap(br)
			if bm == nil {
				continue
			}
			isSelected := selectedIdx == idx
			nv, ch := normalizeOptionalNullsForSchema(bm, value, isRoot, root, insideContent, speculativeUnion || !isSelected)
			if !ch {
				continue
			}
			branchCtx := validationCtx{
				schema:           bm,
				root:             root,
				union:            schema,
				unionBranches:    branches,
				speculativeUnion: speculativeUnion,
			}
			res := validateContext(branchCtx, nv)
			if res.success && branchMatchesSchema(bm, nv, root) {
				return nv, true
			}
			repArgs, repRes, _ := runCoercionPasses(branchCtx, nv, res, isRoot, insideContent, nil)
			if repRes.success && branchMatchesSchema(bm, repArgs, root) {
				return repArgs, true
			}
		}
		return value, false
	}

	if nv, ch := normalizeAnyOfLike("anyOf"); ch {
		return nv, true
	}
	if nv, ch := normalizeAnyOfLike("oneOf"); ch {
		return nv, true
	}

	if allOf, ok := toAnySlice(schema["allOf"]); ok {
		changed := false
		nextVal := value
		for _, br := range allOf {
			bm := schemaNodeMap(br)
			if bm == nil {
				continue
			}
			nv, ch := normalizeOptionalNullsForSchema(bm, nextVal, isRoot, root, insideContent, speculativeUnion)
			if !ch {
				continue
			}
			nextVal = nv
			changed = true
		}
		if changed {
			return nextVal, true
		}
	}

	if arr, isArr := value.([]any); isArr {
		itemSchema := schemaNodeMap(schema["items"])
		if itemSchema == nil {
			return value, false
		}
		changed := false
		nextVal := arr
		for i, elem := range arr {
			nv, ch := normalizeOptionalNullsForSchema(itemSchema, elem, false, root, insideContent, speculativeUnion)
			if !ch {
				continue
			}
			if !changed {
				nextVal = slices.Clone(arr)
				changed = true
			}
			nextVal[i] = nv
		}
		return nextVal, changed
	}

	// upstream: validation.ts:807-809
	if s, isStr := value.(string); isStr {
		if t, ok := schema["type"].(string); ok && (t == "number" || t == "integer") {
			return tryParseNumberString(s, []string{t})
		}
	}

	if t, _ := schema["type"].(string); t != "object" {
		return value, false
	}
	obj, isObj := value.(map[string]any)
	if !isObj || obj == nil {
		return value, false
	}
	propsNode, hasProps := schema["properties"]
	if !hasProps || !isObjectSchemaMap(propsNode) {
		return value, false
	}
	_, props := objectMembers(propsNode)
	required := make(map[string]bool)
	for _, r := range extractStringSlice(schema["required"]) {
		required[r] = true
	}

	changed := false
	nextVal := obj
	ensureCloned := func() {
		if !changed {
			cp := make(map[string]any, len(nextVal))
			for k, v := range nextVal {
				cp[k] = v
			}
			nextVal = cp
			changed = true
		}
	}

	for key, rawPropSchema := range props {
		cur, present := nextVal[key]
		if !present {
			continue
		}
		propSchema := schemaNodeMap(rawPropSchema)
		isNullish := cur == nil || (cur == "null" && !(contentCarryingKeys[key] && branchMatchesSchema(propSchema, cur, root)))
		isInvalidEmpty := cur == "" && !required[key] && !branchMatchesSchema(propSchema, cur, root)
		isAcceptedNull := cur == nil && branchMatchesSchema(propSchema, nil, root)

		if ((isNullish && !isAcceptedNull) || isInvalidEmpty) && !required[key] {
			ensureCloned()
			delete(nextVal, key)
			continue
		}
		if isAcceptedNull && !required[key] {
			continue
		}

		// upstream: validation.ts:849-864
		if isNullish && propSchema != nil {
			if defVal, hasDef := propSchema["default"]; hasDef {
				ensureCloned()
				nextVal[key] = cloneValue(defVal)
				continue
			}
		}

		nv, ch := normalizeOptionalNullsForSchema(
			propSchema,
			cur,
			false,
			root,
			insideContent || contentCarryingKeys[key],
			speculativeUnion,
		)
		if !ch {
			continue
		}
		ensureCloned()
		nextVal[key] = nv
	}

	// upstream: validation.ts:893-905
	if !isRoot && !speculativeUnion && isFalseBool(schema["additionalProperties"]) {
		for key, v := range nextVal {
			if _, known := props[key]; known {
				continue
			}
			if v != nil && v != "null" {
				continue
			}
			ensureCloned()
			delete(nextVal, key)
		}
	}

	if !changed {
		return value, false
	}
	return nextVal, true
}

// upstream: validation.ts:921-1037
func normalizeEnumStringWhitespace(schema map[string]any, value any, root map[string]any, refs map[string]bool) (any, bool) {
	if value == nil || schema == nil {
		return value, false
	}
	if ref, ok := schema["$ref"].(string); ok && ref != "" {
		if refs[ref] {
			return value, false
		}
		refVal, ok := resolveLocalRef(root, ref)
		if !ok {
			return value, false
		}
		resolved := schemaNodeMap(refVal)
		if resolved == nil {
			return value, false
		}
		nextRefs := make(map[string]bool, len(refs)+1)
		for k, v := range refs {
			nextRefs[k] = v
		}
		nextRefs[ref] = true
		return normalizeEnumStringWhitespace(resolved, value, root, nextRefs)
	}

	normalizeAnyOfLike := func(keyword string) (any, bool) {
		branches, ok := toAnySlice(schema[keyword])
		if !ok || len(branches) == 0 {
			return value, false
		}
		for _, br := range branches {
			if bm := schemaNodeMap(br); bm != nil && branchMatchesSchema(bm, value, root) {
				return value, false
			}
		}
		for _, br := range branches {
			bm := schemaNodeMap(br)
			if bm == nil {
				continue
			}
			nv, ch := normalizeEnumStringWhitespace(bm, value, root, refs)
			if !ch {
				continue
			}
			if branchMatchesSchema(bm, nv, root) {
				return nv, true
			}
		}
		return value, false
	}

	if nv, ch := normalizeAnyOfLike("anyOf"); ch {
		return nv, true
	}
	if nv, ch := normalizeAnyOfLike("oneOf"); ch {
		return nv, true
	}
	if allOf, ok := toAnySlice(schema["allOf"]); ok {
		changed := false
		nextVal := value
		for _, br := range allOf {
			bm := schemaNodeMap(br)
			if bm == nil {
				continue
			}
			nv, ch := normalizeEnumStringWhitespace(bm, nextVal, root, refs)
			if !ch {
				continue
			}
			nextVal = nv
			changed = true
		}
		if changed {
			return nextVal, true
		}
	}

	if s, isStr := value.(string); isStr {
		trimmed := strings.TrimSpace(s)
		if trimmed != s {
			if enumVals, hasEnum := toAnySlice(schema["enum"]); hasEnum {
				if !sliceContainsJSON(enumVals, s) && sliceContainsJSON(enumVals, trimmed) {
					return trimmed, true
				}
			}
			if constStr, hasConst := schema["const"].(string); hasConst && trimmed == constStr {
				return trimmed, true
			}
		}
		return value, false
	}

	if arr, isArr := value.([]any); isArr {
		changed := false
		nextVal := arr
		prefixItems, hasPrefix := toAnySlice(schema["prefixItems"])
		if hasPrefix {
			for i := 0; i < len(arr) && i < len(prefixItems); i++ {
				if pm := schemaNodeMap(prefixItems[i]); pm != nil {
					nv, ch := normalizeEnumStringWhitespace(pm, arr[i], root, refs)
					if !ch {
						continue
					}
					if !changed {
						nextVal = slices.Clone(arr)
						changed = true
					}
					nextVal[i] = nv
				}
			}
		}
		if itemSchema := schemaNodeMap(schema["items"]); itemSchema != nil {
			for i := range len(arr) {
				if hasPrefix && i < len(prefixItems) {
					continue
				}
				nv, ch := normalizeEnumStringWhitespace(itemSchema, nextVal[i], root, refs)
				if !ch {
					continue
				}
				if !changed {
					nextVal = slices.Clone(arr)
					changed = true
				}
				nextVal[i] = nv
			}
		}
		return nextVal, changed
	}

	obj, isObj := value.(map[string]any)
	if !isObj {
		return value, false
	}
	propsNode, hasProps := schema["properties"]
	if !hasProps || !isObjectSchemaMap(propsNode) {
		return value, false
	}
	_, props := objectMembers(propsNode)
	changed := false
	nextVal := obj
	for key, rawPropSchema := range props {
		cur, present := nextVal[key]
		if !present {
			continue
		}
		nv, ch := normalizeEnumStringWhitespace(schemaNodeMap(rawPropSchema), cur, root, refs)
		if !ch {
			continue
		}
		if !changed {
			cp := make(map[string]any, len(nextVal))
			for k, v := range nextVal {
				cp[k] = v
			}
			nextVal = cp
			changed = true
		}
		nextVal[key] = nv
	}
	return nextVal, changed
}

// upstream: validation.ts:1084-1160
func trimTrailingLineTerminators(input string) string {
	return strings.TrimRight(input, "\r\n")
}

func trimIdentifierStringLeaf(input any) (any, bool) {
	switch v := input.(type) {
	case string:
		trimmed := trimTrailingLineTerminators(v)
		if trimmed == v {
			return input, false
		}
		return trimmed, true
	case []any:
		changed := false
		next := v
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				continue
			}
			trimmed := trimTrailingLineTerminators(s)
			if trimmed == s {
				continue
			}
			if !changed {
				next = slices.Clone(v)
				changed = true
			}
			next[i] = trimmed
		}
		return next, changed
	default:
		return input, false
	}
}

func normalizeIdentifierStringWhitespace(value any, insideContent bool) (any, bool) {
	if insideContent || value == nil {
		return value, false
	}
	if arr, isArr := value.([]any); isArr {
		changed := false
		next := arr
		for i, elem := range arr {
			nv, ch := normalizeIdentifierStringWhitespace(elem, false)
			if !ch {
				continue
			}
			if !changed {
				next = slices.Clone(arr)
				changed = true
			}
			next[i] = nv
		}
		return next, changed
	}
	obj, isObj := value.(map[string]any)
	if !isObj {
		return value, false
	}
	changed := false
	out := obj
	for key, entry := range obj {
		if contentCarryingKeys[key] {
			continue
		}
		nextEntry := entry
		entryChanged := false
		if identifierStringKeys[key] {
			if trimmed, ch := trimIdentifierStringLeaf(entry); ch {
				nextEntry = trimmed
				entryChanged = true
			}
		}
		if nested, ch := normalizeIdentifierStringWhitespace(nextEntry, false); ch {
			nextEntry = nested
			entryChanged = true
		}
		if !entryChanged {
			continue
		}
		if !changed {
			cp := make(map[string]any, len(obj))
			for k, v := range obj {
				cp[k] = v
			}
			out = cp
			changed = true
		}
		out[key] = nextEntry
	}
	return out, changed
}

// upstream: validation.ts:1186-1259
func decodeDoubleEncodedKey(key string) (string, bool) {
	current := key
	var decoded string
	hasDecoded := false
	for range maxKeyDecodeDepth {
		if len(current) < 2 || current[0] != '"' || current[len(current)-1] != '"' {
			break
		}
		var parsed any
		if err := json.Unmarshal([]byte(current), &parsed); err != nil {
			break
		}
		s, ok := parsed.(string)
		if !ok {
			break
		}
		current = s
		decoded = s
		hasDecoded = true
	}
	return decoded, hasDecoded
}

func normalizeDoubleEncodedKeys(value any) (any, bool) {
	if arr, isArr := value.([]any); isArr {
		changed := false
		next := arr
		for i, elem := range arr {
			nv, ch := normalizeDoubleEncodedKeys(elem)
			if !ch {
				continue
			}
			if !changed {
				next = slices.Clone(arr)
				changed = true
			}
			next[i] = nv
		}
		return next, changed
	}
	obj, isObj := value.(map[string]any)
	if !isObj || obj == nil {
		return value, false
	}
	changed := false
	out := make(map[string]any, len(obj))
	// Process keys in deterministic sorted order so colliding double-encoded keys
	// behave deterministically.
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, key := range keys {
		entry := obj[key]
		nextChild, childChanged := normalizeDoubleEncodedKeys(entry)
		decodedKey, hasDecoded := decodeDoubleEncodedKey(key)
		targetKey := key
		if hasDecoded && decodedKey != key {
			if _, inSrc := obj[decodedKey]; !inSrc {
				if _, inOut := out[decodedKey]; !inOut {
					targetKey = decodedKey
				}
			}
		}
		if targetKey != key || childChanged {
			changed = true
		}
		out[targetKey] = nextChild
	}
	if !changed {
		return value, false
	}
	return out, true
}

// upstream: validation.ts:1272-1403
func schemaAcceptsStringAndArray(schema map[string]any) bool {
	types := schemaEffectiveTypes(schema)
	if slices.Contains(types, "string") && slices.Contains(types, "array") {
		return true
	}
	for _, key := range []string{"anyOf", "oneOf"} {
		branches, ok := toAnySlice(schema[key])
		if !ok {
			continue
		}
		hasString := false
		hasArray := false
		for _, br := range branches {
			bm := schemaNodeMap(br)
			if bm == nil {
				continue
			}
			bt := schemaEffectiveTypes(bm)
			if slices.Contains(bt, "string") {
				hasString = true
			}
			if slices.Contains(bt, "array") {
				hasArray = true
			}
			if hasString && hasArray {
				return true
			}
		}
	}
	return false
}

func schemaNodeAcceptsArray(schema map[string]any) bool {
	if schema == nil {
		return false
	}
	return slices.Contains(schemaEffectiveTypes(schema), "array")
}

func parsedArrayMatchesArrayBranch(schema map[string]any, value []any, root map[string]any) bool {
	if schemaNodeAcceptsArray(schema) {
		return isValidNodeInRoot(schema, value, root)
	}
	for _, key := range []string{"anyOf", "oneOf"} {
		branches, ok := toAnySlice(schema[key])
		if !ok {
			continue
		}
		for _, br := range branches {
			bm := schemaNodeMap(br)
			if !schemaNodeAcceptsArray(bm) {
				continue
			}
			if isValidNodeInRoot(bm, value, root) {
				return true
			}
		}
	}
	return false
}

func normalizeStringEncodedArrayUnions(schema map[string]any, value any, root map[string]any) (any, bool) {
	if value == nil || schema == nil {
		return value, false
	}
	if s, isStr := value.(string); isStr && schemaAcceptsStringAndArray(schema) {
		trimmed := strings.TrimSpace(s)
		if !strings.HasPrefix(trimmed, "[") {
			return value, false
		}
		if parsed, ok := unmarshalJSONStrict(trimmed); ok {
			if parsedArr, isArr := parsed.([]any); isArr {
				candAny, _ := normalizeDoubleEncodedKeys(parsedArr)
				cand, _ := candAny.([]any)
				if parsedArrayMatchesArrayBranch(schema, cand, root) {
					return cand, true
				}
			}
		}
		return value, false
	}
	if arr, isArr := value.([]any); isArr {
		itemSchema := schemaNodeMap(schema["items"])
		if itemSchema == nil {
			return value, false
		}
		changed := false
		nextVal := arr
		for i, elem := range arr {
			nv, ch := normalizeStringEncodedArrayUnions(itemSchema, elem, root)
			if !ch {
				continue
			}
			if !changed {
				nextVal = slices.Clone(arr)
				changed = true
			}
			nextVal[i] = nv
		}
		return nextVal, changed
	}
	if t, _ := schema["type"].(string); t != "object" {
		return value, false
	}
	obj, isObj := value.(map[string]any)
	if !isObj || obj == nil {
		return value, false
	}
	propsNode, hasProps := schema["properties"]
	if !hasProps || !isObjectSchemaMap(propsNode) {
		return value, false
	}
	_, props := objectMembers(propsNode)
	changed := false
	nextVal := obj
	for key, rawPropSchema := range props {
		cur, present := nextVal[key]
		if !present {
			continue
		}
		nv, ch := normalizeStringEncodedArrayUnions(schemaNodeMap(rawPropSchema), cur, root)
		if !ch {
			continue
		}
		if !changed {
			cp := make(map[string]any, len(nextVal))
			for k, v := range nextVal {
				cp[k] = v
			}
			nextVal = cp
			changed = true
		}
		nextVal[key] = nv
	}
	return nextVal, changed
}

// upstream: validation.ts:1410-1463
func singleRequiredStringKey(schema map[string]any) (string, bool) {
	if schema == nil {
		return "", false
	}
	if t, _ := schema["type"].(string); t != "object" {
		return "", false
	}
	propsNode, hasProps := schema["properties"]
	if !hasProps || !isObjectSchemaMap(propsNode) {
		return "", false
	}
	keys, props := objectMembers(propsNode)
	if len(keys) != 1 {
		return "", false
	}
	key := keys[0]
	req := extractStringSlice(schema["required"])
	if len(req) != 1 || req[0] != key {
		return "", false
	}
	pm := schemaNodeMap(props[key])
	if pm == nil {
		return "", false
	}
	if pt, _ := pm["type"].(string); pt == "string" {
		return key, true
	}
	return "", false
}

func normalizeSingleStringField(schema map[string]any, value any) (any, bool) {
	key, ok := singleRequiredStringKey(schema)
	if !ok {
		return value, false
	}
	obj, isObj := value.(map[string]any)
	if !isObj || obj == nil {
		return value, false
	}
	if _, present := obj[key]; present {
		return value, false
	}
	var adopted string
	hasAdopted := false
	for cand, v := range obj {
		if cand == key {
			continue
		}
		if _, isStr := v.(string); !isStr {
			continue
		}
		if hasAdopted {
			return value, false // ambiguous
		}
		adopted = cand
		hasAdopted = true
	}
	if !hasAdopted {
		return value, false
	}
	next := make(map[string]any, len(obj))
	for k, v := range obj {
		if k == adopted {
			continue
		}
		next[k] = v
	}
	next[key] = obj[adopted]
	return next, true
}

// upstream: validation.ts:1488-1641
type flattenedPathStep struct {
	isIndex bool
	name    string
	index   int
}

func parseFlattenedPath(key string) ([]flattenedPathStep, bool) {
	if key == "" {
		return nil, false
	}
	first := flattenedIdentRE.FindString(key)
	if first == "" {
		return nil, false
	}
	steps := []flattenedPathStep{{name: first}}
	pos := len(first)
	sawIndex := false
	for pos < len(key) {
		if key[pos] == '.' {
			pos++
			m := flattenedIdentRE.FindString(key[pos:])
			if m == "" {
				return nil, false
			}
			steps = append(steps, flattenedPathStep{name: m})
			pos += len(m)
			continue
		}
		if key[pos] == '[' {
			m := flattenedIndexRE.FindStringSubmatch(key[pos:])
			if m == nil {
				return nil, false
			}
			idx, err := strconv.Atoi(m[1])
			if err != nil || idx < 0 || idx > maxFlattenedIndex {
				return nil, false
			}
			steps = append(steps, flattenedPathStep{isIndex: true, index: idx})
			sawIndex = true
			pos += len(m[0])
			continue
		}
		return nil, false
	}
	if !sawIndex {
		return nil, false
	}
	return steps, true
}

type undefinedSlot struct{}

var flattenedUndefined = undefinedSlot{}

func buildFlattenedPath(root map[string]any, steps []flattenedPathStep, value any) bool {
	var node any = root
	for i := range len(steps) - 1 {
		step := steps[i]
		nextIsArray := steps[i+1].isIndex
		if !step.isIndex {
			obj, ok := node.(map[string]any)
			if !ok || obj == nil {
				return false
			}
			existing, exists := obj[step.name]
			var child any
			if !exists {
				if nextIsArray {
					child = []any{}
				} else {
					child = map[string]any{}
				}
			} else {
				_, isArr := existing.([]any)
				if isArr != nextIsArray {
					return false
				}
				child = existing
			}
			obj[step.name] = child
			node = child
			continue
		}
		arrPtr, ok := node.(*[]any)
		var arr []any
		if ok {
			arr = *arrPtr
		} else {
			arr, ok = node.([]any)
			if !ok {
				return false
			}
		}
		for len(arr) <= step.index {
			arr = append(arr, flattenedUndefined)
		}
		// Write back grown slice into parent container if needed.
		child := arr[step.index]
		if child == flattenedUndefined {
			if nextIsArray {
				child = []any{}
			} else {
				child = map[string]any{}
			}
			arr[step.index] = child
		} else {
			switch child.(type) {
			case []any:
				if !nextIsArray {
					return false
				}
			case map[string]any:
				if nextIsArray {
					return false
				}
			default:
				return false
			}
		}
		// Persist grown slice back into root along steps[:i].
		if !writeBackSlice(root, steps[:i], arr) {
			return false
		}
		node = child
	}
	last := steps[len(steps)-1]
	if !last.isIndex {
		obj, ok := node.(map[string]any)
		if !ok || obj == nil {
			return false
		}
		obj[last.name] = value
		return true
	}
	arr, ok := node.([]any)
	if !ok {
		return false
	}
	for len(arr) <= last.index {
		arr = append(arr, flattenedUndefined)
	}
	arr[last.index] = value
	return writeBackSlice(root, steps[:len(steps)-1], arr)
}

func writeBackSlice(root map[string]any, prefix []flattenedPathStep, arr []any) bool {
	if len(prefix) == 0 {
		return false
	}
	var node any = root
	for i := range len(prefix) - 1 {
		st := prefix[i]
		if !st.isIndex {
			obj, ok := node.(map[string]any)
			if !ok {
				return false
			}
			node = obj[st.name]
		} else {
			a, ok := node.([]any)
			if !ok || st.index >= len(a) {
				return false
			}
			node = a[st.index]
		}
	}
	last := prefix[len(prefix)-1]
	if !last.isIndex {
		obj, ok := node.(map[string]any)
		if !ok {
			return false
		}
		obj[last.name] = arr
		return true
	}
	a, ok := node.([]any)
	if !ok || last.index >= len(a) {
		return false
	}
	a[last.index] = arr
	return true
}

func replaceUndefinedSlots(v any) any {
	switch x := v.(type) {
	case undefinedSlot:
		return nil
	case []any:
		for i, elem := range x {
			x[i] = replaceUndefinedSlots(elem)
		}
		return x
	case map[string]any:
		for k, elem := range x {
			x[k] = replaceUndefinedSlots(elem)
		}
		return x
	default:
		return v
	}
}

func normalizeFlattenedArrayProperties(value any) (any, bool) {
	obj, ok := value.(map[string]any)
	if !ok || obj == nil {
		return value, false
	}
	out := make(map[string]any, len(obj))
	changed := false
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, key := range keys {
		entry := obj[key]
		steps, isFlat := parseFlattenedPath(key)
		if !isFlat {
			if _, exists := out[key]; exists {
				return value, false
			}
			out[key] = entry
			continue
		}
		if !buildFlattenedPath(out, steps, entry) {
			return value, false
		}
		changed = true
	}
	if !changed {
		return value, false
	}
	return out, true
}

// ============================================================================
// In-band `<arg_key>`/`<arg_value>` spill healing (upstream: validation.ts:1849-1999)
// ============================================================================

type spillPair struct {
	key string
	val string
}

type spillSplit struct {
	head  string
	pairs []spillPair
}

func skipSpillWhitespace(text string, from int) int {
	at := from
	for at < len(text) {
		switch text[at] {
		case ' ', '\n', '\t', '\r':
			at++
		default:
			return at
		}
	}
	return at
}

func isSpillPairStart(text string, at int) bool {
	if !strings.HasPrefix(text[at:], spillKeyOpen) {
		return false
	}
	keyStart := at + len(spillKeyOpen)
	relEnd := strings.Index(text[keyStart:], spillKeyClose)
	if relEnd == -1 {
		return false
	}
	keyEnd := keyStart + relEnd
	if !spillKeyPattern.MatchString(text[keyStart:keyEnd]) {
		return false
	}
	valueAt := skipSpillWhitespace(text, keyEnd+len(spillKeyClose))
	return strings.HasPrefix(text[valueAt:], spillValueOpen)
}

func findSpillValueEnd(text string, from int) (int, int) {
	idxOf := func(sub string, start int) int {
		if start > len(text) {
			return -1
		}
		i := strings.Index(text[start:], sub)
		if i == -1 {
			return -1
		}
		return start + i
	}
	closeIdx := idxOf(spillValueClose, from)
	wrongIdx := idxOf(spillKeyClose, from)
	openIdx := idxOf(spillKeyOpen, from)
	for {
		at := -1
		for _, c := range []int{closeIdx, wrongIdx, openIdx} {
			if c != -1 && (at == -1 || c < at) {
				at = c
			}
		}
		if at == -1 {
			return len(text), len(text)
		}
		if at == closeIdx {
			return at, at + len(spillValueClose)
		}
		if at == wrongIdx {
			follow := skipSpillWhitespace(text, at+len(spillKeyClose))
			if follow >= len(text) ||
				strings.HasPrefix(text[follow:], spillKeyOpen) ||
				strings.HasPrefix(text[follow:], spillToolClose) {
				return at, at + len(spillKeyClose)
			}
			wrongIdx = idxOf(spillKeyClose, at+1)
			continue
		}
		if isSpillPairStart(text, at) {
			end := at
			for end > from && strings.ContainsRune(" \n\t\r", rune(text[end-1])) {
				end--
			}
			return end, at
		}
		openIdx = idxOf(spillKeyOpen, at+1)
	}
}

func parseSpilledPairs(text string) ([]spillPair, bool) {
	var pairs []spillPair
	at := skipSpillWhitespace(text, 0)
	for at < len(text) {
		if strings.HasPrefix(text[at:], spillToolClose) {
			at = skipSpillWhitespace(text, at+len(spillToolClose))
			return pairs, at >= len(text)
		}
		if !strings.HasPrefix(text[at:], spillKeyOpen) {
			return nil, false
		}
		keyStart := at + len(spillKeyOpen)
		relEnd := strings.Index(text[keyStart:], spillKeyClose)
		if relEnd == -1 {
			return nil, false
		}
		keyEnd := keyStart + relEnd
		key := text[keyStart:keyEnd]
		if !spillKeyPattern.MatchString(key) {
			return nil, false
		}
		at = skipSpillWhitespace(text, keyEnd+len(spillKeyClose))
		if !strings.HasPrefix(text[at:], spillValueOpen) {
			return nil, false
		}
		at += len(spillValueOpen)
		end, next := findSpillValueEnd(text, at)
		pairs = append(pairs, spillPair{key: key, val: text[at:end]})
		at = skipSpillWhitespace(text, next)
	}
	return pairs, true
}

func splitSpilledValue(text string) (*spillSplit, bool) {
	idxOf := func(sub string, start int) int {
		if start > len(text) {
			return -1
		}
		i := strings.Index(text[start:], sub)
		if i == -1 {
			return -1
		}
		return start + i
	}
	wrong := idxOf(spillKeyClose, 0)
	open := idxOf(spillKeyOpen, 0)
	for wrong != -1 || open != -1 {
		if wrong != -1 && (open == -1 || wrong < open) {
			if pairs, ok := parseSpilledPairs(text[wrong+len(spillKeyClose):]); ok {
				return &spillSplit{head: text[:wrong], pairs: pairs}, true
			}
			wrong = idxOf(spillKeyClose, wrong+1)
			continue
		}
		if isSpillPairStart(text, open) {
			if pairs, ok := parseSpilledPairs(text[open:]); ok && len(pairs) > 0 {
				return &spillSplit{head: strings.TrimRight(text[:open], " \t\r\n"), pairs: pairs}, true
			}
		}
		open = idxOf(spillKeyOpen, open+1)
	}
	return nil, false
}

func healInbandArgSpill(value any) (any, bool) {
	obj, ok := value.(map[string]any)
	if !ok || obj == nil {
		return value, false
	}
	changed := false
	out := make(map[string]any, len(obj))
	for k, v := range obj {
		out[k] = v
	}
	var recovered []spillPair
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, key := range keys {
		entry, isStr := obj[key].(string)
		if !isStr {
			continue
		}
		if !strings.Contains(entry, spillKeyOpen) && !strings.Contains(entry, spillKeyClose) {
			continue
		}
		split, ok := splitSpilledValue(entry)
		if !ok {
			continue
		}
		out[key] = split.head
		recovered = append(recovered, split.pairs...)
		changed = true
	}
	if !changed {
		return value, false
	}
	for _, p := range recovered {
		if _, exists := out[p.key]; !exists {
			out[p.key] = p.val
		}
	}
	return out, true
}

// ============================================================================
// Issue-driven Coercion Engine (upstream: validation.ts:1645-1844, 2197-2290)
// ============================================================================

type flatIssue struct {
	keyword       string // "type", "unrecognized", "other"
	instancePath  string
	expectedTypes []string
	unionBranch   bool
}

type validationCtx struct {
	schema           map[string]any
	root             map[string]any
	union            map[string]any
	unionBranches    []any
	speculativeUnion bool
}

type contextValidationResult struct {
	success    bool
	value      any
	flatIssues []flatIssue
	issues     []validationIssue
}

// upstream: validation.ts:1672-1733
func coerceArgsFromIssues(args any, issues []flatIssue, droppedRootKeys map[string]any) (any, bool) {
	if len(issues) == 0 {
		return args, false
	}
	changed := false
	owned := false
	nextArgs := args
	for _, issue := range issues {
		if issue.keyword == "unrecognized" {
			if issue.unionBranch {
				continue
			}
			if droppedRootKeys != nil {
				segs := decodeJSONPointer(issue.instancePath)
				if len(segs) == 1 {
					if dropped, ok := getValueAtPointer(nextArgs, issue.instancePath); ok {
						droppedRootKeys[segs[0]] = dropped
					}
				}
			}
			next, delChanged := deleteValueAtPointer(nextArgs, issue.instancePath)
			if delChanged {
				nextArgs = next
				changed = true
			}
			continue
		}
		if issue.keyword != "type" || len(issue.expectedTypes) == 0 {
			continue
		}
		currentValue, present := getValueAtPointer(nextArgs, issue.instancePath)
		if !present {
			continue
		}
		coerced, coercedOK := tryCoerceForExpectedTypes(currentValue, issue.expectedTypes, true)
		if !coercedOK &&
			slices.Contains(issue.expectedTypes, "array") &&
			!issue.unionBranch {
			if _, isArr := currentValue.([]any); !isArr {
				var objCoerced any
				var objChanged bool
				if s, isStr := currentValue.(string); isStr {
					objCoerced, objChanged = tryParseJSONForTypes(s, []string{"object"}, 0)
				}
				if objChanged || !looksLikeJSONContainerString(currentValue) {
					if objChanged {
						coerced = []any{objCoerced}
					} else {
						coerced = []any{currentValue}
					}
					coercedOK = true
				}
			}
		}
		if !coercedOK {
			continue
		}
		if !owned {
			nextArgs = cloneValue(nextArgs)
			owned = true
			changed = true
		}
		nextArgs = setValueAtPointer(nextArgs, issue.instancePath, coerced)
	}
	if !changed {
		return args, false
	}
	return nextArgs, true
}

// upstream: validation.ts:1779-1789
func restoreDroppedRootKeys(value any, dropped map[string]any) any {
	if len(dropped) == 0 {
		return value
	}
	obj, ok := value.(map[string]any)
	if !ok || obj == nil {
		return value
	}
	out := make(map[string]any, len(obj)+len(dropped))
	for k, v := range obj {
		out[k] = v
	}
	for k, v := range dropped {
		if _, exists := out[k]; !exists {
			out[k] = v
		}
	}
	return out
}

// upstream: validation.ts:2197-2290
func runCoercionPasses(
	ctx validationCtx,
	args any,
	initial contextValidationResult,
	isRoot bool,
	insideContent bool,
	droppedRootKeys map[string]any,
) (any, contextValidationResult, bool) {
	schema := ctx.schema
	root := ctx.root
	if root == nil {
		root = schema
	}
	normalizedArgs := args
	result := initial
	changed := false

	for range maxCoercionPasses {
		if result.success {
			break
		}
		coercedVal, passChanged := coerceArgsFromIssues(normalizedArgs, result.flatIssues, droppedRootKeys)
		normalizedArgs = coercedVal

		if kn, ch := normalizeDoubleEncodedKeys(normalizedArgs); ch {
			normalizedArgs = kn
			passChanged = true
		}

		specUnion := ctx.speculativeUnion
		if !specUnion && len(ctx.unionBranches) > 0 {
			sel := getTagSelectedUnionBranch(ctx.unionBranches, normalizedArgs)
			if sel == nil || schemaNodeMap(sel) == nil {
				specUnion = true
			}
		}

		if nn, ch := normalizeOptionalNullsForSchema(schema, normalizedArgs, isRoot, root, insideContent, specUnion); ch {
			normalizedArgs = nn
			passChanged = true
		}

		if en, ch := normalizeEnumStringWhitespace(schema, normalizedArgs, root, nil); ch {
			normalizedArgs = en
			passChanged = true
		}

		if idn, ch := normalizeIdentifierStringWhitespace(normalizedArgs, insideContent); ch {
			normalizedArgs = idn
			passChanged = true
		}

		if san, ch := normalizeStringEncodedArrayUnions(schema, normalizedArgs, root); ch {
			normalizedArgs = san
			passChanged = true
		}

		if idn2, ch := normalizeIdentifierStringWhitespace(normalizedArgs, insideContent); ch {
			normalizedArgs = idn2
			passChanged = true
		}

		if isRoot && ctx.union == nil {
			if ssn, ch := normalizeSingleStringField(schema, normalizedArgs); ch {
				normalizedArgs = ssn
				passChanged = true
			}
		}

		if !passChanged {
			break
		}
		changed = true
		result = validateContext(ctx, normalizedArgs)
	}
	return normalizedArgs, result, changed
}

// ============================================================================
// Main Pipeline Entry Point (upstream: validation.ts:2052-2189)
// ============================================================================

func validateAndNormalizeArgs(toolName string, schema sdk.Schema, params map[string]any) (map[string]any, bool, error) {
	return validateAndNormalizeArgsOpts(toolName, schema, params, validateOpts{})
}

func validateAndNormalizeArgsOpts(toolName string, schema sdk.Schema, params map[string]any, opts validateOpts) (map[string]any, bool, error) {
	if schema == nil || params == nil {
		return params, false, nil
	}
	root := schemaNodeMap(schema)
	if root == nil {
		return params, false, nil
	}

	// Upstream order of application (validation.ts:2072-2188):
	var normalizedArgs any = params
	changed := false

	// 1. Double-encoded keys (validation.ts:2080)
	if v, ch := normalizeDoubleEncodedKeys(normalizedArgs); ch {
		normalizedArgs = v
		changed = true
	}

	// 2. Flattened array properties (validation.ts:2090)
	if v, ch := normalizeFlattenedArrayProperties(normalizedArgs); ch {
		normalizedArgs = v
		changed = true
	}

	// 3. Optional nulls / empty strings / defaults (validation.ts:2096)
	if v, ch := normalizeOptionalNullsForSchema(root, normalizedArgs, true, root, false, false); ch {
		normalizedArgs = v
		changed = true
	}

	// 4. Enum string whitespace (validation.ts:2102)
	if v, ch := normalizeEnumStringWhitespace(root, normalizedArgs, root, nil); ch {
		normalizedArgs = v
		changed = true
	}

	// 5. Identifier trailing line terminators (validation.ts:2113)
	if v, ch := normalizeIdentifierStringWhitespace(normalizedArgs, false); ch {
		normalizedArgs = v
		changed = true
	}

	// 6. String-encoded array unions (validation.ts:2121)
	if v, ch := normalizeStringEncodedArrayUnions(root, normalizedArgs, root); ch {
		normalizedArgs = v
		changed = true
	}

	// 7. Identifier trailing line terminators after array unpack (validation.ts:2127)
	if v, ch := normalizeIdentifierStringWhitespace(normalizedArgs, false); ch {
		normalizedArgs = v
		changed = true
	}

	// 8. Single-string field remap (validation.ts:2137)
	if v, ch := normalizeSingleStringField(root, normalizedArgs); ch {
		normalizedArgs = v
		changed = true
	}

	ctx := validationCtx{schema: root, root: root}
	result := validateContext(ctx, normalizedArgs)
	if result.success {
		m, ok := finalizeAcceptedArgs(root, normalizedArgs).(map[string]any)
		if !ok {
			return params, false, nil
		}
		if !changed && !mapsNeedObjectNormalization(root, params) {
			return params, false, nil
		}
		return m, true, nil
	}

	var droppedRootKeys map[string]any
	if opts.keepUnknownRootKeys {
		droppedRootKeys = make(map[string]any)
	}
	coercedArgs, coercedRes, coercedChanged := runCoercionPasses(ctx, normalizedArgs, result, true, false, droppedRootKeys)
	normalizedArgs = coercedArgs
	changed = changed || coercedChanged
	result = coercedRes
	if result.success {
		restored := finalizeAcceptedArgs(root, restoreDroppedRootKeys(normalizedArgs, droppedRootKeys))
		if m, ok := restored.(map[string]any); ok {
			return m, true, nil
		}
		return params, false, nil
	}

	// Last resort: in-band `<arg_key>`/`<arg_value>` spill healing (validation.ts:2157-2168)
	if healedVal, healedChanged := healInbandArgSpill(normalizedArgs); healedChanged {
		normalizedArgs = healedVal
		changed = true
		result = validateContext(ctx, normalizedArgs)
		if !result.success {
			hArgs, hRes, _ := runCoercionPasses(ctx, normalizedArgs, result, true, false, droppedRootKeys)
			normalizedArgs = hArgs
			result = hRes
		}
		if result.success {
			restored := finalizeAcceptedArgs(root, restoreDroppedRootKeys(normalizedArgs, droppedRootKeys))
			if m, ok := restored.(map[string]any); ok {
				return m, true, nil
			}
			return params, false, nil
		}
	}

	// Format omp's ValidationError message (validation.ts:2172-2188).
	var b strings.Builder
	b.WriteString(`Validation failed for tool "`)
	b.WriteString(toolName)
	b.WriteString("\":\n")
	if len(result.issues) == 0 {
		b.WriteString("Unknown validation error")
	} else {
		for i, iss := range result.issues {
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString("  - ")
			b.WriteString(formatSlashPath(iss.path))
			b.WriteString(": ")
			b.WriteString(formatOmpErrorMessage(iss))
		}
	}
	b.WriteString("\n\nReceived arguments:\n")
	if changed {
		// Emit `original` before `normalized` (validation.ts:2177-2181),
		// serializing argument objects in schema property order.
		b.WriteString("{\n  \"original\": ")
		b.WriteString(indentBlock(formatOrderedArgsJSON(root, truncateArgsForError(params)), "  "))
		b.WriteString(",\n  \"normalized\": ")
		b.WriteString(indentBlock(formatOrderedArgsJSON(root, truncateArgsForError(replaceUndefinedSlots(normalizedArgs))), "  "))
		b.WriteString("\n}")
	} else {
		b.WriteString(formatOrderedArgsJSON(root, truncateArgsForError(params)))
	}
	return nil, false, errors.New(b.String())
}

// ============================================================================
// Validator & omptype Wording (upstream: omptype/src/{errors,ir,interp}.ts & json-schema-validator.ts)
// ============================================================================

type validationIssue struct {
	path        []any
	expected    string
	rawExpected string
	actual      any
	// hasDesc records whether expected came from a schema description.
	hasDesc bool
	// isDomainPrimitive marks string/number/array/object type failures where
	// omptype configures `.describe()` on top of a domain error (keeping
	// describeKind for actual). Boolean, enum, and union nodes compute
	// errorCode(expected) instead.
	isDomainPrimitive bool
	// useRawCode marks constraint failures (min/max/minLength/maxLength/
	// minItems/maxItems/pattern) whose omptype errorCode is derived from the
	// constraint's rawExpected rather than the description override.
	useRawCode bool
}

type missingSentinelType struct{}

var missingSentinel = missingSentinelType{}

func validateContext(ctx validationCtx, value any) contextValidationResult {
	target := ctx.schema
	if ctx.union != nil {
		target = ctx.union
	}
	root := ctx.root
	if root == nil {
		root = ctx.schema
	}
	var issues []validationIssue
	var flat []flatIssue
	validateNodeFull(target, value, nil, root, ctx.speculativeUnion, &issues, &flat)
	if len(issues) == 0 && len(flat) == 0 {
		return contextValidationResult{success: true, value: value}
	}
	return contextValidationResult{
		success:    false,
		flatIssues: flat,
		issues:     issues,
	}
}

func isValidNodeInRoot(schema map[string]any, val any, root map[string]any) bool {
	var issues []validationIssue
	var flat []flatIssue
	validateNodeFull(schema, val, nil, root, false, &issues, &flat)
	return len(issues) == 0 && len(flat) == 0
}

func schemaNodeMap(node any) map[string]any {
	if node == nil || !isObjectSchemaMap(node) {
		return nil
	}
	_, m := objectMembers(node)
	return m
}

func schemaPropKeys(propsNode any) ([]string, map[string]any) {
	if propsNode == nil || !isObjectSchemaMap(propsNode) {
		return nil, nil
	}
	keys, vals := objectMembers(propsNode)
	if _, isOrdered := propsNode.(*sdk.OrderedObject); !isOrdered {
		slices.Sort(keys)
	}
	return keys, vals
}

func validateNodeFull(
	schema map[string]any,
	val any,
	path []any,
	root map[string]any,
	fromUnion bool,
	issues *[]validationIssue,
	flat *[]flatIssue,
) {
	if schema == nil {
		return
	}
	if ref, ok := schema["$ref"].(string); ok && ref != "" {
		if refVal, ok := resolveLocalRef(root, ref); ok {
			if resolved := schemaNodeMap(refVal); resolved != nil {
				validateNodeFull(resolved, val, path, root, fromUnion, issues, flat)
				return
			}
		}
	}
	desc, _ := schema["description"].(string)

	// 1. const
	if constVal, hasConst := schema["const"]; hasConst {
		if !jsonEqual(val, constVal) {
			rawExp := formatLiteralExpected(constVal)
			exp := rawExp
			if desc != "" {
				exp = desc
			}
			*issues = append(*issues, validationIssue{
				path:        slices.Clone(path),
				expected:    exp,
				rawExpected: rawExp,
				actual:      val,
				hasDesc:     desc != "",
			})
			*flat = append(*flat, flatIssue{
				keyword:      "other",
				instancePath: pathToPointer(path),
				unionBranch:  fromUnion,
			})
			return
		}
	}

	// 2. enum
	if rawEnum, hasEnum := schema["enum"]; hasEnum {
		if enumVals, ok := toAnySlice(rawEnum); ok && len(enumVals) > 0 {
			if sliceContainsJSON(enumVals, val) {
				return
			}
			rawExp := formatEnumExpected(enumVals)
			exp := rawExp
			if desc != "" {
				exp = desc
			}
			*issues = append(*issues, validationIssue{
				path:        slices.Clone(path),
				expected:    exp,
				rawExpected: rawExp,
				actual:      val,
				hasDesc:     desc != "",
			})
			// If schema also declares type (e.g. string) and val is not that
			// type, emit a type flatIssue so coercions (e.g. number -> string)
			// can fire just like json-schema-validator.ts.
			types := schemaEffectiveTypes(schema)
			if len(types) > 0 && !matchesExpectedType(val, types) {
				*flat = append(*flat, flatIssue{
					keyword:       "type",
					instancePath:  pathToPointer(path),
					expectedTypes: types,
					unionBranch:   fromUnion,
				})
			} else {
				*flat = append(*flat, flatIssue{
					keyword:      "other",
					instancePath: pathToPointer(path),
					unionBranch:  fromUnion,
				})
			}
			return
		}
	}

	// 3. anyOf / oneOf
	for _, uk := range []string{"anyOf", "oneOf"} {
		if rawBranches, ok := schema[uk]; ok {
			if branches, ok := toAnySlice(rawBranches); ok && len(branches) > 0 {
				for _, br := range branches {
					if bm := schemaNodeMap(br); bm != nil && isValidNodeInRoot(bm, val, root) {
						return
					}
				}
				// Collect flatIssues from the selected branch (if discriminator matches)
				// or the first failing branch so coerceArgsFromIssues can perform
				// schema-directed type repairs (upstream: json-schema-validator.ts:435-450).
				var branchFlat []flatIssue
				selected := getTagSelectedUnionBranch(branches, val)
				if sm := schemaNodeMap(selected); sm != nil {
					var tmpIssues []validationIssue
					validateNodeFull(sm, val, path, root, fromUnion, &tmpIssues, &branchFlat)
				} else {
					for _, br := range branches {
						bm := schemaNodeMap(br)
						if bm == nil {
							continue
						}
						var tmpIssues []validationIssue
						var tmpFlat []flatIssue
						validateNodeFull(bm, val, path, root, true, &tmpIssues, &tmpFlat)
						if len(tmpFlat) > 0 {
							branchFlat = tmpFlat
							break
						}
					}
				}
				*flat = append(*flat, branchFlat...)

				// If value is an array and exactly one branch is an array,
				// refine into the array branch's element errors (omptype unionFail).
				if _, isArr := val.([]any); isArr {
					var arrBranch map[string]any
					arrCount := 0
					for _, br := range branches {
						if bm := schemaNodeMap(br); bm != nil && slices.Contains(schemaEffectiveTypes(bm), "array") {
							arrBranch = bm
							arrCount++
						}
					}
					if arrCount == 1 {
						var dummyFlat []flatIssue
						validateNodeFull(arrBranch, val, path, root, true, issues, &dummyFlat)
						return
					}
				}
				rawExp := formatUnionExpected(branches)
				exp := rawExp
				if desc != "" {
					exp = desc
				}
				*issues = append(*issues, validationIssue{
					path:        slices.Clone(path),
					expected:    exp,
					rawExpected: rawExp,
					actual:      val,
					hasDesc:     desc != "",
				})
				return
			}
		}
	}

	// 4. allOf
	if allOf, ok := toAnySlice(schema["allOf"]); ok {
		for _, br := range allOf {
			if bm := schemaNodeMap(br); bm != nil {
				validateNodeFull(bm, val, path, root, fromUnion, issues, flat)
			}
		}
	}

	// 5. type check
	types := schemaEffectiveTypes(schema)
	if len(types) > 0 && !matchesExpectedType(val, types) {
		// In JS `typeof [] === "object"` and omptype's `case "object"` only
		// checks `typeof v === "object" && v !== null` (omptype/src/interp.ts:391),
		// so omptype accepts arrays as objects (checking required properties on
		// the empty key set). Match omptype here for `type: "object"` when val
		// is `[]any`.
		if len(types) == 1 && types[0] == "object" {
			if _, isArr := val.([]any); isArr {
				validateObjectMap(schema, map[string]any{}, path, root, fromUnion, issues, flat)
				return
			}
		}
		rawExp := formatTypesExpected(types)
		exp := rawExp
		if desc != "" {
			exp = desc
		}
		isDomainPrim := len(types) == 1 && (types[0] == "string" || types[0] == "number" || types[0] == "array" || types[0] == "object")
		*issues = append(*issues, validationIssue{
			path:              slices.Clone(path),
			expected:          exp,
			rawExpected:       rawExp,
			actual:            val,
			hasDesc:           desc != "",
			isDomainPrimitive: isDomainPrim,
		})
		*flat = append(*flat, flatIssue{
			keyword:       "type",
			instancePath:  pathToPointer(path),
			expectedTypes: types,
			unionBranch:   fromUnion,
		})
		return
	}

	// 6. type-specific constraints
	switch v := val.(type) {
	case string:
		strLen := utf16Len(v)
		if minLen, ok := toFloat(schema["minLength"]); ok && float64(strLen) < minLen {
			rawExp := fmt.Sprintf("at least length %s", jsNumberToString(minLen))
			exp := rawExp
			if desc != "" {
				exp = desc
			}
			*issues = append(*issues, validationIssue{
				path:        slices.Clone(path),
				expected:    exp,
				rawExpected: rawExp,
				actual:      strLen,
				hasDesc:     desc != "",
				useRawCode:  true,
			})
			return
		}
		if maxLen, ok := toFloat(schema["maxLength"]); ok && float64(strLen) > maxLen {
			rawExp := fmt.Sprintf("at most length %s", jsNumberToString(maxLen))
			exp := rawExp
			if desc != "" {
				exp = desc
			}
			*issues = append(*issues, validationIssue{
				path:        slices.Clone(path),
				expected:    exp,
				rawExpected: rawExp,
				actual:      strLen,
				hasDesc:     desc != "",
				useRawCode:  true,
			})
			return
		}
		if pat, ok := schema["pattern"].(string); ok && pat != "" {
			if re, err := regexp.Compile(pat); err == nil && !re.MatchString(v) {
				rawExp := fmt.Sprintf("a string matching /%s/", pat)
				exp := rawExp
				if desc != "" {
					exp = desc
				}
				*issues = append(*issues, validationIssue{
					path:        slices.Clone(path),
					expected:    exp,
					rawExpected: rawExp,
					actual:      v,
					hasDesc:     desc != "",
					useRawCode:  true,
				})
				return
			}
		}

	case float64:
		validateNumberConstraintsFull(schema, v, desc, path, fromUnion, issues, flat)
	case int:
		validateNumberConstraintsFull(schema, float64(v), desc, path, fromUnion, issues, flat)
	case int64:
		validateNumberConstraintsFull(schema, float64(v), desc, path, fromUnion, issues, flat)

	case []any:
		if minItems, ok := toFloat(schema["minItems"]); ok && float64(len(v)) < minItems {
			rawExp := fmt.Sprintf("at least length %s", jsNumberToString(minItems))
			exp := rawExp
			if desc != "" {
				exp = desc
			}
			*issues = append(*issues, validationIssue{
				path:        slices.Clone(path),
				expected:    exp,
				rawExpected: rawExp,
				actual:      len(v),
				hasDesc:     desc != "",
				useRawCode:  true,
			})
			return
		}
		if maxItems, ok := toFloat(schema["maxItems"]); ok && float64(len(v)) > maxItems {
			rawExp := fmt.Sprintf("at most length %s", jsNumberToString(maxItems))
			exp := rawExp
			if desc != "" {
				exp = desc
			}
			*issues = append(*issues, validationIssue{
				path:        slices.Clone(path),
				expected:    exp,
				rawExpected: rawExp,
				actual:      len(v),
				hasDesc:     desc != "",
				useRawCode:  true,
			})
			return
		}
		if itemSchema := schemaNodeMap(schema["items"]); itemSchema != nil {
			for idx, elem := range v {
				validateNodeFull(itemSchema, elem, append(path, idx), root, fromUnion, issues, flat)
			}
		}

	case map[string]any:
		validateObjectMap(schema, v, path, root, fromUnion, issues, flat)
	}
}

func validateObjectMap(
	schema map[string]any,
	v map[string]any,
	path []any,
	root map[string]any,
	fromUnion bool,
	issues *[]validationIssue,
	flat *[]flatIssue,
) {
	propsNode, hasProps := schema["properties"]
	var propKeys []string
	var props map[string]any
	if hasProps {
		propKeys, props = schemaPropKeys(propsNode)
	}
	required := make(map[string]bool)
	for _, r := range extractStringSlice(schema["required"]) {
		required[r] = true
	}

	// Declared properties in authored order (omptype interp.ts:408-444).
	for _, k := range propKeys {
		propSchema := schemaNodeMap(props[k])
		valAtKey, present := v[k]
		if !present {
			if required[k] {
				rawExp := expectedOfSchema(propSchema)
				exp := rawExp
				if propSchema != nil {
					if pd, ok := propSchema["description"].(string); ok && pd != "" {
						exp = pd
					}
				}
				*issues = append(*issues, validationIssue{
					path:        append(slices.Clone(path), k),
					expected:    exp,
					rawExpected: rawExp,
					actual:      missingSentinel,
					hasDesc:     propSchema != nil && propSchema["description"] != nil,
				})
				*flat = append(*flat, flatIssue{
					keyword:      "other",
					instancePath: pathToPointer(append(slices.Clone(path), k)),
					unionBranch:  fromUnion,
				})
			}
			continue
		}
		if propSchema != nil {
			validateNodeFull(propSchema, valAtKey, append(path, k), root, fromUnion, issues, flat)
		}
	}

	// additionalProperties: false emits unrecognized issues so
	// coerceArgsFromIssues drops them (upstream: json-schema-validator.ts:594-600).
	if isFalseBool(schema["additionalProperties"]) {
		extraKeys := make([]string, 0)
		for k := range v {
			if _, known := props[k]; !known {
				extraKeys = append(extraKeys, k)
			}
		}
		slices.Sort(extraKeys)
		for _, k := range extraKeys {
			p := append(slices.Clone(path), k)
			*issues = append(*issues, validationIssue{
				path:        p,
				expected:    "removed",
				rawExpected: "removed",
				actual:      v[k],
			})
			*flat = append(*flat, flatIssue{
				keyword:      "unrecognized",
				instancePath: pathToPointer(p),
				unionBranch:  fromUnion,
			})
		}
	}
}

func validateNumberConstraintsFull(
	schema map[string]any,
	v float64,
	desc string,
	path []any,
	fromUnion bool,
	issues *[]validationIssue,
	flat *[]flatIssue,
) {
	if minVal, ok := toFloat(schema["minimum"]); ok && v < minVal {
		var rawExp string
		if minVal == 0 {
			rawExp = "non-negative"
		} else {
			rawExp = fmt.Sprintf("a number at least %s", jsNumberToString(minVal))
		}
		exp := rawExp
		if desc != "" {
			exp = desc
		}
		*issues = append(*issues, validationIssue{
			path:        slices.Clone(path),
			expected:    exp,
			rawExpected: rawExp,
			actual:      v,
			hasDesc:     desc != "",
			useRawCode:  true,
		})
	}
	if maxVal, ok := toFloat(schema["maximum"]); ok && v > maxVal {
		var rawExp string
		if maxVal == 0 {
			rawExp = "non-positive"
		} else {
			rawExp = fmt.Sprintf("a number at most %s", jsNumberToString(maxVal))
		}
		exp := rawExp
		if desc != "" {
			exp = desc
		}
		*issues = append(*issues, validationIssue{
			path:        slices.Clone(path),
			expected:    exp,
			rawExpected: rawExp,
			actual:      v,
			hasDesc:     desc != "",
			useRawCode:  true,
		})
	}
}

// ============================================================================
// omptype Error Formatting & JS Semantics (upstream: omptype/src/{errors,ir}.ts)
// ============================================================================

// upstream: packages/omptype/src/errors.ts:219-290
func ompErrorCode(expected string, data any) string {
	if _, isMissing := data.(missingSentinelType); isMissing {
		return "required"
	}
	if expected == "removed" {
		return "undeclared"
	}
	if strings.Contains(expected, "serialized to the same value") {
		return "referenceSame"
	}
	if strings.Contains(expected, "divisible by") || strings.Contains(expected, "integer") {
		return "divisor"
	}
	if expected == "true" || expected == "false" || expected == "null" || expected == "undefined" ||
		expected == "NaN" || expected == "Infinity" || expected == "-Infinity" ||
		errorCodeNumRE.MatchString(expected) {
		return "unit"
	}
	if strings.Contains(expected, " or ") {
		parts := strings.Split(expected, " or ")
		allLit := true
		for _, p := range parts {
			if !errorCodeLitPartRE.MatchString(p) {
				allLit = false
				break
			}
		}
		if allLit {
			return "unit"
		}
	}
	if strings.Contains(expected, "at least") || strings.Contains(expected, "more than") ||
		strings.Contains(expected, "timestamp after") || expected == "non-negative" || expected == "positive" {
		return "min"
	}
	if strings.Contains(expected, "at most") || strings.Contains(expected, "less than") ||
		strings.Contains(expected, "timestamp before") || expected == "non-positive" || expected == "negative" {
		return "max"
	}
	if strings.Contains(expected, "matching") || strings.Contains(expected, "format") ||
		strings.Contains(expected, "email") || strings.Contains(expected, "parsable") ||
		strings.Contains(expected, "only digits") {
		return "pattern"
	}
	if strings.Contains(expected, "predicate") || strings.Contains(expected, "satisfying") ||
		strings.Contains(expected, "according to") {
		return "predicate"
	}
	if strings.HasPrefix(expected, `"`) || strings.HasPrefix(expected, "the date ") {
		return "unit"
	}
	if expected == "an Error" || expected == "a Date" || strings.HasPrefix(expected, "an instance of ") {
		return "domain"
	}
	if (strings.Contains(expected, " or ") && !strings.Contains(expected, "IPv")) ||
		strings.HasSuffix(expected, " instance") ||
		strings.HasPrefix(expected, "a number representing") {
		return "domain"
	}
	switch expected {
	case "a string", "a number", "a bigint", "a symbol", "boolean", "an object",
		"an array", "a tuple", "undefined", "null", "unknown", "never":
		return "domain"
	}
	return "predicate"
}

// upstream: packages/omptype/src/ir.ts:1726-1798
func expectedOfSchema(schema map[string]any) string {
	if schema == nil {
		return "unknown"
	}
	if desc, ok := schema["description"].(string); ok && desc != "" {
		return desc
	}
	if constVal, hasConst := schema["const"]; hasConst {
		return formatLiteralExpected(constVal)
	}
	if rawEnum, hasEnum := schema["enum"]; hasEnum {
		if enumVals, ok := toAnySlice(rawEnum); ok && len(enumVals) > 0 {
			return formatEnumExpected(enumVals)
		}
	}
	for _, uk := range []string{"anyOf", "oneOf"} {
		if rawBranches, ok := schema[uk]; ok {
			if branches, ok := toAnySlice(rawBranches); ok && len(branches) > 0 {
				return formatUnionExpected(branches)
			}
		}
	}
	types := schemaEffectiveTypes(schema)
	if len(types) > 0 {
		return formatTypesExpected(types)
	}
	return "unknown"
}

func formatLiteralExpected(v any) string {
	switch x := v.(type) {
	case string:
		return jsQuoteString(x)
	case float64:
		return jsNumberToString(x)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case bool:
		if x {
			return "true"
		}
		return "false"
	case nil:
		return "null"
	default:
		return fmt.Sprint(v)
	}
}

func formatEnumExpected(vals []any) string {
	parts := make([]string, 0, len(vals))
	for _, v := range vals {
		parts = append(parts, formatLiteralExpected(v))
	}
	return joinOrList(parts)
}

func formatUnionExpected(branches []any) string {
	var parts []string
	for _, br := range branches {
		bm := schemaNodeMap(br)
		exp := expectedOfSchema(bm)
		if !slices.Contains(parts, exp) {
			parts = append(parts, exp)
		}
	}
	return joinOrList(parts)
}

func formatTypesExpected(types []string) string {
	parts := make([]string, 0, len(types))
	for _, t := range types {
		parts = append(parts, expectedOfTypeName(t))
	}
	return joinOrList(parts)
}

func expectedOfTypeName(t string) string {
	switch t {
	case "string":
		return "a string"
	case "number":
		return "a number"
	case "integer":
		return "an integer"
	case "boolean":
		return "boolean"
	case "array":
		return "an array"
	case "object":
		return "an object"
	case "null":
		return "null"
	default:
		return t
	}
}

// upstream: packages/omptype/src/errors.ts:93-95
func joinOrList(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	case 2:
		return parts[0] + " or " + parts[1]
	default:
		return strings.Join(parts[:len(parts)-1], ", ") + " or " + parts[len(parts)-1]
	}
}

// upstream: packages/ai/src/utils/validation.ts:1814-1816
func formatSlashPath(path []any) string {
	if len(path) == 0 {
		return "root"
	}
	parts := make([]string, len(path))
	for i, seg := range path {
		parts[i] = fmt.Sprint(seg)
	}
	return strings.Join(parts, "/")
}

// upstream: packages/omptype/src/errors.ts:209-217
func formatDotPath(path []any) string {
	var b strings.Builder
	for _, seg := range path {
		switch s := seg.(type) {
		case int:
			b.WriteByte('[')
			b.WriteString(strconv.Itoa(s))
			b.WriteByte(']')
		default:
			str := fmt.Sprint(seg)
			if b.Len() > 0 {
				b.WriteByte('.')
			}
			b.WriteString(str)
		}
	}
	return b.String()
}

// upstream: packages/omptype/src/errors.ts:107-125
func formatOmpErrorMessage(iss validationIssue) string {
	var actual string
	if _, isMissing := iss.actual.(missingSentinelType); isMissing {
		actual = "missing"
	} else if iss.expected == "removed" {
		actual = ""
	} else if iss.isDomainPrimitive {
		actual = describeKind(iss.actual)
	} else {
		codeTarget := iss.expected
		if iss.useRawCode && iss.rawExpected != "" {
			codeTarget = iss.rawExpected
		}
		code := ompErrorCode(codeTarget, iss.actual)
		if code == "domain" {
			actual = describeKind(iss.actual)
		} else {
			actual = describeValue(iss.actual)
		}
	}
	at := ""
	if len(iss.path) > 0 {
		at = formatDotPath(iss.path) + " "
	}
	if actual == "" {
		return fmt.Sprintf("%smust be %s", at, iss.expected)
	}
	return fmt.Sprintf("%smust be %s (was %s)", at, iss.expected, actual)
}

// upstream: packages/omptype/src/errors.ts:173-198
func describeKind(data any) string {
	if data == nil {
		return "null"
	}
	switch data.(type) {
	case undefinedSlot:
		return "undefined"
	case string:
		return "a string"
	case float64, int, int64:
		return "a number"
	case bool:
		return "boolean"
	case []any, map[string]any:
		return "an object"
	default:
		return "an object"
	}
}

// upstream: packages/omptype/src/errors.ts:146-171
func describeValue(data any) string {
	if data == nil {
		return "null"
	}
	switch v := data.(type) {
	case undefinedSlot:
		return "undefined"
	case string:
		u16Len := utf16Len(v)
		if u16Len <= 40 {
			return jsQuoteString(v)
		}
		return fmt.Sprintf("a string (length %d)", u16Len)
	case float64:
		return jsNumberToString(v)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case bool:
		if v {
			return "true"
		}
		return "false"
	case []any:
		return "an array"
	case map[string]any:
		if s, ok := jsJSONStringifyCompact(v); ok {
			return s
		}
		return "an object"
	default:
		return fmt.Sprint(v)
	}
}

// utf16Len returns the JavaScript `string.length` (UTF-16 code unit count).
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// sliceUTF16Prefix returns the prefix of s consisting of the first maxUnits
// UTF-16 code units (matching JS `value.slice(0, MAX)` for well-formed UTF-8,
// avoiding splitting a surrogate pair in half when possible).
func sliceUTF16Prefix(s string, maxUnits int) string {
	units := utf16.Encode([]rune(s))
	if len(units) <= maxUnits {
		return s
	}
	cut := units[:maxUnits]
	runes := utf16.Decode(cut)
	var b strings.Builder
	for _, r := range runes {
		if utf8.ValidRune(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// jsQuoteString encodes s as a JSON string literal without HTML escaping
// (matching JS `JSON.stringify(s)`).
func jsQuoteString(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	out := strings.TrimSuffix(buf.String(), "\n")
	// Go's encoding/json escapes U+2028 and U+2029 even with SetEscapeHTML(false);
	// JS JSON.stringify emits them verbatim.
	out = strings.ReplaceAll(out, `\u2028`, "\u2028")
	out = strings.ReplaceAll(out, `\u2029`, "\u2029")
	return out
}

// jsJSONStringifyCompact encodes v as compact JSON without HTML escaping.
func jsJSONStringifyCompact(v any) (string, bool) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", false
	}
	return strings.TrimSuffix(buf.String(), "\n"), true
}

// jsNumberToString formats a float64 identically to ECMAScript
// `Number.prototype.toString(10)` (shortest round-trip, `-0` -> `"0"`,
// exponential notation for |x| >= 1e21 or 0 < |x| < 1e-6 with no leading zero
// on single-digit exponents like `1e+21`, `1e-7`).
func jsNumberToString(f float64) string {
	if math.IsNaN(f) {
		return "NaN"
	}
	if math.IsInf(f, 1) {
		return "Infinity"
	}
	if math.IsInf(f, -1) {
		return "-Infinity"
	}
	if f == 0 {
		return "0"
	}
	sign := ""
	if f < 0 {
		sign = "-"
		f = -f
	}
	// Get shortest scientific representation: e.g. "1.23e+05" or "1e-07"
	sci := strconv.FormatFloat(f, 'e', -1, 64)
	eIdx := strings.IndexByte(sci, 'e')
	mantissa := sci[:eIdx]
	exp, _ := strconv.Atoi(sci[eIdx+1:])

	// Remove decimal point from mantissa to get significant digits.
	digits := strings.ReplaceAll(mantissa, ".", "")
	// Trim trailing zeros (FormatFloat with -1 already does, but be safe).
	digits = strings.TrimRight(digits, "0")
	if digits == "" {
		return "0"
	}
	k := len(digits)
	n := exp + 1 // value = 0.<digits> * 10^n

	if k <= n && n <= 21 {
		return sign + digits + strings.Repeat("0", n-k)
	}
	if 0 < n && n <= 21 {
		return sign + digits[:n] + "." + digits[n:]
	}
	if -6 < n && n <= 0 {
		return sign + "0." + strings.Repeat("0", -n) + digits
	}
	// Exponential form: d[.ddd]e[+-]exp
	var expStr string
	if exp >= 0 {
		expStr = "e+" + strconv.Itoa(exp)
	} else {
		expStr = "e" + strconv.Itoa(exp)
	}
	if k == 1 {
		return sign + digits + expStr
	}
	return sign + digits[:1] + "." + digits[1:] + expStr
}

// ============================================================================
// Schema / Value Helpers
// ============================================================================

func schemaEffectiveTypes(schema map[string]any) []string {
	raw, ok := schema["type"]
	var types []string
	if ok {
		switch t := raw.(type) {
		case string:
			types = []string{t}
		case []string:
			types = slices.Clone(t)
		case []any:
			types = make([]string, 0, len(t))
			for _, elem := range t {
				if s, ok := elem.(string); ok {
					types = append(types, s)
				}
			}
		}
	}
	if nullable, _ := schema["nullable"].(bool); nullable && !slices.Contains(types, "null") {
		types = append(types, "null")
	}
	return types
}

func matchesSchemaType(val any, t string) bool {
	switch t {
	case "string":
		_, ok := val.(string)
		return ok
	case "number":
		switch n := val.(type) {
		case float64:
			return !math.IsNaN(n) && !math.IsInf(n, 0)
		case int, int64:
			return true
		default:
			return false
		}
	case "integer":
		switch n := val.(type) {
		case float64:
			return !math.IsNaN(n) && !math.IsInf(n, 0) && math.Trunc(n) == n
		case int, int64:
			return true
		default:
			return false
		}
	case "boolean":
		_, ok := val.(bool)
		return ok
	case "null":
		return val == nil
	case "array":
		_, ok := val.([]any)
		return ok
	case "object":
		_, ok := val.(map[string]any)
		return ok
	default:
		return false
	}
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

func sliceContainsJSON(list []any, target any) bool {
	for _, item := range list {
		if jsonEqual(item, target) {
			return true
		}
	}
	return false
}

func jsonEqual(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if fa, ok := toFloat(a); ok {
		if fb, ok := toFloat(b); ok {
			return fa == fb
		}
		return false
	}
	sa, oka := a.(string)
	sb, okb := b.(string)
	if oka && okb {
		return sa == sb
	}
	ba, oka := a.(bool)
	bb, okb := b.(bool)
	if oka && okb {
		return ba == bb
	}
	ja, erra := json.Marshal(a)
	jb, errb := json.Marshal(b)
	return erra == nil && errb == nil && bytes.Equal(ja, jb)
}

func cloneValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = cloneValue(val)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = cloneValue(val)
		}
		return out
	default:
		return v
	}
}

// upstream: packages/ai/src/utils/validation.ts:2021-2033
func truncateArgsForError(v any) any {
	switch x := v.(type) {
	case string:
		u16Len := utf16Len(x)
		if u16Len <= maxErrorArgStringLength {
			return x
		}
		return fmt.Sprintf("%s… [truncated %d chars]", sliceUTF16Prefix(x, maxErrorArgStringLength), u16Len-maxErrorArgStringLength)
	case []any:
		out := make([]any, len(x))
		for i, elem := range x {
			out[i] = truncateArgsForError(elem)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = truncateArgsForError(val)
		}
		return out
	default:
		return v
	}
}

// formatOrderedArgsJSON pretty-prints v with 2-space indent, no HTML escaping,
// and object keys ordered by the schema's `properties` order followed by
// sorted unknown keys.
func formatOrderedArgsJSON(schema map[string]any, v any) string {
	var b strings.Builder
	writeOrderedJSON(&b, schema, v, 0)
	return b.String()
}

func writeOrderedJSON(b *strings.Builder, schema map[string]any, v any, depth int) {
	indent := strings.Repeat("  ", depth)
	childIndent := strings.Repeat("  ", depth+1)
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if x {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case float64:
		b.WriteString(jsNumberToString(x))
	case int:
		b.WriteString(strconv.Itoa(x))
	case int64:
		b.WriteString(strconv.FormatInt(x, 10))
	case string:
		b.WriteString(jsQuoteString(x))
	case []any:
		if len(x) == 0 {
			b.WriteString("[]")
			return
		}
		var itemSchema map[string]any
		if schema != nil {
			itemSchema = schemaNodeMap(schema["items"])
		}
		b.WriteString("[\n")
		for i, elem := range x {
			if i > 0 {
				b.WriteString(",\n")
			}
			b.WriteString(childIndent)
			writeOrderedJSON(b, itemSchema, elem, depth+1)
		}
		b.WriteByte('\n')
		b.WriteString(indent)
		b.WriteByte(']')
	case map[string]any:
		if len(x) == 0 {
			b.WriteString("{}")
			return
		}
		var orderedKeys []string
		var propMap map[string]any
		seen := make(map[string]bool, len(x))
		if schema != nil {
			if propsNode, ok := schema["properties"]; ok {
				pKeys, pMap := schemaPropKeys(propsNode)
				propMap = pMap
				for _, pk := range pKeys {
					if _, present := x[pk]; present {
						orderedKeys = append(orderedKeys, pk)
						seen[pk] = true
					}
				}
			}
		}
		var extras []string
		for k := range x {
			if !seen[k] {
				extras = append(extras, k)
			}
		}
		slices.Sort(extras)
		orderedKeys = append(orderedKeys, extras...)

		b.WriteString("{\n")
		for i, k := range orderedKeys {
			if i > 0 {
				b.WriteString(",\n")
			}
			b.WriteString(childIndent)
			b.WriteString(jsQuoteString(k))
			b.WriteString(": ")
			var childSchema map[string]any
			if propMap != nil {
				childSchema = schemaNodeMap(propMap[k])
			}
			writeOrderedJSON(b, childSchema, x[k], depth+1)
		}
		b.WriteByte('\n')
		b.WriteString(indent)
		b.WriteByte('}')
	default:
		if s, ok := jsJSONStringifyCompact(x); ok {
			b.WriteString(s)
		} else {
			b.WriteString("null")
		}
	}
}

func indentBlock(s, prefix string) string {
	return strings.ReplaceAll(s, "\n", "\n"+prefix)
}

// dropUndeclaredRootKeysIfClosed removes undeclared root keys when schema has
// `additionalProperties: false`. Used after a lenient tool's own
// PrepareArguments has accepted a schema-valid call so PiG's host validator
// does not reject unknown root keys that omp's lenient tools ignore.
func dropUndeclaredRootKeysIfClosed(schema sdk.Schema, params map[string]any) map[string]any {
	root := schemaNodeMap(schema)
	if root == nil || !isFalseBool(root["additionalProperties"]) {
		return params
	}
	propsNode, hasProps := root["properties"]
	if !hasProps || !isObjectSchemaMap(propsNode) {
		return params
	}
	_, props := objectMembers(propsNode)
	hasExtra := false
	for k := range params {
		if _, ok := props[k]; !ok {
			hasExtra = true
			break
		}
	}
	if !hasExtra {
		return params
	}
	out := make(map[string]any, len(params))
	for k, v := range params {
		if _, ok := props[k]; ok {
			out[k] = v
		}
	}
	return out
}

// finalizeAcceptedArgs replaces any internal undefinedSlot values with nil and
// converts accepted array-for-object values (which omptype accepts because
// `typeof [] === "object"` and treats as an object with no named properties,
// e.g. `bash {ready: []}`, `debug {arguments: []}`) into `{}` so PiG's
// downstream JSON Schema host validator also accepts them.
func finalizeAcceptedArgs(schema map[string]any, val any) any {
	val = replaceUndefinedSlots(val)
	return normalizeAcceptedArraysForObjects(schema, val)
}

func mapsNeedObjectNormalization(schema map[string]any, val any) bool {
	if schema == nil || val == nil {
		return false
	}
	types := schemaEffectiveTypes(schema)
	if len(types) == 1 && types[0] == "object" {
		if _, isArr := val.([]any); isArr {
			return true
		}
	}
	switch x := val.(type) {
	case map[string]any:
		propsNode, hasProps := schema["properties"]
		if !hasProps || !isObjectSchemaMap(propsNode) {
			return false
		}
		_, props := objectMembers(propsNode)
		for k, v := range x {
			if ps := schemaNodeMap(props[k]); ps != nil && mapsNeedObjectNormalization(ps, v) {
				return true
			}
		}
	case []any:
		if itemSchema := schemaNodeMap(schema["items"]); itemSchema != nil {
			for _, elem := range x {
				if mapsNeedObjectNormalization(itemSchema, elem) {
					return true
				}
			}
		}
	}
	return false
}

func normalizeAcceptedArraysForObjects(schema map[string]any, val any) any {
	if schema == nil || val == nil {
		return val
	}
	types := schemaEffectiveTypes(schema)
	if len(types) == 1 && types[0] == "object" {
		if _, isArr := val.([]any); isArr {
			return map[string]any{}
		}
	}
	switch x := val.(type) {
	case map[string]any:
		propsNode, hasProps := schema["properties"]
		if !hasProps || !isObjectSchemaMap(propsNode) {
			return x
		}
		_, props := objectMembers(propsNode)
		changed := false
		out := x
		for k, v := range x {
			ps := schemaNodeMap(props[k])
			if ps == nil {
				continue
			}
			nv := normalizeAcceptedArraysForObjects(ps, v)
			if !sameShallow(nv, v) {
				if !changed {
					cp := make(map[string]any, len(x))
					for ck, cv := range x {
						cp[ck] = cv
					}
					out = cp
					changed = true
				}
				out[k] = nv
			}
		}
		return out
	case []any:
		itemSchema := schemaNodeMap(schema["items"])
		if itemSchema == nil {
			return x
		}
		changed := false
		out := x
		for i, elem := range x {
			nv := normalizeAcceptedArraysForObjects(itemSchema, elem)
			if !sameShallow(nv, elem) {
				if !changed {
					out = slices.Clone(x)
					changed = true
				}
				out[i] = nv
			}
		}
		return out
	default:
		return val
	}
}

func sameShallow(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		return ok && len(x) == len(y) && jsonEqual(x, y)
	case []any:
		y, ok := b.([]any)
		return ok && len(x) == len(y) && jsonEqual(x, y)
	default:
		return a == b
	}
}
