package hashline

import (
	"fmt"
)

// Rejection names which check refused an edit whose payload parsed.
// upstream: crates/edit/src/error.rs:55
type Rejection string

const (
	RejectionNone              Rejection = ""
	RejectionStaleTag          Rejection = "stale_tag"
	RejectionUnknownTag        Rejection = "unknown_tag"
	RejectionOtherFileTag      Rejection = "other_file_tag"
	RejectionMissingTag        Rejection = "missing_tag"
	RejectionHeaderOnly        Rejection = "header_only"
	RejectionUnseenLines       Rejection = "unseen_lines"
	RejectionOutOfRange        Rejection = "out_of_range"
	RejectionAmbiguousBoundary Rejection = "ambiguous_boundary"
	RejectionNoChange          Rejection = "no_change"
	RejectionFileNotFound      Rejection = "file_not_found"
)

// ErrorKind classifies an EditError for structured Details (`errorKind`).
// upstream: crates/edit/src/error.rs:98
type ErrorKind string

const (
	ErrorKindParse         ErrorKind = "parse"
	ErrorKindApply         ErrorKind = "apply"
	ErrorKindInvalidUtf8   ErrorKind = "invalid_utf8"
	ErrorKindNoMatch       ErrorKind = "no_match"
	ErrorKindPlanMode      ErrorKind = "plan_mode"
	ErrorKindUnresolvedURL ErrorKind = "unresolved_url"
	ErrorKindIO            ErrorKind = "io"
	ErrorKindWriteFailed   ErrorKind = "write_failed"
)

// EditError is the error type shared across the hashline engine.
// upstream: crates/edit/src/error.rs:11
type EditError struct {
	KindStr string
	Message string
	Line    uint32
	HasLine bool
	Reason  Rejection
	Path    string
	Inner   error
}

func (e *EditError) Error() string {
	return e.Message
}

func (e *EditError) Unwrap() error {
	return e.Inner
}

// Kind returns the stable errorKind string matching crates/edit/src/error.rs:155.
func (e *EditError) Kind() string {
	if e == nil {
		return ""
	}
	if e.Reason != RejectionNone {
		return string(e.Reason)
	}
	if e.KindStr != "" {
		return e.KindStr
	}
	return string(ErrorKindApply)
}

// IsInvalidUtf8 reports whether this error rejects an undecodable target.
// upstream: crates/edit/src/error.rs:145
func (e *EditError) IsInvalidUtf8() bool {
	return e != nil && e.KindStr == string(ErrorKindInvalidUtf8)
}

// ParseError creates a parse failure without a line number.
// upstream: crates/edit/src/error.rs:120
func ParseError(message string) *EditError {
	return &EditError{KindStr: string(ErrorKindParse), Message: message}
}

// ParseErrorAt creates a parse failure at a known payload line.
// upstream: crates/edit/src/error.rs:125
func ParseErrorAt(message string, line uint32) *EditError {
	return &EditError{KindStr: string(ErrorKindParse), Message: message, Line: line, HasLine: true}
}

// ApplyError creates an apply failure carrying a preformatted message.
// upstream: crates/edit/src/error.rs:130
func ApplyError(message string) *EditError {
	return &EditError{KindStr: string(ErrorKindApply), Message: message}
}

// MatchError creates a match failure carrying a preformatted message.
// upstream: crates/edit/src/error.rs:135
func MatchError(message string) *EditError {
	return &EditError{KindStr: string(ErrorKindNoMatch), Message: message}
}

// RejectedError creates a refusal by a named check.
// upstream: crates/edit/src/error.rs:140
func RejectedError(reason Rejection, message string) *EditError {
	return &EditError{KindStr: string(reason), Reason: reason, Message: message}
}

// InvalidUtf8Error creates an invalid-UTF-8 rejection.
// upstream: crates/edit/src/error.rs:23
func InvalidUtf8Error(display string, validUpTo int) *EditError {
	return &EditError{
		KindStr: string(ErrorKindInvalidUtf8),
		Message: fmt.Sprintf("%s is not valid UTF-8 at byte %d; refusing to edit", display, validUpTo),
	}
}

// IOError wraps a filesystem I/O failure.
// upstream: crates/edit/src/error.rs:37
func IOError(path string, err error, notFound bool) *EditError {
	kind := string(ErrorKindIO)
	if notFound {
		kind = string(RejectionFileNotFound)
	}
	return &EditError{
		KindStr: kind,
		Message: err.Error(),
		Path:    path,
		Inner:   err,
	}
}

// PlanError creates a plan-mode refusal.
// upstream: crates/edit/src/error.rs:30
func PlanError(message string) *EditError {
	return &EditError{KindStr: string(ErrorKindPlanMode), Message: message}
}

// InFileError wraps one file's failure in a multi-file call.
// upstream: crates/edit/src/error.rs:49
func InFileError(path string, source *EditError) *EditError {
	return &EditError{
		KindStr: source.Kind(),
		Reason:  source.Reason,
		Path:    path,
		Inner:   source,
		Message: fmt.Sprintf("[%s]: %s\nNo files were modified — sections apply atomically.", path, source.Error()),
	}
}

// ErrorKindOf extracts omp's stable errorKind string from any error.
func ErrorKindOf(err error) string {
	if err == nil {
		return ""
	}
	if ee, ok := err.(*EditError); ok {
		return ee.Kind()
	}
	return string(ErrorKindApply)
}
