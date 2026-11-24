package shell

import (
	"strings"
)

// upstream: packages/coding-agent/src/tools/shell-tokenize.ts

// extractLeadingCdTarget parses a leading `cd <path> && ...` prefix so the bash
// tool can route the target through its structured `cwd` parameter when the
// model omits it. Bails on redirects, extra arguments, or shell expansions
// (`$`, backticks, `(`), leaving the whole command for the shell.
// upstream: shell-tokenize.ts:527
func extractLeadingCdTarget(command string) (path string, rest string, ok bool) {
	if !strings.HasPrefix(command, "cd") || len(command) < 3 {
		return "", "", false
	}
	if command[2] != ' ' && command[2] != '\t' {
		return "", "", false
	}
	i := 2
	for i < len(command) && (command[i] == ' ' || command[i] == '\t') {
		i++
	}
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
					return "", "", false
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
				return "", "", false
			}
			b.WriteByte(next)
			i++
			continue
		}
		if isCdTargetTerminator(ch) {
			break
		}
		b.WriteByte(ch)
	}
	path = b.String()
	if inSingle || inDouble || len(path) == 0 {
		return "", "", false
	}
	if strings.ContainsAny(path, "$`(") {
		return "", "", false
	}
	for i < len(command) && (command[i] == ' ' || command[i] == '\t') {
		i++
	}
	if i+1 >= len(command) || command[i] != '&' || command[i+1] != '&' {
		return "", "", false
	}
	i += 2
	for i < len(command) && (command[i] == ' ' || command[i] == '\t') {
		i++
	}
	return path, command[i:], true
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

// isPersistentShellCdCommand reports whether command is a standalone `cd` whose
// target can be tracked directly.
// upstream: bash-executor.ts:349
func isPersistentShellCdCommand(command string) bool {
	if strings.ContainsAny(command, "\r\n") {
		return false
	}
	trimmed := strings.TrimSpace(command)
	if trimmed == "cd" {
		return true
	}
	if !strings.HasPrefix(trimmed, "cd") || len(trimmed) < 3 || (trimmed[2] != ' ' && trimmed[2] != '\t') {
		return false
	}
	rest := strings.TrimSpace(trimmed[2:])
	if rest == "" || rest == "--" {
		return true
	}
	hasOptionTerminator := false
	if strings.HasPrefix(rest, "--") && len(rest) >= 3 && (rest[2] == ' ' || rest[2] == '\t') {
		hasOptionTerminator = true
		rest = strings.TrimLeft(rest[2:], " \t")
	}
	if rest == "" {
		return true
	}
	var target string
	quoted := false
	quote := rest[0]
	if quote == '"' || quote == '\'' {
		if len(rest) < 2 || rest[len(rest)-1] != quote {
			return false
		}
		target = rest[1 : len(rest)-1]
		if strings.IndexByte(target, quote) >= 0 {
			return false
		}
		if quote == '"' && strings.ContainsAny(target, "\\$`\r\n") {
			return false
		}
		quoted = true
	} else {
		if hasUnsupportedUnquotedCdSyntax(rest) {
			return false
		}
		target = rest
	}
	if target == "" {
		return false
	}
	if isSignedDigits(target) {
		return false
	}
	if !hasOptionTerminator && strings.HasPrefix(target, "-") && target != "-" {
		return false
	}
	if !quoted && strings.HasPrefix(target, "~") && target != "~" && !strings.HasPrefix(target, "~/") {
		return false
	}
	return true
}

const unsupportedUnquotedCdChars = "\\$`;&|<>(){}*?[]!#\"'"

func hasUnsupportedUnquotedCdSyntax(s string) bool {
	for i := range len(s) {
		ch := s[i]
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' || ch == '\f' || ch == '\v' {
			return true
		}
		if strings.IndexByte(unsupportedUnquotedCdChars, ch) >= 0 {
			return true
		}
	}
	return false
}

func isSignedDigits(s string) bool {
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
