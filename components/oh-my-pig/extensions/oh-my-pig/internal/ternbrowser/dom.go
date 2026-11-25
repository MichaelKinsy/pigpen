package ternbrowser

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/browser"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/ternwire"
)

// Native DOM read protocol types and pure mappings.
// upstream: packages/coding-agent/src/tools/browser/tern/dom.ts

const HandleAttribute = "data-omp-tern-handle"

type DomNode struct {
	Ref          string            `json:"ref,omitempty"`
	Tag          string            `json:"tag,omitempty"`
	Role         string            `json:"role"`
	Name         string            `json:"name,omitempty"`
	Value        string            `json:"value,omitempty"`
	Description  string            `json:"description,omitempty"`
	Keyshortcuts string            `json:"keyshortcuts,omitempty"`
	States       []string          `json:"states,omitempty"`
	Rect         [4]float64        `json:"rect"`
	Frame        bool              `json:"frame,omitempty"`
	Children     []DomNode         `json:"children,omitempty"`
	Text         string            `json:"text,omitempty"`
	HTML         string            `json:"html,omitempty"`
	Attributes   map[string]string `json:"attributes,omitempty"`
	Checked      any               `json:"checked,omitempty"`
}

type DomScroll struct {
	At [2]float64 `json:"at"`
	DX float64    `json:"dx"`
	DY float64    `json:"dy"`
}

type DomSnapshot struct {
	URL      string `json:"url"`
	Title    string `json:"title"`
	Viewport struct {
		Width  int     `json:"width"`
		Height int     `json:"height"`
		Scale  float64 `json:"scale"`
	} `json:"viewport"`
	Scroll struct {
		X            float64 `json:"x"`
		Y            float64 `json:"y"`
		Width        float64 `json:"width"`
		Height       float64 `json:"height"`
		ScrollWidth  float64 `json:"scrollWidth"`
		ScrollHeight float64 `json:"scrollHeight"`
	} `json:"scroll"`
	Nodes     []DomNode `json:"nodes"`
	Truncated bool      `json:"truncated"`
}

type DomResolved struct {
	State     string      `json:"state"`
	Node      *DomNode    `json:"node,omitempty"`
	Point     *[2]float64 `json:"point,omitempty"`
	Rect      *[4]float64 `json:"rect,omitempty"`
	Scroll    *DomScroll  `json:"scroll,omitempty"`
	CoveredBy *DomNode    `json:"coveredBy,omitempty"`
}

type DomQuery struct {
	By          string   `json:"by"`
	Value       string   `json:"value,omitempty"`
	Name        string   `json:"name,omitempty"`
	Attribute   string   `json:"attribute,omitempty"`
	Exact       bool     `json:"exact,omitempty"`
	Root        string   `json:"root,omitempty"`
	VisibleOnly bool     `json:"visibleOnly,omitempty"`
	Limit       int      `json:"limit,omitempty"`
	Include     []string `json:"include,omitempty"`
}

type DomMatches struct {
	Nodes []DomNode `json:"nodes"`
	Total int       `json:"total"`
}

type DomWaited struct {
	Matched bool      `json:"matched"`
	Nodes   []DomNode `json:"nodes,omitempty"`
}

// CheckDomReply checks a raw JSON "dom" op reply for an engine-level {error:{kind,message}}.
// upstream: packages/coding-agent/src/tools/browser/tern/dom.ts:182-191
func CheckDomReply(product, read string, raw json.RawMessage, out any) error {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope == nil {
		return &ternwire.Error{
			Kind:    ternwire.ErrProtocol,
			Message: fmt.Sprintf("%s answered dom %s without a reply object", product, read),
		}
	}
	if errRaw, hasErr := envelope["error"]; hasErr {
		var errObj struct {
			Kind    string `json:"kind"`
			Message string `json:"message"`
		}
		if json.Unmarshal(errRaw, &errObj) == nil {
			msg := errObj.Message
			if msg == "" {
				msg = fmt.Sprintf("dom %s failed", read)
			}
			kind := ternwire.ErrFailed
			if errObj.Kind == "invalid" {
				kind = ternwire.ErrInvalid
			}
			return &ternwire.Error{Kind: kind, Message: msg}
		}
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return &ternwire.Error{
				Kind:    ternwire.ErrProtocol,
				Message: fmt.Sprintf("%s dom %s decode failed: %s", product, read, err.Error()),
			}
		}
	}
	return nil
}

// RoleQuery is a parsed `role/` selector.
// upstream: packages/coding-agent/src/tools/browser/query-handlers.ts
type RoleQuery struct {
	Role  string
	Name  *string
	Exact bool
}

var (
	roleSelectorRE = regexp.MustCompile(`^([a-zA-Z0-9_-]+)(?:\s*\[\s*name\s*=\s*(?:"([^"]*)"|'([^']*)')(\s+exact)?\s*\])?$`)
	collapseWSRE   = regexp.MustCompile(`\s+`)
)

// ParseRoleSelector parses `role` or `role[name="..." exact]`.
// upstream: packages/coding-agent/src/tools/browser/query-handlers.ts
func ParseRoleSelector(raw string) (RoleQuery, error) {
	trimmed := strings.TrimSpace(raw)
	m := roleSelectorRE.FindStringSubmatch(trimmed)
	if m == nil {
		return RoleQuery{}, fmt.Errorf("Invalid role selector %s; expected role or role[name=\"...\" exact]", jsonQuote(raw))
	}
	q := RoleQuery{Role: strings.ToLower(m[1])}
	if m[2] != "" || m[3] != "" || strings.Contains(trimmed, "[") {
		nameVal := m[2]
		if nameVal == "" {
			nameVal = m[3]
		}
		nameVal = strings.ToLower(strings.TrimSpace(collapseWSRE.ReplaceAllString(nameVal, " ")))
		q.Name = &nameVal
		q.Exact = strings.TrimSpace(m[4]) == "exact"
	}
	return q, nil
}

// DomQueryFor maps a TernSelector into a native engine DomQuery.
// upstream: packages/coding-agent/src/tools/browser/tern/dom.ts:202-243
func DomQueryFor(sel Selector) (DomQuery, error) {
	switch sel.Engine {
	case "css", "pierce":
		return DomQuery{By: "css", Value: sel.Query}, nil
	case "text":
		return DomQuery{By: "text", Value: sel.Query}, nil
	case "xpath":
		return DomQuery{By: "xpath", Value: sel.Query}, nil
	case "aria":
		q := DomQuery{By: "role", Value: sel.Role}
		if sel.Name != "" {
			q.Name = strings.TrimSpace(collapseWSRE.ReplaceAllString(sel.Name, " "))
			q.Exact = true
		}
		return q, nil
	case "role":
		rq, err := ParseRoleSelector(sel.Query)
		if err != nil {
			return DomQuery{}, err
		}
		q := DomQuery{By: "role", Value: rq.Role}
		if rq.Name != nil {
			q.Name = *rq.Name
			q.Exact = rq.Exact
		}
		return q, nil
	case "label":
		return DomQuery{By: "label", Value: strings.TrimSpace(sel.Query)}, nil
	case "placeholder", "alt", "title":
		return DomQuery{By: "attribute", Attribute: sel.Engine, Value: strings.TrimSpace(sel.Query)}, nil
	case "testid":
		return DomQuery{By: "attribute", Attribute: "data-testid", Value: strings.TrimSpace(sel.Query), Exact: true}, nil
	case "handle":
		return DomQuery{By: "attribute", Attribute: HandleAttribute, Value: sel.Token}, nil
	case "ref":
		return DomQuery{By: "ref", Value: sel.Ref}, nil
	default:
		return DomQuery{}, fmt.Errorf("selector engine %q cannot be converted to native DomQuery", sel.Engine)
	}
}

var (
	checkableRoles = map[string]bool{
		"checkbox": true, "radio": true, "switch": true,
		"menuitemcheckbox": true, "menuitemradio": true,
	}
	numericValueRoles = map[string]bool{
		"slider": true, "spinbutton": true, "progressbar": true, "meter": true,
	}
	structuralRoles = map[string]bool{
		"generic": true, "none": true, "group": true,
	}
	interactiveRoles = map[string]bool{
		"button": true, "link": true, "textbox": true, "combobox": true,
		"listbox": true, "option": true, "checkbox": true, "radio": true,
		"switch": true, "tab": true, "menuitem": true, "menuitemcheckbox": true,
		"menuitemradio": true, "slider": true, "spinbutton": true,
		"searchbox": true, "treeitem": true,
	}
	plainStates = []string{"required", "readonly", "multiselectable", "multiline", "modal", "focused"}
	extraStates = []string{"invalid", "haspopup", "busy"}
)

// observationStates maps engine states into the observation vocabulary.
// upstream: packages/coding-agent/src/tools/browser/tern/dom.ts:272-287
func observationStates(node DomNode) []string {
	has := make(map[string]bool, len(node.States))
	for _, s := range node.States {
		has[s] = true
	}
	states := []string{}
	if has["disabled"] {
		states = append(states, "disabled")
	}
	if has["mixed"] {
		states = append(states, "checked=mixed")
	} else if has["checked"] {
		states = append(states, "checked=true")
	} else if checkableRoles[node.Role] {
		states = append(states, "checked=false")
	}
	if has["pressed"] {
		states = append(states, "pressed=true")
	}
	if has["selected"] {
		states = append(states, "selected=true")
	} else if node.Role == "option" {
		states = append(states, "selected=false")
	}
	if has["expanded"] {
		states = append(states, "expanded=true")
	} else if has["collapsed"] {
		states = append(states, "expanded=false")
	}
	for _, s := range plainStates {
		if has[s] {
			states = append(states, s)
		}
	}
	for _, s := range extraStates {
		if has[s] {
			states = append(states, s)
		}
	}
	return states
}

func isInteractiveDomNode(node DomNode) bool {
	if interactiveRoles[node.Role] {
		return true
	}
	return slices.ContainsFunc(node.States, func(s string) bool {
		return s == "clickable" || s == "editable" || s == "focused"
	})
}

// DomObservation pairs a browser.Observation with its id->ref and id->rect maps.
// upstream: packages/coding-agent/src/tools/browser/tern/dom.ts:297-303
type DomObservation struct {
	Observation browser.Observation
	Refs        map[int]string
	Rects       map[int][4]float64
}

// ObservationFrom converts a DomSnapshot to a DomObservation.
// upstream: packages/coding-agent/src/tools/browser/tern/dom.ts:310-358
func ObservationFrom(snap DomSnapshot, compact bool) DomObservation {
	type item struct {
		node DomNode
		ref  string
		drop bool
	}
	var collected []*item
	var visit func(n DomNode) bool
	visit = func(n DomNode) bool {
		if n.Ref == "" {
			return false
		}
		it := &item{node: n, ref: n.Ref}
		collected = append(collected, it)
		below := false
		for _, child := range n.Children {
			if visit(child) {
				below = true
			}
		}
		inter := isInteractiveDomNode(n)
		it.drop = compact && n.Name == "" && !inter && !below && structuralRoles[n.Role]
		return inter || below
	}
	for _, n := range snap.Nodes {
		visit(n)
	}

	refs := make(map[int]string)
	rects := make(map[int][4]float64)
	elements := make([]browser.ObservationEntry, 0, len(collected))
	for _, it := range collected {
		if it.drop {
			continue
		}
		id := len(elements) + 1
		refs[id] = it.ref
		rects[id] = it.node.Rect
		entry := browser.ObservationEntry{
			ID:           id,
			Role:         it.node.Role,
			Name:         it.node.Name,
			Description:  it.node.Description,
			Keyshortcuts: it.node.Keyshortcuts,
			States:       observationStates(it.node),
		}
		if it.node.Value != "" {
			if numericValueRoles[it.node.Role] {
				if f, err := strconv.ParseFloat(it.node.Value, 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
					entry.Value = f
				} else {
					entry.Value = it.node.Value
				}
			} else {
				entry.Value = it.node.Value
			}
		}
		elements = append(elements, entry)
	}
	return DomObservation{
		Observation: browser.Observation{
			URL:   snap.URL,
			Title: snap.Title,
			Viewport: browser.Viewport{
				Width:             snap.Viewport.Width,
				Height:            snap.Viewport.Height,
				DeviceScaleFactor: snap.Viewport.Scale,
			},
			Scroll: browser.ScrollState{
				X:            snap.Scroll.X,
				Y:            snap.Scroll.Y,
				Width:        snap.Scroll.Width,
				Height:       snap.Scroll.Height,
				ScrollWidth:  snap.Scroll.ScrollWidth,
				ScrollHeight: snap.Scroll.ScrollHeight,
			},
			Elements: elements,
		},
		Refs:  refs,
		Rects: rects,
	}
}

// DescribeDomNode formats a node for failure messages: `div[role=dialog] "Cookie settings"`.
// upstream: packages/coding-agent/src/tools/browser/tern/dom.ts:361-365
func DescribeDomNode(node DomNode) string {
	tag := node.Tag
	if tag == "" {
		tag = node.Role
	}
	role := ""
	if node.Role != tag && node.Role != "generic" {
		role = fmt.Sprintf("[role=%s]", node.Role)
	}
	name := ""
	if node.Name != "" {
		name = " " + jsonQuote(node.Name)
	}
	return tag + role + name
}

// InnerHtmlOf extracts innerHTML from an outerHTML string and tag name.
// upstream: packages/coding-agent/src/tools/browser/tern/dom.ts:372-390
func InnerHtmlOf(outer, tag string) string {
	quoted := false
	open := -1
	for i := range len(outer) {
		ch := outer[i]
		if ch == '"' {
			quoted = !quoted
		} else if ch == '>' && !quoted {
			open = i
			break
		}
	}
	if open < 0 {
		return ""
	}
	closeTag := "</" + tag + ">"
	end := len(outer) - len(closeTag)
	if end <= open || !strings.EqualFold(outer[end:], closeTag) {
		return ""
	}
	return outer[open+1 : end]
}

var simpleNameRE = regexp.MustCompile(`^[A-Za-z_][\w-]*$`)

func cssStringContent(value string) string {
	var b strings.Builder
	for _, r := range value {
		if r == '"' || r == '\\' {
			b.WriteByte('\\')
			b.WriteRune(r)
		} else if r < 0x20 || r == 0x7f {
			fmt.Fprintf(&b, "\\%x ", r)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// DomCandidateSelector builds a CSS selector matching node by tag and simple attributes.
// upstream: packages/coding-agent/src/tools/browser/tern/dom.ts:414-423
func DomCandidateSelector(node DomNode) string {
	sel := "*"
	if node.Tag != "" && simpleNameRE.MatchString(node.Tag) {
		sel = node.Tag
	}
	// Sort attribute keys for deterministic output.
	keys := make([]string, 0, len(node.Attributes))
	for k := range node.Attributes {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, name := range keys {
		val := node.Attributes[name]
		if name == HandleAttribute || !simpleNameRE.MatchString(name) || len(val) > 200 {
			continue
		}
		sel += fmt.Sprintf(`[%s="%s"]`, name, cssStringContent(val))
	}
	return sel
}
