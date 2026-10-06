package websearch

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

// A hostile page must not be able to take the process down. websearch is fused in-process in the
// pig-with-batteries Binary, and the SDK recovers panics only on the request goroutine, so each
// fan-out goroutine needs its own panic boundary and the image decoders need a patched x/image.

func riffChunk(id string, body []byte) []byte {
	out := append([]byte(id), 0, 0, 0, 0)
	binary.LittleEndian.PutUint32(out[4:], uint32(len(body)))
	out = append(out, body...)
	if len(body)%2 == 1 {
		out = append(out, 0)
	}
	return out
}

// hostileWebP is the shape behind GO-2026-5061: a VP8X canvas of canvasW×canvasH with an alpha
// plane of just that size, around a VP8 frame that really is frameW×frameH. Old x/image decodes it
// into an NYCbCrA whose alpha plane is smaller than the picture, and the first scaler that reads
// past the alpha plane panics. A canvas of 2001 pixels wide skips resizeImage's "already small"
// early return, so the bytes reach image.Decode. It is built from x/image's small lossy test image (header patched, partitions zero-padded), so no binary fixture is checked in.
func hostileWebP(t *testing.T, canvasW, canvasH, frameW, frameH int) []byte {
	t.Helper()
	vp8 := lossySample(t)
	binary.LittleEndian.PutUint16(vp8[6:], uint16(frameW))
	binary.LittleEndian.PutUint16(vp8[8:], uint16(frameH))
	const firstPartition = 20000
	tag := uint32(vp8[0]) | uint32(vp8[1])<<8 | uint32(vp8[2])<<16
	tag = tag&0x1f | firstPartition<<5
	vp8[0], vp8[1], vp8[2] = byte(tag), byte(tag>>8), byte(tag>>16)
	vp8 = append(vp8[:10], make([]byte, firstPartition+200000)...)
	vp8x := make([]byte, 10)
	vp8x[0] = 1 << 4 // alpha
	for i, v := range []int{canvasW - 1, canvasH - 1} {
		vp8x[4+3*i], vp8x[5+3*i], vp8x[6+3*i] = byte(v), byte(v>>8), byte(v>>16)
	}
	alph := append([]byte{0}, make([]byte, canvasW*canvasH)...) // uncompressed alpha for the canvas
	body := []byte("WEBP")
	body = append(body, riffChunk("VP8X", vp8x)...)
	body = append(body, riffChunk("ALPH", alph)...)
	body = append(body, riffChunk("VP8 ", vp8)...)
	out := append([]byte("RIFF"), 0, 0, 0, 0)
	binary.LittleEndian.PutUint32(out[4:], uint32(len(body)))
	return append(out, body...)
}

// lossySample returns the VP8 chunk payload of testdata/lossy-sample.webp, a 2450-byte lossy WebP
// copied from golang.org/x/image's test data (BSD-3-Clause, Copyright The Go Authors).
func lossySample(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "lossy-sample.webp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 20 || string(raw[12:16]) != "VP8 " {
		t.Fatalf("unexpected sample layout: %q", raw[:16])
	}
	n := int(binary.LittleEndian.Uint32(raw[16:20]))
	return append([]byte(nil), raw[20:20+n]...)
}

func TestHostileWebPDoesNotPanic(t *testing.T) {
	extractEnv(t, "")
	data := hostileWebP(t, 2001, 1, 2001, 2001)
	t.Cleanup(SetPageFetch(func(ctx context.Context, u *url.URL, _ RequestInit) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200, Status: "200 OK", Header: hdr("Content-Type", "image/webp"),
			Body: readCloser(bytes.NewReader(data)), Request: (&http.Request{URL: u}),
		}, nil
	}))
	urls := []string{"https://93.184.216.34/a.webp", "https://93.184.216.35/b.webp"}
	// Decoding runs on a fan-out goroutine: an unrecovered panic there ends the test binary.
	res := FetchAllContent(bg(), urls, ExtractOptions{Lookup: pubLookup})
	if len(res) != len(urls) {
		t.Fatalf("results %d", len(res))
	}
	for i, r := range res {
		if r.Error == nil && r.Thumbnail == nil {
			t.Fatalf("%d: neither a result nor an error: %+v", i, r)
		}
	}
	// The image itself is also refused or resized, never a panic.
	if img, err := resizeImage(data, "image/webp", 2000, 2000); err == nil && img != nil && (img.width > 2000 || img.height > 2000) {
		t.Fatalf("not resized: %dx%d", img.width, img.height)
	}
}

func TestResizeImageRefusesOverCapBeforeDecode(t *testing.T) {
	// A bare VP8X header declaring 20000×20000 (400 MP): refused from the declared size alone.
	vp8x := make([]byte, 10)
	vp8x[4], vp8x[5] = byte(19999&0xff), byte(19999>>8)
	vp8x[7], vp8x[8] = byte(19999&0xff), byte(19999>>8)
	body := append([]byte("WEBP"), riffChunk("VP8X", vp8x)...)
	data := append([]byte("RIFF"), 0, 0, 0, 0)
	binary.LittleEndian.PutUint32(data[4:], uint32(len(body)))
	data = append(data, body...)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	img, err := resizeImage(data, "image/webp", 2000, 2000)
	runtime.ReadMemStats(&after)
	if img != nil || err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("want a too-large error, got %v %v", img, err)
	}
	if grown := after.TotalAlloc - before.TotalAlloc; grown > 8<<20 {
		t.Fatalf("a refused image allocated %d MiB", grown>>20)
	}
}

func TestFetchAllContentRecoversPanickingExtract(t *testing.T) {
	extractEnv(t, "")
	var calls atomic.Int32
	t.Cleanup(SetPageFetch(func(ctx context.Context, u *url.URL, _ RequestInit) (*http.Response, error) {
		calls.Add(1)
		if strings.HasSuffix(u.Path, "/boom") {
			panic("hostile page")
		}
		return nil, errors.New("plain failure")
	}))
	urls := []string{"https://93.184.216.34/a", "https://93.184.216.35/boom", "https://93.184.216.36/c"}
	res := FetchAllContent(bg(), urls, ExtractOptions{Lookup: pubLookup})
	if len(res) != 3 {
		t.Fatalf("results %d", len(res))
	}
	if res[1].URL != urls[1] || res[1].Error == nil || !strings.Contains(*res[1].Error, "hostile page") {
		t.Fatalf("panic must come back as a per-URL error: %+v", res[1])
	}
	for _, i := range []int{0, 2} {
		if res[i].URL != urls[i] || res[i].Error == nil || strings.Contains(*res[i].Error, "hostile page") {
			t.Fatalf("neighbour %d disturbed: %+v", i, res[i])
		}
	}
	if calls.Load() < 3 {
		t.Fatalf("siblings were not fetched: %d", calls.Load())
	}
}

func TestRunSearchQueriesRecoversPanic(t *testing.T) {
	type out struct {
		v   int
		err error
	}
	got := runSearchQueries([]string{"a", "b", "c"}, func(q string, i int) out {
		if q == "b" {
			panic("provider exploded")
		}
		return out{v: i + 1}
	}, func(_ string, err error) out { return out{err: err} })
	if got[0].v != 1 || got[2].v != 3 || got[1].err == nil || !strings.Contains(got[1].err.Error(), "provider exploded") {
		t.Fatalf("%+v", got)
	}
}

func TestSearchWithProvidersRecoversProviderPanic(t *testing.T) {
	extractEnv(t, "")
	saved := map[string]*providerDef{}
	for _, n := range []string{"exa", "perplexity"} {
		saved[n] = providerTable[n]
	}
	t.Cleanup(func() {
		for n, d := range saved {
			providerTable[n] = d
		}
	})
	providerTable["exa"] = &providerDef{name: "exa", label: "Exa", available: func() bool { return true },
		search: func(context.Context, string, SearchOptions) (*SearchResponse, error) { panic("provider exploded") }}
	providerTable["perplexity"] = &providerDef{name: "perplexity", label: "Perplexity", available: func() bool { return true },
		search: func(context.Context, string, SearchOptions) (*SearchResponse, error) {
			return &SearchResponse{Answer: "fine"}, nil
		}}
	resp, err := searchWithProviders(bg(), "q", FullSearchOptions{}, []string{"exa", "perplexity"})
	if err != nil {
		t.Fatalf("one surviving provider must still answer: %v", err)
	}
	if len(resp.ProviderResponses) != 1 || len(resp.ProviderErrors) != 1 || !strings.Contains(resp.ProviderErrors[0].Error, "provider exploded") {
		t.Fatalf("%+v", resp)
	}
}

func readCloser(r io.Reader) io.ReadCloser { return io.NopCloser(r) }

// The decode cap is declared once in each module (websearch and AHP cannot import each other);
// this keeps the two values from drifting apart.
func TestDecodePixelCapMatchesAHP(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "ahp", "extensions", "ahp", "internal", "pi", "imageinput.go"))
	if err != nil {
		t.Skipf("AHP source not alongside: %v", err)
	}
	want := fmt.Sprintf("maxDecodePixels = %d", maxDecodePixels)
	if !regexp.MustCompile(`maxDecodePixels\s*=\s*1 << 26\b`).Match(src) || maxDecodePixels != 1<<26 {
		t.Fatalf("AHP's maxDecodePixels must equal websearch's (%s)", want)
	}
}
