module github.com/MichaelKinsy/pigpen/ask-user-question

go 1.26.0

// PiG resolves this requirement to the version-matched staged SDK at build time.
require github.com/MichaelKinsy/PiG/extensions/sdk v0.5.3

// The shared pi-tui subset (components/pitui), found through go.work in this directory.
require github.com/MichaelKinsy/pigpen/components/pitui v0.0.0

require (
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/dlclark/regexp2 v1.12.0 // indirect
	golang.org/x/text v0.42.0 // indirect
)
