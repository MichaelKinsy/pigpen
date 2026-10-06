package rpiv_web_tools

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// TestMain isolates HOME and clears every variable the extension reads, as the original's test setup does
// (test/setup.ts removes the config file in a shared beforeEach).
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "rpiv-web-tools-test-home-")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	os.Setenv("USERPROFILE", home)
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}

// clearEnv removes the environment the extension reads for one test (restored by the test's cleanup).
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"XDG_CONFIG_HOME", "WEB_SEARCH_PROVIDER", braveKeyEnv, tavilyKeyEnv, serperKeyEnv, exaKeyEnv, youcomKeyEnv,
		jinaKeyEnv, firecrawlKeyEnv, perplexityKeyEnv, searxngKeyEnv, searxngURLEnv, ollamaKeyEnv, ollamaHostEnv, "GITHUB_TOKEN"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	os.RemoveAll(filepath.Dir(configPath()))
	os.RemoveAll(filepath.Join(defaultConfigDir(), configName))
}

func eq(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

type obj = map[string]any
type arr = []any

func writeRaw(t *testing.T, contents string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(configPath()), 0o755)
	if err := os.WriteFile(configPath(), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeConfigFile(t *testing.T, v any) {
	t.Helper()
	data, _ := json.MarshalIndent(v, "", "  ")
	writeRaw(t, string(data))
}

func readSaved(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile(configPath())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// fakeNet is the network: every request goes to a handler, and is recorded.
type fakeNet struct {
	mu       sync.Mutex
	Requests []netReq
	handler  func(r netReq) netResp
}

type netReq struct {
	Method, URL string
	Header      http.Header
	Body        string
}

type netResp struct {
	Status  int
	Header  map[string]string
	Body    string
	Failure error // a transport failure instead of a response
}

func (f *fakeNet) RoundTrip(req *http.Request) (*http.Response, error) {
	var body string
	if req.Body != nil {
		data, _ := io.ReadAll(req.Body)
		body = string(data)
	}
	r := netReq{Method: req.Method, URL: req.URL.String(), Header: req.Header.Clone(), Body: body}
	f.mu.Lock()
	f.Requests = append(f.Requests, r)
	f.mu.Unlock()
	resp := f.handler(r)
	if resp.Failure != nil {
		return nil, resp.Failure
	}
	status := resp.Status
	if status == 0 {
		status = 200
	}
	h := http.Header{}
	for k, v := range resp.Header {
		h.Set(k, v)
	}
	return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: h, Body: io.NopCloser(strings.NewReader(resp.Body)), Request: req}, nil
}

// useNet installs a handler for the test; the returned value records the requests.
func useNet(t *testing.T, handler func(r netReq) netResp) *fakeNet {
	t.Helper()
	f := &fakeNet{handler: handler}
	t.Cleanup(SetTransport(f))
	return f
}

// jsonResp answers 200 with a JSON body.
func jsonResp(v any) netResp {
	b, _ := json.Marshal(v)
	return netResp{Header: map[string]string{"Content-Type": "application/json"}, Body: string(b)}
}

// plainTheme returns raw text.
type plainTheme struct{}

func (plainTheme) Fg(_, text string) string { return text }
func (plainTheme) Bold(text string) string  { return text }
