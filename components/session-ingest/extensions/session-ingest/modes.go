package sessioningest

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// ── mode=turn ────────────────────────────────────────────────────────────────

var defaultInclude = []string{"text", "tool_calls", "errors"}

type turnReport struct {
	Mode       string          `json:"mode"`
	Path       string          `json:"path"`
	Session    sessionMetadata `json:"session"`
	Turn       int             `json:"turn"`
	TotalTurns int             `json:"totalTurns"`
	Include    []string        `json:"include"`
	Messages   []turnMessage   `json:"messages"`
}

type turnMessage struct {
	EntryIndex   int           `json:"entryIndex"`
	MessageIndex int           `json:"messageIndex"`
	Timestamp    string        `json:"timestamp,omitempty"`
	Role         string        `json:"role"`
	Text         []string      `json:"text,omitempty"`
	Thinking     []string      `json:"thinking,omitempty"`
	ToolCalls    []toolCall    `json:"toolCalls,omitempty"`
	ToolName     string        `json:"toolName,omitempty"`
	IsError      bool          `json:"isError,omitempty"`
	StopReason   string        `json:"stopReason,omitempty"`
	ErrorMessage string        `json:"errorMessage,omitempty"`
	Model        string        `json:"model,omitempty"`
	Usage        *usageSummary `json:"usage,omitempty"`
}

func handleTurn(session sessionFile, params map[string]any, format, path string, start, limit, maxChars int) (any, error) {
	turn := intParam(params, "turn", 0)
	total := countTurns(session.Messages)
	if turn < 1 {
		return nil, sdk.NewToolError(fmt.Sprintf("turn is required for mode=turn (1..%d)", total))
	}
	if turn > total {
		return nil, sdk.NewToolError(fmt.Sprintf("turn %d is out of range: the session has %d turns", turn, total))
	}
	include := stringListParam(params, "include")
	if len(include) == 0 {
		include = defaultInclude
	}
	report := buildTurnReport(session, turn, total, include)
	if format == "json" {
		return report, nil
	}
	content := truncateOutput(formatTurnMarkdown(report, session.Metadata), path, "turn", start, limit, 0)
	return sdk.ToolResult{
		Content: content,
		Preview: fmt.Sprintf("Turn %d of %d — %d messages", turn, total, len(report.Messages)),
	}, nil
}

func countTurns(messages []sessionMessage) int {
	n := 0
	for _, m := range messages {
		if m.Role == "user" {
			n++
		}
	}
	return n
}

func buildTurnReport(session sessionFile, turn, total int, include []string) turnReport {
	want := map[string]bool{}
	for _, name := range include {
		want[name] = true
	}
	report := turnReport{Mode: "turn", Path: session.Path, Session: session.Metadata, Turn: turn, TotalTurns: total, Include: include, Messages: []turnMessage{}}
	current := 0
	for _, msg := range session.Messages {
		if msg.Role == "user" {
			current++
		}
		if current != turn {
			continue
		}
		out := turnMessage{EntryIndex: msg.EntryIndex, MessageIndex: msg.MessageIndex, Timestamp: msg.Timestamp, Role: msg.Role, ToolName: msg.ToolName, Model: msg.Model}
		if msg.Role == "toolResult" {
			if want["tool_results"] {
				out.Text = msg.Texts
			}
		} else if want["text"] {
			out.Text = msg.Texts
		}
		if want["thinking"] {
			out.Thinking = msg.Thinking
		}
		if want["tool_calls"] {
			out.ToolCalls = msg.ToolCalls
		}
		if want["errors"] {
			out.IsError = msg.IsError
			out.ErrorMessage = msg.ErrorMessage
			if strings.EqualFold(msg.StopReason, "error") {
				out.StopReason = msg.StopReason
			}
		}
		if want["usage"] && msg.Usage != (usageSummary{}) {
			usage := msg.Usage
			out.Usage = &usage
		}
		report.Messages = append(report.Messages, out)
	}
	return report
}

func formatTurnMarkdown(report turnReport, meta sessionMetadata) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Session turn %d of %d\n\n", report.Turn, report.TotalTurns)
	fmt.Fprintf(&b, "- session: %s\n", emptyDash(shortID(meta.ID)))
	fmt.Fprintf(&b, "- messages: %d\n", len(report.Messages))
	fmt.Fprintf(&b, "- include: %s\n", strings.Join(report.Include, ", "))
	for _, msg := range report.Messages {
		fmt.Fprintf(&b, "\n## message %d %s\n\n", msg.MessageIndex, msg.Role)
		if msg.ToolName != "" {
			fmt.Fprintf(&b, "- tool: %s\n", msg.ToolName)
		}
		if msg.Model != "" {
			fmt.Fprintf(&b, "- model: %s\n", msg.Model)
		}
		if msg.IsError {
			fmt.Fprintf(&b, "- errors: tool result reported an error\n")
		}
		if msg.StopReason != "" {
			detail := ""
			if msg.ErrorMessage != "" {
				detail = ": " + msg.ErrorMessage
			}
			fmt.Fprintf(&b, "- errors: stop reason %s%s\n", msg.StopReason, detail)
		}
		if len(msg.ToolCalls) > 0 {
			labels := make([]string, 0, len(msg.ToolCalls))
			for _, call := range msg.ToolCalls {
				label := call.Name
				if call.Summary != "" {
					label += " (" + call.Summary + ")"
				}
				labels = append(labels, label)
			}
			fmt.Fprintf(&b, "- tool_calls: %s\n", strings.Join(labels, "; "))
		}
		if msg.Usage != nil {
			u := msg.Usage
			fmt.Fprintf(&b, "- usage: in %d, out %d, cache read %d, cache write %d, total %d, $%.4f\n", u.Input, u.Output, u.CacheRead, u.CacheWrite, u.Total, u.Cost)
		}
		for _, text := range msg.Thinking {
			fmt.Fprintf(&b, "\n_thinking:_ %s\n", text)
		}
		for _, text := range msg.Text {
			fmt.Fprintf(&b, "\n%s\n", text)
		}
	}
	return b.String()
}

// ── mode=tools ───────────────────────────────────────────────────────────────

type toolsReport struct {
	Mode       string          `json:"mode"`
	Path       string          `json:"path"`
	Session    sessionMetadata `json:"session"`
	Turn       int             `json:"turn,omitempty"`
	TotalCalls int             `json:"totalCalls"`
	Tools      []toolUsage     `json:"tools"`
	FilesTotal int             `json:"filesTotal"`
	Start      int             `json:"start"`
	Limit      int             `json:"limit"`
	Files      []fileTouched   `json:"files"`
}

type toolUsage struct {
	Name      string `json:"name"`
	Calls     int    `json:"calls"`
	Results   int    `json:"results"`
	Errors    int    `json:"errors"`
	FirstTurn int    `json:"firstTurn"`
	LastTurn  int    `json:"lastTurn"`
}

type fileTouched struct {
	Path  string   `json:"path"`
	Tools []string `json:"tools"`
}

func handleTools(session sessionFile, params map[string]any, format, path string, start, limit int) (any, error) {
	turn := intParam(params, "turn", 0)
	if turn < 0 {
		return nil, sdk.NewToolError("turn must be 1 or greater")
	}
	if total := countTurns(session.Messages); turn > total {
		return nil, sdk.NewToolError(fmt.Sprintf("turn %d is out of range: the session has %d turns", turn, total))
	}
	report := buildToolsReport(session, turn, start, limit)
	if format == "json" {
		return report, nil
	}
	return sdk.ToolResult{
		Content: truncateOutput(formatToolsMarkdown(report), path, "tools", start, limit, report.FilesTotal),
		Preview: fmt.Sprintf("%d tool calls across %d tools, %d files", report.TotalCalls, len(report.Tools), report.FilesTotal),
	}, nil
}

func buildToolsReport(session sessionFile, turn, start, limit int) toolsReport {
	usage := map[string]*toolUsage{}
	touched := map[string]map[string]bool{}
	get := func(name string, turnIndex int) *toolUsage {
		u := usage[name]
		if u == nil {
			u = &toolUsage{Name: name, FirstTurn: turnIndex}
			usage[name] = u
		}
		u.LastTurn = turnIndex
		return u
	}
	report := toolsReport{Mode: "tools", Path: session.Path, Session: session.Metadata, Turn: turn, Limit: limit}
	current := 0
	for _, msg := range session.Messages {
		if msg.Role == "user" {
			current++
		}
		if current == 0 || (turn > 0 && current != turn) {
			continue
		}
		switch msg.Role {
		case "assistant":
			for _, call := range msg.ToolCalls {
				get(call.Name, current).Calls++
				report.TotalCalls++
				if call.Path != "" {
					if touched[call.Path] == nil {
						touched[call.Path] = map[string]bool{}
					}
					touched[call.Path][call.Name] = true
				}
			}
		case "toolResult":
			name := msg.ToolName
			if name == "" {
				name = "unknown"
			}
			u := get(name, current)
			u.Results++
			if msg.IsError {
				u.Errors++
			}
		}
	}
	report.Tools = make([]toolUsage, 0, len(usage))
	for _, u := range usage {
		report.Tools = append(report.Tools, *u)
	}
	slices.SortFunc(report.Tools, func(a, b toolUsage) int {
		if c := cmp.Compare(b.Calls, a.Calls); c != 0 {
			return c
		}
		return cmp.Compare(a.Name, b.Name)
	})
	files := make([]fileTouched, 0, len(touched))
	for p, tools := range touched {
		names := make([]string, 0, len(tools))
		for name := range tools {
			names = append(names, name)
		}
		slices.Sort(names)
		files = append(files, fileTouched{Path: p, Tools: names})
	}
	slices.SortFunc(files, func(a, b fileTouched) int { return cmp.Compare(a.Path, b.Path) })
	report.FilesTotal = len(files)
	end := min(len(files), start+limit)
	if start > len(files) {
		start, end = len(files), len(files)
	}
	report.Start = start
	report.Files = files[start:end]
	return report
}

func formatToolsMarkdown(report toolsReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Session tools\n\n")
	fmt.Fprintf(&b, "- session: %s\n", emptyDash(shortID(report.Session.ID)))
	if report.Turn > 0 {
		fmt.Fprintf(&b, "- turn: %d\n", report.Turn)
	}
	fmt.Fprintf(&b, "- calls: %d\n", report.TotalCalls)
	if len(report.Tools) == 0 {
		fmt.Fprintf(&b, "\nNo tool calls.\n")
		return b.String()
	}
	fmt.Fprintf(&b, "\n| tool | calls | results | errors | turns |\n|---|---:|---:|---:|---|\n")
	for _, u := range report.Tools {
		turns := fmt.Sprintf("%d", u.FirstTurn)
		if u.LastTurn != u.FirstTurn {
			turns = fmt.Sprintf("%d-%d", u.FirstTurn, u.LastTurn)
		}
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %s |\n", markdownCell(u.Name), u.Calls, u.Results, u.Errors, turns)
	}
	if report.FilesTotal > 0 {
		fmt.Fprintf(&b, "\n## Files touched (%d, read/write/edit)\n\n| file | tools |\n|---|---|\n", report.FilesTotal)
		for _, f := range report.Files {
			fmt.Fprintf(&b, "| %s | %s |\n", markdownCell(f.Path), strings.Join(f.Tools, ", "))
		}
	}
	return b.String()
}

// ── mode=stats ───────────────────────────────────────────────────────────────

type statsReport struct {
	Mode            string          `json:"mode"`
	Path            string          `json:"path"`
	Session         sessionMetadata `json:"session"`
	Turns           int             `json:"turns"`
	Messages        int             `json:"messages"`
	Roles           map[string]int  `json:"roles"`
	ToolCalls       int             `json:"toolCalls"`
	Errors          int             `json:"errors"`
	Usage           usageSummary    `json:"usage"`
	Models          []modelStat     `json:"models,omitempty"`
	FirstTimestamp  string          `json:"firstTimestamp,omitempty"`
	LastTimestamp   string          `json:"lastTimestamp,omitempty"`
	DurationSeconds int             `json:"durationSeconds"`
	LineCount       int             `json:"lineCount"`
	MalformedJSON   int             `json:"malformedJson,omitempty"`
}

type modelStat struct {
	Provider string       `json:"provider,omitempty"`
	Model    string       `json:"model"`
	Messages int          `json:"messages"`
	Usage    usageSummary `json:"usage"`
}

func handleStats(session sessionFile, format, path string) (any, error) {
	report := buildStatsReport(session)
	if format == "json" {
		return report, nil
	}
	return sdk.ToolResult{
		Content: truncateOutput(formatStatsMarkdown(report), path, "stats", 0, 0, 0),
		Preview: fmt.Sprintf("%d turns, %d messages, %d tokens, $%.4f", report.Turns, report.Messages, report.Usage.Total, report.Usage.Cost),
	}, nil
}

func buildStatsReport(session sessionFile) statsReport {
	report := statsReport{
		Mode: "stats", Path: session.Path, Session: session.Metadata, Roles: map[string]int{},
		Messages: len(session.Messages), LineCount: session.LineCount, MalformedJSON: session.MalformedJSON,
	}
	type key struct{ provider, model string }
	models := map[key]*modelStat{}
	var first, last time.Time
	for _, msg := range session.Messages {
		report.Roles[msg.Role]++
		if msg.Role == "user" {
			report.Turns++
		}
		report.ToolCalls += len(msg.ToolCalls)
		if msg.IsError || (msg.Role == "assistant" && strings.EqualFold(msg.StopReason, "error")) {
			report.Errors++
		}
		report.Usage = addUsage(report.Usage, msg.Usage)
		if msg.Role == "assistant" {
			k := key{msg.Provider, msg.Model}
			if msg.Model == "" {
				k.model = "unknown"
			}
			m := models[k]
			if m == nil {
				m = &modelStat{Provider: k.provider, Model: k.model}
				models[k] = m
			}
			m.Messages++
			m.Usage = addUsage(m.Usage, msg.Usage)
		}
		if t, ok := parseTimestamp(msg.Timestamp); ok {
			if first.IsZero() || t.Before(first) {
				first, report.FirstTimestamp = t, msg.Timestamp
			}
			if last.IsZero() || t.After(last) {
				last, report.LastTimestamp = t, msg.Timestamp
			}
		}
	}
	if !first.IsZero() {
		report.DurationSeconds = int(last.Sub(first).Seconds())
	}
	for _, m := range models {
		report.Models = append(report.Models, *m)
	}
	slices.SortFunc(report.Models, func(a, b modelStat) int {
		if c := cmp.Compare(b.Messages, a.Messages); c != 0 {
			return c
		}
		return cmp.Compare(a.Model, b.Model)
	})
	return report
}

func parseTimestamp(ts string) (time.Time, bool) {
	if ts == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
		return t, true
	}
	if ms := floatValue(ts); ms > 0 {
		return time.UnixMilli(int64(ms)), true
	}
	return time.Time{}, false
}

func formatStatsMarkdown(report statsReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Session stats\n\n")
	fmt.Fprintf(&b, "- session: %s\n", emptyDash(shortID(report.Session.ID)))
	fmt.Fprintf(&b, "- cwd: %s\n", emptyDash(report.Session.CWD))
	fmt.Fprintf(&b, "- turns: %d\n", report.Turns)
	fmt.Fprintf(&b, "- messages: %d\n", report.Messages)
	roles := make([]string, 0, len(report.Roles))
	for role := range report.Roles {
		roles = append(roles, role)
	}
	slices.Sort(roles)
	for _, role := range roles {
		fmt.Fprintf(&b, "  - %s: %d\n", role, report.Roles[role])
	}
	fmt.Fprintf(&b, "- tool_calls: %d\n", report.ToolCalls)
	fmt.Fprintf(&b, "- errors: %d\n", report.Errors)
	if report.DurationSeconds > 0 {
		fmt.Fprintf(&b, "- duration: %s\n", (time.Duration(report.DurationSeconds) * time.Second).String())
	}
	u := report.Usage
	fmt.Fprintf(&b, "- tokens: in %d, out %d, cache read %d, cache write %d, total %d\n", u.Input, u.Output, u.CacheRead, u.CacheWrite, u.Total)
	fmt.Fprintf(&b, "- cost: $%.4f\n", u.Cost)
	if report.MalformedJSON > 0 {
		fmt.Fprintf(&b, "- malformed_json_lines: %d\n", report.MalformedJSON)
	}
	if len(report.Models) > 0 {
		fmt.Fprintf(&b, "\n| model | assistant messages | tokens | cost |\n|---|---:|---:|---:|\n")
		for _, m := range report.Models {
			name := m.Model
			if m.Provider != "" {
				name = m.Provider + "/" + m.Model
			}
			fmt.Fprintf(&b, "| %s | %d | %d | $%.4f |\n", markdownCell(name), m.Messages, m.Usage.Total, m.Usage.Cost)
		}
	}
	return b.String()
}
