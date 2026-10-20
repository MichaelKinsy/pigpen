package warden

import "strings"

// Format-aware excerpts (excerpt.ts). The judge selects which tool produced the output; code keeps the exact lines
// that matter for that format: failing tests with their assertion lines, compiler and linter errors with file:line,
// changed files with counts, package manager warnings, and the final summary. Nothing is paraphrased. A parser
// returns false when it does not recognise its markers, and the caller falls back to the generic
// head/diagnostic/tail excerpt.

// OutputFormat is the kind of tool output a parser reads.
type OutputFormat string

const (
	FormatVitestJest OutputFormat = "vitest_jest"
	FormatNodeTest   OutputFormat = "node_test"
	FormatTsc        OutputFormat = "tsc"
	FormatEslint     OutputFormat = "eslint"
	FormatPytest     OutputFormat = "pytest"
	FormatGitDiff    OutputFormat = "git_diff"
	FormatGitLog     OutputFormat = "git_log"
	FormatNpmInstall OutputFormat = "npm_install"
	FormatOther      OutputFormat = "other"
)

// formatOrder is the order of the format question's options (excerpt.ts `formatQuestion`).
var formatOrder = []OutputFormat{FormatVitestJest, FormatNodeTest, FormatTsc, FormatEslint, FormatPytest, FormatGitDiff, FormatGitLog, FormatNpmInstall, FormatOther}

// FormatQuestion is the question that names the format of an output.
func FormatQuestion() Question {
	q := Choice("Which kind of tool produced `output`? Judge from the markers in the sample; `tool` names the calling tool, not the program. Select other when unsure or when the output is source code, data, or prose.", map[string]string{
		"vitest_jest": "A Vitest or Jest run: ✓/✗/× or PASS/FAIL per test, a 'Test Files' or 'Tests:' summary.",
		"node_test":   "Node's built-in test runner: TAP lines 'ok'/'not ok' with '# tests', or the spec reporter with ✔/✖ and 'ℹ tests'.",
		"tsc":         "TypeScript compiler diagnostics: 'file(line,col): error TSnnnn:' lines, 'Found N errors'.",
		"eslint":      "ESLint report: a file path line followed by 'line:col  error|warning  message  rule', then '✖ N problems'.",
		"pytest":      "pytest: dots/F per test, '___ test_name ___' failure headers, 'E ' assertion lines, 'FAILED'/'PASSED' lines, '=== N passed/failed ===' summary.",
		"git_diff":    "A unified diff: 'diff --git', '---'/'+++', '@@' hunks with +/- lines.",
		"git_log":     "git log or git status output: 'commit <hash>' blocks or one-line hashes with subjects; 'modified:'/'new file:' status lines.",
		"npm_install": "npm, pnpm, or yarn install: 'added N packages', 'npm warn', 'deprecated', 'vulnerabilities', 'up to date'.",
		"other":       "Anything else: source code, data, documentation, a shell listing, a mixed or unknown log.",
	})
	for _, f := range formatOrder {
		q.CriteriaOrder = append(q.CriteriaOrder, string(f))
	}
	return q
}

// IsParserFormat reports whether f names one of the parsers (a key of the format question other than "other").
func IsParserFormat(f string) bool {
	if f == string(FormatOther) {
		return false
	}
	_, ok := formatParsers[OutputFormat(f)]
	return ok
}

const formatExcerptLimit = 6000

// capture: header lines start a block; the following non-empty lines up to `depth` are kept with it.
func capture(lines []string, header, keep *lazyRegexp, depth int, summary *lazyRegexp) ([]string, bool) {
	var out []string
	headers, remaining := 0, 0
	for _, line := range lines {
		if header.MatchString(line) {
			headers++
			remaining = depth
			out = append(out, line)
			continue
		}
		if summary.MatchString(line) {
			out = append(out, line)
			remaining = 0
			continue
		}
		if remaining > 0 && jsTrim(line) != "" {
			if keep.MatchString(line) {
				out = append(out, line)
			}
			remaining--
		}
	}
	return out, headers > 0 || len(out) > 0
}

func anyLine(lines []string, tests ...*lazyRegexp) bool {
	for _, line := range lines {
		for _, re := range tests {
			if re.MatchString(line) {
				return true
			}
		}
	}
	return false
}

var (
	reVitestSummary = jsRE(`^\s*(?:Test Files|Tests|Test Suites|Snapshots|Duration|Time|Start at|Ran all test suites)\b|^\s*(?:FAIL|PASS)\s{2,}\S`)
	reVitestHeader  = jsRE(`^\s*(?:FAIL\s|[×✗✕]\s|●\s|❯\s.*\s\d+\s*$|AssertionError\b)`)
	reVitestKeep    = jsRE(`(?i)expected|received|actual|Error|assert|❯|›|\bat\b.*:\d+:\d+|[+-]\s|^\s*\d+\|`)
	reVitestMark    = jsRE(`[✓✗×✔✖]`)
)

func vitestJest(lines []string) ([]string, bool) {
	out, ok := capture(lines, reVitestHeader, reVitestKeep, 8, reVitestSummary)
	if ok && anyLine(lines, reVitestSummary, reVitestMark) {
		return out, true
	}
	return nil, false
}

var (
	reNodeSummary = jsRE(`^(?:#|ℹ)\s*(?:tests|suites|pass|fail|cancelled|skipped|todo|duration_ms)\b`)
	reNodeTap     = jsRE(`^TAP version`)
	reNodeSpec    = jsRE(`^\s*[✔✖]\s`)
	reNodeHeader  = jsRE(`^\s*(?:not ok\b|✖\s)`)
	reNodeKeep    = jsRE(`(?i)error|expected|actual|Error|failureType|code|at\b.*:\d+:\d+|^\s*[+-]\s|^\s*\|`)
)

func nodeTest(lines []string) ([]string, bool) {
	if !anyLine(lines, reNodeSummary, reNodeTap, reNodeSpec) {
		return nil, false
	}
	out, _ := capture(lines, reNodeHeader, reNodeKeep, 10, reNodeSummary)
	return out, true
}

var (
	reTscHeader  = jsRE(`^(?:\S.*?)(?:\(\d+,\d+\)|:\d+:\d+)\s*[-:]\s*error TS\d+`)
	reTscFound   = jsRE(`^Found \d+ errors?`)
	reTscKeep    = jsRE(`^\s{2,}\S`)
	reTscSummary = jsRE(`^Found \d+ errors?|^error TS\d+`)
)

func tsc(lines []string) ([]string, bool) {
	if !anyLine(lines, reTscHeader, reTscFound) {
		return nil, false
	}
	return capture(lines, reTscHeader, reTscKeep, 3, reTscSummary)
}

var (
	reEslintProblem = jsRE(`^\s+\d+:\d+\s+(?:error|warning)\s+`)
	reEslintTotal   = jsRE(`^✖ \d+ problems?|^\d+ problems? \(`)
	reEslintFile    = jsRE(`\S+\.[cm]?[jt]sx?$|\.vue$|\.svelte$`)
	reEslintPath    = jsRE(`^\S.*\.(?:[cm]?[jt]sx?|vue|svelte|json|md)$`)
	reEslintKeep    = jsRE(`^✖ \d+ problems?|^\d+ problems? \(|potentially fixable`)
)

func eslint(lines []string) ([]string, bool) {
	if !anyLine(lines, reEslintProblem) || !anyLine(lines, reEslintTotal, reEslintFile) {
		return nil, false
	}
	out := []string{}
	file, hasFile := "", false
	for _, line := range lines {
		if reEslintPath.MatchString(line) {
			file, hasFile = line, true
			continue
		}
		if reEslintProblem.MatchString(line) {
			if hasFile {
				out = append(out, file)
				hasFile = false
			}
			out = append(out, line)
			continue
		}
		if reEslintKeep.MatchString(line) {
			out = append(out, line)
		}
	}
	return out, true
}

var (
	rePytestSummary = jsRE(`^=+ .*(?:passed|failed|error|skipped|no tests ran).* =+$|^=+ short test summary info =+$|^(?:FAILED|ERROR) \S+`)
	rePytestHeader  = jsRE(`^_{3,} .+ _{3,}$`)
	rePytestKeep    = jsRE(`(?i)^E\s|^>\s|:\d+: |Error|assert`)
)

func pytest(lines []string) ([]string, bool) {
	if !anyLine(lines, rePytestSummary, rePytestHeader) {
		return nil, false
	}
	out, _ := capture(lines, rePytestHeader, rePytestKeep, 12, rePytestSummary)
	return out, true
}

var (
	reDiffHeader = jsRE(`^diff --git a/(.+?) b/(.+)$`)
	reDiffNote   = jsRE(`^(?:new file mode|deleted file mode|rename (?:from|to)|Binary files)`)
)

func gitDiff(lines []string) ([]string, bool) {
	type fileStat struct {
		name           string
		added, removed int
		note           string
	}
	var files []*fileStat
	for _, line := range lines {
		if m := reDiffHeader.FindStringSubmatch(line); m != nil {
			files = append(files, &fileStat{name: m[2]})
			continue
		}
		if len(files) == 0 {
			continue
		}
		current := files[len(files)-1]
		switch {
		case reDiffNote.MatchString(line):
			if current.note != "" {
				current.note += "; "
			}
			current.note += line
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			current.added++
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			current.removed++
		}
	}
	if len(files) == 0 {
		return nil, false
	}
	added, removed := 0, 0
	out := make([]string, 0, len(files)+1)
	for _, f := range files {
		added += f.added
		removed += f.removed
		note := ""
		if f.note != "" {
			note = "  (" + f.note + ")"
		}
		out = append(out, f.name+"  +"+itoaInt(f.added)+" -"+itoaInt(f.removed)+note)
	}
	out = append(out, itoaInt(len(files))+" files changed, +"+itoaInt(added)+" -"+itoaInt(removed))
	return out, true
}

var (
	reLogCommit  = jsRE(`^commit ([0-9a-f]{7,40})\b`)
	reLogBody    = jsRE(`^\s{4}\S`)
	reLogOneLine = jsRE(`^[0-9a-f]{7,40} \S`)
	reLogStatus  = jsRE(`^\s*(?:modified|new file|deleted|renamed|both modified):|^\?\?\s|^[ MADRCU?!]{2}\s\S`)
	reLogBranch  = jsRE(`^(?:On branch|Your branch|Changes (?:to be committed|not staged)|Untracked files|nothing to commit)`)
)

func gitLog(lines []string) ([]string, bool) {
	var out []string
	pending := false
	for _, line := range lines {
		if m := reLogCommit.FindStringSubmatch(line); m != nil {
			out = append(out, utf16Slice(m[1], 0, 12))
			pending = true
			continue
		}
		if pending && reLogBody.MatchString(line) {
			out[len(out)-1] += "  " + jsTrim(line)
			pending = false
			continue
		}
		if reLogOneLine.MatchString(line) || reLogStatus.MatchString(line) || reLogBranch.MatchString(line) {
			out = append(out, line)
		}
	}
	return out, len(out) > 0
}

var (
	reNpmKeep = jsRE(`(?i)^(?:added|removed|changed|audited|found|up to date|up-to-date)\b|\bvulnerabilit(?:y|ies)\b|^npm (?:warn|WARN|error|ERR!|notice)\b|\bdeprecated\b|^(?:warning|error)\s|^\s*Progress: resolved|^Done in|^Packages: `)
	reNpmDone = jsRE(`(?i)^(?:added|removed|changed|audited|up to date|up-to-date|Done in|Packages:)`)
)

func npmInstall(lines []string) ([]string, bool) {
	var out []string
	for _, line := range lines {
		if reNpmKeep.MatchString(line) {
			out = append(out, line)
		}
	}
	if anyOf(out, reNpmDone) {
		return out, true
	}
	return nil, false
}

func anyOf(lines []string, re *lazyRegexp) bool {
	for _, line := range lines {
		if re.MatchString(line) {
			return true
		}
	}
	return false
}

var formatParsers = map[OutputFormat]func([]string) ([]string, bool){
	FormatVitestJest: vitestJest, FormatNodeTest: nodeTest, FormatTsc: tsc, FormatEslint: eslint,
	FormatPytest: pytest, FormatGitDiff: gitDiff, FormatGitLog: gitLog, FormatNpmInstall: npmInstall,
}

func clip400(line string) string {
	if utf16Len(line) > 400 {
		return utf16Slice(line, 0, 400) + "…"
	}
	return line
}

// FormatExcerpt is the exact lines selected by the parser for `format`, joined and capped, with the last non-empty
// lines as a tail so the final status is always present. False when the format is `other` or its markers are absent.
func FormatExcerpt(text string, format OutputFormat) (string, bool) {
	if format == FormatOther {
		return "", false
	}
	parser, ok := formatParsers[format]
	if !ok {
		return "", false
	}
	lines := strings.Split(text, "\n")
	selected, ok := parser(lines)
	if !ok {
		return "", false
	}
	var nonEmpty []string
	for _, line := range lines {
		if jsTrim(line) != "" {
			nonEmpty = append(nonEmpty, line)
		}
	}
	tail := nonEmpty
	if len(tail) > 3 {
		tail = tail[len(tail)-3:]
	}
	var body []string
	size, dropped := 0, 0
	for _, line := range selected {
		clipped := clip400(line)
		if size+utf16Len(clipped)+1 > formatExcerptLimit-400 {
			dropped++
			continue
		}
		body = append(body, clipped)
		size += utf16Len(clipped) + 1
	}
	omitted := ""
	if dropped > 0 {
		omitted = ", " + itoaInt(dropped) + " omitted for size"
	}
	parts := append([]string{"[" + string(format) + " excerpt: " + itoaInt(len(selected)) + " selected lines" + omitted + "]"}, body...)
	// The final status line is the one the agent reads first; add the tail only when the parser did not already keep it.
	// Diffs and logs have no status line; their last lines are hunk or body text.
	if format != FormatGitDiff && format != FormatGitLog && len(tail) > 0 {
		last := tail[len(tail)-1]
		found := false
		for _, b := range body {
			if b == last {
				found = true
				break
			}
		}
		if !found {
			parts = append(parts, "[tail]")
			for _, line := range tail {
				parts = append(parts, clip400(line))
			}
		}
	}
	return strings.Join(parts, "\n"), true
}
