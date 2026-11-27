package memoryskills

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/config"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/events"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/memory"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/paths"
)

type stubMemoryProvider struct {
	mu           sync.Mutex
	name         string
	prefetchText string
	prefetchHit  int
	lastQuery    string
	writes       int
}

func (s *stubMemoryProvider) Name() string { return s.name }

func (s *stubMemoryProvider) Prefetch(_ context.Context, _ string, query string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prefetchHit++
	s.lastQuery = query
	return s.prefetchText, nil
}

func (s *stubMemoryProvider) OnPreCompress(_ context.Context, _ string) error { return nil }

func (s *stubMemoryProvider) OnMemoryWrite(_ context.Context, _ memory.Target, _, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes++
	return nil
}

func (s *stubMemoryProvider) OnSessionEnd(_ context.Context, _ string) error { return nil }

func makeTestProfile(t *testing.T) (paths.ProfilePaths, *config.Config, *memory.Store) {
	t.Helper()
	root := t.TempDir()
	p := paths.ProfilePaths{
		RootHome:    root,
		ProfileName: "test-profile",
		Home:        root,
		ConfigFile:  filepath.Join(root, "config.yaml"),
		EnvFile:     filepath.Join(root, ".env"),
		MemoriesDir: filepath.Join(root, "memories"),
		SkillsDir:   filepath.Join(root, "skills"),
		SoulFile:    filepath.Join(root, "SOUL.md"),
	}
	cfg := config.Default()
	store := memory.NewStore(p.MemoriesDir, cfg.MemoryLimit, cfg.UserLimit)
	return p, cfg, store
}

func decodeToolJSON(t *testing.T, res sdk.ToolResult) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(res.Content), &m); err != nil {
		t.Fatalf("failed to decode tool JSON %q: %v", res.Content, err)
	}
	return m
}

func TestRegisterSmoke(t *testing.T) {
	p, cfg, store := makeTestProfile(t)
	ext := sdk.New("test-memoryskills")
	Register(ext, p, cfg, store)
	// Calling with nil ext should be a safe no-op.
	Register(nil, p, cfg, store)
}

func TestMemoryToolAtomicBatchAndCompat(t *testing.T) {
	_, _, store := makeTestProfile(t)
	prov := &stubMemoryProvider{name: "omh"}
	store.RegisterProvider(prov)

	var ctx sdk.Context

	// 1. Atomic batch add + patch on default target ("memory").
	res := handleMemoryTool(ctx, store, map[string]any{
		"operations": []any{
			map[string]any{"action": "add", "content": "Go 1.26 workspace uses pure stdlib"},
			map[string]any{"action": "add", "content": "Temporary note to remove"},
		},
	})
	if res.IsError {
		t.Fatalf("unexpected batch add error: %s", res.Content)
	}
	body := decodeToolJSON(t, res)
	if ok, _ := body["success"].(bool); !ok {
		t.Fatalf("expected success=true, got %+v", body)
	}

	// 2. Atomic batch remove + add + patch.
	res = handleMemoryTool(ctx, store, map[string]any{
		"target": "memory",
		"operations": []any{
			map[string]any{"action": "remove", "old_text": "Temporary note"},
			map[string]any{"action": "patch", "old_text": "pure stdlib", "content": "pure stdlib only"},
		},
	})
	if res.IsError {
		t.Fatalf("unexpected batch patch/remove error: %s", res.Content)
	}
	entries, _, _, err := store.ReadLive(memory.TargetMemory)
	if err != nil {
		t.Fatalf("ReadLive failed: %v", err)
	}
	if len(entries) != 1 || entries[0] != "Go 1.26 workspace uses pure stdlib only" {
		t.Fatalf("unexpected entries after batch: %#v", entries)
	}

	// 3. Flat legacy single-op compatibility on target="user" with new_text alias.
	res = handleMemoryTool(ctx, store, map[string]any{
		"target":   "user",
		"action":   "add",
		"new_text": "User prefers terse technical answers",
	})
	if res.IsError {
		t.Fatalf("unexpected flat compat add error: %s", res.Content)
	}
	userEntries, _, _, err := store.ReadLive(memory.TargetUser)
	if err != nil || len(userEntries) != 1 || userEntries[0] != "User prefers terse technical answers" {
		t.Fatalf("unexpected user entries: %#v (err=%v)", userEntries, err)
	}

	// 4. Rejects "replace" in both flat and batch forms with Hermes explicit error message.
	for _, params := range []map[string]any{
		{"target": "memory", "action": "replace", "old_text": "Go", "content": "Rust"},
		{"target": "memory", "operations": []any{map[string]any{"action": "replace", "old_text": "Go", "content": "Rust"}}},
	} {
		r := handleMemoryTool(ctx, store, params)
		if !r.IsError {
			t.Fatalf("expected replace to fail, got: %s", r.Content)
		}
		if !strings.Contains(r.Content, "'replace' is not available; use 'patch'") {
			t.Fatalf("expected Hermes replace error message, got: %s", r.Content)
		}
	}

	// 5. Rejects invalid target and cross-target batch operations.
	r := handleMemoryTool(ctx, store, map[string]any{"target": "invalid", "action": "add", "content": "x"})
	if !r.IsError {
		t.Fatalf("expected invalid target error")
	}
	r = handleMemoryTool(ctx, store, map[string]any{
		"target": "memory",
		"operations": []any{
			map[string]any{"action": "add", "target": "user", "content": "x"},
		},
	})
	if !r.IsError || !strings.Contains(r.Content, "different target") {
		t.Fatalf("expected cross-target error, got: %s", r.Content)
	}

	if prov.writes == 0 {
		t.Fatalf("expected provider OnMemoryWrite to be notified")
	}
}

func TestSkillsPrecedenceListViewManage(t *testing.T) {
	p, cfg, _ := makeTestProfile(t)
	extraDir := t.TempDir()
	cfg.Raw["skills.external_dirs"] = extraDir

	projectDir := t.TempDir()
	// Mark projectDir as a git root so .hermes/skills and .agents/skills resolve cleanly.
	if err := os.MkdirAll(filepath.Join(projectDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	writeSkill := func(root, relDir, content string) {
		t.Helper()
		dir := filepath.Join(root, relDir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Same skill "deploy" exists in extra, local, and project -> project must win.
	writeSkill(extraDir, "devops/deploy", "---\nname: deploy\ndescription: Extra deploy\ncategory: devops\ntags: [extra]\n---\nExtra body")
	writeSkill(p.SkillsDir, "devops/deploy", "---\nname: deploy\ndescription: Local deploy\ncategory: devops\ntags: [local]\n---\nLocal body")
	writeSkill(filepath.Join(projectDir, ".hermes", "skills"), "devops/deploy", "---\nname: deploy\ndescription: Project deploy\ncategory: devops\ntags: [proj, release]\nmetadata:\n  owner: platform\n---\nProject body")

	// Skill only in .agents/skills (project tier) and skill only in extra.
	writeSkill(filepath.Join(projectDir, ".agents", "skills"), "testing/fuzz", "---\nname: fuzz\ndescription: Fuzz Go packages\ncategory: testing\n---\nFuzz body")
	writeSkill(extraDir, "misc/extra-only", "---\nname: extra-only\ndescription: Extra helper\ncategory: misc\n---\nExtra helper body")

	// Platform-incompatible skill should be filtered out.
	writeSkill(p.SkillsDir, "misc/plan9-only", "---\nname: plan9-only\ndescription: Plan 9 only\nplatforms: [plan9]\n---\nPlan 9 body")

	discovered := DiscoverSkills(projectDir, p, cfg)
	byName := make(map[string]SkillMeta)
	for _, s := range discovered {
		byName[s.Name] = s
	}
	if got, ok := byName["deploy"]; !ok || got.Source != "project" || got.Description != "Project deploy" {
		t.Fatalf("expected project deploy to shadow local/extra, got: %+v", got)
	}
	if _, ok := byName["fuzz"]; !ok {
		t.Fatalf("expected .agents/skills fuzz to be discovered")
	}
	if got, ok := byName["extra-only"]; !ok || got.Source != "extra" {
		t.Fatalf("expected extra-only from extra dir, got: %+v", got)
	}
	if _, ok := byName["plan9-only"]; ok {
		t.Fatalf("expected platform-filtered skill plan9-only to be excluded")
	}

	// Test skill_manage lifecycle in local profile skills dir.
	var ctx sdk.Context
	// Create skill.
	createRes := handleSkillManage(ctx, p, cfg, map[string]any{
		"action":   "create",
		"name":     "go-review",
		"category": "code",
		"content":  "---\nname: go-review\ndescription: Review Go code for race conditions.\ntags: [go, review]\n---\n# Go Review\nRun go test -race.",
	})
	if createRes.IsError {
		t.Fatalf("create failed: %s", createRes.Content)
	}

	// Invalid skill name rejected.
	badName := handleSkillManage(ctx, p, cfg, map[string]any{
		"action":  "create",
		"name":    "Bad_Name/WithSlash",
		"content": "---\nname: bad\ndescription: bad\n---\nbody",
	})
	if !badName.IsError {
		t.Fatalf("expected invalid skill name error")
	}

	// Write supporting file + path traversal protection.
	wfRes := handleSkillManage(ctx, p, cfg, map[string]any{
		"action":    "write_file",
		"name":      "go-review",
		"file_path": "references/checklist.md",
		"content":   "- Check goroutine leaks\n- Check locks\n",
	})
	if wfRes.IsError {
		t.Fatalf("write_file failed: %s", wfRes.Content)
	}

	travRes := handleSkillManage(ctx, p, cfg, map[string]any{
		"action":    "write_file",
		"name":      "go-review",
		"file_path": "references/../../escape.txt",
		"content":   "nope",
	})
	if !travRes.IsError || !strings.Contains(travRes.Content, "Path traversal") {
		t.Fatalf("expected path traversal rejection, got: %s", travRes.Content)
	}

	badSubdir := handleSkillManage(ctx, p, cfg, map[string]any{
		"action":    "write_file",
		"name":      "go-review",
		"file_path": "outside/foo.txt",
		"content":   "nope",
	})
	if !badSubdir.IsError {
		t.Fatalf("expected unsupported subdir rejection, got: %s", badSubdir.Content)
	}

	// Patch SKILL.md.
	patchRes := handleSkillManage(ctx, p, cfg, map[string]any{
		"action":     "patch",
		"name":       "go-review",
		"old_string": "Run go test -race.",
		"new_string": "Run go test -race ./... and go vet.",
	})
	if patchRes.IsError {
		t.Fatalf("patch failed: %s", patchRes.Content)
	}

	// View Tier 2 (SKILL.md + linked_files) and Tier 3 (references/checklist.md).
	viewRes := handleSkillView(ctx, p, cfg, map[string]any{"name": "go-review"})
	if viewRes.IsError {
		t.Fatalf("skill_view Tier 2 failed: %s", viewRes.Content)
	}
	viewBody := decodeToolJSON(t, viewRes)
	if !strings.Contains(viewBody["content"].(string), "go vet") {
		t.Fatalf("expected patched content in skill_view, got: %s", viewBody["content"])
	}
	if viewBody["linked_files"] == nil {
		t.Fatalf("expected linked_files in skill_view Tier 2 response")
	}

	viewRef := handleSkillView(ctx, p, cfg, map[string]any{
		"name":      "go-review",
		"file_path": "references/checklist.md",
	})
	if viewRef.IsError || !strings.Contains(viewRef.Content, "goroutine leaks") {
		t.Fatalf("skill_view Tier 3 failed: %s", viewRef.Content)
	}

	// Tier 3 traversal guard.
	viewTrav := handleSkillView(ctx, p, cfg, map[string]any{
		"name":      "go-review",
		"file_path": "../../../etc/passwd",
	})
	if !viewTrav.IsError {
		t.Fatalf("expected skill_view path traversal error")
	}

	// skills_list with category and query filters.
	listRes := handleSkillsList(ctx, p, cfg, map[string]any{"category": "code", "query": "race"})
	if listRes.IsError {
		t.Fatalf("skills_list failed: %s", listRes.Content)
	}
	listBody := decodeToolJSON(t, listRes)
	if int(listBody["count"].(float64)) != 1 {
		t.Fatalf("expected 1 filtered skill, got: %+v", listBody)
	}

	// remove_file + edit + delete.
	rmFile := handleSkillManage(ctx, p, cfg, map[string]any{
		"action":    "remove_file",
		"name":      "go-review",
		"file_path": "references/checklist.md",
	})
	if rmFile.IsError {
		t.Fatalf("remove_file failed: %s", rmFile.Content)
	}
	editRes := handleSkillManage(ctx, p, cfg, map[string]any{
		"action":  "edit",
		"name":    "go-review",
		"content": "---\nname: go-review\ndescription: Updated review skill\n---\nRewritten body.",
	})
	if editRes.IsError {
		t.Fatalf("edit failed: %s", editRes.Content)
	}
	delRes := handleSkillManage(ctx, p, cfg, map[string]any{
		"action": "delete",
		"name":   "go-review",
	})
	if delRes.IsError {
		t.Fatalf("delete failed: %s", delRes.Content)
	}
}

func TestProjectContextPriorityOrder(t *testing.T) {
	// Priority: .hermes.md/HERMES.md > AGENTS.override.md/AGENTS.md > CLAUDE.md > .cursorrules
	t.Run("hermes_md_wins_over_all", func(t *testing.T) {
		dir := t.TempDir()
		_ = os.WriteFile(filepath.Join(dir, ".hermes.md"), []byte("---\ntitle: ignore\n---\nHermes rules"), 0o644)
		_ = os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("Agents rules"), 0o644)
		_ = os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("Claude rules"), 0o644)
		_ = os.WriteFile(filepath.Join(dir, ".cursorrules"), []byte("Cursor rules"), 0o644)

		files := DiscoverProjectContextFiles(dir)
		if len(files) != 1 || files[0].Kind != "hermes_md" || files[0].Content != "Hermes rules" {
			t.Fatalf("expected .hermes.md to win with frontmatter stripped, got: %+v", files)
		}
	})

	t.Run("agents_override_wins_and_shadows_claude", func(t *testing.T) {
		dir := t.TempDir()
		_ = os.WriteFile(filepath.Join(dir, "AGENTS.override.md"), []byte("Override rules"), 0o644)
		_ = os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("Base agents rules"), 0o644)
		_ = os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("Claude rules"), 0o644)

		files := DiscoverProjectContextFiles(dir)
		if len(files) != 1 || files[0].Kind != "agents_md" || files[0].Content != "Override rules" {
			t.Fatalf("expected AGENTS.override.md to win, got: %+v", files)
		}
	})

	t.Run("claude_wins_over_cursorrules", func(t *testing.T) {
		dir := t.TempDir()
		_ = os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("Claude rules"), 0o644)
		_ = os.WriteFile(filepath.Join(dir, ".cursorrules"), []byte("Cursor rules"), 0o644)

		files := DiscoverProjectContextFiles(dir)
		if len(files) != 1 || files[0].Kind != "claude_md" || files[0].Content != "Claude rules" {
			t.Fatalf("expected CLAUDE.md to win, got: %+v", files)
		}
	})

	t.Run("cursorrules_fallback_with_mdc", func(t *testing.T) {
		dir := t.TempDir()
		_ = os.WriteFile(filepath.Join(dir, ".cursorrules"), []byte("Root cursor"), 0o644)
		_ = os.MkdirAll(filepath.Join(dir, ".cursor", "rules"), 0o755)
		_ = os.WriteFile(filepath.Join(dir, ".cursor", "rules", "01-style.mdc"), []byte("Style rule"), 0o644)

		files := DiscoverProjectContextFiles(dir)
		if len(files) != 2 || files[0].Kind != "cursorrules" || files[1].Label != ".cursor/rules/01-style.mdc" {
			t.Fatalf("expected .cursorrules + .mdc files, got: %+v", files)
		}
	})
}

func TestPromptSectionsInPlacePreservesExistingAndCallsPrefetch(t *testing.T) {
	p, cfg, store := makeTestProfile(t)
	if err := os.WriteFile(p.SoulFile, []byte("Custom Hermes Soul"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Apply(context.Background(), memory.TargetMemory, []memory.Operation{
		{Action: "add", Content: "Frozen memory fact"},
	}); err != nil {
		t.Fatal(err)
	}
	// Freeze snapshot before another live write to verify snapshot stays frozen.
	snap := store.LoadSnapshot()
	if _, err := store.Apply(context.Background(), memory.TargetMemory, []memory.Operation{
		{Action: "add", Content: "Mid-session write should not alter frozen snapshot"},
	}); err != nil {
		t.Fatal(err)
	}

	prov := &stubMemoryProvider{name: "omh", prefetchText: "Recalled vector memory"}
	store.RegisterProvider(prov)

	// Pre-populate an unrelated Phase 2 section to ensure it is NOT wiped.
	sections := &sdk.SystemPromptSections{
		{Name: "omh_orchestrator", Value: "Phase 2 section must survive"},
	}
	data := map[string]any{
		"prompt": "how do I deploy?",
		"systemPromptOptions": map[string]any{
			"sections": sections,
		},
	}

	state := &promptSessionState{frozenSnap: &snap}
	var ctx sdk.Context
	applyPromptSections(ctx, data, p, cfg, store, state)

	byName := make(map[string]string)
	for _, s := range *sections {
		byName[s.Name] = s.Value
	}

	// Verify Phase 2 section is preserved untouched.
	if byName["omh_orchestrator"] != "Phase 2 section must survive" {
		t.Fatalf("existing section omh_orchestrator was wiped or modified: %q", byName["omh_orchestrator"])
	}

	// Verify all 6 required Hermes sections exist.
	for _, required := range []string{
		events.SectionIdentity,
		events.SectionTools,
		events.SectionContext,
		events.SectionSkills,
		events.SectionMemory,
		events.SectionEnvironment,
	} {
		if _, ok := byName[required]; !ok {
			t.Fatalf("missing required prompt section %q", required)
		}
	}

	// Verify SOUL.md loaded in identity slot.
	if !strings.Contains(byName[events.SectionIdentity], "Custom Hermes Soul") {
		t.Fatalf("expected SOUL.md in %s, got: %q", events.SectionIdentity, byName[events.SectionIdentity])
	}

	// Verify frozen memory snapshot + external provider prefetch.
	memSec := byName[events.SectionMemory]
	if !strings.Contains(memSec, "Frozen memory fact") {
		t.Fatalf("expected frozen memory fact in %s", events.SectionMemory)
	}
	if strings.Contains(memSec, "Mid-session write should not alter frozen snapshot") {
		t.Fatalf("mid-session write leaked into frozen snapshot")
	}
	if !strings.Contains(memSec, "Recalled vector memory") || prov.prefetchHit != 1 || prov.lastQuery != "how do I deploy?" {
		t.Fatalf("expected provider Prefetch to be invoked and included, hits=%d query=%q sec=%q", prov.prefetchHit, prov.lastQuery, memSec)
	}
}

func TestMemoryAndSkillsConcurrentRaceSafety(t *testing.T) {
	p, cfg, store := makeTestProfile(t)
	var ctx sdk.Context
	var wg sync.WaitGroup

	for i := range 8 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_ = handleMemoryTool(ctx, store, map[string]any{
				"target": "memory",
				"operations": []any{
					map[string]any{"action": "add", "content": "Concurrent fact"},
				},
			})
			_ = handleSkillsList(ctx, p, cfg, nil)
		}(i)
	}
	wg.Wait()
}

func TestSkillManageDeleteGuardsRootAndSymlink(t *testing.T) {
	p, cfg, _ := makeTestProfile(t)
	if err := os.MkdirAll(p.SkillsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// 1. SKILL.md placed directly inside the skills root must not allow deleting the root.
	rootSkillMD := filepath.Join(p.SkillsDir, "SKILL.md")
	if err := os.WriteFile(rootSkillMD, []byte("---\nname: root-skill\ndescription: Skill at root\n---\nBody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var ctx sdk.Context
	delRoot := handleSkillManage(ctx, p, cfg, map[string]any{
		"action": "delete",
		"name":   "root-skill",
	})
	if !delRoot.IsError || !strings.Contains(delRoot.Content, "skills root") {
		t.Fatalf("expected skills root delete refusal, got: %+v", delRoot)
	}
	if _, err := os.Stat(rootSkillMD); err != nil {
		t.Fatalf("skills root was unexpectedly removed: %v", err)
	}

	// 2. Symlinked skill directory must be refused.
	realTarget := filepath.Join(t.TempDir(), "real-skill")
	if err := os.MkdirAll(realTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realTarget, "SKILL.md"), []byte("---\nname: linked-skill\ndescription: Symlinked skill\n---\nBody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(p.SkillsDir, "linked-skill")
	if err := os.Symlink(realTarget, linkPath); err != nil {
		t.Skipf("symlinks unsupported in test environment: %v", err)
	}
	if err := validateDeletableSkillDir(linkPath, "", p, cfg); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink delete refusal, got: %v", err)
	}
	if _, err := os.Stat(realTarget); err != nil {
		t.Fatalf("symlink target was unexpectedly removed: %v", err)
	}
}

func TestSubdirectoryHintTrackerDiscoveryDedupAndSymlinkGuard(t *testing.T) {
	root := t.TempDir()
	// Root AGENTS.md (already loaded at session start — should be seeded & never re-injected).
	rootAgents := filepath.Join(root, "AGENTS.md")
	if err := os.WriteFile(rootAgents, []byte("Root instructions"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Nested subdirectory with AGENTS.override.md taking priority over AGENTS.md.
	subDir := filepath.Join(root, "services", "billing")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "AGENTS.md"), []byte("Regular billing rules"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "AGENTS.override.md"), []byte("Override billing rules"), 0o644); err != nil {
		t.Fatal(err)
	}
	targetFile := filepath.Join(subDir, "invoice.go")
	if err := os.WriteFile(targetFile, []byte("package billing\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Sibling directory with identical content to root AGENTS.md (deduped by SHA-256).
	dupDir := filepath.Join(root, "dup")
	if err := os.MkdirAll(dupDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dupDir, "CLAUDE.md"), []byte("Root instructions"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Excluded vendor directory should never be injected.
	vendorDir := filepath.Join(root, "vendor", "lib")
	if err := os.MkdirAll(vendorDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vendorDir, "AGENTS.md"), []byte("Vendored rules"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Symlink escape directory: sub/AGENTS.md -> outside secret file.
	outsideDir := t.TempDir()
	secretFile := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(secretFile, []byte("TOP SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	escapeDir := filepath.Join(root, "escape")
	if err := os.MkdirAll(escapeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	symlinkCreated := os.Symlink(secretFile, filepath.Join(escapeDir, "AGENTS.md")) == nil

	tracker := NewSubdirectoryHintTracker(root)

	// Non-file tool should be ignored.
	if got := tracker.CheckToolCall("terminal", map[string]any{"path": targetFile}); got != "" {
		t.Fatalf("expected non-file tool to yield no hints, got %q", got)
	}

	// First read_file in services/billing discovers AGENTS.override.md.
	hint := tracker.CheckToolCall("read_file", map[string]any{"path": targetFile})
	if !strings.Contains(hint, `<subdirectory_context path="services/billing/AGENTS.override.md">`) {
		t.Fatalf("expected AGENTS.override.md tag, got %q", hint)
	}
	if !strings.Contains(hint, "Override billing rules") || strings.Contains(hint, "Regular billing rules") {
		t.Fatalf("expected AGENTS.override.md priority over AGENTS.md, got %q", hint)
	}

	// Second access in same dir yields nothing (already loaded).
	if again := tracker.CheckToolCall("patch", map[string]any{"path": targetFile}); again != "" {
		t.Fatalf("expected second access in same dir to be deduped, got %q", again)
	}

	// Directory whose hint has identical SHA-256 to root AGENTS.md is deduped.
	if dupHint := tracker.CheckToolCall("read_file", map[string]any{"path": filepath.Join(dupDir, "main.go")}); dupHint != "" {
		t.Fatalf("expected duplicate SHA-256 hint to be skipped, got %q", dupHint)
	}

	// Excluded vendor dir is ignored.
	if vendorHint := tracker.CheckToolCall("search_files", map[string]any{"path": vendorDir}); vendorHint != "" {
		t.Fatalf("expected vendor dir hints to be excluded, got %q", vendorHint)
	}

	// Symlink escaping project root must be blocked.
	if symlinkCreated {
		if escHint := tracker.CheckToolCall("read_file", map[string]any{"path": filepath.Join(escapeDir, "x.go")}); escHint != "" {
			t.Fatalf("expected symlink escape to be blocked, got %q", escHint)
		}
	}
}

func TestSubdirectoryHintTruncationAndAncestorCap(t *testing.T) {
	root := t.TempDir()

	// 1. Oversized hint file (> 32 KiB) must be truncated with marker.
	bigDir := filepath.Join(root, "big")
	if err := os.MkdirAll(bigDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bigContent := strings.Repeat("A", maxSubdirHintBytes+4096)
	if err := os.WriteFile(filepath.Join(bigDir, "CLAUDE.md"), []byte(bigContent), 0o644); err != nil {
		t.Fatal(err)
	}
	tracker := NewSubdirectoryHintTracker(root)
	hint := tracker.CheckToolCall("write_file", map[string]any{"path": filepath.Join(bigDir, "out.txt")})
	if !strings.Contains(hint, "[...truncated big/CLAUDE.md:") {
		t.Fatalf("expected truncation marker for >32KiB hint, got len=%d", len(hint))
	}
	if len(hint) >= len(bigContent) {
		t.Fatalf("expected truncated hint to be shorter than original %d, got %d", len(bigContent), len(hint))
	}

	// 2. Append helper preserves []any content blocks.
	blocks := []any{map[string]any{"type": "text", "text": "file contents"}}
	updated, ok := appendHintToToolResultContent(blocks, hint).([]any)
	if !ok || len(updated) != 2 {
		t.Fatalf("expected 2 content blocks after append, got %#v", updated)
	}
}

func TestBuildContextManifestStatuses(t *testing.T) {
	p, _, _ := makeTestProfile(t)
	cwd := t.TempDir()

	// Write SOUL.md (loaded), HERMES.md (truncated > 20000 chars), AGENTS.md (shadowed),
	// CLAUDE.md (empty), and leave .cursorrules missing.
	if err := os.WriteFile(p.SoulFile, []byte("Be concise and accurate."), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "HERMES.md"), []byte(strings.Repeat("H", defaultContextFileMaxChars+500)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "AGENTS.md"), []byte("Shadowed agents instructions"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "CLAUDE.md"), []byte("   \n\t  "), 0o644); err != nil {
		t.Fatal(err)
	}

	manifest := BuildContextManifest(cwd, p)
	byLabel := make(map[string]ContextFileEntry, len(manifest))
	for _, e := range manifest {
		byLabel[e.Label] = e
	}

	if got := byLabel["SOUL.md"]; got.Status != ContextStatusLoaded || !got.Loaded || got.Chars == 0 || got.EstTokens == 0 {
		t.Fatalf("expected SOUL.md loaded with chars/tokens, got %+v", got)
	}
	if got := byLabel["HERMES.md"]; got.Status != ContextStatusTruncated || !got.Loaded {
		t.Fatalf("expected HERMES.md truncated & loaded, got %+v", got)
	}
	if got := byLabel["AGENTS.md"]; got.Status != ContextStatusShadowed || got.Loaded {
		t.Fatalf("expected AGENTS.md shadowed, got %+v", got)
	}
	if got := byLabel["CLAUDE.md"]; got.Status != ContextStatusEmpty || got.Loaded {
		t.Fatalf("expected CLAUDE.md empty, got %+v", got)
	}
	if got := byLabel[".cursorrules"]; got.Status != ContextStatusMissing || got.Loaded {
		t.Fatalf("expected .cursorrules missing, got %+v", got)
	}

	rendered := RenderContextManifest(manifest)
	for _, want := range []string{"SOUL.md", "HERMES.md", "AGENTS.md", "CLAUDE.md", ".cursorrules", "truncated", "shadowed", "empty", "missing"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("expected rendered manifest to contain %q, got:\n%s", want, rendered)
		}
	}
}
