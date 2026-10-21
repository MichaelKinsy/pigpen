package pi_goal_x

import (
	"strconv"
	"strings"
	"testing"
)

// Twins of tests/goal-live-retention.test.ts. Messages are *jsObject records.

func lrMsg(role, content string, timestamp float64) *jsObject {
	m := newObject()
	m.set("role", role)
	m.set("content", content)
	m.set("timestamp", timestamp)
	return m
}

// lrContents is contents(messages): the content of every message.
func lrContents(messages []any) []string {
	out := make([]string, 0, len(messages))
	for _, m := range messages {
		c, _ := m.(*jsObject).get("content")
		if s, ok := c.(string); ok {
			out = append(out, s)
		} else {
			out = append(out, marshalJSON(c, ""))
		}
	}
	return out
}

func lrFresh(state, counters string) *freshLiveTails {
	return &freshLiveTails{state: state, counters: &counters}
}

func lrRoles(messages []any) []string {
	out := make([]string, 0, len(messages))
	for _, m := range messages {
		out = append(out, gstr(m.(*jsObject), "role"))
	}
	return out
}

func lrAppend(base []any, more ...any) []any {
	return append(append([]any{}, base...), more...)
}

func TestGoalLiveRetention(t *testing.T) {
	const f = "goal-live-retention"

	tw(t, f, "retained tails make each request a literal prefix of the next", func(t *testing.T) {
		retention := newLiveTailRetention()
		base1 := []any{lrMsg("user", "h", 1)}
		first := retention.apply("s", base1, lrFresh("S", "V1"))
		strsEq(t, lrContents(first.messages), []string{"h", "S", "V1"}, "first")
		base2 := lrAppend(base1, lrMsg("assistant", "a", 2))
		second := retention.apply("s", base2, lrFresh("S", "V2"))
		jsonEq(t, second.messages[:len(first.messages)], first.messages, "prefix")
		strsEq(t, lrContents(second.messages), []string{"h", "S", "V1", "a", "V2"}, "second")
	})

	tw(t, f, "unchanged content across repeats and advances adds zero blocks", func(t *testing.T) {
		retention := newLiveTailRetention()
		base := []any{lrMsg("user", "h", 1)}
		fresh := lrFresh("S", "V")
		first := retention.apply("s", base, fresh)
		repeat := retention.apply("s", base, fresh)
		jsonEq(t, repeat.messages, first.messages, "repeat")
		advanced := lrAppend(base, lrMsg("assistant", "a", 2))
		third := retention.apply("s", advanced, fresh)
		jsonEq(t, third.messages[:len(first.messages)], first.messages, "prefix")
		assertEq(t, len(third.messages), len(first.messages)+1, "zero growth beyond the new message")
	})

	tw(t, f, "rewritten history resets once and never loses the live state", func(t *testing.T) {
		retention := newLiveTailRetention()
		retention.apply("s", []any{lrMsg("user", "h", 1)}, lrFresh("S", "V1"))
		compacted := []any{lrMsg("user", "compacted summary", 10)}
		next := retention.apply("s", compacted, lrFresh("S", "V1"))
		strsEq(t, lrContents(next.messages), []string{"compacted summary", "S", "V1"}, "after rewrite")
	})

	tw(t, f, "a changed policy block drops stale tails and starts a fresh prefix", func(t *testing.T) {
		retention := newLiveTailRetention()
		base := []any{lrMsg("user", "h", 1)}
		retention.apply("s", base, lrFresh("S1", "V1"))
		next := retention.apply("s", base, lrFresh("S2", "V2"))
		strsEq(t, lrContents(next.messages), []string{"h", "S2", "V2"}, "fresh prefix")
	})

	tw(t, f, "retained growth is bounded with one amortized reset per window", func(t *testing.T) {
		retention := newLiveTailRetention()
		base := []any{lrMsg("user", "h", 1)}
		var previous []any
		havePrevious := false
		breaks := 0
		for i := 0; i < maxRetainedLiveTails+8; i++ {
			out := retention.apply("s", base, lrFresh("S", "V"+strconv.Itoa(i)))
			if havePrevious && marshalJSON(out.messages[:lrMin(len(out.messages), len(previous))], "") != marshalJSON(previous, "") {
				breaks++
			}
			previous, havePrevious = out.messages, true
			assertTrue(t, len(out.messages) <= len(base)+maxRetainedLiveTails, "wire overhead stays within the retention window")
		}
		assertEq(t, breaks, 1, "breaks")
	})

	tw(t, f, "sessions are tracked independently", func(t *testing.T) {
		retention := newLiveTailRetention()
		base := []any{lrMsg("user", "h", 1)}
		retention.apply("a", base, lrFresh("SA", "VA"))
		other := retention.apply("b", base, lrFresh("SB", "VB"))
		strsEq(t, lrContents(other.messages), []string{"h", "SB", "VB"}, "b")
		again := retention.apply("a", base, lrFresh("SA", "VA"))
		strsEq(t, lrContents(again.messages), []string{"h", "SA", "VA"}, "a")
	})

	tw(t, f, "null fresh clears the session so the next request starts over", func(t *testing.T) {
		retention := newLiveTailRetention()
		base := []any{lrMsg("user", "h", 1)}
		retention.apply("s", base, lrFresh("S", "V"))
		cleared := retention.apply("s", base, nil)
		jsonEq(t, cleared.messages, base, "base returned")
		assertEq(t, len(cleared.transientContents), 0, "no transient contents")
		advanced := lrAppend(base, lrMsg("assistant", "a", 2))
		next := retention.apply("s", advanced, lrFresh("S", "V"))
		strsEq(t, lrContents(next.messages), []string{"h", "a", "S", "V"}, "starts over")
	})

	tw(t, f, "transient contents cover retained and fresh tails for marker relocation", func(t *testing.T) {
		retention := newLiveTailRetention()
		base := []any{lrMsg("user", "h", 1)}
		retention.apply("s", base, lrFresh("S", "V1"))
		advanced := lrAppend(base, lrMsg("assistant", "a", 2))
		next := retention.apply("s", advanced, lrFresh("S", "V2"))
		strsEq(t, next.transientContents, []string{"S", "V1", "V2"}, "transient")
	})

	tw(t, f, "a retained tail never splits a tool-call batch when history diverges", func(t *testing.T) {
		retention := newLiveTailRetention()
		user := lrMsg("user", "Inspect", 1)
		call := func(ids ...string) *jsObject {
			blocks := []any{}
			for _, id := range ids {
				blocks = append(blocks, jo(t, `{"type":"toolCall","id":"`+id+`","name":"read","arguments":{"path":"`+id+`"}}`))
			}
			m := newObject()
			m.set("role", "assistant")
			m.set("timestamp", 2.0)
			m.set("content", blocks)
			return m
		}
		result := func(id string) *jsObject {
			return jo(t, `{"role":"toolResult","toolCallId":"`+id+`","toolName":"read","content":[{"type":"text","text":"OK"}],"timestamp":3,"isError":false}`)
		}
		fresh := lrFresh("S", "V")
		retention.apply("s", []any{user, call("a"), result("a")}, fresh)
		// A different branch with the same shape must not inherit the old anchor.
		out := retention.apply("s", []any{user, call("b", "c"), result("b"), result("c")}, fresh).messages
		roles := lrRoles(out)
		firstResult := lrIndexOf(roles, "toolResult")
		strsEq(t, roles[firstResult:firstResult+2], []string{"toolResult", "toolResult"}, "tool results stay adjacent")
		strsEq(t, roles[len(roles)-2:], []string{"custom", "custom"}, "live state rides at the tail")
	})

	tw(t, f, "an unchanged request near capacity keeps its prefix instead of resetting", func(t *testing.T) {
		retention := newLiveTailRetention()
		base := []any{lrMsg("user", "h", 1)}
		var previous []any
		for i := 0; i < maxRetainedLiveTails-2; i++ {
			previous = retention.apply("s", base, lrFresh("S", "V"+strconv.Itoa(i))).messages
		}
		repeat := retention.apply("s", base, lrFresh("S", "V"+strconv.Itoa(maxRetainedLiveTails-3))).messages
		jsonEq(t, repeat, previous, "a zero-growth request appends nothing and drops nothing")
	})

	for _, editInPlace := range []bool{false, true} {
		kind := "replacement"
		if editInPlace {
			kind = "in-place edit"
		}
		editInPlace := editInPlace
		tw(t, f, `a middle-history ${editInPlace ? "in-place edit" : "replacement"} drops previously retained counters`, func(t *testing.T) {
			t.Log("variant:", kind)
			retention := newLiveTailRetention()
			early := jo(t, `{"role":"user","content":[{"type":"text","text":"early"}],"timestamp":1}`)
			base := []any{early, lrMsg("user", "late", 2)}
			retention.apply("s", base, lrFresh("S", "V1"))
			base = append(base, lrMsg("assistant", "reply", 3))
			retention.apply("s", base, lrFresh("S", "V2"))
			// Keep both anchor messages identical. Only earlier history changes.
			if editInPlace {
				early.vals["content"].([]any)[0].(*jsObject).set("text", "edited")
			} else {
				replacement := early.clone()
				replacement.set("content", []any{jo(t, `{"type":"text","text":"edited"}`)})
				base[0] = replacement
			}
			next := retention.apply("s", base, lrFresh("S", "V3"))
			jsonEq(t, next.messages[:len(base)], base, "no old tail remains between history messages")
			strsEq(t, lrContents(next.messages[len(base):]), []string{"S", "V3"}, "fresh tails")
			strsEq(t, next.transientContents, []string{"S", "V3"}, "old counters are gone, not merely replayed at the same anchor")
			retry := retention.apply("s", base, lrFresh("S", "V3"))
			jsonEq(t, retry.messages, next.messages, "an unchanged retry does not reset or append")
		})
	}

	for _, resultShape := range []string{"toolResult", "tool", "tool_result"} {
		resultShape := resultShape
		tw(t, f, `a retained tail never splits an extended result batch (${resultShape})`, func(t *testing.T) {
			t.Log("variant:", resultShape)
			retention := newLiveTailRetention()
			call := jo(t, `{"role":"assistant","timestamp":2,"content":[{"type":"toolCall","id":"a","name":"read","arguments":{}},{"type":"toolCall","id":"b","name":"read","arguments":{}}]}`)
			result := func(id string) *jsObject {
				return jo(t, `{"role":"toolResult","toolCallId":"`+id+`","toolName":"read","timestamp":3,"content":[{"type":"text","text":"OK"}],"isError":false}`)
			}
			base := []any{lrMsg("user", "Inspect", 1), call, result("a")}
			retention.apply("s", base, lrFresh("S", "V1"))
			// No prior message changes: only the insertion-point guard can stop a replay between these results when a
			// filtered batch is extended.
			var extra *jsObject
			if resultShape == "tool_result" {
				extra = jo(t, `{"role":"user","content":[{"type":"tool_result","tool_use_id":"b","content":"OK"}],"timestamp":4}`)
			} else {
				extra = result("b")
				extra.set("role", resultShape)
			}
			advanced := lrAppend(base, extra)
			next := retention.apply("s", advanced, lrFresh("S", "V2"))
			jsonEq(t, next.messages[:len(advanced)], advanced, "the result batch stays contiguous")
			strsEq(t, lrContents(next.messages[len(advanced):]), []string{"S", "V2"}, "fresh tails")
			strsEq(t, next.transientContents, []string{"S", "V2"}, "transient")
		})
	}

	tw(t, f, "tails anchored on empty history reset once real history arrives", func(t *testing.T) {
		retention := newLiveTailRetention()
		fresh := lrFresh("S", "V")
		first := retention.apply("s", []any{}, fresh).messages
		strsEq(t, lrContents(first), []string{"S", "V"}, "first")
		out := retention.apply("s", []any{lrMsg("user", "h", 1)}, fresh).messages
		strsEq(t, lrContents(out), []string{"h", "S", "V"}, "fresh tails anchor at the tail, nothing lingers at the head")
	})

	tw(t, f, "eviction bounds provider metadata as well as retention", func(t *testing.T) {
		retention := newLiveTailRetention()
		for i := 0; i < 100; i++ {
			retention.apply("s"+strconv.Itoa(i), []any{jo(t, `{"role":"user","content":"start"}`)}, lrFresh("S", "1"))
		}
		_, ok := retention.contents("s0")
		assertTrue(t, !ok, "s0 evicted")
		got, ok := retention.contents("s99")
		assertTrue(t, ok, "s99 retained")
		strsEq(t, got, []string{"S", "1"}, "s99 contents")
	})

	tw(t, f, "a thousand changing requests bound retained counter bytes", func(t *testing.T) {
		retention := newLiveTailRetention()
		base := []any{jo(t, `{"role":"user","content":"start"}`)}
		for i := 0; i < 1000; i++ {
			base = append(base, jo(t, `{"role":"assistant","content":"turn `+strconv.Itoa(i)+`"}`))
			counters := "Snapshot " + strconv.Itoa(i) + ": " + strings.Repeat("x", 180)
			out := retention.apply("s", base, lrFresh("S", counters))
			bytes := 0
			for _, s := range out.transientContents {
				if s != "S" {
					bytes += len(s)
				}
			}
			assertTrue(t, bytes <= 4096, "retained counter bytes bounded")
			last := out.messages[len(out.messages)-1].(*jsObject)
			assertEq(t, gstr(last, "content"), counters, "newest tail is last")
		}
	})

	tw(t, f, "a delayed result resets earlier policy as well as the unsafe counter anchor", func(t *testing.T) {
		retention := newLiveTailRetention()
		base := []any{lrMsg("user", "inspect", 1)}
		retention.apply("s", base, lrFresh("S", "V1"))
		base = append(base, jo(t, `{"role":"assistant","content":[{"type":"toolCall","id":"a","name":"read","arguments":{}}]}`))
		retention.apply("s", base, lrFresh("S", "V2"))
		base = append(base, jo(t, `{"role":"toolResult","toolCallId":"a","content":[{"type":"text","text":"failed"}],"isError":true}`))
		out := retention.apply("s", base, lrFresh("S", "V3"))
		jsonEq(t, out.messages[:len(base)], base, "history intact")
		strsEq(t, lrContents(out.messages[len(base):]), []string{"S", "V3"}, "fresh tails")
		got, _ := retention.contents("s")
		strsEq(t, got, []string{"S", "V3"}, "retained")
	})

	tw(t, f, "removing optional observations does not replay their old values", func(t *testing.T) {
		retention := newLiveTailRetention()
		base := []any{lrMsg("user", "inspect", 1)}
		retention.apply("s", base, lrFresh("S", "obsolete occupancy"))
		out := retention.apply("s", base, &freshLiveTails{state: "S"})
		strsEq(t, lrContents(out.messages), []string{"inspect", "S"}, "no counters")
	})

	tw(t, f, "image and large tool-output edits invalidate hashes without changing stored history", func(t *testing.T) {
		retention := newLiveTailRetention()
		image := jo(t, `{"type":"image","data":"`+strings.Repeat("A", 1024*1024)+`","mimeType":"image/png"}`)
		user := newObject()
		user.set("role", "user")
		user.set("content", []any{image})
		result := jo(t, `{"role":"toolResult","toolCallId":"a","content":[{"type":"text","text":"`+strings.Repeat("x", 1024*1024)+`"}]}`)
		base := []any{user, result}
		retention.apply("s", base, lrFresh("S", "old"))
		image.set("data", "B"+strings.Repeat("A", 1024*1024-1))
		out := retention.apply("s", base, lrFresh("S", "new"))
		jsonEq(t, out.messages[:len(base)], base, "history intact")
		strsEq(t, lrContents(out.messages[len(base):]), []string{"S", "new"}, "fresh tails")
		assertEq(t, len(base), 2, "base untouched")
	})
}

func lrMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func lrIndexOf(list []string, want string) int {
	for i, s := range list {
		if s == want {
			return i
		}
	}
	return -1
}
