module github.com/MichaelKinsy/pigpen/warden

go 1.26

// PiG resolves this requirement to the version-matched staged SDK at build time.
require github.com/MichaelKinsy/PiG/extensions/sdk v0.4.1

// The shared TypeSafe client (components/typesafe), found through go.work in this directory.
require github.com/MichaelKinsy/pigpen/components/typesafe v0.0.0

// The shared pi-tui port (components/pitui): key matching and ANSI-aware width and wrapping for the panels.
require github.com/MichaelKinsy/pigpen/components/pitui v0.0.0
