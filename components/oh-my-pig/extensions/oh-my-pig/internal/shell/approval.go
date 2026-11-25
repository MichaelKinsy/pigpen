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

// evaluateBashApproval mirrors BashTool.approval (tools/bash.ts:834-894) for a single view.
func evaluateBashApproval(command string, rules []BashApprovalPatternRule, allowedEnv []string) approvalVerdict {
	return evaluateBashApprovalViews(command, rules, allowedEnv, rules, allowedEnv)
}

// evaluateBashApprovalViews evaluates two-view bash pattern policy:
//   - authority view (operator layers only): authorityRules + authorityAllowedEnv
//   - session view (including project layers): sessionRules + sessionAllowedEnv
//
// Integrator binding two-view rule: never looser than omp, and a project layer
// can never loosen what operator layers alone would decide:
//   - deny from either view wins (authority checked first)
//   - prompt from either view wins next (authority checked first)
//   - critical bash patterns still override
//   - allow counts ONLY when BOTH the authority view AND the session view allow
//     (so operator `allow npm test*` + `allowedEnvVars: [CI]` with project
//     `allowedEnvVars: [LD_PRELOAD]` prompts on `CI=1 npm test` as omp does,
//     and project-only `allow` never loosens operator policy).
func evaluateBashApprovalViews(
	command string,
	authorityRules []BashApprovalPatternRule,
	authorityAllowedEnv []string,
	sessionRules []BashApprovalPatternRule,
	sessionAllowedEnv []string,
) approvalVerdict {
	sessionRule := findApprovalPatternRule(command, sessionRules, sessionAllowedEnv)
	authorityRule := findApprovalPatternRule(command, authorityRules, authorityAllowedEnv)

	if authorityRule != nil && authorityRule.Approval == "deny" {
		return approvalVerdict{Kind: "deny", Reason: fmt.Sprintf("Blocked by bash pattern: %s", authorityRule.Match)}
	}
	if sessionRule != nil && sessionRule.Approval == "deny" {
		return approvalVerdict{Kind: "deny", Reason: fmt.Sprintf("Blocked by bash pattern: %s", sessionRule.Match)}
	}
	if authorityRule != nil && authorityRule.Approval == "prompt" {
		return approvalVerdict{Kind: "prompt", Reason: fmt.Sprintf("Prompt required by bash pattern: %s", authorityRule.Match)}
	}
	if sessionRule != nil && sessionRule.Approval == "prompt" {
		return approvalVerdict{Kind: "prompt", Reason: fmt.Sprintf("Prompt required by bash pattern: %s", sessionRule.Match)}
	}
	if isCriticalBashCommand(command) {
		return approvalVerdict{Kind: "critical", Reason: "Critical pattern detected"}
	}
	if authorityRule != nil && authorityRule.Approval == "allow" && sessionRule != nil && sessionRule.Approval == "allow" {
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
	verdict := evaluateBashApprovalViews(command, authorityRules, authorityAllowedEnv, sessionRules, sessionAllowedEnv)
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

func init() {
	approval.SetBashEvaluator(EvaluateBashToolDecision, nil)
}
