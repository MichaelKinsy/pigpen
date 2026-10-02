// Package ask is a test fixture: /ask opens a blocking confirmation so the
// herdr scenario can observe the blocked state.
package ask

import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

func Extension() *sdk.Extension {
	e := sdk.New("ask")
	e.Command("ask", "open a confirm dialog", func(ctx sdk.Context, _ string) error {
		_, err := ctx.Confirm("Allow rm -rf build?", "The agent wants to run it")
		return err
	})
	return e
}
