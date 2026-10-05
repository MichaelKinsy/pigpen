package websearch

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Twins of data-uri-sanitize.test.mjs.

const dataURIMarkerPrefix = "[pi-web-access inline data URI omitted;"

func markerCount(text string) int { return strings.Count(text, dataURIMarkerPrefix) }

func encodeURIComponent(s string) string {
	// JS keeps A-Z a-z 0-9 - _ . ! ~ * ' ( ) unescaped; url.QueryEscape differs on space and those.
	var b strings.Builder
	for _, c := range []byte(s) {
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.IndexByte("-_.!~*'()", c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func assertRequiredMarkerFields(t *testing.T, marker string, ordinal int, source string) {
	t.Helper()
	matchRE(t, fmt.Sprintf(`ordinal=%d(?:;|\])`, ordinal), marker)
	if !strings.Contains(marker, "source="+source+";") {
		t.Fatalf("missing source in %s", marker)
	}
	for _, re := range []string{`mime=[a-z0-9!#$&^_.+/-]+;`, `encoding=(?:base64|percent-encoded);`, `encodedBytes=\d+;`,
		`decodedBytes=(?:\d+|unknown)(?:;|\])`, `sha256=[a-f0-9]{64};`, `digestBasis=(?:decoded|encoded);`, `retrieval=not-retained\]`} {
		matchRE(t, re, marker)
	}
}

func TestUpstream_data_uri_sanitize(t *testing.T) {
	const F = "data-uri-sanitize"
	const src = "urls[0].content"
	noData := regexp.MustCompile(`(?i)data:`)

	tw(t, F, "original 21-image failure shape collapses to bounded markers", func(t *testing.T) {
		encoded := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("\xa5", 240*1024)))
		parts := make([]string, 21)
		for i := range parts {
			parts[i] = fmt.Sprintf("Prose %d before ![OSF screenshot %d](data:image/png;base64,%s) prose %d after.", i, i, encoded, i)
		}
		content := strings.Join(parts, "\n\n")
		if len(content) <= 4*1024*1024 {
			t.Fatal("fixture too small")
		}
		started := time.Now()
		text, omissions := SanitizeInlineDataURIs(content, src)
		if time.Since(started) > 15*time.Second {
			t.Fatalf("took %v", time.Since(started))
		}
		if len(omissions) != 21 || markerCount(text) != 21 || regexp.MustCompile(`(?i)data:image/png;base64,`).MatchString(text) {
			t.Fatalf("%d omissions, %d markers", len(omissions), markerCount(text))
		}
		for ordinal := 1; ordinal <= 21; ordinal++ {
			assertRequiredMarkerFields(t, text, ordinal, src)
		}
		if len(text) >= 32*1024 {
			t.Fatalf("output is %d bytes", len(text))
		}
	})
	tw(t, F, "many individually small images have no per-payload exemption", func(t *testing.T) {
		const count = 4300
		encoded := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("Z", 768)))
		parts := make([]string, count)
		for i := range parts {
			parts[i] = fmt.Sprintf("Paragraph %d ![small %d](data:image/png;base64,%s) end %d.", i, i, encoded, i)
		}
		text, omissions := SanitizeInlineDataURIs(strings.Join(parts, "\n"), src)
		if len(omissions) != count || markerCount(text) != count || regexp.MustCompile(`(?i)data:image/png;base64,`).MatchString(text) {
			t.Fatalf("%d omissions, %d markers", len(omissions), markerCount(text))
		}
	})
	tw(t, F, "readable prose and Markdown alt text remain unchanged around replacements", func(t *testing.T) {
		input := `Before ![architecture diagram](data:image/png;base64,SGk=) between <img src="data:text/plain,hello%20world" alt="inline"> after.`
		text, omissions := SanitizeInlineDataURIs(input, src)
		if len(omissions) != 2 || markerCount(text) != 2 || noData.MatchString(text) ||
			!strings.HasPrefix(text, "Before ![architecture diagram](") || !strings.Contains(text, `) between <img src="`) ||
			!strings.HasSuffix(text, `" alt="inline"> after.`) {
			t.Fatalf("%q", text)
		}
	})
	tw(t, F, "small legitimate SVG is explicitly omitted with decoded digest metadata", func(t *testing.T) {
		decoded := `<svg xmlns="http://www.w3.org/2000/svg"><path d="M0 0"/></svg>`
		payload := encodeURIComponent(decoded)
		text, omissions := SanitizeInlineDataURIs("Diagram: ![tiny vector](data:image/svg+xml;charset=utf-8,"+payload+") done", src)
		if len(omissions) != 1 {
			t.Fatal(len(omissions))
		}
		o := omissions[0]
		sum := sha256.Sum256([]byte(decoded))
		if o.MimeType != "image/svg+xml" || o.Encoding != "percent-encoded" || o.EncodedBytes != len(payload) ||
			o.DecodedBytes == nil || *o.DecodedBytes != len(decoded) || o.SHA256 != hex.EncodeToString(sum[:]) ||
			o.DigestBasis != "decoded" || o.Retrieval != "not-retained" || !strings.Contains(text, "![tiny vector](") || noData.MatchString(text) {
			t.Fatalf("%+v %q", o, text)
		}
		assertRequiredMarkerFields(t, text, 1, src)
	})
	tw(t, F, "data URI variants are handled without payload-bearing errors", func(t *testing.T) {
		fixtures := []struct{ key, input string }{
			{"base64", "data:image/png;base64,SGVsbG8="},
			{"percent", "data:text/plain,hello%20world"},
			{"missingMime", "data:;base64,SGk="},
			{"parameters", "data:text/plain;charset=utf-8,hello%2Cworld"},
			{"quoted", "<img src='data:image/gif;base64,R0lGODlhAQABAIAAAAUEBA=='>"},
			{"quotedApostrophe", `<img src="data:text/plain,it's">`},
			{"markdown", "![alt](data:application/octet-stream;base64,AAEC)"},
			{"invalidBase64", "data:image/png;base64,***not-base64***"},
			{"mixedCase", "DATA:IMAGE/PNG;BASE64,UE5H"},
		}
		got := map[string]DataURIOmission{}
		texts := map[string]string{}
		for _, f := range fixtures {
			text, om := SanitizeInlineDataURIs(f.input, f.key)
			if len(om) != 1 || noData.MatchString(text) {
				t.Fatalf("%s: %d omissions %q", f.key, len(om), text)
			}
			got[f.key], texts[f.key] = om[0], text
		}
		if got["missingMime"].MimeType != "text/plain" || got["missingMime"].Encoding != "base64" ||
			*got["parameters"].DecodedBytes != 11 || got["mixedCase"].MimeType != "image/png" {
			t.Fatalf("%+v", got)
		}
		inv := got["invalidBase64"]
		if inv.DecodedBytes != nil || inv.DigestBasis != "encoded" || inv.DecodeError != "invalid-base64-character" ||
			!strings.Contains(texts["invalidBase64"], "decodedBytes=unknown") ||
			!strings.Contains(texts["invalidBase64"], "decodeError=invalid-base64-character") || strings.Contains(texts["invalidBase64"], "not-base64") {
			t.Fatalf("%+v %q", inv, texts["invalidBase64"])
		}
	})
	tw(t, F, "malformed enclosed data URIs are wholly removed without payload suffix leakage", func(t *testing.T) {
		nine := func(n int) *int { return &n }
		cases := []struct {
			input     string
			forbidden []string
			err       string
			encoded   *int
			decoded   *int
		}{
			{input: `<img src="data:image/png;base64,QUFB QkJC">`, forbidden: []string{"QUFB", "QkJC"}, err: "invalid-base64-character"},
			{input: "![x](data:text/plain,abc(def)ghi)", forbidden: []string{"abc(def)ghi", "ghi)"}, decoded: nine(11)},
			{input: "data:text/plain,abc(def)ghi", forbidden: []string{"abc(def)ghi"}, decoded: nine(11)},
			{input: "![x](data:image/png;base64,SGVs\nbG8=)", forbidden: []string{"SGVs", "bG8="}, err: "invalid-base64-character"},
			{input: `<img src="data:image/png;base64">`, forbidden: []string{"data:image/png;base64"}, err: "missing-comma", encoded: nine(len("image/png;base64"))},
		}
		for _, c := range cases {
			text, om := SanitizeInlineDataURIs(c.input, src)
			if len(om) != 1 || markerCount(text) != 1 || noData.MatchString(text) {
				t.Fatalf("%q -> %q", c.input, text)
			}
			for _, f := range c.forbidden {
				if strings.Contains(text, f) {
					t.Fatalf("%q leaked %q: %q", c.input, f, text)
				}
			}
			if c.err != "" && om[0].DecodeError != c.err || c.encoded != nil && om[0].EncodedBytes != *c.encoded ||
				c.decoded != nil && (om[0].DecodedBytes == nil || *om[0].DecodedBytes != *c.decoded) {
				t.Fatalf("%q: %+v", c.input, om[0])
			}
		}
		if text, _ := SanitizeInlineDataURIs("ordinary data: value", src); text != "ordinary data: value" {
			t.Fatal(text)
		}
	})
	tw(t, F, "valid RFC MIME token characters are normalized without truncating the URI", func(t *testing.T) {
		for _, mime := range []string{"application/x.foo~bar", "application/x.foo*bar", "application/x.foo'bar"} {
			text, om := SanitizeInlineDataURIs(`<img src="data:`+mime+`,ok">`, src)
			if len(om) != 1 || om[0].MimeType != mime || *om[0].DecodedBytes != 2 || noData.MatchString(text) {
				t.Fatalf("%s: %+v %q", mime, om, text)
			}
		}
	})
	tw(t, F, "malformed comma-less candidates scale linearly and are explicitly marked", func(t *testing.T) {
		measure := func(count int) time.Duration {
			input := strings.TrimSuffix(strings.Repeat("data:x; ", count), " ")
			started := time.Now()
			text, om := SanitizeInlineDataURIs(input, src)
			elapsed := time.Since(started)
			if len(om) != count || noData.MatchString(text) {
				t.Fatalf("%d omissions", len(om))
			}
			for _, o := range om {
				if o.DecodeError != "missing-comma" {
					t.Fatal(o.DecodeError)
				}
			}
			return elapsed
		}
		small, large := measure(2000), measure(4000)
		if large > 10*time.Second || large > small*4+500*time.Millisecond {
			t.Fatalf("nonlinear malformed scan: %v -> %v", small, large)
		}
	})
	tw(t, F, "overlong headers are bounded and classified without retaining header or payload", func(t *testing.T) {
		header := "image/png;name=" + strings.Repeat("x", 2048) + ";base64"
		text, om := SanitizeInlineDataURIs("![x](data:"+header+",SGVsbG8=)", src)
		if len(om) != 1 || om[0].DecodedBytes != nil || om[0].DecodeError != "header-too-long" || om[0].DigestBasis != "encoded" ||
			strings.Contains(text, strings.Repeat("x", 128)) || noData.MatchString(text) {
			t.Fatalf("%+v", om)
		}
	})
}

// Extra: FetchAllContent applies the sanitizer (non-raw); raw mode passes content through.
func TestFetchSanitizesDataURIs(t *testing.T) {
	extractEnv(t, "")
	body := "hello ![x](data:image/png;base64,SGk=) world"
	useNet(t, func(netCall) netReply {
		return netReply{Status: 200, Body: body, Header: hdr("content-type", "text/plain")}
	})
	res := FetchAllContent(bg(), []string{"https://93.184.216.34/a"}, ExtractOptions{})
	if len(res) != 1 || strings.Contains(res[0].Content, "data:image") || markerCount(res[0].Content) != 1 {
		t.Fatalf("%+v", res)
	}
	raw := FetchAllContent(bg(), []string{"https://93.184.216.34/a"}, ExtractOptions{Mode: "raw"})
	if !strings.Contains(raw[0].Content, "data:image/png;base64,SGk=") {
		t.Fatalf("raw content changed: %q", raw[0].Content)
	}
}

func TestDataURIEdgeCases(t *testing.T) {
	for _, c := range []struct{ input, mime, err string }{
		{"data:a/b/c,x", "application/octet-stream", ""},
		{"data:/b,x", "application/octet-stream", ""},
		{"data:a/,x", "application/octet-stream", ""},
		{"data:abc,x", "application/octet-stream", ""},
		{"data:text/plain;base64,A===", "text/plain", "invalid-base64-padding"},
		{"data:text/plain;base64,SGk=SGk", "text/plain", "invalid-base64-padding"},
		{"data:text/plain;base64,SGVsb", "text/plain", "invalid-base64-length"},
		{"data:text/plain;base64,SG=k", "text/plain", "invalid-base64-padding"},
		{"data:text/plain;base64,S%C3%A9k=", "text/plain", "non-ascii-base64"},
		{"data:text/plain,%zz", "text/plain", "invalid-percent-escape"},
		{"data:text/plain,%4", "text/plain", "invalid-percent-escape"},
		{"data:text/plain;base64,SGk", "text/plain", ""}, // unpadded is valid
	} {
		text, om := SanitizeInlineDataURIs(c.input, "v")
		if len(om) != 1 || om[0].MimeType != c.mime || om[0].DecodeError != c.err {
			t.Errorf("%q: %+v %q", c.input, om, text)
		}
	}
	if got := safeMarkerSource(strings.Repeat("a", 300)); len(got) != maxMarkerSourceChars || !strings.Contains(got, "~") {
		t.Errorf("long source not bounded: %d", len(got))
	}
	if got := safeMarkerSource("we ird/path"); got != "we_ird_path" {
		t.Error(got)
	}
	if text, om := SanitizeInlineDataURIs("no uri here", "v"); text != "no uri here" || om != nil {
		t.Error(text)
	}
	if text, _ := SanitizeInlineDataURIs("metadata:x,y and data-data:x,y", "v"); strings.Contains(text, "omitted") {
		t.Error("scheme boundary ignored: " + text)
	}
}
