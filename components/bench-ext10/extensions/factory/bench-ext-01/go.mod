module example.com/benchext10/factory/bench-ext-01

go 1.26

// PiG resolves the SDK requirement to the version-matched staged SDK at build time; the workspace supplies benchext.
require (
	example.com/benchext10/benchext v0.0.0
	github.com/MichaelKinsy/PiG/extensions/sdk v0.4.1
)
