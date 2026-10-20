package pi_subagents

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Ports of src/shared/{file-system-retry,atomic-json,capacity-resilient-json,file-coalescer,jsonl-writer,accessible-dir}.ts.

// ---- file-system-retry.ts -----------------------------------------------------------------------------------------------

const fsRetryMaxTotalMsEnv = "PI_SUBAGENT_FS_RETRY_MAX_TOTAL_MS"

var baseFileSystemRetryDelaysMs = []int{10, 25, 50, 100, 200, 500, 1000, 2000, 4000}

// fsCodeError is an error carrying a Node-style errno code ("EPERM", "ENOSPC"), as the original's fs errors do.
type fsCodeError struct{ Code, Msg string }

func (e *fsCodeError) Error() string { return e.Msg }

func newFSError(code, msg string) error { return &fsCodeError{Code: code, Msg: msg} }

// fsErrorCode is error.code of a Node fs error: the code of an fsCodeError or the errno name of an OS error ("" if none).
func fsErrorCode(err error) string {
	var ce *fsCodeError
	if errors.As(err, &ce) {
		return ce.Code
	}
	for code, errno := range map[string]syscall.Errno{"EACCES": syscall.EACCES, "EPERM": syscall.EPERM, "EBUSY": syscall.EBUSY, "ENOSPC": syscall.ENOSPC, "EDQUOT": syscall.EDQUOT, "EMFILE": syscall.EMFILE, "ENFILE": syscall.ENFILE, "ENOENT": syscall.ENOENT, "ENOTDIR": syscall.ENOTDIR} {
		if errors.Is(err, errno) {
			return code
		}
	}
	if errors.Is(err, fs.ErrPermission) {
		return "EACCES"
	}
	if errors.Is(err, fs.ErrNotExist) {
		return "ENOENT"
	}
	return ""
}

// resolveFileSystemRetryDelays clamps the retry ladder to a total sleep budget (PI_SUBAGENT_FS_RETRY_MAX_TOTAL_MS), keeping its
// length because callers index it by attempt number. Unset or blank leaves the ladder unchanged.
func resolveFileSystemRetryDelays(env envLookup, base []int) ([]int, error) {
	raw, ok := env(fsRetryMaxTotalMsEnv)
	if !ok || jsTrim(raw) == "" {
		return base, nil
	}
	b := jsStringToNumber(raw)
	if math.IsNaN(b) || b != math.Trunc(b) || b < 0 || math.IsInf(b, 0) {
		return nil, fmt.Errorf("%s must be a non-negative integer number of milliseconds.", fsRetryMaxTotalMsEnv)
	}
	budget, spent := int(b), 0
	out := make([]int, len(base))
	for i, d := range base {
		allowed := max(0, min(d, budget-spent))
		spent += allowed
		out[i] = allowed
	}
	return out, nil
}

// defaultFileSystemRetryDelaysMs is the ladder for this process; an invalid budget variable leaves the base ladder.
func defaultFileSystemRetryDelaysMs() []int {
	d, err := resolveFileSystemRetryDelays(osEnv, baseFileSystemRetryDelaysMs)
	if err != nil {
		return baseFileSystemRetryDelaysMs
	}
	return d
}

// waitForFileSystemRetry sleeps delayMs (nothing for <= 0).
func waitForFileSystemRetry(delayMs int) {
	if delayMs > 0 {
		time.Sleep(time.Duration(delayMs) * time.Millisecond)
	}
}

func isRetryableFileSystemError(err error) bool {
	switch fsErrorCode(err) {
	case "EACCES", "EBUSY", "EPERM":
		return true
	}
	return false
}

func isStorageCapacityError(err error) bool {
	switch fsErrorCode(err) {
	case "EDQUOT", "EMFILE", "ENFILE", "ENOSPC":
		return true
	}
	return false
}

// runFileSystemOperationWithRetry runs op, waiting and retrying along the ladder while it fails with a retryable code.
func runFileSystemOperationWithRetry(op func() error, retryDelaysMs []int, wait func(int)) error {
	if wait == nil {
		wait = waitForFileSystemRetry
	}
	for attempt := 0; ; attempt++ {
		err := op()
		if err == nil {
			return nil
		}
		if attempt >= len(retryDelaysMs) || !isRetryableFileSystemError(err) {
			return err
		}
		wait(retryDelaysMs[attempt])
	}
}

// ---- atomic-json.ts -----------------------------------------------------------------------------------------------------

const maxPathComponentBytes = 255

// atomicJSONFS is the file operations of the atomic writer (the original's AtomicJsonFs).
type atomicJSONFS interface {
	MkdirAll(dir string) error
	WriteFile(path string, data []byte, mode os.FileMode, hasMode bool) error
	Rename(from, to string) error
	Remove(path string) error
}

type osAtomicFS struct{}

func (osAtomicFS) MkdirAll(dir string) error { return os.MkdirAll(dir, 0o777) }
func (osAtomicFS) WriteFile(path string, data []byte, mode os.FileMode, hasMode bool) error {
	perm := os.FileMode(0o666)
	if hasMode {
		perm = mode
	}
	return os.WriteFile(path, data, perm)
}
func (osAtomicFS) Rename(a, b string) error { return os.Rename(a, b) }
func (osAtomicFS) Remove(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// atomicJSONOptions are the original's AtomicJsonWriterOptions; nil functions and pointers take the original's defaults.
type atomicJSONOptions struct {
	FS                             atomicJSONFS
	Now                            func() int64
	PID                            int
	Random                         func() float64
	Mode                           *os.FileMode
	RetryRenameErrors              *bool
	RetryDirectoryErrors           *bool
	IgnoreCleanupErrorAfterSuccess bool
	RetryDelaysMs                  []int
	HasRetryDelays                 bool
	Wait                           func(int)
}

// jsRandomString is Math.random().toString(36).slice(2): the base-36 digits after the point.
func jsRandomString(r float64) string {
	var b strings.Builder
	for i := 0; i < 11 && r > 0; i++ {
		r *= 36
		d := int(r)
		r -= float64(d)
		b.WriteString(strconv.FormatInt(int64(d), 36))
	}
	return b.String()
}

func tempBaseName(filePath string, pid int, nowMs int64, randomID string) string {
	suffix := fmt.Sprintf(".%d.%d.%s.tmp", pid, nowMs, randomID)
	base := filepath.Base(filePath)
	preferred := "." + base + suffix
	if len(preferred) <= maxPathComponentBytes {
		return preferred
	}
	sum := sha256.Sum256([]byte(base))
	return "." + hex.EncodeToString(sum[:]) + suffix
}

// createAtomicJSONWriter is the original's createAtomicJsonWriter: a function that writes JSON (2-space indent) to a temp
// file beside the target and renames it over the target.
func createAtomicJSONWriter(o atomicJSONOptions) func(path string, payload any) error {
	fsImpl := o.FS
	if fsImpl == nil {
		fsImpl = osAtomicFS{}
	}
	now := o.Now
	if now == nil {
		now = func() int64 { return time.Now().UnixMilli() }
	}
	pid := o.PID
	if pid == 0 {
		pid = os.Getpid()
	}
	random := o.Random
	if random == nil {
		random = func() float64 { return float64(time.Now().UnixNano()%1000003) / 1000003 }
	}
	retryRename := windowsHost()
	if o.RetryRenameErrors != nil {
		retryRename = *o.RetryRenameErrors
	}
	retryDir := retryRename
	if o.RetryDirectoryErrors != nil {
		retryDir = *o.RetryDirectoryErrors
	}
	delays := o.RetryDelaysMs
	if !o.HasRetryDelays {
		delays = defaultFileSystemRetryDelaysMs()
	}
	var renameDelays, dirDelays []int
	if retryRename {
		renameDelays = delays
	}
	if retryDir {
		dirDelays = delays
	}
	return func(filePath string, payload any) (err error) {
		if err := runFileSystemOperationWithRetry(func() error { return fsImpl.MkdirAll(filepath.Dir(filePath)) }, dirDelays, o.Wait); err != nil {
			return err
		}
		temp := filepath.Join(filepath.Dir(filePath), tempBaseName(filePath, pid, now(), jsRandomString(random())))
		var writeErr error
		defer func() {
			if cleanup := fsImpl.Remove(temp); cleanup != nil && writeErr == nil && !o.IgnoreCleanupErrorAfterSuccess {
				err = cleanup
			}
		}()
		mode, hasMode := os.FileMode(0), false
		if o.Mode != nil {
			mode, hasMode = *o.Mode, true
		}
		if writeErr = fsImpl.WriteFile(temp, []byte(marshalJSON(payload, "  ")), mode, hasMode); writeErr != nil {
			return writeErr
		}
		if writeErr = runFileSystemOperationWithRetry(func() error { return fsImpl.Rename(temp, filePath) }, renameDelays, o.Wait); writeErr != nil {
			return writeErr
		}
		return nil
	}
}

// writeAtomicJSON and writePrivateAtomicJSON are the original's default writers (the second with mode 0600).
var (
	writeAtomicJSON        = createAtomicJSONWriter(atomicJSONOptions{})
	writePrivateAtomicJSON = createAtomicJSONWriter(atomicJSONOptions{Mode: func() *os.FileMode { m := os.FileMode(0o600); return &m }()})
)

// ---- timers -------------------------------------------------------------------------------------------------------------

// timerAPI is the original's TimerApi.
type timerAPI interface {
	SetTimeout(handler func(), delayMs int) any
	ClearTimeout(handle any)
}

type realTimers struct{}

func (realTimers) SetTimeout(h func(), d int) any {
	return time.AfterFunc(time.Duration(d)*time.Millisecond, h)
}
func (realTimers) ClearTimeout(h any) {
	if t, ok := h.(*time.Timer); ok {
		t.Stop()
	}
}

// ---- capacity-resilient-json.ts -----------------------------------------------------------------------------------------

type capacityPending struct {
	payload any
	write   func(path string, payload any) error
}

// capacityResilientJSONOptions are the original's options; nil fields take the defaults.
type capacityResilientJSONOptions struct {
	Write        func(path string, payload any) error
	RetryDelayMs int
	HasRetryMs   bool
	Timers       timerAPI
	OnError      func(err error, path string)
	OnSuccess    func(path string, payload any)
}

// capacityResilientJSONWriter defers only storage-capacity failures, keeping the newest payload per path.
type capacityResilientJSONWriter struct {
	mu      sync.Mutex
	o       capacityResilientJSONOptions
	pending map[string]*capacityPending
	order   []string
	timer   any
	hasTime bool
}

func newCapacityResilientJSONWriter(o capacityResilientJSONOptions) *capacityResilientJSONWriter {
	if o.Write == nil {
		o.Write = writeAtomicJSON
	}
	if !o.HasRetryMs {
		o.RetryDelayMs = 1000
	}
	if o.Timers == nil {
		o.Timers = realTimers{}
	}
	if o.OnError == nil {
		o.OnError = func(err error, path string) { fmt.Fprintf(os.Stderr, "Failed to persist JSON '%s': %v\n", path, err) }
	}
	return &capacityResilientJSONWriter{o: o, pending: map[string]*capacityPending{}}
}

func (w *capacityResilientJSONWriter) reportError(err error, path string) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "Failed to report JSON persistence error for '%s': %v\n", path, r)
		}
	}()
	w.o.OnError(err, path)
}

func (w *capacityResilientJSONWriter) notifySuccess(path string, payload any) {
	if w.o.OnSuccess == nil {
		return
	}
	func() {
		defer func() {
			if r := recover(); r != nil {
				w.reportError(fmt.Errorf("%v", r), path)
			}
		}()
		w.o.OnSuccess(path, payload)
	}()
}

func (w *capacityResilientJSONWriter) removePending(path string) {
	if _, ok := w.pending[path]; !ok {
		return
	}
	delete(w.pending, path)
	for i, p := range w.order {
		if p == path {
			w.order = append(w.order[:i], w.order[i+1:]...)
			break
		}
	}
}

// scheduleRetry must be called with w.mu held.
func (w *capacityResilientJSONWriter) scheduleRetry() {
	if w.hasTime {
		return
	}
	w.hasTime = true
	w.timer = w.o.Timers.SetTimeout(func() {
		w.mu.Lock()
		w.hasTime, w.timer = false, nil
		paths := append([]string{}, w.order...)
		w.mu.Unlock()
		for _, path := range paths {
			w.mu.Lock()
			entry := w.pending[path]
			w.mu.Unlock()
			if entry == nil {
				continue
			}
			err := entry.write(path, entry.payload)
			w.mu.Lock()
			switch {
			case err == nil:
				w.removePending(path)
				w.mu.Unlock()
				w.notifySuccess(path, entry.payload)
			case isStorageCapacityError(err):
				w.mu.Unlock()
			default:
				w.removePending(path)
				w.mu.Unlock()
				w.reportError(err, path)
			}
		}
		w.mu.Lock()
		if len(w.pending) > 0 {
			w.scheduleRetry()
		}
		w.mu.Unlock()
	}, w.o.RetryDelayMs)
}

// Write writes now; a storage-capacity failure is kept for retry, any other failure is returned.
func (w *capacityResilientJSONWriter) Write(path string, payload any, op func(path string, payload any) error) error {
	if op == nil {
		op = w.o.Write
	}
	err := op(path, payload)
	if err == nil {
		w.mu.Lock()
		w.removePending(path)
		w.mu.Unlock()
		w.notifySuccess(path, payload)
		return nil
	}
	if !isStorageCapacityError(err) {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.pending[path]; !ok {
		w.order = append(w.order, path)
	}
	w.pending[path] = &capacityPending{payload: payload, write: op}
	w.scheduleRetry()
	return nil
}

func (w *capacityResilientJSONWriter) PendingCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.pending)
}

func (w *capacityResilientJSONWriter) Dispose() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.hasTime {
		w.o.Timers.ClearTimeout(w.timer)
	}
	w.hasTime, w.timer = false, nil
	w.pending, w.order = map[string]*capacityPending{}, nil
}

// ---- file-coalescer.ts --------------------------------------------------------------------------------------------------

// fileCoalescer runs a handler once per file after a delay, however often it is scheduled meanwhile.
type fileCoalescer struct {
	mu      sync.Mutex
	handler func(file string)
	delayMs int
	timers  timerAPI
	pending map[string]any
}

func createFileCoalescer(handler func(file string), defaultDelayMs int, timers timerAPI) *fileCoalescer {
	if timers == nil {
		timers = realTimers{}
	}
	return &fileCoalescer{handler: handler, delayMs: defaultDelayMs, timers: timers, pending: map[string]any{}}
}

// Schedule reports whether it started a timer (false: one is already pending). delayMs < 0 is the default delay.
func (c *fileCoalescer) Schedule(file string, delayMs int) bool {
	if delayMs < 0 {
		delayMs = c.delayMs
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.pending[file]; ok {
		return false
	}
	c.pending[file] = c.timers.SetTimeout(func() {
		c.mu.Lock()
		delete(c.pending, file)
		c.mu.Unlock()
		c.handler(file)
	}, delayMs)
	return true
}

// Flush runs a pending file's handler now.
func (c *fileCoalescer) Flush(file string) bool {
	c.mu.Lock()
	timer, ok := c.pending[file]
	if ok {
		c.timers.ClearTimeout(timer)
		delete(c.pending, file)
	}
	c.mu.Unlock()
	if !ok {
		return false
	}
	c.handler(file)
	return true
}

// Clear cancels every pending handler.
func (c *fileCoalescer) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, t := range c.pending {
		c.timers.ClearTimeout(t)
	}
	c.pending = map[string]any{}
}

// ---- jsonl-writer.ts ----------------------------------------------------------------------------------------------------

const defaultMaxJSONLBytes = 50 * 1024 * 1024

// drainableSource is what a JSONL writer pauses while its stream is backed up.
type drainableSource interface {
	Pause()
	Resume()
}

// jsonlWriteStream is the original's JsonlWriteStream: Write reports false when the stream is backed up (then OnceDrain
// fires when it empties).
type jsonlWriteStream interface {
	Write(chunk string) bool
	OnceDrain(listener func())
	OnError(listener func(err error))
	End(callback func())
}

type jsonlWriterDeps struct {
	CreateWriteStream func(path string) (jsonlWriteStream, error)
	MaxBytes          int
	HasMaxBytes       bool
}

// jsonlWriter appends lines to a file, best effort: a stream that cannot open or write is dropped, not raised.
type jsonlWriter struct {
	mu           sync.Mutex
	stream       jsonlWriteStream
	source       drainableSource
	backpressure bool
	closed       bool
	bytes        int
	maxBytes     int
}

type fileJSONLStream struct{ f *os.File }

func (s fileJSONLStream) Write(chunk string) bool { _, _ = s.f.WriteString(chunk); return true }
func (s fileJSONLStream) OnceDrain(func())        {}
func (s fileJSONLStream) OnError(func(error))     {}
func (s fileJSONLStream) End(cb func()) {
	_ = s.f.Close()
	if cb != nil {
		cb()
	}
}

func createJSONLWriter(filePath string, source drainableSource, deps jsonlWriterDeps) *jsonlWriter {
	w := &jsonlWriter{source: source, maxBytes: defaultMaxJSONLBytes}
	if deps.HasMaxBytes {
		w.maxBytes = deps.MaxBytes
	}
	if filePath == "" {
		w.closed = true
		return w
	}
	create := deps.CreateWriteStream
	if create == nil {
		create = func(p string) (jsonlWriteStream, error) {
			f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o666)
			if err != nil {
				return nil, err
			}
			return fileJSONLStream{f}, nil
		}
	}
	stream, err := create(filePath)
	if err != nil {
		w.closed = true
		return w
	}
	w.stream = stream
	stream.OnError(func(error) {
		w.mu.Lock()
		w.closed, w.stream = true, nil
		resume := w.backpressure
		w.backpressure = false
		w.mu.Unlock()
		if resume {
			source.Resume()
		}
	})
	return w
}

// WriteLine appends the line and a newline (blank lines, and lines past the byte cap, are dropped).
func (w *jsonlWriter) WriteLine(line string) {
	w.mu.Lock()
	if w.stream == nil || w.closed || jsTrim(line) == "" {
		w.mu.Unlock()
		return
	}
	chunk := line + "\n"
	if w.bytes+len(chunk) > w.maxBytes {
		w.mu.Unlock()
		return
	}
	stream := w.stream
	w.mu.Unlock()
	ok := stream.Write(chunk)
	w.mu.Lock()
	w.bytes += len(chunk)
	pause := !ok && !w.backpressure
	if pause {
		w.backpressure = true
	}
	w.mu.Unlock()
	if pause {
		w.source.Pause()
		stream.OnceDrain(func() {
			w.mu.Lock()
			w.backpressure = false
			closed := w.closed
			w.mu.Unlock()
			if !closed {
				w.source.Resume()
			}
		})
	}
}

// Close ends the stream once and waits for it to finish.
func (w *jsonlWriter) Close() {
	w.mu.Lock()
	if w.stream == nil || w.closed {
		w.mu.Unlock()
		return
	}
	w.closed = true
	stream := w.stream
	w.stream = nil
	w.mu.Unlock()
	done := make(chan struct{})
	stream.End(func() { close(done) })
	<-done
}

// ---- accessible-dir.ts --------------------------------------------------------------------------------------------------

// accessibleDirFS is the original's AccessibleDirFs.
type accessibleDirFS interface {
	MkdirAll(dir string) error
	Access(dir string) error // read and write access
}

type osAccessibleFS struct{}

func (osAccessibleFS) MkdirAll(dir string) error { return os.MkdirAll(dir, 0o777) }
func (osAccessibleFS) Access(dir string) error {
	st, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return nil
	}
	probe, err := os.Open(dir)
	if err != nil {
		return err
	}
	probe.Close()
	f, err := os.CreateTemp(dir, ".access-*")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

type accessibleDirOptions struct {
	FS                   accessibleDirFS
	RetryDirectoryErrors *bool
	RetryDelaysMs        []int
	HasRetryDelays       bool
	Wait                 func(int)
	PID                  int
}

// ensureAccessibleDir creates dirPath and checks it is readable and writable; when the OS refuses (EPERM, EACCES) it falls back
// to a pid-scoped sibling directory without touching the primary's contents.
func ensureAccessibleDir(dirPath string, o accessibleDirOptions) (string, error) {
	fsImpl := o.FS
	if fsImpl == nil {
		fsImpl = osAccessibleFS{}
	}
	pid := o.PID
	if pid == 0 {
		pid = os.Getpid()
	}
	retry := windowsHost()
	if o.RetryDirectoryErrors != nil {
		retry = *o.RetryDirectoryErrors
	}
	var delays []int
	if retry {
		delays = o.RetryDelaysMs
		if !o.HasRetryDelays {
			delays = defaultFileSystemRetryDelaysMs()
		}
	}
	mkdir := func(t string) error {
		return runFileSystemOperationWithRetry(func() error { return fsImpl.MkdirAll(t) }, delays, o.Wait)
	}
	access := func(t string) error {
		return runFileSystemOperationWithRetry(func() error { return fsImpl.Access(t) }, delays, o.Wait)
	}
	err := mkdir(dirPath)
	if err == nil {
		err = access(dirPath)
	}
	if err == nil {
		return dirPath, nil
	}
	if code := fsErrorCode(err); code != "EPERM" && code != "EACCES" {
		return "", err
	}
	fallback := fmt.Sprintf("%s-%d", dirPath, pid)
	if err := mkdir(fallback); err != nil {
		return "", err
	}
	if err := access(fallback); err != nil {
		return "", err
	}
	return fallback, nil
}

// scopedDirs is the original's DIRS: the shared directories, which a host may reassign at run time (for a fallback).
type scopedDirs struct{ Results, Async, Chain, Artifacts string }

// DIRS is the process's shared directories.
var DIRS = scopedDirs{Results: resultsDir(), Async: asyncDir(), Chain: chainRunsDir(), Artifacts: tempArtifactsDir()}

// nodeFSErrorText is the message Node gives an fs error: "ENOTDIR: not a directory, scandir '/x'". The messages the original
// builds from them ("... is unreadable at '...': <that>") reach the model, so the port words them the same way.
func nodeFSErrorText(err error, op, path string) string {
	code := fsErrorCode(err)
	desc := map[string]string{
		"ENOENT": "no such file or directory", "ENOTDIR": "not a directory", "EACCES": "permission denied", "EPERM": "operation not permitted",
		"EEXIST": "file already exists", "EISDIR": "illegal operation on a directory", "EBUSY": "resource busy or locked",
		"ENOSPC": "no space left on device",
	}[code]
	if code == "" {
		switch {
		case errors.Is(err, syscall.EEXIST):
			code, desc = "EEXIST", "file already exists"
		case errors.Is(err, syscall.EISDIR):
			code, desc = "EISDIR", "illegal operation on a directory"
		case errors.Is(err, syscall.ENOTEMPTY):
			code, desc = "ENOTEMPTY", "directory not empty"
		default:
			return err.Error()
		}
	}
	return fmt.Sprintf("%s: %s, %s '%s'", code, desc, op, path)
}
