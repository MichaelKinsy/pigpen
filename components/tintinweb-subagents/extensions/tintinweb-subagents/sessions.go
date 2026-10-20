package tintinweb_subagents

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

// Persisted sessions, resume and the evicted agents that can still be reopened.
// upstream: src/agent-manager.ts (spawn options resumeSessionFile and reclaim, resume, startResume, tombstones,
// cleanup, removeRecord), src/agent-runner.ts (resumeAgent, the session manager choice), src/mention.ts (handles).

// maxTombstones bounds how many evicted agents stay addressable by name. upstream: agent-manager.ts MAX_TOMBSTONES.
const maxTombstones = 100

// recordLifetime is how long a finished agent's record is kept. upstream: agent-manager.ts cleanup (10 minutes).
const recordLifetime = 10 * time.Minute

// tombstone is what is kept of an evicted agent so that `@handle` can reopen its conversation.
// upstream: src/types.ts AgentTombstone.
type tombstone struct {
	Handle, Alias, ID, Type, Description string
	SessionFile                          string
	CompletedAt                          time.Time
}

// reclaim hands an evicted agent's names back to the spawn that reopens its conversation, so a resumed agent keeps
// the handle the user typed (`explore-2` cannot be derived from the type). upstream: SpawnOptions.reclaim.
type reclaim struct{ Handle, Alias string }

// spawnOptions are the options of a spawn besides the agent's type, description, name and child spec.
// upstream: agent-manager.ts SpawnOptions (isBackground, bypassQueue, resumeSessionFile, reclaim).
type spawnOptions struct {
	Background bool
	// BypassQueue starts the agent at once even at the concurrency limit; it still counts against the limit.
	BypassQueue bool
	// ResumeFile reopens this pi session file instead of starting a conversation.
	ResumeFile string
	Reclaim    *reclaim
}

// resumeRun is one resume of an agent's conversation.
type resumeRun struct {
	prompt     string
	background bool
}

// takenHandles are the names in use: the live agents' and the evicted ones', which stay reserved while they can be
// reopened. The caller holds m.mu. upstream: agent-manager.ts takenHandles.
func (m *manager) takenHandles() map[string]bool {
	taken := map[string]bool{}
	for _, r := range m.records {
		if r.Handle != "" {
			taken[r.Handle] = true
		}
		if r.Alias != "" {
			taken[r.Alias] = true
		}
	}
	for _, t := range m.tombstones {
		taken[t.Handle] = true
		if t.Alias != "" {
			taken[t.Alias] = true
		}
	}
	return taken
}

// spawnWith is spawn with options. upstream: agent-manager.ts spawn.
func (m *manager) spawnWith(typ, description, name string, spec childSpec, opts spawnOptions) *agentRecord {
	if opts.ResumeFile != "" {
		spec.ResumeFile = opts.ResumeFile
	}
	r := &agentRecord{ID: newAgentID(), Type: typ, Description: description, Name: name, Background: opts.Background, spec: spec,
		done: make(chan struct{}), started: make(chan struct{}), Status: statusQueued, StartedAt: time.Now()}
	if spec.SessionName != "" { // upstream: session.setSessionName(`${name}#${agentId.slice(0, 8)}`)
		r.spec.SessionName = spec.SessionName + "#" + r.ID[:min(8, len(r.ID))]
	}
	m.mu.Lock()
	m.records[r.ID] = r
	m.order = append(m.order, r.ID)
	if opts.Reclaim != nil {
		// A reclaimed handle is used as it is: it belongs to the conversation this spawn reopens.
		r.Handle, r.Alias = opts.Reclaim.Handle, opts.Reclaim.Alias
	} else {
		r.Handle = m.assignHandle(handleBase(typ))
	}
	if r.Alias == "" && name != "" {
		r.Alias = m.assignHandle(handleBase(name))
	}
	if opts.Background && !opts.BypassQueue && m.running >= m.maxConcurrent {
		m.queue = append(m.queue, r)
		m.mu.Unlock()
		return r
	}
	if opts.Background {
		m.running++
		r.slot = true
	}
	r.Status = statusRunning
	m.mu.Unlock()
	m.launch(r)
	return r
}

// canResume reports whether an agent has a conversation to continue: a live child, or a session file.
func (m *manager) canResume(r *agentRecord) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return (r.child != nil && r.child.alive()) || r.SessionFile != ""
}

// doneOf is the channel closed when the agent's current run has settled.
func (m *manager) doneOf(r *agentRecord) chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	return r.done
}

// errStillProcessing is what pi's session.prompt throws for a prompt sent to an agent that is mid-run.
const errStillProcessing = "Agent is already processing. Specify streamingBehavior ('steer' or 'followUp') to queue the message."

// resume continues a finished agent's conversation with a new prompt. A background resume returns at once, with the
// record running (or queued at the concurrency limit) and a notification at the end, as for a spawn; a foreground
// resume is awaited through the record's done channel and announces nothing. It reports false for an agent whose
// current run has not finished. upstream: agent-manager.ts resume, startResume.
func (m *manager) resume(r *agentRecord, prompt string, background bool, toolCallID string) bool {
	m.mu.Lock()
	if r.Status == statusRunning || r.Status == statusQueued {
		m.mu.Unlock()
		return false
	}
	r.resume = &resumeRun{prompt: prompt, background: background}
	r.Result, r.Error = "", ""
	r.CompletedAt = time.Time{}
	r.stopped = false
	r.done, r.started = make(chan struct{}), make(chan struct{})
	if background {
		r.Background = true
		r.Consumed = false
		r.ToolCallID = toolCallID // the completion notification names the call that started this run
		r.Status = statusQueued
		if m.running >= m.maxConcurrent {
			m.queue = append(m.queue, r)
			m.mu.Unlock()
			return true
		}
		m.running++
		r.slot = true
	}
	r.Status = statusRunning
	r.StartedAt = time.Now()
	m.mu.Unlock()
	m.launch(r)
	return true
}

// reopen starts the resumed run: the child that still holds the conversation takes the prompt, else the conversation
// is reopened from its session file. A reopened conversation runs under the agent's spec as it was, with no turn limit
// (a resumed turn has none). upstream: agent-runner.ts resumeAgent; spawn with resumeSessionFile.
func (m *manager) reopen(ctx context.Context, r *agentRecord, rr *resumeRun) (child, error) {
	m.mu.Lock()
	c, file, spec := r.child, r.SessionFile, r.spec
	m.mu.Unlock()
	if c != nil {
		err := c.resume(rr.prompt)
		if err == nil {
			return c, nil
		}
		if file == "" {
			return nil, err
		}
	}
	if file == "" {
		return nil, errors.New("the agent has no session to resume")
	}
	spec.ResumeFile, spec.Prompt, spec.MaxTurns, spec.InheritContext = file, rr.prompt, 0, nil // the conversation has its context already
	return m.start(ctx, spec)
}

// resolveMention finds the agent a `@name` names: a live agent's handle or alias (case-insensitively, preferring one
// that is still running, else the most recently started), an agent id, and only when nothing live answers an evicted
// agent, since reopening one while its record exists would fork the session. upstream: agent-manager.ts resolveMention.
func (m *manager) resolveMention(name string) (live *agentRecord, dead *tombstone) {
	m.mu.Lock()
	defer m.mu.Unlock()
	wanted := lower(name)
	named := func(s string) bool { return s != "" && lower(s) == wanted } // an agent without an alias never answers
	var fallback *agentRecord
	for _, id := range m.order {
		r := m.records[id]
		if !named(r.Handle) && !named(r.Alias) {
			continue
		}
		if r.Status == statusRunning || r.Status == statusQueued {
			return r, nil
		}
		if fallback == nil || r.StartedAt.After(fallback.StartedAt) {
			fallback = r
		}
	}
	if fallback != nil {
		return fallback, nil
	}
	if r := m.records[name]; r != nil {
		return r, nil
	}
	for _, t := range m.tombstones {
		if named(t.Handle) || named(t.Alias) || t.ID == name {
			c := *t
			return nil, &c
		}
	}
	return nil, nil
}

// dropTombstone forgets an evicted agent whose session file has gone. upstream: agent-manager.ts dropTombstone.
func (m *manager) dropTombstone(handle string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if i := m.tombstoneIndex(handle); i >= 0 {
		m.tombstones = slices.Delete(m.tombstones, i, i+1)
	}
}

// listTombstones are the evicted agents that can be reopened, newest first (equal ages in the order they were evicted).
func (m *manager) listTombstones() []tombstone {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]tombstone, 0, len(m.tombstones))
	for _, t := range m.tombstones {
		out = append(out, *t)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CompletedAt.After(out[j].CompletedAt) })
	return out
}

// removeRecord forgets an agent: its record, and the process that holds its conversation. What is left of it is a
// tombstone when it has a handle and a session file. The caller holds no lock. upstream: agent-manager.ts removeRecord.
func (m *manager) removeRecord(id string) {
	m.mu.Lock()
	r := m.records[id]
	if r == nil {
		m.mu.Unlock()
		return
	}
	m.tombstoneLocked(r)
	delete(m.records, id)
	for i, o := range m.order {
		if o == id {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
	c, cancel := r.child, r.cancel
	r.child, r.cancel = nil, nil
	m.mu.Unlock()
	if c != nil {
		c.close()
	}
	if cancel != nil {
		cancel()
	}
}

// tombstoneLocked keeps enough of a departing record for `@handle` to reopen its conversation: nothing when it has
// no handle or no session file (an in-memory session leaves no transcript). A handle that has a tombstone keeps its
// place in the order (a JS Map.set). The oldest go first past the bound; of equal ages the earliest kept.
func (m *manager) tombstoneLocked(r *agentRecord) {
	if r.Handle == "" || r.SessionFile == "" {
		return
	}
	completed := r.CompletedAt
	if completed.IsZero() {
		completed = time.Now()
	}
	t := &tombstone{Handle: r.Handle, Alias: r.Alias, ID: r.ID, Type: r.Type, Description: r.Description, SessionFile: r.SessionFile, CompletedAt: completed}
	if i := m.tombstoneIndex(r.Handle); i >= 0 {
		m.tombstones[i] = t
	} else {
		m.tombstones = append(m.tombstones, t)
	}
	for len(m.tombstones) > maxTombstones {
		oldest := 0
		for i, o := range m.tombstones {
			if o.CompletedAt.Before(m.tombstones[oldest].CompletedAt) {
				oldest = i
			}
		}
		m.tombstones = slices.Delete(m.tombstones, oldest, oldest+1)
	}
}

func (m *manager) tombstoneIndex(handle string) int {
	return slices.IndexFunc(m.tombstones, func(t *tombstone) bool { return t.Handle == handle })
}

// cleanup removes the finished agents that completed more than recordLifetime before now, which also ends the
// processes that kept their conversations. upstream: agent-manager.ts cleanup (run every minute).
func (m *manager) cleanup(now time.Time) {
	cutoff := now.Add(-recordLifetime)
	m.mu.Lock()
	var gone []string
	for _, id := range m.order {
		r := m.records[id]
		if r.Status == statusRunning || r.Status == statusQueued || !r.CompletedAt.Before(cutoff) {
			continue
		}
		gone = append(gone, id)
	}
	m.mu.Unlock()
	for _, id := range gone {
		m.removeRecord(id)
	}
}

// clearCompleted removes the finished agents at once, as a session starts or is switched, so that the tasks of the
// session before it do not persist. skipUnconsumed keeps those whose result the model has not read yet (the cleanup
// timer ends them in time). The evicted agents are forgotten whatever the flag says: a new session means new
// handles, or `@explore` would reach an agent the user never started. upstream: agent-manager.ts clearCompleted.
func (m *manager) clearCompleted(skipUnconsumed bool) {
	m.mu.Lock()
	var gone []string
	for _, id := range m.order {
		r := m.records[id]
		if r.Status == statusRunning || r.Status == statusQueued || (skipUnconsumed && !r.Consumed) {
			continue
		}
		gone = append(gone, id)
	}
	m.mu.Unlock()
	for _, id := range gone {
		m.removeRecord(id)
	}
	m.mu.Lock()
	m.tombstones = nil
	m.mu.Unlock()
}

// open starts a session on the manager: a child that settles from now on is kept again, if it holds a conversation.
func (m *manager) open() {
	m.mu.Lock()
	m.disposed = false
	m.mu.Unlock()
}

// dispose ends the session: every agent is stopped, every process that holds a conversation is closed, and the
// queue and the records are dropped. upstream: agent-manager.ts abortAll, dispose (index.ts session_shutdown).
func (m *manager) dispose() {
	m.mu.Lock()
	m.disposed = true // a run that settles after this closes its child
	m.mu.Unlock()
	m.abortAll()
	type held struct {
		c      child
		cancel context.CancelFunc
	}
	var all []held
	m.mu.Lock()
	for _, r := range m.records {
		if r.child != nil || r.cancel != nil {
			all = append(all, held{r.child, r.cancel})
			r.child, r.cancel = nil, nil
		}
	}
	m.records, m.order, m.queue = map[string]*agentRecord{}, nil, nil
	m.mu.Unlock()
	for _, h := range all {
		if h.c != nil {
			h.c.close()
		}
		if h.cancel != nil {
			h.cancel()
		}
	}
}

// sessionPersisted is whether an agent's conversation is a pi session file: the frontmatter's persist_session when it
// says anything, else the rememberAgents default for a top-level agent. upstream: agent-runner.ts runAgent.
func sessionPersisted(cfg *agentConfig, nested, rememberAgents bool) bool {
	if cfg != nil && cfg.PersistSession != nil {
		return *cfg.PersistSession
	}
	return !nested && rememberAgents
}

// resolveConfiguredSessionDir is the agent file's session_dir made absolute: ~ is the home directory and a relative
// path is relative to the agent's working directory. upstream: agent-runner.ts resolveConfiguredSessionDir.
func resolveConfiguredSessionDir(sessionDir, cwd string) string {
	switch {
	case sessionDir == "":
		return ""
	case sessionDir == "~" || (len(sessionDir) > 1 && sessionDir[:2] == "~/"):
		home, _ := os.UserHomeDir()
		return filepath.Join(home, sessionDir[min(2, len(sessionDir)):])
	case isRooted(sessionDir):
		return sessionDir
	}
	return filepath.Join(cwd, sessionDir)
}

// isRooted is Node's path.isAbsolute for the host: a leading `/` is absolute on every platform (Windows too, where
// filepath.IsAbs wants a drive), and so is a leading `\` on Windows.
func isRooted(p string) bool { return isRootedOn(p, os.PathSeparator == '\\') }

func isRootedOn(p string, windows bool) bool {
	return filepath.IsAbs(p) || strings.HasPrefix(p, "/") || (windows && strings.HasPrefix(p, `\`))
}

func lower(s string) string { return strings.ToLower(s) }
