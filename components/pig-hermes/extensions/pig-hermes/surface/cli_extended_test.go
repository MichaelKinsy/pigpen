package surface_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/clihook"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/cli"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/config"
	_ "github.com/MichaelKinsy/pigpen/pig-hermes/surface"
)

func dispatchCLI(stdin string, args ...string) (clihook.Result, string, string) {
	var stdout, stderr bytes.Buffer
	env := clihook.Env{
		Args:   args,
		Stdout: &stdout,
		Stderr: &stderr,
		Getenv: os.Getenv,
	}
	if stdin != "" {
		env.Stdin = strings.NewReader(stdin)
	}
	res := cli.Dispatch(env)
	return res, stdout.String(), stderr.String()
}

func setupIsolatedHermesEnv(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("HERMES_HOME", root)
	t.Setenv("HERMES_PROFILE", "")
	// Isolate PiG session discovery to a temp dir so host ~/.pig sessions don't leak into tests.
	t.Setenv("PIG_CODING_AGENT_SESSION_DIR", filepath.Join(root, "pig-sessions"))
	t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(root, "pig-agent"))
	t.Setenv("PIG_HOME", filepath.Join(root, "pig-home"))
	return root
}

func TestCLIExtended_SetupSubcommand(t *testing.T) {
	root := setupIsolatedHermesEnv(t)

	// Pre-populate config.yaml with an unrelated custom key so we verify backup + preservation.
	cfgPath := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("custom_keep: hello\nmodel:\n  provider: old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, out, errOut := dispatchCLI("",
		"setup", "--non-interactive",
		"--provider", "anthropic",
		"--model", "claude-sonnet-4-5",
		"--skin", "ares",
		"--telemetry", "on",
	)
	if !res.Handled || res.Exit != 0 {
		t.Fatalf("setup failed: res=%+v out=%q err=%q", res, out, errOut)
	}
	if !strings.Contains(out, "Hermes setup complete") || !strings.Contains(out, "Backup:") {
		t.Errorf("expected setup summary + backup hint, got: %q", out)
	}

	// Verify backup was created.
	backups, err := filepath.Glob(filepath.Join(root, "backups", "config.yaml.pre-setup.*"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("expected 1 pre-setup backup, got %v (err=%v)", backups, err)
	}

	// Verify config.yaml values and preserved unrelated key.
	cfg, err := config.Load(cfgPath, filepath.Join(root, ".env"))
	if err != nil {
		t.Fatalf("config.Load failed: %v", err)
	}
	if cfg.Provider != "anthropic" || cfg.Model != "claude-sonnet-4-5" || cfg.Skin != "ares" {
		t.Errorf("unexpected loaded config: provider=%q model=%q skin=%q", cfg.Provider, cfg.Model, cfg.Skin)
	}
	if cfg.Raw["custom_keep"] != "hello" {
		t.Errorf("expected unrelated key custom_keep=hello preserved, got %q", cfg.Raw["custom_keep"])
	}
	if cfg.Raw["telemetry.shared_metrics.enabled"] != "true" || cfg.Raw["telemetry.shared_metrics.send"] != "true" {
		t.Errorf("expected telemetry enabled=true/send=true, got %+v", cfg.Raw)
	}

	// Verify .env created with 0600 permissions on unix.
	envPath := filepath.Join(root, ".env")
	fi, err := os.Stat(envPath)
	if err != nil {
		t.Fatalf("expected .env to exist: %v", err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("expected .env mode 0600, got %o", fi.Mode().Perm())
	}

	// Test interactive section mode (terminal) + --reset drops custom_keep.
	res, out, errOut = dispatchCLI("docker\n", "setup", "terminal", "--reset")
	if !res.Handled || res.Exit != 0 {
		t.Fatalf("setup terminal --reset failed: res=%+v out=%q err=%q", res, out, errOut)
	}
	cfg2, err := config.Load(cfgPath, envPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.Raw["terminal.backend"] != "docker" {
		t.Errorf("expected terminal.backend=docker, got %q", cfg2.Raw["terminal.backend"])
	}
	if cfg2.Raw["custom_keep"] != "" {
		t.Errorf("expected --reset to clear custom_keep, got %q", cfg2.Raw["custom_keep"])
	}
}

func TestCLIExtended_ConfigSubcommand(t *testing.T) {
	root := setupIsolatedHermesEnv(t)

	// path & env-path
	res, out, _ := dispatchCLI("", "config", "path")
	if !res.Handled || res.Exit != 0 || strings.TrimSpace(out) != filepath.Join(root, "config.yaml") {
		t.Fatalf("config path unexpected: res=%+v out=%q", res, out)
	}
	res, out, _ = dispatchCLI("", "config", "env-path")
	if !res.Handled || res.Exit != 0 || strings.TrimSpace(out) != filepath.Join(root, ".env") {
		t.Fatalf("config env-path unexpected: res=%+v out=%q", res, out)
	}

	// set dotted key -> config.yaml
	res, out, errOut := dispatchCLI("", "config", "set", "model.default", "gpt-4o")
	if !res.Handled || res.Exit != 0 {
		t.Fatalf("config set dotted failed: res=%+v out=%q err=%q", res, out, errOut)
	}
	res, _, _ = dispatchCLI("", "config", "set", "display.skin", "matrix")
	if res.Exit != 0 {
		t.Fatalf("config set display.skin failed")
	}

	// get dotted key (plain and --json)
	res, out, _ = dispatchCLI("", "config", "get", "model.default")
	if res.Exit != 0 || strings.TrimSpace(out) != "gpt-4o" {
		t.Errorf("config get model.default got %q (exit %d)", out, res.Exit)
	}
	res, out, _ = dispatchCLI("", "config", "get", "model.default", "--json")
	if res.Exit != 0 || strings.TrimSpace(out) != `"gpt-4o"` {
		t.Errorf("config get --json got %q (exit %d)", out, res.Exit)
	}

	// set UPPER_SNAKE -> .env (and secret redaction on get / show)
	res, out, errOut = dispatchCLI("", "config", "set", "OPENAI_API_KEY", "sk-secret-123456789")
	if !res.Handled || res.Exit != 0 {
		t.Fatalf("config set env failed: res=%+v out=%q err=%q", res, out, errOut)
	}
	if strings.Contains(out, "sk-secret-123456789") {
		t.Errorf("expected secret redacted in set output, got %q", out)
	}
	res, out, _ = dispatchCLI("", "config", "get", "OPENAI_API_KEY")
	if res.Exit != 0 || strings.Contains(out, "123456") || !strings.Contains(out, "...") {
		t.Errorf("expected redacted secret from config get, got %q", out)
	}
	res, out, _ = dispatchCLI("", "config", "get", "OPENAI_API_KEY", "--raw")
	if res.Exit != 0 || strings.TrimSpace(out) != "sk-secret-123456789" {
		t.Errorf("expected raw secret with --raw, got %q", out)
	}

	// Reject dangerous env names
	for _, bad := range []string{"PATH", "LD_PRELOAD", "DYLD_INSERT_LIBRARIES", "PYTHONPATH", "HERMES_HOME", "HERMES_YOLO_MODE"} {
		res, _, errOut := dispatchCLI("", "config", "set", bad, "evil")
		if res.Exit == 0 {
			t.Errorf("expected dangerous env key %q to be rejected", bad)
		}
		if !strings.Contains(errOut, "dangerous") {
			t.Errorf("expected dangerous error for %q, got %q", bad, errOut)
		}
	}

	// show & check
	res, out, _ = dispatchCLI("", "config", "show")
	if res.Exit != 0 || !strings.Contains(out, "Model") || !strings.Contains(out, "gpt-4o") || strings.Contains(out, "sk-secret-123456789") {
		t.Errorf("config show unexpected: %q", out)
	}
	res, out, _ = dispatchCLI("", "config", "check")
	if res.Exit != 0 || !strings.Contains(out, "✓ config.yaml") || !strings.Contains(out, "✓ .env") {
		t.Errorf("config check unexpected: %q", out)
	}

	// unset both YAML and .env keys; missing key returns exit 1
	res, _, _ = dispatchCLI("", "config", "unset", "OPENAI_API_KEY")
	if res.Exit != 0 {
		t.Errorf("config unset env failed")
	}
	res, _, _ = dispatchCLI("", "config", "get", "OPENAI_API_KEY")
	if res.Exit != 1 {
		t.Errorf("expected exit 1 after unset env key, got %d", res.Exit)
	}
	res, _, _ = dispatchCLI("", "config", "unset", "model.default")
	if res.Exit != 0 {
		t.Errorf("config unset yaml failed")
	}
	res, _, _ = dispatchCLI("", "config", "get", "model.default")
	if res.Exit != 1 {
		t.Errorf("expected exit 1 after unset yaml key, got %d", res.Exit)
	}
}

func TestCLIExtended_ModelSubcommand(t *testing.T) {
	root := setupIsolatedHermesEnv(t)

	// --set provider/model and --effort
	res, out, errOut := dispatchCLI("", "model", "--set", "openai/gpt-4.1", "--effort", "high")
	if !res.Handled || res.Exit != 0 {
		t.Fatalf("model --set failed: res=%+v out=%q err=%q", res, out, errOut)
	}
	cfg, err := config.Load(filepath.Join(root, "config.yaml"), filepath.Join(root, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider != "openai" || cfg.Model != "gpt-4.1" || cfg.Raw["agent.reasoning_effort"] != "high" {
		t.Errorf("unexpected model config: provider=%q model=%q effort=%q", cfg.Provider, cfg.Model, cfg.Raw["agent.reasoning_effort"])
	}

	// --list shows current config + presets
	res, out, _ = dispatchCLI("", "model", "--list")
	if res.Exit != 0 || !strings.Contains(out, "github-copilot") || !strings.Contains(out, "anthropic") || !strings.Contains(out, "gpt-4.1") {
		t.Errorf("model --list unexpected: %q", out)
	}

	// Interactive preset selection via stdin ("2" -> anthropic)
	res, out, errOut = dispatchCLI("2\n", "model")
	if res.Exit != 0 {
		t.Fatalf("interactive model selection failed: out=%q err=%q", out, errOut)
	}
	cfg2, _ := config.Load(filepath.Join(root, "config.yaml"), filepath.Join(root, ".env"))
	if cfg2.Provider != "anthropic" || cfg2.Model != "claude-sonnet-4-5" {
		t.Errorf("expected preset 2 (anthropic/claude-sonnet-4-5), got %q/%q", cfg2.Provider, cfg2.Model)
	}
}

func TestCLIExtended_MCPSubcommand(t *testing.T) {
	_ = setupIsolatedHermesEnv(t)

	// Add HTTP server and stdio server
	res, out, errOut := dispatchCLI("", "mcp", "add", "docs", "--url", "https://mcp.example.com/sse")
	if !res.Handled || res.Exit != 0 {
		t.Fatalf("mcp add http failed: res=%+v out=%q err=%q", res, out, errOut)
	}
	stdioCmd := "sh"
	if runtime.GOOS == "windows" {
		stdioCmd = "cmd"
	}
	res, out, errOut = dispatchCLI("", "mcp", "add", "localtool", "--command", stdioCmd, "--args", "-c,echo hi")
	if !res.Handled || res.Exit != 0 {
		t.Fatalf("mcp add stdio failed: res=%+v out=%q err=%q", res, out, errOut)
	}

	// list shows 4-column table with both servers
	res, out, _ = dispatchCLI("", "mcp", "list")
	if res.Exit != 0 || !strings.Contains(out, "Transport") || !strings.Contains(out, "docs") || !strings.Contains(out, "localtool") {
		t.Errorf("mcp list unexpected: %q", out)
	}

	// test ok (http & stdio), fail (missing binary), unknown (exit 3)
	res, out, errOut = dispatchCLI("", "mcp", "test", "docs")
	if res.Exit != 0 {
		t.Errorf("mcp test docs failed: exit=%d out=%q err=%q", res.Exit, out, errOut)
	}
	res, out, errOut = dispatchCLI("", "mcp", "test", "localtool")
	if res.Exit != 0 {
		t.Errorf("mcp test localtool failed: exit=%d out=%q err=%q", res.Exit, out, errOut)
	}
	res, _, _ = dispatchCLI("", "mcp", "add", "broken", "--command", "definitely-not-a-real-binary-xyz-98765")
	if res.Exit != 0 {
		t.Fatalf("mcp add broken failed")
	}
	res, _, _ = dispatchCLI("", "mcp", "test", "broken")
	if res.Exit != 1 {
		t.Errorf("expected mcp test broken exit 1, got %d", res.Exit)
	}
	res, _, _ = dispatchCLI("", "mcp", "test", "nonexistent")
	if res.Exit != 3 {
		t.Errorf("expected mcp test unknown exit 3, got %d", res.Exit)
	}

	// remove
	res, out, errOut = dispatchCLI("", "mcp", "remove", "broken")
	if res.Exit != 0 {
		t.Fatalf("mcp remove failed: out=%q err=%q", out, errOut)
	}
	res, _, _ = dispatchCLI("", "mcp", "test", "broken")
	if res.Exit != 3 {
		t.Errorf("expected removed server to return exit 3, got %d", res.Exit)
	}
}

func TestCLIExtended_SessionsAndInsightsSubcommands(t *testing.T) {
	root := setupIsolatedHermesEnv(t)

	// Empty insights should handle missing DB cleanly.
	res, out, errOut := dispatchCLI("", "insights")
	if !res.Handled || res.Exit != 0 || !strings.Contains(out, "No session data") {
		t.Fatalf("empty insights unexpected: res=%+v out=%q err=%q", res, out, errOut)
	}

	// Write a PiG JSONL session inside the isolated session directory.
	sessDir := filepath.Join(root, "pig-sessions")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	jsonl := strings.Join([]string{
		`{"type":"session","id":"sess-cli-1","cwd":"/tmp/work","timestamp":"` + now + `"}`,
		`{"type":"model_change","modelId":"claude-sonnet-4","timestamp":"` + now + `"}`,
		`{"type":"session_info","name":"CLI Extended Test","timestamp":"` + now + `"}`,
		`{"type":"message","id":"m1","parentId":"","timestamp":"` + now + `","message":{"role":"user","content":"Help me check logs"}}`,
		`{"type":"message","id":"m2","parentId":"m1","timestamp":"` + now + `","message":{"role":"assistant","content":[{"type":"text","text":"Running bash"},{"type":"toolCall","id":"tc1","name":"bash","arguments":{"cmd":"ls"}}]}}`,
		`{"type":"message","id":"m3","parentId":"m2","timestamp":"` + now + `","message":{"role":"toolResult","toolCallId":"tc1","toolName":"bash","content":"ok"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(sessDir, "sess-cli-1.jsonl"), []byte(jsonl), 0o644); err != nil {
		t.Fatal(err)
	}

	// Optionally also create a minimal state.db if sqlite3 is available on PATH.
	if sqliteBin, err := exec.LookPath("sqlite3"); err == nil {
		dbPath := filepath.Join(root, "state.db")
		sql := `
CREATE TABLE sessions (id TEXT PRIMARY KEY, title TEXT, model TEXT, source TEXT, started_at REAL, ended_at REAL);
CREATE TABLE messages (id INTEGER PRIMARY KEY, session_id TEXT, role TEXT, content TEXT, tool_name TEXT, tool_calls TEXT, timestamp REAL);
INSERT INTO sessions VALUES ('hermes-db-1', 'Upstream DB Session', 'gpt-4o', 'cli', strftime('%s','now'), strftime('%s','now'));
INSERT INTO messages VALUES (1, 'hermes-db-1', 'user', 'Hello from SQLite', '', '', strftime('%s','now'));
`
		cmd := exec.Command(sqliteBin, dbPath, sql)
		if err := cmd.Run(); err != nil {
			t.Fatalf("failed to seed test state.db: %v", err)
		}
	}

	// sessions list
	res, out, errOut = dispatchCLI("", "sessions", "list", "--limit", "10")
	if !res.Handled || res.Exit != 0 || !strings.Contains(out, "sess-cli-1") || !strings.Contains(out, "CLI Extended Test") {
		t.Fatalf("sessions list unexpected: res=%+v out=%q err=%q", res, out, errOut)
	}

	// sessions stats
	res, out, _ = dispatchCLI("", "sessions", "stats")
	if res.Exit != 0 || !strings.Contains(out, "Total sessions:") || !strings.Contains(out, "Total messages:") || !strings.Contains(out, "Database size:") {
		t.Errorf("sessions stats unexpected: %q", out)
	}

	// sessions pin / pinned / rename / unpin
	res, _, _ = dispatchCLI("", "sessions", "pin", "sess-cli-1")
	if res.Exit != 0 {
		t.Fatalf("sessions pin failed")
	}
	res, out, _ = dispatchCLI("", "sessions", "rename", "sess-cli-1", "Renamed Session")
	if res.Exit != 0 || !strings.Contains(out, "Renamed Session") {
		t.Fatalf("sessions rename failed: %q", out)
	}
	res, out, _ = dispatchCLI("", "sessions", "pinned", "--json")
	if res.Exit != 0 {
		t.Fatalf("sessions pinned --json failed")
	}
	var pinned []map[string]any
	if err := json.Unmarshal([]byte(out), &pinned); err != nil || len(pinned) != 1 || pinned[0]["title"] != "Renamed Session" {
		t.Errorf("unexpected pinned json: %q (err=%v)", out, err)
	}

	// sessions export (md to file & json to stdout)
	exportPath := filepath.Join(root, "out.md")
	res, _, errOut = dispatchCLI("", "sessions", "export", "sess-cli-1", "--format", "md", "--out", exportPath)
	if res.Exit != 0 {
		t.Fatalf("sessions export md failed: %q", errOut)
	}
	mdRaw, err := os.ReadFile(exportPath)
	if err != nil || !strings.Contains(string(mdRaw), "Help me check logs") {
		t.Errorf("exported md missing content: %q (err=%v)", string(mdRaw), err)
	}
	res, out, _ = dispatchCLI("", "sessions", "export", "sess-cli-1", "--format", "json")
	if res.Exit != 0 || !strings.Contains(out, `"id": "sess-cli-1"`) {
		t.Errorf("sessions export json unexpected: %q", out)
	}

	// insights with populated sessions
	res, out, errOut = dispatchCLI("", "insights", "--days", "30")
	if res.Exit != 0 || !strings.Contains(out, "Overview") || !strings.Contains(out, "Models Used") || !strings.Contains(out, "Top Tools") || !strings.Contains(out, "Activity Patterns") || !strings.Contains(out, "█") {
		t.Errorf("insights output unexpected: out=%q err=%q", out, errOut)
	}

	// sessions unpin & delete
	res, _, _ = dispatchCLI("", "sessions", "unpin", "sess-cli-1")
	if res.Exit != 0 {
		t.Fatalf("sessions unpin failed")
	}
	res, _, _ = dispatchCLI("", "sessions", "delete", "sess-cli-1")
	if res.Exit != 0 {
		t.Fatalf("sessions delete failed")
	}
	res, out, _ = dispatchCLI("", "sessions", "list", "--source", "pig")
	if res.Exit != 0 || strings.Contains(out, "sess-cli-1") {
		t.Errorf("expected deleted session absent from list, got: %q", out)
	}
}

func TestCLIExtended_LogsSubcommand(t *testing.T) {
	root := setupIsolatedHermesEnv(t)

	logsDir := filepath.Join(root, "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	agentLog := strings.Join([]string{
		"2026-11-27 10:00:01 INFO startup complete",
		"2026-11-27 10:00:02 WARNING slow response",
		"2026-11-27 10:00:03 ERROR connection failed",
		"2026-11-27 10:00:04 INFO recovered",
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(logsDir, "agent.log"), []byte(agentLog), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logsDir, "mcp-stderr.log"), []byte("2026-11-27 10:01:00 ERROR mcp timeout\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// logs list
	res, out, errOut := dispatchCLI("", "logs", "list")
	if !res.Handled || res.Exit != 0 || !strings.Contains(out, "agent.log") || !strings.Contains(out, "mcp-stderr.log") {
		t.Fatalf("logs list unexpected: res=%+v out=%q err=%q", res, out, errOut)
	}

	// logs agent -n 2
	res, out, _ = dispatchCLI("", "logs", "agent", "-n", "2")
	if res.Exit != 0 || strings.Contains(out, "startup complete") || !strings.Contains(out, "ERROR connection failed") || !strings.Contains(out, "INFO recovered") {
		t.Errorf("logs agent -n 2 unexpected: %q", out)
	}

	// logs agent --level ERROR
	res, out, _ = dispatchCLI("", "logs", "agent", "--level", "ERROR")
	if res.Exit != 0 || !strings.Contains(out, "ERROR connection failed") || strings.Contains(out, "INFO recovered") {
		t.Errorf("logs agent --level ERROR unexpected: %q", out)
	}

	// logs mcp -> maps to mcp-stderr.log
	res, out, _ = dispatchCLI("", "logs", "mcp")
	if res.Exit != 0 || !strings.Contains(out, "mcp timeout") {
		t.Errorf("logs mcp unexpected: %q", out)
	}

	// unknown log & missing file -> exit 1
	res, _, _ = dispatchCLI("", "logs", "unknownlog")
	if res.Exit != 1 {
		t.Errorf("expected unknown log exit 1, got %d", res.Exit)
	}
	res, _, _ = dispatchCLI("", "logs", "gateway")
	if res.Exit != 1 {
		t.Errorf("expected missing gateway.log exit 1, got %d", res.Exit)
	}
}

func TestCLIExtended_YAMLIndentlessQuotingAndFileModes(t *testing.T) {
	root := setupIsolatedHermesEnv(t)
	cfgPath := filepath.Join(root, "config.yaml")
	envPath := filepath.Join(root, ".env")

	// Pre-populate config.yaml with indentless list, empty map {}, special scalars, and quoted bool/numeric strings.
	initialYAML := strings.Join([]string{
		"model:",
		"  provider: openai",
		"  default: gpt-4o",
		"command_allowlist:",
		"- \"rm -rf ./cache\"",
		"- \"git clean -fd\"",
		"mcp_servers: {}",
		`note_at: "@mentions"`,
		`note_colon: "a: b"`,
		`str_bool: "true"`,
		`str_num: "1234"`,
	}, "\n") + "\n"
	if err := os.WriteFile(cfgPath, []byte(initialYAML), 0o600); err != nil {
		t.Fatal(err)
	}

	// Modify an unrelated key via `config set` and verify nothing is wiped or corrupted.
	res, _, errOut := dispatchCLI("", "config", "set", "display.skin", "ares")
	if !res.Handled || res.Exit != 0 {
		t.Fatalf("config set failed: err=%q", errOut)
	}

	rawYAML, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	yamlText := string(rawYAML)
	for _, want := range []string{
		"provider: openai",
		"default: gpt-4o",
		"rm -rf ./cache",
		"git clean -fd",
		"mcp_servers: {}",
		`note_at: "@mentions"`,
		`note_colon: "a: b"`,
		`str_bool: "true"`,
		`str_num: "1234"`,
		"skin: ares",
	} {
		if !strings.Contains(yamlText, want) {
			t.Errorf("expected %q preserved in config.yaml, got:\n%s", want, yamlText)
		}
	}

	if runtime.GOOS != "windows" {
		fi, err := os.Stat(cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("expected config.yaml mode 0600 preserved, got %o", fi.Mode().Perm())
		}
	}

	// Verify setup backup uses 0600 (not 0644) and new config.yaml defaults to 0600.
	res, _, errOut = dispatchCLI("", "setup", "--non-interactive", "--provider", "anthropic", "--model", "claude-sonnet-4-5")
	if !res.Handled || res.Exit != 0 {
		t.Fatalf("setup failed: err=%q", errOut)
	}
	if runtime.GOOS != "windows" {
		backups, _ := filepath.Glob(filepath.Join(root, "backups", "config.yaml.pre-setup.*"))
		if len(backups) != 1 {
			t.Fatalf("expected 1 backup, got %v", backups)
		}
		bfi, err := os.Stat(backups[0])
		if err != nil {
			t.Fatal(err)
		}
		if bfi.Mode().Perm() != 0o600 {
			t.Errorf("expected backup mode 0600, got %o", bfi.Mode().Perm())
		}
	}

	// Verify .env preserves untouched lines verbatim (including export prefix, quotes, inline comments).
	initialEnv := strings.Join([]string{
		"# Custom header",
		`export KEEP_EXPORT='single quoted' # keep comment`,
		`KEEP_RAW="hello world"`,
		`CHANGE_ME=old`,
	}, "\n") + "\n"
	if err := os.WriteFile(envPath, []byte(initialEnv), 0o600); err != nil {
		t.Fatal(err)
	}
	res, _, errOut = dispatchCLI("", "config", "set", "CHANGE_ME", "newval")
	if !res.Handled || res.Exit != 0 {
		t.Fatalf("config set CHANGE_ME failed: err=%q", errOut)
	}
	rawEnv, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	envText := string(rawEnv)
	for _, want := range []string{
		"# Custom header",
		`export KEEP_EXPORT='single quoted' # keep comment`,
		`KEEP_RAW="hello world"`,
		"CHANGE_ME=newval",
	} {
		if !strings.Contains(envText, want) {
			t.Errorf("expected %q verbatim in .env, got:\n%s", want, envText)
		}
	}
}
