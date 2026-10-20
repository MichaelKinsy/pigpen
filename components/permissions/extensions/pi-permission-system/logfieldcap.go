package pi_permission_system

import (
	"errors"
	"math"
)

// Port of src/logging/log-field-cap.ts: the permission review log's width bound (ADR 0011 section 6). It narrows by length alone
// and never reads a value to decide what to shorten; it is not redaction.

// DefaultReviewLogFieldMaxWidth is the width when the operator configures none (DEFAULT_REVIEW_LOG_FIELD_MAX_WIDTH).
const DefaultReviewLogFieldMaxWidth = 1000

// ResolveReviewLogFieldWidth is the configured review-log field width (nil when unset), or the built-in default.
func ResolveReviewLogFieldWidth(reviewLogFieldMaxWidth *float64) float64 {
	if reviewLogFieldMaxWidth == nil {
		return DefaultReviewLogFieldMaxWidth
	}
	return *reviewLogFieldMaxWidth
}

// CapLogFieldWidths narrows every string in a log-detail record to maxWidth UTF-16 units. It recurses through plain objects and
// arrays and touches strings only; a shortened value ends in a bare ellipsis. The result is a copy: objects come back as *jsObject
// (a Go map becomes one with its keys sorted), arrays as []any.
func CapLogFieldWidths(details any, maxWidth float64) any { return capValue(details, maxWidth, 0) }

// jsSliceEnd is the end index String.prototype.slice(0, end) uses for a string of the given length, for an end below that length
// (the caller has already returned a string that fits).
func jsSliceEnd(end float64, length int) int {
	if math.IsNaN(end) {
		return 0
	}
	e := math.Trunc(end)
	if e < 0 {
		e += float64(length)
		if e < 0 {
			return 0
		}
	}
	return int(e)
}

func capValue(value any, maxWidth float64, depth int) any {
	checkLogDepth(depth)
	switch v := value.(type) {
	case string:
		n := utf16LenWTF(v)
		if float64(n) <= maxWidth {
			return v
		}
		return utf16Prefix(v, jsSliceEnd(maxWidth, n)) + "\u2026"
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = capValue(e, maxWidth, depth+1)
		}
		return out
	case map[string]any:
		if v != nil {
			return capValue(orderedRecord(v), maxWidth, depth)
		}
	case *jsObject:
		if v != nil {
			out := newObject()
			for _, k := range v.order() {
				out.set(k, capValue(v.vals[k], maxWidth, depth+1))
			}
			return out
		}
	}
	return value
}

// maxLogDepth stands in for the JavaScript engine's call-stack limit: the original's recursive walks over a record that contains
// itself end in a RangeError, which the log writer reports as a warning; a Go stack overflow cannot be recovered, so the walks
// stop here and panic with the same message.
const maxLogDepth = 4000

func checkLogDepth(depth int) {
	if depth > maxLogDepth {
		panic(errors.New("Maximum call stack size exceeded"))
	}
}
