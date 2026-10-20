package rpiv_web_tools_test

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	rpiv_web_tools "github.com/MichaelKinsy/pigpen/rpiv-web-tools"
)

// The interceptor through a real host: commands go out as host `exec` calls, the config is read when the
// extension registers, and `/web-tools --show` reports the active interceptor.

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func ghConfig(t *testing.T, h string, cfg string) {
	t.Helper()
	dir := filepath.Join(h, ".config", "rpiv-web-tools")
	os.MkdirAll(dir, 0o755)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
}

// toolText is the text of a tool result.
func toolText(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var r struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &r); err != nil || r.Content == "" {
		return string(raw)
	}
	return r.Content
}

func TestInterceptorShowLines(t *testing.T) {
	const f = "index"
	tw(t, f, "--show lists 'github: enabled' with token + clonePath when opted in", func(t *testing.T) {
		h := home(t)
		t.Cleanup(rpiv_web_tools.ResetInterceptors)
		t.Setenv("GITHUB_TOKEN", "ghp_abcdefgh1234")
		ghConfig(t, h, `{"interceptors":{"github":true}}`)
		r := startRig(t, HostOptions{})
		r.Host.Command("web-tools", "--show")
		msg, _ := lastNotify(r)
		for _, s := range []string{"URL interceptors:", "github: enabled", "GITHUB_TOKEN: ghp_", "clonePath:"} {
			if !strings.Contains(msg, s) {
				t.Fatalf("%q lacks %q", msg, s)
			}
		}
	})
	t.Run("the enabled line in full: masked token, the size limit and the clone path of the object form", func(t *testing.T) {
		h := home(t)
		t.Cleanup(rpiv_web_tools.ResetInterceptors)
		t.Setenv("GITHUB_TOKEN", "  ghp_abcdefgh1234  ")
		ghConfig(t, h, `{"interceptors":{"github":{"maxRepoSizeMB":12.5,"clonePath":"/srv/clones"}}}`)
		r := startRig(t, HostOptions{})
		r.Host.Command("web-tools", "--show")
		msg, _ := lastNotify(r)
		want := "URL interceptors:\n  github: enabled (GITHUB_TOKEN: ghp_...1234, maxRepoSizeMB: 12.5, clonePath: /srv/clones)"
		if !strings.HasSuffix(msg, want) {
			t.Fatalf("%q", msg)
		}
	})
	t.Run("without a token the line says so and the default clone path is under the temp directory", func(t *testing.T) {
		h := home(t)
		t.Cleanup(rpiv_web_tools.ResetInterceptors)
		os.Unsetenv("GITHUB_TOKEN")
		ghConfig(t, h, `{"interceptors":{"github":true}}`)
		r := startRig(t, HostOptions{})
		r.Host.Command("web-tools", "--show")
		msg, _ := lastNotify(r)
		want := "  github: enabled (GITHUB_TOKEN: (not set), maxRepoSizeMB: 350, clonePath: " + filepath.Join(os.TempDir(), "pi-github-repos") + ")"
		if !strings.HasSuffix(msg, want) {
			t.Fatalf("%q", msg)
		}
	})
	t.Run("a disabled object reports the interceptor as disabled", func(t *testing.T) {
		h := home(t)
		t.Cleanup(rpiv_web_tools.ResetInterceptors)
		ghConfig(t, h, `{"interceptors":{"github":{"enabled":false}}}`)
		r := startRig(t, HostOptions{})
		r.Host.Command("web-tools", "--show")
		msg, _ := lastNotify(r)
		if !strings.Contains(msg, "  github: disabled\n") {
			t.Fatal(msg)
		}
	})
	t.Run("the show line follows the config read at registration, not later edits", func(t *testing.T) {
		h := home(t)
		t.Cleanup(rpiv_web_tools.ResetInterceptors)
		r := startRig(t, HostOptions{})
		ghConfig(t, h, `{"interceptors":{"github":true}}`)
		r.Host.Command("web-tools", "--show")
		msg, _ := lastNotify(r)
		if !strings.Contains(msg, "github: disabled") {
			t.Fatal(msg)
		}
	})
}

// execLog answers the host's exec calls from a handler and records them.
type execLog struct {
	mu    sync.Mutex
	calls []map[string]any
}

func (l *execLog) on(handler func(command string, args []any) (stdout string, code int)) func(string, map[string]any) (map[string]any, string) {
	return func(method string, args map[string]any) (map[string]any, string) {
		if method != "exec" {
			return map[string]any{}, ""
		}
		l.mu.Lock()
		l.calls = append(l.calls, args)
		l.mu.Unlock()
		cmd, _ := args["command"].(string)
		list, _ := args["args"].([]any)
		out, code := handler(cmd, list)
		killed := code == -1 // -1 stands for a command the host killed (timeout or cancellation), exit code 0
		if killed {
			code = 0
		}
		return map[string]any{"stdout": out, "stderr": "", "code": code, "killed": killed}, ""
	}
}

func TestInterceptorThroughHost(t *testing.T) {
	page := rtFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Status: "OK", Header: http.Header{"Content-Type": {"text/html"}},
			Body: io.NopCloser(strings.NewReader("<html><body>the page</body></html>")), Request: r}, nil
	})
	t.Run("an API view goes out as exec calls with the timeouts and answers the tool", func(t *testing.T) {
		h := home(t)
		t.Cleanup(rpiv_web_tools.ResetInterceptors)
		ghConfig(t, h, `{"provider":"brave","interceptors":{"github":true}}`)
		t.Setenv("BRAVE_SEARCH_API_KEY", "k")
		defer rpiv_web_tools.SetTransport(page)()
		var log execLog
		host := StartHost(t, rpiv_web_tools.Extension(), HostOptions{OnCall: log.on(func(cmd string, args []any) (string, int) {
			joined := ""
			for _, a := range args {
				joined += a.(string) + " "
			}
			switch {
			case strings.HasPrefix(joined, "--version"):
				return "gh version 2", 0
			case strings.HasSuffix(joined, ".size "):
				return "999999\n", 0
			case strings.HasSuffix(joined, ".default_branch "):
				return "main\n", 0
			case strings.Contains(joined, "readme"):
				return "IyBIZWxsbw==\n", 0
			case strings.HasSuffix(joined, ".tree[].path "):
				return "a.go\nb.go\n", 0
			}
			return "", 1
		})})
		raw, failure := host.Tool("web_fetch", map[string]any{"url": "https://github.com/o/r"})
		if failure != "" {
			t.Fatal(failure)
		}
		text := toolText(t, raw)
		for _, s := range []string{"**Fetched:** https://github.com/o/r", "**Title:** o/r", "Note: Repository is 977MB (threshold: 350MB).", "## Structure\na.go\nb.go\n", "## README.md\n# Hello\n"} {
			if !strings.Contains(text, s) {
				t.Fatalf("%q lacks %q", text, s)
			}
		}
		log.mu.Lock()
		defer log.mu.Unlock()
		first := log.calls[0]
		if first["command"] != "gh" || first["options"].(map[string]any)["timeout"] != float64(5000) {
			t.Fatalf("%v", first)
		}
		second := log.calls[1]
		if second["options"].(map[string]any)["timeout"] != float64(10000) {
			t.Fatalf("%v", second)
		}
	})
	t.Run("a command that exits non-zero fails like a missing one and the page is fetched", func(t *testing.T) {
		h := home(t)
		t.Cleanup(rpiv_web_tools.ResetInterceptors)
		ghConfig(t, h, `{"provider":"brave","interceptors":{"github":true}}`)
		t.Setenv("BRAVE_SEARCH_API_KEY", "k")
		defer rpiv_web_tools.SetTransport(page)()
		var log execLog
		host := StartHost(t, rpiv_web_tools.Extension(), HostOptions{OnCall: log.on(func(string, []any) (string, int) { return "", 127 })})
		raw, failure := host.Tool("web_fetch", map[string]any{"url": "https://github.com/o/r/blob/main/x.go"})
		if failure != "" {
			t.Fatal(failure)
		}
		if text := toolText(t, raw); !strings.Contains(text, "the page") {
			t.Fatal(text)
		}
	})
	t.Run("a stdout past the buffer is a failure", func(t *testing.T) {
		h := home(t)
		t.Cleanup(rpiv_web_tools.ResetInterceptors)
		ghConfig(t, h, `{"provider":"brave","interceptors":{"github":true}}`)
		t.Setenv("BRAVE_SEARCH_API_KEY", "k")
		defer rpiv_web_tools.SetTransport(page)()
		var log execLog
		host := StartHost(t, rpiv_web_tools.Extension(), HostOptions{OnCall: log.on(func(cmd string, args []any) (string, int) {
			if args[0] == "--version" {
				return "gh", 0
			}
			return strings.Repeat("x", 2*1024*1024+1), 0 // the file read allows 2 MiB
		})})
		raw, _ := host.Tool("web_fetch", map[string]any{"url": "https://github.com/o/r/blob/abcdefabcdefabcdefabcdefabcdefabcdefabcd/x.go"})
		if text := toolText(t, raw); !strings.Contains(text, "the page") {
			t.Fatal(text[:200])
		}
	})
	t.Run("a killed command is a failure even with exit code 0", func(t *testing.T) {
		h := home(t)
		t.Cleanup(rpiv_web_tools.ResetInterceptors)
		ghConfig(t, h, `{"provider":"brave","interceptors":{"github":true}}`)
		t.Setenv("BRAVE_SEARCH_API_KEY", "k")
		defer rpiv_web_tools.SetTransport(page)()
		var log execLog
		host := StartHost(t, rpiv_web_tools.Extension(), HostOptions{OnCall: log.on(func(string, []any) (string, int) { return "gh", -1 })})
		raw, _ := host.Tool("web_fetch", map[string]any{"url": "https://github.com/o/r"})
		if text := toolText(t, raw); !strings.Contains(text, "the page") {
			t.Fatal(text)
		}
	})
	t.Run("a file read of exactly 2 MiB is accepted", func(t *testing.T) {
		h := home(t)
		t.Cleanup(rpiv_web_tools.ResetInterceptors)
		ghConfig(t, h, `{"provider":"brave","interceptors":{"github":true}}`)
		t.Setenv("BRAVE_SEARCH_API_KEY", "k")
		defer rpiv_web_tools.SetTransport(page)()
		var log execLog
		host := StartHost(t, rpiv_web_tools.Extension(), HostOptions{OnCall: log.on(func(cmd string, args []any) (string, int) {
			if args[0] == "--version" {
				return "gh", 0
			}
			return strings.Repeat("QQ==", 2*1024*1024/4), 0
		})})
		raw, _ := host.Tool("web_fetch", map[string]any{"url": "https://github.com/o/r/blob/abcdefabcdefabcdefabcdefabcdefabcdefabcd/x.go"})
		if text := toolText(t, raw); strings.Contains(text, "the page") || !strings.Contains(text, "## x.go") {
			t.Fatal(text[:200])
		}
	})
	t.Run("an output of more than 1 MiB fails a command with the default buffer", func(t *testing.T) {
		h := home(t)
		t.Cleanup(rpiv_web_tools.ResetInterceptors)
		ghConfig(t, h, `{"provider":"brave","interceptors":{"github":true}}`)
		t.Setenv("BRAVE_SEARCH_API_KEY", "k")
		defer rpiv_web_tools.SetTransport(page)()
		var log execLog
		host := StartHost(t, rpiv_web_tools.Extension(), HostOptions{OnCall: log.on(func(cmd string, args []any) (string, int) {
			if args[0] == "--version" {
				return strings.Repeat("x", 1024*1024+1), 0
			}
			return "", 1
		})})
		host.Tool("web_fetch", map[string]any{"url": "https://github.com/o/r"})
		log.mu.Lock()
		defer log.mu.Unlock()
		if len(log.calls) < 2 || log.calls[1]["command"] != "git" {
			t.Fatalf("gh was taken as available: %v", log.calls)
		}
	})
	t.Run("the interceptor is off by default: no command is run", func(t *testing.T) {
		h := home(t)
		t.Cleanup(rpiv_web_tools.ResetInterceptors)
		ghConfig(t, h, `{"provider":"brave"}`)
		t.Setenv("BRAVE_SEARCH_API_KEY", "k")
		defer rpiv_web_tools.SetTransport(page)()
		var log execLog
		host := StartHost(t, rpiv_web_tools.Extension(), HostOptions{OnCall: log.on(func(string, []any) (string, int) { return "", 0 })})
		raw, _ := host.Tool("web_fetch", map[string]any{"url": "https://github.com/o/r"})
		if text := toolText(t, raw); !strings.Contains(text, "the page") {
			t.Fatal(text)
		}
		eq2(t, len(log.calls), 0)
	})
}

func eq2(t *testing.T, got, want int) {
	t.Helper()
	if got != want {
		t.Fatalf("got %d, want %d", got, want)
	}
}
