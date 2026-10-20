package pi_subagents

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"
)

// Extension bindings (src/runs/shared/extension-bindings.ts) and the launch binding digest (src/shared/launch-contract.ts):
// the namespaced JSON a caller hands to the extensions of a child, bounded and canonical, and the digest of everything the
// child's launch is bound to.
const (
	extensionBindingsEnv           = "PI_SUBAGENT_EXTENSION_BINDINGS"
	maxExtensionBindingNamespaces  = 16
	maxExtensionBindingsBytes      = 16 * 1024
	maxExtensionBindingsDepth      = 16
	maxExtensionBindingsProperties = 256
)

var extensionBindingNamespaceRe = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]{0,62})/[1-9][0-9]{0,8}$`)

// normalizedExtensionBindings is the canonical value (objects with sorted keys) and its compact JSON.
type normalizedExtensionBindings struct {
	value any
	json  string
}

// compareUTF16 is the default Array.prototype.sort order of strings: by UTF-16 code unit.
func compareUTF16(a, b string) int {
	ua, ub := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := 0; i < len(ua) && i < len(ub); i++ {
		if ua[i] != ub[i] {
			if ua[i] < ub[i] {
				return -1
			}
			return 1
		}
	}
	return len(ua) - len(ub)
}

func pointerOf(v any) uintptr {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.Pointer()
	}
	return 0
}

func canonicalizeBindingJSON(value any, path string, depth int, seen map[uintptr]bool, props *int) (any, error) {
	switch v := value.(type) {
	case nil, string, bool:
		return v, nil
	case int:
		return float64(v), nil
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, fmt.Errorf("%s must contain only finite JSON numbers.", path)
		}
		return v, nil
	case []any, map[string]any, *jsObject:
	default:
		return nil, fmt.Errorf("%s must contain only plain JSON values.", path)
	}
	if depth > maxExtensionBindingsDepth {
		return nil, fmt.Errorf("%s exceeds the maximum nesting depth of %d.", path, maxExtensionBindingsDepth)
	}
	if p := pointerOf(value); p != 0 {
		if seen[p] {
			return nil, fmt.Errorf("%s must not contain cycles.", path)
		}
		seen[p] = true
		defer delete(seen, p)
	}
	if arr, ok := value.([]any); ok {
		out := make([]any, 0, len(arr))
		for i, e := range arr {
			c, err := canonicalizeBindingJSON(e, fmt.Sprintf("%s[%d]", path, i), depth+1, seen, props)
			if err != nil {
				return nil, err
			}
			out = append(out, c)
		}
		return out, nil
	}
	var keys []string
	var get func(string) any
	switch o := value.(type) {
	case *jsObject:
		keys, get = o.keys, func(k string) any { return o.vals[k] }
	case map[string]any:
		for k := range o {
			keys = append(keys, k)
		}
		get = func(k string) any { return o[k] }
	}
	keys = append([]string(nil), keys...)
	sort.SliceStable(keys, func(i, j int) bool { return compareUTF16(keys[i], keys[j]) < 0 })
	out := newObject()
	for _, k := range keys {
		*props++
		if *props > maxExtensionBindingsProperties {
			return nil, fmt.Errorf("extensionBindings exceeds %d total properties.", maxExtensionBindingsProperties)
		}
		c, err := canonicalizeBindingJSON(get(k), path+"."+k, depth+1, seen, props)
		if err != nil {
			return nil, err
		}
		out.set(k, c)
	}
	return out, nil
}

// normalizeExtensionBindings validates and canonicalizes the bindings of a call; nil input (JavaScript undefined) gives nil.
func normalizeExtensionBindings(input any) (*normalizedExtensionBindings, error) {
	if input == nil {
		return nil, nil
	}
	var keys []string
	switch o := input.(type) {
	case *jsObject:
		keys = o.order()
	case map[string]any:
		for k := range o {
			keys = append(keys, k)
		}
		sort.Strings(keys)
	default:
		return nil, fmt.Errorf("extensionBindings must be a plain JSON object.")
	}
	if len(keys) > maxExtensionBindingNamespaces {
		return nil, fmt.Errorf("extensionBindings supports at most %d namespaces.", maxExtensionBindingNamespaces)
	}
	for _, k := range keys {
		if !extensionBindingNamespaceRe.MatchString(k) {
			return nil, fmt.Errorf("extensionBindings namespace '%s' must use a package-like name followed by '/<positive-version>'.", k)
		}
	}
	props := 0
	value, err := canonicalizeBindingJSON(input, "extensionBindings", 0, map[uintptr]bool{}, &props)
	if err != nil {
		return nil, err
	}
	text := marshalJSON(value, "")
	if len(text) > maxExtensionBindingsBytes {
		return nil, fmt.Errorf("extensionBindings canonical JSON is %d bytes; maximum is %d.", len(text), maxExtensionBindingsBytes)
	}
	return &normalizedExtensionBindings{value: value, json: text}, nil
}

// extensionBindingsChildEnv is the environment the in-process child launch of the original sets for the bindings: for the
// runner host the canonical JSON (a nil entry clears the variable when there are none), for the parent host nothing.
func extensionBindingsChildEnv(host string, bindings *normalizedExtensionBindings) map[string]*string {
	if host != "runner" {
		return map[string]*string{}
	}
	if bindings == nil {
		return map[string]*string{extensionBindingsEnv: nil}
	}
	return map[string]*string{extensionBindingsEnv: &bindings.json}
}

// omitExtensionBindingsEnv removes ambient bindings from an environment handed to an external runner.
func omitExtensionBindingsEnv(env map[string]string) map[string]string {
	out := make(map[string]string, len(env))
	for k, v := range env {
		if k != extensionBindingsEnv {
			out[k] = v
		}
	}
	return out
}

const launchBindingProjectionVersion = 2

// stableJSON is launch-contract.ts stableJson: keys sorted by localeCompare, undefined entries left out.
func stableJSON(value any) string {
	switch v := value.(type) {
	case []any:
		parts := make([]string, len(v))
		for i, e := range v {
			parts[i] = stableJSON(e)
		}
		return "[" + strings.Join(parts, ",") + "]"
	case []string:
		parts := make([]string, len(v))
		for i, e := range v {
			parts[i] = stableJSON(e)
		}
		return "[" + strings.Join(parts, ",") + "]"
	case map[string]any:
		var keys []string
		for k, e := range v {
			if _, u := e.(undef); !u {
				keys = append(keys, k)
			}
		}
		sort.SliceStable(keys, func(i, j int) bool { return localeCompare(keys[i], keys[j]) < 0 })
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = marshalJSON(k, "") + ":" + stableJSON(v[k])
		}
		return "{" + strings.Join(parts, ",") + "}"
	case *jsObject:
		var keys []string
		for _, k := range v.order() {
			if v.has(k) {
				keys = append(keys, k)
			}
		}
		sort.SliceStable(keys, func(i, j int) bool { return localeCompare(keys[i], keys[j]) < 0 })
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = marshalJSON(k, "") + ":" + stableJSON(v.vals[k])
		}
		return "{" + strings.Join(parts, ",") + "}"
	case undef:
		return "null"
	}
	return marshalJSON(value, "")
}

func stableJSONDigest(value any) string {
	sum := sha256.Sum256([]byte(stableJSON(value)))
	return hex.EncodeToString(sum[:])
}

// launchBindingInput is LaunchBindingInput: nil pointers and slices are the fields a launch does not set.
type launchBindingInput struct {
	DefinitionDigest                                                string
	Task                                                            *string
	Model                                                           string
	Fast                                                            *bool
	Thinking                                                        string
	SystemPrompt                                                    *string
	SystemPromptMode                                                string
	InheritProjectContext, InheritGlobalContext, InheritSkills      bool
	Skills, Tools, ExcludeTools, Extensions, SubagentOnlyExtensions []string
	McpDirectTools                                                  []string
	OutputPath, OutputMode                                          string
	StructuredOutputSchema                                          any
	ExtensionBindings                                               *normalizedExtensionBindings
}

// projectLaunchBinding is the canonical projection of the resolved inputs handed to the child.
func projectLaunchBinding(in launchBindingInput) map[string]any {
	out := map[string]any{
		"version":               float64(launchBindingProjectionVersion),
		"definitionDigest":      in.DefinitionDigest,
		"inheritProjectContext": in.InheritProjectContext,
		"inheritGlobalContext":  in.InheritGlobalContext,
		"inheritSkills":         in.InheritSkills,
	}
	setStr := func(k, v string) {
		if v != "" {
			out[k] = v
		}
	}
	setList := func(k string, v []string) {
		if v != nil {
			out[k] = v
		}
	}
	if in.Task != nil {
		out["taskDigest"] = stableJSONDigest(*in.Task)
	}
	setStr("model", in.Model)
	if in.Fast != nil {
		out["fast"] = *in.Fast
	}
	setStr("thinking", in.Thinking)
	if in.SystemPrompt != nil {
		out["systemPromptDigest"] = stableJSONDigest(*in.SystemPrompt)
	}
	setStr("systemPromptMode", in.SystemPromptMode)
	setList("skills", in.Skills)
	setList("tools", in.Tools)
	setList("excludeTools", in.ExcludeTools)
	setList("extensions", in.Extensions)
	setList("subagentOnlyExtensions", in.SubagentOnlyExtensions)
	setList("mcpDirectTools", in.McpDirectTools)
	setStr("outputPath", in.OutputPath)
	setStr("outputMode", in.OutputMode)
	if in.StructuredOutputSchema != nil {
		out["structuredOutputSchema"] = in.StructuredOutputSchema
	}
	if in.ExtensionBindings != nil {
		out["extensionBindings"] = in.ExtensionBindings.value
	}
	return out
}

func launchBindingDigest(in launchBindingInput) string {
	return stableJSONDigest(projectLaunchBinding(in))
}
