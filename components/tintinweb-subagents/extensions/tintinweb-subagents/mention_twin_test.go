package tintinweb_subagents

import (
	"strings"
	"testing"
)

// Twins of test/mention.test.ts: the `@handle` grammar.

func TestMentionGrammarTwins(t *testing.T) {
	const f = "mention"
	trig := func(s string) bool { _, ok := mentionTrigger(s); return ok }
	parse := func(s string) (map[string]string, bool) {
		h, m, ok := parseMention(s)
		return map[string]string{"handle": h, "message": m}, ok
	}
	set := func(names ...string) map[string]bool {
		m := map[string]bool{}
		for _, n := range names {
			m[n] = true
		}
		return m
	}

	tw(t, f, "lowercases so the handle matches how it is typed", func(t *testing.T) { eq(t, handleBase("Explore"), "explore") })
	tw(t, f, "keeps a hyphenated type as-is", func(t *testing.T) { eq(t, handleBase("general-purpose"), "general-purpose") })
	tw(t, f, "reduces anything outside [\\w-] to hyphens, without leaving edge hyphens", func(t *testing.T) {
		eq(t, handleBase("Code Review!"), "code-review")
		eq(t, handleBase("  spaced  out  "), "spaced-out")
	})
	tw(t, f, "always produces something typeable", func(t *testing.T) {
		eq(t, handleBase("!!!"), "agent")
		eq(t, handleBase(""), "agent")
	})
	tw(t, f, "caps a long name so one agent can't own an unreadable row", func(t *testing.T) {
		eq(t, len(handleBase(strings.Repeat("x", 200))), 64)
	})
	tw(t, f, "never leaves a trailing hyphen the cap sliced into", func(t *testing.T) {
		eq(t, strings.HasSuffix(handleBase(strings.Repeat("x", 63)+"   tail"), "-"), false)
	})
	tw(t, f, "only ever produces handles the suggestion trigger can match", func(t *testing.T) {
		for _, typ := range []string{"Explore", "general-purpose", "Code Review!", "!!!", "デバッグ"} {
			eq(t, trig("@"+handleBase(typ)), true)
		}
	})

	tw(t, f, "takes the plain base when it is free", func(t *testing.T) { eq(t, assignHandleIn("explore", set()), "explore") })
	tw(t, f, "numbers from 2 on the first collision", func(t *testing.T) { eq(t, assignHandleIn("explore", set("explore")), "explore-2") })
	tw(t, f, "keeps counting past every taken form", func(t *testing.T) {
		eq(t, assignHandleIn("explore", set("explore", "explore-2")), "explore-3")
	})
	tw(t, f, "never hands out the reserved main handle", func(t *testing.T) { eq(t, assignHandleIn("main", set()), "main-2") })
	tw(t, f, "skips a gap rather than reusing a live handle", func(t *testing.T) {
		eq(t, assignHandleIn("explore", set("explore", "explore-3")), "explore-2")
	})

	types := []string{"general-purpose", "Explore", "Code Review!"}
	resolved := func(h string, ts []string) string { s, _ := resolveHandleToType(h, ts); return s }
	tw(t, f, "finds the type a handle was derived from, whatever its casing", func(t *testing.T) {
		eq(t, resolved("explore", types), "Explore")
		eq(t, resolved("EXPLORE", types), "Explore")
	})
	tw(t, f, "resolves a type whose slug differs from its name", func(t *testing.T) { eq(t, resolved("code-review", types), "Code Review!") })
	tw(t, f, "is exact, not a prefix match — a partial handle must not start an agent", func(t *testing.T) {
		_, a := resolveHandleToType("ex", types)
		_, b := resolveHandleToType("explore-2", types)
		eq(t, [2]bool{a, b}, [2]bool{false, false})
	})
	tw(t, f, "round-trips every registered type", func(t *testing.T) {
		for _, typ := range types {
			eq(t, resolved(handleBase(typ), types), typ)
		}
	})
	tw(t, f, "refuses to resolve the reserved handle, even to a type named for it", func(t *testing.T) {
		_, ok := resolveHandleToType("main", append([]string{"main"}, types...))
		eq(t, ok, false)
	})

	tw(t, f, "recognizes main whatever its casing", func(t *testing.T) {
		eq(t, isReservedHandle("main"), true)
		eq(t, isReservedHandle("MAIN"), true)
	})
	tw(t, f, "leaves every ordinary handle alone", func(t *testing.T) {
		for _, h := range []string{"explore", "mainframe", "main-2", "ma"} {
			eq(t, isReservedHandle(h), false)
		}
	})

	stripped := func(h string) (string, bool) { return stripAgentPrefix(h) }
	tw(t, f, "unwraps Claude Code's manual @agent-<type> spelling", func(t *testing.T) {
		s, ok := stripped("agent-explore")
		eq(t, [2]any{s, ok}, [2]any{"explore", true})
	})
	tw(t, f, "keeps the remainder intact when it is itself prefixed", func(t *testing.T) {
		s, _ := stripped("agent-agent-foo")
		eq(t, s, "agent-foo")
	})
	tw(t, f, "returns nothing when there is no prefix or nothing behind it", func(t *testing.T) {
		for _, h := range []string{"explore", "agent-", "agentexplore"} {
			_, ok := stripped(h)
			eq(t, ok, false)
		}
	})
	tw(t, f, "only unwraps a prefix at the very start", func(t *testing.T) {
		_, ok := stripped("sub-agent-explore")
		eq(t, ok, false)
	})

	tw(t, f, "uses the message as the agent's short label", func(t *testing.T) {
		eq(t, describeMention("find every retry marker"), "find every retry marker")
	})
	tw(t, f, "takes the first line and collapses whitespace", func(t *testing.T) {
		eq(t, describeMention("  audit   the RPC path\nthen report back  "), "audit the RPC path")
	})
	tw(t, f, "clips a long message rather than putting a paragraph in every agent surface", func(t *testing.T) {
		label := describeMention(strings.Repeat("x", 200))
		eq(t, len([]rune(label)), 40)
		eq(t, strings.HasSuffix(label, "…"), true)
	})

	tw(t, f, "splits a leading handle from its message", func(t *testing.T) {
		got, ok := parse("@explore check the RPC path")
		eq(t, ok, true)
		eq(t, got, map[string]string{"handle": "explore", "message": "check the RPC path"})
	})
	tw(t, f, "trims the message and accepts a newline as the separator", func(t *testing.T) {
		got, _ := parse("@explore   spaced   ")
		eq(t, got, map[string]string{"handle": "explore", "message": "spaced"})
		got, _ = parse("@explore\nline1\nline2")
		eq(t, got, map[string]string{"handle": "explore", "message": "line1\nline2"})
	})
	tw(t, f, "rejects a bare handle — that belongs to the main model", func(t *testing.T) {
		for _, s := range []string{"@explore", "@explore ", "@explore \t "} {
			_, ok := parse(s)
			eq(t, ok, false)
		}
	})
	tw(t, f, "rejects a leading file path so pi's @-attachment keeps working", func(t *testing.T) {
		for _, s := range []string{"@src/index.ts summarize this", "@README.md what changed"} {
			_, ok := parse(s)
			eq(t, ok, false)
		}
	})
	tw(t, f, "rejects a mention that is not at the start of the input", func(t *testing.T) {
		for _, s := range []string{"hey @explore look at this", " @explore look at this"} {
			_, ok := parse(s)
			eq(t, ok, false)
		}
	})

	tw(t, f, "is Claude Code's string, byte for byte", func(t *testing.T) {
		eq(t, agentMentionReminder("code-review"),
			"<system-reminder>\nThe user has expressed a desire to invoke the agent \"code-review\". Please invoke the agent appropriately, passing in the required context to it. \n</system-reminder>")
	})
	tw(t, f, "names the agent it was given", func(t *testing.T) {
		eq(t, strings.Contains(agentMentionReminder("Plan"), `invoke the agent "Plan"`), true)
	})
}

// This port's own checks of the grammar: the trigger's boundaries, UTF-16 cursor columns, the JS whitespace set.
func TestMentionTriggerBoundaries(t *testing.T) {
	cases := []struct {
		text    string
		partial string
		ok      bool
	}{
		{"@ex", "ex", true}, {"@", "", true}, {"ask @ex", "ex", true}, {"mail@ex", "", false}, {"@src/", "", false},
		{"=@ex", "", false}, {"。@ex", "ex", true}, {"？@e", "e", true}, {"a\u00a0@ex", "ex", true}, {"@@x", "", false},
		{"@ex-1_2", "ex-1_2", true}, {"", "", false},
	}
	for _, c := range cases {
		p, ok := mentionTrigger(c.text)
		eq(t, [2]any{p, ok}, [2]any{c.partial, c.ok})
	}
	eq(t, utf16Prefix("a😀b@ex", 5), "a😀b@")
	eq(t, utf16Prefix("a😀b", 2), "a") // a column inside a surrogate pair stops before it
	eq(t, utf16Prefix("abc", 10), "abc")
	eq(t, utf16Prefix("abc", 0), "")
	eq(t, handleBase("İx"), "i-x") // JS toLowerCase gives i + U+0307
}
