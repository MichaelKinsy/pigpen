package pi_permission_system

import (
	"testing"
	"time"
)

// Tests of the sdk.RemoteComponent bridge (not twins: the original gets the same behaviour from ctx.ui.custom and pi-tui's TUI).

func bridgeUI(t *testing.T, drive func(d *remoteDialog)) *promptUIFake {
	ui := &promptUIFake{}
	ui.custom = func(factory PromptComponentFactory, _ PromptCustomOptions) (UnattributedDecision, bool, error) {
		d := newRemoteDialog(factory, plainTheme{}, expandKeys{ctrlO}, nil)
		drive(d)
		decision, ok := d.outcome()
		return decision, ok, nil
	}
	return ui
}

func TestRemoteDialogBridge(t *testing.T) {
	prompt := func(ui *promptUIFake) (UnattributedDecision, error) {
		view := PermissionPromptView{PromptPreferences: defaultPromptPreferences(), Mode: "tui", UI: ui}
		view.DoublePressToConfirm = false
		return PresentInlinePermissionPrompt(view, "Title", makeAsk("/repo/secret.txt"), nil)
	}

	t.Run("a key press decides and closes the dialog with a value", func(t *testing.T) {
		var res bool
		ui := bridgeUI(t, func(d *remoteDialog) {
			r, err := d.HandleInput("y")
			if err != nil {
				t.Fatal(err)
			}
			res = r.Done && r.Value != nil
		})
		got, err := prompt(ui)
		eq(t, err, nil, "err")
		eq(t, got, UnattributedDecision{Approved: true, State: DecisionApproved}, "decision")
		eq(t, res, true, "done with a value")
	})
	t.Run("a Kitty key release never counts as the press", func(t *testing.T) {
		ui := bridgeUI(t, func(d *remoteDialog) {
			for _, release := range []string{"\x1b[121;1:3u", "\x1b[121;5:3u", "\x1b[110;1:3u"} {
				r, err := d.HandleInput(release)
				if err != nil || r.Done {
					t.Fatalf("release %q: %+v %v", release, r, err)
				}
			}
		})
		if _, err := prompt(ui); err == nil {
			t.Error("the dialog decided on releases alone")
		}
	})
	t.Run("a dialog the host closed without a decision is an error, never an approval", func(t *testing.T) {
		ui := bridgeUI(t, func(*remoteDialog) {})
		if got, err := prompt(ui); err == nil || got.Approved {
			t.Errorf("got %+v, %v", got, err)
		}
	})
	t.Run("a render request reaches the host's invalidation callback", func(t *testing.T) {
		invalidated := make(chan struct{}, 4)
		ui := bridgeUI(t, func(d *remoteDialog) {
			d.SetInvalidate(func() { invalidated <- struct{}{} })
			if _, err := d.HandleInput("\x1b[B"); err != nil { // moves the highlight: a re-render
				t.Fatal(err)
			}
		})
		_, _ = prompt(ui)
		select {
		case <-invalidated:
		case <-time.After(time.Second):
			t.Error("no render request")
		}
	})
	t.Run("rendering the bridge is the component's own render", func(t *testing.T) {
		var lines []string
		ui := bridgeUI(t, func(d *remoteDialog) { lines = d.Render(80) })
		_, _ = prompt(ui)
		if len(lines) == 0 {
			t.Error("no lines")
		}
	})
}
