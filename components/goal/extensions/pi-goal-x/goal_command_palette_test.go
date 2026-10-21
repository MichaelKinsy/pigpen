package pi_goal_x_test

// Twins of tests/goal-command-palette.test.ts: guided drafting is the normal entry path; the two -direct commands are the explicit
// immediate-creation escape hatch; the settings menu rows.

import (
	"path/filepath"
	"strings"
	"testing"

	goal "github.com/MichaelKinsy/pigpen/pi-goal-x"
)

var curatedCommands = []string{
	"goal", "sisyphus", "goal-direct", "sisyphus-direct", "goal-tweak", "goal-pause", "goal-resume",
	"goal-clear", "goal-list", "goal-status", "goal-refresh", "goal-recovery", "goal-focus", "goal-unfocus", "goal-settings", "goal-cancel",
}

func paletteRig(t *testing.T, opts rigOptions) *goalRig {
	t.Helper()
	if opts.Cwd == "" {
		opts.Cwd = archivedCwd(t)
	}
	opts.HasUI = boolPtr(opts.HasUI != nil && *opts.HasUI)
	if opts.SessionID == "" {
		opts.SessionID = "palette-session"
	}
	if opts.ActiveTools == nil {
		opts.ActiveTools = []string{"read", "bash", "edit", "write"}
	}
	return newGoalRig(t, opts)
}

func TestGoalCommandPalette(t *testing.T) {
	const f = "goal-command-palette"

	tw(t, f, "exactly the fourteen curated commands are registered; legacy commands are absent", func(t *testing.T) {
		r := paletteRig(t, rigOptions{})
		r.Host.mu.Lock()
		have := map[string]bool{}
		for name := range r.Host.cmds {
			have[name] = true
		}
		r.Host.mu.Unlock()
		for _, name := range curatedCommands {
			if !have[name] {
				t.Errorf("%s must be registered", name)
			}
		}
		for _, name := range []string{"goals", "goals-set", "sisyphus-set", "goal-abort"} {
			if have[name] {
				t.Errorf("%s must NOT be registered", name)
			}
		}
		if len(have) != len(curatedCommands) {
			t.Errorf("no aliases or extras: %d registered, want %d", len(have), len(curatedCommands))
		}
	})

	tw(t, f, "/goal <objective> starts guided drafting without creating a goal", func(t *testing.T) {
		r := paletteRig(t, rigOptions{})
		r.Fire("session_start", map[string]any{"reason": "start"})
		r.Command("goal", "Create hello.txt with 'Hello'")
		if n := len(goal.ActiveGoalFiles(r.Cwd)); n != 0 {
			t.Errorf("drafting does not create before confirmation: %d files", n)
		}
		if !anyHas(r.SentUser(), "GOAL CONFIRMATION") {
			t.Errorf("drafting prompt sent to agent: %q", r.SentUser())
		}
		var drafting []string
		for _, n := range r.ActiveTools() {
			if strings.HasPrefix(n, "goal_") || n == "propose_goal_draft" {
				drafting = append(drafting, n)
			}
		}
		if strings.Join(drafting, ",") != "goal_question,goal_questionnaire,propose_goal_draft" {
			t.Errorf("drafting tools %v", drafting)
		}
	})

	tw(t, f, "bare /goal begins a guided draft and asks for an objective", func(t *testing.T) {
		r := paletteRig(t, rigOptions{})
		r.Fire("session_start", map[string]any{"reason": "start"})
		r.Command("goal", "")
		if n := len(goal.ActiveGoalFiles(r.Cwd)); n != 0 {
			t.Errorf("no goal created: %d", n)
		}
		if !anyHas(r.SentUser(), "ask the user what they want") {
			t.Errorf("draft prompt requests an objective: %q", r.SentUser())
		}
	})

	tw(t, f, "confirmed draft creates the proposed goal and agent-selected task plan together", func(t *testing.T) {
		r := paletteRig(t, rigOptions{})
		r.Fire("session_start", map[string]any{"reason": "start"})
		r.Command("goal", "Ship a small feature")
		if !r.Host.tools["propose_goal_draft"] {
			t.Fatal("draft proposal tool registered")
		}
		if _, failure := r.Tool("propose_goal_draft", map[string]any{
			"objective":        "Ship a small feature.\nSuccess criteria: tests pass.",
			"sisyphus":         false,
			"tasks":            []any{map[string]any{"id": "implement", "title": "Implement the feature"}, map[string]any{"id": "verify", "title": "Run tests"}},
			"block_completion": true,
		}); failure != "" {
			t.Fatal(failure)
		}
		files := goal.ActiveGoalFiles(r.Cwd)
		if len(files) != 1 {
			t.Fatalf("confirmed draft creates one goal: %v", files)
		}
		g := goal.ParseGoalFileFor(filepath.Join(r.Cwd, ".pi", "goals", files[0]))
		tl, _ := g["taskList"].(map[string]any)
		var ids []string
		for _, x := range tl["tasks"].([]any) {
			ids = append(ids, x.(map[string]any)["id"].(string))
		}
		if strings.Join(ids, ",") != "implement,verify" || tl["blockCompletion"] != true {
			t.Errorf("task plan %v", tl)
		}
		if !contains(r.ActiveTools(), "update_goal") {
			t.Errorf("execution profile restored after confirmation: %v", r.ActiveTools())
		}
	})

	tw(t, f, "/goal-direct and /sisyphus-direct create goals immediately", func(t *testing.T) {
		r := paletteRig(t, rigOptions{})
		r.Fire("session_start", map[string]any{"reason": "start"})
		r.Command("goal-direct", "Create hello.txt")
		r.Command("sisyphus-direct", "1) create a.txt 2) create b.txt")
		goals := goal.DiskGoals(r.Cwd)
		if len(goals) != 2 {
			t.Fatalf("direct commands create immediately: %d", len(goals))
		}
		var sis, reg bool
		for _, g := range goals {
			sis = sis || g["sisyphus"] == true
			reg = reg || g["sisyphus"] == false
		}
		if !sis || !reg {
			t.Errorf("sisyphus direct mode %v, regular direct mode %v", sis, reg)
		}
	})

	tw(t, f, "goal-settings renders sectioned rows with clearer auditor wording", func(t *testing.T) {
		var captured []string
		r := paletteRig(t, rigOptions{HasUI: boolPtr(true), Settings: "-", Select: func(_ string, options []string) (string, bool) {
			if captured == nil {
				captured = options
			}
			return "Done", true
		}})
		r.Command("goal-settings", "")
		if captured == nil {
			t.Fatal("settings menu must call ui.select")
		}
		if !hasPrefixItem(captured, "─── Editing: project (") {
			t.Error("scope header names the edited layer")
		}
		for _, h := range []string{"─── Goal behavior ───", "─── Task tracking ───", "─── Completion auditor ───", "─── Blocker Oracle ───", "Done"} {
			if !contains(captured, h) {
				t.Errorf("missing %q", h)
			}
		}
		if !anyHas(captured, "auditor disabled:") {
			t.Error("clearer auditor wording row")
		}
		if !anyHas(captured, "provider:") || !anyHas(captured, "model:") {
			t.Error("provider/model rows")
		}
		headers := 0
		for _, l := range captured {
			if strings.HasPrefix(l, "───") {
				headers++
			}
		}
		if headers != 5 {
			t.Errorf("editing header + exactly four sections (incl. Blocker Oracle): %d", headers)
		}
	})

	tw(t, f, "goal-settings: stall timeout row accepts 0 (row-driven lower bound)", func(t *testing.T) {
		calls := 0
		r := paletteRig(t, rigOptions{HasUI: boolPtr(true), Settings: "-",
			Select: func(_ string, options []string) (string, bool) {
				calls++
				if calls == 1 {
					for _, o := range options {
						if strings.Contains(o, "stall timeout (minutes)") {
							return o, true
						}
					}
					t.Error("stall timeout row must be listed")
				}
				return "Done", true
			},
			Input: func(string, string) (string, bool) { return "0", true }})
		r.Command("goal-settings", "")
		if v := savedSettings(t, r.Cwd)["stallTimeoutMinutes"]; v != float64(0) {
			t.Errorf("stallTimeoutMinutes must accept 0: %v", v)
		}
	})

	tw(t, f, "goal-settings: autonomous allowance accepts an explicit zero override", func(t *testing.T) {
		calls := 0
		r := paletteRig(t, rigOptions{HasUI: boolPtr(true), Settings: "-",
			Select: func(_ string, options []string) (string, bool) {
				calls++
				switch calls {
				case 1:
					for _, o := range options {
						if strings.Contains(o, "autonomous run allowance") {
							return o, true
						}
					}
					t.Error("allowance row must be listed")
					return "", false
				case 2:
					return "Set project override...", true
				}
				if !anyHas(options, "autonomous run allowance: 0 (disabled) (project override)") {
					t.Errorf("row after the override: %v", options)
				}
				return "Done", true
			},
			Input: func(string, string) (string, bool) { return "0", true }})
		r.Command("goal-settings", "")
		if v := savedSettings(t, r.Cwd)["maxAutonomousRuns"]; v != float64(0) {
			t.Errorf("maxAutonomousRuns %v", v)
		}
	})

	tw(t, f, "goal-settings: subtaskDepth rejects 0 (min 1) and never saves it", func(t *testing.T) {
		calls := 0
		r := paletteRig(t, rigOptions{HasUI: boolPtr(true), Settings: "-",
			Select: func(_ string, options []string) (string, bool) {
				calls++
				if calls == 1 {
					for _, o := range options {
						if strings.Contains(o, "subtaskDepth") {
							return o, true
						}
					}
					t.Error("subtaskDepth row must be listed")
				}
				return "Done", true
			},
			Input: func(string, string) (string, bool) { return "0", true }})
		r.Command("goal-settings", "")
		if !anyHas(r.Notices(), "subtaskDepth must be an integer >= 1") {
			t.Errorf("expected subtaskDepth bound warning, got notifications: %v", r.Notices())
		}
		if p := goal.SettingsPath(r.Cwd); fileExists(p) && strings.Contains(readFile(t, p), "subtaskDepth") {
			t.Error("subtaskDepth must not be written")
		}
	})

	tw(t, f, "goal-settings: max objective length row defaults to 0 and accepts a configured limit", func(t *testing.T) {
		calls := 0
		inputValue := "0"
		r := paletteRig(t, rigOptions{HasUI: boolPtr(true), Settings: "-",
			Select: func(_ string, options []string) (string, bool) {
				calls++
				if calls == 1 {
					for _, o := range options {
						if strings.Contains(o, "max objective length") {
							if !strings.Contains(o, ": 0") {
								t.Errorf("row defaults to 0 = no limit: %q", o)
							}
							return o, true
						}
					}
					t.Error("max objective length row must be listed")
				}
				return "Done", true
			},
			Input: func(_ string, placeholder string) (string, bool) {
				if placeholder != "0" {
					t.Errorf("input defaults to 0: %q", placeholder)
				}
				return inputValue, true
			}})
		r.Command("goal-settings", "")
		if v := savedSettings(t, r.Cwd)["objectiveMaxChars"]; v != float64(0) {
			t.Errorf("0 = no limit persists explicitly: %v", v)
		}
		// A configured limit persists too.
		inputValue = "6000"
		calls = 0
		r.Command("goal-settings", "")
		if v := savedSettings(t, r.Cwd)["objectiveMaxChars"]; v != float64(6000) {
			t.Errorf("configured limit persists: %v", v)
		}
	})

	tw(t, f, "goal-settings: execution-contract opt-in can be set and removed through the menu", func(t *testing.T) {
		var action string
		step := 0
		r := paletteRig(t, rigOptions{HasUI: boolPtr(true), Settings: "-", Select: func(_ string, options []string) (string, bool) {
			step++
			switch step {
			case 1:
				for _, o := range options {
					if strings.Contains(o, "explicit execution contracts (opt-in):") {
						return o, true
					}
				}
			case 2:
				if !contains(options, action) {
					t.Errorf("%q not offered: %v", action, options)
				}
				return action, true
			}
			return "Done", true
		}})
		for _, a := range []string{"Set project override to true", "Set project override to false", "Use inherited value"} {
			action, step = a, 0
			r.Command("goal-settings", "")
			saved := savedSettings(t, r.Cwd)
			want := any(strings.HasSuffix(a, "true"))
			if a == "Use inherited value" {
				want = nil
			}
			if saved["strictExecutionContract"] != want {
				t.Errorf("%s: saved %v, want %v", a, saved["strictExecutionContract"], want)
			}
		}
	})
}
