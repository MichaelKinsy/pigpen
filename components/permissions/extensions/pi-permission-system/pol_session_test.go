package pi_permission_system

import (
	"errors"
	"testing"
)

// Twins of test/session/{active-agent,approval-grant,session-approval,session-identity,session-rules}.test.ts.

type fakeEntriesCtx struct {
	entries []SessionEntryView
	err     error
}

func (c fakeEntriesCtx) GetEntries() ([]SessionEntryView, error) { return c.entries, c.err }

func activeEntry(data any) SessionEntryView {
	return SessionEntryView{"type": "custom", "customType": "active_agent", "data": data}
}

func agentName(t *testing.T, entries ...SessionEntryView) *string {
	t.Helper()
	name, err := GetActiveAgentName(fakeEntriesCtx{entries: entries})
	if err != nil {
		t.Fatal(err)
	}
	return name
}

func TestActiveAgentTwins(t *testing.T) {
	const f = "session/active-agent"
	match := func(s string) any {
		if m := ActiveAgentTagRegex.FindStringSubmatch(s); m != nil {
			return m[1]
		}
		return nil
	}
	tw(t, f, "matches double-quoted name attribute", func(t *testing.T) { eq(t, match(`<active_agent name="my-agent">`), "my-agent", "m") })
	tw(t, f, "matches single-quoted name attribute", func(t *testing.T) { eq(t, match(`<active_agent name='my-agent'>`), "my-agent", "m") })
	tw(t, f, "is case-insensitive", func(t *testing.T) { eq(t, match(`<ACTIVE_AGENT name="bot">`), "bot", "m") })
	tw(t, f, "does not match when tag is absent", func(t *testing.T) { eq(t, match("no tag here"), nil, "m") })

	norm := func(v any) any {
		if s, ok := NormalizeAgentName(v); ok {
			return s
		}
		return nil
	}
	tw(t, f, "returns trimmed string for valid input", func(t *testing.T) { eq(t, norm("  my-agent  "), "my-agent", "n") })
	tw(t, f, "returns null for empty string", func(t *testing.T) { eq(t, norm(""), nil, "n") })
	tw(t, f, "returns null for whitespace-only string", func(t *testing.T) { eq(t, norm("   "), nil, "n") })
	tw(t, f, "returns null for non-string values", func(t *testing.T) {
		for _, v := range []any{nil, 42, jo(t, `{}`)} {
			eq(t, norm(v), nil, "n")
		}
	})

	d := func(name any) map[string]any { return map[string]any{"name": name} }
	tw(t, f, "returns null when session has no entries", func(t *testing.T) { eq(t, agentName(t), (*string)(nil), "name") })
	tw(t, f, "returns null when no active_agent custom entry exists", func(t *testing.T) {
		eq(t, agentName(t, SessionEntryView{"type": "message", "data": d("agent")}), (*string)(nil), "name")
	})
	tw(t, f, "returns agent name from active_agent entry", func(t *testing.T) { eq(t, agentName(t, activeEntry(d("bot"))), sp("bot"), "name") })
	tw(t, f, "last-entry-wins: returns name from the last matching entry", func(t *testing.T) {
		eq(t, agentName(t, activeEntry(d("first")), activeEntry(d("last"))), sp("last"), "name")
	})
	tw(t, f, "entry with name: null resets agent name to null", func(t *testing.T) {
		eq(t, agentName(t, activeEntry(d("bot")), activeEntry(d(nil))), (*string)(nil), "name")
	})
	tw(t, f, "skips entries with whitespace-only name and continues scanning", func(t *testing.T) {
		// "   " normalizes to null: not a sentinel reset, keeps scanning backwards
		eq(t, agentName(t, activeEntry(d("first")), activeEntry(d("   "))), sp("first"), "name")
	})
	tw(t, f, "ignores entries with wrong customType", func(t *testing.T) {
		eq(t, agentName(t, SessionEntryView{"type": "custom", "customType": "something_else", "data": d("bot")}), (*string)(nil), "name")
	})
	tw(t, f, "ignores entries with wrong type", func(t *testing.T) {
		eq(t, agentName(t, SessionEntryView{"type": "tool_call", "customType": "active_agent", "data": d("bot")}), (*string)(nil), "name")
	})

	prompt := GetActiveAgentNameFromSystemPrompt
	tw(t, f, "returns null for undefined system prompt", func(t *testing.T) { eq(t, prompt(""), (*string)(nil), "name") })
	tw(t, f, "returns null for empty system prompt", func(t *testing.T) { eq(t, prompt(""), (*string)(nil), "name") })
	tw(t, f, "returns null when tag is absent", func(t *testing.T) { eq(t, prompt("You are a helpful assistant."), (*string)(nil), "name") })
	tw(t, f, "extracts agent name from tag in system prompt", func(t *testing.T) {
		eq(t, prompt("You are helpful.\n<active_agent name=\"my-bot\">\nDo work."), sp("my-bot"), "name")
	})
	tw(t, f, "returns null when tag name is empty", func(t *testing.T) { eq(t, prompt(`<active_agent name="">`), (*string)(nil), "name") })
	tw(t, f, "trims whitespace from extracted name", func(t *testing.T) { eq(t, prompt(`<active_agent name="  trimmed  ">`), sp("trimmed"), "name") })
}

// The original lets the session manager's throw propagate; the port returns it, and a caller must not read it as "no agent".
func TestActiveAgentSessionManagerErrorIsReturned(t *testing.T) {
	_, err := GetActiveAgentName(fakeEntriesCtx{err: errors.New("boom")})
	if err == nil {
		t.Fatal("the error was swallowed")
	}
}

// JavaScript's \s, spelled out for the tag regex: RE2's is ASCII only.
func TestActiveAgentTagRegexUsesJavaScriptWhitespace(t *testing.T) {
	for _, sep := range []string{" ", "\t", "\n", "\u00a0", "\u2003", "\ufeff", "\u3000"} {
		if got := GetActiveAgentNameFromSystemPrompt("<active_agent" + sep + `name="a">`); got == nil || *got != "a" {
			t.Errorf("separator %q: got %v", sep, got)
		}
	}
	// \s+ needs at least one; the quotes may differ at the two ends; the capture may span a newline.
	eq(t, GetActiveAgentNameFromSystemPrompt(`<active_agentname="a">`), (*string)(nil), "no separator")
	eq(t, GetActiveAgentNameFromSystemPrompt(`<active_agent name="a'>`), sp("a"), "mixed quotes")
	eq(t, GetActiveAgentNameFromSystemPrompt("<active_agent name=\"a\nb\">"), sp("a\nb"), "newline in name")
	eq(t, GetActiveAgentNameFromSystemPrompt(`<active_agent name="a"`), (*string)(nil), "unterminated tag")
	eq(t, GetActiveAgentNameFromSystemPrompt(`<active_agent name="a" x="1">`), sp("a"), "extra attributes")
}

func TestApprovalGrantTwins(t *testing.T) {
	const f = "session/approval-grant"
	g := func(s, p string) ApprovalGrant { return ApprovalGrant{Surface: s, Pattern: p} }
	tw(t, f, "folds a directional surface to its bare family, keeping the pattern", func(t *testing.T) {
		eq(t, WidenGrant(g("external_directory_write", "/tmp/*")), g("external_directory", "/tmp/*"), "g")
	})
	tw(t, f, "folds the other direction the same way", func(t *testing.T) {
		eq(t, WidenGrant(g("path_read", "/tmp/*")), g("path", "/tmp/*"), "g")
	})
	tw(t, f, "leaves a grant that already names a family alone", func(t *testing.T) {
		eq(t, WidenGrant(g("external_directory", "/tmp/*")), g("external_directory", "/tmp/*"), "g")
	})
	tw(t, f, "leaves a non-directional surface alone", func(t *testing.T) { eq(t, WidenGrant(g("bash", "git *")), g("bash", "git *"), "g") })

	proven := func(grants ...ApprovalGrant) any {
		if d, ok := ProvenDirectionOf(grants); ok {
			return string(d)
		}
		return nil
	}
	tw(t, f, "names the direction a single directional grant proves", func(t *testing.T) {
		eq(t, proven(g("external_directory_write", "/tmp/*")), "write", "d")
	})
	tw(t, f, "names the shared direction when every grant agrees", func(t *testing.T) {
		eq(t, proven(g("external_directory_read", "/tmp/a/*"), g("external_directory_read", "/tmp/b/*"), g("external_directory_read", "/tmp/c/*")), "read", "d")
	})
	tw(t, f, "answers null when the grants disagree", func(t *testing.T) {
		eq(t, proven(g("external_directory_read", "/tmp/a/*"), g("external_directory_write", "/tmp/b/*")), nil, "d")
	})
	tw(t, f, "answers null when a later grant is non-directional", func(t *testing.T) {
		eq(t, proven(g("external_directory_read", "/tmp/a/*"), g("external_directory", "/tmp/b/*")), nil, "d")
	})
	tw(t, f, "answers null when the only grant is non-directional", func(t *testing.T) { eq(t, proven(g("bash", "git *")), nil, "d") })
	tw(t, f, "answers null for no grants at all", func(t *testing.T) { eq(t, proven(), nil, "d") })

	tw(t, f, "names one target per distinct pattern", func(t *testing.T) {
		eq(t, GrantTargets([]ApprovalGrant{g("external_directory_read", "/r/*"), g("external_directory_read", "/r/*")}), []string{"/r/*"}, "t")
	})
	tw(t, f, "folds a directory's exact grant into its contents grant", func(t *testing.T) {
		eq(t, GrantTargets([]ApprovalGrant{g("external_directory_read", "/r/a"), g("external_directory_read", "/r/a/*")}), []string{"/r/a/*"}, "t")
	})
	tw(t, f, "folds a win32 directory pair spelled with backslashes", func(t *testing.T) {
		eq(t, GrantTargets([]ApprovalGrant{g("path", `C:\r\a`), g("path", `C:\r\a\*`)}), []string{`C:\r\a\*`}, "t")
	})
	tw(t, f, "keeps a pair apart when the two grants name different surfaces", func(t *testing.T) {
		eq(t, GrantTargets([]ApprovalGrant{g("path_read", "/r/a"), g("path_write", "/r/a/*")}), []string{"/r/a", "/r/a/*"}, "t")
	})
	tw(t, f, "keeps an exact grant whose sibling is not its own contents", func(t *testing.T) {
		eq(t, GrantTargets([]ApprovalGrant{g("path_read", "/r/a"), g("path_read", "/r/ab/*")}), []string{"/r/a", "/r/ab/*"}, "t")
	})
}

func TestIsSessionGrantWidth(t *testing.T) {
	for v, want := range map[any]bool{"proven": true, "family": true, "": false, "Family": false, "wide": false, nil: false, 1: false} {
		eq(t, IsSessionGrantWidth(v), want, "width")
	}
}

func TestSessionApprovalTwins(t *testing.T) {
	const f = "session/session-approval"
	g := func(s, p string) ApprovalGrant { return ApprovalGrant{Surface: s, Pattern: p} }
	tw(t, f, "stores one grant pairing the surface with the pattern", func(t *testing.T) {
		eq(t, SessionApprovalSingle("bash", "git *").Grants, []ApprovalGrant{g("bash", "git *")}, "grants")
	})
	tw(t, f, "is recordable", func(t *testing.T) {
		eq(t, SessionApprovalSingle("bash", "git *").IsRecordable(), true, "recordable")
	})
	tw(t, f, "stores one grant per pattern, all on the given surface", func(t *testing.T) {
		eq(t, SessionApprovalForPatterns("path_read", []string{"/r/a", "/r/a/*"}).Grants, []ApprovalGrant{g("path_read", "/r/a"), g("path_read", "/r/a/*")}, "grants")
	})
	tw(t, f, "stores every grant in order", func(t *testing.T) {
		want := []ApprovalGrant{g("external_directory_read", "/outside/a/*"), g("external_directory_write", "/outside/b/*")}
		eq(t, SessionApprovalForGrants(want).Grants, want, "grants")
	})
	tw(t, f, "keeps a surface per pattern rather than one for all", func(t *testing.T) {
		a := SessionApprovalForGrants([]ApprovalGrant{g("external_directory_read", "/outside/a/*"), g("external_directory_write", "/outside/b/*")})
		surfaces := map[string]bool{}
		for _, x := range a.Grants {
			surfaces[x.Surface] = true
		}
		eq(t, surfaces, map[string]bool{"external_directory_read": true, "external_directory_write": true}, "surfaces")
	})
	tw(t, f, "is recordable", func(t *testing.T) {
		eq(t, SessionApprovalForGrants([]ApprovalGrant{g("external_directory", "/outside/a/*")}).IsRecordable(), true, "recordable")
	})
	tw(t, f, "defensive copy — mutating the source array does not affect grants", func(t *testing.T) {
		source := []ApprovalGrant{g("external_directory", "/a/*")}
		a := SessionApprovalForGrants(source)
		source[0].Pattern = "/mutated/*"
		source = append(source, g("external_directory", "/b/*"))
		_ = source
		eq(t, a.Grants, []ApprovalGrant{g("external_directory", "/a/*")}, "grants")
	})
	tw(t, f, "is not recordable", func(t *testing.T) {
		eq(t, SessionApprovalForGrants(nil).IsRecordable(), false, "recordable")
	})
	tw(t, f, "returns the single grant for a single approval", func(t *testing.T) {
		eq(t, SessionApprovalSingle("bash", "git *").ToForwardedData(), ForwardedSessionApproval{Grants: []ApprovalGrant{g("bash", "git *")}}, "data")
	})
	tw(t, f, "returns every grant, each with its own surface", func(t *testing.T) {
		want := []ApprovalGrant{g("external_directory_read", "/outside/a/*"), g("external_directory_write", "/outside/b/*")}
		eq(t, SessionApprovalForGrants(want).ToForwardedData(), ForwardedSessionApproval{Grants: want}, "data")
	})
	tw(t, f, "defensive copy — mutating the result grants does not affect the approval", func(t *testing.T) {
		a := SessionApprovalSingle("bash", "git *")
		data := a.ToForwardedData()
		data.Grants[0].Pattern = "mutated"
		data.Grants = append(data.Grants, g("bash", "rm *"))
		eq(t, a.Grants, []ApprovalGrant{g("bash", "git *")}, "grants")
	})
	tw(t, f, "returns itself at the proven width", func(t *testing.T) {
		a := SessionApprovalSingle("external_directory_write", "/tmp/*")
		eq(t, a.AtWidth("proven") == a, true, "same")
	})
	tw(t, f, "folds a directional grant to its family at the family width", func(t *testing.T) {
		eq(t, SessionApprovalSingle("external_directory_write", "/tmp/*").AtWidth("family").Grants, []ApprovalGrant{g("external_directory", "/tmp/*")}, "grants")
	})
	tw(t, f, "folds every grant individually, keeping each pattern", func(t *testing.T) {
		a := SessionApprovalForGrants([]ApprovalGrant{g("external_directory_read", "/outside/a/*"), g("path_write", "/outside/b/*")})
		eq(t, a.AtWidth("family").Grants, []ApprovalGrant{g("external_directory", "/outside/a/*"), g("path", "/outside/b/*")}, "grants")
	})
	tw(t, f, "leaves a non-directional grant alone at the family width", func(t *testing.T) {
		eq(t, SessionApprovalSingle("bash", "git *").AtWidth("family").Grants, []ApprovalGrant{g("bash", "git *")}, "grants")
	})
	tw(t, f, "does not mutate the approval it widens", func(t *testing.T) {
		a := SessionApprovalSingle("external_directory_write", "/tmp/*")
		a.AtWidth("family")
		eq(t, a.Grants, []ApprovalGrant{g("external_directory_write", "/tmp/*")}, "grants")
	})
}

type fakeSessionManager struct{ id func() (string, error) }

func (m fakeSessionManager) GetSessionID() (string, error) { return m.id() }

func TestSessionIdentityTwins(t *testing.T) {
	const f = "session/session-identity"
	tw(t, f, "returns the host's session id", func(t *testing.T) {
		id, ok := ReadSessionID(fakeSessionManager{id: func() (string, error) { return "session-abc", nil }})
		eq(t, []any{id, ok}, []any{"session-abc", true}, "id")
	})
	tw(t, f, "returns null for an empty session id", func(t *testing.T) {
		_, ok := ReadSessionID(fakeSessionManager{id: func() (string, error) { return "", nil }})
		eq(t, ok, false, "ok")
	})
	tw(t, f, "returns null when the host throws", func(t *testing.T) {
		calls := 0
		_, ok := ReadSessionID(fakeSessionManager{id: func() (string, error) { calls++; return "", errors.New("session id unavailable") }})
		eq(t, []any{ok, calls}, []any{false, 1}, "ok, calls")
	})
	tw(t, f, "returns null when the host exposes no session id at all", func(t *testing.T) {
		// no session manager at all is the closest Go shape of `sessionManager: {}`
		_, ok := ReadSessionID(nil)
		eq(t, ok, false, "ok")
	})
}

func TestSessionRulesTwins(t *testing.T) {
	const f = "session/session-rules"
	sess := func(surface, pattern string) Rule { return rule(surface, pattern, "allow", "session", "session") }
	pairs := func(rs Ruleset) [][2]string {
		out := [][2]string{}
		for _, r := range rs {
			out = append(out, [2]string{r.Surface, r.Pattern})
		}
		return out
	}
	posix := PosixPathFlavor
	action := func(rs *SessionRules, surface, value string) string {
		return Evaluate(surface, value, rs.GetRuleset(), posix, "").Action
	}

	tw(t, f, "returns an empty ruleset initially", func(t *testing.T) { eq(t, (&SessionRules{}).GetRuleset(), Ruleset{}, "rules") })
	tw(t, f, "returns a ruleset containing approved rules", func(t *testing.T) {
		r := &SessionRules{}
		r.Approve("bash", "git *")
		eq(t, r.GetRuleset(), Ruleset{sess("bash", "git *")}, "rules")
	})
	tw(t, f, "returns a defensive copy — mutations do not affect internal state", func(t *testing.T) {
		r := &SessionRules{}
		r.Approve("external_directory_read", "/other/project/*")
		cp := r.GetRuleset()
		cp = append(cp, rule("bash", "*", "deny", "", "session"))
		cp[0].Pattern = "mutated"
		eq(t, len(r.GetRuleset()), 1, "len")
		eq(t, r.GetRuleset()[0].Pattern, "/other/project/*", "pattern")
	})
	tw(t, f, "accumulates multiple approved patterns", func(t *testing.T) {
		r := &SessionRules{}
		r.Approve("external_directory_read", "/project-a/*")
		r.Approve("external_directory_read", "/project-b/*")
		eq(t, len(r.GetRuleset()), 2, "len")
	})

	tw(t, f, "records an approval on a bare family surface as one rule per member", func(t *testing.T) {
		r := &SessionRules{}
		r.Approve("external_directory", "/other/project/*")
		eq(t, r.GetRuleset(), Ruleset{sess("external_directory_read", "/other/project/*"), sess("external_directory_write", "/other/project/*")}, "rules")
	})
	tw(t, f, "records an approval on a directional surface as a single rule", func(t *testing.T) {
		r := &SessionRules{}
		r.Approve("path_read", "/other/project/*")
		eq(t, r.GetRuleset(), Ruleset{sess("path_read", "/other/project/*")}, "rules")
	})
	tw(t, f, "expands a multi-pattern approval on a bare family surface", func(t *testing.T) {
		r := &SessionRules{}
		r.RecordSessionApproval(SessionApprovalForGrants([]ApprovalGrant{{"path", "/a/*"}, {"path", "/b/*"}}))
		eq(t, pairs(r.GetRuleset()), [][2]string{{"path_read", "/a/*"}, {"path_write", "/a/*"}, {"path_read", "/b/*"}, {"path_write", "/b/*"}}, "rules")
	})
	tw(t, f, "covers both directions when evaluated, so an approval is not half-granted", func(t *testing.T) {
		r := &SessionRules{}
		r.Approve("path", "/other/project/*")
		for _, surface := range []string{"path_read", "path_write"} {
			eq(t, action(r, surface, "/other/project/src/foo.ts"), "allow", surface)
		}
	})

	tw(t, f, "removes all session rules", func(t *testing.T) {
		r := &SessionRules{}
		r.Approve("external_directory_read", "/other/project/*")
		r.Approve("external_directory_read", "/another/path/*")
		r.Clear()
		eq(t, r.GetRuleset(), Ruleset{}, "rules")
	})
	tw(t, f, "allows new approvals after clearing", func(t *testing.T) {
		r := &SessionRules{}
		r.Approve("external_directory_read", "/old/path/*")
		r.Clear()
		r.Approve("external_directory_read", "/new/path/*")
		eq(t, len(r.GetRuleset()), 1, "len")
		eq(t, r.GetRuleset()[0].Pattern, "/new/path/*", "pattern")
	})

	tw(t, f, "satisfies the SessionApprovalRecorder interface", func(t *testing.T) {
		var r SessionApprovalRecorder = &SessionRules{}
		eq(t, r != nil, true, "recorder")
	})
	tw(t, f, "records a single-pattern approval as one rule", func(t *testing.T) {
		r := &SessionRules{}
		r.RecordSessionApproval(SessionApprovalSingle("bash", "git *"))
		eq(t, r.GetRuleset(), Ruleset{sess("bash", "git *")}, "rules")
	})
	tw(t, f, "records a multi-pattern approval as one rule per pattern", func(t *testing.T) {
		r := &SessionRules{}
		r.RecordSessionApproval(SessionApprovalForGrants([]ApprovalGrant{{"external_directory_read", "/outside/a/*"}, {"external_directory_read", "/outside/b/*"}}))
		eq(t, len(r.GetRuleset()), 2, "len")
		eq(t, r.GetRuleset()[0].Pattern, "/outside/a/*", "first")
		eq(t, r.GetRuleset()[1].Pattern, "/outside/b/*", "second")
	})
	tw(t, f, "records each rule with the correct surface", func(t *testing.T) {
		r := &SessionRules{}
		r.RecordSessionApproval(SessionApprovalForGrants([]ApprovalGrant{{"external_directory_read", "/outside/a/*"}, {"external_directory_read", "/outside/b/*"}}))
		for _, x := range r.GetRuleset() {
			eq(t, x.Surface, "external_directory_read", "surface")
		}
	})
	tw(t, f, "records each grant on its own surface when they disagree", func(t *testing.T) {
		r := &SessionRules{}
		r.RecordSessionApproval(SessionApprovalForGrants([]ApprovalGrant{{"external_directory_read", "/outside/*"}, {"external_directory_write", "/elsewhere/*"}}))
		eq(t, pairs(r.GetRuleset()), [][2]string{{"external_directory_read", "/outside/*"}, {"external_directory_write", "/elsewhere/*"}}, "rules")
	})
	tw(t, f, "records nothing for an empty grant list", func(t *testing.T) {
		r := &SessionRules{}
		r.RecordSessionApproval(SessionApprovalForGrants(nil))
		eq(t, r.GetRuleset(), Ruleset{}, "rules")
	})

	tw(t, f, "returns allow for a path under an approved directory", func(t *testing.T) {
		r := &SessionRules{}
		r.Approve("external_directory_read", "/other/project/*")
		eq(t, action(r, "external_directory_read", "/other/project/src/foo.ts"), "allow", "action")
	})
	tw(t, f, "returns ask (default) for a path outside approved directories", func(t *testing.T) {
		r := &SessionRules{}
		r.Approve("external_directory_read", "/other/project/*")
		// No rule matches: Evaluate returns the synthetic rule with default action "ask"
		eq(t, action(r, "external_directory_read", "/other/unrelated/file.ts"), "ask", "action")
	})
	tw(t, f, "does not match a sibling directory that shares a string prefix", func(t *testing.T) {
		r := &SessionRules{}
		r.Approve("external_directory_read", "/other/project/*")
		eq(t, action(r, "external_directory_read", "/other/project-b/foo.ts"), "ask", "action")
	})
	tw(t, f, "matches the directory itself (trailing slash)", func(t *testing.T) {
		r := &SessionRules{}
		r.Approve("external_directory_read", "/other/project/src/*")
		eq(t, action(r, "external_directory_read", "/other/project/src/"), "allow", "action")
	})
	tw(t, f, "handles multiple approved directories", func(t *testing.T) {
		r := &SessionRules{}
		r.Approve("external_directory_read", "/project-a/*")
		r.Approve("external_directory_read", "/project-b/*")
		eq(t, action(r, "external_directory_read", "/project-a/foo.ts"), "allow", "a")
		eq(t, action(r, "external_directory_read", "/project-b/bar.ts"), "allow", "b")
		eq(t, action(r, "external_directory_read", "/project-c/baz.ts"), "ask", "c")
	})
	tw(t, f, "does not match a different surface", func(t *testing.T) {
		r := &SessionRules{}
		r.Approve("external_directory_read", "/other/project/*")
		eq(t, action(r, "bash", "/other/project/foo.ts"), "ask", "action")
	})
	tw(t, f, "returns allow after clearing and re-approving", func(t *testing.T) {
		r := &SessionRules{}
		r.Approve("external_directory_read", "/old/project/*")
		r.Clear()
		r.Approve("external_directory_read", "/new/project/*")
		eq(t, action(r, "external_directory_read", "/old/project/file.ts"), "ask", "old")
		eq(t, action(r, "external_directory_read", "/new/project/file.ts"), "allow", "new")
	})
}
