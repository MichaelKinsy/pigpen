package pirpc

// Twins of test/unit/pi-rpc-request-timeout.test.ts, pi-rpc-session-path.test.ts,
// thinking-level-rpc.test.ts and pi-command.test.ts.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestMain doubles as the fake pi executable of pi-rpc-session-path.test.ts (fake-pi.cjs): the
// test binary re-executed. It appends "started" to the --session file, answers every command
// with that path as sessionFile, and exits shortly after the first one.
func TestMain(m *testing.M) {
	if os.Getenv("PIRPC_FAKE_PI") == "1" {
		fakePi()
		return
	}
	root, _ := os.MkdirTemp("", "pirpc-test-*")
	for _, k := range []string{"HOME", "PIG_HOME", "PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR"} {
		os.Setenv(k, filepath.Join(root, k))
	}
	code := m.Run()
	os.RemoveAll(root)
	os.Exit(code)
}

func fakePi() {
	var sessionPath string
	for i, a := range os.Args {
		if a == "--session" && i+1 < len(os.Args) {
			sessionPath = os.Args[i+1]
		}
	}
	if sessionPath != "" {
		f, err := os.OpenFile(sessionPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		f.WriteString("started\n")
		f.Close()
	}
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		var c map[string]any
		_ = json.Unmarshal(sc.Bytes(), &c)
		out, _ := json.Marshal(map[string]any{"type": "response", "id": c["id"], "command": c["type"], "success": true, "data": map[string]any{"sessionFile": sessionPath}})
		fmt.Println(string(out))
		time.Sleep(20 * time.Millisecond)
		os.Exit(0)
	}
}

// fakeChild is makeFakeChild(): pipes standing in for the child's stdio.
type fakeChild struct {
	stdoutW *io.PipeWriter
	mu      sync.Mutex
	written []string
	proc    *Process
}

type recordingWriter struct{ c *fakeChild }

func (w recordingWriter) Write(p []byte) (int, error) {
	w.c.mu.Lock()
	w.c.written = append(w.c.written, string(p))
	w.c.mu.Unlock()
	return len(p), nil
}
func (w recordingWriter) Close() error { return nil }

func newFakeChild() *fakeChild {
	r, w := io.Pipe()
	c := &fakeChild{stdoutW: w}
	c.proc = NewProcess(recordingWriter{c}, r, func() {})
	return c
}

func (c *fakeChild) sent(t *testing.T, n int) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		if len(c.written) > n {
			line := c.written[n]
			c.mu.Unlock()
			var m map[string]any
			if err := json.Unmarshal([]byte(line), &m); err != nil {
				t.Fatalf("written %q: %v", line, err)
			}
			return m
		}
		c.mu.Unlock()
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("no request was written")
	return nil
}

func (c *fakeChild) send(msg any) {
	b, _ := json.Marshal(msg)
	done := make(chan struct{})
	go func() { c.stdoutW.Write(append(b, '\n')); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		c.stdoutW.CloseWithError(errors.New("nobody reads the child's stdout"))
		panic("the process did not read its child's stdout")
	}
	time.Sleep(20 * time.Millisecond) // the JS test waits two setImmediate turns
}

func TestRequestTimeout(t *testing.T) {
	tw(t, "unit/pi-rpc-request-timeout", "PiRpcProcess: auxiliary context usage has a one-second timeout", func(t *testing.T) {
		if SessionStatsTimeoutMs != 1000 {
			t.Errorf("SessionStatsTimeoutMs = %d", SessionStatsTimeoutMs)
		}
	})

	tw(t, "unit/pi-rpc-request-timeout", "PiRpcProcess: getSessionStats without a timeout accepts a response after one second", func(t *testing.T) {
		c := newFakeChild()
		type res struct {
			s   map[string]any
			err error
		}
		done := make(chan res, 1)
		go func() { s, err := c.proc.GetSessionStats(0); done <- res{s, err} }()
		sent := c.sent(t, 0)
		time.Sleep(time.Duration(SessionStatsTimeoutMs+50) * time.Millisecond)
		if c.proc.PendingCount() != 1 {
			t.Fatalf("pending = %d", c.proc.PendingCount())
		}
		c.send(map[string]any{"type": "response", "id": sent["id"], "command": "get_session_stats", "success": true, "data": map[string]any{"totalMessages": 42}})
		r := <-done
		if r.err != nil || !reflect.DeepEqual(r.s, map[string]any{"totalMessages": float64(42)}) {
			t.Fatalf("got %v, %v", r.s, r.err)
		}
		if c.proc.PendingCount() != 0 {
			t.Errorf("pending = %d", c.proc.PendingCount())
		}
	})

	tw(t, "unit/pi-rpc-request-timeout", "PiRpcProcess: request timeout rejects, clears pending, and swallows the late response", func(t *testing.T) {
		c := newFakeChild()
		var mu sync.Mutex
		var events []map[string]any
		c.proc.OnEvent(func(e map[string]any) { mu.Lock(); events = append(events, e); mu.Unlock() })
		_, err := c.proc.GetSessionStats(5)
		if err == nil || !strings.Contains(err.Error(), "pi get_session_stats timed out after 5ms") {
			t.Fatalf("err = %v", err)
		}
		if c.proc.PendingCount() != 0 {
			t.Errorf("pending = %d", c.proc.PendingCount())
		}
		sent := c.sent(t, 0)
		if sent["type"] != "get_session_stats" {
			t.Errorf("sent = %v", sent)
		}
		c.send(map[string]any{"type": "response", "id": sent["id"], "command": "get_session_stats", "success": true, "data": map[string]any{"contextUsage": map[string]any{"tokens": 1, "contextWindow": 2}}})
		mu.Lock()
		defer mu.Unlock()
		if len(events) != 0 || c.proc.PendingCount() != 0 {
			t.Errorf("events=%v pending=%d", events, c.proc.PendingCount())
		}
	})

	tw(t, "unit/pi-rpc-request-timeout", "PiRpcProcess: response before the timeout resolves and clears pending", func(t *testing.T) {
		c := newFakeChild()
		var mu sync.Mutex
		var events []map[string]any
		c.proc.OnEvent(func(e map[string]any) { mu.Lock(); events = append(events, e); mu.Unlock() })
		type res struct {
			s   map[string]any
			err error
		}
		done := make(chan res, 1)
		go func() { s, err := c.proc.GetSessionStats(5000); done <- res{s, err} }()
		sent := c.sent(t, 0)
		c.send(map[string]any{"type": "response", "id": sent["id"], "command": "get_session_stats", "success": true, "data": map[string]any{"contextUsage": map[string]any{"tokens": 10, "contextWindow": 100}}})
		r := <-done
		want := map[string]any{"contextUsage": map[string]any{"tokens": float64(10), "contextWindow": float64(100)}}
		if r.err != nil || !reflect.DeepEqual(r.s, want) {
			t.Fatalf("got %v, %v", r.s, r.err)
		}
		mu.Lock()
		defer mu.Unlock()
		if c.proc.PendingCount() != 0 || len(events) != 0 {
			t.Errorf("pending=%d events=%v", c.proc.PendingCount(), events)
		}
	})

	tw(t, "unit/pi-rpc-request-timeout", "PiRpcProcess: responses with unknown ids are dropped while real events are delivered", func(t *testing.T) {
		c := newFakeChild()
		var mu sync.Mutex
		var events []map[string]any
		c.proc.OnEvent(func(e map[string]any) { mu.Lock(); events = append(events, e); mu.Unlock() })
		c.send(map[string]any{"type": "response", "id": "nope", "command": "get_state", "success": true, "data": map[string]any{}})
		c.send(map[string]any{"type": "response", "command": "get_state", "success": true, "data": map[string]any{}})
		c.send(map[string]any{"type": "agent_start"})
		mu.Lock()
		defer mu.Unlock()
		if !reflect.DeepEqual(events, []map[string]any{{"type": "agent_start"}}) {
			t.Errorf("events = %v", events)
		}
	})
}

func TestSessionPath(t *testing.T) {
	launcher := func(t *testing.T, root, name string) string {
		t.Helper()
		if runtime.GOOS == "windows" {
			t.Skip("the fake launcher is a POSIX shell script")
		}
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		// The launcher is a shell script that re-executes the test binary as the fake pi.
		p := filepath.Join(root, name)
		script := fmt.Sprintf("#!/bin/sh\nPIRPC_FAKE_PI=1 exec %q \"$@\"\n", exe)
		if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}

	tw(t, "unit/pi-rpc-session-path", "PiRpcProcess passes distinct session paths intact to the pi launcher", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "pi-acp launcher ")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PI_ACP_PATH_TEST", "expanded")
		sessionDir := filepath.Join(root, "project & (a) ^ 100% !PI_ACP_PATH_TEST! %PI_ACP_PATH_TEST%")
		if err := os.MkdirAll(sessionDir, 0o755); err != nil {
			t.Fatal(err)
		}
		l := launcher(t, root, "pi")
		for _, name := range []string{"first", "second"} {
			path := filepath.Join(sessionDir, "pi-"+name+".jsonl")
			p, err := Spawn(root, l, path)
			if err != nil {
				t.Fatal(err)
			}
			p.Dispose()
			b, err := os.ReadFile(path)
			if err != nil || string(b) != "started\n" {
				t.Errorf("%s: %q, %v", name, b, err)
			}
		}
		if _, err := os.Stat(filepath.Join(root, "project")); !os.IsNotExist(err) {
			t.Error("the session path was split at a shell metacharacter")
		}
	})

	tskip(t, "unit/pi-rpc-session-path", "PiRpcProcess keeps a simulated Windows cmd session path intact",
		"gap: the original fakes process.platform = 'win32' to force its cross-spawn path. Go cannot fake runtime.GOOS; a real .cmd launcher needs Windows. ShouldUseShell is covered with an explicit goos argument, and Windows execution is checked with GOOS=windows go vet only (see PORT.md).")
}

func TestSpawnError(t *testing.T) {
	t.Run("PiAcpAgent: newSession returns a helpful Internal error when pi is not installed (spawn layer)", func(t *testing.T) {
		_, err := Spawn(t.TempDir(), "pi-does-not-exist-12345", "")
		var se *SpawnError
		if !errors.As(err, &se) || se.Code != "ENOENT" || !strings.Contains(strings.ToLower(se.Message), "executable not found") {
			t.Fatalf("err = %v", err)
		}
	})
}

func thinkingRPC(response Response, err error) *Process {
	p := &Process{}
	p.requestFn = func(cmd map[string]any, _ time.Duration) (Response, error) {
		if !reflect.DeepEqual(cmd, map[string]any{"type": "get_available_thinking_levels"}) {
			panic(fmt.Sprintf("unexpected command %v", cmd))
		}
		return response, err
	}
	return p
}

func TestThinkingLevelRPC(t *testing.T) {
	for _, levels := range [][]string{{"low", "high", "max"}, {"off"}, {"max", "low"}, {"ordinary", "mean"}, {" mean ", " "}} {
		tw(t, "unit/thinking-level-rpc", "thinking RPC preserves returned levels ${levels}", func(t *testing.T) {
			t.Run(fmt.Sprintf("thinking RPC preserves returned levels %v", levels), func(t *testing.T) {
				var raw []any
				for _, l := range levels {
					raw = append(raw, l)
				}
				got, err := thinkingRPC(Response{Success: true, Data: map[string]any{"levels": raw}}, nil).GetAvailableThinkingLevels()
				if err != nil || !reflect.DeepEqual(got, levels) {
					t.Fatalf("got %q, %v", got, err)
				}
			})
		})
	}
	for _, data := range []any{nil, nil, map[string]any{}, map[string]any{"levels": []any{}}, map[string]any{"levels": "high"},
		map[string]any{"levels": []any{"high", ""}}, map[string]any{"levels": []any{"high", nil}}, map[string]any{"levels": []any{1}}} {
		b, _ := json.Marshal(data)
		tw(t, "unit/thinking-level-rpc", "thinking RPC rejects malformed payload ${JSON.stringify(data)}", func(t *testing.T) {
			t.Run("thinking RPC rejects malformed payload "+string(b), func(t *testing.T) {
				_, err := thinkingRPC(Response{Success: true, Data: data}, nil).GetAvailableThinkingLevels()
				if err == nil || !strings.Contains(err.Error(), "invalid levels") {
					t.Fatalf("err = %v", err)
				}
			})
		})
	}
	tw(t, "unit/thinking-level-rpc", "thinking RPC propagates unsuccessful responses and transport rejection", func(t *testing.T) {
		_, err := thinkingRPC(Response{Success: false, Error: "unsupported command"}, nil).GetAvailableThinkingLevels()
		if err == nil || !strings.Contains(err.Error(), "unsupported command") {
			t.Errorf("err = %v", err)
		}
		_, err = thinkingRPC(Response{}, errors.New("connection closed")).GetAvailableThinkingLevels()
		if err == nil || !strings.Contains(err.Error(), "connection closed") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestPiCommand(t *testing.T) {
	tw(t, "unit/pi-command", "defaultPiCommand: uses pi.cmd on Windows and pi elsewhere", func(t *testing.T) {
		// The port's launcher is pig; the Windows launcher of an npm-style install is pig.cmd.
		if DefaultCommand("windows") != "pig.cmd" || DefaultCommand("darwin") != "pig" {
			t.Errorf("windows=%q darwin=%q", DefaultCommand("windows"), DefaultCommand("darwin"))
		}
	})
	tw(t, "unit/pi-command", "shouldUseShellForPiCommand: enables shell for Windows cmd launchers only", func(t *testing.T) {
		for cmd, want := range map[string]bool{"pig.cmd": true, `C:\Users\me\AppData\Roaming\npm\pig.CMD`: true, "pig.bat": true, "pig": false, `C:\tools\pig.exe`: false} {
			if got := ShouldUseShell("windows", cmd); got != want {
				t.Errorf("%q: got %v, want %v", cmd, got, want)
			}
		}
	})
	tw(t, "unit/pi-command", "shouldUseShellForPiCommand: keeps shell disabled on non-Windows", func(t *testing.T) {
		if ShouldUseShell("darwin", "pig.cmd") || ShouldUseShell("darwin", "pig") {
			t.Error("shell enabled on darwin")
		}
	})
	t.Run("getPiCommand: an override wins", func(t *testing.T) {
		if Command("/opt/pig") != "/opt/pig" {
			t.Error("override ignored")
		}
	})
}
