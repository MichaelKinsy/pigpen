module github.com/MichaelKinsy/pigpen/tintinweb-subagents

go 1.26

// PiG resolves this requirement to the version-matched staged SDK at build time.
require github.com/MichaelKinsy/PiG/extensions/sdk v0.4.1

require github.com/MichaelKinsy/pigpen/components/pitui v0.0.0
require github.com/dop251/goja v0.0.0-20260723142020-b4aef50fa347

require (
	github.com/dlclark/regexp2/v2 v2.5.2 // indirect
	github.com/go-sourcemap/sourcemap v2.1.3+incompatible // indirect
	github.com/google/pprof v0.0.0-20230207041349-798e818bf904 // indirect
	golang.org/x/text v0.3.8 // indirect
)
