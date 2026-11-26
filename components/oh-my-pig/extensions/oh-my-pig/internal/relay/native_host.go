package relay

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"
)

// upstream: packages/coding-agent/src/tools/browser/relay/native-host.ts:1-255

const (
	// NativeFrameLimit is the largest encoded frame body the host writes; under
	// Chrome's 1 MiB host->extension cap with headroom.
	// upstream: native-host.ts:26
	NativeFrameLimit = 900 * 1024

	// chunkRunes is the initial slice size before shrinking to fit NativeFrameLimit.
	// upstream: native-host.ts:28
	chunkRunes = 256 * 1024

	// maxInboundNativeFrameBytes caps inbound native messaging frames so a
	// corrupt length header cannot trigger an unbounded allocation.
	maxInboundNativeFrameBytes = 256 << 20
)

// HostIO is what the native host is given by the command line: Chrome's pipes and the environment.
type HostIO struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Getenv func(string) string
	// Args are the arguments after `browser-relay native-host` (Chrome passes the calling extension's origin).
	Args []string
}

// NativeHostServeOptions configures ServeNativeHost (used by RunNativeHost and tests).
// upstream: native-host.ts:49-60
type NativeHostServeOptions struct {
	Input     io.Reader
	Write     func(frame []byte) error
	RelayPort int
	Log       func(message string)
}

// RunNativeHost serves one Chrome native messaging connection until Chrome closes it and returns the exit code.
// Callers must provide hostIO.Stdin and hostIO.Stdout (clihook.Env supplies them without touching process globals).
// upstream: packages/coding-agent/src/tools/browser/relay/native-host.ts:63-82
func RunNativeHost(ctx context.Context, hostIO HostIO) int {
	stdin := hostIO.Stdin
	stdout := hostIO.Stdout
	stderr := hostIO.Stderr
	if stderr == nil {
		stderr = io.Discard
	}
	if stdin == nil || stdout == nil {
		_, _ = fmt.Fprintf(stderr, "%s native host: stdin and stdout are required\n", ExtensionName)
		return 1
	}
	var wmu sync.Mutex
	err := ServeNativeHost(ctx, NativeHostServeOptions{
		Input: stdin,
		Write: func(frame []byte) error {
			wmu.Lock()
			defer wmu.Unlock()
			_, err := stdout.Write(frame)
			return err
		},
		RelayPort: 0,
		Log: func(msg string) {
			_, _ = fmt.Fprintf(stderr, "%s native host: %s\n", ExtensionName, msg)
		},
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s native host: %v\n", ExtensionName, err)
		return 1
	}
	return 0
}

// EncodeNativeFrame encodes one native messaging frame: uint32 LE byte length + UTF-8 JSON.
// upstream: native-host.ts:112-118
func EncodeNativeFrame(msg any) ([]byte, error) {
	body, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	frame := make([]byte, 4+len(body))
	binary.LittleEndian.PutUint32(frame[:4], uint32(len(body)))
	copy(frame[4:], body)
	return frame, nil
}

// NativeFrameReader incrementally decodes uint32-LE length-prefixed JSON frames.
// upstream: native-host.ts:121-141
type NativeFrameReader struct {
	buf []byte
}

// Push appends bytes and returns every complete decoded JSON message.
func (r *NativeFrameReader) Push(chunk []byte) ([]json.RawMessage, error) {
	r.buf = append(r.buf, chunk...)
	var out []json.RawMessage
	offset := 0
	for len(r.buf)-offset >= 4 {
		length := int(binary.LittleEndian.Uint32(r.buf[offset : offset+4]))
		if length > maxInboundNativeFrameBytes {
			return out, fmt.Errorf("native messaging frame length %d exceeds limit", length)
		}
		if len(r.buf)-offset-4 < length {
			break
		}
		body := make([]byte, length)
		copy(body, r.buf[offset+4:offset+4+length])
		out = append(out, json.RawMessage(body))
		offset += 4 + length
	}
	if offset > 0 {
		remaining := len(r.buf) - offset
		copy(r.buf[:remaining], r.buf[offset:])
		r.buf = r.buf[:remaining]
	}
	return out, nil
}

// LineFrames splits one rpc or relay line into FromNativeHost frames that each
// encode under NativeFrameLimit bytes (Chrome's 1 MiB host->extension cap).
// Never splits a UTF-8 rune across chunks.
// upstream: native-host.ts:147-175
func LineFrames(msgType string, line string, chunkID int) []FromNativeHost {
	whole := FromNativeHost{T: msgType, Line: line}
	if encoded, err := json.Marshal(whole); err == nil && len(encoded) <= NativeFrameLimit {
		return []FromNativeHost{whole}
	}
	var frames []FromNativeHost
	isRelay := msgType == "relay"
	start := 0
	for start < len(line) {
		end := min(len(line), start+chunkRunes)
		// Advance/retreat to a valid UTF-8 rune boundary.
		for end < len(line) && !utf8.RuneStart(line[end]) {
			end--
		}
		if end <= start {
			_, size := utf8.DecodeRuneInString(line[start:])
			end = start + size
		}
		for {
			idx := len(frames)
			last := end == len(line)
			frame := FromNativeHost{
				T:     "chunk",
				Relay: isRelay,
				ID:    chunkID,
				Index: &idx,
				Last:  &last,
				Data:  line[start:end],
			}
			encoded, err := json.Marshal(frame)
			if err == nil && len(encoded) <= NativeFrameLimit {
				frames = append(frames, frame)
				break
			}
			half := start + max(1, (end-start)/2)
			for half > start && !utf8.RuneStart(line[half]) {
				half--
			}
			if half <= start {
				_, size := utf8.DecodeRuneInString(line[start:])
				end = start + size
				// Emit single rune even if somehow huge.
				idx := len(frames)
				last := end == len(line)
				frames = append(frames, FromNativeHost{
					T:     "chunk",
					Relay: isRelay,
					ID:    chunkID,
					Index: &idx,
					Last:  &last,
					Data:  line[start:end],
				})
				break
			}
			end = half
		}
		start = end
	}
	return frames
}

// ResolveSessionCwd expands ~ and ~/...; an empty path means the home directory.
// upstream: native-host.ts:178-183
func ResolveSessionCwd(cwd string) string {
	home, _ := os.UserHomeDir()
	if cwd == "" || cwd == "~" {
		return home
	}
	if strings.HasPrefix(cwd, "~/") || strings.HasPrefix(cwd, `~\`) {
		return filepath.Join(home, cwd[2:])
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return cwd
	}
	return abs
}

func parseToNativeHost(raw json.RawMessage) (*ToNativeHost, bool) {
	var msg ToNativeHost
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&msg); err != nil {
		return nil, false
	}
	switch msg.T {
	case "open":
		if msg.Cwd == nil {
			return nil, false
		}
		return &msg, true
	case "link":
		if msg.ExtensionID == nil {
			return nil, false
		}
		return &msg, true
	case "rpc", "relay":
		if msg.Line == nil {
			return nil, false
		}
		return &msg, true
	default:
		return nil, false
	}
}

// ServeNativeHost runs one native messaging host session over opts until Chrome
// disconnects, ctx cancels, or a leader relay accepts a restart.
// The `link` branch serves the browser relay (leader/follower). The `open` branch
// (side-panel chat sessions) is out of scope for oh-my-pig and replies with an
// explicit `exit` refusal the extension can display.
// upstream: native-host.ts:205-255
func ServeNativeHost(ctx context.Context, opts NativeHostServeOptions) error {
	logFn := opts.Log
	if logFn == nil {
		logFn = func(string) {}
	}
	var disconnected atomic.Bool
	var writeMu sync.Mutex
	send := func(msg FromNativeHost) {
		if disconnected.Load() {
			return
		}
		frame, err := EncodeNativeFrame(msg)
		if err != nil {
			logFn(fmt.Sprintf("failed to encode native frame: %v", err))
			return
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		if disconnected.Load() {
			return
		}
		if err := opts.Write(frame); err != nil {
			logFn(fmt.Sprintf("failed to write native frame: %v", err))
		}
	}

	var frameReader NativeFrameReader
	var queued []*ToNativeHost
	readBuf := make([]byte, 32*1024)

	next := func() (*ToNativeHost, error) {
		for {
			if len(queued) > 0 {
				m := queued[0]
				queued = queued[1:]
				return m, nil
			}
			n, err := opts.Input.Read(readBuf)
			if n > 0 {
				msgs, parseErr := frameReader.Push(readBuf[:n])
				if parseErr != nil {
					logFn(fmt.Sprintf("malformed native stream: %v", parseErr))
					disconnected.Store(true)
					return nil, parseErr
				}
				for _, raw := range msgs {
					if m, ok := parseToNativeHost(raw); ok {
						queued = append(queued, m)
					} else {
						snippet := string(raw)
						if len(snippet) > 200 {
							snippet = snippet[:200]
						}
						logFn("ignoring malformed message " + snippet)
					}
				}
			}
			if err != nil {
				if len(queued) > 0 {
					m := queued[0]
					queued = queued[1:]
					return m, nil
				}
				disconnected.Store(true)
				if err == io.EOF {
					return nil, nil
				}
				return nil, err
			}
		}
	}

	first, err := next()
	if err != nil {
		return err
	}
	if first == nil {
		return nil
	}

	if first.T == "link" {
		var chunkSeq atomic.Int64
		ServeRelayLink(ctx, RelayLinkPort{
			Port:        opts.RelayPort,
			ExtensionID: *first.ExtensionID,
			Next: func() (*ToNativeHost, error) {
				for {
					m, err := next()
					if err != nil || m == nil {
						return m, err
					}
					if m.T == "open" && m.ExtensionID == nil {
						continue
					}
					return m, nil
				}
			},
			Send: send,
			RelayLine: func(line string) {
				id := int(chunkSeq.Add(1))
				for _, frame := range LineFrames("relay", line, id) {
					send(frame)
				}
			},
			Log: logFn,
		})
		return nil
	}

	if first.T == "open" {
		// Side-panel chat sessions are intentionally out of scope for oh-my-pig's relay port.
		send(FromNativeHost{
			T:      "exit",
			Code:   nil,
			Stderr: fmt.Sprintf("%s browser relay does not support side-panel chat sessions (`open`); only the browser relay (`link`) is supported.", ExtensionName),
		})
		return nil
	}

	send(FromNativeHost{
		T:      "exit",
		Code:   nil,
		Stderr: "the first message must be open{cwd} or link{extensionId}",
	})
	return nil
}
