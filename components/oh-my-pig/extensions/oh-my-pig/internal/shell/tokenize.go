package shell

import (
	"strings"
)

// upstream: packages/coding-agent/src/tools/shell-tokenize.ts

// extractLeadingCdTarget parses a leading `cd <path> && ...` or `cd <path>; ...`
// prefix so the bash tool can route the target through its structured `cwd`
// parameter when the model omits it. Bails on redirects, extra arguments,
// directory-stack operands (`-`, `+1`, `-1`), named-user tildes (`~other`), or
// shell expansions (`$`, backticks, `(`, globs), leaving the whole command for
// the shell.
// upstream: shell-tokenize.ts:846
func extractLeadingCdTarget(command string) (path string, rest string, separator string, ok bool) {
	if !strings.HasPrefix(command, "cd") || len(command) < 3 {
		return "", "", "", false
	}
	if command[2] != ' ' && command[2] != '\t' {
		return "", "", "", false
	}
	i := 2
	for i < len(command) && (command[i] == ' ' || command[i] == '\t') {
		i++
	}
	hasUnquotedLeadingTilde := i < len(command) && command[i] == '~'
	var b strings.Builder
	inSingle := false
	inDouble := false
	for ; i < len(command); i++ {
		ch := command[i]
		if inSingle {
			if ch == '\'' {
				inSingle = false
				continue
			}
			b.WriteByte(ch)
			continue
		}
		if inDouble {
			if ch == '\\' && i+1 < len(command) {
				next := command[i+1]
				if next == '\n' || next == '\r' {
					return "", "", "", false
				}
				if next == '"' || next == '\\' || next == '$' || next == '`' {
					b.WriteByte(next)
					i++
					continue
				}
			}
			if ch == '"' {
				inDouble = false
				continue
			}
			if ch == '$' || ch == '`' {
				return "", "", "", false
			}
			b.WriteByte(ch)
			continue
		}
		if ch == '\'' {
			inSingle = true
			continue
		}
		if ch == '"' {
			inDouble = true
			continue
		}
		if ch == '\\' && i+1 < len(command) {
			next := command[i+1]
			if next == '\n' || next == '\r' {
				return "", "", "", false
			}
			b.WriteByte(next)
			i++
			continue
		}
		if strings.ContainsRune("$`()*?[]{}", rune(ch)) {
			return "", "", "", false
		}
		if isCdTargetTerminator(ch) {
			break
		}
		b.WriteByte(ch)
	}
	path = b.String()
	if inSingle || inDouble || len(path) == 0 {
		return "", "", "", false
	}
	// Directory-stack operands depend on shell state.
	if path == "-" || isDirStackOperand(path) {
		return "", "", "", false
	}
	// Named-user tilde prefixes (~other) stay with the shell.
	if hasUnquotedLeadingTilde && path != "~" && !strings.HasPrefix(path, "~/") {
		return "", "", "", false
	}
	for i < len(command) && (command[i] == ' ' || command[i] == '\t') {
		i++
	}
	if i+1 < len(command) && command[i] == '&' && command[i+1] == '&' {
		separator = "&&"
	} else if i < len(command) && command[i] == ';' && (i+1 >= len(command) || (command[i+1] != ';' && command[i+1] != '&')) {
		separator = ";"
	} else {
		return "", "", "", false
	}
	i += len(separator)
	for i < len(command) && (command[i] == ' ' || command[i] == '\t') {
		i++
	}
	return path, command[i:], separator, true
}

func isDirStackOperand(s string) bool {
	if len(s) < 2 || (s[0] != '+' && s[0] != '-') {
		return false
	}
	for i := 1; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func isCdTargetTerminator(ch byte) bool {
	switch ch {
	case ' ', '\t', '\n', '\r', '&', '|', ';', '<', '>', '(', ')':
		return true
	default:
		return false
	}
}

// flatShellCommandSegment is one flat command segment extracted from a command line.
type flatShellCommandSegment struct {
	text       string
	pipedStdin bool
}

// extractFlatShellCommandSegments splits a command line on top-level command
// separators while ignoring quoted/escaped/commented text and bailing out on
// nested shell constructs (subshells, command substitutions, heredocs, brace groups).
// upstream: shell-tokenize.ts:395
func extractFlatShellCommandSegments(command string) []flatShellCommandSegment {
	var segments []flatShellCommandSegment
	segmentStart := 0
	inSingle := false
	inDouble := false
	atWordStart := true
	currentPiped := false

	pushSegment := func(end int) bool {
		text := strings.TrimSpace(command[segmentStart:end])
		if text == "" {
			return false
		}
		segments = append(segments, flatShellCommandSegment{
			text:       text,
			pipedStdin: currentPiped,
		})
		return true
	}

	for i := 0; i < len(command); i++ {
		ch := command[i]
		if inSingle {
			if ch == '\'' {
				inSingle = false
			}
			atWordStart = false
			continue
		}
		if inDouble {
			if ch == '\\' {
				if i+1 >= len(command) {
					return nil
				}
				i++
				atWordStart = false
				continue
			}
			if ch == '`' || (ch == '$' && i+1 < len(command) && (command[i+1] == '(' || command[i+1] == '{')) {
				return nil
			}
			if ch == '"' {
				inDouble = false
			}
			atWordStart = false
			continue
		}
		if ch == '\\' {
			if i+1 >= len(command) {
				return nil
			}
			i++
			atWordStart = false
			continue
		}
		if ch == '\'' {
			inSingle = true
			atWordStart = false
			continue
		}
		if ch == '"' {
			inDouble = true
			atWordStart = false
			continue
		}
		if ch == '`' || ch == '(' || ch == ')' ||
			(ch == '$' && i+1 < len(command) && (command[i+1] == '(' || command[i+1] == '{')) ||
			(ch == '<' && i+1 < len(command) && command[i+1] == '<') ||
			((ch == '{' || ch == '}') && atWordStart && (i+1 >= len(command) || isBraceBoundary(command[i+1]))) {
			return nil
		}
		if ch == '#' && atWordStart {
			pushed := pushSegment(i)
			nl := strings.IndexByte(command[i+1:], '\n')
			if nl < 0 {
				return segments
			}
			i = i + 1 + nl
			segmentStart = i + 1
			atWordStart = true
			if pushed {
				currentPiped = false
			}
			continue
		}
		isRedir := false
		if ch == '|' {
			isRedir = i > 0 && command[i-1] == '>'
		} else if ch == '&' {
			isRedir = (i > 0 && (command[i-1] == '>' || command[i-1] == '<')) || (i+1 < len(command) && command[i+1] == '>')
		}
		if (ch == '\n' || ch == ';' || ch == '|' || ch == '&') && !isRedir {
			pushed := pushSegment(i)
			doubled := (ch == '|' || ch == '&') && i+1 < len(command) && command[i+1] == ch
			pipeStderr := ch == '|' && i+1 < len(command) && command[i+1] == '&'
			if doubled || pipeStderr {
				i++
			}
			if pushed || ch != '\n' {
				currentPiped = ch == '|' && !doubled
			}
			segmentStart = i + 1
			atWordStart = true
			continue
		}
		atWordStart = ch == ' ' || ch == '\t'
	}
	if inSingle || inDouble {
		return nil
	}
	pushSegment(len(command))
	return segments
}

func isBraceBoundary(ch byte) bool {
	return ch == ' ' || ch == '\t' || ch == '\n' || ch == ';'
}
