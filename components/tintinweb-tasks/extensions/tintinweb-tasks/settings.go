package tintinweb_tasks

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

// settingsPanel is the /tasks → Settings component. upstream: ui/settings-menu.ts. A signature stub: the red
// commit's tests run against it and fail on behavior.
type settingsPanel struct{}

func (a *app) newSettingsPanel(cwd, agentDir string, theme sdk.UITheme) *settingsPanel {
	return &settingsPanel{}
}

func (p *settingsPanel) View(width int) kit.View { return kit.View{} }

func (p *settingsPanel) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	return sdk.RemoteComponentResult{}, nil
}

func (p *settingsPanel) HandleViewEvent(event kit.Event) (sdk.RemoteComponentResult, error) {
	return sdk.RemoteComponentResult{}, nil
}
