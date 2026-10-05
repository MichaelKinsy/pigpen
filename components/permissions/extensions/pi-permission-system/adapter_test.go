package pi_permission_system_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	pps "github.com/MichaelKinsy/pigpen/pi-permission-system"
)

func eq(t *testing.T, got, want any, what string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: got %#v, want %#v", what, got, want)
	}
}

var agentDirForTest string

func configPath() string {
	return filepath.Join(agentDirForTest, "extensions", "pi-permission-system", "config.json")
}

// The SDK adapter in a fake interactive host: what the handlers send and return for each event.

type adapterRig struct {
	h      *Host
	active []string
	set    []string
}

func rigWith(t *testing.T, config string, active ...string) *adapterRig {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	agentDirForTest = dir
	if config != "" {
		p := configPath()
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(config), 0o644)
	}
	r := &adapterRig{active: active}
	all := []any{}
	for _, n := range []string{"read", "bash", "edit", "write", "task"} {
		all = append(all, map[string]any{"name": n})
	}
	r.h = StartHost(t, pps.Extension(), HostOptions{OnCall: func(method string, args map[string]any) (map[string]any, string) {
		switch method {
		case "getActiveTools":
			return map[string]any{"tools": r.active}, ""
		case "getAllTools":
			return map[string]any{"tools": all}, ""
		case "setActiveTools":
			r.set = nil
			for _, x := range args["tools"].([]any) {
				r.set = append(r.set, x.(string))
			}
			r.active = r.set
		}
		return map[string]any{}, ""
	}})
	return r
}

func (r *adapterRig) uiCalls(method string) []map[string]any {
	var out []map[string]any
	for _, c := range r.h.CallsTo(method) {
		out = append(out, c.Args)
	}
	return out
}

func (r *adapterRig) toolCall(name string, input map[string]any) (blocked bool, reason string) {
	raw := r.h.Fire("tool_call", map[string]any{"toolName": name, "input": input, "toolCallId": "c1"})
	var res struct {
		Block  bool   `json:"block"`
		Reason string `json:"reason"`
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &res)
	}
	return res.Block, res.Reason
}

func TestAdapterRegistersEvents(t *testing.T) {
	r := rigWith(t, "", "read", "bash")
	for _, ev := range []string{"session_start", "before_agent_start", "session_shutdown", "tool_call"} {
		eq(t, r.h.Registered(ev), true, ev)
	}
}

func TestAdapterToolCall(t *testing.T) {
	r := rigWith(t, `{"permission":{"path":"allow","external_directory":"allow","bash":{"*":"deny","echo hi":"allow"}}}`, "read", "bash")
	r.h.Fire("session_start", map[string]any{"reason": "startup"})
	blocked, _ := r.toolCall("bash", map[string]any{"command": "echo hi"})
	eq(t, blocked, false, "allowed")
	blocked, reason := r.toolCall("bash", map[string]any{"command": "echo bye"})
	eq(t, blocked, true, "denied")
	eq(t, reason, "[pi-permission-system] Denied by policy: 'bash' (rule '*').", "reason")
	// a call before session_start loads the policy itself
	r2 := rigWith(t, `{"permission":{"foo":"deny"}}`)
	blocked, _ = r2.toolCall("foo", nil)
	eq(t, blocked, true, "lazy load")
}

func TestAdapterSessionStart(t *testing.T) {
	r := rigWith(t, `{"yoloMode":true,"permission":{"*":"allow"}}`, "read")
	r.h.Fire("session_start", map[string]any{"reason": "startup"})
	st := r.uiCalls("ui.setStatus")
	eq(t, len(st), 1, "one status")
	eq(t, st[0]["key"], "pi-permission-system", "key")
	eq(t, st[0]["text"], "yolo", "yolo status")
	n := r.uiCalls("ui.notify")
	eq(t, len(n), 1, "the permissive-bash warning")
	eq(t, n[0]["level"], "warning", "level")
	eq(t, strings.HasPrefix(n[0]["message"].(string), "Permission config sets a permissive top-level '*'"), true, "text")

	bad := rigWith(t, `{"permission":`, "read")
	bad.h.Fire("session_start", map[string]any{})
	n = bad.uiCalls("ui.notify")
	eq(t, len(n), 1, "the config issue")
	eq(t, strings.HasPrefix(n[0]["message"].(string), "Failed to read config at '"), true, "text")
	eq(t, bad.uiCalls("ui.setStatus")[0]["text"], "", "status cleared")

	clean := rigWith(t, `{"permission":{"bash":"ask"}}`, "read")
	clean.h.Fire("session_start", map[string]any{})
	eq(t, len(clean.uiCalls("ui.notify")), 0, "no warning")
	// shutdown clears the status
	clean.h.Fire("session_shutdown", map[string]any{})
	st = clean.uiCalls("ui.setStatus")
	eq(t, len(st), 2, "start and shutdown")
	eq(t, st[1]["text"], "", "cleared")
}

func TestAdapterBeforeAgentStart(t *testing.T) {
	r := rigWith(t, `{"permission":{"write":"deny","edit":{"*":"deny","a":"allow"},"task":"deny"}}`, "read", "bash", "edit", "write", "task")
	r.h.Fire("session_start", map[string]any{})
	r.h.Fire("before_agent_start", map[string]any{"systemPrompt": "x", "systemPromptOptions": map[string]any{}})
	eq(t, r.set, []string{"read", "bash", "edit"}, "fully denied tools are withheld")
	st := r.uiCalls("ui.setStatus")
	eq(t, len(st), 2, "status on session start and on each prompt")
	// A relaxed policy restores them on the next prompt.
	p := configPath()
	os.WriteFile(p, []byte(`{"permission":{"write":"allow"}}`), 0o644)
	r.h.Fire("before_agent_start", map[string]any{"systemPrompt": "x", "systemPromptOptions": map[string]any{}})
	eq(t, r.set, []string{"read", "bash", "edit", "write", "task"}, "restored in place")
	// A new session forgets what was withheld.
	os.WriteFile(p, []byte(`{"permission":{"write":"deny"}}`), 0o644)
	r.h.Fire("before_agent_start", map[string]any{"systemPrompt": "x", "systemPromptOptions": map[string]any{}})
	eq(t, r.set, []string{"read", "bash", "edit", "task"}, "withheld again")
	r.active = []string{"read"}
	r.h.Fire("session_start", map[string]any{})
	os.WriteFile(p, []byte(`{}`), 0o644)
	r.h.Fire("before_agent_start", map[string]any{"systemPrompt": "x", "systemPromptOptions": map[string]any{}})
	eq(t, r.set, []string{"read"}, "session start reset the baseline")
}

// The skill-read gate: a read of a listed skill's file is refused while the skill surface asks (skill-read.ts).
func TestAdapterSkillRead(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	agentDirForTest = dir
	os.MkdirAll(filepath.Dir(configPath()), 0o755)
	os.WriteFile(configPath(), []byte(`{"permission":{"*":"ask","path":"allow","external_directory":"allow","read":"allow"}}`), 0o644)
	cwd := t.TempDir()
	h := StartHost(t, pps.Extension(), HostOptions{Cwd: cwd})
	r := &adapterRig{h: h}
	h.Fire("session_start", map[string]any{})
	prompt := "x\n<available_skills>\n<skill>\n<name>demo</name>\n<description>d</description>\n<location>" + cwd +
		"/skills/demo/SKILL.md</location>\n</skill>\n</available_skills>"
	blocked, _ := r.toolCall("read", map[string]any{"path": "skills/demo/SKILL.md"})
	eq(t, blocked, false, "no skills listed yet")
	h.Fire("before_agent_start", map[string]any{"systemPrompt": prompt, "systemPromptOptions": map[string]any{}})
	blocked, reason := r.toolCall("read", map[string]any{"path": "skills/demo/SKILL.md"})
	eq(t, blocked, true, "the skill's file")
	eq(t, strings.Contains(reason, "skill 'demo'"), true, "names the skill")
	blocked, _ = r.toolCall("read", map[string]any{"path": "notes.txt"})
	eq(t, blocked, false, "another file")
	h.Fire("session_start", map[string]any{})
	blocked, _ = r.toolCall("read", map[string]any{"path": "skills/demo/SKILL.md"})
	eq(t, blocked, false, "a new session forgets the listed skills until its first prompt")
}
