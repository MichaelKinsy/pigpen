package stretchtext

import (
	"math"
	"strconv"
)

// The colour conversions below port the part of pi-tui's colors.ts and oklab.ts that turns a colour
// into an SGR sequence. Oklab is Björn Ottosson's colour space (https://bottosson.github.io/posts/colorpicker/,
// MIT, Copyright (c) 2021 Björn Ottosson).

var (
	labToLMS = [3][3]float64{
		{1, 0.3963377773761749, 0.2158037573099136},
		{1, -0.1055613458156586, -0.0638541728258133},
		{1, -0.0894841775298119, -1.2914855480194092},
	}
	lmsToLinearSrgb = [3][3]float64{
		{4.0767416360759583, -3.3077115392580629, 0.2309699031821043},
		{-1.2684379732850315, 2.6097573492876882, -0.341319376002657},
		{-0.0041960761386756, -0.7034186179359362, 1.7076146940746117},
	}
	basicColors = [16]rgb{
		{0, 0, 0}, {128, 0, 0}, {0, 128, 0}, {128, 128, 0}, {0, 0, 128}, {128, 0, 128}, {0, 128, 128}, {192, 192, 192},
		{128, 128, 128}, {255, 0, 0}, {0, 255, 0}, {255, 255, 0}, {0, 0, 255}, {255, 0, 255}, {0, 255, 255}, {255, 255, 255},
	}
	cubeValues = [6]float64{0, 95, 135, 175, 215, 255}
)

func linearToSrgb(v float64) float64 {
	if v > 0.0031308 {
		return 1.055*math.Pow(v, 1/2.4) - 0.055
	}
	return 12.92 * v
}

func oklabToLinearSrgb(l, a, b float64) [3]float64 {
	var cubed [3]float64
	for i, row := range labToLMS {
		lms := row[0]*l + row[1]*a + row[2]*b
		cubed[i] = lms * lms * lms
	}
	var out [3]float64
	for i, row := range lmsToLinearSrgb {
		out[i] = row[0]*cubed[0] + row[1]*cubed[1] + row[2]*cubed[2]
	}
	return out
}

func inGamut(linear [3]float64) bool {
	const epsilon = 1e-7
	for _, c := range linear {
		if !(c >= -epsilon && c <= 1+epsilon) {
			return false
		}
	}
	return true
}

func linearSrgbToRgb(linear [3]float64) rgb {
	ch := func(v float64) float64 { return jsRound(math.Min(1, math.Max(0, linearToSrgb(v))) * 255) }
	return rgb{ch(linear[0]), ch(linear[1]), ch(linear[2])}
}

// oklchToRgb maps an OKLCH colour into sRGB, keeping its hue and reducing chroma until it fits the
// gamut.
func oklchToRgb(c OKLCH) rgb {
	radians := (c.H * math.Pi) / 180
	cos, sin := math.Cos(radians), math.Sin(radians)
	atChroma := func(chroma float64) [3]float64 { return oklabToLinearSrgb(c.L, chroma*cos, chroma*sin) }
	if direct := atChroma(c.C); inGamut(direct) {
		return linearSrgbToRgb(direct)
	}
	linear := atChroma(0)
	low, high := 0.0, c.C
	for range 20 {
		chroma := (low + high) / 2
		if candidate := atChroma(chroma); inGamut(candidate) {
			low = chroma
			linear = candidate
		} else {
			high = chroma
		}
	}
	return linearSrgbToRgb(linear)
}

func indexedToRgb(index int) rgb {
	if index < 16 {
		return basicColors[max(index, 0)]
	}
	if index < 232 {
		cube := index - 16
		return rgb{cubeValues[cube/36], cubeValues[(cube%36)/6], cubeValues[cube%6]}
	}
	gray := float64(8 + (index-232)*10)
	return rgb{gray, gray, gray}
}

func findClosest(values []float64, target float64) int {
	closest, distance := 0, math.Inf(1)
	for i, v := range values {
		if d := math.Abs(target - v); d < distance {
			closest, distance = i, d
		}
	}
	return closest
}

func colorDistance(a, b rgb) float64 {
	dr, dg, db := a.r-b.r, a.g-b.g, a.b-b.b
	return dr*dr*0.299 + dg*dg*0.587 + db*db*0.114
}

func rgbToAnsi256(c rgb) int {
	ri := findClosest(cubeValues[:], c.r)
	gi := findClosest(cubeValues[:], c.g)
	bi := findClosest(cubeValues[:], c.b)
	cube := rgb{cubeValues[ri], cubeValues[gi], cubeValues[bi]}
	cubeIndex := 16 + 36*ri + 6*gi + bi
	grays := make([]float64, 24)
	for i := range grays {
		grays[i] = float64(8 + i*10)
	}
	gray := jsRound(0.299*c.r + 0.587*c.g + 0.114*c.b)
	offset := findClosest(grays, gray)
	g := grays[offset]
	spread := math.Max(c.r, math.Max(c.g, c.b)) - math.Min(c.r, math.Min(c.g, c.b))
	if spread < 10 && colorDistance(c, rgb{g, g, g}) < colorDistance(c, cube) {
		return 232 + offset
	}
	return cubeIndex
}

// foregroundAnsi is the SGR sequence selecting c as the foreground in a colour mode.
func foregroundAnsi(c rgb, mode string) string {
	if mode == "truecolor" {
		return "\x1b[38;2;" + channel(jsRound(c.r)) + ";" + channel(jsRound(c.g)) + ";" + channel(jsRound(c.b)) + "m"
	}
	return "\x1b[38;5;" + strconv.Itoa(rgbToAnsi256(c)) + "m"
}

func channel(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
