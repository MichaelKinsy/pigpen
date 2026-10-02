package warden

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The done-check (src/done.ts): a final message that claims the work is finished after file changes that no
// check has exercised is asked to verify, or to say plainly that nothing was verified.

// ToolOutcome is what a finished tool call contributes to the run's evidence.
type ToolOutcome string

// Tool outcomes.
const (
	OutcomeRead      ToolOutcome = "read"
	OutcomeMutation  ToolOutcome = "mutation"
	OutcomeCheckPass ToolOutcome = "check-pass"
	OutcomeCheckFail ToolOutcome = "check-fail"
	OutcomeUnknown   ToolOutcome = "unknown"
)

// reCheckCommand: commands whose success is evidence that the work was verified.
var reCheckCommand = regexp.MustCompile(`\b(?:(?:npm|pnpm|yarn|bun)\s+(?:run\s+)?(?:test|check|lint|typecheck|build|verify|ci)\b|(?:npx|pnpm|bunx)\s+(?:tsc|jest|vitest|mocha|eslint|biome|prettier\s+--check)\b|pytest|jest|vitest|mocha|tsc|eslint|biome\s+check|ruff|mypy|flake8|pylint|black\s+--check|cargo\s+(?:test|check|build|clippy)|go\s+(?:test|vet|build)|make\s+(?:test|check|lint|build)|mvn\s+(?:test|verify)|gradle\w*\s+(?:test|check|build)|dotnet\s+(?:test|build)|node\s+--test|deno\s+(?:test|check|lint)|rspec|rake\s+test|mix\s+test|phpunit|swift\s+(?:test|build)|xcodebuild\s+test|ctest|zig\s+(?:test|build))\b`)

var (
	reNodeTestAny  = regexp.MustCompile("\u2139 (?:tests|pass|fail) \\d+")
	reNodeTestFail = regexp.MustCompile("\u2139 fail (\\d+)")
	reJest         = regexp.MustCompile(`(?m)^Tests:\s+(?:(\d+) failed, )?.*?\d+ total`)
	rePytestA      = regexp.MustCompile(`(?m)^=+ .*?(?:(\d+) failed|(\d+) error).*?in [\d.]+s`)
	rePytestB      = regexp.MustCompile(`(?m)^=+ (\d+) passed.*? in [\d.]+s =+$`)
	rePytestFail   = regexp.MustCompile(`\d+ (?:failed|error)`)
	reCargo        = regexp.MustCompile(`(?m)^test result: (ok|FAILED)\.`)
	reGoTest       = regexp.MustCompile(`(?m)^(ok|FAIL)\s+\S+\s+[\d.]+s$`)
	reTsc          = regexp.MustCompile(`\berror TS\d{4}:`)
)

// CheckSummary recognises a test or type-check runner's own summary in tool output (node:test, jest/vitest,
// pytest, cargo, go test, tsc): "pass", "fail", or "" when no runner summary is present.
func CheckSummary(output string) string {
	tail := output
	if n := utf16Len(output); n > 6000 {
		tail = utf16Slice(output, n-6000, -1)
	}
	if reNodeTestAny.MatchString(tail) {
		if m := reNodeTestFail.FindStringSubmatch(tail); m != nil {
			if n, _ := strconv.Atoi(m[1]); n > 0 {
				return "fail"
			}
			return "pass"
		}
	}
	if m := reJest.FindStringSubmatch(tail); m != nil {
		if n, _ := strconv.Atoi(m[1]); m[1] != "" && n > 0 {
			return "fail"
		}
		return "pass"
	}
	m := rePytestA.FindString(tail)
	if m == "" {
		m = rePytestB.FindString(tail)
	}
	if m != "" {
		if rePytestFail.MatchString(m) {
			return "fail"
		}
		return "pass"
	}
	sm := reCargo.FindStringSubmatch(tail)
	if sm == nil {
		sm = reGoTest.FindStringSubmatch(tail)
	}
	if sm != nil {
		if sm[1] == "ok" {
			return "pass"
		}
		return "fail"
	}
	if reTsc.MatchString(tail) {
		return "fail"
	}
	return ""
}

// ClassifyToolResult: what a finished tool call contributes to the run's evidence. Only write and edit count as
// code changes: shell side effects (deleting a temp dir, installing a package) are too varied to demand a test
// run for. Custom tools are unknown.
func ClassifyToolResult(tool string, input map[string]any, failed bool, output string) ToolOutcome {
	switch tool {
	case "write", "edit":
		return OutcomeMutation
	case "read", "grep", "find", "ls":
		return OutcomeRead
	}
	view, ok := CommandOf(tool, input)
	if !ok {
		return OutcomeUnknown
	}
	if reCheckCommand.MatchString(view.Command) {
		if failed {
			return OutcomeCheckFail
		}
		return OutcomeCheckPass
	}
	// A test runner launched from inside a script leaves no runner name in the command text, but its output still
	// carries the runner's summary.
	if s := CheckSummary(output); s != "" {
		if s == "fail" || failed {
			return OutcomeCheckFail
		}
		return OutcomeCheckPass
	}
	if view.Shell && IsReadOnlyCommand(view.Command) {
		return OutcomeRead
	}
	return OutcomeUnknown
}

// Check is one check command that ran.
type Check struct {
	Call   string
	Passed bool
}

// RunEvidence is a run's changes and checks.
type RunEvidence struct {
	Mutations            int
	Checks               []Check
	ChecksBeforeMutation *int
	// UnseenUI is the last UI file changed with no visual check after it.
	UnseenUI string
}

// EmptyEvidence starts a run.
func EmptyEvidence() *RunEvidence { return &RunEvidence{} }

// RecordOutcome adds one tool result to the run's evidence.
func RecordOutcome(e *RunEvidence, outcome ToolOutcome, input map[string]any, tool string) {
	if outcome == OutcomeMutation {
		e.Mutations++
		n := len(e.Checks)
		e.ChecksBeforeMutation = &n
	}
	if outcome == OutcomeCheckPass || outcome == OutcomeCheckFail {
		call := "check"
		if view, ok := CommandOf(tool, input); ok {
			c := view.Command
			if utf16Len(c) > 200 {
				c = utf16Slice(c, 0, 200) + "…"
			}
			call = Redact(c)
		}
		e.Checks = append(e.Checks, Check{Call: call, Passed: outcome == OutcomeCheckPass})
	}
}

// ---------------------------------------------------------------------------
// UI files.

var reBrace = regexp.MustCompile(`\{([^{}]*)\}`)

// expandBraces expands `{a,b}` alternatives, which the glob matcher does not read.
func expandBraces(pattern string) []string {
	m := reBrace.FindStringSubmatchIndex(pattern)
	if m == nil {
		return []string{pattern}
	}
	var out []string
	for _, option := range strings.Split(pattern[m[2]:m[3]], ",") {
		out = append(out, expandBraces(pattern[:m[0]]+option+pattern[m[1]:])...)
	}
	return out
}

// globToRegexp is rules.ts `globToRegExp`: `**/` any directories, `**` anything, `*` within a segment, `?` one
// character; every pattern also matches under any parent directory.
func globToRegexp(pattern string) *regexp.Regexp {
	var out strings.Builder
	for i := 0; i < len(pattern); {
		switch {
		case strings.HasPrefix(pattern[i:], "**/"):
			out.WriteString("(?:.*/)?")
			i += 3
		case strings.HasPrefix(pattern[i:], "**"):
			out.WriteString(".*")
			i += 2
		case pattern[i] == '*':
			out.WriteString("[^/]*")
			i++
		case pattern[i] == '?':
			out.WriteString("[^/]")
			i++
		default:
			r := []rune(pattern[i:])[0]
			out.WriteString(regexp.QuoteMeta(string(r)))
			i += len(string(r))
		}
	}
	re, err := regexp.Compile("^(?:.*/)?" + out.String() + "$")
	if err != nil {
		return regexp.MustCompile(`\A\z\A`)
	}
	return re
}

func matchGlob(path string, patterns []string) bool {
	normalised := strings.TrimPrefix(path, "./")
	for _, p := range patterns {
		if globToRegexp(jsTrim(p)).MatchString(normalised) {
			return true
		}
	}
	return false
}

// IsUIFile: a path matches when some glob matches it and no `!` glob does.
func IsUIFile(path string, globs []string) bool {
	normalised := strings.ReplaceAll(path, `\`, "/")
	var include, exclude []string
	for _, g := range globs {
		if strings.HasPrefix(g, "!") {
			exclude = append(exclude, expandBraces(g[1:])...)
		} else {
			include = append(include, expandBraces(g)...)
		}
	}
	return matchGlob(normalised, include) && !matchGlob(normalised, exclude)
}

var (
	reShellSegLead = regexp.MustCompile(`^(?:[A-Za-z_][A-Za-z0-9_]*=\S*\s+)+`)
	reFlutterTest  = regexp.MustCompile(`(?:^|\s)flutter test$`)
	reIntegration  = regexp.MustCompile(`(?:^|/)integration_test(?:/|$)`)
	reMcp          = regexp.MustCompile(`^mcp(?:__|$)`)
	reLeadDashes   = regexp.MustCompile(`^-+`)
	reEqualsTail   = regexp.MustCompile(`=.*$`)
	browserHeads   = setOf("chrome", "chromium", "google-chrome")
)

func shellSegments(command string) []string {
	var out []string
	for _, part := range reSplitShell.Split(command, -1) {
		p := reShellSegLead.ReplaceAllString(jsTrim(part), "")
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// headShows: heads that also run work the user never sees. `flutter test` shows the UI only for integration or
// golden tests, `idb` only through `screenshot` and `ui`, a browser binary only when a command word asks for it.
func headShows(head string, args []string) bool {
	if reFlutterTest.MatchString(head) {
		for _, a := range args {
			if reIntegration.MatchString(a) || strings.Contains(a, "golden") {
				return true
			}
		}
		return false
	}
	if head == "idb" {
		for _, a := range args {
			if a == "screenshot" || a == "ui" {
				return true
			}
		}
		return false
	}
	return !browserHeads[head]
}

// IsVisualCheck reports whether a successful tool call showed the rendered UI: a browser or device command, a
// screenshot, a browser MCP tool, or reading an image. A test run proves the code runs; only this proves what
// the user will see.
func IsVisualCheck(tool string, input map[string]any, failed bool, visual VisualTools) bool {
	if failed {
		return false
	}
	if tool == "read" {
		path, _ := input["path"].(string)
		path = strings.ToLower(path)
		for _, ext := range visual.Images {
			if strings.HasSuffix(path, "."+strings.ToLower(ext)) {
				return true
			}
		}
		return false
	}
	if view, ok := CommandOf(tool, input); ok {
		if !view.Shell {
			return false
		}
		// A PR body, commit message or heredoc note that mentions a screenshot is not one.
		segments := shellSegments(strings.ToLower(StripDataText(view.Command).Text))
		words := map[string]bool{}
		for _, w := range visual.CommandWords {
			words[strings.ToLower(w)] = true
		}
		for _, seg := range segments {
			for _, h := range visual.Commands {
				head := strings.ToLower(h)
				if seg != head && !strings.HasPrefix(seg, head+" ") {
					continue
				}
				var args []string
				for _, a := range jsSplitWhitespace(seg[len(head):]) {
					if a != "" {
						args = append(args, a)
					}
				}
				if headShows(head, args) {
					return true
				}
				// A command word counts only after a visual head: `grep -rn screenshot src` searches for the word.
				for _, a := range args {
					if words[reEqualsTail.ReplaceAllString(reLeadDashes.ReplaceAllString(a, ""), "")] {
						return true
					}
				}
			}
		}
		return false
	}
	names := []string{strings.ToLower(tool)}
	if reMcp.MatchString(tool) {
		if inner, ok := input["tool"].(string); ok {
			names = append(names, strings.ToLower(inner))
		}
	}
	for _, name := range names {
		for _, word := range visual.Tools {
			if name != "" && strings.Contains(name, strings.ToLower(word)) {
				return true
			}
		}
	}
	return false
}

// RecordUI notes the paths a successful call changed that match the UI globs, and a visual check that saw them.
func RecordUI(e *RunEvidence, changed []string, visual bool, globs []string) {
	var ui []string
	for _, p := range changed {
		if IsUIFile(p, globs) {
			ui = append(ui, p)
		}
	}
	if len(ui) > 0 {
		e.UnseenUI = ui[len(ui)-1]
	} else if visual {
		e.UnseenUI = ""
	}
}

// FinalAssistantText is the text of the run's final assistant message, when it ended normally with text (not a
// tool call, error or abort).
func FinalAssistantText(messages []map[string]any) (string, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if role, _ := m["role"].(string); role != "assistant" {
			continue
		}
		if stop, ok := m["stopReason"].(string); ok && stop != "stop" {
			return "", false
		}
		var text string
		switch c := m["content"].(type) {
		case string:
			text = c
		case []any:
			var parts []string
			for _, p := range c {
				if pm, ok := p.(map[string]any); ok && pm["type"] == "text" {
					if s, ok := pm["text"].(string); ok {
						parts = append(parts, s)
					}
				}
			}
			text = strings.Join(parts, "\n")
		case []map[string]any:
			var parts []string
			for _, pm := range c {
				if pm["type"] == "text" {
					if s, ok := pm["text"].(string); ok {
						parts = append(parts, s)
					}
				}
			}
			text = strings.Join(parts, "\n")
		}
		text = jsTrim(text)
		return text, text != ""
	}
	return "", false
}

// FreshChecks are the checks that ran after the latest change: only those ran on the code as it stands now.
func FreshChecks(e *RunEvidence) []Check {
	if e == nil {
		return nil
	}
	from := 0
	if e.ChecksBeforeMutation != nil {
		from = *e.ChecksBeforeMutation
	}
	if from > len(e.Checks) {
		from = len(e.Checks)
	}
	return e.Checks[from:]
}

func anyPassed(checks []Check) bool {
	for _, c := range checks {
		if c.Passed {
			return true
		}
	}
	return false
}

// NeedsDoneCheck: the check only makes sense when something changed and nothing proved that change works.
func NeedsDoneCheck(e *RunEvidence) bool {
	return (e.Mutations > 0 && !anyPassed(FreshChecks(e))) || e.UnseenUI != ""
}

// DoneJudgment is the judge's answer about a final message.
type DoneJudgment struct {
	ClaimsDone          float64
	ClaimsVerified      float64
	VerificationApplies float64
	Outcome             string
	Model               string
	ElapsedMs           int
}

// DoneVerdict is the done-check's decision.
type DoneVerdict struct {
	Unverified bool
	// FalseClaim: the message says checks passed but none ran; stronger than an unverified claim.
	FalseClaim bool
	Reasons    []string
	Evidence   *RunEvidence
	Judgment   *DoneJudgment
	// UnseenUI is set when the claim follows a UI change that nothing showed on screen.
	UnseenUI  string
	Error     string
	ErrorCode string
}

const appliesThreshold = 0.5

// BuildDoneRequest is the request for one final message.
func BuildDoneRequest(task, finalMessage string, e *RunEvidence) Request {
	taskText := "(no user request recorded in this session)"
	if t := jsTrim(task); t != "" {
		// Redacted like the action request's task (deviation D11: done.ts sends it as typed).
		t = Redact(t)
		taskText = t
		if utf16Len(t) > 1500 {
			taskText = utf16Slice(t, 0, 1500) + "…"
		}
	}
	msg := finalMessage
	if utf16Len(msg) > 2000 {
		msg = utf16Slice(msg, 0, 2000) + "…"
	}
	ran := []string{}
	for _, c := range FreshChecks(e) {
		outcome := "failed"
		if c.Passed {
			outcome = "passed"
		}
		ran = append(ran, c.Call+" → "+outcome)
	}
	mutations := 0
	if e != nil {
		mutations = e.Mutations
	}
	return Request{
		State:     map[string]any{"task": taskText, "final_message": Redact(msg), "run": map[string]any{"file_changes": mutations, "checks_run": ran}},
		Questions: build(qs.Done),
	}
}

// DoneOptions configure EvaluateDone.
type DoneOptions struct {
	Config  DoneConfig
	Judge   Judge
	Timeout time.Duration
}

// EvaluateDone asks the judge whether the final message claims completion, and compares the claim with the
// run's evidence.
func EvaluateDone(ctx context.Context, task, finalMessage string, e *RunEvidence, opts DoneOptions) DoneVerdict {
	res := Ask(ctx, opts.Judge, BuildDoneRequest(task, finalMessage, e), opts.Timeout)
	if !res.OK {
		return DoneVerdict{Evidence: e, Error: res.Error, ErrorCode: res.ErrorCode}
	}
	j := &DoneJudgment{
		ClaimsDone:          res.Answers["claims_done"].Noul,
		ClaimsVerified:      res.Answers["claims_verified"].Noul,
		VerificationApplies: res.Answers["verification_applies"].Noul,
		Outcome:             res.Answers["outcome"].Choice,
		Model:               res.Model,
		ElapsedMs:           res.ElapsedMs,
	}
	claimed := j.ClaimsDone >= opts.Config.ClaimsDone && j.Outcome != "blocked"
	checks := FreshChecks(e)
	codeUnverified := claimed && e.Mutations > 0 && !anyPassed(checks) && j.VerificationApplies >= appliesThreshold
	// The file type already says a visual check applies, so verification_applies (about tests and builds) does not gate it.
	unseenUI := ""
	if claimed {
		unseenUI = e.UnseenUI
	}
	unverified := codeUnverified || unseenUI != ""
	// Total checks, not fresh: a false claim is nothing ever run in the run; a stale check is unverified, not a lie.
	falseClaim := unverified && j.ClaimsVerified >= 0.7 && len(e.Checks) == 0
	var reasons []string
	if codeUnverified {
		failed := 0
		for _, c := range checks {
			if !c.Passed {
				failed++
			}
		}
		plural := func(n int, word string) string {
			if n == 1 {
				return strconv.Itoa(n) + " " + word
			}
			return strconv.Itoa(n) + " " + word + "s"
		}
		with := "no test, build, or lint run since the last change"
		if failed > 0 {
			with = plural(failed, "failed check") + " and no passing one"
		}
		reasons = append(reasons, "reports completion ("+toFixed(j.ClaimsDone, 2)+") after "+plural(e.Mutations, "file change")+" with "+with)
	}
	if unseenUI != "" {
		if codeUnverified {
			reasons = append(reasons, "no browser, screenshot, or device check since the last UI change")
		} else {
			reasons = append(reasons, "reports completion ("+toFixed(j.ClaimsDone, 2)+") after a UI change with no browser, screenshot, or device check since")
		}
	}
	if falseClaim {
		reasons = append(reasons, "claims checks passed ("+toFixed(j.ClaimsVerified, 2)+") but none ran")
	}
	return DoneVerdict{Unverified: unverified, FalseClaim: falseClaim, Reasons: reasons, Evidence: e, Judgment: j, UnseenUI: unseenUI}
}

// DoneNudge is the follow-up for the agent: verify, or say plainly that nothing was verified.
func DoneNudge(v DoneVerdict) string {
	fresh := FreshChecks(v.Evidence)
	var failed []Check
	for _, c := range fresh {
		if !c.Passed {
			failed = append(failed, c)
		}
	}
	applies := 1.0
	if v.Judgment != nil {
		applies = v.Judgment.VerificationApplies
	}
	codeUnverified := v.UnseenUI == "" || (v.Evidence.Mutations > 0 && !anyPassed(fresh) && applies >= appliesThreshold)
	detail := "Run the project's tests, build, or lint (whatever exists) on what you changed."
	if len(failed) > 0 {
		detail = "The last check that ran failed: " + failed[len(failed)-1].Call + ". Fix that first."
	}
	code := ""
	if codeUnverified {
		code = " " + detail + " Then report the actual result. If no check exists or can run, say so explicitly instead of presenting the work as done."
	}
	ui := ""
	if v.UnseenUI != "" {
		ui = " You changed `" + Redact(v.UnseenUI) + "` but did not look at the result. Open it in a browser or take a screenshot before calling it done, or say it is unverified."
	}
	return "pi-warden: " + strings.Join(v.Reasons, "; ") + "." + code + ui
}
