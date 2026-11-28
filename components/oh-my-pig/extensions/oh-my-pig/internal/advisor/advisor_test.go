package advisor

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/surface"
)

type advHost struct {
	t        *testing.T
	nc       net.Conn
	runDone  chan error
	writeMu  sync.Mutex
	mu       sync.Mutex
	handlers map[string][]int
	commands map[string]bool
	tools    map[string]bool
	pending  map[string]chan json.RawMessage
	nextID   int

	cwd         string
	sessionID   string
	sessionFile string
	hasUI       bool
	branch      []map[string]any
	notifies    []string
	clipboard   string

	createSessionCalls []map[string]any
	childCallOrder     []string
}

type advFrame struct {
	Type     string `json:"type"`
	ID       string `json:"id,omitempty"`
	Register *struct {
		Handlers []struct {
			Event     string `json:"event"`
			HandlerID int    `json:"handler_id"`
		} `json:"handlers"`
		Commands []struct {
			Name string `json:"name"`
		} `json:"commands"`
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	} `json:"register,omitempty"`
	Response *struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"response,omitempty"`
	Call *struct {
		Method string          `json:"method"`
		Args   json.RawMessage `json:"args"`
	} `json:"call,omitempty"`
}

func startAdvisorHost(t *testing.T, cwd, sessionID, sessionFile string, hasUI bool, branch []map[string]any) (*manager, *advHost) {
	t.Helper()
	if cwd == "" {
		cwd = t.TempDir()
	}
	if sessionID == "" {
		sessionID = "sess-adv-1"
	}
	client, server := net.Pipe()
	ext := sdk.New("advisor-test")
	mgr := newManager()
	registerWithManager(ext, mgr)

	h := &advHost{
		t:           t,
		nc:          server,
		runDone:     make(chan error, 1),
		handlers:    map[string][]int{},
		commands:    map[string]bool{},
		tools:       map[string]bool{},
		pending:     map[string]chan json.RawMessage{},
		cwd:         cwd,
		sessionID:   sessionID,
		sessionFile: sessionFile,
		hasUI:       hasUI,
		branch:      branch,
	}

	go func() {
		h.runDone <- ext.RunWithConn(client)
	}()

	reg := h.readFrame()
	if reg.Type != "register" || reg.Register == nil {
		t.Fatalf("expected register frame, got %+v", reg)
	}
	for _, hh := range reg.Register.Handlers {
		h.handlers[hh.Event] = append(h.handlers[hh.Event], hh.HandlerID)
	}
	for _, cmd := range reg.Register.Commands {
		h.commands[cmd.Name] = true
	}
	for _, tool := range reg.Register.Tools {
		h.tools[tool.Name] = true
	}

	stateRaw, _ := json.Marshal(map[string]any{
		"hasUI": hasUI,
		"model": map[string]any{
			"id":            "claude-sonnet-4-5",
			"provider":      "anthropic",
			"contextWindow": 200000,
		},
		"sessionId":   sessionID,
		"sessionFile": sessionFile,
	})
	ready := map[string]any{
		"type": "ready",
		"ready": map[string]any{
			"mode":  "tui",
			"cwd":   cwd,
			"width": 100,
			"state": json.RawMessage(stateRaw),
		},
	}
	h.writeMsg(ready)
	go h.serveLoop()
	t.Cleanup(func() {
		mgr.endSession(sessionID)
		_ = server.Close()
		_ = client.Close()
		<-h.runDone
	})
	return mgr, h
}

func (h *advHost) writeMsg(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		h.t.Fatal(err)
	}
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(b)))
	_, _ = h.nc.Write(lenBuf[:])
	_, _ = h.nc.Write(b)
}

func (h *advHost) readFrame() advFrame {
	var lenBuf [4]byte
	if _, err := io.ReadFull(h.nc, lenBuf[:]); err != nil {
		return advFrame{}
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	buf := make([]byte, n)
	if _, err := io.ReadFull(h.nc, buf); err != nil {
		return advFrame{}
	}
	var f advFrame
	_ = json.Unmarshal(buf, &f)
	return f
}

func (h *advHost) serveLoop() {
	for {
		f := h.readFrame()
		if f.Type == "" {
			return
		}
		switch f.Type {
		case "response":
			h.mu.Lock()
			ch := h.pending[f.ID]
			delete(h.pending, f.ID)
			h.mu.Unlock()
			if ch != nil {
				if f.Response != nil {
					ch <- f.Response.Result
				} else {
					ch <- nil
				}
			}
		case "call":
			go func(frame advFrame) {
				var args map[string]any
				if frame.Call != nil && len(frame.Call.Args) > 0 {
					_ = json.Unmarshal(frame.Call.Args, &args)
				}
				res := h.handleCall(frame.Call.Method, args)
				raw, _ := json.Marshal(res)
				h.writeMsg(map[string]any{
					"type":        "call_result",
					"id":          frame.ID,
					"call_result": map[string]any{"result": json.RawMessage(raw)},
				})
			}(f)
		}
	}
}

func (h *advHost) handleCall(method string, args map[string]any) any {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch method {
	case "getSessionID", "session.getSessionID":
		return map[string]any{"sessionId": h.sessionID}
	case "getSessionFile", "session.getSessionFile":
		return map[string]any{"sessionFile": h.sessionFile}
	case "getBranch":
		return map[string]any{"entries": h.branch}
	case "session.getBranch":
		return h.branch
	case "watchSessionLog", "getSessionLog":
		var rawEntries []map[string]any
		leafID := ""
		for i, e := range h.branch {
			cp := make(map[string]any, len(e)+1)
			for k, v := range e {
				cp[k] = v
			}
			if id, _ := cp["id"].(string); id != "" {
				if i > 0 {
					if _, hasParent := cp["parentId"]; !hasParent {
						if prevID, _ := h.branch[i-1]["id"].(string); prevID != "" {
							cp["parentId"] = prevID
						}
					}
				}
				leafID = id
			}
			rawEntries = append(rawEntries, cp)
		}
		cursor := 0
		if c, ok := args["cursor"].(float64); ok {
			cursor = int(c)
		}
		var slice []map[string]any
		if cursor < len(rawEntries) {
			slice = rawEntries[cursor:]
		} else {
			slice = []map[string]any{}
		}
		return map[string]any{
			"entries":    slice,
			"entryCount": len(rawEntries),
			"hasMore":    false,
			"leafId":     leafID,
		}
	case "ui.notify":
		msg, _ := args["message"].(string)
		h.notifies = append(h.notifies, msg)
		return map[string]any{}
	case "clipboard.copy", "copyToClipboard", "ui.copyToClipboard":
		txt, _ := args["text"].(string)
		h.clipboard = txt
		return map[string]any{}
	case "childSession.create":
		h.createSessionCalls = append(h.createSessionCalls, args)
		h.childCallOrder = append(h.childCallOrder, "childSession.create")
		return map[string]any{
			"sessionId": "child-sess-1",
		}
	case "childSession.bindExtensions":
		h.childCallOrder = append(h.childCallOrder, "childSession.bindExtensions")
		return map[string]any{}
	case "childSession.prompt":
		h.childCallOrder = append(h.childCallOrder, "childSession.prompt")
		return map[string]any{}
	case "childSession.state":
		return map[string]any{"lastAssistantText": "Looks solid."}
	case "childSession.dispose":
		h.childCallOrder = append(h.childCallOrder, "childSession.dispose")
		return map[string]any{}
	default:
		return map[string]any{}
	}
}

func (h *advHost) request(req map[string]any) json.RawMessage {
	h.mu.Lock()
	h.nextID++
	id := fmt.Sprintf("req-%d", h.nextID)
	ch := make(chan json.RawMessage, 1)
	h.pending[id] = ch
	h.mu.Unlock()

	h.writeMsg(map[string]any{
		"type":    "request",
		"id":      id,
		"request": req,
	})
	select {
	case raw := <-ch:
		return raw
	case <-time.After(5 * time.Second):
		h.t.Fatalf("timeout waiting for request %v", req)
		return nil
	}
}

func (h *advHost) callCommand(name, args string) {
	h.request(map[string]any{
		"method": "command",
		"tool":   name,
		"args":   args,
	})
}

func (h *advHost) fireEvent(event string, data map[string]any) json.RawMessage {
	ids := h.handlers[event]
	raw, _ := json.Marshal(data)
	var last json.RawMessage
	for _, hid := range ids {
		last = h.request(map[string]any{
			"method":     "event",
			"event":      event,
			"handler_id": hid,
			"args":       json.RawMessage(raw),
		})
	}
	return last
}

func (h *advHost) lastNotify() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.notifies) == 0 {
		return ""
	}
	return h.notifies[len(h.notifies)-1]
}

func TestParseWatchdogYAMLAndDiscovery(t *testing.T) {
	dir := t.TempDir()
	userDir := filepath.Join(dir, "agent-dir")
	projDir := filepath.Join(dir, "repo", "sub")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "repo", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	userYAML := `instructions: |
  Global rule A.
maxNotesPerUpdate: 3
advisors:
  - name: Security
    model: openai/gpt-5.4
    tools: [read, search]
    reviewMode: turn
    reviewInterval: 2
    enabled: true
`
	projYAML := `instructions: Project rule B.
advisors:
  - name: Security
    model: anthropic/claude-opus-4-5
    tools:
      - read
      - grep
      - glob
    reviewMode: agent-end
    maxNotesPerUpdate: 2
  - name: Perf
    tools: []
    instructions: |
      Watch allocations.
`
	if err := os.WriteFile(filepath.Join(userDir, "WATCHDOG.yml"), []byte(userYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, "WATCHDOG.yml"), []byte(projYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	disc := DiscoverAdvisorConfigs(projDir, userDir)
	if len(disc.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", disc.Warnings)
	}
	if !strings.Contains(disc.SharedInstructions, "Global rule A.") || !strings.Contains(disc.SharedInstructions, "Project rule B.") {
		t.Fatalf("shared instructions = %q", disc.SharedInstructions)
	}
	if len(disc.Advisors) != 2 {
		t.Fatalf("advisors count = %d, want 2: %+v", len(disc.Advisors), disc.Advisors)
	}
	// Project leaf overrides user Security entry by slug.
	sec := disc.Advisors[0]
	if sec.Slug != "security" || sec.Model != "anthropic/claude-opus-4-5" || sec.ReviewMode != ReviewModeAgentEnd || sec.MaxNotesPerUpdate != 2 {
		t.Fatalf("unexpected merged Security advisor: %+v", sec)
	}
	if len(sec.Tools) != 3 || sec.Tools[0] != "read" || sec.Tools[1] != "grep" || sec.Tools[2] != "glob" {
		t.Fatalf("unexpected Security tools: %v", sec.Tools)
	}
	perf := disc.Advisors[1]
	if perf.Slug != "perf" || !perf.ToolsExplicit || len(perf.Tools) != 0 || perf.Instructions != "Watch allocations." {
		t.Fatalf("unexpected Perf advisor: %+v", perf)
	}
}

func TestEmissionGuardAndFormatting(t *testing.T) {
	g := NewEmissionGuard(2, 16)
	if d := g.Admit("   ", 1, false); d.Accepted || d.Reason != SuppressEmpty {
		t.Fatalf("expected empty suppression, got %+v", d)
	}
	if d := g.Admit("Looks good!", 1, false); d.Accepted || d.Reason != SuppressNoise {
		t.Fatalf("expected noise suppression, got %+v", d)
	}
	if d := g.Admit("Check error path in handler", 1, false); !d.Accepted {
		t.Fatalf("expected first note accepted, got %+v", d)
	}
	if d := g.Admit("check error path in handler!", 1, false); d.Accepted || d.Reason != SuppressDuplicate {
		t.Fatalf("expected duplicate suppression, got %+v", d)
	}
	// Escalation nit -> concern is allowed.
	if d := g.Admit("Check error path in handler", 2, false); !d.Accepted {
		t.Fatalf("expected escalation accepted, got %+v", d)
	}
	// Second unique non-blocker uses slot 2.
	if d := g.Admit("Avoid O(n^2) loop", 1, false); !d.Accepted {
		t.Fatalf("expected second slot accepted, got %+v", d)
	}
	// Third unique non-blocker hits budget.
	if d := g.Admit("Rename helper", 1, false); d.Accepted || d.Reason != SuppressRateLimit {
		t.Fatalf("expected rate-limit, got %+v", d)
	}
	// Blocker bypasses budget.
	if d := g.Admit("Missing lock around map write causes race", 3, false); !d.Accepted {
		t.Fatalf("expected blocker exempt from budget, got %+v", d)
	}

	cur := 5
	xml := FormatBatchContent([]Note{
		{Note: "Minor style <nit>", Severity: SeverityNit, Turn: 4, HasTurn: true},
		{Note: "Race on shared map", Severity: SeverityBlocker, Advisor: "Safety", Turn: 5, HasTurn: true},
	}, &cur)
	wantBlocker := `<advisory advisor="Safety" severity="blocker" guidance="weigh, don't blindly obey">`
	wantNit := `<advisory severity="nit" turns_ago="1" guidance="weigh, don't blindly obey">`
	if !strings.Contains(xml, wantBlocker) || !strings.Contains(xml, wantNit) {
		t.Fatalf("unexpected formatted advisory XML:\n%s", xml)
	}
	if !strings.Contains(xml, "Minor style &lt;nit&gt;") {
		t.Fatalf("expected XML escaping in advisory:\n%s", xml)
	}
}

func TestQuarantineUnsafeOutput(t *testing.T) {
	if _, bad := QuarantineUnsafeOutput("Please run rm -rf /tmp/build", "user ran tests"); !bad {
		t.Fatalf("expected output-only rm -rf to be quarantined")
	}
	if _, bad := QuarantineUnsafeOutput("The script ran rm -rf /tmp/build", "tool output: rm -rf /tmp/build"); bad {
		t.Fatalf("expected transcript-quoted rm -rf not to be quarantined")
	}
}

func TestAdvisorDisabledByDefaultAndNoTopLevelAdviseTool(t *testing.T) {
	origSetting := Setting
	origSetSetting := SetSetting
	origResolve := ResolveModelRole
	origSpawn := SpawnAdvisorSubagent
	t.Cleanup(func() {
		Setting = origSetting
		SetSetting = origSetSetting
		ResolveModelRole = origResolve
		SpawnAdvisorSubagent = origSpawn
	})

	Setting = nil
	SetSetting = nil
	spawnCalls := 0
	SpawnAdvisorSubagent = func(_ context.Context, _ sdk.Context, _ ReviewRequest) (ReviewResult, error) {
		spawnCalls++
		return ReviewResult{}, nil
	}

	_, h := startAdvisorHost(t, "", "sess-default-off", "", true, []map[string]any{
		{"id": "u1", "type": "message", "message": map[string]any{"role": "user", "content": "hi"}},
	})

	if h.tools["advise"] {
		t.Fatalf("advise tool MUST NOT be registered on primary sessions")
	}
	if !h.commands["advisor"] {
		t.Fatalf("expected /advisor command to be registered")
	}

	h.fireEvent(sdk.EventTurnEnd, map[string]any{})
	h.fireEvent(sdk.EventAgentEnd, map[string]any{})
	raw := h.fireEvent(sdk.EventBeforeAgentStart, map[string]any{})
	if len(raw) > 0 && string(raw) != "null" && string(raw) != "{}" {
		t.Fatalf("expected zero injection when disabled, got %s", string(raw))
	}
	if spawnCalls != 0 {
		t.Fatalf("expected zero advisor calls when disabled, got %d", spawnCalls)
	}
	h.callCommand("advisor", "status")
	if got := h.lastNotify(); got != "Advisor is disabled." {
		t.Fatalf("status = %q, want %q", got, "Advisor is disabled.")
	}
}

func TestAdvisorCommandLifecycleReviewAndInjection(t *testing.T) {
	origSetting := Setting
	origSetSetting := SetSetting
	origResolve := ResolveModelRole
	origSpawn := SpawnAdvisorSubagent
	surface.ResetForTests()
	t.Cleanup(func() {
		Setting = origSetting
		SetSetting = origSetSetting
		ResolveModelRole = origResolve
		SpawnAdvisorSubagent = origSpawn
		surface.ResetForTests()
	})

	enabled := false
	Setting = func(_ sdk.Context, key string) any {
		switch key {
		case "advisor.enabled":
			return enabled
		case "advisor.reviewMode":
			return "turn"
		case "advisor.reviewInterval":
			return 1
		case "advisor.immuneTurns":
			return 3
		}
		return nil
	}
	SetSetting = func(_ sdk.Context, key string, value any, persist bool) error {
		if key == "advisor.enabled" {
			if persist {
				t.Errorf("/advisor toggle must be session-scoped (persist=false)")
			}
			enabled = value.(bool)
		}
		return nil
	}
	ResolveModelRole = func(_ sdk.Context, selector string) (string, string, string, bool) {
		if selector == "@advisor" {
			return "anthropic", "claude-opus-4-5", "medium", true
		}
		return "", "", "", false
	}

	reviewDone := make(chan struct{}, 4)
	SpawnAdvisorSubagent = func(_ context.Context, _ sdk.Context, req ReviewRequest) (ReviewResult, error) {
		if req.OnNote != nil {
			req.OnNote(Note{
				Note:     "Check nil pointer before dereferencing config",
				Severity: SeverityConcern,
				Turn:     req.CoveredTurn,
				HasTurn:  true,
			})
		}
		defer func() { reviewDone <- struct{}{} }()
		return ReviewResult{
			AssistantText: "Raised one concern.",
			InputTokens:   120,
			OutputTokens:  30,
			Cost:          0.0042,
		}, nil
	}

	tmp := t.TempDir()
	sessionFile := filepath.Join(tmp, "sess-live.jsonl")
	branch := []map[string]any{
		{"id": "u1", "type": "message", "message": map[string]any{"role": "user", "content": "Fix config loader"}},
		{"id": "a1", "type": "message", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "Editing loader.go"}}}},
	}
	_, h := startAdvisorHost(t, tmp, "sess-live", sessionFile, true, branch)

	// Toggle on
	h.callCommand("advisor", "on")
	if !enabled || h.lastNotify() != "Advisor enabled." {
		t.Fatalf("after /advisor on: enabled=%v notify=%q", enabled, h.lastNotify())
	}

	// Trigger turn_end -> background review
	h.fireEvent(sdk.EventTurnEnd, map[string]any{})
	select {
	case <-reviewDone:
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for advisor review")
	}

	// Wait briefly for goroutine state commit + transcript persist.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if surface.GetComposerMeta().AdvisorCost > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if gotCost := surface.GetComposerMeta().AdvisorCost; gotCost < 0.004 {
		t.Fatalf("expected composer advisor cost >= 0.004, got %v", gotCost)
	}

	// Verify persisted transcript cost scanning works.
	costs := LoadTranscriptCosts(sessionFile)
	if costs[""] < 0.004 {
		t.Fatalf("expected persisted transcript cost >= 0.004, got %+v", costs)
	}

	// Status shows model and spend.
	h.callCommand("advisor", "status")
	if st := h.lastNotify(); !strings.Contains(st, "anthropic/claude-opus-4-5") || !strings.Contains(st, "$0.0042") {
		t.Fatalf("unexpected status: %q", st)
	}

	// Dump copies to clipboard.
	h.callCommand("advisor", "dump")
	if h.lastNotify() != "Advisor history copied to clipboard." {
		t.Fatalf("dump notify = %q", h.lastNotify())
	}
	if !strings.Contains(h.clipboard, "Check nil pointer") {
		t.Fatalf("dump clipboard missing advisory: %q", h.clipboard)
	}

	// Next turn injects pending <advisory> block.
	raw := h.fireEvent(sdk.EventBeforeAgentStart, map[string]any{
		"type":                "before_agent_start",
		"systemPromptOptions": map[string]any{"cwd": tmp},
	})
	var decoded struct {
		Result struct {
			Message struct {
				CustomType string `json:"customType"`
				Content    string `json:"content"`
				Display    bool   `json:"display"`
			} `json:"message"`
		} `json:"_pigPromptResult"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("failed decoding before_agent_start response: %v (raw=%s)", err, string(raw))
	}
	if decoded.Result.Message.CustomType != "advisor-notes" || decoded.Result.Message.Display ||
		!strings.Contains(decoded.Result.Message.Content, `<advisory severity="concern" guidance="weigh, don't blindly obey">`) ||
		!strings.Contains(decoded.Result.Message.Content, "Check nil pointer before dereferencing config") {
		t.Fatalf("before_agent_start unexpected advisory injection: %+v", decoded.Result.Message)
	}

	// Configure prints path + template.
	h.callCommand("advisor", "configure")
	if cfg := h.lastNotify(); !strings.Contains(cfg, "WATCHDOG.yml") {
		t.Fatalf("configure output = %q", cfg)
	}

	// Turn off
	h.callCommand("advisor", "off")
	if enabled || h.lastNotify() != "Advisor disabled." {
		t.Fatalf("after /advisor off: enabled=%v notify=%q", enabled, h.lastNotify())
	}
}

func TestParseWatchdogYAMLIndentZeroAndQuotedComments(t *testing.T) {
	yamlContent := `
instructions: "Global rule" # top-level comment
maxNotesPerUpdate: 3 # budget comment
advisors:
- name: "Security Lead" # inline comment after double quote
  model: '@advisor' # inline comment after single quote
  tools: []
  reviewMode: turn # mode comment
- name: 'Perf #1' # hash inside single quotes preserved
  tools: [read, grep]
`
	doc, warnings := ParseWatchdogYAML(yamlContent, "WATCHDOG.yml")
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if doc.Instructions != "Global rule" {
		t.Fatalf("instructions = %q, want %q", doc.Instructions, "Global rule")
	}
	if doc.MaxNotesPerUpdate != 3 {
		t.Fatalf("maxNotesPerUpdate = %d, want 3", doc.MaxNotesPerUpdate)
	}
	if len(doc.Advisors) != 2 {
		t.Fatalf("expected 2 indent-0 advisors, got %d (%+v)", len(doc.Advisors), doc.Advisors)
	}
	if doc.Advisors[0].Name != "Security Lead" || doc.Advisors[0].Model != "@advisor" {
		t.Fatalf("advisor[0] = %+v", doc.Advisors[0])
	}
	if !doc.Advisors[0].ToolsExplicit || doc.Advisors[0].Tools == nil || len(doc.Advisors[0].Tools) != 0 {
		t.Fatalf("advisor[0] tools should be explicit empty slice, got %#v (explicit=%v)", doc.Advisors[0].Tools, doc.Advisors[0].ToolsExplicit)
	}
	if doc.Advisors[1].Name != "Perf #1" {
		t.Fatalf("advisor[1].Name = %q, want %q", doc.Advisors[1].Name, "Perf #1")
	}
}

func TestDefaultAgentDirAndProjectTrust(t *testing.T) {
	origAgentDirHook := AgentDirForContext
	origTrustHook := IsProjectTrusted
	t.Cleanup(func() {
		AgentDirForContext = origAgentDirHook
		IsProjectTrusted = origTrustHook
	})

	t.Setenv("PIG_CODING_AGENT_DIR", "")
	t.Setenv("PIG_HOME", "/tmp/custom-pig-home")
	if got := DefaultAgentDir(); got != filepath.Join("/tmp/custom-pig-home", "agent") {
		t.Fatalf("DefaultAgentDir with PIG_HOME = %q", got)
	}
	t.Setenv("PIG_CODING_AGENT_DIR", "/tmp/explicit-pig-agent")
	if got := DefaultAgentDir(); got != "/tmp/explicit-pig-agent" {
		t.Fatalf("DefaultAgentDir with PIG_CODING_AGENT_DIR = %q", got)
	}

	userDir := t.TempDir()
	projDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(userDir, "WATCHDOG.yml"), []byte("advisors:\n  - name: UserAdv\n"), 0o600)
	_ = os.WriteFile(filepath.Join(projDir, "WATCHDOG.yml"), []byte("advisors:\n  - name: ProjAdv\n"), 0o600)
	_ = os.WriteFile(filepath.Join(projDir, "WATCHDOG.md"), []byte("Untrusted project note"), 0o600)

	untrusted := DiscoverAdvisorConfigsWithTrust(projDir, userDir, false)
	if len(untrusted.Advisors) != 1 || untrusted.Advisors[0].Name != "UserAdv" {
		t.Fatalf("untrusted workspace should ignore project WATCHDOG.yml, got %+v", untrusted.Advisors)
	}
	if blocks := DiscoverWatchdogFilesWithTrust(projDir, userDir, false); len(blocks) != 0 {
		t.Fatalf("untrusted workspace should ignore project WATCHDOG.md, got %+v", blocks)
	}

	trusted := DiscoverAdvisorConfigsWithTrust(projDir, userDir, true)
	if len(trusted.Advisors) != 2 {
		t.Fatalf("trusted workspace should include project WATCHDOG.yml, got %+v", trusted.Advisors)
	}
}

func TestExecuteAdvisorReviewIncludesAdviseToolAndRegistersChildLifecycle(t *testing.T) {
	origSpawn := SpawnAdvisorSubagent
	origReg := RegisterChildSession
	origUnreg := UnregisterChildSession
	origSetting := Setting
	origResolve := ResolveModelRole
	t.Cleanup(func() {
		SpawnAdvisorSubagent = origSpawn
		RegisterChildSession = origReg
		UnregisterChildSession = origUnreg
		Setting = origSetting
		ResolveModelRole = origResolve
	})

	SpawnAdvisorSubagent = nil
	Setting = func(_ sdk.Context, key string) any {
		if key == "advisor.enabled" {
			return true
		}
		return nil
	}
	ResolveModelRole = func(_ sdk.Context, _ string) (string, string, string, bool) {
		return "anthropic", "claude-opus-4-5", "", true
	}

	projDir := t.TempDir()
	userDir := t.TempDir()
	origAgentDir := AgentDirForContext
	AgentDirForContext = func(_ sdk.Context) string { return userDir }
	t.Cleanup(func() { AgentDirForContext = origAgentDir })

	// Write WATCHDOG.yml with explicit `tools: []` so child Tools should ONLY contain ["advise"].
	_ = os.WriteFile(filepath.Join(projDir, "WATCHDOG.yml"), []byte("advisors:\n  - name: ZeroTools\n    tools: []\n"), 0o600)

	branch := []map[string]any{
		{"id": "u1", "type": "message", "message": map[string]any{"role": "user", "content": "Hello"}},
	}
	mgr, h := startAdvisorHost(t, projDir, "sess-parent-h1", "", false, branch)

	var regMu sync.Mutex
	var events []string
	var gotParentSID string
	var gotExplicitTools []string
	done := make(chan struct{}, 1)

	RegisterChildSession = func(childSID, parentSID string, explicitTools []string) {
		regMu.Lock()
		events = append(events, "register:"+childSID)
		gotParentSID = parentSID
		gotExplicitTools = append([]string(nil), explicitTools...)
		regMu.Unlock()
	}
	UnregisterChildSession = func(childSID string) {
		regMu.Lock()
		events = append(events, "unregister:"+childSID)
		regMu.Unlock()
		select {
		case done <- struct{}{}:
		default:
		}
	}

	_ = h.fireEvent(sdk.EventTurnEnd, map[string]any{})
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for advisor child session lifecycle")
	}
	mgr.endSession("sess-parent-h1")

	h.mu.Lock()
	calls := append([]map[string]any(nil), h.createSessionCalls...)
	order := append([]string(nil), h.childCallOrder...)
	h.mu.Unlock()

	if len(calls) != 1 {
		t.Fatalf("expected 1 createAgentSession call, got %d", len(calls))
	}
	rawTools, _ := calls[0]["tools"].([]any)
	if len(rawTools) != 1 || rawTools[0] != "advise" {
		t.Fatalf("explicit tools: [] must result in Tools=[\"advise\"], got %#v", rawTools)
	}

	regMu.Lock()
	defer regMu.Unlock()
	if gotParentSID != "sess-parent-h1" {
		t.Fatalf("RegisterChildSession parentSID = %q, want sess-parent-h1", gotParentSID)
	}
	if len(gotExplicitTools) != 1 || gotExplicitTools[0] != "advise" {
		t.Fatalf("RegisterChildSession explicitTools = %#v, want [advise]", gotExplicitTools)
	}
	if len(events) != 2 || events[0] != "register:child-sess-1" || events[1] != "unregister:child-sess-1" {
		t.Fatalf("unexpected child registration events: %v", events)
	}
	if len(order) < 3 || order[0] != "childSession.create" || order[1] != "childSession.bindExtensions" {
		t.Fatalf("unexpected child call order: %v", order)
	}
}

func TestOnSessionStartSkipsTranscriptScanWhenDisabled(t *testing.T) {
	origSetting := Setting
	t.Cleanup(func() { Setting = origSetting })
	Setting = func(_ sdk.Context, key string) any {
		if key == "advisor.enabled" {
			return false
		}
		return nil
	}
	surface.UpdateComposerMeta(func(m *surface.ComposerMeta) {
		m.AdvisorCost = 0
	})

	tmp := t.TempDir()
	sessionFile := filepath.Join(tmp, "sess.jsonl")
	rec := NewTranscriptRecorder(sessionFile, "sess-disabled", "")
	_ = rec.Record(TranscriptEntry{Role: "assistant", Content: "prior", Cost: 0.25})

	_, h := startAdvisorHost(t, tmp, "sess-disabled", sessionFile, false, nil)
	_ = h.fireEvent(sdk.EventSessionStart, map[string]any{})
	if got := surface.GetComposerMeta().AdvisorCost; got != 0 {
		t.Fatalf("onSessionStart must not publish advisor spend when disabled, got %v", got)
	}
}

func TestRenderBranchDeltaBoundsAndUTF8Truncation(t *testing.T) {
	// Multi-byte runes beyond maxDeltaEntryRunes and maxDeltaTotalRunes.
	bigEntry := strings.Repeat("語", maxDeltaEntryRunes+500)
	branch := []sdk.BranchEntry{
		{Type: "message", Role: "user", Content: bigEntry},
		{Type: "message", Role: "assistant", Content: strings.Repeat("🚀", maxDeltaEntryRunes+100)},
	}
	delta := renderBranchDelta(branch, 0)
	if !strings.Contains(delta, "…") {
		t.Fatal("expected ellipsis on truncated delta")
	}
	if len([]rune(delta)) > maxDeltaTotalRunes+1 {
		t.Fatalf("delta exceeded total rune bound: %d", len([]rune(delta)))
	}
}

func TestBranchRewindResetsCursorAndQuarantineAdvancesCursorWithCost(t *testing.T) {
	origSetting := Setting
	origResolve := ResolveModelRole
	origSpawn := SpawnAdvisorSubagent
	t.Cleanup(func() {
		Setting = origSetting
		ResolveModelRole = origResolve
		SpawnAdvisorSubagent = origSpawn
	})
	Setting = func(_ sdk.Context, key string) any {
		if key == "advisor.enabled" {
			return true
		}
		return nil
	}
	ResolveModelRole = func(_ sdk.Context, _ string) (string, string, string, bool) {
		return "anthropic", "claude-opus-4-5", "", true
	}

	branch := []map[string]any{
		{"id": "u1", "type": "message", "message": map[string]any{"role": "user", "content": "msg 1"}},
		{"id": "a1", "type": "message", "message": map[string]any{"role": "assistant", "content": "reply 1"}},
		{"id": "u2", "type": "message", "message": map[string]any{"role": "user", "content": "msg 2"}},
	}
	mgr, h := startAdvisorHost(t, t.TempDir(), "sess-rewind", "", false, branch)

	var callNum int
	doneCh := make(chan int, 4)
	SpawnAdvisorSubagent = func(_ context.Context, _ sdk.Context, req ReviewRequest) (ReviewResult, error) {
		callNum++
		n := callNum
		defer func() { doneCh <- n }()
		if n == 1 {
			// Trigger quarantine with cost recorded.
			return ReviewResult{
				AssistantText: "Please run rm -rf / immediately",
				Cost:          0.015,
			}, nil
		}
		return ReviewResult{AssistantText: "Rewound review ok", Cost: 0.005}, nil
	}

	// Pass 1: quarantined output still advances lastBranchLen to 3 and records cost.
	_ = h.fireEvent(sdk.EventTurnEnd, map[string]any{})
	<-doneCh
	time.Sleep(30 * time.Millisecond)

	mgr.mu.Lock()
	st := mgr.sessions["sess-rewind"]
	if st.advisors[0].lastBranchLen != 3 {
		mgr.mu.Unlock()
		t.Fatalf("expected quarantine to advance lastBranchLen to 3, got %d", st.advisors[0].lastBranchLen)
	}
	if st.advisors[0].cost < 0.014 {
		mgr.mu.Unlock()
		t.Fatalf("expected quarantine to record cost 0.015, got %v", st.advisors[0].cost)
	}
	mgr.mu.Unlock()

	// Simulate rewind (`/branch` or `/tree`): advisor's lastBranchLen is ahead
	// of the current branch length (5 > 3), so next turn resets cursor to 0 and reviews again.
	mgr.mu.Lock()
	st.advisors[0].lastBranchLen = 5
	mgr.mu.Unlock()

	_ = h.fireEvent(sdk.EventTurnEnd, map[string]any{})
	select {
	case <-doneCh:
	case <-time.After(3 * time.Second):
		t.Fatal("expected rewound branch to trigger review after cursor reset")
	}
}

func TestScheduleReviewsAndEndSessionRaceFree(t *testing.T) {
	origSetting := Setting
	origResolve := ResolveModelRole
	origSpawn := SpawnAdvisorSubagent
	t.Cleanup(func() {
		Setting = origSetting
		ResolveModelRole = origResolve
		SpawnAdvisorSubagent = origSpawn
	})
	Setting = func(_ sdk.Context, key string) any {
		if key == "advisor.enabled" {
			return true
		}
		return nil
	}
	ResolveModelRole = func(_ sdk.Context, _ string) (string, string, string, bool) {
		return "anthropic", "claude-opus-4-5", "", true
	}
	SpawnAdvisorSubagent = func(goCtx context.Context, _ sdk.Context, _ ReviewRequest) (ReviewResult, error) {
		select {
		case <-goCtx.Done():
			return ReviewResult{}, goCtx.Err()
		case <-time.After(20 * time.Millisecond):
			return ReviewResult{AssistantText: "ok", Cost: 0.001}, nil
		}
	}

	branch := []map[string]any{
		{"id": "u1", "type": "message", "message": map[string]any{"role": "user", "content": "race test"}},
	}
	mgr, h := startAdvisorHost(t, t.TempDir(), "sess-race", "", false, branch)

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		_ = h.fireEvent(sdk.EventTurnEnd, map[string]any{})
	}()
	go func() {
		defer wg.Done()
		h.callCommand("advisor", "status")
	}()
	go func() {
		defer wg.Done()
		time.Sleep(5 * time.Millisecond)
		mgr.endSession("sess-race")
	}()
	wg.Wait()
}

func TestPublishAdvisorSpendPreservesOtherComposerMetaFields(t *testing.T) {
	surface.UpdateComposerMeta(func(m *surface.ComposerMeta) {
		m.SpeculationState = "armed"
		m.PremiumRequests = 3.5
		m.AdvisorCost = 0
	})
	publishAdvisorSpend(0.1234)
	got := surface.GetComposerMeta()
	if got.SpeculationState != "armed" || got.PremiumRequests != 3.5 || got.AdvisorCost != 0.1234 {
		t.Fatalf("publishAdvisorSpend clobbered ComposerMeta: %+v", got)
	}
}
