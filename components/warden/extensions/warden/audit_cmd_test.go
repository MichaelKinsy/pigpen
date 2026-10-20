package warden

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// hookSet runs a callback when the host receives a call of a method, before the harness answers it.
type hookSet struct {
	mu sync.Mutex
	m  map[string]func()
}

func (hs *hookSet) set(method string, f func()) {
	hs.mu.Lock()
	hs.m[method] = f
	hs.mu.Unlock()
}

func (hs *hookSet) run(method string) {
	hs.mu.Lock()
	f := hs.m[method]
	hs.mu.Unlock()
	if f != nil {
		f()
	}
}

func auditHarness(t *testing.T, o harnessOpts) (*harness, *hookSet, string) {
	t.Helper()
	o.cwd = t.TempDir()
	hs := &hookSet{m: map[string]func(){}}
	o.before = hs.run
	return startHarness(t, o), hs, o.cwd
}

func auditLastNotice(h *harness) string {
	n := h.notices()
	if len(n) == 0 {
		return ""
	}
	return n[len(n)-1]
}

func TestAuditHeadlessNeedsInteractiveSession(t *testing.T) {
	h, _, _ := auditHarness(t, harnessOpts{noUI: true})
	h.host.Command("warden", "audit")
	m := h.messages()
	if len(m) != 1 || m[0].Content != "Audit needs an interactive session to run." || !m[0].Display {
		t.Fatalf("%+v", m)
	}
	if len(h.host.CallsTo("sendUserMessage")) != 0 {
		t.Fatal("sent a prompt")
	}
}

func TestAuditDeclinedSendsNothing(t *testing.T) {
	h, _, cwd := auditHarness(t, harnessOpts{})
	h.confirm = false
	h.host.Command("warden", "audit")
	c := h.host.CallsTo("ui.confirm")
	if len(c) != 1 || c[0].Args["title"] != "Run workspace audit?" || !strings.Contains(c[0].Args["message"].(string), "agent-driven audit of "+Redact(cwd)+".") {
		t.Fatalf("%+v", c)
	}
	if len(h.host.CallsTo("sendUserMessage")) != 0 || len(h.notices()) != 0 {
		t.Fatalf("declined audit did something: %v", h.notices())
	}
}

func runAudit(t *testing.T, before func(report string), during func(report string)) (*harness, string) {
	h, hs, cwd := auditHarness(t, harnessOpts{})
	report := filepath.Join(cwd, ".pi-warden", "audit-report.html")
	if before != nil {
		os.MkdirAll(filepath.Dir(report), 0o755)
		before(report)
	}
	if during != nil {
		hs.set("sendUserMessage", func() { during(report) })
	}
	h.host.Command("warden", "audit")
	return h, report
}

func TestAuditPromptAndWrittenReport(t *testing.T) {
	h, report := runAudit(t, nil, func(r string) {
		os.MkdirAll(filepath.Dir(r), 0o755)
		os.WriteFile(r, []byte("<html>"), 0o644)
	})
	calls := h.host.CallsTo("sendUserMessage")
	if len(calls) != 1 {
		t.Fatalf("%d prompts", len(calls))
	}
	prompt, _ := calls[0].Args["content"].(string)
	if !strings.HasPrefix(prompt, "# Workspace Audit — ") || !strings.Contains(prompt, report) {
		t.Fatalf("prompt %q", prompt)
	}
	n := h.notices()
	if len(n) != 2 || n[0] != "pi-warden: Running workspace audit..." || n[1] != "Audit report written to "+report {
		t.Fatalf("%q", n)
	}
}

func TestAuditStaleReport(t *testing.T) {
	old := time.Now().Add(-time.Hour)
	h, _ := runAudit(t, func(r string) { os.WriteFile(r, []byte("x"), 0o644); os.Chtimes(r, old, old) }, nil)
	if got := auditLastNotice(h); got != "Audit finished but the report file was not updated — the agent may have reported findings in chat instead." {
		t.Fatal(got)
	}
}

func TestAuditMissingReport(t *testing.T) {
	h, _ := runAudit(t, nil, nil)
	if got := auditLastNotice(h); got != "Agent did not write an audit report. The model may have reported findings in chat instead." {
		t.Fatal(got)
	}
}

func TestAuditFailureIsReportedThenOutcomeStillChecked(t *testing.T) {
	h, _, _ := auditHarness(t, harnessOpts{fail: map[string]string{"sendUserMessage": "agent busy"}})
	h.host.Command("warden", "audit")
	n := h.notices()
	if len(n) < 3 || !strings.HasPrefix(n[1], "pi-warden audit failed: ") || !strings.Contains(n[1], "agent busy") || !strings.HasSuffix(n[1], ".") {
		t.Fatalf("%q", n)
	}
	if n[2] != "Agent did not write an audit report. The model may have reported findings in chat instead." {
		t.Fatalf("%q", n)
	}
}

func TestAuditBlocksUserInputWhileRunning(t *testing.T) {
	h, hs, _ := auditHarness(t, harnessOpts{})
	var during, ext, after string
	hs.set("waitForIdle", func() {
		during = string(h.host.Fire("input", map[string]any{"text": "hi", "source": "interactive"}))
		ext = string(h.host.Fire("input", map[string]any{"text": "audit prompt", "source": "extension"}))
	})
	h.host.Command("warden", "audit")
	after = string(h.host.Fire("input", map[string]any{"text": "hi", "source": "interactive"}))
	if !strings.Contains(during, `"handled"`) {
		t.Fatalf("during: %q", during)
	}
	if strings.Contains(ext, "handled") || strings.Contains(after, "handled") {
		t.Fatalf("ext %q after %q", ext, after)
	}
	found := false
	for _, n := range h.notices() {
		found = found || n == "pi-warden is running an audit. Please wait..."
	}
	if !found {
		t.Fatalf("%q", h.notices())
	}
}
