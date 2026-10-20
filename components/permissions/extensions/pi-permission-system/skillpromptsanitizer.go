package pi_permission_system

// Port of src/exposure/skill-prompt-sanitizer.ts: the skills a system prompt lists, which of them policy does not deny, and which
// listed skill a path belongs to. The catalogue parser itself (parseAllSkillPromptSections, the unexported twin of the original's
// function the root gate reads) lives in skills.go.

// SkillPermissionChecker is the narrow interface for the raw (no session rules) permission checker used by skill prompt
// resolution. A nil agentName is undefined.
type SkillPermissionChecker interface {
	CheckPermission(surface string, input any, agentName *string) PermissionCheckResult
}

// ParsedSkillPromptEntry is one `<skill>` block of an `<available_skills>` catalogue.
type ParsedSkillPromptEntry struct{ Name, Description, Location string }

// SkillPromptSection is one `<available_skills>` catalogue.
type SkillPromptSection struct{ Entries []ParsedSkillPromptEntry }

// SkillPromptEntry is a listed skill with the policy state of its name and its location made comparable.
type SkillPromptEntry struct {
	Name, Description, Location string
	// State is "allow", "deny" or "ask".
	State              string
	NormalizedLocation string
	NormalizedBaseDir  string
}

// ParseAllSkillPromptSections parses every `<available_skills>` catalogue of a system prompt, in order.
func ParseAllSkillPromptSections(prompt string) []SkillPromptSection {
	sections := []SkillPromptSection{}
	for _, parsed := range parseAllSkillPromptSections(prompt) {
		entries := make([]ParsedSkillPromptEntry, len(parsed))
		for i, e := range parsed {
			entries[i] = ParsedSkillPromptEntry{Name: e.name, Description: e.description, Location: e.location}
		}
		sections = append(sections, SkillPromptSection{Entries: entries})
	}
	return sections
}

// resolveSkillState asks the checker once per skill name.
func resolveSkillState(skillName string, checker SkillPermissionChecker, agentName *string, cache map[string]string) string {
	if state, ok := cache[skillName]; ok && state != "" {
		return state
	}
	state := checker.CheckPermission("skill", skillNameInput(skillName), agentName).State
	cache[skillName] = state
	return state
}

func skillNameInput(skillName string) *jsObject {
	input := newObject()
	input.set("name", skillName)
	return input
}

// VisibleSkillPromptEntries lists the skills in the prompt's `<available_skills>` catalogues that policy does not deny, in
// catalogue order: what skill path matching may treat as a skill's files. It edits no prompt text.
func VisibleSkillPromptEntries(prompt string, checker SkillPermissionChecker, agentName *string, normalizer *PathNormalizer) []SkillPromptEntry {
	cache := map[string]string{}
	entries := []SkillPromptEntry{}
	for _, section := range ParseAllSkillPromptSections(prompt) {
		for _, entry := range section.Entries {
			state := resolveSkillState(entry.Name, checker, agentName, cache)
			if state == "deny" {
				continue
			}
			entries = append(entries, SkillPromptEntry{
				Name: entry.Name, Description: entry.Description, Location: entry.Location, State: state,
				NormalizedLocation: normalizer.ComparableValue(entry.Location),
				NormalizedBaseDir:  normalizer.ComparableValue(HostPath.Dirname(entry.Location)),
			})
		}
	}
	return entries
}

// WithoutDeniedSkills is the skills policy does not deny, judged by name, in their original order. Judging the skill list itself,
// rather than a catalogue rendered from it, keeps the answer independent of whether that catalogue has been rendered yet.
func WithoutDeniedSkills[T any](skills []T, nameOf func(T) string, checker SkillPermissionChecker, agentName *string) []T {
	cache := map[string]string{}
	kept := []T{}
	for _, skill := range skills {
		if resolveSkillState(nameOf(skill), checker, agentName, cache) != "deny" {
			kept = append(kept, skill)
		}
	}
	return kept
}

// FindSkillPathMatch is the listed skill a normalized path belongs to: the one whose location is the path, else the one with the
// longest base directory holding it. nil for none.
func FindSkillPathMatch(normalizedPath string, entries []SkillPromptEntry, normalizer *PathNormalizer) *SkillPromptEntry {
	if normalizedPath == "" || len(entries) == 0 {
		return nil
	}
	for i := range entries {
		if entries[i].NormalizedLocation != "" && normalizedPath == entries[i].NormalizedLocation {
			return &entries[i]
		}
	}
	var best *SkillPromptEntry
	for i := range entries {
		entry := &entries[i]
		if entry.NormalizedBaseDir == "" || !normalizer.IsWithinDirectory(normalizedPath, entry.NormalizedBaseDir) {
			continue
		}
		if best == nil || utf16LenWTF(entry.NormalizedBaseDir) > utf16LenWTF(best.NormalizedBaseDir) {
			best = entry
		}
	}
	return best
}
