package autoresearch

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// upstream: packages/coding-agent/src/autoresearch/helpers.ts:4-11
const (
	MetricLinePrefix   = "METRIC"
	ASILinePrefix      = "ASI"
	ExperimentMaxLines = 10
	ExperimentMaxBytes = 4 * 1024
	DefaultMaxLines    = 3000
	DefaultMaxBytes    = 50 * 1024
)

var (
	deniedKeyNames = map[string]bool{
		"__proto__":   true,
		"constructor": true,
		"prototype":   true,
	}
	metricLineRe = regexp.MustCompile(`^METRIC[ \t\f\v\r\n]+([\w.µ-]+)=([^\s]+)`)
	asiLineRe    = regexp.MustCompile(`^ASI[ \t\f\v\r\n]+([\w.-]+)=([^\r\n\x{2028}\x{2029}]+)`)
	asiNumberRe  = regexp.MustCompile(`^-?\d+(?:\.\d+)?$`)
)

// ExperimentOutputScanner is the streaming equivalent of ParseMetricLines + ParseASILines.
// upstream: packages/coding-agent/src/autoresearch/helpers.ts:27-84
type ExperimentOutputScanner struct {
	carry   string
	metrics map[string]float64
	asi     ASIData
}

// NewExperimentOutputScanner creates a new streaming output scanner.
func NewExperimentOutputScanner() *ExperimentOutputScanner {
	return &ExperimentOutputScanner{
		metrics: make(map[string]float64),
		asi:     make(ASIData),
	}
}

// Append feeds an output chunk into the scanner.
// upstream: packages/coding-agent/src/autoresearch/helpers.ts:34-38
func (s *ExperimentOutputScanner) Append(chunk string) {
	if chunk == "" {
		return
	}
	s.carry += chunk
	s.scan(false)
}

// Finish flushes any buffered trailing line and returns parsed metrics and ASI.
// upstream: packages/coding-agent/src/autoresearch/helpers.ts:40-43
func (s *ExperimentOutputScanner) Finish() (map[string]float64, ASIData) {
	s.scan(true)
	var asi ASIData
	if len(s.asi) > 0 {
		asi = s.asi
	}
	return s.metrics, asi
}

func indexOfLineTerminator(s string, from int) int {
	for i := from; i < len(s); i++ {
		if s[i] == '\n' || s[i] == '\r' {
			return i
		}
		// U+2028 (E2 80 A8) and U+2029 (E2 80 A9)
		if s[i] == 0xe2 && i+2 < len(s) && s[i+1] == 0x80 && (s[i+2] == 0xa8 || s[i+2] == 0xa9) {
			return i
		}
	}
	return -1
}

func lineTerminatorLen(s string, idx int) int {
	if idx < 0 || idx >= len(s) {
		return 0
	}
	if s[idx] == 0xe2 && idx+2 < len(s) && s[idx+1] == 0x80 && (s[idx+2] == 0xa8 || s[idx+2] == 0xa9) {
		return 3
	}
	return 1
}

func indexOfNonWhitespace(s string, from int) int {
	for i := from; i < len(s); i++ {
		c := s[i]
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' && c != '\f' && c != '\v' {
			return i
		}
	}
	return -1
}

func (s *ExperimentOutputScanner) scan(final bool) {
	carry := s.carry
	pos := 0
	for pos < len(carry) {
		lineEnd := indexOfLineTerminator(carry, pos)
		if lineEnd < 0 && !final {
			break
		}
		prefixLen := 0
		if strings.HasPrefix(carry[pos:], MetricLinePrefix) {
			prefixLen = len(MetricLinePrefix)
		} else if strings.HasPrefix(carry[pos:], ASILinePrefix) {
			prefixLen = len(ASILinePrefix)
		}
		if prefixLen > 0 {
			valueStart := indexOfNonWhitespace(carry, pos+prefixLen)
			valueLineEnd := -1
			if valueStart >= 0 {
				valueLineEnd = indexOfLineTerminator(carry, valueStart)
			}
			if valueLineEnd < 0 && !final {
				break
			}
			s.matchAt(carry[pos:])
		}
		if lineEnd < 0 {
			pos = len(carry)
			break
		}
		pos = lineEnd + lineTerminatorLen(carry, lineEnd)
	}
	if pos > 0 {
		s.carry = carry[pos:]
	}
}

func (s *ExperimentOutputScanner) matchAt(sub string) {
	// Check METRIC: after `METRIC\s+<name>=<non-ws>`, only horizontal whitespace may remain before line terminator.
	if loc := metricLineRe.FindStringSubmatchIndex(sub); loc != nil && loc[0] == 0 {
		rest := sub[loc[1]:]
		if hasOnlyTrailingHorizWSBeforeLineEnd(rest) {
			name := sub[loc[2]:loc[3]]
			rawVal := sub[loc[4]:loc[5]]
			if !deniedKeyNames[name] {
				if val, err := strconv.ParseFloat(rawVal, 64); err == nil && !math.IsNaN(val) && !math.IsInf(val, 0) {
					s.metrics[name] = val
				}
			}
		}
	}
	if loc := asiLineRe.FindStringSubmatchIndex(sub); loc != nil && loc[0] == 0 {
		key := sub[loc[2]:loc[3]]
		rawVal := sub[loc[4]:loc[5]]
		if !deniedKeyNames[key] {
			s.asi[key] = parseASIValue(rawVal)
		}
	}
}

func hasOnlyTrailingHorizWSBeforeLineEnd(rest string) bool {
	for i := 0; i < len(rest); {
		if rest[i] == '\n' || rest[i] == '\r' {
			return true
		}
		if rest[i] == 0xe2 && i+2 < len(rest) && rest[i+1] == 0x80 && (rest[i+2] == 0xa8 || rest[i+2] == 0xa9) {
			return true
		}
		if rest[i] == ' ' || rest[i] == '\t' || rest[i] == '\f' || rest[i] == '\v' {
			i++
			continue
		}
		return false
	}
	return true
}

// ParseMetricLines parses all `METRIC name=value` lines from output,
// treating `\n`, `\r`, U+2028, and U+2029 as line boundaries (matching JS `/m`).
// upstream: packages/coding-agent/src/autoresearch/helpers.ts:86-101
func ParseMetricLines(output string) map[string]float64 {
	scanner := NewExperimentOutputScanner()
	scanner.Append(output)
	metrics, _ := scanner.Finish()
	return metrics
}

// ParseASILines parses all `ASI key=value` lines from output,
// treating `\n`, `\r`, U+2028, and U+2029 as line boundaries (matching JS `/m`).
// upstream: packages/coding-agent/src/autoresearch/helpers.ts:103-115
func ParseASILines(output string) ASIData {
	scanner := NewExperimentOutputScanner()
	scanner.Append(output)
	_, asi := scanner.Finish()
	return asi
}

// parseASIValue parses a single ASI value (bool, null, number, JSON, or string).
// upstream: packages/coding-agent/src/autoresearch/helpers.ts:117-135
func parseASIValue(raw string) any {
	value := strings.TrimSpace(raw)
	switch value {
	case "true":
		return true
	case "false":
		return false
	case "null":
		return nil
	}
	if asiNumberRe.MatchString(value) {
		if n, err := strconv.ParseFloat(value, 64); err == nil && !math.IsNaN(n) && !math.IsInf(n, 0) {
			return n
		}
	}
	if strings.HasPrefix(value, "{") || strings.HasPrefix(value, "[") || strings.HasPrefix(value, "\"") {
		var parsed any
		if err := json.Unmarshal([]byte(value), &parsed); err == nil {
			return parsed
		}
	}
	return value
}

// MergeASI merges base and override ASI maps, with override taking precedence.
// upstream: packages/coding-agent/src/autoresearch/helpers.ts:137-143
func MergeASI(base ASIData, override ASIData) ASIData {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := make(ASIData, len(base)+len(override))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		out[k] = v
	}
	return out
}

// InferMetricUnitFromName infers a display unit from a metric name suffix.
// upstream: packages/coding-agent/src/autoresearch/helpers.ts:157-164
func InferMetricUnitFromName(name string) string {
	switch {
	case strings.HasSuffix(name, "µs") || strings.HasSuffix(name, "_µs"):
		return "µs"
	case strings.HasSuffix(name, "ms") || strings.HasSuffix(name, "_ms"):
		return "ms"
	case strings.HasSuffix(name, "_s") || strings.HasSuffix(name, "_sec") || strings.HasSuffix(name, "_secs"):
		return "s"
	case strings.HasSuffix(name, "_kb") || strings.HasSuffix(name, "kb"):
		return "kb"
	case strings.HasSuffix(name, "_mb") || strings.HasSuffix(name, "mb"):
		return "mb"
	default:
		return ""
	}
}

// NormalizePathSpec normalizes a path or directory spec for prefix matching.
// upstream: packages/coding-agent/src/autoresearch/helpers.ts:166-171
func NormalizePathSpec(value string) string {
	trimmed := strings.ReplaceAll(strings.TrimSpace(value), "\\", "/")
	if trimmed == "" || trimmed == "." || trimmed == "./" {
		return "."
	}
	collapsed := strings.TrimLeft(trimmed, "")
	for strings.HasPrefix(collapsed, "./") {
		collapsed = strings.TrimPrefix(collapsed, "./")
		collapsed = strings.TrimLeft(collapsed, "/")
	}
	collapsed = strings.TrimRight(collapsed, "/")
	if collapsed == "" {
		return "."
	}
	return collapsed
}

// PathMatchesSpec reports whether pathValue equals or is nested under specValue.
// upstream: packages/coding-agent/src/autoresearch/helpers.ts:173-178
func PathMatchesSpec(pathValue, specValue string) bool {
	normalizedPath := NormalizePathSpec(pathValue)
	normalizedSpec := NormalizePathSpec(specValue)
	if normalizedSpec == "." {
		return true
	}
	return normalizedPath == normalizedSpec || strings.HasPrefix(normalizedPath, normalizedSpec+"/")
}

// DedupeStrings trims, removes empty strings, and deduplicates preserving order.
// upstream: packages/coding-agent/src/autoresearch/helpers.ts:180-190
func DedupeStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		out = append(out, trimmed)
	}
	return out
}

// EnsureNumericMetricMap copies finite numeric entries, dropping denied keys.
// upstream: packages/coding-agent/src/autoresearch/helpers.ts:192-202
func EnsureNumericMetricMap(value NumericMetricMap) NumericMetricMap {
	out := make(NumericMetricMap)
	for k, v := range value {
		if deniedKeyNames[k] {
			continue
		}
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			out[k] = v
		}
	}
	return out
}

// SanitizeASI sanitizes a free-form ASI map, stripping denied keys and unsupported types.
// upstream: packages/coding-agent/src/autoresearch/helpers.ts:204-239
func SanitizeASI(value map[string]any) ASIData {
	if len(value) == 0 {
		return nil
	}
	result := make(ASIData)
	for k, v := range value {
		if deniedKeyNames[k] {
			continue
		}
		sanitized, ok := sanitizeASIValue(v)
		if ok {
			result[k] = sanitized
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func sanitizeASIValue(value any) (any, bool) {
	if value == nil {
		return nil, true
	}
	switch v := value.(type) {
	case string, bool:
		return v, true
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, false
		}
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		if f, err := v.Float64(); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
			return f, true
		}
		return nil, false
	case []any:
		items := make([]any, 0, len(v))
		for _, item := range v {
			if s, ok := sanitizeASIValue(item); ok {
				items = append(items, s)
			}
		}
		return items, true
	case map[string]any:
		res := make(ASIData)
		for k, entry := range v {
			if deniedKeyNames[k] {
				continue
			}
			if s, ok := sanitizeASIValue(entry); ok {
				res[k] = s
			}
		}
		return res, true
	default:
		return nil, false
	}
}

// FormatElapsed formats a duration in milliseconds as "Ns" or "Mm SSs".
// upstream: packages/tui/src/apps/autoresearch-data.ts:4-12
func FormatElapsed(milliseconds int64) string {
	totalSeconds := milliseconds / 1000
	if totalSeconds < 0 {
		totalSeconds = 0
	}
	minutes := totalSeconds / 60
	seconds := totalSeconds % 60
	if minutes > 0 {
		return fmt.Sprintf("%dm %02ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}

// Commas formats an integer with comma-separated digit groups.
// upstream: packages/tui/src/tools/autoresearch.ts:122-130
func Commas(value float64) string {
	sign := ""
	if value < 0 {
		sign = "-"
	}
	digits := strconv.FormatInt(int64(math.Trunc(math.Abs(value))), 10)
	var groups []string
	for idx := len(digits); idx > 0; idx -= 3 {
		start := idx - 3
		if start < 0 {
			start = 0
		}
		groups = append([]string{digits[start:idx]}, groups...)
	}
	return sign + strings.Join(groups, ",")
}

// FmtNum formats a number with grouped digits and optional decimals.
// upstream: packages/tui/src/tools/autoresearch.ts:133-139
func FmtNum(value float64, decimals int) string {
	if decimals <= 0 {
		return Commas(math.Round(value))
	}
	abs := math.Abs(value)
	whole := math.Floor(abs)
	fracStr := strconv.FormatFloat(abs-whole, 'f', decimals, 64)
	dot := strings.IndexByte(fracStr, '.')
	frac := ""
	if dot >= 0 {
		frac = fracStr[dot:]
	}
	sign := ""
	if value < 0 {
		sign = "-"
	}
	return sign + Commas(whole) + frac
}

// FormatNum formats an optional metric value with its unit.
// upstream: packages/tui/src/tools/autoresearch.ts:142-146
func FormatNum(value *float64, unit string) string {
	if value == nil {
		return "-"
	}
	v := *value
	if math.Trunc(v) == v && !math.IsInf(v, 0) && !math.IsNaN(v) {
		return FmtNum(v, 0) + unit
	}
	return FmtNum(v, 2) + unit
}

// JSNumberString formats a float64 identically to ECMAScript Number.prototype.toString():
// fixed decimal notation for 1e-6 <= |v| < 1e21 (and 0), and shortest exponential
// notation (`e+N` / `e-N` without leading zero on exponent) outside that range.
func JSNumberString(v float64) string {
	if math.IsNaN(v) {
		return "NaN"
	}
	if math.IsInf(v, 1) {
		return "Infinity"
	}
	if math.IsInf(v, -1) {
		return "-Infinity"
	}
	if v == 0 {
		return "0"
	}
	abs := math.Abs(v)
	if abs >= 1e-6 && abs < 1e21 {
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	// Shortest exponential representation, then strip leading zero from exponent (e.g. e-07 -> e-7, e+06 -> e+6).
	s := strconv.FormatFloat(v, 'e', -1, 64)
	eIdx := strings.IndexByte(s, 'e')
	if eIdx < 0 {
		return s
	}
	mantissa := s[:eIdx]
	expSign := s[eIdx+1 : eIdx+2]
	expDigits := strings.TrimLeft(s[eIdx+2:], "0")
	if expDigits == "" {
		expDigits = "0"
	}
	return mantissa + "e" + expSign + expDigits
}

// SliceUTF16Prefix returns the longest valid UTF-8 prefix of s whose UTF-16
// code unit length is <= maxUnits (matching JS string.slice(0, maxUnits)
// without splitting a UTF-8 rune or surrogate pair).
func SliceUTF16Prefix(s string, maxUnits int) (prefix string, truncated bool) {
	if maxUnits <= 0 {
		return "", len(s) > 0
	}
	units := 0
	for i, r := range s {
		u := utf16.RuneLen(r)
		if u < 0 {
			u = 1
		}
		if units+u > maxUnits {
			return s[:i], true
		}
		units += u
	}
	return s, false
}

// FormatBytes formats a byte count in human-readable units.
func FormatBytes(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	kb := float64(n) / 1024.0
	if kb < 1024 {
		if math.Trunc(kb) == kb {
			return fmt.Sprintf("%.0f KB", kb)
		}
		return fmt.Sprintf("%.1f KB", kb)
	}
	mb := kb / 1024.0
	return fmt.Sprintf("%.1f MB", mb)
}

// TruncateTail keeps at most maxLines lines and maxBytes bytes from the end of text.
// upstream: packages/tui/src/tools/streaming-output.ts (`truncateTail`)
func TruncateTail(text string, maxLines, maxBytes int) TruncationResult {
	totalBytes := len(text)
	lines := strings.Split(text, "\n")
	totalLines := len(lines)

	if totalLines <= maxLines && totalBytes <= maxBytes {
		return TruncationResult{
			Content:     text,
			Truncated:   false,
			OutputLines: totalLines,
			OutputBytes: totalBytes,
			TotalLines:  totalLines,
			TotalBytes:  totalBytes,
		}
	}

	var kept []string
	keptBytes := 0
	for i := len(lines) - 1; i >= 0 && len(kept) < maxLines; i-- {
		line := lines[i]
		addBytes := len(line)
		if len(kept) > 0 {
			addBytes++ // newline
		}
		if keptBytes+addBytes > maxBytes {
			if len(kept) == 0 && maxBytes > 0 {
				// Truncate the single trailing line to at most maxBytes without splitting a UTF-8 rune.
				if len(line) > maxBytes {
					start := len(line) - maxBytes
					for start < len(line) && !utf8.RuneStart(line[start]) {
						start++
					}
					line = line[start:]
				}
				kept = append(kept, line)
				keptBytes = len(line)
			}
			break
		}
		kept = append(kept, line)
		keptBytes += addBytes
	}
	// Reverse kept to original order.
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	content := strings.Join(kept, "\n")
	return TruncationResult{
		Content:     content,
		Truncated:   true,
		OutputLines: len(kept),
		OutputBytes: len(content),
		TotalLines:  totalLines,
		TotalBytes:  totalBytes,
	}
}

// Slugify converts free-form text into a lowercase ASCII hyphen-separated slug.
// upstream: packages/utils/src/slug.ts (`slugify`)
func Slugify(text string, maxLength int) string {
	lower := strings.ToLower(strings.TrimSpace(text))
	var b strings.Builder
	prevDash := false
	for _, r := range lower {
		isAlnum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAlnum {
			b.WriteRune(r)
			prevDash = false
		} else if !prevDash && b.Len() > 0 {
			b.WriteByte('-')
			prevDash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if maxLength > 0 && len(s) > maxLength {
		s = strings.TrimRight(s[:maxLength], "-")
	}
	return s
}

func cloneStringSlice(in []string) []string {
	if in == nil {
		return []string{}
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func cloneASI(in ASIData) ASIData {
	if in == nil {
		return nil
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return nil
	}
	var out ASIData
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}
