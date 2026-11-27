package websearch

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

// hostFor must be per call, not "the most recent handler": two tool calls of one parallel batch
// run at the same time, and the SDK's tool-start order (extension-api D187) is per request. A
// host call issued on a sibling's Context waits for this request to open a dialog it never
// opens, so the call never returns and the model is stuck (gh-71).
func TestHostForIsPerCallNotMostRecent(t *testing.T) {
	retained := &sdkHost{}
	r := &Runtime{host: retained}

	// Detached work (a background fetch, which outlives its request) keeps the retained host.
	if got := r.hostFor(context.Background()); got != Host(retained) {
		t.Fatalf("a context without an SDK Context must keep the retained host, got %T", got)
	}

	a := r.hostFor(withSDKContext(context.Background(), sdk.Context{}))
	b := r.hostFor(withSDKContext(context.Background(), sdk.Context{}))
	if a == Host(retained) {
		t.Fatal("a host call inside a handler must not ride the retained (most recent) Context")
	}
	if a == b {
		t.Fatal("two calls of one batch must not share a Host")
	}
}
