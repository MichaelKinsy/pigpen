module github.com/MichaelKinsy/pigpen/piglets/pig-tern/frontend

go 1.26.0

// Pig resolves this requirement to the version-matched staged SDK at build time.
// The frontend package first ships in PiG 0.4.2 (D91).
require github.com/MichaelKinsy/PiG/extensions/sdk v0.4.2

require (
	github.com/stencil-hq/tern-sdk/go/tern v0.1.0
	golang.org/x/text v0.35.0
)

require (
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/term v0.46.0 // indirect
)
