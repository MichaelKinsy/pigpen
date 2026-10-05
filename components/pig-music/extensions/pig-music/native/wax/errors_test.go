package wax

import (
	"context"
	"errors"
	"fmt"
	"github.com/MichaelKinsy/pigpen/pig-music/native"
	"strings"
	"testing"

	"github.com/colespringer/waxtap/v3"
)

func TestExplainMapsWaxTapErrors(t *testing.T) {
	cases := []struct {
		err  error
		kind native.Kind
		want string // a phrase the message must contain
	}{
		{waxtap.ErrNeedsPOToken, native.KindNeedsToken, "proof-of-origin"},
		{waxtap.ErrCipherSolve, native.KindBroken, "needs an update"},
		{waxtap.ErrExtractionFailed, native.KindBroken, "needs an update"},
		{waxtap.ErrURLExpired, native.KindExpired, "expired"},
		{waxtap.ErrAgeRestricted, native.KindLogin, "sign"},
		{waxtap.ErrLoginRequired, native.KindLogin, "sign"},
		{waxtap.ErrVideoUnavailable, native.KindUnavailable, "unavailable"},
		{waxtap.ErrVideoRestricted, native.KindUnavailable, "private"},
		{waxtap.ErrMembersOnly, native.KindUnavailable, "members"},
		{waxtap.ErrGeoBlocked, native.KindUnavailable, "region"},
		{waxtap.ErrLiveContent, native.KindLive, "live"},
		{waxtap.ErrLiveNotStarted, native.KindLive, "live"},
		{waxtap.ErrNoAudioFormats, native.KindUnavailable, "audio"},
		{waxtap.ErrRateLimited, native.KindRateLimited, "too many"},
		{context.DeadlineExceeded, native.KindTimeout, "did not answer"},
		{errors.New("dial tcp 203.0.113.9:443: connection refused"), native.KindNetwork, "network"},
	}
	for _, c := range cases {
		wrapped := fmt.Errorf("resolve abc: %w", c.err)
		got := native.Explain(wrapped)
		var e *native.Error
		if !errors.As(got, &e) {
			t.Fatalf("%v: not an *native.Error: %T", c.err, got)
		}
		if e.Kind != c.kind {
			t.Errorf("%v: kind %v, want %v", c.err, e.Kind, c.kind)
		}
		if !strings.Contains(strings.ToLower(e.Error()), c.want) {
			t.Errorf("%v: message %q lacks %q", c.err, e.Error(), c.want)
		}
		if !errors.Is(got, c.err) {
			t.Errorf("%v: the cause is not reachable", c.err)
		}
		if strings.Contains(e.Error(), "203.0.113.9") {
			t.Errorf("%v: address leaked: %s", c.err, e.Error())
		}
	}
}

func TestExplainKeepsContextCancellation(t *testing.T) {
	if got := native.Explain(context.Canceled); !errors.Is(got, context.Canceled) {
		t.Fatalf("cancellation must stay itself, got %v", got)
	}
	if native.Explain(nil) != nil {
		t.Fatal("nil must stay nil")
	}
}

func TestExplainIsIdempotent(t *testing.T) {
	once := native.Explain(waxtap.ErrNeedsPOToken)
	if twice := native.Explain(once); twice != once {
		t.Fatalf("explained twice: %v", twice)
	}
}

func TestRetryable(t *testing.T) {
	if !native.Retryable(native.Explain(waxtap.ErrURLExpired)) {
		t.Error("an expired URL is worth re-resolving")
	}
	if native.Retryable(native.Explain(waxtap.ErrAgeRestricted)) || native.Retryable(native.Explain(waxtap.ErrNeedsPOToken)) {
		t.Error("login and token failures do not heal by retrying")
	}
}
