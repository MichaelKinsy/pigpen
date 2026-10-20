package ahp

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/MichaelKinsy/pigpen/ahp/internal/tunnel"
	"github.com/MichaelKinsy/pigpen/ahp/internal/ws"
)

func sdkContext() sdk.Context { return sdk.Context{} }

// scripted is a devtunnel CLI that answers from a script and records what it was asked.
type scripted struct {
	mu        sync.Mutex
	responses []string
	calls     [][]string
}

func (s *scripted) run(args []string) (tunnel.CommandResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, append([]string(nil), args...))
	if len(s.responses) == 0 {
		return tunnel.CommandResult{}, os.ErrInvalid
	}
	out := s.responses[0]
	s.responses = s.responses[1:]
	zero := 0
	return tunnel.CommandResult{Status: &zero, Stdout: out}, nil
}

const loggedIn = `{"status":"Logged in"}`

func prepare(t *testing.T, responses []string, name string) (id string, calls [][]string, logs []string, err error) {
	t.Helper()
	s := &scripted{responses: responses}
	id, err = prepareTunnel(tunnel.NewCLI(s.run), name, func(line string) { logs = append(logs, line) })
	return id, s.calls, logs, err
}

var (
	listArgs       = []string{"list", "--all-labels", "_pi_ahp", "--all-labels", "vscode-server-launcher", "--json"}
	portListArgs   = []string{"port", "list", "a.usw2", "--json"}
	portCreateArgs = []string{"port", "create", "a.usw2", "-p", "31546", "--protocol", "http", "--json"}
	httpPort       = `{"ports":[{"portNumber":31546,"protocol":"http"}]}`
	reservedLabels = `["_pi_ahp","vscode-server-launcher","protocolv5"`
)

func TestPrepareTunnelCreatesTheFirstTunnel(t *testing.T) {
	id, calls, logs, err := prepare(t, []string{loggedIn, `{"warning":"No tunnels found."}`, `{"tunnel":{"tunnelId":"a.usw2"}}`, `{"ports":[]}`, `{}`}, "my box")
	if err != nil || id != "a.usw2" {
		t.Fatalf("id = %q, %v", id, err)
	}
	want := [][]string{
		{"user", "show", "--json"}, listArgs,
		{"create", "--json", "-l", "vscode-server-launcher", "-l", "protocolv5", "-l", "_pi_ahp", "-l", "mybox"},
		portListArgs, portCreateArgs,
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v", calls)
	}
	if !reflect.DeepEqual(logs, []string{"tunnel: a.usw2 (new, named mybox)"}) {
		t.Fatalf("logs = %v", logs)
	}
}

func TestPrepareTunnelReusesTheTunnel(t *testing.T) {
	listing := `{"tunnels":[{"tunnelId":"a.usw2","labels":` + reservedLabels + `,"devbox"]}]}`
	t.Run("without a name the label is left alone", func(t *testing.T) {
		id, calls, logs, err := prepare(t, []string{loggedIn, listing, httpPort}, "")
		if err != nil || id != "a.usw2" || len(calls) != 3 {
			t.Fatalf("id = %q, calls = %v, err = %v", id, calls, err)
		}
		if !reflect.DeepEqual(logs, []string{"tunnel: a.usw2 (reused)"}) {
			t.Fatalf("logs = %v", logs)
		}
	})
	t.Run("the same name is not a rename", func(t *testing.T) {
		_, calls, logs, err := prepare(t, []string{loggedIn, listing, httpPort}, "devbox")
		if err != nil || len(calls) != 3 || !reflect.DeepEqual(logs, []string{"tunnel: a.usw2 (reused)"}) {
			t.Fatalf("calls = %v, logs = %v, err = %v", calls, logs, err)
		}
	})
	t.Run("another name replaces the display label", func(t *testing.T) {
		_, calls, logs, err := prepare(t, []string{loggedIn, listing, `{}`, httpPort}, "new name!")
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"update", "a.usw2", "--add-labels", "newname", "--remove-labels", "devbox"}; !reflect.DeepEqual(calls[2], want) {
			t.Fatalf("rename call = %v", calls[2])
		}
		if !reflect.DeepEqual(logs, []string{"tunnel: a.usw2 (reused, renamed to newname)"}) {
			t.Fatalf("logs = %v", logs)
		}
	})
	t.Run("a tunnel without a display label gets one added", func(t *testing.T) {
		bare := `{"tunnels":[{"tunnelId":"a.usw2","labels":` + reservedLabels + `]}]}`
		_, calls, _, err := prepare(t, []string{loggedIn, bare, `{}`, httpPort}, "first")
		if err != nil || !reflect.DeepEqual(calls[2], []string{"update", "a.usw2", "--add-labels", "first"}) {
			t.Fatalf("calls = %v, err = %v", calls, err)
		}
	})
}

func TestPrepareTunnelIgnoresAnUnusableName(t *testing.T) {
	_, _, logs, err := prepare(t, []string{loggedIn, `{"tunnels":[]}`, `{"tunnel":{"tunnelId":"a.usw2"}}`, httpPort}, "..")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"ignoring --name ..: nothing left after removing characters a label cannot hold",
		"tunnel: a.usw2 (new)",
	}
	if !reflect.DeepEqual(logs, want) {
		t.Fatalf("logs = %v", logs)
	}
}

func TestPrepareTunnelStopsAtTheFirstFailure(t *testing.T) {
	for name, c := range map[string]struct {
		responses []string
		want      string
		calls     int
	}{
		"logged out":      {[]string{`{"status":"Not logged in"}`}, "not logged in", 1},
		"listing":         {[]string{loggedIn, `{"nope":1}`}, "devtunnel list returned an unexpected response", 2},
		"creation":        {[]string{loggedIn, `{"tunnels":[]}`, `{}`}, "devtunnel create returned an unexpected response", 3},
		"port inspection": {[]string{loggedIn, `{"tunnels":[]}`, `{"tunnel":{"tunnelId":"a.usw2"}}`, `{}`}, "devtunnel port list returned an unexpected response", 4},
	} {
		_, calls, _, err := prepare(t, c.responses, "")
		if err == nil || !strings.Contains(err.Error(), c.want) || len(calls) != c.calls {
			t.Errorf("%s: err = %v, calls = %v", name, err, calls)
		}
	}
}

// helperHost substitutes the `devtunnel host` command with a shell script.
func helperHost(t *testing.T, script string) {
	t.Helper()
	if goruntime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	previous := hostCommand
	hostCommand = func(id string) *exec.Cmd { return exec.Command("sh", "-c", script, "devtunnel-host", id) }
	t.Cleanup(func() { hostCommand = previous })
}

func TestHostTunnelForwardsOutputAndStopsTheProcess(t *testing.T) {
	helperHost(t, `echo "Hosting $1"; echo "err line" 1>&2; exec sleep 30`)
	var mu sync.Mutex
	var lines []string
	exited := make(chan string, 1)
	hosted, err := hostTunnel("a.usw2", func(line string) { mu.Lock(); lines = append(lines, line); mu.Unlock() }, func(f string) { exited <- f })
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		mu.Lock()
		n := len(lines)
		mu.Unlock()
		if n == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("lines = %v", lines)
		}
		time.Sleep(5 * time.Millisecond)
	}
	start := time.Now()
	hosted.stop()
	if time.Since(start) > 4*time.Second {
		t.Fatal("stop must end a child that takes SIGTERM promptly")
	}
	select {
	case f := <-exited:
		t.Fatalf("a stopped tunnel is not an exit to report: %q", f)
	case <-time.After(100 * time.Millisecond):
	}
	mu.Lock()
	defer mu.Unlock()
	joined := strings.Join(lines, "|")
	if !strings.Contains(joined, "Hosting a.usw2") || !strings.Contains(joined, "err line") {
		t.Fatalf("lines = %v", lines)
	}
}

func TestHostTunnelReportsAnUnexpectedExit(t *testing.T) {
	helperHost(t, `exit 3`)
	exited := make(chan string, 1)
	if _, err := hostTunnel("a.usw2", func(string) {}, func(f string) { exited <- f }); err != nil {
		t.Fatal(err)
	}
	select {
	case f := <-exited:
		if f != "devtunnel host exited (3); shutting down" {
			t.Fatalf("failure = %q", f)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("no exit report")
	}
}

func TestHostTunnelKillsAChildThatIgnoresTheStopRequest(t *testing.T) {
	previousTimeout := hostStopTimeout
	hostStopTimeout = 300 * time.Millisecond
	defer func() { hostStopTimeout = previousTimeout }()
	helperHost(t, `trap '' TERM; echo ready; while :; do sleep 1; done`)
	ready := make(chan struct{}, 1)
	hosted, err := hostTunnel("a.usw2", func(string) {
		select {
		case ready <- struct{}{}:
		default:
		}
	}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	<-ready
	done := make(chan struct{})
	go func() { hosted.stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("stop did not return for a child that ignores SIGTERM")
	}
}

func TestHostTunnelWithoutTheCLI(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	previous := hostCommand
	hostCommand = func(id string) *exec.Cmd { return exec.Command("devtunnel", "host", id) }
	defer func() { hostCommand = previous }()
	if _, err := hostTunnel("a.usw2", func(string) {}, func(string) {}); err == nil || !strings.Contains(err.Error(), "not found on PATH") {
		t.Fatalf("error = %v", err)
	}
}

func TestStopEndsTheTunnelBeforeTheListener(t *testing.T) {
	helperHost(t, `exec sleep 30`)
	hosted, err := hostTunnel("a.usw2", func(string) {}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	rt := newRuntime()
	rt.tunnel, rt.tunnelID = hosted, "a.usw2"
	if !rt.stop() {
		t.Fatal("stopping a running tunnel reports that something was stopped")
	}
	select {
	case <-hosted.done:
	default:
		t.Fatal("stop must wait for devtunnel host to exit")
	}
	if rt.stop() {
		t.Fatal("a second stop has nothing to stop")
	}
	rt.tunnelStarting = true
	if !rt.stop() || rt.tunnelStarting {
		t.Fatal("stop cancels a tunnel that is still being set up")
	}
}

func TestTunnelRefusesWhileAListenerRuns(t *testing.T) {
	rt := newRuntime()
	rt.tunnelStarting = true
	if err := rt.startTunnel(sdkContext(), ""); err == nil || !strings.Contains(err.Error(), "already starting") {
		t.Fatalf("error = %v", err)
	}
	if got := rt.status(); got != "AHP dev tunnel is starting" {
		t.Fatalf("status = %q", got)
	}
	if _, err := rt.start(sdkContext()); err == nil || !strings.Contains(err.Error(), "still starting") {
		t.Fatalf("/ahp start during tunnel setup: %v", err)
	}
	rt.tunnelStarting = false
	rt.server = new(ws.Server)
	if err := rt.startTunnel(sdkContext(), ""); err == nil || !strings.Contains(err.Error(), "listener is already running") {
		t.Fatalf("a tunnel beside the direct listener: %v", err)
	}
	rt.server = nil
	if err := rt.startTunnel(sdkContext(), ""); err == nil || !strings.Contains(err.Error(), "no PiG session") {
		t.Fatalf("error = %v", err)
	}
}

func TestTunnelAccessIsOptional(t *testing.T) {
	dir := t.TempDir()
	if access, err := loadTunnelAccess(filepath.Join(dir, "none.json")); err != nil || access.Filesystem.Enabled || access.Terminals.Enabled {
		t.Fatalf("a missing file grants nothing: %+v, %v", access, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "none.json")); err == nil {
		t.Fatal("the tunnel must not create the settings file")
	}
	path := filepath.Join(dir, "s.json")
	if err := os.WriteFile(path, []byte(`{"port":1,"filesystem":{"enabled":true},"allowedOrigins":["https://x"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	access, err := loadTunnelAccess(path)
	if err != nil || !access.Filesystem.Enabled || !reflect.DeepEqual(access.AllowedOrigins, []string{"https://x"}) {
		t.Fatalf("access = %+v, %v", access, err)
	}
	if err := os.WriteFile(path, []byte(`{"filesystem":3}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadTunnelAccess(path); err == nil {
		t.Fatal("a malformed settings file is an error, not a grant")
	}
}

func TestTunnelNameArgument(t *testing.T) {
	for in, want := range map[string]string{"": "", "devbox": "devbox", "--name devbox": "devbox", "--name   my box ": "my box", "my box": "my box"} {
		if got, err := tunnelName(strings.TrimSpace(in)); err != nil || got != want {
			t.Errorf("tunnelName(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := tunnelName("--name"); err == nil {
		t.Error("--name without a value is a usage error")
	}
}
