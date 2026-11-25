package ohmypig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// Faithful port of PiG's host tool-argument validator/coercer from
// internal/intent/fakehost_test.go (mirroring ai/validate.go + schema/{coerce,checks,explain}.go).
func pigValidateAndCoerce(toolName string, rawSchema, rawArgs json.RawMessage) (json.RawMessage, error) {
	var schema map[string]any
	if err := json.Unmarshal(rawSchema, &schema); err != nil {
		return nil, fmt.Errorf("Validation failed for tool %q: invalid schema JSON: %w", toolName, err)
	}
	var value any
	if err := json.Unmarshal(rawArgs, &value); err != nil {
		return nil, fmt.Errorf("Validation failed for tool %q:\n  - invalid JSON: %w", toolName, err)
	}
	if len(schema) == 0 {
		return rawArgs, nil
	}

	coerced := pigDropOptionalNulls(value, schema)
	coerced = pigConvertValue(schema, coerced)

	errs := pigExplain(schema, coerced, "")
	if len(errs) > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "Validation failed for tool %q:\n", toolName)
		for i, e := range errs {
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString("  - ")
			b.WriteString(e)
		}
		return nil, errors.New(b.String())
	}

	out, err := json.Marshal(coerced)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func extractStringSlice(v any) []string {
	switch arr := v.(type) {
	case []any:
		out := make([]string, 0, len(arr))
		for _, item := range arr {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return arr
	default:
		return nil
	}
}

func pigDropOptionalNulls(value any, schema map[string]any) any {
	obj, isObj := value.(map[string]any)
	if !isObj || schema == nil {
		return value
	}
	props, _ := schema["properties"].(map[string]any)
	if len(props) == 0 {
		return value
	}
	req := extractStringSlice(schema["required"])
	out := make(map[string]any, len(obj))
	for k, v := range obj {
		propRaw, declared := props[k]
		if declared {
			propSchema, _ := propRaw.(map[string]any)
			if v == nil && !slices.Contains(req, k) && propSchema != nil && len(pigExplain(propSchema, nil, "")) > 0 {
				continue
			}
			if propSchema != nil {
				v = pigDropOptionalNulls(v, propSchema)
			}
		}
		out[k] = v
	}
	return out
}

func pigConvertValue(schema map[string]any, value any) any {
	if schema == nil {
		return value
	}
	if anyOf, ok := schema["anyOf"].([]any); ok {
		return pigConvertUnion(anyOf, value)
	}
	if oneOf, ok := schema["oneOf"].([]any); ok {
		return pigConvertUnion(oneOf, value)
	}
	if typeName, ok := schema["type"].(string); ok {
		switch typeName {
		case "string":
			if _, ok := value.(string); ok {
				return value
			}
			switch x := value.(type) {
			case nil:
				return "null"
			case bool:
				return strconv.FormatBool(x)
			case float64:
				if !math.IsNaN(x) && !math.IsInf(x, 0) {
					return strconv.FormatFloat(x, 'f', -1, 64)
				}
			}
			return value
		case "number":
			if _, ok := value.(float64); ok {
				return value
			}
			if s, ok := value.(string); ok {
				if n, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil && !math.IsNaN(n) && !math.IsInf(n, 0) {
					return n
				}
			}
			return value
		case "integer":
			if n, ok := value.(float64); ok {
				return math.Trunc(n)
			}
			if s, ok := value.(string); ok {
				if n, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil && !math.IsNaN(n) && !math.IsInf(n, 0) {
					return math.Trunc(n)
				}
			}
			return value
		case "boolean":
			if _, ok := value.(bool); ok {
				return value
			}
			if s, ok := value.(string); ok {
				if s == "true" {
					return true
				}
				if s == "false" {
					return false
				}
			}
			return value
		case "array":
			itemSchema, _ := schema["items"].(map[string]any)
			if arr, ok := value.([]any); ok {
				if itemSchema == nil {
					return arr
				}
				out := make([]any, len(arr))
				for i, item := range arr {
					out[i] = pigConvertValue(itemSchema, item)
				}
				return out
			}
			if itemSchema != nil {
				return []any{pigConvertValue(itemSchema, value)}
			}
			return []any{value}
		case "object":
			return pigConvertObject(schema, value)
		}
	}
	if _, hasProps := schema["properties"]; hasProps {
		return pigConvertObject(schema, value)
	}
	return value
}
func pigConvertUnion(branches []any, value any) any {
	for _, br := range branches {
		bm, ok := br.(map[string]any)
		if !ok {
			continue
		}
		if len(pigExplain(bm, value, "")) == 0 {
			return value
		}
	}
	for _, br := range branches {
		bm, ok := br.(map[string]any)
		if !ok {
			continue
		}
		cand := pigConvertValue(bm, value)
		if len(pigExplain(bm, cand, "")) == 0 {
			return cand
		}
	}
	return value
}

func pigConvertObject(schema map[string]any, value any) any {
	obj, ok := value.(map[string]any)
	if !ok {
		return value
	}
	props, _ := schema["properties"].(map[string]any)
	out := make(map[string]any, len(obj))
	for k, v := range obj {
		if ps, ok := props[k].(map[string]any); ok {
			out[k] = pigConvertValue(ps, v)
		} else {
			out[k] = v
		}
	}
	return out
}

func pigExplain(schema map[string]any, value any, path string) []string {
	if schema == nil {
		return nil
	}
	loc := path
	if loc == "" {
		loc = "root"
	}
	if rawType, hasType := schema["type"]; hasType {
		var wantTypes []string
		switch t := rawType.(type) {
		case string:
			wantTypes = []string{t}
		case []any:
			for _, x := range t {
				if s, ok := x.(string); ok {
					wantTypes = append(wantTypes, s)
				}
			}
		}
		if len(wantTypes) > 0 && !pigMatchesAnyType(value, wantTypes) {
			return []string{fmt.Sprintf("%s: expected %s, got %s", loc, strings.Join(wantTypes, " or "), pigTypeName(value))}
		}
	}
	var errs []string
	if c, hasConst := schema["const"]; hasConst {
		if !pigEqual(c, value) {
			errs = append(errs, fmt.Sprintf("%s: expected %v", loc, c))
		}
	}
	if rawEnum, hasEnum := schema["enum"].([]any); hasEnum {
		matched := false
		for _, item := range rawEnum {
			if pigEqual(item, value) {
				matched = true
				break
			}
		}
		if !matched {
			errs = append(errs, fmt.Sprintf("%s: expected one of %v", loc, rawEnum))
		}
	}
	if anyOf, ok := schema["anyOf"].([]any); ok {
		matched := false
		var firstBranchErrs []string
		for _, br := range anyOf {
			bm, ok := br.(map[string]any)
			if !ok {
				continue
			}
			be := pigExplain(bm, value, path)
			if len(be) == 0 {
				matched = true
				break
			}
			if firstBranchErrs == nil {
				firstBranchErrs = be
			}
		}
		if !matched {
			errs = append(errs, firstBranchErrs...)
		}
	}
	if oneOf, ok := schema["oneOf"].([]any); ok {
		matched := 0
		var firstBranchErrs []string
		for _, br := range oneOf {
			bm, ok := br.(map[string]any)
			if !ok {
				continue
			}
			be := pigExplain(bm, value, path)
			if len(be) == 0 {
				matched++
			} else if firstBranchErrs == nil {
				firstBranchErrs = be
			}
		}
		if matched != 1 {
			if len(firstBranchErrs) > 0 {
				errs = append(errs, firstBranchErrs...)
			} else {
				errs = append(errs, fmt.Sprintf("%s: expected exactly one oneOf branch to match (matched %d)", loc, matched))
			}
		}
	}
	if obj, isObj := value.(map[string]any); isObj {
		props, _ := schema["properties"].(map[string]any)
		for _, reqName := range extractStringSlice(schema["required"]) {
			if _, present := obj[reqName]; !present {
				errs = append(errs, fmt.Sprintf("%s: missing", joinPath(path, reqName)))
			}
		}
		for k, v := range obj {
			if ps, ok := props[k].(map[string]any); ok {
				errs = append(errs, pigExplain(ps, v, joinPath(path, k))...)
			} else if b, ok := props[k].(bool); ok && !b {
				errs = append(errs, fmt.Sprintf("%s: no value is allowed here", joinPath(path, k)))
			} else if addProps, hasAdd := schema["additionalProperties"]; hasAdd {
				if ab, ok := addProps.(bool); ok && !ab {
					errs = append(errs, fmt.Sprintf("%s: unknown field", joinPath(path, k)))
				} else if as, ok := addProps.(map[string]any); ok {
					errs = append(errs, pigExplain(as, v, joinPath(path, k))...)
				}
			}
		}
	}
	if arr, isArr := value.([]any); isArr {
		if itemSchema, ok := schema["items"].(map[string]any); ok {
			for i, item := range arr {
				errs = append(errs, pigExplain(itemSchema, item, joinPath(path, strconv.Itoa(i)))...)
			}
		}
	}
	return errs
}

func joinPath(base, seg string) string {
	if base == "" {
		return seg
	}
	return base + "." + seg
}

func pigMatchesAnyType(v any, types []string) bool {
	for _, t := range types {
		switch t {
		case "string":
			if _, ok := v.(string); ok {
				return true
			}
		case "number":
			if n, ok := v.(float64); ok && !math.IsNaN(n) && !math.IsInf(n, 0) {
				return true
			}
		case "integer":
			if n, ok := v.(float64); ok && !math.IsNaN(n) && !math.IsInf(n, 0) && math.Trunc(n) == n {
				return true
			}
		case "boolean":
			if _, ok := v.(bool); ok {
				return true
			}
		case "null":
			if v == nil {
				return true
			}
		case "array":
			if _, ok := v.([]any); ok {
				return true
			}
		case "object":
			if _, ok := v.(map[string]any); ok {
				return true
			}
		}
	}
	return false
}

func pigTypeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return fmt.Sprintf("%T", v)
	}
}

func pigEqual(a, b any) bool {
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(ja, jb)
}

func jsonObjectKeys(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		t.Fatalf("decode object open: %v (raw=%s)", err, string(raw))
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		t.Fatalf("expected '{', got %v (raw=%s)", tok, string(raw))
	}
	var keys []string
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			t.Fatalf("decode key: %v", err)
		}
		ks, ok := k.(string)
		if !ok {
			t.Fatalf("expected string key, got %T", k)
		}
		keys = append(keys, ks)
		var discard json.RawMessage
		if err := dec.Decode(&discard); err != nil {
			t.Fatalf("decode value for %q: %v", ks, err)
		}
	}
	return keys
}
