package pi_permission_system

import (
	"reflect"
	"runtime"
	"testing"
)

func TestBashProgramExternalAccesses(t *testing.T) {
	cwd := bpCwd
	norm := func(t *testing.T) *PathNormalizer { return bpNormalizer(t, cwd) }
	// externalValuesOf is the external paths a command reaches, in their as-typed form.
	externalValuesOf := func(t *testing.T, command string, at ...*PathNormalizer) []string {
		t.Helper()
		n := norm(t)
		if len(at) > 0 {
			n = at[0]
		}
		return externalValues(bpParse(t, command, n))
	}
	type pe struct {
		Path   string
		Effect TokenEffect
	}
	externalsOf := func(t *testing.T, command string) []pe {
		t.Helper()
		out := []pe{}
		for _, e := range bpParse(t, command, norm(t)).ExternalAccesses() {
			out = append(out, pe{e.Path.Value(), e.Effect})
		}
		return out
	}
	eqExt := func(t *testing.T, got []pe, want ...pe) {
		t.Helper()
		if len(got) == 0 && len(want) == 0 {
			return
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %#v, want %#v", got, want)
		}
	}

	tw(t, bpxFile, "returns absolute paths resolving outside cwd", func(t *testing.T) {
		if !contains(externalValuesOf(t, "cat /etc/hosts"), "/etc/hosts") {
			t.Error("/etc/hosts is not external")
		}
	})

	// the rest of a heredoc's line
	tw(t, bpxFile, "projects a tail redirect's target with its operator's effect", func(t *testing.T) {
		eqExt(t, externalsOf(t, "cat <<EOF > /tmp/o\nb\nEOF"), pe{"/tmp/o", TokenEffect{EffectWrite, SourceSyntax}})
	})
	tw(t, bpxFile, "folds a current-shell cd in a list after a piped tail", func(t *testing.T) {
		// Unfolded, `../secret` would resolve against the cwd instead: `/projects/secret`.
		eqStrings(t, externalValuesOf(t, "cat <<EOF | true && cd /outside && cat ../secret\nb\nEOF"), "/outside", "/secret")
	})
	// the words after a heredoc
	tw(t, bpxFile, "projects a word as the command's own operand", func(t *testing.T) {
		eqExt(t, externalsOf(t, "cat <<EOF ~/x/in\nb\nEOF"), pe{join(homePath(), "x/in"), TokenEffect{EffectRead, SourceCore}})
	})

	// a redirect's target is projected by its role (#609)
	type pv struct {
		Path   string
		Effect AttributedEffect
	}
	roleExternals := func(t *testing.T, command string) []pv {
		out := []pv{}
		for _, e := range bpParse(t, command, norm(t)).ExternalAccesses() {
			out = append(out, pv{e.Path.Value(), e.Effect.Effect})
		}
		return out
	}
	eqPv := func(t *testing.T, got []pv, want ...pv) {
		t.Helper()
		if len(got) == 0 && len(want) == 0 {
			return
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %#v, want %#v", got, want)
		}
	}
	tw(t, bpxFile, "flags a bare output target after a non-literal cd", func(t *testing.T) {
		eqPv(t, roleExternals(t, `cd "$D" && echo hi > out.txt`), pv{join(cwd, "out.txt"), EffectWrite})
	})
	tw(t, bpxFile, "flags a bare input target after a non-literal cd", func(t *testing.T) {
		eqPv(t, roleExternals(t, `cd "$D" && sort < in.txt`), pv{join(cwd, "in.txt"), EffectRead})
	})
	tw(t, bpxFile, "leaves a bare target inside a known working directory alone", func(t *testing.T) {
		eqPv(t, roleExternals(t, "echo hi > out.txt"))
	})

	// a program that reassigns HOME or PWD
	each(t, "no longer projects %s under its startup value (%s)", func(t *testing.T) {
		for _, command := range []string{
			`HOME=/etc; cat "$HOME/shadow"`,
			`export HOME=/etc; cat "$HOME/shadow"`,
			`unset HOME; cat "$HOME/shadow"`,
			`cat "$HOME/shadow"; HOME=/etc`,
			`PWD=/; cat "$PWD/../shadow"`,
			"HOME=/etc; cat ~/shadow",
			`read HOME; cat "$HOME/shadow"`,
			`source f; cat "$HOME/shadow"`,
		} {
			eqExt(t, externalsOf(t, command))
		}
	})
	tw(t, bpxFile, "counts an assignment only a salvaged region holds", func(t *testing.T) {
		// The grammar cannot parse a heredoc followed by `; ...` on its line, so the assignment is recovered only by the salvage.
		eqExt(t, externalsOf(t, "cat <<EOF ; HOME=/etc\nx\nEOF\ncat \"$HOME/shadow\""))
	})
	tw(t, bpxFile, "projects a for loop's word list but not the loop variable's reference", func(t *testing.T) {
		eqExt(t, externalsOf(t, `for HOME in /etc; do cat "$HOME/shadow"; done`), pe{"/etc", TokenEffect{EffectUnproven, SourceUnproven}})
	})
	tw(t, bpxFile, "withdraws find's read claim for a reassigned $HOME that may spell an option", func(t *testing.T) {
		eqExt(t, externalsOf(t, `HOME=-delete; find /etc "$HOME"`), externalsOf(t, `find /etc "$X"`)...)
	})
	tw(t, bpxFile, "withdraws find's read claim for a tilde once HOME is reassigned", func(t *testing.T) {
		eqExt(t, externalsOf(t, "HOME=-delete; find /etc ~"), externalsOf(t, `find /etc "$X"`)...)
	})
	// an inherited HOME that begins with a dash
	tw(t, bpxFile, "withdraws find's read claim for a tilde", func(t *testing.T) {
		t.Setenv("HOME", "-delete")
		got := externalsOf(t, "find /etc ~")
		if len(got) != 1 || got[0].Effect != (TokenEffect{EffectUnproven, SourceRetracted}) {
			t.Errorf("got %#v", got)
		}
	})
	// a program that leaves both alone
	each(t, "still projects %s", func(t *testing.T) {
		for _, row := range [][2]string{
			{`cat "$HOME/shadow"`, join(homePath(), "shadow")},
			{`env -i HOME="$HOME" cat "$HOME/shadow"`, join(homePath(), "shadow")},
			{`cat "$PWD/../shadow"`, "/projects/shadow"},
			{"grep HOME ~/.bashrc", join(homePath(), ".bashrc")},
		} {
			got := externalsOf(t, row[0])
			if len(got) != 1 || got[0].Path != row[1] {
				t.Errorf("%q: got %#v, want path %q", row[0], got, row[1])
			}
		}
	})
	tw(t, bpxFile, "still proves sed's read of a tilde path", func(t *testing.T) {
		eqExt(t, externalsOf(t, "sed -n p ~/x"), pe{join(homePath(), "x"), TokenEffect{EffectRead, SourceCore}})
	})
	tw(t, bpxFile, "still proves find's read of $HOME", func(t *testing.T) {
		eqExt(t, externalsOf(t, `find "$HOME/other"`), pe{join(homePath(), "other"), TokenEffect{EffectRead, SourceCore}})
	})

	// operands a statement names directly (#839)
	tw(t, bpxFile, "flags a for loop's absolute word-list operand", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "for f in /etc/shadow; do cat $f; done"), "/etc/shadow")
	})
	tw(t, bpxFile, "flags a for loop's home-relative word-list operand", func(t *testing.T) {
		// The issue's motivating repro: the body carries only `$f`, so the word list is the sole place the literal appears.
		eqStrings(t, externalValuesOf(t, "for f in ~/other/secret; do cat $f; done"), join(homePath(), "other/secret"))
	})
	tw(t, bpxFile, "flags an absolute case subject", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "case /etc/shadow in a) echo b;; esac"), "/etc/shadow")
	})
	tw(t, bpxFile, "leaves an in-cwd word-list operand off the external slice", func(t *testing.T) {
		eqExt(t, externalsOf(t, "for f in src/main.ts; do echo; done"))
	})

	// operands of a command hosted in a quoted argument (#945)
	tw(t, bpxFile, "flags the operand of a substitution in a consumed flag argument", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, `sed -e "$(cat /etc/shadow)" f.txt`), "/etc/shadow")
	})
	tw(t, bpxFile, "flags the operand of a substitution in a generic command's argument", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, `echo "$(cat /etc/shadow)"`), "/etc/shadow")
	})

	// glob-bearing path tokens (#821)
	each(t, "projects %s outside the tree", func(t *testing.T) {
		for _, row := range [][2]string{
			{"cat /etc/[p]asswd", "/etc/[p]asswd"}, {"ls /et[c]/pa*", "/et[c]/pa*"}, {"rm -rf /tmp/tmp.*", "/tmp/tmp.*"},
		} {
			eqStrings(t, externalValuesOf(t, row[0]), row[1])
		}
	})
	tw(t, bpxFile, "projects a bracket glob in a home-relative path", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "cat ~/.ssh/[i]d_rsa"), join(homePath(), ".ssh/[i]d_rsa"))
	})
	tw(t, bpxFile, "leaves a glob resolving within cwd alone", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "cat /projects/my-app/src/[i]ndex.ts"))
	})

	// flag spellings of a pattern-first command (#823)
	each(t, "projects the file operand behind %s", func(t *testing.T) {
		for _, command := range []string{
			"grep -A 3 pattern /etc/passwd", "grep -A $N pattern /etc/passwd", "grep --regexp=harmless /etc/passwd",
			"grep -eharmless /etc/passwd", "sed -i 's/a/b/' /etc/passwd",
		} {
			eqStrings(t, externalValuesOf(t, command), "/etc/passwd")
		}
	})
	tw(t, bpxFile, "does not project a pattern flag's own value", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "grep --regexp=/etc/passwd file.txt"))
	})

	// operands of nested commands hosted in a redirect (#741)
	each(t, "projects an operand hosted in %s", func(t *testing.T) {
		for _, command := range []string{
			"echo hi > $(cat /etc/shadow)", "echo hi >> $(cat /etc/shadow)", "cat < <(cat /etc/shadow)",
			"echo hi > ${DIR}/$(cat /etc/shadow)",
		} {
			if !contains(externalValuesOf(t, command), "/etc/shadow") {
				t.Errorf("%q: /etc/shadow is not external", command)
			}
		}
	})
	tw(t, bpxFile, "still projects a plain redirect destination", func(t *testing.T) {
		if !contains(externalValuesOf(t, "echo hi > /etc/passwd"), "/etc/passwd") {
			t.Error("/etc/passwd is not external")
		}
	})

	// bare tokens escaping the tree via symlink (#645): real temporary directories (canonical: the macOS tmpdir is a symlink)
	probe := func(t *testing.T) (string, *PathNormalizer) {
		t.Helper()
		root := realTmp(t)
		return root, NewPathNormalizer(PathFlavorForPlatform(runtime.GOOS), root)
	}
	tw(t, bpxFile, "flags an in-project bare symlink whose target is outside cwd", func(t *testing.T) {
		root, n := probe(t)
		// printf 'test' > /tmp/pi-permission-test-secret; ln -s /tmp/pi-permission-test-secret outside-link; cat outside-link
		secret := writeFile(t, realTmp(t), "pi-permission-test-secret", "s")
		symlink(t, root, "outside-link", secret)
		if !contains(externalBoundaries(bpParse(t, "cat outside-link", n)), secret) {
			t.Errorf("boundaries %q lack %q", externalBoundaries(bpParse(t, "cat outside-link", n)), secret)
		}
	})
	tw(t, bpxFile, "does not flag a bare token resolving inside cwd", func(t *testing.T) {
		root, n := probe(t)
		writeFile(t, root, "inside.txt", "x")
		if got := bpParse(t, "cat inside.txt", n).ExternalAccesses(); len(got) != 0 {
			t.Errorf("got %#v", got)
		}
	})
	tw(t, bpxFile, "does not flag a bare word naming nothing", func(t *testing.T) {
		_, n := probe(t)
		if got := bpParse(t, "git status", n).ExternalAccesses(); len(got) != 0 {
			t.Errorf("got %#v", got)
		}
	})
	tw(t, bpxFile, "flags a bare symlink to an outside directory", func(t *testing.T) {
		root, n := probe(t)
		outside := realTmp(t)
		symlink(t, root, "vault", outside)
		if !contains(externalBoundaries(bpParse(t, "ls vault", n)), outside) {
			t.Errorf("boundaries %q lack %q", externalBoundaries(bpParse(t, "ls vault", n)), outside)
		}
	})
	tw(t, bpxFile, "flags a symlink whose name carries an in-segment ..", func(t *testing.T) {
		root, n := probe(t)
		secret := writeFile(t, realTmp(t), "secret", "s")
		symlink(t, root, "v1..v2", secret)
		eqStrings(t, externalBoundaries(bpParse(t, "cat v1..v2", n)), secret)
	})

	tw(t, bpxFile, "flags a path embedded in a long option (#645)", func(t *testing.T) {
		// `grep --file=...` under an allowing `grep *` rule: the flag token is rejected by the shape prelude, so the value is
		// split out at collection and classified on its own.
		if !contains(externalValuesOf(t, "grep --file=/tmp/pi-permission-patterns target"), "/tmp/pi-permission-patterns") {
			t.Error("the embedded path is not external")
		}
	})
	tw(t, bpxFile, "excludes paths within cwd", func(t *testing.T) {
		eqExt(t, externalsOf(t, "cat src/index.ts"))
	})

	// win32 projection (injected platform)
	win := func(t *testing.T) *PathNormalizer {
		mockRealpath(t, identityRealpath)
		return NewPathNormalizer(Win32PathFlavor, `C:\Projects\App`)
	}
	tw(t, bpxFile, "expands $HOME before any platform-specific token handling", func(t *testing.T) {
		// Expansion happens at collection, upstream of the flavor, so the token carries the expanded path on every host.
		eqStrings(t, ruleTokens(bpParse(t, `ls "$HOME/x"`, win(t))), homePath()+"/x")
	})
	tw(t, bpxFile, "keeps a non-mount POSIX absolute literal (Git Bash semantics)", func(t *testing.T) {
		// On win32, Pi core runs Git Bash: /etc is an MSYS install-root path, not C:\etc, so it is matched and shown as typed (#533).
		eqStrings(t, externalValuesOf(t, "cat /etc/hosts", win(t)), "/etc/hosts")
	})
	tw(t, bpxFile, "keeps a non-mount POSIX absolute as a literal rule candidate", func(t *testing.T) {
		eqStrings(t, bpParse(t, "cat /tmp/foo", win(t)).PathRuleCandidates()[0].Path.MatchValues(), "/tmp/foo")
	})
	tw(t, bpxFile, "folds a drive-mount cd so a following traversal resolves under it", func(t *testing.T) {
		// cd /c/Other gives base C:\Other; ../x resolves to C:\x (not C:\c\x). The cd argument itself is also collected.
		eqStrings(t, externalValuesOf(t, "cd /c/Other && cat ../x", win(t)), `c:\other`, `c:\x`)
	})
	tw(t, bpxFile, "degrades a non-mount POSIX absolute cd to a conservative unknown base", func(t *testing.T) {
		// Git Bash's /tmp is install-dependent, so `cd /tmp` makes the base unresolvable; a following traversal is flagged
		// conservatively against cwd for display, and /tmp itself is a literal external path (#533).
		eqStrings(t, externalValuesOf(t, "cd /tmp && cat ../x", win(t)), "/tmp", `c:\projects\x`)
	})
	tw(t, bpxFile, "flags a ..-traversal escaping cwd under win32 rules", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "cat ../sibling/x", win(t)), `c:\projects\sibling\x`)
	})
	tw(t, bpxFile, "folds a current-shell cd so an in-cwd ..-traversal is not flagged", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "cd sub && cat ../x", win(t)))
	})
	tw(t, bpxFile, "recognizes a backslash-relative token as a path rule candidate (#520)", func(t *testing.T) {
		eqv(t, bpParse(t, `cat dir\file`, win(t)).PathRuleCandidates()[0].Token, `dir\file`)
	})
	tw(t, bpxFile, "resolves a backslash-relative token to the same win32 aliases its forward-slash equivalent matches (#520)", func(t *testing.T) {
		n := win(t)
		backslash := bpParse(t, `cat dir\file`, n).PathRuleCandidates()[0].Path.MatchValues()
		// The backslash token resolves to the canonical win32 path plus its win32-normalized relative alias.
		eqStrings(t, backslash, `c:\projects\app\dir\file`, `dir\file`)
		// The forward-slash equivalent carries the same aliases plus a redundant raw "dir/file" that folds to "dir\file".
		forward := bpParse(t, "cat dir/file", n).PathRuleCandidates()[0].Path.MatchValues()
		for _, alias := range backslash {
			if !contains(forward, alias) {
				t.Errorf("forward-slash aliases %q lack %q", forward, alias)
			}
		}
	})
	// posix backslash-relative tokens stay bare (#520)
	tw(t, bpxFile, "does not treat a backslash-relative token as a path rule candidate on posix", func(t *testing.T) {
		mockRealpath(t, identityRealpath)
		n := NewPathNormalizer(PosixPathFlavor, cwd)
		if got := bpParse(t, `cat dir\file`, n).PathRuleCandidates(); len(got) != 0 {
			t.Errorf("got %#v", got)
		}
	})

	// resolved shell expansions (#694)
	tw(t, bpxFile, "flags $HOME/\u2026 whose target does not exist", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, `touch "$HOME/pi-permission-system-repro-new"`), join(homePath(), "pi-permission-system-repro-new"))
	})
	tw(t, bpxFile, "flags a bare ${HOME}", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, `ls "${HOME}"`), homePath())
	})
	tw(t, bpxFile, "flags ${HOME}/\u2026", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, `ls "${HOME}/somewhere"`), join(homePath(), "somewhere"))
	})
	tw(t, bpxFile, "flags a $HOME redirect destination", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "echo hi > $HOME/out.txt"), join(homePath(), "out.txt"))
	})
	tw(t, bpxFile, "yields exactly one entry for an existing $HOME target", func(t *testing.T) {
		// The strict shape gate accepts it; it must not be collected through the existence probe as well.
		eqStrings(t, externalValuesOf(t, `ls "$HOME"`), homePath())
	})
	tw(t, bpxFile, "gives $HOME/\u2026 and its literal spelling the same projection", func(t *testing.T) {
		expanded := externalValuesOf(t, `ls "`+join(homePath(), "docs")+`"`)
		spelled := externalValuesOf(t, `ls "$HOME/docs"`)
		eqStrings(t, spelled, expanded...)
	})
	tw(t, bpxFile, "resolves $HOME/\u2026 independently of an unknown effective base", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, `cd "$DIR" && cat "$HOME/.ssh/id_rsa"`), join(homePath(), ".ssh/id_rsa"))
	})
	tw(t, bpxFile, "resolves $PWD against the cd-folded base", func(t *testing.T) {
		// `/etc` is flagged by the `cd` argument token itself; `$PWD/passwd` contributes the second entry.
		eqStrings(t, externalValuesOf(t, `cd /etc && ls "$PWD/passwd"`), "/etc", "/etc/passwd")
	})
	tw(t, bpxFile, "does not flag a $PWD token that stays inside the working directory", func(t *testing.T) {
		eqExt(t, externalsOf(t, `ls "$PWD/src"`))
	})
	tw(t, bpxFile, "does not resolve an expansion carrying an operator", func(t *testing.T) {
		eqExt(t, externalsOf(t, `ls "${HOME:-/tmp}/x"`))
	})
	tw(t, bpxFile, "does not resolve a variable through an assignment (accepted residual)", func(t *testing.T) {
		// ADR 0009 keeps assignment-then-reference an accepted residual; this pins the declined behavior.
		eqExt(t, externalsOf(t, `CURRENT="$HOME"; ls "$CURRENT"`))
	})

	// effective working directory projection
	tw(t, bpxFile, "folds a sequence of current-shell cd commands", func(t *testing.T) {
		eqExt(t, externalsOf(t, "cd a && cd b && cat ../c"))
	})
	tw(t, bpxFile, "catches an escape masked by a later cd that the single-base model missed", func(t *testing.T) {
		if !contains(externalValuesOf(t, "cd nested/deep && cd .. && cat ../../etc/passwd"), "/projects/etc/passwd") {
			t.Error("/projects/etc/passwd is not external")
		}
	})
	tw(t, bpxFile, "folds a cd that is not the first command", func(t *testing.T) {
		eqExt(t, externalsOf(t, "mkdir d && cd a && cat ../b"))
	})
	tw(t, bpxFile, "folds a cd whose target follows its redirect", func(t *testing.T) {
		eqExt(t, externalsOf(t, "cd 2>/dev/null a && cat ../b"))
	})
	tw(t, bpxFile, "folds a cd whose redirect precedes its target", func(t *testing.T) {
		// The redirect is not cd's operand; `a` is. ../b resolves to cwd/b.
		eqExt(t, externalsOf(t, "2>/dev/null cd a && cat ../b"))
	})
	tw(t, bpxFile, "does not fold a backgrounded cd", func(t *testing.T) {
		// `cd a &` runs in a subshell, so it must not update the running directory.
		if !contains(externalValuesOf(t, "cd a & cat ../b"), "/projects/b") {
			t.Error("/projects/b is not external")
		}
	})
	tw(t, bpxFile, "does not fold a cd inside a pipeline", func(t *testing.T) {
		if !contains(externalValuesOf(t, "cd nested | cat ../b"), "/projects/b") {
			t.Error("/projects/b is not external")
		}
	})
	tw(t, bpxFile, "folds a cd inside a subshell for paths within that subshell", func(t *testing.T) {
		eqExt(t, externalsOf(t, "( cd sub && cat ../x )"))
	})
	tw(t, bpxFile, "does not leak a subshell cd to following commands", func(t *testing.T) {
		if !contains(externalValuesOf(t, "( cd sub ) && cat ../y"), "/projects/y") {
			t.Error("/projects/y is not external")
		}
	})
	tw(t, bpxFile, "persists a cd inside a brace group to later commands in the group", func(t *testing.T) {
		eqExt(t, externalsOf(t, "{ cd sub; cat ../x; }"))
	})
	tw(t, bpxFile, "persists a brace-group cd to following sibling commands", func(t *testing.T) {
		eqExt(t, externalsOf(t, "{ cd sub; } && cat ../x"))
	})
	tw(t, bpxFile, "conservatively flags a relative path inside a command substitution", func(t *testing.T) {
		// Interior cd folding inside substitutions is deferred: the interior inherits the enclosing base (cwd).
		if !contains(externalValuesOf(t, "echo $(cd q && cat ../r)"), "/projects/r") {
			t.Error("/projects/r is not external")
		}
	})
	tw(t, bpxFile, "flags relative paths conservatively after a non-literal cd", func(t *testing.T) {
		if !contains(externalValuesOf(t, `cd "$DIR" && cat ../x`), "/projects/x") {
			t.Error("/projects/x is not external")
		}
	})
	tw(t, bpxFile, "flags even a within-cwd relative path after a non-literal cd", func(t *testing.T) {
		if !contains(externalValuesOf(t, `cd "$DIR" && cat src/../within.txt`), "/projects/my-app/within.txt") {
			t.Error("/projects/my-app/within.txt is not external")
		}
	})
	tw(t, bpxFile, "does not flag a revision range after a non-literal cd", func(t *testing.T) {
		// `..` inside a segment traverses nothing, so only the cd target is external.
		eqStrings(t, externalValuesOf(t, "cd ~/x && git log HEAD..origin/main"), join(homePath(), "x"))
	})
	tw(t, bpxFile, "flags a whole-segment traversal inside a longer token after a non-literal cd", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "cd ~/x && cat a/../../b"), join(homePath(), "x"), "/projects/b")
	})
	tw(t, bpxFile, "still resolves an absolute path normally after a non-literal cd", func(t *testing.T) {
		eqExt(t, externalsOf(t, `cd "$DIR" && cat /projects/my-app/x.txt`))
	})
	tw(t, bpxFile, "treats `cd -` as an unknown effective directory", func(t *testing.T) {
		if !contains(externalValuesOf(t, "cd - && cat ../x"), "/projects/x") {
			t.Error("/projects/x is not external")
		}
	})
	tw(t, bpxFile, "recovers a known base when a later cd is absolute", func(t *testing.T) {
		eqExt(t, externalsOf(t, `cd "$DIR" && cd /projects/my-app/src && cat ../x`))
	})
	tw(t, bpxFile, "folds a leading current-shell cd across a redirect-then-pipe", func(t *testing.T) {
		// tree-sitter-bash groups `cd a && pnpm x 2>&1 | tail` as `(cd a && pnpm x 2>&1) | tail`; bash precedence makes `cd a`
		// current-shell, so the fold must persist past the pipeline (#454).
		eqExt(t, externalsOf(t, "cd a && pnpm x 2>&1 | tail ; cat ../b"))
	})
	tw(t, bpxFile, "folds it too when the redirect carries the command's words", func(t *testing.T) {
		eqExt(t, externalsOf(t, "cd a && pnpm 2>&1 x | tail ; cat ../b"))
	})
	tw(t, bpxFile, "persists the fold past a redirect-then-pipe to a later cd", func(t *testing.T) {
		eqExt(t, externalsOf(t, "cd a/b && pnpm x 2>&1 | tail ; cd .. && cd .."))
	})
	tw(t, bpxFile, "does not fold the terminal piped command of the first stage", func(t *testing.T) {
		// Fail-closed: `cd b` is the real pipe stage (a subshell), so it must NOT fold.
		if !contains(externalValuesOf(t, "cd a && cd b 2>&1 | tail ; cat ../../x"), "/projects/x") {
			t.Error("/projects/x is not external")
		}
	})
	tw(t, bpxFile, "resolves a downstream pipe stage against the folded base", func(t *testing.T) {
		eqExt(t, externalsOf(t, "cd a && pnpm x 2>&1 | cat ../foo"))
	})

	tw(t, bpxFile, "flags an absolute in-cwd path that resolves externally via a symlink, returning the typed form", func(t *testing.T) {
		n := norm(t)
		// The boundary decision uses the canonical form, while the returned value is the typed form (#418).
		mockRealpath(t, func(p string) (string, error) {
			if p == "/projects/my-app/link/hosts" {
				return "/etc/hosts", nil
			}
			return p, nil
		})
		external := externalValues(bpParse(t, "cat /projects/my-app/link/hosts", n))
		if !contains(external, "/projects/my-app/link/hosts") || contains(external, "/etc/hosts") {
			t.Errorf("external %q", external)
		}
	})
	tw(t, bpxFile, "does not flag a token that resolves within a symlinked cwd", func(t *testing.T) {
		// Simulates /tmp -> /private/tmp on macOS; cwd is the canonical form.
		mockRealpath(t, func(p string) (string, error) {
			if p == "/tmp" {
				return "/private/tmp", nil
			}
			if len(p) >= 5 && p[:5] == "/tmp/" {
				return "/private/tmp" + p[4:], nil
			}
			return p, nil
		})
		n := NewPathNormalizer(PathFlavorForPlatform(runtime.GOOS), "/private/tmp")
		if got := bpParse(t, "cat /tmp/workspace/file.ts", n).ExternalAccesses(); len(got) != 0 {
			t.Errorf("got %#v", got)
		}
	})

	// plain operands
	tw(t, bpxFile, "leaves an absolute path within cwd alone", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "cat /projects/my-app/src/index.ts"))
	})
	tw(t, bpxFile, "projects a home-relative path outside cwd", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "cat ~/documents/secret.txt"), join(homePath(), "documents/secret.txt"))
	})
	tw(t, bpxFile, "leaves a home-relative path resolving within cwd alone", func(t *testing.T) {
		mockRealpath(t, identityRealpath)
		underHome := NewPathNormalizer(PathFlavorForPlatform(runtime.GOOS), join(homePath(), "myproject"))
		eqStrings(t, externalValuesOf(t, "cat ~/myproject/file.ts", underHome))
	})
	tw(t, bpxFile, "leaves a .. traversal that stays within cwd alone", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "cat src/../lib/utils.ts"))
	})
	tw(t, bpxFile, "projects the path after a command's flags", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "ls -la /etc/passwd"), "/etc/passwd")
	})

	// statement separators
	each(t, "projects the path after %s", func(t *testing.T) {
		for _, row := range [][2]string{
			{"echo hello | tee /tmp/output.txt", "/tmp/output.txt"}, {"echo done; cat /etc/hosts", "/etc/hosts"},
			{"true && cat /etc/hosts", "/etc/hosts"},
		} {
			eqStrings(t, externalValuesOf(t, row[0]), row[1])
		}
	})

	// quoted strings
	tw(t, bpxFile, "leaves a path inside a double-quoted string alone", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, `git commit -m "fix: update /etc/hosts handler"`))
	})
	tw(t, bpxFile, "leaves a path inside a single-quoted string alone", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "echo 'see /usr/local/docs for info'"))
	})
	tw(t, bpxFile, "still projects an unquoted path alongside quoted content", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, `cat /etc/hosts && echo "done"`), "/etc/hosts")
	})
	tw(t, bpxFile, "leaves a path alone when adjacent quoted segments form one word", func(t *testing.T) {
		// One concatenation whose resolved text is 'path is /etc/hosts' (one token, not a path candidate).
		eqStrings(t, externalValuesOf(t, `echo "path is "/etc/hosts""`))
	})
	tw(t, bpxFile, "leaves a path inside a string with an escaped quote alone", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, `git commit -m "fix: update \"the /etc/hosts\" handler"`))
	})
	tw(t, bpxFile, "leaves a path inside a node -e script's single-quoted string alone", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, `node -e "const p = '/etc/hosts'; console.log(p);"`))
	})
	tw(t, bpxFile, "leaves a path after an escaped quote in a multi-line node -e script alone", func(t *testing.T) {
		// The shape of a command that prompted during dog-fooding: the outer "..." argument holds real newlines and \" escapes.
		command := "node -e \"\n" +
			"import('shell-quote').then(({ parse }) => {\n" +
			"  const cmd = \\\"cat << 'EOF'\\n/etc/hosts\\nsome content\\nEOF\\\";\n" +
			"  console.log(JSON.stringify(parse(cmd)));\n" +
			"});\n" +
			"\""
		eqStrings(t, externalValuesOf(t, command))
	})
	tw(t, bpxFile, "still projects a real operand beside a quoted flag value", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "grep --regexp='/etc/passwd' /etc/hosts"), "/etc/hosts")
	})

	// safe device paths
	each(t, "leaves %s alone", func(t *testing.T) {
		for _, command := range []string{
			"command 2>/dev/null", "echo hello > /dev/null", "cat /dev/stdin", "cat /dev/stdout", "cat /dev/stderr",
		} {
			eqStrings(t, externalValuesOf(t, command))
		}
	})
	tw(t, bpxFile, "still projects a real external path alongside /dev/null", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "cat /etc/hosts 2>/dev/null"), "/etc/hosts")
	})
	tw(t, bpxFile, "projects a path under a device, which is no device", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "cat /dev/null/subdir"), "/dev/null/subdir")
	})

	// the filesystem root
	tw(t, bpxFile, "projects the root find / scans", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "find /"), "/")
	})
	tw(t, bpxFile, "projects the root find / scans behind search predicates", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, `find / -path "*/pi-coding-agent/*.d.ts"`), "/")
	})
	each(t, "normalizes a bare %s to the root", func(t *testing.T) {
		for _, command := range []string{"echo //", "echo ///"} {
			eqStrings(t, externalValuesOf(t, command), "/")
		}
	})
	tw(t, bpxFile, "projects the root once among other arguments", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "echo // hello"), "/")
	})
	tw(t, bpxFile, "projects the root alongside another external path", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "cat /etc/hosts; echo //"), "/etc/hosts", "/")
	})

	// shell comments
	tw(t, bpxFile, "leaves a path appearing only in a comment alone", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "echo hello # /etc/shadow"))
	})
	tw(t, bpxFile, "projects the path before a comment but not the one inside it", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "cat /etc/hosts # see also /etc/shadow"), "/etc/hosts")
	})

	// heredocs
	each(t, "leaves a path in the body of %s alone", func(t *testing.T) {
		for _, command := range []string{"cat << 'EOF'\n/etc/hosts\nEOF", "cat << \"EOF\"\n/etc/hosts\nEOF", "cat <<- 'EOF'\n\t/etc/hosts\nEOF"} {
			eqStrings(t, externalValuesOf(t, command))
		}
	})
	tw(t, bpxFile, "projects the command's operand but not the heredoc body", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "cat /etc/hosts << 'EOF'\nsome content\nEOF"), "/etc/hosts")
	})

	// command substitution and subshells
	each(t, "projects the path inside %s", func(t *testing.T) {
		for _, command := range []string{"echo $(cat /etc/hosts)", "echo $(echo $(cat /etc/hosts))", "(cat /etc/hosts)"} {
			eqStrings(t, externalValuesOf(t, command), "/etc/hosts")
		}
	})

	// redirect operators
	each(t, "projects the target of %s", func(t *testing.T) {
		for _, row := range [][2]string{
			{"echo hello >> /tmp/out.txt", "/tmp/out.txt"}, {"sort < /etc/hosts", "/etc/hosts"},
			{"command 2>/tmp/errors.log", "/tmp/errors.log"},
		} {
			eqStrings(t, externalValuesOf(t, row[0]), row[1])
		}
	})

	// pattern-first commands
	tw(t, bpxFile, "leaves a sed address pattern alone, whatever it holds", func(t *testing.T) {
		command := "sed -i '' '/source: \"tool\",/{/origin:/!s/source: \"tool\",/source: \"tool\",\n      origin: \"builtin\",/;}' tests/tool-input-preview.test.ts"
		eqStrings(t, externalValuesOf(t, command))
	})
	tw(t, bpxFile, "skips a sed address pattern starting with / and projects the file", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "sed '/pattern/d' /etc/hosts"), "/etc/hosts")
	})
	tw(t, bpxFile, "leaves a sed whose only file is within cwd alone", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "sed 's/foo/bar/' src/index.ts"))
	})
	tw(t, bpxFile, "reads sed -n as a flag that consumes nothing", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "sed -n '/pattern/p' /etc/hosts"), "/etc/hosts")
	})
	tw(t, bpxFile, "skips an awk program and projects the file", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "awk '{print}' /etc/hosts"), "/etc/hosts")
	})
	tw(t, bpxFile, "consumes an unquoted awk -F separator and skips the program", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "awk -F: '{print $1}' /etc/passwd"), "/etc/passwd")
	})
	tw(t, bpxFile, "reads rg -e as consuming the pattern", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "rg -e '/usr/local' /etc/profile.d/"), "/etc/profile.d")
	})
	tw(t, bpxFile, "leaves an sd whose only file is within cwd alone", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "sd 'foo' 'bar' src/index.ts"))
	})
	tw(t, bpxFile, "reads a path-qualified sed as sed", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "/usr/bin/sed 's/foo/bar/' /etc/hosts"), "/etc/hosts")
	})
	tw(t, bpxFile, "still projects a pattern-first command's redirect target", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "sed 's/foo/bar/' input.txt > /tmp/output.txt"), "/tmp/output.txt")
	})
	tw(t, bpxFile, "projects a later pipeline stage's operand", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "sed 's/foo/bar/' src/file.ts | cat /etc/hosts"), "/etc/hosts")
	})
	tw(t, bpxFile, "projects the operand of a substitution passed to a pattern-first command", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "grep 'pattern' $(cat /etc/file-list)"), "/etc/file-list")
	})

	// regex arguments of pattern-first commands
	each(t, "leaves %s alone", func(t *testing.T) {
		for _, command := range []string{
			`grep -n "glob" src/foo.ts 2>/dev/null | grep -v "//.*glob\|globalConfig" | head -30`,
			`grep -v "//.*foo" file.txt`,
			`grep "foo\|bar\|baz" src/file.ts`,
			`grep -E "^/usr/bin" file.txt`,
			`sed "s/foo.*/bar/g" file.txt`,
			`awk "/\/etc\/.*/" file.txt`,
			`rg "/etc/.*passwd" -l`,
		} {
			eqStrings(t, externalValuesOf(t, command))
		}
	})
	tw(t, bpxFile, "still projects a real external path beside a regex argument", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, `grep -v "//.*pattern" /etc/hosts`), "/etc/hosts")
	})

	// a leading cd
	tw(t, bpxFile, "leaves a traversal back into cwd from a cd'd subdirectory alone", func(t *testing.T) {
		// A real command that prompted as a false positive: the traversal resolves inside cwd from the cd target.
		eqStrings(t, externalValuesOf(t, `cd /projects/my-app/packages/sub && grep -n "pattern" .pi/../../../.pi/skills/pkg/SKILL.md`))
	})
	tw(t, bpxFile, "leaves the same traversal alone after a relative cd", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, `cd packages/sub && grep -n "x" .pi/../../../.pi/skills/pkg/SKILL.md`))
	})
	tw(t, bpxFile, "still projects an absolute external path after a cd into a subdirectory", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "cd /projects/my-app/packages/sub && cat /etc/hosts"), "/etc/hosts")
	})
	tw(t, bpxFile, "resolves a traversal against an external cd target", func(t *testing.T) {
		// `cd /tmp` makes /tmp the base, so the cd target itself is projected and ../etc/hosts resolves to /etc/hosts.
		eqStrings(t, externalValuesOf(t, "cd /tmp && cat ../etc/hosts"), "/tmp", "/etc/hosts")
	})
	tw(t, bpxFile, "resolves an escape against a cd that is not the first command", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "echo hello && cd /projects/my-app/src && cat ../../outside.txt"), "/projects/outside.txt")
	})
	tw(t, bpxFile, "folds a cd joined by a semicolon", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "cd /projects/my-app/src ; cat ../README.md"))
	})

	// Git Bash tokens on a win32 host
	gitBash := func(t *testing.T) *PathNormalizer {
		mockRealpath(t, identityRealpath)
		return NewPathNormalizer(Win32PathFlavor, "C:/projects/app")
	}
	each(t, "projects %s outside cwd", func(t *testing.T) {
		for _, row := range [][2]string{
			{"cat C:/Windows/win.ini", `c:\windows\win.ini`}, {"cat D:/secrets/password.txt", `d:\secrets\password.txt`},
		} {
			eqStrings(t, externalValuesOf(t, row[0], gitBash(t)), row[1])
		}
	})
	tw(t, bpxFile, "leaves a drive path inside cwd alone", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "cat C:/projects/app/inside.txt", gitBash(t)))
	})
	tw(t, bpxFile, "leaves a drive path inside cwd alone after a non-literal cd", func(t *testing.T) {
		// C:/ is absolute on win32, so it takes the resolved branch with its inside-cwd check.
		eqStrings(t, externalValuesOf(t, `cd "$D" && cat C:/projects/app/inside.txt`, gitBash(t)))
	})
	tw(t, bpxFile, "leaves all four safe device paths alone", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "cat /dev/stdin /dev/stdout /dev/stderr /dev/null", gitBash(t)))
	})
	tw(t, bpxFile, "leaves an in-cwd drive mount alone", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "cat /c/projects/app/inside.txt", gitBash(t)))
	})
	each(t, "projects %s as its translated Windows path", func(t *testing.T) {
		for _, row := range [][2]string{
			{"cat /c/Other/secret.txt", `c:\other\secret.txt`}, {"cat /d/secrets/pw.txt", `d:\secrets\pw.txt`},
		} {
			eqStrings(t, externalValuesOf(t, row[0], gitBash(t)), row[1])
		}
	})
	tw(t, bpxFile, "keeps distinct literal-only POSIX absolutes apart", func(t *testing.T) {
		eqStrings(t, externalValuesOf(t, "cat /tmp/a /tmp/b", gitBash(t)), "/tmp/a", "/tmp/b")
	})
}
