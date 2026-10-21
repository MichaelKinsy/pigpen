package tintinweb_subagents

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Persisted sessions and resume (the records, the pools and the evicted agents are in manager.go).
// upstream: src/agent-manager.ts (spawn options resumeSessionFile and reclaim, resume, startResume), src/agent-runner.ts
// (resumeAgent, the session manager choice).

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
// resume is awaited through the record's done channel, takes no pool slot and announces nothing. It reports false for
// an agent whose current run has not finished. upstream: agent-manager.ts resume, startResume.
func (m *manager) resume(r *agentRecord, prompt string, background bool, toolCallID string) bool {
	m.mu.Lock()
	if r.Status == statusRunning || r.Status == statusQueued || !r.settled { // not settled: a stopped run is still ending
		m.mu.Unlock()
		return false
	}
	r.resume = &resumeRun{prompt: prompt, background: background}
	r.Result, r.Error = "", ""
	r.CompletedAt = time.Time{}
	r.stopped = false
	r.done, r.started, r.startGate = make(chan struct{}), make(chan struct{}), make(chan struct{})
	if background {
		r.Background = true
		r.Consumed = false
		r.ToolCallID = toolCallID // the completion notification names the call that started this run
		r.Status = statusQueued
		if p := m.poolFor(r); p != poolNone && !m.poolHasRoom(p) {
			m.queue = append(m.queue, queueEntry{r: r, pool: p})
			m.mu.Unlock()
			return true
		}
		m.claimLocked(r)
	} else {
		r.pool, r.settled = poolNone, false
		r.Status = statusRunning
		r.StartedAt = m.now()
		r.closeGate()
	}
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
