package websearch

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Review follow-ups to hostile_test.go: the decoder itself must refuse the GO-2026-5061 shape (so a
// downgrade of golang.org/x/image fails a test, not only govulncheck), the background prefetch
// goroutine needs the same panic boundary as the fan-outs, a recovered panic must not leave the
// progress lock held, and full decodes run one at a time.

// resizeImage's header/pixel check hides a vulnerable decoder from TestHostileWebPDoesNotPanic, so
// pin the patched decoder directly: x/image >= v0.43.0 refuses a VP8 frame that disagrees with its
// VP8X canvas; v0.41.0 returned an NYCbCrA whose alpha plane is smaller than its picture.
func TestWebPDecoderRefusesCanvasFrameMismatch(t *testing.T) {
	img, _, err := image.Decode(bytes.NewReader(hostileWebP(t, 2001, 1, 2001, 2001)))
	if err == nil {
		t.Fatalf("golang.org/x/image decoded a VP8X/VP8 size mismatch (%v): is it older than v0.43.0?", img.Bounds())
	}
}

type panickyHost struct{ fakeHost }

func (h *panickyHost) AppendEntry(string, any) { panic("store exploded") }

// web_search's background prefetch runs after the tool returned, outside the SDK's request
// recover, and does more than FetchAllContent's per-URL work (data: URI sanitizing, storing,
// reporting), so it needs its own boundary. A panic there is reported like a failed fetch.
func TestBackgroundFetchRecoversPanic(t *testing.T) {
	extractEnv(t, "")
	ClearResults()
	t.Cleanup(ClearResults)
	h := &panickyHost{}
	r, err := NewRuntime(h)
	noErr(t, err)
	r.SessionStarted(nil)
	t.Cleanup(func() { r.SessionShutdown(); r.WaitBackground() })
	t.Cleanup(SetPageFetch(func(context.Context, *url.URL, RequestInit) (*http.Response, error) {
		return nil, errors.New("plain failure")
	}))
	id := r.startBackgroundFetch([]string{"https://93.184.216.34/a"}, nil)
	r.WaitBackground()
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.messages) != 1 || h.messages[0].Type != "web-search-error" ||
		!strings.Contains(h.messages[0].Content, "Content fetch failed ["+id+"]") || !strings.Contains(h.messages[0].Content, "store exploded") {
		t.Fatalf("want one web-search-error for %s, got %+v", id, h.messages)
	}
}

// A panic recovered in one query's goroutine must not leave progressMu locked: the sibling
// queries would then block on it forever and web_search would hang instead of crashing.
func TestWebSearchProgressPanicDoesNotWedgeSiblings(t *testing.T) {
	r, _ := newRuntime(t, "")
	saved := providerTable["exa"]
	t.Cleanup(func() { providerTable["exa"] = saved })
	providerTable["exa"] = &providerDef{name: "exa", label: "Exa", available: func() bool { return true },
		search: func(_ context.Context, q string, _ SearchOptions) (*SearchResponse, error) {
			return &SearchResponse{Answer: "answer for " + q}, nil
		}}
	var once sync.Once
	update := func(o ToolOutput) {
		if strings.HasPrefix(o.Content[0].Text, "Searching") {
			once.Do(func() { panic("progress exploded") })
		}
	}
	done := make(chan ToolOutput, 1)
	go func() {
		out, _ := mustTool(t, r, "web_search").Execute(bg(), map[string]any{"queries": []any{"a", "b", "c"}, "provider": "exa"}, update)
		done <- out
	}()
	select {
	case out := <-done:
		if !strings.Contains(out.Content[0].Text, "progress exploded") {
			t.Fatalf("the panicking query must come back as its error: %q", out.Content[0].Text)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("web_search hung after a recovered panic (progressMu left locked)")
	}
}

// slowFormat is a test image format whose decoder blocks until released and counts how many
// decodes run at once.
var slowFormat struct {
	once           sync.Once
	inFlight, peak atomic.Int32
	release        chan struct{}
}

func registerSlowFormat() {
	slowFormat.once.Do(func() {
		image.RegisterFormat("slowtest", "SLOWTEST", func(r io.Reader) (image.Image, error) {
			n := slowFormat.inFlight.Add(1)
			defer slowFormat.inFlight.Add(-1)
			for {
				p := slowFormat.peak.Load()
				if n <= p || slowFormat.peak.CompareAndSwap(p, n) {
					break
				}
			}
			<-slowFormat.release
			return image.NewGray(image.Rect(0, 0, 2001, 1)), nil
		}, func(io.Reader) (image.Config, error) {
			return image.Config{ColorModel: color.GrayModel, Width: 2001, Height: 1}, nil
		})
	})
}

// Under the 64 MP cap one image can still cost about 1 GiB to decode (a 130-byte progressive JPEG
// of 8192x8192 allocates 960 MiB of coefficients and planes before failing), and fetch_content
// decodes on up to three goroutines at once, so full decodes run one at a time.
func TestFullDecodesRunOneAtATime(t *testing.T) {
	registerSlowFormat()
	slowFormat.release = make(chan struct{})
	slowFormat.peak.Store(0)
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = resizeImage([]byte("SLOWTEST"), "image/png", 2000, 2000)
		}()
	}
	time.Sleep(200 * time.Millisecond)
	close(slowFormat.release)
	wg.Wait()
	if p := slowFormat.peak.Load(); p != 1 {
		t.Fatalf("%d full decodes ran at once, want 1", p)
	}
}
