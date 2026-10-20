package warden

import (
	"math"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Call-waste notes (src/waste.ts). Each tool call re-reads the whole conversation, so the number of calls drives
// the cost of a run. Four patterns of work that spends calls for nothing earn one advisory sentence, attached to the
// tool result that triggers it: the result already goes to the model, so the note costs no extra call. A note never
// blocks a call, never changes a hold, never spends the steer budget, and makes no model or TypeSafe request.

const (
	// WasteWindow is how many calls of recent history a detector may look at.
	WasteWindow   = 10
	minReads      = 3
	smallReadLine = 100
	// adjacentGap: a read that starts no more than this many lines past the furthest line already asked for is
	// adjacent to it.
	adjacentGap = 20
	statusLimit = 200
)

// WasteTip is the one-time system prompt tip.
const WasteTip = "Tool calls are expensive: each one re-reads the whole conversation. Read files in large ranges or whole, not a few lines at a time. Run a check once without a pipe and search its output rather than re-running it. Wait for slow work with one blocking command, not repeated sleeps."

// WithWasteTip appends the tip after the prompt it finds, never in place of it; appending the same text twice is
// idempotent.
func WithWasteTip(current, tip string) string {
	base := jsTrim(current)
	if strings.Contains(base, tip) {
		return base
	}
	if base != "" {
		return base + "\n\n" + tip
	}
	return tip
}

// WasteDetector names one detector.
type WasteDetector string

const (
	DetectSleep   WasteDetector = "sleep"
	DetectPaging  WasteDetector = "paging"
	DetectSearch  WasteDetector = "search"
	DetectRecheck WasteDetector = "recheck"
)

// WasteCall is what a detector needs from one finished tool call.
type WasteCall struct {
	Tool   string
	Input  map[string]any
	Cwd    string
	Failed bool
	Result string
}

// WasteNudge is the advisory note for one tool result.
type WasteNudge struct {
	Detector WasteDetector
	// Subject is the file, check, or wait count the note names, for the trace line.
	Subject string
	Text    string
}

// ShellWriteTargets returns the paths the shell-write scanner (shell-writes.ts, owned by the rules lane) finds in a
// command. The default finds none: the waste detectors then use only their own redirect, `tee`, `sed -i` and
// path-verb scan.
var ShellWriteTargets = func(command string) []string { return nil }

// ── shell text ───────────────────────────────────────────────────────────────

type shellSegment struct {
	text string
	// sep is the top-level operator that preceded this segment, "" for the first one.
	sep string
	// start is where the segment starts in the command text.
	start int
}

// matchingParen is the index of the `)` matching the `(` at open, skipping escaped text; the command length when
// there is no match.
func matchingParen(command string, open int) int {
	depth := 0
	for i := open; i < len(command); i++ {
		switch command[i] {
		case '\\':
			i++
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return len(command)
}

// shellSegments splits at top-level `|`, `&&`, `||`, `;`, `&`, and newlines; pipes inside quotes or `$( … )` stay put.
func shellSegments(command string) []shellSegment {
	var segs []shellSegment
	start := 0
	sep := ""
	var quote byte
	at := func(i int) byte {
		if i < len(command) {
			return command[i]
		}
		return 0
	}
	for i := 0; i < len(command); i++ {
		c := command[i]
		if quote != 0 {
			if c == '\\' && quote == '"' {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			continue
		}
		if c == '\\' {
			i++
			continue
		}
		if c == '`' {
			end := strings.IndexByte(command[min(i+1, len(command)):], '`')
			if end == -1 {
				i = len(command)
			} else {
				i = i + 1 + end
			}
			continue
		}
		if c == '$' && at(i+1) == '(' {
			i = matchingParen(command, i+1)
			continue
		}
		op := ""
		switch c {
		case '\n':
			op = "\n"
		case ';':
			op = ";"
		case '&':
			op = "&"
			if at(i+1) == '&' {
				op = "&&"
			}
		case '|':
			op = "|"
			if at(i+1) == '|' {
				op = "||"
			}
		}
		if op == "" {
			continue
		}
		segs = append(segs, shellSegment{text: command[start:min(i, len(command))], sep: sep, start: start})
		i += len(op) - 1
		start = i + 1
		sep = op
	}
	segs = append(segs, shellSegment{text: command[min(start, len(command)):], sep: sep, start: start})
	return segs
}

// shellWords are the words of one already-split segment, unquoted; no expansion.
func shellWords(text string) []string {
	var out []string
	var cur []byte
	var quote byte
	for i := 0; i < len(text); i++ {
		c := text[i]
		if quote != 0 {
			if c == '\\' && quote == '"' {
				i++
				if i < len(text) {
					cur = append(cur, text[i])
				}
				continue
			}
			if c == quote {
				quote = 0
				continue
			}
			cur = append(cur, c)
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			continue
		}
		if c == '\\' {
			i++
			if i < len(text) {
				cur = append(cur, text[i])
			}
			continue
		}
		if c == ' ' || c == '\t' || c == '\n' {
			if len(cur) > 0 {
				out = append(out, string(cur))
			}
			cur = nil
			continue
		}
		cur = append(cur, c)
	}
	if len(cur) > 0 {
		out = append(out, string(cur))
	}
	return out
}

var (
	reCdPrefix   = lazyRE(`^\s*cd\s+(?:'[^']*'|"[^"]*"|[^\s;&|]+)\s*&&\s*`)
	reCdHead     = lazyRE(`^\s*cd\s+`)
	reAndTail    = lazyRE(`\s*&&\s*$`)
	reEdgeQuotes = lazyRE(`^['"]|['"]$`)
)

// stripCd is the command with a leading `cd DIR &&` removed, and DIR when there was one.
func stripCd(command string) (rest, dir string, hasDir bool) {
	loc := reCdPrefix.FindStringIndex(command)
	if loc == nil {
		return command, "", false
	}
	d := reCdHead.ReplaceAllString(command[loc[0]:loc[1]], "")
	d = reAndTail.ReplaceAllString(d, "")
	d = reEdgeQuotes.ReplaceAllString(jsTrim(d), "")
	return command[loc[1]:], d, true
}

func isAbsPath(p string) bool { return strings.HasPrefix(p, "/") || filepath.IsAbs(p) }

// wasteAbsolute resolves p the way a shell would from cwd, after an optional `cd dir`.
func wasteAbsolute(cwd, dir string, hasDir bool, p string) string {
	if isAbsPath(p) {
		return p
	}
	cur := filepath.ToSlash(cwd)
	if hasDir {
		if isAbsPath(dir) {
			cur = filepath.ToSlash(dir)
		} else {
			cur = path.Join(cur, filepath.ToSlash(dir))
		}
	}
	return path.Join(cur, filepath.ToSlash(p))
}

// resolveTarget is a path carried by a call, resolved from the call's working directory.
func resolveTarget(c WasteCall, dir string, hasDir bool, raw string) string {
	if raw == "~" || strings.HasPrefix(raw, "~/") {
		raw = "." + raw[1:]
	}
	return wasteAbsolute(c.Cwd, dir, hasDir, raw)
}

// ── writes ───────────────────────────────────────────────────────────────────

var (
	writeTools  = setOf("write", "edit", "edit_lines", "apply_patch", "multiedit")
	pathWriters = setOf("cp", "mv", "rm", "ln", "install", "touch", "truncate", "tee", "patch", "rsync")
	reRedirect  = lazyRE(`(?:^|\s)\d*>>?\s*(?:'([^']*)'|"([^"]*)"|([^\s;&|]+))`)
)

// bashWriteTargets are the paths written from one bash segment: redirect targets, `tee`, and the path-writing verbs.
func bashWriteTargets(segment string) []string {
	var out []string
	for _, m := range reRedirect.FindAllStringSubmatch(segment, -1) {
		for _, g := range m[1:] {
			if g != "" {
				out = append(out, g)
				break
			}
		}
	}
	toks := shellWords(segment)
	if len(toks) == 0 {
		return out
	}
	head := toks[0]
	if head == "sed" || head == "gsed" {
		inPlace := false
		for _, t := range toks {
			if t == "-i" || strings.HasPrefix(t, "--in-place") {
				inPlace = true
			}
		}
		if inPlace {
			if last := toks[len(toks)-1]; !strings.HasPrefix(last, "-") {
				out = append(out, last)
			}
			return out
		}
	}
	if !pathWriters[head] {
		return out
	}
	for _, t := range toks[1:] {
		if !strings.HasPrefix(t, "-") {
			out = append(out, t)
		}
	}
	return out
}

// WrittenPaths are the paths this call writes, resolved; empty when it writes nothing.
func WrittenPaths(c WasteCall) []string {
	if writeTools[c.Tool] {
		p, ok := c.Input["path"].(string)
		if !ok {
			p, ok = c.Input["file_path"].(string)
		}
		if ok && p != "" {
			return []string{wasteAbsolute(c.Cwd, "", false, p)}
		}
		return nil
	}
	if _, ok := c.Input["command"].(string); c.Tool == "bash" && !ok {
		return nil
	}
	view, ok := CommandOf(c.Tool, c.Input)
	if !ok || !view.Shell {
		return nil
	}
	command, dir, hasDir := stripCd(view.Command)
	var out []string
	for _, w := range ShellWriteTargets(command) {
		out = append(out, resolveTarget(c, dir, hasDir, w))
	}
	for _, seg := range shellSegments(command) {
		for _, t := range bashWriteTargets(seg.text) {
			out = append(out, resolveTarget(c, dir, hasDir, t))
		}
	}
	var uniq []string
	for _, p := range out {
		if !slices.Contains(uniq, p) {
			uniq = append(uniq, p)
		}
	}
	return uniq
}

// ── D1 sleep polling ─────────────────────────────────────────────────────────

var (
	reSleepOnly = lazyRE(`^sleep\s+(\d+(?:\.\d+)?)$`)
	reSleepThen = lazyRE(`^sleep\s+(\d+(?:\.\d+)?)\s*(?:;|&&)\s*([\s\S]+)$`)
	reLoopWord  = lazyRE(`(?:^|[\s;&|()])(?:while|until|for)\b`)
)

// IsSleepPoll: `sleep N`, optionally after `cd DIR &&`, optionally followed by `;`/`&&` and one short status
// command, which may be piped (`sleep 5; curl -s URL | jq .x`). A loop that sleeps, and a follow-up of two or more
// commands, wait for something real and are not polls.
func IsSleepPoll(command string) bool {
	rest, _, _ := stripCd(command)
	text := jsTrim(rest)
	if text == "" || reLoopWord.MatchString(text) {
		return false
	}
	if reSleepOnly.MatchString(text) {
		return true
	}
	m := reSleepThen.FindStringSubmatch(text)
	if m == nil {
		return false
	}
	status := jsTrim(m[2])
	if status == "" || utf16Len(status) > statusLimit {
		return false
	}
	for _, s := range shellSegments(status) {
		if s.sep != "" && s.sep != "|" {
			return false
		}
	}
	return true
}

func shellCommandOf(c WasteCall) (CommandView, bool) {
	v, ok := CommandOf(c.Tool, c.Input)
	return v, ok && v.Shell
}

// SleepNudge fires on the 2nd poll of the last WasteWindow calls.
func SleepNudge(window []WasteCall) *WasteNudge {
	if len(window) == 0 {
		return nil
	}
	cur, ok := shellCommandOf(window[len(window)-1])
	if !ok || !IsSleepPoll(cur.Command) {
		return nil
	}
	polls := 0
	for _, c := range window {
		if v, ok := shellCommandOf(c); ok && IsSleepPoll(v.Command) {
			polls++
		}
	}
	if polls < 2 {
		return nil
	}
	n := strconv.Itoa(len(window))
	return &WasteNudge{
		Detector: DetectSleep,
		Subject:  strconv.Itoa(polls) + " waits in " + n + " calls",
		Text:     "You have waited " + strconv.Itoa(polls) + " times in the last " + n + " calls. Wait once instead: one sleep long enough, or a blocking command such as `gh pr checks --watch`.",
	}
}

// ── D2-A paging ──────────────────────────────────────────────────────────────

// RangeRead is one range read of a file.
type RangeRead struct {
	File string
	// Path is the path as the agent wrote it, for the note.
	Path string
	// HasLines is false for a read whose start is unknowable (`tail -n`).
	HasLines   bool
	Start, End int
}

var (
	reSedRange = lazyRE(`^(\d+),(\d+)p$`)
	reAwkRange = lazyRE(`NR\s*>=\s*(\d+)\s*&&\s*NR\s*<=\s*(\d+)`)
	reHeredoc  = lazyRE(`<<`)
	reDashNum  = lazyRE(`^-\d+$`)
	reLinesEq  = lazyRE(`^--lines=\d+$`)
)

func lineCount(text string) int {
	if text == "" {
		return 0
	}
	lines := 1 + strings.Count(text, "\n")
	if strings.HasSuffix(text, "\n") {
		return lines - 1
	}
	return lines
}

// fileWord is the last word that is not a flag: the file in `sed -n '1,5p' FILE`, `head -n 20 FILE`.
func fileWord(rest []string) string {
	for i := len(rest) - 1; i >= 0; i-- {
		if strings.HasPrefix(rest[i], "-") {
			continue
		}
		return rest[i]
	}
	return ""
}

func numberInput(input map[string]any, key string) (int, bool) {
	switch v := input[key].(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	case int64:
		return int(v), true
	}
	return 0, false
}

// RangeReadOf is a range read of one file: a `read` with offset or limit, `sed -n 'a,bp'`, `head`/`tail -n N`, or
// `awk 'NR>=a && NR<=b'`.
func RangeReadOf(c WasteCall) (RangeRead, bool) {
	if c.Tool == "read" {
		p, ok := c.Input["path"].(string)
		if !ok || p == "" {
			return RangeRead{}, false
		}
		offset, hasOffset := numberInput(c.Input, "offset")
		limit, hasLimit := numberInput(c.Input, "limit")
		if !hasOffset && !hasLimit {
			return RangeRead{}, false
		}
		start := 1
		if hasOffset {
			start = offset
		}
		lines := lineCount(c.Result)
		if hasLimit {
			lines = limit
		}
		if lines < 1 || lines > smallReadLine {
			return RangeRead{}, false
		}
		return RangeRead{File: wasteAbsolute(c.Cwd, "", false, p), Path: p, HasLines: true, Start: start, End: start + lines - 1}, true
	}
	view, ok := shellCommandOf(c)
	// A program's own reads (`python -c`, a heredoc, an interpreter) are not reads of the agent; only a lone shell
	// read command is.
	if !ok || reHeredoc.MatchString(view.Command) {
		return RangeRead{}, false
	}
	command, dir, hasDir := stripCd(view.Command)
	segs := shellSegments(command)
	if len(segs) != 1 {
		return RangeRead{}, false
	}
	toks := shellWords(segs[0].text)
	if len(toks) == 0 {
		return RangeRead{}, false
	}
	head, rest := toks[0], toks[1:]
	file := fileWord(rest)
	if file == "" {
		return RangeRead{}, false
	}
	res := RangeRead{File: resolveTarget(c, dir, hasDir, file), Path: file}
	switch head {
	case "sed", "gsed":
		quiet := slices.Contains(rest, "-n")
		for _, t := range rest {
			if strings.HasPrefix(t, "--quiet") || strings.HasPrefix(t, "--silent") {
				quiet = true
			}
		}
		if !quiet {
			return RangeRead{}, false
		}
		for _, t := range rest {
			if m := reSedRange.FindStringSubmatch(t); m != nil {
				res.Start, _ = strconv.Atoi(m[1])
				res.End, _ = strconv.Atoi(m[2])
				res.HasLines = true
				return res, true
			}
		}
		return RangeRead{}, false
	case "head", "tail":
		count := math.NaN()
		for i, t := range rest {
			if t == "-n" {
				count = math.NaN()
				if i+1 < len(rest) {
					if v, err := strconv.ParseFloat(rest[i+1], 64); err == nil {
						count = v
					}
				}
				break
			}
			if reDashNum.MatchString(t) {
				count, _ = strconv.ParseFloat(t[1:], 64)
				break
			}
			if reLinesEq.MatchString(t) {
				count, _ = strconv.ParseFloat(t[8:], 64)
				break
			}
		}
		if math.IsNaN(count) || count < 1 || count > smallReadLine {
			return RangeRead{}, false
		}
		if head == "head" {
			res.HasLines, res.Start, res.End = true, 1, int(count)
		}
		return res, true
	case "awk", "gawk":
		for _, t := range rest {
			if m := reAwkRange.FindStringSubmatch(t); m != nil {
				res.Start, _ = strconv.Atoi(m[1])
				res.End, _ = strconv.Atoi(m[2])
				res.HasLines = true
				return res, true
			}
		}
	}
	return RangeRead{}, false
}

// allAdjacent: every read starts at or before the furthest line already asked for, plus the gap. An unknown range
// is not adjacent.
func allAdjacent(reads []RangeRead) bool {
	for _, r := range reads {
		if !r.HasLines {
			return false
		}
	}
	sorted := slices.Clone(reads)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Start < sorted[j].Start })
	furthest := sorted[0].End
	for _, r := range sorted[1:] {
		if r.Start > furthest+adjacentGap {
			return false
		}
		furthest = max(furthest, r.End)
	}
	return true
}

func writesBetween(window []WasteCall, first, last int, file string) bool {
	if first+1 > last {
		return false
	}
	for _, c := range window[first+1 : last] {
		if slices.Contains(WrittenPaths(c), file) {
			return true
		}
	}
	return false
}

// PagingNudge fires on the 3rd adjacent range read of one file in the last WasteWindow calls with no write to it
// between.
func PagingNudge(window []WasteCall) *WasteNudge {
	if len(window) == 0 {
		return nil
	}
	trigger, ok := RangeReadOf(window[len(window)-1])
	if !ok {
		return nil
	}
	var reads []RangeRead
	first, last := -1, -1
	for i, c := range window {
		r, ok := RangeReadOf(c)
		if !ok || r.File != trigger.File {
			continue
		}
		if first == -1 {
			first = i
		}
		last = i
		reads = append(reads, r)
	}
	if len(reads) < minReads || !allAdjacent(reads) {
		return nil
	}
	if writesBetween(window, first, last, trigger.File) {
		return nil
	}
	lo, hi := reads[0].Start, reads[0].End
	for _, r := range reads {
		lo, hi = min(lo, r.Start), max(hi, r.End)
	}
	return &WasteNudge{
		Detector: DetectPaging,
		Subject:  trigger.Path,
		Text: "You read " + trigger.Path + " in " + strconv.Itoa(len(reads)) + " calls and the parts run from line " + strconv.Itoa(lo) + " to " + strconv.Itoa(hi) +
			" without a gap. One `read` with offset=" + strconv.Itoa(lo) + ", limit=" + strconv.Itoa(hi-lo+1) + " returns the same lines.",
	}
}

// ── D2-B repeated search ─────────────────────────────────────────────────────

// SearchRead is a `grep`/`rg` on one named file.
type SearchRead struct {
	File, Path, Pattern string
}

// valueFlags take the next word, so it is not read as the pattern or the file.
var (
	valueFlags = setOf("-e", "--regexp", "-f", "--file", "-m", "--max-count", "-A", "-B", "-C", "--include", "--exclude", "--label", "-t", "--type")
	reRecurse  = lazyRE(`[rR]`)
	reGlobChar = lazyRE(`[*?\[\]{}]`)
)

// SearchReadOf is a `grep`/`rg` on one named file: pattern, file, and no recursion and no glob.
func SearchReadOf(c WasteCall) (SearchRead, bool) {
	view, ok := shellCommandOf(c)
	if !ok {
		return SearchRead{}, false
	}
	command, dir, hasDir := stripCd(view.Command)
	segs := shellSegments(command)
	if len(segs) == 0 {
		return SearchRead{}, false
	}
	toks := shellWords(segs[0].text)
	if len(toks) == 0 || (toks[0] != "grep" && toks[0] != "rg") {
		return SearchRead{}, false
	}
	pattern, flagged := "", false
	var operands []string
	for i := 1; i < len(toks); i++ {
		t := toks[i]
		if t == "-e" || t == "--regexp" {
			i++
			flagged = true
			pattern = ""
			if i < len(toks) {
				pattern = toks[i]
			} else {
				flagged = false
			}
			continue
		}
		if valueFlags[t] {
			i++
			continue
		}
		if strings.HasPrefix(t, "-") {
			if reRecurse.MatchString(t) || t == "--recursive" || t == "--dereference-recursive" {
				return SearchRead{}, false
			}
			continue
		}
		operands = append(operands, t)
	}
	var file string
	if flagged {
		if len(operands) > 0 {
			file = operands[0]
		}
	} else {
		if len(operands) > 1 {
			file = operands[1]
		}
		if len(operands) > 0 {
			pattern = operands[0]
		}
	}
	want := 2
	if flagged {
		want = 1
	}
	if pattern == "" || file == "" || len(operands) != want {
		return SearchRead{}, false
	}
	if reGlobChar.MatchString(file) {
		return SearchRead{}, false
	}
	return SearchRead{File: resolveTarget(c, dir, hasDir, file), Path: file, Pattern: pattern}, true
}

// PatternsOverlap: identical patterns, one contained in the other, or a shared `-E` alternative.
func PatternsOverlap(left, right string) bool {
	one, two := jsTrim(left), jsTrim(right)
	if one == "" || two == "" {
		return false
	}
	if one == two || strings.Contains(one, two) || strings.Contains(two, one) {
		return true
	}
	alternatives := func(p string) map[string]bool {
		out := map[string]bool{}
		for _, part := range strings.Split(p, "|") {
			if part = jsTrim(part); part != "" {
				out[part] = true
			}
		}
		return out
	}
	seen := alternatives(two)
	for alt := range alternatives(one) {
		if seen[alt] {
			return true
		}
	}
	return false
}

// SearchNudge fires on the 3rd search of one file in the last WasteWindow calls, two of them overlapping, with no
// write between.
func SearchNudge(window []WasteCall) *WasteNudge {
	if len(window) == 0 {
		return nil
	}
	trigger, ok := SearchReadOf(window[len(window)-1])
	if !ok {
		return nil
	}
	type found struct {
		index  int
		search SearchRead
	}
	var searches []found
	for i, c := range window {
		if s, ok := SearchReadOf(c); ok && s.File == trigger.File {
			searches = append(searches, found{i, s})
		}
	}
	if len(searches) < minReads {
		return nil
	}
	overlapping := false
	for i, l := range searches {
		for _, r := range searches[i+1:] {
			if PatternsOverlap(l.search.Pattern, r.search.Pattern) {
				overlapping = true
			}
		}
	}
	if !overlapping {
		return nil
	}
	if writesBetween(window, searches[0].index, searches[len(searches)-1].index, trigger.File) {
		return nil
	}
	return &WasteNudge{
		Detector: DetectSearch,
		Subject:  trigger.Path,
		Text:     "You searched " + trigger.Path + " " + strconv.Itoa(len(searches)) + " times with the same or an overlapping pattern. The earlier hits are still in your results; reuse them, or widen the search once.",
	}
}

// ── D3-C check re-run with filters ───────────────────────────────────────────

var (
	reCheckHead   = lazyRE(`^(?:npm\s+(?:test|run\s+(?:check|test|lint|build|typecheck))|npx\s+tsc|tsc|eslint|vitest|jest|pytest|ruff|mypy|cargo\s+(?:test|check|build)|go\s+(?:test|build|vet)|make\s+(?:check|test|lint)|flutter\s+(?:test|analyze)|dart\s+analyze|(?:bun|pnpm|uv)\s+(?:run\s+)?(?:check|test|lint|build|typecheck|tsc|eslint|vitest|jest|pytest|ruff|mypy))(?:\s|$)`)
	reRedirectEnd = lazyRE(`\s*(?:\d?>&\d+|\d?>>?|&>>?|&>)\s*(?:'[^']*'|"[^"]*"|\S*)$`)
	reFilterHeads = lazyRE(`^(?:tail|head|grep|rg)\b`)
	reFailSignal  = lazyRE(`fail [1-9]|FAIL|error TS|[1-9][0-9]* failed|Error:`)
)

// CheckRun is one run of a check command.
type CheckRun struct {
	// Key is the runner and target, with everything after the first top-level pipe or redirect dropped.
	Key string
	// Filter is everything after the first top-level pipe; HasFilter is false for an unpiped run.
	Filter    string
	HasFilter bool
	// Cut is true when the command threw its own output away in a pipe (`| tail`, `| head`, `| grep`, `| rg`).
	Cut bool
}

// stripRedirects: trailing redirects carry no part of the check, so they are not part of its key.
func stripRedirects(text string) string {
	value := jsTrim(text)
	for range 4 {
		next := jsTrim(reRedirectEnd.ReplaceAllString(value, ""))
		if next == value {
			break
		}
		value = next
	}
	return value
}

// CheckRunOf parses a check command.
func CheckRunOf(c WasteCall) (CheckRun, bool) {
	view, ok := shellCommandOf(c)
	if !ok {
		return CheckRun{}, false
	}
	command, _, _ := stripCd(view.Command)
	segs := shellSegments(command)
	if len(segs) == 0 {
		return CheckRun{}, false
	}
	lead := stripRedirects(segs[0].text)
	if !reCheckHead.MatchString(lead) {
		return CheckRun{}, false
	}
	run := CheckRun{Key: reJSSpace.ReplaceAllString(lead, " ")}
	for _, s := range segs {
		if s.sep != "|" {
			continue
		}
		if !run.HasFilter {
			run.HasFilter = true
			run.Filter = jsTrim(command[s.start:])
		}
		if reFilterHeads.MatchString(jsTrim(s.text)) {
			run.Cut = true
		}
	}
	return run, true
}

// RecheckNudge fires when the same check runs again within WasteWindow calls and the two runs sliced the output
// differently: the earlier run was cut by a pipe, showed no failure, and nothing wrote a file in between. A run
// whose result already showed a failure is a failure to chase, not waste, and a re-run without a pipe has already
// taken the advice.
func RecheckNudge(window []WasteCall) *WasteNudge {
	if len(window) == 0 {
		return nil
	}
	trigger, ok := CheckRunOf(window[len(window)-1])
	if !ok || !trigger.HasFilter {
		return nil
	}
	earlierIndex := -1
	for i := len(window) - 2; i >= 0; i-- {
		if run, ok := CheckRunOf(window[i]); ok && run.Key == trigger.Key {
			earlierIndex = i
			break
		}
	}
	if earlierIndex < 0 {
		return nil
	}
	earlier := window[earlierIndex]
	earlierRun, _ := CheckRunOf(earlier)
	if !earlierRun.Cut || (earlierRun.HasFilter && earlierRun.Filter == trigger.Filter) {
		return nil
	}
	if earlier.Failed || reFailSignal.MatchString(earlier.Result) {
		return nil
	}
	for _, c := range window[earlierIndex+1 : len(window)-1] {
		if len(WrittenPaths(c)) > 0 {
			return nil
		}
	}
	runs := 0
	for _, c := range window {
		if r, ok := CheckRunOf(c); ok && r.Key == trigger.Key {
			runs++
		}
	}
	return &WasteNudge{
		Detector: DetectRecheck,
		Subject:  trigger.Key,
		Text:     "You ran " + trigger.Key + " " + strconv.Itoa(runs) + " times and filtered the output each time. Run it once without a pipe; the full output arrives, or is saved to a file you can search.",
	}
}

var wasteDetectors = []struct {
	name   WasteDetector
	enable func(WasteConfig) bool
	detect func([]WasteCall) *WasteNudge
}{
	{DetectSleep, func(c WasteConfig) bool { return c.Sleep }, SleepNudge},
	{DetectPaging, func(c WasteConfig) bool { return c.Paging }, PagingNudge},
	{DetectSearch, func(c WasteConfig) bool { return c.Search }, SearchNudge},
	{DetectRecheck, func(c WasteConfig) bool { return c.Recheck }, RecheckNudge},
}

// WasteTracker holds the recent tool calls of one session, with the rate limit: one note per detector per `every`
// calls. A detector sees the last WasteWindow calls including the one that just finished, and at most one note
// rides any single result.
type WasteTracker struct {
	calls     []WasteCall
	seen      int
	lastFired map[WasteDetector]int
}

// NewWasteTracker returns an empty tracker.
func NewWasteTracker() *WasteTracker { return &WasteTracker{lastFired: map[WasteDetector]int{}} }

// Record records a finished call and returns the note to attach to its result, if any.
func (t *WasteTracker) Record(c WasteCall, cfg WasteConfig) *WasteNudge {
	t.calls = append(t.calls, c)
	t.seen++
	if len(t.calls) > WasteWindow {
		t.calls = slices.Clone(t.calls[len(t.calls)-WasteWindow:])
	}
	if !cfg.Enabled {
		return nil
	}
	for _, d := range wasteDetectors {
		if !d.enable(cfg) {
			continue
		}
		nudge := d.detect(t.calls)
		if nudge == nil {
			continue
		}
		if fired, ok := t.lastFired[d.name]; ok && t.seen-fired < cfg.Every {
			continue
		}
		t.lastFired[d.name] = t.seen
		return nudge
	}
	return nil
}

// Reset starts a new session: the window and the rate limit are empty again.
func (t *WasteTracker) Reset() { t.calls, t.seen, t.lastFired = nil, 0, map[WasteDetector]int{} }
