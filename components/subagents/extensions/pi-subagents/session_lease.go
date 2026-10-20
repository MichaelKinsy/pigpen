package pi_subagents

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Revival leases (runs/shared/session-lease.ts): one writer on a session file. A resumed child revives a session; the lease
// directory beside the temp root names its owner, and a lease is only taken over when its owner is demonstrably gone.

func sessionLeasesDir() string { return filepath.Join(tempRootDir(), "session-leases") }

type sessionLeaseRequest struct {
	SessionFile     string
	RunID           string
	SourceRunID     string
	ParentSessionID string
}

type sessionLeaseOwner struct {
	Version                    int    `json:"version"`
	Token                      string `json:"token"`
	CanonicalSessionFile       string `json:"canonicalSessionFile"`
	RunID                      string `json:"runId"`
	SourceRunID                string `json:"sourceRunId"`
	ParentSessionID            string `json:"parentSessionId,omitempty"`
	PID                        int    `json:"pid"`
	Hostname                   string `json:"hostname"`
	ProcessStartIdentity       string `json:"processStartIdentity,omitempty"`
	WriterState                string `json:"writerState"` // none, spawning, running
	WriterPID                  int    `json:"writerPid,omitempty"`
	WriterProcessStartIdentity string `json:"writerProcessStartIdentity,omitempty"`
	AcquiredAt                 string `json:"acquiredAt"`
	AcquiredAtMs               int64  `json:"acquiredAtMs"`
	UpdatedAtMs                int64  `json:"updatedAtMs"`
}

// sessionLeaseOptions are the seams of the lease; a zero field takes the system value.
type sessionLeaseOptions struct {
	RootDir              string
	Now                  func() int64
	Token                func() string
	PID                  int
	Hostname             string
	ProcessStartIdentity string
	// IsProcessAlive reports (alive, known); unknown when the probe cannot tell.
	IsProcessAlive          func(pid int) (alive, known bool)
	GetProcessStartIdentity func(pid int) string // "" when unknown
}

type sessionLeaseWriter struct {
	State string // none, spawning, running
	PID   int    // running only
}

type sessionLeaseConflictError struct {
	msg   string
	Owner *sessionLeaseOwner
}

func (e *sessionLeaseConflictError) Error() string { return e.msg }

type sessionLeaseHandle struct {
	LeaseDir string
	Owner    *sessionLeaseOwner
	getID    func(pid int) string
	now      func() int64
}

type sessionLeaseState struct {
	State                string // free, owned, unreadable
	CanonicalSessionFile string
	CanonicalSessionID   string
	Owner                *sessionLeaseOwner
}

func processStartIdentityOf(pid int) string {
	if runtime.GOOS != "linux" {
		return ""
	}
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return ""
	}
	s := string(b)
	end := strings.LastIndex(s, ")")
	if end < 0 {
		return ""
	}
	fields := strings.Fields(s[end+1:])
	if len(fields) > 19 {
		return "linux:" + fields[19]
	}
	return ""
}

func processIsAlive(pid int) (alive, known bool) {
	err := defaultKill(pid, 0)
	switch {
	case err == nil, err == errNotPermitted:
		return true, true
	case err == errNoSuchProcess:
		return false, true
	}
	return false, false
}

func canonicalSessionFilePath(sessionFile string) (string, error) {
	abs, err := filepath.Abs(sessionFile)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

func canonicalSessionIDOf(canonical string) string {
	key := canonical
	if runtime.GOOS == "windows" {
		key = strings.ToLower(canonical)
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func canonicalSessionID(sessionFile string) (string, error) {
	c, err := canonicalSessionFilePath(sessionFile)
	if err != nil {
		return "", err
	}
	return canonicalSessionIDOf(c), nil
}

func sessionLeaseDir(sessionFile, rootDir string) (string, error) {
	id, err := canonicalSessionID(sessionFile)
	if err != nil {
		return "", err
	}
	if rootDir == "" {
		rootDir = sessionLeasesDir()
	}
	return filepath.Join(rootDir, id), nil
}

func inspectSessionLease(sessionFile, rootDir string) (sessionLeaseState, error) {
	canonical, err := canonicalSessionFilePath(sessionFile)
	if err != nil {
		return sessionLeaseState{}, err
	}
	id := canonicalSessionIDOf(canonical)
	if rootDir == "" {
		rootDir = sessionLeasesDir()
	}
	leaseDir := filepath.Join(rootDir, id)
	st := sessionLeaseState{CanonicalSessionFile: canonical, CanonicalSessionID: id}
	if _, err := os.Stat(leaseDir); err != nil {
		st.State = "free"
		return st, nil
	}
	if owner := readLeaseOwner(leaseDir); owner != nil {
		st.State, st.Owner = "owned", owner
	} else {
		st.State = "unreadable"
	}
	return st, nil
}

// readLeaseOwner parses owner.json with the original's strict checks; nil when it is missing or invalid.
func readLeaseOwner(leaseDir string) *sessionLeaseOwner {
	b, err := os.ReadFile(filepath.Join(leaseDir, "owner.json"))
	if err != nil {
		return nil
	}
	var raw map[string]any
	if json.Unmarshal(b, &raw) != nil {
		return nil
	}
	isStr := func(k string) bool { _, ok := raw[k].(string); return ok }
	isNum := func(k string) bool { _, ok := raw[k].(float64); return ok }
	posInt := func(k string) bool {
		n, ok := raw[k].(float64)
		return ok && n == float64(int64(n)) && n > 0
	}
	if v, _ := raw["version"].(float64); v != 1 || !isStr("token") || !isStr("canonicalSessionFile") || !isStr("runId") || !isStr("sourceRunId") || !posInt("pid") ||
		!isStr("hostname") || !isStr("acquiredAt") || !isNum("acquiredAtMs") || !isNum("updatedAtMs") {
		return nil
	}
	ws, _ := raw["writerState"].(string)
	if ws != "none" && ws != "spawning" && ws != "running" {
		return nil
	}
	for _, k := range []string{"parentSessionId", "processStartIdentity", "writerProcessStartIdentity"} {
		if v, ok := raw[k]; ok && v != nil {
			if _, isS := v.(string); !isS {
				return nil
			}
		}
	}
	if _, has := raw["writerPid"]; has && !posInt("writerPid") {
		return nil
	}
	_, hasWriterPID := raw["writerPid"]
	_, hasWriterID := raw["writerProcessStartIdentity"]
	if ws == "running" && !hasWriterPID {
		return nil
	}
	if ws != "running" && (hasWriterPID || hasWriterID) {
		return nil
	}
	var o sessionLeaseOwner
	if json.Unmarshal(b, &o) != nil {
		return nil
	}
	return &o
}

func leaseConflictMessage(canonical string, owner *sessionLeaseOwner) string {
	if owner == nil {
		return fmt.Sprintf("Direct revival of session '%s' is blocked by an existing lease with unreadable owner metadata. Refusing to reclaim it without proof that the owner is stale.", canonical)
	}
	parent := ""
	if owner.ParentSessionID != "" {
		parent = fmt.Sprintf(", parent session '%s'", owner.ParentSessionID)
	}
	return fmt.Sprintf("Direct revival of session '%s' is already owned by run '%s' (source run '%s'%s, pid %d on %s). Wait for that revival to finish or start a separate continuation without reusing this session file.",
		canonical, owner.RunID, owner.SourceRunID, parent, owner.PID, owner.Hostname)
}

type leaseProbe struct {
	hostname string
	alive    func(int) (bool, bool)
	identity func(int) string
}

func (p leaseProbe) gone(pid int, startIdentity string) bool {
	alive, known := p.alive(pid)
	if known && !alive {
		return true
	}
	if !known || !alive || startIdentity == "" {
		return false
	}
	cur := p.identity(pid)
	return cur != "" && cur != startIdentity
}

func (p leaseProbe) demonstrablyStale(o *sessionLeaseOwner) bool {
	if o.Hostname != p.hostname {
		return false
	}
	if !p.gone(o.PID, o.ProcessStartIdentity) {
		return false
	}
	if o.WriterState == "spawning" {
		return false
	}
	if o.WriterState == "none" {
		return true
	}
	return o.WriterPID != 0 && p.gone(o.WriterPID, o.WriterProcessStartIdentity)
}

// createLeaseDirectory publishes the lease with one rename; false when another owner got there first.
func createLeaseDirectory(leaseDir string, owner *sessionLeaseOwner) (bool, error) {
	tempDir := leaseDir + ".candidate-" + owner.Token
	if err := os.MkdirAll(filepath.Dir(leaseDir), 0o700); err != nil {
		return false, err
	}
	_ = os.RemoveAll(tempDir)
	if err := os.Mkdir(tempDir, 0o700); err != nil {
		return false, err
	}
	defer os.RemoveAll(tempDir)
	b, err := json.MarshalIndent(owner, "", "  ")
	if err != nil {
		return false, err
	}
	if err := os.WriteFile(filepath.Join(tempDir, "owner.json"), b, 0o600); err != nil {
		return false, err
	}
	if err := os.Rename(tempDir, leaseDir); err != nil {
		if _, serr := os.Stat(leaseDir); serr == nil {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

var unsafeTombstoneChars = regexp.MustCompile(`[^A-Za-z0-9._-]`)

func acquireSessionLease(req sessionLeaseRequest, o sessionLeaseOptions) (*sessionLeaseHandle, error) {
	canonical, err := canonicalSessionFilePath(req.SessionFile)
	if err != nil {
		return nil, err
	}
	rootDir := o.RootDir
	if rootDir == "" {
		rootDir = sessionLeasesDir()
	}
	leaseDir := filepath.Join(rootDir, canonicalSessionIDOf(canonical))
	now := o.Now
	if now == nil {
		now = nowMs
	}
	pid := o.PID
	if pid == 0 {
		pid = os.Getpid()
	}
	hostname := o.Hostname
	if hostname == "" {
		hostname, _ = os.Hostname()
	}
	getID := o.GetProcessStartIdentity
	if getID == nil {
		getID = processStartIdentityOf
	}
	identity := o.ProcessStartIdentity
	if identity == "" {
		identity = getID(pid)
	}
	if identity == "" && pid == os.Getpid() {
		identity = "runtime:" + strconv.FormatInt(processStart.UnixMilli(), 10)
	}
	token := randomUUID()
	if o.Token != nil {
		token = o.Token()
	}
	acquired := now()
	owner := &sessionLeaseOwner{Version: 1, Token: token, CanonicalSessionFile: canonical, RunID: req.RunID, SourceRunID: req.SourceRunID, ParentSessionID: req.ParentSessionID,
		PID: pid, Hostname: hostname, ProcessStartIdentity: identity, WriterState: "none", AcquiredAt: time.UnixMilli(acquired).UTC().Format("2006-01-02T15:04:05.000Z"),
		AcquiredAtMs: acquired, UpdatedAtMs: acquired}
	alive := o.IsProcessAlive
	if alive == nil {
		alive = processIsAlive
	}
	probe := leaseProbe{hostname: hostname, alive: alive, identity: getID}
	for range 4 {
		created, err := createLeaseDirectory(leaseDir, owner)
		if err != nil {
			return nil, err
		}
		if created {
			return &sessionLeaseHandle{LeaseDir: leaseDir, Owner: owner, getID: getID, now: now}, nil
		}
		existing := readLeaseOwner(leaseDir)
		if existing == nil || !probe.demonstrablyStale(existing) {
			return nil, &sessionLeaseConflictError{msg: leaseConflictMessage(canonical, existing), Owner: existing}
		}
		// The per-owner tombstone is retained: every contender that saw this stale token targets the same occupied destination,
		// so only the first can rename it and a later contender cannot move a successor lease by mistake.
		tombstone := leaseDir + ".stale-" + unsafeTombstoneChars.ReplaceAllString(existing.Token, "-")
		if err := os.Rename(leaseDir, tombstone); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if _, serr := os.Stat(tombstone); serr == nil {
				continue
			}
			return nil, err
		}
	}
	existing := readLeaseOwner(leaseDir)
	return nil, &sessionLeaseConflictError{msg: leaseConflictMessage(canonical, existing), Owner: existing}
}

// processStart is when this process started, for the identity of a platform without a start probe.
var processStart = time.Now()

func (h *sessionLeaseHandle) UpdateWriter(w sessionLeaseWriter) error {
	cur := readLeaseOwner(h.LeaseDir)
	if cur == nil || cur.Token != h.Owner.Token {
		return fmt.Errorf("Session revival lease ownership changed for run '%s'.", h.Owner.RunID)
	}
	next := *h.Owner
	next.WriterState = w.State
	next.WriterPID, next.WriterProcessStartIdentity = 0, ""
	if w.State == "running" {
		next.WriterPID = w.PID
		next.WriterProcessStartIdentity = h.getID(w.PID)
	}
	next.UpdatedAtMs = h.now()
	if err := asyncWritePrivateJSON(filepath.Join(h.LeaseDir, "owner.json"), &next); err != nil {
		return err
	}
	*h.Owner = next
	return nil
}

func (h *sessionLeaseHandle) Release() bool {
	cur := readLeaseOwner(h.LeaseDir)
	if cur == nil || cur.Token != h.Owner.Token {
		return false
	}
	_ = os.RemoveAll(h.LeaseDir)
	_, err := os.Stat(h.LeaseDir)
	return err != nil
}
