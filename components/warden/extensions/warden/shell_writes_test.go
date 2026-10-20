package warden

// Twins of tests/shell-writes.test.ts (literal file writes in a shell command). The case about the bash action request
// (`describeAction` and `buildRequest`, guard.ts) belongs to the guard lane and stays a named skip.

import (
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var homeMe = ShellWritesOptions{Home: "/home/me", HasHome: true}

func shellOnly(t *testing.T, command string) ShellWrite {
	t.Helper()
	scan := shellWrites(command, homeMe)
	if len(scan.Skips) != 0 {
		t.Fatalf("%q: skips %+v", command, scan.Skips)
	}
	if len(scan.Writes) != 1 {
		t.Fatalf("%q: writes %+v", command, scan.Writes)
	}
	return scan.Writes[0]
}

func shellSkipped(t *testing.T, command, reason string) ShellSkip {
	t.Helper()
	scan := shellWrites(command, homeMe)
	if len(scan.Writes) != 0 {
		t.Fatalf("no synthetic write for %q: %+v", command, scan.Writes)
	}
	if len(scan.Skips) != 1 {
		t.Fatalf("%q: skips %+v", command, scan.Skips)
	}
	mustMatch(t, scan.Skips[0].Reason, reason, command)
	return scan.Skips[0]
}

func nothing(t *testing.T, command string) {
	t.Helper()
	scan := shellWrites(command, ShellWritesOptions{})
	if len(scan.Writes) != 0 || len(scan.Skips) != 0 {
		t.Fatalf("%q: want neither writes nor skips: %+v", command, scan)
	}
}

func sw(path, content string, appending bool, via string) ShellWrite {
	return ShellWrite{Path: path, Content: content, Append: appending, Via: via}
}

// twin: tests/shell-writes.test.ts:28
func TestShellCatRedirectHeredocGivesThePathAndTheBody(t *testing.T) {
	mustEqual(t, shellOnly(t, "cat > src/auth.ts <<EOF\nexport function login() {}\nconst a = 1;\nEOF"), sw("src/auth.ts", "export function login() {}\nconst a = 1;\n", false, "heredoc"), "write")
}

// twin: tests/shell-writes.test.ts:32
func TestShellRedirectAfterTheHeredocOperator(t *testing.T) {
	mustEqual(t, shellOnly(t, "cat <<'EOF' > out.ts\nx\nEOF\n"), sw("out.ts", "x\n", false, "heredoc"), "write")
}

// twin: tests/shell-writes.test.ts:36
func TestShellQuotedDelimiterKeepsVarsAndBackticksLiteral(t *testing.T) {
	for _, delimiter := range []string{"'EOF'", "\"EOF\"", "\\EOF"} {
		mustEqual(t, shellOnly(t, "cat > a.sh <<"+delimiter+"\necho \"$HOME\" `date`\nEOF").Content, "echo \"$HOME\" `date`\n", delimiter)
	}
}

// twin: tests/shell-writes.test.ts:42
func TestShellUnquotedDelimiterReadsEscapesAndKeepsALoneDollar(t *testing.T) {
	mustEqual(t, shellOnly(t, "cat > a.txt <<EOF\nprice: 5$ and \\$HOME \\\\ \\n\nEOF").Content, "price: 5$ and $HOME \\ \\n\n", "content")
}

// twin: tests/shell-writes.test.ts:46
func TestShellDashHeredocStripsLeadingTabs(t *testing.T) {
	mustEqual(t, shellOnly(t, "cat <<-EOF > a.ts\n\tif (x) {\n\t\treturn 1;\n\t}\n\tEOF").Content, "if (x) {\nreturn 1;\n}\n", "content")
}

// twin: tests/shell-writes.test.ts:50
func TestShellTeeAndHereString(t *testing.T) {
	mustEqual(t, shellOnly(t, "tee src/new.ts <<'EOF'\nexport const n = 1;\nEOF"), sw("src/new.ts", "export const n = 1;\n", false, "heredoc"), "tee")
	mustEqual(t, shellOnly(t, "tee -a log.md >/dev/null <<'EOF'\nline\nEOF"), sw("log.md", "line\n", true, "heredoc"), "tee -a")
	mustEqual(t, shellOnly(t, "cat <<< 'const x = 1;' > x.ts"), sw("x.ts", "const x = 1;\n", false, "here-string"), "here-string")
}

// twin: tests/shell-writes.test.ts:56
func TestShellEchoAndPrintfRedirections(t *testing.T) {
	mustEqual(t, shellOnly(t, "echo \"export const x = 1\" > src/index.ts"), sw("src/index.ts", "export const x = 1\n", false, "echo"), "echo")
	mustEqual(t, shellOnly(t, "echo -n 'a  b' c>o.txt").Content, "a  b c", "echo -n")
	mustEqual(t, shellOnly(t, "echo -e 'a\\tb' > o.txt").Content, "a\tb\n", "echo -e")
	mustEqual(t, shellOnly(t, "printf '%s\\n' 'const key = \"k\"' > config.ts"), sw("config.ts", "const key = \"k\"\n", false, "printf"), "printf")
	mustEqual(t, shellOnly(t, "printf '%s=%d\\n' a 1 b 2 > kv.txt").Content, "a=1\nb=2\n", "printf reuse")
	mustEqual(t, shellOnly(t, "printf 'no newline' >| p.txt").Content, "no newline", "clobber")
}

// twin: tests/shell-writes.test.ts:65
func TestShellAppendOnlyTheAppendedText(t *testing.T) {
	mustEqual(t, shellOnly(t, "echo \"export const y = 2\" >> src/index.ts"), sw("src/index.ts", "export const y = 2\n", true, "echo"), "echo")
	mustEqual(t, shellOnly(t, "cat >> notes.md <<'EOF'\nmore\nEOF"), sw("notes.md", "more\n", true, "heredoc"), "cat")
}

// twin: tests/shell-writes.test.ts:70
func TestShellAChainWithTwoTargetsGivesOneWritePerTarget(t *testing.T) {
	scan := shellWrites("mkdir -p src && cat > src/a.ts <<'A' && echo 'export const b = 2;' >> src/b.ts; printf 'x' 2>/dev/null > c.txt\nexport const a = 1;\nA\n", homeMe)
	mustEqual(t, len(scan.Skips), 0, "skips")
	type triple struct {
		path, content string
		appending     bool
	}
	got := []triple{}
	for _, w := range scan.Writes {
		got = append(got, triple{w.Path, w.Content, w.Append})
	}
	mustEqual(t, got, []triple{{"src/a.ts", "export const a = 1;\n", false}, {"src/b.ts", "export const b = 2;\n", true}, {"c.txt", "x", false}}, "writes")
}

// twin: tests/shell-writes.test.ts:80
func TestShellDescriptorRedirectionsAndDevTargetsAreNotWrites(t *testing.T) {
	scan := shellWrites("npm test 2>&1 | tail -5; echo done >&2; echo x > /dev/null; grep '>' a.txt; echo 'a > b'", ShellWritesOptions{})
	mustEqual(t, len(scan.Writes), 0, "writes")
	mustEqual(t, len(shellWrites("npm test 2> err.log", ShellWritesOptions{}).Skips), 0, "stderr to a file")
}

// twin: tests/shell-writes.test.ts:85
func TestShellLeadingTildeExpandsToTheHomeDirectory(t *testing.T) {
	mustEqual(t, shellOnly(t, "echo hi > ~/notes.txt").Path, "/home/me/notes.txt", "expanded")
	mustEqual(t, shellOnly(t, "echo hi > '~/lit.txt'").Path, "~/lit.txt", "quoted")
}

// twin: tests/shell-writes.test.ts:90
func TestShellSkippedFormsGiveNoWriteAndAReason(t *testing.T) {
	mustEqual(t, shellSkipped(t, "echo 'x' | tee src/a.ts", `through a pipe`).Path, "src/a.ts", "path")
	shellSkipped(t, "git show HEAD:src/a.ts | tee -a src/a.ts", `through a pipe`)
	shellSkipped(t, "tee a.txt <<< \"$VALUE\"", `shell expansion`)
	shellSkipped(t, "echo \"key=$API_KEY\" > .env.example", `shell expansion`)
	shellSkipped(t, "echo $(date) > stamp.txt", `shell expansion`)
	shellSkipped(t, "echo \"`whoami`\" > who.txt", `shell expansion`)
	shellSkipped(t, "cat > a.ts <<EOF\nconst home = \"$HOME\";\nEOF", `shell expansion`)
	shellSkipped(t, "cat > a.ts <<EOF\nconst now = `date`;\nEOF", `shell expansion`)
	shellSkipped(t, "echo x > \"$OUT\"", `target path uses shell expansion`)
	shellSkipped(t, "sed -i 's/a/b/' src/a.ts", `sed -i`)
	shellSkipped(t, "sed --in-place=.bak 's/a/b/' src/a.ts", `sed -i`)
	shellSkipped(t, "patch -p1 < fix.diff", `patch applies a diff`)
	shellSkipped(t, "git apply fix.diff", `git apply`)
	shellSkipped(t, "printf '%-10s' x > pad.txt", `conversion that is not judged`)
	shellSkipped(t, "cd src && echo x > a.ts", `changes directory first`)
}

// twin: tests/shell-writes.test.ts:108
func TestShellAProgramsOutputOrAFilesContentIsNeitherAWriteNorASkip(t *testing.T) {
	for _, command := range []string{"node gen.js > src/gen.ts", "npm test > test.log 2>&1", "cat template.ts > src/a.ts", "cat < template.ts > src/a.ts", "git show HEAD:src/a.ts | cat > src/a.ts", "cat <(curl -s https://example.com) > page.html", "diff <(ls) <(ls a) > d.txt", "cd src && node gen.js > gen.ts", "node gen.js > \"$OUT\""} {
		nothing(t, command)
	}
}

// twin: tests/shell-writes.test.ts:114
func TestShellTruncationAndDescriptorOnlyCommands(t *testing.T) {
	nothing(t, "> empty.txt; : > other.txt")
	nothing(t, "ls -la && npm test")
}

// twin: tests/shell-writes.test.ts:119
func TestShellExecRedirectSendsLaterOutputToTheFile(t *testing.T) {
	mustEqual(t, shellOnly(t, "exec > f; echo x"), sw("f", "x\n", false, "echo"), "exec")
	type pair struct {
		content   string
		appending bool
	}
	got := []pair{}
	for _, w := range shellWrites("exec > f\necho a\nnpm test\necho b", ShellWritesOptions{}).Writes {
		got = append(got, pair{w.Content, w.Append})
	}
	mustEqual(t, got, []pair{{"a\n", false}, {"b\n", true}}, "the file stays open, so later text appends")
	nothing(t, "exec > /dev/null; echo x")
	mustEqual(t, shellOnly(t, "exec > f; echo x > g"), sw("g", "x\n", false, "echo"), "an own redirect wins")
}

// twin: tests/shell-writes.test.ts:126
func TestShellRedirectedGroupWritesTheTextItsCommandsPrint(t *testing.T) {
	mustEqual(t, shellOnly(t, "{ echo x; } > f"), sw("f", "x\n", false, "echo"), "brace group")
	mustEqual(t, shellOnly(t, "(echo x) > f"), sw("f", "x\n", false, "echo"), "subshell")
	mustEqual(t, shellOnly(t, "{ echo a; printf 'b\\n'; } >> f; echo c"), sw("f", "a\nb\n", true, "echo"), "append group")
	got := [][2]string{}
	for _, w := range shellWrites("{ echo a; echo b > g; } > f", ShellWritesOptions{}).Writes {
		got = append(got, [2]string{w.Path, w.Content})
	}
	mustEqual(t, got, [][2]string{{"g", "b\n"}, {"f", "a\n"}}, "a command with its own redirect prints nothing into the group")
	shellSkipped(t, "(echo x; npm test) > f", `also runs a program`)
	nothing(t, "(cd app && npm test) > log")
}

// twin: tests/shell-writes.test.ts:135
func TestShellEnvBeforeTheCommandIsAWrapper(t *testing.T) {
	mustEqual(t, shellOnly(t, "env echo x > f"), sw("f", "x\n", false, "echo"), "env")
	mustEqual(t, shellOnly(t, "env -i -u HOME A=1 echo x > f"), sw("f", "x\n", false, "echo"), "env with options")
}

// twin: tests/shell-writes.test.ts:140
func TestShellADevDotDotTargetIsNormalizedBeforeTheDeviceTest(t *testing.T) {
	mustEqual(t, shellOnly(t, "echo x > /dev/../tmp/p/f"), sw("/dev/../tmp/p/f", "x\n", false, "echo"), "not a device")
	nothing(t, "echo x > //dev/null")
}

// twin: tests/shell-writes.test.ts:145
func TestShellAPartlyQuotedHeredocDelimiter(t *testing.T) {
	mustEqual(t, shellOnly(t, "cat > f <<E\"OF\"\n$HOME\nEOF\necho done"), sw("f", "$HOME\n", false, "heredoc"), "E\"OF\"")
	mustEqual(t, shellOnly(t, "cat > f <<'E'OF\na\nEOF"), sw("f", "a\n", false, "heredoc"), "'E'OF")
	mustEqual(t, shellOnly(t, "cat > f <<E\\OF\n$x\nEOF"), sw("f", "$x\n", false, "heredoc"), "E\\OF")
}

// twin: tests/shell-writes.test.ts:151
func TestMergeWritesJoinsAppendsByPath(t *testing.T) {
	w := func(path, content string, appending bool) ShellWrite { return sw(path, content, appending, "echo") }
	mustEqual(t, mergeWrites([]ShellWrite{w("a", "1\n", true), w("b", "x\n", false), w("a", "2\n", true)}), []ShellWrite{w("a", "1\n2\n", true), w("b", "x\n", false)}, "appends join")
	mustEqual(t, mergeWrites([]ShellWrite{w("a", "1\n", true), w("a", "2\n", false), w("a", "3\n", true)}), []ShellWrite{w("a", "2\n3\n", false)}, "a truncating write replaces")
}

// twin: tests/shell-writes.test.ts:157
func TestShellExtractionIsDeterministic(t *testing.T) {
	command := "cat > a.ts <<'EOF'\nexport const a = 1;\nEOF\necho b >> b.ts"
	mustEqual(t, shellWrites(command, ShellWritesOptions{}), shellWrites(command, ShellWritesOptions{}), "twice")
}

// twin: tests/shell-writes.test.ts:177
func TestAHeredocToAGitignoredPathOrOutsideTheProjectIsSkippedLikeARealWrite(t *testing.T) {
	project := t.TempDir()
	if out, err := exec.Command("git", "-C", project, "init").CombinedOutput(); err != nil {
		t.Skipf("git is not available: %v %s", err, out)
	}
	writeFile(t, filepath.Join(project, ".gitignore"), "build/\n")
	writeFile(t, filepath.Join(project, "pi-warden.md"), "# No console\nDo not use console.log.\n")
	judge := &recordingJudge{handler: func(Request) (Evaluation, error) {
		return Evaluation{}, &IntegrationError{Code: "response", Message: "not expected"}
	}}
	guard := NewRulesGuard()
	options := rulesOpts(project, rulesConfig(), nil, judge)
	writes := shellWrites("cat > build/out.js <<'EOF'\nconsole.log(1)\nEOF\ncat > /tmp/elsewhere.js <<'EOF'\nconsole.log(2)\nEOF", ShellWritesOptions{}).Writes
	ignored, outside := writes[0], writes[1]
	ignoredVerdict := guard.Inspect(RulesCallRef{ID: "s1", Tool: "write", Input: writeInput(ignored.Path, ignored.Content)}, nil, options).Wait()
	mustEqual(t, ignoredVerdict.Source, "skipped", "ignored source")
	mustMatch(t, ignoredVerdict.SkippedReason, `gitignored`, "ignored reason")
	outsideVerdict := guard.Inspect(RulesCallRef{ID: "s2", Tool: "write", Input: writeInput(outside.Path, outside.Content)}, nil, options).Wait()
	mustEqual(t, outsideVerdict.Source, "skipped", "outside source")
	mustMatch(t, outsideVerdict.SkippedReason, `outside the project`, "outside reason")
	mustEqual(t, len(judge.calls()), 0, "no rules request for either")
}

var _ = regexp.MustCompile
var _ = strings.Contains
