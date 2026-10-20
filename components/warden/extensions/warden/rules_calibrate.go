package warden

import (
	"bytes"
	"context"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// `/warden rules calibrate` (src/rules-calibrate.ts): replay recent git history through the project rules and find the
// rules that fire on everything or cannot decide. A rule the sample never caught shows `no violation in sample` and is
// not flagged: the replayed commits are mostly reviewed, compliant code, so a rule that never fires there is often a
// rule people follow. The core is a plain function of the history and the rule set: the history in, per-rule results
// out. The caller collects the history (collectHistory, read-only `git log -p`), shows the confirm dialog, and writes
// the scores to the local rules log; nothing here sends anything on its own and nothing here writes a file.
//
// Each changed file of each commit becomes one `edit` input: the hunk's removed lines are `oldText`, its added lines are
// `newText`, and the file after the commit supplies the surrounding context (`before`/`after` around the change). Binary,
// generated, gitignored files and anything under `rules.exclude` or `rules.skip` are skipped, and the requests are
// capped at `--max`. `/warden rules tune` reads the flagged rules from the result (and from `/warden rules check`) and
// asks the session's agent to rewrite them; that prompt is the only thing tune sends.

const (
	// calibrateDefaultCommits: non-merge commits read when `--commits` is absent.
	calibrateDefaultCommits = 20
	// calibrateDefaultMax: requests sent when `--max` is absent.
	calibrateDefaultMax = 40
)

// reGeneratedMarker is the marker a generated source file carries, the standard spelling from every code generator.
var reGeneratedMarker = lazyRE(`(?i)code generated ` + jsDot + `* do not edit`)

// generatedPaths are the paths a build or a package manager writes: their changes say nothing about the rules.
var generatedPaths = []*lazyRegexp{
	lazyRE(`(^|/)(?:node_modules|dist|build|out|vendor|coverage|target)/`),
	lazyRE(`(?:^|/)(?:package-lock\.json|yarn\.lock|pnpm-lock\.yaml|Cargo\.lock|composer\.lock|poetry\.lock|go\.sum)$`),
	lazyRE(`\.min\.(?:js|css)$`),
	lazyRE(`(?:^|/)[^/]+\.map$`),
	lazyRE(`(?:^|/)[^/]+\.snap$`),
	lazyRE(`(?:^|/)[^/]+\.jsonl$`),
	lazyRE(`(?:^|/)[^/]+\.generated\.[^/]+$`),
	lazyRE(`_generated\.[^/]+$`),
	lazyRE(`\.pb\.go$`),
	lazyRE(`_pb2\.py$`),
}

// ---------------------------------------------------------------------------
// History: `git log -p` in, changed files out.

// HistoryHunk is one hunk of a changed file.
type HistoryHunk struct {
	// OldText is the removed lines of the hunk, newline-joined: the edit's oldText.
	OldText string
	// NewText is the added lines, newline-joined: the edit's newText.
	NewText string
	// Before is the hunk as the diff shows it: context plus removed lines, the file before the change.
	Before string
	// After is the hunk as the diff shows it: context plus added lines, the file after the change.
	After string
	// Start is the 1-based first line of the hunk on the file's after side.
	Start int
	// Count is the lines the hunk has on the after side (context plus added).
	Count int
}

// HistoryFile is one file a commit changed.
type HistoryFile struct {
	// Path is project-relative with forward slashes, as the commit changed it.
	Path string
	// Binary: a binary patch carries no lines to judge.
	Binary bool
	Hunks  []HistoryHunk
	// After is the file's text after the commit, when it still exists there; the edit's context.
	After    string
	HasAfter bool
}

// HistoryCommit is one commit with the files it changed.
type HistoryCommit struct {
	Hash string
	// At is the ISO timestamp of the commit.
	At    string
	Files []*HistoryFile
}

var (
	reHunkHeader = lazyRE(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)
	reDiffHeader = lazyRE(`^diff --git `)
	reGitEscape  = lazyRE(`\\(\d{1,3}|.)`)
	reMinusLine  = lazyRE(`^--- (` + jsDot + `*)$`)
	rePlusLine   = lazyRE(`^\+\+\+ (` + jsDot + `*)$`)
	reABPrefix   = lazyRE(`^[ab]/`)
	reDiffTarget = lazyRE(`^.*? b/(.*)$`)
	reAllDigits  = lazyRE(`^\d+$`)
)

// unquoteGitPath undoes the quoting git uses for a path with spaces or unusual characters.
func unquoteGitPath(raw string) string {
	value := jsTrim(raw)
	if !strings.HasPrefix(value, `"`) {
		return value
	}
	inner := value[1:]
	if strings.HasSuffix(value, `"`) && len(value) > 1 {
		inner = value[1 : len(value)-1]
	}
	return reGitEscape.ReplaceAllStringFunc(inner, func(m string) string {
		code := m[1:]
		if reAllDigits.MatchString(code) {
			n, _ := strconv.ParseInt(code, 8, 32)
			return string(rune(n))
		}
		switch code {
		case "n":
			return "\n"
		case "t":
			return "\t"
		case "r":
			return "\r"
		}
		return code
	})
}

// pathFromHeader is the `b/` side of a `+++` line, falling back to the `a/` side when the file was deleted.
func pathFromHeader(line string, fallback string, hasFallback bool) (string, bool) {
	raw := unquoteGitPath(jsTrim(line[min(4, len(line)):]))
	if raw == "/dev/null" {
		return fallback, hasFallback
	}
	return reABPrefix.ReplaceAllString(raw, ""), true
}

// pathFromDiffLine is the `b/` side of a `diff --git a/x b/y` line; quoted pairs are split at the closing quote.
func pathFromDiffLine(line string) (string, bool) {
	rest := line[len("diff --git "):]
	if strings.HasPrefix(rest, `"`) {
		end := strings.Index(rest[1:], `" `)
		if end < 0 {
			return "", false
		}
		end++
		return reABPrefix.ReplaceAllString(unquoteGitPath(rest[end+2:]), ""), true
	}
	if m := reDiffTarget.FindStringSubmatch(rest); m != nil {
		return m[1], true
	}
	return "", false
}

type hunkDraft struct {
	oldLines, newLines, contextBefore, contextAfter []string
	start, count                                    int
}

func concatLines(parts ...[]string) []string {
	var out []string
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// parseHistory is the changed files of each commit in one `git log -p` output: one record per commit, one per changed
// file, and one oldText/newText pair per hunk. Pure: the caller runs git and passes the text.
func parseHistory(log string) []*HistoryCommit {
	var commits []*HistoryCommit
	for _, chunk := range strings.Split(log, "\x01") {
		if jsTrim(chunk) == "" {
			continue
		}
		newline := strings.Index(chunk, "\n")
		headerLine := chunk
		body := ""
		if newline >= 0 {
			headerLine, body = chunk[:newline], chunk[newline+1:]
		}
		header := strings.Split(headerLine, "\x02")
		hash := jsTrim(header[0])
		at := ""
		if len(header) > 1 {
			at = jsTrim(header[1])
		}
		if hash == "" {
			continue
		}
		commit := &HistoryCommit{Hash: hash, At: at}
		commits = append(commits, commit)
		var file *HistoryFile
		oldSide, hasOldSide := "", false
		var hunk *hunkDraft
		closeHunk := func() {
			if file == nil || hunk == nil {
				return
			}
			before := strings.Join(concatLines(hunk.contextBefore, hunk.oldLines, hunk.contextAfter), "\n")
			after := strings.Join(concatLines(hunk.contextBefore, hunk.newLines, hunk.contextAfter), "\n")
			file.Hunks = append(file.Hunks, HistoryHunk{OldText: strings.Join(hunk.oldLines, "\n"), NewText: strings.Join(hunk.newLines, "\n"), Before: before, After: after, Start: hunk.start, Count: hunk.count})
			hunk = nil
		}
		for _, line := range strings.Split(body, "\n") {
			if reDiffHeader.MatchString(line) {
				closeHunk()
				oldSide, hasOldSide = "", false
				path, ok := pathFromDiffLine(line)
				if !ok {
					path = "unknown"
				}
				file = &HistoryFile{Path: path}
				commit.Files = append(commit.Files, file)
				continue
			}
			if file == nil {
				continue
			}
			if strings.HasPrefix(line, "GIT binary patch") || strings.HasPrefix(line, "Binary files ") {
				file.Binary = true
				closeHunk()
				continue
			}
			// `---`/`+++` are file headers only between `diff --git` and the first hunk; inside a hunk they are content.
			if hunk == nil {
				if m := reMinusLine.FindStringSubmatch(line); m != nil {
					oldSide, hasOldSide = reABPrefix.ReplaceAllString(unquoteGitPath(m[1]), ""), true
					continue
				}
				if rePlusLine.MatchString(line) {
					fallback, has := oldSide, hasOldSide && oldSide != "/dev/null"
					if path, ok := pathFromHeader(line, fallback, has); ok && path != "" {
						file.Path = path
					}
					continue
				}
			}
			if m := reHunkHeader.FindStringSubmatch(line); m != nil {
				closeHunk()
				start, _ := strconv.Atoi(m[3])
				count := 1
				if m[4] != "" {
					count, _ = strconv.Atoi(m[4])
				}
				hunk = &hunkDraft{start: start, count: count}
				continue
			}
			if hunk == nil {
				continue
			}
			if strings.HasPrefix(line, "\\") {
				continue
			}
			text := ""
			if line != "" {
				text = line[1:]
			}
			switch {
			case strings.HasPrefix(line, "+"):
				hunk.newLines = append(hunk.newLines, text)
			case strings.HasPrefix(line, "-"):
				hunk.oldLines = append(hunk.oldLines, text)
			case strings.HasPrefix(line, " ") || line == "":
				if len(hunk.newLines) > 0 || len(hunk.oldLines) > 0 {
					hunk.contextAfter = append(hunk.contextAfter, text)
				} else {
					hunk.contextBefore = append(hunk.contextBefore, text)
				}
			}
		}
		closeHunk()
	}
	return commits
}

type gitCommandError struct{ message string }

func (e *gitCommandError) Error() string { return e.message }

// gitCapture runs git read-only and returns stdout; a failure carries Node's `execFileSync` message so the caller can
// show it as the original does.
func gitCapture(cwd string, args []string, timeout time.Duration, maxBuffer int) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = cwd
	hideWindow(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &stdout, left: maxBuffer}
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := "Command failed: git " + strings.Join(args, " ")
		if stderr.Len() > 0 {
			message += "\n" + stderr.String()
		}
		return "", &gitCommandError{message: message}
	}
	return decodeUTF8(stdout.Bytes()), nil
}

// collectHistory is the last `commits` non-merge commits with their patches, and the file after each commit as the
// edit's context. Read-only: `git log -p` and `git show`, nothing else. It fails when git cannot read the history (no
// commits yet, not a repository).
func collectHistory(cwd string, commits int) ([]*HistoryCommit, error) {
	log, err := gitCapture(cwd, []string{"log", "--no-merges", "-n", strconv.Itoa(commits), "-p", "--format=%x01%H%x02%aI"}, 15*time.Second, 64*1024*1024)
	if err != nil {
		return nil, err
	}
	history := parseHistory(log)
	for _, commit := range history {
		for _, file := range commit.Files {
			if file.Binary || len(file.Hunks) == 0 {
				continue
			}
			// A deleted file, or a path that no longer exists at that commit: the hunk's own before/after images stand.
			text, err := gitCapture(cwd, []string{"show", commit.Hash + ":" + file.Path}, 5*time.Second, 16*1024*1024)
			if err == nil && !strings.Contains(text, "\x00") {
				file.After, file.HasAfter = text, true
			}
		}
	}
	return history, nil
}

// ---------------------------------------------------------------------------
// From a changed file to the guard's `edit` input.

// CalibrateSkip is a file the history replay skips, with the same reason wording the live guard reports.
type CalibrateSkip struct{ Path, Commit, Reason string }

// changeSkipReason is why a changed file is not replayed, or "" when it is judged. Binary and generated are decided here.
func changeSkipReason(file *HistoryFile) string {
	if file.Binary {
		return "binary file"
	}
	if len(file.Hunks) == 0 {
		return "no content change"
	}
	added := make([]string, len(file.Hunks))
	for i, h := range file.Hunks {
		added[i] = h.NewText
	}
	for _, pattern := range generatedPaths {
		if pattern.MatchString(file.Path) {
			return "generated file"
		}
	}
	if reGeneratedMarker.MatchString(strings.Join(added, "\n")) {
		return "generated file"
	}
	return ""
}

// jsSliceLines is Array.prototype.slice on lines, negative indexes counting from the end.
func jsSliceLines(lines []string, from, to int) []string {
	n := len(lines)
	norm := func(i int) int {
		if i < 0 {
			i += n
			if i < 0 {
				i = 0
			}
		}
		if i > n {
			i = n
		}
		return i
	}
	a, b := norm(from), norm(to)
	if a >= b {
		return nil
	}
	return lines[a:b]
}

// editViews are the hunk's oldText/newText pairs as one `edit` input: the file after the commit around each change is
// the context.
func editViews(file *HistoryFile, contextLines, maxEditViews int) (edits []EditView, moreEdits int) {
	var kept []HistoryHunk
	for _, hunk := range file.Hunks {
		if len(jsTrim(hunk.NewText)) > 0 {
			kept = append(kept, hunk)
		}
	}
	var afterLines []string
	if file.HasAfter {
		afterLines = strings.Split(file.After, "\n")
	}
	for index, hunk := range kept {
		if len(edits) >= maxEditViews {
			break
		}
		before, after := hunk.Before, hunk.After
		if file.HasAfter {
			// Wide window from the file after the commit: the hunk's own images only show three lines around the change.
			from := max(0, hunk.Start-1-contextLines)
			to := min(len(afterLines), hunk.Start-1+hunk.Count+contextLines)
			var oldLines []string
			if hunk.OldText != "" {
				oldLines = strings.Split(hunk.OldText, "\n")
			}
			after = strings.Join(jsSliceLines(afterLines, from, to), "\n")
			before = strings.Join(concatLines(jsSliceLines(afterLines, from, hunk.Start-1), oldLines, jsSliceLines(afterLines, hunk.Start-1+hunk.Count, to)), "\n")
		}
		b, a := Redact(truncate(before, editTextLimit)), Redact(truncate(after, editTextLimit))
		edits = append(edits, EditView{ID: "edit_" + strconv.Itoa(index+1), Before: &b, After: &a, NewText: Redact(truncate(hunk.NewText, editTextLimit))})
	}
	return edits, max(0, len(kept)-maxEditViews)
}

// ---------------------------------------------------------------------------
// The plan: what would be sent, and what is skipped. Nothing is sent here.

// CalibrateSample is one change that would be judged.
type CalibrateSample struct {
	Commit string
	// At is the ISO timestamp of the commit the change came from.
	At     string
	Path   string
	Target *RulesTarget
}

// CalibratePlan is what a calibration would send.
type CalibratePlan struct {
	// Samples: one `edit` input per judged change, in history order (newest first), at most maxRequests.
	Samples []CalibrateSample
	// Dropped are the samples past maxRequests, not sent.
	Dropped []CalibrateSample
	// Requests the run would send: len(Samples).
	Requests int
	Skipped  []CalibrateSkip
}

// PlanOptions are the inputs of planCalibration.
type PlanOptions struct {
	Set    *RuleSet
	Config RulesConfig
	// Cwd is the project root the paths are relative to; it also decides `gitignore` skips.
	Cwd         string
	MaxRequests int
}

// planCalibration decides which changed files of the history are judged, in what shape, and which are skipped and why.
// Pure apart from `git check-ignore`.
func planCalibration(history []*HistoryCommit, options PlanOptions) CalibratePlan {
	var samples []CalibrateSample
	var skipped []CalibrateSkip
	for _, commit := range history {
		for _, file := range commit.Files {
			skip := func(reason string) {
				skipped = append(skipped, CalibrateSkip{Path: file.Path, Commit: commit.Hash, Reason: reason})
			}
			if reason := changeSkipReason(file); reason != "" {
				skip(reason)
				continue
			}
			if g, ok := matchGlobFirst(file.Path, options.Config.Exclude); ok {
				skip("excluded from Jev by rules.exclude (" + g + ")")
				continue
			}
			if g, ok := matchGlobFirst(file.Path, options.Config.Skip); ok {
				skip("rules do not apply by rules.skip (" + g + ")")
				continue
			}
			if gitIgnored(file.Path, options.Cwd, true) {
				skip("gitignored by the project")
				continue
			}
			edits, moreEdits := editViews(file, editContextLines, maxEdits)
			if len(edits) == 0 {
				skip("nothing written in this change")
				continue
			}
			samples = append(samples, CalibrateSample{Commit: commit.Hash, At: commit.At, Path: file.Path, Target: &RulesTarget{Tool: "edit", Path: file.Path, Edits: edits, MoreEdits: moreEdits}})
		}
	}
	keep := max(0, options.MaxRequests)
	plan := CalibratePlan{Skipped: skipped, Requests: min(len(samples), keep)}
	if keep >= len(samples) {
		plan.Samples = samples
	} else {
		plan.Samples, plan.Dropped = samples[:keep], samples[keep:]
	}
	return plan
}

// ---------------------------------------------------------------------------
// The core: history in, per-rule results out.

// RuleCalibration is one rule's result of a calibration.
type RuleCalibration struct {
	ID, Name string
	// Applied counts how often the rule was asked: its `paths` matched a judged file.
	Applied int
	Fired   int
	// FiredRate is fired / applied.
	FiredRate     float64
	MeanViolation float64
	// Unfired is true when the sample held no violation of this rule. Not a flag: replayed commits are mostly reviewed,
	// compliant code, so a rule that never fires there is often a rule people follow.
	Unfired bool
	// Flags are the `/warden report` flags with the same thresholds, without `never fires`.
	Flags []string
}

// Calibration is the result of replaying a history.
type Calibration struct {
	Rows []RuleCalibration
	// NeverApplied are the rules in the current rule set that no judged file reached.
	NeverApplied []NamedRule
	// Requests sent.
	Requests int
	// Failed counts the requests that came back without an answer; their files are not scored.
	Failed  int
	Skipped []CalibrateSkip
	// Dropped counts the judged changes past the cap, not sent.
	Dropped int
	// Records: one per scored rule, source "calibrate", for the local rules log.
	Records []RuleRecord
	Commits int
}

// CalibrateOptions are the inputs of calibrate.
type CalibrateOptions struct {
	PlanOptions
	Ctx     context.Context
	Judge   Judge
	Timeout time.Duration
	// Session is the session id for the log records; replays default to "calibrate".
	Session string
}

// calibrate replays the history through every rule of the set and returns per-rule results, worst first: how often the
// rule applied, fired, its mean score, and the flags `/warden report` uses. The rules the sample never reached are
// listed apart. Each score also comes back as a RuleRecord with source "calibrate" for the local rules log. A request
// that fails is counted in Failed and leaves its file unscored. With no judge nothing is sent and nothing is scored.
func calibrate(history []*HistoryCommit, options CalibrateOptions) Calibration {
	plan := planCalibration(history, options.PlanOptions)
	var records []RuleRecord
	failed := 0
	var aggregate *string
	if options.Set.isAggregate() {
		name := "the project's " + options.Set.Sources[0]
		aggregate = &name
	}
	ctx := options.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	session := options.Session
	if session == "" {
		session = "calibrate"
	}
	if options.Judge != nil {
		for _, sample := range plan.Samples {
			request := buildRulesRequest(sample.Target, options.Set)
			result := Ask(ctx, options.Judge, Request{State: request.State, Questions: request.Questions}, options.Timeout)
			if !result.OK {
				failed++
				continue
			}
			scoring := scoreRuleAnswers(request.Applicable, result.Answers, options.Config, aggregate)
			soft := options.Config.SoftThreshold
			for _, score := range scoring.Scores {
				cutoff := options.Config.Threshold
				if score.Threshold != nil {
					cutoff = *score.Threshold
				}
				finding := score.Violation >= cutoff
				records = append(records, RuleRecord{
					At: sample.At, Session: session, Path: sample.Path, Tool: "edit", ID: score.ID, Name: score.Name, Outcome: score.Outcome,
					Violation: score.Violation, Threshold: cutoff, Finding: finding,
					Soft:   !finding && soft > 0 && score.Violation >= soft,
					Source: "calibrate",
				})
			}
		}
	}
	var stamps []float64
	for _, commit := range history {
		if at, err := time.Parse(time.RFC3339Nano, commit.At); err == nil {
			stamps = append(stamps, float64(at.UnixMilli()))
		}
	}
	until := float64(time.Now().UnixMilli())
	since := until
	if len(stamps) > 0 {
		until, since = stamps[0], stamps[0]
		for _, s := range stamps {
			until, since = math.Max(until, s), math.Min(since, s)
		}
	}
	days := max(1, int(math.Ceil((until-since)/dayMs))+1)
	current := make([]NamedRule, len(options.Set.Rules))
	for i, rule := range options.Set.Rules {
		current[i] = NamedRule{ID: rule.ID, Name: rule.Name}
	}
	report := buildRulesReport(records, ReportOptions{Days: days, Now: time.UnixMilli(int64(until) + 1), CurrentRules: current, Source: "calibrate"})
	result := Calibration{NeverApplied: report.Unheard, Failed: failed, Skipped: plan.Skipped, Dropped: len(plan.Dropped), Records: records, Commits: len(history)}
	if options.Judge != nil {
		result.Requests = plan.Requests
	}
	for _, row := range report.Rows {
		var flags []string
		// `never fires` means something different in the live report; on a replayed history it mostly means people
		// follow the rule, so it is not carried as a flag here.
		for _, flag := range row.Flags {
			if flag != flagNeverFires {
				flags = append(flags, flag)
			}
		}
		result.Rows = append(result.Rows, RuleCalibration{ID: row.ID, Name: row.Name, Applied: row.Judged, Fired: row.Fired, FiredRate: row.FiredRate, MeanViolation: row.MeanViolation, Unfired: row.Fired == 0, Flags: flags})
	}
	return result
}

// ---------------------------------------------------------------------------
// The report.

// formatCalibration is one line per rule, worst first: how often it applied and fired, the mean score, and the flags;
// then the unheard rules.
func formatCalibration(result Calibration) string {
	parts := []string{strconv.Itoa(result.Requests) + " " + pluralOf(result.Requests, "request", "requests"), strconv.Itoa(result.Commits) + " " + pluralOf(result.Commits, "commit", "commits")}
	if result.Failed > 0 {
		parts = append(parts, strconv.Itoa(result.Failed)+" without an answer")
	}
	if len(result.Skipped) > 0 {
		parts = append(parts, strconv.Itoa(len(result.Skipped))+" "+pluralOf(len(result.Skipped), "change", "changes")+" skipped")
	}
	if result.Dropped > 0 {
		parts = append(parts, strconv.Itoa(result.Dropped)+" past the cap, not sent")
	}
	lines := []string{"Rules calibrate: " + strings.Join(parts, ", ") + "."}
	if len(result.Rows) > 0 {
		lines = append(lines, "Worst first:")
		for i, row := range result.Rows {
			var notes []string
			if row.Unfired {
				notes = append(notes, "no violation in sample")
			}
			notes = append(notes, row.Flags...)
			suffix := ""
			if len(notes) > 0 {
				suffix = " · " + strings.Join(notes, " · ")
			}
			lines = append(lines, strconv.Itoa(i+1)+". "+shortName(row.Name, 40)+" · "+strconv.Itoa(row.Applied)+" applied · "+strconv.Itoa(row.Fired)+" fired "+strconv.Itoa(jsRound(row.FiredRate*100))+"% · mean "+toFixed(row.MeanViolation, 2)+suffix)
		}
	} else {
		lines = append(lines, "No rule was applied to any file in the sample.")
	}
	if len(result.NeverApplied) > 0 {
		names := make([]string, len(result.NeverApplied))
		for i, rule := range result.NeverApplied {
			names[i] = shortName(rule.Name, 40)
		}
		lines = append(lines, "Never applied to any file in the sample ("+strconv.Itoa(len(result.NeverApplied))+"): "+strings.Join(names, ", ")+".")
	}
	if len(result.Skipped) > 0 {
		skips := make([]string, len(result.Skipped))
		for i, skip := range result.Skipped {
			skips[i] = skip.Path + " (" + skip.Reason + ")"
		}
		lines = append(lines, "Skipped: "+strings.Join(skips, ", ")+".")
	}
	lines = append(lines, strconv.Itoa(len(result.Records))+" "+pluralOf(len(result.Records), "score", "scores")+` saved to the local rules log with source "calibrate"; /warden report counts them apart from live verdicts.`)
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------------
// The confirm dialog and the `--yes` path.

// CalibrateGate is what to do before sending: send, confirm first, or refuse with a reason.
type CalibrateGate struct {
	Kind   string // send | confirm | refuse
	Reason string
}

// calibrateGate: nothing leaves the machine before the user sees what it is: a UI shows the confirm dialog, and a
// headless run needs the explicit `--yes`. `--yes` stands in for the dialog in either case.
func calibrateGate(hasUI, yes bool) CalibrateGate {
	if yes {
		return CalibrateGate{Kind: "send"}
	}
	if hasUI {
		return CalibrateGate{Kind: "confirm"}
	}
	return CalibrateGate{Kind: "refuse", Reason: "Nothing was sent: a headless run needs an explicit --yes to send calibration requests."}
}

const (
	noticeSamples     = 3
	noticeSampleLimit = 200
)

// calibrationNotice is what the confirm dialog shows: how many requests go out and what leaves the machine, as
// redacted diff samples.
func calibrationNotice(plan CalibratePlan) string {
	shown := min(noticeSamples, len(plan.Samples))
	lines := []string{
		strconv.Itoa(plan.Requests) + " " + pluralOf(plan.Requests, "request", "requests") + " will go to the judgment backend, one per changed file in the sample. Each carries the project-relative path, the added and removed lines of the change with about " + strconv.Itoa(editContextLines*2) + " lines of the file after the commit around them, and the rule text. Everything is redacted first. The diffs, first " + strconv.Itoa(shown) + " of " + strconv.Itoa(plan.Requests) + ":",
		"",
	}
	for _, sample := range plan.Samples[:shown] {
		var edit *EditView
		if len(sample.Target.Edits) > 0 {
			edit = &sample.Target.Edits[0]
		}
		removed := 0
		if edit != nil && edit.Before != nil && *edit.Before != "" {
			removed = len(strings.Split(*edit.Before, "\n"))
		}
		lines = append(lines, sample.Path+" ("+utf16Slice(sample.Commit, 0, 8)+"):")
		if edit != nil {
			lines = append(lines, "  "+truncate(strings.ReplaceAll(edit.NewText, "\n", "\n  "), noticeSampleLimit))
		}
		lines = append(lines, "  ("+strconv.Itoa(removed)+" lines around the change)", "")
	}
	if n := len(plan.Dropped); n > 0 {
		stays, is := "stay", "are"
		if n == 1 {
			stays, is = "stays", "is"
		}
		lines = append(lines, strconv.Itoa(n)+" further "+pluralOf(n, "change", "changes")+" "+stays+" outside the --max cap and "+is+" not sent.")
	}
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------------
// `/warden rules tune`: the flagged rules and the one prompt that asks for rewrites.

// TuneRule is a rule up for a rewrite.
type TuneRule struct {
	ID, Name string
	// Body is the rule's current text as the agent sees it in the rules file.
	Body string
	// Reasons: one line per flag: why this rule is up for a rewrite.
	Reasons []string
}

// TuneInputs are what tune reads: the latest calibration and rules check of the session, and the rules in force.
type TuneInputs struct {
	Calibration *Calibration
	Check       *RulesCheckResult
	Rules       []Rule
}

// tuneTargets are the rules the latest calibrate flagged, plus the ones `/warden rules check` flagged this session,
// with the reasons.
func tuneTargets(in TuneInputs) []TuneRule {
	var order []string
	targets := map[string]*TuneRule{}
	byID := map[string]Rule{}
	for _, rule := range in.Rules {
		byID[rule.ID] = rule
	}
	add := func(id, reason string) {
		rule, ok := byID[id]
		if !ok {
			return
		}
		entry := targets[id]
		if entry == nil {
			entry = &TuneRule{ID: id, Name: rule.Name, Body: Redact(rule.Body)}
			targets[id] = entry
			order = append(order, id)
		}
		entry.Reasons = append(entry.Reasons, reason)
	}
	if in.Calibration != nil {
		for _, row := range in.Calibration.Rows {
			// Only noise and indecision ask for a rewrite. A rule with no violation in the sample is not flagged: replayed
			// commits are mostly compliant code, so it is often a rule people follow.
			var flags []string
			for _, f := range row.Flags {
				if f == flagFiresOnEverything || f == flagUndecided {
					flags = append(flags, f)
				}
			}
			if len(flags) == 0 {
				continue
			}
			add(row.ID, "flagged "+strings.Join(flags, ", ")+" in the latest calibrate: "+strconv.Itoa(row.Applied)+" applied, "+strconv.Itoa(row.Fired)+" fired ("+strconv.Itoa(jsRound(row.FiredRate*100))+"%), mean "+toFixed(row.MeanViolation, 2))
		}
	}
	if in.Check != nil {
		for _, flagged := range in.Check.Attention {
			add(flagged.ID, "flagged by the rules check: "+strings.Join(checkReasons(flagged), "; "))
		}
	}
	out := make([]TuneRule, len(order))
	for i, id := range order {
		out[i] = *targets[id]
	}
	return out
}

// buildTunePrompt is one prompt for the session's agent: each flagged rule, why it was flagged, and the ask for a
// concrete rewrite.
func buildTunePrompt(targets []TuneRule) string {
	lines := []string{
		"Rewrite the flagged project rules in `pi-warden.md` with your file tools, so the user can review the edit. Each `#` heading is one rule; the text under it is what the guard judges against.",
		"",
		"For each rule below, rewrite it so a violation is concrete and judgeable from the content of one changed file alone: name the exact pattern that makes a change a violation (a call, a literal, a comment or naming shape, a file layout), and keep or add a `paths:` line when it only holds for some files. Keep a `threshold:` or `severity:` header line when the rule still needs one. When no single-file pattern exists, remove the rule instead of leaving it vague. Change only the rules named here; leave the rest of the file as it is.",
		"",
	}
	for _, target := range targets {
		lines = append(lines, "## "+target.Name+" ("+target.ID+")", "Flagged: "+strings.Join(target.Reasons, "; ")+".", "Current text:", target.Body, "")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), " \t\r\n")
}

// TuneRequest is the prompt to send, or the reason to say so and send nothing.
type TuneRequest struct {
	Prompt string
	Reason string
}

// tuneRequest is the prompt to send, or the reason to say so and send nothing when no rule is flagged.
func tuneRequest(in TuneInputs) TuneRequest {
	targets := tuneTargets(in)
	if len(targets) > 0 {
		return TuneRequest{Prompt: buildTunePrompt(targets)}
	}
	if len(in.Rules) == 0 {
		return TuneRequest{Reason: "Nothing to tune: there are no separate rules here to rewrite."}
	}
	if in.Calibration == nil && in.Check == nil {
		return TuneRequest{Reason: "Nothing flagged: run /warden rules calibrate or /warden rules check first, then /warden rules tune."}
	}
	return TuneRequest{Reason: "Nothing flagged: the latest calibrate and the rules check found no rule that needs a rewrite. Nothing was sent."}
}
