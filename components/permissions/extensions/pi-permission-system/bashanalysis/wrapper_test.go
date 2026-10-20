package bashanalysis

import (
	"strconv"
	"testing"
)

const wa = "access-intent/bash/wrapper-analysis"

func TestClassifyWrapperWords(t *testing.T) {
	for _, u := range []string{"eval rm", "bash -c rm", "sh -c rm", "dash -c rm", "zsh -c rm", "ksh -c rm", "bash -ec rm", "bash -xc rm", "/bin/bash -c rm"} {
		t.Run("flags "+u, func(t *testing.T) { eq(t, ClassifyWrapperWords(words(u)), WrapperOpaquePayload) })
	}
	tw(t, wa, "does not flag a shell running a script file", func(t *testing.T) {
		eq(t, ClassifyWrapperWords(words("bash script.sh")), WrapperKind(""))
	})
	tw(t, wa, "does not flag a -c cluster after the end-of-options marker", func(t *testing.T) {
		eq(t, ClassifyWrapperWords(words("bash -- -c")), WrapperKind(""))
	})
	for _, u := range []string{"sudo aws s3 ls", "env FOO=bar aws", "xargs grep foo", "timeout 10 grep foo", "nice -n 5 make", "doas ls", "flock /tmp/lock ls"} {
		t.Run("flags "+u, func(t *testing.T) { eq(t, ClassifyWrapperWords(words(u)), WrapperIndirection) })
	}
	for _, u := range []string{"find . -exec grep foo {} ;", "find . -execdir rm {} ;", "fd -x rm", "fd --exec-batch rm"} {
		t.Run("flags the exec-conditional "+u, func(t *testing.T) { eq(t, ClassifyWrapperWords(words(u)), WrapperIndirection) })
	}
	tw(t, wa, "does not flag a bare search", func(t *testing.T) {
		eq(t, ClassifyWrapperWords(words("find . -name x")), WrapperKind(""))
	})
	for _, u := range []string{"ls -la", "grep -c foo file", "git status"} {
		t.Run("does not flag "+u, func(t *testing.T) { eq(t, ClassifyWrapperWords(words(u)), WrapperKind("")) })
	}
	tw(t, wa, "does not flag an empty word list", func(t *testing.T) {
		eq(t, ClassifyWrapperWords(nil), WrapperKind(""))
	})
}

func TestInlineShellPayloadIndex(t *testing.T) {
	idx := func(u string) int { return InlineShellPayloadIndex(words(u)) }
	for _, c := range []struct {
		unit string
		want int
	}{
		{`bash -c "rm -rf /"`, 2}, {"sh -c 'ls'", 2}, {"dash -c ls", 2}, {"zsh -c ls", 2}, {"ksh -c ls", 2},
		{`bash -ec "make build"`, 2}, {`bash -xc "make build"`, 2}, {`/bin/bash -c "ls"`, 2}, {`bash -i -c "ls"`, 3},
	} {
		t.Run("names the argument after the -c cluster in "+c.unit, func(t *testing.T) { eq(t, idx(c.unit), c.want) })
	}
	tw(t, wa, "names eval's first argument, which takes no flag", func(t *testing.T) { eq(t, idx(`eval "rm x"`), 1) })
	for _, c := range []struct {
		unit string
		want int
	}{
		{`sudo bash -c 'x'`, 3}, {`xargs sh -c 'x'`, 3}, {`timeout 5 bash -c 'x'`, 4}, {`env FOO=bar bash -c 'x'`, 4},
		{`sudo -u root bash -c 'x'`, 5}, {`sudo timeout 5 bash -c 'x'`, 5}, {`find . -exec sh -c 'x' ;`, 5},
	} {
		t.Run("names the payload of "+c.unit+" through the wrapper", func(t *testing.T) { eq(t, idx(c.unit), c.want) })
	}
	for _, u := range []string{
		`sudo ls`, `xargs grep foo`, `sudo python3 -c 'x'`, `xargs --unknown-opt`, `find . -exec`,
		"bash script.sh", "bash --help", "bash -- -c", "python3 -c 'print(1)'", "node -e 'x'", "sudo ls", "ls -la",
	} {
		t.Run("answers -1 for "+u, func(t *testing.T) { eq(t, idx(u), -1) })
	}
	tw(t, wa, "answers -1 for an empty word list", func(t *testing.T) { eq(t, InlineShellPayloadIndex(nil), -1) })
	for _, c := range []struct {
		unit string
		want int
	}{{"bash -c", 2}, {"eval", 1}} {
		t.Run("names the vacant position in "+c.unit, func(t *testing.T) { eq(t, idx(c.unit), c.want) })
	}
}

func TestExecutedUnitOf(t *testing.T) {
	exec := func(u string) (string, bool) { return ExecutedUnitOf(u, words(u)) }
	table := func(name string, rows [][2]string) {
		for _, r := range rows {
			t.Run(name+" "+r[0], func(t *testing.T) {
				got, ok := exec(r[0])
				if !ok || got != r[1] {
					t.Fatalf("got %q,%v want %q", got, ok, r[1])
				}
			})
		}
	}
	none := func(u string) {
		t.Helper()
		if got, ok := exec(u); ok {
			t.Fatalf("%q: got %q, want none", u, got)
		}
	}
	table("names the inner program of", [][2]string{
		{`bash -c "rm -rf /"`, "rm -rf /"}, {"bash -c 'rm -rf /'", "rm -rf /"}, {`sh -ec "make build"`, "make build"},
		{`/bin/bash -c "ls"`, "ls"}, {`eval "rm x"`, "rm x"},
	})
	tw(t, wa, "returns null when the payload argument is missing", func(t *testing.T) { none("bash -c") })
	table("names the inner command of", [][2]string{
		{"sudo aws s3 rm", "aws s3 rm"}, {"sudo -u root aws s3 rm", "aws s3 rm"}, {"sudo -- ls -la", "ls -la"},
		{"xargs grep foo", "grep foo"}, {"xargs -0 -n1 grep foo", "grep foo"}, {"xargs -I{} rm {}", "rm {}"},
		{"timeout 10 grep foo", "grep foo"}, {"timeout -s KILL 10 grep foo", "grep foo"},
		{"nice -n 5 make build", "make build"}, {"env FOO=bar grep foo", "grep foo"},
		{"flock /tmp/lock aws s3 ls", "aws s3 ls"}, {"watch -n 2 ls", "ls"},
	})
	tw(t, wa, "preserves the inner command's original spacing and quoting", func(t *testing.T) {
		got, _ := exec("sudo   grep  'a  b'  x")
		eq(t, got, "grep  'a  b'  x")
	})
	for _, u := range []string{"xargs", "sudo", "sudo -u root", "timeout 10"} {
		t.Run("returns null when "+u+" names no inner command", func(t *testing.T) { none(u) })
	}
	tw(t, wa, "returns null rather than guessing past an unknown trailing option", func(t *testing.T) { none("xargs --unknown-opt") })
	table("names the per-result command of", [][2]string{
		{"find . -name x -exec grep foo {} ;", "grep foo {}"}, {"find . -exec rm {} +", "rm {}"},
		{"find . -execdir grep foo {} ;", "grep foo {}"}, {"fd -x rm", "rm"}, {"fd --exec-batch rm -f", "rm -f"},
	})
	tw(t, wa, "returns null when the exec flag ends the command", func(t *testing.T) { none("find . -exec") })
	table("unwraps to its innermost command", [][2]string{
		{"sudo timeout 5 xargs grep foo", "grep foo"}, {"sudo bash -c 'rm x'", "rm x"},
		{"timeout 10 sudo -u root aws s3 rm", "aws s3 rm"},
	})
	tw(t, wa, "returns null for an ordinary command", func(t *testing.T) { none("grep foo") })
	tw(t, wa, "returns null for an empty word list", func(t *testing.T) {
		if got, ok := ExecutedUnitOf("", nil); ok {
			t.Fatalf("got %q", got)
		}
	})
}

func TestIsTransparentWrapper(t *testing.T) {
	transparent := func(u string) bool { return IsTransparentWrapper(words(u), false) }
	for _, u := range []string{
		"xargs grep foo", "xargs -0 rg pattern", "xargs -I{} basename {}", "xargs cat", "time cat x", "env FOO=bar grep x",
		"sudo grep foo /etc/hosts", "find . -name '*.ts' -exec wc -l {} +", "fd -e ts -x cat",
	} {
		t.Run("is transparent: "+u, func(t *testing.T) { eq(t, transparent(u), true) })
	}
	tw(t, wa, "unwraps nested indirection to the innermost command", func(t *testing.T) {
		eq(t, transparent("sudo timeout 5 xargs grep foo"), true)
	})
	for _, u := range []string{
		"xargs pnpm test", "time pnpm test", "xargs git commit", "xargs ./grep foo", "xargs /usr/bin/grep foo",
		"xargs sort -o /tmp/x", "xargs find . -delete", "xargs fd -x rm",
		"xargs -I{} sh -c 'grep -l x {}'", "find . -exec sh -c 'grep x' \\;", "sudo bash -c 'cat /etc/shadow'",
		"eval 'grep foo'", "bash -c 'grep foo'",
		"xargs --unknown-opt", "find . -exec", "sudo timeout 5 xargs --unknown-opt",
		"grep foo", "find . -name '*.ts'", "cat a",
	} {
		t.Run("is not transparent: "+u, func(t *testing.T) { eq(t, transparent(u), false) })
	}
	tw(t, wa, "still names the payload for display", func(t *testing.T) {
		unit := "xargs -I{} sh -c 'grep -l x {}'"
		got, ok := ExecutedUnitOf(unit, words(unit))
		eq(t, []any{got, ok}, []any{"grep -l x {}", true})
		eq(t, transparent(unit), false)
	})
	tw(t, wa, "is not transparent for an empty word list", func(t *testing.T) {
		eq(t, IsTransparentWrapper(nil, false), false)
	})
	tw(t, wa, "withholds the exemption from an otherwise transparent wrapper", func(t *testing.T) {
		unit := "xargs grep foo"
		eq(t, transparent(unit), true)
		eq(t, IsTransparentWrapper(words(unit), true), false)
	})
}

var _ = strconv.Itoa
