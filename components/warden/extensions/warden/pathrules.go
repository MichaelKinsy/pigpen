package warden

import (
	"os"
	"regexp"
	"strings"
	"sync"
)

// Path rules (src/guard.ts): user-declared paths, an access dimension, and two surfaces. The file surface checks the
// structured `input.path` of file tools: cheap and exact. The command surface (tools: "*") matches the way SENSITIVE_PATH
// does, against the StripDataText-processed command, plus redirect and tee targets for write-side precision. Tokens in
// arbitrary argv are deliberately never classified.

var (
	// A write sink a shell grammar defines: the target of a redirection.
	reRedirectTarget = lazyRE(`(?:^|[\s;&|)(])\d*>{1,2}[|&]?\s*(\S+)`)
	reQuoteChars     = lazyRE(`["']`)
)

// writeSinkTargets are the files a command writes by redirection or `tee`.
func writeSinkTargets(command string) []string {
	var targets []string
	// Redirect targets are scanned on the full command, not per segment: `>|` and `>&` contain `|`/`&` that splitShell
	// would split as pipe/and operators, separating the operator from its target.
	for _, m := range reRedirectTarget.FindAllStringSubmatch(command, -1) {
		if m[1] != "" {
			targets = append(targets, m[1])
		}
	}
	// tee writes every operand after its flags; an -a flag only appends, which is still a write. Head-anchored
	// (segment head, skipping wrappers) so `grep tee file.log` is not mistaken for a tee invocation.
	for _, segment := range splitShell(command) {
		if head, ok := headOf(segment); ok && head == "tee" {
			tokens := jsSplitWhitespace(jsTrim(segment))
			i := 0
			for k, t := range tokens {
				if t == "tee" {
					i = k + 1
					break
				}
			}
			for i < len(tokens) && strings.HasPrefix(tokens[i], "-") {
				i++
			}
			for ; i < len(tokens); i++ {
				if t := reQuoteChars.ReplaceAllString(tokens[i], ""); t != "" {
					targets = append(targets, t)
				}
			}
		}
	}
	return targets
}

// userRegexps caches the regular expressions a user's rules compile to (nil for a pattern that does not compile).
var userRegexps struct {
	mu sync.Mutex
	m  map[string]*regexp.Regexp
}

func compileCached(pattern string) *regexp.Regexp {
	userRegexps.mu.Lock()
	defer userRegexps.mu.Unlock()
	if c, ok := userRegexps.m[pattern]; ok {
		return c
	}
	c, err := regexp.Compile(pattern)
	if err != nil {
		c = nil
	}
	if userRegexps.m == nil || len(userRegexps.m) >= 512 {
		userRegexps.m = map[string]*regexp.Regexp{}
	}
	userRegexps.m[pattern] = c
	return c
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

// matchPathGlobs is the shared glob/regex path matcher used by both path rules and arming rules. It normalises `~`
// expansion and path separators, then tries the pattern in both tilde-prefixed and bare forms.
func matchPathGlobs(patterns []string, useRegex bool, candidate string) bool {
	home := homeDir()
	target := candidate
	if candidate == "~" || strings.HasPrefix(candidate, "~/") {
		target = home + candidate[1:]
	}
	if useRegex {
		for _, pattern := range patterns {
			if re := compileCached(pattern); re != nil && re.MatchString(target) {
				return true
			}
		}
		return false
	}
	relative := target
	if strings.HasPrefix(target, home+"/") {
		relative = "~" + target[len(home):]
	}
	raw := strings.TrimLeft(relative, "/")
	if strings.HasPrefix(relative, "~/") {
		raw = relative[2:]
	}
	for _, pattern := range patterns {
		forms := []string{pattern}
		if strings.HasPrefix(pattern, "~/") {
			forms = append(forms, pattern[2:])
		}
		for _, form := range forms {
			re := globToRegexp(form)
			if re.MatchString(raw) || re.MatchString(relative) {
				return true
			}
		}
	}
	return false
}

func pathRuleMatches(rule PathRule, candidate string) bool {
	return matchPathGlobs(rule.Paths, rule.Regex, candidate)
}

// isWriteTool: which side of a file tool's touch: write/edit change the file, every other tool only reads it.
func isWriteTool(tool string) bool { return tool == "write" || tool == "edit" }

func pathRuleHit(rule PathRule, label string) PatternHit {
	// note rides the sensitive severity (the judge decides whether a command that merely mentions the path can write;
	// offline it stays a warning like the sensitive-path hit); warn/confirm/block map onto the ladder the command rules use.
	hit := PatternHit{ID: rule.ID, Label: label, Message: rule.Message}
	switch rule.Action {
	case "block":
		hit.Severity = SeverityDeny
	case "confirm":
		hit.Severity = SeverityDestructive
		hit.Action = "dialog"
	case "warn":
		hit.Severity = SeverityRisky
	default:
		hit.Severity = SeveritySensitive
	}
	return hit
}

const mentionDepthPrefix = "(?:.*/)?"

// mentionTail is the path boundary the mention regex ends on instead of end-of-string, as a consumed character (the
// upstream lookahead `(?=[\s'`|;()&]|$)`; RE2 has none, and for a yes/no match the two are the same).
const mentionTail = "(?:[\\s\\x{00a0}\\x{1680}\\x{2000}-\\x{200a}\\x{2028}\\x{2029}\\x{202f}\\x{205f}\\x{3000}\\x{feff}'`|;()&]|$)"

var mentionCache struct {
	mu sync.Mutex
	m  map[string][]*regexp.Regexp
}

// mentionRegexps: glob patterns become unanchored regexes: the command surface asks "does this text mention the path",
// not "is this token the path", so `**/.env` must find `.env` inside `kubectl exec -- cat /x/.env`.
func mentionRegexps(rule PathRule) []*regexp.Regexp {
	key := strings.Join(rule.Paths, "\x00")
	if rule.Regex {
		key = "r\x00" + key
	}
	mentionCache.mu.Lock()
	defer mentionCache.mu.Unlock()
	if c, ok := mentionCache.m[key]; ok {
		return c
	}
	var out []*regexp.Regexp
	for _, pattern := range rule.Paths {
		if rule.Regex {
			if re, err := regexp.Compile(pattern); err == nil {
				out = append(out, re)
			}
			continue
		}
		// globBody builds `^(?:.*/)?…$` (plus one more `(?:.*/)?` per `**/` in the pattern); the command surface asks
		// "does this text mention the path", so the head anchor and the depth prefixes come off. The tail stays anchored
		// when the glob ends in a literal (`.env` must not match `.env.example`), but loses the `$` when it ends in a
		// wildcard segment (`id_*` -> `[^/]*$`): the wildcard already allows a suffix, and a hard `$` would prevent
		// `id_ed25519.pub` from matching `id_*` in command text.
		glob := pattern
		if strings.HasPrefix(glob, "~/") {
			glob = glob[2:]
		}
		body := "^" + mentionDepthPrefix + globBody(glob) + "$"
		for strings.HasPrefix(body, "^") || strings.HasPrefix(body, mentionDepthPrefix) {
			if strings.HasPrefix(body, "^") {
				body = body[1:]
			} else {
				body = body[len(mentionDepthPrefix):]
			}
		}
		lastSegment := pattern
		if i := strings.LastIndex(lastSegment, "/"); i >= 0 {
			lastSegment = lastSegment[i+1:]
		}
		tail := mentionTail
		if strings.ContainsAny(lastSegment, "*?") {
			tail = ""
		}
		body = strings.TrimSuffix(body, "$") + tail
		if re, err := regexp.Compile(body); err == nil {
			out = append(out, re)
		}
	}
	if mentionCache.m == nil || len(mentionCache.m) >= 512 {
		mentionCache.m = map[string][]*regexp.Regexp{}
	}
	mentionCache.m[key] = out
	return out
}

func anyMatchText(res []*regexp.Regexp, text string) bool {
	for _, re := range res {
		if re.MatchString(text) {
			return true
		}
	}
	return false
}

// matchPathRules are the hits of the user's path rules for one call.
func matchPathRules(tool string, input map[string]any, cwd string, rules []PathRule, exempt map[string]bool) []PatternHit {
	if len(rules) == 0 {
		return nil
	}
	var hits []PatternHit
	fired := map[string]bool{}
	// The file surface checks the structured path field of the named file tools. The command surface covers every
	// command tool ("*" or an explicit command tool name such as "bash"), because the write-sink and mention matching
	// apply to any tool that carries a shell command.
	isCommandTool := false
	for _, t := range CommandTools {
		isCommandTool = isCommandTool || t == tool
	}
	applies := func(rule PathRule, command bool) bool {
		if exempt[rule.ID] {
			return false
		}
		for _, t := range rule.Tools {
			if t == "*" {
				return true
			}
		}
		if command {
			return isCommandTool
		}
		for _, t := range rule.Tools {
			if t == tool {
				return true
			}
		}
		return false
	}
	fire := func(rule PathRule, writeSide bool, label string) {
		if fired[rule.ID] {
			return
		}
		// The access dimension: "none" fires on any touch; "read" only on the write side (reads flow); "write" only
		// on the read side (writes flow). The names read from the operator's goal: protect reads, or protect writes.
		if rule.Access == "none" || (rule.Access == "read" && writeSide) || (rule.Access == "write" && !writeSide) {
			fired[rule.ID] = true
			hits = append(hits, pathRuleHit(rule, label))
		}
	}
	labelOr := func(rule PathRule, fallback string) string {
		if rule.Message != "" {
			return rule.Message
		}
		return fallback
	}
	// File surface: the structured path field, exact and cheap. ctx_execute_file is a read like any other file tool.
	if path, ok := input["path"].(string); ok && jsTrim(path) != "" {
		for _, rule := range rules {
			if !applies(rule, false) {
				continue
			}
			if (rule.OnlyIfExists == nil || *rule.OnlyIfExists) && cwd != "" {
				if _, err := os.Stat(resolvePath(cwd, path)); err != nil {
					continue
				}
			}
			if pathRuleMatches(rule, path) || (cwd != "" && pathRuleMatches(rule, resolvePath(cwd, path))) {
				fire(rule, isWriteTool(tool), labelOr(rule, "touches "+rule.ID))
			}
		}
	}
	// Command surface: whole-text match for "none" rules (the operator declared the path always-matters, so a mention
	// anywhere counts), write-sink targets only for the write side (a grep naming the path is a read).
	if view, ok := CommandOf(tool, input); ok && view.Command != "" {
		text := StripDataText(view.Command).Text
		sinks := writeSinkTargets(text)
		for _, rule := range rules {
			if !applies(rule, true) {
				continue
			}
			writesToThis := false
			for _, target := range sinks {
				if pathRuleMatches(rule, target) {
					writesToThis = true
					break
				}
			}
			mention := mentionRegexps(rule)
			if rule.Access == "none" && anyMatchText(mention, text) {
				fire(rule, false, labelOr(rule, "touches "+rule.ID))
			}
			// Sink hits and bare mentions are independent: a command can both read and write the same path
			// (`cat a.log | tee b.log`). fire()'s access gate and the fired set keep the two sides from double-reporting.
			if writesToThis {
				fire(rule, true, labelOr(rule, "writes to "+rule.ID))
			}
			// For access:"write" (reads held, writes flow), a mention in a write-sink position is a write, not a read:
			// blank the sink targets from the text before checking mentions, so a path that only appears as a sink stays quiet.
			if rule.Access == "write" {
				sans := text
				for _, s := range sinks {
					sans = strings.ReplaceAll(sans, s, "")
				}
				if anyMatchText(mention, sans) {
					fire(rule, false, labelOr(rule, "reads "+rule.ID))
				}
			}
		}
	}
	return hits
}

// inertPathRules are path rules whose access + tools combination means they can never fire: `access:"write"` (reads held)
// with only write tools (writes flow, nothing to hold), or `tools:["read"]` when `read` is not in `action.tools` (the read
// tool is never inspected). Reported once at load, like unknown exempt ids.
func inertPathRules(rules []PathRule, actionTools []string) []string {
	var result []string
	seen := map[string]bool{}
	add := func(id string) {
		if !seen[id] {
			seen[id] = true
			result = append(result, id)
		}
	}
	has := func(list []string, v string) bool {
		for _, x := range list {
			if x == v {
				return true
			}
		}
		return false
	}
	for _, rule := range rules {
		var fileTools []string
		for _, t := range rule.Tools {
			if t != "*" {
				fileTools = append(fileTools, t)
			}
		}
		every := func(ok func(string) bool) bool {
			for _, t := range fileTools {
				if !ok(t) {
					return false
				}
			}
			return true
		}
		// access:"write" holds reads; a rule with only write/edit tools checks writes, which flow under "write".
		if rule.Access == "write" && len(fileTools) > 0 && every(func(t string) bool { return t == "write" || t == "edit" }) {
			add(rule.ID)
		}
		// tools:["read"] when read is not in action.tools: the read tool is never inspected, so the rule never fires.
		if len(fileTools) > 0 && every(func(t string) bool { return t == "read" }) && !has(actionTools, "read") {
			add(rule.ID)
		}
	}
	return result
}
