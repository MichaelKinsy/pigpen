// Package pig_music is Pig Music, a full-screen music player for PiG.
//
// Milestone 2 ships only the hello screen: /music opens a full-terminal
// component that shows its size, echoes keys, redraws from a timer and closes on
// q. It proves the overlay, input and timer paths of the Go SDK before any
// player code depends on them.
package pig_music

import (
	"context"
	"fmt"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
	"github.com/MichaelKinsy/pigpen/pig-music/teahost"
	"github.com/MichaelKinsy/pigpen/pig-music/ui"
)

// Extension returns the extension.
func Extension() *sdk.Extension { return extensionWith(deps{}) }

func extensionWith(d deps) *sdk.Extension {
	ext := sdk.New("pig-music")
	player := newApp(d)
	diag := newHello(nil)
	ext.RegisterCommand("music", sdk.CommandOptions{
		Description:            "Open the pig-music player. Quick commands print one line and never open it: /music play <query|number>, pause, resume, toggle, next, prev, vol <0-100>, now, queue, shuffle on|off, repeat off|one|all, stop. /music settings: volume, shuffle, repeat, engine, library browser and more; /music setup: check this machine and offer to download yt-dlp and Deno; /music doctor: check only; /music hello: the layout diagnostic screen. Press q or Esc to hide the player; the music keeps playing.",
		GetArgumentCompletions: completions,
		Handler: func(ctx sdk.Context, args string) error {
			words := strings.Fields(args)
			if len(words) > 0 && quickWords[words[0]] {
				return player.quickCommand(ctx, words)
			}
			switch strings.TrimSpace(args) {
			case "hello":
				return open(ctx, diag)
			case "stop":
				return player.stop(ctx)
			case "setup":
				return player.setup(ctx, true)
			case "doctor":
				return player.setup(ctx, false)
			case "settings":
				return player.quick(ctx)
			case "":
				return show(ctx, player)
			}
			return unknownSubcommand(ctx, strings.TrimSpace(args))
		},
	})
	ext.Shortcut("alt+m", "Open the pig-music player", func(ctx sdk.Context) error { return show(ctx, player) })
	ext.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		player.onSessionStart(ctx)
		return nil, nil
	})
	ext.OnSessionShutdown(func(_ sdk.Context, data map[string]any) (any, error) {
		player.onSessionShutdown(data)
		return nil, nil
	})
	return ext
}

// stop is /music stop: end the music, whether or not this PiG session started it.
func (a *app) stop(ctx sdk.Context) error {
	a.foot.capture(ctx)
	stopped, err := a.stopMPV(context.Background())
	if stopped && err == nil {
		a.disown()
	}
	switch {
	case err != nil:
		ctx.Notify("pig-music: could not stop the player: "+err.Error(), "error")
	case stopped:
		ctx.Notify("pig-music: stopped.", "info")
	default:
		ctx.Notify("pig-music: nothing is playing.", "info")
	}
	return nil
}

// show opens the player over the whole terminal.
func show(ctx sdk.Context, a *app) error {
	if !ctx.HasUI() || ctx.Mode() != "tui" {
		ctx.Notify("pig-music needs an interactive terminal.", "warning")
		return nil
	}
	a.foot.capture(ctx)
	host, err := a.ensure(context.Background())
	if err != nil {
		ctx.Notify(err.Error(), "warning")
		return nil
	}
	a.claim() // this PiG plays the music now: its quit stops it (stopOnExit)
	a.foot.screen(true, a.currentState())
	defer func() { a.foot.screen(false, a.currentState()) }()
	if _, err := ctx.Custom(newScreen(a, host, ctx.Height), fullTerminal()); err != nil {
		return err
	}
	return nil
}

func open(ctx sdk.Context, screen *hello) error {
	// Print and JSON mode have no UI; RPC mode has dialogs but no custom
	// components, so only the interactive TUI can show the screen.
	if !ctx.HasUI() || ctx.Mode() != "tui" {
		ctx.Notify("pig-music needs an interactive terminal.", "warning")
		return nil
	}
	screen.mu.Lock()
	screen.height = ctx.Height
	screen.mu.Unlock()
	if _, err := ctx.Custom(screen, fullTerminal()); err != nil {
		return err
	}
	received, ticks := screen.Counters()
	ctx.Notify(fmt.Sprintf("pig-music hello closed: %d keys, %d timer redraws", received, ticks), "info")
	return nil
}

// fullTerminal places the overlay over the whole terminal: the full width and
// height, anchored to the top-left corner, no margin, no host frame.
func fullTerminal() sdk.RemoteOverlayOptions {
	none := 0
	return sdk.RemoteOverlayOptions{
		Overlay: true,
		OverlayOptions: &sdk.OverlayOptions{
			Width:     sdk.OverlayPercent(100),
			MaxHeight: sdk.OverlayPercent(100),
			Anchor:    "top-left",
			Margin:    &sdk.OverlayMargin{All: &none},
		},
	}
}

// quick is /music settings: a small centred overlay with the basic settings, for the status item that cannot take a key.
func (a *app) quick(ctx sdk.Context) error {
	if !ctx.HasUI() || ctx.Mode() != "tui" {
		ctx.Notify("pig-music needs an interactive terminal.", "warning")
		return nil
	}
	a.foot.capture(ctx)
	var scr *quickScreen
	host := teahost.New(ui.NewQuick(ui.QuickDeps{Player: a.livePlayer(context.Background()), Port: quickPort{a}}), teahost.Options{Redraw: func() {
		if scr != nil {
			scr.redraw()
		}
	}})
	host.Start(60, quickRows)
	defer host.Stop()
	scr = newQuickScreen(host, ctx.Height)
	if _, err := ctx.Custom(scr, smallOverlay()); err != nil {
		return err
	}
	return nil
}

// livePlayer is the player when one is running (attached now or found on its socket), nil otherwise: opening the settings
// never starts a music player.
func (a *app) livePlayer(ctx context.Context) music.Player {
	a.mu.Lock()
	p := a.player
	a.mu.Unlock()
	if p == nil {
		p = a.d.Player
	}
	if p != nil {
		return p
	}
	if a.running(ctx) {
		if _, err := a.ensure(ctx); err == nil {
			a.mu.Lock()
			defer a.mu.Unlock()
			return a.player
		}
	}
	return nil
}

// smallOverlay is a centred box of about 64 cells, as high as its rows need.
func smallOverlay() sdk.RemoteOverlayOptions {
	return sdk.RemoteOverlayOptions{
		Overlay: true,
		OverlayOptions: &sdk.OverlayOptions{
			Width:     sdk.OverlayCells(64),
			MinWidth:  40,
			MaxHeight: sdk.OverlayCells(20),
			Anchor:    "center",
		},
	}
}
