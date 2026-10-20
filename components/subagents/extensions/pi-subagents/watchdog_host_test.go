package pi_subagents

import (
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Twins of test/unit/watchdog-compaction-scope, watchdog-native-scheduling and watchdog-register-child.

func TestWatchdogCompactionScopeTwins(t *testing.T) {
	const f = "watchdog-compaction-scope"
	tw(t, f, "preserves user scope while clearing transient activity", func(t *testing.T) {
		withTempHome(t)
		requests := &wdRequests{}
		config := wdRtConfig(func(c *watchdogConfig) { c.Clarification = true })
		rt := newWatchdogRuntime(watchdogRuntimeOptions{Cwd: wdCwd(), ResolveConfig: newWdConfigBox(config).resolve, DisplayClarification: func(string) {},
			Review: func(r watchdogReviewRequest) (*watchdogReviewResult, error) {
				requests.add(r)
				return &watchdogReviewResult{StopReason: "stop"}, nil
			}})
		defer rt.dispose()
		h := &watchdogHost{rt: rt}
		rc := wdRunCtx{Cwd: wdCwd()}
		emit := func(name string, event any) { h.dispatch(name, event, rc) }
		assistantTurn := func(text string) any {
			return wdObj(t, `{"type":"turn_end","message":{"role":"assistant","content":[{"type":"text","text":`+marshalJSON(text, "")+`}]},"toolResults":[]}`)
		}
		emit(sdk.EventSessionStart, wdObj(t, `{"reason":"startup"}`))
		emit(sdk.EventBeforeAgentStart, wdPrompt(t, "Implement the authorized routing change."))
		emit(sdk.EventTurnEnd, wdActivityTurn(t, "subagent", `{"action":"status","id":"run"}`, "Old transient activity"))
		emit(sdk.EventSessionCompact, wdObj(t, `{"reason":"threshold"}`))
		emit(sdk.EventBeforeAgentStart, wdPrompt(t, "Continue the implementation."))
		emit(sdk.EventTurnEnd, assistantTurn("Updated code."))
		emit(sdk.EventAgentEnd, wdObj(t, `{}`))
		wdEq(t, requests.n(), 1, "reviews")
		wdMatch(t, requests.at(0).Delta, `Implement the authorized routing change\.`)
		wdMatch(t, requests.at(0).Delta, `Continue the implementation\.`)
		wdNoMatch(t, requests.at(0).Delta, `Old transient activity`)
		emit(sdk.EventSessionBeforeSwitch, wdObj(t, `{"reason":"new"}`))
		emit(sdk.EventBeforeAgentStart, wdPrompt(t, "Start unrelated work."))
		emit(sdk.EventTurnEnd, assistantTurn("New work."))
		emit(sdk.EventAgentEnd, wdObj(t, `{}`))
		wdNoMatch(t, requests.at(1).Delta, `authorized routing change`)
	})
}

// The original's native-scheduling case runs a real Pi session (it is skipped without PI_SUBAGENTS_NATIVE_SDK). Here the same
// scenario runs on the runtime, the real review and a scripted model: the reviewer asks one question and yields, the runtime hands
// it to the orchestrator as a steer that starts a turn, the continuation's boundary is skipped because its evidence was reviewed.
func TestWatchdogNativeSchedulingTwins(t *testing.T) {
	const f = "watchdog-native-scheduling"
	tw(t, f, "native Pi delivers a yielded question and continues without a reply action or forced review", func(t *testing.T) {
		withTempHome(t)
		repo := wdRepoForRuntime(t)
		question := "Which original scope applies?"
		evidence := "The original task bounded the edit."
		current := wdModel("baseten", "watchdog-test", "reasoning", false)
		ctx := wdReviewCtx(current, nil, nil, repo)
		stream := newWdScriptedStream(wdAssistant("toolUse", "", wdToolCall("watchdog_ask", map[string]any{"question": question, "evidence": evidence})))
		ctx.reg.stream = stream.fn()
		var reviewInputs atomic.Int32
		inner := createMainWatchdogReview(func() wdContext { return ctx }, wdReviewOptions{})
		sink := &wdSink{}
		rt := newWatchdogRuntime(watchdogRuntimeOptions{Cwd: repo, ReviewChangesOnly: true,
			ResolveConfig: newWdConfigBox(wdRtConfig(func(c *watchdogConfig) { c.Clarification, c.GuidanceWatchdogMd = true, false })).resolve,
			DisplayClarification: sink.message, DisplayWarning: sink.warning,
			Review: func(r watchdogReviewRequest) (*watchdogReviewResult, error) {
				reviewInputs.Add(1)
				return inner(r)
			}})
		defer rt.dispose()
		h := &watchdogHost{rt: rt}
		rc := wdRunCtx{Cwd: repo}
		turn := wdObj(t, `{"type":"turn_end","message":{"role":"assistant","content":[{"type":"toolCall","id":"c1","name":"fixture_edit","arguments":{}}]},"toolResults":[{"role":"toolResult","toolCallId":"c1","toolName":"fixture_edit","content":[{"type":"text","text":"Original bounded edit completed."}],"isError":false}]}`)
		h.dispatch(sdk.EventSessionStart, wdObj(t, `{}`), rc)
		h.dispatch(sdk.EventBeforeAgentStart, wdPrompt(t, "Make one original bounded edit."), rc)
		writeFile(t, filepath.Join(repo, "marker.txt"), "after")
		h.dispatch(sdk.EventTurnEnd, turn, rc)
		h.dispatch(sdk.EventAgentEnd, wdObj(t, `{}`), rc)
		wdEq(t, stream.callCount(), 1, "asking yields without another provider call or forced review")
		wdEq(t, sink.nMessages(), 1, "one visible question")
		for _, text := range []string{question, evidence} {
			wdTrue(t, strings.Contains(sink.msg(0), text), "the steer carries "+text)
		}
		msg, opts := wdClarificationMessage(sink.msg(0))
		wdEq(t, msg.CustomType, "subagent_watchdog_clarification", "customType")
		wdEq(t, msg.Display, true, "display")
		wdEq(t, opts.DeliverAs, "steer", "deliverAs")
		wdEq(t, *opts.TriggerTurn, true, "triggerTurn")
		// The orchestrator continues without a new prompt: its boundary has no new evidence, so no review runs.
		h.dispatch(sdk.EventTurnEnd, wdObj(t, `{"type":"turn_end","message":{"role":"assistant","content":[{"type":"text","text":"I will keep the original bounded scope and continue."}]},"toolResults":[]}`), rc)
		h.dispatch(sdk.EventAgentEnd, wdObj(t, `{}`), rc)
		wdEq(t, int(reviewInputs.Load()), 1, "ordinary unchanged-evidence gate skips the continuation boundary")
		wdEq(t, rt.getSnapshot(repo).FailedReviews, 0, "failedReviews")
	})
}

func TestWatchdogRegisterChildTwins(t *testing.T) {
	const f = "watchdog-register-child"
	tw(t, f, "records a late real blocker without triggering a turn after agent settlement", func(t *testing.T) {
		withTempHome(t)
		var mu sync.Mutex
		var sent, appended []any
		var statuses []childWatchdogStatusEvent
		binding := childWatchdogBinding{
			SendMessage: func(m watchdogWarningMessage, _ *wdSendOptions) { mu.Lock(); sent = append(sent, m); mu.Unlock() },
			AppendEntry: func(customType string, d wdWarning) { mu.Lock(); appended = append(appended, d); mu.Unlock() },
		}
		var changeKey atomic.Value
		changeKey.Store("baseline")
		started := make(chan struct{})
		release := make(chan struct{})
		review := func(r watchdogReviewRequest) (*watchdogReviewResult, error) {
			close(started)
			<-release
			r.EmitWarning(wdWarning{Severity: "blocker", Importance: "high", Summary: "Independent blocker", Evidence: "A real failure remains after structured capture.",
				RecommendedAction: "Fix the failure.", Source: "child"})
			return &watchdogReviewResult{StopReason: "stop"}, nil
		}
		cfg := childWatchdogConfig{AgentEndTimeoutMs: 1000, MaxWarnings: wdPtr(3), StalemateRepeats: 2, WatchdogTailTimeoutMs: 1000, Cadence: wdCadence{},
			Lsp: wdLsp{Enabled: false, TimeoutMs: 1000, MaxFiles: 10, MaxDiagnostics: 10}}
		w, err := newChildWatchdog(cfg, func(e childWatchdogStatusEvent) { mu.Lock(); statuses = append(statuses, e); mu.Unlock() }, binding, nil, childWatchdogOptions{
			Review: review,
			RepoChangeSignature: func(string) *watchdogRepoChangeSignature {
				return &watchdogRepoChangeSignature{Root: "/tmp", Key: changeKey.Load().(string), ChangedPaths: []string{"src/file.ts"}}
			}})
		if err != nil {
			t.Fatal(err)
		}
		rc := wdRunCtx{Cwd: "/tmp"}
		w.onSessionStart(rc)
		w.onBeforeAgentStart(wdPrompt(t, "Complete the task."), rc)
		w.onTurnEnd(wdObj(t, `{"type":"turn_end","message":{"role":"assistant","content":[],"stopReason":"stop"},"toolResults":[]}`), rc, true)
		changeKey.Store("changed")
		ending := wdAsync(func() { w.onAgentEnd(wdObj(t, `{}`), rc) })
		wdAwait(t, "review start", started)
		w.onAgentSettled()
		close(release)
		wdAwait(t, "agent end", ending)
		mu.Lock()
		defer mu.Unlock()
		wdEq(t, len(sent), 0, "settled child must not receive a turn-triggering warning")
		wdEq(t, len(appended), 1, "the real blocker remains visible as a status-only entry")
		d := appended[0].(wdWarning)
		wdEq(t, d.Severity, "blocker", "severity")
		wdEq(t, d.Summary, "Independent blocker", "summary")
		found := false
		for _, s := range statuses {
			found = found || (s.Phase == "reviewing" && s.Warning != nil && s.Warning.Severity == "blocker" && s.Warning.Summary == "Independent blocker")
		}
		wdTrue(t, found, "the status sink must retain the late blocker evidence")
		w.onSessionShutdown()
	})
}

