package pi_subagents

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Port of src/runs/shared/permissions.ts.

// PermissionRules maps a tool name to "allow", "ask" or "deny".
type permissionRules map[string]string

var permissionInternalTools = setOf("contact_supervisor", "intercom", "bg_wait", "structured_output")

// validatePermissionRules is the original's validatePermissionRules: nil (undefined) when value is unset or has no
// rule, else the rules; the error text is the original's.
func validatePermissionRules(value any, label string) (permissionRules, error) {
	if isUndefined(value) {
		return nil, nil
	}
	obj, ok := asObject(value)
	if !ok {
		return nil, fmt.Errorf("%s must be an object mapping tool names to allow, ask, or deny.", label)
	}
	result := permissionRules{}
	for _, tool := range obj.order() {
		decision := obj.vals[tool]
		if jsTrim(tool) == "" {
			return nil, fmt.Errorf("%s contains an empty tool name.", label)
		}
		if tool == "bash" {
			return nil, fmt.Errorf("%s.bash is unsupported; pi-subagents leaves bash policy to pi-guard.", label)
		}
		if permissionInternalTools[tool] {
			return nil, fmt.Errorf("%s.%s is reserved for child coordination and cannot be gated.", label, tool)
		}
		d, _ := decision.(string)
		if d != "allow" && d != "ask" && d != "deny" {
			return nil, fmt.Errorf("%s.%s must be allow, ask, or deny.", label, tool)
		}
		result[tool] = d
	}
	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}

// permissionConfig is the original's PermissionConfig.
type permissionConfig struct{ Rules permissionRules }

// validatePermissionConfig is the original's: nil when unset; label is "config.permissions" in the original's default call.
func validatePermissionConfig(value any, label string) (*permissionConfig, error) {
	if isUndefined(value) {
		return nil, nil
	}
	obj, ok := asObject(value)
	if !ok {
		return nil, fmt.Errorf("%s must be an object.", label)
	}
	var unknown []string
	for _, k := range obj.order() {
		if k != "rules" {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		return nil, fmt.Errorf("%s has unsupported fields: %s.", label, strings.Join(unknown, ", "))
	}
	rules, err := validatePermissionRules(obj.vals["rules"], label+".rules")
	if err != nil {
		return nil, err
	}
	return &permissionConfig{Rules: rules}, nil
}

// resolvePermissionRules merges the global config's rules with the agent's (the agent wins) and drops every allow rule.
func resolvePermissionRules(global *permissionConfig, agent permissionRules) permissionRules {
	merged := permissionRules{}
	if global != nil {
		for k, v := range global.Rules {
			merged[k] = v
		}
	}
	for k, v := range agent {
		merged[k] = v
	}
	for k, v := range merged {
		if v == "allow" {
			delete(merged, k)
		}
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

// permissionDecision is "allow" for bash, the coordination tools and any tool without a rule.
func permissionDecision(rules permissionRules, tool string) string {
	if tool == "bash" || permissionInternalTools[tool] {
		return "allow"
	}
	if d, ok := rules[tool]; ok {
		return d
	}
	return "allow"
}

const maxPermissionPreviewBytes = 2048

var (
	permissionSecretKey   = regexp.MustCompile(`(?i)(?:authorization|cookie|credential|password|secret|token|api[-_]?key)`)
	permissionSecretValue = regexp.MustCompile(`(?i)\b(?:Bearer\s+\S+|(?:sk|ghp|github_pat|xox[baprs])[-_A-Za-z0-9]{8,})\b`)
)

// redactSecretValues replaces bearer tokens and well-known key shapes with "[redacted]".
func redactSecretValues(value string) string {
	return permissionSecretValue.ReplaceAllString(value, "[redacted]")
}

func redactPermissionValue(value any, key string, depth int) any {
	if permissionSecretKey.MatchString(key) {
		return "[redacted]"
	}
	if depth >= 3 {
		return "[truncated]"
	}
	if arr, ok := asArray(value); ok {
		if len(arr) > 10 {
			arr = arr[:10]
		}
		out := make([]any, len(arr))
		for i, item := range arr {
			out[i] = redactPermissionValue(item, "", depth+1)
		}
		return out
	}
	if obj, ok := asObject(value); ok {
		out := newObject()
		for i, k := range obj.order() {
			if i >= 20 {
				break
			}
			out.set(k, redactPermissionValue(obj.vals[k], k, depth+1))
		}
		return out
	}
	if s, ok := value.(string); ok {
		r := redactSecretValues(s)
		if u := utf16.Encode([]rune(r)); len(u) > 500 {
			return string(utf16.Decode(u[:500])) + "…"
		}
		return r
	}
	return value
}

// permissionArgsPreview is the redacted JSON of a tool call's arguments, bounded to 2048 bytes (cut at a character, then "…").
func permissionArgsPreview(input any) string {
	serialized := marshalJSON(redactPermissionValue(input, "", 0), "")
	if len(serialized) <= maxPermissionPreviewBytes {
		return serialized
	}
	max := maxPermissionPreviewBytes - len("…")
	n := 0
	for i, r := range serialized {
		if i+utf8.RuneLen(r) > max {
			break
		}
		n = i + utf8.RuneLen(r)
	}
	return serialized[:n] + "…"
}

// appendPermissionAudit appends one JSON line to the audit file (nothing for an empty path).
func appendPermissionAudit(filePath string, record *jsObject) error {
	if filePath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(filePath), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(marshalJSON(record, "") + "\n")
	return err
}
