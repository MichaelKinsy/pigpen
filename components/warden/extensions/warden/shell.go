package warden

import (
	"regexp"
	"strings"
)

// Shell reading (src/guard.ts): segment splitting, data-text stripping and the read-only shortcut.
// Everything here is a latency optimisation or a false-positive filter, not a security boundary.

var (
	reSplitShell   = regexp.MustCompile(`\n|;|&&|\|\||\||&`)
	reAssignment   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
	reHeredoc      = regexp.MustCompile(`<<-?\s*(?:"(\w+)"|'(\w+)'|(\\)?(\w+))`)
	reSubstitution = regexp.MustCompile("\\$\\(|`")
	reInterpreter  = regexp.MustCompile(`^(?:python[\d.]*|node|ruby|perl|php|deno|bun|tsx|Rscript|lua[\d.]*)$`)
	reExecCalls    = regexp.MustCompile("\\b(?:os\\.system|os\\.popen|os\\.exec\\w*|subprocess|child_process|execSync|spawnSync|execFileSync|spawn\\(|exec\\(|system\\(|popen\\(|shell_exec|passthru|proc_open|Open3|IO\\.popen|Deno\\.run|Deno\\.Command|Bun\\.spawn|Bun\\.\\$|%x[\\[{(]|`[^`\\n]*\\b(?:rm|git|dd|mkfs|kubectl|terraform)\\b)")
	reShellC       = regexp.MustCompile(`\b(?:ba|z|da|k)?sh\s+-[a-zA-Z]*c\b`)
	reQuoteChar    = regexp.MustCompile(`["']`)
	reGitConfig    = regexp.MustCompile(`\s-c\s|--config`)
	reGhMessage    = regexp.MustCompile(`\s--(?:body|title|notes)\b`)
	reGhShort      = regexp.MustCompile(`\s-[bt]\s`)
	reShortMessage = regexp.MustCompile(`^-[a-zA-Z]*m$`)
	reLeadingPath  = regexp.MustCompile(`^.*/`)
)

func setOf(words ...string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}

var (
	wrappers           = setOf("sudo", "nohup", "time", "env", "command", "builtin", "exec", "nice", "timeout", "doas")
	shellSinks         = setOf("sh", "bash", "zsh", "dash", "ksh", "fish", "eval", "source", ".", "xargs", "su")
	dataHeads          = setOf("echo", "printf", "grep", "egrep", "fgrep", "rg", "ag", "ugrep", "jq", "cat", "tee", "head", "tail", "wc", "sort", "uniq", "cut", "tr", "less", "more", "test", "[")
	gitMessageSubs     = setOf("commit", "tag", "notes", "merge", "stash")
	ghMessageFlags     = setOf("--body", "-b", "--title", "-t", "--notes")
	ghMessageObjects   = setOf("pr", "issue", "release")
	ghMessageVerbs     = setOf("create", "edit", "comment")
	gitFlagSubcommands = setOf("commit", "tag")
)

func splitShell(command string) []string {
	var out []string
	for _, part := range reSplitShell.Split(command, -1) {
		if p := jsTrim(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func stripPathPrefix(s string) string { return reLeadingPath.ReplaceAllString(s, "") }

// headOf is the command word of a segment after assignments and wrappers, with any directory removed.
func headOf(segment string) (string, bool) {
	tokens := jsSplitWhitespace(jsTrim(segment))
	i := 0
	for i < len(tokens) && (reAssignment.MatchString(tokens[i]) || wrappers[tokens[i]]) {
		i++
	}
	if i >= len(tokens) || tokens[i] == "" {
		return "", false
	}
	return stripPathPrefix(tokens[i]), true
}

func headOrEmpty(segment string) string {
	h, _ := headOf(segment)
	return h
}

var visibleSubcommands = map[string]map[string]bool{
	"git": setOf("push", "commit", "merge", "tag", "reset"),
	"gh":  setOf("pr", "release"),
	"npm": setOf("publish"),
}

var reSubshellSplit = regexp.MustCompile("\\$\\(|`|\\(")
var reTrailParenTick = regexp.MustCompile("[)`]+$")

// IsVisibleCommand reports whether a shell command has a segment whose effect is visible outside the
// working tree (a push, a commit, a pull request, a release, a published package). Quoted data such as a
// commit message is blanked first, so a message that mentions `git push` is not a push.
func IsVisibleCommand(command string) bool {
	for _, seg := range splitShell(StripDataText(command).Text) {
		for _, part := range reSubshellSplit.Split(seg, -1) {
			head, ok := headOf(part)
			if !ok {
				continue
			}
			subs, ok := visibleSubcommands[head]
			if !ok {
				continue
			}
			tokens := jsSplitWhitespace(jsTrim(part))
			start := 0
			for i, t := range tokens {
				if stripPathPrefix(t) == head {
					start = i + 1
					break
				}
			}
			for i := start; i < len(tokens); i++ {
				t := tokens[i]
				if t == "-C" || t == "-c" || t == "-R" || t == "--repo" {
					i++
					continue
				}
				if strings.HasPrefix(t, "-") {
					continue
				}
				if subs[reTrailParenTick.ReplaceAllString(t, "")] {
					return true
				}
				break
			}
		}
	}
	return false
}

// blankQuotes replaces quoted strings by a placeholder; escapes inside double quotes are honoured, single
// quotes take everything. A double-quoted string that substitutes a command or expands a credential
// variable stays visible.
func blankQuotes(segment string) string {
	var out strings.Builder
	for i := 0; i < len(segment); i++ {
		c := segment[i]
		if c != '\'' && c != '"' {
			out.WriteByte(c)
			continue
		}
		end := i + 1
		for end < len(segment) && segment[end] != c {
			if c == '"' && segment[end] == '\\' {
				end += 2
			} else {
				end++
			}
		}
		if end >= len(segment) {
			out.WriteString(segment[i:])
			break
		}
		inner := segment[i+1 : end]
		if c == '"' && (reSubstitution.MatchString(inner) || secretExpansion(inner)) {
			out.WriteByte(c)
			out.WriteString(inner)
			out.WriteByte(c)
		} else {
			out.WriteByte(c)
			out.WriteString("[text]")
			out.WriteByte(c)
		}
		i = end
	}
	return out.String()
}

func isDataSegment(segment string) bool {
	head, ok := headOf(segment)
	if !ok {
		return false
	}
	if head == "git" {
		for _, t := range jsSplitWhitespace(jsTrim(segment)) {
			if !strings.HasPrefix(t, "-") && t != "git" && !wrappers[t] {
				return gitMessageSubs[t] && !reGitConfig.MatchString(segment)
			}
		}
		return false
	}
	if head == "gh" {
		return reGhMessage.MatchString(segment) || reGhShort.MatchString(segment)
	}
	return dataHeads[head]
}

// isMessageFlag reports whether flag, read after words of one simple command, takes a message as its value.
func isMessageFlag(words []string, flag string) bool {
	i := 0
	for i < len(words) && (reAssignment.MatchString(words[i]) || wrappers[words[i]]) {
		i++
	}
	if i >= len(words) {
		return false
	}
	head := stripPathPrefix(words[i])
	rest := words[i+1:]
	at := func(k int) string {
		if k < len(rest) {
			return rest[k]
		}
		return ""
	}
	if head == "gh" {
		return ghMessageObjects[at(0)] && ghMessageVerbs[at(1)] && ghMessageFlags[flag]
	}
	if head != "git" {
		return false
	}
	// `git -c alias.x=!cmd` runs a shell command, so a config override keeps the whole command in scope.
	for _, w := range rest {
		if w == "-c" || strings.HasPrefix(w, "--config") {
			return false
		}
	}
	sub := 0
	for sub < len(rest) && strings.HasPrefix(rest[sub], "-") {
		if rest[sub] == "-C" {
			sub += 2
		} else {
			sub++
		}
	}
	return gitFlagSubcommands[at(sub)] && sub < len(rest) && (flag == "--message" || reShortMessage.MatchString(flag))
}

func slice2(s string, i int) string {
	if i+2 > len(s) {
		return s[i:]
	}
	return s[i : i+2]
}

// blankMessageFlags blanks the quoted values of message flags (`gh pr create --body`, `git commit -m`
// and their `=` forms) across the whole command. It reads quotes before separators, so a body that holds
// `;`, `&&` or new lines stays one value. A value with `$(`, backticks or an unclosed quote is kept.
func blankMessageFlags(command string) string {
	var out strings.Builder
	var words []string
	word := ""
	pending := false
	flush := func() {
		if word == "" {
			return
		}
		pending = isMessageFlag(words, word)
		words = append(words, word)
		word = ""
	}
	for i := 0; i < len(command); {
		c := command[i]
		if c == '\n' || c == ';' || c == '&' || c == '|' {
			flush()
			words = nil
			pending = false
			out.WriteByte(c)
			i++
			continue
		}
		if isSpaceByte(c) {
			flush()
			out.WriteByte(c)
			i++
			continue
		}
		if c == '\\' {
			two := slice2(command, i)
			word += two
			out.WriteString(two)
			i += 2
			continue
		}
		ansi := c == '$' && i+1 < len(command) && command[i+1] == '\''
		if c != '\'' && c != '"' && !ansi {
			word += string(c)
			out.WriteByte(c)
			i++
			continue
		}
		open := i
		if ansi {
			open = i + 1
		}
		quote := command[open]
		end := open + 1
		for end < len(command) && command[end] != quote {
			if quote != '\'' || ansi {
				if command[end] == '\\' {
					end += 2
					continue
				}
			}
			end++
		}
		if end >= len(command) {
			return out.String() + command[i:]
		}
		raw := command[i : end+1]
		inner := command[open+1 : end]
		flagValue := (word == "" && pending) || (strings.HasSuffix(word, "=") && isMessageFlag(words, strings.TrimSuffix(word, "=")))
		if flagValue && !reSubstitution.MatchString(inner) {
			blank := string(quote) + "[text]" + string(quote)
			out.WriteString(blank)
			word += blank
		} else {
			out.WriteString(raw)
			word += raw
		}
		pending = false
		i = end + 1
	}
	return out.String()
}

// ScannedCommand is a command with data text blanked.
type ScannedCommand struct {
	// Text is the command with data text blanked; what the pattern rules read.
	Text string
	// Stripped is true when a heredoc body or quoted data was removed.
	Stripped bool
}

// StripDataText removes heredoc bodies that are not fed to a shell and quoted arguments of data commands.
// Interpreter heredocs (`python3 - <<EOF`) are kept when the script calls out to a shell or process API.
func StripDataText(command string) ScannedCommand {
	lines := strings.Split(command, "\n")
	var out []string
	stripped := false
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		m := reHeredoc.FindStringSubmatchIndex(line)
		if m == nil {
			out = append(out, line)
			continue
		}
		sub := func(n int) (string, bool) {
			if m[2*n] < 0 {
				return "", false
			}
			return line[m[2*n]:m[2*n+1]], true
		}
		var delimiter string
		if d, ok := sub(1); ok {
			delimiter = d
		} else if d, ok := sub(2); ok {
			delimiter = d
		} else {
			delimiter, _ = sub(4)
		}
		_, has4 := sub(4)
		_, has3 := sub(3)
		// 'EOF', "EOF" and \EOF make the body literal; a bare EOF body is expanded, so a substitution inside it runs.
		literal := !has4 || has3
		var body []string
		closeAt := i + 1
		for closeAt < len(lines) && strings.TrimLeft(lines[closeAt], "\t") != delimiter {
			body = append(body, lines[closeAt])
			closeAt++
		}
		bodyText := strings.Join(body, "\n")
		executed := false
		for _, seg := range splitShell(line) {
			if shellSinks[headOrEmpty(seg)] {
				executed = true
			}
		}
		consumer := headOrEmpty(line[:m[0]])
		if !executed {
			executed = (reInterpreter.MatchString(consumer) && reExecCalls.MatchString(bodyText)) || (!literal && reSubstitution.MatchString(bodyText))
		}
		out = append(out, line)
		if executed {
			out = append(out, body...)
		} else if len(body) > 0 {
			out = append(out, "[heredoc body: "+itoaInt(len(body))+" lines of data]")
			stripped = true
		}
		if closeAt < len(lines) {
			out = append(out, lines[closeAt])
		}
		i = closeAt
	}
	scanned := strings.Join(out, "\n")
	joined := blankMessageFlags(scanned)
	segments := splitShell(joined)
	// A shell sink anywhere may run text written earlier in the same command, so nothing is data.
	for _, seg := range segments {
		if h, ok := headOf(seg); ok && shellSinks[h] {
			return ScannedCommand{Text: command}
		}
	}
	if reShellC.MatchString(joined) {
		return ScannedCommand{Text: command}
	}
	text := joined
	if joined != scanned {
		stripped = true
	}
	for _, seg := range segments {
		if !isDataSegment(seg) || !reQuoteChar.MatchString(seg) {
			continue
		}
		blanked := blankQuotes(seg)
		if blanked == seg {
			continue
		}
		text = strings.Replace(text, seg, blanked, 1)
		stripped = true
	}
	return ScannedCommand{Text: text, Stripped: stripped}
}

// ---------------------------------------------------------------------------
// Read-only shell detection: a latency optimisation, not a security boundary. Runs only when no pattern matched.

var (
	readOnlyCommands = setOf("ls", "cat", "head", "tail", "less", "more", "wc", "grep", "rg", "egrep", "fgrep", "ag", "find", "fd", "pwd", "echo", "printf", "which", "whereis", "type",
		"file", "stat", "du", "df", "tree", "diff", "sort", "uniq", "cut", "tr", "cd", "true", "false", "test", "[", "date", "basename", "dirname", "realpath",
		"readlink", "jq", "column", "nl", "strings", "md5", "md5sum", "shasum", "sha1sum", "sha256sum", "hexdump", "xxd", "od", "uname", "hostname", "whoami", "id", "uptime")
	readOnlyGit      = setOf("status", "log", "diff", "show", "blame", "ls-files", "ls-tree", "rev-parse", "describe", "shortlog", "grep", "cat-file", "rev-list", "name-rev", "merge-base")
	readOnlyGitList  = setOf("worktree", "stash")
	reReadOnlyAssign = regexp.MustCompile(`^(?:LANG|LC_[A-Z]+|TZ|NO_COLOR|TERM|COLUMNS|FORCE_COLOR)=`)
	sedAddress       = `(?:\d+|\$|/(?:[^/\\]|\\.)*/)`
	reSedPrint       = regexp.MustCompile(`^(?:` + sedAddress + `(?:,(?:` + sedAddress + `|\+\d+))?)?!?p$`)
	reSedOption      = regexp.MustCompile(`^-[nErsuz]*e?$`)
	reLiteralWord    = []*regexp.Regexp{regexp.MustCompile(`^'[^']*'$`), regexp.MustCompile(`^"[^"$\\]*"$`), regexp.MustCompile(`^[\w,+!/.=-]+$`)}
	gitGrepShort     = "nlLiIwcEFPGvhHoqaWz0123456789"
	gitGrepShortVal  = "ABCefm"
	gitGrepLong      = setOf("line-number", "files-with-matches", "name-only", "files-without-match", "ignore-case", "word-regexp", "count", "extended-regexp",
		"basic-regexp", "fixed-strings", "perl-regexp", "invert-match", "heading", "break", "color", "no-color", "cached", "untracked",
		"no-index", "recurse-submodules", "max-depth", "context", "after-context", "before-context", "function-context", "show-function",
		"all-match", "and", "or", "not", "full-name", "null", "only-matching", "column", "quiet", "text", "max-count", "threads",
		"exclude-standard", "no-exclude-standard", "textconv", "no-textconv", "no-recursive", "recursive")
	reSortOut   = regexp.MustCompile(`^-[A-Za-z]*o|^--o`)
	reTreeOut   = regexp.MustCompile(`^-[A-Za-z]*[oR]|^--o`)
	reXxdRev    = regexp.MustCompile(`^-[A-Za-z]*r|^--?revert`)
	reProcSub   = regexp.MustCompile("\\$\\(|`|<\\(")
	reFdDup     = regexp.MustCompile(`\d?>\s*&\s*\d`)
	reDevNull   = regexp.MustCompile(`&?\d?>\s*/dev/null`)
	reOutput    = regexp.MustCompile(`(?:^|\s)--output\b`)
	reBranchW   = regexp.MustCompile(`\s-[a-zA-Z]*[dDmMcCu]|--(?:delete|move|copy|set-upstream|unset-upstream|edit-description)`)
	reGitCfgRO  = regexp.MustCompile(`--get|--list|-l\b`)
	reFindExec  = regexp.MustCompile(`-(?:delete|exec\w*|ok\w*|fprint\w*|fls)\b`)
	reEscapes   = regexp.MustCompile(`["'\\]`)
	reDoubleEsc = regexp.MustCompile(`\\(["\\$` + "`" + `])`)
)

type shellWord struct{ word, raw string }

func closingDoubleQuote(text string, from int) int {
	for i := from; i < len(text); i++ {
		if text[i] == '\\' {
			i++
		} else if text[i] == '"' {
			return i
		}
	}
	return -1
}

// shellWords are the words of text with quotes and backslash escapes resolved; raw keeps the source.
// ok is false on an unclosed quote.
func shellWords(text string) ([]shellWord, bool) {
	var words []shellWord
	i := 0
	for i < len(text) {
		for i < len(text) && isSpaceByte(text[i]) {
			i++
		}
		if i >= len(text) {
			break
		}
		start := i
		var w strings.Builder
		for i < len(text) && !isSpaceByte(text[i]) {
			c := text[i]
			switch {
			case c == '\'' || c == '"':
				var closeAt int
				if c == '\'' {
					k := strings.IndexByte(text[i+1:], '\'')
					closeAt = -1
					if k >= 0 {
						closeAt = i + 1 + k
					}
				} else {
					closeAt = closingDoubleQuote(text, i+1)
				}
				if closeAt == -1 {
					return nil, false
				}
				inner := text[i+1 : closeAt]
				if c != '\'' {
					inner = reDoubleEsc.ReplaceAllString(inner, "$1")
				}
				w.WriteString(inner)
				i = closeAt + 1
			case c == '\\' && i+1 < len(text):
				w.WriteByte(text[i+1])
				i += 2
			default:
				w.WriteByte(c)
				i++
			}
		}
		words = append(words, shellWord{word: w.String(), raw: text[start:i]})
	}
	return words, true
}

func isLiteralWord(raw string) bool {
	for _, re := range reLiteralWord {
		if re.MatchString(raw) {
			return true
		}
	}
	return false
}

// isReadOnlySed reports whether a segment is a `sed -n` that only prints.
func isReadOnlySed(segment string) bool {
	words, ok := shellWords(segment)
	if !ok {
		return false
	}
	i := 0
	for i < len(words) && reAssignment.MatchString(words[i].word) {
		i++
	}
	quiet, operands, scripts := false, false, 0
	var script *shellWord
	for k := i + 1; k < len(words); k++ {
		w := words[k].word
		if strings.HasPrefix(w, "-") {
			if operands {
				return false
			}
			if w == "--quiet" || w == "--silent" {
				quiet = true
				continue
			}
			if !reSedOption.MatchString(w) || w == "-" {
				return false
			}
			if strings.Contains(w, "n") {
				quiet = true
			}
			if strings.HasSuffix(w, "e") {
				k++
				if k >= len(words) {
					return false
				}
				script = &words[k]
				scripts++
			}
			continue
		}
		if !operands && scripts == 0 {
			script = &words[k]
			scripts++
		}
		operands = true
	}
	return quiet && scripts == 1 && script != nil && isLiteralWord(script.raw) && reSedPrint.MatchString(script.word)
}

func isReadOnlyGitGrep(words []string) bool {
	for k := 0; k < len(words); k++ {
		raw := words[k]
		if raw == "--" {
			return true
		}
		word := reEscapes.ReplaceAllString(raw, "")
		if !strings.HasPrefix(word, "-") || word == "-" {
			continue
		}
		if strings.HasPrefix(word, "--") {
			name := word[2:]
			if i := strings.IndexByte(name, '='); i >= 0 {
				name = name[:i]
			}
			if !gitGrepLong[name] {
				return false
			}
			continue
		}
		for c := 1; c < len(word); c++ {
			flag := word[c]
			if strings.IndexByte(gitGrepShortVal, flag) >= 0 {
				if c == len(word)-1 {
					k++
				}
				break
			}
			if strings.IndexByte(gitGrepShort, flag) < 0 {
				return false
			}
		}
	}
	return true
}

func operandsOf(words []string, valued map[string]bool) []string {
	var operands []string
	for k := 0; k < len(words); k++ {
		w := words[k]
		if w == "--" {
			operands = append(operands, words[k+1:]...)
			break
		}
		if strings.HasPrefix(w, "-") && w != "-" {
			if valued[w] {
				k++
			}
			continue
		}
		operands = append(operands, w)
	}
	return operands
}

func anyMatch(words []string, re *regexp.Regexp) bool {
	for _, w := range words {
		if re.MatchString(w) {
			return true
		}
	}
	return false
}

func writesOutputFile(head string, words []string) bool {
	switch head {
	case "sort":
		return anyMatch(words, reSortOut)
	case "tree":
		return anyMatch(words, reTreeOut)
	case "uniq":
		return len(operandsOf(words, setOf("-f", "-s", "-w"))) > 1
	case "xxd":
		return anyMatch(words, reXxdRev) || len(operandsOf(words, setOf("-c", "-g", "-l", "-o", "-s", "-n", "-cols", "-len", "-seek", "-groupsize", "-name"))) > 1
	}
	return false
}

// IsReadOnlyCommand reports whether every segment of the command only reads.
func IsReadOnlyCommand(command string) bool {
	if jsTrim(command) == "" || reProcSub.MatchString(command) {
		return false
	}
	stripped := reFdDup.ReplaceAllString(command, "")
	stripped = reDevNull.ReplaceAllString(stripped, "")
	if strings.Contains(stripped, ">") {
		return false
	}
	for _, segment := range splitShell(stripped) {
		tokens := jsSplitWhitespace(segment)
		i := 0
		for i < len(tokens) && reAssignment.MatchString(tokens[i]) {
			if !reReadOnlyAssign.MatchString(tokens[i]) {
				return false
			}
			i++
		}
		if i >= len(tokens) || tokens[i] == "" {
			return false
		}
		head := tokens[i]
		if head == "git" {
			restTokens := tokens[i+1:]
			rest := strings.Join(restTokens, " ")
			if len(restTokens) == 0 || restTokens[0] == "" {
				return false
			}
			sub := restTokens[0]
			if reOutput.MatchString(rest) {
				return false
			}
			args := restTokens[1:]
			switch {
			case sub == "grep":
				if !isReadOnlyGitGrep(args) {
					return false
				}
			case readOnlyGitList[sub]:
				if len(args) == 0 || args[0] != "list" {
					return false
				}
			case sub == "branch":
				if reBranchW.MatchString(" " + rest) {
					return false
				}
			case sub == "remote":
				for _, t := range args {
					if !strings.HasPrefix(t, "-") {
						return false
					}
				}
			case sub == "tag":
				for _, t := range args {
					if !(t == "-l" || t == "--list" || strings.HasPrefix(t, "-n")) {
						return false
					}
				}
			case sub == "config":
				if !reGitCfgRO.MatchString(rest) {
					return false
				}
			default:
				if !readOnlyGit[sub] {
					return false
				}
			}
			continue
		}
		if head == "find" && reFindExec.MatchString(segment) {
			return false
		}
		if head == "sed" {
			if !isReadOnlySed(segment) {
				return false
			}
			continue
		}
		if writesOutputFile(head, tokens[i+1:]) {
			return false
		}
		if !readOnlyCommands[head] {
			return false
		}
	}
	return true
}
