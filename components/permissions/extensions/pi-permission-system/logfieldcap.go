package pi_permission_system

import "math"

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
func CapLogFieldWidths(details any, maxWidth float64) any { return capValue(details, maxWidth) }

// jsSliceEnd is the end index String.prototype.slice(0, end) uses for a string of the given length.
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
	if e > float64(length) {
		return length
	}
	return int(e)
}

func capValue(value any, maxWidth float64) any {
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
			out[i] = capValue(e, maxWidth)
		}
		return out
	case map[string]any:
		if v != nil {
			return capValue(orderedRecord(v), maxWidth)
		}
	case *jsObject:
		if v != nil {
			out := newObject()
			for _, k := range v.order() {
				out.set(k, capValue(v.vals[k], maxWidth))
			}
			return out
		}
	}
	return value
}
