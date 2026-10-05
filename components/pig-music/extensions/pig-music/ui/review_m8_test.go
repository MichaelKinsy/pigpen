package ui

import (
	"fmt"
	"image"
	"image/color"
	"math/rand"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichaelKinsy/pigpen/pig-music/art"
)

// Review of M8 (rev-pig-music-m8): the contrast promise is about what the terminal draws, so it is checked cell by cell in
// the colours each mode really shows: 24-bit as written, the xterm-256 palette as xterm defines it (indices 16-255 are the
// same everywhere), and the 16 basic colours as xterm draws them by default (a user's theme can change those).

// xterm256RGB is the colour xterm draws for a 256-colour index.
func xterm256RGB(i int) art.RGB {
	if i < 16 {
		return xterm16RGB[i]
	}
	if i >= 232 {
		v := uint8(8 + 10*(i-232))
		return art.RGB{R: v, G: v, B: v}
	}
	i -= 16
	lv := []uint8{0, 95, 135, 175, 215, 255}
	return art.RGB{R: lv[i/36], G: lv[(i/6)%6], B: lv[i%6]}
}

var xterm16RGB = func() (out [16]art.RGB) {
	for i, c := range [16][3]uint8{
		{0, 0, 0}, {205, 0, 0}, {0, 205, 0}, {205, 205, 0}, {0, 0, 238}, {205, 0, 205}, {0, 205, 205}, {229, 229, 229},
		{127, 127, 127}, {255, 0, 0}, {0, 255, 0}, {255, 255, 0}, {92, 92, 255}, {255, 0, 255}, {0, 255, 255}, {255, 255, 255},
	} {
		out[i] = art.RGB{R: c[0], G: c[1], B: c[2]}
	}
	return out
}()

// cellCheck walks a painted line as a terminal would and reports the lowest contrast of a visible character (pictures, the
// cover's half blocks and shade ramp, excluded) and the first problem: a faint cell, or a cell in the terminal's own colours.
func cellCheck(line string) (worst float64, problem string) {
	worst = 99
	var fg, bg *art.RGB
	rev, faint := false, false
	set := func(params string) {
		ps := strings.Split(params, ";")
		num := func(i int) int { n, _ := strconv.Atoi(ps[i]); return n }
		for i := 0; i < len(ps); i++ {
			n := num(i)
			switch {
			case ps[i] == "" || n == 0:
				fg, bg, rev, faint = nil, nil, false, false
			case n == 2:
				faint = true
			case n == 22:
				faint = false
			case n == 7:
				rev = true
			case n == 27:
				rev = false
			case n == 39:
				fg = nil
			case n == 49:
				bg = nil
			case (n == 38 || n == 48) && i+1 < len(ps):
				var c art.RGB
				if num(i+1) == 2 && i+4 < len(ps) {
					c = art.RGB{R: uint8(num(i + 2)), G: uint8(num(i + 3)), B: uint8(num(i + 4))}
					i += 4
				} else if num(i+1) == 5 && i+2 < len(ps) {
					c = xterm256RGB(num(i + 2))
					i += 2
				}
				if n == 38 {
					fg = &c
				} else {
					bg = &c
				}
			case n >= 30 && n <= 37:
				c := xterm16RGB[n-30]
				fg = &c
			case n >= 90 && n <= 97:
				c := xterm16RGB[n-90+8]
				fg = &c
			case n >= 40 && n <= 47:
				c := xterm16RGB[n-40]
				bg = &c
			case n >= 100 && n <= 107:
				c := xterm16RGB[n-100+8]
				bg = &c
			}
		}
	}
	for i := 0; i < len(line); {
		if strings.HasPrefix(line[i:], "\x1b[") {
			j := strings.IndexByte(line[i:], 'm')
			set(line[i+2 : i+j])
			i += j + 1
			continue
		}
		r, size := rune(line[i]), 1
		if r >= 0x80 {
			rs := []rune(line[i:])
			r, size = rs[0], len(string(rs[0]))
		}
		i += size
		if r == ' ' || strings.ContainsRune("▀▄░▒▓█", r) {
			continue
		}
		if faint && problem == "" {
			problem = fmt.Sprintf("%q is faint", r)
		}
		if fg == nil || bg == nil {
			if problem == "" {
				problem = fmt.Sprintf("%q is in the terminal's own colours", r)
			}
			continue
		}
		f, b := *fg, *bg
		if rev {
			f, b = b, f
		}
		worst = min(worst, art.Contrast(f, b))
	}
	return worst, problem
}

// covers are cover colours a seeded generator picks, plus the awkward ones.
func reviewCovers() []color.Color {
	cs := []color.Color{color.RGBA{200, 30, 40, 255}, color.RGBA{30, 60, 210, 255}, color.RGBA{128, 128, 128, 255},
		color.RGBA{255, 255, 0, 255}, color.RGBA{0, 0, 255, 255}, color.RGBA{60, 40, 30, 255}, color.RGBA{255, 255, 255, 255}}
	r := rand.New(rand.NewSource(8))
	for len(cs) < 40 {
		cs = append(cs, color.RGBA{uint8(r.Intn(256)), uint8(r.Intn(256)), uint8(r.Intn(256)), 255})
	}
	return cs
}

func TestEveryCellIsReadableInTheColoursEachModeReallyDraws(t *testing.T) {
	for _, mode := range []art.Mode{art.TrueColor, art.Color16, art.Color256} {
		worst := 99.0
		for ci, c := range reviewCovers() {
			r, lp, _ := pulseRig(t, func(d *Deps) {
				d.ArtMode = mode
				d.Art = &fakeArt{imgs: map[string]image.Image{"id00": paint(c)}}
			})
			r.state(playing(tracks(3), 0))
			r.send(ShowPlayerMsg{})
			for _, beat := range []float64{0, 0.9} { // quiet, then the top of a beat
				if beat > 0 {
					lp.setLevel(0.02)
					r.send(TickMsg{})
					lp.setLevel(0.9)
					r.send(TickMsg{})
				}
				for row, l := range r.screen() {
					w, problem := cellCheck(l)
					if problem != "" {
						t.Fatalf("mode %d cover %d row %d: %s", mode, ci, row, problem)
					}
					worst = min(worst, w)
					if w < 4.5 {
						t.Errorf("mode %d cover %v row %d: a character at contrast %.2f (the promise is 4.5, 7 for text)", mode, c, row, w)
						break
					}
				}
			}
			if t.Failed() {
				return
			}
		}
		t.Logf("mode %d: the lowest contrast of any character is %.2f", mode, worst)
	}
}

func TestTheSearchListSettingsAndHelpAreReadableIn256Colours(t *testing.T) {
	r, _, _ := pulseRig(t, func(d *Deps) { d.ArtMode = art.Color256; d.Art = redCover() })
	r.state(playing(tracks(3), 0))
	r.send(ShowPlayerMsg{})
	check := func(what string) {
		t.Helper()
		for row, l := range r.screen() {
			if w, problem := cellCheck(l); problem != "" || w < 4.5 {
				t.Errorf("%s row %d: contrast %.2f %s", what, row, w, problem)
				return
			}
		}
	}
	check("player")
	r.send(tea.KeyPressMsg(keyOf("o")))
	check("settings")
	r.send(tea.KeyPressMsg(keyOf("esc")))
	r.send(tea.KeyPressMsg(keyOf("?")))
	check("help")
}

// The frame cap is about what the host repaints: the host repaints after every update that changed the screen, so a tick
// that turns the disc and the level reading it asks for must not each change it. At most one change per tick.
func TestEachPulseTickChangesTheScreenAtMostOnce(t *testing.T) {
	for _, low := range []bool{false, true} {
		r, lp, _ := pulseRig(t, func(d *Deps) { d.LowPower = low })
		r.state(playing(tracks(3), 0))
		r.send(ShowPlayerMsg{})
		most := 0
		for i := 0; i < 24; i++ {
			lp.setLevel(0.1 + 0.4*float64(i%4)/3) // a beat every fourth reading
			before := r.m.View().Content
			changes := 0
			queue := []tea.Msg{TickMsg{}}
			for len(queue) > 0 {
				var cmd tea.Cmd
				r.m, cmd = r.m.Update(queue[0])
				queue = queue[1:]
				if v := r.m.View().Content; v != before {
					changes, before = changes+1, v
				}
				if cmd == nil {
					continue
				}
				switch out := cmd().(type) {
				case nil:
				case tea.BatchMsg:
					for _, c := range out {
						if c != nil {
							if msg := c(); msg != nil {
								queue = append(queue, msg)
							}
						}
					}
				default:
					queue = append(queue, out)
				}
			}
			most = max(most, changes)
		}
		if most > 1 {
			t.Errorf("lowPower=%v: one tick changed the screen %d times, so the host repaints more often than the tick (measured with the real host: 26 a second at a 15 fps tick, 11.5 in low power)", low, most)
		}
	}
}

// runCmd runs a command and every command of a batch, and drops what they answer.
func runCmd(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	if b, ok := cmd().(tea.BatchMsg); ok {
		for _, c := range b {
			runCmd(c)
		}
	}
}

// The host runs every command in its own goroutine, so the switch-on and the switch-off of measuring can reach the player
// in either order. Whatever the order, the player must end up as the model last asked: here hidden, so not measuring.
func TestMeasuringEndsAsTheModelLastAskedWhateverOrderTheCommandsRun(t *testing.T) {
	for _, reversed := range []bool{false, true} {
		r, lp, _ := pulseRig(t, nil)
		var on, off tea.Cmd
		r.m, on = r.m.Update(StateMsg(playing(tracks(3), 0)))
		r.m, off = r.m.Update(ClosedMsg{})
		if reversed {
			runCmd(off)
			runCmd(on)
		} else {
			runCmd(on)
			runCmd(off)
		}
		s := sets(lp)
		if s == "" || s[len(s)-1] != '-' {
			t.Errorf("reversed=%v: the player was told %q and is left measuring while the screen is hidden", reversed, s)
		}
	}
}
