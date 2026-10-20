package pi_permission_system_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pps "github.com/MichaelKinsy/pigpen/pi-permission-system"
)

// The scopes and handlers the port does not read yet refuse instead of allowing (unported.go): project scope, agent scope, the
// `/skill:` input gate and the config options that nothing reads.

const refusalHead = "@pi-in-go/pigpen-permissions (Go port) does not support "
const refusalTail = " yet. Install the original extension, npm:@gotgenes/pi-permission-system@39.0.3, which runs unchanged in PiG."
const wideOpen = `"path":"allow","external_directory":"allow"`

type scopeRig struct {
	h       *Host
	cwd     string
	agent   string
	trusted bool
	entries []map[string]any
}

func newScopeRig(t *testing.T, config string) *scopeRig {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	agentDirForTest = dir
	if config != "" {
		os.MkdirAll(filepath.Dir(configPath()), 0o755)
		os.WriteFile(configPath(), []byte(config), 0o644)
	}
	r := &scopeRig{cwd: t.TempDir(), agent: dir, trusted: true}
	r.h = StartHost(t, pps.Extension(), HostOptions{Cwd: r.cwd, OnCall: func(method string, args map[string]any) (map[string]any, string) {
		switch method {
		case "isProjectTrusted":
			return map[string]any{"trusted": r.trusted}, ""
		case "watchSessionLog":
			entries := make([]any, len(r.entries))
			for i, e := range r.entries {
				entries[i] = e
			}
			if args["complete"] == true { // the follow-up page: nothing new
				return map[string]any{"entries": []any{}, "entryCount": len(entries), "hasMore": false, "leafId": ""}, ""
			}
			return map[string]any{"entries": entries, "entryCount": len(entries), "hasMore": false, "leafId": ""}, ""
		case "getActiveTools":
			return map[string]any{"tools": []string{"read", "bash"}}, ""
		case "getAllTools":
			return map[string]any{"tools": []any{map[string]any{"name": "read"}, map[string]any{"name": "bash"}}}, ""
		}
		return map[string]any{}, ""
	}})
	return r
}

func (r *scopeRig) call(tool string, input map[string]any) (bool, string) {
	raw := r.h.Fire("tool_call", map[string]any{"toolName": tool, "input": input, "toolCallId": "c1"})
	var res struct {
		Block  bool   `json:"block"`
		Reason string `json:"reason"`
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &res)
	}
	return res.Block, res.Reason
}

func (r *scopeRig) input(text string) string {
	raw := r.h.Fire("input", map[string]any{"text": text, "source": "interactive"})
	var res struct {
		Action string `json:"action"`
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &res)
	}
	return res.Action
}

func (r *scopeRig) notices() []string {
	var out []string
	for _, c := range r.h.CallsTo("ui.notify") {
		out = append(out, c.Args["message"].(string))
	}
	return out
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestProjectScopeIsRefusedNotIgnored(t *testing.T) {
	r := newScopeRig(t, `{"permission":{`+wideOpen+`,"bash":"allow"}}`)
	r.h.Fire("session_start", map[string]any{})
	blocked, _ := r.call("bash", map[string]any{"command": "ls"})
	eq(t, blocked, false, "allowed with no project config")

	project := filepath.Join(r.cwd, ".pi", "extensions", "pi-permission-system", "config.json")
	write(t, project, `{"permission":{"bash":"deny"}}`)
	blocked, reason := r.call("bash", map[string]any{"command": "ls"})
	eq(t, blocked, true, "a project rule the port cannot read: blocked, not allowed")
	eq(t, reason, refusalHead+"project permission rules ("+project+")"+refusalTail, "exact text")

	// A call the global scope denies stays denied.
	write(t, configPath(), `{"permission":{`+wideOpen+`,"bash":"deny"}}`)
	r.h.Fire("session_start", map[string]any{})
	_, reason = r.call("bash", map[string]any{"command": "ls"})
	eq(t, strings.Contains(reason, "Denied by policy: 'bash'"), true, "the global deny still names itself")

	// An untrusted project's config is withheld by the original too, so it is not a refusal here either.
	write(t, configPath(), `{"permission":{`+wideOpen+`,"bash":"allow"}}`)
	r.trusted = false
	r.h.Fire("session_start", map[string]any{})
	blocked, _ = r.call("bash", map[string]any{"command": "ls"})
	eq(t, blocked, false, "untrusted project: its scope does not apply")
}

func TestProjectScopeIsAnnouncedAtSessionStart(t *testing.T) {
	r := newScopeRig(t, `{"permission":{`+wideOpen+`,"bash":"allow"}}`)
	project := filepath.Join(r.cwd, ".pi", "extensions", "pi-permission-system", "config.json")
	write(t, project, `{}`)
	r.h.Fire("session_start", map[string]any{})
	n := r.notices()
	eq(t, len(n), 1, "one warning")
	eq(t, n[0], refusalHead+"project permission rules ("+project+")"+refusalTail+" Until then every call the port would allow is blocked.", "text")
}

// The SDK mirrors the session log after its first read, so each case starts its own host with the entries it needs.
func agentRig(t *testing.T, frontmatter string, entries ...map[string]any) (*scopeRig, string) {
	r := newScopeRig(t, `{"permission":{`+wideOpen+`,"bash":"allow"}}`)
	agentFile := filepath.Join(r.agent, "agents", "reviewer.md")
	write(t, agentFile, frontmatter)
	r.entries = entries
	r.h.Fire("session_start", map[string]any{})
	return r, agentFile
}

func active(name any) map[string]any {
	return map[string]any{"type": "custom", "customType": "active_agent", "data": map[string]any{"name": name}}
}

func TestAgentScopeIsRefusedNotIgnored(t *testing.T) {
	const withPermission = "---\nname: reviewer\npermission:\n  bash: deny\n---\nbody\n"
	r, _ := agentRig(t, withPermission)
	blocked, _ := r.call("bash", map[string]any{"command": "ls"})
	eq(t, blocked, false, "no active agent: the file does not apply")

	// The active agent comes from the session's active_agent entry.
	r, agentFile := agentRig(t, withPermission, active("reviewer"))
	blocked, reason := r.call("bash", map[string]any{"command": "ls"})
	eq(t, blocked, true, "the agent's rules cannot be read: blocked")
	eq(t, reason, refusalHead+"agent permission rules ('reviewer' in "+agentFile+")"+refusalTail, "exact text")
	n := r.notices()
	eq(t, len(n), 1, "announced at session start")
	eq(t, strings.HasPrefix(n[0], refusalHead+"agent permission rules ('reviewer'"), true, "the announcement")

	// A cleared entry (name null) ends it.
	r, _ = agentRig(t, withPermission, active("reviewer"), active(nil))
	blocked, _ = r.call("bash", map[string]any{"command": "ls"})
	eq(t, blocked, false, "cleared")

	// An agent file without a permission block adds nothing.
	r, _ = agentRig(t, "---\nname: reviewer\n---\nbody\n", active("reviewer"))
	blocked, _ = r.call("bash", map[string]any{"command": "ls"})
	eq(t, blocked, false, "no permission frontmatter")

	// A global deny stays a deny.
	r, _ = agentRig(t, withPermission, active("reviewer"))
	write(t, configPath(), `{"permission":{`+wideOpen+`,"bash":"deny"}}`)
	r.h.Fire("session_start", map[string]any{})
	_, reason = r.call("bash", map[string]any{"command": "ls"})
	eq(t, strings.Contains(reason, "Denied by policy: 'bash'"), true, "the global deny still names itself")
}

func TestAgentScopeFromTheSystemPromptTag(t *testing.T) {
	r := newScopeRig(t, `{"permission":{`+wideOpen+`,"bash":"allow"}}`)
	agentFile := filepath.Join(r.cwd, ".pi", "agents", "scout.md")
	write(t, agentFile, "---\npermission:\n  bash: ask\n---\n")
	r.h.Fire("session_start", map[string]any{})
	blocked, _ := r.call("bash", map[string]any{"command": "ls"})
	eq(t, blocked, false, "no agent yet")
	r.h.Fire("before_agent_start", map[string]any{"systemPrompt": "x <active_agent name=\"scout\"> y", "systemPromptOptions": map[string]any{}})
	blocked, reason := r.call("bash", map[string]any{"command": "ls"})
	eq(t, blocked, true, "the project agent's rules cannot be read")
	eq(t, reason, refusalHead+"agent permission rules ('scout' in "+agentFile+")"+refusalTail, "exact text")
}

func TestSkillInputGate(t *testing.T) {
	r := newScopeRig(t, `{"permission":{"*":"ask","skill":{"open":"allow","secret":"deny"}}}`)
	r.h.Fire("session_start", map[string]any{})
	eq(t, r.input("hello"), "", "not a skill")
	eq(t, r.input("/skill:"), "", "no name")
	eq(t, r.input("/skill:open do it"), "", "allowed")
	n := len(r.notices())
	eq(t, r.input("  /skill:secret\tgo"), "handled", "denied")
	got := r.notices()
	eq(t, got[len(got)-1], "Skill 'secret' is not permitted by the current skill policy.", "deny notice")
	eq(t, r.input("/skill:other"), "handled", "asked: refused")
	got = r.notices()
	eq(t, len(got), n+2, "a notice each")
	eq(t, got[len(got)-1], refusalHead+"the approval dialog (the skill 'other' needs approval)"+refusalTail, "ask refusal text")

	y := newScopeRig(t, `{"yoloMode":true,"permission":{"*":"ask","skill":{"secret":"deny"}}}`)
	y.h.Fire("session_start", map[string]any{})
	eq(t, y.input("/skill:other"), "", "yolo grants an ask")
	eq(t, y.input("/skill:secret"), "handled", "yolo keeps a deny")

	s := newScopeRig(t, `{"permission":{"skill":"allow"}}`)
	s.h.Fire("session_start", map[string]any{})
	write(t, filepath.Join(s.cwd, ".pi", "extensions", "pi-permission-system", "config.json"), `{"permission":{"skill":"deny"}}`)
	eq(t, s.input("/skill:x"), "handled", "a project scope the port cannot read refuses the skill too")
}

func TestUnportedConfigOptionsAreAnnounced(t *testing.T) {
	r := newScopeRig(t, `{"debugLog":true,"permissionReviewLog":false,"authorizerChain":["a"],"promptNotifications":[],"promptMaxRows":20,"permission":{"bash":"allow"}}`)
	r.h.Fire("session_start", map[string]any{})
	n := r.notices()
	eq(t, len(n), 1, "one warning")
	eq(t, n[0], refusalHead+"the config option(s) 'debugLog', 'promptMaxRows', 'authorizerChain' (they are accepted and ignored)"+refusalTail, "text")
}
