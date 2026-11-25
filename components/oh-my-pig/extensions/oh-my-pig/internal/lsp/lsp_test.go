package lsp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// TestFakeLSPHelperProcess is re-exec'd as a child process speaking JSON-RPC
// LSP over stdio (standard Go os.Args[0] helper-process pattern).
func TestFakeLSPHelperProcess(t *testing.T) {
	specJSON := os.Getenv(fakeLSPModeEnv)
	if specJSON == "" {
		return
	}
	runFakeLSPServer(specJSON)
	os.Exit(0)
}

func fakeServerConfig(t *testing.T, ext string, spec fakeServerSpec) ServerConfig {
	t.Helper()
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	// Pass spec via env through a thin wrapper or env var on os.Args[0]:
	// Set OMP_LSP_TEST_HELPER in process env via t.Setenv (inherited by child).
	t.Setenv(fakeLSPModeEnv, string(raw))
	return ServerConfig{
		Command:         os.Args[0],
		ResolvedCommand: os.Args[0],
		Args:            []string{"-test.run=^TestFakeLSPHelperProcess$"},
		FileTypes:       []string{ext},
		RootMarkers:     []string{"."},
	}
}

func writeLspJSON(t *testing.T, dir, serverName string, cfg ServerConfig) {
	t.Helper()
	payload := map[string]any{
		"servers": map[string]any{
			serverName: map[string]any{
				"command":     cfg.Command,
				"args":        cfg.Args,
				"fileTypes":   cfg.FileTypes,
				"rootMarkers": cfg.RootMarkers,
				"settings":    cfg.Settings,
				"initOptions": cfg.InitOptions,
				"isLinter":    cfg.IsLinter,
			},
		},
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lsp.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func isolateEnv(t *testing.T) {
	t.Helper()
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("USERPROFILE", tmpHome)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(tmpHome, ".omp", "agent"))
}

func setTestHooks(t *testing.T, trusted bool, sessionID string, settings map[string]any) {
	t.Helper()
	hooksMu.Lock()
	origSettings := SettingsReader
	origTrust := projectTrustChecker
	origSession := sessionIDResolver
	if settings != nil {
		SettingsReader = func(sdk.Context) (map[string]any, error) { return settings, nil }
	} else {
		SettingsReader = nil
	}
	projectTrustChecker = func(sdk.Context) (bool, error) { return trusted, nil }
	if sessionID != "" {
		sessionIDResolver = func(sdk.Context) (string, error) { return sessionID, nil }
	} else {
		sessionIDResolver = func(sdk.Context) (string, error) { return "", nil }
	}
	hooksMu.Unlock()
	t.Cleanup(func() {
		hooksMu.Lock()
		SettingsReader = origSettings
		projectTrustChecker = origTrust
		sessionIDResolver = origSession
		hooksMu.Unlock()
	})
}

// =============================================================================
// 1. Diagnostics Ledger Deduplication
// upstream: packages/coding-agent/test/tools/lsp-diagnostics-dedup.test.ts
// =============================================================================

// upstream: lsp-diagnostics-dedup.test.ts: "DiagnosticsLedger"
func TestDiagnosticsLedger(t *testing.T) {
	const (
		fileA        = "/repo/src/a.ts"
		fileB        = "/repo/src/b.ts"
		typeErr      = `src/a.ts:12:5 [error] pyright: Type "str" is not assignable to declared type "int" (reportAssignmentType)`
		typeErrShift = `src/a.ts:48:19 [error] pyright: Type "str" is not assignable to declared type "int" (reportAssignmentType)`
		privImp      = `src/a.ts:20:7 [warning] pyright: "device" is not exported from module "torch" (reportPrivateImportUsage)`
		privImpShift = `src/a.ts:58:11 [warning] pyright: "device" is not exported from module "torch" (reportPrivateImportUsage)`
		newErr       = `src/a.ts:60:3 [error] pyright: Cannot access attribute "missing" for class "Widget" (reportAttributeAccessIssue)`
	)

	makeDiag := func(msgs []string, summary string) *FileDiagnosticsResult {
		errored := false
		for _, m := range msgs {
			if strings.Contains(m, "[error]") {
				errored = true
			}
		}
		return &FileDiagnosticsResult{
			Server:   "pyright",
			Messages: msgs,
			Summary:  summary,
			Errored:  errored,
		}
	}

	t.Run("returns all messages unchanged the first time a file is reduced", func(t *testing.T) {
		ledger := NewDiagnosticsLedger()
		first := makeDiag([]string{typeErr, privImp}, "1 error(s), 1 warning(s)")
		reduced := ledger.Reduce(fileA, first)
		if reduced != first || len(reduced.Messages) != 2 {
			t.Fatalf("expected original unchanged, got %+v", reduced)
		}
	})

	t.Run("fully suppresses an identical second reduce", func(t *testing.T) {
		ledger := NewDiagnosticsLedger()
		ledger.Reduce(fileA, makeDiag([]string{typeErr, privImp}, "1 error(s), 1 warning(s)"))
		reduced := ledger.Reduce(fileA, makeDiag([]string{typeErr, privImp}, "1 error(s), 1 warning(s)"))
		if len(reduced.Messages) != 0 || reduced.Summary != "no issues" || reduced.Errored {
			t.Fatalf("expected suppressed result, got %+v", reduced)
		}
	})

	t.Run("returns only genuinely new messages and recomputes summary state", func(t *testing.T) {
		ledger := NewDiagnosticsLedger()
		ledger.Reduce(fileA, makeDiag([]string{typeErr, privImp}, "1 error(s), 1 warning(s)"))
		reduced := ledger.Reduce(fileA, makeDiag([]string{typeErrShift, privImpShift, newErr}, "2 error(s), 1 warning(s)"))
		if len(reduced.Messages) != 1 || reduced.Messages[0] != newErr {
			t.Fatalf("expected only newErr, got %+v", reduced.Messages)
		}
		if reduced.Summary != "1 error(s)" || !reduced.Errored || reduced.Server != "pyright" {
			t.Fatalf("unexpected reduced metadata: %+v", reduced)
		}
	})

	t.Run("re-surfaces a diagnostic after it was removed", func(t *testing.T) {
		ledger := NewDiagnosticsLedger()
		ledger.Reduce(fileA, makeDiag([]string{typeErr}, "1 error(s)"))
		ledger.Reduce(fileA, makeDiag(nil, "OK"))
		reduced := ledger.Reduce(fileA, makeDiag([]string{typeErr}, "1 error(s)"))
		if len(reduced.Messages) != 1 || reduced.Messages[0] != typeErr {
			t.Fatalf("expected re-surfaced diagnostic, got %+v", reduced)
		}
	})

	t.Run("tracks files independently", func(t *testing.T) {
		ledger := NewDiagnosticsLedger()
		ledger.Reduce(fileA, makeDiag([]string{typeErr}, "1 error(s)"))
		reduced := ledger.Reduce(fileB, makeDiag([]string{typeErr}, "1 error(s)"))
		if len(reduced.Messages) != 1 {
			t.Fatalf("expected fileB unaffected by fileA, got %+v", reduced)
		}
	})
}

// upstream: lsp-diagnostics-dedup.test.ts: "diagnosticIdentity"
func TestDiagnosticIdentity(t *testing.T) {
	first := "fixtures/pkg:2/example.ts:12:5 [error] pyright: Broken import (E1)"
	shifted := "fixtures/pkg:2/example.ts:99:27 [error] pyright: Broken import (E1)"
	if diagnosticIdentity(first) != "[error] pyright: Broken import (E1)" {
		t.Fatalf("unexpected identity: %q", diagnosticIdentity(first))
	}
	if diagnosticIdentity(first) != diagnosticIdentity(shifted) {
		t.Fatalf("shifted identity mismatch")
	}
	if diagnosticIdentity("src/a.ts:1:1 [warning] pyright: Broken import (E1)") == diagnosticIdentity(first) {
		t.Fatalf("severity change must change identity")
	}
	raw := "pyright: Broken import (E1)"
	if diagnosticIdentity(raw) != raw {
		t.Fatalf("unparseable message should pass through unchanged")
	}
}

// =============================================================================
// 2. Format Options & .editorconfig
// upstream: packages/coding-agent/test/lsp-format-options.test.ts
// =============================================================================

// upstream: lsp-format-options.test.ts: "detectIndentFromContent" + "resolveFormatOptions"
func TestFormatOptions(t *testing.T) {
	t.Run("detects 2-space YAML, 4-space Python, JSDoc stride, and tabs", func(t *testing.T) {
		if d := detectIndentFromContent(""); d.TabSize != nil || d.InsertSpaces != nil {
			t.Fatalf("empty content should return empty, got %+v", d)
		}
		yaml := "metadata:\n  name: test\n  labels:\n    app: test\n"
		if d := detectIndentFromContent(yaml); d.TabSize == nil || *d.TabSize != 2 || d.InsertSpaces == nil || !*d.InsertSpaces {
			t.Fatalf("expected 2 spaces for YAML, got %+v", d)
		}
		py := "def f():\n    x = 1\n    if x:\n        return x\n"
		if d := detectIndentFromContent(py); d.TabSize == nil || *d.TabSize != 4 || d.InsertSpaces == nil || !*d.InsertSpaces {
			t.Fatalf("expected 4 spaces for Python, got %+v", d)
		}
		jsdoc := "/**\n * Probe helper.\n * @param a input\n */\nfunction probe(a) {\n  if (a) {\n    return a;\n  }\n}\n"
		if d := detectIndentFromContent(jsdoc); d.TabSize == nil || *d.TabSize != 2 {
			t.Fatalf("JSDoc continuations must keep 2-space stride (#18544), got %+v", d)
		}
		goSrc := "package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n"
		if d := detectIndentFromContent(goSrc); d.InsertSpaces == nil || *d.InsertSpaces || d.TabSize != nil {
			t.Fatalf("expected tabs with unset tabSize for Go, got %+v", d)
		}
	})

	t.Run("honors .editorconfig over content sniffing and falls back to 2 spaces", func(t *testing.T) {
		dir := t.TempDir()
		blank := filepath.Join(dir, "blank.yaml")
		opts := resolveFormatOptions(blank, "")
		if opts.TabSize != 2 || !opts.InsertSpaces || !opts.TrimTrailingWhitespace || !opts.InsertFinalNewline {
			t.Fatalf("unexpected default format options: %+v", opts)
		}

		ec := "root = true\n\n[*]\nindent_size = 4\nindent_style = space\n"
		if err := os.WriteFile(filepath.Join(dir, ".editorconfig"), []byte(ec), 0o644); err != nil {
			t.Fatal(err)
		}
		opts = resolveFormatOptions(filepath.Join(dir, "thing.yaml"), "foo:\n  bar: baz\n")
		if opts.TabSize != 4 || !opts.InsertSpaces {
			t.Fatalf("editorconfig must win over 2-space content: %+v", opts)
		}
		tabDir := t.TempDir()
		ecTab := "root = true\n\n[*]\nindent_style = tab\ntab_width = 8\n"
		if err := os.WriteFile(filepath.Join(tabDir, ".editorconfig"), []byte(ecTab), 0o644); err != nil {
			t.Fatal(err)
		}
		mkOpts := resolveFormatOptions(filepath.Join(tabDir, "Makefile"), "all:\n  echo hi\n")
		if mkOpts.InsertSpaces || mkOpts.TabSize != 8 {
			t.Fatalf("editorconfig tab+tab_width must win for Makefile: %+v", mkOpts)
		}
	})
}

// =============================================================================
// 3. Framing, Edits, Utils, Config Defaults & Discovery
// upstream: packages/coding-agent/test/tools/lsp-regressions.test.ts
// =============================================================================

// upstream: lsp-regressions.test.ts: "supports long LSP timeouts up to the advertised ceiling"
func TestTimeoutsAndSchema(t *testing.T) {
	if got := clampTimeout(nil, 0); got != 20 {
		t.Fatalf("default timeout = %d, want 20", got)
	}
	if got := clampTimeout(new(1.0), 0); got != 5 {
		t.Fatalf("min clamp = %d, want 5", got)
	}
	if got := clampTimeout(new(120.0), 0); got != 120 {
		t.Fatalf("mid timeout = %d, want 120", got)
	}
	if got := clampTimeout(new(1000.0), 0); got != 300 {
		t.Fatalf("max clamp = %d, want 300", got)
	}
	schema := ToolSchema()
	if schema["type"] != "object" {
		t.Fatalf("schema.type = %v, want object", schema["type"])
	}
	if schema["additionalProperties"] != false {
		t.Fatalf("schema.additionalProperties = %v, want false", schema["additionalProperties"])
	}
	req, ok := schema["required"].([]any)
	if !ok || len(req) != 1 || req[0] != "action" {
		t.Fatalf("schema.required = %#v, want [action]", schema["required"])
	}
	props := schema["properties"].(*sdk.OrderedObject)
	wantKeys := []string{"action", "file", "line", "symbol", "query", "new_name", "apply", "timeout", "payload"}
	gotKeys := props.Keys()
	if strings.Join(gotKeys, ",") != strings.Join(wantKeys, ",") {
		t.Fatalf("schema property order = %v, want %v", gotKeys, wantKeys)
	}
	actionRaw, _ := props.Get("action")
	actionObj := actionRaw.(*sdk.OrderedObject)
	enumRaw, _ := actionObj.Get("enum")
	wantActions := []string{
		"diagnostics", "definition", "references", "hover", "symbols", "rename",
		"rename_file", "code_actions", "type_definition", "implementation",
		"status", "reload", "capabilities", "request",
	}
	enumVals := enumRaw.([]any)
	if len(enumVals) != len(wantActions) {
		t.Fatalf("action enum count = %d, want %d", len(enumVals), len(wantActions))
	}
	for i, w := range wantActions {
		if enumVals[i] != w {
			t.Fatalf("action enum[%d] = %v, want %q", i, enumVals[i], w)
		}
	}
	timeoutRaw, ok := props.Get("timeout")
	if !ok {
		t.Fatalf("missing timeout property in schema")
	}
	timeoutSchema := timeoutRaw.(*sdk.TSchema)
	if minVal, _ := timeoutSchema.Get("minimum"); minVal != 5 {
		t.Fatalf("timeout minimum = %v, want 5", minVal)
	}
	if maxVal, _ := timeoutSchema.Get("maximum"); maxVal != 300 {
		t.Fatalf("timeout maximum = %v, want 300", maxVal)
	}
	wantTimeoutDesc := "Timeout in seconds; raises or lowers the default (range 5–300)."
	if gotDesc, _ := timeoutSchema.Get("description"); gotDesc != wantTimeoutDesc {
		t.Fatalf("timeout description = %q, want %q", gotDesc, wantTimeoutDesc)
	}
	desc := renderLspDescription(lspPromptTemplate, DefaultSettings())
	if !strings.Contains(desc, "whole call 20s") || !strings.Contains(desc, "wait 10s") || !strings.Contains(desc, "Capped at 300s") {
		t.Fatalf("unexpected rendered prompt description:\n%s", desc)
	}
}

// upstream: lsp-regressions.test.ts: "detects bracket-style glob patterns", "resolves the requested symbol occurrence on a line", "reads location context from a CRLF file"
func TestUtilsAndEditsRegressions(t *testing.T) {
	if !hasGlobPattern("src/[ab].ts") || !hasGlobPattern("src/**/*.ts") || hasGlobPattern("src/main.ts") {
		t.Fatalf("hasGlobPattern failed")
	}

	dir := t.TempDir()
	// CRLF location context
	crlfFile := filepath.Join(dir, "crlf.ts")
	if err := os.WriteFile(crlfFile, []byte("const a = 1;\r\nconst b = 2;\r\nconst c = 3;\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctxLines, err := readLocationContext(crlfFile, 2, 1)
	if err != nil || len(ctxLines) != 3 {
		t.Fatalf("readLocationContext: %v, %+v", err, ctxLines)
	}
	for _, l := range ctxLines {
		if strings.Contains(l, "\r") {
			t.Fatalf("location context must strip CR: %q", l)
		}
	}

	// Symbol occurrence resolution
	symFile := filepath.Join(dir, "sym.ts")
	if err := os.WriteFile(symFile, []byte("const foo = foo + fooBar + foo;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	col1, err := resolveSymbolColumn(symFile, 1, "foo")
	if err != nil || col1 != 6 {
		t.Fatalf("first foo col = %d, err = %v", col1, err)
	}
	col2, err := resolveSymbolColumn(symFile, 1, "foo#2")
	if err != nil || col2 != 12 {
		t.Fatalf("second foo col = %d, err = %v", col2, err)
	}
	col3, err := resolveSymbolColumn(symFile, 1, "foo#3")
	if err != nil || col3 != 27 {
		t.Fatalf("third foo col (skipping fooBar) = %d, err = %v", col3, err)
	}
	if _, err := resolveSymbolColumn(symFile, 1, "missing"); err == nil {
		t.Fatalf("expected error for missing symbol")
	}
	if _, err := resolveSymbolColumn(symFile, 1, "foo#4"); err == nil {
		t.Fatalf("expected error for out-of-bounds occurrence")
	}

	// Overlapping and snippet text edits rejected; non-overlapping applied bottom-up
	if _, err := applyTextEditsToString("hello world", []TextEdit{
		{Range: Range{Start: Position{0, 0}, End: Position{0, 5}}, NewText: "${1:hi}", InsertTextFormat: 2},
	}); err == nil || !strings.Contains(err.Error(), "snippet-formatted") {
		t.Fatalf("expected snippet rejection, got %v", err)
	}
	if _, err := applyTextEditsToString("hello world", []TextEdit{
		{Range: Range{Start: Position{0, 0}, End: Position{0, 6}}, NewText: "A"},
		{Range: Range{Start: Position{0, 4}, End: Position{0, 9}}, NewText: "B"},
	}); err == nil || !strings.Contains(err.Error(), "overlapping") {
		t.Fatalf("expected overlap rejection, got %v", err)
	}
	updated, err := applyTextEditsToString("alpha beta gamma", []TextEdit{
		{Range: Range{Start: Position{0, 0}, End: Position{0, 5}}, NewText: "ONE"},
		{Range: Range{Start: Position{0, 11}, End: Position{0, 16}}, NewText: "THREE"},
	})
	if err != nil || updated != "ONE beta THREE" {
		t.Fatalf("applyTextEditsToString = %q, err = %v", updated, err)
	}

	// WorkspaceEdit resource ops (create, text edit, rename, delete)
	we := &WorkspaceEdit{
		DocumentChanges: []DocumentChange{
			{Kind: "create", URI: fileToUri(filepath.Join(dir, "new.txt"))},
			{
				TextDocument: &OptionalVersionedTextDocumentIdentifier{URI: fileToUri(filepath.Join(dir, "new.txt"))},
				Edits:        []TextEdit{{Range: Range{Start: Position{0, 0}, End: Position{0, 0}}, NewText: "created\n"}},
			},
			{Kind: "rename", OldURI: fileToUri(filepath.Join(dir, "new.txt")), NewURI: fileToUri(filepath.Join(dir, "moved.txt"))},
		},
	}
	var zeroSDK sdk.Context
	res, err := applyWorkspaceEdit(zeroSDK, we, dir, nil)
	if err != nil || len(res.Applied) != 3 {
		t.Fatalf("applyWorkspaceEdit: %v, %+v", err, res)
	}
	data, err := os.ReadFile(filepath.Join(dir, "moved.txt"))
	if err != nil || string(data) != "created\n" {
		t.Fatalf("moved.txt = %q, err = %v", string(data), err)
	}
}

// upstream: lsp-regressions.test.ts: "registers CUDA sources and headers", "registers expert for .ex while keeping elixirls primary", "registers ty for .py behind existing Python primaries and before ruff", "auto-detects ty in a ty.toml-only project, resolving via project-local venv bin"
func TestDefaultsOrderingAndLocalBinDiscovery(t *testing.T) {
	isolateEnv(t)
	defaults, order := loadDefaultsOrdered()
	cfg := &LspConfig{Servers: defaults, ServerOrder: order}

	indexOf := func(list []string, target string) int {
		for i, v := range list {
			if v == target {
				return i
			}
		}
		return -1
	}

	// CUDA files match clangd ("registers CUDA sources and headers")
	for _, f := range []string{"kernel.cu", "kernel.cuh"} {
		servers := GetServersForFile(cfg, f)
		found := false
		for _, s := range servers {
			if s.Name == "clangd" {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected clangd for %s", f)
		}
	}

	// Elixir ordering ("registers expert for .ex while keeping elixirls primary")
	exNames := []string{}
	for _, s := range GetServersForFile(cfg, "lib/app.ex") {
		exNames = append(exNames, s.Name)
	}
	if indexOf(exNames, "elixirls") == -1 || indexOf(exNames, "expert") == -1 || indexOf(exNames, "elixirls") >= indexOf(exNames, "expert") {
		t.Fatalf("expected elixirls before expert for .ex, got %v", exNames)
	}

	// Python ordering ("registers ty for .py behind existing Python primaries and before ruff")
	pyNames := []string{}
	for _, s := range GetServersForFile(cfg, "app.py") {
		pyNames = append(pyNames, s.Name)
	}
	if indexOf(pyNames, "pyright") >= indexOf(pyNames, "ty") ||
		indexOf(pyNames, "basedpyright") >= indexOf(pyNames, "ty") ||
		indexOf(pyNames, "pylsp") >= indexOf(pyNames, "ty") ||
		indexOf(pyNames, "ty") >= indexOf(pyNames, "ruff") {
		t.Fatalf("unexpected Python server ranking: %v", pyNames)
	}

	// describeMissingServers preserves config order (pyright before basedpyright before pylsp before ty before ruff)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte("[project]\nname = \"demo\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	origLookPath := lookPathFunc
	lookPathFunc = func(string) (string, error) { return "", exec.ErrNotFound }
	defer func() { lookPathFunc = origLookPath }()

	missingCfg := LoadConfigWithTrust(dir, true)
	missingMsg := describeMissingServers(missingCfg, filepath.Join(dir, "app.py"))
	pPyright := strings.Index(missingMsg, "pyright:")
	pBased := strings.Index(missingMsg, "basedpyright:")
	pPylsp := strings.Index(missingMsg, "pylsp:")
	pTy := strings.Index(missingMsg, "ty:")
	pRuff := strings.Index(missingMsg, "ruff:")
	if !(pPyright >= 0 && pPyright < pBased && pBased < pPylsp && pPylsp < pTy && pTy < pRuff) {
		t.Fatalf("describeMissingServers lost config order:\n%s", missingMsg)
	}

	// ty.toml-only project resolves local .venv/bin/ty without PATH when trusted
	tyDir := t.TempDir()
	venvBin := filepath.Join(tyDir, ".venv", "bin")
	if err := os.MkdirAll(venvBin, 0o755); err != nil {
		t.Fatal(err)
	}
	tyBin := filepath.Join(venvBin, "ty")
	if err := os.WriteFile(tyBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tyDir, "ty.toml"), []byte("[configuration]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded := LoadConfigWithTrust(tyDir, true)
	tyCfg, ok := loaded.Servers["ty"]
	if !ok || tyCfg.ResolvedCommand != tyBin {
		t.Fatalf("expected local venv ty resolved to %q, got %+v (ok=%v)", tyBin, tyCfg, ok)
	}
}

// =============================================================================
// 4. End-to-End Subprocess Fake LSP Server Tests (stdio JSON-RPC)
// =============================================================================

func TestFakeLSPStdioEndToEnd(t *testing.T) {
	isolateEnv(t)
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "server.pid")
	mainFile := filepath.Join(dir, "app.fake")
	otherFile := filepath.Join(dir, "other.fake")

	if err := os.WriteFile(mainFile, []byte("func greet(name) {\n  return greet(name)\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otherFile, []byte("call greet(1)\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mainURI := fileToUri(mainFile)
	otherURI := fileToUri(otherFile)

	spec := fakeServerSpec{
		PIDFile:   pidFile,
		HoverText: "```fake\nfunc greet(name: string) string\n```\nGreets a user.",
		Definitions: []Location{
			{URI: mainURI, Range: Range{Start: Position{0, 5}, End: Position{0, 10}}},
		},
		TypeDefinitions: []Location{
			{URI: mainURI, Range: Range{Start: Position{0, 0}, End: Position{0, 4}}},
		},
		Implementations: []Location{
			{URI: otherURI, Range: Range{Start: Position{0, 5}, End: Position{0, 10}}},
		},
		References: []Location{
			{URI: mainURI, Range: Range{Start: Position{0, 5}, End: Position{0, 10}}},
			{URI: mainURI, Range: Range{Start: Position{1, 9}, End: Position{1, 14}}},
			{URI: otherURI, Range: Range{Start: Position{0, 5}, End: Position{0, 10}}},
		},
		DocSymbols: []DocumentSymbol{
			{
				Name:           "greet",
				Detail:         "(name)",
				Kind:           12,
				Range:          Range{Start: Position{0, 0}, End: Position{2, 1}},
				SelectionRange: Range{Start: Position{0, 5}, End: Position{0, 10}},
			},
		},
		WorkspaceSymbols: []SymbolInformation{
			{
				Name:     "greet",
				Kind:     12,
				Location: Location{URI: mainURI, Range: Range{Start: Position{0, 5}, End: Position{0, 10}}},
			},
			{
				Name:     "unrelated",
				Kind:     12,
				Location: Location{URI: otherURI, Range: Range{Start: Position{0, 0}, End: Position{0, 4}}},
			},
		},
		RenameEdit: &WorkspaceEdit{
			Changes: map[string][]TextEdit{
				mainURI: {
					{Range: Range{Start: Position{0, 5}, End: Position{0, 10}}, NewText: "hello"},
					{Range: Range{Start: Position{1, 9}, End: Position{1, 14}}, NewText: "hello"},
				},
				otherURI: {
					{Range: Range{Start: Position{0, 5}, End: Position{0, 10}}, NewText: "hello"},
				},
			},
		},
		CodeActions: []json.RawMessage{
			json.RawMessage(`{"title":"Add doc comment","kind":"quickfix","isPreferred":true,"edit":{"changes":{"` + mainURI + `":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}},"newText":"// doc\n"}]}}}`),
		},
		SourceCodeActions: []json.RawMessage{
			json.RawMessage(`{"title":"Organize Imports","kind":"source.organizeImports","command":{"title":"Organize Imports","command":"fake.organizeImports"}}`),
		},
		FormatEdits: []TextEdit{
			{Range: Range{Start: Position{0, 0}, End: Position{0, 4}}, NewText: "fn"},
		},
	}

	cfg := fakeServerConfig(t, ".fake", spec)
	writeLspJSON(t, dir, "fake-lsp", cfg)
	InvalidateConfig(dir)
	defer InvalidateConfig(dir)

	setTestHooks(t, true, "sess-e2e", nil)
	mgr := newClientManager()
	defer mgr.ShutdownAll()
	ctx := context.Background()
	var zeroSDK sdk.Context

	// 1. status before lazy start shows "configured, not started"
	stRes, err := executeLspTool(ctx, zeroSDK, mgr, dir, LspParams{Action: "status"})
	if err != nil || !strings.Contains(stRes.Content, "fake-lsp (configured, not started)") {
		t.Fatalf("status before start: err=%v, content=%q", err, stRes.Content)
	}
	if _, statErr := os.Stat(pidFile); !os.IsNotExist(statErr) {
		t.Fatalf("lazy server must not start during status")
	}

	// 2. definition (triggers lazy start)
	defRes, err := executeLspTool(ctx, zeroSDK, mgr, dir, LspParams{
		Action: "definition", File: "app.fake", Line: new(2), Symbol: "greet",
	})
	if err != nil {
		t.Fatalf("definition err: %v", err)
	}
	if !strings.Contains(defRes.Content, "Found 1 definition(s):") || !strings.Contains(defRes.Content, "app.fake:1:6") {
		t.Fatalf("unexpected definition output:\n%s", defRes.Content)
	}
	details := defRes.Details.(LspToolDetails)
	if !details.Success || details.ServerName != "fake-lsp" || details.Action != "definition" {
		t.Fatalf("unexpected details: %+v", details)
	}

	// Verify server PID recorded
	pidBytes, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("expected server PID file: %v", err)
	}
	serverPID, _ := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if serverPID <= 0 {
		t.Fatalf("invalid server PID: %q", string(pidBytes))
	}

	// status now shows ready
	stRes, err = executeLspTool(ctx, zeroSDK, mgr, dir, LspParams{Action: "status"})
	if err != nil || !strings.Contains(stRes.Content, "fake-lsp (ready)") {
		t.Fatalf("status after start: err=%v, content=%q", err, stRes.Content)
	}

	// 3. project-aware definition without symbol errors (no silent fallback)
	if _, err := executeLspTool(ctx, zeroSDK, mgr, dir, LspParams{
		Action: "definition", File: "app.fake", Line: new(2),
	}); err == nil || !strings.Contains(err.Error(), "symbol is required") {
		t.Fatalf("expected missing-symbol error, got %v", err)
	}

	// 4. type_definition & implementation
	tdRes, err := executeLspTool(ctx, zeroSDK, mgr, dir, LspParams{Action: "type_definition", File: "app.fake", Line: new(1), Symbol: "greet"})
	if err != nil || !strings.Contains(tdRes.Content, "Found 1 type definition(s):") {
		t.Fatalf("type_definition: err=%v, out=%s", err, tdRes.Content)
	}
	implRes, err := executeLspTool(ctx, zeroSDK, mgr, dir, LspParams{Action: "implementation", File: "app.fake", Line: new(1), Symbol: "greet"})
	if err != nil || !strings.Contains(implRes.Content, "Found 1 implementation(s):") {
		t.Fatalf("implementation: err=%v, out=%s", err, implRes.Content)
	}

	// 5. references
	refRes, err := executeLspTool(ctx, zeroSDK, mgr, dir, LspParams{Action: "references", File: "app.fake", Line: new(1), Symbol: "greet"})
	if err != nil || !strings.Contains(refRes.Content, "Found 3 reference(s):") || !strings.Contains(refRes.Content, "other.fake:1:6") {
		t.Fatalf("references: err=%v, out=%s", err, refRes.Content)
	}

	// 6. hover
	hovRes, err := executeLspTool(ctx, zeroSDK, mgr, dir, LspParams{Action: "hover", File: "app.fake", Line: new(1), Symbol: "greet"})
	if err != nil || !strings.Contains(hovRes.Content, "func greet(name: string) string") {
		t.Fatalf("hover: err=%v, out=%s", err, hovRes.Content)
	}

	// 7. document symbols & workspace symbols
	symRes, err := executeLspTool(ctx, zeroSDK, mgr, dir, LspParams{Action: "symbols", File: "app.fake"})
	if err != nil || !strings.Contains(symRes.Content, "Symbols in app.fake:") || !strings.Contains(symRes.Content, "greet (name) @ line 1") {
		t.Fatalf("document symbols: err=%v, out=%s", err, symRes.Content)
	}
	wsSymRes, err := executeLspTool(ctx, zeroSDK, mgr, dir, LspParams{Action: "symbols", File: "*", Query: "greet"})
	if err != nil || !strings.Contains(wsSymRes.Content, `Found 1 symbol(s) matching "greet":`) || strings.Contains(wsSymRes.Content, "unrelated") {
		t.Fatalf("workspace symbols: err=%v, out=%s", err, wsSymRes.Content)
	}

	// 8. code_actions list merges unfiltered + source actions, and apply executes command
	caList, err := executeLspTool(ctx, zeroSDK, mgr, dir, LspParams{Action: "code_actions", File: "app.fake", Line: new(1), Symbol: "greet"})
	if err != nil || !strings.Contains(caList.Content, "Add doc comment") || !strings.Contains(caList.Content, "Organize Imports") {
		t.Fatalf("code_actions list: err=%v, out=%s", err, caList.Content)
	}
	caCmd, err := executeLspTool(ctx, zeroSDK, mgr, dir, LspParams{Action: "code_actions", File: "app.fake", Line: new(1), Symbol: "greet", Apply: new(true), Query: "Organize"})
	if err != nil || !strings.Contains(caCmd.Content, "fake.organizeImports") {
		t.Fatalf("code_actions apply command: err=%v, out=%s", err, caCmd.Content)
	}

	// 9. diagnostics (clean -> error after write -> deduped -> re-surfaced)
	diagClean, err := executeLspTool(ctx, zeroSDK, mgr, dir, LspParams{Action: "diagnostics", File: "app.fake"})
	if err != nil || diagClean.Content != "OK" {
		t.Fatalf("clean diagnostics: err=%v, out=%q", err, diagClean.Content)
	}

	// Write a broken file and run post-write diagnostics via collectAfterWrite + DiagnosticsLedger
	if err := os.WriteFile(mainFile, []byte("func greet(name) {\n  TYPE_ERROR\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ledger := NewDiagnosticsLedger()
	d1 := collectAfterWrite(ctx, zeroSDK, mgr, "sess-e2e", true, ledger, mainFile, dir, DefaultSettings(), true, false)
	notice1 := formatDiagnosticsNotice(d1)
	if !strings.Contains(notice1, "LSP Diagnostics (1 error(s)):") || !strings.Contains(notice1, "type mismatch on TYPE_ERROR") {
		t.Fatalf("expected post-write diagnostics, got %q", notice1)
	}
	// Identical diagnostic shifted to a different line is deduplicated
	if err := os.WriteFile(mainFile, []byte("\n\nfunc greet(name) {\n  TYPE_ERROR\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d2 := collectAfterWrite(ctx, zeroSDK, mgr, "sess-e2e", true, ledger, mainFile, dir, DefaultSettings(), true, false)
	if notice2 := formatDiagnosticsNotice(d2); notice2 != "" {
		t.Fatalf("expected shifted identical diagnostic to be deduped, got %q", notice2)
	}
	// Restore mainFile before rename test
	if err := os.WriteFile(mainFile, []byte("func greet(name) {\n  return greet(name)\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 10. formatting via formatContent
	fmtRes, err := formatContent(ctx, mgr, "sess-e2e", true, 0, mainFile, "func greet(name) {\n  return greet(name)\n}\n", dir, GetServersForFile(GetConfig(dir), mainFile))
	if err != nil || fmtRes.Failed || fmtRes.Unsupported || !strings.HasPrefix(fmtRes.Content, "fn greet") {
		t.Fatalf("formatContent: err=%v, res=%+v", err, fmtRes)
	}

	// 11. rename preview then rename apply modifies both files on disk
	prevRes, err := executeLspTool(ctx, zeroSDK, mgr, dir, LspParams{
		Action: "rename", File: "app.fake", Line: new(1), Symbol: "greet", NewName: "hello", Apply: new(false),
	})
	if err != nil || !strings.Contains(prevRes.Content, "Rename preview:") {
		t.Fatalf("rename preview: err=%v, out=%s", err, prevRes.Content)
	}
	renRes, err := executeLspTool(ctx, zeroSDK, mgr, dir, LspParams{
		Action: "rename", File: "app.fake", Line: new(1), Symbol: "greet", NewName: "hello",
	})
	if err != nil || !strings.Contains(renRes.Content, "Applied rename:") {
		t.Fatalf("rename apply: err=%v, out=%s", err, renRes.Content)
	}
	gotMain, _ := os.ReadFile(mainFile)
	gotOther, _ := os.ReadFile(otherFile)
	if !strings.Contains(string(gotMain), "func hello(name)") || !strings.Contains(string(gotMain), "return hello(name)") {
		t.Fatalf("mainFile after rename:\n%s", string(gotMain))
	}
	if !strings.Contains(string(gotOther), "call hello(1)") {
		t.Fatalf("otherFile after rename:\n%s", string(gotOther))
	}

	// 12. raw request & capabilities
	capRes, err := executeLspTool(ctx, zeroSDK, mgr, dir, LspParams{Action: "capabilities", File: "app.fake"})
	if err != nil || !strings.Contains(capRes.Content, "renameProvider") {
		t.Fatalf("capabilities: err=%v, out=%s", err, capRes.Content)
	}
	rawRes, err := executeLspTool(ctx, zeroSDK, mgr, dir, LspParams{Action: "request", File: "app.fake", Query: "test/echo", Payload: new(`{"ping":42}`)})
	if err != nil || !strings.Contains(rawRes.Content, `"ping": 42`) {
		t.Fatalf("raw request: err=%v, out=%s", err, rawRes.Content)
	}

	// 13. Shutdown leaves no server process alive
	mgr.ShutdownAll()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && isProcessAlive(serverPID) {
		time.Sleep(20 * time.Millisecond)
	}
	if isProcessAlive(serverPID) {
		t.Fatalf("LSP server process %d still alive after ShutdownAll", serverPID)
	}
}

// upstream: lsp-regressions.test.ts: "reports rustc pushes alongside pull diagnostics from dual-mode servers (#17657)", "retries a diagnostics pull the server declines with ServerCancelled"
func TestPullAndDualModeDiagnostics(t *testing.T) {
	isolateEnv(t)
	setTestHooks(t, true, "sess-dual", nil)
	dir := t.TempDir()
	srcFile := filepath.Join(dir, "main.pull")
	if err := os.WriteFile(srcFile, []byte("fn main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	spec := fakeServerSpec{
		Mode:              "dual",
		PullRetriggerOnce: true,
		PushDiagnostics: []Diagnostic{
			{
				Range:    Range{Start: Position{0, 0}, End: Position{0, 2}},
				Severity: SeverityWarning,
				Source:   "rustc",
				Message:  "unused variable: x",
			},
		},
		PullDiagnostics: []Diagnostic{
			{
				Range:    Range{Start: Position{0, 3}, End: Position{0, 7}},
				Severity: SeverityError,
				Source:   "rust-analyzer",
				Message:  "syntax error",
			},
		},
	}
	cfg := fakeServerConfig(t, ".pull", spec)
	writeLspJSON(t, dir, "dual-lsp", cfg)
	InvalidateConfig(dir)
	defer InvalidateConfig(dir)

	mgr := newClientManager()
	defer mgr.ShutdownAll()

	var zeroSDK sdk.Context
	res, err := executeLspTool(context.Background(), zeroSDK, mgr, dir, LspParams{
		Action: "diagnostics", File: "main.pull", Timeout: new(5.0),
	})
	if err != nil {
		t.Fatalf("dual diagnostics err: %v", err)
	}
	if !strings.Contains(res.Content, "syntax error") || !strings.Contains(res.Content, "unused variable: x") {
		t.Fatalf("expected both pulled and pushed diagnostics merged, got:\n%s", res.Content)
	}
}

// upstream: lsp-regressions.test.ts: "drains every workspace/configuration pull during lazy cold start when a pull id collides with an in-flight request (#3001)"
func TestServerRequestIdCollisionAndJunkHeaderResync(t *testing.T) {
	isolateEnv(t)
	dir := t.TempDir()
	srcFile := filepath.Join(dir, "test.col")
	if err := os.WriteFile(srcFile, []byte("let x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	spec := fakeServerSpec{
		Mode: "config_pull",
	}
	cfg := fakeServerConfig(t, ".col", spec)
	cfg.Settings = map[string]any{"myLang": map[string]any{"strict": true}}

	mgr := newClientManager()
	defer mgr.ShutdownAll()

	client, err := mgr.GetOrCreateClient(context.Background(), cfg, dir, 5*time.Second)
	if err != nil {
		t.Fatalf("GetOrCreateClient: %v", err)
	}
	// Give the server->client workspace/configuration exchange a brief moment to complete
	time.Sleep(50 * time.Millisecond)
	raw, err := client.SendRequest(context.Background(), "test/state", map[string]any{}, 5*time.Second)
	if err != nil {
		t.Fatalf("test/state: %v", err)
	}
	var st struct {
		ConfigReplies []json.RawMessage `json:"configReplies"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	if len(st.ConfigReplies) == 0 || !strings.Contains(string(st.ConfigReplies[0]), `"strict":true`) || !strings.Contains(string(st.ConfigReplies[0]), `null`) {
		t.Fatalf("expected workspace/configuration reply [{\"strict\":true},null], got %v", st.ConfigReplies)
	}
}

// upstream: lsp-regressions.test.ts: "aborts a wedged cold-start initialize on the caller signal (#3962)"
func TestWedgedInitHonorsTimeoutAndLeavesNoProcess(t *testing.T) {
	isolateEnv(t)
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "wedged.pid")
	cfg := fakeServerConfig(t, ".wedge", fakeServerSpec{Mode: "hang_init", PIDFile: pidFile})

	mgr := newClientManager()
	defer mgr.ShutdownAll()

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := mgr.GetOrCreateClient(ctx, cfg, dir, 0)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("expected timeout/cancel error for wedged initialize")
	}
	if elapsed > 3*time.Second {
		t.Fatalf("wedged initialize took too long: %v", elapsed)
	}

	// Require the helper process to have actually started and written its PID file.
	pidBytes, rErr := os.ReadFile(pidFile)
	if rErr != nil {
		t.Fatalf("expected wedged helper to write PID file %s: %v", pidFile, rErr)
	}
	pid, convErr := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if convErr != nil || pid <= 0 {
		t.Fatalf("invalid wedged helper PID %q", string(pidBytes))
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && isProcessAlive(pid) {
		time.Sleep(20 * time.Millisecond)
	}
	if isProcessAlive(pid) {
		t.Fatalf("wedged server PID %d leaked after cancelled init", pid)
	}
}

// upstream: lsp-regressions.test.ts: "surfaces the process diagnostic when stdout closes before exit publication (#7041)"
func TestCrashOnInitializeSurfacesStderr(t *testing.T) {
	isolateEnv(t)
	dir := t.TempDir()
	cfg := fakeServerConfig(t, ".crash", fakeServerSpec{Mode: "crash"})

	mgr := newClientManager()
	defer mgr.ShutdownAll()

	_, err := mgr.GetOrCreateClient(context.Background(), cfg, dir, 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "simulated server crash") {
		t.Fatalf("expected stderr crash message, got %v", err)
	}
}

// TestDiagnosticsAfterWriteExport verifies the exported hook called by internal/files:
// real session id enables dedupe, unresolved session id skips dedupe, and edit defaults off.
func TestDiagnosticsAfterWriteExport(t *testing.T) {
	isolateEnv(t)
	setTestHooks(t, true, "sess-diag-export", nil)
	dir := t.TempDir()
	target := filepath.Join(dir, "broken.fake")
	if err := os.WriteFile(target, []byte("var x = TYPE_ERROR\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := fakeServerConfig(t, ".fake", fakeServerSpec{})
	writeLspJSON(t, dir, "fake-lsp", cfg)
	InvalidateConfig(dir)
	defer func() {
		ShutdownAll()
		InvalidateConfig(dir)
		resetLedgers()
	}()

	var zeroSDK sdk.Context
	out := DiagnosticsAfterWrite(zeroSDK, target)
	if !strings.Contains(out, "LSP Diagnostics (1 error(s)):") || !strings.Contains(out, "type mismatch on TYPE_ERROR") {
		t.Fatalf("expected diagnostics notice from DiagnosticsAfterWrite, got %q", out)
	}

	// Second call in the same session with identical diagnostic is deduplicated to "".
	if second := DiagnosticsAfterWrite(zeroSDK, target); second != "" {
		t.Fatalf("expected deduped empty string on second write, got %q", second)
	}

	// When session ID cannot be resolved, dedupe is skipped so sessions never suppress each other.
	hooksMu.Lock()
	sessionIDResolver = func(sdk.Context) (string, error) { return "", nil }
	hooksMu.Unlock()
	if unreduced := DiagnosticsAfterWrite(zeroSDK, target); !strings.Contains(unreduced, "type mismatch on TYPE_ERROR") {
		t.Fatalf("unresolved session id must skip dedupe, got %q", unreduced)
	}
	if unreduced2 := DiagnosticsAfterWrite(zeroSDK, target); !strings.Contains(unreduced2, "type mismatch on TYPE_ERROR") {
		t.Fatalf("second call with unresolved session id must also stay unreduced, got %q", unreduced2)
	}

	// File type with no matching server returns empty string cleanly.
	plain := filepath.Join(dir, "notes.unknownext")
	if err := os.WriteFile(plain, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if none := DiagnosticsAfterWrite(zeroSDK, plain); none != "" {
		t.Fatalf("expected empty string when no server applies, got %q", none)
	}

	// DiagnosticsAfterEdit defaults to off (lsp.diagnosticsOnEdit=false).
	if editOut := DiagnosticsAfterEdit(zeroSDK, target); editOut != "" {
		t.Fatalf("expected empty string from DiagnosticsAfterEdit with default settings, got %q", editOut)
	}
}

// =============================================================================
// 5. Real `gopls` Integration Test (skipped automatically if gopls is not runnable)
// =============================================================================

func findRunnableGopls() string {
	path, err := exec.LookPath("gopls")
	if err != nil || path == "" {
		return ""
	}
	// Verify gopls is actually runnable (guards against unconfigured mise shims).
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, path, "version").Run(); err != nil {
		return ""
	}
	return path
}

func TestGoplsIntegration(t *testing.T) {
	goplsPath := findRunnableGopls()
	if goplsPath == "" {
		t.Skip("runnable gopls not found on PATH; skipping real gopls integration test")
	}
	isolateEnv(t)
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/lsptest\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mainGo := filepath.Join(dir, "main.go")
	helperGo := filepath.Join(dir, "helper.go")

	if err := os.WriteFile(helperGo, []byte("package main\n\n// ComputeSum adds two integers.\nfunc ComputeSum(a, b int) int {\n\treturn a + b\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mainGo, []byte("package main\n\nfunc main() {\n\t_ = ComputeSum(1, 2)\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	InvalidateConfig(dir)
	defer InvalidateConfig(dir)

	mgr := newClientManager()
	defer mgr.ShutdownAll()

	ctx := context.Background()
	var zeroSDK sdk.Context

	// Definition from callsite in main.go -> helper.go
	defRes, err := executeLspTool(ctx, zeroSDK, mgr, dir, LspParams{
		Action: "definition", File: "main.go", Line: new(4), Symbol: "ComputeSum", Timeout: new(30.0),
	})
	if err != nil {
		t.Fatalf("gopls definition err: %v", err)
	}
	if !strings.Contains(defRes.Content, "helper.go:4:") {
		t.Fatalf("expected definition in helper.go:4, got:\n%s", defRes.Content)
	}

	// Hover
	hovRes, err := executeLspTool(ctx, zeroSDK, mgr, dir, LspParams{
		Action: "hover", File: "main.go", Line: new(4), Symbol: "ComputeSum", Timeout: new(30.0),
	})
	if err != nil || !strings.Contains(hovRes.Content, "ComputeSum") {
		t.Fatalf("gopls hover: err=%v, out=%s", err, hovRes.Content)
	}

	// References across both files
	refRes, err := executeLspTool(ctx, zeroSDK, mgr, dir, LspParams{
		Action: "references", File: "helper.go", Line: new(4), Symbol: "ComputeSum", Timeout: new(30.0),
	})
	if err != nil || !strings.Contains(refRes.Content, "main.go:4:") {
		t.Fatalf("gopls references: err=%v, out=%s", err, refRes.Content)
	}

	// Rename across both files
	renRes, err := executeLspTool(ctx, zeroSDK, mgr, dir, LspParams{
		Action: "rename", File: "helper.go", Line: new(4), Symbol: "ComputeSum", NewName: "AddInts", Timeout: new(30.0),
	})
	if err != nil || !strings.Contains(renRes.Content, "Applied rename:") {
		t.Fatalf("gopls rename: err=%v, out=%s", err, renRes.Content)
	}
	mainAfter, _ := os.ReadFile(mainGo)
	helperAfter, _ := os.ReadFile(helperGo)
	if !strings.Contains(string(mainAfter), "AddInts(1, 2)") || !strings.Contains(string(helperAfter), "func AddInts(") {
		t.Fatalf("rename did not update both files:\nmain.go:\n%s\nhelper.go:\n%s", mainAfter, helperAfter)
	}

	// Diagnostics after write with a real compile error
	if err := os.WriteFile(mainGo, []byte("package main\n\nfunc main() {\n\t_ = UndefinedFunc()\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ledger := NewDiagnosticsLedger()
	d := collectAfterWrite(ctx, zeroSDK, mgr, "gopls-sess", true, ledger, mainGo, dir, DefaultSettings(), true, false)
	notice := formatDiagnosticsNotice(d)
	if !strings.Contains(notice, "UndefinedFunc") {
		t.Fatalf("expected gopls diagnostic for UndefinedFunc, got %q", notice)
	}

	// Capture gopls PID before shutdown and verify clean exit
	var goplsPID int
	mgr.mu.Lock()
	for _, c := range mgr.clients {
		if c.cmd != nil && c.cmd.Process != nil {
			goplsPID = c.cmd.Process.Pid
		}
	}
	mgr.mu.Unlock()

	mgr.ShutdownAll()
	if goplsPID > 0 {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) && isProcessAlive(goplsPID) {
			time.Sleep(50 * time.Millisecond)
		}
		if isProcessAlive(goplsPID) {
			t.Fatalf("gopls PID %d still alive after shutdown", goplsPID)
		}
	}
}

// upstream: packages/coding-agent/src/lsp/utils.ts:359-408, packages/tui/src/theme/symbols.ts:477-603,846-1079,1284-1408
func TestSymbolKindIconsAndPresets(t *testing.T) {
	cases := []struct {
		preset string
		fn     string
		file   string
		folder string
		pkg    string
		class  string
		field  string
	}{
		{preset: "", fn: "⟲", file: "📄", folder: "📁", pkg: "📦", class: "⬢", field: "•"},
		{preset: "unicode", fn: "⟲", file: "📄", folder: "📁", pkg: "📦", class: "⬢", field: "•"},
		{preset: "nerd", fn: "\U000f0068", file: "\uf15b", folder: "\uf115", pkg: "\uf487", class: "\uec19", field: "\uf111"},
		{preset: "ascii", fn: "[A]", file: "[F]", folder: "[D]", pkg: "[P]", class: "[M]", field: "*"},
	}
	for _, tc := range cases {
		if got := symbolKindToIcon(12, tc.preset); got != tc.fn {
			t.Fatalf("preset=%q Function(12): got %q, want %q", tc.preset, got, tc.fn)
		}
		if got := symbolKindToIcon(6, tc.preset); got != tc.fn {
			t.Fatalf("preset=%q Method(6): got %q, want %q", tc.preset, got, tc.fn)
		}
		if got := symbolKindToIcon(9, tc.preset); got != tc.fn {
			t.Fatalf("preset=%q Constructor(9): got %q, want %q", tc.preset, got, tc.fn)
		}
		if got := symbolKindToIcon(1, tc.preset); got != tc.file {
			t.Fatalf("preset=%q File(1): got %q, want %q", tc.preset, got, tc.file)
		}
		if got := symbolKindToIcon(2, tc.preset); got != tc.folder {
			t.Fatalf("preset=%q Module(2): got %q, want %q", tc.preset, got, tc.folder)
		}
		if got := symbolKindToIcon(23, tc.preset); got != tc.folder {
			t.Fatalf("preset=%q Struct(23): got %q, want %q", tc.preset, got, tc.folder)
		}
		if got := symbolKindToIcon(4, tc.preset); got != tc.pkg {
			t.Fatalf("preset=%q Package(4): got %q, want %q", tc.preset, got, tc.pkg)
		}
		if got := symbolKindToIcon(5, tc.preset); got != tc.class {
			t.Fatalf("preset=%q Class(5): got %q, want %q", tc.preset, got, tc.class)
		}
		if got := symbolKindToIcon(11, tc.preset); got != tc.class {
			t.Fatalf("preset=%q Interface(11): got %q, want %q", tc.preset, got, tc.class)
		}
		if got := symbolKindToIcon(8, tc.preset); got != tc.field {
			t.Fatalf("preset=%q Field(8): got %q, want %q", tc.preset, got, tc.field)
		}
		if got := symbolKindToIcon(999, tc.preset); got != tc.field {
			t.Fatalf("preset=%q unknown(999): got %q, want %q", tc.preset, got, tc.field)
		}
	}

	// End-to-end symbols output with fake server: default unicode emits ⟲, Setting hook selects ascii/nerd.
	dir := t.TempDir()
	mainFile := filepath.Join(dir, "app.fake")
	if err := os.WriteFile(mainFile, []byte("func greet(name) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mainURI := fileToUri(mainFile)
	spec := fakeServerSpec{
		DocSymbols: []DocumentSymbol{
			{
				Name:           "greet",
				Detail:         "(name)",
				Kind:           12,
				Range:          Range{Start: Position{0, 0}, End: Position{0, 19}},
				SelectionRange: Range{Start: Position{0, 5}, End: Position{0, 10}},
			},
		},
		WorkspaceSymbols: []SymbolInformation{
			{
				Name:     "greet",
				Kind:     12,
				Location: Location{URI: mainURI, Range: Range{Start: Position{0, 0}, End: Position{0, 10}}},
			},
		},
	}
	cfg := fakeServerConfig(t, ".fake", spec)
	writeLspJSON(t, dir, "fake-lsp", cfg)
	InvalidateConfig(dir)
	defer InvalidateConfig(dir)
	setTestHooks(t, true, "sess-sym-icons", nil)
	mgr := newClientManager()
	defer mgr.ShutdownAll()
	ctx := context.Background()
	var zeroSDK sdk.Context

	resDefault, err := executeLspTool(ctx, zeroSDK, mgr, dir, LspParams{Action: "symbols", File: "app.fake"})
	if err != nil {
		t.Fatalf("default symbols: %v", err)
	}
	if !strings.Contains(resDefault.Content, "⟲ greet (name) @ line 1") {
		t.Fatalf("expected unicode function icon ⟲ in document symbols, got:\n%s", resDefault.Content)
	}

	wsDefault, err := executeLspTool(ctx, zeroSDK, mgr, dir, LspParams{Action: "symbols", File: "*", Query: "greet"})
	if err != nil {
		t.Fatalf("default workspace symbols: %v", err)
	}
	if !strings.Contains(wsDefault.Content, "  ⟲ greet @ app.fake:1:1") {
		t.Fatalf("expected unicode function icon ⟲ in workspace symbols, got:\n%s", wsDefault.Content)
	}

	hooksMu.Lock()
	prevSetting := Setting
	Setting = func(_ sdk.Context, key string) any {
		if key == "symbolPreset" {
			return "ascii"
		}
		return nil
	}
	hooksMu.Unlock()
	t.Cleanup(func() {
		hooksMu.Lock()
		Setting = prevSetting
		hooksMu.Unlock()
	})

	resASCII, err := executeLspTool(ctx, zeroSDK, mgr, dir, LspParams{Action: "symbols", File: "app.fake"})
	if err != nil {
		t.Fatalf("ascii symbols: %v", err)
	}
	if !strings.Contains(resASCII.Content, "[A] greet (name) @ line 1") {
		t.Fatalf("expected ascii function icon [A] in document symbols, got:\n%s", resASCII.Content)
	}

	// Diagnostics details must omit `request` (upstream: lsp/tool.ts:646-650,659).
	diagRes, err := executeLspTool(ctx, zeroSDK, mgr, dir, LspParams{Action: "diagnostics", File: "app.fake"})
	if err != nil {
		t.Fatalf("diagnostics: %v", err)
	}
	d, ok := diagRes.Details.(LspToolDetails)
	if !ok {
		t.Fatalf("expected LspToolDetails, got %T", diagRes.Details)
	}
	if d.Request != nil {
		t.Fatalf("expected diagnostics details.request to be nil, got %+v", d.Request)
	}
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"request"`) {
		t.Fatalf("expected diagnostics details JSON to omit request, got %s", raw)
	}
}
