package pi_subagents

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// The exact status of a nested run (run-status.ts formatNestedExactStatus) and the nested lines it lists (shared/nested-render.ts).
// The small formatters it needs (duration, token usage, activity label, model and thinking) are local: the terminal views slice
// owns the general ones.

func nestedFmtDuration(ms float64) string {
	switch {
	case ms < 1000:
		return jsNumber(ms) + "ms"
	case ms < 60000:
		return jsToFixed(ms/1000, 1) + "s"
	}
	return fmt.Sprintf("%dm%ds", int(math.Floor(ms/60000)), int(math.Floor(math.Mod(ms, 60000)/1000)))
}

func nestedFmtTokens(n float64) string {
	switch {
	case n < 1000:
		return jsNumber(n)
	case n < 10000:
		return jsToFixed(n/1000, 1) + "k"
	case n < 999500:
		return fmt.Sprintf("%.0fk", math.Round(n/1000))
	}
	return jsNumber(math.Round(n/1e5)/10) + "M"
}

func nestedFmtTokenUsage(u jm) string {
	total, _ := u["total"].(float64)
	if w, ok := u["window"].(float64); ok {
		return nestedFmtTokens(w) + " window · " + nestedFmtTokens(total) + " spent"
	}
	return nestedFmtTokens(total) + " tok"
}

func nestedFmtModelThinking(model, thinking string) string {
	display, suffix := model, ""
	if model != "" {
		display, suffix = splitKnownThinkingSuffix(model)
	}
	explicit := ""
	if _, ok := nestedThinking(strings.TrimSpace(thinking)); ok {
		explicit = strings.TrimSpace(thinking)
	}
	level := explicit
	if suffix != "" {
		level = suffix[1:]
	}
	if display != "" {
		if i := strings.LastIndex(display, "/"); i != -1 {
			display = display[i+1:]
		}
	}
	var parts []string
	if display != "" {
		parts = append(parts, display)
	}
	if level != "" {
		parts = append(parts, "thinking "+level)
	}
	return strings.Join(parts, " · ")
}

func nestedFmtActivityLabel(last *float64, state string, now float64) string {
	if last == nil {
		switch state {
		case "needs_attention":
			return "needs attention"
		case "active_long_running":
			return "active but long-running"
		}
		return ""
	}
	ms := math.Max(0, now-*last)
	age := "now"
	switch {
	case ms >= 60000:
		age = fmt.Sprintf("%dm", int(math.Floor(ms/60000)))
	case ms >= 1000:
		age = fmt.Sprintf("%ds", int(math.Floor(ms/1000)))
	}
	switch state {
	case "needs_attention":
		return "no activity for " + age
	case "active_long_running":
		if age != "now" {
			age += " ago"
		}
		return "active but long-running · last activity " + age
	}
	if age == "now" {
		return "active now"
	}
	return "active " + age + " ago"
}

func numPtr(m jm, k string) *float64 {
	if n, ok := m[k].(float64); ok {
		return &n
	}
	return nil
}

func nestedRunCounts(children []jm) (total int, byState map[string]int) {
	byState = map[string]int{}
	for _, c := range children {
		total++
		byState[strOf(c["state"])]++
		sub := append(asJMList(c["children"]), nestedStepChildren(c)...)
		t, bs := nestedRunCounts(sub)
		total += t
		for k, v := range bs {
			byState[k] += v
		}
	}
	return
}

func formatNestedAggregate(children []jm) string {
	total, by := nestedRunCounts(children)
	if total == 0 {
		return ""
	}
	var parts []string
	for _, s := range []string{"running", "paused", "failed", "partial", "rejected", "stopped", "complete", "queued"} {
		if by[s] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", by[s], s))
		}
	}
	plural := "s"
	if total == 1 {
		plural = ""
	}
	out := fmt.Sprintf("+%d nested run%s", total, plural)
	if len(parts) > 0 {
		out += " (" + strings.Join(parts, ", ") + ")"
	}
	return out
}

func nestedRunLabel(run jm) string {
	if n := jsTrim(strOf(run["sessionName"])); n != "" {
		return n
	}
	if a := strOf(run["agent"]); a != "" {
		return a
	}
	if agents, _ := run["agents"].([]any); len(agents) > 0 {
		if len(agents) == 1 {
			return strOf(agents[0])
		}
		s := strOf(agents[0]) + ", " + strOf(agents[1])
		if len(agents) > 2 {
			s += fmt.Sprintf(" +%d", len(agents)-2)
		}
		return s
	}
	return strOf(run["id"])
}

func formatNestedActivity(in jm, now float64) string {
	var facts []string
	tool := strOf(in["currentTool"])
	if start := numPtr(in, "currentToolStartedAt"); tool != "" && start != nil {
		facts = append(facts, "tool "+tool+" "+nestedFmtDuration(math.Max(0, now-*start)))
	} else if tool != "" {
		facts = append(facts, "tool "+tool)
	}
	if p := strOf(in["currentPath"]); p != "" {
		facts = append(facts, shortenPath(p))
	}
	if n := numPtr(in, "turnCount"); n != nil {
		facts = append(facts, jsNumber(*n)+" turns")
	}
	if n := numPtr(in, "toolCount"); n != nil {
		facts = append(facts, jsNumber(*n)+" tools")
	}
	if u := jmGetMap(in["totalTokens"]); u != nil {
		facts = append(facts, nestedFmtTokenUsage(u))
	}
	activity := nestedFmtActivityLabel(numPtr(in, "lastActivityAt"), strOf(in["activityState"]), now)
	if activity == "" && len(facts) == 0 {
		return ""
	}
	var all []string
	if activity != "" {
		all = append(all, activity)
	}
	return strings.Join(append(all, facts...), " | ")
}

func jmGetMap(v any) jm {
	m, _ := v.(map[string]any)
	return m
}

type nestedLineOptions struct {
	indent       string
	maxDepth     int
	maxLines     int
	commandHints bool
}

func formatNestedRunLines(children []jm, o nestedLineOptions, now float64) []string {
	var lines []string
	var appendItems func(items []jm, depth int, indent string)
	appendItems = func(items []jm, depth int, indent string) {
		if len(items) == 0 || len(lines) >= o.maxLines {
			return
		}
		if depth > o.maxDepth {
			if agg := formatNestedAggregate(items); agg != "" && len(lines) < o.maxLines {
				lines = append(lines, indent+"↳ "+agg)
			}
			return
		}
		for i, child := range items {
			if len(lines) >= o.maxLines {
				if agg := formatNestedAggregate(items[i:]); agg != "" {
					lines[len(lines)-1] = indent + "↳ " + agg
				}
				return
			}
			activity := ""
			if child["state"] == "running" {
				activity = formatNestedActivity(child, now)
			}
			errText := ""
			if e := strOf(child["error"]); e != "" {
				errText = " | error: " + e
			}
			mt := nestedFmtModelThinking(strOf(child["model"]), strOf(child["thinking"]))
			line := fmt.Sprintf("%s↳ %s [%s] %s", indent, nestedRunLabel(child), strOf(child["id"]), strOf(child["state"]))
			if mt != "" {
				line += " | " + mt
			}
			if activity != "" {
				line += " | " + activity
			}
			lines = append(lines, line+errText)
			if o.commandHints && len(lines) < o.maxLines {
				lines = append(lines, fmt.Sprintf(`%s  Status: subagent({ action: "status", id: "%s" })`, indent, strOf(child["id"])))
			}
			if depth == o.maxDepth {
				if agg := formatNestedAggregate(append(nestedStepChildren(child), asJMList(child["children"])...)); agg != "" && len(lines) < o.maxLines {
					lines = append(lines, indent+"  ↳ "+agg)
				}
				continue
			}
			for si, step := range asJMList(child["steps"]) {
				if len(lines) >= o.maxLines {
					return
				}
				stepActivity := ""
				if step["status"] == "running" {
					stepActivity = formatNestedActivity(step, now)
				}
				name := jsTrim(strOf(step["sessionName"]))
				if name == "" {
					name = strOf(step["agent"])
				}
				l := fmt.Sprintf("%s  %d. %s %s", indent, si+1, name, strOf(step["status"]))
				if smt := nestedFmtModelThinking(strOf(step["model"]), strOf(step["thinking"])); smt != "" {
					l += " | " + smt
				}
				if stepActivity != "" {
					l += " | " + stepActivity
				}
				if e := strOf(step["error"]); e != "" {
					l += " | error: " + e
				}
				lines = append(lines, l)
				appendItems(asJMList(step["children"]), depth+1, indent+"    ")
			}
			appendItems(asJMList(child["children"]), depth+1, indent+"  ")
		}
	}
	appendItems(children, 0, o.indent)
	return lines
}

func nestedRunDisplayName(run jm) string {
	if n := jsTrim(strOf(run["sessionName"])); n != "" {
		return n
	}
	if a := strOf(run["agent"]); a != "" {
		return a
	}
	if agents, _ := run["agents"].([]any); len(agents) > 0 {
		names := make([]string, len(agents))
		for i, a := range agents {
			names[i] = strOf(a)
		}
		return strings.Join(names, ", ")
	}
	return strOf(run["id"])
}

func formatNestedExactStatus(rootRunID string, run jm) string {
	now := float64(time.Now().UnixMilli())
	var lines []string
	add := func(s string) { lines = append(lines, s) }
	add("Nested run: " + strOf(run["id"]))
	add("Root: " + rootRunID)
	parent := "Parent: " + strOf(run["parentRunId"])
	if p := numPtr(run, "parentStepIndex"); p != nil {
		parent += fmt.Sprintf(" step %s", jsNumber(*p+1))
	}
	add(parent)
	add("State: " + strOf(run["state"]))
	if run["activityState"] != nil || run["lastActivityAt"] != nil {
		if l := nestedFmtActivityLabel(numPtr(run, "lastActivityAt"), strOf(run["activityState"]), now); l != "" {
			add("Activity: " + l)
		}
	}
	if m := strOf(run["mode"]); m != "" {
		add("Mode: " + m)
	}
	add("Agent: " + nestedRunDisplayName(run))
	if cs := numPtr(run, "currentStep"); cs != nil {
		count := float64(len(asJMList(run["steps"])))
		if count == 0 {
			count = 1
		}
		if c := numPtr(run, "chainStepCount"); c != nil {
			count = *c
		}
		add(fmt.Sprintf("Progress: step %s/%s", jsNumber(*cs+1), jsNumber(count)))
	}
	if tb := jmGetMap(run["turnBudget"]); tb != nil {
		add(fmt.Sprintf("Turn budget: %s/%s+%s (%s)", jsNumber(numOr(tb, "turnCount", 0)), jsNumber(numOr(tb, "maxTurns", 0)), jsNumber(numOr(tb, "graceTurns", 0)), strOf(tb["outcome"])))
	}
	for _, kv := range [][2]string{{"Dir", "asyncDir"}, {"Session", "sessionFile"}, {"Error", "error"}} {
		if v := strOf(run[kv[1]]); v != "" {
			add(kv[0] + ": " + v)
		}
	}
	if path, _ := run["path"].([]any); len(path) > 0 {
		parts := make([]string, len(path))
		for i, p := range path {
			pm := jmGetMap(p)
			s := strOf(pm["runId"])
			if si := numPtr(pm, "stepIndex"); si != nil {
				s += ":" + jsNumber(*si+1)
			}
			if a := strOf(pm["agent"]); a != "" {
				s += ":" + a
			}
			parts[i] = s
		}
		add("Path: " + strings.Join(parts, " > ") + " > " + strOf(run["id"]))
	}
	if steps := asJMList(run["steps"]); len(steps) > 0 {
		add("Steps:")
		for i, step := range steps {
			activity := ""
			if step["status"] == "running" {
				activity = nestedFmtActivityLabel(numPtr(step, "lastActivityAt"), strOf(step["activityState"]), now)
			}
			budget := ""
			if tb := jmGetMap(step["turnBudget"]); tb != nil {
				budget = fmt.Sprintf(", turn budget: %s/%s+%s (%s)", jsNumber(numOr(tb, "turnCount", 0)), jsNumber(numOr(tb, "maxTurns", 0)), jsNumber(numOr(tb, "graceTurns", 0)), strOf(tb["outcome"]))
			}
			name := jsTrim(strOf(step["sessionName"]))
			if name == "" {
				name = strOf(step["agent"])
			}
			l := fmt.Sprintf("  %d. %s %s", i+1, name, strOf(step["status"]))
			if activity != "" {
				l += ", " + activity
			}
			l += budget
			if e := strOf(step["error"]); e != "" {
				l += ", error: " + e
			}
			add(l)
			lines = append(lines, formatNestedRunLines(asJMList(step["children"]), nestedLineOptions{indent: "    ", maxDepth: 2, maxLines: 40, commandHints: true}, now)...)
		}
	}
	lines = append(lines, formatNestedRunLines(asJMList(run["children"]), nestedLineOptions{indent: "  ", maxDepth: 2, maxLines: 40, commandHints: true}, now)...)
	id := strOf(run["id"])
	lines = append(lines, "Commands:", fmt.Sprintf(`  Status: subagent({ action: "status", id: "%s" })`, id), fmt.Sprintf(`  Interrupt: subagent({ action: "interrupt", id: "%s" })`, id),
		fmt.Sprintf(`  Resume: subagent({ action: "resume", id: "%s", message: "..." })`, id), fmt.Sprintf(`  Steer: subagent({ action: "steer", id: "%s", message: "..." })`, id),
		fmt.Sprintf(`  Root status: subagent({ action: "status", id: "%s" })`, rootRunID))
	return strings.Join(lines, "\n")
}
