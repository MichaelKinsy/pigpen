module github.com/MichaelKinsy/pigpen/jev

go 1.26

// PiG resolves the SDK requirement to the version-matched staged SDK at build time.
require github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0

// The shared TypeSafe client (components/typesafe), found through go.work in this directory.
require github.com/MichaelKinsy/pigpen/components/typesafe v0.0.0
