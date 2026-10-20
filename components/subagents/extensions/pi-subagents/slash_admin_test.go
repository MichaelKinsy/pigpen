package pi_subagents

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// Twins of the /subagents admin panel cases of test/unit/agent-management.test.ts.

type adminRig struct {
	host    adminHost
	notices [][2]string
	sent    []string
	choices []string
}

func newAdminRig(e mgmtEnv, models []ModelInfo, answer func(title string, items []string) (string, bool)) *adminRig {
	r := &adminRig{}
	r.host = adminHost{cwd: e.dir, hasUI: true,
		available: func() []ModelInfo { return models },
		notify:    func(m, l string) { r.notices = append(r.notices, [2]string{m, l}) },
		send:      func(c string) { r.sent = append(r.sent, c) },
		sel: func(title string, items []string) (string, bool) {
			r.choices = items
			return answer(title, items)
		},
		edit: func(string, string) (string, bool) { return "", false },
	}
	return r
}

func TestAgentManagementAdminPanel(t *testing.T) {
	const f = "agent-management"
	mi := func(provider, id string) ModelInfo {
		return ModelInfo{Provider: provider, ID: id, FullID: provider + "/" + id}
	}
	readJSON := func(t *testing.T, p string) *jsObject {
		o, err := readJSONObjectFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	tw(t, f, "awaits an offline registry refresh before opening the ${action} picker", func(t *testing.T) {
		for _, action := range []string{"model", "thinking"} {
			t.Run(action, func(t *testing.T) {
				e := newMgmtEnv(t)
				e.write(t, "refresh-worker.md", "---\nname: refresh-worker\ndescription: Refresh test\nmodel: custom/fresh\n---\nPrompt.\n")
				refreshed := false
				rig := newAdminRig(e, nil, func(string, []string) (string, bool) { return "", false })
				rig.host.available = func() []ModelInfo {
					if !refreshed {
						return nil
					}
					fresh := mi("custom", "fresh")
					tr := true
					fresh.Reasoning = &tr
					null := (*string)(nil)
					high, max := "high", "max"
					fresh.ThinkingLevelMap = map[string]*string{"minimal": null, "low": null, "medium": null, "high": &high, "max": &max}
					return []ModelInfo{fresh, mi("custom", "added")}
				}
				rig.host.refresh = func() (bool, string, string, error) { refreshed = true; return false, "", "", nil }
				if err := rig.host.openSubagentsAdmin("refresh-worker " + action); err != nil {
					t.Fatal(err)
				}
				if action == "model" {
					eq(t, rig.choices, []string{"Default / inherit session model", "custom/fresh", "custom/added"}, "choices")
				} else {
					eq(t, rig.choices, []string{"Default / inherit session thinking", "off", "high", "max"}, "choices")
				}
				eq(t, len(rig.notices), 0, "warnings")
				eq(t, len(rig.sent), 0, "cancel must not save")
			})
		}
	})
	tskip(t, f, "shows provider-scoped runtime agent metadata but refuses edits without writing configuration", "registers a runtime agent with registerAgent (src/api/agents.ts), which stores it in a registry on JavaScript globalThis keyed by pi-subagents.runtime-agents.v1 so other extensions of the same Pi process can reach it; a Go extension process has no shared global, and the port has no runtime agent registry")
	tskip(t, f, "fails closed when a runtime agent collides with a disabled configured definition", "needs the same runtime agent registry (registerAgent through a JavaScript globalThis key) that the port does not have")
	tw(t, f, "warns and keeps registry choices when refresh ${outcome}", func(t *testing.T) {
		for _, outcome := range []string{"returned error", "aborted", "rejected"} {
			t.Run(outcome, func(t *testing.T) {
				e := newMgmtEnv(t)
				e.write(t, "refresh-worker.md", "---\nname: refresh-worker\ndescription: Refresh test\n---\nPrompt.\n")
				rig := newAdminRig(e, []ModelInfo{mi("custom", "cached")}, func(string, []string) (string, bool) { return "", false })
				rig.host.refresh = func() (bool, string, string, error) {
					switch outcome {
					case "rejected":
						return false, "", "", errors.New("refresh failed")
					case "aborted":
						return true, "", "", nil
					}
					return false, "custom", "catalog failed", nil
				}
				if err := rig.host.openSubagentsAdmin("refresh-worker model"); err != nil {
					t.Fatal(err)
				}
				eq(t, rig.choices, []string{"Default / inherit session model", "custom/cached"}, "choices")
				eq(t, len(rig.notices), 1, "notices")
				eq(t, rig.notices[0][1], "warning", "level")
				re := map[string]string{"returned error": `custom.*catalog failed`, "aborted": `timed out`, "rejected": `refresh failed`}[outcome]
				polMatches(t, rig.notices[0][0], re)
			})
		}
	})
	tw(t, f, "keeps same-value custom override ownership for interactive admin edits", func(t *testing.T) {
		e := newMgmtEnv(t)
		settings := filepath.Join(e.dir, ".pi", "settings.json")
		agentPath := e.write(t, "implementer.md", "---\nname: implementer\ndescription: Frontmatter description\nmodel: anthropic/claude-old\n---\n\nDrive the failing test first.\n")
		wj(t, settings, js(t, `{"subagents":{"agentOverrides":{"implementer":{"model":"anthropic/claude-old"}}}}`))
		rig := newAdminRig(e, []ModelInfo{mi("anthropic", "claude-old"), mi("anthropic", "claude-new")}, func(string, []string) (string, bool) { return "anthropic/claude-new", true })
		if err := rig.host.openSubagentsAdmin("implementer model"); err != nil {
			t.Fatal(err)
		}
		polMatches(t, rig.notices[0][0], `Saved project settings override`)
		polMatches(t, strings.Join(rig.sent, "\n"), `Saved project settings override`)
		polMatches(t, e.read(t, agentPath), `(?m)^model: anthropic/claude-old$`)
		eq(t, readJSON(t, settings).obj("subagents").obj("agentOverrides").obj("implementer").vals["model"], any("anthropic/claude-new"), "override")
		polMatches(t, getText(e, "implementer"), `Model: anthropic/claude-new`)
	})
	tw(t, f, "promotes cross-scope custom override edits to project settings", func(t *testing.T) {
		e := newMgmtEnv(t)
		userSettings := filepath.Join(e.dir, "agent-home", "settings.json")
		projectSettings := filepath.Join(e.dir, ".pi", "settings.json")
		wj(t, userSettings, js(t, `{"subagents":{"agentOverrides":{"implementer":{"model":"anthropic/claude-old"},"critic":{"thinking":"high"},"speaker":{"systemPrompt":"Shared prompt"}}}}`))
		wj(t, projectSettings, js(t, `{"subagents":{"defaultModel":"openai/gpt-default"}}`))
		agentPath := e.write(t, "implementer.md", "---\nname: implementer\ndescription: Frontmatter description\n---\n\nDrive the failing test first.\n")
		e.write(t, "critic.md", "---\nname: critic\ndescription: Thinking critic\nthinking: false\n---\n\nKeep thinking off.\n")
		e.write(t, "speaker.md", "---\nname: speaker\ndescription: Prompt speaker\n---\n\nBase prompt.\n")
		override := func(path, agent, field string) any {
			return readJSON(t, path).obj("subagents").obj("agentOverrides").obj(agent).vals[field]
		}
		models := []ModelInfo{mi("anthropic", "claude-old"), mi("anthropic", "claude-new")}

		rig := newAdminRig(e, models, func(string, []string) (string, bool) { return "anthropic/claude-new", true })
		if err := rig.host.openSubagentsAdmin("implementer model"); err != nil {
			t.Fatal(err)
		}
		polMatches(t, rig.notices[0][0], `Saved project settings override`)
		polMatches(t, strings.Join(rig.sent, "\n"), `Saved project settings override`)
		polNotMatches(t, e.read(t, agentPath), `(?m)^model:`)
		eq(t, override(userSettings, "implementer", "model"), any("anthropic/claude-old"), "user override")
		eq(t, override(projectSettings, "implementer", "model"), any("anthropic/claude-new"), "project override")
		polMatches(t, getText(e, "implementer"), `Model: anthropic/claude-new`)

		rig = newAdminRig(e, []ModelInfo{mi("anthropic", "claude-new")}, func(string, []string) (string, bool) { return "Default / inherit session model", true })
		if err := rig.host.openSubagentsAdmin("implementer model"); err != nil {
			t.Fatal(err)
		}
		eq(t, override(userSettings, "implementer", "model"), any("anthropic/claude-old"), "user override kept")
		eq(t, override(projectSettings, "implementer", "model"), any("openai/gpt-default"), "project override shadows with the default")
		cleared := getText(e, "implementer")
		polMatches(t, cleared, `Model: openai/gpt-default`)
		polNotMatches(t, cleared, `Model: anthropic/claude-old|Model: anthropic/claude-new`)

		rig = newAdminRig(e, nil, func(string, []string) (string, bool) { return "Default / inherit session thinking", true })
		if err := rig.host.openSubagentsAdmin("critic thinking"); err != nil {
			t.Fatal(err)
		}
		eq(t, override(userSettings, "critic", "thinking"), any("high"), "user thinking")
		eq(t, override(projectSettings, "critic", "thinking"), any("off"), "project thinking")
		polMatches(t, getText(e, "critic"), `Thinking: off`)

		rig = newAdminRig(e, nil, func(string, []string) (string, bool) { return "", false })
		rig.host.edit = func(string, string) (string, bool) { return "Shared prompt", true }
		if err := rig.host.openSubagentsAdmin("speaker system-prompt"); err != nil {
			t.Fatal(err)
		}
		eq(t, override(userSettings, "speaker", "systemPrompt"), any("Shared prompt"), "user prompt")
		eq(t, override(projectSettings, "speaker", "systemPrompt"), any("Shared prompt"), "project prompt")
	})
}

func getText(e mgmtEnv, agent string) string {
	return getAgent(e.dir, map[string]any{"agent": agent}).text
}
