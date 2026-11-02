package pi_subagents

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestHerdrPanesSetting(t *testing.T) {
	home := withTempHome(t)
	cwd := tmp(t)
	userSettings := filepath.Join(home, ".pi", "agent", "settings.json")
	projectSettings := filepath.Join(cwd, ".pi", "settings.json")

	d, err := subagentSettingsFor(cwd, ScopeBoth)
	eq(t, err, nil, "error")
	eq(t, d.herdrPanes == nil, true, "unset by default")

	writeFile(t, userSettings, `{"subagents": {"herdrPanes": true}}`)
	d, _ = subagentSettingsFor(cwd, ScopeBoth)
	eq(t, d.herdrPanes != nil && *d.herdrPanes, true, "the user's")

	writeFile(t, projectSettings, `{"subagents": {"herdrPanes": false}}`)
	d, _ = subagentSettingsFor(cwd, ScopeBoth)
	eq(t, d.herdrPanes != nil && !*d.herdrPanes, true, "the project's wins")
	d, _ = subagentSettingsFor(cwd, ScopeUser)
	eq(t, d.herdrPanes != nil && *d.herdrPanes, true, "scope user reads no project settings")

	writeFile(t, projectSettings, `{"subagents": {"herdrPanes": "yes"}}`)
	_, err = subagentSettingsFor(cwd, ScopeBoth)
	hasMatch(t, err, "Subagent settings in '"+projectSettings+"' have invalid 'herdrPanes'; expected true or false.")
}

func TestPanesEnabled(t *testing.T) {
	yes, no := true, false
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	both := map[string]string{"HC_TERMINAL_SOCKET": "/s", "HC_TERMINAL_ID": "t1"}
	for _, c := range []struct {
		name string
		set  *bool
		env  map[string]string
		goos string
		want bool
	}{
		{"darwin", &yes, both, "darwin", true},
		{"linux", &yes, both, "linux", true},
		{"unset", nil, both, "darwin", false},
		{"false", &no, both, "darwin", false},
		{"no socket", &yes, map[string]string{"HC_TERMINAL_ID": "t1"}, "darwin", false},
		{"no id", &yes, map[string]string{"HC_TERMINAL_SOCKET": "/s"}, "darwin", false},
		{"windows", &yes, both, "windows", false},
	} {
		eq(t, panesEnabled(subagentSettings{herdrPanes: c.set}, env(c.env), c.goos), c.want, c.name)
	}
}

func TestPaneArgs(t *testing.T) {
	eq(t, paneArgs([]string{"--print", "--model", "p/m", "--print-x"}, "/d", "id", "Task: t"),
		[]string{"--model", "p/m", "--print-x", "--session-dir", "/d", "--session-id", "id", "--", "Task: t"}, "args")
}

func TestPaneEnv(t *testing.T) {
	eq(t, paneEnv([]string{"A=1", "HC_TERMINAL_ID=t1", "HC_SCOPE=x", "PIG_SUBAGENT=1", "B=x=y"}),
		map[string]string{"A": "1", "PIG_SUBAGENT": "1", "B": "x=y"}, "env")
}

func TestPaneName(t *testing.T) {
	eq(t, paneName("scout", "ab12"), "scout-ab12", "plain")
	eq(t, paneName("My.Agent/X", "ab12"), "my.agent-x-ab12", "sanitized")
	long := paneName(strings.Repeat("a", 40), "ab12")
	eq(t, len(long), 32, "long length")
	eq(t, strings.HasSuffix(long, "-ab12"), true, "long suffix")
	eq(t, paneName("", "ab12"), "agent-ab12", "empty")
}

func TestSessionAnswer(t *testing.T) {
	asst := func(content, extra string) string {
		return `{"type":"message","message":{"role":"assistant","content":[` + content + `]` + extra + `}}` + "\n"
	}
	head := `{"type":"session","id":"id"}` + "\n" +
		`{"type":"message","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}` + "\n" +
		asst(`{"type":"toolCall","name":"x"}`, `,"stopReason":"toolUse"`) +
		`{"type":"message","message":{"role":"toolResult","content":[{"type":"text","text":"` + strings.Repeat("x", 2<<20) + `"}]}}` + "\n"
	answer := func(last string) (childResult, bool) {
		dir := tmp(t)
		writeFile(t, filepath.Join(dir, "1_id.jsonl"), head+last)
		return sessionAnswer(dir, "id")
	}
	r, ok := answer(asst(`{"type":"thinking","thinking":"z"},{"type":"text","text":"a"},{"type":"text","text":"b"}`, `,"stopReason":"stop"`))
	eq(t, ok, true, "ok")
	eq(t, r, childResult{Stdout: "a\nb\n"}, "stop")
	r, _ = answer(asst(``, `,"stopReason":"error","errorMessage":"boom"`))
	eq(t, r, childResult{Exit: 1, Stderr: "boom"}, "error")
	r, _ = answer(asst(``, `,"stopReason":"aborted"`))
	eq(t, r, childResult{Exit: 1, Stderr: "Request aborted"}, "aborted")
	r, _ = answer(asst(`{"type":"text","text":"part"}`, `,"stopReason":"length"`))
	eq(t, r, childResult{Stdout: "part\n", Exit: 1, Stderr: "Response was truncated: the model reached its output token limit."}, "length")

	_, ok = sessionAnswer(tmp(t), "id")
	eq(t, ok, false, "no file")
	dir := tmp(t)
	writeFile(t, filepath.Join(dir, "1_id.jsonl"), `{"type":"session"}`+"\n")
	_, ok = sessionAnswer(dir, "id")
	eq(t, ok, false, "no assistant message")
}
