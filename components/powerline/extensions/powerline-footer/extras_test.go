package powerline_footer

import (
	"os"
	"path/filepath"
	"testing"
)

// Tests the port adds: behavior the original's tests do not pin and the render states cannot reach.

func TestMightChangeGitBranch(t *testing.T) {
	for cmd, want := range map[string]bool{
		"git checkout main": true, "git switch -c x": true, "git branch -d old": true, "git branch -M new": true, "git merge dev": true,
		"git rebase main": true, "git pull": true, "git reset --hard": true, "git worktree add x": true, "git stash pop": true, "git stash apply": true,
		"git status": false, "git branch": false, "git stash": false, "echo git checkout": true, "ls": false, "gitcheckout": false,
	} {
		eq(t, mightChangeGitBranch(cmd), want, cmd)
	}
}

func TestParseGitStatusOutput(t *testing.T) {
	st, un, ut := parseGitStatusOutput(" M a\nM  b\nMM c\n?? d\n?? e\nA  f\n D g\nUU h\n\n")
	eq(t, [3]int{st, un, ut}, [3]int{4, 4, 2}, "staged, unstaged, untracked")
	st, un, ut = parseGitStatusOutput("")
	eq(t, [3]int{st, un, ut}, [3]int{0, 0, 0}, "empty")
}

func TestAgentDirEdges(t *testing.T) {
	home, _ := pathEnv(t)
	eq(t, normalizeAgentDirPath("~x"), "~x", "a tilde that is not the home directory stays")
	eq(t, normalizeAgentDirPath("  /abs/path  "), "/abs/path", "trimmed")
	// The primary sessions dir may itself be the legacy one: it is then listed once.
	setenv(t, "PI_CODING_AGENT_DIR", s(filepath.Join(home, ".pi")))
	os.MkdirAll(filepath.Join(home, ".pi", "sessions"), 0o755)
	eq(t, getAgentSessionDirs(), []string{filepath.Join(home, ".pi", "sessions")}, "no duplicate")
}

func TestJSONOrderAndText(t *testing.T) {
	v := js(t, `{"b":1,"2":"x","a":[],"1":{},"c":"q\"\\\n\u0001é\u2028"}`)
	want := "{\n  \"1\": {},\n  \"2\": \"x\",\n  \"b\": 1,\n  \"a\": [],\n  \"c\": \"q\\\"\\\\\\n\\u0001é\u2028\"\n}"
	eq(t, marshalJSON(v, "  "), want, "pretty, index keys first, JSON.stringify escapes")
	eq(t, marshalJSON(js(t, `{"a":[1,2.5,{"b":null}],"c":true}`), ""), `{"a":[1,2.5,{"b":null}],"c":true}`, "compact")
	eq(t, marshalJSON(js(t, `[]`), "  "), "[]", "empty array")
	eq(t, marshalJSON(js(t, `{}`), "  "), "{}", "empty object")
	// A repeated key keeps its first position and takes the last value.
	eq(t, marshalJSON(js(t, `{"a":1,"b":2,"a":3}`), ""), `{"a":3,"b":2}`, "duplicate key")
	_, err := parseJSON([]byte(`{"a":1} x`))
	eq(t, err != nil, true, "trailing data")
}

func TestDetachedProviderFallsBackToGit(t *testing.T) {
	dir := t.TempDir()
	makeRepo(t, dir, jo(t, `{"branch":"topic"}`))
	invalidateGitBranch()
	eq(t, sv(getCurrentBranch(s("detached"), dir)), "topic", "a provider that says detached is not trusted")
	invalidateGitBranch()
	eq(t, sv(getCurrentBranch(s("external"), dir)), "external", "a provider branch is used as is")
}

func TestGitPollingModes(t *testing.T) {
	dir := t.TempDir()
	makeRepo(t, dir, jo(t, `{"branch":"main","staged":1,"unstaged":1,"untracked":1}`))
	invalidateGitStatus()
	invalidateGitBranch()
	full := getGitStatus(nil, "full", dir)
	eq(t, [3]int{full.Staged, full.Unstaged, full.Untracked}, [3]int{1, 1, 1}, "full")
	branchOnly := getGitStatus(nil, "branch", dir)
	eq(t, [3]int{branchOnly.Staged, branchOnly.Unstaged, branchOnly.Untracked}, [3]int{0, 0, 0}, "branch mode has no counts")
	eq(t, sv(branchOnly.Branch), "main", "branch mode reads the branch")
	off := getGitStatus(s("given"), "off", dir)
	eq(t, sv(off.Branch), "given", "off mode uses the provider only")
}

func TestStatusHostNameStrip(t *testing.T) {
	// A host named www.github.com or ssh.github.com is github (the sub-domain rule covers both).
	for remote, want := range map[string]string{"https://www.github.com/o/r": "github", "git@ssh.github.com:o/r.git": "github", "https://notgithub.com/o/r": "other", "https://github.com.evil.example/o/r": "other"} {
		got := detectGitHost(&remote)
		eq(t, sv(got), want, remote)
	}
}

func TestMergeSettingsKeepsBaseKeys(t *testing.T) {
	merged := mergeSettings(jo(t, `{"a":{"x":1,"y":2},"b":1}`), jo(t, `{"a":{"y":3},"c":2}`))
	eq(t, marshalJSON(merged, ""), `{"a":{"x":1,"y":3},"b":1,"c":2}`, "deep merge, project wins")
	merged = mergeSettings(jo(t, `{"a":{"x":1}}`), jo(t, `{"a":5}`))
	eq(t, marshalJSON(merged, ""), `{"a":5}`, "a scalar replaces an object")
}

func TestKRWHasNoDecimals(t *testing.T) {
	setCurrencyRatesForTest(map[string]float64{"KRW": 1300, "JPY": 150})
	t.Cleanup(resetCurrencyRatesForTest)
	eq(t, formatUsdCost(1.234, "KRW"), "₩1604", "krw")
	eq(t, formatUsdCost(1, "JPY"), "¥150", "jpy")
	eq(t, formatUsdCost(1, "GBP"), "-- GBP", "no rate")
	eq(t, formatUsdCost(1, ""), "$1.00", "default usd")
}

func TestJSToFixedTies(t *testing.T) {
	for _, c := range []struct {
		x    float64
		d    int
		want string
	}{{2.5, 0, "3"}, {0.5, 0, "1"}, {1.45, 1, "1.4"}, {1.005, 2, "1.00"}, {123.456, 1, "123.5"}, {0, 2, "0.00"}, {999.95, 1, "1000.0"}, {0.0004, 3, "0.000"}} {
		eq(t, jsToFixed(c.x, c.d), c.want, jsNumber(c.x))
	}
	eq(t, jsRound(2.5), 3.0, "round half up")
	eq(t, jsRound(-2.5), -2.0, "round half toward +inf")
}

func TestGitSegmentWithoutABranch(t *testing.T) {
	withNerdFonts(t, "0")
	c := newCtx(segmentOptions{}, func(c *segmentContext) { c.Git = gitStatus{Staged: 1, Untracked: 2} })
	eq(t, stripAnsi(renderSegment("git", c).Content), "+1 ?2", "indicators alone")
	eq(t, renderSegment("git", newCtx(segmentOptions{})), renderedSegment{}, "nothing to show")
}

// A host that asks git only when the git segment is on the bar: no other layout may spawn it.
func TestGitIsNotRunWhenTheSegmentIsAbsent(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	os.MkdirAll(bin, 0o755)
	log := filepath.Join(dir, "calls.log")
	script := "#!/bin/sh\necho \"$*\" >> '" + log + "'\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	setenv(t, "PATH", s(bin+":"+os.Getenv("PATH")))
	home, agent := pathEnv(t)
	_ = home
	setenv(t, "PI_CODING_AGENT_DIR", &agent)
	os.MkdirAll(agent, 0o755)
	os.WriteFile(filepath.Join(agent, "settings.json"), []byte(`{"powerline":{"layout":{"left":["model","path"]}}}`), 0o644)
	st := jo(t, `{"model":{"id":"m","name":"M"}}`)
	h := &scriptedHost{st: st, cwd: dir, model: &modelInfo{ID: "m", Name: "M"}, thinking: "off", theme: plainTheme{}, branch: &fakeBranch{}}
	p := newPowerline(h)
	p.sessionStart("startup")
	p.renderTop(100)
	if _, err := os.Stat(log); err == nil {
		t.Errorf("git was spawned although no git segment is on the bar")
	}
}
