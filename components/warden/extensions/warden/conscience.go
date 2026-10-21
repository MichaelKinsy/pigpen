package warden

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// The conscience (src/conscience.ts): a broad-consideration coach that, before the agent acts, asks the judge which
// skill or tool of the session would serve the request, and recommends it (or, in load mode, supplies the skill's
// text). assess is a pure function of its inputs and the judge's answers; everything stateful (generations,
// delivery, the activation gate) is in conscience_state.go.

// Dispositions the shared question can answer.
const (
	DispositionAdvance      = "advance"
	DispositionAwaitingUser = "awaiting_user"
	DispositionNoGap        = "no_gap"
	DispositionUnclear      = "unclear"
)

// Skip reasons: why an assessment selected nothing.
const (
	SkipDisabled           = "disabled"
	SkipNoConsent          = "no_consent"
	SkipBadBackend         = "bad_backend"
	SkipNoKey              = "no_key"
	SkipKeyRejected        = "key_rejected"
	SkipNoMatch            = "no_match"
	SkipAlreadySupplied    = "already_supplied"
	SkipAwaitingUser       = "awaiting_user"
	SkipTimeout            = "timeout"
	SkipError              = "error"
	SkipStale              = "stale"
	SkipCatalogUnavailable = "catalog_unavailable"
	SkipCatalogLimit       = "catalog_limit"
	SkipMetadataUnsafe     = "metadata_unsafe"
	SkipBudget             = "budget"
	SkipExplicitSkill      = "explicit_skill"
	SkipOriginUnknown      = "origin_unknown"
	SkipBelowThreshold     = "below_threshold"
)

// Skill is the slice of a Pi skill the conscience reads (the host's SystemPromptSkill).
type Skill struct {
	Name                   string
	Description            string
	FilePath               string
	DisableModelInvocation bool
}

// CapabilityTool is a tool of the session as getAllTools reports it.
type CapabilityTool struct {
	Name        string
	Description string
}

// Candidate is one skill or tool offered to the judge.
type Candidate struct {
	Kind        string // "skill" or "tool"
	ID          string
	Description string // sanitized: no absolute paths, URLs or credentials
	Skill       *Skill
	Role        string
	IndexEntry  *IndexEntry
}

// AssessmentResult is the outcome of one assessment.
type AssessmentResult struct {
	Disposition  string
	Selected     *Candidate
	Usefulness   float64 // P(directly useful) + P(prerequisite)
	PAdvance     float64
	QuestionHash string
	ElapsedMs    int
	SkipReason   string
	// ErrorCategory is set with SkipError: timeout, network, configuration, auth or other.
	ErrorCategory string
	RequestCount  int
}

// scoreLevels are the usefulness rubric, lowest first (SCORE_LEVELS).
var scoreLevels = []string{
	"No useful contribution to the current request, or conflicts with the supplied constraints.",
	"Related to the subject, but already covered, premature, or unable to resolve the current need.",
	"Addresses a concrete unmet need at the current step without displacing the active workflow.",
	"Supplies a missing prerequisite or directly applicable documented method needed for the current step.",
}

const (
	conscienceMaxEligible   = 256  // eligible entries per category
	conscienceMaxDescBytes  = 8192 // UTF-8 bytes of one candidate description
	conscienceMaxQuestions  = 32   // including the shared disposition question
	conscienceMaxInFlight   = 4
	conscienceDispositionID = "conscience_disposition"
)

// ---------------------------------------------------------------------------
// The description sanitizer (spec section 6): no absolute paths, URLs or credentials reach the judge.

// sanitizeDescription strips credentials, absolute paths and URLs from a description. It is a hand-written scanner
// with the semantics of the original's chain of regular expressions (kept as the reference in the tests): each step
// is skipped unless a literal it needs is in the text, so the usual description costs a few substring searches
// where the regular expressions cost a backtracking pass each.
func sanitizeDescription(text string) string {
	result := text
	result = redactPrefixRun(result, []string{"ghp_", "gho_", "ghu_", "ghs_", "ghr_"}, 4, isAlnum, 20)
	result = redactPrefixRun(result, []string{"sk-"}, 3, isAlnumUnderscoreDash, 16)
	result = redactAKIA(result)
	result = redactXox(result)
	result = redactPrefixRun(result, []string{"AIza"}, 4, isAlnumUnderscoreDash, 30)
	result = redactJWT(result)
	result = redactKeywordAssignments(result)
	result = replaceAbsolutePaths(result)
	result = replacePrefixedPaths(result)
	result = replaceURLs(result)
	return jsTrim(result)
}

func isAlnum(c byte) bool               { return isWordByte(c) && c != '_' }
func isAlnumUnderscoreDash(c byte) bool { return isWordByte(c) || c == '-' }
func isPathByte(c byte) bool            { return isWordByte(c) || c == '.' || c == '~' || c == '-' || c == '/' }
func isAssignValueByte(c byte) bool     { return isWordByte(c) || c == '-' || c == '.' || c == '/' }

// boundaryBefore is `\b` before a word character at i.
func boundaryBefore(s string, i int) bool { return i == 0 || !isWordByte(s[i-1]) }

// spliced builds a text from replaced spans: the pieces between them are copied only when there is a span.
type spliced struct {
	b    strings.Builder
	last int
	any  bool
}

func (p *spliced) replace(s string, from, to int, with string) {
	p.b.WriteString(s[p.last:from])
	p.b.WriteString(with)
	p.last, p.any = to, true
}

func (p *spliced) result(s string) string {
	if !p.any {
		return s
	}
	p.b.WriteString(s[p.last:])
	return p.b.String()
}

// redactPrefixRun is `\b(?:prefixes)RUN{min,}` replaced by [credential]: a prefix at a word boundary and a run of at
// least min bytes of the class after the first skip bytes. A prefix is found by substring search, so a text without
// one costs a search per prefix.
func redactPrefixRun(s string, prefixes []string, skip int, class func(byte) bool, min int) string {
	var p spliced
	for pos := 0; pos < len(s); {
		hit := -1
		for _, pre := range prefixes {
			if k := strings.Index(s[pos:], pre); k >= 0 && (hit < 0 || pos+k < hit) {
				hit = pos + k
			}
		}
		if hit < 0 {
			break
		}
		end := hit + skip
		for end < len(s) && class(s[end]) {
			end++
		}
		if boundaryBefore(s, hit) && end-(hit+skip) >= min {
			p.replace(s, hit, end, "[credential]")
			pos = end
			continue
		}
		pos = hit + 1
	}
	return p.result(s)
}

// redactAKIA is `\bAKIA[0-9A-Z]{16}\b`.
func redactAKIA(s string) string {
	var p spliced
	for pos := 0; pos < len(s); {
		k := strings.Index(s[pos:], "AKIA")
		if k < 0 {
			break
		}
		i := pos + k
		pos = i + 1
		if i+20 > len(s) || !boundaryBefore(s, i) {
			continue
		}
		ok := true
		for q := i + 4; q < i+20; q++ {
			if c := s[q]; !(c >= '0' && c <= '9' || c >= 'A' && c <= 'Z') {
				ok = false
				break
			}
		}
		if ok && (i+20 == len(s) || !isWordByte(s[i+20])) {
			p.replace(s, i, i+20, "[credential]")
			pos = i + 20
		}
	}
	return p.result(s)
}

// redactXox is `\bxox[abprs]-[A-Za-z0-9-]{10,}`.
func redactXox(s string) string {
	var p spliced
	for pos := 0; pos < len(s); {
		k := strings.Index(s[pos:], "xox")
		if k < 0 {
			break
		}
		i := pos + k
		pos = i + 1
		if i+5 > len(s) || !boundaryBefore(s, i) || strings.IndexByte("abprs", s[i+3]) < 0 || s[i+4] != '-' {
			continue
		}
		end := i + 5
		for end < len(s) && (isAlnum(s[end]) || s[end] == '-') {
			end++
		}
		if end-(i+5) >= 10 {
			p.replace(s, i, end, "[credential]")
			pos = end
		}
	}
	return p.result(s)
}

// redactJWT is `\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`.
func redactJWT(s string) string {
	run := func(from int) int {
		end := from
		for end < len(s) && isAlnumUnderscoreDash(s[end]) {
			end++
		}
		return end
	}
	var p spliced
	for pos := 0; pos < len(s); {
		k := strings.Index(s[pos:], "eyJ")
		if k < 0 {
			break
		}
		i := pos + k
		pos = i + 1
		if !boundaryBefore(s, i) {
			continue
		}
		e1 := run(i + 3)
		if e1-(i+3) < 10 || e1 >= len(s) || s[e1] != '.' {
			continue
		}
		e2 := run(e1 + 1)
		if e2-(e1+1) < 10 || e2 >= len(s) || s[e2] != '.' {
			continue
		}
		if e3 := run(e2 + 1); e3-(e2+1) >= 10 {
			p.replace(s, i, e3, "[credential]")
			pos = e3
		}
	}
	return p.result(s)
}

// containsFold reports whether s contains the lower-case ASCII word k in any case.
func containsFold(s, k string) bool {
	for i := 0; i+len(k) <= len(s); i++ {
		if (s[i]|0x20) == k[0] && strings.EqualFold(s[i:i+len(k)], k) {
			return true
		}
	}
	return false
}

// spaceAt reports whether a JavaScript white space character starts at i, and its width.
func spaceAt(s string, i int) (bool, int) {
	c := s[i]
	if c < utf8.RuneSelf {
		return c == ' ' || c >= '\t' && c <= '\r', 1
	}
	r, w := utf8.DecodeRuneInString(s[i:])
	return isJSSpace(r), w
}

func skipSpaces(s string, i int) int {
	for i < len(s) {
		ok, w := spaceAt(s, i)
		if !ok {
			break
		}
		i += w
	}
	return i
}

// spaceBefore reports whether a JavaScript white space character ends at i, and its width.
func spaceBefore(s string, i int) (bool, int) {
	if i == 0 {
		return false, 0
	}
	c := s[i-1]
	if c < utf8.RuneSelf {
		return c == ' ' || c >= '\t' && c <= '\r', 1
	}
	r, w := utf8.DecodeLastRuneInString(s[:i])
	return isJSSpace(r), w
}

// keywordEndingAt reports whether TOKEN, SECRET, KEY or PASSWORD (any case, ASCII) ends at i, and where it starts.
func keywordEndingAt(s string, i int) (int, bool) {
	for _, k := range [...]string{"token", "secret", "password", "key"} {
		if i >= len(k) && strings.EqualFold(s[i-len(k):i], k) {
			return i - len(k), true
		}
	}
	return 0, false
}

// redactKeywordAssignments is `(?i)(?:TOKEN|SECRET|KEY|PASSWORD)\s*[=:]\s*['"]?[\w\-./]{8,}`. Each match holds one
// `=` or `:`, so the separators are found first and the keyword looked for before each.
func redactKeywordAssignments(s string) string {
	var p spliced
	for pos := 0; pos < len(s); {
		k := indexAny2(s[pos:], '=', ':')
		if k < 0 {
			break
		}
		sep := pos + k
		pos = sep + 1
		// The keyword ends before the separator, with white space between.
		kEnd := sep
		for {
			ok, w := spaceBefore(s, kEnd)
			if !ok || kEnd-w < p.last {
				break
			}
			kEnd -= w
		}
		start, ok := keywordEndingAt(s, kEnd)
		if !ok || start < p.last {
			continue
		}
		j := skipSpaces(s, sep+1)
		if j < len(s) && (s[j] == '\'' || s[j] == '"') {
			j++
		}
		end := j
		for end < len(s) && isAssignValueByte(s[end]) {
			end++
		}
		if end-j >= 8 {
			p.replace(s, start, end, "[credential]")
			pos = end
		}
	}
	return p.result(s)
}

// indexAny2 is the index of the first a or b in s, or -1.
func indexAny2(s string, a, b byte) int {
	ia, ib := strings.IndexByte(s, a), strings.IndexByte(s, b)
	switch {
	case ia < 0:
		return ib
	case ib < 0 || ia < ib:
		return ia
	}
	return ib
}

// absolutePathAt is `(?:^|\s)/[\w.~\-/]+(?:\s|$)` around the slash at j: the start and end of the match, or -1.
func absolutePathAt(s string, j, notBefore int) (int, int) {
	start := j
	if j > 0 {
		ok, w := spaceBefore(s, j)
		if !ok {
			return -1, -1
		}
		start = j - w
	}
	if start < notBefore {
		return -1, -1
	}
	end := j + 1
	for end < len(s) && isPathByte(s[end]) {
		end++
	}
	if end == j+1 {
		return -1, -1
	}
	if end == len(s) {
		return start, end
	}
	if ok, w := spaceAt(s, end); ok {
		return start, end + w
	}
	return -1, -1
}

// hasAbsolutePath reports whether the text contains an absolute path the pattern above finds.
func hasAbsolutePath(s string) bool {
	for pos := 0; pos < len(s); {
		k := strings.IndexByte(s[pos:], '/')
		if k < 0 {
			return false
		}
		if start, _ := absolutePathAt(s, pos+k, 0); start >= 0 {
			return true
		}
		pos += k + 1
	}
	return false
}

// replaceAbsolutePaths replaces each such path, with the white space either side of it, by one space.
func replaceAbsolutePaths(s string) string {
	var p spliced
	for pos := 0; pos < len(s); {
		k := strings.IndexByte(s[pos:], '/')
		if k < 0 {
			break
		}
		j := pos + k
		if start, end := absolutePathAt(s, j, p.last); start >= 0 {
			p.replace(s, start, end, " ")
			pos = end
			continue
		}
		pos = j + 1
	}
	return p.result(s)
}

// replacePrefixedPaths is `~/[\w.~\-/]+` and then `~\w+/[\w.~\-/]+`, each replaced by [path].
func replacePrefixedPaths(s string) string {
	if strings.IndexByte(s, '~') < 0 {
		return s
	}
	for pass := 0; pass < 2; pass++ {
		var p spliced
		for pos := 0; pos < len(s); {
			k := strings.IndexByte(s[pos:], '~')
			if k < 0 {
				break
			}
			i := pos + k
			j := i + 1
			if pass == 1 {
				for j < len(s) && isWordByte(s[j]) {
					j++
				}
				if j == i+1 {
					pos = i + 1
					continue
				}
			}
			if j < len(s) && s[j] == '/' {
				end := j + 1
				for end < len(s) && isPathByte(s[end]) {
					end++
				}
				if end > j+1 {
					p.replace(s, i, end, "[path]")
					pos = end
					continue
				}
			}
			pos = i + 1
		}
		s = p.result(s)
	}
	return s
}

// replaceURLs is `https?://[\w.~\-/]+` replaced by [url].
func replaceURLs(s string) string {
	var p spliced
	for pos := 0; pos < len(s); {
		k := strings.Index(s[pos:], "://")
		if k < 0 {
			break
		}
		j := pos + k
		start := -1
		switch {
		case j >= 5 && s[j-5:j] == "https" && j-5 >= p.last:
			start = j - 5
		case j >= 4 && s[j-4:j] == "http" && j-4 >= p.last:
			start = j - 4
		}
		end := j + 3
		for end < len(s) && isPathByte(s[end]) {
			end++
		}
		if start >= 0 && end > j+3 {
			p.replace(s, start, end, "[url]")
			pos = end
			continue
		}
		pos = j + 1
	}
	return p.result(s)
}

// ---------------------------------------------------------------------------
// Opaque ids and the questions.

// opaqueID is c1, c2, ...: positional, so the judge sees no candidate name in any question.
func opaqueID(index int) string { return "c" + itoaInt(index+1) }

type batchItem struct {
	opaqueID  string
	candidate Candidate
}

func assignOpaqueIDs(candidates []Candidate) []batchItem {
	out := make([]batchItem, len(candidates))
	for i, c := range candidates {
		out[i] = batchItem{opaqueID(i), c}
	}
	return out
}

const (
	dispositionInstructions = "Disposition of the current request. A message that reports status, shares context, or narrates what the user is doing elsewhere, without asking the agent to do anything, is no_gap; select nothing on it."
	scoreInstructionsFmt    = "Judge candidates.%s against what the user is asking the agent to do now in task, not against the subjects the prompt mentions in passing; a candidate that matches words in the prompt but does not serve the actual request belongs at the lowest level. When the request needs information the repository cannot supply, a research-role candidate serves the request; when the request is about the repository's own code or behaviour, an evidence-role candidate does."
)

var (
	dispositionOnce     sync.Once
	dispositionQuestion Question
	scoreInstructions   sync.Map // opaque id -> instruction text
)

// dispositionChoice is the shared disposition question; it is read-only, so one value serves every request.
func dispositionChoice() Question {
	dispositionOnce.Do(func() {
		dispositionQuestion = Choice(dispositionInstructions, map[string]string{
			"advance":       "A useful next step can be taken now.",
			"awaiting_user": "The assistant has already asked for information and must wait.",
			"no_gap":        "The request is satisfied with no useful missing step.",
			"unclear":       "Insufficient evidence to determine the next step.",
		})
	})
	return dispositionQuestion
}

// scoreInstructionsFor is the score question's text for one opaque id.
func scoreInstructionsFor(id string) string {
	if v, ok := scoreInstructions.Load(id); ok {
		return v.(string)
	}
	text := strings.Replace(scoreInstructionsFmt, "%s", id, 1)
	scoreInstructions.Store(id, text)
	return text
}

// buildBatchQuestions builds the questions for one batch: the shared disposition choice and one score per
// candidate, keyed by opaque id. The task, context and skill lists are state, not question text.
func buildBatchQuestions(batch []batchItem, _ string, _ string, _ []string, _ []string) (Questions, string, map[string]Candidate) {
	questions := make(Questions, len(batch)+1)
	idMap := make(map[string]Candidate, len(batch))
	questions[conscienceDispositionID] = dispositionChoice()
	for _, it := range batch {
		idMap[it.opaqueID] = it.candidate
		questions[it.opaqueID] = Score(scoreInstructionsFor(it.opaqueID), scoreLevels)
	}
	return questions, conscienceDispositionID, idMap
}

// buildState is the judge request's state (spec section 3): the task, the context, the skill lists and the
// candidates by opaque id. The spine, when given, rides beside the task; it is scope context only.
func buildState(task, recentContext string, active, supplied []string, batch []batchItem, spine *TaskSpine) map[string]any {
	candidates := map[string]any{}
	for _, it := range batch {
		c := it.candidate
		if e := c.IndexEntry; e != nil {
			candidates[it.opaqueID] = map[string]any{"kind": c.Kind, "name": c.ID, "lead": e.Lead, "useWhen": orEmpty(e.UseWhen), "examples": orEmpty(e.Examples)}
		} else {
			candidates[it.opaqueID] = map[string]any{"kind": c.Kind, "name": c.ID, "description": c.Description, "unindexed": true}
		}
	}
	state := map[string]any{
		"task":            sanitizeDescription(Redact(task)),
		"context":         sanitizeDescription(Redact(recentContext)),
		"active_skills":   orEmpty(active),
		"supplied_skills": orEmpty(supplied),
		"candidates":      candidates,
	}
	if spine != nil {
		history := []string{}
		for i, turn := range spine.History {
			if i >= spineHistoryTurns {
				break
			}
			history = append(history, utf16Slice(sanitizeDescription(Redact(turn)), 0, spineHistoryLimit))
		}
		state["spine"] = map[string]any{
			"goal":         utf16Slice(sanitizeDescription(Redact(spine.Goal)), 0, spineGoalLimit),
			"task_history": history,
		}
	}
	return state
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// jsonCompact is JSON.stringify: no HTML escaping, keys of a map in sorted order.
func jsonCompact(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return strings.TrimSuffix(buf.String(), "\n")
}

// questionHash is the 16-hex-digit hash of the assessment questions, for the activation gate. Opaque ids are
// positional, so a conscience question set (recognised by its disposition key) is hashed as the disposition
// question plus one canonical candidate question keyed c1: the batch a prompt happens to produce never moves it.
func questionHash(questions Questions) string {
	var ids []string
	if _, ok := questions[conscienceDispositionID]; ok {
		first := ""
		for id := range questions {
			if reCandidateID.MatchString(id) && (first == "" || id < first) {
				first = id
			}
		}
		if first != "" {
			ids = append(ids, first)
		}
		ids = append(ids, conscienceDispositionID)
	} else {
		for id := range questions {
			ids = append(ids, id)
		}
		sort.Strings(ids)
	}
	// The canonical form: keys in sorted order at every level, compact (JSON.stringify of the sorted tree).
	w := jsonOut{b: make([]byte, 0, 1536)}
	w.b = append(w.b, '{')
	for n, id := range ids {
		if n > 0 {
			w.b = append(w.b, ',')
		}
		w.str(id)
		w.b = append(w.b, ':')
		canonicalQuestion(&w, questions[id])
	}
	w.b = append(w.b, '}')
	sum := sha256.Sum256(w.b)
	return hex.EncodeToString(sum[:])[:16]
}

// canonicalQuestion writes {"criteria":...,"instructions":...,"type":...}: the criteria of a score are its levels, those
// of the other kinds an object by label.
func canonicalQuestion(w *jsonOut, q Question) {
	w.b = append(w.b, `{"criteria":`...)
	if q.Type == "score" {
		w.strings(orEmpty(q.Levels), 0)
	} else {
		labels := make([]string, 0, len(q.Criteria))
		for k := range q.Criteria {
			labels = append(labels, k)
		}
		sort.Strings(labels)
		w.b = append(w.b, '{')
		for n, k := range labels {
			if n > 0 {
				w.b = append(w.b, ',')
			}
			w.str(k)
			w.b = append(w.b, ':')
			w.str(q.Criteria[k])
		}
		w.b = append(w.b, '}')
	}
	w.b = append(w.b, `,"instructions":`...)
	w.str(q.Instructions)
	w.b = append(w.b, `,"type":`...)
	w.str(q.Type)
	w.b = append(w.b, '}')
}

// questionHashReference is questionHash written with maps and the generic encoder: the tests hold the writer to it.
func questionHashReference(questions Questions) string {
	canon := map[string]any{}
	add := func(id string, q Question) {
		switch q.Type {
		case "score":
			canon[id] = map[string]any{"type": q.Type, "instructions": q.Instructions, "criteria": orEmpty(q.Levels)}
		default:
			crit := map[string]string{}
			for k, v := range q.Criteria {
				crit[k] = v
			}
			canon[id] = map[string]any{"type": q.Type, "instructions": q.Instructions, "criteria": crit}
		}
	}
	if disp, ok := questions[conscienceDispositionID]; ok {
		add(conscienceDispositionID, disp)
		var first string
		for id := range questions {
			if reCandidateID.MatchString(id) && (first == "" || id < first) {
				first = id
			}
		}
		if first != "" {
			add(first, questions[first])
		}
	} else {
		for id, q := range questions {
			add(id, q)
		}
	}
	sum := sha256.Sum256([]byte(jsonCompact(canon)))
	return hex.EncodeToString(sum[:])[:16]
}

var (
	conscienceHashOnce  sync.Once
	conscienceHashValue string
)

// conscienceQuestionHash is the hash of every conscience question set: questionHash is independent of the batch
// by construction (the disposition question and one canonical candidate question), so it is computed once.
func conscienceQuestionHash() string {
	conscienceHashOnce.Do(func() {
		q, _, _ := buildBatchQuestions([]batchItem{{opaqueID(0), Candidate{}}}, "", "", nil, nil)
		conscienceHashValue = questionHash(q)
	})
	return conscienceHashValue
}

var reCandidateID = lazyRE(`^c\d+$`)

// ---------------------------------------------------------------------------
// Eligibility.

func isExcluded(id string, exclude []string) bool {
	for _, p := range exclude {
		if p == "*" {
			return true
		}
		if strings.HasSuffix(p, "*") && strings.HasPrefix(id, p[:len(p)-1]) {
			return true
		}
		if p == id {
			return true
		}
	}
	return false
}

// destructiveMarkers mark a tool whose recommendation is never worth the risk of the agent calling it on a prompt
// that only mentioned the subject.
var destructiveMarkers = map[string]bool{"delete": true, "drop": true, "destroy": true, "remove": true, "purge": true, "wipe": true, "reset": true, "truncate": true, "kill": true, "force": true, "uninstall": true, "revoke": true, "erase": true, "clear": true}

var (
	reCamel1   = lazyRE(`([a-z0-9])([A-Z])`)
	reCamel2   = lazyRE(`([A-Z]+)([A-Z][a-z])`)
	reNonAlnum = lazyRE(`[^a-z0-9]+`)
)

// words splits camelCase before lowercasing, so `deleteIssue` and `mcp__db__truncateTable` show their verb. ASCII text,
// the usual tool name, is split in one pass; other text takes the regular expressions of the original.
func words(text string) []string {
	for i := 0; i < len(text); i++ {
		if text[i] >= utf8.RuneSelf {
			return wordsRegexp(text)
		}
	}
	var out []string
	start := -1
	flush := func(end int) {
		if start >= 0 {
			out = append(out, jsLower(text[start:end]))
			start = -1
		}
	}
	isLower := func(c byte) bool { return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' }
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case c >= 'A' && c <= 'Z':
			// A capital starts a word after a lower-case letter or digit, and ends a run of capitals before a lower-case letter.
			if start >= 0 && (isLower(text[i-1]) || text[i-1] >= 'A' && text[i-1] <= 'Z' && i+1 < len(text) && text[i+1] >= 'a' && text[i+1] <= 'z') {
				flush(i)
			}
			if start < 0 {
				start = i
			}
		case isLower(c):
			if start < 0 {
				start = i
			}
		default:
			flush(i)
		}
	}
	flush(len(text))
	return out
}

// wordsRegexp is words as the original writes it.
func wordsRegexp(text string) []string {
	text = reCamel1.ReplaceAllString(text, "$1 $2")
	text = reCamel2.ReplaceAllString(text, "$1 $2")
	var out []string
	for _, w := range reNonAlnum.Split(jsLower(text), -1) {
		if w != "" {
			out = append(out, w)
		}
	}
	return out
}

// isDestructiveTool: a marker is a whole word or `_`-separated part of the name, or the first word of a description.
func isDestructiveTool(name string, descriptions ...string) bool {
	for _, w := range words(name) {
		if destructiveMarkers[w] {
			return true
		}
	}
	for _, d := range descriptions {
		if w := words(d); len(w) > 0 && destructiveMarkers[w[0]] {
			return true
		}
	}
	return false
}

// platformBoundTools only run on one platform (Node platform names).
var platformBoundTools = map[string]string{"powershell": "win32", "pwsh": "win32", "cmd": "win32"}

var platformOnlyDescriptions = []struct {
	platform string
	pattern  *lazyRegexp
}{
	{"win32", lazyRE(`(?i)\bwindows[- ]only\b|\bonly (?:on|for) windows\b`)},
	{"darwin", lazyRE(`(?i)\b(?:macos|mac os|os x)[- ]only\b|\bonly (?:on|for) (?:macos|mac os|os x)\b`)},
}

// isPlatformIneligibleTool: the tool name or a description binds it to a platform other than `platform`.
func isPlatformIneligibleTool(name, platform string, descriptions ...string) bool {
	if bound, ok := platformBoundTools[jsLower(name)]; ok && bound != platform {
		return true
	}
	for _, p := range platformOnlyDescriptions {
		if p.platform == platform {
			continue
		}
		for _, d := range descriptions {
			if d != "" && p.pattern.MatchString(d) {
				return true
			}
		}
	}
	return false
}

type eligibleResult struct {
	Candidates                  []Candidate
	SkillOverflow, ToolOverflow bool
}

// eligibleCandidates filters skills and tools by the rules of spec section 3, capped at conscienceMaxEligible per category,
// and attaches the index entry of each candidate that has one.
func eligibleCandidates(skills []Skill, tools []CapabilityTool, cfg ConscienceConfig, active, supplied []string, globalIndex, projectIndex *IndexFile, platform string) eligibleResult {
	var res eligibleResult
	_ = active
	if platform == "" {
		platform = nodePlatform()
	}
	find := func(name, hash string) *IndexEntry {
		if e := findIndexEntry(projectIndex, name, hash); e != nil {
			return e
		}
		return findIndexEntry(globalIndex, name, hash)
	}
	if cfg.Skills.Mode != "off" {
		count := 0
		for i := range skills {
			s := skills[i]
			if isExcluded(s.Name, cfg.Skills.Exclude) || s.DisableModelInvocation || containsString(supplied, s.Name) {
				continue
			}
			if count >= conscienceMaxEligible {
				res.SkillOverflow = true
				break
			}
			hash := "missing"
			if s.FilePath != "" {
				hash = fileContentHash(s.FilePath)
			}
			c := Candidate{Kind: "skill", ID: s.Name, Description: sanitizeDescription(s.Description), Skill: &skills[i]}
			if e := find(s.Name, hash); e != nil {
				c.Role, c.IndexEntry = e.Role, e
			}
			res.Candidates = append(res.Candidates, c)
			count++
		}
	}
	if cfg.Tools.Enabled {
		count := 0
		for _, t := range tools {
			if isExcluded(t.Name, cfg.Tools.Exclude) || containsString(cfg.SkipTools, t.Name) {
				continue
			}
			e := find(t.Name, toolSourceHash(t.Name, t.Description))
			lead := ""
			if e != nil {
				lead = e.Lead
			}
			if isDestructiveTool(t.Name, t.Description, lead) || isPlatformIneligibleTool(t.Name, platform, t.Description, lead) {
				continue
			}
			if count >= conscienceMaxEligible {
				res.ToolOverflow = true
				break
			}
			c := Candidate{Kind: "tool", ID: t.Name, Description: sanitizeDescription(t.Description)}
			if e != nil {
				c.Role, c.IndexEntry = e.Role, e
			}
			res.Candidates = append(res.Candidates, c)
			count++
		}
	}
	return res
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func findIndexEntry(index *IndexFile, name, sourceHash string) *IndexEntry {
	if index == nil {
		return nil
	}
	for i := range index.Entries {
		if index.Entries[i].Name == name && index.Entries[i].SourceHash == sourceHash {
			return &index.Entries[i]
		}
	}
	return nil
}

// capDescription cuts a description to conscienceMaxDescBytes of UTF-8 without splitting a character.
func capDescription(c Candidate) Candidate {
	if len(c.Description) <= conscienceMaxDescBytes {
		return c
	}
	var b strings.Builder
	n := 0
	for _, r := range c.Description {
		size := utf8.RuneLen(r)
		if size < 0 {
			size = 3
		}
		if n+size > conscienceMaxDescBytes-3 {
			break
		}
		b.WriteRune(r)
		n += size
	}
	c.Description = b.String() + "..."
	return c
}

// ---------------------------------------------------------------------------
// Errors.

var (
	reTimeoutMsg = lazyRE(`(?i)timeout|timed out`)
	reAuthMsg    = lazyRE(`(?i)auth|key|credential|401|403`)
	reNetworkMsg = lazyRE(`(?i)network|fetch|connect|ECONNREFUSED|ENOTFOUND`)
)

// classifyConscienceError names a safe category for an assessment error (spec section 6: never exception bodies).
func classifyConscienceError(err error) string {
	var ie *IntegrationError
	if errors.As(err, &ie) {
		switch ie.Code {
		case "timeout":
			return "timeout"
		case "http", "connection", "response", "network":
			return "network"
		case "configuration", "validation":
			return "configuration"
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	msg := err.Error()
	switch {
	case reTimeoutMsg.MatchString(msg):
		return "timeout"
	case reAuthMsg.MatchString(msg):
		return "auth"
	case reNetworkMsg.MatchString(msg):
		return "network"
	}
	return "other"
}

// ---------------------------------------------------------------------------
// The assessment.

// ConscienceDeps are what assess needs beside its arguments.
type ConscienceDeps struct {
	Judge Judge
	// JudgmentsOff is why Judge is nil, as the extension computed it; no_consent when empty.
	JudgmentsOff string
	Config       ConscienceConfig
	// SharedTimeoutMs is the shared timeout; the deadline is the smaller of it and Config.TimeoutMs.
	SharedTimeoutMs int
	Now             func() time.Time
	GlobalIndex     *IndexFile
	ProjectIndex    *IndexFile
	// Platform is the one the agent's tools run on (Node names); the process's when empty.
	Platform string
}

type choiceAnswer struct {
	label    string
	pAdvance float64
}

func parseChoiceAnswer(a Answer) (choiceAnswer, bool) {
	if a.Type != "choice" || a.Choice == "" {
		return choiceAnswer{}, false
	}
	return choiceAnswer{label: a.Choice, pAdvance: a.Probabilities["advance"]}, true
}

type scoreAnswer struct {
	level         int
	probabilities []float64
}

func parseScoreAnswer(a Answer) (scoreAnswer, bool) {
	if a.Type != "score" || a.Probabilities == nil {
		return scoreAnswer{}, false
	}
	p := make([]float64, len(scoreLevels))
	for i := range p {
		p[i] = a.Probabilities[itoaInt(i)]
	}
	return scoreAnswer{level: jsRound(a.Score), probabilities: p}, true
}

type scored struct {
	candidate  Candidate
	usefulness float64
	level      int
}

type chunkWork struct {
	category string
	items    []Candidate
	overflow bool
}

// assess runs one assessment: up to four concurrent judge requests, skills first, under one shared deadline.
// Nothing is delivered here; the caller owns the activation gate.
func assess(ctx context.Context, prompt, recentContext string, skills []Skill, tools []CapabilityTool, active, supplied []string, deps ConscienceDeps, spine *TaskSpine) AssessmentResult {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	start := now()
	cfg := deps.Config
	if !cfg.Enabled {
		return AssessmentResult{Disposition: DispositionNoGap, SkipReason: SkipDisabled}
	}
	if deps.Judge == nil {
		reason := deps.JudgmentsOff
		if reason == "" {
			reason = SkipNoConsent
		}
		return AssessmentResult{Disposition: DispositionNoGap, SkipReason: reason}
	}
	elig := eligibleCandidates(skills, tools, cfg, active, supplied, deps.GlobalIndex, deps.ProjectIndex, deps.Platform)
	if len(elig.Candidates) == 0 {
		return AssessmentResult{Disposition: DispositionNoGap, SkipReason: SkipNoMatch}
	}
	deadline := start.Add(time.Duration(min(cfg.TimeoutMs, deps.SharedTimeoutMs)) * time.Millisecond)

	// Skill chunks are queued first so the slots fill with skill requests before any tool request gets one.
	var queue []chunkWork
	for _, meta := range []struct {
		category string
		overflow bool
	}{{"skill", elig.SkillOverflow}, {"tool", elig.ToolOverflow}} {
		var items []Candidate
		for _, c := range elig.Candidates {
			if c.Kind == meta.category {
				items = append(items, capDescription(c))
			}
		}
		if len(items) == 0 && !meta.overflow {
			continue
		}
		for i := 0; i < len(items); i += conscienceMaxQuestions - 1 {
			queue = append(queue, chunkWork{category: meta.category, items: items[i:min(i+conscienceMaxQuestions-1, len(items))]})
		}
		if len(items) == 0 && meta.overflow {
			queue = append(queue, chunkWork{category: meta.category, overflow: true})
		}
	}

	var mu sync.Mutex
	completed := map[string]bool{}
	failed := map[string]bool{}
	var judgeErr error
	disposition, pAdvance, hash, requestCount := DispositionUnclear, 0.0, "", 0
	var best *scored

	runChunk := func(work chunkWork) {
		if len(work.items) == 0 {
			if work.overflow {
				mu.Lock()
				completed[work.category] = true
				mu.Unlock()
			}
			return
		}
		batch := assignOpaqueIDs(work.items)
		questions, dispositionKey, _ := buildBatchQuestions(batch, prompt, recentContext, active, supplied)
		qh := conscienceQuestionHash()
		state := buildState(prompt, recentContext, active, supplied, batch, spine)
		mu.Lock()
		hash = qh
		mu.Unlock()
		remaining := deadline.Sub(now())
		if remaining <= 0 {
			mu.Lock()
			failed[work.category] = true
			mu.Unlock()
			return
		}
		cctx, cancel := context.WithTimeout(ctx, remaining)
		defer cancel()
		type outcome struct {
			ev  Evaluation
			err error
		}
		done := make(chan outcome, 1)
		go func() {
			ev, err := deps.Judge.Evaluate(cctx, Request{State: state, Questions: questions})
			done <- outcome{ev, err}
		}()
		var out outcome
		select {
		case out = <-done:
		case <-cctx.Done():
			out = outcome{err: &IntegrationError{Code: "timeout", Message: "timeout"}}
		}
		if out.err != nil {
			mu.Lock()
			failed[work.category] = true
			judgeErr = out.err
			mu.Unlock()
			return
		}
		mu.Lock()
		defer mu.Unlock()
		requestCount++
		completed[work.category] = true
		if disposition == DispositionUnclear {
			if a, ok := out.ev.Answers[dispositionKey]; ok {
				if c, ok := parseChoiceAnswer(a); ok {
					disposition, pAdvance = c.label, c.pAdvance
				}
			}
		}
		if disposition != DispositionAdvance {
			return
		}
		for _, it := range batch {
			a, ok := out.ev.Answers[it.opaqueID]
			if !ok {
				continue
			}
			s, ok := parseScoreAnswer(a)
			if !ok {
				continue
			}
			pUseful := s.probabilities[2] + s.probabilities[3]
			c := it.candidate
			if best == nil || pUseful > best.usefulness ||
				(pUseful == best.usefulness && s.level > best.level) ||
				(pUseful == best.usefulness && s.level == best.level &&
					((c.Kind == "skill" && best.candidate.Kind != "skill") || (c.Kind == best.candidate.Kind && c.ID < best.candidate.ID))) {
				best = &scored{candidate: c, usefulness: pUseful, level: s.level}
			}
		}
	}

	slots := make(chan struct{}, conscienceMaxInFlight)
	var wg sync.WaitGroup
	for _, work := range queue {
		slots <- struct{}{}
		wg.Add(1)
		go func(work chunkWork) {
			defer wg.Done()
			defer func() { <-slots }()
			runChunk(work)
		}(work)
	}
	wg.Wait()

	elapsed := int(now().Sub(start).Milliseconds())
	skillUsable := completed["skill"] && !failed["skill"]
	toolUsable := completed["tool"] && !failed["tool"]
	if !skillUsable && !toolUsable && judgeErr != nil {
		return AssessmentResult{Disposition: DispositionUnclear, QuestionHash: hash, ElapsedMs: elapsed, RequestCount: requestCount, SkipReason: SkipError, ErrorCategory: classifyConscienceError(judgeErr)}
	}
	if disposition != DispositionAdvance {
		r := AssessmentResult{Disposition: disposition, PAdvance: pAdvance, QuestionHash: hash, ElapsedMs: elapsed, RequestCount: requestCount}
		if disposition == DispositionAwaitingUser {
			r.SkipReason = SkipAwaitingUser
		}
		return r
	}
	res := AssessmentResult{Disposition: disposition, PAdvance: pAdvance, QuestionHash: hash, ElapsedMs: elapsed, RequestCount: requestCount}
	if best == nil {
		res.SkipReason = SkipNoMatch
		if elig.SkillOverflow {
			res.SkipReason = SkipCatalogLimit
		}
		return res
	}
	if (best.candidate.Kind == "skill" && !skillUsable) || (best.candidate.Kind == "tool" && !toolUsable) {
		res.SkipReason = SkipNoMatch
		return res
	}
	res.Usefulness = best.usefulness
	if best.usefulness < cfg.RecommendThreshold || pAdvance < cfg.AdvanceThreshold {
		res.SkipReason = SkipBelowThreshold
		return res
	}
	if best.candidate.Kind == "skill" && elig.SkillOverflow {
		res.SkipReason = SkipCatalogLimit
		return res
	}
	c := best.candidate
	res.Selected = &c
	return res
}
