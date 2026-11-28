package lsp

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

// MessageFramingError signals a fatal framing violation on the LSP byte stream.
type MessageFramingError struct {
	Message string
}

func (e *MessageFramingError) Error() string { return e.Message }

// encodeMessageFrame encodes one JSON-RPC message with its Content-Length header.
func encodeMessageFrame(msg any) ([]byte, error) {
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

// messageFramer incrementally decodes Content-Length framed JSON messages,
// resyncing past non-protocol stdout headers without stalling.
type messageFramer struct {
	buf           []byte
	contentLength int
	hasLength     bool
	messageStart  int
	err           error
}

func (f *messageFramer) push(chunk []byte) error {
	if f.err != nil {
		return f.err
	}
	if len(chunk) == 0 {
		return nil
	}
	f.buf = append(f.buf, chunk...)
	return nil
}

// drain extracts all complete JSON message bodies currently buffered.
// onResync is invoked when a header block has no Content-Length (wrapper noise).
func (f *messageFramer) drain(onResync func(header string)) ([][]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	var messages [][]byte
	for {
		if !f.hasLength {
			headerEnd := bytes.Index(f.buf, headerTerminator)
			if headerEnd == -1 {
				if len(f.buf) >= maxHeaderBytes {
					f.err = &MessageFramingError{Message: fmt.Sprintf("JSON-RPC header exceeds %d-byte limit", maxHeaderBytes)}
					f.buf = nil
					return messages, f.err
				}
				break
			}
			if headerEnd >= maxHeaderBytes {
				f.err = &MessageFramingError{Message: fmt.Sprintf("JSON-RPC header exceeds %d-byte limit", maxHeaderBytes)}
				f.buf = nil
				return messages, f.err
			}
			headerText := string(f.buf[:headerEnd])
			length, found, parseErr := parseContentLength(headerText)
			if parseErr != nil {
				f.err = parseErr
				f.buf = nil
				return messages, f.err
			}
			if !found {
				f.consume(headerEnd + 4)
				if onResync != nil {
					onResync(headerText)
				}
				continue
			}
			if length > maxContentBytes {
				f.err = &MessageFramingError{Message: fmt.Sprintf("JSON-RPC Content-Length exceeds %d-byte limit", maxContentBytes)}
				f.buf = nil
				return messages, f.err
			}
			f.contentLength = length
			f.hasLength = true
			f.messageStart = headerEnd + 4
		}

		messageEnd := f.messageStart + f.contentLength
		if len(f.buf) < messageEnd {
			break
		}
		body := make([]byte, f.contentLength)
		copy(body, f.buf[f.messageStart:messageEnd])
		f.consume(messageEnd)
		messages = append(messages, body)
	}
	return messages, nil
}

func (f *messageFramer) consume(n int) {
	if n >= len(f.buf) {
		f.buf = f.buf[:0]
	} else {
		remaining := len(f.buf) - n
		copy(f.buf, f.buf[n:])
		f.buf = f.buf[:remaining]
	}
	f.hasLength = false
	f.contentLength = 0
	f.messageStart = 0
}

func parseContentLength(headerText string) (length int, found bool, err error) {
	count := 0
	var rawLen string
	for _, rawLine := range strings.Split(headerText, "\n") {
		line := strings.TrimSuffix(rawLine, "\r")
		colon := strings.IndexByte(line, ':')
		if colon == -1 {
			continue
		}
		name := strings.TrimSpace(line[:colon])
		if strings.EqualFold(name, "Content-Length") {
			count++
			rawLen = strings.TrimSpace(line[colon+1:])
		}
	}
	if count == 0 {
		return 0, false, nil
	}
	if count != 1 || rawLen == "" {
		return 0, true, &MessageFramingError{Message: "Invalid or duplicate JSON-RPC Content-Length"}
	}
	for i := range len(rawLen) {
		if rawLen[i] < '0' || rawLen[i] > '9' {
			return 0, true, &MessageFramingError{Message: "Invalid or duplicate JSON-RPC Content-Length"}
		}
	}
	n, convErr := strconv.Atoi(rawLen)
	if convErr != nil || n < 0 {
		return 0, true, errors.New("Invalid or duplicate JSON-RPC Content-Length")
	}
	return n, true, nil
}
