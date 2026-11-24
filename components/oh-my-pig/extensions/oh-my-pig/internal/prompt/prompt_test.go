package prompt

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
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

// TestGoldenAssembledPrompt pins the complete assembled system prompt for a fixed
// environment, model, tool list, context file, and skill list.
func TestGoldenAssembledPrompt(t *testing.T) {
	got := BuildSystemPrompt(BuildOptions{
		Cwd:        "/work/repo",
		ToolNames:  []string{"read", "edit", "write", "grep", "glob", "bash", "todo", "ask"},
		ToolLabels: map[string]string{"read": "Read", "edit": "Edit", "write": "Write", "grep": "Grep", "glob": "Glob", "bash": "Bash", "todo": "Todo", "ask": "Ask"},
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
		t.Fatalf("assembled prompt mismatch:\n--- GOT ---\n%s\n--- WANT ---\n%s", got, want)
	}
}

// TestUnregisteredToolsNotNamed proves that tools not present in the session's active
// tool list are never named anywhere in the rendered system prompt.
func TestUnregisteredToolsNotNamed(t *testing.T) {
	got := BuildSystemPrompt(BuildOptions{
		Cwd:                  "/work/repo",
		ToolNames:            []string{"read"},
		ContextFiles:         []ContextFile{{Path: "/work/repo/AGENTS.md", Content: "Project rules.", Depth: 0, HasDepth: true}},
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
		"`ast_grep`",
		"`ast_edit`",
		"`think`",
		"# Delegation",
		"# AST",
		"§ Scratchpad",
		"Ask before destructive commands",
	}
	for _, needle := range forbidden {
		if strings.Contains(got, needle) {
			t.Errorf("unregistered tool reference %q leaked into prompt:\n%s", needle, got)
		}
	}
	if !strings.Contains(got, "- `read`") {
		t.Errorf("registered tool `read` missing from Tool Inventory")
	}
	if !strings.Contains(got, "File/directory reads: `read`") {
		t.Errorf("registered tool `read` missing from Specialized Tools")
	}
	if !strings.Contains(got, "Use `read` ranges, not whole files.") {
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

// upstream: date-cwd-reminder.test.ts (all 5 core DateCwdReminderInjector behaviors)
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
	t.Run("leaves messages without user message untouched", func(t *testing.T) {
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
	if !strings.Contains(withRead, "- visible-skill (`/skills/visible-skill/SKILL.md`): Visible skill") {
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
	if !strings.Contains(prompt1, "- `read`") || !strings.Contains(prompt1, "- `bash`") {
		t.Fatalf("expected active tools read and bash in inventory")
	}
	if strings.Contains(prompt1, "`edit`") || strings.Contains(prompt1, "`grep`") {
		t.Fatalf("unregistered tools edit/grep leaked into prompt")
	}
	if count := strings.Count(prompt1, "Follow repo rules."); count != 1 {
		t.Fatalf("expected context file injected exactly once, got %d", count)
	}
	if count := strings.Count(prompt1, "- rex ("); count != 1 {
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
	if !strings.Contains(prompt2, "Surgical edits: `edit`.") || !strings.Contains(prompt2, "NEVER use `sed`|`perl`|`python` to issue individual edits") {
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
