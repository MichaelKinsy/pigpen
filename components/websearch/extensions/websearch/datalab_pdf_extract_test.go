package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Twins of datalab-pdf-extract.test.mjs.

const datalabTestKey = "synthetic-datalab-key"

// datalabRoute is route() of the upstream file: the happy path of every endpoint.
func datalabRoute(c netCall) netReply {
	switch {
	case strings.HasSuffix(c.URL, "/files/upload"):
		return jsonReply(map[string]any{"file_id": 7, "upload_url": "https://storage.test/put/abc", "reference": "datalab://file-7", "expires_in": 3600})
	case strings.Contains(c.URL, "/put/"):
		return reply(200, "")
	case strings.HasSuffix(c.URL, "/files/7/confirm"):
		return jsonReply(map[string]any{"file_id": 7, "reference": "datalab://file-7", "message": "confirmed"})
	case strings.HasSuffix(c.URL, "/files/7"):
		return reply(200, "")
	case strings.HasSuffix(c.URL, "/convert"):
		return jsonReply(map[string]any{"status": "processing", "request_check_url": "/check/abc123"})
	case strings.HasSuffix(c.URL, "/check/abc123"):
		return jsonReply(map[string]any{"status": "complete", "success": true, "markdown": "<!-- Page 1 -->\n# Sample Document\n\nFirst page.\n\n<!-- Page 2 -->\n\nSecond page.", "page_count": 2, "parse_quality_score": 4.5, "cost_breakdown": map[string]any{"total_cost_cents": 1}})
	}
	return netReply{Status: 418, Body: `{"detail":"unexpected fetch: ` + c.URL + `"}`, Header: hdr("content-type", "application/json")}
}

// datalabEnv isolates the environment and sets the key; it returns the fake net answering fn
// (nil means the happy path) wrapped so that a test can override single endpoints.
func datalabEnv(t *testing.T, fn func(netCall) *netReply) *fakeNet {
	t.Helper()
	isolate(t)
	t.Setenv("DATALAB_API_KEY", datalabTestKey)
	unsetenv(t, "DATALAB_MODE", "DATALAB_PROCESSING_LOCATION", "DATALAB_API_BASE")
	return useNet(t, func(c netCall) netReply {
		if fn != nil {
			if r := fn(c); r != nil {
				return *r
			}
		}
		return datalabRoute(c)
	})
}

func rp(r netReply) *netReply { return &r }

func (f *fakeNet) snapshot() []netCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]netCall(nil), f.calls...)
}

func (f *fakeNet) find(match func(netCall) bool) (netCall, bool) {
	for _, c := range f.snapshot() {
		if match(c) {
			return c, true
		}
	}
	return netCall{}, false
}

func (f *fakeNet) countURL(suffix string) int {
	n := 0
	for _, c := range f.snapshot() {
		if strings.HasSuffix(c.URL, suffix) {
			n++
		}
	}
	return n
}

// waitDeleted waits for the best-effort cleanup request, which runs after the extraction returns.
func waitDeleted(t *testing.T, f *fakeNet) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := f.find(func(c netCall) bool { return c.Method == "DELETE" }); ok {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no cleanup request")
}

func formFields(t *testing.T, c netCall) map[string][]string {
	t.Helper()
	mt, params, err := mime.ParseMediaType(c.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/form-data" {
		t.Fatalf("content type %q: %v", c.Header.Get("Content-Type"), err)
	}
	out := map[string][]string{}
	mr := multipart.NewReader(strings.NewReader(c.Body), params["boundary"])
	for {
		p, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return out
		}
		noErr(t, err)
		b, _ := io.ReadAll(p)
		out[p.FormName()] = append(out[p.FormName()], string(b))
	}
}

func datalabOpts(title string, maxPages int) DatalabPDFOptions {
	return DatalabPDFOptions{MaxPages: maxPages, Title: title}
}

func TestUpstream_datalab_pdf_extract(t *testing.T) {
	const F = "datalab-pdf-extract"
	input := []byte{1, 2, 3, 4}

	tw(t, F, "Datalab conversion uploads the PDF, converts with a datalab reference, and polls to completion", func(t *testing.T) {
		n := datalabEnv(t, nil)
		result, err := ExtractPDFViaDatalab(bg(), input, datalabOpts("Sample Document", 5))
		noErr(t, err)
		waitDeleted(t, n)
		for suffix, want := range map[string]int{"/files/upload": 1, "/files/7/confirm": 1, "/convert": 1, "/check/abc123": 1} {
			if got := n.countURL(suffix); got != want {
				t.Fatalf("%s called %d times, want %d", suffix, got, want)
			}
		}
		puts := 0
		for _, c := range n.snapshot() {
			if strings.Contains(c.URL, "/put/") {
				puts++
			}
		}
		if puts != 1 {
			t.Fatalf("PUT calls = %d", puts)
		}
		upload, _ := n.find(func(c netCall) bool { return strings.HasSuffix(c.URL, "/files/upload") })
		if upload.Header.Get("x-api-key") != datalabTestKey || upload.Header.Get("content-type") != "application/json" {
			t.Fatalf("upload headers %v", upload.Header)
		}
		var body map[string]any
		noErr(t, json.Unmarshal([]byte(upload.Body), &body))
		if body["filename"] != "sample-document.pdf" || body["content_type"] != "application/pdf" || body["processing_location"] != "us" {
			t.Fatalf("upload body %v", body)
		}
		put, _ := n.find(func(c netCall) bool { return strings.Contains(c.URL, "/put/") })
		if put.Method != "PUT" || put.Header.Get("content-type") != "application/pdf" || put.Body != string(input) {
			t.Fatalf("put %v %v %q", put.Method, put.Header, put.Body)
		}
		convert, _ := n.find(func(c netCall) bool { return strings.HasSuffix(c.URL, "/convert") })
		if convert.Method != "POST" || convert.Header.Get("x-api-key") != datalabTestKey {
			t.Fatalf("convert %v %v", convert.Method, convert.Header)
		}
		form := formFields(t, convert)
		for k, want := range map[string]string{"file_url": "datalab://file-7", "output_format": "markdown", "paginate": "true", "max_pages": "5", "mode": "balanced"} {
			if len(form[k]) != 1 || form[k][0] != want {
				t.Fatalf("form %s = %v, want %s", k, form[k], want)
			}
		}
		if _, ok := form["processing_location"]; ok {
			t.Fatal("convert must not carry processing_location")
		}
		if cleanup, ok := n.find(func(c netCall) bool { return strings.HasSuffix(c.URL, "/files/7") && c.Method == "DELETE" }); !ok || cleanup.Header.Get("x-api-key") != datalabTestKey {
			t.Fatal("expected best-effort file cleanup after conversion")
		}
		if !strings.HasPrefix(result.Markdown, "<!-- Page 1 -->") || result.Pages != 2 || result.ParseQualityScore == nil || *result.ParseQualityScore != 4.5 {
			t.Fatalf("result %+v", result)
		}
	})

	tw(t, F, "Datalab conversion still succeeds when cleanup fails", func(t *testing.T) {
		n := datalabEnv(t, func(c netCall) *netReply {
			if strings.HasSuffix(c.URL, "/files/7") && c.Method == "DELETE" {
				return rp(netReply{Err: errors.New("cleanup transport failure")})
			}
			return nil
		})
		result, err := ExtractPDFViaDatalab(bg(), []byte{0}, datalabOpts("Doc", 1))
		noErr(t, err)
		waitDeleted(t, n)
		if !strings.Contains(result.Markdown, "Sample Document") {
			t.Fatal(result.Markdown)
		}
	})

	tw(t, F, "Datalab conversion honors processing location and mode selection", func(t *testing.T) {
		var location, mode string
		n := datalabEnv(t, func(c netCall) *netReply {
			switch {
			case strings.HasSuffix(c.URL, "/files/upload"):
				var b map[string]any
				_ = json.Unmarshal([]byte(c.Body), &b)
				location, _ = b["processing_location"].(string)
				return rp(jsonReply(map[string]any{"file_id": 7, "upload_url": "https://storage.test/put/abc", "reference": "datalab://file-7"}))
			case strings.HasSuffix(c.URL, "/convert"):
				mode = formFields(t, c)["mode"][0]
				return rp(jsonReply(map[string]any{"status": "complete", "success": true, "markdown": "<!-- Page 1 -->\nDone", "page_count": 1}))
			}
			return nil
		})
		t.Setenv("DATALAB_PROCESSING_LOCATION", "eu")
		t.Setenv("DATALAB_MODE", "accurate")
		_, err := ExtractPDFViaDatalab(bg(), []byte{0}, datalabOpts("Doc", 2))
		noErr(t, err)
		waitDeleted(t, n)
		if location != "eu" || mode != "accurate" {
			t.Fatalf("location %q mode %q", location, mode)
		}
	})

	tw(t, F, "Datalab conversion rejects a missing file_id before uploading", func(t *testing.T) {
		n := datalabEnv(t, func(c netCall) *netReply {
			if strings.HasSuffix(c.URL, "/files/upload") {
				return rp(jsonReply(map[string]any{"upload_url": "https://storage.test/put/abc", "reference": "datalab://file-7"}))
			}
			return nil
		})
		_, err := ExtractPDFViaDatalab(bg(), []byte{0}, datalabOpts("Doc", 1))
		wantErr(t, err, `missing file_id`)
		for _, u := range n.urls() {
			if strings.Contains(u, "/put/") {
				t.Fatal("must not upload without a file ID for confirmation and cleanup")
			}
		}
	})

	tw(t, F, "Datalab conversion rejects valid JSON that is not an object", func(t *testing.T) {
		datalabEnv(t, func(c netCall) *netReply {
			if strings.HasSuffix(c.URL, "/files/upload") {
				return rp(netReply{Status: 200, Body: "null", Header: hdr("content-type", "application/json")})
			}
			return nil
		})
		_, err := ExtractPDFViaDatalab(bg(), []byte{0}, datalabOpts("Doc", 1))
		wantErr(t, err, `invalid JSON object`)
	})

	tw(t, F, "Datalab conversion returns immediately when the convert response is complete", func(t *testing.T) {
		n := datalabEnv(t, func(c netCall) *netReply {
			if strings.HasSuffix(c.URL, "/convert") {
				return rp(jsonReply(map[string]any{"status": "complete", "success": true, "markdown": "<!-- Page 1 -->\nFast", "page_count": 1}))
			}
			return nil
		})
		result, err := ExtractPDFViaDatalab(bg(), []byte{0}, datalabOpts("Doc", 1))
		noErr(t, err)
		waitDeleted(t, n)
		if n.countURL("/convert") != 1 || !strings.Contains(result.Markdown, "Fast") {
			t.Fatalf("%d convert calls, %q", n.countURL("/convert"), result.Markdown)
		}
	})

	tw(t, F, "Datalab conversion rejects HTTP errors with the status and redacted body", func(t *testing.T) {
		datalabEnv(t, func(c netCall) *netReply {
			if strings.HasSuffix(c.URL, "/files/upload") {
				return rp(netReply{Status: 401, Body: `{"detail":"invalid api key"}`, Header: hdr("content-type", "application/json")})
			}
			return nil
		})
		_, err := ExtractPDFViaDatalab(bg(), []byte{0}, datalabOpts("Doc", 1))
		wantErr(t, err, `HTTP 401 Unauthorized: .*invalid api key`)
		// The redaction itself: a body that echoes the key.
		datalabEnv(t, func(c netCall) *netReply {
			if strings.HasSuffix(c.URL, "/files/upload") {
				return rp(netReply{Status: 403, Body: "bad key " + datalabTestKey})
			}
			return nil
		})
		_, err = ExtractPDFViaDatalab(bg(), []byte{0}, datalabOpts("Doc", 1))
		if err == nil || strings.Contains(err.Error(), datalabTestKey) {
			t.Fatalf("the key leaked: %v", err)
		}
	})

	tw(t, F, "Datalab conversion redacts API keys from transport errors", func(t *testing.T) {
		datalabEnv(t, func(netCall) *netReply { return rp(netReply{Err: errors.New("network failure for " + datalabTestKey)}) })
		_, err := ExtractPDFViaDatalab(bg(), []byte{0}, datalabOpts("Doc", 1))
		if err == nil || strings.Contains(err.Error(), datalabTestKey) || !strings.Contains(err.Error(), "network failure") {
			t.Fatalf("error %v", err)
		}
	})

	tw(t, F, "Datalab conversion rejects a streamed response above its byte limit", func(t *testing.T) {
		datalabEnv(t, func(c netCall) *netReply {
			if strings.HasSuffix(c.URL, "/files/upload") {
				return rp(netReply{Status: 200, Body: strings.Repeat("a", 4*1024*1024+1)})
			}
			return nil
		})
		_, err := ExtractPDFViaDatalab(bg(), []byte{0}, datalabOpts("Doc", 1))
		wantErr(t, err, `response too large`)
	})

	tw(t, F, "Datalab conversion rejects failed conversion status", func(t *testing.T) {
		n := datalabEnv(t, func(c netCall) *netReply {
			switch {
			case strings.HasSuffix(c.URL, "/convert"):
				return rp(jsonReply(map[string]any{"status": "processing", "request_check_url": "/check/abc"}))
			case strings.HasSuffix(c.URL, "/check/abc"):
				return rp(jsonReply(map[string]any{"status": "failed", "success": false, "error": "document is corrupt"}))
			}
			return nil
		})
		_, err := ExtractPDFViaDatalab(bg(), []byte{0}, datalabOpts("Doc", 1))
		wantErr(t, err, `document is corrupt`)
		waitDeleted(t, n)
	})

	tw(t, F, "Datalab conversion rejects empty markdown", func(t *testing.T) {
		n := datalabEnv(t, func(c netCall) *netReply {
			if strings.HasSuffix(c.URL, "/convert") {
				return rp(jsonReply(map[string]any{"status": "complete", "success": true, "markdown": "   ", "page_count": 1}))
			}
			return nil
		})
		_, err := ExtractPDFViaDatalab(bg(), []byte{0}, datalabOpts("Doc", 1))
		wantErr(t, err, `empty markdown`)
		waitDeleted(t, n)
	})

	tw(t, F, "Datalab conversion times out while polling without waiting a full poll interval", func(t *testing.T) {
		n := datalabEnv(t, func(c netCall) *netReply {
			switch {
			case strings.HasSuffix(c.URL, "/convert"):
				return rp(jsonReply(map[string]any{"status": "processing", "request_check_url": "/check/abc"}))
			case strings.HasSuffix(c.URL, "/check/abc"):
				return rp(jsonReply(map[string]any{"status": "processing"}))
			}
			return nil
		})
		started := time.Now()
		o := datalabOpts("Doc", 1)
		o.Timeout = 60 * time.Millisecond
		_, err := ExtractPDFViaDatalab(bg(), []byte{0}, o)
		wantErr(t, err, `timed out`)
		if time.Since(started) >= 500*time.Millisecond {
			t.Fatal("polling must not delay a 60ms timeout by its 1.5s interval")
		}
		waitDeleted(t, n)
	})

	tw(t, F, "Datalab cancellation returns without awaiting best-effort cleanup", func(t *testing.T) {
		ctx, cancel := context.WithCancel(bg())
		defer cancel()
		submitted, cleanupStarted, finish := make(chan struct{}), make(chan struct{}), make(chan struct{})
		datalabEnv(t, func(c netCall) *netReply {
			if strings.HasSuffix(c.URL, "/convert") {
				close(submitted)
			}
			if strings.HasSuffix(c.URL, "/files/7") && c.Method == "DELETE" {
				close(cleanupStarted)
				<-finish
				return rp(reply(200, ""))
			}
			return nil
		})
		done := make(chan error, 1)
		go func() {
			_, err := ExtractPDFViaDatalab(ctx, []byte{0}, datalabOpts("Doc", 1))
			done <- err
		}()
		await := func(ch chan struct{}, what string) {
			select {
			case <-ch:
			case <-time.After(5 * time.Second):
				t.Fatal("timed out waiting for " + what)
			}
		}
		await(submitted, "the convert request")
		cancel()
		await(cleanupStarted, "the cleanup request")
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("error %v", err)
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("the extraction waited for the cleanup")
		}
		close(finish)
	})

	tw(t, F, "Datalab conversion rejects a cross-origin polling URL", func(t *testing.T) {
		n := datalabEnv(t, func(c netCall) *netReply {
			if strings.HasSuffix(c.URL, "/convert") {
				return rp(jsonReply(map[string]any{"status": "processing", "request_check_url": "https://untrusted.example.test/check/abc"}))
			}
			return nil
		})
		_, err := ExtractPDFViaDatalab(bg(), []byte{0}, datalabOpts("Doc", 1))
		wantErr(t, err, `unexpected origin`)
		waitDeleted(t, n)
		for _, u := range n.urls() {
			if strings.Contains(u, "untrusted.example.test") {
				t.Fatal("must not send the API key to a polling URL returned by another origin")
			}
		}
	})

	tw(t, F, "Datalab conversion surfaces missing request_check_url", func(t *testing.T) {
		n := datalabEnv(t, func(c netCall) *netReply {
			if strings.HasSuffix(c.URL, "/convert") {
				return rp(jsonReply(map[string]any{"status": "processing"}))
			}
			return nil
		})
		_, err := ExtractPDFViaDatalab(bg(), []byte{0}, datalabOpts("Doc", 1))
		wantErr(t, err, `missing request_check_url`)
		waitDeleted(t, n)
	})

	tw(t, F, "Datalab conversion honors a custom API base", func(t *testing.T) {
		n := datalabEnv(t, func(c netCall) *netReply {
			if strings.HasSuffix(c.URL, "/check/abc123") {
				return rp(jsonReply(map[string]any{"status": "complete", "success": true, "markdown": "<!-- Page 1 -->\nCustom", "page_count": 1}))
			}
			return nil
		})
		t.Setenv("DATALAB_API_BASE", "https://datalab.example.test/api/v1")
		if got := DatalabAPIBase(); got != "https://datalab.example.test/api/v1" {
			t.Fatal(got)
		}
		result, err := ExtractPDFViaDatalab(bg(), []byte{0}, datalabOpts("Doc", 1))
		noErr(t, err)
		waitDeleted(t, n)
		if !strings.Contains(result.Markdown, "Custom") {
			t.Fatal(result.Markdown)
		}
		var upload, convert bool
		for _, u := range n.urls() {
			upload = upload || strings.HasPrefix(u, "https://datalab.example.test/api/v1/files/upload")
			convert = convert || strings.HasPrefix(u, "https://datalab.example.test/api/v1/convert")
		}
		if !upload || !convert {
			t.Fatalf("calls %q", n.urls())
		}
	})

	tw(t, F, "Datalab conversion preserves caller cancellation", func(t *testing.T) {
		n := datalabEnv(t, nil)
		ctx, cancel := context.WithCancel(bg())
		cancel()
		_, err := ExtractPDFViaDatalab(ctx, []byte{0}, datalabOpts("Doc", 1))
		if !errors.Is(err, context.Canceled) || n.count() != 0 {
			t.Fatalf("error %v after %d requests", err, n.count())
		}
	})

	tw(t, F, "Datalab conversion rejects invalid processing location env", func(t *testing.T) {
		n := datalabEnv(t, nil)
		t.Setenv("DATALAB_PROCESSING_LOCATION", "mars")
		_, err := ExtractPDFViaDatalab(bg(), []byte{0}, datalabOpts("Doc", 1))
		wantErr(t, err, `Failed to parse DATALAB_PROCESSING_LOCATION`)
		if n.count() != 0 {
			t.Fatal("no request may precede the location check")
		}
	})
}

// Behaviour of the port beyond the upstream cases: redirects are errors (fetch redirect: "error")
// and the file name sent to Datalab follows datalabFilename.
func TestDatalabRedirectAndFilename(t *testing.T) {
	datalabEnv(t, func(c netCall) *netReply {
		if strings.HasSuffix(c.URL, "/files/upload") {
			return rp(redirectTo(http.StatusFound, "https://evil.example/upload"))
		}
		return nil
	})
	_, err := ExtractPDFViaDatalab(bg(), []byte{0}, datalabOpts("Doc", 1))
	wantErr(t, err, `redirect`)
	for in, want := range map[string]string{"Sample Document": "sample-document", "  -- ": "document", "Ünïcode & Ünderscores_ok": "ncode-nderscoresok", strings.Repeat("a", 80): strings.Repeat("a", 60)} {
		if got := datalabFilename(in); got != want {
			t.Fatalf("datalabFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

// Failure paths the upstream file leaves out: the error body is cut at 300 characters, a rejected
// PUT, and a missing reference after the confirm step.
func TestDatalabFailurePaths(t *testing.T) {
	datalabEnv(t, func(c netCall) *netReply {
		if strings.HasSuffix(c.URL, "/files/upload") {
			return rp(netReply{Status: 500, Body: "  " + strings.Repeat("x", 400) + "  "})
		}
		return nil
	})
	_, err := ExtractPDFViaDatalab(bg(), []byte{0}, datalabOpts("Doc", 1))
	if want := "Datalab PDF conversion failed: HTTP 500 Internal Server Error: " + strings.Repeat("x", 300); err == nil || err.Error() != want {
		t.Fatalf("error %v", err)
	}

	n := datalabEnv(t, func(c netCall) *netReply {
		if strings.Contains(c.URL, "/put/") {
			return rp(netReply{Status: 403})
		}
		return nil
	})
	_, err = ExtractPDFViaDatalab(bg(), []byte{0}, datalabOpts("Doc", 1))
	if err == nil || err.Error() != "Datalab PDF upload failed: HTTP 403 Forbidden" {
		t.Fatalf("error %v", err)
	}
	waitDeleted(t, n)
	if n.countURL("/convert") != 0 {
		t.Fatal("no conversion after a rejected upload")
	}

	n = datalabEnv(t, func(c netCall) *netReply {
		switch {
		case strings.HasSuffix(c.URL, "/files/upload"):
			return rp(jsonReply(map[string]any{"file_id": 7, "upload_url": "https://storage.test/put/abc"}))
		case strings.HasSuffix(c.URL, "/confirm"):
			return rp(jsonReply(map[string]any{"file_id": 7}))
		}
		return nil
	})
	_, err = ExtractPDFViaDatalab(bg(), []byte{0}, datalabOpts("Doc", 1))
	wantErr(t, err, `missing file reference`)
	waitDeleted(t, n)
}
