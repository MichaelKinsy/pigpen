package mockls

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"lstools/internal/reqlog"
)

// syncBuf is a log sink the test can read while a handler goroutine may still write.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) Bytes() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return bytes.Clone(s.b.Bytes())
}

func start(t *testing.T, cfg string) (*httptest.Server, *syncBuf, *Server) {
	t.Helper()
	var c *Config
	if cfg != "" {
		var err error
		if c, err = ParseConfig([]byte(cfg)); err != nil {
			t.Fatal(err)
		}
	}
	log := &syncBuf{}
	s, err := New(c, log)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	s.SetBase(ts.URL)
	t.Cleanup(ts.Close)
	return ts, log, s
}

func entries(t *testing.T, log *syncBuf) []*reqlog.Entry {
	t.Helper()
	es, err := reqlog.Read(bytes.NewReader(log.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	return es
}

const boundary = "----LangSmithFormBoundaryabc123"

func mpBody() string {
	return "--" + boundary + "\r\nContent-Disposition: form-data; name=\"post.1\"\r\nContent-Type: application/json; length=7\r\n\r\n{\"a\":1}\r\n" +
		"--" + boundary + "\r\nContent-Disposition: form-data; name=\"post.1.inputs\"\r\nContent-Type: application/json; length=2\r\n\r\n{}\r\n" +
		"--" + boundary + "--\r\n"
}

func do(t *testing.T, method, url string, body io.Reader, hdr map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestInfoDefaultAndConfigured(t *testing.T) {
	ts, log, _ := start(t, "")
	resp := do(t, "GET", ts.URL+"/info", nil, map[string]string{"x-api-key": "k"})
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(b), `"use_multipart_endpoint":true`) || !strings.Contains(string(b), `"gzip_body_enabled":false`) {
		t.Fatalf("default info: %d %s", resp.StatusCode, b)
	}
	e := entries(t, log)[0]
	if e.Route != "info" || e.Transfer != "none" || e.Headers["x-api-key"] != "k" || e.Call != 1 || e.Seq != 1 {
		t.Fatalf("entry %+v", e)
	}

	ts2, _, _ := start(t, `{"info":{"batch_ingest_config":{"size_limit":3},"instance_flags":{"gzip_body_enabled":true},"self":"{{base}}"}}`)
	b2, _ := io.ReadAll(do(t, "GET", ts2.URL+"/info", nil, nil).Body)
	if !strings.Contains(string(b2), `"gzip_body_enabled":true`) || !strings.Contains(string(b2), `"self":"`+ts2.URL+`"`) {
		t.Fatalf("configured info: %s", b2)
	}
}

func TestMultipartChunkedParts(t *testing.T) {
	ts, log, _ := start(t, "")
	pr, pw := io.Pipe()
	go func() {
		// Several writes so the client really streams.
		for _, c := range []string{mpBody()[:30], mpBody()[30:]} {
			_, _ = io.WriteString(pw, c)
		}
		pw.Close()
	}()
	req, _ := http.NewRequest("POST", ts.URL+"/runs/multipart", pr)
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	e := entries(t, log)[0]
	if e.Route != "multipart" || e.Transfer != "chunked" || e.ContentLength != -1 {
		t.Fatalf("framing: %s %d", e.Transfer, e.ContentLength)
	}
	if e.ParseError != "" || e.Boundary != boundary || len(e.Parts) != 2 {
		t.Fatalf("parse: %q %q %d", e.ParseError, e.Boundary, len(e.Parts))
	}
	p := e.Parts[1]
	if e.Parts[0].Name != "post.1" || e.Parts[0].ContentType != "application/json; length=7" || string(e.Parts[0].Bytes) != `{"a":1}` {
		t.Fatalf("part 0: %+v", e.Parts[0])
	}
	if p.Name != "post.1.inputs" || string(p.Bytes) != "{}" || p.Text != "{}" {
		t.Fatalf("part 1: %+v", p)
	}
	if string(e.Body) != mpBody() {
		t.Fatal("raw body not kept verbatim")
	}
}

func TestMultipartContentLengthAndGzip(t *testing.T) {
	ts, log, _ := start(t, "")
	hdr := map[string]string{"Content-Type": "multipart/form-data; boundary=" + boundary}
	do(t, "POST", ts.URL+"/runs/multipart", strings.NewReader(mpBody()), hdr)
	var zb bytes.Buffer
	zw := gzip.NewWriter(&zb)
	_, _ = zw.Write([]byte(mpBody()))
	zw.Close()
	hdr["Content-Encoding"] = "gzip"
	do(t, "POST", ts.URL+"/runs/multipart", bytes.NewReader(zb.Bytes()), hdr)
	es := entries(t, log)
	if es[0].Transfer != "content-length" || es[0].ContentLength != int64(len(mpBody())) || es[0].Encoding != "" {
		t.Fatalf("plain: %+v", es[0])
	}
	if es[1].Encoding != "gzip" || len(es[1].Parts) != 2 || string(es[1].Decoded) != mpBody() || !bytes.Equal(es[1].Body, zb.Bytes()) || es[1].Call != 2 {
		t.Fatalf("gzip: %+v", es[1])
	}
}

func TestBadMultipartIsReportedNotHidden(t *testing.T) {
	ts, log, _ := start(t, "")
	hdr := map[string]string{"Content-Type": "multipart/form-data; boundary=" + boundary}
	do(t, "POST", ts.URL+"/runs/multipart", strings.NewReader("--"+boundary+"\nbroken"), hdr)
	do(t, "POST", ts.URL+"/runs/multipart", strings.NewReader("not gzip"), map[string]string{"Content-Encoding": "gzip"})
	es := entries(t, log)
	if !strings.Contains(es[0].ParseError, "multipart") || !strings.Contains(es[1].ParseError, "gzip") {
		t.Fatalf("%q %q", es[0].ParseError, es[1].ParseError)
	}
}

func TestBatchRunsPatchAndOther(t *testing.T) {
	ts, log, _ := start(t, "")
	js := map[string]string{"Content-Type": "application/json"}
	do(t, "POST", ts.URL+"/runs/batch", strings.NewReader(`{"post":[],"patch":[]}`), js)
	do(t, "POST", ts.URL+"/runs?x=1&a=2", strings.NewReader(`{"id":"i"}`), js)
	do(t, "PATCH", ts.URL+"/runs/abc", strings.NewReader(`{"end_time":1}`), js)
	resp := do(t, "GET", ts.URL+"/nope", nil, nil)
	if resp.StatusCode != 404 {
		t.Fatal(resp.StatusCode)
	}
	es := entries(t, log)
	want := []string{"batch", "runs", "patch", "other"}
	for i, w := range want {
		if es[i].Route != w {
			t.Fatalf("entry %d route %q want %q", i, es[i].Route, w)
		}
	}
	if es[1].Query != "x=1&a=2" || string(es[2].Body) != `{"end_time":1}` || es[2].Path != "/runs/abc" {
		t.Fatalf("%+v %+v", es[1], es[2])
	}
}

func TestFaultsPerRouteAndCallIndex(t *testing.T) {
	ts, log, _ := start(t, `{"faults":[
	  {"route":"multipart","calls":[1],"status":404},
	  {"route":"multipart","calls":[2],"status":422,"body":"nope"},
	  {"route":"multipart","from":3,"to":3,"status":429,"headers":{"Retry-After":"7"}},
	  {"route":"batch","status":500,"json":{"detail":"boom"}}
	]}`)
	hdr := map[string]string{"Content-Type": "multipart/form-data; boundary=" + boundary}
	var got []int
	var retry string
	for i := 0; i < 4; i++ {
		r := do(t, "POST", ts.URL+"/runs/multipart", strings.NewReader(mpBody()), hdr)
		got = append(got, r.StatusCode)
		if i == 2 {
			retry = r.Header.Get("Retry-After")
		}
	}
	if want := []int{404, 422, 429, 200}; !equalInts(got, want) || retry != "7" {
		t.Fatalf("statuses %v retry %q", got, retry)
	}
	r := do(t, "POST", ts.URL+"/runs/batch", strings.NewReader("{}"), nil)
	b, _ := io.ReadAll(r.Body)
	if r.StatusCode != 500 || string(b) != `{"detail":"boom"}` {
		t.Fatalf("batch fault %d %s", r.StatusCode, b)
	}
	es := entries(t, log)
	if es[0].Reply.Status != 404 || es[0].Reply.Fault != 0 || es[3].Reply.Fault != -1 || es[1].Reply.Status != 422 {
		t.Fatalf("reply records %+v %+v %+v", es[0].Reply, es[1].Reply, es[3].Reply)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestDelayAndDrop(t *testing.T) {
	ts, log, _ := start(t, `{"faults":[
	  {"route":"info","calls":[1],"delayMs":120},
	  {"route":"runs","calls":[1],"drop":true}
	]}`)
	t0 := time.Now()
	do(t, "GET", ts.URL+"/info", nil, nil)
	if time.Since(t0) < 100*time.Millisecond {
		t.Fatal("no delay")
	}
	req, _ := http.NewRequest("POST", ts.URL+"/runs", strings.NewReader("{}"))
	if resp, err := http.DefaultClient.Do(req); err == nil {
		resp.Body.Close()
		t.Fatal("expected a dropped connection")
	}
	es := entries(t, log)
	if len(es) != 2 || !es[1].Reply.Dropped || es[1].Reply.Status != 0 || es[0].Reply.DelayMs != 120 {
		t.Fatalf("%+v", es)
	}
}

func TestTokenAndOAuthMetadata(t *testing.T) {
	ts, log, _ := start(t, "")
	if r := do(t, "GET", ts.URL+"/.well-known/oauth-authorization-server", nil, nil); r.StatusCode != 404 {
		t.Fatal(r.StatusCode)
	}
	r := do(t, "POST", ts.URL+"/api/oauth/token", strings.NewReader("grant_type=refresh_token&refresh_token=r1"),
		map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	b, _ := io.ReadAll(r.Body)
	if !strings.Contains(string(b), "mock-access-token") {
		t.Fatal(string(b))
	}
	es := entries(t, log)
	if es[0].Route != "oauth-metadata" || es[1].Route != "token" || es[1].Form["refresh_token"][0] != "r1" || es[1].Form["grant_type"][0] != "refresh_token" {
		t.Fatalf("%+v", es)
	}

	ts2, _, _ := start(t, `{"oauthMetadata":{"issuer":"{{base}}","token_endpoint":"{{base}}/tok/oauth/token","device_authorization_endpoint":"{{base}}/d"},"token":{"access_token":"A"}}`)
	b2, _ := io.ReadAll(do(t, "GET", ts2.URL+"/.well-known/oauth-authorization-server", nil, nil).Body)
	if !strings.Contains(string(b2), `"issuer":"`+ts2.URL+`"`) {
		t.Fatal(string(b2))
	}
	b3, _ := io.ReadAll(do(t, "POST", ts2.URL+"/tok/oauth/token", strings.NewReader("a=b"), nil).Body)
	if string(b3) != `{"access_token":"A"}` {
		t.Fatal(string(b3))
	}
}

func TestAdminIsNotLoggedAndStatsCount(t *testing.T) {
	ts, log, s := start(t, "")
	do(t, "GET", ts.URL+"/__mockls/health", nil, nil)
	do(t, "GET", ts.URL+"/info", nil, nil)
	if n := len(entries(t, log)); n != 1 {
		t.Fatalf("%d logged", n)
	}
	if st := s.Stats(); st.Requests != 1 || st.ByRoute["info"] != 1 {
		t.Fatalf("%+v", st)
	}
	b, _ := io.ReadAll(do(t, "GET", ts.URL+"/__mockls/stats", nil, nil).Body)
	if !strings.Contains(string(b), `"requests":1`) || !strings.Contains(string(b), `"bytes":0`) {
		t.Fatal(string(b))
	}
}

func TestConfigRejectsMistakes(t *testing.T) {
	for _, bad := range []string{
		`{"faults":[{"route":"nope"}]}`,
		`{"faults":[{"route":"info","status":99}]}`,
		`{"faults":[{"route":"info","calls":[0]}]}`,
		`{"faults":[{"body":"x","json":{}}]}`,
		`{"unknown":1}`,
		`{"info":{`,
	} {
		if _, err := ParseConfig([]byte(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestBodyLimit(t *testing.T) {
	ts, log, _ := start(t, `{"maxBodyBytes":10}`)
	r := do(t, "POST", ts.URL+"/runs", strings.NewReader(strings.Repeat("x", 100)), nil)
	if r.StatusCode != 413 || len(entries(t, log)[0].Body) != 10 {
		t.Fatalf("%d", r.StatusCode)
	}
}

func TestStatsCountBodyBytesWithoutALog(t *testing.T) {
	s, err := New(nil, nil) // no log sink at all
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	s.SetBase(ts.URL)
	var zb bytes.Buffer
	zw := gzip.NewWriter(&zb)
	_, _ = zw.Write([]byte(mpBody()))
	zw.Close()
	do(t, "POST", ts.URL+"/runs/multipart", strings.NewReader(mpBody()), map[string]string{"Content-Type": "multipart/form-data; boundary=" + boundary})
	do(t, "POST", ts.URL+"/runs/multipart", bytes.NewReader(zb.Bytes()), map[string]string{"Content-Type": "multipart/form-data; boundary=" + boundary, "Content-Encoding": "gzip"})
	do(t, "POST", ts.URL+"/runs", strings.NewReader("{}"), nil)
	b, _ := io.ReadAll(do(t, "GET", ts.URL+"/__mockls/stats", nil, nil).Body)
	var st Stats
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatal(err)
	}
	wantRaw := int64(len(mpBody()) + zb.Len())
	wantDec := int64(2*len(mpBody())) + 2
	if st.Requests != 3 || st.ByRoute["multipart"] != 2 || st.Bytes != wantRaw+2 || st.ByRouteBytes["multipart"] != wantRaw || st.ByRouteBytes["runs"] != 2 ||
		st.DecodedBytes != wantDec || st.ByRouteDecodedBytes["multipart"] != int64(2*len(mpBody())) {
		t.Fatalf("%s (want raw multipart %d decoded %d)", b, wantRaw, wantDec)
	}
}
