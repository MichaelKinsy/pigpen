package shell

import (
	"fmt"
	"regexp"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/approval"
)

// upstream: packages/coding-agent/src/tools/bash.ts:122-450,834-895,
// packages/coding-agent/src/tools/shell-tokenize.ts:14-109

// Setting retrieves an omp setting value for the active session.
// Security requirement (upstream: tools/bash.ts:846-921, Settings.grantAuthority):
// BOTH "bash.patterns" AND "bash.allowedEnvVars" returned by Setting MUST come
// from operator layers only (`settings.OperatorAny`), so project layers cannot
// loosen operator pattern rules or env allowlists. All other keys (e.g.
// "tools.approvalMode", "tools.approval", "bashInterceptor.enabled",
// "tools.maxTimeout", "async.enabled", "launch.enabled",
// "bash.autoBackground.enabled", "bash.autoBackground.thresholdMs",
// "tools.artifactSpillThreshold", "tools.artifactHeadBytes",
// "tools.outputMaxColumns") come from `settings.Any`.
// Assigned by the integrator; nil uses omp's defaults.
var Setting func(ctx sdk.Context, key string) any

// SessionSetting retrieves the full session-layer view (including project
// layers, i.e. `settings.Any`) for "bash.patterns". Evaluated alongside
// Setting (the operator authority view) in `EvaluateBashToolDecision` so
// project layers can tighten (deny or prompt) operator rules without ever
// loosening operator modes, user policies, or pattern allowlists.
// Assigned by the integrator (`shell.SessionSetting = settings.Any`); when nil,
// only Setting is evaluated.
var SessionSetting func(ctx sdk.Context, key string) any

// BashApprovalPatternRule mirrors omp's BashApprovalPatternRule (exec/settings.ts:19).
type BashApprovalPatternRule struct {
	Match    string   `json:"match"`
	Approval string   `json:"approval"` // "allow", "deny", "prompt"
	Env      []string `json:"env,omitempty"`
}

type approvalVerdict struct {
	Kind   string // "allow", "deny", "prompt", "critical", "exec"
	Reason string
}

// criticalBashPatterns ports omp's CRITICAL_BASH_PATTERNS (tools/bash.ts:205-252)
// in Go RE2 syntax. The `dd of=/dev/...` negative-lookahead rule is checked by
// matchesCriticalDD below.
var criticalBashPatterns = []*regexp.Regexp{
	// Recursive destruction targeting an absolute path.
	regexp.MustCompile(`(?i)\brm\s+(?:-\S+\s+)*(?:-[a-z]*[rRfF][a-z]*|--recursive|--force)\s+(?:-\S+\s+)*/`),
	// --no-preserve-root defeats coreutils' safeguard.
	regexp.MustCompile(`(?i)\brm\s+(?:-\S+\s+)*--no-preserve-root\b`),
	// Any `sudo rm`.
	regexp.MustCompile(`(?i)\bsudo\s+rm\b`),
	// Recursive chmod/chown on /.
	regexp.MustCompile(`(?i)\bchmod\s+-R\s+[0-7]+\s+/`),
	regexp.MustCompile(`\bchmod\s+-R\s+[ugoa+\-=rwxXst,]+\s+/`),
	regexp.MustCompile(`(?i)\bchown\s+-R\s+\S+\s+/`),
	// Fork bomb.
	regexp.MustCompile(`(?i):\(\)\s*\{\s*:\s*\|\s*:`),
	// Disk / filesystem destruction.
	regexp.MustCompile(`(?i)>\s*/dev/sd[a-z]`),
	regexp.MustCompile(`(?i)\bmkfs(?:\.|\b)`),
	regexp.MustCompile(`(?i)\bshred\s+/dev/`),
	regexp.MustCompile(`(?i)\bcryptsetup\b`),
	// System-config destruction.
	regexp.MustCompile(`(?i)>\s*/etc/(?:passwd|shadow|sudoers)\b`),
	regexp.MustCompile(`(?i)\btee\s+(?:-a\s+)?/etc/(?:passwd|shadow|sudoers)\b`),
	// Remote-fetch-then-execute.
	regexp.MustCompile(`(?i)\b(?:curl|wget|fetch)\b[^|]*\|\s*(?:bash|sh|zsh|fish)\b`),
	regexp.MustCompile(`(?i)(?:^|[\s;&|(])(?:bash|sh|zsh|source|\.)\s+<\(\s*(?:curl|wget|fetch)\b`),
	regexp.MustCompile("(?i)\\beval\\s+[\"'`]?\\$\\(\\s*(?:curl|wget|fetch)\\b|\\beval\\s+`\\s*(?:curl|wget|fetch)\\b"),
	// Process/host control.
	regexp.MustCompile(`\bkill\s+-9\s+1\b`),
	regexp.MustCompile(`(?i)(?:^|[\s;&|(])(?:shutdown|poweroff|reboot|halt)(?:\s|$|[;|&])`),
	regexp.MustCompile(`(?i)(?:^|[\s;&|(])init\s+0\b`),
	// Network-shell exfil (`nc -e` / `nc -c`).
	regexp.MustCompile(`(?i)\bnc\b[^|;]*\s-[a-zA-Z]*[ec][a-zA-Z]*\s`),
}

var (
	// ddIfRE and ddOfDevRE port omp's `dd if=... of=/dev/...` critical rule
	// (`tools/bash.ts:227`: `/\bdd\s+if=.+of=\/dev\/(?!(?:null|zero|stdout|stderr|tty|fd\/\d+)(?:[\s;&|)<>]|$))/i`).
	// Matching `if=` followed by any characters before `of=/dev/` matches
	// upstream's `.+` (including `2>&1` redirections like `dd if=x 2>&1 of=/dev/sda`).
	ddIfOfDevRE  = regexp.MustCompile(`(?i)\bdd\s+if=.+?\bof=/dev/([^\s;&|)<>]*)`)
	ddOfDevAllRE = regexp.MustCompile(`(?i)\bof=/dev/([^\s;&|)<>]*)`)
	ddSafeSinkRE = regexp.MustCompile(`(?i)^(?:null|zero|stdout|stderr|tty|fd/\d+)$`)
)

// matchesCriticalDD implements omp's dd rule without RE2-unsupported lookahead:
// matches `dd if=... of=/dev/<target>` unless `<target>` is a harmless pseudo-device
// sink (`null`, `zero`, `stdout`, `stderr`, `tty`, `fd/<n>`).
// upstream: tools/bash.ts:227
func matchesCriticalDD(command string) bool {
	// Match per line because JS `.` in tools/bash.ts:227 does not cross `\n`.
	for _, line := range strings.Split(command, "\n") {
		ifLoc := ddIfOfDevRE.FindStringIndex(line)
		if ifLoc == nil {
			continue
		}
		// Inspect every `of=/dev/...` after the first `dd if=...` on this line
		// (handles `dd if=big.mov of=/dev/null; dd if=image.iso of=/dev/sdb` as
		// well as `dd if=x 2>&1 of=/dev/sda`).
		for _, m := range ddOfDevAllRE.FindAllStringSubmatch(line[ifLoc[0]:], -1) {
			target := m[1]
			if !ddSafeSinkRE.MatchString(target) {
				return true
			}
		}
	}
	return false
}

// isCriticalBashCommand reports whether command matches any CRITICAL_BASH_PATTERNS rule.
func isCriticalBashCommand(command string) bool {
	if command == "" {
		return false
	}
	for _, re := range criticalBashPatterns {
		if re.MatchString(command) {
			return true
		}
	}
	return matchesCriticalDD(command)
}

var (
	bashApprovalReinterpretedArgRE = regexp.MustCompile(`(?:^|[ \t])(?:-[^-]*[ce]|--(?:command|eval))(?:[= \t]|$)`)
	shellEnvAssignRE               = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
)

func isApprovalShellControlChar(ch byte) bool {
	switch ch {
	case '\n', '\r', ';', '&', '|', '(', ')', '<', '>', '`', '$':
		return true
	default:
		return false
	}
}

// hasBashApprovalShellControl mirrors tools/bash.ts:124.
func hasBashApprovalShellControl(command string) bool {
	var quote byte
	hasReinterpretable := false
	for i := 0; i < len(command); i++ {
		ch := command[i]
		if quote == '\'' {
			if ch == '\'' {
				quote = 0
			} else if isApprovalShellControlChar(ch) {
				hasReinterpretable = true
			}
			continue
		}
		if ch == '\\' {
			if i+1 < len(command) && isApprovalShellControlChar(command[i+1]) {
				hasReinterpretable = true
			}
			i++
			continue
		}
		if quote == '"' {
			if ch == '"' {
				quote = 0
				continue
			}
			if ch == '`' || ch == '$' {
				return true
			}
			if isApprovalShellControlChar(ch) {
				hasReinterpretable = true
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			continue
		}
		if isApprovalShellControlChar(ch) {
			return true
		}
	}
	return hasReinterpretable && bashApprovalReinterpretedArgRE.MatchString(command)
}

// tokenizeShellSegments mirrors shell-tokenize.ts:14-109 for compound deny/prompt matching.
func tokenizeShellSegments(command string) [][]string {
	var segments [][]string
	var current []string
	var word strings.Builder
	inWord := false
	inSingle := false
	inDouble := false

	flushWord := func() {
		if !inWord {
			return
		}
		if word.Len() > 0 {
			current = append(current, word.String())
			word.Reset()
		}
		inWord = false
	}

	for i := 0; i < len(command); i++ {
		ch := command[i]
		if inSingle {
			if ch == '\'' {
				inSingle = false
			} else {
				word.WriteByte(ch)
			}
			continue
		}
		if inDouble {
			if ch == '\\' && i+1 < len(command) {
				next := command[i+1]
				if next == '"' || next == '\\' || next == '$' || next == '`' {
					word.WriteByte(next)
					i++
					continue
				}
			}
			if ch == '"' {
				inDouble = false
			} else {
				word.WriteByte(ch)
			}
			continue
		}
		switch ch {
		case ' ', '\t':
			flushWord()
		case '\n', ';', '&', '|', '(', ')':
			flushWord()
			if len(current) > 0 {
				segments = append(segments, current)
				current = nil
			}
		case '\'':
			inWord = true
			inSingle = true
		case '"':
			inWord = true
			inDouble = true
		case '\\':
			inWord = true
			if i+1 < len(command) {
				word.WriteByte(command[i+1])
				i++
			}
		default:
			inWord = true
			word.WriteByte(ch)
		}
	}
	flushWord()
	if len(current) > 0 {
		segments = append(segments, current)
	}
	return segments
}

func bashCommandSegments(command string) []string {
	var candidates []string
	for _, words := range tokenizeShellSegments(command) {
		if len(words) == 0 {
			continue
		}
		candidates = append(candidates, strings.Join(words, " "))
		idx := 0
		for idx < len(words) && shellEnvAssignRE.MatchString(words[idx]) {
			idx++
		}
		if idx > 0 && idx < len(words) {
			candidates = append(candidates, strings.Join(words[idx:], " "))
		}
	}
	return candidates
}

func normalizeApprovalPattern(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func approvalGlobToRegexp(pattern string) *regexp.Regexp {
	parts := strings.Split(normalizeApprovalPattern(pattern), "*")
	for i, part := range parts {
		parts[i] = regexp.QuoteMeta(part)
	}
	re, err := regexp.Compile("^" + strings.Join(parts, ".*") + "$")
	if err != nil {
		return nil
	}
	return re
}

func commandMatchesApprovalPattern(command, pattern string) bool {
	norm := normalizeApprovalPattern(command)
	if norm == "" {
		return false
	}
	re := approvalGlobToRegexp(pattern)
	return re != nil && re.MatchString(norm)
}

func stripAllowedLeadingEnv(command string, allowed []string) (string, bool) {
	if len(allowed) == 0 {
		return "", false
	}
	allowedSet := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		allowedSet[name] = true
	}
	index := 0
	found := false
	for index < len(command) {
		for index < len(command) && (command[index] == ' ' || command[index] == '\t') {
			index++
		}
		assignStart := index
		if index >= len(command) || !isIdentStart(command[index]) {
			break
		}
		nameEnd := index + 1
		for nameEnd < len(command) && isIdentPart(command[nameEnd]) {
			nameEnd++
		}
		if nameEnd >= len(command) || command[nameEnd] != '=' {
			if found {
				return strings.TrimLeft(command[assignStart:], " \t"), true
			}
			return "", false
		}
		varName := command[assignStart:nameEnd]
		if !allowedSet[varName] {
			return "", false
		}
		wordEnd := skipShellWord(command, nameEnd+1)
		if wordEnd < 0 {
			return "", false
		}
		found = true
		index = wordEnd
		if index == len(command) {
			return "", false
		}
	}
	if !found {
		return "", false
	}
	rest := strings.TrimLeft(command[index:], " \t")
	if rest == "" {
		return "", false
	}
	return rest, true
}

func findApprovalPatternRule(command string, rules []BashApprovalPatternRule, allowedEnv []string) *BashApprovalPatternRule {
	if hasBashApprovalShellControl(command) {
		segments := bashCommandSegments(command)
		var promptRule *BashApprovalPatternRule
		for i := range rules {
			rule := &rules[i]
			if rule.Approval == "allow" {
				continue
			}
			if commandMatchesApprovalPattern(command, rule.Match) {
				matchedSeg := false
				for _, seg := range segments {
					if commandMatchesApprovalPattern(seg, rule.Match) {
						matchedSeg = true
						break
					}
				}
				if !matchedSeg {
					if rule.Approval == "deny" {
						return rule
					}
					if promptRule == nil {
						promptRule = rule
					}
				}
			}
		}
		for _, seg := range segments {
			for i := range rules {
				rule := &rules[i]
				if rule.Approval == "allow" {
					continue
				}
				if commandMatchesApprovalPattern(seg, rule.Match) {
					if rule.Approval == "deny" {
						return rule
					}
					if promptRule == nil {
						promptRule = rule
					}
					break
				}
			}
		}
		return promptRule
	}

	trimmed := strings.TrimLeft(command, " \t")
	hasLeadingEnv := withoutLeadingEnvironmentAssignments(trimmed) != "" || shellEnvAssignRE.MatchString(trimmed)
	for i := range rules {
		rule := &rules[i]
		if rule.Approval == "allow" {
			if (!hasLeadingEnv || shellEnvAssignRE.MatchString(rule.Match)) && commandMatchesApprovalPattern(command, rule.Match) {
				return rule
			}
			if !hasLeadingEnv {
				continue
			}
			effEnv := allowedEnv
			if len(rule.Env) > 0 {
				effEnv = append(append([]string(nil), rule.Env...), allowedEnv...)
			}
			if stripped, ok := stripAllowedLeadingEnv(trimmed, effEnv); ok && commandMatchesApprovalPattern(stripped, rule.Match) {
				return rule
			}
			continue
		}
		if commandMatchesApprovalPattern(command, rule.Match) {
			return rule
		}
		for _, seg := range bashCommandSegments(command) {
			if commandMatchesApprovalPattern(seg, rule.Match) {
				return rule
			}
		}
	}
	return nil
}

// literalShellCommandSegment mirrors omp's LiteralShellCommandSegment (`tools/shell-tokenize.ts:112`).
type literalShellCommandSegment struct {
	text string
	argv []string
}

// andChainSegment mirrors omp's AndChainSegment (`tools/shell-tokenize.ts:120`).
type andChainSegment struct {
	text              string
	literal           *literalShellCommandSegment
	mutatesShellState bool
}

// shellStatefulCommands ports omp's SHELL_STATEFUL_COMMANDS (`tools/shell-tokenize.ts:128-224`).
var shellStatefulCommands = map[string]bool{
	"!": true, ":": true, ".": true, "test": true, "[": true, "[[": true,
	"-": true, "autoload": true, "bindkey": true, "bye": true, "chdir": true,
	"disable": true, "emulate": true, "functions": true, "limit": true,
	"print": true, "rehash": true, "repeat": true, "sched": true, "setopt": true,
	"unfunction": true, "unhash": true, "unlimit": true, "unsetopt": true,
	"vared": true, "zle": true, "zmodload": true, "zparseopts": true, "zstyle": true,
	"alias": true, "bg": true, "break": true, "builtin": true, "case": true,
	"cd": true, "command": true, "coproc": true, "continue": true, "declare": true,
	"dirs": true, "disown": true, "do": true, "done": true, "elif": true,
	"else": true, "enable": true, "esac": true, "eval": true, "exec": true,
	"exit": true, "export": true, "fc": true, "fg": true, "fi": true,
	"for": true, "function": true, "getopts": true, "hash": true, "history": true,
	"if": true, "in": true, "jobs": true, "let": true, "logout": true,
	"mapfile": true, "local": true, "popd": true, "printf": true, "pushd": true,
	"readonly": true, "read": true, "readarray": true, "return": true, "select": true,
	"set": true, "shift": true, "shopt": true, "source": true, "suspend": true,
	"then": true, "time": true, "times": true, "trap": true, "typeset": true,
	"ulimit": true, "umask": true, "unalias": true, "unset": true, "until": true,
	"wait": true, "while": true,
}

// shellInterpreterCommands ports omp's SHELL_INTERPRETER_COMMANDS (`tools/shell-tokenize.ts:226-241`).
var shellInterpreterCommands = map[string]bool{
	"ash": true, "bash": true, "busybox": true, "csh": true, "dash": true,
	"env": true, "nocorrect": true, "noglob": true, "fish": true, "ksh": true,
	"sh": true, "sudo": true, "tcsh": true, "zsh": true,
}

var (
	shellAssignmentRE        = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(?:\[[^\]]*\])?\+?=`)
	shellAssignmentNameRE    = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\+?=`)
	shellReinterpretOptionRE = regexp.MustCompile(`^(?:-[^-]*[ce]|--(?:command|eval)(?:=.*)?)$`)
	shellRedirWordRE         = regexp.MustCompile(`^[0-9]*[<>]+$`)
	shellRedirPrefixRE       = regexp.MustCompile(`^[0-9]*[<>]`)
)

func hasApprovalShellControlChar(text string) bool {
	for i := range len(text) {
		if isApprovalShellControlChar(text[i]) {
			return true
		}
	}
	return false
}

// extractLiteralAndChainSegments ports omp's `extractLiteralAndChainSegments`
// (`tools/shell-tokenize.ts:346-480`): parses two or more literal argv commands
// joined solely by `&&`, rejecting expansions, globs, redirections, comments,
// standalone/subscripted assignments, control flow, shell interpreters, and
// stateful shell builtins.
func extractLiteralAndChainSegments(command string) []literalShellCommandSegment {
	for i := range len(command) {
		c := command[i]
		if (c < 0x20 && c != '\t') || c == 0x7f {
			return nil
		}
	}
	var segments []literalShellCommandSegment
	var argv []string
	var rawTokens []string
	var token strings.Builder
	tokenStart := -1
	var quote byte
	segmentStart := 0

	startToken := func(index int) {
		if tokenStart < 0 {
			tokenStart = index
		}
	}
	pushToken := func(end int) {
		if tokenStart < 0 {
			return
		}
		argv = append(argv, token.String())
		rawTokens = append(rawTokens, command[tokenStart:end])
		token.Reset()
		tokenStart = -1
	}
	pushSegment := func(end int) bool {
		pushToken(end)
		if len(argv) == 0 {
			return false
		}
		cmdIdx := 0
		for cmdIdx < len(rawTokens) && shellAssignmentNameRE.MatchString(rawTokens[cmdIdx]) {
			cmdIdx++
		}
		if cmdIdx >= len(argv) {
			return false
		}
		executable := argv[cmdIdx]
		if executable == "" {
			return false
		}
		slashIdx := max(strings.LastIndexByte(executable, '/'), strings.LastIndexByte(executable, '\\'))
		cmdName := executable[slashIdx+1:]
		cmdArgs := argv[cmdIdx+1:]
		for _, arg := range argv[cmdIdx:] {
			if shellAssignmentRE.MatchString(arg) {
				return false
			}
		}
		hasReinterpretOpt := false
		hasControlArg := false
		for _, arg := range cmdArgs {
			if shellReinterpretOptionRE.MatchString(arg) {
				hasReinterpretOpt = true
			}
			if hasApprovalShellControlChar(arg) {
				hasControlArg = true
			}
		}
		if (hasReinterpretOpt && hasControlArg) || shellStatefulCommands[cmdName] || shellInterpreterCommands[cmdName] {
			return false
		}
		segments = append(segments, literalShellCommandSegment{
			text: strings.TrimSpace(command[segmentStart:end]),
			argv: argv,
		})
		argv = nil
		rawTokens = nil
		return true
	}

	for i := 0; i < len(command); i++ {
		ch := command[i]
		if quote == '\'' {
			if ch == '\'' {
				quote = 0
			} else {
				token.WriteByte(ch)
			}
			continue
		}
		if quote == '"' {
			if ch == '"' {
				quote = 0
				continue
			}
			if ch == '$' || ch == '`' || ch == '\n' || ch == '\r' {
				return nil
			}
			if ch == '\\' {
				if i+1 >= len(command) {
					return nil
				}
				next := command[i+1]
				if next == '\n' || next == '\r' {
					return nil
				}
				if next == '"' || next == '\\' || next == '$' || next == '`' {
					token.WriteByte(next)
					i++
				} else {
					token.WriteByte(ch)
				}
				continue
			}
			token.WriteByte(ch)
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			startToken(i)
			continue
		}
		if ch == '\\' {
			if i+1 >= len(command) {
				return nil
			}
			next := command[i+1]
			if next == '\n' || next == '\r' {
				return nil
			}
			startToken(i)
			token.WriteByte(next)
			i++
			continue
		}
		if ch == ' ' || ch == '\t' {
			pushToken(i)
			continue
		}
		if ch == '&' && i+1 < len(command) && command[i+1] == '&' {
			if !pushSegment(i) {
				return nil
			}
			i++
			segmentStart = i + 1
			continue
		}
		switch ch {
		case '\n', '\r', '&', '|', ';', '<', '>', '`', '$', '(', ')', '*', '?', '[', ']', '{', '}', '~':
			return nil
		case '#':
			if tokenStart < 0 {
				return nil
			}
		}
		if ch < 0x20 || ch == 0x7f {
			return nil
		}
		startToken(i)
		token.WriteByte(ch)
	}

	if quote != 0 || !pushSegment(len(command)) || len(segments) < 2 {
		return nil
	}
	return segments
}

func parseLiteralShellCommandSegment(segmentText string) *literalShellCommandSegment {
	pair := extractLiteralAndChainSegments(segmentText + " && true")
	if len(pair) == 0 {
		return nil
	}
	seg := pair[0]
	return &seg
}

func segmentMutatesShellState(segmentText string, literal *literalShellCommandSegment) bool {
	if literal != nil {
		return false
	}
	if strings.ContainsAny(segmentText, "$`(){}[]\n\r") {
		return true
	}
	segments := tokenizeShellSegments(segmentText)
	if len(segments) == 0 {
		return true
	}
	for _, words := range segments {
		idx := 0
		for idx < len(words) {
			w := words[idx]
			if shellAssignmentRE.MatchString(w) {
				if strings.ContainsRune(w, '[') {
					return true
				}
				idx++
			} else if shellRedirWordRE.MatchString(w) {
				idx += 2
			} else if shellRedirPrefixRE.MatchString(w) {
				idx++
			} else {
				break
			}
		}
		if idx >= len(words) {
			return true
		}
		executable := words[idx]
		slashIdx := max(strings.LastIndexByte(executable, '/'), strings.LastIndexByte(executable, '\\'))
		cmdName := executable[slashIdx+1:]
		cmdArgs := words[idx+1:]
		hasReinterpretOpt := false
		hasControlArg := false
		for _, arg := range cmdArgs {
			if shellReinterpretOptionRE.MatchString(arg) {
				hasReinterpretOpt = true
			}
			if hasApprovalShellControlChar(arg) {
				hasControlArg = true
			}
		}
		if shellStatefulCommands[cmdName] || shellInterpreterCommands[cmdName] || (hasReinterpretOpt && hasControlArg) {
			return true
		}
	}
	return false
}

// extractAndChainSegments ports omp's `extractAndChainSegments`
// (`tools/shell-tokenize.ts:534-679`) for compound approval detail formatting.
func extractAndChainSegments(command string) []andChainSegment {
	for i := range len(command) {
		c := command[i]
		if (c < 0x20 && c != '\t') || c == 0x7f {
			return nil
		}
	}
	var rawSegments []string
	var quote byte
	parenDepth := 0
	inBacktick := false
	inDoubleBracket := false
	braceDepth := 0
	atWordStart := true
	segmentStart := 0

	for i := 0; i < len(command); i++ {
		ch := command[i]
		if quote == '\'' {
			if ch == '\'' {
				quote = 0
			}
			continue
		}
		if quote == '"' {
			if ch == '\\' {
				if i+1 >= len(command) {
					return nil
				}
				i++
				continue
			}
			if ch == '"' {
				quote = 0
			}
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
		if ch == '`' {
			inBacktick = !inBacktick
			atWordStart = false
			continue
		}
		if inBacktick {
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			atWordStart = false
			continue
		}
		if ch == '#' && atWordStart {
			break
		}
		if !inDoubleBracket && atWordStart && ch == '[' && i+2 < len(command) && command[i+1] == '[' && (command[i+2] == ' ' || command[i+2] == '\t') {
			inDoubleBracket = true
			i++
			atWordStart = false
			continue
		}
		if inDoubleBracket && atWordStart && ch == ']' && i+1 < len(command) && command[i+1] == ']' {
			var after byte
			if i+2 < len(command) {
				after = command[i+2]
			}
			if after == 0 || after == ' ' || after == '\t' || after == ';' || after == '&' || after == '|' || after == ')' {
				inDoubleBracket = false
				i++
				atWordStart = false
				continue
			}
		}
		if ch == '$' && i+1 < len(command) && command[i+1] == '{' {
			braceDepth++
			i++
			atWordStart = false
			continue
		}
		if ch == '{' && atWordStart && i+1 < len(command) && (command[i+1] == ' ' || command[i+1] == '\t') {
			braceDepth++
			continue
		}
		if ch == '}' && braceDepth > 0 {
			braceDepth--
			atWordStart = false
			continue
		}
		if ch == '(' {
			parenDepth++
			atWordStart = true
			continue
		}
		if ch == ')' {
			if parenDepth == 0 {
				return nil
			}
			parenDepth--
			atWordStart = false
			continue
		}
		if parenDepth > 0 || inDoubleBracket || braceDepth > 0 {
			atWordStart = ch == ' ' || ch == '\t' || ch == '&' || ch == '|' || ch == '(' || ch == ')'
			continue
		}
		if ch == ';' {
			return nil
		}
		if ch == '|' && i+1 < len(command) && command[i+1] == '|' {
			return nil
		}
		if ch == '&' {
			var prev, next byte
			if i > 0 {
				prev = command[i-1]
			}
			if i+1 < len(command) {
				next = command[i+1]
			}
			if prev == '>' || prev == '<' || next == '>' {
				atWordStart = false
				continue
			}
			if next == '&' && (i+2 >= len(command) || command[i+2] != '&') {
				seg := strings.TrimSpace(command[segmentStart:i])
				if seg == "" {
					return nil
				}
				rawSegments = append(rawSegments, seg)
				i++
				segmentStart = i + 1
				atWordStart = true
				continue
			}
			return nil
		}
		atWordStart = ch == ' ' || ch == '\t'
	}

	if quote != 0 || inBacktick || parenDepth != 0 || inDoubleBracket || braceDepth != 0 {
		return nil
	}
	tail := strings.TrimSpace(command[segmentStart:])
	if tail == "" {
		return nil
	}
	rawSegments = append(rawSegments, tail)
	if len(rawSegments) < 2 {
		return nil
	}
	out := make([]andChainSegment, len(rawSegments))
	for i, text := range rawSegments {
		lit := parseLiteralShellCommandSegment(text)
		out[i] = andChainSegment{
			text:              text,
			literal:           lit,
			mutatesShellState: segmentMutatesShellState(text, lit),
		}
	}
	return out
}

func commandSegmentMatchesApprovalPattern(command, pattern string) bool {
	if commandMatchesApprovalPattern(command, pattern) {
		return true
	}
	for _, seg := range bashCommandSegments(command) {
		if commandMatchesApprovalPattern(seg, pattern) {
			return true
		}
	}
	return false
}

type bashPatternDecision struct {
	kind string // "deny" | "prompt" | "allow" | "unmatched"
	rule *BashApprovalPatternRule
}

// resolveBashPatternDecision ports omp's `resolveBashPatternDecision` (`tools/bash.ts:417-450`).
func resolveBashPatternDecision(
	command string,
	compoundSegments []literalShellCommandSegment,
	patternRules []BashApprovalPatternRule,
	allowedEnv []string,
) bashPatternDecision {
	if len(compoundSegments) > 0 {
		var promptRule *BashApprovalPatternRule
		for i := range patternRules {
			rule := &patternRules[i]
			if rule.Approval == "allow" {
				continue
			}
			if commandMatchesApprovalPattern(command, rule.Match) {
				matchedSeg := false
				for _, seg := range compoundSegments {
					if commandSegmentMatchesApprovalPattern(seg.text, rule.Match) {
						matchedSeg = true
						break
					}
				}
				if !matchedSeg {
					if rule.Approval == "deny" {
						return bashPatternDecision{kind: "deny", rule: rule}
					}
					if promptRule == nil {
						promptRule = rule
					}
				}
			}
		}
		hasUnmatchedSegment := false
		for _, seg := range compoundSegments {
			segRule := findApprovalPatternRule(seg.text, patternRules, allowedEnv)
			if segRule != nil && segRule.Approval == "deny" {
				return bashPatternDecision{kind: "deny", rule: segRule}
			}
			if segRule != nil && segRule.Approval == "prompt" && promptRule == nil {
				promptRule = segRule
			}
			if segRule == nil {
				hasUnmatchedSegment = true
			}
		}
		if promptRule != nil {
			return bashPatternDecision{kind: "prompt", rule: promptRule}
		}
		if hasUnmatchedSegment {
			return bashPatternDecision{kind: "unmatched"}
		}
		return bashPatternDecision{kind: "allow"}
	}

	rule := findApprovalPatternRule(command, patternRules, allowedEnv)
	if rule != nil {
		switch rule.Approval {
		case "deny":
			return bashPatternDecision{kind: "deny", rule: rule}
		case "prompt":
			return bashPatternDecision{kind: "prompt", rule: rule}
		case "allow":
			return bashPatternDecision{kind: "allow", rule: rule}
		}
	}
	return bashPatternDecision{kind: "unmatched"}
}

// evaluateBashApproval mirrors BashTool.approval (tools/bash.ts:834-894) for a single view.
func evaluateBashApproval(command string, rules []BashApprovalPatternRule, allowedEnv []string) approvalVerdict {
	return evaluateBashApprovalViews(command, rules, allowedEnv, rules, allowedEnv)
}

// evaluateBashApprovalViews evaluates two-view bash pattern policy without compound splitting.
func evaluateBashApprovalViews(
	command string,
	authorityRules []BashApprovalPatternRule,
	authorityAllowedEnv []string,
	sessionRules []BashApprovalPatternRule,
	sessionAllowedEnv []string,
) approvalVerdict {
	return evaluateBashApprovalViewsWithCompound(command, false, authorityRules, authorityAllowedEnv, sessionRules, sessionAllowedEnv)
}

// evaluateBashApprovalViewsWithCompound evaluates two-view bash pattern policy:
//   - authority view (operator layers only): authorityRules + authorityAllowedEnv
//   - session view (including project layers): sessionRules + sessionAllowedEnv
//   - allowCompound: when true, splits literal `&&` chains per segment (`tools/bash.ts:847-893`).
//
// Integrator binding two-view rule: never looser than omp, and a project layer
// can never loosen what operator layers alone would decide:
//   - deny from either view wins (authority checked first)
//   - prompt from either view wins next (authority checked first)
//   - critical bash patterns still override (checked on both full command and each segment's literal argv)
//   - allow counts ONLY when BOTH the authority view AND the session view allow.
func evaluateBashApprovalViewsWithCompound(
	command string,
	allowCompound bool,
	authorityRules []BashApprovalPatternRule,
	authorityAllowedEnv []string,
	sessionRules []BashApprovalPatternRule,
	sessionAllowedEnv []string,
) approvalVerdict {
	var compoundSegments []literalShellCommandSegment
	if allowCompound {
		compoundSegments = extractLiteralAndChainSegments(command)
	}
	sessionDec := resolveBashPatternDecision(command, compoundSegments, sessionRules, sessionAllowedEnv)
	authorityDec := resolveBashPatternDecision(command, compoundSegments, authorityRules, authorityAllowedEnv)

	if authorityDec.kind == "deny" && authorityDec.rule != nil {
		return approvalVerdict{Kind: "deny", Reason: fmt.Sprintf("Blocked by bash pattern: %s", authorityDec.rule.Match)}
	}
	if sessionDec.kind == "deny" && sessionDec.rule != nil {
		return approvalVerdict{Kind: "deny", Reason: fmt.Sprintf("Blocked by bash pattern: %s", sessionDec.rule.Match)}
	}
	if authorityDec.kind == "prompt" && authorityDec.rule != nil {
		return approvalVerdict{Kind: "prompt", Reason: fmt.Sprintf("Prompt required by bash pattern: %s", authorityDec.rule.Match)}
	}
	if sessionDec.kind == "prompt" && sessionDec.rule != nil {
		return approvalVerdict{Kind: "prompt", Reason: fmt.Sprintf("Prompt required by bash pattern: %s", sessionDec.rule.Match)}
	}
	criticalCommand := isCriticalBashCommand(command)
	if len(compoundSegments) == 0 && criticalCommand {
		return approvalVerdict{Kind: "critical", Reason: "Critical pattern detected"}
	}
	for _, seg := range compoundSegments {
		literalCmd := strings.Join(seg.argv, " ")
		if criticalCommand || isCriticalBashCommand(literalCmd) {
			return approvalVerdict{Kind: "critical", Reason: "Critical pattern detected"}
		}
	}
	if authorityDec.kind == "allow" && sessionDec.kind == "allow" {
		return approvalVerdict{Kind: "allow"}
	}
	return approvalVerdict{Kind: "exec"}
}

func parseBashPatternRules(raw any) []BashApprovalPatternRule {
	if raw == nil {
		return nil
	}
	if typed, ok := raw.([]BashApprovalPatternRule); ok {
		return typed
	}
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	var out []BashApprovalPatternRule
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		matchStr, _ := m["match"].(string)
		matchStr = normalizeApprovalPattern(matchStr)
		approvalStr, _ := m["approval"].(string)
		approvalStr = strings.ToLower(strings.TrimSpace(approvalStr))
		if matchStr == "" || (approvalStr != "allow" && approvalStr != "deny" && approvalStr != "prompt") {
			continue
		}
		var envList []string
		if rawEnv, ok := m["env"].([]any); ok {
			for _, e := range rawEnv {
				if s, ok := e.(string); ok && strings.TrimSpace(s) != "" {
					envList = append(envList, strings.TrimSpace(s))
				}
			}
		} else if rawEnv, ok := m["env"].([]string); ok {
			for _, s := range rawEnv {
				if strings.TrimSpace(s) != "" {
					envList = append(envList, strings.TrimSpace(s))
				}
			}
		}
		out = append(out, BashApprovalPatternRule{
			Match:    matchStr,
			Approval: approvalStr,
			Env:      envList,
		})
	}
	return out
}

func parseStringList(raw any) []string {
	if raw == nil {
		return nil
	}
	if s, ok := raw.([]string); ok {
		return s
	}
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		if s, ok := it.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

func parseInterceptorRules(raw any) []InterceptorRule {
	if raw == nil {
		return nil
	}
	if typed, ok := raw.([]InterceptorRule); ok {
		return typed
	}
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	var out []InterceptorRule
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		pat, _ := m["pattern"].(string)
		flags, _ := m["flags"].(string)
		tool, _ := m["tool"].(string)
		msg, _ := m["message"].(string)
		if pat == "" || tool == "" {
			continue
		}
		out = append(out, InterceptorRule{
			Pattern: pat,
			Flags:   flags,
			Tool:    tool,
			Message: msg,
			matcher: builtinMatcherForPattern(pat, flags),
		})
	}
	return out
}

func readSetting(ctx sdk.Context, key string) any {
	if Setting == nil {
		return nil
	}
	return Setting(ctx, key)
}

func readSessionSetting(ctx sdk.Context, key string) any {
	if SessionSetting == nil {
		return readSetting(ctx, key)
	}
	return SessionSetting(ctx, key)
}

func readSettingBool(ctx sdk.Context, key string, fallback bool) bool {
	if v, ok := readSetting(ctx, key).(bool); ok {
		return v
	}
	return fallback
}

func readSettingInt(ctx sdk.Context, key string, fallback int) int {
	switch v := readSetting(ctx, key).(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		if v == v { // !NaN
			return int(v)
		}
	}
	return fallback
}

func readSettingFloat(ctx sdk.Context, key string, fallback float64) float64 {
	switch v := readSetting(ctx, key).(type) {
	case float64:
		if v == v { // !NaN
			return v
		}
	case int:
		return float64(v)
	case int64:
		return float64(v)
	}
	return fallback
}

// activeToolNamesForInterceptor resolves the session's active tool names for
// `CheckBashInterception`, falling back to omp's default core tool names when
// `ctx.GetActiveTools()` is unavailable (e.g. unit tests without a host).
func activeToolNamesForInterceptor(ctx sdk.Context, configured []string) []string {
	if len(configured) > 0 {
		return configured
	}
	var zero sdk.Context
	if ctx != zero {
		var active []string
		func() {
			defer func() { _ = recover() }()
			if list, err := ctx.GetActiveTools(); err == nil && len(list) > 0 {
				active = list
			}
		}()
		if len(active) > 0 {
			return active
		}
	}
	return []string{"read", "grep", "glob", "edit", "write", "bash"}
}

func checkCommandInterception(ctx sdk.Context, rawCommand, cwdCommand, serviceName string, configuredTools []string, rules []InterceptorRule, toolCmdSet map[string]bool) string {
	if len(rules) == 0 {
		rules = DefaultInterceptorRules
	}
	if strings.TrimSpace(serviceName) != "" {
		filtered := make([]InterceptorRule, 0, len(rules))
		for _, r := range rules {
			if r.Tool != "bash" {
				filtered = append(filtered, r)
			}
		}
		rules = filtered
	}
	available := activeToolNamesForInterceptor(ctx, configuredTools)
	toCheck := []string{cwdCommand}
	if rawCommand != cwdCommand {
		toCheck = []string{rawCommand, cwdCommand}
	}
	for _, candidate := range toCheck {
		if startsWithToolCommand(candidate, toolCmdSet) {
			continue
		}
		res := checkBashInterceptionWithNotify(candidate, available, rules, rawCommand, func(rule InterceptorRule, err error) {
			warnUncompilableInterceptorRuleOnce(ctx, rule, err)
		})
		if res.Block {
			return res.Message
		}
	}
	return ""
}

// EvaluateBashToolDecision implements approval.BashEvaluator for `bash`:
// evaluates `bashInterceptor.enabled` (tool-owned deny before any prompt),
// two-view operator+session `bash.patterns` + operator `bash.allowedEnvVars`,
// `CRITICAL_BASH_PATTERNS`, and mounted tool-command-line read tier
// (`tools/bash.ts:834-894`).
func EvaluateBashToolDecision(ctx sdk.Context, input map[string]any) approval.ToolDecision {
	command, _ := input["command"].(string)
	readOp := readSetting
	if Setting == nil && approval.Setting != nil {
		readOp = approval.Setting
	}
	readSess := readSessionSetting
	if SessionSetting == nil && Setting == nil && approval.SessionSetting != nil {
		readSess = approval.SessionSetting
	} else if SessionSetting == nil && Setting == nil && approval.Setting != nil {
		readSess = approval.Setting
	}

	var toolCmdSet map[string]bool
	if CommandNames != nil {
		if names := CommandNames(ctx); len(names) > 0 {
			toolCmdSet = make(map[string]bool, len(names))
			for _, n := range names {
				if n != "" {
					toolCmdSet[n] = true
				}
			}
		}
	}

	// 1. Bash interceptor check (`tools/bash.ts:838-845`): an intercepted call
	// is a tool-owned deny before any prompt. Under the two-view rule, if
	// enabled in EITHER the operator view or the session view, interception runs.
	opIntEnabled, _ := readOp(ctx, "bashInterceptor.enabled").(bool)
	sessIntEnabled, _ := readSess(ctx, "bashInterceptor.enabled").(bool)
	if (opIntEnabled || sessIntEnabled) && command != "" {
		cwdArg, _ := input["cwd"].(string)
		cwdCommand := command
		if strings.TrimSpace(cwdArg) == "" {
			if _, rest, _, ok := extractLeadingCdTarget(command); ok {
				cwdCommand = rest
			}
		}
		svcName, _ := input["name"].(string)
		rules := DefaultInterceptorRules
		if sessIntEnabled {
			if parsed := parseInterceptorRules(readSess(ctx, "bashInterceptor.patterns")); len(parsed) > 0 {
				rules = parsed
			}
		} else if parsed := parseInterceptorRules(readOp(ctx, "bashInterceptor.patterns")); len(parsed) > 0 {
			rules = parsed
		}
		if msg := checkCommandInterception(ctx, command, cwdCommand, svcName, nil, rules, toolCmdSet); msg != "" {
			reason := strings.TrimPrefix(msg, "Blocked: ")
			if reason == msg {
				reason = strings.TrimLeft(strings.TrimPrefix(msg, "Blocked:"), " \t")
			}
			return approval.ToolDecision{
				Tier:     approval.TierExec,
				Override: true,
				Policy:   approval.PolicyDeny,
				Reason:   reason,
			}
		}
	}

	authorityRules := parseBashPatternRules(readOp(ctx, "bash.patterns"))
	authorityAllowedEnv := parseStringList(readOp(ctx, "bash.allowedEnvVars"))
	sessionRules := parseBashPatternRules(readSess(ctx, "bash.patterns"))
	sessionAllowedEnv := parseStringList(readSess(ctx, "bash.allowedEnvVars"))
	// Two-view tightening for bash.allowCompoundCommands: enabling compound
	// evaluation can loosen approval from exec->allow on `&&` chains, so BOTH
	// operator authority AND session view must enable it (a project layer can
	// disable what the operator enabled, never enable what the operator left off).
	opAllowCompound, _ := readOp(ctx, "bash.allowCompoundCommands").(bool)
	sessAllowCompound := opAllowCompound
	if v, ok := readSess(ctx, "bash.allowCompoundCommands").(bool); ok {
		sessAllowCompound = v
	}
	allowCompound := opAllowCompound && sessAllowCompound
	verdict := evaluateBashApprovalViewsWithCompound(command, allowCompound, authorityRules, authorityAllowedEnv, sessionRules, sessionAllowedEnv)
	if verdict.Kind == "exec" && command != "" && isToolCommandLine(command, toolCmdSet) {
		// Each mounted tool command passes its own approval when executed
		// via ctx.ExecuteTool; the outer bash line itself only reads
		// (upstream: tools/bash.ts:890-891).
		return approval.ToolDecision{Tier: approval.TierRead}
	}
	return verdictToToolDecision(verdict)
}

// shellLexToken mirrors omp's ShellToken (`tools/shell-tokenize.ts:31`).
type shellLexToken struct {
	isSeparator bool
	raw         string
	value       string
}

// lexShellCommand ports omp's `lexShellCommand` (`tools/shell-tokenize.ts:39-109`).
func lexShellCommand(command string) []shellLexToken {
	var tokens []shellLexToken
	var value strings.Builder
	wordStart := -1
	sepStart := -1
	inSingle := false
	inDouble := false

	startWord := func(index int) {
		if wordStart >= 0 {
			return
		}
		if sepStart >= 0 {
			tokens = append(tokens, shellLexToken{isSeparator: true, raw: command[sepStart:index]})
			sepStart = -1
		}
		wordStart = index
	}
	endWord := func(index int) {
		if wordStart < 0 {
			return
		}
		tokens = append(tokens, shellLexToken{isSeparator: false, raw: command[wordStart:index], value: value.String()})
		value.Reset()
		wordStart = -1
	}

	for i := 0; i < len(command); i++ {
		ch := command[i]
		if inSingle {
			if ch == '\'' {
				inSingle = false
			} else {
				value.WriteByte(ch)
			}
			continue
		}
		if inDouble {
			if ch == '\\' && i+1 < len(command) {
				next := command[i+1]
				if next == '"' || next == '\\' || next == '$' || next == '`' {
					value.WriteByte(next)
					i++
					continue
				}
			}
			if ch == '"' {
				inDouble = false
			} else {
				value.WriteByte(ch)
			}
			continue
		}
		switch ch {
		case ' ', '\t', '\n', ';', '&', '|', '(', ')':
			endWord(i)
			if sepStart < 0 {
				sepStart = i
			}
			continue
		}
		startWord(i)
		switch ch {
		case '\'':
			inSingle = true
		case '"':
			inDouble = true
		case '\\':
			if i+1 < len(command) {
				value.WriteByte(command[i+1])
				i++
			}
		default:
			value.WriteByte(ch)
		}
	}
	endWord(len(command))
	if sepStart >= 0 {
		tokens = append(tokens, shellLexToken{isSeparator: true, raw: command[sepStart:]})
	}
	return tokens
}

// isToolCommandLine ports omp's `isToolCommandLine` (`tools/bash.ts:727-759`):
// every simple command in `command` invokes one of `toolCmdSet`, joined only by
// `;`, `&&`, `||`, `|`, `|&`, and newlines, with no redirection (`<`, `>`),
// expansion/substitution (`$`, “ ` “), CR (`\r`), grouping (`(`, `)`), comment
// (`#`), backgrounding (`&`), or assignment prefix. Note that because the scan
// checks `token.raw` for `[<>`$\r]` (including inside quotes, matching omp
// `bash.ts:751`), a quoted `$A` such as `ast_edit -j '{"pat":"console.log($A)"}'`
// returns false and keeps the bash call at `exec` tier.
// upstream: tools/bash.ts:727-759,830-832,891
func isToolCommandLine(command string, toolCmdSet map[string]bool) bool {
	if len(toolCmdSet) == 0 {
		return false
	}
	commandStart := true
	commands := 0
	for _, tok := range lexShellCommand(command) {
		if tok.isSeparator {
			op := strings.NewReplacer(" ", "", "\t", "", "\n", "").Replace(tok.raw)
			switch op {
			case "", ";", "&&", "||", "|", "|&":
			default:
				return false
			}
			if strings.ContainsAny(tok.raw, "\n;&|") {
				commandStart = true
			}
			continue
		}
		if strings.ContainsAny(tok.raw, "<>`$\r") || strings.HasPrefix(tok.raw, "#") {
			return false
		}
		if commandStart {
			if !toolCmdSet[tok.value] {
				return false
			}
			commands++
			commandStart = false
		}
	}
	return commands > 0
}

func verdictToToolDecision(verdict approvalVerdict) approval.ToolDecision {
	switch verdict.Kind {
	case "deny":
		return approval.ToolDecision{
			Tier:     approval.TierExec,
			Policy:   approval.PolicyDeny,
			Override: true,
			Reason:   verdict.Reason,
		}
	case "prompt":
		return approval.ToolDecision{
			Tier:     approval.TierExec,
			Policy:   approval.PolicyPrompt,
			Override: true,
			Reason:   verdict.Reason,
		}
	case "critical":
		return approval.ToolDecision{
			Tier:     approval.TierExec,
			Override: true,
			Reason:   verdict.Reason,
		}
	case "allow":
		return approval.ToolDecision{
			Tier:   approval.TierWrite,
			Policy: approval.PolicyAllow,
		}
	default:
		return approval.ToolDecision{Tier: approval.TierExec}
	}
}

// formatCompoundApprovalDetails ports omp's `BashTool.#formatCompoundApprovalDetails`
// (`tools/bash.ts:924-992`) so approval prompts on `&&` chains show per-segment coverage
// when `bash.allowCompoundCommands` is enabled.
func formatCompoundApprovalDetails(
	command string,
	authorityRules []BashApprovalPatternRule,
	authorityAllowedEnv []string,
	sessionRules []BashApprovalPatternRule,
	sessionAllowedEnv []string,
) []string {
	segments := extractAndChainSegments(command)
	if len(segments) == 0 {
		return nil
	}
	// Whole-chain restrictions apply to the complete chain rather than individual segments.
	for _, rules := range [][]BashApprovalPatternRule{authorityRules, sessionRules} {
		for i := range rules {
			rule := &rules[i]
			if rule.Approval == "allow" || !commandMatchesApprovalPattern(command, rule.Match) {
				continue
			}
			matchedSeg := false
			for _, seg := range segments {
				if commandSegmentMatchesApprovalPattern(seg.text, rule.Match) {
					matchedSeg = true
					break
				}
			}
			if !matchedSeg {
				return nil
			}
		}
	}

	segmentCritical := make([]bool, len(segments))
	anySegCritical := false
	for i, seg := range segments {
		crit := isCriticalBashCommand(seg.text)
		if !crit && seg.literal != nil {
			crit = isCriticalBashCommand(strings.Join(seg.literal.argv, " "))
		}
		segmentCritical[i] = crit
		if crit {
			anySegCritical = true
		}
	}
	if command != "" && isCriticalBashCommand(command) && !anySegCritical {
		return nil
	}

	chainUntainted := true
	rows := make([]string, 0, len(segments)+1)
	rows = append(rows, "Command:")
	for i, seg := range segments {
		isCrit := segmentCritical[i]
		segRule := findApprovalPatternRule(seg.text, sessionRules, sessionAllowedEnv)
		authRule := findApprovalPatternRule(seg.text, authorityRules, authorityAllowedEnv)
		var promptRule *BashApprovalPatternRule
		if authRule != nil && authRule.Approval == "prompt" {
			promptRule = authRule
		} else if segRule != nil && segRule.Approval == "prompt" {
			promptRule = segRule
		}
		covered := chainUntainted &&
			seg.literal != nil &&
			!isCrit &&
			promptRule == nil &&
			(authRule == nil || authRule.Approval != "deny") &&
			authRule != nil && authRule.Approval == "allow" &&
			segRule != nil && segRule.Approval == "allow"
		if seg.mutatesShellState {
			chainUntainted = false
		}
		if covered {
			rows = append(rows, fmt.Sprintf("  ✓ %s (allowed: %s)", truncateForApprovalPrompt(seg.text), segRule.Match))
		} else {
			reason := "requires approval"
			if promptRule != nil {
				reason = "prompt: " + promptRule.Match
			} else if isCrit {
				reason = "critical pattern"
			}
			rows = append(rows, fmt.Sprintf("  ? %s (%s)", truncateForApprovalPrompt(seg.text), reason))
		}
	}
	return rows
}

func truncateForApprovalPrompt(s string) string {
	const maxLen = 120
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "…"
}

func init() {
	approval.SetBashEvaluator(EvaluateBashToolDecision, nil)
}
