package typesafe

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestReleaseRegressions_ReplacesMixedCaseDefaultsAndProtectsEverySDKHeaderOnEveryAttempt(t *testing.T) {
	twin(t, "release-regressions.test.ts | release regressions replaces mixed-case defaults and protects every SDK header on every attempt")
	calls := 0
	m := newMock(func(*recordedRequest) (*http.Response, error) {
		calls++
		if calls == 1 {
			return jsonResp(503, map[string]any{}), nil
		}
		return jsonResp(200, map[string]any{}), nil
	})
	c := newClient(t, m, func(cfg *Config) {
		cfg.APIKey = "secret"
		cfg.DefaultHeaders = map[string]string{"X-Team": "default", "authorization": "bad", "content-type": "text/plain", "x-typesafe-retry-count": "99"}
		cfg.Retry = RetryOverrides{MaxRetries: Ptr(1), BackoffInitial: Ptr(time.Duration(0))}
	})
	_, err := c.SystemOne(ctxBG(), SystemOneRequest{State: Text("s"), Questions: Questions{Ask("q", Noul("?"))}}, &RequestOptions{Headers: map[string]string{
		"x-team": "call", "AUTHORIZATION": "bad-again", "ACCEPT": "text/plain", "USER-AGENT": "bad", "X-TYPESAFE-SDK": "bad",
		"X-TYPESAFE-RUNTIME": "bad", "CONTENT-TYPE": "text/html", "X-TYPESAFE-RETRY-COUNT": "88",
	}})
	noErr(t, err)
	eq(t, m.count(), 2)
	for i := 0; i < 2; i++ {
		hd := m.req(i).Header
		eq(t, hd.Get("authorization"), "Bearer secret")
		eq(t, hd.Get("x-team"), "call")
		eq(t, hd.Get("content-type"), "application/json")
		eq(t, hd.Get("accept"), "application/json")
		if !strings.HasPrefix(hd.Get("user-agent"), "typesafe-sdk") || !strings.HasPrefix(hd.Get("x-typesafe-sdk"), "typesafe-sdk") {
			t.Fatalf("identity headers %v", hd)
		}
		if strings.Contains(hd.Get("x-typesafe-runtime"), "bad") {
			t.Fatal("runtime header overridden")
		}
		want := ""
		if i == 1 {
			want = "1"
		}
		eq(t, hd.Get("x-typesafe-retry-count"), want)
		eq(t, len(hd.Values("Authorization")), 1)
		eq(t, len(hd.Values("X-Team")), 1)
	}
}

func TestReleaseRegressions_DoesNotSendACallerSuppliedContentTypeOrRetryCountOnGET(t *testing.T) {
	twin(t, "release-regressions.test.ts | release regressions does not send a caller-supplied content type or retry count on GET")
	m := modelsOK()
	c := newClient(t, m, func(cfg *Config) {
		cfg.DefaultHeaders = map[string]string{"content-type": "bad", "x-typesafe-retry-count": "99"}
	})
	_, err := c.Models().List(ctxBG(), nil)
	noErr(t, err)
	hd := m.req(0).Header
	if _, has := hd["Content-Type"]; has {
		t.Fatal("content type on GET")
	}
	if _, has := hd["X-Typesafe-Retry-Count"]; has {
		t.Fatal("retry count on the first attempt")
	}
}

func TestReleaseRegressions_PreservesOwnProtoQuestionsWithoutMutation(t *testing.T) {
	twin(t, "release-regressions.test.ts | release regressions preserves own __proto__ questions without mutation")
	qs := Questions{Ask("__proto__", Noul("?")), Ask("score", Score("?", "no", "yes"))}
	before, err := json.Marshal(qs)
	noErr(t, err)
	m := always(func() *http.Response { return jsonResp(200, map[string]any{}) })
	_, err = newClient(t, m).SystemOne(ctxBG(), SystemOneRequest{State: Text("s"), Questions: qs}, nil)
	noErr(t, err)
	var body struct {
		Questions map[string]json.RawMessage `json:"questions"`
	}
	noErr(t, json.Unmarshal(m.req(0).Raw, &body))
	jsonEq(t, body.Questions["__proto__"], `{"type":"noul","instructions":"?"}`)
	jsonEq(t, body.Questions["score"], `{"type":"score","instructions":"?","criteria":["no","yes"]}`)
	after, err := json.Marshal(qs)
	noErr(t, err)
	eq(t, string(after), string(before))
}

func TestReleaseRegressions_TimesOutAStalledBodyEvenWithACustomFetchIgnoringSignals(t *testing.T) {
	twin(t,
		"release-regressions.test.ts | release regressions times out a stalled 200 body even with a custom fetch ignoring signals",
		"release-regressions.test.ts | release regressions times out a stalled 503 body even with a custom fetch ignoring signals")
	for _, status := range []int{200, 503} {
		synctest.Test(t, func(t *testing.T) {
			var bodies []*blockingBody
			m := newMock(func(*recordedRequest) (*http.Response, error) {
				b := newBlockingBody()
				bodies = append(bodies, b)
				return respWithBody(status, b), nil
			})
			c := newClient(t, m, func(cfg *Config) {
				cfg.Timeout = 50 * time.Millisecond
				cfg.Retry = RetryOverrides{MaxRetries: Ptr(1), BackoffInitial: Ptr(time.Duration(0))}
			})
			_, err := c.Models().List(ctxBG(), nil)
			mustAs[*APITimeoutError](t, err)
			eq(t, m.count(), 2)
			eq(t, len(bodies), 2)
			for _, b := range bodies {
				eq(t, b.closeCount(), 1)
			}
		})
	}
}

func TestReleaseRegressions_WrapsBodyFailuresWithTheirCauseAndHonorsDisabledConnectionRetries(t *testing.T) {
	twin(t,
		"release-regressions.test.ts | release regressions wraps 200 body failures with their cause and honors disabled connection retries",
		"release-regressions.test.ts | release regressions wraps 503 body failures with their cause and honors disabled connection retries")
	for _, status := range []int{200, 503} {
		cause := errors.New("socket dropped")
		m := newMock(func(*recordedRequest) (*http.Response, error) { return respWithBody(status, errBody{cause}), nil })
		c := newClient(t, m, func(cfg *Config) { cfg.Retry = RetryOverrides{APIConnectionError: Ptr(false)} })
		_, err := c.Models().List(ctxBG(), nil)
		conn := mustAs[*APIConnectionError](t, err)
		if !errors.Is(conn, cause) {
			t.Fatalf("%d: cause lost: %v", status, err)
		}
		eq(t, m.count(), 1)
	}
}

func TestReleaseRegressions_CancelsABodyWhenTheSignalWasAbortedJustBeforeFetchReturnedHeaders(t *testing.T) {
	twin(t, "release-regressions.test.ts | release regressions cancels a body when the signal was aborted just before fetch returned headers")
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		body := newBlockingBody()
		m := newMock(func(*recordedRequest) (*http.Response, error) { cancel(); return respWithBody(200, body), nil })
		_, err := newClient(t, m).Models().List(ctx, nil)
		mustAs[*APIUserAbortError](t, err)
		eq(t, m.count(), 1)
		eq(t, body.closeCount(), 1)
	})
}

func TestReleaseRegressions_ReturnsANullBodyResponseWithoutTryingToReadAStream(t *testing.T) {
	twin(t, "release-regressions.test.ts | release regressions returns a null-body response without trying to read a stream")
	m := newMock(func(*recordedRequest) (*http.Response, error) {
		return &http.Response{StatusCode: 204, Header: http.Header{}, Body: http.NoBody}, nil
	})
	raw, err := newClient(t, m).Models().ListRaw(ctxBG(), nil)
	noErr(t, err)
	eq(t, raw.Status, 204)
	eq(t, len(raw.Body), 0)
}
