package ohmypig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/github"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/intent"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/modes"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/schemaorder"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/settings"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/toolcmd"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/surface"
)

// Expected top-level registered tool set for Extension() (yield is dynamic per
// structured child session and is not registered top-level).
var expectedRegisteredTools = []string{
	"ask",
	"ast_edit",
	"ast_grep",
	"bash",
	"checkpoint",
	"debug",
	"edit",
	"eval",
	"find",
	"github",
	"glob",
	"goal",
	"grep",
	"lsp",
	"propose",
	"read",
	"reject",
	"resolve",
	"rewind",
	"task",
	"todo",
	"wait",
	"web_search",
	"write",
}

func TestExtensionDoubleRegisterAndSchemaOrder(t *testing.T) {
	t.Setenv("PI_NO_INTENT", "")
	t.Setenv("PI_INTENT_TRACING", "")

	// (a) Register Extension() twice (two independent sessions) without panic,
	// and verify hook wiring runs exactly once.
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("USERPROFILE", fakeHome)
	t.Setenv("PIG_HOME", filepath.Join(fakeHome, ".pig"))
	t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(fakeHome, ".pig", "agent"))

	extTUI := Extension()
	extPrint := Extension()
	if got := wireCount.Load(); got != 1 {
		t.Fatalf("wire() ran %d times, want 1", got)
	}

	hTUI := startFakeHost(t, extTUI, fakeHostOptions{
		SessionID: "sess-tui-schema",
		Mode:      "tui",
		HasUI:     true,
		FakeHome:  fakeHome,
	})
	hTUI.emitEvent(sdk.EventSessionStart, nil)

	hPrint := startFakeHost(t, extPrint, fakeHostOptions{
		SessionID: "sess-print-schema",
		Mode:      "print",
		HasUI:     false,
		FakeHome:  fakeHome,
	})
	hPrint.emitEvent(sdk.EventSessionStart, nil)

	// (b) Exact set of registered tool names.
	gotTools := hTUI.registeredToolNames()
	if !slices.Equal(gotTools, expectedRegisteredTools) {
		t.Fatalf("registered tools mismatch:\ngot:  %v\nwant: %v", gotTools, expectedRegisteredTools)
	}

	// Fire before_agent_start as well so dynamic re-registrations (task, wait,
	// eval, github, intent sync) are included in the schema order verification.
	hTUI.emitBeforeAgentStart()
	hPrint.emitBeforeAgentStart()

	// Verify recursive wire schema key order for ALL registered tools against
	// omp_schemas.json in both TUI (with `i` first on non-omit tools) and print
	// mode (no `i`, matching omp's exact key order at every nested object).
	ompEntries := schemaorder.Entries()
	for _, toolName := range expectedRegisteredTools {
		ompEntry := ompEntries[toolName]
		if ompEntry == nil {
			t.Fatalf("missing omp_schemas.json entry for %s", toolName)
		}
		mode := intent.ModeFor(toolName)
		ompOrders := extractWireObjectKeyOrders(t, ompEntry.RawParameters)
		tuiOrders := extractWireObjectKeyOrders(t, hTUI.tool(toolName).Parameters)
		printOrders := extractWireObjectKeyOrders(t, hPrint.tool(toolName).Parameters)

		for path, ompKeys := range ompOrders {
			// Filter ompKeys to the keys actually present in the port schema at
			// this path (e.g., task omits tasks[].tools in known_content_diffs),
			// then verify those keys appear in omp's exact order (with `i` first
			// at $.properties in TUI for non-omit tools).
			gotPrint, ok := printOrders[path]
			if !ok {
				continue
			}
			if path == "$.properties" && slices.Contains(gotPrint, intent.Field) {
				t.Errorf("print-mode %s wire schema must omit %q, got %v", toolName, intent.Field, gotPrint)
			}
			wantPrint := filterKeysInOrder(ompKeys, gotPrint)
			if !slices.Equal(gotPrint[:len(wantPrint)], wantPrint) {
				t.Errorf("print-mode %s %s key order = %v, want prefix %v", toolName, path, gotPrint, wantPrint)
			}

			gotTUI, ok := tuiOrders[path]
			if !ok {
				t.Errorf("TUI %s missing schema object at %s", toolName, path)
				continue
			}
			if path == "$.properties" {
				if mode == intent.ModeOmit {
					if slices.Contains(gotTUI, intent.Field) {
						t.Errorf("omit tool %s must not inject i in TUI: %v", toolName, gotTUI)
					}
					wantTUI := filterKeysInOrder(ompKeys, gotTUI)
					if !slices.Equal(gotTUI[:len(wantTUI)], wantTUI) {
						t.Errorf("TUI %s %s key order = %v, want prefix %v", toolName, path, gotTUI, wantTUI)
					}
				} else {
					if len(gotTUI) == 0 || gotTUI[0] != intent.Field {
						t.Errorf("tool %s in TUI must have i first, got %v", toolName, gotTUI)
					} else {
						rest := gotTUI[1:]
						wantRest := filterKeysInOrder(ompKeys, rest)
						if !slices.Equal(rest[:len(wantRest)], wantRest) {
							t.Errorf("TUI %s %s key order after i = %v, want prefix %v", toolName, path, rest, wantRest)
						}
					}
				}
			} else {
				wantTUI := filterKeysInOrder(ompKeys, gotTUI)
				if !slices.Equal(gotTUI[:len(wantTUI)], wantTUI) {
					t.Errorf("TUI %s %s key order = %v, want prefix %v", toolName, path, gotTUI, wantTUI)
				}
			}
		}
	}
}

// extractWireObjectKeyOrders parses raw JSON by token stream and returns a map
// of JSON path -> object key order as serialized on the wire.
func extractWireObjectKeyOrders(t *testing.T, raw []byte) map[string][]string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	out := map[string][]string{}
	tok, err := dec.Token()
	if err != nil {
		t.Fatalf("extractWireObjectKeyOrders: %v", err)
	}
	walkWireOrder(t, dec, tok, "$", out)
	return out
}

func walkWireOrder(t *testing.T, dec *json.Decoder, tok json.Token, path string, out map[string][]string) {
	t.Helper()
	delim, ok := tok.(json.Delim)
	if !ok {
		return
	}
	switch delim {
	case '{':
		var keys []string
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				t.Fatalf("read key at %s: %v", path, err)
			}
			k := kt.(string)
			keys = append(keys, k)
			vt, err := dec.Token()
			if err != nil {
				t.Fatalf("read value at %s.%s: %v", path, k, err)
			}
			walkWireOrder(t, dec, vt, path+"."+k, out)
		}
		if _, err := dec.Token(); err != nil {
			t.Fatalf("read '}' at %s: %v", path, err)
		}
		out[path] = keys
	case '[':
		idx := 0
		for dec.More() {
			et, err := dec.Token()
			if err != nil {
				t.Fatalf("read element at %s[%d]: %v", path, idx, err)
			}
			walkWireOrder(t, dec, et, fmt.Sprintf("%s[%d]", path, idx), out)
			idx++
		}
		if _, err := dec.Token(); err != nil {
			t.Fatalf("read ']' at %s: %v", path, err)
		}
	}
}

func filterKeysInOrder(reference, present []string) []string {
	set := make(map[string]bool, len(present))
	for _, k := range present {
		set[k] = true
	}
	out := make([]string, 0, len(reference))
	for _, k := range reference {
		if set[k] {
			out = append(out, k)
		}
	}
	return out
}

// (c) real tool calls, (d) mounted tool command help via bash,
// (e) plan-mode gate refusal, (g) call without `i` passing faithful host validator.
func TestExtensionEndToEndCallsMountedPlanGateAndNoIntent(t *testing.T) {
	t.Setenv("PI_NO_INTENT", "")
	t.Setenv("PI_INTENT_TRACING", "")

	cwd := t.TempDir()
	samplePath := filepath.Join(cwd, "hello.txt")
	if err := os.WriteFile(samplePath, []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ext := Extension()
	h := startFakeHost(t, ext, fakeHostOptions{
		Cwd:       cwd,
		SessionID: "sess-e2e-1",
		Mode:      "tui",
		HasUI:     true,
	})
	h.emitEvent(sdk.EventSessionStart, nil)

	// (c) + (g): call wrapped require tool `read` WITHOUT `i` through faithful validator.
	readRaw, err := h.callTool("read", "tc-read-1", map[string]any{"path": "hello.txt"})
	if err != nil {
		t.Fatalf("read without i failed host validation/execution: %v", err)
	}
	readRes := decodeToolOutcome(t, readRaw)
	if readRes.IsError || !strings.Contains(readRes.Content, "beta") {
		t.Fatalf("unexpected read result: %+v", readRes)
	}

	// (c) grep
	grepRaw, err := h.callTool("grep", "tc-grep-1", map[string]any{
		"i":       "Searching hello",
		"pattern": "beta",
		"path":    "hello.txt",
	})
	if err != nil {
		t.Fatalf("grep failed: %v", err)
	}
	grepRes := decodeToolOutcome(t, grepRaw)
	if grepRes.IsError || !strings.Contains(grepRes.Content, "beta") {
		t.Fatalf("unexpected grep result: %+v", grepRes)
	}

	// (c) bash echo hi (also omit `i` to exercise require validator on bash)
	bashRaw, err := h.callTool("bash", "tc-bash-1", map[string]any{"command": "echo hi"})
	if err != nil {
		t.Fatalf("bash echo hi failed: %v", err)
	}
	bashRes := decodeToolOutcome(t, bashRaw)
	if bashRes.IsError || !strings.HasPrefix(bashRes.Content, "hi") {
		t.Fatalf("unexpected bash result: %+v", bashRes)
	}
	// (c) todo init
	todoRaw, err := h.callTool("todo", "tc-todo-1", map[string]any{
		"i":     "Initializing checklist",
		"op":    "init",
		"items": []string{"first item", "second item"},
	})
	if err != nil {
		t.Fatalf("todo init failed: %v", err)
	}
	todoRes := decodeToolOutcome(t, todoRaw)
	if todoRes.IsError || !strings.Contains(todoRes.Content, "first item") {
		t.Fatalf("unexpected todo result: %+v", todoRes)
	}

	// (c) write then edit
	writeRaw, err := h.callTool("write", "tc-write-1", map[string]any{
		"i":       "Writing notes",
		"path":    "notes.txt",
		"content": "line one\nline two\n",
	})
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}
	writeRes := decodeToolOutcome(t, writeRaw)
	if writeRes.IsError {
		t.Fatalf("write returned error: %+v", writeRes)
	}
	header := strings.SplitN(writeRes.Content, "\n", 2)[0]
	if !strings.HasPrefix(header, "[notes.txt#") {
		t.Fatalf("expected hashline header from write, got %q", header)
	}
	editRaw, err := h.callTool("edit", "tc-edit-1", map[string]any{
		"i":     "Updating second line",
		"input": header + "\nPUT 2.=2:\n+line TWO\n",
	})
	if err != nil {
		t.Fatalf("edit failed: %v", err)
	}
	editRes := decodeToolOutcome(t, editRaw)
	if editRes.IsError {
		t.Fatalf("edit returned error: %+v", editRes)
	}
	editedBytes, err := os.ReadFile(filepath.Join(cwd, "notes.txt"))
	if err != nil || string(editedBytes) != "line one\nline TWO\n" {
		t.Fatalf("edited file = %q (err=%v), want %q", string(editedBytes), err, "line one\nline TWO\n")
	}

	// (d) Mounted tool command: with tool commands on (default), `bash` running
	// `lsp --help` returns the mounted lsp tool's help text.
	helpRaw, err := h.callTool("bash", "tc-bash-lsp-help", map[string]any{
		"i":       "Checking lsp usage",
		"command": "lsp --help",
	})
	if err != nil {
		t.Fatalf("bash lsp --help failed: %v", err)
	}
	helpRes := decodeToolOutcome(t, helpRaw)
	if helpRes.IsError || !strings.Contains(helpRes.Content, "lsp <action>") || !strings.Contains(helpRes.Content, "--action") {
		t.Fatalf("unexpected lsp --help output: %+v", helpRes)
	}

	// (e) Plan mode: enter plan mode via /plan, then verify write outside the
	// local:// plan file is refused by internal/gate.
	if err := h.callCommand("plan", ""); err != nil {
		t.Fatalf("/plan command failed: %v", err)
	}
	planWriteRaw, planWriteErr := h.callTool("write", "tc-write-blocked", map[string]any{
		"i":       "Attempting blocked write",
		"path":    "blocked.txt",
		"content": "should not land\n",
	})
	var refusalText string
	if planWriteErr != nil {
		refusalText = planWriteErr.Error()
	} else {
		res := decodeToolOutcome(t, planWriteRaw)
		if res.IsError {
			refusalText = res.Content
		}
	}
	if !strings.Contains(refusalText, "Plan mode") {
		t.Fatalf("expected plan-mode gate refusal on working-tree write, got err=%v raw=%s", planWriteErr, string(planWriteRaw))
	}
	if _, statErr := os.Stat(filepath.Join(cwd, "blocked.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("blocked.txt should not have been created in plan mode")
	}
}

// (f) session_shutdown for session A releases A's state in areas with per-session
// state and leaves session B working.
// (i) session_shutdown{reason: "new"} for A followed by session_start for new id B:
// A's state is gone, B is clean.
func TestExtensionSessionShutdownIsolationAndNewSession(t *testing.T) {
	t.Setenv("PI_NO_INTENT", "")
	t.Setenv("PI_INTENT_TRACING", "")

	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("USERPROFILE", fakeHome)
	t.Setenv("PIG_HOME", filepath.Join(fakeHome, ".pig"))
	t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(fakeHome, ".pig", "agent"))

	cwdA := t.TempDir()
	cwdB := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwdA, "a.txt"), []byte("from A\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwdB, "b.txt"), []byte("from B\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	extA := Extension()
	extB := Extension()
	hA := startFakeHost(t, extA, fakeHostOptions{
		Cwd:       cwdA,
		SessionID: "sess-iso-A",
		Mode:      "tui",
		HasUI:     true,
		FakeHome:  fakeHome,
	})
	hB := startFakeHost(t, extB, fakeHostOptions{
		Cwd:       cwdB,
		SessionID: "sess-iso-B",
		Mode:      "tui",
		HasUI:     true,
		FakeHome:  fakeHome,
	})
	hA.emitEvent(sdk.EventSessionStart, nil)
	hB.emitEvent(sdk.EventSessionStart, nil)

	// Populate per-session state in both A and B: todo, plan mode (A only), hashline snapshot, settings override.
	if _, err := hA.callTool("todo", "iso-a-todo", map[string]any{"op": "init", "items": []string{"task A"}}); err != nil {
		t.Fatalf("A todo init: %v", err)
	}
	if _, err := hB.callTool("todo", "iso-b-todo", map[string]any{"op": "init", "items": []string{"task B"}}); err != nil {
		t.Fatalf("B todo init: %v", err)
	}
	if err := hA.callCommand("plan", ""); err != nil {
		t.Fatalf("A /plan: %v", err)
	}
	readB, err := hB.callTool("read", "iso-b-read", map[string]any{"path": "b.txt"})
	if err != nil {
		t.Fatalf("B read: %v", err)
	}
	headerB := strings.SplitN(decodeToolOutcome(t, readB).Content, "\n", 2)[0]

	// Probe A's plan mode via a command helper before shutdown.
	var planActiveA bool
	extA.RegisterCommand("probe-a", sdk.CommandOptions{
		Description: "probe",
		Handler: func(ctx sdk.Context, _ string) error {
			planActiveA = modes.PlanActive(ctx)
			_ = settings.Set(ctx, "todo.remindersMax", 7, false)
			return nil
		},
	})
	if err := hA.callCommand("probe-a", ""); err != nil {
		t.Fatalf("probe-a: %v", err)
	}
	if !planActiveA {
		t.Fatalf("expected session A to be in plan mode before shutdown")
	}

	// Shut down session A with reason "new".
	hA.emitEvent(sdk.EventSessionShutdown, map[string]any{"reason": "new"})

	// Verify A's plan mode and todo state are gone, while B keeps working
	// (todo view still shows task B, edit with prior read header works, bash works).
	var planAfterA bool
	var remindersAfterA int
	extA.RegisterCommand("probe-a-after", sdk.CommandOptions{
		Description: "probe after",
		Handler: func(ctx sdk.Context, _ string) error {
			planAfterA = modes.PlanActive(ctx)
			remindersAfterA = settings.Int(ctx, "todo.remindersMax")
			return nil
		},
	})
	if err := hA.callCommand("probe-a-after", ""); err != nil {
		t.Fatalf("probe-a-after: %v", err)
	}
	if planAfterA {
		t.Fatalf("expected session A plan mode cleared after session_shutdown")
	}
	if remindersAfterA == 7 {
		t.Fatalf("expected session A runtime override cleared after session_shutdown, still got 7")
	}

	todoViewB, err := hB.callTool("todo", "iso-b-view", map[string]any{"op": "view"})
	if err != nil || !strings.Contains(decodeToolOutcome(t, todoViewB).Content, "task B") {
		t.Fatalf("session B todo lost after session A shutdown: err=%v raw=%s", err, string(todoViewB))
	}
	editB, err := hB.callTool("edit", "iso-b-edit", map[string]any{
		"input": headerB + "\nPUT 1.=1:\n+from B edited\n",
	})
	if err != nil || decodeToolOutcome(t, editB).IsError {
		t.Fatalf("session B edit failed after session A shutdown: err=%v raw=%s", err, string(editB))
	}
	bashB, err := hB.callTool("bash", "iso-b-bash", map[string]any{"command": "echo b-ok"})
	if err != nil || !strings.HasPrefix(decodeToolOutcome(t, bashB).Content, "b-ok") {
		t.Fatalf("session B bash failed after session A shutdown: err=%v raw=%s", err, string(bashB))
	}
	// (i) Start a brand-new session C after A shut down with reason "new": C is clean.
	extC := Extension()
	hC := startFakeHost(t, extC, fakeHostOptions{
		Cwd:       cwdA,
		SessionID: "sess-iso-C",
		Mode:      "tui",
		HasUI:     true,
		FakeHome:  fakeHome,
	})
	hC.emitEvent(sdk.EventSessionStart, nil)
	todoViewC, err := hC.callTool("todo", "iso-c-view", map[string]any{"op": "view"})
	if err != nil || strings.Contains(decodeToolOutcome(t, todoViewC).Content, "task A") {
		t.Fatalf("new session C must not inherit session A todos: err=%v raw=%s", err, string(todoViewC))
	}
	writeC, err := hC.callTool("write", "iso-c-write", map[string]any{
		"path":    "clean.txt",
		"content": "clean\n",
	})
	if err != nil || decodeToolOutcome(t, writeC).IsError {
		t.Fatalf("new session C must not inherit session A plan mode: err=%v raw=%s", err, string(writeC))
	}
}

// (h) RELOAD: PiG fires session_shutdown{reason: "reload"} and then
// session_start for the SAME session ID with a fresh Extension() instance.
// Deliberate difference from omp (integrator decision, review finding 6):
// reload is a clean slate that drops pre-reload session-only setting overrides
// and reloads from the global + trusted project layers (omp keeps runtime
// overrides for the life of the process). Every area must work again after
// reload: tools run, ansi reinstalls working indicator and header/footer
// renderers, intent is recorded, settings reads return the project layer, and
// eval runs a cell.
func TestExtensionReloadSameSessionIDCleanSlate(t *testing.T) {
	t.Setenv("PI_NO_INTENT", "")
	t.Setenv("PI_INTENT_TRACING", "")
	// Force terminal surface so ansi installs both working indicator and
	// header/footer renderers deterministically even inside a Tern pane.
	surface.ResetForTests()
	surface.SetNative(false)
	t.Cleanup(surface.ResetForTests)

	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("USERPROFILE", fakeHome)
	t.Setenv("PIG_HOME", filepath.Join(fakeHome, ".pig"))
	t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(fakeHome, ".pig", "agent"))

	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "reload.txt"), []byte("before reload\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Write a trusted project config so we can assert project-layer reads after reload.
	if err := os.MkdirAll(filepath.Join(cwd, ".pig"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".pig", "config.yml"), []byte("todo:\n  remindersMax: 9\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	const sid = "sess-reload-same-id"

	// Phase 1: initial session instance.
	ext1 := Extension()
	var setOverrideErr error
	ext1.RegisterCommand("set-override", sdk.CommandOptions{
		Description: "set runtime override",
		Handler: func(ctx sdk.Context, _ string) error {
			setOverrideErr = settings.Set(ctx, "todo.remindersMax", 4, false)
			return nil
		},
	})
	h1 := startFakeHost(t, ext1, fakeHostOptions{
		Cwd:            cwd,
		SessionID:      sid,
		Mode:           "tui",
		HasUI:          true,
		FakeHome:       fakeHome,
		ProjectTrusted: true,
	})
	h1.emitEvent(sdk.EventSessionStart, nil)

	if _, err := h1.callTool("read", "rl-read-1", map[string]any{"i": "Reading before reload", "path": "reload.txt"}); err != nil {
		t.Fatalf("pre-reload read: %v", err)
	}
	if _, err := h1.callTool("bash", "rl-bash-1", map[string]any{"i": "Running before reload", "command": "echo pre-reload"}); err != nil {
		t.Fatalf("pre-reload bash: %v", err)
	}
	if err := h1.callCommand("set-override", ""); err != nil || setOverrideErr != nil {
		t.Fatalf("pre-reload set-override: cmd=%v set=%v", err, setOverrideErr)
	}
	if _, err := h1.callTool("todo", "rl-todo-1", map[string]any{"i": "Creating pre-reload todo", "op": "init", "items": []string{"pre"}}); err != nil {
		t.Fatalf("pre-reload todo: %v", err)
	}

	// Fire session_shutdown with reason "reload" and stop the old extension instance.
	h1.emitEvent(sdk.EventSessionShutdown, map[string]any{"reason": "reload"})
	h1.stop()

	// Phase 2: fresh Extension() instance with the SAME session ID.
	ext2 := Extension()
	var (
		postRemindersMax int
		recordedIntent   string
	)
	ext2.RegisterCommand("probe-reload", sdk.CommandOptions{
		Description: "probe settings after reload",
		Handler: func(ctx sdk.Context, _ string) error {
			postRemindersMax = settings.Int(ctx, "todo.remindersMax")
			return nil
		},
	})
	ext2.OnEvent(sdk.EventToolCall, func(ctx sdk.Context, data map[string]any) (any, error) {
		if name, _ := data["toolName"].(string); name == "read" {
			if id, _ := data["toolCallId"].(string); id == "rl-read-2" {
				if input, _ := data["input"].(map[string]any); input != nil {
					recordedIntent, _ = input["i"].(string)
				}
			}
		}
		return nil, nil
	})
	h2 := startFakeHost(t, ext2, fakeHostOptions{
		Cwd:            cwd,
		SessionID:      sid,
		Mode:           "tui",
		HasUI:          true,
		FakeHome:       fakeHome,
		ProjectTrusted: true,
	})
	h2.emitEvent(sdk.EventSessionStart, nil)

	// 1. ANSI reinstalled its working indicator plus header/footer renderers on
	// the reloaded session (ansi never calls ui.setStatus; internal/ansi/chrome.go:787-825).
	h2.mu.Lock()
	callsCopy := append([]string(nil), h2.calls...)
	h2.mu.Unlock()
	for _, wantCall := range []string{"ui.setWorkingIndicator", "ui.setHeader", "ui.setFooter"} {
		if !slices.Contains(callsCopy, wantCall) {
			t.Errorf("post-reload ansi did not call %s; calls=%v", wantCall, callsCopy)
		}
	}
	// 2. Deliberate clean-slate difference from omp: reload drops the pre-reload
	// session override (4) and reads the trusted project layer (9, not default 3).
	if err := h2.callCommand("probe-reload", ""); err != nil {
		t.Fatalf("probe-reload command: %v", err)
	}
	if postRemindersMax != 9 {
		t.Errorf("post-reload settings.Int(todo.remindersMax) = %d, want 9 (clean-slate reload from project .pig/config.yml)", postRemindersMax)
	}
	// 3 & 4. Core tools work again after reload (read, bash, todo) and intent is captured on read.
	read2, err := h2.callTool("read", "rl-read-2", map[string]any{"i": "Probing post-reload intent", "path": "reload.txt"})
	if err != nil || !strings.Contains(decodeToolOutcome(t, read2).Content, "before reload") {
		t.Errorf("post-reload read failed: err=%v raw=%s", err, string(read2))
	}
	if recordedIntent != "Probing post-reload intent" {
		t.Errorf("post-reload intent = %q, want %q", recordedIntent, "Probing post-reload intent")
	}
	bash2, err := h2.callTool("bash", "rl-bash-2", map[string]any{"i": "Running after reload", "command": "echo post-reload"})
	if err != nil || !strings.HasPrefix(decodeToolOutcome(t, bash2).Content, "post-reload") {
		t.Errorf("post-reload bash failed: err=%v raw=%s", err, string(bash2))
	}
	todo2, err := h2.callTool("todo", "rl-todo-2", map[string]any{"i": "Init after reload", "op": "init", "items": []string{"post"}})
	if err != nil || !strings.Contains(decodeToolOutcome(t, todo2).Content, "post") {
		t.Errorf("post-reload todo failed: err=%v raw=%s", err, string(todo2))
	}

	// 5. Eval runs a cell after reload.
	eval2, err := h2.callTool("eval", "rl-eval-2", map[string]any{
		"language": "py",
		"code":     "print(6 * 7)",
		"title":    "Post-reload eval",
	})
	if err != nil {
		t.Errorf("post-reload eval failed: %v", err)
	} else {
		evalRes := decodeToolOutcome(t, eval2)
		if evalRes.IsError || !strings.Contains(evalRes.Content, "42") {
			t.Errorf("post-reload eval unexpected result: %+v", evalRes)
		}
	}

	// Clean up reloaded session so Python kernel exits before test finishes.
	h2.emitEvent(sdk.EventSessionShutdown, map[string]any{"reason": "exit"})
}

// Joined test with the REAL internal/settings store and NO overrides exercising
// every settings-connected area with omp's registered default values, plus
// prompt advertisement of cfg:// and out:// in an interactive TUI session.
func TestExtensionRealSettingsDefaults(t *testing.T) {
	t.Setenv("PI_NO_INTENT", "")
	t.Setenv("PI_INTENT_TRACING", "")
	t.Setenv("PI_OFFLINE", "1")

	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("USERPROFILE", fakeHome)
	t.Setenv("PIG_HOME", filepath.Join(fakeHome, ".pig"))
	t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(fakeHome, ".pig", "agent"))

	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "sample.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ext := Extension()
	var (
		ghCache        github.CacheTTLSettings
		askTimeout     any
		todoEnabled    any
		todoReminders  any
		todoMax        any
		findEnabled    any
		findThreshold  any
		ghCacheZeroTTL github.CacheTTLSettings
		ghCacheFracTTL github.CacheTTLSettings
	)
	ext.RegisterCommand("probe-defaults", sdk.CommandOptions{
		Description: "probe default settings",
		Handler: func(ctx sdk.Context, _ string) error {
			ghCache = github.CacheSettingsFn(ctx)
			askTimeout = settings.Any(ctx, "ask.timeout")
			todoEnabled = settings.Any(ctx, "todo.enabled")
			todoReminders = settings.Any(ctx, "todo.reminders")
			todoMax = settings.Any(ctx, "todo.remindersMax")
			findEnabled = settings.Any(ctx, "find.enabled")
			findThreshold = settings.Any(ctx, "find.hitThreshold")
			return nil
		},
	})
	ext.RegisterCommand("probe-github-ttl-edge", sdk.CommandOptions{
		Description: "probe zero, negative, and fractional github cache TTLs",
		Handler: func(ctx sdk.Context, _ string) error {
			// 0 is valid (soft 0 = always revalidate, hard 0 = keep nothing).
			if err := settings.Set(ctx, github.SettingCacheSoftTTL, 0, false); err != nil {
				return err
			}
			if err := settings.Set(ctx, github.SettingCacheHardTTL, 0, false); err != nil {
				return err
			}
			ghCacheZeroTTL = github.CacheSettingsFn(ctx)
			// Fractional seconds preserved; negatives clamped to 0.
			if err := settings.Set(ctx, github.SettingCacheSoftTTL, 1.5, false); err != nil {
				return err
			}
			if err := settings.Set(ctx, github.SettingCacheHardTTL, -10, false); err != nil {
				return err
			}
			ghCacheFracTTL = github.CacheSettingsFn(ctx)
			return nil
		},
	})

	h := startFakeHost(t, ext, fakeHostOptions{
		Cwd:       cwd,
		SessionID: "sess-real-defaults",
		Mode:      "tui",
		HasUI:     true,
		FakeHome:  fakeHome,
	})
	h.emitEvent(sdk.EventSessionStart, nil)

	// Interactive TUI prompt advertises cfg:// and out:// under # Internal URLs.
	sysPrompt := h.emitBeforeAgentStart()
	if !strings.Contains(sysPrompt, "cfg://:") {
		t.Errorf("interactive TUI prompt missing cfg:// advertisement")
	}
	if !strings.Contains(sysPrompt, "out://:") {
		t.Errorf("interactive TUI prompt missing out:// advertisement")
	}

	// 1. Bash output of ~10 KB is NOT elided under default tools.artifactSpillThreshold (50 KB).
	bashRaw, err := h.callTool("bash", "def-bash-10kb", map[string]any{
		"i":       "Printing 10 KB output",
		"command": "seq 1 2500",
	})
	if err != nil {
		t.Fatalf("bash 10KB failed: %v", err)
	}
	bashRes := decodeToolOutcome(t, bashRaw)
	if bashRes.IsError {
		t.Fatalf("bash 10KB error: %+v", bashRes)
	}
	if len(bashRes.Content) < 10240 {
		t.Fatalf("bash 10KB output unexpectedly small (%d bytes)", len(bashRes.Content))
	}
	if strings.Contains(bashRes.Content, "elided") || strings.Contains(bashRes.Content, "local://") {
		t.Fatalf("bash 10KB output must not be elided/spilled with default 50KB threshold; got len=%d content head=%q",
			len(bashRes.Content), bashRes.Content[:min(300, len(bashRes.Content))])
	}

	// 2. Eval py and js both run with default eval.py=true, eval.js=true.
	pyRaw, err := h.callTool("eval", "def-eval-py", map[string]any{
		"language": "py",
		"code":     "print('py-default-ok', 3 + 4)",
		"title":    "Default py cell",
	})
	if err != nil {
		t.Fatalf("eval py failed: %v", err)
	}
	pyRes := decodeToolOutcome(t, pyRaw)
	if pyRes.IsError || !strings.Contains(pyRes.Content, "py-default-ok 7") {
		t.Fatalf("unexpected eval py result: %+v", pyRes)
	}

	jsRaw, err := h.callTool("eval", "def-eval-js", map[string]any{
		"language": "js",
		"code":     "console.log('js-default-ok', 5 * 6)",
		"title":    "Default js cell",
	})
	if err != nil {
		t.Fatalf("eval js failed: %v", err)
	}
	jsRes := decodeToolOutcome(t, jsRaw)
	if jsRes.IsError || !strings.Contains(jsRes.Content, "js-default-ok 30") {
		t.Fatalf("unexpected eval js result: %+v", jsRes)
	}

	// 3. web_search with PI_OFFLINE=1 answers omp's offline message without network.
	webRaw, err := h.callTool("web_search", "def-web-offline", map[string]any{
		"i":     "Checking offline web search",
		"query": "golang context",
	})
	if err != nil {
		t.Fatalf("web_search offline failed: %v", err)
	}
	webRes := decodeToolOutcome(t, webRaw)
	wantOffline := "No web search backend is available on the active model's provider under --offline."
	if !strings.Contains(webRes.Content, wantOffline) {
		t.Fatalf("web_search offline content = %q, want substring %q", webRes.Content, wantOffline)
	}

	// 4. glob, todo, ask defaults.
	globRaw, err := h.callTool("glob", "def-glob", map[string]any{
		"i":    "Listing files",
		"path": "*.txt",
	})
	if err != nil {
		t.Fatalf("glob default failed: %v", err)
	}
	globRes := decodeToolOutcome(t, globRaw)
	if globRes.IsError || !strings.Contains(globRes.Content, "sample.txt") {
		t.Fatalf("unexpected glob result: %+v", globRes)
	}

	todoRaw, err := h.callTool("todo", "def-todo", map[string]any{
		"i":     "Testing default todo",
		"op":    "init",
		"items": []string{"default task"},
	})
	if err != nil {
		t.Fatalf("todo default failed: %v", err)
	}
	if td := decodeToolOutcome(t, todoRaw); td.IsError || !strings.Contains(td.Content, "default task") {
		t.Fatalf("unexpected todo result: %+v", td)
	}

	// 5. Probe default values directly from settings / github.CacheSettingsFn.
	if err := h.callCommand("probe-defaults", ""); err != nil {
		t.Fatalf("probe-defaults: %v", err)
	}
	if !ghCache.Enabled || ghCache.SoftTTL != 300*time.Second || ghCache.HardTTL != 604800*time.Second {
		t.Errorf("github default cache TTLs = %+v, want Enabled=true SoftTTL=300s HardTTL=604800s", ghCache)
	}
	if fmt.Sprint(askTimeout) != "0" {
		t.Errorf("ask.timeout default = %v (%T), want 0", askTimeout, askTimeout)
	}
	if todoEnabled != true || todoReminders != true || fmt.Sprint(todoMax) != "3" {
		t.Errorf("todo defaults = enabled:%v reminders:%v max:%v, want true/true/3", todoEnabled, todoReminders, todoMax)
	}
	if findEnabled != "auto" || findThreshold != nil {
		t.Errorf("find defaults = enabled:%v hitThreshold:%v, want auto/<nil> (no schema default)", findEnabled, findThreshold)
	}

	// 6. Review finding 8: github cache TTL 0 is valid, fractions preserved, negatives clamp to 0.
	if err := h.callCommand("probe-github-ttl-edge", ""); err != nil {
		t.Fatalf("probe-github-ttl-edge: %v", err)
	}
	if ghCacheZeroTTL.SoftTTL != 0 || ghCacheZeroTTL.HardTTL != 0 {
		t.Errorf("github cache TTL=0 clamped wrongly: %+v, want SoftTTL=0 HardTTL=0", ghCacheZeroTTL)
	}
	if ghCacheFracTTL.SoftTTL != 1500*time.Millisecond || ghCacheFracTTL.HardTTL != 0 {
		t.Errorf("github cache TTL frac/neg = %+v, want SoftTTL=1.5s HardTTL=0", ghCacheFracTTL)
	}

	h.emitEvent(sdk.EventSessionShutdown, map[string]any{"reason": "exit"})
}

// Joined test with the REAL internal/settings store and per-key overrides
// proving every connected override reaches its target area, including the
// two security-sensitive bash.patterns / bash.allowedEnvVars cases from
// review finding 1.
func TestExtensionRealSettingsOverridesAndBashPatternAuthority(t *testing.T) {
	t.Setenv("PI_NO_INTENT", "")
	t.Setenv("PI_INTENT_TRACING", "")
	t.Setenv("PI_OFFLINE", "")
	t.Setenv("PIG_OFFLINE", "")
	// Clear search provider API keys so providers.webKeyless=false hits the
	// keyless-disabled branch deterministically with zero network traffic.
	for _, envKey := range []string{
		"PARALLEL_API_KEY", "EXA_API_KEY", "FIRECRAWL_API_KEY", "PERPLEXITY_API_KEY",
		"TINYFISH_API_KEY", "JINA_API_KEY", "KAGI_API_KEY", "TAVILY_API_KEY",
		"BRAVE_API_KEY", "DEEPSEEK_API_KEY", "KIMI_API_KEY", "SYNTHETIC_API_KEY",
		"OLLAMA_API_KEY", "SEARXNG_ENDPOINT", "SEARXNG_URL",
	} {
		t.Setenv(envKey, "")
	}

	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("USERPROFILE", fakeHome)
	t.Setenv("PIG_HOME", filepath.Join(fakeHome, ".pig"))
	t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(fakeHome, ".pig", "agent"))

	// Global (operator) config: allow `npm test*`, prompt everything else.
	agentDir := filepath.Join(fakeHome, ".pig", "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	globalCfg := strings.Join([]string{
		"bash:",
		"  patterns:",
		"    - match: \"npm test*\"",
		"      approval: allow",
		"    - match: \"*\"",
		"      approval: prompt",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(agentDir, "config.yml"), []byte(globalCfg), 0o644); err != nil {
		t.Fatal(err)
	}

	// Trusted project config:
	//   1. Tries to add LD_PRELOAD to bash.allowedEnvVars (MUST NOT loosen operator rule).
	//   2. Adds a project-layer deny rule for `echo projdeny*` (MUST be enforced).
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".pig"), 0o755); err != nil {
		t.Fatal(err)
	}
	projectCfg := strings.Join([]string{
		"bash:",
		"  allowedEnvVars:",
		"    - LD_PRELOAD",
		"  patterns:",
		"    - match: \"echo projdeny*\"",
		"      approval: deny",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(cwd, ".pig", "config.yml"), []byte(projectCfg), 0o644); err != nil {
		t.Fatal(err)
	}

	var promptedTitles []string
	ext := Extension()
	var applyErr error
	ext.RegisterCommand("apply-overrides", sdk.CommandOptions{
		Description: "apply session overrides",
		Handler: func(ctx sdk.Context, _ string) error {
			for _, kv := range []struct {
				k string
				v any
			}{
				{"eval.py", false},
				{"async.enabled", false},
				{"tools.intentTracing", false},
				{"providers.webKeyless", false},
			} {
				if err := settings.Set(ctx, kv.k, kv.v, false); err != nil {
					applyErr = err
					return err
				}
			}
			return nil
		},
	})

	h := startFakeHost(t, ext, fakeHostOptions{
		Cwd:            cwd,
		SessionID:      "sess-real-overrides",
		Mode:           "tui",
		HasUI:          true,
		FakeHome:       fakeHome,
		ProjectTrusted: true,
		SelectAnswer: func(title string, _ []string) (string, bool) {
			promptedTitles = append(promptedTitles, title)
			return "Deny", true
		},
	})
	h.emitEvent(sdk.EventSessionStart, nil)

	// Before overrides: prompt has intent line and read/bash/task schemas carry `i`.
	beforePrompt := h.emitBeforeAgentStart()
	if !strings.Contains(beforePrompt, "Most tools take `i`") {
		t.Fatalf("expected intent line in prompt before override")
	}
	for _, name := range []string{"read", "bash", "task"} {
		var obj struct {
			Properties json.RawMessage `json:"properties"`
		}
		_ = json.Unmarshal(h.tool(name).Parameters, &obj)
		if keys := jsonObjectKeys(t, obj.Properties); len(keys) == 0 || keys[0] != intent.Field {
			t.Fatalf("tool %s expected i first before override, got %v", name, keys)
		}
	}

	// Security case 1 (review finding 1): trusted project `bash.allowedEnvVars: [LD_PRELOAD]`
	// MUST NOT loosen operator `bash.patterns: [allow "npm test*", prompt "*"]`.
	// Calling `LD_PRELOAD=/tmp/x.so npm test` must trigger an approval prompt (which our
	// SelectAnswer denies), NOT run unprompted.
	promptedTitles = nil
	_, envErr := h.callTool("bash", "ov-bash-ldpreload", map[string]any{
		"i":       "Testing env allowlist authority",
		"command": "LD_PRELOAD=/tmp/x.so npm test",
	})
	if envErr == nil {
		t.Fatalf("LD_PRELOAD=/tmp/x.so npm test ran unprompted; project bash.allowedEnvVars loosened operator rule")
	}
	if len(promptedTitles) == 0 {
		t.Fatalf("expected approval prompt for LD_PRELOAD=/tmp/x.so npm test, got err=%v with zero prompts", envErr)
	}

	// Security case 2 (review finding 1): project-layer deny rule `echo projdeny*`
	// MUST be enforced without prompting.
	promptedTitles = nil
	_, denyErr := h.callTool("bash", "ov-bash-projdeny", map[string]any{
		"i":       "Testing project deny tightening",
		"command": "echo projdeny-secret",
	})
	if denyErr == nil {
		t.Fatalf("echo projdeny-secret was allowed; project bash.patterns deny rule was not enforced")
	}
	if len(promptedTitles) != 0 {
		t.Fatalf("project deny rule should deny immediately without prompting, got prompts=%v", promptedTitles)
	}

	// Apply session overrides: eval.py=false, async.enabled=false,
	// tools.intentTracing=false, providers.webKeyless=false.
	if err := h.callCommand("apply-overrides", ""); err != nil || applyErr != nil {
		t.Fatalf("apply-overrides failed: cmd=%v set=%v", err, applyErr)
	}

	// Next run (`before_agent_start`): `tools.intentTracing=false` removes BOTH
	// the prompt line AND `i` from wire tool schemas (`read`, `bash`, `task`).
	afterPrompt := h.emitBeforeAgentStart()
	if strings.Contains(afterPrompt, "Most tools take `i`") {
		t.Errorf("prompt still advertises `i` after tools.intentTracing=false")
	}
	for _, name := range []string{"read", "bash", "task"} {
		var obj struct {
			Properties json.RawMessage `json:"properties"`
		}
		_ = json.Unmarshal(h.tool(name).Parameters, &obj)
		if keys := jsonObjectKeys(t, obj.Properties); slices.Contains(keys, intent.Field) {
			t.Errorf("tool %s still advertises %q after tools.intentTracing=false + before_agent_start: %v", name, intent.Field, keys)
		}
	}

	// eval.py=false stops a py cell.
	pyRaw, pyErr := h.callTool("eval", "ov-eval-py", map[string]any{
		"language": "py",
		"code":     "print('should-not-run')",
		"title":    "Blocked py cell",
	})
	var pyMsg string
	if pyErr != nil {
		pyMsg = pyErr.Error()
	} else if res := decodeToolOutcome(t, pyRaw); res.IsError {
		pyMsg = res.Content
	}
	if pyMsg == "" || !strings.Contains(strings.ToLower(pyMsg), "python") && !strings.Contains(strings.ToLower(pyMsg), "disabled") {
		t.Errorf("expected eval.py=false refusal, got err=%v raw=%s", pyErr, string(pyRaw))
	}

	// async.enabled=false stops `bash {async: true}`. Use an operator-allowed
	// command (`npm test`) so the approval hook lets it reach bash's async check.
	asyncRaw, asyncErr := h.callTool("bash", "ov-bash-async", map[string]any{
		"command": "npm test",
		"async":   true,
	})
	var asyncMsg string
	if asyncErr != nil {
		asyncMsg = asyncErr.Error()
	} else if res := decodeToolOutcome(t, asyncRaw); res.IsError {
		asyncMsg = res.Content
	}
	if !strings.Contains(asyncMsg, "Async bash execution is disabled") {
		t.Errorf("expected async.enabled=false refusal, got err=%v raw=%s", asyncErr, string(asyncRaw))
	}

	// providers.webKeyless=false refuses web_search with omp's keyless-off message
	// (no network attempt because all credentialed providers lack API keys).
	webRaw, webErr := h.callTool("web_search", "ov-web-nokeyless", map[string]any{
		"query": "golang",
	})
	if webErr != nil {
		t.Fatalf("web_search keyless-off call error: %v", webErr)
	}
	webRes := decodeToolOutcome(t, webRaw)
	if !strings.Contains(webRes.Content, "providers.webKeyless is off") {
		t.Errorf("expected providers.webKeyless=false message, got: %q", webRes.Content)
	}

	h.emitEvent(sdk.EventSessionShutdown, map[string]any{"reason": "exit"})
}

// Item 9: Mounted tools stay active and directly callable while hidden from
// model declarations via PiG's PrepareLoadout / HiddenDeclarations hook;
// special commands (`resolve`, `reject`, `propose`) stay inactive while the
// transport is on so direct calls answer `Tool <name> not found`; default
// declared set equals omp's exact advertised order and matches `# Tool Inventory`;
// explicit tool lists keep caller order; no empty `setActiveTools([])` is ever
// sent after session_start; and `intent.SyncSessionTools` preserves
// `PrepareLoadout` on bash, eval, grep, and glob.
func TestExtensionMountedToolsLoadoutOrderAndPrepareLoadoutPreservation(t *testing.T) {
	t.Setenv("PI_NO_INTENT", "")
	t.Setenv("PI_INTENT_TRACING", "")

	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("USERPROFILE", fakeHome)
	t.Setenv("PIG_HOME", filepath.Join(fakeHome, ".pig"))
	t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(fakeHome, ".pig", "agent"))

	cwd := t.TempDir()

	ext := Extension()
	var toggleIntentErr error
	ext.RegisterCommand("disable-intent", sdk.CommandOptions{
		Description: "disable intent tracing",
		Handler: func(ctx sdk.Context, _ string) error {
			toggleIntentErr = settings.Set(ctx, "tools.intentTracing", false, false)
			return nil
		},
	})

	h := startFakeHost(t, ext, fakeHostOptions{
		Cwd:       cwd,
		SessionID: "sess-loadout-default",
		Mode:      "tui",
		HasUI:     true,
		FakeHome:  fakeHome,
	})
	h.emitEvent(sdk.EventSessionStart, nil)

	// Clear host call log right after session_start so we can assert no empty
	// `setActiveTools([])` is ever emitted after session_start.
	h.resetCallLog()

	sysPrompt := h.emitBeforeAgentStart()

	// 1. Default declared set (active minus HiddenDeclarations) equals omp's exact
	// advertised set in omp's BUILTIN_TOOLS order (TUI includes `ask`).
	wantDeclaredTUI := []string{
		"read", "bash", "edit", "ask", "eval", "glob", "grep", "task", "wait", "todo", "web_search", "write",
	}
	gotDeclared := h.declaredToolsForModel()
	if !slices.Equal(gotDeclared, wantDeclaredTUI) {
		t.Fatalf("TUI declared tools = %v, want %v (active=%v hidden=%v)",
			gotDeclared, wantDeclaredTUI, h.activeToolNames(), h.hiddenToolsForPrompt())
	}

	// 2. `# Tool Inventory` in the system prompt equals the exact advertised set in order.
	wantInventory := "# Tool Inventory\n" + strings.Join(wantDeclaredTUI, ", ")
	if !strings.Contains(sysPrompt, wantInventory) {
		t.Fatalf("system prompt # Tool Inventory mismatch; want %q", wantInventory)
	}

	// 3. Mounted tools (`ast_edit`, `debug`, `lsp`) are hidden from model
	// declarations yet remain active and directly callable.
	gotHidden := h.hiddenToolsForPrompt()
	for _, mounted := range []string{"ast_edit", "debug", "lsp"} {
		if !slices.Contains(gotHidden, mounted) {
			t.Errorf("expected mounted tool %q in HiddenDeclarations, got %v", mounted, gotHidden)
		}
	}
	lspRaw, lspErr := h.callTool("lsp", "direct-lsp-status", map[string]any{
		"i":      "Checking LSP status directly",
		"action": "status",
	})
	if lspErr != nil {
		t.Fatalf("direct call to mounted tool `lsp` failed: %v", lspErr)
	}
	if res := decodeToolOutcome(t, lspRaw); res.IsError {
		t.Fatalf("direct `lsp` status returned error: %+v", res)
	}

	// 4. Special commands (`resolve`, `reject`, `propose`) stay inactive while
	// the transport is on so direct model calls answer `Tool <name> not found`
	// (omp tools/tool-commands.ts:237-254).
	for _, special := range []string{"resolve", "reject", "propose"} {
		_, err := h.callTool(special, "direct-"+special, map[string]any{})
		wantErr := fmt.Sprintf("Tool %s not found", special)
		if err == nil || err.Error() != wantErr {
			t.Errorf("direct call to special tool %q: err=%v, want %q", special, err, wantErr)
		}
	}

	// 5. No `setActiveTools([])` call was sent after session_start (including
	// across `before_agent_start`, `mcp_servers_change`, and `intent.SyncSessionTools`).
	h.emitEvent(sdk.EventMcpServersChange, nil)
	if err := h.callCommand("disable-intent", ""); err != nil || toggleIntentErr != nil {
		t.Fatalf("disable-intent: cmd=%v err=%v", err, toggleIntentErr)
	}
	h.emitBeforeAgentStart()

	h.mu.Lock()
	postStartSetActive := append([][]string(nil), h.setActiveToolCalls...)
	h.mu.Unlock()
	for _, call := range postStartSetActive {
		if len(call) == 0 {
			t.Errorf("unexpected empty setActiveTools([]) after session_start; calls=%v", postStartSetActive)
		}
	}

	// 6. After `intent.SyncSessionTools` re-registers tools on `before_agent_start`,
	// `PrepareLoadout` MUST remain installed on bash, eval, grep, and glob.
	for _, name := range []string{"bash", "eval", "grep", "glob"} {
		if !h.tool(name).PreparesLoadout {
			t.Errorf("tool %q lost PrepareLoadout after intent.SyncSessionTools re-registration", name)
		}
	}
	// Verify bash's PrepareLoadout still hides mounted tools after sync.
	if !slices.Contains(h.hiddenToolsForPrompt(), "lsp") {
		t.Errorf("lsp no longer hidden after intent.SyncSessionTools; hidden=%v", h.hiddenToolsForPrompt())
	}

	// 7. Print mode (headless, no UI -> `ask` removed): advertised set equals
	// omp's exact default 11-tool order `read, bash, edit, eval, glob, grep, task, wait, todo, web_search, write`.
	extPrint := Extension()
	hPrint := startFakeHost(t, extPrint, fakeHostOptions{
		Cwd:       cwd,
		SessionID: "sess-loadout-print",
		Mode:      "print",
		HasUI:     false,
		FakeHome:  fakeHome,
	})
	hPrint.emitEvent(sdk.EventSessionStart, nil)
	printPrompt := hPrint.emitBeforeAgentStart()
	wantDeclaredPrint := []string{
		"read", "bash", "edit", "eval", "glob", "grep", "task", "wait", "todo", "web_search", "write",
	}
	if got := hPrint.declaredToolsForModel(); !slices.Equal(got, wantDeclaredPrint) {
		t.Errorf("print declared tools = %v, want %v", got, wantDeclaredPrint)
	}
	wantPrintInv := "# Tool Inventory\n" + strings.Join(wantDeclaredPrint, ", ")
	if !strings.Contains(printPrompt, wantPrintInv) {
		t.Errorf("print # Tool Inventory mismatch; want %q", wantPrintInv)
	}

	// 8. Explicit tool list keeps the caller's order (via toolcmd.ExplicitToolOrder).
	prevExplicit := toolcmd.ExplicitToolOrder
	t.Cleanup(func() { toolcmd.ExplicitToolOrder = prevExplicit })
	explicitOrder := []string{"write", "grep", "read", "bash"}
	toolcmd.ExplicitToolOrder = func(ctx sdk.Context) []string {
		if sid, err := ctx.GetSessionID(); err == nil && sid == "sess-loadout-explicit" {
			return explicitOrder
		}
		return nil
	}
	extExp := Extension()
	hExp := startFakeHost(t, extExp, fakeHostOptions{
		Cwd:       cwd,
		SessionID: "sess-loadout-explicit",
		Mode:      "print",
		HasUI:     false,
		FakeHome:  fakeHome,
	})
	hExp.emitEvent(sdk.EventSessionStart, nil)
	hExp.emitBeforeAgentStart()
	gotExp := hExp.declaredToolsForModel()
	if len(gotExp) < len(explicitOrder) || !slices.Equal(gotExp[:len(explicitOrder)], explicitOrder) {
		t.Errorf("explicit tool order not preserved: got %v, want prefix %v", gotExp, explicitOrder)
	}

	h.emitEvent(sdk.EventSessionShutdown, map[string]any{"reason": "exit"})
	hPrint.emitEvent(sdk.EventSessionShutdown, map[string]any{"reason": "exit"})
	hExp.emitEvent(sdk.EventSessionShutdown, map[string]any{"reason": "exit"})
}
