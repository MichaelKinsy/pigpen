package warden

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// Twins of tests/prefs-rules.test.ts: the seven injection rules, the agent's lessons, forget, and the lesson file.

var rulesNow = time.Date(2026, 1, 20, 12, 0, 0, 0, time.UTC).UnixMilli()

func atDate(date string) int64 {
	t, err := time.Parse(time.RFC3339, date+"T12:00:00Z")
	if err != nil {
		panic(err)
	}
	return t.UnixMilli()
}

func said(clause, session, date string, permit ...bool) PrefCandidate {
	return PrefCandidate{Clause: clause, Session: session, At: atDate(date), Permit: len(permit) > 0 && permit[0]}
}

// thrice is three sessions on two days: enough to be injected when every other rule holds.
func thrice(clause string) []PrefCandidate {
	return []PrefCandidate{said(clause, clause+"-1", "2026-01-10"), said(clause, clause+"-2", "2026-01-10"), said(clause, clause+"-3", "2026-01-12")}
}

func jsonString(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

// ruleCounts: "subagent" is a rare subject word, as it is in real messages.
func ruleCounts() *WordCounts {
	return &WordCounts{Messages: 100, Words: map[string]int{"subagent": 1, "spawn": 1}}
}

func scanOf(candidates []PrefCandidate) *PrefsScan {
	return &PrefsScan{Prefs: groupPreferences(candidates, ruleCounts(), prefsMinSessions, maxPrefs), Candidates: candidates, Counts: ruleCounts()}
}

func evaluate(candidates []PrefCandidate) []PrefItem {
	return evaluatePrefs(scanOf(candidates), emptyPrefsStore(), rulesNow)
}

func evaluateWith(candidates []PrefCandidate, store PrefsStore, now int64) []PrefItem {
	return evaluatePrefs(scanOf(candidates), store, now)
}

func statusOf(items []PrefItem, text string) string {
	for _, item := range items {
		if item.Text == text {
			return item.Status
		}
	}
	return "<absent>"
}

func sessionOf(messages ...string) []byte {
	var lines []string
	for i, content := range messages {
		lines = append(lines, `{"type":"message","id":"m`+itoaInt(i)+`","timestamp":"2026-01-10T12:00:00.000Z","message":{"role":"user","content":`+jsonString(content)+`}}`)
	}
	return []byte(strings.Join(lines, "\n"))
}

func clausesOf(cs []PrefCandidate) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Clause)
	}
	return out
}

// twin: tests/prefs-rules.test.ts:27
func TestPrefsRule1OnlyWhatTheHumanTypedCounts(t *testing.T) {
	if got := clausesOf(candidatesInSession(sessionOf("never force-push the release branch"), "s1", rulesNow, nil)); !slices.Equal(got, []string{"Never force-push the release branch"}) {
		t.Errorf("typed: %q", got)
	}
	if got := candidatesInSession(sessionOf("From Lead agent:\n\nNever force-push the release branch."), "s1", rulesNow, nil); len(got) != 0 {
		t.Errorf("relayed from an agent: %v", got)
	}
	if got := candidatesInSession(sessionOf("# ORDER\n\nNever force-push the release branch."), "s1", rulesNow, nil); len(got) != 0 {
		t.Errorf("an order: %v", got)
	}
}

// twin: tests/prefs-rules.test.ts:33
func TestPrefsRule2StandingFormsPassQuestionsAndTemporaryWordsFail(t *testing.T) {
	for text, clause := range map[string]string{
		"don't open pull requests":                           "Don't open pull requests",
		"never force-push the release branch":                "Never force-push the release branch",
		"always run the linter before a commit":              "Always run the linter before a commit",
		"stop adding emoji to the changelog":                 "Stop adding emoji to the changelog",
		"from now on, write commit messages in the imperative": "Write commit messages in the imperative",
		"next time ask before deleting a branch":             "Ask before deleting a branch",
		"I told you to keep the tests offline":               "Keep the tests offline",
		"always use pnpm for this project":                   "Always use pnpm for this project",
	} {
		clauseList(t, text, clause)
	}
	for _, text := range []string{
		"why don't you rebase the branch?", "please make the table wider", "don't merge the migration yet", "don't touch the schema now",
		"don't push until the checks pass", "never squash commits in this PR", "don't rebase this branch onto main", "never rename the helper for this",
		"don't bump the version for now", "don't deploy today",
	} {
		clauseList(t, text)
	}
}

// twin: tests/prefs-rules.test.ts:58
func TestPrefsRule3PronounOnlyObjectOrNamedTicketBranchPRHashIsTaskBound(t *testing.T) {
	for _, clause := range []string{
		"Don't commit or stage it", "Never do that again", "Dont commit it back though", "Don't make it amber anymore", "Stop doing that", "Don't touch them", "Never revert ABC-123 by hand",
		"Don't close PR 42 before the demo", "Never rebase on branch feat/new-login", "Don't cherry-pick 3f9c2ab1 onto main", "Never close #17 by hand",
	} {
		if got := classifyClause(clause); got != classTaskBound {
			t.Errorf("%q: %s", clause, got)
		}
	}
	for _, clause := range []string{"Don't commit to main", "Never touch this file by hand", "Always check that the build passes", "Don't spawn subagents", "Don't let it spawn subagents"} {
		if got := classifyClause(clause); got != classStanding {
			t.Errorf("%q: %s", clause, got)
		}
	}
	items := evaluate(append(thrice("Don't commit or stage it"), thrice("Don't commit to main")...))
	if got := statusOf(items, "Don't commit or stage it"); got != "not injected: task-bound" {
		t.Errorf("task-bound: %s", got)
	}
	if got := statusOf(items, "Don't commit to main"); got != wasInjected {
		t.Errorf("the task-bound wording lends it no sessions and takes none: %s", got)
	}
}

// twin: tests/prefs-rules.test.ts:71
func TestPrefsRule4ThreeSessionsOnTwoDaysAreInjected(t *testing.T) {
	two := []PrefCandidate{said("Never force-push the release branch", "a", "2026-01-10"), said("Never force-push the release branch", "b", "2026-01-12")}
	var oneDay []PrefCandidate
	for _, name := range []string{"c", "d", "e"} {
		oneDay = append(oneDay, said("Always squash merge feature branches", name, "2026-01-11"))
	}
	all := append(append(thrice("Keep replies short"), two...), oneDay...)
	items := evaluate(all)
	if got := statusOf(items, "Keep replies short"); got != wasInjected {
		t.Errorf("thrice: %s", got)
	}
	if got := statusOf(items, "Never force-push the release branch"); got != "not injected: seen in 2 sessions" {
		t.Errorf("two: %s", got)
	}
	if got := statusOf(items, "Always squash merge feature branches"); got != "not injected: seen on 1 day" {
		t.Errorf("one day: %s", got)
	}
}

// twin: tests/prefs-rules.test.ts:80
func TestPrefsRule5LastSeenWithin30DaysAndNoLaterOppositeMessage(t *testing.T) {
	stale := evaluateWith(thrice("Keep replies short"), emptyPrefsStore(), atDate("2026-02-20"))
	if stale[0].Status != "not injected: last seen 2026-01-12, over 30 days ago" {
		t.Errorf("stale: %s", stale[0].Status)
	}
	subagents := thrice("Don't spawn subagents")
	lifted := evaluate(append(slices.Clone(subagents), said("do use subagents here", "later", "2026-01-14", true)))
	if got := statusOf(lifted, "Don't spawn subagents"); got != "not injected: contradicted by a later message" {
		t.Errorf("lifted: %s", got)
	}
	if len(lifted) != 1 {
		t.Errorf("a permission is never listed itself: %d items", len(lifted))
	}
	earlier := evaluate(append(slices.Clone(subagents), said("do use subagents here", "earlier", "2026-01-09", true)))
	if got := statusOf(earlier, "Don't spawn subagents"); got != wasInjected {
		t.Errorf("a permission the user later took back does not lift it: %s", got)
	}
	opposite := evaluate(append(thrice("Always squash commits"), said("Never squash commits", "later", "2026-01-15")))
	if got := statusOf(opposite, "Always squash commits"); got != "not injected: contradicted by a later message" {
		t.Errorf("opposite: %s", got)
	}
}

// twin: tests/prefs-rules.test.ts:93
func TestPrefsRule6AClauseThatSkipsDisablesOrAvoidsACheckIsListedNeverInjected(t *testing.T) {
	for _, clause := range []string{
		"Always skip the tests before pushing", "Don't run the tests", "Don't run the live tests", "Don't run the checks", "Never ask before pushing",
		"Never ask for a review", "Don't wait for the review", "Don't review the diff", "Don't check the output", "Never verify before pushing",
		"Always commit with --no-verify", "Always disable warden in this repo", "Always push without asking", "Always deploy without asking",
		"Always delete old branches without confirmation", "Stop testing it",
	} {
		if got := classifyClause(clause); got != classWeakens {
			t.Errorf("%q: %s", clause, got)
		}
	}
	if len(weakensChecks.asked) == 0 || len(weakensChecks.refused) == 0 {
		t.Error("the markers live in one constant")
	}
	items := evaluate(append(thrice("Always skip the tests before pushing"), thrice("Don't run the live tests")...))
	for _, text := range []string{"Always skip the tests before pushing", "Don't run the live tests"} {
		if got := statusOf(items, text); got != "not injected: weakens a check" {
			t.Errorf("%q: %s", text, got)
		}
	}
}

// twin: tests/prefs-rules.test.ts:106
func TestPrefsRule6AProhibitionOfAHarmfulActionIsASafetyPreferenceAndIsInjected(t *testing.T) {
	safety := []string{
		"Never check in secrets", "Never force-push the release branch", "Don't commit to main", "Never test in production", "Never skip the tests",
		"Never push without asking", "Always run the checks before a commit", "Dont run subagents do the review yourself",
	}
	for _, clause := range safety {
		if got := classifyClause(clause); got != classStanding {
			t.Errorf("%q: %s", clause, got)
		}
	}
	if got := classifyClause("Don't ever run the tests"); got != classWeakens {
		t.Errorf("Don't ever run the tests: %s", got)
	}
	injected := []string{"Never check in secrets", "Never force-push the release branch", "Don't commit to main", "Never test in production", "Never push without asking"}
	var all []PrefCandidate
	for _, clause := range injected {
		all = append(all, thrice(clause)...)
	}
	items := evaluate(all)
	for _, clause := range injected {
		if got := statusOf(items, clause); got != wasInjected {
			t.Errorf("%q: %s", clause, got)
		}
	}
	mustMatch(t, prefsMessage(items), `- "Never check in secrets" \(3 sessions\)`)
}

// twin: tests/prefs-rules.test.ts:119
func TestPrefsRule7AtMostFiveItemsAnd400CharactersTheClosingSentenceIsExact(t *testing.T) {
	var all []PrefCandidate
	for _, clause := range []string{"Keep replies short", "Never force-push the release branch", "Always sign the tags", "Don't edit the lockfile by hand", "Always write changelog entries", "Never rename public exports"} {
		all = append(all, thrice(clause)...)
	}
	items := evaluate(all)
	injected, firstOut := 0, ""
	for _, item := range items {
		if item.Injected {
			injected++
		} else if firstOut == "" {
			firstOut = item.Status
		}
	}
	if injected != maxInjected || firstOut != "not injected: over the 5-item or 400-character cap" {
		t.Errorf("injected %d, first out %q", injected, firstOut)
	}
	message := prefsMessage(items)
	if !strings.HasPrefix(message, prefsLead+"\n- \"") || !strings.HasSuffix(message, "\nIf the current request says otherwise, follow the current request.") {
		t.Errorf("message %q", message)
	}
	if prefsClosing != "If the current request says otherwise, follow the current request." {
		t.Error("closing sentence")
	}
	mustMatch(t, message, `- "Keep replies short" \(3 sessions\)`)
	var long []PrefCandidate
	for _, clause := range []string{
		"Always describe the database migration steps in the pull request body with rollback notes and owners",
		"Never rename exported functions without adding a deprecated alias that forwards to the new name",
		"Always keep screenshots of changed panels beside the design review document for the product team",
	} {
		long = append(long, thrice(clause)...)
	}
	capped := evaluate(long)
	if utf16Len(prefsMessage(capped)) > messageChars {
		t.Error("the message is over the cap")
	}
	hasCap := false
	for _, item := range capped {
		hasCap = hasCap || (!item.Injected && strings.Contains(item.Status, "cap"))
	}
	if !hasCap {
		t.Error("no item is kept out by the cap")
	}
	if prefsMessage(evaluate(nil)) != "" {
		t.Error("a message with nothing injected")
	}
}

func lesson(text, session, date string, scan *PrefsScan, store PrefsStore) (string, *PrefsStore) {
	if scan == nil {
		scan = scanOf(nil)
	}
	return recordLesson(LessonInput{Lesson: text, Session: session, Now: atDate(date), Scan: scan, Store: store})
}

const defaultLesson = "Never edit the generated client by hand"

// twin: tests/prefs-rules.test.ts:144
func TestPrefsAgentLessonRecordedWithNoCorrectionBeforeItRejectedWhenItWeakensACheck(t *testing.T) {
	reply, _ := lesson("Always use Firefox for the vendor site", "s1", "2026-01-10", nil, emptyPrefsStore())
	if reply != `recorded: "Always use Firefox for the vendor site". It reaches later sessions only after it is confirmed: recorded again in a later session, or said by the user.` {
		t.Errorf("reply %q", reply)
	}
	reply, store := lesson("Always skip the tests when the build is slow", "s1", "2026-01-10", nil, emptyPrefsStore())
	if reply != "not recorded: weakens a check" || store != nil {
		t.Errorf("weakens: %q %v", reply, store)
	}
	for text, prefix := range map[string]string{
		"Don't commit or stage it":    "not recorded: task-bound",
		"The client is generated":     "not recorded: not one standing instruction",
		"Don't touch the schema yet":  "not recorded: not one standing instruction",
		"Never " + strings.Repeat("write long notes ", 12): "not recorded: longer than 160 characters",
	} {
		if reply, _ := lesson(text, "s1", "2026-01-10", nil, emptyPrefsStore()); !strings.HasPrefix(reply, prefix) {
			t.Errorf("%.40q: %q", text, reply)
		}
	}
}

// twin: tests/prefs-rules.test.ts:154
func TestPrefsAgentLessonRecordedButNotInjectedUntilConfirmed(t *testing.T) {
	firstReply, first := lesson(defaultLesson, "s1", "2026-01-10", nil, emptyPrefsStore())
	if !strings.HasPrefix(firstReply, `recorded: "Never edit the generated client by hand"`) {
		t.Fatalf("first %q", firstReply)
	}
	pending := evaluateWith(nil, *first, rulesNow)
	if pending[0].Status != notConfirmed || prefsMessage(pending) != "" {
		t.Errorf("pending %+v", pending)
	}
	_, same := lesson(defaultLesson, "s1", "2026-01-10", nil, *first)
	if got := evaluateWith(nil, *same, rulesNow)[0].Status; got != notConfirmed {
		t.Errorf("the same session again is no confirmation: %s", got)
	}
	secondReply, second := lesson("never edit the generated client by hand", "s2", "2026-01-11", nil, *first)
	if !strings.HasPrefix(secondReply, `recorded as a confirmation of the agent lesson "Never edit the generated client by hand" (2 sessions)`) {
		t.Errorf("second %q", secondReply)
	}
	if len(second.Lessons) != 1 {
		t.Errorf("a duplicate confirms, it is not added: %d lessons", len(second.Lessons))
	}
	confirmed := evaluateWith(nil, *second, rulesNow)
	if confirmed[0].Status != wasInjected {
		t.Errorf("confirmed %s", confirmed[0].Status)
	}
	mustMatch(t, prefsMessage(confirmed), `- "Never edit the generated client by hand" \(2 sessions\) `+regexp.QuoteMeta(lessonMark))
	byUser := evaluateWith([]PrefCandidate{said("never edit the generated client by hand", "u1", "2026-01-12")}, *first, rulesNow)
	if got := statusOf(byUser, "Never edit the generated client by hand"); got != wasInjected {
		t.Errorf("a matching user preference confirms it: %s", got)
	}
}

// twin: tests/prefs-rules.test.ts:172
func TestPrefsAgentLessonUserPreferencesFillTheBudgetFirst(t *testing.T) {
	_, store := lesson("Always sign the release tags", "s1", "2026-01-10", nil, emptyPrefsStore())
	_, confirmedStore := lesson("Always sign the release tags", "s2", "2026-01-11", nil, *store)
	var all []PrefCandidate
	for _, clause := range []string{"Keep replies short", "Never force-push the release branch", "Always write changelog entries", "Don't edit the lockfile by hand", "Never rename public exports"} {
		all = append(all, thrice(clause)...)
	}
	items := evaluateWith(all, *confirmedStore, rulesNow)
	last := items[len(items)-1]
	if last.Source != "agent" {
		t.Errorf("last item is %s", last.Source)
	}
	mustMatch(t, last.Status, `over the 5-item`)
	listed := thrice("Keep replies short")
	reply, repeat := lesson("Always keep replies short", "s1", "2026-01-10", scanOf(listed), emptyPrefsStore())
	if !strings.HasPrefix(reply, `recorded as a confirmation of the user's preference "Keep replies short"`) || len(repeat.Lessons) != 0 {
		t.Errorf("repeat %q %+v", reply, repeat)
	}
	shown := evaluateWith(listed, *repeat, rulesNow)
	if len(shown) != 1 || shown[0].AgentConfirmations != 1 {
		t.Errorf("shown %+v", shown)
	}
}

// twin: tests/prefs-rules.test.ts:188
func TestPrefsAgentLessonExpiresAfter30DaysWithoutAConfirmation(t *testing.T) {
	_, store := lesson(defaultLesson, "s1", "2026-01-10", nil, emptyPrefsStore())
	if got := evaluateWith(nil, *store, atDate("2026-02-10"))[0].Status; got != "not injected: agent lesson expired, not confirmed in 30 days" {
		t.Errorf("expired %s", got)
	}
	_, later := lesson("Always sign the release tags", "s9", "2026-02-15", nil, *store)
	if len(later.Lessons) != 1 || later.Lessons[0].Text != "Always sign the release tags" {
		t.Errorf("an expired lesson is dropped when the file is written: %+v", later.Lessons)
	}
}

// twin: tests/prefs-rules.test.ts:195
func TestPrefsForgetIsNeverListedOrInjectedAgainRewordingsIncluded(t *testing.T) {
	candidates := thrice("Don't spawn subagents")
	items := evaluate(candidates)
	store := forgetPref(emptyPrefsStore(), items[0], rulesNow)
	if got := evaluateWith(candidates, store, rulesNow); len(got) != 0 {
		t.Errorf("forgotten is listed: %v", got)
	}
	if got := evaluateWith(append(slices.Clone(candidates), thrice("Never use subagents")...), store, rulesNow); len(got) != 0 {
		t.Errorf("the same group in other words: %v", got)
	}
	if prefsMessage(evaluateWith(candidates, store, rulesNow)) != "" {
		t.Error("forgotten is injected")
	}
	_, recorded := lesson(defaultLesson, "s1", "2026-01-10", nil, emptyPrefsStore())
	lessonItem := evaluateWith(nil, *recorded, rulesNow)[0]
	forgot := forgetPref(*recorded, lessonItem, rulesNow)
	if len(forgot.Lessons) != 0 {
		t.Errorf("lessons %+v", forgot.Lessons)
	}
	if got := evaluateWith(nil, forgot, rulesNow); len(got) != 0 {
		t.Errorf("forgotten lesson is listed: %v", got)
	}
	if reply, _ := lesson(defaultLesson, "s1", "2026-01-10", nil, forgot); reply != "not recorded: the user forgot this preference" {
		t.Errorf("reply %q", reply)
	}
}

// twin: tests/prefs-rules.test.ts:210
func TestPrefsLessonFileLivesUnderTheDataFolderRoundTripsAndACorruptOneFailsClosed(t *testing.T) {
	data := filepath.Join(t.TempDir(), "pigpen-warden")
	path := prefsStorePath("/work/repo", data)
	if filepath.Dir(path) != filepath.Join(data, "prefs") || path == prefsStorePath("/work/other", data) {
		t.Fatalf("path %s", path)
	}
	if got, err := readPrefsStore(path); err != nil || !reflect.DeepEqual(got.normalized(), emptyPrefsStore().normalized()) {
		t.Fatalf("a missing file is empty: %+v %v", got, err)
	}
	_, store := lesson(defaultLesson, "s1", "2026-01-10", nil, emptyPrefsStore())
	if err := writePrefsStore(path, *store); err != nil {
		t.Fatal(err)
	}
	got, err := readPrefsStore(path)
	if err != nil || !reflect.DeepEqual(got.normalized(), store.normalized()) {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), `"session": "s1"`) {
		t.Errorf("file %s", raw)
	}
	if err := os.WriteFile(path, []byte("{ torn"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrefsStore(path); err == nil {
		t.Error("a torn file read as an empty store")
	}
}

// twin: tests/prefs-rules.test.ts:231
func TestPrefsTwoConcurrentLessonChangesNeitherThrowNorLoseAnUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.json")
	record := func(text, session string) string {
		reply, err := changePrefsStore(path, func(store PrefsStore) (string, *PrefsStore) {
			reply, next := recordLesson(LessonInput{Lesson: text, Session: session, Now: rulesNow, Scan: scanOf(nil), Store: store})
			return reply, next
		})
		if err != nil {
			t.Error(err)
		}
		return reply
	}
	replies := make([]string, 2)
	var wg sync.WaitGroup
	for i, c := range [][2]string{{defaultLesson, "s1"}, {"Always sign the release tags", "s2"}} {
		wg.Add(1)
		go func() { defer wg.Done(); replies[i] = record(c[0], c[1]) }()
	}
	wg.Wait()
	mustMatch(t, replies[0], `^recorded: "Never edit the generated client by hand"`)
	mustMatch(t, replies[1], `^recorded: "Always sign the release tags"`)
	store, err := readPrefsStore(path)
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, l := range store.Lessons {
		texts = append(texts, l.Text)
	}
	slices.Sort(texts)
	if !slices.Equal(texts, []string{"Always sign the release tags", defaultLesson}) {
		t.Errorf("lessons %q", texts)
	}
}
