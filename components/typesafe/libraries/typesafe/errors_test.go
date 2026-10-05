package typesafe

import (
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// clientReturning returns a client whose every response is resp() with retries off.
func clientReturning(t *testing.T, resp func() *http.Response) *Client {
	t.Helper()
	return newClient(t, always(resp), func(c *Config) { c.Retry = RetryOverrides{MaxRetries: Ptr(0)} })
}

func TestAPIError_FromResponseMapsStatusesToTheirClasses(t *testing.T) {
	twin(t,
		"errors.test.ts | APIError.fromResponse maps 400 to [Function BadRequestError]",
		"errors.test.ts | APIError.fromResponse maps 401 to [Function AuthenticationError]",
		"errors.test.ts | APIError.fromResponse maps 403 to [Function PermissionDeniedError]",
		"errors.test.ts | APIError.fromResponse maps 404 to [Function NotFoundError]",
		"errors.test.ts | APIError.fromResponse maps 422 to [Function UnprocessableEntityError]",
		"errors.test.ts | APIError.fromResponse maps 429 to [Function RateLimitError]",
		"errors.test.ts | APIError.fromResponse maps 500 to [Function InternalServerError]",
		"errors.test.ts | APIError.fromResponse maps 503 to [Function InternalServerError]",
		"errors.test.ts | APIError.fromResponse maps 418 to [Function APIError]")
	cases := []struct {
		status int
		class  string
	}{{400, "BadRequestError"}, {401, "AuthenticationError"}, {403, "PermissionDeniedError"}, {404, "NotFoundError"}, {422, "UnprocessableEntityError"}, {429, "RateLimitError"}, {500, "InternalServerError"}, {503, "InternalServerError"}, {418, "APIError"}}
	for _, tc := range cases {
		err := NewAPIError(tc.status, nil, http.Header{})
		if got := reflect.TypeOf(err).Elem().Name(); got != tc.class {
			t.Fatalf("%d: got %s, want %s", tc.status, got, tc.class)
		}
		api := mustAs[*APIError](t, err)
		eq(t, api.Status, tc.status)
		mustAs[*TypeSafeError](t, err)
		if tc.class != "APIError" {
			// Exactly its own class among the subclasses.
			eq(t, errorClass(err), tc.class)
		}
		if err.Error() == "" {
			t.Fatal("empty message")
		}
	}
}

// errorClass names the subclass an error is (not the parents it unwraps to).
func errorClass(err error) string {
	switch err.(type) {
	case *BadRequestError:
		return "BadRequestError"
	case *AuthenticationError:
		return "AuthenticationError"
	case *PermissionDeniedError:
		return "PermissionDeniedError"
	case *NotFoundError:
		return "NotFoundError"
	case *UnprocessableEntityError:
		return "UnprocessableEntityError"
	case *RateLimitError:
		return "RateLimitError"
	case *InternalServerError:
		return "InternalServerError"
	case *APIError:
		return "APIError"
	}
	return "?"
}

func TestErrorMessagesAndBodies_UsesErrorMessageFromAJSONBodyAndExposesTheRequestID(t *testing.T) {
	twin(t, "errors.test.ts | error messages and bodies uses error.message from a JSON body and exposes the request id")
	c := clientReturning(t, func() *http.Response {
		return jsonResp(401, map[string]any{"error": map[string]any{"message": "invalid api key"}}, "x-typesafe-request-id", "req_123")
	})
	_, err := c.Models().List(ctxBG(), nil)
	mustAs[*AuthenticationError](t, err)
	api := mustAs[*APIError](t, err)
	eq(t, api.Error(), "401 invalid api key")
	eq(t, api.RequestID, "req_123")
	eq(t, api.Body, any(map[string]any{"error": map[string]any{"message": "invalid api key"}}))
	eq(t, api.Header.Get("x-typesafe-request-id"), "req_123")
}

func TestErrorMessagesAndBodies_ExtractsAMessage(t *testing.T) {
	twin(t,
		"errors.test.ts | error messages and bodies extracts a message from { error: 'plain string' }",
		"errors.test.ts | error messages and bodies extracts a message from { message: 'top-level message' }",
		"errors.test.ts | error messages and bodies extracts a message from { detail: 'fastapi style' }",
		"errors.test.ts | error messages and bodies extracts a message from { detail: { error_type: 'api_usage_error', message: 'Unknown model: x' } }",
		"errors.test.ts | error messages and bodies extracts a message from { detail: [ { type: 'list_type', loc: [ 'body', 'questions', 'q', 'score', 'criteria' ], msg: 'Input should be a valid list' }, { type: 'too_short', loc: [ 'body', 'questions' ], msg: 'Dictionary should have at least 1 item' } ] }")
	cases := []struct{ body, want string }{
		{`{"error":"plain string"}`, "plain string"},
		{`{"message":"top-level message"}`, "top-level message"},
		{`{"detail":"fastapi style"}`, "fastapi style"},
		{`{"detail":{"error_type":"api_usage_error","message":"Unknown model: x"}}`, "Unknown model: x"},
		{`{"detail":[{"type":"list_type","loc":["body","questions","q","score","criteria"],"msg":"Input should be a valid list"},{"type":"too_short","loc":["body","questions"],"msg":"Dictionary should have at least 1 item"}]}`,
			"questions.q.score.criteria: Input should be a valid list; questions: Dictionary should have at least 1 item"},
	}
	for _, tc := range cases {
		c := clientReturning(t, func() *http.Response { return jsonResp(400, decode(t, tc.body)) })
		_, err := c.Models().List(ctxBG(), nil)
		eq(t, err.Error(), "400 "+tc.want)
	}
}

func TestErrorMessagesAndBodies_FallsBackToTheRawBodyWhenNoMessageCanBeExtractedTruncated(t *testing.T) {
	twin(t, "errors.test.ts | error messages and bodies falls back to the raw body when no message can be extracted, truncated")
	c := clientReturning(t, func() *http.Response { return jsonResp(400, map[string]any{"code": 7}) })
	_, err := c.Models().List(ctxBG(), nil)
	eq(t, err.Error(), `400 {"code":7}`)
	c = clientReturning(t, func() *http.Response { return jsonResp(400, map[string]any{"blob": strings.Repeat("x", 500)}) })
	_, err = c.Models().List(ctxBG(), nil)
	eq(t, utf8.RuneCountInString(err.Error()), len("400 ")+200+1)
	if !strings.HasSuffix(err.Error(), "…") {
		t.Fatalf("not truncated with an ellipsis: %s", err)
	}
}

func TestErrorMessagesAndBodies_KeepsNonJSONBodiesAsText(t *testing.T) {
	twin(t, "errors.test.ts | error messages and bodies keeps non-JSON bodies as text")
	c := clientReturning(t, func() *http.Response { return textResp(502, "<h1>bad gateway</h1>", "content-type", "text/html") })
	_, err := c.Models().List(ctxBG(), nil)
	mustAs[*InternalServerError](t, err)
	eq(t, mustAs[*APIError](t, err).Body, any("<h1>bad gateway</h1>"))
	eq(t, err.Error(), "502 <h1>bad gateway</h1>")
}

func TestErrorMessagesAndBodies_HandlesEmptyBodies(t *testing.T) {
	twin(t, "errors.test.ts | error messages and bodies handles empty bodies")
	c := clientReturning(t, func() *http.Response { return textResp(429, "") })
	_, err := c.Models().List(ctxBG(), nil)
	mustAs[*RateLimitError](t, err)
	if mustAs[*APIError](t, err).Body != nil {
		t.Fatal("an empty body is nil")
	}
	eq(t, err.Error(), "429 status code (no body)")
}

func TestErrorMessagesAndBodies_ParsesJSONEvenWhenContentTypeIsMissing(t *testing.T) {
	twin(t, "errors.test.ts | error messages and bodies parses JSON even when content-type is missing")
	c := clientReturning(t, func() *http.Response { return textResp(400, `{"message":"no content type"}`) })
	_, err := c.Models().List(ctxBG(), nil)
	eq(t, mustAs[*APIError](t, err).Body, any(map[string]any{"message": "no content type"}))
}

// Go-specific cases beyond the upstream suite.

func TestAPIError_SubclassesUnwrapToTheirParents(t *testing.T) {
	var err error = NewAPIError(404, nil, http.Header{})
	mustAs[*NotFoundError](t, err)
	mustAs[*APIError](t, err)
	mustAs[*TypeSafeError](t, err)
	notAs[*BadRequestError](t, err)
	notAs[*APIConnectionError](t, err)
	conn := &APITimeoutError{APIConnectionError: &APIConnectionError{TypeSafeError: &TypeSafeError{Message: "Request timed out after 5ms."}}, TimeoutMs: 5}
	mustAs[*APITimeoutError](t, conn)
	mustAs[*APIConnectionError](t, conn)
	mustAs[*TypeSafeError](t, conn)
	notAs[*APIError](t, conn)
	abort := &APIUserAbortError{TypeSafeError: &TypeSafeError{Message: "Request was aborted.", Cause: errors.New("c")}}
	mustAs[*TypeSafeError](t, abort)
	notAs[*APIConnectionError](t, abort)
}

func TestAPIError_RawBodyKeepsKeyOrderInTheMessage(t *testing.T) {
	c := clientReturning(t, func() *http.Response {
		return textResp(400, `{ "b": 1,  "a": [1, 2] }`, "content-type", "application/json")
	})
	_, err := c.Models().List(ctxBG(), nil)
	eq(t, err.Error(), `400 {"b":1,"a":[1,2]}`)
}
