package warden

import (
	"strings"
)

// Authorization (src/guard.ts): a deterministic reading of the user's prompt against the deletions a command
// would make. Only rm-family hits of a bash command yield violations; every other hit is decided by the
// pattern loop. A prompt authorizes a hit when it names the action (delete, remove, clean...), does not
// negate it, and names every target. Nothing authorizes a recursive rm of `/`, `~`, `$HOME`, a parent, or a
// path outside the project.

type violationScope struct {
	paths []string
	// segments are the command pipelines the prompt must quote when the deletion has no readable target.
	segments []string
}

type violation struct {
	id       string
	severity Severity
	scope    violationScope
}

var (
	rmFamilyIDs      = setOf("rm", "rm-recursive", "rm-rf", "rm-recursive-dangerous-target", "rm-session-scratch", "find-delete")
	neverAuthorized  = setOf("rm-recursive-dangerous-target")
	deleteVerbs      = []string{"delete", "remove", "clean", "tidy", "purge"}
	reNegators       = lazyRE(`(?i)\b(?:don'?t|do\s+not|never|skip|avoid|without|no\s+(?:need\s+to\s+)?)\b`)
	reUnscopedTarget = lazyRE("(?s)^(?:.?|[./~*]+|~.*|.*\\$.*)$")
	rePathBefore     = lazyRE("[\\s\"'`(\\[,:;]")
	rePathAfter      = lazyRE("^(?:[\\s/\"'`)\\],:;!]|\\.(?:$|\\s))")
	reRmCommand      = lazyRE(`(?i)(?:^|[\s"'(])rm\s+(.*)$`)
	reRedirectWord   = lazyRE(`^\d*>{1,2}[|&]?`)
	reSplitPipelines = lazyRE(`\n|;|&&|\|\||&`)
)

func actionVerbs(id string) []string {
	if rmFamilyIDs[id] {
		return deleteVerbs
	}
	return nil
}

func isNegated(prompt, verb string) bool {
	lower := strings.ToLower(prompt)
	i := strings.Index(lower, verb)
	if i < 0 {
		return false
	}
	start := max(0, i-40)
	return reNegators.MatchString(lower[start:i])
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

func scopeMatches(prompt string, scope violationScope) bool {
	if scope.segments != nil {
		text := collapseSpace(prompt)
		for _, seg := range scope.segments {
			if !strings.Contains(text, collapseSpace(seg)) {
				return false
			}
		}
		return true
	}
	if len(scope.paths) == 0 {
		return true
	}
	lower := strings.ToLower(prompt)
	for _, p := range scope.paths {
		// A root-like target or a one-character one is found in almost any prompt.
		if reUnscopedTarget.MatchString(strings.TrimRight(p, "/")) {
			return false
		}
		lp := strings.ToLower(p)
		if checkExact(lower, lp) {
			continue
		}
		if strings.HasSuffix(lp, "/") && len(lp) > 1 && checkExact(lower, lp[:len(lp)-1]) {
			continue
		}
		return false
	}
	return true
}

func authorize(prompt string, v violation) bool {
	if v.severity == SeverityDeny || v.severity == SeveritySensitive || neverAuthorized[v.id] {
		return false
	}
	verbs := actionVerbs(v.id)
	lower := strings.ToLower(prompt)
	matched := false
	for _, verb := range verbs {
		if strings.Contains(lower, verb) {
			matched = true
		}
	}
	if !matched {
		return false
	}
	for _, verb := range verbs {
		if isNegated(prompt, verb) {
			return false
		}
	}
	return scopeMatches(prompt, v.scope)
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
			if hit := classifyRm(seg, ""); hit != nil {
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

// hitViolations are the violations of each hit, in hit order.
func hitViolations(hits []PatternHit, tool string, input map[string]any) [][]violation {
	command, _ := input["command"].(string)
	if command == "" {
		command, _ = input["code"].(string)
	}
	var segments []rmSegment
	if tool == "bash" && command != "" {
		segments = rmSegments(command)
	}
	out := make([][]violation, len(hits))
	for i, hit := range hits {
		if tool != "bash" || command == "" || !rmFamilyIDs[hit.ID] {
			continue
		}
		var targets []string
		for _, s := range segmentsOfHit(hit, hits, segments) {
			targets = append(targets, s.targets...)
		}
		if len(targets) == 0 {
			// A deletion with no readable target (`xargs rm -rf`, `find -delete`) can remove anything: only a
			// task that quotes its command segment authorizes it.
			out[i] = []violation{{id: hit.ID, severity: hit.Severity, scope: violationScope{segments: untargetedSegments(hit, command)}}}
			continue
		}
		for _, t := range targets {
			out[i] = append(out[i], violation{id: hit.ID, severity: hit.Severity, scope: violationScope{paths: []string{t}}})
		}
	}
	return out
}
