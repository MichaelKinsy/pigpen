package warden

import (
	"strings"
	"unicode/utf8"
)

// scanShellRef and heredocTextRef are scanShell and heredocText as they were before the allocation-free rewrite
// (shell_writes.go): shell_writes_fast_test.go holds the rewrite to them.

var reLeadTabs = lazyRE(`^\t+`)

func heredocTextRef(body string, literal bool) (text string, expanded bool) {
	if literal {
		return body, false
	}
	var out strings.Builder
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

func scanShellRef(command string) []*scanSegment {
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
	endWord := func() {
		if word == nil {
			return
		}
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
	appendText := func(text string, expanded, tilde bool) {
		if word == nil {
			word = &scanWord{tilde: tilde}
		}
		word.text += text
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
			appendText(command[index+1:stop], false, false)
			index = stop + 1
			continue
		case char == '"':
			end := index + 1
			var text strings.Builder
			expanded := false
			for end < len(command) && command[end] != '"' {
				at := command[end]
				after := byteAt(command, end+1)
				hasAfter := end+1 < len(command)
				if at == '\\' && hasAfter && strings.IndexByte("\"\\$`\n", after) >= 0 {
					if after != '\n' {
						text.WriteByte(after)
					}
					end += 2
					continue
				}
				if at == '`' || (at == '$' && hasAfter && isExpansionStart(after)) {
					expanded = true
				}
				if at == '$' && after == '(' {
					closeAt := closeParen(command, end+1)
					text.WriteString(command[end:closeAt])
					end = closeAt
					continue
				}
				text.WriteByte(at)
				end++
			}
			appendText(text.String(), expanded, false)
			index = end + 1
			continue
		case char == '\\':
			if next != '\n' {
				if hasNext {
					_, size := utf8.DecodeRuneInString(command[index+1:])
					appendText(command[index+1:index+1+size], false, false)
					index += 1 + size
					continue
				}
				appendText("", false, false)
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
				var lines []string
				for index < len(command) {
					lineEnd := strings.IndexByte(command[index:], '\n')
					stop := len(command)
					if lineEnd >= 0 {
						stop = index + lineEnd
					}
					raw := command[index:stop]
					line := raw
					if heredoc.op.tabs {
						line = reLeadTabs.ReplaceAllString(raw, "")
					}
					index = stop + 1
					if line == heredoc.op.delimiter {
						break
					}
					lines = append(lines, line)
				}
				var body strings.Builder
				for _, line := range lines {
					body.WriteString(line)
					body.WriteByte('\n')
				}
				text, expanded := heredocTextRef(body.String(), heredoc.op.literal)
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
			appendText(command[from:stop], true, false)
			index = stop + 1
			continue
		case char == '$' && next == '(':
			closeAt := closeParen(command, index+1)
			appendText(command[index:closeAt], true, false)
			index = closeAt
			continue
		case char == '$' && hasNext && isExpansionStart(next):
			appendText("$", true, false)
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
			appendText(command[index:upto], true, false)
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
			} else if word != nil && reFdDigits.MatchString(word.text) && !word.expanded {
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
		appendText(command[index:index+size], false, word == nil && char == '~')
		index += size
	}
	endSegment(false)
	return segments
}
