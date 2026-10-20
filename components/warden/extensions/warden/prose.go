package warden

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode"
)

// The prose check (src/prose.ts): the final reply is scored against the configured audience for wordiness,
// assistant clichés and jargon; a trend across the last three replies earns one nudge for the next reply.
// Restatement is measured in code only and is never a steer.

// ProseSymptom is one thing the prose check looks for.
type ProseSymptom string

// Prose symptoms.
const (
	ProseWordy   ProseSymptom = "wordy"
	ProseCliches ProseSymptom = "cliches"
	ProseJargon  ProseSymptom = "jargon"
)

// ProseSymptoms is every symptom, in upstream order.
var ProseSymptoms = []ProseSymptom{ProseWordy, ProseCliches, ProseJargon}

var proseQuestions = Questions{
	"wordy": Noul("Is `reply` longer than its content requires: a preamble, restating the request, summarising what it just said, or filler sentences that add no information?",
		"Yes: it opens with what it is about to do, repeats the question, ends with a summary of the same points, or pads with sentences that could be removed without losing anything.",
		"No: each sentence carries information the reader needs; length matches the substance."),
	"cliches": Noul("Does `reply` use assistant clichés or filler phrasing: \"Great question\", \"Certainly!\", \"I hope this helps\", \"it's worth noting\", \"delve\", \"let me know if\", unrequested bullet lists of caveats, or emoji headings?",
		"Yes: one or more such phrases or patterns are present.",
		"No: the wording is direct and specific to the subject."),
	"jargon": Noul("Would a reader described by `audience` struggle with `reply` because of unexplained technical terms or assumed background?",
		"Yes: terms, acronyms, or concepts appear without explanation that this audience would not know.",
		"No: the reply matches what this audience can follow, or the audience is technical."),
}

// ProseLabels name each symptom for the nudge.
var ProseLabels = map[ProseSymptom]string{
	ProseWordy:   "longer than the content needs (preamble, restating, summarising, filler)",
	ProseCliches: "assistant clichés or filler phrasing",
	ProseJargon:  "unexplained technical terms for the configured audience",
}

// DescribeAudience maps the two built-in audience names to descriptions the judge can score against; anything else
// is used as written.
func DescribeAudience(audience string) string {
	switch strings.ToLower(audience) {
	case "technical":
		return "a software developer who knows this codebase and its tools"
	case "plain":
		return "a non-programmer who owns the product and reads the reply as a status update"
	}
	return audience
}

// ProseVerdict is the outcome of one prose check.
type ProseVerdict struct {
	// Scores is nil when the judge failed.
	Scores map[ProseSymptom]float64
	// Flagged: symptoms at or above the threshold for this reply, strongest first.
	Flagged []ProseSymptom
	// Nudged is set by the caller when a nudge was sent.
	Nudged    bool
	Model     string
	ElapsedMs int
	Error     string
	ErrorCode string
}

// BuildProseRequest is prose.ts `buildProseRequest`. An empty task means none was recorded.
func BuildProseRequest(task, reply, audience string) Request {
	shown := "(no user request recorded in this session)"
	if trimmed := jsTrim(task); trimmed != "" {
		shown = trimmed
		if utf16Len(trimmed) > 1000 {
			shown = utf16Slice(trimmed, 0, 1000) + "…"
		}
	}
	if utf16Len(reply) > 2500 {
		reply = utf16Slice(reply, 0, 2500) + "…"
	}
	return Request{
		State:     map[string]any{"task": shown, "audience": DescribeAudience(audience), "reply": Redact(reply)},
		Questions: proseQuestions,
	}
}

// ProseOptions configure EvaluateProse.
type ProseOptions struct {
	Config  ProseConfig
	Judge   Judge
	Timeout time.Duration
}

// EvaluateProse scores one reply.
func EvaluateProse(ctx context.Context, task, reply string, opts ProseOptions) ProseVerdict {
	res := Ask(ctx, opts.Judge, BuildProseRequest(task, reply, opts.Config.Audience), opts.Timeout)
	if !res.OK {
		return ProseVerdict{Error: res.Error, ErrorCode: res.ErrorCode}
	}
	scores := map[ProseSymptom]float64{}
	for _, s := range ProseSymptoms {
		scores[s] = res.Answers[string(s)].Noul
	}
	var flagged []ProseSymptom
	for _, s := range ProseSymptoms {
		if scores[s] >= opts.Config.Threshold {
			flagged = append(flagged, s)
		}
	}
	sort.SliceStable(flagged, func(i, j int) bool { return scores[flagged[i]] > scores[flagged[j]] })
	return ProseVerdict{Scores: scores, Flagged: flagged, Model: res.Model, ElapsedMs: res.ElapsedMs}
}

// ProseTrend remembers the last three replies' symptoms. A nudge needs a symptom in `trend` of them, so one long
// answer to a long question is not punished, and after a nudge the next two replies are given time to change.
type ProseTrend struct {
	history  [][]ProseSymptom
	cooldown int
	Counts   map[ProseSymptom]int
}

// NewProseTrend returns an empty trend.
func NewProseTrend() *ProseTrend {
	return &ProseTrend{Counts: map[ProseSymptom]int{ProseWordy: 0, ProseCliches: 0, ProseJargon: 0}}
}

// Record adds one reply's flagged symptoms.
func (p *ProseTrend) Record(flagged []ProseSymptom) {
	p.history = append(p.history, append([]ProseSymptom(nil), flagged...))
	if len(p.history) > 3 {
		p.history = p.history[1:]
	}
	for _, s := range flagged {
		p.Counts[s]++
	}
	if p.cooldown > 0 {
		p.cooldown--
	}
}

// Due returns the symptoms that crossed the trend requirement, or none during cool-down.
func (p *ProseTrend) Due(trend int) []ProseSymptom {
	if p.cooldown > 0 {
		return nil
	}
	var due []ProseSymptom
	for _, s := range ProseSymptoms {
		n := 0
		for _, set := range p.history {
			for _, got := range set {
				if got == s {
					n++
					break
				}
			}
		}
		if n >= trend {
			due = append(due, s)
		}
	}
	return due
}

// MarkNudged starts the cool-down.
func (p *ProseTrend) MarkNudged() { p.cooldown = 2 }

// Reset clears history, cool-down and counts.
func (p *ProseTrend) Reset() {
	p.history = nil
	p.cooldown = 0
	for _, s := range ProseSymptoms {
		p.Counts[s] = 0
	}
}

// Restatement: a reply whose substantive sentences were already sent earlier in the same run.
const (
	// SentenceMinChars: sentences shorter than this (as content words) carry nothing worth comparing.
	SentenceMinChars = 24
	// SentenceOverlap: a sentence sharing this share of another's content words restates it.
	SentenceOverlap = 0.7
	// RestateShare: a reply restates when at least this share of its substantive sentences was already sent this run.
	RestateShare = 0.5
	// RestateMinSentences: restatement needs more than one substantive sentence.
	RestateMinSentences = 2
)

var reSentenceBreak = lazyRE(`(?:[.!?]|\n)+` + jsSpaceClass + `*`)

// sentenceWords is one sentence's content-word set; order keeps the first sighting, as a JS Set does.
type sentenceWords struct {
	set   map[string]bool
	order []string
}

func contentWords(sentence string) sentenceWords {
	out := sentenceWords{set: map[string]bool{}}
	words := strings.FieldsFunc(strings.ToLower(sentence), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	for _, w := range words {
		if utf16Len(w) > 2 && !out.set[w] {
			out.set[w] = true
			out.order = append(out.order, w)
		}
	}
	return out
}

// SubstantiveSentences are the substantive sentences of a reply, as content-word sets.
func SubstantiveSentences(text string) []sentenceWords {
	var out []sentenceWords
	for _, raw := range reSentenceBreak.Split(text, -1) {
		words := contentWords(raw)
		if utf16Len(strings.Join(words.order, " ")) >= SentenceMinChars {
			out = append(out, words)
		}
	}
	return out
}

// RestatedShare is the share of reply's substantive sentences that restate a sentence of the earlier replies (0..1).
func RestatedShare(reply string, earlier []string) float64 {
	mine := SubstantiveSentences(reply)
	var pool []sentenceWords
	for _, text := range earlier {
		pool = append(pool, SubstantiveSentences(text)...)
	}
	if len(mine) == 0 || len(pool) == 0 {
		return 0
	}
	restated := 0
sentence:
	for _, s := range mine {
		for _, old := range pool {
			shared := 0
			for _, w := range s.order {
				if old.set[w] {
					shared++
				}
			}
			if float64(shared)/float64(len(s.order)) >= SentenceOverlap {
				restated++
				continue sentence
			}
		}
	}
	return float64(restated) / float64(len(mine))
}

// RestatementWindow remembers the final messages of the current run; reset with each user prompt, since answering
// the user is never a restatement.
type RestatementWindow struct {
	finals []string
	limit  int
}

// NewRestatementWindow returns a window of the last limit finals (upstream's default is 4).
func NewRestatementWindow(limit int) *RestatementWindow { return &RestatementWindow{limit: limit} }

// Share is the 0..1 share of the reply already stated in an earlier final of this run.
func (w *RestatementWindow) Share(reply string) float64 { return RestatedShare(reply, w.finals) }

// Record remembers a final reply.
func (w *RestatementWindow) Record(reply string) {
	w.finals = append(w.finals, reply)
	if len(w.finals) > w.limit {
		w.finals = w.finals[1:]
	}
}

// Reset forgets every final.
func (w *RestatementWindow) Reset() { w.finals = nil }

// ProseNudge is queued for the next user prompt, so it shapes the next reply without spending a turn.
func ProseNudge(symptoms []ProseSymptom, audience string, counts map[ProseSymptom]int) string {
	has := func(s ProseSymptom) bool {
		for _, got := range symptoms {
			if got == s {
				return true
			}
		}
		return false
	}
	var parts, fixes []string
	standing := false
	for _, s := range symptoms {
		part := ProseLabels[s]
		if counts[s] >= 3 {
			part += " (" + itoaInt(counts[s]) + " replies this session)"
			standing = true
		}
		parts = append(parts, part)
	}
	if has(ProseWordy) {
		fixes = append(fixes, "lead with the answer, cut preambles and closing summaries, and stop when the information is complete")
	}
	if has(ProseCliches) {
		fixes = append(fixes, "drop filler phrases and unrequested caveat lists")
	}
	if has(ProseJargon) {
		fixes = append(fixes, "write for "+DescribeAudience(audience)+": explain or replace technical terms")
	}
	suffix := ""
	if standing {
		suffix = " Treat this as a standing rule for the rest of the session."
	}
	return "pi-warden: your recent replies read as " + strings.Join(parts, "; ") + ". From the next reply on, " + strings.Join(fixes, "; ") + "." + suffix
}

// ProseTokens are the widget template values of a prose verdict (widget.ts proseTokens).
func ProseTokens(v ProseVerdict, at time.Time) Tokens {
	t := Tokens{"guard": "prose", "time": clock(at)}
	if v.Scores != nil {
		t["wordy"] = toFixed(v.Scores[ProseWordy], 2)
		t["cliches"] = toFixed(v.Scores[ProseCliches], 2)
		t["jargon"] = toFixed(v.Scores[ProseJargon], 2)
	}
	flagged := make([]string, len(v.Flagged))
	for i, s := range v.Flagged {
		flagged[i] = string(s)
	}
	switch {
	case v.Nudged:
		t["status"] = "nudged"
	case len(flagged) > 0:
		t["status"] = strings.Join(flagged, ", ")
	default:
		t["status"] = "ok"
	}
	if len(flagged) > 0 {
		t["reasons"] = strings.Join(flagged, ", ")
	}
	put(t, "model", v.Model)
	if v.Model != "" || v.Scores != nil {
		t["ms"] = itoaInt(v.ElapsedMs)
	}
	if v.Error != "" {
		t["flags"] = "typesafe error"
	}
	return t
}

// ProseDetails are the trace detail lines of a prose verdict (trace.ts proseDetails).
func ProseDetails(v ProseVerdict, reply, audience, told string) []string {
	lines := []string{"reply: " + clipEllipsis(reWhitespaceRun.ReplaceAllString(reply, " "), 300), "audience: " + audience}
	if v.Scores != nil {
		line := "jev: wordy " + percent(v.Scores[ProseWordy]) + " · clichés " + percent(v.Scores[ProseCliches]) + " · jargon " + percent(v.Scores[ProseJargon])
		if v.Model != "" {
			line += " · " + v.Model + " · " + itoaInt(v.ElapsedMs) + " ms"
		}
		lines = append(lines, line)
	}
	if len(v.Flagged) > 0 {
		flagged := make([]string, len(v.Flagged))
		for i, s := range v.Flagged {
			flagged[i] = string(s)
		}
		lines = append(lines, "flagged: "+strings.Join(flagged, ", "))
	}
	if v.Error != "" {
		lines = append(lines, "typesafe: "+v.Error)
	}
	if told != "" {
		lines = append(lines, "agent told: "+clipEllipsis(told, 400))
	}
	return lines
}
