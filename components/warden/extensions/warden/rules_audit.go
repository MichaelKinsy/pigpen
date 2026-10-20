package warden

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

// `/warden rules audit` and `/warden bench` (src/rules-audit.ts): two plain functions over the rules guard.
//
// The audit judges existing files against the project rules as if each had just been written, one `write` request per
// file. Nothing leaves the machine without the confirm dialog (or `--yes` in a headless run), the results never reach
// the rules log, and the only file written is the Markdown copy under `.pi-warden/`. The bench measures what one of
// those requests costs on this machine with a fixed built-in sample, so no project content is sent for it at all.

const (
	// auditDefaultMax: files the audit judges unless `--max` says otherwise.
	auditDefaultMax = 50
	// auditConcurrency: requests in flight at once; small so a big audit cannot burst the backend.
	auditConcurrency = 4
	// auditDiscoveryMax: source files the discovery walk collects before it stops; the cap counts what it found.
	auditDiscoveryMax = 5000
	auditReportFile   = ".pi-warden/rules-audit.md"
	rulesAuditUsage   = "Usage: /warden rules audit [paths...] [--max N] [--yes], where N is a positive integer (default 50) and paths default to the project."
	benchUsage        = "Usage: /warden bench [--runs N], where N is a positive integer (default 10)."
)

// ---------------------------------------------------------------------------
// Arguments

// RulesAuditArgs are the parsed arguments of `/warden rules audit`.
type RulesAuditArgs struct {
	// Paths are the project-relative paths to audit; ["."] when none were given.
	Paths []string
	Max   int
	Yes   bool
	Error string
}

func positiveInteger(value string) (int, bool) {
	n := jsToNumber(value)
	if math.IsNaN(n) || math.IsInf(n, 0) || n != math.Floor(n) || n < 1 || n > math.MaxInt32 {
		return 0, false
	}
	return int(n), true
}

func parseRulesAuditArgs(tokens []string) RulesAuditArgs {
	var paths []string
	max, yes := auditDefaultMax, false
	for index := 0; index < len(tokens); index++ {
		token := tokens[index]
		if token == "--yes" {
			yes = true
			continue
		}
		if token == "--max" {
			index++
			count, ok := 0, false
			if index < len(tokens) {
				count, ok = positiveInteger(tokens[index])
			}
			if !ok {
				return RulesAuditArgs{Paths: paths, Max: max, Yes: yes, Error: "--max needs a positive integer. " + rulesAuditUsage}
			}
			max = count
			continue
		}
		if strings.HasPrefix(token, "-") {
			return RulesAuditArgs{Paths: paths, Max: max, Yes: yes, Error: "Unknown option " + token + ". " + rulesAuditUsage}
		}
		paths = append(paths, token)
	}
	if len(paths) == 0 {
		paths = []string{"."}
	}
	return RulesAuditArgs{Paths: paths, Max: max, Yes: yes}
}

// BenchArgs are the parsed arguments of `/warden bench`.
type BenchArgs struct {
	Runs  int
	Error string
}

func parseBenchArgs(tokens []string) BenchArgs {
	runs := 10
	for index := 0; index < len(tokens); index++ {
		token := tokens[index]
		if token == "--runs" {
			index++
			count, ok := 0, false
			if index < len(tokens) {
				count, ok = positiveInteger(tokens[index])
			}
			if !ok {
				return BenchArgs{Runs: runs, Error: "--runs needs a positive integer. " + benchUsage}
			}
			runs = count
			continue
		}
		return BenchArgs{Runs: runs, Error: "Unknown option " + token + ". " + benchUsage}
	}
	return BenchArgs{Runs: runs}
}

// ---------------------------------------------------------------------------
// Discovery

// AuditFilePlan is the files an audit will judge.
type AuditFilePlan struct {
	// Files are the project-relative paths to judge, in sorted order, capped at max.
	Files []string
	// Matched counts the files that passed every filter (git, `rules.exclude`, `rules.skip`, and at least one applicable rule).
	Matched int
	// LeftOut counts the matched files left out at the cap.
	LeftOut int
	// Missing are the given paths that are not a file or directory inside the project.
	Missing []string
}

// jsSortStrings sorts as Array.prototype.sort does by default: by UTF-16 code units.
func jsSortStrings(list []string) {
	sort.Slice(list, func(i, j int) bool {
		a, b := utf16.Encode([]rune(list[i])), utf16.Encode([]rune(list[j]))
		for k := 0; k < len(a) && k < len(b); k++ {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return len(a) < len(b)
	})
}

// collectAuditFiles are the source files under paths the audit will judge: found by DiscoverSourceFiles, inside the
// project, not ignored by git, not kept out by `rules.exclude` or `rules.skip`, and with at least one rule that applies
// to them. Capped in sorted order so the same command judges the same files twice.
func collectAuditFiles(cwd string, paths []string, set *RuleSet, config RulesConfig, max int) AuditFilePlan {
	seen := map[string]bool{}
	var candidates, missing []string
	base := resolvePath("", cwd)
	for _, raw := range paths {
		abs := resolvePath(cwd, raw)
		info, err := os.Stat(abs)
		rel, relErr := filepath.Rel(base, abs)
		rel = filepath.ToSlash(rel)
		// The project root is the one path rulesProjectPath rejects: it is not inside itself.
		if err != nil || relErr != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			missing = append(missing, raw)
			continue
		}
		if rel == "." {
			rel = ""
		}
		var found []string
		if info.IsDir() {
			for _, name := range DiscoverSourceFiles(abs, auditDiscoveryMax) {
				if p, ok := rulesProjectPath(filepath.Join(abs, name), cwd); ok {
					found = append(found, p)
				}
			}
		} else if rel != "" {
			found = []string{rel}
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			missing = append(missing, raw)
		}
		for _, candidate := range found {
			if seen[candidate] {
				continue
			}
			seen[candidate] = true
			if gitIgnored(candidate, cwd, false) {
				continue
			}
			if skipReason(&RulesTarget{Tool: "write", Path: candidate}, set, config) != "" {
				continue
			}
			candidates = append(candidates, candidate)
		}
	}
	jsSortStrings(candidates)
	plan := AuditFilePlan{Matched: len(candidates), Missing: missing, LeftOut: max0(len(candidates) - max)}
	if len(candidates) > max {
		plan.Files = candidates[:max]
	} else {
		plan.Files = candidates
	}
	return plan
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

// ---------------------------------------------------------------------------
// Results

// RulesAuditFileResult is the verdict on one audited file.
type RulesAuditFileResult struct {
	// Path is project-relative.
	Path    string
	Verdict RulesVerdict
}

// RulesAuditRow is one rule's row of the audit table.
type RulesAuditRow struct {
	ID, Name string
	// Judged counts the files whose request asked about this rule.
	Judged int
	// Flagged counts the files where the rule scored at or above its cutoff.
	Flagged int
	// Mean is the mean P(violation) over the files judged.
	Mean float64
}

// RulesAuditOutcome is the outcome of an audit that ran.
type RulesAuditOutcome struct {
	Paths []string
	Max   int
	// Selected counts the files selected for judgment.
	Selected int
	Judged   int
	LeftOut  int
	Missing  []string
	// Unreadable are the selected files that could not be read or were empty.
	Unreadable []string
	// Errors are the files whose judgment came back as an error.
	Errors  []string
	Results []RulesAuditFileResult
	Rows    []RulesAuditRow
	// Flagged counts the files with at least one finding.
	Flagged int
	Sources []string
	// Rules counts the distinct rules asked across the audit.
	Rules      int
	Aggregate  bool
	ReportPath string
	ElapsedMs  int
}

// RulesAuditResult is the result of runRulesAudit: Status is no-judge | no-files | needs-yes | cancelled | done.
type RulesAuditResult struct {
	Status  string
	Reason  string
	Files   int
	LeftOut int
	Outcome *RulesAuditOutcome
}

// RulesAuditOptions are the inputs of runRulesAudit.
type RulesAuditOptions struct {
	Ctx   context.Context
	Cwd   string
	Paths []string
	Max   int
	// Yes: headless runs pass `--yes`; without it and without a dialog nothing is sent.
	Yes     bool
	Config  RulesConfig
	Set     *RuleSet
	Judge   Judge
	Timeout time.Duration
	// Destination is where the samples go, named in the confirm dialog.
	Destination string
	Confirm     func(title, body string) bool
}

// runRulesAudit sends one `write` request per selected file, at most auditConcurrency at a time, after the confirm
// dialog (or `--yes`). With no judge nothing is sent and no dialog is shown. The Markdown copy is written to
// auditReportFile; the rules log is never touched.
func runRulesAudit(options RulesAuditOptions) RulesAuditResult {
	if options.Judge == nil {
		return RulesAuditResult{Status: "no-judge"}
	}
	set := options.Set
	if set == nil {
		return RulesAuditResult{Status: "no-files", Reason: "no rules file resolved, so there is nothing to judge the files against"}
	}
	if set.ProseOnly {
		return RulesAuditResult{Status: "no-files", Reason: "no rules found in " + strings.Join(set.Sources, ", ") + " (prose only)"}
	}
	plan := collectAuditFiles(options.Cwd, options.Paths, set, options.Config, options.Max)
	if len(plan.Files) == 0 {
		return RulesAuditResult{Status: "no-files", Reason: "no source file under " + strings.Join(options.Paths, ", ") + " has a rule that applies to it"}
	}
	if !options.Yes {
		if options.Confirm == nil {
			return RulesAuditResult{Status: "needs-yes", Files: len(plan.Files), LeftOut: plan.LeftOut}
		}
		title := "Send " + strconv.Itoa(len(plan.Files)) + " " + pluralOf(len(plan.Files), "file", "files") + " to " + options.Destination + " for a rules audit?"
		body := []string{
			"Each file is judged against the project rules as if it had just been written.",
			"What leaves the machine: a redacted sample of each file (up to 6000 characters per file) and the rule text, sent to " + options.Destination + ".",
		}
		if plan.LeftOut > 0 {
			body = append(body, strconv.Itoa(plan.LeftOut)+" "+pluralOf(plan.LeftOut, "file", "files")+" left out at the --max "+strconv.Itoa(options.Max)+" cap.")
		}
		body = append(body, "Nothing is written to the rules log.")
		if !options.Confirm(title, strings.Join(body, "\n")) {
			return RulesAuditResult{Status: "cancelled"}
		}
	}
	results := make([]*RulesAuditFileResult, len(plan.Files))
	var mu sync.Mutex
	var unreadable, errs []string
	started := time.Now()
	queue := make(chan int)
	var wg sync.WaitGroup
	for range auditConcurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range queue {
				path := plan.Files[index]
				data, err := os.ReadFile(filepath.Join(options.Cwd, path))
				content := decodeUTF8(data)
				if err != nil || jsTrim(content) == "" {
					mu.Lock()
					unreadable = append(unreadable, path)
					mu.Unlock()
					continue
				}
				verdict := evaluateRules("write", writeInputOf(path, content), RulesOptions{Ctx: options.Ctx, Cwd: options.Cwd, Config: options.Config, Set: set, Judge: options.Judge, Timeout: options.Timeout})
				mu.Lock()
				if verdict.Source == "error" {
					errs = append(errs, path)
				}
				results[index] = &RulesAuditFileResult{Path: path, Verdict: verdict}
				mu.Unlock()
			}
		}()
	}
	for index := range plan.Files {
		queue <- index
	}
	close(queue)
	wg.Wait()
	var judged []RulesAuditFileResult
	for _, r := range results {
		if r != nil {
			judged = append(judged, *r)
		}
	}
	rows := ruleAuditRows(judged)
	flagged := 0
	for _, r := range judged {
		if len(r.Verdict.Findings) > 0 {
			flagged++
		}
	}
	outcome := &RulesAuditOutcome{
		Paths: append([]string(nil), options.Paths...), Max: options.Max, Selected: len(plan.Files), Judged: len(judged), LeftOut: plan.LeftOut,
		Missing: plan.Missing, Unreadable: unreadable, Errors: errs, Results: judged, Rows: rows, Flagged: flagged, Sources: set.Sources,
		Rules: len(rows), Aggregate: set.isAggregate(), ReportPath: auditReportFile, ElapsedMs: int(time.Since(started).Milliseconds()),
	}
	_, _ = writeRulesAuditReport(options.Cwd, rulesAuditMarkdown(*outcome, time.Now()))
	return RulesAuditResult{Status: "done", Outcome: outcome}
}

func writeInputOf(path, content string) map[string]any {
	return map[string]any{"path": path, "content": content}
}

// ---------------------------------------------------------------------------
// The Markdown copy

func usd(value float64) string { return "$" + toFixed(value, 6) }

func ruleCell(id string) string {
	if utf16Len(id) <= 44 {
		return id
	}
	return utf16Slice(id, 0, 43) + "…"
}

func padEnd(s string, n int) string {
	if k := utf16Len(s); k < n {
		return s + strings.Repeat(" ", n-k)
	}
	return s
}

func padStart(s string, n int) string {
	if k := utf16Len(s); k < n {
		return strings.Repeat(" ", n-k) + s
	}
	return s
}

// formatRulesAuditRows is the per-rule table body, flagged rules first.
func formatRulesAuditRows(rows []RulesAuditRow) string {
	lines := []string{padEnd("rule", 44) + padStart("judged", 6) + padStart("flagged", 8) + padStart("mean", 7)}
	for _, row := range rows {
		lines = append(lines, padEnd(ruleCell(row.ID), 44)+padStart(strconv.Itoa(row.Judged), 6)+padStart(strconv.Itoa(row.Flagged), 8)+padStart(toFixed(row.Mean, 2), 7))
	}
	return strings.Join(lines, "\n")
}

// RulesAuditFlag is one flagged rule of one file.
type RulesAuditFlag struct {
	Name  string
	Score float64
}

// RulesAuditFileFlags is the flags of one file.
type RulesAuditFileFlags struct {
	Path     string
	Findings []RulesAuditFlag
	Soft     []RulesAuditFlag
}

func flagsOf(findings []RuleFinding) []RulesAuditFlag {
	var out []RulesAuditFlag
	for _, f := range findings {
		out = append(out, RulesAuditFlag{Name: f.Name, Score: f.Violation})
	}
	return out
}

func maxScore(flags []RulesAuditFlag) float64 {
	best := 0.0
	for _, f := range flags {
		best = math.Max(best, f.Score)
	}
	return best
}

// flaggedAuditFiles are the files with findings, worst first (most findings, then the strongest score).
func flaggedAuditFiles(results []RulesAuditFileResult) []RulesAuditFileFlags {
	var out []RulesAuditFileFlags
	for _, entry := range results {
		flags := RulesAuditFileFlags{Path: entry.Path, Findings: flagsOf(entry.Verdict.Findings), Soft: flagsOf(entry.Verdict.SoftFindings)}
		if len(flags.Findings) > 0 || len(flags.Soft) > 0 {
			out = append(out, flags)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if d := len(b.Findings) - len(a.Findings); d != 0 {
			return d < 0
		}
		if d := maxScore(b.Findings) - maxScore(a.Findings); d != 0 {
			return d < 0
		}
		return jsLocaleCompare(a.Path, b.Path) < 0
	})
	return out
}

func flagLines(entry RulesAuditFileFlags) []string {
	var lines []string
	for _, f := range entry.Findings {
		lines = append(lines, f.Name+" "+toFixed(f.Score, 2))
	}
	for _, f := range entry.Soft {
		lines = append(lines, f.Name+" "+toFixed(f.Score, 2)+" (double-check)")
	}
	return lines
}

// formatRulesAuditFiles is one line per file, rule names with scores indented under the path.
func formatRulesAuditFiles(results []RulesAuditFileResult) string {
	flagged := flaggedAuditFiles(results)
	if len(flagged) == 0 {
		return "no file flagged"
	}
	var lines []string
	for _, entry := range flagged {
		lines = append(lines, entry.Path)
		for _, l := range flagLines(entry) {
			lines = append(lines, "  "+l)
		}
	}
	return strings.Join(lines, "\n")
}

// formatRulesAudit is the terminal output: summary, the table by rule, the file list, and where the Markdown copy went.
func formatRulesAudit(outcome RulesAuditOutcome) string {
	lines := []string{"Rules audit: " + strconv.Itoa(outcome.Judged) + " " + pluralOf(outcome.Judged, "file", "files") + " judged as new writes against " + strings.Join(outcome.Sources, ", ") + " (" + strconv.Itoa(outcome.Rules) + " " + pluralOf(outcome.Rules, "rule", "rules") + " in play); " + strconv.Itoa(outcome.LeftOut) + " left out at the --max " + strconv.Itoa(outcome.Max) + " cap; " + strconv.Itoa(outcome.Flagged) + " of " + strconv.Itoa(outcome.Judged) + " flagged."}
	if outcome.Aggregate {
		lines = append(lines, "The rules document has no rule headings, so each file is judged against it as one document.")
	}
	lines = append(lines, formatRulesAuditRows(outcome.Rows), "flagged files, worst first:", formatRulesAuditFiles(outcome.Results))
	if len(outcome.Missing) > 0 {
		lines = append(lines, "not found in the project: "+strings.Join(outcome.Missing, "; ")+".")
	}
	if len(outcome.Unreadable) > 0 {
		lines = append(lines, "not judged (unreadable or empty): "+strings.Join(outcome.Unreadable, "; ")+".")
	}
	if len(outcome.Errors) > 0 {
		lines = append(lines, "judgment failed for "+strconv.Itoa(len(outcome.Errors))+" "+pluralOf(len(outcome.Errors), "file", "files")+": "+strings.Join(outcome.Errors, "; ")+".")
	}
	lines = append(lines, "Markdown copy: "+outcome.ReportPath+". Nothing was recorded in the rules log.")
	return strings.Join(lines, "\n")
}

// rulesAuditMarkdown is the Markdown copy of one audit run.
func rulesAuditMarkdown(outcome RulesAuditOutcome, date time.Time) string {
	one := ""
	if outcome.Aggregate {
		one = ", judged as one document"
	}
	lines := []string{
		"# Rules audit",
		"",
		"- Date: " + isoTime(date),
		"- Paths: " + strings.Join(outcome.Paths, ", "),
		"- Judged: " + strconv.Itoa(outcome.Judged) + " of " + strconv.Itoa(outcome.Selected) + " selected files as new writes (" + strconv.Itoa(outcome.LeftOut) + " left out at the --max " + strconv.Itoa(outcome.Max) + " cap)",
		"- Rules in force: " + strings.Join(outcome.Sources, ", ") + " (" + strconv.Itoa(outcome.Rules) + " " + pluralOf(outcome.Rules, "rule", "rules") + " in play" + one + ")",
		"- Flagged: " + strconv.Itoa(outcome.Flagged) + " of " + strconv.Itoa(outcome.Judged) + " files",
		"",
		"## By rule",
		"",
		"| rule | judged | flagged | mean |",
		"| --- | ---: | ---: | ---: |",
	}
	for _, row := range outcome.Rows {
		lines = append(lines, "| `"+row.ID+"` | "+strconv.Itoa(row.Judged)+" | "+strconv.Itoa(row.Flagged)+" | "+toFixed(row.Mean, 2)+" |")
	}
	lines = append(lines, "", "## Flagged files, worst first", "")
	if flagged := flaggedAuditFiles(outcome.Results); len(flagged) > 0 {
		for _, entry := range flagged {
			lines = append(lines, "- `"+entry.Path+"` — "+strings.Join(flagLines(entry), "; "))
		}
	} else {
		lines = append(lines, "- no file flagged")
	}
	lines = append(lines, "")
	if len(outcome.Missing) > 0 {
		lines = append(lines, "Not found in the project: "+strings.Join(outcome.Missing, "; ")+".", "")
	}
	if len(outcome.Unreadable) > 0 {
		lines = append(lines, "Not judged (unreadable or empty): "+strings.Join(outcome.Unreadable, "; ")+".", "")
	}
	if len(outcome.Errors) > 0 {
		lines = append(lines, "Judgment failed for "+strconv.Itoa(len(outcome.Errors))+" "+pluralOf(len(outcome.Errors), "file", "files")+": "+strings.Join(outcome.Errors, "; ")+".", "")
	}
	lines = append(lines, "Nothing was recorded in the rules log.")
	return strings.Join(lines, "\n") + "\n"
}

// writeRulesAuditReport writes the Markdown copy and returns the path it went to.
func writeRulesAuditReport(cwd, markdown string) (string, error) {
	path := filepath.Join(cwd, filepath.FromSlash(auditReportFile))
	if err := os.MkdirAll(filepath.Join(cwd, ".pi-warden"), 0o755); err != nil {
		return path, err
	}
	return path, os.WriteFile(path, []byte(markdown), 0o644)
}

// ---------------------------------------------------------------------------
// Per-rule rows

// ruleAuditRows is one row per rule asked anywhere in the audit: files judged, files flagged, mean score; flagged first.
func ruleAuditRows(results []RulesAuditFileResult) []RulesAuditRow {
	type acc struct {
		RulesAuditRow
		sum float64
	}
	rows := map[string]*acc{}
	var order []string
	for _, entry := range results {
		for _, score := range entry.Verdict.Scores {
			row := rows[score.ID]
			if row == nil {
				row = &acc{RulesAuditRow: RulesAuditRow{ID: score.ID, Name: score.Name}}
				rows[score.ID] = row
				order = append(order, score.ID)
			}
			row.Judged++
			row.sum += score.Violation
			for _, f := range entry.Verdict.Findings {
				if f.ID == score.ID {
					row.Flagged++
					break
				}
			}
		}
	}
	out := make([]RulesAuditRow, 0, len(order))
	for _, id := range order {
		row := rows[id]
		r := row.RulesAuditRow
		if r.Judged > 0 {
			r.Mean = row.sum / float64(r.Judged)
		}
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Flagged != b.Flagged {
			return a.Flagged > b.Flagged
		}
		if d := b.Mean - a.Mean; d != 0 {
			return d < 0
		}
		return jsLocaleCompare(a.ID, b.ID) < 0
	})
	return out
}

// ---------------------------------------------------------------------------
// Bench

// benchSample is the one fixed sample the bench judges. It is built in, so no project content is sent for a bench run;
// only the active rule text travels with it.
var benchSample = struct{ Path, Content string }{
	Path: "src/warden-bench-sample.ts",
	Content: strings.Join([]string{
		"// Built-in so /warden bench measures a rules check without sending project content.",
		"export interface Sample { id: string; label: string }",
		"",
		"export function groupByLabel(items: readonly Sample[]): Map<string, Sample[]> {",
		"  const groups = new Map<string, Sample[]>();",
		"  for (const item of items) {",
		"    const bucket = groups.get(item.label);",
		"    if (bucket) bucket.push(item);",
		"    else groups.set(item.label, [item]);",
		"  }",
		"  return groups;",
		"}",
		"",
		"export function labels(items: readonly Sample[]): string[] {",
		"  return [...new Set(items.map((item) => item.label))].sort();",
		"}",
		"",
	}, "\n"),
}

// BenchUsage are the session counters a bench run diffs.
type BenchUsage struct {
	RequestsStarted int
	InputTokens     float64
	OutputTokens    float64
	EstimatedUSD    float64
}

// BenchOptions are the inputs of runBench.
type BenchOptions struct {
	Ctx      context.Context
	Runs     int
	Cwd      string
	Config   RulesConfig
	Set      *RuleSet
	Judge    Judge
	GetUsage func() BenchUsage
	Timeout  time.Duration
	// Now is the clock for the latency measurements; tests inject one.
	Now func() float64
}

// BenchResult is the result of runBench: Status is no-judge | skipped | done.
type BenchResult struct {
	Status string
	Reason string
	Runs   int
	// Asked counts the rules asked per check.
	Asked     int
	Aggregate bool
	// Flagged counts the checks with at least one finding.
	Flagged int
	// Errors counts the checks that came back as an error.
	Errors int
	// Requests sent, from GetUsage; the checks themselves without it.
	Requests int
	P50, P95 int
	// Usage figures are set only with GetUsage.
	HasUsage        bool
	MeanInputTokens float64
	CostPerCheck    float64
	CostPer100      float64
}

// percentile is the nearest-rank percentile of a sorted list of numbers.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	index := math.Min(float64(len(sorted)-1), math.Max(0, math.Ceil(p/100*float64(len(sorted)))-1))
	return sorted[int(index)]
}

// runBench judges the built-in sample `runs` times against the active rules, one request at a time so the latency
// numbers mean something, and reports what the checks cost on this machine. With no judge nothing is sent; a sample
// the rules keep out (`rules.exclude`, `rules.skip`) is reported and never sent either.
func runBench(options BenchOptions) BenchResult {
	if options.Judge == nil {
		return BenchResult{Status: "no-judge"}
	}
	target := &RulesTarget{Tool: "write", Path: benchSample.Path}
	if reason := skipReason(target, options.Set, options.Config); reason != "" {
		return BenchResult{Status: "skipped", Reason: reason}
	}
	now := options.Now
	if now == nil {
		now = func() float64 { return float64(time.Now().UnixMilli()) }
	}
	var before, after BenchUsage
	if options.GetUsage != nil {
		before = options.GetUsage()
	}
	var timings []float64
	flagged, errs, asked, aggregate := 0, 0, 0, false
	for range options.Runs {
		started := now()
		verdict := evaluateRules("write", writeInputOf(benchSample.Path, benchSample.Content), RulesOptions{Ctx: options.Ctx, Cwd: options.Cwd, Config: options.Config, Set: options.Set, Judge: options.Judge, Timeout: options.Timeout})
		timings = append(timings, now()-started)
		if verdict.Source == "error" {
			errs++
		}
		if len(verdict.Findings) > 0 {
			flagged++
		}
		asked, aggregate = verdict.Asked, verdict.Aggregate
	}
	sort.Float64s(timings)
	result := BenchResult{Status: "done", Runs: options.Runs, Asked: asked, Aggregate: aggregate, Flagged: flagged, Errors: errs, Requests: options.Runs,
		P50: int(jsRoundFloat(percentile(timings, 50))), P95: int(jsRoundFloat(percentile(timings, 95)))}
	if options.GetUsage != nil {
		after = options.GetUsage()
		runs := float64(options.Runs)
		usd := (after.EstimatedUSD - before.EstimatedUSD) / runs
		result.HasUsage = true
		result.Requests = after.RequestsStarted - before.RequestsStarted
		result.MeanInputTokens = (after.InputTokens - before.InputTokens) / runs
		result.CostPerCheck, result.CostPer100 = usd, usd*100
	}
	return result
}

func jsRoundFloat(x float64) float64 { return math.Floor(x + 0.5) }

// formatBench is the bench output: the numbers, and the line that says no project content was sent.
func formatBench(result BenchResult) string {
	one := ""
	if result.Aggregate {
		one = ", document judged as one rule"
	}
	failed := ""
	if result.Errors > 0 {
		failed = " (" + strconv.Itoa(result.Errors) + " failed)"
	}
	lines := []string{
		"Bench: " + strconv.Itoa(result.Runs) + " " + pluralOf(result.Runs, "check", "checks") + " of one built-in sample file against the active rules (" + strconv.Itoa(result.Asked) + " " + pluralOf(result.Asked, "rule", "rules") + " per check" + one + ").",
		"The sample is built in and no project content is sent, so no confirmation was needed.",
		"Latency: p50 " + strconv.Itoa(result.P50) + " ms, p95 " + strconv.Itoa(result.P95) + " ms; requests " + strconv.Itoa(result.Requests) + failed + ".",
	}
	if result.Flagged > 0 {
		lines = append(lines, "Findings in "+strconv.Itoa(result.Flagged)+" of "+strconv.Itoa(result.Runs)+" checks (the sample is judged as written code, not as a fixture).")
	}
	if !result.HasUsage {
		lines = append(lines, "Input tokens per check: unknown (no usage counters).", "Estimated cost per check: unknown (no usage counters).")
	} else {
		lines = append(lines, "Input tokens per check: "+strconv.Itoa(int(jsRoundFloat(result.MeanInputTokens)))+" (mean).",
			"Estimated cost per check: "+usd(result.CostPerCheck)+"; per 100 edits: "+usd(result.CostPer100)+".")
	}
	return strings.Join(lines, "\n")
}
