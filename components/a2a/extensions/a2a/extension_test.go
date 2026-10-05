package a2aext_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"

	a2aext "github.com/MichaelKinsy/pigpen/a2a"
)

const (
	extTokenA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	extTokenB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

type echoWorker struct{ runs atomic.Int32 }

func (w *echoWorker) Run(ctx context.Context, t a2aext.Turn, up func(a2aext.Update)) (a2aext.Result, error) {
	w.runs.Add(1)
	up(a2aext.Update{Text: "pong: " + t.Prompt})
	return a2aext.Result{Text: "pong: " + t.Prompt}, nil
}

func mapEnv(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// hostWith starts the extension against the fake host with a config file and environment.
func hostWith(t *testing.T, config string, env map[string]string, w a2aext.Worker) *Host {
	t.Helper()
	dir := t.TempDir()
	if env == nil {
		env = map[string]string{}
	}
	if config != "" {
		path := filepath.Join(dir, "a2a.json")
		if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
		env["PIG_A2A_CONFIG"] = path
	}
	env["PIG_CODING_AGENT_DIR"] = dir
	return StartHost(t, a2aext.ExtensionWith(a2aext.Options{Getenv: mapEnv(env), Worker: w}), HostOptions{Mode: "rpc"})
}

func notifications(h *Host) []string {
	var out []string
	for _, c := range h.CallsTo("ui.notify") {
		out = append(out, c.Args["level"].(string)+": "+c.Args["message"].(string))
	}
	return out
}

var listenRE = regexp.MustCompile(`127\.0\.0\.1:\d+`)

func listeningAddr(t *testing.T, h *Host) string {
	t.Helper()
	h.Command("a2a", "")
	for _, n := range notifications(h) {
		if m := listenRE.FindString(n); m != "" && strings.Contains(n, "listening") {
			return m
		}
	}
	t.Fatalf("no listening address in %v", notifications(h))
	return ""
}

func canDial(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

func TestRegistersToolsCommandAndLifecycleHandlers(t *testing.T) {
	h := hostWith(t, "", nil, nil)
	for _, tool := range []string{"a2a_agents", "a2a_send", "a2a_task"} {
		if !h.tools[tool] {
			t.Errorf("tool %s is not registered", tool)
		}
	}
	if !h.cmds["a2a"] {
		t.Error("command /a2a is not registered")
	}
	for _, ev := range []string{"session_start", "session_shutdown"} {
		if !h.Registered(ev) {
			t.Errorf("no %s handler", ev)
		}
	}
}

func TestNoConfigurationOpensNoListener(t *testing.T) {
	h := hostWith(t, "", nil, nil)
	h.Fire("session_start", map[string]any{"reason": "startup"})
	h.Command("a2a", "")
	got := strings.Join(notifications(h), "\n")
	if !strings.Contains(got, "off") || strings.Contains(got, "listening") {
		t.Fatalf("status %q", got)
	}
}

func TestConfiguredListenerServesAndShutdownStopsIt(t *testing.T) {
	w := &echoWorker{}
	h := hostWith(t, `{"listen":"127.0.0.1:0","tokens":[{"name":"ci","tokenEnv":"CI_TOKEN"}]}`, map[string]string{"CI_TOKEN": extTokenA}, w)
	h.Fire("session_start", map[string]any{"reason": "startup"})
	addr := listeningAddr(t, h)
	resp, err := http.Get("http://" + addr + "/.well-known/agent-card.json")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("card: %v %v", resp, err)
	}
	resp.Body.Close()
	hc := &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		r.Header.Set("Authorization", "Bearer "+extTokenA)
		return http.DefaultTransport.RoundTrip(r)
	})}
	c, err := a2aclient.NewFromEndpoints(context.Background(), []*a2a.AgentInterface{a2a.NewAgentInterface("http://"+addr, a2a.TransportProtocolJSONRPC)}, a2aclient.WithJSONRPCTransport(hc))
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("ping"))})
	if err != nil {
		t.Fatal(err)
	}
	if task, ok := res.(*a2a.Task); !ok || task.Status.State != a2a.TaskStateCompleted || w.runs.Load() != 1 {
		t.Fatalf("%#v runs %d", res, w.runs.Load())
	}
	h.Fire("session_shutdown", map[string]any{"reason": "quit"})
	if canDial(addr) {
		t.Fatal("the listener survived session shutdown")
	}
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSessionSwitchKeepsTheListenerReloadRestartsIt(t *testing.T) {
	h := hostWith(t, `{"listen":"127.0.0.1:0","insecureNoAuth":true}`, nil, &echoWorker{})
	h.Fire("session_start", map[string]any{"reason": "startup"})
	addr := listeningAddr(t, h)
	for _, reason := range []string{"new", "resume", "fork"} {
		h.Fire("session_shutdown", map[string]any{"reason": reason})
		if !canDial(addr) {
			t.Fatalf("a %s session switch must not drop the A2A listener", reason)
		}
		h.Fire("session_start", map[string]any{"reason": reason})
		if !canDial(addr) {
			t.Fatalf("listener gone after %s", reason)
		}
	}
	h.Fire("session_shutdown", map[string]any{"reason": "reload"})
	if canDial(addr) {
		t.Fatal("reload must stop the listener so new configuration applies")
	}
}

func TestInvalidConfigurationIsReportedNotFatal(t *testing.T) {
	h := hostWith(t, `{"listen":"0.0.0.0:9999"}`, nil, nil)
	h.Fire("session_start", map[string]any{"reason": "startup"}) // a handler error would fail the test
	got := strings.Join(notifications(h), "\n")
	if !strings.Contains(got, "error:") || !strings.Contains(got, "a2a") {
		t.Fatalf("the user must be told why the listener is off: %q", got)
	}
	if canDial("127.0.0.1:9999") {
		t.Fatal("must not listen with invalid configuration")
	}
}

func TestWorkerChildNeverListens(t *testing.T) {
	h := hostWith(t, `{"listen":"127.0.0.1:0","insecureNoAuth":true}`, map[string]string{"PIG_A2A_WORKER": "1"}, &echoWorker{})
	h.Fire("session_start", map[string]any{"reason": "startup"})
	h.Command("a2a", "")
	if got := strings.Join(notifications(h), "\n"); strings.Contains(got, "listening") {
		t.Fatalf("a worker child opened a listener: %q", got)
	}
}

// remote starts this package's own server as the peer the tools talk to.
func remote(t *testing.T) (*a2aext.Server, string) {
	t.Helper()
	cfg := a2aext.Config{Listen: "127.0.0.1:0", Name: "peer-pig", MaxConcurrentTasks: 2, TaskTimeoutSeconds: 30,
		Tokens: []a2aext.TokenConfig{{Name: "caller", TokenEnv: "PEER_TOKEN"}}}
	s, err := a2aext.NewServer(cfg, &echoWorker{}, mapEnv(map[string]string{"PEER_TOKEN": extTokenB}))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	})
	return s, "http://" + s.Addr()
}

func hostWithRemote(t *testing.T, url string) *Host {
	t.Helper()
	cfg, _ := json.Marshal(map[string]any{"remotes": map[string]any{"peer": map[string]any{"url": url, "bearerTokenEnv": "PEER_TOKEN"}}})
	return hostWith(t, string(cfg), map[string]string{"PEER_TOKEN": extTokenB}, nil)
}

func TestAgentsToolListsRemotesWithTheirCards(t *testing.T) {
	_, url := remote(t)
	h := hostWithRemote(t, url)
	raw, fail := h.Tool("a2a_agents", map[string]any{})
	if fail != "" {
		t.Fatal(fail)
	}
	if s := string(raw); !strings.Contains(s, "peer") || !strings.Contains(s, "peer-pig") {
		t.Fatalf("%s", s)
	}
}

func TestSendToolRunsARemoteTaskEndToEnd(t *testing.T) {
	_, url := remote(t)
	h := hostWithRemote(t, url)
	raw, fail := h.Tool("a2a_send", map[string]any{"agent": "peer", "message": "are you there"})
	if fail != "" {
		t.Fatal(fail)
	}
	s := string(raw)
	for _, want := range []string{"completed", "pong: are you there", "taskId", "contextId"} {
		if !strings.Contains(s, want) {
			t.Errorf("result lacks %q: %s", want, s)
		}
	}
}

func TestTaskToolGetsAndCancels(t *testing.T) {
	_, url := remote(t)
	h := hostWithRemote(t, url)
	raw, _ := h.Tool("a2a_send", map[string]any{"agent": "peer", "message": "first"})
	m := regexp.MustCompile(`taskId[\\"]*:\s*[\\"]*([0-9a-f-]{36})`).FindStringSubmatch(string(raw))
	if m == nil {
		t.Fatalf("no task id in %s", raw)
	}
	got, fail := h.Tool("a2a_task", map[string]any{"agent": "peer", "taskId": m[1], "action": "get"})
	if fail != "" || !strings.Contains(string(got), "completed") {
		t.Fatalf("%s %s", got, fail)
	}
	_, fail = h.Tool("a2a_task", map[string]any{"agent": "peer", "taskId": m[1], "action": "cancel"})
	if fail == "" {
		t.Fatal("cancelling a finished task is an error the model should see")
	}
}

func TestToolsRejectBadArguments(t *testing.T) {
	_, url := remote(t)
	h := hostWithRemote(t, url)
	for name, params := range map[string]map[string]any{
		"unknown agent":   {"agent": "nobody", "message": "x y"},
		"missing message": {"agent": "peer"},
		"empty message":   {"agent": "peer", "message": " "},
	} {
		want := map[string]string{"unknown agent": "unknown agent", "missing message": "message is required", "empty message": "must not be empty"}[name]
		if _, fail := h.Tool("a2a_send", params); !strings.Contains(fail, want) {
			t.Errorf("%s: want a tool error saying %q, got %q", name, want, fail)
		}
	}
	if _, fail := h.Tool("a2a_task", map[string]any{"agent": "peer", "taskId": "x", "action": "explode"}); fail == "" {
		t.Error("unknown action")
	}
}

func TestToolsWithoutRemotesSayWhy(t *testing.T) {
	h := hostWith(t, "", nil, nil)
	_, fail := h.Tool("a2a_send", map[string]any{"agent": "peer", "message": "x y"})
	if fail == "" || !strings.Contains(fail, "remotes") {
		t.Fatalf("%q", fail)
	}
}

// hostWithOptions is hostWith with host options (for example an OnCall that answers getFlag).
func hostWithOptions(t *testing.T, config string, env map[string]string, w a2aext.Worker, opts HostOptions) (*Host, string) {
	t.Helper()
	dir := t.TempDir()
	if env == nil {
		env = map[string]string{}
	}
	if config != "" {
		if err := os.WriteFile(filepath.Join(dir, "a2a.json"), []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	env["PIG_CODING_AGENT_DIR"] = dir
	if opts.Mode == "" {
		opts.Mode = "rpc"
	}
	return StartHost(t, a2aext.ExtensionWith(a2aext.Options{Getenv: mapEnv(env), Worker: w}), opts), dir
}

func TestConfigIsReadFromTheAgentDirectory(t *testing.T) { // agent-dir-ignored
	h, _ := hostWithOptions(t, `{"listen":"127.0.0.1:0","insecureNoAuth":true}`, nil, &echoWorker{}, HostOptions{})
	h.Fire("session_start", map[string]any{"reason": "startup"})
	if addr := listeningAddr(t, h); !canDial(addr) {
		t.Fatalf("%s", addr)
	}
}

func TestFlagOverridesTheConfigurationFile(t *testing.T) { // flag-not-read
	h, _ := hostWithOptions(t, `{"insecureNoAuth":true}`, nil, &echoWorker{}, HostOptions{
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			if method == "getFlag" && args["name"] == "a2a-listen" {
				return map[string]any{"value": "127.0.0.1:0"}, ""
			}
			return nil, ""
		}})
	h.Fire("session_start", map[string]any{"reason": "startup"})
	if addr := listeningAddr(t, h); !canDial(addr) {
		t.Fatalf("%s", addr)
	}
}

func TestReloadAppliesChangedConfiguration(t *testing.T) { // stop-does-not-reload-config
	h, dir := hostWithOptions(t, `{"remotes":{"first":{"url":"http://127.0.0.1:1"}}}`, nil, nil, HostOptions{})
	h.Fire("session_start", map[string]any{"reason": "startup"})
	h.Command("a2a", "")
	if got := strings.Join(notifications(h), "\n"); !strings.Contains(got, "remotes: first") {
		t.Fatalf("%q", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "a2a.json"), []byte(`{"remotes":{"second":{"url":"http://127.0.0.1:1"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	h.Fire("session_shutdown", map[string]any{"reason": "reload"})
	h.Fire("session_start", map[string]any{"reason": "reload"})
	before := len(notifications(h))
	h.Command("a2a", "")
	after := strings.Join(notifications(h)[before:], "\n")
	if !strings.Contains(after, "remotes: second") || strings.Contains(after, "first") {
		t.Fatalf("a reload must apply the new configuration: %q", after)
	}
}

func TestListenFailureIsReportedToTheUser(t *testing.T) { // start-failure-silent
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	h, _ := hostWithOptions(t, `{"listen":"`+ln.Addr().String()+`","insecureNoAuth":true}`, nil, &echoWorker{}, HostOptions{})
	h.Fire("session_start", map[string]any{"reason": "startup"})
	if got := strings.Join(notifications(h), "\n"); !strings.Contains(got, "error:") || !strings.Contains(got, "listen") {
		t.Fatalf("a busy port must be reported, not swallowed: %q", got)
	}
}

func TestStatusListsRemotes(t *testing.T) { // status-hides-remotes
	_, url := remote(t)
	h := hostWithRemote(t, url)
	h.Command("a2a", "")
	if got := strings.Join(notifications(h), "\n"); !strings.Contains(got, "remotes: peer") {
		t.Fatalf("%q", got)
	}
}

func TestToolAfterShutdownUsesTheRewrittenConfiguration(t *testing.T) { // stop-does-not-reload-config
	h, dir := hostWithOptions(t, `{"remotes":{"first":{"url":"http://127.0.0.1:1"}}}`, nil, nil, HostOptions{})
	h.Fire("session_start", map[string]any{"reason": "startup"})
	if raw, _ := h.Tool("a2a_agents", map[string]any{}); !strings.Contains(string(raw), "first") {
		t.Fatalf("%s", raw)
	}
	if err := os.WriteFile(filepath.Join(dir, "a2a.json"), []byte(`{"remotes":{"second":{"url":"http://127.0.0.1:1"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	h.Fire("session_shutdown", map[string]any{"reason": "reload"})
	raw, _ := h.Tool("a2a_agents", map[string]any{}) // no session_start in between
	if s := string(raw); !strings.Contains(s, "second") || strings.Contains(s, "first") {
		t.Fatalf("a shutdown must drop the cached configuration: %s", s)
	}
}

func TestSendToolPassesTheContextIDThrough(t *testing.T) { // send-drops-context
	_, url := remote(t)
	h := hostWithRemote(t, url)
	raw, fail := h.Tool("a2a_send", map[string]any{"agent": "peer", "message": "one", "contextId": "ctx-from-the-model"})
	if fail != "" {
		t.Fatal(fail)
	}
	if !strings.Contains(string(raw), "ctx-from-the-model") {
		t.Fatalf("the caller's contextId must reach the remote and come back: %s", raw)
	}
}
