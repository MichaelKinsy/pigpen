package ternwire

import "errors"

// ErrorKind classifies failures from the Tern protocol or the client itself.
// upstream: packages/coding-agent/src/tools/browser/tern/wire.ts:29
type ErrorKind string

const (
	ErrInvalid      ErrorKind = "invalid"
	ErrNotFound     ErrorKind = "not_found"
	ErrNotAgent     ErrorKind = "not_agent"
	ErrNoWindow     ErrorKind = "no_window"
	ErrWindowClosed ErrorKind = "window_closed"
	ErrUnsupported  ErrorKind = "unsupported"
	ErrJS           ErrorKind = "js"
	ErrFailed       ErrorKind = "failed"
	ErrUnresponsive ErrorKind = "unresponsive"
	ErrConnect      ErrorKind = "connect"
	ErrClosed       ErrorKind = "closed"
	ErrTimeout      ErrorKind = "timeout"
	ErrProtocol     ErrorKind = "protocol"
)

// Error is a failed request or connection of the Tern protocol, carrying the
// protocol's error kind.
// upstream: packages/coding-agent/src/tools/browser/tern/wire.ts:49
type Error struct {
	Kind    ErrorKind
	Message string
}

func (e *Error) Error() string {
	return e.Message
}

// protocolErrorKinds maps protocol error.kind strings to ErrorKind.
// upstream: packages/coding-agent/src/tools/browser/tern/wire.ts:338
var protocolErrorKinds = map[string]ErrorKind{
	"invalid":       ErrInvalid,
	"not_found":     ErrNotFound,
	"not_agent":     ErrNotAgent,
	"no_window":     ErrNoWindow,
	"window_closed": ErrWindowClosed,
	"unsupported":   ErrUnsupported,
	"js":            ErrJS,
	"failed":        ErrFailed,
	"unresponsive":  ErrUnresponsive,
}

// IsUnavailable reports whether err says Tern cannot host a browser at all
// (auto mode falls back to another backend).
// upstream: packages/coding-agent/src/tools/browser/tern/wire.ts:67
func IsUnavailable(err error) bool {
	var te *Error
	if !errors.As(err, &te) {
		return false
	}
	switch te.Kind {
	case ErrNoWindow, ErrUnsupported, ErrConnect:
		return true
	default:
		return false
	}
}
