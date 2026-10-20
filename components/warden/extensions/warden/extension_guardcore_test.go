package warden

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Layer-1 wiring of the guard core through the fake host: session scratch (extension.test.ts:980-1062), path rules
// (:2945) and arming rules, large-output steers, host paths.

func offlineCfg() *Config {
	c := DefaultConfig()
	c.Enabled = true
	c.Rules.Enabled = false
	return &c
}

// callWith fires tool_call, runs effect as the command would, then fires tool_result with output.
func (h *harness) callWith(tool, id string, input map[string]any, effect func(), output string) map[string]any {
	h.t.Helper()
	res := h.toolCall(tool, id, input)
	if res != nil {
		return res
	}
	// A path born in the same millisecond the call began does not count as created by it.
	time.Sleep(5 * time.Millisecond)
	if effect != nil {
		effect()
	}
	h.host.Fire("tool_result", map[string]any{"toolName": tool, "toolCallId": id, "input": input, "content": []any{map[string]any{"type": "text", "text": output}}, "isError": false})
	return nil
}

func scratchHarness(t *testing.T) (*harness, string) {
	t.Helper()
	base, err := os.MkdirTemp("/tmp", "pi-warden-scratch-")
	if err != nil {
		t.Skip("no /tmp:", err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	skipWithoutBirthtime(t, base)
	h := startHarness(t, harnessOpts{cfg: offlineCfg(), scratchPlatform: "darwin"})
	h.start()
	return h, base
}

func wantNotHeld(t *testing.T, h *harness, id, command string) {
	t.Helper()
	if reason, held := blocked(h.toolCall("bash", id, map[string]any{"command": command})); held {
		t.Fatalf("held: %s", reason)
	}
}

func wantHeld(t *testing.T, h *harness, id, command string) string {
	t.Helper()
	reason, held := blocked(h.toolCall("bash", id, map[string]any{"command": command}))
	if !held {
		t.Fatalf("not held: %s", command)
	}
	return reason
}

// twin: tests/extension.test.ts:980 "session scratch: mkdir -p under /tmp, then rm -rf of it is not held"
func TestWiringSessionScratchMkdirThenRmIsNotHeld(t *testing.T) {
	h, base := scratchHarness(t)
	probe := filepath.Join(base, "probe-abc")
	h.callWith("bash", "m1", map[string]any{"command": "mkdir -p " + probe}, func() { os.MkdirAll(probe, 0o755) }, "")
	wantNotHeld(t, h, "r1", "rm -rf "+probe)
	if n := h.notices(); len(n) == 0 || !strings.Contains(n[len(n)-1], "session scratch") {
		t.Errorf("notices %v", n)
	}
}

// twin: tests/extension.test.ts:988 "session scratch: mktemp -d printing a $TMPDIR path, then rm -rf of it is not held"
func TestWiringSessionScratchMktempIsNotHeld(t *testing.T) {
	h, _ := scratchHarness(t)
	input := map[string]any{"command": "mktemp -d"}
	h.toolCall("bash", "m1", input)
	time.Sleep(5 * time.Millisecond)
	made, err := os.MkdirTemp(os.TempDir(), "tmp.")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(made)
	h.host.Fire("tool_result", map[string]any{"toolName": "bash", "toolCallId": "m1", "input": input, "content": []any{map[string]any{"type": "text", "text": made + "\n"}}, "isError": false})
	wantNotHeld(t, h, "r1", "rm -rf "+made)
	if n := h.notices(); len(n) == 0 || !strings.Contains(n[len(n)-1], "session scratch") {
		t.Errorf("notices %v", n)
	}
}

// twin: tests/extension.test.ts:1001 "session scratch: a written file's new parent under /tmp is scratch"
func TestWiringSessionScratchAWrittenFilesNewParentIsScratch(t *testing.T) {
	h, base := scratchHarness(t)
	file := filepath.Join(base, "gen", "out.txt")
	h.callWith("write", "w1", map[string]any{"path": file, "content": "x"}, func() { os.MkdirAll(filepath.Dir(file), 0o755); os.WriteFile(file, []byte("x"), 0o600) }, "")
	wantNotHeld(t, h, "r1", "rm -rf "+filepath.Join(base, "gen"))
}

// twin: tests/extension.test.ts:1007 "session scratch: one target never created keeps the whole rm held"
func TestWiringSessionScratchOneTargetNeverCreatedKeepsTheHold(t *testing.T) {
	h, base := scratchHarness(t)
	probe := filepath.Join(base, "probe-abc")
	os.MkdirAll(filepath.Join(base, "other"), 0o755)
	h.callWith("bash", "m1", map[string]any{"command": "mkdir -p " + probe}, func() { os.MkdirAll(probe, 0o755) }, "")
	reason := wantHeld(t, h, "r1", "rm -rf "+probe+" "+filepath.Join(base, "other"))
	mustMatch(t, reason, `recursive rm on an absolute, home, variable, or parent path`, "reason")
}

// twin: tests/extension.test.ts:1016 "session scratch: mkdir -p of a directory that already existed records nothing"
func TestWiringSessionScratchAnExistingDirectoryRecordsNothing(t *testing.T) {
	h, base := scratchHarness(t)
	existing := filepath.Join(base, "existing")
	os.MkdirAll(existing, 0o755)
	h.callWith("bash", "m1", map[string]any{"command": "mkdir -p " + existing}, nil, "")
	wantHeld(t, h, "r1", "rm -rf "+existing)
}

// twin: tests/extension.test.ts:1023 "session scratch: a symlink under /tmp pointing outside the temp directory stays held"
func TestWiringSessionScratchASymlinkOutsideStaysHeld(t *testing.T) {
	h, base := scratchHarness(t)
	dir := filepath.Join(base, "dir")
	h.callWith("bash", "m1", map[string]any{"command": "mkdir -p " + dir}, func() { os.MkdirAll(dir, 0o755) }, "")
	outside := t.TempDir()
	inside := filepath.Join(dir, "out")
	if err := os.Symlink(outside, inside); err != nil {
		t.Skip("no symlinks:", err)
	}
	wantHeld(t, h, "r1", "rm -rf "+inside)
	printed := filepath.Join(base, "link")
	h.callWith("bash", "m2", map[string]any{"command": "ln -s " + outside + " " + printed + " && echo " + printed}, func() { os.Symlink(outside, printed) }, printed+"\n")
	wantHeld(t, h, "r2", "rm -rf "+printed)
}

// twin: tests/extension.test.ts:1034 "session scratch: a temp directory printed by a command counts only when the command created it"
func TestWiringSessionScratchAPrintedDirectoryCountsOnlyWhenCreated(t *testing.T) {
	h, base := scratchHarness(t)
	before := filepath.Join(base, "before")
	os.MkdirAll(before, 0o755)
	made := filepath.Join(base, "made")
	h.callWith("bash", "m1", map[string]any{"command": "npm test"}, func() { os.MkdirAll(made, 0o755) }, "fixture at "+made+"\nreused "+before+"\n")
	wantNotHeld(t, h, "r1", "rm -rf "+made)
	wantHeld(t, h, "r2", "rm -rf "+before)
}

// twin: tests/extension.test.ts:1043 "session scratch: a recorded path deleted, then made again outside the agent, stays held"
func TestWiringSessionScratchARecreatedPathStaysHeld(t *testing.T) {
	h, base := scratchHarness(t)
	probe := filepath.Join(base, "probe-abc")
	h.callWith("bash", "m1", map[string]any{"command": "mkdir -p " + probe}, func() { os.MkdirAll(probe, 0o755) }, "")
	h.callWith("bash", "m2", map[string]any{"command": "rm -rf " + probe}, func() { os.RemoveAll(probe) }, "")
	os.MkdirAll(probe, 0o755)
	wantHeld(t, h, "r1", "rm -rf "+probe)
}

// twin: tests/extension.test.ts:1051 "session scratch: older content moved into a recorded directory stays held"
func TestWiringSessionScratchMovedInContentStaysHeld(t *testing.T) {
	h, base := scratchHarness(t)
	important := filepath.Join(base, "important")
	os.MkdirAll(important, 0o755)
	dir := filepath.Join(base, "S", "x")
	h.callWith("bash", "m1", map[string]any{"command": "mkdir -p " + dir}, func() { os.MkdirAll(dir, 0o755) }, "")
	h.callWith("bash", "m2", map[string]any{"command": "mv " + important + " " + dir + "/"}, func() { os.Rename(important, filepath.Join(dir, "important")) }, "")
	reason := wantHeld(t, h, "r1", "rm -rf "+dir)
	mustMatch(t, reason, `recursive rm on an absolute, home, variable, or parent path`, "reason")
}

// twin: tests/extension.test.ts:1062 "session scratch: a fresh session forgets what the last one created"
func TestWiringSessionScratchAFreshSessionForgets(t *testing.T) {
	h, base := scratchHarness(t)
	probe := filepath.Join(base, "probe-abc")
	h.callWith("bash", "m1", map[string]any{"command": "mkdir -p " + probe}, func() { os.MkdirAll(probe, 0o755) }, "")
	h.start()
	wantHeld(t, h, "r1", "rm -rf "+probe)
}

// twin: tests/extension.test.ts:2945 "pathRules: a confirm rule prompts the user, a block rule blocks, and notes only steer the agent"
func TestWiringPathRulesConfirmBlockAndFlow(t *testing.T) {
	c := offlineCfg()
	c.Action.Tools = []string{"bash", "write", "edit", "read"}
	c.Action.PathRules = []PathRule{
		{ID: "repo-readonly", Paths: []string{"deploy.yaml"}, Access: "read", Tools: []string{"write", "edit"}, Action: "confirm", Message: "deploys change cluster state"},
		{ID: "audit-log", Paths: []string{"audit/app.log"}, Access: "write", Tools: []string{"read"}, Action: "block"},
	}
	h := startHarness(t, harnessOpts{cfg: c})
	dir := h.host.opts.Cwd
	os.WriteFile(filepath.Join(dir, "deploy.yaml"), []byte("image: app\n"), 0o600)
	os.MkdirAll(filepath.Join(dir, "audit"), 0o755)
	os.WriteFile(filepath.Join(dir, "audit", "app.log"), []byte("entry\n"), 0o600)
	h.start()
	h.confirm = true
	if _, held := blocked(h.toolCall("edit", "e1", map[string]any{"path": "deploy.yaml", "edits": []any{map[string]any{"oldText": "app", "newText": "app2"}}})); held {
		t.Fatal("the dialog answer allowed the edit")
	}
	confirms := h.host.CallsTo("ui.confirm")
	if len(confirms) != 1 {
		t.Fatalf("the confirm path rule prompted the user: %d", len(confirms))
	}
	if text, _ := confirms[0].Args["message"].(string); !strings.Contains(text, "deploy.yaml") && !strings.Contains(text, "deploys change cluster state") {
		t.Errorf("the dialog names the path or the rule: %v", confirms[0].Args)
	}
	if h.ts.count() != 0 {
		t.Fatal("a path rule costs no request")
	}
	reason, held := blocked(h.toolCall("read", "r1", map[string]any{"path": "audit/app.log"}))
	if !held || !strings.Contains(reason, "audit-log") {
		t.Fatalf("the block path rule denies, naming the rule: %q", reason)
	}
	if len(h.host.CallsTo("ui.confirm")) != 1 {
		t.Fatal("a block never opens a dialog")
	}
	if _, held := blocked(h.toolCall("read", "r2", map[string]any{"path": "deploy.yaml"})); held {
		t.Fatal("a read of the read-flow path passes without a prompt")
	}
}

func armingCfg(rules ...ArmingRule) *Config {
	c := offlineCfg()
	c.Action.ArmingRules = rules
	return c
}

func TestWiringArmingRuleConfirmsABlockedCommandAfterAnEdit(t *testing.T) {
	rule := ArmingRule{ID: "gitops", When: ArmingWhen{Edited: []string{"**/kustomization.yaml"}}, Arms: ArmingArms{Command: `\bflux\b`, For: "10m"}, Action: "confirm", Message: "reconciling applies the edit"}
	h := startHarness(t, harnessOpts{cfg: armingCfg(rule)})
	h.start()
	dir := h.host.opts.Cwd
	if _, held := blocked(h.toolCall("bash", "b0", map[string]any{"command": "flux reconcile"})); held {
		t.Fatal("unarmed")
	}
	h.toolCall("write", "w1", map[string]any{"path": filepath.Join(dir, "apps", "kustomization.yaml"), "content": "resources: []\n"})
	h.confirm = false
	reason, held := blocked(h.toolCall("bash", "b1", map[string]any{"command": "flux reconcile"}))
	confirms := h.host.CallsTo("ui.confirm")
	if !held || len(confirms) == 0 || !strings.Contains(reason, "declined") {
		t.Fatalf("armed confirm asks and honours the answer: %q %d", reason, len(confirms))
	}
	if msg, _ := confirms[len(confirms)-1].Args["message"].(string); !strings.Contains(msg, "armed by") {
		t.Errorf("the dialog names the file that armed it: %q", msg)
	}
}

func TestWiringArmingRuleBlockAndExemption(t *testing.T) {
	rule := ArmingRule{ID: "never-reset", When: ArmingWhen{Edited: []string{"**/dangerous.conf"}}, Arms: ArmingArms{Command: `\btalosctl\s+reset\b`, For: "5m"}, Action: "block"}
	h := startHarness(t, harnessOpts{cfg: armingCfg(rule)})
	h.start()
	dir := h.host.opts.Cwd
	h.toolCall("write", "w1", map[string]any{"path": filepath.Join(dir, "dangerous.conf"), "content": "x"})
	reason, held := blocked(h.toolCall("bash", "b1", map[string]any{"command": "talosctl reset --graceful=false"}))
	if !held || !strings.Contains(reason, "armed rule never-reset") {
		t.Fatalf("block: %q", reason)
	}

	c := armingCfg(rule)
	c.Action.ExemptRules = []string{"never-reset"}
	h2 := startHarness(t, harnessOpts{cfg: c})
	h2.start()
	h2.toolCall("write", "w1", map[string]any{"path": filepath.Join(h2.host.opts.Cwd, "dangerous.conf"), "content": "x"})
	if _, held := blocked(h2.toolCall("bash", "b1", map[string]any{"command": "talosctl reset --graceful=false"})); held {
		t.Fatal("exemptRules silences an armed rule by its own id")
	}
}

func TestWiringArmingStateIsClearedByASessionStart(t *testing.T) {
	rule := ArmingRule{ID: "never-reset", When: ArmingWhen{Edited: []string{"**/dangerous.conf"}}, Arms: ArmingArms{Command: `\btalosctl\s+reset\b`, For: "5m"}, Action: "block"}
	h := startHarness(t, harnessOpts{cfg: armingCfg(rule)})
	h.start()
	h.toolCall("write", "w1", map[string]any{"path": filepath.Join(h.host.opts.Cwd, "dangerous.conf"), "content": "x"})
	h.start()
	if _, held := blocked(h.toolCall("bash", "b1", map[string]any{"command": "talosctl reset"})); held {
		t.Fatal("a new session forgets the arming")
	}
}

func TestWiringConfigNoticesAreSaidOnce(t *testing.T) {
	c := offlineCfg()
	c.Action.ExemptRules = []string{"typo-id"}
	c.Action.PathRules = []PathRule{{ID: "audit", Paths: []string{"x.log"}, Access: "write", Tools: []string{"write"}, Action: "block"}}
	c.Action.ArmingRules = []ArmingRule{{ID: "bad", When: ArmingWhen{Edited: []string{"*"}}, Arms: ArmingArms{Command: "[bad"}, Action: "confirm"}}
	h := startHarness(t, harnessOpts{cfg: c})
	h.start()
	h.toolCall("bash", "c1", map[string]any{"command": "ls"})
	h.toolCall("bash", "c2", map[string]any{"command": "ls"})
	joined := strings.Join(h.notices(), "\n")
	for _, want := range []string{"exemptRules names typo-id, which match no built-in or user rule; those entries are inert", "path rules audit can never fire with the current access/tools combination", "arming rules bad have an invalid command regex; the rules will never fire"} {
		if strings.Count(joined, want) != 1 {
			t.Errorf("%q said %d times in:\n%s", want, strings.Count(joined, want), joined)
		}
	}
}

func TestWiringLargeOutputSteersOncePerFamily(t *testing.T) {
	c := on(BackendTypeSafe)
	h := startHarness(t, harnessOpts{cfg: c})
	h.ts.set(map[string]float64{"large_output": 0.95, "mutates": 0.05, "irreversible": 0.05})
	h.say("user", "run the tests")
	h.start()
	h.toolCall("bash", "c1", map[string]any{"command": "npm test -- --reporter=spec"})
	h.toolCall("bash", "c2", map[string]any{"command": "CI=1 npm test --verbose"})
	n := 0
	for _, s := range h.steers() {
		if strings.Contains(s.Content, "`npm test` commands may print far more than you need") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("steered %d times: %v", n, h.steers())
	}
}
