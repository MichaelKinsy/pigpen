module github.com/MichaelKinsy/pigpen/warden

go 1.26

// PiG resolves this requirement to the version-matched staged SDK at build time.
require github.com/MichaelKinsy/PiG/extensions/sdk v0.4.1

// The shared TypeSafe client (components/typesafe), found through go.work in this directory.
require github.com/MichaelKinsy/pigpen/components/typesafe v0.0.0

// The shared pi-tui port (components/pitui): key matching and ANSI-aware width and wrapping for the panels.
require github.com/MichaelKinsy/pigpen/components/pitui v0.0.0

// Pure-Go SQLite for the learning database (holds.db, the file the original writes with node:sqlite).
require modernc.org/sqlite v1.60.1

require (
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/dlclark/regexp2 v1.12.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	modernc.org/libc v1.77.1 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)
