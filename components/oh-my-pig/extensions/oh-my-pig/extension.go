// Package ohmypig is the oh-my-pig extension: omp's model-facing tools, prompt and commands on PiG's Go SDK.
// It is a Go port of parts of omp (https://github.com/can1357/oh-my-pi, MIT); CREDITS.md names the source of
// each part. Each area is a package under internal/ that registers itself; this file only lists them.
package ohmypig

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/ask"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/files"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/prompt"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/search"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/shell"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/todo"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/web"
)

// registrars are the areas of the extension, in registration order. An area is added here when its package
// lands; nothing else in this file changes.
var registrars = []func(*sdk.Extension){
	prompt.Register,
	files.Register,
	search.Register,
	shell.Register,
	ask.Register,
	todo.Register,
	web.Register,
}

// Extension returns the oh-my-pig extension.
func Extension() *sdk.Extension {
	e := sdk.New("oh-my-pig")
	for _, register := range registrars {
		register(e)
	}
	return e
}
