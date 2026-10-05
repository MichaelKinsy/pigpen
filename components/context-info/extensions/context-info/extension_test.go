package contextinfo_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	contextinfo "github.com/MichaelKinsy/pigpen/context-info"
)

// Layer-1 cases through the fake host: what the extension registers, what it
// does at session start, and how it reports host failures.

// answers is a small scriptable host: replies keyed by method.
type answers map[string]map[string]any

func (a answers) onCall(method string, _ map[string]any) (map[string]any, string) {
	if r, ok := a[method]; ok {
		return r, ""
	}
	switch method {
	case "getContextUsage":
		return map[string]any{"tokens": 1200, "contextWindow": 8000, "percent": 15.0}, ""
	case "getThinkingLevel":
		return map[string]any{"level": "off"}, ""
	case "getActiveTools":
		return map[string]any{"tools": []string{"read"}}, ""
	case "getAllTools":
		return map[string]any{"tools": []map[string]any{{"name": "read", "description": "Read"}, {"name": "bash", "description": "Run"}}}, ""
	case "getSystemPrompt":
		return map[string]any{"prompt": "You are a coding agent."}, ""
	case "getCommands":
		return map[string]any{"commands": []map[string]any{}}, ""
	case "getModelInfo":
		return map[string]any{"id": "m-1", "provider": "acme", "contextWindow": 8000}, ""
	}
	return nil, ""
}

func footerCalls(h *Host) (set, cleared int) {
	for _, c := range h.CallsTo("ui.setFooter") {
		if c.Args["clear"] == true {
			cleared++
		} else if lines, _ := c.Args["lines"].([]any); len(lines) > 0 {
			set++
		}
	}
	return set, cleared
}

func TestRegistersTheInspectionCommands(t *testing.T) {
	h := StartHost(t, contextinfo.Extension(), HostOptions{})
	for _, name := range []string{"context", "tools", "cost", "prompts", "context-footer"} {
		if !h.cmds[name] {
			t.Errorf("command /%s is not registered", name)
		}
	}
	if h.cmds["prompt-edit"] {
		t.Error("/prompt-edit edits an overlay file nothing in this Package reads; it must not be registered")
	}
	if len(h.tools) != 0 {
		t.Errorf("tools = %v, want none", h.tools)
	}
}

func TestFooterStaysOffUntilEnabled(t *testing.T) {
	h := StartHost(t, contextinfo.Extension(), HostOptions{OnCall: answers{}.onCall})
	h.Fire("session_start", map[string]any{"reason": "startup"})
	h.Fire("turn_end", nil)
	h.Fire("model_select", nil)
	if set, cleared := footerCalls(h); set != 0 || cleared != 0 {
		t.Fatalf("footer touched while disabled: set=%d cleared=%d", set, cleared)
	}
	if n := len(h.CallsTo("ui.setStatus")); n != 0 {
		t.Errorf("%d status updates while the footer is off", n)
	}
}

func TestFooterFlagEnablesItAtSessionStart(t *testing.T) {
	h := StartHost(t, contextinfo.Extension(), HostOptions{OnCall: answers{"getFlag": {"value": true}}.onCall})
	h.Fire("session_start", map[string]any{"reason": "startup"})
	if set, _ := footerCalls(h); set == 0 {
		t.Fatal("--context-footer did not paint the footer at session start")
	}
}

func TestContextFooterCommandTogglesTheFooter(t *testing.T) {
	h := StartHost(t, contextinfo.Extension(), HostOptions{OnCall: answers{}.onCall})
	h.Fire("session_start", map[string]any{"reason": "startup"})
	if e := h.Command("context-footer", "on"); e != "" {
		t.Fatalf("/context-footer on: %s", e)
	}
	if set, _ := footerCalls(h); set == 0 {
		t.Fatal("/context-footer on did not set the footer")
	}
	h.Fire("turn_end", nil)
	before, _ := footerCalls(h)
	if e := h.Command("context-footer", "off"); e != "" {
		t.Fatalf("/context-footer off: %s", e)
	}
	if _, cleared := footerCalls(h); cleared != 1 {
		t.Fatalf("/context-footer off cleared the footer %d times, want 1", cleared)
	}
	h.Fire("turn_end", nil)
	if after, _ := footerCalls(h); after != before {
		t.Errorf("footer repainted after it was turned off (%d -> %d)", before, after)
	}
}

func TestContextFooterRejectsUnknownArgument(t *testing.T) {
	h := StartHost(t, contextinfo.Extension(), HostOptions{OnCall: answers{}.onCall})
	if e := h.Command("context-footer", "sideways"); e != "" {
		t.Fatalf("a usage message is a notification, not a failure: %s", e)
	}
	notes := h.CallsTo("ui.notify")
	if len(notes) == 0 || !strings.Contains(notes[len(notes)-1].Args["message"].(string), "on|off") {
		t.Fatalf("notifications = %+v, want a usage line", notes)
	}
}

func TestSessionStartLeavesTheConfigHomeAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	stale := filepath.Join(home, "prompt-overlays.json")
	if err := os.WriteFile(stale, []byte(`{"main":"keep me"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	h := StartHost(t, contextinfo.Extension(), HostOptions{OnCall: answers{}.onCall})
	h.Fire("session_start", map[string]any{"reason": "startup"})
	if _, err := os.Stat(stale); err != nil {
		t.Fatalf("session_start touched a file it does not own: %v", err)
	}
}

func TestContextCommandReportsAHostFailure(t *testing.T) {
	h := StartHost(t, contextinfo.Extension(), HostOptions{OnCall: func(method string, args map[string]any) (map[string]any, string) {
		if method == "getSystemPrompt" {
			return nil, "host went away"
		}
		return answers{}.onCall(method, args)
	}})
	if e := h.Command("context", ""); !strings.Contains(e, "host went away") {
		t.Fatalf("error = %q, want the host failure to reach the user", e)
	}
}

func TestContextCommandShowsTheBreakdown(t *testing.T) {
	h := StartHost(t, contextinfo.Extension(), HostOptions{OnCall: answers{}.onCall})
	if e := h.Command("context", ""); e != "" {
		t.Fatalf("/context failed: %s", e)
	}
	notes := h.CallsTo("ui.notify")
	if len(notes) != 1 {
		t.Fatalf("%d notifications, want 1", len(notes))
	}
	text, _ := notes[0].Args["message"].(string)
	if strings.Contains(text, "Cost:            cumulative") || strings.Contains(text, "rates (exact)") {
		t.Errorf("/context claims exact cost, but cost is tokens x model rates:\n%s", text)
	}
	for _, want := range []string{"Context Window", "System prompt", "1,200", "an estimate, not a bill"} {
		if !strings.Contains(text, want) {
			t.Errorf("/context output missing %q:\n%s", want, text)
		}
	}
}

func TestUnknownContextUsageAfterCompactionDoesNotBreakTheFooter(t *testing.T) {
	// Pi reports tokens and percent as null right after compaction, until the next response.
	h := StartHost(t, contextinfo.Extension(), HostOptions{OnCall: answers{
		"getFlag":         {"value": true},
		"getContextUsage": {"tokens": nil, "contextWindow": 8000, "percent": nil},
	}.onCall})
	h.Fire("session_start", map[string]any{"reason": "startup"})
	h.Fire("turn_end", nil)
	if set, _ := footerCalls(h); set == 0 {
		t.Fatal("footer not painted with unknown usage")
	}
	for _, c := range h.CallsTo("ui.setFooter") {
		for _, l := range c.Args["lines"].([]any) {
			if strings.Contains(l.(string), "NaN") || strings.Contains(l.(string), "0.0%") {
				t.Errorf("footer shows a made-up percentage: %q", l)
			}
		}
	}
}

func TestPromptsAgentsListsAgentDefinitionsFromTheConfigHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	def := "---\nname: scout\ndescription: finds things\n---\nYou scout the codebase.\n"
	if err := os.WriteFile(filepath.Join(home, "agents", "scout.md"), []byte(def), 0o644); err != nil {
		t.Fatal(err)
	}
	h := StartHost(t, contextinfo.Extension(), HostOptions{OnCall: answers{}.onCall})
	if e := h.Command("prompts", "agents"); e != "" {
		t.Fatalf("/prompts agents: %s", e)
	}
	notes := h.CallsTo("ui.notify")
	if len(notes) != 1 || !strings.Contains(notes[0].Args["message"].(string), "scout") {
		t.Fatalf("notifications = %+v, want the scout agent listed", notes)
	}
}

func TestZeroTokensAreTreatedAsNoLiveUsage(t *testing.T) {
	h := StartHost(t, contextinfo.Extension(), HostOptions{OnCall: answers{
		"getContextUsage": {"tokens": 0, "contextWindow": 8000, "percent": 0.0},
	}.onCall})
	if e := h.Command("context", ""); e != "" {
		t.Fatalf("/context failed: %s", e)
	}
	text, _ := h.CallsTo("ui.notify")[0].Args["message"].(string)
	if !strings.Contains(text, "est.") || strings.Contains(text, "tokens (0.0%)") {
		t.Errorf("a zero token count is not live usage; want the estimate:\n%s", text)
	}
}

func TestZeroTokensDoNotPaintALiveZeroPercentInTheFooter(t *testing.T) {
	h := StartHost(t, contextinfo.Extension(), HostOptions{OnCall: answers{
		"getFlag":         {"value": true},
		"getContextUsage": {"tokens": 0, "contextWindow": 8000, "percent": 0.0},
	}.onCall})
	h.Fire("session_start", map[string]any{"reason": "startup"})
	for _, c := range h.CallsTo("ui.setFooter") {
		for _, l := range c.Args["lines"].([]any) {
			if strings.Contains(l.(string), "0.0%") {
				t.Errorf("footer presents zero tokens as live usage: %q", l)
			}
		}
	}
}

// Agent files are the user's own: a tiny file or one without frontmatter must not
// break /prompts, and a Markdown rule (---) in a plain file is body, not frontmatter.
func TestPromptsHandlesShortAndFrontmatterFreeAgentFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	agents := filepath.Join(home, "agents")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"tiny.md":  "hi",
		"empty.md": "",
		"plain.md": "Intro line\n---\nText after a rule\n",
	} {
		if err := os.WriteFile(filepath.Join(agents, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h := StartHost(t, contextinfo.Extension(), HostOptions{OnCall: answers{}.onCall})
	if e := h.Command("prompts", "agents"); e != "" {
		t.Fatalf("/prompts agents: %s", e)
	}
	list := h.CallsTo("ui.notify")[0].Args["message"].(string)
	for _, want := range []string{"tiny", "empty", "plain", "Intro line"} {
		if !strings.Contains(list, want) {
			t.Errorf("/prompts agents is missing %q:\n%s", want, list)
		}
	}
	if e := h.Command("prompts", "tiny"); e != "" {
		t.Fatalf("/prompts tiny: %s", e)
	}
	if e := h.Command("prompts", "plain"); e != "" {
		t.Fatalf("/prompts plain: %s", e)
	}
	notes := h.CallsTo("ui.notify")
	plain := notes[len(notes)-1].Args["message"].(string)
	if !strings.Contains(plain, "Intro line") || !strings.Contains(plain, "Text after a rule") {
		t.Errorf("/prompts plain must show the whole file (it has no frontmatter):\n%s", plain)
	}
}

// With no live usage (null after compaction) /context must say the total is an
// estimate, not "from provider usage response (exact)".
func TestContextCommandSaysTheTotalIsEstimatedWithoutLiveUsage(t *testing.T) {
	h := StartHost(t, contextinfo.Extension(), HostOptions{OnCall: answers{
		"getContextUsage": {"tokens": nil, "contextWindow": 8000, "percent": nil},
	}.onCall})
	if e := h.Command("context", ""); e != "" {
		t.Fatalf("/context failed: %s", e)
	}
	text := h.CallsTo("ui.notify")[0].Args["message"].(string)
	if strings.Contains(text, "(exact)") && strings.Contains(text, "Context total:   from provider") {
		t.Errorf("/context claims an exact total without provider usage:\n%s", text)
	}
	if !strings.Contains(text, "Context total:   estimated") {
		t.Errorf("/context should say the total is estimated:\n%s", text)
	}
}
