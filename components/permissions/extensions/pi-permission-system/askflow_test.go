package pi_permission_system_test

// The ask dialog through the SDK adapter, against the shape of config a real session uses: a universal ask with
// the day's tools allowed and the external-directory surfaces gating writes.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pps "github.com/MichaelKinsy/pigpen/pi-permission-system"
)

// harnessConfig mirrors the config the pigpen harness runs with: everything the session allows by name, an ask for
// anything else, an ask on the external boundary, and bash allowed.
const harnessConfig = `{
  "$schema": "https://example.invalid/permissions.schema.json",
  "permission": {
    "*": "ask",
    "read": "allow",
    "grep": "allow",
    "find": "allow",
    "ls": "allow",
    "todo": "allow",
    "codemode": "allow",
    "edit": "allow",
    "write": "allow",
    "external_directory_read": "allow",
    "external_directory_write": "ask",
    "external_directory": "ask",
    "bash": "allow"
  }
}`

func harnessRig(t *testing.T) *adapterRig {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	agentDirForTest = dir
	os.MkdirAll(filepath.Dir(configPath()), 0o755)
	if err := os.WriteFile(configPath(), []byte(harnessConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	r := rigWith(t, harnessConfig, "read", "bash", "edit", "write", "todo")
	r.h.Fire("session_start", map[string]any{})
	return r
}

func selects(r *adapterRig) []map[string]any { return r.uiCalls("ui.select") }

// The $schema key and the config parse; the allowed tools never prompt, and anything else asks.
func TestHarnessConfigAllowedToolsDoNotPrompt(t *testing.T) {
	r := harnessRig(t)
	cwd := r.h.opts.Cwd
	for _, c := range []struct {
		tool  string
		input map[string]any
	}{
		{"read", map[string]any{"path": "notes.txt"}},
		{"read", map[string]any{"path": filepath.Join(cwd, "skills", "demo", "SKILL.md")}},
		{"edit", map[string]any{"path": filepath.Join(cwd, "a.txt")}},
		{"bash", map[string]any{"command": "git status"}},
		{"bash", map[string]any{"command": "ls -la | wc -l"}},
		{"todo", nil},
	} {
		blocked, reason := r.toolCall(c.tool, c.input)
		if blocked {
			t.Errorf("%s %v: blocked: %s", c.tool, c.input, reason)
		}
	}
	eq(t, len(selects(r)), 0, "no dialog for the allowed calls")
	// An external read is allowed by external_directory_read.
	blocked, _ := r.toolCall("read", map[string]any{"path": filepath.Join(r.h.opts.Cwd, "..", "outside.txt")})
	eq(t, blocked, false, "an external read")
	eq(t, len(selects(r)), 0, "still no dialog")
}

// An unlisted tool asks; the dialog decides it, and "for this session" records a grant that answers the next call.
func TestHarnessConfigAskAndSessionApproval(t *testing.T) {
	r := harnessRig(t)
	// Dismissed: denied, fail-closed, with the port's user-denial text.
	blocked, reason := r.toolCall("mystery_tool", nil)
	eq(t, blocked, true, "a dismissed dialog denies")
	eq(t, reason, "[pi-permission-system] The user denied this 'mystery_tool' call.", "denial text")
	calls := selects(r)
	eq(t, len(calls), 1, "one dialog")
	eq(t, calls[0]["title"], "pi-permission-system: mystery_tool", "title")
	opts := calls[0]["options"].([]any)
	eq(t, len(opts), 4, "approve, the session option, deny, deny with a reason")
	eq(t, opts[1], `Yes, allow tool "mystery_tool" for this session`, "the session option's label")

	// Approve for this session: allowed now and on the next call, without a dialog.
	r.selectAnswer = opts[1].(string)
	blocked, _ = r.toolCall("mystery_tool", nil)
	eq(t, blocked, false, "approved for the session")
	blocked, _ = r.toolCall("mystery_tool", nil)
	eq(t, blocked, false, "the session grant answers the next call")
	eq(t, len(selects(r)), 2, "one more dialog, none for the granted call")

	// A new session forgets the grant.
	r.h.Fire("session_start", map[string]any{})
	r.selectAnswer = nil
	blocked, _ = r.toolCall("mystery_tool", nil)
	eq(t, blocked, true, "a new session asks again")
}

// Deny with a reason carries the human's text to the model.
func TestHarnessConfigDenyWithReason(t *testing.T) {
	r := harnessRig(t)
	r.selectAnswer = "Deny with a reason"
	r.inputs = []any{"not now"}
	blocked, reason := r.toolCall("mystery_tool", nil)
	eq(t, blocked, true, "denied")
	eq(t, reason, "[pi-permission-system] The user denied this 'mystery_tool' call. Reason: not now.", "the reason rides the denial")
}

// Approving once allows that call only.
func TestHarnessConfigApproveOnce(t *testing.T) {
	r := harnessRig(t)
	r.selectAnswer = "Approve"
	blocked, _ := r.toolCall("mystery_tool", nil)
	eq(t, blocked, false, "approved")
	r.selectAnswer = nil // the dialog would approve again; the point is that a grant was not recorded
	blocked, _ = r.toolCall("mystery_tool", nil)
	eq(t, blocked, true, "approve is not a session grant")
}

// An external write asks on the external_directory_write surface, naming the path; a session grant covers the path.
func TestHarnessConfigExternalWriteAsk(t *testing.T) {
	r := harnessRig(t)
	outside := filepath.Join(r.h.opts.Cwd, "..", "outside.txt")
	blocked, _ := r.toolCall("write", map[string]any{"path": outside})
	eq(t, blocked, true, "an external write asks, and the dismissed dialog denies")
	calls := selects(r)
	eq(t, len(calls), 1, "one dialog")
	eq(t, calls[0]["title"], "pi-permission-system: external_directory_write: "+outside, "the surface and the path")
	opts := calls[0]["options"].([]any)
	eq(t, opts[1], `Yes, allow access to external directory "`+outside+`" for this session`, "the session option names the path")
	r.selectAnswer = opts[1].(string)
	blocked, _ = r.toolCall("write", map[string]any{"path": outside})
	eq(t, blocked, false, "approved for the session")
	blocked, _ = r.toolCall("write", map[string]any{"path": outside})
	eq(t, blocked, false, "the path grant answers the next write")
	// A different external path still asks.
	r.selectAnswer = nil
	blocked, _ = r.toolCall("write", map[string]any{"path": filepath.Join(r.h.opts.Cwd, "..", "other.txt")})
	eq(t, blocked, true, "another path asks")
}

// A wrapper's allow is floored to ask; an external write asks; a plain allow decides a command the port cannot
// read, exactly as the original decides it.
func TestHarnessConfigBashFloors(t *testing.T) {
	r := harnessRig(t)
	// A wrapper whose payload writes is floored to ask, and the dismissed dialog denies.
	blocked, _ := r.toolCall("bash", map[string]any{"command": "sudo rm x"})
	eq(t, blocked, true, "a wrapper around a writer asks")
	calls := selects(r)
	eq(t, calls[0]["title"], "pi-permission-system: bash: sudo rm x", "the human sees the command")
	eq(t, calls[0]["options"].([]any)[1], `Yes, allow bash "sudo *" for this session`, "the session grant names the wrapper's prefix")

	// An indirection around a pure reader is exempt and allowed; a non-wrapper command is decided by the rules
	// alone; and a command the port cannot read is allowed by the plain allow, as the original allows it.
	for _, cmd := range []string{"sudo ls", "ssh host ls", `echo "hi" > out.txt`, "echo $(hostname)"} {
		blocked, _ = r.toolCall("bash", map[string]any{"command": cmd})
		eq(t, blocked, false, "allowed by the plain allow: "+cmd)
	}

	// A command with an external path: reads are allowed, writes ask.
	blocked, _ = r.toolCall("bash", map[string]any{"command": "cat " + filepath.Join(r.h.opts.Cwd, "..", "hosts")})
	eq(t, blocked, false, "an external read is allowed")
	blocked, _ = r.toolCall("bash", map[string]any{"command": "touch " + filepath.Join(r.h.opts.Cwd, "..", "new.txt")})
	eq(t, blocked, true, "an external write asks")
	// The typed path's session grant answers the next external write.
	opts := selects(r)[len(selects(r))-1]["options"].([]any)
	r.selectAnswer = opts[1].(string)
	blocked, _ = r.toolCall("bash", map[string]any{"command": "touch " + filepath.Join(r.h.opts.Cwd, "..", "new.txt")})
	eq(t, blocked, false, "the path grant answers the next external write")
}

// Without a UI no dialog can answer, and the refusal says so.
func TestAskWithoutUI(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	agentDirForTest = dir
	os.MkdirAll(filepath.Dir(configPath()), 0o755)
	os.WriteFile(configPath(), []byte(`{"permission":{"*":"ask"}}`), 0o644)
	mode := "json"
	h := StartHost(t, pps.Extension(), HostOptions{Mode: mode})
	r := &adapterRig{h: h}
	h.Fire("session_start", map[string]any{})
	blocked, reason := r.toolCall("foo", nil)
	eq(t, blocked, true, "blocked")
	eq(t, strings.Contains(reason, "requires approval, but no interactive UI is available"), true, "the unavailable text")
	eq(t, len(selects(r)), 0, "no dialog was shown")
}

// The tool_call reply stays in the shape the host reads.
func TestAskToolCallReplyShape(t *testing.T) {
	r := harnessRig(t)
	raw := r.h.Fire("tool_call", map[string]any{"toolName": "mystery_tool", "input": map[string]any{}, "toolCallId": "c1"})
	var res struct {
		Block  bool   `json:"block"`
		Reason string `json:"reason"`
	}
	if len(raw) == 0 {
		t.Fatal("no result")
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	eq(t, res.Block, true, "a dismissed dialog blocks")
}
