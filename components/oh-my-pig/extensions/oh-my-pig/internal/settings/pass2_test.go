package settings

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
)

// N1: No-op /save must NOT rewrite the file; real scalar changes must preserve comments;
// fallback re-serialization (multi-line/flow values) must say comments were not preserved.
func TestSaveNoOpSkipsRewriteAndPreservesComments(t *testing.T) {
	ResetForTests()
	t.Cleanup(ResetForTests)
	tmp := t.TempDir()
	agentDir := filepath.Join(tmp, "agent")
	_ = os.MkdirAll(agentDir, 0o755)
	cfgPath := filepath.Join(agentDir, "config.yml")

	original := "# Top-level header comment\n" +
		"theme:\n" +
		"  # Dark theme preference\n" +
		"  dark: nord # inline dark comment\n" +
		"  light: snow\n" +
		"# Footer comment\n"
	if err := os.WriteFile(cfgPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-10 * time.Second).Truncate(time.Second)
	_ = os.Chtimes(cfgPath, oldTime, oldTime)

	st, err := NewStore(StoreOptions{AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}

	// 1. No-op save: value already equals "nord" -> file mtime and exact bytes untouched.
	res, err := st.setWithResult("theme.dark", "nord", true)
	if err != nil {
		t.Fatalf("no-op save failed: %v", err)
	}
	if res.changed {
		t.Error("no-op save reported changed=true")
	}
	info, _ := os.Stat(cfgPath)
	if !info.ModTime().Equal(oldTime) {
		t.Errorf("no-op save rewrote file: mtime changed from %v to %v", oldTime, info.ModTime())
	}
	gotRaw, _ := os.ReadFile(cfgPath)
	if string(gotRaw) != original {
		t.Errorf("no-op save modified bytes:\n%s", string(gotRaw))
	}

	// 2. Patch existing nested scalar: all comments (including inline comment) must survive.
	res, err = st.setWithResult("theme.dark", "titanium", true)
	if err != nil {
		t.Fatalf("patch existing scalar failed: %v", err)
	}
	if !res.changed || !res.commentsPreserved {
		t.Errorf("expected changed=true commentsPreserved=true, got %+v", res)
	}
	afterPatch, _ := os.ReadFile(cfgPath)
	sAfter := string(afterPatch)
	for _, wantComment := range []string{"# Top-level header comment", "# Dark theme preference", "# inline dark comment", "# Footer comment"} {
		if !strings.Contains(sAfter, wantComment) {
			t.Errorf("comment %q lost after scalar patch:\n%s", wantComment, sAfter)
		}
	}
	if !strings.Contains(sAfter, "dark: titanium # inline dark comment") {
		t.Errorf("patched line wrong:\n%s", sAfter)
	}

	// 3. Append new key under existing parent AND brand-new top-level key: comments survive.
	if res, err = st.setWithResult("ask.timeout", 30, true); err != nil || !res.commentsPreserved {
		t.Fatalf("append nested new key: res=%+v err=%v", res, err)
	}
	if res, err = st.setWithResult("autoResume", true, true); err != nil || !res.commentsPreserved {
		t.Fatalf("append top-level new key: res=%+v err=%v", res, err)
	}
	afterAppend, _ := os.ReadFile(cfgPath)
	for _, wantComment := range []string{"# Top-level header comment", "# Dark theme preference", "# inline dark comment", "# Footer comment"} {
		if !strings.Contains(string(afterAppend), wantComment) {
			t.Errorf("comment %q lost after append:\n%s", wantComment, string(afterAppend))
		}
	}
	reloaded, err := NewStore(StoreOptions{AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.String("theme.dark") != "titanium" || reloaded.Int("ask.timeout") != 30 || !reloaded.Bool("autoResume") {
		t.Errorf("reloaded mismatch: dark=%q timeout=%d autoResume=%v", reloaded.String("theme.dark"), reloaded.Int("ask.timeout"), reloaded.Bool("autoResume"))
	}

	// 4. Fallback save (non-scalar record write) over a commented file warns in cfg:// result text.
	host := &stubApprovalHost{persistent: st}
	SetApprovalHost(host)
	wres, err := cfgWrite(t, "cfg://modelRoles/save", `{"smol":"anthropic/fast"}`, st, "sess-comment-fallback")
	if err != nil {
		t.Fatalf("fallback save failed: %v", err)
	}
	if !strings.Contains(wres.Text, "comments in the config file were not preserved") {
		t.Errorf("fallback save missing comment-loss notice in result text:\n%s", wres.Text)
	}
}

// N1 fuzz: random commented YAML documents + random single-key scalar mutations
// (updates and inserts); every comment line survives in the patch path, and the
// parsed document ALWAYS equals the intended change.
func TestPatchYAMLCommentsFuzz(t *testing.T) {
	rng := rand.New(rand.NewPCG(20261124, 1))
	keys := [][]string{
		{"theme", "dark"},
		{"theme", "light"},
		{"advisor", "enabled"},
		{"advisor", "syncBacklog"},
		{"ask", "timeout"},
		{"autoResume"},
		{"symbolPreset"},
		{"read", "defaultLimit"},
		{"read", "summarize", "enabled"},
		{"read", "summarize", "prose"},
	}
	scalarFor := func(seg []string) any {
		id := strings.Join(seg, ".")
		switch id {
		case "theme.dark", "theme.light":
			choices := []string{"titanium", "nord", "gruvbox", "light", "custom:theme", "it's dark", "true", "0x10"}
			return choices[rng.IntN(len(choices))]
		case "advisor.enabled", "autoResume", "read.summarize.enabled", "read.summarize.prose":
			return rng.IntN(2) == 1
		case "advisor.syncBacklog":
			choices := []string{"off", "1", "3", "5", "strict"}
			return choices[rng.IntN(len(choices))]
		case "ask.timeout", "read.defaultLimit":
			return float64(rng.IntN(500))
		case "symbolPreset":
			choices := []string{"unicode", "nerd", "ascii"}
			return choices[rng.IntN(len(choices))]
		}
		return "val"
	}

	for iter := range 250 {
		// Build a random commented YAML document with 1..5 initial settings.
		var docLines []string
		var expectedComments []string
		c1 := fmt.Sprintf("# doc header %d", iter)
		docLines = append(docLines, c1)
		expectedComments = append(expectedComments, c1)

		// Include a subset of blocks with comments.
		if rng.IntN(2) == 0 {
			c2 := fmt.Sprintf("  # theme comment %d", iter)
			docLines = append(docLines, "theme:", c2, fmt.Sprintf("  dark: nord # inline-dark-%d", iter))
			expectedComments = append(expectedComments, c2, fmt.Sprintf("# inline-dark-%d", iter))
			if rng.IntN(2) == 0 {
				docLines = append(docLines, "  light: snow")
			}
		}
		if rng.IntN(2) == 0 {
			c3 := fmt.Sprintf("# read section %d", iter)
			docLines = append(docLines, c3, "read:", "  defaultLimit: 300")
			expectedComments = append(expectedComments, c3)
			if rng.IntN(2) == 0 {
				c4 := fmt.Sprintf("    # summarize comment %d", iter)
				docLines = append(docLines, "  summarize:", c4, fmt.Sprintf("    enabled: true # sum-inline-%d", iter))
				expectedComments = append(expectedComments, c4, fmt.Sprintf("# sum-inline-%d", iter))
			}
		}
		if rng.IntN(2) == 0 {
			docLines = append(docLines, fmt.Sprintf("autoResume: false # top-inline-%d", iter))
			expectedComments = append(expectedComments, fmt.Sprintf("# top-inline-%d", iter))
		}
		cEnd := fmt.Sprintf("# footer %d", iter)
		docLines = append(docLines, cEnd)
		expectedComments = append(expectedComments, cEnd)

		src := strings.Join(docLines, "\n") + "\n"
		before, err := ParseYAMLOrdered(src)
		if err != nil {
			t.Fatalf("iter %d: initial parse failed: %v\n%s", iter, err, src)
		}

		// Apply 3 sequential random scalar changes.
		curText := src
		curMap := before.Clone()
		for step := range 3 {
			seg := keys[rng.IntN(len(keys))]
			val := scalarFor(seg)
			patched, ok := patchYAMLText(curText, seg, val, false)
			if !ok {
				t.Fatalf("iter %d step %d: patchYAMLText returned ok=false for %v=%#v\ndoc:\n%s", iter, step, seg, val, curText)
			}
			for _, cm := range expectedComments {
				if !strings.Contains(patched, cm) {
					t.Fatalf("iter %d step %d: comment %q dropped after patching %v=%#v\nbefore:\n%s\nafter:\n%s", iter, step, cm, seg, val, curText, patched)
				}
			}
			parsed, err := ParseYAMLOrdered(patched)
			if err != nil {
				t.Fatalf("iter %d step %d: patched YAML failed to parse: %v\n%s", iter, step, err, patched)
			}
			setByPathOrdered(curMap, seg, deepCloneOrdered(val))
			if !reflect.DeepEqual(parsed.ToPlainMap(), curMap.ToPlainMap()) {
				t.Fatalf("iter %d step %d: parsed mismatch for %v=%#v\nwant: %#v\ngot:  %#v\nyaml:\n%s",
					iter, step, seg, val, curMap.ToPlainMap(), parsed.ToPlainMap(), patched)
			}
			curText = patched
		}
	}
}

// N2: 8 concurrent writers saving 8 distinct keys across 40 rounds must lose ZERO writes.
func TestConcurrentEightWriters40Rounds(t *testing.T) {
	ResetForTests()
	t.Cleanup(ResetForTests)
	tmp := t.TempDir()
	agentDir := filepath.Join(tmp, "agent")
	_ = os.MkdirAll(agentDir, 0o755)
	cfgPath := filepath.Join(agentDir, "config.yml")

	writerKeys := []struct {
		key string
		val func(round int) any
	}{
		{"read.defaultLimit", func(r int) any { return float64(200 + r) }},
		{"ask.timeout", func(r int) any { return float64(10 + r) }},
		{"todo.remindersMax", func(r int) any { return float64(1 + r) }},
		{"lsp.requestTimeout", func(r int) any { return float64(15 + r) }},
		{"exa.searchDelayMs", func(r int) any { return float64(500 + r) }},
		{"grep.contextBefore", func(r int) any { return float64(r % 5) }},
		{"grep.contextAfter", func(r int) any { return float64((r + 1) % 5) }},
		{"theme.dark", func(r int) any { return fmt.Sprintf("theme-%d", r) }},
	}

	stores := make([]*Store, len(writerKeys))
	for i := range stores {
		st, err := NewStore(StoreOptions{AgentDir: agentDir, SessionID: fmt.Sprintf("w-%d", i)})
		if err != nil {
			t.Fatal(err)
		}
		stores[i] = st
	}

	for round := range 40 {
		var wg sync.WaitGroup
		errs := make([]error, len(writerKeys))
		for i, wk := range writerKeys {
			wg.Add(1)
			go func(idx int, key string, val any) {
				defer wg.Done()
				errs[idx] = stores[idx].Set(key, val, true)
			}(i, wk.key, wk.val(round))
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("round %d writer %d (%s) failed: %v", round, i, writerKeys[i].key, err)
			}
		}

		// Re-read raw file from disk and verify ALL 8 keys match this round's values.
		onDisk, ok, err := readConfigFileOrdered(cfgPath)
		if err != nil || !ok {
			t.Fatalf("round %d readConfigFileOrdered: ok=%v err=%v", round, ok, err)
		}
		for _, wk := range writerKeys {
			got, exists := getByPathOrdered(onDisk, strings.Split(wk.key, "."))
			want := wk.val(round)
			if !exists || !orderedValuesEqual(got, want) {
				t.Fatalf("round %d lost write for %s: got (%#v, %v), want %#v", round, wk.key, got, exists, want)
			}
		}
	}
}

// T1: a pre-existing permanent lock file left behind by an exited process does not
// block advisory locking, and the permanent lock file remains in place after release.
func TestAdvisoryLockPermanentFile(t *testing.T) {
	ResetForTests()
	t.Cleanup(ResetForTests)
	tmp := t.TempDir()
	agentDir := filepath.Join(tmp, "agent")
	_ = os.MkdirAll(agentDir, 0o755)
	cfgPath := filepath.Join(agentDir, "config.yml")
	lockPath := cfgPath + ".lock"

	// Pre-existing lock file left by a dead process (unlocked by OS on exit).
	_ = os.WriteFile(lockPath, []byte("stale-contents"), 0o600)

	st, err := NewStore(StoreOptions{AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Set("ask.timeout", 12, true); err != nil {
		t.Fatalf("save with pre-existing unlocked lock file failed: %v", err)
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Errorf("permanent lock file should not be removed after release: %v", err)
	}
}

// N3: Malformed global config caches the store with its error so session overrides
// persist across reads, reads don't re-parse on every call, and fixing the file on
// disk (changing mtime/size) automatically recovers on the next read.
func TestMalformedGlobalCachesOverridesAndRecoversOnFix(t *testing.T) {
	ResetForTests()
	t.Cleanup(ResetForTests)
	tmp := t.TempDir()
	t.Setenv("PIG_HOME", tmp)
	t.Setenv("PIG_CODING_AGENT_DIR", "")
	agentDir := filepath.Join(tmp, "agent")
	_ = os.MkdirAll(agentDir, 0o755)
	cfgPath := filepath.Join(agentDir, "config.yml")
	_ = os.WriteFile(cfgPath, []byte("theme:\n  dark: \"unterminated\n"), 0o600)

	ext := sdk.New("malformed-n3")
	Register(ext)
	var beforeFix, afterFix bool
	var darkAfterFix string
	ext.Tool("op", "op", map[string]any{"type": "object"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		switch params["act"] {
		case "set":
			if err := Set(ctx, "advisor.enabled", true, false); err != nil {
				return sdk.ToolResult{Content: err.Error(), IsError: true}, nil
			}
		case "read":
			beforeFix = Bool(ctx, "advisor.enabled")
		case "read_fixed":
			afterFix = Bool(ctx, "advisor.enabled")
			darkAfterFix = String(ctx, "theme.dark")
		}
		return sdk.ToolResult{Content: "ok"}, nil
	})
	host := startHost(t, ext, hostOptions{Mode: "tui", Cwd: tmp, SessionID: "sess-n3"})

	// Session override must persist across subsequent reads even while global file is broken.
	_, _ = host.callTool("op", map[string]any{"act": "set"})
	_, _ = host.callTool("op", map[string]any{"act": "read"})
	if !beforeFix {
		t.Fatal("session override vanished on malformed global config (N3)")
	}
	if got := len(host.callsTo("ui.notify")); got != 1 {
		t.Errorf("expected 1 notify warning, got %d", got)
	}

	// Fix the file on disk (different size + newer mtime): next read reloads global layer AND keeps session override.
	_ = os.WriteFile(cfgPath, []byte("theme:\n  dark: nord\n"), 0o600)
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(cfgPath, future, future)

	_, _ = host.callTool("op", map[string]any{"act": "read_fixed"})
	if !afterFix || darkAfterFix != "nord" {
		t.Errorf("after fix: advisor.enabled=%v (want true), theme.dark=%q (want nord)", afterFix, darkAfterFix)
	}
}

// N4 + T2: IsProjectTrusted is called ONCE when the session entry is created (not on
// every read), `project_trust` is NOT hooked (so a pending decision never drops the
// project layer), and `before_agent_start` / `session_start` re-resolve trust while
// preserving session overrides.
func TestTrustCheckedOnceAndRefreshedAtTurnStart(t *testing.T) {
	ResetForTests()
	t.Cleanup(ResetForTests)
	tmp := t.TempDir()
	t.Setenv("PIG_HOME", tmp)
	t.Setenv("PIG_CODING_AGENT_DIR", "")
	cwd := filepath.Join(tmp, "proj")
	_ = os.MkdirAll(filepath.Join(cwd, ".pig"), 0o755)
	_ = os.WriteFile(filepath.Join(cwd, ".pig", "config.yml"), []byte("theme:\n  dark: project-dark\n"), 0o600)

	ext := sdk.New("trust-n4")
	Register(ext)
	var gotDark string
	var gotAdvisor bool
	ext.Tool("reads", "reads", map[string]any{"type": "object"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		if params["override"] == true {
			_ = Set(ctx, "advisor.enabled", true, false)
		}
		for range 50 {
			gotDark = String(ctx, "theme.dark")
			gotAdvisor = Bool(ctx, "advisor.enabled")
		}
		return sdk.ToolResult{Content: gotDark}, nil
	})

	var trustedMu sync.Mutex
	isTrusted := false
	host := startHost(t, ext, hostOptions{
		Mode:      "tui",
		Cwd:       cwd,
		SessionID: "sess-n4",
		OnCall: func(method string, _ map[string]any) (map[string]any, string) {
			if method == "isProjectTrusted" {
				trustedMu.Lock()
				v := isTrusted
				trustedMu.Unlock()
				return map[string]any{"trusted": v}, ""
			}
			return nil, ""
		},
	})

	// Verify project_trust is NOT registered (T2).
	if len(host.handlers[sdk.EventProjectTrust]) != 0 {
		t.Fatalf("settings must not hook project_trust event; got handlers %v", host.handlers[sdk.EventProjectTrust])
	}

	// 50 reads must result in exactly 1 isProjectTrusted host call.
	_, _ = host.callTool("reads", map[string]any{"override": true})
	if trustCalls := len(host.callsTo("isProjectTrusted")); trustCalls != 1 {
		t.Fatalf("expected exactly 1 isProjectTrusted call across 50 reads, got %d", trustCalls)
	}
	if gotDark != "titanium" || !gotAdvisor {
		t.Fatalf("before trust: dark=%q advisor=%v, want titanium/true", gotDark, gotAdvisor)
	}

	// User trusts the project, then before_agent_start runs on the next turn:
	// project layer appears AND session override survives!
	trustedMu.Lock()
	isTrusted = true
	trustedMu.Unlock()
	_, fail := host.fire(sdk.EventBeforeAgentStart, map[string]any{
		"type":                sdk.EventBeforeAgentStart,
		"prompt":              "hello",
		"systemPrompt":        "",
		"systemPromptOptions": map[string]any{"cwd": cwd},
	})
	if fail != "" {
		t.Fatalf("before_agent_start failed: %s", fail)
	}
	_, _ = host.callTool("reads", map[string]any{})
	if gotDark != "project-dark" {
		t.Errorf("after user trusts + before_agent_start: theme.dark=%q, want project-dark", gotDark)
	}
	if !gotAdvisor {
		t.Error("session override advisor.enabled was dropped on trust change (N4/T2)")
	}

	// Pending/transient error on isProjectTrusted during session_start must NOT drop the trusted project layer.
	_, fail = host.fire(sdk.EventSessionStart, map[string]any{"type": sdk.EventSessionStart})
	if fail != "" {
		t.Fatalf("session_start failed: %s", fail)
	}
	_, _ = host.callTool("reads", map[string]any{})
	if gotDark != "project-dark" {
		t.Errorf("after session_start: theme.dark=%q, want project-dark", gotDark)
	}
}

// N5: Subagents read through to parent session's overrides via ParentSession hook,
// while child overrides stay local to the child (upstream config/settings.ts:981).
func TestSubagentReadsThroughParentOverrides(t *testing.T) {
	ResetForTests()
	t.Cleanup(func() {
		ResetForTests()
		lifecycle.ResetForTests()
	})
	tmp := t.TempDir()
	t.Setenv("PIG_HOME", tmp)
	t.Setenv("PIG_CODING_AGENT_DIR", "")
	cwd := filepath.Join(tmp, "proj")
	_ = os.MkdirAll(cwd, 0o755)

	ParentSession = func(ctx sdk.Context) (string, bool) {
		if id, err := ctx.GetSessionID(); err == nil && id == "child-sess" {
			return "parent-sess", true
		}
		return "", false
	}

	extP := sdk.New("parent")
	Register(extP)
	extC := sdk.New("child")
	Register(extC)

	var pDark, cDark, cLight string
	extP.Tool("p_op", "p", map[string]any{"type": "object"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		if v, ok := params["dark"].(string); ok {
			_ = Set(ctx, "theme.dark", v, false)
		}
		pDark = String(ctx, "theme.dark")
		return sdk.ToolResult{Content: pDark}, nil
	})
	extC.Tool("c_op", "c", map[string]any{"type": "object"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		if v, ok := params["light"].(string); ok {
			_ = Set(ctx, "theme.light", v, false)
		}
		cDark = String(ctx, "theme.dark")
		cLight = String(ctx, "theme.light")
		return sdk.ToolResult{Content: cDark}, nil
	})

	hP := startHost(t, extP, hostOptions{Mode: "tui", Cwd: cwd, SessionID: "parent-sess"})
	hC := startHost(t, extC, hostOptions{Mode: "tui", Cwd: cwd, SessionID: "child-sess"})

	// Parent sets theme.dark override; child sees it live.
	_, _ = hP.callTool("p_op", map[string]any{"dark": "nord"})
	_, _ = hC.callTool("c_op", map[string]any{"light": "latte"})
	if cDark != "nord" || cLight != "latte" {
		t.Fatalf("child initial read: dark=%q light=%q, want nord/latte", cDark, cLight)
	}
	// Parent updates theme.dark again; child sees the live update.
	_, _ = hP.callTool("p_op", map[string]any{"dark": "gruvbox"})
	_, _ = hC.callTool("c_op", map[string]any{})
	if cDark != "gruvbox" {
		t.Errorf("child did not see live parent override update: got %q, want gruvbox", cDark)
	}
	// Child's own override does not leak into parent.
	if pLight := func() string {
		sessionsMu.Lock()
		defer sessionsMu.Unlock()
		return sessions["parent-sess"].store.String("theme.light")
	}(); pLight != "light" {
		t.Errorf("child override leaked to parent: theme.light=%q, want default light", pLight)
	}
}

// N6 + OnChange: A save from session A is immediately visible to session B on its
// next read, and OnChange fires outside store locks (safe to read settings inside listener).
func TestSaveVisibleAcrossSessionsAndOnChangeHook(t *testing.T) {
	ResetForTests()
	t.Cleanup(func() {
		ResetForTests()
		lifecycle.ResetForTests()
	})
	tmp := t.TempDir()
	t.Setenv("PIG_HOME", tmp)
	t.Setenv("PIG_CODING_AGENT_DIR", "")
	cwd := filepath.Join(tmp, "proj")
	_ = os.MkdirAll(cwd, 0o755)

	var (
		mu     sync.Mutex
		events []string
	)
	extA := sdk.New("sess-a")
	Register(extA)
	extB := sdk.New("sess-b")
	Register(extB)

	var bLimit int
	extA.Tool("a_save", "a", map[string]any{"type": "object"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		persist, _ := params["persist"].(bool)
		if err := Set(ctx, "read.defaultLimit", 777, persist); err != nil {
			return sdk.ToolResult{Content: err.Error(), IsError: true}, nil
		}
		return sdk.ToolResult{Content: "ok"}, nil
	})
	extB.Tool("b_read", "b", map[string]any{"type": "object"}, func(ctx sdk.Context, _ map[string]any) (any, error) {
		bLimit = Int(ctx, "read.defaultLimit")
		return sdk.ToolResult{Content: fmt.Sprint(bLimit)}, nil
	})

	hA := startHost(t, extA, hostOptions{Mode: "tui", Cwd: cwd, SessionID: "sess-A"})
	hB := startHost(t, extB, hostOptions{Mode: "tui", Cwd: cwd, SessionID: "sess-B"})

	// Prime session B's store before session A saves.
	_, _ = hB.callTool("b_read", map[string]any{})
	if bLimit != 300 {
		t.Fatalf("initial B read.defaultLimit = %d, want 300", bLimit)
	}

	remove := OnChange(func(sessionID, key string) {
		// Reading from store inside OnChange proves no store lock is held (no deadlock).
		sessionsMu.Lock()
		st := sessions[sessionID]
		sessionsMu.Unlock()
		if st != nil {
			_ = st.store.Int(key)
		}
		mu.Lock()
		events = append(events, sessionID+":"+key)
		mu.Unlock()
	})

	// Session A persists read.defaultLimit = 777; session B must see 777 on its very next read.
	_, _ = hA.callTool("a_save", map[string]any{"persist": true})
	_, _ = hB.callTool("b_read", map[string]any{})
	if bLimit != 777 {
		t.Fatalf("session B saw stale read.defaultLimit = %d after A saved 777 (N6)", bLimit)
	}

	// Unsubscribe stops further notifications.
	remove()
	remove() // idempotent
	_, _ = hA.callTool("a_save", map[string]any{"persist": false})

	mu.Lock()
	gotEvents := append([]string(nil), events...)
	mu.Unlock()
	// T4: global /save notifies EVERY live session sharing the global file whose effective value changed.
	wantEvents := []string{"sess-A:read.defaultLimit", "sess-B:read.defaultLimit"}
	if len(gotEvents) != 2 ||
		!((gotEvents[0] == wantEvents[0] && gotEvents[1] == wantEvents[1]) ||
			(gotEvents[0] == wantEvents[1] && gotEvents[1] == wantEvents[0])) {
		t.Errorf("OnChange events = %v, want %v", gotEvents, wantEvents)
	}
}

// A5 residual (`1_000` rejected) and B7 residual (`- - - ...` capped linearly).
func TestNumberUnderscoreRejectedAndDeepSequenceFast(t *testing.T) {
	numEntry, _ := Lookup("ask.timeout")
	if _, err := numEntry.ParseText("1_000"); err == nil || err.Error() != "Invalid number: 1_000" {
		t.Errorf("ParseText(1_000) err = %v, want 'Invalid number: 1_000'", err)
	}

	// B7 residual: 40 KB of `- - - ...` must fail fast (< 50 ms) with depth error instead of hanging.
	deep := "a:\n  " + strings.Repeat("- ", 20000) + "x\n"
	start := time.Now()
	if _, err := ParseYAML(deep); err == nil {
		t.Fatal("expected error for 20000-deep inline sequence")
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Errorf("deep `- - -` parse took %v, expected < 200ms", elapsed)
	}

	// U+FEFF key round-trip (B6 residual noted by reviewer).
	bomKey := map[string]any{"\uFEFFkey": "val"}
	out, err := MarshalYAML(bomKey)
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseYAML(out)
	if err != nil || !reflect.DeepEqual(back, bomKey) {
		t.Errorf("U+FEFF key round-trip failed: out=%q back=%#v err=%v", out, back, err)
	}
}
