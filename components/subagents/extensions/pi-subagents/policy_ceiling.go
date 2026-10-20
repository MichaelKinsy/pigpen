package pi_subagents

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Port of src/runs/shared/capability-ceiling.ts.

const capabilityCeilingVersion = 1

// resolvedCeiling is the original's ResolvedSubagentCapabilityCeiling: a nil list is unset, an empty one is "none allowed".
type resolvedCeiling struct {
	Version          int
	AllowedTools     []string
	HasAllowedTools  bool
	AllowedAgents    []string
	HasAllowedAgents bool
	DenyExtensions   bool
	Sources          []string
}

var ceilingNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]+$`)

func validateCeilingText(value any, field string) (string, error) {
	s, ok := value.(string)
	if ok {
		t := jsTrim(s)
		bad := t == ""
		for _, r := range s {
			if r <= 0x1f || r == 0x7f {
				bad = true
			}
		}
		if !bad && len(t) <= 256 {
			return t, nil
		}
	}
	return "", fmt.Errorf("Invalid capability ceiling %s; expected a non-empty string without control characters (max 256 UTF-8 bytes).", field)
}

// normalizeCeilingList is normalizeList in the original's normalizeCeiling: unique, sorted, validated names.
func normalizeCeilingList(values any, field string) ([]string, error) {
	arr, ok := asArray(values)
	if !ok {
		return nil, fmt.Errorf("Invalid capability ceiling %s; expected an array.", field)
	}
	if len(arr) > 256 {
		return nil, fmt.Errorf("Invalid capability ceiling %s; expected at most 256 names.", field)
	}
	seen := map[string]bool{}
	out := []string{}
	for _, entry := range arr {
		name, err := validateCeilingText(entry, field+" entry")
		if err != nil {
			return nil, err
		}
		if !ceilingNamePattern.MatchString(name) {
			return nil, fmt.Errorf("Invalid capability ceiling %s entry '%s'.", field, name)
		}
		if len(name) > 128 {
			return nil, fmt.Errorf("Invalid capability ceiling %s entry '%s'; max 128 UTF-8 bytes.", field, name)
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}

// normalizeCapabilityCeilingAllowedAgents is the original's normalizeCapabilityCeilingAllowedAgents: the validated,
// de-duplicated, sorted agent names of an allowedAgents value, or the original's error.
func normalizeCapabilityCeilingAllowedAgents(values any) ([]string, error) {
	return normalizeCeilingList(values, "allowedAgents")
}

// normalizeCeiling is the original's normalizeCeiling: the validated ceiling of an object with allowedTools, allowedAgents
// and/or denyExtensions (sources empty).
func normalizeCeiling(ceiling any) (*resolvedCeiling, error) {
	obj, ok := asObject(ceiling)
	if !ok {
		return nil, fmt.Errorf("Invalid capability ceiling; expected an object.")
	}
	hasTools, hasAgents, hasDeny := obj.hasOwn("allowedTools"), obj.hasOwn("allowedAgents"), obj.hasOwn("denyExtensions")
	if !hasTools && !hasAgents && !hasDeny {
		return nil, fmt.Errorf("Invalid capability ceiling; expected allowedTools, allowedAgents, or denyExtensions.")
	}
	deny, _ := obj.vals["denyExtensions"].(bool)
	if _, isBool := obj.vals["denyExtensions"].(bool); hasDeny && !isBool {
		return nil, fmt.Errorf("Invalid capability ceiling denyExtensions; expected a boolean.")
	}
	out := &resolvedCeiling{Version: capabilityCeilingVersion, DenyExtensions: deny, Sources: []string{}}
	var err error
	if hasTools {
		if out.AllowedTools, err = normalizeCeilingList(obj.vals["allowedTools"], "allowedTools"); err != nil {
			return nil, err
		}
		out.HasAllowedTools = true
	}
	if hasAgents {
		if out.AllowedAgents, err = normalizeCeilingList(obj.vals["allowedAgents"], "allowedAgents"); err != nil {
			return nil, err
		}
		out.HasAllowedAgents = true
	}
	return out, nil
}

// parseSubagentCapabilityCeiling is the original's: a resolved ceiling as it travels (version 1 and sources included).
func parseSubagentCapabilityCeiling(value any, field string) (*resolvedCeiling, error) {
	record, ok := asObject(value)
	if !ok {
		return nil, fmt.Errorf("Invalid %s; expected an object.", field)
	}
	if v, _ := asNumber(record.vals["version"]); v != capabilityCeilingVersion {
		return nil, fmt.Errorf("Invalid %s version.", field)
	}
	normalized, err := normalizeCeiling(record)
	if err != nil {
		return nil, err
	}
	sources, isArr := asArray(record.vals["sources"])
	if !isArr {
		return nil, fmt.Errorf("Invalid %s sources; expected an array of strings.", field)
	}
	for _, s := range sources {
		if _, isStr := s.(string); !isStr {
			return nil, fmt.Errorf("Invalid %s sources; expected an array of strings.", field)
		}
	}
	seen := map[string]bool{}
	normalized.Sources = []string{}
	for _, s := range sources {
		t, err := validateCeilingText(s, field+" source")
		if err != nil {
			return nil, err
		}
		if !seen[t] {
			seen[t] = true
			normalized.Sources = append(normalized.Sources, t)
		}
	}
	sort.Strings(normalized.Sources)
	return normalized, nil
}

// object is the ceiling as JSON.stringify writes it (version, allowedTools, allowedAgents, denyExtensions, sources).
func (c *resolvedCeiling) object() *jsObject {
	o := newObject()
	o.set("version", float64(c.Version))
	if c.HasAllowedTools {
		o.set("allowedTools", anyStrings(c.AllowedTools))
	}
	if c.HasAllowedAgents {
		o.set("allowedAgents", anyStrings(c.AllowedAgents))
	}
	o.set("denyExtensions", c.DenyExtensions)
	o.set("sources", anyStrings(c.Sources))
	return o
}

func anyStrings(s []string) []any {
	out := make([]any, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
}

// intersectSubagentCapabilityCeilings is the original's: tools and agents intersect over the ceilings that define them,
// extensions are denied when any denies, sources are united. No ceiling gives nil.
func intersectSubagentCapabilityCeilings(ceilings ...*resolvedCeiling) *resolvedCeiling {
	var active []*resolvedCeiling
	for _, c := range ceilings {
		if c != nil {
			active = append(active, c)
		}
	}
	if len(active) == 0 {
		return nil
	}
	intersect := func(has func(*resolvedCeiling) bool, list func(*resolvedCeiling) []string) ([]string, bool) {
		var defined []map[string]bool
		var first []string
		for _, c := range active {
			if !has(c) {
				continue
			}
			set := map[string]bool{}
			for _, e := range list(c) {
				set[e] = true
			}
			if defined == nil {
				first = list(c)
			}
			defined = append(defined, set)
		}
		if defined == nil {
			return nil, false
		}
		out := []string{}
		seen := map[string]bool{}
		for _, e := range first {
			if seen[e] {
				continue
			}
			seen[e] = true
			all := true
			for _, set := range defined {
				all = all && set[e]
			}
			if all {
				out = append(out, e)
			}
		}
		sort.Strings(out)
		return out, true
	}
	out := &resolvedCeiling{Version: capabilityCeilingVersion, Sources: []string{}}
	out.AllowedTools, out.HasAllowedTools = intersect(func(c *resolvedCeiling) bool { return c.HasAllowedTools }, func(c *resolvedCeiling) []string { return c.AllowedTools })
	out.AllowedAgents, out.HasAllowedAgents = intersect(func(c *resolvedCeiling) bool { return c.HasAllowedAgents }, func(c *resolvedCeiling) []string { return c.AllowedAgents })
	seen := map[string]bool{}
	for _, c := range active {
		out.DenyExtensions = out.DenyExtensions || c.DenyExtensions
		for _, s := range c.Sources {
			if !seen[s] {
				seen[s] = true
				out.Sources = append(out.Sources, s)
			}
		}
	}
	sort.Strings(out.Sources)
	return out
}

// The registry of the original (a process-wide map of session to registrations; there it is on globalThis so every copy of the
// module sees it; in a Go process one variable is the same).
type ceilingRegistration struct {
	source  string
	ceiling *resolvedCeiling
}

var ceilingRegistry = struct {
	sync.Mutex
	sessions map[string]map[*ceilingHandle]*ceilingRegistration
}{sessions: map[string]map[*ceilingHandle]*ceilingRegistration{}}

// ceilingHandle is the original's SubagentCapabilityCeilingHandle.
type ceilingHandle struct {
	sessionID, source string
	disposed          bool
}

// registerSubagentCapabilityCeiling registers a ceiling (an object: allowedTools, allowedAgents and/or denyExtensions) for
// one exact session under a source name.
func registerSubagentCapabilityCeiling(sessionID, source string, ceiling any) (*ceilingHandle, error) {
	sid, err := validateCeilingText(sessionID, "sessionId")
	if err != nil {
		return nil, err
	}
	src, err := validateCeilingText(source, "source")
	if err != nil {
		return nil, err
	}
	normalized, err := normalizeCeiling(ceiling)
	if err != nil {
		return nil, err
	}
	normalized.Sources = []string{src}
	h := &ceilingHandle{sessionID: sid, source: src}
	ceilingRegistry.Lock()
	defer ceilingRegistry.Unlock()
	if ceilingRegistry.sessions[sid] == nil {
		ceilingRegistry.sessions[sid] = map[*ceilingHandle]*ceilingRegistration{}
	}
	ceilingRegistry.sessions[sid][h] = &ceilingRegistration{source: src, ceiling: normalized}
	return h, nil
}

// update replaces the registered ceiling.
func (h *ceilingHandle) update(ceiling any) error {
	ceilingRegistry.Lock()
	defer ceilingRegistry.Unlock()
	if h.disposed {
		return fmt.Errorf("Cannot update a disposed capability ceiling handle.")
	}
	normalized, err := normalizeCeiling(ceiling)
	if err != nil {
		return err
	}
	normalized.Sources = []string{h.source}
	ceilingRegistry.sessions[h.sessionID][h] = &ceilingRegistration{source: h.source, ceiling: normalized}
	return nil
}

// dispose removes the registration (a second call does nothing).
func (h *ceilingHandle) dispose() {
	ceilingRegistry.Lock()
	defer ceilingRegistry.Unlock()
	if h.disposed {
		return
	}
	h.disposed = true
	session := ceilingRegistry.sessions[h.sessionID]
	delete(session, h)
	if len(session) == 0 {
		delete(ceilingRegistry.sessions, h.sessionID)
	}
}

// resolveSubagentCapabilityCeiling intersects the inherited ceiling with the registrations of the exact session.
func resolveSubagentCapabilityCeiling(sessionID string, inherited *resolvedCeiling) *resolvedCeiling {
	all := []*resolvedCeiling{inherited}
	if sessionID != "" {
		ceilingRegistry.Lock()
		for _, r := range ceilingRegistry.sessions[sessionID] {
			all = append(all, r.ceiling)
		}
		ceilingRegistry.Unlock()
	}
	return intersectSubagentCapabilityCeilings(all...)
}

// resolveCurrentSubagentCapabilityCeiling is the registrations of the session alone.
func resolveCurrentSubagentCapabilityCeiling(sessionID string) *resolvedCeiling {
	return resolveSubagentCapabilityCeiling(sessionID, nil)
}

func isAgentAllowedByCapabilityCeiling(agent string, c *resolvedCeiling) bool {
	if c == nil || !c.HasAllowedAgents {
		return true
	}
	for _, a := range c.AllowedAgents {
		if a == agent {
			return true
		}
	}
	return false
}

// capabilityCeilingAgentRestrictionMessage is "" when the agent is allowed.
func capabilityCeilingAgentRestrictionMessage(agent string, c *resolvedCeiling) string {
	if isAgentAllowedByCapabilityCeiling(agent, c) {
		return ""
	}
	sources := "unknown source"
	if c != nil && len(c.Sources) > 0 {
		sources = strings.Join(c.Sources, ", ")
	}
	allowed := "(none)"
	if c != nil && len(c.AllowedAgents) > 0 {
		allowed = strings.Join(c.AllowedAgents, ", ")
	}
	return fmt.Sprintf("Capability ceiling from %s does not allow agent '%s'. Allowed agents: %s.", sources, agent, allowed)
}

// capabilityCeilingAgentRestrictionSources is nil when allowedAgents is not restricted.
func capabilityCeilingAgentRestrictionSources(c *resolvedCeiling) []string {
	if c == nil || !c.HasAllowedAgents {
		return nil
	}
	return append([]string{}, c.Sources...)
}

// encodeSubagentCapabilityCeiling is the base64url of the ceiling's JSON ("" for none).
func encodeSubagentCapabilityCeiling(c *resolvedCeiling) string {
	if c == nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString([]byte(marshalJSON(c.object(), "")))
}

// decodeSubagentCapabilityCeiling reverses it; the base64 is read as Node's Buffer reads it (characters outside the alphabet
// are skipped, a final partial group is kept).
func decodeSubagentCapabilityCeiling(value string) (*resolvedCeiling, error) {
	if value == "" {
		return nil, nil
	}
	var clean []byte
	for _, c := range []byte(value) {
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			clean = append(clean, c)
		case c == '-' || c == '+':
			clean = append(clean, '-')
		case c == '_' || c == '/':
			clean = append(clean, '_')
		case c == '=':
			// padding ends the data
		}
	}
	if r := len(clean) % 4; r == 1 {
		clean = clean[:len(clean)-1]
	}
	raw, _ := base64.RawURLEncoding.DecodeString(string(clean))
	parsed, msg := jsJSONParse(strings.ToValidUTF8(string(raw), "\uFFFD"))
	if msg != "" {
		return nil, fmt.Errorf("Invalid inherited capability ceiling: %s", msg)
	}
	obj, isObj := asObject(parsed)
	if v, _ := asNumber(func() any {
		if isObj {
			return obj.vals["version"]
		}
		return nil
	}()); !isObj || v != capabilityCeilingVersion {
		return nil, fmt.Errorf("Invalid inherited capability ceiling version.")
	}
	return parseSubagentCapabilityCeiling(parsed, "inherited capability ceiling")
}
