package pi_goal_x_test

// Helpers shared by the GoalCommands twins (palette, drafting, tweak, settings, lifecycle): the original's createHarness variants
// over the goal rig.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	goal "github.com/MichaelKinsy/pigpen/pi-goal-x"
)

func boolPtr(b bool) *bool { return &b }

// archivedCwd is a temp project with .pi/goals/archived, as the original's mkdtemp + mkdirSync.
func archivedCwd(t *testing.T) string {
	t.Helper()
	// A short neutral name (the original's mkdtemp "goal-palette-"): the menu lists the path, so a test-named directory would add
	// words to the rows the tests search.
	cwd, err := os.MkdirTemp("", "goal-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(cwd) })
	if err := os.MkdirAll(filepath.Join(cwd, ".pi", "goals", "archived"), 0o755); err != nil {
		t.Fatal(err)
	}
	return cwd
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func fileExists(path string) bool { _, err := os.Stat(path); return err == nil }

// savedSettings is JSON.parse(readFileSync(settings file)).
func savedSettings(t *testing.T, cwd string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(readFile(t, goal.SettingsPath(cwd))), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func anyHas(list []string, sub string) bool {
	for _, v := range list {
		if strings.Contains(v, sub) {
			return true
		}
	}
	return false
}

func hasPrefixItem(list []string, prefix string) bool {
	for _, v := range list {
		if strings.HasPrefix(v, prefix) {
			return true
		}
	}
	return false
}

// toolText is the text of a tool result's content blocks, joined (JSON.stringify(result.content) matches go through this too).
func toolText(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var r struct {
		Content any `json:"content"`
	}
	_ = json.Unmarshal(raw, &r)
	switch c := r.Content.(type) {
	case string:
		return c
	case []any:
		var parts []string
		for _, b := range c {
			if m, ok := b.(map[string]any); ok {
				if s, ok := m["text"].(string); ok {
					parts = append(parts, s)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return string(raw)
}

// toolDetails is the result's details as decoded JSON.
func toolDetails(raw json.RawMessage) map[string]any {
	var r struct {
		Details map[string]any `json:"details"`
	}
	_ = json.Unmarshal(raw, &r)
	return r.Details
}

func toolTerminate(raw json.RawMessage) bool {
	var r struct {
		Terminate bool `json:"terminate"`
	}
	_ = json.Unmarshal(raw, &r)
	return r.Terminate
}

// dialogDriver answers the drafting dialogs the way the original's tests drive ui.custom (hasDialog / dialogResult). The host cannot
// show the rich questionnaire through the fake host, so the questionnaire runs on its basic select dialogs (the same questions and
// answers): a proposal confirmation is the completion-auditor select followed by the question select, a questionnaire tool one select
// per question. The first select of a dialog blocks until result is called.
type dialogDriver struct {
	mu      sync.Mutex
	cond    *sync.Cond
	pending bool
	have    bool
	cur     dialogAnswer
}

type dialogAnswer struct {
	choice  string // the offered answer text ("Confirm — create this goal now"); "" dismisses the dialog
	auditor *bool  // the completion-auditor toggle, nil keeps the offered default
}

func newDialogDriver() *dialogDriver {
	d := &dialogDriver{}
	d.cond = sync.NewCond(&d.mu)
	return d
}

func (d *dialogDriver) hasDialog() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.pending
}

// waitOpen waits for a dialog to open (the tool runs on its own goroutine).
func (d *dialogDriver) waitOpen(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !d.hasDialog() {
		if time.Now().After(deadline) {
			t.Fatal("the dialog never opened")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// result is dialogResult: the user's answer to the open dialog.
func (d *dialogDriver) result(choice string, auditor *bool) {
	d.mu.Lock()
	d.cur, d.have = dialogAnswer{choice: choice, auditor: auditor}, true
	d.cond.Broadcast()
	d.mu.Unlock()
}

// selectFn is rigOptions.Select.
func (d *dialogDriver) selectFn(title string, options []string) (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.have {
		d.pending = true
		for !d.have {
			d.cond.Wait()
		}
		d.pending = false
	}
	a := d.cur
	if strings.HasPrefix(title, "Completion auditor") {
		if a.choice == "" {
			d.have = false
			return "", false
		}
		want := options[0]
		if a.auditor != nil {
			prefix := "Disabled"
			if *a.auditor {
				prefix = "Enabled"
			}
			for _, o := range options {
				if strings.HasPrefix(o, prefix) {
					want = o
				}
			}
		}
		return want, true
	}
	d.have = false
	for _, o := range options {
		if a.choice != "" && strings.Contains(o, ". "+a.choice) {
			return o, true
		}
	}
	return "", false
}

// async runs fn on its own goroutine, as the original awaits a tool promise while the test keeps driving the dialog.
type async struct{ done chan struct{} }

func goAsync(fn func()) *async {
	a := &async{done: make(chan struct{})}
	go func() { defer close(a.done); fn() }()
	return a
}

func (a *async) wait(t *testing.T) {
	t.Helper()
	select {
	case <-a.done:
	case <-time.After(15 * time.Second):
		t.Fatal("the awaited call never finished")
	}
}

const (
	confirmAnswer  = "Confirm — create this goal now"
	continueAnswer = "Continue chatting — keep refining"
	cancelAnswer   = "Cancel — discard this draft"
)

// terminalInput delivers raw terminal input to the extension's listeners the way the host does (waiting for the verdict) and reports
// whether the input was consumed.
func (r *goalRig) terminalInput(data string) (consumed bool) {
	r.t.Helper()
	raw, failure := r.Host.roundTrip(map[string]any{"method": "terminal_input", "args": map[string]any{"data": data}})
	if failure != "" {
		r.t.Fatalf("terminal_input: %s", failure)
	}
	var v struct {
		Consume bool `json:"consume"`
	}
	_ = json.Unmarshal(raw, &v)
	return v.Consume
}
