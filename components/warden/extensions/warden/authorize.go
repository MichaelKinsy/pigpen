package warden

import (
	"strings"
)

// The violation model and authorization (src/guard.ts): a deterministic reading of the user's prompt against the
// deletions a command would make. Only rm-family hits of a bash command yield violations; every other hit is decided by
// the pattern loop. A prompt authorizes a violation when it names the action (delete, remove, clean...), does not negate
// it, and names every target. Nothing authorizes a recursive rm of `/`, `~`, `$HOME`, a parent, or a path outside the
// project.

// ViolationSource is who found the violation.
type ViolationSource string

const (
	SourcePattern       ViolationSource = "pattern"
	SourceRulesGuard    ViolationSource = "rules-guard"
	SourceSecurityGuard ViolationSource = "security-guard"
	SourceSlopGuard     ViolationSource = "slop-guard"
)

// ViolationScope is the scope of a violation for deterministic authorization matching.
type ViolationScope struct {
	// Paths are the files involved, e.g. ["eval/reports/"]; none means the verb alone decides.
	Paths []string
	// Command is the full shell command, if bash.
	Command string
	// Tool is the tool name.
	Tool string
	// TargetIndex and TargetCount say which target of an rm this violation represents.
	TargetIndex, TargetCount int
	// Segments are the command pipelines the prompt must quote (whitespace collapsed) when an rm-family violation has no
	// target. nil means no such requirement; an empty non-nil slice is satisfied by any prompt.
	Segments []string
}

// Violation is a pattern detection result enriched with severity, authorization eligibility, and scope.
type Violation struct {
	ID          string
	Severity    Severity
	Source      ViolationSource
	Description string
	// MatchedRule is the pi-warden.md rule text, if Source is the rules guard.
	MatchedRule string
	// PatternFamily groups related patterns (e.g. "rm" covers rm, git-rm, find-delete).
	PatternFamily string
	Scope         ViolationScope
}

// Authorization is the result of deterministic authorization analysis for one violation.
type Authorization struct {
	// Authorized is true only if action and scope match and nothing negates it.
	Authorized bool
	// ActionMatched: the prompt contains the action verb.
	ActionMatched bool
	// ScopeMatched: the prompt references the affected paths/targets (when there is a scope).
	ScopeMatched bool
	// Negated: the prompt negates the action ("don't", "do not", "never", "skip").
	Negated bool
}

// EscalatedViolation is a violation after escalation rules have been applied.
type EscalatedViolation struct {
	Violation
	EscalatedSeverity Severity
}

var (
	rmFamilyIDs     = setOf("rm", "rm-recursive", "rm-rf", "rm-recursive-dangerous-target", "rm-session-scratch", "find-delete")
	neverAuthorized = setOf("rm-recursive-dangerous-target")
	deleteVerbs     = []string{"delete", "remove", "clean", "tidy", "purge"}
	// actionVerbTable are the action verb families for authorization matching. Keys match a violation's patternFamily or id.
	actionVerbTable = map[string][]string{
		"git-commit":                    {"commit"},
		"git-push":                      {"push"},
		"git-force-push":                {"force push", "force-push"},
		"git-force-with-lease":          {"force push", "force-push", "force-with-lease"},
		"deploy":                        {"deploy", "release", "ship"},
		"rm":                            deleteVerbs,
		"rm-recursive":                  deleteVerbs,
		"rm-rf":                         deleteVerbs,
		"rm-recursive-dangerous-target": deleteVerbs,
		"rm-session-scratch":            deleteVerbs,
		"find-delete":                   deleteVerbs,
		"git-rm":                        deleteVerbs,
		"publish":                       {"publish"},
		"npm-publish":                   {"publish"},
		"merge":                         {"merge"},
		"pr-merge":                      {"merge"},
		"git-reset-hard":                {"reset"},
		"git-clean":                     {"clean"},
		"sql-drop":                      {"drop"},
		"sql-truncate":                  {"truncate"},
		"sql-delete":                    {"delete"},
		"sql-delete-local":              {"delete"},
		"infra-destroy":                 {"destroy"},
		"git-branch-force-delete":       {"delete", "remove"},
	}
	reNegators       = lazyRE(`(?i)\b(?:don'?t|do\s+not|never|skip|avoid|without|no\s+(?:need\s+to\s+)?)\b`)
	reUnscopedTarget = lazyRE("(?s)^(?:.?|[./~*]+|~.*|.*\\$.*)$")
	rePathBefore     = lazyRE("[\\s\"'`(\\[,:;]")
	rePathAfter      = lazyRE("^(?:[\\s/\"'`)\\],:;!]|\\.(?:$|\\s))")
	reRmCommand      = lazyRE(`(?i)(?:^|[\s"'(])rm\s+(.*)$`)
	reRedirectWord   = lazyRE(`^\d*>{1,2}[|&]?`)
	reSplitPipelines = lazyRE(`\n|;|&&|\|\||&`)
)

// isNegated: whether a negator precedes the action verb within 40 characters.
func isNegated(prompt, verb string) bool {
	lower := strings.ToLower(prompt)
	i := strings.Index(lower, verb)
	if i < 0 {
		return false
	}
	// JavaScript measures in UTF-16 units: so does the 40-character window.
	at := utf16Len(lower[:i])
	return reNegators.MatchString(utf16Slice(lower, max(0, at-40), at))
}

func collapseSpace(text string) string { return jsTrim(reSpaceRuns.ReplaceAllString(text, " ")) }

func checkExact(haystack, needle string) bool {
	for from := 0; from <= len(haystack); {
		k := strings.Index(haystack[from:], needle)
		if k < 0 {
			return false
		}
		i := from + k
		from = i + 1
		if i > 0 && !rePathBefore.MatchString(haystack[i-1:i]) {
			continue
		}
		after := haystack[i+len(needle):]
		if after == "" || rePathAfter.MatchString(after) {
			return true
		}
	}
	return false
}

// scopeMatches is deterministic scope matching: exact path or basename. For command-scoped violations (bash with no
// file paths), scope is not required to match: the verb family alone determines authorization. File-scoped violations
// require the prompt to mention the path.
func scopeMatches(prompt string, scope ViolationScope) bool {
	// A deletion with no readable target (`xargs rm -rf`, `find -delete`) can remove anything: the verb is not enough.
	if scope.Segments != nil {
		text := collapseSpace(prompt)
		for _, seg := range scope.Segments {
			if !strings.Contains(text, collapseSpace(seg)) {
				return false
			}
		}
		return true
	}
	if len(scope.Paths) == 0 {
		return true // no file paths = verb alone determines authorization
	}
	lower := strings.ToLower(prompt)
	for _, p := range scope.Paths {
		// A root-like target or a one-character one is found in almost any prompt.
		if reUnscopedTarget.MatchString(strings.TrimRight(p, "/")) {
			return false
		}
		lp := strings.ToLower(p)
		if checkExact(lower, lp) {
			continue
		}
		// Trailing slash in path: also match when the prompt omits it ("delete eval/reports" for "eval/reports/").
		if strings.HasSuffix(lp, "/") && len(lp) > 1 && checkExact(lower, lp[:len(lp)-1]) {
			continue
		}
		return false
	}
	return true
}

// isAuthEligible: whether a violation with this severity is authorization-eligible. Hard denies and sensitive-path
// violations are not.
func isAuthEligible(severity Severity) bool {
	return severity != SeverityDeny && severity != SeveritySensitive
}

// authorize is the full authorization check for one violation against the user's prompt. It requires (1) the action
// verb in the prompt, (2) no negation, (3) a scope match. Command-scoped violations match on the verb; path-scoped
// ones require every path to appear exactly.
func authorize(prompt string, v Violation) Authorization {
	family := v.PatternFamily
	if family == "" {
		family = v.ID
	}
	if !isAuthEligible(v.Severity) || neverAuthorized[family] {
		return Authorization{}
	}
	verbs := actionVerbTable[family]
	lower := strings.ToLower(prompt)
	actionMatched := false
	for _, verb := range verbs {
		if strings.Contains(lower, verb) {
			actionMatched = true
		}
	}
	if !actionMatched {
		return Authorization{}
	}
	for _, verb := range verbs {
		if isNegated(prompt, verb) {
			return Authorization{ActionMatched: true, Negated: true}
		}
	}
	scopeMatched := scopeMatches(prompt, v.Scope)
	return Authorization{Authorized: scopeMatched, ActionMatched: true, ScopeMatched: scopeMatched}
}

func rmSegmentTargets(args string) []string {
	words, ok := shellWords(args)
	if !ok {
		// An unclosed quote means rm sits inside a quoted string: read its words as plain text.
		for _, tok := range strings.Fields(args) {
			w := tok
			if strings.HasPrefix(w, `"`) || strings.HasPrefix(w, "'") {
				w = w[1:]
			}
			if strings.HasSuffix(w, `"`) || strings.HasSuffix(w, "'") {
				w = w[:len(w)-1]
			}
			words = append(words, shellWord{word: w, raw: tok})
		}
	}
	var targets []string
	for i := 0; i < len(words); i++ {
		word, raw := words[i].word, words[i].raw
		if m := reRedirectWord.FindString(raw); m != "" {
			if m == raw {
				i++
			}
			continue
		}
		if word != "" && !strings.HasPrefix(word, "-") {
			targets = append(targets, word)
		}
	}
	return targets
}

type rmSegment struct {
	id      string
	hasID   bool
	targets []string
}

func rmSegments(command string) []rmSegment {
	var out []rmSegment
	for _, seg := range splitShell(StripDataText(command).Text) {
		if m := reRmCommand.FindStringSubmatch(seg); m != nil {
			s := rmSegment{targets: rmSegmentTargets(m[1])}
			if hit := classifyRm(seg, "", nil); hit != nil {
				s.id, s.hasID = hit.ID, true
			}
			out = append(out, s)
		}
	}
	return out
}

func findDeleteRule() func(string) bool {
	for _, r := range shellRules {
		if r.ID == "find-delete" {
			return r.Test
		}
	}
	return func(string) bool { return false }
}

func untargetedSegments(hit PatternHit, command string) []string {
	findDelete := findDeleteRule()
	var own []string
	for _, part := range reSplitPipelines.Split(command, -1) {
		pipeline := jsTrim(part)
		if pipeline == "" {
			continue
		}
		if hit.ID == "find-delete" {
			if findDelete(pipeline) {
				own = append(own, pipeline)
			}
			continue
		}
		for _, p := range strings.Split(pipeline, "|") {
			if reRmCommand.MatchString(p) {
				own = append(own, pipeline)
				break
			}
		}
	}
	if len(own) > 0 {
		return own
	}
	return []string{command}
}

func segmentsOfHit(hit PatternHit, hits []PatternHit, segments []rmSegment) []rmSegment {
	if hit.ID == "find-delete" {
		return nil
	}
	if hit.ID == "rm" {
		return segments
	}
	var exact []rmSegment
	for _, s := range segments {
		if s.hasID && s.id == hit.ID {
			exact = append(exact, s)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	claimed := map[string]bool{}
	for _, o := range hits {
		if o.ID != hit.ID {
			claimed[o.ID] = true
		}
	}
	var rest []rmSegment
	for _, s := range segments {
		if s.hasID && !claimed[s.id] {
			rest = append(rest, s)
		}
	}
	return rest
}

// hitViolations are the violations of each hit, in hit order. Only rm-family hits of a shell command yield violations:
// one per target of the segments that produced the hit, so authorization and scope checks are per target (the prompt must
// name each target the user wants to authorize). An rm-family hit with no readable target produces one violation without
// paths, scoped to the command segments the prompt must quote. Other hits are decided by the pattern loop.
func hitViolations(hits []PatternHit, tool string, input map[string]any) [][]Violation {
	command, _ := input["command"].(string)
	if command == "" {
		command, _ = input["code"].(string)
	}
	var segments []rmSegment
	if tool == "bash" && command != "" {
		segments = rmSegments(command)
	}
	out := make([][]Violation, len(hits))
	for i, hit := range hits {
		if tool != "bash" || command == "" || !rmFamilyIDs[hit.ID] {
			continue
		}
		base := Violation{ID: hit.ID, Severity: hit.Severity, Source: SourcePattern, Description: orDefault(hit.Message, hit.Label), PatternFamily: hit.ID}
		var targets []string
		for _, s := range segmentsOfHit(hit, hits, segments) {
			targets = append(targets, s.targets...)
		}
		if len(targets) == 0 {
			// A deletion with no readable target (`xargs rm -rf`, `find -delete`) can remove anything: only a
			// task that quotes its command segment authorizes it.
			v := base
			v.Scope = ViolationScope{Command: command, Tool: tool, Segments: untargetedSegments(hit, command)}
			out[i] = []Violation{v}
			continue
		}
		for ti, t := range targets {
			v := base
			v.Scope = ViolationScope{Paths: []string{t}, Command: command, Tool: tool, TargetIndex: ti, TargetCount: len(targets)}
			out[i] = append(out[i], v)
		}
	}
	return out
}

// patternHitsToViolations converts hits to violations with scope (hitViolations, flattened).
func patternHitsToViolations(hits []PatternHit, tool string, input map[string]any) []Violation {
	var out []Violation
	for _, vs := range hitViolations(hits, tool, input) {
		out = append(out, vs...)
	}
	return out
}
