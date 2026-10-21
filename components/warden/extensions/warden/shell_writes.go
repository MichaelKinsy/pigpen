package warden

import (
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

// File writes whose content is literal in a shell command (src/shell-writes.ts): heredocs into `cat`/`tee`,
// `echo`/`printf` redirections, here-strings, and `>>` appends. The rules, slop, and security guards judge them as
// `write` calls, so a heredoc is not a way around the checks a `write` gets. Nothing is executed. An authoring form
// whose text cannot be read (shell expansion, a pipe into `tee`) and an in-place change (`sed -i`, `patch`,
// `git apply`) are skipped with a reason for the trace; a program's output sent to a file is not an authoring form and
// is passed over silently.

// ShellWrite is a write whose content is literal in the command.
type ShellWrite struct {
	// Path is the target as written, with a leading unquoted `~` expanded.
	Path string
	// Content is the text the shell writes: for an append, only the appended text.
	Content string
	Append  bool
	// Via is the command that produces the content: heredoc | here-string | echo | printf.
	Via string
}

// ShellSkip is a write that is not judged, with the reason for the trace.
type ShellSkip struct {
	Path   string
	Reason string
}

// ShellWriteScan is the result of scanning one command.
type ShellWriteScan struct {
	Writes []ShellWrite
	Skips  []ShellSkip
}

type scanWord struct {
	text string
	// expanded: the shell substitutes into this word ($VAR, $(...), backticks, $'...'), so its value is not in the
	// command text.
	expanded bool
	// tilde: starts with an unquoted `~`, which the shell expands to the home directory.
	tilde bool
}

type scanRedirect struct {
	fd        string
	appending bool
	target    *scanWord
}

type scanInput struct {
	kind     string // heredoc | here-string
	text     string
	expanded bool
}

type scanGroup struct{ start, end int }

type scanSegment struct {
	words     []*scanWord
	redirects []*scanRedirect
	// input: the last heredoc or here-string wins stdin, as in the shell.
	input        *scanInput
	stdin        string // "" | pipe | file
	substitution bool
	// group is set on the segment after `)` or at `}`: its redirects take the output of segments start to end (exclusive).
	group *scanGroup
}

const shellExpansionReason = "the content uses shell expansion ($VAR, $(...), or backticks), so it is not in the command text"

// isExpansionStart is /[A-Za-z_{(0-9@*#?$!-]/ on one character.
func isExpansionStart(c byte) bool {
	switch {
	case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		return true
	}
	return strings.IndexByte("_{(@*#?$!-", c) >= 0
}

func byteAt(s string, i int) byte {
	if i >= 0 && i < len(s) {
		return s[i]
	}
	return 0
}

// closeParen is the index just past the `)` that closes the `(` at open, skipping quoted text.
func closeParen(command string, open int) int {
	depth := 0
	for index := open; index < len(command); index++ {
		switch command[index] {
		case '\\':
			index++
			continue
		case '\'':
			end := strings.IndexByte(command[index+1:], '\'')
			if end < 0 {
				index = len(command)
			} else {
				index = index + 1 + end
			}
			continue
		}
		if command[index] == '(' {
			depth++
		}
		if command[index] == ')' {
			depth--
			if depth == 0 {
				return index + 1
			}
		}
	}
	return len(command)
}

type heredocOp struct {
	delimiter string
	tabs      bool
	literal   bool
	end       int
}

// heredocOperator is the heredoc operator at index: bash removes the quotes from the delimiter word (<<E"OF" ends at
// EOF), and any quoted or escaped part keeps the body literal.
func heredocOperator(command string, index int) (heredocOp, bool) {
	at := index + 2
	tabs := byteAt(command, at) == '-'
	if tabs {
		at++
	}
	for byteAt(command, at) == ' ' || byteAt(command, at) == '\t' {
		at++
	}
	var delimiter strings.Builder
	literal := false
	for at < len(command) {
		r, size := utf8.DecodeRuneInString(command[at:])
		if isJSSpace(r) || strings.ContainsRune(";&|<>()", r) {
			break
		}
		char := command[at]
		if char == '\'' || char == '"' {
			closeAt := strings.IndexByte(command[at+1:], char)
			if closeAt < 0 {
				return heredocOp{}, false
			}
			closeAt += at + 1
			inner := command[at+1 : closeAt]
			if strings.Contains(inner, "\n") {
				return heredocOp{}, false
			}
			if char == '"' {
				inner = reHeredocEscape.ReplaceAllString(inner, "$1")
			}
			delimiter.WriteString(inner)
			literal = true
			at = closeAt + 1
			continue
		}
		if char == '\\' {
			if at+1 < len(command) {
				r2, size2 := utf8.DecodeRuneInString(command[at+1:])
				delimiter.WriteRune(r2)
				at += 1 + size2
			} else {
				at += 2
			}
			literal = true
			continue
		}
		delimiter.WriteRune(r)
		at += size
	}
	if delimiter.Len() == 0 {
		return heredocOp{}, false
	}
	return heredocOp{delimiter: delimiter.String(), tabs: tabs, literal: literal, end: at}, true
}

var (
	reHeredocEscape = lazyRE("\\\\([\"\\\\$`])")
	reDupFd         = lazyRE(`^(?:\d+|-)`)
	reFdDigits      = lazyRE(`^\d+$`)
	reEnvAssign     = lazyRE(`^[A-Za-z_][A-Za-z0-9_]*=`)
	reDevPath       = lazyRE(`^/dev/`)
	reEchoFlags     = lazyRE(`^-[neE]+$`)
	reOctalEscape   = lazyRE(`^0[0-7]{0,3}`)
	reSignedInt     = lazyRE(`^[+-]?\d+$`)
	reInPlaceFlag   = lazyRE(`^-[A-Za-z]*i`)
	reAppendFlag    = lazyRE(`^-[A-Za-z]*a`)
	reDirPrefix     = lazyRE(`^.*/`)
)

// heredocText applies bash's heredoc rules for an unquoted delimiter: `\$`, “ \` “, `\\` and a line continuation are
// escapes; other backslashes stay.
func heredocText(body string, literal bool) (text string, expanded bool) {
	if literal || !strings.ContainsAny(body, "\\`$") {
		return body, false
	}
	var out strings.Builder
	out.Grow(len(body))
	for index := 0; index < len(body); index++ {
		char := body[index]
		next := byteAt(body, index+1)
		hasNext := index+1 < len(body)
		if char == '\\' && hasNext && strings.IndexByte("$`\\\n", next) >= 0 {
			if next != '\n' {
				out.WriteByte(next)
			}
			index++
			continue
		}
		if char == '`' || (char == '$' && hasNext && isExpansionStart(next)) {
			return body, true
		}
		out.WriteByte(char)
	}
	return out.String(), false
}

// scanDoubleQuoted is where the double-quoted text that starts at from ends (the closing quote, or the end of the
// command), whether it holds an expansion, and whether a backslash escape in it drops a character.
func scanDoubleQuoted(command string, from int) (end int, expanded, escaped bool) {
	end = from
	for end < len(command) && command[end] != '"' {
		at := command[end]
		after := byteAt(command, end+1)
		hasAfter := end+1 < len(command)
		if at == '\\' && hasAfter && strings.IndexByte("\"\\$`\n", after) >= 0 {
			escaped = true
			end += 2
			continue
		}
		if at == '`' || (at == '$' && hasAfter && isExpansionStart(after)) {
			expanded = true
		}
		if at == '$' && after == '(' {
			end = closeParen(command, end+1)
			continue
		}
		end++
	}
	return end, expanded, escaped
}

// doubleQuotedText is the text of the double-quoted string that starts at from, with its escapes resolved.
func doubleQuotedText(command string, from int) (text string, end int, expanded bool) {
	var out strings.Builder
	end = from
	for end < len(command) && command[end] != '"' {
		at := command[end]
		after := byteAt(command, end+1)
		hasAfter := end+1 < len(command)
		if at == '\\' && hasAfter && strings.IndexByte("\"\\$`\n", after) >= 0 {
			if after != '\n' {
				out.WriteByte(after)
			}
			end += 2
			continue
		}
		if at == '`' || (at == '$' && hasAfter && isExpansionStart(after)) {
			expanded = true
		}
		if at == '$' && after == '(' {
			closeAt := closeParen(command, end+1)
			out.WriteString(command[end:closeAt])
			end = closeAt
			continue
		}
		out.WriteByte(at)
		end++
	}
	return out.String(), end, expanded
}

type pendingHeredoc struct {
	segment *scanSegment
	op      heredocOp
}

func scanShell(command string) []*scanSegment {
	var segments []*scanSegment
	segment := &scanSegment{}
	var word *scanWord
	var redirect *scanRedirect
	// inputFile: the word after `<` names an input file, not an argument.
	inputFile := false
	hereString := false
	var pending []pendingHeredoc
	// groups: where each open `{` or `(` group starts in segments.
	var groups []int
	popGroup := func() (int, bool) {
		if len(groups) == 0 {
			return 0, false
		}
		g := groups[len(groups)-1]
		groups = groups[:len(groups)-1]
		return g, true
	}
	// The open word's text is command[wordLo:wordHi] while it is contiguous there, and wordBuf once it is not.
	var wordLo, wordHi int
	var wordBuf []byte
	wordBuffered := false
	finalizeWord := func() {
		if wordBuffered {
			word.text = string(wordBuf)
		} else {
			word.text = command[wordLo:wordHi]
		}
	}
	// fdWord: the open word is digits only, so it names a descriptor for a redirection that follows it.
	fdWord := func() bool {
		finalizeWord()
		return reFdDigits.MatchString(word.text)
	}
	endWord := func() {
		if word == nil {
			return
		}
		finalizeWord()
		bare := len(segment.words) == 0 && redirect == nil && !hereString && !inputFile
		if bare && word.text == "{" {
			groups = append(groups, len(segments))
			word = nil
			return
		}
		if bare && word.text == "}" && len(groups) > 0 {
			start, _ := popGroup()
			segment.group = &scanGroup{start: start, end: len(segments)}
			word = nil
			return
		}
		switch {
		case redirect != nil:
			redirect.target = word
			segment.redirects = append(segment.redirects, redirect)
			redirect = nil
		case hereString:
			segment.input = &scanInput{kind: "here-string", text: word.text + "\n", expanded: word.expanded}
			hereString = false
		case inputFile:
			segment.stdin = "file"
			inputFile = false
		default:
			segment.words = append(segment.words, word)
		}
		word = nil
	}
	endSegment := func(piped bool) {
		endWord()
		redirect = nil
		inputFile, hereString = false, false
		if len(segment.words) > 0 || len(segment.redirects) > 0 || segment.input != nil {
			segments = append(segments, segment)
		}
		segment = &scanSegment{}
		if piped {
			segment.stdin = "pipe"
		}
	}
	// appendSlice appends command[a:b] to the open word, opening one when there is none.
	appendSlice := func(a, b int, expanded, tilde bool) {
		switch {
		case word == nil:
			word = &scanWord{tilde: tilde}
			wordLo, wordHi, wordBuffered = a, b, false
		case wordBuffered:
			wordBuf = append(wordBuf, command[a:b]...)
		case wordHi == a:
			wordHi = b
		case a == b:
		default:
			wordBuf = append(append(wordBuf[:0], command[wordLo:wordHi]...), command[a:b]...)
			wordBuffered = true
		}
		word.expanded = word.expanded || expanded
	}
	// appendString appends text that is not a slice of the command.
	appendString := func(text string, expanded bool) {
		switch {
		case word == nil:
			word = &scanWord{}
			wordBuf = append(wordBuf[:0], text...)
		case wordBuffered:
			wordBuf = append(wordBuf, text...)
		default:
			wordBuf = append(append(wordBuf[:0], command[wordLo:wordHi]...), text...)
		}
		wordBuffered = true
		word.expanded = word.expanded || expanded
	}
	index := 0
	for index < len(command) {
		char := command[index]
		next := byteAt(command, index+1)
		hasNext := index+1 < len(command)
		switch {
		case char == '\'':
			stop := len(command)
			if end := strings.IndexByte(command[index+1:], '\''); end >= 0 {
				stop = index + 1 + end
			}
			appendSlice(index+1, stop, false, false)
			index = stop + 1
			continue
		case char == '"':
			end, expanded, escaped := scanDoubleQuoted(command, index+1)
			if escaped {
				text, _, _ := doubleQuotedText(command, index+1)
				appendString(text, expanded)
			} else {
				appendSlice(index+1, end, expanded, false)
			}
			index = end + 1
			continue
		case char == '\\':
			if next != '\n' {
				if hasNext {
					_, size := utf8.DecodeRuneInString(command[index+1:])
					appendSlice(index+1, index+1+size, false, false)
					index += 1 + size
					continue
				}
				appendSlice(index+1, index+1, false, false)
			}
			index += 2
			continue
		case char == '#' && word == nil:
			for index < len(command) && command[index] != '\n' {
				index++
			}
			continue
		case char == '\n':
			endSegment(false)
			index++
			for _, heredoc := range pending {
				bodyStart, bodyEnd, lines := 0, 0, 0
				var stripped strings.Builder
				for index < len(command) {
					lineEnd := strings.IndexByte(command[index:], '\n')
					stop := len(command)
					if lineEnd >= 0 {
						stop = index + lineEnd
					}
					start := index
					line := command[start:stop]
					if heredoc.op.tabs {
						line = strings.TrimLeft(line, "\t")
					}
					index = stop + 1
					if line == heredoc.op.delimiter {
						break
					}
					if lines == 0 {
						bodyStart = start
					}
					lines++
					bodyEnd = stop
					if heredoc.op.tabs {
						stripped.WriteString(line)
						stripped.WriteByte('\n')
					}
				}
				// The body is each line and a newline: a slice of the command unless the tabs were stripped or the
				// last line has no newline after it.
				body := ""
				switch {
				case lines == 0:
				case heredoc.op.tabs:
					body = stripped.String()
				case bodyEnd < len(command):
					body = command[bodyStart : bodyEnd+1]
				default:
					body = command[bodyStart:bodyEnd] + "\n"
				}
				text, expanded := heredocText(body, heredoc.op.literal)
				heredoc.segment.input = &scanInput{kind: "heredoc", text: text, expanded: expanded}
			}
			pending = nil
			continue
		case char == '$' && next == '\'':
			stop := len(command)
			if end := strings.IndexByte(command[index+2:], '\''); end >= 0 {
				stop = index + 2 + end
			}
			from := index + 2
			if from > stop {
				from = stop
			}
			appendSlice(from, stop, true, false)
			index = stop + 1
			continue
		case char == '$' && next == '(':
			closeAt := closeParen(command, index+1)
			appendSlice(index, closeAt, true, false)
			index = closeAt
			continue
		case char == '$' && hasNext && isExpansionStart(next):
			appendSlice(index, index+1, true, false)
			index++
			continue
		case char == '`':
			stop := len(command)
			if end := strings.IndexByte(command[index+1:], '`'); end >= 0 {
				stop = index + 1 + end
			}
			upto := stop + 1
			if upto > len(command) {
				upto = len(command)
			}
			appendSlice(index, upto, true, false)
			index = stop + 1
			continue
		case (char == '<' || char == '>') && next == '(':
			endWord()
			segment.substitution = true
			index = closeParen(command, index+1)
			continue
		case char == '<':
			endWord()
			if strings.HasPrefix(command[index:], "<<<") {
				hereString = true
				index += 3
				continue
			}
			if strings.HasPrefix(command[index:], "<<") {
				if heredoc, ok := heredocOperator(command, index); ok {
					pending = append(pending, pendingHeredoc{segment: segment, op: heredoc})
					index = heredoc.end
					continue
				}
			}
			inputFile = true
			index++
			continue
		case char == '>' || (char == '&' && next == '>'):
			fd := "1"
			if char == '&' {
				fd = "&"
				index++
			} else if word != nil && !word.expanded && fdWord() {
				fd = word.text
				word = nil
			}
			endWord()
			index++
			appending := byteAt(command, index) == '>'
			if appending {
				index++
			}
			if byteAt(command, index) == '|' {
				index++
			}
			if byteAt(command, index) == '&' {
				index++
				// `>&2` and `2>&1` duplicate a descriptor and name no file; `>&file` is `&>file`.
				if dup := reDupFd.FindString(command[min(index, len(command)):]); dup != "" {
					index += len(dup)
					continue
				}
				fd = "&"
			}
			redirect = &scanRedirect{fd: fd, appending: appending}
			continue
		case char == '(':
			endSegment(false)
			groups = append(groups, len(segments))
			index++
			continue
		case char == ')':
			endSegment(false)
			if start, ok := popGroup(); ok {
				segment.group = &scanGroup{start: start, end: len(segments)}
			}
			index++
			continue
		case char == ';' || char == '&' || char == '|':
			double := next == char
			piped := char == '|' && !double
			endSegment(piped)
			if double || (piped && next == '&') {
				index += 2
			} else {
				index++
			}
			continue
		}
		r, size := utf8.DecodeRuneInString(command[index:])
		if isJSSpace(r) {
			endWord()
			index += size
			continue
		}
		appendSlice(index, index+size, false, word == nil && char == '~')
		index += size
	}
	endSegment(false)
	return segments
}

var shellWrappers = map[string]bool{"command": true, "builtin": true, "exec": true, "env": true}

// envValueFlags are the `env` options that take the next word as their value.
var envValueFlags = map[string]bool{"-u": true, "--unset": true, "-C": true, "--chdir": true, "-S": true, "--split-string": true}

func headIndex(words []*scanWord) int {
	index := 0
	for index < len(words) {
		text := words[index].text
		if !reEnvAssign.MatchString(text) && !shellWrappers[text] {
			break
		}
		index++
		if text != "env" {
			continue
		}
		for index < len(words) && strings.HasPrefix(words[index].text, "-") {
			if envValueFlags[words[index].text] {
				index += 2
			} else {
				index++
			}
		}
	}
	return index
}

var shellEscapes = map[byte]string{'n': "\n", 't': "\t", 'r': "\r", 'a': "\x07", 'b': "\b", 'f': "\f", 'v': "\v", 'e': "\x1b", '\\': "\\", '"': "\"", '\'': "'"}

// unescapeShell reads backslash escapes as `echo -e`, `printf` formats, and `%b` read them; an unknown escape stays as
// written.
func unescapeShell(text string) (out string, stop bool) {
	var b strings.Builder
	for index := 0; index < len(text); index++ {
		char := text[index]
		if char != '\\' || index+1 >= len(text) {
			b.WriteByte(char)
			continue
		}
		next := text[index+1]
		if next == 'c' {
			return b.String(), true
		}
		if octal := reOctalEscape.FindString(text[index+1:]); octal != "" {
			code, _ := strconv.ParseInt(octal, 8, 32)
			b.WriteRune(rune(code))
			index += len(octal)
			continue
		}
		if replacement, ok := shellEscapes[next]; ok {
			b.WriteString(replacement)
		} else {
			b.WriteString("\\" + string(next))
		}
		index++
	}
	return b.String(), false
}

func echoText(args []string) string {
	index := 0
	newline := true
	escapes := false
	for ; index < len(args) && reEchoFlags.MatchString(args[index]); index++ {
		for _, flag := range args[index][1:] {
			if flag == 'n' {
				newline = false
			} else {
				escapes = flag == 'e'
			}
		}
	}
	joined := strings.Join(args[index:], " ")
	if !escapes {
		if newline {
			return joined + "\n"
		}
		return joined
	}
	text, stop := unescapeShell(joined)
	if newline && !stop {
		return text + "\n"
	}
	return text
}

// printfText is the output of `printf` for the conversions `%s`, `%b`, `%d`, `%i`, and `%%`; false for any other format.
func printfText(args []string) (string, bool) {
	rest := args
	if len(rest) > 0 && rest[0] == "--" {
		rest = rest[1:]
	}
	if len(rest) == 0 {
		return "", false
	}
	format := rest[0]
	values := rest[1:]
	var out strings.Builder
	used := 0
	for {
		consumed := 0
		for index := 0; index < len(format); index++ {
			char := format[index]
			if char == '\\' {
				end := index + 2
				if byteAt(format, index+1) == '0' {
					end = index + 2 + len(leadingOctalDigits(format[min(index+2, len(format)):]))
				}
				text, stop := unescapeShell(format[index:min(end, len(format))])
				out.WriteString(text)
				if stop {
					return out.String(), true
				}
				index = end - 1
				continue
			}
			if char != '%' {
				out.WriteByte(char)
				continue
			}
			index++
			if index >= len(format) {
				return "", false
			}
			conversion := format[index]
			if conversion == '%' {
				out.WriteByte('%')
				continue
			}
			value := ""
			if used+consumed < len(values) {
				value = values[used+consumed]
			}
			switch conversion {
			case 's':
				out.WriteString(value)
			case 'b':
				text, stop := unescapeShell(value)
				out.WriteString(text)
				if stop {
					return out.String(), true
				}
			case 'd', 'i':
				if value != "" && !reSignedInt.MatchString(value) {
					return "", false
				}
				if value == "" {
					out.WriteString("0")
				} else {
					f, _ := strconv.ParseFloat(value, 64)
					out.WriteString(jsNumberString(f))
				}
			default:
				return "", false
			}
			consumed++
		}
		if consumed == 0 {
			break
		}
		used += consumed
		if used >= len(values) {
			break
		}
	}
	return out.String(), true
}

// leadingOctalDigits is `/^[0-7]{0,3}/` on text.
func leadingOctalDigits(text string) string {
	n := 0
	for n < len(text) && n < 3 && text[n] >= '0' && text[n] <= '7' {
		n++
	}
	return text[:n]
}

// expandShellHome expands a leading `~` of a word; false when it cannot be expanded.
func expandShellHome(w *scanWord, home string, hasHome bool) (string, bool) {
	if !w.tilde {
		return w.text, true
	}
	if w.text != "~" && !strings.HasPrefix(w.text, "~/") {
		return "", false
	}
	if !hasHome {
		return "", false
	}
	return home + w.text[1:], true
}

type shellSource struct {
	content string
	via     string
	// reason is set when the text cannot be read.
	reason string
}

func wordTexts(words []*scanWord) []string {
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = w.text
	}
	return out
}

// contentOf is where the written text comes from, or why an authoring form's text cannot be read. nil when the command
// only sends a program's output or a file's content to the target: that is no authoring form, so it leaves no trace note.
func contentOf(segment *scanSegment, name string, args []*scanWord) *shellSource {
	if name == "echo" || name == "printf" {
		anyExpanded := false
		for _, a := range args {
			anyExpanded = anyExpanded || a.expanded
		}
		if segment.substitution || anyExpanded {
			return &shellSource{reason: shellExpansionReason}
		}
		if name == "echo" {
			return &shellSource{content: echoText(wordTexts(args)), via: "echo"}
		}
		if len(args) > 0 && args[0].text == "-v" {
			return nil
		}
		content, ok := printfText(wordTexts(args))
		if !ok {
			return &shellSource{reason: "the printf format uses a conversion that is not judged"}
		}
		return &shellSource{content: content, via: "printf"}
	}
	if name != "cat" && name != "tee" {
		return nil
	}
	if name == "cat" {
		for _, a := range args {
			if a.text != "-" && !strings.HasPrefix(a.text, "-") {
				return nil
			}
		}
	}
	if segment.input != nil {
		if segment.input.expanded {
			return &shellSource{reason: shellExpansionReason}
		}
		return &shellSource{content: segment.input.text, via: segment.input.kind}
	}
	if name == "tee" && segment.stdin == "pipe" {
		return &shellSource{reason: "the content arrives through a pipe"}
	}
	return nil
}

type shellTarget struct {
	word      *scanWord
	appending bool
}

func stdoutTargets(segment *scanSegment) []shellTarget {
	var out []shellTarget
	for _, item := range segment.redirects {
		if item.fd == "1" || item.fd == "&" {
			out = append(out, shellTarget{word: item.target, appending: item.appending})
		}
	}
	return out
}

func commandNameOf(segment *scanSegment, head int) string {
	if head >= len(segment.words) {
		return ""
	}
	return reDirPrefix.ReplaceAllString(segment.words[head].text, "")
}

// groupSource is the text a `{ ...; }` or `( ... )` group prints, in order. nil when no command in it is an authoring
// form: a group of programs is output, not authoring. A command with its own stdout redirect prints nothing into the
// group.
func groupSource(segments []*scanSegment, group scanGroup) *shellSource {
	var content strings.Builder
	via := ""
	program := false
	for _, inner := range segments[group.start:group.end] {
		if inner.group != nil {
			return &shellSource{reason: "the command group holds another group, which is not judged"}
		}
		if len(stdoutTargets(inner)) > 0 {
			continue
		}
		head := headIndex(inner.words)
		name := commandNameOf(inner, head)
		switch name {
		case "", ":", "true", "cd", "pushd", "popd":
			continue
		}
		var args []*scanWord
		if name != "tee" && head+1 <= len(inner.words) {
			args = inner.words[head+1:]
		}
		source := contentOf(inner, name, args)
		if source == nil {
			program = true
			continue
		}
		if source.reason != "" {
			return source
		}
		content.WriteString(source.content)
		if via == "" {
			via = source.via
		}
	}
	if via == "" {
		return nil
	}
	if program {
		return &shellSource{reason: "the command group also runs a program whose output is not in the command text"}
	}
	return &shellSource{content: content.String(), via: via}
}

// posixNormalize is path.posix.normalize: Clean, but a trailing slash stays.
func posixNormalize(p string) string {
	if p == "" {
		return "."
	}
	cleaned := path.Clean(p)
	if strings.HasSuffix(p, "/") && !strings.HasSuffix(cleaned, "/") {
		cleaned += "/"
	}
	return cleaned
}

// ShellWritesOptions: Home expands a leading `~` in a target; without it such a target is skipped.
type ShellWritesOptions struct {
	Home    string
	HasHome bool
}

// shellWrites are the writes whose content is literal in command, and the writes skipped with a reason. Pure: the
// command is only read.
func shellWrites(command string, options ShellWritesOptions) ShellWriteScan {
	var scan ShellWriteScan
	movedDirectory := false
	segments := scanShell(command)
	// grouped: segments whose output a group redirect takes, so `exec > file` does not also claim it.
	grouped := map[int]bool{}
	for _, segment := range segments {
		if segment.group != nil && len(stdoutTargets(segment)) > 0 {
			for i := segment.group.start; i < segment.group.end; i++ {
				grouped[i] = true
			}
		}
	}
	// execTargets: after `exec > file`, every later command without its own stdout redirect writes to the file.
	var execTargets []shellTarget
	emit := func(targets []shellTarget, read func() *shellSource) {
		// `/dev/../tmp/f` is a file under /tmp, not a device.
		var files []shellTarget
		for _, target := range targets {
			if !reDevPath.MatchString(posixNormalize(target.word.text)) {
				files = append(files, target)
			}
		}
		if len(files) == 0 {
			return
		}
		source := read()
		if source == nil {
			return
		}
		for _, target := range files {
			p, ok := "", false
			if !target.word.expanded {
				p, ok = expandShellHome(target.word, options.Home, options.HasHome)
			}
			if !ok || p == "" {
				scan.Skips = append(scan.Skips, ShellSkip{Path: target.word.text, Reason: "the target path uses shell expansion"})
				continue
			}
			if movedDirectory && !isAbsShellPath(p) {
				scan.Skips = append(scan.Skips, ShellSkip{Path: p, Reason: "the command changes directory first, so the relative target is not resolved"})
				continue
			}
			if source.reason != "" {
				scan.Skips = append(scan.Skips, ShellSkip{Path: p, Reason: source.reason})
				continue
			}
			scan.Writes = append(scan.Writes, ShellWrite{Path: p, Content: source.content, Append: target.appending, Via: source.via})
		}
	}
	for position, segment := range segments {
		head := headIndex(segment.words)
		name := commandNameOf(segment, head)
		var args []*scanWord
		if head+1 <= len(segment.words) {
			args = segment.words[head+1:]
		}
		if name == "cd" || name == "pushd" || name == "popd" {
			movedDirectory = true
			continue
		}
		own := stdoutTargets(segment)
		if segment.group != nil {
			if len(own) > 0 {
				group := *segment.group
				emit(own, func() *shellSource { return groupSource(segments, group) })
			}
			continue
		}
		if name == "" {
			isExec := false
			for _, w := range segment.words[:min(head, len(segment.words))] {
				if w.text == "exec" {
					isExec = true
				}
			}
			if isExec {
				if len(own) > 0 {
					execTargets = own
				}
				continue
			}
		}
		// `> file` and `: > file` truncate: an empty file has nothing to judge.
		if name == "" || name == ":" || name == "true" {
			continue
		}
		if name == "sed" {
			inPlace := false
			for _, a := range args {
				if reInPlaceFlag.MatchString(a.text) || strings.HasPrefix(a.text, "--in-place") {
					inPlace = true
				}
			}
			if inPlace {
				scan.Skips = append(scan.Skips, ShellSkip{Reason: "sed -i edits a file in place; the new content is not in the command text"})
				continue
			}
		}
		if name == "patch" {
			scan.Skips = append(scan.Skips, ShellSkip{Reason: "patch applies a diff; the resulting file is not in the command text"})
			continue
		}
		if name == "git" {
			first := ""
			for _, a := range args {
				if !strings.HasPrefix(a.text, "-") {
					first = a.text
					break
				}
			}
			if first == "apply" {
				scan.Skips = append(scan.Skips, ShellSkip{Reason: "git apply applies a diff; the resulting file is not in the command text"})
				continue
			}
		}
		targets := append([]shellTarget(nil), own...)
		if len(own) == 0 && !grouped[position] && len(execTargets) > 0 {
			targets = append(targets, execTargets...)
			// The file stays open: whatever this command prints, the next one appends after it.
			next := make([]shellTarget, len(execTargets))
			for i, t := range execTargets {
				next[i] = shellTarget{word: t.word, appending: true}
			}
			execTargets = next
		}
		if name == "tee" {
			appending := false
			for _, a := range args {
				if reAppendFlag.MatchString(a.text) || a.text == "--append" {
					appending = true
				}
			}
			for _, a := range args {
				if !strings.HasPrefix(a.text, "-") || a.text == "-" {
					targets = append(targets, shellTarget{word: a, appending: appending})
				}
			}
		}
		contentArgs := args
		if name == "tee" {
			contentArgs = nil
		}
		emit(targets, func() *shellSource { return contentOf(segment, name, contentArgs) })
	}
	return scan
}

// isAbsShellPath is node:path isAbsolute on the platform's rules.
func isAbsShellPath(p string) bool { return filepath.IsAbs(p) || strings.HasPrefix(p, "/") }

// mergeWrites keeps one write per path, in first-seen order: appends to a path join their text, and a later truncating
// write replaces what came before it. Forty `>>` lines to one file are one file to judge.
func mergeWrites(writes []ShellWrite) []ShellWrite {
	byPath := map[string]*ShellWrite{}
	var order []string
	for _, w := range writes {
		earlier, ok := byPath[w.Path]
		if !ok {
			order = append(order, w.Path)
		}
		if ok && w.Append {
			merged := *earlier
			merged.Content += w.Content
			byPath[w.Path] = &merged
		} else {
			copied := w
			byPath[w.Path] = &copied
		}
	}
	out := make([]ShellWrite, len(order))
	for i, p := range order {
		out[i] = *byPath[p]
	}
	return out
}
