package warden

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Per-model steer adaptation (src/adaptive.ts). A steer kind that a model follows rarely or disputes often becomes
// trace-only for that model: on recorded sessions intent-mismatch notices were followed 8% of the time, the
// done-check 77%, and strong models dispute noise ("as I said ..."). Holds and per-call judgments never change;
// only whether a note is sent. The kinds, pairs and outcomes are declared with the steering core (steer_core.go);
// this file is the store and the watch behind its steerAdaptor.

// SteerProbe is the decision for a muted pair's re-check steer: it is still sent (adaptive.ts "probe").
const SteerProbe SteerDecision = "probe"

// steerStatsEnv overrides the stats file location (the original's PI_WARDEN_STEER_STATS, PIGPEN_WARDEN_ prefixed: D6).
const steerStatsEnv = "PIGPEN_WARDEN_STEER_STATS"

// SteerStatsPath is where the counts live: next to the user config, in warden's data folder.
func SteerStatsPath(dataDir string, getenv func(string) string) string {
	if p := getenv(steerStatsEnv); p != "" {
		return p
	}
	return filepath.Join(dataDir, "steer-stats.json")
}

// followMeasured lists the kinds whose "followed" can be read from the next calls or text; the rest are judged by
// disputes alone (adaptive.ts FOLLOW_MEASURED).
var followMeasured = map[SteerKind]bool{
	SteerKindIntentMismatch: true, SteerKindOffTask: true, SteerKindShouldProceed: true, SteerKindWarnHeadless: true,
	SteerKindLargeOutput: true, SteerKindSlop: true, SteerKindRules: true, SteerKindSecurityWrite: true,
	SteerKindStuck: true, SteerKindRepeat: true, SteerKindDone: true, SteerKindConscience: true, SteerKindLoops: true,
}

// steerFailing is true when the counts put the pair under the follow floor or over the dispute ceiling.
func steerFailing(kind SteerKind, c SteerCounts, cfg SteersConfig) bool {
	if c.Sent == 0 {
		return false
	}
	sent := float64(c.Sent)
	return (followMeasured[kind] && float64(c.Followed)/sent < cfg.MinFollowed) || float64(c.Disputed)/sent > cfg.MaxDisputed
}

// counts is one set of tallies as the stats file stores it.
type counts struct {
	Sent     int `json:"sent"`
	Followed int `json:"followed"`
	Disputed int `json:"disputed"`
}

// pairState is PairState with the file's key order (sent, followed, disputed, muted, asked, probe).
type pairState struct {
	counts
	Muted bool   `json:"muted"`
	Asked int    `json:"asked"`
	Probe counts `json:"probe"`
}

func (p *pairState) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Sent     int    `json:"sent"`
		Followed int    `json:"followed"`
		Disputed int    `json:"disputed"`
		Muted    bool   `json:"muted"`
		Asked    int    `json:"asked"`
		Probe    counts `json:"probe"`
	}{p.Sent, p.Followed, p.Disputed, p.Muted, p.Asked, p.Probe})
}

func (p *pairState) UnmarshalJSON(b []byte) error {
	var v struct {
		Sent     int    `json:"sent"`
		Followed int    `json:"followed"`
		Disputed int    `json:"disputed"`
		Muted    bool   `json:"muted"`
		Asked    int    `json:"asked"`
		Probe    counts `json:"probe"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*p = pairState{counts: counts{v.Sent, v.Followed, v.Disputed}, Muted: v.Muted, Asked: v.Asked, Probe: v.Probe}
	return nil
}

func (p *pairState) public() SteerPair {
	return SteerPair{SteerCounts: SteerCounts{p.Sent, p.Followed, p.Disputed}, Muted: p.Muted, Asked: p.Asked, Probe: SteerCounts{p.Probe.Sent, p.Probe.Followed, p.Probe.Disputed}}
}

// ordered is a JSON object that keeps its keys in insertion order, as a JavaScript object does, so the stats file
// is written the way the original writes it.
type ordered[V any] struct {
	keys []string
	m    map[string]V
}

func newOrdered[V any]() *ordered[V] { return &ordered[V]{m: map[string]V{}} }

func (o *ordered[V]) get(k string) (V, bool) { v, ok := o.m[k]; return v, ok }

func (o *ordered[V]) set(k string, v V) {
	if _, ok := o.m[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.m[k] = v
}

func (o *ordered[V]) del(k string) {
	if _, ok := o.m[k]; !ok {
		return
	}
	delete(o.m, k)
	for i, key := range o.keys {
		if key == k {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

func (o *ordered[V]) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, err := marshalNoEscape(k)
		if err != nil {
			return nil, err
		}
		buf.Write(kb)
		buf.WriteByte(':')
		vb, err := marshalNoEscape(o.m[k])
		if err != nil {
			return nil, err
		}
		buf.Write(vb)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func (o *ordered[V]) UnmarshalJSON(b []byte) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return &json.UnmarshalTypeError{Value: "non-object", Type: nil}
	}
	o.keys, o.m = nil, map[string]V{}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return err
		}
		var v V
		if err := dec.Decode(&v); err != nil {
			return err
		}
		o.set(kt.(string), v)
	}
	return nil
}

// marshalNoEscape is json.Marshal without the HTML escaping, without the trailing newline.
func marshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

type statsFile struct {
	Version int                            `json:"version"`
	Models  *ordered[*ordered[*pairState]] `json:"models"`
}

// SteerStats holds the counts per (model, kind) pair, read once and written after every change (adaptive.ts SteerStats).
type SteerStats struct {
	mu   sync.Mutex
	path string
	// pathFn resolves the path on first use when it is not known at construction (the data folder is known once the
	// config has loaded); an empty path keeps the counts in memory only.
	pathFn func() string
	data   *statsFile
	// Warn receives a stats write failure; nil drops it.
	Warn func(string)
}

// NewSteerStats opens the store at path (read lazily).
func NewSteerStats(path string) *SteerStats { return &SteerStats{path: path} }

// NewSteerStatsAt opens the store at the path pathFn answers on first use.
func NewSteerStatsAt(pathFn func() string) *SteerStats { return &SteerStats{pathFn: pathFn} }

func (s *SteerStats) load() *statsFile {
	if s.data != nil {
		return s.data
	}
	fresh := &statsFile{Version: 1, Models: newOrdered[*ordered[*pairState]]()}
	if s.path == "" && s.pathFn != nil {
		s.path = s.pathFn()
	}
	if s.path == "" {
		s.data = fresh
		return s.data
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		s.data = fresh
		return s.data
	}
	var f statsFile
	if json.Unmarshal(raw, &f) != nil || f.Version != 1 || f.Models == nil {
		s.data = fresh
		return s.data
	}
	for _, k := range f.Models.keys {
		if f.Models.m[k] == nil {
			f.Models.m[k] = newOrdered[*pairState]()
		}
	}
	s.data = &f
	return s.data
}

func (s *SteerStats) save() {
	if s.path == "" {
		return
	}
	b, err := marshalNoEscape(s.data)
	if err == nil {
		err = WriteFileAtomic(s.path, append(b, '\n'))
	}
	if err != nil && s.Warn != nil {
		s.Warn("pi-warden: steer stats write failed: " + err.Error())
	}
}

func (s *SteerStats) pairLocked(model string, kind SteerKind) *pairState {
	kinds, ok := s.load().Models.get(model)
	if !ok {
		return nil
	}
	p, _ := kinds.get(string(kind))
	return p
}

func (s *SteerStats) pair(model string, kind SteerKind) (SteerPair, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.pairLocked(model, kind)
	if p == nil {
		return SteerPair{}, false
	}
	return p.public(), true
}

func (s *SteerStats) ensure(model string, kind SteerKind) *pairState {
	models := s.load().Models
	kinds, ok := models.get(model)
	if !ok {
		kinds = newOrdered[*pairState]()
		models.set(model, kinds)
	}
	p, ok := kinds.get(string(kind))
	if !ok {
		p = &pairState{}
		kinds.set(string(kind), p)
	}
	return p
}

// request is called before a steer of this kind goes out. A trace-only pair still sends the first of every
// probeEvery steers; after recheckEvery steers the probes decide: rates within the thresholds end trace-only, and
// the probe counts become the pair's new counts, so a fresh minSteers must pass before it can be muted again.
func (s *SteerStats) request(model string, kind SteerKind, cfg SteersConfig) SteerDecision {
	if !cfg.Adaptive || NeverMuted[kind] {
		return SteerSend
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.pairLocked(model, kind)
	if p == nil || !p.Muted {
		return SteerSend
	}
	probe := p.Asked%max(1, cfg.ProbeEvery) == 0
	p.Asked++
	if p.Asked >= cfg.RecheckEvery {
		if p.Probe.Sent > 0 && !steerFailing(kind, SteerCounts{p.Probe.Sent, p.Probe.Followed, p.Probe.Disputed}, cfg) {
			*p = pairState{counts: p.Probe}
		} else {
			p.Asked, p.Probe = 0, counts{}
		}
	}
	s.save()
	if probe {
		return SteerProbe
	}
	return SteerTraceOnly
}

// observe records the outcome of one sent steer. Crossing the thresholds at minSteers or later makes the pair
// trace-only. Kinds in NeverMuted are counted, for the status, and never muted.
func (s *SteerStats) observe(model string, kind SteerKind, outcome SteerOutcome, cfg SteersConfig) {
	if !cfg.Adaptive {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.ensure(model, kind)
	add := func(c *counts) {
		c.Sent++
		if outcome.Followed {
			c.Followed++
		}
		if outcome.Disputed {
			c.Disputed++
		}
	}
	add(&p.counts)
	if p.Muted {
		add(&p.Probe)
	} else if !NeverMuted[kind] && p.Sent >= cfg.MinSteers && steerFailing(kind, SteerCounts{p.Sent, p.Followed, p.Disputed}, cfg) {
		p.Muted, p.Asked, p.Probe = true, 0, counts{}
	}
	s.save()
}

// unmute drops the pair's counts; its steers are sent again. True when there was something to reset.
func (s *SteerStats) unmute(model string, kind SteerKind) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	models := s.load().Models
	kinds, ok := models.get(model)
	if !ok {
		return false
	}
	if _, ok := kinds.get(string(kind)); !ok {
		return false
	}
	kinds.del(string(kind))
	if len(kinds.keys) == 0 {
		models.del(model)
	}
	s.save()
	return true
}

// muted lists every trace-only pair, per model.
func (s *SteerStats) muted() []MutedPair {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []MutedPair
	models := s.load().Models
	for _, model := range models.keys {
		kinds := models.m[model]
		for _, kind := range kinds.keys {
			if p := kinds.m[kind]; p.Muted {
				out = append(out, MutedPair{Model: model, Kind: SteerKind(kind), SteerCounts: SteerCounts{p.Sent, p.Followed, p.Disputed}})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Model != out[j].Model {
			return localeLess(out[i].Model, out[j].Model)
		}
		return localeLess(string(out[i].Kind), string(out[j].Kind))
	})
	return out
}

// localeLess approximates String.prototype.localeCompare for the ASCII model and kind names the store holds.
func localeLess(a, b string) bool {
	la, lb := strings.ToLower(a), strings.ToLower(b)
	if la != lb {
		return la < lb
	}
	return a > b // lower case sorts before upper case in ICU collation
}

// modelsWith lists the models with a pair of this kind, for `/warden unmute` without a model.
func (s *SteerStats) modelsWith(kind SteerKind) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	models := s.load().Models
	for _, model := range models.keys {
		if _, ok := models.m[model].get(string(kind)); ok {
			out = append(out, model)
		}
	}
	return out
}

func steerPercent(part, whole int) string {
	if whole == 0 {
		return "0%"
	}
	return strconv.Itoa(jsRound(float64(part)/float64(whole)*100)) + "%"
}

// FormatMuted is the status line about trace-only pairs (adaptive.ts formatMuted).
func FormatMuted(pairs []MutedPair, cfg SteersConfig) string {
	if !cfg.Adaptive {
		return "Adaptive steers: off."
	}
	if len(pairs) == 0 {
		return "Adaptive steers: no steer kind is trace-only for any model."
	}
	var order []string
	byModel := map[string][]MutedPair{}
	for _, p := range pairs {
		if _, ok := byModel[p.Model]; !ok {
			order = append(order, p.Model)
		}
		byModel[p.Model] = append(byModel[p.Model], p)
	}
	models := make([]string, 0, len(order))
	for _, model := range order {
		var parts []string
		for _, p := range byModel[model] {
			parts = append(parts, string(p.Kind)+" ("+strconv.Itoa(p.Sent)+" steers, "+steerPercent(p.Followed, p.Sent)+" followed, "+steerPercent(p.Disputed, p.Sent)+" disputed)")
		}
		models = append(models, model+": "+strings.Join(parts, ", "))
	}
	return "Adaptive steers, trace-only per model: " + strings.Join(models, "; ") + ". /warden unmute <kind> resets a pair."
}

// ---------------------------------------------------------------------------
// Outcome of a sent steer.

// disputePattern: agent replies that argue with a note instead of acting on it (adaptive.ts DISPUTE).
var disputePattern = lazyRE(`(?i)false positive|false alarm|no (real )?(credentials|secrets)|not (a |real )?(secret|credential|token)s?|(only|just) (placeholder|fixture|test|dummy|fake)|as (I )?(said|planned|stated)|matches (what I said|the plan)|this is expected|warden (is wrong|misfired|misread)|misfire|spurious|harmless|benign|nothing sensitive|not sensitive|was intended|intended`)

var (
	checkPattern      = lazyRE(`(?i)\b(test|tests|vitest|jest|pytest|build|lint|tsc|check|make|cargo|go test|flutter (test|analyze)|npm run|eslint|typecheck)\b`)
	unverifiedPattern = lazyRE(`(?i)not (yet )?(verified|tested)|unverified|did not run|didn't run`)
	replanPattern     = lazyRE(`(?i)\b(instead|re-?plan(ned|ning)?|ask(ing)? (you|the user|first|before)|should I|do you want)\b`)
)

// commandText is what a call's input says: the command, else the path, else the whole input.
func commandText(input map[string]any) string {
	if s, ok := input["command"].(string); ok {
		return s
	}
	if s, ok := input["path"].(string); ok {
		return s
	}
	b, err := marshalNoEscape(input)
	if err != nil {
		return ""
	}
	return string(b)
}

// endsWithQuestion is /\?\s*$/ with JavaScript's whitespace.
func endsWithQuestion(text string) bool {
	return strings.HasSuffix(strings.TrimRightFunc(text, isJSSpace), "?")
}

// steerOutcome says whether the next assistant messages (at most two) changed course after a steer of this kind,
// and whether they disputed it. A port of the heuristic the field replays used; it is a proxy and does not prove the
// note caused the change.
func steerOutcome(kind SteerKind, subject SteerSubject, views []AssistantView) (followed, disputed bool) {
	texts := make([]string, len(views))
	var calls []SteerCall
	for i, v := range views {
		texts[i] = v.Text
		calls = append(calls, v.Calls...)
	}
	text := strings.Join(texts, "\n")
	disputed = disputePattern.MatchString(text)
	switch kind {
	case SteerKindDone:
		for _, c := range calls {
			if c.Name == "bash" && checkPattern.MatchString(commandText(c.Input)) {
				followed = true
			}
		}
		followed = followed || unverifiedPattern.MatchString(text)
	case SteerKindRules, SteerKindSlop, SteerKindSecurityWrite:
		path := subject.Path
		if path != "" {
			for _, c := range calls {
				if containsAny(c.Name, "edit", "write") {
					if strings.HasSuffix(commandText(c.Input), path) {
						followed = true
					}
				} else if c.Name == "bash" && strings.Contains(commandText(c.Input), path) {
					followed = true
				}
			}
		}
	case SteerKindStuck, SteerKindRepeat:
		if len(calls) > 0 {
			next := calls[0]
			followed = subject.Call == nil || next.Name != subject.Call.Name || commandText(next.Input) != commandText(subject.Call.Input)
		}
	case SteerKindIntentMismatch, SteerKindOffTask, SteerKindShouldProceed, SteerKindWarnHeadless, SteerKindLargeOutput:
		first := ""
		if len(views) > 0 {
			first = views[0].Text
		}
		followed = (len(calls) == 0 && endsWithQuestion(first)) || replanPattern.MatchString(text)
	case SteerKindConscience:
		if t := subject.Capability; t != nil {
			for _, c := range calls {
				if t.Kind == "tool" {
					if c.Name == t.ID || strings.HasSuffix(c.Name, t.ID) {
						followed = true
					}
				} else if strings.Contains(commandText(c.Input), "/"+t.ID+"/") {
					followed = true
				}
			}
		}
	case SteerKindLoops:
		for _, c := range calls {
			if c.Name == "warden_loops" {
				followed = true
			}
		}
	}
	return followed, disputed
}

type pendingSteer struct {
	model    string
	kinds    []SteerKind
	subject  SteerSubject
	views    []AssistantView
	waitUser bool
}

// SteerWatch holds sent steers waiting for the agent's next two messages. A steer queued for the next turn waits
// past the next user message; any other steer that meets a user message first is settled on what it saw, or dropped
// when it saw nothing (adaptive.ts SteerWatch).
type SteerWatch struct {
	mu      sync.Mutex
	pending []*pendingSteer
}

func (w *SteerWatch) add(model string, kinds []SteerKind, subject SteerSubject, nextTurn bool) {
	if len(kinds) == 0 {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending = append(w.pending, &pendingSteer{model: model, kinds: append([]SteerKind(nil), kinds...), subject: subject, waitUser: nextTurn})
	if len(w.pending) > 50 {
		w.pending = w.pending[1:]
	}
}

// assistant feeds one assistant message; it returns the steers it settled.
func (w *SteerWatch) assistant(view AssistantView) []SteerOutcome {
	w.mu.Lock()
	defer w.mu.Unlock()
	var settled []SteerOutcome
	var keep []*pendingSteer
	for _, item := range w.pending {
		if item.waitUser {
			keep = append(keep, item)
			continue
		}
		item.views = append(item.views, view)
		outcomes := make([]SteerOutcome, len(item.kinds))
		all := true
		for i, kind := range item.kinds {
			f, d := steerOutcome(kind, item.subject, item.views)
			outcomes[i] = SteerOutcome{Model: item.model, Kind: kind, Followed: f, Disputed: d}
			all = all && (f || d)
		}
		if len(item.views) >= 2 || all {
			settled = append(settled, outcomes...)
		} else {
			keep = append(keep, item)
		}
	}
	w.pending = keep
	return settled
}

// user notes that a user message arrived.
func (w *SteerWatch) user() []SteerOutcome {
	w.mu.Lock()
	defer w.mu.Unlock()
	var settled []SteerOutcome
	var keep []*pendingSteer
	for _, item := range w.pending {
		if item.waitUser {
			item.waitUser = false
			keep = append(keep, item)
			continue
		}
		if len(item.views) > 0 {
			for _, kind := range item.kinds {
				f, d := steerOutcome(kind, item.subject, item.views)
				settled = append(settled, SteerOutcome{Model: item.model, Kind: kind, Followed: f, Disputed: d})
			}
		}
	}
	w.pending = keep
	return settled
}

func (w *SteerWatch) clear() {
	w.mu.Lock()
	w.pending = nil
	w.mu.Unlock()
}

// adaptiveAdaptor is the steerAdaptor of the steering core: the stats store and the watch.
type adaptiveAdaptor struct {
	*SteerStats
	*SteerWatch
}

func newAdaptiveAdaptor(pathFn func() string, warn func(string)) *adaptiveAdaptor {
	s := NewSteerStatsAt(pathFn)
	s.Warn = warn
	return &adaptiveAdaptor{SteerStats: s, SteerWatch: &SteerWatch{}}
}

// assistantView reads the text and tool calls of an assistant message as the host stores it: a string, or content
// parts (text parts and toolCall parts). Anything else is empty.
func assistantView(content any) AssistantView {
	switch c := content.(type) {
	case string:
		return AssistantView{Text: c}
	case []any:
		var texts []string
		var calls []SteerCall
		for _, part := range c {
			m, ok := part.(map[string]any)
			if !ok {
				continue
			}
			switch m["type"] {
			case "text":
				if t, ok := m["text"].(string); ok {
					texts = append(texts, t)
				}
			case "toolCall":
				if name, ok := m["name"].(string); ok {
					args, _ := m["arguments"].(map[string]any)
					if args == nil {
						args = map[string]any{}
					}
					calls = append(calls, SteerCall{Name: name, Input: args})
				}
			}
		}
		return AssistantView{Text: strings.Join(texts, "\n"), Calls: calls}
	}
	return AssistantView{}
}
