package ast

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/dirs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/gate"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/hashline"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/toolcmd"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// installStrictFakeAstGrep places an executable fake `ast-grep` binary on PATH
// that enforces the real ast-grep CLI contract:
//   - `--version` prints `ast-grep 0.36.0`
//   - Rejects unknown flags with exit code 2 (like clap)
//   - Rejects `--pattern`/`--rewrite`/`--lang` values starting with `-` when passed as a separate token (clap without allow_hyphen_values)
//   - Rejects languages outside stock ast-grep's built-in language set with exit code 2
//   - In file-argument mode (without `--stdin`), enforces stock ast-grep's `RunWithSpecificLang::produce_item`
//     behaviour: files whose case-sensitive extension does not map to `--lang` return zero matches!
//   - In `--stdin` mode, reads source from stdin and reports `"file":"STDIN"`.
//   - Records every non-`--version` invocation's exact argv (tab-separated) to the returned log path.
func installStrictFakeAstGrep(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell fake ast-grep helper runs on Unix; Windows uses unit tests and cross-compile vet")
	}
	resetProbeCacheForTests()
	t.Cleanup(resetProbeCacheForTests)

	binDir := t.TempDir()
	recordLog := filepath.Join(binDir, "invocations.log")
	tmpl := `#!/bin/sh
if [ "$1" = "--version" ]; then
  printf 'ast-grep 0.40.5\n'
  exit 0
fi
RECORD_LOG=__RECORD_LOG__
{
  first=1
  for a in "$@"; do
    if [ "$first" -eq 1 ]; then
      printf '%s' "$a"
      first=0
    else
      printf '\t%s' "$a"
    fi
  done
  printf '\n'
} >> "$RECORD_LOG"

if [ "$1" != "run" ]; then
  printf 'error: unrecognized subcommand %s\n' "$1" >&2
  exit 2
fi
shift

PATTERN=""
REWRITE=""
HAS_REWRITE=0
LANG=""
JSON_STYLE=""
STDIN_MODE=0
END_OPTS=0

while [ $# -gt 0 ]; do
  if [ "$END_OPTS" -eq 1 ]; then
    break
  fi
  case "$1" in
    --pattern=*) PATTERN="${1#--pattern=}"; shift ;;
    --pattern)
      case "$2" in
        -*) printf "error: unexpected argument '%s' found\n" "$2" >&2; exit 2 ;;
      esac
      PATTERN="$2"; shift 2 ;;
    --rewrite=*) REWRITE="${1#--rewrite=}"; HAS_REWRITE=1; shift ;;
    --rewrite)
      case "$2" in
        -*) printf "error: unexpected argument '%s' found\n" "$2" >&2; exit 2 ;;
      esac
      REWRITE="$2"; HAS_REWRITE=1; shift 2 ;;
    --lang=*) LANG="${1#--lang=}"; shift ;;
    --lang) LANG="$2"; shift 2 ;;
    --json=*) JSON_STYLE="${1#--json=}"; shift ;;
    --no-ignore=*) shift ;;
    --stdin) STDIN_MODE=1; shift ;;
    --) END_OPTS=1; shift; break ;;
    -*)
      printf "error: unexpected argument '%s' found\n" "$1" >&2
      exit 2 ;;
    *) break ;;
  esac
done

if [ "$JSON_STYLE" != "stream" ]; then
  printf "error: expected --json=stream, got '%s'\n" "$JSON_STYLE" >&2
  exit 2
fi

case "$LANG" in
  bash|c|cpp|csharp|css|elixir|go|haskell|hcl|html|java|javascript|json|kotlin|lua|nix|php|python|ruby|rust|scala|solidity|swift|tsx|typescript|yaml)
    ;;
  *)
    printf "error: invalid value '%s' for '--lang <LANG>': %s is not supported!\n\nFor more information, try '--help'.\n" "$LANG" "$LANG" >&2
    exit 2 ;;
esac

# Enforce real ast-grep RunWithSpecificLang file-extension check when not using --stdin.
if [ "$STDIN_MODE" -eq 0 ]; then
  for f in "$@"; do
    base=$(basename "$f")
    ext=""
    case "$base" in
      ?*.*) ext="${base##*.}" ;;
    esac
    flang=""
    case "$ext" in
      ts|mts|cts) flang="typescript" ;;
      tsx) flang="tsx" ;;
      js|jsx|mjs|cjs) flang="javascript" ;;
      json) flang="json" ;;
      c|h) flang="c" ;;
      cc|cpp|hpp|hh|cxx|cu|ino) flang="cpp" ;;
      py|py3|pyi) flang="python" ;;
      go) flang="go" ;;
      rs) flang="rust" ;;
      sh|bash|zsh) flang="bash" ;;
    esac
    if [ "$flang" != "$LANG" ]; then
      printf "STRICT_FAKE_MISMATCH: file=%s flang=%s requested=%s\n" "$f" "$flang" "$LANG" >&2
      exit 1
    fi
  done
fi

__BODY__
`
	script := strings.NewReplacer(
		"__RECORD_LOG__", fmt.Sprintf("%q", recordLog),
		"__BODY__", body,
	).Replace(tmpl)
	binPath := filepath.Join(binDir, "ast-grep")
	if err := os.WriteFile(binPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ast-grep: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+"/usr/bin"+string(os.PathListSeparator)+"/bin")
	return recordLog
}

// fakeLocalURLHandler implements urls.Handler + urls.Locator for local:// in tests.
type fakeLocalURLHandler struct {
	root string
}

func (f fakeLocalURLHandler) Scheme() string { return "local" }
func (f fakeLocalURLHandler) Spec() urls.Spec {
	return urls.Spec{
		Backing:       urls.BackingFile,
		Selectors:     urls.SelectorsLines,
		PathAuthority: true,
		Write:         &urls.WritePolicy{Via: "file", Payload: "text", Scope: "local"},
	}
}
func (f fakeLocalURLHandler) Resolve(_ urls.Context, u urls.URL) (*urls.Resource, error) {
	return &urls.Resource{URL: u.Href}, nil
}
func (f fakeLocalURLHandler) Locate(_ urls.Context, u urls.URL, _ urls.LocateOptions) (string, bool, error) {
	rel := strings.TrimPrefix(u.Host+"/"+strings.TrimPrefix(u.Path, "/"), "/")
	rel = strings.TrimRight(rel, "/")
	return filepath.Join(f.root, filepath.FromSlash(rel)), true, nil
}

// fakeReadOnlyURLHandler implements a read-only scheme with no file write policy.
type fakeReadOnlyURLHandler struct {
	root string
}

func (f fakeReadOnlyURLHandler) Scheme() string { return "readonly" }
func (f fakeReadOnlyURLHandler) Spec() urls.Spec {
	return urls.Spec{
		Backing:   urls.BackingFile,
		Immutable: true,
	}
}
func (f fakeReadOnlyURLHandler) Resolve(_ urls.Context, u urls.URL) (*urls.Resource, error) {
	return &urls.Resource{URL: u.Href}, nil
}
func (f fakeReadOnlyURLHandler) Locate(_ urls.Context, u urls.URL, _ urls.LocateOptions) (string, bool, error) {
	rel := strings.TrimPrefix(u.Host+"/"+strings.TrimPrefix(u.Path, "/"), "/")
	return filepath.Join(f.root, filepath.FromSlash(rel)), true, nil
}

// --- Registration, schemas, toolcmd providers, and special-tool activation ---

func TestRegisterDefaultsSchemasAndToolcmdExposure(t *testing.T) {
	toolcmd.ResetForTests()
	t.Cleanup(toolcmd.ResetForTests)

	ext := sdk.New("test-ast")
	Register(ext)
	toolcmd.Register(ext)
	h := startHostWithSession(t, ext, t.TempDir(), "sess-toolcmd-reg", []string{"bash"}, sdk.ToolInfo{
		Name:     "bash",
		Exposure: sdk.ToolExposureDirect,
		Source:   "builtin",
	})

	// All 4 tools register with ToolExposureCodemode and DefaultActive: false so
	// toolcmd owns activation (mounted in bash vs top-level when transport is off).
	for _, name := range []string{"ast_grep", "ast_edit", "resolve", "reject"} {
		tl, ok := h.tools[name]
		if !ok {
			t.Fatalf("%s not registered", name)
		}
		if tl.Exposure != sdk.ToolExposureCodemode {
			t.Fatalf("expected %s Exposure=codemode, got %q", name, tl.Exposure)
		}
		if tl.DefaultActive == nil || *tl.DefaultActive {
			t.Fatalf("expected %s DefaultActive=false, got %v", name, tl.DefaultActive)
		}
	}

	ag := h.tools["ast_grep"]
	if ag.Label != "AST Grep" {
		t.Fatalf("expected label AST Grep, got %q", ag.Label)
	}
	ae := h.tools["ast_edit"]

	// upstream: ast-edit.test.ts: "uses op entries as [{ pat, out }]"
	// upstream: ast-edit.test.ts: "remains strict-representable after strict adaptation"
	props, _ := ae.Parameters["properties"].(map[string]any)
	ops, _ := props["ops"].(map[string]any)
	if ops["type"] != "array" {
		t.Fatalf("expected ops.type=array, got %v", ops["type"])
	}
	items, _ := ops["items"].(map[string]any)
	if items["type"] != "object" || items["additionalProperties"] != false {
		t.Fatalf("expected strict object items in ops schema, got %v", items)
	}
	if _, hasPreview := props["preview"]; hasPreview {
		t.Fatalf("expected no preview property in ast_edit schema")
	}
	if ae.Parameters["additionalProperties"] != false || ag.Parameters["additionalProperties"] != false {
		t.Fatalf("expected additionalProperties=false on ast_edit and ast_grep schemas")
	}

	// With bash + tools.commands active, toolcmd mounts ast_edit (default on; kept
	// active for fallback direct calls while hidden from model declarations via
	// PrepareLoadout) and keeps ast_grep (default off), resolve, and reject inactive.
	prevToolcmdEnabled := toolcmd.Enabled
	t.Cleanup(func() { toolcmd.Enabled = prevToolcmdEnabled })
	toolcmd.Enabled = func(_ sdk.Context, key string, def bool) bool { return def }

	h.emitEvent("session_start", map[string]any{})
	h.mu.Lock()
	active := slices.Clone(h.activeTools)
	h.mu.Unlock()
	for _, name := range []string{"ast_grep", "resolve", "reject"} {
		if slices.Contains(active, name) {
			t.Fatalf("expected %s NOT active top-level while bash+tools.commands active, active=%v", name, active)
		}
	}
	if !slices.Contains(active, "ast_edit") {
		t.Fatalf("expected mounted ast_edit active for fallback dispatch, active=%v", active)
	}

	// When tools.commands is disabled (or bash is absent), toolcmd activates enabled
	// mountable tools (ast_edit) AND resolve/reject statically (matching omp tools/index.ts:974-981)
	// so resolve/reject are already available in the same run that stages a preview.
	toolcmd.Enabled = func(_ sdk.Context, key string, def bool) bool {
		if key == "tools.commands" {
			return false
		}
		return def
	}
	h.emitEvent("session_start", map[string]any{})
	h.mu.Lock()
	activeNoCmd := slices.Clone(h.activeTools)
	h.mu.Unlock()
	if !slices.Contains(activeNoCmd, "ast_edit") {
		t.Fatalf("expected ast_edit active top-level when tools.commands=false, active=%v", activeNoCmd)
	}
	if slices.Contains(activeNoCmd, "ast_grep") {
		t.Fatalf("expected ast_grep inactive when astGrep.enabled=false, active=%v", activeNoCmd)
	}
	if !slices.Contains(activeNoCmd, "resolve") || !slices.Contains(activeNoCmd, "reject") {
		t.Fatalf("expected resolve/reject active top-level when tools.commands=false and ast_edit enabled, active=%v", activeNoCmd)
	}

	// When astEdit.enabled is false and tools.commands is off, resolve/reject stay inactive.
	prevEnabled := Enabled
	t.Cleanup(func() { Enabled = prevEnabled })
	Enabled = func(_ sdk.Context, key string, def bool) bool {
		if key == SettingAstEditEnabled {
			return false
		}
		return def
	}
	h.emitEvent("session_start", map[string]any{})
	h.mu.Lock()
	activeNoAstEdit := slices.Clone(h.activeTools)
	h.mu.Unlock()
	if slices.Contains(activeNoAstEdit, "resolve") || slices.Contains(activeNoAstEdit, "reject") {
		t.Fatalf("expected resolve/reject inactive when astEdit.enabled=false, active=%v", activeNoAstEdit)
	}
}

func TestMountedToolsCallableViaHostExecuteToolRule(t *testing.T) {
	toolcmd.ResetForTests()
	t.Cleanup(toolcmd.ResetForTests)

	cwd := t.TempDir()
	filePath := filepath.Join(cwd, "sample.ts")
	writeTestFile(t, filePath, "legacyWrap(1, 2)\n")

	installStrictFakeAstGrep(t, `
for f in "$@"; do
  line=$(head -n 1 "$f")
  len=${#line}
  repl=$(printf '%s' "$line" | sed 's/legacyWrap/modernWrap/')
  if [ "$HAS_REWRITE" -eq 1 ]; then
    printf '{"text":"%s","range":{"byteOffset":{"start":0,"end":%d},"start":{"line":0,"column":0},"end":{"line":0,"column":%d}},"file":"%s","lines":"%s","replacement":"%s","replacementOffsets":{"start":0,"end":%d},"language":"TypeScript"}\n' "$line" "$len" "$len" "$f" "$line" "$repl" "$len"
  else
    printf '{"text":"%s","range":{"byteOffset":{"start":0,"end":%d},"start":{"line":0,"column":0},"end":{"line":0,"column":%d}},"file":"%s","lines":"%s","language":"TypeScript"}\n' "$line" "$len" "$len" "$f" "$line"
  fi
done
`)

	prevEnabled := Enabled
	t.Cleanup(func() { Enabled = prevEnabled })
	Enabled = func(_ sdk.Context, key string, def bool) bool {
		if key == SettingAstGrepEnabled {
			return true
		}
		return def
	}

	ext := sdk.New("test-ast-mounted-exec")
	Register(ext)
	toolcmd.Register(ext)

	// Register an inactive `direct` tool to verify our fake host truly rejects
	// inactive direct tools while allowing codemode tools (ast_grep, ast_edit, resolve).
	defaultOff := false
	ext.RegisterTool(sdk.ToolDefinition{
		Name:          "inactive_direct",
		Label:         "Inactive Direct",
		Description:   "direct tool left inactive",
		Parameters:    sdk.Schema{"type": "object"},
		Exposure:      sdk.ToolExposureDirect,
		DefaultActive: &defaultOff,
		Execute: func(_ sdk.Context, _ map[string]any) (any, error) {
			return sdk.ToolResult{Content: "should not run"}, nil
		},
	})

	// Register a bash harness tool that dispatches mounted commands through
	// toolcmd.TryDirect -> host Context.ExecuteTool (enforcing exposure rules).
	ext.RegisterTool(sdk.ToolDefinition{
		Name:        "bash",
		Label:       "Bash",
		Description: "test bash transport",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{"type": "string"},
			},
			"required": []string{"command"},
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			cmdline, _ := params["command"].(string)
			if cmdline == "probe_direct" {
				out, err := ctx.ExecuteTool("inactive_direct", map[string]any{}, nil)
				if err != nil {
					return nil, err
				}
				txt := ""
				if len(out.Result.Content) > 0 {
					txt = out.Result.Content[0].Text
				}
				return sdk.ToolResult{Content: txt, IsError: out.IsError}, nil
			}
			out, exitCode, handled := toolcmd.TryDirect(ctx, cmdline)
			if !handled {
				return sdk.ToolResult{Content: "not handled by toolcmd: " + cmdline, IsError: true}, nil
			}
			return sdk.ToolResult{Content: out, IsError: exitCode != 0}, nil
		},
	})

	h := startHostWithSession(t, ext, cwd, "sess-mounted-exec", []string{"bash"})
	t.Cleanup(func() { lifecycle.End("sess-mounted-exec") })
	h.emitEvent("session_start", map[string]any{})

	// Sanity check: host blocks calling an inactive `direct` tool via ExecuteTool.
	directRes, errMsg := h.callTool("bash", map[string]any{"command": "probe_direct"})
	if errMsg != "" || !directRes.IsError || !strings.Contains(directRes.Content, "not found") {
		t.Fatalf("expected fake host to reject inactive direct tool, got err=%q res=%+v", errMsg, directRes)
	}

	// Mounted ast_grep (inactive top-level, codemode exposure) executes through host ExecuteTool.
	grepCmd := fmt.Sprintf("ast_grep --pat 'legacyWrap($A, $B)' --path %q", filePath)
	grepRes, errMsg := h.callTool("bash", map[string]any{"command": grepCmd})
	if errMsg != "" || grepRes.IsError || !strings.Contains(grepRes.Content, "legacyWrap(1, 2)") {
		t.Fatalf("mounted ast_grep failed: err=%q res=%+v", errMsg, grepRes)
	}

	// Mounted ast_edit stages preview, then special command `resolve` applies it through host ExecuteTool.
	editJSON := fmt.Sprintf(`{"ops":[{"pat":"legacyWrap($A, $B)","out":"modernWrap($A, $B)"}],"paths":[%q]}`, filePath)
	editRes, errMsg := h.callTool("bash", map[string]any{
		"command": fmt.Sprintf("ast_edit --json '%s'", editJSON),
	})
	if errMsg != "" || editRes.IsError || !strings.Contains(editRes.Content, previewPendingNotice) {
		t.Fatalf("mounted ast_edit failed: err=%q res=%+v", errMsg, editRes)
	}
	resolveRes, errMsg := h.callTool("bash", map[string]any{
		"command": `resolve "apply mounted rewrite"`,
	})
	if errMsg != "" || resolveRes.IsError || !strings.Contains(resolveRes.Content, "Applied 1 replacement in 1 file.") {
		t.Fatalf("mounted special resolve failed: err=%q res=%+v", errMsg, resolveRes)
	}
	if b, _ := os.ReadFile(filePath); string(b) != "modernWrap(1, 2)\n" {
		t.Fatalf("expected rewritten file via mounted ast_edit+resolve, got %q", string(b))
	}
}

func TestAstGrepSharesSessionHashlineStoreWithEdit(t *testing.T) {
	cwd := t.TempDir()
	filePath := filepath.Join(cwd, "chain.ts")
	// Build a file whose unseen line is far beyond the shown match line so edit's
	// seen-line provenance check refuses an edit anchored on an unseen line.
	var lines []string
	for i := 1; i <= 40; i++ {
		if i == 3 {
			lines = append(lines, "const shownLine = legacyWrap(1, 2);")
		} else {
			lines = append(lines, fmt.Sprintf("const filler%02d = %d;", i, i))
		}
	}
	content := strings.Join(lines, "\n") + "\n"
	writeTestFile(t, filePath, content)

	installStrictFakeAstGrep(t, `
for f in "$@"; do
  printf '{"text":"const shownLine = legacyWrap(1, 2);","range":{"byteOffset":{"start":42,"end":77},"start":{"line":2,"column":0},"end":{"line":2,"column":35}},"file":"%s","lines":"const shownLine = legacyWrap(1, 2);","language":"TypeScript"}\n' "$f"
done
`)

	ext := sdk.New("test-ast-hashline-chain")
	Register(ext)
	// Register an edit tool using the exact same session store (hashline.ForSession(ctx))
	// and hashline.StageInput(EnforceSeenLines: true) as internal/files's edit tool.
	ext.RegisterTool(sdk.ToolDefinition{
		Name:        "edit",
		Label:       "Edit",
		Description: "test edit tool using hashline.ForSession",
		Parameters: sdk.Schema{
			"type":       "object",
			"properties": map[string]any{"input": map[string]any{"type": "string"}},
			"required":   []string{"input"},
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			rawInput, _ := params["input"].(string)
			store := hashline.ForSession(ctx)
			src := &hashline.DiskFileSource{WorkDir: ctx.Cwd()}
			staged, editErr := hashline.StageInput(rawInput, src, store, hashline.StageOptions{
				EnforceSeenLines: true,
				MaxColumns:       512,
			})
			if editErr != nil {
				return sdk.ToolResult{Content: editErr.Error(), IsError: true}, nil
			}
			for _, sf := range staged {
				if err := os.WriteFile(sf.Absolute, []byte(sf.Persisted), 0o644); err != nil {
					return nil, err
				}
			}
			return sdk.ToolResult{Content: "applied"}, nil
		},
	})
	h := startHostWithSession(t, ext, cwd, "sess-hashline-chain", []string{"edit"})
	t.Cleanup(func() { lifecycle.End("sess-hashline-chain") })
	grepRes, errMsg := h.callTool("ast_grep", map[string]any{
		"pat":  "legacyWrap($A, $B)",
		"path": filePath,
	})
	if errMsg != "" || grepRes.IsError {
		t.Fatalf("ast_grep failed: err=%q res=%+v", errMsg, grepRes)
	}

	tagRe := regexp.MustCompile(`\[chain\.ts#([0-9A-F]{4})\]`)
	m := tagRe.FindStringSubmatch(grepRes.Content)
	if len(m) < 2 {
		t.Fatalf("expected [chain.ts#TAG] in ast_grep output:\n%s", grepRes.Content)
	}
	tag := m[1]

	// Editing a line NEVER shown by ast_grep (line 35) must be refused by edit's seen-line check.
	unseenPatch := fmt.Sprintf("[chain.ts#%s]\nPUT 35.=35:\n+const filler35 = 999;\n", tag)
	unseenRes, unseenErr := h.callTool("edit", map[string]any{"input": unseenPatch})
	if unseenErr == "" && !unseenRes.IsError {
		t.Fatalf("expected edit on unseen line 35 to be refused, got content=%q", unseenRes.Content)
	}

	// Editing the line SHOWN by ast_grep (line 3) using the tag ast_grep printed must succeed!
	seenPatch := fmt.Sprintf("[chain.ts#%s]\nPUT 3.=3:\n+const shownLine = modernWrap(1, 2);\n", tag)
	seenRes, seenErr := h.callTool("edit", map[string]any{"input": seenPatch})
	if seenErr != "" || seenRes.IsError {
		t.Fatalf("expected edit on ast_grep-shown line 3 to succeed, got err=%q res=%+v", seenErr, seenRes)
	}
	got, _ := os.ReadFile(filePath)
	if !strings.Contains(string(got), "const shownLine = modernWrap(1, 2);") {
		t.Fatalf("file was not updated by edit after ast_grep:\n%s", string(got))
	}
	if strings.Contains(string(got), "const filler35 = 999;") {
		t.Fatalf("unseen line 35 must remain unmodified")
	}
}

func TestAstEditResolveSnapshotIsNotAuthored(t *testing.T) {
	cwd := t.TempDir()
	filePath := filepath.Join(cwd, "mod.ts")
	var lines []string
	for i := 1; i <= 40; i++ {
		if i == 1 {
			lines = append(lines, "legacyWrap(1, 2);")
		} else {
			lines = append(lines, fmt.Sprintf("const filler%02d = %d;", i, i))
		}
	}
	writeTestFile(t, filePath, strings.Join(lines, "\n")+"\n")

	// Replace line 1 with two lines so lines 2..40 shift down and are NOT carried from
	// the pre-resolve snapshot; then verify Head().SeenLines is nil (not authored) and a
	// subsequent partial read of line 1 restricts unseen line 35 (whereas an authored
	// RecordSnapshot would have set SeenLines = empty non-nil map and ignored the partial read).
	installStrictFakeAstGrep(t, `
for f in "$@"; do
  if grep -q 'legacyWrap(1, 2)' "$f"; then
    printf '{"text":"legacyWrap(1, 2)","range":{"byteOffset":{"start":0,"end":16},"start":{"line":0,"column":0},"end":{"line":0,"column":16}},"file":"%s","lines":"legacyWrap(1, 2);","replacement":"modernWrap(\\n  1, 2)","replacementOffsets":{"start":0,"end":16},"language":"TypeScript"}\n' "$f"
  fi
done
`)

	ext := sdk.New("test-ast-resolve-not-authored")
	Register(ext)
	ext.RegisterTool(sdk.ToolDefinition{
		Name:        "partial_read_and_edit",
		Label:       "PartialReadAndEdit",
		Description: "test helper",
		Parameters:  sdk.Schema{"type": "object"},
		Execute: func(ctx sdk.Context, _ map[string]any) (any, error) {
			store := hashline.ForSession(ctx)
			snap, ok := store.Head(filePath)
			if !ok {
				return sdk.ToolResult{Content: "MISSING_HEAD", IsError: true}, nil
			}
			if snap.SeenLines != nil {
				return sdk.ToolResult{Content: fmt.Sprintf("EXPECTED_NIL_SEENLINES_GOT_%v", snap.SeenLines), IsError: true}, nil
			}
			data, err := os.ReadFile(filePath)
			if err != nil {
				return nil, err
			}
			norm := hashline.NormalizeToLF(string(data))
			// Record a partial read showing only line 1 (like read :1-1).
			tag := store.RecordReadLines(filePath, norm, []int{1})
			src := &hashline.DiskFileSource{WorkDir: ctx.Cwd()}
			unseenPatch := fmt.Sprintf("[mod.ts#%s]\nPUT 35.=35:\n+const filler35 = 999;\n", tag)
			if _, editErr := hashline.StageInput(unseenPatch, src, store, hashline.StageOptions{
				EnforceSeenLines: true,
				MaxColumns:       512,
			}); editErr == nil {
				return sdk.ToolResult{Content: "UNEXPECTED_ALLOW", IsError: true}, nil
			}
			seenPatch := fmt.Sprintf("[mod.ts#%s]\nPUT 1.=1:\n+modernWrap(9, 9);\n", tag)
			if _, editErr := hashline.StageInput(seenPatch, src, store, hashline.StageOptions{
				EnforceSeenLines: true,
				MaxColumns:       512,
			}); editErr != nil {
				return sdk.ToolResult{Content: "UNEXPECTED_DENY: " + editErr.Error(), IsError: true}, nil
			}
			return sdk.ToolResult{Content: "OK"}, nil
		},
	})
	h := startHostWithSession(t, ext, cwd, "sess-resolve-not-authored", []string{"ast_edit", "resolve", "partial_read_and_edit"})
	t.Cleanup(func() { lifecycle.End("sess-resolve-not-authored") })

	if _, errMsg := h.callTool("ast_edit", map[string]any{
		"ops":   []any{map[string]any{"pat": "legacyWrap($A, $B)", "out": "modernWrap(\n  $A, $B)"}},
		"paths": []any{filePath},
	}); errMsg != "" {
		t.Fatalf("ast_edit preview failed: %s", errMsg)
	}
	applyRes, errMsg := h.callTool("resolve", map[string]any{"reason": "apply edit"})
	if errMsg != "" || applyRes.IsError {
		t.Fatalf("resolve failed: err=%q res=%+v", errMsg, applyRes)
	}
	probeRes, errMsg := h.callTool("partial_read_and_edit", map[string]any{})
	if errMsg != "" || probeRes.IsError || probeRes.Content != "OK" {
		t.Fatalf("expected partial read after resolve to restrict unseen line 35, got err=%q res=%+v", errMsg, probeRes)
	}
}

func TestExit8PatternCompileErrorContinuesOtherLanguagesAndFormatsErrorNode(t *testing.T) {
	cwd := t.TempDir()
	tsFile := filepath.Join(cwd, "a.ts")
	jsonFile := filepath.Join(cwd, "package.json")
	writeTestFile(t, tsFile, "const fn = (a) => a + 1;\n")
	writeTestFile(t, jsonFile, "{\"name\": \"demo\"}\n")

	installStrictFakeAstGrep(t, `
if [ "$LANG" = "json" ]; then
  cat >&2 <<'ERR'
Error: Cannot parse query as a valid pattern.
Help: The pattern either fails to parse or contains error. Please refer to pattern syntax guide.
See also: https://ast-grep.github.io/guide/pattern-syntax.html

✖ Caused by
╰▻ Multiple AST nodes are detected. Please check the pattern source.
ERR
  exit 8
fi
if [ "$LANG" = "typescript" ]; then
  for f in "$@"; do
    printf '{"text":"const fn = (a) => a + 1;","range":{"byteOffset":{"start":0,"end":24},"start":{"line":0,"column":0},"end":{"line":0,"column":24}},"file":"%s","lines":"const fn = (a) => a + 1;","replacement":"function fn(a) { return a + 1; }","replacementOffsets":{"start":0,"end":24},"language":"TypeScript"}\n' "$f"
  done
fi
`)

	ext := sdk.New("test-ast-exit8")
	Register(ext)
	h := startHost(t, ext, cwd, []string{"ast_grep", "ast_edit"})

	grepRes, errMsg := h.callTool("ast_grep", map[string]any{
		"pat":  "const $NAME = ($$$ARGS) => $BODY",
		"path": cwd,
	})
	if errMsg != "" || grepRes.IsError {
		t.Fatalf("expected ast_grep to succeed across mixed languages despite exit 8 on json, got err=%q res=%+v", errMsg, grepRes)
	}
	if mc, _ := grepRes.Details["matchCount"].(float64); int(mc) != 1 {
		t.Fatalf("expected 1 match from a.ts, got %v", grepRes.Details["matchCount"])
	}
	if !strings.Contains(grepRes.Content, "const $NAME = ($$$ARGS) => $BODY (json): Invalid pattern: Multiple AST nodes are detected.") {
		t.Fatalf("expected omp-shaped per-language parse issue in:\n%s", grepRes.Content)
	}

	editRes, errMsg := h.callTool("ast_edit", map[string]any{
		"ops":   []any{map[string]any{"pat": "const $NAME = ($$$ARGS) => $BODY", "out": "function $NAME($$$ARGS) { return $BODY; }"}},
		"paths": []any{cwd},
	})
	if errMsg != "" || editRes.IsError {
		t.Fatalf("expected ast_edit to succeed across mixed languages despite exit 8 on json, got err=%q res=%+v", errMsg, editRes)
	}
	if tr, _ := editRes.Details["totalReplacements"].(float64); int(tr) != 1 {
		t.Fatalf("expected 1 replacement in a.ts, got %v", editRes.Details["totalReplacements"])
	}

	// Verify ERROR-node stderr warning names the language per crates/ast/src/search/mod.rs:1407-1411.
	diags := parseStderrDiagnostics("Warning: Pattern contains an ERROR node and may cause unexpected results.\nHelp: ast-grep parsed the pattern...", "function (", "typescript")
	wantDiag := "function (: pattern is not valid typescript on its own (contains an ERROR node); matches may be missing or unexpected"
	if len(diags) != 1 || diags[0] != wantDiag {
		t.Fatalf("unexpected ERROR-node diagnostic:\ngot:  %q\nwant: %q", diags, wantDiag)
	}
}

// --- Binary identity verification, shadow-utils `sg` rejection, min version, and Windows .cmd guard ---

func TestRejectsShadowUtilsSgAndOldVersions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX script test")
	}
	resetProbeCacheForTests()
	t.Cleanup(resetProbeCacheForTests)

	cwd := t.TempDir()
	filePath := filepath.Join(cwd, "a.ts")
	writeTestFile(t, filePath, "console.log(1);\n")

	// 1. Stand-in mimicking Linux shadow-utils `/usr/bin/sg` (prints group error, exit 1).
	shadowDir := t.TempDir()
	writeTestFile(t, filepath.Join(shadowDir, "sg"), "#!/bin/sh\necho \"sg: group '$1' does not exist\" >&2\nexit 1\n")
	_ = os.Chmod(filepath.Join(shadowDir, "sg"), 0o755)
	t.Setenv("PATH", shadowDir)

	ext := sdk.New("test-ast-shadow-sg")
	Register(ext)
	h := startHost(t, ext, cwd, []string{"ast_grep", "ast_edit"})

	res, errMsg := h.callTool("ast_grep", map[string]any{"pat": "console.log($X)", "path": filePath})
	if errMsg != "" {
		t.Fatalf("unexpected transport error: %s", errMsg)
	}
	if !res.IsError || !strings.Contains(res.Content, "ast-grep (>= 0.40) is not installed") {
		t.Fatalf("expected missing-binary guidance when only shadow sg exists, got isError=%v content=%q", res.IsError, res.Content)
	}

	editRes, errMsg := h.callTool("ast_edit", map[string]any{
		"ops":   []any{map[string]any{"pat": "a", "out": "b"}},
		"paths": []any{filePath},
	})
	if errMsg != "" {
		t.Fatalf("unexpected transport error: %s", errMsg)
	}
	if !editRes.IsError || !strings.Contains(editRes.Content, "ast-grep (>= 0.40) is not installed") {
		t.Fatalf("expected missing-binary guidance from ast_edit, got isError=%v content=%q", editRes.IsError, editRes.Content)
	}

	// 2. Old ast-grep (< 0.40, missing HCL which arrived in 0.39.3) must be rejected.
	oldDir := t.TempDir()
	writeTestFile(t, filepath.Join(oldDir, "ast-grep"), "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo \"ast-grep 0.39.0\"; exit 0; fi\nexit 0\n")
	_ = os.Chmod(filepath.Join(oldDir, "ast-grep"), 0o755)
	resetProbeCacheForTests()
	t.Setenv("PATH", oldDir)

	if _, err := findAstGrepBinary(); err == nil {
		t.Fatalf("expected ast-grep 0.39.0 to be rejected as < 0.40")
	}

	// 3. Verified `sg` (>= 0.40) IS accepted when `ast-grep` is absent.
	goodSgDir := t.TempDir()
	writeTestFile(t, filepath.Join(goodSgDir, "sg"), "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo \"ast-grep 0.40.5\"; exit 0; fi\nexit 0\n")
	_ = os.Chmod(filepath.Join(goodSgDir, "sg"), 0o755)
	resetProbeCacheForTests()
	t.Setenv("PATH", goodSgDir)

	gotBin, err := findAstGrepBinary()
	if err != nil || filepath.Base(gotBin) != "sg" {
		t.Fatalf("expected verified sg binary to be accepted, got bin=%q err=%v", gotBin, err)
	}
}

func TestMissingBinaryReturnsGuidance(t *testing.T) {
	cwd := t.TempDir()
	filePath := filepath.Join(cwd, "a.ts")
	writeTestFile(t, filePath, "console.log(1);\n")

	prevLookPath := lookPathFunc
	t.Cleanup(func() { lookPathFunc = prevLookPath })
	lookPathFunc = func(string) (string, error) {
		return "", exec.ErrNotFound
	}

	ext := sdk.New("test-ast-missing")
	Register(ext)
	h := startHost(t, ext, cwd, []string{"ast_grep"})

	res, errMsg := h.callTool("ast_grep", map[string]any{"pat": "console.log($X)", "path": filePath})
	if errMsg != "" {
		t.Fatalf("unexpected transport error: %s", errMsg)
	}
	if !res.IsError || !strings.Contains(res.Content, "brew install ast-grep") {
		t.Fatalf("expected install guidance, got isError=%v content=%q", res.IsError, res.Content)
	}
}

// --- Explicit lang -> --stdin routing & unknown lang rejection ---

// upstream: ast-grep.test.ts: "honors an explicit C++ language for ambiguous headers"
// upstream: ast-grep.test.ts: "forwards an explicit language to every multi-target branch"
func TestAstGrepExplicitCppUsesStdinForHeaders(t *testing.T) {
	cwd := t.TempDir()
	dirA := filepath.Join(cwd, "a")
	dirB := filepath.Join(cwd, "b")
	writeTestFile(t, filepath.Join(dirA, "buffer.h"), "class Buffer { public: int size() const { return 1; } };\n")
	writeTestFile(t, filepath.Join(dirB, "buffer.h"), "class Buffer { public: int size() const { return 1; } };\n")

	logPath := installStrictFakeAstGrep(t, `
if [ "$STDIN_MODE" -eq 1 ]; then
  body=$(cat)
  case "$body" in
    *"class Buffer"*)
      printf '{"text":"class Buffer","range":{"byteOffset":{"start":0,"end":12},"start":{"line":0,"column":0},"end":{"line":0,"column":12}},"file":"STDIN","lines":"class Buffer {}","language":"Cpp"}\n'
      ;;
  esac
fi
`)

	ext := sdk.New("test-ast-cpp")
	Register(ext)
	h := startHost(t, ext, cwd, []string{"ast_grep"})

	res, errMsg := h.callTool("ast_grep", map[string]any{
		"pat":  "class Buffer",
		"path": dirA + ";" + dirB,
		"lang": "cpp",
	})
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if mc, _ := res.Details["matchCount"].(float64); int(mc) != 2 {
		t.Fatalf("expected matchCount=2, got %v", res.Details["matchCount"])
	}
	// Verify both `.h` files were fed via `--stdin` with `--lang=cpp`.
	logBytes, _ := os.ReadFile(logPath)
	lines := strings.Split(strings.TrimSpace(string(logBytes)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 --stdin invocations for 2 .h files, got %d:\n%s", len(lines), string(logBytes))
	}
	for _, l := range lines {
		if !strings.Contains(l, "--lang=cpp") || !strings.Contains(l, "--stdin") {
			t.Fatalf("expected --lang=cpp and --stdin in argv, got %q", l)
		}
	}
}

// upstream: ast-grep.test.ts: "skips unknown and extensionless files unless a language is explicitly selected"
func TestAstGrepUnknownAndExtensionlessFiles(t *testing.T) {
	cwd := t.TempDir()
	for _, name := range []string{"source.ts", "notes.unsupported", "extensionless"} {
		writeTestFile(t, filepath.Join(cwd, name), "console.log(1);\n")
	}

	logPath := installStrictFakeAstGrep(t, `
if [ "$STDIN_MODE" -eq 1 ]; then
  cat >/dev/null
  printf '{"text":"console.log(1)","range":{"byteOffset":{"start":0,"end":14},"start":{"line":0,"column":0},"end":{"line":0,"column":14}},"file":"STDIN","lines":"console.log(1);","language":"TypeScript"}\n'
else
  for f in "$@"; do
    printf '{"text":"console.log(1)","range":{"byteOffset":{"start":0,"end":14},"start":{"line":0,"column":0},"end":{"line":0,"column":14}},"file":"%s","lines":"console.log(1);","language":"TypeScript"}\n' "$f"
  done
fi
`)

	ext := sdk.New("test-ast-filter")
	Register(ext)
	h := startHost(t, ext, cwd, []string{"ast_grep"})

	inferred, errMsg := h.callTool("ast_grep", map[string]any{
		"pat":  "console.log($X)",
		"path": cwd,
	})
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if mc, _ := inferred.Details["matchCount"].(float64); int(mc) != 1 {
		t.Fatalf("expected matchCount=1 without lang, got %v", inferred.Details["matchCount"])
	}

	explicit, errMsg := h.callTool("ast_grep", map[string]any{
		"pat":  "console.log($X)",
		"path": cwd,
		"lang": "ts",
	})
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if mc, _ := explicit.Details["matchCount"].(float64); int(mc) != 3 {
		t.Fatalf("expected matchCount=3 with lang=ts, got %v", explicit.Details["matchCount"])
	}
	logBytes, _ := os.ReadFile(logPath)
	if !strings.Contains(string(logBytes), "--lang=typescript") || !strings.Contains(string(logBytes), "--stdin") {
		t.Fatalf("expected --lang=typescript and --stdin for extensionless/unknown files, got:\n%s", string(logBytes))
	}
}

func TestAstGrepUnknownLangReturnsOmpError(t *testing.T) {
	cwd := t.TempDir()
	writeTestFile(t, filepath.Join(cwd, "a.ts"), "foo()\n")

	ext := sdk.New("test-ast-bad-lang")
	Register(ext)
	h := startHost(t, ext, cwd, []string{"ast_grep"})

	_, errMsg := h.callTool("ast_grep", map[string]any{
		"pat":  "foo()",
		"path": cwd,
		"lang": "brainfuck",
	})
	if !strings.Contains(errMsg, "Unsupported language 'brainfuck'. Supported:") {
		t.Fatalf("expected omp unsupported language error, got %q", errMsg)
	}
}

// --- Unsupported stock-CLI languages skipped without bogus parse issues ---

func TestMixedDirectorySkipsStockUnsupportedLanguagesCleanly(t *testing.T) {
	cwd := t.TempDir()
	writeTestFile(t, filepath.Join(cwd, "a.ts"), "foo(1);\n")
	writeTestFile(t, filepath.Join(cwd, "Cargo.toml"), "[package]\nname = \"x\"\n")
	writeTestFile(t, filepath.Join(cwd, "Makefile"), "all:\n\techo hi\n")

	logPath := installStrictFakeAstGrep(t, `
for f in "$@"; do
  printf '{"text":"foo(1)","range":{"byteOffset":{"start":0,"end":6},"start":{"line":0,"column":0},"end":{"line":0,"column":6}},"file":"%s","lines":"foo(1);","language":"TypeScript"}\n' "$f"
done
`)

	ext := sdk.New("test-ast-mixed-langs")
	Register(ext)
	h := startHost(t, ext, cwd, []string{"ast_grep"})

	res, errMsg := h.callTool("ast_grep", map[string]any{"pat": "foo($A)", "path": cwd})
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if _, hasParseErrs := res.Details["parseErrors"]; hasParseErrs {
		t.Fatalf("expected no bogus parseErrors for unsupported stock-CLI languages, got %v", res.Details["parseErrors"])
	}
	if !strings.Contains(res.Content, "Skipped make, toml files:") {
		t.Fatalf("expected missing-language note for make and toml, got:\n%s", res.Content)
	}
	if fs, _ := res.Details["filesSearched"].(float64); int(fs) != 1 {
		t.Fatalf("expected filesSearched=1 (excluding skipped grammars), got %v", res.Details["filesSearched"])
	}
	logBytes, _ := os.ReadFile(logPath)
	if strings.Contains(string(logBytes), "toml") || strings.Contains(string(logBytes), "make") {
		t.Fatalf("ast-grep must not be spawned for unsupported languages, got:\n%s", string(logBytes))
	}
}

// --- Leading-dash patterns and rewrites passed via attached --pattern= / --rewrite= ---

func TestLeadingDashPatternsAndRewritesUseAttachedFlags(t *testing.T) {
	cwd := t.TempDir()
	filePath := filepath.Join(cwd, "math.ts")
	writeTestFile(t, filePath, "const x = -y;\n")

	logPath := installStrictFakeAstGrep(t, `
for f in "$@"; do
  if [ "$HAS_REWRITE" -eq 1 ]; then
    printf '{"text":"-y","range":{"byteOffset":{"start":10,"end":12},"start":{"line":0,"column":10},"end":{"line":0,"column":12}},"file":"%s","lines":"const x = -y;","replacement":"--y","replacementOffsets":{"start":10,"end":12},"language":"TypeScript"}\n' "$f"
  else
    printf '{"text":"-y","range":{"byteOffset":{"start":10,"end":12},"start":{"line":0,"column":10},"end":{"line":0,"column":12}},"file":"%s","lines":"const x = -y;","language":"TypeScript"}\n' "$f"
  fi
done
`)

	ext := sdk.New("test-ast-dash")
	Register(ext)
	h := startHost(t, ext, cwd, []string{"ast_grep", "ast_edit"})

	if _, errMsg := h.callTool("ast_grep", map[string]any{"pat": "-$A", "path": filePath}); errMsg != "" {
		t.Fatalf("ast_grep leading-dash failed: %s", errMsg)
	}
	if _, errMsg := h.callTool("ast_edit", map[string]any{
		"ops":   []any{map[string]any{"pat": "-$A", "out": "--$A"}},
		"paths": []any{filePath},
	}); errMsg != "" {
		t.Fatalf("ast_edit leading-dash failed: %s", errMsg)
	}
	logBytes, _ := os.ReadFile(logPath)
	if !strings.Contains(string(logBytes), "--pattern=-$A") || !strings.Contains(string(logBytes), "--rewrite=--$A") {
		t.Fatalf("expected attached --pattern= and --rewrite= flags in argv, got:\n%s", string(logBytes))
	}
}

// --- Killed / crashed child must fail, never return partial matches ---

func TestKilledChildFailsInsteadOfReturningPartialResults(t *testing.T) {
	cwd := t.TempDir()
	filePath := filepath.Join(cwd, "a.ts")
	writeTestFile(t, filePath, "foo(1);\nfoo(2);\n")

	installStrictFakeAstGrep(t, `
for f in "$@"; do
  printf '{"text":"foo(1)","range":{"byteOffset":{"start":0,"end":6},"start":{"line":0,"column":0},"end":{"line":0,"column":6}},"file":"%s","lines":"foo(1);","language":"TypeScript"}\n' "$f"
done
kill -9 $$
`)

	ext := sdk.New("test-ast-kill")
	Register(ext)
	h := startHost(t, ext, cwd, []string{"ast_grep"})

	_, errMsg := h.callTool("ast_grep", map[string]any{"pat": "foo($X)", "path": filePath})
	if errMsg == "" || !strings.Contains(errMsg, "terminated by signal") {
		t.Fatalf("expected signal termination error for killed child, got %q", errMsg)
	}
}

// --- Symlinks skipped during directory walk (never rewritten outside workspace) ---

func TestWalkSkipsSymlinksToOutsideFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on Windows")
	}
	cwd := t.TempDir()
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "outside.ts")
	insideFile := filepath.Join(cwd, "src", "inside.ts")
	linkFile := filepath.Join(cwd, "src", "link.ts")

	writeTestFile(t, outsideFile, "legacyWrap(1, 2)\n")
	writeTestFile(t, insideFile, "legacyWrap(1, 2)\n")
	if err := os.Symlink(outsideFile, linkFile); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	installStrictFakeAstGrep(t, `
for f in "$@"; do
  printf '{"text":"legacyWrap(1, 2)","range":{"byteOffset":{"start":0,"end":16},"start":{"line":0,"column":0},"end":{"line":0,"column":16}},"file":"%s","lines":"legacyWrap(1, 2)","replacement":"modernWrap(1, 2)","replacementOffsets":{"start":0,"end":16},"language":"TypeScript"}\n' "$f"
done
`)

	ext := sdk.New("test-ast-symlink")
	Register(ext)
	h := startHost(t, ext, cwd, []string{"ast_edit", "resolve"})

	preview, errMsg := h.callTool("ast_edit", map[string]any{
		"ops":   []any{map[string]any{"pat": "legacyWrap($A, $B)", "out": "modernWrap($A, $B)"}},
		"paths": []any{"src"},
	})
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if ft, _ := preview.Details["filesTouched"].(float64); int(ft) != 1 {
		t.Fatalf("expected only inside.ts touched (symlink skipped), got filesTouched=%v files=%v", preview.Details["filesTouched"], preview.Details["files"])
	}
	applyRes, errMsg := h.callTool("resolve", map[string]any{"reason": "apply safe rewrite"})
	if errMsg != "" || applyRes.IsError {
		t.Fatalf("resolve failed: err=%q res=%+v", errMsg, applyRes)
	}
	if b, _ := os.ReadFile(outsideFile); string(b) != "legacyWrap(1, 2)\n" {
		t.Fatalf("symlink target outside workspace was modified: %q", string(b))
	}
	if b, _ := os.ReadFile(insideFile); string(b) != "modernWrap(1, 2)\n" {
		t.Fatalf("inside file was not rewritten: %q", string(b))
	}
}

// --- Parent .gitignore, .git/info/exclude, and global excludes ---

func TestWalkHonorsParentGitignoreExcludeAndGlobalExcludes(t *testing.T) {
	repo := t.TempDir()
	// Mark as git repo with .git/info/exclude.
	writeTestFile(t, filepath.Join(repo, ".git", "info", "exclude"), "*.excluded.ts\n")
	writeTestFile(t, filepath.Join(repo, ".gitignore"), "gen/\n*.gen.ts\n")
	writeTestFile(t, filepath.Join(repo, "src", "keep.ts"), "foo(1);\n")
	writeTestFile(t, filepath.Join(repo, "src", "c.gen.ts"), "foo(2);\n")
	writeTestFile(t, filepath.Join(repo, "src", "gen", "b.ts"), "foo(3);\n")
	writeTestFile(t, filepath.Join(repo, "src", "d.excluded.ts"), "foo(4);\n")
	writeTestFile(t, filepath.Join(repo, "src", "e.global.ts"), "foo(5);\n")

	globalCfgDir := t.TempDir()
	globalIgnore := filepath.Join(globalCfgDir, "global.ignore")
	globalGitCfg := filepath.Join(globalCfgDir, "gitconfig")
	writeTestFile(t, globalIgnore, "*.global.ts\n")
	writeTestFile(t, globalGitCfg, fmt.Sprintf("[core]\n\texcludesFile = %q\n", globalIgnore))
	t.Setenv("GIT_CONFIG_GLOBAL", globalGitCfg)

	scope, err := resolveToolScope(sdk.Context{}, repo, []string{"src"}, "search", false)
	if err != nil {
		t.Fatalf("resolveToolScope: %v", err)
	}
	cands, err := discoverCandidates(context.Background(), repo, scope, "")
	if err != nil {
		t.Fatalf("discoverCandidates: %v", err)
	}
	if len(cands) != 1 || cands[0].displayPath != "src/keep.ts" {
		t.Fatalf("expected only src/keep.ts after parent .gitignore/.git/info/exclude/global excludes, got %+v", cands)
	}
}

// --- Comma & whitespace path-list recovery ---

func TestDelimitedPathListRecovery(t *testing.T) {
	cwd := t.TempDir()
	writeTestFile(t, filepath.Join(cwd, "src", "a.ts"), "foo(1);\n")
	writeTestFile(t, filepath.Join(cwd, "tests", "b.ts"), "foo(2);\n")

	scope, err := resolveToolScope(sdk.Context{}, cwd, []string{"src, tests"}, "search", false)
	if err != nil {
		t.Fatalf("comma split failed: %v", err)
	}
	if len(scope.targets) != 2 {
		t.Fatalf("expected 2 targets from comma split, got %d", len(scope.targets))
	}

	scopeWS, err := resolveToolScope(sdk.Context{}, cwd, []string{"src tests"}, "search", false)
	if err != nil {
		t.Fatalf("whitespace split failed: %v", err)
	}
	if len(scopeWS.targets) != 2 {
		t.Fatalf("expected 2 targets from whitespace split, got %d", len(scopeWS.targets))
	}
}

// --- Streaming large JSON lines (> 4 MiB) + bounded matchWindow ---

func TestStreamJSONMatchesHandlesOver4MiBLineAndWindowBounds(t *testing.T) {
	bigText := strings.Repeat("x", 5*1024*1024)
	payload, err := json.Marshal(sgMatchJSON{
		Text:     bigText,
		File:     "big.ts",
		Language: "TypeScript",
		Range: sgRange{
			ByteOffset: sgByteOffset{Start: 0, End: len(bigText)},
			Start:      sgPosition{Line: 0, Column: 0},
			End:        sgPosition{Line: 0, Column: len(bigText)},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var count int
	if err := streamJSONMatches(bytes.NewReader(append(payload, '\n')), func(m sgMatchJSON) error {
		if len(m.Text) != len(bigText) {
			t.Fatalf("unexpected decoded length: %d", len(m.Text))
		}
		count++
		return nil
	}); err != nil {
		t.Fatalf("streamJSONMatches failed on >4MiB line: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 match, got %d", count)
	}

	// Verify matchWindow keeps only skip+limit+1 matches while tracking full totals.
	w := newMatchWindow(2, 3)
	for i := 20; i >= 1; i-- {
		w.add(astFindMatch{Path: "a.ts", StartLine: i, StartColumn: 1})
	}
	if len(w.matches) != 6 {
		t.Fatalf("expected window capacity 6, got %d", len(w.matches))
	}
	if w.total != 20 {
		t.Fatalf("expected total=20, got %d", w.total)
	}
	page, limited := w.page(2, 3)
	if !limited || len(page) != 3 || page[0].StartLine != 3 || page[2].StartLine != 5 {
		t.Fatalf("unexpected page: limited=%v page=%+v", limited, page)
	}
}

// --- Matched-text and UTF-8 boundary verification on byte offsets ---

func TestApplyEditsToSourceRejectsTextMismatchAndBadUTF8Boundary(t *testing.T) {
	// Text mismatch (e.g. concurrent modification between child read and Go read) must fail.
	_, _, err := applyEditsToSource("otherWrap(1)\n", []astReplaceChange{
		{ByteStart: 0, ByteEnd: 10, Before: "legacyWrap", After: "modernWrap"},
	}, "a.ts")
	if err == nil {
		t.Fatalf("expected mismatch error when source[start:end] != Before")
	}

	// Multi-byte UTF-8 boundary split must fail.
	src := "const s = \"é\";\n" // 'é' is 2 bytes at index 11..13
	_, _, err = applyEditsToSource(src, []astReplaceChange{
		{ByteStart: 12, ByteEnd: 13, Before: "", After: "e"},
	}, "utf8.ts")
	if err == nil {
		t.Fatalf("expected UTF-8 boundary error")
	}

	// Valid multi-byte UTF-8 rewrite succeeds.
	out, _, err := applyEditsToSource("const greeting = \"héllo\"; foo(1);\n", []astReplaceChange{
		{ByteStart: 27, ByteEnd: 33, Before: "foo(1)", After: "bar(1)"},
	}, "utf8.ts")
	if err != nil || out != "const greeting = \"héllo\"; bar(1);\n" {
		t.Fatalf("valid UTF-8 edit failed: out=%q err=%v", out, err)
	}
}

// --- Language inference parity (crates/ast/src/language/mod.rs:720-781) ---

func TestInferLangFromPathParity(t *testing.T) {
	cases := []struct {
		path string
		want string
		ok   bool
	}{
		{".env", "", false},             // dotfile with no extension must NOT infer bash
		{"config.env", "bash", true},    // extension `.env` DOES infer bash
		{"kernel.C", "cpp", true},       // uppercase .C -> cpp
		{"header.H", "cpp", true},       // uppercase .H -> cpp
		{"header.h", "c", true},         // lowercase .h -> c
		{".zsh_aliases", "bash", true},  // rc files
		{".bash_aliases", "bash", true}, // rc files
		{"kshrc", "bash", true},
		{".mkshrc", "bash", true},
		{".shrc", "bash", true},
		{"Makefile", "make", true},
		{"Dockerfile.dev", "dockerfile", true},
	}
	for _, tc := range cases {
		got, ok := inferLangFromPath(tc.path)
		if ok != tc.ok || got != tc.want {
			t.Errorf("inferLangFromPath(%q) = (%q, %v), want (%q, %v)", tc.path, got, ok, tc.want, tc.ok)
		}
	}
}

// --- Ported upstream ast-grep.test.ts cases ---

// upstream: ast-grep.test.ts: "combines globbing from path and glob parameters"
func TestAstGrepCombinesGlobbingAndFormatsTree(t *testing.T) {
	cwd := t.TempDir()
	sourceDir := filepath.Join(cwd, "packages", "pkg-123", "src")
	nestedDir := filepath.Join(sourceDir, "nested")
	rootFile := filepath.Join(sourceDir, "root.ts")
	childFile := filepath.Join(nestedDir, "child.ts")
	writeTestFile(t, rootFile, "const providerOptions = {};\n")
	writeTestFile(t, childFile, "const providerOptions = { nested: true };\n")
	writeTestFile(t, filepath.Join(sourceDir, "ignore.js"), "const providerOptions = {};\n")
	writeTestFile(t, filepath.Join(cwd, "outside.ts"), "const providerOptions = {};\n")

	installStrictFakeAstGrep(t, `
for f in "$@"; do
  printf '{"text":"providerOptions","range":{"byteOffset":{"start":6,"end":21},"start":{"line":0,"column":6},"end":{"line":0,"column":21}},"file":"%s","lines":"const providerOptions = {};","language":"TypeScript"}\n' "$f"
done
`)

	ext := sdk.New("test-ast-glob")
	Register(ext)
	h := startHost(t, ext, cwd, []string{"ast_grep"})

	res, errMsg := h.callTool("ast_grep", map[string]any{
		"pat":  "providerOptions",
		"path": filepath.Join(cwd, "packages") + "/pkg-*/src/**/*.ts",
	})
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if !regexp.MustCompile(`(?m)^## root\.ts#[0-9A-F]{4}$`).MatchString(res.Content) {
		t.Fatalf("expected ## root.ts#<HASH> in:\n%s", res.Content)
	}
	if !regexp.MustCompile(`(?m)^### child\.ts#[0-9A-F]{4}$`).MatchString(res.Content) {
		t.Fatalf("expected ### child.ts#<HASH> in:\n%s", res.Content)
	}
	if strings.Contains(res.Content, "ignore.js") || strings.Contains(res.Content, "outside.ts") {
		t.Fatalf("unexpected excluded files in:\n%s", res.Content)
	}
	if mc, _ := res.Details["matchCount"].(float64); int(mc) != 2 {
		t.Fatalf("expected matchCount=2, got %v", res.Details["matchCount"])
	}
	if fc, _ := res.Details["fileCount"].(float64); int(fc) != 2 {
		t.Fatalf("expected fileCount=2, got %v", res.Details["fileCount"])
	}
}

// upstream: ast-grep.test.ts: "keeps multi-target results globally ordered and skip does not truncate match totals"
func TestAstGrepMultiTargetOrderingAndSkip(t *testing.T) {
	cwd := t.TempDir()
	earlyDir := filepath.Join(cwd, "a")
	lateDir := filepath.Join(cwd, "z")
	lines := strings.Repeat("const sharedSymbol = 1;\n", 8)
	writeTestFile(t, filepath.Join(earlyDir, "a.ts"), lines)
	writeTestFile(t, filepath.Join(lateDir, "z.ts"), lines)

	installStrictFakeAstGrep(t, `
for f in "$@"; do
  i=0
  while [ $i -lt 8 ]; do
    start=$((i * 24 + 6))
    end=$((start + 12))
    printf '{"text":"sharedSymbol","range":{"byteOffset":{"start":%d,"end":%d},"start":{"line":%d,"column":6},"end":{"line":%d,"column":18}},"file":"%s","lines":"const sharedSymbol = 1;","language":"TypeScript"}\n' "$start" "$end" "$i" "$i" "$f"
    i=$((i + 1))
  done
done
`)

	ext := sdk.New("test-ast-multi-skip")
	Register(ext)
	h := startHost(t, ext, cwd, []string{"ast_grep"})

	res, errMsg := h.callTool("ast_grep", map[string]any{
		"pat":  "sharedSymbol",
		"path": earlyDir + ";" + lateDir,
		"skip": float64(8),
	})
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if mc, _ := res.Details["matchCount"].(float64); int(mc) != 16 {
		t.Fatalf("expected untruncated matchCount=16, got %v", res.Details["matchCount"])
	}
	if lr, _ := res.Details["limitReached"].(bool); lr {
		t.Fatalf("expected limitReached=false")
	}
	if !strings.Contains(res.Content, "z.ts") || strings.Contains(res.Content, "a.ts") {
		t.Fatalf("expected only z.ts after skip=8, got:\n%s", res.Content)
	}
}

// upstream: ast-grep.test.ts: "names explicit host and local:// file targets alongside directory targets"
func TestAstGrepHostAndLocalURLTargets(t *testing.T) {
	cwd := t.TempDir()
	localRoot := t.TempDir()
	urls.ResetForTests()
	t.Cleanup(urls.ResetForTests)
	if err := urls.Register(fakeLocalURLHandler{root: localRoot}); err != nil {
		t.Fatalf("register local://: %v", err)
	}

	examplePath := filepath.Join(cwd, "a", "example.ts")
	otherPath := filepath.Join(cwd, "b", "other.ts")
	childPath := filepath.Join(cwd, "c", "nested", "child.ts")
	writeTestFile(t, examplePath, "legacyWrap(value, other)\n")
	writeTestFile(t, otherPath, "legacyWrap(value, other)\n")
	writeTestFile(t, childPath, "legacyWrap(nested, other)\n")
	writeTestFile(t, filepath.Join(localRoot, "scratch", "note.ts"), "legacyWrap(note, other)\n")
	writeTestFile(t, filepath.Join(localRoot, "dir", "nested.ts"), "legacyWrap(dirChild, other)\n")

	installStrictFakeAstGrep(t, `
for f in "$@"; do
  printf '{"text":"legacyWrap(a, b)","range":{"byteOffset":{"start":0,"end":16},"start":{"line":0,"column":0},"end":{"line":0,"column":16}},"file":"%s","lines":"legacyWrap(a, b)","language":"TypeScript"}\n' "$f"
done
`)

	ext := sdk.New("test-ast-urls")
	Register(ext)
	h := startHost(t, ext, cwd, []string{"ast_grep"})

	res, errMsg := h.callTool("ast_grep", map[string]any{
		"pat":  "legacyWrap($A, $B)",
		"path": strings.Join([]string{examplePath, otherPath, "local://scratch/note.ts", filepath.Join(cwd, "c"), "local://dir"}, ";"),
	})
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if mc, _ := res.Details["matchCount"].(float64); int(mc) != 5 {
		t.Fatalf("expected matchCount=5, got %v", res.Details["matchCount"])
	}
	rawFiles, _ := res.Details["files"].([]any)
	gotFiles := make([]string, len(rawFiles))
	for i, f := range rawFiles {
		gotFiles[i], _ = f.(string)
	}
	sort.Strings(gotFiles)
	want := []string{"a/example.ts", "b/other.ts", "c/nested/child.ts", "local://dir/nested.ts", "local://scratch/note.ts"}
	if !slices.Equal(gotFiles, want) {
		t.Fatalf("files mismatch:\ngot:  %v\nwant: %v", gotFiles, want)
	}
}

// upstream: ast-grep.test.ts: "keeps a multi-line capture on one display row while the model reads it as written"
func TestAstGrepMultilineCaptureDisplayVsModel(t *testing.T) {
	cwd := t.TempDir()
	filePath := filepath.Join(cwd, "calls.ts")
	writeTestFile(t, filePath, "foo({\n  a: 1,\n\n  b: 2,\n});\nfoo(x);\n")

	installStrictFakeAstGrep(t, `
for f in "$@"; do
  cat <<EOF
{"text":"foo({\n  a: 1,\n\n  b: 2,\n})","range":{"byteOffset":{"start":0,"end":25},"start":{"line":0,"column":0},"end":{"line":4,"column":2}},"file":"$f","lines":"foo({","language":"TypeScript","metaVariables":{"single":{"A":{"text":"{\n  a: 1,\n\n  b: 2,\n}","range":{"byteOffset":{"start":4,"end":24},"start":{"line":0,"column":4},"end":{"line":4,"column":1}}}},"multi":{}}}
{"text":"foo(x)","range":{"byteOffset":{"start":27,"end":33},"start":{"line":5,"column":0},"end":{"line":5,"column":6}},"file":"$f","lines":"foo(x);","language":"TypeScript","metaVariables":{"single":{"A":{"text":"x","range":{"byteOffset":{"start":31,"end":32},"start":{"line":5,"column":4},"end":{"line":5,"column":5}}}},"multi":{}}}
EOF
done
`)

	ext := sdk.New("test-ast-capture")
	Register(ext)
	h := startHost(t, ext, cwd, []string{"ast_grep"})

	res, errMsg := h.callTool("ast_grep", map[string]any{
		"pat":  "foo($A)",
		"path": filePath,
	})
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	display, _ := res.Details["displayContent"].(string)
	var metaDisplay []string
	for _, line := range strings.Split(display, "\n") {
		if strings.HasPrefix(line, "  meta: ") {
			metaDisplay = append(metaDisplay, line)
		} else if !regexp.MustCompile(`^[* ]\d+│`).MatchString(line) {
			t.Fatalf("unexpected display line: %q", line)
		}
	}
	wantMeta := []string{
		"  meta: A={   a: 1,   b: 2, }",
		"  meta: A=x",
	}
	if !slices.Equal(metaDisplay, wantMeta) {
		t.Fatalf("display meta mismatch:\ngot:  %q\nwant: %q", metaDisplay, wantMeta)
	}
	if !strings.Contains(res.Content, "  meta: A={\n  a: 1,\n\n  b: 2,\n}") {
		t.Fatalf("expected raw multi-line capture in model text:\n%s", res.Content)
	}
}

func TestParseErrorNormalizationAndCap(t *testing.T) {
	// Tests capParseErrors and formatParseErrors (used when CLI stderr reports diagnostics).
	raw := make([]string, 21)
	for i := range 21 {
		raw[i] = normalizeParseError(fmt.Sprintf("pat($A): broken-%d.ts: parse error (syntax tree contains error nodes)", i))
	}
	if strings.Contains(raw[0], "pat($A):") {
		t.Fatalf("expected pattern prefix stripped, got %q", raw[0])
	}
	capped, total := capParseErrors(raw)
	if len(capped) != 20 || total != 21 {
		t.Fatalf("expected capped=20 total=21, got %d/%d", len(capped), total)
	}
	formatted := formatParseErrors(capped, total)
	if formatted[0] != "Parse issues (20 / 21):" {
		t.Fatalf("unexpected header: %q", formatted[0])
	}
}

// --- Ported upstream ast-edit.test.ts and resolve.test.ts cases ---

// upstream: ast-edit.test.ts: "renders +/- lines with numbered hashline prefixes"
// upstream: ast-edit.test.ts: "registers a pending action that apply writes changes"
func TestAstEditPreviewHashlinesAndResolveApply(t *testing.T) {
	cwd := t.TempDir()
	filePath := filepath.Join(cwd, "legacy.ts")
	writeTestFile(t, filePath, "legacyWrap(x, value)\n")

	installStrictFakeAstGrep(t, `
for f in "$@"; do
  if grep -q 'legacyWrap(x, value)' "$f"; then
    printf '{"text":"legacyWrap(x, value)","range":{"byteOffset":{"start":0,"end":20},"start":{"line":0,"column":0},"end":{"line":0,"column":20}},"file":"%s","lines":"legacyWrap(x, value)","replacement":"modernWrap(x, value)","replacementOffsets":{"start":0,"end":20},"language":"TypeScript"}\n' "$f"
  fi
done
`)

	ext := sdk.New("test-ast-edit-apply")
	Register(ext)
	h := startHost(t, ext, cwd, []string{"ast_edit", "resolve", "reject"})

	preview, errMsg := h.callTool("ast_edit", map[string]any{
		"ops":   []any{map[string]any{"pat": "legacyWrap($A, $B)", "out": "modernWrap($A, $B)"}},
		"paths": []any{filePath},
	})
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if applied, _ := preview.Details["applied"].(bool); applied {
		t.Fatalf("expected preview applied=false")
	}
	if !strings.HasPrefix(preview.Content, previewPendingNotice) {
		t.Fatalf("expected PREVIEW_PENDING_NOTICE prefix, got:\n%s", preview.Content)
	}

	var removedLine, addedLine string
	for _, line := range strings.Split(preview.Content, "\n") {
		if strings.HasPrefix(line, "-") && removedLine == "" {
			removedLine = line
		}
		if strings.HasPrefix(line, "+") && addedLine == "" {
			addedLine = line
		}
	}
	if !regexp.MustCompile(`^-\d+:`).MatchString(removedLine) || !regexp.MustCompile(`^\+\d+:`).MatchString(addedLine) {
		t.Fatalf("expected -N: and +N: prefixes, got %q / %q", removedLine, addedLine)
	}

	// File must remain unmodified until resolve is invoked.
	if b, _ := os.ReadFile(filePath); string(b) != "legacyWrap(x, value)\n" {
		t.Fatalf("preview modified file on disk: %q", string(b))
	}

	applyRes, errMsg := h.callTool("resolve", map[string]any{"reason": "apply previewed AST edit"})
	if errMsg != "" || applyRes.IsError {
		t.Fatalf("resolve failed: err=%q res=%+v", errMsg, applyRes)
	}
	if !strings.Contains(applyRes.Content, "Applied 1 replacement in 1 file.") {
		t.Fatalf("unexpected apply content:\n%s", applyRes.Content)
	}
	expectedTag := hashline.FileHash("modernWrap(x, value)\n")
	if !strings.Contains(applyRes.Content, "[legacy.ts#"+expectedTag+"]") {
		t.Fatalf("expected fresh tag %s in:\n%s", expectedTag, applyRes.Content)
	}
	if b, _ := os.ReadFile(filePath); string(b) != "modernWrap(x, value)\n" {
		t.Fatalf("expected rewritten file, got %q", string(b))
	}
}

// upstream: ast-edit.test.ts: "reports and applies explicit file targets under their own paths"
func TestAstEditExplicitFileTargetsUnderOwnPaths(t *testing.T) {
	cwd := t.TempDir()
	first := filepath.Join(cwd, "server", "public", "session.ts")
	second := filepath.Join(cwd, "client.ts")
	writeTestFile(t, first, "legacyWrap(a, b)\n")
	writeTestFile(t, second, "legacyWrap(c, d)\n")

	installStrictFakeAstGrep(t, `
for f in "$@"; do
  line=$(head -n 1 "$f")
  len=${#line}
  repl=$(printf '%s' "$line" | sed 's/legacyWrap/modernWrap/')
  printf '{"text":"%s","range":{"byteOffset":{"start":0,"end":%d},"start":{"line":0,"column":0},"end":{"line":0,"column":%d}},"file":"%s","lines":"%s","replacement":"%s","replacementOffsets":{"start":0,"end":%d},"language":"TypeScript"}\n' "$line" "$len" "$len" "$f" "$line" "$repl" "$len"
done
`)

	ext := sdk.New("test-ast-explicit-files")
	Register(ext)
	h := startHost(t, ext, cwd, []string{"ast_edit", "resolve"})

	preview, errMsg := h.callTool("ast_edit", map[string]any{
		"ops":   []any{map[string]any{"pat": "legacyWrap($A, $B)", "out": "modernWrap($A, $B)"}},
		"paths": []any{"server/public/session.ts", "client.ts"},
	})
	if errMsg != "" {
		t.Fatalf("preview failed: %s", errMsg)
	}
	rawFiles, _ := preview.Details["files"].([]any)
	gotFiles := make([]string, len(rawFiles))
	for i, f := range rawFiles {
		gotFiles[i], _ = f.(string)
	}
	sort.Strings(gotFiles)
	if !slices.Equal(gotFiles, []string{"client.ts", "server/public/session.ts"}) {
		t.Fatalf("unexpected files: %v", gotFiles)
	}
	applyRes, errMsg := h.callTool("resolve", map[string]any{"reason": "apply explicit file targets"})
	if errMsg != "" || applyRes.IsError {
		t.Fatalf("apply failed: err=%q res=%+v", errMsg, applyRes)
	}
	if b, _ := os.ReadFile(first); string(b) != "modernWrap(a, b)\n" {
		t.Fatalf("first not rewritten: %q", string(b))
	}
	if b, _ := os.ReadFile(second); string(b) != "modernWrap(c, d)\n" {
		t.Fatalf("second not rewritten: %q", string(b))
	}
}

// upstream: ast-edit.test.ts: "fails stale pending apply when preview no longer matches"
func TestAstEditStalePreviewFailsOnResolve(t *testing.T) {
	cwd := t.TempDir()
	filePath := filepath.Join(cwd, "legacy.ts")
	writeTestFile(t, filePath, "legacyWrap(x, value)\n")

	installStrictFakeAstGrep(t, `
for f in "$@"; do
  if grep -q 'legacyWrap(x, value)' "$f"; then
    printf '{"text":"legacyWrap(x, value)","range":{"byteOffset":{"start":0,"end":20},"start":{"line":0,"column":0},"end":{"line":0,"column":20}},"file":"%s","lines":"legacyWrap(x, value)","replacement":"modernWrap(x, value)","replacementOffsets":{"start":0,"end":20},"language":"TypeScript"}\n' "$f"
  fi
done
`)

	ext := sdk.New("test-ast-stale")
	Register(ext)
	h := startHost(t, ext, cwd, []string{"ast_edit", "resolve", "reject"})

	if _, errMsg := h.callTool("ast_edit", map[string]any{
		"ops":   []any{map[string]any{"pat": "legacyWrap($A, $B)", "out": "modernWrap($A, $B)"}},
		"paths": []any{filePath},
	}); errMsg != "" {
		t.Fatalf("unexpected preview error: %s", errMsg)
	}

	writeTestFile(t, filePath, "otherWrap(x, value)\n")

	applyRes, errMsg := h.callTool("resolve", map[string]any{"reason": "apply stale preview"})
	if errMsg != "" {
		t.Fatalf("unexpected transport error: %s", errMsg)
	}
	if !applyRes.IsError || !strings.Contains(applyRes.Content, "Preview is stale / no longer matches") {
		t.Fatalf("expected stale apply error, got isError=%v content=%q", applyRes.IsError, applyRes.Content)
	}
	if b, _ := os.ReadFile(filePath); string(b) != "otherWrap(x, value)\n" {
		t.Fatalf("mutated content should be preserved, got %q", string(b))
	}
}

// upstream: ast-edit.test.ts: "combines globbing from path and glob parameters"
func TestAstEditGlobbingAndTreeHeaders(t *testing.T) {
	cwd := t.TempDir()
	sourceDir := filepath.Join(cwd, "packages", "pkg-123", "src")
	nestedDir := filepath.Join(sourceDir, "nested")
	rootFile := filepath.Join(sourceDir, "root.ts")
	childFile := filepath.Join(nestedDir, "child.ts")
	ignoreFile := filepath.Join(sourceDir, "ignore.js")
	outsideFile := filepath.Join(cwd, "outside.ts")

	writeTestFile(t, rootFile, "legacyWrap(rootValue, rootArg)\n")
	writeTestFile(t, childFile, "legacyWrap(childValue, childArg)\n")
	writeTestFile(t, ignoreFile, "legacyWrap(ignoreValue, ignoreArg)\n")
	writeTestFile(t, outsideFile, "legacyWrap(outsideValue, outsideArg)\n")

	installStrictFakeAstGrep(t, `
for f in "$@"; do
  line=$(head -n 1 "$f")
  len=${#line}
  repl=$(printf '%s' "$line" | sed 's/legacyWrap/modernWrap/')
  printf '{"text":"%s","range":{"byteOffset":{"start":0,"end":%d},"start":{"line":0,"column":0},"end":{"line":0,"column":%d}},"file":"%s","lines":"%s","replacement":"%s","replacementOffsets":{"start":0,"end":%d},"language":"TypeScript"}\n' "$line" "$len" "$len" "$f" "$line" "$repl" "$len"
done
`)

	ext := sdk.New("test-ast-edit-glob")
	Register(ext)
	h := startHost(t, ext, cwd, []string{"ast_edit", "resolve"})

	preview, errMsg := h.callTool("ast_edit", map[string]any{
		"ops":   []any{map[string]any{"pat": "legacyWrap($A, $B)", "out": "modernWrap($A, $B)"}},
		"paths": []any{filepath.Join(cwd, "packages") + "/pkg-*/src/**/*.ts"},
	})
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if !regexp.MustCompile(`(?m)^## root\.ts#[0-9A-F]{4} \(1 replacement\)$`).MatchString(preview.Content) {
		t.Fatalf("expected ## root.ts#<HASH> (1 replacement) in:\n%s", preview.Content)
	}
	if !regexp.MustCompile(`(?m)^### child\.ts#[0-9A-F]{4} \(1 replacement\)$`).MatchString(preview.Content) {
		t.Fatalf("expected ### child.ts#<HASH> (1 replacement) in:\n%s", preview.Content)
	}
	if strings.Contains(preview.Content, "ignore.js") || strings.Contains(preview.Content, "outside.ts") {
		t.Fatalf("unexpected excluded files in:\n%s", preview.Content)
	}

	applyRes, errMsg := h.callTool("resolve", map[string]any{"reason": "apply combined glob edit"})
	if errMsg != "" || applyRes.IsError {
		t.Fatalf("apply failed: err=%s res=%+v", errMsg, applyRes)
	}
	if b, _ := os.ReadFile(rootFile); !strings.Contains(string(b), "modernWrap(rootValue, rootArg)") {
		t.Fatalf("root.ts not rewritten: %q", string(b))
	}
	if b, _ := os.ReadFile(childFile); !strings.Contains(string(b), "modernWrap(childValue, childArg)") {
		t.Fatalf("child.ts not rewritten: %q", string(b))
	}
	if b, _ := os.ReadFile(ignoreFile); !strings.Contains(string(b), "legacyWrap(ignoreValue, ignoreArg)") {
		t.Fatalf("ignore.js should be untouched: %q", string(b))
	}
}

// upstream: ast-edit.test.ts: "names explicit host and local:// file targets alongside directory targets"
func TestAstEditHostAndLocalURLTargets(t *testing.T) {
	cwd := t.TempDir()
	localRoot := t.TempDir()
	urls.ResetForTests()
	t.Cleanup(urls.ResetForTests)
	if err := urls.Register(fakeLocalURLHandler{root: localRoot}); err != nil {
		t.Fatalf("register local://: %v", err)
	}
	if err := urls.Register(fakeReadOnlyURLHandler{root: localRoot}); err != nil {
		t.Fatalf("register readonly://: %v", err)
	}

	examplePath := filepath.Join(cwd, "a", "example.ts")
	localNotePath := filepath.Join(localRoot, "scratch", "note.ts")
	writeTestFile(t, examplePath, "legacyWrap(value, other)\n")
	writeTestFile(t, localNotePath, "legacyWrap(note, other)\n")

	installStrictFakeAstGrep(t, `
for f in "$@"; do
  line=$(head -n 1 "$f")
  len=${#line}
  repl=$(printf '%s' "$line" | sed 's/legacyWrap/modernWrap/')
  printf '{"text":"%s","range":{"byteOffset":{"start":0,"end":%d},"start":{"line":0,"column":0},"end":{"line":0,"column":%d}},"file":"%s","lines":"%s","replacement":"%s","replacementOffsets":{"start":0,"end":%d},"language":"TypeScript"}\n' "$line" "$len" "$len" "$f" "$line" "$repl" "$len"
done
`)

	ext := sdk.New("test-ast-edit-urls")
	Register(ext)
	h := startHost(t, ext, cwd, []string{"ast_edit", "resolve"})

	if _, roErr := h.callTool("ast_edit", map[string]any{
		"ops":   []any{map[string]any{"pat": "legacyWrap($A, $B)", "out": "modernWrap($A, $B)"}},
		"paths": []any{"readonly://scratch/note.ts"},
	}); !strings.Contains(roErr, "are not editable files") {
		t.Fatalf("expected non-editable URL error, got %q", roErr)
	}

	preview, errMsg := h.callTool("ast_edit", map[string]any{
		"ops":   []any{map[string]any{"pat": "legacyWrap($A, $B)", "out": "modernWrap($A, $B)"}},
		"paths": []any{examplePath, "local://scratch/note.ts"},
	})
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	rawFiles, _ := preview.Details["files"].([]any)
	gotFiles := make([]string, len(rawFiles))
	for i, f := range rawFiles {
		gotFiles[i], _ = f.(string)
	}
	sort.Strings(gotFiles)
	if !slices.Equal(gotFiles, []string{"a/example.ts", "local://scratch/note.ts"}) {
		t.Fatalf("unexpected files: %v", gotFiles)
	}

	applyRes, errMsg := h.callTool("resolve", map[string]any{"reason": "apply local:// edit"})
	if errMsg != "" || applyRes.IsError {
		t.Fatalf("apply failed: err=%s res=%+v", errMsg, applyRes)
	}
	if b, _ := os.ReadFile(localNotePath); string(b) != "modernWrap(note, other)\n" {
		t.Fatalf("local:// backing file not rewritten: %q", string(b))
	}
}

// upstream: resolve.test.ts: "errors and clears stale pending markers when resolve has no invoker"
// upstream: resolve.test.ts: "treats reject with no pending action as a successful cancellation and clears stale markers"
// upstream: resolve.test.ts: "rejects through the pending invoker"
// upstream: resolve.test.ts: "applies through the pending invoker"
func TestResolveRejectSpecialCommandsAndProposeHint(t *testing.T) {
	gate.ResetForTests()
	t.Cleanup(gate.ResetForTests)

	cwd := t.TempDir()
	filePath := filepath.Join(cwd, "legacy.ts")
	writeTestFile(t, filePath, "legacyWrap(x, value)\n")

	installStrictFakeAstGrep(t, `
for f in "$@"; do
  printf '{"text":"legacyWrap(x, value)","range":{"byteOffset":{"start":0,"end":20},"start":{"line":0,"column":0},"end":{"line":0,"column":20}},"file":"%s","lines":"legacyWrap(x, value)","replacement":"modernWrap(x, value)","replacementOffsets":{"start":0,"end":20},"language":"TypeScript"}\n' "$f"
done
`)

	ext := sdk.New("test-ast-resolve")
	Register(ext)
	h := startHost(t, ext, cwd, []string{"ast_edit", "resolve", "reject"})

	// Nothing pending + plan active appends propose hint.
	gate.SetPlanActive(func(_ sdk.Context) bool { return true })
	noop, errMsg := h.callTool("reject", map[string]any{"reason": "Abandoning the staged edit."})
	if errMsg != "" || !strings.Contains(noop.Content, "Nothing to reject") || !strings.Contains(noop.Content, "use `propose`") {
		t.Fatalf("expected Nothing to reject with propose hint, err=%q content=%q", errMsg, noop.Content)
	}
	_, applyErr := h.callTool("resolve", map[string]any{"reason": "looks correct"})
	if !strings.Contains(applyErr, "No pending action to apply") || !strings.Contains(applyErr, "use `propose`") {
		t.Fatalf("expected no-pending error with propose hint, got %q", applyErr)
	}
	gate.ResetForTests()

	// Stage and discard.
	if _, errMsg = h.callTool("ast_edit", map[string]any{
		"ops":   []any{map[string]any{"pat": "legacyWrap($A, $B)", "out": "modernWrap($A, $B)"}},
		"paths": []any{filePath},
	}); errMsg != "" {
		t.Fatalf("preview failed: %s", errMsg)
	}
	disc, errMsg := h.callTool("reject", map[string]any{"reason": "wrong scope"})
	if errMsg != "" || !strings.Contains(disc.Content, "Discarded: AST Edit: 1 replacement in 1 file. Reason: wrong scope") {
		t.Fatalf("unexpected discard result: err=%q content=%q", errMsg, disc.Content)
	}
	if b, _ := os.ReadFile(filePath); string(b) != "legacyWrap(x, value)\n" {
		t.Fatalf("file should be unchanged after reject: %q", string(b))
	}
}

// --- internal/gate integration: plan mode blocks resolve before any file is written ---

func TestResolveChecksGateBeforeWritingAnyFile(t *testing.T) {
	gate.ResetForTests()
	t.Cleanup(gate.ResetForTests)

	cwd := t.TempDir()
	first := filepath.Join(cwd, "a.ts")
	second := filepath.Join(cwd, "b.ts")
	writeTestFile(t, first, "legacyWrap(1, 2)\n")
	writeTestFile(t, second, "legacyWrap(3, 4)\n")

	installStrictFakeAstGrep(t, `
for f in "$@"; do
  line=$(head -n 1 "$f")
  len=${#line}
  repl=$(printf '%s' "$line" | sed 's/legacyWrap/modernWrap/')
  printf '{"text":"%s","range":{"byteOffset":{"start":0,"end":%d},"start":{"line":0,"column":0},"end":{"line":0,"column":%d}},"file":"%s","lines":"%s","replacement":"%s","replacementOffsets":{"start":0,"end":%d},"language":"TypeScript"}\n' "$line" "$len" "$len" "$f" "$line" "$repl" "$len"
done
`)

	// Gate denies ONLY the second file: neither file may be written!
	var checked []gate.Request
	gate.SetPolicy(func(_ sdk.Context, r gate.Request) error {
		checked = append(checked, r)
		if filepath.Base(r.Path) == "b.ts" {
			return fmt.Errorf("plan mode: cannot edit %s", r.Path)
		}
		return nil
	})

	ext := sdk.New("test-ast-gate")
	Register(ext)
	h := startHost(t, ext, cwd, []string{"ast_edit", "resolve"})

	// Preview itself does not mutate and must succeed even under a denying gate.
	if _, errMsg := h.callTool("ast_edit", map[string]any{
		"ops":   []any{map[string]any{"pat": "legacyWrap($A, $B)", "out": "modernWrap($A, $B)"}},
		"paths": []any{first, second},
	}); errMsg != "" {
		t.Fatalf("preview should not be blocked by write gate: %s", errMsg)
	}
	if len(checked) != 0 {
		t.Fatalf("preview should not invoke gate.Check, got %d checks", len(checked))
	}

	// Resolve must fail via gate and leave BOTH files untouched (and preview still pending).
	_, errMsg := h.callTool("resolve", map[string]any{"reason": "try to apply in plan mode"})
	if !strings.Contains(errMsg, "plan mode: cannot edit") {
		t.Fatalf("expected gate denial error, got %q", errMsg)
	}
	if len(checked) != 2 {
		t.Fatalf("expected gate checked for both files, got %d", len(checked))
	}
	for _, r := range checked {
		if r.Tool != "ast_edit" || r.Op != gate.OpEdit {
			t.Fatalf("unexpected gate request: %+v", r)
		}
	}
	if b, _ := os.ReadFile(first); string(b) != "legacyWrap(1, 2)\n" {
		t.Fatalf("first file was mutated despite gate denying second file: %q", string(b))
	}
	if b, _ := os.ReadFile(second); string(b) != "legacyWrap(3, 4)\n" {
		t.Fatalf("second file was mutated despite gate denial: %q", string(b))
	}
}

// --- Multi-session isolation, lifecycle.End, and repeated Register ---

func TestMultiSessionIsolationLifecycleEndAndRegisterTwice(t *testing.T) {
	cwd := t.TempDir()
	fileA := filepath.Join(cwd, "a.ts")
	fileB := filepath.Join(cwd, "b.ts")
	writeTestFile(t, fileA, "legacyWrap(1, 2)\n")
	writeTestFile(t, fileB, "legacyWrap(3, 4)\n")

	installStrictFakeAstGrep(t, `
for f in "$@"; do
  line=$(head -n 1 "$f")
  len=${#line}
  repl=$(printf '%s' "$line" | sed 's/legacyWrap/modernWrap/')
  printf '{"text":"%s","range":{"byteOffset":{"start":0,"end":%d},"start":{"line":0,"column":0},"end":{"line":0,"column":%d}},"file":"%s","lines":"%s","replacement":"%s","replacementOffsets":{"start":0,"end":%d},"language":"TypeScript"}\n' "$line" "$len" "$len" "$f" "$line" "$repl" "$len"
done
`)

	// Register twice (simulating PiG creating a fresh extension instance per session).
	ext1 := sdk.New("test-ast-sess1")
	Register(ext1)
	h1 := startHostWithSession(t, ext1, cwd, "sess-1", []string{"ast_edit", "resolve", "reject"})

	ext2 := sdk.New("test-ast-sess2")
	Register(ext2)
	h2 := startHostWithSession(t, ext2, cwd, "sess-2", []string{"ast_edit", "resolve", "reject"})

	// Both sessions stage their own previews.
	if _, errMsg := h1.callTool("ast_edit", map[string]any{
		"ops":   []any{map[string]any{"pat": "legacyWrap($A, $B)", "out": "modernWrap($A, $B)"}},
		"paths": []any{fileA},
	}); errMsg != "" {
		t.Fatalf("sess-1 preview failed: %s", errMsg)
	}
	if _, errMsg := h2.callTool("ast_edit", map[string]any{
		"ops":   []any{map[string]any{"pat": "legacyWrap($A, $B)", "out": "modernWrap($A, $B)"}},
		"paths": []any{fileB},
	}); errMsg != "" {
		t.Fatalf("sess-2 preview failed: %s", errMsg)
	}

	// Ending sess-1 (twice, and also an unknown id) must clear ONLY sess-1's state,
	// even though Register was called a second time for ext2!
	lifecycle.End("sess-1")
	lifecycle.End("sess-1")
	lifecycle.End("unknown-session-id")

	// sess-1's staged preview is gone.
	if _, errMsg := h1.callTool("resolve", map[string]any{"reason": "should be gone"}); !strings.Contains(errMsg, "No pending action") {
		t.Fatalf("expected sess-1 preview cleared after lifecycle.End, got %q", errMsg)
	}

	// sess-2's staged preview is intact and still applies cleanly!
	res2, errMsg := h2.callTool("resolve", map[string]any{"reason": "apply sess-2 after sess-1 ended"})
	if errMsg != "" || res2.IsError || !strings.Contains(res2.Content, "Applied 1 replacement in 1 file.") {
		t.Fatalf("sess-2 apply failed after sess-1 ended: err=%q res=%+v", errMsg, res2)
	}
	if b, _ := os.ReadFile(fileB); string(b) != "modernWrap(3, 4)\n" {
		t.Fatalf("fileB not rewritten by sess-2: %q", string(b))
	}
	if b, _ := os.ReadFile(fileA); string(b) != "legacyWrap(1, 2)\n" {
		t.Fatalf("fileA should remain untouched: %q", string(b))
	}
}

func TestAstEditOverlappingReplacementsRejected(t *testing.T) {
	cwd := t.TempDir()
	filePath := filepath.Join(cwd, "overlap.ts")
	writeTestFile(t, filePath, "foo(foo(1))\n")

	installStrictFakeAstGrep(t, `
for f in "$@"; do
  printf '{"text":"foo(foo(1))","range":{"byteOffset":{"start":0,"end":11},"start":{"line":0,"column":0},"end":{"line":0,"column":11}},"file":"%s","lines":"foo(foo(1))","replacement":"bar(foo(1))","replacementOffsets":{"start":0,"end":11},"language":"TypeScript"}\n' "$f"
  printf '{"text":"foo(1)","range":{"byteOffset":{"start":4,"end":10},"start":{"line":0,"column":4},"end":{"line":0,"column":10}},"file":"%s","lines":"foo(foo(1))","replacement":"bar(1)","replacementOffsets":{"start":4,"end":10},"language":"TypeScript"}\n' "$f"
done
`)

	ext := sdk.New("test-ast-overlap")
	Register(ext)
	h := startHost(t, ext, cwd, []string{"ast_edit"})

	_, errMsg := h.callTool("ast_edit", map[string]any{
		"ops":   []any{map[string]any{"pat": "foo($X)", "out": "bar($X)"}},
		"paths": []any{filePath},
	})
	if !strings.Contains(errMsg, "overlapping replacements detected") {
		t.Fatalf("expected overlap error, got %q", errMsg)
	}
}

func TestAstEditMaxFilesLimit(t *testing.T) {
	cwd := t.TempDir()
	writeTestFile(t, filepath.Join(cwd, "a.ts"), "old(1)\n")
	writeTestFile(t, filepath.Join(cwd, "b.ts"), "old(2)\n")
	t.Setenv("PI_MAX_AST_FILES", "1")

	installStrictFakeAstGrep(t, `
for f in "$@"; do
  line=$(head -n 1 "$f")
  len=${#line}
  repl=$(printf '%s' "$line" | sed 's/old/new/')
  printf '{"text":"%s","range":{"byteOffset":{"start":0,"end":%d},"start":{"line":0,"column":0},"end":{"line":0,"column":%d}},"file":"%s","lines":"%s","replacement":"%s","replacementOffsets":{"start":0,"end":%d},"language":"TypeScript"}\n' "$line" "$len" "$len" "$f" "$line" "$repl" "$len"
done
`)

	ext := sdk.New("test-ast-maxfiles")
	Register(ext)
	h := startHost(t, ext, cwd, []string{"ast_edit"})

	res, errMsg := h.callTool("ast_edit", map[string]any{
		"ops":   []any{map[string]any{"pat": "old($X)", "out": "new($X)"}},
		"paths": []any{cwd},
	})
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if lr, _ := res.Details["limitReached"].(bool); !lr {
		t.Fatalf("expected limitReached=true")
	}
	if ft, _ := res.Details["filesTouched"].(float64); int(ft) != 1 {
		t.Fatalf("expected filesTouched=1, got %v", res.Details["filesTouched"])
	}
	if !strings.Contains(res.Content, "Limit reached; narrow paths.") {
		t.Fatalf("expected limit notice in:\n%s", res.Content)
	}
}

func TestAstEditValidationErrors(t *testing.T) {
	cwd := t.TempDir()
	filePath := filepath.Join(cwd, "a.ts")
	writeTestFile(t, filePath, "x()\n")

	ext := sdk.New("test-ast-validate")
	Register(ext)
	h := startHost(t, ext, cwd, []string{"ast_grep", "ast_edit"})

	if _, errMsg := h.callTool("ast_grep", map[string]any{"pat": "   "}); !strings.Contains(errMsg, "non-empty pattern") {
		t.Fatalf("expected empty pat error, got %q", errMsg)
	}
	if _, errMsg := h.callTool("ast_grep", map[string]any{"pat": "x", "skip": float64(-1)}); !strings.Contains(errMsg, "non-negative") {
		t.Fatalf("expected negative skip error, got %q", errMsg)
	}
	if _, errMsg := h.callTool("ast_edit", map[string]any{
		"ops":   []any{map[string]any{"pat": "", "out": "b"}},
		"paths": []any{filePath},
	}); !strings.Contains(errMsg, "ops[0].pat") {
		t.Fatalf("expected empty op pat error, got %q", errMsg)
	}
	if _, errMsg := h.callTool("ast_edit", map[string]any{
		"ops":   []any{map[string]any{"pat": "a", "out": "b"}, map[string]any{"pat": "a", "out": "c"}},
		"paths": []any{filePath},
	}); !strings.Contains(errMsg, "Duplicate rewrite pattern") {
		t.Fatalf("expected duplicate pattern error, got %q", errMsg)
	}
	if _, errMsg := h.callTool("ast_edit", map[string]any{
		"ops":   []any{map[string]any{"pat": "a", "out": "b"}},
		"paths": []any{"https://example.com/a.ts"},
	}); !strings.Contains(errMsg, "Cannot rewrite external URL") {
		t.Fatalf("expected external URL error, got %q", errMsg)
	}
}

func TestApplyEditsToSourceDeduplicatesIdentical(t *testing.T) {
	src := "foo(1) + foo(2)\n"
	changes := []astReplaceChange{
		{ByteStart: 0, ByteEnd: 6, Before: "foo(1)", After: "bar(1)"},
		{ByteStart: 0, ByteEnd: 6, Before: "foo(1)", After: "bar(1)"},
		{ByteStart: 9, ByteEnd: 15, Before: "foo(2)", After: "bar(2)"},
	}
	out, deduped, err := applyEditsToSource(src, changes, "test.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(deduped) != 2 || out != "bar(1) + bar(2)\n" {
		t.Fatalf("unexpected result: len=%d out=%q", len(deduped), out)
	}
}

// --- Real-binary integration tests (skip cleanly when no verified ast-grep >= 0.40 is on PATH) ---

func TestRealAstGrepIntegration(t *testing.T) {
	resetProbeCacheForTests()
	bin, err := findAstGrepBinary()
	if err != nil {
		t.Skip("verified ast-grep (>= 0.40) binary not installed on PATH")
	}
	t.Logf("running real ast-grep integration test with %s", bin)

	cwd := t.TempDir()
	tsFile := filepath.Join(cwd, "sample.ts")
	hdrFile := filepath.Join(cwd, "buffer.h")
	noExtFile := filepath.Join(cwd, "script_no_ext")
	crlfFile := filepath.Join(cwd, "crlf_utf8.ts")

	writeTestFile(t, tsFile, "function greet() {\n  const neg = -val;\n  legacyWrap(1, 2);\n}\n")
	writeTestFile(t, hdrFile, "class Buffer { public: int size() const { return 1; } };\n")
	writeTestFile(t, noExtFile, "console.log(42);\n")
	writeTestFile(t, crlfFile, "const s = \"héllo 🌍\";\r\nlegacyWrap(10, 20);\r\n")

	ext := sdk.New("test-ast-real")
	Register(ext)
	h := startHost(t, ext, cwd, []string{"ast_grep", "ast_edit", "resolve"})

	// 1. Standard TS search
	searchRes, errMsg := h.callTool("ast_grep", map[string]any{"pat": "legacyWrap($A, $B)", "path": tsFile})
	if errMsg != "" || searchRes.IsError {
		t.Fatalf("real ast_grep failed: err=%q res=%+v", errMsg, searchRes)
	}
	if mc, _ := searchRes.Details["matchCount"].(float64); int(mc) != 1 {
		t.Fatalf("expected 1 match in sample.ts, got %v", searchRes.Details["matchCount"])
	}

	// 2. Explicit cpp on .h header (requires --stdin routing in real ast-grep)
	hdrRes, errMsg := h.callTool("ast_grep", map[string]any{"pat": "class Buffer", "path": hdrFile, "lang": "cpp"})
	if errMsg != "" || hdrRes.IsError {
		t.Fatalf("real ast_grep .h+cpp failed: err=%q res=%+v", errMsg, hdrRes)
	}
	if mc, _ := hdrRes.Details["matchCount"].(float64); int(mc) != 1 {
		t.Fatalf("expected 1 match in buffer.h with lang=cpp, got %v", hdrRes.Details["matchCount"])
	}

	// 3. Extensionless file with explicit lang=ts (requires --stdin routing in real ast-grep)
	noExtRes, errMsg := h.callTool("ast_grep", map[string]any{"pat": "console.log($X)", "path": noExtFile, "lang": "ts"})
	if errMsg != "" || noExtRes.IsError {
		t.Fatalf("real ast_grep extensionless+ts failed: err=%q res=%+v", errMsg, noExtRes)
	}
	if mc, _ := noExtRes.Details["matchCount"].(float64); int(mc) != 1 {
		t.Fatalf("expected 1 match in extensionless with lang=ts, got %v", noExtRes.Details["matchCount"])
	}

	// 4. Leading-dash pattern (requires attached --pattern=-$A)
	dashRes, errMsg := h.callTool("ast_grep", map[string]any{"pat": "-$A", "path": tsFile})
	if errMsg != "" || dashRes.IsError {
		t.Fatalf("real ast_grep leading-dash failed: err=%q res=%+v", errMsg, dashRes)
	}
	if mc, _ := dashRes.Details["matchCount"].(float64); int(mc) != 1 {
		t.Fatalf("expected 1 match for -$A, got %v", dashRes.Details["matchCount"])
	}

	// 5. Rewrite across LF + CRLF + non-ASCII UTF-8 offsets
	if _, errMsg := h.callTool("ast_edit", map[string]any{
		"ops":   []any{map[string]any{"pat": "legacyWrap($A, $B)", "out": "modernWrap($B, $A)"}},
		"paths": []any{tsFile, crlfFile},
	}); errMsg != "" {
		t.Fatalf("real ast_edit preview failed: %s", errMsg)
	}
	applyRes, errMsg := h.callTool("resolve", map[string]any{"reason": "apply real integration rewrite"})
	if errMsg != "" || applyRes.IsError {
		t.Fatalf("real resolve failed: err=%q res=%+v", errMsg, applyRes)
	}
	if b, _ := os.ReadFile(tsFile); !strings.Contains(string(b), "modernWrap(2, 1)") {
		t.Fatalf("sample.ts not rewritten properly:\n%s", string(b))
	}
	if b, _ := os.ReadFile(crlfFile); string(b) != "const s = \"héllo 🌍\";\r\nmodernWrap(20, 10);\r\n" {
		t.Fatalf("crlf_utf8.ts corrupted or not rewritten:\n%q", string(b))
	}
}

func TestRealAstGrepMixedLanguageDirectory(t *testing.T) {
	resetProbeCacheForTests()
	bin, err := findAstGrepBinary()
	if err != nil {
		t.Skip("verified ast-grep (>= 0.40) binary not installed on PATH")
	}
	t.Logf("running real mixed-language integration test with %s", bin)

	cwd := t.TempDir()
	tsFile := filepath.Join(cwd, "a.ts")
	jsonFile := filepath.Join(cwd, "package.json")
	pyFile := filepath.Join(cwd, "tool.py")

	writeTestFile(t, tsFile, "import { useState, useEffect } from \"react\";\nconst double = (x: number) => x * 2;\n")
	writeTestFile(t, jsonFile, "{\"name\": \"demo\", \"version\": \"1.0.0\"}\n")
	writeTestFile(t, pyFile, "def helper(x):\n    return x + 1\n")

	ext := sdk.New("test-ast-real-mixed")
	Register(ext)
	h := startHost(t, ext, cwd, []string{"ast_grep", "ast_edit", "resolve"})

	// 1. ast_grep with omp's own import example across .ts + .json + .py
	impRes, errMsg := h.callTool("ast_grep", map[string]any{
		"pat":  `import { $$$IMPORTS } from "react"`,
		"path": cwd,
	})
	if errMsg != "" || impRes.IsError {
		t.Fatalf("real mixed-dir import search failed: err=%q res=%+v", errMsg, impRes)
	}
	if mc, _ := impRes.Details["matchCount"].(float64); int(mc) != 1 {
		t.Fatalf("expected 1 TS import match, got %v (content:\n%s)", impRes.Details["matchCount"], impRes.Content)
	}
	if !strings.Contains(impRes.Content, "Invalid pattern:") {
		t.Fatalf("expected per-language Invalid pattern diagnostic in:\n%s", impRes.Content)
	}

	// 2. ast_grep with omp's own arrow-function example across .ts + .json + .py
	arrowRes, errMsg := h.callTool("ast_grep", map[string]any{
		"pat":  "const $NAME = ($$$ARGS) => $BODY",
		"path": cwd,
	})
	if errMsg != "" || arrowRes.IsError {
		t.Fatalf("real mixed-dir arrow search failed: err=%q res=%+v", errMsg, arrowRes)
	}
	if mc, _ := arrowRes.Details["matchCount"].(float64); int(mc) != 1 {
		t.Fatalf("expected 1 TS arrow match, got %v (content:\n%s)", arrowRes.Details["matchCount"], arrowRes.Content)
	}

	// 3. ast_edit + resolve with arrow-function rewrite across .ts + .json + .py
	editRes, errMsg := h.callTool("ast_edit", map[string]any{
		"ops":   []any{map[string]any{"pat": "const $NAME = ($$$ARGS) => $BODY", "out": "function $NAME($$$ARGS) { return $BODY; }"}},
		"paths": []any{cwd},
	})
	if errMsg != "" || editRes.IsError {
		t.Fatalf("real mixed-dir ast_edit failed: err=%q res=%+v", errMsg, editRes)
	}
	if tr, _ := editRes.Details["totalReplacements"].(float64); int(tr) != 1 {
		t.Fatalf("expected 1 TS replacement, got %v", editRes.Details["totalReplacements"])
	}
	applyRes, errMsg := h.callTool("resolve", map[string]any{"reason": "apply mixed-dir arrow rewrite"})
	if errMsg != "" || applyRes.IsError {
		t.Fatalf("real mixed-dir resolve failed: err=%q res=%+v", errMsg, applyRes)
	}
	if b, _ := os.ReadFile(tsFile); !strings.Contains(string(b), "function double(x: number) { return x * 2; }") {
		t.Fatalf("a.ts not rewritten as expected:\n%s", string(b))
	}
	if b, _ := os.ReadFile(jsonFile); string(b) != "{\"name\": \"demo\", \"version\": \"1.0.0\"}\n" {
		t.Fatalf("package.json should remain untouched:\n%s", string(b))
	}
}

// upstream: packages/coding-agent/src/tools/resolve.ts:197-216, packages/coding-agent/src/prompts/system/resolve-reminder.md
func TestAstEditPreviewInjectsResolveReminder(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "sample.ts")
	writeTestFile(t, file, "foo(1)\n")

	installStrictFakeAstGrep(t, `
if [ "$HAS_REWRITE" -eq 1 ]; then
  for f in "$@"; do
    printf '{"text":"foo(1)","replacement":"bar(1)","replacementOffsets":{"start":0,"end":6},"range":{"byteOffset":{"start":0,"end":6},"start":{"line":0,"column":0},"end":{"line":0,"column":6}},"file":"%s","lines":"foo(1)","language":"TypeScript"}\n' "$f"
  done
fi
`)

	// 1. With bash active (command transport on): reminder tells model to run resolve/reject with `bash`.
	ext1 := sdk.New("test-ast-reminder-cmd")
	Register(ext1)
	h1 := startHost(t, ext1, dir, []string{"bash", "ast_edit"})
	res1, errMsg := h1.callTool("ast_edit", map[string]any{
		"ops":   []any{map[string]any{"pat": "foo($A)", "out": "bar($A)"}},
		"paths": []any{"sample.ts"},
	})
	if errMsg != "" || res1.IsError {
		t.Fatalf("ast_edit preview failed: err=%q res=%+v", errMsg, res1)
	}
	h1.mu.Lock()
	msgs1 := append([]sentCustomMessage(nil), h1.sentMsgs...)
	h1.mu.Unlock()
	if len(msgs1) != 1 {
		t.Fatalf("expected 1 resolve-reminder message, got %d: %+v", len(msgs1), msgs1)
	}
	wantCmd := "<system-reminder>\n`ast_edit` result above: PREVIEW — no files changed. Finalize now with `bash`: run `resolve \"<one-sentence reason>\"` to APPLY, or `reject \"<one-sentence reason>\"` to DISCARD.\n</system-reminder>"
	if msgs1[0].CustomType != "resolve-reminder" || msgs1[0].Display || msgs1[0].DeliverAs != "steer" {
		t.Fatalf("unexpected reminder metadata: %+v", msgs1[0])
	}
	if msgs1[0].TriggerTurn == nil || *msgs1[0].TriggerTurn {
		t.Fatalf("expected TriggerTurn=false, got %+v", msgs1[0].TriggerTurn)
	}
	if msgs1[0].Content != wantCmd {
		t.Fatalf("reminder content mismatch:\ngot:  %q\nwant: %q", msgs1[0].Content, wantCmd)
	}
	if gotTool, _ := msgs1[0].Details["toolName"].(string); gotTool != "ast_edit" {
		t.Fatalf("expected details.toolName=ast_edit, got %+v", msgs1[0].Details)
	}

	// 2. Without bash active (command transport off): reminder tells model to call `resolve`/`reject` directly.
	ext2 := sdk.New("test-ast-reminder-direct")
	Register(ext2)
	h2 := startHost(t, ext2, dir, []string{"ast_edit", "resolve", "reject"})
	res2, errMsg := h2.callTool("ast_edit", map[string]any{
		"ops":   []any{map[string]any{"pat": "foo($A)", "out": "bar($A)"}},
		"paths": []any{"sample.ts"},
	})
	if errMsg != "" || res2.IsError {
		t.Fatalf("ast_edit preview (direct) failed: err=%q res=%+v", errMsg, res2)
	}
	h2.mu.Lock()
	msgs2 := append([]sentCustomMessage(nil), h2.sentMsgs...)
	h2.mu.Unlock()
	if len(msgs2) != 1 {
		t.Fatalf("expected 1 direct resolve-reminder message, got %d", len(msgs2))
	}
	wantDirect := "<system-reminder>\n`ast_edit` result above: PREVIEW — no files changed. Finalize now: call `resolve` with a one-sentence `reason` to APPLY, or `reject` with a one-sentence `reason` to DISCARD.\n</system-reminder>"
	if msgs2[0].Content != wantDirect {
		t.Fatalf("direct reminder content mismatch:\ngot:  %q\nwant: %q", msgs2[0].Content, wantDirect)
	}
}

// upstream: packages/coding-agent/src/tools/ast-grep.ts (details.files is always an array, never null)
func TestAstGrepNoMatchDetailsFilesIsEmptyArrayNotNull(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "sample.ts"), "const x = 1;\n")

	installStrictFakeAstGrep(t, ``)

	ext := sdk.New("test-ast-grep-empty-files")
	Register(ext)
	h := startHost(t, ext, dir, []string{"ast_grep"})
	res, errMsg := h.callTool("ast_grep", map[string]any{
		"pat":  "nonexistent($X)",
		"path": dir,
	})
	if errMsg != "" || res.IsError {
		t.Fatalf("ast_grep failed: err=%q res=%+v", errMsg, res)
	}
	raw, err := json.Marshal(res.Details)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"files":[]`) {
		t.Fatalf("expected details.files to serialize as [], got %s", raw)
	}
}

// upstream: packages/utils/src/dirs.ts:148-158 (standardizeMacOSPath),
// packages/coding-agent/src/tools/ast-grep.ts:298-299, ast-edit.ts:349-350
func TestAstToolsStandardizeMacOSPaths(t *testing.T) {
	rawDir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(rawDir); err == nil {
		rawDir = resolved
	}
	wantDir := dirs.StandardizeMacOSPath(rawDir)
	if runtime.GOOS == "darwin" && strings.HasPrefix(rawDir, "/private/") && strings.HasPrefix(wantDir, "/private/") {
		t.Fatalf("expected darwin TempDir %q to strip /private, got %q", rawDir, wantDir)
	}
	if runtime.GOOS != "darwin" && wantDir != rawDir {
		t.Fatalf("expected non-darwin path unchanged: got %q, want %q", wantDir, rawDir)
	}

	filePath := filepath.Join(rawDir, "sample.ts")
	writeTestFile(t, filePath, "foo(1);\n")
	ndjson := fmt.Sprintf(`{"text":"foo(1)","replacement":"bar(1)","replacementOffsets":{"start":0,"end":6},"range":{"byteOffset":{"start":0,"end":6},"start":{"line":0,"column":0},"end":{"line":0,"column":6}},"file":%q,"language":"TypeScript"}`, filePath)
	installStrictFakeAstGrep(t, fmt.Sprintf("printf '%%s\\n' %q", ndjson))

	ext := sdk.New("test-ast-macos-paths")
	Register(ext)
	h := startHost(t, ext, rawDir, []string{"ast_grep", "ast_edit"})

	grepRes, errMsg := h.callTool("ast_grep", map[string]any{"pat": "foo($A)", "path": "."})
	if errMsg != "" || grepRes.IsError {
		t.Fatalf("ast_grep failed: err=%q res=%+v", errMsg, grepRes)
	}
	if got, _ := grepRes.Details["cwd"].(string); got != wantDir {
		t.Fatalf("ast_grep details.cwd = %q, want %q", got, wantDir)
	}
	if got, _ := grepRes.Details["searchPath"].(string); got != wantDir {
		t.Fatalf("ast_grep details.searchPath = %q, want %q", got, wantDir)
	}

	editRes, errMsg := h.callTool("ast_edit", map[string]any{
		"ops":   []any{map[string]any{"pat": "foo($A)", "out": "bar($A)"}},
		"paths": []any{"sample.ts"},
	})
	if errMsg != "" || editRes.IsError {
		t.Fatalf("ast_edit failed: err=%q res=%+v", errMsg, editRes)
	}
	wantFile := filepath.Join(wantDir, "sample.ts")
	if got, _ := editRes.Details["cwd"].(string); got != wantDir {
		t.Fatalf("ast_edit details.cwd = %q, want %q", got, wantDir)
	}
	if got, _ := editRes.Details["searchPath"].(string); got != wantFile {
		t.Fatalf("ast_edit details.searchPath = %q, want %q", got, wantFile)
	}
}
