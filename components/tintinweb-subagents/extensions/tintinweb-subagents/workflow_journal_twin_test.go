package tintinweb_subagents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Twins of test/workflow-journal.test.ts.

func liveHost(reply func(*workflowSpawnRequest) workflowSpawnResult) *wfStub {
	if reply == nil {
		reply = func(r *workflowSpawnRequest) workflowSpawnResult {
			return workflowSpawnResult{OK: true, Text: "live:" + r.Prompt}
		}
	}
	return wfStubHost(reply)
}

func prompts(calls []*workflowSpawnRequest) []string {
	out := []string{}
	for _, c := range calls {
		out = append(out, c.Prompt)
	}
	return out
}

func TestWorkflowJournal(t *testing.T) {
	const f = "workflow-journal"
	twoAgents := "const a = await agent(\"first\");\nconst b = await agent(\"second\");\nreturn [a, b];"

	// journalKey
	tw(t, f, "is stable for the same call and different for a changed prompt", func(t *testing.T) {
		eq(t, workflowJournalKey(workflowJournalKeyInput{Prompt: "a"}), workflowJournalKey(workflowJournalKeyInput{Prompt: "a"}))
		if workflowJournalKey(workflowJournalKeyInput{Prompt: "a"}) == workflowJournalKey(workflowJournalKeyInput{Prompt: "b"}) {
			t.Fatal("same key")
		}
	})
	tw(t, f, "separates calls that differ only in how the agent was configured", func(t *testing.T) {
		base := func() workflowJournalKeyInput { return workflowJournalKeyInput{Prompt: "audit"} }
		with := func(mod func(*workflowJournalKeyInput)) workflowJournalKeyInput { in := base(); mod(&in); return in }
		keys := map[string]bool{}
		for _, in := range []workflowJournalKeyInput{base(),
			with(func(i *workflowJournalKeyInput) { i.Model = wfStr("haiku") }),
			with(func(i *workflowJournalKeyInput) { i.AgentType = wfStr("Explore") }),
			with(func(i *workflowJournalKeyInput) { i.Effort = wfStr("high") }),
			with(func(i *workflowJournalKeyInput) { i.Isolation = wfStr("worktree") }),
			with(func(i *workflowJournalKeyInput) { i.Gate = wfStr("npm test") }),
			with(func(i *workflowJournalKeyInput) { i.Label = wfStr("one") })} {
			keys[workflowJournalKey(in)] = true
		}
		eq(t, len(keys), 7)
	})
	tw(t, f, "ignores which phase the row is filed under", func(t *testing.T) {
		eq(t, workflowJournalKey(workflowJournalKeyInput{Prompt: "a", Label: wfStr("x")}), workflowJournalKey(workflowJournalKeyInput{Prompt: "a", Label: wfStr("x")}))
		// The key has no phase field at all: the call's phaseIndex and phaseTitle never reach it.
		eq(t, workflowJournalKey(parseWorkflowAgentCall(map[string]any{"prompt": "a", "label": "x", "phaseIndex": int64(3), "phaseTitle": "P"}).keyInput()),
			workflowJournalKey(workflowJournalKeyInput{Prompt: "a", Label: wfStr("x")}))
	})
	// The key must equal the original's for a journal to be shared (JSON.stringify, sha256, first 32 hex digits). The
	// expected values were computed with Node 24: createHash("sha256").update(JSON.stringify([...])).digest("hex").slice(0, 32).
	t.Run("matches the original's key bytes", func(t *testing.T) {
		eq(t, workflowJournalKey(workflowJournalKeyInput{Prompt: "a"}), "26e6e3e6257fcbc88d417ab0c9ef43a0")
		eq(t, workflowJournalKey(workflowJournalKeyInput{Prompt: "x\"<>&\u2028\n\u0001é", Schema: wfStr(`{"a":1}`)}), "b83ac74b28bb023e5ea744cb56097cf2")
	})

	// journal files
	tw(t, f, "round-trips appended entries in position order", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "run.jsonl")
		appendWorkflowJournal(path, workflowJournalEntry{Index: 1, Key: "k1", OK: true, Text: wfStr("second")})
		appendWorkflowJournal(path, workflowJournalEntry{Index: 0, Key: "k0", OK: true, Text: wfStr("first")})
		eq(t, readWorkflowJournal(path), []workflowJournalEntry{{Index: 0, Key: "k0", OK: true, Text: wfStr("first")}, {Index: 1, Key: "k1", OK: true, Text: wfStr("second")}})
	})
	tw(t, f, "treats a missing journal as nothing to replay", func(t *testing.T) {
		eq(t, readWorkflowJournal(filepath.Join(t.TempDir(), "nope.jsonl")), []workflowJournalEntry{})
	})
	tw(t, f, "keeps the entries before a truncated final line", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "torn.jsonl")
		appendWorkflowJournal(path, workflowJournalEntry{Index: 0, Key: "k0", OK: true, Text: wfStr("kept")})
		data, _ := os.ReadFile(path)
		_ = os.WriteFile(path, append(data, []byte(`{"index":1,"key":"k1"`)...), 0o644)
		eq(t, readWorkflowJournal(path), []workflowJournalEntry{{Index: 0, Key: "k0", OK: true, Text: wfStr("kept")}})
	})
	tw(t, f, "drops lines that are JSON but not entries", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "junk.jsonl")
		_ = os.WriteFile(path, []byte(`{"index":"one","key":"k","ok":true}`+"\n[]\nnull\n"), 0o644)
		eq(t, readWorkflowJournal(path), []workflowJournalEntry{})
	})

	// replay
	tw(t, f, "records every settled call, so a first run can be resumed", func(t *testing.T) {
		rec := &wfRecorder{}
		res := wfRun(t, twoAgents, func(o *workflowRunOptions) { o.Host = liveHost(nil); o.Journal = rec.journal() })
		eq(t, res.Status, "completed")
		// Keyed on what the script asked for, not on what the runtime derived: the label was derived from the prompt.
		eq(t, rec.entries, []workflowJournalEntry{
			{Index: 0, Key: workflowJournalKey(workflowJournalKeyInput{Prompt: "first"}), OK: true, Text: wfStr("live:first")},
			{Index: 1, Key: workflowJournalKey(workflowJournalKeyInput{Prompt: "second"}), OK: true, Text: wfStr("live:second")}})
	})
	tw(t, f, "replays an identical run without spawning anything", func(t *testing.T) {
		first := &wfRecorder{}
		wfRun(t, twoAgents, func(o *workflowRunOptions) { o.Host = liveHost(nil); o.Journal = first.journal() })
		second := liveHost(nil)
		res := wfRun(t, twoAgents, func(o *workflowRunOptions) {
			o.Host = second
			o.Journal = &workflowJournalOptions{Entries: first.entries}
		})
		eq(t, res.Value, []any{"live:first", "live:second"})
		eq(t, res.ReplayedCount, 2)
		eq(t, len(second.Calls()), 0) // a full cache hit must not reach the manager at all
	})
	tw(t, f, "runs live from the first changed call, and no earlier", func(t *testing.T) {
		first := &wfRecorder{}
		wfRun(t, twoAgents, func(o *workflowRunOptions) { o.Host = liveHost(nil); o.Journal = first.journal() })
		second := liveHost(func(r *workflowSpawnRequest) workflowSpawnResult { return workflowSpawnResult{OK: true, Text: "fresh"} })
		edited := "const a = await agent(\"first\");\nconst b = await agent(\"second, edited\");\nreturn [a, b];"
		res := wfRun(t, edited, func(o *workflowRunOptions) {
			o.Host = second
			o.Journal = &workflowJournalOptions{Entries: first.entries}
		})
		eq(t, res.Value, []any{"live:first", "fresh"})
		eq(t, res.ReplayedCount, 1)
		eq(t, prompts(second.Calls()), []string{"second, edited"})
	})
	tw(t, f, "stops replaying after a changed call even when a later one still matches", func(t *testing.T) {
		three := "await agent(\"one\");\nawait agent(\"two\");\nreturn await agent(\"three\");"
		first := &wfRecorder{}
		wfRun(t, three, func(o *workflowRunOptions) { o.Host = liveHost(nil); o.Journal = first.journal() })
		second := liveHost(func(r *workflowSpawnRequest) workflowSpawnResult { return workflowSpawnResult{OK: true, Text: "fresh"} })
		edited := "await agent(\"one\");\nawait agent(\"two, edited\");\nreturn await agent(\"three\");"
		res := wfRun(t, edited, func(o *workflowRunOptions) {
			o.Host = second
			o.Journal = &workflowJournalOptions{Entries: first.entries}
		})
		eq(t, res.ReplayedCount, 1)
		eq(t, prompts(second.Calls()), []string{"two, edited", "three"})
	})
	tw(t, f, "re-runs a call the journal recorded as failed", func(t *testing.T) {
		first := &wfRecorder{}
		attempt := 0
		failing := liveHost(func(r *workflowSpawnRequest) workflowSpawnResult {
			attempt++
			if attempt-1 == 1 {
				return workflowSpawnResult{Error: "boom"}
			}
			return workflowSpawnResult{OK: true, Text: "fine"}
		})
		wfRun(t, twoAgents, func(o *workflowRunOptions) { o.Host = failing; o.Journal = first.journal() })
		eq(t, first.entries[1], workflowJournalEntry{Index: 1, Key: first.entries[1].Key, OK: false})
		second := liveHost(func(r *workflowSpawnRequest) workflowSpawnResult {
			return workflowSpawnResult{OK: true, Text: "retried"}
		})
		res := wfRun(t, twoAgents, func(o *workflowRunOptions) {
			o.Host = second
			o.Journal = &workflowJournalOptions{Entries: first.entries}
		})
		eq(t, res.Value, []any{"fine", "retried"})
		eq(t, res.ReplayedCount, 1)
		eq(t, prompts(second.Calls()), []string{"second"})
	})
	tw(t, f, "re-records replayed calls, so a resume can itself be resumed", func(t *testing.T) {
		first := &wfRecorder{}
		wfRun(t, twoAgents, func(o *workflowRunOptions) { o.Host = liveHost(nil); o.Journal = first.journal() })
		second := &wfRecorder{}
		wfRun(t, twoAgents, func(o *workflowRunOptions) {
			o.Host = liveHost(nil)
			o.Journal = &workflowJournalOptions{Entries: first.entries, Append: second.append}
		})
		eq(t, second.entries, first.entries)
	})
	tw(t, f, "runs everything live when there is no journal", func(t *testing.T) {
		stub := liveHost(nil)
		res := wfRun(t, twoAgents, func(o *workflowRunOptions) {
			o.Host = stub
			o.Journal = &workflowJournalOptions{Entries: []workflowJournalEntry{}}
		})
		eq(t, res.ReplayedCount, 0)
		eq(t, len(stub.Calls()), 2)
	})
	tw(t, f, "still counts replayed agents in the run's agent total", func(t *testing.T) {
		first := &wfRecorder{}
		wfRun(t, twoAgents, func(o *workflowRunOptions) { o.Host = liveHost(nil); o.Journal = first.journal() })
		res := wfRun(t, twoAgents, func(o *workflowRunOptions) {
			o.Host = liveHost(nil)
			o.Journal = &workflowJournalOptions{Entries: first.entries}
		})
		// A replayed agent is an agent that ran, as far as the run's shape goes.
		eq(t, res.AgentCount, 2)
		done := 0
		for _, a := range wfAgentRows(res.Progress) {
			if a.State == "done" {
				done++
			}
		}
		eq(t, done, 2)
	})
	tw(t, f, "marks a replayed agent so the views can say where it came from", func(t *testing.T) {
		first := &wfRecorder{}
		wfRun(t, twoAgents, func(o *workflowRunOptions) { o.Host = liveHost(nil); o.Journal = first.journal() })
		res := wfRun(t, twoAgents, func(o *workflowRunOptions) {
			o.Host = liveHost(nil)
			o.Journal = &workflowJournalOptions{Entries: first.entries}
		})
		done := 0
		for _, a := range wfAgentRows(res.Progress) {
			if a.State == "done" {
				done++
				eq(t, a.Cached, true)
			}
		}
		eq(t, done, 2)
	})
	tw(t, f, "does not mark an agent that actually ran", func(t *testing.T) {
		res := wfRun(t, twoAgents, func(o *workflowRunOptions) {
			o.Host = liveHost(nil)
			o.Journal = &workflowJournalOptions{Entries: []workflowJournalEntry{}}
		})
		for _, a := range wfAgentRows(res.Progress) {
			if a.State == "done" && a.Cached {
				t.Fatal("marked cached")
			}
		}
	})
	tw(t, f, "does not replay a run that used agent({ resume })", func(t *testing.T) {
		body := "await agent(\"first\", { label: \"a\" });\nreturn await agent(\"follow up\", { resume: \"a\" });"
		resuming := &resumeStub{wfStub: liveHost(nil), resume: func(id, prompt string, _ func(workflowResolvedInfo)) workflowSpawnResult {
			return workflowSpawnResult{OK: true, Text: "resumed:" + prompt}
		}}
		first := &wfRecorder{}
		one := wfRun(t, body, func(o *workflowRunOptions) { o.Host = resuming; o.Journal = first.journal() })
		eq(t, one.Status, "completed")
		eq(t, one.Value, "resumed:follow up")
		eq(t, first.entries[1].Resumed, true)
		two := wfRun(t, body, func(o *workflowRunOptions) {
			o.Host = resuming
			o.Journal = &workflowJournalOptions{Entries: first.entries}
		})
		eq(t, two.Status, "completed")
		eq(t, two.Value, "resumed:follow up")
		// Declined whole rather than replaying the half that would strand it.
		eq(t, two.ReplayedCount, 0)
	})
	tw(t, f, "blames the replay, not the script, when an added resume has no live target", func(t *testing.T) {
		plain := &wfRecorder{}
		wfRun(t, "await agent(\"first\", { label: \"a\" });\nreturn null;", func(o *workflowRunOptions) { o.Host = liveHost(nil); o.Journal = plain.journal() })
		host := &resumeStub{wfStub: liveHost(nil), resume: func(string, string, func(workflowResolvedInfo)) workflowSpawnResult {
			return workflowSpawnResult{OK: true, Text: "x"}
		}}
		edited := wfRun(t, "await agent(\"first\", { label: \"a\" });\nreturn await agent(\"more\", { resume: \"a\" });",
			func(o *workflowRunOptions) {
				o.Host = host
				o.Journal = &workflowJournalOptions{Entries: plain.entries}
			})
		eq(t, edited.Status, "failed")
		if !strings.Contains(edited.Error, "was replayed from the resume journal") || !strings.Contains(edited.Error, "Re-run without resumeFromRunId") {
			t.Fatal(edited.Error)
		}
	})
	tw(t, f, "replays a parallel fan-out without holding concurrency slots", func(t *testing.T) {
		fanout := `return await parallel([() => agent("a"), () => agent("b"), () => agent("c")]);`
		first := &wfRecorder{}
		wfRun(t, fanout, func(o *workflowRunOptions) { o.Host = liveHost(nil); o.Journal = first.journal() })
		second := liveHost(nil)
		res := wfRun(t, fanout, func(o *workflowRunOptions) {
			o.Host = second
			o.Journal = &workflowJournalOptions{Entries: first.entries}
			o.Concurrency = 1
		})
		eq(t, res.Value, []any{"live:a", "live:b", "live:c"})
		eq(t, len(second.Calls()), 0)
	})
}
