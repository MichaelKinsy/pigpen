// Package frontend is oh-my-pig's frontend member: pig-tern's member with omp's look fixed on. The drawing
// itself lives in pig-tern's package, so both Piglets share one implementation of the Tern Surface Protocol.
package frontend

import (
	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	ternfe "github.com/MichaelKinsy/pigpen/piglets/pig-tern/frontend"
)

// Frontend is the member's factory, the symbol the Piglet build registers.
func Frontend() fe.Frontend { return ternfe.FrontendWith(ternfe.Options{Omp: true}) }
