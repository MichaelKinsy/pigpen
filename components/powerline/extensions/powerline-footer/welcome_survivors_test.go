package powerline_footer

import "testing"

// Skills and prompt templates are counted separately: with a different number of each, counting skills by
// the prompt source gives the wrong pair.
func TestCountLoadedCommandsCountsSkillsBySkillSource(t *testing.T) {
	cmds := []slashCommand{
		{"a", "skill"}, {"b", "skill"}, {"c", "skill"}, {"d", "skill"}, {"e", "skill"},
		{"p", "prompt"},
		{"x", "extension"},
	}
	skills, prompts := countLoadedCommands(cmds)
	if skills != 5 || prompts != 1 {
		t.Fatalf("skills, prompts = %d, %d; want 5, 1", skills, prompts)
	}
	skills, prompts = countLoadedCommands([]slashCommand{{"p", "prompt"}, {"q", "prompt"}})
	if skills != 0 || prompts != 2 {
		t.Fatalf("prompt-only: skills, prompts = %d, %d; want 0, 2", skills, prompts)
	}
}
