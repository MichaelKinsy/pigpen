package pigmodeltweaks

import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

// Stubs for the red commit: signatures only.

type pickerItem struct {
	value     string
	checked   bool
	available bool
}

type guardPicker struct {
	items []pickerItem
}

func newGuardPicker(title string, models []modelRef, checked []string) *guardPicker { return &guardPicker{} }

func pickerValues(p *guardPicker) []string { return nil }

func localeCompare(a, b string) int { return 0 }

func (p *guardPicker) Render(width int) []string { return nil }

func (p *guardPicker) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	return sdk.RemoteComponentResult{}, nil
}

type lockItem struct {
	id          string
	description string
}

type lockPicker struct{}

func newLockPicker(items []lockItem) *lockPicker { return &lockPicker{} }

func (p *lockPicker) Render(width int) []string { return nil }

func (p *lockPicker) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	return sdk.RemoteComponentResult{}, nil
}
