module github.com/MichaelKinsy/pigpen/pig-music/cmd/pigmusic

go 1.26.0

// A module of its own: PiG rejects an extension directory that holds both a factory and a
// standalone main package, so the command line sits beside the factory, not in its module.
require github.com/MichaelKinsy/pigpen/pig-music v0.0.0

require (
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/colespringer/waxflow v0.0.0-20260923050513-446ca3124d89 // indirect
	github.com/colespringer/waxlabel v1.8.0 // indirect
	github.com/colespringer/waxtap/v3 v3.6.0 // indirect
	github.com/dlclark/regexp2/v2 v2.5.2 // indirect
	github.com/dop251/goja v0.0.0-20260723142020-b4aef50fa347 // indirect
	github.com/ebitengine/oto/v3 v3.5.1 // indirect
	github.com/ebitengine/purego v0.11.0 // indirect
	github.com/go-sourcemap/sourcemap v2.1.4+incompatible // indirect
	github.com/google/pprof v0.0.0-20230207041349-798e818bf904 // indirect
	github.com/jfreymuth/pulse v0.1.3 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/MichaelKinsy/pigpen/pig-music => ../..
