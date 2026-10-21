package warden

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

// The rewritten scanShell and heredocText against the code they replace (shell_writes_ref_test.go), over generated
// commands built from heredocs of every spelling with bodies and near-miss delimiters, redirections with descriptor
// words, here-strings, groups, substitutions, every quoting and escape, comments, tildes, invalid UTF-8 and Unicode
// spaces.

var shellGenWords = []string{
	"cat", "tee", "echo", "printf", "sed -i", "dd", "ls", "git commit -m", "env", "FOO=1", "command", "exec", "sudo", "-a", "-n", "-e", "-E", "-neE", "src/a.ts", "./out.txt", "~/notes.md", "~", "~user/x", "a~b", "/dev/null", "file name", "x.log", "1", "2", "10", "02", "a1", "1a",
	"$HOME", "${HOME}", "$(date)", "$(echo (nested) \"q\")", "$((1+2))", "`date`", "`unterminated", "$'tab\\t'", "$'", "$'a'b", "$", "$ ", "$1", "$@", "$-", "$#", "$?", "$!", "$*", "$_", "$$",
	"\"plain\"", "\"with $VAR\"", "\"with `bt`\"", "\"esc \\\" quote\"", "\"esc \\\\ back\"", "\"esc \\$ dollar\"", "\"esc \\` tick\"", "\"line\\\ncont\"", "\"nl\nin\"", "\"$(echo \\\")\"", "\"$(echo \"x\")\"", "\"unterminated", "\"\"", "\"a\"b\"c\"", "\"\\\"",
	"'single'", "'a b'", "'$x'", "''", "'a''b'", "'unterminated", "ab'cd'ef", "a\\ b", "\\n", "\\", "\\é", "a\\\nb", "\\\xff", "\\'", "\\\"",
	"é", "日本", "\xff", "\xc3", "\xe6\x97", "\ufffd", "\U0001F600", "\u00a0", "\u2003", "\ufeff",
}

var shellGenSyntax = []string{
	" ", " ", " ", "  ", "\t", "\n", "\n", "\r\n", "\v", ";", ";;", "&", "&&", "||", "|", "|&", "(", ")", "{", "}", "{ ", " }", "( ", " )", "{ cat; }", "(cat)",
	">", ">>", ">|", ">&", ">&2", ">&-", "2>&1", "1>&2", "&>", "&>>", "2>", "10>", "02>>", "a>", "$X>", "'2'>", "<", "<<<", "<(", ">(", "<(cat x)", ">(tee y)", "<<", "<<<'x'", "<<< $X",
	"<<EOF", "<<-EOF", "<<'EOF'", "<<\"EOF\"", "<<\\EOF", "<< EOF", "<<- EOF", "<<E\"O\"F", "<<'E O'", "<<EOF;", "<<EOF|", "<<EOF>", "<<\"EOF", "<<''", "<<", "<<-", "<<EOF2", "<<é", "<<'a\nb'", "<<\"a\\\"b\"", "<<\"a\\$b\"", "<<\"a\\xb\"",
	"EOF", "EOF\n", "\nEOF\n", "\tEOF", "\t\tEOF", " EOF", "EOF ", "eof", "\nEOF", "\n\tEOF", "\n\t\tEOF\n",
	"line one\n", "line \\$x\n", "line `bt`\n", "line $x\n", "line $(cmd)\n", "line \\\\\n", "line \\\n", "line \\`x\n", "\\$HOME\n", "\\\n", "\\n\n", "$ x\n", "$\n", "${x}\n", "\ttabbed line\n", "\t\n", "\n\n", "é\n", "\xff\n", "no newline end",
	"#", "# comment\n", "x#y", " #c ", "\"#\"", "'#'",
}

func genShellCommand(r *rand.Rand) string {
	var b strings.Builder
	n := 1 + r.IntN(12)
	for range n {
		switch r.IntN(7) {
		case 0, 1, 2:
			b.WriteString(shellGenWords[r.IntN(len(shellGenWords))])
		case 3:
			b.WriteString("cat " + shellGenSyntax[r.IntN(30)+30] + " out.txt")
		default:
			b.WriteString(shellGenSyntax[r.IntN(len(shellGenSyntax))])
		}
		if r.IntN(3) > 0 {
			b.WriteByte(' ')
		}
	}
	return b.String()
}

func genShellCharCommand(r *rand.Rand) string {
	pieces := []string{"'", "\"", "\\", "$", "`", "<", ">", "&", "|", ";", "(", ")", "{", "}", "#", "\n", " ", "\t", "\n", "'", "\"", "a", "1", "2", "~", "-", "=", "E", "O", "F", "EOF", "<<", "<<-", "$(", "$'", ">&", "&>", "\xff", "é", "\\\n", "cat", "tee"}
	var b strings.Builder
	for range 1 + r.IntN(26) {
		b.WriteString(pieces[r.IntN(len(pieces))])
	}
	return b.String()
}

func mutateShellCommand(r *rand.Rand, s string) string {
	for range 1 + r.IntN(3) {
		if len(s) == 0 {
			return s
		}
		i := r.IntN(len(s))
		switch r.IntN(4) {
		case 0:
			s = s[:i] + s[i+1:]
		case 1:
			s = s[:i] + string(s[i]) + s[i:]
		case 2:
			s = s[:i] + shellGenSyntax[r.IntN(len(shellGenSyntax))] + s[i:]
		default:
			j := r.IntN(len(s))
			if i > j {
				i, j = j, i
			}
			s = s[:i] + s[j:]
		}
	}
	return s
}

func shellGenCases(seed uint64, n int, each func(i int, command string)) {
	r := rand.New(rand.NewPCG(seed, seed+1))
	for i := range n {
		var command string
		switch i % 4 {
		case 0, 1:
			command = genShellCommand(r)
		case 2:
			command = genShellCharCommand(r)
		default:
			command = mutateShellCommand(r, genShellCommand(r))
		}
		each(i, command)
	}
}

func dumpScan(segments []*scanSegment) string {
	var b strings.Builder
	for _, s := range segments {
		fmt.Fprintf(&b, "{")
		for _, w := range s.words {
			fmt.Fprintf(&b, "%+v ", *w)
		}
		for _, rd := range s.redirects {
			t := scanWord{}
			if rd.target != nil {
				t = *rd.target
			}
			fmt.Fprintf(&b, "redirect{%s %v %+v} ", rd.fd, rd.appending, t)
		}
		fmt.Fprintf(&b, "input:%+v stdin:%q sub:%v group:%+v} ", s.input, s.stdin, s.substitution, s.group)
	}
	return b.String()
}

func TestScanShellAgreesWithTheReference(t *testing.T) {
	heredocs, redirects, multi, quoted, escaped := 0, 0, 0, 0, 0
	shellGenCases(11, 500000, func(i int, command string) {
		got, want := scanShell(command), scanShellRef(command)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("case %d %q:\n got  %s\n want %s", i, command, dumpScan(got), dumpScan(want))
		}
		if len(want) > 1 {
			multi++
		}
		for _, s := range want {
			if s.input != nil && s.input.kind == "heredoc" {
				heredocs++
				break
			}
		}
		for _, s := range want {
			if len(s.redirects) > 0 {
				redirects++
				break
			}
		}
		if strings.ContainsAny(command, `'"`) {
			quoted++
		}
		if strings.Contains(command, `\"`) || strings.Contains(command, `\\`) {
			escaped++
		}
	})
	if heredocs < 20000 || redirects < 50000 || multi < 100000 || quoted < 150000 || escaped < 20000 {
		t.Fatalf("generator too narrow: %d heredocs, %d redirects, %d multi-segment, %d quoted, %d escaped", heredocs, redirects, multi, quoted, escaped)
	}
}

func TestHeredocTextAgreesWithTheReference(t *testing.T) {
	r := rand.New(rand.NewPCG(12, 12))
	pieces := []string{"a", " ", "\n", "\\", "$", "`", "$x", "${", "$(", "\\$", "\\`", "\\\\", "\\\n", "\\n", "$ ", "$1", "$-", "$$", "é", "\xff", "line\n", "text with spaces\n", "$\n"}
	for i := range 200000 {
		body := genText(r, pieces, 10)
		literal := r.IntN(4) == 0
		gt, ge := heredocText(body, literal)
		wt, we := heredocTextRef(body, literal)
		if gt != wt || ge != we {
			t.Fatalf("case %d %q literal=%v: got (%q,%v) want (%q,%v)", i, body, literal, gt, ge, wt, we)
		}
	}
}

// ShellWrites reads what scanShell finds: the heredoc forms the guards judge keep their content byte for byte.
func TestShellWritesOfHeredocsKeepTheirContent(t *testing.T) {
	for _, tc := range []struct{ command, path, content string }{
		{"cat > a.txt <<EOF\nhello\nworld\nEOF\n", "a.txt", "hello\nworld\n"},
		{"cat > a.txt <<EOF\nhello\nworld", "a.txt", "hello\nworld\n"},
		{"cat > a.txt <<'EOF'\n$x `y` \\z\nEOF", "a.txt", "$x `y` \\z\n"},
		{"cat > a.txt <<-EOF\n\tone\n\t\ttwo\n\tEOF", "a.txt", "one\ntwo\n"},
		{"cat > a.txt <<EOF\n\\$x \\\\ \\`y\nEOF", "a.txt", "$x \\ `y\n"},
		{"cat > a.txt <<EOF\nEOF", "a.txt", ""},
	} {
		scan := shellWrites(tc.command, ShellWritesOptions{})
		if len(scan.Writes) != 1 || scan.Writes[0].Path != tc.path || scan.Writes[0].Content != tc.content {
			t.Errorf("%q: %+v", tc.command, scan)
		}
	}
}
