package typesafe

import (
	"encoding/json"
	"net/http"
	"testing"
)

func clientWith(t *testing.T, resp func() *http.Response) (*Client, *mockDoer) {
	m := always(resp)
	return newClient(t, m, func(c *Config) { c.Retry = RetryOverrides{MaxRetries: Ptr(0)} }), m
}

func TestAPIPromise_IsReturnedFromClientMethodsAndIsARealPromise(t *testing.T) {
	skipTwin(t, "JavaScript Promise subclass; Go returns (value, error) and the *Response types are TestAPIPromise_WithResponse*",
		"api-promise.test.ts | APIPromise is returned from client methods and is a real Promise")
}

func TestAPIPromise_AwaitsToTheParsedData(t *testing.T) {
	twin(t, "api-promise.test.ts | APIPromise awaits to the parsed data")
	c, _ := clientWith(t, func() *http.Response { return textResp(200, modelsBody, "content-type", "application/json") })
	got, err := c.Models().List(ctxBG(), nil)
	noErr(t, err)
	eq(t, got, modelCards)
}

func TestAPIPromise_WithResponseReturnsDataResponseAndRequestID(t *testing.T) {
	twin(t, "api-promise.test.ts | APIPromise withResponse() returns data, response, and requestId")
	c, _ := clientWith(t, func() *http.Response {
		return textResp(200, modelsBody, "content-type", "application/json", "x-typesafe-request-id", "req_abc")
	})
	res, err := c.Models().ListWithResponse(ctxBG(), nil)
	noErr(t, err)
	eq(t, res.Data, modelCards)
	eq(t, res.Status, 200)
	eq(t, res.Header.Get("x-typesafe-request-id"), "req_abc")
	eq(t, res.RequestID, "req_abc")
}

func TestAPIPromise_WithResponseHasUndefinedRequestIDWhenTheHeaderIsAbsent(t *testing.T) {
	twin(t, "api-promise.test.ts | APIPromise withResponse() has undefined requestId when the header is absent")
	c, _ := clientWith(t, func() *http.Response { return textResp(200, modelsBody, "content-type", "application/json") })
	res, err := c.Models().ListWithResponse(ctxBG(), nil)
	noErr(t, err)
	eq(t, res.RequestID, "")
}

func TestAPIPromise_AsResponseReturnsTheRawResponseWithAnUnconsumedBody(t *testing.T) {
	// Adapted: the raw response carries the buffered body bytes.
	twin(t, "api-promise.test.ts | APIPromise asResponse() returns the raw Response with an unconsumed body")
	c, _ := clientWith(t, func() *http.Response { return textResp(200, modelsBody, "content-type", "application/json") })
	raw, err := c.Models().ListRaw(ctxBG(), nil)
	noErr(t, err)
	eq(t, raw.Status, 200)
	var got map[string]any
	noErr(t, json.Unmarshal(raw.Body, &got))
	eq(t, len(got["models"].([]any)), 1)
}

func TestAPIPromise_ParsesTheBodyOnlyOnceAcrossMultipleConsumers(t *testing.T) {
	skipTwin(t, "promise sharing across several consumers; a Go call returns its single result, and TestAPIPromise_OneRequestPerCall shows one request per call",
		"api-promise.test.ts | APIPromise parses the body only once across multiple consumers")
}

func TestAPIPromise_OneRequestPerCall(t *testing.T) {
	c, m := clientWith(t, func() *http.Response { return textResp(200, modelsBody, "content-type", "application/json") })
	_, err := c.Models().ListWithResponse(ctxBG(), nil)
	noErr(t, err)
	eq(t, m.count(), 1)
}

func TestAPIPromise_RejectsWithAPIErrorOnEveryPath(t *testing.T) {
	twin(t, "api-promise.test.ts | APIPromise rejects with APIError on every path")
	c, _ := clientWith(t, func() *http.Response { return jsonResp(404, map[string]any{"message": "nope"}) })
	_, err := c.Models().List(ctxBG(), nil)
	mustAs[*NotFoundError](t, err)
	mustAs[*APIError](t, err)
	_, err = c.Models().ListWithResponse(ctxBG(), nil)
	mustAs[*NotFoundError](t, err)
	_, err = c.Models().ListRaw(ctxBG(), nil)
	mustAs[*NotFoundError](t, err)
}

func TestAPIPromise_MapTransformsTheDataWhileSharingTheResponseAndParsingOnce(t *testing.T) {
	twin(t, "api-promise.test.ts | APIPromise map() transforms the data while sharing the response and parsing once")
	c, m := clientWith(t, func() *http.Response {
		return textResp(200, modelsBody, "content-type", "application/json", "x-typesafe-request-id", "req_m")
	})
	res, err := c.Models().ListWithResponse(ctxBG(), nil)
	noErr(t, err)
	names := MapResponse(res, func(cards []ModelCard) []string {
		var out []string
		for _, c := range cards {
			out = append(out, c.Name)
		}
		return out
	})
	eq(t, names.Data, []string{"m"})
	eq(t, names.RequestID, "req_m")
	eq(t, names.Status, 200)
	eq(t, res.Data, modelCards)
	eq(t, m.count(), 1)
}

func TestAPIPromise_SupportsThenCatchFinallyChaining(t *testing.T) {
	skipTwin(t, "JavaScript thenable chaining; Go uses ordinary error returns",
		"api-promise.test.ts | APIPromise supports then/catch/finally chaining")
}

func TestAPIPromise_DoesNotIssueTheRequestUntilConstructedButDoesNotRequireAwaitingToSend(t *testing.T) {
	skipTwin(t, "a Go call is synchronous: the request is sent when the method is called and there is no unawaited promise",
		"api-promise.test.ts | APIPromise does not issue the request until constructed, but does not require awaiting to send")
}
