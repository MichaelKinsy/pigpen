package debug

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/gate"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/toolcmd"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

// TestHelperProcess acts as a fake DAP adapter subprocess when invoked by tests
// with GO_WANT_DAP_HELPER=<mode>. Modes:
//   - "stdio": strict fake DAP adapter over stdin/stdout (rejects configurationDone before launch/attach)
//   - "tcp": strict fake DAP adapter listening on argv[0] port (prints banner); also accepts child TCP connections
//   - "tcp_child": TCP server that initiates a startDebugging reverse request and serves child sessions on the same port
//   - "socket_client_addr": strict fake DAP adapter dialing --client-addr=HOST:PORT
//   - "socket_unix_delayed": creates --listen=unix:PATH after 100ms and serves strict DAP
//   - "wedged_socket": writes pid to file and sleeps without creating socket / dialing back
//   - "malformed": writes a Content-Length header exceeding the framing limit
//   - "ghost_tcp": binds port, announces listening, accepts and closes after 30ms without answering
//   - "slow_continue": delays continue response by 30ms and never emits stopped (tests continue timeout)
//   - "self_exit_with_child": spawns a sleeping child in the same process group, writes child pid, then exits 0
//   - "exit_with_stderr_child": spawns a child inheriting stderr, prints stderr, then exits 42 immediately
func TestHelperProcess(t *testing.T) {
	mode := os.Getenv("GO_WANT_DAP_HELPER")
	if mode == "" {
		return
	}
	var args []string
	for i, a := range os.Args {
		if a == "--" {
			args = os.Args[i+1:]
			break
		}
	}
	switch mode {
	case "stdio":
		runFakeDapServer(os.Stdin, os.Stdout, fakeServerOpts{strictOrder: true})
		os.Exit(0)
	case "dlv_launch_fail":
		// Models real Delve (`dlv dap`) when launch fails (e.g. build error):
		// initialize succeeds WITHOUT emitting `initialized`, then launch
		// returns success=false and `initialized` is never sent.
		runFakeDapServer(os.Stdin, os.Stdout, fakeServerOpts{
			strictOrder:           true,
			deferInitUntilLaunch:  true,
			failLaunchWithoutInit: "Failed to launch",
		})
		os.Exit(0)
	case "slow_continue":
		runFakeDapServer(os.Stdin, os.Stdout, fakeServerOpts{
			strictOrder:         true,
			continueRespDelay:   30 * time.Millisecond,
			neverStopOnContinue: true,
		})
		os.Exit(0)
	case "tcp":
		if len(args) < 1 {
			os.Exit(2)
		}
		port := args[0]
		ln, err := net.Listen("tcp", "127.0.0.1:"+port)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "listen failed: %v\n", err)
			os.Exit(1)
		}
		defer func() { _ = ln.Close() }()
		_, _ = fmt.Fprintf(os.Stdout, "Debug server listening at 127.0.0.1:%s\n", port)
		conn, err := ln.Accept()
		if err != nil {
			os.Exit(1)
		}
		defer func() { _ = conn.Close() }()
		runFakeDapServer(conn, conn, fakeServerOpts{strictOrder: true})
		os.Exit(0)
	case "tcp_child":
		if len(args) < 1 {
			os.Exit(2)
		}
		port := args[0]
		ln, err := net.Listen("tcp", "127.0.0.1:"+port)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "listen failed: %v\n", err)
			os.Exit(1)
		}
		defer func() { _ = ln.Close() }()
		_, _ = fmt.Fprintf(os.Stdout, "Debug server listening at 127.0.0.1:%s\n", port)
		var wg sync.WaitGroup
		connNum := 0
		for connNum < 2 {
			conn, err := ln.Accept()
			if err != nil {
				break
			}
			connNum++
			isRoot := connNum == 1
			wg.Add(1)
			go func(c net.Conn, root bool) {
				defer wg.Done()
				defer func() { _ = c.Close() }()
				if root {
					runFakeDapServer(c, c, fakeServerOpts{
						strictOrder:          true,
						emitChildOnLaunch:    true,
						skipRootStopOnLaunch: true,
					})
				} else {
					runFakeDapServer(c, c, fakeServerOpts{strictOrder: true})
				}
			}(conn, isRoot)
		}
		wg.Wait()
		os.Exit(0)
	case "socket_client_addr":
		var addr string
		for _, a := range args {
			if rest, ok := strings.CutPrefix(a, "--client-addr="); ok {
				addr = rest
			}
		}
		if addr == "" {
			os.Exit(2)
		}
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "dial failed: %v\n", err)
			os.Exit(1)
		}
		defer func() { _ = conn.Close() }()
		runFakeDapServer(conn, conn, fakeServerOpts{strictOrder: true})
		os.Exit(0)
	case "socket_unix_delayed":
		var sockPath string
		for _, a := range args {
			if rest, ok := strings.CutPrefix(a, "--listen=unix:"); ok {
				sockPath = rest
			}
		}
		if sockPath == "" {
			os.Exit(2)
		}
		time.Sleep(100 * time.Millisecond)
		ln, err := net.Listen("unix", sockPath)
		if err != nil {
			os.Exit(1)
		}
		defer func() { _ = ln.Close() }()
		conn, err := ln.Accept()
		if err != nil {
			os.Exit(1)
		}
		defer func() { _ = conn.Close() }()
		runFakeDapServer(conn, conn, fakeServerOpts{strictOrder: true})
		os.Exit(0)
	case "wedged_socket":
		if len(args) >= 1 {
			_ = os.WriteFile(args[0], []byte(strconv.Itoa(os.Getpid())), 0o644)
		}
		time.Sleep(60 * time.Second)
		os.Exit(0)
	case "malformed":
		_, _ = os.Stdout.Write([]byte("Content-Length: 999999999999\r\n\r\n{}"))
		time.Sleep(5 * time.Second)
		os.Exit(0)
	case "ghost_tcp":
		if len(args) < 1 {
			os.Exit(2)
		}
		port := args[0]
		ln, err := net.Listen("tcp", "127.0.0.1:"+port)
		if err != nil {
			os.Exit(1)
		}
		defer func() { _ = ln.Close() }()
		_, _ = fmt.Fprintf(os.Stdout, "Debug server listening at 127.0.0.1:%s\n", port)
		conn, err := ln.Accept()
		if err == nil {
			time.Sleep(30 * time.Millisecond)
			_ = conn.Close()
		}
		time.Sleep(10 * time.Second)
		os.Exit(0)
	case "self_exit_with_child":
		if len(args) < 1 {
			os.Exit(2)
		}
		childPidFile := args[0]
		exe, _ := os.Executable()
		child := exec.Command(exe, "-test.run=^TestHelperProcess$", "--")
		child.Env = append(os.Environ(), "GO_WANT_DAP_HELPER=sleep_child")
		child.Stdin = nil
		child.Stdout = nil
		child.Stderr = nil
		if err := child.Start(); err != nil {
			os.Exit(1)
		}
		_ = os.WriteFile(childPidFile, []byte(strconv.Itoa(child.Process.Pid)), 0o644)
		os.Exit(0)
	case "exit_with_stderr_child":
		if len(args) < 1 {
			os.Exit(2)
		}
		childPidFile := args[0]
		exe, _ := os.Executable()
		child := exec.Command(exe, "-test.run=^TestHelperProcess$", "--")
		child.Env = append(os.Environ(), "GO_WANT_DAP_HELPER=sleep_child")
		// Child inherits stderr pipe.
		child.Stderr = os.Stderr
		if err := child.Start(); err != nil {
			os.Exit(1)
		}
		_ = os.WriteFile(childPidFile, []byte(strconv.Itoa(child.Process.Pid)), 0o644)
		_, _ = fmt.Fprintln(os.Stderr, "adapter fatal crash")
		os.Exit(42)
	case "sleep_child":
		time.Sleep(60 * time.Second)
		os.Exit(0)
	default:
		os.Exit(2)
	}
}

type fakeServerOpts struct {
	strictOrder           bool
	deferInitUntilLaunch  bool
	failLaunchWithoutInit string
	continueRespDelay     time.Duration
	neverStopOnContinue   bool
	emitChildOnLaunch     bool
	skipRootStopOnLaunch  bool
}

// runFakeDapServer implements a strict DAP peer matching real adapters (such as
// gdb's launch.py deferral: configurationDone fails if launch/attach was not
// already received; launch/attach response is deferred until configurationDone).
func runFakeDapServer(r io.Reader, w io.Writer, opts fakeServerOpts) {
	framer := NewMessageFramer(nil)
	var writeMu sync.Mutex
	seq := 0
	send := func(msg any) {
		writeMu.Lock()
		defer writeMu.Unlock()
		frame, err := EncodeMessageFrame(msg)
		if err != nil {
			return
		}
		_, _ = w.Write(frame)
	}
	sendEvent := func(event string, body any) {
		seq++
		send(map[string]any{
			"seq":   seq,
			"type":  "event",
			"event": event,
			"body":  body,
		})
	}
	sendResp := func(reqSeq int, cmd string, success bool, body any, errMsg string) {
		seq++
		resp := map[string]any{
			"seq":         seq,
			"type":        "response",
			"request_seq": reqSeq,
			"success":     success,
			"command":     cmd,
		}
		if body != nil {
			resp["body"] = body
		}
		if errMsg != "" {
			resp["message"] = errMsg
		}
		send(resp)
	}
	sendReverseReq := func(cmd string, args any) {
		seq++
		send(map[string]any{
			"seq":       seq,
			"type":      "request",
			"command":   cmd,
			"arguments": args,
		})
	}

	program := ""
	currentLine := 10
	memStore := []byte("Hello, DAP world")
	var breakpoints []int
	paused := false
	pendingStartSeq := 0
	pendingStartCmd := ""
	buf := make([]byte, 16*1024)

	for {
		n, err := r.Read(buf)
		if n > 0 {
			_ = framer.Push(buf[:n])
			msgs, _ := framer.Drain(nil)
			for _, raw := range msgs {
				var env DapProtocolMessage
				if uerr := json.Unmarshal(raw, &env); uerr != nil {
					continue
				}
				if env.Type != "request" {
					continue
				}
				switch env.Command {
				case "initialize":
					sendResp(env.Seq, "initialize", true, map[string]any{
						"supportsConfigurationDoneRequest": true,
						"supportsFunctionBreakpoints":      true,
						"supportsInstructionBreakpoints":   true,
						"supportsDataBreakpoints":          true,
						"supportsDisassembleRequest":       true,
						"supportsReadMemoryRequest":        true,
						"supportsWriteMemoryRequest":       true,
						"supportsModulesRequest":           true,
						"supportsLoadedSourcesRequest":     true,
						"supportsTerminateRequest":         true,
					}, "")
					if !opts.deferInitUntilLaunch {
						// Emit initialized immediately after initialize response, just like
						// gdb, netcoredbg, and codelldb do — this catches any client that
						// sends configurationDone before launch/attach.
						sendEvent("initialized", map[string]any{})
					}
				case "launch":
					var la map[string]any
					_ = json.Unmarshal(env.Arguments, &la)
					program, _ = la["program"].(string)
					if opts.failLaunchWithoutInit != "" {
						sendResp(env.Seq, "launch", false, nil, opts.failLaunchWithoutInit)
						continue
					}
					if opts.deferInitUntilLaunch {
						// Delve emits `initialized` only after launch succeeds and responds
						// to launch immediately, then waits for configurationDone.
						sendEvent("initialized", map[string]any{})
						sendResp(env.Seq, "launch", true, map[string]any{}, "")
						continue
					}
					if opts.strictOrder {
						// Defer launch response until configurationDone (matches gdb/dap spec).
						pendingStartSeq = env.Seq
						pendingStartCmd = "launch"
					} else {
						sendResp(env.Seq, "launch", true, map[string]any{}, "")
						sendEvent("output", map[string]any{"category": "stdout", "output": "started " + filepath.Base(program) + "\n"})
						paused = true
						sendEvent("stopped", map[string]any{"reason": "entry", "threadId": 1, "allThreadsStopped": true})
					}
				case "attach":
					if opts.strictOrder {
						pendingStartSeq = env.Seq
						pendingStartCmd = "attach"
					} else {
						sendResp(env.Seq, "attach", true, map[string]any{}, "")
						paused = true
						sendEvent("stopped", map[string]any{"reason": "attach", "threadId": 1, "allThreadsStopped": true})
					}
				case "configurationDone":
					if opts.strictOrder && pendingStartSeq == 0 {
						// Mirror gdb's launch.py: reject configurationDone when no launch/attach arrived first.
						sendResp(env.Seq, "configurationDone", false, nil, "launch or attach not specified")
						continue
					}
					sendResp(env.Seq, "configurationDone", true, map[string]any{}, "")
					if pendingStartSeq != 0 {
						sSeq, sCmd := pendingStartSeq, pendingStartCmd
						pendingStartSeq, pendingStartCmd = 0, ""
						sendResp(sSeq, sCmd, true, map[string]any{}, "")
						if sCmd == "launch" {
							sendEvent("output", map[string]any{"category": "stdout", "output": "started " + filepath.Base(program) + "\n"})
							if opts.emitChildOnLaunch {
								sendReverseReq("startDebugging", map[string]any{
									"request":       "launch",
									"configuration": map[string]any{"name": "child.js", "program": program},
								})
							}
							if !opts.skipRootStopOnLaunch {
								paused = true
								sendEvent("stopped", map[string]any{"reason": "entry", "threadId": 1, "allThreadsStopped": true})
							}
						} else {
							paused = true
							sendEvent("stopped", map[string]any{"reason": "attach", "threadId": 1, "allThreadsStopped": true})
						}
					}
				case "setBreakpoints":
					var a struct {
						Source      DapSource             `json:"source"`
						Breakpoints []DapSourceBreakpoint `json:"breakpoints"`
					}
					_ = json.Unmarshal(env.Arguments, &a)
					if a.Source.Path == "" {
						sendResp(env.Seq, "setBreakpoints", false, nil, "source.path required")
						continue
					}
					breakpoints = nil
					out := make([]map[string]any, len(a.Breakpoints))
					for i, bp := range a.Breakpoints {
						breakpoints = append(breakpoints, bp.Line)
						out[i] = map[string]any{"id": i + 1, "verified": true, "line": bp.Line}
					}
					sendResp(env.Seq, "setBreakpoints", true, map[string]any{"breakpoints": out}, "")
				case "setFunctionBreakpoints":
					var a struct {
						Breakpoints []DapFunctionBreakpoint `json:"breakpoints"`
					}
					_ = json.Unmarshal(env.Arguments, &a)
					out := make([]map[string]any, len(a.Breakpoints))
					for i := range a.Breakpoints {
						out[i] = map[string]any{"id": i + 100, "verified": true}
					}
					sendResp(env.Seq, "setFunctionBreakpoints", true, map[string]any{"breakpoints": out}, "")
				case "setInstructionBreakpoints":
					var a struct {
						Breakpoints []DapInstructionBreakpoint `json:"breakpoints"`
					}
					_ = json.Unmarshal(env.Arguments, &a)
					out := make([]map[string]any, len(a.Breakpoints))
					for i, bp := range a.Breakpoints {
						out[i] = map[string]any{"id": i + 200, "verified": true, "instructionReference": bp.InstructionReference, "offset": bp.Offset}
					}
					sendResp(env.Seq, "setInstructionBreakpoints", true, map[string]any{"breakpoints": out}, "")
				case "dataBreakpointInfo":
					var a struct {
						Name string `json:"name"`
					}
					_ = json.Unmarshal(env.Arguments, &a)
					if a.Name == "" {
						sendResp(env.Seq, "dataBreakpointInfo", false, nil, "name required")
						continue
					}
					sendResp(env.Seq, "dataBreakpointInfo", true, map[string]any{
						"dataId":      "watch-" + a.Name,
						"description": "Watchpoint on " + a.Name,
						"accessTypes": []string{"read", "write", "readWrite"},
						"canPersist":  true,
					}, "")
				case "setDataBreakpoints":
					var a struct {
						Breakpoints []DapDataBreakpoint `json:"breakpoints"`
					}
					_ = json.Unmarshal(env.Arguments, &a)
					out := make([]map[string]any, len(a.Breakpoints))
					for i := range a.Breakpoints {
						out[i] = map[string]any{"id": i + 300, "verified": true}
					}
					sendResp(env.Seq, "setDataBreakpoints", true, map[string]any{"breakpoints": out}, "")
				case "continue":
					var a struct {
						ThreadID int `json:"threadId"`
					}
					_ = json.Unmarshal(env.Arguments, &a)
					if a.ThreadID <= 0 {
						sendResp(env.Seq, "continue", false, nil, "threadId required")
						continue
					}
					if opts.continueRespDelay > 0 {
						time.Sleep(opts.continueRespDelay)
					}
					sendResp(env.Seq, "continue", true, map[string]any{"allThreadsContinued": true}, "")
					sendEvent("output", map[string]any{"category": "stdout", "output": "continued\n"})
					if opts.neverStopOnContinue {
						paused = false
						continue
					}
					if len(breakpoints) > 0 {
						currentLine = breakpoints[0]
						paused = true
						sendEvent("stopped", map[string]any{"reason": "breakpoint", "threadId": 1})
					} else {
						sendEvent("exited", map[string]any{"exitCode": 0})
						sendEvent("terminated", map[string]any{})
					}
				case "next":
					currentLine++
					sendResp(env.Seq, "next", true, map[string]any{}, "")
					sendEvent("stopped", map[string]any{"reason": "step", "threadId": 1})
				case "stepIn":
					currentLine += 2
					sendResp(env.Seq, "stepIn", true, map[string]any{}, "")
					sendEvent("stopped", map[string]any{"reason": "step", "threadId": 1})
				case "stepOut":
					currentLine += 3
					sendResp(env.Seq, "stepOut", true, map[string]any{}, "")
					sendEvent("stopped", map[string]any{"reason": "step", "threadId": 1})
				case "pause":
					paused = true
					sendResp(env.Seq, "pause", true, map[string]any{}, "")
					sendEvent("stopped", map[string]any{"reason": "pause", "threadId": 1})
				case "threads":
					sendResp(env.Seq, "threads", true, map[string]any{
						"threads": []map[string]any{
							{"id": 1, "name": "main"},
							{"id": 2, "name": "worker"},
						},
					}, "")
				case "stackTrace":
					var a struct {
						ThreadID int `json:"threadId"`
					}
					_ = json.Unmarshal(env.Arguments, &a)
					if a.ThreadID <= 0 {
						sendResp(env.Seq, "stackTrace", false, nil, "threadId required")
						continue
					}
					srcPath := program
					if srcPath == "" {
						srcPath = "/tmp/app.go"
					}
					sendResp(env.Seq, "stackTrace", true, map[string]any{
						"stackFrames": []map[string]any{
							{
								"id":                          1000,
								"name":                        "main.compute",
								"source":                      map[string]any{"name": filepath.Base(srcPath), "path": srcPath},
								"line":                        currentLine,
								"column":                      1,
								"instructionPointerReference": "0x1000",
							},
						},
						"totalFrames": 1,
					}, "")
				case "scopes":
					var a struct {
						FrameID int `json:"frameId"`
					}
					_ = json.Unmarshal(env.Arguments, &a)
					if a.FrameID != 1000 {
						sendResp(env.Seq, "scopes", false, nil, fmt.Sprintf("unexpected frameId %d", a.FrameID))
						continue
					}
					sendResp(env.Seq, "scopes", true, map[string]any{
						"scopes": []map[string]any{
							{"name": "Locals", "presentationHint": "locals", "variablesReference": 500, "expensive": false},
						},
					}, "")
				case "variables":
					var a struct {
						VariablesReference int `json:"variablesReference"`
					}
					_ = json.Unmarshal(env.Arguments, &a)
					if a.VariablesReference != 500 {
						sendResp(env.Seq, "variables", false, nil, fmt.Sprintf("unexpected variablesReference %d", a.VariablesReference))
						continue
					}
					sendResp(env.Seq, "variables", true, map[string]any{
						"variables": []map[string]any{
							{"name": "x", "value": "42", "type": "int", "variablesReference": 0},
							{"name": "items", "value": "[]int{1, 2}", "type": "[]int", "variablesReference": 501},
						},
					}, "")
				case "evaluate":
					var a struct {
						Expression string `json:"expression"`
						Context    string `json:"context"`
					}
					_ = json.Unmarshal(env.Arguments, &a)
					if a.Expression == "" {
						sendResp(env.Seq, "evaluate", false, nil, "expression required")
						continue
					}
					res := "84"
					if a.Expression == "x + 1" {
						res = "43"
					}
					sendResp(env.Seq, "evaluate", true, map[string]any{
						"result":             res,
						"type":               "int",
						"variablesReference": 0,
					}, "")
				case "disassemble":
					var a struct {
						MemoryReference  string `json:"memoryReference"`
						InstructionCount int    `json:"instructionCount"`
					}
					_ = json.Unmarshal(env.Arguments, &a)
					if a.MemoryReference == "" || a.InstructionCount <= 0 {
						sendResp(env.Seq, "disassemble", false, nil, "invalid disassemble arguments")
						continue
					}
					sendResp(env.Seq, "disassemble", true, map[string]any{
						"instructions": []map[string]any{
							{
								"address":          a.MemoryReference,
								"instructionBytes": "48 89 e5",
								"instruction":      "mov rbp, rsp",
								"symbol":           "main.compute",
							},
						},
					}, "")
				case "readMemory":
					sendResp(env.Seq, "readMemory", true, map[string]any{
						"address": "0x2000",
						"data":    base64.StdEncoding.EncodeToString(memStore),
					}, "")
				case "writeMemory":
					var a struct {
						Data string `json:"data"`
					}
					_ = json.Unmarshal(env.Arguments, &a)
					dec, _ := base64.StdEncoding.DecodeString(a.Data)
					memStore = dec
					sendResp(env.Seq, "writeMemory", true, map[string]any{
						"bytesWritten": len(dec),
						"offset":       0,
					}, "")
				case "modules":
					sendResp(env.Seq, "modules", true, map[string]any{
						"modules": []map[string]any{
							{"id": 1, "name": "app", "path": "/tmp/app", "symbolStatus": "loaded", "addressRange": "0x1000-0x2000"},
						},
					}, "")
				case "loadedSources":
					sendResp(env.Seq, "loadedSources", true, map[string]any{
						"sources": []map[string]any{
							{"name": "main.go", "path": "/tmp/main.go"},
						},
					}, "")
				case "customPing":
					sendResp(env.Seq, "customPing", true, map[string]any{"pong": true, "paused": paused}, "")
				case "terminate":
					sendResp(env.Seq, "terminate", true, map[string]any{}, "")
				case "disconnect":
					sendResp(env.Seq, "disconnect", true, map[string]any{}, "")
					sendEvent("terminated", map[string]any{})
					return
				default:
					sendResp(env.Seq, env.Command, false, nil, "unknown command: "+env.Command)
				}
			}
		}
		if err != nil {
			return
		}
	}
}

func helperAdapter(t *testing.T, mode string, connectMode ConnectMode, extraArgs ...string) DapResolvedAdapter {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	t.Setenv("GO_WANT_DAP_HELPER", mode)
	args := append([]string{"-test.run=^TestHelperProcess$", "--"}, extraArgs...)
	return DapResolvedAdapter{
		Name:                    "fake-" + mode,
		Command:                 exe,
		ResolvedCommand:         exe,
		Args:                    args,
		ConnectMode:             connectMode,
		LaunchDefaults:          map[string]any{"request": "launch", "stopOnEntry": true},
		AttachDefaults:          map[string]any{"request": "attach"},
		AcceptsDirectoryProgram: false,
	}
}

// =============================================================================
// Fake in-memory dapClient for session-manager & multi-session tests
// =============================================================================

type recordedRequest struct {
	Command string
	Args    any
}

type fakeClientOpts struct {
	launchErr          string
	launchErrDelay     time.Duration
	attachErr          string
	attachErrDelay     time.Duration
	configDoneErr      string
	supportsConfigDone *bool
	deferInitialized   bool
	stopAfterLaunch    bool
	childConfig        map[string]any
	childRequest       string
	threads            []DapThread
	stopThreadID       int
	port               int
}

type fakeDapClient struct {
	adapter DapResolvedAdapter
	cwd     string
	opts    fakeClientOpts

	mu              sync.Mutex
	alive           bool
	disposed        bool
	exited          chan struct{}
	exitOnce        sync.Once
	requests        []recordedRequest
	eventHandlers   map[string][]DapEventHandler
	reverseHandlers map[string]DapReverseRequestHandler
	caps            *DapCapabilities
}

func newFakeDapClient(adapter DapResolvedAdapter, cwd string, opts fakeClientOpts) *fakeDapClient {
	if opts.childRequest == "" {
		opts.childRequest = "launch"
	}
	if opts.stopThreadID == 0 {
		opts.stopThreadID = 7
	}
	if opts.port == 0 {
		opts.port = 8123
	}
	return &fakeDapClient{
		adapter:         adapter,
		cwd:             cwd,
		opts:            opts,
		alive:           true,
		exited:          make(chan struct{}),
		eventHandlers:   make(map[string][]DapEventHandler),
		reverseHandlers: make(map[string]DapReverseRequestHandler),
	}
}

func (f *fakeDapClient) Adapter() DapResolvedAdapter    { return f.adapter }
func (f *fakeDapClient) Cwd() string                    { return f.cwd }
func (f *fakeDapClient) Port() int                      { return f.opts.port }
func (f *fakeDapClient) Exited() <-chan struct{}        { return f.exited }
func (f *fakeDapClient) Capabilities() *DapCapabilities { return f.caps }

func (f *fakeDapClient) IsAlive() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.alive
}

func (f *fakeDapClient) Initialize(_ context.Context, _ DapInitializeArguments, _ time.Duration) (*DapCapabilities, error) {
	supportsCD := true
	if f.opts.supportsConfigDone != nil {
		supportsCD = *f.opts.supportsConfigDone
	}
	f.caps = &DapCapabilities{SupportsConfigurationDoneRequest: supportsCD}
	if !f.opts.deferInitialized {
		// Emit synchronously so `initialized` is deterministically delivered
		// before Launch/Attach enters completeConfigurationHandshake.
		f.emit("initialized", map[string]any{})
	}
	return f.caps, nil
}

func (f *fakeDapClient) StartRequest(ctx context.Context, command string, args any, timeout time.Duration) (*PendingDapCall, error) {
	f.mu.Lock()
	f.requests = append(f.requests, recordedRequest{Command: command, Args: args})
	f.mu.Unlock()

	type res struct {
		body json.RawMessage
		err  error
	}
	ch := make(chan res, 1)
	go func() {
		body, err := f.executeRequest(command, args)
		ch <- res{body: body, err: err}
	}()
	return &PendingDapCall{
		await: func(waitCtx context.Context) (json.RawMessage, error) {
			select {
			case r := <-ch:
				return r.body, r.err
			case <-waitCtx.Done():
				return nil, mapContextError(waitCtx.Err())
			}
		},
	}, nil
}

func (f *fakeDapClient) SendRequest(ctx context.Context, command string, args any, timeout time.Duration) (json.RawMessage, error) {
	call, err := f.StartRequest(ctx, command, args, timeout)
	if err != nil {
		return nil, err
	}
	return call.Await(ctx)
}

func (f *fakeDapClient) executeRequest(command string, args any) (json.RawMessage, error) {
	if command == "launch" && f.opts.launchErr != "" {
		if f.opts.launchErrDelay > 0 {
			time.Sleep(f.opts.launchErrDelay)
		}
		return nil, errors.New(f.opts.launchErr)
	}
	if command == "attach" && f.opts.attachErr != "" {
		if f.opts.attachErrDelay > 0 {
			time.Sleep(f.opts.attachErrDelay)
		}
		return nil, errors.New(f.opts.attachErr)
	}
	if command == "configurationDone" && f.opts.configDoneErr != "" {
		return nil, errors.New(f.opts.configDoneErr)
	}
	if command == "launch" {
		if f.opts.childConfig != nil {
			go func() {
				_, _ = f.triggerReverse("startDebugging", map[string]any{
					"request":       f.opts.childRequest,
					"configuration": f.opts.childConfig,
				})
			}()
		} else if f.opts.stopAfterLaunch {
			tid := f.opts.stopThreadID
			go f.emit("stopped", map[string]any{"reason": "entry", "threadId": tid})
		}
	}
	if command == "threads" {
		ths := f.opts.threads
		if ths == nil {
			ths = []DapThread{{ID: 7, Name: "target.js"}}
		}
		raw, _ := json.Marshal(map[string]any{"threads": ths})
		return raw, nil
	}
	if command == "stackTrace" {
		raw, _ := json.Marshal(map[string]any{
			"stackFrames": []map[string]any{
				{"id": 70, "name": "main", "line": 2, "column": 1, "source": map[string]any{"path": "/tmp/target.js"}},
			},
		})
		return raw, nil
	}
	if strings.HasSuffix(command, "Breakpoints") {
		var count int
		if m, ok := args.(map[string]any); ok {
			b, _ := json.Marshal(m["breakpoints"])
			var arr []any
			_ = json.Unmarshal(b, &arr)
			count = len(arr)
		}
		bps := make([]map[string]any, count)
		for i := range count {
			bps[i] = map[string]any{"id": i, "verified": true}
		}
		raw, _ := json.Marshal(map[string]any{"breakpoints": bps})
		return raw, nil
	}
	return json.RawMessage(`{}`), nil
}

func (f *fakeDapClient) WaitForEvent(ctx context.Context, event string, predicate func(json.RawMessage) bool, timeout time.Duration) (json.RawMessage, error) {
	ch := make(chan json.RawMessage, 1)
	unsub := f.OnEvent(event, func(body json.RawMessage, _ DapEventMessage) {
		if predicate != nil && !predicate(body) {
			return
		}
		select {
		case ch <- body:
		default:
		}
	})
	defer unsub()
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case b := <-ch:
		return b, nil
	case <-ctx.Done():
		return nil, mapContextError(ctx.Err())
	case <-timer.C:
		return nil, fmt.Errorf("DAP event %s timed out", event)
	}
}

func (f *fakeDapClient) OnEvent(event string, handler DapEventHandler) func() {
	f.mu.Lock()
	f.eventHandlers[event] = append(f.eventHandlers[event], handler)
	f.mu.Unlock()
	return func() {}
}

func (f *fakeDapClient) OnReverseRequest(command string, handler DapReverseRequestHandler) func() {
	f.mu.Lock()
	f.reverseHandlers[command] = handler
	f.mu.Unlock()
	return func() {}
}

func (f *fakeDapClient) Dispose() error {
	f.mu.Lock()
	f.disposed = true
	f.alive = false
	f.mu.Unlock()
	f.exitOnce.Do(func() { close(f.exited) })
	return nil
}

func (f *fakeDapClient) emit(event string, body any) {
	raw, _ := json.Marshal(body)
	f.mu.Lock()
	hs := append([]DapEventHandler(nil), f.eventHandlers[event]...)
	f.mu.Unlock()
	msg := DapEventMessage{Seq: 1, Type: "event", Event: event, Body: raw}
	for _, h := range hs {
		h(raw, msg)
	}
}

func (f *fakeDapClient) triggerReverse(command string, args any) (any, error) {
	f.mu.Lock()
	h := f.reverseHandlers[command]
	f.mu.Unlock()
	if h == nil {
		return nil, fmt.Errorf("missing reverse handler for %s", command)
	}
	raw, _ := json.Marshal(args)
	return h(raw)
}

func (f *fakeDapClient) countRequests(cmd string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if r.Command == cmd {
			n++
		}
	}
	return n
}

func writeExecFile(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "#!/bin/sh\n"
	if runtime.GOOS == "windows" {
		content = "@echo off\r\n"
	}
	if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func isolateHome(t *testing.T) {
	t.Helper()
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	t.Setenv("PI_CONFIG_DIR", filepath.Join(h, ".omp"))
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(h, ".omp", "agent"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(h, ".claude"))
}

// =============================================================================
// Upstream ported tests: dap-config.test.ts & dap-config-precedence.test.ts
// =============================================================================

// upstream: packages/coding-agent/test/debug/dap-config.test.ts: "loads a custom adapter from dap.json and selects it by file extension"
func TestDapConfigCustomAdapterJSON(t *testing.T) {
	isolateHome(t)
	cwd := t.TempDir()
	_ = os.WriteFile(filepath.Join(cwd, "pom.xml"), []byte("<project />\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(cwd, "src"), 0o755)
	_ = os.WriteFile(filepath.Join(cwd, "src", "Main.java"), []byte("class Main {}\n"), 0o644)

	binPath := filepath.Join(cwd, "tools", "my-jvm")
	if runtime.GOOS == "windows" {
		binPath += ".cmd"
	}
	writeExecFile(t, binPath)

	cfg := map[string]any{
		"adapters": map[string]any{
			"custom-jvm": map[string]any{
				"command":        binPath,
				"args":           []string{"run", "debug-adapter"},
				"languages":      []string{"java", "kotlin"},
				"fileTypes":      []string{".java", ".kt"},
				"rootMarkers":    []string{"pom.xml", "build.gradle.kts"},
				"launchDefaults": map[string]any{"request": "launch", "mainClass": ""},
				"attachDefaults": map[string]any{"request": "attach", "host": "127.0.0.1"},
			},
		},
	}
	data, _ := json.Marshal(cfg)
	_ = os.WriteFile(filepath.Join(cwd, "dap.json"), data, 0o644)

	adapter := ResolveAdapter("custom-jvm", cwd)
	if adapter == nil {
		t.Fatalf("expected custom-jvm adapter to resolve")
	}
	if adapter.Name != "custom-jvm" || !slices.Equal(adapter.Args, []string{"run", "debug-adapter"}) {
		t.Fatalf("unexpected adapter: %+v", adapter)
	}
	sel := SelectLaunchAdapter(filepath.Join("src", "Main.java"), cwd, "", ProgramKindFile)
	if sel.Kind != "adapter" || sel.Adapter == nil || sel.Adapter.Name != "custom-jvm" {
		t.Fatalf("expected custom-jvm selection, got %+v", sel)
	}
}

// upstream: packages/coding-agent/test/debug/dap-config.test.ts: "merges partial user overrides over built-in adapters"
func TestDapConfigMergesPartialOverrides(t *testing.T) {
	isolateHome(t)
	cwd := t.TempDir()
	_ = os.WriteFile(filepath.Join(cwd, "script.py"), []byte("print('hi')\n"), 0o644)
	cfg := map[string]any{
		"adapters": map[string]any{
			"debugpy": map[string]any{
				"args":           []string{"-m", "debugpy.adapter", "--log-dir", ".debugpy-logs"},
				"launchDefaults": map[string]any{"justMyCode": false},
			},
		},
	}
	data, _ := json.Marshal(cfg)
	_ = os.WriteFile(filepath.Join(cwd, "dap.json"), data, 0o644)

	got := GetAdapterConfigs(cwd)["debugpy"]
	if got.Command != "python" {
		t.Fatalf("expected preserved command python, got %q", got.Command)
	}
	if !slices.Equal(got.Args, []string{"-m", "debugpy.adapter", "--log-dir", ".debugpy-logs"}) {
		t.Fatalf("unexpected args: %v", got.Args)
	}
	if !slices.Contains(got.FileTypes, ".py") {
		t.Fatalf("expected .py in fileTypes: %v", got.FileTypes)
	}
	if got.LaunchDefaults["request"] != "launch" || got.LaunchDefaults["justMyCode"] != false {
		t.Fatalf("unexpected launchDefaults: %v", got.LaunchDefaults)
	}
}

// upstream: packages/coding-agent/test/debug/dap-config.test.ts: "loads adapter config from project config directories and YAML"
func TestDapConfigProjectYAML(t *testing.T) {
	isolateHome(t)
	cwd := t.TempDir()
	_ = os.MkdirAll(filepath.Join(cwd, ".omp"), 0o755)
	_ = os.WriteFile(filepath.Join(cwd, "build.gradle.kts"), []byte("plugins {}\n"), 0o644)
	_ = os.WriteFile(filepath.Join(cwd, "Main.kt"), []byte("fun main() {}\n"), 0o644)
	binPath := filepath.Join(cwd, "kotlin-bin")
	if runtime.GOOS == "windows" {
		binPath += ".cmd"
	}
	writeExecFile(t, binPath)

	yamlContent := fmt.Sprintf(`adapters:
  yaml-kotlin:
    command: %q
    args:
      - run
      - kotlin-debug-adapter
    languages:
      - kotlin
    fileTypes:
      - .kt
    rootMarkers:
      - build.gradle.kts
    launchDefaults:
      request: launch
      projectRoot: .
`, binPath)
	_ = os.WriteFile(filepath.Join(cwd, ".omp", "dap.yaml"), []byte(yamlContent), 0o644)

	sel := SelectLaunchAdapter("Main.kt", cwd, "", ProgramKindFile)
	if sel.Kind != "adapter" || sel.Adapter == nil || sel.Adapter.Name != "yaml-kotlin" {
		t.Fatalf("expected yaml-kotlin, got %+v", sel)
	}
	if sel.Adapter.LaunchDefaults["projectRoot"] != "." {
		t.Fatalf("unexpected launchDefaults: %v", sel.Adapter.LaunchDefaults)
	}
}

// upstream: packages/coding-agent/test/dap-config-precedence.test.ts: "a cwd file overrides the project config dir field by field"
func TestDapConfigPrecedence(t *testing.T) {
	isolateHome(t)
	cwd := t.TempDir()
	_ = os.MkdirAll(filepath.Join(cwd, ".omp"), 0o755)
	_ = os.WriteFile(filepath.Join(cwd, ".omp", "dap.yaml"), []byte("omp-precedence-probe:\n  command: project-cmd\n  args: [from-project]\n"), 0o644)
	_ = os.WriteFile(filepath.Join(cwd, "dap.json"), []byte(`{"omp-precedence-probe":{"command":"cwd-cmd"}}`), 0o644)

	got := GetAdapterConfigs(cwd)["omp-precedence-probe"]
	if got.Command != "cwd-cmd" {
		t.Fatalf("expected cwd-cmd, got %q", got.Command)
	}
	if !slices.Equal(got.Args, []string{"from-project"}) {
		t.Fatalf("expected preserved args [from-project], got %v", got.Args)
	}
}

// upstream: packages/coding-agent/test/debug/dap-config.test.ts: "resolves relative adapter commands from the debug cwd"
func TestDapConfigRelativeCommand(t *testing.T) {
	isolateHome(t)
	cwd := t.TempDir()
	rel := "./tools/debug-adapter"
	if runtime.GOOS == "windows" {
		rel = `.\tools\debug-adapter.cmd`
	}
	absCmd := filepath.Join(cwd, "tools", "debug-adapter")
	if runtime.GOOS == "windows" {
		absCmd += ".cmd"
	}
	writeExecFile(t, absCmd)
	data, _ := json.Marshal(map[string]any{
		"adapters": map[string]any{
			"relative": map[string]any{
				"command":   rel,
				"fileTypes": []string{".rel"},
			},
		},
	})
	_ = os.WriteFile(filepath.Join(cwd, "dap.json"), data, 0o644)

	a := ResolveAdapter("relative", cwd)
	if a == nil || a.ResolvedCommand != absCmd {
		t.Fatalf("expected resolved command %q, got %+v", absCmd, a)
	}
}

// upstream: packages/coding-agent/test/debug/dap-config.test.ts: "ignores invalid custom adapters without discarding valid configs"
func TestDapConfigIgnoresInvalid(t *testing.T) {
	isolateHome(t)
	cwd := t.TempDir()
	data, _ := json.Marshal(map[string]any{
		"adapters": map[string]any{
			"missing-command": map[string]any{"fileTypes": []string{".bad"}},
			"valid":           map[string]any{"command": "go", "fileTypes": []string{".ok"}, "rootMarkers": []string{"."}},
		},
	})
	_ = os.WriteFile(filepath.Join(cwd, "dap.json"), data, 0o644)
	cfgs := GetAdapterConfigs(cwd)
	if _, ok := cfgs["missing-command"]; ok {
		t.Fatalf("expected missing-command to be ignored")
	}
	if cfgs["valid"].Command != "go" {
		t.Fatalf("expected valid adapter preserved")
	}
}

// upstream: packages/coding-agent/test/debug/dap-config.test.ts: "reports missing dlv for Go source instead of falling back to a native debugger"
// upstream: packages/coding-agent/test/debug/dap-config.test.ts: "reports missing dlv for Go package directories instead of selecting a native debugger"
func TestDapConfigMissingDlvGoSourceAndDir(t *testing.T) {
	isolateHome(t)
	cwd := t.TempDir()
	missingCmd := filepath.Join(cwd, "tools", "missing-dlv")
	_ = os.WriteFile(filepath.Join(cwd, "go.mod"), []byte("module example.com/app\n\ngo 1.22\n"), 0o644)
	writeExecFile(t, filepath.Join(cwd, "bin", "gdb"))
	data, _ := json.Marshal(map[string]any{"adapters": map[string]any{"dlv": map[string]any{"command": missingCmd}}})
	_ = os.WriteFile(filepath.Join(cwd, "dap.json"), data, 0o644)

	goFile := filepath.Join(cwd, "main.go")
	_ = os.WriteFile(goFile, []byte("package main\nfunc main() {}\n"), 0o644)

	selFile := SelectLaunchAdapter(goFile, cwd, "", ProgramKindFile)
	if selFile.Kind != "unavailable" || selFile.AdapterName != "dlv" || selFile.Command != missingCmd {
		t.Fatalf("expected unavailable dlv for file, got %+v", selFile)
	}

	pkgDir := filepath.Join(cwd, "cmd", "server")
	_ = os.MkdirAll(pkgDir, 0o755)
	selDir := SelectLaunchAdapter(pkgDir, cwd, "", ProgramKindDirectory)
	if selDir.Kind != "unavailable" || selDir.AdapterName != "dlv" || selDir.Command != missingCmd {
		t.Fatalf("expected unavailable dlv for dir, got %+v", selDir)
	}
}

// upstream: packages/coding-agent/test/debug/dap-config.test.ts: "prefers a nested module adapter over cwd and PATH for inferred launches"
// upstream: packages/coding-agent/test/debug/dap-config.test.ts: "uses a nested module adapter when dlv is requested explicitly"
// upstream: packages/coding-agent/test/debug/dap-config.test.ts: "prefers the session cwd adapter over PATH after a nested-root miss"
// upstream: packages/coding-agent/test/debug/dap-config.test.ts: "resolves a local dlv for Go workspaces rooted by go.work"
func TestDapConfigNestedModuleLocalBinResolution(t *testing.T) {
	isolateHome(t)
	oldLook := lookPathFn
	lookCalled := 0
	lookPathFn = func(file string) (string, error) {
		lookCalled++
		return "/global/" + file, nil
	}
	t.Cleanup(func() { lookPathFn = oldLook })

	cwd := t.TempDir()
	modRoot := filepath.Join(cwd, "services", "api")
	prog := filepath.Join(modRoot, "main.go")
	_ = os.MkdirAll(modRoot, 0o755)
	_ = os.WriteFile(filepath.Join(modRoot, "go.mod"), []byte("module example.com/api\n\ngo 1.22\n"), 0o644)
	_ = os.WriteFile(prog, []byte("package main\nfunc main() {}\n"), 0o644)
	nestedDlv := filepath.Join(modRoot, "bin", "dlv")
	writeExecFile(t, nestedDlv)
	_ = os.WriteFile(filepath.Join(cwd, "go.mod"), []byte("module example.com/repo\n\ngo 1.22\n"), 0o644)
	cwdDlv := filepath.Join(cwd, "bin", "dlv")
	writeExecFile(t, cwdDlv)

	sel := SelectLaunchAdapter(prog, cwd, "", ProgramKindFile)
	if sel.Kind != "adapter" || sel.Adapter.ResolvedCommand != nestedDlv || lookCalled != 0 {
		t.Fatalf("expected nestedDlv %q (lookCalled=0), got %+v (lookCalled=%d)", nestedDlv, sel, lookCalled)
	}

	selExplicit := SelectLaunchAdapter(prog, cwd, "dlv", ProgramKindFile)
	if selExplicit.Kind != "adapter" || selExplicit.Adapter.ResolvedCommand != nestedDlv || lookCalled != 0 {
		t.Fatalf("expected nestedDlv for explicit dlv, got %+v", selExplicit)
	}

	_ = os.Remove(nestedDlv)
	selCwd := SelectLaunchAdapter(prog, cwd, "", ProgramKindFile)
	if selCwd.Kind != "adapter" || selCwd.Adapter.ResolvedCommand != cwdDlv || lookCalled != 0 {
		t.Fatalf("expected cwdDlv %q, got %+v (lookCalled=%d)", cwdDlv, selCwd, lookCalled)
	}

	workCwd := t.TempDir()
	workPkg := filepath.Join(workCwd, "cmd", "worker")
	_ = os.MkdirAll(workPkg, 0o755)
	_ = os.WriteFile(filepath.Join(workCwd, "go.work"), []byte("go 1.22\nuse ./cmd/worker\n"), 0o644)
	workDlv := filepath.Join(workCwd, "bin", "dlv")
	writeExecFile(t, workDlv)
	selWork := SelectLaunchAdapter(workPkg, workCwd, "", ProgramKindDirectory)
	if selWork.Kind != "adapter" || selWork.Adapter.ResolvedCommand != workDlv {
		t.Fatalf("expected workDlv %q, got %+v", workDlv, selWork)
	}
}

// upstream: packages/coding-agent/test/debug/dap-config.test.ts: "re-resolves an adapter installed after an earlier miss"
func TestDapConfigReResolvesAfterEarlierMiss(t *testing.T) {
	isolateHome(t)
	cwd := t.TempDir()
	prog := filepath.Join(cwd, "main.go")
	cmdPath := filepath.Join(cwd, "tools", "dlv")
	if runtime.GOOS == "windows" {
		cmdPath += ".cmd"
	}
	_ = os.WriteFile(filepath.Join(cwd, "go.mod"), []byte("module example.com/cache\n\ngo 1.22\n"), 0o644)
	_ = os.WriteFile(prog, []byte("package main\nfunc main() {}\n"), 0o644)
	data, _ := json.Marshal(map[string]any{"adapters": map[string]any{"dlv": map[string]any{"command": cmdPath}}})
	_ = os.WriteFile(filepath.Join(cwd, "dap.json"), data, 0o644)

	sel1 := SelectLaunchAdapter(prog, cwd, "", ProgramKindFile)
	if sel1.Kind != "unavailable" || sel1.AdapterName != "dlv" || sel1.Command != cmdPath {
		t.Fatalf("expected unavailable before install, got %+v", sel1)
	}
	writeExecFile(t, cmdPath)
	sel2 := SelectLaunchAdapter(prog, cwd, "", ProgramKindFile)
	if sel2.Kind != "adapter" || sel2.Adapter == nil || sel2.Adapter.ResolvedCommand != cmdPath {
		t.Fatalf("expected resolved adapter after install, got %+v", sel2)
	}
}

// TestDapConfigDefaultsInsertionOrder verifies adapters iterate in defaults.json
// declaration order (gdb before lldb-dap before codelldb), not alphabetically.
func TestDapConfigDefaultsInsertionOrder(t *testing.T) {
	isolateHome(t)
	oldLook := lookPathFn
	lookPathFn = func(string) (string, error) { return "", exec.ErrNotFound }
	t.Cleanup(func() { lookPathFn = oldLook })

	cwd := t.TempDir()
	cFile := filepath.Join(cwd, "main.c")
	_ = os.WriteFile(cFile, []byte("int main() { return 0; }\n"), 0o644)
	selC := SelectLaunchAdapter(cFile, cwd, "", ProgramKindFile)
	if selC.Kind != "unavailable" || selC.AdapterName != "gdb" {
		t.Fatalf("expected defaults.json first match 'gdb' for .c, got %+v", selC)
	}

	zigFile := filepath.Join(cwd, "main.zig")
	_ = os.WriteFile(zigFile, []byte("pub fn main() void {}\n"), 0o644)
	selZig := SelectLaunchAdapter(zigFile, cwd, "", ProgramKindFile)
	if selZig.Kind != "unavailable" || selZig.AdapterName != "lldb-dap" {
		t.Fatalf("expected defaults.json first match 'lldb-dap' for .zig, got %+v", selZig)
	}

	// Verify GetAvailableAdapters returns defaults.json order when all resolve.
	lookPathFn = func(file string) (string, error) { return "/usr/bin/" + file, nil }
	avail := GetAvailableAdapters(cwd)
	if len(avail) < 5 || avail[0].Name != "gdb" || avail[1].Name != "lldb-dap" || avail[2].Name != "codelldb" || avail[3].Name != "debugpy" || avail[4].Name != "dlv" {
		var names []string
		for _, a := range avail {
			names = append(names, a.Name)
		}
		t.Fatalf("expected defaults.json order [gdb lldb-dap codelldb debugpy dlv ...], got %v", names)
	}
}

// =============================================================================
// Upstream ported tests: dap-launch-failures.test.ts
// =============================================================================

// upstream: packages/coding-agent/test/debug/dap-launch-failures.test.ts: "rejects pending DAP work and terminates an adapter with invalid framing"
func TestDapClientMalformedFraming(t *testing.T) {
	adapter := helperAdapter(t, "malformed", ConnectModeStdio)
	client, err := SpawnDapClient(context.Background(), SpawnOptions{Adapter: adapter, Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("SpawnDapClient: %v", err)
	}
	defer func() { _ = client.Dispose() }()

	reqDone := make(chan error, 1)
	go func() {
		_, rerr := client.SendRequest(context.Background(), "initialize", map[string]any{}, 10*time.Second)
		reqDone <- rerr
	}()
	evDone := make(chan error, 1)
	go func() {
		_, eerr := client.WaitForEvent(context.Background(), "stopped", nil, 10*time.Second)
		evDone <- eerr
	}()

	for _, ch := range []chan error{reqDone, evDone} {
		select {
		case err := <-ch:
			if err == nil || !strings.Contains(err.Error(), "Content-Length") {
				t.Fatalf("expected Content-Length framing error, got %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for framing error rejection")
		}
	}

	if _, err := client.SendRequest(context.Background(), "threads", nil, time.Second); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("expected not running error after framing disposal, got %v", err)
	}
	select {
	case <-client.Exited():
	case <-time.After(5 * time.Second):
		t.Fatal("malformed adapter remained alive")
	}
}

// upstream: packages/coding-agent/test/debug/dap-launch-failures.test.ts: "preserves adapter launchDefaults args when launch omits args"
func TestSessionPreservesLaunchDefaultsArgs(t *testing.T) {
	mgr := NewSessionManager()
	defer mgr.Shutdown(context.Background())

	adapter := DapResolvedAdapter{
		Name:           "lldb-dap",
		Command:        "lldb-dap",
		LaunchDefaults: map[string]any{"request": "launch", "args": []string{"--configured"}, "stopOnEntry": true},
		AttachDefaults: map[string]any{"request": "attach"},
		ConnectMode:    ConnectModeStdio,
	}
	fake := newFakeDapClient(adapter, t.TempDir(), fakeClientOpts{stopAfterLaunch: true})
	mgr.spawnFn = func(_ context.Context, _ SpawnOptions) (dapClient, error) { return fake, nil }

	_, err := mgr.Launch(context.Background(), DapLaunchSessionOptions{
		Adapter: adapter,
		Program: "/bin/echo",
		Cwd:     t.TempDir(),
	}, time.Second)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	var launchArgs map[string]any
	for _, r := range fake.requests {
		if r.Command == "launch" {
			launchArgs, _ = r.Args.(map[string]any)
		}
	}
	if launchArgs == nil {
		t.Fatal("launch request not recorded")
	}
	argsSlice, _ := launchArgs["args"].([]string)
	if !slices.Equal(argsSlice, []string{"--configured"}) {
		t.Fatalf("expected preserved launchDefaults args [--configured], got %#v", launchArgs["args"])
	}
}

// upstream: packages/coding-agent/test/debug/dap-launch-failures.test.ts: "surfaces the attach failure when configurationDone also fails"
// upstream: packages/coding-agent/test/debug/dap-launch-failures.test.ts: "prefers a delayed launch failure over the configurationDone cascade"
func TestSessionStartErrorPreference(t *testing.T) {
	adapter := DapResolvedAdapter{Name: "lldb-dap", ConnectMode: ConnectModeStdio}

	t.Run("attach", func(t *testing.T) {
		mgr := NewSessionManager()
		defer mgr.Shutdown(context.Background())
		fake := newFakeDapClient(adapter, t.TempDir(), fakeClientOpts{
			attachErr:     "attach: target process exited",
			configDoneErr: "configurationDone: Expected process to be stopped.",
		})
		mgr.spawnFn = func(_ context.Context, _ SpawnOptions) (dapClient, error) { return fake, nil }
		pid := 123
		_, err := mgr.Attach(context.Background(), DapAttachSessionOptions{Adapter: adapter, Cwd: t.TempDir(), PID: &pid}, time.Second)
		if err == nil || !strings.Contains(err.Error(), "attach: target process exited") || !strings.Contains(err.Error(), "configurationDone: Expected process to be stopped.") {
			t.Fatalf("expected combined attach + configurationDone error, got %v", err)
		}
	})

	t.Run("attach error before initialized returns bare start error", func(t *testing.T) {
		mgr := NewSessionManager()
		defer mgr.Shutdown(context.Background())
		fake := newFakeDapClient(adapter, t.TempDir(), fakeClientOpts{
			deferInitialized: true,
			attachErr:        "attach: target process exited",
			configDoneErr:    "configurationDone: Expected process to be stopped.",
		})
		mgr.spawnFn = func(_ context.Context, _ SpawnOptions) (dapClient, error) { return fake, nil }
		pid := 123
		_, err := mgr.Attach(context.Background(), DapAttachSessionOptions{Adapter: adapter, Cwd: t.TempDir(), PID: &pid}, time.Second)
		if err == nil || err.Error() != "attach: target process exited" {
			t.Fatalf("expected bare start error %q, got %v", "attach: target process exited", err)
		}
		if fake.countRequests("configurationDone") != 0 {
			t.Fatalf("expected configurationDone not sent when attach failed before initialized")
		}
	})

	t.Run("delayed launch", func(t *testing.T) {
		mgr := NewSessionManager()
		defer mgr.Shutdown(context.Background())
		fake := newFakeDapClient(adapter, t.TempDir(), fakeClientOpts{
			launchErr:      "launch: not a valid executable",
			launchErrDelay: 10 * time.Millisecond,
			configDoneErr:  "configurationDone: Expected process to be stopped.",
		})
		mgr.spawnFn = func(_ context.Context, _ SpawnOptions) (dapClient, error) { return fake, nil }
		_, err := mgr.Launch(context.Background(), DapLaunchSessionOptions{Adapter: adapter, Program: "/bad/prog", Cwd: t.TempDir()}, time.Second)
		if err == nil || !strings.Contains(err.Error(), "launch: not a valid executable") || !strings.Contains(err.Error(), "configurationDone: Expected process to be stopped.") {
			t.Fatalf("expected combined launch + configurationDone error, got %v", err)
		}
	})
}

func TestShouldKillAdapterTree(t *testing.T) {
	cases := []struct {
		goos   string
		exited bool
		want   bool
	}{
		{"darwin", false, true},
		{"darwin", true, true},
		{"linux", false, true},
		{"linux", true, true},
		{"windows", false, true},
		{"windows", true, false},
	}
	for _, tc := range cases {
		if got := shouldKillAdapterTree(tc.goos, tc.exited); got != tc.want {
			t.Errorf("shouldKillAdapterTree(%q, %v) = %v, want %v", tc.goos, tc.exited, got, tc.want)
		}
	}
}

func TestDebugToolGatePolicy(t *testing.T) {
	isolateHome(t)
	gate.ResetForTests()
	t.Cleanup(gate.ResetForTests)

	cwd := t.TempDir()
	prog := filepath.Join(cwd, "main.go")
	_ = os.WriteFile(prog, []byte("package main\nfunc main(){}\n"), 0o644)

	adapter := helperAdapter(t, "stdio", ConnectModeStdio)
	tool := NewDebugTool(Config{}, nil)
	tool.selectLaunchAdapter = func(_, _, _ string, _ LaunchProgramKind, _ bool) LaunchAdapterSelection {
		return LaunchAdapterSelection{Kind: "adapter", Adapter: &adapter}
	}

	ext := sdk.New("oh-my-pig")
	ext.RegisterTool(tool.Definition())
	sid := "gate-test-session"
	host := startHost(t, ext, cwd, sid)
	t.Cleanup(func() {
		tool.registry.shutdownKey(context.Background(), sid)
	})

	// 1. Default policy (nil): exec action is allowed.
	res, errMsg := host.invokeTool("debug", map[string]any{"action": "launch", "program": "main.go"})
	if errMsg != "" || !strings.Contains(res["content"].(string), "Status: stopped") {
		t.Fatalf("default gate policy should allow launch, err=%q res=%#v", errMsg, res)
	}

	// 2. Denying policy: exec actions are blocked with policy error; readonly actions stay allowed.
	var seen []gate.Request
	gate.SetPolicy(func(_ sdk.Context, r gate.Request) error {
		seen = append(seen, r)
		return errors.New("plan mode forbids exec action: " + r.Detail)
	})

	for _, execAction := range []string{"continue", "step_over", "evaluate", "set_breakpoint", "write_memory", "custom_request", "terminate"} {
		_, errMsg = host.invokeTool("debug", map[string]any{"action": execAction})
		if !strings.Contains(errMsg, "plan mode forbids exec action: "+execAction) {
			t.Fatalf("action %q: expected gate denial, got %q", execAction, errMsg)
		}
	}
	if len(seen) != 7 {
		t.Fatalf("expected 7 gate.Check calls, got %d", len(seen))
	}
	for _, r := range seen {
		if r.Tool != "debug" || r.Op != gate.OpExec {
			t.Fatalf("unexpected gate.Request: %+v", r)
		}
	}

	// Readonly actions must bypass gate.Check and still succeed.
	before := len(seen)
	for _, roAction := range []string{"sessions", "threads", "stack_trace", "output"} {
		_, errMsg = host.invokeTool("debug", map[string]any{"action": roAction})
		if errMsg != "" {
			t.Fatalf("readonly action %q should not be blocked by gate, got %q", roAction, errMsg)
		}
	}
	if len(seen) != before {
		t.Fatalf("readonly actions should not invoke gate.Check, got %d extra calls", len(seen)-before)
	}

	// Clean up with policy cleared.
	gate.SetPolicy(nil)
	_, errMsg = host.invokeTool("debug", map[string]any{"action": "terminate"})
	if errMsg != "" {
		t.Fatalf("terminate after clearing gate policy failed: %q", errMsg)
	}
}

// upstream: packages/coding-agent/test/debug/dap-launch-failures.test.ts: "completes configuration without sending attach for preattached adapters"
func TestSessionPreattachedAdapter(t *testing.T) {
	mgr := NewSessionManager()
	defer mgr.Shutdown(context.Background())

	adapter := DapResolvedAdapter{
		Name:           "preattached",
		AttachDefaults: map[string]any{"request": "attach", "skipAttachRequest": true},
		ConnectMode:    ConnectModeStdio,
	}
	fBool := false
	fake := newFakeDapClient(adapter, t.TempDir(), fakeClientOpts{
		supportsConfigDone: &fBool,
		deferInitialized:   true,
	})
	mgr.spawnFn = func(_ context.Context, _ SpawnOptions) (dapClient, error) { return fake, nil }

	summary, err := mgr.Attach(context.Background(), DapAttachSessionOptions{Adapter: adapter, Cwd: t.TempDir()}, time.Second)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if len(fake.requests) != 0 || summary.Status != StatusRunning || summary.NeedsConfigurationDone {
		t.Fatalf("unexpected preattached state: reqs=%v summary=%+v", fake.requests, summary)
	}
	fake.emit("initialized", map[string]any{})
	if got := mgr.GetActiveSession(); got == nil || got.Status != StatusRunning {
		t.Fatalf("expected running after late initialized, got %+v", got)
	}
}

// upstream: packages/coding-agent/test/debug/dap-launch-failures.test.ts: "surfaces the adapter name and ENOENT when spawn fails"
func TestSessionSpawnENOENTSurfacesAdapterName(t *testing.T) {
	mgr := NewSessionManager()
	defer mgr.Shutdown(context.Background())

	adapter := DapResolvedAdapter{
		Name:            "lldb-dap",
		Command:         "/nonexistent/path/to/lldb-dap-missing",
		ResolvedCommand: "/nonexistent/path/to/lldb-dap-missing",
		ConnectMode:     ConnectModeStdio,
	}
	_, err := mgr.Launch(context.Background(), DapLaunchSessionOptions{Adapter: adapter, Program: "/bin/echo", Cwd: t.TempDir()}, time.Second)
	if err == nil || !strings.Contains(err.Error(), "lldb-dap") {
		t.Fatalf("expected spawn error naming lldb-dap, got %v", err)
	}
}

// upstream: packages/coding-agent/test/debug/dap-launch-failures.test.ts: "surfaces 'pip install debugpy' when launch stderr mentions missing module"
// upstream: packages/coding-agent/test/debug/dap-launch-failures.test.ts: "surfaces 'pip install debugpy' when attach stderr mentions missing module"
// upstream: packages/coding-agent/test/debug/dap-launch-failures.test.ts: "does NOT rewrite to 'pip install debugpy' for non-debugpy adapters even when stderr mentions the module"
func TestSessionDebugpyMissingModuleHint(t *testing.T) {
	t.Run("debugpy launch", func(t *testing.T) {
		mgr := NewSessionManager()
		defer mgr.Shutdown(context.Background())
		adapter := DapResolvedAdapter{Name: "debugpy", ConnectMode: ConnectModeStdio}
		fake := newFakeDapClient(adapter, t.TempDir(), fakeClientOpts{launchErr: "ImportError: No module named 'debugpy'"})
		mgr.spawnFn = func(_ context.Context, _ SpawnOptions) (dapClient, error) { return fake, nil }
		_, err := mgr.Launch(context.Background(), DapLaunchSessionOptions{Adapter: adapter, Program: "/bin/echo", Cwd: t.TempDir()}, time.Second)
		if err == nil || !strings.Contains(err.Error(), "pip install debugpy") {
			t.Fatalf("expected pip install debugpy hint, got %v", err)
		}
	})

	t.Run("debugpy attach", func(t *testing.T) {
		mgr := NewSessionManager()
		defer mgr.Shutdown(context.Background())
		adapter := DapResolvedAdapter{Name: "debugpy", ConnectMode: ConnectModeStdio}
		fake := newFakeDapClient(adapter, t.TempDir(), fakeClientOpts{attachErr: `ModuleNotFoundError: No module named "debugpy"`})
		mgr.spawnFn = func(_ context.Context, _ SpawnOptions) (dapClient, error) { return fake, nil }
		pid := 123
		_, err := mgr.Attach(context.Background(), DapAttachSessionOptions{Adapter: adapter, Cwd: t.TempDir(), PID: &pid}, time.Second)
		if err == nil || !strings.Contains(err.Error(), "pip install debugpy") {
			t.Fatalf("expected pip install debugpy hint on attach, got %v", err)
		}
	})

	t.Run("non-debugpy untouched", func(t *testing.T) {
		mgr := NewSessionManager()
		defer mgr.Shutdown(context.Background())
		adapter := DapResolvedAdapter{Name: "lldb-dap", ConnectMode: ConnectModeStdio}
		fake := newFakeDapClient(adapter, t.TempDir(), fakeClientOpts{launchErr: "incidental log: No module named debugpy"})
		mgr.spawnFn = func(_ context.Context, _ SpawnOptions) (dapClient, error) { return fake, nil }
		_, err := mgr.Launch(context.Background(), DapLaunchSessionOptions{Adapter: adapter, Program: "/bin/echo", Cwd: t.TempDir()}, time.Second)
		if err == nil || strings.Contains(err.Error(), "pip install debugpy") || !strings.Contains(err.Error(), "incidental log") {
			t.Fatalf("expected untouched error, got %v", err)
		}
	})
}

// blockingWriter blocks writes until closed; used to simulate wedged stdin.
type blockingWriter struct {
	block chan struct{}
	once  sync.Once
}

func (b *blockingWriter) Write(p []byte) (int, error) {
	<-b.block
	return 0, errors.New("EPIPE: broken pipe")
}

func (b *blockingWriter) Close() error {
	b.once.Do(func() { close(b.block) })
	return nil
}

// failingWriter always fails Write immediately with EPIPE.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("EPIPE: broken pipe, write") }
func (failingWriter) Close() error              { return nil }

// upstream: packages/coding-agent/test/debug/dap-launch-failures.test.ts: "times out promptly and does not emit an unhandled rejection when the stdin flush is wedged"
// upstream: packages/coding-agent/test/debug/dap-launch-failures.test.ts: "rejects the request instead of emitting an unhandled rejection when a pending stdin write fails"
// upstream: packages/coding-agent/test/debug/dap-launch-failures.test.ts: "disposes the client when the stdin flush throws synchronously"
func TestDapClientWriteFailureResilience(t *testing.T) {
	adapter := DapResolvedAdapter{Name: "lldb-dap", ConnectMode: ConnectModeStdio}

	t.Run("wedged stdin write times out promptly", func(t *testing.T) {
		pr, pw := io.Pipe()
		bw := &blockingWriter{block: make(chan struct{})}
		client := newDapClient(adapter, t.TempDir(), nil, pr, bw, nil, 0, "", nil, nil)
		defer func() {
			_ = pw.Close()
			_ = client.Dispose()
		}()

		start := time.Now()
		_, err := client.SendRequest(context.Background(), "initialize", map[string]any{}, 60*time.Millisecond)
		if err == nil || !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("expected timed out error, got %v", err)
		}
		if time.Since(start) > 500*time.Millisecond {
			t.Fatalf("expected prompt timeout, took %v", time.Since(start))
		}
	})

	t.Run("failing stdin write rejects immediately and disposes client", func(t *testing.T) {
		pr, pw := io.Pipe()
		client := newDapClient(adapter, t.TempDir(), nil, pr, failingWriter{}, nil, 0, "", nil, nil)
		defer func() {
			_ = pw.Close()
			_ = client.Dispose()
		}()

		start := time.Now()
		_, err := client.SendRequest(context.Background(), "evaluate", map[string]any{}, 5*time.Second)
		if err == nil || !strings.Contains(err.Error(), "EPIPE") {
			t.Fatalf("expected EPIPE error, got %v", err)
		}
		if time.Since(start) > time.Second {
			t.Fatalf("expected fast failure, took %v", time.Since(start))
		}
		// Give background dispose a moment, then confirm subsequent calls fail fast.
		time.Sleep(30 * time.Millisecond)
		_, err2 := client.SendRequest(context.Background(), "evaluate", map[string]any{}, 5*time.Second)
		if err2 == nil || !strings.Contains(err2.Error(), "not running") {
			t.Fatalf("expected not running after broken pipe disposal, got %v", err2)
		}
	})
}

// upstream: packages/coding-agent/test/debug/dap-launch-failures.test.ts: "waits for delayed Unix socket adapters before connecting on Linux"
// upstream: packages/coding-agent/test/debug/dap-launch-failures.test.ts: "kills the detached adapter process when the Unix socket never appears (Linux)"
func TestDapClientUnixSocketLifecycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix socket tests are Unix-only")
	}

	t.Run("waits for delayed Unix socket adapter", func(t *testing.T) {
		cwd := t.TempDir()
		adapter := helperAdapter(t, "socket_unix_delayed", ConnectModeSocket)
		client, err := spawnSocketUnix(context.Background(), SpawnOptions{Adapter: adapter, Cwd: cwd}, 5*time.Second)
		if err != nil {
			t.Fatalf("spawnSocketUnix: %v", err)
		}
		defer func() { _ = client.Dispose() }()
		if !client.IsAlive() {
			t.Fatal("expected client alive after delayed Unix socket connect")
		}
		caps, err := client.Initialize(context.Background(), buildInitializeArguments(adapter), 2*time.Second)
		if err != nil || caps == nil || !caps.SupportsConfigurationDoneRequest {
			t.Fatalf("Initialize over Unix socket failed: err=%v caps=%+v", err, caps)
		}
	})

	t.Run("kills wedged Unix socket adapter on timeout", func(t *testing.T) {
		cwd := t.TempDir()
		pidFile := filepath.Join(cwd, "unix-wedged.pid")
		adapter := helperAdapter(t, "wedged_socket", ConnectModeSocket, pidFile)
		_, err := spawnSocketUnix(context.Background(), SpawnOptions{Adapter: adapter, Cwd: cwd}, 800*time.Millisecond)
		if err == nil || !strings.Contains(err.Error(), "Socket not ready") {
			t.Fatalf("expected Socket not ready timeout, got %v", err)
		}
		raw, rerr := os.ReadFile(pidFile)
		if rerr != nil {
			t.Fatalf("expected pid file: %v", rerr)
		}
		pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
		time.Sleep(100 * time.Millisecond)
		if isProcessAlive(pid) {
			t.Fatalf("wedged unix adapter pid %d leaked after timeout", pid)
		}
	})
}

// upstream: packages/coding-agent/test/debug/dap-launch-failures.test.ts: "kills the detached adapter process when it never dials back on the TCP client-addr path"
func TestDapClientKillsWedgedSocketAdapter(t *testing.T) {
	cwd := t.TempDir()
	pidFile := filepath.Join(cwd, "adapter.pid")
	adapter := helperAdapter(t, "wedged_socket", ConnectModeSocket, pidFile)

	_, err := SpawnDapClient(context.Background(), SpawnOptions{
		Adapter:            adapter,
		Cwd:                cwd,
		SocketReadyTimeout: 800 * time.Millisecond,
		ForceClientAddr:    true,
	})
	if err == nil || !strings.Contains(err.Error(), "did not connect within") {
		t.Fatalf("expected connect timeout error, got %v", err)
	}
	raw, rerr := os.ReadFile(pidFile)
	if rerr != nil {
		t.Fatalf("expected pid file: %v", rerr)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	if pid <= 0 {
		t.Fatalf("invalid pid: %q", string(raw))
	}
	time.Sleep(100 * time.Millisecond)
	if runtime.GOOS != "windows" && isProcessAlive(pid) {
		t.Fatalf("wedged adapter pid %d leaked after timeout", pid)
	}
}

// upstream: packages/coding-agent/test/debug/dap-launch-failures.test.ts: "rejects instead of hanging when the unix socket cannot be connected"
func TestConnectUnixSocketRejectsFast(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix socket test is unix-only")
	}
	dead := filepath.Join(t.TempDir(), "nonexistent.sock")
	start := time.Now()
	_, err := ConnectUnixSocket(context.Background(), dead, 5*time.Second)
	if err == nil {
		t.Fatal("expected error connecting to nonexistent unix socket")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("expected fast rejection, took %v", time.Since(start))
	}
}

// upstream: packages/coding-agent/test/debug/dap-launch-failures.test.ts: "rejects a pending request fast when the transport closes cleanly without answering"
// upstream: packages/coding-agent/test/debug/dap-launch-failures.test.ts: "wakes an event waiter when the transport closes instead of waiting out its timeout"
func TestDapClientGhostTCPResilience(t *testing.T) {
	adapter := helperAdapter(t, "ghost_tcp", ConnectModeTCP, dapPortArgument)
	client, err := SpawnDapClient(context.Background(), SpawnOptions{
		Adapter:            adapter,
		Cwd:                t.TempDir(),
		SocketReadyTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("SpawnDapClient: %v", err)
	}
	defer func() { _ = client.Dispose() }()

	_, rerr := client.SendRequest(context.Background(), "initialize", map[string]any{}, 30*time.Second)
	if rerr == nil || !strings.Contains(rerr.Error(), "DAP connection closed") {
		t.Fatalf("expected DAP connection closed on request, got %v", rerr)
	}
	_, eerr := client.WaitForEvent(context.Background(), "stopped", nil, 30*time.Second)
	if eerr == nil || !strings.Contains(eerr.Error(), "DAP connection closed") {
		t.Fatalf("expected DAP connection closed on event waiter, got %v", eerr)
	}
}

// upstream: packages/coding-agent/test/debug/dap-launch-failures.test.ts: "holds the gate until stdout announces the port, even split across chunks"
// upstream: packages/coding-agent/test/debug/dap-launch-failures.test.ts: "opens the gate when stdout ends without a banner so the connect loop surfaces the real failure"
func TestWaitForTcpServerListening(t *testing.T) {
	t.Run("split chunks", func(t *testing.T) {
		pr, pw := io.Pipe()
		done := make(chan struct{})
		go func() {
			WaitForTcpServerListening(context.Background(), pr, 43210, 5*time.Second)
			close(done)
		}()
		_, _ = pw.Write([]byte("Starting inspector...\nDebug server listening at 127.0.0.1:43"))
		select {
		case <-done:
			t.Fatal("gate opened before full port appeared")
		case <-time.After(30 * time.Millisecond):
		}
		_, _ = pw.Write([]byte("210\n"))
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("gate did not open after port completed")
		}
		_ = pw.Close()
	})

	t.Run("eof without banner", func(t *testing.T) {
		pr, pw := io.Pipe()
		done := make(chan struct{})
		go func() {
			WaitForTcpServerListening(context.Background(), pr, 43210, 5*time.Second)
			close(done)
		}()
		_, _ = pw.Write([]byte("adapter crashed before binding\n"))
		_ = pw.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("gate did not open on EOF")
		}
	})
}

// TestDapClientDisposeKillsOrphanAfterSelfExit verifies that even when the
// adapter process exits on its own, Dispose still kills its process group so
// orphaned debuggee descendants cannot outlive the session.
func TestDapClientDisposeKillsOrphanAfterSelfExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group signal test is Unix-only")
	}
	cwd := t.TempDir()
	pidFile := filepath.Join(cwd, "child.pid")
	adapter := helperAdapter(t, "self_exit_with_child", ConnectModeStdio, pidFile)
	client, err := SpawnDapClient(context.Background(), SpawnOptions{Adapter: adapter, Cwd: cwd})
	if err != nil {
		t.Fatalf("SpawnDapClient: %v", err)
	}
	// Wait for adapter itself to exit (so handleProcessExit has run).
	select {
	case <-client.Exited():
	case <-time.After(5 * time.Second):
		t.Fatal("adapter did not exit")
	}
	time.Sleep(50 * time.Millisecond)

	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("child pid file missing: %v", err)
	}
	childPID, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	if !isProcessAlive(childPID) {
		t.Fatalf("expected orphan child %d to still be alive before Dispose", childPID)
	}

	_ = client.Dispose()
	time.Sleep(100 * time.Millisecond)
	if isProcessAlive(childPID) {
		t.Fatalf("orphan child %d survived Dispose after adapter self-exit", childPID)
	}
}

// TestDapClientDetectsExitWhenChildHoldsStderr verifies that owning the stderr
// pipe allows cmd.Wait() to return immediately when the adapter exits even if a
// descendant inherited stderr, surfacing the exit code and stderr output.
func TestDapClientDetectsExitWhenChildHoldsStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix pipe inheritance test")
	}
	cwd := t.TempDir()
	pidFile := filepath.Join(cwd, "stderr-child.pid")
	adapter := helperAdapter(t, "exit_with_stderr_child", ConnectModeStdio, pidFile)
	client, err := SpawnDapClient(context.Background(), SpawnOptions{Adapter: adapter, Cwd: cwd})
	if err != nil {
		t.Fatalf("SpawnDapClient: %v", err)
	}
	defer func() { _ = client.Dispose() }()

	select {
	case <-client.Exited():
	case <-time.After(2 * time.Second):
		t.Fatal("adapter exit not detected while descendant held stderr")
	}
	if client.IsAlive() {
		t.Fatal("expected IsAlive()==false after adapter exit")
	}
	_, rerr := client.WaitForEvent(context.Background(), "stopped", nil, time.Second)
	if rerr == nil || !strings.Contains(rerr.Error(), "code 42") || !strings.Contains(rerr.Error(), "adapter fatal crash") {
		t.Fatalf("expected exit code 42 and stderr in error, got %v", rerr)
	}
}

// upstream: packages/coding-agent/test/debug/dap-launch-failures.test.ts: DebugTool launch validation suite
func TestDebugToolLaunchValidation(t *testing.T) {
	isolateHome(t)

	t.Run("rejects directory when adapter cannot debug directories", func(t *testing.T) {
		cwd := t.TempDir()
		_ = os.MkdirAll(filepath.Join(cwd, "python"), 0o755)
		tool := NewDebugTool(Config{}, NewSessionManager())
		tool.selectLaunchAdapter = func(_, _, _ string, _ LaunchProgramKind, _ bool) LaunchAdapterSelection {
			return LaunchAdapterSelection{
				Kind:    "adapter",
				Adapter: &DapResolvedAdapter{Name: "lldb-dap", AcceptsDirectoryProgram: false},
			}
		}
		ext := sdk.New("test")
		ext.RegisterTool(tool.Definition())
		host := startHost(t, ext, cwd, "s1")
		_, errMsg := host.invokeTool("debug", map[string]any{"action": "launch", "program": "python"})
		if !strings.Contains(errMsg, "launch program resolves to a directory: python/") {
			t.Fatalf("expected directory rejection with python/, got %q", errMsg)
		}
		// Launching `.` should display `.` (not `./`), matching path-utils.ts:527.
		_, errDot := host.invokeTool("debug", map[string]any{"action": "launch", "program": "."})
		if !strings.Contains(errDot, "launch program resolves to a directory: .") || strings.Contains(errDot, "./") {
			t.Fatalf("expected '.' without trailing slash, got %q", errDot)
		}
	})

	// upstream: packages/coding-agent/test/debug/dap-launch-failures.test.ts: "allows directory programs when the selected adapter accepts them (dlv on a Go package)"
	// upstream: packages/coding-agent/test/debug/dap-launch-failures.test.ts: "dlv launch with a compiled binary switches mode from debug to exec"
	t.Run("dlv launch through tool sets mode=debug for dir and mode=exec for binary", func(t *testing.T) {
		cwd := t.TempDir()
		_ = os.MkdirAll(filepath.Join(cwd, "cmd"), 0o755)
		_ = os.WriteFile(filepath.Join(cwd, "hello"), []byte("#!/bin/sh\necho hi\n"), 0o755)

		dlvAdapter := DapResolvedAdapter{
			Name:                    "dlv",
			Command:                 "dlv",
			ResolvedCommand:         "dlv",
			LaunchDefaults:          map[string]any{"request": "launch", "mode": "debug", "stopOnEntry": true},
			AcceptsDirectoryProgram: true,
			ConnectMode:             ConnectModeStdio,
		}
		for _, tc := range []struct {
			prog     string
			wantMode string
		}{
			{"cmd", "debug"},
			{"hello", "exec"},
		} {
			mgr := NewSessionManager()
			defer mgr.Shutdown(context.Background())
			fake := newFakeDapClient(dlvAdapter, cwd, fakeClientOpts{stopAfterLaunch: true})
			mgr.spawnFn = func(_ context.Context, _ SpawnOptions) (dapClient, error) { return fake, nil }
			tool := NewDebugTool(Config{}, mgr)
			tool.selectLaunchAdapter = func(_, _, _ string, _ LaunchProgramKind, _ bool) LaunchAdapterSelection {
				return LaunchAdapterSelection{Kind: "adapter", Adapter: &dlvAdapter}
			}
			ext := sdk.New("test")
			ext.RegisterTool(tool.Definition())
			host := startHost(t, ext, cwd, "dlv-"+tc.prog)
			_, errMsg := host.invokeTool("debug", map[string]any{"action": "launch", "program": tc.prog, "adapter": "dlv"})
			if errMsg != "" {
				t.Fatalf("launch %s failed: %s", tc.prog, errMsg)
			}
			var gotMode any
			for _, req := range fake.requests {
				if req.Command == "launch" {
					if m, ok := req.Args.(map[string]any); ok {
						gotMode = m["mode"]
					}
				}
			}
			if gotMode != tc.wantMode {
				t.Fatalf("launch %s: expected mode=%q, got %v", tc.prog, tc.wantMode, gotMode)
			}
		}
	})

	// upstream: packages/coding-agent/test/debug/dap-launch-failures.test.ts: "prefers directory-capable dlv over native adapters for extensionless Go package directories"
	t.Run("prefers directory-capable dlv over gdb for Go package directory through tool", func(t *testing.T) {
		cwd := t.TempDir()
		_ = os.WriteFile(filepath.Join(cwd, "go.mod"), []byte("module hello\n\ngo 1.22\n"), 0o644)
		_ = os.WriteFile(filepath.Join(cwd, "Makefile"), []byte("all:\n\tgo build ./...\n"), 0o644)
		writeExecFile(t, filepath.Join(cwd, "bin", "dlv"))
		writeExecFile(t, filepath.Join(cwd, "bin", "gdb"))
		_ = os.MkdirAll(filepath.Join(cwd, "cmd", "hello"), 0o755)

		mgr := NewSessionManager()
		defer mgr.Shutdown(context.Background())
		var capturedAdapter string
		mgr.spawnFn = func(_ context.Context, opts SpawnOptions) (dapClient, error) {
			capturedAdapter = opts.Adapter.Name
			return newFakeDapClient(opts.Adapter, cwd, fakeClientOpts{stopAfterLaunch: true}), nil
		}
		tool := NewDebugTool(Config{}, mgr)
		ext := sdk.New("test")
		ext.RegisterTool(tool.Definition())
		host := startHost(t, ext, cwd, "dlv-pref")
		_, errMsg := host.invokeTool("debug", map[string]any{"action": "launch", "program": "cmd/hello"})
		if errMsg != "" {
			t.Fatalf("launch failed: %s", errMsg)
		}
		if capturedAdapter != "dlv" {
			t.Fatalf("expected dlv adapter, got %q", capturedAdapter)
		}
	})

	// upstream: packages/coding-agent/test/debug/dap-launch-failures.test.ts:
	// - "throws targeted 'python not found in PATH' when adapter:'debugpy' is unresolvable for launch"
	// - "throws targeted 'python not found in PATH' when adapter:'debugpy' is unresolvable for attach"
	// - "shows the Delve install command when the canonical dlv adapter is unavailable"
	// - "points to DAP configuration when a custom adapter command is unavailable"
	// - "falls back to the generic 'No debugger adapter' error when adapter is unspecified"
	t.Run("unavailable and missing adapter messages through tool", func(t *testing.T) {
		cwd := t.TempDir()
		_ = os.WriteFile(filepath.Join(cwd, "main.py"), []byte("print('hi')\n"), 0o644)
		_ = os.WriteFile(filepath.Join(cwd, "main.go"), []byte("package main\nfunc main(){}\n"), 0o644)

		cases := []struct {
			name   string
			sel    LaunchAdapterSelection
			params map[string]any
			want   string
		}{
			{
				name:   "debugpy launch",
				sel:    LaunchAdapterSelection{Kind: "unavailable", AdapterName: "debugpy", Command: "python"},
				params: map[string]any{"action": "launch", "program": "main.py", "adapter": "debugpy"},
				want:   "python not found in PATH",
			},
			{
				name:   "dlv canonical hint",
				sel:    LaunchAdapterSelection{Kind: "unavailable", AdapterName: "dlv", Command: "dlv"},
				params: map[string]any{"action": "launch", "program": "main.go"},
				want:   "go install github.com/go-delve/delve/cmd/dlv@latest",
			},
			{
				name:   "custom command hint",
				sel:    LaunchAdapterSelection{Kind: "unavailable", AdapterName: "dlv", Command: "./bin/missing-dlv"},
				params: map[string]any{"action": "launch", "program": "main.go"},
				want:   "configured command './bin/missing-dlv' did not resolve",
			},
			{
				name:   "no adapter fallback",
				sel:    LaunchAdapterSelection{Kind: "none"},
				params: map[string]any{"action": "launch", "program": "main.py"},
				want:   "No debugger adapter available",
			},
		}
		for _, tc := range cases {
			tool := NewDebugTool(Config{}, NewSessionManager())
			tool.selectLaunchAdapter = func(_, _, _ string, _ LaunchProgramKind, _ bool) LaunchAdapterSelection { return tc.sel }
			ext := sdk.New("test")
			ext.RegisterTool(tool.Definition())
			host := startHost(t, ext, cwd, "err-"+tc.name)
			_, errMsg := host.invokeTool("debug", tc.params)
			if !strings.Contains(errMsg, tc.want) {
				t.Fatalf("%s: expected %q in %q", tc.name, tc.want, errMsg)
			}
		}

		// Attach with unavailable debugpy.
		tool := NewDebugTool(Config{}, NewSessionManager())
		tool.selectAttachAdapter = func(_ context.Context, _, _ string, _ DapAttachTarget, _ bool) *DapResolvedAdapter { return nil }
		ext := sdk.New("test")
		ext.RegisterTool(tool.Definition())
		host := startHost(t, ext, cwd, "attach-debugpy")
		_, errMsg := host.invokeTool("debug", map[string]any{"action": "attach", "pid": 1234, "adapter": "debugpy"})
		if !strings.Contains(errMsg, "python not found in PATH") {
			t.Fatalf("expected debugpy attach hint, got %q", errMsg)
		}
	})

	// upstream: packages/coding-agent/test/debug/dap-launch-failures.test.ts: "validates missing attach targets before adapter discovery"
	// upstream: packages/coding-agent/test/debug/dap-launch-failures.test.ts: "allows explicit adapters with target attach defaults to attach without pid or port"
	t.Run("attach target validation and explicit adapter without pid/port", func(t *testing.T) {
		cwd := t.TempDir()
		mgr := NewSessionManager()
		defer mgr.Shutdown(context.Background())
		tool := NewDebugTool(Config{}, mgr)
		selectCalled := false
		tool.selectAttachAdapter = func(_ context.Context, _, adapterName string, _ DapAttachTarget, _ bool) *DapResolvedAdapter {
			selectCalled = true
			if adapterName == "pico-openocd" {
				return &DapResolvedAdapter{
					Name:           "pico-openocd",
					AttachDefaults: map[string]any{"target": ":3334"},
					ConnectMode:    ConnectModeStdio,
				}
			}
			return nil
		}
		fake := newFakeDapClient(DapResolvedAdapter{Name: "pico-openocd"}, cwd, fakeClientOpts{})
		mgr.spawnFn = func(_ context.Context, _ SpawnOptions) (dapClient, error) { return fake, nil }
		ext := sdk.New("test")
		ext.RegisterTool(tool.Definition())
		host := startHost(t, ext, cwd, "attach-val")

		_, errMsg := host.invokeTool("debug", map[string]any{"action": "attach"})
		if !strings.Contains(errMsg, "attach requires pid or port") || selectCalled {
			t.Fatalf("expected validation before discovery, err=%q selectCalled=%v", errMsg, selectCalled)
		}

		res, errMsg := host.invokeTool("debug", map[string]any{"action": "attach", "adapter": "pico-openocd"})
		if errMsg != "" || !strings.Contains(res["content"].(string), "Adapter: pico-openocd") {
			t.Fatalf("expected explicit attach to succeed, err=%q res=%#v", errMsg, res)
		}
	})
}

// =============================================================================
// Upstream ported tests: dap-multi-session.test.ts
// =============================================================================

// upstream: packages/coding-agent/test/debug/dap-multi-session.test.ts: "routes recursive js-debug children, breakpoints, and termination through one session tree"
func TestDapMultiSessionTree(t *testing.T) {
	adapter := DapResolvedAdapter{Name: "js-debug-adapter", ConnectMode: ConnectModeTCP}
	root := newFakeDapClient(adapter, "/tmp", fakeClientOpts{
		childConfig: map[string]any{"name": "target.js", "type": "pwa-node", "program": "/tmp/target.js"},
	})
	child := newFakeDapClient(adapter, "/tmp", fakeClientOpts{
		childConfig: map[string]any{"name": "[worker 1]", "type": "pwa-node"},
	})
	grandchild := newFakeDapClient(adapter, "/tmp", fakeClientOpts{stopAfterLaunch: true})
	children := []*fakeDapClient{child, grandchild}

	mgr := NewSessionManager()
	defer mgr.Shutdown(context.Background())
	mgr.spawnFn = func(_ context.Context, _ SpawnOptions) (dapClient, error) { return root, nil }
	mgr.connectFn = func(_ context.Context, _ ConnectOptions) (dapClient, error) {
		next := children[0]
		children = children[1:]
		return next, nil
	}

	launched, err := mgr.Launch(context.Background(), DapLaunchSessionOptions{Adapter: adapter, Program: "/tmp/target.js", Cwd: "/tmp"}, 2*time.Second)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if launched.Status != StatusStopped || launched.ParentSessionID == "" || launched.Line == nil || *launched.Line != 2 {
		t.Fatalf("unexpected launched summary: %+v", launched)
	}
	if got := mgr.ListSessions(); len(got) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(got))
	}

	_, bps, _, err := mgr.SetBreakpoint(context.Background(), "/tmp/target.js", 2, "", 2*time.Second)
	if err != nil || len(bps) != 1 || !bps[0].Verified {
		t.Fatalf("SetBreakpoint failed: err=%v bps=%+v", err, bps)
	}
	for _, cl := range []*fakeDapClient{root, child, grandchild} {
		if cl.countRequests("setBreakpoints") != 1 {
			t.Fatalf("expected 1 setBreakpoints on every node, got %d", cl.countRequests("setBreakpoints"))
		}
	}

	if _, err := mgr.Terminate(context.Background(), 2*time.Second); err != nil {
		t.Fatalf("Terminate: %v", err)
	}
	if len(mgr.ListSessions()) != 0 {
		t.Fatalf("expected 0 sessions after terminate")
	}
	for _, cl := range []*fakeDapClient{root, child, grandchild} {
		if cl.countRequests("disconnect") == 0 || !cl.disposed {
			t.Fatalf("expected disconnect and disposal on every node")
		}
	}
}

// upstream: packages/coding-agent/test/debug/dap-multi-session.test.ts: "targets a running attach child before it emits a stopped event"
func TestDapMultiSessionTargetsRunningAttachChild(t *testing.T) {
	adapter := DapResolvedAdapter{Name: "js-debug-adapter", ConnectMode: ConnectModeTCP}
	root := newFakeDapClient(adapter, "/tmp", fakeClientOpts{
		childConfig:  map[string]any{"name": "attached.js", "type": "pwa-node"},
		childRequest: "attach",
		threads:      []DapThread{},
	})
	child := newFakeDapClient(adapter, "/tmp", fakeClientOpts{stopAfterLaunch: false})

	mgr := NewSessionManager()
	defer mgr.Shutdown(context.Background())
	mgr.spawnFn = func(_ context.Context, _ SpawnOptions) (dapClient, error) { return root, nil }
	mgr.connectFn = func(_ context.Context, _ ConnectOptions) (dapClient, error) { return child, nil }

	_, err := mgr.Launch(context.Background(), DapLaunchSessionOptions{Adapter: adapter, Program: "/tmp/attached.js", Cwd: "/tmp"}, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	active := mgr.GetActiveSession()
	if active == nil || active.ParentSessionID == "" {
		t.Fatalf("expected active session to be child, got %+v", active)
	}
	_, threads, err := mgr.Threads(context.Background(), time.Second)
	if err != nil || len(threads) != 1 || threads[0].ID != 7 {
		t.Fatalf("expected child thread 7, got err=%v threads=%+v", err, threads)
	}
	if child.countRequests("threads") != 1 || root.countRequests("threads") != 1 {
		t.Fatalf("expected threads queried on both root and child")
	}
}

// upstream: packages/coding-agent/test/debug/dap-multi-session.test.ts: "reactivates a live session when the active child terminates"
func TestDapMultiSessionReactivatesLiveSessionOnChildTerminate(t *testing.T) {
	adapter := DapResolvedAdapter{Name: "js-debug-adapter", ConnectMode: ConnectModeTCP}
	root := newFakeDapClient(adapter, "/tmp", fakeClientOpts{
		childConfig: map[string]any{"name": "target.js", "type": "pwa-node"},
	})
	child := newFakeDapClient(adapter, "/tmp", fakeClientOpts{stopAfterLaunch: true})

	mgr := NewSessionManager()
	defer mgr.Shutdown(context.Background())
	mgr.spawnFn = func(_ context.Context, _ SpawnOptions) (dapClient, error) { return root, nil }
	mgr.connectFn = func(_ context.Context, _ ConnectOptions) (dapClient, error) { return child, nil }

	launched, err := mgr.Launch(context.Background(), DapLaunchSessionOptions{Adapter: adapter, Program: "/tmp/target.js", Cwd: "/tmp"}, 2*time.Second)
	if err != nil || launched.ParentSessionID == "" {
		t.Fatalf("expected child launched, err=%v summary=%+v", err, launched)
	}

	child.emit("terminated", map[string]any{})
	_ = child.Dispose()

	active := mgr.GetActiveSession()
	if active == nil || active.ID == launched.ID || active.Status == StatusTerminated {
		t.Fatalf("expected active session reactivated to live root, got %+v", active)
	}
	_, threads, err := mgr.Threads(context.Background(), time.Second)
	if err != nil || len(threads) != 1 || root.countRequests("threads") != 1 {
		t.Fatalf("expected root threads after child termination, err=%v threads=%+v", err, threads)
	}
}

// upstream: packages/coding-agent/test/debug/dap-multi-session.test.ts: "keeps focus on the stopped script child when a worker attaches later"
// upstream: packages/coding-agent/test/debug/dap-multi-session.test.ts: "preserves per-session threads that share an id and name across children"
func TestDapMultiSessionFocusAndThreadAggregation(t *testing.T) {
	adapter := DapResolvedAdapter{Name: "js-debug-adapter", ConnectMode: ConnectModeTCP}
	root := newFakeDapClient(adapter, "/tmp", fakeClientOpts{
		childConfig: map[string]any{"name": "script.mts", "type": "pwa-node", "program": "/tmp/script.mts"},
		threads:     []DapThread{},
	})
	mainChild := newFakeDapClient(adapter, "/tmp", fakeClientOpts{
		stopAfterLaunch: true,
		stopThreadID:    1,
		threads:         []DapThread{{ID: 1, Name: "worker.js"}},
	})
	worker := newFakeDapClient(adapter, "/tmp", fakeClientOpts{
		stopAfterLaunch: false,
		threads:         []DapThread{{ID: 1, Name: "worker.js"}},
	})
	children := []*fakeDapClient{mainChild, worker}

	mgr := NewSessionManager()
	defer mgr.Shutdown(context.Background())
	mgr.spawnFn = func(_ context.Context, _ SpawnOptions) (dapClient, error) { return root, nil }
	mgr.connectFn = func(_ context.Context, _ ConnectOptions) (dapClient, error) {
		next := children[0]
		children = children[1:]
		return next, nil
	}

	launched, err := mgr.Launch(context.Background(), DapLaunchSessionOptions{Adapter: adapter, Program: "/tmp/script.mts", Cwd: "/tmp"}, 2*time.Second)
	if err != nil || launched.Status != StatusStopped {
		t.Fatalf("Launch failed: err=%v status=%s", err, launched.Status)
	}
	scriptID := launched.ID

	if _, err := root.triggerReverse("startDebugging", map[string]any{
		"request":       "launch",
		"configuration": map[string]any{"name": "[worker 1]", "type": "pwa-node"},
	}); err != nil {
		t.Fatalf("triggerReverse startDebugging: %v", err)
	}

	active := mgr.GetActiveSession()
	if active == nil || active.ID != scriptID {
		t.Fatalf("focus should remain on stopped script %s, got %+v", scriptID, active)
	}

	_, threads, err := mgr.Threads(context.Background(), 2*time.Second)
	if err != nil || len(threads) != 2 {
		t.Fatalf("expected 2 distinct per-session threads, got err=%v threads=%+v", err, threads)
	}
}

// upstream: packages/coding-agent/test/debug/dap-multi-session.test.ts: "drains a runInTerminal debuggee's stdout into the session output buffer"
func TestDapRunInTerminalOutputDrain(t *testing.T) {
	adapter := DapResolvedAdapter{Name: "js-debug-adapter", ConnectMode: ConnectModeStdio}
	root := newFakeDapClient(adapter, "/tmp", fakeClientOpts{stopAfterLaunch: true})
	mgr := NewSessionManager()
	defer mgr.Shutdown(context.Background())
	mgr.spawnFn = func(_ context.Context, _ SpawnOptions) (dapClient, error) { return root, nil }

	marker := "__RUNINTERMINAL_MARKER__"
	payload := strings.Repeat("x", 128*1024) + marker + "\ntail\n"
	drained := make(chan struct{})
	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write([]byte(payload))
		_ = pw.Close()
		close(drained)
	}()
	mgr.spawnTermFn = func(args []string, _ string, _ []string) (int, io.ReadCloser, *trackedTerminalProc, error) {
		return 4242, pr, nil, nil
	}

	if _, err := mgr.Launch(context.Background(), DapLaunchSessionOptions{Adapter: adapter, Program: "/tmp/target.js", Cwd: "/tmp"}, 2*time.Second); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if _, err := root.triggerReverse("runInTerminal", map[string]any{"args": []string{"/usr/bin/debuggee", "--verbose"}}); err != nil {
		t.Fatalf("runInTerminal: %v", err)
	}
	<-drained
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		out, err := mgr.GetOutput(0)
		if err == nil && strings.Contains(out.Output, marker) && out.Snapshot.OutputTruncated {
			if len(out.Output) > maxOutputBytes {
				t.Fatalf("output exceeded cap: %d", len(out.Output))
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	out, _ := mgr.GetOutput(0)
	t.Fatalf("marker not captured in bounded output: bytes=%d truncated=%v", out.Snapshot.OutputBytes, out.Snapshot.OutputTruncated)
}

// =============================================================================
// End-to-end tool tests against strict subprocess fake DAP adapter
// =============================================================================

func TestDebugToolEndToEndStrictStdioAdapter(t *testing.T) {
	isolateHome(t)
	cwd := t.TempDir()
	prog := filepath.Join(cwd, "main.go")
	_ = os.WriteFile(prog, []byte("package main\nfunc main() {}\n"), 0o644)

	adapter := helperAdapter(t, "stdio", ConnectModeStdio)
	tool := NewDebugTool(Config{}, nil)
	tool.selectLaunchAdapter = func(_, _, _ string, _ LaunchProgramKind, _ bool) LaunchAdapterSelection {
		return LaunchAdapterSelection{Kind: "adapter", Adapter: &adapter}
	}

	ext := sdk.New("oh-my-pig")
	ext.RegisterTool(tool.Definition())
	host := startHost(t, ext, cwd, "e2e-stdio")
	t.Cleanup(func() {
		tool.registry.shutdownKey(context.Background(), "e2e-stdio")
	})

	// 1. Launch (strict server rejects configurationDone before launch).
	res, errMsg := host.invokeTool("debug", map[string]any{"action": "launch", "program": "main.go"})
	if errMsg != "" {
		t.Fatalf("strict launch failed (check launch-before-configurationDone order): %s", errMsg)
	}
	content, _ := res["content"].(string)
	if !strings.Contains(content, "Status: stopped") || !strings.Contains(content, "Frame: main.compute") {
		t.Fatalf("unexpected launch content:\n%s", content)
	}

	// 2. Source & function breakpoints
	res, errMsg = host.invokeTool("debug", map[string]any{"action": "set_breakpoint", "file": "main.go", "line": 42})
	if errMsg != "" || !strings.Contains(res["content"].(string), "- line 42: verified") {
		t.Fatalf("set_breakpoint failed: err=%q res=%#v", errMsg, res)
	}
	res, errMsg = host.invokeTool("debug", map[string]any{"action": "set_breakpoint", "function": "main.compute"})
	if errMsg != "" || !strings.Contains(res["content"].(string), "- main.compute: verified") {
		t.Fatalf("set function breakpoint failed: err=%q res=%#v", errMsg, res)
	}

	// 3. Continue & steps
	res, errMsg = host.invokeTool("debug", map[string]any{"action": "continue"})
	if errMsg != "" || !strings.Contains(res["content"].(string), "Continue stopped at") || !strings.Contains(res["content"].(string), ":42:1") {
		t.Fatalf("continue failed: err=%q res=%#v", errMsg, res)
	}
	for _, tc := range []struct{ action, verb string }{
		{"step_over", "Step over"},
		{"step_in", "Step in"},
		{"step_out", "Step out"},
	} {
		res, errMsg = host.invokeTool("debug", map[string]any{"action": tc.action})
		if errMsg != "" || !strings.Contains(res["content"].(string), tc.verb+" stopped at") {
			t.Fatalf("%s failed: err=%q res=%#v", tc.action, errMsg, res)
		}
	}

	// 4. Threads, stack_trace, scopes, variables, evaluate
	res, errMsg = host.invokeTool("debug", map[string]any{"action": "threads"})
	if errMsg != "" || !strings.Contains(res["content"].(string), "- 1: main") {
		t.Fatalf("threads failed: err=%q res=%#v", errMsg, res)
	}
	res, errMsg = host.invokeTool("debug", map[string]any{"action": "stack_trace", "levels": 5})
	if errMsg != "" || !strings.Contains(res["content"].(string), "#1000 main.compute") {
		t.Fatalf("stack_trace failed: err=%q res=%#v", errMsg, res)
	}
	res, errMsg = host.invokeTool("debug", map[string]any{"action": "scopes"})
	if errMsg != "" || !strings.Contains(res["content"].(string), "Locals: ref=500") {
		t.Fatalf("scopes failed: err=%q res=%#v", errMsg, res)
	}
	res, errMsg = host.invokeTool("debug", map[string]any{"action": "variables", "scope_id": 500})
	if errMsg != "" || !strings.Contains(res["content"].(string), "- x = 42 (int)") {
		t.Fatalf("variables failed: err=%q res=%#v", errMsg, res)
	}
	res, errMsg = host.invokeTool("debug", map[string]any{"action": "evaluate", "expression": "x + 1"})
	if errMsg != "" || !strings.Contains(res["content"].(string), "Result: 43") {
		t.Fatalf("evaluate failed: err=%q res=%#v", errMsg, res)
	}

	// 5. Disassemble, memory, modules, sources
	res, errMsg = host.invokeTool("debug", map[string]any{"action": "disassemble", "instruction_count": 1})
	if errMsg != "" || !strings.Contains(res["content"].(string), "mov rbp, rsp") {
		t.Fatalf("disassemble failed: err=%q res=%#v", errMsg, res)
	}
	res, errMsg = host.invokeTool("debug", map[string]any{"action": "read_memory", "memory_reference": "0x2000", "count": 16})
	if errMsg != "" || !strings.Contains(res["content"].(string), "|Hello, DAP world|") {
		t.Fatalf("read_memory failed: err=%q res=%#v", errMsg, res)
	}
	res, errMsg = host.invokeTool("debug", map[string]any{"action": "write_memory", "memory_reference": "0x2000", "data": base64.StdEncoding.EncodeToString([]byte("OK"))})
	if errMsg != "" || !strings.Contains(res["content"].(string), "Bytes written: 2") {
		t.Fatalf("write_memory failed: err=%q res=%#v", errMsg, res)
	}
	res, errMsg = host.invokeTool("debug", map[string]any{"action": "modules"})
	if errMsg != "" || !strings.Contains(res["content"].(string), "0x1000-0x2000") {
		t.Fatalf("modules failed: err=%q res=%#v", errMsg, res)
	}
	res, errMsg = host.invokeTool("debug", map[string]any{"action": "loaded_sources"})
	if errMsg != "" || !strings.Contains(res["content"].(string), "/tmp/main.go") {
		t.Fatalf("loaded_sources failed: err=%q res=%#v", errMsg, res)
	}

	// 6. Instruction & data breakpoints
	res, errMsg = host.invokeTool("debug", map[string]any{"action": "set_instruction_breakpoint", "instruction_reference": "0x1000"})
	if errMsg != "" || !strings.Contains(res["content"].(string), "- 0x1000: verified") {
		t.Fatalf("set_instruction_breakpoint failed: err=%q res=%#v", errMsg, res)
	}
	res, errMsg = host.invokeTool("debug", map[string]any{"action": "remove_instruction_breakpoint", "instruction_reference": "0x1000"})
	if errMsg != "" || !strings.Contains(res["content"].(string), "(none)") {
		t.Fatalf("remove_instruction_breakpoint failed: err=%q res=%#v", errMsg, res)
	}
	res, errMsg = host.invokeTool("debug", map[string]any{"action": "data_breakpoint_info", "name": "x", "scope_id": 500})
	if errMsg != "" || !strings.Contains(res["content"].(string), "Data ID: watch-x") {
		t.Fatalf("data_breakpoint_info failed: err=%q res=%#v", errMsg, res)
	}
	res, errMsg = host.invokeTool("debug", map[string]any{"action": "set_data_breakpoint", "data_id": "watch-x", "access_type": "write"})
	if errMsg != "" || !strings.Contains(res["content"].(string), "- watch-x: verified (write)") {
		t.Fatalf("set_data_breakpoint failed: err=%q res=%#v", errMsg, res)
	}
	res, errMsg = host.invokeTool("debug", map[string]any{"action": "remove_data_breakpoint", "data_id": "watch-x"})
	if errMsg != "" || !strings.Contains(res["content"].(string), "(none)") {
		t.Fatalf("remove_data_breakpoint failed: err=%q res=%#v", errMsg, res)
	}

	// 7. Custom request, output, sessions, remove_breakpoint, pause, terminate
	res, errMsg = host.invokeTool("debug", map[string]any{"action": "custom_request", "command": "customPing"})
	if errMsg != "" || !strings.Contains(res["content"].(string), `"pong": true`) {
		t.Fatalf("custom_request failed: err=%q res=%#v", errMsg, res)
	}
	res, errMsg = host.invokeTool("debug", map[string]any{"action": "output"})
	if errMsg != "" || !strings.Contains(res["content"].(string), "started main.go") {
		t.Fatalf("output failed: err=%q res=%#v", errMsg, res)
	}
	res, errMsg = host.invokeTool("debug", map[string]any{"action": "sessions"})
	if errMsg != "" || !strings.Contains(res["content"].(string), "debug-1: stopped") {
		t.Fatalf("sessions failed: err=%q res=%#v", errMsg, res)
	}
	res, errMsg = host.invokeTool("debug", map[string]any{"action": "remove_breakpoint", "file": "main.go", "line": 42})
	if errMsg != "" || !strings.Contains(res["content"].(string), "(none)") {
		t.Fatalf("remove_breakpoint failed: err=%q res=%#v", errMsg, res)
	}
	res, errMsg = host.invokeTool("debug", map[string]any{"action": "pause"})
	if errMsg != "" || !strings.Contains(res["content"].(string), "Program paused.") {
		t.Fatalf("pause failed: err=%q res=%#v", errMsg, res)
	}
	res, errMsg = host.invokeTool("debug", map[string]any{"action": "terminate"})
	if errMsg != "" || !strings.Contains(res["content"].(string), "Debug session terminated.") {
		t.Fatalf("terminate failed: err=%q res=%#v", errMsg, res)
	}
	res, errMsg = host.invokeTool("debug", map[string]any{"action": "terminate"})
	if errMsg != "" || res["content"].(string) != "No debug session to terminate." {
		t.Fatalf("second terminate failed: err=%q res=%#v", errMsg, res)
	}
}

// TestContinueTimeoutReturnsStillRunningNotContextDeadline verifies that when a
// continue request has adapter latency and the debuggee never stops before the
// timeout, we return omp's "still running" outcome instead of Go's "context deadline exceeded".
func TestContinueTimeoutReturnsStillRunningNotContextDeadline(t *testing.T) {
	cwd := t.TempDir()
	prog := filepath.Join(cwd, "main.go")
	_ = os.WriteFile(prog, []byte("package main\nfunc main(){}\n"), 0o644)
	adapter := helperAdapter(t, "slow_continue", ConnectModeStdio)

	mgr := NewSessionManager()
	defer mgr.Shutdown(context.Background())

	if _, err := mgr.Launch(context.Background(), DapLaunchSessionOptions{Adapter: adapter, Program: prog, Cwd: cwd}, 5*time.Second); err != nil {
		t.Fatalf("Launch: %v", err)
	}

	outcome, err := mgr.Continue(context.Background(), 120*time.Millisecond)
	if err != nil {
		t.Fatalf("expected timedOut outcome instead of error %v", err)
	}
	if !outcome.TimedOut || outcome.State != "running" {
		t.Fatalf("expected state=running timedOut=true, got %+v", outcome)
	}
	text := buildOutcomeText(outcome, 5, "Continue")
	if !strings.Contains(text, "Program is still running after 5s. Use pause to interrupt and inspect state.") {
		t.Fatalf("unexpected timeout text:\n%s", text)
	}
}

func TestDebugToolTCPAndSocketAndRealChildTransports(t *testing.T) {
	t.Run("tcp mode", func(t *testing.T) {
		cwd := t.TempDir()
		prog := filepath.Join(cwd, "app.js")
		_ = os.WriteFile(prog, []byte("console.log(1)\n"), 0o644)
		adapter := helperAdapter(t, "tcp", ConnectModeTCP, dapPortArgument)

		mgr := NewSessionManager()
		defer mgr.Shutdown(context.Background())

		snap, err := mgr.Launch(context.Background(), DapLaunchSessionOptions{Adapter: adapter, Program: prog, Cwd: cwd}, 5*time.Second)
		if err != nil || snap.Status != StatusStopped {
			t.Fatalf("TCP Launch failed: err=%v snap=%+v", err, snap)
		}
		_, evalResp, err := mgr.Evaluate(context.Background(), "x + 1", "repl", nil, 5*time.Second)
		if err != nil || evalResp.Result != "43" {
			t.Fatalf("TCP Evaluate failed: err=%v resp=%+v", err, evalResp)
		}
		if _, err := mgr.Terminate(context.Background(), 5*time.Second); err != nil {
			t.Fatalf("TCP Terminate: %v", err)
		}
	})

	t.Run("real TCP child session via ConnectDapClient", func(t *testing.T) {
		cwd := t.TempDir()
		prog := filepath.Join(cwd, "child.js")
		_ = os.WriteFile(prog, []byte("console.log(1)\n"), 0o644)
		adapter := helperAdapter(t, "tcp_child", ConnectModeTCP, dapPortArgument)

		mgr := NewSessionManager()
		defer mgr.Shutdown(context.Background())

		snap, err := mgr.Launch(context.Background(), DapLaunchSessionOptions{Adapter: adapter, Program: prog, Cwd: cwd}, 5*time.Second)
		if err != nil {
			t.Fatalf("TCP child Launch failed: %v", err)
		}
		if snap.Status != StatusStopped || snap.ParentSessionID == "" {
			t.Fatalf("expected stopped child session over real TCP, got %+v", snap)
		}
		if len(mgr.ListSessions()) != 2 {
			t.Fatalf("expected 2 sessions (root+child), got %d", len(mgr.ListSessions()))
		}
		if _, err := mgr.Terminate(context.Background(), 5*time.Second); err != nil {
			t.Fatalf("TCP child Terminate: %v", err)
		}
	})

	t.Run("socket client-addr mode", func(t *testing.T) {
		cwd := t.TempDir()
		prog := filepath.Join(cwd, "main.go")
		_ = os.WriteFile(prog, []byte("package main\nfunc main(){}\n"), 0o644)
		adapter := helperAdapter(t, "socket_client_addr", ConnectModeSocket)

		mgr := NewSessionManager()
		defer mgr.Shutdown(context.Background())
		mgr.spawnFn = func(ctx context.Context, opts SpawnOptions) (dapClient, error) {
			opts.ForceClientAddr = true
			return SpawnDapClient(ctx, opts)
		}

		snap, err := mgr.Launch(context.Background(), DapLaunchSessionOptions{Adapter: adapter, Program: prog, Cwd: cwd}, 5*time.Second)
		if err != nil || snap.Status != StatusStopped {
			t.Fatalf("Socket Launch failed: err=%v snap=%+v", err, snap)
		}
		if _, err := mgr.Terminate(context.Background(), 5*time.Second); err != nil {
			t.Fatalf("Socket Terminate: %v", err)
		}
	})
}

// =============================================================================
// Project trust gating tests (integrator decision: trusted vs untrusted project)
// =============================================================================

func TestDebugToolProjectTrustGating(t *testing.T) {
	isolateHome(t)
	cwd := t.TempDir()
	prog := filepath.Join(cwd, "app.custom")
	_ = os.WriteFile(prog, []byte("hello\n"), 0o644)

	// Project dap.json points .custom at our strict fake adapter.
	exe, _ := os.Executable()
	t.Setenv("GO_WANT_DAP_HELPER", "stdio")
	dapCfg := map[string]any{
		"adapters": map[string]any{
			"proj-adapter": map[string]any{
				"command":   exe,
				"args":      []string{"-test.run=^TestHelperProcess$", "--"},
				"fileTypes": []string{".custom"},
			},
		},
	}
	raw, _ := json.Marshal(dapCfg)
	_ = os.WriteFile(filepath.Join(cwd, "dap.json"), raw, 0o644)

	// Subdir for testing project-local bin/dlv separately from app.custom (so go.mod doesn't match app.custom).
	goDir := filepath.Join(cwd, "go-proj")
	_ = os.MkdirAll(goDir, 0o755)
	_ = os.WriteFile(filepath.Join(goDir, "go.mod"), []byte("module untrusted\n\ngo 1.22\n"), 0o644)
	_ = os.WriteFile(filepath.Join(goDir, "main.go"), []byte("package main\nfunc main(){}\n"), 0o644)
	writeExecFile(t, filepath.Join(goDir, "bin", "dlv"))

	// Mock PATH lookup to fail bare commands like "dlv", while allowing absolute paths like exe.
	oldLook := lookPathFn
	lookPathFn = func(file string) (string, error) {
		if filepath.IsAbs(file) {
			return exec.LookPath(file)
		}
		return "", exec.ErrNotFound
	}
	t.Cleanup(func() { lookPathFn = oldLook })

	t.Run("untrusted skips project dap.json and project-local bin and notes once", func(t *testing.T) {
		ext := sdk.New("oh-my-pig")
		Register(ext)
		sid := "untrusted-session-1"
		host := startHostWithTrust(t, ext, cwd, sid, false)
		t.Cleanup(func() { lifecycle.End(sid) })

		// 1st call: project dap.json is ignored -> no adapter; note appears once.
		_, err1 := host.invokeTool("debug", map[string]any{"action": "launch", "program": "app.custom"})
		if !strings.Contains(err1, "No debugger adapter available") {
			t.Fatalf("expected project adapter skipped when untrusted, got %q", err1)
		}
		if !strings.Contains(err1, "project DAP configuration and project-local binaries were skipped because this project is not trusted") {
			t.Fatalf("expected untrusted project note on first call, got %q", err1)
		}

		// 2nd call in same session: project-local bin/dlv also skipped, note NOT repeated.
		_, err2 := host.invokeTool("debug", map[string]any{"action": "launch", "cwd": goDir, "program": "main.go"})
		if !strings.Contains(err2, "adapter 'dlv' is not available") {
			t.Fatalf("expected project-local bin/dlv skipped when untrusted, got %q", err2)
		}
		if strings.Contains(err2, "not trusted") {
			t.Fatalf("expected untrusted note only once per session, got repeated in %q", err2)
		}
	})

	t.Run("trusted loads project dap.json and launches cleanly", func(t *testing.T) {
		ext := sdk.New("oh-my-pig")
		Register(ext)
		sid := "trusted-session-1"
		host := startHostWithTrust(t, ext, cwd, sid, true)
		t.Cleanup(func() { lifecycle.End(sid) })

		res, errMsg := host.invokeTool("debug", map[string]any{"action": "launch", "program": "app.custom"})
		if errMsg != "" {
			t.Fatalf("expected trusted project adapter to launch, got %q", errMsg)
		}
		content, _ := res["content"].(string)
		if !strings.Contains(content, "Adapter: proj-adapter") || strings.Contains(content, "not trusted") {
			t.Fatalf("unexpected trusted launch content:\n%s", content)
		}
		_, _ = host.invokeTool("debug", map[string]any{"action": "terminate"})
	})
}

// =============================================================================
// Multi-session isolation, Register-twice, and internal/lifecycle contract tests
// =============================================================================

func TestLifecycleMultiSessionIsolationAndRegisterTwice(t *testing.T) {
	isolateHome(t)
	cwd := t.TempDir()
	prog := filepath.Join(cwd, "main.go")
	_ = os.WriteFile(prog, []byte("package main\nfunc main() {}\n"), 0o644)

	exe, _ := os.Executable()
	t.Setenv("GO_WANT_DAP_HELPER", "stdio")
	dapCfg := map[string]any{
		"adapters": map[string]any{
			"dlv": map[string]any{
				"command":     exe,
				"args":        []string{"-test.run=^TestHelperProcess$", "--"},
				"connectMode": "stdio",
			},
		},
	}
	raw, _ := json.Marshal(dapCfg)
	_ = os.WriteFile(filepath.Join(cwd, "dap.json"), raw, 0o644)

	// Simulate PiG building TWO fresh extension instances in one process and calling Register twice.
	ext1 := sdk.New("oh-my-pig")
	Register(ext1)
	ext2 := sdk.New("oh-my-pig")
	Register(ext2)

	sid1 := "lifecycle-session-1"
	sid2 := "lifecycle-session-2"
	host1 := startHost(t, ext1, cwd, sid1)
	host2 := startHost(t, ext2, cwd, sid2)
	t.Cleanup(func() {
		lifecycle.End(sid1)
		lifecycle.End(sid2)
	})

	// Launch a live adapter in each session.
	if _, errMsg := host1.invokeTool("debug", map[string]any{"action": "launch", "program": "main.go", "adapter": "dlv"}); errMsg != "" {
		t.Fatalf("session 1 launch failed: %s", errMsg)
	}
	if _, errMsg := host2.invokeTool("debug", map[string]any{"action": "launch", "program": "main.go", "adapter": "dlv"}); errMsg != "" {
		t.Fatalf("session 2 launch failed: %s", errMsg)
	}

	// Capture session 1's adapter PID via the shared defaultRegistry.
	mgr1 := defaultRegistry.forKey(sid1)
	mgr1.mu.Lock()
	var pid1 int
	for _, s := range mgr1.sessions {
		if dc, ok := s.client.(*DapClient); ok && dc.cmd != nil && dc.cmd.Process != nil {
			pid1 = dc.cmd.Process.Pid
		}
	}
	mgr1.mu.Unlock()
	if pid1 <= 0 {
		t.Fatal("failed to capture session 1 adapter PID")
	}

	// Shut down session 1 through Register's real session_shutdown hook -> lifecycle.End(sid1).
	host1.emitEvent(sdk.EventSessionShutdown, map[string]any{})
	// Calling lifecycle.End twice and for an unknown ID must be safe no-ops.
	lifecycle.End(sid1)
	lifecycle.End("never-seen-session")

	time.Sleep(150 * time.Millisecond)
	if runtime.GOOS != "windows" && isProcessAlive(pid1) {
		t.Fatalf("session 1 adapter pid %d survived session_shutdown / lifecycle.End", pid1)
	}

	// Session 2's adapter and state must still be completely intact and usable.
	res2, errMsg2 := host2.invokeTool("debug", map[string]any{"action": "evaluate", "expression": "x + 1"})
	if errMsg2 != "" || !strings.Contains(res2["content"].(string), "Result: 43") {
		t.Fatalf("session 2 broken after session 1 ended: err=%q res=%#v", errMsg2, res2)
	}

	// Ending session 2 via lifecycle.End (child-session path without session_shutdown) cleans it up too.
	lifecycle.End(sid2)
	resAfter, errAfter := host2.invokeTool("debug", map[string]any{"action": "sessions"})
	if errAfter != "" || resAfter["content"].(string) != "No debug sessions." {
		t.Fatalf("expected session 2 cleared after lifecycle.End, err=%q res=%#v", errAfter, resAfter)
	}
}

func TestEmptySessionIDRejectedAndDoesNotAlias(t *testing.T) {
	isolateHome(t)
	ext := sdk.New("oh-my-pig")
	Register(ext)
	host := startHost(t, ext, t.TempDir(), "")
	_, errMsg := host.invokeTool("debug", map[string]any{"action": "sessions"})
	if errMsg == "" || !strings.Contains(errMsg, "session ID") {
		t.Fatalf("expected error on empty session ID, got %q", errMsg)
	}
}

type dummyURLHandler struct{}

func (dummyURLHandler) Scheme() string { return "local" }
func (dummyURLHandler) Spec() urls.Spec {
	return urls.Spec{Backing: urls.BackingVirtual, Selectors: urls.SelectorsNone}
}
func (dummyURLHandler) Resolve(urls.Context, urls.URL) (*urls.Resource, error) { return nil, nil }

func TestResolveToCwdWorkspaceAliasAndInternalURLRejection(t *testing.T) {
	cwd := t.TempDir()
	got, err := resolveToCwd("/", cwd)
	if err != nil || got != cwd {
		t.Fatalf("expected '/' to alias cwd %q, got %q err=%v", cwd, got, err)
	}
	got3, err := resolveToCwd("///", cwd)
	if err != nil || got3 != cwd {
		t.Fatalf("expected '///' to alias cwd %q, got %q err=%v", cwd, got3, err)
	}
	_ = urls.Register(dummyURLHandler{})
	_, err = resolveToCwd("local://plan.md", cwd)
	if err == nil || !strings.Contains(err.Error(), `internal scheme "local://"`) {
		t.Fatalf("expected internal URL rejection, got %v", err)
	}
}

func TestRegisterNoIOAndNoPanic(t *testing.T) {
	ext := sdk.New("oh-my-pig")
	Register(ext)
	host := startHost(t, ext, t.TempDir(), "reg-check")
	tl, ok := host.tools["debug"]
	if !ok {
		t.Fatal("Register did not register debug tool")
	}
	if tl.ExecutionMode != "sequential" {
		t.Fatalf("expected sequential execution mode, got %q", tl.ExecutionMode)
	}
	if tl.Exposure != sdk.ToolExposureCodemode {
		t.Fatalf("expected Exposure %q, got %q", sdk.ToolExposureCodemode, tl.Exposure)
	}
	if tl.DefaultActive == nil || *tl.DefaultActive {
		t.Fatalf("expected DefaultActive=false, got %v", tl.DefaultActive)
	}
	props, _ := tl.Parameters["properties"].(map[string]any)
	actionSchema, _ := props["action"].(map[string]any)
	enumRaw, _ := actionSchema["enum"].([]any)
	if len(enumRaw) != len(AllDebugActions) {
		t.Fatalf("expected %d actions in schema enum, got %d", len(AllDebugActions), len(enumRaw))
	}
}

func TestDebugSchemaMatchesOmpCapturedSchema(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "schemaorder", "omp_schemas.json"))
	if err != nil {
		t.Fatalf("read omp_schemas.json: %v", err)
	}
	var captured map[string]struct {
		Parameters map[string]any `json:"parameters"`
	}
	if err := json.Unmarshal(raw, &captured); err != nil {
		t.Fatalf("unmarshal omp_schemas.json: %v", err)
	}
	ompEntry, ok := captured["debug"]
	if !ok {
		t.Fatal("omp_schemas.json missing debug entry")
	}
	gotBytes, err := json.Marshal(DebugParametersSchema())
	if err != nil {
		t.Fatalf("marshal DebugParametersSchema: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(gotBytes, &got); err != nil {
		t.Fatalf("unmarshal DebugParametersSchema: %v", err)
	}
	if !reflect.DeepEqual(got, ompEntry.Parameters) {
		wantJSON, _ := json.MarshalIndent(ompEntry.Parameters, "", "  ")
		gotJSON, _ := json.MarshalIndent(got, "", "  ")
		t.Fatalf("debug schema differs from omp v19.1.1:\n--- got ---\n%s\n--- want ---\n%s", gotJSON, wantJSON)
	}
}

// TestToolCmdMountableCodemodeExposure verifies the toolcmd contract under a
// fake host that enforces PiG's real exposure rule (direct tools callable only
// while active; codemode tools always callable via Context.ExecuteTool):
//   - With bash active and tools.commands=true, `debug` mounts as a command:
//     it stays in activeTools for direct fallback dispatch (omp resolveFallbackTool),
//     appears in toolcmd.MountedNames, is omitted from toolcmd.AdvertisedTools,
//     is hidden from model declarations by toolcmd.PrepareLoadout (HiddenDeclarations),
//     and dispatches both through bash -> toolcmd.TryDirect -> Context.ExecuteTool
//     and via a direct tool call (whereas an inactive `direct` tool is rejected).
//   - When Enabled("debug.enabled") returns false, `debug` is neither mounted nor active.
//   - When tools.commands is false, `debug` is advertised top-level and not hidden.
func TestToolCmdMountableCodemodeExposure(t *testing.T) {
	toolcmd.ResetForTests()
	t.Cleanup(toolcmd.ResetForTests)

	origDebugEnabled := Enabled
	origCmdEnabled := toolcmd.Enabled
	t.Cleanup(func() {
		Enabled = origDebugEnabled
		toolcmd.Enabled = origCmdEnabled
	})

	debugEnabled := true
	toolsCommandsOn := true
	Enabled = func(_ sdk.Context, key string, def bool) bool {
		if key == "debug.enabled" {
			return debugEnabled
		}
		return def
	}
	toolcmd.Enabled = func(_ sdk.Context, key string, def bool) bool {
		if key == "tools.commands" {
			return toolsCommandsOn
		}
		return def
	}

	ext := sdk.New("oh-my-pig")
	var capturedCtx sdk.Context
	ext.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		capturedCtx = ctx
		return nil, nil
	})
	// Register a minimal bash transport tool that runs toolcmd.TryDirect (or
	// executes a direct target to prove host direct-vs-codemode enforcement),
	// plus an inactive direct tool.
	ext.RegisterTool(sdk.ToolDefinition{
		Name:           "bash",
		Label:          "Bash",
		Description:    "Run shell commands",
		Parameters:     sdk.Schema{"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}, "target": map[string]any{"type": "string"}}},
		PrepareLoadout: toolcmd.PrepareLoadout,
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			if cmd, _ := params["command"].(string); cmd != "" {
				out, rc, handled := toolcmd.TryDirect(ctx, cmd)
				if !handled {
					return sdk.ToolResult{Content: "not handled", IsError: true}, nil
				}
				return sdk.ToolResult{Content: out, IsError: rc != 0}, nil
			}
			target, _ := params["target"].(string)
			if target == "" {
				return sdk.ToolResult{Content: "ok"}, nil
			}
			outcome, err := ctx.ExecuteTool(target, map[string]any{"action": "sessions"}, nil)
			if err != nil {
				return nil, err
			}
			if outcome.IsError {
				return nil, sdk.NewToolError(outcome.Result.Text())
			}
			return sdk.ToolResult{Content: outcome.Result.Text()}, nil
		},
	})
	inactiveDirect := false
	ext.RegisterTool(sdk.ToolDefinition{
		Name:          "inactive_direct",
		Label:         "InactiveDirect",
		Description:   "A direct tool that is not active",
		Parameters:    sdk.Schema{"type": "object"},
		Exposure:      sdk.ToolExposureDirect,
		DefaultActive: &inactiveDirect,
		Execute: func(sdk.Context, map[string]any) (any, error) {
			return sdk.ToolResult{Content: "should not be reached"}, nil
		},
	})

	Register(ext)
	toolcmd.Register(ext)

	sid := "toolcmd-codemode-session"
	host := startHost(t, ext, t.TempDir(), sid)
	t.Cleanup(func() { lifecycle.End(sid) })

	loadout := sdk.ToolLoadout{
		Declared: []sdk.AgentTool{
			{Name: "bash"},
			{Name: "debug"},
		},
		GetExposure: func(name string) sdk.ToolExposure {
			if tl, ok := host.tools[name]; ok {
				return tl.Exposure
			}
			return sdk.ToolExposureDirect
		},
	}

	// 1. Default: tools.commands=true, debug.enabled=true, bash active.
	// Mounted debug stays in activeTools (for direct fallback), appears in
	// MountedNames, is omitted from AdvertisedTools, and is hidden by PrepareLoadout.
	host.emitEvent(sdk.EventSessionStart, map[string]any{})
	active := host.getActiveTools()
	if !slices.Contains(active, "debug") {
		t.Fatalf("expected mounted debug to stay in activeTools for direct fallback, got %v", active)
	}
	if mounted := toolcmd.MountedNames(capturedCtx); !slices.Contains(mounted, "debug") {
		t.Fatalf("expected debug in toolcmd.MountedNames, got %v", mounted)
	}
	if advertised := toolcmd.AdvertisedTools(capturedCtx, active); slices.Contains(advertised, "debug") {
		t.Fatalf("expected mounted debug omitted from toolcmd.AdvertisedTools, got %v", advertised)
	}
	if changes := toolcmd.PrepareLoadout(loadout); changes == nil || !slices.Contains(changes.HiddenDeclarations, "debug") {
		t.Fatalf("expected toolcmd.PrepareLoadout to hide debug, got %+v", changes)
	}

	// Prove host enforces rule: calling inactive direct tool fails.
	_, directErr := host.invokeTool("bash", map[string]any{"target": "inactive_direct"})
	if !strings.Contains(directErr, `Tool "inactive_direct" not found`) {
		t.Fatalf("expected inactive direct tool to be rejected by host, got %q", directErr)
	}

	// Prove mounted `debug` dispatches through bash -> toolcmd.TryDirect -> Context.ExecuteTool.
	res, errMsg := host.invokeTool("bash", map[string]any{"command": "debug --action sessions"})
	if errMsg != "" {
		t.Fatalf("expected mounted debug to dispatch through bash, got error: %s", errMsg)
	}
	if got, _ := res["content"].(string); !strings.Contains(got, "No debug sessions.") {
		t.Fatalf("expected 'No debug sessions.' from bash dispatch, got %q", got)
	}

	// Prove direct call to mounted `debug` also dispatches (omp resolveFallbackTool).
	directRes, directCallErr := host.invokeTool("debug", map[string]any{"action": "sessions"})
	if directCallErr != "" {
		t.Fatalf("expected direct call to mounted debug to dispatch, got error: %s", directCallErr)
	}
	if got, _ := directRes["content"].(string); got != "No debug sessions." {
		t.Fatalf("expected 'No debug sessions.' from direct call, got %q", got)
	}

	// 2. When debug.enabled=false: neither mounted nor active.
	debugEnabled = false
	host.emitEvent(sdk.EventBeforeAgentStart, map[string]any{"systemPromptOptions": map[string]any{}})
	if slices.Contains(host.getActiveTools(), "debug") {
		t.Fatalf("expected debug inactive when debug.enabled=false, got %v", host.getActiveTools())
	}
	if mounted := toolcmd.MountedNames(capturedCtx); slices.Contains(mounted, "debug") {
		t.Fatalf("expected debug unmounted when debug.enabled=false, got %v", mounted)
	}

	// 3. When debug.enabled=true and tools.commands=false: advertised top-level and not hidden.
	debugEnabled = true
	toolsCommandsOn = false
	host.emitEvent(sdk.EventBeforeAgentStart, map[string]any{"systemPromptOptions": map[string]any{}})
	activeOff := host.getActiveTools()
	if !slices.Contains(activeOff, "debug") {
		t.Fatalf("expected debug activated top-level when tools.commands=false, got %v", activeOff)
	}
	if mounted := toolcmd.MountedNames(capturedCtx); slices.Contains(mounted, "debug") {
		t.Fatalf("expected debug not mounted when tools.commands=false, got %v", mounted)
	}
	if advertisedOff := toolcmd.AdvertisedTools(capturedCtx, activeOff); !slices.Contains(advertisedOff, "debug") {
		t.Fatalf("expected debug advertised top-level when tools.commands=false, got %v", advertisedOff)
	}
	if changesOff := toolcmd.PrepareLoadout(loadout); changesOff != nil && slices.Contains(changesOff.HiddenDeclarations, "debug") {
		t.Fatalf("expected debug not hidden by PrepareLoadout when tools.commands=false, got %+v", changesOff)
	}
}

// TestLaunchFailureBeforeInitializedReturnsPromptly verifies the real Delve
// failure exchange: initialize responds without emitting `initialized`, then
// launch fails with success=false and never sends `initialized`. Launch must
// return the error immediately instead of blocking for the full timeout waiting
// for `initialized`.
func TestLaunchFailureBeforeInitializedReturnsPromptly(t *testing.T) {
	cwd := t.TempDir()
	prog := filepath.Join(cwd, "main.go")
	_ = os.WriteFile(prog, []byte("package main\nfunc main(){}\n"), 0o644)
	adapter := helperAdapter(t, "dlv_launch_fail", ConnectModeStdio)

	mgr := NewSessionManager()
	defer mgr.Shutdown(context.Background())

	start := time.Now()
	_, err := mgr.Launch(context.Background(), DapLaunchSessionOptions{
		Adapter: adapter,
		Program: prog,
		Cwd:     cwd,
	}, 10*time.Second)
	if err == nil || !strings.Contains(err.Error(), "Failed to launch") {
		t.Fatalf("expected 'Failed to launch' error, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("launch failure waited %v instead of failing promptly", elapsed)
	}
}

// TestDebugLiveDlv exercises a real Delve (`dlv dap`) session when `dlv` is on PATH:
// launch, set breakpoint on `sum := a + b`, continue, stack_trace (`main.add`),
// scopes + variables (`a = 17`, `b = 25`), evaluate (`a + b` -> 42), step_out,
// continue to exit, output contains `42`, terminate leaves no dlv or debuggee PID.
func TestDebugLiveDlv(t *testing.T) {
	if _, err := exec.LookPath("dlv"); err != nil {
		t.Skip("dlv not on PATH; skipping live Delve integration test")
	}
	// Resolve the real Go toolchain paths BEFORE isolateHome(t) overrides HOME:
	// on workstations using a shim manager (e.g. mise's ~/.local/share/mise/shims/go),
	// changing HOME breaks the shim lookup and drops the build cache. We keep HOME
	// isolated for dap config isolation while passing the real GOROOT/bin first on PATH
	// and preserving GOCACHE/GOMODCACHE/GOPATH so `go build` works reliably.
	goEnvOut, err := exec.Command("go", "env", "GOROOT", "GOCACHE", "GOMODCACHE", "GOPATH").Output()
	if err != nil {
		t.Fatalf("go env failed: %v", err)
	}
	goEnvLines := strings.Split(strings.TrimSpace(string(goEnvOut)), "\n")
	if len(goEnvLines) < 4 {
		t.Fatalf("unexpected go env output: %q", string(goEnvOut))
	}
	goroot, gocache, gomodcache, gopath := goEnvLines[0], goEnvLines[1], goEnvLines[2], goEnvLines[3]
	goBin := filepath.Join(goroot, "bin", "go")
	if runtime.GOOS == "windows" {
		goBin += ".exe"
	}

	isolateHome(t)
	t.Setenv("GOROOT", goroot)
	t.Setenv("GOCACHE", gocache)
	t.Setenv("GOMODCACHE", gomodcache)
	t.Setenv("GOPATH", gopath)
	t.Setenv("GOFLAGS", "-mod=mod")
	t.Setenv("PATH", filepath.Join(goroot, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))

	// Resolve symlinks (macOS /var -> /private/var) so the source file paths
	// recorded in DWARF debug info match the resolved paths sent in setBreakpoints.
	cwd := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolved
	}
	goMod := "module example.com/tiny\n\ngo 1.22\n"
	mainGo := `package main

import "fmt"

func add(a, b int) int {
	sum := a + b
	return sum
}

func main() {
	x := 17
	y := 25
	z := add(x, y)
	fmt.Println(z)
}
`
	if err := os.WriteFile(filepath.Join(cwd, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(cwd, "main.go")
	if err := os.WriteFile(mainPath, []byte(mainGo), 0o644); err != nil {
		t.Fatal(err)
	}

	// Determine launch target. On macOS when Developer Mode is disabled,
	// debugserver refuses `task_for_pid` on binaries lacking the
	// `com.apple.security.get-task-allow` entitlement (go build's linker-signed
	// ad-hoc binary omits it). In that environment, pre-build with `-gcflags=all=-N -l`
	// and ad-hoc codesign with get-task-allow, then launch the binary through dlv
	// (which exercises dlv's mode=exec path); otherwise launch "." (mode=debug).
	launchProgram := "."
	if runtime.GOOS == "darwin" {
		devModeOut, _ := exec.Command("DevToolsSecurity", "-status").CombinedOutput()
		if strings.Contains(string(devModeOut), "disabled") {
			binPath := filepath.Join(cwd, "tiny")
			buildCmd := exec.Command(goBin, "build", "-gcflags=all=-N -l", "-o", binPath, ".")
			buildCmd.Dir = cwd
			if out, berr := buildCmd.CombinedOutput(); berr != nil {
				t.Fatalf("go build tiny failed: %v\n%s", berr, out)
			}
			entPath := filepath.Join(cwd, "ent.plist")
			entPlist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>com.apple.security.get-task-allow</key><true/></dict></plist>
`
			if err := os.WriteFile(entPath, []byte(entPlist), 0o644); err != nil {
				t.Fatal(err)
			}
			signCmd := exec.Command("codesign", "-f", "-s", "-", "--entitlements", entPath, binPath)
			if out, serr := signCmd.CombinedOutput(); serr != nil {
				t.Fatalf("codesign tiny failed: %v\n%s", serr, out)
			}
			launchProgram = binPath
		}
	}

	ext := sdk.New("oh-my-pig")
	Register(ext)
	sid := "live-dlv"
	host := startHost(t, ext, cwd, sid)
	t.Cleanup(func() {
		lifecycle.End(sid)
	})

	// 1. Launch via dlv dap.
	res, errMsg := host.invokeTool("debug", map[string]any{
		"action":  "launch",
		"program": launchProgram,
		"adapter": "dlv",
		"timeout": 30,
	})
	if errMsg != "" {
		t.Fatalf("dlv launch failed: %s", errMsg)
	}
	if !strings.Contains(res["content"].(string), "Adapter: dlv") {
		t.Fatalf("unexpected dlv launch output:\n%s", res["content"])
	}

	// Record dlv adapter PID and debuggee PID so we can assert neither survives terminate.
	mgr := defaultRegistry.forKey(sid)
	mgr.mu.Lock()
	var dlvPID, debuggeePID int
	for _, s := range mgr.sessions {
		if dc, ok := s.client.(*DapClient); ok && dc.cmd != nil && dc.cmd.Process != nil {
			dlvPID = dc.cmd.Process.Pid
		}
		if s.debuggeePID > 0 {
			debuggeePID = s.debuggeePID
		}
	}
	mgr.mu.Unlock()
	if dlvPID <= 0 {
		t.Fatal("failed to record live dlv PID")
	}
	t.Logf("live dlv started: dlvPID=%d debuggeePID=%d program=%s", dlvPID, debuggeePID, launchProgram)

	// 2. Set breakpoint on line 6 (`sum := a + b`).
	res, errMsg = host.invokeTool("debug", map[string]any{
		"action": "set_breakpoint",
		"file":   "main.go",
		"line":   6,
	})
	if errMsg != "" || !strings.Contains(res["content"].(string), "- line 6: verified") {
		t.Fatalf("dlv set_breakpoint failed: err=%q res=%#v", errMsg, res)
	}

	// 3. Continue to breakpoint in main.add.
	res, errMsg = host.invokeTool("debug", map[string]any{"action": "continue", "timeout": 30})
	if errMsg != "" || !strings.Contains(res["content"].(string), "Continue stopped at") || !strings.Contains(res["content"].(string), "main.go:6") {
		t.Fatalf("dlv continue to breakpoint failed: err=%q res=%#v", errMsg, res)
	}

	// 4. Stack trace shows main.add.
	res, errMsg = host.invokeTool("debug", map[string]any{"action": "stack_trace"})
	if errMsg != "" || !strings.Contains(res["content"].(string), "main.add") {
		t.Fatalf("dlv stack_trace failed: err=%q res=%#v", errMsg, res)
	}

	// 5. Scopes -> Variables show a = 17 and b = 25.
	res, errMsg = host.invokeTool("debug", map[string]any{"action": "scopes"})
	if errMsg != "" {
		t.Fatalf("dlv scopes failed: %s", errMsg)
	}
	details, _ := res["details"].(map[string]any)
	scopesRaw, _ := details["scopes"].([]any)
	if len(scopesRaw) == 0 {
		t.Fatalf("dlv returned no scopes: %#v", res)
	}
	firstScope, _ := scopesRaw[0].(map[string]any)
	ref, _ := firstScope["variablesReference"].(float64)

	res, errMsg = host.invokeTool("debug", map[string]any{"action": "variables", "scope_id": int(ref)})
	varsText, _ := res["content"].(string)
	if errMsg != "" || !strings.Contains(varsText, "- a = 17") || !strings.Contains(varsText, "- b = 25") {
		t.Fatalf("dlv variables failed: err=%q content=\n%s", errMsg, varsText)
	}

	// 6. Evaluate `a + b` -> 42.
	res, errMsg = host.invokeTool("debug", map[string]any{"action": "evaluate", "expression": "a + b"})
	if errMsg != "" || !strings.Contains(res["content"].(string), "Result: 42") {
		t.Fatalf("dlv evaluate failed: err=%q content=%v", errMsg, res["content"])
	}

	// 7. Step out back to main.main.
	res, errMsg = host.invokeTool("debug", map[string]any{"action": "step_out", "timeout": 30})
	if errMsg != "" || !strings.Contains(res["content"].(string), "Step out stopped at") {
		t.Fatalf("dlv step_out failed: err=%q res=%#v", errMsg, res)
	}

	// 8. Continue to program exit.
	res, errMsg = host.invokeTool("debug", map[string]any{"action": "continue", "timeout": 30})
	if errMsg != "" || !strings.Contains(res["content"].(string), "Program terminated with exit code 0.") {
		t.Fatalf("dlv continue to exit failed: err=%q res=%#v", errMsg, res)
	}

	// 9. Captured output contains `42`.
	res, errMsg = host.invokeTool("debug", map[string]any{"action": "output"})
	outText, _ := res["content"].(string)
	if errMsg != "" || !strings.Contains(outText, "42") {
		t.Fatalf("dlv output missing '42': err=%q output=%q", errMsg, outText)
	}

	// 10. Terminate and verify neither dlv nor debuggee PID remains alive.
	res, errMsg = host.invokeTool("debug", map[string]any{"action": "terminate"})
	if errMsg != "" || !strings.Contains(res["content"].(string), "Debug session terminated.") {
		t.Fatalf("dlv terminate failed: err=%q res=%#v", errMsg, res)
	}
	time.Sleep(150 * time.Millisecond)
	if runtime.GOOS != "windows" {
		if isProcessAlive(dlvPID) {
			t.Fatalf("dlv process %d still alive after terminate", dlvPID)
		}
		if debuggeePID > 0 && isProcessAlive(debuggeePID) {
			t.Fatalf("debuggee process %d still alive after terminate", debuggeePID)
		}
	}
}

func TestDebugMacOSPathStandardization(t *testing.T) {
	isolateHome(t)
	// Build a real directory under /private/tmp on darwin so EvalSymlinks sees
	// /private/tmp/... and /tmp/... resolving to the same location. On non-darwin
	// platforms, StandardizeMacOSPath must be a no-op.
	if runtime.GOOS == "darwin" {
		dir, err := os.MkdirTemp("/private/tmp", "omp-debug-stdpath-*")
		if err != nil {
			t.Fatalf("MkdirTemp under /private/tmp: %v", err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })

		if !strings.HasPrefix(dir, "/private/tmp/") {
			t.Fatalf("expected /private/tmp/ prefix, got %q", dir)
		}
		wantCwd := strings.TrimPrefix(dir, "/private")
		progPath := filepath.Join(dir, "app.py")
		wantProg := filepath.Join(wantCwd, "app.py")
		if err := os.WriteFile(progPath, []byte("x = 1\n"), 0o644); err != nil {
			t.Fatalf("write app.py: %v", err)
		}

		var spawnedClient *fakeDapClient
		mgr := NewSessionManager()
		t.Cleanup(func() { mgr.Shutdown(context.Background()) })
		mgr.spawnFn = func(_ context.Context, opts SpawnOptions) (dapClient, error) {
			c := newFakeDapClient(opts.Adapter, opts.Cwd, fakeClientOpts{
				stopAfterLaunch: true,
				stopThreadID:    1,
			})
			spawnedClient = c
			return c, nil
		}

		adapter := DapResolvedAdapter{
			Name:        "mockdap",
			Command:     "mockdap",
			ConnectMode: ConnectModeStdio,
		}
		tool := NewDebugTool(Config{}, mgr)
		tool.selectLaunchAdapter = func(_, _, _ string, _ LaunchProgramKind, _ bool) LaunchAdapterSelection {
			return LaunchAdapterSelection{Kind: "adapter", Adapter: &adapter}
		}

		ext := sdk.New("oh-my-pig")
		ext.RegisterTool(tool.Definition())
		host := startHost(t, ext, dir, "stdpath-darwin")

		// 1. launch: CWD and Program in both content and details.snapshot strip /private.
		res, errMsg := host.invokeTool("debug", map[string]any{"action": "launch", "program": "app.py"})
		if errMsg != "" {
			t.Fatalf("launch failed: %s", errMsg)
		}
		content, _ := res["content"].(string)
		if !strings.Contains(content, "CWD: "+wantCwd) {
			t.Fatalf("launch content missing standardized CWD %q:\n%s", wantCwd, content)
		}
		if !strings.Contains(content, "Program: "+wantProg) {
			t.Fatalf("launch content missing standardized Program %q:\n%s", wantProg, content)
		}
		if strings.Contains(content, "/private/tmp/") {
			t.Fatalf("launch content still contains /private/tmp/:\n%s", content)
		}
		details, _ := res["details"].(map[string]any)
		snap, _ := details["snapshot"].(map[string]any)
		if snap["cwd"] != wantCwd {
			t.Fatalf("details.snapshot.cwd = %v, want %q", snap["cwd"], wantCwd)
		}
		if snap["program"] != wantProg {
			t.Fatalf("details.snapshot.program = %v, want %q", snap["program"], wantProg)
		}

		// 2. sessions: cwd and program strip /private.
		res, errMsg = host.invokeTool("debug", map[string]any{"action": "sessions"})
		if errMsg != "" {
			t.Fatalf("sessions failed: %s", errMsg)
		}
		content, _ = res["content"].(string)
		if !strings.Contains(content, "cwd="+wantCwd) || !strings.Contains(content, "program="+wantProg) {
			t.Fatalf("sessions content not standardized:\n%s", content)
		}
		if strings.Contains(content, "/private/tmp/") {
			t.Fatalf("sessions content still contains /private/tmp/:\n%s", content)
		}

		// 3. set_breakpoint with /private/tmp path strips /private in header.
		res, errMsg = host.invokeTool("debug", map[string]any{"action": "set_breakpoint", "file": progPath, "line": 1})
		if errMsg != "" {
			t.Fatalf("set_breakpoint failed: %s", errMsg)
		}
		content, _ = res["content"].(string)
		if !strings.Contains(content, "Breakpoints for "+wantProg+":") {
			t.Fatalf("set_breakpoint content not standardized:\n%s", content)
		}
		if strings.Contains(content, "/private/tmp/") {
			t.Fatalf("set_breakpoint content still contains /private/tmp/:\n%s", content)
		}
		if spawnedClient == nil {
			t.Fatalf("expected spawnedClient to be set")
		}
		// Verify the DAP wire requests carried the unstandardized /private/tmp paths
		// (required so real adapters like dlv match DWARF debug info).
		var sawLaunch, sawSetBP bool
		spawnedClient.mu.Lock()
		reqs := append([]recordedRequest(nil), spawnedClient.requests...)
		spawnedClient.mu.Unlock()
		for _, req := range reqs {
			m, _ := req.Args.(map[string]any)
			switch req.Command {
			case "launch":
				sawLaunch = true
				if m["cwd"] != dir {
					t.Fatalf("wire launch cwd = %v, want unstandardized %q", m["cwd"], dir)
				}
				if m["program"] != progPath {
					t.Fatalf("wire launch program = %v, want unstandardized %q", m["program"], progPath)
				}
			case "setBreakpoints":
				sawSetBP = true
				src, _ := m["source"].(DapSource)
				if src.Path != progPath {
					t.Fatalf("wire setBreakpoints source.path = %q, want unstandardized %q", src.Path, progPath)
				}
			}
		}
		if !sawLaunch || !sawSetBP {
			t.Fatalf("expected both launch (%v) and setBreakpoints (%v) wire requests", sawLaunch, sawSetBP)
		}

		// 3b. remove_breakpoint also sends the unstandardized path on the wire
		// while formatting the standardized header.
		res, errMsg = host.invokeTool("debug", map[string]any{"action": "remove_breakpoint", "file": progPath, "line": 1})
		if errMsg != "" {
			t.Fatalf("remove_breakpoint failed: %s", errMsg)
		}
		content, _ = res["content"].(string)
		if !strings.Contains(content, "Breakpoints for "+wantProg+":") || strings.Contains(content, "/private/tmp/") {
			t.Fatalf("remove_breakpoint content not standardized:\n%s", content)
		}
		spawnedClient.mu.Lock()
		lastReq := spawnedClient.requests[len(spawnedClient.requests)-1]
		spawnedClient.mu.Unlock()
		if lastReq.Command != "setBreakpoints" {
			t.Fatalf("expected setBreakpoints for remove_breakpoint, got %q", lastReq.Command)
		}
		if m, _ := lastReq.Args.(map[string]any); m != nil {
			if src, _ := m["source"].(DapSource); src.Path != progPath {
				t.Fatalf("wire remove_breakpoint source.path = %q, want unstandardized %q", src.Path, progPath)
			}
		}

		// 4. stack_trace with adapter returning /private/tmp source path strips /private in both text and details.
		customMgr := NewSessionManager()
		t.Cleanup(func() { customMgr.Shutdown(context.Background()) })
		customMgr.spawnFn = func(_ context.Context, opts SpawnOptions) (dapClient, error) {
			return &privatePathClient{
				fakeDapClient: newFakeDapClient(opts.Adapter, opts.Cwd, fakeClientOpts{stopAfterLaunch: true, stopThreadID: 1}),
				sourcePath:    progPath,
			}, nil
		}
		tool2 := NewDebugTool(Config{}, customMgr)
		tool2.selectLaunchAdapter = func(_, _, _ string, _ LaunchProgramKind, _ bool) LaunchAdapterSelection {
			return LaunchAdapterSelection{Kind: "adapter", Adapter: &adapter}
		}
		ext2 := sdk.New("oh-my-pig")
		ext2.RegisterTool(tool2.Definition())
		host2 := startHost(t, ext2, dir, "stdpath-darwin-stack")
		_, errMsg = host2.invokeTool("debug", map[string]any{"action": "launch", "program": "app.py"})
		if errMsg != "" {
			t.Fatalf("launch2 failed: %s", errMsg)
		}
		res, errMsg = host2.invokeTool("debug", map[string]any{"action": "stack_trace"})
		if errMsg != "" {
			t.Fatalf("stack_trace failed: %s", errMsg)
		}
		content, _ = res["content"].(string)
		if !strings.Contains(content, wantProg+":5:1") || strings.Contains(content, "/private/tmp/") {
			t.Fatalf("stack_trace content not standardized:\n%s", content)
		}
		details, _ = res["details"].(map[string]any)
		frames, _ := details["stackFrames"].([]any)
		if len(frames) != 1 {
			t.Fatalf("expected 1 stackFrame, got %d", len(frames))
		}
		frame0, _ := frames[0].(map[string]any)
		src, _ := frame0["source"].(map[string]any)
		if src["path"] != wantProg {
			t.Fatalf("details.stackFrames[0].source.path = %v, want %q", src["path"], wantProg)
		}
		snap, _ = details["snapshot"].(map[string]any)
		snapSrc, _ := snap["source"].(map[string]any)
		if snapSrc["path"] != wantProg {
			t.Fatalf("details.snapshot.source.path = %v, want %q", snapSrc["path"], wantProg)
		}

		// 5. terminate strips /private in final snapshot.
		res, errMsg = host2.invokeTool("debug", map[string]any{"action": "terminate"})
		if errMsg != "" {
			t.Fatalf("terminate failed: %s", errMsg)
		}
		content, _ = res["content"].(string)
		if !strings.Contains(content, "CWD: "+wantCwd) || strings.Contains(content, "/private/tmp/") {
			t.Fatalf("terminate content not standardized:\n%s", content)
		}
	} else {
		// Non-darwin: /private/tmp/... must remain untouched (no-op).
		got, err := resolveToCwd("/private/tmp/example/app.py", "/private/tmp/example")
		if err != nil {
			t.Fatalf("resolveToCwd error: %v", err)
		}
		want := filepath.Clean("/private/tmp/example/app.py")
		if got != want {
			t.Fatalf("non-darwin resolveToCwd = %q, want %q", got, want)
		}
		src := &DapSource{Path: "/private/tmp/example/app.py"}
		standardizeSource(src)
		if src.Path != "/private/tmp/example/app.py" {
			t.Fatalf("non-darwin standardizeSource modified path to %q", src.Path)
		}
	}
}

type privatePathClient struct {
	*fakeDapClient
	sourcePath string
}

func (c *privatePathClient) SendRequest(ctx context.Context, command string, args any, timeout time.Duration) (json.RawMessage, error) {
	if command == "stackTrace" {
		raw, _ := json.Marshal(map[string]any{
			"stackFrames": []map[string]any{
				{"id": 10, "name": "main", "line": 5, "column": 1, "source": map[string]any{"name": "app.py", "path": c.sourcePath}},
			},
		})
		return raw, nil
	}
	return c.fakeDapClient.SendRequest(ctx, command, args, timeout)
}
