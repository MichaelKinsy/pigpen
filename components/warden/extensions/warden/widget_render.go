package warden

import (
	"math"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

// The status stack and its colour vocabulary: the rest of widget.ts (LEVEL_COLOR, QUIET_COLORS, SEVERITY, carriesFlag,
// pickSentenceTemplate, renderSegment, parseVerdictLine, TOKEN_NAMES) and all of widget-render.ts (widgetLines). The
// guard-specific token builders live with the guards that own their verdict types.

// ThemeLike is the palette the status line and the trace panel share (widget.ts ThemeLike). The SDK's UITheme has the
// same two methods.
type ThemeLike interface {
	Fg(color, text string) string
	Bold(text string) string
}

// LevelColor maps a verdict word to its colour token.
var LevelColor = map[string]string{
	"allow": "success", "ok": "success", "warn": "warning", "unverified": "warning", "nudged": "warning", "confirm": "error", "deny": "error",
	"stuck": "error", "false claim": "error", "stopped": "error", "stopped, recovering": "error", "violation": "warning", "skipped": "muted",
	"wake": "warning", "silent": "muted", "appended silently": "muted", "possible credentials": "warning", "error": "error",
}

// QuietColors are the verdicts that need no eye: the guard ran and found nothing.
var QuietColors = map[string]bool{"success": true, "muted": true}

// ColorSeverity is how far a verdict is from the editor; a worse verdict sits closer to where the eye already is.
var ColorSeverity = map[string]int{"warning": 1, "error": 2}

var flagWords = []string{"typesafe error", "user approved", "off plan", "off task", "read-only"}

// reFinding: a `{slop}` or `{patterns}` segment names something a guard found; `slop: none` names the absence of one.
// Go has no lookahead, so the exclusion is checked by hand in findingSegment.
var reFindingPrefix = lazyRE(`^(slop|patterns): `)
var reNoneWord = lazyRE(`^none\b`)

func findingSegment(segment string) bool {
	loc := reFindingPrefix.FindStringIndex(segment)
	return loc != nil && !reNoneWord.MatchString(segment[loc[1]:])
}

// CarriesFlag: a quiet verdict never folds when its line names a finding or a caveat.
func CarriesFlag(body []string) bool {
	for _, segment := range body {
		if findingSegment(segment) {
			return true
		}
		for _, word := range flagWords {
			if strings.Contains(segment, word) {
				return true
			}
		}
	}
	return false
}

// TokenNames are the token names users can put in templates, for /warden status and the README.
var TokenNames = map[string][]string{
	"security": {"tool", "injection", "exfiltration", "status"},
	"context":  {"tool", "retention", "bytesSaved"},
	"runaway":  {"kind", "count", "chars", "signal", "block", "status", "time", "guard"},
	"rules":    {"tool", "path", "asked", "violations", "status", "source", "reasons", "model", "ms", "flags", "time", "guard"},
	"action":   {"tool", "level", "source", "irreversible", "offTask", "scope", "approved", "intent", "visible", "largeOutput", "plan", "slop", "slopStub", "slopComments", "slopDead", "slopHedging", "patterns", "reasons", "path", "model", "ms", "flags", "time", "guard"},
	"prose":    {"wordy", "cliches", "jargon", "status", "reasons", "model", "ms", "flags", "time", "guard"},
	"stuck":    {"failures", "sameStrategy", "approachChange", "progress", "status", "source", "reasons", "model", "ms", "flags", "time", "guard"},
	"done":     {"changes", "checks", "checksPassed", "claimsDone", "claimsVerified", "checksApply", "outcome", "status", "reasons", "model", "ms", "flags", "time", "guard"},
	"subagent": {"agent", "kind", "wake", "status", "time", "guard"},
}

// tokenNumber is JavaScript's Number() of a token, NaN when it is not a number.
func tokenNumber(s string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return math.NaN()
	}
	return f
}

// PickSentenceTemplate picks the best sentence template for a verdict. It falls back to the data-style template.
func PickSentenceTemplate(guard string, tokens Tokens) string {
	variants, ok := SentenceTemplates[guard]
	if !ok {
		return defaultTemplateFor(guard)
	}
	level, hasLevel := tokens["level"]
	if !hasLevel {
		level = tokens["status"]
	}
	has := func(key string) bool { v := tokens[key]; return v != "" && v != "none" && v != "0.00" }
	flag := func(word string) bool { return strings.Contains(tokens["flags"], word) }
	switch guard {
	case "action":
		if flag("user approved") {
			return variants["allow_approved"]
		}
		if flag("typesafe error") {
			return variants["allow_typesafe_error"]
		}
		if level == "allow" || level == "ok" {
			switch {
			case has("patterns"):
				return variants["allow_patterns"]
			case tokens["slop"] != "" && tokens["slop"] != "none":
				return variants["allow_slop"]
			case flag("off task"):
				return variants["allow_off_task"]
			case flag("off plan"):
				return variants["allow_intent_mismatch"]
			case flag("read-only"):
				return variants["allow_read_only"]
			case tokens["irreversible"] != "" && tokenNumber(tokens["irreversible"]) < 0.3:
				return variants["allow_safe"]
			case tokens["scope"] != "":
				return variants["allow_with_context"]
			}
			return variants["allow_default"]
		}
		// These reasons are emitted only when the configured irreversible threshold is met.
		irreversibleWarning := reIrreversibleWarning.MatchString(tokens["reasons"])
		irreversibleHold := reIrreversibleHold.MatchString(tokens["reasons"])
		if level == "warn" {
			switch {
			case irreversibleWarning:
				return variants["warn_irreversible"]
			case has("reasons"):
				return variants["warn_reason"]
			case has("patterns"):
				return variants["warn_patterns"]
			}
			return variants["warn_reason"]
		}
		if level == "confirm" || level == "deny" {
			switch {
			case level == "deny":
				return variants["hold_deny"]
			case irreversibleHold:
				return variants["hold_irreversible"]
			case has("reasons"):
				return variants["hold_reason"]
			case has("patterns"):
				return variants["hold_dangerous"]
			}
			return variants["hold_reason"]
		}
		return variants["default"]
	case "stuck":
		if level == "stuck" || level == "error" {
			switch {
			case flag("successful repeat"):
				return variants["stuck_repeat_successful"]
			case flag("churn"):
				return variants["stuck_churn"]
			case flag("typesafe error"):
				return variants["stuck_typesafe_error"]
			case flag("exact repeat"):
				return variants["stuck_repeat"]
			case tokens["progress"] != "" && tokenNumber(tokens["progress"]) < 0.2:
				return variants["stuck_no_progress"]
			}
			return variants["stuck_repeat"]
		}
		if level == "ok" {
			return variants["ok"]
		}
		return variants["default"]
	case "done":
		switch level {
		case "false claim":
			return variants["false_claim"]
		case "unverified":
			switch {
			case flag("typesafe error"):
				return variants["typesafe_error"]
			case flag("nudged"):
				return variants["unverified_nudged"]
			}
			return variants["unverified"]
		case "ok":
			if tokens["checks"] != "" && tokens["checksPassed"] != "" && tokens["checks"] == tokens["checksPassed"] {
				return variants["ok_all_passed"]
			}
			return variants["ok"]
		}
		return variants["default"]
	case "prose":
		switch {
		case level == "nudged":
			return variants["nudged"]
		case level == "ok":
			return variants["ok"]
		case flag("typesafe error"):
			return variants["typesafe_error"]
		case has("wordy") && has("cliches") && has("jargon"):
			return variants["multiple"]
		case has("wordy"):
			return variants["wordy"]
		case has("cliches"):
			return variants["cliches"]
		case has("jargon"):
			return variants["jargon"]
		}
		return variants["default"]
	case "security":
		switch {
		case has("injection") && has("exfiltration"):
			return variants["both"]
		case has("injection"):
			return variants["injection"]
		case has("exfiltration"):
			return variants["exfiltration"]
		case level == "ok":
			return variants["ok"]
		}
		return variants["default"]
	case "context":
		switch {
		case tokens["bytesSaved"] != "" && tokenNumber(tokens["bytesSaved"]) > 0:
			return variants["compressed"]
		case tokens["retention"] == "all":
			return variants["kept"]
		}
		return variants["default"]
	case "runaway":
		switch level {
		case "stopped, recovering":
			return variants["stopped_recovering"]
		case "stopped":
			return variants["stopped"]
		}
		return variants["default"]
	case "rules":
		switch level {
		case "violation":
			return variants["violations"]
		case "skipped":
			return variants["skipped"]
		case "ok":
			return variants["none"]
		}
		return variants["default"]
	case "subagent":
		switch {
		case tokens["wake"] == "true":
			return variants["wake"]
		case level == "appended silently":
			return variants["appended"]
		case level == "silent":
			return variants["silent"]
		}
		return variants["default"]
	}
	return defaultTemplateFor(guard)
}

var (
	reIrreversibleWarning = lazyRE(`(?:^|; )possibly irreversible \d+%`)
	reIrreversibleHold    = lazyRE(`(?:^|; )irreversible \d+%`)
)

// defaultTemplateFor is DEFAULT_TEMPLATES[guard] ?? "" for the guards the Go Templates struct carries and the
// widget configuration's templates for the rest.
func defaultTemplateFor(guard string) string {
	d := defaultWidgetConfig()
	switch guard {
	case "action":
		return d.Action
	case "stuck":
		return d.Stuck
	case "done":
		return d.Done
	case "prose":
		return d.Prose
	case "security":
		return d.Security
	case "context":
		return d.Context
	case "runaway":
		return d.Runaway
	case "rules":
		return d.Rules
	case "subagent":
		return d.Subagent
	}
	return ""
}

var reNumberTail = lazyRE(`^(.+[ \t])([0-9][0-9.]*)$`)

// RenderSegment: body segments read as data, not prose. The subject (tool, path, agent) stays in the text tone, numeric
// values keep the text tone, and the labels around them sit one step down in muted.
func RenderSegment(segment string, subject bool, theme ThemeLike) string {
	if subject {
		return theme.Fg("text", segment)
	}
	if m := reNumberTail.FindStringSubmatch(segment); m != nil {
		return theme.Fg("muted", m[1]) + theme.Fg("text", m[2])
	}
	return theme.Fg("muted", segment)
}

// ParseVerdictLine splits a rendered line into its verdict and its body. The verdict is the trailing segment when the
// template ends on a known level, wherever the template put `{level}` or `{status}`; the body drops the `warden` prefix
// and the guard's own name, so the guard is named once by the rail that renders this. status is "" without a verdict.
func ParseVerdictLine(line, guard string) (status string, body []string) {
	segments := strings.Split(line, separator)
	last := segments[len(segments)-1]
	if _, known := LevelColor[last]; len(segments) > 1 && known {
		status = last
		segments = segments[:len(segments)-1]
	}
	if len(segments) > 0 && segments[0] == "warden" {
		segments = segments[1:]
	}
	if len(segments) > 0 && segments[0] == guard {
		segments = segments[1:]
	}
	return status, segments
}

// WidgetEntry is one guard's latest line.
type WidgetEntry struct{ Guard, Line string }

func padEnd(s string, width int) string {
	if n := utf16Len(s); n < width {
		return s + strings.Repeat(" ", width-n)
	}
	return s
}

// WidgetLines renders the status stack: a verdict chip leads each line, the guard follows in muted, and the body reads
// as data. Guards whose last verdict found nothing fold into one line per verdict, so a calm turn costs one line instead
// of one per guard; the verdict that ends nearest the editor is the one most worth the eye. With a width, each line wraps
// to it and a continuation keeps the column of the body.
func WidgetLines(entries []WidgetEntry, theme ThemeLike, width int) []string {
	type item struct {
		guard, status string
		body          []string
	}
	parsed := make([]item, len(entries))
	for i, e := range entries {
		status, body := ParseVerdictLine(e.Line, e.Guard)
		parsed[i] = item{e.Guard, status, body}
	}
	severity := func(status string) int { return ColorSeverity[LevelColor[status]] }
	// Folded: one line per quiet verdict. Own: one line, verdict or not. Loud: one line each, worst nearest the editor.
	var foldedOrder []string
	folded := map[string][]string{}
	var own, loud []item
	for _, it := range parsed {
		color, known := LevelColor[it.status]
		switch {
		case it.status == "" || !known || CarriesFlag(it.body):
			own = append(own, it)
		case QuietColors[color]:
			if _, seen := folded[it.status]; !seen {
				foldedOrder = append(foldedOrder, it.status)
			}
			folded[it.status] = append(folded[it.status], it.guard)
		default:
			loud = append(loud, it)
		}
	}
	// Stable sort on severity alone: guards that fired together keep the order they were recorded in.
	for i := 1; i < len(loud); i++ {
		for j := i; j > 0 && severity(loud[j-1].status) > severity(loud[j].status); j-- {
			loud[j-1], loud[j] = loud[j], loud[j-1]
		}
	}
	// The rail is one column, as wide as the verdicts present. A two-word verdict (`stopped, recovering`) overflows its
	// column instead of pushing every other line right.
	const railMax = 10
	railWidth := 0
	for _, it := range parsed {
		if size := utf16Len(strings.ToUpper(it.status)); size > 0 && size <= railMax && size > railWidth {
			railWidth = size
		}
	}
	color := func(status string) string {
		if c, ok := LevelColor[status]; ok {
			return c
		}
		return "text"
	}
	rail := func(status string) string {
		return theme.Bold(theme.Fg(color(status), padEnd(strings.ToUpper(status), railWidth)))
	}
	dimSep := theme.Fg("dim", separator)
	var lines []string
	// The chip column plus the space after it; a line with no verdict keeps the column blank so the bodies stay in line.
	chipCell := func(status string) (string, int) {
		if status == "" {
			if railWidth > 0 {
				return strings.Repeat(" ", railWidth+1), railWidth + 1
			}
			return "", 0
		}
		return rail(status) + " ", max(railWidth, utf16Len(strings.ToUpper(status))) + 1
	}
	push := func(head string, headWidth int, body string) {
		if width == 0 || body == "" {
			lines = append(lines, head+body)
			return
		}
		wrapped := widthx.WrapTextWithAnsi(body, max(10, width-headWidth))
		first := ""
		if len(wrapped) > 0 {
			first = wrapped[0]
			wrapped = wrapped[1:]
		}
		lines = append(lines, head+first)
		for _, rest := range wrapped {
			lines = append(lines, strings.Repeat(" ", headWidth)+rest)
		}
	}
	for _, status := range foldedOrder {
		text, w := chipCell(status)
		push(text, w, theme.Fg("muted", strings.Join(folded[status], separator)))
	}
	named := append(append([]item{}, own...), loud...)
	guardWidth := 0
	for _, it := range named {
		guardWidth = max(guardWidth, utf16Len(it.guard))
	}
	for _, it := range named {
		segments := make([]string, len(it.body))
		for n, segment := range it.body {
			segments[n] = RenderSegment(segment, n == 0, theme)
		}
		body := strings.Join(segments, dimSep)
		name := it.guard
		if body != "" {
			name = padEnd(it.guard, guardWidth)
		}
		chip, chipWidth := chipCell(it.status)
		extra := 0
		if body != "" {
			extra = 1
		}
		suffix := ""
		if body != "" {
			suffix = " "
		}
		push(chip+theme.Fg("muted", name)+suffix, chipWidth+utf16Len(name)+extra, body)
	}
	return lines
}
