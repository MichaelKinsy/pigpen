// Package ohmypig is the oh-my-pig extension: omp's model-facing tools, prompt and commands on PiG's Go SDK.
// It is a Go port of parts of omp (https://github.com/can1357/oh-my-pi, MIT); CREDITS.md names the source of
// each part. Each area is a package under internal/ that registers itself; this file only lists them.
package ohmypig

import (
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/agents"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/ansi"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/approval"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/ask"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/ast"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/autolearn"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/autoresearch"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/browser"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/checkpoint"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/commit"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/debug"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/eval"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/files"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/github"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/intent"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lsp"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/modes"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/prediction"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/prompt"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/ratchet"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/search"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/settings"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/shell"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/ssh"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/tern"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/ternbrowser"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/todo"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/toolcmd"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/vibe"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/web"
)

// registrars are the areas of the extension, in registration order.
var registrars = []func(*sdk.Extension){
	settings.Register,
	urls.RegisterExtension,
	toolcmd.Register,
	prompt.Register,
	files.Register,
	search.Register,
	shell.Register,
	ssh.Register,
	approval.Register,
	ask.Register,
	todo.Register,
	web.Register,
	ast.Register,
	lsp.Register,
	debug.Register,
	github.Register,
	checkpoint.Register,
	autolearn.Register,
	eval.Register,
	commit.Register,
	ratchet.Register,
	agents.Register,
	modes.Register,
	vibe.Register,
	browser.Register,
	ternbrowser.Register,
	tern.Register,
	ansi.Register,
	autoresearch.Register,
	intent.RegisterLifecycle,
	prediction.Register,
}

// Extension returns the oh-my-pig extension.
func Extension() *sdk.Extension {
	wire()
	e := sdk.New("oh-my-pig")
	for _, register := range registrars {
		register(e)
	}
	// Single top-level session_shutdown hook releasing only the ending session
	// through internal/lifecycle (repeated End for one id is idempotent).
	e.OnSessionShutdown(func(ctx sdk.Context, _ map[string]any) (any, error) {
		if id, err := ctx.GetSessionID(); err == nil {
			if sid := strings.TrimSpace(id); sid != "" {
				lifecycle.End(sid)
			}
		}
		return nil, nil
	})
	return e
}
