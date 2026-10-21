package pi_background_tasks

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// Spawn, output, stop and exit of a task. upstream: extensions/background-tasks.ts:459-770, 859-1002.

const settingsChangedEvent = "kendex:extension-settings-changed"

// childProc is the running process of a task.
type childProc struct {
	cmd *exec.Cmd
	pid int
}

func (c *childProc) Pid() int { return c.pid }

// spawnOptions is SpawnTaskOptions. The pointer fields are the original's optional ones.
type spawnOptions struct {
	command        string
	cwd            string
	notifyOnExit   *bool
	notifyOnOutput *bool
	notifyPattern  string
	notifyMode     string
	dedupeKey      string
	timeoutSeconds *float64
	title          string
	origin         string
}

// appendLogLine is the log writer's append for a task; a non-nil result is the producer's hold.
func (rt *bgRuntime) appendLogLine(t *task, text string) *future {
	return rt.logs.append(t.LogFile, text)
}

func (rt *bgRuntime) warnResourceControlFallback(message, cwd string) {
	if !settingBoolean("resourceControlWarnOnFallback", true, cwd) || rt.warned[message] {
		return
	}
	rt.warned[message] = true
	logBackgroundDiagnostic(message, nil)
	rt.notify(message, "warning")
}

// nodeSpawnError is the message Node's child_process emits when the process cannot start.
func nodeSpawnError(file string, err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "spawn " + file + " ENOENT"
	case errors.Is(err, fs.ErrPermission):
		return "spawn " + file + " EACCES"
	}
	return "spawn " + file + " " + err.Error()
}

// spawnTask starts a task. upstream: spawnTask. It is called with mu held.
func (rt *bgRuntime) spawnTask(o spawnOptions) (*task, error) {
	command := jsTrim(o.command)
	if command == "" {
		return nil, errors.New("command is required for background task spawn")
	}
	cwd := jsTrim(o.cwd)
	if cwd == "" {
		cwd = rt.cwd()
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	rt.counter++
	id := "bg-" + itoa(rt.counter)
	now := clockNow()
	timeoutSeconds := settingNumber("defaultTimeoutSeconds", defaultTimeoutMs/1000, cwd)
	if o.timeoutSeconds != nil {
		timeoutSeconds = *o.timeoutSeconds
	}
	var expiresAt *float64
	if timeoutSeconds > 0 {
		expiresAt = new(float64(now) + timeoutSeconds*1000)
	}
	laneCwd := rt.cwd()
	if laneCwd == "" {
		laneCwd = cwd
	}
	laneDir, err := openLaneDir(rt.ownLaneDir(), laneCwd)
	if err != nil {
		return nil, err
	}
	logFile := logFilePath(laneDir, id, now)
	if err := os.WriteFile(logFile, nil, 0o666); err != nil {
		return nil, err
	}

	sh, err := getShellConfig()
	if err != nil {
		return nil, err
	}
	origin := o.origin
	if origin == "" {
		origin = originBgTask
	}
	plan := planResourceControlledSpawn(resourceControlSpawnInput{command: command, cwd: cwd, shell: sh.shell, shellArgs: sh.args, taskID: id, now: now, origin: origin})
	for _, w := range plan.warnings {
		rt.warnResourceControlFallback(w, cwd)
	}
	// kendex#97 hardening: the child gets its own session and process group (Node's detached: true is setsid on POSIX), so
	// the death of Pi's terminal or Pi itself does not reach it, and a signal to the group reaches the task and everything
	// it started.
	cmd := exec.Command(plan.file, plan.args...)
	cmd.Dir = cwd
	cmd.Env = taskEnv()
	detachProcess(cmd)
	stdout, outErr := cmd.StdoutPipe()
	stderr, errErr := cmd.StderrPipe()
	startErr := errors.Join(outErr, errErr)
	if startErr == nil {
		startErr = cmd.Start()
	}
	pid := 0
	if startErr == nil {
		pid = cmd.Process.Pid
	}

	t := &task{snapshot: snapshot{
		Command: command, Cwd: cwd, ExitNotified: false, ExpiresAt: expiresAt, ID: id, LogFile: logFile,
		NotifyOnExit: o.notifyOnExit == nil || *o.notifyOnExit, NotifyOnOutput: o.notifyOnOutput != nil && *o.notifyOnOutput,
		NotifyPattern: jsTrim(o.notifyPattern), NotifyMode: resolveNotifyMode(o.notifyMode, o.notifyPattern), DedupeKey: jsTrim(o.dedupeKey),
		WakeEvents: []wakeEvent{}, VoidedWakeSequences: []int{}, PendingWakes: []wakePending{}, LastOutputDedupeBy: map[string]string{},
		OutputWakeBudget: emptyOutputWakeBudget(), Pid: pid, ResourceControl: plan.metadata, SessionID: rt.sessionID, StartedAt: now,
		Status: statusRunning, Title: jsTrim(o.title), UpdatedAt: now,
	}}
	if t.Title == "" {
		t.Title = command
	}
	if startErr == nil {
		t.child = &childProc{cmd: cmd, pid: pid}
	}
	t.matcher = parseOutputMatcher(o.notifyPattern)
	t.voidedWakes = map[int]bool{}
	rt.tasks.set(t)
	rememberSnapshot(t)
	rt.persistNow()
	publishBackgroundTaskStarted(t)

	// The identity lets a later restore tell this process from a reused pid; it rides the next windowed persist, and every
	// lifecycle persist and the shutdown flush it. A task cleared or replaced before the read resolves stays forgotten. An
	// identity the read could not answer stays unset, so restore falls back to pid-only liveness.
	if pid > 0 {
		go func() {
			reading := readProcessIdentity(pid)
			rt.loop.post(func() {
				rt.run(func() {
					if rt.tasks.get(id) != t {
						return
					}
					switch reading.kind {
					case "identity":
						ident := reading.identity
						t.ProcIdent = &ident
						rememberSnapshot(t)
						rt.persistSoon.request()
					case "unknown":
						logBackgroundDiagnostic("spawn identity unknown", map[string]any{"id": id, "pid": pid, "reason": reading.reason})
					}
				})
			})
		}()
	}

	if startErr != nil {
		// Node reports a failed start as an error event after spawn returns.
		rt.loop.post(func() {
			rt.run(func() {
				rt.handleChunk(t, "\n[spawn error] "+nodeSpawnError(plan.file, startErr)+"\n", 0)
				rt.finalizeTask(t, new(1), statusFailed)
			})
		})
	} else {
		var readers sync.WaitGroup
		for _, r := range []io.Reader{stdout, stderr} {
			readers.Add(1)
			go func() {
				defer readers.Done()
				rt.pump(t, r)
			}()
		}
		go func() {
			// Node's close event: the process has exited and both pipes are drained.
			readers.Wait()
			err := cmd.Wait()
			var code *int
			if err == nil {
				code = new(0)
			} else if ps := cmd.ProcessState; ps != nil && ps.ExitCode() >= 0 {
				code = new(ps.ExitCode())
			}
			rt.loop.post(func() { rt.run(func() { rt.finalizeTask(t, code, "") }) })
		}()
	}

	if expiresAt != nil {
		t.timeoutTimer = rt.after(time.Duration(max(1, timeoutSeconds*1000))*time.Millisecond, func() {
			rt.appendLogLine(t, "\n[timeout] Background task exceeded "+formatDuration(timeoutSeconds*1000)+".\n")
			rt.requestStop(t, stopTimeout)
		})
	}
	rt.refreshUI()
	return t, nil
}

// pump reads one pipe of a task, handing each chunk to handleChunk as Node's data event does. While the log writer holds the
// producer, no pipe is read, so the child blocks on its own write.
func (rt *bgRuntime) pump(t *task, r io.Reader) {
	buf := make([]byte, 64*1024)
	for {
		rt.mu.Lock()
		gate := t.gate
		rt.mu.Unlock()
		if gate != nil {
			gate.wait()
		}
		n, err := r.Read(buf)
		if n > 0 {
			text := decodeUTF8(buf[:n])
			rt.mu.Lock()
			rt.handleChunk(t, text, n)
			rt.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// appendOutput is `task.output += text` without copying the buffer: outBuf only grows by append, so an earlier output string
// stays valid when it is replaced.
func appendOutput(t *task, text string) {
	if len(t.outBuf) != len(t.output) || (len(t.output) > 0 && unsafe.SliceData(t.outBuf) != unsafe.StringData(t.output)) {
		t.outBuf = []byte(t.output)
	}
	t.outBuf = append(t.outBuf, text...)
	t.output = unsafe.String(unsafe.SliceData(t.outBuf), len(t.outBuf))
	t.outputUnits += jsLen(text)
}

// handleChunk is one chunk of task output. bytes is the chunk's length on the pipe.
func (rt *bgRuntime) handleChunk(t *task, text string, bytes int) {
	now := clockNow()
	t.UpdatedAt = now
	t.LastOutputAt = new(now)
	t.OutputBytes += int64(bytes)
	if t.outputUnits == 0 && t.output != "" {
		t.outputUnits = jsLen(t.output)
	}
	appendOutput(t, text)
	if float64(t.outputUnits) > settingNumber("outputBufferMaxChars", defaultOutputBufferMaxChars, "") {
		t.output, t.lastAnnouncedLen = trimOutputBuffer(t.output, t.lastAnnouncedLen)
		t.outBuf, t.outputUnits = nil, jsLen(t.output)
	}
	if hold := rt.appendLogLine(t, text); hold != nil {
		// The log's writes fell behind: stop reading until they settle or one stalls, so the child blocks on its pipe meanwhile.
		t.gate = hold
		rt.whenDone(hold, func() {
			if t.gate == hold {
				t.gate = nil
			}
		})
	}
	rt.scheduleOutputReaction(t)
	rt.uiRefresh.request()
}

// finalizeTask is a task's close: its final status and timers settle at once, so a stop, timeout or shutdown after the child
// exits signals nothing. The exit wake names the log file as the task's full output, so it waits for the log's flush to
// release. A task cleared or replaced meanwhile gets no wake. Once the log holds the output, the process handle and the
// in-memory output are released; a log whose last write failed or is still stalled keeps the in-memory output as the record.
func (rt *bgRuntime) finalizeTask(t *task, exitCode *int, statusOverride string) {
	if !closeTaskLifecycle(t, exitCode, rt.hooks, statusOverride, "") {
		return
	}
	t.child = nil
	rt.refreshUI()
	rt.exitWakeDue[t] = true
	settle := func() {
		delete(rt.exitWakeDue, t)
		if rt.tasks.get(t.ID) != t {
			return
		}
		sendExitWakeLifecycle(t, rt.hooks)
		if rt.logs.settled(t.LogFile) && exists(t.LogFile) {
			t.output, t.outBuf, t.outputUnits, t.lastAnnouncedLen = "", nil, 0, 0
		}
		if rt.boundFinishedTasks() > 0 {
			rt.persistNow()
		}
	}
	if written := rt.logs.flush(t.LogFile); written != nil {
		rt.whenDone(written, settle)
		return
	}
	settle()
}

// ── stop ────────────────────────────────────────────────────────────────────

type killResult struct {
	err  string
	sent bool
}

func (rt *bgRuntime) killTaskProcess(t *task, signal string) killResult {
	stop := stopResourceControlledTask(t.ResourceControl, signal, nil)
	if stop.attempted && stop.ok {
		return killResult{sent: true}
	}
	if stop.attempted {
		e := stop.err
		if e == "" {
			e = "resource-control stop failed"
		}
		rt.appendLogLine(t, "\n[resource-control stop error] "+e+"\n")
		return killResult{err: e}
	}
	if t.Pid <= 0 {
		return killResult{}
	}
	// The task is its own process group on POSIX (it was spawned in its own session), so -pid reaches everything it started;
	// on Windows the tree goes with taskkill /T /F.
	if err := sendKill(t.Pid, signal, runtime.GOOS != "windows"); err != nil {
		if !isNoSuchProcess(err) {
			rt.appendLogLine(t, "\n[kill error] "+nodeKillError(err)+"\n")
		}
		return killResult{}
	}
	return killResult{sent: true}
}

// sendKill is killProcess; the tests count the signals a task receives.
var sendKill = killProcess

// nodeKillError is String(error) of the error process.kill throws.
func nodeKillError(err error) string {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		code, _ := errnoCodeAndText(errno)
		return "Error: kill " + code
	}
	return "Error: " + err.Error()
}

// requestStop asks a task to stop. upstream: requestStop. reason is stopUser, stopTimeout or stopShutdown.
func (rt *bgRuntime) requestStop(t *task, reason string) (ok bool, message string) {
	if t == nil {
		return false, "No background task matched that id or pid."
	}
	if t.Status != statusRunning {
		return true, t.ID + " is already " + summarizeTaskStatus(t.Status, t.ExitCode, t.TerminationReason) + "."
	}
	t.stopReason = reason
	// The termination reason is stamped before the signal, so when the child's close reaches the lifecycle the annotation is
	// already there.
	switch reason {
	case stopUser:
		t.TerminationReason = termExtensionStop
	case stopShutdown:
		t.TerminationReason = termSessionShutdown
	case stopTimeout:
		t.TerminationReason = termTimeout
	}
	t.UpdatedAt = clockNow()
	action := "stop"
	if reason == stopShutdown {
		action = "shutdown"
	}
	voidPendingTaskWakes(t, action, rt.logWake, clockNow)
	rememberSnapshot(t)
	if t.outputTimer != nil {
		t.outputTimer.Stop()
	}
	t.outputTimer = nil
	rt.persistNow()

	// The command preview in the message is bounded, so a 100 KB heredoc cannot reach the tool result.
	safe := truncateForTranscript(t.Command, wakeManifestFieldMaxChars)
	result := rt.killTaskProcess(t, "SIGTERM")
	if !result.sent {
		if result.err != "" {
			t.stopReason = ""
			t.TerminationReason = ""
			t.UpdatedAt = clockNow()
			rememberSnapshot(t)
			rt.persistNow()
			rt.refreshUI()
			return false, "Failed to stop " + t.ID + ": " + result.err
		}
		override := statusStopped
		if reason == stopTimeout {
			override = statusTimedOut
		}
		rt.finalizeTask(t, t.ExitCode, override)
		return true, "Stopped " + t.ID + " (" + safe + ")."
	}
	grace := settingNumber("forceKillGraceMs", defaultForceKillGraceMs, rt.cwd())
	t.forceKillTimer = rt.after(time.Duration(grace*float64(time.Millisecond)), func() {
		if t.Status == statusRunning && !t.closed {
			rt.appendLogLine(t, "\n[stop] Escalating to SIGKILL after "+formatDuration(grace)+".\n")
			rt.killTaskProcess(t, "SIGKILL")
		}
	})
	rt.refreshUI()
	return true, "Stopping " + t.ID + " (" + safe + ")."
}
