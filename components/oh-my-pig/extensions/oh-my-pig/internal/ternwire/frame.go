package ternwire

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

// MaxFrameBytes is the daemon's frame size cap (256 MiB).
// upstream: packages/coding-agent/src/tools/browser/tern/wire.ts:24
const MaxFrameBytes = 256 << 20

// EncodeFrame encodes message as a frame: u32 LE length followed by UTF-8 JSON.
// upstream: packages/coding-agent/src/tools/browser/tern/wire.ts:75
func EncodeFrame(message any) ([]byte, error) {
	payload, err := json.Marshal(message)
	if err != nil {
		return nil, err
	}
	if len(payload) > MaxFrameBytes {
		return nil, &Error{
			Kind:    ErrInvalid,
			Message: fmt.Sprintf("frame size %d exceeds limit %d", len(payload), MaxFrameBytes),
		}
	}
	frame := make([]byte, 4+len(payload))
	binary.LittleEndian.PutUint32(frame[:4], uint32(len(payload)))
	copy(frame[4:], payload)
	return frame, nil
}

// ReplyType identifies a decoded reply from the daemon.
type ReplyType string

const (
	ReplyWelcome ReplyType = "welcome"
	ReplyBrowser ReplyType = "browser"
	ReplyFork    ReplyType = "fork"
	ReplyOther   ReplyType = "other"
)

// Reply is a decoded frame payload from the daemon.
// upstream: packages/coding-agent/src/tools/browser/tern/wire.ts:87
type Reply struct {
	Type   ReplyType
	Ops    []string
	ID     int64
	Answer json.RawMessage
}

// DecodeReply decodes one JSON reply payload.
// upstream: packages/coding-agent/src/tools/browser/tern/wire.ts:96
func DecodeReply(payload []byte, product string) (Reply, error) {
	if product == "" {
		product = "Tern"
	}
	if !utf8.Valid(payload) {
		return Reply{}, &Error{
			Kind:    ErrProtocol,
			Message: fmt.Sprintf("%s sent a frame that is not JSON: invalid UTF-8", product),
		}
	}
	var generic any
	if err := json.Unmarshal(payload, &generic); err != nil {
		return Reply{}, &Error{
			Kind:    ErrProtocol,
			Message: fmt.Sprintf("%s sent a frame that is not JSON: %s", product, err.Error()),
		}
	}
	if _, isObj := generic.(map[string]any); !isObj {
		return Reply{}, &Error{
			Kind:    ErrProtocol,
			Message: fmt.Sprintf("%s sent a frame that is not a JSON object", product),
		}
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(payload, &raw); err != nil || raw == nil {
		return Reply{}, &Error{
			Kind:    ErrProtocol,
			Message: fmt.Sprintf("%s sent a frame that is not a JSON object", product),
		}
	}
	if welcomeRaw, ok := raw["welcome"]; ok {
		var w struct {
			Ops []any `json:"ops"`
		}
		_ = json.Unmarshal(welcomeRaw, &w)
		var ops []string
		for _, item := range w.Ops {
			if s, ok := item.(string); ok {
				ops = append(ops, s)
			}
		}
		return Reply{Type: ReplyWelcome, Ops: ops}, nil
	}

	var channel ReplyType
	var answerRaw json.RawMessage
	if b, ok := raw["browser"]; ok {
		channel = ReplyBrowser
		answerRaw = b
	} else if f, ok := raw["fork"]; ok {
		channel = ReplyFork
		answerRaw = f
	} else {
		return Reply{Type: ReplyOther}, nil
	}

	idRaw, hasID := raw["id"]
	if !hasID {
		return Reply{}, &Error{
			Kind:    ErrProtocol,
			Message: fmt.Sprintf("%s sent a %s answer without an id", product, channel),
		}
	}
	var idVal any
	if err := json.Unmarshal(idRaw, &idVal); err != nil {
		return Reply{}, &Error{
			Kind:    ErrProtocol,
			Message: fmt.Sprintf("%s sent a %s answer without an id", product, channel),
		}
	}
	idNum, isNum := idVal.(float64)
	if !isNum {
		return Reply{}, &Error{
			Kind:    ErrProtocol,
			Message: fmt.Sprintf("%s sent a %s answer without an id", product, channel),
		}
	}
	return Reply{
		Type:   channel,
		ID:     int64(idNum),
		Answer: answerRaw,
	}, nil
}

// FrameReader incrementally splits a byte stream into length-prefixed frame payloads.
// upstream: packages/coding-agent/src/tools/browser/tern/wire.ts:129
type FrameReader struct {
	chunks   [][]byte
	head     int
	buffered int
	product  string
}

// NewFrameReader creates a FrameReader. product names the sender in protocol errors.
func NewFrameReader(product string) *FrameReader {
	if product == "" {
		product = "Tern"
	}
	return &FrameReader{product: product}
}

// Push appends chunk and returns every complete payload it finished.
func (r *FrameReader) Push(chunk []byte) ([][]byte, error) {
	if len(chunk) > 0 {
		cp := make([]byte, len(chunk))
		copy(cp, chunk)
		r.chunks = append(r.chunks, cp)
		r.buffered += len(cp)
	}
	var payloads [][]byte
	for r.buffered >= 4 {
		header := r.peek(4)
		length := binary.LittleEndian.Uint32(header)
		if int(length) > MaxFrameBytes {
			return nil, &Error{
				Kind:    ErrProtocol,
				Message: fmt.Sprintf("%s sent a %d-byte frame (limit %d)", r.product, length, MaxFrameBytes),
			}
		}
		if r.buffered < 4+int(length) {
			break
		}
		r.take(4)
		payloads = append(payloads, r.take(int(length)))
	}
	return payloads, nil
}

func (r *FrameReader) peek(count int) []byte {
	first := r.chunks[0]
	if len(first)-r.head >= count {
		return first[r.head : r.head+count]
	}
	out := make([]byte, count)
	filled := 0
	head := r.head
	for _, chunk := range r.chunks {
		n := len(chunk) - head
		if n > count-filled {
			n = count - filled
		}
		copy(out[filled:filled+n], chunk[head:head+n])
		filled += n
		head = 0
		if filled == count {
			break
		}
	}
	return out
}

func (r *FrameReader) take(count int) []byte {
	if count == 0 {
		return []byte{}
	}
	var out []byte
	first := r.chunks[0]
	if len(first)-r.head >= count {
		out = first[r.head : r.head+count]
		r.head += count
	} else {
		out = make([]byte, count)
		filled := 0
		for filled < count {
			chunk := r.chunks[0]
			n := len(chunk) - r.head
			if n > count-filled {
				n = count - filled
			}
			copy(out[filled:filled+n], chunk[r.head:r.head+n])
			filled += n
			r.head += n
			if r.head == len(chunk) {
				r.chunks = r.chunks[1:]
				r.head = 0
			}
		}
	}
	if len(r.chunks) > 0 && r.head == len(r.chunks[0]) {
		r.chunks = r.chunks[1:]
		r.head = 0
	}
	r.buffered -= count
	return out
}
