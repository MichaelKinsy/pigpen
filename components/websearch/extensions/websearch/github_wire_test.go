package websearch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
)

// port/record-github-wire.mjs (writing testdata/github-wire.json) runs the vendored original with fake gh and git programs (shell scripts)
// and records the commands it starts and the documents it returns; the REST fallback talks to the
// fixed api.github.com, so there `fetch` is replaced by a recorder. This test writes the same programs,
// replays the same steps against the port and compares command for command and document for document.

type ghWireStep struct {
	URL        string       `json:"url"`
	ForceClone bool         `json:"forceClone"`
	Concurrent []ghWireStep `json:"concurrent"`
}

type ghWireCase struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	ConfigText string `json:"configText"`
	Shims      struct {
		GH  string `json:"gh"`
		Git string `json:"git"`
	} `json:"shims"`
	UTF8 []struct {
		Hex  string `json:"hex"`
		Text string `json:"text"`
	} `json:"utf8"`
	Base64 []struct {
		Text string `json:"text"`
		Hex  string `json:"hex"`
	} `json:"base64"`
	ParseInt []struct {
		Text  string `json:"text"`
		Value string `json:"value"`
	} `json:"parseInt"`
	Steps     []ghWireStep    `json:"steps"`
	Responses [][]any         `json:"responses"`
	Data      json.RawMessage `json:"data"`
	Expect    struct {
		Results  []any    `json:"results"`
		Calls    []string `json:"calls"`
		Requests []any    `json:"requests"`
	} `json:"expect"`
}

var (
	ghWireRuntimeRE = regexp.MustCompile(`runtime-[A-Za-z0-9]{6}`)
	ghWireParseRE   = regexp.MustCompile(`(?s)^(Failed to parse \S+): .*$`)
	ghWireRestRE    = regexp.MustCompile(`(?s)^(GitHub REST fallback failed): .*$`)
)

// ghWireSummary is summarize() of the recorder: a document over 20000 UTF-16 units is its length, hash and ends.
func ghWireSummary(s string) any {
	n := jsLen(s)
	if n <= 20000 {
		return s
	}
	sum := sha256.Sum256([]byte(s))
	return map[string]any{"len": float64(n), "sha256": hex.EncodeToString(sum[:]), "head": jsSlice(s, 0, 200), "tail": jsSlice(s, n-200, -1)}
}

func ghWireProject(r *ExtractedContent, norm func(string) string) any {
	if r == nil {
		return nil
	}
	o := map[string]any{"url": r.URL, "title": norm(r.Title), "error": nil}
	content := norm(r.Content)
	if r.Error != nil {
		e := norm(*r.Error)
		o["error"] = e
		if strings.HasPrefix(e, "GitHub REST fallback failed") {
			content = "<message>"
		}
	}
	o["content"] = ghWireSummary(content)
	if r.Status != nil {
		o["status"] = float64(*r.Status)
	}
	return o
}

func ghWireThrown(err error, norm func(string) string) any {
	return map[string]any{"thrown": norm(err.Error())}
}

func TestGitHubWire(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "github-wire.json"))
	noErr(t, err)
	var cases []ghWireCase
	noErr(t, json.Unmarshal(data, &cases))
	if len(cases) < 40 {
		t.Fatalf("only %d recorded cases", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			if c.Kind == "decode" {
				ghWireDecode(t, c)
				return
			}
			if c.Kind == "render" {
				ghWireRender(t, c)
				return
			}
			ghWireRun(t, c)
		})
	}
}

// ghWireDecode compares the decoders with Node's: Buffer toString("utf-8"), Buffer.from(text, "base64") and parseInt.
func ghWireDecode(t *testing.T, c ghWireCase) {
	for _, v := range c.UTF8 {
		b, err := hex.DecodeString(v.Hex)
		noErr(t, err)
		if got := decodeUTF8Lossy(b); got != v.Text {
			t.Errorf("utf-8 %s: got %q, want %q", v.Hex, got, v.Text)
		}
	}
	for _, v := range c.Base64 {
		if got := hex.EncodeToString(nodeBase64Decode(v.Text)); got != v.Hex {
			t.Errorf("base64 %q: got %s, want %s", v.Text, got, v.Hex)
		}
	}
	for _, v := range c.ParseInt {
		got := "NaN"
		if n, ok := jsParseInt10(v.Text); ok {
			got = formatNumber(n + 0)
			if n == 0 {
				got = "0"
			}
		}
		if got != v.Value {
			t.Errorf("parseInt %q: got %s, want %s", v.Text, got, v.Value)
		}
	}
}

func ghWireRender(t *testing.T, c ghWireCase) {
	var in struct {
		URL, Owner, Repo, Kind   string
		Number                   int
		Anchor                   string
		View                     map[string]any
		ReviewThreads            []map[string]any
		ReviewThreadsBounded     bool
		ReviewThreadsUnavailable bool
		FallbackNotes            []string
	}
	noErr(t, json.Unmarshal(c.Data, &in))
	got := renderGitHubPRIssue(issuePRRender{URL: in.URL, Owner: in.Owner, Repo: in.Repo, Kind: in.Kind, Number: in.Number, Anchor: in.Anchor, View: in.View,
		ReviewThreads: in.ReviewThreads, ReviewThreadsBounded: in.ReviewThreadsBounded, ReviewThreadsUnavailable: in.ReviewThreadsUnavailable, FallbackNotes: in.FallbackNotes})
	ghWireCompare(t, []any{ghWireProject(&got, func(s string) string { return s })}, c.Expect.Results)
}

func ghWireCompare(t *testing.T, got, want []any) {
	t.Helper()
	b, _ := json.Marshal(got)
	var round []any
	noErr(t, json.Unmarshal(b, &round))
	if !reflect.DeepEqual(round, want) {
		gj, _ := json.MarshalIndent(round, "", " ")
		wj, _ := json.MarshalIndent(want, "", " ")
		t.Fatalf("results differ\n got: %.3000s\nwant: %.3000s", gj, wj)
	}
}

func ghWireRun(t *testing.T, c ghWireCase) {
	skipWithoutShell(t)
	_, agentDir := isolate(t)
	root := t.TempDir()
	bin, clones := filepath.Join(root, "bin"), filepath.Join(root, "clones")
	for _, d := range []string{bin, clones} {
		noErr(t, os.MkdirAll(d, 0o755))
	}
	logPath := filepath.Join(root, "shim.log")
	writeFile(t, logPath, "")
	for name, body := range map[string]string{"gh": c.Shims.GH, "git": c.Shims.Git} {
		noErr(t, os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755))
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SHIM_LOG", logPath)
	writeConfig(t, agentDir, strings.ReplaceAll(c.ConfigText, "<clones>", clones))
	realClones, err := filepath.EvalSymlinks(clones)
	noErr(t, err)
	norm := func(s string) string {
		for _, r := range []string{clones, realClones} {
			s = strings.ReplaceAll(s, r, "<clones>")
		}
		s = strings.ReplaceAll(s, agentDir, "<agent>")
		s = ghWireRuntimeRE.ReplaceAllString(s, "runtime-<id>")
		s = ghWireParseRE.ReplaceAllString(s, "$1")
		return ghWireRestRE.ReplaceAllString(s, "$1")
	}

	var results []any
	var requests []any
	var reqMu sync.Mutex
	state := ghState(t)
	var runOne func(s ghWireStep) any
	switch c.Kind {
	case "extract":
		runOne = func(s ghWireStep) any {
			r, err := state.extractGitHub(bg(), s.URL, s.ForceClone)
			if err != nil {
				return ghWireThrown(err, norm)
			}
			return ghWireProject(r, norm)
		}
	case "pr":
		useNet(t, func(netCall) netReply { return reply(404, "not found") })
		runOne = func(s ghWireStep) any {
			r, err := state.extractGitHubIssuePR(bg(), s.URL, pageOpts())
			if err != nil {
				return ghWireThrown(err, norm)
			}
			return ghWireProject(r, norm)
		}
	case "content":
		t.Cleanup(SetPageFetch(func(ctx context.Context, u *url.URL, init RequestInit) (*http.Response, error) {
			rec := map[string]any{"url": u.String(), "method": init.Method}
			if init.Method == "" {
				rec["method"] = "GET"
			}
			if strings.HasPrefix(u.String(), "https://api.github.com/") {
				rec["accept"], rec["userAgent"] = init.Header.Get("Accept"), init.Header.Get("User-Agent")
			}
			reqMu.Lock()
			requests = append(requests, rec)
			reqMu.Unlock()
			for _, row := range c.Responses {
				if u.String() != "https://api.github.com/repos/o/r"+row[0].(string) {
					continue
				}
				if row[1] == "throw" {
					return nil, errors.New(row[2].(string))
				}
				body, isText := row[2].(string)
				if !isText {
					b, _ := json.Marshal(row[2])
					body = string(b)
				}
				h := http.Header{}
				if len(row) > 3 {
					for k, v := range row[3].(map[string]any) {
						h.Set(k, v.(string))
					}
				}
				return &http.Response{StatusCode: int(row[1].(float64)), Header: h, Body: io.NopCloser(strings.NewReader(body)), Request: &http.Request{}}, nil
			}
			return &http.Response{StatusCode: 404, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("not found")), Request: &http.Request{}}, nil
		}))
		runOne = func(s ghWireStep) any {
			r := ExtractContent(bg(), s.URL, pageOpts())
			return ghWireProject(&r, norm)
		}
	default:
		t.Fatalf("unknown kind %q", c.Kind)
	}
	for _, s := range c.Steps {
		if len(s.Concurrent) == 0 {
			results = append(results, runOne(s))
			continue
		}
		group := make([]any, len(s.Concurrent))
		var wg sync.WaitGroup
		for i, cs := range s.Concurrent {
			wg.Add(1)
			go func() { defer wg.Done(); group[i] = runOne(cs) }()
		}
		wg.Wait()
		results = append(results, group)
	}
	state.clearCloneCache()
	ghWireCompare(t, results, c.Expect.Results)

	var calls []string
	for _, line := range strings.Split(readFile(t, logPath), "\n") {
		if line != "" {
			calls = append(calls, norm(line))
		}
	}
	sort.Strings(calls)
	if !reflect.DeepEqual(calls, c.Expect.Calls) && !(len(calls) == 0 && len(c.Expect.Calls) == 0) {
		t.Fatalf("commands differ\n got: %q\nwant: %q", calls, c.Expect.Calls)
	}
	if c.Kind == "content" {
		b, _ := json.Marshal(requests)
		var got []any
		noErr(t, json.Unmarshal(b, &got))
		if !reflect.DeepEqual(got, c.Expect.Requests) && !(len(got) == 0 && len(c.Expect.Requests) == 0) {
			t.Fatalf("requests differ\n got: %v\nwant: %v", got, c.Expect.Requests)
		}
	}
}
