package warden

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// End-of-run rules (src/turn-rules.ts). `when: turn` rules are judged once against the whole run's diff and the user's
// task, and files a shell command changed in ways the per-edit guard cannot see (`sed -i`, generated output) are judged
// with the edit rules against their diff. The baseline is a read-only snapshot of the working tree: a temporary index
// file, so the working tree, the real index, and the stash list are never touched. Everything is steered, never held.
// Every git call runs off the hook's critical path in its own process and under a timeout, so the hooks that start the
// snapshot and take the end-of-run diff never block the UI while git hashes the changed and untracked files.

const (
	turnGitTimeout = 30 * time.Second
	// turnPath is the verdict path for a judgment about the whole run; the rules log keys clears by path, so it must
	// not look like a file.
	turnPath           = "(whole turn)"
	turnQuestionPrefix = "turn_"
	// shellRulesChecks is the files judged per shell command and per run; beyond the cap the files are named in the
	// trace and left out.
	shellRulesChecks = 5
	// gitMaxBuffer is Node's default `execFile` output cap: a diff past it fails the call.
	gitMaxBuffer = 1024 * 1024
)

// gitOutput runs git and returns its stdout, untrimmed. An error covers a failing exit, a timeout and output past
// gitMaxBuffer.
func gitOutput(cwd string, args []string, timeout time.Duration, extraEnv ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = cwd
	cmd.Env = append(append(os.Environ(), "GIT_OPTIONAL_LOCKS=0"), extraEnv...)
	hideWindow(cmd)
	var out bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &out, left: gitMaxBuffer}
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return decodeUTF8(out.Bytes()), nil
}

type limitedWriter struct {
	w    io.Writer
	left int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if len(p) > l.left {
		return 0, io.ErrShortWrite
	}
	l.left -= len(p)
	return l.w.Write(p)
}

var reTreeID = lazyRE(`^[0-9a-f]{40,64}$`)

// workTree is a tree of the working directory as it stands, untracked non-ignored files included. The index is copied
// to a temporary file first so files that are tracked but ignored stay in the snapshot; only that copy is ever written.
func workTree(cwd string) (string, bool) {
	var suffix [6]byte
	_, _ = rand.Read(suffix[:])
	temporary := filepath.Join(os.TempDir(), "pi-warden-tree-"+strconv.Itoa(os.Getpid())+"-"+hex.EncodeToString(suffix[:]))
	defer os.Remove(temporary)
	if index, err := gitOutput(cwd, []string{"rev-parse", "--git-path", "index"}, 5*time.Second); err == nil {
		index = jsTrim(index)
		if data, err := os.ReadFile(resolvePath(cwd, index)); err == nil {
			// A repository with no index yet starts from an empty one; the files on disk are added below.
			_ = os.WriteFile(temporary, data, 0o600)
		}
	}
	env := "GIT_INDEX_FILE=" + temporary
	if _, err := gitOutput(cwd, []string{"add", "-A"}, turnGitTimeout, env); err != nil {
		return "", false
	}
	tree, err := gitOutput(cwd, []string{"write-tree"}, 10*time.Second, env)
	if err != nil {
		return "", false
	}
	tree = jsTrim(tree)
	if !reTreeID.MatchString(tree) {
		return "", false
	}
	return tree, true
}

// SnapshotResult is the run's baseline; Reason says why turn rules are skipped this run and earns one trace line.
type SnapshotResult struct {
	Tree   string
	Reason string
}

// snapshotTree takes the baseline tree of the working directory.
func snapshotTree(cwd string) SnapshotResult {
	inside, err := gitOutput(cwd, []string{"rev-parse", "--is-inside-work-tree"}, 5*time.Second)
	if err != nil || jsTrim(inside) != "true" {
		return SnapshotResult{Reason: "not a git repository"}
	}
	if tree, ok := workTree(cwd); ok {
		return SnapshotResult{Tree: tree}
	}
	return SnapshotResult{Reason: "the git snapshot failed"}
}

// TurnFileDiff is one changed file of the run.
type TurnFileDiff struct {
	// Path is project-relative with forward slashes.
	Path string
	// Status is M, A or D, as git reports it between the two trees.
	Status string
	// Diff is this file's unified diff, capped at maxChars with an inline note when cut.
	Diff string
}

// TurnDiff is the working-tree change since the snapshot.
type TurnDiff struct {
	Files []TurnFileDiff
	// Text is the whole change for the turn question: every file's diff joined, capped at maxChars in total.
	Text string
	// Cuts names what the caps cut, per file, so the request and the trace say what the judge does not see.
	Cuts []string
}

// diffSince is the working-tree change since the snapshot: the files git reports and each one's unified diff, capped
// per file and in total at maxChars (the rules.maxChars cap), each cut named. Untracked non-ignored files are in both
// trees, so they appear here like any other change. nil when the end-of-run snapshot fails.
func diffSince(cwd, startTree string, maxChars int) *TurnDiff {
	endTree, ok := workTree(cwd)
	if !ok {
		return nil
	}
	listing, err := gitOutput(cwd, []string{"diff", "--name-status", "--no-renames", "-z", startTree, endTree, "--"}, turnGitTimeout)
	if err != nil {
		return nil
	}
	names := strings.Split(listing, "\x00")
	diff := &TurnDiff{}
	for index := 0; index+1 < len(names); index += 2 {
		status := utf16Slice(names[index], 0, 1)
		path := names[index+1]
		if path == "" {
			continue
		}
		text, err := gitOutput(cwd, []string{"diff", "--no-ext-diff", "--no-renames", startTree, endTree, "--", path}, turnGitTimeout)
		if err != nil || jsTrim(text) == "" {
			continue
		}
		if n := utf16Len(text); n > maxChars {
			diff.Cuts = append(diff.Cuts, path+": file diff cut from "+strconv.Itoa(n)+" to "+strconv.Itoa(maxChars)+" chars")
			text = utf16Slice(text, 0, maxChars) + "… [" + strconv.Itoa(n-maxChars) + " more chars of this file's diff cut]"
		}
		diff.Files = append(diff.Files, TurnFileDiff{Path: path, Status: status, Diff: text})
	}
	var chunks []string
	total := 0
	spent := false
	for _, file := range diff.Files {
		if spent {
			diff.Cuts = append(diff.Cuts, file.Path+": file diff not sent (the "+strconv.Itoa(maxChars)+"-char total is spent)")
			continue
		}
		room := maxChars - total
		if n := utf16Len(file.Diff); n <= room {
			chunks = append(chunks, file.Diff)
			total += n + 1
			continue
		}
		chunks = append(chunks, utf16Slice(file.Diff, 0, room)+"… [the rest of this file's diff and every later file are cut at the "+strconv.Itoa(maxChars)+"-char total]")
		diff.Cuts = append(diff.Cuts, file.Path+": file diff cut to "+strconv.Itoa(room)+" chars to fit the "+strconv.Itoa(maxChars)+"-char total; later files not sent")
		spent = true
	}
	diff.Text = strings.Join(chunks, "\n")
	return diff
}

// ---------------------------------------------------------------------------
// The turn question: one Choice per turn rule, against the whole diff and the task.

func turnRuleQuestion(rule Rule) Question {
	return outcomeChoice(ruleQuestionText(rulesQuestions().turnFrame, rule))
}

func buildTurnRequest(task, diff string, rules []Rule) (map[string]any, Questions) {
	questions := Questions{}
	for _, rule := range rules {
		questions[turnQuestionPrefix+rule.ID] = turnRuleQuestion(rule)
	}
	return map[string]any{"task": task, "diff": diff}, questions
}

// TurnRulesOptions are the inputs of the turn judgment.
type TurnRulesOptions struct {
	Ctx     context.Context
	Judge   Judge
	Config  RulesConfig
	Timeout time.Duration
	Sources []string
}

// evaluateTurnRules is one request that judges the turn rules against the whole diff and the task.
func evaluateTurnRules(task, diff string, rules []Rule, options TurnRulesOptions) RulesVerdict {
	base := RulesVerdict{Tool: "turn", Path: turnPath, Sources: append([]string(nil), options.Sources...), Asked: len(rules)}
	state, questions := buildTurnRequest(Redact(task), Redact(diff), rules)
	ctx := options.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	result := Ask(ctx, options.Judge, Request{State: state, Questions: questions}, options.Timeout)
	if !result.OK {
		base.Source, base.Error, base.ErrorCode = "error", result.Error, result.ErrorCode
		return base
	}
	entries := make([]RuleScoreEntry, len(rules))
	for i := range rules {
		rule := rules[i]
		entries[i] = RuleScoreEntry{Key: turnQuestionPrefix + rule.ID, ID: rule.ID, Name: rule.Name, Body: rule.Body, Rule: &rule}
	}
	scoring := scoreAnswers(result.Answers, entries, options.Config)
	base.Source, base.Scores, base.Findings, base.SoftFindings = "typesafe", scoring.Scores, scoring.Findings, scoring.SoftFindings
	base.Model, base.ElapsedMs = result.Model, new(result.ElapsedMs)
	return base
}

// ---------------------------------------------------------------------------
// The end-of-run pass: one turn request, then one request per file no per-edit judgment saw, at most shellRulesChecks
// of the files per run.

// TurnRunOptions are the inputs of the end-of-run pass.
type TurnRunOptions struct {
	Ctx     context.Context
	Cwd     string
	Config  RulesConfig
	Set     *RuleSet
	Judge   Judge
	Timeout time.Duration
	// Task is the user's request for this run.
	Task string
	// StartTree is the snapshot tree taken at the start of the run.
	StartTree string
	// AlreadyJudged are the project-relative paths a `write`, `edit`, or literal shell write already judged this run.
	AlreadyJudged map[string]bool
}

// TurnRunResult is the outcome of the end-of-run pass.
type TurnRunResult struct {
	// Verdicts: the turn verdict first (when turn rules exist), then one per file a per-edit judgment never saw.
	Verdicts []RulesVerdict
	// Cuts names what the caps cut from the diff, per file.
	Cuts []string
	// Skips are the files the per-run request cap left unjudged, named for the trace like the shell path's skips.
	Skips []ShellSkip
	// Skipped says why nothing was judged, when the pass was skipped; empty when there was nothing to judge or it ran.
	Skipped string
}

// unjudgedFiles are the files of the diff no per-edit judgment saw: in-place and generated changes, and subagent writes.
func unjudgedFiles(diff *TurnDiff, alreadyJudged map[string]bool) []TurnFileDiff {
	var out []TurnFileDiff
	for _, file := range diff.Files {
		if !alreadyJudged[file.Path] {
			out = append(out, file)
		}
	}
	return out
}

func evaluateTurnRun(options TurnRunOptions) TurnRunResult {
	diff := diffSince(options.Cwd, options.StartTree, options.Config.MaxChars)
	if diff == nil {
		return TurnRunResult{Skipped: "the end-of-run git snapshot failed"}
	}
	if len(diff.Files) == 0 {
		return TurnRunResult{Cuts: diff.Cuts}
	}
	var turnRules []Rule
	if options.Set != nil {
		turnRules = turnRulesFor(options.Set)
		if len(turnRules) > maxRules {
			turnRules = turnRules[:maxRules]
		}
	}
	files := unjudgedFiles(diff, options.AlreadyJudged)
	// With no turn rules and no change the per-edit guard missed, the run is judged exactly as it was before.
	if len(turnRules) == 0 && len(files) == 0 {
		return TurnRunResult{Cuts: diff.Cuts}
	}
	if options.Set == nil {
		return TurnRunResult{Cuts: diff.Cuts, Skipped: "no rules file"}
	}
	if options.Judge == nil {
		return TurnRunResult{Cuts: diff.Cuts, Skipped: "TypeSafe judgments are off"}
	}
	result := TurnRunResult{Cuts: diff.Cuts}
	if len(turnRules) > 0 {
		result.Verdicts = append(result.Verdicts, evaluateTurnRules(options.Task, diff.Text, turnRules, TurnRulesOptions{Ctx: options.Ctx, Judge: options.Judge, Config: options.Config, Timeout: options.Timeout, Sources: options.Set.Sources}))
	}
	// The same cap the per-edit path puts on the files one command writes: the first files in diff order are judged,
	// the rest are named in the trace and left out, so one run can never start an unbounded number of requests.
	if len(files) > shellRulesChecks {
		for _, file := range files[shellRulesChecks:] {
			result.Skips = append(result.Skips, ShellSkip{Path: file.Path, Reason: "only the first " + strconv.Itoa(shellRulesChecks) + " files a run changes are judged"})
		}
		files = files[:shellRulesChecks]
	}
	for _, file := range files {
		if gitIgnored(file.Path, options.Cwd, false) {
			result.Verdicts = append(result.Verdicts, RulesVerdict{Source: "skipped", Tool: "edit", Path: file.Path, Sources: options.Set.Sources, SkippedReason: "gitignored by the project"})
			continue
		}
		// The file's diff stands as the edit: the same question and scoring a `write` or `edit` of this file would get.
		target := &RulesTarget{Tool: "edit", Path: file.Path, Edits: []EditView{{ID: "diff", NewText: Redact(truncate(file.Diff, options.Config.MaxChars))}}}
		result.Verdicts = append(result.Verdicts, evaluateRulesTarget(target, "edit", file.Path, RulesOptions{Ctx: options.Ctx, Cwd: options.Cwd, Config: options.Config, Set: options.Set, Judge: options.Judge, Timeout: options.Timeout}))
	}
	return result
}

// ---------------------------------------------------------------------------
// What the agent is told: one steer for the whole run.

// turnSteer names each violated rule like the per-edit steer; one message covers the turn verdict and the missed
// files. "" when nothing was found.
func turnSteer(verdicts []RulesVerdict, counts map[string]int) string {
	var parts []string
	standing := false
	for _, verdict := range verdicts {
		if len(verdict.Findings) == 0 {
			continue
		}
		named, repeat := namedFindings(verdict.Findings, counts, false)
		standing = standing || repeat
		where := "the changes this run made"
		verb := "violate"
		if verdict.Tool != "turn" {
			where = "the change to " + verdict.Path + " (made by a command, not an edit)"
			verb = "violates"
		}
		what := verb + " project rules"
		if len(verdict.Findings) == 1 {
			what = verb + " a project rule"
		}
		parts = append(parts, where+" "+what+": "+named)
	}
	if len(parts) == 0 {
		return ""
	}
	tail := ""
	if standing {
		tail = " Treat this as a standing rule for the rest of the session."
	}
	return "pi-warden: " + strings.Join(parts, "; ") + ". Review the run's diff and fix the violations before you finish." + tail
}
