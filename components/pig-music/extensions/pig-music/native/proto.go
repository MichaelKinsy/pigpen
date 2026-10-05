package native

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// The daemon speaks one JSON object per line over a local socket (a unix
// socket, or a named pipe on Windows). A client sends requests with an id; the
// daemon answers each with a response carrying the same id, and, once a client
// has subscribed, pushes state events in between.

// ProtocolVersion changes when a request or reply stops being compatible.
const ProtocolVersion = 1

// maxLine bounds one message: a queue of thousands of tracks fits, a runaway does not.
const maxLine = 8 << 20

// Request commands.
const (
	cmdHello     = "hello"
	cmdState     = "state"
	cmdSubscribe = "subscribe"
	cmdReplace   = "replace"
	cmdEnqueue   = "enqueue"
	cmdRemove    = "remove"
	cmdMove      = "move"
	cmdJump      = "jump"
	cmdNext      = "next"
	cmdPrev      = "prev"
	cmdPause     = "pause"
	cmdToggle    = "toggle"
	cmdSeek      = "seek"
	cmdVolume    = "volume"
	cmdMeter     = "meter" // switches level measuring on or off
	cmdLevel     = "level" // reads the latest level
	cmdShutdown  = "shutdown"
)

// Reply error codes that stand for the sentinel errors.
const (
	codeEndOfQueue     = "end-of-queue"
	codeNothingPlaying = "nothing-playing"
	codeNotSeekable    = "not-seekable"
)

// message is every request, response and event; unused fields are omitted.
type message struct {
	ID    int64  `json:"id,omitempty"`
	Cmd   string `json:"cmd,omitempty"`
	Event string `json:"event,omitempty"` // "state"

	Tracks []music.Track `json:"tracks,omitempty"`
	Index  int           `json:"index,omitempty"`
	To     int           `json:"to,omitempty"`
	Paused bool          `json:"paused,omitempty"`
	Millis int64         `json:"millis,omitempty"` // seek, relative
	Volume int           `json:"volume,omitempty"`
	Meter  *bool         `json:"meter,omitempty"` // cmdMeter: on or off

	// Responses and events.
	OK        bool         `json:"ok,omitempty"`
	Error     string       `json:"error,omitempty"`
	Code      string       `json:"code,omitempty"`
	State     *music.State `json:"state,omitempty"`
	LastError string       `json:"lastError,omitempty"`
	Level     *levelReply  `json:"level,omitempty"`
	PID       int          `json:"pid,omitempty"`
	Version   int          `json:"version,omitempty"`
}

func codeFor(err error) string {
	switch {
	case errors.Is(err, ErrEndOfQueue):
		return codeEndOfQueue
	case errors.Is(err, ErrNothingPlaying):
		return codeNothingPlaying
	case errors.Is(err, ErrNotSeekable):
		return codeNotSeekable
	}
	return ""
}

func errorFor(code, msg string) error {
	switch code {
	case codeEndOfQueue:
		return ErrEndOfQueue
	case codeNothingPlaying:
		return ErrNothingPlaying
	case codeNotSeekable:
		return ErrNotSeekable
	}
	return errors.New(msg)
}

// codec reads and writes messages on one connection.
type codec struct {
	r *bufio.Reader
	w io.Writer
}

func newCodec(rw io.ReadWriter) *codec {
	return &codec{r: bufio.NewReaderSize(rw, 64<<10), w: rw}
}

// read returns the next message; io.EOF when the peer closed.
func (c *codec) read() (*message, error) {
	var line []byte
	for {
		chunk, isPrefix, err := c.r.ReadLine()
		if err != nil {
			return nil, err
		}
		line = append(line, chunk...)
		if len(line) > maxLine {
			return nil, fmt.Errorf("message over %d bytes", maxLine)
		}
		if !isPrefix {
			break
		}
	}
	var m message
	if err := json.Unmarshal(line, &m); err != nil {
		return nil, fmt.Errorf("malformed message: %w", err)
	}
	return &m, nil
}

// write sends one message. Callers serialize writes.
func (c *codec) write(m *message) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = c.w.Write(append(data, '\n'))
	return err
}

// levelReply is the answer to cmdLevel.
type levelReply struct {
	RMS  float64 `json:"rms"`
	Peak float64 `json:"peak"`
	OK   bool    `json:"ok"`
}
