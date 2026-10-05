// Command pigmusic is the pig-music player core with no UI: search YouTube Music,
// play, queue, pause and skip through a detached mpv that keeps playing after the
// command ends. It is also the one program that links the native engine (WaxTap, WaxFlow, oto): `pigmusic serve` is the
// native player's daemon and `pigmusic native` the helper the extension asks, so the extension itself stays small.
package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/MichaelKinsy/pigpen/pig-music/cli"
	"github.com/MichaelKinsy/pigpen/pig-music/native/wax"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(cli.Run(ctx, os.Args[1:], cli.IO{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}, cli.Env{Serve: wax.Serve, Native: wax.RunHelper}))
}
