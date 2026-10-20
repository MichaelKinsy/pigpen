package warden

import (
	"strings"
	"unicode/utf8"
)

// Preference detection (prefs.ts isHumanTyped ... extractPermissions, tokens, jaccard, classifyClause). Standing
// preferences are corrections the user repeated in earlier sessions; this file reads one message. The upstream
// patterns that used lookahead, lookbehind or a backreference are written out here, each beside the pattern it
// replaces.

const (
	// clauseChars is CLAUSE_CHARS: a clause is cut to this many UTF-16 units.
	clauseChars = 160
	// humanMaxChars: longer user messages are pasted orders, reports, or logs; a correction typed by hand is short.
	humanMaxChars = 2000
)

var (
	reHeadingLine = prefsRE("m", `^#{1,6}\s`)
	reUpperLabel  = prefsRE("", `\b[A-Z]{2,}:\s`)
	reDotSep      = prefsRE("", `\s·\s`)
	// SENDER_LINE: "From Lead agent:" or "Heads up from the Planner:".
	reSenderLine = prefsRE("", `^[^\n]{0,40}\b[Ff]rom (?:the )?[A-Z][\w-]*(?: [\w-]+)?:`)
	// ON_BEHALF: "Added by the owner:", "Owner change:".
	reOnBehalf = prefsRE("im", `\b(?:by|from) the owner\b|^owner\b[^\n:]{0,30}:`)
	// HEADER_FIELD: `PROJECT: …` / `BRANCH: …` field lines.
	reHeaderField = prefsRE("m", `^\W*[A-Z][A-Z']{2,}(?: [A-Z']+)*:\s`)
)

// hasPairedTag is `/<([a-z][\w-]*)[^>]*>[\s\S]*<\/\1>/i`: a tag, then somewhere after its first `>` the closing tag
// of the same name. The name may be any prefix of the name characters (the engine backtracks into it), so every
// prefix is tried; `[^>]*` reaches the first `>` after the `<` whatever the prefix is.
func hasPairedTag(text string) bool {
	for i := 0; i < len(text); i++ {
		if text[i] != '<' {
			continue
		}
		j := i + 1
		if j >= len(text) || !isASCIILetter(text[j]) {
			continue
		}
		end := j
		for end < len(text) && (isASCIILetter(text[end]) || text[end] >= '0' && text[end] <= '9' || text[end] == '_' || text[end] == '-') {
			end++
		}
		gt := strings.IndexByte(text[end:], '>')
		if gt < 0 {
			// No `>` after the name: no longer name prefix finds one either, and a later `<` has the same fate.
			// A `>` can sit inside the name prefix? It is not a name character, so none.
			continue
		}
		after := asciiLower(text[end+gt+1:])
		for l := 1; l <= end-j; l++ {
			if strings.Contains(after, "</"+asciiLower(text[j:j+l])+">") {
				return true
			}
		}
	}
	return false
}

func isASCIILetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// isHumanTyped: a message a person typed, not one relayed into the prompt (prefs.ts isHumanTyped).
func isHumanTyped(text string) bool {
	trimmed := jsTrim(text)
	if trimmed == "" || utf16Len(trimmed) > humanMaxChars {
		return false
	}
	if strings.HasPrefix(trimmed, "<") {
		return false
	}
	if reHeadingLine.MatchString(trimmed) {
		return false
	}
	if hasPairedTag(trimmed) {
		return false
	}
	first, _, _ := strings.Cut(trimmed, "\n")
	if reUpperLabel.MatchString(first) || reDotSep.MatchString(first) {
		return false
	}
	if reSenderLine.MatchString(first) || reOnBehalf.MatchString(trimmed) {
		return false
	}
	return len(reHeaderField.FindAllStringIndex(trimmed, -1)) < 2
}

var (
	reQuestionMark = prefsRE("", `\?\s*$`)
	reQuestionWord = prefsRE("i", `^(?:why|how|what|when|where|who|which|can|could|would|should|will|is|are|did|does|do\s+(?:you|we|i|they))\b`)
)

// isQuestion: a sentence that ends with `?` or starts with a question word is skipped.
func isQuestion(sentence string) bool {
	return reQuestionMark.MatchString(sentence) || reQuestionWord.MatchString(sentence)
}

const (
	apostrophe = `['’]`
	// lead starts an imperative: at the start of a sentence, after a separator, or after a joining word.
	prefLead = `(?:^|[,:;–—-]\s*|\b(?:and|but|so|just|also|then|again|ok|okay|pls|please|btw)\s+)`
)

var (
	// IMPERATIVE is `LEAD((?:please\s+)?(?:don't|do not|never|stop(?=\s+\w+ing\b))|(?:please\s+)?always)\s+(.+)$`.
	// The lookahead after `stop` becomes a second alternative whose rest starts with the `\w+ing` word; the groups
	// are: 1/2 negative, 3/4 stop, 5/6 always.
	reImperative = prefsRE("i", prefLead+`(?:((?:please\s+)?(?:don`+apostrophe+`?t|do\s+not|never))\s+(.+)|((?:please\s+)?stop)\s+(\w+ing\b.*)|((?:please\s+)?always)\s+(.+))$`)
	// The part of STANDING up to and including the trigger word; the tail (with its lookahead) is standingTail.
	reStandingHead = prefsRE("i", prefLead+`(?:next\s+time|from\s+now\s+on)\b`)
	// REPEATED: `\bi\s+(?:said|told\s+you)\b[,:]?\s+(?:to\s+(.+)|((?:NEG|always)\s+.+))$`, with `stop(?=\s+\w+ing\b)`
	// written as an alternative: groups 1 is "to …", group 2 the instruction.
	reRepeated = prefsRE("i", `\bi\s+(?:said|told\s+you)\b[,:]?\s+(?:to\s+(.+)|((?:(?:please\s+)?(?:don`+apostrophe+`?t|do\s+not|never)\s+.+|(?:please\s+)?stop\s+\w+ing\b.*|always\s+.+)))$`)
	// NOT_A_PREFERENCE, second alternative: `^(?!always\b)(?:[a-z]+[^s\W]s|[a-z]+[^e\W]ed)\b`.
	reNotPrefWord  = prefsRE("i", `^(?:worry|mind|panic|like|know|think|see|care|get|understand|remember|want|need|have|feel|recall)\b`)
	reNotPrefPast  = prefsRE("i", `^(?:[a-z]+[^s\W]s|[a-z]+[^e\W]ed)\b`)
	reStartsAlways = prefsRE("i", `^always\b`)
	reStandingStop = prefsRE("i", `^(?:would|will|should|could|might|may|is|are|was|were)\b`)
	// TEMPORARY is `\b(?:yet|now(?!\s+on\b)|today|this\s+time|at\s+the\s+moment|until|this\s+PR|this\s+branch|for\s+this\b(?!\s+(?:project|repo|repository|codebase)\b))\b`:
	// the alternatives without a lookahead here, the two with one in temporary.
	reTemporaryRest = prefsRE("i", `\b(?:yet|today|this\s+time|at\s+the\s+moment|until|this\s+PR|this\s+branch)\b`)
	reTemporaryNow  = prefsRE("i", `\bnow\b`)
	reTemporaryFor  = prefsRE("i", `\bfor\s+this\b`)
)

// notAPreference is NOT_A_PREFERENCE: "don't worry", "don't like", and a third-person or past form after the trigger.
func notAPreference(rest string) bool {
	if reNotPrefWord.MatchString(rest) {
		return true
	}
	return !reStartsAlways.MatchString(rest) && reNotPrefPast.MatchString(rest)
}

// temporary is TEMPORARY, whose two negative lookaheads are written out: `now` counts unless `\s+on\b` follows it,
// and `for this` counts unless a project word follows. Every occurrence is tested, as the engine tries each one.
func temporary(clause string) bool {
	if reTemporaryRest.MatchString(clause) {
		return true
	}
	for _, loc := range reTemporaryNow.FindAllStringIndex(clause, -1) {
		if !reOnAfter.MatchString(clause[loc[1]:]) {
			return true
		}
	}
	for _, loc := range reTemporaryFor.FindAllStringIndex(clause, -1) {
		if !reProjectAfter.MatchString(clause[loc[1]:]) {
			return true
		}
	}
	return false
}

var (
	reOnAfter      = prefsRE("i", `^\s+on\b`)
	reProjectAfter = prefsRE("i", `^\s+(?:project|repo|repository|codebase)\b`)
)

// substantive: two content words at least, and not a temporary hold.
func substantive(clause string) bool {
	if temporary(clause) {
		return false
	}
	n := 0
	for _, word := range tokens(clause) {
		if word != "not" {
			n++
		}
	}
	return n >= 2
}

// jsSentences is `text.split(/(?<=[.!?])\s+|\n+|;\s*/).map(trim).filter(Boolean)`. The lookbehind reads the character
// before the match position, even when an earlier separator consumed it.
func jsSentences(text string) []string {
	var parts []string
	p, q := 0, 0
	for q < len(text) {
		end := -1
		switch {
		case q > 0 && (text[q-1] == '.' || text[q-1] == '!' || text[q-1] == '?') && startsWithSpace(text[q:]):
			end = q + spaceRun(text[q:])
		case text[q] == '\n':
			end = q
			for end < len(text) && text[end] == '\n' {
				end++
			}
		case text[q] == ';':
			end = q + 1 + spaceRun(text[q+1:])
		}
		if end < 0 || end == p {
			_, size := utf8.DecodeRuneInString(text[q:])
			q += size
			continue
		}
		parts = append(parts, text[p:q])
		p, q = end, end
	}
	parts = append(parts, text[p:])
	var out []string
	for _, part := range parts {
		if t := jsTrim(part); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func startsWithSpace(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return s != "" && isJSSpace(r)
}

// spaceRun is the byte length of the JavaScript white space at the start of s.
func spaceRun(s string) int {
	n := 0
	for n < len(s) {
		r, size := utf8.DecodeRuneInString(s[n:])
		if !isJSSpace(r) {
			break
		}
		n += size
	}
	return n
}

// tidy: whitespace collapsed, trailing punctuation dropped, redacted, capped at CLAUSE_CHARS units, first letter up.
func tidy(clause string) string {
	collapsed := jsTrim(jsCollapseSpace(clause))
	collapsed = strings.TrimRightFunc(collapsed, func(r rune) bool {
		return isJSSpace(r) || r == '.' || r == ',' || r == ';' || r == ':' || r == '!'
	})
	cleaned := Redact(collapsed)
	capped := cleaned
	if utf16Len(cleaned) > clauseChars {
		capped = jsTrimEnd(utf16Slice(cleaned, 0, clauseChars-1)) + "…"
	}
	return jsUpperFirst(capped)
}

// wsThenClause is `\s+(?!REJECT)(.+)$` from position p: the white space is taken whole first and given back one
// character at a time (a character of it then starts the clause, which a lookahead on a word never rejects). It
// returns where the clause starts. reject tests the text after the white space.
func wsThenClause(s string, p int, reject func(rest string) bool) (int, bool) {
	n := spaceRun(s[p:])
	if n == 0 {
		return 0, false
	}
	accept := func(start int, lookahead bool) bool {
		rest := s[start:]
		if rest == "" || lookahead && reject(rest) {
			return false
		}
		for _, r := range rest {
			if r == '\n' || r == '\r' || r == '\u2028' || r == '\u2029' {
				return false
			}
		}
		return true
	}
	end := p + n
	if accept(end, true) {
		return end, true
	}
	for e := end; e > p+1; {
		_, size := utf8.DecodeLastRuneInString(s[p:e])
		e -= size
		if accept(e, false) {
			return e, true
		}
	}
	return 0, false
}

// standingTail finishes STANDING after the trigger word that ended at pos:
// `[,:]?(?:\s+too\b)?[,:]?\s+(?!(?:would|will|…)\b)(.+)$`. It tries the engine's choices in the engine's order
// (consume an optional piece first, then skip it; take all the white space, then give it back one character at a
// time) and returns where the clause starts.
func standingTail(s string, pos int) (int, bool) {
	// `[,:]?` consumes the character when it is there; skipping it is the second choice.
	punct := func(p int) []int {
		if p < len(s) && (s[p] == ',' || s[p] == ':') {
			return []int{p + 1, p}
		}
		return []int{p}
	}
	for _, p1 := range punct(pos) {
		var choices []int
		// `(?:\s+too\b)?`: consumed when it is there, then skipped.
		if n := spaceRun(s[p1:]); n > 0 {
			rest := s[p1+n:]
			if len(rest) >= 3 && strings.EqualFold(rest[:3], "too") && !wordAt(rest, 3) {
				choices = append(choices, p1+n+3)
			}
		}
		choices = append(choices, p1)
		for _, p2 := range choices {
			for _, p3 := range punct(p2) {
				if start, ok := wsThenClause(s, p3, reStandingStop.MatchString); ok {
					return start, true
				}
			}
		}
	}
	return 0, false
}

// wordAt reports whether s[i] is a word character (`\w`); false at the end of s.
func wordAt(s string, i int) bool { return i < len(s) && isWordByte(s[i]) }

// standingClause is `STANDING.exec(sentence)?.[1]`.
func standingClause(sentence string) (string, bool) {
	for _, loc := range reStandingHead.FindAllStringIndex(sentence, -1) {
		if start, ok := standingTail(sentence, loc[1]); ok {
			return sentence[start:], true
		}
	}
	return "", false
}

// standingClauses: the clauses in the standing forms (`don't`, `never`, `always`, `stop`, `from now on`,
// `next time`, `I told you`).
func standingClauses(text string) []string {
	if !isHumanTyped(text) {
		return nil
	}
	var found []string
	for _, sentence := range jsSentences(text) {
		if isQuestion(sentence) {
			continue
		}
		if m := reImperative.FindStringSubmatch(sentence); m != nil {
			neg, rest := m[1], m[2]
			switch {
			case m[3] != "":
				neg, rest = m[3], m[4]
			case m[5] != "":
				neg, rest = m[5], m[6]
			}
			if !notAPreference(rest) {
				found = append(found, reLeadingPlease.ReplaceAllString(neg, "")+" "+rest)
			}
			continue
		}
		var clause string
		if m := reRepeated.FindStringSubmatch(sentence); m != nil {
			clause = m[1]
			if clause == "" {
				clause = m[2]
			}
		} else {
			clause, _ = standingClause(sentence)
		}
		if clause != "" {
			found = append(found, clause)
		}
	}
	return found
}

var reLeadingPlease = prefsRE("i", `^please\s+`)

// extractPreferences: the preference clauses in one user message, redacted, each at most CLAUSE_CHARS units.
func extractPreferences(text string) []string {
	var out []string
	for _, clause := range standingClauses(text) {
		if substantive(clause) {
			out = append(out, tidy(clause))
		}
	}
	return out
}

var (
	// PERMISSION: "Do use subagents here", "feel free to push", "you can skip it". Its `do\s+(?!not\b|you\b|…)` alternative
	// has a negative lookahead and is handled in permissionClause; the other alternatives are plain.
	rePermissionPlain = prefsRE("i", prefLead+`(?:please\s+)?(?:feel\s+free\s+to\s+|you\s+(?:can|may)\s+(?:now\s+)?|it`+apostrophe+`?s\s+(?:fine|ok|okay)\s+to\s+)(.+)$`)
	rePermissionDo    = prefsRE("i", prefLead+`(?:please\s+)?do`)
	rePermissionNot   = prefsRE("i", `^(?:not|you|we|i|they|it|that|this|the|a|an|so|some|any|what|as)\b`)
)

// permissionClause is `PERMISSION.exec(sentence)?.[1]`: the match that starts first wins, the `do` alternative
// first on a tie.
func permissionClause(sentence string) (string, bool) {
	plainStart, plainClause, havePlain := -1, "", false
	if loc := rePermissionPlain.FindStringSubmatchIndex(sentence); loc != nil {
		plainStart, plainClause, havePlain = loc[0], sentence[loc[2]:loc[3]], true
	}
	for _, loc := range rePermissionDo.FindAllStringIndex(sentence, -1) {
		if havePlain && loc[0] > plainStart {
			break
		}
		if start, ok := wsThenClause(sentence, loc[1], rePermissionNot.MatchString); ok {
			return sentence[start:], true
		}
	}
	return plainClause, havePlain
}

// extractPermissions: permissions in one user message. They only contradict an earlier preference; none of them
// is listed or injected.
func extractPermissions(text string) []string {
	if !isHumanTyped(text) {
		return nil
	}
	var found []string
	for _, sentence := range jsSentences(text) {
		if isQuestion(sentence) {
			continue
		}
		if clause, ok := permissionClause(sentence); ok && len(tokens(clause)) > 0 {
			found = append(found, tidy(clause))
		}
	}
	return found
}
