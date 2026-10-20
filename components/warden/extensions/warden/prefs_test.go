package warden

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// Twins of tests/prefs.test.ts. The fixtures are copies of the original's tests/fixtures/prefs.

var prefsNow = time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC).UnixMilli()

const prefsCurrent = "2026-01-04T10-00-00-000Z_current.jsonl"

// prefsFixtureDir copies the fixtures and pins each file's modification time to the session date in its name: the
// window is measured from file modification times.
func prefsFixtureDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	entries, err := os.ReadDir("testdata/prefs")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join("testdata/prefs", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, e.Name())
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		at, err := time.Parse(time.RFC3339, e.Name()[:10]+"T12:00:00Z")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func clauseList(t *testing.T, text string, want ...string) {
	t.Helper()
	got := extractPreferences(text)
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !slices.Equal(got, want) {
		t.Errorf("extractPreferences(%q) = %q, want %q", text, got, want)
	}
}

// twin: tests/prefs.test.ts:29
func TestPrefsDetectionImperativeFormsAreCandidates(t *testing.T) {
	clauseList(t, "Please don't open pull requests, leave the branch local.", "Don't open pull requests, leave the branch local")
	clauseList(t, "looks fine. never force-push the release branch", "Never force-push the release branch")
	clauseList(t, "Always run the linter before you commit.", "Always run the linter before you commit")
	clauseList(t, "stop adding emoji to the changelog", "Stop adding emoji to the changelog")
	clauseList(t, "ok, from now on write commit messages in the imperative mood", "Write commit messages in the imperative mood")
	clauseList(t, "next time, ask before you delete a branch", "Ask before you delete a branch")
	clauseList(t, "I told you to keep the tests offline", "Keep the tests offline")
	clauseList(t, "yes proceed. but do not bump the version in this commit", "Do not bump the version in this commit")
}

// twin: tests/prefs.test.ts:40
func TestPrefsDetectionQuestionsStatementsFragmentsAndRelayedTextAreNotCandidates(t *testing.T) {
	for _, text := range []string{
		// A question asks for a reason or suggests one step; it is not a standing preference.
		"why don't you run the linter first?", "Why do you always amend the last commit?", "do you always run the full suite?",
		// The dropped "I": a statement about the user, not an instruction.
		"don't know why the build broke", "i don't like how the panel looks", "don't worry about the flaky test",
		"the watcher never closes the file handle", "it never printed a summary", "i told you i did", "agents started from now on would use it too",
		// One content word is a fragment.
		"don't merge",
	} {
		clauseList(t, text)
	}
	for text, want := range map[string]bool{
		"# Order\n\nNever push to main.":                              false,
		"<skill name=\"x\">\nAlways lint.\n</skill>":                  false,
		"STATUS REPORT   FROM: worker-1\nNever push to main.":        false,
		"SCHEDULE · 10:00 · check the queue\nNever push to main.":    false,
		"never push to main. " + strings.Repeat("x", 2100):           false,
		"never push to main":                                          true,
	} {
		if got := isHumanTyped(text); got != want {
			t.Errorf("isHumanTyped(%.40q) = %v", text, got)
		}
	}
}

// twin: tests/prefs.test.ts:62
func TestPrefsDetectionAClauseIsRedactedAndCapped(t *testing.T) {
	got := extractPreferences("never paste the api_key=abcd1234efgh5678 value into the chat log")
	if len(got) != 1 || got[0] != "Never paste the api_key=[redacted] value into the chat log" {
		t.Fatalf("redacted: %q", got)
	}
	long := extractPreferences("always " + strings.Repeat("write a long careful note ", 20))
	if len(long) != 1 || utf16Len(long[0]) > clauseChars || !strings.HasSuffix(long[0], "…") {
		t.Fatalf("capped: %q", long)
	}
}

func atDay(day int) int64 { return time.Date(2026, 1, day, 0, 0, 0, 0, time.UTC).UnixMilli() }

func prefTexts(prefs []StandingPref) [][2]any {
	var out [][2]any
	for _, p := range prefs {
		out = append(out, [2]any{p.Text, p.Sessions})
	}
	return out
}

// twin: tests/prefs.test.ts:70
func TestPrefsGroupingNearDuplicatesShareAGroupPolarityKeepsOppositesApart(t *testing.T) {
	if jaccard(tokens("don't open pull requests, leave the branch local"), tokens("dont open pull requests leave the branch local")) < 0.6 {
		t.Error("near-duplicates are apart")
	}
	if jaccard(tokens("don't use tabs"), tokens("dont use tabs in the makefile")) < 0.6 {
		t.Error("tabs wordings are apart")
	}
	prefs := groupPreferences([]PrefCandidate{
		{Clause: "Don't use tabs", Session: "s1", At: atDay(1)},
		{Clause: "Dont use tabs in the makefile", Session: "s2", At: atDay(2)},
		{Clause: "Never squash commits", Session: "s1", At: atDay(1)},
		{Clause: "Always squash commits", Session: "s2", At: atDay(2)},
	}, nil, prefsMinSessions, maxPrefs)
	if len(prefs) != 1 || prefs[0].Text != "Dont use tabs in the makefile" || prefs[0].Sessions != 2 || prefs[0].LastAt != atDay(2) {
		t.Fatalf("prefs %+v", prefs)
	}
}

// twin: tests/prefs.test.ts:83
func TestPrefsTwoSessionThresholdRepeatsInOneSessionDoNotCount(t *testing.T) {
	prefs := groupPreferences([]PrefCandidate{
		{Clause: "Never bump the version", Session: "s1", At: atDay(1)},
		{Clause: "Never bump the version", Session: "s1", At: atDay(2)},
		{Clause: "Keep replies short please", Session: "s1", At: atDay(1)},
		{Clause: "Keep replies short", Session: "s2", At: atDay(2)},
		{Clause: "Run the linter first", Session: "s1", At: atDay(3)},
		{Clause: "Run the linter first", Session: "s2", At: atDay(4)},
		{Clause: "Keep replies short", Session: "s3", At: atDay(1)},
	}, nil, prefsMinSessions, maxPrefs)
	if !reflect.DeepEqual(prefTexts(prefs), [][2]any{{"Keep replies short", 3}, {"Run the linter first", 2}}) {
		t.Fatalf("prefs %v", prefTexts(prefs))
	}
}

// twin: tests/prefs.test.ts:97
func TestPrefsScanHumanUserMessagesOfEarlierSessionsOnlyTheCurrentSessionIsExcluded(t *testing.T) {
	dir := prefsFixtureDir(t)
	before, _ := os.ReadFile(filepath.Join(dir, prefsCurrent))
	scan := scanPreferences(ScanOptions{Dir: dir, Exclude: filepath.Join(dir, prefsCurrent), Now: prefsNow})
	if scan.Scanned != 3 {
		t.Fatalf("scanned %d", scan.Scanned)
	}
	if !reflect.DeepEqual(prefTexts(scan.Prefs), [][2]any{
		{"Never paste the api_key=[redacted] value into the chat log", 2},
		{"Write commit messages in imperative mood", 2},
		{"Dont open pull requests, leave the branch local", 2},
	}) {
		t.Fatalf("prefs %v", prefTexts(scan.Prefs))
	}
	var texts []string
	for _, p := range scan.Prefs {
		texts = append(texts, p.Text)
	}
	text := strings.Join(texts, "\n")
	if regexp.MustCompile(`abcd1234|zzzz9999`).MatchString(text) {
		t.Error("not redacted")
	}
	if regexp.MustCompile(`(?i)formatter`).MatchString(text) {
		t.Error("tool results, custom messages, relayed orders, and questions are not skipped")
	}
	if regexp.MustCompile(`(?i)linter`).MatchString(text) {
		t.Error("one earlier session plus the current one is a standing preference")
	}
	if after, _ := os.ReadFile(filepath.Join(dir, prefsCurrent)); string(after) != string(before) {
		t.Error("the session file was written")
	}
	withCurrent := scanPreferences(ScanOptions{Dir: dir, Now: prefsNow})
	found := false
	for _, p := range withCurrent.Prefs {
		found = found || regexp.MustCompile(`(?i)linter`).MatchString(p.Text)
	}
	if !found {
		t.Error("the current session is not read when it is not excluded")
	}
}

// twin: tests/prefs.test.ts:115
func TestPrefsScanTheWindowDropsOldSessionsAndCapsTheCountAMissingDirectoryIsEmpty(t *testing.T) {
	dir := prefsFixtureDir(t)
	if got := scanPreferences(ScanOptions{Dir: dir, Now: prefsNow, MaxAgeDays: 2}); got.Scanned != 2 {
		t.Errorf("recent scanned %d", got.Scanned)
	}
	capped := scanPreferences(ScanOptions{Dir: dir, Now: prefsNow, MaxSessions: 1})
	if capped.Scanned != 1 || len(capped.Prefs) != 0 {
		t.Errorf("capped %d %v", capped.Scanned, capped.Prefs)
	}
	missing := scanPreferences(ScanOptions{Dir: filepath.Join(dir, "absent"), Now: prefsNow})
	if len(missing.Prefs) != 0 || len(missing.Candidates) != 0 || missing.Scanned != 0 || missing.Directories != 1 {
		t.Errorf("missing %+v", missing)
	}
}

// twin: tests/prefs.test.ts:125
func TestPrefsCommandOutputListsEachItemWithCountDaysLastDateAndStatusWithTheHint(t *testing.T) {
	dir := prefsFixtureDir(t)
	scan := scanPreferences(ScanOptions{Dir: dir, Exclude: prefsCurrent, Now: prefsNow})
	text := formatPrefs(scan.Scanned, scan.Directories, evaluatePrefs(scan, emptyPrefsStore(), prefsNow))
	mustMatch(t, text, `^Standing preferences \(repeated in 2\+ of the last 3 sessions of this project; injected from 3 sessions on 2 days\):`)
	mustMatch(t, text, `1\. Never paste the api_key=\[redacted\] value into the chat log \(2 sessions on 2 days, last 2026-01-03\): not injected: seen in 2 sessions`)
	if !strings.HasSuffix(text, prefsHint) {
		t.Error("no hint")
	}
	mustMatch(t, formatPrefs(1, 1, nil), `No standing preferences: nothing was repeated in 2 or more of the last 1 session of this project, and the agent recorded no lesson\.`)
}

// twin: tests/prefs.test.ts:134
func TestPrefsScanTimeOnTheFixtureStaysSmallAndTheFilesAreUntouched(t *testing.T) {
	dir := prefsFixtureDir(t)
	stamps := func() []time.Time {
		var out []time.Time
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			info, _ := e.Info()
			out = append(out, info.ModTime())
		}
		return out
	}
	before := stamps()
	scan := scanPreferences(ScanOptions{Dir: dir, Now: prefsNow})
	if scan.Ms >= 300 {
		t.Errorf("scan took %.1f ms", scan.Ms)
	}
	if !reflect.DeepEqual(stamps(), before) {
		t.Error("modification times changed")
	}
}

// twin: tests/prefs.test.ts:141
func TestPrefsDetectionRelayedOrdersAndMessagesFromOtherAgentsAreNotTheUser(t *testing.T) {
	for _, text := range []string{
		"# WORK ORDER\n\nPROJECT: docs\nBRANCH: tidy-readme\n\nNever push to main.",
		"Tidy the docs.\nPROJECT: docs\nBRANCH: tidy-readme\nNever push to main.",
		"From Lead agent:\n\nAdded rule: offline only. Do not run the live tests. Continue your task.",
		"Heads up from the Planner: never push to main while the release is open.",
		"Added by the owner: never push to main while the release is open.",
		"Owner change: never push to main, open a draft instead.",
	} {
		if isHumanTyped(text) {
			t.Errorf("isHumanTyped(%.40q) = true", text)
		}
	}
	clauseList(t, "From Lead agent:\n\nAdded rule: offline only. Do not run the live tests. Continue your task.")
	if !isHumanTyped("from now on: never push to main") {
		t.Error(`a lowercase "from" line is the user's own`)
	}
	if !isHumanTyped("Note: never push to main") {
		t.Error("one label is not a header block")
	}
}

// twin: tests/prefs.test.ts:153
func TestPrefsDetectionATemporaryHoldIsNotAStandingPreference(t *testing.T) {
	clauseList(t, "don't commit the migration yet")
	clauseList(t, "always skip the e2e suite for now")
	clauseList(t, "don't push until the checks pass")
}

// twin: tests/prefs.test.ts:159
func TestPrefsGroupingDifferentlyWordedClausesThatShareARareSubjectWordAreOnePreference(t *testing.T) {
	counts := emptyWordCounts()
	counts.Messages = 100
	for word, n := range map[string]int{"subagent": 2, "review": 2, "spawn": 1, "commit": 3, "push": 3, "bump": 1, "version": 2} {
		counts.Words[word] = n
	}
	subagents := []PrefCandidate{
		{Clause: "Don't spawn subagents", Session: "s1", At: atDay(1)},
		{Clause: "Never use subagents, review it yourself", Session: "s2", At: atDay(2)},
	}
	if jaccard(tokens(subagents[0].Clause), tokens(subagents[1].Clause)) >= 0.6 {
		t.Error("the words alone match")
	}
	if got := prefTexts(groupPreferences(subagents, counts, prefsMinSessions, maxPrefs)); !reflect.DeepEqual(got, [][2]any{{"Never use subagents, review it yourself", 2}}) {
		t.Errorf("rare word: %v", got)
	}
	common := emptyWordCounts()
	common.Messages = 100
	common.Words["subagent"] = 40
	if got := groupPreferences(subagents, common, prefsMinSessions, maxPrefs); len(got) != 0 {
		t.Errorf("a common word is a subject: %v", got)
	}
	// A shared rare word with little else in common stays apart.
	if got := groupPreferences([]PrefCandidate{
		{Clause: "Always commit and push", Session: "s1", At: atDay(1)},
		{Clause: "Always commit and bump the version", Session: "s2", At: atDay(2)},
	}, counts, prefsMinSessions, maxPrefs); len(got) != 0 {
		t.Errorf("little in common: %v", got)
	}
	if got := groupPreferences([]PrefCandidate{
		{Clause: "Always spawn subagents", Session: "s1", At: atDay(1)},
		{Clause: "Never use subagents, review it yourself", Session: "s2", At: atDay(2)},
	}, counts, prefsMinSessions, maxPrefs); len(got) != 0 {
		t.Errorf("opposite polarity grouped: %v", got)
	}
}

// twin: tests/prefs.test.ts:185
func TestPrefsScanSessionsFromAnotherWorktreeOfTheSameRepositoryAreRead(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	git := func(cwd string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", cwd, "-c", "user.name=test", "-c", "user.email=test@example.com"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	main, worktree, other := filepath.Join(base, "repo"), filepath.Join(base, "repo-feature"), filepath.Join(base, "other")
	nested := filepath.Join(main, "vendor", "lib")
	for _, p := range []string{main, other, nested} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	git(main, "init", "-q")
	git(main, "commit", "-q", "--allow-empty", "-m", "init")
	git(main, "worktree", "add", "-q", worktree)
	git(other, "init", "-q")
	git(nested, "init", "-q")
	sessions := filepath.Join(base, "sessions")
	session := func(cwd, name, text string) string {
		dir := filepath.Join(sessions, regexp.MustCompile(`[/\\]`).ReplaceAllString(strings.TrimPrefix(cwd, base), "-"))
		if dir == filepath.Join(sessions, "") {
			dir = filepath.Join(sessions, "root")
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		header := `{"type":"session","version":3,"id":"` + name + `","timestamp":"2026-01-02T00:00:00.000Z","cwd":` + jsonString(cwd) + `}`
		message := `{"type":"message","id":"m1","parentId":null,"timestamp":"2026-01-02T00:00:01.000Z","message":{"role":"user","content":` + jsonString(text) + `}}`
		file := filepath.Join(dir, name+".jsonl")
		if err := os.WriteFile(file, []byte(header+"\n"+message+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		at := time.UnixMilli(prefsNow - 86_400_000)
		if err := os.Chtimes(file, at, at); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	own := session(main, "s-main", "never force-push the release branch. always squash merge feature branches")
	session(worktree, "s-worktree", "please never force-push the release branch")
	session(filepath.Join(main, "docs"), "s-subdir", "ok, never force-push the release branch")
	session(other, "s-other", "always squash merge feature branches")
	session(nested, "s-nested", "always squash merge feature branches")
	scan := scanPreferences(ScanOptions{Dir: own, Cwd: main, Now: prefsNow})
	if scan.Directories != 3 {
		t.Errorf("directories %d: the project, its worktree, and a subdirectory of it", scan.Directories)
	}
	if !reflect.DeepEqual(prefTexts(scan.Prefs), [][2]any{{"Never force-push the release branch", 3}}) {
		t.Errorf("prefs %v", prefTexts(scan.Prefs))
	}
	mustMatch(t, formatPrefs(scan.Scanned, scan.Directories, evaluatePrefs(scan, emptyPrefsStore(), prefsNow)), `of the last 3 sessions of this project and its worktrees`)
	if scan.Project != main {
		t.Errorf("project %q, want the main worktree %q", scan.Project, main)
	}
	if alone := scanPreferences(ScanOptions{Dir: own, Now: prefsNow}); alone.Directories != 1 {
		t.Errorf("without a working directory %d directories", alone.Directories)
	}
	if outside := scanPreferences(ScanOptions{Dir: own, Cwd: base, Now: prefsNow}); outside.Directories != 1 {
		t.Errorf("outside a repository %d directories", outside.Directories)
	}
}
