module github.com/MichaelKinsy/pigpen/components/pi-typesafe/extensions/pi-typesafe

go 1.26

// PiG resolves this requirement to the version-matched staged SDK at build time.
require github.com/MichaelKinsy/PiG/extensions/sdk v0.5.3

// The typed API (components/pi-typesafe-api) and, through it, the shared client (components/typesafe),
// found through go.work in this directory.
require github.com/MichaelKinsy/pigpen/components/pi-typesafe-api v0.0.0

require github.com/MichaelKinsy/pigpen/components/typesafe v0.0.0
