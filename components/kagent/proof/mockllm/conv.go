package main

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"sync/atomic"
)

// toolDef is one tool a client offered on the request.
type toolDef struct {
	Name      string
	Schema    map[string]any
	Namespace string // Responses API: function nested in a namespace tool (Codex MCP tools)
}

// convMsg is one protocol-neutral conversation entry.
type convMsg struct {
	Role       string // user, assistant, system, tool
	Text       string
	ToolResult bool     // carries tool results (not a prompt)
	Results    []string // tool result texts
	Calls      []string // assistant tool call names
}

// conv is the protocol-neutral view of a model request.
type conv struct {
	API          string // chat, responses, anthropic
	Model        string
	Stream       bool
	IncludeUsage bool
	Msgs         []convMsg
	Tools        []toolDef
	InputBytes   int
}

type call struct {
	Name      string
	Args      string // JSON text
	Namespace string
}

// reply is what the scenario decided to answer.
type reply struct {
	Text string
	Call *call
	Step string
}

var (
	sysReminder = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)
	markerRe    = regexp.MustCompile(`\[([A-Za-z0-9][A-Za-z0-9_.:-]{0,63})\]`)
	shellNames  = []string{"bash", "shell", "Bash", "exec_command", "shell_command"}
)

const (
	shellCmd = "echo basket-ok > /tmp/basket.txt && cat /tmp/basket.txt"
	probeSfx = "kagent_probe"
)

func cleanPrompt(s string) string {
	return strings.TrimSpace(sysReminder.ReplaceAllString(s, ""))
}

func isPrompt(m convMsg) bool {
	return m.Role == "user" && !m.ToolResult && cleanPrompt(m.Text) != ""
}

func hasScenarioMarker(t string) bool {
	return strings.Contains(t, "[basket]") || strings.Contains(t, "[approve]") || strings.Contains(t, "[recall]")
}

// lastPrompt returns the index of the message that drives planning.
//
// It is the last user message containing a scenario marker, so a short continuation that a harness appends
// after an injected tool result ("Continue.") does not replace it. A user message without a marker after
// that one is a nudge, and is ignored, only while the scenario is pending: the tail after the last tool
// result holds nothing but such user messages and no assistant has given final text since the marker
// message. Any other later user message (for example after BASKET-DONE) is a new prompt and is echoed.
// With no marker anywhere the last user message is the prompt.
func (c *conv) lastPrompt() int {
	last, marker := -1, -1
	for i, m := range c.Msgs {
		if !isPrompt(m) {
			continue
		}
		last = i
		if hasScenarioMarker(cleanPrompt(m.Text)) {
			marker = i
		}
	}
	if marker < 0 || last == marker {
		return last
	}
	tool := -1
	for i := marker + 1; i < len(c.Msgs); i++ {
		m := c.Msgs[i]
		if m.Role == "assistant" && len(m.Calls) == 0 && strings.TrimSpace(m.Text) != "" {
			return last // scenario already answered: the later message is a new prompt
		}
		if m.ToolResult {
			tool = i
		}
	}
	if tool < 0 {
		return last
	}
	for _, m := range c.Msgs[tool+1:] {
		if m.Role != "user" || m.ToolResult {
			return last
		}
	}
	return marker
}

// isContextMsg reports user messages that harnesses inject as context, not prompts.
func isContextMsg(t string) bool {
	t = strings.TrimSpace(t)
	return strings.HasPrefix(t, "<") || strings.HasPrefix(t, "# AGENTS.md") || strings.Contains(t, "<INSTRUCTIONS>")
}

// firstMarker finds the first [marker] in user prompts, skipping [recall].
func (c *conv) firstMarker() (string, bool) {
	for _, m := range c.Msgs {
		if m.Role != "user" || m.ToolResult {
			continue
		}
		t := cleanPrompt(m.Text)
		if t == "" || isContextMsg(t) {
			continue
		}
		for _, loc := range markerRe.FindAllStringSubmatchIndex(t, -1) {
			end := loc[1]
			if end < len(t) && (t[end] == '(' || t[end] == ':') {
				continue
			}
			mk := t[loc[0]:loc[1]]
			if mk == "[recall]" {
				continue
			}
			return mk, true
		}
	}
	return "", false
}

func (c *conv) firstUserText() string {
	for _, m := range c.Msgs {
		if m.Role == "user" && !m.ToolResult {
			t := cleanPrompt(m.Text)
			if t != "" && !isContextMsg(t) {
				return t
			}
		}
	}
	return ""
}

func (c *conv) findTool(pred func(string) bool) *toolDef {
	for i := range c.Tools {
		if pred(c.Tools[i].Name) {
			return &c.Tools[i]
		}
	}
	return nil
}

func (c *conv) shellTool() *toolDef {
	for _, n := range shellNames {
		if t := c.findTool(func(x string) bool { return x == n }); t != nil {
			return t
		}
	}
	if len(c.Tools) > 0 {
		return &c.Tools[0]
	}
	return nil
}

// probeTool picks the kagent_probe tool. Several MCP servers can offer it (kagent wires one server twice, once with
// requireApproval). A tool counts as approval-gated when its name or namespace contains "approval", for example
// Claude's mcp__mock-mcp-approval__kagent_probe or Codex's namespace mcp__mock-mcp-approval. With approval wanted the
// gated tool is chosen (else the last candidate when there are several, the later-registered server); without it the
// first ungated one (else the first).
func (c *conv) probeTool(approval bool) *toolDef {
	var cands []*toolDef
	for i := range c.Tools {
		if strings.HasSuffix(c.Tools[i].Name, probeSfx) {
			cands = append(cands, &c.Tools[i])
		}
	}
	if len(cands) == 0 {
		return nil
	}
	gated := func(t *toolDef) bool { return strings.Contains(strings.ToLower(t.Namespace+"/"+t.Name), "approval") }
	for _, t := range cands {
		if gated(t) == approval {
			return t
		}
	}
	if approval {
		return cands[len(cands)-1]
	}
	return cands[0]
}

func zeroFor(typ any) any {
	switch typ {
	case "integer", "number":
		return 0
	case "boolean":
		return false
	case "array":
		return []any{}
	case "object":
		return map[string]any{}
	}
	return ""
}

// commandArgs builds arguments that run cmd for the client's shell tool.
func commandArgs(t *toolDef, cmd string) string {
	props, _ := t.Schema["properties"].(map[string]any)
	args := map[string]any{}
	switch {
	case props["cmd"] != nil:
		args["cmd"] = cmd
	case props["command"] != nil:
		p, _ := props["command"].(map[string]any)
		if p["type"] == "array" {
			args["command"] = []string{"bash", "-lc", cmd}
		} else {
			args["command"] = cmd
		}
	default:
		// first string property by schema order is unknowable from a map; prefer common names
		for _, k := range []string{"script", "input", "code", "text", "query"} {
			if props[k] != nil {
				args[k] = cmd
				break
			}
		}
	}
	if req, ok := t.Schema["required"].([]any); ok {
		for _, r := range req {
			k, _ := r.(string)
			if _, set := args[k]; set || k == "" {
				continue
			}
			p, _ := props[k].(map[string]any)
			if len(args) == 0 && p["type"] == "string" {
				args[k] = cmd
				continue
			}
			args[k] = zeroFor(p["type"])
		}
	}
	return string(marshal(args))
}

func shaHex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// resultsAfter returns the tool results that follow message pi, in order.
func (c *conv) resultsAfter(pi int) []string {
	var results []string
	for _, m := range c.Msgs[pi+1:] {
		if m.ToolResult {
			results = append(results, m.Results...)
		}
	}
	return results
}

// maxLoggedResults bounds the tool result text the request log carries.
const maxLoggedResults = 4096

// loggedResults returns the tool results the current scenario step is answering, joined exactly as plan joins them for
// the answer sha (so a reader can recompute the sha), and whether the text was cut to maxLoggedResults. Tool results in
// the proof are the mock's own probe output or a harness's rejection text, never credentials.
func (c *conv) loggedResults() (string, bool) {
	pi := c.lastPrompt()
	if pi < 0 {
		return "", false
	}
	j := strings.Join(c.resultsAfter(pi), "")
	if len(j) > maxLoggedResults {
		return j[:maxLoggedResults], true
	}
	return j, false
}

// plan maps conversation state to the scenario reply. It never looks at the model name.
func plan(c *conv) reply {
	pi := c.lastPrompt()
	if pi < 0 {
		return reply{Text: "ECHO ", Step: "echo"}
	}
	prompt := cleanPrompt(c.Msgs[pi].Text)
	basket := strings.Contains(prompt, "[basket]")
	approve := strings.Contains(prompt, "[approve]")
	recall := strings.Contains(prompt, "[recall]")

	results := c.resultsAfter(pi)
	n := len(results)

	switch {
	case recall:
		if mk, ok := c.firstMarker(); ok {
			return reply{Text: "RECALL " + mk, Step: "recall"}
		}
		return reply{Text: "RECALL " + c.firstUserText(), Step: "recall"}
	case basket || approve:
		done := "BASKET-DONE "
		if !basket {
			done = "APPROVE-DONE "
		}
		final := reply{Text: done + shaHex(strings.Join(results, "")), Step: "final"}
		id := shaHex(prompt)[:8]
		if !basket { // approve only: single probe call
			if n == 0 {
				if pt := c.probeTool(true); pt != nil {
					return reply{Call: &call{pt.Name, `{"note":"basket"}`, pt.Namespace}, Step: "probe:" + id}
				}
				return reply{Text: "APPROVE-NOPROBE", Step: "final"}
			}
			return final
		}
		switch n {
		case 0:
			st := c.shellTool()
			if st == nil {
				return reply{Text: "BASKET-NOTOOLS", Step: "final"}
			}
			return reply{Call: &call{st.Name, commandArgs(st, shellCmd), st.Namespace}, Step: "shell:" + id}
		case 1:
			if pt := c.probeTool(approve); pt != nil {
				return reply{Call: &call{pt.Name, `{"note":"basket"}`, pt.Namespace}, Step: "probe:" + id}
			}
			return final
		}
		return final
	}
	return reply{Text: "ECHO " + prompt, Step: "echo"}
}

func (c *conv) hash() string {
	h := sha256.New()
	for _, m := range c.Msgs {
		h.Write([]byte(m.Role + "\x00" + m.Text + "\x00"))
		for _, r := range m.Results {
			h.Write([]byte(r + "\x01"))
		}
		for _, k := range m.Calls {
			h.Write([]byte(k + "\x02"))
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

func (c *conv) toolNames() []string {
	out := make([]string, 0, len(c.Tools))
	for _, t := range c.Tools {
		if t.Namespace != "" {
			out = append(out, t.Namespace+"/"+t.Name)
		} else {
			out = append(out, t.Name)
		}
	}
	return out
}

// usage returns deterministic token counts derived from input and output length.
func usageFor(c *conv, r reply) (in, out int) {
	in = (c.InputBytes + 3) / 4
	if in < 1 {
		in = 1
	}
	n := len(r.Text)
	if r.Call != nil {
		n = len(r.Call.Name) + len(r.Call.Args)
	}
	out = (n + 3) / 4
	if out < 1 {
		out = 1
	}
	return
}

func callID(prefix string, r reply, c *conv) string {
	return prefix + shaHex(c.hash() + r.Step)[:20]
}

// ---- generic JSON helpers ----

func asMap(v any) map[string]any { m, _ := v.(map[string]any); return m }
func asSlice(v any) []any        { s, _ := v.([]any); return s }
func asStr(v any) string         { s, _ := v.(string); return s }

// contentText flattens a string or array-of-parts content value.
func contentText(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		var sb strings.Builder
		for _, p := range t {
			pm := asMap(p)
			if pm == nil {
				continue
			}
			switch asStr(pm["type"]) {
			case "text", "input_text", "output_text", "":
				sb.WriteString(asStr(pm["text"]))
			}
		}
		return sb.String()
	}
	return ""
}

func addBytes(c *conv, s string) { c.InputBytes += len(s) }

func parseChat(body map[string]any) *conv {
	c := &conv{API: "chat", Model: asStr(body["model"])}
	c.Stream, _ = body["stream"].(bool)
	if so := asMap(body["stream_options"]); so != nil {
		c.IncludeUsage, _ = so["include_usage"].(bool)
	}
	for _, mv := range asSlice(body["messages"]) {
		m := asMap(mv)
		role := asStr(m["role"])
		text := contentText(m["content"])
		addBytes(c, text)
		cm := convMsg{Role: role, Text: text}
		switch role {
		case "tool", "function":
			cm.Role, cm.ToolResult, cm.Results = "user", true, []string{text}
		case "developer":
			cm.Role = "system"
		case "assistant":
			for _, tc := range asSlice(m["tool_calls"]) {
				cm.Calls = append(cm.Calls, asStr(asMap(asMap(tc)["function"])["name"]))
			}
		}
		c.Msgs = append(c.Msgs, cm)
	}
	for _, tv := range asSlice(body["tools"]) {
		t := asMap(tv)
		fn := asMap(t["function"])
		if fn == nil {
			fn = t
		}
		if n := asStr(fn["name"]); n != "" {
			c.Tools = append(c.Tools, toolDef{Name: n, Schema: asMap(fn["parameters"])})
		}
	}
	return c
}

func parseResponses(body map[string]any) *conv {
	c := &conv{API: "responses", Model: asStr(body["model"])}
	c.Stream, _ = body["stream"].(bool)
	if ins := asStr(body["instructions"]); ins != "" {
		addBytes(c, ins)
		c.Msgs = append(c.Msgs, convMsg{Role: "system", Text: ins})
	}
	switch in := body["input"].(type) {
	case string:
		addBytes(c, in)
		c.Msgs = append(c.Msgs, convMsg{Role: "user", Text: in})
	case []any:
		for _, iv := range in {
			it := asMap(iv)
			typ := asStr(it["type"])
			switch typ {
			case "function_call_output", "custom_tool_call_output", "local_shell_call_output":
				out := it["output"]
				var s string
				if str, ok := out.(string); ok {
					s = str
				} else if om := asMap(out); om != nil {
					s = contentText(om["content"])
					if s == "" {
						s = asStr(om["text"])
					}
				} else {
					s = contentText(out)
				}
				addBytes(c, s)
				c.Msgs = append(c.Msgs, convMsg{Role: "user", ToolResult: true, Results: []string{s}})
			case "function_call", "custom_tool_call", "local_shell_call":
				c.Msgs = append(c.Msgs, convMsg{Role: "assistant", Calls: []string{asStr(it["name"])}})
			case "message", "":
				role := asStr(it["role"])
				if role == "developer" {
					role = "system"
				}
				text := contentText(it["content"])
				addBytes(c, text)
				c.Msgs = append(c.Msgs, convMsg{Role: role, Text: text})
			}
		}
	}
	for _, tv := range asSlice(body["tools"]) {
		t := asMap(tv)
		if n := asStr(t["name"]); n != "" && asStr(t["type"]) != "namespace" {
			c.Tools = append(c.Tools, toolDef{Name: n, Schema: asMap(t["parameters"])})
		}
		// Codex nests MCP tools: {"type":"namespace","name":"mcp__srv","tools":[{"type":"function",...}]}
		for _, cv := range asSlice(t["tools"]) {
			ch := asMap(cv)
			if n := asStr(ch["name"]); n != "" {
				c.Tools = append(c.Tools, toolDef{Name: n, Schema: asMap(ch["parameters"]), Namespace: asStr(t["name"])})
			}
		}
	}
	return c
}

func parseAnthropic(body map[string]any) *conv {
	c := &conv{API: "anthropic", Model: asStr(body["model"])}
	c.Stream, _ = body["stream"].(bool)
	if sys := contentText(body["system"]); sys != "" {
		addBytes(c, sys)
		c.Msgs = append(c.Msgs, convMsg{Role: "system", Text: sys})
	}
	for _, mv := range asSlice(body["messages"]) {
		m := asMap(mv)
		role := asStr(m["role"])
		cm := convMsg{Role: role}
		switch cont := m["content"].(type) {
		case string:
			cm.Text = cont
		case []any:
			var sb strings.Builder
			for _, bv := range cont {
				b := asMap(bv)
				switch asStr(b["type"]) {
				case "text":
					sb.WriteString(asStr(b["text"]))
				case "tool_result":
					cm.ToolResult = true
					cm.Results = append(cm.Results, contentText(b["content"]))
				case "tool_use":
					cm.Calls = append(cm.Calls, asStr(b["name"]))
				}
			}
			cm.Text = sb.String()
		}
		addBytes(c, cm.Text)
		for _, r := range cm.Results {
			addBytes(c, r)
		}
		c.Msgs = append(c.Msgs, cm)
	}
	for _, tv := range asSlice(body["tools"]) {
		t := asMap(tv)
		if n := asStr(t["name"]); n != "" {
			c.Tools = append(c.Tools, toolDef{Name: n, Schema: asMap(t["input_schema"])})
		}
	}
	return c
}

// chunks splits s into pieces of at most n bytes on rune boundaries.
func chunks(s string, n int) []string {
	if n < 1 {
		n = 8
	}
	var out []string
	for len(s) > 0 {
		k := n
		if k >= len(s) {
			out = append(out, s)
			break
		}
		for k < len(s) && k > 0 && s[k]&0xC0 == 0x80 {
			k++
		}
		out = append(out, s[:k])
		s = s[k:]
	}
	return out
}

// holds is how many model calls the hold markers may still block (MOCK_HOLD_COUNT; 0 or unset: none). A held call is
// never answered, so the turn that made it is still running until its client goes away; the crash-and-revert check
// uses it to make an Actor crash in the middle of a turn.
var holds = func() *atomic.Int64 {
	var n atomic.Int64
	n.Store(int64(envInt("MOCK_HOLD_COUNT", 0)))
	return &n
}()

// shouldHold reports whether this call is held: a prompt with [hold] holds its first model call, one with [holdafter]
// holds the call that follows its first tool result, as long as the budget lasts.
func (c *conv) shouldHold() bool {
	pi := c.lastPrompt()
	if pi < 0 {
		return false
	}
	prompt := cleanPrompt(c.Msgs[pi].Text)
	switch {
	case strings.Contains(prompt, "[hold]"):
	case strings.Contains(prompt, "[holdafter]") && len(c.resultsAfter(pi)) > 0:
	default:
		return false
	}
	return holds.Add(-1) >= 0
}
