package powerline_footer

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Checks of working-vibes.ts beyond the upstream tests. Expected values come from the original's own functions run under Node
// (mulberry32, the shuffle, normalizeEllipsis, String.replace with `$` patterns, toLowerCase).

func TestVibeMulberry32MatchesTheOriginal(t *testing.T) {
	for _, c := range []struct {
		seed int64
		want [3]float64
	}{
		{1700000000000, [3]float64{0.2730101849883795, 0.7020110758021474, 0.936108916066587}},
		{12345, [3]float64{0.9797282677609473, 0.3067522644996643, 0.484205421525985}},
		{0, [3]float64{0.26642920868471265, 0.0003297457005828619, 0.2232720274478197}},
		{4294967295, [3]float64{0.8964226141106337, 0.189478256739676, 0.7156526781618595}},
		{1760000000123, [3]float64{0.6521194675005972, 0.7577579119242728, 0.25515110371634364}},
	} {
		rng := mulberry32(c.seed)
		for i, want := range c.want {
			if got := rng(); got != want {
				t.Errorf("seed %d draw %d = %v, want %v", c.seed, i, got, want)
			}
		}
	}
}

func TestVibeShuffleOrderMatchesTheOriginal(t *testing.T) {
	m := newVibeManager()
	for _, c := range []struct {
		n    int
		seed int64
		want []int
	}{
		{8, 1700000000000, []int{0, 7, 6, 3, 1, 5, 4, 2}},
		{5, 42, []int{0, 4, 2, 1, 3}},
	} {
		vibes := make([]string, c.n)
		for i := range vibes {
			vibes[i] = string(rune('a' + i))
		}
		for i, idx := range c.want {
			if got := m.vibeAtIndex(vibes, i, c.seed); got != vibes[idx] {
				t.Errorf("n=%d seed=%d index %d = %q, want %q", c.n, c.seed, i, got, vibes[idx])
			}
		}
		// Past the end the order repeats (index modulo the length).
		if got := m.vibeAtIndex(vibes, c.n, c.seed); got != vibes[c.want[0]] {
			t.Errorf("index %d wraps to %q, want %q", c.n, got, vibes[c.want[0]])
		}
	}
}

func TestVibeNormalizeEllipsisMatchesTheOriginal(t *testing.T) {
	for in, want := range map[string]string{
		"ローディング中":  "ローディング中……", // Script_Extensions, not Script: "ー" is Hiragana and Katakana only
		"ー":        "ー……",
		"Hello。":   "Hello。……", // U+3002 is Common with Han, Hiragana, Katakana extensions
		"「x」":      "「x」……",
		"a·b":      "a·b……", // U+00B7 is in Han's extensions
		"…":        "...",
		"abc....":  "abc...",
		"中文...":    "中文……",
		"x、y":      "x、y……",
		"Hi ー":     "Hi ー……",
		"plain":    "plain...",
		"mix……...": "mix...",
		"":         "...",
	} {
		if got := normalizeEllipsis(in); got != want {
			t.Errorf("normalizeEllipsis(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestVibeTemplateSubstitutionFollowsStringReplace(t *testing.T) {
	m := newVibeManager()
	m.config.PromptTemplate = "Hello {theme} {task}"
	// Node: "Hello {theme} {task}".replace(/\{theme\}/g, "a$&b$$c$`d$'e$1f$<g") gives
	// "Hello a{theme}b$cHello d {task}e$1f$<g {task}", and {task} then takes the text in both places.
	got := m.buildVibePrompt("a$&b$$c$`d$'e$1f$<g", "T")
	if want := "Hello a{theme}b$cHello d Te$1f$<g T"; got != want {
		t.Errorf("prompt = %q, want %q", got, want)
	}
}

func TestVibeFileSlugMatchesTheOriginal(t *testing.T) {
	for in, want := range map[string]string{
		"star trek":      "star-trek",
		"  Star   Trek ": "star-trek",
		"İstanbul Thing": "i-stanbul-thing",
		"--_a_--":        "a",
		"":               "theme",
		"!!!":            "theme",
		"日本語":            "theme",
		"a/b\\c":         "a-b-c",
		"Under_score-ok": "under_score-ok",
	} {
		if got := toVibeFileSlug(in); got != want {
			t.Errorf("toVibeFileSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestVibeParseResponse(t *testing.T) {
	m := newVibeManager()
	m.config.MaxLength = 20
	for _, c := range []struct{ in, want string }{
		{"", "Working..."},
		{"  \"Warping in...\"  \nExplanation", "Warping in..."},
		{"'Quoted'", "Quoted..."},
		{`"`, "Working..."},
		{"...", "Working..."},
		{"……", "Working..."},
		{"This message is far too long for the limit...", "This message is f..."},
		{"Cloaking up", "Cloaking up..."},
		{"abcdefghijklmnopq...", "abcdefghijklmnopq..."},                 // exactly the limit: kept
		{"abcdefghijklmnopqr...", "abcdefghijklmnopq..."},                // one over: cut to limit-3 and re-ellipsised
		{strings.Repeat("日", 18) + "……", strings.Repeat("日", 18) + "……"}, // exactly the limit with the two-unit ellipsis: kept
		{strings.Repeat("日", 19) + "……", strings.Repeat("日", 17) + "……"}, // one over: the cut leaves 17 units, then the ellipsis again
	} {
		if got := m.parseVibeResponse(c.in, "Working"); got != c.want {
			t.Errorf("parseVibeResponse(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestVibeConfigDefaultsAndLimits(t *testing.T) {
	home, _ := pathEnv(t)
	write := func(json string) vibeConfig {
		dir := filepath.Join(home, ".pi", "agent")
		_ = os.MkdirAll(dir, 0o755)
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(json), 0o644); err != nil {
			t.Fatal(err)
		}
		return newVibeManager().config
	}
	cfg := write(``)
	eq(t, cfg, vibeConfig{Mode: vibeModeGenerate, ModelSpec: defaultVibeModel, Fallback: "Working", TimeoutMs: 3000, RefreshInterval: 30000, PromptTemplate: defaultVibePrompt, MaxLength: 65}, "defaults")

	cfg = write(`{"workingVibe":"Off","workingVibeMode":"file","workingVibeRefreshInterval":-5,"workingVibeMaxLength":2.9,"workingVibeModel":"a/b","workingVibeFallback":"Hold on","workingVibePrompt":"{theme}!"}`)
	eq(t, cfg.Theme == nil, true, "off is disabled")
	eq(t, cfg.Mode, vibeModeFile, "mode")
	eq(t, cfg.RefreshInterval, 0.0, "negative interval clamps to 0")
	eq(t, cfg.MaxLength, 4, "max length floors at 4")
	eq(t, cfg.ModelSpec, "a/b", "model")
	eq(t, cfg.Fallback, "Hold on", "fallback")
	eq(t, cfg.PromptTemplate, "{theme}!", "prompt")

	cfg = write(`{"workingVibe":"pirate","workingVibeMode":"bogus","workingVibeRefreshInterval":"7","workingVibeMaxLength":70.9}`)
	eq(t, *cfg.Theme, "pirate", "theme")
	eq(t, cfg.Mode, vibeModeGenerate, "invalid mode")
	eq(t, cfg.RefreshInterval, 30000.0, "non-number interval")
	eq(t, cfg.MaxLength, 70, "max length floors")

	cfg = write(`[1]`)
	eq(t, cfg.Theme == nil && cfg.ModelSpec == defaultVibeModel, true, "non-object settings are ignored")
}

func TestVibeSettingsWritesKeepOtherKeysAndNeverOverwriteBrokenFiles(t *testing.T) {
	home, m := vibeHome(t)
	path := filepath.Join(home, ".pi", "agent", "settings.json")
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte(`{"theme":"dark","workingVibeMode":"file","workingVibeModel":"x/y"}`), 0o644)
	m.initVibeManager(scriptedRegistry{})
	eq(t, m.setTheme(new("pirate")), true, "set theme")
	eq(t, m.setMode(vibeModeGenerate), true, "mode generate removes the key")
	eq(t, m.setModel(defaultVibeModel), true, "default model removes the key")
	data, _ := os.ReadFile(path)
	eq(t, string(data), "{\n  \"theme\": \"dark\",\n  \"workingVibe\": \"pirate\"\n}\n", "file")
	eq(t, m.setTheme(nil), true, "off")
	data, _ = os.ReadFile(path)
	eq(t, string(data), "{\n  \"theme\": \"dark\"\n}\n", "file after off")

	for _, broken := range []string{`not json`, `[1,2]`, `"text"`} {
		_ = os.WriteFile(path, []byte(broken), 0o644)
		eq(t, m.setTheme(new("pirate")), false, "broken settings "+broken)
		data, _ = os.ReadFile(path)
		eq(t, string(data), broken, "broken settings are left alone")
	}
}

func TestVibeRateLimitAndHints(t *testing.T) {
	_, m := vibeHome(t)
	var prompts []string
	m.initVibeManager(scriptedRegistry{
		find: testProvider("test-model"),
		complete: func(_, request, _ map[string]any) map[string]any {
			prompts = append(prompts, request["messages"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string))
			return vibeText("Beep...")
		},
	})
	now := int64(1_000_000)
	m.now = func() int64 { return now }
	m.setTheme(new("robots"))
	m.setModel("test-provider/test-model")
	m.config.RefreshInterval = 30000
	noop := func(string) {}

	m.onToolCall("read", map[string]any{"path": "/a"}, noop, "") // not streaming yet
	m.onAgentStart()
	m.onBeforeAgentStart("start", noop)
	m.wait()
	m.onToolCall("read", map[string]any{"path": "/a"}, noop, "") // inside the interval
	now += 29999
	m.onToolCall("read", map[string]any{"path": "/a"}, noop, "")
	m.wait()
	eq(t, len(prompts), 1, "prompts inside the interval")

	now += 1 // exactly the interval: allowed (the original skips only when now - last < interval)
	m.onToolCall("read", map[string]any{"path": "/a"}, noop, "")
	m.wait()
	eq(t, len(prompts), 2, "the call at the interval is allowed")
	now += 29999 // the allowed call restarted the interval
	m.onToolCall("read", map[string]any{"path": "/a"}, noop, "")
	m.wait()
	eq(t, len(prompts), 2, "the interval restarts at every refresh")
	now += 1
	hints := map[string]struct {
		tool  string
		input map[string]any
		ctx   string
		want  string
	}{
		"read":    {"read", map[string]any{"path": "/r"}, "", "reading file: /r"},
		"write":   {"write", map[string]any{"path": "/w"}, "", "writing file: /w"},
		"edit":    {"edit", map[string]any{"path": "/e"}, "", "editing file: /e"},
		"bash":    {"bash", map[string]any{"command": strings.Repeat("x", 50)}, "", "running command: " + strings.Repeat("x", 40)},
		"other":   {"grep", map[string]any{"path": "/g"}, "", "using grep tool"},
		"nopath":  {"read", map[string]any{}, "", "using read tool"},
		"agent":   {"read", map[string]any{"path": "/r"}, "Compiling the warp core", "Compiling the warp core"},
		"short":   {"read", map[string]any{"path": "/r"}, "0123456789", "reading file: /r"}, // 10 chars is not longer than 10
		"numpath": {"read", map[string]any{"path": float64(3)}, "", "reading file: 3"},
	}
	for name, c := range hints {
		now += 30000
		prompts = nil
		m.onToolCall(c.tool, c.input, noop, c.ctx)
		m.wait()
		if len(prompts) != 1 || !strings.Contains(prompts[0], "Task: "+c.want+"\n") {
			t.Errorf("%s: prompts = %q, want the task %q", name, prompts, c.want)
		}
	}
	m.onAgentEnd(noop)
}

func TestVibeRecentVibesAreExcludedAndFallbackIsNotTracked(t *testing.T) {
	_, m := vibeHome(t)
	replies := []string{"One...", "Two...", "One...", "Three...", "Four...", "Five...", "Six...", "Seven..."}
	var prompts []string
	m.initVibeManager(scriptedRegistry{
		find: testProvider("test-model"),
		complete: func(_, request, _ map[string]any) map[string]any {
			prompts = append(prompts, request["messages"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string))
			reply := replies[0]
			replies = replies[1:]
			return vibeText(reply)
		},
	})
	m.setTheme(new("numbers"))
	m.setModel("test-provider/test-model")
	m.onAgentStart()
	for range 8 {
		m.onBeforeAgentStart("count", func(string) {})
		m.wait()
	}
	excluded := func(i int) string {
		_, after, _ := strings.Cut(prompts[i], "Don't use: ")
		line, _, _ := strings.Cut(after, "\n")
		return line
	}
	eq(t, strings.Contains(prompts[0], "Don't use"), false, "no exclusion at first")
	eq(t, excluded(1), "One...", "second prompt excludes the first")
	eq(t, excluded(2), "Two..., One...", "newest first")
	eq(t, excluded(3), "One..., Two...", "a repeat moves to the front without a duplicate")
	eq(t, excluded(6), "Five..., Four..., Three..., One..., Two...", "five at most")
	eq(t, excluded(7), "Six..., Five..., Four..., Three..., One...", "the oldest drops out")
	m.setTheme(new("other"))
	eq(t, len(m.recentVibes), 0, "a theme change clears the list")
}

func TestVibeOnlyTheLatestGenerationUpdatesAndAgentEndCancels(t *testing.T) {
	_, m := vibeHome(t)
	release := make(chan struct{})
	var calls atomic.Int32
	m.initVibeManager(scriptedRegistry{
		find: testProvider("test-model"),
		complete: func(_, _, _ map[string]any) map[string]any {
			n := calls.Add(1)
			if n == 1 {
				<-release // the first generation is still running when the second starts
				return vibeText("Stale...")
			}
			return vibeText("Fresh...")
		},
	})
	m.setTheme(new("race"))
	m.setModel("test-provider/test-model")
	var rec vibeRecorder
	m.onAgentStart()
	m.onBeforeAgentStart("one", rec.set)
	for calls.Load() < 1 {
		time.Sleep(time.Millisecond)
	}
	m.onBeforeAgentStart("two", rec.set)
	m.wait()
	close(release)
	eq(t, rec.all(), []string{"Channeling race...", "Channeling race...", "Fresh..."}, "only the latest generation updates")

	// An agent end while a generation runs: no update afterwards, and the message resets.
	calls.Store(0)
	release2 := make(chan struct{})
	m.initVibeManager(scriptedRegistry{
		find: testProvider("test-model"),
		complete: func(_, _, _ map[string]any) map[string]any {
			calls.Add(1)
			<-release2
			return vibeText("Late...")
		},
	})
	var rec2 vibeRecorder
	m.onAgentStart()
	m.onBeforeAgentStart("three", rec2.set)
	for calls.Load() < 1 {
		time.Sleep(time.Millisecond)
	}
	m.onAgentEnd(rec2.set)
	done := make(chan struct{})
	go func() { m.wait(); close(done) }()
	select {
	case <-done: // the aborted wait returns without the late reply
	case <-time.After(2 * time.Second):
		t.Error("agent end did not abort the in-flight generation")
	}
	close(release2)
	m.wait()
	eq(t, rec2.all(), []string{"Channeling race...", ""}, "agent end cancels")
}

func TestVibeTimeoutShowsTheFallback(t *testing.T) {
	_, m := vibeHome(t)
	release := make(chan struct{})
	defer close(release)
	m.initVibeManager(scriptedRegistry{
		find:     testProvider("test-model"),
		complete: func(_, _, _ map[string]any) map[string]any { <-release; return vibeText("Never...") },
	})
	m.setTheme(new("slow"))
	m.setModel("test-provider/test-model")
	m.config.TimeoutMs = 30
	var rec vibeRecorder
	m.onAgentStart()
	m.onBeforeAgentStart("wait", rec.set)
	m.wait()
	// An aborted provider request resolves with an empty aborted message, so the original shows the fallback (its controller was not aborted).
	eq(t, rec.all(), []string{"Channeling slow...", "Working..."}, "timeout")
	eq(t, len(m.recentVibes), 0, "the fallback is not tracked")
}

func TestVibeFailuresShowTheFallbackOrKeepThePlaceholder(t *testing.T) {
	home, m := vibeHome(t)
	m.setTheme(new("noir"))
	m.setModel("test-provider/test-model")
	settings := filepath.Join(home, ".pi", "agent", "settings.json")
	var logs [][]any
	m.debug = func(args ...any) { logs = append(logs, args) }
	run := func(registry scriptedRegistry) []string {
		var rec vibeRecorder
		m.initVibeManager(registry)
		m.onAgentStart()
		m.onBeforeAgentStart("x", rec.set)
		m.wait()
		return rec.all()
	}
	// Model not found: the fallback replaces the placeholder.
	eq(t, run(scriptedRegistry{}), []string{"Channeling noir...", "Working..."}, "model not found")
	eq(t, logs, [][]any{{"[working-vibes] Model not found: test-provider/test-model"}}, "model log")
	// Auth failure.
	logs = nil
	eq(t, run(scriptedRegistry{find: testProvider("test-model"), auth: func(map[string]any) (map[string]any, error) {
		return map[string]any{"ok": false, "error": "no key"}, nil
	}}), []string{"Channeling noir...", "Working..."}, "auth failure")
	eq(t, logs, [][]any{{"[working-vibes] Auth failed for test-provider:", "no key"}}, "auth log")
	// A provider error with no text shows the fallback and logs the error.
	logs = nil
	eq(t, run(scriptedRegistry{find: testProvider("test-model"), complete: func(_, _, _ map[string]any) map[string]any {
		return map[string]any{"content": []any{}, "stopReason": "error", "errorMessage": "boom"}
	}}), []string{"Channeling noir...", "Working..."}, "provider error")
	eq(t, logs, [][]any{{"[working-vibes] Vibe generation failed for test-provider/test-model: boom"}}, "error log")
	// A custom fallback is normalized.
	if err := os.WriteFile(settings, []byte(`{"workingVibe":"noir","workingVibeModel":"test-provider/test-model","workingVibeFallback":"Hold on"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	eq(t, run(scriptedRegistry{}), []string{"Channeling noir...", "Hold on..."}, "custom fallback")
}

func TestVibeGenerateBatchEdges(t *testing.T) {
	home, m := vibeHome(t)
	// Not initialized.
	res, _ := m.generateVibesBatch("x", 5)
	eq(t, res.Error, "Extension not initialized", "no registry")
	m.initVibeManager(scriptedRegistry{find: testProvider("test-model")})
	m.setModel("nomodelspec")
	res, _ = m.generateVibesBatch("x", 5)
	eq(t, res.Error, "Invalid model spec", "bad spec")
	m.setModel("test-provider/missing")
	res, _ = m.generateVibesBatch("x", 5)
	eq(t, res.Error, "Model not found: test-provider/missing", "missing model")

	m.setModel("test-provider/test-model")
	reply := ""
	var asked string
	m.initVibeManager(scriptedRegistry{
		find: testProvider("test-model"),
		auth: func(map[string]any) (map[string]any, error) { return map[string]any{"ok": false}, nil },
	})
	res, _ = m.generateVibesBatch("x", 5)
	eq(t, res.Error, "undefined", "auth error without text")
	m.initVibeManager(scriptedRegistry{
		find: testProvider("test-model"),
		complete: func(_, request, _ map[string]any) map[string]any {
			asked = request["messages"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
			return vibeText(reply)
		},
	})
	res, _ = m.generateVibesBatch("x", 5)
	eq(t, res.Error, "Empty response from model", "empty")
	reply = "...\nab\nx\n  \n3) 'Okay...'\n\"Wow\"\n.5.Counted"
	res, _ = m.generateVibesBatch("It's $& {count}", 700)
	eq(t, res.Success, true, "success")
	eq(t, strings.Contains(asked, "Generate 500 unique 2-4 word loading messages for a \"It's {theme} 500\" theme."), true, "count clamped to 500; $& expands to the match, and {count} is replaced in the theme too: "+asked)
	data, _ := os.ReadFile(res.FilePath)
	eq(t, string(data), "ab...\nx...\nOkay...\nWow...\nCounted...", "cleaned lines (leading bullets, trailing quote, length filter)")
	eq(t, res.FilePath, filepath.Join(home, ".pi", "agent", "vibes", "it-s-count.txt"), "path")

	reply = "..\n--\n1."
	res, _ = m.generateVibesBatch("none", 1)
	eq(t, res.Error, "No valid vibes generated", "nothing valid")

	// The vibes directory is a file: Node's wording of the failure.
	dir := filepath.Join(home, ".pi", "agent", "vibes")
	_ = os.RemoveAll(dir)
	_ = os.WriteFile(dir, []byte("x"), 0o644)
	reply = "Fine..."
	res, _ = m.generateVibesBatch("z", 1)
	eq(t, res.Error, "ENOTDIR: not a directory, open '"+filepath.Join(dir, "z.txt")+"'", "file in the way")
}

func TestVibeNodeFsMessage(t *testing.T) {
	_, err := os.ReadFile(filepath.Join(t.TempDir(), "missing"))
	eq(t, nodeFsMessage(err, "open", "/p"), "ENOENT: no such file or directory, open '/p'", "enoent")
}

func TestVibeRecentAgentContext(t *testing.T) {
	msg := func(role string, blocks ...any) map[string]any {
		return map[string]any{"type": "message", "message": map[string]any{"role": role, "content": blocks}}
	}
	text := func(s string) map[string]any { return map[string]any{"type": "text", "text": s} }
	long := strings.Repeat("é", 250)
	branch := []map[string]any{
		msg("assistant", text("old")),
		msg("user", text("question")),
		{"type": "message", "message": map[string]any{"role": "assistant", "content": "not an array"}},
		msg("assistant", map[string]any{"type": "thinking", "thinking": "t"}, text("   "), text("  "+long+"  ")),
		{"type": "compaction"},
	}
	eq(t, getRecentAgentContext(branch), strings.Repeat("é", 200), "first 200 units of the latest assistant text")
	eq(t, getRecentAgentContext(branch[:2]), "old", "falls back to older messages")
	eq(t, getRecentAgentContext(nil), "", "none")
}

func TestVibeStatusInFileMode(t *testing.T) {
	home, m := vibeHome(t)
	m.initVibeManager(scriptedRegistry{})
	var notes []string
	notify := func(message, level string) { notes = append(notes, level+": "+message) }
	_ = m.command("pirate", notify)
	_ = m.command("mode generate", notify) // no key change
	m.config.Mode = vibeModeFile
	_ = m.command("", notify)
	dir := filepath.Join(home, ".pi", "agent", "vibes")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "pirate.txt"), []byte("Arr...\nYo ho\nAhoy..."), 0o644)
	_ = m.command("   ", notify)
	_ = m.command("Mixed Case Theme", notify)
	_ = m.command("MODE FILE", notify)
	eq(t, notes, []string{
		"info: Vibe set to: pirate",
		"info: Vibe mode set to: generate",
		"info: Vibe: pirate | Mode: file | Model: openai-codex/gpt-5.6-luna:low | File: not found",
		"info: Vibe: pirate | Mode: file | Model: openai-codex/gpt-5.6-luna:low | File: 2 vibes",
		"warning: Vibe set to: Mixed Case Theme (file mode, but no file found - run /vibe generate Mixed Case Theme)",
		`error: No vibe file for "Mixed Case Theme". Run /vibe generate Mixed Case Theme first`,
	}, "notifications")
}

func TestVibeCommandReportsUnpersistedSettings(t *testing.T) {
	home, m := vibeHome(t)
	path := filepath.Join(home, ".pi", "agent", "settings.json")
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte(`not json`), 0o644)
	var notes []string
	notify := func(message, level string) { notes = append(notes, level+": "+message) }
	_ = m.command("pirate", notify)
	_ = m.command("model a/b", notify)
	_ = m.command("mode file", notify)
	_ = m.command("mode generate", notify)
	_ = m.command("off", notify)
	eq(t, notes, []string{
		"warning: Vibe set to: pirate (not persisted; check settings.json)",
		"warning: Vibe model set to: a/b (not persisted; check settings.json)",
		`error: No vibe file for "pirate". Run /vibe generate pirate first`,
		"warning: Vibe mode set to: generate (not persisted; check settings.json)",
		"warning: Vibe disabled (not persisted; check settings.json)",
	}, "notifications")
	// With a file mode and no file, the theme warning carries the suffix too.
	notes = nil
	m.config.Mode = vibeModeFile
	_ = m.command("pirate2", notify)
	eq(t, notes, []string{"warning: Vibe set to: pirate2 (file mode, but no file found - run /vibe generate pirate2) (not persisted; check settings.json)"}, "theme in file mode")
}

func TestVibeUpdatesOnlyWhileTheAgentStreams(t *testing.T) {
	_, m := vibeHome(t)
	release := make(chan struct{})
	m.initVibeManager(scriptedRegistry{
		find:     testProvider("test-model"),
		complete: func(_, _, _ map[string]any) map[string]any { <-release; return vibeText("Late...") },
	})
	m.setTheme(new("stream"))
	m.setModel("test-provider/test-model")

	// agent_start never came: the reply is dropped.
	var rec vibeRecorder
	m.onBeforeAgentStart("x", rec.set)
	release <- struct{}{}
	m.wait()
	eq(t, rec.all(), []string{"Channeling stream..."}, "not streaming")

	// agent_start came while the call ran (as it does in a turn): the reply is shown.
	var rec2 vibeRecorder
	m.onBeforeAgentStart("x", rec2.set)
	m.onAgentStart()
	release <- struct{}{}
	m.wait()
	eq(t, rec2.all(), []string{"Channeling stream...", "Late..."}, "streaming")
}

func TestVibeStyleNeedsTheTheme(t *testing.T) {
	_, m := vibeHome(t)
	m.initVibeManager(scriptedRegistry{})
	m.setTheme(new("plain"))
	m.setMode(vibeModeFile)
	m.setWorkingMessageColor("warning") // a color with no theme yet: unstyled
	var rec vibeRecorder
	m.onBeforeAgentStart("x", rec.set)
	eq(t, rec.all()[0], "Channeling plain...", "no theme")
	m.setWorkingMessageColor("rainbow") // the gradient needs no theme token but still the theme being known
	m.onBeforeAgentStart("x", rec.set)
	eq(t, rec.all()[2], "Channeling plain...", "rainbow without a theme")
}

func TestVibeNeedsTheRegistry(t *testing.T) {
	_, m := vibeHome(t) // no initVibeManager: the original has no extension context yet
	m.setTheme(new("early"))
	m.setMode(vibeModeFile)
	var rec vibeRecorder
	m.onBeforeAgentStart("x", rec.set)
	m.onAgentStart()
	m.onToolCall("read", map[string]any{"path": "/a"}, rec.set, "")
	eq(t, rec.all(), []string(nil), "no registry")
}

func TestVibeBatchTimeout(t *testing.T) {
	_, m := vibeHome(t)
	eq(t, m.batchTimeout, 30*time.Second, "the batch waits 30 seconds")
	release := make(chan struct{})
	defer close(release)
	m.initVibeManager(scriptedRegistry{
		find:     testProvider("test-model"),
		complete: func(_, _, _ map[string]any) map[string]any { <-release; return vibeText("Never...") },
	})
	m.setModel("test-provider/test-model")
	m.batchTimeout = 20 * time.Millisecond
	res, _ := m.generateVibesBatch("slow", 3)
	eq(t, res.Success, false, "timed out")
	eq(t, res.Error, "Empty response from model", "an aborted request has no text")
}

func TestVibeGenerateArgsOverflowIsNotACount(t *testing.T) {
	huge := strings.Repeat("9", 400) // parseInt gives Infinity
	theme, count, ok := parseVibeGenerateArgs([]string{"a", huge})
	eq(t, []any{theme, count, ok}, []any{"a " + huge, 100, true}, "overflowing count")
	theme, count, ok = parseVibeGenerateArgs([]string{"a", "007"})
	eq(t, []any{theme, count, ok}, []any{"a", 7, true}, "leading zeros")
	theme, count, ok = parseVibeGenerateArgs([]string{"42"})
	eq(t, []any{theme, count, ok}, []any{"42", 100, true}, "a lone number is the theme")
}

func TestVibeFileLinesNeedAThreeDotOrEllipsisSuffix(t *testing.T) {
	home, m := vibeHome(t)
	dir := filepath.Join(home, ".pi", "agent", "vibes")
	_ = os.MkdirAll(dir, 0o755)
	write := func(theme, content string) {
		if err := os.WriteFile(filepath.Join(dir, toVibeFileSlug(theme)+".txt"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("dots", "Two dots..\nFour dots....\nOne dot.\n\ufeffBom first...\n\u0085Nel...\nok…\n")
	// JavaScript's trim strips U+FEFF but not U+0085 (Go's TrimSpace is the other way round).
	eq(t, m.loadVibesFromFile("dots"), []string{"Four dots...", "Bom first...", "\u0085Nel...", "ok..."}, "accepted lines")
	// The cache follows the theme.
	write("first", "From first...")
	write("second", "From second...")
	m.initVibeManager(scriptedRegistry{})
	m.setMode(vibeModeFile)
	m.setTheme(new("first"))
	var rec vibeRecorder
	m.onBeforeAgentStart("x", rec.set)
	m.setTheme(new("second"))
	m.onBeforeAgentStart("x", rec.set)
	eq(t, rec.all(), []string{"Channeling first...", "From first...", "Channeling second...", "From second..."}, "a theme change reloads the file")
}

func TestVibeEveryThinkingLevelSuffixResolves(t *testing.T) {
	_, m := vibeHome(t)
	var levels []any
	m.initVibeManager(scriptedRegistry{
		find: testProvider("test-model"),
		complete: func(_, _, options map[string]any) map[string]any {
			levels = append(levels, options["reasoning"])
			return vibeText("Thinking...")
		},
	})
	var want []any
	for _, level := range []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"} {
		m.setModel("test-provider/test-model:" + level)
		res, _ := m.generateVibesBatch("t", 1)
		eq(t, res.Success, true, level)
		want = append(want, level)
	}
	eq(t, levels, want, "reasoning levels")
	// Only the last colon starts the suffix: "colon:id" is the model, "low" the level.
	m.initVibeManager(scriptedRegistry{
		find: testProvider("colon:id"),
		complete: func(_, _, options map[string]any) map[string]any {
			levels = append(levels, options["reasoning"])
			return vibeText("Thinking...")
		},
	})
	m.setModel("test-provider/colon:id:low")
	res, _ := m.generateVibesBatch("t", 1)
	eq(t, res.Success, true, "colon in the model id")
	eq(t, levels[len(levels)-1], "low", "level after the last colon")
	m.initVibeManager(scriptedRegistry{find: testProvider("test-model")})
	m.setModel("test-provider/test-model:ultra")
	res, _ = m.generateVibesBatch("t", 1)
	eq(t, res.Error, "Model not found: test-provider/test-model:ultra", "an unknown suffix stays part of the id")
}

func TestVibeCommandSplitsOnJavaScriptWhitespace(t *testing.T) {
	_, m := vibeHome(t)
	var notes []string
	notify := func(message, level string) { notes = append(notes, level+": "+message) }
	// U+FEFF separates words in JavaScript (\s) but not in Go's strings.Fields.
	_ = m.command("model\ufeffa/b\u3000c", notify)
	eq(t, notes, []string{"info: Vibe model set to: a/b c"}, "words split on U+FEFF and U+3000")
}

func TestVibeBatchClearsTheCacheOfItsTheme(t *testing.T) {
	_, m := vibeHome(t)
	m.initVibeManager(scriptedRegistry{
		find:     testProvider("test-model"),
		complete: func(_, _, _ map[string]any) map[string]any { return vibeText("Fresh one...") },
	})
	m.setModel("test-provider/test-model")
	m.setMode(vibeModeFile)
	m.setTheme(new("cached"))
	var rec vibeRecorder
	m.onBeforeAgentStart("x", rec.set) // no file yet: the fallback, and an empty cache for "cached"
	res, _ := m.generateVibesBatch("cached", 1)
	eq(t, res.Success, true, "batch")
	m.onBeforeAgentStart("x", rec.set)
	eq(t, rec.all(), []string{"Channeling cached...", "Working...", "Channeling cached...", "Fresh one..."}, "the next use loads the new file")
}
