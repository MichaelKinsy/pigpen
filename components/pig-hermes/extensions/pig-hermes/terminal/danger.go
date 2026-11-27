package terminal

import (
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
