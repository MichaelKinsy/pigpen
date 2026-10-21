package websearch

import (
	"errors"
	"strings"
)

// The port refuses what it does not implement with one message that names the working
// alternative: the original extension, which runs unchanged in PiG. The package name is this
// Package's package.json name and the spec is the original pinned in provenance.json (the
// layout test refusal_test.go keeps both in step). A refusal is returned as a tool error (or
// a command notification); it is never a silent fallback and never a partial run.
const (
	portPackage  = "@pi-in-go/pigpen-websearch"
	originalSpec = "npm:pi-web-access@0.33.0"
)

// UnsupportedError is the refusal of one unported feature.
type UnsupportedError struct{ Feature string }

func (e *UnsupportedError) Error() string {
	return portPackage + " (Go port) does not support " + e.Feature + " yet. Install the original extension, " + originalSpec + ", which runs unchanged in PiG."
}

func unsupported(feature string) *UnsupportedError { return &UnsupportedError{Feature: feature} }

// asUnsupported finds a refusal in err's chain.
func asUnsupported(err error) *UnsupportedError {
	var u *UnsupportedError
	if errors.As(err, &u) {
		return u
	}
	return nil
}

// isRefusalText reports whether msg is a refusal message (a per-URL fetch error carries the text).
func isRefusalText(msg string) bool {
	return strings.HasPrefix(msg, portPackage+" (Go port) does not support ")
}

// refusalIn extracts the refusal a per-URL error carries (alone, or after the original's own
// verdict), or nil when the error is not one.
func refusalIn(msg string) error {
	i := strings.Index(msg, portPackage+" (Go port) does not support ")
	if i < 0 {
		return nil
	}
	msg = msg[i:]
	const tail = "which runs unchanged in PiG."
	if j := strings.Index(msg, tail); j >= 0 {
		msg = msg[:j+len(tail)]
	}
	return errors.New(msg)
}
