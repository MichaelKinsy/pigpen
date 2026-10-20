package ask_user_question

import (
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const (
	fListener = "ask-user-question.listener"

	kittyCtrlRBracketPress   = "\x1b[93;5u"
	kittyCtrlRBracketRepeat  = "\x1b[93;5:2u"
	kittyCtrlRBracketRelease = "\x1b[93;5:3u"
	altO                     = "\x1bo" // ESC-prefixed 'o': legacy encoding for Alt+O
)

var (
	listenerParams = askArgsOf(askQ("Pick one", "Choice", false, labels("Alpha", "Beta")...))
	consumed       = sdk.TerminalInputResult{Consume: true}
	untouched      = sdk.TerminalInputResult{}
)

// driveWithListener is a fake interactive-mode ctx.ui: onTerminalInput captures the raw listener, custom hands
// out the overlay handle and runs the script against the real component.
func driveWithListener(t *testing.T, handle *fakeHandle, script func(f *fakeUIHost, c *scriptComp, done func(any))) *fakeUIHost {
	t.Helper()
	f := newFakeUI(t)
	f.rawInput = true
	f.handle = handle
	f.script = func(c *scriptComp, done func(any)) { script(f, c, done) }
	return f
}

func TestListener(t *testing.T) {
	tw(t, fListener, "hides via OverlayHandle.setHidden, notifies once, and unhides on the second press", func(t *testing.T) {
		handle := &fakeHandle{}
		f := driveWithListener(t, handle, func(f *fakeUIHost, _ *scriptComp, done func(any)) {
			// First press: hide + one-shot notification with the reopen key.
			eq(t, f.raw(ctrlRBr), consumed, "first press")
			eq(t, handle.IsHidden(), true, "hidden after first")
			eq(t, len(f.notices), 1, "notify count")
			if !strings.Contains(f.notices[0].Message, "press Ctrl+] to reopen") || f.notices[0].Level != "info" {
				t.Fatalf("notice %#v", f.notices[0])
			}
			// Second press: unhide, and the notification stays one-shot.
			eq(t, f.raw(ctrlRBr), consumed, "second press")
			eq(t, handle.IsHidden(), false, "hidden after second")
			// Third round-trip re-hides without a second announcement.
			eq(t, f.raw(ctrlRBr), consumed, "third press")
			eq(t, handle.IsHidden(), true, "hidden after third")
			eq(t, len(f.notices), 1, "notify count after third")
			cancelledNow(done)
		})
		f.run(listenerParams)
		// execute's finally must tear the raw listener down once the tool resolves.
		eq(t, f.rawRemovals, 1, "listener removals")
	})
	tw(t, fListener, "toggles once for Kitty keyboard press, repeat, and release events", func(t *testing.T) {
		handle := &fakeHandle{}
		f := driveWithListener(t, handle, func(f *fakeUIHost, _ *scriptComp, done func(any)) {
			eq(t, f.raw(kittyCtrlRBracketPress), consumed, "press")
			eq(t, handle.IsHidden(), true, "hidden")
			// Repeat and release still belong to the collapse binding, so consume them without toggling or leaking
			// them into the newly focused chat editor.
			eq(t, f.raw(kittyCtrlRBracketRepeat), consumed, "repeat")
			eq(t, handle.IsHidden(), true, "hidden after repeat")
			eq(t, f.raw(kittyCtrlRBracketRelease), consumed, "release")
			eq(t, handle.IsHidden(), true, "hidden after release")
			eq(t, f.raw(kittyCtrlRBracketPress), consumed, "second press")
			eq(t, handle.IsHidden(), false, "unhidden")
			cancelledNow(done)
		})
		f.run(listenerParams)
	})
	tw(t, fListener, "ignores non-matching keys", func(t *testing.T) {
		handle := &fakeHandle{}
		f := driveWithListener(t, handle, func(f *fakeUIHost, _ *scriptComp, done func(any)) {
			eq(t, f.raw("x"), untouched, "x")
			eq(t, f.raw(altO), untouched, "alt+o")
			eq(t, handle.IsHidden(), false, "hidden")
			eq(t, len(f.notices), 0, "notices")
			cancelledNow(done)
		})
		f.run(listenerParams)
	})
	tw(t, fListener, "leaves the key to another focused overlay (visible but unfocused questionnaire)", func(t *testing.T) {
		// e.g. `/btw` opened on top: the questionnaire is visible underneath but not focused; the listener must
		// not toggle it from under the top overlay.
		handle := &fakeHandle{isFocused: func() bool { return false }}
		f := driveWithListener(t, handle, func(f *fakeUIHost, _ *scriptComp, done func(any)) {
			eq(t, f.raw(ctrlRBr), untouched, "ctrl+]")
			eq(t, handle.IsHidden(), false, "hidden")
			cancelledNow(done)
		})
		f.run(listenerParams)
	})
	tw(t, fListener, "host with an overlay handle but no raw input keeps the overlay visible on collapse (fallback row, no trap)", func(t *testing.T) {
		// A host that delivers the handle but not onTerminalInput has exactly one input path: the component's
		// handleInput, which pi-tui does not deliver to a hidden overlay. If collapsing hid the overlay here,
		// nothing could ever reopen it, so the session must suppress setHidden and rely on the one-line row.
		handle := &fakeHandle{}
		f := driveWithListener(t, handle, func(_ *fakeUIHost, c *scriptComp, done func(any)) {
			c.in(ctrlRBr)
			eq(t, handle.IsHidden(), false, "hidden")
			collapsed := c.render(120)
			eq(t, len(collapsed), 1, "collapsed rows")
			if !strings.Contains(collapsed[0], "Ctrl+] to expand") {
				t.Fatalf("collapsed row %q", collapsed[0])
			}
			// The visible row still routes input, so the same key expands it again.
			c.in(ctrlRBr)
			if len(c.render(120)) <= 1 {
				t.Fatal("did not expand")
			}
			cancelledNow(done)
		})
		f.rawInput = false
		f.run(listenerParams)
		eq(t, f.rawRegisters, 0, "listeners registered")
	})
	tw(t, fListener, "honours a configured collapseKey (alt+o toggles, ctrl+] does not)", func(t *testing.T) {
		handle := &fakeHandle{}
		f := driveWithListener(t, handle, func(f *fakeUIHost, _ *scriptComp, done func(any)) {
			eq(t, f.raw(ctrlRBr), untouched, "ctrl+]")
			eq(t, handle.IsHidden(), false, "hidden")
			eq(t, f.raw(altO), consumed, "alt+o")
			eq(t, handle.IsHidden(), true, "hidden after alt+o")
			cancelledNow(done)
		})
		f.writeConfig(`{"collapseKey":"alt+o"}`)
		f.run(listenerParams)
	})
	tw(t, fListener, "footer hint and collapsed row name the configured collapseKey (#176)", func(t *testing.T) {
		handle := &fakeHandle{}
		f := driveWithListener(t, handle, func(_ *fakeUIHost, c *scriptComp, done func(any)) {
			expanded := strings.Join(c.render(120), "\n")
			if !strings.Contains(expanded, "Alt+O to collapse") || strings.Contains(expanded, "Ctrl+]") {
				t.Fatalf("expanded footer %q", expanded)
			}
			// Collapse via the component input path: the one-line footer must name the same key the router honours.
			c.in(altO)
			collapsed := c.render(120)
			eq(t, len(collapsed), 1, "collapsed rows")
			if !strings.Contains(collapsed[0], "Alt+O to expand") {
				t.Fatalf("collapsed row %q", collapsed[0])
			}
			cancelledNow(done)
		})
		f.writeConfig(`{"collapseKey":"alt+o"}`)
		f.run(listenerParams)
	})
	tw(t, fListener, "does not register a listener when collapseKey is 'off'", func(t *testing.T) {
		handle := &fakeHandle{}
		f := driveWithListener(t, handle, func(_ *fakeUIHost, c *scriptComp, done func(any)) {
			// The footer must not advertise a collapse shortcut that cannot fire (#176).
			rendered := strings.Join(c.render(120), "\n")
			if strings.Contains(rendered, "to collapse") || !strings.Contains(rendered, "Esc to cancel") {
				t.Fatalf("footer %q", rendered)
			}
			cancelledNow(done)
		})
		f.writeConfig(`{"collapseKey":"off"}`)
		f.run(listenerParams)
		eq(t, f.rawRegisters, 0, "listeners registered")
	})
}
