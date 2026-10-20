package bashanalysis

import (
	"testing"

	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse"
)

func TestEmbeddedOptionValuesPatternFirst(t *testing.T) {
	tw(t, tk, "does not split a non-flag token containing '='", func(t *testing.T) { eq(t, tokensOf(t, "cat a=b"), s("a=b")) })
	tw(t, tk, "keeps only the first '=' as the separator", func(t *testing.T) { contains(t, tokensOf(t, "cat --opt=/tmp/a=b"), "/tmp/a=b") })
	tw(t, tk, "suppresses a recognized pattern flag's embedded value", func(t *testing.T) {
		notContains(t, tokensOf(t, "grep --regexp=/etc/passwd file.txt"), "/etc/passwd")
		notContains(t, tokensOf(t, "sed --expression=/etc/shadow file.txt"), "/etc/shadow")
	})
	tw(t, tk, "suppresses the same pattern in its short-flag form", func(t *testing.T) {
		notContains(t, tokensOf(t, "grep -e /etc/passwd file.txt"), "/etc/passwd")
	})
	tw(t, tk, "still splits an unrecognized option's embedded value", func(t *testing.T) {
		contains(t, tokensOf(t, "grep --exclude-dir=/etc/skel pattern f"), "/etc/skel")
	})
}

func TestExtractCommandWord(t *testing.T) {
	tw(t, tk, "returns a bare head word unchanged", func(t *testing.T) {
		got, ok := ExtractCommandWord(commandNode(t, "grep pattern file.txt"), unrebound)
		eq(t, []any{got, ok}, []any{"grep", true})
	})
	for _, head := range []string{"/usr/bin/grep", "./grep", "../bin/grep"} {
		t.Run("keeps the directory prefix of "+head+", which extractCommandName strips", func(t *testing.T) {
			node := commandNode(t, head+" p file.txt")
			word, _ := ExtractCommandWord(node, unrebound)
			name, _ := ExtractCommandName(node, unrebound)
			eq(t, []string{word, name}, []string{head, "grep"})
		})
	}
}

func TestEffectAttribution(t *testing.T) {
	attributedTokens := func(command string) []tokenEffect {
		return tokenEffectsOf(CollectPathCandidateTokens(parseRoot(t, command), unrebound))
	}
	te := func(token string, effect bashparse.TokenEffect) tokenEffect { return tokenEffect{token, effect} }
	unproven := bashparse.UnprovenEffect
	list := func(v ...tokenEffect) []tokenEffect { return v }
	all := func(tokens []string, effect bashparse.TokenEffect) []tokenEffect {
		out := []tokenEffect{}
		for _, tok := range tokens {
			out = append(out, te(tok, effect))
		}
		return out
	}

	tw(t, tk, "attributes a core word's read to every token it owns", func(t *testing.T) {
		eq(t, attributedTokens("cat /etc/hosts /etc/passwd"), list(te("/etc/hosts", coreRead), te("/etc/passwd", coreRead)))
	})
	tw(t, tk, "attributes a pattern-first command's read to its file arguments", func(t *testing.T) {
		eq(t, attributedTokens("grep needle /etc/hosts"), list(te("/etc/hosts", coreRead)))
	})
	tw(t, tk, "attributes a core word's read to an embedded option value", func(t *testing.T) {
		eq(t, attributedTokens("grep --file=/tmp/patterns target"), list(te("/tmp/patterns", coreRead), te("target", coreRead)))
	})
	tw(t, tk, "proves nothing for a command outside the core", func(t *testing.T) {
		eq(t, attributedTokens("pnpm test /etc/hosts"), list(te("test", unproven), te("/etc/hosts", unproven)))
	})
	tw(t, tk, "proves nothing for a path-qualified core word", func(t *testing.T) {
		eq(t, attributedTokens("/tmp/evil/cat /etc/hosts"), list(te("/etc/hosts", unproven)))
	})
	tw(t, tk, "keeps sed's read claim over a digit argument, which the shell passes as written", func(t *testing.T) {
		eq(t, attributedTokens("sed -n 1p 2 /etc/hosts"), list(te("/etc/hosts", coreRead)))
	})
	tw(t, tk, "retracts a guarded word's claim when an option withdraws it", func(t *testing.T) {
		eq(t, attributedTokens("find /etc -delete"), list(te("/etc", retracted), te("-delete", retracted)))
	})
	tw(t, tk, "retracts find's claim when a variable may spell a withdrawing option", func(t *testing.T) {
		eq(t, attributedTokens("A=-delete; find /etc $A"), all(s("/etc"), retracted))
	})
	tw(t, tk, "retracts find's claim when a quoted variable leads its start point", func(t *testing.T) {
		eq(t, attributedTokens(`find "$dir" -name /etc/x`), all(s("$dir", "-name", "/etc/x"), retracted))
	})
	tw(t, tk, "retracts sort's claim when a quoted variable may spell -o", func(t *testing.T) {
		eq(t, attributedTokens(`sort "$O" /etc/out in`), all(s("$O", "/etc/out", "in"), retracted))
	})
	tw(t, tk, `retracts find's claim when a quoted "$@" follows a literal`, func(t *testing.T) {
		eq(t, attributedTokens(`find /etc "x$@"`), all(s("/etc", "x$@"), retracted))
	})
	tw(t, tk, "retracts find's claim for a quoted variable, which may be a nameref", func(t *testing.T) {
		eq(t, attributedTokens(`find /etc -name "x$y"`), all(s("/etc", "-name", "x$y"), retracted))
	})
	tw(t, tk, "keeps find's claim for a glob behind a literal", func(t *testing.T) {
		eq(t, attributedTokens("find /etc/* -name x"), all(s("/etc/*", "-name", "x"), coreRead))
	})
	tw(t, tk, "keeps find's claim for a quoted glob and a depth limit", func(t *testing.T) {
		eq(t, attributedTokens("find /etc -maxdepth 2 -name '*.ts'"), all(s("/etc", "-maxdepth", "-name", "*.ts"), coreRead))
	})
	tw(t, tk, "proves a write for an output redirect destination", func(t *testing.T) {
		eq(t, attributedTokens("cat /etc/hosts > /tmp/out.txt"), list(te("/etc/hosts", coreRead), te("/tmp/out.txt", syntaxWrite)))
	})
	tw(t, tk, "proves a write for an append redirect even under a core reader", func(t *testing.T) {
		eq(t, attributedTokens("echo hi >> /tmp/log.txt"), list(te("hi", coreRead), te("/tmp/log.txt", syntaxWrite)))
	})
	tw(t, tk, "proves a read for an input redirect destination", func(t *testing.T) {
		eq(t, attributedTokens("pnpm x < /tmp/in.txt"), list(te("x", unproven), te("/tmp/in.txt", syntaxRead)))
	})
	tw(t, tk, "collects no token for a file-descriptor duplication", func(t *testing.T) {
		eq(t, attributedTokens("pnpm x 2>&1"), list(te("x", unproven)))
	})
	tw(t, tk, "gives a nested execution's tokens their own command's attribution", func(t *testing.T) {
		eq(t, attributedTokens("pnpm x > $(cat /etc/shadow)"), list(te("x", unproven), te("/etc/shadow", coreRead)))
	})
	tw(t, tk, "gives an argument-hosted execution's tokens their own attribution", func(t *testing.T) {
		eq(t, attributedTokens(`sed -e "$(cat /etc/shadow)" f.txt`), list(te("/etc/shadow", coreRead), te("f.txt", retracted)))
	})
	tw(t, tk, "attributes each unit of a pipeline separately", func(t *testing.T) {
		eq(t, attributedTokens("cat /etc/hosts | tee /tmp/copy"), list(te("/etc/hosts", coreRead), te("/tmp/copy", unproven)))
	})
	tw(t, tk, "proves nothing for a bare destination", func(t *testing.T) {
		eq(t, attributedTokens("cat <> rw.txt"), list(te("rw.txt", unproven)))
	})
	tw(t, tk, "proves nothing for a home-relative destination", func(t *testing.T) {
		eq(t, attributedTokens("cat <> ~/rw.txt"), list(te("~/rw.txt", unproven)))
	})
	tw(t, tk, "carries the command's proof, not the operator's", func(t *testing.T) {
		eq(t, attributedTokens("grep pat 2>/dev/null f.txt"), list(te("/dev/null", syntaxWrite), te("f.txt", coreRead)))
	})
}

type tokenRole2 struct {
	Token string
	Role  TokenRole
}

func TestTokenRole(t *testing.T) {
	rolesOf := func(command string) []tokenRole2 {
		out := []tokenRole2{}
		for _, tok := range CollectPathCandidateTokens(parseRoot(t, command), unrebound) {
			out = append(out, tokenRole2{tok.Token, tok.Role})
		}
		return out
	}
	list := func(v ...tokenRole2) []tokenRole2 { return v }
	tw(t, tk, "is a redirect destination for an output redirect", func(t *testing.T) {
		eq(t, rolesOf("cat a > out.txt"), list(tokenRole2{"a", RoleOperand}, tokenRole2{"out.txt", RoleRedirectDestination}))
	})
	tw(t, tk, "is a redirect destination for an input redirect", func(t *testing.T) {
		eq(t, rolesOf("sort < in.txt"), list(tokenRole2{"in.txt", RoleRedirectDestination}))
	})
	tw(t, tk, "is an operand when the target is computed at run time", func(t *testing.T) {
		eq(t, rolesOf(`echo hi > "$OUT"`), list(tokenRole2{"hi", RoleOperand}, tokenRole2{"$OUT", RoleOperand}))
	})
	tw(t, tk, "is an operand when the parse could not resolve the redirect", func(t *testing.T) {
		eq(t, rolesOf("cat <> rw.txt"), list(tokenRole2{"rw.txt", RoleOperand}))
	})
	tw(t, tk, "is an operand when an unresolved neighbour leaves a well-formed redirect", func(t *testing.T) {
		eq(t, rolesOf("cat <> ~/rw.txt"), list(tokenRole2{"~/rw.txt", RoleOperand}))
	})
	tw(t, tk, "is an operand when the target is empty", func(t *testing.T) {
		eq(t, rolesOf(`echo hi > ""`), list(tokenRole2{"hi", RoleOperand}, tokenRole2{"", RoleOperand}))
	})
	tw(t, tk, "gives a command hosted in a target its own operands' role", func(t *testing.T) {
		eq(t, rolesOf("echo hi > $(cat /etc/shadow)"), list(tokenRole2{"hi", RoleOperand}, tokenRole2{"/etc/shadow", RoleOperand}))
	})
}
