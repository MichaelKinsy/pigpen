// Package sdkcheck proves at compile time that the public PiG Go SDK's ModelRegistry
// satisfies pigmodel.Registry, so an extension can hand ctx.ModelRegistry() to pigmodel.New.
// It is built by run.sh against a PiG checkout; it is not part of the typesafe module.
package sdkcheck

import (
	"github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/ownmodel"
	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/pigmodel"
)

var _ pigmodel.Registry = sdk.ModelRegistry{}

// ModelFor is what an extension writes: the session's model registry as an own-model Model.
func ModelFor(ctx sdk.Context, provider, id string) (ownmodel.Model, error) {
	return pigmodel.New(ctx.ModelRegistry(), pigmodel.Ref{Provider: provider, ID: id})
}
