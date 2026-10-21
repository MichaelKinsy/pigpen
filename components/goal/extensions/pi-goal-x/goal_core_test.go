package pi_goal_x

import (
	"regexp"
	"testing"
)

const gcF = "goal-core"

func TestGoalCoreTwins(t *testing.T) {
	tw(t, gcF, "displayObjectiveTitle strips goal block boilerplate", func(t *testing.T) {
		eq(t, displayObjectiveTitle("=== Goal ===\nObjective: Build tests first\nSuccess criteria: pass"), "Build tests first", "goal")
		eq(t, displayObjectiveTitle("=== Sisyphus Goal ===\n目标：严格执行三步\nSteps:\n1. x"), "严格执行三步", "sisyphus")
		eq(t, displayObjectiveTitle("Just a plain objective"), "Just a plain objective", "plain")
	})
	tw(t, gcF, "formatters preserve existing compact duration/token/status behavior", func(t *testing.T) {
		eq(t, formatDuration(-10), "0s", "neg")
		eq(t, formatDuration(65), "1m05s", "65")
		eq(t, formatDuration(3661), "1h01m01s", "3661")
		eq(t, formatTokenValue(999), "999 tokens", "999")
		eq(t, formatTokenValue(1200), "1.2K (1,200) tokens", "1200")
		eq(t, formatTokenValue(12000), "12K (12,000) tokens", "12000")
		eq(t, formatTokenValue(2_500_000), "2.5M (2,500,000) tokens", "2.5M")
		eq(t, truncateText(" a\n b\t c ", 20), "a b c", "collapse")
		eq(t, truncateText("abcdefghij", 8), "abcde...", "cut")
	})
	tw(t, gcF, "goal display helpers derive labels and footer", func(t *testing.T) {
		g := jo(t, `{"objective":"=== Goal ===\nObjective: Build test scaffolding and split helpers","status":"active","autoContinue":true,"usage":{"activeSeconds":125,"tokensUsed":4500},"sisyphus":false}`)
		eq(t, statusLabel(g), "running", "label")
		if !regexp.MustCompile(`^goal: running \[2m05s 4.5K\] - === Goal === Objective:`).MatchString(footerStatus(g)) {
			t.Fatalf("footer %q", footerStatus(g))
		}
		s := g.clone()
		s.set("sisyphus", true)
		eq(t, statusLabel(s), "sisyphus running", "sisyphus")
		p := g.clone()
		p.set("status", "paused")
		p.set("stopReason", "agent")
		eq(t, statusLabel(p), "paused (agent)", "paused")
	})
}
