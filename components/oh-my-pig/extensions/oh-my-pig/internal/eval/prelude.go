// Package eval is the eval tool: Python and JavaScript cells in kernels that
// keep state, with preludes that reach back into the host.
//
// This file is the contract between areas. An area that adds a global to eval
// cells (browser, agent, workpool, commit, ...) registers a Prelude from its
// Register function; the kernels install its snippets and route its host
// calls. Exported names and signatures in this file are fixed; add, do not
// change.
//
// upstream: packages/coding-agent/src/eval/preludes.ts
package eval

import (
	"encoding/json"
	"sort"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// PreludeCall is the host context of one call out of a language VM.
// upstream: EvalPreludeContext.
type PreludeCall struct {
	// SDK is the owning session's context; its Done channel is the cell's
	// cancellation.
	SDK sdk.Context
	// CallID is the identifier of this host invocation.
	CallID string
	// Language is "py" or "js": the kernel that made the call.
	Language string
	// CellTimeoutMs is the cell's timeout, which the watchdog suspends while
	// this call runs; 0 when the cell has none.
	CellTimeoutMs int
	// OnUpdate reports progress to the active eval call; it may be nil.
	OnUpdate func(partial any)
}

// PreludeResult is the result of a host call. upstream: AgentToolResult.
type PreludeResult struct {
	// Text is what the snippet receives as `response.text`.
	Text string
	// Details is what the snippet receives as the structured response.
	Details any
	// Images are images the call produced, shown with the cell's output.
	Images []PreludeImage
}

// PreludeImage is one image a host call produced.
type PreludeImage struct {
	MimeType string
	// Data is base64.
	Data string
}

// Prelude is a language prelude whose snippets run in eval VMs while its
// handler stays in the host. It is a capability, not a tool: it has no schema
// and never enters the model's tool inventory. upstream: EvalPreludeDefinition.
type Prelude struct {
	// Name is the bridge name and the registration key. Snippets reach Invoke
	// as `__omp_prelude__(Name, parameters)`.
	Name string
	// Documentation is Markdown shown only while the prelude is enabled,
	// served at help://eval/<Name>.
	Documentation func() string
	// Summary replaces the first Documentation line in listings.
	Summary string
	// InlineDocumentation renders Documentation in the eval tool description.
	InlineDocumentation bool
	// FirstCallDocumentation is the short guide sent after the tool batch
	// holding the prelude's first call.
	FirstCallDocumentation string
	// JavaScript is the source installed into a JavaScript realm.
	JavaScript string
	// Python is the source installed into a Python kernel.
	Python string
	// Exports are the globals the snippets own.
	Exports []string
	// Guidance is appended to the system prompt as its own block while the
	// prelude is advertised. Nil is none.
	Guidance func() string
	// DocumentationFor and GuidanceFor are the session-aware forms: when set
	// they are used instead of Documentation and Guidance, with the context of
	// the session the text is rendered for. A prelude whose text depends on a
	// session's settings MUST set these and MUST NOT keep a session's settings
	// in state shared by sessions. Callers go through DocumentationText and
	// GuidanceText, never the fields.
	DocumentationFor func(c sdk.Context) string
	GuidanceFor      func(c sdk.Context) string
	// Approval is the approval tier of host calls: "read", "write" or "exec".
	Approval string
	// ApprovalFor returns the approval tier for one host invocation based on
	// its JSON parameters ("read", "write", or "exec"). When set, it takes
	// precedence over Approval. upstream: EvalPreludeDefinition.approval function.
	ApprovalFor func(parameters json.RawMessage) string
	Enabled     func(c sdk.Context) bool
	// Invoke runs a host call outside the VM. Nil for preludes whose snippets
	// only call session tools.
	Invoke func(call PreludeCall, parameters json.RawMessage) (*PreludeResult, error)
	// Status is the one-line description of a successful host call for the
	// eval status tree; "" records nothing. Nil is none.
	Status func(parameters json.RawMessage, result *PreludeResult) string
}

// DocumentationText is the prelude's documentation for the session of c.
func (p Prelude) DocumentationText(c sdk.Context) string {
	if p.DocumentationFor != nil {
		return p.DocumentationFor(c)
	}
	if p.Documentation != nil {
		return p.Documentation()
	}
	return ""
}

// GuidanceText is the prelude's system prompt block for the session of c.
func (p Prelude) GuidanceText(c sdk.Context) string {
	if p.GuidanceFor != nil {
		return p.GuidanceFor(c)
	}
	if p.Guidance != nil {
		return p.Guidance()
	}
	return ""
}

// ApprovalTier returns the approval tier ("read", "write", "exec", or "") for
// one host invocation with parameters.
func (p Prelude) ApprovalTier(parameters json.RawMessage) string {
	if p.ApprovalFor != nil {
		return p.ApprovalFor(parameters)
	}
	return p.Approval
}

var (
	preludeMu sync.RWMutex
	preludes  = map[string]Prelude{}
)

// RegisterPrelude installs a prelude. A later definition replaces an earlier
// one of the same name, as extension tool registration does.
func RegisterPrelude(p Prelude) {
	preludeMu.Lock()
	preludes[p.Name] = p
	preludeMu.Unlock()
}

// LookupPrelude returns the prelude registered under name.
func LookupPrelude(name string) (Prelude, bool) {
	preludeMu.RLock()
	defer preludeMu.RUnlock()
	p, ok := preludes[name]
	return p, ok
}

// EnabledPreludes lists the preludes enabled for c, sorted by name.
// upstream: getEnabledEvalPreludes.
func EnabledPreludes(c sdk.Context) []Prelude {
	preludeMu.RLock()
	defer preludeMu.RUnlock()
	out := make([]Prelude, 0, len(preludes))
	for _, p := range preludes {
		if p.Enabled == nil || p.Enabled(c) {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ResetPreludesForTests empties the registry.
func ResetPreludesForTests() {
	preludeMu.Lock()
	preludes = map[string]Prelude{}
	preludeMu.Unlock()
}
