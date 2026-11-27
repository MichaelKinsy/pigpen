package terminal

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultRingBufferBytes = 512 * 1024 // 512 KB rolling output buffer per background process
	defaultLogLimit        = 200
)

// ringBuffer is a thread-safe fixed-capacity byte ring buffer that tracks total bytes written
// so poll() can return only new bytes since the last read cursor.
type ringBuffer struct {
	mu      sync.Mutex
	buf     []byte
	cap     int
	total   int64 // cumulative bytes ever written
	dropped int64 // bytes evicted from head
}

func newRingBuffer(capacity int) *ringBuffer {
	if capacity <= 0 {
		capacity = defaultRingBufferBytes
	}
	return &ringBuffer{
		cap: capacity,
	}
}

func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(p)
	if n == 0 {
		return 0, nil
	}
	r.total += int64(n)
	if n >= r.cap {
		r.buf = append(r.buf[:0], p[n-r.cap:]...)
		r.dropped = r.total - int64(len(r.buf))
		return n, nil
	}
	overflow := len(r.buf) + n - r.cap
	if overflow > 0 {
		copy(r.buf, r.buf[overflow:])
		r.buf = r.buf[:len(r.buf)-overflow]
		r.dropped += int64(overflow)
	}
	r.buf = append(r.buf, p...)
	return n, nil
}

// ReadSince returns all buffered bytes written after cursor and the new cursor value.
func (r *ringBuffer) ReadSince(cursor int64) (string, int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cursor < r.dropped {
		cursor = r.dropped
	}
	if cursor >= r.total {
		return "", r.total
	}
	start := int(cursor - r.dropped)
	if start < 0 {
		start = 0
	}
	if start >= len(r.buf) {
		return "", r.total
	}
	return string(r.buf[start:]), r.total
}

// Snapshot returns the full retained buffer content.
func (r *ringBuffer) Snapshot() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(r.buf)
}

type managedProcess struct {
	id         string
	command    string
	workdir    string
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	output     *ringBuffer
	startedAt  time.Time
	done       chan struct{}

	mu         sync.Mutex
	finished   bool
	finishedAt time.Time
	exitCode   int
	exitErr    string
	pollCursor int64
	stdinClosed bool
}

func (p *managedProcess) status() (running bool, exitCode int, elapsed time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.finished {
		return false, p.exitCode, p.finishedAt.Sub(p.startedAt)
	}
	return true, 0, time.Since(p.startedAt)
}

func (p *managedProcess) summary() map[string]any {
	running, exitCode, elapsed := p.status()
	state := "running"
	if !running {
		state = "exited"
	}
	m := map[string]any{
		"session_id": p.id,
		"command":    p.command,
		"workdir":    p.workdir,
		"status":     state,
		"elapsed_ms": elapsed.Milliseconds(),
	}
	if !running {
		m["exit_code"] = exitCode
	}
	return m
}

// processRegistry manages background processes started by terminal(background=true).
type processRegistry struct {
	mu    sync.RWMutex
	procs map[string]*managedProcess
}

func newProcessRegistry() *processRegistry {
	return &processRegistry{
		procs: make(map[string]*managedProcess),
	}
}

func generateSessionID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("proc_%x", time.Now().UnixNano())
	}
	return "proc_" + hex.EncodeToString(b[:])
}

func (r *processRegistry) start(command, workdir string, env []string) (*managedProcess, error) {
	shell, args := shellCommand(command)
	cmd := exec.Command(shell, args...)
	cmd.Dir = workdir
	if len(env) > 0 {
		cmd.Env = env
	}
	setProcGroup(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}

	rb := newRingBuffer(defaultRingBufferBytes)
	cmd.Stdout = rb
	cmd.Stderr = rb

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start process: %w", err)
	}

	mp := &managedProcess{
		id:        generateSessionID(),
		command:   command,
		workdir:   workdir,
		cmd:       cmd,
		stdin:     stdin,
		output:    rb,
		startedAt: time.Now(),
		done:      make(chan struct{}),
	}

	r.mu.Lock()
	r.procs[mp.id] = mp
	r.mu.Unlock()

	go func() {
		waitErr := cmd.Wait()
		code := 0
		errMsg := ""
		if waitErr != nil {
			errMsg = waitErr.Error()
			if exitErr, ok := waitErr.(*exec.ExitError); ok {
				code = exitErr.ExitCode()
			} else {
				code = -1
			}
		}
		mp.mu.Lock()
		mp.finished = true
		mp.finishedAt = time.Now()
		mp.exitCode = code
		mp.exitErr = errMsg
		mp.mu.Unlock()
		close(mp.done)
	}()

	return mp, nil
}

// resolve looks up a session by exact ID or unique prefix (matching Hermes's process_manage).
func (r *processRegistry) resolve(sessionID string) (*managedProcess, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	if p, ok := r.procs[sessionID]; ok {
		return p, nil
	}
	var matches []*managedProcess
	for id, p := range r.procs {
		if strings.HasPrefix(id, sessionID) || strings.HasPrefix(strings.TrimPrefix(id, "proc_"), sessionID) {
			matches = append(matches, p)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("ambiguous session_id prefix %q (%d matches)", sessionID, len(matches))
	}
	return nil, fmt.Errorf("process session %q not found", sessionID)
}

func (r *processRegistry) list() []map[string]any {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := make([]*managedProcess, 0, len(r.procs))
	for _, p := range r.procs {
		list = append(list, p)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].startedAt.After(list[j].startedAt)
	})
	out := make([]map[string]any, 0, len(list))
	for _, p := range list {
		out = append(out, p.summary())
	}
	return out
}

func (r *processRegistry) poll(sessionID string) (map[string]any, error) {
	p, err := r.resolve(sessionID)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	delta, nextCursor := p.output.ReadSince(p.pollCursor)
	p.pollCursor = nextCursor
	p.mu.Unlock()

	res := p.summary()
	res["output"] = delta
	return res, nil
}

func (r *processRegistry) log(sessionID string, offset, limit int, hasOffset bool) (map[string]any, error) {
	p, err := r.resolve(sessionID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = defaultLogLimit
	}
	snap := p.output.Snapshot()
	var lines []string
	if snap != "" {
		lines = strings.Split(strings.TrimSuffix(snap, "\n"), "\n")
	}
	total := len(lines)

	startIdx := 0
	if !hasOffset {
		// Default: last `limit` lines (matching Hermes's default last 200).
		if total > limit {
			startIdx = total - limit
		}
	} else if offset < 0 {
		startIdx = total + offset
		if startIdx < 0 {
			startIdx = 0
		}
	} else {
		startIdx = offset
		if startIdx > total {
			startIdx = total
		}
	}
	endIdx := startIdx + limit
	if endIdx > total {
		endIdx = total
	}

	res := p.summary()
	res["total_lines"] = total
	res["offset"] = startIdx
	res["limit"] = limit
	res["output"] = strings.Join(lines[startIdx:endIdx], "\n")
	return res, nil
}

func (r *processRegistry) wait(sessionID string, timeout time.Duration, cancelCh <-chan struct{}) (map[string]any, error) {
	p, err := r.resolve(sessionID)
	if err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	timedOut := false
	select {
	case <-p.done:
	case <-timer.C:
		timedOut = true
	case <-cancelCh:
		timedOut = true
	}

	p.mu.Lock()
	delta, nextCursor := p.output.ReadSince(p.pollCursor)
	p.pollCursor = nextCursor
	p.mu.Unlock()

	res := p.summary()
	res["timed_out"] = timedOut
	res["output"] = delta
	return res, nil
}

func (r *processRegistry) writeStdin(sessionID, data string, appendNewline bool) (map[string]any, error) {
	p, err := r.resolve(sessionID)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	if p.finished {
		p.mu.Unlock()
		return nil, fmt.Errorf("process %q has already exited", p.id)
	}
	if p.stdinClosed || p.stdin == nil {
		p.mu.Unlock()
		return nil, fmt.Errorf("stdin for process %q is closed", p.id)
	}
	stdin := p.stdin
	p.mu.Unlock()

	payload := data
	if appendNewline {
		payload += "\n"
	}
	n, err := io.WriteString(stdin, payload)
	if err != nil {
		return nil, fmt.Errorf("write stdin: %w", err)
	}
	res := p.summary()
	res["bytes_written"] = n
	return res, nil
}

func (r *processRegistry) closeStdin(sessionID string) (map[string]any, error) {
	p, err := r.resolve(sessionID)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	if !p.stdinClosed && p.stdin != nil {
		_ = p.stdin.Close()
		p.stdinClosed = true
	}
	p.mu.Unlock()
	res := p.summary()
	res["stdin_closed"] = true
	return res, nil
}

func (r *processRegistry) kill(sessionID string) (map[string]any, error) {
	p, err := r.resolve(sessionID)
	if err != nil {
		return nil, err
	}
	running, _, _ := p.status()
	if running {
		_ = killProcGroup(p.cmd)
		select {
		case <-p.done:
		case <-time.After(2 * time.Second):
		}
	}
	p.mu.Lock()
	delta, nextCursor := p.output.ReadSince(p.pollCursor)
	p.pollCursor = nextCursor
	p.mu.Unlock()

	res := p.summary()
	res["killed"] = true
	res["output"] = delta
	return res, nil
}

func (r *processRegistry) shutdownAll() {
	r.mu.RLock()
	procs := make([]*managedProcess, 0, len(r.procs))
	for _, p := range r.procs {
		procs = append(procs, p)
	}
	r.mu.RUnlock()

	for _, p := range procs {
		running, _, _ := p.status()
		if running {
			_ = killProcGroup(p.cmd)
		}
		p.mu.Lock()
		if !p.stdinClosed && p.stdin != nil {
			_ = p.stdin.Close()
			p.stdinClosed = true
		}
		p.mu.Unlock()
	}
	for _, p := range procs {
		select {
		case <-p.done:
		case <-time.After(1 * time.Second):
		}
	}
}
