package pi_goal_x

import (
	"strings"
	"testing"
)

// Twins of tests/goal-prompt-cache.test.ts. Payloads are decoded JSON; "undefined" results are nil.

func TestGoalPromptCache(t *testing.T) {
	const f = "goal-prompt-cache"
	live := "[PI GOAL ACTIVE goalId=fixture]\nUsage: 123 tokens"
	control := `{"type":"ephemeral","ttl":"1h"}`
	liveJSON := marshalJSON(live, "")

	// payload builds {messages:[...]} from message literals; the %LIVE% marker is replaced by the live text.
	payload := func(t *testing.T, messages ...string) *jsObject {
		t.Helper()
		src := `{"messages":[` + strings.Join(messages, ",") + `]}`
		src = strings.ReplaceAll(src, `"%LIVE%"`, liveJSON)
		return jo(t, src)
	}
	msgs := func(p *jsObject) []any { return p.vals["messages"].([]any) }
	message := func(p *jsObject, i int) *jsObject { return msgs(p)[i].(*jsObject) }
	contentOf := func(m *jsObject) []any { return m.vals["content"].([]any) }
	block := func(m *jsObject, i int) *jsObject { return contentOf(m)[i].(*jsObject) }
	last := func(l []any) *jsObject { return l[len(l)-1].(*jsObject) }
	noCacheControl := func(b *jsObject) bool { return !b.has("cache_control") }
	clone := func(p *jsObject) *jsObject { return deepClone(p).(*jsObject) }

	tw(t, f, "explicit breakpoints move before transient state without changing TTL or tool ordering", func(t *testing.T) {
		for _, content := range []string{`"history"`, `[{"type":"text","text":"history"}]`, `[{"type":"tool_result","tool_use_id":"id","content":"history"}]`} {
			p := payload(t, `{"role":"user","content":`+content+`}`, `{"role":"user","content":[{"type":"text","text":"%LIVE%","cache_control":`+control+`}]}`)
			got := cacheGoalHistory(p, []string{live})
			assertTrue(t, got == any(p), "returns the same payload")
			// content was a string for the first case: it is now a one-block array carrying the marker.
			jsonEq(t, last(contentOf(message(p, 0))).vals["cache_control"], jo(t, control), "breakpoint moved with the original TTL")
			assertTrue(t, noCacheControl(block(message(p, 1), 0)), "live block lost the marker")
		}
		same := payload(t, `{"role":"user","content":[{"type":"text","text":"history"},{"type":"text","text":"%LIVE%","cache_control":`+control+`}]}`)
		cacheGoalHistory(same, []string{live})
		jsonEq(t, block(message(same, 0), 0).vals["cache_control"], jo(t, control), "same-message history marker")
		assertTrue(t, noCacheControl(block(message(same, 0), 1)), "live block lost the marker")
		thinking := payload(t,
			`{"role":"user","content":[{"type":"text","text":"history"}]}`,
			`{"role":"assistant","content":[{"type":"thinking","thinking":"private"}]}`,
			`{"role":"user","content":[{"type":"text","text":"%LIVE%","cache_control":`+control+`}]}`)
		cacheGoalHistory(thinking, []string{live})
		jsonEq(t, block(message(thinking, 0), 0).vals["cache_control"], jo(t, control), "marker lands on history")
		assertTrue(t, noCacheControl(block(message(thinking, 1), 0)), "thinking block never receives it")
	})

	tw(t, f, "implicit caches, disabled caching, unknown payloads and another extension's tail are untouched", func(t *testing.T) {
		for _, src := range []string{`null`, `{"input":[]}`, `{"messages":[]}`,
			`{"messages":[{"role":"user","content":[{"type":"text","text":"` + live[:0] + `%LIVE%"}]}]}`,
			`{"messages":[{"role":"user","content":[{"type":"text","text":"another extension","cache_control":` + control + `}]}]}`} {
			src = strings.ReplaceAll(src, `%LIVE%`, strings.Trim(liveJSON, `"`))
			p := parseAny(t, src)
			before := deepClone(p)
			assertTrue(t, cacheGoalHistory(p, []string{live}) == nil, "untouched payload returns undefined")
			jsonEq(t, p, before, "payload unchanged")
		}
	})

	tw(t, f, "a contentless trailing message from the host does not defeat the repair", func(t *testing.T) {
		marker := `{"role":"system","content":[],"output_config":{"effort":"high"}}`
		for _, trailing := range []string{marker, `{"role":"system","content":""}`, `{"role":"system","content":null}`, `{"role":"system"}`} {
			p := payload(t,
				`{"role":"user","content":[{"type":"text","text":"history"}]}`,
				`{"role":"user","content":[{"type":"text","text":"%LIVE%","cache_control":`+control+`}]}`,
				trailing)
			got := cacheGoalHistory(p, []string{live})
			assertTrue(t, got == any(p), "returns the same payload")
			jsonEq(t, block(message(p, 0), 0).vals["cache_control"], jo(t, control), "marker on history")
			assertTrue(t, noCacheControl(block(message(p, 1), 0)), "live block lost the marker")
			jsonEq(t, message(p, 2), jo(t, trailing), "trailing message untouched")
		}

		bedrock := payload(t,
			`{"role":"user","content":[{"text":"history"}]}`,
			`{"role":"user","content":[{"text":"%LIVE%"},{"cachePoint":{"type":"default","ttl":"1h"}}]}`,
			marker)
		cacheGoalHistory(bedrock, []string{live})
		jsonEq(t, last(contentOf(message(bedrock, 0))), jo(t, `{"cachePoint":{"type":"default","ttl":"1h"}}`), "cachePoint moved")
		jsonEq(t, contentOf(message(bedrock, 1)), []any{jo(t, `{"text":`+liveJSON+`}`)}, "live content keeps only the text")

		// A trailing message that does carry content is still another extension's business: the tail no longer matches the
		// live block, so nothing moves.
		foreign := payload(t,
			`{"role":"user","content":[{"type":"text","text":"history"}]}`,
			`{"role":"user","content":[{"type":"text","text":"%LIVE%","cache_control":`+control+`}]}`,
			`{"role":"user","content":[{"type":"text","text":"another extension"}]}`)
		before := clone(foreign)
		assertTrue(t, cacheGoalHistory(foreign, []string{live}) == nil, "foreign tail untouched")
		jsonEq(t, foreign, before, "payload unchanged")
	})

	tw(t, f, "Bedrock moves its existing cachePoint before the live message", func(t *testing.T) {
		p := payload(t,
			`{"role":"user","content":[{"text":"history"}]}`,
			`{"role":"user","content":[{"text":"%LIVE%"},{"cachePoint":{"type":"default","ttl":"1h"}}]}`)
		cacheGoalHistory(p, []string{live})
		jsonEq(t, last(contentOf(message(p, 0))), jo(t, `{"cachePoint":{"type":"default","ttl":"1h"}}`), "cachePoint moved")
		jsonEq(t, contentOf(message(p, 1)), []any{jo(t, `{"text":`+liveJSON+`}`)}, "live content keeps only the text")
	})

	tw(t, f, "split and retained live blocks never receive the explicit breakpoint", func(t *testing.T) {
		counters := "Usage: 456 tokens"
		for _, retained := range []string{`"%LIVE%"`, `[{"type":"text","text":"%LIVE%"}]`} {
			p := payload(t,
				`{"role":"user","content":[{"type":"text","text":"real history"}]}`,
				`{"role":"user","content":`+retained+`}`,
				`{"role":"assistant","content":[{"type":"thinking","thinking":"reasoning"}]}`,
				`{"role":"user","content":[{"type":"text","text":"`+counters+`","cache_control":`+control+`}]}`)
			cacheGoalHistory(p, []string{live, counters})
			jsonEq(t, block(message(p, 0), 0).vals["cache_control"], jo(t, control), "marker on real history")
			assertTrue(t, !strings.Contains(marshalJSON(msgs(p)[1:], ""), "cache_control"), "no marker after history")
		}
	})

	tw(t, f, "Bedrock relocates a merged live suffix without moving real blocks", func(t *testing.T) {
		p := payload(t, `{"role":"user","content":[{"text":"real history"},{"text":"%LIVE%"},{"text":"counters"},{"cachePoint":{"type":"default","ttl":"1h"}}]}`)
		cacheGoalHistory(p, []string{live, "counters"})
		jsonEq(t, contentOf(message(p, 0)), parseAny(t, `[{"text":"real history"},{"cachePoint":{"type":"default","ttl":"1h"}},{"text":`+liveJSON+`},{"text":"counters"}]`), "merged suffix")
	})

	tw(t, f, "Bedrock skips retained tails separated from the fresh tail by empty content", func(t *testing.T) {
		p := payload(t,
			`{"role":"user","content":[{"text":"history"}]}`,
			`{"role":"user","content":[{"text":"%LIVE%"}]}`,
			`{"role":"assistant","content":[]}`,
			`{"role":"user","content":[{"text":"counters"},{"cachePoint":{"type":"default"}}]}`)
		cacheGoalHistory(p, []string{live, "counters"})
		jsonEq(t, last(contentOf(message(p, 0))), jo(t, `{"cachePoint":{"type":"default"}}`), "cachePoint on history")
		whole := marshalJSON(p, "")
		assertEq(t, strings.Count(whole, "cachePoint"), 1, "exactly one cachePoint")
		assertTrue(t, !strings.Contains(marshalJSON(msgs(p)[1:], ""), "cachePoint"), "none after history")
	})

	tw(t, f, "Bedrock does not relocate another extension's bare cachePoint", func(t *testing.T) {
		p := payload(t, `{"role":"user","content":[{"text":"history"}]}`, `{"role":"user","content":[{"cachePoint":{"type":"default"}}]}`)
		original := clone(p)
		assertTrue(t, cacheGoalHistory(p, []string{live}) == nil, "bare cachePoint untouched")
		jsonEq(t, p, original, "payload unchanged")
	})
}
