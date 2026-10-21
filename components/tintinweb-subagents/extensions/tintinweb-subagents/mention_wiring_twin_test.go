package tintinweb_subagents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Twins of test/agent-mention-wiring.test.ts and test/mention-start-notification.test.ts: the `input` hook booted
// through the real extension on a fake host, with a fleet of scripted children for the original's mocked runAgent and
// resumeAgent. A hook that waits for nothing announces what an agent's startup came to afterwards, so the cases wait
// for the notification where the original reads it after the hook returned.

var handled = obj{"action": "handled"}
var cont = obj{"action": "continue"}

func TestMentionWiringTwins(t *testing.T) {
	const f = "agent-mention-wiring"
	direct := obj{"agentMentions": "direct"}

	tw(t, f, "steers it, announces it, and spends no main-model turn", func(t *testing.T) {
		m := startMention(t, mentionBoot{})
		m.spawnBg("Explore")
		c := m.fleet.wait(t, 1)[0]
		eq(t, m.send("@explore also check the RPC path"), handled)
		waitFor(t, "steered", func() bool { return len(c.steers()) == 1 })
		eq(t, c.steers(), []string{"also check the RPC path"})
		m.waitNotify(t, "Sent to @explore", "info")
		eq(t, len(m.messages()), 0)
	})
	tw(t, f, "un-consumes the result so the agent's reply is still relayed", func(t *testing.T) {
		m := startMention(t, mentionBoot{})
		rec := m.spawnBg("Explore")
		m.fleet.wait(t, 1)
		m.app.mgr.mu.Lock()
		rec.Consumed = true
		m.app.mgr.mu.Unlock()
		m.send("@explore keep going")
		m.app.mgr.mu.Lock()
		defer m.app.mgr.mu.Unlock()
		eq(t, rec.Consumed, false)
	})
	tw(t, f, "addresses same-type siblings by their numbered handles", func(t *testing.T) {
		m := startMention(t, mentionBoot{})
		m.spawnBg("Explore")
		m.spawnBg("Explore")
		kids := m.fleet.wait(t, 2)
		m.send("@explore-2 you take the second half")
		waitFor(t, "steered", func() bool { return len(kids[1].steers()) == 1 })
		eq(t, kids[1].steers(), []string{"you take the second half"})
		eq(t, len(kids[0].steers()), 0)
	})

	// finished starts an agent and lets it finish, leaving a resumable record.
	finished := func(m *mentionRig, typ string) (*agentRecord, *fakeChild) {
		rec := m.spawnBg(typ)
		return rec, m.finish(m.t, rec, 0, "first answer")
	}

	tw(t, f, "resumes it from its session in the background", func(t *testing.T) {
		m := startMention(t, mentionBoot{})
		_, c := finished(m, "Explore")
		eq(t, m.send("@explore anything else?"), handled)
		waitFor(t, "resumed", func() bool { return len(c.resumes()) == 1 })
		eq(t, c.resumes(), []string{"anything else?"})
		m.waitNotify(t, "Resuming @explore", "info")
	})
	tskip(t, f, "honours the agent's output_transcript: false when resuming",
		"output transcripts (.output files) are not ported and refused; a resume never opens one, so there is no transcript for this flag to keep closed")
	tw(t, f, "does not attribute the new answer to the tool call that spawned it", func(t *testing.T) {
		m := startMention(t, mentionBoot{})
		rec, c := finished(m, "Explore")
		m.send("@explore anything else?")
		waitFor(t, "resumed", func() bool { return len(c.resumes()) == 1 })
		c.finish("second answer")
		<-m.app.mgr.doneOf(rec)
		waitFor(t, "notification", func() bool {
			for _, s := range m.sentMessages() {
				if strings.Contains(s, "second answer") {
					return true
				}
			}
			return false
		})
		for _, s := range m.sentMessages() {
			if strings.Contains(s, "second answer") {
				eq(t, strings.Contains(s, "<tool-use-id>"), false)
			}
		}
	})
	tw(t, f, "relays the resumed answer through the ordinary completion notification", func(t *testing.T) {
		m := startMention(t, mentionBoot{})
		rec, c := finished(m, "Explore")
		m.send("@explore anything else?")
		waitFor(t, "resumed", func() bool { return len(c.resumes()) == 1 })
		c.finish("second answer")
		<-m.app.mgr.doneOf(rec)
		waitFor(t, "notification", func() bool {
			for _, call := range m.CallsTo("sendMessage") {
				msg, _ := call.Args["message"].(map[string]any)
				opts, _ := call.Args["options"].(map[string]any)
				if msg["customType"] == "subagent-notification" && strings.Contains(msg["content"].(string), "second answer") && opts["triggerTurn"] == true {
					return true
				}
			}
			return false
		})
	})

	// The suggestion provider.
	tw(t, f, "registers exactly once, however often session_start fires", func(t *testing.T) {
		m := startMention(t, mentionBoot{})
		waitFor(t, "first registration", func() bool { return len(m.CallsTo("ui.addAutocompleteProvider")) == 1 })
		m.Fire("session_start", obj{"reason": "startup"})
		time.Sleep(100 * time.Millisecond)
		eq(t, len(m.CallsTo("ui.addAutocompleteProvider")), 1)
	})
	tw(t, f, "stays out of non-TUI modes, which have no editor to complete into", func(t *testing.T) {
		m := startMention(t, mentionBoot{mode: "rpc"})
		time.Sleep(100 * time.Millisecond)
		eq(t, len(m.CallsTo("ui.addAutocompleteProvider")), 0)
	})
	tw(t, f, "hands pi a provider that answers a live handle", func(t *testing.T) {
		m := startMention(t, mentionBoot{})
		m.spawnBg("Explore")
		m.fleet.wait(t, 1)
		got, _ := m.suggestions(t, "@ex")
		eq(t, got, []string{"@explore"})
	})

	// Resolving which agent a handle means.
	tw(t, f, "matches the handle case-insensitively", func(t *testing.T) {
		m := startMention(t, mentionBoot{})
		m.spawnBg("Explore")
		c := m.fleet.wait(t, 1)[0]
		eq(t, m.send("@EXPLORE shout"), handled)
		waitFor(t, "steered", func() bool { return len(c.steers()) == 1 })
		eq(t, c.steers(), []string{"shout"})
	})
	tw(t, f, "accepts the raw agent id, which the README offers as a fallback", func(t *testing.T) {
		m := startMention(t, mentionBoot{})
		rec := m.spawnBg("Explore")
		c := m.fleet.wait(t, 1)[0]
		eq(t, m.send("@"+rec.ID+" by id"), handled)
		waitFor(t, "steered", func() bool { return len(c.steers()) == 1 })
		eq(t, c.steers(), []string{"by id"})
	})
	tw(t, f, "queues the message for an agent still waiting on a concurrency slot", func(t *testing.T) {
		m := startMention(t, mentionBoot{settings: obj{"maxConcurrent": 1}})
		m.spawnBg("Explore")
		queued := m.spawnBg("Explore")
		m.fleet.wait(t, 1)
		eq(t, m.status(queued), statusQueued)
		eq(t, m.send("@explore-2 wait for me"), handled)
		waitFor(t, "parked", func() bool {
			m.app.mgr.mu.Lock()
			defer m.app.mgr.mu.Unlock()
			return len(queued.PendingSteers) == 1
		})
		m.app.mgr.mu.Lock()
		defer m.app.mgr.mu.Unlock()
		eq(t, queued.PendingSteers, []string{"wait for me"})
	})
	tskip(t, f, "never reaches into a nested child, and starts a top-level agent instead",
		"nested delegation is not part of this port yet, so no record has an owner to hide; the nested slice adds the ownership filter and this case beside it")
	tw(t, f, "starts a fresh agent once the old record has been evicted", func(t *testing.T) {
		m := startMention(t, mentionBoot{settings: direct})
		rec, _ := finished(m, "Explore")
		m.app.mgr.mu.Lock()
		rec.Consumed = true
		m.app.mgr.mu.Unlock()
		m.Fire("session_before_switch", obj{})
		eq(t, m.app.mgr.get(rec.ID) == nil, true)
		m.send("@explore start over")
		kids := m.fleet.wait(t, 2)
		eq(t, kids[1].spec.Prompt, "start over")
		eq(t, len(kids[0].resumes()), 0)
	})

	// Mentioning an agent that has never run.
	tw(t, f, "starts one, using the message as its prompt", func(t *testing.T) {
		m := startMention(t, mentionBoot{settings: direct})
		eq(t, m.send("@explore find every retry marker"), handled)
		c := m.fleet.wait(t, 1)[0]
		eq(t, c.spec.Prompt, "find every retry marker")
		m.waitNotify(t, "Started @explore", "info")
	})
	tw(t, f, "leaves model, thinking and max turns to the agent's own config", func(t *testing.T) {
		m := startMention(t, mentionBoot{settings: direct})
		m.send("@explore go")
		c := m.fleet.wait(t, 1)[0]
		eq(t, [3]any{c.spec.Model, c.spec.Thinking, c.spec.MaxTurns}, [3]any{"", "", 0})
	})
	tskip(t, f, "shows the turn limit it will actually be held to (#181)",
		"asserts the widget's row text; the widget is a TUI component the Go SDK cannot draw and is not ported (README). The limit itself is the agent's own, asserted by 'leaves model, thinking and max turns to the agent's own config' and turnlimit_test.go")
	tskip(t, f, "tracks its tool activity, so the widget shows what it is doing (#181)",
		"asserts the activity-tracker callbacks of the in-process runner; this port reads a child's activity from its event stream, and the widget that shows it is not ported")
	tw(t, f, "runs it in the background so the prompt is not blocked", func(t *testing.T) {
		m := startMention(t, mentionBoot{settings: direct})
		m.send("@explore go")
		m.fleet.wait(t, 1)
		recs := m.app.mgr.list()
		eq(t, len(recs), 1)
		m.app.mgr.mu.Lock()
		defer m.app.mgr.mu.Unlock()
		eq(t, [2]any{recs[0].Background, recs[0].Description}, [2]any{true, "go"})
	})
	tw(t, f, "messages the running agent rather than starting a second one", func(t *testing.T) {
		m := startMention(t, mentionBoot{settings: direct})
		m.send("@explore first task")
		c := m.fleet.wait(t, 1)[0]
		m.send("@explore actually do this instead")
		waitFor(t, "steered", func() bool { return len(c.steers()) == 1 })
		eq(t, c.steers(), []string{"actually do this instead"})
		eq(t, m.fleet.count(), 1)
	})
	tw(t, f, "reports a failed start instead of silently doing nothing", func(t *testing.T) {
		m := startMention(t, mentionBoot{settings: direct})
		m.fleet.setFail(errBoom)
		eq(t, m.send("@explore go"), handled)
		m.waitNotifyContaining(t, "Could not start @explore", "error")
	})

	// Letting a clone of the conversation start the agent.
	tw(t, f, "claims the turn, so nothing about the mention reaches the chat", func(t *testing.T) {
		m := startMention(t, mentionBoot{})
		eq(t, m.send("@explore find the flaky test"), handled)
		waitFor(t, "clone ran", func() bool { m.mu.Lock(); defer m.mu.Unlock(); return len(m.cloneRan) == 1 })
		time.Sleep(50 * time.Millisecond)
		eq(t, len(m.messages()), 0)
		eq(t, m.fleet.count(), 0) // the scripted clone spawned nothing itself, and nothing fell back
	})
	tw(t, f, "hands the clone the resolved type, the message and the real Agent tool", func(t *testing.T) {
		m := startMention(t, mentionBoot{})
		m.send("@plan sketch the migration")
		waitFor(t, "clone ran", func() bool { m.mu.Lock(); defer m.mu.Unlock(); return len(m.cloneRan) == 1 })
		m.mu.Lock()
		o := m.cloneRan[0]
		m.mu.Unlock()
		eq(t, [3]any{o.Type, o.Message, o.Tool.Name}, [3]any{"Plan", "sketch the migration", "Agent"})
		// The tool is the registered one: running it is an ordinary top-level spawn.
		text, isErr := o.Tool.Run(obj{"subagent_type": "Plan", "prompt": "p", "description": "d", "run_in_background": true})
		eq(t, isErr, false)
		eq(t, strings.Contains(text, "Agent ID: "), true)
		eq(t, m.fleet.wait(t, 1)[0].spec.Prompt, "p")
	})
	tw(t, f, "does not block the prompt on the clone's turn", func(t *testing.T) {
		m := startMention(t, mentionBoot{})
		gate := make(chan struct{})
		m.mu.Lock()
		m.cloneGo = gate
		m.mu.Unlock()
		eq(t, m.send("@explore find the flaky test"), handled) // returned while the clone still waits
		close(gate)
	})
	tw(t, f, "says the agent is being prompted, not started — nothing runs yet", func(t *testing.T) {
		m := startMention(t, mentionBoot{})
		m.send("@explore find the flaky test")
		eq(t, m.hasNotify("Prompting @explore…", "info"), true)
	})
	tw(t, f, "starts the agent directly when the clone cannot", func(t *testing.T) {
		m := startMention(t, mentionBoot{})
		m.cloneRes = mentionCloneResult{Error: "no session file"}
		m.send("@explore find the flaky test")
		m.fleet.wait(t, 1)
		m.waitNotify(t, "Started @explore directly — no session file", "warning")
	})
	tw(t, f, "reports a fallback that also fails rather than going quiet", func(t *testing.T) {
		m := startMention(t, mentionBoot{})
		m.fleet.setFail(errBoom)
		m.cloneRes = mentionCloneResult{Error: "no session file"}
		m.send("@explore find the flaky test")
		m.waitNotifyContaining(t, "Could not start @explore", "error")
	})
	tw(t, f, "still steers a running agent directly, without cloning anything", func(t *testing.T) {
		m := startMention(t, mentionBoot{})
		m.spawnBg("Explore")
		c := m.fleet.wait(t, 1)[0]
		eq(t, m.send("@explore also check the RPC path"), handled)
		waitFor(t, "steered", func() bool { return len(c.steers()) == 1 })
		eq(t, c.steers(), []string{"also check the RPC path"})
		eq(t, len(m.cloneRan), 0)
	})
	tw(t, f, "still resumes a finished agent directly, without cloning anything", func(t *testing.T) {
		m := startMention(t, mentionBoot{})
		_, c := finished(m, "Explore")
		eq(t, m.send("@explore anything else?"), handled)
		waitFor(t, "resumed", func() bool { return len(c.resumes()) == 1 })
		eq(t, c.resumes(), []string{"anything else?"})
		eq(t, len(m.cloneRan), 0)
	})
	tw(t, f, "does not clone for a handle that names no agent", func(t *testing.T) {
		m := startMention(t, mentionBoot{})
		eq(t, m.send("@nosuchagent hello"), cont)
		eq(t, len(m.cloneRan), 0)
	})
	tw(t, f, "works headlessly, where the visible-turn version never could", func(t *testing.T) {
		m := startMention(t, mentionBoot{mode: "print"})
		eq(t, m.send("@explore find the flaky test"), handled)
		waitFor(t, "clone ran", func() bool { m.mu.Lock(); defer m.mu.Unlock(); return len(m.cloneRan) == 1 })
	})
	tw(t, f, "leaves a running agent alone headlessly rather than starting a rival", func(t *testing.T) {
		m := startMention(t, mentionBoot{mode: "print"})
		m.spawnBg("Explore")
		c := m.fleet.wait(t, 1)[0]
		eq(t, m.send("@explore also check the RPC path"), cont)
		time.Sleep(50 * time.Millisecond)
		eq(t, len(c.steers()), 0)
		eq(t, len(m.cloneRan), 0)
	})
	tw(t, f, "clones nothing when mentions are off", func(t *testing.T) {
		m := startMention(t, mentionBoot{settings: obj{"agentMentions": "off"}})
		eq(t, m.send("@explore find the flaky test"), cont)
		eq(t, len(m.cloneRan), 0)
	})
	tw(t, f, "starts the agent here instead when the mode is direct", func(t *testing.T) {
		m := startMention(t, mentionBoot{settings: direct})
		eq(t, m.send("@explore find the flaky test"), handled)
		m.fleet.wait(t, 1)
		eq(t, len(m.cloneRan), 0)
	})

	// Input that is not a mention.
	tw(t, f, "passes an unknown handle to the main model rather than eating it", func(t *testing.T) {
		eq(t, startMention(t, mentionBoot{}).send("@nosuchagent hello"), cont)
	})
	tw(t, f, "passes a bare handle to the main model", func(t *testing.T) {
		m := startMention(t, mentionBoot{})
		m.spawnBg("Explore")
		c := m.fleet.wait(t, 1)[0]
		eq(t, m.send("@explore"), cont)
		eq(t, len(c.steers()), 0)
	})
	tw(t, f, "leaves a leading file attachment alone", func(t *testing.T) {
		m := startMention(t, mentionBoot{})
		m.spawnBg("Explore")
		eq(t, m.send("@src/index.ts summarize this"), cont)
	})
	tw(t, f, "ignores input the extension layer submitted", func(t *testing.T) {
		m := startMention(t, mentionBoot{})
		m.spawnBg("Explore")
		c := m.fleet.wait(t, 1)[0]
		eq(t, m.input("@explore relayed text", "extension", nil), cont)
		time.Sleep(50 * time.Millisecond)
		eq(t, len(c.steers()), 0)
	})
	tw(t, f, "leaves a headless prompt to the main model", func(t *testing.T) {
		m := startMention(t, mentionBoot{mode: "print", settings: direct})
		eq(t, m.send("@explore go"), cont)
		eq(t, m.fleet.count(), 0)
	})
	tw(t, f, "leaves an RPC-driven prompt to the main model", func(t *testing.T) {
		m := startMention(t, mentionBoot{mode: "rpc", settings: direct})
		m.spawnBg("Explore")
		c := m.fleet.wait(t, 1)[0]
		eq(t, m.input("@explore also check this", "rpc", nil), cont)
		time.Sleep(50 * time.Millisecond)
		eq(t, len(c.steers()), 0)
	})
	tw(t, f, "falls through entirely when mentions are disabled", func(t *testing.T) {
		m := startMention(t, mentionBoot{settings: obj{"agentMentions": false}})
		m.spawnBg("Explore")
		c := m.fleet.wait(t, 1)[0]
		eq(t, m.send("@explore do this"), cont)
		time.Sleep(50 * time.Millisecond)
		eq(t, len(c.steers()), 0)
	})
	tw(t, f, "disabled also blocks starting an agent, not just messaging one", func(t *testing.T) {
		m := startMention(t, mentionBoot{settings: obj{"agentMentions": false}})
		eq(t, m.send("@explore go"), cont)
		eq(t, m.fleet.count(), 0)
	})
	tw(t, f, "disabled also blocks resuming a finished agent", func(t *testing.T) {
		m := startMention(t, mentionBoot{settings: obj{"agentMentions": false}})
		_, c := finished(m, "Explore")
		eq(t, m.send("@explore anything else?"), cont)
		time.Sleep(50 * time.Millisecond)
		eq(t, len(c.resumes()), 0)
	})
	tw(t, f, "the suggestion popup goes quiet too, so @ means only 'attach a file'", func(t *testing.T) {
		m := startMention(t, mentionBoot{settings: obj{"agentMentions": false}})
		m.spawnBg("Explore")
		m.fleet.wait(t, 1)
		m.mu.Lock()
		m.below = func(op string) any {
			return obj{"items": []any{obj{"value": "@src/x.ts", "label": "src/x.ts"}}, "prefix": "@ex"}
		}
		m.mu.Unlock()
		got, prefix := m.suggestions(t, "@ex")
		eq(t, got, []string{"@src/x.ts"})
		eq(t, prefix, "@ex")
	})

	// @main.
	tw(t, f, "strips the prefix and sends the rest to the main model", func(t *testing.T) {
		eq(t, startMention(t, mentionBoot{}).send("@main @explore is not a mention"), obj{"action": "transform", "text": "@explore is not a mention"})
	})
	tw(t, f, "carries attachments through with the text", func(t *testing.T) {
		images := []any{obj{"type": "image", "data": "x"}}
		got := startMention(t, mentionBoot{}).input("@main look at this", "interactive", obj{"images": images})
		eq(t, got, obj{"action": "transform", "text": "look at this", "images": images})
	})
	tw(t, f, "never starts an agent, even when a type would slug to main", func(t *testing.T) {
		m := startMention(t, mentionBoot{})
		got := m.send("@main do the thing")
		eq(t, got["action"], "transform")
		eq(t, m.fleet.count(), 0)
		eq(t, len(m.messages()), 0)
	})
	tw(t, f, "leaves a bare @main to the main model untouched", func(t *testing.T) {
		eq(t, startMention(t, mentionBoot{}).send("@main"), cont)
	})

	// @agent-<type>.
	tw(t, f, "starts the agent the unprefixed handle would have", func(t *testing.T) {
		m := startMention(t, mentionBoot{settings: direct})
		eq(t, m.send("@agent-explore find the flaky test"), handled)
		eq(t, m.fleet.wait(t, 1)[0].spec.Prompt, "find the flaky test")
		eq(t, m.app.mgr.list()[0].Type, "Explore")
	})
	tw(t, f, "reaches a running agent, not a second copy of it", func(t *testing.T) {
		m := startMention(t, mentionBoot{})
		m.spawnBg("Explore")
		c := m.fleet.wait(t, 1)[0]
		m.send("@agent-explore also check the RPC path")
		waitFor(t, "steered", func() bool { return len(c.steers()) == 1 })
		eq(t, c.steers(), []string{"also check the RPC path"})
	})
	tw(t, f, "prefers an agent literally named agent-<x> over the unwrapped spelling", func(t *testing.T) {
		m := startMention(t, mentionBoot{})
		m.spawnBg("Explore") // plain Explore: @explore
		m.must("Agent", obj{"prompt": "go", "description": "named", "subagent_type": "Plan", "name": "agent-explore", "run_in_background": true})
		kids := m.fleet.wait(t, 2)
		m.send("@agent-explore over here")
		waitFor(t, "steered", func() bool { return len(kids[1].steers()) == 1 })
		eq(t, kids[1].steers(), []string{"over here"})
		eq(t, len(kids[0].steers()), 0)
	})
	tw(t, f, "still falls through when nothing answers either spelling", func(t *testing.T) {
		eq(t, startMention(t, mentionBoot{}).send("@agent-nosuchtype hello"), cont)
	})

	// Resuming an evicted agent by name.
	sessionFile := func(t *testing.T, m *mentionRig) string {
		p := filepath.Join(m.cwd, "explore-session.jsonl")
		writeFile(t, p, "")
		m.fleet.mu.Lock()
		m.fleet.session = p
		m.fleet.mu.Unlock()
		return p
	}
	// evict ages the record past the cutoff and lets the real cleanup evict it.
	evict := func(t *testing.T, m *mentionRig, rec *agentRecord) {
		m.app.mgr.mu.Lock()
		rec.CompletedAt = time.Now().Add(-11 * time.Minute)
		m.app.mgr.mu.Unlock()
		m.app.mgr.cleanup(time.Now())
		eq(t, m.app.mgr.get(rec.ID) == nil, true)
	}
	evicted := func(t *testing.T, m *mentionRig, typ string) (file string, c *fakeChild) {
		file = sessionFile(t, m)
		rec := m.spawnBg(typ)
		c = m.finish(t, rec, 0, "first answer")
		evict(t, m, rec)
		return file, c
	}
	// newest is the record of the latest spawn.
	newest := func(m *mentionRig) *agentRecord { l := m.app.mgr.list(); return l[len(l)-1] }

	tw(t, f, "reopens the conversation instead of starting a fresh agent", func(t *testing.T) {
		m := startMention(t, mentionBoot{})
		file, _ := evicted(t, m, "Explore")
		eq(t, m.send("@explore anything else?"), handled)
		kids := m.fleet.wait(t, 2)
		eq(t, [2]string{kids[1].spec.Prompt, kids[1].spec.ResumeFile}, [2]string{"anything else?", file})
		m.waitNotify(t, "Resuming @explore", "info")
	})
	tw(t, f, "hands the resumed agent the handle back instead of numbering it", func(t *testing.T) {
		m := startMention(t, mentionBoot{})
		evicted(t, m, "Explore")
		m.send("@explore anything else?")
		m.fleet.wait(t, 2)
		eq(t, newest(m).Handle, "explore")
	})
	tw(t, f, "stops resolving to the tombstone once the resume has taken the name", func(t *testing.T) {
		m := startMention(t, mentionBoot{})
		evicted(t, m, "Explore")
		m.send("@explore anything else?")
		kids := m.fleet.wait(t, 2)
		m.waitNotify(t, "Resuming @explore", "info")
		m.send("@explore and one more thing")
		waitFor(t, "steered", func() bool { return len(kids[1].steers()) == 1 })
		eq(t, kids[1].steers(), []string{"and one more thing"})
		eq(t, m.fleet.count(), 2) // reopened once
		m.waitNotify(t, "Sent to @explore", "info")
	})
	tw(t, f, "gives a named agent its alias back too", func(t *testing.T) {
		m := startMention(t, mentionBoot{})
		file := sessionFile(t, m)
		_ = file
		text := m.must("Agent", obj{"prompt": "audit", "description": "audit the auth flow", "subagent_type": "Explore", "name": "auth-audit", "run_in_background": true})
		rec := m.app.mgr.get(idFrom(t, text))
		m.finish(t, rec, 0, "first answer")
		evict(t, m, rec)
		m.send("@auth-audit anything else?")
		m.fleet.wait(t, 2)
		got := newest(m)
		eq(t, [2]string{got.Handle, got.Alias}, [2]string{"explore", "auth-audit"})
	})
	tw(t, f, "refuses to reopen a conversation under a substitute agent", func(t *testing.T) {
		m := startMention(t, mentionBoot{agentFiles: map[string]string{"scout": "---\ndescription: scouts\n---\nbody"}})
		sessionFile(t, m)
		rec := m.spawnBg("scout")
		m.finish(t, rec, 0, "first answer")
		evict(t, m, rec)
		writeFile(t, filepath.Join(m.cwd, ".pi", "agents", "scout.md"), "---\ndescription: scouts\nenabled: false\n---\nbody")
		eq(t, m.send("@scout anything else?"), handled)
		eq(t, m.fleet.count(), 1)
		eq(t, m.hasNotify("Could not resume @scout — the scout agent is no longer available.", "warning"), true)
	})
	tw(t, f, "resumes again once the agent is re-enabled", func(t *testing.T) {
		m := startMention(t, mentionBoot{agentFiles: map[string]string{"scout": "---\ndescription: scouts\n---\nbody"}})
		file := sessionFile(t, m)
		rec := m.spawnBg("scout")
		m.finish(t, rec, 0, "first answer")
		evict(t, m, rec)
		agent := filepath.Join(m.cwd, ".pi", "agents", "scout.md")
		writeFile(t, agent, "---\ndescription: scouts\nenabled: false\n---\nbody")
		m.send("@scout hi")
		writeFile(t, agent, "---\ndescription: scouts\n---\nbody")
		m.send("@scout hi again")
		kids := m.fleet.wait(t, 2)
		eq(t, [3]string{kids[1].spec.Prompt, kids[1].spec.ResumeFile, m.app.mgr.list()[0].Type}, [3]string{"hi again", file, "scout"})
		m.waitNotify(t, "Resuming @scout", "info")
	})
	tw(t, f, "keeps the agent resolvable when the resume itself fails", func(t *testing.T) {
		m := startMention(t, mentionBoot{})
		file, _ := evicted(t, m, "Explore")
		m.fleet.setFail(errBoom) // stands in for any spawn-time failure
		m.send("@explore anything else?")
		m.waitNotifyContaining(t, "Could not resume @explore", "warning")
		// Still reachable: try again and it reopens the same conversation.
		m.fleet.setFail(nil)
		m.send("@explore try again")
		kids := m.fleet.wait(t, 2)
		eq(t, [2]string{kids[len(kids)-1].spec.Prompt, kids[len(kids)-1].spec.ResumeFile}, [2]string{"try again", file})
	})
	tw(t, f, "keeps the original description rather than relabelling from the message", func(t *testing.T) {
		m := startMention(t, mentionBoot{})
		evicted(t, m, "Explore")
		m.send("@explore anything else?")
		m.fleet.wait(t, 2)
		eq(t, newest(m).Description, "find flaky tests")
	})
	tw(t, f, "does not let the tools steer or read an agent that is gone", func(t *testing.T) {
		m := startMention(t, mentionBoot{})
		evicted(t, m, "Explore")
		steered := m.must("steer_subagent", obj{"agent_id": "explore", "message": "hi"})
		read := m.must("get_subagent_result", obj{"agent_id": "explore"})
		eq(t, strings.Contains(steered, "Agent not found"), true)
		eq(t, strings.Contains(read, "Agent not found"), true)
	})
	tw(t, f, "reports a session that is gone, rather than starting something else", func(t *testing.T) {
		m := startMention(t, mentionBoot{})
		file, _ := evicted(t, m, "Explore")
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
		eq(t, m.send("@explore anything else?"), handled)
		eq(t, m.fleet.count(), 1)
		eq(t, m.hasNotify("Could not resume @explore — its session is gone.", "warning"), true)
	})
	tw(t, f, "forgets an unopenable session so the next mention starts fresh", func(t *testing.T) {
		m := startMention(t, mentionBoot{settings: direct})
		file, _ := evicted(t, m, "Explore")
		_ = os.Remove(file)
		m.send("@explore anything else?")
		m.send("@explore start over")
		kids := m.fleet.wait(t, 2)
		eq(t, [2]string{kids[1].spec.Prompt, kids[1].spec.ResumeFile}, [2]string{"start over", ""})
		m.waitNotify(t, "Started @explore", "info")
	})

	// Handles as tool arguments.
	tw(t, f, "steers by handle, not just by raw id", func(t *testing.T) {
		m := startMention(t, mentionBoot{})
		m.spawnBg("Explore")
		c := m.fleet.wait(t, 1)[0]
		text := m.must("steer_subagent", obj{"agent_id": "explore", "message": "look at the RPC path"})
		eq(t, c.steers(), []string{"look at the RPC path"})
		eq(t, strings.Contains(text, "Agent not found"), false)
	})
	tw(t, f, "steers by the name the model gave the agent", func(t *testing.T) {
		m := startMention(t, mentionBoot{})
		m.must("Agent", obj{"prompt": "go", "description": "audit", "subagent_type": "Explore", "name": "auth-audit", "run_in_background": true})
		c := m.fleet.wait(t, 1)[0]
		m.must("steer_subagent", obj{"agent_id": "auth-audit", "message": "keep going"})
		eq(t, c.steers(), []string{"keep going"})
	})
	tw(t, f, "reads a result by handle", func(t *testing.T) {
		m := startMention(t, mentionBoot{})
		rec := m.spawnBg("Explore")
		m.finish(t, rec, 0, "first answer")
		eq(t, strings.Contains(m.must("get_subagent_result", obj{"agent_id": "explore"}), "first answer"), true)
	})
	tw(t, f, "still reports an unknown reference as not found", func(t *testing.T) {
		eq(t, strings.Contains(startMention(t, mentionBoot{}).must("steer_subagent", obj{"agent_id": "nosuchagent", "message": "hi"}), "Agent not found"), true)
	})
}

func TestMentionStartNotificationTwins(t *testing.T) {
	const f = "mention-start-notification"
	relayed := func(m *mentionRig, want string) bool {
		for _, call := range m.CallsTo("sendMessage") {
			msg, _ := call.Args["message"].(map[string]any)
			opts, _ := call.Args["options"].(map[string]any)
			if msg["customType"] == "subagent-notification" && strings.Contains(msg["content"].(string), want) && opts["triggerTurn"] == true {
				return true
			}
		}
		return false
	}
	tw(t, f, "relays its answer through the ordinary completion notification (direct mode)", func(t *testing.T) {
		m := startMention(t, mentionBoot{settings: obj{"agentMentions": "direct"}})
		m.send("@Explore find the planted bugs in src/")
		c := m.fleet.wait(t, 1)[0]
		c.finish("found four planted bugs")
		waitFor(t, "notification", func() bool { return relayed(m, "found four planted bugs") })
	})
	tw(t, f, "relays it when the clone fell back to a direct start (model mode)", func(t *testing.T) {
		m := startMention(t, mentionBoot{})
		m.cloneRes = mentionCloneResult{Error: "the conversation clone did not start it"}
		m.send("@Explore whats your favorite color")
		c := m.fleet.wait(t, 1)[0]
		c.finish("cyan, obviously")
		waitFor(t, "notification", func() bool { return relayed(m, "cyan, obviously") })
	})
}
