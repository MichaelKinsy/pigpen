// Package frontend is oh-my-pig's frontend member: pig-tern's member with omp's look fixed on. The drawing
// itself lives in pig-tern's package, so both Piglets share one implementation of the Tern Surface Protocol.
package frontend

import (
	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/surface"
	ternfe "github.com/MichaelKinsy/pigpen/piglets/pig-tern/frontend"
)

// Frontend is the member's factory, the symbol the Piglet build registers.
func Frontend() fe.Frontend {
	return ternfe.FrontendWith(ternfe.Options{
		Omp:       true,
		OnSurface: surface.SetNative,
		Name:      "oh-my-pig",
		Version:   surface.Version,
		// The host gives a frontend the model id; the extension knows its name.
		ModelDisplayName: surface.ModelDisplayName,
		// Relative paths are resolved against the working directory as omp
		// spells it.
		DisplayPath: surface.DisplayPath,
	})
}
