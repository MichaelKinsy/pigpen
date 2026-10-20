package pi_subagents

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf16"
)

// Port of src/runs/background/control-channel.ts: the portable file inbox of a background run's control directory. The parent
// drops an interrupt, timeout, stop or steer request file; the active owner of the run consumes each exactly once. The file
// layout (control/interrupt.json, timeout.json, stop-requests/, steer-requests/, revival-briefs/, the closed markers) is the
// original's, so either implementation can own a run the other started.

const (
	steerRequestsDirName   = "steer-requests"
	stopRequestsDirName    = "stop-requests"
	revivalBriefsDirName   = "revival-briefs"
	steerInboxClosedFile   = "steer-inbox-closed.json"
	stopInboxClosedFile    = "stop-inbox-closed.json"
	maxSteerQueueSize      = 20
	maxSteerMessageBytes   = 128 * 1024
	maxSteerRequestIDLen   = 256
	maxControlChildIndex   = 1_000_000
	maxSteerTargetIndexes  = 1_000
	controlInboxPollPeriod = 250 * time.Millisecond
)

// controlClock and controlStopWrite are the clock and the stop-request writer of this file; a test replaces them.
var (
	controlClock     = nowMs
	controlStopWrite = func(path string, req controlRequest) error { return asyncWriteJSON(path, req) }
)

func controlInboxDir(asyncDir string) string { return filepath.Join(asyncDir, "control") }
func interruptRequestPath(asyncDir string) string {
	return filepath.Join(controlInboxDir(asyncDir), "interrupt.json")
}
func timeoutRequestPath(asyncDir string) string {
	return filepath.Join(controlInboxDir(asyncDir), "timeout.json")
}

// stopRequestPath is the legacy single stop file.
func stopRequestPath(asyncDir string) string {
	return filepath.Join(controlInboxDir(asyncDir), "stop.json")
}
func stopRequestsDir(asyncDir string) string {
	return filepath.Join(controlInboxDir(asyncDir), stopRequestsDirName)
}
func steerRequestsDir(asyncDir string) string {
	return filepath.Join(controlInboxDir(asyncDir), steerRequestsDirName)
}
func steerInboxClosedPath(asyncDir string) string {
	return filepath.Join(controlInboxDir(asyncDir), steerInboxClosedFile)
}
func stopInboxClosedPath(asyncDir string) string {
	return filepath.Join(controlInboxDir(asyncDir), stopInboxClosedFile)
}

// controlFS is the file operations of the inbox consumers, so a test can make one fail (the original's ControlChannelFs).
type controlFS interface {
	MkdirAll(dir string) error
	Exists(path string) bool
	// Remove removes a file or a directory tree; a missing path is an error that satisfies os.ErrNotExist.
	Remove(path string) error
	// ReadDir lists the entry names of a directory.
	ReadDir(dir string) ([]string, error)
	ReadFile(path string) ([]byte, error)
}

type osControlFS struct{}

func (osControlFS) MkdirAll(dir string) error { return os.MkdirAll(dir, 0o755) }
func (osControlFS) Exists(path string) bool   { _, err := os.Stat(path); return err == nil }
func (osControlFS) Remove(path string) error {
	if _, err := os.Lstat(path); err != nil {
		return err
	}
	return os.RemoveAll(path)
}
func (osControlFS) ReadDir(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	return names, nil
}
func (osControlFS) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }

// ---- request shapes ----

// controlRequest is an interrupt, timeout or stop request.
type controlRequest struct {
	Type        string `json:"type"`
	TS          int64  `json:"ts,omitempty"`
	Source      string `json:"source,omitempty"`
	Reason      string `json:"reason,omitempty"`
	TargetIndex *int   `json:"targetIndex,omitempty"`
	ChildID     string `json:"childId,omitempty"`
}

// steerRequest is one steering request of the steer inbox.
type steerRequest struct {
	Type          string            `json:"type"`
	ID            string            `json:"id"`
	TS            float64           `json:"ts"`
	Message       string            `json:"message"`
	Mode          steerDeliveryMode `json:"mode,omitempty"`
	TargetIndex   *int              `json:"targetIndex,omitempty"`
	TargetIndexes []int             `json:"targetIndexes,omitempty"`
	Source        string            `json:"source,omitempty"`
}

// steerPayload is what requestAsyncSteer takes (the original's payload object): a nil/zero field is unset.
type steerPayload struct {
	Message       string
	Mode          steerDeliveryMode
	TargetIndex   *int
	TargetIndexes []int
	// HasTargetIndexes distinguishes an unset targetIndexes from an empty one.
	HasTargetIndexes bool
	Source           string
	ID               string
	TS               float64
}

func validChildIndex(i int) bool { return i >= 0 && i <= maxControlChildIndex }

func validStopChildID(id string) bool {
	return jsTrim(id) != "" && len(utf16Of(id)) <= 256 && !strings.ContainsAny(id, "\r\n")
}

// utf16Of is the UTF-16 units of s (JavaScript's string length).
func utf16Of(s string) []uint16 { return utf16.Encode([]rune(s)) }

func hasJSSpace(s string) bool {
	for _, r := range s {
		if isJSSpace(r) {
			return true
		}
	}
	return false
}

func validSteerMode(m steerDeliveryMode) bool {
	return m == "" || m == steerModeSteer || m == steerModeFollowUp || m == steerModeAuto
}

func validSteerRequest(r *steerRequest, targetIndexSet, targetIndexesSet bool) bool {
	if r.Type != "steer" || r.ID == "" || hasJSSpace(r.ID) || len(utf16Of(r.ID)) > maxSteerRequestIDLen {
		return false
	}
	if math.IsNaN(r.TS) || math.IsInf(r.TS, 0) || r.TS <= 0 {
		return false
	}
	if jsTrim(r.Message) == "" || len(r.Message) > maxSteerMessageBytes || !validSteerMode(r.Mode) {
		return false
	}
	if targetIndexSet && (r.TargetIndex == nil || !validChildIndex(*r.TargetIndex)) {
		return false
	}
	if targetIndexesSet {
		if targetIndexSet || len(r.TargetIndexes) == 0 || len(r.TargetIndexes) > maxSteerTargetIndexes {
			return false
		}
		seen := map[int]bool{}
		for _, i := range r.TargetIndexes {
			if !validChildIndex(i) || seen[i] {
				return false
			}
			seen[i] = true
		}
	}
	if r.Source != "" && (jsTrim(r.Source) == "" || len(utf16Of(r.Source)) > 256) {
		return false
	}
	return true
}

func steerRequestFileName(r *steerRequest) string {
	ts := jsNumber(r.TS)
	if len(ts) < 13 {
		ts = strings.Repeat("0", 13-len(ts)) + ts
	}
	return ts + "-" + base64URL(r.ID) + ".json"
}

func stopRequestFileName(r *controlRequest) string {
	ts := fmt.Sprintf("%013d", r.TS)
	return ts + "-" + randomUUID() + ".json"
}

// writeSteerRequestToDir validates the request and writes it atomically into dir (created when missing).
func writeSteerRequestToDir(dir string, r *steerRequest) (string, error) {
	if !validSteerRequest(r, r.TargetIndex != nil, r.TargetIndexes != nil) {
		return "", errors.New("steer request is malformed or exceeds transport limits.")
	}
	p := filepath.Join(dir, steerRequestFileName(r))
	return p, asyncWriteJSON(p, r)
}

// writeSteerRequestToExistingDir is writeSteerRequestToDir for a directory that must already exist.
func writeSteerRequestToExistingDir(dir string, r *steerRequest) (string, error) {
	if !validSteerRequest(r, r.TargetIndex != nil, r.TargetIndexes != nil) {
		return "", errors.New("steer request is malformed or exceeds transport limits.")
	}
	p := filepath.Join(dir, steerRequestFileName(r))
	body, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	tmp := filepath.Join(dir, "."+filepath.Base(p)+"."+fmt.Sprint(os.Getpid())+"."+fmt.Sprint(controlClock())+"."+randomUUID()+".tmp")
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	_, werr := f.Write(body)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp, p)
	}
	if werr != nil {
		_ = os.Remove(tmp)
		return "", werr
	}
	return p, nil
}

// ---- parent side ----

// requestAsyncInterrupt drops the portable interrupt request the runner's inbox picks up on any OS.
func requestAsyncInterrupt(asyncDir, source string) (string, error) {
	p := interruptRequestPath(asyncDir)
	return p, asyncWriteJSON(p, controlRequest{Type: "interrupt", TS: controlClock(), Source: source})
}

func requestAsyncTimeout(asyncDir, source, reason string) (string, error) {
	p := timeoutRequestPath(asyncDir)
	return p, asyncWriteJSON(p, controlRequest{Type: "timeout", TS: controlClock(), Source: source, Reason: reason})
}

func closeStopInbox(asyncDir string) error {
	return asyncWriteJSON(stopInboxClosedPath(asyncDir), map[string]any{"version": 1, "closedAt": controlClock()})
}

func closeSteerInbox(asyncDir, state string) error {
	return asyncWriteJSON(steerInboxClosedPath(asyncDir), map[string]any{"version": 1, "closedAt": controlClock(), "state": state})
}

// requestAsyncStop writes a stop request into the inbox; once the runner closed the inbox it is refused.
func requestAsyncStop(asyncDir, source string, targetIndex *int, childID string) (string, error) {
	if targetIndex != nil && !validChildIndex(*targetIndex) {
		return "", errors.New("child index must be a non-negative integer.")
	}
	if childID != "" && !validStopChildID(childID) {
		return "", errors.New("stop childId must be a non-empty string without newlines and at most 256 characters.")
	}
	closed := stopInboxClosedPath(asyncDir)
	if pathExists(closed) {
		return "", errors.New(stopInboxClosedMessage)
	}
	req := controlRequest{Type: "stop", TS: controlClock(), Source: source, TargetIndex: targetIndex, ChildID: childID}
	p := filepath.Join(stopRequestsDir(asyncDir), stopRequestFileName(&req))
	if err := controlStopWrite(p, req); err != nil {
		return "", err
	}
	if pathExists(closed) {
		_ = os.Remove(p)
		return "", errors.New(stopInboxClosedMessage)
	}
	return p, nil
}

// requestAsyncSteer validates a steer and writes it into the steer inbox (the id, ts and source default as the original's do).
func requestAsyncSteer(asyncDir string, in steerPayload) (string, error) {
	message := jsTrim(in.Message)
	if message == "" {
		return "", errors.New("steer message must not be empty.")
	}
	if len(message) > maxSteerMessageBytes {
		return "", fmt.Errorf("steer message exceeds %d UTF-8 bytes.", maxSteerMessageBytes)
	}
	if in.TargetIndex != nil && !validChildIndex(*in.TargetIndex) {
		return "", errors.New("steer targetIndex must be an integer between 0 and 1000000.")
	}
	if in.HasTargetIndexes {
		bad := in.TargetIndex != nil || len(in.TargetIndexes) == 0 || len(in.TargetIndexes) > maxSteerTargetIndexes
		seen := map[int]bool{}
		for _, i := range in.TargetIndexes {
			if !validChildIndex(i) || seen[i] {
				bad = true
			}
			seen[i] = true
		}
		if bad {
			return "", errors.New("steer targetIndexes must contain 1-1000 unique non-negative integers and cannot be combined with targetIndex.")
		}
	}
	closed := steerInboxClosedPath(asyncDir)
	if pathExists(closed) {
		return "", errors.New("Async run no longer accepts steering requests.")
	}
	req := steerRequest{Type: "steer", ID: in.ID, TS: in.TS, Message: message, Source: in.Source}
	if req.ID == "" {
		req.ID = randomUUID()
	}
	if req.TS == 0 {
		req.TS = float64(controlClock())
	}
	if in.Mode != "" && in.Mode != steerModeSteer {
		req.Mode = in.Mode
	}
	if in.TargetIndex != nil {
		req.TargetIndex = in.TargetIndex
	}
	if in.HasTargetIndexes {
		req.TargetIndexes = append([]int{}, in.TargetIndexes...)
	}
	p, err := writeSteerRequestToDir(steerRequestsDir(asyncDir), &req)
	if err != nil {
		return "", err
	}
	if pathExists(closed) {
		_ = os.Remove(p)
		return "", errors.New("Async run stopped accepting steering before the request was committed.")
	}
	return p, nil
}

// ---- runner side ----

// parseSteerRequest validates a decoded file and returns it normalized (trimmed id, message and source).
func parseSteerRequest(raw []byte) *steerRequest {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil || obj == nil {
		return nil
	}
	var r steerRequest
	// every field must have its exact type: decode strictly field by field
	if !decodeStrict(obj["type"], &r.Type) || obj["type"] == nil || !decodeStrict(obj["id"], &r.ID) || obj["id"] == nil ||
		!decodeStrict(obj["ts"], &r.TS) || obj["ts"] == nil || !decodeStrict(obj["message"], &r.Message) || obj["message"] == nil {
		return nil
	}
	var mode string
	if m, ok := obj["mode"]; ok && !isJSONNull(m) {
		if !decodeStrict(m, &mode) {
			return nil
		}
		r.Mode = steerDeliveryMode(mode)
	} else if ok && isJSONNull(m) {
		return nil
	}
	_, targetIndexSet := obj["targetIndex"]
	if targetIndexSet {
		var i int
		if !decodeStrict(obj["targetIndex"], &i) || isJSONNull(obj["targetIndex"]) {
			return nil
		}
		r.TargetIndex = &i
	}
	_, targetIndexesSet := obj["targetIndexes"]
	if targetIndexesSet {
		if !decodeStrict(obj["targetIndexes"], &r.TargetIndexes) || isJSONNull(obj["targetIndexes"]) {
			return nil
		}
	}
	if s, ok := obj["source"]; ok {
		if isJSONNull(s) || !decodeStrict(s, &r.Source) {
			return nil
		}
	}
	if !validSteerRequest(&r, targetIndexSet, targetIndexesSet) {
		return nil
	}
	out := steerRequest{Type: "steer", ID: jsTrim(r.ID), TS: r.TS, Message: jsTrim(r.Message), Mode: r.Mode, TargetIndex: r.TargetIndex}
	if targetIndexesSet {
		out.TargetIndexes = append([]int{}, r.TargetIndexes...)
	}
	if jsTrim(r.Source) != "" {
		out.Source = r.Source
	}
	return &out
}

func isJSONNull(b json.RawMessage) bool { return strings.TrimSpace(string(b)) == "null" }

// decodeStrict decodes b into v, refusing a value of another JSON type (a missing field decodes nothing and succeeds).
func decodeStrict(b json.RawMessage, v any) bool {
	if b == nil {
		return true
	}
	if isJSONNull(b) {
		return false
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if dec.Decode(v) != nil {
		return false
	}
	// a number must be an integer where the target is an int; json already refuses 1.5 for an int
	return true
}

func isNotExistErr(err error) bool { return err != nil && errors.Is(err, fs.ErrNotExist) }

// consumeSteerRequestsFromDir reads and removes the requests of a steer directory, oldest first. A request that cannot be read or
// removed stays for the next scan (onError is told); one removed by another consumer first is not delivered twice.
func consumeSteerRequestsFromDir(dir string, fsys controlFS, onError func(error)) []steerRequest {
	if fsys == nil {
		fsys = osControlFS{}
	}
	report := func(err error) {
		if onError != nil {
			onError(err)
		}
	}
	names, err := fsys.ReadDir(dir)
	if err != nil {
		if !isNotExistErr(err) {
			report(err)
		}
		return nil
	}
	var entries []string
	for _, n := range names {
		if strings.HasSuffix(n, ".json") {
			entries = append(entries, n)
		}
	}
	sort.Strings(entries)
	var out []steerRequest
	for _, n := range entries {
		p := filepath.Join(dir, n)
		text, err := fsys.ReadFile(p)
		if err != nil {
			if !isNotExistErr(err) {
				report(err)
			}
			continue
		}
		parsed := parseSteerRequest(text)
		if err := fsys.Remove(p); err != nil {
			// Already removed by a concurrent check: do not execute it twice.
			if !isNotExistErr(err) {
				report(err)
			}
			continue
		}
		if parsed != nil {
			out = append(out, *parsed)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].TS != out[j].TS {
			return out[i].TS < out[j].TS
		}
		return localeCompare(out[i].ID, out[j].ID) < 0
	})
	return out
}

func consumeSteerRequests(asyncDir string, fsys controlFS, onError func(error)) []steerRequest {
	return consumeSteerRequestsFromDir(steerRequestsDir(asyncDir), fsys, onError)
}

// queueRevivalBrief keeps a follow-up for the next resume of a retained run (at most 20, FIFO).
func queueRevivalBrief(asyncDir string, r steerRequest) (string, error) {
	dir := filepath.Join(controlInboxDir(asyncDir), revivalBriefsDirName)
	queued := 0
	if names, err := os.ReadDir(dir); err == nil {
		for _, e := range names {
			if strings.HasSuffix(e.Name(), ".json") {
				queued++
			}
		}
	}
	if queued >= maxSteerQueueSize {
		return "", fmt.Errorf("Follow-up queue is full (%d messages).", maxSteerQueueSize)
	}
	r.Mode = steerModeFollowUp
	return writeSteerRequestToDir(dir, &r)
}

type revivalBrief struct {
	Request steerRequest
	Path    string
}

func readRevivalBriefs(asyncDir string) []revivalBrief {
	dir := filepath.Join(controlInboxDir(asyncDir), revivalBriefsDirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var out []revivalBrief
	for _, n := range names {
		p := filepath.Join(dir, n)
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if r := parseSteerRequest(b); r != nil {
			out = append(out, revivalBrief{*r, p})
		}
	}
	return out
}

// consumeInterruptRequest removes a pending interrupt request; true when one was pending (a directory in its place counts, and goes).
func consumeInterruptRequest(asyncDir string, fsys controlFS) bool {
	return consumeMarker(interruptRequestPath(asyncDir), fsys)
}

func consumeTimeoutRequest(asyncDir string, fsys controlFS) bool {
	return consumeMarker(timeoutRequestPath(asyncDir), fsys)
}

func consumeMarker(p string, fsys controlFS) bool {
	if fsys == nil {
		fsys = osControlFS{}
	}
	if !fsys.Exists(p) {
		return false
	}
	_ = fsys.Remove(p) // already removed by a concurrent check: still consumed
	return true
}

func parseStopRequest(raw []byte) *controlRequest {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil || obj == nil {
		return nil
	}
	var typ string
	if decodeStrict(obj["type"], &typ) == false || typ != "stop" {
		return nil
	}
	out := controlRequest{Type: "stop"}
	if v, ok := obj["targetIndex"]; ok {
		var f float64
		if isJSONNull(v) || json.Unmarshal(v, &f) != nil || f != math.Trunc(f) || f < 0 || f > maxControlChildIndex {
			return nil
		}
		i := int(f)
		out.TargetIndex = &i
	}
	if v, ok := obj["childId"]; ok {
		var s string
		if isJSONNull(v) || json.Unmarshal(v, &s) != nil || !validStopChildID(s) {
			return nil
		}
		out.ChildID = s
	}
	if v, ok := obj["ts"]; ok {
		var f float64
		if json.Unmarshal(v, &f) == nil {
			out.TS = int64(f)
		}
	}
	if v, ok := obj["source"]; ok {
		var s string
		if json.Unmarshal(v, &s) == nil {
			out.Source = s
		}
	}
	if v, ok := obj["reason"]; ok {
		var s string
		if json.Unmarshal(v, &s) == nil {
			out.Reason = s
		}
	}
	return &out
}

func consumeStopRequestFile(p string, fsys controlFS, onError func(error)) *controlRequest {
	text, err := fsys.ReadFile(p)
	if err != nil {
		if !isNotExistErr(err) && onError != nil {
			onError(err)
		}
		return nil
	}
	req := parseStopRequest(text)
	if err := fsys.Remove(p); err != nil {
		// Execute only after a successful removal.
		if !isNotExistErr(err) && onError != nil {
			onError(err)
		}
		return nil
	}
	return req
}

// consumeStopRequestPayloads takes every pending stop request (the stop-requests directory, then the legacy stop.json), oldest first;
// a malformed one is dropped instead of widening to a run-level stop.
func consumeStopRequestPayloads(asyncDir string, fsys controlFS, onError func(error)) []controlRequest {
	if fsys == nil {
		fsys = osControlFS{}
	}
	dir := stopRequestsDir(asyncDir)
	var out []controlRequest
	if fsys.Exists(dir) {
		names, err := fsys.ReadDir(dir)
		if err != nil {
			if !isNotExistErr(err) && onError != nil {
				onError(err)
			}
			names = nil
		}
		var entries []string
		for _, n := range names {
			if strings.HasSuffix(n, ".json") {
				entries = append(entries, n)
			}
		}
		sort.Strings(entries)
		for _, n := range entries {
			if r := consumeStopRequestFile(filepath.Join(dir, n), fsys, onError); r != nil {
				out = append(out, *r)
			}
		}
	}
	legacy := stopRequestPath(asyncDir)
	if fsys.Exists(legacy) {
		if r := consumeStopRequestFile(legacy, fsys, onError); r != nil {
			out = append(out, *r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].TS < out[j].TS })
	return out
}

func consumeStopRequest(asyncDir string, fsys controlFS) bool {
	return len(consumeStopRequestPayloads(asyncDir, fsys, nil)) > 0
}

// consumeControl reads and removes the pending interrupt, stop and timeout requests of a run.
func consumeControl(asyncDir string) (interrupt, stop, timeout bool) {
	stop = len(consumeStopRequestPayloads(asyncDir, nil, nil)) > 0
	timeout = consumeTimeoutRequest(asyncDir, nil)
	interrupt = consumeInterruptRequest(asyncDir, nil)
	return
}

// ---- the watcher ----

// controlTimers starts the polling of an inbox; the returned func stops it. A test replaces it to fire a poll by hand.
type controlTimers interface {
	Every(d time.Duration, fn func()) (stop func())
}

type realControlTimers struct{}

func (realControlTimers) Every(d time.Duration, fn func()) func() {
	t := time.NewTicker(d)
	done := make(chan struct{})
	var once sync.Once
	go func() {
		for {
			select {
			case <-t.C:
				fn()
			case <-done:
				return
			}
		}
	}()
	return func() { once.Do(func() { t.Stop(); close(done) }) }
}

// controlWatch is the options of watchAsyncControlInbox (the original's): only the kinds with a handler are consumed.
type controlWatch struct {
	OnInterrupt func()
	OnTimeout   func()
	OnStop      func(controlRequest)
	OnSteer     func(steerRequest)
	// OnError is told of a failure; phase is "install", "scan" or "callback" (req is the steer request of a callback failure).
	OnError      func(err error, phase string, req *steerRequest)
	PollInterval time.Duration
	FS           controlFS
	Timers       controlTimers
}

// watchAsyncControlInbox consumes the control inbox of a run on every poll, once per distinct request, and returns the disposer.
// The original uses fs.watch with a slow safety poll where the platform has one; Go's standard library has no file watcher, so this
// is the original's portable branch (the one it takes on Darwin and Windows) on every platform: a poll every 250 ms.
func watchAsyncControlInbox(asyncDir string, o controlWatch) func() {
	fsys := o.FS
	if fsys == nil {
		fsys = osControlFS{}
	}
	timers := o.Timers
	if timers == nil {
		timers = realControlTimers{}
	}
	period := o.PollInterval
	if period <= 0 {
		period = controlInboxPollPeriod
	}
	report := func(err error, phase string, req *steerRequest) {
		defer func() { _ = recover() }() // a failing reporter must not end the watcher
		if o.OnError != nil {
			o.OnError(err, phase, req)
		} else {
			fmt.Fprintf(os.Stderr, "Control inbox %s failed: %v\n", phase, err)
		}
	}
	var dirs []string
	if o.OnInterrupt != nil || o.OnTimeout != nil || o.OnStop != nil {
		dirs = append(dirs, controlInboxDir(asyncDir))
	}
	if o.OnStop != nil {
		dirs = append(dirs, stopRequestsDir(asyncDir))
	}
	if o.OnSteer != nil {
		dirs = append(dirs, steerRequestsDir(asyncDir))
	}
	if len(dirs) == 0 {
		return func() {}
	}
	for _, d := range dirs {
		if err := fsys.MkdirAll(d); err != nil {
			report(err, "install", nil)
			break
		}
	}
	var checkMu sync.Mutex
	var disposed atomic.Bool
	guard := func(fn func()) {
		defer func() {
			if r := recover(); r != nil {
				report(fmt.Errorf("%v", r), "callback", nil)
			}
		}()
		fn()
	}
	check := func() {
		checkMu.Lock()
		defer checkMu.Unlock()
		if disposed.Load() {
			return
		}
		scan := func(err error) { report(err, "scan", nil) }
		if o.OnStop != nil {
			for _, r := range consumeStopRequestPayloads(asyncDir, fsys, scan) {
				guard(func() { o.OnStop(r) })
			}
		}
		if o.OnTimeout != nil && consumeTimeoutRequest(asyncDir, fsys) {
			guard(o.OnTimeout)
		}
		if o.OnInterrupt != nil && consumeInterruptRequest(asyncDir, fsys) {
			guard(o.OnInterrupt)
		}
		if o.OnSteer != nil {
			for _, r := range consumeSteerRequestsFromDir(steerRequestsDir(asyncDir), fsys, scan) {
				func() {
					defer func() {
						if rec := recover(); rec != nil {
							req := r
							report(fmt.Errorf("%v", rec), "callback", &req)
						}
					}()
					o.OnSteer(r)
				}()
			}
		}
	}
	// A request that arrived before the watcher started.
	check()
	stop := timers.Every(period, check)
	return func() {
		if !disposed.Swap(true) {
			stop()
		}
	}
}
