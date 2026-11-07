package eq

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
)

// This file holds the scenario features that let a recorded scenario reproduce a Pi session suite:
// the scripted model's shape and outcomes, settings, real sessions, helper extensions, response values
// carried to later steps, triggers and captured files. Every field is opt-in; a scenario without them
// runs and records exactly as before they existed.

// DefaultModelID is the id of the scripted provider's primary model.
const DefaultModelID = "eq-1"

// FileRoots are the directories a FileCapture can name. workspace is the lane's working directory,
// agent its agent directory (settings.json, an extension's own files), sessions the --session-dir of a
// scenario with Session, home the host's own home (<PIG_HOME> on pig, <HOME>/.pi on pi).
var FileRoots = []string{"workspace", "agent", "sessions", "home"}

// FileCapture selects files whose state enters the trace (the files channel). JSON: a string
// "[root:]glob" (root one of FileRoots, default workspace) or an object.
type FileCapture struct {
	Root string `json:"root,omitempty"`
	// Glob is relative to Root and may use ** for any number of directories, plus * ? [..] within one name.
	Glob string `json:"glob"`
	// Exclude globs (same syntax) remove matches.
	Exclude []string `json:"exclude,omitempty"`
	// Modes records each regular file's permission bits (a note that must stay private).
	Modes bool `json:"modes,omitempty"`
	// Drop lists key paths removed from every line of a *.jsonl file before it is recorded: dotted key names from
	// the line's root (array indices do not count), * for any one key, a leading ** for any depth
	// ("**.sections.docs" drops a host's documentation paragraph wherever a system message carries it). It is for host
	// wording that differs between Pi and PiG by design; never for what the extension writes.
	Drop []string `json:"drop,omitempty"`
}

// UnmarshalJSON accepts the string form.
func (f *FileCapture) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		root, glob := "", s
		// A lowercase word before the first colon is a root; an unknown one (a typo) fails validation instead of
		// silently capturing a glob that matches nothing.
		if i := strings.IndexByte(s, ':'); i > 0 && rootWordRE.MatchString(s[:i]) {
			root, glob = s[:i], s[i+1:]
		}
		*f = FileCapture{Root: root, Glob: glob}
		return nil
	}
	type plain FileCapture
	var p plain
	if err := strictUnmarshal(b, &p); err != nil {
		return err
	}
	*f = FileCapture(p)
	return nil
}

func (f FileCapture) root() string {
	if f.Root == "" {
		return "workspace"
	}
	return f.Root
}

func (f FileCapture) label() string { return f.root() + ":" + f.Glob }

func strictUnmarshal(b []byte, v any) error {
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// DefaultFullEvents are the host event types recorded whole (see Projection.Full) when they are captured: the
// session events that carry data an extension can influence and that no earlier scenario could capture.
var DefaultFullEvents = []string{
	"compaction_start", "compaction_end", "auto_retry_start", "auto_retry_end", "queue_update",
	"session_info_changed", "agent_settled", "entry_appended", "thinking_level_changed",
	"summarization_retry_scheduled", "summarization_retry_attempt_start", "summarization_retry_finished",
}

var (
	varRefRE   = regexp.MustCompile(`\{\{var:([^}]*)\}\}`)
	varNameRE  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)
	helperName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*\.(?:ts|mts|js|mjs)$`)
	modelIDRE  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	rootWordRE = regexp.MustCompile(`^[a-z]+$`)
)

// builtinVars are the {{NAME}} values the harness knows per lane.
var builtinVars = []string{"sessions", "cwd", "agent"}

var builtinVarRE = regexp.MustCompile(`\{\{(sessions|cwd|agent)\}\}`)

// primary returns the primary model, with the defaults filled in.
func (s *Scenario) primary() ModelSpec {
	m := ModelSpec{}
	if s.Model != nil {
		m = *s.Model
	}
	return m.withDefaults()
}

func (m ModelSpec) withDefaults() ModelSpec {
	if m.ID == "" {
		m.ID = DefaultModelID
	}
	if m.ContextWindow == 0 {
		m.ContextWindow = 100000
	}
	if m.MaxTokens == 0 {
		m.MaxTokens = 4096
	}
	if len(m.Input) == 0 {
		m.Input = []string{"text"}
	}
	return m
}

// allModels is the primary model followed by Models.
func (s *Scenario) allModels() []ModelSpec {
	out := []ModelSpec{s.primary()}
	for _, m := range s.Models {
		out = append(out, m.withDefaults())
	}
	return out
}

func (m ModelSpec) validate(where string) error {
	if m.ID != "" && !modelIDRE.MatchString(m.ID) {
		return fmt.Errorf("%s: id %q must be letters, digits, dot, underscore or hyphen", where, m.ID)
	}
	if m.ContextWindow < 0 || m.MaxTokens < 0 {
		return fmt.Errorf("%s: contextWindow and maxTokens cannot be negative", where)
	}
	for _, in := range m.Input {
		if in != "text" && in != "image" {
			return fmt.Errorf("%s: input %q must be text or image", where, in)
		}
	}
	return nil
}

// recordsModel reports whether the model id is part of the llm trace event: only a scenario with several
// models can tell them apart, and a scenario with one model records what it always did.
func (s *Scenario) recordsModel() bool { return len(s.Models) > 0 }

var finishReasons = []string{"", "stop", "length", "tool_calls", "content_filter"}

func (u *Usage) sum() int { return u.Input + u.Output + u.CacheRead + u.CacheWrite }

func (t Turn) validate(i int) error {
	where := fmt.Sprintf("llm turn %d", i+1)
	if t.DelayMs < 0 {
		return fmt.Errorf("%s: delayMs cannot be negative", where)
	}
	if !slices.Contains(finishReasons, t.FinishReason) {
		return fmt.Errorf("%s: finishReason %q must be one of stop, length, tool_calls, content_filter", where, t.FinishReason)
	}
	if t.Hold != "" && !varNameRE.MatchString(t.Hold) {
		return fmt.Errorf("%s: hold %q must be a name (letters, digits, underscore, hyphen)", where, t.Hold)
	}
	if u := t.Usage; u != nil {
		if u.Input < 0 || u.Output < 0 || u.CacheRead < 0 || u.CacheWrite < 0 {
			return fmt.Errorf("%s: usage cannot be negative", where)
		}
		if u.TotalTokens != nil && *u.TotalTokens != u.sum() {
			return fmt.Errorf("%s: usage.totalTokens %d is not input+output+cacheRead+cacheWrite = %d: the host derives the total, so another value cannot be reported", where, *u.TotalTokens, u.sum())
		}
	}
	if e := t.Error; e != nil {
		if t.Text != "" || len(t.ToolCalls) > 0 || t.Usage != nil || t.FinishReason != "" || t.Thinking != "" {
			return fmt.Errorf("%s: error answers the request with an HTTP error, so text, toolCalls, thinking, usage and finishReason cannot be set", where)
		}
		if e.status() < 400 || e.status() > 599 {
			return fmt.Errorf("%s: error status %d must be 400 to 599", where, e.status())
		}
		if (e.Message == "") == (e.Body == "") {
			return fmt.Errorf("%s: error needs exactly one of message and body", where)
		}
	}
	return nil
}

func (e *TurnError) status() int {
	if e.Status == 0 {
		return 400
	}
	return e.Status
}

// body is the response body: the scenario's own, or the OpenAI error object around Message.
func (e *TurnError) body() string {
	if e.Body != "" {
		return e.Body
	}
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(map[string]any{"error": map[string]any{"message": e.Message, "type": "invalid_request_error", "param": nil, "code": nil}})
	return strings.TrimRight(buf.String(), "\n")
}

// validateFeatures checks the fields this file defines. Validate calls it last.
func (s *Scenario) validateFeatures() error {
	if s.SystemDiff != "" && s.SystemDiff != SystemDiffSpan && s.SystemDiff != SystemDiffHunks && s.SystemDiff != SystemDiffInserted {
		return fmt.Errorf("systemDiff %q must be %q, %q or %q", s.SystemDiff, SystemDiffSpan, SystemDiffHunks, SystemDiffInserted)
	}
	if (s.Model != nil || len(s.Models) > 0) && len(s.LLM) == 0 {
		return errors.New("model and models describe the scripted model, so the scenario needs an llm script")
	}
	if len(s.Models) > 0 && s.Model == nil {
		return errors.New("models are further models next to model: give model too")
	}
	seen := map[string]bool{}
	if s.Model != nil {
		if err := s.Model.validate("model"); err != nil {
			return err
		}
	}
	for i, m := range s.allModels() {
		if i > 0 {
			if s.Models[i-1].ID == "" {
				return fmt.Errorf("models[%d]: id is required", i-1)
			}
			if err := s.Models[i-1].validate(fmt.Sprintf("models[%d]", i-1)); err != nil {
				return err
			}
		}
		if seen[m.ID] {
			return fmt.Errorf("model id %q is used twice", m.ID)
		}
		seen[m.ID] = true
	}
	if s.Settings != nil {
		if _, ok := s.AgentFiles["settings.json"]; ok {
			return errors.New("settings and agentFiles settings.json both write the agent directory's settings.json: use one")
		}
	}
	if s.Session != nil {
		for rel := range s.Session.Files {
			if err := confinedPath(rel); err != nil {
				return fmt.Errorf("session file: %w", err)
			}
		}
	}
	names := map[string]bool{}
	for i, h := range s.Extensions {
		if !helperName.MatchString(h.Name) {
			return fmt.Errorf("extensions[%d]: name %q must be a bare file name ending in .ts, .mts, .js or .mjs", i, h.Name)
		}
		if names[h.Name] {
			return fmt.Errorf("extensions[%d]: name %q is used twice", i, h.Name)
		}
		names[h.Name] = true
		if h.Position != "" && h.Position != "before" && h.Position != "after" {
			return fmt.Errorf("extensions[%d]: position %q must be before or after", i, h.Position)
		}
		if strings.TrimSpace(h.Source) == "" {
			return fmt.Errorf("extensions[%d]: source is empty", i)
		}
	}
	if s.Project != nil {
		for _, k := range s.Project.MessageFields {
			if k == "" {
				return errors.New("project.messageFields: empty key")
			}
		}
		for _, k := range s.Project.Full {
			if k == "" {
				return errors.New("project.full: empty event type")
			}
		}
		for _, d := range s.Project.Drop {
			for i, seg := range strings.Split(d, ".") {
				if seg == "" || seg == "**" && i != 0 {
					return fmt.Errorf("project.drop %q: segments are key names, * or (first only) **", d)
				}
			}
		}
	}
	holds := map[string]bool{}
	for i, t := range s.LLM {
		if err := t.validate(i); err != nil {
			return err
		}
		if t.Hold != "" {
			holds[t.Hold] = true
		}
	}
	for i, c := range s.CaptureFiles {
		if err := c.validate(fmt.Sprintf("captureFiles[%d]", i)); err != nil {
			return err
		}
	}
	saved := map[string]bool{}
	for i, st := range s.Steps {
		where := fmt.Sprintf("step %d", i)
		for j, c := range st.CaptureFiles {
			if err := c.validate(fmt.Sprintf("%s captureFiles[%d]", where, j)); err != nil {
				return err
			}
		}
		if err := checkVars(where+" rpc", st.RPC, saved, s.Session != nil); err != nil {
			return err
		}
		for name, p := range st.Save {
			if !varNameRE.MatchString(name) {
				return fmt.Errorf("%s: save name %q must be letters, digits, underscore or hyphen, starting with a letter", where, name)
			}
			if slices.Contains(builtinVars, name) {
				return fmt.Errorf("%s: save name %q is a built-in variable", where, name)
			}
			if _, err := splitPath(p); err != nil {
				return fmt.Errorf("%s: save %s: %w", where, name, err)
			}
		}
		for j, tr := range st.Triggers {
			tw := fmt.Sprintf("%s trigger %d", where, j)
			w := tr.When
			switch {
			case (w.Event == "") == (w.LLMRequest == 0):
				return fmt.Errorf("%s: when needs exactly one of event and llmRequest", tw)
			case w.LLMRequest < 0 || w.Nth < 0:
				return fmt.Errorf("%s: llmRequest and nth cannot be negative", tw)
			case w.LLMRequest > len(s.LLM):
				return fmt.Errorf("%s: llmRequest %d but the llm script has %d turns", tw, w.LLMRequest, len(s.LLM))
			case w.LLMRequest > 0 && (w.Nth != 0 || len(w.Match) > 0):
				return fmt.Errorf("%s: nth and match go with event, not llmRequest", tw)
			}
			if tr.RPC == nil && len(tr.Release) == 0 {
				return fmt.Errorf("%s: set rpc and/or release", tw)
			}
			if tr.RPC != nil {
				if typ, _ := tr.RPC["type"].(string); typ == "" {
					return fmt.Errorf("%s: rpc.type is required", tw)
				}
				if _, ok := tr.RPC["id"]; ok {
					return fmt.Errorf("%s: rpc.id is assigned by the harness", tw)
				}
				if err := checkVars(tw+" rpc", tr.RPC, saved, s.Session != nil); err != nil {
					return err
				}
			}
			if (len(tr.Save) > 0 || tr.Quiet) && tr.RPC == nil {
				return fmt.Errorf("%s: save and quiet need an rpc", tw)
			}
			for name, p := range tr.Save {
				if !varNameRE.MatchString(name) {
					return fmt.Errorf("%s: save name %q must be letters, digits, underscore or hyphen, starting with a letter", tw, name)
				}
				if slices.Contains(builtinVars, name) {
					return fmt.Errorf("%s: save name %q is a built-in variable", tw, name)
				}
				if _, err := splitPath(p); err != nil {
					return fmt.Errorf("%s: save %s: %w", tw, name, err)
				}
				saved[name] = true
			}
			for _, name := range tr.Release {
				if !holds[name] {
					return fmt.Errorf("%s: release %q, but no llm turn holds that name", tw, name)
				}
			}
		}
		for name := range st.Save {
			saved[name] = true
		}
	}
	// A turn is served while the steps run, so it may use any variable some step saves.
	for i, t := range s.LLM {
		if err := checkVars(fmt.Sprintf("llm turn %d", i+1), turnStrings(t), saved, true); err != nil {
			return err
		}
	}
	return nil
}

func (c FileCapture) validate(where string) error {
	if !slices.Contains(FileRoots, c.root()) {
		return fmt.Errorf("%s: root %q must be one of %s", where, c.Root, strings.Join(FileRoots, ", "))
	}
	for _, g := range append([]string{c.Glob}, c.Exclude...) {
		if g == "" || filepath.IsAbs(g) || strings.HasPrefix(g, "/") {
			return fmt.Errorf("%s: glob %q must be a non-empty relative pattern", where, g)
		}
		for _, seg := range strings.Split(g, "/") {
			if seg == ".." {
				return fmt.Errorf("%s: glob %q must stay inside its root", where, g)
			}
			if _, err := path.Match(seg, ""); err != nil {
				return fmt.Errorf("%s: glob %q: %w", where, g, err)
			}
		}
	}
	for _, d := range c.Drop {
		for i, seg := range strings.Split(d, ".") {
			if seg == "" || seg == "**" && i != 0 {
				return fmt.Errorf("%s: drop %q: segments are key names, * or (first only) **", where, d)
			}
		}
	}
	return nil
}

// checkVars reports a {{var:NAME}} that no earlier step saved and a {{sessions}} in a scenario without Session.
func checkVars(where string, v any, saved map[string]bool, session bool) error {
	var err error
	walkStrings(v, func(s string) {
		for _, m := range varRefRE.FindAllStringSubmatch(s, -1) {
			if !saved[m[1]] && err == nil {
				err = fmt.Errorf("%s uses {{var:%s}}, which no earlier step saves", where, m[1])
			}
		}
		if !session && strings.Contains(s, "{{sessions}}") && err == nil {
			err = fmt.Errorf("%s uses {{sessions}}, but the scenario has no session directory", where)
		}
	})
	return err
}

func walkStrings(v any, f func(string)) {
	switch x := v.(type) {
	case string:
		f(x)
	case []any:
		for _, e := range x {
			walkStrings(e, f)
		}
	case map[string]any:
		for _, e := range x {
			walkStrings(e, f)
		}
	}
}

// laneVars are the values {{var:NAME}} and the built-in names expand to in one lane.
type laneVars struct {
	builtin map[string]string
	mu      sync.RWMutex
	saved   map[string]string
}

func (lv *laneVars) set(name, val string) {
	lv.mu.Lock()
	defer lv.mu.Unlock()
	if lv.saved == nil {
		lv.saved = map[string]string{}
	}
	lv.saved[name] = val
}

// expand returns a copy of v with every variable replaced. An unsaved variable is an error.
func (lv *laneVars) expand(v any) (any, error) {
	switch x := v.(type) {
	case string:
		var err error
		out := varRefRE.ReplaceAllStringFunc(x, func(m string) string {
			name := varRefRE.FindStringSubmatch(m)[1]
			lv.mu.RLock()
			val, ok := lv.saved[name]
			lv.mu.RUnlock()
			if !ok && err == nil {
				err = fmt.Errorf("variable %q has no value (the step that saves it did not run or its path found nothing)", name)
			}
			return val
		})
		out = builtinVarRE.ReplaceAllStringFunc(out, func(m string) string { return lv.builtin[m[2:len(m)-2]] })
		return out, err
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			r, err := lv.expand(e)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			r, err := lv.expand(e)
			if err != nil {
				return nil, err
			}
			out[k] = r
		}
		return out, nil
	}
	return v, nil
}

// pathSeg is one segment of a response path: a key, an index (negative from the end) or a selector.
type pathSeg struct {
	key   string
	index int
	isIdx bool
	// selector [k=v]: the first (or, with last, the last) array element whose k equals v.
	selK, selV string
	sel, last  bool
}

// splitPath parses data.entries.-1.id, data.entries.[type=compaction].id and data.entries.[message.role=user]!.id.
func splitPath(p string) ([]pathSeg, error) {
	if p == "" {
		return nil, errors.New("empty path")
	}
	var segs []pathSeg
	for i := 0; i < len(p); {
		if p[i] == '[' {
			j := strings.IndexByte(p[i:], ']')
			if j < 0 {
				return nil, fmt.Errorf("path %q: unterminated [", p)
			}
			k, v, ok := strings.Cut(p[i+1:i+j], "=")
			if !ok || k == "" {
				return nil, fmt.Errorf("path %q: a selector is [key=value]", p)
			}
			seg := pathSeg{sel: true, selK: k, selV: v}
			i += j + 1
			if i < len(p) && p[i] == '!' {
				seg.last = true
				i++
			}
			segs = append(segs, seg)
		} else {
			j := strings.IndexByte(p[i:], '.')
			var tok string
			if j < 0 {
				tok, i = p[i:], len(p)
			} else {
				tok, i = p[i:i+j], i+j
			}
			if tok == "" {
				return nil, fmt.Errorf("path %q: empty segment", p)
			}
			seg := pathSeg{key: tok}
			if n, err := atoi(tok); err == nil {
				seg.index, seg.isIdx = n, true
			}
			segs = append(segs, seg)
		}
		if i < len(p) {
			if p[i] != '.' {
				return nil, fmt.Errorf("path %q: expected . at %d", p, i)
			}
			i++
			if i == len(p) {
				return nil, fmt.Errorf("path %q: ends with .", p)
			}
		}
	}
	return segs, nil
}

func atoi(s string) (int, error) {
	n := 0
	neg := false
	t := s
	if strings.HasPrefix(t, "-") {
		neg, t = true, t[1:]
	}
	if t == "" {
		return 0, errors.New("not a number")
	}
	for _, c := range t {
		if c < '0' || c > '9' {
			return 0, errors.New("not a number")
		}
		n = n*10 + int(c-'0')
	}
	if neg {
		n = -n
	}
	return n, nil
}

// pathGet follows segments through decoded JSON.
func pathGet(v any, segs []pathSeg) (any, bool) {
	for _, s := range segs {
		switch {
		case s.sel:
			arr, ok := v.([]any)
			if !ok {
				return nil, false
			}
			var found any
			ok = false
			for _, e := range arr {
				got, has := dottedGet(e, s.selK)
				if has && scalarText(got) == s.selV {
					found, ok = e, true
					if !s.last {
						break
					}
				}
			}
			if !ok {
				return nil, false
			}
			v = found
		default:
			if arr, ok := v.([]any); ok && s.isIdx {
				i := s.index
				if i < 0 {
					i += len(arr)
				}
				if i < 0 || i >= len(arr) {
					return nil, false
				}
				v = arr[i]
				continue
			}
			m, ok := v.(map[string]any)
			if !ok {
				return nil, false
			}
			e, ok := m[s.key]
			if !ok {
				return nil, false
			}
			v = e
		}
	}
	return v, true
}

// dottedGet reads a dotted key path (message.role) through objects.
func dottedGet(v any, p string) (any, bool) {
	for _, k := range strings.Split(p, ".") {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		if v, ok = m[k]; !ok {
			return nil, false
		}
	}
	return v, true
}

// scalarText is the text a selector or a saved variable compares and stores: a string as it is, anything
// else as its canonical JSON.
func scalarText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return string(canonical(v))
}

// eventMatches reports whether a host record satisfies a trigger's event, match and (separately counted) Nth.
func (w TriggerWhen) eventMatches(rec map[string]any) bool {
	if typ, _ := rec["type"].(string); typ != w.Event {
		return false
	}
	for k, want := range w.Match {
		got, ok := dottedGet(rec, k)
		if !ok || string(canonical(got)) != string(canonical(want)) {
			return false
		}
	}
	return true
}
