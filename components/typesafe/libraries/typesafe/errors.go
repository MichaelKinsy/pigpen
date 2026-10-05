package typesafe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

// TypeSafeError is the base of every error this package returns: configuration
// mistakes, invalid questions, API errors, connection failures and aborts. Every other
// error type unwraps to a *TypeSafeError, so
//
//	var base *typesafe.TypeSafeError
//	errors.As(err, &base)
//
// matches them all. The hierarchy of the TypeScript SDK maps to errors.As targets:
//
//	TypeSafeError
//	├─ APIError (Status, Header, Body, RequestID)
//	│  ├─ BadRequestError 400          ├─ AuthenticationError 401
//	│  ├─ PermissionDeniedError 403    ├─ NotFoundError 404
//	│  ├─ UnprocessableEntityError 422 ├─ RateLimitError 429 (RetryAfter)
//	│  └─ InternalServerError >= 500   (any other status: a bare *APIError)
//	├─ APIConnectionError (Cause)
//	│  └─ APITimeoutError (Timeout)
//	└─ APIUserAbortError (Cause: the context error)
//
// Each subclass embeds a pointer to its parent and unwraps to it.
type TypeSafeError struct {
	Message string
	Cause   error
}

func (e *TypeSafeError) Error() string { return e.Message }
func (e *TypeSafeError) Unwrap() error { return e.Cause }

func errorf(format string, args ...any) *TypeSafeError {
	return &TypeSafeError{Message: fmt.Sprintf(format, args...)}
}

// APIError is an unsuccessful HTTP response from the API.
type APIError struct {
	*TypeSafeError
	// Status is the HTTP status code.
	Status int
	// Header holds the response headers.
	Header http.Header
	// Body is the parsed JSON (map[string]any, []any, float64, bool), the response text
	// (string), or nil for an empty body.
	Body any
	// RequestID is the x-typesafe-request-id header, or "".
	RequestID string
}

// requestIDHeader is the response header that carries the request ID.
const requestIDHeader = "x-typesafe-request-id"

// NewAPIError returns the error type for an HTTP status (the TypeScript
// APIError.fromResponse): a *BadRequestError for 400, and so on. The result's Error
// text is "<status> <detail>" where detail is extracted from the body (error string,
// error.message, message, detail string, detail.message, or FastAPI validation
// entries formatted as "loc: msg" joined by "; "), or the raw body truncated to 200
// characters plus "…", or "status code (no body)".
func NewAPIError(status int, body any, header http.Header) error {
	return newAPIError(status, body, "", header)
}

// newAPIError is NewAPIError with the raw JSON text of the body, which keeps the key
// order of the server's answer in the message.
func newAPIError(status int, body any, rawJSON string, header http.Header) error {
	if header == nil {
		header = http.Header{}
	}
	base := &APIError{
		TypeSafeError: &TypeSafeError{Message: describeAPIError(status, body, rawJSON)},
		Status:        status,
		Header:        header,
		Body:          body,
		RequestID:     header.Get(requestIDHeader),
	}
	switch {
	case status == 400:
		return &BadRequestError{base}
	case status == 401:
		return &AuthenticationError{base}
	case status == 403:
		return &PermissionDeniedError{base}
	case status == 404:
		return &NotFoundError{base}
	case status == 422:
		return &UnprocessableEntityError{base}
	case status == 429:
		e := &RateLimitError{APIError: base}
		e.RetryAfter, e.HasRetryAfter = ParseRetryAfter(header, time.Now())
		return e
	case status >= 500:
		return &InternalServerError{base}
	}
	return base
}

func (e *APIError) Error() string { return e.Message }
func (e *APIError) Unwrap() error { return e.TypeSafeError }

const maxRawBodyInMessage = 200

func describeAPIError(status int, body any, rawJSON string) string {
	if detail := extractMessage(body); detail != "" {
		return fmt.Sprintf("%d %s", status, detail)
	}
	if body == nil {
		return fmt.Sprintf("%d status code (no body)", status)
	}
	raw := ""
	if s, ok := body.(string); ok {
		raw = s
	} else if rawJSON != "" {
		var buf bytes.Buffer
		if json.Compact(&buf, []byte(rawJSON)) == nil {
			raw = buf.String()
		} else {
			raw = rawJSON
		}
	} else if b, err := json.Marshal(body); err == nil {
		raw = string(b)
	}
	units := utf16.Encode([]rune(raw))
	if len(units) > maxRawBodyInMessage {
		raw = string(utf16.Decode(units[:maxRawBodyInMessage])) + "…"
	}
	return fmt.Sprintf("%d %s", status, raw)
}

// extractMessage returns the message of a text, error, or validation response body.
func extractMessage(body any) string {
	if s, ok := body.(string); ok {
		return s
	}
	m, ok := body.(map[string]any)
	if !ok {
		return ""
	}
	if s, ok := m["error"].(string); ok {
		return s
	}
	if e, ok := m["error"].(map[string]any); ok {
		if s, ok := e["message"].(string); ok {
			return s
		}
	}
	if s, ok := m["message"].(string); ok {
		return s
	}
	if s, ok := m["detail"].(string); ok {
		return s
	}
	switch d := m["detail"].(type) {
	case map[string]any:
		if s, ok := d["message"].(string); ok {
			return s
		}
	case []any:
		return describeValidationErrors(d)
	}
	return ""
}

// describeValidationErrors formats validation errors as semicolon-separated "path: message" entries.
func describeValidationErrors(errs []any) string {
	var parts []string
	for _, e := range errs {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		msg, ok := m["msg"].(string)
		if !ok {
			continue
		}
		var loc []string
		if l, ok := m["loc"].([]any); ok {
			for _, x := range l {
				if s, isStr := x.(string); isStr && s == "body" {
					continue
				}
				loc = append(loc, jsString(x))
			}
		}
		if p := strings.Join(loc, "."); p != "" {
			parts = append(parts, p+": "+msg)
		} else {
			parts = append(parts, msg)
		}
	}
	return strings.Join(parts, "; ")
}

// jsString is JavaScript's String(x) for a decoded JSON scalar (array join semantics: null is empty).
func jsString(x any) string {
	switch v := x.(type) {
	case nil:
		return ""
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	}
	b, _ := json.Marshal(x)
	return string(b)
}

// BadRequestError is HTTP 400.
type BadRequestError struct{ *APIError }

// AuthenticationError is HTTP 401.
type AuthenticationError struct{ *APIError }

// PermissionDeniedError is HTTP 403.
type PermissionDeniedError struct{ *APIError }

// NotFoundError is HTTP 404.
type NotFoundError struct{ *APIError }

// UnprocessableEntityError is HTTP 422.
type UnprocessableEntityError struct{ *APIError }

// InternalServerError is HTTP 5xx (500 and above).
type InternalServerError struct{ *APIError }

// RateLimitError is HTTP 429.
type RateLimitError struct {
	*APIError
	// RetryAfter is the server's retry delay from retry-after-ms or Retry-After;
	// HasRetryAfter is false when the header is absent or invalid.
	RetryAfter    time.Duration
	HasRetryAfter bool
}

func (e *BadRequestError) Unwrap() error          { return e.APIError }
func (e *AuthenticationError) Unwrap() error      { return e.APIError }
func (e *PermissionDeniedError) Unwrap() error    { return e.APIError }
func (e *NotFoundError) Unwrap() error            { return e.APIError }
func (e *UnprocessableEntityError) Unwrap() error { return e.APIError }
func (e *InternalServerError) Unwrap() error      { return e.APIError }
func (e *RateLimitError) Unwrap() error           { return e.APIError }

// APIConnectionError is a failed request or response-body delivery (DNS, TLS, closed
// connection, interrupted body). The message is "Connection error: <cause>".
type APIConnectionError struct {
	*TypeSafeError
}

// APITimeoutError is a request whose full response did not arrive within the
// per-attempt timeout; it is also an *APIConnectionError. The message is
// "Request timed out after <n>ms."
type APITimeoutError struct {
	*APIConnectionError
	// TimeoutMs is the configured timeout in milliseconds.
	TimeoutMs int64
}

// APIUserAbortError is a request cancelled through its context. Cause is the context
// error, so errors.Is(err, context.Canceled) holds. Message: "Request was aborted."
type APIUserAbortError struct {
	*TypeSafeError
}

func (e *APIConnectionError) Unwrap() error { return e.TypeSafeError }
func (e *APITimeoutError) Unwrap() error    { return e.APIConnectionError }
func (e *APIUserAbortError) Unwrap() error  { return e.TypeSafeError }

func newConnectionError(cause error) *APIConnectionError {
	msg := "Connection error."
	if cause != nil {
		msg = "Connection error: " + cause.Error()
	}
	return &APIConnectionError{&TypeSafeError{Message: msg, Cause: cause}}
}

func newTimeoutError(timeout time.Duration, cause error) *APITimeoutError {
	ms := timeout.Milliseconds()
	return &APITimeoutError{
		APIConnectionError: &APIConnectionError{&TypeSafeError{Message: fmt.Sprintf("Request timed out after %dms.", ms), Cause: cause}},
		TimeoutMs:          ms,
	}
}

func newAbortError(cause error) *APIUserAbortError {
	return &APIUserAbortError{&TypeSafeError{Message: "Request was aborted.", Cause: cause}}
}

// NewConnectionError returns an *APIConnectionError with the SDK's message for cause.
// Sources of model or HTTP calls use it to report transport failures the retry policy
// recognizes.
func NewConnectionError(cause error) *APIConnectionError { return newConnectionError(cause) }

// NewTimeoutError returns an *APITimeoutError for a timeout that elapsed.
func NewTimeoutError(timeout time.Duration, cause error) *APITimeoutError {
	return newTimeoutError(timeout, cause)
}

// NewAbortError returns an *APIUserAbortError carrying the context's cause.
func NewAbortError(cause error) *APIUserAbortError { return newAbortError(cause) }
