package settings

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
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

func TestSchemaCountMatchesTag(t *testing.T) {
	all := Schema()
	// omp v19.1.1 (31ffee513f) registers exactly 644 settings across 115 namespaces.
	const wantCount = 644
	if len(all) != wantCount {
		t.Fatalf("Schema() returned %d keys, want %d (omp v19.1.1)", len(all), wantCount)
	}
	ns := NamespaceCounts()
	const wantNamespaces = 115
	if len(ns) != wantNamespaces {
		t.Fatalf("NamespaceCounts() returned %d namespaces, want %d", len(ns), wantNamespaces)
	}
	if ns["(top-level)"] != 47 {
		t.Errorf("top-level count = %d, want 47", ns["(top-level)"])
	}
	if ns["advisor"] != 8 {
		t.Errorf("advisor count = %d, want 8", ns["advisor"])
	}
	if ns["task"] != 35 {
		t.Errorf("task count = %d, want 35", ns["task"])
	}

	check := func(id string, wantType ValueType) *Entry {
		t.Helper()
		e, ok := Lookup(id)
		if !ok {
			t.Fatalf("missing key %q in schema", id)
		}
		if e.Type != wantType {
			t.Errorf("%s type = %q, want %q", id, e.Type, wantType)
		}
		return e
	}
	askTimeout := check("ask.timeout", TypeNumber)
	if !askTimeout.Honoured {
		t.Errorf("ask.timeout should be marked Honoured")
	}
	if askTimeout.Default != float64(0) {
		t.Errorf("ask.timeout default = %#v, want 0", askTimeout.Default)
	}
	syncBacklog := check("advisor.syncBacklog", TypeEnum)
	if !reflect.DeepEqual(syncBacklog.Enum, []string{"off", "1", "3", "5", "strict"}) {
		t.Errorf("advisor.syncBacklog enum = %v", syncBacklog.Enum)
	}
	brokerToken := check("auth.broker.token", TypeString)
	if !brokerToken.Credential || brokerToken.EnvName != "OMP_AUTH_BROKER_TOKEN" {
		t.Errorf("auth.broker.token credential=%v env=%q", brokerToken.Credential, brokerToken.EnvName)
	}
	bashPatterns := check("bash.patterns", TypeArray)
	if bashPatterns.ArrayMerge != "prepend" || bashPatterns.PathScoped == nil {
		t.Errorf("bash.patterns merge=%q pathScoped=%+v", bashPatterns.ArrayMerge, bashPatterns.PathScoped)
	}
	searxngEndpoint := check("searxng.endpoint", TypeString)
	if searxngEndpoint.EnvName != "SEARXNG_ENDPOINT" || searxngEndpoint.EnvFallback != "blank" {
		t.Errorf("searxng.endpoint env=%q fallback=%q", searxngEndpoint.EnvName, searxngEndpoint.EnvFallback)
	}
}

// ── Store precedence, isolation, trust, persistence & symlinks ───────────────

// upstream: packages/coding-agent/test/config/settings-registry.test.ts: "ranks a runtime override over an override env, that env over persisted layers, and a fallback env only over the default"
func TestRegistryPrecedenceAndEnvFallbacks(t *testing.T) {
	t.Cleanup(ResetForTests)
	t.Setenv("PI_EDIT_FUZZY", "0")
	t.Setenv("SEARXNG_ENDPOINT", "https://env.example")

	store, err := NewIsolated(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("edit.fuzzyMatch", true, true); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("searxng.endpoint", "https://cfg.example", true); err != nil {
		t.Fatal(err)
	}
	if store.Bool("edit.fuzzyMatch") != false || store.Provenance("edit.fuzzyMatch") != ProvenanceEnv {
		t.Errorf("edit.fuzzyMatch = (%v, %q), want (false, env)", store.Bool("edit.fuzzyMatch"), store.Provenance("edit.fuzzyMatch"))
	}
	if store.String("searxng.endpoint") != "https://cfg.example" || store.Provenance("searxng.endpoint") != ProvenanceGlobal {
		t.Errorf("searxng.endpoint = (%q, %q), want (https://cfg.example, global)", store.String("searxng.endpoint"), store.Provenance("searxng.endpoint"))
	}

	// Runtime override outranks non-fallback env.
	if err := store.Set("edit.fuzzyMatch", true, false); err != nil {
		t.Fatal(err)
	}
	if store.Bool("edit.fuzzyMatch") != true || store.Provenance("edit.fuzzyMatch") != ProvenanceRuntime {
		t.Errorf("edit.fuzzyMatch after override = (%v, %q), want (true, runtime)", store.Bool("edit.fuzzyMatch"), store.Provenance("edit.fuzzyMatch"))
	}
	store.ClearOverride("edit.fuzzyMatch")
	if store.Bool("edit.fuzzyMatch") != false {
		t.Error("edit.fuzzyMatch should return to env false after ClearOverride")
	}

	bare, _ := NewIsolated(nil)
	if bare.String("searxng.endpoint") != "https://env.example" || bare.Provenance("searxng.endpoint") != ProvenanceEnv {
		t.Errorf("bare searxng.endpoint = (%q, %q)", bare.String("searxng.endpoint"), bare.Provenance("searxng.endpoint"))
	}
}

// upstream: packages/coding-agent/test/config/settings-registry.test.ts: "parses a boolean env var like parseFlag: empty is unset, unlisted text is false"
func TestRegistryBooleanEnvParseFlag(t *testing.T) {
	store, _ := NewIsolated(nil)
	_ = store.Set("eval.py", false, true)

	t.Setenv("PI_PY", "")
	if store.Bool("eval.py") != false || store.Provenance("eval.py") != ProvenanceGlobal {
		t.Errorf("empty PI_PY should defer to global: (%v, %q)", store.Bool("eval.py"), store.Provenance("eval.py"))
	}
	t.Setenv("PI_PY", "y")
	if store.Bool("eval.py") != true {
		t.Error("PI_PY=y should be true")
	}
	t.Setenv("PI_PY", "disabled")
	bare, _ := NewIsolated(nil)
	if bare.Bool("eval.py") != false || bare.Provenance("eval.py") != ProvenanceEnv {
		t.Errorf("PI_PY=disabled should be (false, env), got (%v, %q)", bare.Bool("eval.py"), bare.Provenance("eval.py"))
	}
}

// upstream: packages/coding-agent/test/config/settings-registry.test.ts: "treats a configured null as unset for fallback env vars and provenance"
func TestRegistryNullTreatedAsUnset(t *testing.T) {
	t.Setenv("SEARXNG_BASIC_PASSWORD", "env-secret")
	cleared, err := NewIsolated(map[string]any{"searxng.basicPassword": nil})
	if err != nil {
		t.Fatal(err)
	}
	if cleared.String("searxng.basicPassword") != "env-secret" || cleared.Provenance("searxng.basicPassword") != ProvenanceEnv {
		t.Errorf("with fallback env: (%q, %q)", cleared.String("searxng.basicPassword"), cleared.Provenance("searxng.basicPassword"))
	}
	t.Setenv("SEARXNG_BASIC_PASSWORD", "")
	os.Unsetenv("SEARXNG_BASIC_PASSWORD")
	if cleared.Provenance("searxng.basicPassword") != ProvenanceDefault {
		t.Errorf("without env: provenance = %q, want default", cleared.Provenance("searxng.basicPassword"))
	}
}

// upstream: packages/coding-agent/test/config/settings-registry.test.ts: "migrates, normalizes, and validates constructor overrides like handle writes"
func TestRegistryMigrationsNormalizersAndValidators(t *testing.T) {
	s1, err := NewIsolated(map[string]any{"queueMode": "all"})
	if err != nil {
		t.Fatal(err)
	}
	if s1.String("steeringMode") != "all" {
		t.Errorf("queueMode migration: steeringMode = %q, want all", s1.String("steeringMode"))
	}

	s2, err := NewIsolated(map[string]any{"providers.maxInFlightRequests": map[string]any{"openai": 2.7}})
	if err != nil {
		t.Fatal(err)
	}
	if got := s2.Any("providers.maxInFlightRequests").(map[string]any)["openai"]; got != float64(2) {
		t.Errorf("maxInFlightRequests = %#v, want 2", got)
	}

	if _, err := NewIsolated(map[string]any{"providers.maxInFlightRequests": map[string]any{"openai": 0}}); err == nil || !strings.Contains(err.Error(), "Provider request limits must be positive numbers: openai") {
		t.Errorf("expected maxInFlightRequests validation error, got %v", err)
	}
	if _, err := NewIsolated(map[string]any{"temperature": "hot"}); err == nil || !strings.Contains(err.Error(), "Invalid value for temperature") {
		t.Errorf("expected temperature type error, got %v", err)
	}

	// A6: spotlight error text ("from 0 to 1"), HINDSIGHT_* parseInt prefix, PI_EDIT_FUZZY_THRESHOLD parseFloat prefix, theme migration.
	if err := s1.Set("spotlight.experiment.holdout", 2, false); err == nil || !strings.Contains(err.Error(), "from 0 to 1") {
		t.Errorf("spotlight error = %v, want 'from 0 to 1'", err)
	}
	t.Setenv("HINDSIGHT_REQUEST_TIMEOUT_MS", "5000ms")
	if got := s1.Int("hindsight.requestTimeoutMs"); got != 5000 {
		t.Errorf("HINDSIGHT_REQUEST_TIMEOUT_MS='5000ms' -> %d, want 5000", got)
	}
	t.Setenv("PI_EDIT_FUZZY_THRESHOLD", "0.85x")
	if got := s1.Float("edit.fuzzyThreshold"); got != 0.85 {
		t.Errorf("PI_EDIT_FUZZY_THRESHOLD='0.85x' -> %v, want 0.85", got)
	}

	// Theme migration: flat "light"/"dark" deleted so defaults apply; custom theme lands in one slot only.
	migDark := migrateRawSettings(toOrderedMap(map[string]any{"theme": "dark"}))
	if _, has := migDark.Get("theme"); has {
		t.Errorf("flat 'dark' should be deleted, got %#v", migDark.ToPlainMap())
	}
	migCustom := migrateRawSettings(toOrderedMap(map[string]any{"theme": "dracula"}))
	tm, _ := migCustom.Get("theme")
	if !reflect.DeepEqual(toPlainValue(tm), map[string]any{"dark": "dracula"}) {
		t.Errorf("flat custom theme = %#v, want {dark: dracula}", toPlainValue(tm))
	}

	// A5: JS Number() parsing accepts 0x10 and formats 1e21 like JS.
	numEntry, _ := Lookup("ask.timeout")
	parsedHex, err := numEntry.ParseText("0x10")
	if err != nil || parsedHex != float64(16) {
		t.Errorf("ParseText(0x10) = (%#v, %v), want (16, nil)", parsedHex, err)
	}
	if got := formatValue(numEntry, 1e21); got != "1e+21" {
		t.Errorf("formatValue(1e21) = %q, want 1e+21", got)
	}
}

func TestStorePrecedenceIsolationAndTrust(t *testing.T) {
	t.Cleanup(ResetForTests)
	tmp := t.TempDir()
	agentDir := filepath.Join(tmp, "agent")
	projectDir := filepath.Join(tmp, "project")
	overlay1 := filepath.Join(tmp, "overlay1.yml")
	overlay2 := filepath.Join(tmp, "overlay2.yml")

	_ = os.MkdirAll(agentDir, 0o755)
	_ = os.MkdirAll(filepath.Join(projectDir, ".omp"), 0o755)
	_ = os.MkdirAll(filepath.Join(projectDir, ".pig"), 0o755)

	globalYAML := `
theme:
  dark: nord
  light: snow
skills:
  enableClaudeUser: true
tools:
  approvalMode: write
  approval:
    bash: prompt
    read: allow
bash:
  patterns:
    - match: "git status*"
      approval: allow
`
	_ = os.WriteFile(filepath.Join(agentDir, "config.yml"), []byte(globalYAML), 0o600)

	// B4 + integrator decision: .pig/settings.json and .omp/* MUST be ignored.
	_ = os.WriteFile(filepath.Join(projectDir, ".pig", "settings.json"), []byte(`{"skills":["./skills"],"theme":"dark","steeringMode":"all"}`), 0o600)
	_ = os.WriteFile(filepath.Join(projectDir, ".omp", "config.yml"), []byte("theme:\n  dark: from-real-omp\n"), 0o600)
	_ = os.WriteFile(filepath.Join(projectDir, ".omp", "settings.json"), []byte(`{"theme":{"dark":"from-omp-json"}}`), 0o600)

	// Only <cwd>/.pig/config.yml is read as the project layer.
	pigProjectYAML := `
theme:
  dark: tokyo
tools:
  approval:
    bash: allow
bash:
  patterns:
    - match: "go test*"
      approval: allow
`
	_ = os.WriteFile(filepath.Join(projectDir, ".pig", "config.yml"), []byte(pigProjectYAML), 0o600)
	_ = os.WriteFile(overlay1, []byte("compaction:\n  enabled: false\n  thresholdPercent: 75\n"), 0o600)
	_ = os.WriteFile(overlay2, []byte("compaction:\n  thresholdPercent: 82\n"), 0o600)

	// PI_CONFIG_FILES (omp's env var) must NOT be read; PIG_CONFIG_FILES is read.
	t.Setenv("PI_CONFIG_FILES", "/nonexistent/omp-only.yml")
	t.Setenv(EnvConfigFiles, overlay1)

	// B5: Untrusted project must NOT apply .pig/config.yml.
	untrusted, err := NewStore(StoreOptions{
		AgentDir:       agentDir,
		Cwd:            projectDir,
		ProjectTrusted: false,
		HomeDir:        filepath.Join(tmp, "home"),
		ConfigFiles:    []string{overlay2},
	})
	if err != nil {
		t.Fatalf("untrusted NewStore: %v", err)
	}
	if untrusted.String("theme.dark") != "nord" {
		t.Errorf("untrusted project leaked theme.dark = %q, want nord", untrusted.String("theme.dark"))
	}

	// Home dir exclusion: even if trusted, cwd == home does not load project config.
	atHome, err := NewStore(StoreOptions{
		AgentDir:       agentDir,
		Cwd:            projectDir,
		ProjectTrusted: true,
		HomeDir:        projectDir,
	})
	if err != nil {
		t.Fatalf("atHome NewStore: %v", err)
	}
	if atHome.String("theme.dark") != "nord" {
		t.Errorf("cwd==home loaded project theme.dark = %q, want nord", atHome.String("theme.dark"))
	}

	// Trusted project outside home loads .pig/config.yml, ignores .omp/* and .pig/settings.json.
	store, err := NewStore(StoreOptions{
		AgentDir:       agentDir,
		Cwd:            projectDir,
		ProjectTrusted: true,
		HomeDir:        filepath.Join(tmp, "home"),
		ConfigFiles:    []string{overlay2},
	})
	if err != nil {
		t.Fatalf("trusted NewStore: %v", err)
	}
	if store.String("theme.dark") != "tokyo" || store.Provenance("theme.dark") != ProvenanceProject {
		t.Errorf("theme.dark = (%q, %q), want (tokyo, project)", store.String("theme.dark"), store.Provenance("theme.dark"))
	}
	if store.String("theme.light") != "snow" {
		t.Errorf("theme.light = %q, want snow (not clobbered by .pig/settings.json)", store.String("theme.light"))
	}
	if !store.Bool("skills.enableClaudeUser") {
		t.Error("skills.enableClaudeUser clobbered by .pig/settings.json skills array")
	}
	if store.Int("compaction.thresholdPercent") != 82 || store.Provenance("compaction.thresholdPercent") != ProvenanceOverlay {
		t.Errorf("overlay thresholdPercent = (%d, %q), want (82, overlay)", store.Int("compaction.thresholdPercent"), store.Provenance("compaction.thresholdPercent"))
	}

	// Operator view (GetOperator / grantAuthority) excludes project layer (B5).
	opPatterns := store.Any("bash.patterns").([]any)
	if len(opPatterns) != 2 {
		t.Fatalf("merged bash.patterns len = %d, want 2", len(opPatterns))
	}
	opOnly, _ := store.GetOperator("bash.patterns")
	if len(opOnly.([]any)) != 1 || opOnly.([]any)[0].(map[string]any)["match"] != "git status*" {
		t.Errorf("GetOperator(bash.patterns) = %#v, want only global rule", opOnly)
	}
}

// B1: /save re-reads the file on disk, changes only the one key, preserves external edits,
// concurrent sessions' saves, and existing file permissions.
// upstream: packages/coding-agent/test/settings-manager.test.ts: "merges a pending local change with a later disjoint external edit"
func TestSavePreservesExternalEditsConcurrentSessionsAndMode(t *testing.T) {
	tmp := t.TempDir()
	agentDir := filepath.Join(tmp, "agent")
	_ = os.MkdirAll(agentDir, 0o755)
	cfgPath := filepath.Join(agentDir, "config.yml")
	if err := os.WriteFile(cfgPath, []byte("theme:\n  dark: nord\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(cfgPath, 0o644)
	}

	sessA, err := NewStore(StoreOptions{AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	sessB, err := NewStore(StoreOptions{AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}

	// External edit adds read.defaultLimit after both sessions loaded.
	if err := os.WriteFile(cfgPath, []byte("theme:\n  dark: nord\nread:\n  defaultLimit: 500\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Session A saves advisor.enabled, then Session B saves todo.enabled.
	if err := sessA.Set("advisor.enabled", true, true); err != nil {
		t.Fatalf("sessA save: %v", err)
	}
	if err := sessB.Set("todo.enabled", false, true); err != nil {
		t.Fatalf("sessB save: %v", err)
	}

	// Reload fresh: all four keys must be present (external + sessA + sessB + original).
	fresh, err := NewStore(StoreOptions{AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	if fresh.String("theme.dark") != "nord" || fresh.Int("read.defaultLimit") != 500 ||
		!fresh.Bool("advisor.enabled") || fresh.Bool("todo.enabled") {
		t.Errorf("lost edits after saves: theme.dark=%q read.defaultLimit=%d advisor=%v todo=%v",
			fresh.String("theme.dark"), fresh.Int("read.defaultLimit"), fresh.Bool("advisor.enabled"), fresh.Bool("todo.enabled"))
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o644 {
			t.Errorf("file mode changed to %o, want 0644", info.Mode().Perm())
		}
	}
}

// B2: symlinked config.yml (including dangling symlink chains) must stay symlinks
// while atomically updating the final target.
// upstream: packages/coding-agent/test/settings-manager.test.ts: "preserves a symlinked main config while atomically updating its target"
// upstream: packages/coding-agent/test/settings-manager.test.ts: "writes through a dangling symlink chain to the final target, preserving every link"
func TestSavePreservesSymlinkAndDanglingChain(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file symlinks require elevated privileges on Windows")
	}
	tmp := t.TempDir()
	agentDir := filepath.Join(tmp, "agent")
	_ = os.MkdirAll(agentDir, 0o755)
	managed := filepath.Join(tmp, "managed-config.yml")
	_ = os.WriteFile(managed, []byte("setupVersion: 1\n"), 0o644)
	cfgLink := filepath.Join(agentDir, "config.yml")
	if err := os.Symlink(managed, cfgLink); err != nil {
		t.Fatal(err)
	}

	st, err := NewStore(StoreOptions{AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Set("setupVersion", 2, true); err != nil {
		t.Fatalf("save through symlink: %v", err)
	}
	linfo, err := os.Lstat(cfgLink)
	if err != nil || linfo.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("config.yml symlink was replaced by regular file: %v", err)
	}
	got, _ := os.ReadFile(managed)
	if !strings.Contains(string(got), "setupVersion: 2") {
		t.Errorf("managed target not updated: %q", string(got))
	}

	// Dangling chain: config.yml -> mid.yml -> final.yml (missing)
	agentDir2 := filepath.Join(tmp, "agent2")
	_ = os.MkdirAll(agentDir2, 0o755)
	finalPath := filepath.Join(tmp, "final-config.yml")
	midPath := filepath.Join(tmp, "mid-config.yml")
	link2 := filepath.Join(agentDir2, "config.yml")
	_ = os.Symlink(finalPath, midPath)
	_ = os.Symlink(midPath, link2)

	st2, err := NewStore(StoreOptions{AgentDir: agentDir2})
	if err != nil {
		t.Fatal(err)
	}
	if err := st2.Set("setupVersion", 3, true); err != nil {
		t.Fatalf("save through dangling chain: %v", err)
	}
	for _, p := range []string{link2, midPath} {
		li, err := os.Lstat(p)
		if err != nil || li.Mode()&os.ModeSymlink == 0 {
			t.Errorf("%s should remain a symlink", p)
		}
	}
	finalData, err := os.ReadFile(finalPath)
	if err != nil || !strings.Contains(string(finalData), "setupVersion: 3") {
		t.Errorf("final target = (%q, %v), want setupVersion: 3", string(finalData), err)
	}
}

// B3: Malformed global file must surface a warning via Context.Notify, never silently
// rewrite or claim /save succeeded, and must not cache the failure.
// upstream: packages/coding-agent/test/settings-manager.test.ts: "keeps malformed config in place for read-only loads"
func TestMalformedConfigRefusesSaveAndNotifiesOnce(t *testing.T) {
	ResetForTests()
	t.Cleanup(ResetForTests)
	tmp := t.TempDir()
	t.Setenv("PIG_HOME", tmp)
	t.Setenv("PIG_CODING_AGENT_DIR", "")
	agentDir := filepath.Join(tmp, "agent")
	_ = os.MkdirAll(agentDir, 0o755)
	cfgPath := filepath.Join(agentDir, "config.yml")
	broken := "theme:\n  dark: nord\nmodelRoles:\n  default: \"unterminated\n"
	_ = os.WriteFile(cfgPath, []byte(broken), 0o600)

	// NewStore returns an error naming the parse line and leaves the file untouched.
	if _, err := NewStore(StoreOptions{AgentDir: agentDir}); err == nil || !strings.Contains(err.Error(), "line 4") {
		t.Fatalf("NewStore error = %v, want error naming line 4", err)
	}
	rawAfter, _ := os.ReadFile(cfgPath)
	if string(rawAfter) != broken {
		t.Error("malformed config file was modified on load")
	}

	// Via fakeHost: StoreFor notifies once, and /save refuses without touching disk.
	ext := sdk.New("malformed-test")
	Register(ext)
	var saveErr string
	ext.Tool("try_save", "try save", map[string]any{"type": "object"}, func(ctx sdk.Context, _ map[string]any) (any, error) {
		_ = Bool(ctx, "advisor.enabled")
		_ = Bool(ctx, "advisor.enabled") // second read must not spam Notify
		_, err := urls.Write(urls.Context{SDK: ctx, Cwd: ctx.Cwd()}, "cfg://advisor/enabled/save", "true")
		if err != nil {
			saveErr = err.Error()
		}
		return sdk.ToolResult{Content: "ok"}, nil
	})
	host := startHost(t, ext, hostOptions{
		Mode: "tui",
		Cwd:  filepath.Join(tmp, "proj"),
		OnCall: func(method string, _ map[string]any) (map[string]any, string) {
			if method == "ui.confirm" {
				return map[string]any{"confirmed": true}, ""
			}
			return nil, ""
		},
	})
	_, _ = host.callTool("try_save", map[string]any{})
	if saveErr == "" || !strings.Contains(saveErr, "line 4") {
		t.Fatalf("/save on malformed config should fail naming line 4, got: %q", saveErr)
	}
	notifies := host.callsTo("ui.notify")
	if len(notifies) != 1 {
		t.Errorf("expected exactly 1 ui.notify warning, got %d: %+v", len(notifies), notifies)
	}
	rawAfterSave, _ := os.ReadFile(cfgPath)
	if string(rawAfterSave) != broken {
		t.Error("/save overwrote malformed config file")
	}

	// Corrupted on disk AFTER startup: /save must also refuse and preserve file.
	_ = os.WriteFile(cfgPath, []byte("theme:\n  dark: nord\n"), 0o600)
	validStore, err := NewStore(StoreOptions{AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(cfgPath, []byte(broken), 0o600)
	if err := validStore.Set("advisor.enabled", true, true); err == nil || !strings.Contains(err.Error(), "line 4") {
		t.Fatalf("save over post-load corrupted file = %v, want error naming line 4", err)
	}
	rawAfterCorrupt, _ := os.ReadFile(cfgPath)
	if string(rawAfterCorrupt) != broken {
		t.Error("corrupted file was overwritten on save")
	}
}

// Bad project config warns and skips ONLY the project file while keeping global config intact.
func TestMalformedProjectConfigSkipsOnlyProjectLayer(t *testing.T) {
	tmp := t.TempDir()
	agentDir := filepath.Join(tmp, "agent")
	projDir := filepath.Join(tmp, "proj")
	_ = os.MkdirAll(agentDir, 0o755)
	_ = os.MkdirAll(filepath.Join(projDir, ".pig"), 0o755)
	_ = os.WriteFile(filepath.Join(agentDir, "config.yml"), []byte("theme:\n  dark: nord\n"), 0o600)
	_ = os.WriteFile(filepath.Join(projDir, ".pig", "config.yml"), []byte("enabledModels: [\n"), 0o600)

	st, err := NewStore(StoreOptions{
		AgentDir:       agentDir,
		Cwd:            projDir,
		ProjectTrusted: true,
		HomeDir:        filepath.Join(tmp, "home"),
	})
	if err != nil {
		t.Fatalf("malformed project config should not fail store load: %v", err)
	}
	if st.String("theme.dark") != "nord" {
		t.Errorf("global theme.dark lost: %q", st.String("theme.dark"))
	}
	if len(st.Warnings()) == 0 {
		t.Error("expected a warning for skipped malformed project config")
	}
}

// ── Path-scoped arrays (A1) & dropSettingsGroupShadows ───────────────────────

// upstream: packages/coding-agent/test/config/settings-registry.test.ts: "resolves path-scoped object entries in bash.patterns and bashInterceptor.patterns across cwd changes"
func TestPathScopedObjectEntriesAcrossCwd(t *testing.T) {
	tmp := t.TempDir()
	agentDir := filepath.Join(tmp, "agent")
	repoA := filepath.Join(tmp, "repo-a")
	repoASub := filepath.Join(repoA, "packages", "sub")
	repoB := filepath.Join(tmp, "repo-b")
	_ = os.MkdirAll(agentDir, 0o755)
	_ = os.MkdirAll(repoASub, 0o755)
	_ = os.MkdirAll(repoB, 0o755)

	cfg, err := MarshalYAML(map[string]any{
		"bash": map[string]any{
			"patterns": []any{
				map[string]any{
					"path":     repoA,
					"patterns": []any{map[string]any{"match": "make lint*", "approval": "allow"}},
				},
				map[string]any{
					"paths":    []any{repoA},
					"match":    "rm -rf *",
					"approval": "deny",
				},
				map[string]any{
					"paths":    []any{repoB},
					"patterns": []any{map[string]any{"match": "cargo test*", "approval": "allow"}},
				},
				map[string]any{"match": "git status*", "approval": "allow"},
			},
		},
		"bashInterceptor": map[string]any{
			"patterns": []any{
				map[string]any{
					"pathPrefix": repoA,
					"patterns":   []any{map[string]any{"pattern": `^\s*repo-a-tool\b`, "tool": "read", "message": "Use read in repo-a."}},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(agentDir, "config.yml"), []byte(cfg), 0o600)

	st, err := NewStore(StoreOptions{AgentDir: agentDir, Cwd: repoASub})
	if err != nil {
		t.Fatal(err)
	}
	wantA := []any{
		map[string]any{"match": "make lint*", "approval": "allow"},
		map[string]any{"match": "rm -rf *", "approval": "deny"},
		map[string]any{"match": "git status*", "approval": "allow"},
	}
	if got := st.Any("bash.patterns"); !reflect.DeepEqual(got, wantA) {
		t.Errorf("repoASub bash.patterns =\n%#v\nwant:\n%#v", got, wantA)
	}
	defEntry, _ := Lookup("bashInterceptor.patterns")
	defRules := toPlainValue(defEntry.DefaultClone()).([]any)
	gotBI := st.Any("bashInterceptor.patterns").([]any)
	if len(gotBI) != 1+len(defRules) {
		t.Fatalf("repoASub bashInterceptor.patterns len = %d, want %d (1 scoped + %d default)", len(gotBI), 1+len(defRules), len(defRules))
	}
	if gotBI[0].(map[string]any)["tool"] != "read" {
		t.Errorf("first rule = %#v", gotBI[0])
	}

	if err := st.ReloadForCwd(repoB, false); err != nil {
		t.Fatal(err)
	}
	wantB := []any{
		map[string]any{"match": "cargo test*", "approval": "allow"},
		map[string]any{"match": "git status*", "approval": "allow"},
	}
	if got := st.Any("bash.patterns"); !reflect.DeepEqual(got, wantB) {
		t.Errorf("repoB bash.patterns = %#v, want %#v", got, wantB)
	}
	if gotBIB := st.Any("bashInterceptor.patterns").([]any); len(gotBIB) != len(defRules) {
		t.Errorf("repoB bashInterceptor.patterns len = %d, want %d defaults", len(gotBIB), len(defRules))
	}
}

// upstream: packages/coding-agent/test/config/settings-registry.test.ts: "keeps every bash.patterns rule a path-scoped entry carries inside its scope"
func TestPathScopedInlineNestedAndNullPath(t *testing.T) {
	tmp := t.TempDir()
	agentDir := filepath.Join(tmp, "agent")
	repoA := filepath.Join(tmp, "repo-a")
	prod := filepath.Join(repoA, "prod")
	other := filepath.Join(tmp, "other")
	_ = os.MkdirAll(agentDir, 0o755)
	_ = os.MkdirAll(prod, 0o755)
	_ = os.MkdirAll(other, 0o755)

	repoAQ, _ := json.Marshal(repoA)
	prodQ, _ := json.Marshal(prod)
	yamlDoc := strings.Join([]string{
		"bash:",
		"  patterns:",
		"    - path: " + string(repoAQ),
		`      match: "rm -rf *"`,
		"      approval: deny",
		"      patterns:",
		`        - match: "make*"`,
		"          approval: allow",
		"        - path: " + string(prodQ),
		`          match: "terraform apply*"`,
		"          approval: deny",
		"    - path: ~",
		`      match: "curl *"`,
		"      approval: prompt",
		"",
	}, "\n")
	_ = os.WriteFile(filepath.Join(agentDir, "config.yml"), []byte(yamlDoc), 0o600)

	st, err := NewStore(StoreOptions{AgentDir: agentDir, Cwd: prod})
	if err != nil {
		t.Fatal(err)
	}
	matches := func(v any) []string {
		arr := v.([]any)
		out := make([]string, len(arr))
		for i, item := range arr {
			out[i] = item.(map[string]any)["match"].(string)
		}
		return out
	}
	if got := matches(st.Any("bash.patterns")); !reflect.DeepEqual(got, []string{"rm -rf *", "make*", "terraform apply*", "curl *"}) {
		t.Errorf("prod matches = %v", got)
	}
	_ = st.ReloadForCwd(repoA, false)
	if got := matches(st.Any("bash.patterns")); !reflect.DeepEqual(got, []string{"rm -rf *", "make*", "curl *"}) {
		t.Errorf("repoA matches = %v", got)
	}
	_ = st.ReloadForCwd(other, false)
	if got := matches(st.Any("bash.patterns")); !reflect.DeepEqual(got, []string{"curl *"}) {
		t.Errorf("other matches = %v", got)
	}
}

// upstream: packages/coding-agent/test/config/settings-registry.test.ts: "keeps built-in interceptor rules when a project rule joins a global list of only path-scoped entries"
func TestPathScopedLayerMergeKeepsBuiltinDefaults(t *testing.T) {
	tmp := t.TempDir()
	agentDir := filepath.Join(tmp, "agent")
	projDir := filepath.Join(tmp, "project")
	_ = os.MkdirAll(agentDir, 0o755)
	_ = os.MkdirAll(filepath.Join(projDir, ".pig"), 0o755)

	scopedRule := map[string]any{"pattern": `^\s*scoped\s+`, "tool": "read", "message": "Use read."}
	projRule := map[string]any{"pattern": `^\s*custom\s+`, "tool": "read", "message": "Use read."}
	gYaml, _ := MarshalYAML(map[string]any{
		"bashInterceptor": map[string]any{
			"patterns": []any{map[string]any{"path": projDir, "patterns": []any{scopedRule}}},
		},
	})
	pYaml, _ := MarshalYAML(map[string]any{
		"bashInterceptor": map[string]any{"patterns": []any{projRule}},
	})
	_ = os.WriteFile(filepath.Join(agentDir, "config.yml"), []byte(gYaml), 0o600)
	_ = os.WriteFile(filepath.Join(projDir, ".pig", "config.yml"), []byte(pYaml), 0o600)

	st, err := NewStore(StoreOptions{
		AgentDir:       agentDir,
		Cwd:            projDir,
		ProjectTrusted: true,
		HomeDir:        filepath.Join(tmp, "home"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defEntry, _ := Lookup("bashInterceptor.patterns")
	defRules := toPlainValue(defEntry.DefaultClone()).([]any)
	got := st.Any("bashInterceptor.patterns").([]any)
	if len(got) != 2+len(defRules) {
		t.Fatalf("len = %d, want %d", len(got), 2+len(defRules))
	}
	if !reflect.DeepEqual(got[0], projRule) || !reflect.DeepEqual(got[1], scopedRule) {
		t.Errorf("first two rules = %#v, %#v", got[0], got[1])
	}
}

// A1 string-item path-scoped arrays: drops non-strings and unscoped objects, concatenates values+items+valuesKey.
func TestPathScopedStringItems(t *testing.T) {
	tmp := t.TempDir()
	agentDir := filepath.Join(tmp, "agent")
	workDir := filepath.Join(tmp, "work")
	_ = os.MkdirAll(agentDir, 0o755)
	_ = os.MkdirAll(workDir, 0o755)

	gYaml, _ := MarshalYAML(map[string]any{
		"enabledModels": []any{
			"a",
			5,
			map[string]any{"other": "x"},
			map[string]any{
				"path":   workDir,
				"values": []any{"v1"},
				"items":  "i1",
				"models": []any{"m1"},
			},
		},
	})
	_ = os.WriteFile(filepath.Join(agentDir, "config.yml"), []byte(gYaml), 0o600)

	st, err := NewStore(StoreOptions{AgentDir: agentDir, Cwd: workDir})
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Strings("enabledModels"); !reflect.DeepEqual(got, []string{"a", "v1", "i1", "m1"}) {
		t.Errorf("enabledModels = %v, want [a v1 i1 m1]", got)
	}
}

// upstream: packages/coding-agent/test/settings-group-shadowing.test.ts: all 6 cases
func TestDropSettingsGroupShadows(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want map[string]any
	}{
		{
			name: "drops a non-object leaf that would shadow a settings group",
			in:   map[string]any{"tui": "fullscreen"},
			want: map[string]any{},
		},
		{
			name: "keeps well-formed nested objects for a settings group",
			in:   map[string]any{"tui": map[string]any{"resizeScrollback": "preserve"}},
			want: map[string]any{"tui": map[string]any{"resizeScrollback": "preserve"}},
		},
		{
			name: "drops nested non-object shadows without discarding valid siblings",
			in:   map[string]any{"auth": map[string]any{"broker": "nonsense"}, "autoResume": true},
			want: map[string]any{"auth": map[string]any{}, "autoResume": true},
		},
		{
			name: "drops top-level model string shadowing model.* group",
			in:   map[string]any{"model": "opus"},
			want: map[string]any{},
		},
		{
			name: "passes unknown keys through untouched",
			in:   map[string]any{"permissions": map[string]any{"allow": []any{"Bash"}}, "$schema": "https://example.com"},
			want: map[string]any{"permissions": map[string]any{"allow": []any{"Bash"}}, "$schema": "https://example.com"},
		},
		{
			name: "preserves arrays at non-group paths",
			in:   map[string]any{"cycleOrder": []any{"a", "b"}},
			want: map[string]any{"cycleOrder": []any{"a", "b"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DropSettingsGroupShadows(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("DropSettingsGroupShadows = %#v, want %#v", got, tc.want)
			}
		})
	}
}

// ── YAML parser value assertions & edge cases (B6, B7, E) ────────────────────

func TestYAMLSubsetValuesAndEdgeCases(t *testing.T) {
	// Value assertions (not just parse->marshal->parse identity).
	doc := "\uFEFF# leading comment\r\n" +
		"theme:\r\n" +
		"   dark: titanium\r\n" +
		"   light: light\r\n" +
		"compaction:\r\n" +
		"   enabled: true\r\n" +
		"   thresholdPercent: -1\r\n" +
		"   methodOrder: [remote, soft]\r\n" +
		"edit:\r\n" +
		"   fuzzyThreshold: 0.95\r\n" +
		"bashInterceptor:\r\n" +
		"   patterns:\r\n" +
		"      - pattern: '^\\s*(cat|head)\\s+'\r\n" +
		"        tool: read\r\n" +
		"        message: \"Use read\"\r\n" +
		"shellCommandPrefix: echo don't # trailing comment after apostrophe\r\n" +
		"retry:\r\n" +
		"   maxRetries: 0x0A\r\n" +
		"   baseDelayMs: 0o10\r\n" +
		"   fallbackChains:\r\n" +
		"      tiny: []\r\n" +
		"      empty: ~\r\n" +
		"dup: first\r\n" +
		"dup: second\r\n" +
		"emoji: \"\\uD83D\\uDE00\"\r\n" +
		"esc: \"\\a\\e\"\r\n" +
		"nbsp: \u00A0keep\u00A0\r\n"

	parsed, err := ParseYAML(doc)
	if err != nil {
		t.Fatalf("ParseYAML failed: %v", err)
	}
	if parsed["theme"].(map[string]any)["dark"] != "titanium" {
		t.Errorf("theme.dark = %#v", parsed["theme"])
	}
	comp := parsed["compaction"].(map[string]any)
	if comp["enabled"] != true || comp["thresholdPercent"] != float64(-1) {
		t.Errorf("compaction = %#v", comp)
	}
	if !reflect.DeepEqual(comp["methodOrder"], []any{"remote", "soft"}) {
		t.Errorf("methodOrder = %#v", comp["methodOrder"])
	}
	if parsed["edit"].(map[string]any)["fuzzyThreshold"] != 0.95 {
		t.Errorf("fuzzyThreshold = %#v", parsed["edit"])
	}
	// B6: apostrophe in plain scalar must not swallow `# comment`.
	if parsed["shellCommandPrefix"] != "echo don't" {
		t.Errorf("shellCommandPrefix = %q, want %q", parsed["shellCommandPrefix"], "echo don't")
	}
	retry := parsed["retry"].(map[string]any)
	if retry["maxRetries"] != float64(10) || retry["baseDelayMs"] != float64(8) {
		t.Errorf("hex/octal retry = %#v", retry)
	}
	fc := retry["fallbackChains"].(map[string]any)
	if !reflect.DeepEqual(fc["tiny"], []any{}) || fc["empty"] != nil {
		t.Errorf("fallbackChains = %#v", fc)
	}
	if parsed["dup"] != "second" {
		t.Errorf("duplicate key = %#v, want second", parsed["dup"])
	}
	if parsed["emoji"] != "😀" {
		t.Errorf("UTF-16 surrogate emoji = %q, want 😀", parsed["emoji"])
	}
	if parsed["esc"] != "\a\x1b" {
		t.Errorf("\\a\\e escape = %q", parsed["esc"])
	}
	if parsed["nbsp"] != "\u00A0keep\u00A0" {
		t.Errorf("non-ASCII nbsp was trimmed: %q", parsed["nbsp"])
	}

	// B6: keys with quotes round-trip cleanly.
	withQuotedKey := map[string]any{"tools": map[string]any{"approval": map[string]any{"it's": "allow", `say "hi"`: "deny"}}}
	marshaled, err := MarshalYAML(withQuotedKey)
	if err != nil {
		t.Fatal(err)
	}
	reparsed, err := ParseYAML(marshaled)
	if err != nil {
		t.Fatalf("re-parse quoted key failed: %v\nyaml:\n%s", err, marshaled)
	}
	if !reflect.DeepEqual(withQuotedKey, reparsed) {
		t.Errorf("quoted-key round-trip mismatch:\nwant %#v\ngot  %#v", withQuotedKey, reparsed)
	}

	// Unsupported / malformed constructs must error (never silently misparse).
	badInputs := []struct {
		name string
		src  string
	}{
		{"tab indent", "theme:\n\tdark: titanium\n"},
		{"bad indent", "theme:\n  dark: titanium\n    bad: 1\n"},
		{"anchor", "a: &x hello\n"},
		{"alias", "a: *x\n"},
		{"tag", "a: !!str 123\n"},
		{"block scalar", "a: |\n  hello\n"},
		{"multi-doc", "---\na: 1\n---\nb: 2\n"},
		{"sequence root", "- item1\n- item2\n"},
		{"deep flow nesting (B7)", "a: " + strings.Repeat("[", 80) + strings.Repeat("]", 80) + "\n"},
	}
	for _, tc := range badInputs {
		if _, err := ParseYAML(tc.src); err == nil {
			t.Errorf("%s: expected error, got nil", tc.name)
		}
	}
}

func TestEnabledHelper(t *testing.T) {
	t.Cleanup(ResetForTests)
	store, err := NewIsolated(map[string]any{
		"bash.enabled": false,
		"find.enabled": "off",
	})
	if err != nil {
		t.Fatal(err)
	}
	SetStoreForTests(func(sdk.Context) *Store { return store })
	var zero sdk.Context
	if Enabled(zero, "bash.enabled", true) != false {
		t.Error("Enabled(bash.enabled) should be false")
	}
	if Enabled(zero, "read.summarize.enabled", false) != true {
		t.Error("Enabled(read.summarize.enabled) should return omp default true")
	}
	if Enabled(zero, "find.enabled", true) != false {
		t.Error("Enabled(find.enabled='off') should be false")
	}
	if Enabled(zero, "unknown.setting.key", true) != true {
		t.Error("Enabled(unknown, true) should return def=true")
	}
}

// ── cfg:// protocol tests (ported from omp cfg-protocol.test.ts & cfg-approval-timeout.test.ts) ──

type stubApprovalHost struct {
	approveFn  func(req ChangeRequest) Approval
	applied    []AppliedChange
	persistent *Store
	asked      []ChangeRequest
}

func (s *stubApprovalHost) Approve(_ urls.Context, req ChangeRequest) (Approval, error) {
	s.asked = append(s.asked, req)
	if s.approveFn != nil {
		return s.approveFn(req), nil
	}
	return ApprovalOnce, nil
}

func (s *stubApprovalHost) Applied(change AppliedChange) {
	s.applied = append(s.applied, change)
}

func (s *stubApprovalHost) PersistentStore(_ urls.Context) *Store {
	return s.persistent
}

func cfgRead(t *testing.T, rawURL string, store *Store) (*urls.Resource, error) {
	t.Helper()
	SetStoreForTests(func(sdk.Context) *Store { return store })
	h := NewCfgHandler()
	_ = urls.Register(h)
	u, err := urls.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	return h.Resolve(urls.Context{}, u)
}

func cfgWrite(t *testing.T, rawURL, content string, store *Store, sessionID string) (*urls.WriteResult, error) {
	t.Helper()
	SetStoreForTests(func(sdk.Context) *Store { return store })
	cfgMu.Lock()
	sessionIDHook = func(urls.Context) string { return sessionID }
	cfgMu.Unlock()
	h := NewCfgHandler()
	_ = urls.Register(h)
	u, err := urls.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	return h.Write(urls.Context{}, u, content)
}

// upstream: packages/coding-agent/test/internal-urls/cfg-protocol.test.ts: "lists a namespace relative to its prefix and a setting with its source"
func TestCfgProtocolNamespaceAndLeafRead(t *testing.T) {
	t.Cleanup(ResetForTests)
	store, err := NewIsolated(map[string]any{"advisor.syncBacklog": "3"})
	if err != nil {
		t.Fatal(err)
	}

	ns, err := cfgRead(t, "cfg://advisor", store)
	if err != nil {
		t.Fatalf("cfg://advisor: %v", err)
	}
	if !strings.Contains(ns.Content, `syncBacklog: "3"  # off|1|3|5|strict · default "off"`) {
		t.Errorf("unexpected namespace output:\n%s", ns.Content)
	}
	if strings.Contains(ns.Content, "advisor:") {
		t.Errorf("namespace listing should strip prefix:\n%s", ns.Content)
	}
	cfgDet := ns.Details["cfg"].(ReadDetails)
	if cfgDet.Path != "advisor" || cfgDet.Modified != 1 || cfgDet.Count != 8 {
		t.Errorf("namespace details.cfg = %+v", cfgDet)
	}

	leaf, err := cfgRead(t, "cfg://Advisor.SyncBacklog", store)
	if err != nil {
		t.Fatalf("cfg://Advisor.SyncBacklog: %v", err)
	}
	if !strings.HasPrefix(leaf.Content, `advisor.syncBacklog: "3"`) {
		t.Errorf("leaf prefix mismatch:\n%s", leaf.Content)
	}
	if !strings.Contains(leaf.Content, "source: session override") {
		t.Errorf("leaf missing source:\n%s", leaf.Content)
	}

	// A2: default bashInterceptor.patterns line must preserve key order (`pattern`, `tool`, `message`)
	// and must NOT HTML-escape `>` into `\u003e`.
	bi, err := cfgRead(t, "cfg://bashInterceptor/patterns", store)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(bi.Content, `\u003e`) || strings.Contains(bi.Content, `\u003c`) {
		t.Errorf("formatValue HTML-escaped angle brackets:\n%s", bi.Content)
	}
	if !strings.Contains(bi.Content, `{"pattern":`) {
		t.Errorf("formatValue reordered bashInterceptor rule keys:\n%s", bi.Content)
	}

	// Root listing cfg:// returns all 644 settings.
	root, err := cfgRead(t, "cfg://", store)
	if err != nil {
		t.Fatalf("cfg://: %v", err)
	}
	rootDet := root.Details["cfg"].(ReadDetails)
	if rootDet.Path != "" || rootDet.Count != 644 || rootDet.Modified != 1 {
		t.Errorf("root details.cfg = %+v", rootDet)
	}
}

// upstream: packages/coding-agent/test/internal-urls/cfg-protocol.test.ts: "redacts credential values"
func TestCfgProtocolRedactsCredentials(t *testing.T) {
	t.Cleanup(ResetForTests)
	store, err := NewIsolated(map[string]any{"auth.broker.token": "sekrit"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := cfgRead(t, "cfg://auth", store)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "token: <redacted>") {
		t.Errorf("expected redacted token:\n%s", res.Content)
	}
	if strings.Contains(res.Content, "sekrit") {
		t.Errorf("credential leaked in output:\n%s", res.Content)
	}
}

// A3 + E: print and RPC modes (running on real fakeHost, not zero Context) refuse cfg:// writes without prompting.
// upstream: packages/coding-agent/test/internal-urls/cfg-protocol.test.ts: "refuses subagent and headless writes without prompting the user"
func TestCfgProtocolRefusesPrintAndRPCWrites(t *testing.T) {
	for _, mode := range []string{"print", "json", "rpc"} {
		t.Run(mode, func(t *testing.T) {
			ResetForTests()
			t.Cleanup(ResetForTests)
			tmp := t.TempDir()
			t.Setenv("PIG_HOME", tmp)
			t.Setenv("PIG_CODING_AGENT_DIR", "")

			ext := sdk.New("mode-refusal")
			Register(ext)
			var gotErr string
			ext.Tool("try_write", "try write", map[string]any{"type": "object"}, func(ctx sdk.Context, _ map[string]any) (any, error) {
				_, err := urls.Write(urls.Context{SDK: ctx, Cwd: ctx.Cwd()}, "cfg://advisor/enabled", "true")
				if err != nil {
					gotErr = err.Error()
				}
				return sdk.ToolResult{Content: "done"}, nil
			})
			host := startHost(t, ext, hostOptions{
				Mode: mode,
				Cwd:  tmp,
			})
			_, _ = host.callTool("try_write", map[string]any{})
			if !strings.Contains(gotErr, "no interactive UI") {
				t.Errorf("mode=%s error = %q, want 'no interactive UI'", mode, gotErr)
			}
			if len(host.callsTo("ui.confirm")) != 0 {
				t.Errorf("mode=%s should never prompt ui.confirm", mode)
			}
		})
	}
}

// upstream: packages/coding-agent/test/internal-urls/cfg-protocol.test.ts: "applies a session change only after approval and leaves disk untouched"
func TestCfgProtocolSessionApproveAndDeny(t *testing.T) {
	t.Cleanup(ResetForTests)
	store, _ := NewIsolated(nil)
	persistent, _ := NewIsolated(nil)
	allow := false
	host := &stubApprovalHost{
		approveFn: func(ChangeRequest) Approval {
			if allow {
				return ApprovalOnce
			}
			return ApprovalDeny
		},
		persistent: persistent,
	}
	SetApprovalHost(host)

	declined, err := cfgWrite(t, "cfg://advisor/enabled", "true", store, "sess-1")
	if err != nil {
		t.Fatalf("declined write errored: %v", err)
	}
	if declined.Details["cfg"].(WriteDetails).Outcome != OutcomeDeclined {
		t.Errorf("outcome = %+v, want declined", declined.Details["cfg"])
	}
	if store.Bool("advisor.enabled") || len(host.applied) != 0 {
		t.Error("declined write must not apply")
	}

	allow = true
	applied, err := cfgWrite(t, "cfg://advisor/enabled", "true", store, "sess-1")
	if err != nil {
		t.Fatalf("approved write errored: %v", err)
	}
	det := applied.Details["cfg"].(WriteDetails)
	if det.Outcome != OutcomeApplied || det.Previous != "false" || det.Value != "true" || det.Save {
		t.Errorf("details = %+v", det)
	}
	if !strings.Contains(applied.Text, "cfg://advisor/enabled/save") {
		t.Errorf("expected save hint in text: %s", applied.Text)
	}
	if !store.Bool("advisor.enabled") {
		t.Error("advisor.enabled should be true after approval")
	}
	if persistent.Provenance("advisor.enabled") != ProvenanceDefault {
		t.Error("persistent store should remain untouched on session write")
	}
	if len(host.applied) != 1 || host.applied[0].Path != "advisor.enabled" || host.applied[0].Save {
		t.Errorf("applied callbacks = %+v", host.applied)
	}
}

// upstream: packages/coding-agent/test/internal-urls/cfg-protocol.test.ts: "stops asking for the rest of the session once the user allows it always"
func TestCfgProtocolSessionGrant(t *testing.T) {
	t.Cleanup(ResetForTests)
	store, _ := NewIsolated(nil)
	host := &stubApprovalHost{
		approveFn:  func(ChangeRequest) Approval { return ApprovalSession },
		persistent: store,
	}
	SetApprovalHost(host)

	// B8: empty session ID must NEVER record a session grant.
	if _, err := cfgWrite(t, "cfg://advisor/enabled", "true", store, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := cfgWrite(t, "cfg://advisor/syncBacklog", `"3"`, store, ""); err != nil {
		t.Fatal(err)
	}
	if len(host.asked) != 2 {
		t.Fatalf("empty sessionID should not grant across writes: asked=%d", len(host.asked))
	}
	host.asked = nil
	store, _ = NewIsolated(nil)
	host.persistent = store

	if _, err := cfgWrite(t, "cfg://advisor/enabled", "true", store, "session-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := cfgWrite(t, "cfg://advisor/syncBacklog", `"3"`, store, "session-a"); err != nil {
		t.Fatal(err)
	}
	if len(host.asked) != 1 {
		t.Fatalf("expected 1 prompt for session-a non-save writes, got %d", len(host.asked))
	}
	if _, err := cfgWrite(t, "cfg://advisor/syncBacklog/save", `"5"`, store, "session-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := cfgWrite(t, "cfg://advisor/enabled/save", "false", store, "session-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := cfgWrite(t, "cfg://advisor/syncBacklog", `"1"`, store, "session-b"); err != nil {
		t.Fatal(err)
	}
	if len(host.asked) != 3 {
		t.Fatalf("expected 3 prompts total, got %d: %+v", len(host.asked), host.asked)
	}
}

// upstream: packages/coding-agent/test/internal-urls/cfg-protocol.test.ts: "fails an unanswered write without changing anything and asks again next time"
func TestCfgProtocolApprovalTimeout(t *testing.T) {
	t.Cleanup(ResetForTests)
	store, _ := NewIsolated(nil)
	answer := ApprovalTimeout
	host := &stubApprovalHost{
		approveFn:  func(ChangeRequest) Approval { return answer },
		persistent: store,
	}
	SetApprovalHost(host)

	_, err := cfgWrite(t, "cfg://advisor/enabled", "true", store, "sess")
	if err == nil || !strings.Contains(err.Error(), "did not answer the approval prompt") {
		t.Fatalf("expected timeout error text, got: %v", err)
	}
	if store.Bool("advisor.enabled") {
		t.Error("timed out write must not change setting")
	}

	answer = ApprovalOnce
	res, err := cfgWrite(t, "cfg://advisor/enabled", "true", store, "sess")
	if err != nil {
		t.Fatalf("retry after timeout failed: %v", err)
	}
	if res.Details["cfg"].(WriteDetails).Outcome != OutcomeApplied {
		t.Errorf("retry outcome = %+v", res.Details["cfg"])
	}
}

// upstream: packages/coding-agent/test/internal-urls/cfg-protocol.test.ts: "refuses a session change an environment variable overrides without asking the user"
func TestCfgProtocolEnvShadowedRefusesWithoutPrompting(t *testing.T) {
	t.Cleanup(ResetForTests)
	store, _ := NewIsolated(nil)
	host := &stubApprovalHost{persistent: store}
	SetApprovalHost(host)
	t.Setenv("PI_EDIT_FUZZY", "1")

	_, err := cfgWrite(t, "cfg://edit/fuzzyMatch", "false", store, "sess")
	if err == nil || !strings.Contains(err.Error(), "PI_EDIT_FUZZY") {
		t.Fatalf("expected PI_EDIT_FUZZY refusal, got: %v", err)
	}
	if len(host.asked) != 0 {
		t.Errorf("should not have prompted user: %+v", host.asked)
	}
}

// upstream: packages/coding-agent/test/internal-urls/cfg-protocol.test.ts: "asks normally for writes a fallback environment variable only defaults"
func TestCfgProtocolFallbackEnvAllowsWrites(t *testing.T) {
	t.Cleanup(ResetForTests)
	store, _ := NewIsolated(nil)
	host := &stubApprovalHost{persistent: store}
	SetApprovalHost(host)
	t.Setenv("SEARXNG_ENDPOINT", "http://env.example")

	res, err := cfgWrite(t, "cfg://searxng/endpoint", `"http://session.example"`, store, "sess")
	if err != nil {
		t.Fatalf("session write failed: %v", err)
	}
	if res.Details["cfg"].(WriteDetails).Outcome != OutcomeApplied {
		t.Errorf("outcome = %+v", res.Details["cfg"])
	}
	if store.String("searxng.endpoint") != "http://session.example" {
		t.Errorf("searxng.endpoint = %q", store.String("searxng.endpoint"))
	}
	if _, err := cfgWrite(t, "cfg://searxng/endpoint/save", `"http://saved.example"`, store, "sess"); err != nil {
		t.Fatalf("save write failed: %v", err)
	}
	if store.String("searxng.endpoint") != "http://saved.example" {
		t.Errorf("searxng.endpoint = %q", store.String("searxng.endpoint"))
	}
	if len(host.asked) != 2 || host.asked[0].ShadowedBy != "" || host.asked[1].ShadowedBy != "" {
		t.Errorf("unexpected asked = %+v", host.asked)
	}
}

// upstream: packages/coding-agent/test/internal-urls/cfg-protocol.test.ts: "reports a partial record write as applied, since layers deep-merge"
func TestCfgProtocolPartialRecordWrite(t *testing.T) {
	t.Cleanup(ResetForTests)
	store, _ := NewIsolated(nil)
	_ = store.Set("modelRoles", map[string]any{"default": "anthropic/a"}, true)
	host := &stubApprovalHost{persistent: store}
	SetApprovalHost(host)

	session, err := cfgWrite(t, "cfg://modelRoles", `{"smol":"anthropic/b"}`, store, "sess")
	if err != nil {
		t.Fatal(err)
	}
	sDet := session.Details["cfg"].(WriteDetails)
	if sDet.Outcome != OutcomeApplied || sDet.Effective != "" || !strings.Contains(session.Text, "cfg://modelRoles/save") {
		t.Errorf("session partial record write = (%+v, %q)", sDet, session.Text)
	}

	saved, err := cfgWrite(t, "cfg://modelRoles/save", `{"smol":"anthropic/c"}`, store, "sess")
	if err != nil {
		t.Fatal(err)
	}
	svDet := saved.Details["cfg"].(WriteDetails)
	if svDet.Effective != "" || strings.Contains(saved.Text, "Effective value is still") {
		t.Errorf("saved partial record write = (%+v, %q)", svDet, saved.Text)
	}
	roles := store.Any("modelRoles").(map[string]any)
	if roles["smol"] != "anthropic/c" {
		t.Errorf("modelRoles = %#v", roles)
	}
}

// upstream: packages/coding-agent/test/internal-urls/cfg-protocol.test.ts: "reports a saved value a project layer still overrides"
func TestCfgProtocolSaveShadowedByProject(t *testing.T) {
	t.Cleanup(ResetForTests)
	store, _ := NewIsolated(nil)
	store.SetProjectLayer(map[string]any{
		"modelRoles": map[string]any{"default": "anthropic/project"},
	})
	host := &stubApprovalHost{persistent: store}
	SetApprovalHost(host)

	shadowed, err := cfgWrite(t, "cfg://modelRoles/save", `{"default":"anthropic/global"}`, store, "sess")
	if err != nil {
		t.Fatal(err)
	}
	if host.asked[0].ShadowedBy != "project config" {
		t.Errorf("ShadowedBy = %q, want 'project config'", host.asked[0].ShadowedBy)
	}
	if !strings.Contains(shadowed.Text, "project config takes precedence") {
		t.Errorf("expected shadowing note in text: %s", shadowed.Text)
	}
	if eff := shadowed.Details["cfg"].(WriteDetails).Effective; !strings.Contains(eff, "anthropic/project") {
		t.Errorf("details.cfg.effective = %q", eff)
	}

	applied, err := cfgWrite(t, "cfg://modelRoles/save", `{"smol":"anthropic/global"}`, store, "sess")
	if err != nil {
		t.Fatal(err)
	}
	if host.asked[1].ShadowedBy != "" || applied.Details["cfg"].(WriteDetails).Effective != "" {
		t.Errorf("unexpected shadowing on non-overridden key: %+v / %+v", host.asked[1], applied.Details["cfg"])
	}
}

// upstream: packages/coding-agent/test/internal-urls/cfg-protocol.test.ts: "reports a saved bash.patterns list as applied under project rules that prepend to it"
func TestCfgProtocolSaveBashPatternsPrependedByProject(t *testing.T) {
	t.Cleanup(ResetForTests)
	tmp := t.TempDir()
	agentDir := filepath.Join(tmp, "agent")
	projDir := filepath.Join(tmp, "project")
	resetDir := filepath.Join(tmp, "reset")
	homeDir := filepath.Join(tmp, "home")
	_ = os.MkdirAll(filepath.Join(projDir, ".pig"), 0o755)
	_ = os.MkdirAll(filepath.Join(resetDir, ".pig"), 0o755)
	_ = os.WriteFile(filepath.Join(projDir, ".pig", "config.yml"), []byte("bash:\n  patterns:\n    - match: \"make *\"\n      approval: allow\n"), 0o600)
	_ = os.WriteFile(filepath.Join(resetDir, ".pig", "config.yml"), []byte("bash:\n  patterns: []\n"), 0o600)

	store, err := NewStore(StoreOptions{AgentDir: agentDir, Cwd: projDir, ProjectTrusted: true, HomeDir: homeDir})
	if err != nil {
		t.Fatal(err)
	}
	host := &stubApprovalHost{persistent: store}
	SetApprovalHost(host)

	saved, err := cfgWrite(t, "cfg://bash/patterns/save", `[{"match":"git *","approval":"allow"}]`, store, "sess")
	if err != nil {
		t.Fatal(err)
	}
	if host.asked[0].ShadowedBy != "" || strings.Contains(saved.Text, "Effective value is still") {
		t.Errorf("unexpected shadowing for prepended bash.patterns: %+v / %s", host.asked[0], saved.Text)
	}

	resetStore, err := NewStore(StoreOptions{AgentDir: agentDir, Cwd: resetDir, ProjectTrusted: true, HomeDir: homeDir})
	if err != nil {
		t.Fatal(err)
	}
	host2 := &stubApprovalHost{persistent: resetStore}
	SetApprovalHost(host2)
	shadowed, err := cfgWrite(t, "cfg://bash/patterns/save", `[{"match":"ls *","approval":"allow"}]`, resetStore, "sess")
	if err != nil {
		t.Fatal(err)
	}
	if host2.asked[0].ShadowedBy != "project config" || !strings.Contains(shadowed.Text, "project config takes precedence") {
		t.Errorf("expected project shadowing on empty reset: %+v / %s", host2.asked[0], shadowed.Text)
	}
}

// upstream: packages/coding-agent/test/internal-urls/cfg-protocol.test.ts: "persists /save writes to the host settings and mirrors them into a separate session instance"
func TestCfgProtocolSaveMirrorsIntoSeparateSession(t *testing.T) {
	t.Cleanup(ResetForTests)
	sessionStore, _ := NewIsolated(nil)
	persistentStore, _ := NewIsolated(nil)
	host := &stubApprovalHost{persistent: persistentStore}
	SetApprovalHost(host)

	if _, err := cfgWrite(t, "cfg://advisor/syncBacklog/save", `"5"`, sessionStore, "sess"); err != nil {
		t.Fatal(err)
	}
	if persistentStore.String("advisor.syncBacklog") != "5" || persistentStore.Provenance("advisor.syncBacklog") != ProvenanceGlobal {
		t.Errorf("persistent = (%q, %q)", persistentStore.String("advisor.syncBacklog"), persistentStore.Provenance("advisor.syncBacklog"))
	}
	if sessionStore.String("advisor.syncBacklog") != "5" {
		t.Errorf("session mirror = %q, want 5", sessionStore.String("advisor.syncBacklog"))
	}
	if len(host.applied) != 2 || host.applied[0].Store != persistentStore || host.applied[1].Store != sessionStore {
		t.Errorf("applied callbacks = %+v", host.applied)
	}
}

// upstream: packages/coding-agent/test/internal-urls/cfg-protocol.test.ts: "rejects namespace targets and values outside the schema before asking"
func TestCfgProtocolRejectsInvalidTargetsAndMatchesFuzzyFilter(t *testing.T) {
	t.Cleanup(ResetForTests)
	store, _ := NewIsolated(nil)
	host := &stubApprovalHost{persistent: store}
	SetApprovalHost(host)

	if _, err := cfgWrite(t, "cfg://advisor", "true", store, "sess"); err == nil || !strings.Contains(err.Error(), "is a namespace") {
		t.Errorf("expected namespace error, got %v", err)
	}
	if _, err := cfgWrite(t, "cfg://advisor/syncBacklog", "7", store, "sess"); err == nil || !strings.Contains(err.Error(), "Valid values: off, 1, 3, 5") {
		t.Errorf("expected enum error, got %v", err)
	}
	// A4: exact omp fuzzyFilter suggestions: `advisr.enabled` -> only `advisor.enabled`; `readlimit` -> none.
	_, err := cfgWrite(t, "cfg://advisr/enabled", "true", store, "sess")
	if err == nil || !strings.Contains(err.Error(), "Similar: advisor.enabled\n") {
		t.Errorf("advisr.enabled error = %v, want exact 'Similar: advisor.enabled'", err)
	}
	_, err = cfgWrite(t, "cfg://readlimit", "10", store, "sess")
	if err == nil || strings.Contains(err.Error(), "Similar:") {
		t.Errorf("readlimit error = %v, want no Similar line (matching omp fuzzyFilter)", err)
	}
	if len(host.asked) != 0 {
		t.Errorf("should not prompt on invalid writes: %+v", host.asked)
	}
}

// B9: values containing `{{previous}}` must not be double-substituted.
func TestCfgProtocolSinglePassTemplateRendering(t *testing.T) {
	t.Cleanup(ResetForTests)
	store, _ := NewIsolated(nil)
	host := &stubApprovalHost{persistent: store}
	SetApprovalHost(host)

	res, err := cfgWrite(t, "cfg://theme/dark", `"{{previous}}"`, store, "sess")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, `"{{previous}}"`) {
		t.Errorf("template double-substituted {{previous}}: %s", res.Text)
	}
}

// End-to-end tests using PiG's real sdk.Context.Confirm / ConfirmWithOptions over fakeHost.
func TestCfgProtocolConfirmViaSDK(t *testing.T) {
	runCase := func(t *testing.T, name string, askTimeout float64, save bool, onConfirm func(args map[string]any) (map[string]any, string), advanceClock time.Duration) (string, map[string]any, string, []hostCall) {
		t.Helper()
		ResetForTests()
		t.Cleanup(ResetForTests)

		tmp := t.TempDir()
		t.Setenv("PIG_HOME", tmp)
		t.Setenv("PIG_CODING_AGENT_DIR", "")
		cwd := filepath.Join(tmp, "proj")
		_ = os.MkdirAll(cwd, 0o755)

		ext := sdk.New("settings-test")
		Register(ext)

		var gotText string
		var gotDetails map[string]any
		var gotErr string

		ext.Tool("cfg_write_harness", "test harness", map[string]any{"type": "object"}, func(ctx sdk.Context, params map[string]any) (any, error) {
			if askTimeout > 0 {
				if err := Set(ctx, "ask.timeout", askTimeout, false); err != nil {
					return sdk.ToolResult{Content: err.Error(), IsError: true}, nil
				}
			}
			target := "cfg://advisor/enabled"
			if save {
				target += "/save"
			}
			res, err := urls.Write(urls.Context{SDK: ctx, Cwd: ctx.Cwd()}, target, "true")
			if err != nil {
				gotErr = err.Error()
				return sdk.ToolResult{Content: err.Error(), IsError: true}, nil
			}
			gotText = res.Text
			gotDetails = res.Details
			return sdk.ToolResult{Content: res.Text}, nil
		})

		fakeNow := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
		nowFunc = func() time.Time { return fakeNow }

		host := startHost(t, ext, hostOptions{
			Mode: "tui",
			Cwd:  cwd,
			OnCall: func(method string, args map[string]any) (map[string]any, string) {
				if method == "ui.confirm" {
					if advanceClock > 0 {
						fakeNow = fakeNow.Add(advanceClock)
					}
					return onConfirm(args)
				}
				return nil, ""
			},
		})
		_, fail := host.callTool("cfg_write_harness", map[string]any{})
		if fail != "" {
			t.Fatalf("%s: tool call transport error: %s", name, fail)
		}
		return gotText, gotDetails, gotErr, host.callsTo("ui.confirm")
	}

	// upstream: packages/coding-agent/test/cfg-approval-timeout.test.ts: "prompts without a deadline when ask.timeout is at its default"
	t.Run("approved session write", func(t *testing.T) {
		text, det, errStr, calls := runCase(t, "approved", 0, false, func(args map[string]any) (map[string]any, string) {
			return map[string]any{"confirmed": true}, ""
		}, 0)
		if errStr != "" {
			t.Fatalf("unexpected error: %s", errStr)
		}
		if len(calls) != 1 {
			t.Fatalf("expected 1 ui.confirm call, got %d", len(calls))
		}
		if _, hasOpts := calls[0].Args["opts"]; hasOpts {
			t.Errorf("ask.timeout=0 should omit dialog opts timeout, got %#v", calls[0].Args["opts"])
		}
		if !strings.Contains(text, "for this session only") {
			t.Errorf("unexpected text: %s", text)
		}
		// E: verify actual Details returned by Write marshals with cfg-render.ts field names.
		b, _ := json.Marshal(det["cfg"])
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		if m["outcome"] != "applied" || m["path"] != "advisor.enabled" || m["previous"] != "false" || m["value"] != "true" || m["save"] != false {
			t.Errorf("marshaled Details.cfg = %#v", m)
		}
	})

	t.Run("approved /save persists config.yml", func(t *testing.T) {
		ResetForTests()
		t.Cleanup(ResetForTests)
		tmp := t.TempDir()
		t.Setenv("PIG_HOME", tmp)
		t.Setenv("PIG_CODING_AGENT_DIR", "")
		cwd := filepath.Join(tmp, "proj")
		_ = os.MkdirAll(cwd, 0o755)

		ext := sdk.New("settings-save-test")
		Register(ext)
		var saveErr string
		var saveText string
		ext.Tool("cfg_save", "save harness", map[string]any{"type": "object"}, func(ctx sdk.Context, _ map[string]any) (any, error) {
			res, err := urls.Write(urls.Context{SDK: ctx, Cwd: ctx.Cwd()}, "cfg://advisor/enabled/save", "true")
			if err != nil {
				saveErr = err.Error()
				return sdk.ToolResult{Content: err.Error(), IsError: true}, nil
			}
			saveText = res.Text
			return sdk.ToolResult{Content: res.Text}, nil
		})
		host := startHost(t, ext, hostOptions{
			Mode: "tui",
			Cwd:  cwd,
			OnCall: func(method string, _ map[string]any) (map[string]any, string) {
				if method == "ui.confirm" {
					return map[string]any{"confirmed": true}, ""
				}
				return nil, ""
			},
		})
		_, _ = host.callTool("cfg_save", map[string]any{})
		if saveErr != "" {
			t.Fatalf("save error: %s", saveErr)
		}
		if !strings.Contains(saveText, "Saved `advisor.enabled` = true to the global config") {
			t.Errorf("unexpected save text: %s", saveText)
		}
		cfgBytes, err := os.ReadFile(filepath.Join(tmp, "agent", "config.yml"))
		if err != nil {
			t.Fatalf("expected persisted config.yml: %v", err)
		}
		parsed, err := ParseYAML(string(cfgBytes))
		if err != nil {
			t.Fatalf("parse persisted config.yml: %v", err)
		}
		adv, _ := parsed["advisor"].(map[string]any)
		if adv["enabled"] != true {
			t.Errorf("persisted advisor.enabled = %#v, want true", adv["enabled"])
		}
	})

	t.Run("denied write", func(t *testing.T) {
		text, det, errStr, calls := runCase(t, "denied", 30, false, func(args map[string]any) (map[string]any, string) {
			return map[string]any{"confirmed": false}, ""
		}, 1*time.Second)
		if errStr != "" {
			t.Fatalf("denied write should not error, got: %s", errStr)
		}
		if len(calls) != 1 {
			t.Fatalf("expected 1 ui.confirm call, got %d", len(calls))
		}
		opts, _ := calls[0].Args["opts"].(map[string]any)
		if opts["timeout"] != float64(30000) {
			t.Errorf("ui.confirm opts.timeout = %#v, want 30000", opts["timeout"])
		}
		if det["cfg"].(WriteDetails).Outcome != OutcomeDeclined {
			t.Errorf("details = %+v", det)
		}
		if !strings.Contains(text, "User declined changing `advisor.enabled`") {
			t.Errorf("unexpected text: %s", text)
		}
	})

	// upstream: packages/coding-agent/test/cfg-approval-timeout.test.ts: "prompts with the ask.timeout deadline in milliseconds"
	t.Run("timed out write", func(t *testing.T) {
		_, _, errStr, calls := runCase(t, "timeout", 45, false, func(args map[string]any) (map[string]any, string) {
			return map[string]any{"confirmed": false}, ""
		}, 45*time.Second)
		if len(calls) != 1 {
			t.Fatalf("expected 1 ui.confirm call, got %d", len(calls))
		}
		opts, _ := calls[0].Args["opts"].(map[string]any)
		if opts["timeout"] != float64(45000) {
			t.Errorf("ui.confirm opts.timeout = %#v, want 45000", opts["timeout"])
		}
		if !strings.Contains(errStr, "The user did not answer the approval prompt for changing `advisor.enabled` in time") {
			t.Errorf("unexpected timeout error: %q", errStr)
		}
	})
}

func TestCfgProtocolPromptDocAndComplete(t *testing.T) {
	h := NewCfgHandler()
	if doc := h.PromptDoc(urls.SchemeHost{SettingsApproval: false}); doc != "" {
		t.Errorf("expected empty PromptDoc without SettingsApproval, got %q", doc)
	}
	doc := h.PromptDoc(urls.SchemeHost{SettingsApproval: true})
	if !strings.HasPrefix(doc, "cfg://: omp settings;") {
		t.Errorf("unexpected PromptDoc: %q", doc)
	}
	comps, err := h.Complete(urls.Context{}, "advisor/")
	if err != nil {
		t.Fatal(err)
	}
	if len(comps) != 8 {
		t.Errorf("advisor completions = %d, want 8", len(comps))
	}
}

// Multi-session isolation, double Register, and lifecycle.End cleanup test:
// two sessions in one process (each with its own Extension/Register), one ends
// via lifecycle.End (and End twice), the other's runtime overrides and cfg://
// session grants remain completely intact, while the first session's state is released.
func TestMultiSessionLifecycleAndDoubleRegister(t *testing.T) {
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

	// Build two fresh Extension instances and call Register twice (simulating PiG's per-session instantiation).
	ext1 := sdk.New("settings-sess-1")
	Register(ext1)
	ext2 := sdk.New("settings-sess-2")
	Register(ext2)

	var read1, read2 string
	ext1.Tool("s1_op", "s1", map[string]any{"type": "object"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		if v, ok := params["set"].(string); ok {
			if err := Set(ctx, "theme.dark", v, false); err != nil {
				return sdk.ToolResult{Content: err.Error(), IsError: true}, nil
			}
		}
		read1 = String(ctx, "theme.dark")
		return sdk.ToolResult{Content: read1}, nil
	})
	ext2.Tool("s2_op", "s2", map[string]any{"type": "object"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		if v, ok := params["set"].(string); ok {
			if err := Set(ctx, "theme.dark", v, false); err != nil {
				return sdk.ToolResult{Content: err.Error(), IsError: true}, nil
			}
		}
		read2 = String(ctx, "theme.dark")
		return sdk.ToolResult{Content: read2}, nil
	})

	h1 := startHost(t, ext1, hostOptions{Mode: "tui", Cwd: cwd, SessionID: "sess-1"})
	h2 := startHost(t, ext2, hostOptions{Mode: "tui", Cwd: cwd, SessionID: "sess-2"})

	// Distinct session overrides in the same cwd must not bleed into each other.
	_, _ = h1.callTool("s1_op", map[string]any{"set": "nord"})
	_, _ = h2.callTool("s2_op", map[string]any{"set": "gruvbox"})
	if read1 != "nord" || read2 != "gruvbox" {
		t.Fatalf("session overrides collided: s1=%q s2=%q", read1, read2)
	}

	// Record session grants for both sessions.
	setSessionGrant("sess-1", true)
	setSessionGrant("sess-2", false)

	// End the FIRST session via lifecycle.End (twice, plus unknown id).
	lifecycle.End("sess-1")
	lifecycle.End("sess-1")
	lifecycle.End("unknown-session-id")

	// Session 1's cached store and grant must be released; next read returns schema default ("titanium").
	_, _ = h1.callTool("s1_op", map[string]any{})
	if read1 != "titanium" {
		t.Errorf("sess-1 override survived lifecycle.End: got %q, want default titanium", read1)
	}
	if _, has1 := getSessionGrant("sess-1"); has1 {
		t.Error("sess-1 approval grant was not released on lifecycle.End")
	}
	if g2, has2 := getSessionGrant("sess-2"); !has2 || g2.save {
		t.Errorf("sess-2 approval grant disturbed by sess-1 End: (%+v, %v)", g2, has2)
	}

	// Session 2 must still have its runtime override intact.
	_, _ = h2.callTool("s2_op", map[string]any{})
	if read2 != "gruvbox" {
		t.Errorf("sess-2 override lost when sess-1 ended: got %q, want gruvbox", read2)
	}

	// Ending session 2 via session_shutdown event releases session 2 only.
	_, fail := h2.fire(sdk.EventSessionShutdown, map[string]any{"type": sdk.EventSessionShutdown, "reason": "quit"})
	if fail != "" {
		t.Fatalf("session_shutdown failed: %s", fail)
	}
	if _, has2After := getSessionGrant("sess-2"); has2After {
		t.Error("sess-2 approval grant was not released on session_shutdown")
	}
}
