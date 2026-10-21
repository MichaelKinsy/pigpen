package pi_goal_x

import (
	"math"
	"regexp"
)

// ported from widgets/auditor-dashboard-model.ts: pure derivation of the structured audit dashboard from the auditor
// progress stream. Optional TS fields are "" / nil.

type auditCheck struct {
	id     string
	label  string
	state  string // pending | running | passed | failed
	detail string
}

type auditorDashboardModel struct {
	auditorLabel    string
	elapsedMs       float64
	percentage      *int
	checks          []auditCheck
	currentTool     string
	currentToolArgs string
	recentOutput    []string
	// verdict is "" when the audit has no result yet (approved | disapproved | error otherwise).
	verdict string
	active  bool
}

type auditResultCard struct {
	verdict string
	label   string
	lines   []string
}

var auditCheckLabels = []struct{ id, label string }{
	{"objective", "Objective and success criteria"},
	{"verification", "Verification contracts"},
	{"tasks", "Tasks and recorded evidence"},
	{"workspace", "Workspace inspection"},
	{"decision", "Final decision"},
}

type deriveAuditorDashboardOptions struct {
	// auditorLabel "" = default (progress label, then "Independent completion audit").
	auditorLabel string
	// verdict "" = not known.
	verdict string
}

// deriveAuditorDashboardModel: active audits advance through percentage bands (objective >=20%, verification >=40%,
// tasks >=60%, workspace >=80%, decision >=80%); a finished audit passes every check except decision, which follows the verdict.
func deriveAuditorDashboardModel(progress auditorProgress, options deriveAuditorDashboardOptions) *auditorDashboardModel {
	pct := auditClampPercentage(progress.percentage)
	done := progress.phase == "done"
	verdict := options.verdict

	checks := make([]auditCheck, 0, len(auditCheckLabels))
	for _, c := range auditCheckLabels {
		switch {
		case !done:
			checks = append(checks, auditCheck{id: c.id, label: c.label, state: auditCheckStateActive(c.id, pct)})
		case c.id == "decision":
			state := "passed"
			if verdict == "disapproved" || verdict == "error" {
				state = "failed"
			}
			checks = append(checks, auditCheck{id: c.id, label: c.label, state: state})
		default:
			checks = append(checks, auditCheck{id: c.id, label: c.label, state: "passed"})
		}
	}
	label := options.auditorLabel
	if label == "" {
		label = progress.label
	}
	if label == "" {
		label = "Independent completion audit"
	}
	m := &auditorDashboardModel{
		auditorLabel: label, elapsedMs: progress.elapsedMs, checks: checks,
		currentTool: progress.currentTool, currentToolArgs: progress.currentToolArgs,
		recentOutput: progress.recentOutput, verdict: verdict, active: !done,
	}
	if progress.percentage != nil {
		m.percentage = &pct
	}
	return m
}

func auditCheckStateActive(id string, pct int) string {
	pick := func(passAt, runAt int, runFallback string) string {
		switch {
		case pct >= passAt:
			return "passed"
		case pct >= runAt:
			return "running"
		}
		return runFallback
	}
	switch id {
	case "objective":
		if pct >= 20 {
			return "passed"
		}
		return "running"
	case "verification":
		return pick(40, 20, "pending")
	case "tasks":
		return pick(60, 40, "pending")
	case "workspace":
		return pick(80, 60, "pending")
	}
	if pct >= 80 {
		return "running"
	}
	return "pending"
}

func auditClampPercentage(value *float64) int {
	if value == nil {
		return 0
	}
	return int(math.Min(100, math.Max(0, jsRound(*value))))
}

// auditorFormatAuditElapsed is the auditor-dashboard-model.ts copy of formatAuditElapsed (floors without clamping at 0).
// The TS name collides with the model module's export; the renderer uses the model one.
func auditorFormatAuditElapsed(elapsedMs float64) string {
	return formatDashboardDuration(math.Floor(elapsedMs / 1000))
}

func auditOneLine(value string) string { return jsTrim(wsRun.ReplaceAllString(value, " ")) }

var (
	auditVerdictTag = regexp.MustCompile(`^<(approved|disapproved)` + jsS + `*/>`)
	auditBullet     = regexp.MustCompile(`^[-*•✗✓]` + jsS + `*`)
	auditNumbering  = regexp.MustCompile(`^(\d+[.)])` + jsS + `*`)
	auditAttrib     = regexp.MustCompile(`(?i)^auditor` + jsS + `*:`)
	auditHeader     = regexp.MustCompile(`(?i)^[a-z][a-z ]{0,24}:$`)
	auditNewline    = regexp.MustCompile(`\r?\n`)
)

// deriveAuditResultCard: approval shows the fixed acceptance lines; rejection extracts up to three findings; errors state the failure.
func deriveAuditResultCard(verdict, report string) auditResultCard {
	if verdict == "approved" {
		return auditResultCard{verdict, "APPROVED", []string{"Objective satisfied.", "Verification requirements satisfied.", "Required tasks and evidence accepted."}}
	}
	if verdict == "error" {
		line := "The auditor could not finish the review."
		if jsTrim(report) != "" {
			line = auditOneLine(report)
		}
		return auditResultCard{verdict, "ERROR", []string{line}}
	}
	findings := auditExtractFindings(report)
	if len(findings) == 0 {
		line := auditOneLine(report)
		if line == "" {
			line = "The auditor requested changes — review the report."
		}
		findings = []string{line}
	}
	return auditResultCard{verdict, "CHANGES REQUIRED", findings}
}

func auditExtractFindings(report string) []string {
	findings := []string{}
	for _, raw := range auditNewline.Split(report, -1) {
		line := jsTrim(raw)
		if line == "" {
			continue
		}
		if auditVerdictTag.MatchString(line) {
			continue
		}
		cleaned := jsTrim(auditNumbering.ReplaceAllString(auditBullet.ReplaceAllString(line, ""), ""))
		if cleaned == "" {
			continue
		}
		if auditAttrib.MatchString(jsFoldSafe(cleaned)) {
			continue
		}
		if auditHeader.MatchString(jsFoldSafe(cleaned)) {
			continue
		}
		if utf16Len(cleaned) > 200 {
			findings = append(findings, utf16Prefix(cleaned, 197)+"...")
		} else {
			findings = append(findings, cleaned)
		}
		if len(findings) >= 3 {
			break
		}
	}
	return findings
}
