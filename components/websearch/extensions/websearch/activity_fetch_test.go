package websearch

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"strings"
	"testing"
)

// The entries a page fetch leaves in the activity monitor, one case per ending of extract.ts
// fetchViaHttp (the logComplete and logError calls at extract.ts:1224-1458) and of the Jina reader
// (extract.ts:380-431). An ending that logs a status completes the entry with the HTTP status; an
// ending that logs an error leaves the message and no status; an abort completes it with status 0.

var errTransport = errors.New("connect failed")

func fetchEntries(t *testing.T) []ActivityEntry {
	t.Helper()
	var out []ActivityEntry
	for _, e := range Activity.Entries() {
		if e.Type == "fetch" {
			out = append(out, e)
		}
	}
	return out
}

func wantLoggedStatus(t *testing.T, e ActivityEntry, status int) {
	t.Helper()
	if e.EndTime == 0 || e.Status == nil || *e.Status != status || e.Error != "" {
		t.Fatalf("want an entry completed with status %d: %+v", status, e)
	}
}

func wantLoggedError(t *testing.T, e ActivityEntry, contains string) {
	t.Helper()
	if e.EndTime == 0 || e.Status != nil || !strings.Contains(e.Error, contains) {
		t.Fatalf("want an entry closed with an error containing %q: %+v", contains, e)
	}
}

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestFetchEntriesOfEveryEnding(t *testing.T) {
	const page = "https://example.com/page"
	type tc struct {
		name   string
		config string
		mode   string
		reply  netReply
		status int    // the entry is completed with this status ...
		errHas string // ... or closed with an error containing this
		// direct runs the HTTP path alone: ExtractContent checks the configuration and the address first.
		direct bool
	}
	html := http.Header{"Content-Type": {"text/html"}}
	big := strings.Repeat("x", 6<<20)
	pdfBytes, err := os.ReadFile("testdata/pdf/flate.pdf")
	noErr(t, err)
	cases := []tc{
		{name: "a non-2xx status", reply: reply(404, "gone"), status: 404},
		{name: "a disabled PDF", config: `{"pdf":{"enabled":false}}`, reply: netReply{Status: 200, Body: "%PDF-1.4", Header: http.Header{"Content-Type": {"application/pdf"}}}, status: 200},
		{name: "a declared size over the limit", reply: netReply{Status: 200, Body: "x", Header: http.Header{"Content-Type": {"text/html"}, "Content-Length": {"99999999"}}}, status: 200},
		{name: "a raw fetch of a binary type", mode: "raw", reply: netReply{Status: 200, Body: "x", Header: http.Header{"Content-Type": {"application/zip"}}}, status: 200},
		{name: "a raw fetch of text", mode: "raw", reply: netReply{Status: 200, Body: "plain", Header: http.Header{"Content-Type": {"text/plain"}}}, status: 200},
		{name: "a raw fetch that is too large", mode: "raw", reply: netReply{Status: 200, Body: big, Header: http.Header{"Content-Type": {"text/plain"}}}, errHas: "too large"},
		{name: "an image when images are disabled", config: `{"image":{"enabled":false}}`, reply: netReply{Status: 200, Body: "x", Header: http.Header{"Content-Type": {"image/png"}}}, status: 200},
		{name: "an image", reply: netReply{Status: 200, Body: string(tinyPNG(t)), Header: http.Header{"Content-Type": {"image/png"}}}, status: 200},
		{name: "an image that is too large", reply: netReply{Status: 200, Body: big, Header: http.Header{"Content-Type": {"image/png"}}}, errHas: "too large"},
		{name: "an image that cannot be decoded", reply: netReply{Status: 200, Body: "not an image", Header: http.Header{"Content-Type": {"image/png"}}}, status: 200},
		{name: "a PDF", reply: netReply{Status: 200, Body: string(pdfBytes), Header: http.Header{"Content-Type": {"application/pdf"}}}, status: 200},
		{name: "a PDF that is too large", config: `{"pdf":{"maxSizeMB":1}}`, reply: netReply{Status: 200, Body: big[:2<<20], Header: http.Header{"Content-Type": {"application/pdf"}}}, errHas: "pdf.maxSizeMB"},
		{name: "a PDF that cannot be read", reply: netReply{Status: 200, Body: "%PDF-1.4 not a document", Header: http.Header{"Content-Type": {"application/pdf"}}}, errHas: "PDF"},
		{name: "a binary type", reply: netReply{Status: 200, Body: "x", Header: http.Header{"Content-Type": {"application/octet-stream"}}}, status: 200},
		{name: "a page that is too large", reply: netReply{Status: 200, Body: big, Header: html}, errHas: "too large"},
		{name: "a Cloudflare challenge", reply: netReply{Status: 200, Body: "<html><body>wait</body></html>", Header: http.Header{"Content-Type": {"text/html"}, "Cf-Mitigated": {"challenge"}}}, status: 200},
		{name: "text that is not HTML", reply: netReply{Status: 200, Body: "plain", Header: http.Header{"Content-Type": {"text/plain"}}}, status: 200},
		{name: "an HTML page", reply: netReply{Status: 200, Body: "<html><body><p>hello</p></body></html>", Header: html}, status: 200},
		{name: "a transport failure", reply: netReply{Err: errTransport}, errHas: "connect failed"},
		{name: "a blocked address", direct: true, reply: reply(200, ""), errHas: "Blocked"},
		{name: "a malformed ssrf section", direct: true, config: `{"ssrf":5}`, reply: reply(200, ""), errHas: "ssrf"},
		{name: "a malformed domain policy", direct: true, config: `{"fetchContent":5}`, reply: reply(200, ""), errHas: "fetchContent"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			extractEnv(t, c.config)
			useNet(t, func(netCall) netReply { return c.reply })
			Activity.Clear()
			url := page
			if c.name == "a blocked address" {
				url = "http://10.1.2.3/page"
			}
			o := pageOpts()
			o.Mode = c.mode
			if c.direct {
				doHTTP(bg(), url, o)
			} else {
				ExtractContent(bg(), url, o)
			}
			got := fetchEntries(t)
			if len(got) != 1 {
				t.Fatalf("%+v", got)
			}
			if c.errHas != "" {
				wantLoggedError(t, got[0], c.errHas)
			} else {
				wantLoggedStatus(t, got[0], c.status)
			}
		})
	}
	t.Run("a caller that cancels while the page is parsed", func(t *testing.T) {
		extractEnv(t, "")
		useNet(t, func(netCall) netReply {
			return netReply{Status: 200, Body: "<html><body><p>hello</p></body></html>", Header: html}
		})
		ctx, cancel := context.WithCancel(bg())
		prev := extractHook
		extractHook = func(stage string) {
			if stage == "before-parse" {
				cancel()
			}
		}
		t.Cleanup(func() { extractHook = prev })
		Activity.Clear()
		ExtractContent(ctx, page, pageOpts())
		got := fetchEntries(t)
		if len(got) != 1 {
			t.Fatalf("%+v", got)
		}
		wantLoggedStatus(t, got[0], 0)
	})
}

func TestJinaReaderEntries(t *testing.T) {
	const page = "https://example.com/article"
	type tc struct {
		name   string
		config string
		reply  netReply
		status int
		errHas string
	}
	cases := []tc{
		{name: "markdown", reply: reply(200, "Markdown Content:\n# T\n\nbody"), status: 200},
		{name: "a non-2xx status", reply: reply(503, "down"), status: 503},
		{name: "a transport failure", reply: netReply{Err: errTransport}, errHas: "connect failed"},
		{name: "a malformed ssrf section", config: `{"ssrf":5}`, errHas: "ssrf"},
		{name: "a malformed domain policy", config: `{"fetchContent":5}`, errHas: "fetchContent"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			extractEnv(t, c.config)
			useNet(t, func(netCall) netReply { return c.reply })
			Activity.Clear()
			extractWithJinaReader(bg(), page, 5000, pubLookup)
			var got []ActivityEntry
			for _, e := range Activity.Entries() {
				if e.Type == "api" {
					got = append(got, e)
				}
			}
			if len(got) != 1 || got[0].Query != "jina: "+page {
				t.Fatalf("%+v", got)
			}
			if c.errHas != "" {
				wantLoggedError(t, got[0], c.errHas)
			} else {
				wantLoggedStatus(t, got[0], c.status)
			}
		})
	}
	t.Run("a blocked address", func(t *testing.T) {
		extractEnv(t, "")
		useNet(t, func(netCall) netReply { return reply(200, "") })
		Activity.Clear()
		extractWithJinaReader(bg(), "http://10.1.2.3/x", 5000, pubLookup)
		got := Activity.Entries()
		if len(got) != 1 {
			t.Fatalf("%+v", got)
		}
		wantLoggedError(t, got[0], "Blocked")
	})
}
