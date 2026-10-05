package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// The tool layer: what index.ts registers with pi.registerTool. Tools are plain Go functions on a
// Runtime so they can be tested without a host; extension.go adapts them to the PiG SDK.

// ContentBlock is one block of a tool result: "text" or "image".
type ContentBlock struct{ Type, Text, Data, MimeType string }

// ToolOutput is a tool result: content blocks and the details object.
type ToolOutput struct {
	Content []ContentBlock
	Details map[string]any
	IsError bool
}

// Text is the first text block.
func (o ToolOutput) Text() string {
	for _, b := range o.Content {
		if b.Type == "text" {
			return b.Text
		}
	}
	return ""
}

// ToolSpec is one registered tool.
type ToolSpec struct {
	Name, Label, Description, PromptSnippet string
	parameters                              obj
	// Execute runs the tool. update reports progress and may be nil.
	Execute func(ctx context.Context, params map[string]any, update func(ToolOutput)) (ToolOutput, error)
}

// ParametersJSON is the parameter schema in the original's key order.
func (s ToolSpec) ParametersJSON() string {
	b, _ := marshalNoEscape(s.parameters)
	return string(b)
}

// ParametersMap is the parameter schema as plain maps.
func (s ToolSpec) ParametersMap() map[string]any { return toPlain(s.parameters) }

// Host is what the runtime needs from the running session.
type Host interface {
	AppendEntry(customType string, data any)
	SendMessage(customType, content string, triggerTurn bool)
}

// ToolActivator is implemented by hosts that can change the active tool set (web_enable).
type ToolActivator interface {
	AllToolNames() []string
	ActiveToolNames() []string
	SetActiveTools(names []string) error
}

// UIHost is implemented by hosts that know whether a UI is attached.
type UIHost interface{ HasUI() bool }

// Runtime holds the registered tools and the state they share.
type Runtime struct {
	cfg  *ToolConfig
	host Host

	mu            sync.Mutex
	sessionActive bool
	// loaderSelected: the session's transcript selected the web_enable loader.
	loaderSelected bool
	pending        map[string]context.CancelFunc
	wg             sync.WaitGroup

	specs []ToolSpec
}

// NewRuntime reads the registration configuration and builds the tools it enables. An invalid
// configuration is an error (the original throws at registration); an unreadable one only
// produces a Warning and the defaults.
func NewRuntime(host Host) (*Runtime, error) {
	cfg, err := LoadToolConfig()
	if err != nil {
		return nil, err
	}
	r := &Runtime{cfg: cfg, host: host, pending: map[string]context.CancelFunc{}, loaderSelected: true}
	r.buildSpecs()
	return r, nil
}

// Tools are the registered tools in registration order.
func (r *Runtime) Tools() []ToolSpec { return r.specs }

// Tool finds a registered tool by its public name.
func (r *Runtime) Tool(name string) *ToolSpec {
	for i := range r.specs {
		if r.specs[i].Name == name {
			return &r.specs[i]
		}
	}
	return nil
}

// Commands are the enabled slash commands this port implements.
func (r *Runtime) Commands() []string {
	if r.cfg.Commands["search"] {
		return []string{"search"}
	}
	return nil
}

// Warning is set when web-search.json could not be read at registration.
func (r *Runtime) Warning() string { return r.cfg.Warning }

// Config exposes the registration configuration.
func (r *Runtime) Config() *ToolConfig { return r.cfg }

func (r *Runtime) hasUI() bool {
	if u, ok := r.host.(UIHost); ok {
		return u.HasUI()
	}
	return true
}

func (r *Runtime) registeredNames() RegisteredToolNames {
	var n RegisteredToolNames
	if r.cfg.WebSearch {
		n.WebSearch = r.cfg.Names.WebSearch
	}
	if r.cfg.FetchContent {
		n.FetchContent = r.cfg.Names.FetchContent
	}
	return n
}

func (r *Runtime) storedContentSources() string {
	var names []string
	if r.cfg.WebSearch {
		names = append(names, r.cfg.Names.WebSearch)
	}
	if r.cfg.SourceCheck {
		names = append(names, r.cfg.Names.SourceCheck)
	}
	if r.cfg.FetchContent {
		names = append(names, r.cfg.Names.FetchContent)
	}
	return joinToolNames(names)
}

func (r *Runtime) allPolicyDescription() string {
	var eligible, excluded []string
	for _, p := range r.cfg.AllowedProviders {
		if sliceHas(AllSearchProviders, p) {
			eligible = append(eligible, p)
		} else {
			excluded = append(excluded, p)
		}
	}
	switch {
	case len(eligible) == 0:
		return fmt.Sprintf("all has no eligible allowed providers; explicit-only allowed providers (%s) remain excluded", labels(excluded))
	case len(excluded) > 0:
		return fmt.Sprintf("all searches eligible allowed providers (%s); explicit-only allowed providers (%s) remain excluded", labels(eligible), labels(excluded))
	}
	return fmt.Sprintf("all searches every eligible allowed provider (%s)", labels(eligible))
}

func (r *Runtime) buildSpecs() {
	cfg := r.cfg
	if cfg.WebSearch {
		r.specs = append(r.specs, r.webSearchSpec())
	}
	if cfg.SourceCheck {
		r.specs = append(r.specs, r.sourceCheckSpec())
	}
	if cfg.FetchContent {
		r.specs = append(r.specs, r.fetchContentSpec())
	}
	if cfg.GetSearchContent {
		r.specs = append(r.specs, r.getSearchContentSpec())
	}
	if cfg.ToolActivation == "dynamic" && len(r.activationTools()) > 0 {
		r.specs = append(r.specs, r.webEnableSpec())
	}
}

// ---- results ------------------------------------------------------------------------------

func textOutput(text string, details map[string]any) ToolOutput {
	return ToolOutput{Content: []ContentBlock{{Type: "text", Text: text}}, Details: details}
}

// errorOutput is `{content: "Error: ...", details: {error}}`.
func errorOutput(msg, detail string, extra map[string]any) ToolOutput {
	d := map[string]any{"error": detail}
	for k, v := range extra {
		d[k] = v
	}
	return textOutput("Error: "+msg, d)
}

// inputValue is formatInputValue: strings as JSON, numbers as JS prints them, the rest as JSON.
func inputValue(v any) string {
	switch x := v.(type) {
	case string:
		b, _ := marshalNoEscape(x)
		return string(b)
	case float64:
		return jsNumber(x)
	case int:
		return fmt.Sprint(x)
	}
	b, err := marshalNoEscape(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func jsNumber(f float64) string {
	if f != f {
		return "NaN"
	}
	b, _ := json.Marshal(f)
	return strings.TrimSuffix(string(b), ".0")
}

func paramString(params map[string]any, key string) (string, bool) {
	s, ok := params[key].(string)
	return s, ok
}

func paramStrings(v any) []string {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, e := range list {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func normalizeQueryList(list []any) []string {
	var out []string
	for _, q := range list {
		if s, ok := q.(string); ok {
			if t := jsTrim(s); t != "" {
				out = append(out, t)
			}
		}
	}
	return out
}

// expandQueryString: some local models put a JSON array of queries into the single `query`
// string; an unambiguous string-only array is searched element by element.
func expandQueryString(q string) []string {
	trimmed := jsTrim(q)
	if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
		var parsed []any
		if json.Unmarshal([]byte(trimmed), &parsed) == nil {
			var out []string
			all := true
			for _, e := range parsed {
				s, ok := e.(string)
				if !ok {
					all = false
					break
				}
				if t := jsTrim(s); t != "" {
					out = append(out, t)
				}
			}
			if all {
				return out
			}
		}
	}
	return []string{q}
}

func normalizeRecency(v any) string {
	if s, ok := v.(string); ok && (s == "day" || s == "week" || s == "month" || s == "year") {
		return s
	}
	return ""
}

// runSearchQueries runs at most three queries at once and keeps input order.
func runSearchQueries[T any](queries []string, run func(query string, index int) T) []T {
	out := make([]T, len(queries))
	sem := make(chan struct{}, 3)
	var wg sync.WaitGroup
	for i, q := range queries {
		wg.Add(1)
		go func(i int, q string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = run(q, i)
		}(i, q)
	}
	wg.Wait()
	return out
}

func stripThumbnails(in []ExtractedContent) []ExtractedContent {
	out := make([]ExtractedContent, len(in))
	for i, c := range in {
		c.Thumbnail, c.Frames = nil, nil
		out[i] = c
	}
	return out
}
