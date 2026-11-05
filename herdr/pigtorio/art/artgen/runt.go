package main

import "image"

// The Runt: a piglet foreman. 18x18 canvas (14x15 body + 1px outline margin,
// plus room top-right for the speech mark). Orange hard hat, pink piglet face,
// the Swineherd's red shirt, blue overalls and brown boots, a clipboard.
const (
	hardC, hardL, hardD = "#e8833a", "#f6a95a", "#b85f22"
	mouthC              = "#8a3d57"
	boardC, boardD      = "#8b5a2b", "#5e3f22"
	paperC, paperI      = "#f3e3c3", "#a89570"
	clipC               = "#b8c2c9"
)

// runtPose is one frame's pose.
type runtPose struct {
	s            int  // torso rise: 0 or -1
	llift, rlift int  // legs lifted (walk)
	larm         int  // free arm swing
	cdy          int  // clipboard dy (arm raise)
	open         bool // mouth open
	mark         bool // speech mark
}

func runtFrames(dir string, poses []runtPose) []*image.NRGBA {
	var fr []*image.NRGBA
	for _, po := range poses {
		var m *image.NRGBA
		switch dir {
		case "down":
			m = runtFront(po, true)
		case "up":
			m = flipH(runtFront(po, false))
		case "left":
			m = runtSide(po)
		case "right":
			m = flipH(runtSide(po))
		}
		outline(m, outlineC)
		if po.mark {
			speechMark(m)
		}
		fr = append(fr, m)
	}
	return fr
}

func runtIdle() []*image.NRGBA {
	return runtFrames("down", []runtPose{{s: 0}, {s: -1}})
}

func runtTalk() []*image.NRGBA {
	return runtFrames("down", []runtPose{
		{open: true, mark: true},
		{cdy: -1, mark: true},
		{open: true, cdy: -1, mark: true},
		{cdy: 0},
	})
}

func runtWalk(dir string) []*image.NRGBA {
	return runtFrames(dir, []runtPose{
		{llift: 0, rlift: 1, larm: 1},
		{s: -1},
		{llift: 1, rlift: 0, larm: -1},
		{s: -1},
	})
}

// speechMark: a tiny white bubble with "!" in the canvas's top-right corner.
func speechMark(m *image.NRGBA) {
	rect(m, 14, 0, 4, 6, outlineC)
	rect(m, 15, 1, 2, 4, "#ffffff")
	px(m, 15, 1, "#e05a4f")
	px(m, 15, 2, "#e05a4f")
	px(m, 15, 4, "#e05a4f")
}

func runtHat(p pen, T int) {
	p.r(5, T, 4, 1, hardC)
	p.r(4, T+1, 6, 1, hardC)
	p.r(3, T+2, 8, 1, hardC)
	p.r(5, T, 2, 1, hardL)
	p.r(4, T+1, 1, 1, hardL)
	p.r(6, T+1, 2, 2, hardL)  // ridge shine
	p.r(1, T+3, 12, 1, hardD) // brim
	p.r(3, T+2, 1, 1, hardD)
	p.r(10, T+2, 1, 1, hardD)
}

func runtClipboard(p pen, x, y int) {
	p.r(x, y, 3, 5, boardC)
	p.r(x, y, 1, 5, boardD)
	p.r(x+1, y+1, 2, 3, paperC)
	p.r(x+1, y+2, 1, 1, paperI)
	p.r(x+1, y+3, 2, 1, paperI)
	p.r(x+1, y, 1, 1, clipC)
}

func runtLegs(p pen, T, lx, rx, llift, rlift int) {
	ground := 15
	for i, x := range []int{lx, rx} {
		lift := llift
		if i == 1 {
			lift = rlift
		}
		end := ground - lift
		for y := T + 12; y <= end; y++ {
			c := bibD
			if y == end {
				c = bootC
			}
			p.r(x, y, 3, 1, c)
		}
	}
}

func runtFront(po runtPose, front bool) *image.NRGBA {
	m := newImg(18, 18)
	p := pen{m}
	T := 1 + po.s
	runtHat(p, T)
	p.r(2, T+4, 1, 2, earC)
	p.r(11, T+4, 1, 2, earC)
	if front {
		p.r(3, T+4, 8, 5, faceC)
		p.r(4, T+5, 1, 2, "#111111")
		p.r(9, T+5, 1, 2, "#111111")
		p.r(4, T+5, 1, 1, "#ffffff")
		p.r(9, T+5, 1, 1, "#ffffff")
		p.r(5, T+6, 4, 2, snoutC)
		p.r(6, T+7, 1, 1, nostrilC)
		p.r(7, T+7, 1, 1, nostrilC)
		p.r(4, T+7, 1, 1, "#f27a98")
		p.r(9, T+7, 1, 1, "#f27a98")
		if po.open {
			p.r(6, T+8, 2, 1, mouthC)
		}
	} else {
		p.r(3, T+4, 8, 5, backC)
		p.r(3, T+4, 8, 1, "#d98fa4")
	}
	// torso: shirt with a bib
	p.r(3, T+9, 8, 3, shirtC)
	p.r(3, T+11, 8, 1, shirtD)
	p.r(4, T+9, 6, 3, bibC)
	if front {
		p.r(4, T+9, 1, 1, buttonC)
		p.r(9, T+9, 1, 1, buttonC)
	} else {
		p.r(6, T+10, 2, 1, bibD)
		p.r(10, T+11, 1, 1, faceC) // tail (flipped to the other side)
		p.r(11, T+10, 1, 1, faceC)
	}
	// free arm
	p.r(2, T+9+po.larm, 1, 2, shirtC)
	p.r(2, T+11+po.larm, 1, 1, faceC)
	// clipboard arm
	runtClipboard(p, 11, T+8+po.cdy)
	p.r(11, T+11+po.cdy, 1, 1, faceC)
	runtLegs(p, T, 4, 7, po.llift, po.rlift)
	return m
}

func runtSide(po runtPose) *image.NRGBA {
	// faces left
	m := newImg(18, 18)
	p := pen{m}
	T := 1 + po.s
	runtHat(p, T)
	p.r(1, T+3, 1, 1, "#00000000")
	p.r(4, T+4, 7, 5, faceC)
	p.r(4, T+4, 7, 1, "#d98fa4")
	p.r(1, T+6, 3, 2, snoutC) // snout
	p.r(1, T+7, 1, 1, nostrilC)
	p.r(5, T+5, 1, 2, "#111111")
	p.r(5, T+5, 1, 1, "#ffffff")
	p.r(4, T+7, 1, 1, "#f27a98")
	p.r(8, T+4, 3, 3, earC)
	if po.open {
		p.r(2, T+8, 2, 1, mouthC)
	}
	// torso
	p.r(4, T+9, 7, 3, shirtC)
	p.r(4, T+11, 7, 1, shirtD)
	p.r(4, T+10, 7, 2, bibC)
	p.r(4, T+10, 1, 1, buttonC)
	// tail
	p.r(11, T+11, 1, 1, faceC)
	p.r(12, T+10, 1, 1, faceC)
	// legs
	ground := 15
	for i, l := range []int{po.llift, po.rlift} {
		x := 4
		if i == 1 {
			x = 7
		}
		dx := 0
		if po.llift != 0 || po.rlift != 0 {
			dx = -1 + 2*l
		}
		end := ground - l
		for y := T + 12; y <= end; y++ {
			c := bibD
			if y == end {
				c = bootC
			}
			p.r(x+dx, y, 3, 1, c)
			if y == end {
				p.r(x+dx-1, y, 1, 1, bootC)
			}
		}
	}
	// clipboard held in front of the chest, arm behind it
	runtClipboard(p, 2, T+8+po.cdy)
	p.r(5, T+11+po.cdy, 2, 1, shirtC)
	p.r(4, T+11+po.cdy, 1, 1, faceC)
	return m
}
