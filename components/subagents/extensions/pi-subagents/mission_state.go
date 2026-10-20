package pi_subagents

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Port of src/missions/workflow-state.ts: the JSON object a bound mission keeps for its workflow scripts (`state.get` and
// `state.set`), one file per mission, written under a lock directory that survives crashed and reused-pid owners.

const (
	stateLockStaleMs = 60_000
	// MissionStateMaxBytes is MISSION_STATE_MAX_BYTES.
	MissionStateMaxBytes = 256 * 1024
)

// MissionWorkflowStateOptions lets a test replace the process probes and the retry ladder.
type MissionWorkflowStateOptions struct {
	IsProcessAlive     func(pid int) bool
	GetProcessStartKey func(pid int) string // "" is unknown
	RetryDelaysMs      []int                // nil: the default ladder
}

func missionStatePath(loc MissionStoreLocation, missionID string) (string, error) {
	id, err := validateMissionID(missionID, "missionId")
	if err != nil {
		return "", err
	}
	return filepath.Join(loc.MissionDir, id, "state.json"), nil
}

type stateLockOwner struct {
	pid        int64
	token      string
	createdAt  int64
	processKey string
}

type stateLockOptions struct {
	isProcessAlive     func(int) bool
	getProcessStartKey func(int) string
	retryDelaysMs      []int
}

var (
	currentProcessKeyOnce sync.Once
	currentProcessKey     string
)

func linuxProcessStartKey(pid int) string {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return ""
	}
	s := string(raw)
	tail := strings.Fields(strings.TrimSpace(s[strings.LastIndex(s, ")")+2:]))
	if len(tail) > 19 && tail[19] != "" {
		return "linux:" + tail[19]
	}
	return ""
}

func commandOutput(name string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	missionPrepareCmd(cmd)
	var out bytes.Buffer
	cmd.Stdout = &out
	if cmd.Run() != nil {
		return ""
	}
	return strings.TrimSpace(out.String())
}

func computeProcessStartKey(pid int) string {
	switch runtime.GOOS {
	case "linux":
		if k := linuxProcessStartKey(pid); k != "" {
			return k
		}
		if raw := commandOutput("ps", "-p", strconv.Itoa(pid), "-o", "lstart="); raw != "" {
			return "ps:" + raw
		}
	case "windows":
		if raw := commandOutput("powershell.exe", "-NoProfile", "-Command", fmt.Sprintf(`(Get-CimInstance Win32_Process -Filter "ProcessId=%d").CreationDate`, pid)); raw != "" {
			return "win:" + raw
		}
	}
	return ""
}

// processStartKey identifies a process across pid reuse; foreign pids are resolved afresh, this process's once.
func processStartKey(pid int) string {
	if pid != os.Getpid() {
		return computeProcessStartKey(pid)
	}
	currentProcessKeyOnce.Do(func() { currentProcessKey = computeProcessStartKey(pid) })
	return currentProcessKey
}

func readStateLockOwner(lockPath string) *stateLockOwner {
	raw, err := os.ReadFile(filepath.Join(lockPath, "owner.json"))
	if err != nil {
		return nil
	}
	parsed, msg := missionParseJSON(string(raw))
	o, ok := parsed.(*jsObject)
	if msg != "" || !ok {
		return nil
	}
	pid, pidOK := safeInteger(o.vals["pid"])
	token, _ := o.vals["token"].(string)
	createdAt, createdOK := safeInteger(o.vals["createdAt"])
	if !pidOK || pid <= 0 || token == "" || !createdOK {
		return nil
	}
	owner := &stateLockOwner{pid: pid, token: token, createdAt: createdAt}
	if k, _ := o.vals["processKey"].(string); k != "" {
		owner.processKey = k
	}
	return owner
}

func mtimeAgeExceeds(p string, now time.Time) (bool, error) {
	st, err := os.Stat(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return float64(now.UnixMilli())-float64(st.ModTime().UnixNano())/1e6 > stateLockStaleMs, nil
}

func stateLockIsStale(lockPath string, o stateLockOptions) (bool, error) {
	if owner := readStateLockOwner(lockPath); owner != nil {
		if !o.isProcessAlive(int(owner.pid)) {
			return true, nil
		}
		if owner.processKey != "" {
			if observed := o.getProcessStartKey(int(owner.pid)); observed != "" {
				return owner.processKey != observed, nil
			}
			if int(owner.pid) == os.Getpid() {
				return true, nil
			}
		}
		return false, nil
	}
	return mtimeAgeExceeds(lockPath, time.Now())
}

func removeOwnedStateLock(lockPath string, owner *stateLockOwner) {
	current := readStateLockOwner(lockPath)
	if current == nil || current.token != owner.token {
		return
	}
	_ = os.RemoveAll(lockPath)
}

func tryMakeDirectory(dirPath string) (bool, error) {
	err := os.Mkdir(dirPath, 0o700)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	return false, err
}

func waitForStateLock(attempt int, delays []int, lockPath string) error {
	if attempt >= len(delays) {
		return fmt.Errorf("Timed out acquiring mission state lock '%s'.", lockPath)
	}
	waitForFileSystemRetry(delays[attempt])
	return nil
}

func reclaimStaleStateLock(lockPath, reclaimPath string, o stateLockOptions) (bool, error) {
	stale, err := stateLockIsStale(lockPath, o)
	if err != nil || !stale {
		return false, err
	}
	made, err := tryMakeDirectory(reclaimPath)
	if err != nil || !made {
		return false, err
	}
	defer os.RemoveAll(reclaimPath)
	stale, err = stateLockIsStale(lockPath, o)
	if err != nil || !stale {
		return false, err
	}
	_ = os.RemoveAll(lockPath)
	return true, nil
}

func withStateFileLock(filePath string, operation func() error, o stateLockOptions) error {
	if err := os.MkdirAll(filepath.Dir(filePath), 0o777); err != nil {
		return err
	}
	lockPath := filePath + ".lock"
	reclaimPath := lockPath + ".reclaim"
	var owner *stateLockOwner
	for attempt := 0; ; attempt++ {
		if _, err := os.Stat(reclaimPath); err == nil {
			stale, serr := mtimeAgeExceeds(reclaimPath, time.Now())
			if serr != nil {
				return serr
			}
			if stale {
				_ = os.RemoveAll(reclaimPath)
				continue
			}
			if err := waitForStateLock(attempt, o.retryDelaysMs, lockPath); err != nil {
				return err
			}
			continue
		}
		acquired, err := tryMakeDirectory(lockPath)
		if err != nil {
			if isRetryableFileSystemError(err) {
				if werr := waitForStateLock(attempt, o.retryDelaysMs, lockPath); werr != nil {
					return werr
				}
				continue
			}
			return fmt.Errorf("Failed to acquire mission state lock '%s': %s", lockPath, err.Error())
		}
		if !acquired {
			reclaimed, rerr := reclaimStaleStateLock(lockPath, reclaimPath, o)
			if rerr != nil {
				return rerr
			}
			if reclaimed {
				continue
			}
			if err := waitForStateLock(attempt, o.retryDelaysMs, lockPath); err != nil {
				return err
			}
			continue
		}
		key := o.getProcessStartKey(os.Getpid())
		owner = &stateLockOwner{pid: int64(os.Getpid()), token: missionUUID(), createdAt: time.Now().UnixMilli(), processKey: key}
		ownerJSON := newObject()
		ownerJSON.set("pid", float64(owner.pid))
		ownerJSON.set("token", owner.token)
		ownerJSON.set("createdAt", float64(owner.createdAt))
		if key != "" {
			ownerJSON.set("processKey", key)
		}
		if err := os.WriteFile(filepath.Join(lockPath, "owner.json"), []byte(marshalJSON(ownerJSON, "")), 0o600); err != nil {
			_ = os.RemoveAll(lockPath)
			return err
		}
		break
	}
	defer removeOwnedStateLock(lockPath, owner)
	return operation()
}

func validateStateKey(v any) (string, error) {
	s, ok := v.(string)
	if !ok || !workflowKeyPattern.MatchString(s) {
		return "", errors.New("state key must be 1-128 characters using letters, numbers, '.', '_' or '-', and start with a letter or number.")
	}
	return s, nil
}

// MissionWorkflowState is createMissionWorkflowState: the workflow's reads come from the file as it was when the first one
// ran, its writes go through the lock and re-read the file, so concurrent workflows keep each other's keys.
type MissionWorkflowState struct {
	path   string
	lock   stateLockOptions
	mu     sync.Mutex
	loaded bool
	values *jsObject
}

// NewMissionWorkflowState binds the state of one mission.
func NewMissionWorkflowState(loc MissionStoreLocation, missionID string, opts MissionWorkflowStateOptions) (*MissionWorkflowState, error) {
	p, err := missionStatePath(loc, missionID)
	if err != nil {
		return nil, err
	}
	s := &MissionWorkflowState{path: p, values: newObject(), lock: stateLockOptions{isProcessAlive: opts.IsProcessAlive, getProcessStartKey: opts.GetProcessStartKey, retryDelaysMs: opts.RetryDelaysMs}}
	if s.lock.isProcessAlive == nil {
		s.lock.isProcessAlive = missionProcessAlive
	}
	if s.lock.getProcessStartKey == nil {
		s.lock.getProcessStartKey = processStartKey
	}
	if s.lock.retryDelaysMs == nil {
		s.lock.retryDelaysMs = defaultFileSystemRetryDelaysMs()
	}
	return s, nil
}

// Path is the state file.
func (s *MissionWorkflowState) Path() string { return s.path }

func (s *MissionWorkflowState) readStateFile() (*jsObject, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return newObject(), nil
		}
		return nil, fmt.Errorf("Failed to read mission state '%s': %s", s.path, err.Error())
	}
	if len(raw) > MissionStateMaxBytes {
		return nil, fmt.Errorf("Mission state file '%s' exceeds the 256 KiB limit (%d bytes).", s.path, len(raw))
	}
	invalid := func(msg string) error { return fmt.Errorf("Invalid mission state file '%s': %s", s.path, msg) }
	parsed, msg := missionParseJSON(string(raw))
	if msg != "" {
		return nil, invalid(msg)
	}
	o, ok := parsed.(*jsObject)
	if !ok {
		return nil, invalid("root must be a JSON object")
	}
	if err := assertWorkflowJSONValue(o, "mission state", map[any]bool{}); err != nil {
		return nil, invalid(err.Error())
	}
	return o, nil
}

// Get is state.get: the value as of the first read, or undefined.
func (s *MissionWorkflowState) Get(key string) (any, error) {
	valid, err := validateStateKey(key)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loaded {
		values, err := s.readStateFile()
		if err != nil {
			return nil, err
		}
		s.values, s.loaded = values, true
	}
	if v, ok := s.values.vals[valid]; ok {
		return v, nil
	}
	return undef{}, nil
}

// Set is state.set.
func (s *MissionWorkflowState) Set(key string, value any) error {
	valid, err := validateStateKey(key)
	if err != nil {
		return err
	}
	if err := assertWorkflowJSONValue(value, "state.set('"+valid+"') value", map[any]bool{}); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return withStateFileLock(s.path, func() error {
		current, err := s.readStateFile()
		if err != nil {
			return err
		}
		next := current.clone()
		next.set(valid, value)
		if n := len(marshalJSON(next, "  ")); n > MissionStateMaxBytes {
			return fmt.Errorf("Mission state exceeds the 256 KiB limit (%d bytes; maximum %d bytes).", n, MissionStateMaxBytes)
		}
		if err := writePrivateAtomicJSON(s.path, next); err != nil {
			return err
		}
		s.values, s.loaded = next, true
		return nil
	}, s.lock)
}

// GetState and SetState make the state the `state` global of a workflow run (WorkflowStateStore).
func (s *MissionWorkflowState) GetState(key string) *WorkflowFuture[any] {
	v, err := s.Get(key)
	if err != nil {
		return WorkflowRejected[any](err)
	}
	return WorkflowResolved(v)
}

func (s *MissionWorkflowState) SetState(key string, value any) *WorkflowFuture[struct{}] {
	if err := s.Set(key, value); err != nil {
		return WorkflowRejected[struct{}](err)
	}
	return WorkflowResolved(struct{}{})
}

var _ WorkflowStateStore = (*MissionWorkflowState)(nil)
