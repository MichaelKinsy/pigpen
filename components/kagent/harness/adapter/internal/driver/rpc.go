package driver

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// MaxFrameBytes is PiG's own command-reader limit, applied to its output too.
const MaxFrameBytes = 16 << 20

// frame is one JSONL record from pig's stdout, or the error that ended the stream.
type frame struct {
	kind string // the record's "type"
	raw  []byte
	err  error
}

// rpcClient is the hand-rolled JSONL side of `pig --mode rpc`.
type rpcClient struct {
	mu     sync.Mutex
	stdin  io.WriteCloser
	closed bool
	frames chan frame
	done   chan struct{}
}

func newRPCClient(stdin io.WriteCloser, stdout io.Reader, maxFrame int) *rpcClient {
	c := &rpcClient{stdin: stdin, frames: make(chan frame, 64), done: make(chan struct{})}
	go func() {
		defer close(c.done)
		defer close(c.frames)
		readFrames(stdout, maxFrame, c.frames)
	}()
	return c
}

// readFrames splits stdout on LF only and enforces the frame cap while the
// line is still being read, so a runaway record never sits in memory whole.
func readFrames(r io.Reader, maxFrame int, out chan<- frame) {
	reader := bufio.NewReaderSize(r, 1<<20)
	for {
		var line []byte
		for {
			chunk, err := reader.ReadSlice('\n')
			line = append(line, chunk...)
			if len(line) > maxFrame {
				out <- frame{err: fmt.Errorf("PiG frame exceeds %d bytes", maxFrame)}
				return
			}
			if errors.Is(err, bufio.ErrBufferFull) {
				continue
			}
			if err != nil {
				if len(bytes.TrimSpace(line)) > 0 {
					out <- frame{err: fmt.Errorf("PiG stream closed inside a record: %w", err)}
				}
				return
			}
			break
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(line, &head); err != nil || head.Type == "" {
			out <- frame{err: errors.New("PiG sent a record that is not an event")}
			return
		}
		out <- frame{kind: head.Type, raw: line}
	}
}

// send writes one command record.
func (c *rpcClient) send(command any) error {
	data, err := json.Marshal(command)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("PiG stdin is closed")
	}
	if _, err := c.stdin.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write to PiG: %w", err)
	}
	return nil
}

// closeStdin is PiG's documented shutdown: it cancels the active prompt and
// every outstanding extension request, then exits.
func (c *rpcClient) closeStdin() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		_ = c.stdin.Close()
	}
}

// Wire shapes for the records the adapter consumes.

type responseRecord struct {
	ID      string          `json:"id"`
	Command string          `json:"command"`
	Success bool            `json:"success"`
	Error   string          `json:"error"`
	Data    json.RawMessage `json:"data"`
}

type usageWire struct {
	Input       int64 `json:"input"`
	Output      int64 `json:"output"`
	CacheRead   int64 `json:"cacheRead"`
	CacheWrite  int64 `json:"cacheWrite"`
	Reasoning   int64 `json:"reasoning"`
	TotalTokens int64 `json:"totalTokens"`
}

type messageWire struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	StopReason string          `json:"stopReason"`
	Provider   string          `json:"provider"`
	Model      string          `json:"model"`
	ResponseID string          `json:"responseId"`
	Timestamp  int64           `json:"timestamp"`
	DurationMs int64           `json:"durationMs"`
	Usage      *usageWire      `json:"usage"`
}

type messageRecord struct {
	Message               *messageWire `json:"message"`
	AssistantMessageEvent *struct {
		Type  string `json:"type"`
		Delta string `json:"delta"`
	} `json:"assistantMessageEvent"`
}

type toolRecord struct {
	ToolCallID string          `json:"toolCallId"`
	ToolName   string          `json:"toolName"`
	Args       json.RawMessage `json:"args"`
	Result     json.RawMessage `json:"result"`
	IsError    bool            `json:"isError"`
}

type uiRequestRecord struct {
	ID         string `json:"id"`
	Method     string `json:"method"`
	Message    string `json:"message"`
	NotifyType string `json:"notifyType"`
}

type settledRecord struct {
	Aborted bool `json:"aborted"`
}
