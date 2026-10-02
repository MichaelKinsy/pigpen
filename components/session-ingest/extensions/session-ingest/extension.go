// Package sessioningest provides the read_session tool: token-safe queries over
// Pi and PiG JSONL session transcripts, in a compact indexed format.
package sessioningest

import (
	"bufio"
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Extension returns the session-ingest extension.
func Extension() *sdk.Extension {
	ext := sdk.New("session-ingest")

	ext.Tool(
		"read_session",
		"Read, query, and slice Pi/PiG JSONL session transcripts in a token-friendly format. Modes: toc (turn overview), query (text search), slice (message range), turn (one turn in full), tools (tool usage and files touched), stats (tokens, cost, roles, errors).",
		readSessionSchema(),
		handleReadSession,
	)

	return ext
}

func readSessionSchema() sdk.Schema {
	return sdk.Schema{
		"type":     "object",
		"required": []string{"path"},
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to a Pi or PiG .jsonl session file.",
			},
			"mode": map[string]any{
				"type":    "string",
				"enum":    []string{"toc", "query", "slice", "turn", "tools", "stats"},
				"default": "toc",
			},
			"query":         map[string]any{"type": "string"},
			"regex":         map[string]any{"type": "boolean", "default": false},
			"caseSensitive": map[string]any{"type": "boolean", "default": false},
			"turn":          map[string]any{"type": "integer", "minimum": 1, "description": "1-based turn number. Required for mode=turn; optional filter for mode=tools."},
			"start":         map[string]any{"type": "integer", "default": 0},
			"limit":         map[string]any{"type": "integer", "default": 20, "minimum": 1, "maximum": 200},
			"roles": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string", "enum": []string{"user", "assistant", "toolResult", "custom", "system"}},
			},
			"include": map[string]any{
				"type":    "array",
				"items":   map[string]any{"type": "string", "enum": []string{"text", "thinking", "tool_calls", "tool_results", "errors", "usage"}},
				"default": []string{"text", "tool_calls", "errors"},
			},
			"maxCharsPerItem": map[string]any{"type": "integer", "default": 0, "minimum": 0, "maximum": 50000, "description": "Max chars per message text block. 0 = no per-item truncation (default)."},
			"format":          map[string]any{"type": "string", "enum": []string{"compact_markdown", "json"}, "default": "compact_markdown"},
		},
	}
}

// Output truncation constants — match pig's built-in tool limits.
const (
	defaultMaxOutputBytes = 50 * 1024 // 50 KB
	defaultMaxOutputLines = 2000
)

func handleReadSession(ctx sdk.Context, params map[string]any) (any, error) {
	mode := stringParam(params, "mode", "toc")
	switch mode {
	case "toc", "query", "slice", "turn", "tools", "stats":
	default:
		return nil, sdk.NewToolError(fmt.Sprintf("unknown mode %q", mode))
	}

	path := stringParam(params, "path", "")
	if strings.TrimSpace(path) == "" {
		return nil, sdk.NewToolError("path is required")
	}
	// maxCharsPerItem: default 0 means no per-item truncation (let pig's TUI
	// handle collapse/expand). Callers can still pass a value to compress output.
	maxChars := intParam(params, "maxCharsPerItem", 0)
	start := intParam(params, "start", 0)
	if start < 0 {
		start = 0
	}
	limit := intParam(params, "limit", 20)
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}

	width := safeWidth(ctx)
	if width <= 0 {
		width = 120
	}

	// Query searches the full text: maxCharsPerItem bounds each excerpt, not what
	// is searched, so a match past the first maxCharsPerItem characters is found.
	parseChars := maxChars
	if mode == "query" {
		parseChars = 0
	}
	session, err := parseSessionFile(path, parseChars)
	if err != nil {
		return nil, sdk.NewToolError(err.Error())
	}
	format := stringParam(params, "format", "compact_markdown")
	if mode == "query" {
		query := stringParam(params, "query", "")
		excerptChars := maxChars
		if excerptChars <= 0 {
			excerptChars = max(200, width*2)
		}
		report, err := buildQueryReport(session, query, boolParam(params, "regex", false), boolParam(params, "caseSensitive", false), start, limit, excerptChars)
		if err != nil {
			return nil, sdk.NewToolError(err.Error())
		}
		if format == "json" {
			return report, nil
		}
		content := truncateOutput(formatQueryMarkdown(report, width), path, mode, start, limit, report.TotalHits)
		preview := queryPreview(report)
		return sdk.ToolResult{Content: content, Preview: preview}, nil
	}
	switch mode {
	case "turn":
		return handleTurn(session, params, format, path, start, limit, maxChars)
	case "tools":
		return handleTools(session, params, format, path, start, limit)
	case "stats":
		return handleStats(session, format, path)
	}
	if mode == "slice" {
		report := buildSliceReport(session, stringListParam(params, "roles"), start, limit)
		if format == "json" {
			return report, nil
		}
		content := truncateOutput(formatSliceMarkdown(report), path, mode, start, limit, report.FilteredTotal)
		preview := slicePreview(report)
		return sdk.ToolResult{Content: content, Preview: preview}, nil
	}
	report := buildTOCReport(session, start, limit)
	if format == "json" {
		return report, nil
	}
	content := truncateOutput(formatTOCMarkdown(report, width), path, mode, start, limit, report.TotalTurns)
	preview := tocPreview(report)
	return sdk.ToolResult{Content: content, Preview: preview}, nil
}

type sessionFile struct {
	Path          string
	Metadata      sessionMetadata
	Events        []sessionEvent
	Messages      []sessionMessage
	LineCount     int
	MalformedJSON int
}

type sessionMetadata struct {
	ID        string `json:"id,omitempty"`
	Version   string `json:"version,omitempty"`
	CWD       string `json:"cwd,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
}

type sessionEvent struct {
	Type          string `json:"type"`
	Provider      string `json:"provider,omitempty"`
	ModelID       string `json:"modelId,omitempty"`
	ThinkingLevel string `json:"thinkingLevel,omitempty"`
	Timestamp     string `json:"timestamp,omitempty"`
}

type sessionMessage struct {
	EntryIndex   int
	MessageIndex int
	Timestamp    string
	Role         string
	Texts        []string
	Thinking     []string
	ToolCalls    []toolCall
	ToolName     string
	IsError      bool
	ErrorMessage string
	Usage        usageSummary
	StopReason   string
	Model        string
	Provider     string
}

type toolCall struct {
	ID      string `json:"id,omitempty"`
	Name    string `json:"name"`
	Summary string `json:"summary,omitempty"`
	// Path is the file argument of a read, write or edit call.
	Path string `json:"path,omitempty"`
}

type usageSummary struct {
	Input      int     `json:"input,omitempty"`
	Output     int     `json:"output,omitempty"`
	CacheRead  int     `json:"cacheRead,omitempty"`
	CacheWrite int     `json:"cacheWrite,omitempty"`
	Total      int     `json:"total,omitempty"`
	Cost       float64 `json:"cost,omitempty"`
}

type tocReport struct {
	Mode          string          `json:"mode"`
	Path          string          `json:"path"`
	Session       sessionMetadata `json:"session"`
	Events        []sessionEvent  `json:"events,omitempty"`
	LineCount     int             `json:"lineCount"`
	MalformedJSON int             `json:"malformedJson,omitempty"`
	TotalMessages int             `json:"totalMessages"`
	TotalTurns    int             `json:"totalTurns"`
	Start         int             `json:"start"`
	Limit         int             `json:"limit"`
	Items         []tocItem       `json:"items"`
}

type tocItem struct {
	TurnIndex         int          `json:"turnIndex"`
	EntryIndex        int          `json:"entryIndex"`
	MessageIndex      int          `json:"messageIndex"`
	Timestamp         string       `json:"timestamp,omitempty"`
	User              string       `json:"user"`
	Tools             []string     `json:"tools,omitempty"`
	AssistantMessages int          `json:"assistantMessages"`
	ToolResults       int          `json:"toolResults"`
	Errors            int          `json:"errors,omitempty"`
	Usage             usageSummary `json:"usage,omitempty"`
}

type queryReport struct {
	Mode          string          `json:"mode"`
	Path          string          `json:"path"`
	Session       sessionMetadata `json:"session"`
	Query         string          `json:"query"`
	Regex         bool            `json:"regex"`
	CaseSensitive bool            `json:"caseSensitive"`
	TotalHits     int             `json:"totalHits"`
	Start         int             `json:"start"`
	Limit         int             `json:"limit"`
	Hits          []queryHit      `json:"hits"`
}

type queryHit struct {
	TurnIndex     int    `json:"turnIndex"`
	EntryIndex    int    `json:"entryIndex"`
	MessageIndex  int    `json:"messageIndex"`
	Timestamp     string `json:"timestamp,omitempty"`
	Role          string `json:"role"`
	ToolName      string `json:"toolName,omitempty"`
	Excerpt       string `json:"excerpt"`
	Score         int    `json:"score"`
	RetrievalHint string `json:"retrievalHint"`
}

type sliceReport struct {
	Mode          string          `json:"mode"`
	Path          string          `json:"path"`
	Session       sessionMetadata `json:"session"`
	TotalMessages int             `json:"totalMessages"`
	FilteredTotal int             `json:"filteredTotal"`
	Start         int             `json:"start"`
	Limit         int             `json:"limit"`
	Roles         []string        `json:"roles,omitempty"`
	Messages      []sliceMessage  `json:"messages"`
}

type sliceMessage struct {
	EntryIndex   int          `json:"entryIndex"`
	MessageIndex int          `json:"messageIndex"`
	TurnIndex    int          `json:"turnIndex,omitempty"`
	Timestamp    string       `json:"timestamp,omitempty"`
	Role         string       `json:"role"`
	Text         []string     `json:"text,omitempty"`
	ToolCalls    []toolCall   `json:"toolCalls,omitempty"`
	ToolName     string       `json:"toolName,omitempty"`
	IsError      bool         `json:"isError,omitempty"`
	Usage        usageSummary `json:"usage,omitempty"`
	StopReason   string       `json:"stopReason,omitempty"`
	Model        string       `json:"model,omitempty"`
	Provider     string       `json:"provider,omitempty"`
}

func parseSessionFile(path string, maxChars int) (sessionFile, error) {
	clean := filepath.Clean(path)
	file, err := os.Open(clean)
	if err != nil {
		return sessionFile{}, fmt.Errorf("read session %s: %w", clean, err)
	}
	defer file.Close()

	session := sessionFile{Path: clean}
	scanner := bufio.NewScanner(file)
	// Session entries can contain large tool outputs. Keep parsing, but cap the
	// scanner at 16 MiB so a single pathological line fails clearly.
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	messageIndex := 0
	for scanner.Scan() {
		session.LineCount++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			session.MalformedJSON++
			continue
		}
		typeName := stringValue(obj["type"])
		switch typeName {
		case "session":
			session.Metadata = sessionMetadata{
				ID:        stringValue(obj["id"]),
				Version:   stringValue(obj["version"]),
				CWD:       stringValue(obj["cwd"]),
				Timestamp: stringValue(obj["timestamp"]),
			}
		case "model_change", "thinking_level_change":
			session.Events = append(session.Events, sessionEvent{
				Type:          typeName,
				Provider:      stringValue(obj["provider"]),
				ModelID:       stringValue(obj["modelId"]),
				ThinkingLevel: stringValue(obj["thinkingLevel"]),
				Timestamp:     stringValue(obj["timestamp"]),
			})
		case "message":
			msg, ok := objectValue(obj["message"])
			if !ok {
				continue
			}
			messageIndex++
			session.Messages = append(session.Messages, parseMessage(obj, msg, session.LineCount, messageIndex, maxChars))
		}
	}
	if err := scanner.Err(); err != nil {
		return sessionFile{}, fmt.Errorf("read session %s: %w", clean, err)
	}
	return session, nil
}

func parseMessage(entry, msg map[string]any, entryIndex, messageIndex, maxChars int) sessionMessage {
	m := sessionMessage{
		EntryIndex:   entryIndex,
		MessageIndex: messageIndex,
		Timestamp:    firstString(entry["timestamp"], msg["timestamp"]),
		Role:         stringValue(msg["role"]),
		ToolName:     stringValue(msg["toolName"]),
		IsError:      boolValue(msg["isError"]),
		StopReason:   stringValue(msg["stopReason"]),
		ErrorMessage: stringValue(msg["errorMessage"]),
		Model:        stringValue(msg["model"]),
		Provider:     stringValue(msg["provider"]),
		Usage:        parseUsage(msg["usage"]),
	}
	m.Texts, m.Thinking, m.ToolCalls = parseContent(msg["content"], maxChars)
	if m.Role == "toolResult" && m.ToolName == "" && len(m.ToolCalls) > 0 {
		m.ToolName = m.ToolCalls[0].Name
	}
	return m
}

func parseContent(content any, maxChars int) (texts, thinking []string, tools []toolCall) {
	switch v := content.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return nil, nil, nil
		}
		return []string{truncate(v, maxChars)}, nil, nil
	case []any:
		for _, item := range v {
			obj, ok := objectValue(item)
			if !ok {
				continue
			}
			switch stringValue(obj["type"]) {
			case "text":
				text := strings.TrimSpace(stringValue(obj["text"]))
				if text != "" {
					texts = append(texts, truncate(text, maxChars))
				}
			case "thinking":
				text := strings.TrimSpace(stringValue(obj["thinking"]))
				if text != "" {
					thinking = append(thinking, truncate(text, maxChars))
				}
			case "toolCall":
				name := stringValue(obj["name"])
				if name != "" {
					call := toolCall{ID: stringValue(obj["id"]), Name: name}
					call.Summary, call.Path = summarizeToolArgs(name, obj["arguments"])
					tools = append(tools, call)
				}
			}
		}
	}
	return texts, thinking, tools
}

// fileTools are the built-in tools whose "path" argument names a file.
var fileTools = map[string]bool{"read": true, "write": true, "edit": true}

// summarizeToolArgs returns a short label for a tool call and, for file tools,
// the file it names. Other arguments are never copied into a report.
func summarizeToolArgs(name string, args any) (summary, path string) {
	obj, ok := objectValue(args)
	if !ok {
		return "", ""
	}
	if fileTools[name] {
		path = firstString(obj["path"], obj["file_path"], obj["filePath"])
		return path, path
	}
	if name == "bash" {
		return clipCell(stringValue(obj["command"]), 120), ""
	}
	return "", ""
}

func parseUsage(value any) usageSummary {
	obj, ok := objectValue(value)
	if !ok {
		return usageSummary{}
	}
	usage := usageSummary{
		Input:      intValue(obj["input"]),
		Output:     intValue(obj["output"]),
		CacheRead:  intValue(obj["cacheRead"]),
		CacheWrite: intValue(obj["cacheWrite"]),
		Total:      intValue(obj["totalTokens"]),
	}
	if usage.Total == 0 {
		usage.Total = intValue(obj["total"])
	}
	if cost, ok := objectValue(obj["cost"]); ok {
		usage.Cost = floatValue(cost["total"])
	} else {
		usage.Cost = floatValue(obj["cost"])
	}
	return usage
}

func buildTOCReport(session sessionFile, start, limit int) tocReport {
	items := buildTOCItems(session.Messages)
	end := min(len(items), start+limit)
	if start > len(items) {
		start = len(items)
		end = len(items)
	}
	return tocReport{
		Mode:          "toc",
		Path:          session.Path,
		Session:       session.Metadata,
		Events:        session.Events,
		LineCount:     session.LineCount,
		MalformedJSON: session.MalformedJSON,
		TotalMessages: len(session.Messages),
		TotalTurns:    len(items),
		Start:         start,
		Limit:         limit,
		Items:         items[start:end],
	}
}

func buildTOCItems(messages []sessionMessage) []tocItem {
	var items []tocItem
	var current *tocItem
	for _, msg := range messages {
		if msg.Role == "user" {
			items = append(items, tocItem{
				TurnIndex:    len(items) + 1,
				EntryIndex:   msg.EntryIndex,
				MessageIndex: msg.MessageIndex,
				Timestamp:    msg.Timestamp,
				User:         strings.Join(msg.Texts, " "),
			})
			current = &items[len(items)-1]
			continue
		}
		if current == nil {
			continue
		}
		switch msg.Role {
		case "assistant":
			current.AssistantMessages++
			for _, tool := range msg.ToolCalls {
				if tool.Name != "" && !slices.Contains(current.Tools, tool.Name) {
					current.Tools = append(current.Tools, tool.Name)
				}
			}
			current.Usage = addUsage(current.Usage, msg.Usage)
			if strings.EqualFold(msg.StopReason, "error") {
				current.Errors++
			}
		case "toolResult":
			current.ToolResults++
			if msg.ToolName != "" && !slices.Contains(current.Tools, msg.ToolName) {
				current.Tools = append(current.Tools, msg.ToolName)
			}
			if msg.IsError {
				current.Errors++
			}
		}
	}
	for i := range items {
		slices.Sort(items[i].Tools)
	}
	return items
}

func addUsage(a, b usageSummary) usageSummary {
	a.Input += b.Input
	a.Output += b.Output
	a.CacheRead += b.CacheRead
	a.CacheWrite += b.CacheWrite
	a.Total += b.Total
	a.Cost += b.Cost
	return a
}

func buildQueryReport(session sessionFile, query string, regexMode, caseSensitive bool, start, limit, maxChars int) (queryReport, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return queryReport{}, fmt.Errorf("query is required for mode=query")
	}
	matcher, err := newTextMatcher(query, regexMode, caseSensitive)
	if err != nil {
		return queryReport{}, err
	}
	var hits []queryHit
	turnIndex := 0
	for _, msg := range session.Messages {
		if msg.Role == "user" {
			turnIndex++
		}
		if turnIndex == 0 {
			continue
		}
		for _, text := range msg.Texts {
			if !matcher.matches(text) {
				continue
			}
			hits = append(hits, queryHit{
				TurnIndex:     turnIndex,
				EntryIndex:    msg.EntryIndex,
				MessageIndex:  msg.MessageIndex,
				Timestamp:     msg.Timestamp,
				Role:          msg.Role,
				ToolName:      msg.ToolName,
				Excerpt:       matcher.excerpt(text, maxChars),
				Score:         matcher.score(text, msg.Role),
				RetrievalHint: fmt.Sprintf("mode=turn turn=%d or mode=slice start=%d limit=1", turnIndex, msg.MessageIndex-1),
			})
		}
	}
	slices.SortFunc(hits, func(a, b queryHit) int {
		if byScore := cmp.Compare(b.Score, a.Score); byScore != 0 {
			return byScore
		}
		if byTurn := cmp.Compare(a.TurnIndex, b.TurnIndex); byTurn != 0 {
			return byTurn
		}
		return cmp.Compare(a.MessageIndex, b.MessageIndex)
	})
	end := min(len(hits), start+limit)
	if start > len(hits) {
		start = len(hits)
		end = len(hits)
	}
	return queryReport{
		Mode:          "query",
		Path:          session.Path,
		Session:       session.Metadata,
		Query:         query,
		Regex:         regexMode,
		CaseSensitive: caseSensitive,
		TotalHits:     len(hits),
		Start:         start,
		Limit:         limit,
		Hits:          hits[start:end],
	}, nil
}

type textMatcher struct {
	query         string
	needle        string
	regex         *regexp.Regexp
	caseSensitive bool
}

func newTextMatcher(query string, regexMode, caseSensitive bool) (textMatcher, error) {
	m := textMatcher{query: query, caseSensitive: caseSensitive}
	if regexMode {
		pattern := query
		if !caseSensitive {
			pattern = "(?i)" + pattern
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return textMatcher{}, fmt.Errorf("invalid regex query: %w", err)
		}
		m.regex = re
		return m, nil
	}
	if !caseSensitive {
		// Folding through the regexp engine keeps match offsets valid in the
		// original text (strings.ToLower can change byte lengths).
		m.regex = regexp.MustCompile("(?i)" + regexp.QuoteMeta(query))
		return m, nil
	}
	m.needle = query
	return m, nil
}

func (m textMatcher) matches(text string) bool {
	if m.regex != nil {
		return m.regex.MatchString(text)
	}
	haystack := text
	if !m.caseSensitive {
		haystack = strings.ToLower(text)
	}
	return strings.Contains(haystack, m.needle)
}

func (m textMatcher) score(text, role string) int {
	base := 1
	switch role {
	case "user":
		base = 30
	case "assistant":
		base = 20
	case "toolResult":
		base = 10
	}
	return base + m.count(text)
}

func (m textMatcher) count(text string) int {
	if m.regex != nil {
		return len(m.regex.FindAllStringIndex(text, -1))
	}
	haystack := text
	if !m.caseSensitive {
		haystack = strings.ToLower(text)
	}
	return strings.Count(haystack, m.needle)
}

func (m textMatcher) excerpt(text string, maxChars int) string {
	if maxChars <= 0 {
		maxChars = 1200
	}
	idx := m.firstIndex(text)
	if idx < 0 {
		return truncate(text, maxChars)
	}
	radius := max(80, maxChars/2)
	start := runeBoundary(text, max(0, idx-radius))
	end := runeBoundary(text, min(len(text), idx+len(m.query)+radius))
	prefix := ""
	if start > 0 {
		prefix = "... "
	}
	suffix := ""
	if end < len(text) {
		suffix = " ..."
	}
	return truncate(prefix+text[start:end]+suffix, maxChars)
}

func (m textMatcher) firstIndex(text string) int {
	if m.regex != nil {
		loc := m.regex.FindStringIndex(text)
		if loc == nil {
			return -1
		}
		return loc[0]
	}
	haystack := text
	needle := m.needle
	if !m.caseSensitive {
		haystack = strings.ToLower(text)
	}
	return strings.Index(haystack, needle)
}

func buildSliceReport(session sessionFile, roles []string, start, limit int) sliceReport {
	roleSet := map[string]bool{}
	for _, role := range roles {
		roleSet[role] = true
	}
	var messages []sliceMessage
	turnIndex := 0
	for _, msg := range session.Messages {
		if msg.Role == "user" {
			turnIndex++
		}
		if len(roleSet) > 0 && !roleSet[msg.Role] {
			continue
		}
		messages = append(messages, sliceMessage{
			EntryIndex:   msg.EntryIndex,
			MessageIndex: msg.MessageIndex,
			TurnIndex:    turnIndex,
			Timestamp:    msg.Timestamp,
			Role:         msg.Role,
			Text:         msg.Texts,
			ToolCalls:    msg.ToolCalls,
			ToolName:     msg.ToolName,
			IsError:      msg.IsError,
			Usage:        msg.Usage,
			StopReason:   msg.StopReason,
			Model:        msg.Model,
			Provider:     msg.Provider,
		})
	}
	end := min(len(messages), start+limit)
	if start > len(messages) {
		start = len(messages)
		end = len(messages)
	}
	return sliceReport{
		Mode:          "slice",
		Path:          session.Path,
		Session:       session.Metadata,
		TotalMessages: len(session.Messages),
		FilteredTotal: len(messages),
		Start:         start,
		Limit:         limit,
		Roles:         roles,
		Messages:      messages[start:end],
	}
}

// tocPreview builds a short collapsed preview for TOC results.
func tocPreview(report tocReport) string {
	return fmt.Sprintf("Session %s — %d turns, %d messages (showing %d)",
		shortID(report.Session.ID), report.TotalTurns, report.TotalMessages, len(report.Items))
}

// queryPreview builds a short collapsed preview for query results.
func queryPreview(report queryReport) string {
	return fmt.Sprintf("Query %q — %d hits (showing %d)",
		report.Query, report.TotalHits, len(report.Hits))
}

// slicePreview builds a short collapsed preview for slice results.
func slicePreview(report sliceReport) string {
	roles := ""
	if len(report.Roles) > 0 {
		roles = fmt.Sprintf(" (roles: %s)", strings.Join(report.Roles, ", "))
	}
	return fmt.Sprintf("Slice %d–%d of %d messages%s",
		report.Start, report.Start+len(report.Messages)-1, report.FilteredTotal, roles)
}

func formatTOCMarkdown(report tocReport, width int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Session TOC\n\n")
	fmt.Fprintf(&b, "- session: %s\n", emptyDash(shortID(report.Session.ID)))
	fmt.Fprintf(&b, "- cwd: %s\n", emptyDash(report.Session.CWD))
	fmt.Fprintf(&b, "- started: %s\n", emptyDash(report.Session.Timestamp))
	fmt.Fprintf(&b, "- turns: %d\n", report.TotalTurns)
	fmt.Fprintf(&b, "- messages: %d\n", report.TotalMessages)
	if report.MalformedJSON > 0 {
		fmt.Fprintf(&b, "- malformed_json_lines: %d\n", report.MalformedJSON)
	}
	// Allocate column widths based on terminal width.
	// Fixed cols: turn(6) + time(10) + tools(~25) + notes(~30) + separators(16) ≈ 87
	userWidth := max(20, width-87)

	fmt.Fprintf(&b, "\n| turn | time | user | tools | notes |\n")
	fmt.Fprintf(&b, "|---:|---|---|---|---|\n")
	for _, item := range report.Items {
		tools := "-"
		if len(item.Tools) > 0 {
			tools = strings.Join(item.Tools, ", ")
		}
		notes := []string{fmt.Sprintf("assistant:%d", item.AssistantMessages)}
		if item.ToolResults > 0 {
			notes = append(notes, fmt.Sprintf("results:%d", item.ToolResults))
		}
		if item.Errors > 0 {
			notes = append(notes, fmt.Sprintf("errors:%d", item.Errors))
		}
		if item.Usage.Cost > 0 {
			notes = append(notes, fmt.Sprintf("$%.4f", item.Usage.Cost))
		}
		userText := clipCell(item.User, userWidth)
		fmt.Fprintf(&b, "| %d | %s | %s | %s | %s |\n", item.TurnIndex, emptyDash(formatTimestamp(item.Timestamp)), markdownCell(userText), markdownCell(tools), strings.Join(notes, ", "))
	}
	return b.String()
}

func formatSliceMarkdown(report sliceReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Session slice\n\n")
	fmt.Fprintf(&b, "- session: %s\n", emptyDash(shortID(report.Session.ID)))
	fmt.Fprintf(&b, "- messages: %d of %d\n", len(report.Messages), report.FilteredTotal)
	fmt.Fprintf(&b, "- start: %d\n", report.Start)
	if len(report.Roles) > 0 {
		fmt.Fprintf(&b, "- roles: %s\n", strings.Join(report.Roles, ", "))
	}
	for _, msg := range report.Messages {
		fmt.Fprintf(&b, "\n## message %d turn %d %s\n\n", msg.MessageIndex, msg.TurnIndex, msg.Role)
		if msg.ToolName != "" {
			fmt.Fprintf(&b, "- tool: %s\n", msg.ToolName)
		}
		if msg.IsError {
			fmt.Fprintf(&b, "- error: true\n")
		}
		if len(msg.ToolCalls) > 0 {
			tools := make([]string, 0, len(msg.ToolCalls))
			for _, call := range msg.ToolCalls {
				tools = append(tools, call.Name)
			}
			fmt.Fprintf(&b, "- tool_calls: %s\n", strings.Join(tools, ", "))
		}
		for _, text := range msg.Text {
			fmt.Fprintf(&b, "\n%s\n", text)
		}
	}
	return b.String()
}

func formatQueryMarkdown(report queryReport, width int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Session query\n\n")
	fmt.Fprintf(&b, "- session: %s\n", emptyDash(shortID(report.Session.ID)))
	fmt.Fprintf(&b, "- query: %s\n", report.Query)
	fmt.Fprintf(&b, "- hits: %d\n", report.TotalHits)
	// Fixed cols: rank(6)+turn(6)+role(12)+tool(12)+score(7)+hint(~60)+seps(21) ≈ 124
	excerptWidth := max(30, width-124)

	fmt.Fprintf(&b, "\n| rank | turn | role | tool | score | excerpt | hint |\n")
	fmt.Fprintf(&b, "|---:|---:|---|---|---:|---|---|\n")
	for i, hit := range report.Hits {
		tool := hit.ToolName
		if tool == "" {
			tool = "-"
		}
		excerpt := clipCell(hit.Excerpt, excerptWidth)
		fmt.Fprintf(&b, "| %d | %d | %s | %s | %d | %s | `%s` |\n", report.Start+i+1, hit.TurnIndex, markdownCell(hit.Role), markdownCell(tool), hit.Score, markdownCell(excerpt), hit.RetrievalHint)
	}
	if len(report.Hits) == 0 {
		fmt.Fprintf(&b, "\nNo hits.\n")
	}
	return b.String()
}

func truncate(text string, maxLen int) string {
	text = strings.TrimSpace(text)
	if maxLen <= 0 {
		return text
	}
	total := utf8.RuneCountInString(text)
	if total <= maxLen {
		return text
	}
	return cutRunes(text, maxLen) + fmt.Sprintf(" ... [truncated, %d chars total]", total)
}

// cutRunes returns the first n runes of text, never splitting a character.
func cutRunes(text string, n int) string {
	for i := range text {
		if n == 0 {
			return text[:i]
		}
		n--
	}
	return text
}

// runeBoundary moves i back to the start of the rune that contains it.
func runeBoundary(text string, i int) int {
	for i > 0 && i < len(text) && !utf8.RuneStart(text[i]) {
		i--
	}
	return i
}

// truncateOutput applies pig's built-in 2000-line / 50KB safety net to the
// final markdown output string, matching bash/read/grep tool behavior. total is
// the number of pageable items (0 for modes that are not paged); a continuation
// hint is added only when the page stops before total or the output was cut.
func truncateOutput(output, path, mode string, start, limit, total int) string {
	lines := strings.Split(output, "\n")
	totalLines := len(lines)
	totalBytes := len(output)

	truncated := false
	if totalLines > defaultMaxOutputLines {
		lines = lines[:defaultMaxOutputLines]
		truncated = true
	}
	if joined := strings.Join(lines, "\n"); len(joined) > defaultMaxOutputBytes {
		joined = joined[:runeBoundary(joined, defaultMaxOutputBytes)]
		if last := strings.LastIndex(joined, "\n"); last > 0 {
			joined = joined[:last]
		}
		lines = strings.Split(joined, "\n")
		truncated = true
	}

	result := strings.Join(lines, "\n")
	hint := ""
	if total > 0 && start+limit < total {
		hint = fmt.Sprintf(`read_session {"path":%q, "mode":%q, "start":%d, "limit":%d}`, path, mode, start+limit, limit)
	}
	switch {
	case truncated && hint != "":
		result += fmt.Sprintf("\n\n[Showing %d of %d lines (%d bytes). Next: %s]", len(lines), totalLines, totalBytes, hint)
	case truncated && total > 0:
		result += fmt.Sprintf("\n\n[Showing %d of %d lines (%d bytes). Lower limit or set maxCharsPerItem to fit.]", len(lines), totalLines, totalBytes)
	case truncated:
		result += fmt.Sprintf("\n\n[Showing %d of %d lines (%d bytes). Narrow it with include or set maxCharsPerItem.]", len(lines), totalLines, totalBytes)
	case hint != "":
		result += fmt.Sprintf("\n\n[Next: %s]", hint)
	}
	return result
}

func markdownCell(text string) string {
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.ReplaceAll(text, "|", "\\|")
	return text
}

// clipCell truncates a markdown table cell to maxWidth characters.
func clipCell(text string, maxWidth int) string {
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.Join(strings.Fields(text), " ") // collapse whitespace
	if maxWidth <= 0 || utf8.RuneCountInString(text) <= maxWidth {
		return text
	}
	if maxWidth <= 4 {
		return cutRunes(text, maxWidth)
	}
	return cutRunes(text, maxWidth-3) + "..."
}

func shortID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12] + "..."
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func formatTimestamp(ts string) string {
	if ts == "" {
		return "-"
	}
	if parsed, err := time.Parse(time.RFC3339Nano, strings.ReplaceAll(ts, "Z", "+00:00")); err == nil {
		return parsed.Format("15:04:05")
	}
	if f := floatValue(ts); f > 0 {
		return time.UnixMilli(int64(f)).Format("15:04:05")
	}
	if len(ts) > 8 {
		return ts[:8]
	}
	return ts
}

func stringParam(params map[string]any, key, fallback string) string {
	if value, ok := params[key].(string); ok && value != "" {
		return value
	}
	return fallback
}

func intParam(params map[string]any, key string, fallback int) int {
	if value, ok := params[key]; ok {
		if n := intValue(value); n != 0 {
			return n
		}
	}
	return fallback
}

func boolParam(params map[string]any, key string, fallback bool) bool {
	if value, ok := params[key]; ok {
		if typed, ok := value.(bool); ok {
			return typed
		}
	}
	return fallback
}

func stringListParam(params map[string]any, key string) []string {
	value, ok := params[key]
	if !ok {
		return nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

func firstString(values ...any) string {
	for _, value := range values {
		if s := stringValue(value); s != "" {
			return s
		}
	}
	return ""
}

func stringValue(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case float64:
		return fmt.Sprintf("%.0f", v)
	default:
		return ""
	}
}

func boolValue(value any) bool {
	v, _ := value.(bool)
	return v
}

func intValue(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		i, _ := v.Int64()
		return int(i)
	case string:
		var i int
		_, _ = fmt.Sscanf(v, "%d", &i)
		return i
	default:
		return 0
	}
}

func floatValue(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case json.Number:
		f, _ := v.Float64()
		return f
	case string:
		var f float64
		_, _ = fmt.Sscanf(v, "%f", &f)
		return f
	default:
		return 0
	}
}

func objectValue(value any) (map[string]any, bool) {
	obj, ok := value.(map[string]any)
	return obj, ok
}

// safeWidth returns ctx.Width(), recovering from nil-context panics in tests.
func safeWidth(ctx sdk.Context) (w int) {
	defer func() {
		if r := recover(); r != nil {
			w = 0
		}
	}()
	return ctx.Width()
}
