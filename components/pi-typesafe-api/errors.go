package pitypesafe

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/typesafe"
)

// ErrorCode classifies an IntegrationError.
type ErrorCode string

// The error codes.
const (
	CodeConfiguration ErrorCode = "configuration"
	CodeValidation    ErrorCode = "validation"
	CodeBudget        ErrorCode = "budget"
	CodeAborted       ErrorCode = "aborted"
	CodeTimeout       ErrorCode = "timeout"
	CodeHTTP          ErrorCode = "http"
	CodeConnection    ErrorCode = "connection"
	CodeResponse      ErrorCode = "response"
)

// IntegrationError is safe to display: it never contains upstream bodies, keys, or submitted
// state. The only header value a message can quote is a numeric Retry-After count in seconds,
// which cannot carry a secret.
type IntegrationError struct {
	Code    ErrorCode
	Message string
	// Status is the HTTP status for CodeHTTP errors, else 0.
	Status int
}

// Error returns the safe message.
func (e *IntegrationError) Error() string { return e.Message }

func newError(code ErrorCode, message string) *IntegrationError {
	return &IntegrationError{Code: code, Message: message}
}

func errorf(code ErrorCode, format string, args ...any) *IntegrationError {
	return newError(code, fmt.Sprintf(format, args...))
}

// SafeError classifies any error into a message safe to display. backend names the key variable
// the 401 advice tells the user to check and selects the 402 wording; nil assumes the default
// TypeSafe key. An error that is already an *IntegrationError is returned as is.
func SafeError(err error, backend any) *IntegrationError {
	var own *IntegrationError
	if errors.As(err, &own) {
		return own
	}
	config := errorBackend(backend)
	var abort *typesafe.APIUserAbortError
	if errors.As(err, &abort) || errors.Is(err, context.Canceled) {
		return newError(CodeAborted, config.subject()+" request cancelled; an already submitted request may still be billed.")
	}
	var timeout *typesafe.APITimeoutError
	if errors.As(err, &timeout) || errors.Is(err, context.DeadlineExceeded) {
		return newError(CodeTimeout, config.subject()+" request timed out; it was not retried and may still be billed.")
	}
	var api *typesafe.APIError
	if errors.As(err, &api) {
		advice := httpAdvice(api.Status, api.Header, config)
		e := errorf(CodeHTTP, "%s returned HTTP %d. %s No automatic retry was made.", config.subject(), api.Status, advice)
		e.Status = api.Status
		return e
	}
	var conn *typesafe.APIConnectionError
	if errors.As(err, &conn) {
		return newError(CodeConnection, "Could not complete the "+config.subject()+" connection. No automatic retry was made.")
	}
	return newError(CodeResponse, config.subject()+" returned an unreadable or unexpected response.")
}

// errorConfig is what error wording needs from a backend.
type errorConfig struct {
	keyEnv     string
	openrouter bool
	local      bool
}

func (c errorConfig) subject() string {
	if c.local {
		return "The configured model"
	}
	return "TypeSafe"
}

func errorBackend(backend any) errorConfig {
	if backend == nil {
		return errorConfig{keyEnv: typesafeKeyEnv}
	}
	resolved, err := ResolveBackend(backend)
	if err != nil {
		return errorConfig{keyEnv: typesafeKeyEnv}
	}
	return errorConfig{keyEnv: resolved.KeyEnv, openrouter: resolved.Host == DecisionsBackends[BackendOpenRouter].Host, local: resolved.Local}
}

// retryAfterSeconds is a numeric Retry-After delay in seconds; dates, blanks, and anything else stay out of the message.
func retryAfterSeconds(h http.Header) (int, bool) {
	raw := strings.TrimSpace(h.Get("Retry-After"))
	if raw == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 || n > 1<<53-1 {
		return 0, false
	}
	return int(n), true
}

func httpAdvice(status int, h http.Header, c errorConfig) string {
	switch status {
	case 401:
		if c.local {
			return "Check the credentials of the configured model."
		}
		return "Check " + c.keyEnv + "."
	case 402:
		if c.openrouter {
			return "Insufficient credits. Add credits at https://openrouter.ai/credits."
		}
		return "Check your account balance."
	case 403:
		return "Check your account access and model permissions."
	case 429:
		advice := "Check your account quota and try again later."
		if secs, ok := retryAfterSeconds(h); ok {
			advice += " Retry after " + strconv.Itoa(secs) + " seconds."
		}
		return advice
	case 400, 422:
		return "Check the question format and model limits."
	}
	return "Try again later or check the service status."
}
