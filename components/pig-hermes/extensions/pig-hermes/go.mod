module github.com/MichaelKinsy/pigpen/pig-hermes

go 1.26

// PiG resolves this requirement to the version-matched staged SDK at build time.
require github.com/MichaelKinsy/PiG/extensions/sdk v0.8.0

// Shared library module (pig-hermes-core), found through go.work in this directory.
require github.com/MichaelKinsy/pigpen/components/pig-hermes-core v0.0.0
