package pig_music

import (
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/MichaelKinsy/pigpen/pig-music/ui"
)

func TestOnlyLeftClicksAndTheWheelReachTheModel(t *testing.T) {
	ev := func(typ, button string, count, wheel int) sdk.MouseEvent {
		return sdk.MouseEvent{Type: typ, Button: button, X: 7, Y: 4, Width: 80, Height: 24, ClickCount: count, WheelDelta: wheel}
	}
	cases := []struct {
		name string
		in   sdk.MouseEvent
		want ui.MouseMsg
		ok   bool
	}{
		{"click", ev(sdk.MouseClick, sdk.MouseButtonLeft, 1, 0), ui.MouseMsg{X: 7, Y: 4, Clicks: 1}, true},
		{"click without a count", ev(sdk.MouseClick, sdk.MouseButtonLeft, 0, 0), ui.MouseMsg{X: 7, Y: 4, Clicks: 1}, true},
		{"double click", ev(sdk.MouseClick, sdk.MouseButtonLeft, 2, 0), ui.MouseMsg{X: 7, Y: 4, Clicks: 2}, true},
		{"wheel down", ev(sdk.MouseWheel, sdk.MouseButtonNone, 0, 3), ui.MouseMsg{X: 7, Y: 4, Wheel: 3}, true},
		{"wheel up", ev(sdk.MouseWheel, sdk.MouseButtonNone, 0, -1), ui.MouseMsg{X: 7, Y: 4, Wheel: -1}, true},
		{"wheel without lines", ev(sdk.MouseWheel, sdk.MouseButtonNone, 0, 0), ui.MouseMsg{}, false},
		{"press", ev(sdk.MousePress, sdk.MouseButtonLeft, 0, 0), ui.MouseMsg{}, false},
		{"release", ev(sdk.MouseRelease, sdk.MouseButtonLeft, 0, 0), ui.MouseMsg{}, false},
		{"move", ev(sdk.MouseMove, sdk.MouseButtonNone, 0, 0), ui.MouseMsg{}, false},
		{"drag", ev(sdk.MouseDrag, sdk.MouseButtonLeft, 0, 0), ui.MouseMsg{}, false},
		{"right click", ev(sdk.MouseClick, sdk.MouseButtonRight, 1, 0), ui.MouseMsg{}, false},
		{"middle click", ev(sdk.MouseClick, sdk.MouseButtonMiddle, 1, 0), ui.MouseMsg{}, false},
	}
	for _, c := range cases {
		got, ok := mouseMsg(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: %+v %v, want %+v %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestBothScreensTakeTheMouse(t *testing.T) {
	var _ sdk.MouseHandler = (*screen)(nil)
	var _ sdk.MouseHandler = (*quickScreen)(nil)
}
