package websearch

// Stub of the timestamp and frame planning of extract.ts: the twins fail until it is written.

const (
	defaultRangeFrames = 6
	minFrameInterval   = 5
)

func jsToNumber(s string) (float64, bool)   { return 0, false }
func jsParseFloat(s string) (float64, bool) { return 0, false }
func isFinite(f float64) bool               { return false }
func parseTimestamp(ts string) (float64, bool) {
	return 0, false
}

type timestampSpec struct {
	Range      bool
	Seconds    float64
	Start, End float64
}

func parseTimestampSpec(ts string) (timestampSpec, bool) { return timestampSpec{}, false }
func computeRangeTimestamps(start, end float64, maxFrames int) []float64 {
	return nil
}
func formatSecondsJS(s float64) string { return "" }
func buildFrameResult(url, label string, requested int, frames []VideoFrame, errMsg string, duration *float64) ExtractedContent {
	return ExtractedContent{}
}
func jsToFixed1(n float64) string { return "" }
