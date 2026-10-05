package powerline_footer

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The render golden (port/render/golden.json) is the output of the ORIGINAL extension (port/drive/drive.mjs) for each scripted
// state in port/render/states.json. This test replays the same states through the Go core and compares the notifications it
// raises, the settings it persists, which widgets it installs, and every line it renders at each width.

func (o *jsObject) str(k string) string {
	v, _ := o.vals[k].(string)
	return v
}

func (o *jsObject) obj(k string) *jsObject {
	v, _ := o.vals[k].(*jsObject)
	return v
}

func (o *jsObject) num(k string) float64 {
	v, _ := o.vals[k].(float64)
	return v
}

func (o *jsObject) list(k string) []any {
	v, _ := o.vals[k].([]any)
	return v
}

func (o *jsObject) has(k string) bool {
	if o == nil {
		return false
	}
	_, ok := o.vals[k]
	return ok
}

func readFixture(t *testing.T, name string) any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "render-"+name))
	if err != nil {
		t.Fatal(err)
	}
	v, err := parseJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// scriptedTheme reproduces the driver's theme: a token the real Pi theme knows becomes SGR 38;5;(17+index), others fail.
type scriptedTheme struct{ tokens []string }

func (s scriptedTheme) Fg(token, text string) (string, error) {
	for i, k := range s.tokens {
		if k == token {
			return fmt.Sprintf("\x1b[38;5;%dm%s\x1b[39m", 17+i, text), nil
		}
	}
	return "", fmt.Errorf("Unknown theme color: %s", token)
}

type scriptedHost struct {
	st       *jsObject
	cwd      string
	model    *modelInfo
	thinking string
	statuses []statusEntry
	notes    [][2]string
	theme    theme
	branch   *fakeBranch
}

func (h *scriptedHost) Cwd() string { return h.cwd }
func (h *scriptedHost) HasUI() bool { return h.st.vals["hasUI"] != false }
func (h *scriptedHost) Mode() string {
	if m := h.st.str("mode"); m != "" {
		return m
	}
	return "tui"
}
func (h *scriptedHost) Model() *modelInfo     { return h.model }
func (h *scriptedHost) ThinkingLevel() string { return h.thinking }
func (h *scriptedHost) SessionID() string     { return h.st.str("sessionId") }
func (h *scriptedHost) SessionName() string   { return h.st.str("sessionName") }
func (h *scriptedHost) UsingOAuth() bool      { b, _ := h.st.vals["usingOAuth"].(bool); return b }
func (h *scriptedHost) AutoCompactEnabled() bool {
	if b, ok := h.st.vals["autoCompact"].(bool); ok {
		return b
	}
	return true
}
func (h *scriptedHost) Session() branchProvider { return h.branch }
func (h *scriptedHost) ContextUsage() map[string]any {
	if o, ok := h.st.vals["contextUsage"].(*jsObject); ok {
		return plain(o).(map[string]any)
	}
	return nil
}
func (h *scriptedHost) ExtensionStatuses() []statusEntry { return h.statuses }
func (h *scriptedHost) ProviderGitBranch() *string {
	if b, ok := h.st.vals["gitBranch"].(string); ok {
		return &b
	}
	return nil
}
func (h *scriptedHost) Theme() theme { return h.theme }
func (h *scriptedHost) Notify(message, level string) {
	h.notes = append(h.notes, [2]string{level, message})
}
func (h *scriptedHost) SetStatus(key string, text *string)                {}
func (h *scriptedHost) ClearWidget(key string)                            {}
func (h *scriptedHost) SetFooterInstalled(on bool)                        {}
func (h *scriptedHost) SetWidgetInstalled(key, placement string, on bool) {}
func (h *scriptedHost) Repaint()                                          {}

// plain turns decoded JSON (ordered objects) into the maps and slices the host's session API returns.
func plain(v any) any {
	switch x := v.(type) {
	case *jsObject:
		m := map[string]any{}
		for _, k := range x.keys {
			m[k] = plain(x.vals[k])
		}
		return m
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = plain(e)
		}
		return out
	}
	return v
}

func modelFrom(o *jsObject) *modelInfo {
	if o == nil {
		return nil
	}
	m := &modelInfo{ID: o.str("id"), Name: o.str("name"), Provider: o.str("provider"), ProviderID: o.str("providerId"), ProviderName: o.str("providerName"), ContextWindow: o.num("contextWindow")}
	m.Reasoning, _ = o.vals["reasoning"].(bool)
	return m
}

func TestRenderGolden(t *testing.T) {
	golden, ok := readFixture(t, "golden.json").(*jsObject)
	if !ok {
		t.Fatal("the golden did not decode to an object")
	}
	meta := golden.obj("_meta")
	var tokens []string
	for _, k := range meta.list("themeTokens") {
		tokens = append(tokens, k.(string))
	}
	states, ok := readFixture(t, "states.json").([]any)
	if !ok {
		t.Fatal("the states did not decode to a list")
	}
	oldLocal := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = oldLocal })
	for _, raw := range states {
		st := raw.(*jsObject)
		name := st.str("name")
		want := golden.obj(name)
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			home := filepath.Join(root, "home")
			agent := filepath.Join(home, "agent")
			os.MkdirAll(agent, 0o755)
			switch raw := st.vals["rawSettings"].(type) {
			case string:
				os.WriteFile(filepath.Join(agent, "settings.json"), []byte(raw), 0o644)
			default:
				if settings := st.vals["settings"]; settings != nil {
					os.WriteFile(filepath.Join(agent, "settings.json"), []byte(marshalJSON(settings, "")), 0o644)
				} else {
					os.WriteFile(filepath.Join(agent, "settings.json"), []byte("{}"), 0o644)
				}
			}
			if tj := st.obj("themeJson"); tj != nil {
				dir := filepath.Join(agent, "extensions", "powerline-footer")
				os.MkdirAll(dir, 0o755)
				os.WriteFile(filepath.Join(dir, "theme.json"), []byte(marshalJSON(tj, "")), 0o644)
			}
			cwd := st.str("cwd")
			if cwd == "" {
				cwd = "{HOME}/work/proj"
			}
			cwd = strings.Replace(cwd, "{HOME}", home, 1)
			os.MkdirAll(cwd, 0o755)
			if ps := st.vals["projectSettings"]; ps != nil {
				os.MkdirAll(filepath.Join(cwd, ".pi"), 0o755)
				os.WriteFile(filepath.Join(cwd, ".pi", "settings.json"), []byte(marshalJSON(ps, "")), 0o644)
			}
			if r := st.obj("repo"); r != nil {
				makeRepo(t, cwd, r)
			}
			resetCurrencyRatesForTest()
			t.Cleanup(resetCurrencyRatesForTest)
			invalidateGitStatus()
			invalidateGitBranch()
			if cr := st.obj("currencyRates"); cr != nil {
				os.MkdirAll(filepath.Join(agent, "powerline-footer"), 0o755)
				os.WriteFile(filepath.Join(agent, "powerline-footer", "currency-rates.json"), []byte(marshalJSON(map[string]any{"timestamp": float64(startTimeOf(st).UnixMilli()), "rates": cr}, "")), 0o644)
			}
			for _, k := range []string{"POWERLINE_NERD_FONTS", "TERM_PROGRAM", "TERM", "GHOSTTY_RESOURCES_DIR"} {
				setenv(t, k, nil)
			}
			setenv(t, "HOME", &home)
			setenv(t, "USERPROFILE", nil)
			setenv(t, "PI_CODING_AGENT_DIR", &agent)
			setenv(t, "POWERLINE_NERD_FONTS", s("0"))
			if env := st.obj("env"); env != nil {
				for _, k := range env.keys {
					setenv(t, k, s(env.str(k)))
				}
			}
			nowMs := startTimeOf(st).UnixMilli()
			oldClock, oldHost := clock, hostnameFn
			clock = func() int64 { return nowMs }
			hostnameFn = func() string { return "builder.local" }
			t.Cleanup(func() { clock, hostnameFn = oldClock, oldHost })

			var entries []map[string]any
			for i, e := range st.list("branch") {
				m := plain(e).(map[string]any)
				m["id"] = fmt.Sprintf("e%d", i)
				entries = append(entries, m)
			}
			leaf := ""
			if len(entries) > 0 {
				leaf = entries[len(entries)-1]["id"].(string)
			}
			h := &scriptedHost{st: st, cwd: cwd, model: modelFrom(st.obj("model")), thinking: "off", theme: scriptedTheme{tokens},
				branch: &fakeBranch{leaf: leaf, branch: entries}}
			if v := st.str("thinkingLevel"); v != "" {
				h.thinking = v
			}
			if so := st.obj("statuses"); so != nil {
				for _, k := range so.keys {
					h.statuses = append(h.statuses, statusEntry{k, so.str(k)})
				}
			}
			p := newPowerline(h)
			reason := st.str("reason")
			if reason == "" {
				reason = "startup"
			}
			p.sessionStart(reason)
			for _, step := range st.list("steps") {
				so := step.(*jsObject)
				if c := so.str("command"); c != "" {
					p.command(so.str("args"))
				}
				if ev := so.str("event"); ev != "" {
					data := so.obj("data")
					if ev == "model_select" {
						h.model = modelFrom(data.obj("model"))
					}
					if ev == "thinking_level_select" {
						h.thinking = data.str("level")
					}
					var dm map[string]any
					if data != nil {
						dm = plain(data).(map[string]any)
					}
					p.event(ev, dm)
				}
				if so.has("advanceMs") {
					nowMs += int64(so.num("advanceMs"))
				}
				if status := so.obj("status"); status != nil {
					for _, k := range status.keys {
						h.setStatus(k, status.vals[k])
					}
				}
			}
			nowMs += int64(st.num("elapsedMs"))

			// notifications
			var gotNotes []any
			for _, n := range h.notes {
				gotNotes = append(gotNotes, []any{n[0], n[1]})
			}
			wantNotes := plain(want.vals["notes"])
			if wantNotes == nil {
				wantNotes = []any(nil)
			}
			if len(gotNotes) == 0 && len(wantNotes.([]any)) == 0 {
				gotNotes = nil
				wantNotes = []any(nil)
			}
			eq(t, gotNotes, wantNotes, "notifications")
			// persisted settings
			var after any
			if data, err := os.ReadFile(filepath.Join(agent, "settings.json")); err == nil {
				json.Unmarshal(data, &after)
			}
			eq(t, after, plain(want.vals["settingsAfter"]), "settings after")
			var rawAfter any
			if data, err := os.ReadFile(filepath.Join(agent, "settings.json")); err == nil {
				rawAfter = string(data)
			}
			eq(t, rawAfter, want.vals["settingsRaw"], "settings text after")
			// installed UI
			ui := p.installed()
			wantWidths := want.obj("widths")
			first := wantWidths.obj(wantWidths.keys[0])
			eq(t, ui.Footer, first.has("footer"), "footer installed")
			for _, key := range []string{"powerline-top", "powerline-status"} {
				_, got := ui.Widgets[key]
				eq(t, got, first.has(key), key+" installed")
			}
			if want.obj("placement").has("powerline-top") && first.has("powerline-top") {
				eq(t, ui.Widgets["powerline-top"], want.obj("placement").str("powerline-top"), "primary placement")
			}
			for _, wk := range wantWidths.keys {
				var width int
				fmt.Sscanf(wk, "%d", &width)
				row := wantWidths.obj(wk)
				check := func(key string, got []string) {
					if !row.has(key) {
						return
					}
					wantLines := []string{}
					for _, l := range row.list(key) {
						wantLines = append(wantLines, l.(string))
					}
					eq(t, got, wantLines, fmt.Sprintf("%s at width %d", key, width))
				}
				check("powerline-top", p.renderTop(width))
				check("footer", p.renderFooter(width))
				check("footer", p.footerRows(width)) // what the SDK's footer renderer draws
				check("powerline-status", p.renderStatus(width))
			}
		})
	}
}

func (h *scriptedHost) setStatus(key string, v any) {
	for i, e := range h.statuses {
		if e.Key == key {
			if v == nil {
				h.statuses = append(h.statuses[:i], h.statuses[i+1:]...)
			} else {
				h.statuses[i].Value, _ = v.(string)
			}
			return
		}
	}
	if str, ok := v.(string); ok {
		h.statuses = append(h.statuses, statusEntry{key, str})
	}
}

var _ = reflect.DeepEqual

func startTimeOf(st *jsObject) time.Time {
	if v := st.str("start"); v != "" {
		t, _ := time.Parse(time.RFC3339, v)
		return t
	}
	return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
}

// makeRepo builds the repository the driver builds for a state: a base commit, then unstaged and staged edits and untracked files.
func makeRepo(t *testing.T, cwd string, r *jsObject) {
	t.Helper()
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = cwd
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
			"GIT_AUTHOR_DATE=2026-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	write := func(name, text string) { os.WriteFile(filepath.Join(cwd, name), []byte(text), 0o644) }
	branch := r.str("branch")
	if branch == "" {
		branch = "main"
	}
	staged, unstaged, untracked := int(r.num("staged")), int(r.num("unstaged")), int(r.num("untracked"))
	git("init", "-q", "-b", branch)
	if remote := r.str("remote"); remote != "" {
		git("remote", "add", "origin", remote)
	}
	total := staged + unstaged
	if total < 1 {
		total = 1
	}
	for i := 0; i < total; i++ {
		write(fmt.Sprintf("b%d", i), "base")
	}
	git("add", ".")
	git("commit", "-q", "-m", "base")
	for i := 0; i < unstaged; i++ {
		write(fmt.Sprintf("b%d", i), "changed")
	}
	for i := 0; i < staged; i++ {
		write(fmt.Sprintf("b%d", unstaged+i), "staged")
		git("add", fmt.Sprintf("b%d", unstaged+i))
	}
	for i := 0; i < untracked; i++ {
		write(fmt.Sprintf("u%d", i), "new")
	}
	if r.vals["detached"] == true {
		git("checkout", "-q", "--detach")
	}
}

func TestMain(m *testing.M) {
	fetchRates = nil // no test may reach the network
	os.Exit(m.Run())
}
