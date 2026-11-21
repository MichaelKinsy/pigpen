package pi_subagents

// The parent link: the agreed OS socket between a session and the children it delegates. It is a generic channel for
// parent↔child communication — its first operation is steer delivery (the message joins the child's running turn at the
// host's next safe point; the turn is never interrupted) — and other coordination rides the same socket. Both ends are
// this extension: the parent session listens at the agreed path, a delegated child dials it (PIG_PARENT_SESSION is all
// the child carries; the location is agreed on, never passed) and names itself with its own session id, so the parent
// routes each operation to the right child.
//
// Wire: one JSON object per line. The child opens with {"hello":{"session":"<its session id>"}}; every request is
// {"id":N,"op":"<name>","..."} and its reply is {"id":N,"ok":true} or {"id":N,"ok":false,"error":"..."}.

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// linkAckTimeout is how long a request waits for the child's reply (the original waits 3s for its runner's ack).
const linkAckTimeout = 3 * time.Second

// parentLinkPath is the agreed location of a session's link (an AF_UNIX socket under the temp directory; Go's "unix"
// network is OS-native on Unix and on Windows 10+).
func parentLinkPath(sessionID string) string {
	safe := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, sessionID)
	return filepath.Join(os.TempDir(), "pig-parent-link-"+safe+".sock")
}

// linkConn is one connected child: its socket and the reply reader, one request at a time.
type linkConn struct {
	mu   sync.Mutex
	conn net.Conn
	rd   *bufio.Reader
	next int
}

// parentLink is a session's end of the link.
type parentLink struct {
	mu    sync.Mutex
	ln    net.Listener
	path  string
	conns map[string]*linkConn // by the child's own session id
}

// listenParentLink starts the session's link at the agreed path.
func listenParentLink(sessionID string) (*parentLink, error) {
	path := parentLinkPath(sessionID)
	os.Remove(path) // a stale socket of a crashed session with the same id
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	_ = os.Chmod(path, 0o600) // the link is this user's
	l := &parentLink{ln: ln, path: path, conns: map[string]*linkConn{}}
	go l.accept()
	return l, nil
}

func (l *parentLink) close() {
	l.mu.Lock()
	ln := l.ln
	conns := l.conns
	l.ln, l.conns = nil, map[string]*linkConn{}
	l.mu.Unlock()
	if ln != nil {
		_ = ln.Close()
	}
	os.Remove(l.path)
	for _, c := range conns {
		_ = c.conn.Close()
	}
}

func (l *parentLink) accept() {
	for {
		conn, err := l.ln.Accept()
		if err != nil {
			return
		}
		go l.hello(conn)
	}
}

// hello reads the child's first line, which names it, and keeps the connection for requests.
func (l *parentLink) hello(conn net.Conn) {
	rd := bufio.NewReader(conn)
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	line, err := rd.ReadBytes('\n')
	if err != nil {
		conn.Close()
		return
	}
	var h struct {
		Hello struct {
			Session string `json:"session"`
		} `json:"hello"`
	}
	if json.Unmarshal(line, &h) != nil || h.Hello.Session == "" {
		conn.Close()
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	l.mu.Lock()
	if old := l.conns[h.Hello.Session]; old != nil {
		_ = old.conn.Close() // a later turn of the same child replaces its link
	}
	l.conns[h.Hello.Session] = &linkConn{conn: conn, rd: rd}
	l.mu.Unlock()
}

// op sends one operation to the named child and waits for its reply.
func (l *parentLink) op(session string, request map[string]any) error {
	l.mu.Lock()
	c := l.conns[session]
	l.mu.Unlock()
	if c == nil {
		return errors.New("no link to that child")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.next++
	request["id"] = c.next
	line, err := json.Marshal(request)
	if err != nil {
		return err
	}
	_ = c.conn.SetDeadline(time.Now().Add(linkAckTimeout))
	if _, err := c.conn.Write(append(line, '\n')); err != nil {
		return err
	}
	reply, err := c.rd.ReadBytes('\n')
	if err != nil {
		return err
	}
	var r struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if json.Unmarshal(reply, &r) != nil {
		return errors.New("a malformed reply came back on the link")
	}
	if !r.OK {
		if r.Error != "" {
			return errors.New(r.Error)
		}
		return errors.New("the child refused the operation")
	}
	return nil
}

// serveParentLink dials the parent session's link and serves its operations until either end goes away. apply performs
// one operation; a child with no reachable parent link simply has none (steering falls back to queued turns).
func serveParentLink(parentSession, ownSession string, apply func(op string, request map[string]any) error) {
	path := parentLinkPath(parentSession)
	var conn net.Conn
	for try := 0; try < 5 && conn == nil; try++ {
		c, err := net.Dial("unix", path)
		if err == nil {
			conn = c
		} else {
			time.Sleep(250 * time.Millisecond) // the parent's link may still be starting
		}
	}
	if conn == nil {
		return
	}
	defer conn.Close()
	hello, _ := json.Marshal(map[string]any{"hello": map[string]any{"session": ownSession}})
	if _, err := conn.Write(append(hello, '\n')); err != nil {
		return
	}
	rd := bufio.NewReader(conn)
	for {
		line, err := rd.ReadBytes('\n')
		if err != nil {
			return
		}
		var req map[string]any
		if json.Unmarshal(line, &req) != nil {
			return
		}
		id, _ := req["id"].(float64)
		op, _ := req["op"].(string)
		reply := map[string]any{"id": id}
		if err := apply(op, req); err != nil {
			reply["ok"] = false
			reply["error"] = err.Error()
		} else {
			reply["ok"] = true
		}
		out, _ := json.Marshal(reply)
		if _, err := conn.Write(append(out, '\n')); err != nil {
			return
		}
	}
}

// sessionLink is this session's link with its children, started at session start and used by the steer action.
var (
	sessionLinkMu sync.Mutex
	sessionLink   *parentLink
)

// startSessionLink opens this session's link; without one, steering falls back to queued turns.
func startSessionLink(sessionID string) {
	l, _ := listenParentLink(sessionID)
	sessionLinkMu.Lock()
	old := sessionLink
	sessionLink = l
	sessionLinkMu.Unlock()
	if old != nil {
		old.close()
	}
}

func closeSessionLink() {
	sessionLinkMu.Lock()
	l := sessionLink
	sessionLink = nil
	sessionLinkMu.Unlock()
	if l != nil {
		l.close()
	}
}

// sendToChild runs one operation on a child's link.
func sendToChild(session string, request map[string]any) error {
	sessionLinkMu.Lock()
	l := sessionLink
	sessionLinkMu.Unlock()
	if l == nil {
		return errors.New("this session has no parent link")
	}
	if session == "" {
		return errors.New("the child names no session")
	}
	return l.op(session, request)
}
