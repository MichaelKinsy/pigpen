package pigmodeltweaks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// These tests pin the behaviour the pi-tweaks original has and the first port
// lacked: the model-guard command's texts and pickers, the session_start hint,
// remember-model's texts and deferred toast, and the provider-lock command's
// picker. Each expectation is the original's (port/oracle/extensions/*.ts); the
// command names and the settings file name are this port's, which the
// equivalence run swaps for the original's (port/eqcopy.sh).

// parityHost is a host with an agent directory, a model registry answering the
// given models as available, and a scripted answer to a focused component.
type parityHost struct {
	*Host
	home string
	// customCalls counts ui.custom opens.
	customCalls func() int
}

type pickerAnswer func() any

func seedSettings(t *testing.T, home string, document any) {
	t.Helper()
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "agent", settingsFileName), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func guardSettings(enabled bool, allowed ...modelRef) map[string]any {
	list := make([]map[string]string, 0, len(allowed))
	for _, model := range allowed {
		list = append(list, map[string]string{"provider": model.Provider, "model": model.Model})
	}
	return map[string]any{"version": 1, "modelGuard": map[string]any{"enabled": enabled, "allowedModels": list}}
}

// startParityHost starts the extension in mode with the models registered as
// available. answer, when set, is what the host returns for a ui.custom call.
func startParityHost(t *testing.T, mode string, settings any, available []modelRef, answer pickerAnswer) *parityHost {
	t.Helper()
	home := lockHome(t)
	if settings != nil {
		seedSettings(t, home, settings)
	}
	models := make([]map[string]any, 0, len(available))
	providers := map[string]any{}
	for _, model := range available {
		models = append(models, map[string]any{"provider": model.Provider, "id": model.Model})
		providers[model.Provider] = map[string]any{"configured": true}
	}
	opens := 0
	host := StartHost(t, Extension(), HostOptions{Mode: mode, OnCall: func(method string, args map[string]any) (map[string]any, string) {
		switch method {
		case "getModelRegistryState":
			return map[string]any{"models": models, "providers": providers}, ""
		case "ui.custom":
			opens++
			if answer == nil {
				return map[string]any{}, ""
			}
			return map[string]any{"ok": true, "result": answer()}, ""
		}
		return map[string]any{}, ""
	}})
	return &parityHost{Host: host, home: home, customCalls: func() int { return opens }}
}

func (p *parityHost) start(t *testing.T, reason string) {
	t.Helper()
	if failure := fire(p.Host, "session_start", map[string]any{"reason": reason}); failure != "" {
		t.Fatalf("session_start: %s", failure)
	}
}

type note struct{ message, level string }

func notes(h *Host) []note {
	var out []note
	for _, call := range h.CallsTo("ui.notify") {
		message, _ := call.Args["message"].(string)
		level, _ := call.Args["level"].(string)
		out = append(out, note{message, level})
	}
	return out
}

func (p *parityHost) run(t *testing.T, command, args string) []note {
	t.Helper()
	before := len(notes(p.Host))
	if failure := p.Command(command, args); failure != "" {
		t.Fatalf("/%s %s: %s", command, args, failure)
	}
	return notes(p.Host)[before:]
}

func wantNotes(t *testing.T, got []note, want ...note) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("notifications = %q, want %q", got, want)
	}
}

func info(message string) note    { return note{message, "info"} }
func warning(message string) note { return note{message, "warning"} }

// ---- session_start hint (pt-model-guard.ts session_start) ----

func TestSessionStartHintsWhenTheGuardIsOnWithoutPreferences(t *testing.T) {
	p := startParityHost(t, "tui", nil, nil, nil)
	p.start(t, "startup")
	wantNotes(t, notes(p.Host), info("💡 No model preferences set. Run /"+modelGuardName+" to choose preferred models."))
}

func TestSessionStartHintsOnEveryStartReason(t *testing.T) {
	p := startParityHost(t, "tui", nil, nil, nil)
	p.start(t, "new")
	p.start(t, "resume")
	if got := len(notes(p.Host)); got != 2 {
		t.Fatalf("hints = %d, want one per session_start", got)
	}
}

func TestSessionStartIsQuietWhenTheGuardIsOffOrHasPreferences(t *testing.T) {
	off := startParityHost(t, "tui", guardSettings(false), nil, nil)
	off.start(t, "startup")
	if got := notes(off.Host); len(got) != 0 {
		t.Fatalf("a disabled guard hinted: %q", got)
	}
	on := startParityHost(t, "tui", guardSettings(true, modelRef{"acme", "m1"}), nil, nil)
	on.start(t, "startup")
	if got := notes(on.Host); len(got) != 0 {
		t.Fatalf("a guard with preferences hinted: %q", got)
	}
}

// ---- guard texts (pt-model-guard.ts handler) ----

func TestGuardOnOffToggleSayEnabledOrDisabled(t *testing.T) {
	p := startParityHost(t, "tui", guardSettings(true, modelRef{"acme", "m1"}), nil, nil)
	p.start(t, "resume")
	wantNotes(t, p.run(t, modelGuardName, "off"), info("Model guard disabled"))
	wantNotes(t, p.run(t, modelGuardName, "on"), info("Model guard enabled"))
	wantNotes(t, p.run(t, modelGuardName, "toggle"), info("Model guard disabled"))
	wantNotes(t, p.run(t, modelGuardName, "TOGGLE "), info("Model guard enabled"))
}

func TestGuardListTexts(t *testing.T) {
	p := startParityHost(t, "tui", guardSettings(true), nil, nil)
	wantNotes(t, p.run(t, modelGuardName, "list"), info("No model preferences saved. All models allowed."))

	q := startParityHost(t, "tui", guardSettings(true, modelRef{"acme", "m1"}, modelRef{"zed", "a/b"}), nil, nil)
	wantNotes(t, q.run(t, modelGuardName, "list"), info("Guard: ON\nAllowed models:\n  • acme/m1\n  • zed/a/b"))
	q.run(t, modelGuardName, "off")
	wantNotes(t, q.run(t, modelGuardName, "list"), info("Guard: OFF\nAllowed models:\n  • acme/m1\n  • zed/a/b"))
}

// ---- add / remove / bare with the multi-select (showModelPicker) ----

var catalog = []modelRef{{"zed", "m2"}, {"acme", "m1"}, {"acme", "m3"}}

func TestPickersWarnWhenNoModelHasAKey(t *testing.T) {
	for _, sub := range []string{"add", "remove", ""} {
		p := startParityHost(t, "tui", nil, nil, nil)
		wantNotes(t, p.run(t, modelGuardName, sub), warning("No models available. Check your API keys."))
		if p.customCalls() != 0 {
			t.Fatalf("/%s opened a picker with nothing to pick", sub)
		}
	}
}

func TestPickerCancelledSaysCancelled(t *testing.T) {
	for _, sub := range []string{"add", "remove"} {
		p := startParityHost(t, "tui", guardSettings(true, modelRef{"acme", "m1"}), catalog, nil)
		wantNotes(t, p.run(t, modelGuardName, sub), info("Cancelled"))
		if p.customCalls() != 1 {
			t.Fatalf("/%s opened %d pickers, want 1", sub, p.customCalls())
		}
	}
}

func TestBareCommandNamesFirstTimeAndCancels(t *testing.T) {
	p := startParityHost(t, "tui", nil, catalog, nil)
	wantNotes(t, p.run(t, modelGuardName, ""),
		info("No preferences set yet. Pick your preferred models:"), info("Cancelled"))
	q := startParityHost(t, "tui", guardSettings(true, modelRef{"acme", "m1"}), catalog, nil)
	wantNotes(t, q.run(t, modelGuardName, ""), info("Cancelled"))
}

func TestAddKeepsOnlyTheNewlyCheckedModels(t *testing.T) {
	p := startParityHost(t, "tui", guardSettings(true, modelRef{"acme", "m1"}), catalog,
		func() any { return []string{"acme/m1", "zed/m2", "acme/m3"} })
	wantNotes(t, p.run(t, modelGuardName, "add"), info("Added 2 model(s): zed/m2, acme/m3"))
	wantNotes(t, p.run(t, modelGuardName, "list"), info("Guard: ON\nAllowed models:\n  • acme/m1\n  • zed/m2\n  • acme/m3"))
}

func TestAddWithNothingNewSaysSo(t *testing.T) {
	p := startParityHost(t, "tui", guardSettings(true, modelRef{"acme", "m1"}), catalog,
		func() any { return []string{"acme/m1"} })
	wantNotes(t, p.run(t, modelGuardName, "add"), info("No new models added"))
}

func TestRemoveKeepsWhatStaysChecked(t *testing.T) {
	p := startParityHost(t, "tui", guardSettings(true, modelRef{"acme", "m1"}, modelRef{"zed", "m2"}), catalog,
		func() any { return []string{"zed/m2"} })
	wantNotes(t, p.run(t, modelGuardName, "remove"), info("Removed 1 model(s)"))
	wantNotes(t, p.run(t, modelGuardName, "list"), info("Guard: ON\nAllowed models:\n  • zed/m2"))

	q := startParityHost(t, "tui", guardSettings(true, modelRef{"acme", "m1"}), catalog,
		func() any { return []string{"acme/m1"} })
	wantNotes(t, q.run(t, modelGuardName, "remove"), info("No models removed"))
}

func TestBareCommandSetsTheListToTheSelectionInPickerOrder(t *testing.T) {
	p := startParityHost(t, "tui", nil, catalog,
		func() any { return []string{"zed/m2", "acme/m3"} })
	wantNotes(t, p.run(t, modelGuardName, ""),
		info("No preferences set yet. Pick your preferred models:"),
		info("Updated preferences: 2 model(s) allowed"))
	wantNotes(t, p.run(t, modelGuardName, "list"), info("Guard: ON\nAllowed models:\n  • zed/m2\n  • acme/m3"))
}

func TestBareCommandWithNothingSelected(t *testing.T) {
	// First time: an empty confirmation changes nothing.
	p := startParityHost(t, "tui", nil, catalog, func() any { return []string{} })
	wantNotes(t, p.run(t, modelGuardName, ""),
		info("No preferences set yet. Pick your preferred models:"),
		info("No models selected. All models remain allowed."))
	// Later: an empty confirmation clears the list.
	q := startParityHost(t, "tui", guardSettings(true, modelRef{"acme", "m1"}), catalog, func() any { return []string{} })
	wantNotes(t, q.run(t, modelGuardName, ""), info("Updated preferences: 0 model(s) allowed"))
	wantNotes(t, q.run(t, modelGuardName, "list"), info("No model preferences saved. All models allowed."))
}

func TestModelValuesSplitOnTheFirstSlash(t *testing.T) {
	p := startParityHost(t, "tui", nil, []modelRef{{"openrouter", "vendor/model"}},
		func() any { return []string{"openrouter/vendor/model"} })
	p.run(t, modelGuardName, "")
	wantNotes(t, p.run(t, modelGuardName, "list"), info("Guard: ON\nAllowed models:\n  • openrouter/vendor/model"))
	state := readSettings(filepath.Join(p.home, "agent", settingsFileName))
	if got := state.ModelGuard.AllowedModels; len(got) != 1 || got[0] != (modelRef{"openrouter", "vendor/model"}) {
		t.Fatalf("allowed = %v, want openrouter + vendor/model", got)
	}
}

// ---- the multi-select component (showModelPicker's ctx.ui.custom) ----

func press(t *testing.T, component sdk.RemoteComponent, keys ...string) sdk.RemoteComponentResult {
	t.Helper()
	var result sdk.RemoteComponentResult
	for _, data := range keys {
		var err error
		if result, err = component.HandleInput(data); err != nil {
			t.Fatal(err)
		}
	}
	return result
}

// asStrings reads a picker result without panicking on a missing one.
func asStrings(value any) []string {
	list, _ := value.([]string)
	return list
}

func TestPickerOrdersCheckedFirstThenByLocaleCompare(t *testing.T) {
	picker := newGuardPicker("T", []modelRef{{"zed", "m2"}, {"acme", "m3"}, {"acme", "m1"}, {"Acme", "m9"}}, []string{"zed/m2"})
	got := pickerValues(picker)
	want := []string{"zed/m2", "acme/m1", "acme/m3", "Acme/m9"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v (checked first, then ICU-style collation: a before A, punctuation before digits)", got, want)
	}
}

func TestLocaleCompareMatchesICUOrderForModelIds(t *testing.T) {
	// Node's String.prototype.localeCompare: punctuation sorts before digits
	// before letters, "_" < "-" < "." < "/", lowercase before uppercase.
	ordered := []string{"a_b", "a-b", "a.b", "a/b", "a1", "ab", "aB", "b"}
	for i := 1; i < len(ordered); i++ {
		if localeCompare(ordered[i-1], ordered[i]) >= 0 || localeCompare(ordered[i], ordered[i-1]) <= 0 {
			t.Fatalf("localeCompare(%q, %q) is not ordered", ordered[i-1], ordered[i])
		}
	}
	if localeCompare("same", "same") != 0 {
		t.Fatal("equal strings compare equal")
	}
}

func TestPickerRendersTheOriginalFrame(t *testing.T) {
	picker := newGuardPicker("Select your PREFERRED models", catalog, []string{"acme/m1"})
	lines := picker.Render(60)
	want := []string{
		strings.Repeat("─", 60),
		" Select your PREFERRED models",
		" 🔍 _",
		" 1/3 selected, 3 shown",
		"",
		"> ☑ acme/m1",
		"  ☐ acme/m3",
		"  ☐ zed/m2",
		"",
		"  Type to search • Space toggle • a=all • n=none • Enter confirm • Esc cancel",
		strings.Repeat("─", 60),
	}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("frame:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}

func TestPickerMarksAModelWithoutAKey(t *testing.T) {
	picker := newGuardPicker("T", catalog, nil)
	if len(picker.items) < 2 {
		t.Fatal("picker has no rows")
	}
	picker.items[1].available = false
	joined := strings.Join(picker.Render(60), "\n")
	if !strings.Contains(joined, "☐ acme/m3 ⚠ no key") {
		t.Fatalf("no key marker in:\n%s", joined)
	}
}

func TestPickerWrapsALongLabelUnderItsPrefix(t *testing.T) {
	// 20 columns, a 2-column prefix: "☐ acme/a-very-long-model-name-indeed" wraps at 18.
	picker := newGuardPicker("T", []modelRef{{"acme", "a-very-long-model-name-indeed"}}, nil)
	lines := picker.Render(20)
	if len(lines) < 7 {
		t.Fatalf("frame = %q", lines)
	}
	rows := lines[5:]
	want := []string{"> ☐ acme/a-very-long-", "  model-name-indeed"}
	if len(rows) < 2 || !reflect.DeepEqual(rows[:2], want) {
		t.Fatalf("rows = %q, want the label wrapped to %q", rows, want)
	}
}

func TestPickerSpaceTogglesAndAdvances(t *testing.T) {
	picker := newGuardPicker("T", catalog, nil)
	press(t, picker, " ", " ")
	done := press(t, picker, "\r")
	if !done.Done || !reflect.DeepEqual(done.Value, []string{"acme/m1", "acme/m3"}) {
		t.Fatalf("result = %+v, want the first two rows, which Space checked and stepped over", done)
	}
}

func TestPickerSpaceOnTheLastRowStays(t *testing.T) {
	picker := newGuardPicker("T", catalog, nil)
	press(t, picker, "\x1b[B", "\x1b[B", " ", " ")
	done := press(t, picker, "\r")
	if got := done.Value; got == nil || len(asStrings(got)) != 0 {
		t.Fatalf("result = %v, want nothing: the second Space on the last row unchecks it", got)
	}
}

func TestPickerSearchFiltersAndEscapeClearsThenCancels(t *testing.T) {
	picker := newGuardPicker("T", catalog, nil)
	press(t, picker, "z", "e")
	frame := strings.Join(picker.Render(60), "\n")
	if !strings.Contains(frame, " 🔍 ze_") || !strings.Contains(frame, "0/3 selected, 1 shown") || strings.Contains(frame, "acme/m1") {
		t.Fatalf("search frame:\n%s", frame)
	}
	if done := press(t, picker, "\x1b"); done.Done {
		t.Fatal("Esc with a search cancelled instead of clearing it")
	}
	if !strings.Contains(strings.Join(picker.Render(60), "\n"), "3 shown") {
		t.Fatal("Esc did not clear the search")
	}
	if done := press(t, picker, "\x1b"); !done.Done || done.Value != nil {
		t.Fatalf("second Esc = %+v, want a cancel (nil value)", done)
	}
}

func TestPickerBackspaceEditsTheSearch(t *testing.T) {
	picker := newGuardPicker("T", catalog, nil)
	press(t, picker, "a", "c", "x", "\x7f")
	if !strings.Contains(strings.Join(picker.Render(60), "\n"), " 🔍 ac_") {
		t.Fatal("backspace did not remove the last search character")
	}
}

func TestPickerAllAndNoneApplyToTheFilteredRowsOnlyWithoutASearch(t *testing.T) {
	picker := newGuardPicker("T", catalog, nil)
	press(t, picker, "a")
	if got := press(t, picker, "\r").Value; !reflect.DeepEqual(got, []string{"acme/m1", "acme/m3", "zed/m2"}) {
		t.Fatalf("after a = %v", got)
	}
	picker = newGuardPicker("T", catalog, []string{"acme/m1", "zed/m2"})
	press(t, picker, "n")
	if got := press(t, picker, "\r").Value; len(asStrings(got)) != 0 {
		t.Fatalf("after n = %v, want none", got)
	}
	// With a search typed, `a` is a search character, not select-all.
	picker = newGuardPicker("T", catalog, nil)
	press(t, picker, "m", "a")
	if got := press(t, picker, "\r").Value; len(asStrings(got)) != 0 {
		t.Fatalf("a typed into a search selected %v", got)
	}
}

func TestPickerSearchTypedSpaceIsNotASearchCharacter(t *testing.T) {
	picker := newGuardPicker("T", catalog, nil)
	press(t, picker, "a", " ")
	if !strings.Contains(strings.Join(picker.Render(60), "\n"), " 🔍 a_") {
		t.Fatal("Space went into the search instead of toggling")
	}
}

func TestPickerResultIsInRowOrderNotSelectionOrder(t *testing.T) {
	picker := newGuardPicker("T", catalog, nil)
	press(t, picker, "\x1b[B", "\x1b[B", " ")
	press(t, picker, "\x1b[A", "\x1b[A", " ")
	if got := press(t, picker, "\r").Value; !reflect.DeepEqual(got, []string{"acme/m1", "zed/m2"}) {
		t.Fatalf("result = %v, want row order", got)
	}
}

func TestPickerScrollsTwentyRowsWithAnIndicator(t *testing.T) {
	models := make([]modelRef, 0, 30)
	for i := 0; i < 30; i++ {
		models = append(models, modelRef{"p", "m" + string(rune('a'+i%26)) + string(rune('a'+i/26))})
	}
	picker := newGuardPicker("T", models, nil)
	frame := strings.Join(picker.Render(60), "\n")
	if !strings.Contains(frame, " 1-20 of 30") {
		t.Fatalf("no scroll indicator in:\n%s", frame)
	}
	for i := 0; i < 21; i++ {
		press(t, picker, "\x1b[B")
	}
	if frame = strings.Join(picker.Render(60), "\n"); !strings.Contains(frame, " 3-22 of 30") {
		t.Fatalf("scrolled frame has no 3-22 indicator:\n%s", frame)
	}
}

// ---- remember-model texts and the deferred toast (pt-remember-model.ts) ----

func TestRememberModelCommandTexts(t *testing.T) {
	p := startParityHost(t, "tui", nil, nil, nil)
	p.start(t, "resume")
	wantNotes(t, p.run(t, rememberModelName, ""), info("remember-model: on\nlast: (none)\nUsage: /"+rememberModelName+" [on|off|clear]"))
	wantNotes(t, p.run(t, rememberModelName, "off"), info("remember-model disabled"))
	wantNotes(t, p.run(t, rememberModelName, "ON"), info("remember-model enabled"))
	wantNotes(t, p.run(t, rememberModelName, "clear"), info("Cleared remembered model"))
}

func TestRememberModelStatusNamesTheLastModel(t *testing.T) {
	p := startParityHost(t, "tui", map[string]any{"version": 1, "rememberModel": map[string]any{
		"enabled": true, "last": map[string]any{"provider": "acme", "modelId": "m1"}}}, nil, nil)
	wantNotes(t, p.run(t, rememberModelName, ""),
		info("remember-model: on\nlast: acme/m1\nUsage: /"+rememberModelName+" [on|off|clear]"))
}

// The original defers its toast one tick so it lands after the host's own
// "Model: ..." status. The handler must have returned before the toast is sent.
func TestRememberedToastIsSentAfterTheHandlerReturns(t *testing.T) {
	p := startParityHost(t, "tui", nil, nil, nil)
	if err := os.WriteFile(filepath.Join(p.home, "agent", "settings.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p.start(t, "resume")
	if failure := fire(p.Host, "model_select", selectModel()); failure != "" {
		t.Fatal(failure)
	}
	want := rememberModelName + ": remembered model: acme/model-1 in settings.json"
	deadline := time.Now().Add(2 * time.Second)
	for {
		if got := notes(p.Host); len(got) > 0 {
			wantNotes(t, got, info(want))
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no toast; want %q", want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRememberedToastIsNotSentInsideTheHandler(t *testing.T) {
	p := startParityHost(t, "tui", nil, nil, nil)
	if err := os.WriteFile(filepath.Join(p.home, "agent", "settings.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p.start(t, "resume")
	// fire returns when the handler's result reached the host; the toast is a
	// separate, later call, so it cannot already be in the call log.
	if failure := fire(p.Host, "model_select", selectModel()); failure != "" {
		t.Fatal(failure)
	}
	if got := notes(p.Host); len(got) != 0 {
		t.Fatalf("%d notifications were sent before the handler returned: %q", len(got), got)
	}
}

// ---- provider lock texts and picker (pt-openrouter-lock-provider.ts) ----

func TestLockOnOffListTexts(t *testing.T) {
	home := lockHome(t)
	seedSettings(t, home, map[string]any{"version": 1, "openrouterModelProviderPref": map[string]any{
		"enabled": true, "locks": map[string]string{"vendor/b": "beta", "vendor/a": "alpha"}}})
	h := startLockHost(t, map[string]any{"id": "vendor/m", "provider": "acme"})
	p := &parityHost{Host: h, home: home}
	wantNotes(t, p.run(t, lockProviderName, "list"), info("OpenRouter provider locks (ON):\n  • vendor/a -> alpha\n  • vendor/b -> beta"))
	wantNotes(t, p.run(t, lockProviderName, "off"), info("openrouter-lock-provider disabled"))
	wantNotes(t, p.run(t, lockProviderName, "list"), info("OpenRouter provider locks (OFF):\n  • vendor/a -> alpha\n  • vendor/b -> beta"))
	wantNotes(t, p.run(t, lockProviderName, "on"), info("openrouter-lock-provider enabled"))
}

func TestLockListWithNoLocks(t *testing.T) {
	lockHome(t)
	h := startLockHost(t, map[string]any{"id": "vendor/m", "provider": "acme"})
	p := &parityHost{Host: h}
	wantNotes(t, p.run(t, lockProviderName, "list"), info("OpenRouter provider locks (ON):\n  (none)"))
}

func TestLockTakesTheWholeArgumentAsTheProvider(t *testing.T) {
	home := lockHome(t)
	h := startLockHost(t, map[string]any{"id": "vendor/m", "provider": "openrouter"})
	p := &parityHost{Host: h, home: home}
	got := p.run(t, lockProviderName, "my provider  slug ")
	if len(got) != 1 || !strings.HasPrefix(got[0].message, "OpenRouter provider lock for vendor/m: my provider  slug\n") {
		t.Fatalf("notification = %q, want the trimmed whole argument as the provider", got)
	}
	state := readSettings(filepath.Join(home, "agent", settingsFileName))
	if state.OpenRouterModelProviderPref.Locks["vendor/m"] != "my provider  slug" {
		t.Fatalf("locks = %v", state.OpenRouterModelProviderPref.Locks)
	}
}

func lockPickerHost(t *testing.T, locks map[string]string, available []modelRef, answer pickerAnswer, input func() map[string]any) *parityHost {
	t.Helper()
	home := lockHome(t)
	seedSettings(t, home, map[string]any{"version": 1, "openrouterModelProviderPref": map[string]any{"enabled": true, "locks": locks}})
	models := make([]map[string]any, 0, len(available))
	providers := map[string]any{}
	for _, model := range available {
		models = append(models, map[string]any{"provider": model.Provider, "id": model.Model})
		providers[model.Provider] = map[string]any{"configured": true}
	}
	var picked []map[string]any
	host := StartHost(t, Extension(), HostOptions{OnCall: func(method string, args map[string]any) (map[string]any, string) {
		switch method {
		case "getModelRegistryState":
			return map[string]any{"models": models, "providers": providers}, ""
		case "ui.custom":
			picked = append(picked, args)
			if answer == nil {
				return map[string]any{}, ""
			}
			return map[string]any{"ok": true, "result": answer()}, ""
		case "ui.input":
			if input != nil {
				return input(), ""
			}
		}
		return map[string]any{}, ""
	}})
	setActiveModel(host, map[string]any{"id": "x", "provider": "acme"})
	if failure := fire(host, "session_start", map[string]any{"reason": "resume"}); failure != "" {
		t.Fatal(failure)
	}
	return &parityHost{Host: host, home: home}
}

func TestLockPickerWithNoOpenRouterModelWarns(t *testing.T) {
	p := lockPickerHost(t, nil, []modelRef{{"acme", "m1"}}, nil, nil)
	got := p.run(t, lockProviderName, "")
	wantNotes(t, got, warning("No OpenRouter models available"))
}

func TestLockPickerListsAvailableModelsAndLockedIdsAndIsCancellable(t *testing.T) {
	p := lockPickerHost(t, map[string]string{"vendor/locked": "alpha"}, []modelRef{{"openrouter", "vendor/b"}, {"openrouter", "vendor/a"}, {"acme", "x"}}, nil, nil)
	if got := p.run(t, lockProviderName, ""); len(got) != 0 {
		t.Fatalf("a cancelled lock picker said %q; the original returns silently", got)
	}
	if len(p.CallsTo("ui.custom")) != 1 {
		t.Fatal("no picker opened")
	}
}

func TestLockPickerItems(t *testing.T) {
	picker := newLockPicker([]lockItem{{"vendor/a", "provider: alpha"}, {"vendor/b", "no provider lock"}})
	frame := strings.Join(picker.Render(80), "\n")
	for _, want := range []string{"OpenRouter Provider Lock", "  type to search", "→ vendor/a", "provider: alpha", "no provider lock", "↑↓ navigate • enter select • esc cancel"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("frame lacks %q:\n%s", want, frame)
		}
	}
	press(t, picker, "v", "e", "n", "d", "o", "r", "/", "b")
	frame = strings.Join(picker.Render(80), "\n")
	if !strings.Contains(frame, "  search: vendor/b") || strings.Contains(frame, "vendor/a") {
		t.Fatalf("filter frame:\n%s", frame)
	}
	if done := press(t, picker, "\r"); !done.Done || done.Value != "vendor/b" {
		t.Fatalf("result = %+v, want vendor/b", done)
	}
}

func TestLockPickerFilterIsAPrefixMatchAndArrowsWrap(t *testing.T) {
	picker := newLockPicker([]lockItem{{"vendor/a", ""}, {"vendor/b", ""}})
	press(t, picker, "\x1b[A")
	if done := press(t, picker, "\r"); done.Value != "vendor/b" {
		t.Fatalf("Up from the first row = %v, want wrap to the last", done.Value)
	}
	picker = newLockPicker([]lockItem{{"vendor/a", ""}, {"vendor/b", ""}})
	press(t, picker, "b")
	if !strings.Contains(strings.Join(picker.Render(80), "\n"), "No matching commands") {
		t.Fatal("a filter that is not a prefix of any id must show the no-match row")
	}
}

func TestLockPickerEscapeClearsTheSearchThenCancels(t *testing.T) {
	picker := newLockPicker([]lockItem{{"vendor/a", ""}})
	press(t, picker, "v")
	if done := press(t, picker, "\x1b"); done.Done {
		t.Fatal("Esc with a search cancelled")
	}
	if done := press(t, picker, "\x1b"); !done.Done || done.Value != nil {
		t.Fatalf("second Esc = %+v", done)
	}
}

func TestLockPickerFlowAsksForTheProviderWithTheCurrentOneAsPlaceholder(t *testing.T) {
	var asked map[string]any
	p := lockPickerHost(t, map[string]string{"vendor/a": "alpha"}, []modelRef{{"openrouter", "vendor/a"}},
		func() any { return "vendor/a" },
		func() map[string]any { return map[string]any{"value": "  beta "} })
	got := p.run(t, lockProviderName, "")
	wantNotes(t, got, info("OpenRouter provider lock for vendor/a: beta"))
	for _, call := range p.CallsTo("ui.input") {
		asked = call.Args
	}
	if asked["title"] != "Provider for vendor/a (empty to clear):" || asked["placeholder"] != "alpha" {
		t.Fatalf("input = %v", asked)
	}
	state := readSettings(filepath.Join(p.home, "agent", settingsFileName))
	if state.OpenRouterModelProviderPref.Locks["vendor/a"] != "beta" {
		t.Fatalf("locks = %v", state.OpenRouterModelProviderPref.Locks)
	}
}

func TestLockPickerFlowEmptyInputClearsAndPlaceholderDefaultsToDeepseek(t *testing.T) {
	p := lockPickerHost(t, map[string]string{"vendor/a": "alpha"}, []modelRef{{"openrouter", "vendor/a"}, {"openrouter", "vendor/c"}},
		func() any { return "vendor/a" },
		func() map[string]any { return map[string]any{"value": "  "} })
	wantNotes(t, p.run(t, lockProviderName, ""), info("Cleared provider lock for vendor/a"))
	if state := readSettings(filepath.Join(p.home, "agent", settingsFileName)); len(state.OpenRouterModelProviderPref.Locks) != 0 {
		t.Fatalf("locks = %v, want none", state.OpenRouterModelProviderPref.Locks)
	}
}

func TestLockPickerFlowCancelledInputChangesNothing(t *testing.T) {
	p := lockPickerHost(t, nil, []modelRef{{"openrouter", "vendor/c"}},
		func() any { return "vendor/c" },
		func() map[string]any { return map[string]any{"cancelled": true} })
	if got := p.run(t, lockProviderName, ""); len(got) != 0 {
		t.Fatalf("a cancelled input said %q", got)
	}
	for _, call := range p.CallsTo("ui.input") {
		if call.Args["placeholder"] != "deepseek" {
			t.Fatalf("placeholder = %v, want deepseek for a model with no lock", call.Args["placeholder"])
		}
	}
}
