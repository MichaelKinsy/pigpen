package tintinweb_subagents

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// Tests of the process lifecycle around the agents: what a child's context is bound to, what a stop does to a
// conversation that lives in memory, and the cleanup that bounds the processes that keep one.

// snap is a record's status and result, read under the manager's lock.
func snap(m *manager, r *agentRecord) (status, result string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return r.Status, r.Result
}

// settled waits for the current run of an agent to settle.
func settled(t *testing.T, m *manager, r *agentRecord) {
	t.Helper()
	select {
	case <-m.doneOf(r):
	case <-time.After(15 * time.Second):
		t.Fatal("the agent did not settle")
	}
}

// realManager is a manager that starts real RPC children, which are the test binary standing in for pig.
func realManager(t *testing.T, mode string) *manager {
	t.Helper()
	useFakePig(t, mode)
	m := newManager(startRPCChild, nil)
	t.Cleanup(m.dispose)
	return m
}

func TestAnInMemoryConversationSurvivesItsRunAndTheManagerResumesIt(t *testing.T) {
	m := realManager(t, "events")
	r := m.spawn("general-purpose", "d", "", childSpec{Prompt: "one", Cwd: t.TempDir()}, false)
	settled(t, m, r)
	status, result := snap(m, r)
	eq(t, status, statusCompleted)
	eq(t, result, "answer 1 to one")
	// The context the child was started with is not ended by the run settling: the process still holds the conversation.
	eq(t, m.canResume(r), true)
	pid := r.child.(*rpcChild).cmd.Process.Pid
	eq(t, m.resume(r, "two", false, ""), true)
	settled(t, m, r)
	status, result = snap(m, r)
	eq(t, status, statusCompleted)
	eq(t, result, "answer 2 to two") // the same session answered
	eq(t, r.child.(*rpcChild).cmd.Process.Pid, pid)
}

func TestAKeptChildIsNotEndedByItsRunSettling(t *testing.T) {
	fl := &fleet{}
	m := newManager(fl.start, nil)
	t.Cleanup(m.dispose)
	r := m.spawn("general-purpose", "d", "", childSpec{Prompt: "p"}, false)
	c := fl.wait(t, 1)[0]
	c.finish("x")
	settled(t, m, r)
	time.Sleep(20 * time.Millisecond) // a context that was cancelled ends the fake child asynchronously
	eq(t, c.alive(), true)
	eq(t, m.resume(r, "more", false, ""), true)
	for i := 0; i < 400 && func() bool { c.mu.Lock(); defer c.mu.Unlock(); return len(c.resumed) == 0 }(); i++ {
		time.Sleep(5 * time.Millisecond)
	}
	c.mu.Lock()
	eq(t, c.resumed, []string{"more"}) // the process that held the conversation took the prompt
	c.mu.Unlock()
}

func TestACancelledContextEndsTheChildProcess(t *testing.T) {
	useFakePig(t, "hang")
	ctx, cancel := context.WithCancel(context.Background())
	c, err := startRPCChild(ctx, childSpec{Prompt: "p", Cwd: t.TempDir()})
	eq(t, err, nil)
	done := make(chan error, 1)
	go func() { _, err := c.wait(); done <- err }()
	cancel()
	select {
	case err := <-done:
		eq(t, err != nil, true)
	case <-time.After(10 * time.Second):
		t.Fatal("the child outlived its context")
	}
}

func TestStoppingAnInMemoryAgentKeepsItsConversation(t *testing.T) {
	m := realManager(t, "stoppable")
	r := m.spawn("general-purpose", "d", "", childSpec{Prompt: "one", Cwd: t.TempDir()}, false)
	eq(t, m.awaitStartup(r, 10*time.Second), nil)
	eq(t, m.abort(r), true)
	settled(t, m, r)
	status, _ := snap(m, r)
	eq(t, status, statusStopped)
	eq(t, m.canResume(r), true) // session.abort() leaves the session resumable
	eq(t, m.resume(r, "two", false, ""), true)
	settled(t, m, r)
	status, result := snap(m, r)
	eq(t, status, statusCompleted)
	eq(t, result, "answer 2 to two")
}

func TestAnInMemoryAgentThatIgnoresAStopIsEndedAfterTheGrace(t *testing.T) {
	old := abortGrace
	abortGrace = 200 * time.Millisecond
	t.Cleanup(func() { abortGrace = old })
	m := realManager(t, "deaf")
	r := m.spawn("general-purpose", "d", "", childSpec{Prompt: "one", Cwd: t.TempDir()}, false)
	eq(t, m.awaitStartup(r, 10*time.Second), nil)
	eq(t, m.abort(r), true)
	settled(t, m, r) // a run that never settles must not hold its agent forever
	status, _ := snap(m, r)
	eq(t, status, statusStopped)
	eq(t, m.canResume(r), false) // the process went, and its conversation with it
}

func TestStoppingAPersistedAgentEndsItsProcess(t *testing.T) {
	useFakePig(t, "hang")
	c, err := startRPCChild(context.Background(), childSpec{Prompt: "p", Cwd: t.TempDir(), Persist: true})
	eq(t, err, nil)
	c.abort()
	select {
	case <-c.(*rpcChild).exited:
	case <-time.After(10 * time.Second):
		t.Fatal("the process did not end")
	}
}

func TestAStopAndAResumeThatMeetOnAQueuedAgentNeitherRaceNorPanic(t *testing.T) {
	for range 200 {
		fl := &fleet{}
		m := newManager(fl.start, nil)
		m.maxConcurrent = 1
		a := m.spawn("general-purpose", "a", "", childSpec{}, true)
		fl.wait(t, 1)
		b := m.spawn("general-purpose", "b", "", childSpec{}, true) // queued behind a
		var wg sync.WaitGroup
		wg.Add(2)
		accepted := false
		go func() { defer wg.Done(); m.abort(b) }()
		go func() {
			defer wg.Done()
			for range 10000 {
				if m.resume(b, "again", true, "") {
					accepted = true
					return
				}
				runtime.Gosched()
			}
		}()
		wg.Wait()
		if accepted {
			// The resumed run is queued behind a: the stop must not have closed the channels that run will use.
			select {
			case <-m.doneOf(b):
				t.Fatal("the stop closed the resumed run's done channel")
			default:
			}
			m.mu.Lock()
			started := b.started
			m.mu.Unlock()
			select {
			case <-started:
				t.Fatal("the stop closed the resumed run's started channel")
			default:
			}
		}
		m.dispose()
		settled(t, m, a)
	}
}

// A stopped record's channels are closed in the same step that makes it visible as stopped: a waiter that sees the
// status must not find the channel it waits on still open.
func TestAStoppedQueuedAgentIsNeverVisibleBeforeItsChannelsAreClosed(t *testing.T) {
	for range 500 {
		fl := &fleet{}
		m := newManager(fl.start, nil)
		m.maxConcurrent = 1
		m.spawn("general-purpose", "a", "", childSpec{}, true)
		fl.wait(t, 1)
		b := m.spawn("general-purpose", "b", "", childSpec{}, true) // queued
		seen := make(chan string, 1)
		go func() {
			for {
				m.mu.Lock()
				if b.Status == statusStopped {
					done, started := b.done, b.started
					m.mu.Unlock()
					for _, ch := range []chan struct{}{done, started} {
						select {
						case <-ch:
						default:
							seen <- "a stopped agent's channel was still open"
							return
						}
					}
					seen <- ""
					return
				}
				m.mu.Unlock()
				runtime.Gosched()
			}
		}()
		m.abort(b)
		if msg := <-seen; msg != "" {
			m.dispose()
			t.Fatal(msg)
		}
		m.dispose()
	}
}

func TestAPanicInARunSettlesTheAgentAndStartsTheNext(t *testing.T) {
	fl := &fleet{}
	var mu sync.Mutex
	first := true
	m := newManager(func(ctx context.Context, spec childSpec) (child, error) {
		mu.Lock()
		boom := first
		first = false
		mu.Unlock()
		if boom {
			panic("boom")
		}
		return fl.start(ctx, spec)
	}, nil)
	t.Cleanup(m.dispose)
	m.maxConcurrent = 1
	a := m.spawn("general-purpose", "a", "", childSpec{}, true)
	b := m.spawn("general-purpose", "b", "", childSpec{}, true) // queued: it starts once a's slot is given back
	settled(t, m, a)
	status, _ := snap(m, a)
	eq(t, status, statusError)
	fl.wait(t, 1)
	m.mu.Lock()
	eq(t, a.Error, "internal error in the agent run")
	eq(t, b.Status, statusRunning)
	m.mu.Unlock()
}

func TestGuardRecoversAndReports(t *testing.T) {
	eq(t, guard("a test", func() { panic("x") }), true)
	eq(t, guard("a test", func() {}), false)
}

func TestACompletedRunEndsItsProcessWhenTheSessionHasEnded(t *testing.T) {
	fl := &fleet{}
	m := newManager(fl.start, nil)
	r := m.spawn("general-purpose", "d", "", childSpec{}, false)
	c := fl.wait(t, 1)[0]
	m.mu.Lock()
	m.disposed = true
	m.mu.Unlock()
	c.finish("x")
	settled(t, m, r)
	for i := 0; i < 400 && c.alive(); i++ {
		time.Sleep(5 * time.Millisecond)
	}
	eq(t, c.alive(), false)
	c.mu.Lock()
	eq(t, c.closed, true)
	c.mu.Unlock()
}

// The cleanup timer, the shutdown and the session boundaries bound the processes that keep conversations.

func TestTheCleanupTimerRemovesFinishedAgents(t *testing.T) {
	r := startRigTuned(t, t.TempDir(), func(a *app) { a.cleanupEvery = 10 * time.Millisecond })
	kept, id := finished(t, r, "Plan")
	running, _ := spawnOne(t, r, "Plan", nil)
	rec := r.app.mgr.get(id)
	r.app.mgr.mu.Lock()
	rec.CompletedAt = time.Now().Add(-recordLifetime - time.Minute)
	r.app.mgr.mu.Unlock()
	deadline := time.Now().Add(5 * time.Second)
	for r.app.mgr.get(id) != nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	eq(t, r.app.mgr.get(id) == nil, true)
	kept.mu.Lock()
	eq(t, kept.closed, true) // the process that held its conversation ended with the record
	kept.mu.Unlock()
	running.mu.Lock()
	eq(t, running.closed, false) // a running agent is never cleaned up
	running.mu.Unlock()
}

func TestTheCleanupTimerRunsEveryMinute(t *testing.T) {
	eq(t, cleanupEvery, time.Minute) // upstream: agent-manager.ts:428 setInterval(cleanup, 60_000)
	eq(t, startRig(t).app.cleanupEvery, time.Minute)
}

func TestShutdownClosesEveryKeptChildAndStopsTheRunningOnes(t *testing.T) {
	r := startRig(t)
	kept, id := finished(t, r, "Plan")
	running, _ := spawnOne(t, r, "Plan", nil)
	r.app.mu.Lock()
	ticking := r.app.stopCleanup != nil
	r.app.mu.Unlock()
	eq(t, ticking, true)
	r.Fire("session_shutdown", obj{"reason": "quit"})
	for i := 0; i < 400 && !(func() bool { running.mu.Lock(); defer running.mu.Unlock(); return running.closed }()); i++ {
		time.Sleep(5 * time.Millisecond)
	}
	kept.mu.Lock()
	eq(t, kept.closed, true)
	kept.mu.Unlock()
	running.mu.Lock()
	eq(t, running.aborted && running.closed, true)
	running.mu.Unlock()
	eq(t, len(r.app.mgr.list()), 0)
	eq(t, r.app.mgr.get(id) == nil, true)
	r.app.mu.Lock()
	eq(t, r.app.stopCleanup == nil, true) // the timer is gone with the session
	r.app.mu.Unlock()
}

func TestShutdownCancelsTheNoticesNotYetSent(t *testing.T) {
	r := startRig(t)
	_, id := finished(t, r, "Plan") // its notice is held for nudgeHold, for a consume to cancel it
	r.Fire("session_shutdown", obj{"reason": "quit"})
	time.Sleep(nudgeHold + 300*time.Millisecond)
	for _, m := range r.messages() {
		if strings.Contains(m["message"].(map[string]any)["content"].(string), id) {
			t.Fatal("a notice was sent to a session that had ended")
		}
	}
}

func TestASessionThatStartsAgainAfterShutdownKeepsItsConversations(t *testing.T) {
	r := startRig(t)
	r.Fire("session_shutdown", obj{"reason": "new"})
	r.Fire("session_start", obj{"reason": "new"})
	kept, id := finished(t, r, "Plan")
	time.Sleep(20 * time.Millisecond)
	eq(t, kept.alive(), true)
	eq(t, r.app.mgr.canResume(r.app.mgr.get(id)), true)
	r.app.mu.Lock()
	eq(t, r.app.stopCleanup != nil, true) // and the cleanup timer runs again
	r.app.mu.Unlock()
}

func TestSessionBoundariesClearTheFinishedAgentsAndTheEvictedOnes(t *testing.T) {
	for _, event := range []string{"session_start", "session_before_switch"} {
		t.Run(event, func(t *testing.T) {
			r := startRig(t)
			r.fleet.session = "/sessions/plan.jsonl"
			_, first := finished(t, r, "Plan")
			// Evicted: what is left of it is a tombstone, which holds its handle.
			r.app.mgr.cleanup(time.Now().Add(recordLifetime + time.Minute))
			eq(t, r.app.mgr.get(first) == nil, true)
			eq(t, len(r.app.mgr.listTombstones()), 1)
			read, readID := spawnOne(t, r, "Plan", nil)
			read.finish("read")
			waitStatus(t, r, readID, statusCompleted)
			eq(t, r.app.mgr.consume(readID), true)
			unread, unreadID := spawnOne(t, r, "Plan", nil)
			unread.finish("not read")
			waitStatus(t, r, unreadID, statusCompleted)

			r.Fire(event, obj{"reason": "new"})

			eq(t, r.app.mgr.get(readID) == nil, true) // a session boundary clears the agents of the session before
			read.mu.Lock()
			eq(t, read.closed, true)
			read.mu.Unlock()
			eq(t, r.app.mgr.get(unreadID) != nil, true) // but not a result the model has not read yet
			unread.mu.Lock()
			eq(t, unread.closed, false)
			unread.mu.Unlock()
			eq(t, len(r.app.mgr.listTombstones()), 0) // a new session means new handles: `@plan` reaches nothing evicted
			eq(t, r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "Plan", "resume": readID}),
				`Agent not found: "`+readID+`". It may have been cleaned up.`)
		})
	}
}

func TestClearCompletedWithoutTheFlagClearsUnreadResultsToo(t *testing.T) {
	r := startRig(t)
	c, id := finished(t, r, "Plan")
	r.app.mgr.clearCompleted(false)
	eq(t, r.app.mgr.get(id) == nil, true)
	c.mu.Lock()
	eq(t, c.closed, true)
	c.mu.Unlock()
}

func TestClearCompletedKeepsWhatIsRunning(t *testing.T) {
	r := startRig(t)
	_, id := spawnOne(t, r, "Plan", nil)
	r.app.mgr.clearCompleted(false)
	eq(t, r.app.mgr.get(id) != nil, true)
}

// The order of the evicted agents is the order they were evicted in, as a JS Map keeps it.

func TestEvictedAgentsKeepTheirInsertionOrder(t *testing.T) {
	add := func(m *manager, handle, alias string, at int64) {
		m.mu.Lock()
		m.tombstoneLocked(&agentRecord{ID: "id-" + handle, Handle: handle, Alias: alias, SessionFile: "/s/" + handle, CompletedAt: time.Unix(at, 0)})
		m.mu.Unlock()
	}
	t.Run("a mention answers the first evicted agent that matches", func(t *testing.T) {
		for range 20 { // a Go map would answer in a random order
			m := newManager(nil, nil)
			add(m, "b", "zed", 1)
			add(m, "zed", "", 2)
			_, dead := m.resolveMention("ZED")
			eq(t, dead.Handle, "b")
		}
	})
	t.Run("agents evicted together are listed in the order they were evicted", func(t *testing.T) {
		m := newManager(nil, nil)
		for _, h := range []string{"c", "a", "d", "b"} {
			add(m, h, "", 5)
		}
		var got []string
		for _, tb := range m.listTombstones() {
			got = append(got, tb.Handle)
		}
		eq(t, got, []string{"c", "a", "d", "b"})
	})
	t.Run("past the bound the earliest of the oldest goes", func(t *testing.T) {
		m := newManager(nil, nil)
		for i := range maxTombstones + 1 {
			add(m, "h"+string(rune('a'+i%26))+string(rune('a'+i/26)), "", 7)
		}
		eq(t, len(m.tombstones), maxTombstones)
		eq(t, m.tombstoneIndex("haa"), -1)
		eq(t, m.tombstoneIndex("hba") >= 0, true)
	})
	t.Run("an agent evicted again keeps its place", func(t *testing.T) {
		m := newManager(nil, nil)
		add(m, "a", "", 1)
		add(m, "b", "", 1)
		add(m, "a", "", 1)
		eq(t, m.tombstoneIndex("a"), 0)
		eq(t, len(m.tombstones), 2)
	})
}

func TestAMentionOfNothingAnswersNothing(t *testing.T) {
	fl := &fleet{}
	m := newManager(fl.start, nil)
	t.Cleanup(m.dispose)
	m.spawn("general-purpose", "d", "", childSpec{}, true) // it has a handle but no alias
	fl.wait(t, 1)
	live, dead := m.resolveMention("")
	eq(t, live == nil && dead == nil, true) // undefined !== "" in the original
	live, _ = m.resolveMention("General-Purpose")
	eq(t, live != nil, true)
}

func TestARefusedResumeLeavesTheToolCallIDAlone(t *testing.T) {
	fl := &fleet{}
	m := newManager(fl.start, nil)
	t.Cleanup(m.dispose)
	r := m.spawn("general-purpose", "d", "", childSpec{}, true)
	m.mu.Lock()
	r.ToolCallID = "call-1"
	m.mu.Unlock()
	fl.wait(t, 1)
	eq(t, m.resume(r, "x", true, "call-2"), false) // still running
	m.mu.Lock()
	eq(t, r.ToolCallID, "call-1")
	m.mu.Unlock()
	fl.children[0].finish("done")
	settled(t, m, r)
	eq(t, m.resume(r, "x", true, "call-3"), true)
	m.mu.Lock()
	eq(t, r.ToolCallID, "call-3")
	m.mu.Unlock()
}

func TestRememberAgentsKeepsTheLastValueWhenTheSettingIsGone(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	file := filepath.Join(cwd, ".pi", "subagents.json")
	write(t, file, `{"rememberAgents": false}`)
	r := startRigIn(t, cwd)
	r.app.mu.Lock()
	eq(t, r.app.rememberAgents, false)
	r.app.mu.Unlock()
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	r.app.reload(cwd)
	r.app.mu.Lock()
	eq(t, r.app.rememberAgents, false) // upstream: settings.ts applies a setting only when it is a boolean
	r.app.mu.Unlock()
}

func TestTheNameOfAPersistedAgentIsOnTheSessionItRunsIn(t *testing.T) {
	logOf := func(t *testing.T, spec childSpec) []string {
		t.Helper()
		useFakePig(t, "ok")
		logFile := t.TempDir() + "/log"
		t.Setenv("FAKE_PIG_LOG", logFile)
		t.Setenv("FAKE_PIG_SESSION", "/sessions/child.jsonl")
		spec.Prompt, spec.Cwd = "p", t.TempDir()
		c, err := startRPCChild(context.Background(), spec)
		eq(t, err, nil)
		_, err = c.wait()
		eq(t, err, nil)
		b, _ := os.ReadFile(logFile)
		var out []string
		for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			if l != "get_session_stats" && l != "get_last_assistant_text" {
				out = append(out, l)
			}
		}
		return out
	}
	t.Run("a new persisted session is named after new_session replaced the one --name named", func(t *testing.T) {
		got := logOf(t, childSpec{Persist: true, ParentSession: "/sessions/parent.jsonl", SessionName: "Explore#abcd1234"})
		eq(t, got, []string{"new_session", "set_session_name Explore#abcd1234", "get_state", "prompt"})
	})
	t.Run("a reopened session is named again", func(t *testing.T) {
		got := logOf(t, childSpec{Persist: true, ResumeFile: "/sessions/child.jsonl", SessionName: "Explore#abcd1234"})
		eq(t, got, []string{"set_session_name Explore#abcd1234", "get_state", "prompt"})
	})
	t.Run("an in-memory session is named too", func(t *testing.T) {
		got := logOf(t, childSpec{SessionName: "Explore#abcd1234"})
		eq(t, got, []string{"set_session_name Explore#abcd1234", "prompt"})
	})
	t.Run("an agent with no name sets none", func(t *testing.T) {
		got := logOf(t, childSpec{Persist: true, ParentSession: "/sessions/parent.jsonl"})
		eq(t, got, []string{"new_session", "get_state", "prompt"})
	})
}

func TestAChildNeverReadsAppendSystemMd(t *testing.T) {
	// An explicit empty append keeps pig from discovering APPEND_SYSTEM.md, as the original's loader suppresses it
	// (appendSystemPromptOverride: () => []) so that prompt_mode: replace and isolated are not defeated.
	for _, mode := range []string{"replace", "append"} {
		args := childArgs(childSpec{SystemPrompt: "SP", PromptMode: mode})
		i := slices.Index(args, "--append-system-prompt")
		eq(t, i >= 0 && args[i+1] == "", true)
	}
	args := childArgs(childSpec{SystemPrompt: "SP", PromptMode: "append"})
	eq(t, args[len(args)-2:], []string{"--append-system-prompt", "SP"}) // the agent's own append comes after the empty one
}

func TestJSTrim(t *testing.T) {
	eq(t, jsTrim("\uFEFF  x \u00a0\u2028"), "x")  // BOM, NBSP and the line separators are white space in JS
	eq(t, jsTrim("\u0085x\u0085"), "\u0085x\u0085") // NEL is not
	eq(t, jsTrim(" \t\r\n\v\f"), "")
}

func TestTextWithABOMIsTrimmedAsJSDoes(t *testing.T) {
	// The parent context: a message that is only a BOM says nothing.
	eq(t, buildParentContext([]map[string]any{{"type": "message", "message": obj{"role": "user", "content": "\uFEFF"}}}), "")
	// A skill file: its content is trimmed with the BOM.
	cwd := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	write(t, filepath.Join(cwd, ".pi", "skills", "bom.md"), "\uFEFFbody\n")
	eq(t, preloadSkills([]string{"bom"}, cwd)[0].Content, "body")
}

func TestRootedPaths(t *testing.T) {
	eq(t, isRootedOn("/x", true), true)   // Node: a leading slash is absolute on Windows too
	eq(t, isRootedOn(`\x`, true), true)   // and a leading backslash
	eq(t, isRootedOn(`\x`, false), false) // which is a file name on POSIX
	eq(t, isRootedOn("x/y", true), false)
	eq(t, isRootedOn("", false), false)
	eq(t, resolveConfiguredSessionDir("/abs/s", "/cwd"), "/abs/s")
}

func TestAPackageJSONOfAnotherShapeStopsTheClimb(t *testing.T) {
	for _, body := range []string{`{"pi": "x"}`, `{"name": "inner"}`, `[]`, `"text"`, `{"pi": {"extensions": "x"}}`} {
		root := t.TempDir()
		write(t, filepath.Join(root, "package.json"), `{"name": "outer", "pi": {"extensions": ["./inner/index.ts"]}}`)
		write(t, filepath.Join(root, "inner", "package.json"), body)
		entry := filepath.Join(root, "inner", "index.ts")
		write(t, entry, "export default () => {};")
		eq(t, extensionPackageName(entry), "") // the first package.json is the package root: decide there
	}
	// Not JSON: the climb goes on, as the original's JSON.parse throws and is caught.
	root := t.TempDir()
	write(t, filepath.Join(root, "package.json"), `{"name": "outer", "pi": {"extensions": ["./inner/index.ts"]}}`)
	write(t, filepath.Join(root, "inner", "package.json"), `{ not json`)
	entry := filepath.Join(root, "inner", "index.ts")
	write(t, entry, "export default () => {};")
	eq(t, extensionPackageName(entry), "outer")
}

func TestAnExcludeMayNameAnExtensionLoadedFromAPath(t *testing.T) {
	in := scopeInput{Type: "Explore", ConfigCwd: "/tmp", ToolNames: builtins7,
		Config:   &agentConfig{Extensions: []string{"/abs/foo.ts"}, ExcludeExtensions: []string{"foo"}, BuiltinToolNames: builtins7},
		Loadable: map[string]loadedExtension{"/abs/foo.ts": {Path: "/abs/foo.ts"}}}
	p := planScope(in)
	// The loader's base holds it, so the exclude is no typo: only the conflict is reported.
	eq(t, warnings(p, "extension-error:"), []string{`extension-error:extension "foo" is in both extensions: and exclude_extensions: for agent "Explore" — exclude wins`})
}

func TestSpawnRPCOptionsOfTheOriginalsInternalsAreDroppedAndTruthyOnesCount(t *testing.T) {
	r := startRig(t)
	reply := r.request("subagents:rpc:spawn", obj{"type": "Plan", "prompt": "task", "options": obj{"description": "d",
		"parentAgentId": "x", "workflowId": "w", "depth": 3.0, "maxSubagentDepth": 2.0, "configCwd": "/elsewhere", "rootSessionId": "../../x",
		"isolated": "yes", "inheritContext": 1.0}})
	eq(t, reply["success"], true) // dropped, not refused: upstream: index.ts spawnTopLevel
	c := r.fleet.wait(t, 1)[0]
	eq(t, c.spec.NoExtensions && c.spec.NoSkills, true) // JS truthiness: "yes" is isolated
	eq(t, c.spec.InheritContext != nil, true)           // and 1 is inheritContext
	eq(t, jsTruthy(0.0) || jsTruthy("") || jsTruthy(nil) || jsTruthy(false), false)
	eq(t, jsTruthy(obj{}) && jsTruthy([]any{}) && jsTruthy("0") && jsTruthy(-1.0), true)
}

func TestInheritContextIsReadWhenTheRunStarts(t *testing.T) {
	r := startRig(t)
	r.app.mgr.mu.Lock()
	r.app.mgr.maxConcurrent = 1
	r.app.mgr.mu.Unlock()
	r.branch = []any{obj{"type": "message", "message": obj{"role": "user", "content": "early"}}}
	r.must("Agent", obj{"prompt": "first", "description": "d", "subagent_type": "Plan"})
	a := r.fleet.wait(t, 1)[0]
	r.must("Agent", obj{"prompt": "second", "description": "d", "subagent_type": "Plan", "inherit_context": true}) // queued
	r.mu.Lock()
	r.branch = []any{obj{"type": "message", "message": obj{"role": "user", "content": "late"}}}
	r.mu.Unlock()
	a.finish("done")
	b := r.fleet.wait(t, 2)[1]
	eq(t, strings.Contains(b.spec.Prompt, "[User]: late") && !strings.Contains(b.spec.Prompt, "early"), true)
}
