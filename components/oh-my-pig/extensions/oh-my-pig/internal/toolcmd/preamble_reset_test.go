package toolcmd

import (
	"bytes"
	"errors"
	"net"
	"os/exec"
	"testing"
)

// A bridge that refuses a call closes the connection with the request unread, which the kernel reports to the
// client as a reset. The dispatcher must then say so and exit 1 every time. Before the request was written by a
// child shell that ignores SIGPIPE, a write that came after the reset ended the dispatcher with 141 and nothing on
// stderr (most runs on Linux, a few in a hundred on macOS), and the other runs showed bash's own read error first.
func TestDispatcherReportsAConnectionTheBridgeReset(t *testing.T) {
	bashPath := findBash(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			// The first byte of the request proves the client's connect has returned: a reset that races the
			// connect makes bash's /dev/tcp open fail on macOS, which is the "bridge unavailable" path, not this one.
			_, _ = conn.Read(make([]byte, 1))
			// Linger 0 with the rest of the request unread: Close sends a reset.
			_ = conn.(*net.TCPConn).SetLinger(0)
			_ = conn.Close()
		}
	}()

	script := renderDispatcherFunction(listener.Addr().(*net.TCPAddr).Port, "token") + "\n__omp_tc_call tool hello\n"
	for run := range 40 {
		var stdout, stderr bytes.Buffer
		cmd := exec.Command(bashPath, "-c", script)
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		code := 0
		if err := cmd.Run(); err != nil {
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				t.Fatal(err)
			}
			code = exit.ExitCode()
		}
		if code != 1 || stdout.Len() != 0 || stderr.String() != "tool: tool-command bridge closed the connection\n" {
			t.Fatalf("run %d: exit %d, stdout %q, stderr %q", run, code, stdout.String(), stderr.String())
		}
	}
}
