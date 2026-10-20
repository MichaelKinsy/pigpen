package superpowers

import (
	_ "embed"
	"regexp"
	"strings"
	"sync"
)

// upstream: .pi/extensions/superpowers.ts
const (
	extremelyImportantMarker = "<EXTREMELY_IMPORTANT>"
	bootstrapMarker          = "superpowers:using-superpowers bootstrap for pi"
)

// The original reads skills/using-superpowers/SKILL.md at run time and falls back to no injection when it
// cannot. This port embeds the shipped Skill (built by port/adapt.py from the original with the Package's
// Skill names), which cannot be missing, so there is no fallback.
//
//go:embed bootstrap_skill.md
var bootstrapSkill string

// piToolMapping is the text of the original's piToolMapping() template literal, extracted by port/adapt.py.
//
//go:embed pi_tool_mapping.md
var piToolMapping string

var (
	bootstrapOnce  sync.Once
	bootstrapCache string
)

// bootstrapContent is the text of the injected message (cached, as the original's cachedBootstrap is).
func bootstrapContent() string {
	bootstrapOnce.Do(func() {
		bootstrapCache = extremelyImportantMarker + "\n" +
			bootstrapMarker + "\n\n" +
			"You have superpowers.\n\n" +
			"The using-superpowers skill content is included below and is already loaded for this Pi session. Follow it now. Do not try to load using-superpowers again.\n\n" +
			stripFrontmatter(bootstrapSkill) + "\n\n" +
			piToolMapping + "\n" +
			"</EXTREMELY_IMPORTANT>"
	})
	return bootstrapCache
}

var frontmatterRe = regexp.MustCompile(`(?s)^---\n.*?\n---\n(.*)$`)

// stripFrontmatter: content.match(/^---\n[\s\S]*?\n---\n([\s\S]*)$/), then .trim().
func stripFrontmatter(content string) string {
	if m := frontmatterRe.FindStringSubmatch(content); m != nil {
		return jsTrim(m[1])
	}
	return jsTrim(content)
}

// isJSSpace is ECMAScript's WhiteSpace and LineTerminator set, which String.prototype.trim strips. It is not
// unicode.IsSpace: that includes U+0085 and omits U+FEFF.
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', '\u00a0', '\u1680', '\u2028', '\u2029', '\u202f', '\u205f', '\u3000', '\ufeff':
		return true
	}
	return r >= '\u2000' && r <= '\u200a'
}

func jsTrim(s string) string { return strings.TrimFunc(s, isJSSpace) }

// messageContainsBootstrap: a string content that includes the marker, or a text part that does.
func messageContainsBootstrap(message any) bool {
	m, ok := message.(map[string]any)
	if !ok {
		return false
	}
	switch content := m["content"].(type) {
	case string:
		return strings.Contains(content, bootstrapMarker)
	case []any:
		for _, part := range content {
			p, ok := part.(map[string]any)
			if !ok {
				continue
			}
			if p["type"] != "text" {
				continue
			}
			if text, ok := p["text"].(string); ok && strings.Contains(text, bootstrapMarker) {
				return true
			}
		}
	}
	return false
}

// firstNonCompactionSummaryIndex is the index after the leading compactionSummary messages.
func firstNonCompactionSummaryIndex(messages []any) int {
	index := 0
	for index < len(messages) {
		m, ok := messages[index].(map[string]any)
		if !ok || m["role"] != "compactionSummary" {
			break
		}
		index++
	}
	return index
}
