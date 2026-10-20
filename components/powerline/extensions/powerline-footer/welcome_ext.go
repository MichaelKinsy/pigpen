package powerline_footer

import (
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Red stub: the lifecycle hooks of welcome_ext.go with no behavior.

type welcomeTimers interface {
	After(d time.Duration, f func()) (cancel func())
	Every(d time.Duration, f func()) (cancel func())
}

var welcomeClock welcomeTimers

type welcomeState struct{ mu sync.Mutex }

func (a *app) welcomeDismiss()                                    {}
func (a *app) welcomeEndSession()                                 {}
func (a *app) welcomeBeginSession()                               {}
func (a *app) welcomeOverlayMounted() bool                        { return false }
func (a *app) welcomeSessionStart(ctx sdk.Context, reason string) {}
func registerWelcome(e *sdk.Extension, a *app)                    {}
