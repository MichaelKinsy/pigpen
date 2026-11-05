// artgen draws Pigtorio's own pixel art (deterministic, stdlib only).
//
//	artgen -out DIR      writes 1x frames: DIR/<name>/NN.png and DIR/<name>/ms
//	                     (ms = frame time; absent or 0 for still images)
//	artgen -contact OUT.png -frames DIR
//	                     composites frame 0 of every asset (all frames of runt/*,
//	                     hotbar icons on a slot) on grass, 4x, for eyeballing.
//
// gen.sh scales the frames 4x (nearest) and packs animated ones into WebP.
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type asset struct {
	name   string
	ms     int
	frames []*image.NRGBA
}

func hex(s string) color.NRGBA {
	var r, g, b, a uint8 = 0, 0, 0, 255
	switch len(s) {
	case 7:
		fmt.Sscanf(s, "#%02x%02x%02x", &r, &g, &b)
	case 9:
		fmt.Sscanf(s, "#%02x%02x%02x%02x", &r, &g, &b, &a)
	}
	return color.NRGBA{r, g, b, a}
}

func newImg(w, h int) *image.NRGBA { return image.NewNRGBA(image.Rect(0, 0, w, h)) }

func rect(m *image.NRGBA, x, y, w, h int, c string) {
	col := hex(c)
	for j := y; j < y+h; j++ {
		for i := x; i < x+w; i++ {
			if image.Pt(i, j).In(m.Rect) {
				m.SetNRGBA(i, j, col)
			}
		}
	}
}

func px(m *image.NRGBA, x, y int, c string) { rect(m, x, y, 1, 1, c) }

func flipH(m *image.NRGBA) *image.NRGBA {
	o := newImg(m.Rect.Dx(), m.Rect.Dy())
	for y := 0; y < m.Rect.Dy(); y++ {
		for x := 0; x < m.Rect.Dx(); x++ {
			o.SetNRGBA(m.Rect.Dx()-1-x, y, m.NRGBAAt(x, y))
		}
	}
	return o
}

// outline adds a 1px 4-neighbour outline around opaque pixels (in transparent pixels only).
func outline(m *image.NRGBA, c string) {
	col := hex(c)
	src := image.NewNRGBA(m.Rect)
	copy(src.Pix, m.Pix)
	for y := 0; y < m.Rect.Dy(); y++ {
		for x := 0; x < m.Rect.Dx(); x++ {
			if src.NRGBAAt(x, y).A != 0 {
				continue
			}
			for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
				nx, ny := x+d[0], y+d[1]
				if image.Pt(nx, ny).In(src.Rect) && src.NRGBAAt(nx, ny).A != 0 {
					m.SetNRGBA(x, y, col)
					break
				}
			}
		}
	}
}

func main() {
	out := flag.String("out", "", "write frames here")
	contact := flag.String("contact", "", "write contact sheet png")
	frames := flag.String("frames", "", "frames dir for -contact")
	flag.Parse()
	switch {
	case *out != "":
		for _, a := range all() {
			dir := filepath.Join(*out, a.name)
			os.RemoveAll(dir)
			must(os.MkdirAll(dir, 0o755))
			for i, f := range a.frames {
				write(filepath.Join(dir, fmt.Sprintf("%02d.png", i)), f)
			}
			if a.ms > 0 {
				must(os.WriteFile(filepath.Join(dir, "ms"), []byte(fmt.Sprint(a.ms)), 0o644))
			}
		}
	case *contact != "" && *frames != "":
		sheet(*contact, *frames)
	default:
		flag.Usage()
		os.Exit(2)
	}
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func write(path string, m image.Image) {
	f, err := os.Create(path)
	must(err)
	defer f.Close()
	must(png.Encode(f, m))
}

func all() []asset {
	var a []asset
	a = append(a, asset{"tiles/river", 150, river()})
	a = append(a, asset{"tiles/belt-h", 100, beltH()})
	a = append(a, asset{"tiles/belt-v", 100, beltV()})
	a = append(a, asset{"ui/barn", 0, []*image.NRGBA{barn()}})
	a = append(a, asset{"ui/oven", 140, oven()})
	a = append(a, asset{"ui/review", 0, []*image.NRGBA{review()}})
	a = append(a, asset{"ui/jobboard", 0, []*image.NRGBA{jobboard()}})
	a = append(a, asset{"ui/crate", 0, []*image.NRGBA{crate(false)}})
	a = append(a, asset{"ui/crate-burnt", 0, []*image.NRGBA{crate(true)}})
	a = append(a, asset{"ui/bridge", 0, []*image.NRGBA{bridge()}})
	a = append(a, asset{"ui/logo", 0, []*image.NRGBA{logo()}})
	a = append(a, asset{"ui/ghost", 0, []*image.NRGBA{ghost()}})
	for _, d := range []string{"down", "up", "left", "right"} {
		a = append(a, asset{"swineherd/idle-" + d, 600, swine(d, false)})
		a = append(a, asset{"swineherd/walk-" + d, 120, swine(d, true)})
	}
	a = append(a, asset{"runt/idle", 600, runtIdle()})
	a = append(a, asset{"runt/talk", 140, runtTalk()})
	for _, d := range []string{"down", "up", "left", "right"} {
		a = append(a, asset{"runt/walk-" + d, 120, runtWalk(d)})
		if d != "right" {
			a = append(a, asset{"ui/belt-arrow-" + d, 0, []*image.NRGBA{beltArrow(d)}})
		}
	}
	a = append(a, asset{"ui/belt-arrow", 0, []*image.NRGBA{beltArrow("right")}})
	a = append(a, asset{"ui/cursor-ok", 0, []*image.NRGBA{cursor("#4ed1a1")}})
	a = append(a, asset{"ui/cursor-bad", 0, []*image.NRGBA{cursor("#e05a4f")}})
	for _, h := range []struct {
		n string
		f func() *image.NRGBA
	}{
		{"pen", hotbarPen}, {"pig", hotbarPig}, {"belt", hotbarBelt}, {"oven", hotbarOven},
		{"review", hotbarReview}, {"barn", hotbarBarn}, {"jobboard", hotbarJobboard},
		{"remove", hotbarRemove}, {"runt", hotbarRunt}, {"hand", hotbarHand},
	} {
		a = append(a, asset{"hotbar/" + h.n, 0, []*image.NRGBA{h.f()}})
	}
	a = append(a, asset{"hotbar/slot", 0, []*image.NRGBA{hotbarSlot(false)}})
	a = append(a, asset{"hotbar/slot-on", 0, []*image.NRGBA{hotbarSlot(true)}})
	sort.Slice(a, func(i, j int) bool { return a[i].name < a[j].name })
	return a
}

func load(dir, name string, i int) image.Image {
	f, err := os.Open(filepath.Join(dir, name, fmt.Sprintf("%02d.png", i)))
	must(err)
	defer f.Close()
	im, err := png.Decode(f)
	must(err)
	return im
}

// onSlot composites a 16x16 hotbar icon centred on a 20x20 slot frame.
func onSlot(slot, icon image.Image) image.Image {
	m := newImg(20, 20)
	draw.Draw(m, m.Bounds(), slot, slot.Bounds().Min, draw.Over)
	draw.Draw(m, image.Rect(2, 2, 18, 18), icon, icon.Bounds().Min, draw.Over)
	return m
}

func sheet(outPath, dir string) {
	grass := hex("#2f6b45")
	var imgs []image.Image
	for _, a := range all() {
		switch {
		case strings.HasPrefix(a.name, "runt/"):
			// every frame: the pose changes are the point
			for i := range a.frames {
				imgs = append(imgs, load(dir, a.name, i))
			}
		case strings.HasPrefix(a.name, "hotbar/slot"):
			imgs = append(imgs, load(dir, a.name, 0))
		case strings.HasPrefix(a.name, "hotbar/"):
			imgs = append(imgs, onSlot(load(dir, "hotbar/slot", 0), load(dir, a.name, 0)))
		default:
			imgs = append(imgs, load(dir, a.name, 0))
		}
	}
	// selected slot with an icon, to judge the highlight
	imgs = append(imgs, onSlot(load(dir, "hotbar/slot-on", 0), load(dir, "hotbar/belt", 0)))
	const S, pad = 4, 6
	// simple shelf packing
	maxW := 1200
	x, y, rowH := pad, pad, 0
	type pl struct {
		x, y int
		im   image.Image
	}
	var ps []pl
	W, H := 0, 0
	for _, im := range imgs {
		w, h := im.Bounds().Dx()*S, im.Bounds().Dy()*S
		if x+w+pad > maxW {
			x, y, rowH = pad, y+rowH+pad, 0
		}
		ps = append(ps, pl{x, y, im})
		x += w + pad
		if h > rowH {
			rowH = h
		}
		if x > W {
			W = x
		}
		if y+rowH+pad > H {
			H = y + rowH + pad
		}
	}
	m := newImg(W, H)
	draw.Draw(m, m.Bounds(), &image.Uniform{grass}, image.Point{}, draw.Src)
	for _, p := range ps {
		b := p.im.Bounds()
		for j := 0; j < b.Dy()*S; j++ {
			for i := 0; i < b.Dx()*S; i++ {
				c := color.NRGBAModel.Convert(p.im.At(b.Min.X+i/S, b.Min.Y+j/S)).(color.NRGBA)
				if c.A == 0 {
					continue
				}
				d := m.NRGBAAt(p.x+i, p.y+j)
				a := int(c.A)
				m.SetNRGBA(p.x+i, p.y+j, color.NRGBA{
					uint8((int(c.R)*a + int(d.R)*(255-a)) / 255),
					uint8((int(c.G)*a + int(d.G)*(255-a)) / 255),
					uint8((int(c.B)*a + int(d.B)*(255-a)) / 255), 255})
			}
		}
	}
	write(outPath, m)
}
