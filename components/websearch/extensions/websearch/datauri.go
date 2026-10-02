package websearch

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// Port of data-uri-sanitize.ts: inline RFC 2397 data: URIs in extracted text become explicit,
// bounded omission markers so base64 payloads never reach model-visible tool results, the fetch
// cache or the session. Scanning works on UTF-16 code units, like the original's String indexes.

const (
	maxDataURIHeaderChars = 1024
	maxMIMEChars          = 127
	maxMarkerSourceChars  = 180
)

// DataURIOmission describes one replaced data URI.
type DataURIOmission struct {
	Ordinal      int
	SourcePath   string
	MimeType     string
	Encoding     string // "base64" or "percent-encoded"
	EncodedBytes int
	DecodedBytes *int
	DecodeError  string
	SHA256       string
	DigestBasis  string // "decoded" or "encoded"
	Retrieval    string
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func lowerUnit(c uint16) uint16 {
	if c >= 65 && c <= 90 {
		return c + 32
	}
	return c
}

func matchesDataScheme(t []uint16, i int) bool {
	return i+5 <= len(t) && lowerUnit(t[i]) == 'd' && lowerUnit(t[i+1]) == 'a' && lowerUnit(t[i+2]) == 't' && lowerUnit(t[i+3]) == 'a' && t[i+4] == ':'
}

func isSchemeBoundary(t []uint16, i int) bool {
	if i == 0 {
		return true
	}
	c := t[i-1]
	alnum := c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
	return !alnum && c != '+' && c != '-' && c != '.'
}

func findNextDataScheme(t []uint16, from int) int {
	for i := from; i+5 <= len(t); i++ {
		if matchesDataScheme(t, i) && isSchemeBoundary(t, i) {
			return i
		}
	}
	return -1
}

type enclosure struct {
	kind  string // "", "quote", "parentheses", "angle"
	close uint16
}

func dataURIEnclosure(t []uint16, start int) enclosure {
	if start == 0 {
		return enclosure{}
	}
	imm := start - 1
	c := t[imm]
	if c == '"' || c == '\'' || c == '`' {
		likely := imm == 0
		if !likely {
			b := t[imm-1]
			likely = b == '=' || b == '(' || b == '[' || b == '{' || b == ':' || b == ',' || isJSSpace(rune(b))
		}
		if likely {
			return enclosure{"quote", c}
		}
	}
	op := imm
	for op >= 0 && (t[op] == ' ' || t[op] == '\t') {
		op--
	}
	if op >= 0 {
		switch t[op] {
		case '(':
			return enclosure{kind: "parentheses"}
		case '<':
			return enclosure{kind: "angle"}
		}
	}
	return enclosure{}
}

func isBareTerminator(c uint16) bool {
	return c <= 32 || c == 127 || c == '"' || c == '`' || c == '<' || c == '>'
}

// scanDataURICandidate scans one candidate exactly once; enclosures swallow malformed whitespace
// and balanced inner parentheses so an invalid payload suffix cannot escape.
func scanDataURICandidate(t []uint16, start int) (end, comma int, enc enclosure) {
	enc = dataURIEnclosure(t, start)
	comma = -1
	depth := 0
	if enc.kind == "parentheses" {
		depth = 1
	}
	i := start + 5
	for ; i < len(t); i++ {
		c := t[i]
		stop := false
		switch enc.kind {
		case "quote":
			stop = c == enc.close
		case "parentheses":
			if c == '(' {
				depth++
			} else if c == ')' {
				depth--
				stop = depth == 0
			}
		case "angle":
			stop = c == '>'
		default:
			stop = isBareTerminator(c)
		}
		if stop {
			break
		}
		if comma < 0 && c == ',' {
			comma = i
		}
	}
	return i, comma, enc
}

func headerEndsWithBase64(t []uint16, headerStart, comma int) bool {
	end := comma
	for end > headerStart && (t[end-1] == ' ' || t[end-1] == '\t') {
		end--
	}
	const token = "base64"
	if end-headerStart < len(token)+1 {
		return false
	}
	ts := end - len(token)
	for i := 0; i < len(token); i++ {
		if lowerUnit(t[ts+i]) != uint16(token[i]) {
			return false
		}
	}
	return ts > headerStart && t[ts-1] == ';'
}

func isMIMETokenChar(c uint16) bool {
	if c < 33 || c > 126 {
		return false
	}
	switch c {
	case '"', '(', ')', ',', '/', ':', ';', '<', '=', '>', '?', '@', '[', '\\', ']':
		return false
	}
	return true
}

func u16string(t []uint16, start, end int) string { return u16text{u: t}.slice(start, end) }

func normalizeMIMEType(t []uint16, headerStart, comma int) string {
	end := headerStart
	for end < comma && t[end] != ';' {
		end++
	}
	if end == headerStart {
		return "text/plain"
	}
	if end-headerStart > maxMIMEChars {
		return "application/octet-stream"
	}
	slashes := 0
	for i := headerStart; i < end; i++ {
		if t[i] == '/' {
			slashes++
			if i == headerStart || i == end-1 {
				return "application/octet-stream"
			}
			continue
		}
		if !isMIMETokenChar(t[i]) {
			return "application/octet-stream"
		}
	}
	if slashes != 1 {
		return "application/octet-stream"
	}
	return strings.ToLower(u16string(t, headerStart, end))
}

func hexValue(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	}
	return -1
}

func decodePercentEncoded(payload string) ([]byte, string) {
	out := make([]byte, 0, len(payload))
	for cursor := 0; cursor < len(payload); {
		p := strings.IndexByte(payload[cursor:], '%')
		if p < 0 {
			return append(out, payload[cursor:]...), ""
		}
		p += cursor
		out = append(out, payload[cursor:p]...)
		if p+2 >= len(payload) {
			return nil, "invalid-percent-escape"
		}
		hi, lo := hexValue(payload[p+1]), hexValue(payload[p+2])
		if hi < 0 || lo < 0 {
			return nil, "invalid-percent-escape"
		}
		out = append(out, byte(hi<<4|lo))
		cursor = p + 3
	}
	return out, ""
}

func decodeBase64Payload(payload string) ([]byte, string) {
	enc, errCode := decodePercentEncoded(payload)
	if errCode != "" {
		return nil, errCode
	}
	paddingStart, paddingCount := len(enc), 0
	for i, b := range enc {
		if b > 127 {
			return nil, "non-ascii-base64"
		}
		if b == '=' {
			if paddingStart == len(enc) {
				paddingStart = i
			}
			paddingCount++
			continue
		}
		if paddingStart != len(enc) {
			return nil, "invalid-base64-padding"
		}
		if !(b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '+' || b == '/') {
			return nil, "invalid-base64-character"
		}
	}
	switch {
	case paddingCount > 2, paddingCount > 0 && len(enc)%4 != 0:
		return nil, "invalid-base64-padding"
	case paddingCount == 0 && len(enc)%4 == 1:
		return nil, "invalid-base64-length"
	case paddingCount == 1 && paddingStart%4 != 3, paddingCount == 2 && paddingStart%4 != 2:
		return nil, "invalid-base64-padding"
	}
	out, err := base64.RawStdEncoding.DecodeString(string(enc[:paddingStart]))
	if err != nil {
		return nil, "invalid-base64-character"
	}
	return out, ""
}

func safeMarkerSource(source string) string {
	if source == "" {
		source = "value"
	}
	var b strings.Builder
	for _, r := range source {
		ok := r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || strings.ContainsRune("._-[]*$", r)
		if ok {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	safe := b.String()
	if len(safe) <= maxMarkerSourceChars {
		return safe
	}
	suffix := sha256Hex([]byte(source))[:12]
	return safe[:maxMarkerSourceChars-len(suffix)-1] + "~" + suffix
}

func buildDataURIMarker(o DataURIOmission) string {
	decoded := "unknown"
	if o.DecodedBytes != nil {
		decoded = strconv.Itoa(*o.DecodedBytes)
	}
	decodeErr := ""
	if o.DecodeError != "" {
		decodeErr = "; decodeError=" + o.DecodeError
	}
	return fmt.Sprintf("[pi-web-access inline data URI omitted; ordinal=%d; source=%s; mime=%s; encoding=%s; encodedBytes=%d; decodedBytes=%s%s; sha256=%s; digestBasis=%s; retrieval=not-retained]",
		o.Ordinal, o.SourcePath, o.MimeType, o.Encoding, o.EncodedBytes, decoded, decodeErr, o.SHA256, o.DigestBasis)
}

// SanitizeInlineDataURIs replaces every recognized inline data URI in text.
func SanitizeInlineDataURIs(text, sourcePath string) (string, []DataURIOmission) {
	t := newU16(text).u
	var omissions []DataURIOmission
	var pieces []string
	scanFrom, retainedFrom := 0, 0
	for scanFrom < len(t) {
		start := findNextDataScheme(t, scanFrom)
		if start < 0 {
			break
		}
		end, comma, enc := scanDataURICandidate(t, start)
		headerStart := start + 5
		missingComma := comma < 0
		// A bare prose label such as "data: value" is not an inline URI; enclosed or non-empty
		// comma-less candidates are malformed URIs and are removed.
		if missingComma && end == headerStart && enc.kind == "" {
			scanFrom = end
			continue
		}
		headerEnd := comma
		if missingComma {
			headerEnd = end
		}
		encoding := "percent-encoded"
		if headerEndsWithBase64(t, headerStart, headerEnd) {
			encoding = "base64"
		}
		mime := normalizeMIMEType(t, headerStart, headerEnd)
		var payload string
		if missingComma {
			payload = u16string(t, headerStart, end)
		} else {
			payload = u16string(t, comma+1, end)
		}
		o := DataURIOmission{Ordinal: len(omissions) + 1, SourcePath: safeMarkerSource(sourcePath), MimeType: mime, Encoding: encoding,
			EncodedBytes: len(payload), DigestBasis: "encoded", Retrieval: "not-retained"}
		switch {
		case missingComma:
			o.DecodeError, o.SHA256 = "missing-comma", sha256Hex([]byte(payload))
		case headerEnd-headerStart > maxDataURIHeaderChars:
			o.DecodeError, o.SHA256 = "header-too-long", sha256Hex([]byte(payload))
		default:
			var decoded []byte
			var code string
			if encoding == "base64" {
				decoded, code = decodeBase64Payload(payload)
			} else {
				decoded, code = decodePercentEncoded(payload)
			}
			if code != "" {
				o.DecodeError, o.SHA256 = code, sha256Hex([]byte(payload))
			} else {
				n := len(decoded)
				o.DecodedBytes, o.DigestBasis, o.SHA256 = &n, "decoded", sha256Hex(decoded)
			}
		}
		pieces = append(pieces, u16string(t, retainedFrom, start), buildDataURIMarker(o))
		omissions = append(omissions, o)
		retainedFrom, scanFrom = end, end
	}
	if len(omissions) == 0 && retainedFrom == 0 {
		return text, nil
	}
	pieces = append(pieces, u16string(t, retainedFrom, len(t)))
	return strings.Join(pieces, ""), omissions
}
