package warden

import (
	"context"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Project rules (src/rules.ts): Markdown headings become rules, the judge asks about each write or edit against every
// rule on its own request, and the agent is steered, never held, with the violated rule. Path scoping and
// sensitive-path notes are code only.
//
// Sources, in order: `pi-warden.md` at the project root; else the files in `rules.files`; else, with `rules.fallback`,
// the first of AGENTS.md, CLAUDE.md, README.md as one aggregate rule set. Files are re-read when their mtime or size
// changes.

// RulesFile is the project rules file.
const RulesFile = "pi-warden.md"

// fallbackFiles are the documents the guard falls back to, in order (rules.ts FALLBACK_FILES).
var fallbackFiles = []string{"AGENTS.md", "CLAUDE.md", "README.md"}

const (
	// maxRules is the most questions one request carries: the judge answers at most 32 per request and one is kept for
	// the edit locator.
	maxRules              = 31
	rulesContentLimit     = 6000
	editTextLimit         = 1500
	editContextLines      = 20
	maxEdits              = 6
	ruleBodyLimit         = 400
	steerBodyLimit        = 200
	ruleIDLimit           = 64
	ruleShapeContextLines = 15
)

// Rule outcomes (the choice labels of a rule question).
const (
	outcomeCompliant   = "compliant"
	outcomeViolation   = "violation"
	outcomeNotApplic   = "not_applicable"
	outcomeInsufficent = "insufficient_context"
)

// Rule tiers: which source answered.
const (
	tierRoot       = "root"
	tierConfigured = "configured"
	tierFallback   = "fallback"
	tierNone       = "none"
)

// Reserved question ids.
const (
	aggregateQuestion = "rules"
	locatorQuestion   = "which_edit"
)

// SourceRef is the `source: <file>:<line>` header: the instruction line a rule's wording came from.
type SourceRef struct {
	File string
	Line int
}

// Rule is one rule of the project's rules file.
type Rule struct {
	ID   string
	Name string
	// Body is the rule text under the heading, fences included, with the header lines removed.
	Body string
	// Paths are the globs the rule applies to; none means every file.
	Paths []string
	// Threshold is the per-rule cutoff for a finding; nil means rules.threshold.
	Threshold *float64
	// Severity orders findings only: high | normal | low; "" means normal.
	Severity  string
	SourceRef *SourceRef
	// When: "turn" rules are judged once per run against the whole diff; "" or "edit" per edit.
	When string
	// HeaderWarnings are header lines that carried a bad value, reported by `/warden rules`.
	HeaderWarnings []string
	// Source is the project-relative file the rule came from.
	Source string
}

// RuleSet is the set of rules in force.
type RuleSet struct {
	Sources []string
	Rules   []Rule
	// Aggregate is a fallback document's condensed text: one question judges the content against it. HasAggregate says
	// whether the set has one (the text itself can only be empty for a blank document, which is not a source).
	Aggregate    string
	HasAggregate bool
	// AlwaysDropped counts the unscoped rules past the request cap: they are dropped for every file.
	AlwaysDropped int
	// ProseOnly marks a fallback document with no rule-shaped sections: judgment is skipped.
	ProseOnly bool
}

func (s *RuleSet) isAggregate() bool { return s.HasAggregate && len(s.Rules) == 0 }

// ---------------------------------------------------------------------------
// Markdown parsing

// jsSpaceClass is JavaScript's `\s` as a character-class body; Go's `\s` is ASCII only.
const jsSpaceClass = `\s\x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

// jsDot is JavaScript's `.` (no line terminators) as a character-class body.
const jsDot = `[^\n\r\x{2028}\x{2029}]`

var (
	reFence        = lazyRE("^[" + jsSpaceClass + "]{0,3}(`{3,}|~{3,})")
	reHeading      = lazyRE(`^(#{1,6})[` + jsSpaceClass + `]+(` + jsDot + `+?)[` + jsSpaceClass + `]*#*[` + jsSpaceClass + `]*$`)
	rePathsLine    = lazyRE(`(?i)^[` + jsSpaceClass + `]*(?:paths?|applies to|files?)[` + jsSpaceClass + `]*:[` + jsSpaceClass + `]*(` + jsDot + `+?)[` + jsSpaceClass + `]*$`)
	reThresholdLn  = lazyRE(`(?i)^[` + jsSpaceClass + `]*threshold[` + jsSpaceClass + `]*:[` + jsSpaceClass + `]*(` + jsDot + `+?)[` + jsSpaceClass + `]*$`)
	reSeverityLine = lazyRE(`(?i)^[` + jsSpaceClass + `]*severity[` + jsSpaceClass + `]*:[` + jsSpaceClass + `]*(` + jsDot + `+?)[` + jsSpaceClass + `]*$`)
	reSourceRef    = lazyRE(`(?i)^[` + jsSpaceClass + `]*source[` + jsSpaceClass + `]*:[` + jsSpaceClass + `]*(` + jsDot + `+?)[` + jsSpaceClass + `]*$`)
	reWhenLine     = lazyRE(`(?i)^[` + jsSpaceClass + `]*when[` + jsSpaceClass + `]*:[` + jsSpaceClass + `]*(` + jsDot + `+?)[` + jsSpaceClass + `]*$`)
	reRefParts     = lazyRE(`^(` + jsDot + `+):(\d+)$`)
	rePathSep      = lazyRE(`[,` + jsSpaceClass + `]+`)
	reModalWords   = lazyRE(`(?i)^(?:must|shall|never|always|do not|don't|cannot|can't|avoid|prefer|require|should not|shouldn't|no |not )`)
	reBulletLine   = lazyRE(`^[` + jsSpaceClass + `]*[-*+][` + jsSpaceClass + `]|^[` + jsSpaceClass + `]*\d+\.[` + jsSpaceClass + `]`)
	reNonSlug      = lazyRE(`[^a-z0-9]+`)
	reSlugTail     = lazyRE(`-[^-]*$`)
	reLineBreak    = lazyRE(`\r\n|\r|\n`)
	reSpaceRunsJS  = lazyRE(`[` + jsSpaceClass + `]+`)
)

var severityRank = map[string]int{"high": 0, "normal": 1, "low": 2}

func severityOf(s string) int {
	if s == "" {
		return severityRank["normal"]
	}
	return severityRank[s]
}

func slug(name string, used map[string]bool) string {
	// JavaScript's toLowerCase maps İ to "i" and a combining dot; Go's maps it to a bare "i".
	full := reNonSlug.ReplaceAllString(strings.ToLower(strings.ReplaceAll(name, "İ", "i\u0307")), "-")
	full = strings.Trim(full, "-")
	base := full
	if len(full) > ruleIDLimit {
		// Cut at a word boundary so a long heading keeps whole words in its id.
		base = reSlugTail.ReplaceAllString(full[:ruleIDLimit], "")
	}
	if base == "" {
		base = "rule"
	}
	candidate := base
	for n := 2; used[candidate]; n++ {
		candidate = base + "-" + strconv.Itoa(n)
	}
	used[candidate] = true
	return candidate
}

type taggedLine struct {
	text   string
	fenced bool
}

// taggedLines are the lines of a document tagged with whether they sit inside a fenced code block, where `#` is code.
func taggedLines(markdown string) []taggedLine {
	var out []taggedLine
	fence := ""
	for _, text := range reLineBreak.Split(markdown, -1) {
		if m := reFence.FindStringSubmatch(text); m != nil {
			marker := m[1]
			if fence == "" {
				fence = marker
			} else if marker[0] == fence[0] && len(marker) >= len(fence) {
				fence = ""
			}
			out = append(out, taggedLine{text, true})
			continue
		}
		out = append(out, taggedLine{text, fence != ""})
	}
	return out
}

// condense cuts a fallback document to limit characters while keeping its shape: every heading stays and each section
// keeps its head, so a rule stated in the last section of a very long AGENTS.md is still seen.
func condense(text string, limit int) string {
	if utf16Len(text) <= limit {
		return text
	}
	type section struct {
		heading    string
		hasHeading bool
		body       []string
	}
	sections := []*section{{}}
	for _, line := range taggedLines(text) {
		if !line.fenced && reHeading.MatchString(line.text) {
			sections = append(sections, &section{heading: line.text, hasHeading: true})
		} else {
			last := sections[len(sections)-1]
			last.body = append(last.body, line.text)
		}
	}
	headings := 0
	for _, s := range sections {
		headings += utf16Len(s.heading) + 4
	}
	perSection := (limit - headings) / len(sections)
	if limit-headings < 0 {
		perSection = 0
	}
	if perSection < 80 {
		perSection = 80
	}
	parts := make([]string, len(sections))
	for i, s := range sections {
		body := jsTrim(strings.Join(s.body, "\n"))
		if utf16Len(body) > perSection {
			body = utf16Slice(body, 0, perSection) + "…"
		}
		head := ""
		if s.hasHeading {
			head = s.heading + "\n"
		}
		parts[i] = head + body
	}
	out := jsTrim(strings.Join(parts, "\n\n"))
	if utf16Len(out) <= limit {
		return out
	}
	return utf16Slice(out, 0, limit) + "… [" + itoaInt(utf16Len(text)-limit) + " more chars in the document]"
}

// ruleLevel is the highest heading level present outside code fences, or 7 when there is none.
func ruleLevel(lines []taggedLine) int {
	level := 7
	for _, line := range lines {
		if line.fenced {
			continue
		}
		if m := reHeading.FindStringSubmatch(line.text); m != nil && len(m[1]) < level {
			level = len(m[1])
		}
	}
	return level
}

// jsToNumber is JavaScript's Number(string): decimal, hex, octal and binary literals, Infinity, and "" as 0.
func jsToNumber(raw string) float64 {
	s := jsTrim(raw)
	if s == "" {
		return 0
	}
	if len(s) > 2 && s[0] == '0' {
		base := 0
		switch s[1] {
		case 'x', 'X':
			base = 16
		case 'o', 'O':
			base = 8
		case 'b', 'B':
			base = 2
		}
		if base != 0 {
			n, ok := new(big.Int).SetString(s[2:], base)
			if !ok {
				return math.NaN()
			}
			v, _ := new(big.Float).SetInt(n).Float64()
			return v
		}
	}
	switch s {
	case "Infinity", "+Infinity":
		return math.Inf(1)
	case "-Infinity":
		return math.Inf(-1)
	}
	if !reJSDecimal.MatchString(s) {
		return math.NaN()
	}
	// A range error still carries the nearest value (±Inf), as JavaScript's does.
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

var reJSDecimal = lazyRE(`^[+-]?(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?$`)

func isFiniteNumber(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func cutBackticks(s string) string {
	s = strings.TrimPrefix(s, "`")
	return strings.TrimSuffix(s, "`")
}

// parseRules: headings at the highest level present outside code fences delimit rules (`#` in a jev-rules style file,
// `##` under a document title). Text before the first such heading is not a rule. Header lines at the top of a body
// (paths, threshold, severity, source, when) configure the rule.
func parseRules(markdown string) []Rule {
	lines := taggedLines(markdown)
	level := ruleLevel(lines)
	if level == 7 {
		return nil
	}
	type draft struct {
		name  string
		lines []string
	}
	var drafts []*draft
	for _, line := range lines {
		if !line.fenced {
			if m := reHeading.FindStringSubmatch(line.text); m != nil && len(m[1]) == level {
				drafts = append(drafts, &draft{name: m[2]})
				continue
			}
		}
		if len(drafts) > 0 {
			d := drafts[len(drafts)-1]
			d.lines = append(d.lines, line.text)
		}
	}
	used := map[string]bool{}
	rules := make([]Rule, 0, len(drafts))
	for _, d := range drafts {
		body := append([]string(nil), d.lines...)
		var rule Rule
		var warnings []string
		seenPaths, seenThreshold, seenSeverity, seenSource, seenWhen := false, false, false, false, false
		// Header lines may sit in any order and at most once. A bad value is dropped with a warning; the line never
		// stays in the body.
		for {
			first := -1
			for i, text := range body {
				if jsTrim(text) != "" {
					first = i
					break
				}
			}
			if first < 0 {
				break
			}
			line := body[first]
			remove := func() { body = append(body[:first], body[first+1:]...) }
			if m := rePathsLine.FindStringSubmatch(line); m != nil {
				remove()
				if seenPaths {
					warnings = append(warnings, "paths: appears more than once; the first is kept")
				} else {
					seenPaths = true
					rule.Paths = nil
					for _, glob := range rePathSep.Split(m[1], -1) {
						if glob = cutBackticks(glob); glob != "" {
							rule.Paths = append(rule.Paths, glob)
						}
					}
				}
				continue
			}
			if m := reThresholdLn.FindStringSubmatch(line); m != nil {
				remove()
				raw := jsTrim(cutBackticks(m[1]))
				value := jsToNumber(raw)
				if seenThreshold {
					warnings = append(warnings, "threshold: appears more than once; the first is kept")
				} else if raw == "" || !isFiniteNumber(value) || value < 0 || value > 1 {
					shown := raw
					if shown == "" {
						shown = "(empty)"
					}
					warnings = append(warnings, "threshold: "+shown+" is not a number from 0 to 1; ignored")
				} else {
					v := value
					rule.Threshold = &v
					seenThreshold = true
				}
				continue
			}
			if m := reSeverityLine.FindStringSubmatch(line); m != nil {
				remove()
				raw := strings.ToLower(jsTrim(m[1]))
				if seenSeverity {
					warnings = append(warnings, "severity: appears more than once; the first is kept")
				} else if raw != "high" && raw != "normal" && raw != "low" {
					shown := jsTrim(m[1])
					if shown == "" {
						shown = "(empty)"
					}
					warnings = append(warnings, "severity: "+shown+" is not high, normal, or low; ignored")
				} else {
					rule.Severity = raw
					seenSeverity = true
				}
				continue
			}
			// One small block for the `source: <file>:<line>` citation header, beside the other header lines.
			if m := reSourceRef.FindStringSubmatch(line); m != nil {
				remove()
				raw := jsTrim(cutBackticks(m[1]))
				ref := reRefParts.FindStringSubmatch(raw)
				file := ""
				at := 0.0
				if ref != nil {
					file = jsTrim(ref[1])
					at, _ = strconv.ParseFloat(ref[2], 64)
				}
				if seenSource {
					warnings = append(warnings, "source: appears more than once; the first is kept")
				} else if file == "" || at == 0 {
					shown := raw
					if shown == "" {
						shown = "(empty)"
					}
					warnings = append(warnings, "source: "+shown+" is not a file:line reference; ignored")
				} else {
					rule.SourceRef = &SourceRef{File: file, Line: int(at)}
					seenSource = true
				}
				continue
			}
			if m := reWhenLine.FindStringSubmatch(line); m != nil {
				remove()
				raw := strings.ToLower(jsTrim(m[1]))
				if seenWhen {
					warnings = append(warnings, "when: appears more than once; the first is kept")
				} else if raw != "edit" && raw != "turn" {
					shown := jsTrim(m[1])
					if shown == "" {
						shown = "(empty)"
					}
					warnings = append(warnings, "when: "+shown+" is not edit or turn; ignored")
				} else {
					rule.When = raw
					seenWhen = true
				}
				continue
			}
			break
		}
		rule.ID = slug(d.name, used)
		rule.Name = jsTrim(d.name)
		rule.Body = jsTrim(strings.Join(body, "\n"))
		rule.HeaderWarnings = warnings
		rules = append(rules, rule)
	}
	return rules
}

// isRuleShaped: a document counts as rule-shaped when at least one section (heading at the rule level) contains an
// imperative or constraint sentence (a bullet list, or a line starting with a modal such as must, never, always, do
// not, avoid, prefer) within the first few lines of its body. A document with zero rule-shaped sections is prose only.
func isRuleShaped(markdown string) bool {
	lines := taggedLines(markdown)
	level := ruleLevel(lines)
	if level == 7 {
		return false
	}
	inSection := false
	inBody := 0
	for _, line := range lines {
		if line.fenced {
			continue
		}
		if m := reHeading.FindStringSubmatch(line.text); m != nil && len(m[1]) == level {
			inSection = true
			inBody = 0
			continue
		}
		if inSection {
			inBody++
			if inBody > ruleShapeContextLines {
				inSection = false
				continue
			}
			if reBulletLine.MatchString(line.text) || reModalWords.MatchString(jsTrim(line.text)) {
				return true
			}
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Globs: `**/` any depth, `*` within a segment, `?` one character; unanchored at the start so `migrations/**` matches
// `db/migrations/0182.sql`. Paths are project-relative with forward slashes.

// matchGlobFirst is the first pattern that matches the path (rules.ts matchGlob).
func matchGlobFirst(path string, patterns []string) (string, bool) {
	normalised := strings.TrimPrefix(path, "./")
	for _, pattern := range patterns {
		if globToRegexp(jsTrim(pattern)).MatchString(normalised) {
			return pattern, true
		}
	}
	return "", false
}

func resolvePath(cwd, target string) string {
	if filepath.IsAbs(target) {
		return filepath.Clean(target)
	}
	return filepath.Join(cwd, target)
}

// rulesProjectPath is the project-relative path with forward slashes, or false when the target lies outside the
// project (or is the project itself).
func rulesProjectPath(target, cwd string) (string, bool) {
	base := resolvePath("", cwd)
	rel, err := filepath.Rel(base, resolvePath(cwd, target))
	if err != nil || rel == "" || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// ---------------------------------------------------------------------------
// Rule sources with an mtime cache

type cachedFile struct {
	mtime int64
	size  int64
	text  string
}

// RuleStore loads and caches the rule sources: a file is re-read when its mtime or size changes, and the rule set built
// from unchanged files is reused (upstream parses and redacts it again on every call).
type RuleStore struct {
	mu    sync.Mutex
	cache map[string]cachedFile
	sets  map[string]cachedSet
}

type cachedSet struct {
	key  string
	set  *RuleSet
	tier string
}

// NewRuleStore returns an empty store.
func NewRuleStore() *RuleStore {
	return &RuleStore{cache: map[string]cachedFile{}, sets: map[string]cachedSet{}}
}

// decodeUTF8 reads bytes as Node's "utf8" does: an invalid byte becomes U+FFFD.
func decodeUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var out strings.Builder
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		if r == utf8.RuneError && size == 1 {
			out.WriteString("\uFFFD")
		} else {
			out.Write(b[:size])
		}
		b = b[size:]
	}
	return out.String()
}

// read is a file's text and its stamp (path, mtime, size), or false when it cannot be read.
func (s *RuleStore) read(path string) (string, string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	info, err := os.Stat(path)
	if err != nil {
		delete(s.cache, path)
		return "", "", false
	}
	stamp := path + "|" + strconv.FormatInt(info.ModTime().UnixNano(), 10) + "|" + strconv.FormatInt(info.Size(), 10)
	if cached, ok := s.cache[path]; ok && cached.mtime == info.ModTime().UnixNano() && cached.size == info.Size() {
		return cached.text, stamp, true
	}
	data, err := os.ReadFile(path)
	if err != nil {
		delete(s.cache, path)
		return "", "", false
	}
	text := decodeUTF8(data)
	s.cache[path] = cachedFile{mtime: info.ModTime().UnixNano(), size: info.Size(), text: text}
	return text, stamp, true
}

// LoadTiered is where a rule set came from; the order is the resolution order.
func (s *RuleStore) LoadTiered(cwd string, config RulesConfig) (*RuleSet, string) {
	var stamps, sources, texts []string
	tier := tierNone
	fallbackSource, fallbackText := "", ""
	if text, stamp, ok := s.read(resolvePath(cwd, RulesFile)); ok {
		stamps, sources, texts, tier = append(stamps, stamp), []string{RulesFile}, []string{text}, tierRoot
	} else {
		for _, file := range config.Files {
			if text, stamp, ok := s.read(resolvePath(cwd, file)); ok {
				stamps, sources, texts = append(stamps, stamp), append(sources, file), append(texts, text)
			}
		}
		if len(sources) > 0 {
			tier = tierConfigured
		} else if config.Fallback {
			for _, file := range fallbackFiles {
				text, stamp, ok := s.read(resolvePath(cwd, file))
				if !ok {
					continue
				}
				// A blank fallback document is not a source, but a change to it must still show.
				stamps = append(stamps, stamp)
				if jsTrim(text) != "" {
					fallbackSource, fallbackText, tier = file, text, tierFallback
					break
				}
			}
		}
	}
	if tier == tierNone {
		return nil, tierNone
	}
	key := cwd + "\x00" + strconv.Itoa(config.MaxChars) + "\x00" + strconv.FormatBool(config.Fallback) + "\x00" + strings.Join(config.Files, "\x01") + "\x00" + strings.Join(stamps, "\x00")
	s.mu.Lock()
	hit, ok := s.sets[cwd]
	s.mu.Unlock()
	if ok && hit.key == key {
		return hit.set, hit.tier
	}
	var set *RuleSet
	if tier == tierFallback {
		redactedText := Redact(fallbackText)
		if isRuleShaped(fallbackText) {
			set = &RuleSet{Sources: []string{fallbackSource}, Aggregate: condense(redactedText, config.MaxChars), HasAggregate: true}
		} else {
			set = &RuleSet{Sources: []string{fallbackSource}, ProseOnly: true}
		}
	} else {
		set = newRuleSet(sources, texts, config.MaxChars)
	}
	s.mu.Lock()
	s.sets[cwd] = cachedSet{key: key, set: set, tier: tier}
	s.mu.Unlock()
	return set, tier
}

// Load is the active rule set for a project, or nil when no source exists.
func (s *RuleStore) Load(cwd string, config RulesConfig) *RuleSet {
	set, _ := s.LoadTiered(cwd, config)
	return set
}

func newRuleSet(sources, texts []string, maxChars int) *RuleSet {
	used := map[string]bool{}
	var rules []Rule
	for i, text := range texts {
		for _, rule := range parseRules(text) {
			rule.Source = sources[i]
			rule.ID = slug(rule.ID, used)
			rule.Body = Redact(rule.Body)
			rules = append(rules, rule)
		}
	}
	if len(rules) == 0 {
		text := jsTrim(strings.Join(texts, "\n\n"))
		if text == "" {
			return nil
		}
		return &RuleSet{Sources: sources, Aggregate: condense(Redact(text), maxChars), HasAggregate: true}
	}
	unscoped := 0
	for _, rule := range rules {
		if len(rule.Paths) == 0 {
			unscoped++
		}
	}
	dropped := unscoped - maxRules
	if dropped < 0 {
		dropped = 0
	}
	return &RuleSet{Sources: sources, Rules: rules, AlwaysDropped: dropped}
}

// rulesFor are the rules that apply to a path: unscoped rules plus those whose `paths` match.
func rulesFor(set *RuleSet, path string) []Rule {
	var out []Rule
	// Rules of a file commonly share one `paths:` list: the answer for the list just asked about is reused.
	var lastPaths []string
	lastMatch, asked := false, false
	for i := range set.Rules {
		rule := &set.Rules[i]
		keep := len(rule.Paths) == 0
		if !keep {
			if !asked || !slices.Equal(rule.Paths, lastPaths) {
				_, lastMatch = matchGlobFirst(path, rule.Paths)
				lastPaths, asked = rule.Paths, true
			}
			keep = lastMatch
		}
		if keep {
			if out == nil {
				out = make([]Rule, 0, len(set.Rules))
			}
			out = append(out, *rule)
		}
	}
	return out
}

// editRulesFor are the rules asked about a write or edit: those that apply to the path without a `when: turn` header.
func editRulesFor(set *RuleSet, path string) []Rule {
	scoped := rulesFor(set, path)
	var out []Rule
	for i := range scoped {
		if scoped[i].When != "turn" {
			if out == nil {
				out = make([]Rule, 0, len(scoped))
			}
			out = append(out, scoped[i])
		}
	}
	return out
}

// turnRulesFor are the rules judged once per run against the whole diff: those with a `when: turn` header.
func turnRulesFor(set *RuleSet) []Rule {
	var out []Rule
	for _, rule := range set.Rules {
		if rule.When == "turn" {
			out = append(out, rule)
		}
	}
	return out
}

func pluralOf(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// describeRuleSet is the one-line description of a rule set.
func describeRuleSet(set *RuleSet) string {
	if set == nil {
		return "none found"
	}
	where := strings.Join(set.Sources, ", ")
	if set.ProseOnly {
		return "no rules found in " + where + " (prose only)"
	}
	if set.isAggregate() {
		return where + " (no rule headings: judged as one document)"
	}
	dropped := ""
	if set.AlwaysDropped > 0 {
		dropped = ", " + strconv.Itoa(set.AlwaysDropped) + " unscoped past the " + strconv.Itoa(maxRules) + "-question cap for every file"
	}
	return where + " (" + strconv.Itoa(len(set.Rules)) + " " + pluralOf(len(set.Rules), "rule", "rules") + dropped + ")"
}

// formatRuleSetDetails is the detailed local-only view for `/warden rules`; nothing here is sent to the judge.
func formatRuleSetDetails(set *RuleSet, tier string, exclude []string) string {
	if set == nil {
		return "No rules file detected. Run /warden init to create project-specific rules."
	}
	source := strings.Join(set.Sources, ", ")
	if tier == tierConfigured {
		source = "rules.files: " + source
	}
	excluded := ""
	if len(exclude) > 0 {
		excluded = "\nExcluded from Jev by rules.exclude: " + strings.Join(exclude, ", ")
	}
	if set.isAggregate() {
		return "Rules in force: " + source + " judged as one aggregate rule (" + strconv.Itoa(utf16Len(set.Aggregate)) + " condensed chars)" + excluded
	}
	if set.ProseOnly {
		return "Rules in force: " + source + " (prose only; no rule-shaped sections)" + excluded
	}
	lines := []string{"Rules in force: " + source + " (" + strconv.Itoa(len(set.Rules)) + " " + pluralOf(len(set.Rules), "rule", "rules") + ", " + strconv.Itoa(set.AlwaysDropped) + " dropped)"}
	showSource := tier == tierConfigured && len(set.Sources) > 1
	for i, rule := range set.Rules {
		from := ""
		if showSource && rule.Source != "" {
			from = " [" + rule.Source + "]"
		}
		paths := "(all)"
		if len(rule.Paths) > 0 {
			paths = strings.Join(rule.Paths, ", ")
		}
		settings := []string{"paths: " + paths}
		if rule.Threshold != nil {
			settings = append(settings, "threshold: "+jsNumberString(*rule.Threshold))
		}
		if rule.Severity != "" {
			settings = append(settings, "severity: "+rule.Severity)
		}
		if rule.SourceRef != nil {
			settings = append(settings, "source: "+rule.SourceRef.File+":"+strconv.Itoa(rule.SourceRef.Line))
		}
		if rule.When != "" && rule.When != "edit" {
			settings = append(settings, "when: "+rule.When)
		}
		lines = append(lines, strconv.Itoa(i+1)+". "+rule.ID+from+" "+strings.Join(settings, ", "))
		if len(rule.HeaderWarnings) > 0 {
			lines = append(lines, "   "+strings.Join(rule.HeaderWarnings, "; "))
		}
	}
	if len(exclude) > 0 {
		lines = append(lines, "Excluded from Jev by rules.exclude: "+strings.Join(exclude, ", "))
	}
	return strings.Join(lines, "\n")
}

// jsNumberString is a number printed as JavaScript prints it in a template string.
func jsNumberString(v float64) string {
	if v == 0 {
		return "0"
	}
	abs := v
	if abs < 0 {
		abs = -abs
	}
	if abs >= 1e21 || abs < 1e-6 {
		s := strconv.FormatFloat(v, 'e', -1, 64)
		mant, exp, _ := strings.Cut(s, "e")
		sign := exp[:1]
		digits := strings.TrimLeft(exp[1:], "0")
		return mant + "e" + sign + digits
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// ---------------------------------------------------------------------------
// What is judged

// EditView is one edit as the judge sees it.
type EditView struct {
	ID string
	// Before is the current file text around the replaced text, when the file exists and the text was found; After is
	// the same lines with this edit applied.
	Before, After *string
	NewText       string
}

// RulesTarget is a write or edit prepared for judgment.
type RulesTarget struct {
	Tool string // write | edit
	// Path is project-relative.
	Path    string
	Content *string
	Edits   []EditView
	// MoreEdits counts the edits beyond maxEdits, not shown.
	MoreEdits int
}

// contextAround is the lines of the current file around the first occurrence of oldText, before and after the edit. A
// rule about the surrounding code (a doc comment above a function whose body changed) can only be judged on the
// result, not on newText alone.
func contextAround(file *string, oldText, newText string) (before, after string, ok bool) {
	if file == nil || oldText == "" {
		return "", "", false
	}
	at := strings.Index(*file, oldText)
	if at < 0 {
		return "", "", false
	}
	lines := strings.Split(*file, "\n")
	startLine := strings.Count((*file)[:at], "\n")
	endLine := startLine + strings.Count(oldText, "\n")
	from := startLine - editContextLines
	if from < 0 {
		from = 0
	}
	to := endLine + editContextLines + 1
	if to > len(lines) {
		to = len(lines)
	}
	excerpt := strings.Join(lines[from:to], "\n")
	offset := at - len(strings.Join(lines[:from], "\n"))
	if from > 0 {
		offset--
	}
	afterText := excerpt[:offset] + newText + excerpt[offset+len(oldText):]
	return truncate(excerpt, editTextLimit), truncate(afterText, editTextLimit), true
}

// describeTarget is the redacted content of a write or edit as the judge sees it, or nil when the call carries nothing
// to judge.
func describeTarget(tool string, input map[string]any, cwd string) *RulesTarget {
	if tool != "write" && tool != "edit" {
		return nil
	}
	rawPath, _ := input["path"].(string)
	if jsTrim(rawPath) == "" {
		return nil
	}
	path, ok := rulesProjectPath(rawPath, cwd)
	if !ok {
		return nil
	}
	if tool == "write" {
		content, _ := input["content"].(string)
		if jsTrim(content) == "" {
			return nil
		}
		redacted := Redact(sample(content, rulesContentLimit))
		return &RulesTarget{Tool: tool, Path: path, Content: &redacted}
	}
	rawEdits, isArray := input["edits"].([]any)
	if !isArray {
		return nil
	}
	var file *string
	if data, err := os.ReadFile(resolvePath(cwd, rawPath)); err == nil {
		text := decodeUTF8(data)
		file = &text
	}
	var edits []EditView
	for index, raw := range rawEdits {
		if len(edits) >= maxEdits {
			break
		}
		item, _ := raw.(map[string]any)
		newText, _ := item["newText"].(string)
		if jsTrim(newText) == "" {
			continue
		}
		oldText, _ := item["oldText"].(string)
		view := EditView{ID: "edit_" + strconv.Itoa(index+1), NewText: Redact(truncate(newText, editTextLimit))}
		if before, after, ok := contextAround(file, oldText, newText); ok {
			b, a := Redact(before), Redact(after)
			view.Before, view.After = &b, &a
		}
		edits = append(edits, view)
	}
	if len(edits) == 0 {
		return nil
	}
	target := &RulesTarget{Tool: tool, Path: path, Edits: edits}
	if more := len(rawEdits) - maxEdits; more > 0 {
		target.MoreEdits = more
	}
	return target
}

// ---------------------------------------------------------------------------
// The request: one Choice per rule, an aggregate Choice for a fallback document, one locator Choice for several edits.

// outcomeChoice is a choice question over the four outcomes.
func outcomeChoice(instructions string) Question {
	criteria, order := outcomeCriteria()
	return Question{Type: "choice", Instructions: instructions, Criteria: criteria, CriteriaOrder: order}
}

// outcomeCriteria is the wording of the four outcomes, built once: every rule's question shares the map and the order,
// and nothing writes to them (a request is only read, by the backends and the answer validation).
var outcomeCriteria = sync.OnceValues(func() (map[string]string, []string) {
	q := rulesQuestions()
	criteria := make(map[string]string, len(q.outcomeOrder))
	for _, key := range q.outcomeOrder {
		criteria[key] = q.outcomes[key]
	}
	return criteria, append([]string(nil), q.outcomeOrder...)
})

func ruleQuestionText(frame string, rule Rule) string {
	body := "(no further detail beyond the heading)"
	if rule.Body != "" {
		body = truncate(rule.Body, ruleBodyLimit)
	}
	return frame + "\nRule: " + rule.Name + "\n" + body
}

func ruleQuestion(rule Rule) Question {
	return outcomeChoice(ruleQuestionText(rulesQuestions().ruleFrame, rule))
}

// RulesRequest is the judge request for one write or edit and what it asks about.
type RulesRequest struct {
	State     map[string]any
	Questions Questions
	// Applicable are the rules asked about, after path scoping and the cap.
	Applicable []Rule
	// Dropped counts the applicable rules past the cap, FirstDropped is the id of the first of them.
	Dropped      int
	FirstDropped string
}

// buildRulesRequest is the rules asked about for a write: scoped to its path first, `when: turn` rules excluded, then
// cut to the request cap in file order.
func buildRulesRequest(target *RulesTarget, set *RuleSet) RulesRequest {
	scoped := editRulesFor(set, target.Path)
	applicable := scoped
	if len(applicable) > maxRules {
		applicable = applicable[:maxRules]
	}
	dropped := len(scoped) - len(applicable)
	questions := Questions{}
	for _, rule := range applicable {
		questions["rule_"+rule.ID] = ruleQuestion(rule)
	}
	if set.isAggregate() {
		questions[aggregateQuestion] = rulesQuestions().aggregate
	}
	if len(target.Edits) >= 2 {
		q := Question{Type: "choice", Instructions: rulesQuestions().locator.Instructions, Criteria: map[string]string{}}
		for _, edit := range target.Edits {
			q.Criteria[edit.ID] = "the entry with id " + edit.ID
			q.CriteriaOrder = append(q.CriteriaOrder, edit.ID)
		}
		questions[locatorQuestion] = q
	}
	state := map[string]any{"path": target.Path}
	if target.Content != nil {
		state["content"] = *target.Content
	}
	if len(target.Edits) > 0 {
		edits := make([]any, len(target.Edits))
		for i, edit := range target.Edits {
			entry := map[string]any{"id": edit.ID, "newText": edit.NewText}
			if edit.Before != nil {
				entry["before"] = *edit.Before
			}
			if edit.After != nil {
				entry["after"] = *edit.After
			}
			edits[i] = entry
		}
		state["edits"] = edits
	}
	if target.MoreEdits > 0 {
		state["moreEdits"] = target.MoreEdits
	}
	if set.isAggregate() {
		state["rules"] = set.Aggregate
	}
	request := RulesRequest{State: state, Questions: questions, Applicable: applicable, Dropped: dropped}
	if dropped > 0 {
		request.FirstDropped = scoped[maxRules].ID
	}
	return request
}

// ---------------------------------------------------------------------------
// Verdict

// RuleScore is one rule's score for one request.
type RuleScore struct {
	ID        string
	Name      string
	Outcome   string
	Violation float64
	// Threshold is the rule's own cutoff when it set one; nil means rules.threshold.
	Threshold *float64
	// Severity is the rule's severity when it set one.
	Severity string
	// SourceRef is the instruction line the rule's wording came from, when its `source:` header set one.
	SourceRef *SourceRef
}

// RuleFinding is a score at or above its cutoff, with the rule's text.
type RuleFinding struct {
	RuleScore
	Body string
}

// RuleScoreEntry is one question in a rules request: its key, the rule it came from, and the body quoted in a steer.
type RuleScoreEntry struct {
	Key, ID, Name, Body string
	// Rule carries the rule's own cutoff, severity and source citation when it has them.
	Rule *Rule
}

// RuleScoring is the scores and findings of one request.
type RuleScoring struct {
	Scores []RuleScore
	// Findings are violations at or above the rule's cutoff, severity first then strongest.
	Findings []RuleFinding
	// SoftFindings are scores at or above rules.softThreshold but below the rule's cutoff.
	SoftFindings []RuleFinding
}

func bySeverityThenScore(list []RuleFinding) {
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if d := severityOf(a.Severity) - severityOf(b.Severity); d != 0 {
			return d < 0
		}
		return b.Violation-a.Violation < 0
	})
}

// scoreAnswers are the scores and findings for one request's answers: a finding needs the rule's own cutoff when it set
// one, else the global threshold, and the soft tier sits under that cutoff. Findings are ordered severity first, then
// the strongest score.
func scoreAnswers(answers map[string]Answer, entries []RuleScoreEntry, config RulesConfig) RuleScoring {
	out := RuleScoring{Scores: make([]RuleScore, 0, len(entries))}
	for _, entry := range entries {
		answer, ok := answers[entry.Key]
		if !ok || answer.Choice == "" {
			continue
		}
		violation := 0.0
		if p, has := answer.Probabilities[outcomeViolation]; has {
			violation = p
		} else if answer.Choice == outcomeViolation {
			violation = 1
		}
		outcome := answer.Choice
		if _, known := rulesQuestions().outcomes[outcome]; !known {
			outcome = outcomeInsufficent
		}
		score := RuleScore{ID: entry.ID, Name: entry.Name, Outcome: outcome, Violation: violation}
		cutoff := config.Threshold
		if entry.Rule != nil {
			score.Threshold, score.Severity, score.SourceRef = entry.Rule.Threshold, entry.Rule.Severity, entry.Rule.SourceRef
			if entry.Rule.Threshold != nil {
				cutoff = *entry.Rule.Threshold
			}
		}
		out.Scores = append(out.Scores, score)
		if violation >= cutoff {
			out.Findings = append(out.Findings, RuleFinding{RuleScore: score, Body: entry.Body})
		} else if config.SoftThreshold > 0 && violation >= config.SoftThreshold {
			out.SoftFindings = append(out.SoftFindings, RuleFinding{RuleScore: score, Body: entry.Body})
		}
	}
	bySeverityThenScore(out.Findings)
	bySeverityThenScore(out.SoftFindings)
	return out
}

// scoreRuleAnswers scores the judge's answers rule by rule: one score each, a finding at the rule's own cutoff (or
// rules.threshold when the rule sets none), and a soft finding between rules.softThreshold and that cutoff. An
// aggregate document is one rule under its source's name. Shared by the live guard and history replays.
func scoreRuleAnswers(applicable []Rule, answers map[string]Answer, config RulesConfig, aggregateName *string) RuleScoring {
	var entries []RuleScoreEntry
	if aggregateName != nil {
		entries = []RuleScoreEntry{{Key: aggregateQuestion, ID: aggregateQuestion, Name: *aggregateName}}
	} else {
		entries = make([]RuleScoreEntry, 0, len(applicable))
		for i := range applicable {
			rule := &applicable[i]
			entries = append(entries, RuleScoreEntry{Key: "rule_" + rule.ID, ID: rule.ID, Name: rule.Name, Body: rule.Body, Rule: rule})
		}
	}
	return scoreAnswers(answers, entries, config)
}

// RulesVerdict is the verdict on one write, edit or run.
type RulesVerdict struct {
	Source  string // skipped | typesafe | error
	Path    string
	Tool    string // write | edit | turn
	Sources []string
	// Asked counts the rules asked about, after path scoping and the cap; 1 for an aggregate document.
	Asked int
	// Dropped counts the rules that apply to the path but were past the request cap; FirstDropped is the id of the first.
	Dropped      int
	FirstDropped string
	Aggregate    bool
	// Scores is nil unless the judge answered.
	Scores       []RuleScore
	Findings     []RuleFinding
	SoftFindings []RuleFinding
	// EditID is the edit the judge points at when several edits were judged and something was flagged.
	EditID, EditPreview string
	Model               string
	ElapsedMs           *int
	SkippedReason       string
	Error, ErrorCode    string
}

// RulesOptions are the inputs of one evaluation.
type RulesOptions struct {
	Ctx     context.Context
	Cwd     string
	Config  RulesConfig
	Set     *RuleSet
	Judge   Judge
	Timeout time.Duration
}

func (o RulesOptions) ctx() context.Context {
	if o.Ctx == nil {
		return context.Background()
	}
	return o.Ctx
}

// gitIgnored reports whether a project-relative path is ignored by the project's gitignore rules, via
// `git check-ignore -q` run from cwd (with --no-index, a force-added ignored file still counts as ignored). False when
// git is unavailable or the path cannot be checked.
func gitIgnored(projectRel, cwd string, noIndex bool) bool {
	args := []string{"check-ignore"}
	if noIndex {
		args = append(args, "--no-index")
	}
	args = append(args, "-q", projectRel)
	_, ok := runGit(cwd, args...)
	return ok
}

// skipReason is why a write or edit is not judged against the rules: nothing to judge, no rules, or a path the config
// keeps out. "" means it is judged.
func skipReason(target *RulesTarget, set *RuleSet, config RulesConfig) string {
	if target == nil {
		return "nothing to judge or outside the project"
	}
	if set == nil {
		return "no rules file"
	}
	if g, ok := matchGlobFirst(target.Path, config.Exclude); ok {
		return "excluded from Jev by rules.exclude (" + g + ")"
	}
	if g, ok := matchGlobFirst(target.Path, config.Skip); ok {
		return "rules do not apply by rules.skip (" + g + ")"
	}
	if set.ProseOnly {
		return "no rules found in " + strings.Join(set.Sources, ", ") + " (prose only)"
	}
	if len(set.Rules) > 0 && len(editRulesFor(set, target.Path)) == 0 {
		return "no rule's paths match this file"
	}
	return ""
}

func skippedVerdict(target *RulesTarget, tool, path string, set *RuleSet, reason string) RulesVerdict {
	t := "write"
	if target != nil {
		t = target.Tool
	} else if tool == "edit" {
		t = "edit"
	}
	var sources []string
	if set != nil {
		sources = set.Sources
	}
	return RulesVerdict{Source: "skipped", Tool: t, Path: path, Sources: sources, SkippedReason: reason}
}

// evaluateRules prepares the target of a write or edit and judges it.
func evaluateRules(tool string, input map[string]any, options RulesOptions) RulesVerdict {
	target := describeTarget(tool, input, options.Cwd)
	shown, ok := input["path"].(string)
	if !ok {
		shown = tool
	}
	return evaluateRulesTarget(target, tool, shown, options)
}

// evaluateRulesTarget is the judgment half of evaluateRules for a prepared target: skip reasons, one request, scores,
// findings. A caller that builds its own target (the end-of-run pass judges a file's diff as one edit) gets the same
// question and scoring.
func evaluateRulesTarget(target *RulesTarget, tool, shownPath string, options RulesOptions) RulesVerdict {
	set := options.Set
	reason := skipReason(target, set, options.Config)
	if reason != "" || target == nil || set == nil {
		if reason == "" {
			reason = "nothing to judge"
		}
		return skippedVerdict(target, tool, shownPath, set, reason)
	}
	if options.Judge == nil {
		return skippedVerdict(target, tool, shownPath, set, "TypeSafe judgments are off")
	}
	request := buildRulesRequest(target, set)
	aggregate := set.isAggregate()
	base := RulesVerdict{Tool: target.Tool, Path: target.Path, Sources: set.Sources, Asked: len(request.Applicable), Aggregate: aggregate}
	if aggregate {
		base.Asked = 1
	}
	if request.FirstDropped != "" {
		base.Dropped, base.FirstDropped = request.Dropped, request.FirstDropped
	}
	result := Ask(options.ctx(), options.Judge, Request{State: request.State, Questions: request.Questions}, options.Timeout)
	if !result.OK {
		base.Source, base.Error, base.ErrorCode = "error", result.Error, result.ErrorCode
		return base
	}
	var aggregateName *string
	if aggregate {
		name := "the project's " + set.Sources[0]
		aggregateName = &name
	}
	scoring := scoreRuleAnswers(request.Applicable, result.Answers, options.Config, aggregateName)
	base.Source, base.Scores, base.Findings, base.SoftFindings = "typesafe", scoring.Scores, scoring.Findings, scoring.SoftFindings
	base.Model, base.ElapsedMs = result.Model, new(result.ElapsedMs)
	if locator, ok := result.Answers[locatorQuestion]; ok && len(scoring.Findings) > 0 && locator.Choice != "" {
		for _, edit := range target.Edits {
			if edit.ID == locator.Choice {
				base.EditID = edit.ID
				first, _, _ := strings.Cut(jsTrim(edit.NewText), "\n")
				base.EditPreview = utf16Slice(first, 0, 60)
				break
			}
		}
	}
	return base
}

// ---------------------------------------------------------------------------
// What the agent is told

var (
	reRepeatSpace = lazyRE(`[` + jsSpaceClass + `]+`)
	reBodyEnd     = lazyRE(`[.;:,]+$`)
)

func ordinalSuffix(count int) string {
	if count == 3 {
		return "rd"
	}
	return "th"
}

// namedFindings names each finding with its score, repeat count and a short quote of its text.
func namedFindings(findings []RuleFinding, counts map[string]int, withSource bool) (string, bool) {
	standing := false
	parts := make([]string, len(findings))
	for i, finding := range findings {
		count := counts[finding.ID]
		repeat := ""
		if count >= 3 {
			repeat = "; " + strconv.Itoa(count) + ordinalSuffix(count) + " time this session"
			standing = true
		}
		body := ""
		if finding.Body != "" {
			body = ": " + reBodyEnd.ReplaceAllString(truncate(jsTrim(reRepeatSpace.ReplaceAllString(finding.Body, " ")), steerBodyLimit), "")
		}
		from := ""
		if withSource && finding.SourceRef != nil {
			from = " (from " + finding.SourceRef.File + " line " + strconv.Itoa(finding.SourceRef.Line) + ")"
		}
		parts[i] = `"` + finding.Name + `"` + from + " (" + toFixed(finding.Violation, 2) + repeat + ")" + body
	}
	return strings.Join(parts, "; "), standing
}

// rulesSteer names each violated rule with a short quote of its text; the third hit of one rule in a session makes it a
// standing rule.
func rulesSteer(verdict RulesVerdict, counts map[string]int) string {
	where := verdict.Path
	if verdict.EditID != "" {
		where = verdict.Path + " in " + strings.Replace(verdict.EditID, "_", " ", 1)
		if verdict.EditPreview != "" {
			where += ` (starting "` + verdict.EditPreview + `")`
		}
	}
	// One short sentence for the soft tier, in the same steer; a soft-only verdict is just that sentence.
	alsoCheck := ""
	if len(verdict.SoftFindings) > 0 {
		parts := make([]string, len(verdict.SoftFindings))
		for i, finding := range verdict.SoftFindings {
			parts[i] = `"` + finding.Name + `" applies here (` + toFixed(finding.Violation, 2) + ")"
		}
		alsoCheck = "Also check whether " + strings.Join(parts, " or ") + "."
	}
	if len(verdict.Findings) == 0 {
		return "pi-warden: " + alsoCheck
	}
	named, standing := namedFindings(verdict.Findings, counts, true)
	// The steer names the rule and the written file only: a named rules or config file sends a weak model off to read it.
	what := "violates project rule"
	if len(verdict.Findings) != 1 {
		what += "s"
	}
	if verdict.Aggregate {
		what = "breaks a project rule"
	}
	tail := ""
	if standing {
		tail = " Treat this as a standing rule for the rest of the session."
	}
	if alsoCheck != "" {
		tail += " " + alsoCheck
	}
	return "pi-warden: the content just written to " + where + " " + what + ": " + named + ". Fix it in your next edit." + tail
}

// ---------------------------------------------------------------------------
// Sensitive paths: glob -> note, code only, no request.

// PathNote is a sensitive-path glob and its note.
type PathNote struct{ Glob, Note string }

func pathNotes(path string, notes map[string]string) []PathNote {
	if path == "" {
		return nil
	}
	var globs []string
	for glob := range notes {
		globs = append(globs, glob)
	}
	// JavaScript lists an object's keys in insertion order; a Go map has none, so the notes are read in sorted order.
	sort.Strings(globs)
	var out []PathNote
	for _, glob := range globs {
		if _, ok := matchGlobFirst(path, []string{glob}); ok {
			out = append(out, PathNote{Glob: glob, Note: notes[glob]})
		}
	}
	return out
}

var reNoteEnd = lazyRE(`[.!]?$`)

func pathNoteSteer(path string, hits []PathNote) string {
	globs := make([]string, len(hits))
	notes := make([]string, len(hits))
	for i, hit := range hits {
		globs[i] = hit.Glob
		notes[i] = reNoteEnd.ReplaceAllString(jsTrim(hit.Note), ".")
	}
	return "pi-warden: " + path + " is a sensitive path in this project (" + strings.Join(globs, ", ") + "). " + strings.Join(notes, " ")
}

// ---------------------------------------------------------------------------
// Session state: sibling prejudging and repeat counts.

// RulesCallRef is a tool call as the guard reads it.
type RulesCallRef struct {
	ID    string
	Tool  string
	Input map[string]any
}

// RulesFuture is a verdict being judged: Wait returns it once the judgment has finished.
type RulesFuture struct {
	done    chan struct{}
	verdict RulesVerdict
	// started closes when the request has reached the judge (or the call needed none).
	started chan struct{}
}

// Wait blocks until the verdict is ready.
func (f *RulesFuture) Wait() RulesVerdict {
	<-f.done
	return f.verdict
}

func resolvedFuture(v RulesVerdict) *RulesFuture {
	f := &RulesFuture{done: make(chan struct{}), started: closedChan(), verdict: v}
	close(f.done)
	return f
}

type rulesPrejudged struct {
	key     string
	verdict *RulesFuture
	used    bool
}

// RulesGuard is the rules guard for one session: it loads and caches the rule sources, judges each write or edit on its
// own request (fired together with the action guard's), prejudges sibling writes so their requests overlap, and counts
// hits per rule so a repeat becomes a standing rule.
type RulesGuard struct {
	Store *RuleStore

	mu        sync.Mutex
	inflight  sync.WaitGroup
	prejudged map[string]*rulesPrejudged
	counts    map[string]int
	noted     map[string]bool
	capNoted  bool
}

// NewRulesGuard returns a guard with no history.
func NewRulesGuard() *RulesGuard {
	return &RulesGuard{Store: NewRuleStore(), prejudged: map[string]*rulesPrejudged{}, counts: map[string]int{}, noted: map[string]bool{}}
}

// start judges one call in the background; the judge it uses tells when the request reaches it.
func (g *RulesGuard) start(tool string, input map[string]any, options RulesOptions, detached bool) *RulesFuture {
	f := &RulesFuture{done: make(chan struct{}), started: make(chan struct{})}
	if options.Judge != nil {
		options.Judge = &startNotifier{Judge: options.Judge, started: f.started}
	} else {
		close(f.started)
	}
	options.Ctx = options.ctx()
	if detached {
		// A sibling's request outlives the call that started it: it ends by its own timeout.
		options.Ctx = context.WithoutCancel(options.Ctx)
	}
	g.inflight.Add(1)
	go func() {
		defer g.inflight.Done()
		defer close(f.done)
		defer func() {
			if recover() != nil {
				f.verdict = RulesVerdict{Source: "error", Tool: "write", Error: "TypeSafe request failed."}
			}
		}()
		f.verdict = evaluateRules(tool, input, options)
	}()
	return f
}

// Inspect starts the judgment of one write or edit and returns it; siblings (the other calls of the same assistant
// message) are prejudged at the same time so their requests overlap. It returns once every request has reached the
// judge.
func (g *RulesGuard) Inspect(call RulesCallRef, siblings []RulesCallRef, options RulesOptions) *RulesFuture {
	set := g.Store.Load(options.Cwd, options.Config)
	// A write or edit to a gitignored path is not project code; the rules in pi-warden.md do not apply. Only checked
	// when a rule set exists: no set means no rules file and skipReason already handles that.
	if set != nil {
		if rawPath, ok := call.Input["path"].(string); ok && rawPath != "" {
			if rel, inside := rulesProjectPath(rawPath, options.Cwd); inside && gitIgnored(rel, options.Cwd, false) {
				tool := "write"
				if call.Tool == "edit" {
					tool = "edit"
				}
				return resolvedFuture(RulesVerdict{Source: "skipped", Tool: tool, Path: rel, Sources: set.Sources, SkippedReason: "gitignored by the project"})
			}
		}
	}
	options.Set = set
	g.mu.Lock()
	var started []*RulesFuture
	if options.Judge != nil {
		for _, sibling := range siblings {
			if sibling.ID == call.ID || g.prejudged[sibling.ID] != nil || (sibling.Tool != "write" && sibling.Tool != "edit") {
				continue
			}
			f := g.start(sibling.Tool, sibling.Input, options, true)
			g.prejudged[sibling.ID] = &rulesPrejudged{key: inputKey(sibling.Input), verdict: f}
			started = append(started, f)
		}
	}
	key := inputKey(call.Input)
	var pending *RulesFuture
	if ready := g.prejudged[call.ID]; ready != nil && !ready.used && ready.key == key {
		pending = ready.verdict
	} else {
		pending = g.start(call.Tool, call.Input, options, false)
		started = append(started, pending)
	}
	g.prejudged[call.ID] = &rulesPrejudged{key: key, verdict: pending, used: true}
	g.mu.Unlock()
	for _, f := range started {
		select {
		case <-f.started:
		case <-f.done:
		case <-options.ctx().Done():
		}
	}
	return pending
}

// Wait blocks until every request in flight has finished (a prejudgment nobody used is not cancelled: it ends by its
// own timeout).
func (g *RulesGuard) Wait() { g.inflight.Wait() }

// Count records the findings and returns the per-rule session counts the steer text uses.
func (g *RulesGuard) Count(verdict RulesVerdict) map[string]int {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, finding := range verdict.Findings {
		g.counts[finding.ID]++
	}
	out := make(map[string]int, len(g.counts))
	for k, v := range g.counts {
		out[k] = v
	}
	return out
}

// NotesFor are the sensitive-path notes not yet given for this path in this session.
func (g *RulesGuard) NotesFor(path string, notes map[string]string) []PathNote {
	var out []PathNote
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, hit := range pathNotes(path, notes) {
		key := hit.Glob + "\x00" + path
		if g.noted[key] {
			continue
		}
		g.noted[key] = true
		out = append(out, hit)
	}
	return out
}

// CapNotice is the notice for the first write this session that had rules past the cap; "" after that.
func (g *RulesGuard) CapNotice(verdict RulesVerdict) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.capNoted || verdict.Dropped == 0 || verdict.FirstDropped == "" {
		return ""
	}
	g.capNoted = true
	return "warden · rules · " + verdict.Path + ": " + strconv.Itoa(verdict.Dropped) + " " + pluralOf(verdict.Dropped, "rule", "rules") + " from " + strings.Join(verdict.Sources, ", ") +
		" not judged, past the " + strconv.Itoa(maxRules) + "-question cap, starting with " + verdict.FirstDropped +
		". A `paths:` line on a rule keeps it out of requests for other files. Shown once per session."
}

// Describe is the one-line description of the rule set in force.
func (g *RulesGuard) Describe(cwd string, config RulesConfig) string {
	return describeRuleSet(g.Store.Load(cwd, config))
}

// Details is the detailed local-only view for `/warden rules`.
func (g *RulesGuard) Details(cwd string, config RulesConfig) string {
	set, tier := g.Store.LoadTiered(cwd, config)
	text := formatRuleSetDetails(set, tier, config.Exclude)
	if !config.Enabled {
		return "Rules guard is off (rules.enabled: false). These would apply:\n" + text
	}
	return text
}

// TurnEnd forgets the prejudgments of the turn.
func (g *RulesGuard) TurnEnd() {
	g.mu.Lock()
	g.prejudged = map[string]*rulesPrejudged{}
	g.mu.Unlock()
}

// Reset starts the session over.
func (g *RulesGuard) Reset() {
	g.mu.Lock()
	g.prejudged = map[string]*rulesPrejudged{}
	g.counts = map[string]int{}
	g.noted = map[string]bool{}
	g.capNoted = false
	g.mu.Unlock()
}

// ---------------------------------------------------------------------------
// The verdict line and trace details (widget.ts rulesTokens, trace.ts rulesDetails).

// rulesTokens are a verdict's template values.
func rulesTokens(verdict RulesVerdict, at time.Time) Tokens {
	t := Tokens{"guard": "rules", "time": clock(at), "tool": verdict.Tool, "path": verdict.Path, "source": verdict.Source, "model": verdict.Model}
	if verdict.Source != "skipped" {
		t["asked"] = strconv.Itoa(verdict.Asked)
	}
	switch {
	case len(verdict.Findings) > 0:
		parts := make([]string, len(verdict.Findings))
		for i, f := range verdict.Findings {
			parts[i] = f.Name + " " + toFixed(f.Violation, 2)
		}
		t["violations"] = strings.Join(parts, ", ")
	case verdict.Scores != nil:
		t["violations"] = "none"
	}
	switch {
	case verdict.Source == "error":
		t["status"] = "typesafe error"
	case verdict.Source == "skipped":
		t["status"] = "skipped"
	case len(verdict.Findings) > 0:
		t["status"] = "violation"
	default:
		t["status"] = "ok"
	}
	switch {
	case verdict.SkippedReason != "":
		t["reasons"] = verdict.SkippedReason
	case len(verdict.Findings) > 0:
		names := make([]string, len(verdict.Findings))
		for i, f := range verdict.Findings {
			names[i] = f.Name
		}
		t["reasons"] = strings.Join(names, "; ")
	}
	if verdict.ElapsedMs != nil {
		t["ms"] = strconv.Itoa(*verdict.ElapsedMs)
	}
	if verdict.Error != "" {
		t["flags"] = "typesafe error"
	}
	return t
}

// formatRules is the one-line verdict for the trace and the widget.
func formatRules(verdict RulesVerdict, template string, at time.Time) string {
	return RenderTemplate(template, rulesTokens(verdict, at))
}

// rulesDetails are the trace lines of a rules verdict.
func rulesDetails(verdict RulesVerdict, told string) []string {
	sources := strings.Join(verdict.Sources, ", ")
	if sources == "" {
		sources = "nowhere"
	}
	one := ""
	if verdict.Aggregate {
		one = " (judged as one document)"
	}
	dropped := ""
	if verdict.Dropped > 0 {
		dropped = "; " + strconv.Itoa(verdict.Dropped) + " past the question cap from " + verdict.FirstDropped
	}
	lines := []string{verdict.Tool + " " + verdict.Path + "; rules from " + sources + one + "; " + strconv.Itoa(verdict.Asked) + " " + pluralOf(verdict.Asked, "question", "questions") + dropped}
	if len(verdict.Scores) > 0 {
		parts := make([]string, len(verdict.Scores))
		for i, s := range verdict.Scores {
			parts[i] = s.Name + " " + strings.ReplaceAll(s.Outcome, "_", " ") + " " + percent(s.Violation)
		}
		model := ""
		if verdict.Model != "" {
			ms := ""
			if verdict.ElapsedMs != nil {
				ms = strconv.Itoa(*verdict.ElapsedMs)
			}
			model = " · " + verdict.Model + " · " + ms + " ms"
		}
		lines = append(lines, "jev: "+strings.Join(parts, " · ")+model)
	}
	if len(verdict.Findings) > 0 {
		parts := make([]string, len(verdict.Findings))
		for i, f := range verdict.Findings {
			parts[i] = f.Name + " (" + percent(f.Violation) + ")"
		}
		in := ""
		if verdict.EditID != "" {
			in = " in " + strings.Replace(verdict.EditID, "_", " ", 1)
		}
		lines = append(lines, "violations: "+strings.Join(parts, "; ")+in)
	}
	if verdict.SkippedReason != "" {
		lines = append(lines, "skipped: "+verdict.SkippedReason)
	}
	if verdict.Error != "" {
		lines = append(lines, "typesafe: "+verdict.Error)
	}
	if told != "" {
		shown := told
		if utf16Len(told) > 400 {
			shown = utf16Slice(told, 0, 400) + "…"
		}
		lines = append(lines, "agent told: "+shown)
	}
	return lines
}
