// Package seedcheck is a deliberately empty build-pipeline fixture.
// It adds no tools, commands, hooks, prompts or agent behavior. Replace it with
// the owner's curated resources before making this Piglet available.
package seedcheck

import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

func Extension() *sdk.Extension {
	return sdk.New("seed-check")
}
