package tintinweb_subagents

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

// Twins of test/fleet-list.test.ts.

const (
	keyDown        = "\x1b[B"
	keyUp          = "\x1b[A"
	keyLeft        = "\x1b[D"
	keyRight       = "\x1b[C"
	keyEsc         = "\x1b"
	keyEnter       = "\r"
	keyDownRelease = "\x1b[1;1:3B" // the kitty-protocol key RELEASE for ↓: listeners receive these too
)

const (
	badgedType     = "colored-reviewer"
	purpleBackdrop = "\x1b[48;2;130;125;189m"
)

var badgedConfig = &agentConfig{Name: badgedType, DisplayName: "Code Reviewer", Color: "purple", Description: "Reviews code", Extensions: false, Skills: false,
	SystemPrompt: "Review code.", PromptMode: "replace", Enabled: true}

var fakeTags = regexp.MustCompile(`</?[a-zA-Z]+>|\*`)

// plainRow is the visible text of a rendered row: ANSI stripped, along with this theme's fake markers.
func plainRow(row string) string { return fakeTags.ReplaceAllString(stripANSI(row), "") }

// fleetRecord is a record the list can open (the original's makeRecord).
func fleetRecord(mut func(*agentView)) *agentView {
	r := &agentView{ID: "a1", Type: "general-purpose", Description: "Sleep then report 1", Status: "running", StartedAt: nowMs(), HasSession: true, Usage: lifetimeUsage{Input: 13100}}
	if mut != nil {
		mut(r)
	}
	return r
}

func idRecord(id, description string) *agentView {
	return fleetRecord(func(r *agentView) { r.ID, r.Description = id, description })
}

type fleetHarness struct {
	t         *testing.T
	fleet     *fleetList
	ui        *fakeFleetUI
	manager   *stubFleetManager
	list      *fixedList
	workflows []fleetWorkflow
	opened    []string
	closeFlow func()
	toolTheme uiTheme
}

func newFleetHarness(t *testing.T, agents []*agentView) *fleetHarness {
	t.Helper()
	h := &fleetHarness{t: t, toolTheme: tagTheme()}
	h.list = &fixedList{agents: agents}
	h.manager = &stubFleetManager{fixedList: h.list}
	h.ui = &fakeFleetUI{host: &fakeHost{rows: 40}}
	h.fleet = newFleetList(h.manager, nil, nil, nil, nil)
	h.setup()
	return h
}

func (h *fleetHarness) setup() {
	h.fleet.setUI(h.ui)
	h.fleet.setWorkflowSource(func() []fleetWorkflow { return h.workflows }, func(id string, onClose func()) {
		h.opened = append(h.opened, id)
		// The real opener hands back the dialog's promise, so the list can put the cursor back when it closes. Held
		// open here until a test closes it.
		h.closeFlow = onClose
	})
	h.fleet.update()
}

func (h *fleetHarness) press(data string) *inputResult { return h.ui.press(data) }

func (h *fleetHarness) render(width int) []string {
	h.ui.mu.Lock()
	r := h.ui.render
	h.ui.mu.Unlock()
	if r == nil {
		return nil
	}
	return r(width, h.toolTheme)
}

func (h *fleetHarness) renderDefault() []string { return h.render(120) }

func (h *fleetHarness) setWorkflows(list []fleetWorkflow) {
	h.workflows = list
	h.fleet.update()
}

func (h *fleetHarness) closeWorkflowDialog() {
	if h.closeFlow != nil {
		h.closeFlow()
	}
}

func (h *fleetHarness) closeOverlay() {
	h.ui.mu.Lock()
	d := h.ui.done
	h.ui.mu.Unlock()
	if d != nil {
		d()
	}
}

func (h *fleetHarness) overlayComponent() viewerComponent {
	h.ui.mu.Lock()
	defer h.ui.mu.Unlock()
	return h.ui.component
}

func (h *fleetHarness) overlayOpened() bool {
	h.ui.mu.Lock()
	defer h.ui.mu.Unlock()
	return h.ui.opened
}
func (h *fleetHarness) overlayClosed() bool {
	h.ui.mu.Lock()
	defer h.ui.mu.Unlock()
	return h.ui.closed
}

func findLine(lines []string, sub string) string {
	for _, l := range lines {
		if strings.Contains(l, sub) {
			return l
		}
	}
	return ""
}

func anyLine(lines []string, sub string) bool { return findLine(lines, sub) != "" }

func makeWorkflow(mut func(*fleetWorkflow)) fleetWorkflow {
	w := fleetWorkflow{ID: "wf_abc123", Name: "audit-src", Status: "running", DoneCount: 1, TotalCount: 3, StartedAt: nowMs() - 32_000, Tokens: 26_400}
	if mut != nil {
		mut(&w)
	}
	return w
}

func consumed(r *inputResult) bool { return r != nil && r.Consume }

func TestFleetListTwins(t *testing.T) {
	const file = "fleet-list"

	t.Run("formatFleetElapsed", func(t *testing.T) {
		tw(t, file, "renders integer seconds (no decimal, no suffix)", func(t *testing.T) {
			eqStr(t, formatFleetElapsed(0), "0s")
			eqStr(t, formatFleetElapsed(11_000), "11s")
			eqStr(t, formatFleetElapsed(11_400), "11s")
			eqStr(t, formatFleetElapsed(11_600), "12s")
		})
		tw(t, file, "floors negatives to 0s", func(t *testing.T) {
			eqStr(t, formatFleetElapsed(-500), "0s")
		})
	})

	t.Run("formatFleetTokens", func(t *testing.T) {
		tw(t, file, "prefixes a down-arrow and uses plural 'tokens'", func(t *testing.T) {
			eqStr(t, formatFleetTokens(13_100), "↓ 13.1k tokens")
			eqStr(t, formatFleetTokens(950), "↓ 950 tokens")
			eqStr(t, formatFleetTokens(1_200_000), "↓ 1.2M tokens")
		})
	})

	t.Run("FleetList navigation", func(t *testing.T) {
		tw(t, file, "does not register a widget when there are no agents", func(t *testing.T) {
			h := newFleetHarness(t, nil)
			if got := h.renderDefault(); len(got) != 0 {
				t.Errorf("%q", got)
			}
		})
		tw(t, file, "hides nested child records from the coordinator fleet", func(t *testing.T) {
			h := newFleetHarness(t, []*agentView{
				idRecord("top", "top-level"),
				fleetRecord(func(r *agentView) { r.ID, r.Description, r.ParentAgentID = "nested", "nested-child", "top" }),
			})
			output := strings.Join(h.renderDefault(), "\n")
			hasStr(t, output, "top-level")
			lacksStr(t, output, "nested-child")
		})
		tw(t, file, "activates on ↓ at an empty prompt, consuming the key", func(t *testing.T) {
			h := newFleetHarness(t, []*agentView{fleetRecord(nil)})
			if res := h.press(keyDown); res == nil || !res.Consume {
				t.Fatalf("%v", res)
			}
			// main selected, list active → nav hint shown
			if !anyLine(h.renderDefault(), "enter view") {
				t.Error("no nav hint")
			}
		})
		tw(t, file, "also activates on ← (matches the '← for agents' hint)", func(t *testing.T) {
			h := newFleetHarness(t, []*agentView{fleetRecord(nil)})
			if !consumed(h.press(keyLeft)) {
				t.Error("not consumed")
			}
		})
		tw(t, file, "does NOT activate when the prompt is non-empty (typing is preserved)", func(t *testing.T) {
			h := newFleetHarness(t, []*agentView{fleetRecord(nil)})
			h.ui.editorText = "hello"
			if h.press(keyDown) != nil {
				t.Error("consumed")
			}
		})
		tw(t, file, "ignores key-release events so one tap moves exactly one row", func(t *testing.T) {
			h := newFleetHarness(t, []*agentView{idRecord("a1", "one"), idRecord("a2", "two")})
			h.press(keyDown)        // activate → selection on main (idx 0)
			h.press(keyDownRelease) // release half of the SAME tap: must be a no-op
			hasStr(t, findLine(h.renderDefault(), "main"), "●")
			h.press(keyDown) // a real second tap → first agent
			h.press(keyDownRelease)
			hasStr(t, findLine(h.renderDefault(), "one"), "●")
			hasStr(t, findLine(h.renderDefault(), "two"), "○")
		})
		tw(t, file, "renders the whole selected row in the theme's primary text color (#230)", func(t *testing.T) {
			h := newFleetHarness(t, []*agentView{idRecord("a1", "one"), idRecord("a2", "two")})
			h.press(keyDown) // activate → main
			h.press(keyDown) // → a1
			selected := findLine(h.renderDefault(), "one")
			// Selection marker keeps accent color; row content uses primary text color.
			hasStr(t, selected, "<accent>●</accent>")
			hasStr(t, selected, "<text>one</text>")
			if !regexp.MustCompile(`<text>\d+s · ↓ [\d.]+k? tokens</text>`).MatchString(selected) {
				t.Errorf("stats: %q", selected)
			}
			// Agent display name rendered with the text token too (this type has no badge).
			hasStr(t, selected, "<text>"+getDisplayName("general-purpose")+"</text>")
			// Inactive rows keep the muted/dim treatment.
			unselected := findLine(h.renderDefault(), "two")
			hasStr(t, unselected, "<dim>○</dim>")
			if !regexp.MustCompile(`<dim>\d+s · ↓ [\d.]+k? tokens</dim>`).MatchString(unselected) {
				t.Errorf("stats: %q", unselected)
			}
			lacksStr(t, unselected, "<text>")
		})
		tw(t, file, "keeps a color badge on the selected row, bolded, without shifting it (#230)", func(t *testing.T) {
			r := newRegistry()
			r.set(badgedConfig)
			registerAgents(r)
			defer registerAgents(newRegistry())
			mk := func(id, desc string) *agentView {
				return fleetRecord(func(v *agentView) { v.ID, v.Type, v.Description = id, badgedType, desc })
			}
			h := newFleetHarness(t, []*agentView{mk("a1", "one"), mk("a2", "two")})
			h.press(keyDown) // activate → main
			before := findLine(h.renderDefault(), "one")
			hasStr(t, before, purpleBackdrop)
			hasStr(t, before, " "+badgedConfig.DisplayName+" ")

			h.press(keyDown) // → a1
			selected := findLine(h.renderDefault(), "one")
			// Selection bolds the badge rather than repainting it (Claude Code's FleetView) …
			hasStr(t, selected, purpleBackdrop)
			hasStr(t, selected, "* "+badgedConfig.DisplayName+" *")
			lacksStr(t, selected, "<text>"+badgedConfig.DisplayName)
			// … so the description stays in the same column as when unselected.
			if a, b := strings.Index(plainRow(selected), "one"), strings.Index(plainRow(before), "one"); a != b {
				t.Errorf("description moved from column %d to %d", b, a)
			}
		})
		tw(t, file, "moves selection down/up and clamps at the ends", func(t *testing.T) {
			h := newFleetHarness(t, []*agentView{idRecord("a1", "one"), idRecord("a2", "two")})
			h.press(keyDown) // activate → index 0 (main)
			h.press(keyDown) // → 1 (a1)
			hasStr(t, findLine(h.renderDefault(), "one"), "●")
			h.press(keyDown) // → 2 (a2)
			h.press(keyDown) // clamp at 2
			hasStr(t, findLine(h.renderDefault(), "two"), "●")
			hasStr(t, findLine(h.renderDefault(), "one"), "○")
		})
		tw(t, file, "↑ above 'main' deactivates (returns to the prompt)", func(t *testing.T) {
			h := newFleetHarness(t, []*agentView{fleetRecord(nil)})
			h.press(keyDown) // activate, index 0
			if !consumed(h.press(keyUp)) {
				t.Error("not consumed")
			}
			if !anyLine(h.renderDefault(), "← for agents") {
				t.Error("no inactive hint")
			}
		})
		tw(t, file, "Esc deactivates", func(t *testing.T) {
			h := newFleetHarness(t, []*agentView{fleetRecord(nil)})
			h.press(keyDown)
			if !consumed(h.press(keyEsc)) {
				t.Error("not consumed")
			}
			if !anyLine(h.renderDefault(), "← for agents") {
				t.Error("no inactive hint")
			}
		})
		tw(t, file, "passes non-nav keys through and cancels navigation", func(t *testing.T) {
			h := newFleetHarness(t, []*agentView{fleetRecord(nil)})
			h.press(keyDown)
			if h.press(keyRight) != nil {
				t.Error("consumed")
			}
			if !anyLine(h.renderDefault(), "← for agents") {
				t.Error("no inactive hint")
			}
		})
		tw(t, file, "ignores all input while disabled and hides the widget", func(t *testing.T) {
			h := newFleetHarness(t, []*agentView{fleetRecord(nil)})
			h.fleet.setEnabled(false)
			if h.press(keyDown) != nil {
				t.Error("consumed")
			}
			if got := h.renderDefault(); len(got) != 0 {
				t.Errorf("%q", got)
			}
		})
		tw(t, file, "re-arms the refresh timer when the list is re-shown (toggle off→on)", func(t *testing.T) {
			clock, restore := installManualClock()
			defer restore()
			list := &fixedList{agents: []*agentView{idRecord("a1", "x")}}
			f := newFleetList(&stubFleetManager{fixedList: list}, nil, nil, nil, nil)
			f.setUI(&fakeFleetUI{})
			f.update()           // shows list, arms the timer
			f.setEnabled(false)  // hides, clears the timer
			f.setEnabled(true)   // re-shows: must re-arm the timer
			before := list.calls // a tick should fire and re-read the roster
			clock.fire()
			if list.calls <= before {
				t.Errorf("the roster was not re-read (%d calls)", list.calls)
			}
			f.dispose()
		})
	})

	t.Run("FleetList vs other focused components (#123)", func(t *testing.T) {
		// pi dispatches terminal input to extension listeners BEFORE the focused component, and dialogs swap the prompt
		// editor out while the editor text still reads empty. So while another component owns the keyboard the list
		// must not consume its keys. The Go SDK exposes no focused component: the probe stands for the original's
		// `focusedComponent instanceof Editor`.
		focus := func(h *fleetHarness, isEditor bool) {
			h.fleet.editorHasFocus = func() bool { return isEditor }
			h.renderDefault()
		}
		tw(t, file, "does not steal ↓ from a focused selector (activation)", func(t *testing.T) {
			h := newFleetHarness(t, []*agentView{fleetRecord(nil)})
			focus(h, false)
			if h.press(keyDown) != nil {
				t.Error("consumed")
			}
		})
		tw(t, file, "does not steal navigation keys from a selector opened while the list was active", func(t *testing.T) {
			h := newFleetHarness(t, []*agentView{fleetRecord(nil)})
			focus(h, true)
			if !consumed(h.press(keyDown)) { // activate at the prompt
				t.Fatal("not activated")
			}
			focus(h, false) // a dialog takes focus
			for _, k := range []string{keyDown, keyEnter, keyEsc} {
				if h.press(k) != nil {
					t.Errorf("consumed %q", k)
				}
			}
			// and the list dropped back to its inactive hint
			if !anyLine(h.renderDefault(), "← for agents") {
				t.Error("no inactive hint")
			}
		})
		tw(t, file, "still activates when the prompt editor has focus", func(t *testing.T) {
			h := newFleetHarness(t, []*agentView{fleetRecord(nil)})
			focus(h, true)
			if !consumed(h.press(keyDown)) {
				t.Error("not consumed")
			}
		})
		tw(t, file, "assumes the editor when focus is unknowable (no tui yet / nothing focused)", func(t *testing.T) {
			h := newFleetHarness(t, []*agentView{fleetRecord(nil)})
			// No probe: the list has never seen the focus, and activation must still work.
			if !consumed(h.press(keyDown)) {
				t.Error("not consumed")
			}
		})
	})

	t.Run("FleetList rendering", func(t *testing.T) {
		tw(t, file, "renders main + agent rows with markers, type, description and right-aligned stats", func(t *testing.T) {
			h := newFleetHarness(t, []*agentView{fleetRecord(nil)})
			lines := h.render(120)
			hasStr(t, lines[0], "← for agents")
			hasStr(t, findLine(lines, "main"), "●") // main selected by default
			agentLine := findLine(lines, "Sleep then report 1")
			hasStr(t, agentLine, "○")
			hasStr(t, agentLine, getDisplayName("general-purpose"))
			hasStr(t, agentLine, "↓ 13.1k tokens")
			if !regexp.MustCompile(`\d+s · ↓`).MatchString(agentLine) {
				t.Errorf("%q", agentLine)
			}
		})
		tw(t, file, "orders agents earliest-launched first (top)", func(t *testing.T) {
			h := newFleetHarness(t, []*agentView{
				fleetRecord(func(r *agentView) { r.ID, r.Description, r.StartedAt = "new", "newest", 2000 }),
				fleetRecord(func(r *agentView) { r.ID, r.Description, r.StartedAt = "old", "oldest", 1000 }),
			})
			lines := h.renderDefault()
			oldIdx, newIdx := -1, -1
			for i, l := range lines {
				if strings.Contains(l, "oldest") {
					oldIdx = i
				}
				if strings.Contains(l, "newest") {
					newIdx = i
				}
			}
			if oldIdx < 0 || oldIdx >= newIdx {
				t.Errorf("oldest at %d, newest at %d", oldIdx, newIdx)
			}
		})
		tw(t, file, "hides agents that have no session yet (pending)", func(t *testing.T) {
			h := newFleetHarness(t, []*agentView{
				idRecord("live", "running one"),
				fleetRecord(func(r *agentView) {
					r.ID, r.Description, r.Status, r.HasSession = "pending", "queued one", "queued", false
				}),
			})
			lines := h.renderDefault()
			if !anyLine(lines, "running one") || anyLine(lines, "queued one") {
				t.Errorf("%q", lines)
			}
		})
		tw(t, file, "collapses overflow into a '↓ N more' indicator", func(t *testing.T) {
			var agents []*agentView
			for i := 0; i < 8; i++ {
				agents = append(agents, idRecord(fmt.Sprintf("a%d", i), fmt.Sprintf("report %d", i)))
			}
			h := newFleetHarness(t, agents)
			if !anyLine(h.render(120), "↓ 3 more") {
				t.Error("no overflow row")
			}
		})
		tw(t, file, "never emits a line wider than the terminal (guards wrap-induced flicker)", func(t *testing.T) {
			var agents []*agentView
			for i := 0; i < 8; i++ {
				agents = append(agents, idRecord(fmt.Sprintf("a%d", i), fmt.Sprintf("a very long agent description number %d that keeps going", i)))
			}
			h := newFleetHarness(t, agents)
			for _, w := range []int{4, 8, 12, 20, 40, 80, 200} {
				for _, line := range h.render(w) {
					if vw := widthx.VisibleWidth(line); vw > w {
						t.Errorf("width %d: line of %d: %q", w, vw, line)
					}
				}
			}
		})
		tw(t, file, "windows the visible agents so the selection stays on screen", func(t *testing.T) {
			var agents []*agentView
			for i := 0; i < 8; i++ {
				agents = append(agents, idRecord(fmt.Sprintf("a%d", i), fmt.Sprintf("report %d", i)))
			}
			h := newFleetHarness(t, agents)
			h.press(keyDown) // activate (main)
			for i := 0; i < 8; i++ {
				h.press(keyDown) // step down to the last agent (8 agents → roster index 8)
			}
			lines := h.render(120)
			hasStr(t, findLine(lines, "report 7"), "●")
			if !anyLine(lines, "↑") { // hidden-above indicator
				t.Error("no hidden-above row")
			}
		})
	})

	t.Run("FleetList overlay lifecycle", func(t *testing.T) {
		tw(t, file, "Enter on 'main' just deactivates (no overlay)", func(t *testing.T) {
			h := newFleetHarness(t, []*agentView{fleetRecord(nil)})
			h.press(keyDown) // active, index 0 (main)
			h.press(keyEnter)
			if h.overlayOpened() {
				t.Error("an overlay opened")
			}
			if !anyLine(h.renderDefault(), "← for agents") {
				t.Error("not deactivated")
			}
		})
		tw(t, file, "keeps the cursor on the viewed agent after closing, even if the list reordered", func(t *testing.T) {
			agents := []*agentView{idRecord("a1", "one"), idRecord("a2", "two"), idRecord("a3", "three")}
			h := newFleetHarness(t, agents)
			h.press(keyDown)  // activate (main, idx 0)
			h.press(keyDown)  // a1 (idx 1)
			h.press(keyDown)  // a2 (idx 2)
			h.press(keyEnter) // open a2
			// a1 finishes and drops out while viewing → a2 shifts from idx 2 to idx 1.
			h.list.mu.Lock()
			h.list.agents = h.list.agents[1:]
			h.list.mu.Unlock()
			h.closeOverlay()
			// Selection follows a2 ("two") to its new position, not whatever is at idx 2 now.
			hasStr(t, findLine(h.renderDefault(), "two"), "●")
			hasStr(t, findLine(h.renderDefault(), "three"), "○")
		})
		tw(t, file, "wires the viewer's steer composer to manager.steer with the agent id", func(t *testing.T) {
			h := newFleetHarness(t, []*agentView{idRecord("live", "the one")})
			h.press(keyDown)  // activate (main)
			h.press(keyDown)  // → the agent
			h.press(keyEnter) // open the conversation viewer

			viewer := h.overlayComponent()
			if viewer == nil {
				t.Fatal("no viewer")
			}
			viewer.HandleInput("\r") // Enter → open composer
			for _, ch := range "go left" {
				viewer.HandleInput(string(ch))
			}
			viewer.HandleInput("\r") // Enter → send

			if got := h.manager.steeredCalls(); !reflect.DeepEqual(got, [][2]string{{"live", "go left"}}) {
				t.Errorf("%v", got)
			}
		})
		tw(t, file, "hands the viewer the user's markdown setting, and persists a mode chosen with m", func(t *testing.T) {
			var persisted []viewerMarkdownMode
			h := newFleetHarness(t, []*agentView{idRecord("live", "the one")})
			h.fleet.viewerMarkdown = func() viewerMarkdownMode { return viewerMarkdownAll }
			h.fleet.onViewerMarkdown = func(m viewerMarkdownMode) { persisted = append(persisted, m) }
			h.press(keyDown)  // activate (main)
			h.press(keyDown)  // → the agent
			h.press(keyEnter) // open the conversation viewer

			h.overlayComponent().HandleInput("m")

			// "all" → "off" proves the cycle started from the *setting*; the viewer's own fallback would have started
			// at "assistant" and landed on "all".
			if !reflect.DeepEqual(persisted, []viewerMarkdownMode{viewerMarkdownOff}) {
				t.Errorf("%v", persisted)
			}
		})
		tw(t, file, "does NOT auto-close when the viewed agent finishes (final output stays readable)", func(t *testing.T) {
			h := newFleetHarness(t, []*agentView{idRecord("live", "the one")})
			h.press(keyDown)  // active (main)
			h.press(keyDown)  // → the agent
			h.press(keyEnter) // opens overlay
			if !h.overlayOpened() {
				t.Fatal("no overlay")
			}
			// The agent finishes, well past the linger window...
			h.list.mu.Lock()
			h.list.agents[0] = fleetRecord(func(r *agentView) {
				r.ID, r.Description, r.Status, r.CompletedAt = "live", "the one", "completed", nowMs()-60_000
			})
			h.list.mu.Unlock()
			h.fleet.onAgentFinished("live")
			if h.overlayClosed() { // viewer stays open
				t.Error("the overlay closed")
			}
			if !anyLine(h.renderDefault(), "the one") { // and stays listed while viewed
				t.Error("the agent left the list")
			}
		})
		tw(t, file, "lingers a finished agent in the list, then drops it after the window", func(t *testing.T) {
			recent := fleetRecord(func(r *agentView) {
				r.ID, r.Description, r.Status, r.CompletedAt = "r", "recent done", "completed", nowMs()
			})
			if !anyLine(newFleetHarness(t, []*agentView{recent}).renderDefault(), "recent done") {
				t.Error("recent dropped")
			}
			old := fleetRecord(func(r *agentView) {
				r.ID, r.Description, r.Status, r.CompletedAt = "o", "old done", "completed", nowMs()-60_000
			})
			if anyLine(newFleetHarness(t, []*agentView{old}).renderDefault(), "old done") {
				t.Error("old kept")
			}
		})
	})

	t.Run("FleetList cost display", func(t *testing.T) {
		row := func(showCost bool, cost float64, activity activitySource) string {
			record := fleetRecord(func(r *agentView) { r.Usage = lifetimeUsage{Input: 13100, Cost: cost} })
			f := newFleetList(&stubFleetManager{fixedList: &fixedList{agents: []*agentView{record}}}, activity, func() bool { return showCost }, nil, nil)
			ui := &fakeFleetUI{}
			f.setUI(ui)
			f.update()
			return strings.Join(ui.render(120, plainTheme()), "\n")
		}
		tw(t, file, "appends the cost after the token count when enabled", func(t *testing.T) {
			out := row(true, 0.0042, nil)
			hasStr(t, out, "13.1k tokens")
			hasStr(t, out, "~$0.0042")
		})
		tw(t, file, "shows no cost when disabled, and none for an unpriced model", func(t *testing.T) {
			lacksStr(t, row(false, 0.0042, nil), "$")
			lacksStr(t, row(true, 0, nil), "$")
		})
		tw(t, file, "reads the record, so the figures do not change when the agent finishes", func(t *testing.T) {
			// An activity entry carrying figures of its own is what the old fallback preferred; the row still renders
			// the record's numbers, so the tracker is no longer consulted for spend.
			tracked := activityMap{"a1": {TurnCount: 1}}
			eqStr(t, row(true, 0.0042, tracked), row(true, 0.0042, nil))
		})
	})

	t.Run("FleetList workflow rows", func(t *testing.T) {
		tw(t, file, "renders identically with no workflow source and an empty one", func(t *testing.T) {
			withNone := newFleetHarness(t, []*agentView{idRecord("a1", "one")})
			withNone.fleet.setWorkflowSource(nil, nil)
			before := strings.Join(withNone.renderDefault(), "\n")
			lacksStr(t, before, "workflow")

			withEmpty := newFleetHarness(t, []*agentView{idRecord("a1", "one")})
			withEmpty.setWorkflows(nil)
			eqStr(t, strings.Join(withEmpty.renderDefault(), "\n"), before)
		})
		tw(t, file, "navigates agents exactly as before when no run is present", func(t *testing.T) {
			h := newFleetHarness(t, []*agentView{idRecord("a1", "one"), idRecord("a2", "two")})
			h.setWorkflows(nil)
			if !consumed(h.press(keyDown)) {
				t.Error("not consumed")
			}
			h.press(keyDown)
			h.press(keyDown)
			h.press(keyEnter)
			// The second agent, not a run and not `main`.
			if !h.overlayOpened() || len(h.opened) != 0 {
				t.Errorf("opened %v overlay %v", h.opened, h.overlayOpened())
			}
		})
		tw(t, file, "clears the widget when the last run and the last agent both go", func(t *testing.T) {
			h := newFleetHarness(t, nil)
			h.setWorkflows([]fleetWorkflow{makeWorkflow(nil)})
			if len(h.renderDefault()) == 0 {
				t.Fatal("nothing rendered")
			}
			h.setWorkflows(nil)
			if got := h.renderDefault(); len(got) != 0 {
				t.Errorf("%q", got)
			}
		})
		tw(t, file, "lists a run above the agents, with its counts and stats", func(t *testing.T) {
			h := newFleetHarness(t, []*agentView{idRecord("a1", "one")})
			h.setWorkflows([]fleetWorkflow{makeWorkflow(nil)})
			var rows []string
			for _, l := range h.renderDefault() {
				if p := plainRow(l); strings.TrimSpace(p) != "" {
					rows = append(rows, p)
				}
			}
			run, agent, runIdx := "", -1, -1
			for i, r := range rows {
				if strings.Contains(r, "audit-src") {
					run, runIdx = r, i
				}
				if strings.Contains(r, "one") {
					agent = i
				}
			}
			hasStr(t, run, "workflow")
			hasStr(t, run, "1/3 agents")
			hasStr(t, run, "26.4k tokens")
			if runIdx < 0 || runIdx >= agent { // the container comes first
				t.Errorf("run at %d, agent at %d", runIdx, agent)
			}
		})
		tw(t, file, "agrees with itself about a single-agent run", func(t *testing.T) {
			h := newFleetHarness(t, nil)
			h.setWorkflows([]fleetWorkflow{makeWorkflow(func(w *fleetWorkflow) { w.DoneCount, w.TotalCount = 1, 1 })})
			var rows []string
			for _, l := range h.renderDefault() {
				rows = append(rows, plainRow(l))
			}
			hasStr(t, strings.Join(rows, "\n"), "1/1 agent ")
		})
		tw(t, file, "hides the run's own agents — the run is the row that represents them", func(t *testing.T) {
			h := newFleetHarness(t, []*agentView{
				idRecord("a1", "mine"),
				fleetRecord(func(r *agentView) { r.ID, r.Description, r.WorkflowID = "w1", "the workflow's", "wf_abc123" }),
			})
			h.setWorkflows([]fleetWorkflow{makeWorkflow(nil)})
			var rows []string
			for _, l := range h.renderDefault() {
				rows = append(rows, plainRow(l))
			}
			rendered := strings.Join(rows, "\n")
			hasStr(t, rendered, "mine")
			hasStr(t, rendered, "audit-src")
			lacksStr(t, rendered, "the workflow's")
		})
		tw(t, file, "moves into the list when the only row is a run", func(t *testing.T) {
			h := newFleetHarness(t, nil)
			h.setWorkflows([]fleetWorkflow{makeWorkflow(func(w *fleetWorkflow) { w.ID = "wf_only" })})
			// The first ↓ moves focus into the list and lands on `main`; the second reaches the only other row.
			if !consumed(h.press(keyDown)) {
				t.Error("↓ at an empty prompt has somewhere to go")
			}
			h.press(keyDown)
			h.press(keyEnter)
			if !reflect.DeepEqual(h.opened, []string{"wf_only"}) {
				t.Errorf("%v", h.opened)
			}
		})
		tw(t, file, "still does nothing at an empty prompt with no rows at all", func(t *testing.T) {
			h := newFleetHarness(t, nil)
			if consumed(h.press(keyDown)) {
				t.Error("consumed")
			}
		})
		tw(t, file, "opens the selected run rather than a conversation viewer", func(t *testing.T) {
			h := newFleetHarness(t, []*agentView{idRecord("a1", "one")})
			h.setWorkflows([]fleetWorkflow{makeWorkflow(func(w *fleetWorkflow) { w.ID = "wf_pick" })})
			h.press(keyLeft)
			h.press(keyDown)
			h.press(keyEnter)
			if !reflect.DeepEqual(h.opened, []string{"wf_pick"}) {
				t.Errorf("%v", h.opened)
			}
			// The workflow dialog owns its own overlay; the list must not open one.
			if h.overlayOpened() {
				t.Error("an overlay opened")
			}
		})

		now := nowMs()
		openSecondRun := func(t *testing.T) *fleetHarness {
			h := newFleetHarness(t, []*agentView{idRecord("a1", "one")})
			h.setWorkflows([]fleetWorkflow{
				makeWorkflow(func(w *fleetWorkflow) { w.ID, w.Name, w.StartedAt = "wf_a", "audit-src", now-30_000 }),
				makeWorkflow(func(w *fleetWorkflow) { w.ID, w.Name, w.StartedAt = "wf_b", "review-changes", now-20_000 }),
			})
			h.renderDefault()
			h.press(keyLeft)
			h.press(keyDown)
			h.press(keyDown)
			if len(h.opened) != 0 {
				t.Fatalf("%v", h.opened)
			}
			h.press(keyEnter)
			if !reflect.DeepEqual(h.opened, []string{"wf_b"}) {
				t.Fatalf("%v", h.opened)
			}
			return h
		}
		tw(t, file, "keeps its hands off the keyboard while the inspector is up", func(t *testing.T) {
			// The list only stays out of an overlay's keys when it knows one is open. Focus alone is not enough:
			// unknowable focus reads as the editor's, which is exactly the state a fresh overlay leaves behind.
			h := openSecondRun(t)
			if consumed(h.press(keyDown)) {
				t.Error("the dialog's keys are not the list's")
			}
			if consumed(h.press(keyEnter)) {
				t.Error("enter consumed")
			}
			// And nothing moved behind it: one ENTER opened one run.
			if !reflect.DeepEqual(h.opened, []string{"wf_b"}) {
				t.Errorf("%v", h.opened)
			}
		})
		tw(t, file, "comes back to the same run when the inspector closes", func(t *testing.T) {
			// Runs settle and start while the dialog is open, so the row the reader came from is not reliably where it
			// was: a run has to be findable by id.
			h := openSecondRun(t)
			h.setWorkflows([]fleetWorkflow{
				makeWorkflow(func(w *fleetWorkflow) { w.ID, w.Name, w.StartedAt = "wf_a", "audit-src", now-30_000 }),
				makeWorkflow(func(w *fleetWorkflow) { w.ID, w.Name, w.StartedAt = "wf_new", "started-meanwhile", now-25_000 }),
				makeWorkflow(func(w *fleetWorkflow) { w.ID, w.Name, w.StartedAt = "wf_b", "review-changes", now-20_000 }),
			})
			h.closeWorkflowDialog()
			hasStr(t, findLine(h.renderDefault(), "review-changes"), "●")
			lacksStr(t, findLine(h.renderDefault(), "started-meanwhile"), "●")
		})
		tw(t, file, "still opens an agent's viewer when the selection is past the runs", func(t *testing.T) {
			h := newFleetHarness(t, []*agentView{idRecord("a1", "one")})
			h.setWorkflows([]fleetWorkflow{makeWorkflow(nil)})
			h.press(keyLeft)
			h.press(keyDown)
			h.press(keyDown)
			h.press(keyEnter)
			if len(h.opened) != 0 || !h.overlayOpened() {
				t.Errorf("opened %v overlay %v", h.opened, h.overlayOpened())
			}
		})
		tw(t, file, "drops a settled run once it stops lingering, and keeps a live one", func(t *testing.T) {
			h := newFleetHarness(t, nil)
			h.setWorkflows([]fleetWorkflow{
				makeWorkflow(func(w *fleetWorkflow) {
					w.ID, w.Name, w.Status, w.CompletedAt = "wf_old", "old", "completed", nowMs()-60*60_000
				}),
				makeWorkflow(func(w *fleetWorkflow) { w.ID, w.Name = "wf_now", "now" }),
			})
			var rows []string
			for _, l := range h.renderDefault() {
				rows = append(rows, plainRow(l))
			}
			rendered := strings.Join(rows, "\n")
			hasStr(t, rendered, "now")
			lacksStr(t, rendered, "old")
		})
		tw(t, file, "freezes a finished run's clock the way an agent's is frozen", func(t *testing.T) {
			h := newFleetHarness(t, nil)
			// Inside the linger window, or the row would be gone before it could be read.
			completedAt := nowMs() - 1_000
			h.setWorkflows([]fleetWorkflow{makeWorkflow(func(w *fleetWorkflow) {
				w.Status, w.StartedAt, w.CompletedAt = "completed", completedAt-12_000, completedAt
			})})
			var rows []string
			for _, l := range h.renderDefault() {
				rows = append(rows, plainRow(l))
			}
			hasStr(t, strings.Join(rows, "\n"), "12s")
		})
	})
}
