package mpv

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

func ctx5(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// scripted is a raw mpv stand-in: the test reads each request and writes
// whatever it likes, in any order.
type scripted struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
}

func newScripted(t *testing.T) (*Client, *scripted, *eventLog) {
	t.Helper()
	a, b := net.Pipe()
	log := &eventLog{}
	c := NewClient(a, log.add)
	t.Cleanup(func() { _ = c.Close(); _ = b.Close() })
	return c, &scripted{t: t, conn: b, r: bufio.NewReader(b)}, log
}

func (s *scripted) request() map[string]any {
	s.t.Helper()
	line, err := s.r.ReadBytes('\n')
	if err != nil {
		s.t.Fatalf("read request: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(line, &m); err != nil {
		s.t.Fatalf("bad request %q: %v", line, err)
	}
	return m
}

func (s *scripted) send(line string) {
	s.t.Helper()
	if _, err := s.conn.Write([]byte(line + "\n")); err != nil {
		s.t.Fatalf("write: %v", err)
	}
}

type eventLog struct {
	mu     sync.Mutex
	events []Event
}

func (l *eventLog) add(e Event) { l.mu.Lock(); l.events = append(l.events, e); l.mu.Unlock() }
func (l *eventLog) all() []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Event(nil), l.events...)
}

func TestRepliesAreMatchedByRequestIDEvenOutOfOrder(t *testing.T) {
	c, s, _ := newScripted(t)
	type result struct {
		data string
		err  error
	}
	first, second := make(chan result, 1), make(chan result, 1)
	go func() { d, err := c.Command(ctx5(t), "get_property", "volume"); first <- result{string(d), err} }()
	r1 := s.request()
	go func() { d, err := c.Command(ctx5(t), "get_property", "pause"); second <- result{string(d), err} }()
	r2 := s.request()
	id1, id2 := r1["request_id"].(float64), r2["request_id"].(float64)
	if id1 == id2 {
		t.Fatalf("both requests carry request_id %v", id1)
	}
	// Answer the second request first.
	s.send(`{"request_id":` + itoa(id2) + `,"error":"success","data":true}`)
	s.send(`{"request_id":` + itoa(id1) + `,"error":"success","data":55}`)
	if got := <-first; got.err != nil || got.data != "55" {
		t.Errorf("first = %+v, want 55", got)
	}
	if got := <-second; got.err != nil || got.data != "true" {
		t.Errorf("second = %+v, want true", got)
	}
}

func itoa(f float64) string { b, _ := json.Marshal(int64(f)); return string(b) }

func TestAFailingCommandReturnsMpvsMessage(t *testing.T) {
	c, s, _ := newScripted(t)
	done := make(chan error, 1)
	go func() { _, err := c.Command(ctx5(t), "get_property", "time-pos"); done <- err }()
	req := s.request()
	s.send(`{"request_id":` + itoa(req["request_id"].(float64)) + `,"error":"property unavailable"}`)
	if err := <-done; err == nil || err.Error() != "mpv: property unavailable" {
		t.Fatalf("err = %v", err)
	}
}

func TestUnsolicitedMessagesAreEventsInOrderAndNeverReplies(t *testing.T) {
	c, s, log := newScripted(t)
	done := make(chan error, 1)
	go func() { _, err := c.Command(ctx5(t), "get_property", "pause"); done <- err }()
	req := s.request()
	// Events around the reply, one with an id that collides with the request id.
	s.send(`{"event":"property-change","id":1,"name":"pause","data":false}`)
	s.send(`{"event":"file-loaded"}`)
	s.send(`{"request_id":` + itoa(req["request_id"].(float64)) + `,"error":"success","data":false}`)
	s.send(`{"event":"property-change","id":2,"name":"time-pos","data":1.5}`)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "three events", func() bool { return len(log.all()) == 3 })
	got := log.all()
	if got[0].Name != "pause" || got[1].Event != "file-loaded" || got[2].Name != "time-pos" || string(got[2].Data) != "1.5" {
		t.Fatalf("events = %+v", got)
	}
}

func TestMessagesThatAreNotJSONAreSkipped(t *testing.T) {
	c, s, log := newScripted(t)
	s.send("not json at all")
	s.send(`{"event":"idle"}`)
	waitUntil(t, "the idle event", func() bool { return len(log.all()) == 1 })
	_ = c
}

func TestAnEventConsumerMayCallBackIntoTheClient(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	got := make(chan error, 1)
	var c *Client
	c = NewClient(a, func(Event) {
		_, err := c.Command(ctx5(t), "get_property", "volume") // needs the reader to deliver the reply
		got <- err
	})
	defer c.Close()
	s := &scripted{t: t, conn: b, r: bufio.NewReader(b)}
	s.send(`{"event":"idle"}`)
	req := s.request()
	s.send(`{"request_id":` + itoa(req["request_id"].(float64)) + `,"error":"success","data":1}`)
	select {
	case err := <-got:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a command made from an event handler never returned")
	}
}

func TestPendingCommandsFailWhenTheConnectionDrops(t *testing.T) {
	c, s, _ := newScripted(t)
	done := make(chan error, 1)
	go func() { _, err := c.Command(ctx5(t), "get_property", "volume"); done <- err }()
	s.request()
	_ = s.conn.Close()
	if err := <-done; !errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v, want ErrClosed", err)
	}
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Done never closed")
	}
	if _, err := c.Command(ctx5(t), "get_property", "volume"); !errors.Is(err, ErrClosed) {
		t.Fatalf("command after close: %v", err)
	}
}

func TestACancelledCommandReturnsAndLeavesNoPendingReply(t *testing.T) {
	c, s, _ := newScripted(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := c.Command(ctx, "get_property", "volume"); done <- err }()
	req := s.request()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	// A late reply is dropped without disturbing the next command.
	s.send(`{"request_id":` + itoa(req["request_id"].(float64)) + `,"error":"success","data":1}`)
	next := make(chan string, 1)
	go func() { d, _ := c.Command(ctx5(t), "get_property", "pause"); next <- string(d) }()
	r2 := s.request()
	s.send(`{"request_id":` + itoa(r2["request_id"].(float64)) + `,"error":"success","data":true}`)
	if got := <-next; got != "true" {
		t.Fatalf("next = %q", got)
	}
}

func TestALongPlaylistLineIsOneMessage(t *testing.T) {
	c, s, _ := newScripted(t)
	done := make(chan json.RawMessage, 1)
	go func() { d, _ := c.Command(ctx5(t), "get_property", "playlist"); done <- d }()
	req := s.request()
	big := make([]byte, 0, 3<<20)
	big = append(big, '[')
	for i := 0; i < 20000; i++ {
		if i > 0 {
			big = append(big, ',')
		}
		big = append(big, `{"filename":"https://music.youtube.com/watch?v=aaaaaaaaaaa","id":1}`...)
	}
	big = append(big, ']')
	s.send(`{"request_id":` + itoa(req["request_id"].(float64)) + `,"error":"success","data":` + string(big) + `}`)
	if d := <-done; len(d) != len(big) {
		t.Fatalf("got %d bytes, want %d", len(d), len(big))
	}
}

func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}
