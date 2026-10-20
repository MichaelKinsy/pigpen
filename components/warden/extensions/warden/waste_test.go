package warden

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Twins of tests/waste.test.ts.

func wcfg() WasteConfig {
	return WasteConfig{Enabled: true, Tip: true, Every: 20, Sleep: true, Paging: true, Search: true, Recheck: true}
}

func wcall(tool string, input map[string]any, result string) WasteCall {
	return WasteCall{Tool: tool, Input: input, Cwd: "/work/project", Result: result}
}

func wbash(command, result string) WasteCall {
	return wcall("bash", map[string]any{"command": command}, result)
}

func wlines(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	return b.String()
}

const pollCommand = "sleep 5; curl -s http://127.0.0.1:4000/api/state | jq .status"

func wpoll() WasteCall { return wbash(pollCommand, `{ "status": "running" }`) }

func nudgeText(n *WasteNudge) string {
	if n == nil {
		return ""
	}
	return n.Text
}

func wread(path string, offset, limit int, result string) WasteCall {
	return wcall("read", map[string]any{"path": path, "offset": float64(offset), "limit": float64(limit)}, result)
}

// twin: tests/waste.test.ts:18
func TestWasteSleepASecondPollInTheWindowFiresOnePollALoopAndASleepingChainDoNot(t *testing.T) {
	if SleepNudge([]WasteCall{wpoll()}) != nil {
		t.Error("one poll is waiting, not polling")
	}
	n := SleepNudge([]WasteCall{wpoll(), wbash("git status --short", ""), wpoll()})
	if n == nil {
		t.Fatal("two polls inside the window fire")
	}
	mustMatch(t, n.Text, `^You have waited 2 times in the last 3 calls\.`, "text")
	mustMatch(t, n.Text, "one sleep long enough, or a blocking command such as `gh pr checks --watch`\\.", "text")

	chain := "sleep 5; git fetch origin && git rebase main && npm test"
	if IsSleepPoll(chain) {
		t.Error("a chain of work is not a poll")
	}
	if SleepNudge([]WasteCall{wbash(chain, ""), wbash(chain, "")}) != nil {
		t.Error("chain fires")
	}
	loop := "while [ ! -f /tmp/build.done ]; do sleep 5; done"
	if IsSleepPoll(loop) || IsSleepPoll("until gh pr checks 12; do sleep 10; done") {
		t.Error("a loop is not a poll")
	}
	if SleepNudge([]WasteCall{wbash(loop, ""), wbash(loop, "")}) != nil {
		t.Error("loop fires")
	}
	if !IsSleepPoll("sleep 90; tail -4 /tmp/build.log") {
		t.Error("a sleep with a status tail is a poll")
	}
	if SleepNudge([]WasteCall{wbash("sleep 90; tail -4 /tmp/build.log", "")}) != nil {
		t.Error("alone it does not fire")
	}
	if !IsSleepPoll("cd /tmp/run && sleep 20 && gh pr checks 12") {
		t.Error("cd prefix")
	}
	if IsSleepPoll("sleep 5; echo one; echo two") {
		t.Error("two follow-ups")
	}
}

// twin: tests/waste.test.ts:47
func TestWastePagingThreeAdjacentRangedReadsOfOneFileFireWithTheOneReadAlternative(t *testing.T) {
	reads := []WasteCall{
		wread("src/extension.ts", 745, 62, wlines(62)),
		wread("src/extension.ts", 760, 22, wlines(22)),
		wread("src/extension.ts", 750, 17, wlines(17)),
	}
	n := PagingNudge(reads)
	if n == nil || n.Detector != DetectPaging {
		t.Fatalf("three overlapping reads of one file fire: %+v", n)
	}
	mustMatch(t, n.Text, `^You read src/extension\.ts in 3 calls and the parts run from line 745 to 806 without a gap\.`, "text")
	mustMatch(t, n.Text, "One `read` with offset=745, limit=62 returns the same lines\\.", "text")

	var far []WasteCall
	for _, r := range []string{"40,60", "400,420", "900,920"} {
		c := wbash("sed -n '"+r+"p' src/big.ts", wlines(20))
		if _, ok := RangeReadOf(c); !ok {
			t.Error("sed ranges are ranged reads")
		}
		far = append(far, c)
	}
	if PagingNudge(far) != nil {
		t.Error("far apart ranges do not fire")
	}
	edited := []WasteCall{reads[0], wcall("edit", map[string]any{"path": "src/extension.ts", "edits": []any{map[string]any{"oldText": "a", "newText": "b"}}}, ""), reads[1], reads[2]}
	if PagingNudge(edited) != nil {
		t.Error("an edit between voids the note")
	}
	if _, ok := RangeReadOf(wcall("read", map[string]any{"path": "src/big.ts"}, wlines(20))); ok {
		t.Error("a whole-file read is not a page")
	}
	if _, ok := RangeReadOf(wread("src/big.ts", 1, 200, wlines(200))); ok {
		t.Error("a big ranged read is not a page")
	}
	wide := []WasteCall{wread("src/big.ts", 1, 200, wlines(200)), wread("src/big.ts", 1, 200, wlines(200)), wread("src/big.ts", 1, 200, wlines(200))}
	if PagingNudge(wide) != nil {
		t.Error("wide reads")
	}
	script := wbash("python3 - <<'PY'\nprint(open('src/extension.ts').read())\nPY", wlines(200))
	if _, ok := RangeReadOf(script); ok {
		t.Error("a heredoc has no pages")
	}
	if _, ok := RangeReadOf(wbash(`node -e "console.log(1)"`, "")); ok {
		t.Error("node -e has no pages")
	}
	from := func(path string) WasteCall { return wread(path, 10, 5, wlines(5)) }
	cwdReads := []WasteCall{from("src/a.ts"), wbash("cd src && sed -n '12,16p' a.ts", wlines(5)), from("./src/a.ts")}
	if PagingNudge(cwdReads) == nil {
		t.Error("the same file reached two ways is one run of pages")
	}
}

// twin: tests/waste.test.ts:85
func TestWasteSearchThreeSearchesOfOneFileWithAnOverlappingPatternFire(t *testing.T) {
	key := wbash(`grep " 429" /tmp/x/backend.log`, "line: 429\n")
	again := wbash(`grep " 429" /tmp/x/backend.log`, "line: 429\n")
	counted := wbash(`grep -c "ERROR" /tmp/x/backend.log`, "12\n")
	n := SearchNudge([]WasteCall{key, again, counted})
	if n == nil {
		t.Fatal("three searches of one file fire")
	}
	mustMatch(t, n.Text, `^You searched /tmp/x/backend\.log 3 times with the same or an overlapping pattern\.`, "text")
	mustMatch(t, n.Text, `reuse them, or widen the search once\.`, "text")

	recursive := wbash("grep -rn foo src/", "src/a.ts:1:foo\n")
	if _, ok := SearchReadOf(recursive); ok {
		t.Error("recursive names no file")
	}
	if SearchNudge([]WasteCall{recursive, recursive, recursive}) != nil {
		t.Error("recursive fires")
	}
	if _, ok := SearchReadOf(wbash("grep foo 'src/*.ts'", "")); ok {
		t.Error("a glob names no file")
	}
	if SearchNudge([]WasteCall{key}) != nil {
		t.Error("one search")
	}
	got, _ := SearchReadOf(key)
	if !reflect.DeepEqual(got, SearchRead{File: "/tmp/x/backend.log", Path: "/tmp/x/backend.log", Pattern: " 429"}) {
		t.Errorf("search %+v", got)
	}
	if rg, _ := SearchReadOf(wbash("rg -n 'class Foo' src/foo.ts", "")); rg.Pattern != "class Foo" {
		t.Errorf("rg pattern %q", rg.Pattern)
	}
	written := []WasteCall{key, wcall("edit", map[string]any{"path": "/tmp/x/backend.log", "edits": []any{}}, ""), again, counted}
	if SearchNudge(written) != nil {
		t.Error("a write between voids the note")
	}
	var distinct []WasteCall
	for _, p := range []string{"alpha", "beta", "gamma"} {
		distinct = append(distinct, wbash("grep "+p+" /tmp/x/backend.log", ""))
	}
	if SearchNudge(distinct) != nil {
		t.Error("no two patterns overlap")
	}
}

// twin: tests/waste.test.ts:115
func TestWastePatternsIdenticalContainedAndSharedAlternativesOverlap(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"foo", "foo", true}, {" foo ", "foo", true}, {"class Foo", "Foo", true}, {"ERROR|WARN", "WARN|DEBUG", true}, {"alpha", "beta", false}} {
		if PatternsOverlap(c.a, c.b) != c.want {
			t.Errorf("%q %q", c.a, c.b)
		}
	}
}

// twin: tests/waste.test.ts:123
func TestWasteRecheckAFilteredRerunOfTheSameCheckFiresAFailureAndAnUnpipedRerunDoNot(t *testing.T) {
	first := wbash("npm run check 2>&1 | tail -30", "…\nℹ tests 1175\n")
	second := wbash(`npm run check 2>&1 | grep -E "^(ℹ|✖)"`, "ℹ tests 1175\n")
	n := RecheckNudge([]WasteCall{first, second})
	if n == nil || n.Subject != "npm run check" {
		t.Fatalf("recheck %+v", n)
	}
	mustMatch(t, n.Text, `^You ran npm run check 2 times and filtered the output each time\. Run it once without a pipe;`, "text")

	failing := first
	failing.Result = "…\n# fail 2\n✖ two tests failed\n"
	if RecheckNudge([]WasteCall{failing, second}) != nil {
		t.Error("a result that showed a failure")
	}
	failedCall := first
	failedCall.Failed = true
	if RecheckNudge([]WasteCall{failedCall, second}) != nil {
		t.Error("a failed call")
	}
	written := []WasteCall{first, wcall("write", map[string]any{"path": "src/config.ts", "content": "x"}, ""), second}
	if RecheckNudge(written) != nil {
		t.Error("a write between")
	}
	if RecheckNudge([]WasteCall{first, wbash("npm run check", "ℹ tests 1175\n")}) != nil {
		t.Error("unpiped re-run")
	}
	if RecheckNudge([]WasteCall{first, wbash("npm run check 2>&1 | tail -30", "ℹ tests 1175\n")}) != nil {
		t.Error("same filter")
	}
	if r, _ := CheckRunOf(wbash("npm test 2>&1 > /tmp/out.log | tail -20", "")); r.Key != "npm test" {
		t.Errorf("key %q", r.Key)
	}
	if r, _ := CheckRunOf(wbash("cd /work/project && npx tsc --noEmit | head -20", "")); r.Key != "npx tsc --noEmit" {
		t.Errorf("key %q", r.Key)
	}
	if _, ok := CheckRunOf(wbash("python3 scripts/build.py", "")); ok {
		t.Error("not a check")
	}
	other := wbash("npm run build 2>&1 | grep error", "ℹ tests 1175\n")
	if RecheckNudge([]WasteCall{first, other}) != nil {
		t.Error("a different check is not a re-run")
	}
}

// twin: tests/waste.test.ts:154
func TestWasteWritesAWriteToTheFileBetweenTwoReadsVoidsTheNote(t *testing.T) {
	for _, c := range []struct {
		call WasteCall
		want []string
	}{
		{wcall("edit", map[string]any{"path": "src/a.ts", "edits": []any{}}, ""), []string{"/work/project/src/a.ts"}},
		{wbash("npm test > /tmp/out.log", ""), []string{"/tmp/out.log"}},
		{wbash("sed -i '' 's/a/b/' src/a.ts", ""), []string{"/work/project/src/a.ts"}},
		{wbash("cd src && cp a.ts b.ts", ""), []string{"/work/project/src/a.ts", "/work/project/src/b.ts"}},
		{wcall("read", map[string]any{"path": "src/a.ts"}, ""), nil},
		{wbash("git status --short", ""), nil},
	} {
		got := WrittenPaths(c.call)
		if len(got) != len(c.want) || (len(got) > 0 && !reflect.DeepEqual(got, c.want)) {
			t.Errorf("%v: %v want %v", c.call.Input, got, c.want)
		}
	}
}

// twin: tests/waste.test.ts:163
func TestWasteRateLimitOneNotePerDetectorPerWindowOfCalls(t *testing.T) {
	tr := NewWasteTracker()
	var fired []*WasteNudge
	c := wcfg()
	for range 22 {
		if n := tr.Record(wpoll(), c); n != nil {
			fired = append(fired, n)
		}
	}
	if len(fired) != 2 {
		t.Fatalf("calls 2 and 22 are 20 calls apart: %d", len(fired))
	}
	mustMatch(t, fired[0].Text, `2 times in the last 2 calls`, "first")
	mustMatch(t, fired[1].Text, `10 times in the last 10 calls`, "second")

	off := NewWasteTracker()
	noSleep := wcfg()
	noSleep.Sleep = false
	if off.Record(wpoll(), noSleep) != nil || off.Record(wpoll(), noSleep) != nil {
		t.Error("a detector switched off is silent")
	}
	disabled := wcfg()
	disabled.Enabled = false
	if off.Record(wpoll(), disabled) != nil || off.Record(wpoll(), disabled) != nil {
		t.Error("the whole guard off is silent")
	}
	reset := NewWasteTracker()
	if reset.Record(wpoll(), wcfg()) != nil || reset.Record(wpoll(), wcfg()) == nil {
		t.Error("second poll fires")
	}
	reset.Reset()
	if reset.Record(wpoll(), wcfg()) != nil || reset.Record(wpoll(), wcfg()) == nil {
		t.Error("a reset starts a new session")
	}
}

// twin: tests/waste.test.ts:190
func TestWasteNotesAreTextForAToolResultNoHoldNoBlockNoLevel(t *testing.T) {
	n := SleepNudge([]WasteCall{wpoll(), wpoll()})
	if n == nil {
		t.Fatal("nudge")
	}
	// The note is a detector, a subject and text: nothing that could hold, block or set a level.
	rt := reflect.TypeOf(*n)
	var fields []string
	for i := range rt.NumField() {
		fields = append(fields, rt.Field(i).Name)
	}
	mustEqual(t, fields, []string{"Detector", "Subject", "Text"}, "fields")
}

// twin: tests/waste.test.ts:199
func TestWasteTipAppendedAfterThePromptItFindsNeverInPlaceOfIt(t *testing.T) {
	mustEqual(t, WithWasteTip("", WasteTip), WasteTip, "empty")
	mustEqual(t, WithWasteTip("BASE PROMPT", WasteTip), "BASE PROMPT\n\n"+WasteTip, "append")
	mustEqual(t, WithWasteTip("BASE PROMPT\n\n"+WasteTip, WasteTip), "BASE PROMPT\n\n"+WasteTip, "idempotent")
	if !strings.HasPrefix(WithWasteTip("BASE PROMPT", WasteTip), "BASE PROMPT") {
		t.Error("prefix")
	}
	mustMatch(t, WasteTip, `^Tool calls are expensive: each one re-reads the whole conversation\.`, "tip head")
	mustMatch(t, WasteTip, `Wait for slow work with one blocking command, not repeated sleeps\.$`, "tip tail")
}
