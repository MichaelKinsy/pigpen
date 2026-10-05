package acp

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/MichaelKinsy/pigpen/acp/cmd/pig-acp/internal/pirpc"
)

// This file ports src/acp/session.ts of pi-acp: one ACP session bound to one pig RPC child, the
// translation of pig events into `session/update` notifications, the turn queue, and the mapping
// of extension dialogs onto `session/request_permission`.

// SessionOptions create a Session (PiAcpSession's constructor arguments).
type SessionOptions struct {
	SessionID    string
	Cwd          string
	McpServers   []any
	Proc         Proc
	Conn         Conn
	FileCommands []FileSlashCommand
}

const (
	choiceOptionPrefix = "choice-"
)

var extensionUIRawInputKeys = []string{"title", "message", "options", "placeholder", "prefill"}

// serial runs functions one at a time, in the order they were submitted, without ever blocking
// the submitter. It is the Go form of a promise chain (lastEmit in the original).
type serial struct {
	mu   sync.Mutex
	cond *sync.Cond
	q    []func()
	busy bool
}

func newSerial() *serial {
	s := &serial{}
	s.cond = sync.NewCond(&s.mu)
	return s
}

func (s *serial) do(fn func()) {
	s.mu.Lock()
	s.q = append(s.q, fn)
	if !s.busy {
		s.busy = true
		go s.run()
	}
	s.mu.Unlock()
}

func (s *serial) run() {
	for {
		s.mu.Lock()
		if len(s.q) == 0 {
			s.busy = false
			s.cond.Broadcast()
			s.mu.Unlock()
			return
		}
		fn := s.q[0]
		s.q = s.q[1:]
		s.mu.Unlock()
		fn()
	}
}

// wait blocks until everything submitted so far has run.
func (s *serial) wait() {
	s.mu.Lock()
	for s.busy || len(s.q) > 0 {
		s.cond.Wait()
	}
	s.mu.Unlock()
}

type queuedTurn struct {
	message string
	images  []Image
	result  chan TurnResult
}

type fileSnapshot struct {
	path    string
	oldText *string
}

// Session is one ACP session bound to one pig child.
type Session struct {
	id         string
	cwd        string
	mcpServers []any
	proc       Proc
	conn       Conn
	cmds       []FileSlashCommand

	emitQ *serial // session/update notifications, in order
	ackQ  *serial // acknowledgements of fire-and-forget extension dialogs, in order

	mu               sync.Mutex
	startupInfo      *string
	startupSent      bool
	cancelRequested  bool
	pending          *queuedTurn
	queue            []*queuedTurn
	currentToolCalls map[string]string // pending | in_progress, never downgraded
	inAgentLoop      bool
	fileSnapshots    map[string]fileSnapshot
	fileMutation     map[string]bool
	bashToolCalls    map[string]bool
	bashSnapshots    map[string]string
}

// NewSession subscribes to the child's events and returns the session.
func NewSession(opts SessionOptions) *Session {
	s := &Session{
		id: opts.SessionID, cwd: opts.Cwd, mcpServers: opts.McpServers, proc: opts.Proc, conn: opts.Conn, cmds: opts.FileCommands,
		emitQ: newSerial(), ackQ: newSerial(),
		currentToolCalls: map[string]string{}, fileSnapshots: map[string]fileSnapshot{}, fileMutation: map[string]bool{},
		bashToolCalls: map[string]bool{}, bashSnapshots: map[string]string{},
	}
	s.proc.OnEvent(s.handlePiEvent)
	return s
}

// ID is the ACP session id.
func (s *Session) ID() string { return s.id }

// Cwd is the session working directory.
func (s *Session) Cwd() string { return s.cwd }

// Proc is the pig child.
func (s *Session) Proc() Proc { return s.proc }

// SetStartupInfo records the startup text to send once.
func (s *Session) SetStartupInfo(text string) {
	s.mu.Lock()
	s.startupInfo = &text
	s.startupSent = false
	s.mu.Unlock()
}

// SendStartupInfoIfPending sends the startup text if it was not sent yet. Some clients render
// agent messages only once their UI is ready, so the agent calls this shortly after session/new.
func (s *Session) SendStartupInfoIfPending() {
	s.mu.Lock()
	if s.startupSent || s.startupInfo == nil || *s.startupInfo == "" {
		s.mu.Unlock()
		return
	}
	s.startupSent = true
	text := *s.startupInfo
	s.mu.Unlock()
	s.emit(Update{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": text}})
}

func textChunk(kind, text string) Update {
	return Update{"sessionUpdate": kind, "content": map[string]any{"type": "text", "text": text}}
}

func queueInfo(depth int, running bool) Update {
	return Update{"sessionUpdate": "session_info_update", "_meta": map[string]any{"piAcp": map[string]any{"queueDepth": depth, "running": running}}}
}

// Prompt starts a turn now, or queues it behind the running one. The channel yields once.
func (s *Session) Prompt(message string, images []Image) <-chan TurnResult {
	// The startup text goes out with the first prompt when nothing sent it earlier.
	s.SendStartupInfoIfPending()
	// pig's RPC mode does not expand prompt templates, so it is done here.
	t := &queuedTurn{message: ExpandSlashCommand(message, s.cmds), images: images, result: make(chan TurnResult, 1)}

	s.mu.Lock()
	if s.pending != nil {
		s.queue = append(s.queue, t)
		depth := len(s.queue)
		s.mu.Unlock()
		s.emit(textChunk("agent_message_chunk", fmt.Sprintf("Queued message (position %d).", depth)))
		s.emit(queueInfo(depth, true))
		return t.result
	}
	s.mu.Unlock()
	s.startTurn(t)
	return t.result
}

// Cancel aborts the running turn and clears the queue.
func (s *Session) Cancel() error {
	s.mu.Lock()
	s.cancelRequested = true
	queued := s.queue
	s.queue = nil
	running := s.pending != nil
	s.mu.Unlock()
	if len(queued) > 0 {
		for _, t := range queued {
			t.result <- TurnResult{Reason: StopCancelled}
		}
		s.emit(textChunk("agent_message_chunk", "Cleared queued prompts."))
		s.emit(queueInfo(0, running))
	}
	// If nothing is running, aborting is a no-op.
	return s.proc.Abort()
}

// WasCancelRequested reports whether the running turn was cancelled.
func (s *Session) WasCancelRequested() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancelRequested
}

func (s *Session) emit(update Update) {
	id, conn := s.id, s.conn
	s.emitQ.do(func() {
		// A failed notification (the client went away) must not stop the turn.
		_ = conn.SessionUpdate(id, update)
	})
}

func (s *Session) flushEmits() { s.emitQ.wait() }

// toUsageUpdate maps stats.contextUsage to a usage_update, or nil when pig reports no
// trustworthy token count (tokens null right after compaction) or unusable numbers.
func toUsageUpdate(stats SessionStats) Update {
	cu := asObject(stats["contextUsage"])
	used, ok1 := safeInteger(cu["tokens"])
	size, ok2 := safeInteger(cu["contextWindow"])
	if !ok1 || used < 0 || !ok2 || size <= 0 {
		return nil
	}
	return Update{"sessionUpdate": "usage_update", "used": used, "size": size}
}

func safeInteger(v any) (int64, bool) {
	f, ok := asNumber(v)
	if !ok || f != math.Trunc(f) || math.Abs(f) > 9007199254740991 {
		return 0, false
	}
	return int64(f), true
}

// PublishContextUsage sends a usage_update from get_session_stats and flushes updates. It never
// fails or delays the turn beyond the stats timeout: context usage is auxiliary.
func (s *Session) PublishContextUsage() {
	if stats, err := s.proc.GetSessionStats(SessionStatsTimeoutMs); err == nil {
		if u := toUsageUpdate(stats); u != nil {
			s.emit(u)
		}
	}
	s.flushEmits()
}

func (s *Session) settleTurn() {
	// Every update derived from pig events, plus the final usage update, is delivered before the
	// ACP session/prompt request resolves.
	s.PublishContextUsage()

	s.mu.Lock()
	reason := StopEndTurn
	if s.cancelRequested {
		reason = StopCancelled
	}
	pending := s.pending
	s.pending = nil
	s.inAgentLoop = false
	var next *queuedTurn
	if len(s.queue) > 0 {
		next, s.queue = s.queue[0], s.queue[1:]
	}
	remaining := len(s.queue)
	s.mu.Unlock()
	if next == nil {
		// The idle notice goes out before the response, so a client that stops listening at the
		// response has seen the whole turn.
		s.emit(queueInfo(0, false))
		s.flushEmits()
	}
	if pending != nil {
		pending.result <- TurnResult{Reason: reason}
	}
	if next != nil {
		s.emit(textChunk("agent_message_chunk", fmt.Sprintf("Starting queued message. (%d remaining)", remaining)))
		s.startTurn(next)
	}
}

func (s *Session) startTurn(t *queuedTurn) {
	s.mu.Lock()
	s.cancelRequested = false
	s.inAgentLoop = false
	s.pending = t
	depth := len(s.queue)
	s.mu.Unlock()
	s.emit(queueInfo(depth, true))

	// Completion is determined by pig's events, not by the RPC response: the prompt command only
	// acknowledges acceptance, and retry, compaction or queued continuations can emit several
	// agent_end events before agent_settled.
	go func() {
		err := s.proc.Prompt(t.message, t.images)
		if err == nil {
			return
		}
		// The child failed before agent_settled: flush what is queued, then end the turn.
		s.flushEmits()
		s.mu.Lock()
		pending := s.pending
		cancelled := s.cancelRequested
		s.pending = nil
		s.inAgentLoop = false
		depth := len(s.queue)
		s.mu.Unlock()
		if pending != nil {
			if authErr := MaybeAuthRequiredError(err); authErr != nil {
				pending.result <- TurnResult{Err: authErr}
			} else if cancelled {
				pending.result <- TurnResult{Reason: StopCancelled}
			} else {
				pending.result <- TurnResult{Reason: StopError}
			}
		}
		// The queue is not restarted: pig may be unhealthy. Only the depth metadata is cleared.
		s.emit(queueInfo(depth, false))
	}()
}

// ---- tool helpers (module-level functions of the original) ----

func findUniqueLineNumber(text, needle string) (int, bool) {
	if needle == "" {
		return 0, false
	}
	first := strings.Index(text, needle)
	if first < 0 {
		return 0, false
	}
	if strings.Contains(text[first+len(needle):], needle) {
		return 0, false
	}
	return strings.Count(text[:first], "\n") + 1, true
}

func getToolPath(args any) (string, bool) {
	rec := asObject(args)
	if p, ok := rec["path"].(string); ok {
		return p, true
	}
	if p, ok := rec["file_path"].(string); ok {
		return p, true
	}
	return "", false
}

type parsedEdit struct{ oldText, newText string }

func editsOf(args any) []any {
	rec := asObject(args)
	edits := rec["edits"]
	if s, ok := edits.(string); ok {
		var parsed any
		if json.Unmarshal([]byte(s), &parsed) != nil {
			return nil
		}
		edits = parsed
	}
	arr, _ := edits.([]any)
	return arr
}

func getEditOldTexts(args any) []string {
	rec := asObject(args)
	var olds []string
	add := func(s string) {
		for _, o := range olds {
			if o == s {
				return
			}
		}
		olds = append(olds, s)
	}
	// Edits that carry both oldText and newText come first, then any bare oldText.
	if o, ok := rec["oldText"].(string); ok {
		if _, ok := rec["newText"].(string); ok {
			olds = append(olds, o)
		}
	}
	for _, e := range editsOf(args) {
		m := asObject(e)
		if o, ok := m["oldText"].(string); ok {
			if _, ok := m["newText"].(string); ok {
				olds = append(olds, o)
			}
		}
	}
	if o, ok := rec["oldText"].(string); ok {
		add(o)
	}
	for _, e := range editsOf(args) {
		if o, ok := asObject(e)["oldText"].(string); ok {
			add(o)
		}
	}
	return olds
}

func resolvePath(cwd, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(cwd, p)
}

func toToolCallLocations(args any, cwd string, line int) []map[string]any {
	path, ok := getToolPath(args)
	if !ok || path == "" {
		return nil
	}
	loc := map[string]any{"path": resolvePath(cwd, path)}
	if line > 0 {
		loc["line"] = line
	}
	return []map[string]any{loc}
}

func toToolKind(name string) string {
	switch name {
	case "read":
		return "read"
	case "write", "edit":
		return "edit"
	case "bash":
		return "execute"
	}
	return "other"
}

// set adds key to m unless v is nil (a JS `undefined`, dropped by JSON.stringify).
func set(m Update, key string, v any) {
	switch x := v.(type) {
	case nil:
		return
	case []map[string]any:
		if x == nil {
			return
		}
	case []any:
		if x == nil {
			return
		}
	}
	m[key] = v
}

func (s *Session) emitBashToolCall(kind, id, toolName string, args any, status string, locations []map[string]any, includeTerminal bool) {
	s.mu.Lock()
	s.bashToolCalls[id] = true
	s.mu.Unlock()
	title := toolName
	if c, ok := BashCommand(args); ok {
		title = c
	}
	u := Update{"sessionUpdate": kind, "toolCallId": id, "title": title, "kind": "execute", "status": status}
	set(u, "locations", locations)
	if includeTerminal {
		u["content"] = BashTerminalContent(id)
		u["_meta"] = BashTerminalInfoMeta(id, s.cwd)
	}
	s.emit(u)
}

func (s *Session) emitBashOutputUpdate(id, status string, result any, isError bool) {
	text := BashResultText(result)
	s.mu.Lock()
	previous := s.bashSnapshots[id]
	s.bashSnapshots[id] = text
	s.mu.Unlock()
	meta := map[string]any{}
	if delta := BashOutputDelta(previous, text); delta != "" {
		for k, v := range BashTerminalOutputMeta(id, delta) {
			meta[k] = v
		}
	}
	if status == "completed" || status == "failed" {
		for k, v := range BashTerminalExitMeta(id, BashExitCode(result, isError)) {
			meta[k] = v
		}
	}
	s.emit(Update{"sessionUpdate": "tool_call_update", "toolCallId": id, "status": status, "_meta": meta})
}

func (s *Session) cleanupToolCall(id string) {
	s.mu.Lock()
	delete(s.currentToolCalls, id)
	delete(s.fileSnapshots, id)
	delete(s.fileMutation, id)
	delete(s.bashToolCalls, id)
	delete(s.bashSnapshots, id)
	s.mu.Unlock()
}

func jsStr(v any, fallback string) string {
	if v == nil {
		return fallback
	}
	return jsString(v)
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func (s *Session) handlePiEvent(ev Event) {
	typ := jsStr(ev["type"], "")
	switch typ {
	case "message_update":
		s.handleMessageUpdate(ev)
	case "tool_execution_start":
		s.handleToolStart(ev)
	case "tool_execution_update":
		s.handleToolUpdate(ev)
	case "tool_execution_end":
		s.handleToolEnd(ev)
	case "extension_ui_request":
		s.handleExtensionUIRequest(ev)
	case "auto_retry_start":
		s.emit(textChunk("agent_message_chunk", formatAutoRetryMessage(ev)))
	case "auto_retry_end":
		s.emit(textChunk("agent_message_chunk", "Retry finished, resuming."))
	case "auto_compaction_start":
		s.emit(textChunk("agent_message_chunk", "Context nearing limit, running automatic compaction..."))
	case "auto_compaction_end":
		s.emit(textChunk("agent_message_chunk", "Automatic compaction finished; context was summarized to continue the session."))
	case "agent_start":
		s.mu.Lock()
		s.inAgentLoop = true
		s.mu.Unlock()
	case "turn_end":
		// A turn_end is a sub-step (for example tool_use); the ACP prompt waits for agent_settled.
	case "agent_end":
		// One low-level run ended; pig may still retry, compact or continue, so the turn stays open.
		s.mu.Lock()
		s.inAgentLoop = false
		s.mu.Unlock()
	case "agent_settled":
		go s.settleTurn()
	}
}

func (s *Session) handleMessageUpdate(ev Event) {
	ame := asObject(ev["assistantMessageEvent"])
	typ, _ := ame["type"].(string)
	switch typ {
	case "text_delta":
		if d, ok := ame["delta"].(string); ok {
			s.emit(textChunk("agent_message_chunk", d))
			return
		}
	case "thinking_delta":
		if d, ok := ame["delta"].(string); ok {
			s.emit(textChunk("agent_thought_chunk", d))
			return
		}
	}
	if typ != "toolcall_start" && typ != "toolcall_delta" && typ != "toolcall_end" {
		return
	}
	// pig sometimes puts the tool call on the event, and always in the partial message at contentIndex.
	toolCall := ame["toolCall"]
	if toolCall == nil {
		idx := 0
		if f, ok := asNumber(ame["contentIndex"]); ok {
			idx = int(f)
		}
		content, _ := asObject(ame["partial"])["content"].([]any)
		if idx >= 0 && idx < len(content) {
			toolCall = content[idx]
		}
	}
	tc := asObject(toolCall)
	id := jsStr(tc["id"], "")
	name := jsStr(tc["name"], "tool")
	if id == "" {
		return
	}
	var rawInput any
	if args, ok := tc["arguments"]; ok && truthy(args) && isJSObject(args) {
		rawInput = args
	} else if ps := jsStr(tc["partialArgs"], ""); ps != "" {
		var parsed any
		if json.Unmarshal([]byte(ps), &parsed) == nil {
			rawInput = parsed
		} else {
			rawInput = map[string]any{"partialArgs": ps}
		}
	}
	locations := toToolCallLocations(rawInput, s.cwd, 0)
	s.mu.Lock()
	existing, has := s.currentToolCalls[id]
	if !has {
		s.currentToolCalls[id] = "pending"
	}
	s.mu.Unlock()
	// Never downgrade a status that already advanced (for example via tool_execution_start).
	status := "pending"
	if has {
		status = existing
	}
	switch {
	case IsBashTool(name):
		kind := "tool_call"
		if has {
			kind = "tool_call_update"
		}
		s.emitBashToolCall(kind, id, name, rawInput, status, locations, !has)
	case !has:
		u := Update{"sessionUpdate": "tool_call", "toolCallId": id, "title": name, "kind": toToolKind(name), "status": status}
		set(u, "locations", locations)
		set(u, "rawInput", rawInput)
		s.emit(u)
	default:
		// Keep rawInput current while the arguments stream; the status stays as it is.
		u := Update{"sessionUpdate": "tool_call_update", "toolCallId": id, "status": status}
		set(u, "locations", locations)
		set(u, "rawInput", rawInput)
		s.emit(u)
	}
}

func isJSObject(v any) bool {
	switch v.(type) {
	case map[string]any, []any:
		return true
	}
	return false
}

func (s *Session) handleToolStart(ev Event) {
	id := newUUID()
	if v, ok := ev["toolCallId"]; ok && v != nil {
		id = jsString(v)
	}
	name := jsStr(ev["toolName"], "tool")
	args, hasArgs := ev["args"]

	if IsBashTool(name) {
		locations := toToolCallLocations(args, s.cwd, 0)
		s.mu.Lock()
		_, has := s.currentToolCalls[id]
		s.currentToolCalls[id] = "in_progress"
		s.mu.Unlock()
		kind := "tool_call"
		if has {
			kind = "tool_call_update"
		}
		s.emitBashToolCall(kind, id, name, args, "in_progress", locations, !has)
		return
	}

	// Capture the file before it is mutated so a structured diff can be sent afterwards.
	line := 0
	if name == "edit" || name == "write" {
		s.mu.Lock()
		s.fileMutation[id] = true
		s.mu.Unlock()
		if p, ok := getToolPath(args); ok && p != "" {
			data, err := os.ReadFile(resolvePath(s.cwd, p))
			s.mu.Lock()
			if err != nil {
				s.fileSnapshots[id] = fileSnapshot{path: p}
			} else {
				old := string(data)
				s.fileSnapshots[id] = fileSnapshot{path: p, oldText: &old}
			}
			s.mu.Unlock()
			if err == nil && name == "edit" {
				for _, needle := range getEditOldTexts(args) {
					if n, ok := findUniqueLineNumber(string(data), needle); ok {
						line = n
						break
					}
				}
			}
		}
	}
	locations := toToolCallLocations(args, s.cwd, line)
	s.mu.Lock()
	_, has := s.currentToolCalls[id]
	s.currentToolCalls[id] = "in_progress"
	s.mu.Unlock()
	kind := "tool_call"
	if has {
		kind = "tool_call_update"
	}
	u := Update{"sessionUpdate": kind, "toolCallId": id, "status": "in_progress"}
	if !has {
		u["title"], u["kind"] = name, toToolKind(name)
	}
	set(u, "locations", locations)
	if hasArgs {
		set(u, "rawInput", args)
	}
	s.emit(u)
}

func textContent(text string) []any {
	return []any{map[string]any{"type": "content", "content": map[string]any{"type": "text", "text": text}}}
}

func (s *Session) handleToolUpdate(ev Event) {
	id := jsStr(ev["toolCallId"], "")
	if id == "" {
		return
	}
	partial, hasPartial := ev["partialResult"]
	s.mu.Lock()
	isBash := s.bashToolCalls[id]
	isFile := s.fileMutation[id]
	s.mu.Unlock()
	if isBash {
		s.emitBashOutputUpdate(id, "in_progress", partial, false)
		return
	}
	text := ""
	if !isFile {
		text = ToolResultToText(partial)
	}
	u := Update{"sessionUpdate": "tool_call_update", "toolCallId": id, "status": "in_progress"}
	if text != "" {
		u["content"] = textContent(text)
	}
	if !isFile && hasPartial {
		u["rawOutput"] = partial
	}
	s.emit(u)
}

func (s *Session) handleToolEnd(ev Event) {
	id := jsStr(ev["toolCallId"], "")
	if id == "" {
		return
	}
	result, hasResult := ev["result"]
	isError := truthy(ev["isError"])
	s.mu.Lock()
	isBash := s.bashToolCalls[id]
	snap, hasSnap := s.fileSnapshots[id]
	s.mu.Unlock()
	status := "completed"
	if isError {
		status = "failed"
	}
	if isBash {
		s.emitBashOutputUpdate(id, status, result, isError)
		s.cleanupToolCall(id)
		return
	}
	text := ToolResultToText(result)
	var content []any
	hasDiff := false
	if !isError && hasSnap {
		if data, err := os.ReadFile(resolvePath(s.cwd, snap.path)); err == nil {
			newText := string(data)
			if snap.oldText == nil || newText != *snap.oldText {
				hasDiff = true
				var old any
				if snap.oldText != nil {
					old = *snap.oldText
				}
				content = []any{map[string]any{"type": "diff", "path": snap.path, "oldText": old, "newText": newText}}
			}
		}
	}
	if content == nil && !hasDiff && text != "" {
		content = textContent(text)
	}
	u := Update{"sessionUpdate": "tool_call_update", "toolCallId": id, "status": status}
	set(u, "content", content)
	if !hasDiff && hasResult {
		u["rawOutput"] = result
	}
	s.emit(u)
	s.cleanupToolCall(id)
}

func formatAutoRetryMessage(ev Event) string {
	attempt, ok1 := numberish(ev["attempt"])
	maxAttempts, ok2 := numberish(ev["maxAttempts"])
	delayMs, ok3 := numberish(ev["delayMs"])
	if !ok1 || !ok2 || !ok3 {
		return "Retrying..."
	}
	secs := math.Floor(delayMs/1000 + 0.5)
	if delayMs > 0 && secs == 0 {
		secs = 1
	}
	return fmt.Sprintf("Retrying (attempt %s/%s, waiting %ss)...", jsNumber(attempt), jsNumber(maxAttempts), jsNumber(secs))
}

// numberish is Number(v) for the values pig sends: numbers and numeric strings.
func numberish(v any) (float64, bool) {
	if f, ok := asNumber(v); ok {
		return f, !math.IsInf(f, 0) && !math.IsNaN(f)
	}
	if s, ok := v.(string); ok {
		f, err := strconv.ParseFloat(jsTrim(s), 64)
		if err == nil && !math.IsInf(f, 0) && !math.IsNaN(f) {
			return f, true
		}
	}
	return 0, false
}

// ---- extension dialogs ----

func (s *Session) sendUI(resp map[string]any) {
	// A failed acknowledgement leaves nothing to do: the child is gone or will time the dialog out.
	_ = s.proc.SendExtensionUIResponse(resp)
}

func (s *Session) handleExtensionUIRequest(ev Event) {
	id, _ := ev["id"].(string)
	method, _ := ev["method"].(string)
	if id == "" {
		return
	}
	cancel := func() { s.ackQ.do(func() { s.sendUI(map[string]any{"id": id, "cancelled": true}) }) }
	switch method {
	case "select":
		go s.handleSelect(ev, id)
	case "confirm":
		go s.handleConfirm(ev, id)
	case "input", "editor":
		s.emit(textChunk("agent_message_chunk", fmt.Sprintf("Pi %s UI request is not supported in ACP yet; cancelling it.", method)))
		cancel()
	case "notify":
		text := "Pi notification"
		if m, ok := ev["message"].(string); ok {
			text = m
		}
		level := "info"
		if l, ok := ev["notifyType"].(string); ok {
			level = l
		}
		u := textChunk("agent_message_chunk", text)
		u["_meta"] = map[string]any{"piAcp": map[string]any{"notify": map[string]any{"level": level}}}
		s.emit(u)
		cancel()
	default:
		cancel()
	}
}

func extensionUIToolCall(id string, ev Event) map[string]any {
	method := "ui"
	if m, ok := ev["method"].(string); ok {
		method = m
	}
	title := "Pi " + method
	if t, ok := ev["title"].(string); ok {
		title = t
	}
	rawInput := map[string]any{"method": method}
	for _, k := range extensionUIRawInputKeys {
		if v, ok := ev[k]; ok {
			rawInput[k] = v
		}
	}
	return map[string]any{"toolCallId": "pi-ui-" + id, "title": title, "kind": "other", "status": "pending", "rawInput": rawInput}
}

func (s *Session) requestPermission(id string, ev Event, options []PermissionOption) (PermissionResponse, bool) {
	resp, err := s.conn.RequestPermission(PermissionRequest{SessionID: s.id, ToolCall: extensionUIToolCall(id, ev), Options: options})
	if err != nil {
		s.sendUI(map[string]any{"id": id, "cancelled": true})
		return PermissionResponse{}, false
	}
	return resp, true
}

func optionIndex(optionID string) (int, bool) {
	if !strings.HasPrefix(optionID, choiceOptionPrefix) {
		return 0, false
	}
	raw := optionID[len(choiceOptionPrefix):]
	if raw == "" {
		return 0, false
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 || strconv.Itoa(n) != raw {
		return 0, false
	}
	return n, true
}

func (s *Session) handleSelect(ev Event, id string) {
	raw, _ := ev["options"].([]any)
	options := make([]string, 0, len(raw))
	for _, o := range raw {
		options = append(options, jsString(o))
	}
	if len(options) == 0 {
		s.sendUI(map[string]any{"id": id, "cancelled": true})
		return
	}
	perm := make([]PermissionOption, len(options))
	for i, name := range options {
		perm[i] = PermissionOption{OptionID: fmt.Sprintf("%s%d", choiceOptionPrefix, i), Name: name, Kind: "allow_once"}
	}
	resp, ok := s.requestPermission(id, ev, perm)
	if !ok {
		return
	}
	if resp.Outcome.Outcome == "selected" {
		if idx, ok := optionIndex(resp.Outcome.OptionID); ok && idx < len(options) {
			s.sendUI(map[string]any{"id": id, "value": options[idx]})
			return
		}
	}
	s.sendUI(map[string]any{"id": id, "cancelled": true})
}

func (s *Session) handleConfirm(ev Event, id string) {
	resp, ok := s.requestPermission(id, ev, []PermissionOption{
		{OptionID: "yes", Name: "Yes", Kind: "allow_once"},
		{OptionID: "no", Name: "No", Kind: "reject_once"},
	})
	if !ok {
		return
	}
	if resp.Outcome.Outcome == "cancelled" {
		s.sendUI(map[string]any{"id": id, "cancelled": true})
		return
	}
	s.sendUI(map[string]any{"id": id, "confirmed": resp.Outcome.OptionID == "yes"})
}

// ---- session registry ----

// SessionManager is the default SessionRegistry.
type SessionManager struct {
	spawn SpawnFunc
	store Store

	mu       sync.Mutex
	sessions map[string]*Session
}

// NewSessionManager returns an empty manager.
func NewSessionManager(spawn SpawnFunc, store Store) *SessionManager {
	if spawn == nil {
		spawn = func(p SpawnParams) (Proc, error) {
			proc, err := pirpc.SpawnWithArgs(p.Cwd, p.PiCommand, p.SessionPath, nil)
			if err != nil {
				return nil, err
			}
			return proc, nil
		}
	}
	return &SessionManager{spawn: spawn, store: store, sessions: map[string]*Session{}}
}

// Create spawns a child and registers the session. pig manages persistence in its default
// location, so the sessions stay visible to the regular `pig` CLI.
func (m *SessionManager) Create(p SessionCreateParams) (ActiveSession, error) {
	proc, err := m.spawn(SpawnParams{Cwd: p.Cwd, PiCommand: p.PiCommand})
	if err != nil {
		var sc interface{ SpawnCode() string }
		if errors.As(err, &sc) {
			data := map[string]any{}
			if c := sc.SpawnCode(); c != "" {
				data["code"] = c
			}
			return nil, ErrInternal(data, err.Error())
		}
		return nil, err
	}
	state, serr := proc.GetState()
	id, file := "", ""
	if serr == nil {
		id, _ = state["sessionId"].(string)
		file, _ = state["sessionFile"].(string)
	}
	if id == "" {
		id = newUUID()
	}
	if file != "" {
		m.store.Upsert(StoredSession{SessionID: id, Cwd: p.Cwd, SessionFile: file})
	}
	sess := NewSession(SessionOptions{SessionID: id, Cwd: p.Cwd, McpServers: p.McpServers, Proc: proc, Conn: p.Conn, FileCommands: p.FileCommands})
	m.mu.Lock()
	m.sessions[id] = sess
	m.mu.Unlock()
	return sess, nil
}

// MaybeGet returns a registered session or nil.
func (m *SessionManager) MaybeGet(id string) ActiveSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[id]; ok {
		return s
	}
	return nil
}

// Get returns a registered session or an invalid-params error.
func (m *SessionManager) Get(id string) (ActiveSession, error) {
	if s := m.MaybeGet(id); s != nil {
		return s, nil
	}
	return nil, ErrInvalidParams(nil, "Unknown sessionId: "+id)
}

// GetOrCreate registers a session around an existing child (session/load).
func (m *SessionManager) GetOrCreate(id string, p SessionCreateParams) ActiveSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[id]; ok {
		return s
	}
	sess := NewSession(SessionOptions{SessionID: id, Cwd: p.Cwd, McpServers: p.McpServers, Proc: p.Proc, Conn: p.Conn, FileCommands: p.FileCommands})
	m.sessions[id] = sess
	return sess
}

// Close disposes a session's child and forgets the session.
func (m *SessionManager) Close(id string) {
	m.mu.Lock()
	s, ok := m.sessions[id]
	delete(m.sessions, id)
	m.mu.Unlock()
	if ok {
		s.proc.Dispose()
	}
}

// CloseAllExcept disposes every other session.
func (m *SessionManager) CloseAllExcept(keep string) {
	m.mu.Lock()
	var ids []string
	for id := range m.sessions {
		if id != keep {
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.Close(id)
	}
}

// DisposeAll disposes every session.
func (m *SessionManager) DisposeAll() { m.CloseAllExcept("") }
