// Package mpv drives mpv through its JSON IPC socket: a request/reply client and
// a music.Player built on mpv's own playlist, so the queue lives in mpv and
// survives the extension.
package mpv

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

// Event is a message mpv sent unasked: a property change, a file start and so on.
type Event struct {
	Event string          `json:"event"`
	ID    int             `json:"id,omitempty"` // observe_property id
	Name  string          `json:"name,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
	// Reason and PlaylistEntryID are end-file's: why the entry ended ("error" when it could not be opened) and which one.
	Reason          string `json:"reason,omitempty"`
	PlaylistEntryID int    `json:"playlist_entry_id,omitempty"`
}

// ErrClosed is returned by calls on a connection that has ended.
var ErrClosed = errors.New("mpv connection closed")

type message struct {
	Event     string          `json:"event"`
	ID        int             `json:"id"`
	Name      string          `json:"name"`
	Data      json.RawMessage `json:"data"`
	Error     string          `json:"error"`
	RequestID *int64          `json:"request_id"`
	Reason    string          `json:"reason"`
	EntryID   int             `json:"playlist_entry_id"`
}

// Client is one connection to an mpv IPC socket. Replies are matched to
// requests by request_id; every other message is an event.
type Client struct {
	conn net.Conn

	writeMu sync.Mutex

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan reply
	closed  bool
	err     error

	queue   eventQueue
	done    chan struct{}
	onEvent func(Event)
}

type reply struct {
	data json.RawMessage
	err  error
}

// Dial connects to the socket at path. onEvent receives every event, in order,
// on a goroutine of its own, so it may call back into the client.
func Dial(ctx context.Context, path string, onEvent func(Event)) (*Client, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	return NewClient(conn, onEvent), nil
}

// NewClient runs a client over an established connection.
func NewClient(conn net.Conn, onEvent func(Event)) *Client {
	c := &Client{conn: conn, pending: map[int64]chan reply{}, done: make(chan struct{}), onEvent: onEvent}
	c.queue.init()
	go c.read()
	go c.dispatch()
	return c
}

// Done is closed when the connection ends.
func (c *Client) Done() <-chan struct{} { return c.done }

// Err is why the connection ended, nil while it is open.
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Close ends the connection. It does not stop mpv.
func (c *Client) Close() error {
	c.fail(ErrClosed)
	return nil
}

func (c *Client) fail(err error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.err = err
	pending := c.pending
	c.pending = map[int64]chan reply{}
	c.mu.Unlock()
	_ = c.conn.Close()
	for _, ch := range pending {
		ch <- reply{err: ErrClosed}
	}
	c.queue.close()
	close(c.done)
}

func (c *Client) read() {
	scanner := bufio.NewScanner(c.conn)
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024) // a long playlist is one line
	for scanner.Scan() {
		line := scanner.Bytes()
		var m message
		if err := json.Unmarshal(line, &m); err != nil {
			continue // not a message this client understands
		}
		if m.RequestID != nil {
			c.mu.Lock()
			ch := c.pending[*m.RequestID]
			delete(c.pending, *m.RequestID)
			c.mu.Unlock()
			if ch == nil {
				continue
			}
			if m.Error != "" && m.Error != "success" {
				ch <- reply{err: fmt.Errorf("mpv: %s", m.Error)}
			} else {
				ch <- reply{data: m.Data}
			}
			continue
		}
		if m.Event != "" {
			c.queue.push(Event{Event: m.Event, ID: m.ID, Name: m.Name, Data: m.Data, Reason: m.Reason, PlaylistEntryID: m.EntryID})
		}
	}
	err := scanner.Err()
	if err == nil {
		err = ErrClosed
	}
	c.fail(err)
}

func (c *Client) dispatch() {
	for {
		ev, ok := c.queue.pop()
		if !ok {
			return
		}
		if c.onEvent != nil {
			c.onEvent(ev)
		}
	}
}

// Command sends one command and returns the reply's data. A failing command
// returns an error carrying mpv's own message ("property unavailable").
func (c *Client) Command(ctx context.Context, args ...any) (json.RawMessage, error) {
	ch := make(chan reply, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrClosed
	}
	c.nextID++
	id := c.nextID
	c.pending[id] = ch
	c.mu.Unlock()

	payload, err := json.Marshal(map[string]any{"command": args, "request_id": id})
	if err != nil {
		c.forget(id)
		return nil, err
	}
	c.writeMu.Lock()
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.conn.SetWriteDeadline(deadline)
	}
	_, err = c.conn.Write(append(payload, '\n'))
	_ = c.conn.SetWriteDeadline(time.Time{})
	c.writeMu.Unlock()
	if err != nil {
		c.forget(id)
		c.fail(err)
		return nil, err
	}
	select {
	case r := <-ch:
		return r.data, r.err
	case <-ctx.Done():
		c.forget(id)
		return nil, ctx.Err()
	}
}

func (c *Client) forget(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// eventQueue is an unbounded FIFO, so the reader never blocks on a slow event
// consumer (which could be waiting for a reply the reader has to deliver).
type eventQueue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	items  []Event
	closed bool
}

func (q *eventQueue) init() { q.cond = sync.NewCond(&q.mu) }

func (q *eventQueue) push(e Event) {
	q.mu.Lock()
	if !q.closed {
		q.items = append(q.items, e)
		q.cond.Signal()
	}
	q.mu.Unlock()
}

func (q *eventQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.cond.Broadcast()
	q.mu.Unlock()
}

// pop returns the next event, and false once the queue is closed and drained.
func (q *eventQueue) pop() (Event, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.items) == 0 && !q.closed {
		q.cond.Wait()
	}
	if len(q.items) == 0 {
		return Event{}, false
	}
	e := q.items[0]
	q.items = q.items[1:]
	return e, true
}
