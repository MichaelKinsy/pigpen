//go:build !windows

package herdr

import (
	"context"
	"net"
	"time"
)

// attempt connects to herdr's unix socket, sends one request line and waits for the first data back. The whole
// attempt, the connect included, is bounded by timeout (ts:34-54).
func attempt(endpoint string, line []byte, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", endpoint)
	if err != nil {
		return err
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	return exchange(conn, line)
}
