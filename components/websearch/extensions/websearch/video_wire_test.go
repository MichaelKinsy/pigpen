package websearch

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

// The video and YouTube endpoints and programs the original uses are not reachable from a pigeq
// scenario (Perplexity, the YouTube thumbnail host and the Gemini API are fixed endpoints in
// the default configuration; yt-dlp, ffmpeg and ffprobe are canned there, one answer for every
// call). This test replays what the ORIGINAL did, recorded by port/record-video-requests.mjs
// from the unmodified oracle with a stubbed fetch and shell scripts for the three programs
// (extensions/websearch/testdata/video-wire.json): the port, run against the same scripts,
// must start the same commands, send the same requests in the same order and return the same
// result.

type videoResponse struct {
	Match       string            `json:"match"`
	Method      string            `json:"method"`
	Status      int               `json:"status"`
	JSON        json.RawMessage   `json:"json"`
	Text        string            `json:"text"`
	BytesBase64 *string           `json:"bytesBase64"`
	Headers     map[string]string `json:"headers"`
	Times       *int              `json:"times"`
}

type videoWireCase struct {
	Env       map[string]string `json:"env"`
	Config    map[string]any    `json:"config"`
	RawConfig *string           `json:"rawConfig"`
	Files     map[string]string `json:"files"`
	Sparse    map[string]int64  `json:"sparse"`
	Scripts   map[string]string `json:"scripts"`
	URL       string            `json:"url"`
	Options   struct {
		Prompt    string `json:"prompt"`
		Model     string `json:"model"`
		Frames    int    `json:"frames"`
		Timestamp string `json:"timestamp"`
	} `json:"options"`
	Responses []videoResponse `json:"responses"`
	Commands  [][]string      `json:"commands"`
	Requests  []wireRequest   `json:"requests"`
	Result    any             `json:"result"`
}

var jsonParseFailure = regexp.MustCompile(`(Failed to parse \S*web-search\.json): [^"]*`)

func TestVideoWireFixture(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the recorded programs are shell scripts")
	}
	raw, err := os.ReadFile("testdata/video-wire.json")
	noErr(t, err)
	var fx struct {
		Cases map[string]videoWireCase `json:"cases"`
	}
	noErr(t, json.Unmarshal(raw, &fx))
	if len(fx.Cases) < 50 {
		t.Fatalf("fixture has %d cases", len(fx.Cases))
	}
	names := make([]string, 0, len(fx.Cases))
	for n := range fx.Cases {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		c := fx.Cases[name]
		t.Run(name, func(t *testing.T) {
			_, agentDir := geminiEnv(t)
			unsetenv(t, "PERPLEXITY_API_KEY")
			for k, v := range c.Env {
				t.Setenv(k, v)
			}
			switch {
			case c.RawConfig != nil:
				writeConfig(t, agentDir, *c.RawConfig)
			case c.Config != nil:
				b, _ := json.Marshal(c.Config)
				writeConfig(t, agentDir, string(b))
			}
			for f, content := range c.Files {
				noErr(t, os.WriteFile(filepath.Join(agentDir, f), []byte(content), 0o600))
			}
			for f, size := range c.Sparse {
				p := filepath.Join(agentDir, f)
				noErr(t, os.WriteFile(p, nil, 0o600))
				noErr(t, os.Truncate(p, size))
			}
			bin := filepath.Join(t.TempDir(), "bin")
			noErr(t, os.MkdirAll(bin, 0o755))
			for prog, script := range c.Scripts {
				noErr(t, os.WriteFile(filepath.Join(bin, prog), []byte(script), 0o755))
			}
			log := filepath.Join(t.TempDir(), "shim.log")
			noErr(t, os.WriteFile(log, nil, 0o600))
			t.Setenv("PATH", bin)
			t.Setenv("SHIM_LOG", log)

			prev := geminiFilePollInterval
			geminiFilePollInterval = time.Millisecond
			t.Cleanup(func() { geminiFilePollInterval = prev })
			used := make([]int, len(c.Responses))
			n := useNet(t, func(call netCall) netReply {
				for i, r := range c.Responses {
					if !strings.Contains(call.URL, r.Match) || (r.Method != "" && r.Method != call.Method) || (r.Times != nil && used[i] >= *r.Times) {
						continue
					}
					used[i]++
					status := r.Status
					if status == 0 {
						status = 200
					}
					rep := netReply{Status: status, Body: r.Text}
					if r.JSON != nil {
						rep.Body = string(r.JSON)
					}
					if r.BytesBase64 != nil {
						b, _ := base64.StdEncoding.DecodeString(*r.BytesBase64)
						rep.Body = string(b)
					}
					if len(r.Headers) > 0 {
						rep.Header = http.Header{}
						for k, v := range r.Headers {
							rep.Header.Set(k, v)
						}
					}
					return rep
				}
				return netReply{Err: errors.New("Unexpected fetch: " + call.URL)}
			})

			res := ExtractContent(bg(), strings.ReplaceAll(c.URL, "<root>", agentDir), ExtractOptions{
				Prompt: c.Options.Prompt, Model: c.Options.Model, Frames: c.Options.Frames, Timestamp: c.Options.Timestamp, Lookup: publicLookup,
			})
			pendingGeminiDeletes.Wait()

			got := toAny(t, res)
			b, _ := json.Marshal(got)
			s := strings.ReplaceAll(string(b), agentDir, "<root>")
			var normalized, want any
			noErr(t, json.Unmarshal([]byte(jsonParseFailure.ReplaceAllString(s, "$1")), &normalized))
			wb, _ := json.Marshal(c.Result)
			noErr(t, json.Unmarshal([]byte(jsonParseFailure.ReplaceAllString(string(wb), "$1")), &want))
			if !reflect.DeepEqual(normalized, want) {
				t.Fatalf("result\n got %s\nwant %s", s, wb)
			}

			logged, _ := os.ReadFile(log)
			var commands [][]string
			for _, line := range strings.Split(strings.TrimSpace(string(logged)), "\n") {
				if line == "" {
					continue
				}
				args := strings.Split(line, "\x1f")[1:]
				for i := range args {
					args[i] = strings.ReplaceAll(args[i], agentDir, "<root>")
				}
				commands = append(commands, args)
			}
			if len(commands) != len(c.Commands) || (len(commands) > 0 && !reflect.DeepEqual(commands, c.Commands)) {
				t.Fatalf("commands\n got %q\nwant %q", commands, c.Commands)
			}

			if len(n.calls) != len(c.Requests) {
				t.Fatalf("%d requests, the original sent %d: %v", len(n.calls), len(c.Requests), n.urls())
			}
			for i, want := range c.Requests {
				got := n.calls[i]
				if got.Method != want.Method || got.URL != want.URL {
					t.Fatalf("request %d: %s %s, want %s %s", i, got.Method, got.URL, want.Method, want.URL)
				}
				headers := map[string]string{}
				for k := range got.Header {
					headers[strings.ToLower(k)] = got.Header.Get(k)
				}
				if !reflect.DeepEqual(headers, orEmpty(want.Headers)) {
					t.Fatalf("request %d headers %v, want %v", i, headers, want.Headers)
				}
				if want.Body == nil {
					if got.Body != "" {
						t.Fatalf("request %d body %q", i, got.Body)
					}
					continue
				}
				if !reflect.DeepEqual(wireBody(t, got.Body), recordedAssertion(t, *want.Body)) {
					t.Fatalf("request %d body\n got %s\nwant %s", i, got.Body, *want.Body)
				}
			}
		})
	}
}
