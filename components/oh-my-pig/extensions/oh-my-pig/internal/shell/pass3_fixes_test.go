package shell

import (
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/approval"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
)

// Third-pass review regressions (T1–T5). T6 is tested in internal/jobs/delivery_test.go.

// Regression for T1:
// On platforms without ExtraFiles pipes (Windows file-fallback path), the
// wrapper MUST NEVER redirect the full BASE dump (which contains inherited
// environment secrets) to the save file. Instead it keeps BASE in memory and
// diffs BASE vs POST inside bash via awk, writing ONLY changed/added/removed
// records to saveFile.
//
// Runs on EVERY OS: structurally verifies the generated file-fallback wrapper,
// AND executes the file-fallback wrapper under host bash (when not Windows) to
// prove inherited secrets never appear in saveFile while modified/multi-line
// vars, unsets, functions, aliases, and options round-trip through parseCapturedStateStream.
func TestPass3_T1_WindowsFallbackNeverWritesInheritedEnvToDisk(t *testing.T) {
	dir := t.TempDir()
	saveFile := filepath.Join(dir, "save-win.sh")
	nonce := "win12345"

	script := wrapCommandWithState(
		`NEW_VAR=hello; ML=$(printf 'a\nb'); unset INH_UNSET; f_win(){ echo wf; }; alias al_win='echo wa'; set -o pipefail`,
		"",
		saveFile,
		"",
		nonce,
		false, // Windows file-fallback mode
		0,
	)

	// 1. Structural assertions (run on every OS including Linux/macOS/Windows):
	// BASE capture must go into in-memory `__omp_win_base=$(...)`, NOT `>| saveFile`,
	// marked `readonly` so commands cannot clear it, and guarded by a `case` check
	// for the base end marker so an empty/corrupted base refuses to write to disk (T1, U3).
	if !strings.Contains(script, "__omp_win_base=$(") {
		t.Fatalf("expected Windows wrapper to capture BASE into in-memory __omp_win_base, got:\n%s", script)
	}
	if strings.Contains(script, ") >| "+singleQuotePosix(saveFile)) {
		t.Fatalf("Windows wrapper redirected full BASE dump directly to saveFile")
	}
	if !strings.Contains(script, "readonly __omp_win_base") {
		t.Fatalf("Windows wrapper must mark __omp_win_base readonly (U3)")
	}
	endBase := "#__OMP_END_BASE_" + nonce + "__"
	if !strings.Contains(script, "(*"+endBase+"*)") {
		t.Fatalf("Windows wrapper trap must refuse to write when __omp_win_base lacks end marker %q (U3)", endBase)
	}
	if !strings.Contains(script, "awk ") || !strings.Contains(script, "#__OMP_DELTA_"+nonce+"__") {
		t.Fatalf("Windows wrapper missing in-bash awk delta filter")
	}
	// proves zero inherited secrets reach saveFile AND parsed delta is exact.
	if runtime.GOOS == "windows" {
		return
	}
	bashPath, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not found")
	}
	cmd := exec.Command(bashPath, "-c", script)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"OMP_WIN_TEST_SECRET=top-secret-api-key-99999",
		"INH_UNSET=will-be-removed",
	)
	if out, runErr := cmd.CombinedOutput(); runErr != nil {
		t.Fatalf("file-fallback wrapper failed: %v\nout=%s", runErr, out)
	}

	raw, rErr := os.ReadFile(saveFile)
	if rErr != nil {
		t.Fatalf("read saveFile: %v", rErr)
	}
	content := string(raw)
	if strings.Contains(content, "top-secret-api-key-99999") || strings.Contains(content, "OMP_WIN_TEST_SECRET") {
		t.Fatalf("inherited secret leaked into Windows fallback saveFile:\n%s", content)
	}
	delta, complete := parseCapturedStateStream(content, nonce)
	if !complete {
		t.Fatalf("expected complete delta parse from saveFile, got:\n%s", content)
	}
	if _, ok := delta.setVars["NEW_VAR"]; !ok {
		t.Fatalf("NEW_VAR missing from Windows delta: %+v", delta.setVars)
	}
	if _, ok := delta.setVars["ML"]; !ok {
		t.Fatalf("multi-line ML missing from Windows delta: %+v", delta.setVars)
	}
	if !delta.unsetVars["INH_UNSET"] {
		t.Fatalf("INH_UNSET missing from Windows delta.unsetVars: %+v", delta.unsetVars)
	}
	if _, ok := delta.setFuncs["f_win"]; !ok {
		t.Fatalf("f_win missing from Windows delta.setFuncs: %+v", delta.setFuncs)
	}
	if _, ok := delta.setAliases["al_win"]; !ok {
		t.Fatalf("al_win missing from Windows delta.setAliases: %+v", delta.setAliases)
	}
	if !delta.setChange["pipefail"] {
		t.Fatalf("pipefail missing from Windows delta.setChange: %+v", delta.setChange)
	}
}

// Regression for T2:
// Stateful streamSanitizer MUST match whole-input sanitizeText across arbitrary
// random split boundaries for escape sequences of ANY length, including:
//   - OSC 8 hyperlinks with >400-byte URLs (previously leaked in 2934/3000 splits)
//   - Multi-kilobyte Sixel-style DCS sequences (previously leaked in 3000/3000 splits)
//   - Long APC/PM/SOS sequences, CSI, charset designations, 4-byte UTF-8, CR/CRLF,
//     and invalid UTF-8 bytes.
func TestPass3_T2_LongEscapeSequenceSplitFuzzMatchesWholeSanitize(t *testing.T) {
	longURL := "https://example.com/" + strings.Repeat("a/", 220) + "target"
	osc8Long := "see \x1b]8;;" + longURL + "\x1b\\link\x1b]8;;\x1b\\ end"
	osc8Bel := "before \x1b]8;;" + longURL + "\x07click\x1b]8;;\x07 after"
	sixelDCS := "img \x1bP0;1q" + strings.Repeat("#0;2;0;0;0~~@@vv", 120) + "\x1b\\ done"
	kittyAPC := "pic \x1b_Ga=T,f=32;" + strings.Repeat("AAAA", 300) + "\x1b\\ ok"
	pmLong := "pre \x1b^" + strings.Repeat("privacy-msg-", 40) + "\x1b\\ post"
	mixed := "🚀\x1b[38;2;255;0;128mhello\x1b[0m " + osc8Long + "\r\n" + sixelDCS + " \x1b(B日本語\xff\xfeend"
	// U2 cases: ESC non-backslash recovery, raw 0x9c & UTF-8 U+009C ST, CAN/SUB
	// abort, raw 8-bit & UTF-8 C1 CSI/OSC/DCS/APC/PM/SOS introducers.
	oscEscRecovery := "a \x1b]0;title\x1b[31mred\x1b[0m b"
	dcsEscRecovery := "a \x1bP0;1qdata\x1b[31mred\x1b[0m b"
	oscRawST := "a \x1b]0;title\x9cred"
	oscUtf8ST := "a \x1b]0;title\u009cred"
	dcsUtf8ST := "a \x1bP0;1qdata\u009cred"
	oscCanSub := "a \x1b]0;title\x18mid \x1bP0;1qdata\x1aend"
	csiCan := "a \x1b[31\x18after"
	c1RawCSI := "\x9b31mred\x9b0m"
	c1Utf8CSI := "\u009b31mred\u009b0m"
	c1RawOSC := "a \x9d0;title\x9cb"
	c1Utf8OSC := "a \u009d0;title\u009cb"
	c1RawDCS := "a \x900;1qdata\x9cb"
	c1Utf8DCS := "a \u00900;1qdata\u009cb"
	c1Utf8APCPMSOS := "a \u009fGa=T\u009cb \u009epriv\u009cc \u0098sos\u009cd"

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"osc8-long-st", osc8Long, "see link end"},
		{"osc8-long-bel", osc8Bel, "before click after"},
		{"sixel-dcs-1.6kb", sixelDCS, "img  done"},
		{"kitty-apc-1.2kb", kittyAPC, "pic  ok"},
		{"pm-long", pmLong, "pre  post"},
		{"mixed-unicode-ansi-crlf", mixed, "🚀hello see link end\nimg  done 日本語end"},
		{"osc-esc-reentry", oscEscRecovery, "a red b"},
		{"dcs-esc-reentry", dcsEscRecovery, "a red b"},
		{"osc-raw-st", oscRawST, "a red"},
		{"osc-utf8-st", oscUtf8ST, "a red"},
		{"dcs-utf8-st", dcsUtf8ST, "a red"},
		{"osc-dcs-can-sub", oscCanSub, "a mid end"},
		{"csi-can", csiCan, "a after"},
		{"c1-raw-csi", c1RawCSI, "red"},
		{"c1-utf8-csi", c1Utf8CSI, "red"},
		{"c1-raw-osc", c1RawOSC, "a b"},
		{"c1-utf8-osc", c1Utf8OSC, "a b"},
		{"c1-raw-dcs", c1RawDCS, "a b"},
		{"c1-utf8-dcs", c1Utf8DCS, "a b"},
		{"c1-utf8-apc-pm-sos", c1Utf8APCPMSOS, "a b c d"},
	}

	rng := rand.New(rand.NewPCG(42, 7))
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Verify whole-input sanitizeText first (after CRLF normalization).
			wholeNorm := strings.ReplaceAll(strings.ReplaceAll(tc.input, "\r\n", "\n"), "\r", "\n")
			wholeClean := sanitizeText(wholeNorm)
			if wholeClean != tc.want {
				t.Fatalf("whole sanitizeText = %q, want %q", wholeClean, tc.want)
			}
			for run := range 500 {
				sink := NewOutputSink(OutputSinkOptions{
					SpillThreshold: 64 * 1024,
					MaxColumns:     0,
				})
				s := tc.input
				pos := 0
				for pos < len(s) {
					rem := len(s) - pos
					step := 1 + rng.IntN(min(rem, 64))
					sink.Push(s[pos : pos+step])
					pos += step
				}
				got := sink.Dump("").Output
				if got != wholeClean {
					t.Fatalf("run %d: split OutputSink = %q, want %q", run, got, wholeClean)
				}
			}
		})
	}
}

// Regression for T3 + U1:
//   - Exhaustive 160-cell matrix (4 modes × 4 user policies × 5 bash verdicts ×
//     2 session-grant states) comparing `resolveBashPolicy` + `sessionApprovalScopeKey`
//     against an independent transcription of omp `tools/approval.ts:382-492, 583-589`
//     and `wrapper.ts:408-415`.
//   - Interactive three-choice `ctx.Select` tests in `always-ask`, `write`, and
//     `always-ask-cached`:
//   - Plain "Approve" allows ONLY that call — next call asks again.
//   - "Approve bash for this session" records grant under `bash\x00exec` for
//     that strict session ID, allowing subsequent mode-sourced calls without
//     re-prompting while critical overrides and explicit pattern prompts still ask.
//   - "Deny" / cancelled select blocks.
//   - Without a strict session ID (no cwd fallback), the session option is
//     not offered and no grant is read or stored.
//   - `lifecycle.End(sessionID)` clears the session's grants.
//
// upstream: packages/coding-agent/src/tools/approval.ts:382-492, 583-589, 900-938
func TestPass3_T3_ApprovalPolicyFullMatrixAndSessionCache(t *testing.T) {
	prevSetting := Setting
	prevApprSetting := approval.Setting
	prevApprSess := approval.SessionSetting
	t.Cleanup(func() {
		Setting = prevSetting
		approval.Setting = prevApprSetting
		approval.SessionSetting = prevApprSess
	})

	const (
		approvalChoiceOnce    = "Approve"
		approvalChoiceSession = "Approve bash for this session"
		approvalChoiceDeny    = "Deny"
	)

	// 1. Complete 160-cell matrix (4 modes × 4 user policies × 5 verdicts × 2 sessionGrant states)
	// verified through approval.ResolvePolicy + approval.SessionApprovalScope.
	modes := []approval.Mode{approval.ModeYolo, approval.ModeWrite, approval.ModeAlwaysAsk, approval.ModeAlwaysAskCached}
	userPolicies := []approval.Policy{"", approval.PolicyAllow, approval.PolicyPrompt, approval.PolicyDeny}
	verdicts := []approvalVerdict{
		{Kind: "exec"},
		{Kind: "allow"},
		{Kind: "prompt", Reason: "Prompt required by bash pattern: curl *"},
		{Kind: "deny", Reason: "Blocked by bash pattern: rm -rf *"},
		{Kind: "critical", Reason: "Critical pattern detected"},
	}

	upstreamExpected := func(v approvalVerdict, mode approval.Mode, userPol approval.Policy, hasSessionGrant bool) (policy approval.Policy, source string, promptRequired bool) {
		var tPol approval.Policy
		var tOverride bool
		switch v.Kind {
		case "deny":
			tPol, tOverride = approval.PolicyDeny, true
		case "prompt":
			tPol, tOverride = approval.PolicyPrompt, true
		case "critical":
			tOverride = true
		case "allow":
			tPol = approval.PolicyAllow
		}
		switch {
		case tPol == approval.PolicyDeny:
			return approval.PolicyDeny, "tool", false
		case userPol == approval.PolicyDeny:
			return approval.PolicyDeny, "user", false
		case mode == approval.ModeYolo:
			if tPol != "" {
				return tPol, "tool", tPol == approval.PolicyPrompt
			}
			if userPol != "" {
				return userPol, "user", userPol == approval.PolicyPrompt
			}
			return approval.PolicyAllow, "mode", false
		case tOverride:
			if tPol == approval.PolicyAllow {
				return approval.PolicyAllow, "tool", false
			}
			return approval.PolicyPrompt, "tool", true
		case tPol == approval.PolicyAllow || tPol == approval.PolicyPrompt:
			return tPol, "tool", tPol == approval.PolicyPrompt
		case userPol != "":
			return userPol, "user", userPol == approval.PolicyPrompt
		default:
			return approval.PolicyPrompt, "mode", !hasSessionGrant
		}
	}

	checked := 0
	for _, mode := range modes {
		for _, userPol := range userPolicies {
			for _, v := range verdicts {
				for _, hasSessionGrant := range []bool{false, true} {
					checked++
					got := approval.ResolvePolicy("bash", verdictToToolDecision(v), mode, userPol)
					scopeKey := approval.SessionApprovalScope(got, "bash")
					gotPromptReq := got.Policy == approval.PolicyPrompt && !(hasSessionGrant && scopeKey != "")
					wantPol, wantSrc, wantPromptReq := upstreamExpected(v, mode, userPol, hasSessionGrant)
					if got.Policy != wantPol || got.Source != wantSrc || gotPromptReq != wantPromptReq {
						t.Errorf("cell(mode=%q, user=%q, verdict=%q, sessionGrant=%v): got={%s,%s,prompt=%v} want={%s,%s,prompt=%v}",
							mode, userPol, v.Kind, hasSessionGrant, got.Policy, got.Source, gotPromptReq, wantPol, wantSrc, wantPromptReq)
					}
				}
			}
		}
	}
	if checked != 160 {
		t.Fatalf("expected 160 matrix cells, checked %d", checked)
	}

	// 2. Headless `approval.HandleToolCallVeto` checks:
	for _, mode := range []string{"always-ask", "write", "always-ask-cached"} {
		m := mode
		Setting = func(_ sdk.Context, key string) any {
			if key == "tools.approvalMode" {
				return m
			}
			return nil
		}
		approval.Setting = Setting
		approval.SessionSetting = Setting
		res, err := approval.HandleToolCallVeto(sdk.Context{}, map[string]any{
			"toolName": "bash",
			"input":    map[string]any{"command": "echo hi"},
		})
		rm, _ := res.(map[string]any)
		if err != nil || rm["block"] != true || !strings.Contains(fmt.Sprint(rm["reason"]), `Tool "bash" requires approval but no interactive UI available.`) {
			t.Fatalf("mode %q headless echo hi: got res=%+v err=%v", m, res, err)
		}
	}

	// `tools.approval = {bash: "deny"}` blocks in yolo; `{bash: "allow"}` allows in always-ask.
	Setting = func(_ sdk.Context, key string) any {
		switch key {
		case "tools.approvalMode":
			return "yolo"
		case "tools.approval":
			return map[string]any{"bash": "deny"}
		}
		return nil
	}
	approval.Setting = Setting
	approval.SessionSetting = Setting
	res, err := approval.HandleToolCallVeto(sdk.Context{}, map[string]any{
		"toolName": "bash",
		"input":    map[string]any{"command": "echo hi"},
	})
	rm, _ := res.(map[string]any)
	if err != nil || rm["block"] != true || !strings.Contains(fmt.Sprint(rm["reason"]), `blocked by user policy`) {
		t.Fatalf("tools.approval.bash=deny in yolo: got res=%+v err=%v", res, err)
	}

	Setting = func(_ sdk.Context, key string) any {
		switch key {
		case "tools.approvalMode":
			return "always-ask"
		case "tools.approval":
			return map[string]any{"bash": "allow"}
		}
		return nil
	}
	approval.Setting = Setting
	approval.SessionSetting = Setting
	res, err = approval.HandleToolCallVeto(sdk.Context{}, map[string]any{
		"toolName": "bash",
		"input":    map[string]any{"command": "echo hi"},
	})
	if err != nil || res != nil {
		t.Fatalf("tools.approval.bash=allow in always-ask: expected allow, got res=%+v err=%v", res, err)
	}

	// 3. Interactive three-answer Select dialog tests across all three non-yolo modes
	// via shellFakeHost (HasUI=true, strict session ID) with approval.Register:
	for _, mode := range []string{"always-ask", "write", "always-ask-cached"} {
		m := mode
		t.Run("interactive-"+m, func(t *testing.T) {
			sessID := "sess-u1-" + m
			defaultRuntime.DisposeSession(sessID)
			approval.ClearSession(sessID)
			t.Cleanup(func() {
				defaultRuntime.DisposeSession(sessID)
				approval.ClearSession(sessID)
			})

			Setting = func(_ sdk.Context, key string) any {
				if key == "tools.approvalMode" {
					return m
				}
				return nil
			}
			approval.Setting = Setting
			approval.SessionSetting = Setting
			ext := sdk.New("ext-u1-" + m)
			Register(ext)
			approval.Register(ext)
			h := startShellFakeHost(t, ext, t.TempDir(), sessID)
			var seenOpts [][]string
			var nextReply string
			h.mu.Lock()
			h.onSelect = func(_ string, opts []string) (any, bool) {
				seenOpts = append(seenOpts, append([]string(nil), opts...))
				return nextReply, nextReply != ""
			}
			h.mu.Unlock()

			// (a) Plain "Approve" (once) must offer 3 choices and MUST NOT grant the session!
			nextReply = approvalChoiceOnce
			evRaw, evErr := h.emitEvent(sdk.EventToolCall, map[string]any{
				"toolName": "bash",
				"input":    map[string]any{"command": "echo first"},
			})
			if evErr != "" || strings.Contains(string(evRaw), `"block":true`) {
				t.Fatalf("Approve once failed: raw=%s err=%s", evRaw, evErr)
			}
			if len(seenOpts) != 1 || len(seenOpts[0]) != 3 ||
				seenOpts[0][0] != approvalChoiceOnce ||
				seenOpts[0][1] != approvalChoiceSession ||
				seenOpts[0][2] != approvalChoiceDeny {
				t.Fatalf("expected 3 choices [Approve, Approve bash for this session, Deny], got %v", seenOpts)
			}
			if approval.HasSessionApproval(sessID, "bash\x00exec") {
				t.Fatalf("plain Approve once MUST NOT grant session approval in mode %q", m)
			}

			// (b) Second call MUST prompt again; user chooses "Deny" -> blocked.
			nextReply = approvalChoiceDeny
			evRaw, evErr = h.emitEvent(sdk.EventToolCall, map[string]any{
				"toolName": "bash",
				"input":    map[string]any{"command": "echo second"},
			})
			if evErr != "" || !strings.Contains(string(evRaw), `"block":true`) {
				t.Fatalf("Deny expected block: raw=%s err=%s", evRaw, evErr)
			}
			if len(seenOpts) != 2 {
				t.Fatalf("second call did not prompt after plain Approve once: prompts=%d", len(seenOpts))
			}

			// (c) Third call: user selects "Approve bash for this session" -> grants bash\x00exec.
			nextReply = approvalChoiceSession
			evRaw, evErr = h.emitEvent(sdk.EventToolCall, map[string]any{
				"toolName": "bash",
				"input":    map[string]any{"command": "echo third"},
			})
			if evErr != "" || strings.Contains(string(evRaw), `"block":true`) {
				t.Fatalf("Approve session failed: raw=%s err=%s", evRaw, evErr)
			}
			if !approval.HasSessionApproval(sessID, "bash\x00exec") {
				t.Fatalf("expected bash\\x00exec session grant recorded")
			}

			// (d) Fourth ordinary call runs WITHOUT prompting (prompts count stays 3).
			nextReply = approvalChoiceDeny // would fail if prompted
			evRaw, evErr = h.emitEvent(sdk.EventToolCall, map[string]any{
				"toolName": "bash",
				"input":    map[string]any{"command": "echo fourth"},
			})
			if evErr != "" || strings.Contains(string(evRaw), `"block":true`) {
				t.Fatalf("session-granted call should allow without prompt: raw=%s err=%s", evRaw, evErr)
			}
			if len(seenOpts) != 3 {
				t.Fatalf("session-granted call prompted unexpectedly: prompts=%d", len(seenOpts))
			}

			// (e) Critical command STILL prompts (with ONLY 2 choices: no session grant for critical!).
			nextReply = approvalChoiceOnce
			evRaw, evErr = h.emitEvent(sdk.EventToolCall, map[string]any{
				"toolName": "bash",
				"input":    map[string]any{"command": "rm -rf /tmp/build-123"},
			})
			if evErr != "" || strings.Contains(string(evRaw), `"block":true`) {
				t.Fatalf("critical Approve once failed: raw=%s err=%s", evRaw, evErr)
			}
			if len(seenOpts) != 4 || len(seenOpts[3]) != 2 {
				t.Fatalf("critical override must prompt with 2 choices [Approve, Deny], got %v", seenOpts)
			}

			// (f) After lifecycle.End(sessID), session grant is gone and next call prompts again.
			lifecycle.End(sessID)
			if approval.HasSessionApproval(sessID, "bash\x00exec") {
				t.Fatalf("session grant survived lifecycle.End")
			}
		})
	}

	// 4. Strict session ID requirement: without a session ID (""), session option
	// is NOT offered (only 2 choices) and no grant is stored under cwd.
	t.Run("no-session-id-no-grant", func(t *testing.T) {
		Setting = func(_ sdk.Context, key string) any {
			if key == "tools.approvalMode" {
				return "always-ask-cached"
			}
			return nil
		}
		approval.Setting = Setting
		approval.SessionSetting = Setting
		ext := sdk.New("ext-u1-nosess")
		Register(ext)
		approval.Register(ext)
		h := startShellFakeHost(t, ext, t.TempDir(), "") // empty session ID
		var optsSeen []string
		h.mu.Lock()
		h.onSelect = func(_ string, opts []string) (any, bool) {
			optsSeen = append([]string(nil), opts...)
			return approvalChoiceOnce, true
		}
		h.mu.Unlock()

		evRaw, evErr := h.emitEvent(sdk.EventToolCall, map[string]any{
			"toolName": "bash",
			"input":    map[string]any{"command": "echo nosess"},
		})
		if evErr != "" || strings.Contains(string(evRaw), `"block":true`) {
			t.Fatalf("nosess Approve once failed: raw=%s err=%s", evRaw, evErr)
		}
		if len(optsSeen) != 2 || optsSeen[0] != approvalChoiceOnce || optsSeen[1] != approvalChoiceDeny {
			t.Fatalf("without strict session ID expected 2 choices [Approve, Deny], got %v", optsSeen)
		}
	})
}

// Regression for T4:
//   - When a requested state capture is incomplete after a normal exit (e.g.
//     `exec echo hi` or user `trap ... EXIT`), the result includes a bracketed
//     notice instead of dropping state silently, while keeping earlier state.
//   - An oversized variable (> maxStateRecordBytes = 4 MiB) is skipped with a
//     notice while smaller variables set in the same call STILL persist.
func TestPass3_T4_IncompleteCaptureNoticeAndOversizedVarSkip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX state capture notice test")
	}
	tool, dir := newTestTool(t, nil)

	// Seed baseline state.
	if _, err := runTool(t, tool, map[string]any{"command": "BASE_OK=yes", "cwd": dir}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// 1. User overrides EXIT trap -> incomplete capture -> must emit bracketed notice, keep BASE_OK.
	res, err := runTool(t, tool, map[string]any{
		"command": `trap 'echo bye' EXIT; LOST_VAR=1; echo hi`,
		"cwd":     dir,
	})
	if err != nil {
		t.Fatalf("trap call: %v", err)
	}
	if !strings.Contains(res.Content, "[Shell state changes of this call were not saved") {
		t.Fatalf("expected bracketed incomplete-capture notice, got:\n%q", res.Content)
	}

	// 2. `exec echo replaced` -> incomplete capture -> must emit bracketed notice, keep BASE_OK.
	res, err = runTool(t, tool, map[string]any{
		"command": `exec echo replaced`,
		"cwd":     dir,
	})
	if err != nil {
		t.Fatalf("exec call: %v", err)
	}
	if !strings.Contains(res.Content, "[Shell state changes of this call were not saved") {
		t.Fatalf("expected bracketed incomplete-capture notice after exec, got:\n%q", res.Content)
	}

	// 3. Oversized variable (~5 MiB, > 4 MiB record cap) + small variable in the
	// same call: oversized var is skipped with a notice while SMALL_KEEP persists.
	res, err = runTool(t, tool, map[string]any{
		"command": `HUGE_VAR=$(python3 -c 'print("x"*5242880, end="")'); SMALL_KEEP=survived`,
		"cwd":     dir,
	})
	if err != nil {
		t.Fatalf("oversized call: %v", err)
	}
	if !strings.Contains(res.Content, "[Skipped persisting oversized shell entries") || !strings.Contains(res.Content, "HUGE_VAR") {
		t.Fatalf("expected skipped oversized var notice naming HUGE_VAR, got:\n%q", res.Content)
	}

	verifyRes, err := runTool(t, tool, map[string]any{
		"command": `printf '[%s][%s][%s]' "$BASE_OK" "$SMALL_KEEP" "${HUGE_VAR:+present}"`,
		"cwd":     dir,
	})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if verifyRes.Content != "[yes][survived][]" {
		t.Fatalf("expected BASE_OK=yes, SMALL_KEEP=survived, HUGE_VAR unset; got %q", verifyRes.Content)
	}
}

// Regression for T5:
//  1. Self-changing variables (BASHPID, EPOCHREALTIME, EPOCHSECONDS) are ignored
//     so unchanged calls do NOT save them or rewrite state.sh.
//  2. `IFS` and `PS4` persist across calls.
//  3. A persisted `set -x` does NOT trace wrapper lines (`+ shopt`, `+ exec`,
//     `+ exit`) into subsequent call outputs.
//  4. Redirecting the state FD to a file (`exec <fd>>file`) CANNOT capture the
//     post dump or inherited environment into that file (even if the command
//     discovers the per-call FD from $BASH_EXECUTION_STRING).
func TestPass3_T5_StateNitsIgnoreSelfChangingPersistIFSPS4XtraceAndFDGuard(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX state nits test")
	}
	t.Setenv("OMP_T5_SECRET", "t5-secret-token-777")
	tool, dir := newTestTool(t, nil)

	// 1. Trivial call with no mutations must NOT save BASHPID/EPOCHREALTIME/EPOCHSECONDS.
	if _, err := runTool(t, tool, map[string]any{"command": "true", "cwd": dir}); err != nil {
		t.Fatalf("trivial call: %v", err)
	}
	st := tool.runtime.sessionState("default")
	st.mu.Lock()
	createdAfterNoop := st.created
	st.mu.Unlock()
	if createdAfterNoop {
		raw, _ := os.ReadFile(st.file)
		t.Fatalf("trivial unchanged call rewrote state.sh:\n%s", string(raw))
	}

	// 2. Persist IFS and PS4 across calls (and verify no self-changing vars appear).
	if _, err := runTool(t, tool, map[string]any{
		"command": `IFS=,; PS4='>> '`,
		"cwd":     dir,
	}); err != nil {
		t.Fatalf("set IFS/PS4: %v", err)
	}
	st.mu.Lock()
	sf := st.file
	st.mu.Unlock()
	rawState, err := os.ReadFile(sf)
	if err != nil {
		t.Fatalf("read state.sh: %v", err)
	}
	for _, selfChanging := range []string{"BASHPID", "EPOCHREALTIME", "EPOCHSECONDS"} {
		if strings.Contains(string(rawState), selfChanging) {
			t.Fatalf("self-changing var %s persisted into state.sh:\n%s", selfChanging, string(rawState))
		}
	}
	res, err := runTool(t, tool, map[string]any{
		"command": `printf '[%s][%s]' "$IFS" "$PS4"`,
		"cwd":     dir,
	})
	if err != nil {
		t.Fatalf("verify IFS/PS4: %v", err)
	}
	if res.Content != "[,][>> ]" {
		t.Fatalf("IFS/PS4 did not persist: got %q, want %q", res.Content, "[,][>> ]")
	}

	// Reset IFS back to default for readability before xtrace check.
	if _, err := runTool(t, tool, map[string]any{"command": `IFS=$' \t\n'; set -x`, "cwd": dir}); err != nil {
		t.Fatalf("set -x: %v", err)
	}

	// 3. Next call under persisted `set -x` must NOT trace wrapper lines.
	xRes, err := runTool(t, tool, map[string]any{"command": "echo traced-ok", "cwd": dir})
	if err != nil {
		t.Fatalf("xtrace call: %v", err)
	}
	if !strings.Contains(xRes.Content, "traced-ok") {
		t.Fatalf("expected traced-ok in output, got %q", xRes.Content)
	}
	for _, wrapperLeak := range []string{"shopt -s expand_aliases", "exec ", "__omp_", "exit 0"} {
		if strings.Contains(xRes.Content, wrapperLeak) {
			t.Fatalf("persisted set -x leaked wrapper line %q into output:\n%s", wrapperLeak, xRes.Content)
		}
	}
	if _, err := runTool(t, tool, map[string]any{"command": "set +x", "cwd": dir}); err != nil {
		t.Fatalf("set +x: %v", err)
	}

	// 4. Attempting to hijack the state FD by extracting it from BASH_EXECUTION_STRING
	// and running `exec <fd>><leakFile>` MUST NOT capture the post dump or secret.
	leakFile := filepath.Join(dir, "leaked.txt")
	hijackCmd := fmt.Sprintf(`fd=$(printf '%%s' "$BASH_EXECUTION_STRING" | sed -n 's/.*exec \([0-9][0-9]*\)>&3.*/\1/p' | head -n1); if [ -n "$fd" ]; then eval "exec ${fd}>%s"; fi; HIJACK_VAR=1`, singleQuotePosix(leakFile))
	hijackRes, err := runTool(t, tool, map[string]any{"command": hijackCmd, "cwd": dir})
	if err != nil {
		t.Fatalf("hijack call: %v", err)
	}
	if rawLeak, lErr := os.ReadFile(leakFile); lErr == nil && len(rawLeak) > 0 {
		t.Fatalf("redirected state FD leaked %d bytes into file (secret leaked=%v): %q",
			len(rawLeak), strings.Contains(string(rawLeak), "t5-secret-token-777"), string(rawLeak[:min(200, len(rawLeak))]))
	}
	// Because the hijack redirected the state FD on bash 5.3, the guard refused
	// to write to the file and reported the incomplete capture notice.
	_ = hijackRes
}
