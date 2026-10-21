package powerline_footer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Twins of tests/stash-shortcut.test.ts, and the fakes they share. The original builds a fake pi and a fake ctx; the port's
// seams are keysHost (the editor, the status, the pickers, the clipboard) and hostAPI (the bar), both scripted here.

type keysPlainTheme struct{}

func (keysPlainTheme) Fg(_, text string) string { return text }
func (keysPlainTheme) Bold(text string) string  { return text }

type statusCall struct {
	key  string
	text *string
}

type noteCall struct{ message, level string }

// fakeKeys is the fake ctx: an editor text, recorded calls and scripted pickers. overlayInputs scripts the keys typed into each
// overlay in turn (default: Enter), as customInputs does in the original.
type fakeKeys struct {
	cwd, session  string
	text          string
	setTextCalls  []string
	statuses      []statusCall
	notes         []noteCall
	overlayInputs [][]string
	titles        []string
	items         [][]selectItem
	selectAnswer  func(title string, options []string) string
	clipboard     []string
	clipboardErr  error
	hooks         map[string]func()
}

func (f *fakeKeys) Cwd() string        { return f.cwd }
func (f *fakeKeys) SessionID() string  { return f.session }
func (f *fakeKeys) EditorText() string { return f.text }
func (f *fakeKeys) SetEditorText(text string) {
	f.setTextCalls = append(f.setTextCalls, text)
	f.text = text
}
func (f *fakeKeys) SetStatus(key string, text *string) {
	f.statuses = append(f.statuses, statusCall{key, text})
}
func (f *fakeKeys) Notify(message, level string) { f.notes = append(f.notes, noteCall{message, level}) }
func (f *fakeKeys) Select(title string, options []string) string {
	if f.selectAnswer != nil {
		return f.selectAnswer(title, options)
	}
	return "Insert"
}
func (f *fakeKeys) SelectOverlay(title, hint string, items []selectItem, maxVisible int) *selectItem {
	f.titles = append(f.titles, title)
	f.items = append(f.items, items)
	component := newSelectOverlay(keysPlainTheme{}, title, hint, items, maxVisible)
	component.Render(80)
	inputs := []string{"\r"}
	if len(f.overlayInputs) > 0 {
		inputs, f.overlayInputs = f.overlayInputs[0], f.overlayInputs[1:]
	}
	for _, in := range inputs {
		res, _ := component.HandleInput(in)
		if res.Done {
			if m, ok := res.Value.(map[string]any); ok {
				return &selectItem{Value: m["value"].(string), Label: m["label"].(string), Description: m["description"].(string)}
			}
			return nil
		}
	}
	return nil
}
func (f *fakeKeys) CopyToClipboard(text string) error {
	if f.clipboardErr != nil {
		return f.clipboardErr
	}
	f.clipboard = append(f.clipboard, text)
	return nil
}
func (f *fakeKeys) Hook(action string) func() { return f.hooks[action] }

func (f *fakeKeys) lastStatus(t *testing.T) statusCall {
	t.Helper()
	if len(f.statuses) == 0 {
		t.Fatal("no status was set")
	}
	return f.statuses[len(f.statuses)-1]
}

func (f *fakeKeys) wantStatus(t *testing.T, key, text string) {
	t.Helper()
	got := f.lastStatus(t)
	if got.key != key || (text == "" && got.text != nil) || (text != "" && (got.text == nil || *got.text != text)) {
		g := "undefined"
		if got.text != nil {
			g = *got.text
		}
		t.Errorf("last status = [%s, %s], want [%s, %q]", got.key, g, key, text)
	}
}

func (f *fakeKeys) hasNote(message string) bool {
	for _, n := range f.notes {
		if n.message == message {
			return true
		}
	}
	return false
}

func (f *fakeKeys) lastNote() string {
	if len(f.notes) == 0 {
		return ""
	}
	return f.notes[len(f.notes)-1].message
}

// recHost is the bar's host for the twins that start the core: the scripted host of the render tests plus what was installed.
type recHost struct {
	*scriptedHost
	pl      *powerline
	footer  bool
	widgets map[string]string
}

func (h *recHost) SetFooterInstalled(on bool) { h.footer = on }
func (h *recHost) SetWidgetInstalled(key, placement string, on bool) {
	if on {
		h.widgets[key] = placement
	} else {
		delete(h.widgets, key)
	}
}
func (h *recHost) ClearWidget(key string)    { delete(h.widgets, key) }
func (h *recHost) Repaint()                  { h.pl.refresh() }
func (h *recHost) SetStatus(string, *string) {}

func newRecHost(t *testing.T, statuses []statusEntry, state string) *recHost {
	t.Helper()
	st, _ := parseJSON([]byte(state))
	h := &recHost{
		scriptedHost: &scriptedHost{st: st.(*jsObject), cwd: t.TempDir(), model: &modelInfo{ID: "test", Name: "test", Provider: "test"},
			thinking: "off", theme: scriptedTheme{piThemeTokens}, statuses: statuses, branch: &fakeBranch{}},
		widgets: map[string]string{},
	}
	h.pl = newPowerline(h)
	return h
}

// testKeys is the keys slice the core talks to in the twins that start the core: keysState over a fakeKeys.
type testKeys struct {
	state *keysState
	f     *fakeKeys
}

func (k testKeys) sessionStart(reason string) { k.state.sessionStart(k.f, reason, true) }
func (k testKeys) setup()                     {}
func (k testKeys) disable()                   { k.state.disable(k.f) }

func writeAgentSettings(t *testing.T, agentDir string) {
	t.Helper()
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"powerline":{"welcome":false}}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeStashHistory(t *testing.T, agentDir string, history []string) {
	t.Helper()
	dir := filepath.Join(agentDir, "powerline-footer")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	items := make([]any, len(history))
	for i, h := range history {
		items[i] = h
	}
	o := newObject()
	o.set("version", float64(1))
	o.set("history", items)
	if err := os.WriteFile(filepath.Join(dir, "stash-history.json"), []byte(marshalJSON(o, "")), 0o644); err != nil {
		t.Fatal(err)
	}
}

// generation is one extension generation of a session (createStashGeneration): its own core, keys state and fake ctx.
type generation struct {
	t  *testing.T
	f  *fakeKeys
	k  *keysState
	pl *powerline
	h  *recHost
}

func newGeneration(t *testing.T, cwd, sessionID, text string) *generation {
	t.Helper()
	h := newRecHost(t, nil, fmt.Sprintf(`{"sessionId":%q}`, sessionID))
	h.cwd = cwd
	f := &fakeKeys{cwd: cwd, session: sessionID, text: text}
	k := newKeysState(readSettings(cwd), false)
	h.pl.keys = testKeys{k, f}
	return &generation{t, f, k, h.pl, h}
}

func (g *generation) start(reason string) { g.pl.sessionStart(reason) }
func (g *generation) shutdown(reason string) {
	g.k.sessionShutdown(g.f, reason)
}
func (g *generation) togglePowerline() { g.pl.command("") }

// sendTerminalInput is the listener of setupCustomEditor for the stash shortcut.
func (g *generation) sendTerminalInput(data string) {
	if g.k.isStashShortcutInput(data) {
		g.k.stashOrRestoreEditorText(g.f)
	}
}

func agentRig(t *testing.T) (agentDir, cwd string) {
	t.Helper()
	agentDir = t.TempDir()
	cwd = t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	writeAgentSettings(t, agentDir)
	return agentDir, cwd
}

func sessionLine(text string, timestamp int64) string {
	msg := newObject()
	msg.set("role", "user")
	msg.set("content", text)
	msg.set("timestamp", float64(timestamp))
	o := newObject()
	o.set("type", "message")
	o.set("message", msg)
	o.set("timestamp", time.UnixMilli(timestamp).UTC().Format("2006-01-02T15:04:05.000Z"))
	return marshalJSON(o, "")
}

func TestStashShortcutTwins(t *testing.T) {
	const file = "stash-shortcut"

	tw(t, file, "responsive secondary content is owned exclusively by the footer", func(t *testing.T) {
		root := t.TempDir()
		t.Setenv("PI_CODING_AGENT_DIR", root)
		os.WriteFile(filepath.Join(root, "settings.json"), []byte(`{"powerline":{"welcome":false,"placement":"below","layout":{"left":["model"],"right":[],"secondary":["custom:review"]},"customItems":[{"id":"review","statusKey":"review","position":"secondary","prefix":"review"}]}}`), 0o644)
		h := newRecHost(t, []statusEntry{{"review", "ready"}}, `{}`)
		h.cwd = root
		join := func(lines []string) string { return strings.Join(lines, "\n") }
		review := regexp.MustCompile(`review.*ready`)
		stripAnsi := regexp.MustCompile(`\x1b\[[0-9;]*m`)
		plain := func(lines []string) string { return stripAnsi.ReplaceAllString(join(lines), "") }

		h.pl.sessionStart("resume")
		if _, has := h.widgets["powerline-secondary"]; has {
			t.Error("no secondary widget")
		}
		if h.widgets["powerline-top"] != "belowEditor" {
			t.Errorf("placement = %q", h.widgets["powerline-top"])
		}
		if !h.footer {
			t.Fatal("custom footer is installed")
		}

		narrowPrimary := plain(h.pl.renderTop(16))
		narrowFooter := plain(h.pl.footerRows(16))
		if !strings.Contains(narrowPrimary, "test") || strings.Contains(narrowPrimary, "ready") {
			t.Errorf("narrow primary %q", narrowPrimary)
		}
		if !review.MatchString(narrowFooter) || strings.Contains(narrowFooter, "test") {
			t.Errorf("narrow footer %q", narrowFooter)
		}

		widePrimary := plain(h.pl.renderTop(200))
		if rows := h.pl.footerRows(200); len(rows) != 0 {
			t.Errorf("footer returns no fabricated blank line without overflow, got %q", rows)
		}
		if n := len(review.FindAllString(widePrimary, -1)); n != 1 {
			t.Errorf("wide primary %q has %d review statuses, want 1", widePrimary, n)
		}

		h.pl.command("placement above")
		if h.widgets["powerline-top"] != "aboveEditor" {
			t.Errorf("placement after the command = %q", h.widgets["powerline-top"])
		}
		if _, has := h.widgets["powerline-secondary"]; has {
			t.Error("no secondary widget after placement")
		}
		if !review.MatchString(plain(h.pl.footerRows(16))) {
			t.Error("footer keeps the secondary content after placement")
		}

		h.pl.command("")
		if h.footer {
			t.Error("disable restores Pi's footer")
		}
		if len(h.widgets) != 0 {
			t.Errorf("disable leaves widgets %v", h.widgets)
		}
		h.pl.command("")
		if !h.footer {
			t.Error("re-enable reinstalls the custom footer")
		}
		if _, has := h.widgets["powerline-secondary"]; has {
			t.Error("no secondary widget after re-enable")
		}
		if !review.MatchString(plain(h.pl.footerRows(16))) {
			t.Error("footer shows the secondary content after re-enable")
		}
	})

	tw(t, file, "web frontends outside the TUI receive only the primary bar widget", func(t *testing.T) {
		root := t.TempDir()
		t.Setenv("PI_CODING_AGENT_DIR", root)
		os.WriteFile(filepath.Join(root, "settings.json"), []byte(`{"powerline":{"welcome":false,"layout":{"left":["model"],"right":[],"secondary":[]}}}`), 0o644)
		h := newRecHost(t, nil, `{"mode":"rpc"}`)
		h.cwd = root
		h.pl.sessionStart("resume")
		if len(h.widgets) != 1 || h.widgets["powerline-top"] == "" {
			t.Errorf("widgets = %v, want only powerline-top", h.widgets)
		}
		if got := strings.Join(h.pl.renderTop(120), "\n"); !strings.Contains(got, "test") {
			t.Errorf("primary bar %q lacks the model", got)
		}
	})

	tskip(t, file, "Git rendering reuses the cwd-owned provider only on demand and refreshes across sessions",
		"asserts on Pi's ReadonlyFooterDataProvider (getGitBranch must not be read, onBranchChange subscribe/unsubscribe) and spies on node:child_process spawn; the Go SDK exposes no footer data provider (G11) and the port's git status is covered by its own twins")
	tskip(t, file, "footer queue demand follows resolved layout while preview and picker stay independent",
		"stubs node:fs readFileSync to make the queue inbox unreadable and checks which renderers throw; it exercises the queue preview widget and picker, owned by the queue slice, and a Go port reads the inbox through its own store, not fs.readFileSync")

	tw(t, file, "stash shortcut matches Alt+S encodings without consuming literal sharp-S by default", func(t *testing.T) {
		if matchesStashShortcutInput("ß", false) {
			t.Error(`"ß" must not match by default`)
		}
		if !matchesStashShortcutInput("ß", true) {
			t.Error(`"ß" must match with includePrintableSharpS`)
		}
		for _, data := range []string{"\x1bs", "\x1bS", "\x1b[115;3u", "\x1b[83;3u", "\x1b[27;3;115~", "\x1b[27;3;83~"} {
			if !matchesStashShortcutInput(data, false) {
				t.Errorf("%q must match", data)
			}
		}
		if matchesStashShortcutInput("s", false) {
			t.Error(`"s" must not match`)
		}
		if matchesStashShortcutInput("\x1b[115;5u", false) {
			t.Error(`"\x1b[115;5u" (ctrl+s) must not match`)
		}
	})

	tw(t, file, "stash shortcut stays in terminal/editor fallback routing", func(t *testing.T) {
		read := func(name string) string {
			b, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			return string(b)
		}
		ext, core, short := read("keys_ext.go"), read("keys.go"), read("shortcuts.go")
		if regexp.MustCompile(`\.Shortcut\(\s*"alt\+s"`).MatchString(ext + core) {
			t.Error(`alt+s must not be registered with Shortcut`)
		}
		for _, want := range []struct{ src, pattern string }{
			{core, `matchesStashShortcutInput\(data, k\.stashSharpS\)`},
			{ext, `ctx\.OnTerminalInput\(k\.onInput\(`},
			{ext, `state\.isStashShortcutInput\(data\)`},
			{core, `func \(k \*keysState\) stashOrRestoreEditorText\(h keysHost\)`},
			{core, `func \(k \*keysState\) isPromptHistoryShortcutInput\(data string\) bool`},
			{core, `matchesConfiguredShortcut\(data, k\.shortcuts\.StashHistory\)`},
		} {
			if !regexp.MustCompile(want.pattern).MatchString(want.src) {
				t.Errorf("source lacks %s", want.pattern)
			}
		}
		if strings.Contains(short, `data == "\x1b\b"`) || strings.Contains(short, `data == "\x1b\x7f"`) {
			t.Error("no raw backspace sequences in the stash matcher")
		}
	})

	tw(t, file, "agent_end leaves an active stash untouched until explicit restore", func(t *testing.T) {
		_, cwd := agentRig(t)
		g := newGeneration(t, cwd, "agent-end-session", "draft to keep")
		g.start("resume")

		g.sendTerminalInput("\x1bs")
		if g.f.text != "" {
			t.Fatalf("text = %q", g.f.text)
		}
		g.f.wantStatus(t, "stash", "stash")

		g.f.setTextCalls = nil
		g.pl.event("agent_end", map[string]any{})
		if len(g.f.setTextCalls) != 0 || g.f.text != "" {
			t.Errorf("agent_end touched the editor: %q %q", g.f.setTextCalls, g.f.text)
		}
		g.f.wantStatus(t, "stash", "stash")
		if g.f.hasNote("Stash restored") {
			t.Error("agent_end restored the stash")
		}

		g.sendTerminalInput("\x1bs")
		if g.f.text != "draft to keep" {
			t.Errorf("text = %q", g.f.text)
		}
		g.f.wantStatus(t, "stash", "")
		if !g.f.hasNote("Stash restored") {
			t.Error("no restore note")
		}
	})

	tw(t, file, "reload transfers an active stash to the next extension generation", func(t *testing.T) {
		_, cwd := agentRig(t)
		sessionID := fmt.Sprintf("reload-%d", time.Now().UnixNano())
		oldG := newGeneration(t, cwd, sessionID, "draft across reload")
		newG := newGeneration(t, cwd, sessionID, "")

		oldG.start("startup")
		oldG.sendTerminalInput("\x1bs")
		oldG.shutdown("reload")
		newG.start("reload")

		newG.f.wantStatus(t, "stash", "stash")
		newG.sendTerminalInput("\x1bs")
		if newG.f.text != "draft across reload" {
			t.Errorf("text = %q", newG.f.text)
		}
		newG.f.wantStatus(t, "stash", "")
	})

	tw(t, file, "reload stash handoff does not leak to a different session", func(t *testing.T) {
		_, cwd := agentRig(t)
		sourceID := fmt.Sprintf("source-%d", time.Now().UnixNano())
		source := newGeneration(t, cwd, sourceID, "private draft")
		other := newGeneration(t, cwd, "other-"+sourceID, "")
		receiver := newGeneration(t, cwd, sourceID, "")

		source.start("startup")
		source.sendTerminalInput("\x1bs")
		source.shutdown("reload")
		other.start("reload")

		other.f.wantStatus(t, "stash", "")
		other.sendTerminalInput("\x1bs")
		if other.f.lastNote() != "Nothing to stash" {
			t.Errorf("last note = %q", other.f.lastNote())
		}
		receiver.start("reload")
		receiver.sendTerminalInput("\x1bs")
		if receiver.f.text != "private draft" {
			t.Errorf("text = %q", receiver.f.text)
		}
	})

	tw(t, file, "quit and Powerline disable clear active stash reload state", func(t *testing.T) {
		_, cwd := agentRig(t)
		sessionID := fmt.Sprintf("quit-%d", time.Now().UnixNano())
		oldG := newGeneration(t, cwd, sessionID, "discard on quit")
		newG := newGeneration(t, cwd, sessionID, "")
		disabled := newGeneration(t, cwd, sessionID+"-disabled", "discard on disable")
		afterDisable := newGeneration(t, cwd, sessionID+"-disabled", "")

		oldG.start("startup")
		oldG.sendTerminalInput("\x1bs")
		oldG.shutdown("quit")
		newG.start("reload")
		newG.sendTerminalInput("\x1bs")
		if newG.f.lastNote() != "Nothing to stash" {
			t.Errorf("after quit: last note = %q", newG.f.lastNote())
		}

		disabled.start("startup")
		disabled.sendTerminalInput("\x1bs")
		disabled.togglePowerline()
		disabled.shutdown("reload")
		afterDisable.start("reload")
		afterDisable.sendTerminalInput("\x1bs")
		if afterDisable.f.lastNote() != "Nothing to stash" {
			t.Errorf("after disable: last note = %q", afterDisable.f.lastNote())
		}
	})

	tw(t, file, "reload stash handoff is consumed only once", func(t *testing.T) {
		_, cwd := agentRig(t)
		sessionID := fmt.Sprintf("one-shot-%d", time.Now().UnixNano())
		oldG := newGeneration(t, cwd, sessionID, "consume once")
		receiver := newGeneration(t, cwd, sessionID, "")
		extra := newGeneration(t, cwd, sessionID, "")

		oldG.start("startup")
		oldG.sendTerminalInput("\x1bs")
		oldG.shutdown("reload")
		receiver.start("reload")
		extra.start("reload")

		extra.sendTerminalInput("\x1bs")
		if extra.f.lastNote() != "Nothing to stash" {
			t.Errorf("extra: last note = %q", extra.f.lastNote())
		}
		receiver.sendTerminalInput("\x1bs")
		if receiver.f.text != "consume once" {
			t.Errorf("receiver text = %q", receiver.f.text)
		}
	})

	tw(t, file, "stash history can open stashed prompts without reading project JSONL", func(t *testing.T) {
		agentDir, _ := agentRig(t)
		cwd := "/tmp/powerline-stash-history-cwd"
		writeStashHistory(t, agentDir, []string{"saved stash"})
		sessions := getProjectSessionsPath(cwd)
		if err := os.MkdirAll(sessions, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(sessions) })
		os.WriteFile(filepath.Join(sessions, "broken.jsonl"), []byte(`{"type":"message","message":{"role":"user",`), 0o644)

		f := &fakeKeys{cwd: cwd, session: "s", overlayInputs: [][]string{{"\r"}, {"\r"}}}
		newKeysState(map[string]any{}, false).openStashHistory(f)

		if got := strings.Join(f.titles, "|"); got != "Prompt history|Stash history" {
			t.Errorf("titles = %q", got)
		}
		if got := f.items[0]; len(got) != 2 || got[0] != (selectItem{"stash", "Stashed prompts", "1 saved"}) || got[1] != (selectItem{"project", "Recent project prompts", "load on demand"}) {
			t.Errorf("source items = %+v", got)
		}
		if got := f.items[1]; len(got) != 1 || got[0] != (selectItem{"0", "#1 saved stash", ""}) {
			t.Errorf("stash items = %+v", got)
		}
		if f.text != "saved stash" {
			t.Errorf("text = %q", f.text)
		}
		for _, n := range f.notes {
			if n.level == "warning" {
				t.Errorf("warning %q", n.message)
			}
		}
	})

	tw(t, file, "stash history loads project prompts on demand from bounded newest files", func(t *testing.T) {
		agentDir, _ := agentRig(t)
		cwd := "/tmp/powerline-project-history-cwd"
		writeStashHistory(t, agentDir, []string{"saved stash"})
		sessions := getProjectSessionsPath(cwd)
		if err := os.MkdirAll(sessions, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(sessions) })

		now := time.Now()
		for i := 0; i < 50; i++ {
			path := filepath.Join(sessions, fmt.Sprintf("recent-%02d.jsonl", i))
			os.WriteFile(path, []byte(sessionLine(fmt.Sprintf("project prompt %d", i), now.UnixMilli()-int64(i))+"\n"), 0o644)
			mtime := now.Add(-time.Duration(i) * time.Second)
			os.Chtimes(path, mtime, mtime)
		}
		oldBroken := filepath.Join(sessions, "old-broken.jsonl")
		os.WriteFile(oldBroken, []byte(`{"type":"message","message":{"role":"user",`), 0o644)
		oldTime := now.Add(-100 * time.Second)
		os.Chtimes(oldBroken, oldTime, oldTime)

		f := &fakeKeys{cwd: cwd, session: "s", overlayInputs: [][]string{{"\x1b[B", "\r"}, {"\r"}}}
		newKeysState(map[string]any{}, false).openStashHistory(f)

		if got := strings.Join(f.titles, "|"); got != "Prompt history|Recent project prompts" {
			t.Errorf("titles = %q", got)
		}
		if f.text != "project prompt 0" {
			t.Errorf("text = %q", f.text)
		}
		for _, n := range f.notes {
			if n.level == "warning" {
				t.Errorf("warning %q", n.message)
			}
		}
	})
}

// What the twins above do not reach.

func TestStashStates(t *testing.T) {
	_, cwd := agentRig(t)
	f := &fakeKeys{cwd: cwd, session: "states", text: "  \n "}
	k := newKeysState(map[string]any{}, false)

	k.stashOrRestoreEditorText(f)
	if f.lastNote() != "Nothing to stash" {
		t.Errorf("an empty editor with no stash: %q", f.lastNote())
	}

	f.text = "first"
	k.stashOrRestoreEditorText(f)
	if f.lastNote() != "Text stashed" || f.text != "" {
		t.Errorf("stash: note %q text %q", f.lastNote(), f.text)
	}
	f.text = "second"
	k.stashOrRestoreEditorText(f)
	if f.lastNote() != "Stash updated" {
		t.Errorf("a second stash replaces the first: %q", f.lastNote())
	}
	k.stashOrRestoreEditorText(f)
	if f.text != "second" || f.lastNote() != "Stash restored" {
		t.Errorf("restore: note %q text %q", f.lastNote(), f.text)
	}
	f.text = ""
	k.stashOrRestoreEditorText(f)
	if f.text != "" || f.lastNote() != "Nothing to stash" {
		t.Errorf("a restored stash is gone: note %q text %q", f.lastNote(), f.text)
	}
	f.text = "second"
	k.stashOrRestoreEditorText(f)
	k.stashOrRestoreEditorText(f)
	f.text = "second"
	if got := readPersistedStashHistory(); len(got) != 2 || got[0] != "second" || got[1] != "first" {
		t.Errorf("persisted history = %q", got)
	}
	// Stashing the same text again does not duplicate it.
	f.text = "second"
	k.stashOrRestoreEditorText(f)
	if got := readPersistedStashHistory(); len(got) != 2 {
		t.Errorf("history grew on a repeated stash: %q", got)
	}
}

func TestStashHistoryPersistence(t *testing.T) {
	agentDir, _ := agentRig(t)
	if got := readPersistedStashHistory(); len(got) != 0 {
		t.Fatalf("no file: %q", got)
	}
	os.MkdirAll(filepath.Join(agentDir, "powerline-footer"), 0o755)
	path := getStashHistoryPath()
	os.WriteFile(path, []byte(`{"version":1,"history":["a","a"," ",3,"b"]}`), 0o644)
	if got := readPersistedStashHistory(); strings.Join(got, ",") != "a,b" {
		t.Errorf("normalized history = %q", got)
	}
	os.WriteFile(path, []byte(`[1]`), 0o644)
	if got := readPersistedStashHistory(); len(got) != 0 {
		t.Errorf("a non-object file: %q", got)
	}
	os.WriteFile(path, []byte(`{`), 0o644)
	if got := readPersistedStashHistory(); len(got) != 0 {
		t.Errorf("a broken file: %q", got)
	}
	var many []string
	for i := 0; i < 20; i++ {
		many = append(many, fmt.Sprintf("entry %d", i))
	}
	persistStashHistory(many)
	data, _ := os.ReadFile(path)
	want := "{\n  \"version\": 1,\n  \"history\": [\n    \"entry 0\""
	if !strings.HasPrefix(string(data), want) || !strings.HasSuffix(string(data), "\n  ]\n}\n") {
		t.Errorf("file = %q", data)
	}
	if got := readPersistedStashHistory(); len(got) != 12 {
		t.Errorf("history limit: %d", len(got))
	}
	h := []string{"x"}
	if pushStashHistory(&h, "x") || pushStashHistory(&h, " \n") || len(h) != 1 {
		t.Error("a repeat of the newest entry and blank text are not added")
	}
	for i := 0; i < 20; i++ {
		pushStashHistory(&h, fmt.Sprintf("p%d", i))
	}
	if len(h) != 12 || h[0] != "p19" {
		t.Errorf("pushed history = %q", h)
	}
}

func TestProjectPromptReading(t *testing.T) {
	agentDir, _ := agentRig(t)
	cwd := "/work/some//project/"
	if got := getProjectSessionsPath(cwd); got != filepath.Join(agentDir, "sessions", "--work-some-project--") {
		t.Errorf("sessions path = %q", got)
	}
	if prompts, err := readRecentProjectPrompts(cwd, 10); err != nil || len(prompts) != 0 {
		t.Errorf("no sessions: %q %v", prompts, err)
	}
	dir := getProjectSessionsPath(cwd)
	os.MkdirAll(dir, 0o755)
	blocks := newObject()
	blocks.set("type", "text")
	blocks.set("text", "block  one")
	image := newObject()
	image.set("type", "image")
	image.set("text", "not a text block")
	msg := newObject()
	msg.set("role", "user")
	msg.set("content", []any{blocks, image, newObject(), "x"})
	entry := newObject()
	entry.set("type", "message")
	entry.set("message", msg)
	lines := strings.Join([]string{
		sessionLine("older\n  prompt", 1),
		sessionLine("older\n  prompt", 2), // duplicate: kept once
		marshalJSON(entry, ""),
		`{"type":"message","message":{"role":"assistant","content":"no"}}`,
		`{"type":"message","message":{"role":"assistant","content":"leaked","meta":{"role":"user"}}}`,
		sessionLine("   ", 3),
		`not a message line`,
	}, "\n") + "\n"
	os.WriteFile(filepath.Join(dir, "a.jsonl"), []byte(lines), 0o644)
	os.WriteFile(filepath.Join(dir, "ignored.txt"), []byte(sessionLine("never", 1)), 0o644)

	got, err := readRecentProjectPrompts(cwd, 10)
	if err != nil || strings.Join(got, "|") != "block one|older prompt" {
		t.Errorf("prompts = %q (%v)", got, err)
	}
	if limited, _ := readRecentProjectPrompts(cwd, 1); len(limited) != 1 {
		t.Errorf("limit: %q", limited)
	}
	os.WriteFile(filepath.Join(dir, "b.jsonl"), []byte("{\"type\":\"message\",\"message\":{\"role\":\"user\",\n"), 0o644)
	_, err = readRecentProjectPrompts(cwd, 10)
	if err == nil || !strings.HasPrefix(err.Error(), "Failed to parse session file "+filepath.Join(dir, "b.jsonl")+": ") {
		t.Errorf("a broken user line must fail the read: %v", err)
	}
	// A file larger than the per-file window: only its tail is read, from the first complete line.
	big := filepath.Join(dir, "c.jsonl")
	os.Remove(filepath.Join(dir, "b.jsonl"))
	var b strings.Builder
	b.WriteString(sessionLine("far head", 1) + "\n")
	for b.Len() < projectPromptHistoryFileMaxBytes*2 {
		b.WriteString(sessionLine("filler", 1) + "\n")
	}
	b.WriteString(sessionLine("the tail", 9) + "\n")
	os.WriteFile(big, []byte(b.String()), 0o644)
	got, _ = readRecentProjectPrompts(cwd, 100)
	if !keysContains(got, "the tail") || keysContains(got, "far head") {
		t.Errorf("tail window: %q", got)
	}
}

func TestProjectPromptFileLimit(t *testing.T) {
	agentRig(t)
	cwd := "/work/file-limit"
	dir := getProjectSessionsPath(cwd)
	os.MkdirAll(dir, 0o755)
	now := time.Now()
	for i := 0; i < 55; i++ {
		path := filepath.Join(dir, fmt.Sprintf("s-%02d.jsonl", i))
		os.WriteFile(path, []byte(sessionLine(fmt.Sprintf("prompt %d", i), 1)+"\n"), 0o644)
		mtime := now.Add(-time.Duration(i) * time.Second)
		os.Chtimes(path, mtime, mtime)
	}
	got, err := readRecentProjectPrompts(cwd, 100)
	if err != nil || len(got) != 50 || got[0] != "prompt 0" || got[49] != "prompt 49" {
		t.Errorf("only the 50 newest files are read: %d prompts, first %q (%v)", len(got), got, err)
	}
}

func TestDisableDropsTheReloadHandOff(t *testing.T) {
	agentRig(t)
	f := &fakeKeys{session: "disable-session"}
	transferActiveStashReload("disable-session", new("handed over"))
	newKeysState(map[string]any{}, false).disable(f)
	if got := takeActiveStashReload("disable-session"); got != nil {
		t.Errorf("disable left the hand-off %q", *got)
	}
	f.wantStatus(t, "stash", "")
}

func TestSelectOverlayKeys(t *testing.T) {
	items := []selectItem{{"0", "a", ""}, {"1", "b", "second"}, {"2", "c", ""}}
	o := newSelectOverlay(keysPlainTheme{}, "Title", "hint", items, 2)
	press := func(data string) sdkResult {
		res, err := o.HandleInput(data)
		if err != nil {
			t.Fatal(err)
		}
		return sdkResult{res.Done, res.Value}
	}
	if o.list.selectedIndex != 0 {
		t.Fatal("starts on the first item")
	}
	press("\x1b[A")
	if o.list.selectedIndex != 2 {
		t.Errorf("up wraps to the last item, got %d", o.list.selectedIndex)
	}
	press("\x1b[B")
	if o.list.selectedIndex != 0 {
		t.Errorf("down wraps to the first item, got %d", o.list.selectedIndex)
	}
	press("\x1b[B")
	if r := press("\r"); !r.done || r.value.(map[string]any)["value"] != "1" || r.value.(map[string]any)["description"] != "second" {
		t.Errorf("enter selects the current item: %+v", r)
	}
	if r := press("\x1b"); !r.done || r.value != nil {
		t.Errorf("escape cancels: %+v", r)
	}
	if r := press("\x03"); !r.done || r.value != nil {
		t.Errorf("ctrl+c cancels: %+v", r)
	}
	if r := press("x"); r.done {
		t.Error("other keys do nothing")
	}
	lines := o.Render(30)
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "╭") || !strings.Contains(strings.Join(lines, "\n"), "(2/3)") || !strings.Contains(strings.Join(lines, "\n"), "→ b") {
		t.Errorf("render: %q", lines)
	}
	if buildStashPreview("  a \n b\t c ", 72) != "a b c" || buildStashPreview(" \n ", 72) != "(empty)" || buildStashPreview(strings.Repeat("x", 100), 10) == strings.Repeat("x", 100) {
		t.Error("buildStashPreview")
	}
}

type sdkResult struct {
	done  bool
	value any
}

func keysContains(list []string, s string) bool {
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}

func TestInsertSelectedPromptHistoryEntry(t *testing.T) {
	for _, c := range []struct {
		name, text, answer, wantText, wantNote string
	}{
		{"empty editor", "", "", "picked", "Inserted prompt"},
		{"whitespace editor", "  \n ", "", "picked", "Inserted prompt"},
		{"replace", "draft", "Replace", "picked", "Replaced editor with prompt"},
		{"append", "draft", "Append", "draft\npicked", "Appended prompt"},
		{"append after a newline", "draft\n", "Append", "draft\npicked", "Appended prompt"},
		{"cancel", "draft", "Cancel", "draft", ""},
		{"dismissed", "draft", "", "draft", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeKeys{text: c.text, selectAnswer: func(string, []string) string { return c.answer }}
			insertSelectedPromptHistoryEntry(f, "picked")
			if f.text != c.wantText || f.lastNote() != c.wantNote {
				t.Errorf("text %q note %q, want %q %q", f.text, f.lastNote(), c.wantText, c.wantNote)
			}
		})
	}
	f := &fakeKeys{text: "draft"}
	insertSelectedPromptHistoryEntry(f, "\npicked")
	f.selectAnswer = func(string, []string) string { return "Append" }
	insertSelectedPromptHistoryEntry(f, "\npicked")
	if f.text != "draft\npicked" {
		t.Errorf("a prompt that starts with a newline needs no separator: %q", f.text)
	}
}

func TestOpenStashHistoryWithoutAnyHistory(t *testing.T) {
	_, cwd := agentRig(t)
	f := &fakeKeys{cwd: cwd}
	newKeysState(map[string]any{}, false).openStashHistory(f)
	if f.lastNote() != "No prompt history yet" || len(f.titles) != 0 {
		t.Errorf("note %q titles %q: with nothing stashed the source picker is skipped", f.lastNote(), f.titles)
	}

	agentDir := os.Getenv("PI_CODING_AGENT_DIR")
	writeStashHistory(t, agentDir, []string{"one"})
	f = &fakeKeys{cwd: cwd, overlayInputs: [][]string{{"\x1b[B", "\r"}}}
	newKeysState(map[string]any{}, false).openStashHistory(f)
	if f.lastNote() != "No recent project prompts" {
		t.Errorf("note %q", f.lastNote())
	}

	// Escape closes a picker without touching the editor.
	f = &fakeKeys{cwd: cwd, text: "keep", overlayInputs: [][]string{{"\x1b"}}}
	newKeysState(map[string]any{}, false).openStashHistory(f)
	if f.text != "keep" || len(f.titles) != 1 {
		t.Errorf("escape: text %q titles %q", f.text, f.titles)
	}

	// A project read that fails is a warning.
	dir := getProjectSessionsPath(cwd)
	os.MkdirAll(dir, 0o755)
	t.Cleanup(func() { os.RemoveAll(dir) })
	os.WriteFile(filepath.Join(dir, "bad.jsonl"), []byte(`{"type":"message","role":"user"`+"\n"), 0o644)
	f = &fakeKeys{cwd: cwd, overlayInputs: [][]string{{"\x1b[B", "\r"}}}
	newKeysState(map[string]any{}, false).openStashHistory(f)
	if len(f.notes) != 1 || f.notes[0].level != "warning" || !strings.HasPrefix(f.notes[0].message, "Failed to load project prompts: Failed to parse session file ") {
		t.Errorf("notes = %+v", f.notes)
	}
}

func TestCopyAndCutShortcuts(t *testing.T) {
	f := &fakeKeys{text: "  "}
	k := newKeysState(map[string]any{}, false)
	k.run(f, actionCopyEditor)
	if f.lastNote() != "Editor is empty" || f.notes[len(f.notes)-1].level != "info" || len(f.clipboard) != 0 {
		t.Errorf("an empty editor: note %q clipboard %q", f.lastNote(), f.clipboard)
	}

	f = &fakeKeys{text: "draft"}
	k.run(f, actionCopyEditor)
	if len(f.clipboard) != 1 || f.clipboard[0] != "draft" || f.text != "draft" || f.lastNote() != "Copied editor text" {
		t.Errorf("copy: clipboard %q text %q note %q", f.clipboard, f.text, f.lastNote())
	}

	f = &fakeKeys{text: "draft"}
	k.run(f, actionCutEditor)
	if len(f.clipboard) != 1 || f.text != "" || f.lastNote() != "Cut editor text" || len(f.notes) != 1 {
		t.Errorf("cut: clipboard %q text %q notes %+v", f.clipboard, f.text, f.notes)
	}

	f = &fakeKeys{text: "draft", clipboardErr: errors.New("Clipboard unavailable")}
	k.run(f, actionCutEditor)
	if f.text != "draft" || len(f.notes) != 1 || f.notes[0] != (noteCall{"Clipboard unavailable", "error"}) {
		t.Errorf("a failed copy keeps the text and says so: text %q notes %+v", f.text, f.notes)
	}
}

func TestShortcutActions(t *testing.T) {
	k := newKeysState(map[string]any{}, false)
	hooked := &fakeKeys{hooks: map[string]func(){"queueOpen": func() {}, "reply": func() {}, "bashMode": func() {}}}
	bare := &fakeKeys{}
	for _, c := range []struct {
		data, want, wantBare string
	}{
		{"\x1b[104;7u", actionStashHistory, actionStashHistory},
		{"\x1b\x08", actionStashHistory, actionStashHistory},
		{"\x1b[27;7;72~", actionStashHistory, actionStashHistory},
		{"\x1b[99;7u", actionCopyEditor, actionCopyEditor},
		{"\x1b[120;7u", actionCutEditor, actionCutEditor},
		{"\x1b[113;7u", actionQueueOpen, ""},
		{"\x1b[66;6u", actionBashMode, ""},
		{"\x1b[98;6u", actionBashMode, ""},
		{"\x1b[104;7:3u", "", ""}, // key release
		{"h", "", ""},
	} {
		if got := k.shortcutAction(hooked, c.data); got != c.want {
			t.Errorf("shortcutAction(%q) = %q, want %q", c.data, got, c.want)
		}
		if got := k.shortcutAction(bare, c.data); got != c.wantBare {
			t.Errorf("without hooks shortcutAction(%q) = %q, want %q", c.data, got, c.wantBare)
		}
	}
	// reply is opt-in.
	k2 := newKeysState(settingsFrom(t, `{"powerlineShortcuts":{"reply":"ctrl+shift+r"}}`), false)
	if got := k2.shortcutAction(hooked, "\x1b[114;6u"); got != actionReply {
		t.Errorf("reply = %q", got)
	}
	if got := k.shortcutAction(hooked, "\x1b[114;6u"); got != "" {
		t.Errorf("reply is off by default, got %q", got)
	}
	// A hook runs when the action runs.
	ran := false
	h := &fakeKeys{hooks: map[string]func(){"queueOpen": func() { ran = true }}}
	k.run(h, actionQueueOpen)
	if !ran {
		t.Error("the queueOpen hook did not run")
	}
	// stashHistory rebinding: the legacy ctrl+alt+h forms only count for the default binding.
	k3 := newKeysState(settingsFrom(t, `{"powerlineShortcuts":{"stashHistory":"ctrl+alt+y"}}`), false)
	if got := k3.shortcutAction(bare, "\x1b[104;7u"); got != "" {
		t.Errorf("rebound stashHistory still answers ctrl+alt+h: %q", got)
	}
	if got := k3.shortcutAction(bare, "\x1b[121;7u"); got != actionStashHistory {
		t.Errorf("rebound stashHistory = %q", got)
	}
	// stashSharpSShortcut makes the printable ß a stash key.
	if newKeysState(map[string]any{}, true).isStashShortcutInput("ß") != true || newKeysState(map[string]any{}, false).isStashShortcutInput("ß") {
		t.Error("stashSharpSShortcut")
	}
}

func TestClipboard(t *testing.T) {
	type call struct {
		cmd     string
		args    []string
		input   string
		timeout float64
	}
	run := func(env clipboardEnv, ok func(cmd string) bool) (error, []call) {
		var calls []call
		r := runnerFunc(func(cmd string, args []string, input string, timeout float64) (string, bool) {
			calls = append(calls, call{cmd, args, input, timeout})
			if cmd == "wslpath" {
				return "C:\\tmp\\x.txt\n", ok(cmd)
			}
			return "", ok(cmd)
		})
		return copyToClipboard(r, env, "hello"), calls
	}
	envOf := func(goos string, vars map[string]string) clipboardEnv {
		return clipboardEnv{goos: goos, getenv: func(k string) string { return vars[k] }, isWSL: func() bool { return false },
			tempFile: func(string) (string, func(), error) { return "/tmp/x", func() {}, nil }}
	}
	all := func(string) bool { return true }
	none := func(string) bool { return false }

	if err, calls := run(envOf("darwin", nil), all); err != nil || len(calls) != 1 || calls[0].cmd != "pbcopy" || calls[0].input != "hello" || calls[0].timeout != 5000 {
		t.Errorf("darwin: %v %+v", err, calls)
	}
	if err, calls := run(envOf("windows", nil), all); err != nil || calls[0].cmd != "clip" {
		t.Errorf("windows: %v %+v", err, calls)
	}
	err, calls := run(envOf("linux", map[string]string{"WAYLAND_DISPLAY": "w", "DISPLAY": ":0"}), func(c string) bool { return c == "xsel" })
	if err != nil || len(calls) != 3 || calls[0].cmd != "wl-copy" || calls[1].cmd != "xclip" || strings.Join(calls[1].args, " ") != "-selection clipboard" || calls[2].cmd != "xsel" || strings.Join(calls[2].args, " ") != "--clipboard --input" {
		t.Errorf("linux fallback order: %v %+v", err, calls)
	}
	if err, calls := run(envOf("linux", map[string]string{"TERMUX_VERSION": "1"}), all); err != nil || calls[0].cmd != "termux-clipboard-set" {
		t.Errorf("termux: %v %+v", err, calls)
	}
	for _, c := range []struct {
		name string
		env  clipboardEnv
		want string
	}{
		{"wayland", envOf("linux", map[string]string{"WAYLAND_DISPLAY": "w"}), "Clipboard unavailable: install `wl-clipboard` (`wl-copy`) or check Wayland access"},
		{"x11", envOf("linux", map[string]string{"DISPLAY": ":0"}), "Clipboard unavailable: install `xclip` or `xsel`, or check X11 access"},
		{"termux", envOf("linux", map[string]string{"TERMUX_VERSION": "1"}), "Clipboard unavailable: install the Termux:API app and `termux-api` package"},
		{"darwin", envOf("darwin", nil), "Clipboard unavailable"},
	} {
		if err, _ := run(c.env, none); err == nil || err.Error() != c.want {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
	}
	if err, _ := run(envOf("linux", nil), none); err == nil || !strings.HasPrefix(err.Error(), "Clipboard unavailable") {
		t.Errorf("headless linux without OSC 52: %v", err)
	}
	wsl := envOf("linux", nil)
	wsl.isWSL = func() bool { return true }
	err, calls = run(wsl, all)
	if err != nil || len(calls) != 2 || calls[0].timeout != 1000 || calls[1].timeout != 5000 || calls[0].cmd != "wslpath" || calls[1].cmd != "powershell.exe" || !strings.Contains(calls[1].args[2], `ReadAllText('C:\tmp\x.txt'`) {
		t.Errorf("wsl: %v %+v", err, calls)
	}
	if err, _ := run(wsl, func(c string) bool { return c != "powershell.exe" }); err == nil {
		t.Error("a failing powershell is a failed copy")
	}
	big := strings.Repeat("x", 80_000)
	r := runnerFunc(func(string, []string, string, float64) (string, bool) { return "", false })
	if err := copyToClipboard(r, envOf("linux", nil), big); err == nil || !strings.Contains(err.Error(), "OSC 52 size limit") {
		t.Errorf("oversized: %v", err)
	}
	if s := clipboardScript("hi", "darwin"); s != `printf %s 'aGk=' | base64 -D | exec "$0" "$@"` {
		t.Errorf("script = %q", s)
	}
}

type runnerFunc func(command string, args []string, input string, timeoutMs float64) (string, bool)

func (f runnerFunc) run(command string, args []string, input string, timeoutMs float64) (string, bool) {
	return f(command, args, input, timeoutMs)
}
