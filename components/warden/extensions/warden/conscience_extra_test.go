package warden

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Go-only tests of the conscience: behaviors the upstream cases leave to the mutation check.

// The deadline is the smaller of the conscience's and the shared timeout (conscience.ts 509): a judge that takes
// 400 ms times out when either is 100 ms.
func TestAssessDeadlineIsTheSmallerOfTheTwoTimeouts(t *testing.T) {
	slow := csJudge(func(ctx context.Context, _ Request) (Evaluation, error) {
		select {
		case <-time.After(400 * time.Millisecond):
		case <-ctx.Done():
		}
		return Evaluation{Answers: map[string]Answer{"conscience_disposition": csChoice("advance", 1)}}, nil
	})
	for _, c := range []struct{ conscience, shared int }{{10000, 100}, {100, 10000}} {
		deps := ConscienceDeps{Judge: slow, Config: csConfig(func(cc *ConscienceConfig) { cc.TimeoutMs = c.conscience }), SharedTimeoutMs: c.shared}
		start := time.Now()
		r := csAssess(t, "test", "", csDefaultSkills, nil, nil, nil, deps, nil)
		if r.SkipReason != SkipError || r.ErrorCategory != "timeout" || time.Since(start) > 350*time.Millisecond {
			t.Errorf("conscience %d shared %d: %+v after %v", c.conscience, c.shared, r, time.Since(start))
		}
	}
}

// A request that overran the deadline is not retried and later chunks do not start: with a past deadline every
// chunk fails without a judge request.
func TestAssessPastDeadlineMakesNoRequest(t *testing.T) {
	var n atomic.Int64
	j := csJudge(func(context.Context, Request) (Evaluation, error) { n.Add(1); return Evaluation{}, nil })
	now := time.Unix(100, 0)
	var calls atomic.Int64
	deps := ConscienceDeps{Judge: j, Config: csConfig(), SharedTimeoutMs: 50, Now: func() time.Time {
		if calls.Add(1) == 1 {
			return now
		}
		return now.Add(time.Second)
	}}
	if r := csAssess(t, "test", "", csDefaultSkills, nil, nil, nil, deps, nil); n.Load() != 0 || r.Selected != nil {
		t.Errorf("%d requests, %+v", n.Load(), r)
	}
}

// Of equal scores a skill beats a tool, and the smaller name beats the larger within a kind (conscience.ts 590).
func TestAssessTieBreaksBySkillThenName(t *testing.T) {
	var n atomic.Int64
	skills := []Skill{csSkill("zeta", "z"), csSkill("alpha", "a")}
	tools := []CapabilityTool{{"aardvark", "tool"}}
	r := csAssess(t, "test", "", skills, tools, nil, nil, csDeps(csAnswerAll(&n, nil, 0.1, 0.1, 0.4, 0.4)), nil)
	if r.Selected == nil || r.Selected.ID != "alpha" {
		t.Errorf("%+v", r.Selected)
	}
}

// A failure of the selected tool earns one re-assessment, like a failed read of the selected skill.
func TestConscienceSelectedToolFailureTriggersOneTurnEndReassessment(t *testing.T) {
	e := newCsExt(t, csOpts{})
	e.mu.Lock()
	e.host2 = func(method string, _ map[string]any) (map[string]any, string, bool) {
		if method == "getAllTools" {
			return map[string]any{"tools": []any{map[string]any{"name": "search_graph", "description": "Search the code graph"}}}, "", true
		}
		return nil, "", false
	}
	e.mu.Unlock()
	e.set(csAdvance(3))
	r := e.prompt("find the loader", nil)
	if _, content, ok := csMessage(r); !ok || !strings.Contains(content, `"search_graph" tool`) {
		t.Fatalf("a tool is recommended: %v", r)
	}
	before := len(e.conscienceRequests())
	e.toolCall("search_graph", "t1", map[string]any{"q": "x"})
	e.toolResult("search_graph", map[string]any{"q": "x"}, "boom", true)
	e.host.Fire("turn_end", map[string]any{"turnIndex": 1})
	if len(e.conscienceRequests()) <= before {
		t.Error("the failure earns a re-assessment")
	}
	tr := e.traceText()
	for _, want := range []string{"tool_attempted: search_graph", "tool_failed: search_graph", "turn_end reassessment → tool:search_graph"} {
		if !strings.Contains(tr, want) {
			t.Errorf("trace lacks %q:\n%s", want, tr)
		}
	}
	// A success settles the trigger without a re-assessment.
	e2 := newCsExt(t, csOpts{})
	e2.mu.Lock()
	e2.host2 = e.host2
	e2.mu.Unlock()
	e2.set(csAdvance(3))
	e2.prompt("find the loader", nil)
	b2 := len(e2.conscienceRequests())
	e2.toolCall("search_graph", "t1", map[string]any{"q": "x"})
	e2.toolResult("search_graph", map[string]any{"q": "x"}, "found", false)
	e2.host.Fire("turn_end", map[string]any{"turnIndex": 1})
	if len(e2.conscienceRequests()) != b2 || !strings.Contains(e2.traceText(), "tool_succeeded: search_graph") {
		t.Errorf("a success needs no re-assessment:\n%s", e2.traceText())
	}
}

// The index nudge is said once per session, in a UI as a notice and without one as a trace line.
func TestConscienceIndexNudgeIsSaidOncePerSession(t *testing.T) {
	e := newCsExt(t, csOpts{})
	e.set(map[string]any{"conscience_disposition": "no_gap"})
	e.prompt("one", csDefaultSkillEvents())
	e.prompt("two", csDefaultSkillEvents())
	n := 0
	for _, m := range e.notices() {
		if m == "pi-warden: run /warden index so the conscience recommends skills and tools from real descriptions." {
			n++
		}
	}
	if n != 1 {
		t.Errorf("nudges: %d", n)
	}
	h := newCsExt(t, csOpts{noUI: true})
	h.set(map[string]any{"conscience_disposition": "no_gap"})
	h.prompt("one", csDefaultSkillEvents())
	h.prompt("two", csDefaultSkillEvents())
	if c := strings.Count(h.traceText(), "nudge: index missing"); c != 1 || !strings.Contains(h.traceText(), "nudgeReason: missing_index") {
		t.Errorf("trace:\n%s", h.traceText())
	}
}

// With a current index the nudge never fires; with a stale one it names the unindexed skills.
func TestConscienceStaleIndexNudgeNamesTheUnindexedSkills(t *testing.T) {
	e := newCsExt(t, csOpts{noUI: true})
	idx := e.w.indexDirsOf()
	skill := csWriteSkill(t, "impeccable", "---\nname: impeccable\ndescription: UI design\n---\n\nBody.")
	entry := ixSample
	entry.Name, entry.SourceHash = "impeccable", "stale-hash"
	if err := writeIndex(idx.path("global", ""), ixFile(entry)); err != nil {
		t.Fatal(err)
	}
	if err := writeIndex(idx.path("project", e.cwd), ixFile()); err != nil {
		t.Fatal(err)
	}
	e.start() // the indexes are read at session start
	e.set(map[string]any{"conscience_disposition": "no_gap"})
	e.prompt("one", []map[string]any{csSkillEvent("impeccable", "UI design", skill)})
	tr := e.traceText()
	if !strings.Contains(tr, "nudge: 1 skill(s) unindexed") || !strings.Contains(tr, "nudgeReason: stale_index") || !strings.Contains(tr, "unindexed: impeccable") {
		t.Errorf("trace:\n%s", tr)
	}
	// Once the entry matches the file, nothing is said.
	entry.SourceHash = fileContentHash(skill)
	writeIndex(idx.path("global", ""), ixFile(entry))
	e.start()
	e.prompt("two", []map[string]any{csSkillEvent("impeccable", "UI design", skill)})
	if strings.Contains(e.traceText(), "nudge:") {
		t.Errorf("a current index is not nudged:\n%s", e.traceText())
	}
}

// The recent context is up to four of the last six session messages, each cut to 500 characters (extension.ts 1163).
func TestConsciencePromptCarriesTheRecentContext(t *testing.T) {
	e := newCsExt(t, csOpts{})
	e.set(map[string]any{"conscience_disposition": "no_gap"})
	for i := 1; i <= 8; i++ {
		role := "user"
		if i%2 == 0 {
			role = "assistant"
		}
		text := "m" + itoaInt(i)
		if i == 4 {
			text = strings.Repeat("y", 700)
		}
		e.say(role, text)
	}
	e.prompt("next", csDefaultSkillEvents())
	got := e.requests()[0].State["context"].(string)
	want := "user: m3\nassistant: " + strings.Repeat("y", 500) + "\nuser: m5\nassistant: m6"
	if got != want {
		t.Errorf("context %q want %q", got, want)
	}
}

// Number.prototype.toFixed: a tie of the exact value goes up, and the binary value decides the rest.
func TestJSToFixedRoundsLikeJavaScript(t *testing.T) {
	cases := []struct {
		x      float64
		digits int
		want   string
	}{{0.125, 2, "0.13"}, {2.5, 0, "3"}, {0.95, 2, "0.95"}, {1.005, 2, "1.00"}, {0, 2, "0.00"}, {0.8, 2, "0.80"}, {0.875, 2, "0.88"}, {-0.125, 2, "-0.13"}, {12.345, 1, "12.3"}, {0.004, 2, "0.00"}}
	for _, c := range cases {
		if got := jsToFixed(c.x, c.digits); got != c.want {
			t.Errorf("toFixed(%v, %d) = %s want %s", c.x, c.digits, got, c.want)
		}
	}
}

// The agent_end reminder is one steer of the conscience guard, delivered as a follow-up, and the capability is
// watched afterwards (the adaptive lane's hook).
func TestConscienceReminderIsAFollowUpSteer(t *testing.T) {
	e := newCsExt(t, csOpts{})
	e.set(csAdvance(3))
	e.prompt("design a landing page", csDefaultSkillEvents())
	csAgentEndToolCall(e)
	st := e.steers()
	if len(st) != 1 || st[0].Options["deliverAs"] != "followUp" || st[0].Content != "Reminder: consider using the \"impeccable\" skill. UI design" {
		t.Errorf("%+v", st)
	}
	if e.w.stats.SteerGuards[SteerGuardConscience] != 1 {
		t.Errorf("counted against the conscience guard: %v", e.w.stats.SteerGuards)
	}
}

// Every eligible candidate reaches exactly one request, 31 to a request (32 questions with the disposition).
func TestAssessPacksEveryCandidateIntoExactlyOneRequest(t *testing.T) {
	for _, c := range []struct{ n, requests int }{{31, 1}, {32, 2}, {62, 2}, {63, 3}, {95, 4}} {
		j, reqs := csRecording()
		csAssess(t, "test", "", csManySkills(c.n), nil, nil, nil, csDeps(j), nil)
		seen := map[string]int{}
		for _, r := range reqs() {
			if len(r) > 31 {
				t.Errorf("%d candidates: a request carries %d", c.n, len(r))
			}
			for _, name := range r {
				seen[name]++
			}
		}
		if len(reqs()) != c.requests || len(seen) != c.n {
			t.Errorf("%d candidates: %d requests, %d distinct candidates", c.n, len(reqs()), len(seen))
		}
		for name, n := range seen {
			if n != 1 {
				t.Errorf("%s sent %d times", name, n)
			}
		}
	}
}

// What leaves the machine is redacted twice: the hook redacts the prompt before it cuts it to 2000 characters (a
// token cut in half would escape redaction), and buildState redacts the task, the context and the spine again.
func TestConsciencePromptIsRedactedBeforeItIsCut(t *testing.T) {
	e := newCsExt(t, csOpts{})
	e.set(map[string]any{"conscience_disposition": "no_gap"})
	prompt := strings.Repeat("x", 1990) + " ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef1234567890 tail"
	e.prompt(prompt, csDefaultSkillEvents())
	for _, r := range e.requests() {
		if task := r.State["task"].(string); strings.Contains(task, "ghp_") {
			t.Errorf("a token cut in half by the 2000-character limit reached the judge: %q", task[1980:])
		}
	}
}

func TestBuildStateRedactsTaskContextAndSpine(t *testing.T) {
	secret := "Authorization: Bearer abcdef0123456789abcdef"
	url := "postgres://admin:s3cretpass9@db.internal/app"
	spine := &TaskSpine{Goal: secret, Task: "t", History: []string{url}}
	st := buildState("run "+secret, "context "+url, nil, nil, nil, spine)
	text := jsonCompact(st)
	for _, leak := range []string{"abcdef0123456789abcdef", "s3cretpass9"} {
		if strings.Contains(text, leak) {
			t.Errorf("%q reached the state: %s", leak, text)
		}
	}
}

// The reminder is gated by the fresh assessment too: a run whose capability the judge no longer wants (the user
// is being asked something) gets no reminder.
func TestConscienceReminderNotSentWhenTheFreshAssessmentAwaitsTheUser(t *testing.T) {
	e := newCsExt(t, csOpts{})
	e.set(csAdvance(3))
	if _, _, ok := csMessage(e.prompt("design a landing page", csDefaultSkillEvents())); !ok {
		t.Fatal("the first recommendation is delivered")
	}
	e.set(map[string]any{"conscience_disposition": "awaiting_user", "c1": 3})
	csAgentEndToolCall(e)
	if r := e.reminders(); len(r) != 0 {
		t.Errorf("%v", r)
	}
}

// maxNudges below 1 in the config file is raised to 1 by the loader (config.ts: Math.max(1, Math.min(5, ...))), so a
// zero cannot switch reminders off: one is still delivered.
func TestConscienceMaxNudgesZeroInTheFileIsRaisedToOne(t *testing.T) {
	e := newCsExt(t, csOpts{conscience: func(c *ConscienceConfig) { c.MaxNudges = 0 }})
	e.set(csAdvance(3))
	e.prompt("design a landing page", csDefaultSkillEvents())
	csAgentEndToolCall(e)
	if r := e.reminders(); len(r) != 1 {
		t.Errorf("%v", r)
	}
}

// The conscience is off unless conscience.enabled: no assessment, no request, no trace line, with the judge ready.
func TestConscienceDisabledDoesNothing(t *testing.T) {
	e := newCsExt(t, csOpts{conscience: func(c *ConscienceConfig) { c.Enabled = false }})
	e.set(csAdvance(3))
	if _, _, ok := csMessage(e.prompt("design a landing page", csDefaultSkillEvents())); ok {
		t.Error("nothing is recommended")
	}
	csAgentEndToolCall(e)
	e.host.Fire("agent_settled", nil)
	lines := 0
	for _, entry := range e.w.trace.Entries() {
		if entry.Guard == GuardConscience {
			lines++
		}
	}
	if len(e.requests()) != 0 || lines != 0 || len(e.reminders()) != 0 {
		t.Errorf("%d requests, %d conscience trace lines:\n%s", len(e.requests()), lines, e.traceText())
	}
}

// Go-only: the one-pass word splitter equals the original's regular expressions on tool names.
func TestWordsOnePassEqualsTheRegularExpressions(t *testing.T) {
	fixed := []string{"deleteIssue", "mcp__db__truncateTable", "HTMLParser", "getHTTPResponse", "aBCd", "ABc", "ABCDe", "a1B2", "X", "xY", "wipe disk", "list_projects", "", "__", "dropdown_state", "camelCaseWithNumbers123Here", "ABC", "AbcDEFgh"}
	for _, f := range fixed {
		if got, want := words(f), wordsRegexp(f); strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("%q: %v want %v", f, got, want)
		}
	}
	alphabet := []string{"a", "b", "Z", "Q", "0", "9", "_", "-", " ", "x", "Y"}
	for n := 0; n < 20000; n++ {
		var b strings.Builder
		for k := n%9 + 1; k > 0; k-- {
			b.WriteString(alphabet[(n*31+k*17+n/7)%len(alphabet)])
			n++
		}
		s := b.String()
		if got, want := words(s), wordsRegexp(s); strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("%q: %v want %v", s, got, want)
		}
	}
}

// Go-only: the hand-written canonical form equals the maps-and-encoder form on conscience sets and on arbitrary ones.
func TestQuestionHashWriterEqualsTheReference(t *testing.T) {
	sets := []Questions{
		{"q1": Choice("", map[string]string{"a": "1"})},
		{"q1": Choice("", map[string]string{"a": "2"}), "z": Noul("is it?", "yes", "no"), "b": Score("rate \"it\" <now> & é", []string{"low", "high"})},
		{}, {"c1": Score("x", nil)},
	}
	for _, n := range []int{0, 1, 4, 31} {
		items := make([]string, n)
		for i := range items {
			items[i] = "s" + itoaInt(i)
		}
		q, _, _ := buildBatchQuestions(csBatch("skill", items...), "t", "", nil, nil)
		sets = append(sets, q)
	}
	for i, q := range sets {
		if got, want := questionHash(q), questionHashReference(q); got != want {
			t.Errorf("set %d: %s want %s", i, got, want)
		}
	}
}
