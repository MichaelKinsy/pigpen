package ponytail

import (
	_ "embed"
	"regexp"
	"strings"
)

// The ruleset injected into the system prompt. upstream: hooks/ponytail-instructions.js.
//
// The original reads skills/ponytail/SKILL.md at run time and falls back to a built-in copy when it cannot; this
// port embeds the shipped Skill, which cannot be missing, so there is no fallback.

//go:embed ponytail_skill.md
var skillBody []byte

const jsSpace = `[\s\x{00a0}\x{feff}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}]`

var (
	frontmatterRe = regexp.MustCompile(`(?s)^---.*?---` + jsSpace + `*`)
	tableRowRe    = regexp.MustCompile(`^\|` + jsSpace + `*\*\*(.+?)\*\*` + jsSpace + `*\|`)
	exampleRe     = regexp.MustCompile(`^-` + jsSpace + `*([^:]+):` + jsSpace + `*"`)
	lineBreakRe   = regexp.MustCompile(`\r?\n`)
)

// filterSkillBodyForMode drops the frontmatter and keeps, of the per-level lines, only those of the mode: the rows of
// the intensity table (`| **lite** | ... |`) and the worked examples (`- lite: "..."`). A rule bullet whose label is
// not a mode name, or whose value is not quoted, is an ordinary line and stays.
func filterSkillBodyForMode(body, mode string) string {
	effective := normalizeMode(mode)
	if effective == "" {
		effective = defaultMode
	}
	body = frontmatterRe.ReplaceAllString(body, "")
	var kept []string
	for _, line := range lineBreakRe.Split(body, -1) {
		if m := tableRowRe.FindStringSubmatch(line); m != nil {
			if lm := normalizeMode(jsTrim(m[1])); lm != "" && lm != effective {
				continue
			} else if lm != "" {
				kept = append(kept, line)
				continue
			}
		}
		if m := exampleRe.FindStringSubmatch(line); m != nil {
			if lm := normalizeMode(jsTrim(m[1])); lm != "" && lm != effective {
				continue
			}
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// getPonytailInstructions is the text added to the system prompt for a mode. review is defined by its own Skill.
func getPonytailInstructions(mode string) string {
	configured := normalizePersistedMode(mode)
	if configured == "" {
		configured = defaultMode
	}
	if configured == "review" {
		return "PONYTAIL MODE ACTIVE — level: review. Behavior defined by /ponytail-review skill."
	}
	effective := normalizeMode(configured)
	if effective == "" {
		effective = defaultMode
	}
	return "PONYTAIL MODE ACTIVE — level: " + effective + "\n\n" + filterSkillBodyForMode(string(skillBody), effective)
}
