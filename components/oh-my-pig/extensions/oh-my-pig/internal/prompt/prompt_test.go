package prompt

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/eval"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/toolcmd"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

// TestPigletSystemPromptMatchesEmbedded pins that piglets/oh-my-pig/prompts/system.md
// and the embedded internal/prompt/system-prompt.md stay byte-identical.
func TestPigletSystemPromptMatchesEmbedded(t *testing.T) {
	pigletPromptPath := filepath.Join("..", "..", "..", "..", "..", "..", "piglets", "oh-my-pig", "prompts", "system.md")
	data, err := os.ReadFile(pigletPromptPath)
	if err != nil {
		t.Fatalf("read %s: %v", pigletPromptPath, err)
	}
	if string(data) != systemPromptTemplate {
		t.Fatalf("piglets/oh-my-pig/prompts/system.md differs from embedded system-prompt.md")
	}
}

// fakeURLHandler is a minimal urls.Handler + urls.PromptDocer for golden tests.
type fakeURLHandler struct {
	scheme string
	doc    string
}

func (f fakeURLHandler) Scheme() string { return f.scheme }
func (f fakeURLHandler) Spec() urls.Spec {
	return urls.Spec{Backing: urls.BackingVirtual, Selectors: urls.SelectorsNone}
}
func (f fakeURLHandler) Resolve(urls.Context, urls.URL) (*urls.Resource, error) {
	return &urls.Resource{Content: "ok"}, nil
}
func (f fakeURLHandler) PromptDoc(urls.SchemeHost) string { return f.doc }

// TestGoldenWave1Tools pins the complete assembled system prompt for wave 1's ten tools
// with no URL handlers and no eval preludes registered.
func TestGoldenWave1Tools(t *testing.T) {
	urls.ResetForTests()
	eval.ResetPreludesForTests()
	t.Cleanup(func() {
		urls.ResetForTests()
		eval.ResetPreludesForTests()
	})

	got := BuildSystemPrompt(BuildOptions{
		Cwd:       "/work/repo",
		ToolNames: []string{"read", "edit", "write", "grep", "glob", "bash", "todo", "ask", "web_search", "fetch"},
		ContextFiles: []ContextFile{
			{Path: "/work/repo/AGENTS.md", Content: "Always run go test before committing.", Depth: 0, HasDepth: true},
		},
		Skills: []SkillEntry{
			{Name: "deploy", Description: "Deploy services safely to staging.", FilePath: "/work/repo/.agents/skills/deploy/SKILL.md"},
		},
		Model:       "anthropic/claude-opus-4",
		Personality: PersonalityDefault,
		Environment: []EnvEntry{
			{Label: "OS", Value: "darwin 25.5.0"},
			{Label: "Arch", Value: "arm64"},
		},
		SetActiveRepoContext: true,
		ActiveRepoContext:    nil,
	})

	goldenPath := filepath.Join("testdata", "golden_prompt.txt")
	wantBytes, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden file %s: %v", goldenPath, err)
	}
	want := strings.TrimRight(string(wantBytes), "\n")
	if got != want {
		t.Fatalf("assembled wave-1 prompt mismatch:\n--- GOT ---\n%s\n--- WANT ---\n%s", got, want)
	}
}

// TestGoldenDefaultToolsWithURLAndPrelude pins the assembled system prompt against
// the omp v19.1.1 oracle for omp's interactive default top-level tools with a fake
// URL handler and a fake browser eval prelude registered.
func TestGoldenDefaultToolsWithURLAndPrelude(t *testing.T) {
	urls.ResetForTests()
	eval.ResetPreludesForTests()
	t.Cleanup(func() {
		urls.ResetForTests()
		eval.ResetPreludesForTests()
	})

	urls.Register(fakeURLHandler{
		scheme: "fake",
		doc:    "fake://<name>: fake test resource.",
	})
	eval.RegisterPrelude(eval.Prelude{
		Name: "browser",
		Guidance: func() string {
			return "# Browser sessions\n- Fake prelude guidance line."
		},
	})

	got := BuildSystemPrompt(BuildOptions{
		Cwd: "/work/repo",
		ToolNames: []string{
			"read", "bash", "edit", "ask", "eval", "glob", "grep",
			"task", "wait", "todo", "web_search", "write",
		},
		ContextFiles: []ContextFile{
			{Path: "/work/repo/AGENTS.md", Content: "Always run go test before committing.", Depth: 0, HasDepth: true},
		},
		Skills: []SkillEntry{
			{Name: "deploy", Description: "Deploy services safely to staging.", FilePath: "/work/repo/.agents/skills/deploy/SKILL.md"},
		},
		EvalPreludes: eval.EnabledPreludes(sdk.Context{}),
		Model:        "anthropic/claude-opus-4",
		Personality:  PersonalityDefault,
		Environment: []EnvEntry{
			{Label: "OS", Value: "darwin 25.5.0"},
			{Label: "Arch", Value: "arm64"},
		},
		SetActiveRepoContext: true,
		ActiveRepoContext:    nil,
	})

	goldenPath := filepath.Join("testdata", "golden_prompt_full.txt")
	wantBytes, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden file %s: %v", goldenPath, err)
	}
	want := strings.TrimRight(string(wantBytes), "\n")
	if got != want {
		t.Fatalf("assembled full prompt mismatch:\n--- GOT ---\n%s\n--- WANT ---\n%s", got, want)
	}
}

// TestGoldenToolCommandsDefaultSession pins the assembled system prompt against the
// omp v19.1.1 oracle for a default interactive session with tools.commands=true
// (catalog mode) and omp's real tool-command catalog entries for ast_edit, debug, lsp.
func TestGoldenToolCommandsDefaultSession(t *testing.T) {
	urls.ResetForTests()
	eval.ResetPreludesForTests()
	prevToolCommands := ToolCommands
	t.Cleanup(func() {
		urls.ResetForTests()
		eval.ResetPreludesForTests()
		ToolCommands = prevToolCommands
	})

	ToolCommands = func(sdk.Context) ([]string, string) {
		return []string{"ast_edit", "debug", "lsp"}, strings.Join([]string{
			"## More commands (help on demand)",
			"- `ast_edit` — Perform AST-aware code edits (SHOULD be preferred for codemods)",
			"- `debug` — Debug a running process with DAP (debugger adapter protocol)",
			"- `lsp` — Query LSP (language server) for diagnostics, hover info, and references",
			"",
			"Run `<tool> --help` before first use.",
		}, "\n")
	}

	mounted, docs := ToolCommands(sdk.Context{})
	got := BuildSystemPrompt(BuildOptions{
		Cwd: "/work/repo",
		ToolNames: []string{
			"read", "bash", "edit", "ask", "eval", "glob", "grep",
			"task", "wait", "todo", "web_search", "write",
		},
		MountedCommands: mounted,
		ToolCommandDocs: docs,
		ContextFiles: []ContextFile{
			{Path: "/work/repo/AGENTS.md", Content: "Always run go test before committing.", Depth: 0, HasDepth: true},
		},
		Skills: []SkillEntry{
			{Name: "deploy", Description: "Deploy services safely to staging.", FilePath: "/work/repo/.agents/skills/deploy/SKILL.md"},
		},
		Model:       "anthropic/claude-opus-4",
		Personality: PersonalityDefault,
		Environment: []EnvEntry{
			{Label: "OS", Value: "darwin 25.5.0"},
			{Label: "Arch", Value: "arm64"},
		},
		SetActiveRepoContext: true,
		ActiveRepoContext:    nil,
	})

	goldenPath := filepath.Join("testdata", "golden_prompt_toolcmd.txt")
	wantBytes, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden file %s: %v", goldenPath, err)
	}
	want := strings.TrimRight(string(wantBytes), "\n")
	if got != want {
		t.Fatalf("assembled toolcmd prompt mismatch:\n--- GOT ---\n%s\n--- WANT ---\n%s", got, want)
	}
}

// TestOracleFixtureParity verifies byte-for-byte equality (blank lines included,
// apart from the intentional oh-my-pig role line) between BuildSystemPrompt and
// stored outputs generated by omp v19.1.1's own template renderer across 49 option sets.
func TestOracleFixtureParity(t *testing.T) {
	urls.ResetForTests()
	eval.ResetPreludesForTests()
	t.Cleanup(func() {
		urls.ResetForTests()
		eval.ResetPreludesForTests()
	})

	type fixtureSpec struct {
		Tools        []string `json:"tools"`
		ContextFiles []struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		} `json:"contextFiles"`
		Skills []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Hide        bool   `json:"hide"`
		} `json:"skills"`
		Rules []struct {
			Name        string   `json:"name"`
			Globs       []string `json:"globs"`
			Description string   `json:"description"`
		} `json:"rules"`
		AlwaysApply []struct {
			Name    string `json:"name"`
			Content string `json:"content"`
		} `json:"alwaysApply"`
		Model       string   `json:"model"`
		Personality string   `json:"personality"`
		URLs        []string `json:"urls"`
		URLRef      []string `json:"urlRef"`
		Preludes    []struct {
			Name     string `json:"name"`
			Guidance string `json:"guidance"`
		} `json:"preludes"`
		Bias            string   `json:"bias"`
		Eager           bool     `json:"eager"`
		EagerAlways     bool     `json:"eagerAlways"`
		TaskBatch       bool     `json:"taskBatch"`
		Scout           bool     `json:"scout"`
		IRC             bool     `json:"irc"`
		Secrets         bool     `json:"secrets"`
		Shaping         string   `json:"shaping"`
		IntentField     string   `json:"intentField"`
		IntentGuidance  string   `json:"intentGuidance"`
		Mermaid         bool     `json:"mermaid"`
		SVG             bool     `json:"svg"`
		Embeds          bool     `json:"embeds"`
		AutoGraph       bool     `json:"autoGraph"`
		Reactions       bool     `json:"reactions"`
		EncourageEdits  bool     `json:"encourageEdits"`
		AutoQA          bool     `json:"autoQa"`
		Ovnfs           string   `json:"ovnfs"`
		Subagent        bool     `json:"subagent"`
		ActiveRepo      string   `json:"activeRepo"`
		Append          string   `json:"append"`
		Mounted         []string `json:"mounted"`
		ToolCommandDocs string   `json:"toolCommandDocs"`
	}
	type fixtureCase struct {
		Name     string      `json:"name"`
		Spec     fixtureSpec `json:"spec"`
		Expected string      `json:"expected"`
	}

	raw, err := os.ReadFile(filepath.Join("testdata", "oracle", "fixtures.json"))
	if err != nil {
		t.Fatalf("read oracle fixtures: %v", err)
	}
	var cases []fixtureCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("unmarshal oracle fixtures: %v", err)
	}
	if len(cases) < 40 {
		t.Fatalf("expected at least 40 oracle fixture cases, got %d", len(cases))
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			s := tc.Spec
			cfs := make([]ContextFile, 0, len(s.ContextFiles))
			for _, f := range s.ContextFiles {
				cfs = append(cfs, ContextFile{Path: f.Path, Content: f.Content, Depth: 0, HasDepth: true})
			}
			skills := make([]SkillEntry, 0, len(s.Skills))
			for _, k := range s.Skills {
				skills = append(skills, SkillEntry{Name: k.Name, Description: k.Description, Hide: k.Hide})
			}
			rules := make([]Rule, 0, len(s.Rules))
			for _, r := range s.Rules {
				rules = append(rules, Rule{Name: r.Name, Globs: r.Globs, Description: r.Description})
			}
			always := make([]AlwaysApplyRule, 0, len(s.AlwaysApply))
			for _, a := range s.AlwaysApply {
				always = append(always, AlwaysApplyRule{Name: a.Name, Content: a.Content})
			}
			preludes := make([]eval.Prelude, 0, len(s.Preludes))
			for _, p := range s.Preludes {
				g := p.Guidance
				preludes = append(preludes, eval.Prelude{
					Name:     p.Name,
					Guidance: func() string { return g },
				})
			}
			var activeRepo *ActiveRepoContext
			if s.ActiveRepo != "" {
				activeRepo = &ActiveRepoContext{RelativeRepoRoot: s.ActiveRepo}
			}
			taskBatch := s.TaskBatch
			scout := s.Scout
			encourage := s.EncourageEdits
			urlLines := s.URLs
			if urlLines == nil {
				urlLines = []string{}
			}
			urlRef := s.URLRef
			if urlRef == nil {
				urlRef = []string{}
			}
			got := BuildSystemPrompt(BuildOptions{
				ToolNames:            s.Tools,
				MountedCommands:      s.Mounted,
				ToolCommandDocs:      s.ToolCommandDocs,
				ContextFiles:         cfs,
				Skills:               skills,
				Rules:                rules,
				AlwaysApplyRules:     always,
				InternalURLs:         urlLines,
				InternalURLReference: urlRef,
				EvalPreludes:         preludes,
				Model:                s.Model,
				Personality:          Personality(s.Personality),
				Environment: []EnvEntry{
					{Label: "OS", Value: "darwin 25.5.0"},
					{Label: "Arch", Value: "arm64"},
				},
				SetActiveRepoContext: true,
				ActiveRepoContext:    activeRepo,
				AppendSystemPrompt:   s.Append,
				Subagent:             s.Subagent,
				DelegationBias:       s.Bias,
				EagerTasks:           s.Eager,
				EagerTasksAlways:     s.EagerAlways,
				TaskBatch:            &taskBatch,
				ScoutAvailable:       &scout,
				TaskIRCEnabled:       s.IRC,
				EncourageEdits:       &encourage,
				RenderMermaid:        s.Mermaid,
				RenderSVG:            s.SVG,
				RenderEmbeds:         s.Embeds,
				AutoGraph:            s.AutoGraph,
				Reactions:            s.Reactions,
				IntentTracing:        s.IntentField != "",
				IntentField:          s.IntentField,
				IntentGuidance:       s.IntentGuidance,
				SecretsEnabled:       s.Secrets,
				ResultShaping:        s.Shaping,
				AutoQAEnabled:        s.AutoQA,
				Ovnfs:                s.Ovnfs,
			})
			if got != tc.Expected {
				gotLines := strings.Split(got, "\n")
				wantLines := strings.Split(tc.Expected, "\n")
				diffLine := 0
				for diffLine < len(gotLines) && diffLine < len(wantLines) && gotLines[diffLine] == wantLines[diffLine] {
					diffLine++
				}
				var gotLine, wantLine string
				if diffLine < len(gotLines) {
					gotLine = gotLines[diffLine]
				} else {
					gotLine = "<EOF>"
				}
				if diffLine < len(wantLines) {
					wantLine = wantLines[diffLine]
				} else {
					wantLine = "<EOF>"
				}
				t.Fatalf("mismatch at line %d (got %d lines, want %d lines)\n got: %q\nwant: %q", diffLine+1, len(gotLines), len(wantLines), gotLine, wantLine)
			}
		})
	}
}

// TestUnregisteredToolsNotNamed proves that tools not present in the session's active
// tool list are not named in tool-gated sections of the rendered system prompt.
func TestUnregisteredToolsNotNamed(t *testing.T) {
	urls.ResetForTests()
	t.Cleanup(urls.ResetForTests)

	got := BuildSystemPrompt(BuildOptions{
		Cwd:                  "/work/repo",
		ToolNames:            []string{"read"},
		ContextFiles:         []ContextFile{},
		Skills:               []SkillEntry{},
		Personality:          PersonalityNone,
		Environment:          []EnvEntry{{Label: "OS", Value: "linux 6.8.0"}, {Label: "Arch", Value: "x64"}},
		SetActiveRepoContext: true,
	})

	// Only "read" is registered: none of the unregistered tools or their gated sections may appear.
	forbidden := []string{
		"`edit`",
		"`write`",
		"`bash`",
		"`grep`",
		"`glob`",
		"`find`",
		"`lsp`",
		"`task`",
		"`todo`",
		"`think`",
		"# Delegation",
		"⁂ Scratchpad",
		"Ask before destructive commands",
	}
	for _, needle := range forbidden {
		if strings.Contains(got, needle) {
			t.Errorf("unregistered tool reference %q leaked into prompt:\n%s", needle, got)
		}
	}
	if !strings.Contains(got, "# Tool Inventory\nread") {
		t.Errorf("registered tool `read` missing from Tool Inventory")
	}
	if !strings.Contains(got, "File/directory reads: `read`") {
		t.Errorf("registered tool `read` missing from Specialized Tools")
	}
	if !strings.Contains(got, "Read small files whole; ranges only for large files.") {
		t.Errorf("registered tool `read` missing from Exploration")
	}
	if !strings.Contains(got, "NEVER run destructive git commands") {
		t.Errorf("expected fallback destructive-command wording when `ask` is not registered")
	}
}

// TestWorkstationOSFromPlatformAndRelease pins omp's workstation block format.
// upstream: system-prompt-kernel.test.ts: "identifies the host even when os.version() returns \"unknown\""
func TestWorkstationOSFromPlatformAndRelease(t *testing.T) {
	got := BuildSystemPrompt(BuildOptions{
		Cwd:                  t.TempDir(),
		ContextFiles:         []ContextFile{},
		ToolNames:            []string{},
		Environment:          []EnvEntry{{Label: "OS", Value: "darwin 25.5.0"}, {Label: "Arch", Value: "arm64"}},
		SetActiveRepoContext: true,
	})
	if !strings.Contains(got, "- OS: darwin 25.5.0") {
		t.Fatalf("missing OS line in workstation block:\n%s", got)
	}
	if !strings.Contains(got, "- Arch: arm64") {
		t.Fatalf("missing Arch line in workstation block:\n%s", got)
	}
	if strings.Contains(got, "unknown") {
		t.Fatalf("workstation block leaked 'unknown'")
	}
}

// upstream: system-prompt-model.test.ts: "renders the model identifier into the workstation block when provided"
// upstream: system-prompt-model.test.ts: "omits the model line when no model is provided"
func TestModelInWorkstationBlock(t *testing.T) {
	withModel := BuildSystemPrompt(BuildOptions{
		Cwd:                  t.TempDir(),
		ContextFiles:         []ContextFile{},
		ToolNames:            []string{},
		Model:                "anthropic/claude-opus-4",
		SetActiveRepoContext: true,
	})
	if !strings.Contains(withModel, "- Model: anthropic/claude-opus-4") {
		t.Fatalf("expected Model line in workstation block")
	}

	withoutModel := BuildSystemPrompt(BuildOptions{
		Cwd:                  t.TempDir(),
		ContextFiles:         []ContextFile{},
		ToolNames:            []string{},
		SetActiveRepoContext: true,
	})
	if strings.Contains(withoutModel, "Model:") {
		t.Fatalf("expected Model line omitted when empty")
	}
}

// upstream: system-prompt-dedup.test.ts: "keeps per-request date/cwd out of the system prompt footer"
func TestKeepsDateAndCwdOutOfSystemPrompt(t *testing.T) {
	projectDir := filepath.Join(t.TempDir(), "my-unique-project-dir")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	got := BuildSystemPrompt(BuildOptions{
		Cwd:                  projectDir,
		ContextFiles:         []ContextFile{},
		ToolNames:            []string{},
		SetActiveRepoContext: true,
	})
	normalized := NormalizePromptPath(projectDir)
	for _, forbidden := range []string{normalized, "Today:", "current working directory"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("system prompt must not contain per-request %q", forbidden)
		}
	}
}

// upstream: system-prompt-dedup.test.ts: "renders active child repo context in the main system prompt"
func TestActiveChildRepoContext(t *testing.T) {
	parentDir := t.TempDir()
	gitDir := filepath.Join(parentDir, "active-project", ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := BuildSystemPrompt(BuildOptions{
		Cwd:          parentDir,
		ContextFiles: []ContextFile{},
		ToolNames:    []string{},
	})
	if !strings.Contains(got, "<active-repo-context>") || !strings.Contains(got, "`active-project/`") {
		t.Fatalf("expected active child repo context in prompt:\n%s", got)
	}

	// Adding a second sibling git repo makes active repo ambiguous -> omitted.
	gitDir2 := filepath.Join(parentDir, "other-project", ".git")
	if err := os.MkdirAll(gitDir2, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir2, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gotTwo := BuildSystemPrompt(BuildOptions{
		Cwd:          parentDir,
		ContextFiles: []ContextFile{},
		ToolNames:    []string{},
	})
	if strings.Contains(gotTwo, "<active-repo-context>") {
		t.Fatalf("expected active child repo context omitted when multiple child repos exist")
	}
}

// upstream: system-prompt-personality.test.ts:
//   - "replaces the selected preset's text with <agentDir>/PERSONALITY.md"
//   - "omits the block for personality \"none\" even when PERSONALITY.md exists (subagent contract)"
//   - "falls back to the configured preset when the file is empty"
//   - "renders the selected preset when no override file exists"
func TestPersonalityOverride(t *testing.T) {
	const defaultMarker = "Evidence-first terse engineer"
	const override = "Follow ASD-STE100 Simplified Technical English for all responses."

	t.Run("renders default preset when no override exists", func(t *testing.T) {
		got := BuildSystemPrompt(BuildOptions{
			AgentDir:             t.TempDir(),
			ContextFiles:         []ContextFile{},
			Personality:          PersonalityDefault,
			SetActiveRepoContext: true,
		})
		if !strings.Contains(got, "# Personality") || !strings.Contains(got, defaultMarker) {
			t.Fatalf("expected default personality preset")
		}
	})

	t.Run("replaces preset with PERSONALITY.md", func(t *testing.T) {
		agentDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(agentDir, "PERSONALITY.md"), []byte(override), 0o644); err != nil {
			t.Fatal(err)
		}
		got := BuildSystemPrompt(BuildOptions{
			AgentDir:             agentDir,
			ContextFiles:         []ContextFile{},
			Personality:          PersonalityDefault,
			SetActiveRepoContext: true,
		})
		if !strings.Contains(got, "# Personality") || !strings.Contains(got, override) {
			t.Fatalf("expected PERSONALITY.md override in prompt")
		}
		if strings.Contains(got, defaultMarker) {
			t.Fatalf("expected default preset replaced by override")
		}
	})

	t.Run("omits block for personality none even with PERSONALITY.md", func(t *testing.T) {
		agentDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(agentDir, "PERSONALITY.md"), []byte(override), 0o644); err != nil {
			t.Fatal(err)
		}
		got := BuildSystemPrompt(BuildOptions{
			AgentDir:             agentDir,
			ContextFiles:         []ContextFile{},
			Personality:          PersonalityNone,
			SetActiveRepoContext: true,
		})
		if strings.Contains(got, "# Personality") || strings.Contains(got, override) {
			t.Fatalf("expected personality block omitted for PersonalityNone")
		}
	})

	t.Run("falls back to preset when PERSONALITY.md is whitespace-only", func(t *testing.T) {
		agentDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(agentDir, "PERSONALITY.md"), []byte("   \n\t\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		got := BuildSystemPrompt(BuildOptions{
			AgentDir:             agentDir,
			ContextFiles:         []ContextFile{},
			Personality:          PersonalityDefault,
			SetActiveRepoContext: true,
		})
		if !strings.Contains(got, defaultMarker) {
			t.Fatalf("expected fallback to default preset for empty PERSONALITY.md")
		}
	})
}

// upstream: system-prompt-context-dedup.test.ts (all 11 cases)
func TestDedupeContainedContextFiles(t *testing.T) {
	file := func(p, content string, depth int) ContextFile {
		return ContextFile{Path: p, Content: content, Depth: depth, HasDepth: true}
	}
	userFile := func(p, content string) ContextFile {
		return ContextFile{Path: p, Content: content, HasDepth: false}
	}
	paths := func(files []ContextFile) []string {
		out := make([]string, len(files))
		for i, f := range files {
			out[i] = f.Path
		}
		return out
	}

	// upstream: system-prompt-context-dedup.test.ts: "keeps only the more authoritative file when two are byte-identical"
	t.Run("byte-identical", func(t *testing.T) {
		content := "Rule one.\n\nRule two.\n\nRule three."
		got := DedupeContainedContextFiles([]ContextFile{
			file("/home/user/.config/AGENTS.md", content, 5),
			file("/project/AGENTS.md", content, 0),
		})
		if want := []string{"/project/AGENTS.md"}; !reflect.DeepEqual(paths(got), want) {
			t.Fatalf("got %v, want %v", paths(got), want)
		}
	})

	// upstream: system-prompt-context-dedup.test.ts: "drops a file whose paragraphs appear contiguously in a more authoritative file"
	t.Run("contiguous subset", func(t *testing.T) {
		got := DedupeContainedContextFiles([]ContextFile{
			file("/home/user/.config/AGENTS.md", "Shared rule A.\n\nShared rule B.\n\nShared rule C.", 5),
			file("/project/AGENTS.md", "Shared rule A.\n\nShared rule B.\n\nShared rule C.\n\nProject-specific rule.", 0),
		})
		if want := []string{"/project/AGENTS.md"}; !reflect.DeepEqual(paths(got), want) {
			t.Fatalf("got %v, want %v", paths(got), want)
		}
	})

	// upstream: system-prompt-context-dedup.test.ts: "keeps a file whose paragraphs appear non-contiguously (interleaved)"
	t.Run("non-contiguous interleaved", func(t *testing.T) {
		got := DedupeContainedContextFiles([]ContextFile{
			file("/home/user/.config/AGENTS.md", "First.\n\nSecond.\n\nThird.", 5),
			file("/project/AGENTS.md", "First.\n\nInterleaved.\n\nSecond.\n\nThird.", 0),
		})
		if want := []string{"/home/user/.config/AGENTS.md", "/project/AGENTS.md"}; !reflect.DeepEqual(paths(got), want) {
			t.Fatalf("got %v, want %v", paths(got), want)
		}
	})

	// upstream: system-prompt-context-dedup.test.ts: "keeps a file whose paragraphs appear with wording changes (containment is exact, not fuzzy)"
	t.Run("wording changes kept", func(t *testing.T) {
		got := DedupeContainedContextFiles([]ContextFile{
			file("/home/user/.config/AGENTS.md", "Always use tabs.\n\nNever commit directly.", 5),
			file("/project/AGENTS.md", "Always use spaces.\n\nNever commit directly to main.", 0),
		})
		if want := []string{"/home/user/.config/AGENTS.md", "/project/AGENTS.md"}; !reflect.DeepEqual(paths(got), want) {
			t.Fatalf("got %v, want %v", paths(got), want)
		}
	})

	// upstream: system-prompt-context-dedup.test.ts: "keeps all files when there is no containment"
	t.Run("no containment", func(t *testing.T) {
		got := DedupeContainedContextFiles([]ContextFile{
			file("/a/AGENTS.md", "Alpha rules.\n\nBeta rules.", 3),
			file("/b/AGENTS.md", "Gamma rules.\n\nDelta rules.", 2),
			file("/c/AGENTS.md", "Epsilon rules.\n\nZeta rules.", 0),
		})
		if want := []string{"/a/AGENTS.md", "/b/AGENTS.md", "/c/AGENTS.md"}; !reflect.DeepEqual(paths(got), want) {
			t.Fatalf("got %v, want %v", paths(got), want)
		}
	})

	// upstream: system-prompt-context-dedup.test.ts: "treats empty content as no blocks, never matched"
	t.Run("empty content", func(t *testing.T) {
		got := DedupeContainedContextFiles([]ContextFile{
			file("/empty/AGENTS.md", "", 5),
			file("/project/AGENTS.md", "Real content.", 0),
		})
		if want := []string{"/empty/AGENTS.md", "/project/AGENTS.md"}; !reflect.DeepEqual(paths(got), want) {
			t.Fatalf("got %v, want %v", paths(got), want)
		}
	})

	// upstream: system-prompt-context-dedup.test.ts: "keeps only the most authoritative file in a transitive chain A⊂B⊂C"
	t.Run("transitive chain", func(t *testing.T) {
		got := DedupeContainedContextFiles([]ContextFile{
			file("/level0/AGENTS.md", "Rule one.\n\nRule two.", 10),
			file("/level1/AGENTS.md", "Rule one.\n\nRule two.\n\nRule three.", 5),
			file("/level2/AGENTS.md", "Rule one.\n\nRule two.\n\nRule three.\n\nRule four.", 0),
		})
		if want := []string{"/level2/AGENTS.md"}; !reflect.DeepEqual(paths(got), want) {
			t.Fatalf("got %v, want %v", paths(got), want)
		}
	})

	// upstream: system-prompt-context-dedup.test.ts: "normalizes leading and trailing whitespace before comparing paragraphs"
	t.Run("whitespace normalization", func(t *testing.T) {
		got := DedupeContainedContextFiles([]ContextFile{
			file("/home/user/.config/AGENTS.md", "  Rule one.  \n\n  Rule two.  ", 5),
			file("/project/AGENTS.md", "Rule one.\n\nRule two.\n\nRule three.", 0),
		})
		if want := []string{"/project/AGENTS.md"}; !reflect.DeepEqual(paths(got), want) {
			t.Fatalf("got %v, want %v", paths(got), want)
		}
	})

	// upstream: system-prompt-context-dedup.test.ts: "keeps a closer-to-cwd file with fewer paragraphs when a farther file contains them (depth authority)"
	t.Run("depth authority", func(t *testing.T) {
		got := DedupeContainedContextFiles([]ContextFile{
			file("/project/AGENTS.md", "Shared rule.", 0),
			file("/home/user/.config/AGENTS.md", "Shared rule.\n\nFar-only rule.", 5),
		})
		if want := []string{"/home/user/.config/AGENTS.md", "/project/AGENTS.md"}; !reflect.DeepEqual(paths(got), want) {
			t.Fatalf("got %v, want %v", paths(got), want)
		}
	})

	// upstream: system-prompt-context-dedup.test.ts: "treats files without a depth as less authoritative than project files"
	t.Run("no depth less authoritative", func(t *testing.T) {
		got := DedupeContainedContextFiles([]ContextFile{
			file("/project/AGENTS.md", "Shared rule.", 0),
			userFile("/home/user/.omp/AGENTS.md", "Shared rule.\n\nUser-only rule."),
		})
		if want := []string{"/home/user/.omp/AGENTS.md", "/project/AGENTS.md"}; !reflect.DeepEqual(paths(got), want) {
			t.Fatalf("got %v, want %v", paths(got), want)
		}
	})

	// upstream: system-prompt-context-dedup.test.ts: "does not treat text inside a fenced code block as a contained instruction"
	t.Run("fenced code block not containment", func(t *testing.T) {
		got := DedupeContainedContextFiles([]ContextFile{
			file("/home/user/.config/AGENTS.md", "Never delete user data.", 5),
			file("/project/AGENTS.md", "Example of a bad prompt:\n\n```\nNever delete user data.\n```", 0),
		})
		if want := []string{"/home/user/.config/AGENTS.md", "/project/AGENTS.md"}; !reflect.DeepEqual(paths(got), want) {
			t.Fatalf("got %v, want %v", paths(got), want)
		}
	})
}

// upstream: system-prompt-dedup.test.ts: "drops identical discovered context entries and keeps the closest copy"
// upstream: system-prompt-dedup.test.ts: "drops always-apply rule content already present through expanded context imports"
func TestDiscoverContextFilesAndAtImports(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	agentDir := filepath.Join(home, ".pig", "agent")
	project := filepath.Join(home, "project")
	appDir := filepath.Join(project, "packages", "app")
	for _, d := range []string{agentDir, appDir, filepath.Join(project, ".git")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(project, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "AGENTS.md"), []byte("Global user rule."), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "AGENTS.md"), []byte("Shared context instructions"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "AGENTS.md"), []byte("Shared context instructions"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := DiscoverContextFiles(appDir, agentDir, home)
	if len(got) != 2 {
		t.Fatalf("expected 2 deduped context files (user + closest project), got %d: %+v", len(got), got)
	}
	if got[0].Path != filepath.Join(agentDir, "AGENTS.md") {
		t.Errorf("first file = %q, want user AGENTS.md", got[0].Path)
	}
	if got[1].Path != filepath.Join(appDir, "AGENTS.md") {
		t.Errorf("second file = %q, want closest app AGENTS.md", got[1].Path)
	}

	// @-import expansion + always-apply rule dedupe
	importTarget := filepath.Join(project, "shared.md")
	if err := os.WriteFile(importTarget, []byte("Shared imported guidance"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "AGENTS.md"), []byte("See @shared.md and `@shared.md`\n```\n@shared.md\n```"), 0o644); err != nil {
		t.Fatal(err)
	}
	expanded := ExpandAtImports("See @shared.md and `@shared.md`\n```\n@shared.md\n```", filepath.Join(project, "AGENTS.md"), home)
	if !strings.Contains(expanded, "See Shared imported guidance and `@shared.md`") {
		t.Fatalf("unexpected @-import expansion: %q", expanded)
	}
	if !strings.Contains(expanded, "```\n@shared.md\n```") {
		t.Fatalf("@-import inside fenced code block must not expand: %q", expanded)
	}

	rendered := BuildSystemPrompt(BuildOptions{
		Cwd: project,
		ContextFiles: []ContextFile{
			{Path: filepath.Join(project, "AGENTS.md"), Content: "Shared imported guidance", Depth: 0, HasDepth: true},
		},
		AlwaysApplyRules: []AlwaysApplyRule{
			{Name: "shared", Path: importTarget, Content: "Shared imported guidance"},
		},
		ToolNames:            []string{},
		SetActiveRepoContext: true,
	})
	if count := strings.Count(rendered, "Shared imported guidance"); count != 1 {
		t.Fatalf("expected always-apply rule deduped against context file (count=%d)", count)
	}
}

// TestAtImportsUpstreamCases ports v19.1.1 @-import repeat and budget semantics.
func TestAtImportsUpstreamCases(t *testing.T) {
	// upstream: discovery/at-imports.test.ts: "expands the same include every time it appears in one document"
	t.Run("expands repeated include every time", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "shared.md"), []byte("SHARED CONTENT\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		root := filepath.Join(dir, "root.md")
		got := ExpandAtImports("A\n@shared.md\nB\n@shared.md\n", root, dir)
		want := "A\nSHARED CONTENT\n\nB\nSHARED CONTENT\n\n"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	// upstream: discovery/at-imports.test.ts: "expands a shared include reached through two different files"
	t.Run("expands diamond include", func(t *testing.T) {
		dir := t.TempDir()
		_ = os.WriteFile(filepath.Join(dir, "shared.md"), []byte("SHARED CONTENT\n"), 0o644)
		_ = os.WriteFile(filepath.Join(dir, "left.md"), []byte("LEFT: @shared.md\n"), 0o644)
		_ = os.WriteFile(filepath.Join(dir, "right.md"), []byte("RIGHT: @shared.md\n"), 0o644)
		got := ExpandAtImports("@left.md\n@right.md\n", filepath.Join(dir, "root.md"), dir)
		if !strings.Contains(got, "LEFT: SHARED CONTENT") || !strings.Contains(got, "RIGHT: SHARED CONTENT") || strings.Contains(got, "@shared.md") {
			t.Fatalf("unexpected diamond expansion: %q", got)
		}
	})

	// upstream: discovery/at-imports.test.ts: "caps total repeated include expansions"
	t.Run("caps total expansions at MaxAtImportExpansions", func(t *testing.T) {
		dir := t.TempDir()
		_ = os.WriteFile(filepath.Join(dir, "leaf.md"), []byte("LEAF\n"), 0o644)
		lines := make([]string, MaxAtImportExpansions+20)
		for i := range lines {
			lines[i] = "@leaf.md"
		}
		got := ExpandAtImports(strings.Join(lines, "\n"), filepath.Join(dir, "root.md"), dir)
		if count := strings.Count(got, "LEAF"); count != MaxAtImportExpansions {
			t.Fatalf("expanded %d leaves, want %d", count, MaxAtImportExpansions)
		}
		if !strings.Contains(got, "@leaf.md") {
			t.Fatalf("expected residual unexpanded @leaf.md after budget exhaustion")
		}
	})
}

// TestFormatUnicodeAndCloseCodeFence verifies A4 and A6 against omp's packages/utils/src/prompt.ts.
func TestFormatUnicodeAndCloseCodeFence(t *testing.T) {
	// NBSP-only line is blank in JS trimEnd/NON_BLANK and gets collapsed in a 2+ blank run.
	got := formatPrompt("a\n\u00a0\n\nb")
	if got != "a\nb" {
		t.Fatalf("formatPrompt NBSP blank line = %q, want %q", got, "a\nb")
	}
	// Trailing Unicode whitespace (U+3000) is trimmed.
	if got := formatPrompt("hello\u3000"); got != "hello" {
		t.Fatalf("formatPrompt ideographic space = %q, want %q", got, "hello")
	}
	// U+0085 (NEL) is NOT whitespace in ECMAScript trimEnd / \S, so it must be preserved.
	if got := formatPrompt("a\n\u0085\n\nb"); got != "a\n\u0085\n\nb" {
		t.Fatalf("formatPrompt U+0085 line = %q, want %q", got, "a\n\u0085\n\nb")
	}
	if got := formatPrompt("hello\u0085"); got != "hello\u0085" {
		t.Fatalf("formatPrompt trailing U+0085 = %q, want %q", got, "hello\u0085")
	}
	rendered := BuildSystemPrompt(BuildOptions{
		ToolNames:            []string{"read"},
		ContextFiles:         []ContextFile{{Path: "/work/AGENTS.md", Content: "Example:\n```go\nfunc f() {}", Depth: 0, HasDepth: true}},
		SetActiveRepoContext: true,
	})
	if !strings.Contains(rendered, "func f() {}\n```\n</file>") {
		t.Fatalf("expected unclosed fence closed before </file>:\n%s", rendered)
	}
}

// TestSystemPromptInventoryUpstreamCases ports key v19.1.1 cases from system-prompt-inventory.test.ts.
func TestSystemPromptInventoryUpstreamCases(t *testing.T) {
	// upstream: system-prompt-inventory.test.ts: "references mounted-only tools by their bare command name"
	t.Run("references mounted-only tools by bare command name", func(t *testing.T) {
		got := BuildSystemPrompt(BuildOptions{
			ToolNames:            []string{"read", "bash"},
			MountedCommands:      []string{"lsp"},
			ToolCommandDocs:      "- `lsp` — Language server",
			ContextFiles:         []ContextFile{},
			SetActiveRepoContext: true,
		})
		if !strings.Contains(got, "MUST use `lsp` for definitions") {
			t.Fatalf("expected specialized lsp rule for mounted command:\n%s", got)
		}
		if !strings.Contains(got, "MUST run `lsp` references first") {
			t.Fatalf("expected workflow lsp rule for mounted command:\n%s", got)
		}
		if strings.Contains(got, "# Tool Inventory\nread, bash, lsp") {
			t.Fatalf("mounted-only lsp must not appear in # Tool Inventory")
		}
	})

	// upstream: system-prompt-inventory.test.ts: "omits mounted-only tools from the compact inventory"
	t.Run("omits mounted-only tools from compact inventory even when passed in ToolNames", func(t *testing.T) {
		got := BuildSystemPrompt(BuildOptions{
			ToolNames:            []string{"read", "web_search"},
			MountedCommands:      []string{"web_search"},
			ToolCommandDocs:      "Mounted web search documentation.",
			ContextFiles:         []ContextFile{},
			SetActiveRepoContext: true,
		})
		if !strings.Contains(got, "# Tool Inventory\nread\n") {
			t.Fatalf("expected mounted web_search omitted from # Tool Inventory:\n%s", got)
		}
		if !strings.Contains(got, "# Tool Commands") || !strings.Contains(got, "Mounted web search documentation.") {
			t.Fatalf("expected Tool Commands section")
		}
	})

	// upstream: system-prompt-inventory.test.ts: "renders exactly one of the map-unknown-code, explore and inline-first delegation rules per bias"
	t.Run("renders exactly one delegation rule per bias", func(t *testing.T) {
		cases := []struct {
			bias  string
			eager bool
			want  [3]int
		}{
			{"eager", false, [3]int{1, 0, 0}},
			{"eager", true, [3]int{1, 0, 0}},
			{"balanced", false, [3]int{0, 1, 0}},
			{"balanced", true, [3]int{1, 0, 0}},
			{"restrained", true, [3]int{1, 0, 0}},
			{"restrained", false, [3]int{0, 0, 1}},
			{"gated", true, [3]int{0, 0, 0}},
		}
		for _, tc := range cases {
			got := BuildSystemPrompt(BuildOptions{
				ToolNames:            []string{"read", "task"},
				DelegationBias:       tc.bias,
				EagerTasks:           tc.eager,
				ContextFiles:         []ContextFile{},
				SetActiveRepoContext: true,
			})
			counts := [3]int{
				strings.Count(got, "Map unknown code via `task`"),
				strings.Count(got, "Unfamiliar subsystem?"),
				strings.Count(got, "Inline first."),
			}
			if counts != tc.want {
				t.Fatalf("bias=%s eager=%v counts=%v, want %v", tc.bias, tc.eager, counts, tc.want)
			}
		}
	})

	// upstream: system-prompt-inventory.test.ts: "appends each advertised prelude's guidance as its own block"
	t.Run("appends each advertised prelude guidance as its own block", func(t *testing.T) {
		got := BuildSystemPrompt(BuildOptions{
			ToolNames:    []string{"read"},
			ContextFiles: []ContextFile{},
			EvalPreludes: []eval.Prelude{
				{Name: "browser"},
				{Name: "computer", Guidance: func() string { return "COMPUTER-GUIDANCE" }},
			},
			SetActiveRepoContext: true,
		})
		if strings.Count(got, "COMPUTER-GUIDANCE") != 1 {
			t.Fatalf("expected COMPUTER-GUIDANCE once:\n%s", got)
		}
		if !strings.Contains(got, "</critical>\n\nCOMPUTER-GUIDANCE\n\n<project-context>") {
			t.Fatalf("expected COMPUTER-GUIDANCE as its own block between </critical> and <project-context>:\n%s", got)
		}
		if !strings.Contains(got, "Native desktop: JS/Python eval `computer` helpers") {
			t.Fatalf("expected computer verification bullet")
		}
		if strings.Contains(got, "No runtime for changed surface") {
			t.Fatalf("expected fallback bullet omitted when both browser and computer enabled")
		}
	})

	// upstream: system-prompt-inventory.test.ts: "links the internal URL reference only while read can fetch it"
	t.Run("links internal URL reference only while read can fetch it", func(t *testing.T) {
		withRead := BuildSystemPrompt(BuildOptions{
			ToolNames:            []string{"read", "bash"},
			InternalURLs:         []string{"proc://: jobs."},
			InternalURLReference: []string{"proc://<name>: named service"},
			ContextFiles:         []ContextFile{},
			SetActiveRepoContext: true,
		})
		if !strings.Contains(withRead, "help://read/urls") || strings.Contains(withRead, "named service") {
			t.Fatalf("with read expected link and no inline ref:\n%s", withRead)
		}
		withoutRead := BuildSystemPrompt(BuildOptions{
			ToolNames:            []string{"bash"},
			InternalURLs:         []string{"proc://: jobs."},
			InternalURLReference: []string{"proc://<name>: named service"},
			ContextFiles:         []ContextFile{},
			SetActiveRepoContext: true,
		})
		if strings.Contains(withoutRead, "help://read/urls") || !strings.Contains(withoutRead, "named service") {
			t.Fatalf("without read expected inline ref and no link:\n%s", withoutRead)
		}
	})
}

// TestDateCwdReminderInjector ports core and B1-regression cases from upstream date-cwd-reminder.test.ts.
func TestDateCwdReminderInjector(t *testing.T) {
	// upstream: date-cwd-reminder.test.ts: "injects the first reminder without mutating the context"
	t.Run("injects first reminder without mutating input", func(t *testing.T) {
		inj := NewDateCwdReminderInjector()
		orig := map[string]any{"role": "user", "content": "hello", "timestamp": float64(1)}
		asst := map[string]any{"role": "assistant", "content": "hi", "timestamp": float64(2)}
		msgs := []any{orig, asst}

		out, changed := inj.Transform(msgs, "2026-08-14", "/work/omp")
		if !changed {
			t.Fatalf("expected changed=true")
		}
		wantText := RenderDateCwdReminder("2026-08-14", "/work/omp") + "\n\nhello"
		if out[0].(map[string]any)["content"] != wantText {
			t.Fatalf("content = %q, want %q", out[0].(map[string]any)["content"], wantText)
		}
		if orig["content"] != "hello" {
			t.Fatalf("input message was mutated: %v", orig["content"])
		}
	})

	// upstream: date-cwd-reminder.test.ts: "prepends a text part before image parts"
	t.Run("prepends text block before image blocks", func(t *testing.T) {
		inj := NewDateCwdReminderInjector()
		img := map[string]any{"type": "image", "data": "img", "mimeType": "image/png"}
		msgs := []any{map[string]any{"role": "user", "content": []any{img}, "timestamp": float64(1)}}

		out, changed := inj.Transform(msgs, "2026-08-14", "/work/omp")
		if !changed {
			t.Fatalf("expected changed=true")
		}
		blocks := out[0].(map[string]any)["content"].([]any)
		if len(blocks) != 2 {
			t.Fatalf("len(blocks) = %d, want 2", len(blocks))
		}
		first := blocks[0].(map[string]any)
		if first["type"] != "text" || first["text"] != RenderDateCwdReminder("2026-08-14", "/work/omp") {
			t.Fatalf("first block = %+v", first)
		}
	})

	// upstream: date-cwd-reminder.test.ts: "leaves contexts without a system prompt or user message untouched"
	t.Run("leaves messages without user or summary message untouched", func(t *testing.T) {
		inj := NewDateCwdReminderInjector()
		msgs := []any{map[string]any{"role": "assistant", "content": "hi"}}
		out, changed := inj.Transform(msgs, "2026-08-14", "/cwd")
		if changed || len(out) != 1 {
			t.Fatalf("expected unchanged")
		}
	})

	// upstream: date-cwd-reminder.test.ts: "keeps prior reminder bytes and moves a changed reminder to the next user turn"
	t.Run("keeps prior reminder bytes on change and attaches to next user turn", func(t *testing.T) {
		inj := NewDateCwdReminderInjector()
		u1 := map[string]any{"role": "user", "content": "first", "timestamp": float64(1)}
		a1 := map[string]any{"role": "assistant", "content": "done", "timestamp": float64(2)}
		u2 := map[string]any{"role": "user", "content": "second", "timestamp": float64(3)}

		firstOut, _ := inj.Transform([]any{u1}, "2026-08-14", "/old")
		secondOut, changed := inj.Transform([]any{u1, a1, u2}, "2026-08-15", "/new")
		if !changed {
			t.Fatalf("expected changed=true")
		}
		if secondOut[0].(map[string]any)["content"] != firstOut[0].(map[string]any)["content"] {
			t.Fatalf("first turn bytes changed: got %q, want %q", secondOut[0].(map[string]any)["content"], firstOut[0].(map[string]any)["content"])
		}
		wantSecond := RenderDateCwdReminder("2026-08-15", "/new") + "\n\nsecond"
		if secondOut[2].(map[string]any)["content"] != wantSecond {
			t.Fatalf("second turn = %q, want %q", secondOut[2].(map[string]any)["content"], wantSecond)
		}
	})

	// B1 case 1: a date rollover during a tool loop must not be lost; the next new user turn receives it.
	t.Run("delivers mid-loop rollover on next new user turn", func(t *testing.T) {
		inj := NewDateCwdReminderInjector()
		u1 := map[string]any{"role": "user", "content": "first", "timestamp": float64(1)}
		a1 := map[string]any{"role": "assistant", "content": "call", "timestamp": float64(2)}
		t1 := map[string]any{"role": "toolResult", "content": "res", "timestamp": float64(3)}
		a2 := map[string]any{"role": "assistant", "content": "done", "timestamp": float64(4)}
		u2 := map[string]any{"role": "user", "content": "second", "timestamp": float64(5)}

		req1, _ := inj.Transform([]any{u1}, "2026-08-14", "/w")
		req2, _ := inj.Transform([]any{u1, a1, t1}, "2026-08-15", "/w")
		if req2[0].(map[string]any)["content"] != req1[0].(map[string]any)["content"] {
			t.Fatalf("u1 bytes changed during tool loop")
		}
		req3, _ := inj.Transform([]any{u1, a1, t1, a2, u2}, "2026-08-15", "/w")
		wantU2 := RenderDateCwdReminder("2026-08-15", "/w") + "\n\nsecond"
		if req3[4].(map[string]any)["content"] != wantU2 {
			t.Fatalf("u2 = %q, want %q", req3[4].(map[string]any)["content"], wantU2)
		}
	})

	// B1 case 2: an already-sent user turn must never be retroactively rewritten on later rollover.
	t.Run("never rewrites already-sent user turn on rollover", func(t *testing.T) {
		inj := NewDateCwdReminderInjector()
		u1 := map[string]any{"role": "user", "content": "first", "timestamp": float64(1)}
		a1 := map[string]any{"role": "assistant", "content": "x", "timestamp": float64(2)}
		u2 := map[string]any{"role": "user", "content": "second", "timestamp": float64(3)}
		a2 := map[string]any{"role": "assistant", "content": "call", "timestamp": float64(4)}
		t1 := map[string]any{"role": "toolResult", "content": "res", "timestamp": float64(5)}

		inj.Transform([]any{u1}, "2026-08-14", "/w")
		inj.Transform([]any{u1, a1, u2}, "2026-08-14", "/w")
		req3, _ := inj.Transform([]any{u1, a1, u2, a2, t1}, "2026-08-15", "/w")
		if req3[2].(map[string]any)["content"] != "second" {
			t.Fatalf("already-sent u2 was retroactively rewritten: %q", req3[2].(map[string]any)["content"])
		}
	})

	// B1 case 3 + N4: post-compaction list starting with compactionSummary keeps the
	// synthetic reminder bytes identical across midnight rollover and once a later real
	// user turn arrives (moving only the new date onto the new user turn).
	t.Run("keeps synthetic compactionSummary reminder byte-stable across rollover and later user turn", func(t *testing.T) {
		inj := NewDateCwdReminderInjector()
		summary := map[string]any{"role": "compactionSummary", "summary": "earlier work", "timestamp": float64(10)}
		asst := map[string]any{"role": "assistant", "content": "call", "timestamp": float64(11)}
		toolRes := map[string]any{"role": "toolResult", "content": "res", "timestamp": float64(12)}
		userNext := map[string]any{"role": "user", "content": "next question", "timestamp": float64(13)}

		day1 := RenderDateCwdReminder("2026-08-14", "/w")
		day2 := RenderDateCwdReminder("2026-08-15", "/w")

		out1, changed1 := inj.Transform([]any{summary, asst}, "2026-08-14", "/w")
		if !changed1 || len(out1) != 3 {
			t.Fatalf("expected synthetic user message prepended, got len=%d changed=%v", len(out1), changed1)
		}
		if first := out1[0].(map[string]any); first["role"] != "user" || first["content"] != day1 {
			t.Fatalf("unexpected synthetic first message: %+v", first)
		}

		// Midnight rolls over mid-loop before any real user turn: head stays day1.
		out2, changed2 := inj.Transform([]any{summary, asst, toolRes}, "2026-08-15", "/w")
		if !changed2 || len(out2) != 4 {
			t.Fatalf("expected synthetic head preserved on rollover, got len=%d changed=%v", len(out2), changed2)
		}
		if gotHead := out2[0].(map[string]any)["content"]; gotHead != day1 {
			t.Fatalf("synthetic head changed at midnight: got %q, want %q", gotHead, day1)
		}

		// A real user turn arrives on day2: synthetic head still stays day1, and userNext gets day2.
		out3, changed3 := inj.Transform([]any{summary, asst, toolRes, userNext}, "2026-08-15", "/w")
		if !changed3 || len(out3) != 5 {
			t.Fatalf("expected synthetic head + userNext, got len=%d changed=%v", len(out3), changed3)
		}
		if gotHead := out3[0].(map[string]any)["content"]; gotHead != day1 {
			t.Fatalf("synthetic head changed when real user turn arrived: got %q, want %q", gotHead, day1)
		}
		if gotNext := out3[4].(map[string]any)["content"]; gotNext != day2+"\n\nnext question" {
			t.Fatalf("real user turn = %q, want %q", gotNext, day2+"\n\nnext question")
		}
	})
	// upstream: date-cwd-reminder.test.ts: "gives the newest of two identical same-millisecond turns the changed reminder"
	t.Run("distinguishes identical same-millisecond or untimestamped turns by ordinal", func(t *testing.T) {
		inj := NewDateCwdReminderInjector()
		u1 := map[string]any{"role": "user", "content": "continue"}
		a1 := map[string]any{"role": "assistant", "content": "x"}
		u2 := map[string]any{"role": "user", "content": "continue"}

		inj.Transform([]any{u1}, "2026-08-14", "/w")
		sameDay, _ := inj.Transform([]any{u1, a1, u2}, "2026-08-14", "/w")
		if sameDay[2].(map[string]any)["content"] != "continue" {
			t.Fatalf("second equal-text turn on same day must not get duplicate reminder: %q", sameDay[2].(map[string]any)["content"])
		}

		inj2 := NewDateCwdReminderInjector()
		t1 := map[string]any{"role": "user", "content": "ok", "timestamp": float64(1)}
		t2 := map[string]any{"role": "user", "content": "ok", "timestamp": float64(1)}
		inj2.Transform([]any{t1}, "2026-08-14", "/work")
		rolled, _ := inj2.Transform([]any{t1, a1, t2}, "2026-08-15", "/work")
		if rolled[0].(map[string]any)["content"] != RenderDateCwdReminder("2026-08-14", "/work")+"\n\nok" {
			t.Fatalf("first turn lost original reminder: %q", rolled[0].(map[string]any)["content"])
		}
		if rolled[2].(map[string]any)["content"] != RenderDateCwdReminder("2026-08-15", "/work")+"\n\nok" {
			t.Fatalf("second same-ms turn did not receive rolled reminder: %q", rolled[2].(map[string]any)["content"])
		}
	})

	// upstream: date-cwd-reminder.test.ts: "keeps the first reminder when the first turn's content is rewritten in place"
	t.Run("keeps first reminder when first turn content is rewritten in place", func(t *testing.T) {
		inj := NewDateCwdReminderInjector()
		first := map[string]any{"role": "user", "content": "token abc", "timestamp": float64(1)}
		inj.Transform([]any{first}, "2026-08-14", "/work")
		redacted := map[string]any{"role": "user", "content": "token [redacted]", "timestamp": float64(1)}
		next := map[string]any{"role": "user", "content": "second", "timestamp": float64(4)}
		out, _ := inj.Transform([]any{redacted, map[string]any{"role": "assistant", "content": "a", "timestamp": float64(2)}, next}, "2026-08-15", "/work")
		if out[0].(map[string]any)["content"] != RenderDateCwdReminder("2026-08-14", "/work")+"\n\ntoken [redacted]" {
			t.Fatalf("redacted first turn got wrong reminder: %q", out[0].(map[string]any)["content"])
		}
		if out[2].(map[string]any)["content"] != RenderDateCwdReminder("2026-08-15", "/work")+"\n\nsecond" {
			t.Fatalf("second turn got wrong reminder: %q", out[2].(map[string]any)["content"])
		}
	})

	// upstream: date-cwd-reminder.test.ts: "keeps the main conversation's bytes when another conversation's request runs between two of its own"
	t.Run("isolates interleaved conversations per root", func(t *testing.T) {
		inj := NewDateCwdReminderInjector()
		a1 := map[string]any{"role": "user", "content": "A1", "timestamp": float64(1)}
		b1 := map[string]any{"role": "user", "content": "B1", "timestamp": float64(10)}
		a2 := map[string]any{"role": "user", "content": "A2", "timestamp": float64(20)}
		asst := map[string]any{"role": "assistant", "content": "x", "timestamp": float64(2)}

		mainFirst, _ := inj.Transform([]any{a1}, "2026-08-14", "/a")
		advisor, _ := inj.Transform([]any{b1}, "2026-08-15", "/b")
		mainAfter, _ := inj.Transform([]any{a1, asst, a2}, "2026-08-15", "/a")

		if advisor[0].(map[string]any)["content"] != RenderDateCwdReminder("2026-08-15", "/b")+"\n\nB1" {
			t.Fatalf("advisor got wrong reminder: %q", advisor[0].(map[string]any)["content"])
		}
		if mainAfter[0].(map[string]any)["content"] != mainFirst[0].(map[string]any)["content"] {
			t.Fatalf("main A1 bytes changed after interleaved conversation: %q", mainAfter[0].(map[string]any)["content"])
		}
		if mainAfter[2].(map[string]any)["content"] != RenderDateCwdReminder("2026-08-15", "/a")+"\n\nA2" {
			t.Fatalf("main A2 did not receive updated reminder: %q", mainAfter[2].(map[string]any)["content"])
		}
	})

	// upstream: date-cwd-reminder.test.ts: "tells the model the date again after a branch back to before the turn that carried it"
	t.Run("re-sends updated date after branching back before carrier turn", func(t *testing.T) {
		inj := NewDateCwdReminderInjector()
		first := map[string]any{"role": "user", "content": "first", "timestamp": float64(1)}
		asst := map[string]any{"role": "assistant", "content": "a", "timestamp": float64(2)}
		carrier := map[string]any{"role": "user", "content": "second", "timestamp": float64(20)}
		replacement := map[string]any{"role": "user", "content": "instead", "timestamp": float64(30)}

		inj.Transform([]any{first}, "2026-08-14", "/work")
		rolled, _ := inj.Transform([]any{first, asst, carrier}, "2026-08-15", "/work")
		branched, _ := inj.Transform([]any{first, asst, replacement}, "2026-08-15", "/work")

		wantNew := RenderDateCwdReminder("2026-08-15", "/work")
		if branched[0].(map[string]any)["content"] != rolled[0].(map[string]any)["content"] {
			t.Fatalf("first turn changed on branch")
		}
		if branched[2].(map[string]any)["content"] != wantNew+"\n\ninstead" {
			t.Fatalf("replacement turn = %q, want %q", branched[2].(map[string]any)["content"], wantNew+"\n\ninstead")
		}
	})
}

// TestSkillsHiddenAndReaderGated verifies that skills require an active `read` tool
// and honor Hide / DisableModelInvocation.
// upstream: system-prompt-inventory.test.ts: "omits skill URL guidance when an override suppresses the skill URI reader"
func TestSkillsHiddenAndReaderGated(t *testing.T) {
	skills := []SkillEntry{
		{Name: "visible-skill", Description: "Visible skill", FilePath: "/skills/visible-skill/SKILL.md"},
		{Name: "secret-skill", Description: "Secret skill", FilePath: "/skills/secret-skill/SKILL.md", Hide: true},
	}

	withRead := BuildSystemPrompt(BuildOptions{
		ToolNames:            []string{"read"},
		ContextFiles:         []ContextFile{},
		Skills:               skills,
		SetActiveRepoContext: true,
	})
	if !strings.Contains(withRead, "- visible-skill: Visible skill") {
		t.Fatalf("expected visible skill when read is active:\n%s", withRead)
	}
	if strings.Contains(withRead, "secret-skill") {
		t.Fatalf("hidden skill must not appear in prompt")
	}

	withoutRead := BuildSystemPrompt(BuildOptions{
		ToolNames:            []string{"bash"},
		ContextFiles:         []ContextFile{},
		Skills:               skills,
		SetActiveRepoContext: true,
	})
	if strings.Contains(withoutRead, "<skills>") || strings.Contains(withoutRead, "visible-skill") {
		t.Fatalf("skills must not be listed when read tool is inactive")
	}
}

// TestPreviewSkillDescription pins omp's bounded preview truncation rules.
func TestPreviewSkillDescription(t *testing.T) {
	if got := PreviewSkillDescription("Short   desc\nwith  spaces."); got != "Short desc with spaces." {
		t.Fatalf("got %q", got)
	}
	longSentence := "This is a complete first sentence that is over forty characters long. And here is a second sentence that pushes past one hundred characters."
	if got := PreviewSkillDescription(longSentence); got != "This is a complete first sentence that is over forty characters long." {
		t.Fatalf("got %q", got)
	}
	longWord := strings.Repeat("word ", 30)
	got := PreviewSkillDescription(longWord)
	if !strings.HasSuffix(got, "…") || len([]rune(got)) > 100 {
		t.Fatalf("unexpected truncated preview %q (len=%d)", got, len([]rune(got)))
	}
}

// TestRegisterEndToEnd exercises Register through the PiG SDK wire protocol:
// - pulls live active tools from ctx.GetActiveTools() (not a constant)
// - consumes PiG's contextFiles and skills without duplicating them
// - preserves an explicit user --system-prompt override
// - injects omp's date/cwd reminder on the context event
func TestRegisterEndToEnd(t *testing.T) {
	workDir := t.TempDir()
	pigHome := t.TempDir()
	t.Setenv("PIG_HOME", pigHome)
	t.Setenv("HOME", t.TempDir())

	activeTools := []string{"read", "bash"}
	fixedTime := func() time.Time { return time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC) }
	fixedEnv := []EnvEntry{{Label: "OS", Value: "darwin 25.6.0"}, {Label: "Arch", Value: "arm64"}}

	ext := sdk.New("oh-my-pig")
	registerWithClock(ext, fixedTime, fixedEnv)

	host := startHost(t, ext, hostOptions{
		Cwd:           workDir,
		Model:         "claude-opus-4",
		ModelProvider: "anthropic",
		OnCall: func(method string, _ map[string]any) (map[string]any, string) {
			if method == "getActiveTools" {
				return map[string]any{"tools": activeTools}, ""
			}
			return map[string]any{}, ""
		},
	})

	// 1. Piglet system.md template -> filled with live session tools, contextFiles, skills, model.
	prompt1, ok := host.fireBeforeAgentStart(map[string]any{
		"prompt":       "hi",
		"systemPrompt": systemPromptTemplate,
		"systemPromptOptions": map[string]any{
			"customPrompt": systemPromptTemplate,
			"cwd":          workDir,
			"contextFiles": []any{
				map[string]any{"path": filepath.Join(workDir, "AGENTS.md"), "content": "Follow repo rules."},
			},
			"skills": []any{
				map[string]any{"name": "rex", "description": "Manage Rex sessions.", "filePath": "/skills/rex/SKILL.md"},
			},
			"sections": map[string]any{},
		},
	})
	if !ok {
		t.Fatalf("expected before_agent_start to return assembled systemPrompt")
	}
	if !strings.Contains(prompt1, "You are oh-my-pig's trusted coding assistant.") {
		t.Fatalf("missing role line in prompt")
	}
	if !strings.Contains(prompt1, "- Model: anthropic/claude-opus-4") {
		t.Fatalf("missing provider-qualified model in workstation block")
	}
	if !strings.Contains(prompt1, "# Tool Inventory\nread, bash") {
		t.Fatalf("expected active tools read and bash in inventory:\n%s", prompt1)
	}
	if strings.Contains(prompt1, "`edit`") {
		t.Fatalf("unregistered tool edit leaked into prompt")
	}
	if count := strings.Count(prompt1, "Follow repo rules."); count != 1 {
		t.Fatalf("expected context file injected exactly once, got %d", count)
	}
	if count := strings.Count(prompt1, "- rex: Manage Rex sessions."); count != 1 {
		t.Fatalf("expected skill injected exactly once, got %d", count)
	}

	// 2. Runtime tool list change is reflected immediately on the next turn.
	activeTools = []string{"read", "edit", "grep"}
	prompt2, ok := host.fireBeforeAgentStart(map[string]any{
		"prompt":       "next",
		"systemPrompt": systemPromptTemplate,
		"systemPromptOptions": map[string]any{
			"customPrompt": systemPromptTemplate,
			"cwd":          workDir,
			"sections":     map[string]any{},
		},
	})
	if !ok {
		t.Fatalf("expected second before_agent_start to return systemPrompt")
	}
	if !strings.Contains(prompt2, "File edits: MUST use `edit`.") {
		t.Fatalf("expected edit guidance after runtime tool activation")
	}
	if strings.Contains(prompt2, "`bash`") {
		t.Fatalf("removed tool `bash` still named in prompt")
	}

	// 3. Explicit user --system-prompt without slots is preserved untouched.
	if _, overridden := host.fireBeforeAgentStart(map[string]any{
		"prompt":       "custom",
		"systemPrompt": "Custom user prompt.",
		"systemPromptOptions": map[string]any{
			"customPrompt": "Custom user prompt.",
			"cwd":          workDir,
			"sections":     map[string]any{},
		},
	}); overridden {
		t.Fatalf("explicit user customPrompt without slots must not be overwritten")
	}

	// 4. Context event attaches omp's date/cwd reminder to the first user message.
	msgs, changed := host.fireContext([]any{
		map[string]any{"role": "user", "content": "hello", "timestamp": float64(100)},
	})
	if !changed {
		t.Fatalf("expected context handler to attach date/cwd reminder")
	}
	wantReminder := RenderDateCwdReminder("2026-11-24", workDir)
	gotContent, _ := msgs[0].(map[string]any)["content"].(string)
	if !strings.HasPrefix(gotContent, wantReminder+"\n\nhello") {
		t.Fatalf("context message = %q, want prefix %q", gotContent, wantReminder)
	}
}

// TestMultipleSessionsAndDoubleRegister verifies that:
//   - two sessions registered in one process isolate their per-session reminder state;
//   - Register called a second (and third) time replaces the lifecycle.OnEnd callback cleanly
//     against the shared package-level registry, so lifecycle.End for the FIRST session still
//     releases that session's state while leaving the second session intact;
//   - lifecycle.End called twice, for an unknown ID, or for "" is safe and idempotent.
func TestMultipleSessionsAndDoubleRegister(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Cleanup(func() {
		releaseSessionReminder("sess-A")
		releaseSessionReminder("sess-B")
		releaseSessionReminder("sess-C")
	})

	day := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	clock := func() time.Time { return day }
	fixedEnv := []EnvEntry{{Label: "OS", Value: "darwin 25.6.0"}, {Label: "Arch", Value: "arm64"}}

	extA := sdk.New("oh-my-pig")
	registerWithClock(extA, clock, fixedEnv)
	hostA := startHost(t, extA, hostOptions{
		Cwd:           dirA,
		SessionID:     "sess-A",
		Model:         "claude-opus-4",
		ModelProvider: "anthropic",
		OnCall: func(method string, _ map[string]any) (map[string]any, string) {
			if method == "getActiveTools" {
				return map[string]any{"tools": []string{"read", "bash"}}, ""
			}
			return map[string]any{}, ""
		},
	})

	extB := sdk.New("oh-my-pig")
	registerWithClock(extB, clock, fixedEnv)
	hostB := startHost(t, extB, hostOptions{
		Cwd:           dirB,
		SessionID:     "sess-B",
		Model:         "claude-sonnet-4",
		ModelProvider: "anthropic",
		OnCall: func(method string, _ map[string]any) (map[string]any, string) {
			if method == "getActiveTools" {
				return map[string]any{"tools": []string{"read", "edit"}}, ""
			}
			return map[string]any{}, ""
		},
	})

	// Both sessions run their first turn on day 1.
	msgA1 := map[string]any{"role": "user", "content": "task A", "timestamp": float64(1)}
	msgB1 := map[string]any{"role": "user", "content": "task B", "timestamp": float64(1)}
	outA1, changedA := hostA.fireContext([]any{msgA1})
	outB1, changedB := hostB.fireContext([]any{msgB1})
	if !changedA || !changedB {
		t.Fatalf("expected both sessions to attach initial reminders (A=%v B=%v)", changedA, changedB)
	}
	wantB1 := RenderDateCwdReminder("2026-11-24", dirB) + "\n\ntask B"
	if outB1[0].(map[string]any)["content"] != wantB1 {
		t.Fatalf("session B turn 1 = %q, want %q", outB1[0].(map[string]any)["content"], wantB1)
	}
	if gotA1 := outA1[0].(map[string]any)["content"]; gotA1 != RenderDateCwdReminder("2026-11-24", dirA)+"\n\ntask A" {
		t.Fatalf("session A turn 1 = %q", gotA1)
	}

	// Verify both sessions populated the package-level table before shutdown (not fallback).
	sessionRemindersMu.Lock()
	_, hasABefore := sessionReminders["sess-A"]
	_, hasBBefore := sessionReminders["sess-B"]
	sessionRemindersMu.Unlock()
	if !hasABefore || !hasBBefore {
		t.Fatalf("expected both sess-A (%v) and sess-B (%v) in package-level sessionReminders", hasABefore, hasBBefore)
	}

	// End the FIRST session (sess-A) after Register has already run a second time for extB:
	// via session_shutdown AND directly via lifecycle.End (twice), plus unknown/empty IDs.
	hostA.fire(sdk.EventSessionShutdown, map[string]any{"type": sdk.EventSessionShutdown})
	lifecycle.End("sess-A")
	lifecycle.End("sess-unknown")
	lifecycle.End("")
	hostA.stop()

	sessionRemindersMu.Lock()
	_, hasA := sessionReminders["sess-A"]
	_, hasB := sessionReminders["sess-B"]
	sessionRemindersMu.Unlock()
	if hasA {
		t.Fatalf("expected sess-A reminder state released after lifecycle.End")
	}
	if !hasB {
		t.Fatalf("expected sess-B reminder state preserved after sess-A ended")
	}

	// Advance clock to day 2: session B's turn 1 must keep its day-1 bytes and turn 2 gets day 2.
	day = time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	outB2, changedB2 := hostB.fireContext([]any{
		map[string]any{"role": "user", "content": "task B", "timestamp": float64(1)},
		map[string]any{"role": "assistant", "content": "ok", "timestamp": float64(2)},
		map[string]any{"role": "user", "content": "follow-up B", "timestamp": float64(3)},
	})
	if !changedB2 {
		t.Fatalf("expected session B turn 2 to attach reminders")
	}
	if outB2[0].(map[string]any)["content"] != wantB1 {
		t.Fatalf("session B turn 1 changed after session A shutdown: got %q, want %q", outB2[0].(map[string]any)["content"], wantB1)
	}
	wantB2 := RenderDateCwdReminder("2026-11-25", dirB) + "\n\nfollow-up B"
	if outB2[2].(map[string]any)["content"] != wantB2 {
		t.Fatalf("session B turn 2 = %q, want %q", outB2[2].(map[string]any)["content"], wantB2)
	}

	// Session B's before_agent_start prompt rendering still works after session A shutdown.
	promptB, ok := hostB.fireBeforeAgentStart(map[string]any{
		"prompt":       "continue",
		"systemPrompt": systemPromptTemplate,
		"systemPromptOptions": map[string]any{
			"customPrompt": systemPromptTemplate,
			"cwd":          dirB,
			"sections":     map[string]any{},
		},
	})
	if !ok || !strings.Contains(promptB, "# Tool Inventory\nread, edit") || !strings.Contains(promptB, "- Model: anthropic/claude-sonnet-4") {
		t.Fatalf("session B prompt broken after session A shutdown:\n%s", promptB)
	}

	// Calling Register twice more (simulating reload /new) succeeds cleanly and leaves session B intact.
	extC := sdk.New("oh-my-pig")
	Register(extC)
	Register(sdk.New("oh-my-pig"))
	hostC := startHost(t, extC, hostOptions{
		Cwd:       dirA,
		SessionID: "sess-C",
		OnCall: func(method string, _ map[string]any) (map[string]any, string) {
			if method == "getActiveTools" {
				return map[string]any{"tools": []string{"read"}}, ""
			}
			return map[string]any{}, ""
		},
	})
	promptC, ok := hostC.fireBeforeAgentStart(map[string]any{
		"prompt":       "fresh",
		"systemPrompt": systemPromptTemplate,
		"systemPromptOptions": map[string]any{
			"customPrompt": systemPromptTemplate,
			"cwd":          dirA,
			"sections":     map[string]any{},
		},
	})
	if !ok || !strings.Contains(promptC, "# Tool Inventory\nread") {
		t.Fatalf("fresh session C prompt failed:\n%s", promptC)
	}
	// Ending session B via lifecycle.End after Register ran again still releases sess-B.
	lifecycle.End("sess-B")
	sessionRemindersMu.Lock()
	_, stillHasB := sessionReminders["sess-B"]
	sessionRemindersMu.Unlock()
	if stillHasB {
		t.Fatalf("expected sess-B released by lifecycle.End after subsequent Register calls")
	}
}

// TestSubagentSessionHook verifies N2: when SubagentSession returns true, the
// before_agent_start hook renders omp's child prompt (Subagent: true, PersonalityNone,
// `# 2. Hand-off`, no `# Personality`, no `# 2. Verify`); when false it renders main.
func TestSubagentSessionHook(t *testing.T) {
	prev := SubagentSession
	t.Cleanup(func() { SubagentSession = prev })

	isChild := false
	SubagentSession = func(sdk.Context) bool { return isChild }

	workDir := t.TempDir()
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	ext := sdk.New("oh-my-pig")
	Register(ext)
	host := startHost(t, ext, hostOptions{
		Cwd:       workDir,
		SessionID: "sess-subagent-test",
		OnCall: func(method string, _ map[string]any) (map[string]any, string) {
			if method == "getActiveTools" {
				return map[string]any{"tools": []string{"read", "edit", "bash"}}, ""
			}
			return map[string]any{}, ""
		},
	})

	fire := func() string {
		t.Helper()
		p, ok := host.fireBeforeAgentStart(map[string]any{
			"prompt":       "go",
			"systemPrompt": systemPromptTemplate,
			"systemPromptOptions": map[string]any{
				"customPrompt": systemPromptTemplate,
				"cwd":          workDir,
				"sections":     map[string]any{},
			},
		})
		if !ok {
			t.Fatalf("expected before_agent_start to return systemPrompt")
		}
		return p
	}

	mainPrompt := fire()
	if !strings.Contains(mainPrompt, "# Personality") || !strings.Contains(mainPrompt, "# 2. Verify") {
		t.Fatalf("main session missing personality or # 2. Verify:\n%s", mainPrompt)
	}
	if strings.Contains(mainPrompt, "# 2. Hand-off") {
		t.Fatalf("main session must not contain # 2. Hand-off")
	}

	isChild = true
	childPrompt := fire()
	if strings.Contains(childPrompt, "# Personality") || strings.Contains(childPrompt, "# 2. Verify") {
		t.Fatalf("subagent session must omit # Personality and # 2. Verify:\n%s", childPrompt)
	}
	if !strings.Contains(childPrompt, "# 2. Hand-off") || !strings.Contains(childPrompt, "Changes complete → yield; verification is main agent's job.") {
		t.Fatalf("subagent session missing hand-off wording:\n%s", childPrompt)
	}
}

// TestIntentHookAndEnvOverride verifies N3: the Intent hook gates omp's intent line
// (and blank line after it per system-prompt.md:105), and PI_INTENT_PROMPT overrides
// custom or default guidance (upstream system-prompt.ts:1138-1140).
func TestIntentHookAndEnvOverride(t *testing.T) {
	prev := Intent
	t.Cleanup(func() { Intent = prev })

	enabled := false
	field := "i"
	guidance := ""
	Intent = func(sdk.Context) (bool, string, string) { return enabled, field, guidance }

	workDir := t.TempDir()
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	ext := sdk.New("oh-my-pig")
	Register(ext)
	host := startHost(t, ext, hostOptions{
		Cwd:       workDir,
		SessionID: "sess-intent-test",
		OnCall: func(method string, _ map[string]any) (map[string]any, string) {
			if method == "getActiveTools" {
				return map[string]any{"tools": []string{"read", "bash"}}, ""
			}
			return map[string]any{}, ""
		},
	})

	fire := func() string {
		t.Helper()
		p, ok := host.fireBeforeAgentStart(map[string]any{
			"prompt":       "go",
			"systemPrompt": systemPromptTemplate,
			"systemPromptOptions": map[string]any{
				"customPrompt": systemPromptTemplate,
				"cwd":          workDir,
				"sections":     map[string]any{},
			},
		})
		if !ok {
			t.Fatalf("expected before_agent_start to return systemPrompt")
		}
		return p
	}

	t.Setenv("PI_INTENT_PROMPT", "")
	off := fire()
	if strings.Contains(off, "Most tools take") {
		t.Fatalf("intent line rendered when Intent returned enabled=false:\n%s", off)
	}

	enabled = true
	onDefault := fire()
	wantDefault := "- Prefer relative paths.\n- Most tools take `i`: capitalized 2–6-word present-participle intent (e.g. \"Reading model role settings\").\n\n- XML tags inject system content;"
	if !strings.Contains(onDefault, wantDefault) {
		t.Fatalf("expected omp intent line + blank line:\n%s", onDefault)
	}

	guidance = "brief imperative verb phrase."
	onCustom := fire()
	if !strings.Contains(onCustom, "- Most tools take `i`: brief imperative verb phrase.\n\n- XML tags") {
		t.Fatalf("expected custom intent guidance:\n%s", onCustom)
	}

	t.Setenv("PI_INTENT_PROMPT", "env override intent guidance.")
	onEnv := fire()
	if !strings.Contains(onEnv, "- Most tools take `i`: env override intent guidance.\n\n- XML tags") {
		t.Fatalf("expected PI_INTENT_PROMPT override:\n%s", onEnv)
	}
}

// TestToolCommandsRunsBeforeActiveTools verifies N5: ToolCommands is called before
// GetActiveTools so tools restored to top-level during sync appear in # Tool Inventory
// and tool-gated sections for the same run.
func TestToolCommandsRunsBeforeActiveTools(t *testing.T) {
	prev := ToolCommands
	t.Cleanup(func() { ToolCommands = prev })

	activeTools := []string{"read", "bash"}
	order := []string{}
	ToolCommands = func(sdk.Context) ([]string, string) {
		order = append(order, "ToolCommands")
		// Simulate toolcmd unmounting lsp back to top-level active tools.
		activeTools = []string{"read", "bash", "lsp"}
		return nil, ""
	}

	workDir := t.TempDir()
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	ext := sdk.New("oh-my-pig")
	Register(ext)
	host := startHost(t, ext, hostOptions{
		Cwd:       workDir,
		SessionID: "sess-order-test",
		OnCall: func(method string, _ map[string]any) (map[string]any, string) {
			if method == "getActiveTools" {
				order = append(order, "getActiveTools")
				return map[string]any{"tools": activeTools}, ""
			}
			return map[string]any{}, ""
		},
	})

	p, ok := host.fireBeforeAgentStart(map[string]any{
		"prompt":       "go",
		"systemPrompt": systemPromptTemplate,
		"systemPromptOptions": map[string]any{
			"customPrompt": systemPromptTemplate,
			"cwd":          workDir,
			"sections":     map[string]any{},
		},
	})
	if !ok {
		t.Fatalf("expected before_agent_start to return systemPrompt")
	}
	if !reflect.DeepEqual(order, []string{"ToolCommands", "getActiveTools"}) {
		t.Fatalf("call order = %v, want [ToolCommands getActiveTools]", order)
	}
	if !strings.Contains(p, "# Tool Inventory\nread, bash, lsp") {
		t.Fatalf("tool restored by ToolCommands missing from inventory:\n%s", p)
	}
	if !strings.Contains(p, "MUST use `lsp` for definitions") {
		t.Fatalf("tool restored by ToolCommands missing from lsp gates:\n%s", p)
	}
}

// schemeHostCapturingHandler records the SchemeHost passed to PromptDoc and
// emits a distinct line when SettingsApproval / OutReplies are set.
type schemeHostCapturingHandler struct {
	scheme   string
	lastHost *urls.SchemeHost
	docFn    func(urls.SchemeHost) string
}

func (h schemeHostCapturingHandler) Scheme() string { return h.scheme }
func (h schemeHostCapturingHandler) Spec() urls.Spec {
	return urls.Spec{Backing: urls.BackingVirtual, Selectors: urls.SelectorsNone}
}
func (h schemeHostCapturingHandler) Resolve(urls.Context, urls.URL) (*urls.Resource, error) {
	return &urls.Resource{Content: "ok"}, nil
}
func (h schemeHostCapturingHandler) PromptDoc(host urls.SchemeHost) string {
	if h.lastHost != nil {
		*h.lastHost = host
	}
	return h.docFn(host)
}

// TestSchemeHostHooksCfgAndOut verifies that prompt.SettingsApproval and
// prompt.OutReplies feed urls.SchemeHost so cfg:// and out:// appear in
// # Internal URLs when active and are omitted by default (nil hooks).
// upstream: sdk.ts:4673-4674, cfg-protocol.ts:352-354, out-protocol.ts:77-79
func TestSchemeHostHooksCfgAndOut(t *testing.T) {
	prevSettings := SettingsApproval
	prevOut := OutReplies
	t.Cleanup(func() {
		SettingsApproval = prevSettings
		OutReplies = prevOut
	})

	var captured urls.SchemeHost
	cfgScheme := "testcfgprompt"
	outScheme := "testoutprompt"
	urls.Register(schemeHostCapturingHandler{
		scheme:   cfgScheme,
		lastHost: &captured,
		docFn: func(h urls.SchemeHost) string {
			if !h.SettingsApproval {
				return ""
			}
			return cfgScheme + "://: omp settings."
		},
	})
	urls.Register(schemeHostCapturingHandler{
		scheme: outScheme,
		docFn: func(h urls.SchemeHost) string {
			if !h.OutReplies {
				return ""
			}
			return outScheme + "://: write Markdown."
		},
	})
	t.Cleanup(func() {
		urls.Unregister(cfgScheme)
		urls.Unregister(outScheme)
	})

	workDir := t.TempDir()
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	ext := sdk.New("oh-my-pig")
	Register(ext)
	host := startHost(t, ext, hostOptions{
		Cwd:       workDir,
		SessionID: "sess-scheme-host-test",
		OnCall: func(method string, _ map[string]any) (map[string]any, string) {
			if method == "getActiveTools" {
				return map[string]any{"tools": []string{"read", "write"}}, ""
			}
			return map[string]any{}, ""
		},
	})

	fire := func() string {
		t.Helper()
		p, ok := host.fireBeforeAgentStart(map[string]any{
			"prompt":       "go",
			"systemPrompt": systemPromptTemplate,
			"systemPromptOptions": map[string]any{
				"customPrompt": systemPromptTemplate,
				"cwd":          workDir,
				"sections":     map[string]any{},
			},
		})
		if !ok {
			t.Fatalf("expected before_agent_start to return systemPrompt")
		}
		return p
	}

	SettingsApproval = nil
	OutReplies = nil
	defPrompt := fire()
	if captured.SettingsApproval || captured.OutReplies {
		t.Fatalf("nil hooks must default false, got %+v", captured)
	}
	if strings.Contains(defPrompt, cfgScheme+"://") || strings.Contains(defPrompt, outScheme+"://") {
		t.Fatalf("expected cfg/out omitted when hooks nil:\n%s", defPrompt)
	}

	SettingsApproval = func(sdk.Context) bool { return true }
	OutReplies = func(sdk.Context) bool { return true }
	onPrompt := fire()
	if !captured.SettingsApproval || !captured.OutReplies {
		t.Fatalf("expected SettingsApproval=true and OutReplies=true, got %+v", captured)
	}
	if !strings.Contains(onPrompt, "- "+cfgScheme+"://: omp settings.") {
		t.Fatalf("expected cfg doc in prompt:\n%s", onPrompt)
	}
	if !strings.Contains(onPrompt, "- "+outScheme+"://: write Markdown.") {
		t.Fatalf("expected out doc in prompt:\n%s", onPrompt)
	}
}

// TestTaskIRCClauseDefaultAndHook verifies that the `B asks A via write agent://<id>`
// clause renders by default when `task` is active (matching omp's default
// task.maxRecursionDepth=2) and obeys the TaskIRC hook when assigned.
// upstream: sdk.ts:4664, irc/messaging.ts:16-20
func TestTaskIRCClauseDefaultAndHook(t *testing.T) {
	prev := TaskIRC
	t.Cleanup(func() { TaskIRC = prev })

	workDir := t.TempDir()
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	ext := sdk.New("oh-my-pig")
	Register(ext)
	host := startHost(t, ext, hostOptions{
		Cwd:       workDir,
		SessionID: "sess-task-irc-test",
		OnCall: func(method string, _ map[string]any) (map[string]any, string) {
			if method == "getActiveTools" {
				return map[string]any{"tools": []string{"read", "task"}}, ""
			}
			return map[string]any{}, ""
		},
	})

	fire := func() string {
		t.Helper()
		p, ok := host.fireBeforeAgentStart(map[string]any{
			"prompt":       "go",
			"systemPrompt": systemPromptTemplate,
			"systemPromptOptions": map[string]any{
				"customPrompt": systemPromptTemplate,
				"cwd":          workDir,
				"sections":     map[string]any{},
			},
		})
		if !ok {
			t.Fatalf("expected before_agent_start to return systemPrompt")
		}
		return p
	}

	const clause = "Small missing detail? Run parallel; B asks A via `write agent://<id>` only if blocked."

	TaskIRC = nil
	defPrompt := fire()
	if !strings.Contains(defPrompt, clause) {
		t.Fatalf("expected IRC clause by default when task is active:\n%s", defPrompt)
	}

	TaskIRC = func(sdk.Context) bool { return false }
	offPrompt := fire()
	if strings.Contains(offPrompt, clause) {
		t.Fatalf("expected IRC clause omitted when TaskIRC returns false:\n%s", offPrompt)
	}
}

// TestToolInventoryPreservesActiveToolOrder verifies that # Tool Inventory keeps
// the exact order returned by ctx.GetActiveTools() without re-sorting.
func TestToolInventoryPreservesActiveToolOrder(t *testing.T) {
	ompOrder := []string{"read", "bash", "edit", "eval", "glob", "grep", "task", "wait", "todo", "web_search", "write"}
	workDir := t.TempDir()
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	ext := sdk.New("oh-my-pig")
	Register(ext)
	host := startHost(t, ext, hostOptions{
		Cwd:       workDir,
		SessionID: "sess-tool-order-test",
		OnCall: func(method string, _ map[string]any) (map[string]any, string) {
			if method == "getActiveTools" {
				return map[string]any{"tools": ompOrder}, ""
			}
			return map[string]any{}, ""
		},
	})

	p, ok := host.fireBeforeAgentStart(map[string]any{
		"prompt":       "go",
		"systemPrompt": systemPromptTemplate,
		"systemPromptOptions": map[string]any{
			"customPrompt": systemPromptTemplate,
			"cwd":          workDir,
			"sections":     map[string]any{},
		},
	})
	if !ok {
		t.Fatalf("expected before_agent_start to return systemPrompt")
	}
	want := "# Tool Inventory\n" + strings.Join(ompOrder, ", ")
	if !strings.Contains(p, want) {
		t.Fatalf("expected inventory in host active-tool order %q:\n%s", want, p)
	}
}

// TestStandardizePromptCwd verifies macOS /private stripping in both
// StandardizePromptCwd and RenderDateCwdReminder.
// upstream: packages/utils/src/dirs.ts:148-158
func TestStandardizePromptCwd(t *testing.T) {
	// Non-/private paths are always untouched.
	if got := StandardizePromptCwd("/usr/local/bin"); got != "/usr/local/bin" {
		t.Fatalf("got %q, want /usr/local/bin", got)
	}
	// Non-existent /private/nonexistent-... path cannot resolve both sides, so stays unchanged.
	missing := "/private/nonexistent-omp-prompt-test-dir-xyz"
	if got := StandardizePromptCwd(missing); got != missing {
		t.Fatalf("got %q, want %q", got, missing)
	}

	if runtime.GOOS == "darwin" {
		// /tmp is a symlink to /private/tmp on macOS; stripping /private resolves to the same realpath.
		sub := filepath.Join("/private/tmp", filepath.Base(t.TempDir()))
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(sub) })
		want := strings.TrimPrefix(sub, "/private")
		if got := StandardizePromptCwd(sub); got != want {
			t.Fatalf("StandardizePromptCwd(%q) = %q, want %q", sub, got, want)
		}
		rem := RenderDateCwdReminder("2026-11-24", sub)
		if !strings.Contains(rem, "'"+want+"'") {
			t.Fatalf("RenderDateCwdReminder did not strip /private: %s", rem)
		}
	}
}

// TestToolInventoryFiltersMountedAndSpecialActiveTools verifies that when mounted
// tools (lsp, debug, ast_edit) and special commands (resolve, reject, propose)
// remain in PiG's GetActiveTools() for direct-call fallback dispatch, # Tool
// Inventory shows only omp's advertised set via toolcmd.AdvertisedTools while
// # Tool Commands and mounted-tool policy gates remain intact.
func TestToolInventoryFiltersMountedAndSpecialActiveTools(t *testing.T) {
	prevToolCommands := ToolCommands
	toolcmd.ResetForTests()
	t.Cleanup(func() {
		ToolCommands = prevToolCommands
		toolcmd.ResetForTests()
	})

	toolcmd.Mountable("ast_edit", func(sdk.Context) bool { return true })
	toolcmd.Mountable("debug", func(sdk.Context) bool { return true })
	toolcmd.Mountable("lsp", func(sdk.Context) bool { return true })
	toolcmd.RegisterSpecialCommand("resolve", func(sdk.Context) bool { return true })
	toolcmd.RegisterSpecialCommand("reject", func(sdk.Context) bool { return true })
	toolcmd.RegisterSpecialCommand("propose", func(sdk.Context) bool { return true })
	ToolCommands = toolcmd.PromptDocs

	baseActive := []string{
		"read", "bash", "edit", "eval", "glob", "grep",
		"task", "wait", "todo", "web_search", "write",
	}
	allTools := make([]map[string]any, 0, len(baseActive)+6)
	for _, name := range baseActive {
		allTools = append(allTools, map[string]any{
			"name":       name,
			"exposure":   "direct",
			"parameters": map[string]any{"type": "object"},
		})
	}
	for _, entry := range []struct {
		name, desc string
	}{
		{"ast_edit", "Perform AST-aware code edits (SHOULD be preferred for codemods)"},
		{"debug", "Debug a running process with DAP (debugger adapter protocol)"},
		{"lsp", "Query LSP (language server) for diagnostics, hover info, and references"},
		{"resolve", "Apply pending AST edit"},
		{"reject", "Discard pending AST edit"},
		{"propose", "Submit plan proposal"},
	} {
		allTools = append(allTools, map[string]any{
			"name":        entry.name,
			"description": entry.desc,
			"exposure":    "codemode",
			"parameters":  map[string]any{"type": "object"},
		})
	}

	active := append([]string(nil), baseActive...)
	active = append(active, "lsp", "debug", "ast_edit", "resolve", "reject", "propose")

	workDir := t.TempDir()
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	ext := sdk.New("oh-my-pig")
	toolcmd.Register(ext)
	Register(ext)
	host := startHost(t, ext, hostOptions{
		Cwd:       workDir,
		SessionID: "sess-advertised-filter",
		OnCall: func(method string, params map[string]any) (map[string]any, string) {
			switch method {
			case "getAllTools":
				return map[string]any{"tools": allTools}, ""
			case "getActiveTools":
				return map[string]any{"tools": active}, ""
			case "setActiveTools":
				if raw, ok := params["tools"].([]any); ok {
					next := make([]string, 0, len(raw))
					for _, v := range raw {
						if s, ok := v.(string); ok {
							next = append(next, s)
						}
					}
					active = next
				}
				return map[string]any{}, ""
			default:
				return map[string]any{}, ""
			}
		},
	})

	// Trigger session_start so toolcmd mounts lsp, debug, ast_edit and special commands.
	host.fireAll(sdk.EventSessionStart, map[string]any{})

	p, ok := host.fireBeforeAgentStart(map[string]any{
		"prompt":       "go",
		"systemPrompt": systemPromptTemplate,
		"systemPromptOptions": map[string]any{
			"customPrompt": systemPromptTemplate,
			"cwd":          workDir,
			"sections":     map[string]any{},
		},
	})
	if !ok {
		t.Fatalf("expected before_agent_start to return systemPrompt")
	}

	wantInv := "# Tool Inventory\nread, bash, edit, eval, glob, grep, task, wait, todo, web_search, write\n"
	if !strings.Contains(p, wantInv) {
		t.Fatalf("expected # Tool Inventory to contain only omp's default advertised set:\ngot prompt:\n%s", p)
	}
	// Check the Tool Inventory and Tool Commands blocks specifically (prose contains the verb "resolve").
	invStart := strings.Index(p, "# Tool Inventory\n")
	policyStart := strings.Index(p, "⁂ Tool Policy")
	if invStart < 0 || policyStart <= invStart {
		t.Fatalf("missing # Tool Inventory / ⁂ Tool Policy markers:\n%s", p)
	}
	toolSection := p[invStart:policyStart]
	for _, hidden := range []string{"resolve", "reject", "propose"} {
		if strings.Contains(toolSection, hidden) {
			t.Fatalf("special command %q leaked into tool section:\n%s", hidden, toolSection)
		}
	}
	if !strings.Contains(toolSection, "# Tool Commands\n") {
		t.Fatalf("expected # Tool Commands section to remain present:\n%s", toolSection)
	}
	for _, mounted := range []string{"- `ast_edit`", "- `debug`", "- `lsp`"} {
		if !strings.Contains(toolSection, mounted) {
			t.Fatalf("expected mounted command %s in # Tool Commands:\n%s", mounted, toolSection)
		}
	}
	if !strings.Contains(p, "MUST use `lsp` for definitions") {
		t.Fatalf("expected mounted lsp to still satisfy lsp policy gate:\n%s", p)
	}
}

// TestEvalPreludeGuidanceForSession verifies that RenderTemplate / before_agent_start
// invokes eval.Prelude.GuidanceText(ctx), so session-aware GuidanceFor receives
// each session's sdk.Context and emits distinct per-session guidance.
func TestEvalPreludeGuidanceForSession(t *testing.T) {
	eval.ResetPreludesForTests()
	t.Cleanup(eval.ResetPreludesForTests)
	eval.RegisterPrelude(eval.Prelude{
		Name: "test_session_guidance",
		Guidance: func() string {
			return "FALLBACK-GUIDANCE"
		},
		GuidanceFor: func(ctx sdk.Context) string {
			id, _ := ctx.GetSessionID()
			return "SESSION-GUIDANCE:" + id
		},
	})
	workDir := t.TempDir()
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	fireFor := func(sid string) string {
		t.Helper()
		ext := sdk.New("oh-my-pig")
		Register(ext)
		host := startHost(t, ext, hostOptions{
			Cwd:       workDir,
			SessionID: sid,
			OnCall: func(method string, _ map[string]any) (map[string]any, string) {
				if method == "getActiveTools" {
					return map[string]any{"tools": []string{"read", "eval"}}, ""
				}
				return map[string]any{}, ""
			},
		})
		p, ok := host.fireBeforeAgentStart(map[string]any{
			"prompt":       "go",
			"systemPrompt": systemPromptTemplate,
			"systemPromptOptions": map[string]any{
				"customPrompt": systemPromptTemplate,
				"cwd":          workDir,
				"sections":     map[string]any{},
			},
		})
		if !ok {
			t.Fatalf("expected before_agent_start to return systemPrompt for %s", sid)
		}
		return p
	}

	pA := fireFor("sess-guidance-A")
	pB := fireFor("sess-guidance-B")

	if !strings.Contains(pA, "SESSION-GUIDANCE:sess-guidance-A") || strings.Contains(pA, "FALLBACK-GUIDANCE") {
		t.Fatalf("session A did not receive GuidanceFor(ctxA):\n%s", pA)
	}
	if !strings.Contains(pB, "SESSION-GUIDANCE:sess-guidance-B") || strings.Contains(pB, "sess-guidance-A") {
		t.Fatalf("session B did not receive isolated GuidanceFor(ctxB):\n%s", pB)
	}
}
