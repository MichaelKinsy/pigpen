package pi_permission_system

import (
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

// Twins of test/authority/permission-prompt-component.test.ts. The fake TUI view is the original's: ctx.ui.custom becomes a Custom
// that builds the component from the factory with a plain theme and a keybindings double that knows only app.tools.expand, and blocks
// until the component calls done.

type plainTheme struct{}

func (plainTheme) Fg(_, text string) string { return text }

const (
	ctrlO      = "\x0f"
	arrowDown  = "\x1b[B"
	enterKey   = "\r"
	escapeKey  = "\x1b"
	decisionCh = 8 // Title, blank separator, four decision options, blank, hint.
)

func paste(content string) string { return "\x1b[200~" + content + "\x1b[201~" }

type expandKeys struct{ key string }

func (k expandKeys) Matches(data, action string) bool {
	return action == "app.tools.expand" && data == k.key
}

type recordingTUI struct {
	mu      sync.Mutex
	writes  []string
	renders int
}

func (r *recordingTUI) RequestRender() { r.mu.Lock(); r.renders++; r.mu.Unlock() }
func (r *recordingTUI) Write(data string) {
	r.mu.Lock()
	r.writes = append(r.writes, data)
	r.mu.Unlock()
}
func (r *recordingTUI) written() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.writes...)
}

// fakeView is makeFakeView(): a PermissionPromptView over a promptUIFake whose Custom captures the component.
type fakeView struct {
	view      PermissionPromptView
	ui        *promptUIFake
	tui       *recordingTUI
	mu        sync.Mutex
	component PromptComponent
	options   *PromptCustomOptions
	mounted   chan struct{}
}

type fakeViewOptions struct {
	expandKey     string
	notice        *PromptNotice
	dialogKeys    DialogKeyBindings
	budget        *RenderBudget
	notifications []string
}

func makeFakeView(doublePress bool, opts ...func(*fakeViewOptions)) *fakeView {
	o := fakeViewOptions{expandKey: ctrlO}
	for _, m := range opts {
		m(&o)
	}
	v := &fakeView{ui: &promptUIFake{}, tui: &recordingTUI{}, mounted: make(chan struct{})}
	v.ui.custom = func(factory PromptComponentFactory, options PromptCustomOptions) (UnattributedDecision, bool, error) {
		done := make(chan UnattributedDecision, 4)
		component := factory(v.tui, plainTheme{}, expandKeys{o.expandKey}, func(d UnattributedDecision) { done <- d })
		v.mu.Lock()
		v.component, v.options = component, &options
		v.mu.Unlock()
		close(v.mounted)
		return <-done, true, nil
	}
	prefs := defaultPromptPreferences()
	prefs.DoublePressToConfirm = doublePress
	if o.dialogKeys != nil {
		prefs.DialogKeys = o.dialogKeys
	}
	if o.budget != nil {
		prefs.Budget = *o.budget
	}
	prefs.PromptNotifications = o.notifications
	notice := PromptNotice{Title: "pi", Body: "Permission Required"}
	if o.notice != nil {
		notice = *o.notice
	}
	v.view = PermissionPromptView{PromptPreferences: prefs, Mode: "tui", UI: v.ui, Notice: notice}
	return v
}

type presented struct {
	result chan presentResult
}

type presentResult struct {
	decision UnattributedDecision
	err      error
}

// present starts the inline prompt and waits for the component to mount.
func (v *fakeView) present(t *testing.T, title string, payload PromptPayload, options *RequestPermissionOptions) *presented {
	t.Helper()
	p := &presented{result: make(chan presentResult, 1)}
	go func() {
		d, err := PresentInlinePermissionPrompt(v.view, title, payload, options)
		p.result <- presentResult{d, err}
	}()
	select {
	case <-v.mounted:
	case <-time.After(5 * time.Second):
		t.Fatal("the component never mounted")
	}
	return p
}

func (v *fakeView) comp() PromptComponent {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.component
}

func (v *fakeView) keys(data ...string) {
	for _, d := range data {
		v.comp().HandleInput(d)
	}
}

func (v *fakeView) text(width int) string { return strings.Join(v.comp().Render(width), "\n") }

// await is `await promise`.
func (p *presented) await(t *testing.T) UnattributedDecision {
	t.Helper()
	select {
	case r := <-p.result:
		if r.err != nil {
			t.Fatalf("prompt failed: %v", r.err)
		}
		return r.decision
	case <-time.After(5 * time.Second):
		t.Fatal("the prompt never settled")
		return UnattributedDecision{}
	}
}

// settled reports whether the prompt has settled, after the queue had time to run.
func (p *presented) settled() bool {
	time.Sleep(30 * time.Millisecond)
	select {
	case r := <-p.result:
		p.result <- r
		return true
	default:
		return false
	}
}

func makeAsk(value string) PromptPayload {
	return authPayload(func(p *PromptPayload) {
		p.Kind = "path"
		p.Request.Surface, p.Request.ToolName, p.Request.Value, p.Request.MatchedPattern = "path", sp("read"), value, nil
	})
}

var optionRow = regexp.MustCompile(`^[ ▶] \((\w)\) `)

// decisionOptionKeys are the hotkeys of the decision step's option rows, in rendered order.
func decisionOptionKeys(v *fakeView) []string {
	var keys []string
	for _, line := range v.comp().Render(80) {
		if m := optionRow.FindStringSubmatch(line); m != nil {
			keys = append(keys, m[1])
		}
	}
	return keys
}

func TestPermissionPromptComponentTwins(t *testing.T) {
	f := "authority/permission-prompt-component"
	ask := makeAsk("/repo/secret.txt")
	denied := UnattributedDecision{State: DecisionDenied}
	approved := UnattributedDecision{Approved: true, State: DecisionApproved}
	runPrompt := func(t *testing.T, double bool, keys []string, options *RequestPermissionOptions) UnattributedDecision {
		t.Helper()
		v := makeFakeView(double)
		p := v.present(t, "Permission Required", ask, options)
		v.keys(keys...)
		return p.await(t)
	}

	tw(t, f, "renders inline (not as an overlay) with the request facts and hotkey labels", func(t *testing.T) {
		v := makeFakeView(true)
		v.present(t, "Permission Required", ask, nil)
		eq(t, *v.options, PromptCustomOptions{Overlay: false}, "options")
		text := v.text(80)
		for _, s := range []string{"tool : read", "path : /repo/secret.txt", "Yes", "No, provide reason", "y", "r"} {
			if !strings.Contains(text, s) {
				t.Errorf("missing %q in\n%s", s, text)
			}
		}
	})
	tw(t, f, "renders the decision options in the model's display order", func(t *testing.T) {
		v := makeFakeView(true)
		v.present(t, "Permission Required", ask, nil)
		eq(t, decisionOptionKeys(v), []string{"y", "s", "n", "r"}, "keys")
	})
	tw(t, f, "clips every rendered line to the terminal width", func(t *testing.T) {
		v := makeFakeView(true)
		v.present(t, "Permission Required", makeAsk("~/.pi/agent/sessions/"+strings.Repeat("a", 300)), nil)
		lines := v.comp().Render(40)
		if len(lines) == 0 {
			t.Fatal("no lines")
		}
		for _, line := range lines {
			if widthx.VisibleWidth(line) > 40 {
				t.Errorf("line wider than 40: %q", line)
			}
		}
	})

	tw(t, f, "resolves approved on y, y", func(t *testing.T) { eq(t, runPrompt(t, true, []string{"y", "y"}, nil), approved, "decision") })
	tw(t, f, "does not resolve on a single armed press", func(t *testing.T) {
		v := makeFakeView(true)
		p := v.present(t, "Permission Required", ask, nil)
		v.keys("y")
		if p.settled() {
			t.Error("settled on one press")
		}
		if !strings.Contains(v.text(80), "Press y again to approve.") {
			t.Error("no arming hint")
		}
	})
	tw(t, f, "resolves denied on n, n", func(t *testing.T) { eq(t, runPrompt(t, true, []string{"n", "n"}, nil), denied, "decision") })

	digits := DialogKeyBindings{ActionApprove: "1", ActionApproveSession: "2", ActionApproveSessionBoth: "3", ActionDeny: "4", ActionDenyWithReason: "5"}
	withKeys := func(k DialogKeyBindings) func(*fakeViewOptions) { return func(o *fakeViewOptions) { o.dialogKeys = k } }
	tw(t, f, "renders each decision row with its configured binding", func(t *testing.T) {
		v := makeFakeView(true, withKeys(digits))
		v.present(t, "Permission Required", ask, nil)
		eq(t, decisionOptionKeys(v), []string{"1", "2", "4", "5"}, "keys")
	})
	tw(t, f, "commits the decision whose configured binding was pressed", func(t *testing.T) {
		v := makeFakeView(true, withKeys(digits))
		p := v.present(t, "Permission Required", ask, nil)
		v.keys("4", "4")
		eq(t, p.await(t), denied, "decision")
	})
	tw(t, f, "routes a keystroke by the action it is bound to, not by its letter", func(t *testing.T) {
		swapped := defaultDialogKeys()
		swapped[ActionApprove], swapped[ActionDeny] = "n", "y"
		v := makeFakeView(false, withKeys(swapped))
		p := v.present(t, "Permission Required", ask, nil)
		v.keys("y")
		eq(t, p.await(t), denied, "decision")
	})

	tw(t, f, "writes the configured channels once, before any keystroke", func(t *testing.T) {
		v := makeFakeView(true, func(o *fakeViewOptions) {
			o.notifications = []string{"bell", "osc777"}
			o.notice = &PromptNotice{Title: "the notice title", Body: "the notice body"}
		})
		v.present(t, "Permission Required", ask, nil)
		// The view's notice, not the dialog title: the notice is composed from labels only, so the path being decided never reaches the
		// terminal's notification history.
		eq(t, v.tui.written(), []string{"\x07\x1b]777;notify;the notice title;the notice body\x07"}, "writes")
	})
	tw(t, f, "does not signal again when the dialog re-renders", func(t *testing.T) {
		v := makeFakeView(true, func(o *fakeViewOptions) { o.notifications = []string{"bell"} })
		v.present(t, "Permission Required", ask, nil)
		v.keys("y") // arms, and re-renders
		v.comp().Render(80)
		eq(t, len(v.tui.written()), 1, "writes")
	})
	tw(t, f, "writes nothing when no channel is configured", func(t *testing.T) {
		v := makeFakeView(true, func(o *fakeViewOptions) { o.notifications = []string{} })
		v.present(t, "Permission Required", ask, nil)
		eq(t, len(v.tui.written()), 0, "writes")
	})

	tw(t, f, "resolves approved on a single y", func(t *testing.T) { eq(t, runPrompt(t, false, []string{"y"}, nil), approved, "decision") })

	tw(t, f, "resolves the highlighted option on enter", func(t *testing.T) {
		// y, s, n, then enter
		eq(t, runPrompt(t, true, []string{arrowDown, arrowDown, enterKey}, nil), denied, "decision")
	})
	tw(t, f, "denies on escape at the decision step", func(t *testing.T) { eq(t, runPrompt(t, true, []string{escapeKey}, nil), denied, "decision") })
	tw(t, f, "never decides on a stray paste at the decision step", func(t *testing.T) {
		v := makeFakeView(false)
		p := v.present(t, "Title", ask, nil)
		v.keys(paste("y"), paste("some copied text"))
		if p.settled() {
			t.Fatal("a paste decided")
		}
		v.keys("n")
		eq(t, p.await(t), denied, "decision")
	})

	reasoned := func(reason string) UnattributedDecision {
		return UnattributedDecision{State: DecisionDeniedWithReason, DenialReason: &reason}
	}
	tw(t, f, "collects a typed reason and resolves denied_with_reason", func(t *testing.T) {
		eq(t, runPrompt(t, false, []string{"r", "n", "o", "p", "e", enterKey}, nil), reasoned("nope"), "decision")
	})
	tw(t, f, "rejects an empty reason and shows an error, then accepts a real one", func(t *testing.T) {
		v := makeFakeView(false)
		p := v.present(t, "T", ask, nil)
		v.keys("r", enterKey) // opens the reason step, then an empty submit is rejected
		if !strings.Contains(v.text(80), "A reason is required.") {
			t.Error("no error shown")
		}
		v.keys("x", enterKey)
		eq(t, p.await(t), reasoned("x"), "decision")
	})
	tw(t, f, "supports backspace while editing the reason", func(t *testing.T) {
		eq(t, runPrompt(t, false, []string{"r", "a", "b", "\x7f", enterKey}, nil), reasoned("a"), "decision")
	})
	tw(t, f, "accepts pasted text into the reason", func(t *testing.T) {
		eq(t, runPrompt(t, false, []string{"r", paste("pasted text"), enterKey}, nil), reasoned("pasted text"), "decision")
	})
	tw(t, f, "flattens a multi-line paste into one readable line", func(t *testing.T) {
		eq(t, runPrompt(t, false, []string{"r", paste("denied because it touches\n~/.ssh"), enterKey}, nil), reasoned("denied because it touches ~/.ssh"), "decision")
	})
	tw(t, f, "keeps a pasted reason on one row, however long it is", func(t *testing.T) {
		v := makeFakeView(false)
		v.present(t, "Title", ask, nil)
		v.keys("r")
		before := v.comp().Render(40)
		// "q" appears nowhere else in this render; "x" would match secret.txt.
		v.keys(paste(strings.Repeat("q", 500)))
		after := v.comp().Render(40)
		eq(t, len(after), len(before), "rows")
		if !strings.Contains(strings.Join(after, "\n"), "qqq") {
			t.Error("the paste is not shown")
		}
		for _, line := range after {
			if widthx.VisibleWidth(line) > 40 {
				t.Errorf("line wider than 40: %q", line)
			}
		}
	})
	tw(t, f, "drops the expand key instead of typing it into the reason", func(t *testing.T) {
		v := makeFakeView(false)
		p := v.present(t, "Title", ask, nil)
		v.keys("r", "a", ctrlO, enterKey)
		eq(t, p.await(t), reasoned("a"), "decision")
		eq(t, len(v.ui.setCalls), 0, "setToolsExpanded calls")
	})
	tw(t, f, "navigates back to the decision step on escape from the reason step", func(t *testing.T) {
		// r opens reason, esc returns to decision, then n deny
		eq(t, runPrompt(t, false, []string{"r", escapeKey, "n"}, nil), denied, "decision")
	})

	tw(t, f, "renders the inline dialog in TUI mode", func(t *testing.T) {
		v := makeFakeView(true)
		var got PermissionPromptDecision
		var gotErr error
		done := make(chan struct{})
		go func() { got, gotErr = RequestPermissionDecision(v.view, "Title", ask, nil); close(done) }()
		<-v.mounted
		v.keys("y", "y")
		<-done
		eq(t, gotErr, nil, "err")
		eq(t, got, PermissionPromptDecision{Approved: true, State: DecisionApproved, DecidedBy: DecisionSource{Kind: "user", Via: "dialog"}}, "decision")
	})
	tw(t, f, "bounds a pathological forwarded ask instead of filling the viewport", func(t *testing.T) {
		v := makeFakeView(true)
		body := strings.TrimSuffix(strings.Repeat("- a finding line about some module in the codebase\n", 200), "\n")
		command := "@'\n" + body + "\n'@ | Out-File -FilePath report.md"
		v.present(t, "Permission Required (Subagent)", authPayload(func(p *PromptPayload) {
			p.Kind = "forwarded"
			p.Request.Requester = PromptRequester{AgentName: sp("scout"), Forwarded: true, SessionID: sp("abc123")}
			p.Request.Surface, p.Request.ToolName, p.Request.Value, p.Request.MatchedPattern = "bash", nil, command, nil
			p.Evidence = []PromptEvidence{{Label: "requested", Text: command}}
		}), nil)
		lines := v.comp().Render(120)
		// The same ask renders 205 rows through the unbounded flat message.
		if limit := int(DefaultRenderBudget.MaxRows) + decisionCh; len(lines) > limit {
			t.Errorf("%d rows, want at most %d", len(lines), limit)
		}
		found := false
		for _, l := range lines {
			found = found || l == "subagent  : scout · session abc123"
		}
		if !found {
			t.Errorf("no subagent row in\n%s", strings.Join(lines, "\n"))
		}
	})
	tw(t, f, "falls back to the select flow outside TUI mode", func(t *testing.T) {
		ui := &promptUIFake{dialogUI: dialogUI{selects: []dialogAnswer{answer("Yes")}}}
		view := PermissionPromptView{PromptPreferences: defaultPromptPreferences(), Mode: "rpc", UI: ui}
		decision, err := RequestPermissionDecision(view, "Title", ask, nil)
		eq(t, err, nil, "err")
		eq(t, ui.selectTitles, []string{"Title\ntool : read\npath : /repo/secret.txt"}, "title")
		if ui.custom != nil || len(ui.selectOpts[0]) != 4 {
			t.Error("custom used or wrong options")
		}
		eq(t, decision, PermissionPromptDecision{Approved: true, State: DecisionApproved, DecidedBy: DecisionSource{Kind: "user", Via: "select"}}, "decision")
	})
	tw(t, f, "attributes a denial to the surface the human answered on", func(t *testing.T) {
		ui := &promptUIFake{dialogUI: dialogUI{selects: []dialogAnswer{answer("No")}}}
		view := PermissionPromptView{PromptPreferences: defaultPromptPreferences(), Mode: "rpc", UI: ui}
		decision, err := RequestPermissionDecision(view, "Title", ask, nil)
		eq(t, err, nil, "err")
		// The denial is the human's, and which surface they used is what separates "the operator declined" from "a prompt they never saw".
		eq(t, decision, PermissionPromptDecision{State: DecisionDenied, DecidedBy: DecisionSource{Kind: "user", Via: "select"}}, "decision")
	})

	sessionWidth := &SessionWidthOption{Label: `Yes, allow reads and writes to "/tmp/*" for this session`}
	tw(t, f, "renders the width row after the session row when the ask is widenable", func(t *testing.T) {
		v := makeFakeView(true)
		v.present(t, "Permission Required", ask, &RequestPermissionOptions{SessionLabel: sp(`Yes, allow writes to "/tmp/*" for this session`), SessionWidth: sessionWidth})
		eq(t, decisionOptionKeys(v), []string{"y", "s", "b", "n", "r"}, "keys")
		if !strings.Contains(v.text(120), sessionWidth.Label) {
			t.Error("width label missing")
		}
	})
	tw(t, f, "renders no width row for an ask that is not widenable", func(t *testing.T) {
		v := makeFakeView(true)
		v.present(t, "Permission Required", ask, nil)
		eq(t, decisionOptionKeys(v), []string{"y", "s", "n", "r"}, "keys")
	})
	tw(t, f, "commits the family width on the b hotkey", func(t *testing.T) {
		eq(t, runPrompt(t, false, []string{"b"}, &RequestPermissionOptions{SessionWidth: sessionWidth}),
			UnattributedDecision{Approved: true, State: DecisionApprovedForSession, SessionGrantWidth: "family"}, "decision")
	})
	tw(t, f, "leaves the s hotkey at the proven width", func(t *testing.T) {
		eq(t, runPrompt(t, false, []string{"s"}, &RequestPermissionOptions{SessionWidth: sessionWidth}), UnattributedDecision{Approved: true, State: DecisionApprovedForSession}, "decision")
	})

	scopeOptions := &RequestPermissionOptions{SessionScope: &SessionScopeOption{SubagentLabel: "This subagent only", ServingSessionLabel: "The whole session"}}
	tw(t, f, "commits the subagent scope by default", func(t *testing.T) {
		eq(t, runPrompt(t, false, []string{"s", enterKey}, scopeOptions), UnattributedDecision{Approved: true, State: DecisionApprovedForSession}, "decision")
	})
	tw(t, f, "commits the serving-session scope when the second option is chosen", func(t *testing.T) {
		eq(t, runPrompt(t, false, []string{"s", arrowDown, enterKey}, scopeOptions), UnattributedDecision{Approved: true, State: DecisionApprovedForServingSess}, "decision")
	})

	tw(t, f, "toggles tool expansion without settling the decision", func(t *testing.T) {
		v := makeFakeView(true)
		p := v.present(t, "Title", ask, nil)
		v.keys(ctrlO)
		eq(t, v.ui.setCalls, []bool{true}, "after the first toggle")
		if p.settled() {
			t.Fatal("settled")
		}
		v.keys(ctrlO)
		eq(t, v.ui.setCalls, []bool{true, false}, "after the second toggle")
		eq(t, v.ui.getCalls, 2, "getToolsExpanded calls")
		if p.settled() {
			t.Fatal("settled")
		}
		v.keys("y", "y")
		// Unattributed: the inline component states the outcome, and the dispatcher above it names the surface the human answered on.
		eq(t, p.await(t), approved, "decision")
	})
	tw(t, f, "toggles during the scope step without committing the grant", func(t *testing.T) {
		v := makeFakeView(false)
		p := v.present(t, "Title", ask, scopeOptions)
		v.keys("s") // decision -> scope
		v.keys(ctrlO)
		eq(t, v.ui.setCalls, []bool{true}, "toggle")
		v.keys(enterKey)
		eq(t, p.await(t), UnattributedDecision{Approved: true, State: DecisionApprovedForSession}, "decision")
	})
	tw(t, f, "expands the dialog to the complete request and back", func(t *testing.T) {
		v := makeFakeView(true, func(o *fakeViewOptions) { o.budget = &RenderBudget{MaxRows: 24, FieldMaxWidth: 10} })
		v.present(t, "Title", makeAsk("/repo/a/very/long/secret.txt"), nil)
		contains := func(lines []string, want string) bool {
			for _, l := range lines {
				if l == want {
					return true
				}
			}
			return false
		}
		bounded := v.comp().Render(120)
		if !contains(bounded, "path : /repo/a/ve…") || !strings.Contains(bounded[len(bounded)-1], "ctrl+o full request") {
			t.Errorf("bounded render:\n%s", strings.Join(bounded, "\n"))
		}
		v.keys(ctrlO)
		expanded := v.comp().Render(120)
		if !contains(expanded, "path : /repo/a/very/long/secret.txt") || !strings.Contains(expanded[len(expanded)-1], "ctrl+o collapse") {
			t.Errorf("expanded render:\n%s", strings.Join(expanded, "\n"))
		}
		// The host's own tool expansion still follows the same keystroke (#642).
		eq(t, v.ui.setCalls, []bool{true}, "setToolsExpanded")
		v.keys(ctrlO)
		eq(t, v.comp().Render(120), bounded, "collapsed render")
	})
	tw(t, f, "advertises the affordance only when the render left something out", func(t *testing.T) {
		v := makeFakeView(true)
		v.present(t, "Title", ask, nil)
		lines := v.comp().Render(120)
		if strings.Contains(lines[len(lines)-1], "ctrl+o") {
			t.Error("advertised")
		}
	})
	tw(t, f, "does not intercept the expand key while a denial reason is typed", func(t *testing.T) {
		// Bound to a printable key on purpose: the default Ctrl+O is a control character the reason editor rejects anyway.
		v := makeFakeView(false, func(o *fakeViewOptions) { o.expandKey = "e" })
		p := v.present(t, "Title", ask, nil)
		v.keys("r", "e", enterKey) // decision -> reason; "e" is typed literally, not an app action
		eq(t, p.await(t), reasoned("e"), "decision")
		eq(t, len(v.ui.setCalls), 0, "setToolsExpanded calls")
	})
}
