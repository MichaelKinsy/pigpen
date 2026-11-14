package pi_permission_system

import (
	"path/filepath"
	"regexp"
	"strings"
)

// Port of the skill-read gate (src/handlers/gates/skill-read.ts) and what it reads: the skill catalogues of the system prompt
// (src/exposure/skill-prompt-sanitizer.ts: parseAllSkillPromptSections, visibleSkillPromptEntries, findSkillPathMatch) and the
// path cleanup it compares with (src/access-intent/path-normalization.ts normalizePathForComparison, POSIX flavor). A `read` of a
// file inside a listed skill is decided by the `skill` surface for that skill's name: the original asks when that surface asks,
// even when `read` itself is allowed.

type skillPromptEntry struct{ name, description, location string }

// skillEntry is a listed skill the policy does not deny, with its location and base directory made comparable.
type skillEntry struct {
	name, state       string
	location, baseDir string
}

var (
	skillBlockRegexp       = regexp.MustCompile(`<skill>((?s:.*?))</skill>`)
	skillNameRegexp        = regexp.MustCompile(`<name>((?s:.*?))</name>`)
	skillDescriptionRegexp = regexp.MustCompile(`<description>((?s:.*?))</description>`)
	skillLocationRegexp    = regexp.MustCompile(`<location>((?s:.*?))</location>`)
	xmlEntities            = strings.NewReplacer("&lt;", "<", "&gt;", ">", "&quot;", `"`, "&apos;", "'")
)

func decodeXML(s string) string {
	// The original replaces &amp; last, so "&amp;lt;" becomes "&lt;", not "<".
	return strings.ReplaceAll(xmlEntities.Replace(s), "&amp;", "&")
}

// parseAllSkillPromptSections returns the entries of every <available_skills> block, one slice per block.
func parseAllSkillPromptSections(prompt string) [][]skillPromptEntry {
	const open, close = "<available_skills>", "</available_skills>"
	var sections [][]skillPromptEntry
	for start := 0; start < len(prompt); {
		i := strings.Index(prompt[start:], open)
		if i < 0 {
			break
		}
		i += start
		j := strings.Index(prompt[i+len(open):], close)
		if j < 0 {
			break
		}
		j += i + len(open)
		var entries []skillPromptEntry
		for _, m := range skillBlockRegexp.FindAllStringSubmatch(prompt[i+len(open):j], -1) {
			name, description, location := skillNameRegexp.FindStringSubmatch(m[1]), skillDescriptionRegexp.FindStringSubmatch(m[1]),
				skillLocationRegexp.FindStringSubmatch(m[1])
			if name == nil || description == nil || location == nil {
				continue
			}
			e := skillPromptEntry{decodeXML(jsTrim(name[1])), decodeXML(jsTrim(description[1])), decodeXML(jsTrim(location[1]))}
			if e.name == "" || e.location == "" {
				continue
			}
			entries = append(entries, e)
		}
		sections = append(sections, entries)
		start = j + len(close)
	}
	return sections
}

var wrappingQuote = regexp.MustCompile(`^['"]|['"]$`)

// comparablePath is normalizePathForComparison on POSIX: trimmed, one wrapping quote stripped at each end, a leading @
// dropped, the home prefix expanded, then resolved against cwd. "" when nothing is left.
func comparablePath(value, cwd string) string {
	t := wrappingQuote.ReplaceAllString(jsTrim(value), "")
	if t == "" {
		return ""
	}
	t = expandHomePath(strings.TrimPrefix(t, "@"))
	if filepath.IsAbs(t) {
		return filepath.Clean(t)
	}
	return filepath.Join(cwd, t)
}

// isWithinDir is the isWithin of the platform's flavor: the directory itself or a path below it. The parent and
// its prefix are outside, judged with the platform's separator so a Windows `..` is caught too.
func isWithinDir(path, dir string) bool {
	if path == "" || dir == "" {
		return false
	}
	if path == dir {
		return true
	}
	rel, err := filepath.Rel(dir, path)
	if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// visibleSkillEntries lists the prompt's skills the policy does not deny, in catalogue order.
func (p *Policy) visibleSkillEntries(prompt, cwd string) []skillEntry {
	var out []skillEntry
	for _, section := range parseAllSkillPromptSections(prompt) {
		for _, e := range section {
			state := Evaluate("skill", e.name, p.rules, PosixPathFlavor, "").Action
			if state == "deny" {
				continue
			}
			out = append(out, skillEntry{name: e.name, state: state, location: comparablePath(e.location, cwd),
				baseDir: comparablePath(filepath.Dir(e.location), cwd)})
		}
	}
	return out
}

// findSkillPathMatch is the skill whose location is the path, or else the one with the longest base directory holding it.
func findSkillPathMatch(path string, entries []skillEntry) *skillEntry {
	if path == "" {
		return nil
	}
	for i := range entries {
		if entries[i].location != "" && path == entries[i].location {
			return &entries[i]
		}
	}
	var best *skillEntry
	for i := range entries {
		if !isWithinDir(path, entries[i].baseDir) {
			continue
		}
		if best == nil || len(entries[i].baseDir) > len(best.baseDir) {
			best = &entries[i]
		}
	}
	return best
}

// skillRead is the skill-read gate for a `read` call: an ask when the file belongs to a listed skill the policy
// asks about (the entry's state is pre-resolved, describeSkillReadGate preResolved). The gate carries no session
// approval in the original, so the ask offers none.
func skillRead(tool string, input map[string]any, entries []skillEntry, cwd string) Verdict {
	if tool != "read" || len(entries) == 0 {
		return Verdict{}
	}
	path, _ := input["path"].(string)
	if path == "" {
		return Verdict{}
	}
	m := findSkillPathMatch(comparablePath(path, cwd), entries)
	if m == nil || m.state != "ask" {
		return Verdict{}
	}
	a := skillAsk(m.name, "read")
	return Verdict{Ask: &a}
}

// promptText is the system prompt of a before_agent_start event, a string or a list of strings.
func promptText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		parts := make([]string, 0, len(x))
		for _, s := range x {
			if t, ok := s.(string); ok {
				parts = append(parts, t)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}
