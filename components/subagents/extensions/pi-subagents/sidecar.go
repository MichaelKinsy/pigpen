package pi_subagents

// The fleet's sidecar: a session's run records are written to `<sessionDir>/<sessionId>.subagents.json`, so a resumed
// session shows the fleet it had — the fleet inspector, the `subagent-async` widget and the status and control actions
// all work on the restored runs, the way the todo extension re-attaches its tasks from the session. Every run-state
// transition rewrites the file, and session_shutdown takes one last snapshot before stopAll kills the children: a run
// that had not finished is recorded paused, which is what it is once the process that ran it is gone.
//
// What is not written: the child's environment (this process's environment, secrets included) — a restore rebuilds it
// as the launch did (childEnv) — and steer messages queued for a turn that never ran. What is written is what the
// views read and what `resume` continues a child session with: the ids, the states, the session files and the child's
// request (program, arguments, stdin, cwd).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// sidecarVersion is the file's format version: a reader refuses a file written by a later format than it knows.
const sidecarVersion = 1

// sidecarPath is a session's sidecar in its session directory; "" when either half is missing — an in-memory session
// has no file to sit beside, so it persists nothing.
func sidecarPath(dir, sessionID string) string {
	if dir == "" || sessionID == "" {
		return ""
	}
	return filepath.Join(dir, sessionID+".subagents.json")
}

// sidecarFile is the file: one session's fleet.
type sidecarFile struct {
	Version int          `json:"version"`
	Session string       `json:"session"`
	Next    int          `json:"next"`
	Runs    []sidecarRun `json:"runs"`
}

// sidecarRun is one run's persisted record: the fields the fleet views and the control actions read, without the
// control channel (a restored run is not live) and without the environment.
type sidecarRun struct {
	ID          string        `json:"id"`
	Mode        string        `json:"mode"`
	Name        string        `json:"name"`
	Task        string        `json:"task"`
	Cwd         string        `json:"cwd"`
	StartedAt   *time.Time    `json:"startedAt,omitempty"`
	EndedAt     *time.Time    `json:"endedAt,omitempty"`
	ResumedFrom string        `json:"resumedFrom,omitempty"`
	TurnNotice  string        `json:"turnNotice,omitempty"`
	SessionDir  string        `json:"sessionDir,omitempty"`
	SessionID   string        `json:"sessionId,omitempty"`
	Child       sidecarChild  `json:"child"`
	Steps       []sidecarStep `json:"steps"`
}

// sidecarChild is the child's request (run.go's childRequest) without OnOutput, OnSession (rebound by the next step)
// and Env (never written; rebuilt by the restore).
type sidecarChild struct {
	Bin   string   `json:"bin,omitempty"`
	Args  []string `json:"args,omitempty"`
	Stdin string   `json:"stdin,omitempty"`
	Cwd   string   `json:"cwd,omitempty"`
}

// sidecarStep is one child step: its identity, its state and what the transcript views read.
type sidecarStep struct {
	Agent        string     `json:"agent"`
	Task         string     `json:"task,omitempty"`
	Thinking     string     `json:"thinking,omitempty"`
	State        string     `json:"state"`
	StartedAt    *time.Time `json:"startedAt,omitempty"`
	EndedAt      *time.Time `json:"endedAt,omitempty"`
	LastActivity *time.Time `json:"lastActivity,omitempty"`
	Output       string     `json:"output,omitempty"`
	Err          string     `json:"error,omitempty"`
	Exit         int        `json:"exit,omitempty"`
	Session      string     `json:"session,omitempty"`
	SessionDir   string     `json:"sessionDir,omitempty"`
	SessionID    string     `json:"sessionId,omitempty"`
	NoticeKind   string     `json:"noticeKind,omitempty"`
	NoticeInfo   string     `json:"noticeInfo,omitempty"`
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func timeVal(p *time.Time) time.Time {
	if p == nil {
		return time.Time{}
	}
	return *p
}

// runNumberOf is the counter in a run's id ("run-7" is 7); false for an id this format did not write.
func runNumberOf(id string) (int, bool) {
	rest, ok := strings.CutPrefix(id, "run-")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// writeSidecar replaces the file atomically (a temp file beside it, then rename): a reader never sees a half file.
// The write is best-effort — the in-memory fleet stays correct if the disk refuses — and errors reach the caller for
// tests to assert on.
func writeSidecar(path string, file sidecarFile) error {
	if path == "" {
		return nil
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// sidecarLocked builds the file's content from the registry (fleet.mu held). liveToPaused records every queued or
// running step paused, ending now: the snapshot the session's shutdown takes before stopAll cancels the children.
func (f *fleetState) sidecarLocked(liveToPaused bool, now time.Time) sidecarFile {
	file := sidecarFile{
		Version: sidecarVersion,
		Session: f.persistSession,
		Next:    f.next,
		Runs:    make([]sidecarRun, 0, len(f.runs)),
	}
	for _, r := range f.runs {
		sr := sidecarRun{
			ID:          r.id,
			Mode:        r.mode,
			Name:        r.name,
			Task:        r.task,
			Cwd:         r.cwd,
			StartedAt:   timePtr(r.startedAt),
			EndedAt:     timePtr(r.endedAt),
			ResumedFrom: r.resumedFrom,
			TurnNotice:  r.turnNotice,
			SessionDir:  r.sessionDir,
			SessionID:   r.sessionID,
			Child: sidecarChild{
				Bin:   r.childReq.Bin,
				Args:  r.childReq.Args,
				Stdin: r.childReq.Stdin,
				Cwd:   r.childReq.Cwd,
			},
			Steps: make([]sidecarStep, 0, len(r.steps)),
		}
		for _, s := range r.steps {
			ss := sidecarStep{
				Agent:        s.agent,
				Task:         s.task,
				Thinking:     s.thinking,
				State:        string(s.state),
				StartedAt:    timePtr(s.startedAt),
				EndedAt:      timePtr(s.endedAt),
				LastActivity: timePtr(s.lastActivity),
				Output:       s.output,
				Err:          s.err,
				Exit:         s.exit,
				Session:      s.session,
				SessionDir:   s.sessionDir,
				SessionID:    s.sessionID,
				NoticeKind:   s.noticeKind,
				NoticeInfo:   s.noticeInfo,
			}
			if liveToPaused && (s.state == stateQueued || s.state == stateRunning) {
				ss.State = string(statePaused)
				if s.endedAt.IsZero() {
					ss.EndedAt = timePtr(now)
				}
			}
			sr.Steps = append(sr.Steps, ss)
		}
		file.Runs = append(file.Runs, sr)
	}
	return file
}

// persist writes the registry's bound sidecar with the runs as they are. Callers must not hold fleet.mu: persistMu
// serializes writers, the snapshot takes fleet.mu for the copy, and the write happens outside both.
func (f *fleetState) persist() {
	f.persistMu.Lock()
	defer f.persistMu.Unlock()
	f.mu.Lock()
	if !f.persisting {
		f.mu.Unlock()
		return
	}
	file, path := f.sidecarLocked(false, fleetNow()), f.persistPath
	f.mu.Unlock()
	_ = writeSidecar(path, file)
}

// bindPersist points every later write at a session's sidecar; an empty directory or id unbinds, so an in-memory
// session persists nothing.
func (f *fleetState) bindPersist(dir, sessionID string) {
	f.persistMu.Lock()
	defer f.persistMu.Unlock()
	f.mu.Lock()
	f.persistPath, f.persistSession = sidecarPath(dir, sessionID), sessionID
	f.persisting = f.persistPath != ""
	f.mu.Unlock()
}

// finalizePersist is the session's last write: the snapshot stopAll would otherwise overwrite, with every queued or
// running step recorded paused — the session is ending and its child will not finish. It stops further writes, so
// the cancellations stopAll triggers cannot rewrite the snapshot as `stopped`.
func (f *fleetState) finalizePersist() {
	f.persistMu.Lock()
	defer f.persistMu.Unlock()
	f.mu.Lock()
	if !f.persisting {
		f.mu.Unlock()
		return
	}
	file, path := f.sidecarLocked(true, fleetNow()), f.persistPath
	f.persisting = false
	f.mu.Unlock()
	_ = writeSidecar(path, file)
}

// restore fills the (just reset) registry from a session's sidecar and reports whether it held runs. A queued or
// running step comes back paused: its child died with the process that ran it, so it did not complete. Runs keep the
// ids the session's transcript names them by, and the next id continues past the restored ones.
func (f *fleetState) restore(dir, sessionID string) bool {
	path := sidecarPath(dir, sessionID)
	if path == "" {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var file sidecarFile
	if err := json.Unmarshal(data, &file); err != nil {
		return false // a corrupt or foreign file is no file: the session starts with an empty fleet
	}
	if file.Version != sidecarVersion || file.Session != sessionID {
		return false
	}
	runs := make([]*runRecord, 0, len(file.Runs))
	next := file.Next
	for _, sr := range file.Runs {
		if n, ok := runNumberOf(sr.ID); ok && n+1 > next {
			next = n + 1
		}
		if r := runFromSidecar(sr, fleetNow()); r != nil {
			runs = append(runs, r)
		}
	}
	if len(runs) > maxFleetRuns {
		runs = runs[len(runs)-maxFleetRuns:]
	}
	if next < 1 {
		next = 1
	}
	f.mu.Lock()
	f.runs, f.next = runs, next
	f.mu.Unlock()
	f.changed()
	return len(runs) > 0
}

// runFromSidecar rebuilds one run: a fresh control channel (nothing restored is live), the recorded child request
// with its environment rebuilt (never written) and its binary re-resolved when the recorded one is gone, and every
// queued or running step paused — it did not complete, so it may not claim to be going. A record without a child
// step has nothing to show or control and is dropped.
func runFromSidecar(sr sidecarRun, now time.Time) *runRecord {
	if sr.ID == "" || len(sr.Steps) == 0 {
		return nil
	}
	mode := sr.Mode
	if mode == "" {
		mode = "single"
	}
	r := &runRecord{
		id:          sr.ID,
		mode:        mode,
		name:        sr.Name,
		task:        sr.Task,
		cwd:         sr.Cwd,
		startedAt:   timeVal(sr.StartedAt),
		endedAt:     timeVal(sr.EndedAt),
		done:        make(chan struct{}),
		childReq:    childRequest{Bin: sr.Child.Bin, Args: sr.Child.Args, Stdin: sr.Child.Stdin, Cwd: sr.Child.Cwd},
		sessionDir:  sr.SessionDir,
		sessionID:   sr.SessionID,
		resumedFrom: sr.ResumedFrom,
		turnNotice:  sr.TurnNotice,
		noticeSent:  true, // no live turn waits to deliver a settle notice for a restored run
	}
	// the environment the launch built (childEnv), from this process's depth and the step's agent as its role
	role := sr.Name
	if last := sr.Steps[len(sr.Steps)-1]; last.Agent != "" {
		role = last.Agent
	}
	depth, _ := subagentDepth(os.Getenv)
	r.childReq.Env = childEnv(os.Environ(), depth, role)
	if r.childReq.Bin == "" || !fileExists(r.childReq.Bin) {
		r.childReq.Bin = pigBinary()
	}
	for _, ss := range sr.Steps {
		s := &stepRecord{
			agent:        ss.Agent,
			task:         ss.Task,
			thinking:     ss.Thinking,
			state:        runState(ss.State),
			startedAt:    timeVal(ss.StartedAt),
			endedAt:      timeVal(ss.EndedAt),
			lastActivity: timeVal(ss.LastActivity),
			output:       ss.Output,
			err:          ss.Err,
			exit:         ss.Exit,
			session:      ss.Session,
			sessionDir:   ss.SessionDir,
			sessionID:    ss.SessionID,
			noticeKind:   ss.NoticeKind,
			noticeInfo:   ss.NoticeInfo,
		}
		if s.state == stateQueued || s.state == stateRunning || s.state == "" {
			s.state = statePaused
			if s.endedAt.IsZero() {
				s.endedAt = now // it stops here, not as a live child ticking on
			}
		}
		if s.startedAt.IsZero() {
			s.startedAt = s.endedAt // durations subtract startedAt; a step that never began ends where it pauses
		}
		r.steps = append(r.steps, s)
	}
	return r
}
