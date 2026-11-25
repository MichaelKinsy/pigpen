module github.com/MichaelKinsy/pigpen/piglets/oh-my-pig/frontend

go 1.26.0

require github.com/MichaelKinsy/PiG/extensions/sdk v0.5.3

require (
	github.com/MichaelKinsy/pigpen/oh-my-pig v0.0.0
	github.com/MichaelKinsy/pigpen/piglets/pig-tern/frontend v0.0.0
)

require (
	github.com/stencil-hq/tern-sdk/go/tern v0.2.1-0.20261123044329-25444b122bfb // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/term v0.46.0 // indirect
	golang.org/x/text v0.35.0 // indirect
)

replace (
	github.com/MichaelKinsy/pigpen/oh-my-pig => ../../../components/oh-my-pig/extensions/oh-my-pig
	github.com/MichaelKinsy/pigpen/piglets/pig-tern/frontend => ../../pig-tern/frontend
)
