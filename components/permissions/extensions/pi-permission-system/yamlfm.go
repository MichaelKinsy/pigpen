package pi_permission_system

import (
	"regexp"
	"strings"
)

// Port of src/config/yaml-frontmatter.ts: the simple YAML subset the agent files' frontmatter uses.

var crlfRe = regexp.MustCompile(`\r\n`)
var lineSplitRe = regexp.MustCompile(`\r?\n`)

// parseSimpleYamlMap reads `key: value` lines; a key with no value opens a nested map for the lines indented under it. Values are
// strings with one pair of surrounding quotes removed; lines without a colon after position 0 and comment lines are skipped.
// The result's "__proto__" key is dropped as an assignment on a JavaScript object would not create it.
func parseSimpleYamlMap(input string) *jsObject {
	type node struct {
		indent int
		target *jsObject
	}
	root := newObject()
	stack := []node{{-1, root}}
	for _, rawLine := range lineSplitRe.Split(input, -1) {
		trimmed := jsTrim(rawLine)
		if trimmed == "" {
			continue
		}
		startTrimmed := strings.TrimLeftFunc(rawLine, isJSSpace)
		if strings.HasPrefix(startTrimmed, "#") {
			continue
		}
		indent := utf16Len(rawLine) - utf16Len(startTrimmed)
		line := trimmed
		sep := strings.IndexByte(line, ':')
		if sep <= 0 {
			continue
		}
		key := jsTrim(line[:sep])
		key = stripOneQuotePair(key)
		rawValue := jsTrim(line[sep+1:])
		for len(stack) > 1 && indent <= stack[len(stack)-1].indent {
			stack = stack[:len(stack)-1]
		}
		current := stack[len(stack)-1].target
		if rawValue == "" {
			child := newObject()
			setOwn(current, key, child)
			stack = append(stack, node{indent, child})
			continue
		}
		scalar := rawValue
		if (strings.HasPrefix(scalar, `"`) && strings.HasSuffix(scalar, `"`)) || (strings.HasPrefix(scalar, "'") && strings.HasSuffix(scalar, "'")) {
			// slice(1, -1) of a lone quote is the empty string
			if len(scalar) >= 2 {
				scalar = scalar[1 : len(scalar)-1]
			} else {
				scalar = ""
			}
		}
		setOwn(current, key, scalar)
	}
	return root
}

// stripOneQuotePair is .replace(/^['"]|['"]$/g, ""): a leading and a trailing quote each go (one leading, one trailing).
func stripOneQuotePair(s string) string {
	if s != "" && (s[0] == '\'' || s[0] == '"') {
		s = s[1:]
	}
	if s != "" && (s[len(s)-1] == '\'' || s[len(s)-1] == '"') {
		s = s[:len(s)-1]
	}
	return s
}

// setOwn is `target[key] = value` on a plain object: "__proto__" sets the prototype instead of creating a key.
func setOwn(o *jsObject, key string, v any) {
	if key == "__proto__" {
		return
	}
	o.set(key, v)
}

func utf16Len(s string) int {
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

// extractFrontmatter returns the text between a leading `---` line and the next `\n---`, "" when there is none.
func extractFrontmatter(markdown string) string {
	normalized := crlfRe.ReplaceAllString(markdown, "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return ""
	}
	end := strings.Index(normalized[4:], "\n---")
	if end == -1 {
		return ""
	}
	return normalized[4 : 4+end]
}
