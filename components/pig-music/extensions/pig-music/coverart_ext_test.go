package pig_music_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/charmbracelet/x/ansi"

	pig_music "github.com/MichaelKinsy/pigpen/pig-music"
	"github.com/MichaelKinsy/pigpen/pig-music/art"
	"github.com/MichaelKinsy/pigpen/pig-music/internal/mpvfake"
	"github.com/MichaelKinsy/pigpen/pig-music/mpv"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// countedCover is every track's cover, a PNG a frontend can draw, and the number of times one was asked for.
type countedCover struct {
	data []byte
	asks atomic.Int32
}

func newCountedCover(t *testing.T) *countedCover {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := range 16 {
		for x := range 16 {
			img.Set(x, y, color.RGBA{R: 200, G: uint8(10 * x), B: uint8(10 * y), A: 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return &countedCover{data: b.Bytes()}
}

func (c *countedCover) Image(context.Context, music.Track) (image.Image, error) {
	c.asks.Add(1)
	img, err := png.Decode(bytes.NewReader(c.data))
	if err != nil {
		return nil, err
	}
	return art.Picture{Image: img, Data: c.data, MimeType: "image/png"}, nil
}

// coverShown says whether a Player screen's frame shows the cover (half-block rows and the kit view's image annotation) or
// the disc (braille rows and no image), and fails when it is neither or both.
func coverShown(t *testing.T, s snapshot) bool {
	t.Helper()
	text := ansi.Strip(screenText(s))
	half := strings.Count(text, "▀") >= 20
	braille := strings.ContainsFunc(text, func(r rune) bool { return r >= 0x2800 && r <= 0x28FF })
	images := 0
	walkNodes(decodeView(t, s.View).Root, func(n wireNode) {
		if n.Image != nil {
			images++
		}
	})
	switch {
	case half && !braille && images == 1:
		return true
	case !half && braille && images == 0:
		return false
	}
	t.Fatalf("neither the cover nor the disc (half blocks %v, braille %v, %d images):\n%s", half, braille, images, text)
	return false
}

func playerSnapshot(s snapshot) bool { return s.View != nil && hasText("Up Next")(s) }

// toggleCoverArt opens /music settings (the nth overlay), switches Cover art and closes the box.
func toggleCoverArt(t *testing.T, h *overlayHost, env map[string]string, n int, want string) {
	t.Helper()
	done := h.command("music", "settings")
	h.waitOpenN(n)
	h.waitSnapshot("the quick settings", hasText("quick settings"))
	for range 6 {
		h.input("j") // down to Cover art
	}
	h.input(" ")
	waitFor(t, "coverArt "+want+" saved", func() bool {
		data, _ := os.ReadFile(settingsFile(env))
		return strings.Contains(string(data), `"coverArt": `+want)
	})
	if snap := h.waitSnapshot("cover art in the box", hasText("Cover art")); strings.Contains(screenText(snap), "takes effect after") {
		t.Errorf("cover art applies at once:\n%s", screenText(snap))
	}
	h.input("q")
	<-done
}

// The Cover art switch in /music settings reaches the running player: the Player screen swaps the cover (and the kit view's
// picture) for the disc and back with no /reload, and nothing is fetched while it is off.
func TestSwitchingCoverArtInTheBoxSwapsTheCoverAndTheDiscAtOnce(t *testing.T) {
	dir, err := os.MkdirTemp("", "pm") // short: the mpv socket's path has a limit
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	env := map[string]string{"XDG_RUNTIME_DIR": dir, "HOME": dir, "COLORTERM": "truecolor"}
	paths, err := music.DefaultPaths(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if err := paths.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	srv, err := mpvfake.Listen(paths.Socket, mpvfake.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	src := fixedSource{songs(5)}
	p := mpv.New(mpv.Config{Paths: paths, PlayURL: src.PlayURL})
	t.Cleanup(func() { _ = p.Close() })
	cover := newCountedCover(t)
	h := startHost(t, pig_music.ExtensionWithArt(src, p, cover, env), "tui", 100, 30, true)

	done := playFirstResult(t, h)
	if !coverShown(t, h.waitSnapshot("the cover", func(s snapshot) bool { return playerSnapshot(s) && strings.Contains(screenText(s), "▀") })) {
		t.Fatal("the player should start with the cover")
	}
	h.input("q")
	<-done
	asked := cover.asks.Load()

	toggleCoverArt(t, h, env, 2, "false")
	done = h.command("music")
	h.waitOpenN(3)
	if coverShown(t, h.waitSnapshot("the disc", playerSnapshot)) {
		t.Errorf("the running player still shows the cover after Cover art was switched off")
	}
	h.input("q")
	<-done
	if n := cover.asks.Load(); n != asked {
		t.Errorf("a cover was asked for %d times while Cover art was off", n-asked)
	}

	toggleCoverArt(t, h, env, 4, "true")
	done = h.command("music")
	h.waitOpenN(5)
	if !coverShown(t, h.waitSnapshot("the cover again", func(s snapshot) bool { return playerSnapshot(s) && strings.Contains(screenText(s), "▀") })) {
		t.Errorf("the running player does not show the cover after Cover art was switched back on")
	}
	h.input("q")
	<-done
}
