package powerline_footer_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	powerline "github.com/MichaelKinsy/pigpen/powerline-footer"
)

// The queue through the real SDK against the fake host: the /queue command, the events that deliver after a compaction, the
// picker overlay's host call and the queue preview widget.

type queueSDKRig struct {
	h       *Host
	agent   string
	idle    atomic.Bool
	custom  []map[string]any // the host's answers to ui.custom, in order (the user's choices)
	customN atomic.Int32
	onSend  func(text string)
}

type queueSDKOptions struct {
	inbox  []string
	idle   bool
	custom []map[string]any
	// onSend runs inside the host's answer to sendUserMessage, as a host that starts the message before it answers does.
	onSend func(r *queueSDKRig, text string)
}

func inboxLine(id, text string, created int, extra string) string {
	return `{"id":"` + id + `","text":"` + text + `","createdAt":` + itoa(created) + `,"updatedAt":` + itoa(created) +
		`,"source":{"cwd":"/work/proj"},"target":{"kind":"global"},"intent":"follow-up","status":"queued"` + extra + `}`
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

func startQueueHost(t *testing.T, opts queueSDKOptions) *queueSDKRig {
	t.Helper()
	home := t.TempDir()
	agent := filepath.Join(home, "agent")
	t.Setenv("HOME", home)
	t.Setenv("PI_CODING_AGENT_DIR", agent)
	t.Setenv("POWERLINE_NERD_FONTS", "0")
	os.MkdirAll(filepath.Join(agent, "powerline-footer"), 0o755)
	os.WriteFile(filepath.Join(agent, "settings.json"), []byte(`{"powerline":{"welcome":false}}`), 0o644)
	if len(opts.inbox) > 0 {
		os.WriteFile(filepath.Join(agent, "powerline-footer", "inbox.jsonl"), []byte(strings.Join(opts.inbox, "\n")+"\n"), 0o644)
	}
	r := &queueSDKRig{agent: agent, custom: opts.custom}
	r.idle.Store(opts.idle)
	var once sync.Once
	ready := make(chan struct{})
	r.h = StartHost(t, powerline.Extension(), HostOptions{Mode: "tui", OnCall: func(method string, args map[string]any) (map[string]any, string) {
		switch method {
		case "getSessionID":
			return map[string]any{"sessionId": "sdk-session"}, ""
		case "isIdle":
			return map[string]any{"idle": r.idle.Load()}, ""
		case "getContextUsage":
			return nil, "no usage yet"
		case "ui.custom":
			n := int(r.customN.Add(1)) - 1
			if n < len(r.custom) && r.custom[n] != nil {
				return CustomResult(r.custom[n]), ""
			}
			return map[string]any{"ok": false}, ""
		case "sendUserMessage":
			if opts.onSend != nil {
				once.Do(func() { <-ready })
				opts.onSend(r, args["content"].(string))
			}
		}
		return map[string]any{}, ""
	}})
	close(ready)
	r.h.Fire("session_start", map[string]any{"reason": "startup"})
	return r
}

func (r *queueSDKRig) sent() []map[string]any {
	var out []map[string]any
	for _, c := range r.h.CallsTo("sendUserMessage") {
		opts, _ := c.Args["options"].(map[string]any)
		out = append(out, map[string]any{"content": c.Args["content"], "deliverAs": opts["deliverAs"]})
	}
	return out
}

func (r *queueSDKRig) waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestQueueIsRegisteredAndNotRefused(t *testing.T) {
	r := startQueueHost(t, queueSDKOptions{})
	before := len(notifications(r.h))
	if failure := r.h.Command("queue", "alias work /somewhere"); failure != "" {
		t.Fatal(failure)
	}
	got := notifications(r.h)
	if len(got) != before+1 || got[len(got)-1] != "info: Alias @work → /somewhere" {
		t.Errorf("notifications = %q", got)
	}
	if failure := r.h.Command("queue", ""); failure != "" {
		t.Fatal(failure)
	}
	if got := notifications(r.h); got[len(got)-1] != "info: No queued items" {
		t.Errorf("an empty picker: %q", got)
	}
	if len(r.h.CallsTo("ui.custom")) != 0 {
		t.Error("an empty queue opened an overlay")
	}
}

func TestQueueSendDeliversThroughTheHost(t *testing.T) {
	r := startQueueHost(t, queueSDKOptions{idle: true, inbox: []string{inboxLine("aaaa1111", "run the tests", 1, "")}})
	if failure := r.h.Command("queue", "send"); failure != "" {
		t.Fatal(failure)
	}
	r.waitFor(t, "the host call", func() bool { return len(r.sent()) == 1 })
	sent := r.sent()
	if len(sent) != 1 || sent[0]["content"] != "run the tests" || sent[0]["deliverAs"] != nil && sent[0]["deliverAs"] != "" {
		t.Fatalf("sent = %v, want one plain prompt", sent)
	}
	r.h.Fire("before_agent_start", agentStart("run the tests"))
	got := notifications(r.h)
	if got[len(got)-1] != "info: Sent queued item aaaa1111" {
		t.Errorf("notifications = %q", got)
	}
	data, _ := os.ReadFile(filepath.Join(r.agent, "powerline-footer", "inbox.jsonl"))
	if !strings.Contains(string(data), `"status":"sent"`) {
		t.Errorf("inbox = %s", data)
	}
}

func TestQueueSendToABusySessionIsAFollowUp(t *testing.T) {
	r := startQueueHost(t, queueSDKOptions{inbox: []string{inboxLine("bbbb2222", "later", 1, "")}})
	r.h.Command("queue", "send bbbb")
	r.waitFor(t, "the host call", func() bool { return len(r.sent()) == 1 })
	sent := r.sent()
	if len(sent) != 1 || sent[0]["deliverAs"] != "followUp" {
		t.Errorf("sent = %v, want deliverAs followUp", sent)
	}
}

// The host may start the message, and so deliver before_agent_start, before it answers the sendUserMessage call. The queue must
// not hold its lock across that call, or the event handler waits for the lock the call is waiting to release.
func TestQueueDeliveryDoesNotDeadlockWhenTheHostStartsTheMessageFirst(t *testing.T) {
	// The host delivers the event to every handler of the extension while the sendUserMessage call is open; the test ends only after
	// that, or the delivery would outlive the host.
	started := make(chan struct{})
	r := startQueueHost(t, queueSDKOptions{idle: true, inbox: []string{inboxLine("cccc3333", "start me", 1, "")},
		onSend: func(r *queueSDKRig, text string) {
			defer close(started)
			r.h.Fire("before_agent_start", agentStart(text))
		}})
	defer func() {
		select {
		case <-started:
		case <-time.After(8 * time.Second):
			t.Error("the host never finished delivering before_agent_start")
		}
	}()
	done := make(chan string, 1)
	go func() { done <- r.h.Command("queue", "send") }()
	select {
	case failure := <-done:
		if failure != "" {
			t.Fatal(failure)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("/queue send deadlocked")
	}
	r.waitFor(t, "the acknowledgement", func() bool {
		got := notifications(r.h)
		return len(got) > 0 && got[len(got)-1] == "info: Sent queued item cccc3333"
	})
	got := notifications(r.h)
	if got[len(got)-1] != "info: Sent queued item cccc3333" {
		t.Errorf("notifications = %q", got)
	}
}

func TestQueuePickerOpensAnOverlayAndActsOnTheChoice(t *testing.T) {
	r := startQueueHost(t, queueSDKOptions{idle: true, inbox: []string{inboxLine("dddd4444", "pick me", 1, "")},
		custom: []map[string]any{
			{"value": "dddd4444", "label": "dddd4444 follow-up pick me", "description": "global"},
			{"value": "send", "label": "Send to current session", "description": "Deliver as prompt/follow-up"},
		}})
	if failure := r.h.Command("queue", ""); failure != "" {
		t.Fatal(failure)
	}
	calls := r.h.CallsTo("ui.custom")
	if len(calls) != 2 {
		t.Fatalf("ui.custom calls = %d, want the list and the actions", len(calls))
	}
	if calls[0].Args["overlay"] != true {
		t.Errorf("the list is not an overlay: %v", calls[0].Args)
	}
	if oo, _ := calls[0].Args["overlayOptions"].(map[string]any); oo["anchor"] != "center" {
		t.Errorf("overlayOptions = %v, want centered", calls[0].Args["overlayOptions"])
	}
	r.waitFor(t, "the host call", func() bool { return len(r.sent()) == 1 })
	if sent := r.sent(); len(sent) != 1 || sent[0]["content"] != "pick me" {
		t.Errorf("sent = %v", sent)
	}
}

func TestQueuePickerCancelledDoesNothing(t *testing.T) {
	r := startQueueHost(t, queueSDKOptions{idle: true, inbox: []string{inboxLine("eeee5555", "stay", 1, "")}})
	if failure := r.h.Command("queue", ""); failure != "" {
		t.Fatal(failure)
	}
	if len(r.h.CallsTo("ui.custom")) != 1 || len(r.sent()) != 0 {
		t.Errorf("custom=%d sent=%v", len(r.h.CallsTo("ui.custom")), r.sent())
	}
}

func TestQueuePreviewWidgetAndSegmentShowTheQueue(t *testing.T) {
	r := startQueueHost(t, queueSDKOptions{inbox: []string{inboxLine("ffff6666", "fix   the build", 1, "")}})
	preview, options := lastWidget(t, r.h, "powerline-queue-preview")
	if preview != " queued: fix the build" || options["placement"] != "belowEditor" {
		t.Errorf("preview = %q %v", preview, options)
	}
	if top, _ := lastWidget(t, r.h, "powerline-top"); !strings.Contains(top, "q 1") {
		t.Errorf("top bar %q lacks the queue segment", top)
	}
	if _, options := lastWidget(t, r.h, "powerline-pending-send"); options["placement"] != "belowEditor" {
		t.Errorf("pending-send placement %v", options)
	}
	r.h.Command("queue", "clear all")
	if preview, _ := lastWidget(t, r.h, "powerline-queue-preview"); preview != "" {
		t.Errorf("preview after clear = %q", preview)
	}
}

func TestQueueDeliversAfterACompactionThroughTheEvents(t *testing.T) {
	r := startQueueHost(t, queueSDKOptions{idle: true, inbox: []string{
		`{"id":"abab7777","text":"after compact","createdAt":1,"updatedAt":1,"source":{"cwd":"/work/proj","sessionId":"sdk-session"},"target":{"kind":"current-session"},"intent":"post-compact","status":"queued"}`}})
	r.h.Fire("session_before_compact", nil)
	r.h.Fire("session_compact", map[string]any{})
	r.waitFor(t, "the post-compaction message", func() bool { return len(r.sent()) == 1 })
	if sent := r.sent(); sent[0]["content"] != "after compact" {
		t.Errorf("sent = %v", sent)
	}
}

func TestQueueFailedCompactionBlocksTheQueuedItems(t *testing.T) {
	r := startQueueHost(t, queueSDKOptions{idle: true, inbox: []string{
		`{"id":"cdcd8888","text":"after compact","createdAt":1,"updatedAt":1,"source":{"cwd":"/work/proj","sessionId":"sdk-session"},"target":{"kind":"current-session"},"intent":"post-compact","status":"queued"}`}})
	r.h.Fire("session_before_compact", nil)
	r.h.Fire("session_compact_failed", map[string]any{"errorMessage": "model refused"})
	got := notifications(r.h)
	if got[len(got)-1] != "warning: Kept 1 post-compaction item blocked" {
		t.Errorf("notifications = %q", got)
	}
	if preview, _ := lastWidget(t, r.h, "powerline-queue-preview"); preview != " blocked: after compact" {
		t.Errorf("preview = %q", preview)
	}
	time.Sleep(150 * time.Millisecond)
	if len(r.sent()) != 0 {
		t.Errorf("a blocked item was sent: %v", r.sent())
	}
}

func TestQueueShutdownQueuesAnUnstartedMessageAgain(t *testing.T) {
	r := startQueueHost(t, queueSDKOptions{idle: true, inbox: []string{inboxLine("efef9999", "never starts", 1, "")}})
	r.h.Command("queue", "send")
	r.waitFor(t, "the host call", func() bool { return len(r.sent()) == 1 })
	r.h.Fire("session_shutdown", map[string]any{"reason": "quit"})
	data, _ := os.ReadFile(filepath.Join(r.agent, "powerline-footer", "inbox.jsonl"))
	if !strings.Contains(string(data), `"status":"queued"`) || !strings.Contains(string(data), "Session ended before queued message started") {
		t.Errorf("inbox = %s", data)
	}
}

// agentStart is a before_agent_start event: the SDK requires the system prompt options.
func agentStart(prompt string) map[string]any {
	return map[string]any{"prompt": prompt, "systemPrompt": "", "systemPromptOptions": map[string]any{}}
}

// pi.sendUserMessage does not wait for the message to start, so /queue send answers at once even when the host is slow to
// accept the message (Pi's response precedes the events of the message it starts; pigeq scenario queue-send-idle).
func TestQueueSendDoesNotWaitForTheHostToAcceptTheMessage(t *testing.T) {
	release := make(chan struct{})
	r := startQueueHost(t, queueSDKOptions{idle: true, inbox: []string{inboxLine("a9a9a9a9", "slow host", 1, "")},
		onSend: func(r *queueSDKRig, text string) { <-release }})
	defer func() {
		close(release)
		r.waitFor(t, "the host call to finish", func() bool { return len(r.sent()) == 1 })
		r.h.Fire("session_shutdown", map[string]any{"reason": "quit"}) // settles the inbox before the directory is removed
	}()
	done := make(chan string, 1)
	go func() { done <- r.h.Command("queue", "send") }()
	select {
	case failure := <-done:
		if failure != "" {
			t.Fatal(failure)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("/queue send waited for the host's answer to sendUserMessage")
	}
	r.waitFor(t, "the host call", func() bool { return len(r.sent()) == 1 })
}

func TestQueueRegistersItsEvents(t *testing.T) {
	r := startQueueHost(t, queueSDKOptions{})
	for _, ev := range []string{"session_before_compact", "session_compact", "session_compact_failed", "agent_settled", "agent_end", "before_agent_start", "message_start", "session_shutdown"} {
		if !r.h.Registered(ev) {
			t.Errorf("no handler for %s", ev)
		}
	}
}
