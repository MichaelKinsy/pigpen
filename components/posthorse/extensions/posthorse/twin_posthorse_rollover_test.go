package posthorse

// Twins of the rollover and reminder cases of test/posthorse.test.ts and test/rollover-cost.test.ts,
// run against the real extension on the fake host (rollover_harness_test.go).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const roFile = "posthorse"

// roSentLog is the pi.sendMessage log of a fixture with the TS `messages.length = 0` reset.
type roSentLog struct {
	f      *fixture
	offset int
}

func (l *roSentLog) all() []fixSent { return l.f.Messages()[l.offset:] }
func (l *roSentLog) count() int     { return len(l.all()) }
func (l *roSentLog) clear()         { l.offset = len(l.f.Messages()) }

// roBoundary is the BoundaryResult of a turn_end reply: the SDK's reply wraps the handler's result as
// `_pigBoundaryResult` next to the event's entries, and the fixture returns either form.
func roBoundary(reply rec) rec {
	if wrapped, ok := reply["_pigBoundaryResult"]; ok {
		result, _ := wrapped.(map[string]any)
		return result
	}
	return reply
}

// roEntryReads counts the by-id entry reads. TS reads the leaf with getEntry(getLeafId()); Go reads it with
// getLeafEntry (one wire read instead of two), so the TS count is the sum of both Go reads.
func roEntryReads(f *fixture) int {
	return f.Reads("sessionRead.getEntry") + f.Reads("sessionRead.getLeafEntry")
}

func roGetBranchReads(f *fixture) int {
	return f.Reads("sessionRead.getBranch") + f.Reads("watchSessionLog")
}

func TestTwinPosthorseRollover(t *testing.T) {
	// posthorse.test.ts:634
	tw(t, roFile, "budget policy sends one reminder and lets Pi own rollover (window=${contextWindow}, reserve=${reserve})", func(t *testing.T) {
		for _, c := range []struct{ contextWindow, reserve, remindAt int }{
			{100_000, 64_000, 32_401},
			{335_000, 16_384, 286_756},
			{335_000, 64_000, 243_901},
			{262_144, 64_000, 178_331},
			{262_144, 16_384, 221_185},
			{1_048_576, 16_384, 1_000_193},
		} {
			t.Run(fmt.Sprintf("window=%d,reserve=%d", c.contextWindow, c.reserve), func(t *testing.T) {
				dir := t.TempDir()
				// A project file that disagrees with Pi's effective policy must be ignored: Pi decides trust, not Posthorse.
				if err := os.MkdirAll(filepath.Join(dir, ".pi"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, ".pi", "settings.json"), []byte(`{"compaction":{"enabled":false,"reserveTokens":1}}`), 0o644); err != nil {
					t.Fatal(err)
				}
				threshold := c.contextWindow - c.reserve
				rolloverAt := threshold + 1
				f := newFixture(t, withStartCwd(dir), withUsage(c.contextWindow, c.remindAt-1), withReserve(c.reserve),
					withSession(fxIndexedSession(rec{"type": "context_window", "id": "window-2"})), withEmptyProjection())
				log := &roSentLog{f: f}

				f.FireAll("session_start", rec{})
				f.TurnEnd()
				roEqual(t, log.count(), 0, "no reminder below the band")
				roEqual(t, roGetBranchReads(f), 0, "below-band turns must not fetch history")
				f.Set(withUsage(c.contextWindow, c.remindAt))
				f.Run("new_context", rec{})
				reset := roBoundary(f.TurnEnd(rec{"toolName": "new_context", "toolCallId": "id"}))
				roEqual(t, log.count(), 0, "a successful rollover batch sends no reminder")
				entries, _ := reset["entries"].([]any)
				if len(entries) != 1 || roRec(entries[0])["type"] != "compaction" || roRec(roRec(entries[0])["details"])["reason"] != "explicit" || reset["continue"] != true {
					t.Errorf("new_context must draft one explicit compaction and continue, got %v", reset)
				}
				f.TurnEndEvent(rec{"message": rec{"role": "assistant", "stopReason": "error"}, "toolResults": []any{}})
				roEqual(t, log.count(), 0, "an errored turn sends no reminder")
				f.TurnEndEvent(rec{"message": rec{"role": "assistant", "stopReason": "aborted"}, "toolResults": []any{}})
				roEqual(t, log.count(), 0, "an aborted turn sends no reminder")
				roEqual(t, roGetBranchReads(f), 0, "successful rollover batches, errors, and aborts need no history")

				f.TurnEnd()
				sent := log.all()
				roEqual(t, len(sent), 1, "one reminder in the band")
				if len(sent) != 1 {
					return
				}
				if !strings.Contains(sent[0].Content, "Routine task-note update") {
					t.Errorf("reminder content %q", sent[0].Content)
				}
				if sent[0].CustomType != "posthorse-reminder" || !sent[0].Display || sent[0].Options["deliverAs"] != "steer" {
					t.Errorf("reminder message %+v", sent[0])
				}
				roSameJSON(t, sent[0].Details, roDetails("window-2", c.contextWindow, c.reserve), "reminder fingerprint")
				roEqual(t, roGetBranchReads(f), 0, "reminder checks use by-id ancestry, not a full branch copy")
				f.Session().push(rec{"type": "custom_message", "customType": "posthorse-reminder", "details": sent[0].Details})

				f.Set(withUsage(c.contextWindow, threshold))
				f.TurnEnd()
				roEqual(t, log.count(), 1, "Pi does not roll at exact threshold equality")
				f.Session().pop()
				log.clear()
				f.TurnEnd()
				roEqual(t, log.count(), 1, "Posthorse still offers a checkpoint at equality")
				roEqual(t, roGetBranchReads(f), 0, "in-band checks do not materialize the branch")

				log.clear()
				f.Set(withUsage(c.contextWindow, rolloverAt))
				f.TurnEnd()
				roEqual(t, log.count(), 0, "Pi owns the first token that actually triggers rollover")
				roEqual(t, roGetBranchReads(f), 0, "rollover equality must not fetch history")
				f.Set(withUsage(c.contextWindow, rolloverAt+1_000))
				f.TurnEnd()
				roEqual(t, log.count(), 0, "Posthorse also stays out of Pi's over-threshold path")
				roEqual(t, roGetBranchReads(f), 0, "over-threshold turns must not fetch history")
			})
		}
	})

	// posthorse.test.ts:749. The Go SDK always has Context.HasPendingMessages, so the host answers it
	// (false) where the TS context lacks the function (undefined): both leave the reminder off.
	tw(t, roFile, "reminders respect live pending messages and tool calls even with a stop reason", func(t *testing.T) {
		for _, pending := range []string{"undefined", "false", "true"} {
			t.Run("pending="+pending, func(t *testing.T) {
				f := newFixture(t, withUsage(100_000, 80_000), withPending(pending == "true"))
				f.TurnEndEvent(rec{
					"message":     rec{"role": "assistant", "stopReason": "stop", "content": []any{}},
					"toolResults": []any{}, "entries": []any{}, "continue": false,
					"context": rec{"pendingMessages": []any{}},
				})
				want := 0
				if pending == "true" {
					want = 1
				}
				roEqual(t, len(f.Messages()), want, "reminder with pending="+pending)
			})
		}
		f := newFixture(t, withUsage(100_000, 80_000))
		f.TurnEndEvent(rec{
			"message": rec{"role": "assistant", "stopReason": "stop", "content": []any{
				rec{"type": "toolCall", "id": "id", "name": "get_context_remaining", "arguments": rec{}},
			}},
			"toolResults": []any{}, "entries": []any{}, "continue": false,
			"context": rec{"pendingMessages": []any{}},
		})
		roEqual(t, len(f.Messages()), 1, "tool calls make a stop turn a working turn")
	})

	// posthorse.test.ts:793
	tw(t, roFile, "a large reserve does not send checkpoint reminders in a fresh window", func(t *testing.T) {
		base := newFixture(t)
		for _, tokens := range []int{1000, 9000} {
			base.With(withUsage(400_000, tokens), withReserve(390_000)).TurnEnd()
		}
		roEqual(t, len(base.Messages()), 0, "a fresh 10K usable window must not immediately checkpoint again")
		base.With(withUsage(400_000, 9001), withReserve(390_000)).TurnEnd()
		sent := base.Messages()
		roEqual(t, len(sent), 1, "one reminder at 9001")
		if len(sent) == 1 && !strings.Contains(sent[0].Content, "Routine task-note update") {
			t.Errorf("reminder content %q", sent[0].Content)
		}
	})

	// posthorse.test.ts:926
	tw(t, roFile, "reminder lookups are leaf-certified and preserve the full kept range (${archived} archived entries)", func(t *testing.T) {
		for _, archived := range []int{10, 43_000} {
			t.Run(fmt.Sprintf("archived=%d", archived), func(t *testing.T) {
				// The negative cold lookup is `archived` wire reads in one turn_end (the TS test counts them and
				// forbids getBranch); under -race that outlasts the fixture's 10s wait.
				defer func(saved time.Duration) { fxWaitTimeout = saved }(fxWaitTimeout)
				fxWaitTimeout = 10 * time.Minute
				manager := fxMemorySession("")
				for range archived {
					manager.appendCustomEntry("old", rec{})
				}
				window := manager.appendCompaction("carry on", "", 100, rec{"posthorse": float64(1)})
				fingerprint := roDetails(window, 100_000, 16_384)
				reminder := manager.appendCustomMessageEntry("posthorse-reminder", "Checkpoint now", true, fingerprint)
				manager.appendMessage(rec{"role": "user", "content": "kept", "timestamp": float64(1)})
				manager.appendCompaction("native summary", reminder, 100, nil)
				// The TS fixture's getBranch throws: reminder checks must not copy full history.
				f := newFixture(t, withUsage(100_000, 76_000), withSession(manager), withEmptyProjection(),
					withHostError("sessionRead.getBranch", "Reminder checks must not copy full history"))
				log := &roSentLog{f: f}

				f.TurnEnd()
				roEqual(t, log.count(), 0, "native compaction retains a reminder before its own entry")
				roEqual(t, roEntryReads(f), 4, "cold lookup stops at the Posthorse boundary, independent of archive size")
				for range 5 {
					f.TurnEnd()
				}
				roEqual(t, roEntryReads(f), 9, "unchanged leaf needs only one by-id certification")
				manager.appendCustomEntry("new", rec{})
				f.ResetReads()
				f.TurnEnd()
				roEqual(t, roEntryReads(f), 2, "append lookup stops at the certified previous leaf")
				roEqual(t, log.count(), 0, "an append sends nothing")
				removed := manager.appendCompaction("removed reminder", "", 100, nil)
				f.TurnEnd()
				roEqual(t, log.count(), 1, "removal permits a new reminder in the same logical window")
				if sent := log.all(); len(sent) > 0 {
					roSameJSON(t, sent[0].Details, fingerprint, "reminder fingerprint")
				}
				manager.branch(reminder)
				f.TurnEnd()
				roEqual(t, log.count(), 1, "navigation back restores the kept reminder without stale negative state")
				manager.branch(removed)
				f.FireAll("session_tree", rec{})
				f.TurnEnd()
				roEqual(t, log.count(), 2, "navigation forward restores its absence")
				f.FireAll("session_start", rec{})
				f.TurnEnd()
				roEqual(t, log.count(), 3, "session lifecycle invalidates branch facts, not safety snapshots")

				initial := fxMemorySession("")
				for range archived {
					initial.appendCustomEntry("old", rec{})
				}
				f.Set(withSession(initial))
				f.ResetReads()
				f.TurnEnd()
				roEqual(t, roEntryReads(f), archived, "a negative cold lookup is complete, never capped")
				f.ResetReads()
				f.TurnEnd()
				roEqual(t, roEntryReads(f), 1, "negative state is cached only for the certified leaf and session")
				sent := log.all()
				if len(sent) == 0 || sent[len(sent)-1].WindowID() != "initial" {
					t.Errorf("the last reminder must carry the initial window, got %+v", sent)
				}
			})
		}
	})

	// posthorse.test.ts:1094
	tw(t, roFile, "disabling compaction filters persisted reminders without changing history or deduplication", func(t *testing.T) {
		reminder := roMessage("posthorse-reminder", "Checkpoint now", roDetails("initial", 100_000, 16_384))
		entry := rec{"type": "custom_message", "id": "reminder"}
		for k, v := range reminder {
			entry[k] = v
		}
		f := newFixture(t, withUsage(100_000, 76_000), withSession(fxIndexedSession(entry)), withEmptyProjection())
		owner := rec{"role": "user", "content": "continue"}
		if got := f.ContextEvent(owner, reminder); got != nil {
			t.Errorf("an enabled policy keeps a matching reminder, got %v", got)
		}
		f.Set(withEnabled(false))
		got := f.ContextEvent(owner, reminder)
		roSameJSON(t, got["messages"], []rec{owner}, "disabled compaction drops the reminder")
		if content := f.Session().getEntry("reminder")["content"]; content != "Checkpoint now" {
			t.Errorf("raw history must stay intact, got %v", content)
		}
		f.Set(withEnabled(true))
		f.TurnEnd()
		roEqual(t, len(f.Messages()), 0, "reenabling does not emit another reminder")
	})

	// posthorse.test.ts:1132
	tw(t, roFile, "reminder-free context and fork reminder filtering never read history", func(t *testing.T) {
		f := newFixture(t)
		marker := roMessage("context-window", "new", rec{"windowId": "new"})
		user := rec{"role": "user", "content": "posthorse-reminder is just text here"}
		other := rec{"role": "custom", "customType": "intercom_message", "content": "keep"}
		for _, messages := range [][]rec{{}, {user, other}, {marker, user, other}} {
			if got := f.ContextEvent(messages...); got != nil {
				t.Errorf("unrelated messages remain unchanged, got %v", got)
			}
		}
		current := roMessage("posthorse-reminder", "current", roDetails("new", 100_000, 16_384))
		stale := roMessage("posthorse-reminder", "stale", roDetails("old", 100_000, 16_384))
		got := f.ContextEvent(marker, user, other, stale, current)
		roSameJSON(t, got["messages"], []rec{marker, user, other, current}, "stale reminder removed")
		roEqual(t, roGetBranchReads(f), 0, "the fork's window marker identifies the window")
		roEqual(t, roEntryReads(f), 0, "the fork's window marker needs no ancestry either")
	})

	// posthorse.test.ts:1173
	tw(t, roFile, "context filtering removes reminders from an older window or a different budget", func(t *testing.T) {
		f := newFixture(t)
		marker := roMessage("context-window", "new", rec{"windowId": "new"})
		current := roMessage("posthorse-reminder", "current", roDetails("new", 100_000, 16_384))
		old := roMessage("posthorse-reminder", "old", roDetails("old", 100_000, 16_384))
		otherModel := roMessage("posthorse-reminder", "smaller model", roDetails("new", 50_000, 16_384))
		otherReserve := roMessage("posthorse-reminder", "other reserve", roDetails("new", 100_000, 64_000))
		other := rec{"role": "custom", "customType": "intercom_message", "content": "keep"}
		filtered := f.ContextEvent(marker, old, otherModel, otherReserve, other, current)
		roSameJSON(t, filtered["messages"], []rec{marker, other, current}, "older window and other budgets removed")
		if got := f.ContextEvent(marker, other, current); got != nil {
			t.Errorf("a current reminder stays, got %v", got)
		}
		// Before any rollover the window is "initial"; a reminder computed for another model size is still stale.
		initial := roMessage("posthorse-reminder", "initial", roDetails("initial", 100_000, 16_384))
		filteredInitial := f.ContextEvent(otherModel, initial)
		roSameJSON(t, filteredInitial["messages"], []rec{initial}, "initial window")
	})

	// posthorse.test.ts:2077 (PR #51 review): a reminder that Pi's own compaction summarized away must not block the next one.
	tw(t, roFile, "a reminder before a native compaction does not suppress the next reminder", func(t *testing.T) {
		f := newFixture(t, withUsage(100_000, 76_000), withEmptyProjection(), withSession(fxIndexedSession(
			rec{"type": "custom_message", "id": "old-reminder", "customType": "posthorse-reminder", "details": roDetails("initial", 100_000, 16_384)},
			rec{"type": "message", "id": "kept", "message": rec{"role": "user", "content": "kept tail"}},
			rec{"type": "compaction", "id": "native", "summary": "native summary", "firstKeptEntryId": "kept"},
		)))
		f.TurnEnd()
		sent := f.Messages()
		roEqual(t, len(sent), 1, "the reminder the compaction summarized away does not count")
		if len(sent) != 1 {
			return
		}
		f.Session().push(rec{"type": "custom_message", "id": "new-reminder", "customType": "posthorse-reminder", "details": sent[0].Details})
		f.TurnEnd()
		roEqual(t, len(f.Messages()), 1, "still one reminder per window and budget")
	})

	// posthorse.test.ts:2110
	tw(t, roFile, "new_context beside a failed sibling tool does not suppress the reminder", func(t *testing.T) {
		f := newFixture(t, withUsage(100_000, 76_000))
		log := &roSentLog{f: f}
		f.Run("new_context", rec{})
		f.TurnEnd(rec{"toolName": "new_context", "toolCallId": "id"}, rec{"toolName": "bash", "isError": true})
		roEqual(t, log.count(), 1, "Pi will not commit the boundary, so the checkpoint reminder still applies")
		log.clear()
		f.Run("new_context", rec{})
		f.TurnEnd(rec{"toolName": "new_context", "toolCallId": "id"}, rec{"toolName": "bash", "isError": false})
		roEqual(t, log.count(), 0, "a fully successful batch rolls over; no reminder needed")
	})
}

func roRec(v any) rec { m, _ := v.(rec); return m }

func roMessage(customType, content string, details rec) rec {
	m := rec{"role": "custom", "customType": customType, "content": content}
	if details != nil {
		m["details"] = details
	}
	return m
}

func roDetails(windowID string, contextWindow, reserve int) rec {
	return rec{"windowId": windowID, "contextWindow": float64(contextWindow), "reserveTokens": float64(reserve)}
}

func roSameJSON(t *testing.T, got, want any, msg string) {
	t.Helper()
	g, _ := json.Marshal(got)
	w, _ := json.Marshal(want)
	if string(g) != string(w) {
		t.Errorf("%s:\n got %s\nwant %s", msg, g, w)
	}
}

func roEqual(t *testing.T, got, want any, msg string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: got %v, want %v", msg, got, want)
	}
}

// roPolicy is a PolicyContext over fixed values.
type roPolicy struct {
	settings compactionPolicy
	usage    *contextUsage
	prompt   string
}

func (p roPolicy) getCompactionSettings() (compactionPolicy, error) { return p.settings, nil }
func (p roPolicy) getContextUsage() (*contextUsage, error)          { return p.usage, nil }
func (p roPolicy) getSystemPrompt() (string, error)                 { return p.prompt, nil }

// rollover-cost.test.ts
func TestTwinRolloverCost(t *testing.T) {
	const window = 335_000
	// rollover-cost.test.ts:13
	tw(t, "rollover-cost", "reserve scenario ${scenario.reserve} agrees with the native threshold", func(t *testing.T) {
		for _, s := range []struct{ reserve, usable, line, headroom int }{
			{64_000, 271_000, 271_001, 63_999},
			{32_000, 303_000, 303_001, 31_999},
			{24_000, 311_000, 311_001, 23_999},
		} {
			t.Run(fmt.Sprintf("reserve=%d", s.reserve), func(t *testing.T) {
				keep := 20_000
				settings := compactionPolicy{enabled: true, reserveTokens: s.reserve, keepRecentTokens: &keep}
				b, err := budgetFor(roPolicy{settings: settings, usage: &contextUsage{contextWindow: window}, prompt: "Synthetic reserve arithmetic"}, nil)
				if err != nil || b == nil {
					t.Fatalf("budgetFor = %v, %v", b, err)
				}
				if !b.supported {
					t.Errorf("budget must be supported")
				}
				roEqual(t, b.usable, s.usable, "usable")
				roEqual(t, b.rolloverAt, s.line, "rolloverAt")
				native := sdk.CompactionSettings{Enabled: true, ReserveTokens: s.reserve, KeepRecentTokens: keep}
				if sdk.ShouldCompact(s.usable, window, native) {
					t.Errorf("shouldCompact(%d) must be false", s.usable)
				}
				if !sdk.ShouldCompact(s.line, window, native) {
					t.Errorf("shouldCompact(%d) must be true", s.line)
				}
				roEqual(t, window-s.line, s.headroom, "headroom")
			})
		}
	})

	// rollover-cost.test.ts:34. pi-ai's calculateCost is the SDK's CalculateCost; the faux provider's model is a
	// plain record with the synthetic prices.
	tw(t, "rollover-cost", "synthetic prices distinguish a cache read, fresh input, and both write lifetimes", func(t *testing.T) {
		model := map[string]any{"cost": map[string]any{"input": float64(10), "output": float64(30), "cacheRead": float64(1), "cacheWrite": 12.5}}
		price := func(input, cacheRead, cacheWrite, cacheWrite1h float64) float64 {
			usage := map[string]any{
				"input": input, "output": float64(0), "cacheRead": cacheRead, "cacheWrite": cacheWrite, "totalTokens": float64(0),
				"cost": map[string]any{"input": float64(0), "output": float64(0), "cacheRead": float64(0), "cacheWrite": float64(0), "total": float64(0)},
			}
			if cacheWrite1h != 0 {
				usage["cacheWrite1h"] = cacheWrite1h
			}
			cost, err := sdk.CalculateCost(model, usage)
			if err != nil {
				t.Fatal(err)
			}
			return cost["total"].(float64)
		}
		read := price(0, 1_000_000, 0, 0)
		roEqual(t, read, float64(1), "a cache read")
		roEqual(t, price(1_000_000, 0, 0, 0)-read, float64(9), "fresh input")
		roEqual(t, price(0, 0, 1_000_000, 0)-read, 11.5, "a short cache write")
		roEqual(t, price(0, 0, 1_000_000, 1_000_000)-read, float64(19), "a one-hour cache write")
	})
}
