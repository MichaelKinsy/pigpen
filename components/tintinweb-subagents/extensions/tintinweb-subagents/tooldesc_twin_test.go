package tintinweb_subagents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Twins of test/tool-description-mode.test.ts. The original instantiates the extension with every optional feature
// on (its default) and reads the registered Agent tool; so does this, with the port's feature switches (features.go)
// turned on for the test: the text is what the settings and the mode decide.

type descEnv struct {
	t     *testing.T
	cwd   string
	agent string
	warns []string
}

// setupDesc builds the tools the way the extension registers them in a workspace with the given subagents.json, after
// before has written whatever else the case needs. The Agent tool is returned.
func setupDesc(t *testing.T, settings map[string]any, before ...func(e *descEnv)) (sdk.ToolDefinition, *descEnv) {
	t.Helper()
	oldS, oldI, oldW := featureScheduling, featureWorktreeIsolation, featureWorkflows
	featureScheduling, featureWorktreeIsolation, featureWorkflows = true, true, true
	// Temp directories with a neutral name: the test's own name would end up in {{agentDir}}.
	e := &descEnv{t: t}
	var err error
	if e.cwd, err = os.MkdirTemp("", "pi-tooldesc-"); err != nil {
		t.Fatal(err)
	}
	if e.agent, err = os.MkdirTemp("", "pi-tooldesc-agentdir-"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(e.cwd); os.RemoveAll(e.agent) })
	t.Setenv("PIG_CODING_AGENT_DIR", e.agent) // hermetic: the developer's own subagents.json must not leak into "default is full"
	t.Setenv("PIG_USE_PI_DIRS", "")
	oldWarn := descriptionWarn
	descriptionWarn = func(m string) { e.warns = append(e.warns, m) }
	resetTypes()
	resetSettingsView()
	t.Cleanup(func() {
		featureScheduling, featureWorktreeIsolation, featureWorkflows = oldS, oldI, oldW
		descriptionWarn = oldWarn
		resetTypes()
		resetSettingsView()
	})
	if settings != nil {
		writeJSONFile(t, filepath.Join(e.cwd, ".pi", "subagents.json"), settings)
	}
	for _, fn := range before {
		fn(e)
	}
	app := newApp(sdk.New("tintinweb-subagents"), nil)
	defs := app.toolDefinitions(e.cwd)
	t.Cleanup(func() { releaseManagerRegistry(app.entry); app.mgr.dispose() })
	return defs[0], e
}

func (e *descEnv) write(rel, content string) {
	e.t.Helper()
	path := filepath.Join(e.cwd, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func propNames(def sdk.ToolDefinition) []string {
	props, _ := def.Parameters["properties"].(map[string]any)
	var out []string
	for k := range props {
		out = append(out, k)
	}
	return out
}

func has(list []string, s string) bool { return inList(list, s) }

func TestToolDescriptionModeTwins(t *testing.T) {
	const f = "tool-description-mode"
	tw(t, f, "defaults to the full description", func(t *testing.T) {
		def, _ := setupDesc(t, nil)
		eq(t, contains(def.Description, "## Usage notes"), true)
		eq(t, contains(def.Description, "## Writing the prompt"), true)
		eq(t, contains(def.Description, "very thorough"), true) // a late Explore sentence survives: the full agent descriptions are embedded
	})
	tw(t, f, "compact mode swaps in the short description with one-line type list", func(t *testing.T) {
		def, _ := setupDesc(t, obj{"toolDescriptionMode": "compact"})
		d := def.Description
		eq(t, contains(d, "Launch an autonomous agent"), true)
		eq(t, contains(d, "## Usage notes") || contains(d, "## Writing the prompt"), false)
		eq(t, contains(d, "- general-purpose:"), true)
		eq(t, contains(d, "- Explore: Fast read-only search agent for locating code. (Tools:"), true)
		eq(t, contains(d, "very thorough"), false)
		eq(t, jsLen(d) < 1600, true) // the point of the feature: materially smaller than the full version
	})
	tw(t, f, "invalid mode in the settings file is dropped — full description", func(t *testing.T) {
		def, _ := setupDesc(t, obj{"toolDescriptionMode": "tiny"})
		eq(t, contains(def.Description, "## Usage notes"), true)
	})
	tw(t, f, "compact keeps every load-bearing contract — fails when a behavior change forgets compact", func(t *testing.T) {
		def, _ := setupDesc(t, obj{"toolDescriptionMode": "compact"})
		for _, contract := range []string{"run_in_background", "resume", "steer_subagent", `isolation: "worktree"`, ".pi/agents/", "self-contained"} {
			eq(t, contains(def.Description, contract), true)
		}
	})
	tw(t, f, "full states every load-bearing contract in the description or the schema", func(t *testing.T) {
		def, _ := setupDesc(t, nil)
		params, _ := json.Marshal(def.Parameters)
		visible := def.Description + "\n" + string(params)
		for _, contract := range []string{"run_in_background", "resume", "steer_subagent", "worktree", ".pi/agents/", "self-contained", "model", "thinking", "inherit_context"} {
			eq(t, contains(visible, contract), true)
		}
	})
	tw(t, f, "every strategy param carries a real description of its own", func(t *testing.T) {
		def, _ := setupDesc(t, nil)
		props := def.Parameters["properties"].(map[string]any)
		for _, name := range []string{"run_in_background", "model", "thinking", "inherit_context"} {
			d, _ := props[name].(map[string]any)["description"].(string)
			eq(t, jsLen(d) > 40, true) // an explanation the model can act on, not a bare label
		}
	})
	tw(t, f, "custom mode renders the project template with placeholders substituted", func(t *testing.T) {
		def, e := setupDesc(t, obj{"toolDescriptionMode": "custom"}, func(e *descEnv) {
			e.write(".pi/agent-tool-description.md", "My agents:\n{{typeList}}\n\nGlobal dir: {{agentDir}}\nUnknown: {{nope}}\nCost: $& stays literal")
		})
		d := def.Description
		eq(t, contains(d, "My agents:"), true)
		eq(t, contains(d, "- general-purpose:"), true)     // {{typeList}} expanded
		eq(t, contains(d, "Global dir: "+e.agent), true)   // {{agentDir}} expanded
		eq(t, contains(d, "Unknown: {{nope}}"), true)      // an unknown placeholder is left verbatim
		eq(t, contains(d, "Cost: $& stays literal"), true) // no $-pattern expansion
		eq(t, contains(d, "## Usage notes"), false)
		eq(t, len(e.warns), 1)
		eq(t, contains(e.warns[0], "unknown placeholder {{nope}} left as-is"), true)
	})
	tw(t, f, "custom mode falls back to the global file when no project file exists", func(t *testing.T) {
		def, _ := setupDesc(t, obj{"toolDescriptionMode": "custom"}, func(e *descEnv) {
			if err := os.WriteFile(filepath.Join(e.agent, "agent-tool-description.md"), []byte("GLOBAL CUSTOM\n{{compactTypeList}}"), 0o644); err != nil {
				t.Fatal(err)
			}
		})
		eq(t, contains(def.Description, "GLOBAL CUSTOM"), true)
		eq(t, contains(def.Description, "- Explore: Fast read-only search agent for locating code. (Tools:"), true)
	})
	custom := func(t *testing.T, settings map[string]any, tpl string) string {
		def, _ := setupDesc(t, settings, func(e *descEnv) { e.write(".pi/agent-tool-description.md", tpl) })
		return def.Description
	}
	tw(t, f, "{{scheduleGuideline}} expands to the schedule bullet when scheduling is on (default)", func(t *testing.T) {
		eq(t, contains(custom(t, obj{"toolDescriptionMode": "custom"}, "RULES:{{scheduleGuideline}}\nEND"), "RULES:\n- Use `schedule` only when"), true) // the expansion carries its own leading "\n- "
	})
	tw(t, f, "{{scheduleGuideline}} expands to the empty string when scheduling is disabled", func(t *testing.T) {
		d := custom(t, obj{"toolDescriptionMode": "custom", "schedulingEnabled": false}, "RULES:{{scheduleGuideline}}\nEND")
		eq(t, contains(d, "RULES:\nEND"), true)
		eq(t, contains(d, "schedule"), false)
	})
	tw(t, f, "{{isolationGuideline}} expands to the isolation bullet when worktrees are on (default)", func(t *testing.T) {
		eq(t, contains(custom(t, obj{"toolDescriptionMode": "custom"}, "RULES:{{isolationGuideline}}\nEND"), "RULES:\n- Use isolation: \"worktree\""), true)
	})
	tw(t, f, "{{isolationGuideline}} expands to the empty string when worktree isolation is disabled", func(t *testing.T) {
		d := custom(t, obj{"toolDescriptionMode": "custom", "worktreeIsolation": false}, "RULES:{{isolationGuideline}}\nEND")
		eq(t, contains(d, "RULES:\nEND"), true)
		eq(t, contains(d, "isolation"), false)
	})
	tw(t, f, "every documented placeholder is replaced — no {{ }} residue", func(t *testing.T) {
		d := custom(t, obj{"toolDescriptionMode": "custom"}, "A {{typeList}} B {{compactTypeList}} C {{agentDir}} D {{scheduleGuideline}} E {{isolationGuideline}} F")
		eq(t, contains(d, "{{") || contains(d, "}}"), false)
	})
	tw(t, f, "the shipped example template renders byte-identical to the full description", func(t *testing.T) {
		// examples/agent-tool-description.md of the pinned commit (port/upstream-extra: the oracle vendors src and test only).
		example, err := os.ReadFile(filepath.Join("..", "..", "port", "upstream-extra", "agent-tool-description.md"))
		if err != nil {
			t.Skip("port/upstream-extra is not beside this module (a mutation run tests a copy of it)")
		}
		customDesc, customEnv := setupDesc(t, obj{"toolDescriptionMode": "custom"}, func(e *descEnv) { e.write(".pi/agent-tool-description.md", string(example)) })
		fullDesc, fullEnv := setupDesc(t, obj{"toolDescriptionMode": "full"})
		// The two instances run in different hermetic directories; {{agentDir}} is the only thing that may differ.
		eq(t, strings.ReplaceAll(customDesc.Description, customEnv.agent, "<agent>"), strings.ReplaceAll(fullDesc.Description, fullEnv.agent, "<agent>"))
	})
	tw(t, f, "custom mode without a file falls back to the full description with a warning", func(t *testing.T) {
		def, e := setupDesc(t, obj{"toolDescriptionMode": "custom"})
		eq(t, contains(def.Description, "## Usage notes"), true)
		eq(t, len(e.warns), 1)
		eq(t, contains(e.warns[0], "no agent-tool-description.md found"), true)
	})
	tw(t, f, "advertises `schedule` by default", func(t *testing.T) {
		def, _ := setupDesc(t, nil)
		eq(t, has(propNames(def), "schedule"), true)
	})
	tw(t, f, "removes `schedule` from the tool schema when scheduling is disabled", func(t *testing.T) {
		def, _ := setupDesc(t, obj{"schedulingEnabled": false})
		names := propNames(def)
		eq(t, has(names, "schedule"), false)
		eq(t, has(names, "prompt") && has(names, "description") && has(names, "subagent_type"), true) // one field, not the tool
	})
	tw(t, f, "advertises `isolation` in schema and prose by default", func(t *testing.T) {
		def, _ := setupDesc(t, nil)
		eq(t, has(propNames(def), "isolation"), true)
		eq(t, contains(def.Description, `Use isolation: "worktree"`), true)
	})
	tw(t, f, "drops both when worktree isolation is disabled", func(t *testing.T) {
		def, _ := setupDesc(t, obj{"worktreeIsolation": false})
		names := propNames(def)
		eq(t, has(names, "isolation"), false)
		eq(t, contains(def.Description, "isolation"), false)
		eq(t, has(names, "prompt") && has(names, "description") && has(names, "subagent_type") && has(names, "schedule"), true) // the neighbouring gate is unaffected
	})
	tw(t, f, "drops the compact description's bullet too", func(t *testing.T) {
		def, _ := setupDesc(t, obj{"toolDescriptionMode": "compact"})
		eq(t, contains(def.Description, `isolation: "worktree"`), true)
	})
	tw(t, f, "compact mode says nothing about isolation when disabled", func(t *testing.T) {
		def, _ := setupDesc(t, obj{"toolDescriptionMode": "compact", "worktreeIsolation": false})
		eq(t, contains(def.Description, "isolation"), false)
		eq(t, contains(def.Description, "resume continues a previous agent by ID"), true) // the gate trims a suffix, not the list
	})

	withAgent := func(t *testing.T, name, frontmatter string, settings map[string]any) string {
		def, _ := setupDesc(t, settings, func(e *descEnv) {
			extra := ""
			if frontmatter != "" {
				extra = frontmatter + "\n"
			}
			e.write(filepath.Join(".pi", "agents", name+".md"), "---\ndescription: "+name+" agent.\n"+extra+"---\n\nBody.\n")
		})
		return def.Description
	}
	tw(t, f, "`tools: none` never claims the full built-in set", func(t *testing.T) {
		eq(t, contains(withAgent(t, "quiet", "tools: none", nil), "- quiet: quiet agent. (Tools: *)"), false)
	})
	tw(t, f, "`tools: none` says none only when the agent can call nothing at all", func(t *testing.T) {
		for _, fm := range []string{"tools: none\nextensions: false", "tools: none\nisolated: true"} {
			eq(t, contains(withAgent(t, "silent", fm, nil), "- silent: silent agent. (Tools: none)"), true)
		}
	})
	tw(t, f, "`tools: none` with extensions loaded is not described as having no tools", func(t *testing.T) {
		d := withAgent(t, "probe", "tools: none\nextensions: \"./ext-alpha.mjs\"", nil)
		eq(t, contains(d, "- probe: probe agent. (Tools: no built-ins, extension tools only)"), true)
		eq(t, contains(d, "- probe: probe agent. (Tools: *)") || contains(d, "- probe: probe agent. (Tools: none)"), false)
	})
	tw(t, f, "an ext:-only `tools:` is described by what it actually has", func(t *testing.T) {
		d := withAgent(t, "extonly", "tools: \"ext:probe.mjs\"", nil)
		eq(t, contains(d, "- extonly: extonly agent. (Tools: no built-ins, extension tools only)"), true)
		eq(t, contains(d, "- extonly: extonly agent. (Tools: *)"), false)
	})
	tw(t, f, "compact mode shares the suffix builder and must not diverge", func(t *testing.T) {
		d := withAgent(t, "quiet", "tools: none\nextensions: false", obj{"toolDescriptionMode": "compact"})
		eq(t, contains(d, "- quiet: quiet agent. (Tools: none)"), true)
		eq(t, contains(d, "- quiet: quiet agent. (Tools: *)"), false)
	})
	tw(t, f, "an omitted `tools:` still renders as * — absent means all built-ins", func(t *testing.T) {
		eq(t, contains(withAgent(t, "broad", "", nil), "- broad: broad agent. (Tools: *)"), true)
	})
	tw(t, f, "a narrowed `tools:` still lists the names it actually has", func(t *testing.T) {
		eq(t, contains(withAgent(t, "narrow", "tools: read, grep", nil), "- narrow: narrow agent. (Tools: read, grep)"), true)
	})
}
