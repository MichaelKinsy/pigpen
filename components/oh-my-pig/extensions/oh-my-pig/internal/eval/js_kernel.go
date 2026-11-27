package eval

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// upstream: packages/coding-agent/src/eval/js/executor.ts,
//           packages/coding-agent/src/eval/js/context-manager.ts,
//           packages/coding-agent/src/eval/js/shared/prelude.txt,
//           packages/coding-agent/src/eval/js/shared/runtime.ts,
//           packages/coding-agent/src/eval/js/worker-core.ts

//go:embed js_runner.js
var jsRunnerScript string

//go:embed js_rewriter.cjs
var jsRewriterBundle string

//go:embed js_prelude.txt
var jsPreludeSource string

const (
	jsMinNodeMajor   = 22
	jsStartupTimeout = 15 * time.Second
	jsShutdownGrace  = 1 * time.Second
	jsProbeTimeout   = 5 * time.Second
)

var jsNodeVersionRe = regexp.MustCompile(`v?(\d+)\.(\d+)`)

type jsRuntimeInfo struct {
	Kind       string // "bun" or "node"
	BinaryPath string
	Version    string
}

type jsAvailability struct {
	OK      bool
	Runtime *jsRuntimeInfo
	Reason  string
}

var (
	jsAvailMu    sync.Mutex
	jsAvailCache = map[string]jsAvailability{}
)

// probeJSRuntime looks for `bun` on PATH first, then `node` (>= 22).
func probeJSRuntime(ctx context.Context) jsAvailability {
	pathEnv := os.Getenv("PATH")
	jsAvailMu.Lock()
	if cached, ok := jsAvailCache[pathEnv]; ok && cached.OK {
		jsAvailMu.Unlock()
		return cached
	}
	jsAvailMu.Unlock()

	var failures []string

	// Prefer Bun when available on PATH.
	for _, bunPath := range lookPathAll("bun", pathEnv) {
		pctx, cancel := context.WithTimeout(ctx, jsProbeTimeout)
		out, err := exec.CommandContext(pctx, bunPath, "--version").Output()
		cancel()
		if err == nil {
			res := jsAvailability{
				OK: true,
				Runtime: &jsRuntimeInfo{
					Kind:       "bun",
					BinaryPath: bunPath,
					Version:    strings.TrimSpace(string(out)),
				},
			}
			jsAvailMu.Lock()
			jsAvailCache[pathEnv] = res
			jsAvailMu.Unlock()
			return res
		}
		failures = append(failures, fmt.Sprintf("%s (%v)", bunPath, err))
	}

	// Fall back to Node.js 22+.
	for _, nodePath := range lookPathAll("node", pathEnv) {
		pctx, cancel := context.WithTimeout(ctx, jsProbeTimeout)
		out, err := exec.CommandContext(pctx, nodePath, "--version").Output()
		cancel()
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s (%v)", nodePath, err))
			continue
		}
		verStr := strings.TrimSpace(string(out))
		m := jsNodeVersionRe.FindStringSubmatch(verStr)
		if m == nil {
			failures = append(failures, fmt.Sprintf("%s (unrecognized version %q)", nodePath, verStr))
			continue
		}
		major, _ := strconv.Atoi(m[1])
		if major < jsMinNodeMajor {
			failures = append(failures, fmt.Sprintf("%s (Node %s is too old; %d+ required)", nodePath, verStr, jsMinNodeMajor))
			continue
		}
		res := jsAvailability{
			OK: true,
			Runtime: &jsRuntimeInfo{
				Kind:       "node",
				BinaryPath: nodePath,
				Version:    verStr,
			},
		}
		jsAvailMu.Lock()
		jsAvailCache[pathEnv] = res
		jsAvailMu.Unlock()
		return res
	}

	reason := "No working Bun or Node.js 22+ runtime found on PATH."
	if len(failures) > 0 {
		reason = "No working Bun or Node.js 22+ runtime found. Tried: " + strings.Join(failures, "; ")
	}
	return jsAvailability{OK: false, Reason: reason}
}

type jsFrame struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Data  string          `json:"data"`
	Text  string          `json:"text"`
	View  any             `json:"view"`
	Image map[string]any  `json:"image"`
	Event EvalStatusEvent `json:"event"`
	OK    bool            `json:"ok"`
	Error json.RawMessage `json:"error"`
}

type jsPending struct {
	filename  string
	onChunk   func(string)
	onDisplay func(DisplayOutput)
	ok        bool
	errText   string
	done      chan struct{}
	once      sync.Once
}

func (p *jsPending) finish() {
	p.once.Do(func() { close(p.done) })
}

type jsKernel struct {
	id        string
	runtime   jsRuntimeInfo
	cmd       *exec.Cmd
	ctrlWrite io.WriteCloser
	ctrlMu    sync.Mutex

	mu          sync.Mutex
	alive       bool
	disposed    bool
	exitMsg     string
	pending     map[string]*jsPending
	activeRunID string
	readyCh     chan error
	exited      chan struct{}
}

func startJSKernel(ctx context.Context, cwd, sessionID string, localRoots map[string]string, bridge *bridgeServer) (*jsKernel, error) {
	avail := probeJSRuntime(ctx)
	if !avail.OK || avail.Runtime == nil {
		return nil, errors.New(avail.Reason)
	}
	return startJSKernelWithRuntime(ctx, *avail.Runtime, cwd, sessionID, localRoots, bridge)
}

func startJSKernelWithRuntime(ctx context.Context, rt jsRuntimeInfo, cwd, sessionID string, localRoots map[string]string, bridge *bridgeServer) (*jsKernel, error) {
	scriptPath, err := stageRunnerScript("omp-js-runner", "cjs", jsRunnerScript)
	if err != nil {
		return nil, fmt.Errorf("stage js runner: %w", err)
	}
	rewriterPath, err := stageRunnerScript("omp-js-runner", "rewriter.cjs", jsRewriterBundle)
	if err != nil {
		return nil, fmt.Errorf("stage js rewriter: %w", err)
	}

	var args []string
	if rt.Kind == "bun" {
		args = []string{"run", scriptPath}
	} else {
		args = []string{scriptPath}
	}
	cmd := exec.Command(rt.BinaryPath, args...)
	cmd.Dir = cwd
	configureProcessGroup(cmd)

	var (
		ctrlWrite  io.WriteCloser
		frameRead  io.ReadCloser
		captureOut io.ReadCloser
		captureErr io.ReadCloser
		childClose []*os.File
		parentOpen []io.Closer
	)
	closeAllOnErr := func() {
		for _, f := range childClose {
			_ = f.Close()
		}
		for _, c := range parentOpen {
			_ = c.Close()
		}
	}

	if runtime.GOOS != "windows" {
		// Isolate NDJSON protocol on dedicated pipe FDs (3 = ctrl read, 4 = frame write).
		// Child fd 0 is /dev/null; child fd 1/2 are captured as regular stdout/stderr chunks
		// via parent-owned os.Pipe so cmd.Wait() never closes a pipe while a reader is draining.
		ctrlR, ctrlW, err := os.Pipe()
		if err != nil {
			return nil, fmt.Errorf("js control pipe: %w", err)
		}
		childClose = append(childClose, ctrlR)
		parentOpen = append(parentOpen, ctrlW)

		frameR, frameW, err := os.Pipe()
		if err != nil {
			closeAllOnErr()
			return nil, fmt.Errorf("js frame pipe: %w", err)
		}
		childClose = append(childClose, frameW)
		parentOpen = append(parentOpen, frameR)

		outR, outW, err := os.Pipe()
		if err != nil {
			closeAllOnErr()
			return nil, fmt.Errorf("js capture stdout pipe: %w", err)
		}
		childClose = append(childClose, outW)
		parentOpen = append(parentOpen, outR)

		errR, errW, err := os.Pipe()
		if err != nil {
			closeAllOnErr()
			return nil, fmt.Errorf("js capture stderr pipe: %w", err)
		}
		childClose = append(childClose, errW)
		parentOpen = append(parentOpen, errR)

		configureJSExtraFiles(cmd, ctrlR, frameW)
		cmd.Env = append(os.Environ(), "OMP_JS_CTRL_FD=3", "OMP_JS_FRAME_FD=4")
		// fd 0 = /dev/null (nil Stdin in exec.Cmd attaches os.DevNull).
		cmd.Stdin = nil
		cmd.Stdout = outW
		cmd.Stderr = errW
		ctrlWrite = ctrlW
		frameRead = frameR
		captureOut = outR
		captureErr = errR
	} else {
		// Parent-owned os.Pipe for stdout/stderr on Windows so cmd.Wait() never closes
		// the frame/stderr pipes while readFrames/drainRawStream are still reading.
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return nil, fmt.Errorf("js stdin pipe: %w", err)
		}
		parentOpen = append(parentOpen, stdin)

		outR, outW, err := os.Pipe()
		if err != nil {
			closeAllOnErr()
			return nil, fmt.Errorf("js stdout pipe: %w", err)
		}
		childClose = append(childClose, outW)
		parentOpen = append(parentOpen, outR)

		errR, errW, err := os.Pipe()
		if err != nil {
			closeAllOnErr()
			return nil, fmt.Errorf("js stderr pipe: %w", err)
		}
		childClose = append(childClose, errW)
		parentOpen = append(parentOpen, errR)

		cmd.Stdout = outW
		cmd.Stderr = errW
		ctrlWrite = stdin
		frameRead = outR
		captureErr = errR
	}

	if err := cmd.Start(); err != nil {
		closeAllOnErr()
		return nil, fmt.Errorf("start js kernel: %w", err)
	}
	for _, f := range childClose {
		_ = f.Close()
	}

	k := &jsKernel{
		id:        "js-" + newRandomID(),
		runtime:   rt,
		cmd:       cmd,
		ctrlWrite: ctrlWrite,
		alive:     true,
		pending:   make(map[string]*jsPending),
		readyCh:   make(chan error, 1),
		exited:    make(chan struct{}),
	}

	var readersWG sync.WaitGroup
	readersWG.Add(1)
	go func() {
		defer readersWG.Done()
		k.readFrames(frameRead)
	}()
	if captureOut != nil {
		readersWG.Add(1)
		go func() {
			defer readersWG.Done()
			defer captureOut.Close()
			k.drainRawStream(captureOut)
		}()
	}
	if captureErr != nil {
		readersWG.Add(1)
		go func() {
			defer readersWG.Done()
			defer captureErr.Close()
			k.drainRawStream(captureErr)
		}()
	}
	go k.waitExit(&readersWG)
	initPayload := map[string]any{
		"type":          "init",
		"cwd":           cwd,
		"sessionId":     sessionID,
		"preludeSource": jsPreludeSource,
		"rewriterPath":  rewriterPath,
		"localRoots":    localRoots,
	}
	if bridge != nil {
		initPayload["bridge"] = map[string]any{
			"url":     bridge.url,
			"token":   bridge.token,
			"session": sessionID,
		}
	}
	if err := k.writeLine(initPayload); err != nil {
		k.shutdown()
		return nil, fmt.Errorf("js kernel init write: %w", err)
	}

	initCtx, cancel := context.WithTimeout(ctx, jsStartupTimeout)
	defer cancel()
	select {
	case err := <-k.readyCh:
		if err != nil {
			k.shutdown()
			return nil, fmt.Errorf("JS kernel init failed: %w", err)
		}
	case <-k.exited:
		return nil, errors.New("JS eval worker exited during startup")
	case <-initCtx.Done():
		k.shutdown()
		return nil, fmt.Errorf("JS kernel startup timed out: %w", initCtx.Err())
	}
	return k, nil
}

func (k *jsKernel) pid() int {
	if k == nil || k.cmd == nil || k.cmd.Process == nil {
		return 0
	}
	return k.cmd.Process.Pid
}

func (k *jsKernel) isAlive() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.alive && !k.disposed
}

// drainRawStream forwards bytes written directly to fd 1/2 (e.g. fs.writeSync(1, ...) or
// inherited child process stdio) to the currently executing cell's output stream.
func (k *jsKernel) drainRawStream(r io.Reader) {
	buf := make([]byte, 8192)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			chunk := string(buf[:n])
			k.mu.Lock()
			var p *jsPending
			if k.activeRunID != "" {
				p = k.pending[k.activeRunID]
			}
			k.mu.Unlock()
			if p != nil && p.onChunk != nil {
				p.onChunk(chunk)
			}
		}
		if err != nil {
			return
		}
	}
}

func (k *jsKernel) abortPending(reason string) {
	k.mu.Lock()
	pendings := make([]*jsPending, 0, len(k.pending))
	for _, p := range k.pending {
		pendings = append(pendings, p)
	}
	k.pending = make(map[string]*jsPending)
	k.activeRunID = ""
	k.mu.Unlock()

	for _, p := range pendings {
		p.ok = false
		if p.errText == "" {
			p.errText = reason
		}
		p.finish()
	}
}

func (k *jsKernel) waitExit(readersWG *sync.WaitGroup) {
	// Wait for frame and raw-stdio readers to reach EOF before cmd.Wait() so
	// final frames and raw output tails are never lost and scanners never race Wait.
	if readersWG != nil {
		readersWG.Wait()
	}
	err := k.cmd.Wait()
	k.mu.Lock()
	k.alive = false
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			k.exitMsg = fmt.Sprintf("JS eval worker exited with code %d", exitErr.ExitCode())
		} else {
			k.exitMsg = fmt.Sprintf("JS eval worker exited: %v", err)
		}
	} else {
		k.exitMsg = "JS eval worker exited with code 0"
	}
	reason := k.exitMsg
	k.mu.Unlock()

	k.abortPending(reason)
	close(k.exited)
}

func (k *jsKernel) readFrames(r io.ReadCloser) {
	defer r.Close()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var frame jsFrame
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			continue
		}
		k.handleFrame(frame)
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, os.ErrClosed) {
		k.abortPending(fmt.Sprintf("JS eval worker protocol reader failed: %v", err))
		go k.shutdown()
	}
}

func (k *jsKernel) handleFrame(frame jsFrame) {
	switch frame.Type {
	case "ready":
		select {
		case k.readyCh <- nil:
		default:
		}
		return
	case "init_failed":
		var msg string
		_ = json.Unmarshal(frame.Error, &msg)
		if msg == "" {
			msg = string(frame.Error)
		}
		select {
		case k.readyCh <- errors.New(msg):
		default:
		}
		return
	}

	k.mu.Lock()
	p := k.pending[frame.ID]
	k.mu.Unlock()
	if p == nil {
		return
	}
	switch frame.Type {
	case "stdout":
		if frame.Data != "" && p.onChunk != nil {
			p.onChunk(frame.Data)
		}
	case "view":
		if p.onDisplay != nil && frame.View != nil {
			p.onDisplay(DisplayOutput{Type: "view", Text: frame.Text, View: frame.View})
		}
	case "image":
		if p.onDisplay != nil && frame.Image != nil {
			if img := parseImageMap(frame.Image); img != nil {
				p.onDisplay(DisplayOutput{Type: "image", Image: img})
			}
		}
	case "status":
		if p.onDisplay != nil && frame.Event != nil {
			p.onDisplay(DisplayOutput{Type: "status", Status: frame.Event})
		}
	case "done":
		p.ok = frame.OK
		if !frame.OK && len(frame.Error) > 0 {
			var errObj struct {
				Name    string `json:"name"`
				Message string `json:"message"`
				Stack   string `json:"stack"`
			}
			if err := json.Unmarshal(frame.Error, &errObj); err == nil {
				if errObj.Stack != "" {
					p.errText = formatJSCellError(errObj.Stack, p.filename)
				} else if errObj.Name != "" && errObj.Message != "" {
					p.errText = errObj.Name + ": " + errObj.Message
				} else {
					p.errText = errObj.Message
				}
			} else {
				p.errText = string(frame.Error)
			}
		}
		k.mu.Lock()
		delete(k.pending, frame.ID)
		if k.activeRunID == frame.ID {
			k.activeRunID = ""
		}
		k.mu.Unlock()
		p.finish()
	}
}

var jsStackFrameRe = regexp.MustCompile(`^\s+at (?:.*? \((.*)\)|(.*))$`)
var jsHostLocationRe = regexp.MustCompile(`^(?:native\b|unknown$|node:|bun:|\[eval-prelude:|\[omp-js-prelude\])|\$bunfs|~BUN`)
var jsRunnerMicrotaskFrameRe = regexp.MustCompile(`^\s+at (?:process\.)?processTicksAndRejections \((?:native:|node:internal/process/task_queues:)`)

// formatJSCellError strips internal runner/engine frames from a JS stack trace
// and appends omp's hidden-frame summary line.
// upstream: packages/coding-agent/src/eval/js/error-text.ts:35-88
func formatJSCellError(stack, filename string) string {
	lines := strings.Split(stack, "\n")
	var kept []string
	hidden := 0
	for i, line := range lines {
		m := jsStackFrameRe.FindStringSubmatch(line)
		if m != nil {
			loc := strings.TrimSpace(m[1])
			if loc == "" {
				loc = strings.TrimSpace(m[2])
			}
			isCellFrame := filename != "" && strings.HasPrefix(loc, filename)
			if !isCellFrame && (jsHostLocationRe.MatchString(loc) || strings.Contains(loc, "omp-js-runner") || strings.Contains(loc, "js_rewriter.cjs")) {
				// Drop the runner/readline/watchdog's trailing processTicksAndRejections
				// frame without counting it as an omp frame (omp's IPC worker has no
				// readline/Worker nextTick pump beneath runOne).
				if i == len(lines)-1 && jsRunnerMicrotaskFrameRe.MatchString(line) {
					continue
				}
				hidden++
				continue
			}
		}
		kept = append(kept, line)
	}
	if hidden == 0 {
		return stack
	}
	plural := "s"
	if hidden == 1 {
		plural = ""
	}
	count := fmt.Sprintf("%d omp frame%s hidden", hidden, plural)
	if filename != "" && strings.Contains(stack, filename) {
		kept = append(kept, fmt.Sprintf("    (%s; catch the error and print e.stack to see them)", count))
	} else {
		kept = append(kept, fmt.Sprintf("    (%s)", count))
	}
	return strings.Join(kept, "\n")
}

func (k *jsKernel) writeLine(payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	k.ctrlMu.Lock()
	defer k.ctrlMu.Unlock()
	if k.ctrlWrite == nil {
		return errors.New("JS kernel control pipe closed")
	}
	_, err = k.ctrlWrite.Write(raw)
	return err
}

func (k *jsKernel) exec(
	ctx context.Context,
	runID, code, filename, cwd string,
	localRoots map[string]string,
	bridge *bridgeServer,
	sessionID string,
	preludes []Prelude,
	onChunk func(string),
	onDisplay func(DisplayOutput),
) (kernelResult, error) {
	if ctx.Err() != nil {
		return kernelResult{Cancelled: true}, nil
	}
	if !k.isAlive() {
		return kernelResult{}, errors.New("JS kernel is not running")
	}

	outBuf := newSpillableOutputBuffer(DefaultOutputMaxBytes, WriteSpill != nil)
	var displays []DisplayOutput
	var dispMu sync.Mutex

	cellFilename := filename
	if cellFilename == "" {
		cellFilename = "[eval:" + runID + "]"
	}
	p := &jsPending{
		filename: cellFilename,
		done:     make(chan struct{}),
		onChunk: func(chunk string) {
			outBuf.Append(chunk)
			if onChunk != nil {
				onChunk(chunk)
			}
		},
		onDisplay: func(d DisplayOutput) {
			dispMu.Lock()
			displays = append(displays, d)
			dispMu.Unlock()
			if onDisplay != nil {
				onDisplay(d)
			}
		},
	}

	k.mu.Lock()
	if !k.alive || k.disposed {
		k.mu.Unlock()
		return kernelResult{}, errors.New("JS kernel is not running")
	}
	k.pending[runID] = p
	k.activeRunID = runID
	k.mu.Unlock()

	type wirePrelude struct {
		Name    string   `json:"name"`
		Exports []string `json:"exports"`
		Source  string   `json:"source"`
	}
	var wirePreludes []wirePrelude
	for _, pr := range preludes {
		if isBuiltinBridgePrelude(pr.Name) {
			continue
		}
		if strings.TrimSpace(pr.JavaScript) == "" {
			continue
		}
		wirePreludes = append(wirePreludes, wirePrelude{
			Name:    pr.Name,
			Exports: pr.Exports,
			Source:  pr.JavaScript,
		})
	}

	req := map[string]any{
		"type":       "run",
		"id":         runID,
		"code":       code,
		"filename":   filename,
		"cwd":        cwd,
		"localRoots": localRoots,
		"preludes":   wirePreludes,
	}
	if bridge != nil {
		req["bridge"] = map[string]any{
			"url":     bridge.url,
			"token":   bridge.token,
			"session": sessionID,
		}
	}

	if err := k.writeLine(req); err != nil {
		k.mu.Lock()
		delete(k.pending, runID)
		if k.activeRunID == runID {
			k.activeRunID = ""
		}
		k.mu.Unlock()
		return kernelResult{}, err
	}

	var cancelled bool
	var killed bool
	select {
	case <-p.done:
	case <-ctx.Done():
		// Force-kill the JS worker to interrupt synchronous or hung JS code,
		// matching omp's JS worker cancellation semantics.
		cancelled = true
		killed = true
		k.shutdown()
		<-p.done
	}

	if !p.ok && p.errText != "" && !cancelled {
		outBuf.AppendLine(p.errText)
	}
	outStr, truncMeta := outBuf.Finalize()

	var exitCode *int
	if !cancelled {
		codeVal := 0
		if !p.ok {
			codeVal = 1
		}
		exitCode = &codeVal
	}

	dispMu.Lock()
	dispCopy := append([]DisplayOutput(nil), displays...)
	dispMu.Unlock()

	fullOut := ""
	if truncMeta != nil && WriteSpill != nil {
		fullOut = outBuf.FullText()
	}
	return kernelResult{
		Output:         outStr,
		FullOutput:     fullOut,
		Truncation:     truncMeta,
		ExitCode:       exitCode,
		Cancelled:      cancelled,
		KernelKilled:   killed || !k.isAlive(),
		DisplayOutputs: dispCopy,
	}, nil
}

func (k *jsKernel) shutdown() {
	if k == nil {
		return
	}
	k.mu.Lock()
	if k.disposed {
		k.mu.Unlock()
		<-k.exited
		return
	}
	k.disposed = true
	k.alive = false
	k.mu.Unlock()

	_ = k.writeLine(map[string]any{"type": "exit"})
	k.ctrlMu.Lock()
	if k.ctrlWrite != nil {
		_ = k.ctrlWrite.Close()
		k.ctrlWrite = nil
	}
	k.ctrlMu.Unlock()

	select {
	case <-k.exited:
		return
	case <-time.After(jsShutdownGrace):
	}
	killProcessTreeGraceful(k.cmd.Process, k.exited, jsShutdownGrace)
	<-k.exited
}

func formatJSTimeoutAnnotation(timeout time.Duration) string {
	// upstream: packages/coding-agent/src/eval/js/executor.ts:89
	const reset = "The JS worker was force-killed and its VM state was reset; variables from earlier cells are gone."
	if timeout <= 0 {
		return "Command timed out. " + reset
	}
	secs := max(int(timeout.Round(time.Second)/time.Second), 1)
	return fmt.Sprintf("Command timed out after %d seconds. %s", secs, reset)
}
