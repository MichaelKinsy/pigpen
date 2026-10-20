package warden

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Layer-1 tests of the wiring of the three tools, the standing-preference injection, the open-loop lifecycle and the
// compaction appendix, through the fake host. Twins of the cases of tests/extension.test.ts that exercise them.

// toolSession is the session the fake host serves: its id, directory, file and branch, and how often the directory
// was asked for.
type toolSession struct {
	mu        sync.Mutex
	id        string
	dir, file string
	branch    []map[string]any
	dirReads  int
}

func (s *toolSession) answer(method string, args map[string]any) (map[string]any, string, bool) {
	if method != "sessionRead" {
		return nil, "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch args["method"] {
	case "getSessionId":
		return map[string]any{"__value": s.id}, "", true
	case "getSessionDir":
		s.dirReads++
		return map[string]any{"__value": s.dir}, "", true
	case "getSessionFile":
		if s.file == "" {
			return map[string]any{"__value": nil}, "", true
		}
		return map[string]any{"__value": s.file}, "", true
	case "getBranch":
		branch := s.branch
		if branch == nil {
			branch = []map[string]any{}
		}
		return map[string]any{"__value": branch}, "", true
	}
	return nil, "", false
}

func (s *toolSession) set(f func(*toolSession)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(s)
}

func (s *toolSession) reads() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dirReads
}

// fixedNow is the fake clock the preference rules count calendar days against.
var fixedNow = func() time.Time { return time.UnixMilli(rulesNow) }

type toolsEnv struct {
	*harness
	sess *toolSession
}

func enabledConfig(mutate ...func(*Config)) *Config {
	c := DefaultConfig()
	c.Enabled, c.Backend, c.Consent = true, BackendNone, BackendNone
	for _, m := range mutate {
		m(&c)
	}
	return &c
}

func startTools(t *testing.T, cfg *Config, session string) *toolsEnv {
	t.Helper()
	s := &toolSession{id: session}
	h := startHarness(t, harnessOpts{cfg: cfg, noUI: false, answer: s.answer, now: fixedNow, cwd: t.TempDir()})
	return &toolsEnv{harness: h, sess: s}
}

func (e *toolsEnv) toolText(name string, params map[string]any) string {
	e.t.Helper()
	raw, failure := e.host.Tool(name, params)
	if failure != "" {
		e.t.Fatalf("%s failed: %s", name, failure)
	}
	var result struct {
		Content any `json:"content"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		e.t.Fatalf("%s result %s: %v", name, raw, err)
	}
	switch c := result.Content.(type) {
	case string:
		return c
	case []any:
		var parts []string
		for _, p := range c {
			if m, ok := p.(map[string]any); ok {
				s, _ := m["text"].(string)
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "\n")
	}
	return string(raw)
}

func (e *toolsEnv) loops(params map[string]any) string { return e.toolText("warden_loops", params) }

func (e *toolsEnv) messagesOf(customType string) []sent {
	var out []sent
	for _, m := range e.messages() {
		if m.Type == customType {
			out = append(out, m)
		}
	}
	return out
}

func lastNotice(t *testing.T, e *toolsEnv) string {
	t.Helper()
	// A command reports through ui.notify with a UI, and the tests start with one.
	n := e.notices()
	if len(n) == 0 {
		t.Fatal("no notice")
	}
	return n[len(n)-1]
}

// twin: tests/extension.test.ts:198 (the tools are registered; their schemas are compared with the original by pigeq)
func TestToolsAreRegisteredOnceWithTheOriginalsNames(t *testing.T) {
	e := startTools(t, enabledConfig(), "s")
	for _, name := range []string{"warden_remember", "warden_loops", "warden_recall"} {
		if !e.host.tools[name] {
			t.Errorf("%s is not registered", name)
		}
	}
	if len(e.host.tools) != 3 {
		t.Errorf("tools %v", e.host.tools)
	}
	if !e.host.Registered("session_compact") {
		t.Error("no session_compact handler")
	}
}

// twin: tests/extension.test.ts:4632
func TestWardenLoopsToolAddDoneDropListAndTheCommand(t *testing.T) {
	e := startTools(t, enabledConfig(), "session-a")
	e.start()
	for _, c := range []struct {
		params map[string]any
		want   string
	}{
		{map[string]any{"action": "add", "text": "Bump the version", "when": "after CI passes"}, "added #1: Bump the version (when: after CI passes)"},
		{map[string]any{"action": "add", "text": "Rerun the flaky upload test"}, "added #2: Rerun the flaky upload test"},
		{map[string]any{"action": "add", "text": "Answer the review thread"}, "added #3: Answer the review thread"},
		{map[string]any{"action": "done", "id": float64(2)}, "done #2: Rerun the flaky upload test"},
		{map[string]any{"action": "drop", "id": float64(3), "reason": "the thread was resolved"}, "dropped #3: Answer the review thread"},
		{map[string]any{"action": "list"}, "Open loops:\n- #1 Bump the version (when: after CI passes)\n(2 closed)"},
	} {
		if got := e.loops(c.params); got != c.want {
			t.Errorf("%v: %q, want %q", c.params, got, c.want)
		}
	}
	e.host.Command("warden", "loops")
	mustMatchRe(t, lastNotice(t, e), `^Open loops \(1\):\n- #1 Bump the version \(when: after CI passes\)\nClosed \(2\):`)
	if len(e.ts.reqs()) != 0 {
		t.Error("a judgment request was made")
	}
}

// twin: tests/extension.test.ts:4651
func TestWardenLoopsAgentEndNoticeComesOncePerUnchangedListAsALoopsSteer(t *testing.T) {
	e := startTools(t, enabledConfig(), "session-notice")
	e.start()
	e.prompt("Ship the release")
	e.loops(map[string]any{"action": "add", "text": "Bump the version", "when": "after CI passes"})
	agentEnd := func(text string) {
		e.host.Fire("agent_end", map[string]any{"messages": []any{map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": text}}}}})
	}
	notices := func() []sent {
		var out []sent
		for _, m := range e.steers() {
			if strings.Contains(m.Content, "open loop") {
				out = append(out, m)
			}
		}
		return out
	}
	agentEnd("CI is running; I will bump the version after it passes.")
	if len(notices()) != 1 {
		t.Fatalf("notices %d", len(notices()))
	}
	mustMatchRe(t, notices()[0].Content, `^pi-warden: 1 open loop you promised in this session:\n- #1 Bump the version \(when: after CI passes\)\n`)
	if notices()[0].Options["deliverAs"] != "nextTurn" {
		t.Errorf("it starts a turn of its own: %v", notices()[0].Options)
	}
	e.prompt("status?")
	agentEnd("Still waiting for CI.")
	if len(notices()) != 1 {
		t.Errorf("the same unchanged list is named again: %d", len(notices()))
	}
	e.loops(map[string]any{"action": "add", "text": "Tag the release"})
	e.prompt("go on")
	agentEnd("Waiting.")
	if len(notices()) != 2 {
		t.Errorf("a changed list is not named again: %d", len(notices()))
	}
	e.loops(map[string]any{"action": "done", "id": float64(1)})
	e.loops(map[string]any{"action": "done", "id": float64(2)})
	e.prompt("done?")
	agentEnd("All done.")
	if len(notices()) != 2 {
		t.Errorf("a notice without open loops: %d", len(notices()))
	}
}

// twin: tests/extension.test.ts:4680
func TestWardenLoopsSurviveAResumeWhichListsThemOnceANewSessionDoesNot(t *testing.T) {
	e := startTools(t, enabledConfig(), "session-resume")
	e.start()
	e.loops(map[string]any{"action": "add", "text": "Bump the version", "when": "after CI passes"})
	e.host.Fire("session_start", map[string]any{"reason": "resume"})
	listed := e.messagesOf(loopsType)
	want := "Open loops from earlier in this session (warden_loops):\n- #1 Bump the version (when: after CI passes)\nClose each with warden_loops done or drop once it is finished or no longer needed."
	if len(listed) != 1 || listed[0].Content != want {
		t.Fatalf("listed %+v", listed)
	}
	if got := e.loops(map[string]any{"action": "list"}); got != "Open loops:\n- #1 Bump the version (when: after CI passes)" {
		t.Errorf("list %q", got)
	}
	// The branch already carries the same list: a second resume says nothing.
	e.sess.set(func(s *toolSession) {
		s.branch = []map[string]any{{"type": "custom_message", "customType": loopsType, "content": want}}
	})
	before := len(e.messagesOf(loopsType))
	e.host.Fire("session_start", map[string]any{"reason": "resume"})
	if got := len(e.messagesOf(loopsType)); got != before {
		t.Errorf("the branch already carries the same list, but %d more messages were sent", got-before)
	}
	// A new session of the same project, started fresh, lists nothing.
	e.sess.set(func(s *toolSession) { s.id, s.branch = "session-other", nil })
	e.host.Fire("session_start", map[string]any{"reason": "startup"})
	if got := len(e.messagesOf(loopsType)); got != before {
		t.Errorf("a new session announced loops: %d", got-before)
	}
}

// twin: tests/extension.test.ts:4703
func TestWardenLoopsOfSessionAAreInvisibleInSessionBAndInAnotherProject(t *testing.T) {
	e := startTools(t, enabledConfig(), "session-a")
	e.start()
	e.loops(map[string]any{"action": "add", "text": "Bump the version"})
	e.sess.set(func(s *toolSession) { s.id = "session-b" })
	e.start()
	if got := e.loops(map[string]any{"action": "list"}); got != "No open loops." {
		t.Errorf("session b: %q", got)
	}
	if got := e.loops(map[string]any{"action": "done", "id": float64(1)}); got != "no loop #1 in this session" {
		t.Errorf("session b done: %q", got)
	}
	// The same session id in another project: a second harness shares the data folder through the same home.
	other := startToolsInHome(t, enabledConfig(), "session-a", e.home, t.TempDir())
	other.start()
	if got := other.loops(map[string]any{"action": "list"}); got != "No open loops." {
		t.Errorf("another project: %q", got)
	}
	before := len(other.messages())
	other.host.Fire("agent_end", map[string]any{"messages": []any{}})
	for _, m := range other.messages()[before:] {
		if strings.Contains(m.Content, "open loop") {
			t.Errorf("an open-loop notice in another project: %q", m.Content)
		}
	}
	e.sess.set(func(s *toolSession) { s.id = "session-a" })
	e.start()
	if got := e.loops(map[string]any{"action": "list"}); got != "Open loops:\n- #1 Bump the version" {
		t.Errorf("session a again: %q", got)
	}
	e.sess.set(func(s *toolSession) { s.id = "" })
	if got := e.loops(map[string]any{"action": "list"}); got != "not available: this session has no id to keep loops under" {
		t.Errorf("no id: %q", got)
	}
}

// startToolsInHome starts a second extension that shares the config home (so the data folder) of the first.
func startToolsInHome(t *testing.T, cfg *Config, session, home, cwd string) *toolsEnv {
	t.Helper()
	s := &toolSession{id: session}
	h := startHarness(t, harnessOpts{cfg: cfg, answer: s.answer, now: fixedNow, cwd: cwd, home: home})
	return &toolsEnv{harness: h, sess: s}
}

// twin: tests/extension.test.ts:4729 and tests/extension.test.ts:3895 (failed attempts and the verification state survive compaction)
func TestCompactionAppendixCarriesOpenLoopsAndFailedAttemptsAndRecallPrintsItsSections(t *testing.T) {
	e := startTools(t, enabledConfig(), "session-compact")
	e.start()
	e.say("user", "Fix the build")
	e.prompt("Fix the build")
	build := "src/a.ts(3,7): error TS2322: Type 'string' is not assignable to type 'number'.\nFound 1 error."
	e.toolResult("bash", map[string]any{"command": "npm run build"}, build, true)
	e.toolResult("bash", map[string]any{"command": "npm run build"}, build, true)
	e.toolResult("bash", map[string]any{"command": "npm test"}, "all 42 tests passed", false)
	e.toolResult("edit", map[string]any{"path": filepath.Join(e.host.opts.Cwd, "src/a.ts"), "edits": []any{}}, "Edited src/a.ts", false)
	e.loops(map[string]any{"action": "add", "text": "Rerun the build", "when": "after the type fix"})
	e.host.Fire("session_compact", map[string]any{"reason": "manual", "fromExtension": false, "willRetry": false})
	sentEvidence := e.messagesOf("pi-warden-compact-evidence")
	if len(sentEvidence) != 1 {
		t.Fatalf("one message per compaction, got %d", len(sentEvidence))
	}
	appendix := sentEvidence[0].Content
	mustMatchRe(t, appendix, `### Open loops\n.*\n- #1 Rerun the build \(when: after the type fix\)`)
	mustMatchRe(t, appendix, `### Tried and failed\n.*\n- npm run build → src/a\.ts\(3,7\): error TS2322`)
	if strings.Count(appendix, "- npm run build →") != 1 {
		t.Errorf("the repeated call is listed once:\n%s", appendix)
	}
	mustMatchRe(t, appendix, `- last passing check: npm test; code changed since last passing check: yes`)
	mustNotMatchRe(t, appendix, `undefined`)
	recall := e.toolText("warden_recall", map[string]any{})
	sections := strings.Split(recall, "\n\n")
	var heads []string
	for _, s := range sections {
		heads = append(heads, strings.SplitN(s, "\n", 2)[0])
		if !strings.Contains(appendix, s) {
			t.Errorf("the appendix does not carry the same text: %s", s)
		}
	}
	if strings.Join(heads, "|") != "### Tried and failed|### Verification" {
		t.Errorf("sections %v", heads)
	}
	mustMatchRe(t, recall, `npm run build → src/a\.ts\(3,7\): error TS2322`)
	if len(e.ts.reqs()) != 0 {
		t.Error("recall and loops made a judgment request")
	}
}

type fakeLedger struct{ saved []SavedOutput }

func (l fakeLedger) storedOutputs() []SavedOutput { return l.saved }

type fakeHolds struct{ held []HeldEntry }

func (h fakeHolds) heldRecords() []HeldEntry { return h.held }

// twin: tests/extension.test.ts:3866 (the appendix includes the saved output, the failed check and the held action;
// the ledger and the hold records are fakes here: the context and learning lanes test their own)
func TestSessionCompactAppendixIncludesSavedOutputFailedCheckAndHeldAction(t *testing.T) {
	e := startTools(t, enabledConfig(func(c *Config) { c.Stuck.Enabled = false }), "s")
	e.start()
	e.w.tools.ledger = fakeLedger{[]SavedOutput{{Tool: "bash", Path: "/tmp/pi-warden-output-x/output.txt", Bytes: 36000, HasBytes: true}}}
	e.w.tools.holds = fakeHolds{[]HeldEntry{{Tool: "write", Preview: "src/a.ts", Outcome: "pending"}}}
	e.toolResult("bash", map[string]any{"command": "npm run lint"}, "lint error", true)
	e.host.Fire("session_compact", map[string]any{"reason": "manual"})
	msgs := e.messagesOf("pi-warden-compact-evidence")
	if len(msgs) != 1 {
		t.Fatalf("one sendMessage with pi-warden-compact-evidence, got %d", len(msgs))
	}
	text := msgs[0].Content
	mustMatchRe(t, text, `=== PI-WARDEN COMPACT EVIDENCE ===`)
	mustMatchRe(t, text, `=== END PI-WARDEN COMPACT EVIDENCE ===`)
	mustMatchRe(t, text, `npm run lint`)
	mustMatchRe(t, text, `write.*pending`)
	if !strings.Contains(text, "- bash → /tmp/pi-warden-output-x/output.txt (36000 bytes)") {
		t.Errorf("the saved output shows the real tool and size:\n%s", text)
	}
	mustNotMatchRe(t, text, `unknown →|\(0 bytes\)`)
}

// twin: tests/extension.test.ts:3914
func TestSessionCompactAppendixFalseSendsNothing(t *testing.T) {
	e := startTools(t, enabledConfig(func(c *Config) { c.Context.CompactAppendix = false }), "s")
	e.start()
	e.toolResult("bash", map[string]any{"command": "npm test"}, "ok", false)
	e.host.Fire("session_compact", map[string]any{"reason": "manual"})
	if n := len(e.messagesOf("pi-warden-compact-evidence")); n != 0 {
		t.Errorf("%d messages sent when compactAppendix is false", n)
	}
}

// twin: tests/extension.test.ts:3923
func TestSessionCompactEmptySessionSendsNothing(t *testing.T) {
	e := startTools(t, enabledConfig(), "s")
	e.start()
	e.host.Fire("session_compact", map[string]any{"reason": "manual"})
	if n := len(e.messagesOf("pi-warden-compact-evidence")); n != 0 {
		t.Errorf("%d messages for an empty session", n)
	}
}

// twin: tests/extension.test.ts:3933 (the output of the first check is small here: the saver's saved output is the context lane's)
func TestSessionCompactTwoCompactionsSendTwoMessagesEachFromTheMemoryAtThatTime(t *testing.T) {
	e := startTools(t, enabledConfig(func(c *Config) { c.Stuck.Enabled = false }), "s")
	e.start()
	e.toolResult("bash", map[string]any{"command": "npm test"}, "output A", false)
	e.host.Fire("session_compact", map[string]any{"reason": "manual"})
	first := e.messagesOf("pi-warden-compact-evidence")
	if len(first) != 1 || !strings.Contains(first[0].Content, "Last checks") {
		t.Fatalf("first compaction: %+v", first)
	}
	e.toolResult("bash", map[string]any{"command": "npm run lint"}, "lint error", true)
	e.host.Fire("session_compact", map[string]any{"reason": "manual"})
	second := e.messagesOf("pi-warden-compact-evidence")
	if len(second) != 2 || !strings.Contains(second[1].Content, "npm run lint") {
		t.Fatalf("second compaction: %+v", second)
	}
}

// prefsSessionDir copies the prefs fixture into a session directory.
func (e *toolsEnv) useFixtureSessions(t *testing.T) {
	dir := prefsFixtureDir(t)
	e.sess.set(func(s *toolSession) { s.dir, s.file = dir, filepath.Join(dir, prefsCurrent) })
}

// standingSessions writes three earlier sessions on two fixed days (UTC), before the fixed prefs clock.
func standingSessions(t *testing.T, cwd string, sessions [][]string) string {
	t.Helper()
	dir := t.TempDir()
	stamps := []string{"2026-01-17T12:00:00.000Z", "2026-01-19T09:00:00.000Z", "2026-01-19T10:00:00.000Z"}
	for i, messages := range sessions {
		lines := []string{`{"type":"session","version":3,"id":"s` + itoaInt(i) + `","timestamp":"` + stamps[i] + `","cwd":` + jsonString(cwd) + `}`}
		for n, content := range messages {
			lines = append(lines, `{"type":"message","id":"m`+itoaInt(n)+`","parentId":null,"timestamp":"`+stamps[i]+`","message":{"role":"user","content":`+jsonString(content)+`}}`)
		}
		path := filepath.Join(dir, "s"+itoaInt(i)+".jsonl")
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		at, _ := time.Parse(time.RFC3339, stamps[i])
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

var said3 = [][]string{
	{"never force-push the release branch", "don't commit or stage it", "always skip the tests before pushing"},
	{"ok, never force-push the release branch", "don't commit or stage it", "always skip the tests before pushing"},
	{"please never force-push the release branch. also keep replies short", "don't commit or stage it", "always skip the tests before pushing"},
}

// twin: tests/extension.test.ts:4470
func TestWardenPrefsListsTheStandingPreferencesWithCountsDatesAndTheHintAndWritesNothing(t *testing.T) {
	e := startTools(t, enabledConfig(), "current-session")
	e.useFixtureSessions(t)
	e.start()
	var dir string
	e.sess.set(func(s *toolSession) { dir = s.dir })
	before, _ := os.ReadDir(dir)
	e.host.Command("warden", "prefs")
	text := lastNotice(t, e)
	mustMatchRe(t, text, `^Standing preferences \(repeated in 2\+ of the last 3 sessions of this project; injected from 3 sessions on 2 days\):`)
	mustMatchRe(t, text, `Never paste the api_key=\[redacted\] value into the chat log \(2 sessions on 2 days, last 2026-01-03\): not injected: seen in 2 sessions`)
	mustMatchRe(t, text, `Dont open pull requests, leave the branch local \(2 sessions on 2 days, last 2026-01-02\): not injected: seen in 2 sessions`)
	if !strings.HasSuffix(text, prefsHint) {
		t.Error("no hint")
	}
	mustNotMatchRe(t, text, `(?i)linter|formatter`)
	if len(e.ts.reqs()) != 0 {
		t.Error("a judgment request was made")
	}
	if n := len(e.messagesOf(prefsType)); n != 0 {
		t.Errorf("nothing passes the injection rules, but %d messages were sent", n)
	}
	e.host.Command("warden", "prefs")
	if e.sess.reads() != 1 {
		t.Errorf("one scan per session start, got %d reads", e.sess.reads())
	}
	after, _ := os.ReadDir(dir)
	if len(after) != len(before) {
		t.Errorf("the session directory changed: %d -> %d files", len(before), len(after))
	}
}

// twin: tests/extension.test.ts:4517
func TestPrefsInjectIsOnByDefaultOneQuotedContextMessageNotASteerAndNotAgainOnAResume(t *testing.T) {
	e := startTools(t, enabledConfig(), "current-session")
	dir := standingSessions(t, e.host.opts.Cwd, said3)
	e.sess.set(func(s *toolSession) { s.dir, s.file = dir, filepath.Join(dir, "current.jsonl") })
	e.start()
	sentPrefs := e.messagesOf(prefsType)
	want := strings.Join([]string{
		"Standing preferences for this project, quoted as said in earlier sessions:",
		"- \"Never force-push the release branch\" (3 sessions)",
		"If the current request says otherwise, follow the current request.",
	}, "\n")
	if len(sentPrefs) != 1 || sentPrefs[0].Content != want {
		t.Fatalf("sent %+v", sentPrefs)
	}
	if len(e.steers()) != 0 {
		t.Error("a steer was sent")
	}
	// A resumed session already carries the message.
	e.sess.set(func(s *toolSession) {
		s.branch = []map[string]any{{"type": "custom_message", "customType": prefsType, "content": want}}
	})
	e.host.Fire("session_start", map[string]any{"reason": "resume"})
	if n := len(e.messagesOf(prefsType)); n != 1 {
		t.Errorf("sent again on a resume: %d", n)
	}
	// prefs.inject: false sends nothing.
	off := startToolsWith(t, enabledConfig(func(c *Config) { c.Prefs.Inject = false }), "current-session", e.home, e.host.opts.Cwd, dir)
	off.start()
	if n := len(off.messagesOf(prefsType)); n != 0 {
		t.Errorf("inject: false sent %d messages", n)
	}
	if len(e.ts.reqs()) != 0 {
		t.Error("a judgment request was made")
	}
}

func startToolsWith(t *testing.T, cfg *Config, session, home, cwd, dir string) *toolsEnv {
	t.Helper()
	e := startToolsInHome(t, cfg, session, home, cwd)
	e.sess.set(func(s *toolSession) { s.dir, s.file = dir, filepath.Join(dir, "current.jsonl") })
	return e
}

// twin: tests/extension.test.ts:4543
func TestWardenPrefsNamesEachItemsStatusAndForgetDropsOneForTheProject(t *testing.T) {
	e := startTools(t, enabledConfig(), "current-session")
	dir := standingSessions(t, e.host.opts.Cwd, said3)
	e.sess.set(func(s *toolSession) { s.dir, s.file = dir, filepath.Join(dir, "current.jsonl") })
	e.start()
	e.host.Command("warden", "prefs")
	text := lastNotice(t, e)
	mustMatchRe(t, text, `1\. Never force-push the release branch \(3 sessions on 2 days, last 2026-01-19\): injected`)
	mustMatchRe(t, text, `Don't commit or stage it \(3 sessions on 2 days, last 2026-01-19\): not injected: task-bound`)
	mustMatchRe(t, text, `Always skip the tests before pushing \(3 sessions on 2 days, last 2026-01-19\): not injected: weakens a check`)
	e.host.Command("warden", "prefs forget 1")
	mustMatchRe(t, lastNotice(t, e), `^Forgotten for this project: "Never force-push the release branch"`)
	e.host.Command("warden", "prefs")
	mustNotMatchRe(t, lastNotice(t, e), `force-push`)
	e.host.Command("warden", "prefs forget 9")
	mustMatchRe(t, lastNotice(t, e), `^Usage: /warden prefs forget <n>`)
	before := len(e.messagesOf(prefsType))
	e.start()
	if n := len(e.messagesOf(prefsType)); n != before {
		t.Errorf("a forgotten item is injected: %d", n-before)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 3 {
		t.Errorf("session files touched: %d entries", len(entries))
	}
}

// twin: tests/extension.test.ts:4570
func TestWardenRememberRecordsALessonItIsInjectedOnlyAfterASecondSessionRecordsIt(t *testing.T) {
	e := startTools(t, enabledConfig(), "first-session")
	dir := standingSessions(t, e.host.opts.Cwd, nil)
	e.sess.set(func(s *toolSession) { s.dir, s.file = dir, filepath.Join(dir, "current.jsonl") })
	remember := func(env *toolsEnv, lesson string) string {
		return env.toolText("warden_remember", map[string]any{"lesson": lesson})
	}
	e.start()
	e.prompt("Add the export button")
	if got := remember(e, "Always use Firefox for the vendor site"); got != `recorded: "Always use Firefox for the vendor site". It reaches later sessions only after it is confirmed: recorded again in a later session, or said by the user.` {
		t.Errorf("first: %q", got)
	}
	if got := remember(e, "Always skip the tests when the build is slow"); got != "not recorded: weakens a check" {
		t.Errorf("weakens: %q", got)
	}
	e.host.Command("warden", "prefs")
	mustMatchRe(t, lastNotice(t, e), `Always use Firefox for the vendor site \(agent lesson, recorded in 1 session, last 2026-01-20\): agent lesson, not yet confirmed`)
	second := startToolsWith(t, enabledConfig(), "second-session", e.home, e.host.opts.Cwd, dir)
	second.start()
	if n := len(second.messagesOf(prefsType)); n != 0 {
		t.Errorf("a lesson recorded in one session is injected: %d", n)
	}
	second.prompt("Check the vendor invoices")
	mustMatchRe(t, remember(second, "Always use Firefox for the vendor site"), `^recorded as a confirmation of the agent lesson "Always use Firefox for the vendor site" \(2 sessions\)`)
	third := startToolsWith(t, enabledConfig(), "third-session", e.home, e.host.opts.Cwd, dir)
	third.start()
	injected := third.messagesOf(prefsType)
	if len(injected) != 1 {
		t.Fatalf("injected %d", len(injected))
	}
	mustMatchRe(t, injected[0].Content, `- "Always use Firefox for the vendor site" \(2 sessions\) \(agent lesson\)`)
	if len(e.ts.reqs()) != 0 {
		t.Error("a judgment request was made")
	}
}

// twin: tests/extension.test.ts:4603
func TestPrefsDisabledReadsNoSessionFileEvenWithInjectOn(t *testing.T) {
	for name, mutate := range map[string]func(*Config){
		"enabled: false":       func(c *Config) { c.Enabled = false },
		"prefs.enabled: false": func(c *Config) { c.Prefs.Enabled = false },
	} {
		e := startTools(t, enabledConfig(mutate), "s")
		e.useFixtureSessions(t)
		e.start()
		e.host.Command("warden", "prefs")
		mustMatchRe(t, lastMessageOrNotice(e), `Standing preferences are off .*no session files were read`)
		if n := len(e.messagesOf(prefsType)); n != 0 {
			t.Errorf("%s: %d messages", name, n)
		}
		if e.sess.reads() != 0 {
			t.Errorf("%s: the session directory was asked for %d times", name, e.sess.reads())
		}
	}
}

// lastMessageOrNotice is what a command said: a notice with a UI.
func lastMessageOrNotice(e *toolsEnv) string {
	if n := e.notices(); len(n) > 0 {
		return n[len(n)-1]
	}
	return ""
}

// The disabled warden answers the tools with the original's refusal texts.
func TestToolsWhenDisabledReplyWithTheOriginalsRefusals(t *testing.T) {
	e := startTools(t, enabledConfig(func(c *Config) { c.Enabled = false }), "s")
	e.start()
	if got := e.toolText("warden_loops", map[string]any{"action": "list"}); got != "not available: pi-warden is off (enabled is false)" {
		t.Errorf("loops: %q", got)
	}
	if got := e.toolText("warden_recall", map[string]any{}); got != "not available: pi-warden is off (enabled is false)" {
		t.Errorf("recall: %q", got)
	}
	if got := e.toolText("warden_remember", map[string]any{"lesson": "Never x y"}); got != "not recorded: standing preferences are off (enabled is false)" {
		t.Errorf("remember: %q", got)
	}
	off := startTools(t, enabledConfig(func(c *Config) { c.Prefs.Enabled = false }), "s")
	off.start()
	if got := off.toolText("warden_remember", map[string]any{"lesson": "Never x y"}); got != "not recorded: standing preferences are off (prefs.enabled is false)" {
		t.Errorf("remember with prefs off: %q", got)
	}
}

// prompt starts a run with a new user prompt.
func (e *toolsEnv) prompt(text string) {
	e.host.Fire("before_agent_start", map[string]any{"prompt": text, "systemPromptOptions": map[string]any{}})
}
