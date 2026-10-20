package pi_subagents

// The per-session cap on active background runs: one slot directory per active run, owned by a reservation token, released
// only with proof that the run's processes ended. Ports active-async-capacity.ts and the process evidence part of
// workflow-terminal-proof.ts.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

const (
	defaultAbandonedSlotReleaseAfterMs = 20 * 60 * 1000
	minAbandonedSlotReleaseAfterMs     = 5 * 60 * 1000
	maxAbandonedSlotReleaseAfterMs     = 24 * 60 * 60 * 1000
)

// ActiveAsyncCapacitySnapshot is used slots against the limit (0: no limit).
type ActiveAsyncCapacitySnapshot struct{ Used, Limit int }

// ActiveAsyncCapacityOwner is the owner.json of a slot.
type ActiveAsyncCapacityOwner struct {
	Version                 int    `json:"version"`
	ReservationToken        string `json:"reservationToken"`
	OwnerSessionID          string `json:"ownerSessionId"`
	OwnerSessionKey         string `json:"ownerSessionKey"`
	Slot                    int    `json:"slot"`
	RunID                   string `json:"runId"`
	SourceRunID             string `json:"sourceRunId,omitempty"`
	Generation              int    `json:"generation"`
	Kind                    string `json:"kind"` // runner or workflow
	AsyncDir                string `json:"asyncDir"`
	ReservedAt              ms     `json:"reservedAt"`
	RunnerProcessInstanceID string `json:"runnerProcessInstanceId,omitempty"`
	RunnerStartedAt         *ms    `json:"runnerStartedAt,omitempty"`
}

// ActiveAsyncCapacityError is the refusal when the cap is reached.
type ActiveAsyncCapacityError struct{ Snapshot ActiveAsyncCapacitySnapshot }

// capacityRefusalPrefix starts every ActiveAsyncCapacityError message (the launch turns the error into a tool error, text only).
const capacityRefusalPrefix = "Active async run capacity exhausted: "

func (e *ActiveAsyncCapacityError) Error() string {
	return fmt.Sprintf(capacityRefusalPrefix+"%d/%d used.", e.Snapshot.Used, e.Snapshot.Limit)
}

// CapacityOptions are the seams of the capacity module.
type CapacityOptions struct {
	RootDir                     string
	Now                         func() int64
	Token                       func() string
	AbandonedSlotReleaseAfterMs any // false, or an int64 (unset: the default)
	PidLiveness                 func(pid int) PidLiveness
	AfterSlotRename             func(releasedDir string)
	WriteOwner                  func(path string, owner *ActiveAsyncCapacityOwner) error
	LiveWorkflowRunIDs          map[string]bool
}

func (o CapacityOptions) root() string {
	if o.RootDir != "" {
		return o.RootDir
	}
	return filepath.Join(tempRootDir(), "session-active-async-capacity")
}

func (o CapacityOptions) now() int64 {
	if o.Now != nil {
		return o.Now()
	}
	return nowMs()
}

// ActiveAsyncCapacityRelease says whether a slot may be released and why.
type ActiveAsyncCapacityRelease struct {
	State    string // releasable, retained, not-owned
	Reason   string
	Evidence *abandonedEvidence
}

type abandonedEvidence struct {
	ReleasedBy                  string `json:"releasedBy"`
	ProcessProof                string `json:"processProof"`
	RunnerPid                   string `json:"runnerPid"`
	LastActivityAgeMs           int64  `json:"lastActivityAgeMs"`
	AbandonedSlotReleaseAfterMs int64  `json:"abandonedSlotReleaseAfterMs"`
}

// ActiveAsyncCapacityInspection is what a run's slot says about it.
type ActiveAsyncCapacityInspection struct {
	Owner    *ActiveAsyncCapacityOwner
	Relation string // current, source, none
	SlotDir  string
	Release  ActiveAsyncCapacityRelease
}

func resolveMaxActiveAsyncRunsPerSession(v any) *int {
	n, ok := isInteger(v)
	if !ok || n < 0 || n == 0 {
		return nil
	}
	return &n
}

// resolveAbandonedSlotReleaseAfterMs: false stays false; anything but a positive integer is the default.
func resolveAbandonedSlotReleaseAfterMs(v any) (ms int64, disabled bool) {
	if b, ok := v.(bool); ok && !b {
		return 0, true
	}
	if n, ok := isInteger(v); ok && n >= 1 {
		return int64(n), false
	}
	if n, ok := v.(int64); ok && n >= 1 {
		return n, false
	}
	return defaultAbandonedSlotReleaseAfterMs, false
}

func activeAsyncCapacitySessionKey(sessionID string) string {
	sum := sha256.Sum256([]byte(sessionID))
	return hex.EncodeToString(sum[:])
}

func capacitySessionDir(sessionID, root string) string {
	return filepath.Join(root, activeAsyncCapacitySessionKey(sessionID))
}

func capacitySlotDir(pool string, slot int) string {
	return filepath.Join(pool, fmt.Sprintf("slot-%d", slot))
}

var slotNamePattern = regexp.MustCompile(`^slot-\d+$`)

func readCapacityOwner(dir string) *ActiveAsyncCapacityOwner {
	data, err := os.ReadFile(filepath.Join(dir, "owner.json"))
	if err != nil {
		return nil
	}
	var raw map[string]any
	var o ActiveAsyncCapacityOwner
	if json.Unmarshal(data, &raw) != nil || json.Unmarshal(data, &o) != nil {
		return nil
	}
	_, hasReserved := raw["reservedAt"].(float64)
	if o.Version != 1 || o.ReservationToken == "" || o.OwnerSessionID == "" || o.OwnerSessionKey == "" || o.Slot < 0 || o.RunID == "" || o.Generation < 0 ||
		(o.Kind != "runner" && o.Kind != "workflow") || o.AsyncDir == "" || !hasReserved {
		return nil
	}
	if v, has := raw["runnerProcessInstanceId"]; has && (v == nil || v == "") {
		return nil
	}
	return &o
}

func occupiedSlots(pool string) []string {
	entries, err := os.ReadDir(pool)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && slotNamePattern.MatchString(e.Name()) {
			out = append(out, filepath.Join(pool, e.Name()))
		}
	}
	return out
}

func capacitySnapshotFor(sessionID string, limit *int, root string) ActiveAsyncCapacitySnapshot {
	s := ActiveAsyncCapacitySnapshot{Used: len(occupiedSlots(capacitySessionDir(sessionID, root)))}
	if limit != nil {
		s.Limit = *limit
	}
	return s
}

func matchingCapacityOwner(dir string, expected *ActiveAsyncCapacityOwner) *ActiveAsyncCapacityOwner {
	o := readCapacityOwner(dir)
	if o != nil && o.ReservationToken == expected.ReservationToken && o.RunID == expected.RunID && o.Generation == expected.Generation {
		return o
	}
	return nil
}

// withSlotClaim runs op while holding the slot's claim file; acquired is false when another holder has it.
func withSlotClaim[T any](dir string, op func() T) (value T, acquired bool, err error) {
	claimPath := filepath.Join(dir, "capacity.claim")
	token := randomUUID()
	f, err := os.OpenFile(claimPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) || isNotExist(err) {
			return value, false, nil
		}
		return value, false, err
	}
	_, _ = f.WriteString(token)
	_ = f.Close()
	defer func() {
		if b, rerr := os.ReadFile(claimPath); rerr == nil && string(b) == token {
			_ = os.Remove(claimPath)
		}
	}()
	return op(), true, nil
}

func removeOwnedSlot(dir string, expected *ActiveAsyncCapacityOwner, o CapacityOptions, requireUnstarted bool, release *ActiveAsyncCapacityRelease) bool {
	if requireUnstarted && (expected.RunnerProcessInstanceID != "" || expected.RunnerStartedAt != nil) {
		return false
	}
	ok, acquired, _ := withSlotClaim(dir, func() bool {
		cur := matchingCapacityOwner(dir, expected)
		if cur == nil || (requireUnstarted && (cur.RunnerProcessInstanceID != "" || cur.RunnerStartedAt != nil)) {
			return false
		}
		released := filepath.Join(filepath.Dir(dir), fmt.Sprintf(".%s.released-%s", filepath.Base(dir), randomUUID()))
		if err := os.Rename(dir, released); err != nil {
			return false
		}
		if o.AfterSlotRename != nil {
			o.AfterSlotRename(released)
		}
		if release != nil && release.State == "releasable" && release.Evidence != nil {
			ev := map[string]any{"type": "subagent.capacity.released", "ts": o.now(), "runId": expected.RunID, "sessionId": expected.OwnerSessionID,
				"releasedBy": release.Evidence.ReleasedBy, "processProof": release.Evidence.ProcessProof, "runnerPid": release.Evidence.RunnerPid,
				"lastActivityAgeMs": release.Evidence.LastActivityAgeMs, "abandonedSlotReleaseAfterMs": release.Evidence.AbandonedSlotReleaseAfterMs}
			appendJSONLine(filepath.Join(expected.AsyncDir, "events.jsonl"), ev)
		}
		_ = os.RemoveAll(released)
		return true
	})
	return acquired && ok
}

// capacityTerminalState: the capacity module's terminal states exclude paused (a paused run may still be revived).
func capacityTerminalState(state string) bool {
	return state != "queued" && state != "running" && state != "paused"
}

func runnerReleaseVerdict(owner *ActiveAsyncCapacityOwner, st *AsyncStatus, o CapacityOptions) ActiveAsyncCapacityRelease {
	retained := func(f string, a ...any) ActiveAsyncCapacityRelease {
		return ActiveAsyncCapacityRelease{State: "retained", Reason: fmt.Sprintf(f, a...)}
	}
	switch {
	case st == nil:
		return retained("status file is missing or unreadable")
	case owner.RunnerProcessInstanceID == "":
		return retained("runner process identity has not been recorded")
	case st.SessionID != owner.OwnerSessionID:
		return retained("status session %s does not match owner session %s", firstNonEmpty(st.SessionID, "unknown"), owner.OwnerSessionID)
	case st.RunID != owner.RunID:
		return retained("status run %s does not match owner run %s", st.RunID, owner.RunID)
	case !capacityTerminalState(st.State):
		return retained("run is still %s", st.State)
	}
	if pt := st.ProcessTerminal; pt != nil && pt.State == "not-started" && pt.RunID == owner.RunID && pt.RunnerProcessInstanceID == owner.RunnerProcessInstanceID && st.Error != "" {
		return ActiveAsyncCapacityRelease{State: "releasable", Reason: "run failed before child startup completed"}
	}
	proof := readProcessTerminal(owner.AsyncDir, owner.RunID, owner.RunnerProcessInstanceID)
	if proof != nil && proof.State == "observed" && proof.RunID == owner.RunID && proof.RunnerProcessInstanceID == owner.RunnerProcessInstanceID {
		return ActiveAsyncCapacityRelease{State: "releasable", Reason: "matching observed process-terminal proof is present"}
	}
	state := "missing"
	if proof != nil {
		state = proof.State
	}
	return abandonedRunnerReleaseVerdict(st, state, o)
}

func abandonedRunnerReleaseVerdict(st *AsyncStatus, proofState string, o CapacityOptions) ActiveAsyncCapacityRelease {
	proofReason := "process-terminal proof is " + proofState
	retained := func(f string, a ...any) ActiveAsyncCapacityRelease {
		return ActiveAsyncCapacityRelease{State: "retained", Reason: proofReason + "; " + fmt.Sprintf(f, a...)}
	}
	threshold, disabled := resolveAbandonedSlotReleaseAfterMs(o.AbandonedSlotReleaseAfterMs)
	switch {
	case disabled:
		return retained("abandoned-timeout policy is disabled")
	case st.State != "failed":
		return retained("abandoned-timeout policy requires a failed run, not %s", st.State)
	case st.PID == nil || *st.PID < 1:
		return retained("runner PID is missing or invalid")
	}
	liveness := o.PidLiveness
	if liveness == nil {
		liveness = func(pid int) PidLiveness { return checkPidLiveness(pid, nil) }
	}
	if l := liveness(*st.PID); l != pidDead {
		return retained("runner PID liveness is %s", l)
	}
	var last *ms
	for _, c := range []*ms{st.LastActivityAt, st.LastUpdate, st.EndedAt} {
		if c != nil {
			last = c
			break
		}
	}
	if last == nil {
		return retained("last activity timestamp is missing or invalid")
	}
	age := max(0, o.now()-int64(*last))
	if age <= threshold {
		return retained("last activity age %dms has not exceeded abandoned-timeout %dms", age, threshold)
	}
	ev := &abandonedEvidence{ReleasedBy: "abandoned-timeout", ProcessProof: "unknown", RunnerPid: "gone", LastActivityAgeMs: age, AbandonedSlotReleaseAfterMs: threshold}
	return ActiveAsyncCapacityRelease{State: "releasable", Evidence: ev,
		Reason: fmt.Sprintf("%s: %s; runner PID is gone; process proof unknown; last activity age %dms exceeds %dms", ev.ReleasedBy, proofReason, age, threshold)}
}

// WorkflowChildProcessEvidence is the process evidence of a workflow's async children.
type WorkflowChildProcessEvidence struct {
	State    string // observed, pending, unknown
	Reason   string
	Children []*ProcessTerminal
}

// readWorkflowChildProcessEvidence: status proofs and capacity release both use it so they cannot disagree about a workflow.
func readWorkflowChildProcessEvidence(workflowAsyncDir string, steps []AsyncStep) WorkflowChildProcessEvidence {
	if steps == nil {
		return WorkflowChildProcessEvidence{State: "unknown", Reason: "workflow child roster is missing"}
	}
	var children []*ProcessTerminal
	for _, step := range steps {
		label := firstNonEmpty(step.WorkflowKey, step.Agent)
		if step.Async == nil {
			return WorkflowChildProcessEvidence{State: "unknown", Reason: fmt.Sprintf("workflow child %s is missing async classification", label)}
		}
		if !*step.Async {
			continue
		}
		if step.RunID == "" || filepath.Base(step.RunID) != step.RunID {
			return WorkflowChildProcessEvidence{State: "unknown", Reason: fmt.Sprintf("async workflow child %s is missing run id", label)}
		}
		childDir := filepath.Join(filepath.Dir(workflowAsyncDir), step.RunID)
		if _, err := os.Stat(childDir); err != nil {
			return WorkflowChildProcessEvidence{State: "unknown", Reason: fmt.Sprintf("async workflow child %s directory is missing", label)}
		}
		cs, _ := readStatusQuiet(childDir)
		if cs == nil {
			return WorkflowChildProcessEvidence{State: "unknown", Reason: fmt.Sprintf("async workflow child %s status is missing or unreadable", label)}
		}
		if !capacityTerminalState(cs.State) {
			return WorkflowChildProcessEvidence{State: "pending", Reason: fmt.Sprintf("async workflow child %s is still %s", label, cs.State)}
		}
		rec := cs.ProcessTerminal
		if rec == nil || rec.RunnerProcessInstanceID == "" {
			return WorkflowChildProcessEvidence{State: "unknown", Reason: fmt.Sprintf("async workflow child %s has no runner process identity", label)}
		}
		// a runner startup failure records `not-started` in status and never writes a process-terminal sidecar
		if rec.State == "not-started" && rec.RunID == step.RunID && cs.Error != "" {
			children = append(children, rec)
			continue
		}
		proof := readProcessTerminal(childDir, step.RunID, rec.RunnerProcessInstanceID)
		if proof == nil || proof.State != "observed" || proof.RunID != step.RunID {
			state, ps := "unknown", "missing"
			if proof == nil || proof.State == "pending" {
				state = "pending"
			}
			if proof != nil {
				ps = proof.State
			}
			return WorkflowChildProcessEvidence{State: state, Reason: fmt.Sprintf("async workflow child %s process-terminal proof is %s", label, ps)}
		}
		children = append(children, proof)
	}
	return WorkflowChildProcessEvidence{State: "observed", Children: children}
}

func workflowReleaseVerdict(owner *ActiveAsyncCapacityOwner, st *AsyncStatus, live map[string]bool) ActiveAsyncCapacityRelease {
	retained := func(f string, a ...any) ActiveAsyncCapacityRelease {
		return ActiveAsyncCapacityRelease{State: "retained", Reason: fmt.Sprintf(f, a...)}
	}
	switch {
	case st == nil:
		return retained("status file is missing or unreadable")
	case st.SessionID != owner.OwnerSessionID:
		return retained("status session %s does not match owner session %s", firstNonEmpty(st.SessionID, "unknown"), owner.OwnerSessionID)
	case st.RunID != owner.RunID:
		return retained("status run %s does not match owner run %s", st.RunID, owner.RunID)
	case st.Mode != "workflow":
		return retained("status mode is %s, not workflow", st.Mode)
	case !capacityTerminalState(st.State):
		return retained("workflow is still %s", st.State)
	case live[owner.RunID]:
		return retained("workflow controller is still live")
	}
	if ev := readWorkflowChildProcessEvidence(owner.AsyncDir, st.Steps); ev.State != "observed" {
		return ActiveAsyncCapacityRelease{State: "retained", Reason: ev.Reason}
	}
	return ActiveAsyncCapacityRelease{State: "releasable", Reason: "workflow is terminal, controller is gone, and async children have observed proof"}
}

func ownerReleaseVerdict(owner *ActiveAsyncCapacityOwner, o CapacityOptions) ActiveAsyncCapacityRelease {
	st, _ := readStatusQuiet(owner.AsyncDir)
	if owner.Kind == "runner" {
		return runnerReleaseVerdict(owner, st, o)
	}
	return workflowReleaseVerdict(owner, st, o.LiveWorkflowRunIDs)
}

func capacitySessionDirs(root, sessionID string) []string {
	if sessionID != "" {
		return []string{capacitySessionDir(sessionID, root)}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, filepath.Join(root, e.Name()))
		}
	}
	return out
}

// inspectActiveAsyncCapacityOwner says which slot records a run and whether it may be released.
func inspectActiveAsyncCapacityOwner(runID, sessionID, asyncDir string, o CapacityOptions) ActiveAsyncCapacityInspection {
	for _, pool := range capacitySessionDirs(o.root(), sessionID) {
		for _, dir := range occupiedSlots(pool) {
			owner := readCapacityOwner(dir)
			if owner == nil {
				continue
			}
			a1, _ := filepath.Abs(owner.AsyncDir)
			a2, _ := filepath.Abs(asyncDir)
			sameRun := owner.RunID == runID || (asyncDir != "" && a1 == a2)
			sourceRun := owner.SourceRunID == runID
			if !sameRun && !sourceRun {
				continue
			}
			if sourceRun && !sameRun {
				return ActiveAsyncCapacityInspection{Owner: owner, Relation: "source", SlotDir: dir, Release: ActiveAsyncCapacityRelease{State: "not-owned", Reason: "slot was transferred to " + owner.RunID}}
			}
			return ActiveAsyncCapacityInspection{Owner: owner, Relation: "current", SlotDir: dir, Release: ownerReleaseVerdict(owner, o)}
		}
	}
	return ActiveAsyncCapacityInspection{Relation: "none", Release: ActiveAsyncCapacityRelease{State: "not-owned", Reason: "no active-capacity slot records this run"}}
}

// reconcileActiveAsyncCapacity releases the slots whose runs ended with proof, and reports the usage.
func reconcileActiveAsyncCapacity(sessionID string, limit *int, o CapacityOptions) ActiveAsyncCapacitySnapshot {
	root := o.root()
	for _, dir := range occupiedSlots(capacitySessionDir(sessionID, root)) {
		owner := readCapacityOwner(dir)
		if owner == nil || owner.OwnerSessionID != sessionID || owner.OwnerSessionKey != activeAsyncCapacitySessionKey(sessionID) || filepath.Base(dir) != fmt.Sprintf("slot-%d", owner.Slot) {
			continue
		}
		release := ownerReleaseVerdict(owner, o)
		if release.State != "releasable" {
			continue
		}
		removeOwnedSlot(dir, owner, o, false, &release)
	}
	return capacitySnapshotFor(sessionID, limit, root)
}

func getActiveAsyncCapacitySnapshot(sessionID string, limit *int, o CapacityOptions) ActiveAsyncCapacitySnapshot {
	return reconcileActiveAsyncCapacity(sessionID, limit, o)
}

func createSlot(pool string, owner *ActiveAsyncCapacityOwner) (bool, error) {
	dest := capacitySlotDir(pool, owner.Slot)
	if err := os.MkdirAll(pool, 0o700); err != nil {
		return false, err
	}
	if err := os.Mkdir(dest, 0o700); err != nil {
		if os.IsExist(err) {
			return false, nil
		}
		return false, err
	}
	// if the owner cannot be written the occupied directory stays: it fails closed instead of becoming free
	return true, asyncWritePrivateJSON(filepath.Join(dest, "owner.json"), owner)
}

// ActiveAsyncCapacityHandle is a reserved slot.
type ActiveAsyncCapacityHandle struct {
	Owner         *ActiveAsyncCapacityOwner
	dir           string
	limit         int
	opts          CapacityOptions
	rollbackOwner *ActiveAsyncCapacityOwner
}

func newCapacityHandle(owner *ActiveAsyncCapacityOwner, limit int, o CapacityOptions, rollbackOwner *ActiveAsyncCapacityOwner) *ActiveAsyncCapacityHandle {
	return &ActiveAsyncCapacityHandle{Owner: owner, dir: capacitySlotDir(capacitySessionDir(owner.OwnerSessionID, o.root()), owner.Slot), limit: limit, opts: o, rollbackOwner: rollbackOwner}
}

func (h *ActiveAsyncCapacityHandle) writeOwner(p string, o *ActiveAsyncCapacityOwner) error {
	if h.opts.WriteOwner != nil {
		return h.opts.WriteOwner(p, o)
	}
	return asyncWritePrivateJSON(p, o)
}

// MarkStarted binds the slot to the runner process.
func (h *ActiveAsyncCapacityHandle) MarkStarted(runnerID string) error {
	var werr error
	ok, acquired, _ := withSlotClaim(h.dir, func() bool {
		cur := matchingCapacityOwner(h.dir, h.Owner)
		if cur == nil {
			return false
		}
		next := *cur
		next.RunnerProcessInstanceID = runnerID
		next.RunnerStartedAt = msPtrOf(h.opts.now())
		// memory first, so pre-proceed cleanup can tell a failed durable bind from an unrelated unstarted reservation
		*h.Owner = next
		werr = h.writeOwner(filepath.Join(h.dir, "owner.json"), &next)
		return werr == nil
	})
	if werr != nil {
		return werr
	}
	if !acquired || !ok {
		return fmt.Errorf("Active async capacity ownership changed for run '%s'.", h.Owner.RunID)
	}
	return nil
}

// MarkWorkflowStarted records that a workflow owner started.
func (h *ActiveAsyncCapacityHandle) MarkWorkflowStarted() error {
	ok, acquired, _ := withSlotClaim(h.dir, func() bool {
		cur := matchingCapacityOwner(h.dir, h.Owner)
		if cur == nil || cur.Kind != "workflow" {
			return false
		}
		next := *cur
		next.RunnerStartedAt = msPtrOf(h.opts.now())
		*h.Owner = next
		_ = asyncWritePrivateJSON(filepath.Join(h.dir, "owner.json"), &next)
		return true
	})
	if !acquired || !ok {
		return fmt.Errorf("Active async capacity ownership changed for workflow '%s'.", h.Owner.RunID)
	}
	return nil
}

// Rollback frees an unstarted slot (or restores the source of a transfer); false when the runner already started.
func (h *ActiveAsyncCapacityHandle) Rollback() bool {
	if h.rollbackOwner == nil {
		return removeOwnedSlot(h.dir, h.Owner, h.opts, true, nil)
	}
	if h.Owner.RunnerProcessInstanceID != "" || h.Owner.RunnerStartedAt != nil {
		return false
	}
	ok, acquired, _ := withSlotClaim(h.dir, func() bool {
		cur := matchingCapacityOwner(h.dir, h.Owner)
		if cur == nil || cur.RunnerProcessInstanceID != "" || cur.RunnerStartedAt != nil {
			return false
		}
		_ = asyncWritePrivateJSON(filepath.Join(h.dir, "owner.json"), h.rollbackOwner)
		*h.Owner = *h.rollbackOwner
		return true
	})
	return acquired && ok
}

// RollbackBeforeRunnerProceed undoes the binding of a runner that was terminated before it proceeded.
func (h *ActiveAsyncCapacityHandle) RollbackBeforeRunnerProceed(runnerID string) bool {
	ok, acquired, _ := withSlotClaim(h.dir, func() bool {
		cur := matchingCapacityOwner(h.dir, h.Owner)
		if cur == nil {
			return false
		}
		bound := cur.RunnerProcessInstanceID == runnerID
		bindFailed := cur.RunnerProcessInstanceID == "" && cur.RunnerStartedAt == nil && h.Owner.RunnerProcessInstanceID == runnerID
		if !bound && !bindFailed {
			return false
		}
		if h.rollbackOwner != nil {
			_ = asyncWritePrivateJSON(filepath.Join(h.dir, "owner.json"), h.rollbackOwner)
			*h.Owner = *h.rollbackOwner
			return true
		}
		released := filepath.Join(filepath.Dir(h.dir), fmt.Sprintf(".%s.released-%s", filepath.Base(h.dir), randomUUID()))
		if err := os.Rename(h.dir, released); err != nil {
			return false
		}
		if h.opts.AfterSlotRename != nil {
			h.opts.AfterSlotRename(released)
		}
		_ = os.RemoveAll(released)
		return true
	})
	return acquired && ok
}

// Reconcile releases what can be released and reports the usage.
func (h *ActiveAsyncCapacityHandle) Reconcile(live map[string]bool) ActiveAsyncCapacitySnapshot {
	o := h.opts
	o.LiveWorkflowRunIDs = live
	lim := h.limit
	return reconcileActiveAsyncCapacity(h.Owner.OwnerSessionID, &lim, o)
}

// acquireActiveAsyncCapacity reserves a slot; nil (no error) when the limit is unset.
func acquireActiveAsyncCapacity(sessionID string, limit *int, runID, kind, asyncDir string, o CapacityOptions) (*ActiveAsyncCapacityHandle, error) {
	if limit == nil {
		return nil, nil
	}
	reconciled := reconcileActiveAsyncCapacity(sessionID, limit, o)
	if reconciled.Used >= *limit {
		return nil, &ActiveAsyncCapacityError{reconciled}
	}
	pool := capacitySessionDir(sessionID, o.root())
	token := randomUUID()
	if o.Token != nil {
		token = o.Token()
	}
	for slot := range *limit {
		owner := &ActiveAsyncCapacityOwner{Version: 1, ReservationToken: token, OwnerSessionID: sessionID, OwnerSessionKey: activeAsyncCapacitySessionKey(sessionID), Slot: slot, RunID: runID,
			Generation: 0, Kind: kind, AsyncDir: asyncDir, ReservedAt: ms(o.now())}
		created, err := createSlot(pool, owner)
		if err != nil {
			return nil, err
		}
		if created {
			return newCapacityHandle(owner, *limit, o, nil), nil
		}
	}
	return nil, &ActiveAsyncCapacityError{capacitySnapshotFor(sessionID, limit, o.root())}
}

// transferActiveAsyncCapacity hands the slot of a finished run to the run that revives it.
func transferActiveAsyncCapacity(sessionID string, limit *int, sourceRunID, runID, asyncDir string, o CapacityOptions) (*ActiveAsyncCapacityHandle, error) {
	lim := 0
	if limit != nil {
		lim = *limit
	}
	pool := capacitySessionDir(sessionID, o.root())
	for _, dir := range occupiedSlots(pool) {
		source := readCapacityOwner(dir)
		if source == nil || source.OwnerSessionID != sessionID || source.RunID != sourceRunID {
			continue
		}
		var terr error
		h, acquired, _ := withSlotClaim(dir, func() *ActiveAsyncCapacityHandle {
			cur := matchingCapacityOwner(dir, source)
			st, _ := readStatusQuiet(source.AsyncDir)
			if cur == nil || st == nil || st.RunID != sourceRunID || st.State == "queued" || st.State == "running" {
				terr = fmt.Errorf("Active async capacity source '%s' is not transferable.", sourceRunID)
				return nil
			}
			next := *cur
			next.RunID, next.SourceRunID, next.Generation, next.Kind, next.AsyncDir, next.ReservedAt = runID, sourceRunID, cur.Generation+1, "runner", asyncDir, ms(o.now())
			next.RunnerProcessInstanceID, next.RunnerStartedAt = "", nil
			if err := asyncWritePrivateJSON(filepath.Join(dir, "owner.json"), &next); err != nil {
				terr = err
				return nil
			}
			return newCapacityHandle(&next, lim, o, cur)
		})
		if terr != nil {
			return nil, terr
		}
		if !acquired {
			return nil, fmt.Errorf("Active async capacity transfer is already in progress for run '%s'.", sourceRunID)
		}
		return h, nil
	}
	return acquireActiveAsyncCapacity(sessionID, limit, runID, "runner", asyncDir, o)
}
