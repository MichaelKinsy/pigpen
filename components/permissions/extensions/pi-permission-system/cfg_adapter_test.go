package pi_permission_system_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	pps "github.com/MichaelKinsy/pigpen/pi-permission-system"
)

// trustRig is the extension in a fake interactive host whose answer to isProjectTrusted is set by the test.
type trustRig struct {
	h       *Host
	agent   string
	cwd     string
	trusted *bool // nil: the host fails the call
}

func newTrustRig(t *testing.T, trusted *bool) *trustRig {
	t.Helper()
	r := &trustRig{agent: t.TempDir(), cwd: t.TempDir(), trusted: trusted}
	t.Setenv("PI_CODING_AGENT_DIR", r.agent)
	agentDirForTest = r.agent
	r.h = StartHost(t, pps.Extension(), HostOptions{Cwd: r.cwd, OnCall: func(method string, _ map[string]any) (map[string]any, string) {
		switch method {
		case "isProjectTrusted":
			if r.trusted == nil {
				return nil, "no trust answer"
			}
			return map[string]any{"trusted": *r.trusted}, ""
		case "getActiveTools", "getAllTools":
			return map[string]any{"tools": []any{}}, ""
		}
		return map[string]any{}, ""
	}})
	return r
}

func (r *trustRig) global(config string) {
	p := filepath.Join(r.agent, "extensions", "pi-permission-system", "config.json")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte(config), 0o644)
}

func (r *trustRig) project(config string) {
	p := filepath.Join(r.cwd, ".pi", "extensions", "pi-permission-system", "config.json")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte(config), 0o644)
}

func (r *trustRig) notes() []string {
	var out []string
	for _, c := range r.h.CallsTo("ui.notify") {
		out = append(out, c.Args["message"].(string))
	}
	return out
}

func (r *trustRig) statuses() []any {
	var out []any
	for _, c := range r.h.CallsTo("ui.setStatus") {
		out = append(out, c.Args["text"])
	}
	return out
}

func bp(b bool) *bool { return &b }

const untrustedMessage = "pi-permission-system: project is not trusted \u2014 skipping project-scoped permission configuration. Only global policy applies. Grant project trust to load this project's permission rules."

func TestProjectConfigFollowsTrust(t *testing.T) {
	r := newTrustRig(t, bp(true))
	r.global(`{"yoloMode":false}`)
	r.project(`{"yoloMode":true}`)
	r.h.Fire("session_start", map[string]any{"reason": "startup"})
	eq(t, r.statuses(), []any{"yolo"}, "a trusted project's runtime config is merged")
	eq(t, len(r.notes()), 0, "no warning")

	u := newTrustRig(t, bp(false))
	u.global(`{"yoloMode":false}`)
	u.project(`{"yoloMode":true}`)
	u.h.Fire("session_start", map[string]any{"reason": "startup"})
	eq(t, u.statuses(), []any{""}, "an untrusted project cannot turn yolo on (#644)")
	eq(t, u.notes(), []string{untrustedMessage}, "the reduced scope is announced")
	// every turn re-reads the config under the current trust, without repeating the trust warning
	u.h.Fire("before_agent_start", map[string]any{"systemPrompt": "x", "systemPromptOptions": map[string]any{}})
	eq(t, u.statuses(), []any{"", ""}, "still no yolo")
	eq(t, len(u.notes()), 1, "the trust warning is not repeated")
}

func TestFailedTrustAnswerIsNotTrust(t *testing.T) {
	r := newTrustRig(t, nil)
	r.project(`{"yoloMode":true}`)
	r.h.Fire("session_start", map[string]any{})
	eq(t, r.statuses(), []any{""}, "no yolo")
	eq(t, r.notes(), []string{untrustedMessage}, "announced")
}

func TestTrustGrantedOnReloadIncludesTheProject(t *testing.T) {
	r := newTrustRig(t, bp(false))
	r.project(`{"yoloMode":true}`)
	r.h.Fire("session_start", map[string]any{})
	r.h.Fire("resources_discover", map[string]any{"reason": "reload"})
	eq(t, r.notes(), []string{untrustedMessage, untrustedMessage}, "announced on session start and on reload")
	r.h.Fire("resources_discover", map[string]any{"reason": "startup"})
	eq(t, len(r.notes()), 2, "only a reload is handled")
	granted := true
	r.trusted = &granted
	r.h.Fire("before_agent_start", map[string]any{"systemPrompt": "x", "systemPromptOptions": map[string]any{}})
	eq(t, r.statuses()[len(r.statuses())-1], "yolo", "the next turn merges the project")
}

func TestConfigIssuesAreSaidOncePerIssue(t *testing.T) {
	r := newTrustRig(t, bp(true))
	r.global(`{"nope":1}`)
	r.h.Fire("session_start", map[string]any{})
	eq(t, r.notes(), []string{"Unrecognized config key 'nope'."}, "session start says it")
	prompt := map[string]any{"systemPrompt": "x", "systemPromptOptions": map[string]any{}}
	r.h.Fire("before_agent_start", prompt)
	eq(t, len(r.notes()), 1, "an unchanged issue is not repeated")
	r.global(`{"nope":1,"also":2}`)
	r.h.Fire("before_agent_start", prompt)
	eq(t, r.notes()[1:], []string{"Unrecognized config key 'also'."}, "only the new issue (the whole scope is rejected, both keys are reported; the first was heard)")
}

func TestPermissionSystemCommandInTheHost(t *testing.T) {
	r := newTrustRig(t, bp(true))
	r.global(`{"yoloMode":true}`)
	r.h.Fire("session_start", map[string]any{})
	eq(t, r.h.Command("permission-system", "path"), "", "path")
	n := r.notes()
	eq(t, n[len(n)-1], "permission-system config: "+filepath.Join(r.agent, "extensions", "pi-permission-system", "config.json"), "path text")
	eq(t, r.h.Command("permission-system", "show"), "", "show")
	n = r.notes()
	eq(t, strings.HasPrefix(n[len(n)-1], "permission-system: yoloMode=on, permissionReviewLog=on, debugLog=off"), true, "show text")
	eq(t, r.h.Command("permission-system", "reset"), "", "reset")
	b, _ := os.ReadFile(filepath.Join(r.agent, "extensions", "pi-permission-system", "config.json"))
	eq(t, string(b), "{\n  \"yoloMode\": false,\n  \"debugLog\": false,\n  \"permissionReviewLog\": true\n}\n",
		"persisted: the existing key keeps its place, and save() writes only debugLog, permissionReviewLog and yoloMode, as the original does")
	st := r.statuses()
	eq(t, st[len(st)-1], "", "the status follows the saved config")
}
