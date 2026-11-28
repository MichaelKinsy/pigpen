package terminal

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type commandRule struct {
	re          *regexp.Regexp
	description string
	hardline    bool
}

// dangerousCommandRules mirrors Hermes's hardline + dangerous-command detection tables
// in tools/approval_detection.py.
var dangerousCommandRules = []commandRule{
	// Hardline (unconditional blocks)
	{
		re:          regexp.MustCompile(`(?i)(?:^|[;&|\n]\s*)(?:sudo\s+)?rm\s+-[a-z]*r[a-z]*f[a-z]*\s+(?:/\s*$|/\*\s*$|/\s+[;&|]|~/?\s*$|"\$HOME"\s*$|\$HOME\s*$)`),
		description: "recursive forced deletion of root or home directory",
		hardline:    true,
	},
	{
		re:          regexp.MustCompile(`(?i)(?:^|[;&|\n]\s*)(?:sudo\s+)?rm\s+-[a-z]*f[a-z]*r[a-z]*\s+(?:/\s*$|/\*\s*$|/\s+[;&|]|~/?\s*$|"\$HOME"\s*$|\$HOME\s*$)`),
		description: "recursive forced deletion of root or home directory",
		hardline:    true,
	},
	{
		re:          regexp.MustCompile(`(?i)(?:^|[;&|\n]\s*)(?:sudo\s+)?mkfs(?:\.[a-z0-9]+)?\b`),
		description: "format filesystem with mkfs",
		hardline:    true,
	},
	{
		re:          regexp.MustCompile(`(?i)(?:^|[;&|\n]\s*)(?:sudo\s+)?dd\b.*\bof=/dev/(?:sd|nvme|hd|vd|disk)`),
		description: "raw block device overwrite with dd",
		hardline:    true,
	},
	{
		re:          regexp.MustCompile(`:\(\)\s*\{\s*:\s*\|\s*:\s*&\s*\}\s*;\s*:`),
		description: "fork bomb",
		hardline:    true,
	},
	{
		re:          regexp.MustCompile(`(?i)(?:^|[;&|\n]\s*)(?:sudo\s+)?(?:shutdown|reboot|poweroff|halt)\b`),
		description: "host shutdown or reboot",
		hardline:    true,
	},

	// Dangerous (require confirmation when !Yolo)
	{
		re:          regexp.MustCompile(`(?i)\brm\s+-[a-z]*r[a-z]*f|\brm\s+-[a-z]*f[a-z]*r`),
		description: "recursive forced delete (rm -rf)",
	},
	{
		re:          regexp.MustCompile(`(?i)\bchmod\s+-R\s+777\b`),
		description: "recursive world-writable permissions (chmod -R 777)",
	},
	{
		re:          regexp.MustCompile(`(?i)\b(?:curl|wget)\b[^|]*\|\s*(?:sudo\s+)?(?:sh|bash|zsh|python3?)\b`),
		description: "download and pipe directly to shell/interpreter",
	},
	{
		re:          regexp.MustCompile(`(?i)\bgit\s+push\b.*(?:--force|-f)\b`),
		description: "git force push",
	},
	{
		re:          regexp.MustCompile(`(?i)\bgit\s+reset\s+--hard\b`),
		description: "git reset --hard (discards uncommitted work)",
	},
	{
		re:          regexp.MustCompile(`(?i)\bgit\s+clean\s+-[a-z]*f`),
		description: "git clean -f (deletes untracked files)",
	},
	{
		re:          regexp.MustCompile(`(?i)\b(?:drop\s+(?:database|table|schema)|truncate\s+table)\b`),
		description: "destructive SQL statement",
	},
	{
		re:          regexp.MustCompile(`(?i)(?:>|>>|\btee\b)\s*(?:/etc/|/private/etc/|~/.ssh/|~/\.bashrc|~/\.zshrc)`),
		description: "overwrite sensitive system or shell config file",
	},
	{
		re:          regexp.MustCompile(`(?i)\bsudo\b`),
		description: "privileged command execution via sudo",
	},
}

// checkDangerousCommand classifies a shell command.
// Returns (reason, hardline). Empty reason means the command is not flagged.
func checkDangerousCommand(command string) (string, bool) {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return "", false
	}
	for _, rule := range dangerousCommandRules {
		if rule.re.MatchString(trimmed) {
			return rule.description, rule.hardline
		}
	}
	return "", false
}

// hasCompoundShellOperators reports whether command contains unquoted shell
// chaining, redirection, or subshell operators (;, &, |, <, >, `, $().
// Matches upstream tools/approval_floors.py so compound commands cannot bypass
// dangerous-command checks via command_allowlist.
func hasCompoundShellOperators(command string) bool {
	var inSingle, inDouble, escaped bool
	for i := range len(command) {
		ch := command[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && !inSingle {
			escaped = true
			continue
		}
		if ch == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if ch == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if inSingle {
			continue
		}
		// Backtick and $(...) expand even inside double quotes in POSIX shells.
		if ch == '`' || (ch == '$' && i+1 < len(command) && command[i+1] == '(') {
			return true
		}
		if inDouble {
			continue
		}
		switch ch {
		case ';', '&', '|', '<', '>', '\n', '\r':
			return true
		}
	}
	return false
}

// normalizeApprovalPattern returns a stable key for session/permanent allowlist storage.
func normalizeApprovalPattern(command string) string {
	return strings.TrimSpace(command)
}

// escapeAllowlistGlob escapes glob metacharacters so modal approvals with
// *, ?, or [ are persisted as literal matches rather than wildcards.
func escapeAllowlistGlob(pattern string) string {
	if !strings.ContainsAny(pattern, "*?[") {
		return pattern
	}
	var b strings.Builder
	b.Grow(len(pattern) + 4)
	for i := range len(pattern) {
		switch pattern[i] {
		case '\\', '*', '?', '[':
			b.WriteByte('\\')
		}
		b.WriteByte(pattern[i])
	}
	return b.String()
}

// matchAllowlistGlob matches text against pattern supporting *, ?, [...], and
// backslash escapes (\*, \?, \[, \\).
func matchAllowlistGlob(pattern, text string) bool {
	pi, ti := 0, 0
	starPi, starTi := -1, -1
	for ti < len(text) {
		if pi < len(pattern) {
			switch pattern[pi] {
			case '\\':
				if pi+1 < len(pattern) && pattern[pi+1] == text[ti] {
					pi += 2
					ti++
					continue
				}
			case '*':
				starPi = pi
				starTi = ti
				pi++
				continue
			case '?':
				pi++
				ti++
				continue
			case '[':
				if end, matched := matchCharClass(pattern[pi:], text[ti]); end > 0 {
					if matched {
						pi += end
						ti++
						continue
					}
				} else if pattern[pi] == text[ti] {
					pi++
					ti++
					continue
				}
			default:
				if pattern[pi] == text[ti] {
					pi++
					ti++
					continue
				}
			}
		}
		if starPi >= 0 {
			pi = starPi + 1
			starTi++
			ti = starTi
			continue
		}
		return false
	}
	for pi < len(pattern) && pattern[pi] == '*' {
		pi++
	}
	return pi == len(pattern)
}

// matchCharClass matches a bracket expression at the start of pat against ch.
// Returns (consumedLen, matched); consumedLen == 0 if no closing ']'.
func matchCharClass(pat string, ch byte) (int, bool) {
	if len(pat) < 2 || pat[0] != '[' {
		return 0, false
	}
	i := 1
	neg := false
	if i < len(pat) && (pat[i] == '!' || pat[i] == '^') {
		neg = true
		i++
	}
	matched := false
	first := true
	for i < len(pat) {
		if pat[i] == ']' && !first {
			if neg {
				return i + 1, !matched
			}
			return i + 1, matched
		}
		first = false
		lo := pat[i]
		if lo == '\\' && i+1 < len(pat) {
			i++
			lo = pat[i]
		}
		if i+2 < len(pat) && pat[i+1] == '-' && pat[i+2] != ']' {
			hi := pat[i+2]
			i += 3
			if ch >= lo && ch <= hi {
				matched = true
			}
			continue
		}
		if ch == lo {
			matched = true
		}
		i++
	}
	return 0, false
}

// matchesAllowlistEntry reports whether command matches a single disk command_allowlist entry.
// Compound commands are never eligible to match the allowlist.
// Exact normalized equality always matches; globs (*, ?, [...]) are supported for hand-authored
// entries while escaped metacharacters (\*, \?, \[) match literally.
func matchesAllowlistEntry(command, pattern string) bool {
	cmd := strings.TrimSpace(command)
	pat := strings.TrimSpace(pattern)
	if cmd == "" || pat == "" || hasCompoundShellOperators(cmd) {
		return false
	}
	if cmd == pat {
		return true
	}
	if strings.ContainsAny(pat, "*?[") {
		return matchAllowlistGlob(pat, cmd)
	}
	return false
}

// loadCommandAllowlist reads command_allowlist entries live from configFile.
func loadCommandAllowlist(configFile string) []string {
	if strings.TrimSpace(configFile) == "" {
		return nil
	}
	raw, err := os.ReadFile(configFile)
	if err != nil {
		return nil
	}
	return parseCommandAllowlistYAML(string(raw))
}

func parseCommandAllowlistYAML(content string) []string {
	var out []string
	inList := false
	listIndent := -1
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if inList {
			if indent >= listIndent && strings.HasPrefix(trimmed, "- ") {
				item := unquoteAllowlistItem(strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))
				if item != "" {
					out = append(out, item)
				}
				continue
			}
			if indent <= listIndent && !strings.HasPrefix(trimmed, "- ") {
				inList = false
			}
		}
		if indent == 0 && strings.HasPrefix(trimmed, "command_allowlist:") {
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "command_allowlist:"))
			if rest == "" {
				inList = true
				listIndent = indent
				continue
			}
			if strings.HasPrefix(rest, "[") && strings.HasSuffix(rest, "]") {
				inner := strings.TrimSpace(rest[1 : len(rest)-1])
				if inner != "" {
					for _, part := range strings.Split(inner, ",") {
						if item := unquoteAllowlistItem(strings.TrimSpace(part)); item != "" {
							out = append(out, item)
						}
					}
				}
			}
		}
	}
	return out
}

func unquoteAllowlistItem(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		inner := s[1 : len(s)-1]
		var b strings.Builder
		b.Grow(len(inner))
		for i := 0; i < len(inner); i++ {
			if inner[i] == '\\' && i+1 < len(inner) {
				switch inner[i+1] {
				case '\\':
					b.WriteByte('\\')
					i++
					continue
				case '"':
					b.WriteByte('"')
					i++
					continue
				case 'n':
					b.WriteByte('\n')
					i++
					continue
				case 'r':
					b.WriteByte('\r')
					i++
					continue
				case 't':
					b.WriteByte('\t')
					i++
					continue
				}
			}
			b.WriteByte(inner[i])
		}
		return b.String()
	}
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return strings.ReplaceAll(s[1:len(s)-1], "''", "'")
	}
	return s
}

func quoteAllowlistItem(s string) string {
	escaped := strings.ReplaceAll(s, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	escaped = strings.ReplaceAll(escaped, "\n", `\n`)
	escaped = strings.ReplaceAll(escaped, "\r", `\r`)
	escaped = strings.ReplaceAll(escaped, "\t", `\t`)
	return `"` + escaped + `"`
}

// appendCommandAllowlist additively merges pattern into command_allowlist in configFile.
// It re-reads the current disk file first so any hand-edited entries and other YAML
// keys are preserved verbatim.
func appendCommandAllowlist(configFile, pattern string) error {
	pattern = normalizeApprovalPattern(pattern)
	if strings.TrimSpace(configFile) == "" || pattern == "" {
		return nil
	}
	if hasCompoundShellOperators(pattern) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(configFile), 0o755); err != nil {
		return err
	}

	perm := os.FileMode(0o600)
	var raw string
	if info, err := os.Stat(configFile); err == nil && !info.IsDir() {
		perm = info.Mode().Perm()
	}
	if data, err := os.ReadFile(configFile); err == nil {
		raw = string(data)
	} else if !os.IsNotExist(err) {
		return err
	}

	existing := parseCommandAllowlistYAML(raw)
	for _, item := range existing {
		if item == pattern {
			return nil
		}
	}

	var lines []string
	if raw != "" {
		lines = strings.Split(strings.TrimRight(raw, "\n"), "\n")
	}

	// Find existing top-level command_allowlist block.
	blockStart := -1
	blockEnd := -1
	itemIndent := -1
	inlineFlow := false
	for i, l := range lines {
		trimmed := strings.TrimSpace(l)
		indent := len(l) - len(strings.TrimLeft(l, " "))
		if indent == 0 && strings.HasPrefix(trimmed, "command_allowlist:") {
			blockStart = i
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "command_allowlist:"))
			if rest != "" {
				inlineFlow = true
				blockEnd = i + 1
			} else {
				blockEnd = i + 1
				for blockEnd < len(lines) {
					nl := lines[blockEnd]
					nt := strings.TrimSpace(nl)
					if nt == "" || strings.HasPrefix(nt, "#") {
						blockEnd++
						continue
					}
					nIndent := len(nl) - len(strings.TrimLeft(nl, " "))
					if nIndent >= indent && strings.HasPrefix(nt, "- ") {
						if itemIndent < 0 {
							itemIndent = nIndent
						}
						blockEnd++
						continue
					}
					break
				}
			}
			break
		}
	}

	var outLines []string
	if blockStart == -1 {
		outLines = append(outLines, lines...)
		outLines = append(outLines, "command_allowlist:", "  - "+quoteAllowlistItem(pattern))
	} else if inlineFlow {
		outLines = append(outLines, lines[:blockStart]...)
		outLines = append(outLines, "command_allowlist:")
		for _, item := range existing {
			outLines = append(outLines, "  - "+quoteAllowlistItem(item))
		}
		outLines = append(outLines, "  - "+quoteAllowlistItem(pattern))
		outLines = append(outLines, lines[blockEnd:]...)
	} else {
		// Insert right after the last list item in the block (before trailing blank lines if any).
		insertAt := blockEnd
		for insertAt > blockStart+1 && strings.TrimSpace(lines[insertAt-1]) == "" {
			insertAt--
		}
		if itemIndent < 0 {
			itemIndent = 2
		}
		prefix := strings.Repeat(" ", itemIndent) + "- "
		outLines = append(outLines, lines[:insertAt]...)
		outLines = append(outLines, prefix+quoteAllowlistItem(pattern))
		outLines = append(outLines, lines[insertAt:]...)
	}

	out := strings.Join(outLines, "\n") + "\n"
	tmp, err := os.CreateTemp(filepath.Dir(configFile), "config-allowlist-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if _, err := tmp.WriteString(out); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, configFile)
}

// isCommandAllowlisted checks sessionApproved (exact normalized match) first,
// then live disk command_allowlist. Compound commands always return false.
func isCommandAllowlisted(command string, sessionApproved map[string]bool, configFile string) bool {
	normCmd := normalizeApprovalPattern(command)
	if normCmd == "" || hasCompoundShellOperators(normCmd) {
		return false
	}
	if sessionApproved[normCmd] {
		return true
	}
	for _, pat := range loadCommandAllowlist(configFile) {
		if matchesAllowlistEntry(normCmd, pat) {
			return true
		}
	}
	return false
}
