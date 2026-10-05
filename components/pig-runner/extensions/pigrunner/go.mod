module github.com/MichaelKinsy/pigpen/pigrunner

go 1.26

// PiG resolves the SDK requirement to the version-matched staged SDK at build time.
require github.com/MichaelKinsy/PiG/extensions/sdk v0.4.1

// The shared library module (pig-play), found through go.work in this directory.
require github.com/MichaelKinsy/pigpen/components/pig-play v0.0.0
