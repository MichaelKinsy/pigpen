package pi_goal_x

import (
	"strings"
	"testing"
)

// Twins of tests/goal-audit-dashboard.test.ts (plan §19.7).

func auditProgressOf(mod func(p *auditorProgress)) auditorProgress {
	p := auditorProgress{recentOutput: []string{}, phase: "running", elapsedMs: 138000}
	if mod != nil {
		mod(&p)
	}
	return p
}

func auditCheckState(model *auditorDashboardModel, id string) string {
	for _, c := range model.checks {
		if c.id == id {
			return c.state
		}
	}
	return "pending"
}

func TestGoalAuditDashboard(t *testing.T) {
	f := "goal-audit-dashboard"
	theme := wgTestTheme
	derive := deriveAuditorDashboardModel
	tw(t, f, "objective: pending → running → passed as the audit advances", func(t *testing.T) {
		start := derive(auditProgressOf(func(p *auditorProgress) { p.percentage = nil; p.label = "Starting audit..." }), deriveAuditorDashboardOptions{})
		eq(t, auditCheckState(start, "objective"), "running", "objective")
		eq(t, auditCheckState(start, "verification"), "pending", "verification")
		eq(t, auditCheckState(start, "decision"), "pending", "decision")
		early := derive(auditProgressOf(func(p *auditorProgress) { p.percentage = fp(10) }), deriveAuditorDashboardOptions{})
		eq(t, auditCheckState(early, "objective"), "running", "10%")
		mid := derive(auditProgressOf(func(p *auditorProgress) { p.percentage = fp(20) }), deriveAuditorDashboardOptions{})
		eq(t, auditCheckState(mid, "objective"), "passed", "objective at 20%")
		eq(t, auditCheckState(mid, "verification"), "running", "verification at 20%")
	})
	tw(t, f, "all five stages advance in order through percentage bands (§15.2)", func(t *testing.T) {
		cases := []struct {
			pct      float64
			expected []string
		}{
			{0, []string{"running", "pending", "pending", "pending", "pending"}},
			{30, []string{"passed", "running", "pending", "pending", "pending"}},
			{50, []string{"passed", "passed", "running", "pending", "pending"}},
			{72, []string{"passed", "passed", "passed", "running", "pending"}},
			{90, []string{"passed", "passed", "passed", "passed", "running"}},
		}
		for _, c := range cases {
			model := derive(auditProgressOf(func(p *auditorProgress) { p.percentage = fp(c.pct) }), deriveAuditorDashboardOptions{})
			states := []string{}
			for _, ch := range model.checks {
				states = append(states, ch.state)
			}
			eq(t, states, c.expected, "states at "+jsNumber(c.pct)+"%")
		}
	})
	tw(t, f, "decision passes on approval and fails on disapproval/error", func(t *testing.T) {
		done := func(p *auditorProgress) { p.phase = "done"; p.percentage = fp(100) }
		approved := derive(auditProgressOf(done), deriveAuditorDashboardOptions{verdict: "approved"})
		states := []string{}
		for _, ch := range approved.checks {
			states = append(states, ch.state)
		}
		eq(t, states, []string{"passed", "passed", "passed", "passed", "passed"}, "approved")
		eq(t, approved.active, false, "active")
		rejected := derive(auditProgressOf(done), deriveAuditorDashboardOptions{verdict: "disapproved"})
		eq(t, auditCheckState(rejected, "decision"), "failed", "rejected decision")
		prior := []string{}
		for _, ch := range rejected.checks[:4] {
			prior = append(prior, ch.state)
		}
		eq(t, prior, []string{"passed", "passed", "passed", "passed"}, "rejection keeps prior stages passed")
		errored := derive(auditProgressOf(done), deriveAuditorDashboardOptions{verdict: "error"})
		eq(t, auditCheckState(errored, "decision"), "failed", "errored decision")
	})
	tw(t, f, "auditor identity is shown in the model and the rendered header", func(t *testing.T) {
		model := derive(auditProgressOf(nil), deriveAuditorDashboardOptions{auditorLabel: "anthropic/claude-sonnet:high"})
		eq(t, model.auditorLabel, "anthropic/claude-sonnet:high", "model label")
		lines := renderAuditorDashboard(model, theme, 100, false)
		reMatch(t, `anthropic/claude-sonnet:high`, lines[0])
		reMatch(t, `2m18s`, lines[0])
	})
	tw(t, f, "percentage clamps to [0, 100]", func(t *testing.T) {
		negative := derive(auditProgressOf(func(p *auditorProgress) { p.percentage = fp(-20) }), deriveAuditorDashboardOptions{})
		eq(t, *negative.percentage, 0, "negative")
		over := derive(auditProgressOf(func(p *auditorProgress) { p.percentage = fp(150) }), deriveAuditorDashboardOptions{})
		eq(t, *over.percentage, 100, "over")
	})
	tw(t, f, "expanded diagnostics retain tool details only when requested", func(t *testing.T) {
		p := auditProgressOf(func(p *auditorProgress) {
			p.currentTool = "read"
			p.currentToolArgs = `{"path":"src/parser.ts"}`
			p.recentOutput = []string{"checking file exists..."}
		})
		compact := strings.Join(renderAuditorDashboard(derive(p, deriveAuditorDashboardOptions{}), theme, 100, false), "\n")
		reNoMatch(t, `tool read`, compact)
		reNoMatch(t, `checking file exists`, compact)
		expanded := strings.Join(renderAuditorDashboard(derive(p, deriveAuditorDashboardOptions{}), theme, 100, true), "\n")
		reMatch(t, `tool read`, expanded)
		reMatch(t, `src/parser\.ts`, expanded)
		reMatch(t, `checking file exists`, expanded)
	})
	tw(t, f, "failed audits show diagnostics automatically", func(t *testing.T) {
		p := auditProgressOf(func(p *auditorProgress) {
			p.phase = "done"
			p.recentOutput = []string{"ERROR: cannot read package.json"}
		})
		model := derive(p, deriveAuditorDashboardOptions{verdict: "error"})
		reMatch(t, `ERROR: cannot read package.json`, strings.Join(renderAuditorDashboard(model, theme, 100, false), "\n"))
	})
	tw(t, f, "no rendered line exceeds the terminal width at 40..140 cols", func(t *testing.T) {
		p := auditProgressOf(func(p *auditorProgress) {
			p.percentage = fp(72)
			p.currentTool = "read"
			p.currentToolArgs = strings.Repeat("x", 80)
			p.recentOutput = []string{strings.Repeat("y", 120)}
		})
		for _, width := range []int{40, 50, 60, 80, 100, 140} {
			for _, showToolDetails := range []bool{false, true} {
				for _, line := range renderAuditorDashboard(derive(p, deriveAuditorDashboardOptions{}), theme, width, showToolDetails) {
					if visibleWidth(line) > width {
						t.Errorf("line exceeds %d: %q", width, line)
					}
				}
			}
			for _, line := range renderAuditResultCard(deriveAuditResultCard("disapproved", strings.Repeat("fix the tests", 30)), theme, width) {
				if visibleWidth(line) > width {
					t.Errorf("card line exceeds %d: %q", width, line)
				}
			}
		}
	})
	tw(t, f, "result cards: approval, changes-required, error (§15.4)", func(t *testing.T) {
		approved := deriveAuditResultCard("approved", "ok")
		eq(t, approved.label, "APPROVED", "label")
		eq(t, approved.lines, []string{"Objective satisfied.", "Verification requirements satisfied.", "Required tasks and evidence accepted."}, "lines")
		reMatch(t, `Audit result ─ APPROVED`, strings.Join(renderAuditResultCard(approved, theme, 100), "\n"))

		rejected := deriveAuditResultCard("disapproved", strings.Join([]string{
			"Audit report:",
			"- Tests were not run after the final implementation change.",
			"- Task \"Update documentation\" has no completion evidence.",
			"<disapproved/>",
		}, "\n"))
		eq(t, rejected.label, "CHANGES REQUIRED", "label")
		eq(t, rejected.lines, []string{
			"Tests were not run after the final implementation change.",
			"Task \"Update documentation\" has no completion evidence.",
		}, "rejected lines")
		reMatch(t, `✗ Tests were not run after the final implementation change[.]`, strings.Join(renderAuditResultCard(rejected, theme, 100), "\n"))

		errored := deriveAuditResultCard("error", "timeout")
		eq(t, errored.label, "ERROR", "error label")
	})
	tw(t, f, "the normal dashboard returns after the audit result card clears", func(t *testing.T) {
		tui, _ := createMockTUI()
		auditResult := &auditResultView{"disapproved", "- Tests were not run."}
		c := newWidget(tui, func() *jsObject {
			return wgObj("id", "g1", "createdAt", "2026-01-01T00:00:00.000Z", "updatedAt", "2026-01-01T00:00:00.000Z",
				"objective", "=== Goal ===\nObjective: Fix the parser", "status", "active", "autoContinue", true,
				"usage", wgObj("activeSeconds", 10.0, "tokensUsed", 100.0), "sisyphus", false, "activePath", ".pi/goals/active_goal_g1.md")
		}, func(o *goalWidgetOptions) { o.getAuditResult = func() *auditResultView { return auditResult } })
		cardText := strings.Join(c.Render(100), "\n")
		reMatch(t, `Audit result ─ CHANGES REQUIRED`, cardText)
		reMatch(t, `✗ Tests were not run`, cardText)
		auditResult = nil
		normal := strings.Join(c.Render(100), "\n")
		reMatch(t, `pi-goal-x ─ Fix the parser`, normal)
		reNoMatch(t, `Audit result`, normal)
	})
}
