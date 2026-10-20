package powerline_footer

import (
	"strings"
	"testing"
)

// Survivor-killing checks for working-vibes.ts. Uses code of the vibes slice only (working_vibes.go, helpers_test.go).

// working-vibes.ts:290: `.replace(/-+/g, "-")` collapses runs of dashes that were already in the theme.
func TestVibeSlugCollapsesDashRuns(t *testing.T) {
	for in, want := range map[string]string{
		"a--b":      "a-b",
		"a---b--c":  "a-b-c",
		"a - b":     "a-b",
		"x__--__y":  "x__-__y",
		"a--b!!c--": "a-b-c",
	} {
		eq(t, toVibeFileSlug(in), want, "slug of "+in)
	}
}

// working-vibes.ts:396: `ctx.userPrompt.slice(0, 100)`: exactly 100 UTF-16 units of the prompt reach the template.
func TestVibePromptTaskIsCutAt100(t *testing.T) {
	m := newVibeManager()
	m.config.PromptTemplate = "[{task}]"
	prompt := strings.Repeat("a", 100) + "b" + "c"
	eq(t, m.buildVibePrompt("t", prompt), "["+strings.Repeat("a", 100)+"]", "101st unit is dropped")
	eq(t, m.buildVibePrompt("t", strings.Repeat("a", 100)), "["+strings.Repeat("a", 100)+"]", "100 units stay whole")
}
