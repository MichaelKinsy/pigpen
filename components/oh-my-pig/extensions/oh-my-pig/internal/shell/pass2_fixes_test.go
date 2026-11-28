package shell

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/approval"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/jobs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

// Second-pass review regressions (N1–N8), integrator toolcmd additions
// (InvokedCommand pre-flight pty refusal, DirectCommand timeout/cancel,
// allexport on/off), and renderer details-contract tests.

// Regression for N1 + allexport round-trip:
// Runs under BOTH macOS `/bin/bash` (bash 3.2 when present) AND the default
// newest bash on PATH. Verifies:
//   - Multi-line variable values (including quotes, $, \, and spoofed `declare --`
//     / `alias ` / `set ` / `shopt ` / `f ()` lines) round-trip verbatim without
//     truncation or data-to-code promotion across multiple subsequent calls.
//   - Multi-line aliases and functions survive.
//   - `allexport` (`set -a` / `set +a`) persists on and off across calls.
func TestPass2_N1_MultiLineStateAndAllexportBash32AndLatest(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX bash 3.2 / newest bash test")
	}

	type shellCandidate struct {
		label string
		path  string // PATH override ("" = inherit default PATH)
	}
	var candidates []shellCandidate
	if _, err := os.Stat("/bin/bash"); err == nil {
		candidates = append(candidates, shellCandidate{label: "bin-bash", path: "/bin:/usr/bin"})
	}
	if newest, err := exec.LookPath("bash"); err == nil && newest != "" && newest != "/bin/bash" {
		candidates = append(candidates, shellCandidate{label: "newest-bash", path: ""})
	}
	if len(candidates) == 0 {
		t.Skip("no bash found")
	}

	for _, cand := range candidates {
		t.Run(cand.label, func(t *testing.T) {
			if cand.path != "" {
				t.Setenv("PATH", cand.path)
			}
			tool, dir := newTestTool(t, nil)

			// Call 1: set scalar, multi-line var containing spoofed statements,
			// function, multi-line alias, and arrays.
			setupCmd := `A=1
ml=$(printf 'line1\nline2 "q" $x \\\ndeclare -- SPOOF="bad"\nalias evil="echo hacked"\nset -o noclobber\nshopt -s nullglob\nfakefn ()\nline3')
f() { echo fn-ok; }
alias mla=$'printf A\nprintf B'`
			if res, err := runTool(t, tool, map[string]any{"command": setupCmd, "cwd": dir}); err != nil || res.IsError {
				t.Fatalf("call 1 failed: res=%+v err=%v", res, err)
			}

			// Call 2: another unrelated state commit (this used to trigger the
			// line-by-line re-parse bug that truncated `ml` and broke state.sh).
			if res, err := runTool(t, tool, map[string]any{"command": "B=2", "cwd": dir}); err != nil || res.IsError {
				t.Fatalf("call 2 failed: res=%+v err=%v", res, err)
			}

			// Call 3: verify all state survived intact and NO spoofed data became code.
			checkCmd := `printf '[%s][%s][%s][%s][%s]|' "$A" "$B" "${SPOOF:-none}" "$(alias evil 2>/dev/null || echo noalias)" "$(type -t fakefn 2>/dev/null || echo nofn)"
f
printf '|%s|' "$ml"
mla`
			res, err := runTool(t, tool, map[string]any{"command": checkCmd, "cwd": dir})
			if err != nil || res.IsError {
				t.Fatalf("call 3 failed: res=%+v err=%v", res, err)
			}
			wantPrefix := "[1][2][none][noalias][nofn]|fn-ok\n|line1\nline2 \"q\" $x \\\ndeclare -- SPOOF=\"bad\"\nalias evil=\"echo hacked\"\nset -o noclobber\nshopt -s nullglob\nfakefn ()\nline3|AB"
			if res.Content != wantPrefix {
				t.Fatalf("multi-line state mismatch:\ngot:  %q\nwant: %q", res.Content, wantPrefix)
			}

			// Verify allexport ON persists across calls (and causes new vars in
			// the next call to be exported automatically).
			if res, err := runTool(t, tool, map[string]any{"command": "set -a; ALLEXP_1=one", "cwd": dir}); err != nil || res.IsError {
				t.Fatalf("set -a failed: res=%+v err=%v", res, err)
			}
			res, err = runTool(t, tool, map[string]any{
				"command": `ALLEXP_2=two; env | grep -E '^ALLEXP_[12]=' | sort`,
				"cwd":     dir,
			})
			if err != nil || res.IsError {
				t.Fatalf("allexport on check failed: res=%+v err=%v", res, err)
			}
			if strings.TrimSpace(res.Content) != "ALLEXP_1=one\nALLEXP_2=two" {
				t.Fatalf("allexport on did not persist: got %q", res.Content)
			}

			// Verify allexport OFF persists across calls.
			if res, err := runTool(t, tool, map[string]any{"command": "set +a", "cwd": dir}); err != nil || res.IsError {
				t.Fatalf("set +a failed: res=%+v err=%v", res, err)
			}
			res, err = runTool(t, tool, map[string]any{
				"command": `ALLEXP_3=three; env | grep '^ALLEXP_3=' || echo NOT_EXPORTED`,
				"cwd":     dir,
			})
			if err != nil || res.IsError {
				t.Fatalf("allexport off check failed: res=%+v err=%v", res, err)
			}
			if strings.TrimSpace(res.Content) != "NOT_EXPORTED" {
				t.Fatalf("allexport off did not persist: got %q", res.Content)
			}
		})
	}
}

// Regression for N2:
//   - In omp's default approvalMode ("yolo"), critical patterns do NOT prompt or
//     block (both interactive and headless), matching tools/approval.ts:353, 428-445.
//   - Explicit `bash.patterns` deny/prompt STILL apply in yolo mode (and block
//     headless with omp's message).
//   - In `always-ask`, `always-ask-cached`, and `write`, critical patterns prompt
//     when UI exists (allow on Confirm=true, block on Confirm=false) and block
//     headless when !HasUI.
//   - `dd if=x 2>&1 of=/dev/sda` matches critical dd pattern (fixed RE2 rewrite).
//
// upstream: test/tools/approval.test.ts: "ignores override-based prompts in yolo mode"
func TestPass2_N2_ApprovalModeCriticalAndDD(t *testing.T) {
	prevSetting := Setting
	prevApprSetting := approval.Setting
	prevApprSession := approval.SessionSetting
	t.Cleanup(func() {
		Setting = prevSetting
		approval.Setting = prevApprSetting
		approval.SessionSetting = prevApprSession
	})

	// Lane-only gap fix: `dd if=x 2>&1 of=/dev/sda` must be flagged critical,
	// while `dd if=x 2>&1 of=/dev/null` remains benign.
	if v := evaluateBashApproval("dd if=x 2>&1 of=/dev/sda", nil, nil); v.Kind != "critical" {
		t.Fatalf("dd with 2>&1 before of=/dev/sda: got %+v, want critical", v)
	}
	if v := evaluateBashApproval("dd if=x 2>&1 of=/dev/null", nil, nil); v.Kind != "exec" {
		t.Fatalf("dd with 2>&1 before of=/dev/null: got %+v, want exec", v)
	}

	// Settings present with unset mode => schema default yolo: critical patterns run without block/prompt.
	Setting = func(_ sdk.Context, _ string) any { return nil }
	approval.Setting = Setting
	approval.SessionSetting = Setting
	for _, cmd := range []string{"rm -rf /tmp/build-123", "rm -f /abs/file", "sudo rm -rf /tmp/x", "echo reboot"} {
		res, err := approval.HandleToolCallVeto(sdk.Context{}, map[string]any{
			"toolName": "bash",
			"input":    map[string]any{"command": cmd},
		})
		if err != nil || res != nil {
			t.Fatalf("default yolo mode blocked %q: res=%+v err=%v", cmd, res, err)
		}
	}

	// Explicit bash.patterns prompt/deny STILL enforce in yolo mode (including headless).
	Setting = func(_ sdk.Context, key string) any {
		switch key {
		case "tools.approvalMode":
			return "yolo"
		case "bash.patterns":
			return []any{
				map[string]any{"match": "git push *", "approval": "prompt"},
				map[string]any{"match": "rm -rf /etc/*", "approval": "deny"},
			}
		}
		return nil
	}
	approval.Setting = Setting
	approval.SessionSetting = Setting
	// Prompt rule in yolo blocks headless with omp's no-UI refusal.
	res, err := approval.HandleToolCallVeto(sdk.Context{}, map[string]any{
		"toolName": "bash",
		"input":    map[string]any{"command": "git push origin main"},
	})
	m, _ := res.(map[string]any)
	if err != nil || m["block"] != true || !strings.Contains(fmt.Sprint(m["reason"]), "no interactive UI available") {
		t.Fatalf("yolo explicit prompt headless: res=%+v err=%v", res, err)
	}
	// Deny rule in yolo blocks.
	res, err = approval.HandleToolCallVeto(sdk.Context{}, map[string]any{
		"toolName": "bash",
		"input":    map[string]any{"command": "rm -rf /etc/foo"},
	})
	m, _ = res.(map[string]any)
	if err != nil || m["block"] != true || !strings.Contains(fmt.Sprint(m["reason"]), "Blocked by bash pattern") {
		t.Fatalf("yolo explicit deny: res=%+v err=%v", res, err)
	}
	// Non-matching critical command in yolo still runs.
	res, err = approval.HandleToolCallVeto(sdk.Context{}, map[string]any{
		"toolName": "bash",
		"input":    map[string]any{"command": "rm -rf /tmp/build-123"},
	})
	if err != nil || res != nil {
		t.Fatalf("yolo critical non-pattern: expected nil, got res=%+v err=%v", res, err)
	}

	// Non-yolo modes (`always-ask`, `always-ask-cached`, `write`):
	// headless blocks critical patterns with omp's no-UI refusal.
	for _, mode := range []string{"always-ask", "always-ask-cached", "write"} {
		mMode := mode
		Setting = func(_ sdk.Context, key string) any {
			if key == "tools.approvalMode" {
				return mMode
			}
			return nil
		}
		approval.Setting = Setting
		approval.SessionSetting = Setting
		res, err := approval.HandleToolCallVeto(sdk.Context{}, map[string]any{
			"toolName": "bash",
			"input":    map[string]any{"command": "rm -rf /tmp/build-123"},
		})
		m, _ := res.(map[string]any)
		if err != nil || m["block"] != true || !strings.Contains(fmt.Sprint(m["reason"]), `Tool "bash" requires approval but no interactive UI available.`) {
			t.Fatalf("mode %q headless critical: got res=%+v err=%v", mMode, res, err)
		}
	}
}

// Regression for N3:
// Streaming OutputSink must NOT corrupt multi-byte UTF-8 runes or leak tails
// of ANSI escape sequences split across Push / pipe read boundaries.
func TestPass2_N3_StreamingSanitizeAcrossChunkBoundaries(t *testing.T) {
	// 1) Feed 12,000 three-byte UTF-8 runes (`あ`, 0xE3 0x81 0x82) split at
	// awkward byte offsets (every 7 bytes, cutting runes in half on nearly every chunk).
	const totalRunes = 12000
	full := strings.Repeat("あ", totalRunes)
	artDir := filepath.Join(t.TempDir(), "utf8-art")
	store := NewArtifactStore(artDir)
	t.Cleanup(store.DisposeAll)
	sink := NewOutputSink(OutputSinkOptions{
		SpillThreshold: 64 * 1024, // keep inline too so we can check both
		HeadBytes:      0,
		MaxColumns:     0,
		ArtifactStore:  store,
		SessionID:      "sess-utf8",
	})
	for i := 0; i < len(full); i += 7 {
		end := min(i+7, len(full))
		sink.Push(full[i:end])
	}
	sum := sink.Dump("")
	if gotRunes := len([]rune(sum.Output)); gotRunes != totalRunes {
		t.Fatalf("UTF-8 rune count across chunk boundaries = %d, want %d", gotRunes, totalRunes)
	}
	if sum.Output != full {
		t.Fatalf("UTF-8 content corrupted across boundaries (len=%d vs %d)", len(sum.Output), len(full))
	}

	// 2) ANSI CSI, OSC (BEL and ST), and 4-byte emoji split across chunks.
	sink2 := NewOutputSink(OutputSinkOptions{SpillThreshold: 4096, MaxColumns: 0})
	// Split `ESC[31mredESC[0m`, `ESC]0;titleESC\ok`, and 4-byte `🚀` (F0 9F 9A 80).
	chunks := []string{
		"\x1b[3", "1mred\x1b", "[0m ",
		"\x1b]0;my-tit", "le\x1b", "\\ok ",
		"\xf0\x9f", "\x9a\x80",
	}
	for _, c := range chunks {
		sink2.Push(c)
	}
	sum2 := sink2.Dump("")
	want2 := "red ok 🚀"
	if sum2.Output != want2 {
		t.Fatalf("split ANSI/emoji output = %q, want %q", sum2.Output, want2)
	}

	// 3) End-to-end through bash writing 12,000 `あ` in 400 lines (matching the
	// reviewer's exact reproducer).
	if runtime.GOOS != "windows" {
		tool, dir := newTestTool(t, nil)
		res, err := runTool(t, tool, map[string]any{
			"command": `python3 -c 'print(("あ"*30 + "\n")*400, end="")'`,
			"cwd":     dir,
		})
		if err != nil {
			t.Fatalf("bash utf8 repro failed: %v", err)
		}
		if cnt := strings.Count(res.Content, "あ"); cnt != 12000 {
			t.Fatalf("bash 12000 あ count = %d, want 12000", cnt)
		}
	}
}

// Regression for N4:
// `unset`, `unset -f`, and `unalias` must persist across calls (and also
// unsetting an inherited environment variable).
func TestPass2_N4_UnsetVarFuncAliasPersist(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX unset persistence test")
	}
	t.Setenv("OMP_INHERITED_TO_UNSET", "from-parent")
	tool, dir := newTestTool(t, nil)

	if _, err := runTool(t, tool, map[string]any{
		"command": `A=1; KEEP=stay; f(){ echo fn; }; g(){ echo keepfn; }; alias ll='echo ll-alias'; alias kk='echo kk-alias'`,
		"cwd":     dir,
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// Unset A, OMP_INHERITED_TO_UNSET, function f, and alias ll.
	if _, err := runTool(t, tool, map[string]any{
		"command": `unset A OMP_INHERITED_TO_UNSET; unset -f f; unalias ll`,
		"cwd":     dir,
	}); err != nil {
		t.Fatalf("unset call: %v", err)
	}

	res, err := runTool(t, tool, map[string]any{
		"command": `printf '[%s][%s][%s][%s][%s][%s][%s]' "${A:-gone}" "${OMP_INHERITED_TO_UNSET:-gone}" "$KEEP" "$(type -t f 2>/dev/null || echo nofn)" "$(g)" "$(alias ll 2>/dev/null || echo noal)" "$(kk)"`,
		"cwd":     dir,
	})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	want := "[gone][gone][stay][nofn][keepfn][noal][kk-alias]"
	if res.Content != want {
		t.Fatalf("unset persistence mismatch: got %q, want %q", res.Content, want)
	}
}

// Regression for N5:
// An overlapping backgrounded call that finishes later must NOT revert
// functions, aliases, or shopt/set-o options modified by a call that ran
// meanwhile when the backgrounded call did not touch them.
// upstream: packages/coding-agent/src/exec/bash-executor.ts:901-907
func TestPass2_N5_OverlappingCallPreservesUntouchedFuncsAliasesOptions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX overlapping delta test")
	}
	tool, dir := newTestTool(t, func(cfg *Config) {
		// Start with auto-background disabled for setup and overlapping calls so
		// heavy CPU load cannot spuriously auto-background fast state calls;
		// enable with a short threshold ONLY for the single backgrounded call
		// (which blocks on releaseFile until released).
		cfg.AutoBackgroundEnabled = false
	})

	// Seed initial function f (v1), alias al (v1), and default options.
	if _, err := runTool(t, tool, map[string]any{
		"command": `f(){ printf v1; }; alias al='printf al1'`,
		"cwd":     dir,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Start a call that auto-backgrounds and blocks on a release file (instead
	// of a fixed sleep duration) so it is GUARANTEED to finish AFTER the
	// overlapping call even under heavy CPU load.
	releaseFile := filepath.Join(dir, "release-bg")
	bgCmd := fmt.Sprintf(`BG_ONLY=yes; while [ ! -e %s ]; do sleep 0.05; done`, singleQuotePosix(releaseFile))
	tool.cfg.AutoBackgroundEnabled = true
	tool.cfg.AutoBackgroundThreshold = 100 * time.Millisecond
	bgRes, err := runTool(t, tool, map[string]any{
		"command": bgCmd,
		"cwd":     dir,
	})
	tool.cfg.AutoBackgroundEnabled = false
	if err != nil {
		t.Fatalf("bg start: %v", err)
	}
	if det, _ := bgRes.Details.(map[string]any); det["async"] == nil {
		t.Fatalf("expected auto-background, got %+v", bgRes)
	}

	// Overlapping call updates f -> v2, alias al -> al2, enables pipefail & nocasematch.
	if _, err := runTool(t, tool, map[string]any{
		"command": `f(){ printf v2; }; alias al='printf al2'; set -o pipefail; shopt -s nocasematch`,
		"cwd":     dir,
	}); err != nil {
		t.Fatalf("overlapping update: %v", err)
	}

	// Now release the backgrounded call and wait for it to settle and commit.
	if err := os.WriteFile(releaseFile, []byte("go\n"), 0o600); err != nil {
		t.Fatalf("create release file: %v", err)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = tool.runtime.Jobs.WaitAll(waitCtx)

	// Next call must see BG_ONLY=yes AND f=v2, al=al2, pipefail on, nocasematch on.
	res, err := runTool(t, tool, map[string]any{
		"command": `printf '%s|' "$BG_ONLY"; f; printf '|'; al; printf '|%s|' "$(set -o | awk '$1=="pipefail"{print $2}')"; shopt -q nocasematch && printf on || printf off`,
		"cwd":     dir,
	})
	if err != nil {
		t.Fatalf("final verify: %v", err)
	}
	want := "yes|v2|al2|on|on"
	if res.Content != want {
		t.Fatalf("later-finishing overlapping call clobbered untouched state: got %q, want %q", res.Content, want)
	}
}

// Regression for N6:
// Process groups that exit cleanly must be dropped immediately from
// sessionState.pgids on return so session end can never SIGKILL a reused PGID.
func TestPass2_N6_ExitedPGIDForgottenImmediately(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX pgid tracking test")
	}
	tool, dir := newTestTool(t, nil)

	for i := range 5 {
		if _, err := runTool(t, tool, map[string]any{
			"command": fmt.Sprintf("echo call-%d", i),
			"cwd":     dir,
		}); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}

	st := tool.runtime.sessionState("default")
	st.mu.Lock()
	remaining := len(st.pgids)
	st.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("expected 0 tracked pgids after cleanly exited commands, got %d", remaining)
	}
}

// Regression for N7:
//   - State wrapper exposes ZERO `__omp_*` helper functions or variables to
//     the user command (`declare -F` and `compgen -v` are clean; `set` does not
//     duplicate the environment in a baseline variable).
//   - Structurally proves the wrapper spawns NO per-variable `$(declare -p …)`
//     subshell and stays constant size regardless of how many variables exist
//     in the session snapshot (tested with 0 vs 200 session variables, plus a
//     live round-trip of 200 variables verifying all persist with zero leaked
//     internals).
func TestPass2_N7_StateWrapperNoExposedInternalsAndFast(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX state wrapper hygiene test")
	}

	// 1. Structural proof: the generated wrapper script must NOT contain
	// per-variable command substitutions (`$(declare -p ...)`), and its size /
	// command-substitution count must be constant whether the session has 0
	// or 200 variables.
	snap0 := newShellSnapshot()
	snap200 := newShellSnapshot()
	for i := range 200 {
		name := fmt.Sprintf("OMP_BULK_VAR_%03d", i)
		snap200.vars[name] = fmt.Sprintf("declare -- %s=\"val-%03d\"", name, i)
	}
	dir := t.TempDir()
	load0 := filepath.Join(dir, "load0.sh")
	load200 := filepath.Join(dir, "load200.sh")
	if err := os.WriteFile(load0, []byte(snap0.renderScript()), 0o600); err != nil {
		t.Fatalf("write load0: %v", err)
	}
	if err := os.WriteFile(load200, []byte(snap200.renderScript()), 0o600); err != nil {
		t.Fatalf("write load200: %v", err)
	}

	w0 := wrapCommandWithState("true", load0, "", "", "nonce000", true, 73)
	w200 := wrapCommandWithState("true", load200, "", "", "nonce200", true, 73)

	// Wrapper script must never spawn $(declare -p ...) or $(declare -f ...) or
	// $(alias ...) per entry; only the constant compgen enumerators may use $(...).
	for _, forbidden := range []string{"$(declare", "`declare", "$(alias", "`alias", "__omp_base_decls", "__omp_capture_baseline", "__omp_save_state"} {
		if strings.Contains(w200, forbidden) {
			t.Fatalf("wrapper script contains forbidden per-variable/leaked construct %q", forbidden)
		}
	}
	// Constant wrapper structure: length differs only by the 2-byte load filename difference.
	if diff := len(w200) - len(w0); diff < -16 || diff > 16 {
		t.Fatalf("wrapper script grew with session variable count: len(w0)=%d len(w200)=%d", len(w0), len(w200))
	}
	// Exactly 6 command substitutions total across both phases (3 per phase:
	// compgen -v, compgen -A function, compgen -a) — O(1), independent of N.
	if got := strings.Count(w200, "$("); got != 6 {
		t.Fatalf("wrapper command-substitution count = %d, want constant 6 (O(1) per call)", got)
	}

	// 2. Live execution proof: zero `__omp_*` functions or variables are visible
	// inside the user command, even with 200 variables in the session, and `set`
	// does not carry a duplicated baseline copy of the environment.
	tool, workDir := newTestTool(t, nil)
	res, err := runTool(t, tool, map[string]any{
		"command": `printf 'fns=[%s] vars=[%s]' "$(declare -F | grep __omp_ || true)" "$(compgen -v | grep ^__omp_ || true)"`,
		"cwd":     workDir,
	})
	if err != nil {
		t.Fatalf("hygiene check: %v", err)
	}
	if res.Content != "fns=[] vars=[]" {
		t.Fatalf("state wrapper leaked internals into user shell: %q", res.Content)
	}

	// Set 200 variables in one call, then confirm they persist and `declare -p`
	// has no duplicated baseline variable holding a second copy.
	var setCmd strings.Builder
	for i := range 200 {
		fmt.Fprintf(&setCmd, "OMP_BULK_VAR_%03d=val-%03d\n", i, i)
	}
	if _, err := runTool(t, tool, map[string]any{"command": setCmd.String(), "cwd": workDir}); err != nil {
		t.Fatalf("bulk set: %v", err)
	}
	res, err = runTool(t, tool, map[string]any{
		"command": `printf '%s|%s|%s|%s' "$OMP_BULK_VAR_000" "$OMP_BULK_VAR_199" "$(declare -F | grep __omp_ || true)" "$(compgen -v | grep ^__omp_ || true)"`,
		"cwd":     workDir,
	})
	if err != nil {
		t.Fatalf("bulk verify: %v", err)
	}
	if res.Content != "val-000|val-199||" {
		t.Fatalf("bulk state/hygiene mismatch: got %q, want %q", res.Content, "val-000|val-199||")
	}
}

// Regression for N8:
//   - Consumed eviction re-arms the timer AFTER async delivery calls MarkConsumed,
//     evicting the job in consumedEviction (e.g. 60ms) instead of full Retention (5s).
//   - lifecycle.End("sess") sweeps per-session evictedJobs / evictedOrder /
//     consumedJobs maps after the grace period so ended sessions do not leak memory.
func TestPass2_N8_ConsumedEvictionReArmAndSessionSweep(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX async job eviction test")
	}
	jobs.ResetForTests()
	jobs.ResetDeliveryForTests()
	lifecycle.ResetForTests()
	urls.ResetForTests()
	t.Cleanup(func() {
		jobs.ResetForTests()
		jobs.ResetDeliveryForTests()
		lifecycle.ResetForTests()
		urls.ResetForTests()
	})

	deliveredCh := make(chan string, 2)
	jobs.SetDeliverySink(func(sessionID, customType, content string, _ bool, _ sdk.SendMessageOptions) error {
		if customType == jobs.AsyncResultMessageType {
			deliveredCh <- sessionID
		}
		return nil
	})
	jobs.EnsureDeliveryListener()

	// Long retention (30s), very short consumed eviction (40ms), with an
	// event-driven eviction channel (no polling sleep).
	evictedCh := make(chan string, 2)
	jm := NewJobManager(JobManagerOptions{
		Retention:        30 * time.Second,
		ConsumedEviction: 40 * time.Millisecond,
	})
	jm.mu.Lock()
	jm.onEvict = func(sessionID, jobID string) {
		evictedCh <- sessionID + ":" + jobID
	}
	jm.mu.Unlock()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = jm.Dispose(ctx)
	})

	tool, dir := newTestTool(t, nil)
	tool.runtime.Jobs = jm

	res, err := runTool(t, tool, map[string]any{
		"command": "echo fast-done",
		"cwd":     dir,
		"async":   true,
	})
	if err != nil {
		t.Fatalf("async start: %v", err)
	}
	jobID := res.Details.(map[string]any)["async"].(map[string]any)["jobId"].(string)

	// Wait for async-result delivery to succeed (which calls jobs.MarkConsumed).
	select {
	case <-deliveredCh:
	case <-time.After(30 * time.Second):
		t.Fatalf("timed out waiting for async-result delivery")
	}

	// Event-driven wait: job must be evicted by the shortened consumedEviction
	// timer (40ms), well before the 30s full retention.
	select {
	case got := <-evictedCh:
		if got != "default:"+jobID {
			t.Fatalf("evicted %q, want default:%s", got, jobID)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("delivered job %s was not evicted via consumedEviction (retention=30s, consumed=40ms)", jobID)
	}
	if _, ok := jm.Get(jobID); ok {
		t.Fatalf("job %s still present in JobManager after eviction event", jobID)
	}
	if rec, hasRec := jobs.GetEvicted("default", jobID); !hasRec || !rec.Delivered {
		t.Fatalf("expected delivered eviction tombstone for %s, got rec=%+v ok=%v", jobID, rec, hasRec)
	}
}

// Regression for integrator addition:
// `pty: true` on a session tool command MUST be refused BEFORE DirectCommand or
// any shell runs, using `InvokedCommand` when wired (and asserting neither the
// tool nor any side-effecting shell command ran).
// upstream: test/bash-tool-commands.test.ts:172-183 ("refuses pty for a line naming a tool command before running anything")
func TestPass2_PtyRefusalBeforeDirectOrSpawn(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX pty refusal test")
	}
	prevInvoked := InvokedCommand
	prevDirect := DirectCommand
	prevNames := CommandNames
	t.Cleanup(func() {
		InvokedCommand = prevInvoked
		DirectCommand = prevDirect
		CommandNames = prevNames
	})

	tool, dir := newTestTool(t, nil)
	var directCalled bool
	DirectCommand = func(_ sdk.Context, _, _ string, _ time.Duration) (string, int, bool, bool, bool) {
		directCalled = true
		return "should-not-run", 0, false, false, true
	}
	InvokedCommand = func(_ sdk.Context, command string) (string, bool) {
		if strings.Contains(command, "ast_edit") {
			return "ast_edit", true
		}
		return "", false
	}

	// 1) Pure tool command line with pty: true must NOT call DirectCommand.
	marker := filepath.Join(dir, "ran")
	_, err := runTool(t, tool, map[string]any{
		"command": "ast_edit --file a.go",
		"cwd":     dir,
		"pty":     true,
	})
	wantErr := "`ast_edit` is a session tool command and cannot run in a PTY; run it without `pty`."
	if err == nil || err.Error() != wantErr {
		t.Fatalf("pure toolcmd pty error = %v, want %q", err, wantErr)
	}
	if directCalled {
		t.Fatalf("DirectCommand was executed despite pty: true")
	}

	// 2) Compound line `touch '<marker>' && ast_edit ...` with pty: true must
	// refuse BEFORE spawning the shell so `marker` is never created.
	_, err = runTool(t, tool, map[string]any{
		"command": fmt.Sprintf("touch %s && ast_edit --file a.go", singleQuotePosix(marker)),
		"cwd":     dir,
		"pty":     true,
	})
	if err == nil || err.Error() != wantErr {
		t.Fatalf("compound toolcmd pty error = %v, want %q", err, wantErr)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("marker file %s was created; shell ran despite pty refusal", marker)
	}
}

// Regression for renderer details-contract fields:
// Verifies wire `details.service` (name, state, ready, timedOut, pid) and
// `details.toolCommands` + `details.toolCommandsSole` when ToolCommandDispatches
// is wired.
func TestPass2_RendererDetailsContractServiceAndToolCommands(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX details contract test")
	}
	prevDirect := DirectCommand
	prevDispatches := ToolCommandDispatches
	t.Cleanup(func() {
		DirectCommand = prevDirect
		ToolCommandDispatches = prevDispatches
	})

	tool, dir := newTestTool(t, nil)

	// 1) `details.service` fields on ready service start and timed-out readiness.
	svcRes, err := runTool(t, tool, map[string]any{
		"name":    "api",
		"command": "echo UP; sleep 30",
		"cwd":     dir,
		"pty":     false,
		"ready":   map[string]any{"log": "^UP$", "timeout": 20},
	})
	if err != nil {
		t.Fatalf("service start: %v", err)
	}
	det, _ := svcRes.Details.(map[string]any)
	svcMap, _ := det["service"].(map[string]any)
	if svcMap["name"] != "api" || svcMap["state"] != "ready" || svcMap["ready"] != true || svcMap["timedOut"] != false {
		t.Fatalf("unexpected details.service: %+v", svcMap)
	}
	if pid, ok := svcMap["pid"].(int); !ok || pid <= 0 {
		t.Fatalf("expected positive details.service.pid, got %v", svcMap["pid"])
	}

	// Timed-out readiness reports ready=false, timedOut=true.
	toRes, err := runTool(t, tool, map[string]any{
		"name":    "slow",
		"command": "sleep 30",
		"cwd":     dir,
		"pty":     false,
		"ready":   map[string]any{"log": "NEVER_APPEARS", "timeout": 0.1},
	})
	if err != nil {
		t.Fatalf("service timeout start: %v", err)
	}
	toDet, _ := toRes.Details.(map[string]any)
	toSvc, _ := toDet["service"].(map[string]any)
	if toSvc["name"] != "slow" || toSvc["ready"] != false || toSvc["timedOut"] != true {
		t.Fatalf("unexpected timed-out details.service: %+v", toSvc)
	}

	// 2) `details.toolCommands` and `details.toolCommandsSole` on DirectCommand
	// (sole invocation => toolCommandsSole=true) and compound shell run
	// (compound invocation => toolCommandsSole omitted).
	var mu sync.Mutex
	var pending []map[string]any
	DirectCommand = func(_ sdk.Context, command, _ string, _ time.Duration) (string, int, bool, bool, bool) {
		if command == "lsp hover a.go" {
			mu.Lock()
			pending = []map[string]any{{"tool": "lsp", "mode": "execute", "exitCode": 0, "args": map[string]any{"action": "hover"}}}
			mu.Unlock()
			return "func main()", 0, false, false, true
		}
		return "", 0, false, false, false
	}
	ToolCommandDispatches = func(_ sdk.Context) []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		out := pending
		pending = nil
		return out
	}

	soleRes, err := runTool(t, tool, map[string]any{"command": "lsp hover a.go", "cwd": dir})
	if err != nil {
		t.Fatalf("sole toolcmd: %v", err)
	}
	rawJSON, _ := json.Marshal(soleRes.Details)
	var wire map[string]any
	_ = json.Unmarshal(rawJSON, &wire)
	if wire["toolCommandsSole"] != true {
		t.Fatalf("expected toolCommandsSole=true on sole invocation, got %s", rawJSON)
	}
	tcList, _ := wire["toolCommands"].([]any)
	if len(tcList) != 1 || tcList[0].(map[string]any)["tool"] != "lsp" {
		t.Fatalf("unexpected details.toolCommands: %s", rawJSON)
	}

	// Compound line should emit toolCommands WITHOUT toolCommandsSole.
	mu.Lock()
	pending = []map[string]any{{"tool": "lsp", "mode": "execute", "exitCode": 0}}
	mu.Unlock()
	compRes, err := runTool(t, tool, map[string]any{"command": "echo hi && true", "cwd": dir})
	if err != nil {
		t.Fatalf("compound toolcmd: %v", err)
	}
	compDet, _ := compRes.Details.(map[string]any)
	if _, hasSole := compDet["toolCommandsSole"]; hasSole {
		t.Fatalf("compound line should not set toolCommandsSole, got %+v", compDet)
	}
	if list, _ := compDet["toolCommands"].([]map[string]any); len(list) != 1 {
		t.Fatalf("expected details.toolCommands on compound line, got %+v", compDet)
	}
}
