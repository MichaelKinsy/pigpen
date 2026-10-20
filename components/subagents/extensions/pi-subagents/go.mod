module github.com/MichaelKinsy/pigpen/pi-subagents

go 1.26.0

// PiG resolves this requirement to the version-matched staged SDK at build time.
require github.com/MichaelKinsy/PiG/extensions/sdk v0.4.1

require gopkg.in/yaml.v3 v3.0.1
require github.com/dop251/goja v0.0.0-20261020130706-bae9ed654303

require (
	github.com/dlclark/regexp2/v2 v2.8.1 // indirect
	github.com/go-sourcemap/sourcemap v2.1.3+incompatible // indirect
	github.com/google/pprof v0.0.0-20230207041349-798e818bf904 // indirect
	golang.org/x/text v0.43.0 // indirect
)
