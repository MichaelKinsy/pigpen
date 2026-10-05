// Package pitypesafe is the typed API of Pigpen's TypeSafe integration: a Go port of the
// library half of pi-typesafe (https://github.com/DevMortimer/pi-typesafe, MIT, Ryan
// Gapac). It wraps the shared client (components/typesafe) with what an extension needs
// around it: one admission rule for requests, request and spend caps that survive
// restarts, a key store and an auth state that says whether judgments are really
// happening, judgment backends (TypeSafe, OpenRouter, Command Code, a caller-supplied
// endpoint, and the model PiG is configured with), a never-throwing Ask, batching, and
// a calibration kit.
//
// Content sent through a TypeSafe, OpenRouter, Command Code or custom endpoint backend
// leaves the machine for that host; content sent through the own-model backend goes to
// the provider of the model PiG is configured with. ResolveBackend and BackendHost name
// the destination for consent text.
package pitypesafe
