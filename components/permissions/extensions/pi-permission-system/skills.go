package pi_permission_system

import (
	"regexp"
	"strings"
)

// The catalogue parser of src/exposure/skill-prompt-sanitizer.ts (parseAllSkillPromptSections); the sanitizer around it is in
// skillpromptsanitizer.go.

type skillPromptEntry struct{ name, description, location string }

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
