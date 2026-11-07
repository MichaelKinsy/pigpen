// Package reqlog is the request-log format shared by mockls (writer) and lsdiff (reader): one JSON object per
// line, one line per request the mock endpoint received.
package reqlog

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// Part is one part of a multipart body, after Content-Encoding was undone.
type Part struct {
	Name        string `json:"name"`
	ContentType string `json:"contentType"`
	// Filename is the Content-Disposition filename, empty when absent.
	Filename string `json:"filename,omitempty"`
	// Header is the part's raw header block (everything before the blank line), CRLF-joined.
	Header string `json:"header"`
	// Bytes is the part body, base64 in the JSONL (encoding/json does it for []byte).
	Bytes []byte `json:"bytes"`
	// Text is Bytes when it is valid UTF-8 (readability only, never compared).
	Text string `json:"text,omitempty"`
}

// Entry is one request.
type Entry struct {
	Seq   int    `json:"seq"`
	Time  string `json:"time"` // RFC 3339 nano, arrival of the end of the body
	Route string `json:"route"`
	// Call is the 1-based index of this request among those of the same route.
	Call   int    `json:"call"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Query  string `json:"query,omitempty"` // raw query string
	// Headers are all request headers, names lower-cased, repeated values joined with ", ", Host included.
	Headers map[string]string `json:"headers"`
	// Transfer is "chunked", "content-length" or "none" (no body framing: a bodiless request).
	Transfer      string `json:"transfer"`
	ContentLength int64  `json:"contentLength"` // -1 when chunked or none
	Encoding      string `json:"encoding,omitempty"`
	// Body is the raw body as it crossed the wire (still gzip-compressed when Encoding is gzip).
	Body []byte `json:"body,omitempty"`
	// Decoded is Body after Content-Encoding was undone; only set when Encoding is not empty.
	Decoded []byte `json:"decoded,omitempty"`
	// Boundary and Parts are set for multipart/form-data bodies.
	Boundary string `json:"boundary,omitempty"`
	Parts    []Part `json:"parts,omitempty"`
	// Form is the decoded application/x-www-form-urlencoded body (token endpoint).
	Form map[string][]string `json:"form,omitempty"`
	// ParseError is set when the body claimed a format it did not have.
	ParseError string `json:"parseError,omitempty"`
	// Reply is what mockls answered.
	Reply Reply `json:"reply"`
}

// Reply records the answer: the status (0 when the connection was dropped), the fault rule that produced it
// (index into the config's faults, -1 for none) and the delay applied.
type Reply struct {
	Status  int  `json:"status"`
	Fault   int  `json:"fault"`
	DelayMs int  `json:"delayMs,omitempty"`
	Dropped bool `json:"dropped,omitempty"`
}

// Payload is the body to analyse: Decoded when the request was encoded, else Body.
func (e *Entry) Payload() []byte {
	if e.Encoding != "" {
		return e.Decoded
	}
	return e.Body
}

// Read parses a JSONL request log.
func Read(r io.Reader) ([]*Entry, error) {
	br := bufio.NewReaderSize(r, 1<<20)
	var out []*Entry
	for line := 1; ; line++ {
		b, err := br.ReadBytes('\n')
		if len(bytes.TrimSpace(b)) > 0 {
			e := new(Entry)
			if jerr := json.Unmarshal(b, e); jerr != nil {
				return nil, fmt.Errorf("line %d: %w", line, jerr)
			}
			out = append(out, e)
		}
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// ReadFile parses the JSONL request log at path.
func ReadFile(path string) ([]*Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	es, err := Read(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return es, nil
}
