package pi_subagents

// A child's live transcript and the progress facts the original keeps while it runs. The original's executor writes its
// own child transcript records (tool_start, tool_end, message; its readFleetTranscript parses them) and streams
// AgentProgress snapshots (its execution.ts) that the fleet inspector, the status views and `includeProgress` results
// show. The port runs a child `pig` process, whose session JSONL (docs: session-format.md) is that transcript: message
// entries appended as the child works. This file parses those entries into the original's transcript events, derives
// the AgentProgress facts from them, and flattens them for the status views (run-status.ts: formatLiveForegroundTranscript).

import (
	"encoding/json"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// The original's readFleetTranscript and execution.ts bounds.
const (
	transcriptMaxBytes        = 2 * 1024 * 1024 // readFleetTranscript's DEFAULT_MAX_BYTES
	transcriptMaxRecords      = 240             // its DEFAULT_MAX_RECORDS
	transcriptMaxMessageChars = 64 * 1024       // its MAX_MESSAGE_CHARS
	transcriptToolPreviewLine = 7               // its TOOL_PREVIEW_LINES
	transcriptRecentLines     = 10              // execution.ts: appendRecentOutput takes 10 lines per message
	transcriptRecentMax       = 50              // its recentOutput bound
	transcriptRecentToolsMax  = 32              // its MAX_STREAMED_RECENT_TOOLS
	transcriptLineChars       = 2000            // its MAX_STREAMED_OUTPUT_LINE_CHARS
)

// transcriptEvent is one event of a child's conversation (the original's FleetTranscriptEvent).
type transcriptEvent struct {
	kind      string // "assistant", "user", "tool", "notice"
	text      string // assistant/user/notice text
	model     string // the assistant message's model, when it carries one
	name      string // tool
	args      string // tool args preview (extractToolArgsPreview)
	argsRaw   string // tool args payload (the arguments as JSON text)
	path      string // the tool's file (resolveCurrentPath)
	output    string // tool result text
	errText   string // tool error text
	status    string // tool: "running", "complete", "error"
	toolID    string // the toolCall's id, for the result that completes it
	startedAt time.Time
	endedAt   time.Time
	matched   bool // a tool whose result was seen
}

// sessionTranscript is what one bounded read of a child's session file yields (the original's FleetTranscript).
type sessionTranscript struct {
	events      []transcriptEvent
	truncated   bool // the file tail was cut to the bound
	turns       int  // the assistant messages it saw (the original's usage.turns)
	tokens      int64
	tokensKnown bool   // any message reported usage
	model       string // the last assistant message's model
}

// readSessionTranscript reads a child's session JSONL tail: the last transcriptMaxBytes bytes, the last
// transcriptMaxRecords entries (readFleetTranscript's bounds). The path is one the port pinned for its own child; a
// missing file is simply empty.
func readSessionTranscript(path string) sessionTranscript {
	if path == "" {
		return sessionTranscript{}
	}
	f, err := os.Open(path)
	if err != nil {
		return sessionTranscript{}
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return sessionTranscript{}
	}
	size := info.Size()
	start := int64(0)
	if size > transcriptMaxBytes {
		start = size - transcriptMaxBytes
	}
	buf := make([]byte, size-start)
	n, err := f.ReadAt(buf, start)
	if err != nil && n == 0 {
		return sessionTranscript{}
	}
	buf = buf[:n]
	t := sessionTranscript{truncated: start > 0}
	lines := strings.Split(strings.ReplaceAll(string(buf), "\r\n", "\n"), "\n")
	if start > 0 && len(lines) > 0 {
		lines = lines[1:] // the first line is the cut remainder of one record
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	// a live file can end mid-record; an unparseable last line is that record, not a malformed one
	for len(lines) > 0 && !json.Valid([]byte(lines[len(lines)-1])) {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > transcriptMaxRecords {
		lines = lines[len(lines)-transcriptMaxRecords:]
		t.truncated = true
	}
	parsed := parseSessionEntries(lines)
	parsed.truncated = t.truncated
	return parsed
}

// parseSessionEntries turns session entries into transcript events (the original's parseTranscriptLines). A malformed
// entry is skipped: a live file heals on the next read.
func parseSessionEntries(lines []string) sessionTranscript {
	var t sessionTranscript
	assistantSeen := false
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record struct {
			Type      string          `json:"type"`
			Timestamp string          `json:"timestamp"`
			Message   json.RawMessage `json:"message"`
		}
		if json.Unmarshal([]byte(line), &record) != nil || record.Type != "message" {
			continue
		}
		at := parseEntryTime(record.Timestamp)
		var msg struct {
			Role    string `json:"role"`
			Content json.RawMessage
			Text    string `json:"text"`
			Model   string `json:"model"`
			Usage   *struct {
				TotalTokens int64 `json:"totalTokens"`
			} `json:"usage"`
			StopReason   string `json:"stopReason"`
			ErrorMessage string `json:"errorMessage"`
			ToolCallID   string `json:"toolCallId"`
			ToolName     string `json:"toolName"`
			IsError      bool   `json:"isError"`
			Timestamp    int64  `json:"timestamp"`
		}
		if json.Unmarshal(record.Message, &msg) != nil {
			continue
		}
		if at.IsZero() && msg.Timestamp > 0 {
			at = time.UnixMilli(msg.Timestamp)
		}
		switch msg.Role {
		case "user":
			text := clipTranscriptMessage(messageText(msg.Content, msg.Text))
			if !assistantSeen || text == "" {
				continue
			}
			t.events = appendTextEvent(t.events, "user", text, "", at)
		case "assistant":
			assistantSeen = true
			t.turns++ // every assistant message is a turn (execution.ts: usage.turns)
			if msg.Model != "" {
				t.model = msg.Model
			}
			if msg.Usage != nil && msg.Usage.TotalTokens > 0 {
				t.tokensKnown = true
				t.tokens += msg.Usage.TotalTokens
			}
			text := clipTranscriptMessage(messageText(msg.Content, msg.Text))
			if text != "" {
				t.events = appendTextEvent(t.events, "assistant", text, msg.Model, at)
			}
			for _, call := range messageToolCalls(msg.Content) {
				t.events = append(t.events, newToolEvent(call, at))
			}
			if (msg.StopReason == "error" || msg.StopReason == "aborted") && msg.ErrorMessage != "" {
				t.events = append(t.events, transcriptEvent{kind: "notice", text: clipTranscriptMessage(msg.ErrorMessage)})
			}
		case "toolResult", "tool_result":
			idx := findToolEvent(t.events, msg.ToolCallID, msg.ToolName)
			if idx < 0 {
				t.events = append(t.events, transcriptEvent{kind: "tool", name: msg.ToolName, status: "complete"})
				idx = len(t.events) - 1
			}
			tool := &t.events[idx]
			text := clipTranscriptMessage(messageText(msg.Content, msg.Text))
			if !tool.matched {
				tool.matched = true
				tool.endedAt = at
				if msg.IsError {
					tool.status = "error"
					tool.errText = firstLine(text)
				} else {
					tool.status = "complete"
				}
				if text != "" && (!msg.IsError || tool.output == "") {
					tool.output = text
				}
			}
		}
	}
	for i := range t.events {
		if t.events[i].kind == "tool" && !t.events[i].matched {
			t.events[i].status = "running"
		}
		t.events[i] = safeTranscriptEvent(t.events[i])
	}
	return t
}

// safeTranscriptEvent strips terminal escapes and control bytes from the strings an event will be drawn with (the
// original's safeTranscriptEvent, applied when a transcript is read). A child's session text is not display text: the
// fleet inspector writes it into one row of an alt-screen frame, where a control byte moves the terminal cursor
// instead of printing. sanitizeDisplay keeps "\n" and "\t" the way the original's safeTerminalText does; wrapPlain
// turns those into row breaks when it wraps.
func safeTranscriptEvent(e transcriptEvent) transcriptEvent {
	e.text = sanitizeDisplay(e.text)
	e.model = sanitizeDisplay(e.model)
	e.name = sanitizeDisplay(e.name)
	e.args = sanitizeDisplay(e.args)
	e.output = sanitizeDisplay(e.output)
	e.errText = sanitizeDisplay(e.errText)
	return e
}

// toolCall is one toolCall content block.
type toolCall struct {
	id, name string
	args     any // the arguments object, or the legacy string form
	argsRaw  string
}

// messageToolCalls is the assistant message's toolCall blocks (a legacy `tool_use` block with `input` is the same).
func messageToolCalls(content json.RawMessage) []toolCall {
	var blocks []struct {
		Type      string          `json:"type"`
		ID        string          `json:"id"`
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
		Input     json.RawMessage `json:"input"`
	}
	if json.Unmarshal(content, &blocks) != nil {
		return nil
	}
	var calls []toolCall
	for _, b := range blocks {
		if b.Type != "toolCall" && b.Type != "tool_use" {
			continue
		}
		raw := b.Arguments
		if len(raw) == 0 {
			raw = b.Input
		}
		c := toolCall{id: b.ID, name: b.Name, argsRaw: jsonRawText(raw)}
		if len(raw) > 0 {
			if json.Unmarshal(raw, &c.args) != nil {
				c.args = nil
			}
		}
		calls = append(calls, c)
	}
	return calls
}

// messageText is the message's text: its text blocks, or the plain string form.
func messageText(content json.RawMessage, plain string) string {
	if len(content) > 0 {
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(content, &blocks) == nil {
			var parts []string
			for _, b := range blocks {
				if b.Type == "text" && b.Text != "" {
					parts = append(parts, b.Text)
				}
			}
			if len(parts) > 0 {
				return strings.Join(parts, "\n")
			}
		}
		var s string
		if json.Unmarshal(content, &s) == nil {
			return s
		}
	}
	return plain
}

// findToolEvent is the original's findTool: the index of the event with this toolCall id, else the latest tool still
// waiting for a result under this name, else -1 (a result with no call opens its own event).
func findToolEvent(events []transcriptEvent, id, name string) int {
	if id != "" {
		for i := len(events) - 1; i >= 0; i-- {
			if events[i].kind == "tool" && events[i].toolID == id {
				return i
			}
		}
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].kind == "tool" && !events[i].matched && (name == "" || events[i].name == name) {
			return i
		}
	}
	return -1
}

// newToolEvent is a toolCall block as a running tool event (the original's tool_start record).
func newToolEvent(call toolCall, at time.Time) transcriptEvent {
	name := call.name
	if name == "" {
		name = "tool"
	}
	return transcriptEvent{
		kind: "tool", name: name, toolID: call.id,
		args: toolArgsPreview(call.args), argsRaw: call.argsRaw,
		path:   toolArgsPath(name, call.args),
		status: "running", startedAt: at,
	}
}

// appendTextEvent adds an assistant/user event, skipping the duplicate of itself a retried message can write (the
// original's appendTextEvent).
func appendTextEvent(events []transcriptEvent, kind, text, model string, at time.Time) []transcriptEvent {
	if text == "" {
		return events
	}
	if n := len(events); n > 0 && events[n-1].kind == kind && events[n-1].text == text {
		return events
	}
	return append(events, transcriptEvent{kind: kind, text: text, model: model, startedAt: at})
}

// clipTranscriptMessage is the original's clipMessage: a message is kept whole up to its bound, then marked.
func clipTranscriptMessage(text string) string {
	if len(text) <= transcriptMaxMessageChars {
		return text
	}
	cut := transcriptMaxMessageChars
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "\n\n… message truncated"
}

// parseEntryTime is a session entry's ISO timestamp.
func parseEntryTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if at, err := time.Parse(layout, s); err == nil {
			return at
		}
	}
	return time.Time{}
}

// jsonRawText is raw JSON text as it was written, or "".
func jsonRawText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// ---- the tool args display (the original's extractToolArgsPreview and resolveCurrentPath) ----

// toolArgsPreview is the one-line args display (the original's extractToolArgsPreview).
func toolArgsPreview(args any) string {
	obj, ok := args.(map[string]any)
	if !ok {
		if s, isString := args.(string); isString {
			return truncateRunes(s, 60)
		}
		return ""
	}
	str := func(key string) string {
		s, _ := obj[key].(string)
		return s
	}
	if tool := str("tool"); tool != "" {
		server := ""
		if s := str("server"); s != "" {
			server = s + "/"
		}
		extra := ""
		if s := str("args"); s != "" {
			extra = " " + truncateRunes(sanitizeDisplay(s), 40)
		}
		return sanitizeDisplay(server + tool + extra)
	}
	if p := arrayPreview(obj["queries"]); p != "" {
		return truncateRunes(p, 60)
	}
	if s := str("query"); strings.TrimSpace(s) != "" {
		return truncateRunes(s, 60)
	}
	if s := str("workflow"); strings.TrimSpace(s) != "" {
		return "workflow=" + truncateRunes(s, 48)
	}
	for _, key := range []string{"url", "prompt"} {
		if s := str(key); strings.TrimSpace(s) != "" {
			return truncateRunes(s, 60)
		}
	}
	if p := arrayPreview(obj["urls"]); p != "" {
		return truncateRunes(p, 60)
	}
	for _, key := range []string{"command", "path", "file_path", "pattern", "query", "url", "task", "describe", "search"} {
		if s := str(key); s != "" {
			return truncateRunes(s, 60)
		}
	}
	for key, value := range obj {
		if p := arrayPreview(value); p != "" {
			return sanitizeDisplay(key) + "=" + truncateRunes(p, 50)
		}
		if s, isString := value.(string); isString && s != "" {
			return sanitizeDisplay(key) + "=" + truncateRunes(s, 50)
		}
	}
	return ""
}

// arrayPreview is an array's first value and a count of the rest (the original's previewArray).
func arrayPreview(value any) string {
	list, ok := value.([]any)
	if !ok || len(list) == 0 {
		return ""
	}
	first := ""
	switch v := list[0].(type) {
	case string:
		if strings.TrimSpace(v) != "" {
			first = sanitizeDisplay(v)
		}
	case float64:
		first = strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		first = strconv.FormatBool(v)
	}
	if first == "" {
		return ""
	}
	if len(list) > 1 {
		return first + " (+" + strconv.Itoa(len(list)-1) + " more)"
	}
	return first
}

// bashRedirect matches the file a bash command redirects or tees to (the original's resolveCurrentPath).
var bashRedirect = regexp.MustCompile(`(?:>|>>|tee\s+)(\S+)`)

// toolArgsPath is the file an args object names (the original's resolveCurrentPath).
func toolArgsPath(toolName string, args any) string {
	obj, ok := args.(map[string]any)
	if !ok {
		return ""
	}
	for _, key := range []string{"path", "file", "filename", "target", "cwd"} {
		if s, _ := obj[key].(string); strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	if toolName == "bash" {
		if command, _ := obj["command"].(string); command != "" {
			if m := bashRedirect.FindStringSubmatch(command); m != nil {
				return m[1]
			}
		}
	}
	return ""
}

// sanitizeDisplay strips terminal escapes and control bytes from a display string (the original's safeDisplayText).
func sanitizeDisplay(s string) string {
	s = sgrSequence.ReplaceAllString(s, "")
	return strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\n' && r != '\t' {
			return -1
		}
		if r == 0x7f {
			return -1
		}
		return r
	}, s)
}

// truncateRunes cuts a display string to n characters, marking the cut (the original's truncateDisplayText).
func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

// ---- the status view's live transcript tail (run-status.ts: formatLiveForegroundTranscript) ----

// transcriptTailLines flattens the events the way the original's live transcript view does: one block per event, the
// last lineLimit lines, at most 32 KiB. The bool reports the tail's truncation.
func transcriptTailLines(t sessionTranscript, lineLimit int) (string, bool) {
	var all []string
	for _, e := range t.events {
		var block string
		switch e.kind {
		case "tool":
			parts := []string{"Tool: " + e.name + " (" + e.status + ")"}
			if e.argsRaw != "" {
				parts = append(parts, e.argsRaw)
			} else if e.args != "" {
				parts = append(parts, e.args)
			}
			if e.output != "" {
				parts = append(parts, e.output)
			} else if e.errText != "" {
				parts = append(parts, e.errText)
			}
			block = strings.Join(parts, "\n")
		case "notice":
			block = e.text
		case "assistant":
			block = "Assistant: " + e.text
		default:
			block = "Supervisor: " + e.text
		}
		all = append(all, strings.Split(strings.ReplaceAll(block, "\r\n", "\n"), "\n")...)
	}
	truncated := t.truncated
	if lineLimit < 1 {
		lineLimit = 1
	}
	if len(all) > lineLimit {
		all = all[len(all)-lineLimit:]
		truncated = true
	}
	body := strings.Join(all, "\n")
	if strings.Contains(body, "… message truncated") {
		truncated = true
	}
	const maxBodyBytes = 32 * 1024 // the original's MAX_TRANSCRIPT_BODY_BYTES
	if len(body) > maxBodyBytes {
		start := len(body) - maxBodyBytes
		for start < len(body) && body[start]&0xc0 == 0x80 {
			start++ // keep UTF-8 whole
		}
		body = body[start:]
		truncated = true
	}
	return body, truncated
}

// ---- the progress facts (the original's AgentProgress, execution.ts) ----

// progressTool is one finished tool in the progress record (AgentProgress.recentTools).
type progressTool struct {
	Tool  string `json:"tool"`
	Args  string `json:"args"`
	EndMs int64  `json:"endMs"`
}

// progressFacts is what a transcript (or the live output, when there is none) says about a child's work.
type progressFacts struct {
	currentTool          string
	currentToolArgs      string
	currentToolStartedAt int64 // Unix ms, 0 unknown
	currentPath          string
	recentTools          []progressTool
	recentOutput         []string
	toolCount            int
	turnCount            int
	tokens               int64
	tokensKnown          bool
	model                string
}

// factsFromTranscript derives the facts (execution.ts's tool_execution/message_end handlers).
func factsFromTranscript(t sessionTranscript) progressFacts {
	f := progressFacts{turnCount: t.turns, tokens: t.tokens, tokensKnown: t.tokensKnown, model: t.model}
	for _, e := range t.events {
		switch e.kind {
		case "assistant":
			appendRecentOutput(&f.recentOutput, e.text)
		case "tool":
			f.toolCount++
			if e.matched {
				endMs := int64(0)
				if !e.endedAt.IsZero() {
					endMs = e.endedAt.UnixMilli()
				}
				f.recentTools = append(f.recentTools, progressTool{Tool: e.name, Args: e.args, EndMs: endMs})
				if len(f.recentTools) > transcriptRecentToolsMax {
					f.recentTools = f.recentTools[len(f.recentTools)-transcriptRecentToolsMax:]
				}
				appendRecentOutput(&f.recentOutput, e.output)
			}
		}
	}
	// the current tool is the latest call still waiting for its result (execution.ts: latestActiveToolCall)
	for i := len(t.events) - 1; i >= 0; i-- {
		e := t.events[i]
		if e.kind == "tool" && !e.matched {
			f.currentTool = e.name
			f.currentToolArgs = e.args
			f.currentPath = e.path
			if !e.startedAt.IsZero() {
				f.currentToolStartedAt = e.startedAt.UnixMilli()
			}
			break
		}
	}
	return f
}

// appendRecentOutput is the original's: the message's last lines, bounded (execution.ts: appendRecentOutput).
func appendRecentOutput(lines *[]string, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	all := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var kept []string
	for _, l := range all {
		if strings.TrimSpace(l) != "" {
			kept = append(kept, l)
		}
	}
	if len(kept) > transcriptRecentLines {
		kept = kept[len(kept)-transcriptRecentLines:]
	}
	*lines = append(*lines, kept...)
	if len(*lines) > transcriptRecentMax {
		*lines = (*lines)[len(*lines)-transcriptRecentMax:]
	}
	for i, l := range *lines {
		if len(l) > transcriptLineChars {
			(*lines)[i] = l[:transcriptLineChars] + "… [truncated]"
		}
	}
}

// progressRecord is the result's progress record (the original's AgentProgress): the fields the port can fill, its
// required arrays always present, the fields only a live child session can report omitted (never invented).
type progressRecord struct {
	Index                int            `json:"index"`
	Agent                string         `json:"agent"`
	Status               string         `json:"status"`
	Task                 string         `json:"task"`
	LastActivityAt       int64          `json:"lastActivityAt,omitempty"`
	CurrentTool          string         `json:"currentTool,omitempty"`
	CurrentToolArgs      string         `json:"currentToolArgs,omitempty"`
	CurrentToolStartedAt int64          `json:"currentToolStartedAt,omitempty"`
	CurrentPath          string         `json:"currentPath,omitempty"`
	RecentTools          []progressTool `json:"recentTools"`
	RecentOutput         []string       `json:"recentOutput"`
	ToolCount            int            `json:"toolCount"`
	TurnCount            *int           `json:"turnCount,omitempty"`
	Tokens               *int64         `json:"tokens,omitempty"`
	Model                string         `json:"model,omitempty"`
	Thinking             string         `json:"thinking,omitempty"`
	DurationMs           int64          `json:"durationMs"`
	Error                string         `json:"error,omitempty"`
}

// progressStatus is the original's AgentProgress status vocabulary for a step's state.
func progressStatus(s runState) string {
	switch s {
	case stateQueued:
		return "pending"
	case stateRunning:
		return "running"
	case stateComplete:
		return "completed"
	}
	return "failed" // failed, stopped or paused: the attempt did not complete
}

// progressRecordOf is the step's progress record: its facts plus what the transcript (else the live output) shows.
func progressRecordOf(s *stepRecord, index int, now time.Time, launchModel string) progressRecord {
	rec := progressRecord{
		Index:        index,
		Agent:        s.agent,
		Status:       progressStatus(s.state),
		Task:         s.task,
		Thinking:     s.thinking,
		Error:        s.err,
		RecentTools:  []progressTool{},
		RecentOutput: []string{},
	}
	end := s.endedAt
	if end.IsZero() {
		end = now
	}
	if !s.startedAt.IsZero() {
		rec.DurationMs = max64(0, end.Sub(s.startedAt).Milliseconds())
		if last := s.lastActivity; !last.IsZero() {
			rec.LastActivityAt = last.UnixMilli()
		} else {
			rec.LastActivityAt = s.startedAt.UnixMilli()
		}
	}
	facts := factsFromTranscript(readSessionTranscript(s.sessionPath()))
	if facts.toolCount == 0 && facts.turnCount == 0 && s.output != "" {
		// no transcript (a child without a pinned session): its output is the only progress there is
		appendRecentOutput(&facts.recentOutput, s.output)
	}
	rec.CurrentTool = facts.currentTool
	rec.CurrentToolArgs = facts.currentToolArgs
	rec.CurrentToolStartedAt = facts.currentToolStartedAt
	rec.CurrentPath = facts.currentPath
	if facts.recentTools != nil {
		rec.RecentTools = facts.recentTools
	}
	if facts.recentOutput != nil {
		rec.RecentOutput = facts.recentOutput
	}
	rec.ToolCount = facts.toolCount
	if facts.turnCount > 0 {
		turns := facts.turnCount
		rec.TurnCount = &turns
	}
	if facts.tokensKnown {
		tokens := facts.tokens
		rec.Tokens = &tokens
	}
	rec.Model = facts.model
	if rec.Model == "" {
		rec.Model = launchModel
	}
	return rec
}

// detachedProgressRecord is an async launch's progress record: the child is detached and has not produced anything yet
// (the original's detach receipt sets its progress status to "detached").
func detachedProgressRecord(a *AgentConfig, task, model string) progressRecord {
	return progressRecord{
		Agent:        a.Name,
		Status:       "detached",
		Task:         task,
		Thinking:     thinkingLevelFor(a, model),
		RecentTools:  []progressTool{},
		RecentOutput: []string{},
		Model:        effectiveModel(a, model),
	}
}
