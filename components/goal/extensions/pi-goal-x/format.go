package pi_goal_x

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Text the goal commands show. goal-core.ts, goal-format.ts, goal-pool.ts, goal-policy.ts, widgets/goal-notifications.ts.

// jsS is JavaScript's \s: the ECMAScript WhiteSpace and LineTerminator set (RE2's \s is ASCII only).
const jsS = `[\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]`

// jsDot is JavaScript's `.` without the s flag.
const jsDot = `[^\n\r\x{2028}\x{2029}]`

var wsRun = regexp.MustCompile(jsS + `+`)

// utf16Prefix returns the first n UTF-16 code units of s; a cut inside a surrogate pair keeps the replacement character.
func utf16Prefix(s string, n int) string {
	units := 0
	for i, r := range s {
		w := 1
		if r >= 0x10000 {
			w = 2
		}
		if units+w > n {
			if w == 2 && units+1 == n {
				return s[:i] + "\ufffd"
			}
			return s[:i]
		}
		units += w
	}
	return s
}

// truncateText collapses white space and cuts to max UTF-16 units with "...".
func truncateText(value string, max int) string {
	one := jsTrim(wsRun.ReplaceAllString(value, " "))
	if utf16Len(one) > max {
		return utf16Prefix(one, max-3) + "..."
	}
	return one
}

var (
	titleBanner  = regexp.MustCompile(`(?i)^=+` + jsS + `*(?:sisyphus` + jsS + `+)?goal` + jsS + `*=+$`)
	titleObj     = regexp.MustCompile(`(?i)^(?:objective|目标)` + jsS + `*[:：]` + jsS + `*(` + jsDot + `+)$`)
	titleSection = regexp.MustCompile(`(?i)^(success criteria|boundaries|constraints|steps|order rules|don'ts|if blocked|if blocked / unclear / failing|sisyphus reminder)` + jsS + `*[:：]`)
)

// jsFoldSafe prepares text for a case-insensitive match that must behave like JavaScript's /i without the u flag. RE2's (?i)
// folds U+017F (long s) onto s and U+212A (Kelvin sign) onto k; JavaScript never maps a non-ASCII character onto an ASCII one,
// so neither matches an ASCII letter there. They become U+FFFD, which matches nothing in these patterns either. Use it only for
// a yes/no match, never for text that is captured.
var jsFoldSafe = strings.NewReplacer("\u017f", "\ufffd", "\u212a", "\ufffd").Replace

func displayObjectiveTitle(objective string) string {
	var lines []string
	for _, l := range strings.Split(strings.ReplaceAll(objective, "\r", ""), "\n") {
		if t := jsTrim(l); t != "" {
			lines = append(lines, t)
		}
	}
	for _, line := range lines {
		if titleBanner.MatchString(jsFoldSafe(line)) {
			continue
		}
		if m := titleObj.FindStringSubmatch(line); m != nil && m[1] != "" {
			return jsTrim(m[1])
		}
		if titleSection.MatchString(jsFoldSafe(line)) {
			continue
		}
		return line
	}
	return truncateText(objective, 120)
}

func groupDigits(n int64) string {
	s := fmt.Sprintf("%d", n)
	var out []byte
	for i := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	return string(out)
}

// formatTokenValue is "12.5K (12,500) tokens" style text.
func formatTokenValue(value float64) string {
	safe := value
	if safe < 0 {
		safe = 0
	}
	n := int64(safe)
	f := float64(n)
	trim := func(s string) string { return strings.TrimSuffix(s, ".0") }
	var compact string
	switch {
	case f >= 1e9:
		d := 1
		if f >= 1e10 {
			d = 0
		}
		compact = trim(jsToFixed(f/1e9, d)) + "B"
	case f >= 1e6:
		d := 1
		if f >= 1e7 {
			d = 0
		}
		compact = trim(jsToFixed(f/1e6, d)) + "M"
	case f >= 10000:
		compact = jsToFixed(f/1e3, 0) + "K"
	case f >= 1000:
		compact = trim(jsToFixed(f/1e3, 1)) + "K"
	default:
		compact = fmt.Sprintf("%d", n)
	}
	exact := groupDigits(n)
	if compact == exact {
		return exact + " tokens"
	}
	return compact + " (" + exact + ") tokens"
}

func formatDuration(seconds float64) string {
	total := int64(0)
	if seconds > 0 {
		total = int64(seconds)
	}
	h, m, s := total/3600, (total%3600)/60, total%60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh%02dm%02ds", h, m, s)
	case m > 0:
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

func gstr(g *jsObject, k string) string  { s, _ := g.str(k); return s }
func gnum(g *jsObject, k string) float64 { f, _ := g.num(k); return f }
func gflag(g *jsObject, k string) bool   { b, _ := g.flag(k); return b }

func statusLabel(g *jsObject) string {
	prefix := ""
	if gflag(g, "sisyphus") {
		prefix = "sisyphus "
	}
	status := gstr(g, "status")
	switch {
	case status == "active" && gflag(g, "autoContinue"):
		return prefix + "running"
	case status == "paused" && gstr(g, "stopReason") == "agent":
		return prefix + "paused (agent)"
	case status == "blocked":
		return prefix + "blocked"
	case status == "budget_limited":
		return prefix + "budget limited"
	}
	return prefix + status
}

func usageOf(g *jsObject) (tokens, seconds float64) {
	if u := g.obj("usage"); u != nil {
		return gnum(u, "tokensUsed"), gnum(u, "activeSeconds")
	}
	return 0, 0
}

func firstWord(s string) string { return strings.SplitN(s, " ", 2)[0] }

func oneLineSummary(g *jsObject) string {
	if g == nil {
		return "No goal is set."
	}
	tokens, _ := usageOf(g)
	tail := ""
	if tokens > 0 {
		tail = " [" + firstWord(formatTokenValue(tokens)) + "]"
	}
	return statusLabel(g) + tail + " - " + truncateText(gstr(g, "objective"), 120)
}

type taskCounts struct{ total, complete, skipped int }

func countTaskSubtree(tasks []any) taskCounts {
	var c taskCounts
	var walk func([]any)
	walk = func(list []any) {
		for _, x := range list {
			t, _ := x.(*jsObject)
			if t == nil {
				continue
			}
			c.total++
			switch gstr(t, "status") {
			case "complete":
				c.complete++
			case "skipped":
				c.skipped++
			}
			if subs, ok := t.vals["subtasks"].([]any); ok && len(subs) > 0 {
				walk(subs)
			}
		}
	}
	walk(tasks)
	return c
}

func buildTaskSummary(list *jsObject) string {
	tasks, _ := list.vals["tasks"].([]any)
	c := countTaskSubtree(tasks)
	if c.total == 0 {
		return "No tasks"
	}
	parts := []string{fmt.Sprintf("%d/%d tasks complete", c.complete, c.total)}
	if c.skipped > 0 {
		parts = append(parts, fmt.Sprintf("(%d skipped)", c.skipped))
	}
	return strings.Join(parts, " ")
}

func detailedSummary(g *jsObject) string {
	if g == nil {
		return "No goal is set. Use /goal <objective> or /sisyphus <objective> to start immediately."
	}
	tokens, seconds := usageOf(g)
	on := "off"
	if gflag(g, "autoContinue") {
		on = "on"
	}
	lines := []string{"Goal: " + gstr(g, "objective"), "Status: " + statusLabel(g), "Auto-continue: " + on,
		"Time spent: " + formatDuration(seconds), "Tokens used: " + formatTokenValue(tokens)}
	if gflag(g, "sisyphus") {
		lines = append(lines, "Mode: Sisyphus (prompt/criteria variant; shared goal lifecycle)")
	}
	if tl := g.obj("taskList"); tl != nil {
		lines = append(lines, "Tasks: "+buildTaskSummary(tl))
		queue, _ := tl.vals["tasks"].([]any)
		queue = append([]any(nil), queue...)
		var first *jsObject
		for len(queue) > 0 && first == nil {
			t, _ := queue[0].(*jsObject)
			queue = queue[1:]
			if t == nil {
				continue
			}
			if gstr(t, "status") == "pending" {
				first = t
			} else if subs, ok := t.vals["subtasks"].([]any); ok {
				queue = append(queue, subs...)
			}
		}
		if first != nil {
			lines = append(lines, "Next pending task: "+gstr(first, "id")+" — "+gstr(first, "title"))
		}
	}
	for _, p := range [][2]string{{"File: ", "activePath"}, {"Archive: ", "archivedPath"}, {"Stop reason: ", "stopReason"}, {"Agent pause reason: ", "pauseReason"}, {"Agent suggests: ", "pauseSuggestedAction"}} {
		if g.has(p[1]) && truthy(g.vals[p[1]]) {
			lines = append(lines, p[0]+gstr(g, p[1]))
		}
	}
	return strings.Join(lines, "\n")
}

// ---- goal-pool.ts ----

// openGoalsFromPool lists the goals that are not complete, oldest first, ties by id (UTF-16 order is not reproduced:
// localeCompare is approximated by byte order, which agrees for the ids this extension generates).
func openGoalsFromPool(pool *goalPool) []*jsObject {
	var out []*jsObject
	for _, g := range pool.values() {
		if gstr(g, "status") != "complete" {
			out = append(out, g)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if c := localeCompare(gstr(out[i], "createdAt"), gstr(out[j], "createdAt")); c != 0 {
			return c < 0
		}
		return localeCompare(gstr(out[i], "id"), gstr(out[j], "id")) < 0
	})
	return out
}

func otherOpenGoalCount(pool *goalPool, focused *string) int {
	n := 0
	for _, g := range pool.values() {
		if gstr(g, "status") != "complete" && (focused == nil || gstr(g, "id") != *focused) {
			n++
		}
	}
	return n
}

func goalSelectorLabel(g *jsObject, focused *string) string {
	marker := " "
	if focused != nil && gstr(g, "id") == *focused {
		marker = "*"
	}
	mode := "goal"
	if gflag(g, "sisyphus") {
		mode = "sisyphus"
	}
	path := ""
	if g.has("activePath") && truthy(g.vals["activePath"]) {
		path = " " + gstr(g, "activePath")
	}
	return fmt.Sprintf("%s %s | %s | %s | %s%s", marker, gstr(g, "id"), statusLabel(g), mode, truncateText(displayObjectiveTitle(gstr(g, "objective")), 72), path)
}

func buildGoalListText(pool *goalPool, focused *string) string {
	open := openGoalsFromPool(pool)
	if len(open) == 0 {
		return "No open goals. Use /goal <objective> or /sisyphus <objective> to start immediately."
	}
	lines := []string{fmt.Sprintf("Open goals: %d", len(open)), ""}
	for _, g := range open {
		marker := " "
		if focused != nil && gstr(g, "id") == *focused {
			marker = "*"
		}
		mode := "goal"
		if gflag(g, "sisyphus") {
			mode = "sisyphus"
		}
		usage := ""
		if tokens, seconds := usageOf(g); tokens > 0 || seconds > 0 {
			usage = " · " + formatDuration(seconds) + " · " + firstWord(formatTokenValue(tokens))
		}
		lines = append(lines, fmt.Sprintf("%s %s — %s · %s%s", marker, gstr(g, "id"), statusLabel(g), mode, usage))
		lines = append(lines, "  "+displayObjectiveTitle(gstr(g, "objective")))
		if g.has("activePath") && truthy(g.vals["activePath"]) {
			lines = append(lines, "  "+gstr(g, "activePath"))
		}
	}
	return strings.Join(lines, "\n")
}

func buildUnfocusedOpenGoalsSummary(n int) string {
	s := "s"
	if n == 1 {
		s = ""
	}
	return fmt.Sprintf("No goal is focused in this session. %d open goal%s exist in the goal pool. Use /goal-focus to choose the session focus before doing goal work.", n, s)
}

// localeCompare approximates String.prototype.localeCompare for the ASCII text of ISO times and generated ids.
func localeCompare(a, b string) int {
	if a == b {
		return 0
	}
	if utf8.ValidString(a) && utf8.ValidString(b) {
		return strings.Compare(strings.ToLower(a), strings.ToLower(b))*2 + strings.Compare(a, b)
	}
	return strings.Compare(a, b)
}

func clearGoalCommandMessage(archived bool) string {
	if archived {
		return "Goal cleared and archived."
	}
	return "No goal is set."
}

func buildGoalRunningNotification(objective string, sisyphus, autoContinue bool) string {
	icon, mode := "●", "Goal"
	if sisyphus {
		icon, mode = "◆", "Sisyphus"
	}
	drive := "manual mode"
	if autoContinue {
		drive = "auto-continue on"
	}
	return strings.Join([]string{icon + " " + mode + " running", "├─ ⟡ " + truncateText(displayObjectiveTitle(objective), 92), "└─ " + drive}, "\n")
}

// ---- goal-contract.ts ----

var contractRE = regexp.MustCompile(`(?im)^Verification contract:` + jsS + `*(` + jsDot + `+)$`)

// extractVerificationContract removes the "Verification contract:" lines and returns the last one's text.
func extractVerificationContract(objective string) (string, string) {
	var contract string
	var kept []string
	for _, line := range strings.Split(strings.ReplaceAll(objective, "\r", ""), "\n") {
		if m := contractRE.FindStringSubmatch(jsTrim(line)); m != nil {
			contract = jsTrim(m[1])
		} else {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n"), contract
}

var sisyphusSteps = regexp.MustCompile(`(?i)\b\d{1,2}` + jsS + `*[).:]|\bstep` + jsS + `*\d+`)

func sisyphusObjectiveSufficient(objective string) bool {
	return sisyphusSteps.MatchString(jsFoldSafe(jsTrim(objective)))
}
