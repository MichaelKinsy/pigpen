package native

import (
	"context"
	"errors"
	"fmt"
)

// Kind classifies a failure so that a UI can react without parsing text.
type Kind int

const (
	KindOther Kind = iota
	KindNeedsToken
	KindBroken
	KindExpired
	KindLogin
	KindUnavailable
	KindLive
	KindRateLimited
	KindTimeout
	KindNetwork
)

// Error is a failure with a message fit to show a person.
type Error struct {
	Kind Kind
	Msg  string
	Err  error
}

func (e *Error) Error() string { return e.Msg }
func (e *Error) Unwrap() error { return e.Err }

// classifiers recognise errors of the engine's own libraries; the program that links them registers one (see RegisterClassifier),
// so that this package does not.
var classifiers []func(error) (Kind, string, bool)

// RegisterClassifier adds a recogniser of errors from a library this package does not import. It is called from an init.
func RegisterClassifier(f func(error) (Kind, string, bool)) { classifiers = append(classifiers, f) }

func classify(err error) (Kind, string) {
	for _, f := range classifiers {
		if kind, msg, ok := f(err); ok {
			return kind, msg
		}
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return KindTimeout, "YouTube did not answer in time."
	}
	return KindNetwork, ""
}

// Explain turns an error from WaxTap, the range reader or the network into an
// *Error with a clear message. Nothing in the message carries a URL or an
// address. A cancelled context is returned as it is, and so is an *Error.
func Explain(err error) error {
	if err == nil {
		return nil
	}
	var already *Error
	if errors.As(err, &already) {
		return err
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	kind, msg := classify(err)
	if kind == KindNetwork {
		msg = "the network request failed: " + Redact(err.Error())
	}
	if kind == KindTimeout {
		msg = "YouTube did not answer in time (the network is slow or the track did not respond)."
	}
	return &Error{Kind: kind, Msg: msg, Err: err}
}

// Retryable reports whether resolving again may fix err: an expired link, a
// truncated stream, a dropped connection. Login, token and availability
// failures do not heal.
func Retryable(err error) bool {
	var e *Error
	if !errors.As(err, &e) {
		return false
	}
	switch e.Kind {
	case KindExpired, KindNetwork, KindTimeout:
		return true
	}
	return false
}

func Errorf(kind Kind, format string, a ...any) *Error {
	return &Error{Kind: kind, Msg: Redact(fmt.Sprintf(format, a...))}
}
