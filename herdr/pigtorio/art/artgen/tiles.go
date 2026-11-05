package main

import "image/color"
import "image"

var _ = color.NRGBA{}

// river: 18x64 vertical water, streaks descend 8 px per frame (64 = 8 frames x 8 px), seamless.
func river() []*image.NRGBA {
	const W, H, N = 18, 64, 8
	type streak struct {
		x, y, l int
		c       string
	}
	ss := []streak{
		{3, 2, 5, "#4f8db8"}, {11, 6, 4, "#6fa8cf"}, {7, 15, 6, "#4f8db8"}, {14, 20, 4, "#4f8db8"},
		{2, 27, 4, "#6fa8cf"}, {9, 33, 5, "#6fa8cf"}, {15, 38, 6, "#4f8db8"}, {5, 44, 5, "#4f8db8"},
		{12, 51, 4, "#6fa8cf"}, {8, 58, 3, "#4f8db8"}, {1, 12, 3, "#3a7099"}, {16, 47, 3, "#3a7099"},
		{10, 24, 3, "#3a7099"}, {4, 54, 3, "#3a7099"},
	}
	var fr []*image.NRGBA
	for f := 0; f < N; f++ {
		m := newImg(W, H)
		rect(m, 0, 0, W, H, "#2c5f86")
		rect(m, 0, 0, 1, H, "#25506f")
		rect(m, W-1, 0, 1, H, "#25506f")
		for _, s := range ss {
			for k := 0; k < s.l; k++ {
				y := ((s.y+k+f*8)%H + H) % H
				px(m, s.x, y, s.c)
				if s.l > 3 && k > 0 && k < s.l-1 {
					px(m, s.x+1, y, s.c)
				}
			}
		}
		fr = append(fr, m)
	}
	return fr
}

func belt(w, h int, horiz bool) []*image.NRGBA {
	var fr []*image.NRGBA
	for f := 0; f < 6; f++ {
		m := newImg(w, h)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				var along, across int
				if horiz {
					along, across = x, y
				} else {
					along, across = y, x
				}
				across0 := h
				if !horiz {
					across0 = w
				}
				c := "#3a3a3a"
				if across == 0 || across == across0-1 {
					c = "#222222"
				} else if ((along-f)%6+6)%6 >= 3 {
					c = "#6b6b6b"
				}
				px(m, x, y, c)
			}
		}
		fr = append(fr, m)
	}
	return fr
}

func beltH() []*image.NRGBA { return belt(32, 6, true) }
func beltV() []*image.NRGBA { return belt(6, 32, false) }
