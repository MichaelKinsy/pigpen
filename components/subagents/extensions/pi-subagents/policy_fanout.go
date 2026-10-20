package pi_subagents

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Port of src/runs/shared/run-fanout-budget.ts: a persisted cap on the children one top-level run may start, shared by every
// process of the run through a directory of claim files.

func runFanoutRoot() string { return filepath.Join(tempRootDir(), "run-fanout-budgets") }

// runFanoutBudgetDescriptor is the original's RunFanoutBudgetDescriptor.
type runFanoutBudgetDescriptor struct {
	RootRunID  string
	Directory  string
	Limit      int
	ParentPath string
}

func (d runFanoutBudgetDescriptor) object() *jsObject {
	o := newObject()
	o.set("version", 1.0)
	o.set("rootRunId", d.RootRunID)
	o.set("directory", d.Directory)
	o.set("limit", float64(d.Limit))
	if d.ParentPath != "" {
		o.set("parentPath", d.ParentPath)
	}
	return o
}

// runFanoutBudgetSnapshot is the original's RunFanoutBudgetSnapshot.
type runFanoutBudgetSnapshot struct{ Used, Limit, Remaining int }

// runFanoutRejection is the original's RunFanoutRejection.
type runFanoutRejection struct {
	Code                              string
	Path                              string
	Requested, Used, Limit, Remaining int
}

// runFanoutLimitError is the original's RunFanoutLimitError.
type runFanoutLimitError struct {
	Rejection runFanoutRejection
	Snapshot  runFanoutBudgetSnapshot
}

func (e *runFanoutLimitError) Error() string { return formatRunFanoutRejection(e.Rejection) }

func formatRunFanoutRejection(r runFanoutRejection) string {
	return fmt.Sprintf("Run fan-out limit reached at %s (%d/%d used; %d requested, %d remaining). No children from this admission group were started. Start a new top-level run or raise config.maxSubagentSpawnsPerRun.", r.Path, r.Used, r.Limit, r.Requested, r.Remaining)
}

func formatRunFanoutBudget(s runFanoutBudgetSnapshot) string {
	return fmt.Sprintf("Run fan-out: %d/%d used, %d remaining", s.Used, s.Limit, s.Remaining)
}

var fanoutUnsafeID = regexp.MustCompile(`[^A-Za-z0-9._-]`)

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func safeRootRunID(id string) string {
	s := fanoutUnsafeID.ReplaceAllString(id, "_")
	if r := []rune(s); len(r) > 120 {
		s = string(r[:120])
	}
	if s == "" {
		return newUUID()
	}
	return s
}

type fanoutManifest struct {
	rootRunID string
	limit     int
}

func parseFanoutManifest(v any) *fanoutManifest {
	o, ok := asObject(v)
	if !ok {
		return nil
	}
	root, _ := o.vals["rootRunId"].(string)
	lim, _ := asNumber(o.vals["limit"])
	created, hasCreated := asNumber(o.vals["createdAt"])
	if ver, _ := asNumber(o.vals["version"]); ver != 1 || root == "" || !isIntegerValue(o.vals["limit"]) || lim <= 0 || !hasCreated || created != created {
		return nil
	}
	return &fanoutManifest{root, int(lim)}
}

func readFanoutManifest(dir string) (*fanoutManifest, error) {
	p := filepath.Join(dir, "manifest.json")
	data, err := os.ReadFile(p)
	var parsed any
	if err == nil {
		var msg string
		parsed, msg = jsJSONParse(string(data))
		if msg != "" {
			err = errors.New(msg)
		}
	} else {
		err = errors.New(nodeFSErrorText(err, "open", p))
	}
	if err != nil {
		return nil, fmt.Errorf("Run fan-out budget manifest is unreadable at '%s': %s", dir, err.Error())
	}
	m := parseFanoutManifest(parsed)
	if m == nil {
		return nil, fmt.Errorf("Run fan-out budget manifest is invalid at '%s'.", dir)
	}
	return m, nil
}

func validateFanoutDirectory(dir string) (string, error) {
	abs, _ := filepath.Abs(dir)
	realDir, err := filepath.EvalSymlinks(abs)
	var realRoot string
	if err == nil {
		rootAbs, _ := filepath.Abs(runFanoutRoot())
		realRoot, err = filepath.EvalSymlinks(rootAbs)
	}
	if err != nil {
		return "", fmt.Errorf("Run fan-out budget directory is unavailable: %s", nodeFSErrorText(err, "realpath", abs))
	}
	if realDir != realRoot && !strings.HasPrefix(realDir, realRoot+string(filepath.Separator)) {
		return "", errors.New("Run fan-out budget directory resolves outside the managed budget root.")
	}
	return realDir, nil
}

// createRunFanoutBudget creates the directory of a run's budget (mode 0700) with its manifest.
func createRunFanoutBudget(rootRunID string, limit int) (runFanoutBudgetDescriptor, error) {
	if limit <= 0 {
		return runFanoutBudgetDescriptor{}, errors.New("Run fan-out limit must be a positive integer.")
	}
	root := runFanoutRoot()
	if err := os.MkdirAll(root, 0o700); err != nil {
		return runFanoutBudgetDescriptor{}, err
	}
	var dir string
	for {
		dir = filepath.Join(root, safeRootRunID(rootRunID)+"-"+newUUID())
		if _, err := os.Stat(dir); err != nil {
			break
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "claims"), 0o700); err != nil {
		return runFanoutBudgetDescriptor{}, err
	}
	m := newObject()
	m.set("version", 1.0)
	m.set("rootRunId", rootRunID)
	m.set("limit", float64(limit))
	m.set("createdAt", float64(time.Now().UnixMilli()))
	f, err := os.OpenFile(filepath.Join(dir, "manifest.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return runFanoutBudgetDescriptor{}, err
	}
	_, err = f.WriteString(marshalJSON(m, "") + "\n")
	f.Close()
	return runFanoutBudgetDescriptor{RootRunID: rootRunID, Directory: dir, Limit: limit}, err
}

// validateRunFanoutBudgetDescriptor checks a decoded descriptor against its manifest and the managed root.
func validateRunFanoutBudgetDescriptor(value any) (runFanoutBudgetDescriptor, error) {
	o, ok := asObject(value)
	if !ok {
		return runFanoutBudgetDescriptor{}, errors.New("Run fan-out budget descriptor is missing or invalid.")
	}
	root, _ := o.vals["rootRunId"].(string)
	dir, _ := o.vals["directory"].(string)
	lim, _ := asNumber(o.vals["limit"])
	parent, hasParent := o.vals["parentPath"], !isUndefined(o.vals["parentPath"])
	parentStr, parentOK := parent.(string)
	if ver, _ := asNumber(o.vals["version"]); ver != 1 || root == "" || dir == "" || !isIntegerValue(o.vals["limit"]) || lim <= 0 || (hasParent && !parentOK) {
		return runFanoutBudgetDescriptor{}, errors.New("Run fan-out budget descriptor is invalid.")
	}
	real, err := validateFanoutDirectory(dir)
	if err != nil {
		return runFanoutBudgetDescriptor{}, err
	}
	m, err := readFanoutManifest(real)
	if err != nil {
		return runFanoutBudgetDescriptor{}, err
	}
	if m.rootRunID != root || m.limit != int(lim) {
		return runFanoutBudgetDescriptor{}, errors.New("Run fan-out budget descriptor does not match its manifest.")
	}
	return runFanoutBudgetDescriptor{RootRunID: root, Directory: real, Limit: int(lim), ParentPath: parentStr}, nil
}

// writeRunFanoutBudgetDescriptor persists the (validated) descriptor in a run's directory.
func writeRunFanoutBudgetDescriptor(asyncDir string, d runFanoutBudgetDescriptor) error {
	valid, err := validateRunFanoutBudgetDescriptor(d.object())
	if err != nil {
		return err
	}
	if err := os.MkdirAll(asyncDir, 0o777); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(asyncDir, "run-fanout-budget.json"), []byte(marshalJSON(valid.object(), "")+"\n"), 0o600)
}

// readRunFanoutBudgetDescriptor is the persisted descriptor of a run (ok false when there is none).
func readRunFanoutBudgetDescriptor(asyncDir string) (d runFanoutBudgetDescriptor, ok bool, err error) {
	if asyncDir == "" {
		return d, false, nil
	}
	p := filepath.Join(asyncDir, "run-fanout-budget.json")
	data, rerr := os.ReadFile(p)
	if errors.Is(rerr, fs.ErrNotExist) {
		return d, false, nil
	}
	var msg string
	var parsed any
	if rerr != nil {
		msg = nodeFSErrorText(rerr, "open", p)
	} else if parsed, msg = jsJSONParse(string(data)); msg == "" {
		d, err = validateRunFanoutBudgetDescriptor(parsed)
		if err == nil {
			return d, true, nil
		}
		msg = err.Error()
	}
	return d, false, fmt.Errorf("Invalid persisted run fan-out budget '%s': %s", p, msg)
}

const admissionLockStaleMs = 60000

type admissionLockOwner struct {
	pid   int
	token string
}

func readAdmissionLockOwner(lockPath string) *admissionLockOwner {
	data, err := os.ReadFile(filepath.Join(lockPath, "owner.json"))
	if err != nil {
		return nil
	}
	v, msg := jsJSONParse(string(data))
	o, ok := asObject(v)
	if msg != "" || !ok {
		return nil
	}
	pid, _ := asNumber(o.vals["pid"])
	token, _ := o.vals["token"].(string)
	if isSafeInteger(o.vals["pid"]) && pid > 0 && token != "" {
		return &admissionLockOwner{int(pid), token}
	}
	return nil
}

func admissionLockIsStale(lockPath string) (bool, error) {
	if owner := readAdmissionLockOwner(lockPath); owner != nil {
		return !processAlive(owner.pid), nil
	}
	st, err := os.Stat(lockPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return time.Since(st.ModTime()) > admissionLockStaleMs*time.Millisecond, nil
}

func withAdmissionLock(dir string, op func() error) (err error) {
	lockPath := filepath.Join(dir, "admission.lock")
	owner := &admissionLockOwner{os.Getpid(), newUUID()}
	delays := defaultFileSystemRetryDelaysMs()
	for attempt := 0; ; attempt++ {
		mkerr := os.Mkdir(lockPath, 0o700)
		if mkerr == nil {
			ownerJSON := newObject()
			ownerJSON.set("pid", float64(owner.pid))
			ownerJSON.set("token", owner.token)
			if werr := os.WriteFile(filepath.Join(lockPath, "owner.json"), []byte(marshalJSON(ownerJSON, "")), 0o600); werr != nil {
				_ = os.RemoveAll(lockPath)
				return werr
			}
			break
		}
		if !errors.Is(mkerr, fs.ErrExist) {
			return mkerr
		}
		stale, serr := admissionLockIsStale(lockPath)
		if serr != nil {
			return serr
		}
		if stale {
			stalePath := filepath.Join(dir, "admission.stale-"+newUUID())
			if rerr := os.Rename(lockPath, stalePath); rerr == nil {
				_ = os.RemoveAll(stalePath)
				continue
			} else if !errors.Is(rerr, fs.ErrNotExist) {
				return rerr
			}
		}
		if attempt >= len(delays) {
			return fmt.Errorf("Timed out acquiring run fan-out admission lock at '%s'.", dir)
		}
		waitForFileSystemRetry(delays[attempt])
	}
	defer func() {
		if o := readAdmissionLockOwner(lockPath); o != nil && o.token == owner.token {
			_ = os.RemoveAll(lockPath)
		}
	}()
	return op()
}

var fanoutClaimName = regexp.MustCompile(`^\d{6}\.json$`)

func claimCount(dir string) (int, error) {
	claims := filepath.Join(dir, "claims")
	entries, err := os.ReadDir(claims)
	if err != nil {
		return 0, fmt.Errorf("Run fan-out claims directory is unreadable at '%s': %s", claims, nodeFSErrorText(err, "scandir", claims))
	}
	n := 0
	for _, e := range entries {
		if fanoutClaimName.MatchString(e.Name()) {
			n++
		}
	}
	return n, nil
}

// getRunFanoutBudgetSnapshot counts the claims of a (validated) budget.
func getRunFanoutBudgetSnapshot(d runFanoutBudgetDescriptor) (runFanoutBudgetSnapshot, error) {
	valid, err := validateRunFanoutBudgetDescriptor(d.object())
	if err != nil {
		return runFanoutBudgetSnapshot{}, err
	}
	used, err := claimCount(valid.Directory)
	if err != nil {
		return runFanoutBudgetSnapshot{}, err
	}
	return runFanoutBudgetSnapshot{Used: used, Limit: valid.Limit, Remaining: max(0, valid.Limit-used)}, nil
}

func qualifyRunFanoutPaths(d runFanoutBudgetDescriptor, paths []string) []string {
	prefix := jsTrim(d.ParentPath)
	out := make([]string, len(paths))
	for i, p := range paths {
		if prefix != "" {
			out[i] = prefix + "/" + p
		} else {
			out[i] = p
		}
	}
	return out
}

// commitRunFanoutBatch claims one slot per path under the admission lock, all or none, then runs commit; a failing commit
// releases the claims.
func commitRunFanoutBatch(d runFanoutBudgetDescriptor, paths []string, commit func(runFanoutBudgetSnapshot) error) error {
	valid, err := validateRunFanoutBudgetDescriptor(d.object())
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		snap, err := getRunFanoutBudgetSnapshot(valid)
		if err != nil {
			return err
		}
		return commit(snap)
	}
	qualified := qualifyRunFanoutPaths(valid, paths)
	return withAdmissionLock(valid.Directory, func() error {
		before, err := getRunFanoutBudgetSnapshot(valid)
		if err != nil {
			return err
		}
		if len(qualified) > before.Remaining {
			path := qualified[0]
			if before.Remaining < len(qualified) {
				path = qualified[before.Remaining]
			}
			rej := runFanoutRejection{Code: "RUN_FANOUT_LIMIT", Path: path, Requested: len(qualified), Used: before.Used, Limit: before.Limit, Remaining: before.Remaining}
			return &runFanoutLimitError{Rejection: rej, Snapshot: before}
		}
		var created []string
		rollback := func() {
			for _, p := range created {
				_ = os.Remove(p)
			}
		}
		for _, claimPath := range qualified {
			for slot := 0; slot < valid.Limit; slot++ {
				slotPath := filepath.Join(valid.Directory, "claims", fmt.Sprintf("%06d.json", slot))
				f, err := os.OpenFile(slotPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
				if errors.Is(err, fs.ErrExist) {
					continue
				}
				if err != nil {
					rollback()
					return err
				}
				created = append(created, slotPath)
				claim := newObject()
				claim.set("version", 1.0)
				claim.set("claimId", newUUID())
				claim.set("path", claimPath)
				claim.set("claimedAt", float64(time.Now().UnixMilli()))
				_, werr := f.WriteString(marshalJSON(claim, "") + "\n")
				f.Close()
				if werr != nil {
					rollback()
					return werr
				}
				break
			}
		}
		snap, err := getRunFanoutBudgetSnapshot(valid)
		if err == nil {
			err = commit(snap)
		}
		if err != nil {
			rollback()
		}
		return err
	})
}

// claimRunFanoutBatch claims the paths (all or none) and returns the snapshot after.
func claimRunFanoutBatch(d runFanoutBudgetDescriptor, paths []string) (snap runFanoutBudgetSnapshot, err error) {
	err = commitRunFanoutBatch(d, paths, func(s runFanoutBudgetSnapshot) error { snap = s; return nil })
	return
}

// claimRunFanoutBatchWithCommit claims the paths, then runs commit; if it fails the claims are released and its error returned.
func claimRunFanoutBatchWithCommit(d runFanoutBudgetDescriptor, paths []string, commit func() error) error {
	return commitRunFanoutBatch(d, paths, func(runFanoutBudgetSnapshot) error { return commit() })
}
