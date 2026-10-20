//go:build windows

package herdr

import (
	"os"
	"sync"
	"syscall"
	"time"
)

var cancelIoEx = syscall.NewLazyDLL("kernel32.dll").NewProc("CancelIoEx")

// pipeConn is a herdr named pipe opened as a file. A synchronous handle has no read deadline, so the deadline cancels
// the pending I/O instead: once it passes, a read or write that is blocked (or starts blocking) is cancelled until the
// handle is closed.
type pipeConn struct {
	f       *os.File
	handle  syscall.Handle
	mu      sync.Mutex
	expired bool
	closed  bool
}

func (c *pipeConn) Write(p []byte) (int, error) {
	if c.hasExpired() {
		return 0, os.ErrDeadlineExceeded
	}
	n, err := c.f.Write(p)
	if err != nil && c.hasExpired() {
		err = os.ErrDeadlineExceeded
	}
	return n, err
}

func (c *pipeConn) Read(p []byte) (int, error) {
	if c.hasExpired() {
		return 0, os.ErrDeadlineExceeded
	}
	n, err := c.f.Read(p)
	if err != nil && c.hasExpired() {
		err = os.ErrDeadlineExceeded
	}
	return n, err
}

func (c *pipeConn) hasExpired() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.expired
}

// expire marks the deadline as passed and cancels the pending I/O until the connection is closed.
func (c *pipeConn) expire() {
	c.mu.Lock()
	c.expired = true
	c.mu.Unlock()
	for {
		c.mu.Lock()
		closed := c.closed
		c.mu.Unlock()
		if closed {
			return
		}
		_, _, _ = cancelIoEx.Call(uintptr(c.handle), 0)
		time.Sleep(20 * time.Millisecond)
	}
}

func (c *pipeConn) close() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	_ = c.f.Close()
}

// attempt opens herdr's named pipe, sends one request line and waits for the first data back, all within timeout
// (ts:34-54).
func attempt(endpoint string, line []byte, timeout time.Duration) error {
	f, err := os.OpenFile(endpoint, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	c := &pipeConn{f: f, handle: syscall.Handle(f.Fd())}
	defer c.close()
	timer := time.AfterFunc(timeout, c.expire)
	defer timer.Stop()
	return exchange(c, line)
}
