package toolcmd

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// upstream: packages/coding-agent/src/exec/tool-command-host.ts
// upstream: packages/coding-agent/src/tools/bash.ts (isToolCommandLine, invokedToolCommand)

const (
	// maxPreAuthConnections caps concurrent unauthenticated TCP connections
	// reading their first TOKEN line (5s deadline), so an unauthenticated peer
	// can never starve authenticated tool command calls.
	maxPreAuthConnections = 8
	// maxAuthConnections caps concurrent authenticated tool command executions
	// per session listener; authenticated clients wait for a slot rather than
	// being dropped.
	maxAuthConnections = 32
	// maxTokenLineBytes caps the unauthenticated first line (`TOKEN <48 hex>\n`).
	maxTokenLineBytes  = 128
	maxHeaderLineBytes = 64 * 1024
	// maxArgBudgetBytes caps total encoded argument bytes across all ARG lines.
	maxArgBudgetBytes = 4 * 1024 * 1024
	// maxEncodedStdinBytes caps base64-encoded stdin (16 MiB raw ≈ 21.33 MiB b64).
	maxEncodedStdinBytes = ((ToolCliInputLimitBytes + 2) / 3) * 4
	// maxTotalRequestBytes is the hard ceiling on a single wire request.
	maxTotalRequestBytes = maxArgBudgetBytes + maxEncodedStdinBytes + 128*1024
	// bridgeCloseWaitTimeout bounds how long bridgeServer.Close waits for
	// stuck handler goroutines so lifecycle.OnEnd never hangs session shutdown.
	bridgeCloseWaitTimeout = 2 * time.Second
)

// callGrant represents a per-bash-call credential minted by PreambleWithRelease
// (or Preamble). It binds one random 192-bit token to that specific bash call's
// sdk.Context, outer bash tool-call ID, and cancellation context, and is revoked
// as soon as the bash call ends (or when the session closes).
type callGrant struct {
	token          string
	bashToolCallID string
	sdkCtx         sdk.Context
	runCtx         context.Context
	cancel         context.CancelFunc
	revoked        atomic.Bool
}

// bridgeServer is a per-session loopback TCP server that accepts tool-command
// invocations from shell functions emitted by Preamble/PreambleWithRelease.
//
// Wire protocol (line-oriented, base64-framed, single request per connection):
//
//	Client -> Server:
//	  TOKEN <hex-token>\n
//	  NAME <tool-name>\n
//	  CWD <base64(cwd)>\n
//	  ARGC <n>\n
//	  ARG <base64(argv[0])>\n
//	  ...
//	  ARG <base64(argv[n-1])>\n
//	  STDIN_B64\n             (present only when argv contains a bare `-`, matching omp)
//	  <base64 of stdin>\n
//	  END\n
//
//	Server -> Client:
//	  RC <exit-code>\n
//	  OUT <base64(stdout)>\n
//	  ERR <base64(stderr)>\n
//	  DONE\n
//
// Every argument, cwd, stdin, stdout, and stderr is base64-encoded so quoting,
// newlines, NULs, binary data, and arbitrary shell metacharacters survive intact
// over bash's `/dev/tcp/127.0.0.1/<port>`.
type bridgeServer struct {
	ln     net.Listener
	addr   string
	port   int
	sess   *sessionState
	rt     *cmdRuntime
	closed atomic.Bool

	grantsMu sync.RWMutex
	grants   map[string]*callGrant

	wg          sync.WaitGroup
	preAuthSem  chan struct{}
	authSem     chan struct{}
	baseCtx     context.Context
	baseCancel  context.CancelFunc
	connsMu     sync.Mutex
	activeConns map[net.Conn]struct{}
}

func startBridgeServer(rt *cmdRuntime, sess *sessionState) (*bridgeServer, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("toolcmd: listen loopback: %w", err)
	}
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		_ = ln.Close()
		return nil, errors.New("toolcmd: unexpected non-TCP listener address")
	}
	baseCtx, baseCancel := context.WithCancel(context.Background())
	srv := &bridgeServer{
		ln:          ln,
		addr:        tcpAddr.String(),
		port:        tcpAddr.Port,
		sess:        sess,
		rt:          rt,
		grants:      make(map[string]*callGrant),
		preAuthSem:  make(chan struct{}, maxPreAuthConnections),
		authSem:     make(chan struct{}, maxAuthConnections),
		baseCtx:     baseCtx,
		baseCancel:  baseCancel,
		activeConns: make(map[net.Conn]struct{}),
	}
	srv.wg.Add(1)
	go srv.acceptLoop()
	return srv, nil
}

// issueGrant mints a fresh per-call random 192-bit token bound to sdkCtx and
// its cancellation signal, returning the token and a revoke function.
func (s *bridgeServer) issueGrant(sdkCtx sdk.Context) (string, func(), error) {
	var tokenBytes [24]byte
	if _, err := rand.Read(tokenBytes[:]); err != nil {
		return "", nil, fmt.Errorf("toolcmd: generate call token: %w", err)
	}
	token := hex.EncodeToString(tokenBytes[:])

	parent := s.baseCtx
	var bashToolCallID string
	var zero sdk.Context
	if sdkCtx != zero {
		bashToolCallID = sdkCtx.ToolCallID()
		if sig := sdkCtx.Signal(); sig != nil {
			parent = sig
		}
	}
	runCtx, cancel := context.WithCancel(parent)
	if parent != s.baseCtx {
		stop := context.AfterFunc(s.baseCtx, cancel)
		origCancel := cancel
		cancel = func() {
			stop()
			origCancel()
		}
	}

	grant := &callGrant{
		token:          token,
		bashToolCallID: bashToolCallID,
		sdkCtx:         sdkCtx,
		runCtx:         runCtx,
		cancel:         cancel,
	}

	s.grantsMu.Lock()
	if s.closed.Load() {
		s.grantsMu.Unlock()
		cancel()
		return "", nil, errors.New("toolcmd: bridge closed")
	}
	s.grants[token] = grant
	s.grantsMu.Unlock()

	var once sync.Once
	revoke := func() {
		once.Do(func() {
			grant.revoked.Store(true)
			s.grantsMu.Lock()
			delete(s.grants, token)
			s.grantsMu.Unlock()
			// Drop any undrained dispatches for this bash call (e.g. an aborted
			// run that never called DrainDispatches) so records never leak.
			if s.sess != nil && grant.bashToolCallID != "" {
				s.sess.dropDispatches(grant.bashToolCallID)
			}
			cancel()
		})
	}
	return token, revoke, nil
}

// lookupGrant validates token in constant time across active grants without
// leaking valid tokens via timing.
func (s *bridgeServer) lookupGrant(candidate string) *callGrant {
	if len(candidate) != 48 {
		return nil
	}
	candBytes := []byte(candidate)
	s.grantsMu.RLock()
	defer s.grantsMu.RUnlock()
	var matched *callGrant
	for tok, grant := range s.grants {
		if len(tok) == len(candBytes) && subtle.ConstantTimeCompare([]byte(tok), candBytes) == 1 {
			matched = grant
		}
	}
	if matched != nil && matched.revoked.Load() {
		return nil
	}
	return matched
}

// Close shuts down the listener, revokes all active per-call grants, cancels
// in-flight handlers, and waits up to bridgeCloseWaitTimeout so lifecycle.OnEnd
// never blocks session teardown indefinitely.
func (s *bridgeServer) Close() {
	if !s.closed.CompareAndSwap(false, true) {
		return
	}
	s.grantsMu.Lock()
	grants := make([]*callGrant, 0, len(s.grants))
	for _, g := range s.grants {
		g.revoked.Store(true)
		grants = append(grants, g)
	}
	s.grants = make(map[string]*callGrant)
	s.grantsMu.Unlock()
	for _, g := range grants {
		g.cancel()
	}

	s.baseCancel()
	_ = s.ln.Close()
	s.connsMu.Lock()
	for c := range s.activeConns {
		_ = c.Close()
	}
	s.connsMu.Unlock()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(bridgeCloseWaitTimeout):
	}
}

func (s *bridgeServer) acceptLoop() {
	defer s.wg.Done()
	for {
		// Acquire a pre-auth slot BEFORE accepting the next connection so a
		// burst of legitimate concurrent clients queues cleanly in the OS TCP
		// listen backlog instead of being silently dropped, while at most
		// maxPreAuthConnections unauthenticated readers can run concurrently
		// (each bounded to a 5s read deadline for its TOKEN line).
		select {
		case s.preAuthSem <- struct{}{}:
		case <-s.baseCtx.Done():
			return
		}
		conn, err := s.ln.Accept()
		if err != nil {
			<-s.preAuthSem
			if s.closed.Load() || errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		s.connsMu.Lock()
		if s.closed.Load() {
			s.connsMu.Unlock()
			<-s.preAuthSem
			_ = conn.Close()
			return
		}
		s.activeConns[conn] = struct{}{}
		s.connsMu.Unlock()
		s.wg.Add(1)
		go func(c net.Conn) {
			defer s.wg.Done()
			defer func() {
				s.connsMu.Lock()
				delete(s.activeConns, c)
				s.connsMu.Unlock()
				_ = c.Close()
			}()
			s.handleConn(c)
		}(conn)
	}
}

func (s *bridgeServer) handleConn(conn net.Conn) {
	var preAuthOnce sync.Once
	releasePreAuth := func() {
		preAuthOnce.Do(func() { <-s.preAuthSem })
	}
	defer releasePreAuth()

	// Never let a panic inside tool execution or decoding crash the PiG host process.
	defer func() {
		if r := recover(); r != nil {
			writeBridgeResponse(conn, ToolCliResult{
				Stderr:   fmt.Sprintf("toolcmd: internal panic: %v\n", r),
				ExitCode: 1,
			})
		}
	}()

	// Short deadline for the unauthenticated token line; extended after auth.
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	br := bufio.NewReaderSize(io.LimitReader(conn, maxTotalRequestBytes), 64*1024)

	tokLine, err := readBoundedLine(br, maxTokenLineBytes)
	if err != nil || !strings.HasPrefix(tokLine, "TOKEN ") {
		return
	}
	grant := s.lookupGrant(tokLine[len("TOKEN "):])
	if grant == nil {
		return
	}
	// Authentication succeeded: release the pre-auth slot immediately and wait
	// for an authenticated execution slot (never drop an authenticated client).
	releasePreAuth()
	select {
	case s.authSem <- struct{}{}:
		defer func() { <-s.authSem }()
	case <-grant.runCtx.Done():
		return
	case <-s.baseCtx.Done():
		return
	}

	// Authenticated: allow up to 30s to receive large base64 stdin/args.
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	req, reqErr := readAuthenticatedRequest(br)
	_ = conn.SetReadDeadline(time.Time{})
	if reqErr != nil {
		var usageErr *ToolCliUsageError
		if errors.As(reqErr, &usageErr) {
			name := req.Name
			if name == "" {
				name = "toolcmd"
			}
			// Upstream exits 2 when stdin cannot be passed (size limit / invalid UTF-8).
			// upstream: crates/shell/src/commands.rs:20-24, 372, 396
			writeBridgeResponse(conn, ToolCliResult{
				Stderr:   fmt.Sprintf("%s: %s\n", name, usageErr.Message),
				ExitCode: 2,
			})
			return
		}
		writeBridgeResponse(conn, ToolCliResult{
			Stderr:   fmt.Sprintf("toolcmd: %s\n", reqErr.Error()),
			ExitCode: 1,
		})
		return
	}

	// Derive a per-connection context cancelled when ANY of:
	//  - the issuing bash call's grant is revoked/cancelled,
	//  - the session closes (baseCtx),
	//  - the shell client disconnects / is killed by timeout (EOF watcher on conn).
	connCtx, cancel := context.WithCancel(grant.runCtx)
	defer cancel()

	// Watch the TCP socket for peer disconnect (EOF/reset) while the tool runs
	// so killing or timing out the bash subprocess cancels the in-flight tool.
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		var probe [1]byte
		for {
			_, rErr := conn.Read(probe[:])
			if rErr != nil {
				cancel()
				return
			}
		}
	}()
	defer func() {
		// Unblock watcher goroutine cleanly before returning.
		_ = conn.SetReadDeadline(time.Now())
		<-watchDone
		_ = conn.SetReadDeadline(time.Time{})
	}()

	res, runErr := s.rt.runAndRecordCli(connCtx, s.sess, grant.sdkCtx, grant.bashToolCallID, req)
	if runErr != nil {
		if isAbortError(connCtx, runErr) {
			writeBridgeResponse(conn, ToolCliResult{
				Stderr:   fmt.Sprintf("%s: cancelled\n", req.Name),
				ExitCode: 130,
			})
			return
		}
		writeBridgeResponse(conn, ToolCliResult{
			Stderr:   fmt.Sprintf("%s: %s\n", req.Name, runErr.Error()),
			ExitCode: 1,
		})
		return
	}
	writeBridgeResponse(conn, res)
}

var errLineTooLong = errors.New("frame line exceeds limit")

// readBoundedLine reads a single '\n'-terminated line up to maxBytes without
// buffering unbounded input from an untrusted peer.
func readBoundedLine(br *bufio.Reader, maxBytes int) (string, error) {
	var buf []byte
	for {
		frag, err := br.ReadSlice('\n')
		if len(buf)+len(frag) > maxBytes {
			return "", errLineTooLong
		}
		buf = append(buf, frag...)
		if err == nil {
			break
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return "", err
	}
	s := string(buf)
	return strings.TrimRight(s, "\r\n"), nil
}

// readAuthenticatedRequest parses the framed request after TOKEN has been verified.
// On stdin limit/UTF-8 violations it returns a partial ToolCliRequest with Name
// populated alongside a *ToolCliUsageError so the caller emits exit code 2 with
// `<name>: ...` matching omp's `crates/shell/src/commands.rs:372,396`.
func readAuthenticatedRequest(br *bufio.Reader) (ToolCliRequest, error) {
	nameLine, err := readBoundedLine(br, maxHeaderLineBytes)
	if err != nil || !strings.HasPrefix(nameLine, "NAME ") {
		return ToolCliRequest{}, errors.New("malformed request: missing NAME")
	}
	name := nameLine[len("NAME "):]
	partial := ToolCliRequest{Name: name}

	cwdLine, err := readBoundedLine(br, maxHeaderLineBytes)
	if err != nil || !strings.HasPrefix(cwdLine, "CWD ") {
		return partial, errors.New("malformed request: missing CWD")
	}
	cwdBytes, err := decodeBase64Field(cwdLine[len("CWD "):])
	if err != nil {
		return partial, fmt.Errorf("malformed CWD: %w", err)
	}

	argcLine, err := readBoundedLine(br, maxHeaderLineBytes)
	if err != nil || !strings.HasPrefix(argcLine, "ARGC ") {
		return partial, errors.New("malformed request: missing ARGC")
	}
	argc, err := strconv.Atoi(argcLine[len("ARGC "):])
	if err != nil || argc < 0 || argc > 65536 {
		return partial, errors.New("malformed ARGC")
	}

	argv := make([]string, argc)
	argBytesUsed := 0
	for i := range argc {
		remaining := maxArgBudgetBytes - argBytesUsed
		if remaining <= 0 {
			return partial, errors.New("arguments exceed size limit")
		}
		argLine, err := readBoundedLine(br, remaining)
		if err != nil || !strings.HasPrefix(argLine, "ARG ") {
			return partial, errors.New("malformed request: missing ARG")
		}
		argBytesUsed += len(argLine)
		b, err := decodeBase64Field(argLine[len("ARG "):])
		if err != nil {
			return partial, fmt.Errorf("malformed ARG %d: %w", i, err)
		}
		argv[i] = string(b)
	}

	var stdinStr string
	for {
		line, err := readBoundedLine(br, maxHeaderLineBytes)
		if err != nil {
			return partial, err
		}
		if line == "END" {
			break
		}
		if line == "STDIN_B64" {
			decoded, sErr := readBoundedBase64Stdin(br)
			if sErr != nil {
				return partial, sErr
			}
			stdinStr = decoded
			break
		}
	}

	return ToolCliRequest{
		Name:  name,
		Argv:  argv,
		Cwd:   string(cwdBytes),
		Stdin: stdinStr,
	}, nil
}

// readBoundedBase64Stdin streams base64-encoded stdin chunks until an `END` line,
// enforcing ToolCliInputLimitBytes incrementally without allocating unbounded memory.
func readBoundedBase64Stdin(br *bufio.Reader) (string, error) {
	var b64Buf strings.Builder
	// Allow up to maxEncodedStdinBytes of base64 payload (plus a small slack for
	// line endings when wrapped).
	limit := maxEncodedStdinBytes + 4096
	for {
		line, err := readBoundedLine(br, limit-b64Buf.Len()+16)
		if err != nil {
			if errors.Is(err, errLineTooLong) {
				return "", &ToolCliUsageError{
					Message: fmt.Sprintf("stdin exceeds the %d byte limit", ToolCliInputLimitBytes),
				}
			}
			return "", err
		}
		if line == "END" {
			break
		}
		if b64Buf.Len()+len(line) > limit {
			return "", &ToolCliUsageError{
				Message: fmt.Sprintf("stdin exceeds the %d byte limit", ToolCliInputLimitBytes),
			}
		}
		b64Buf.WriteString(line)
	}
	decoded, err := decodeBase64Field(b64Buf.String())
	if err != nil {
		return "", &ToolCliUsageError{Message: fmt.Sprintf("malformed stdin: %v", err)}
	}
	if len(decoded) > ToolCliInputLimitBytes {
		return "", &ToolCliUsageError{
			Message: fmt.Sprintf("stdin exceeds the %d byte limit", ToolCliInputLimitBytes),
		}
	}
	if !utf8.Valid(decoded) {
		return "", &ToolCliUsageError{Message: "stdin is not valid UTF-8"}
	}
	return string(decoded), nil
}

func decodeBase64Field(s string) ([]byte, error) {
	clean := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, s)
	if clean == "" {
		return nil, nil
	}
	return base64.StdEncoding.DecodeString(clean)
}

func writeBridgeResponse(w io.Writer, res ToolCliResult) {
	rc := res.ExitCode
	if rc < 0 {
		rc = 0
	} else if rc > 255 {
		rc = 255
	}
	outB64 := base64.StdEncoding.EncodeToString([]byte(res.Stdout))
	errB64 := base64.StdEncoding.EncodeToString([]byte(res.Stderr))
	_, _ = fmt.Fprintf(w, "RC %d\nOUT %s\nERR %s\nDONE\n", rc, outB64, errB64)
}

func (r *cmdRuntime) ensureListener(sess *sessionState) (*bridgeServer, error) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.listener != nil && !sess.listener.closed.Load() {
		return sess.listener, nil
	}
	srv, err := startBridgeServer(r, sess)
	if err != nil {
		return nil, err
	}
	sess.listener = srv
	return srv, nil
}

// PreambleWithRelease mints a per-bash-call credential bound to ctx's active
// tool call and returns (preambleText, release). The caller (`internal/shell`)
// MUST call release() when the bash command (or async job / service) finishes
// so the credential is immediately revoked and cannot be reused after the call
// ends. Returns ("", noop) when no tool commands or special commands are mounted.
func PreambleWithRelease(ctx sdk.Context) (preamble string, release func()) {
	return defaultRuntime.preambleWithRelease(ctx)
}

// Preamble returns shell code defining functions for every mounted and special
// tool command in ctx's session, or "" when nothing is mounted.
//
// Prefer PreambleWithRelease so the per-call credential is revoked as soon as
// the bash call ends; Preamble automatically revokes its grant when ctx's
// request completes (ctx.Done()) or when the session ends.
func Preamble(ctx sdk.Context) string {
	text, release := defaultRuntime.preambleWithRelease(ctx)
	if text == "" {
		return ""
	}
	// Tie grant lifetime to the calling request when no explicit release is used.
	var zero sdk.Context
	if ctx != zero {
		if done := ctx.Done(); done != nil {
			go func() {
				<-done
				release()
			}()
		}
	}
	return text
}

func (r *cmdRuntime) preambleWithRelease(ctx sdk.Context) (string, func()) {
	noop := func() {}
	key := sessionKeyFor(ctx)
	if key == "" {
		return "", noop
	}
	sess := r.getSession(key)
	if sess == nil {
		return "", noop
	}
	names := CommandNames(ctx)
	if len(names) == 0 {
		return "", noop
	}
	srv, err := r.ensureListener(sess)
	if err != nil {
		return "", noop
	}
	token, release, err := srv.issueGrant(ctx)
	if err != nil {
		return "", noop
	}
	return renderPreamble(srv.port, token, names), release
}

// renderPreamble builds the shell preamble for the given loopback port,
// per-call token, and mounted+special command names. Platform-specific tweaks
// live in preamble_*.go.
//
// Clears `allexport` (`set -a`) around all preamble assignments and function
// definitions and explicitly unexports all `__omp_tc_*` variables so a
// persisted `set -a` from an earlier bash call never exports the port, token,
// or helper paths into child processes' environments, then restores `set -a`
// if it was enabled.
func renderPreamble(port int, token string, names []string) string {
	var b strings.Builder
	b.WriteString("__omp_tc_had_a=0\n")
	b.WriteString("case \"$-\" in *a*) __omp_tc_had_a=1; set +a ;; esac\n")
	// Unalias every mounted command name so earlier session aliases cannot shadow
	// tool commands (matching omp's bash-tool-commands.test.ts precedence tests).
	for _, name := range names {
		fmt.Fprintf(&b, "unalias %s 2>/dev/null || true\n", name)
	}
	b.WriteString(renderDispatcherFunction(port, token))
	for _, name := range names {
		// Standard POSIX function definition; works in bash 3.2+, bash 4/5.
		// Command names validated by IsToolCommandName match ^[A-Za-z_][A-Za-z0-9_.-]*$.
		fmt.Fprintf(&b, "%s() { __omp_tc_call %s \"$@\"; }\n", name, singleQuoteShell(name))
	}
	b.WriteString("export -n __omp_tc_port __omp_tc_token __omp_tc_b64_cmd __omp_tc_b64_dec_flag __omp_tc_tr_cmd __omp_tc_openssl_cmd __omp_tc_had_a 2>/dev/null || true\n")
	b.WriteString("if [ \"$__omp_tc_had_a\" = \"1\" ]; then set -a; fi\n")
	b.WriteString("unset __omp_tc_had_a\n")
	return b.String()
}

func singleQuoteShell(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// DirectOptions configures TryDirectWithOptions with the resolved command
// working directory and optional timeout from the bash tool call.
type DirectOptions struct {
	Cwd     string
	Timeout time.Duration // <= 0 means no extra deadline beyond ctx.Signal()
}

// DirectResult is the structured outcome of TryDirectWithOptions.
// TimedOut distinguishes opts.Timeout expiration from a user/parent abort
// (Cancelled) so internal/shell can render its standard timeout notice rather
// than treating a deadline as exit 130 / cancelled.
type DirectResult struct {
	Output    string
	ExitCode  int
	TimedOut  bool
	Cancelled bool
	Handled   bool
}

// InvokedCommand is a non-executing classifier that reports whether `command`
// invokes any mounted or special session tool command in ctx's session,
// returning the first invoked command name and true.
//
// It checks `parseSimpleDirectCommand` first so it never disagrees with
// `TryDirect`/`TryDirectWithOptions`, and also scans compound shell lines
// (`;`, `&&`, `||`, `|`, newlines, parentheses) matching omp's
// `invokedToolCommand` (`tools/bash.ts:711-718`) so `internal/shell` can refuse
// `pty: true` BEFORE executing any tool or spawning a shell.
func InvokedCommand(ctx sdk.Context, command string) (name string, ok bool) {
	names := CommandNames(ctx)
	if len(names) == 0 {
		return "", false
	}
	nameSet := make(map[string]bool, len(names))
	for _, n := range names {
		nameSet[n] = true
	}
	if words, simple := parseSimpleDirectCommand(command); simple && len(words) > 0 {
		if nameSet[words[0]] {
			return words[0], true
		}
		return "", false
	}
	for _, words := range tokenizeShellSegments(command) {
		if cmdName := simpleSegmentCommandName(words); cmdName != "" && nameSet[cmdName] {
			return cmdName, true
		}
	}
	return "", false
}

// TryDirectWithOptions short-circuits simple single-command invocations that
// contain no shell operators, pipes, redirections, expansions, substitutions,
// tilde expansions, comments, grouping, backgrounding, or assignment prefixes —
// honoring the bash call's resolved cwd and timeout and distinguishing timeout
// from cancellation.
//
// upstream: packages/coding-agent/src/tools/bash.ts (isToolCommandLine)
func TryDirectWithOptions(ctx sdk.Context, command string, opts DirectOptions) DirectResult {
	return defaultRuntime.tryDirect(ctx, command, opts)
}

// TryDirect short-circuits simple single-command invocations using ctx.Cwd().
// Prefer TryDirectWithOptions when the bash call specifies an explicit cwd or timeout.
func TryDirect(ctx sdk.Context, command string) (output string, exitCode int, handled bool) {
	res := defaultRuntime.tryDirect(ctx, command, DirectOptions{})
	return res.Output, res.ExitCode, res.Handled
}

func (r *cmdRuntime) tryDirect(ctx sdk.Context, command string, opts DirectOptions) DirectResult {
	key := sessionKeyFor(ctx)
	if key == "" {
		return DirectResult{}
	}
	sess := r.getSession(key)
	if sess == nil {
		return DirectResult{}
	}
	sess.mu.RLock()
	st := sess.state
	isSpecialMap := sess.specialSet
	sess.mu.RUnlock()
	if st == nil || (len(st.MountedNames) == 0 && len(isSpecialMap) == 0) {
		return DirectResult{}
	}

	words, ok := parseSimpleDirectCommand(command)
	if !ok || len(words) == 0 {
		return DirectResult{}
	}
	name := words[0]
	if !st.MountedNames[name] && !isSpecialMap[name] {
		return DirectResult{}
	}

	cwd := opts.Cwd
	var parentCtx context.Context
	var bashToolCallID string
	var zero sdk.Context
	if ctx != zero {
		bashToolCallID = ctx.ToolCallID()
		if cwd == "" {
			cwd = ctx.Cwd()
		}
		parentCtx = ctx.Signal()
	}
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	runCtx := parentCtx
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(parentCtx, opts.Timeout)
		defer cancel()
	}

	res, err := r.runAndRecordCli(runCtx, sess, ctx, bashToolCallID, ToolCliRequest{
		Name:  name,
		Argv:  words[1:],
		Cwd:   cwd,
		Stdin: "",
	})
	if err != nil {
		if isAbortError(runCtx, err) {
			if opts.Timeout > 0 && parentCtx.Err() == nil && errors.Is(runCtx.Err(), context.DeadlineExceeded) {
				return DirectResult{TimedOut: true, Handled: true}
			}
			return DirectResult{
				Output:    fmt.Sprintf("%s: cancelled\n", name),
				ExitCode:  130,
				Cancelled: true,
				Handled:   true,
			}
		}
		return DirectResult{
			Output:   fmt.Sprintf("%s: %s\n", name, err.Error()),
			ExitCode: 1,
			Handled:  true,
		}
	}
	return DirectResult{
		Output:   res.Stdout + res.Stderr,
		ExitCode: res.ExitCode,
		Handled:  true,
	}
}

// parseSimpleDirectCommand parses a single simple shell command with no
// operators (|, ;, &, <, >, (, ), newlines), no expansions/substitutions
// ($, `, ~, *, ?, [, ]), no comments (#), and no leading env assignments.
// Handles single quotes, double quotes (without $/`), and backslash escapes.
// Returns (words, true) only when the command is unambiguously a single literal
// command invocation.
func parseSimpleDirectCommand(command string) ([]string, bool) {
	s := strings.Trim(command, " \t")
	if s == "" {
		return nil, false
	}
	var words []string
	var cur strings.Builder
	inWord := false
	inSingle := false
	inDouble := false

	flush := func() {
		if inWord {
			words = append(words, cur.String())
			cur.Reset()
			inWord = false
		}
	}

	for i := 0; i < len(s); i++ {
		ch := s[i]
		if inSingle {
			if ch == '\'' {
				inSingle = false
			} else {
				cur.WriteByte(ch)
			}
			continue
		}
		if inDouble {
			if ch == '\\' {
				if i+1 >= len(s) {
					return nil, false
				}
				next := s[i+1]
				if next == '"' || next == '\\' || next == '$' || next == '`' {
					cur.WriteByte(next)
					i++
					continue
				}
				if next == '\n' {
					return nil, false
				}
				cur.WriteByte(ch)
				continue
			}
			if ch == '"' {
				inDouble = false
				continue
			}
			// Reject variable/command expansion inside double quotes.
			if ch == '$' || ch == '`' {
				return nil, false
			}
			cur.WriteByte(ch)
			continue
		}

		// Unquoted characters
		switch ch {
		case ' ', '\t':
			flush()
			continue
		case '\n', '\r', ';', '&', '|', '<', '>', '(', ')', '{', '}', '$', '`':
			return nil, false
		case '#':
			if !inWord {
				return nil, false
			}
			cur.WriteByte(ch)
			continue
		case '*', '?', '[', ']':
			// Unquoted glob metacharacters need the shell.
			return nil, false
		case '~':
			// Any unquoted `~` (leading `~/x`, `name=~/x`, `path=a:~/b`) may be
			// tilde-expanded by bash; defer to the shell.
			return nil, false
		case '\'':
			inWord = true
			inSingle = true
			continue
		case '"':
			inWord = true
			inDouble = true
			continue
		case '\\':
			if i+1 >= len(s) {
				return nil, false
			}
			next := s[i+1]
			if next == '\n' || next == '\r' {
				return nil, false
			}
			inWord = true
			cur.WriteByte(next)
			i++
			continue
		default:
			inWord = true
			cur.WriteByte(ch)
		}
	}
	if inSingle || inDouble {
		return nil, false
	}
	flush()
	if len(words) == 0 {
		return nil, false
	}
	// Reject leading env assignments (FOO=bar cmd).
	if strings.ContainsRune(words[0], '=') {
		return nil, false
	}
	return words, true
}

// shellCommandPrefixWords mirrors omp's SHELL_COMMAND_PREFIX_WORDS (tools/bash.ts:692-703).
var shellCommandPrefixWords = map[string]bool{
	"!":     true,
	"{":     true,
	"if":    true,
	"then":  true,
	"elif":  true,
	"else":  true,
	"do":    true,
	"while": true,
	"until": true,
	"time":  true,
}

func isShellEnvAssignment(word string) bool {
	eq := strings.IndexByte(word, '=')
	if eq <= 0 {
		return false
	}
	name := word[:eq]
	for i := range len(name) {
		c := name[i]
		if i == 0 {
			if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '_') {
				return false
			}
		} else {
			if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_') {
				return false
			}
		}
	}
	return true
}

func simpleSegmentCommandName(words []string) string {
	for _, w := range words {
		if !isShellEnvAssignment(w) && !shellCommandPrefixWords[w] {
			return w
		}
	}
	return ""
}

// tokenizeShellSegments mirrors omp's `tokenizeShellSegments` (tools/shell-tokenize.ts:14-109).
func tokenizeShellSegments(command string) [][]string {
	var segments [][]string
	var current []string
	var val strings.Builder
	wordActive := false
	sepStart := -1
	inSingle := false
	inDouble := false

	flushSep := func(end int) {
		if sepStart < 0 {
			return
		}
		raw := command[sepStart:end]
		sepStart = -1
		if strings.ContainsAny(raw, "\n;&|()") && len(current) > 0 {
			segments = append(segments, current)
			current = nil
		}
	}
	flushWord := func() {
		if !wordActive {
			return
		}
		if val.Len() > 0 {
			current = append(current, val.String())
			val.Reset()
		}
		wordActive = false
	}

	for i := 0; i < len(command); i++ {
		ch := command[i]
		if inSingle {
			if ch == '\'' {
				inSingle = false
			} else {
				val.WriteByte(ch)
			}
			continue
		}
		if inDouble {
			if ch == '\\' && i+1 < len(command) {
				next := command[i+1]
				if next == '"' || next == '\\' || next == '$' || next == '`' {
					val.WriteByte(next)
					i++
					continue
				}
			}
			if ch == '"' {
				inDouble = false
			} else {
				val.WriteByte(ch)
			}
			continue
		}
		switch ch {
		case ' ', '\t', '\n', ';', '&', '|', '(', ')':
			flushWord()
			if sepStart < 0 {
				sepStart = i
			}
			continue
		}
		flushSep(i)
		wordActive = true
		if ch == '\'' {
			inSingle = true
		} else if ch == '"' {
			inDouble = true
		} else if ch == '\\' && i+1 < len(command) {
			val.WriteByte(command[i+1])
			i++
		} else {
			val.WriteByte(ch)
		}
	}
	flushWord()
	flushSep(len(command))
	if len(current) > 0 {
		segments = append(segments, current)
	}
	return segments
}
