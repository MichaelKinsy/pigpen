package debug

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// upstream: packages/coding-agent/src/dap/client.ts

const (
	defaultRequestTimeout = 30 * time.Second
	writeMessageTimeout   = 30 * time.Second
	socketReadyTimeout    = 10 * time.Second
	readerExitGrace       = 200 * time.Millisecond
	stderrFlushGrace      = 30 * time.Millisecond
	maxStderrBytes        = 64 * 1024
)

// ErrOperationAborted matches omp's ToolAbortError.MESSAGE (tools/tool-errors.ts:14).
var ErrOperationAborted = errors.New("Operation aborted")

// SpawnOptions controls how SpawnDapClient starts an adapter.
type SpawnOptions struct {
	Adapter            DapResolvedAdapter
	Cwd                string
	SocketReadyTimeout time.Duration
	// ForceClientAddr forces the TCP --client-addr path in tests even on Linux.
	ForceClientAddr bool
}

// ConnectOptions controls how ConnectDapClient attaches to an existing TCP DAP server.
type ConnectOptions struct {
	Adapter DapResolvedAdapter
	Cwd     string
	Host    string
	Port    int
}

// DapEventHandler receives decoded DAP event bodies and envelopes.
type DapEventHandler func(body json.RawMessage, event DapEventMessage)

// DapReverseRequestHandler handles adapter-initiated DAP reverse requests (e.g. runInTerminal, startDebugging).
type DapReverseRequestHandler func(args json.RawMessage) (any, error)

// PendingDapCall is a DAP request whose sequence number has already been allocated
// and enqueued in wire order, awaiting its correlated response.
type PendingDapCall struct {
	await func(ctx context.Context) (json.RawMessage, error)
}

// Await blocks until the correlated response arrives, the timeout expires, or ctx is canceled.
func (p *PendingDapCall) Await(ctx context.Context) (json.RawMessage, error) {
	return p.await(ctx)
}

// dapClient is the transport contract consumed by DapSessionManager (implemented by *DapClient and test fakes).
type dapClient interface {
	Adapter() DapResolvedAdapter
	Cwd() string
	Port() int
	IsAlive() bool
	Exited() <-chan struct{}
	Capabilities() *DapCapabilities
	Initialize(ctx context.Context, args DapInitializeArguments, timeout time.Duration) (*DapCapabilities, error)
	StartRequest(ctx context.Context, command string, args any, timeout time.Duration) (*PendingDapCall, error)
	SendRequest(ctx context.Context, command string, args any, timeout time.Duration) (json.RawMessage, error)
	WaitForEvent(ctx context.Context, event string, predicate func(json.RawMessage) bool, timeout time.Duration) (json.RawMessage, error)
	OnEvent(event string, handler DapEventHandler) func()
	OnReverseRequest(command string, handler DapReverseRequestHandler) func()
	Dispose() error
}

// buildAdapterEnv merges NON_INTERACTIVE_ENV over the current environment.
// upstream: packages/coding-agent/src/exec/non-interactive-env.ts
func buildAdapterEnv(extra map[string]string) []string {
	envMap := make(map[string]string)
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			envMap[k] = v
		}
	}
	defaults := map[string]string{
		"PAGER":                          "cat",
		"GIT_PAGER":                      "cat",
		"MANPAGER":                       "cat",
		"SYSTEMD_PAGER":                  "cat",
		"BAT_PAGER":                      "cat",
		"DELTA_PAGER":                    "cat",
		"GH_PAGER":                       "cat",
		"GLAB_PAGER":                     "cat",
		"PSQL_PAGER":                     "cat",
		"MYSQL_PAGER":                    "cat",
		"AWS_PAGER":                      "",
		"HOMEBREW_PAGER":                 "cat",
		"LESS":                           "FRX",
		"TERM":                           "dumb",
		"NO_COLOR":                       "1",
		"PYTHONUNBUFFERED":               "1",
		"GIT_EDITOR":                     "true",
		"VISUAL":                         "true",
		"EDITOR":                         "true",
		"GIT_TERMINAL_PROMPT":            "0",
		"GIT_SEQUENCE_EDITOR":            "true",
		"GCM_INTERACTIVE":                "never",
		"SSH_ASKPASS":                    rejectPromptCommand(),
		"CI":                             "true",
		"AGENT":                          "1",
		"npm_config_yes":                 "true",
		"npm_config_update_notifier":     "false",
		"npm_config_fund":                "false",
		"npm_config_audit":               "false",
		"npm_config_progress":            "false",
		"PNPM_DISABLE_SELF_UPDATE_CHECK": "true",
		"PNPM_UPDATE_NOTIFIER":           "false",
		"YARN_ENABLE_TELEMETRY":          "0",
		"YARN_ENABLE_PROGRESS_BARS":      "0",
		"CARGO_TERM_PROGRESS_WHEN":       "never",
		"DEBIAN_FRONTEND":                "noninteractive",
		"PIP_NO_INPUT":                   "1",
		"PIP_DISABLE_PIP_VERSION_CHECK":  "1",
		"TF_INPUT":                       "0",
		"TF_IN_AUTOMATION":               "1",
		"GH_PROMPT_DISABLED":             "1",
		"COMPOSER_NO_INTERACTION":        "1",
		"CLOUDSDK_CORE_DISABLE_PROMPTS":  "1",
	}
	for k, v := range defaults {
		envMap[k] = v
	}
	for k, v := range extra {
		envMap[k] = v
	}
	out := make([]string, 0, len(envMap))
	for k, v := range envMap {
		out = append(out, k+"="+v)
	}
	return out
}

// stderrRing captures the tail of adapter stderr bounded by maxStderrBytes.
type stderrRing struct {
	mu  sync.Mutex
	buf []byte
}

func (r *stderrRing) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, p...)
	if len(r.buf) > maxStderrBytes {
		r.buf = append([]byte(nil), r.buf[len(r.buf)-maxStderrBytes:]...)
	}
	return len(p), nil
}

func (r *stderrRing) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.TrimSpace(string(r.buf))
}

func startStderrDrain(r io.ReadCloser, ring *stderrRing) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = r.Close() }()
		_, _ = io.Copy(ring, r)
	}()
	return done
}

// spawnedProc bundles an exec.Cmd with owned OS pipes so cmd.Wait() returns as
// soon as the adapter itself exits even when a descendant inherits stderr/stdout,
// and never races to close the stdout pipe while readLoop is still draining it.
type spawnedProc struct {
	cmd        *exec.Cmd
	stdin      *os.File
	stdout     *os.File
	stderrR    *os.File
	stderr     *stderrRing
	stderrDone <-chan struct{}
}

func startAdapterProcess(command string, args []string, cwd string) (*spawnedProc, error) {
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		_ = inR.Close()
		_ = inW.Close()
		return nil, err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		_ = inR.Close()
		_ = inW.Close()
		_ = outR.Close()
		_ = outW.Close()
		return nil, err
	}

	cmd := exec.Command(command, args...)
	cmd.Dir = cwd
	cmd.Env = buildAdapterEnv(nil)
	cmd.Stdin = inR
	cmd.Stdout = outW
	cmd.Stderr = errW
	configureProcessGroup(cmd)

	if err := cmd.Start(); err != nil {
		_ = inR.Close()
		_ = inW.Close()
		_ = outR.Close()
		_ = outW.Close()
		_ = errR.Close()
		_ = errW.Close()
		return nil, err
	}

	// Close child-side ends in parent immediately.
	_ = inR.Close()
	_ = outW.Close()
	_ = errW.Close()

	ring := &stderrRing{}
	stderrDone := startStderrDrain(errR, ring)

	return &spawnedProc{
		cmd:        cmd,
		stdin:      inW,
		stdout:     outR,
		stderrR:    errR,
		stderr:     ring,
		stderrDone: stderrDone,
	}, nil
}

type pendingResult struct {
	body json.RawMessage
	err  error
}

type pendingRequest struct {
	command string
	ch      chan pendingResult
}

type eventSubscription struct {
	id      uint64
	handler DapEventHandler
}

type writeJob struct {
	frame []byte
	done  chan error
}

// DapClient speaks DAP over stdio pipes, Unix domain sockets, or TCP sockets.
type DapClient struct {
	adapter DapResolvedAdapter
	cwd     string
	port    int

	cmd        *exec.Cmd
	stdinFile  io.Closer
	stderrR    io.Closer
	stderr     *stderrRing
	stderrDone <-chan struct{}
	reader     io.ReadCloser
	writer     io.WriteCloser
	socket     net.Conn
	socketPath string

	exited      chan struct{}
	exitOnce    sync.Once
	exitCodeMu  sync.Mutex
	hasExitCode bool
	exitCode    int

	readDone    chan struct{}
	releaseOnce sync.Once

	mu               sync.Mutex
	connClosed       bool
	closedErr        error
	requestSeq       int
	pending          map[int]*pendingRequest
	eventHandlers    map[string][]*eventSubscription
	anyEventHandlers []*eventSubscription
	reverseHandlers  map[string]DapReverseRequestHandler
	eventWaiters     map[uint64]func(error)
	nextSubID        uint64
	capabilities     *DapCapabilities
	lastActivity     time.Time

	writeCh   chan writeJob
	writeStop chan struct{}
	writeOnce sync.Once
}

func newDapClient(
	adapter DapResolvedAdapter,
	cwd string,
	sp *spawnedProc,
	reader io.ReadCloser,
	writer io.WriteCloser,
	socket net.Conn,
	port int,
	socketPath string,
	waitDone <-chan struct{},
	waitErrPtr *error,
) *DapClient {
	var cmd *exec.Cmd
	var stdinFile io.Closer
	var stderrR io.Closer
	var stderr *stderrRing
	var stderrDone <-chan struct{}
	if sp != nil {
		cmd = sp.cmd
		stdinFile = sp.stdin
		stderrR = sp.stderrR
		stderr = sp.stderr
		stderrDone = sp.stderrDone
	}
	c := &DapClient{
		adapter:         adapter,
		cwd:             cwd,
		port:            port,
		cmd:             cmd,
		stdinFile:       stdinFile,
		stderrR:         stderrR,
		stderr:          stderr,
		stderrDone:      stderrDone,
		reader:          reader,
		writer:          writer,
		socket:          socket,
		socketPath:      socketPath,
		exited:          make(chan struct{}),
		readDone:        make(chan struct{}),
		pending:         make(map[int]*pendingRequest),
		eventHandlers:   make(map[string][]*eventSubscription),
		reverseHandlers: make(map[string]DapReverseRequestHandler),
		eventWaiters:    make(map[uint64]func(error)),
		lastActivity:    time.Now(),
		writeCh:         make(chan writeJob, 128),
		writeStop:       make(chan struct{}),
	}
	go c.writeLoop()
	if cmd != nil {
		go func() {
			var err error
			if waitDone != nil {
				<-waitDone
				if waitErrPtr != nil {
					err = *waitErrPtr
				}
			} else {
				err = cmd.Wait()
			}
			code := 0
			if err != nil {
				var exitErr *exec.ExitError
				if errors.As(err, &exitErr) {
					code = exitErr.ExitCode()
				} else {
					code = -1
				}
			} else if cmd.ProcessState != nil {
				code = cmd.ProcessState.ExitCode()
			}
			c.markExited(code)
			c.handleProcessExit()
		}()
	}
	go c.readLoop()
	return c
}

// writeLoop serializes all DAP frames onto c.writer strictly in enqueue order
// so seq N is always written to the wire before seq N+1 (e.g. launch before configurationDone).
func (c *DapClient) writeLoop() {
	for {
		select {
		case job := <-c.writeCh:
			if c.writer == nil {
				job.done <- fmt.Errorf("DAP adapter %s is not running", c.adapter.Name)
				continue
			}
			_, err := c.writer.Write(job.frame)
			job.done <- err
		case <-c.writeStop:
			for {
				select {
				case job := <-c.writeCh:
					job.done <- fmt.Errorf("DAP adapter %s is not running", c.adapter.Name)
				default:
					return
				}
			}
		}
	}
}

func (c *DapClient) markExited(code int) {
	c.exitOnce.Do(func() {
		c.exitCodeMu.Lock()
		c.hasExitCode = true
		c.exitCode = code
		c.exitCodeMu.Unlock()
		close(c.exited)
	})
}

func (c *DapClient) processExitInfo() (exited bool, code int) {
	c.exitCodeMu.Lock()
	defer c.exitCodeMu.Unlock()
	return c.hasExitCode, c.exitCode
}

// SpawnDapClient starts a DAP adapter process according to adapter.ConnectMode.
func SpawnDapClient(ctx context.Context, opts SpawnOptions) (*DapClient, error) {
	switch opts.Adapter.ConnectMode {
	case ConnectModeSocket:
		return spawnSocketClient(ctx, opts)
	case ConnectModeTCP:
		return spawnTCPClient(ctx, opts)
	default:
		return spawnStdioClient(ctx, opts)
	}
}

// ConnectDapClient connects a child DAP session to an already-running TCP DAP server.
func ConnectDapClient(ctx context.Context, opts ConnectOptions) (*DapClient, error) {
	dialer := &net.Dialer{Timeout: socketReadyTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(opts.Host, strconv.Itoa(opts.Port)))
	if err != nil {
		return nil, err
	}
	c := newDapClient(opts.Adapter, opts.Cwd, nil, conn, conn, conn, opts.Port, "", nil, nil)
	return c, nil
}

func spawnStdioClient(_ context.Context, opts SpawnOptions) (*DapClient, error) {
	sp, err := startAdapterProcess(opts.Adapter.ResolvedCommand, opts.Adapter.Args, opts.Cwd)
	if err != nil {
		return nil, fmt.Errorf("failed to spawn DAP adapter %s: %w", opts.Adapter.Name, err)
	}
	return newDapClient(opts.Adapter, opts.Cwd, sp, sp.stdout, sp.stdin, nil, 0, "", nil, nil), nil
}

func spawnTCPClient(ctx context.Context, opts SpawnOptions) (*DapClient, error) {
	timeout := opts.SocketReadyTimeout
	if timeout <= 0 {
		timeout = socketReadyTimeout
	}
	host := "127.0.0.1"
	ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return nil, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	args := make([]string, len(opts.Adapter.Args))
	for i, a := range opts.Adapter.Args {
		args[i] = strings.ReplaceAll(a, dapPortArgument, strconv.Itoa(port))
	}

	sp, err := startAdapterProcess(opts.Adapter.ResolvedCommand, args, opts.Cwd)
	if err != nil {
		return nil, fmt.Errorf("failed to spawn DAP adapter %s: %w", opts.Adapter.Name, err)
	}

	var exited atomic.Bool
	waitDone := make(chan struct{})
	var waitErr error
	go func() {
		waitErr = sp.cmd.Wait()
		exited.Store(true)
		close(waitDone)
	}()

	cleanupFailed := func() {
		_ = sp.stdin.Close()
		killProcessTree(sp.cmd.Process)
		<-waitDone
		_ = sp.stdout.Close()
		_ = sp.stderrR.Close()
	}

	// Gate on stdout announcing the port (and keep draining stdout so child never blocks on pipe buffer).
	WaitForTcpServerListening(ctx, sp.stdout, port, timeout)

	conn, err := waitForTcpTransport(ctx, host, port, timeout, &exited)
	if err != nil {
		cleanupFailed()
		if s := sp.stderr.String(); s != "" {
			return nil, fmt.Errorf("%w: %s", err, s)
		}
		return nil, err
	}

	return newDapClient(opts.Adapter, opts.Cwd, sp, conn, conn, conn, port, "", waitDone, &waitErr), nil
}

func spawnSocketClient(ctx context.Context, opts SpawnOptions) (*DapClient, error) {
	timeout := opts.SocketReadyTimeout
	if timeout <= 0 {
		timeout = socketReadyTimeout
	}
	if useUnixSocketMode() && !opts.ForceClientAddr {
		return spawnSocketUnix(ctx, opts, timeout)
	}
	return spawnSocketClientAddr(ctx, opts, timeout)
}

func spawnSocketUnix(ctx context.Context, opts SpawnOptions, timeout time.Duration) (*DapClient, error) {
	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("dap-%s-%d-%06x.sock", opts.Adapter.Name, time.Now().UnixMilli(), rand.IntN(0xffffff)))
	args := append(append([]string(nil), opts.Adapter.Args...), "--listen=unix:"+socketPath)

	sp, err := startAdapterProcess(opts.Adapter.ResolvedCommand, args, opts.Cwd)
	if err != nil {
		return nil, fmt.Errorf("failed to spawn DAP adapter %s: %w", opts.Adapter.Name, err)
	}
	go func() {
		defer func() { _ = sp.stdout.Close() }()
		_, _ = io.Copy(io.Discard, sp.stdout)
	}()

	var exited atomic.Bool
	waitDone := make(chan struct{})
	var waitErr error
	go func() {
		waitErr = sp.cmd.Wait()
		exited.Store(true)
		close(waitDone)
	}()

	cleanupFailed := func() {
		_ = sp.stdin.Close()
		killProcessTree(sp.cmd.Process)
		<-waitDone
		_ = sp.stderrR.Close()
		_ = os.Remove(socketPath)
	}

	if err := waitForUnixSocket(ctx, socketPath, timeout, &exited); err != nil {
		cleanupFailed()
		if s := sp.stderr.String(); s != "" {
			return nil, fmt.Errorf("%w: %s", err, s)
		}
		return nil, err
	}
	conn, err := ConnectUnixSocket(ctx, socketPath, timeout)
	if err != nil {
		cleanupFailed()
		return nil, err
	}

	return newDapClient(opts.Adapter, opts.Cwd, sp, conn, conn, conn, 0, socketPath, waitDone, &waitErr), nil
}

func spawnSocketClientAddr(ctx context.Context, opts SpawnOptions, timeout time.Duration) (*DapClient, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	args := append(append([]string(nil), opts.Adapter.Args...), fmt.Sprintf("--client-addr=127.0.0.1:%d", port))

	sp, err := startAdapterProcess(opts.Adapter.ResolvedCommand, args, opts.Cwd)
	if err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("failed to spawn DAP adapter %s: %w", opts.Adapter.Name, err)
	}
	go func() {
		defer func() { _ = sp.stdout.Close() }()
		_, _ = io.Copy(io.Discard, sp.stdout)
	}()

	waitDone := make(chan struct{})
	var waitErr error
	go func() {
		waitErr = sp.cmd.Wait()
		close(waitDone)
	}()

	type acceptResult struct {
		conn net.Conn
		err  error
	}
	acceptCh := make(chan acceptResult, 1)
	go func() {
		conn, aerr := ln.Accept()
		acceptCh <- acceptResult{conn: conn, err: aerr}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	var conn net.Conn
	select {
	case res := <-acceptCh:
		_ = ln.Close()
		if res.err != nil {
			_ = sp.stdin.Close()
			killProcessTree(sp.cmd.Process)
			<-waitDone
			_ = sp.stderrR.Close()
			return nil, res.err
		}
		conn = res.conn
	case <-waitDone:
		_ = ln.Close()
		_ = sp.stdin.Close()
		select {
		case <-sp.stderrDone:
		case <-time.After(stderrFlushGrace):
		}
		msg := fmt.Sprintf("Adapter process %s exited before connecting", opts.Adapter.Name)
		if s := sp.stderr.String(); s != "" {
			msg += ": " + s
		}
		_ = sp.stderrR.Close()
		return nil, errors.New(msg)
	case <-timer.C:
		_ = ln.Close()
		_ = sp.stdin.Close()
		killProcessTree(sp.cmd.Process)
		<-waitDone
		_ = sp.stderrR.Close()
		return nil, fmt.Errorf("%s did not connect within %dms", opts.Adapter.Name, timeout.Milliseconds())
	case <-ctx.Done():
		_ = ln.Close()
		_ = sp.stdin.Close()
		killProcessTree(sp.cmd.Process)
		<-waitDone
		_ = sp.stderrR.Close()
		return nil, mapContextError(ctx.Err())
	}

	return newDapClient(opts.Adapter, opts.Cwd, sp, conn, conn, conn, 0, "", waitDone, &waitErr), nil
}

func mapContextError(err error) error {
	if errors.Is(err, context.Canceled) {
		return ErrOperationAborted
	}
	return err
}

// WaitForTcpServerListening drains stdout until it sees the reserved port banner, EOF, or timeout.
// upstream: packages/coding-agent/src/dap/client.ts:902-930
func WaitForTcpServerListening(ctx context.Context, stdout io.Reader, port int, timeout time.Duration) {
	portText := strconv.Itoa(port)
	ready := make(chan struct{})
	var once sync.Once
	signalReady := func() { once.Do(func() { close(ready) }) }

	go func() {
		buf := make([]byte, 4096)
		var acc string
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				acc += string(buf[:n])
				if strings.Contains(acc, portText) {
					signalReady()
				}
				if len(acc) > 4096 {
					acc = acc[len(acc)-1024:]
				}
			}
			if err != nil {
				signalReady()
				return
			}
		}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ready:
	case <-timer.C:
	case <-ctx.Done():
	}
}

func waitForTcpTransport(ctx context.Context, host string, port int, timeout time.Duration, exited *atomic.Bool) (net.Conn, error) {
	deadline := time.Now().Add(timeout)
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	for time.Now().Before(deadline) {
		if exited != nil && exited.Load() {
			return nil, fmt.Errorf("Adapter process exited before TCP port %s was ready", addr)
		}
		if ctx.Err() != nil {
			return nil, mapContextError(ctx.Err())
		}
		dialer := &net.Dialer{Timeout: 500 * time.Millisecond}
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		if err == nil {
			return conn, nil
		}
		select {
		case <-ctx.Done():
			return nil, mapContextError(ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
	return nil, fmt.Errorf("TCP port %s was not ready after %dms", addr, timeout.Milliseconds())
}

func waitForUnixSocket(ctx context.Context, socketPath string, timeout time.Duration, exited *atomic.Bool) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ready, err := isUnixSocketReady(socketPath)
		if err != nil {
			return err
		}
		if ready {
			return nil
		}
		if exited != nil && exited.Load() {
			return errors.New("Adapter process exited before socket was ready")
		}
		select {
		case <-ctx.Done():
			return mapContextError(ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
	return fmt.Errorf("Socket not ready after %dms", timeout.Milliseconds())
}

// ConnectUnixSocket dials a Unix domain socket bounded by timeout.
// upstream: packages/coding-agent/src/dap/client.ts:961-1026
func ConnectUnixSocket(ctx context.Context, socketPath string, timeout time.Duration) (net.Conn, error) {
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var dialer net.Dialer
	conn, err := dialer.DialContext(dialCtx, "unix", socketPath)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func (c *DapClient) Adapter() DapResolvedAdapter { return c.adapter }
func (c *DapClient) Cwd() string                 { return c.cwd }
func (c *DapClient) Port() int                   { return c.port }
func (c *DapClient) Exited() <-chan struct{}     { return c.exited }

func (c *DapClient) Capabilities() *DapCapabilities {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.capabilities
}

func (c *DapClient) IsAlive() bool {
	c.mu.Lock()
	closed := c.connClosed
	c.mu.Unlock()
	if closed {
		return false
	}
	exited, _ := c.processExitInfo()
	return !exited
}

// Initialize sends the DAP initialize request and caches capabilities.
func (c *DapClient) Initialize(ctx context.Context, args DapInitializeArguments, timeout time.Duration) (*DapCapabilities, error) {
	body, err := c.SendRequest(ctx, "initialize", args, timeout)
	if err != nil {
		return nil, err
	}
	caps := &DapCapabilities{}
	if len(body) > 0 && string(body) != "null" {
		if err := json.Unmarshal(body, caps); err != nil {
			return nil, fmt.Errorf("failed to parse DAP capabilities: %w", err)
		}
	}
	c.mu.Lock()
	c.capabilities = caps
	c.mu.Unlock()
	return caps, nil
}

// OnEvent subscribes a handler for a specific DAP event name and returns an unsubscribe func.
func (c *DapClient) OnEvent(event string, handler DapEventHandler) func() {
	c.mu.Lock()
	c.nextSubID++
	id := c.nextSubID
	c.eventHandlers[event] = append(c.eventHandlers[event], &eventSubscription{id: id, handler: handler})
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		subs := c.eventHandlers[event]
		for i, s := range subs {
			if s.id == id {
				c.eventHandlers[event] = append(subs[:i], subs[i+1:]...)
				break
			}
		}
		if len(c.eventHandlers[event]) == 0 {
			delete(c.eventHandlers, event)
		}
	}
}

// OnReverseRequest registers a handler for adapter-initiated reverse requests.
func (c *DapClient) OnReverseRequest(command string, handler DapReverseRequestHandler) func() {
	c.mu.Lock()
	c.reverseHandlers[command] = handler
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		delete(c.reverseHandlers, command)
	}
}

// WaitForEvent waits until a matching DAP event arrives, transport closes, timeout expires, or ctx cancels.
func (c *DapClient) WaitForEvent(
	ctx context.Context,
	event string,
	predicate func(json.RawMessage) bool,
	timeout time.Duration,
) (json.RawMessage, error) {
	if ctx.Err() != nil {
		return nil, mapContextError(ctx.Err())
	}
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}
	type waitOutcome struct {
		body json.RawMessage
		err  error
	}
	ch := make(chan waitOutcome, 1)
	var once sync.Once
	deliver := func(body json.RawMessage, err error) {
		once.Do(func() {
			ch <- waitOutcome{body: body, err: err}
		})
	}

	c.mu.Lock()
	if c.connClosed || c.closedErr != nil {
		err := c.closedErr
		c.mu.Unlock()
		if err == nil {
			err = fmt.Errorf("DAP connection closed: %s transport ended", c.adapter.Name)
		}
		return nil, err
	}
	c.nextSubID++
	waiterID := c.nextSubID
	c.eventWaiters[waiterID] = func(err error) {
		deliver(nil, err)
	}
	c.mu.Unlock()

	unsub := c.OnEvent(event, func(body json.RawMessage, _ DapEventMessage) {
		if predicate != nil && !predicate(body) {
			return
		}
		deliver(body, nil)
	})
	defer func() {
		unsub()
		c.mu.Lock()
		delete(c.eventWaiters, waiterID)
		c.mu.Unlock()
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case out := <-ch:
		return out.body, out.err
	case <-ctx.Done():
		return nil, mapContextError(ctx.Err())
	case <-timer.C:
		return nil, fmt.Errorf("DAP event %s timed out after %dms", event, timeout.Milliseconds())
	}
}

// StartRequest synchronously allocates a DAP sequence number, registers the
// pending entry, and enqueues the framed message in FIFO wire order before
// returning a handle to await the response.
// upstream: packages/coding-agent/src/dap/client.ts:434-502
func (c *DapClient) StartRequest(ctx context.Context, command string, args any, timeout time.Duration) (*PendingDapCall, error) {
	if ctx.Err() != nil {
		return nil, mapContextError(ctx.Err())
	}
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}

	c.mu.Lock()
	if c.connClosed {
		c.mu.Unlock()
		return nil, fmt.Errorf("DAP adapter %s is not running", c.adapter.Name)
	}
	c.requestSeq++
	seq := c.requestSeq
	pr := &pendingRequest{
		command: command,
		ch:      make(chan pendingResult, 1),
	}
	c.pending[seq] = pr
	c.lastActivity = time.Now()
	req := DapRequestMessage{
		Seq:       seq,
		Type:      "request",
		Command:   command,
		Arguments: args,
	}
	frame, err := EncodeMessageFrame(req)
	if err != nil {
		delete(c.pending, seq)
		c.mu.Unlock()
		return nil, err
	}
	// Enqueue under c.mu so wire order matches sequence-number order even across concurrent callers.
	job := writeJob{frame: frame, done: make(chan error, 1)}
	select {
	case c.writeCh <- job:
	case <-c.writeStop:
		delete(c.pending, seq)
		c.mu.Unlock()
		return nil, fmt.Errorf("DAP adapter %s is not running", c.adapter.Name)
	}
	c.mu.Unlock()

	// Watch the write completion in the background so a wedged stdin never blocks
	// the caller's request timeout (matching dap/client.ts:492-500).
	go func() {
		if werr := c.awaitWriteJob(job); werr != nil {
			c.mu.Lock()
			if p, ok := c.pending[seq]; ok {
				delete(c.pending, seq)
				p.ch <- pendingResult{err: werr}
			}
			c.mu.Unlock()
		}
	}()

	return &PendingDapCall{
		await: func(waitCtx context.Context) (json.RawMessage, error) {
			timer := time.NewTimer(timeout)
			defer timer.Stop()
			select {
			case res := <-pr.ch:
				return res.body, res.err
			case <-waitCtx.Done():
				c.mu.Lock()
				delete(c.pending, seq)
				c.mu.Unlock()
				return nil, mapContextError(waitCtx.Err())
			case <-timer.C:
				c.mu.Lock()
				delete(c.pending, seq)
				c.mu.Unlock()
				return nil, fmt.Errorf("DAP request %s timed out after %dms", command, timeout.Milliseconds())
			}
		},
	}, nil
}

// SendRequest sends a DAP request and awaits its correlated response.
func (c *DapClient) SendRequest(ctx context.Context, command string, args any, timeout time.Duration) (json.RawMessage, error) {
	call, err := c.StartRequest(ctx, command, args, timeout)
	if err != nil {
		return nil, err
	}
	return call.Await(ctx)
}

// SendResponse writes a DAP response to an adapter-initiated reverse request.
func (c *DapClient) SendResponse(reqSeq int, command string, success bool, body any, message string) error {
	c.mu.Lock()
	if c.connClosed {
		c.mu.Unlock()
		return fmt.Errorf("DAP adapter %s is not running", c.adapter.Name)
	}
	c.requestSeq++
	seq := c.requestSeq
	resp := DapResponseMessage{
		Seq:        seq,
		Type:       "response",
		RequestSeq: reqSeq,
		Success:    success,
		Command:    command,
		Message:    message,
		Body:       body,
	}
	frame, err := EncodeMessageFrame(resp)
	if err != nil {
		c.mu.Unlock()
		return err
	}
	job := writeJob{frame: frame, done: make(chan error, 1)}
	select {
	case c.writeCh <- job:
	case <-c.writeStop:
		c.mu.Unlock()
		return fmt.Errorf("DAP adapter %s is not running", c.adapter.Name)
	}
	c.mu.Unlock()
	return c.awaitWriteJob(job)
}

func (c *DapClient) awaitWriteJob(job writeJob) error {
	timer := time.NewTimer(writeMessageTimeout)
	defer timer.Stop()

	select {
	case err := <-job.done:
		if err != nil {
			go func() { _ = c.Dispose() }()
			return err
		}
		return nil
	case <-c.exited:
		go func() { _ = c.Dispose() }()
		return fmt.Errorf("DAP adapter %s exited before write completed", c.adapter.Name)
	case <-timer.C:
		go func() { _ = c.Dispose() }()
		return fmt.Errorf("DAP adapter %s write timed out after %dms", c.adapter.Name, writeMessageTimeout.Milliseconds())
	}
}

// shouldKillAdapterTree decides whether Dispose should invoke killProcessTree
// for the adapter process. On Unix we always signal the process group (-pid),
// which is safe even after the adapter leader has been reaped and ensures no
// orphaned debuggee descendants outlive the session; on Windows, taskkill
// targets a raw PID with no process group, so once the adapter has already
// exited and been reaped, skipping it avoids killing an unrelated process that
// reused the PID.
func shouldKillAdapterTree(goos string, exited bool) bool {
	if goos == "windows" && exited {
		return false
	}
	return true
}

// Dispose tears down the transport, removes any Unix socket file, and kills the
// adapter's process tree (on Unix even after self-exit; on Windows only while
// the adapter has not yet been reaped).
func (c *DapClient) Dispose() error {
	c.mu.Lock()
	alreadyClosed := c.connClosed
	c.connClosed = true
	c.mu.Unlock()

	if !alreadyClosed {
		c.failConnection(fmt.Errorf("DAP adapter %s disposed", c.adapter.Name))
	}

	c.releaseOnce.Do(func() {
		c.writeOnce.Do(func() { close(c.writeStop) })

		if c.socket != nil {
			_ = c.socket.Close()
		}
		if c.stdinFile != nil {
			_ = c.stdinFile.Close()
		} else if c.writer != nil && c.socket == nil {
			_ = c.writer.Close()
		}
		if c.reader != nil && c.socket == nil {
			_ = c.reader.Close()
		}
		if c.stderrR != nil {
			_ = c.stderrR.Close()
		}

		if c.cmd != nil && c.cmd.Process != nil {
			exited := false
			select {
			case <-c.exited:
				exited = true
			default:
			}
			if shouldKillAdapterTree(runtime.GOOS, exited) {
				killProcessTree(c.cmd.Process)
				select {
				case <-c.exited:
				case <-time.After(2 * time.Second):
				}
			}
		} else {
			c.markExited(0)
		}

		if c.socketPath != "" {
			_ = os.Remove(c.socketPath)
		}
	})
	return nil
}

func (c *DapClient) readLoop() {
	defer close(c.readDone)
	framer := NewMessageFramer(nil)
	buf := make([]byte, 16*1024)
	var closeErr error
	framingFailed := false

	for {
		n, rerr := c.reader.Read(buf)
		if n > 0 {
			if err := framer.Push(buf[:n]); err != nil {
				framingFailed = isFramingError(err)
				closeErr = fmt.Errorf("DAP connection closed: %w", err)
				break
			}
			msgs, derr := framer.Drain(func(_ string) {})
			for _, raw := range msgs {
				c.mu.Lock()
				c.lastActivity = time.Now()
				c.mu.Unlock()
				c.handleIncomingMessage(raw)
			}
			if derr != nil {
				framingFailed = isFramingError(derr)
				closeErr = fmt.Errorf("DAP connection closed: %w", derr)
				break
			}
		}
		if rerr != nil {
			if !errors.Is(rerr, io.EOF) && !errors.Is(rerr, net.ErrClosed) {
				closeErr = fmt.Errorf("DAP connection closed: %w", rerr)
			}
			break
		}
	}

	// On clean stream end, wait briefly for the process exit to land so callers see exit code and stderr.
	if closeErr == nil && c.cmd != nil {
		select {
		case <-c.exited:
			if c.stderrDone != nil {
				select {
				case <-c.stderrDone:
				case <-time.After(stderrFlushGrace):
				}
			}
			_, code := c.processExitInfo()
			stderrText := ""
			if c.stderr != nil {
				stderrText = c.stderr.String()
			}
			if stderrText != "" {
				closeErr = fmt.Errorf("DAP adapter exited (code %d): %s", code, stderrText)
			} else {
				closeErr = fmt.Errorf("DAP adapter exited unexpectedly (code %d)", code)
			}
		case <-time.After(readerExitGrace):
		}
	}
	if c.cmd == nil {
		c.markExited(0)
	}

	if closeErr == nil {
		closeErr = fmt.Errorf("DAP connection closed: %s transport ended", c.adapter.Name)
	}
	c.failConnection(closeErr)

	if framingFailed {
		_ = c.Dispose()
	}
}

func (c *DapClient) handleIncomingMessage(raw []byte) {
	var env DapProtocolMessage
	if err := json.Unmarshal(raw, &env); err != nil {
		return
	}
	switch env.Type {
	case "response":
		c.handleResponse(env)
	case "event":
		c.dispatchEvent(DapEventMessage{
			Seq:   env.Seq,
			Type:  "event",
			Event: env.Event,
			Body:  env.Body,
		})
	case "request":
		go c.handleAdapterRequest(env)
	}
}

// handleResponse matches upstream packages/coding-agent/src/dap/client.ts:677-689.
func (c *DapClient) handleResponse(env DapProtocolMessage) {
	c.mu.Lock()
	pr, ok := c.pending[env.RequestSeq]
	if ok {
		delete(c.pending, env.RequestSeq)
	}
	c.mu.Unlock()
	if !ok {
		return
	}
	if env.Success {
		pr.ch <- pendingResult{body: env.Body}
		return
	}
	msg := env.Message
	if msg == "" {
		msg = fmt.Sprintf("DAP request %s failed", pr.command)
	}
	pr.ch <- pendingResult{err: errors.New(msg)}
}

func (c *DapClient) dispatchEvent(msg DapEventMessage) {
	c.mu.Lock()
	specific := append([]*eventSubscription(nil), c.eventHandlers[msg.Event]...)
	anySubs := append([]*eventSubscription(nil), c.anyEventHandlers...)
	c.mu.Unlock()

	for _, sub := range specific {
		sub.handler(msg.Body, msg)
	}
	for _, sub := range anySubs {
		sub.handler(msg.Body, msg)
	}
}

func (c *DapClient) handleAdapterRequest(env DapProtocolMessage) {
	c.mu.Lock()
	handler := c.reverseHandlers[env.Command]
	c.mu.Unlock()
	if handler != nil {
		body, err := handler(env.Arguments)
		if err != nil {
			msg := err.Error()
			_ = c.SendResponse(env.Seq, env.Command, false, map[string]any{
				"error": map[string]any{"id": 1, "format": msg},
			}, msg)
			return
		}
		_ = c.SendResponse(env.Seq, env.Command, true, body, "")
		return
	}
	msg := fmt.Sprintf("Unsupported DAP request: %s", env.Command)
	_ = c.SendResponse(env.Seq, env.Command, false, map[string]any{
		"error": map[string]any{"id": 1, "format": msg},
	}, msg)
}

func (c *DapClient) handleProcessExit() {
	// Wait briefly for readLoop to drain any final events (exited/terminated)
	// and for stderr to flush, without hanging if a descendant inherited either pipe.
	select {
	case <-c.readDone:
	case <-time.After(stderrFlushGrace):
	}
	if c.stderrDone != nil {
		select {
		case <-c.stderrDone:
		case <-time.After(stderrFlushGrace):
		}
	}

	c.mu.Lock()
	if c.connClosed {
		c.mu.Unlock()
		return
	}
	c.connClosed = true
	c.mu.Unlock()

	_, code := c.processExitInfo()
	stderrText := ""
	if c.stderr != nil {
		stderrText = c.stderr.String()
	}
	var err error
	if stderrText != "" {
		err = fmt.Errorf("DAP adapter exited (code %d): %s", code, stderrText)
	} else {
		err = fmt.Errorf("DAP adapter exited unexpectedly (code %d)", code)
	}
	c.failConnection(err)
}

func (c *DapClient) failConnection(err error) {
	c.mu.Lock()
	if c.closedErr == nil {
		c.closedErr = err
	}
	pending := c.pending
	c.pending = make(map[int]*pendingRequest)
	waiters := c.eventWaiters
	c.eventWaiters = make(map[uint64]func(error))
	c.mu.Unlock()

	for _, pr := range pending {
		select {
		case pr.ch <- pendingResult{err: err}:
		default:
		}
	}
	for _, w := range waiters {
		w(err)
	}
}
