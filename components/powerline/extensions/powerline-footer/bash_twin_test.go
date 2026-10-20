package powerline_footer

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Twins of tests/bash-mode.test.ts (ledger file "bash-mode"). Adaptations common to all of them: the original leaks
// process.env.HISTFILE between cases and writes project history into the real agent dir; each twin sets its own HISTFILE and a
// private PI_CODING_AGENT_DIR (bashTestEnv). The AbortController signal is a context.Context.

// bashTestEnv isolates a case: a private agent dir for project history and theme.json, a private HISTFILE (empty unless
// histfile content is given) and a private working directory.
func bashTestEnv(t *testing.T, histfile *string) (cwd, histPath string) {
	t.Helper()
	root := t.TempDir()
	setenv(t, "PI_CODING_AGENT_DIR", s(filepath.Join(root, "agent")))
	cwd = filepath.Join(root, "cwd")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	histPath = filepath.Join(root, ".zsh_history")
	setenv(t, "HISTFILE", s(histPath))
	if histfile != nil {
		if err := os.WriteFile(histPath, []byte(*histfile), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return cwd, histPath
}

func ghost(t *testing.T, line, cwd, shell string) *ghostSuggestion {
	t.Helper()
	return (&bashCompletionEngine{}).getGhostSuggestion(context.Background(), line, cwd, shell)
}

func wantGhost(t *testing.T, got *ghostSuggestion, value, source string) {
	t.Helper()
	if got == nil {
		t.Fatalf("no suggestion, want %q (%s)", value, source)
	}
	if got.Value != value || got.Source != source {
		t.Errorf("suggestion = {%q %s}, want {%q %s}", got.Value, got.Source, value, source)
	}
}

func resolveManagedShellPath() string {
	for _, p := range []string{"/bin/zsh", "/bin/bash"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func waitForShellCommand(t *testing.T, session *managedShellSession) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for session.State().Running && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	if session.State().Running {
		t.Fatal("shell command still running after 5s")
	}
}

func TestBashModeTwins(t *testing.T) {
	tw(t, "bash-mode", "project history is stored newest-first and global zsh history parses histfile format", func(t *testing.T) {
		cwd, histfile := bashTestEnv(t, nil)
		appendProjectHistory(cwd, "git status", cwd)
		appendProjectHistory(cwd, "git stash", cwd)
		appendProjectHistory(cwd, "git status", cwd)
		content := strings.Join([]string{": 1711111111:0;git fetch", ": 1711111112:0;git pull", "plain-command", ""}, "\n")
		if err := os.WriteFile(histfile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		eq(t, readGlobalShellHistory("/bin/zsh"), []string{"plain-command", "git pull", "git fetch"}, "global zsh history")
	})

	tw(t, "bash-mode", "global history caches an unreadable file until its fingerprint changes", func(t *testing.T) {
		_, historyPath := bashTestEnv(t, nil)
		debugCalls := 0
		old := bashDebug
		bashDebug = func(string, ...any) { debugCalls++ }
		t.Cleanup(func() { bashDebug = old })

		if err := os.Mkdir(historyPath, 0o755); err != nil {
			t.Fatal(err)
		}
		eq(t, readGlobalShellHistory("/bin/zsh"), []string(nil), "directory as histfile")
		eq(t, readGlobalShellHistory("/bin/zsh"), []string(nil), "cached")
		eq(t, debugCalls, 1, "debug calls")

		if err := os.RemoveAll(historyPath); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(historyPath, []byte(": 1711111111:0;git status\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		eq(t, readGlobalShellHistory("/bin/zsh"), []string{"git status"}, "after the file changed")
	})

	tw(t, "bash-mode", "matchHistoryEntries returns newest entries when the prefix is empty", func(t *testing.T) {
		got := matchHistoryEntries([]string{"git stash", "git status", "git stash", "git fetch"}, "", 10)
		eq(t, got, []string{"git stash", "git status", "git fetch"}, "matches")
	})

	// Adaptation: the original writes theme.json beside its module (process.cwd()). A Go extension has no module directory, so
	// the documented agent path is the only theme file the port reads (theme.go themePaths).
	tw(t, "bash-mode", "theme.json can override icons without touching colors", func(t *testing.T) {
		bashTestEnv(t, nil)
		dir := filepath.Join(getAgentDir(), "extensions", "powerline-footer")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "theme.json"), []byte(`{"icons":{"auto":"↯","warning":""}}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		setenv(t, "POWERLINE_NERD_FONTS", s("0"))
		icons := getIcons()
		eq(t, icons.Auto, "↯", "auto icon")
		eq(t, icons.Warning, "", "warning icon")
		eq(t, icons.Folder, "dir", "folder icon")
	})

	tw(t, "bash-mode", "theme.json loads from the documented agent extension path", func(t *testing.T) {
		bashTestEnv(t, nil)
		dir := filepath.Join(getAgentDir(), "extensions", "powerline-footer")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "theme.json"), []byte(`{"colors":{"model":"#ff5500","path":"#ff5500"}}`), 0o644); err != nil {
			t.Fatal(err)
		}
		eq(t, resolveColor("model", colorScheme{}), "#ff5500", "model color")
		eq(t, resolveColor("path", colorScheme{}), "#ff5500", "path color")
	})

	tw(t, "bash-mode", "one-off bash command context strips ! and !! prefixes", func(t *testing.T) {
		eq(t, getOneOffBashCommandContext("!git status"), &oneOffBashCommandContext{Prefix: "!", Command: "git status", Offset: 1}, "bang")
		eq(t, getOneOffBashCommandContext("!!git status"), &oneOffBashCommandContext{Prefix: "!!", Command: "git status", Offset: 2}, "double bang")
		eq(t, getOneOffBashCommandContext("  !!git status"), (*oneOffBashCommandContext)(nil), "indented")
		eq(t, getOneOffBashCommandContext("git status"), (*oneOffBashCommandContext)(nil), "plain")
	})

	tw(t, "bash-mode", "transcript store truncates oldest commands at command boundaries", func(t *testing.T) {
		store := newBashTranscriptStore(bashTranscriptLimits{MaxLines: 3, MaxBytes: 1024})
		store.startCommand("a", "echo one", "/tmp")
		store.appendOutput("a", "line-1\nline-2")
		store.finishCommand("a", 0)

		store.startCommand("b", "echo two", "/tmp")
		store.appendOutput("b", "line-3\nline-4")
		store.finishCommand("b", 0)

		snapshot := store.getSnapshot()
		eq(t, len(snapshot.Commands), 1, "commands")
		eq(t, snapshot.Commands[0].ID, "b", "kept command")
		eq(t, snapshot.TruncatedCommands, 1, "truncated")
	})

	tw(t, "bash-mode", "transcript store keeps the active command even when it alone exceeds limits", func(t *testing.T) {
		store := newBashTranscriptStore(bashTranscriptLimits{MaxLines: 3, MaxBytes: 1024})
		store.startCommand("a", "echo big", "/tmp")
		store.appendOutput("a", "1\n2\n3\n4")

		snapshot := store.getSnapshot()
		eq(t, len(snapshot.Commands), 1, "commands")
		eq(t, snapshot.Commands[0].ID, "a", "kept command")
		eq(t, snapshot.Commands[0].Output, []string{"1", "2", "3", "4"}, "output")
	})

	tw(t, "bash-mode", "ghost suggestion prefers project history over global history", func(t *testing.T) {
		histfile := ": 1711111111:0;git switch\n"
		cwd, _ := bashTestEnv(t, &histfile)
		appendProjectHistory(cwd, "git status", cwd)
		appendProjectHistory(cwd, "git stash", cwd)
		wantGhost(t, ghost(t, "git st", cwd, "/bin/zsh"), "git stash", "project-history")
	})

	tw(t, "bash-mode", "ghost suggestion shows newest project history on an empty prompt", func(t *testing.T) {
		histfile := ": 1711111111:0;git pull\n"
		cwd, _ := bashTestEnv(t, &histfile)
		appendProjectHistory(cwd, "git status", cwd)
		appendProjectHistory(cwd, "git stash", cwd)
		wantGhost(t, ghost(t, "", cwd, "/bin/zsh"), "git stash", "project-history")
	})

	tw(t, "bash-mode", "ghost suggestion stays empty on an empty prompt when only global history exists", func(t *testing.T) {
		histfile := strings.Join([]string{": 1711111111:0;git fetch", ": 1711111112:0;git pull"}, "\n")
		cwd, _ := bashTestEnv(t, &histfile)
		eq(t, ghost(t, "", cwd, "/bin/zsh"), (*ghostSuggestion)(nil), "suggestion")
	})

	tw(t, "bash-mode", "ghost suggestion stays empty when the prompt is empty and no history exists", func(t *testing.T) {
		histfile := ""
		cwd, _ := bashTestEnv(t, &histfile)
		eq(t, ghost(t, "", cwd, "/bin/zsh"), (*ghostSuggestion)(nil), "suggestion")
	})

	tw(t, "bash-mode", "ghost suggestion can extend the current token from deterministic path completions", func(t *testing.T) {
		cwd, _ := bashTestEnv(t, nil)
		mustMkdir(t, filepath.Join(cwd, "dev"))
		mustMkdir(t, filepath.Join(cwd, "My Folder"))
		wantGhost(t, ghost(t, "cd d", cwd, "/bin/sh"), "cd dev/", "path")
		wantGhost(t, ghost(t, "cd M", cwd, "/bin/sh"), "cd My\\ Folder/", "path")
	})

	// Adaptation: the original replaces a getNativeSuggestions method on the engine with one that throws. The Go engine has
	// no such method to replace, so the case asserts what it protects: the answer comes from the deterministic path source
	// alone, and the engine type has no shell-native completion hook.
	tw(t, "bash-mode", "ghost suggestion does not invoke shell-native completion hooks", func(t *testing.T) {
		cwd, _ := bashTestEnv(t, nil)
		mustMkdir(t, filepath.Join(cwd, "dev"))
		if _, ok := reflect.TypeOf(&bashCompletionEngine{}).MethodByName("getNativeSuggestions"); ok {
			t.Fatal("the engine must not have a native completion hook")
		}
		wantGhost(t, ghost(t, "cd d", cwd, "/bin/zsh"), "cd dev/", "path")
	})

	tw(t, "bash-mode", "command-position ghost prefers the newest successful project-history command", func(t *testing.T) {
		histfile := ""
		cwd, _ := bashTestEnv(t, &histfile)
		appendProjectHistory(cwd, "git status", cwd)
		wantGhost(t, ghost(t, "g", cwd, "/bin/zsh"), "git status", "project-history")
	})

	tw(t, "bash-mode", "command-position ghost uses guarded global git history when project history is absent", func(t *testing.T) {
		histfile := ": 1711111111:0;git stash\n"
		cwd, _ := bashTestEnv(t, &histfile)
		wantGhost(t, ghost(t, "g", cwd, "/bin/zsh"), "git status", "git")
		wantGhost(t, ghost(t, "gi", cwd, "/bin/zsh"), "git stash", "global-history")
	})

	tw(t, "bash-mode", "command-position ghost falls back to git status when git is likely but history is absent", func(t *testing.T) {
		histfile := ""
		cwd, _ := bashTestEnv(t, &histfile)
		wantGhost(t, ghost(t, "g", cwd, "/bin/zsh"), "git status", "git")
	})

	tw(t, "bash-mode", "command-position ghost falls back to cd dot-dot for the cd stem", func(t *testing.T) {
		histfile := ""
		cwd, _ := bashTestEnv(t, &histfile)
		wantGhost(t, ghost(t, "c", cwd, "/bin/zsh"), "cd ..", "path")
	})

	tw(t, "bash-mode", "command-position ghost stays empty when there is no supported history-backed stem", func(t *testing.T) {
		histfile := ""
		cwd, _ := bashTestEnv(t, &histfile)
		eq(t, ghost(t, "x", cwd, "/bin/zsh"), (*ghostSuggestion)(nil), "suggestion")
	})

	tw(t, "bash-mode", "ghost suggestion ignores invalid raw global history and keeps a deterministic git candidate", func(t *testing.T) {
		histfile := ": 1711111111:0;git statis\n"
		cwd, _ := bashTestEnv(t, &histfile)
		got := ghost(t, "git st", cwd, "/bin/zsh")
		if got == nil || !regexp.MustCompile(`^git sta(?:sh|tus)$`).MatchString(got.Value) {
			t.Fatalf("suggestion = %+v, want git stash or git status", got)
		}
		eq(t, got.Source, "git", "source")
	})

	tw(t, "bash-mode", "global history boosts already-valid deterministic git candidates", func(t *testing.T) {
		histfile := ": 1711111111:0;git stash\n"
		cwd, _ := bashTestEnv(t, &histfile)
		wantGhost(t, ghost(t, "git st", cwd, "/bin/zsh"), "git stash", "git")
	})

	tw(t, "bash-mode", "deterministic path completion keeps directory suffixes for escaped paths", func(t *testing.T) {
		histfile := ""
		cwd, _ := bashTestEnv(t, &histfile)
		mustMkdir(t, filepath.Join(cwd, "My Folder"))
		wantGhost(t, ghost(t, "cd M", cwd, "/bin/zsh"), "cd My\\ Folder/", "path")
	})

	tw(t, "bash-mode", "deterministic path completion handles bash argument position", func(t *testing.T) {
		cwd, _ := bashTestEnv(t, nil)
		mustMkdir(t, filepath.Join(cwd, "devdir"))
		wantGhost(t, ghost(t, "cd d", cwd, "/bin/bash"), "cd devdir/", "path")
	})

	tw(t, "bash-mode", "managed shell session preserves cwd changes across commands", func(t *testing.T) {
		shellPath := resolveManagedShellPath()
		if shellPath == "" {
			t.Skip("requires zsh or bash")
		}
		cwd := t.TempDir()
		childDir := filepath.Join(cwd, "child")
		mustMkdir(t, childDir)
		store := newBashTranscriptStore(bashTranscriptLimits{MaxLines: 100, MaxBytes: 64 * 1024})
		session, err := newManagedShellSession(shellPath, cwd, store, func() {}, func(string, string) {})
		if err != nil {
			t.Fatal(err)
		}
		defer session.dispose()
		ctx := context.Background()

		if err := session.ensureReady(ctx); err != nil {
			t.Fatal(err)
		}
		if err := session.runCommand(ctx, "cd "+childDir); err != nil {
			t.Fatal(err)
		}
		waitForShellCommand(t, session)
		eq(t, session.State().Cwd, childDir, "cwd after cd")

		if err := session.runCommand(ctx, "pwd"); err != nil {
			t.Fatal(err)
		}
		waitForShellCommand(t, session)
		snapshot := store.getSnapshot()
		last := snapshot.Commands[len(snapshot.Commands)-1]
		found := false
		for _, line := range last.Output {
			found = found || line == childDir
		}
		if !found {
			t.Errorf("pwd output %q lacks %q", last.Output, childDir)
		}
	})

	tw(t, "bash-mode", "managed shell session recovers cleanly after interrupt", func(t *testing.T) {
		shellPath := resolveManagedShellPath()
		if shellPath == "" {
			t.Skip("requires zsh or bash")
		}
		cwd := t.TempDir()
		store := newBashTranscriptStore(bashTranscriptLimits{MaxLines: 100, MaxBytes: 64 * 1024})
		session, err := newManagedShellSession(shellPath, cwd, store, func() {}, func(string, string) {})
		if err != nil {
			t.Fatal(err)
		}
		defer session.dispose()
		ctx := context.Background()

		if err := session.ensureReady(ctx); err != nil {
			t.Fatal(err)
		}
		if err := session.runCommand(ctx, "sleep 5"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
		session.interrupt()
		waitForShellCommand(t, session)

		interrupted := store.getSnapshot().Commands[0]
		if interrupted.ExitCode == nil || *interrupted.ExitCode != 130 {
			t.Fatalf("interrupted exit code = %v, want 130", interrupted.ExitCode)
		}

		if err := session.runCommand(ctx, "printf 'after\\n'"); err != nil {
			t.Fatal(err)
		}
		waitForShellCommand(t, session)
		snapshot := store.getSnapshot()
		last := snapshot.Commands[len(snapshot.Commands)-1]
		eq(t, last.Command, "printf 'after\\n'", "command")
		if last.ExitCode == nil || *last.ExitCode != 0 {
			t.Errorf("exit code = %v, want 0", last.ExitCode)
		}
		found := false
		for _, line := range last.Output {
			found = found || line == "after"
		}
		if !found {
			t.Errorf("output %q lacks after", last.Output)
		}
	})

	tw(t, "bash-mode", "one-off bash autocomplete provider stays inactive even inside bang commands", func(t *testing.T) {
		provider := newOneOffBashAutocompleteProvider()
		suggestions, err := provider.GetSuggestions(sdk.Context{}, []string{"!!gi"}, 0, 4, false)
		if err != nil || suggestions != nil {
			t.Errorf("suggestions = %v, %v, want nil", suggestions, err)
		}
	})

	tw(t, "bash-mode", "bash autocomplete providers return null in shell contexts", func(t *testing.T) {
		bash, err := newBashAutocompleteProvider().GetSuggestions(sdk.Context{}, []string{"git st"}, 0, 6, false)
		if err != nil || bash != nil {
			t.Errorf("bash suggestions = %v, %v, want nil", bash, err)
		}
		oneOff, err := newOneOffBashAutocompleteProvider().GetSuggestions(sdk.Context{}, []string{"!git st"}, 0, 7, false)
		if err != nil || oneOff != nil {
			t.Errorf("one-off suggestions = %v, %v, want nil", oneOff, err)
		}
	})

	tw(t, "bash-mode", "mode-aware autocomplete provider preserves default results", func(t *testing.T) {
		result := &sdk.AutocompleteSuggestions{Items: []sdk.AutocompleteItem{{Value: "status", Label: "status"}}, Prefix: "st"}
		provider := newModeAwareAutocompleteProvider(
			&sdk.AutocompleteProvider{
				GetSuggestions: func(sdk.Context, []string, int, int, bool) (*sdk.AutocompleteSuggestions, error) {
					return result, nil
				},
				ApplyCompletion: func(_ sdk.Context, lines []string, cursorLine, cursorCol int, _ sdk.AutocompleteItem, _ string) (sdk.AutocompleteCompletion, error) {
					return sdk.AutocompleteCompletion{Lines: lines, CursorLine: cursorLine, CursorCol: cursorCol}, nil
				},
			},
			newBashAutocompleteProvider(),
			newOneOffBashAutocompleteProvider(),
			func() bool { return false },
		)
		suggestions, err := provider.GetSuggestions(sdk.Context{}, []string{"st"}, 0, 2, false)
		if err != nil || suggestions != result {
			t.Errorf("suggestions = %p, %v, want the default provider's result %p", suggestions, err, result)
		}
	})

	tw(t, "bash-mode", "one-off bash autocomplete provider stays inactive before the bang command starts", func(t *testing.T) {
		provider := newOneOffBashAutocompleteProvider()
		trigger, err := provider.ShouldTriggerFileCompletion(sdk.Context{}, []string{"!git status"}, 0, 0)
		if err != nil || trigger {
			t.Errorf("shouldTriggerFileCompletion = %v, %v, want false", trigger, err)
		}
		suggestions, err := provider.GetSuggestions(sdk.Context{}, []string{"!git status"}, 0, 0, false)
		if err != nil || suggestions != nil {
			t.Errorf("suggestions = %v, %v, want nil", suggestions, err)
		}
	})

	// Adaptations of the cases below that name the editor: the original calls BashModeEditor.prototype methods on a stub
	// `this`. The Go port has no BashModeEditor (no SDK editor component), so each case runs the ported logic the method is made
	// of (ghostUpdater, shellHistoryBrowser, acceptGhostSuggestion) with the same inputs and expectations.
	tw(t, "bash-mode", "bash editor refreshGhostSuggestion reuses the ghost scheduling path", func(t *testing.T) {
		resolved := make(chan string, 1)
		updater := newGhostUpdater(func(_ context.Context, text string) (*ghostSuggestion, error) {
			resolved <- text
			return nil, nil
		}, nil)
		updater.schedule(true, "git")
		select {
		case text := <-resolved:
			eq(t, text, "git", "scheduled text")
		case <-time.After(2 * time.Second):
			t.Fatal("refresh with completions enabled did not schedule a ghost update")
		}
	})

	tw(t, "bash-mode", "bash editor refreshGhostSuggestion clears ghosts when completions are disabled", func(t *testing.T) {
		scheduled := make(chan struct{}, 1)
		updater := newGhostUpdater(func(context.Context, string) (*ghostSuggestion, error) {
			scheduled <- struct{}{}
			return &ghostSuggestion{Value: "git status", Source: bashSourceProjectHistory}, nil
		}, nil)
		updater.mu.Lock()
		updater.ghost = &ghostSuggestion{Value: "git status", Source: bashSourceProjectHistory}
		updater.mu.Unlock()
		updater.schedule(false, "git")
		eq(t, updater.current(), (*ghostSuggestion)(nil), "ghost after a disabled refresh")
		select {
		case <-scheduled:
			t.Fatal("a disabled refresh must not schedule a ghost update")
		case <-time.After(3 * ghostUpdateDebounce):
		}
	})

	tw(t, "bash-mode", "bash editor shell history state does not clobber the base prompt history index", func(t *testing.T) {
		// The editor's prompt history index (historyIndex) and the shell history browsing state are separate fields; the
		// Go browser is its own value, so the case proves that stepping it moves only its own index and asks for the text.
		promptHistoryIndex := 5
		browser := newShellHistoryBrowser()
		step := browser.navigate(-1, "git st", func(string) []string { return []string{"git stash", "git status"} })
		eq(t, promptHistoryIndex, 5, "prompt history index")
		eq(t, browser.index, 0, "shell history index")
		if step.SetText == nil || *step.SetText != "git stash" {
			t.Errorf("step.SetText = %v, want git stash", step.SetText)
		}
	})

	tw(t, "bash-mode", "bash editor does not accept a hidden ghost suggestion when the cursor is not at the end", func(t *testing.T) {
		text, accepted := acceptGhostSuggestion(&ghostSuggestion{Value: "git status", Source: bashSourceProjectHistory}, "git st", 0, 3)
		if accepted {
			t.Errorf("accepted %q with the cursor inside the text", text)
		}
	})

	tskip(t, "bash-mode", "bash editor Tab accepts the current ghost suggestion without opening autocomplete", "handleInput's Tab branch of BashModeEditor (CustomEditor subclass, keybinding tui.input.tab): needs Context.SetEditorComponent and keybinding lookup, neither in the Go SDK; the accept rule it calls is twinned by the hidden-ghost case")
	tskip(t, "bash-mode", "bash editor does not submit pasted multiline input while bracketed paste is active", "handleInput delegates to Pi's CustomEditor.handleInput while isInPaste is set (prototype patched in the original): needs the editor component and Pi's paste state, absent from the Go SDK")
	tskip(t, "bash-mode", "bash editor refreshes shell ghost state after a bracketed paste completes", "handleInput's post-paste ghost refresh runs after CustomEditor.handleInput clears its isInPaste flag: editor-internal state the SDK cannot reach")
	tskip(t, "bash-mode", "bash editor inserts Finder file drops as path strings", "constructs BashModeEditor over Pi's pi-tui Editor and KeybindingsManager and reads getText() after bracketed pastes; the drop decoding itself (droppedPathTextFromInput) is covered by TestBashDroppedPathText")
	tskip(t, "bash-mode", "bash editor hot path avoids full expansion and coalesces ghost work", "asserts BashModeEditor.insertCharacter fast path, onExtensionShortcut and getExpandedText never being called while typing: editor internals; the 50 ms debounce and coalescing of ghost work is covered by TestGhostUpdaterCoalescesUpdates")
	tskip(t, "bash-mode", "bash editor long ASCII backspace keeps undo without scanning the full line", "needs Pi's Editor undo stack, state.lines and pushUndoSnapshot to prove undo survives the 5000-character backspace fast path; the text rule is covered by TestFastASCIIEdits")
	tskip(t, "bash-mode", "bash editor long ASCII backspace preserves custom app bindings by input sequence", "needs KeybindingsManager with a user binding app.clear=ctrl+h and editor.onAction dispatch (Pi keybinding lookup is not in the Go SDK)")
	tskip(t, "bash-mode", "bash editor long ASCII forward delete keeps undo without scanning the full line", "needs Pi's Editor undo stack to prove undo survives the forward-delete fast path on a 5000-character line; the text rule is covered by TestFastASCIIEdits")
	tskip(t, "bash-mode", "bash editor long ASCII forward delete preserves custom app bindings by input sequence", "needs KeybindingsManager with a user binding for the delete-forward key sequence and editor.onAction dispatch (no keybinding lookup in the Go SDK)")
	tskip(t, "bash-mode", "bash editor long ASCII horizontal movement avoids visual remapping", "asserts Pi's Editor.buildVisualLineMap is not called while moving the cursor over a long ASCII line: editor internals; the cursor rule is covered by TestFastASCIIEdits")
	tskip(t, "bash-mode", "bash editor long ASCII horizontal movement preserves custom app bindings by input sequence", "needs KeybindingsManager with a user binding on the arrow key sequence and editor.onAction dispatch (no keybinding lookup in the Go SDK)")
	tskip(t, "bash-mode", "bash editor horizontal fast path resets shell history browsing", "drives up/left/down through BashModeEditor.handleInput with Pi keybindings and reads its private shellHistoryIndex; the browser's reset is covered by TestShellHistoryBrowser")
	tskip(t, "bash-mode", "bash editor horizontal fast path falls back with active paste markers", "needs Pi's Editor paste markers (pastes map, [paste #n] placeholders) to see the fast path decline: editor state absent from the Go SDK")
	tskip(t, "bash-mode", "bash editor horizontal fast path falls back at mixed Unicode boundaries", "runs the arrow keys through BashModeEditor.handleInput and Pi's grapheme-aware cursor movement; the ASCII-neighbour rule is covered by TestFastASCIIEdits")
	tskip(t, "bash-mode", "bash editor fast path preserves plain custom keybindings", "needs KeybindingsManager.setUserBindings, pi-tui's global setKeybindings and BashModeEditor.plainBoundInputs: keybinding lookup and editor component are absent from the Go SDK")
	tskip(t, "bash-mode", "bash editor dismiss clears autocomplete when mode turns off", "dismissBashModeUi calls Pi Editor's cancelAutocomplete and tui.requestRender on the editor instance; the ghost clear and history reset it performs are covered by TestGhostUpdaterCoalescesUpdates and TestShellHistoryBrowser")
	tskip(t, "bash-mode", "bash editor recalls prompt history from single-line end without losing the live draft", "BashModeEditor up/down prompt-history recall over Pi's Editor history, historyIndex, isOnFirstVisualLine and navigateHistory: editor internals absent from the Go SDK")
	tskip(t, "bash-mode", "bash editor escape exits bash mode", "handleInput's app.interrupt branch (KeybindingsManager.matches) calling onExitBashMode: needs the editor component and keybinding lookup; the mode switch it triggers is covered by TestBashModeToggle")
	tskip(t, "bash-mode", "bash editor right arrow accepts an empty-prompt ghost suggestion without submitting", "handleInput's tui.editor.cursorRight branch with a stub keybinding matcher: needs the editor component and keybinding lookup; accepting itself is covered by TestAcceptGhostSuggestion")
	tskip(t, "bash-mode", "bash editor right arrow accepts ghost text for one-off bang commands", "handleInput's cursorRight branch in a one-off bang line (isOneOffBashCommandContext on editor state.lines): needs the editor component; the bang detection is covered by the one-off context twin")
	tskip(t, "bash-mode", "bash editor runs copied Pi app action handlers for alt-enter", "needs CustomEditor.actionHandlers, pi-tui setKittyProtocolActive and the Alt+Enter key encodings routed by Pi's editor: editor component absent from the Go SDK")
	tskip(t, "bash-mode", "bash editor command-z undoes deleted text for supported encodings only", "handleInput's Command+Z branch calling Pi Editor.undo over a real editor buffer; the encoding list is covered by TestIsCommandUndoShortcut")
	tskip(t, "bash-mode", "bash editor command-z resets shell history and updates ghost state", "handleInput's Command+Z branch resetting shellHistory* fields and calling scheduleGhostUpdate on the editor instance: editor component absent from the Go SDK")
	tskip(t, "bash-mode", "bash editor command arrows jump to editor boundaries", "moveCursorToEditorBoundary writes Pi Editor's private state.cursorLine/cursorCol, lastAction and preferredVisualCol and uses matchesConfiguredShortcut on terminal key sequences: editor internals")
	tskip(t, "bash-mode", "bash editor enter does not accept ghost text while a shell command is running", "handleInput's tui.input.submit branch (running shell warns 'Shell command already running'): needs the editor component and keybinding lookup; the running-state guard is covered by TestBashRunShellCommandWhileRunning")
	tskip(t, "bash-mode", "bash editor enter submits the typed command without accepting ghost text", "handleInput's submit branch reading getExpandedText and calling onSubmitCommand/setText on the editor instance: needs the editor component")
	tskip(t, "bash-mode", "one-off bang submit does not accept ghost text before submitting", "handleInput delegating a one-off bang submit to CustomEditor.handleInput (prototype patched in the original): needs the editor component")
	tskip(t, "bash-mode", "bash editor submit clears the prompt and refreshes the empty ghost suggestion", "handleInput's submit branch calling setText('') and refreshGhostSuggestion on the editor instance, with shellHistory* reset: needs the editor component")
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}
