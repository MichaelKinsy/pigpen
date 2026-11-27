package shell

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/approval"
)

func TestParseDirenvExport(t *testing.T) {
	diff := ParseDirenvExport(`{"FOO":"bar","EMPTY":"","DROP":null,"NUM":42}`)
	if diff.Set["FOO"] != "bar" {
		t.Fatalf("FOO = %q, want bar", diff.Set["FOO"])
	}
	if v, ok := diff.Set["EMPTY"]; !ok || v != "" {
		t.Fatalf("EMPTY = %q (ok=%v), want empty string", v, ok)
	}
	if _, ok := diff.Set["NUM"]; ok {
		t.Fatalf("non-string NUM should be ignored")
	}
	if len(diff.Unset) != 1 || diff.Unset[0] != "DROP" {
		t.Fatalf("Unset = %#v, want [DROP]", diff.Unset)
	}

	empty := ParseDirenvExport("")
	if len(empty.Set) != 0 || len(empty.Unset) != 0 {
		t.Fatalf("expected empty diff, got %#v", empty)
	}

	invalid := ParseDirenvExport("{bad json")
	if len(invalid.Set) != 0 || len(invalid.Unset) != 0 {
		t.Fatalf("expected empty diff on invalid json, got %#v", invalid)
	}
}

func TestFindEnvrcWalksUpAncestors(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	envrc := filepath.Join(root, ".envrc")
	if err := os.WriteFile(envrc, []byte("export FOO=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := FindEnvrc(nested)
	want, _ := filepath.Abs(envrc)
	if got != want {
		t.Fatalf("FindEnvrc(%s) = %q, want %q", nested, got, want)
	}
}

func TestDirenvStatCacheAndApplyInBashTool(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake direnv script test")
	}
	ClearDirenvCachesForTests()
	prevLookPath := direnvLookPathFn
	prevNow := direnvNowFn
	prevSetting := Setting
	t.Cleanup(func() {
		direnvLookPathFn = prevLookPath
		direnvNowFn = prevNow
		Setting = prevSetting
		ClearDirenvCachesForTests()
	})

	dir := t.TempDir()
	envrc := filepath.Join(dir, ".envrc")
	if err := os.WriteFile(envrc, []byte("export OMP_DIRENV_VAR=hello_v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	countFile := filepath.Join(dir, "direnv-calls.txt")
	fakeDirenv := filepath.Join(dir, "fake-direnv")
	script := `#!/bin/sh
echo call >> ` + singleQuotePosix(countFile) + `
if [ "$1" = "export" ] && [ "$2" = "json" ]; then
	if grep -q hello_v2 .envrc; then
		printf '{"OMP_DIRENV_VAR":"hello_v2","OMP_DIRENV_REMOVE":null}\n'
	else
		printf '{"OMP_DIRENV_VAR":"hello_v1","OMP_DIRENV_REMOVE":null}\n'
	fi
	exit 0
fi
exit 1
`
	if err := os.WriteFile(fakeDirenv, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	direnvLookPathFn = func(name string) (string, error) {
		if name == "direnv" {
			return fakeDirenv, nil
		}
		return "", os.ErrNotExist
	}
	t.Setenv("OMP_DIRENV_REMOVE", "should_be_removed")

	baseNow := time.Now()
	direnvNowFn = func() time.Time { return baseNow }

	// 1. Default ("auto") loads direnv, sets OMP_DIRENV_VAR and unsets OMP_DIRENV_REMOVE.
	Setting = func(_ sdk.Context, key string) any {
		switch key {
		case "bash.direnv":
			return "auto"
		case "bash.direnvLoadTimeoutMs":
			return float64(5000)
		}
		return nil
	}
	rt := NewRuntime(t.TempDir())
	t.Cleanup(rt.DisposeSessions)
	cfg := DefaultConfig()
	cfg.AutoBackgroundEnabled = false
	cfg.WallTimeNoticeMinMS = -1
	tool := NewBashTool(cfg, rt)

	res, err := tool.Execute(sdk.Context{}, map[string]any{
		"command": `printf '%s|%s' "$OMP_DIRENV_VAR" "${OMP_DIRENV_REMOVE:-UNSET}"`,
		"cwd":     dir,
	})
	if err != nil {
		t.Fatalf("Execute 1 failed: %v", err)
	}
	tr := res.(sdk.ToolResult)
	if !strings.Contains(tr.Content, "hello_v1|UNSET") {
		t.Fatalf("expected direnv env applied, got %q", tr.Content)
	}

	// 2. Repeated call within warm TTL hits stat cache (zero extra subprocesses).
	res2, err := tool.Execute(sdk.Context{}, map[string]any{
		"command": `printf '%s|%s' "$OMP_DIRENV_VAR" "${OMP_DIRENV_REMOVE:-UNSET}"`,
		"cwd":     dir,
	})
	if err != nil {
		t.Fatalf("Execute 2 failed: %v", err)
	}
	if !strings.Contains(res2.(sdk.ToolResult).Content, "hello_v1|UNSET") {
		t.Fatalf("expected cached direnv env, got %q", res2.(sdk.ToolResult).Content)
	}
	callsRaw, err := os.ReadFile(countFile)
	if err != nil {
		t.Fatalf("read count file: %v", err)
	}
	callCount := len(strings.Split(strings.TrimSpace(string(callsRaw)), "\n"))
	if callCount != 1 {
		t.Fatalf("expected 1 direnv subprocess call (cached on 2nd), got %d", callCount)
	}

	// 3. Modifying .envrc invalidates the stat cache immediately (even within TTL).
	newContent := "export OMP_DIRENV_VAR=hello_v2 # changed size\n"
	if err := os.WriteFile(envrc, []byte(newContent), 0o644); err != nil {
		t.Fatal(err)
	}
	futureTime := baseNow.Add(2 * time.Second)
	_ = os.Chtimes(envrc, futureTime, futureTime)

	res3, err := tool.Execute(sdk.Context{}, map[string]any{
		"command": `printf '%s' "$OMP_DIRENV_VAR"`,
		"cwd":     dir,
	})
	if err != nil {
		t.Fatalf("Execute 3 failed: %v", err)
	}
	if !strings.Contains(res3.(sdk.ToolResult).Content, "hello_v2") {
		t.Fatalf("expected updated direnv value hello_v2, got %q", res3.(sdk.ToolResult).Content)
	}

	// 4. Setting bash.direnv = "off" skips direnv entirely.
	Setting = func(_ sdk.Context, key string) any {
		if key == "bash.direnv" {
			return "off"
		}
		return nil
	}
	res4, err := tool.Execute(sdk.Context{}, map[string]any{
		"command": `printf '%s|%s' "${OMP_DIRENV_VAR:-NONE}" "${OMP_DIRENV_REMOVE:-UNSET}"`,
		"cwd":     dir,
	})
	if err != nil {
		t.Fatalf("Execute 4 failed: %v", err)
	}
	if !strings.Contains(res4.(sdk.ToolResult).Content, "NONE|should_be_removed") {
		t.Fatalf("expected direnv skipped when off, got %q", res4.(sdk.ToolResult).Content)
	}
}

func TestDirenvGracefulOnMissingBinaryAndTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake direnv script test")
	}
	ClearDirenvCachesForTests()
	prevLookPath := direnvLookPathFn
	t.Cleanup(func() {
		direnvLookPathFn = prevLookPath
		ClearDirenvCachesForTests()
	})

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".envrc"), []byte("export A=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Missing binary -> returns nil cleanly.
	direnvLookPathFn = func(string) (string, error) { return "", os.ErrNotExist }
	if diff := LoadDirenvEnv(context.Background(), dir, 500*time.Millisecond); diff != nil {
		t.Fatalf("expected nil diff when direnv missing, got %#v", diff)
	}

	// Slow binary -> times out gracefully and returns nil.
	ClearDirenvCachesForTests()
	slowBin := filepath.Join(dir, "slow-direnv")
	if err := os.WriteFile(slowBin, []byte("#!/bin/sh\nsleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	direnvLookPathFn = func(string) (string, error) { return slowBin, nil }
	start := time.Now()
	if diff := LoadDirenvEnv(context.Background(), dir, 80*time.Millisecond); diff != nil {
		t.Fatalf("expected nil diff on timeout, got %#v", diff)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("expected direnv timeout to abort promptly, took %v", time.Since(start))
	}
}

func TestBashAllowCompoundCommands(t *testing.T) {
	prevSetting := Setting
	prevSession := SessionSetting
	prevApprSetting := approval.Setting
	prevApprSession := approval.SessionSetting
	t.Cleanup(func() {
		Setting = prevSetting
		SessionSetting = prevSession
		approval.Setting = prevApprSetting
		approval.SessionSetting = prevApprSession
	})

	opPatterns := []any{
		map[string]any{"match": "git status*", "approval": "allow"},
		map[string]any{"match": "go test*", "approval": "allow"},
		map[string]any{"match": "rm -rf*", "approval": "deny"},
		map[string]any{"match": "git push*", "approval": "prompt"},
	}

	// 1. Default (bash.allowCompoundCommands=false): && chain is NOT allowed.
	Setting = func(_ sdk.Context, key string) any {
		if key == "bash.patterns" {
			return opPatterns
		}
		return nil
	}
	SessionSetting = Setting
	dec := EvaluateBashToolDecision(sdk.Context{}, map[string]any{"command": "git status && go test ./..."})
	if dec.Policy == approval.PolicyAllow {
		t.Fatalf("expected && chain not allowed when bash.allowCompoundCommands is default false, got %#v", dec)
	}

	// 2. Enabled in operator + session: literal && chain where all segments match allow -> allowed.
	Setting = func(_ sdk.Context, key string) any {
		switch key {
		case "bash.patterns":
			return opPatterns
		case "bash.allowCompoundCommands":
			return true
		}
		return nil
	}
	SessionSetting = Setting
	dec = EvaluateBashToolDecision(sdk.Context{}, map[string]any{"command": `git status --short && go test -run "a && b" ./...`})
	if dec.Policy != approval.PolicyAllow || dec.Tier != approval.TierWrite {
		t.Fatalf("expected allowed literal && chain (with quoted && inside arg), got %#v", dec)
	}

	// 3. Any segment denied -> deny wins.
	dec = EvaluateBashToolDecision(sdk.Context{}, map[string]any{"command": "git status && rm -rf /tmp/x"})
	if dec.Policy != approval.PolicyDeny || !dec.Override {
		t.Fatalf("expected deny on compound chain with denied segment, got %#v", dec)
	}

	// 4. Any segment prompted -> prompt wins.
	dec = EvaluateBashToolDecision(sdk.Context{}, map[string]any{"command": "git status && git push origin main"})
	if dec.Policy != approval.PolicyPrompt || !dec.Override {
		t.Fatalf("expected prompt on compound chain with prompted segment, got %#v", dec)
	}

	// 5. Any unmatched segment -> falls back to exec (not allow).
	dec = EvaluateBashToolDecision(sdk.Context{}, map[string]any{"command": "git status && echo hi"})
	if dec.Policy == approval.PolicyAllow || dec.Tier != approval.TierExec {
		t.Fatalf("expected exec tier when one segment is unmatched, got %#v", dec)
	}

	// 6. Disallowed operators/syntax (`;`, `||`, `|`, `$(...)`, backticks, `&`, stateful `cd`) MUST NOT be allowed.
	for _, cmd := range []string{
		"git status; go test ./...",
		"git status || go test ./...",
		"git status | go test ./...",
		"git status && go test $(whoami)",
		"git status && go test `whoami`",
		"git status & go test ./...",
		"cd /tmp && git status",
		"git status && sh -c 'go test'",
	} {
		dec = EvaluateBashToolDecision(sdk.Context{}, map[string]any{"command": cmd})
		if dec.Policy == approval.PolicyAllow {
			t.Fatalf("expected %q NOT to be allowed under bash.allowCompoundCommands, got %#v", cmd, dec)
		}
	}

	// 7. Two-view security invariant: project layer enabling bash.allowCompoundCommands
	// MUST NOT override operator default (false).
	Setting = func(_ sdk.Context, key string) any {
		switch key {
		case "bash.patterns":
			return opPatterns
		case "bash.allowCompoundCommands":
			return false
		}
		return nil
	}
	SessionSetting = func(_ sdk.Context, key string) any {
		switch key {
		case "bash.patterns":
			return opPatterns
		case "bash.allowCompoundCommands":
			return true
		}
		return nil
	}
	dec = EvaluateBashToolDecision(sdk.Context{}, map[string]any{"command": "git status && go test ./..."})
	if dec.Policy == approval.PolicyAllow {
		t.Fatalf("project bash.allowCompoundCommands=true MUST NOT loosen operator false, got %#v", dec)
	}

	// 8. Two-view tightening: project deny on second segment overrides operator allow on both.
	Setting = func(_ sdk.Context, key string) any {
		switch key {
		case "bash.patterns":
			return opPatterns
		case "bash.allowCompoundCommands":
			return true
		}
		return nil
	}
	SessionSetting = func(_ sdk.Context, key string) any {
		switch key {
		case "bash.patterns":
			return append([]any{map[string]any{"match": "go test*", "approval": "deny"}}, opPatterns...)
		case "bash.allowCompoundCommands":
			return true
		}
		return nil
	}
	dec = EvaluateBashToolDecision(sdk.Context{}, map[string]any{"command": "git status && go test ./..."})
	if dec.Policy != approval.PolicyDeny {
		t.Fatalf("expected project deny to tighten operator compound allow, got %#v", dec)
	}

	// 9. formatCompoundApprovalDetails formats per-segment coverage rows.
	authRules := parseBashPatternRules(opPatterns)
	rows := formatCompoundApprovalDetails("git status && git push origin main", authRules, nil, authRules, nil)
	if len(rows) != 3 || !strings.Contains(rows[1], "✓ git status") || !strings.Contains(rows[2], "? git push origin main (prompt: git push*)") {
		t.Fatalf("unexpected compound approval detail rows: %#v", rows)
	}
}

func TestDirenvTimeoutKillsChildTreeAndBoundsWait(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell script fake direnv")
	}
	ClearDirenvCachesForTests()
	binDir := t.TempDir()
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, ".envrc"), []byte("export TEST_SLOW=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Fake direnv that spawns a background child holding stderr open, then sleeps.
	fakeBin := filepath.Join(binDir, "direnv")
	script := "#!/bin/sh\n(sleep 60) &\nsleep 60\n"
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Cleanup(ClearDirenvCachesForTests)

	start := time.Now()
	diff := LoadDirenvEnv(context.Background(), workDir, 200*time.Millisecond)
	elapsed := time.Since(start)
	if diff != nil {
		t.Fatalf("expected nil diff on timed-out direnv, got %+v", diff)
	}
	if elapsed > 4*time.Second {
		t.Fatalf("timed-out direnv blocked for %v (want <= 4s)", elapsed)
	}
}
