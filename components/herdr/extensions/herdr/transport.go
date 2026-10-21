package herdr

import (
	"errors"
	"io"
)

// exchange is one attempt on an open connection (ts:29-55): write the request line on connect, and the attempt
// delivered as soon as any data comes back. The connection ending first, or the deadline, is a failure.
func exchange(conn io.ReadWriter, line []byte) error {
	if _, err := conn.Write(line); err != nil {
		return err
	}
	var buf [4096]byte
	n, err := conn.Read(buf[:])
	if n > 0 {
		return nil
	}
	if err == nil || errors.Is(err, io.EOF) {
		return errors.New("herdr closed the connection without an answer")
	}
	return err
}
