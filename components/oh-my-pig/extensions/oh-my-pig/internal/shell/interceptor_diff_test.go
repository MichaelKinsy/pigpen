package shell

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// TestInterceptorDifferentialAgainstOmp compares all four hand-written
// bash-interceptor matchers (`matchesCatReadRule`, `matchesEchoCatWriteRule`,
// `matchesNohupOrBackgroundRule`, `matchesDevServiceRule`) against 200,000
// deterministic command lines (50,000 per rule) whose verdicts were recorded
// from omp v19.1.1's own JavaScript RegExps (`exec/settings.ts:38,87,93,100`)
// via `testdata/gen_interceptor_diff.ts`.
func TestInterceptorDifferentialAgainstOmp(t *testing.T) {
	f, err := os.Open(filepath.Join("testdata", "interceptor_diff.txt.gz"))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer gz.Close()

	matchers := map[byte]struct {
		name string
		fn   func(string) bool
	}{
		'0': {"cat_read", matchesCatReadRule},
		'6': {"echo_cat_write", matchesEchoCatWriteRule},
		'7': {"nohup_bg", matchesNohupOrBackgroundRule},
		'8': {"dev_service", matchesDevServiceRule},
	}
	counts := map[byte]int{}
	mismatches := map[byte]int{}
	var examples []string

	sc := bufio.NewScanner(gz)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		if len(line) < 4 || line[2] != '\t' {
			t.Fatalf("line %d: malformed record %q", lineNo, line)
		}
		ruleKey := line[0]
		want := line[1] == '1'
		m, ok := matchers[ruleKey]
		if !ok {
			t.Fatalf("line %d: unknown rule key %q", lineNo, ruleKey)
		}
		var cmd string
		if err := json.Unmarshal([]byte(line[3:]), &cmd); err != nil {
			t.Fatalf("line %d: unmarshal command: %v", lineNo, err)
		}
		counts[ruleKey]++
		got := m.fn(cmd)
		if got != want {
			mismatches[ruleKey]++
			if len(examples) < 20 {
				examples = append(examples, m.name+": cmd="+line[3:]+" got="+boolStr(got)+" want="+boolStr(want))
			}
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan fixture: %v", err)
	}

	for key, m := range matchers {
		if counts[key] < 50_000 {
			t.Errorf("rule %s: expected >= 50000 cases, got %d", m.name, counts[key])
		}
		if mismatches[key] > 0 {
			t.Errorf("rule %s: %d mismatches out of %d", m.name, mismatches[key], counts[key])
		}
	}
	if len(examples) > 0 {
		t.Fatalf("interceptor differential mismatches:\n%s", strings.Join(examples, "\n"))
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// TestUserInterceptorRuleFlagsAndUncompilableWarning verifies Finding 7:
//   - user rules honour `flags` (`i`, `m`, `s`) via `bashInterceptor.patterns`
//   - user rules that Go RE2 cannot compile (JS lookaround, backreferences,
//     unsupported `y` flag) notify the user ONCE per session instead of
//     silently dropping the rule.
func TestUserInterceptorRuleFlagsAndUncompilableWarning(t *testing.T) {
	// 1. Direct unit check: flags "i", "m", "s" work.
	rules := []InterceptorRule{
		{
			Pattern: `^RM `,
			Flags:   "i",
			Tool:    "edit",
			Message: "Do not use rm here.",
		},
		{
			Pattern: `^foo.*bar$`,
			Flags:   "ms",
			Tool:    "read",
			Message: "Multiline dotall match.",
		},
	}
	if res := CheckBashInterception("rm -f x", []string{"edit", "read"}, rules, "rm -f x"); !res.Block || res.SuggestedTool != "edit" {
		t.Fatalf("expected case-insensitive flags:\"i\" rule to block `rm -f x`, got %+v", res)
	}
	if res := CheckBashInterception("echo ok\nfoo\nbar", []string{"edit", "read"}, rules, "echo ok\nfoo\nbar"); !res.Block || res.SuggestedTool != "read" {
		t.Fatalf("expected flags:\"ms\" rule to match across lines, got %+v", res)
	}

	// 2. Settings parser preserves `flags` and warns ONCE per session for uncompilable JS regex.
	prevSetting := Setting
	prevSession := SessionSetting
	t.Cleanup(func() {
		Setting = prevSetting
		SessionSetting = prevSession
	})

	ext := sdk.New("interceptor-flags-test")
	Register(ext)
	dir := t.TempDir()
	sessID := "sess-int-warn"
	h := startShellFakeHost(t, ext, dir, sessID)
	t.Cleanup(func() { defaultRuntime.DisposeSession(sessID) })

	Setting = func(_ sdk.Context, key string) any {
		switch key {
		case "bashInterceptor.enabled":
			return true
		case "bashInterceptor.patterns":
			return []any{
				map[string]any{
					"pattern": `^\s*rm\s+(?!-i)`, // JS negative lookahead — invalid in RE2
					"tool":    "bash",
					"message": "Lookahead rule.",
				},
				map[string]any{
					"pattern": `^DEPLOY\s+`,
					"flags":   "i",
					"tool":    "bash",
					"message": "Use release workflow.",
				},
			}
		}
		return nil
	}
	SessionSetting = Setting

	// Call 1: harmless echo triggers compilation of the invalid lookahead rule -> warns once.
	if _, errMsg := h.callTool("bash", map[string]any{"command": "echo safe1", "cwd": dir}); errMsg != "" {
		t.Fatalf("safe1 failed unexpectedly: %s", errMsg)
	}
	// Call 2: second call in the same session must NOT emit a duplicate warning.
	if _, errMsg := h.callTool("bash", map[string]any{"command": "echo safe2", "cwd": dir}); errMsg != "" {
		t.Fatalf("safe2 failed unexpectedly: %s", errMsg)
	}
	h.mu.Lock()
	var warnMsgs []string
	for _, c := range h.calls {
		if c.Method == "ui.notify" || c.Method == "notify" {
			if msg, _ := c.Args["message"].(string); strings.Contains(msg, "bashInterceptor rule") && strings.Contains(msg, "unsupported by Go RE2") {
				warnMsgs = append(warnMsgs, msg)
			}
		}
	}
	h.mu.Unlock()
	if len(warnMsgs) != 1 {
		t.Fatalf("expected exactly 1 uncompilable interceptor warning per session, got %d: %v (all calls=%v)", len(warnMsgs), warnMsgs, h.calls)
	}

	// Call 3: case-insensitive user rule with `flags: "i"` blocks `deploy prod`.
	if _, errMsg := h.callTool("bash", map[string]any{"command": "deploy prod", "cwd": dir}); !strings.Contains(errMsg, "Use release workflow.") {
		t.Fatalf("expected flags:\"i\" rule to block `deploy prod`, got err=%q", errMsg)
	}
}
