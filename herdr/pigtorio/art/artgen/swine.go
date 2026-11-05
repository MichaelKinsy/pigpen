package main

import "image"

// The Swineherd: 18x22 canvas (16x20 body + 1px outline margin).
// Straw hat, pink PiG pig face, red shirt, blue overalls, brown boots.
const (
	hatC, hatD, hatB = "#e3b63b", "#b98a22", "#b5483a"
	earC, faceC      = "#c46a85", "#f29ab0"
	backC, snoutC    = "#e88aa3", "#f8c4d2"
	nostrilC         = "#8a3d57"
	shirtC, shirtD   = "#c0392b", "#9a2c21"
	bibC, bibD       = "#3b6fb0", "#2a5088"
	buttonC          = "#f3d36b"
	bootC            = "#4a2f1a"
	outlineC         = "#2b1a14"
)

func swine(dir string, walk bool) []*image.NRGBA {
	var fr []*image.NRGBA
	n := 2
	if walk {
		n = 4
	}
	for f := 0; f < n; f++ {
		s := 0 // torso rise (0 or -1)
		var llift, rlift, larm, rarm int
		if walk {
			if f%2 == 1 {
				s = -1
			}
			switch f {
			case 0:
				llift, rlift, larm, rarm = 0, 1, 1, -1
			case 2:
				llift, rlift, larm, rarm = 1, 0, -1, 1
			}
		} else if f == 1 {
			s = -1
		}
		var m *image.NRGBA
		switch dir {
		case "down":
			m = swineFront(s, llift, rlift, larm, rarm, true)
		case "up":
			m = swineFront(s, llift, rlift, larm, rarm, false)
		case "left":
			m = swineSide(s, llift, rlift, larm)
		case "right":
			m = flipH(swineSide(s, llift, rlift, larm))
		}
		outline(m, outlineC)
		fr = append(fr, m)
	}
	return fr
}

// everything is drawn in body coords (0,0 = top-left of the 16x20 box); +1 for the margin.
type pen struct{ m *image.NRGBA }

func (p pen) r(x, y, w, h int, c string) { rect(p.m, x+1, y+1, w, h, c) }

func hat(p pen, T int, x0, x1 int) {
	// crown, band, brim
	p.r(x0+3, T, x1-x0-5, 1, hatC)
	p.r(x0+2, T+1, x1-x0-3, 1, hatC)
	p.r(x0+2, T+1, 1, 1, "#f3d46b")
	p.r(x0+2, T+2, x1-x0-3, 1, hatB)
	p.r(x0, T+3, x1-x0+1, 1, hatC)
	p.r(x0, T+3, 1, 1, hatD)
	p.r(x1, T+3, 1, 1, hatD)
	p.r(x0+1, T+4, x1-x0-1, 1, "#d98fa4") // brim shadow on forehead
}

func legs(p pen, T int, lx, rx, llift, rlift int, w int) {
	ground := 19
	for _, l := range [][3]int{{lx, llift, 0}, {rx, rlift, 1}} {
		x, lift := l[0], l[1]
		top := T + 16
		end := ground - lift
		for y := top; y <= end; y++ {
			c := bibD
			if y >= end-1 {
				c = bootC
			}
			p.r(x, y, w, 1, c)
		}
	}
}

func swineFront(s, llift, rlift, larm, rarm int, front bool) *image.NRGBA {
	m := newImg(18, 22)
	p := pen{m}
	T := 1 + s
	hat(p, T, 2, 13)
	// ears
	p.r(2, T+4, 2, 2, earC)
	p.r(12, T+4, 2, 2, earC)
	if front {
		p.r(4, T+4, 8, 6, faceC)
		p.r(5, T+6, 1, 2, "#111111")
		p.r(10, T+6, 1, 2, "#111111")
		p.r(5, T+6, 1, 1, "#ffffff")
		p.r(10, T+6, 1, 1, "#ffffff")
		p.r(6, T+7, 4, 3, snoutC)
		p.r(7, T+8, 1, 1, nostrilC)
		p.r(8, T+8, 1, 1, nostrilC)
		p.r(4, T+8, 1, 1, "#f27a98")
		p.r(11, T+8, 1, 1, "#f27a98")
	} else {
		p.r(4, T+4, 8, 6, backC)
		p.r(4, T+4, 8, 1, "#d98fa4")
	}
	// torso
	p.r(4, T+10, 8, 3, shirtC)
	p.r(4, T+12, 8, 1, shirtD)
	// arms (sleeve + hand)
	p.r(2, T+10+larm, 2, 3, shirtC)
	p.r(2, T+13+larm, 2, 2, faceC)
	p.r(12, T+10+rarm, 2, 3, shirtC)
	p.r(12, T+13+rarm, 2, 2, faceC)
	// overalls
	if front {
		p.r(5, T+10, 1, 2, bibC)
		p.r(10, T+10, 1, 2, bibC)
		p.r(5, T+11, 6, 5, bibC)
		p.r(4, T+13, 8, 3, bibC)
		p.r(5, T+11, 1, 1, buttonC)
		p.r(10, T+11, 1, 1, buttonC)
		p.r(7, T+13, 2, 2, bibD) // pocket
		p.r(7, T+13, 2, 1, "#4f84c8")
	} else {
		p.r(5, T+10, 2, 2, bibC)
		p.r(9, T+10, 2, 2, bibC)
		p.r(4, T+11, 8, 5, bibC)
		p.r(5, T+12, 6, 1, bibD)
		p.r(7, T+14, 2, 1, bibD)
		// curly tail
		p.r(12, T+14, 1, 1, faceC)
		p.r(13, T+13, 1, 1, faceC)
		p.r(8, T+11, 1, 1, buttonC)
	}
	legs(p, T, 4, 9, llift, rlift, 3)
	return m
}

func swineSide(s, llift, rlift, arm int) *image.NRGBA {
	// faces left
	m := newImg(18, 22)
	p := pen{m}
	T := 1 + s
	hat(p, T, 2, 12)
	// head
	p.r(5, T+4, 7, 6, faceC)
	p.r(5, T+4, 7, 1, "#d98fa4")
	p.r(2, T+7, 3, 3, snoutC) // snout
	p.r(2, T+8, 1, 1, nostrilC)
	p.r(6, T+6, 1, 2, "#111111")
	p.r(6, T+6, 1, 1, "#ffffff")
	p.r(5, T+8, 1, 1, "#f27a98")
	p.r(9, T+4, 3, 3, earC)
	// torso
	p.r(5, T+10, 7, 3, shirtC)
	p.r(5, T+12, 7, 1, shirtD)
	p.r(5, T+11, 7, 5, bibC)
	p.r(5, T+10, 2, 2, bibC)
	p.r(5, T+11, 1, 1, buttonC)
	p.r(5, T+13, 1, 2, bibD)
	// tail
	p.r(12, T+13, 1, 1, faceC)
	p.r(13, T+12, 1, 1, faceC)
	// legs: front leg toward the snout
	ground := 19
	for i, l := range []int{llift, rlift} {
		x := 5
		if i == 1 {
			x = 8
		}
		// stride: lifted leg swings back
		dx := 0
		if llift != 0 || rlift != 0 {
			if i == 0 {
				dx = -1 + 2*llift
			} else {
				dx = -1 + 2*rlift
			}
		}
		end := ground - l
		for y := T + 16; y <= end; y++ {
			c := bibD
			if y >= end-1 {
				c = bootC
			}
			p.r(x+dx, y, 3, 1, c)
			if y == end {
				p.r(x+dx-1, y, 1, 1, bootC)
			}
		}
	}
	// arm in front of the body
	ax := 7 - arm
	p.r(ax, T+10, 2, 3, shirtC)
	p.r(ax, T+13, 2, 2, faceC)
	return m
}
