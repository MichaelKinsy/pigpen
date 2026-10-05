package pig_music

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/pig-music/cli"
	"github.com/MichaelKinsy/pigpen/pig-music/mpv"
	"github.com/MichaelKinsy/pigpen/pig-music/ui"
)

// The quick commands are the command line's, run in this process: /music play, pause, resume, toggle, next, prev, vol, now,
// queue, shuffle and repeat go through cli.Run, so they behave exactly like `pigmusic` and share its code (the result cache of
// the last search, the engine choice, the doctor before mpv starts). They never open the overlay and answer with one line (queue:
// a short list) in a notification: a notification, not a conversation message, so the line is never sent to the model.
//
// /music like is not offered: liking a song writes to the account, which is a new cookie scope (the library only reads) and an
// action this extension has no consent for.
var quickWords = map[string]bool{
	"play": true, "pause": true, "resume": true, "toggle": true, "next": true, "prev": true, "vol": true,
	"now": true, "queue": true, "shuffle": true, "repeat": true,
}

// quickQueueLines is how many queue lines the notification shows, around the current track.
const quickQueueLines = 8

func (a *app) quickCommand(ctx sdk.Context, words []string) error {
	a.foot.capture(ctx)
	cctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var out, errOut bytes.Buffer
	code := cli.Run(cctx, words, cli.IO{Out: &out, Err: &errOut}, a.cliEnv())
	if code != 0 {
		ctx.Notify("pig-music: "+failureLine(errOut.String()), "warning")
		return nil
	}
	text := strings.TrimRight(out.String(), "\n")
	if words[0] == "queue" {
		ctx.Notify("pig-music queue:\n"+cleanLines(shortQueue(text, quickQueueLines)), "info")
	} else {
		ctx.Notify("pig-music: "+cleanLines(text), "info")
	}
	if words[0] == "play" {
		a.claim()                                             // this PiG plays the music now: its quit stops it (stopOnExit)
		go func() { _, _ = a.ensure(context.Background()) }() // follows the new music, so the footer or widget shows it
	}
	return nil
}

// cliEnv is the command line's environment as this extension has it (the tests' fakes included).
func (a *app) cliEnv() cli.Env {
	// The /music player drives mpv whatever "engine" says (the engine choice is wired into pigmusic only), so its quick
	// commands do too: a native player started here would be invisible to the overlay, the footer and stopOnExit.
	getenv := func(k string) string {
		if k == "PIG_MUSIC_ENGINE" {
			return "mpv"
		}
		return a.d.Getenv(k)
	}
	return cli.Env{
		Getenv: getenv, LookPath: a.d.LookPath, Source: a.d.Source, Doctor: a.d.Doctor, Name: "/music",
		Configure: func(c *mpv.Config) {
			c.Env = append(append([]string(nil), c.Env...), a.d.MPVEnv...)
			if a.d.MPVBin != "" && c.MPVPath == "" {
				c.MPVPath = a.d.MPVBin
			}
		},
	}
}

// failureLine is the first line of the command line's complaint, without its "pigmusic:" prefix and without the usage text
// that follows a usage mistake.
func failureLine(stderr string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(stderr), "\n")
	line = strings.TrimSpace(strings.TrimPrefix(line, "pigmusic:"))
	if line == "" {
		line = "the command failed"
	}
	return ui.Clean(line)
}

func cleanLines(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = ui.Clean(l)
	}
	return strings.Join(lines, "\n")
}

// shortQueue keeps n lines of the queue listing around the line marked with ">" and says how many it left out.
func shortQueue(text string, n int) string {
	lines := strings.Split(text, "\n")
	if len(lines) <= n {
		return text
	}
	at := 0
	for i, l := range lines {
		if strings.HasPrefix(l, ">") {
			at = i
			break
		}
	}
	from := min(max(at-n/4, 0), len(lines)-n)
	out := append([]string(nil), lines[from:from+n]...)
	if hidden := len(lines) - n; hidden > 0 {
		out = append(out, fmt.Sprintf("(%d more in the queue)", hidden))
	}
	return strings.Join(out, "\n")
}

// unknownSubcommand answers a word after /music that names nothing, in one line, instead of opening the player: a typo of a
// quick command (/music pasue) must not take over the terminal. /music like says why it is not offered.
func unknownSubcommand(ctx sdk.Context, text string) error {
	words := make([]string, 0, len(subcommands))
	for _, s := range subcommands {
		words = append(words, s.word)
	}
	known := "/music alone opens the player; the others are " + strings.Join(words, ", ")
	if first, _, _ := strings.Cut(text, " "); strings.EqualFold(first, "like") {
		ctx.Notify("pig-music: /music like is not offered: liking a song writes to your YouTube account, and pig-music only reads it (for the library). "+known, "warning")
		return nil
	}
	ctx.Notify(fmt.Sprintf("pig-music: unknown command %q; %s", ui.Clean(text), known), "warning")
	return nil
}

type subcommand struct{ word, help string }

var subcommands = []subcommand{
	{"play", "play a search result number, or search for words and play the first"},
	{"pause", "pause"},
	{"resume", "resume"},
	{"toggle", "pause or resume"},
	{"next", "next track"},
	{"prev", "previous track"},
	{"vol", "set the volume, 0 to 100"},
	{"now", "what is playing, in one line"},
	{"queue", "the queue, a short list"},
	{"shuffle", "shuffle on or off"},
	{"repeat", "repeat off, one or all"},
	{"stop", "stop the music"},
	{"settings", "volume, shuffle, repeat, engine, library browser and more"},
	{"setup", "check this machine and offer to download yt-dlp and Deno"},
	{"doctor", "check this machine"},
	{"hello", "the layout diagnostic screen"},
}

// completions are the suggestions for the text after "/music ". Values replace that whole text.
func completions(prefix string) ([]sdk.AutocompleteItem, error) {
	word, rest, spaced := strings.Cut(prefix, " ")
	var items []sdk.AutocompleteItem
	if !spaced {
		for _, s := range subcommands {
			if strings.HasPrefix(s.word, word) {
				items = append(items, sdk.AutocompleteItem{Value: s.word, Description: s.help})
			}
		}
		return items, nil
	}
	var options []string
	switch word {
	case "shuffle":
		options = []string{"on", "off"}
	case "repeat":
		options = []string{"off", "one", "all"}
	case "vol":
		options = []string{"0", "25", "50", "75", "100"}
	}
	for _, o := range options {
		if strings.HasPrefix(o, rest) {
			items = append(items, sdk.AutocompleteItem{Value: word + " " + o, Label: o})
		}
	}
	return items, nil
}
