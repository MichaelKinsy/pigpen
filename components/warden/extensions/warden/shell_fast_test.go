package warden

import (
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

// The hand-written shell primitives against the expressions and the code they replaced, over generated commands: every
// separator, doubled separators, quotes, heredocs, backslashes, every kind of whitespace and invalid UTF-8.
var (
	refAssignment  = lazyRE(`^[A-Za-z_][A-Za-z0-9_]*=`)
	refLeadingPath = lazyRE(`^.*/`)
)

func refSplitShell(command string) []string {
	var out []string
	for _, part := range reSplitShell.Split(command, -1) {
		if p := jsTrim(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func refHeadOf(segment string) (string, bool) {
	tokens := jsSplitWhitespace(jsTrim(segment))
	i := 0
	for i < len(tokens) && (refAssignment.MatchString(tokens[i]) || wrappers[tokens[i]]) {
		i++
	}
	if i >= len(tokens) || tokens[i] == "" {
		return "", false
	}
	return refLeadingPath.ReplaceAllString(tokens[i], ""), true
}

var shellVocab = []string{
	"git", "push", "-f", "sudo", "env", "FOO=bar", "A=", "_x=1", "1a=2", "a-b=3", "echo", "cat", "bash", "-c", "sh", "rm", "-rf", "/usr/bin/git", "./run", "a/b/", "/", "dir/",
	" ", " ", "  ", "\t", "\n", "\n\n", "\v", "\f", "\r", "\u00a0", "\u2003", "\ufeff", "é", "\xff", ";", "&&", "||", "|", "&", "&&&", "|||", "&|", "(", ")", "$(", "`",
	"'", "\"", "'x y'", "\"a; b\"", "<<EOF", "<<'EOF'", "<<-EOF", "EOF", "body", "\\", "\\'", "$'", "-m", "--message", "--body", "commit", "gh pr create", "=", "#",
}

func TestShellPrimitivesAgreeWithTheExpressionsTheyReplaced(t *testing.T) {
	r := rand.New(rand.NewPCG(21, 21))
	for i := range 80000 {
		text := genText(r, shellVocab, 12)
		if got, want := splitShell(text), refSplitShell(text); !reflect.DeepEqual(got, want) {
			t.Fatalf("case %d splitShell(%q): got %q want %q", i, text, got, want)
		}
		if got, want := isAssignment(text), refAssignment.MatchString(text); got != want {
			t.Fatalf("case %d isAssignment(%q): got %v want %v", i, text, got, want)
		}
		if got, want := stripPathPrefix(text), refLeadingPath.ReplaceAllString(text, ""); got != want {
			t.Fatalf("case %d stripPathPrefix(%q): got %q want %q", i, text, got, want)
		}
		gh, gok := headOf(text)
		wh, wok := refHeadOf(text)
		if gh != wh || gok != wok {
			t.Fatalf("case %d headOf(%q): got %q %v want %q %v", i, text, gh, gok, wh, wok)
		}
	}
}

// refStripDataText is StripDataText without its fast exits: both halves run on every command.
func refStripDataText(command string) ScannedCommand {
	scanned, stripped := stripHeredocs(command)
	return scanSegments(command, scanned, blankMessageFlags(scanned), stripped, true)
}

func TestStripDataTextFastExitsAgreeWithTheFullScan(t *testing.T) {
	r := rand.New(rand.NewPCG(22, 22))
	for i := range 60000 {
		text := genText(r, shellVocab, 14)
		if got, want := StripDataText(text), refStripDataText(text); got != want {
			t.Fatalf("case %d StripDataText(%q): got %+v want %+v", i, text, got, want)
		}
	}
	for _, text := range []string{"", "ls", "go test ./... && git status", "echo 'a; b'", "git commit -m \"x\"", "cat <<EOF\nbody\nEOF", "bash <<'EOF'\nrm -rf x\nEOF", "a\\", "'unclosed", "gh pr create --body 'x'"} {
		if got, want := StripDataText(text), refStripDataText(text); got != want || strings.Contains(text, "\x00") {
			t.Errorf("StripDataText(%q): got %+v want %+v", text, got, want)
		}
	}
}
