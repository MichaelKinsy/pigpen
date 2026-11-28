package settings

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

// TestRealProcessHelper is a subprocess entrypoint invoked ONLY by
// TestMultiProcessSixWriters40Rounds (T1) when PIG_SETTINGS_PROC_HELPER=1.
// Using the compiled test binary itself (os.Args[0] -test.run=^TestRealProcessHelper$)
// exercises real OS processes with the exact package code without needing
// extra temp module builds or external Go toolchain downloads.
func TestRealProcessHelper(t *testing.T) {
	if os.Getenv("PIG_SETTINGS_PROC_HELPER") != "1" {
		return
	}
	agentDir := os.Getenv("HELPER_AGENT_DIR")
	key := os.Getenv("HELPER_KEY")
	valStr := os.Getenv("HELPER_VAL")
	valKind := os.Getenv("HELPER_KIND")
	iters := 1
	if s := os.Getenv("HELPER_ITERS"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			t.Fatalf("helper invalid HELPER_ITERS %q", s)
		}
		iters = n
	}
	st, err := NewStore(StoreOptions{AgentDir: agentDir})
	if err != nil {
		t.Fatalf("helper NewStore: %v", err)
	}
	for i := range iters {
		var val any = valStr
		if valKind == "int" {
			n, err := strconv.Atoi(valStr)
			if err != nil {
				t.Fatalf("helper atoi: %v", err)
			}
			val = float64(n + i)
		} else if iters > 1 {
			val = fmt.Sprintf("%s-%d", valStr, i)
		}
		if err := st.Set(key, val, true); err != nil {
			t.Fatalf("helper iter %d Set(%s=%v): %v", i, key, val, err)
		}
	}
}

// T1: Real multi-process concurrency test matching the reviewer's exact shape:
// 6 real OS processes x 40 rounds writing 6 distinct keys concurrently, starting
// with a pre-existing lock file left by a dead process -> zero lost keys, zero errors.
// Also verifies symlinked lock file refusal on Unix.
func TestMultiProcessSixWriters40Rounds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping multi-process stress test in -short mode")
	}
	ResetForTests()
	t.Cleanup(ResetForTests)

	tmp := t.TempDir()
	agentDir := filepath.Join(tmp, "agent")
	_ = os.MkdirAll(agentDir, 0o755)
	cfgPath := filepath.Join(agentDir, "config.yml")
	lockPath := cfgPath + ".lock"

	// Pre-existing lock file left behind by a dead process (PID 999999).
	if err := os.WriteFile(lockPath, []byte("pid=999999\nts=0\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Build a tiny helper binary if `go` is available? Even better: the test binary
	// itself is already compiled at os.Args[0] and executes TestRealProcessHelper
	// as a real OS process with zero extra build overhead and zero network access.
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	writers := []struct {
		key  string
		kind string
		val  func(round int) string
	}{
		{"read.defaultLimit", "int", func(r int) string { return strconv.Itoa(200 + r) }},
		{"ask.timeout", "int", func(r int) string { return strconv.Itoa(10 + r) }},
		{"todo.remindersMax", "int", func(r int) string { return strconv.Itoa(1 + r) }},
		{"lsp.requestTimeout", "int", func(r int) string { return strconv.Itoa(15 + r) }},
		{"exa.searchDelayMs", "int", func(r int) string { return strconv.Itoa(500 + r) }},
		{"theme.dark", "str", func(r int) string { return fmt.Sprintf("proc-theme-%d", r) }},
	}

	for round := range 40 {
		var wg sync.WaitGroup
		errs := make([]error, len(writers))
		outs := make([]string, len(writers))
		for i, w := range writers {
			wg.Add(1)
			go func(idx int, key, kind, val string) {
				defer wg.Done()
				cmd := exec.Command(exe, "-test.run=^TestRealProcessHelper$")
				cmd.Env = append(os.Environ(),
					"PIG_SETTINGS_PROC_HELPER=1",
					"HELPER_AGENT_DIR="+agentDir,
					"HELPER_KEY="+key,
					"HELPER_VAL="+val,
					"HELPER_KIND="+kind,
				)
				out, err := cmd.CombinedOutput()
				outs[idx] = string(out)
				errs[idx] = err
			}(i, w.key, w.kind, w.val(round))
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("round %d process %d (%s) failed: %v\noutput:\n%s", round, i, writers[i].key, err, outs[i])
			}
		}

		onDisk, ok, err := readConfigFileOrdered(cfgPath)
		if err != nil || !ok {
			t.Fatalf("round %d readConfigFileOrdered: ok=%v err=%v", round, ok, err)
		}
		for _, w := range writers {
			got, exists := getByPathOrdered(onDisk, strings.Split(w.key, "."))
			var want any = w.val(round)
			if w.kind == "int" {
				n, _ := strconv.Atoi(w.val(round))
				want = float64(n)
			}
			if !exists || !orderedValuesEqual(got, want) {
				t.Fatalf("round %d lost write for %s: got (%#v, %v), want %#v", round, w.key, got, exists, want)
			}
		}
	}

	// Verify O_NOFOLLOW symlink refusal on lock file (Unix).
	if runtime.GOOS != "windows" {
		symDir := filepath.Join(tmp, "sym-agent")
		_ = os.MkdirAll(symDir, 0o755)
		decoy := filepath.Join(tmp, "decoy.txt")
		_ = os.WriteFile(decoy, []byte("keep"), 0o600)
		_ = os.Symlink(decoy, filepath.Join(symDir, "config.yml.lock"))
		st, err := NewStore(StoreOptions{AgentDir: symDir})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Set("ask.timeout", 5, true); err == nil {
			t.Fatal("expected symlinked lock file to be refused via O_NOFOLLOW")
		}
	}
}

// T3: A child session created BEFORE its parent's store exists still reads through
// to parent overrides lazily (cycle-safe) and creates nothing for an ended parent.
func TestChildCreatedBeforeParentReadsThroughLazily(t *testing.T) {
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
		if id, err := ctx.GetSessionID(); err == nil && id == "child-first" {
			return "parent-later", true
		}
		return "", false
	}

	extC := sdk.New("child-first")
	Register(extC)
	extP := sdk.New("parent-later")
	Register(extP)

	var cDark string
	extC.Tool("c_read", "c", map[string]any{"type": "object"}, func(ctx sdk.Context, _ map[string]any) (any, error) {
		cDark = String(ctx, "theme.dark")
		return sdk.ToolResult{Content: cDark}, nil
	})
	extP.Tool("p_set", "p", map[string]any{"type": "object"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		if v, ok := params["dark"].(string); ok {
			_ = Set(ctx, "theme.dark", v, false)
		}
		return sdk.ToolResult{Content: "ok"}, nil
	})

	hC := startHost(t, extC, hostOptions{Mode: "tui", Cwd: cwd, SessionID: "child-first"})
	hP := startHost(t, extP, hostOptions{Mode: "tui", Cwd: cwd, SessionID: "parent-later"})

	// Child reads FIRST — before parent's store exists in `sessions`.
	_, _ = hC.callTool("c_read", map[string]any{})
	if cDark != "titanium" {
		t.Fatalf("initial child theme.dark = %q, want default titanium", cDark)
	}

	// Now parent sets a runtime override; child must see it on its next read!
	_, _ = hP.callTool("p_set", map[string]any{"dark": "dracula"})
	_, _ = hC.callTool("c_read", map[string]any{})
	if cDark != "dracula" {
		t.Fatalf("child created before parent did not see parent override: got %q, want dracula (T3)", cDark)
	}

	// End parent: child read must not re-create parent's entry in `sessions`.
	lifecycle.End("parent-later")
	_, _ = hC.callTool("c_read", map[string]any{})
	sessionsMu.Lock()
	_, parentRecreated := sessions["parent-later"]
	sessionsMu.Unlock()
	if parentRecreated {
		t.Fatal("child read re-created an entry for ended parent session")
	}
}

// T4: OnChange fires for EVERY live session whose effective value changed (peer
// session on /save, child on parent override, all peers on external edit refresh),
// never for an ended session, and a late call on an ended session does NOT
// re-create its entry in `sessions`.
func TestOnChangeBroadcastsToPeersChildrenExternalEditsAndSuppressesEnded(t *testing.T) {
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
	cfgPath := filepath.Join(tmp, "agent", "config.yml")

	ParentSession = func(ctx sdk.Context) (string, bool) {
		if id, err := ctx.GetSessionID(); err == nil && id == "child-t4" {
			return "parent-t4", true
		}
		return "", false
	}

	extP := sdk.New("p")
	Register(extP)
	extC := sdk.New("c")
	Register(extC)
	extPeer := sdk.New("peer")
	Register(extPeer)

	wireTool := func(ext *sdk.Extension, name string) {
		ext.Tool(name, name, map[string]any{"type": "object"}, func(ctx sdk.Context, params map[string]any) (any, error) {
			if k, ok := params["setKey"].(string); ok {
				persist, _ := params["persist"].(bool)
				_ = Set(ctx, k, params["setVal"], persist)
			}
			if k, ok := params["readKey"].(string); ok {
				_ = Any(ctx, k)
			}
			return sdk.ToolResult{Content: "ok"}, nil
		})
	}
	wireTool(extP, "op")
	wireTool(extC, "op")
	wireTool(extPeer, "op")

	hP := startHost(t, extP, hostOptions{Mode: "tui", Cwd: cwd, SessionID: "parent-t4"})
	hC := startHost(t, extC, hostOptions{Mode: "tui", Cwd: cwd, SessionID: "child-t4"})
	hPeer := startHost(t, extPeer, hostOptions{Mode: "tui", Cwd: cwd, SessionID: "peer-t4"})

	// Prime all three sessions.
	_, _ = hP.callTool("op", map[string]any{"readKey": "theme.dark"})
	_, _ = hC.callTool("op", map[string]any{"readKey": "theme.dark"})
	_, _ = hPeer.callTool("op", map[string]any{"readKey": "theme.dark"})

	var (
		mu     sync.Mutex
		events []string
	)
	remove := OnChange(func(sessionID, key string) {
		mu.Lock()
		events = append(events, sessionID+":"+key)
		mu.Unlock()
	})
	defer remove()

	takeEvents := func() []string {
		mu.Lock()
		out := append([]string(nil), events...)
		events = nil
		mu.Unlock()
		sort.Strings(out)
		return out
	}

	// 1. Parent runtime override notifies BOTH parent and child (not unrelated peer).
	_, _ = hP.callTool("op", map[string]any{"setKey": "theme.dark", "setVal": "nord", "persist": false})
	if got := takeEvents(); !reflect.DeepEqual(got, []string{"child-t4:theme.dark", "parent-t4:theme.dark"}) {
		t.Fatalf("parent override OnChange = %v, want [child-t4:theme.dark parent-t4:theme.dark]", got)
	}

	// 2. Peer /save of read.defaultLimit notifies ALL three live sessions (parent, child, peer).
	_, _ = hPeer.callTool("op", map[string]any{"setKey": "read.defaultLimit", "setVal": 555, "persist": true})
	if got := takeEvents(); !reflect.DeepEqual(got, []string{"child-t4:read.defaultLimit", "parent-t4:read.defaultLimit", "peer-t4:read.defaultLimit"}) {
		t.Fatalf("global save OnChange = %v, want all 3 sessions", got)
	}

	// 3. External edit to config.yml picked up on next read notifies all live sessions whose value changed.
	if err := os.WriteFile(cfgPath, []byte("read:\n  defaultLimit: 555\nask:\n  timeout: 22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(3 * time.Second)
	_ = os.Chtimes(cfgPath, future, future)
	_, _ = hP.callTool("op", map[string]any{"readKey": "ask.timeout"})
	if got := takeEvents(); !reflect.DeepEqual(got, []string{"child-t4:ask.timeout", "parent-t4:ask.timeout", "peer-t4:ask.timeout"}) {
		t.Fatalf("external refresh OnChange = %v, want all 3 sessions for ask.timeout", got)
	}

	// 4. End peer-t4; late write/read on peer-t4 must NOT fire OnChange and must NOT re-create its session entry.
	lifecycle.End("peer-t4")
	_, _ = hPeer.callTool("op", map[string]any{"setKey": "autoResume", "setVal": true, "persist": false})
	if got := takeEvents(); len(got) != 0 {
		t.Fatalf("ended session fired OnChange: %v", got)
	}
	sessionsMu.Lock()
	_, peerRecreated := sessions["peer-t4"]
	sessionsMu.Unlock()
	if peerRecreated {
		t.Fatal("late call on ended session re-created entry in sessions table (T4)")
	}
}

// T5: Lone-CR line endings are parsed and patched cleanly without corrupting the file.
func TestLoneCRLineEndingsParseAndPatch(t *testing.T) {
	// Reviewer's exact document: `theme:\r  dark: nord\rx: 1\r`
	src := "theme:\r  dark: nord\rx: 1\r"
	parsed, err := ParseYAML(src)
	if err != nil {
		t.Fatalf("ParseYAML lone-CR failed: %v", err)
	}
	if parsed["theme"].(map[string]any)["dark"] != "nord" || parsed["x"] != float64(1) {
		t.Fatalf("unexpected lone-CR parse: %#v", parsed)
	}

	patched, ok := patchYAMLText(src, []string{"theme", "dark"}, "titanium", false)
	if !ok {
		t.Fatal("patchYAMLText failed on lone-CR document")
	}
	reparsed, err := ParseYAML(patched)
	if err != nil {
		t.Fatalf("re-parse patched lone-CR failed: %v\npatched=%q", err, patched)
	}
	if reparsed["theme"].(map[string]any)["dark"] != "titanium" || reparsed["x"] != float64(1) {
		t.Fatalf("lone-CR patch corrupted document: %#v (raw=%q)", reparsed, patched)
	}
}

// T6: Same-size edit with restored mtime (new inode via atomic rename, or updated ctime)
// is detected by the freshness signature on Unix.
func TestSameSizeRestoredMtimeReplacementDetected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("inode/ctime stat signature check is Unix-specific")
	}
	ResetForTests()
	t.Cleanup(ResetForTests)
	tmp := t.TempDir()
	agentDir := filepath.Join(tmp, "agent")
	_ = os.MkdirAll(agentDir, 0o755)
	cfgPath := filepath.Join(agentDir, "config.yml")

	v1 := "theme:\n  dark: aaaa\n"
	v2 := "theme:\n  dark: bbbb\n" // identical byte length!
	if err := os.WriteFile(cfgPath, []byte(v1), 0o600); err != nil {
		t.Fatal(err)
	}
	fixedTime := time.Now().Add(-10 * time.Second).Truncate(time.Second)
	_ = os.Chtimes(cfgPath, fixedTime, fixedTime)

	st, err := NewStore(StoreOptions{AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	if st.String("theme.dark") != "aaaa" {
		t.Fatalf("initial theme.dark = %q", st.String("theme.dark"))
	}

	// Atomically replace with same-size content AND restore identical mtime (new inode + new ctime).
	tmpFile := filepath.Join(agentDir, "replacement.tmp")
	_ = os.WriteFile(tmpFile, []byte(v2), 0o600)
	_ = os.Chtimes(tmpFile, fixedTime, fixedTime)
	if err := os.Rename(tmpFile, cfgPath); err != nil {
		t.Fatal(err)
	}

	if got := st.String("theme.dark"); got != "bbbb" {
		t.Fatalf("same-size atomic replacement with restored mtime was missed: got %q, want bbbb (T6)", got)
	}
}

// T7: Mixed-EOL preservation, missing final newline preservation, and pre-approval
// warning BEFORE the user approves when /save will drop comments.
func TestMixedEOLNoTrailingNewlineAndPreApprovalCommentWarning(t *testing.T) {
	// 1. Mixed EOL: line 1 LF, line 2 CRLF, line 3 no trailing newline.
	mixed := "theme:\n  dark: nord\r\n  light: snow"
	patched, ok := patchYAMLText(mixed, []string{"theme", "dark"}, "titanium", false)
	if !ok {
		t.Fatal("patchYAMLText failed on mixed-EOL doc")
	}
	want := "theme:\n  dark: titanium\r\n  light: snow"
	if patched != want {
		t.Fatalf("mixed-EOL / no-final-newline not preserved:\ngot:  %q\nwant: %q", patched, want)
	}

	// 2. Appending to a file without a trailing newline does not add a trailing newline.
	noNL := "theme:\n  dark: nord"
	appended, ok := patchYAMLText(noNL, []string{"autoResume"}, true, false)
	if !ok {
		t.Fatal("patchYAMLText append failed")
	}
	if strings.HasSuffix(appended, "\n") {
		t.Errorf("appending to file without final newline added trailing newline: %q", appended)
	}

	// 3. Pre-approval warning via sdk.Context.Confirm when /save will re-serialize over comments.
	ResetForTests()
	t.Cleanup(ResetForTests)
	tmp := t.TempDir()
	t.Setenv("PIG_HOME", tmp)
	t.Setenv("PIG_CODING_AGENT_DIR", "")
	agentDir := filepath.Join(tmp, "agent")
	_ = os.MkdirAll(agentDir, 0o755)
	_ = os.WriteFile(filepath.Join(agentDir, "config.yml"), []byte("# important comment\ntheme:\n  dark: nord\n"), 0o600)

	ext := sdk.New("comment-warn")
	Register(ext)
	ext.Tool("do_save", "save", map[string]any{"type": "object"}, func(ctx sdk.Context, _ map[string]any) (any, error) {
		_, err := urls.Write(urls.Context{SDK: ctx, Cwd: ctx.Cwd()}, "cfg://modelRoles/save", `{"smol":"anthropic/fast"}`)
		if err != nil {
			return sdk.ToolResult{Content: err.Error(), IsError: true}, nil
		}
		return sdk.ToolResult{Content: "ok"}, nil
	})

	var confirmMsg string
	host := startHost(t, ext, hostOptions{
		Mode:      "tui",
		Cwd:       tmp,
		SessionID: "sess-t7",
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			if method == "ui.confirm" {
				confirmMsg, _ = args["message"].(string)
				return map[string]any{"confirmed": false}, "" // decline after seeing warning
			}
			return nil, ""
		},
	})
	_, _ = host.callTool("do_save", map[string]any{})
	if !strings.Contains(confirmMsg, "drop existing comments") {
		t.Fatalf("approval prompt did not warn BEFORE approval that comments will be dropped; message=%q", confirmMsg)
	}
}
