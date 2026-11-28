package modes

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func TestTanSlashCommand(t *testing.T) {
	origSpawn := SpawnDetachedTask
	t.Cleanup(func() { SpawnDetachedTask = origSpawn })

	var gotReq DetachedTaskRequest
	spawnCalls := 0
	SpawnDetachedTask = func(_ sdk.Context, req DetachedTaskRequest) (DetachedTaskResult, error) {
		spawnCalls++
		gotReq = req
		return DetachedTaskResult{
			JobID:   "job-tan-1",
			AgentID: req.Name,
		}, nil
	}

	_, h := newTestExtension(t, hostOptions{
		SessionID: "s-tan-1",
		Branch: []map[string]any{
			{
				"id":   "u1",
				"type": "message",
				"message": map[string]any{
					"role":    "user",
					"content": "Working on parser refactor",
				},
			},
			{
				"id":   "a1",
				"type": "message",
				"message": map[string]any{
					"role":    "assistant",
					"content": []any{map[string]any{"type": "text", "text": "Inspecting lexer.go"}},
				},
			},
		},
	})

	// Empty args -> usage
	h.callCommand("tan", "   ")
	if got := h.lastNotify(); got != "Usage: /tan <work>" {
		t.Fatalf("empty /tan notify = %q, want Usage: /tan <work>", got)
	}
	if spawnCalls != 0 {
		t.Fatalf("empty /tan should not spawn, got %d", spawnCalls)
	}

	// Non-empty work -> spawns detached task with forked context + tangent notice
	h.callCommand("tan", "fix flaky retry test in client")
	if spawnCalls != 1 {
		t.Fatalf("expected 1 spawn, got %d", spawnCalls)
	}
	if gotReq.Name != "FixFlakyRetryTestInClient" {
		t.Fatalf("tan PascalCase name = %q, want FixFlakyRetryTestInClient", gotReq.Name)
	}
	if gotReq.AgentDisplayName != "tan" || !gotReq.ShareParentLocal {
		t.Fatalf("unexpected tan request metadata: %+v", gotReq)
	}
	if !strings.Contains(gotReq.Context, "Working on parser refactor") || !strings.Contains(gotReq.Context, `<system-notice cause="fork">`) {
		t.Fatalf("tan context missing parent snapshot or fork notice: %q", gotReq.Context)
	}
	notify := h.lastNotify()
	if !strings.Contains(notify, "agent://FixFlakyRetryTestInClient") || !strings.Contains(notify, "proc://job-tan-1") {
		t.Fatalf("tan notify missing handles: %q", notify)
	}

	// Confirm non-triggering tangent-notice custom message was sent.
	foundNotice := false
	for _, c := range h.callsTo("sendMessage") {
		if msg, ok := c.Args["message"].(map[string]any); ok {
			if ct, _ := msg["customType"].(string); ct == "background-tan-dispatch" {
				foundNotice = true
			}
		}
	}
	if !foundNotice {
		t.Fatalf("expected background-tan-dispatch custom message")
	}
}

func TestCleanseArgParserAndDiscovery(t *testing.T) {
	opts, err := ParseCleanseArgs("")
	if err != nil {
		t.Fatalf("empty parse failed: %v", err)
	}
	if opts.MaxAgents != 32 || opts.Model != "@smol" || opts.All || opts.IncludeTests || opts.Request != "" {
		t.Fatalf("unexpected defaults: %+v", opts)
	}

	opts, err = ParseCleanseArgs("fix lint --all -t -n 128 -m @slow")
	if err != nil {
		t.Fatalf("full parse failed: %v", err)
	}
	if opts.Request != "fix lint" || !opts.All || !opts.IncludeTests || opts.MaxAgents != 64 || opts.Model != "@slow" {
		t.Fatalf("unexpected parsed options: %+v", opts)
	}

	if _, err := ParseCleanseArgs("--agents 0"); err == nil {
		t.Fatalf("expected error for --agents 0")
	}
	if _, err := ParseCleanseArgs("--bogus"); err == nil {
		t.Fatalf("expected error for unknown flag")
	}

	// Discovery across Go, JS/TS, Rust, Python
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/test\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"scripts":{"typecheck":"tsc","lint":"eslint","test":"vitest"}}`), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]\nname=\"demo\"\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte("[tool.ruff]\n[tool.mypy]\n"), 0o644)

	checkers := DiscoverCleanseCheckers(dir, true)
	ids := map[string]bool{}
	for _, c := range checkers {
		ids[c.ID] = true
	}
	for _, want := range []string{"go-vet", "go-test", "js-typecheck", "js-lint", "js-test", "cargo-check", "cargo-clippy", "cargo-test", "ruff", "mypy", "pytest"} {
		if !ids[want] {
			t.Errorf("missing discovered checker %q in %+v", want, checkers)
		}
	}
}

func TestCleanseCommandSingleActiveRunAndCancel(t *testing.T) {
	origSpawn := SpawnCleanse
	t.Cleanup(func() { SpawnCleanse = origSpawn })

	cancelled := false
	var lastReq CleanseRequest
	SpawnCleanse = func(_ sdk.Context, req CleanseRequest) (func(), error) {
		lastReq = req
		return func() { cancelled = true }, nil
	}

	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/demo\n"), 0o644)

	hasUI := false
	_, h := newTestExtension(t, hostOptions{
		SessionID: "s-cleanse-1",
		Cwd:       dir,
		HasUI:     &hasUI,
	})

	h.callCommand("cleanse", "--all --tests -n 8")
	if lastReq.Options.MaxAgents != 8 || !lastReq.Options.All || !lastReq.Options.IncludeTests {
		t.Fatalf("unexpected cleanse request options: %+v", lastReq.Options)
	}
	if len(lastReq.Checkers) != 2 {
		t.Fatalf("expected 2 Go checkers, got %+v", lastReq.Checkers)
	}

	// Second call while active is rejected by single-active-run guard.
	h.callCommand("cleanse", "--all")
	if got := h.lastNotify(); !strings.Contains(got, "already active") {
		t.Fatalf("expected active-run guard warning, got %q", got)
	}

	// Cancel releases the guard and invokes cancel callback.
	h.callCommand("cleanse", "cancel")
	if !cancelled {
		t.Fatalf("expected cleanse cancel callback to run")
	}
	if got := h.lastNotify(); got != "Cleanse cancelled." {
		t.Fatalf("cancel notify = %q, want Cleanse cancelled.", got)
	}
}

func TestExportCopyGuardAndThemesHTML(t *testing.T) {
	dir := t.TempDir()
	_, h := newTestExtension(t, hostOptions{
		SessionID:   "s-export-1",
		SessionName: "Wave 8 <Demo>",
		Cwd:         dir,
		Branch: []map[string]any{
			{
				"id":   "u1",
				"type": "message",
				"message": map[string]any{
					"role":    "user",
					"content": "Check <script>alert(1)</script>",
				},
			},
			{
				"id":   "a1",
				"type": "message",
				"message": map[string]any{
					"role":    "assistant",
					"content": []any{map[string]any{"type": "text", "text": "All safe & escaped."}},
				},
			},
		},
	})

	// Ensure `/export` is NOT registered as a colliding slash command.
	if h.commands["export"] {
		t.Fatalf("/export must NOT be registered as a slash command (collides with PiG built-in)")
	}

	// Bare `/export` and `/export out.html` pass through (`action: continue`).
	for _, input := range []string{"/export", "/export out.html"} {
		raw := h.fireEvent(sdk.EventInput, map[string]any{"text": input, "source": "interactive"})
		var res map[string]any
		_ = json.Unmarshal(raw, &res)
		if res["action"] != "continue" {
			t.Fatalf("input %q should pass through to PiG builtin, got %v", input, res)
		}
	}

	// `/export --copy`, `/export copy`, `/export clipboard` intercepted with /dump hint.
	for _, input := range []string{"/export --copy", "/export copy", "/export clipboard"} {
		raw := h.fireEvent(sdk.EventInput, map[string]any{"text": input, "source": "interactive"})
		var res map[string]any
		_ = json.Unmarshal(raw, &res)
		if res["action"] != "handled" {
			t.Fatalf("input %q should be handled, got %v", input, res)
		}
		if got := h.lastNotify(); got != "Use /dump to copy the session to clipboard." {
			t.Fatalf("input %q notify = %q, want %q", input, got, "Use /dump to copy the session to clipboard.")
		}
	}

	// `/export --themes custom.html` writes escaped dual-theme HTML atomically.
	raw := h.fireEvent(sdk.EventInput, map[string]any{"text": "/export --themes custom.html", "source": "interactive"})
	var res map[string]any
	_ = json.Unmarshal(raw, &res)
	if res["action"] != "handled" {
		t.Fatalf("/export --themes should be handled, got %v", res)
	}
	dest := filepath.Join(dir, "custom.html")
	if got := h.lastNotify(); got != "Session exported to: "+dest {
		t.Fatalf("export notify = %q, want %q", got, "Session exported to: "+dest)
	}
	bodyBytes, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("failed reading exported HTML: %v", err)
	}
	body := string(bodyBytes)
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Fatalf("HTML export failed to escape script tag!")
	}
	if !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") || !strings.Contains(body, `data-theme="dark"`) || !strings.Contains(body, `Wave 8 &lt;Demo&gt;`) {
		t.Fatalf("unexpected exported HTML content:\n%s", body)
	}
}

func TestTanUTF8TruncationValid(t *testing.T) {
	// Multi-byte runes must never split mid-rune in previewTanWork or buildTanContextSnapshot.
	longWork := strings.Repeat("日本語", 60)
	preview := previewTanWork(longWork)
	if len([]rune(preview)) != tanLabelPreviewLen || !strings.HasSuffix(preview, "…") {
		t.Fatalf("unexpected previewTanWork result: runeLen=%d suffix=%q", len([]rune(preview)), preview[len(preview)-3:])
	}
	longBranch := []sdk.BranchEntry{
		{Type: "message", Role: "user", Content: strings.Repeat("🚀", tanBranchMaxChars+50)},
	}
	snap := buildTanContextSnapshot(longBranch, "work")
	if !strings.Contains(snap, "…(earlier turns truncated)…") {
		t.Fatalf("expected truncated branch marker")
	}
}

func TestCleanseRollbackOnErrorAndNilCancel(t *testing.T) {
	origSpawn := SpawnCleanse
	t.Cleanup(func() { SpawnCleanse = origSpawn })

	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/test\n\ngo 1.24\n"), 0o644)
	hasUI := false
	_, h := newTestExtension(t, hostOptions{
		SessionID: "s-cleanse-rollback",
		Cwd:       dir,
		HasUI:     &hasUI,
	})

	// SpawnCleanse returning an error must release cleanseActive so a subsequent run succeeds.
	SpawnCleanse = func(_ sdk.Context, _ CleanseRequest) (func(), error) {
		return nil, fmt.Errorf("boom")
	}
	h.callCommand("cleanse", "--all")
	if got := h.lastNotify(); !strings.Contains(got, "Failed to start /cleanse") {
		t.Fatalf("expected failure notify, got %q", got)
	}

	// SpawnCleanse returning nil cancelFn must also release cleanseActive on completion.
	calls := 0
	SpawnCleanse = func(_ sdk.Context, _ CleanseRequest) (func(), error) {
		calls++
		return nil, nil
	}
	h.callCommand("cleanse", "--all")
	h.callCommand("cleanse", "--all")
	if calls != 2 {
		t.Fatalf("expected cleanseActive rollback after error and nil cancelFn, got %d calls", calls)
	}
}

func TestExportThemesTildeExpansionAndFileMode0600(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("USERPROFILE", fakeHome)

	dir := t.TempDir()
	_, h := newTestExtension(t, hostOptions{
		SessionID: "s-export-tilde",
		Cwd:       dir,
		Branch: []map[string]any{
			{"id": "u1", "type": "message", "message": map[string]any{"role": "user", "content": "hi"}},
		},
	})

	raw := h.fireEvent(sdk.EventInput, map[string]any{"text": "/export --themes ~/exported.html", "source": "interactive"})
	var res map[string]any
	_ = json.Unmarshal(raw, &res)
	if res["action"] != "handled" {
		t.Fatalf("expected handled, got %v", res)
	}
	wantPath := filepath.Join(fakeHome, "exported.html")
	info, err := os.Stat(wantPath)
	if err != nil {
		t.Fatalf("expected exported file at %s: %v", wantPath, err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		t.Fatalf("expected mode 0600 for exported HTML, got %o", mode)
	}
}
