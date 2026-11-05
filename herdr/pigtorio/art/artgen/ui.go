package main

import "image"

func barn() *image.NRGBA {
	m := newImg(36, 34)
	O, W, W2, R, R2 := "#4f1a17", "#8f2f2a", "#7a2824", "#5e3f22", "#774f2b"
	// walls
	rect(m, 2, 14, 32, 20, O)
	rect(m, 4, 15, 28, 18, W)
	for x := 8; x < 32; x += 4 {
		rect(m, x, 15, 1, 18, W2)
	}
	rect(m, 4, 15, 28, 1, "#a63a34")
	// roof: stepped triangle
	for i := 0; i < 14; i++ {
		w := 2 + i*2
		x := 17 - i
		if x < 0 {
			x = 0
		}
		if w+x > 36 {
			w = 36 - x
		}
		rect(m, x, i, w, 1, R)
	}
	for i := 0; i < 13; i += 3 {
		rect(m, 17-i+2, i+1, 2*i-2+2, 1, R2)
	}
	// roof outline eave
	rect(m, 0, 13, 36, 2, O)
	rect(m, 1, 13, 34, 1, R)
	// hayloft window
	rect(m, 14, 5, 8, 7, O)
	rect(m, 15, 6, 6, 5, "#2a0f0d")
	rect(m, 15, 6, 6, 1, "#e3b63b")
	// door
	rect(m, 10, 19, 16, 15, "#f3e3c3")
	rect(m, 11, 20, 14, 14, "#6f2420")
	for i := 0; i < 14; i++ {
		// X diagonals
		a := 11 + i*13/13
		px(m, a, 20+i, "#f3e3c3")
		px(m, 24-i*13/13+0, 20+i, "#f3e3c3")
		if a+1 < 25 {
			px(m, a+1, 20+i, "#f3e3c3")
		}
		if 23-i >= 11 {
			px(m, 23-i, 20+i, "#f3e3c3")
		}
	}
	rect(m, 2, 33, 32, 1, O)
	return m
}

func ovenFrame(f int) *image.NRGBA {
	m := newImg(22, 20)
	B, D := "#5a2a1a", "#2e1510"
	rect(m, 0, 4, 22, 16, D)
	rect(m, 1, 5, 20, 14, B)
	// bricks
	for y := 7; y < 19; y += 3 {
		rect(m, 1, y, 20, 1, D)
		off := (y / 3 % 2) * 3
		for x := 1 + off; x < 21; x += 6 {
			rect(m, x, y-2, 1, 2, D)
		}
	}
	rect(m, 1, 5, 20, 1, "#74382a")
	// chimney
	rect(m, 14, 0, 6, 6, D)
	rect(m, 15, 1, 4, 5, B)
	rect(m, 15, 1, 4, 1, "#74382a")
	// mouth
	rect(m, 6, 9, 10, 10, D)
	rect(m, 7, 8, 8, 1, D)
	rect(m, 7, 9, 8, 10, "#1a0b08")
	fire := [][]string{
		{"#e0561f", "#ffb347", "#ffd27a"},
		{"#e0561f", "#ffb347", "#ffe39a"},
		{"#c9461a", "#ffb347", "#ffd27a"},
		{"#e0561f", "#ff9a2e", "#ffd27a"},
	}[f]
	hs := [][3]int{{6, 5, 3}, {7, 6, 4}, {5, 4, 3}, {7, 5, 3}}[f]
	rect(m, 7, 19-hs[0]-1, 8, hs[0], fire[0])
	rect(m, 8, 19-hs[1]-1, 6, hs[1], fire[1])
	rect(m, 9+f%2, 19-hs[2]-1, 3, hs[2], fire[2])
	rect(m, 7, 18, 8, 1, fire[0])
	rect(m, 7, 18, 8, 1, "#ffb347")
	return m
}

func review() *image.NRGBA {
	m := newImg(22, 20)
	O, B, L := "#10202f", "#1f3d5a", "#2d5780"
	// stand
	rect(m, 8, 11, 6, 9, O)
	rect(m, 9, 12, 4, 7, B)
	rect(m, 5, 18, 12, 2, O)
	rect(m, 6, 18, 10, 1, B)
	// slanted top
	rect(m, 1, 3, 20, 9, O)
	rect(m, 2, 4, 18, 7, B)
	rect(m, 2, 4, 18, 1, L)
	// paper
	rect(m, 4, 1, 12, 10, "#10202f")
	rect(m, 5, 2, 10, 8, "#eef3f7")
	for y := 3; y < 9; y += 2 {
		rect(m, 6, y, 7, 1, "#9ec9ee")
	}
	rect(m, 6, 8, 3, 1, "#4ed1a1")
	// stamp
	rect(m, 15, 6, 5, 4, "#10202f")
	rect(m, 16, 7, 3, 2, "#e05a4f")
	return m
}

func jobboard() *image.NRGBA {
	m := newImg(38, 24)
	P, W, W2 := "#5e3f22", "#8b5a2b", "#a9743a"
	// posts
	rect(m, 5, 17, 3, 7, P)
	rect(m, 30, 17, 3, 7, P)
	rect(m, 6, 17, 1, 7, W)
	rect(m, 31, 17, 1, 7, W)
	// board
	rect(m, 0, 0, 38, 20, P)
	rect(m, 2, 2, 34, 16, W)
	for x := 2; x < 36; x += 1 {
		if x%7 == 3 {
			rect(m, x, 2, 1, 16, "#7a4e24")
		}
	}
	rect(m, 2, 2, 34, 1, W2)
	note := func(x, y, w, h int, c string, ink string, pin string) {
		rect(m, x, y, w, h, c)
		rect(m, x, y+h-1, w, 1, "#d9c79d")
		for j := y + 3; j < y+h-1; j += 2 {
			rect(m, x+1, j, w-3, 1, ink)
		}
		px(m, x+w/2, y, pin)
		px(m, x+w/2, y+1, "#b13a30")
	}
	note(4, 4, 9, 11, "#f3e3c3", "#a89570", "#e05a4f")
	note(15, 5, 8, 8, "#f6efd2", "#a89570", "#4ed1a1")
	note(25, 4, 9, 12, "#efe0b6", "#a89570", "#e3a63b")
	rect(m, 16, 14, 6, 3, "#f3e3c3")
	px(m, 17, 15, "#a89570")
	return m
}

func crate(burnt bool) *image.NRGBA {
	m := newImg(8, 8)
	if !burnt {
		rect(m, 0, 0, 8, 8, "#5e3f22")
		rect(m, 1, 1, 6, 6, "#8a5a3a")
		rect(m, 1, 1, 6, 1, "#a2704a")
		rect(m, 2, 2, 1, 1, "#5e3f22")
		for i := 1; i < 7; i++ {
			px(m, i, i, "#6f4a2e")
		}
		return m
	}
	rect(m, 0, 0, 8, 8, "#1a1210")
	rect(m, 1, 1, 6, 6, "#2f231c")
	rect(m, 1, 1, 6, 1, "#3d2e25")
	px(m, 2, 3, "#1a1210")
	px(m, 5, 5, "#1a1210")
	px(m, 3, 4, "#e0561f")
	px(m, 5, 2, "#ffb347")
	px(m, 2, 6, "#c9461a")
	px(m, 0, 0, "#00000000")
	px(m, 7, 7, "#00000000")
	return m
}

func bridge() *image.NRGBA {
	m := newImg(24, 10)
	rect(m, 0, 0, 24, 10, "#8b5a2b")
	rect(m, 0, 0, 24, 2, "#a9743a")
	rect(m, 0, 8, 24, 2, "#5e3f22")
	for x := 3; x < 24; x += 4 {
		rect(m, x, 2, 1, 6, "#5e3f22")
		px(m, x+1, 3, "#a2692f")
	}
	px(m, 1, 5, "#6d4520")
	px(m, 13, 4, "#6d4520")
	return m
}

func logo() *image.NRGBA {
	m := newImg(14, 13)
	rect(m, 2, 0, 3, 3, "#1f5c43")
	rect(m, 9, 0, 3, 3, "#1f5c43")
	rect(m, 1, 3, 12, 9, "#1f5c43")
	rect(m, 2, 4, 10, 7, "#4ed1a1")
	rect(m, 4, 5, 2, 2, "#ffffff")
	rect(m, 8, 5, 2, 2, "#ffffff")
	px(m, 5, 6, "#111111")
	px(m, 9, 6, "#111111")
	rect(m, 5, 8, 4, 3, "#8fe8c4")
	px(m, 6, 9, "#1f5c43")
	px(m, 8, 9, "#1f5c43")
	return m
}

func ghost() *image.NRGBA {
	m := newImg(22, 20)
	rect(m, 1, 1, 20, 18, "#ffffff22")
	c := "#ffffffcc"
	dash := func(i int) bool { return i%4 < 2 }
	for x := 0; x < 22; x++ {
		if dash(x) {
			px(m, x, 0, c)
			px(m, x, 19, c)
		}
	}
	for y := 0; y < 20; y++ {
		if dash(y) {
			px(m, 0, y, c)
			px(m, 21, y, c)
		}
	}
	return m
}

func oven() []*image.NRGBA {
	var fr []*image.NRGBA
	for f := 0; f < 4; f++ {
		fr = append(fr, ovenFrame(f))
	}
	return fr
}
