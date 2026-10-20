package tintinweb_subagents

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Persisted sessions, resume, inherit_context, the spawn RPC options and the scope flags a child gets.
// upstream: src/agent-manager.ts, src/agent-runner.ts, src/index.ts (Agent execute, spawnTopLevel), src/context.ts.

// sessionRig starts a session in a workspace that holds one custom agent, "custom", and a project subagents.json.
func sessionRig(t *testing.T, frontmatter, settingsJSON string) *rig {
	t.Helper()
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	cwd := t.TempDir()
	if frontmatter != "" {
		write(t, filepath.Join(cwd, ".pi", "agents", "custom.md"), "---\ndescription: a custom agent\n"+frontmatter+"\n---\nDo the work.\n")
	}
	if settingsJSON != "" {
		write(t, filepath.Join(cwd, ".pi", "subagents.json"), settingsJSON)
	}
	return startRigIn(t, cwd)
}

// spawnOne spawns an agent in the background and returns its fake child and id.
func spawnOne(t *testing.T, r *rig, typ string, extra obj) (*fakeChild, string) {
	t.Helper()
	params := obj{"prompt": "p", "description": "d", "subagent_type": typ}
	for k, v := range extra {
		params[k] = v
	}
	before := r.fleet.count()
	id := idFrom(t, r.must("Agent", params))
	return r.fleet.wait(t, before+1)[before], id
}

func TestSessionPersistenceTwins(t *testing.T) {
	const f = "agent-runner"
	tw(t, f, "persists by default, so a handle can reopen the conversation later", func(t *testing.T) {
		r := sessionRig(t, "", "")
		r.must("Agent", obj{"prompt": "go", "description": "d", "subagent_type": "Plan"})
		c := r.fleet.wait(t, 1)[0]
		eq(t, c.spec.Persist, true)
		eq(t, slices.Contains(childArgs(c.spec), "--no-session"), false)
	})
	tw(t, f, "keeps the session in memory when rememberAgents is off", func(t *testing.T) {
		r := sessionRig(t, "", `{"rememberAgents": false}`)
		r.must("Agent", obj{"prompt": "go", "description": "d", "subagent_type": "Plan"})
		c := r.fleet.wait(t, 1)[0]
		eq(t, c.spec.Persist, false)
		eq(t, slices.Contains(childArgs(c.spec), "--no-session"), true)
	})
	tw(t, f, "lets frontmatter override rememberAgents in both directions", func(t *testing.T) {
		// An agent that declares itself ephemeral stays ephemeral with the setting on...
		r := sessionRig(t, "persist_session: false", "")
		r.must("Agent", obj{"prompt": "go", "description": "d", "subagent_type": "custom"})
		eq(t, r.fleet.wait(t, 1)[0].spec.Persist, false)
		// ...and one that declares itself persistent still persists with it off.
		r = sessionRig(t, "persist_session: true", `{"rememberAgents": false}`)
		r.must("Agent", obj{"prompt": "go", "description": "d", "subagent_type": "custom"})
		eq(t, r.fleet.wait(t, 1)[0].spec.Persist, true)
	})
	// A nested child is reached through nested delegation, which this port does not ship: the decision is the same
	// function the spawn uses.
	tw(t, f, "leaves a nested child in memory, since nothing can address it later", func(t *testing.T) {
		eq(t, sessionPersisted(&agentConfig{Name: "Explore"}, true, true), false)
		eq(t, sessionPersisted(&agentConfig{Name: "Explore"}, false, true), true)
	})
	tw(t, f, "still persists a nested child that asks for it in frontmatter", func(t *testing.T) {
		yes := true
		eq(t, sessionPersisted(&agentConfig{Name: "Explore", PersistSession: &yes}, true, true), true)
	})
	tw(t, f, "reopens an existing session file instead of starting a new conversation", func(t *testing.T) {
		r := startRig(t)
		rec := r.app.mgr.spawnWith("Plan", "d", "", childSpec{Prompt: "carry on", Persist: true}, spawnOptions{ResumeFile: "/sessions/explore.jsonl", Background: true})
		c := r.fleet.wait(t, 1)[0]
		eq(t, c.spec.ResumeFile, "/sessions/explore.jsonl")
		args := childArgs(c.spec)
		i := slices.Index(args, "--session")
		eq(t, i >= 0 && args[i+1] == "/sessions/explore.jsonl", true)
		eq(t, slices.Contains(args, "--no-session"), false) // an empty conversation would defeat the resume
		_ = rec
	})
	tw(t, f, "uses pi's normal persistent session location and links to the parent session", func(t *testing.T) {
		r := sessionRig(t, "persist_session: true", "")
		r.sessionFile = "/sessions/parent.jsonl"
		r.must("Agent", obj{"prompt": "go", "description": "d", "subagent_type": "custom"})
		c := r.fleet.wait(t, 1)[0]
		eq(t, c.spec.Persist, true)
		eq(t, c.spec.SessionDir, "") // pi's own default location: the child resolves it
		eq(t, c.spec.ParentSession, "/sessions/parent.jsonl")
		eq(t, slices.Contains(childArgs(c.spec), "--session-dir"), false)
	})
	tw(t, f, "uses a frontmatter sessionDir when persistSession is true and sessionDir is configured", func(t *testing.T) {
		r := sessionRig(t, "persist_session: true\nsession_dir: .seams/pi-sessions/seam-plan-reviewer", "")
		r.sessionFile = "/sessions/parent.jsonl"
		r.must("Agent", obj{"prompt": "go", "description": "d", "subagent_type": "custom"})
		c := r.fleet.wait(t, 1)[0]
		eq(t, c.spec.SessionDir, filepath.Join(r.app.cwd, ".seams/pi-sessions/seam-plan-reviewer"))
		eq(t, c.spec.ParentSession, "/sessions/parent.jsonl")
		args := childArgs(c.spec)
		i := slices.Index(args, "--session-dir")
		eq(t, i >= 0 && args[i+1] == c.spec.SessionDir, true)
	})
	tw(t, f, "suppresses AGENTS.md/CLAUDE.md/APPEND_SYSTEM.md for subagents", func(t *testing.T) {
		// The loader of the original is told noContextFiles; here the child is told --no-context-files (and no prompt
		// templates or themes, as the original's loader is).
		args := childArgs(childSpec{})
		for _, want := range []string{"--no-context-files", "--no-prompt-templates", "--no-themes"} {
			eq(t, slices.Contains(args, want), true)
		}
	})
	tw(t, f, "sets the agent name as session name before binding extensions", func(t *testing.T) {
		r := startRig(t)
		r.must("Agent", obj{"prompt": "go", "description": "d", "subagent_type": "Explore"})
		c := r.fleet.wait(t, 1)[0]
		// --name is a start-up flag: the name is in place before the child binds its extensions.
		args := childArgs(c.spec)
		i := slices.Index(args, "--name")
		eq(t, i >= 0 && strings.HasPrefix(args[i+1], "Explore#"), true)
	})
	tw(t, f, "suffixes the session name with a short agentId so parallel spawns are distinguishable", func(t *testing.T) {
		r := startRig(t)
		id := idFrom(t, r.must("Agent", obj{"prompt": "go", "description": "d", "subagent_type": "Explore"}))
		c := r.fleet.wait(t, 1)[0]
		eq(t, c.spec.SessionName, "Explore#"+id[:8])
	})
}

// finishAndResume spawns a background agent, lets it finish, and returns it with its id.
func finished(t *testing.T, r *rig, typ string) (*fakeChild, string) {
	t.Helper()
	id := idFrom(t, r.must("Agent", obj{"prompt": "first", "description": "d", "subagent_type": typ}))
	c := r.fleet.wait(t, 1)[0]
	c.finish("first answer")
	r.waitEmitted("subagents:completed")
	waitStatus(t, r, id, statusCompleted)
	return c, id
}

func waitStatus(t *testing.T, r *rig, id, status string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rec := r.app.mgr.get(id)
		r.app.mgr.mu.Lock()
		got := rec.Status
		r.app.mgr.mu.Unlock()
		if got == status {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("agent %s never reached %s", id, status)
}

// foreground runs a tool call that blocks until the agent settles, and returns its result when it has.
func foreground(r *rig, name string, params obj) func() (string, string) {
	type out struct{ text, failure string }
	done := make(chan out, 1)
	go func() {
		text, failure := r.tool(name, params)
		done <- out{text, failure}
	}()
	return func() (string, string) {
		select {
		case o := <-done:
			return o.text, o.failure
		case <-time.After(10 * time.Second):
			r.t.Fatal("the tool call did not return")
		}
		return "", ""
	}
}

func TestResumeContinuesTheSameConversation(t *testing.T) {
	r := startRig(t)
	c, id := finished(t, r, "Plan")
	eq(t, len(c.resumed), 0)
	wait := foreground(r, "Agent", obj{"prompt": "and now the second part", "description": "d", "subagent_type": "Plan", "resume": id, "run_in_background": false})
	for i := 0; i < 500 && func() bool { c.mu.Lock(); defer c.mu.Unlock(); return len(c.resumed) == 0 }(); i++ {
		time.Sleep(5 * time.Millisecond)
	}
	// The prompt goes into the child that holds the conversation: no second child, no old answer pasted in.
	eq(t, r.fleet.count(), 1)
	eq(t, c.resumed, []string{"and now the second part"})
	c.finish("second answer")
	text, failure := wait()
	eq(t, failure, "")
	eq(t, text, "second answer") // the original returns the bare result, without the "Agent completed" header
	// The record is the same agent: its result is the new answer, and it is not announced for a foreground resume.
	out := r.must("get_subagent_result", obj{"agent_id": id})
	eq(t, strings.HasSuffix(out, "\n\nsecond answer"), true)
}

func TestForegroundResumeIsNotAnnounced(t *testing.T) {
	r := startRig(t)
	c, id := finished(t, r, "Plan")
	time.Sleep(nudgeHold + 300*time.Millisecond) // the first run's own notification
	before := len(r.messages())
	wait := foreground(r, "Agent", obj{"prompt": "more", "description": "d", "subagent_type": "Plan", "resume": id, "run_in_background": false})
	for i := 0; i < 500 && func() bool { c.mu.Lock(); defer c.mu.Unlock(); return len(c.resumed) == 0 }(); i++ {
		time.Sleep(5 * time.Millisecond)
	}
	c.finish("done again")
	wait()
	time.Sleep(nudgeHold + 300*time.Millisecond)
	eq(t, len(r.messages()), before) // foreground resume does not fire onComplete
}

func TestBackgroundResumeNotifiesLikeASpawn(t *testing.T) {
	r := startRig(t)
	c, id := finished(t, r, "Plan")
	time.Sleep(nudgeHold + 300*time.Millisecond)
	before := len(r.messages())
	text := r.must("Agent", obj{"prompt": "continue", "description": "d", "subagent_type": "Plan", "resume": id})
	eq(t, text, "Agent resumed in background.\nAgent ID: "+id+"\nType: Plan\n\nYou will be notified when this agent completes.\n"+
		"Use get_subagent_result to retrieve full results, or steer_subagent to send it messages.")
	eq(t, c.resumed, []string{"continue"})
	eq(t, r.fleet.count(), 1)
	c.finish("background answer")
	deadline := time.Now().Add(5 * time.Second)
	for len(r.messages()) == before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	msgs := r.messages()
	eq(t, len(msgs), before+1)
	content := msgs[len(msgs)-1]["message"].(map[string]any)["content"].(string)
	eq(t, strings.Contains(content, "<result>background answer</result>"), true)
	eq(t, strings.Contains(content, "<task-id>"+id+"</task-id>"), true)
}

func TestResumeAnswersWhatItCannotDo(t *testing.T) {
	r := startRig(t)
	eq(t, r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "Plan", "resume": "nope"}), `Agent not found: "nope". It may have been cleaned up.`)
	// A resume does not need a valid subagent_type: it replays the stored session and ignores it.
	eq(t, r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "no-such-type", "resume": "nope"}), `Agent not found: "nope". It may have been cleaned up.`)
	id := idFrom(t, r.must("Agent", obj{"prompt": "first", "description": "d", "subagent_type": "Plan"}))
	c := r.fleet.wait(t, 1)[0]
	// Still running: a background resume says why it cannot, a foreground one fails as pi's session does.
	eq(t, r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "Plan", "resume": id}),
		`Agent "`+id+`" is still running — it can only be resumed once its current run finishes.`+"\n"+
			"Use steer_subagent to send it a message mid-run, or get_subagent_result to wait for it.")
	eq(t, r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "Plan", "resume": id, "run_in_background": false}), "Agent failed: "+errStillProcessing)
	eq(t, len(c.resumed), 0)
	c.finish("x")
}

func TestAnAgentWithNoSessionCannotBeResumed(t *testing.T) {
	r := startRig(t)
	r.fleet.setFail(errBoom)
	id := idFrom(t, r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "Plan"}))
	<-r.app.mgr.doneOf(r.app.mgr.get(id))
	eq(t, r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "Plan", "resume": id}), `Agent "`+id+`" has no active session to resume.`)
}

func TestResumeReopensAPersistedConversationFromItsFile(t *testing.T) {
	r := startRig(t)
	r.fleet.session = "/sessions/plan.jsonl"
	c, id := finished(t, r, "Plan")
	c.mu.Lock()
	c.dead = true // the process ended after the run: a persisted conversation is reopened from its file
	c.mu.Unlock()
	text := r.must("Agent", obj{"prompt": "next", "description": "d", "subagent_type": "Plan", "resume": id})
	eq(t, strings.HasPrefix(text, "Agent resumed in background.\nAgent ID: "+id), true)
	c2 := r.fleet.wait(t, 2)[1]
	eq(t, c2.spec.ResumeFile, "/sessions/plan.jsonl")
	eq(t, c2.spec.Prompt, "next")
	eq(t, c2.spec.MaxTurns, 0) // a resumed turn has no turn limit
	eq(t, c2.spec.SystemPrompt, c.spec.SystemPrompt)
	c2.finish("from the file")
	waitStatus(t, r, id, statusCompleted)
	eq(t, r.app.mgr.get(id).Result, "from the file")
}

func TestAFailedResumeIsAnError(t *testing.T) {
	r := startRig(t)
	c, id := finished(t, r, "Plan")
	wait := foreground(r, "Agent", obj{"prompt": "more", "description": "d", "subagent_type": "Plan", "resume": id, "run_in_background": false})
	for i := 0; i < 500 && func() bool { c.mu.Lock(); defer c.mu.Unlock(); return len(c.resumed) == 0 }(); i++ {
		time.Sleep(5 * time.Millisecond)
	}
	c.mu.Lock()
	c.res = &childResult{Text: "half", Failure: "429 rate limited"}
	c.mu.Unlock()
	c.releaseNow()
	text, _ := wait()
	eq(t, text, "Agent failed: 429 rate limited\n\nPartial output before the failure:\nhalf")
}

func TestAResumeAtTheLimitIsQueued(t *testing.T) {
	r := startRig(t)
	c, id := finished(t, r, "Plan")
	r.app.mgr.mu.Lock()
	r.app.mgr.maxConcurrent = 1
	r.app.mgr.mu.Unlock()
	other := idFrom(t, r.must("Agent", obj{"prompt": "other", "description": "o", "subagent_type": "Plan"}))
	o := r.fleet.wait(t, 2)[1]
	text := r.must("Agent", obj{"prompt": "later", "description": "d", "subagent_type": "Plan", "resume": id})
	eq(t, text, "Agent queued in background.\nAgent ID: "+id+"\nType: Plan\nPosition: queued (max 1 concurrent)\n\nYou will be notified when this agent completes.\n"+
		"Use get_subagent_result to retrieve full results, or steer_subagent to send it messages.")
	eq(t, len(c.resumed), 0)
	o.finish("other done")
	waitStatus(t, r, other, statusCompleted)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && func() bool { c.mu.Lock(); defer c.mu.Unlock(); return len(c.resumed) == 0 }() {
		time.Sleep(5 * time.Millisecond)
	}
	eq(t, c.resumed, []string{"later"})
	c.finish("late answer")
	waitStatus(t, r, id, statusCompleted)
}

func TestAQueuedResumeCanBeStopped(t *testing.T) {
	r := startRig(t)
	c, id := finished(t, r, "Plan")
	r.app.mgr.mu.Lock()
	r.app.mgr.maxConcurrent = 1
	r.app.mgr.mu.Unlock()
	r.must("Agent", obj{"prompt": "other", "description": "o", "subagent_type": "Plan"})
	r.fleet.wait(t, 2)
	r.must("Agent", obj{"prompt": "later", "description": "d", "subagent_type": "Plan", "resume": id})
	rec := r.app.mgr.get(id)
	eq(t, r.app.mgr.abort(rec), true)
	waitStatus(t, r, id, statusStopped)
	eq(t, len(c.resumed), 0)
}

func TestInheritContextPutsTheParentConversationInFrontOfTheTask(t *testing.T) {
	r := startRig(t)
	r.branch = []any{
		obj{"type": "message", "message": obj{"role": "user", "content": "refactor the parser"}},
		obj{"type": "message", "message": obj{"role": "assistant", "content": []any{obj{"type": "text", "text": "I will start with the lexer."}, obj{"type": "toolCall", "name": "Agent"}}}},
	}
	want := "# Parent Conversation Context\nThe following is the conversation history from the parent session that spawned you.\n" +
		"Use this context to understand what has been discussed and decided so far.\n\n[User]: refactor the parser\n\n[Assistant]: I will start with the lexer.\n\n---\n# Your Task (below)\n"
	r.must("Agent", obj{"prompt": "continue where we stopped", "description": "d", "subagent_type": "Plan", "inherit_context": true})
	eq(t, r.fleet.wait(t, 1)[0].spec.Prompt, want+"continue where we stopped")
	// Without it, or with a branch that has nothing to say, the task goes alone.
	r.must("Agent", obj{"prompt": "alone", "description": "d", "subagent_type": "Plan"})
	eq(t, r.fleet.wait(t, 2)[1].spec.Prompt, "alone")
	r.branch = nil
	r.must("Agent", obj{"prompt": "empty branch", "description": "d", "subagent_type": "Plan", "inherit_context": true})
	eq(t, r.fleet.wait(t, 3)[2].spec.Prompt, "empty branch")
}

func TestTheAgentFileOutranksTheCallForInheritContextAndIsolated(t *testing.T) {
	r := sessionRig(t, "inherit_context: false\nisolated: false\nrun_in_background: true", "")
	r.branch = []any{obj{"type": "message", "message": obj{"role": "user", "content": "hello"}}}
	r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "custom", "inherit_context": true, "isolated": true})
	c := r.fleet.wait(t, 1)[0]
	eq(t, c.spec.Prompt, "p") // inherit_context: false in the file wins over the call
	eq(t, c.spec.NoExtensions, false)
	eq(t, c.spec.NoSkills, false)

	r = sessionRig(t, "inherit_context: true\nisolated: true\nrun_in_background: true", "")
	r.branch = []any{obj{"type": "message", "message": obj{"role": "user", "content": "hello"}}}
	r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "custom", "inherit_context": false, "isolated": false})
	c = r.fleet.wait(t, 1)[0]
	eq(t, strings.HasSuffix(c.spec.Prompt, "# Your Task (below)\np"), true)
	eq(t, c.spec.NoExtensions && c.spec.NoSkills, true)
}

func TestIsolatedMeansNoExtensionsAndNoSkills(t *testing.T) {
	r := startRig(t)
	r.registry = []any{obj{"name": "ptool", "description": "d", "parameters": obj{}, "sourceInfo": obj{"path": "/ext/ptool.ts"}}}
	r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "Plan", "isolated": true})
	c := r.fleet.wait(t, 1)[0]
	eq(t, c.spec.NoExtensions, true)
	eq(t, c.spec.NoSkills, true)
	eq(t, c.spec.AllowTools, []string{"read", "bash", "grep", "find", "ls"})
	eq(t, len(c.spec.ExcludeTools), 0)
	args := childArgs(c.spec)
	eq(t, slices.Contains(args, "--no-extensions") && slices.Contains(args, "--no-skills"), true)
}

func TestSkillsInTheAgentFile(t *testing.T) {
	r := sessionRig(t, "skills: alpha, beta", "")
	write(t, filepath.Join(r.app.cwd, ".pi", "skills", "alpha.md"), "ALPHA BODY\n")
	r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "custom"})
	c := r.fleet.wait(t, 1)[0]
	eq(t, c.spec.NoSkills, true) // preloaded into the prompt: the child must not load them again
	for _, want := range []string{"\n# Preloaded Skill: alpha\nALPHA BODY", "\n# Preloaded Skill: beta\n(Skill \"beta\" not found in .pi/skills/, .agents/skills/, or global skill locations)"} {
		eq(t, strings.Contains(c.spec.SystemPrompt, want), true)
	}
	r = sessionRig(t, "skills: false", "")
	r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "custom"})
	c = r.fleet.wait(t, 1)[0]
	eq(t, c.spec.NoSkills, true)
	eq(t, strings.Contains(c.spec.SystemPrompt, "Preloaded Skill"), false)
	r = sessionRig(t, "skills: true", "")
	r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "custom"})
	eq(t, r.fleet.wait(t, 1)[0].spec.NoSkills, false) // the child loads its own
}

func TestToolAndExtensionScopeReachTheChildAsFlags(t *testing.T) {
	registry := []any{
		obj{"name": "read", "description": "", "parameters": obj{}, "sourceInfo": obj{"path": "<builtin:read>", "source": "builtin"}},
		obj{"name": "ptool", "description": "", "parameters": obj{}, "sourceInfo": obj{"path": "/ext/ptool.ts"}},
		obj{"name": "ptool2", "description": "", "parameters": obj{}, "sourceInfo": obj{"path": "/ext/ptool.ts"}},
		obj{"name": "qtool", "description": "", "parameters": obj{}, "sourceInfo": obj{"path": "/ext/qtool.ts"}},
	}
	spawn := func(t *testing.T, frontmatter string) childSpec {
		t.Helper()
		r := sessionRig(t, frontmatter, "")
		r.registry = registry
		r.app.listExtensions = func(context.Context, string) ([]string, error) {
			return []string{"/ext/ptool.ts", "/ext/qtool.ts"}, nil
		}
		r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "custom"})
		return r.fleet.wait(t, 1)[0].spec
	}
	t.Run("a built-in list denies the others and keeps the extension tools", func(t *testing.T) {
		s := spawn(t, "tools: read, grep")
		eq(t, s.NoExtensions, false)
		eq(t, s.AllowTools == nil, true)
		eq(t, s.ExcludeTools, []string{"Agent", "SubagentWorkflow", "get_subagent_result", "steer_subagent", "bash", "edit", "write", "find", "ls"})
	})
	t.Run("disallowed_tools denies built-ins and extension tools by name", func(t *testing.T) {
		s := spawn(t, "disallowed_tools: bash, ptool")
		eq(t, slices.Contains(s.ExcludeTools, "bash") && slices.Contains(s.ExcludeTools, "ptool"), true)
		eq(t, slices.Contains(s.ExcludeTools, "ptool2"), false)
	})
	t.Run("extensions: false is a strict allowlist", func(t *testing.T) {
		s := spawn(t, "extensions: false\ntools: read")
		eq(t, s.NoExtensions, true)
		eq(t, s.AllowTools, []string{"read"})
		eq(t, len(s.ExcludeTools), 0)
	})
	t.Run("an extensions list loads exactly those", func(t *testing.T) {
		s := spawn(t, "extensions: ptool")
		eq(t, s.NoExtensions, true)
		eq(t, s.ExtensionPaths, []string{"/ext/ptool.ts"}) // the only file extension; PiG's built-ins are not named, so not kept
	})
	t.Run("exclude_extensions keeps the rest", func(t *testing.T) {
		s := spawn(t, "exclude_extensions: ptool")
		// the other file extension, and PiG's built-in ones, which an exclude list does not name
		eq(t, s.ExtensionPaths, []string{"/ext/qtool.ts", "builtin:llama.cpp", "builtin:codemode", "builtin:tool-search", "builtin:mcp"})
		eq(t, s.NoExtensions, true)
	})
	t.Run("an ext: selector mutes the extensions it does not name", func(t *testing.T) {
		s := spawn(t, "tools: read, ext:ptool/ptool2")
		eq(t, s.NoExtensions, false)
		for _, muted := range []string{"ptool", "qtool"} {
			eq(t, slices.Contains(s.ExcludeTools, muted), true)
		}
		eq(t, slices.Contains(s.ExcludeTools, "ptool2"), false)
	})
	t.Run("a scope that needs the list of extensions fails loudly without it", func(t *testing.T) {
		r := sessionRig(t, "extensions: ptool", "")
		r.registry = registry
		r.app.listExtensions = func(context.Context, string) ([]string, error) { return nil, errors.New("no pig") }
		_, failure := r.tool("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "custom"})
		eq(t, strings.Contains(failure, "no pig"), true)
		eq(t, r.fleet.count(), 0)
	})
}

func TestTheChildTrustsTheProjectWhenTheParentDoes(t *testing.T) {
	for _, trusted := range []bool{true, false} {
		r := startRig(t)
		r.trusted = trusted
		r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "Plan"})
		want := map[bool]string{true: "approve", false: "no-approve"}[trusted]
		eq(t, r.fleet.wait(t, 1)[0].spec.Trust, want)
	}
}

func TestSpawnRPCOptions(t *testing.T) {
	spawn := func(r *rig, options obj) {
		t.Helper()
		reply := r.request("subagents:rpc:spawn", obj{"type": "Plan", "prompt": "task", "options": options})
		eq(t, reply["success"], true)
	}
	t.Run("isolated starts the child without extensions or skills", func(t *testing.T) {
		r := startRig(t)
		spawn(r, obj{"description": "d", "isolated": true})
		c := r.fleet.wait(t, 1)[0]
		eq(t, c.spec.NoExtensions && c.spec.NoSkills, true)
	})
	t.Run("the agent file's isolated does not apply to an RPC spawn", func(t *testing.T) {
		// upstream: only the Agent tool resolves the file's isolated; the spawn RPC passes options.isolated alone.
		r := sessionRig(t, "isolated: true\nextensions: true", "")
		reply := r.request("subagents:rpc:spawn", obj{"type": "custom", "prompt": "task", "options": obj{"description": "d"}})
		eq(t, reply["success"], true)
		eq(t, r.fleet.wait(t, 1)[0].spec.NoExtensions, false)
	})
	t.Run("inheritContext puts the parent conversation in front of the task", func(t *testing.T) {
		r := startRig(t)
		r.branch = []any{obj{"type": "message", "message": obj{"role": "user", "content": "hello"}}}
		spawn(r, obj{"description": "d", "inheritContext": true})
		eq(t, r.fleet.wait(t, 1)[0].spec.Prompt, buildParentContext([]map[string]any{{"type": "message", "message": obj{"role": "user", "content": "hello"}}})+"task")
	})
	t.Run("bypassQueue starts at the limit", func(t *testing.T) {
		r := startRig(t)
		r.app.mgr.mu.Lock()
		r.app.mgr.maxConcurrent = 1
		r.app.mgr.mu.Unlock()
		spawn(r, obj{"description": "one"})
		r.fleet.wait(t, 1)
		spawn(r, obj{"description": "two", "bypassQueue": true})
		r.fleet.wait(t, 2) // started at once, past the limit
		spawn(r, obj{"description": "three"})
		time.Sleep(100 * time.Millisecond)
		eq(t, r.fleet.count(), 2) // the next one without it waits
		r.app.mgr.mu.Lock()
		eq(t, r.app.mgr.running, 2) // the bypassing spawn still counts against the limit
		r.app.mgr.mu.Unlock()
	})
	t.Run("what only this extension may set is dropped, as the original drops it", func(t *testing.T) {
		r := startRig(t)
		spawn(r, obj{"description": "d", "resumeSessionFile": "/etc/passwd", "reclaim": obj{"handle": "main"}, "blocking": true})
		c := r.fleet.wait(t, 1)[0]
		eq(t, c.spec.ResumeFile, "") // a forged value would name a file to open and replay
		recs := r.app.mgr.list()
		eq(t, recs[0].Handle, "plan") // nor can a caller take a handle
	})
}

func TestAnEvictedAgentCanBeReopenedByItsHandle(t *testing.T) {
	r := startRig(t)
	r.fleet.session = "/sessions/explore.jsonl"
	c, id := finished(t, r, "Explore")
	_ = c
	rec := r.app.mgr.get(id)
	eq(t, rec.Handle, "explore")
	r.app.mgr.removeRecord(id)
	eq(t, r.app.mgr.get(id) == nil, true)
	live, dead := r.app.mgr.resolveMention("EXPLORE")
	eq(t, live == nil, true)
	eq(t, dead.SessionFile, "/sessions/explore.jsonl")
	eq(t, dead.ID, id)
	// A new Explore does not shadow the evicted one: its name is still held.
	again, _ := spawnOne(t, r, "Explore", nil)
	_ = again
	for _, rec := range r.app.mgr.list() {
		eq(t, rec.Handle, "explore-2")
	}
	// Reopening: the file, the type's current definition and the handle the user typed.
	reopened := r.app.mgr.spawnWith("Explore", "d", "", childSpec{Prompt: "carry on", Persist: true}, spawnOptions{ResumeFile: dead.SessionFile, Reclaim: &reclaim{Handle: dead.Handle, Alias: dead.Alias}, Background: true})
	eq(t, reopened.Handle, "explore")
	cs := r.fleet.wait(t, 3)
	eq(t, cs[2].spec.ResumeFile, "/sessions/explore.jsonl")
	// Once a live agent answers the name, the tombstone is not offered.
	live, dead = r.app.mgr.resolveMention("explore")
	eq(t, live != nil && dead == nil, true)
}

func TestTombstonesNeedAHandleAndASessionFile(t *testing.T) {
	r := startRig(t)
	c, id := finished(t, r, "Plan") // the fake child reports no session file: an in-memory conversation
	_ = c
	r.app.mgr.removeRecord(id)
	live, dead := r.app.mgr.resolveMention("plan")
	eq(t, live == nil && dead == nil, true)
	eq(t, len(r.app.mgr.listTombstones()), 0)
}

func TestTombstonesAreBounded(t *testing.T) {
	m := newManager(nil, nil)
	for i := range maxTombstones + 5 {
		h := "agent-" + strings.Repeat("x", 1) + string(rune('a'+i%26)) + string(rune('a'+i/26))
		m.tombstoneLocked(&agentRecord{Handle: h, SessionFile: "/s/" + h, CompletedAt: time.Unix(int64(i), 0)})
	}
	eq(t, len(m.tombstones), maxTombstones)
	list := m.listTombstones()
	eq(t, list[0].CompletedAt, time.Unix(maxTombstones+4, 0)) // newest first; the oldest went
	eq(t, list[len(list)-1].CompletedAt, time.Unix(5, 0))
	m.dropTombstone(list[0].Handle)
	eq(t, len(m.tombstones), maxTombstones-1)
}

func TestCleanupRemovesFinishedAgentsAfterTenMinutes(t *testing.T) {
	r := startRig(t)
	c, id := finished(t, r, "Plan")
	run := idFrom(t, r.must("Agent", obj{"prompt": "running", "description": "d", "subagent_type": "Plan"}))
	r.fleet.wait(t, 2)
	r.app.mgr.cleanup(time.Now().Add(recordLifetime - time.Minute))
	eq(t, r.app.mgr.get(id) != nil, true) // not yet
	r.app.mgr.cleanup(time.Now().Add(recordLifetime + time.Minute))
	eq(t, r.app.mgr.get(id) == nil, true)
	eq(t, r.app.mgr.get(run) != nil, true) // a running agent is never cleaned up
	c.mu.Lock()
	eq(t, c.closed, true) // the process that held its conversation ended with it
	c.mu.Unlock()
}

func TestRememberAgentsSettingIsRead(t *testing.T) {
	eq(t, sanitizeSettings(map[string]any{"rememberAgents": false}).RememberAgents != nil && !*sanitizeSettings(map[string]any{"rememberAgents": false}).RememberAgents, true)
	eq(t, sanitizeSettings(map[string]any{"rememberAgents": "no"}).RememberAgents == nil, true) // a value of the wrong type is ignored
	eq(t, sanitizeSettings(map[string]any{}).RememberAgents == nil, true)
	dir := t.TempDir()
	write(t, filepath.Join(dir, ".pi", "subagents.json"), `{"rememberAgents": false}`)
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	write(t, filepath.Join(agentDir(), "subagents.json"), `{"rememberAgents": true}`)
	st := loadSettings(dir)
	eq(t, st.RememberAgents != nil && !*st.RememberAgents, true) // the project file overrides the global one
}

func TestSessionDirResolution(t *testing.T) {
	home, _ := os.UserHomeDir()
	eq(t, resolveConfiguredSessionDir("", "/cwd"), "")
	eq(t, resolveConfiguredSessionDir("~", "/cwd"), home)
	eq(t, resolveConfiguredSessionDir("~/s", "/cwd"), filepath.Join(home, "s"))
	eq(t, resolveConfiguredSessionDir("/abs/s", "/cwd"), "/abs/s")
	eq(t, resolveConfiguredSessionDir("rel/s", "/cwd"), "/cwd/rel/s")
}
