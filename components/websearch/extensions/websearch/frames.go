package websearch

import (
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

// Timestamp and frame planning of extract.ts (parseTimestamp :432, parseTimestampSpec :449,
// computeRangeTimestamps :463, buildFrameResult :477) with JavaScript's number semantics: a
// timestamp is a float64 so that "Infinity", "1e3" and "0x10" mean what Number() makes of them.

const (
	defaultRangeFrames = 6
	minFrameInterval   = 5
)

var jsDecimalRE = regexp.MustCompile(`^[+-]?(?:\d+\.?\d*(?:[eE][+-]?\d+)?|\.\d+(?:[eE][+-]?\d+)?)$`)
var jsRadixRE = regexp.MustCompile(`^0([xX][0-9a-fA-F]+|[oO][0-7]+|[bB][01]+)$`)
var jsFloatPrefixRE = regexp.MustCompile(`^[+-]?(?:Infinity|(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?)`)

// jsToNumber is Number(s) for a string; ok is false for NaN.
func jsToNumber(s string) (float64, bool) {
	s = jsTrim(s)
	switch s {
	case "":
		return 0, true
	case "Infinity", "+Infinity":
		return math.Inf(1), true
	case "-Infinity":
		return math.Inf(-1), true
	}
	if m := jsRadixRE.FindStringSubmatch(s); m != nil {
		base := map[byte]int{'x': 16, 'X': 16, 'o': 8, 'O': 8, 'b': 2, 'B': 2}[m[1][0]]
		n, ok := new(big.Int).SetString(m[1][1:], base)
		if !ok {
			return 0, false
		}
		f, _ := new(big.Float).SetInt(n).Float64()
		return f, true
	}
	if !jsDecimalRE.MatchString(s) {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil && !math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

// jsParseFloat is Number.parseFloat(s); ok is false for NaN.
func jsParseFloat(s string) (float64, bool) {
	s = strings.TrimLeftFunc(s, isJSSpace)
	m := jsFloatPrefixRE.FindString(s)
	if m == "" {
		return 0, false
	}
	switch strings.TrimLeft(m, "+") {
	case "Infinity":
		return math.Inf(1), true
	case "-Infinity":
		return math.Inf(-1), true
	}
	f, err := strconv.ParseFloat(m, 64)
	if err != nil && !math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

func isFinite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

// parseTimestamp is extract.ts parseTimestamp: "85", "23:45" and "1:23:45" in whole seconds.
func parseTimestamp(ts string) (float64, bool) {
	if n, ok := jsToNumber(ts); ok && n >= 0 {
		return math.Floor(n), true
	}
	parts := strings.Split(ts, ":")
	nums := make([]float64, len(parts))
	for i, p := range parts {
		n, ok := jsToNumber(p)
		if !ok || n < 0 {
			return 0, false
		}
		nums[i] = n
	}
	switch len(nums) {
	case 3:
		return math.Floor(nums[0]*3600 + nums[1]*60 + nums[2]), true
	case 2:
		return math.Floor(nums[0]*60 + nums[1]), true
	}
	return 0, false
}

// timestampSpec is a single timestamp or a start-end range.
type timestampSpec struct {
	Range      bool
	Seconds    float64
	Start, End float64
}

// parseTimestampSpec is extract.ts parseTimestampSpec.
func parseTimestampSpec(ts string) (timestampSpec, bool) {
	if ts != "" {
		first := len(string([]rune(ts)[:1]))
		if i := strings.IndexByte(ts[first:], '-'); i >= 0 {
			dash := first + i
			start, ok1 := parseTimestamp(ts[:dash])
			end, ok2 := parseTimestamp(ts[dash+1:])
			if ok1 && ok2 && end > start {
				return timestampSpec{Range: true, Start: start, End: end}, true
			}
		}
	}
	if seconds, ok := parseTimestamp(ts); ok {
		return timestampSpec{Seconds: seconds}, true
	}
	return timestampSpec{}, false
}

// computeRangeTimestamps is extract.ts computeRangeTimestamps: maxFrames evenly spaced seconds
// across start..end, at least five seconds apart.
func computeRangeTimestamps(start, end float64, maxFrames int) []float64 {
	if maxFrames <= 1 {
		return []float64{start}
	}
	ideal := (end - start) / float64(maxFrames-1)
	if ideal < minFrameInterval {
		var out []float64
		for t := start; t <= end && len(out) < maxFrames; t += minFrameInterval {
			out = append(out, t)
		}
		return out
	}
	out := make([]float64, maxFrames)
	for i := range out {
		out[i] = math.Floor(start + float64(i)*ideal + 0.5)
	}
	return out
}

// formatSecondsJS is utils.ts formatSeconds for a JavaScript number.
func formatSecondsJS(s float64) string {
	h := math.Floor(s / 3600)
	m := math.Floor(math.Mod(s, 3600) / 60)
	sec := math.Mod(s, 60)
	pad := func(f float64) string {
		str := jsNumberString(f)
		if len(str) < 2 {
			return "0" + str
		}
		return str
	}
	if h > 0 {
		return jsNumberString(h) + ":" + pad(m) + ":" + pad(sec)
	}
	return jsNumberString(m) + ":" + pad(sec)
}

// buildFrameResult is extract.ts buildFrameResult.
func buildFrameResult(url, label string, requested int, frames []VideoFrame, errMsg string, duration *float64) ExtractedContent {
	if len(frames) == 0 {
		msg := errMsg
		if msg == "" {
			msg = "Frame extraction failed"
		}
		return ExtractedContent{URL: url, Title: "Frames " + label + " (0/" + strconv.Itoa(requested) + ")", Content: msg, Error: errp(msg)}
	}
	n := strconv.Itoa(len(frames))
	return ExtractedContent{
		URL:      url,
		Title:    "Frames " + label + " (" + n + "/" + strconv.Itoa(requested) + ")",
		Content:  n + " frames extracted from " + label,
		Frames:   frames,
		Duration: duration,
	}
}

// jsToFixed1 is n.toFixed(1) for a non-negative finite n: ties go up, as the exact decimal
// expansion of the double decides.
func jsToFixed1(n float64) string {
	r := new(big.Rat).SetFloat64(n)
	r.Mul(r, big.NewRat(10, 1))
	scaled := new(big.Int).Div(new(big.Int).Add(new(big.Int).Mul(r.Num(), big.NewInt(2)), r.Denom()), new(big.Int).Mul(r.Denom(), big.NewInt(2)))
	s := scaled.String()
	if len(s) < 2 {
		s = "0" + s
	}
	return s[:len(s)-1] + "." + s[len(s)-1:]
}
