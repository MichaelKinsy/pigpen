package langsmith

// Port of run_trees.js getMicrosecondPrecisionDatestring / convertToDottedOrderFormat / stripNonAlphanumeric
// (langsmith 0.10.5, run_trees.js:21-35).

import (
	"strconv"
	"time"
)

// jsMaxTimeMs is the largest |time value| a JavaScript Date accepts (ECMA-262 TimeClip).
const jsMaxTimeMs = 8_640_000_000_000_000

// nowMs is Date.now().
func nowMs() int64 { return time.Now().UnixMilli() }

// appendJSISOString appends new Date(epochMs).toISOString(): UTC, millisecond precision, always `Z`; years outside
// 0..9999 are written as a sign and six digits. It panics with the RangeError text of JavaScript when epochMs is not
// a valid time value (|epochMs| > 8.64e15), where `toISOString` throws.
func appendJSISOString(dst []byte, epochMs int64) []byte {
	if epochMs > jsMaxTimeMs || epochMs < -jsMaxTimeMs {
		panic("RangeError: Invalid time value")
	}
	t := time.UnixMilli(epochMs).UTC()
	y := t.Year()
	switch {
	case y >= 0 && y <= 9999:
		dst = appendPad(dst, y, 4)
	case y < 0:
		dst = append(dst, '-')
		dst = appendPad(dst, -y, 6)
	default:
		dst = append(dst, '+')
		dst = appendPad(dst, y, 6)
	}
	dst = append(dst, '-')
	dst = appendPad(dst, int(t.Month()), 2)
	dst = append(dst, '-')
	dst = appendPad(dst, t.Day(), 2)
	dst = append(dst, 'T')
	dst = appendPad(dst, t.Hour(), 2)
	dst = append(dst, ':')
	dst = appendPad(dst, t.Minute(), 2)
	dst = append(dst, ':')
	dst = appendPad(dst, t.Second(), 2)
	dst = append(dst, '.')
	dst = appendPad(dst, t.Nanosecond()/1e6, 3)
	return append(dst, 'Z')
}

func appendPad(dst []byte, v, width int) []byte {
	var tmp [20]byte
	s := strconv.AppendInt(tmp[:0], int64(v), 10)
	for i := len(s); i < width; i++ {
		dst = append(dst, '0')
	}
	return append(dst, s...)
}

// jsISOString is new Date(epochMs).toISOString() (see appendJSISOString).
func jsISOString(epochMs int64) string {
	var buf [32]byte
	return string(appendJSISOString(buf[:0], epochMs))
}

// appendMicrosecondPrecisionDatestring appends run_trees.js:24-28.
func appendMicrosecondPrecisionDatestring(dst []byte, epochMs int64, executionOrder int) []byte {
	dst = appendJSISOString(dst, epochMs)
	dst = dst[:len(dst)-1] // .slice(0, -1)
	// executionOrder.toFixed(0).slice(0, 3).padStart(3, "0")
	var tmp [20]byte
	s := strconv.AppendInt(tmp[:0], int64(executionOrder), 10)
	if len(s) > 3 {
		s = s[:3]
	}
	for i := len(s); i < 3; i++ {
		dst = append(dst, '0')
	}
	dst = append(dst, s...)
	return append(dst, 'Z')
}

// getMicrosecondPrecisionDatestring is run_trees.js:24-28: the ISO string without its final `Z`, then the
// execution order as three digits (`executionOrder.toFixed(0).slice(0, 3).padStart(3, "0")`), then `Z`.
func getMicrosecondPrecisionDatestring(epochMs int64, executionOrder int) string {
	var buf [40]byte
	return string(appendMicrosecondPrecisionDatestring(buf[:0], epochMs, executionOrder))
}

// convertToDottedOrderFormat is run_trees.js:29-35: dottedOrder is the datestring without `-`, `:` and `.`
// followed by runID.
func convertToDottedOrderFormat(epochMs int64, runID string, executionOrder int) (dottedOrder, microsecondPrecisionDatestring string) {
	var buf [40]byte
	ds := appendMicrosecondPrecisionDatestring(buf[:0], epochMs, executionOrder)
	microsecondPrecisionDatestring = string(ds)
	// stripNonAlphanumeric (run_trees.js:21-23): input.replace(/[-:.]/g, "")
	var obuf [96]byte
	out := obuf[:0]
	for _, c := range ds {
		if c != '-' && c != ':' && c != '.' {
			out = append(out, c)
		}
	}
	out = append(out, runID...)
	return string(out), microsecondPrecisionDatestring
}
