package search

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
)

func TestGrepContextSettingsWiring(t *testing.T) {
	dir := t.TempDir()
	content := strings.Join([]string{
		"line 1 alpha",
		"line 2 beta",
		"line 3 NEEDLE_ONE",
		"line 4 gamma",
		"line 5 delta",
		"line 6 epsilon",
		"line 7 zeta",
		"line 8 NEEDLE_TWO",
		"line 9 eta",
		"line 10 theta",
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. Default (Setting == nil): 0 context lines -> only matching lines appear.
	setSettingForTest(t, nil)
	res, err := runGrep(context.Background(), dir, map[string]any{
		"pattern": "NEEDLE_",
		"path":    "sample.txt",
	})
	if err != nil {
		t.Fatalf("default grep err: %v", err)
	}
	tr := res.(sdk.ToolResult)
	if !strings.Contains(tr.Content, "*3:line 3 NEEDLE_ONE") || !strings.Contains(tr.Content, "*8:line 8 NEEDLE_TWO") {
		t.Fatalf("expected matching lines, got:\n%s", tr.Content)
	}
	if strings.Contains(tr.Content, "line 2 beta") || strings.Contains(tr.Content, "line 4 gamma") {
		t.Fatalf("expected no context lines by default, got:\n%s", tr.Content)
	}

	// 2. Configured grep.contextBefore=1, grep.contextAfter=2 via search.Setting.
	setSettingForTest(t, func(_ sdk.Context, key string) any {
		switch key {
		case "grep.contextBefore":
			return float64(1)
		case "grep.contextAfter":
			return float64(2)
		}
		return nil
	})
	res, err = runGrep(context.Background(), dir, map[string]any{
		"pattern": "NEEDLE_",
		"path":    "sample.txt",
	})
	if err != nil {
		t.Fatalf("context grep err: %v", err)
	}
	tr = res.(sdk.ToolResult)
	for _, want := range []string{
		" 2:line 2 beta",
		"*3:line 3 NEEDLE_ONE",
		" 4:line 4 gamma",
		" 5:line 5 delta",
		"...",
		" 7:line 7 zeta",
		"*8:line 8 NEEDLE_TWO",
		" 9:line 9 eta",
		" 10:line 10 theta",
	} {
		if !strings.Contains(tr.Content, want) {
			t.Fatalf("expected %q in grep output, got:\n%s", want, tr.Content)
		}
	}
	if strings.Contains(tr.Content, "line 1 alpha") || strings.Contains(tr.Content, "line 6 epsilon") {
		t.Fatalf("unexpected out-of-window lines in grep output:\n%s", tr.Content)
	}

	// 3. Explicit tool params override configured settings (e.g. context=0 disables settings).
	res, err = runGrep(context.Background(), dir, map[string]any{
		"pattern": "NEEDLE_",
		"path":    "sample.txt",
		"context": float64(0),
	})
	if err != nil {
		t.Fatalf("explicit context=0 grep err: %v", err)
	}
	tr = res.(sdk.ToolResult)
	if strings.Contains(tr.Content, "line 2 beta") || strings.Contains(tr.Content, "line 4 gamma") {
		t.Fatalf("expected explicit context=0 to override settings, got:\n%s", tr.Content)
	}

	// 4. Overlapping context between adjacent matches does not duplicate lines.
	setSettingForTest(t, func(_ sdk.Context, key string) any {
		switch key {
		case "grep.contextBefore":
			return float64(3)
		case "grep.contextAfter":
			return float64(3)
		}
		return nil
	})
	res, err = runGrep(context.Background(), dir, map[string]any{
		"pattern": "NEEDLE_",
		"path":    "sample.txt",
	})
	if err != nil {
		t.Fatalf("overlap grep err: %v", err)
	}
	tr = res.(sdk.ToolResult)
	if strings.Count(tr.Content, "line 5 delta") != 1 {
		t.Fatalf("expected line 5 delta once without duplication, got:\n%s", tr.Content)
	}
}

func TestGrepAndGlobEnabledSettingsWiring(t *testing.T) {
	lifecycle.ResetForTests()
	t.Cleanup(lifecycle.ResetForTests)

	defaultActive := []string{"read", "bash", "edit", "eval", "glob", "grep", "task", "write"}

	runCase := func(t *testing.T, sid string, settingFn func(sdk.Context, string) any) []string {
		t.Helper()
		dir := t.TempDir()
		ext := sdk.New("search-enabled-" + sid)
		Register(ext)
		h := startTestHost(t, ext, dir, sid)
		defer h.close()

		setSettingForTest(t, settingFn)
		h.mu.Lock()
		h.activeTools = slices.Clone(defaultActive)
		h.mu.Unlock()

		h.emit("session_start")
		return h.active()
	}

	// 1. Default (Setting == nil): both grep and glob remain active.
	got := runCase(t, "default-nil", nil)
	if !slices.Equal(got, defaultActive) {
		t.Fatalf("default active tools = %v, want %v", got, defaultActive)
	}

	// 2. grep.enabled=false: removes grep, keeps glob.
	got = runCase(t, "grep-off", func(_ sdk.Context, key string) any {
		if key == "grep.enabled" {
			return false
		}
		return nil
	})
	if slices.Contains(got, "grep") {
		t.Fatalf("expected grep to be deactivated when grep.enabled=false, got %v", got)
	}
	if !slices.Contains(got, "glob") {
		t.Fatalf("expected glob to remain active when only grep.enabled=false, got %v", got)
	}

	// 3. glob.enabled=false: removes glob, keeps grep.
	got = runCase(t, "glob-off", func(_ sdk.Context, key string) any {
		if key == "glob.enabled" {
			return false
		}
		return nil
	})
	if slices.Contains(got, "glob") {
		t.Fatalf("expected glob to be deactivated when glob.enabled=false, got %v", got)
	}
	if !slices.Contains(got, "grep") {
		t.Fatalf("expected grep to remain active when only glob.enabled=false, got %v", got)
	}

	// 4. Both grep.enabled=false and glob.enabled=false: removes both.
	got = runCase(t, "both-off", func(_ sdk.Context, key string) any {
		if key == "grep.enabled" || key == "glob.enabled" {
			return false
		}
		return nil
	})
	if slices.Contains(got, "grep") || slices.Contains(got, "glob") {
		t.Fatalf("expected both grep and glob to be deactivated, got %v", got)
	}
}

func TestGrepAndGlobMidSessionToggle(t *testing.T) {
	lifecycle.ResetForTests()
	t.Cleanup(lifecycle.ResetForTests)

	defaultActive := []string{"read", "grep", "glob", "bash"}
	dir := t.TempDir()
	ext := sdk.New("search-toggle")
	Register(ext)
	h := startTestHost(t, ext, dir, "sess-toggle")
	defer h.close()

	var toggleMu sync.Mutex
	grepOn := true
	globOn := true
	setSettingForTest(t, func(_ sdk.Context, key string) any {
		toggleMu.Lock()
		defer toggleMu.Unlock()
		switch key {
		case "grep.enabled":
			return grepOn
		case "glob.enabled":
			return globOn
		}
		return nil
	})
	h.mu.Lock()
	h.activeTools = slices.Clone(defaultActive)
	h.mu.Unlock()

	// Start with both enabled: no change.
	h.emit("session_start")
	if got := h.active(); !slices.Equal(got, defaultActive) {
		t.Fatalf("initial active = %v, want %v", got, defaultActive)
	}

	// Toggle grep off mid-session -> before_agent_start removes grep.
	toggleMu.Lock()
	grepOn = false
	toggleMu.Unlock()
	h.emit(string(sdk.EventBeforeAgentStart))
	if got := h.active(); slices.Contains(got, "grep") || !slices.Contains(got, "glob") {
		t.Fatalf("after grep=false, active = %v (handlers=%v)", got, h.allHandlerIDs)
	}

	// Toggle grep back on -> before_agent_start restores grep in canonical order.
	toggleMu.Lock()
	grepOn = true
	toggleMu.Unlock()
	h.emit(string(sdk.EventBeforeAgentStart))
	if got := h.active(); !slices.Contains(got, "grep") {
		t.Fatalf("after grep=true, expected grep restored, got %v", got)
	}
}
