//go:build windows

package ternwire

import (
	"context"
	"fmt"
	"net"
)

type unixSocketTransport struct {
	socketPath string
}

func newUnixSocketTransport(socketPath string) Transport {
	return &unixSocketTransport{socketPath: socketPath}
}

func (t *unixSocketTransport) Product() string      { return "Tern" }
func (t *unixSocketTransport) Peer() string         { return fmt.Sprintf("Tern daemon at %s", t.socketPath) }
func (t *unixSocketTransport) WaitsForEvents() bool { return false }
func (t *unixSocketTransport) GreetingHint() string {
	return "a Tern without omp's JSON protocol cannot read its hello (update Tern)"
}

func (t *unixSocketTransport) Connect(_ context.Context) (net.Conn, error) {
	return nil, &Error{
		Kind:    ErrUnsupported,
		Message: "Tern Unix socket transport is not supported on Windows",
	}
}
