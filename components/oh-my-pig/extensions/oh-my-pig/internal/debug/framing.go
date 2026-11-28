package debug

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// upstream: packages/coding-agent/src/jsonrpc/message-framing.ts

const (
	maxHeaderBytes  = 16 * 1024
	maxContentBytes = 256 * 1024 * 1024
)

var headerTerminator = []byte("\r\n\r\n")

// MessageFramingError signals a protocol-level framing violation.
type MessageFramingError struct {
	Msg string
}

func (e *MessageFramingError) Error() string { return e.Msg }

// EncodeMessageFrame encodes a DAP/JSON-RPC message with Content-Length framing.
func EncodeMessageFrame(msg any) ([]byte, error) {
	body, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	frame := make([]byte, len(header)+len(body))
	copy(frame, header)
	copy(frame[len(header):], body)
	return frame, nil
}

// MessageFramer incrementally decodes Content-Length framed JSON messages.
type MessageFramer struct {
	buf           []byte
	contentLength int
	hasLength     bool
	messageStart  int
	failure       *MessageFramingError
}

// NewMessageFramer constructs a framer optionally seeded with initial bytes.
func NewMessageFramer(seed []byte) *MessageFramer {
	f := &MessageFramer{}
	if len(seed) > 0 {
		f.buf = append([]byte(nil), seed...)
	}
	return f
}

// Push appends incoming bytes to the framer buffer.
func (f *MessageFramer) Push(chunk []byte) error {
	if f.failure != nil {
		return f.failure
	}
	if len(chunk) == 0 {
		return nil
	}
	f.buf = append(f.buf, chunk...)
	return nil
}

// Drain returns every complete message currently buffered.
// onResync is invoked when a header block lacks Content-Length (non-protocol stdout noise).
func (f *MessageFramer) Drain(onResync func(headerText string)) ([][]byte, error) {
	if f.failure != nil {
		return nil, f.failure
	}
	var messages [][]byte
	for {
		if !f.hasLength {
			headerEnd := bytes.Index(f.buf, headerTerminator)
			if headerEnd == -1 {
				if len(f.buf) > maxHeaderBytes {
					f.failure = &MessageFramingError{
						Msg: fmt.Sprintf("JSON-RPC header exceeds %d-byte limit", maxHeaderBytes),
					}
					return messages, f.failure
				}
				break
			}
			if headerEnd > maxHeaderBytes {
				f.failure = &MessageFramingError{
					Msg: fmt.Sprintf("JSON-RPC header exceeds %d-byte limit", maxHeaderBytes),
				}
				return messages, f.failure
			}
			headerText := string(f.buf[:headerEnd])
			length, found, err := parseContentLength(headerText)
			if err != nil {
				f.failure = err
				return messages, f.failure
			}
			if !found {
				f.dropFront(headerEnd + 4)
				if onResync != nil {
					onResync(headerText)
				}
				continue
			}
			f.contentLength = length
			f.hasLength = true
			f.messageStart = headerEnd + 4
		}

		messageEnd := f.messageStart + f.contentLength
		if len(f.buf) < messageEnd {
			break
		}
		payload := make([]byte, f.contentLength)
		copy(payload, f.buf[f.messageStart:messageEnd])
		f.dropFront(messageEnd)
		messages = append(messages, payload)
	}
	return messages, nil
}

func (f *MessageFramer) dropFront(n int) {
	if n >= len(f.buf) {
		f.buf = f.buf[:0]
	} else {
		// Compact when leading dead space is significant to avoid unbounded backing-array growth.
		if n > 64*1024 && n*2 >= cap(f.buf) {
			rem := make([]byte, len(f.buf)-n)
			copy(rem, f.buf[n:])
			f.buf = rem
		} else {
			f.buf = f.buf[n:]
		}
	}
	f.hasLength = false
	f.contentLength = 0
	f.messageStart = 0
}

func parseContentLength(headerText string) (length int, found bool, fail *MessageFramingError) {
	lines := strings.Split(headerText, "\n")
	count := 0
	var rawVal string
	for _, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		key := line[:colon]
		if !strings.EqualFold(key, "Content-Length") {
			continue
		}
		count++
		rawVal = strings.TrimSpace(line[colon+1:])
	}
	if count == 0 {
		return 0, false, nil
	}
	if count != 1 || rawVal == "" {
		return 0, false, &MessageFramingError{Msg: "Invalid or duplicate JSON-RPC Content-Length"}
	}
	for i := range len(rawVal) {
		if rawVal[i] < '0' || rawVal[i] > '9' {
			return 0, false, &MessageFramingError{Msg: "Invalid or duplicate JSON-RPC Content-Length"}
		}
	}
	n, err := strconv.ParseInt(rawVal, 10, 64)
	if err != nil || n < 0 || n > maxContentBytes {
		return 0, false, &MessageFramingError{
			Msg: fmt.Sprintf("JSON-RPC Content-Length exceeds %d-byte limit", maxContentBytes),
		}
	}
	return int(n), true, nil
}

func isFramingError(err error) bool {
	var fe *MessageFramingError
	return errors.As(err, &fe)
}
