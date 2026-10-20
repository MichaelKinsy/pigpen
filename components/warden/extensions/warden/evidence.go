package warden

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Structured evidence for the stuck judge (src/evidence.ts): what a run failed on, and what an edit changed, in a
// shape a judge can compare across attempts. The parser is generic: it knows the shapes of common runners and
// compilers (node test/TAP, jest, vitest, pytest, tsc, eslint, cargo, go, a `make test` script, Playwright, Python
// and Node tracebacks) and falls back to an error line (`generic`) or to the head and tail of the raw output
// (`unparsed`). Nothing here redacts what the caller keeps; every string in the built evidence is redacted.

const (
	evidenceMaxItems     = 6
	evidenceDiffCap      = 600
	evidenceFallbackHead = 300
	evidenceFallbackTail = 300
	// EvidenceLimit is the byte cap for the whole evidence object; over it, the oldest runs go first, then the
	// oldest edits.
	EvidenceLimit = 4096
)

var (
	// jsSpace is JavaScript's `\s`, which Go's `\s` is narrower than.
	reJSSpace = &lazyRegexp{expr: "[\\s\\x{000b}\\x{00a0}\\x{1680}\\x{2000}-\\x{200a}\\x{2028}\\x{2029}\\x{202f}\\x{205f}\\x{3000}\\x{feff}]+"}
	reRunner  = &lazyRegexp{expr: `\b(?:(?:npm|pnpm|yarn|bun)\s+(?:run\s+)?(?:test|check|lint|typecheck|build|e2e)|(?:npx|pnpm|bunx)\s+(?:tsc|jest|vitest|eslint|playwright)|node\s+--test|pytest|jest|vitest|tsc|eslint|cargo\s+(?:test|check|build|clippy)|go\s+(?:test|vet|build)|make\s+\w+|sbcl\s+--script)\b`}
)

func isEditTool(tool string) bool { return tool == "edit" || tool == "write" }

func evClip(value string, limit int) string {
	v := jsTrim(reJSSpace.ReplaceAllString(value, " "))
	return utf16Slice(v, 0, limit)
}

func evUniq(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range values {
		c := evClip(v, 160)
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out
}

func evHeadTail(text string, head, tail int) string {
	n := utf16Len(text)
	if n <= head+tail {
		return text
	}
	return utf16Slice(text, 0, head) + "… [" + strconv.Itoa(n-head-tail) + " middle chars] …" + utf16Slice(text, n-tail, -1)
}

func evTailOf(text string, limit int) string {
	n := utf16Len(text)
	if n <= limit {
		return text
	}
	return "[" + strconv.Itoa(n-limit) + " earlier chars] …" + utf16Slice(text, n-limit, -1)
}

// ParsedFailure is a failed or check output parsed into failing tests, error lines, a location, a summary and an
// exit code.
type ParsedFailure struct {
	Format      string
	Failing     []string
	FailingMore int
	Errors      []string
	Location    string
	Summary     string
	// Exit is nil when the output names no exit code.
	Exit *int
}

var reEvExitCode = &lazyRegexp{expr: `(?im)(?:exited with code|exit code:?|exit status|Error)\s+(\d+)\s*$`}

func evExitCode(text string) *int {
	m := reEvExitCode.FindAllStringSubmatch(text, -1)
	if len(m) == 0 {
		return nil
	}
	n, err := strconv.Atoi(m[len(m)-1][1])
	if err != nil {
		return nil
	}
	return &n
}

type parsedFields struct {
	failing  []string
	errors   []string
	location string
	summary  string
}

func evResult(format string, f parsedFields, exit *int) ParsedFailure {
	failing := evUniq(f.failing)
	errs := evUniq(f.errors)
	out := ParsedFailure{Format: format, Exit: exit}
	out.Failing = failing[:min(len(failing), evidenceMaxItems)]
	if len(failing) > evidenceMaxItems {
		out.FailingMore = len(failing) - evidenceMaxItems
	}
	out.Errors = errs[:min(len(errs), evidenceMaxItems)]
	if f.location != "" {
		out.Location = evClip(f.location, 160)
	}
	if f.summary != "" {
		out.Summary = evClip(f.summary, 160)
	}
	return out
}

// evBlockAfter is the lines after index from, up to (not including) the first line that matches stop.
func evBlockAfter(lines []string, from int, stop *lazyRegexp) []string {
	out := []string{}
	for i := from + 1; i < len(lines) && !stop.MatchString(lines[i]); i++ {
		out = append(out, lines[i])
	}
	return out
}

func subs(re *lazyRegexp, text string) [][]string { return re.FindAllStringSubmatch(text, -1) }

func col(ms [][]string, i int) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m[i])
	}
	return out
}

func firstGroup(re *lazyRegexp, text string) string {
	if m := re.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	return ""
}

var (
	reTscLine      = &lazyRegexp{expr: `(?m)^(\S+?)\((\d+),(\d+)\): error (TS\d+): (.+)$`}
	reCargoTest    = &lazyRegexp{expr: `(?m)^test (\S+) \.\.\. FAILED$`}
	reCargoCompile = &lazyRegexp{expr: `(?m)^error(\[E\d+\])?: (.+)\n\s*--> (\S+)`}
	reCargoPanic   = &lazyRegexp{expr: `panicked at (\S+?):\n((?:.+\n?){1,3})`}
	reCargoSummary = &lazyRegexp{expr: `(?m)^test result: FAILED\. (.+?); finished`}
	reNotePrefix   = &lazyRegexp{expr: `^note:`}
	reGoFail       = &lazyRegexp{expr: `(?m)^\s*--- FAIL: (\S+)`}
	reGoBuild      = &lazyRegexp{expr: `(?m)^(\S+\.go):(\d+):(\d+): (.+)$`}
	reGoDetail     = &lazyRegexp{expr: `(?m)^\s+(\S+_test\.go:\d+): (.+)$`}
	reGoPackage    = &lazyRegexp{expr: `(?m)^FAIL\t(\S+)(?:\t| \[)`}
	reGoBuildFail  = &lazyRegexp{expr: `\[build failed\]`}
	rePyFailed     = &lazyRegexp{expr: `(?m)^(?:FAILED|ERROR) (\S+)(?: - (.+))?$`}
	rePySummary    = &lazyRegexp{expr: `(\d+ failed(?:, \d+ passed)?(?:, \d+ errors?)?) in [\d.]+s`}
	rePyE          = &lazyRegexp{expr: `(?m)^E[ \t]+(\S.*)$`}
	rePyLoc        = &lazyRegexp{expr: `(?m)^(\S+\.py):(\d+): \w+`}
	reEslintFile   = &lazyRegexp{expr: `^/?\S+\.\w+$`}
	reLeadSpace    = &lazyRegexp{expr: `^\s`}
	reEslintLine   = &lazyRegexp{expr: `^\s+(\d+):(\d+)\s+(error|warning)\s+(.+?)\s{2,}(\S+)$`}
	reEslintTotal  = &lazyRegexp{expr: `✖ (\d+ problems? \(.+?\))`}
	reTapNotOk     = &lazyRegexp{expr: `^\s*not ok \d+ - (.+?)(?:\s+#.*)?$`}
	reTapStop      = &lazyRegexp{expr: `^\s*(?:\.\.\.|ok \d+|not ok \d+|# Subtest)`}
	reTapError     = &lazyRegexp{expr: `^\s+error:`}
	reTapErrorLine = &lazyRegexp{expr: `^\s+error:\s*(?:\|-?)?\s*(.*)$`}
	reStartPipe    = &lazyRegexp{expr: `^\|`}
	reQuoteEnds    = &lazyRegexp{expr: `^['"]|['"]$`}
	reTapNoise     = &lazyRegexp{expr: `^[+-] (?:actual|expected)|^\^$`}
	reTapLocation  = &lazyRegexp{expr: `(?m)^\s+location:\s*'?(.+?)'?$`}
	reTapSpec      = &lazyRegexp{expr: `(?m)^\s*✖ (.+?)(?: \([\d.]+m?s\))?$`}
	reTapSpecSkip  = &lazyRegexp{expr: `(?i)^failing tests:?$|^\d+ problems? \(`}
	reTapFail      = &lazyRegexp{expr: `(?m)^# fail (\d+)$`}
	reTapTests     = &lazyRegexp{expr: `(?m)^# tests (\d+)$`}
	rePwHead       = &lazyRegexp{expr: `(?m)^\s+\d+\) \[([^\]]+)\] › (.+?) ─+$`}
	rePwHeadLine   = &lazyRegexp{expr: `^\s+\d+\) \[[^\]]+\] › `}
	rePwStop       = &lazyRegexp{expr: `^\s+\d+\) \[|^\s+\d+ failed$`}
	rePwError      = &lazyRegexp{expr: `^\s+Error: `}
	rePwDetail     = &lazyRegexp{expr: `^\s+(?:Expected|Received)(?: string)?:`}
	rePwLoc        = &lazyRegexp{expr: `(?m)^\s+at (\S+:\d+:\d+)$`}
	rePwFailed     = &lazyRegexp{expr: `(?m)^\s+(\d+) failed$`}
	rePwPassed     = &lazyRegexp{expr: `(?m)^\s+(\d+) passed`}
	reJestBullet   = &lazyRegexp{expr: `(?m)^\s*● (.+)$`}
	reJestLine     = &lazyRegexp{expr: `^\s*● `}
	reJestConsole  = &lazyRegexp{expr: `● Console`}
	reJestStop     = &lazyRegexp{expr: `^\s*● |^(?:PASS|FAIL) |^Test Suites:`}
	reJestSkip     = &lazyRegexp{expr: `^\d+ \||^>|^\|`}
	reJestDetail   = &lazyRegexp{expr: `^(?:Expected|Received):|^[-+] {2,}"`}
	reJestSuite    = &lazyRegexp{expr: `(?m)^FAIL (\S+)`}
	reJestLoc      = &lazyRegexp{expr: `at .*?\(?((?:[\w.\-~]+/)*[\w.\-]+\.[cm]?[jt]sx?:\d+:\d+)\)?`}
	reJestSummary  = &lazyRegexp{expr: `(?m)^Tests:\s+(.+)$`}
	reVitestFail   = &lazyRegexp{expr: `(?m)^ FAIL {1,2}(\S+ > .+)$`}
	reVitestCross  = &lazyRegexp{expr: `(?m)^\s+× (.+?)(?: \d+m?s)?$`}
	reVitestError  = &lazyRegexp{expr: `(?m)^(\w*Error: .+)$`}
	reVitestLoc    = &lazyRegexp{expr: `❯ (\S+:\d+:\d+)`}
	reVitestTests  = &lazyRegexp{expr: `(?m)^\s+Tests\s+(.+)$`}
	reScriptFail   = &lazyRegexp{expr: `(?m)^FAIL (\S+?): (.+)$`}
	reScriptTotal  = &lazyRegexp{expr: `(?m)^(\d+ passed, \d+ failed)$`}
	rePyTrace      = &lazyRegexp{expr: `(?m)^Traceback \(most recent call last\):`}
	rePyLast       = &lazyRegexp{expr: `(?m)^([\w.]*(?:Error|Exception|Exit|Interrupt)\w*): (.*)$`}
	rePyFrame      = &lazyRegexp{expr: `(?m)^\s+File "(.+?)", line (\d+)`}
	reLisp         = &lazyRegexp{expr: `(?m)^Unhandled (\S+)(?: in thread[^\n]*)?\n(?:\s+\{[^}]*\}>:\n)?\s*(.+)$`}
	reNodeError    = &lazyRegexp{expr: `(?m)^(\w*Error)(?: \[\w+\])?: (.+)$`}
	reNodeAt       = &lazyRegexp{expr: `(?m)^\s+at `}
	reNodeLoc      = &lazyRegexp{expr: `(?m)^\s+at (?:.*?\()?((?:file://)?\S+?:\d+:\d+)\)?$`}
	reNodeInternal = &lazyRegexp{expr: `node:`}
	reGenericLine  = &lazyRegexp{expr: `\b(?:\w*Error|ERR!|error|FAIL(?:ED)?|Exception|panic|fatal|cannot|not found|No such file|denied)\b`}
	reCommandExit  = &lazyRegexp{expr: `^Command exited with code`}
)

type evParser struct {
	format string
	// needs are literals of which at least one must occur for the parser's expressions to match: a runner's output
	// is tried against every parser in turn, and the expressions are the cost.
	needs []string
	parse func(text string) (parsedFields, bool)
}

func (p evParser) mayMatch(text string) bool {
	for _, n := range p.needs {
		if strings.Contains(text, n) {
			return true
		}
	}
	return false
}

var evParsers = []evParser{
	{"tsc", []string{"): error TS"}, func(text string) (parsedFields, bool) {
		ms := subs(reTscLine, text)
		if len(ms) == 0 {
			return parsedFields{}, false
		}
		errs := make([]string, 0, len(ms))
		for _, m := range ms {
			errs = append(errs, m[4]+" "+m[1]+": "+m[5])
		}
		s := ""
		if len(ms) != 1 {
			s = "s"
		}
		return parsedFields{errors: errs, location: ms[0][1] + ":" + ms[0][2] + ":" + ms[0][3], summary: strconv.Itoa(len(ms)) + " type error" + s}, true
	}},
	{"cargo", []string{"... FAILED", "--> "}, func(text string) (parsedFields, bool) {
		tests := col(subs(reCargoTest, text), 1)
		compile := subs(reCargoCompile, text)
		if len(tests) == 0 && len(compile) == 0 {
			return parsedFields{}, false
		}
		type panicAt struct{ at, msg string }
		var panics []panicAt
		for _, m := range subs(reCargoPanic, text) {
			var keep []string
			for _, line := range strings.Split(m[2], "\n") {
				if jsTrim(line) != "" && !reNotePrefix.MatchString(line) {
					keep = append(keep, line)
				}
			}
			panics = append(panics, panicAt{m[1], strings.Join(keep, " ")})
		}
		summary := firstGroup(reCargoSummary, text)
		var errs []string
		for _, m := range compile {
			errs = append(errs, m[1]+m[2])
		}
		for _, p := range panics {
			errs = append(errs, p.msg)
		}
		loc := ""
		if len(compile) > 0 {
			loc = compile[0][3]
		} else if len(panics) > 0 {
			loc = panics[0].at
		}
		if summary == "" && len(compile) > 0 {
			summary = "build failed"
		}
		return parsedFields{failing: tests, errors: errs, location: loc, summary: summary}, true
	}},
	{"go", []string{"--- FAIL:", ".go:"}, func(text string) (parsedFields, bool) {
		fails := col(subs(reGoFail, text), 1)
		build := subs(reGoBuild, text)
		if len(fails) == 0 && len(build) == 0 {
			return parsedFields{}, false
		}
		details := subs(reGoDetail, text)
		var packages []string
		seen := map[string]bool{}
		for _, m := range subs(reGoPackage, text) {
			if !seen[m[1]] {
				seen[m[1]] = true
				packages = append(packages, m[1])
			}
		}
		var errs []string
		if len(details) > 0 {
			errs = col(details, 2)
		} else {
			for _, m := range build {
				errs = append(errs, m[1]+": "+m[4])
			}
		}
		loc := ""
		if len(details) > 0 {
			loc = details[0][1]
		} else if len(build) > 0 {
			loc = build[0][1] + ":" + build[0][2]
		}
		summary := ""
		if len(packages) > 0 {
			summary = "FAIL " + strings.Join(packages, ", ")
			if reGoBuildFail.MatchString(text) {
				summary += " [build failed]"
			}
		}
		return parsedFields{failing: fails, errors: errs, location: loc, summary: summary}, true
	}},
	{"pytest", []string{"FAILED ", "ERROR ", " failed"}, func(text string) (parsedFields, bool) {
		failed := subs(rePyFailed, text)
		summary := firstGroup(rePySummary, text)
		if len(failed) == 0 && summary == "" {
			return parsedFields{}, false
		}
		// The short summary carries one message per failure; the `E` lines are the fallback when it is off (-rN).
		var messages []string
		for _, m := range failed {
			if m[2] != "" {
				messages = append(messages, m[2])
			}
		}
		eLines := col(subs(rePyE, text), 1)
		errs := messages
		if len(messages) == 0 {
			errs = eLines[:min(3, len(eLines))]
		}
		loc := ""
		if m := rePyLoc.FindStringSubmatch(text); m != nil {
			loc = m[1] + ":" + m[2]
		}
		return parsedFields{failing: col(failed, 1), errors: errs, location: loc, summary: summary}, true
	}},
	{"eslint", []string{"error"}, func(text string) (parsedFields, bool) {
		var errs []string
		file := ""
		for _, line := range strings.Split(text, "\n") {
			if reESlintFileMatch(line) {
				file = jsTrim(line)
			}
			m := reEslintLine.FindStringSubmatch(line)
			if m != nil && m[3] == "error" {
				where := m[1]
				if file != "" {
					parts := strings.Split(file, "/")
					where = strings.Join(parts[max(0, len(parts)-2):], "/") + ":" + m[1]
				}
				errs = append(errs, where+" "+m[5]+": "+m[4])
			}
		}
		if len(errs) == 0 {
			return parsedFields{}, false
		}
		return parsedFields{errors: errs, summary: firstGroup(reEslintTotal, text)}, true
	}},
	{"tap", []string{"not ok", "✖"}, func(text string) (parsedFields, bool) {
		lines := strings.Split(text, "\n")
		var failing, errs []string
		location := ""
		for i, line := range lines {
			m := reTapNotOk.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			failing = append(failing, m[1])
			block := evBlockAfter(lines, i, reTapStop)
			errorAt := -1
			for j, c := range block {
				if reTapError.MatchString(c) {
					errorAt = j
					break
				}
			}
			if errorAt >= 0 {
				inline := reTapErrorLine.FindStringSubmatch(block[errorAt])[1]
				var message string
				if inline != "" && !reStartPipe.MatchString(inline) {
					message = reQuoteEnds.ReplaceAllString(inline, "")
				} else {
					var keep []string
					for _, c := range block[errorAt+1:] {
						c = jsTrim(c)
						if c != "" && !reTapNoise.MatchString(c) {
							keep = append(keep, c)
						}
					}
					message = strings.Join(keep[:min(3, len(keep))], " ")
				}
				errs = append(errs, message)
			}
			if location == "" {
				location = firstGroup(reTapLocation, strings.Join(block, "\n"))
			}
		}
		var spec []string
		for _, m := range subs(reTapSpec, text) {
			if !reTapSpecSkip.MatchString(m[1]) {
				spec = append(spec, m[1])
			}
		}
		if len(failing) == 0 && len(spec) == 0 {
			return parsedFields{}, false
		}
		fail := firstGroup(reTapFail, text)
		summary := ""
		if fail != "" {
			tests := firstGroup(reTapTests, text)
			if tests == "" {
				tests = "undefined"
			}
			summary = fail + " of " + tests + " tests failed"
		}
		names := failing
		if len(failing) == 0 {
			names = spec
		}
		return parsedFields{failing: names, errors: errs, location: location, summary: summary}, true
	}},
	{"playwright", []string{"› "}, func(text string) (parsedFields, bool) {
		heads := subs(rePwHead, text)
		if len(heads) == 0 {
			return parsedFields{}, false
		}
		lines := strings.Split(text, "\n")
		var errs []string
		for i, line := range lines {
			if !rePwHeadLine.MatchString(line) {
				continue
			}
			block := evBlockAfter(lines, i, rePwStop)
			errLine := ""
			found := false
			for _, c := range block {
				if rePwError.MatchString(c) {
					errLine, found = c, true
					break
				}
			}
			var detail []string
			for _, c := range block {
				if rePwDetail.MatchString(c) {
					detail = append(detail, jsTrim(c))
				}
			}
			if found {
				errs = append(errs, strings.Join(append([]string{jsTrim(errLine)}, detail...), " "))
			}
		}
		failing := make([]string, 0, len(heads))
		for _, m := range heads {
			failing = append(failing, "["+m[1]+"] "+m[2])
		}
		summary := ""
		if failed := firstGroup(rePwFailed, text); failed != "" {
			summary = failed + " failed"
			if passed := firstGroup(rePwPassed, text); passed != "" {
				summary += ", " + passed + " passed"
			}
		}
		return parsedFields{failing: failing, errors: errs, location: firstGroup(rePwLoc, text), summary: summary}, true
	}},
	{"jest", []string{"● "}, func(text string) (parsedFields, bool) {
		var bullets []string
		for _, m := range subs(reJestBullet, text) {
			if name := jsTrim(m[1]); name != "Console" {
				bullets = append(bullets, name)
			}
		}
		if len(bullets) == 0 {
			return parsedFields{}, false
		}
		lines := strings.Split(text, "\n")
		var errs []string
		for i, line := range lines {
			if !reJestLine.MatchString(line) || reJestConsole.MatchString(line) {
				continue
			}
			var block []string
			for _, c := range evBlockAfter(lines, i, reJestStop) {
				if c = jsTrim(c); c != "" {
					block = append(block, c)
				}
			}
			firstLine, ok := "", false
			for _, c := range block {
				if !reJestSkip.MatchString(c) {
					firstLine, ok = c, true
					break
				}
			}
			var detail []string
			for _, c := range block {
				if reJestDetail.MatchString(c) {
					detail = append(detail, c)
				}
			}
			if len(detail) > 4 {
				detail = detail[:4]
			}
			if ok {
				errs = append(errs, strings.Join(append([]string{firstLine}, detail...), " "))
			}
		}
		suiteFail := col(subs(reJestSuite, text), 1)
		failing := make([]string, 0, len(bullets))
		for _, b := range bullets {
			if b == "Test suite failed to run" {
				suite := "suite"
				if len(suiteFail) > 0 {
					suite = suiteFail[0]
				}
				b = suite + " (suite failed to run)"
			}
			failing = append(failing, b)
		}
		return parsedFields{failing: failing, errors: errs, location: firstGroup(reJestLoc, text), summary: firstGroup(reJestSummary, text)}, true
	}},
	{"vitest", []string{" FAIL ", "× "}, func(text string) (parsedFields, bool) {
		fails := col(subs(reVitestFail, text), 1)
		crosses := col(subs(reVitestCross, text), 1)
		if len(fails) == 0 && len(crosses) == 0 {
			return parsedFields{}, false
		}
		names := fails
		if len(fails) == 0 {
			names = crosses
		}
		return parsedFields{failing: names, errors: col(subs(reVitestError, text), 1), location: firstGroup(reVitestLoc, text), summary: firstGroup(reVitestTests, text)}, true
	}},
	{"test-script", []string{"FAIL "}, func(text string) (parsedFields, bool) {
		fails := subs(reScriptFail, text)
		if len(fails) == 0 {
			return parsedFields{}, false
		}
		return parsedFields{failing: col(fails, 1), errors: col(fails, 2), summary: firstGroup(reScriptTotal, text)}, true
	}},
	{"python-traceback", []string{"Traceback (most recent call last):"}, func(text string) (parsedFields, bool) {
		if !rePyTrace.MatchString(text) {
			return parsedFields{}, false
		}
		var errs []string
		if lasts := subs(rePyLast, text); len(lasts) > 0 {
			l := lasts[len(lasts)-1]
			errs = []string{l[1] + ": " + l[2]}
		}
		loc := ""
		if frames := subs(rePyFrame, text); len(frames) > 0 {
			f := frames[len(frames)-1]
			loc = f[1] + ":" + f[2]
		}
		return parsedFields{errors: errs, location: loc}, true
	}},
	{"lisp-condition", []string{"Unhandled "}, func(text string) (parsedFields, bool) {
		m := reLisp.FindStringSubmatch(text)
		if m == nil {
			return parsedFields{}, false
		}
		return parsedFields{errors: []string{m[1] + ": " + m[2]}}, true
	}},
	{"node-error", []string{"Error"}, func(text string) (parsedFields, bool) {
		m := reNodeError.FindStringSubmatch(text)
		if m == nil || !reNodeAt.MatchString(text) {
			return parsedFields{}, false
		}
		var kept []string
		for _, line := range strings.Split(text, "\n") {
			if !reNodeInternal.MatchString(line) {
				kept = append(kept, line)
			}
		}
		return parsedFields{errors: []string{m[1] + ": " + m[2]}, location: firstGroup(reNodeLoc, strings.Join(kept, "\n"))}, true
	}},
}

func reESlintFileMatch(line string) bool {
	return reEslintFile.MatchString(jsTrim(line)) && !reLeadSpace.MatchString(line)
}

// ParseFailure parses a failed or check output into failing tests, error lines, a location, a summary and an
// exit code.
func ParseFailure(text string) ParsedFailure {
	t := strings.ReplaceAll(text, "\r", "")
	exit := evExitCode(t)
	for _, p := range evParsers {
		if !p.mayMatch(t) {
			continue
		}
		if found, ok := p.parse(t); ok {
			return evResult(p.format, found, exit)
		}
	}
	for _, c := range strings.Split(t, "\n") {
		c = jsTrim(c)
		if reGenericLine.MatchString(c) && !reCommandExit.MatchString(c) {
			return evResult("generic", parsedFields{errors: []string{c}}, exit)
		}
	}
	return evResult("unparsed", parsedFields{}, exit)
}

var failureNoise = []struct {
	re   *lazyRegexp
	repl string
	// digits: the expression needs a digit to match, so a text without one skips it.
	digits bool
}{
	{&lazyRegexp{expr: `(?:/private)?/(?:tmp|var/folders)/[^\s'"):]+`}, "<tmp>", false},
	{&lazyRegexp{expr: `\bpytest-\d+\b`}, "pytest-#", true},
	{&lazyRegexp{expr: `\.tmp[0-9a-zA-Z]+`}, ".tmp#", false},
	{&lazyRegexp{expr: `\b\d{4}[-/]\d{2}[-/]\d{2}[T ]\d{2}:\d{2}(?::\d{2}(?:[.,]\d+)?)?Z?`}, "<time>", true},
	{&lazyRegexp{expr: `\b\d{2}:\d{2}:\d{2}\b`}, "<time>", true},
	{&lazyRegexp{expr: `\brun=[0-9a-f]{6,}\b`}, "run=#", false},
	{&lazyRegexp{expr: `\b\d+(?:\.\d+)?\s*(?:ms|s|m|h|µs|us|ns)\b`}, "#t", true},
	{&lazyRegexp{expr: `0x[0-9a-fA-F]+`}, "0x#", true},
	{&lazyRegexp{expr: `\(\d+\)`}, "(#)", true},
	{&lazyRegexp{expr: `\d{5,}`}, "#", true},
	{&lazyRegexp{expr: `:\d+(?::\d+)?\b`}, ":#", true},
	{&lazyRegexp{expr: `\(\d+,\d+\)`}, "(#)", true},
}

func hasDigit(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			return true
		}
	}
	return false
}

// NormaliseFailureText normalises what changes between identical runs: durations, clock times and dates,
// addresses, long numbers, thread ids, temp paths, and line:column positions. Other digits stay, so "expected 3"
// and "expected 4" still differ.
func NormaliseFailureText(value string) string {
	digits := hasDigit(value)
	for _, n := range failureNoise {
		if n.digits && !digits {
			continue
		}
		value = n.re.ReplaceAllString(value, n.repl)
		if n.repl == "<tmp>" || n.repl == ".tmp#" {
			// A replacement can add digits, never remove the need: recompute only when it matters.
			digits = hasDigit(value)
		}
	}
	return jsTrim(reJSSpace.ReplaceAllString(value, " "))
}

// marshalPlain is JSON.stringify: no HTML escaping.
func marshalPlain(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return strings.TrimSuffix(buf.String(), "\n")
}

// compareUTF16 orders strings by UTF-16 code unit, as JavaScript's default sort does.
func compareUTF16(a, b string) int {
	x, y := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := 0; i < len(x) && i < len(y); i++ {
		if x[i] != y[i] {
			if x[i] < y[i] {
				return -1
			}
			return 1
		}
	}
	return len(x) - len(y)
}

func sortedNormalised(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, NormaliseFailureText(v))
	}
	sort.SliceStable(out, func(i, j int) bool { return compareUTF16(out[i], out[j]) < 0 })
	return out
}

// FailureSignature is the order-free signature of a failure: which tests failed and with which errors, both
// normalised. Pass counts are left out, so a run narrowed by a flag (`-x`, a test filter) still matches the full
// run that failed the same way.
func FailureSignature(parsed ParsedFailure, raw string) string {
	if parsed.Format == "unparsed" {
		tail := raw
		if n := utf16Len(raw); n > 400 {
			tail = utf16Slice(raw, n-400, -1)
		}
		return marshalPlain([]string{"unparsed", NormaliseFailureText(tail)})
	}
	return marshalPlain([][]string{sortedNormalised(parsed.Failing), sortedNormalised(parsed.Errors)})
}

// jsToString is String(value) for the JSON values an edit input holds.
func jsToString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	case json.Number:
		return x.String()
	}
	return fmt.Sprint(v)
}

// EditPairs are the edit pairs of an `edit` input, as [oldText, newText]: the `edits` array, or one top-level pair.
func EditPairs(input map[string]any) [][2]string {
	if edits, ok := input["edits"]; ok {
		if list, ok := edits.([]any); ok {
			out := make([][2]string, 0, len(list))
			for _, e := range list {
				pair, _ := e.(map[string]any)
				out = append(out, [2]string{jsToString(pair["oldText"]), jsToString(pair["newText"])})
			}
			return out
		}
		if list, ok := edits.([]map[string]any); ok {
			out := make([][2]string, 0, len(list))
			for _, pair := range list {
				out = append(out, [2]string{jsToString(pair["oldText"]), jsToString(pair["newText"])})
			}
			return out
		}
	}
	_, hasOld := input["oldText"]
	_, hasNew := input["newText"]
	if hasOld || hasNew {
		return [][2]string{{jsToString(input["oldText"]), jsToString(input["newText"])}}
	}
	return nil
}

// LineDelta is the removed and added lines of one edit, as a multiset difference, so unchanged context lines
// drop out.
func LineDelta(oldText, newText string) (removed, added []string) {
	count := map[string]int{}
	var order []string
	for _, line := range strings.Split(oldText, "\n") {
		if _, ok := count[line]; !ok {
			order = append(order, line)
		}
		count[line]++
	}
	for _, line := range strings.Split(newText, "\n") {
		if count[line] > 0 {
			count[line]--
		} else {
			added = append(added, line)
		}
	}
	for _, line := range order {
		for range count[line] {
			removed = append(removed, line)
		}
	}
	return removed, added
}

// EditDiff is what an `edit` or `write` changed: the path, the line counts, and a diff capped at 600 characters.
type EditDiff struct {
	Path         string
	Added        int
	Removed      int
	RemovedLines []string
	AddedLines   []string
	Diff         string
}

func capDiff(text string) string {
	if utf16Len(text) > evidenceDiffCap {
		return utf16Slice(text, 0, evidenceDiffCap) + "… [diff capped]"
	}
	return text
}

// MakeEditDiff is the change an `edit` or `write` made. Every string is as the agent wrote it: the caller
// redacts what leaves the machine.
func MakeEditDiff(tool string, input map[string]any) EditDiff {
	path := "?"
	if v, ok := input["path"]; ok && v != nil {
		path = jsToString(v)
	}
	if tool == "write" {
		lines := strings.Split(jsToString(input["content"]), "\n")
		plus := make([]string, len(lines))
		for i, l := range lines {
			plus[i] = "+" + l
		}
		return EditDiff{Path: path, Added: len(lines), AddedLines: lines, RemovedLines: []string{}, Diff: capDiff(strings.Join(plus, "\n"))}
	}
	removedLines, addedLines := []string{}, []string{}
	for _, p := range EditPairs(input) {
		r, a := LineDelta(p[0], p[1])
		removedLines = append(removedLines, r...)
		addedLines = append(addedLines, a...)
	}
	var parts []string
	for _, l := range removedLines {
		parts = append(parts, "-"+l)
	}
	for _, l := range addedLines {
		parts = append(parts, "+"+l)
	}
	return EditDiff{Path: path, Added: len(addedLines), Removed: len(removedLines), RemovedLines: removedLines, AddedLines: addedLines, Diff: capDiff(strings.Join(parts, "\n"))}
}

// nullableInt is a JSON number or null, omitted while unset: `same_failure_as_run` is absent on a passing run.
type nullableInt struct {
	set bool
	n   int
}

func (v nullableInt) IsZero() bool { return !v.set }
func (v nullableInt) MarshalJSON() ([]byte, error) {
	if v.n == 0 {
		return []byte("null"), nil
	}
	return []byte(strconv.Itoa(v.n)), nil
}

// EvidenceRun is one run in the evidence: a failed call, or a call that ran a test, build or lint command and passed.
// The JSON field order is upstream's.
type EvidenceRun struct {
	N                int         `json:"n"`
	Outcome          string      `json:"outcome"`
	Parser           string      `json:"parser,omitempty"`
	FailingTests     []string    `json:"failing_tests,omitempty"`
	FailingTestsMore int         `json:"failing_tests_more,omitempty"`
	Errors           []string    `json:"errors,omitempty"`
	Location         string      `json:"location,omitempty"`
	Summary          string      `json:"summary,omitempty"`
	OutputTail       string      `json:"output_tail,omitempty"`
	OutputHeadTail   string      `json:"output_head_tail,omitempty"`
	SameFailureAsRun nullableInt `json:"same_failure_as_run,omitzero"`
	ExitCode         *int        `json:"exit_code,omitempty"`
}

// EvidenceEdit is one `edit`/`write` call in the evidence.
type EvidenceEdit struct {
	N            int    `json:"n"`
	Path         string `json:"path"`
	AddedLines   int    `json:"added_lines"`
	RemovedLines int    `json:"removed_lines"`
	Diff         string `json:"diff"`
}

// EvidenceDigest is the counts a judge can weigh without reading every run.
type EvidenceDigest struct {
	FailedRuns         int   `json:"failed_runs"`
	DistinctFailures   int   `json:"distinct_failures"`
	SameCommandRuns    []int `json:"same_command_runs"`
	LatestFailureSeen  bool  `json:"latest_failure_seen_before"`
	EditsBetweenFailed int   `json:"edits_between_failed_runs"`
	InfoCallsBetween   int   `json:"information_calls_between_failed_runs"`
}

// StuckEvidence is the evidence for one stuck window.
type StuckEvidence struct {
	Runs   []EvidenceRun  `json:"runs"`
	Edits  []EvidenceEdit `json:"edits"`
	Digest EvidenceDigest `json:"digest"`
}

func failureView(output string, parsed ParsedFailure) EvidenceRun {
	if parsed.Format == "unparsed" {
		return EvidenceRun{OutputHeadTail: Redact(evHeadTail(jsTrim(output), evidenceFallbackHead, evidenceFallbackTail))}
	}
	v := EvidenceRun{Parser: parsed.Format}
	if len(parsed.Failing) > 0 {
		v.FailingTests = redactAll(parsed.Failing)
	}
	v.FailingTestsMore = parsed.FailingMore
	if len(parsed.Errors) > 0 {
		v.Errors = redactAll(parsed.Errors)
	}
	if parsed.Location != "" {
		v.Location = Redact(parsed.Location)
	}
	if parsed.Summary != "" {
		v.Summary = Redact(parsed.Summary)
	}
	if parsed.Format == "generic" {
		v.OutputTail = Redact(evTailOf(jsTrim(output), evidenceFallbackTail))
	}
	return v
}

func redactAll(values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = Redact(v)
	}
	return out
}

type pendingRun struct {
	view          EvidenceRun
	signature     string
	failed        bool
	repeatCommand bool
}

func digestOf(runs []pendingRun, edits []EvidenceEdit, informationCalls []int) EvidenceDigest {
	type failure struct {
		n   int
		sig string
	}
	var failures []failure
	for _, r := range runs {
		if r.failed {
			failures = append(failures, failure{r.view.N, r.signature})
		}
	}
	firstN, lastN, latest := 0, 0, ""
	if len(failures) > 0 {
		firstN, lastN, latest = failures[0].n, failures[len(failures)-1].n, failures[len(failures)-1].sig
	}
	distinct := map[string]bool{}
	for _, f := range failures {
		distinct[f.sig] = true
	}
	d := EvidenceDigest{FailedRuns: len(failures), DistinctFailures: len(distinct), SameCommandRuns: []int{}}
	for _, r := range runs {
		if r.repeatCommand {
			d.SameCommandRuns = append(d.SameCommandRuns, r.view.N)
		}
	}
	if len(failures) > 1 {
		for _, f := range failures[:len(failures)-1] {
			if f.sig == latest {
				d.LatestFailureSeen = true
			}
		}
	}
	for _, e := range edits {
		if e.N > firstN && e.N < lastN {
			d.EditsBetweenFailed++
		}
	}
	if len(failures) > 0 {
		for _, n := range informationCalls {
			if n > firstN {
				d.InfoCallsBetween++
			}
		}
	}
	return d
}

// BuildStuckEvidence builds the evidence for one stuck window: a run per failed call and per passing check
// command, an edit per `edit`/`write`, and a digest. The oldest runs are dropped first when the whole object is
// over limit bytes, then the oldest edits; the digest is then recomputed over what is left, and a run that
// pointed at a dropped run points at nothing. The floor is one run plus the digest, far under the cap.
func BuildStuckEvidence(attempts []Attempt, limit int) StuckEvidence {
	var runs []pendingRun
	edits := []EvidenceEdit{}
	var informationCalls []int
	firstCommandRun := map[string]int{}
	for index, a := range attempts {
		n := index + 1
		if isEditTool(a.Tool) {
			if c := a.Change; c != nil {
				edits = append(edits, EvidenceEdit{N: n, Path: c.Path, AddedLines: c.Added, RemovedLines: c.Removed, Diff: c.Diff})
			}
			continue
		}
		if !a.Failed && !reRunner.MatchString(a.Call) {
			for _, r := range runs {
				if r.failed {
					informationCalls = append(informationCalls, n)
					break
				}
			}
			continue
		}
		command := NormaliseFailureText(a.Call)
		_, repeated := firstCommandRun[command]
		outcome := "ok"
		if a.Failed {
			outcome = "failed"
		}
		run := pendingRun{view: EvidenceRun{N: n, Outcome: outcome}, repeatCommand: repeated}
		if !repeated {
			firstCommandRun[command] = n
		}
		if a.Failed {
			parsed := ParseFailure(a.Text)
			sig := FailureSignature(parsed, a.Text)
			view := failureView(a.Text, parsed)
			view.N, view.Outcome = n, outcome
			view.SameFailureAsRun = nullableInt{set: true}
			for _, c := range runs {
				if c.failed && c.signature == sig {
					view.SameFailureAsRun = nullableInt{set: true, n: c.view.N}
					break
				}
			}
			view.ExitCode = parsed.Exit
			run.view, run.signature, run.failed = view, sig, true
		}
		runs = append(runs, run)
	}
	for {
		present := map[int]bool{}
		for _, r := range runs {
			present[r.view.N] = true
		}
		kept := StuckEvidence{Runs: make([]EvidenceRun, 0, len(runs)), Edits: edits, Digest: digestOf(runs, edits, informationCalls)}
		for _, r := range runs {
			v := r.view
			if v.SameFailureAsRun.set && v.SameFailureAsRun.n != 0 && !present[v.SameFailureAsRun.n] {
				v.SameFailureAsRun = nullableInt{set: true}
			}
			kept.Runs = append(kept.Runs, v)
		}
		if len(marshalPlain(kept)) <= limit || (len(runs) <= 1 && len(edits) <= 1) {
			return kept
		}
		if len(runs) > 1 {
			runs = runs[1:]
		} else {
			edits = edits[1:]
		}
	}
}
