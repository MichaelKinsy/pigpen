package main

// Twins of the src/index.ts behavior (terminal login entrypoint, stdio wiring, shutdown) and of
// test/unit/stdout-destroyed-does-not-crash.test.ts at the process level.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain doubles as the fake pig: with PIGACP_FAKE_PIG set it prints its arguments and exits
// with the requested code.
func TestMain(m *testing.M) {
	if code := os.Getenv("PIGACP_FAKE_PIG"); code != "" {
		fmt.Printf("fake-pig args=%q\n", os.Args[1:])
		var n int
		fmt.Sscanf(code, "%d", &n)
		os.Exit(n)
	}
	root, _ := os.MkdirTemp("", "pigacp-main-*")
	for _, k := range []string{"HOME", "PIG_HOME", "PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR", "XDG_CONFIG_HOME"} {
		os.Setenv(k, filepath.Join(root, k))
	}
	for _, k := range []string{"PIG_ACP_PIG_COMMAND", "PI_ACP_PI_COMMAND", "PIG_USE_PI_DIRS"} {
		os.Unsetenv(k)
	}
	code := m.Run()
	os.RemoveAll(root)
	os.Exit(code)
}

func fakePig(t *testing.T, exit int) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "pig")
	body := fmt.Sprintf("#!/bin/sh\nPIGACP_FAKE_PIG=%d exec %q \"$@\"\n", exit, exe)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

func TestVersionAndUsage(t *testing.T) {
	t.Run("--version names the tool and the pinned ACP protocol version", func(t *testing.T) {
		var out, errw bytes.Buffer
		if code := run([]string{"--version"}, strings.NewReader(""), &out, &errw); code != 0 {
			t.Fatalf("exit %d: %s", code, errw.String())
		}
		if !strings.HasPrefix(out.String(), "pig-acp ") || !strings.Contains(out.String(), "ACP protocol version 1") {
			t.Errorf("out = %q", out.String())
		}
	})
	t.Run("--help lists the flags and the environment variables", func(t *testing.T) {
		var out bytes.Buffer
		if code := run([]string{"--help"}, strings.NewReader(""), &out, io.Discard); code != 0 {
			t.Fatalf("exit %d", code)
		}
		for _, want := range []string{"--pig", "--piglet", "--pig-arg", "--terminal-login", "PIG_ACP_PIG_COMMAND", "PIG_ACP_ENABLE_EMBEDDED_CONTEXT"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("help lacks %q:\n%s", want, out.String())
			}
		}
	})
	t.Run("an unknown flag exits 2 with a message on stderr and nothing on stdout", func(t *testing.T) {
		var out, errw bytes.Buffer
		if code := run([]string{"--bogus"}, strings.NewReader(""), &out, &errw); code != 2 || out.Len() != 0 || !strings.Contains(errw.String(), "--bogus") {
			t.Errorf("code=%d out=%q err=%q", code, out.String(), errw.String())
		}
	})
}

func TestTerminalLogin(t *testing.T) {
	t.Run("--terminal-login starts pig interactively and returns its exit status", func(t *testing.T) {
		var out, errw bytes.Buffer
		code := run([]string{"--pig", fakePig(t, 3), "--terminal-login"}, strings.NewReader(""), &out, &errw)
		if code != 3 {
			t.Fatalf("exit %d, stderr %s", code, errw.String())
		}
		if !strings.Contains(out.String(), "fake-pig args=[]") {
			t.Errorf("pig was not started without arguments: %q", out.String())
		}
	})
	t.Run("--terminal-login with a missing pig explains how to install it and exits 1", func(t *testing.T) {
		var errw bytes.Buffer
		code := run([]string{"--pig", "pig-does-not-exist-12345", "--terminal-login"}, strings.NewReader(""), io.Discard, &errw)
		if code != 1 || !strings.Contains(errw.String(), "pig-acp: could not start pig (command not found: pig-does-not-exist-12345)") {
			t.Errorf("code=%d stderr=%q", code, errw.String())
		}
	})
}

func TestPigCommandSelection(t *testing.T) {
	t.Run("--pig beats PIG_ACP_PIG_COMMAND beats PI_ACP_PI_COMMAND", func(t *testing.T) {
		a, b, c := fakePig(t, 11), fakePig(t, 12), fakePig(t, 13)
		var out bytes.Buffer
		t.Setenv("PI_ACP_PI_COMMAND", c)
		t.Setenv("PIG_ACP_PIG_COMMAND", b)
		if code := run([]string{"--pig", a, "--terminal-login"}, strings.NewReader(""), &out, io.Discard); code != 11 {
			t.Errorf("flag: exit %d", code)
		}
		if code := run([]string{"--terminal-login"}, strings.NewReader(""), &out, io.Discard); code != 12 {
			t.Errorf("PIG_ACP_PIG_COMMAND: exit %d", code)
		}
		t.Setenv("PIG_ACP_PIG_COMMAND", "")
		if code := run([]string{"--terminal-login"}, strings.NewReader(""), &out, io.Discard); code != 13 {
			t.Errorf("PI_ACP_PI_COMMAND: exit %d", code)
		}
	})
	t.Run("--piglet and --pig-arg are forwarded to the pig child after the RPC flags", func(t *testing.T) {
		// Checked through the launch arguments the terminal login does not use; see pirpc.BuildArgs.
		got := buildPigArgs([]string{"--piglet", "acp"}, "")
		want := []string{"--mode", "rpc", "--no-themes", "--piglet", "acp"}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("args = %v, want %v", got, want)
		}
	})
}

func TestServing(t *testing.T) {
	t.Run("serves ACP on stdin and stdout and exits 0 when stdin closes", func(t *testing.T) {
		req, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": 1}})
		var out bytes.Buffer
		code := run(nil, strings.NewReader(string(req)+"\n"), &out, io.Discard)
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		var resp map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
			t.Fatalf("stdout %q is not one JSON line: %v", out.String(), err)
		}
		if resp["id"] != float64(1) || resp["result"].(map[string]any)["protocolVersion"] != float64(1) {
			t.Errorf("response = %v", resp)
		}
	})
	t.Run("writes nothing but protocol messages to stdout", func(t *testing.T) {
		var out, errw bytes.Buffer
		run(nil, strings.NewReader("garbage\n"), &out, &errw)
		if out.Len() != 0 {
			t.Errorf("stdout has %q", out.String())
		}
	})
	t.Run("stdout writer: a destroyed stdout ends the run with exit 0 and no crash", func(t *testing.T) {
		req, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": 1}})
		pr, pw := io.Pipe()
		pr.Close()
		done := make(chan int, 1)
		go func() { done <- run(nil, strings.NewReader(string(req)+"\n"), pw, io.Discard) }()
		select {
		case code := <-done:
			if code != 0 {
				t.Errorf("exit %d", code)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("run blocked on a destroyed stdout")
		}
	})
}

func TestPigletAndPigArgReachPig(t *testing.T) {
	t.Run("--piglet and --pig-arg are the arguments pig is started with", func(t *testing.T) {
		var out bytes.Buffer
		code := run([]string{"--pig", fakePig(t, 0), "--piglet", "acp", "--pig-arg", "-e", "--pig-arg", "/x y", "--terminal-login"}, strings.NewReader(""), &out, io.Discard)
		if code != 0 || !strings.Contains(out.String(), `args=["--piglet" "acp" "-e" "/x y"]`) {
			t.Errorf("code=%d out=%q", code, out.String())
		}
	})
}

// The auth method the editor runs for "sign in" must start the same agent the sessions use: a
// session started with --piglet and --pig-arg must not get a plain-pig login. (Found by review: the
// advertised arguments kept only --pig.)
func TestTerminalAuthMethodKeepsTheAgentSelection(t *testing.T) {
	req, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
		"protocolVersion": 1, "clientCapabilities": map[string]any{"_meta": map[string]any{"terminal-auth": true}}}})
	var out bytes.Buffer
	args := []string{"--pig", "/opt/pig", "--piglet", "acp", "--pig-arg", "-e", "--pig-arg", "/x y"}
	if code := run(args, strings.NewReader(string(req)+"\n"), &out, io.Discard); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var resp struct {
		Result struct {
			AuthMethods []struct {
				Args []string `json:"args"`
				Meta struct {
					TerminalAuth struct {
						Args []string `json:"args"`
					} `json:"terminal-auth"`
				} `json:"_meta"`
			} `json:"authMethods"`
		} `json:"result"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil || len(resp.Result.AuthMethods) != 1 {
		t.Fatalf("response %q: %v", out.String(), err)
	}
	want := `["--pig" "/opt/pig" "--piglet" "acp" "--pig-arg" "-e" "--pig-arg" "/x y" "--terminal-login"]`
	m := resp.Result.AuthMethods[0]
	if got := fmt.Sprintf("%q", m.Args); got != want {
		t.Errorf("args = %s, want %s", got, want)
	}
	if got := fmt.Sprintf("%q", m.Meta.TerminalAuth.Args); got != want {
		t.Errorf("_meta terminal-auth args = %s, want %s", got, want)
	}
}
