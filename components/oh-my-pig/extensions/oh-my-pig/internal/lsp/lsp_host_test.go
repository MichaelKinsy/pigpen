package lsp

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/gate"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/hashline"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/toolcmd"
)

// upstream: packages/coding-agent/src/jsonrpc/message-framing.ts
func TestMessageFramerResyncAndErrors(t *testing.T) {
	var f messageFramer
	raw := "Wrapper: stdout noise\r\n\r\n" +
		"Content-Length: 17\r\n\r\n" +
		`{"jsonrpc":"2.0"}`
	resyncs := 0
	var got [][]byte
	for i := range len(raw) {
		if err := f.push([]byte{raw[i]}); err != nil {
			t.Fatalf("push err: %v", err)
		}
		msgs, err := f.drain(func(string) { resyncs++ })
		if err != nil {
			t.Fatalf("drain err: %v", err)
		}
		got = append(got, msgs...)
	}
	if resyncs != 1 || len(got) != 1 || string(got[0]) != `{"jsonrpc":"2.0"}` {
		t.Fatalf("resyncs=%d got=%q", resyncs, got)
	}

	var bad messageFramer
	_ = bad.push([]byte("Content-Length: 2\r\nContent-Length: 2\r\n\r\n{}"))
	if _, err := bad.drain(nil); err == nil {
		t.Fatalf("expected error on duplicate Content-Length")
	}
}

// upstream: lsp-regressions.test.ts: "builds every go.work use module when go.work and go.mod coexist"
func TestWorkspaceDiagnosticsHelpers(t *testing.T) {
	patterns := parseGoWorkspaceBuildPatterns(`{"Use":[{"DiskPath":"."},{"DiskPath":"./service"},{"DiskPath":"tools/helper"}]}`)
	want := "./...,./service/...,./tools/helper/..."
	if strings.Join(patterns, ",") != want {
		t.Fatalf("patterns = %v, want %s", patterns, want)
	}
	if msg := interpretEmptyDiagnosticsResult(0, []string{"go", "build", "./..."}); msg != "No issues found" {
		t.Fatalf("exit 0 msg = %q", msg)
	}
	if msg := interpretEmptyDiagnosticsResult(2, []string{"go", "build", "./..."}); !strings.Contains(msg, "exited with code 2") {
		t.Fatalf("exit 2 msg = %q", msg)
	}
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]\nname = \"x\"\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte("{}\n"), 0o644)
	pts := detectProjectTypes(context.Background(), dir)
	if len(pts) != 2 || pts[0].Type != "rust" || pts[1].Type != "typescript" {
		t.Fatalf("polyglot detectProjectTypes = %+v", pts)
	}
}

// upstream: lsp-regressions.test.ts: "rename_file applies LSP willRenameFiles edits and renames the file", "resolves code actions before applying edits", "workspace reload replaces a client whose process or initialization config changed"
func TestRenameFileCodeActionResolveAndReload(t *testing.T) {
	isolateEnv(t)
	setTestHooks(t, true, "sess-rename-file", nil)
	dir := t.TempDir()
	srcFile := filepath.Join(dir, "old.fake")
	impFile := filepath.Join(dir, "importer.fake")
	destFile := filepath.Join(dir, "new.fake")

	if err := os.WriteFile(srcFile, []byte("export const val = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(impFile, []byte("import './old.fake'\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	impURI := fileToUri(impFile)
	srcURI := fileToUri(srcFile)

	spec := fakeServerSpec{
		WillRenameEdit: &WorkspaceEdit{
			Changes: map[string][]TextEdit{
				impURI: {{Range: Range{Start: Position{0, 10}, End: Position{0, 18}}, NewText: "new.fake"}},
			},
		},
		CodeActions: []json.RawMessage{
			json.RawMessage(`{"title":"Add import from \"./utils\"","kind":"quickfix","data":{"id":7}}`),
		},
		ResolvedAction: json.RawMessage(`{"title":"Add import from \"./utils\"","kind":"quickfix","edit":{"changes":{"` + srcURI + `":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}},"newText":"// resolved\n"}]}}}`),
	}

	cfg := fakeServerConfig(t, ".fake", spec)
	writeLspJSON(t, dir, "fake-lsp", cfg)
	InvalidateConfig(dir)
	defer InvalidateConfig(dir)

	mgr := newClientManager()
	defer mgr.ShutdownAll()
	ctx := context.Background()
	var zeroSDK sdk.Context

	// 0. All-digit out-of-range query must NOT fall back to title substring matching (tool.ts:1730-1744).
	badIdx, err := executeLspTool(ctx, zeroSDK, mgr, dir, LspParams{
		Action: "code_actions", File: "old.fake", Line: new(1), Symbol: "val", Apply: new(true), Query: "99",
	})
	if err != nil || !strings.Contains(badIdx.Content, `No code action matches "99"`) {
		t.Fatalf("out-of-range numeric query: err=%v, out=%s", err, badIdx.Content)
	}

	// 1. code_actions apply triggers codeAction/resolve, prints plain quotes (not %q escaped).
	caRes, err := executeLspTool(ctx, zeroSDK, mgr, dir, LspParams{
		Action: "code_actions", File: "old.fake", Line: new(1), Symbol: "val", Apply: new(true), Query: "Add import",
	})
	if err != nil || !strings.Contains(caRes.Content, `Applied "Add import from "./utils"":`) {
		t.Fatalf("codeAction/resolve apply should use plain quotes: err=%v, out=%s", err, caRes.Content)
	}
	srcAfterCA, _ := os.ReadFile(srcFile)
	if !strings.HasPrefix(string(srcAfterCA), "// resolved\n") {
		t.Fatalf("expected resolved edit on disk, got:\n%s", string(srcAfterCA))
	}
	// 2. rename_file preview does not move file; apply rewrites importer + moves file.
	prev, err := executeLspTool(ctx, zeroSDK, mgr, dir, LspParams{
		Action: "rename_file", File: "old.fake", NewName: "new.fake", Apply: new(false),
	})
	if err != nil || !strings.Contains(prev.Content, "Rename preview:") {
		t.Fatalf("rename_file preview: err=%v, out=%s", err, prev.Content)
	}
	if _, statErr := os.Stat(srcFile); statErr != nil {
		t.Fatalf("preview must not move source file")
	}

	applied, err := executeLspTool(ctx, zeroSDK, mgr, dir, LspParams{
		Action: "rename_file", File: "old.fake", NewName: "new.fake",
	})
	if err != nil || !strings.Contains(applied.Content, "Renamed old.fake → new.fake") {
		t.Fatalf("rename_file apply: err=%v, out=%s", err, applied.Content)
	}
	if _, statErr := os.Stat(srcFile); !os.IsNotExist(statErr) {
		t.Fatalf("old.fake should no longer exist after rename_file")
	}
	if _, statErr := os.Stat(destFile); statErr != nil {
		t.Fatalf("new.fake should exist after rename_file: %v", statErr)
	}
	impAfter, _ := os.ReadFile(impFile)
	if !strings.Contains(string(impAfter), "./new.fake") {
		t.Fatalf("importer not rewritten: %q", string(impAfter))
	}

	// 3. reload * re-reads config and replaces a client whose config changed.
	relRes, err := executeLspTool(ctx, zeroSDK, mgr, dir, LspParams{Action: "reload", File: "*"})
	if err != nil || !strings.Contains(relRes.Content, "Reloaded fake-lsp") {
		t.Fatalf("reload *: err=%v, out=%s", err, relRes.Content)
	}
}

// =============================================================================
// Review Fix Round Tests: Trust, UTF-16 Columns, YAML, Gate, Multi-Session
// Lifecycle, Mid-Init Shutdown, Wedged Stdin, Concurrency, Slow Diag, lsp.enabled
// =============================================================================

func TestProjectTrustGating(t *testing.T) {
	isolateEnv(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "main.trusttest")
	if err := os.WriteFile(target, []byte("var x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Place a project lsp.json AND a project-local bin/gopls in an untrusted repo.
	markerFile := filepath.Join(dir, "evil-ran.marker")
	evilCfg := ServerConfig{
		Command:     os.Args[0],
		Args:        []string{"-test.run=^TestFakeLSPHelperProcess$"},
		FileTypes:   []string{".trusttest"},
		RootMarkers: []string{"."},
	}
	writeLspJSON(t, dir, "evil-project-lsp", evilCfg)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/untrusted\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	localGopls := filepath.Join(dir, "bin", "gopls")
	if err := os.WriteFile(localGopls, []byte("#!/bin/sh\necho ran > "+markerFile+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	InvalidateConfig(dir)
	defer InvalidateConfig(dir)

	origLookPath := lookPathFunc
	lookPathFunc = func(string) (string, error) { return "", exec.ErrNotFound }
	defer func() { lookPathFunc = origLookPath }()

	// Untrusted: project lsp.json and project-local bin/gopls are BOTH skipped.
	untrustedCfg := LoadConfigWithTrust(dir, false)
	if _, exists := untrustedCfg.Servers["evil-project-lsp"]; exists {
		t.Fatalf("untrusted project must ignore project lsp.json")
	}
	if _, exists := untrustedCfg.Servers["gopls"]; exists {
		t.Fatalf("untrusted project must ignore project-local bin/gopls")
	}
	if !untrustedCfg.ProjectConfigSkipped {
		t.Fatalf("expected ProjectConfigSkipped=true for untrusted project with project lsp.json")
	}

	// Tool status in an untrusted session emits the note ONCE, then suppresses it on the second call.
	setTestHooks(t, false, "sess-untrusted", nil)
	mgr := newClientManager()
	defer mgr.ShutdownAll()
	var zeroSDK sdk.Context

	res1, err := executeLspTool(context.Background(), zeroSDK, mgr, dir, LspParams{Action: "status"})
	if err != nil {
		t.Fatalf("untrusted status 1: %v", err)
	}
	if !strings.Contains(res1.Content, "not trusted") {
		t.Fatalf("expected untrusted project note on first call, got:\n%s", res1.Content)
	}
	res2, err := executeLspTool(context.Background(), zeroSDK, mgr, dir, LspParams{Action: "status"})
	if err != nil {
		t.Fatalf("untrusted status 2: %v", err)
	}
	if strings.Contains(res2.Content, "not trusted") {
		t.Fatalf("untrusted project note should be emitted only once per session, got:\n%s", res2.Content)
	}

	// Trusted: project lsp.json and project-local bin/gopls ARE loaded.
	trustedCfg := LoadConfigWithTrust(dir, true)
	if _, exists := trustedCfg.Servers["evil-project-lsp"]; !exists {
		t.Fatalf("trusted project should load project lsp.json")
	}
	if g, exists := trustedCfg.Servers["gopls"]; !exists || g.ResolvedCommand != localGopls {
		t.Fatalf("trusted project should resolve project-local bin/gopls, got %+v", g)
	}
	if trustedCfg.ProjectConfigSkipped {
		t.Fatalf("trusted project must not set ProjectConfigSkipped")
	}
}

// TestIdleCheckerNeverLoadsTrustedConfigOrExecsProjectBinaries verifies that
// starting a PATH-configured server inside an untrusted project (which triggers
// maybeStartIdleCheckerLocked and checkIdleClients) NEVER populates the trusted
// configCache entry and NEVER executes any binary inside the project directory
// (including node_modules/.bin/tsc via selectTypescriptServer).
func TestIdleCheckerNeverLoadsTrustedConfigOrExecsProjectBinaries(t *testing.T) {
	isolateEnv(t)
	dir := t.TempDir()

	// Untrusted project has package.json + non-npm node_modules/.bin/tsc that
	// writes a marker file if executed, plus a project lsp.json with idleTimeoutMs.
	markerFile := filepath.Join(dir, "project-bin-exec.marker")
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"untrusted"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nodeBinDir := filepath.Join(dir, "node_modules", ".bin")
	if err := os.MkdirAll(nodeBinDir, 0o755); err != nil {
		t.Fatal(err)
	}
	evilTsc := filepath.Join(nodeBinDir, "tsc")
	if err := os.WriteFile(evilTsc, []byte("#!/bin/sh\necho pwned > "+markerFile+"\necho Version 7.0.0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lsp.json"), []byte(`{"idleTimeoutMs": 5000, "servers": {"evil-proj": {"command": "`+evilTsc+`", "fileTypes": [".fake"], "rootMarkers": ["."]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	srcFile := filepath.Join(dir, "app.fake")
	if err := os.WriteFile(srcFile, []byte("func hi() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Configure a user-level (trusted-PATH / user config) server for .fake.
	userAgentDir := os.Getenv("PI_CODING_AGENT_DIR")
	if err := os.MkdirAll(userAgentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	srvCfg := fakeServerConfig(t, ".fake", fakeServerSpec{HoverText: "func hi()"})
	writeLspJSON(t, userAgentDir, "user-fake-lsp", srvCfg)

	InvalidateConfig(dir)
	defer InvalidateConfig(dir)

	origLookPath := lookPathFunc
	lookPathFunc = func(string) (string, error) { return "", exec.ErrNotFound }
	defer func() { lookPathFunc = origLookPath }()

	setTestHooks(t, false, "sess-untrusted-idle", nil)
	mgr := newClientManager()
	defer mgr.ShutdownAll()
	var zeroSDK sdk.Context

	// Run an LSP tool action in the untrusted workspace -> spawns user-fake-lsp
	// and runs maybeStartIdleCheckerLocked().
	res, err := executeLspTool(context.Background(), zeroSDK, mgr, dir, LspParams{
		Action: "hover", File: "app.fake", Line: new(1), Symbol: "hi",
	})
	if err != nil || !strings.Contains(res.Content, "func hi()") {
		t.Fatalf("untrusted hover via user server failed: err=%v, out=%s", err, res.Content)
	}

	// Trigger background idle sweep directly.
	mgr.checkIdleClients()

	// 1. No binary from the project directory may have executed.
	if _, statErr := os.Stat(markerFile); !os.IsNotExist(statErr) {
		t.Fatalf("project-local binary was executed for untrusted project (marker %s exists)", markerFile)
	}

	// 2. The trusted config cache entry for dir must NEVER have been populated.
	cleanDir := filepath.Clean(dir)
	configCacheMu.Lock()
	_, hasTrusted := configCache[configCacheKey{cwd: cleanDir, trusted: true}]
	_, hasUntrusted := configCache[configCacheKey{cwd: cleanDir, trusted: false}]
	configCacheMu.Unlock()
	if hasTrusted {
		t.Fatalf("trusted configCache entry was created for untrusted workspace %s", cleanDir)
	}
	if !hasUntrusted {
		t.Fatalf("expected untrusted configCache entry for %s", cleanDir)
	}
}

func TestUTF16SymbolColumnsBMPAndAstral(t *testing.T) {
	isolateEnv(t)
	setTestHooks(t, true, "sess-utf16", nil)
	dir := t.TempDir()
	srcFile := filepath.Join(dir, "unicode.fake")
	// Line 1 (BMP CJK): `x := "日本語"; target()` -> byte offset 18, UTF-16 offset 12.
	// Line 2 (Astral emoji U+1F600 = 2 UTF-16 units): `y := "😀"; target()` -> byte offset 13, UTF-16 offset 11.
	content := "x := \"日本語\"; target()\ny := \"😀\"; target()\n"
	if err := os.WriteFile(srcFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	colBMP, err := resolveSymbolColumn(srcFile, 1, "target")
	if err != nil || colBMP != 12 {
		t.Fatalf("BMP line UTF-16 column = %d (err=%v), want 12", colBMP, err)
	}
	colAstral, err := resolveSymbolColumn(srcFile, 2, "target")
	if err != nil || colAstral != 11 {
		t.Fatalf("Astral line UTF-16 column = %d (err=%v), want 11", colAstral, err)
	}

	// Verify end-to-end through fake server that receives LSP Position.character.
	cfg := fakeServerConfig(t, ".fake", fakeServerSpec{EchoHoverPos: true})
	writeLspJSON(t, dir, "utf16-lsp", cfg)
	InvalidateConfig(dir)
	defer InvalidateConfig(dir)

	mgr := newClientManager()
	defer mgr.ShutdownAll()
	var zeroSDK sdk.Context

	res1, err := executeLspTool(context.Background(), zeroSDK, mgr, dir, LspParams{
		Action: "hover", File: "unicode.fake", Line: new(1), Symbol: "target",
	})
	if err != nil || res1.Content != "pos=0:12" {
		t.Fatalf("hover BMP pos = %q (err=%v), want pos=0:12", res1.Content, err)
	}
	res2, err := executeLspTool(context.Background(), zeroSDK, mgr, dir, LspParams{
		Action: "hover", File: "unicode.fake", Line: new(2), Symbol: "target",
	})
	if err != nil || res2.Content != "pos=1:11" {
		t.Fatalf("hover Astral pos = %q (err=%v), want pos=1:11", res2.Content, err)
	}
}

func TestYAMLIndentedSequencesNoPiDirsAndPerFieldDecode(t *testing.T) {
	isolateEnv(t)
	dir := t.TempDir()

	// 1. Indented YAML block sequence (`args:\n      - --stdio`) parses cleanly.
	yamlContent := `servers:
  my-yaml-lsp:
    command: ` + os.Args[0] + `
    args:
      - --stdio
      - --verbose
    fileTypes:
      - .ylsp
    rootMarkers:
      - .
    settings:
      strict: true
`
	if err := os.WriteFile(filepath.Join(dir, "lsp.yaml"), []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded := LoadConfigWithTrust(dir, true)
	srv, ok := loaded.Servers["my-yaml-lsp"]
	if !ok {
		t.Fatalf("expected my-yaml-lsp parsed from indented YAML sequence, got %+v", loaded.Servers)
	}
	if strings.Join(srv.Args, ",") != "--stdio,--verbose" {
		t.Fatalf("YAML args = %v, want [--stdio --verbose]", srv.Args)
	}

	// 2. `.pi/lsp.json` is ignored (omp v19.1.1 does not read .pi directories).
	piDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(piDir, ".pi"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeLspJSON(t, filepath.Join(piDir, ".pi"), "pi-only-lsp", ServerConfig{
		Command:     os.Args[0],
		FileTypes:   []string{".pifake"},
		RootMarkers: []string{"."},
	})
	piLoaded := LoadConfigWithTrust(piDir, true)
	if _, exists := piLoaded.Servers["pi-only-lsp"]; exists {
		t.Fatalf(".pi/lsp.json must not be read (omp v19.1.1 does not read .pi dirs)")
	}

	// 3. Per-field decode: a bad optional field in one server or one invalid server does not discard valid servers.
	jsonDir := t.TempDir()
	rawJSON := `{
  "servers": {
    "bad-server": {"command": 123, "fileTypes": [".bad"]},
    "good-server": {
      "command": ` + strconv.Quote(os.Args[0]) + `,
      "args": ["--stdio"],
      "fileTypes": [".good"],
      "rootMarkers": ["."],
      "warmupTimeoutMs": "not-a-number"
    }
  }
}`
	if err := os.WriteFile(filepath.Join(jsonDir, "lsp.json"), []byte(rawJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	jLoaded := LoadConfigWithTrust(jsonDir, true)
	if _, ok := jLoaded.Servers["good-server"]; !ok {
		t.Fatalf("good-server should survive a sibling invalid server and mistyped optional field")
	}
}

func TestGateBlocksWorkspaceEditsAndRenameFile(t *testing.T) {
	isolateEnv(t)
	setTestHooks(t, true, "sess-gate", nil)
	dir := t.TempDir()
	f1 := filepath.Join(dir, "a.fake")
	if err := os.WriteFile(f1, []byte("func foo() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var gatedOps []gate.Request
	gate.SetPolicy(func(_ sdk.Context, r gate.Request) error {
		gatedOps = append(gatedOps, r)
		return errors.New("blocked by plan mode")
	})
	defer gate.ResetForTests()

	we := &WorkspaceEdit{
		Changes: map[string][]TextEdit{
			fileToUri(f1): {{Range: Range{Start: Position{0, 5}, End: Position{0, 8}}, NewText: "bar"}},
		},
	}
	var zeroSDK sdk.Context
	if _, err := applyWorkspaceEdit(zeroSDK, we, dir, nil); err == nil || !strings.Contains(err.Error(), "blocked by plan mode") {
		t.Fatalf("expected gate denial on WorkspaceEdit, got %v", err)
	}
	after, _ := os.ReadFile(f1)
	if string(after) != "func foo() {}\n" {
		t.Fatalf("gated file must not be modified on disk")
	}
	if len(gatedOps) != 1 || gatedOps[0].Op != gate.OpEdit || gatedOps[0].Tool != "lsp" {
		t.Fatalf("unexpected gate request: %+v", gatedOps)
	}

	// rename_file also checks gate before touching disk.
	gatedOps = nil
	if err := applyEditsThenRename(zeroSDK, nil, f1, filepath.Join(dir, "b.fake")); err == nil || !strings.Contains(err.Error(), "blocked by plan mode") {
		t.Fatalf("expected gate denial on applyEditsThenRename, got %v", err)
	}
	if len(gatedOps) != 1 || gatedOps[0].Op != gate.OpMove {
		t.Fatalf("expected OpMove gate request, got %+v", gatedOps)
	}
}

// upstream: lsp-writethrough-gate.test.ts: "does not start a language server for a write when LSP is disabled"
func TestLspDisabledStartsNoServerAndEnabledPredicate(t *testing.T) {
	isolateEnv(t)
	setTestHooks(t, true, "sess-disabled", map[string]any{"lsp.enabled": false})
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "should-not-start.pid")
	target := filepath.Join(dir, "code.fake")
	if err := os.WriteFile(target, []byte("var x = TYPE_ERROR\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := fakeServerConfig(t, ".fake", fakeServerSpec{PIDFile: pidFile})
	writeLspJSON(t, dir, "fake-lsp", cfg)
	InvalidateConfig(dir)
	defer InvalidateConfig(dir)

	var zeroSDK sdk.Context
	if Enabled(zeroSDK) {
		t.Fatalf("Enabled(ctx) must return false when lsp.enabled=false")
	}
	if out := DiagnosticsAfterWrite(zeroSDK, target); out != "" {
		t.Fatalf("DiagnosticsAfterWrite when disabled = %q, want empty", out)
	}
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Fatalf("LSP server must not start when lsp.enabled=false")
	}
}

// upstream: lsp-regressions.test.ts: "shutdownAll shuts down mid-initialize clients" & "bounds a wedged notification flush (#3962)"
func TestShutdownAllKillsMidInitAndWedgedStdin(t *testing.T) {
	isolateEnv(t)
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "mid-init.pid")
	cfg := fakeServerConfig(t, ".hang", fakeServerSpec{Mode: "hang_init", PIDFile: pidFile})

	mgr := newClientManager()
	initDone := make(chan error, 1)
	go func() {
		_, err := mgr.GetOrCreateClient(context.Background(), cfg, dir, 30*time.Second)
		initDone <- err
	}()

	// Wait until the helper server is actually running and mid-initialize.
	var pid int
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(pidFile); err == nil {
			if n, convErr := strconv.Atoi(strings.TrimSpace(string(b))); convErr == nil && n > 0 {
				pid = n
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pid <= 0 {
		t.Fatalf("mid-init helper never wrote PID file")
	}

	mgr.ShutdownAll()
	<-initDone

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && isProcessAlive(pid) {
		time.Sleep(20 * time.Millisecond)
	}
	if isProcessAlive(pid) {
		t.Fatalf("mid-initialize server PID %d outlived ShutdownAll", pid)
	}

	// Wedged stdin: a server that stops reading stdin after initialized must
	// honor caller context deadline instead of hanging forever.
	dir2 := t.TempDir()
	pidFile2 := filepath.Join(dir2, "wedged-stdin.pid")
	cfg2 := fakeServerConfig(t, ".wedge2", fakeServerSpec{Mode: "wedge_stdin", PIDFile: pidFile2})
	mgr2 := newClientManager()
	defer mgr2.ShutdownAll()

	client, err := mgr2.GetOrCreateClient(context.Background(), cfg2, dir2, 5*time.Second)
	if err != nil {
		t.Fatalf("start wedge_stdin client: %v", err)
	}
	// Send a large payload that overflows the OS pipe buffer (~64KB) with a short ctx timeout.
	bigPayload := strings.Repeat("A", 512*1024)
	wCtx, wCancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer wCancel()
	start := time.Now()
	wErr := client.SendNotification(wCtx, "textDocument/didOpen", map[string]any{"text": bigPayload})
	if wErr == nil {
		t.Fatalf("expected wedged stdin write to fail on context timeout")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("wedged stdin write hung past timeout: %v", time.Since(start))
	}
}

func TestSlowDiagnosticsUnreportedNoticeAndConcurrentSafety(t *testing.T) {
	isolateEnv(t)
	setTestHooks(t, true, "sess-slow", nil)
	dir := t.TempDir()
	f1 := filepath.Join(dir, "slow.fake")
	if err := os.WriteFile(f1, []byte("var x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// slow_diag never publishes diagnostics and never ends $/progress; tool reports unverified, never "OK".
	cfg := fakeServerConfig(t, ".fake", fakeServerSpec{Mode: "slow_diag"})
	writeLspJSON(t, dir, "slow-lsp", cfg)
	InvalidateConfig(dir)
	defer InvalidateConfig(dir)

	mgr := newClientManager()
	defer mgr.ShutdownAll()
	var zeroSDK sdk.Context

	// Lower singleDiagnosticsWaitTimeout via a tight 5s call budget + direct waitForDiagnostics check.
	client, err := mgr.GetOrCreateClientForSession(context.Background(), "sess-slow", cfg, dir, 5*time.Second)
	if err != nil {
		t.Fatalf("GetOrCreateClient: %v", err)
	}
	diags, reported, wErr := waitForDiagnostics(context.Background(), client, fileToUri(f1), waitDiagnosticsOptions{
		timeout: 150 * time.Millisecond,
		settle:  50 * time.Millisecond,
	})
	if wErr != nil || reported || diags != nil {
		t.Fatalf("expected unreported (nil, false, nil) on slow_diag timeout, got (%v, %v, %v)", diags, reported, wErr)
	}

	// Concurrent lock-order and config-read stress test (verifies no deadlock or map race under -race).
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ctx := context.Background()
			_ = mgr.NotifyWorkspaceWatchedFiles(ctx, dir, []WatchedFileChange{
				{FilePath: f1, Type: FileChangeChanged},
			})
			_ = mgr.ActiveClients()
			_, _ = mgr.GetOrCreateClientForSession(ctx, "sess-slow", cfg, dir, 5*time.Second)
			_ = GetServersForFile(GetConfigWithTrust(dir, true), f1)
			_ = describeMissingServers(GetConfigWithTrust(dir, true), filepath.Join(dir, "missing.xyz"))
			_, _ = executeLspTool(ctx, zeroSDK, mgr, dir, LspParams{Action: "status"})
			if idx%2 == 0 {
				mgr.beginPendingDiskWrite(f1)
				mgr.endPendingDiskWrite(f1)
			}
		}(i)
	}
	wg.Wait()
}

// TestTwoSessionsLifecycleIsolationAndDoubleRegister verifies the PiG host
// contract: Register called twice (two session instances in one process), both
// sessions share one workspace server, lifecycle.End(sessionA) leaves sessionB's
// server and dedupe ledger intact, and lifecycle.End(sessionB) stops the server.
func TestTwoSessionsLifecycleIsolationAndDoubleRegister(t *testing.T) {
	isolateEnv(t)
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "shared-two-sess.pid")
	srcFile := filepath.Join(dir, "code.fake")
	if err := os.WriteFile(srcFile, []byte("var x = TYPE_ERROR\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := fakeServerConfig(t, ".fake", fakeServerSpec{
		PIDFile:   pidFile,
		HoverText: "var x: error",
	})
	writeLspJSON(t, dir, "fake-lsp", cfg)
	InvalidateConfig(dir)
	defer func() {
		ShutdownAll()
		InvalidateConfig(dir)
		resetLedgers()
		lifecycle.ResetForTests()
	}()

	// Call Register twice (simulating two PiG session extension instances in one process).
	ext1 := sdk.New("oh-my-pig-sess-1")
	ext2 := sdk.New("oh-my-pig-sess-2")
	Register(ext1)
	Register(ext2)

	var zeroSDK sdk.Context

	// Session A runs lsp hover + DiagnosticsAfterWrite (populates session A's ledger & acquires server ref).
	setTestHooks(t, true, "session-A", nil)
	hovA, err := executeLspTool(context.Background(), zeroSDK, defaultManager, dir, LspParams{
		Action: "hover", File: "code.fake", Line: new(1), Symbol: "x",
	})
	if err != nil || !strings.Contains(hovA.Content, "var x: error") {
		t.Fatalf("session-A hover failed: err=%v, out=%s", err, hovA.Content)
	}
	diagA := DiagnosticsAfterWrite(zeroSDK, srcFile)
	if !strings.Contains(diagA, "type mismatch on TYPE_ERROR") {
		t.Fatalf("session-A DiagnosticsAfterWrite = %q", diagA)
	}

	pidBytes, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("missing PID file: %v", err)
	}
	serverPID, _ := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if serverPID <= 0 {
		t.Fatalf("invalid PID %q", string(pidBytes))
	}

	// Session B uses the same server and populates its own independent dedupe ledger.
	hooksMu.Lock()
	sessionIDResolver = func(sdk.Context) (string, error) { return "session-B", nil }
	hooksMu.Unlock()
	diagB1 := DiagnosticsAfterWrite(zeroSDK, srcFile)
	if !strings.Contains(diagB1, "type mismatch on TYPE_ERROR") {
		t.Fatalf("session-B first DiagnosticsAfterWrite must NOT be suppressed by session-A, got %q", diagB1)
	}

	// End Session A (and call twice to verify idempotence).
	lifecycle.End("session-A")
	lifecycle.End("session-A")

	// Session B's server process must STILL be alive, hover still works, and session B's ledger STILL dedupes!
	if !isProcessAlive(serverPID) {
		t.Fatalf("ending session-A killed server PID %d still held by session-B", serverPID)
	}
	hovB, err := executeLspTool(context.Background(), zeroSDK, defaultManager, dir, LspParams{
		Action: "hover", File: "code.fake", Line: new(1), Symbol: "x",
	})
	if err != nil || !strings.Contains(hovB.Content, "var x: error") {
		t.Fatalf("session-B hover after session-A ended: err=%v, out=%s", err, hovB.Content)
	}
	if diagB2 := DiagnosticsAfterWrite(zeroSDK, srcFile); diagB2 != "" {
		t.Fatalf("session-B dedupe ledger was wiped when session-A ended, got %q", diagB2)
	}

	// End Session B: last holder left, so the server process MUST now exit.
	lifecycle.End("session-B")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && isProcessAlive(serverPID) {
		time.Sleep(20 * time.Millisecond)
	}
	if isProcessAlive(serverPID) {
		t.Fatalf("server PID %d still alive after last holder session-B ended", serverPID)
	}
}

// TestSDKHostRegistrationAndShutdownHook exercises Register(ext) over a real
// PiG SDK net.Pipe host connection: verifies registered wire schema property
// order, trust + session ID RPC queries, tool execution, and session_shutdown
// reaping of spawned LSP processes via lifecycle.End(sessionID).
func TestSDKHostRegistrationAndShutdownHook(t *testing.T) {
	isolateEnv(t)
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "sdk-host.pid")
	srcFile := filepath.Join(dir, "hello.fake")
	if err := os.WriteFile(srcFile, []byte("func hi() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := fakeServerConfig(t, ".fake", fakeServerSpec{
		PIDFile:   pidFile,
		HoverText: "func hi()",
	})
	writeLspJSON(t, dir, "fake-lsp", cfg)
	InvalidateConfig(dir)
	defer func() {
		ShutdownAll()
		InvalidateConfig(dir)
		resetLedgers()
		lifecycle.ResetForTests()
	}()

	ext := sdk.New("oh-my-pig-lsp-test")
	Register(ext)

	hostConn, extConn := net.Pipe()
	runDone := make(chan error, 1)
	go func() { runDone <- ext.RunWithConn(extConn) }()

	var writeMu sync.Mutex
	writeFrame := func(v any) {
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal frame: %v", err)
		}
		var hdr [4]byte
		binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
		writeMu.Lock()
		defer writeMu.Unlock()
		if _, err := hostConn.Write(hdr[:]); err != nil {
			t.Fatalf("write hdr: %v", err)
		}
		if _, err := hostConn.Write(data); err != nil {
			t.Fatalf("write body: %v", err)
		}
	}
	readFrame := func() map[string]json.RawMessage {
		var hdr [4]byte
		if _, err := io.ReadFull(hostConn, hdr[:]); err != nil {
			t.Fatalf("read hdr: %v", err)
		}
		buf := make([]byte, binary.BigEndian.Uint32(hdr[:]))
		if _, err := io.ReadFull(hostConn, buf); err != nil {
			t.Fatalf("read body: %v", err)
		}
		var out map[string]json.RawMessage
		if err := json.Unmarshal(buf, &out); err != nil {
			t.Fatalf("unmarshal frame: %v", err)
		}
		return out
	}

	regFrame := readFrame()
	if string(regFrame["type"]) != `"register"` {
		t.Fatalf("expected register frame, got %s", regFrame["type"])
	}
	var reg struct {
		Tools []struct {
			Name          string          `json:"name"`
			Exposure      string          `json:"exposure"`
			DefaultActive *bool           `json:"default_active"`
			Parameters    json.RawMessage `json:"parameters"`
		} `json:"tools"`
		Handlers []struct {
			ID    int    `json:"handler_id"`
			Event string `json:"event"`
		} `json:"handlers"`
	}
	if err := json.Unmarshal(regFrame["register"], &reg); err != nil {
		t.Fatalf("unmarshal register: %v", err)
	}
	if len(reg.Tools) != 1 || reg.Tools[0].Name != "lsp" {
		t.Fatalf("expected 1 registered tool 'lsp', got %+v", reg.Tools)
	}
	if reg.Tools[0].Exposure != string(sdk.ToolExposureCodemode) {
		t.Fatalf("lsp tool exposure = %q, want %q", reg.Tools[0].Exposure, sdk.ToolExposureCodemode)
	}
	if reg.Tools[0].DefaultActive == nil || *reg.Tools[0].DefaultActive {
		t.Fatalf("lsp tool default_active = %v, want false", reg.Tools[0].DefaultActive)
	}
	var schemaObj struct {
		Properties json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(reg.Tools[0].Parameters, &schemaObj); err != nil {
		t.Fatalf("unmarshal wire schema: %v", err)
	}
	wireKeys := extractTopLevelJSONKeys(schemaObj.Properties)
	wantKeys := "i,action,file,line,symbol,query,new_name,apply,timeout,payload"
	if strings.Join(wireKeys, ",") != wantKeys {
		t.Fatalf("wire schema property order = %v, want %s", wireKeys, wantKeys)
	}
	shutdownHandlerID := -1
	for _, h := range reg.Handlers {
		if h.Event == sdk.EventSessionShutdown {
			shutdownHandlerID = h.ID
		}
	}
	if shutdownHandlerID < 0 {
		t.Fatalf("session_shutdown handler not registered")
	}

	stateJSON, _ := json.Marshal(map[string]any{"hasUI": false})
	settingsJSON, _ := json.Marshal(map[string]any{"lsp": map[string]any{"enabled": true, "lazy": true}})
	writeFrame(map[string]any{
		"type": "ready",
		"ready": map[string]any{
			"session_name": "",
			"cwd":          dir,
			"mode":         "print",
			"width":        80,
			"state":        json.RawMessage(stateJSON),
			"settings":     json.RawMessage(settingsJSON),
		},
	})

	// Answer host calls from the extension (isProjectTrusted, sessionRead getSessionId).
	readResponse := func(wantID string) map[string]json.RawMessage {
		for {
			f := readFrame()
			switch string(f["type"]) {
			case `"request_state"`:
				continue
			case `"call"`:
				var call struct {
					Method string          `json:"method"`
					Args   json.RawMessage `json:"args"`
				}
				_ = json.Unmarshal(f["call"], &call)
				var callID string
				_ = json.Unmarshal(f["id"], &callID)
				var res any
				switch call.Method {
				case "isProjectTrusted":
					res = map[string]any{"trusted": true}
				case "sessionRead":
					res = "sdk-host-sess-1"
				default:
					res = map[string]any{}
				}
				writeFrame(map[string]any{
					"type":        "call_result",
					"id":          callID,
					"call_result": map[string]any{"result": res},
				})
				continue
			case `"response"`:
				if string(f["id"]) != `"`+wantID+`"` {
					t.Fatalf("unexpected response id %s waiting for %s", f["id"], wantID)
				}
				return f
			default:
				t.Fatalf("unexpected frame waiting for %s: %+v", wantID, f)
			}
		}
	}

	argsJSON, _ := json.Marshal(map[string]any{"action": "hover", "file": "hello.fake", "line": 1, "symbol": "hi"})
	writeFrame(map[string]any{
		"type": "request",
		"id":   "req-1",
		"request": map[string]any{
			"method":       "tool_call",
			"tool":         "lsp",
			"tool_call_id": "tc-1",
			"args":         json.RawMessage(argsJSON),
		},
	})

	respFrame := readResponse("req-1")
	if !strings.Contains(string(respFrame["response"]), "func hi()") {
		t.Fatalf("expected hover result in SDK response, got %s", respFrame["response"])
	}

	pidBytes, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("expected spawned server PID file: %v", err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if pid <= 0 {
		t.Fatalf("invalid PID %q", string(pidBytes))
	}

	writeFrame(map[string]any{
		"type": "request",
		"id":   "req-2",
		"request": map[string]any{
			"method":     "event",
			"handler_id": shutdownHandlerID,
			"event":      sdk.EventSessionShutdown,
			"args":       map[string]any{},
		},
	})
	_ = readResponse("req-2")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && isProcessAlive(pid) {
		time.Sleep(20 * time.Millisecond)
	}
	if isProcessAlive(pid) {
		t.Fatalf("LSP server PID %d still alive after session_shutdown event", pid)
	}

	_ = hostConn.Close()
	<-runDone
}

// TestMountedCodemodeExposureAndSessionHashlineStore verifies:
//  1. A fake PiG host enforcing the host rule (`direct` tools are callable via
//     executeTool only while active; `codemode` tools are always callable even
//     when inactive / mounted as bash tool commands) executes `lsp` when
//     mounted through `internal/toolcmd`.
//  2. `lsp rename` records the new snapshot into `hashline.ForSession(ctx)`
//     (NOT `hashline.Default()`), so the same session's `edit` sees the new tag
//     while a different session's store does not.
func TestMountedCodemodeExposureAndSessionHashlineStore(t *testing.T) {
	isolateEnv(t)
	dir := t.TempDir()
	srcFile := filepath.Join(dir, "app.fake")
	if err := os.WriteFile(srcFile, []byte("func oldName() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srcURI := fileToUri(srcFile)
	cfg := fakeServerConfig(t, ".fake", fakeServerSpec{
		HoverText: "func oldName()",
		RenameEdit: &WorkspaceEdit{
			Changes: map[string][]TextEdit{
				srcURI: {{Range: Range{Start: Position{0, 5}, End: Position{0, 12}}, NewText: "newName"}},
			},
		},
	})
	writeLspJSON(t, dir, "fake-lsp", cfg)
	InvalidateConfig(dir)
	defer func() {
		ShutdownAll()
		InvalidateConfig(dir)
		resetLedgers()
		lifecycle.ResetForTests()
		toolcmd.ResetForTests()
		hashline.RegisterLifecycle()
	}()

	const sessID = "mounted-sess-1"
	ext := sdk.New("oh-my-pig-mounted-test")
	// Register a direct bash transport tool + lsp (codemode, DefaultActive=false) + toolcmd.
	ext.RegisterTool(sdk.ToolDefinition{
		Name:       "bash",
		Label:      "Bash",
		Parameters: sdk.Schema{"type": "object", "properties": map[string]any{}},
		Execute:    func(sdk.Context, map[string]any) (any, error) { return sdk.ToolResult{Content: "ok"}, nil },
	})
	Register(ext)
	toolcmd.Register(ext)

	hostConn, extConn := net.Pipe()
	runDone := make(chan error, 1)
	go func() { runDone <- ext.RunWithConn(extConn) }()
	defer func() {
		_ = hostConn.Close()
		<-runDone
	}()

	var writeMu sync.Mutex
	writeFrame := func(v any) {
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal frame: %v", err)
		}
		var hdr [4]byte
		binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
		writeMu.Lock()
		defer writeMu.Unlock()
		if _, err := hostConn.Write(hdr[:]); err != nil {
			t.Fatalf("write hdr: %v", err)
		}
		if _, err := hostConn.Write(data); err != nil {
			t.Fatalf("write body: %v", err)
		}
	}
	readFrame := func() map[string]json.RawMessage {
		var hdr [4]byte
		if _, err := io.ReadFull(hostConn, hdr[:]); err != nil {
			t.Fatalf("read hdr: %v", err)
		}
		buf := make([]byte, binary.BigEndian.Uint32(hdr[:]))
		if _, err := io.ReadFull(hostConn, buf); err != nil {
			t.Fatalf("read body: %v", err)
		}
		var out map[string]json.RawMessage
		if err := json.Unmarshal(buf, &out); err != nil {
			t.Fatalf("unmarshal frame: %v", err)
		}
		return out
	}

	regFrame := readFrame()
	var reg struct {
		Tools []struct {
			Name          string          `json:"name"`
			Description   string          `json:"description"`
			Exposure      string          `json:"exposure"`
			DefaultActive *bool           `json:"default_active"`
			Parameters    json.RawMessage `json:"parameters"`
		} `json:"tools"`
		Handlers []struct {
			ID    int    `json:"handler_id"`
			Event string `json:"event"`
		} `json:"handlers"`
	}
	if err := json.Unmarshal(regFrame["register"], &reg); err != nil {
		t.Fatalf("unmarshal register: %v", err)
	}

	// Host tracks exposure + active set and enforces PiG's host rule:
	// direct tools are callable only while active; codemode tools are always callable.
	exposures := make(map[string]string)
	active := make(map[string]bool)
	var allToolInfos []map[string]any
	for _, tl := range reg.Tools {
		exp := tl.Exposure
		if exp == "" {
			exp = string(sdk.ToolExposureDirect)
		}
		exposures[tl.Name] = exp
		isAct := exp == string(sdk.ToolExposureDirect)
		if tl.DefaultActive != nil {
			isAct = *tl.DefaultActive
		}
		if isAct {
			active[tl.Name] = true
		}
		var paramsAny any
		_ = json.Unmarshal(tl.Parameters, &paramsAny)
		allToolInfos = append(allToolInfos, map[string]any{
			"name":        tl.Name,
			"description": tl.Description,
			"exposure":    exp,
			"parameters":  paramsAny,
		})
	}
	if exposures["lsp"] != string(sdk.ToolExposureCodemode) {
		t.Fatalf("lsp exposure = %q, want codemode", exposures["lsp"])
	}
	if active["lsp"] {
		t.Fatalf("lsp must start inactive (DefaultActive=false)")
	}

	canExecute := func(toolName string) bool {
		if exposures[toolName] == string(sdk.ToolExposureCodemode) {
			return true
		}
		return active[toolName]
	}

	stateJSON, _ := json.Marshal(map[string]any{"hasUI": false, "session": map[string]any{"sessionId": sessID}})
	settingsJSON, _ := json.Marshal(map[string]any{"lsp": map[string]any{"enabled": true, "lazy": true}})
	writeFrame(map[string]any{
		"type": "ready",
		"ready": map[string]any{
			"session_name": "",
			"cwd":          dir,
			"mode":         "print",
			"width":        80,
			"state":        json.RawMessage(stateJSON),
			"settings":     json.RawMessage(settingsJSON),
		},
	})

	readResponse := func(wantID string) map[string]json.RawMessage {
		for {
			f := readFrame()
			switch string(f["type"]) {
			case `"request_state"`:
				continue
			case `"call"`:
				var call struct {
					Method string          `json:"method"`
					Args   json.RawMessage `json:"args"`
				}
				_ = json.Unmarshal(f["call"], &call)
				var callID string
				_ = json.Unmarshal(f["id"], &callID)
				var res any
				switch call.Method {
				case "isProjectTrusted":
					res = map[string]any{"trusted": true}
				case "getSessionID":
					res = map[string]any{"sessionId": sessID}
				case "sessionRead":
					res = sessID
				case "fileMutation.enter":
					res = map[string]any{"ticket": "mounted-tk-1"}
				case "fileMutation.exit":
					res = map[string]any{}
				case "getAllTools":
					res = map[string]any{"tools": allToolInfos}
				case "getActiveTools":
					var names []string
					for name, ok := range active {
						if ok {
							names = append(names, name)
						}
					}
					res = map[string]any{"tools": names}
				case "setActiveTools":
					var arg struct {
						Tools []string `json:"tools"`
					}
					_ = json.Unmarshal(call.Args, &arg)
					active = make(map[string]bool)
					for _, n := range arg.Tools {
						active[n] = true
					}
					res = map[string]any{}
				default:
					res = map[string]any{}
				}
				writeFrame(map[string]any{
					"type":        "call_result",
					"id":          callID,
					"call_result": map[string]any{"result": res},
				})
				continue
			case `"response"`:
				if string(f["id"]) != `"`+wantID+`"` {
					t.Fatalf("unexpected response id %s waiting for %s", f["id"], wantID)
				}
				return f
			default:
				t.Fatalf("unexpected frame waiting for %s: %+v", wantID, f)
			}
		}
	}

	// Fire session_start handlers (triggers toolcmd mount sync).
	for i, h := range reg.Handlers {
		if h.Event == sdk.EventSessionStart {
			reqID := "start-" + strconv.Itoa(i)
			writeFrame(map[string]any{
				"type": "request",
				"id":   reqID,
				"request": map[string]any{
					"method":     "event",
					"handler_id": h.ID,
					"event":      sdk.EventSessionStart,
					"args":       map[string]any{},
				},
			})
			_ = readResponse(reqID)
		}
	}

	// While mounted as a bash tool command, toolcmd keeps lsp active for fallback
	// direct calls while hiding its declaration via PrepareLoadout.
	if !active["lsp"] {
		t.Fatalf("mounted lsp should remain active for fallback dispatch when tools.commands is on")
	}
	if !canExecute("lsp") {
		t.Fatalf("host exposure rule must allow executing mounted codemode tool lsp")
	}

	// Execute lsp rename through the host and verify hashline.StoreForSession(sessID)
	// receives the updated snapshot (NOT hashline.Default(), NOT another session).
	renameArgs, _ := json.Marshal(map[string]any{
		"action":   "rename",
		"file":     "app.fake",
		"line":     1,
		"symbol":   "oldName",
		"new_name": "newName",
	})
	writeFrame(map[string]any{
		"type": "request",
		"id":   "req-rename",
		"request": map[string]any{
			"method":       "tool_call",
			"tool":         "lsp",
			"tool_call_id": "tc-rename",
			"args":         json.RawMessage(renameArgs),
		},
	})
	resp := readResponse("req-rename")
	if !strings.Contains(string(resp["response"]), "Applied rename:") {
		t.Fatalf("expected Applied rename from mounted lsp tool, got %s", resp["response"])
	}

	afterBytes, err := os.ReadFile(srcFile)
	if err != nil || string(afterBytes) != "func newName() {}\n" {
		t.Fatalf("renamed file content = %q (err=%v)", string(afterBytes), err)
	}
	wantTag := hashline.FileHash(hashline.NormalizeToLF(string(afterBytes)))
	if !hashline.StoreForSession(sessID).HasHashForPath(srcFile, wantTag) {
		t.Fatalf("expected hashline.StoreForSession(%q) to have snapshot tag %s for %s", sessID, wantTag, srcFile)
	}
	if hashline.StoreForSession("other-sess").HasHashForPath(srcFile, wantTag) {
		t.Fatalf("other session's hashline store must NOT receive %q's snapshot", sessID)
	}

	// Fire session_shutdown and verify clean teardown.
	for i, h := range reg.Handlers {
		if h.Event == sdk.EventSessionShutdown {
			reqID := "stop-" + strconv.Itoa(i)
			writeFrame(map[string]any{
				"type": "request",
				"id":   reqID,
				"request": map[string]any{
					"method":     "event",
					"handler_id": h.ID,
					"event":      sdk.EventSessionShutdown,
					"args":       map[string]any{},
				},
			})
			_ = readResponse(reqID)
		}
	}
	// hostConn closed and runDone drained by defer above.
}

// TestServerApplyEditGatedWithLiveContextAndRefusedWithoutContext verifies
// integrator decision (a):
//  1. A server-initiated workspace/applyEdit sent during workspace/executeCommand
//     carries the calling tool's live sdk.Context into gate.Check (tested under a
//     policy that dereferences ctx.GetSessionID() just like modes.planGatePolicy):
//     when the policy blocks that session, the edit is denied with applied:false
//     and disk is untouched; when it allows that session, the edit applies cleanly.
//  2. A server-initiated workspace/applyEdit arriving with no active valid
//     sdk.Context (e.g. unsolicited or zero Context) is refused with applied:false
//     and never calls gate.Check with a zero Context or modifies disk.
func TestServerApplyEditGatedWithLiveContextAndRefusedWithoutContext(t *testing.T) {
	isolateEnv(t)
	dir := t.TempDir()
	srcFile := filepath.Join(dir, "target.fake")
	if err := os.WriteFile(srcFile, []byte("oldValue\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srcURI := fileToUri(srcFile)
	srvEdit := &WorkspaceEdit{
		Changes: map[string][]TextEdit{
			srcURI: {{Range: Range{Start: Position{0, 0}, End: Position{0, 8}}, NewText: "newValue"}},
		},
	}
	cfg := fakeServerConfig(t, ".fake", fakeServerSpec{
		CodeActions: []json.RawMessage{
			json.RawMessage(`{"title":"Apply via server command","command":"fake.applyEdit","arguments":[]}`),
		},
		CommandApplyEdit: srvEdit,
	})
	writeLspJSON(t, dir, "fake-lsp", cfg)
	InvalidateConfig(dir)
	defer func() {
		ShutdownAll()
		InvalidateConfig(dir)
		resetLedgers()
		lifecycle.ResetForTests()
		gate.ResetForTests()
	}()

	// Part 1: unsolicited / zero-Context workspace/applyEdit must be refused without
	// invoking gate.Check (which would panic on ctx.GetSessionID()) and leave disk untouched.
	var policyCalls atomic.Int32
	var blockSession atomic.Bool
	blockSession.Store(true)
	var seenSession atomic.Value
	gate.SetPolicy(func(ctx sdk.Context, r gate.Request) error {
		policyCalls.Add(1)
		// Dereference ctx.GetSessionID() exactly like internal/modes.planGatePolicy!
		sid, err := ctx.GetSessionID()
		if err != nil || sid == "" {
			t.Errorf("gate policy received unresolvable session ctx: sid=%q err=%v", sid, err)
			return errors.New("missing session id")
		}
		seenSession.Store(sid)
		if blockSession.Load() {
			return errors.New("plan mode forbids edits for " + sid)
		}
		return nil
	})

	// Send unsolicited workspace/applyEdit via direct client request without any live sdk.Context.
	mgr := newClientManager()
	defer mgr.ShutdownAll()
	directClient, err := mgr.GetOrCreateClientForSessionWithTrust(context.Background(), "sess-direct", true, 0, cfg, dir, 5*time.Second)
	if err != nil {
		t.Fatalf("start direct client: %v", err)
	}
	rawReply, err := directClient.SendRequest(context.Background(), "test/triggerApplyEdit", map[string]any{}, 5*time.Second)
	if err != nil {
		t.Fatalf("test/triggerApplyEdit: %v", err)
	}
	var applyResp struct {
		Applied       bool   `json:"applied"`
		FailureReason string `json:"failureReason"`
	}
	if err := json.Unmarshal(rawReply, &applyResp); err != nil {
		t.Fatalf("unmarshal applyResp: %v (raw=%s)", err, rawReply)
	}
	if applyResp.Applied {
		t.Fatalf("expected unsolicited workspace/applyEdit to be refused (applied:false), got %+v", applyResp)
	}
	if policyCalls.Load() != 0 {
		t.Fatalf("gate.Check must not be called when no valid sdk.Context exists, got %d calls", policyCalls.Load())
	}
	if got, _ := os.ReadFile(srcFile); string(got) != "oldValue\n" {
		t.Fatalf("unsolicited applyEdit modified disk: %q", string(got))
	}
	mgr.ShutdownAll()

	// Part 2: real SDK net.Pipe host carrying a live sdk.Context with sessionId.
	const sessID = "sess-apply-edit-gate"
	ext := sdk.New("oh-my-pig-applyedit-test")
	Register(ext)
	hostConn, extConn := net.Pipe()
	runDone := make(chan error, 1)
	go func() { runDone <- ext.RunWithConn(extConn) }()
	defer func() {
		_ = hostConn.Close()
		<-runDone
	}()

	var writeMu sync.Mutex
	writeFrame := func(v any) {
		data, _ := json.Marshal(v)
		var hdr [4]byte
		binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
		writeMu.Lock()
		defer writeMu.Unlock()
		_, _ = hostConn.Write(hdr[:])
		_, _ = hostConn.Write(data)
	}
	readFrame := func() map[string]json.RawMessage {
		var hdr [4]byte
		if _, err := io.ReadFull(hostConn, hdr[:]); err != nil {
			t.Fatalf("read hdr: %v", err)
		}
		buf := make([]byte, binary.BigEndian.Uint32(hdr[:]))
		if _, err := io.ReadFull(hostConn, buf); err != nil {
			t.Fatalf("read body: %v", err)
		}
		var out map[string]json.RawMessage
		_ = json.Unmarshal(buf, &out)
		return out
	}
	_ = readFrame() // register

	stateJSON, _ := json.Marshal(map[string]any{"hasUI": false, "session": map[string]any{"sessionId": sessID}})
	settingsJSON, _ := json.Marshal(map[string]any{"lsp": map[string]any{"enabled": true, "lazy": true}})
	writeFrame(map[string]any{
		"type": "ready",
		"ready": map[string]any{
			"session_name": "",
			"cwd":          dir,
			"mode":         "print",
			"width":        80,
			"state":        json.RawMessage(stateJSON),
			"settings":     json.RawMessage(settingsJSON),
		},
	})

	var fmEnters atomic.Int32
	readResponse := func(wantID string) map[string]json.RawMessage {
		for {
			f := readFrame()
			switch string(f["type"]) {
			case `"request_state"`:
				continue
			case `"call"`:
				var call struct {
					Method string          `json:"method"`
					Args   json.RawMessage `json:"args"`
				}
				_ = json.Unmarshal(f["call"], &call)
				var callID string
				_ = json.Unmarshal(f["id"], &callID)
				var res any
				switch call.Method {
				case "isProjectTrusted":
					res = map[string]any{"trusted": true}
				case "getSessionID":
					res = map[string]any{"sessionId": sessID}
				case "sessionRead":
					res = sessID
				case "fileMutation.enter":
					n := fmEnters.Add(1)
					res = map[string]any{"ticket": "tk-" + strconv.Itoa(int(n))}
				case "fileMutation.exit":
					res = map[string]any{}
				default:
					res = map[string]any{}
				}
				writeFrame(map[string]any{
					"type":        "call_result",
					"id":          callID,
					"call_result": map[string]any{"result": res},
				})
				continue
			case `"response"`:
				if string(f["id"]) == `"`+wantID+`"` {
					return f
				}
			}
		}
	}

	// 2a. With policy blocking sessID: code_actions apply triggers workspace/executeCommand
	// -> server sends workspace/applyEdit -> policy dereferences ctx.GetSessionID() -> denied, no panic, disk untouched.
	caArgs, _ := json.Marshal(map[string]any{
		"action": "code_actions", "file": "target.fake", "line": 1, "symbol": "oldValue", "apply": true, "query": "0",
	})
	writeFrame(map[string]any{
		"type": "request", "id": "req-ca-blocked",
		"request": map[string]any{"method": "tool_call", "tool": "lsp", "tool_call_id": "tc-ca-1", "args": json.RawMessage(caArgs)},
	})
	_ = readResponse("req-ca-blocked")
	if policyCalls.Load() < 1 {
		t.Fatalf("expected gate policy to be called during server workspace/applyEdit")
	}
	if got, _ := seenSession.Load().(string); got != sessID {
		t.Fatalf("gate policy saw session %q, want %q", got, sessID)
	}
	if got, _ := os.ReadFile(srcFile); string(got) != "oldValue\n" {
		t.Fatalf("blocked server workspace/applyEdit must not modify disk, got %q", string(got))
	}

	// 2b. With policy allowing sessID: same command applies edit cleanly through the host mutation queue.
	blockSession.Store(false)
	writeFrame(map[string]any{
		"type": "request", "id": "req-ca-allowed",
		"request": map[string]any{"method": "tool_call", "tool": "lsp", "tool_call_id": "tc-ca-2", "args": json.RawMessage(caArgs)},
	})
	_ = readResponse("req-ca-allowed")
	if got, _ := os.ReadFile(srcFile); string(got) != "newValue\n" {
		t.Fatalf("allowed server workspace/applyEdit should update disk to newValue, got %q", string(got))
	}
	if fmEnters.Load() != 1 {
		t.Fatalf("expected exactly 1 host fileMutation.enter call, got %d", fmEnters.Load())
	}

	lifecycle.End(sessID)
}

// TestFusedMutationQueueRunsExactlyOnce verifies integrator decision (c):
//  1. Over a fused in-process pipe connection with PIG_EXT_SOCKET UNSET,
//     LSP mutations go through the host's fileMutation.enter/exit queue
//     exactly once (never bypassing the host queue and never applying twice).
//  2. When ctx is zero (no host connection), fallback to the per-path mutex
//     runs fn exactly once.
//  3. A panic thrown by fn itself (both under a live host connection and under
//     fallback) is propagated and never causes fn to run twice.
//  4. A "not connected" error returned by fn itself (both under a live host
//     connection and under fallback) never causes fn to run twice.
func TestFusedMutationQueueRunsExactlyOnce(t *testing.T) {
	isolateEnv(t)
	t.Setenv("PIG_EXT_SOCKET", "")

	dir := t.TempDir()
	srcFile := filepath.Join(dir, "mut.fake")
	if err := os.WriteFile(srcFile, []byte("count = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srcURI := fileToUri(srcFile)
	cfg := fakeServerConfig(t, ".fake", fakeServerSpec{
		RenameEdit: &WorkspaceEdit{
			Changes: map[string][]TextEdit{
				srcURI: {{Range: Range{Start: Position{0, 0}, End: Position{0, 5}}, NewText: "total"}},
			},
		},
	})
	writeLspJSON(t, dir, "fake-lsp", cfg)
	InvalidateConfig(dir)
	defer func() {
		ShutdownAll()
		InvalidateConfig(dir)
		resetLedgers()
		lifecycle.ResetForTests()
	}()

	// 1. Fused pipe connection (PIG_EXT_SOCKET unset): rename takes host fileMutation.enter/exit once.
	const sessID = "sess-fused-queue"
	ext := sdk.New("oh-my-pig-fused-queue-test")
	Register(ext)
	hostConn, extConn := net.Pipe()
	runDone := make(chan error, 1)
	go func() { runDone <- ext.RunWithConn(extConn) }()
	defer func() {
		_ = hostConn.Close()
		<-runDone
	}()

	var writeMu sync.Mutex
	writeFrame := func(v any) {
		data, _ := json.Marshal(v)
		var hdr [4]byte
		binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
		writeMu.Lock()
		defer writeMu.Unlock()
		_, _ = hostConn.Write(hdr[:])
		_, _ = hostConn.Write(data)
	}
	readFrame := func() map[string]json.RawMessage {
		var hdr [4]byte
		if _, err := io.ReadFull(hostConn, hdr[:]); err != nil {
			t.Fatalf("read hdr: %v", err)
		}
		buf := make([]byte, binary.BigEndian.Uint32(hdr[:]))
		if _, err := io.ReadFull(hostConn, buf); err != nil {
			t.Fatalf("read body: %v", err)
		}
		var out map[string]json.RawMessage
		_ = json.Unmarshal(buf, &out)
		return out
	}
	_ = readFrame() // register

	stateJSON, _ := json.Marshal(map[string]any{"hasUI": false, "session": map[string]any{"sessionId": sessID}})
	settingsJSON, _ := json.Marshal(map[string]any{"lsp": map[string]any{"enabled": true, "lazy": true}})
	writeFrame(map[string]any{
		"type": "ready",
		"ready": map[string]any{
			"session_name": "",
			"cwd":          dir,
			"mode":         "print",
			"width":        80,
			"state":        json.RawMessage(stateJSON),
			"settings":     json.RawMessage(settingsJSON),
		},
	})

	var enters, exits atomic.Int32
	readResponse := func(wantID string) map[string]json.RawMessage {
		for {
			f := readFrame()
			switch string(f["type"]) {
			case `"request_state"`:
				continue
			case `"call"`:
				var call struct {
					Method string          `json:"method"`
					Args   json.RawMessage `json:"args"`
				}
				_ = json.Unmarshal(f["call"], &call)
				var callID string
				_ = json.Unmarshal(f["id"], &callID)
				var res any
				switch call.Method {
				case "isProjectTrusted":
					res = map[string]any{"trusted": true}
				case "getSessionID":
					res = map[string]any{"sessionId": sessID}
				case "sessionRead":
					res = sessID
				case "fileMutation.enter":
					n := enters.Add(1)
					res = map[string]any{"ticket": "fused-tk-" + strconv.Itoa(int(n))}
				case "fileMutation.exit":
					exits.Add(1)
					res = map[string]any{}
				default:
					res = map[string]any{}
				}
				writeFrame(map[string]any{
					"type":        "call_result",
					"id":          callID,
					"call_result": map[string]any{"result": res},
				})
				continue
			case `"response"`:
				if string(f["id"]) == `"`+wantID+`"` {
					return f
				}
			}
		}
	}

	renameArgs, _ := json.Marshal(map[string]any{
		"action": "rename", "file": "mut.fake", "line": 1, "symbol": "count", "new_name": "total",
	})
	writeFrame(map[string]any{
		"type": "request", "id": "req-fused-rename",
		"request": map[string]any{"method": "tool_call", "tool": "lsp", "tool_call_id": "tc-fused-1", "args": json.RawMessage(renameArgs)},
	})
	resp := readResponse("req-fused-rename")
	if !strings.Contains(string(resp["response"]), "Applied rename:") {
		t.Fatalf("fused rename failed: %s", resp["response"])
	}
	if enters.Load() != 1 || exits.Load() != 1 {
		t.Fatalf("fused host queue enters=%d exits=%d, want 1 and 1", enters.Load(), exits.Load())
	}
	if got, _ := os.ReadFile(srcFile); string(got) != "total = 1\n" {
		t.Fatalf("expected single rename application, got %q", string(got))
	}
	lifecycle.End(sessID)

	// 2. Fallback to per-path mutex when ctx is zero (no host connection) runs fn once.
	var fnCalls atomic.Int32
	var zeroSDK sdk.Context
	if err := withFileMutationQueue(zeroSDK, srcFile, func() error {
		fnCalls.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("fallback queue call err: %v", err)
	}
	if fnCalls.Load() != 1 {
		t.Fatalf("fallback queue fnCalls=%d, want 1", fnCalls.Load())
	}

	// 3. Panic inside fn propagates and does NOT run fn a second time (tested
	//    both under a live connected host ctx via a test tool and under zero ctx).
	ext2 := sdk.New("oh-my-pig-fused-once-test")
	ext2.RegisterTool(sdk.ToolDefinition{
		Name:       "probe_mut",
		Label:      "Probe",
		Parameters: sdk.Schema{"type": "object", "properties": map[string]any{}},
		Execute: func(ctx sdk.Context, _ map[string]any) (any, error) {
			// 3a. Panic inside fn with live connected ctx: must propagate and run once.
			var liveCalls atomic.Int32
			var caughtLive any
			func() {
				defer func() { caughtLive = recover() }()
				_ = withFileMutationQueue(ctx, srcFile, func() error {
					liveCalls.Add(1)
					panic("live-edit boom")
				})
			}()
			if caughtLive != "live-edit boom" {
				return nil, errors.New("expected live-edit boom panic to propagate")
			}
			if liveCalls.Load() != 1 {
				return nil, errors.New("live fn ran more than once on panic")
			}
			// 4a. "not connected" error returned by fn with live connected ctx: must run once.
			liveCalls.Store(0)
			wantLiveErr := errors.New("not connected to downstream")
			if err := withFileMutationQueue(ctx, srcFile, func() error {
				liveCalls.Add(1)
				return wantLiveErr
			}); !errors.Is(err, wantLiveErr) {
				return nil, errors.New("unexpected error from live fn")
			}
			if liveCalls.Load() != 1 {
				return nil, errors.New("live fn ran more than once on not-connected error")
			}
			return sdk.ToolResult{Content: "ok"}, nil
		},
	})
	hConn2, eConn2 := net.Pipe()
	runDone2 := make(chan error, 1)
	go func() { runDone2 <- ext2.RunWithConn(eConn2) }()
	defer func() {
		_ = hConn2.Close()
		<-runDone2
	}()
	var wMu2 sync.Mutex
	writeF2 := func(v any) {
		data, _ := json.Marshal(v)
		var hdr [4]byte
		binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
		wMu2.Lock()
		defer wMu2.Unlock()
		_, _ = hConn2.Write(hdr[:])
		_, _ = hConn2.Write(data)
	}
	readF2 := func() map[string]json.RawMessage {
		var hdr [4]byte
		if _, err := io.ReadFull(hConn2, hdr[:]); err != nil {
			t.Fatalf("read hdr2: %v", err)
		}
		buf := make([]byte, binary.BigEndian.Uint32(hdr[:]))
		if _, err := io.ReadFull(hConn2, buf); err != nil {
			t.Fatalf("read body2: %v", err)
		}
		var out map[string]json.RawMessage
		_ = json.Unmarshal(buf, &out)
		return out
	}
	_ = readF2() // register
	writeF2(map[string]any{
		"type": "ready",
		"ready": map[string]any{
			"session_name": "", "cwd": dir, "mode": "print", "width": 80,
			"state": json.RawMessage(stateJSON), "settings": json.RawMessage(settingsJSON),
		},
	})
	writeF2(map[string]any{
		"type": "request", "id": "req-probe",
		"request": map[string]any{"method": "tool_call", "tool": "probe_mut", "tool_call_id": "tc-p1", "args": json.RawMessage(`{}`)},
	})
	var liveEnters, liveExits atomic.Int32
	for {
		f := readF2()
		switch string(f["type"]) {
		case `"request_state"`:
			continue
		case `"call"`:
			var call struct {
				Method string `json:"method"`
			}
			_ = json.Unmarshal(f["call"], &call)
			var callID string
			_ = json.Unmarshal(f["id"], &callID)
			var res any = map[string]any{}
			if call.Method == "fileMutation.enter" {
				n := liveEnters.Add(1)
				res = map[string]any{"ticket": "probe-tk-" + strconv.Itoa(int(n))}
			} else if call.Method == "fileMutation.exit" {
				liveExits.Add(1)
			}
			writeF2(map[string]any{"type": "call_result", "id": callID, "call_result": map[string]any{"result": res}})
		case `"response"`:
			if !strings.Contains(string(f["response"]), `"ok"`) {
				t.Fatalf("probe_mut failed: %s", f["response"])
			}
			goto probeDone
		}
	}
probeDone:
	if liveEnters.Load() != 2 || liveExits.Load() != 2 {
		t.Fatalf("expected 2 live queue enters/exits (1 panic + 1 error), got enters=%d exits=%d", liveEnters.Load(), liveExits.Load())
	}

	// 3b & 4b: same guarantees under zero ctx fallback.
	fnCalls.Store(0)
	var caught any
	func() {
		defer func() { caught = recover() }()
		_ = withFileMutationQueue(zeroSDK, srcFile, func() error {
			fnCalls.Add(1)
			panic("mid-edit boom")
		})
	}()
	if caught != "mid-edit boom" {
		t.Fatalf("expected panic from fn to propagate, got %v", caught)
	}
	if fnCalls.Load() != 1 {
		t.Fatalf("fn ran %d times on panic, want 1", fnCalls.Load())
	}

	fnCalls.Store(0)
	wantErr := errors.New("not connected to downstream")
	gotErr := withFileMutationQueue(zeroSDK, srcFile, func() error {
		fnCalls.Add(1)
		return wantErr
	})
	if !errors.Is(gotErr, wantErr) {
		t.Fatalf("got err %v, want %v", gotErr, wantErr)
	}
	if fnCalls.Load() != 1 {
		t.Fatalf("fn ran %d times on error, want 1", fnCalls.Load())
	}
}

// TestReleaseSessionCancelDoesNotNegativeCacheInit verifies finding 4:
// cancelling an in-flight initialize via ReleaseSession (which cancels initCtx,
// not the caller's ctx) must NOT store a 3-minute negative-cache entry, so a
// subsequent session immediately starts the healthy server.
func TestReleaseSessionCancelDoesNotNegativeCacheInit(t *testing.T) {
	isolateEnv(t)
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "cancel-init.pid")
	hangCfg := fakeServerConfig(t, ".neg", fakeServerSpec{Mode: "hang_init", PIDFile: pidFile})

	mgr := newClientManager()
	defer mgr.ShutdownAll()

	initDone := make(chan error, 1)
	go func() {
		// Pass context.Background() so caller ctx is NOT cancelled when ReleaseSession runs.
		_, err := mgr.GetOrCreateClientForSessionWithTrust(context.Background(), "sess-cancel", true, 0, hangCfg, dir, 30*time.Second)
		initDone <- err
	}()

	// Wait for the server to start mid-initialize.
	var pid int
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(pidFile); err == nil {
			if n, convErr := strconv.Atoi(strings.TrimSpace(string(b))); convErr == nil && n > 0 {
				pid = n
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pid <= 0 {
		t.Fatalf("mid-init helper never wrote PID file (initErr=%v)", <-initDone)
	}

	mgr.ReleaseSession("sess-cancel")
	initErr := <-initDone
	if initErr == nil {
		t.Fatalf("expected cancelled init error after ReleaseSession")
	}

	// Verify initFailures has NO negative-cache entry for hangCfg.
	key := stableClientKey(hangCfg, dir)
	mgr.mu.Lock()
	entry, cached := mgr.initFailures[key]
	mgr.mu.Unlock()
	if cached {
		t.Fatalf("ReleaseSession-cancelled initialize must NOT be negative-cached in initFailures: got %+v (initErr=%v)", entry, initErr)
	}

	// Also verify the cancelled mid-init helper exited.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && isProcessAlive(pid) {
		time.Sleep(20 * time.Millisecond)
	}
	if isProcessAlive(pid) {
		t.Fatalf("cancelled mid-init helper PID %d still alive", pid)
	}
}

// TestUnresolvedSessionOwnsServerAndBoundedSessionShutdown verifies finding 5:
//  1. A tool call or formatContent whose session ID cannot be resolved still
//     tracks the spawned server under a request-scoped ephemeral owner and stops
//     it when the call returns (never leaving an ownerless orphan process).
//  2. ReleaseSession on a wedged server (that hangs on `shutdown`) returns
//     promptly under the bounded session-end budget instead of stalling ~7s.
func TestUnresolvedSessionOwnsServerAndBoundedSessionShutdown(t *testing.T) {
	isolateEnv(t)
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "ephemeral.pid")
	srcFile := filepath.Join(dir, "app.fake")
	if err := os.WriteFile(srcFile, []byte("func hi() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := fakeServerConfig(t, ".fake", fakeServerSpec{
		PIDFile:   pidFile,
		HoverText: "func hi()",
		FormatEdits: []TextEdit{
			{Range: Range{Start: Position{0, 0}, End: Position{0, 4}}, NewText: "fn"},
		},
	})
	writeLspJSON(t, dir, "fake-lsp", cfg)
	InvalidateConfig(dir)
	defer InvalidateConfig(dir)

	// Unresolved session id ("") for executeLspTool: server runs during the call
	// and is stopped as soon as the call finishes.
	setTestHooks(t, true, "", nil)
	mgr := newClientManager()
	defer mgr.ShutdownAll()
	var zeroSDK sdk.Context

	res, err := executeLspTool(context.Background(), zeroSDK, mgr, dir, LspParams{
		Action: "hover", File: "app.fake", Line: new(1), Symbol: "hi",
	})
	if err != nil || !strings.Contains(res.Content, "func hi()") {
		t.Fatalf("ephemeral hover failed: err=%v, out=%s", err, res.Content)
	}
	pidBytes, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("expected ephemeral server PID file: %v", err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && isProcessAlive(pid) {
		time.Sleep(20 * time.Millisecond)
	}
	if isProcessAlive(pid) {
		t.Fatalf("server PID %d started without session ID outlived its call", pid)
	}

	// formatContent with a real session ID registers the server under that session ID
	// so ReleaseSession(sessionID) stops it cleanly.
	pidFile2 := filepath.Join(dir, "format-sess.pid")
	cfg2 := fakeServerConfig(t, ".fake", fakeServerSpec{
		PIDFile: pidFile2,
		FormatEdits: []TextEdit{
			{Range: Range{Start: Position{0, 0}, End: Position{0, 4}}, NewText: "fn"},
		},
	})
	fmtRes, err := formatContent(context.Background(), mgr, "sess-fmt", true, 0, srcFile, "func hi() {}\n", dir, []NamedServer{{Name: "fake-lsp", Config: cfg2}})
	if err != nil || !strings.HasPrefix(fmtRes.Content, "fn hi") {
		t.Fatalf("formatContent: err=%v, res=%+v", err, fmtRes)
	}
	pidBytes2, err := os.ReadFile(pidFile2)
	if err != nil {
		t.Fatalf("expected formatContent PID file: %v", err)
	}
	pid2, _ := strconv.Atoi(strings.TrimSpace(string(pidBytes2)))
	mgr.ReleaseSession("sess-fmt")
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && isProcessAlive(pid2) {
		time.Sleep(20 * time.Millisecond)
	}
	if isProcessAlive(pid2) {
		t.Fatalf("formatContent server PID %d outlived ReleaseSession", pid2)
	}

	// Bounded ReleaseSession on a server that hangs on `shutdown`: must complete well under 4s (was ~7s).
	pidFile3 := filepath.Join(dir, "hang-shutdown.pid")
	cfg3 := fakeServerConfig(t, ".hangshut", fakeServerSpec{Mode: "hang_shutdown", PIDFile: pidFile3})
	if _, err := mgr.GetOrCreateClientForSessionWithTrust(context.Background(), "sess-wedged", true, 0, cfg3, dir, 5*time.Second); err != nil {
		t.Fatalf("start hang_shutdown client: %v", err)
	}
	start := time.Now()
	mgr.ReleaseSession("sess-wedged")
	elapsed := time.Since(start)
	if elapsed > 3*time.Second {
		t.Fatalf("ReleaseSession on wedged server took %v, want bounded (<3s)", elapsed)
	}
	pidBytes3, _ := os.ReadFile(pidFile3)
	pid3, _ := strconv.Atoi(strings.TrimSpace(string(pidBytes3)))
	if pid3 > 0 && isProcessAlive(pid3) {
		t.Fatalf("wedged server PID %d still alive after ReleaseSession", pid3)
	}
}
