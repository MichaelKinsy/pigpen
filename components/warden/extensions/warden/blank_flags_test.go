package warden

import (
	"math/rand/v2"
	"strings"
	"testing"
)

// blankMessageFlags (with its `git`/`gh` prefilter) against the whole scan, over commands built from message flags in
// every spelling, quotes of each kind, escapes, separators, wrappers, and text with and without `git` and `gh`.
func TestBlankMessageFlagsAgreesWithTheScan(t *testing.T) {
	vocab := []string{"git", "gh", "git commit", "git commit -m", "gh pr create --body", "--body=", "-m", "-m=", "--message=", "--title", "-F", "commit", "pr", "create", "issue", "comment",
		"'text'", `"text"`, `"a $(b)"`, "'a `b`'", `"a\"b"`, `'a\'`, `$'a\'b'`, "'unclosed", `"unclosed`, "''", `""`, " ", " ", " ", "\t", "\n", ";", "&&", "||", "|", "&", `\`, `\ `, `\"`, "=", "-c", "alias.x=!sh", "--config", "sudo", "env", "FOO=1", "/usr/bin/git", "./gh", "psql", "-h", "echo", "ls", "ghost", "digit", "light", "é", "\xff",
		"git commit -m 'msg'", `git commit -m "msg"`, "gh pr create --body 'b; c'", `gh pr create --body="b"`, "git commit --message='m'", "git commit -m 'a && b'", `gh pr create --title "t" --body "$(x)"`, "git -c a=b commit -m 'x'", "sudo git commit -m 'x'", "FOO=1 git commit -m 'x'", "gh issue comment --body 'x'", "git commit -am 'x'", "git commit -m", "gh pr create --body",
		"x/git", `"git"`, "g'i't", `g\it`, "gi", "gh2", "GIT", "Gh"}
	r := rand.New(rand.NewPCG(61, 61))
	blanked, skipped := 0, 0
	for i := range 400000 {
		command := genText(r, vocab, 12)
		if r.IntN(2) == 0 {
			command = strings.ReplaceAll(command, "  ", " ")
		}
		got, want := blankMessageFlags(command), blankMessageFlagsScan(command)
		if got != want {
			t.Fatalf("case %d %q:\n got  %q\n want %q", i, command, got, want)
		}
		if want != command {
			blanked++
		}
		if !strings.Contains(command, "git") && !strings.Contains(command, "gh") {
			skipped++
		}
	}
	if blanked < 20000 || skipped < 20000 {
		t.Fatalf("generator too narrow: %d blanked, %d without git or gh", blanked, skipped)
	}
}
