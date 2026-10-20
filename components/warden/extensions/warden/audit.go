package warden

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func lastSlashSegment(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// AuditReportPath is where /warden audit asks the agent to write its report.
func AuditReportPath(cwd string) string {
	return filepath.Join(cwd, ".pi-warden", "audit-report.html")
}

// BuildAuditPrompt is the user message that sends the workspace to the session model for an agent-driven audit
// (audit.ts buildAuditPrompt). projects comes from FindProjects.
func BuildAuditPrompt(cwd string, projects []string) string {
	projectName := lastSlashSegment(cwd)
	summaries := make([]string, 0, len(projects))
	for _, projectPath := range projects {
		name := lastSlashSegment(projectPath)
		files := DiscoverSourceFiles(projectPath, 200)
		manifest := ""
		for _, m := range projectManifests {
			if _, err := os.Stat(filepath.Join(projectPath, m)); err == nil {
				manifest = m
				break
			}
		}
		line := "- " + name + " (" + strconv.Itoa(len(files)) + " source files"
		if manifest != "" {
			line += ", manifest: " + manifest
		}
		summaries = append(summaries, line+")")
	}
	reportPath := AuditReportPath(cwd)
	return strings.Join([]string{
		"# Workspace Audit — " + projectName + "",
		"",
		"You are auditing this workspace to find concrete places where hand-written heuristics can be replaced by typed Jev (TypeSafe) questions.",
		"",
		"## Workspace inventory",
		strings.Join(summaries, "\n"),
		"",
		"## What to do",
		"",
		"1. **Read the actual source** of each project. Do not rely on README or manifest alone. Open the files listed above (and any others you discover) and read the code.",
		"",
		"2. **Find concrete replacement candidates.** Look for:",
		"   - if/else or switch chains that classify, route, or decide",
		"   - regex classifiers or keyword lists that label text",
		"   - threshold tables or scoring heuristics",
		"   - ranking or sorting by hand-built criteria",
		"   - validation logic that checks correctness of free-form output",
		"   - routing decisions based on intent or content",
		"",
		"   For each candidate, cite the exact **file:line** location.",
		"",
		"3. **Produce measurable data for every finding.** For each candidate, gather evidence such as:",
		"   - Count the branches, keyword entries, or cases the current code handles",
		"   - Count cases it misses (e.g. grep for keywords it does not cover, list known false positives from comments or issues)",
		"   - Run the existing test suite and count pass/fail relevant to this code path",
		"   - Write a throwaway script under the OS temp directory that feeds sample inputs through both the current code and a Jev question (using `typesafe_evaluate` when available in the session; this tool requires the operator to run `/typesafe enable` once per session — if a call fails with \"TypeSafe is disabled\", ask the operator once, and if it remains disabled, continue with the other evidence and mark those findings unmeasured)",
		"",
		"   A finding without measurable comparison data must be marked **\"unmeasured\"** — do not dress it up.",
		"",
		"4. **Rank findings** by expected improvement (high/medium/low) and setup effort (low/medium/high).",
		"",
		"5. **Write a self-contained HTML report** to `" + reportPath + "` (create the `.pi-warden` directory if it does not exist).",
		"   - Plain HTML + inline CSS, no external assets, no scripts that fetch anything.",
		"   - Sections: summary, findings table, one section per finding with evidence, methodology, and what was not measured.",
		"   - One short chat line at the end naming the file path and the finding count.",
		"",
		"## Constraints",
		"",
		"- Read-only: do not modify any project source while auditing. The only files you may write are the report and throwaway scripts under the OS temp directory.",
		"- Be specific. Every finding must name a file:line in this workspace.",
		"- Prefer fewer, well-evidenced findings over many thin ones.",
		"- If no replacement opportunities exist in a project, say so briefly and move on.",
	}, "\n")
}

// ReportSnapshot is a point-in-time look at the audit report file.
type ReportSnapshot struct {
	Exists  bool
	MtimeMs float64
}

// SnapshotReport takes the snapshot; a file that cannot be stat'ed counts as missing.
func SnapshotReport(reportPath string) ReportSnapshot {
	info, err := os.Stat(reportPath)
	if err != nil {
		return ReportSnapshot{}
	}
	return ReportSnapshot{Exists: true, MtimeMs: float64(info.ModTime().UnixNano()) / 1e6}
}

// ReportOutcome says whether the audit produced a new or updated report: written, stale or missing.
func ReportOutcome(before, after ReportSnapshot) string {
	if !after.Exists {
		return "missing"
	}
	if !before.Exists || after.MtimeMs > before.MtimeMs {
		return "written"
	}
	return "stale"
}
