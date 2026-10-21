package pi_permission_system

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompositionRootTwins3(t *testing.T) {
	const f = "composition-root"

	runBash := func(t *testing.T, config map[string]any, command string) (blocked bool, prompts []string) {
		agent := compEnv(t)
		compWriteGlobalConfig(t, agent, config)
		n := newCompNode(t, "bash")
		ctx, ui := makeBaseCtx(t.TempDir(), "ui-session", compCtxOpts{select_: approveFirst})
		n.sessionStart(ctx)
		res := n.toolCall(ctx, "bash", map[string]any{"command": command})
		return res.Block, ui.selected()
	}
	permissiveBash := func(yolo bool) map[string]any {
		return map[string]any{"permission": map[string]any{"*": "allow", "bash": map[string]any{"*": "allow"}}, "yoloMode": yolo}
	}
	const flooredWrapper = "git status | xargs rm -rf"
	tw(t, f, "auto-approves an indirection wrapper under yolo", func(t *testing.T) {
		blocked, prompts := runBash(t, permissiveBash(true), flooredWrapper)
		eq(t, blocked, false, "blocked")
		eq(t, len(prompts), 0, "prompts")
	})
	tw(t, f, "still floors an indirection wrapper to a prompt with yolo off", func(t *testing.T) {
		blocked, prompts := runBash(t, permissiveBash(false), flooredWrapper)
		eq(t, blocked, false, "blocked")
		eq(t, len(prompts), 1, "one prompt")
		eq(t, strings.Contains(prompts[0], "<indirection-bash-wrapper>"), true, "names the floor")
	})
	tw(t, f, "never raises the ask for a wrapper running a pure reader", func(t *testing.T) {
		blocked, prompts := runBash(t, permissiveBash(false), "git status | xargs grep foo")
		eq(t, blocked, false, "blocked")
		eq(t, len(prompts), 0, "prompts")
	})
	tw(t, f, "auto-approves an unparseable command under yolo", func(t *testing.T) {
		blocked, prompts := runBash(t, permissiveBash(true), "> out.txt")
		eq(t, blocked, false, "blocked")
		eq(t, len(prompts), 0, "prompts")
	})
	tw(t, f, "blocks a denied wrapper under yolo", func(t *testing.T) {
		blocked, prompts := runBash(t, map[string]any{"permission": map[string]any{"*": "allow", "bash": map[string]any{"*": "allow", "xargs*": "deny"}}, "yoloMode": true}, "git status | xargs grep foo")
		eq(t, blocked, true, "blocked")
		eq(t, len(prompts), 0, "prompts")
	})
	tw(t, f, "blocks a denied unparseable command under yolo", func(t *testing.T) {
		blocked, prompts := runBash(t, map[string]any{"permission": map[string]any{"*": "allow", "bash": map[string]any{"*": "deny"}}, "yoloMode": true}, "> out.txt")
		eq(t, blocked, true, "blocked")
		eq(t, len(prompts), 0, "prompts")
	})

	const externalRoot = "/tmp/pi-perm-directional-target"
	externalPath := externalRoot + "/notes.md"
	runPath := func(t *testing.T, permission map[string]any, tool, path string) (blocked bool, prompts []string) {
		agent := compEnv(t)
		compWriteGlobalConfig(t, agent, map[string]any{"permission": permission})
		n := newCompNode(t, "read", "write", "edit")
		ctx, ui := makeBaseCtx(t.TempDir(), "ui-session", compCtxOpts{select_: approveFirst})
		n.sessionStart(ctx)
		res := n.toolCall(ctx, tool, map[string]any{"path": path})
		return res.Block, ui.selected()
	}
	readRelief := map[string]any{"*": "allow", "external_directory": map[string]any{"*": "ask"},
		"external_directory_read": map[string]any{externalRoot + "/*": "allow"}}
	tw(t, f, "silences a read the directional grant covers", func(t *testing.T) {
		blocked, prompts := runPath(t, readRelief, "read", externalPath)
		eq(t, blocked, false, "blocked")
		eq(t, len(prompts), 0, "prompts")
	})
	tw(t, f, "still prompts for a write to the same path", func(t *testing.T) {
		blocked, prompts := runPath(t, readRelief, "write", externalPath)
		eq(t, blocked, false, "blocked")
		eq(t, len(prompts), 1, "prompts")
	})
	tw(t, f, "still prompts for an edit, which reads and writes", func(t *testing.T) {
		blocked, prompts := runPath(t, readRelief, "edit", externalPath)
		eq(t, blocked, false, "blocked")
		eq(t, len(prompts), 1, "prompts")
	})
	tw(t, f, "prompts for a read the grant does not cover", func(t *testing.T) {
		blocked, prompts := runPath(t, readRelief, "read", "/tmp/pi-perm-directional-elsewhere/notes.md")
		eq(t, blocked, false, "blocked")
		eq(t, len(prompts), 1, "prompts")
	})
	tw(t, f, "keeps a bare external_directory config prompting every direction", func(t *testing.T) {
		bare := map[string]any{"*": "allow", "external_directory": map[string]any{"*": "ask"}}
		for _, tool := range []string{"read", "write", "edit"} {
			_, prompts := runPath(t, bare, tool, externalPath)
			eq(t, len(prompts), 1, tool)
		}
	})

	permissiveWarn := "bash commands silently inherit 'allow'"
	countWarn := func(notified []string) int {
		n := 0
		for _, m := range notified {
			if strings.Contains(m, permissiveWarn) {
				n++
			}
		}
		return n
	}
	tw(t, f, "is shown by session_start alone, before the first turn", func(t *testing.T) {
		agent := compEnv(t)
		compWriteGlobalConfig(t, agent, map[string]any{"permission": map[string]any{"*": "allow"}})
		var notified []string
		n := newCompNode(t, "demo")
		ctx, _ := makeBaseCtx(t.TempDir(), "ui", compCtxOpts{notify: func(m string) { notified = append(notified, m) }})
		n.sessionStart(ctx)
		eq(t, countWarn(notified), 1, "shown once")
	})
	tw(t, f, "is shown once, however many turns follow", func(t *testing.T) {
		agent := compEnv(t)
		compWriteGlobalConfig(t, agent, map[string]any{"permission": map[string]any{"*": "allow"}})
		var notified []string
		n := newCompNode(t, "demo")
		ctx, _ := makeBaseCtx(t.TempDir(), "ui", compCtxOpts{notify: func(m string) { notified = append(notified, m) }})
		n.sessionStart(ctx)
		n.beforeAgentStart(ctx, emptyPromptPayload())
		n.beforeAgentStart(ctx, emptyPromptPayload())
		eq(t, countWarn(notified), 1, "shown once")
	})
	tw(t, f, "is shown on the next turn when it appears mid-session", func(t *testing.T) {
		agent := compEnv(t)
		compWriteGlobalConfig(t, agent, map[string]any{"permission": map[string]any{"*": "ask"}})
		var notified []string
		n := newCompNode(t, "demo")
		ctx, _ := makeBaseCtx(t.TempDir(), "ui", compCtxOpts{notify: func(m string) { notified = append(notified, m) }})
		n.sessionStart(ctx)
		eq(t, countWarn(notified), 0, "nothing yet")
		compWriteGlobalConfig(t, agent, map[string]any{"permission": map[string]any{"*": "allow"}})
		n.beforeAgentStart(ctx, emptyPromptPayload())
		eq(t, countWarn(notified), 1, "shown on the next turn")
		n.beforeAgentStart(ctx, emptyPromptPayload())
		eq(t, countWarn(notified), 1, "not repeated")
	})

	wipe, other := "mcp__danger_srv__wipe", "mcp__danger_srv__list"
	writeMcp := func(agent string) {
		_ = os.WriteFile(filepath.Join(agent, "mcp.json"), []byte(`{"mcpServers":{"danger-srv":{"command":"danger"}}}`), 0o644)
	}
	tw(t, f, "denies a call when an mcp rule denies its server by its mcp.json name", func(t *testing.T) {
		agent := compEnv(t)
		compWriteGlobalConfig(t, agent, map[string]any{"permission": map[string]any{"*": "allow", "mcp": map[string]any{"*": "allow", "danger-srv": "deny"}}})
		writeMcp(agent)
		n := newCompNode(t, wipe)
		ctx := makeChildCtx(t.TempDir(), "mcp-tool-deny")
		n.sessionStart(ctx)
		eq(t, n.toolCall(ctx, wipe, map[string]any{"target": "prod"}).Block, true, "blocked")
	})
	tw(t, f, "approves exactly the asked tool for the session", func(t *testing.T) {
		agent := compEnv(t)
		compWriteGlobalConfig(t, agent, map[string]any{"permission": map[string]any{"*": "allow", "mcp": map[string]any{"*": "ask"}}})
		writeMcp(agent)
		n := newCompNode(t, wipe, other)
		ctx, ui := makeBaseCtx(t.TempDir(), "mcp-tool-session", compCtxOpts{select_: func(_ string, o []string) (string, bool) { return o[1], true }})
		n.sessionStart(ctx)
		eq(t, n.toolCall(ctx, wipe, map[string]any{"target": "prod"}).Block, false, "first")
		eq(t, n.toolCall(ctx, wipe, map[string]any{"target": "prod"}).Block, false, "second")
		eq(t, len(ui.selected()), 1, "asked once")
		n.toolCall(ctx, other, map[string]any{"target": "prod"})
		eq(t, len(ui.selected()), 2, "another tool asks again")
	})

	readOutcome := func(t *testing.T, permission func(agent string) map[string]any, target func(agent string) string) ToolCallResult {
		agent := compEnv(t)
		compWriteGlobalConfig(t, agent, map[string]any{"permission": permission(agent)})
		n := newCompNode(t, "read")
		ctx := makeChildCtx(t.TempDir(), "infra-session")
		n.sessionStart(ctx)
		return n.toolCall(ctx, "read", map[string]any{"path": target(agent)})
	}
	static := func(m map[string]any) func(string) map[string]any { return func(string) map[string]any { return m } }
	tw(t, f, "blocks a read the bypass would otherwise allow", func(t *testing.T) {
		skillOf := func(agent string) string { return filepath.Join(agent, "git", "x", "SKILL.md") }
		res := readOutcome(t, func(agent string) map[string]any {
			return map[string]any{"*": "allow", "external_directory_read": map[string]any{skillOf(agent): "deny"}}
		}, skillOf)
		eq(t, res.Block, true, "blocked")
		eq(t, strings.Contains(res.Reason, "external_directory_read"), true, "names the surface")
	})
	tw(t, f, "keeps the bypass under a catch-all deny", func(t *testing.T) {
		res := readOutcome(t, static(map[string]any{"*": "allow", "external_directory": map[string]any{"*": "deny"}}), func(agent string) string { return filepath.Join(agent, "git", "x", "SKILL.md") })
		eq(t, res.Block, false, "allowed")
	})
	tw(t, f, "gates a read of the review log", func(t *testing.T) {
		res := readOutcome(t, static(map[string]any{"*": "allow", "external_directory_read": map[string]any{"*": "deny"}}), func(agent string) string {
			return filepath.Join(agent, "extensions", "pi-permission-system", "logs", reviewLogFilename)
		})
		eq(t, res.Block, true, "blocked")
	})
	tw(t, f, "still auto-allows the package's config beside them", func(t *testing.T) {
		res := readOutcome(t, static(map[string]any{"*": "allow", "external_directory_read": map[string]any{"*": "deny"}}), func(agent string) string {
			return filepath.Join(agent, "extensions", "pi-permission-system", "config.json")
		})
		eq(t, res.Block, false, "allowed")
	})

	tskip(t, f, "records a whole-session grant on the serving node so later forwards and the parent's own action resolve without a second prompt", "drives the serving node's forwarded-inbox poll end to end (a real timer and the forwarding files); the whole-session grant is twinned by the serving-server twins (fwd_server_test.go)")
	tskip(t, f, "contains a subagent-only grant to the requesting child so the parent's own action still prompts", "drives the serving node's forwarded-inbox poll end to end (a real timer and the forwarding files); the subagent-scope grant is twinned by the serving-server twins (fwd_server_test.go)")
	tskip(t, f, "rings the configured channels when a prompt opens, and only then", "mounts the inline dialog through ctx.ui.custom in tui mode: twinned against the dialog component by auth_prompt_component_test.go and RenderPromptNotification's twins")
	tskip(t, f, "names the session and the requested tool", "mounts the inline dialog through ctx.ui.custom in tui mode: the notice text is twinned by auth_local_user_test.go")
	tskip(t, f, "names the working directory for an unnamed session", "mounts the inline dialog through ctx.ui.custom in tui mode: the notice text is twinned by auth_local_user_test.go")
	tskip(t, f, "renders and honors the characters the config bound", "mounts the inline dialog through ctx.ui.custom in tui mode: the bound keys are twinned by auth_prompt_component_test.go and the dialog-keys twins")
	tskip(t, f, "warns about a refused binding and leaves the policy alone", "needs the inline dialog mounted through ctx.ui.custom in tui mode: the refused-binding warning is twinned by the dialog-keys twins (cfg_dialog_schema_test.go)")
}

func TestSessionStartTwins(t *testing.T) {
	const f = "session-start"
	tskip(t, f, "registers exactly one session_start handler", "needs the host's registered-handler list: asserted through the fake host by TestAdapterRegistersEvents (adapter_test.go, external package)")
	tw(t, f, "session_start handler preserves lifecycle.reload debug log", func(t *testing.T) {
		agent := compEnv(t)
		compWriteGlobalConfig(t, agent, map[string]any{"debugLog": true})
		n := newCompNode(t)
		cwd := t.TempDir()
		ctx, _ := makeBaseCtx(cwd, "reload-session", compCtxOpts{})
		if err := n.p.lifecycle.HandleSessionStart(SessionStartPayload{Reason: "reload"}, ctx); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(n.shutdown)
		raw, err := os.ReadFile(filepath.Join(agent, "extensions", "pi-permission-system", "logs", debugLogFilename))
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.Contains(line, `"event":"lifecycle.reload"`) && strings.Contains(line, `"triggeredBy":"session_start"`) && strings.Contains(line, `"reason":"reload"`) {
				found = true
			}
		}
		eq(t, found, true, "the debug log carries lifecycle.reload")
	})
}
