package frontend

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/stencil-hq/tern-sdk/go/tern"
	"github.com/stencil-hq/tern-sdk/go/tern/ui"
)

// ompModelPickerScopeAll is the "all providers" scope ID matching omp's
// ALL_SCOPE (packages/tui/src/overlays/model-picker.ts:121).
const ompModelPickerScopeAll = "all"

// ompModelPickerScopePrefix prefixes provider rail scope IDs matching omp's
// PROVIDER_SCOPE (packages/tui/src/overlays/model-picker.ts:123).
const ompModelPickerScopePrefix = "provider:"

// ompThinkingStepLabels mirrors THINKING_STEP_LABELS
// (packages/tui/src/overlays/model-picker.ts:126-135).
var ompThinkingStepLabels = map[string]string{
	"off":     "Off",
	"minimal": "Min",
	"low":     "Low",
	"medium":  "Med",
	"high":    "High",
	"xhigh":   "XHigh",
	"max":     "Max",
}

// ompFrozenThinkingSteps mirrors FROZEN_THINKING_STEPS
// (packages/tui/src/overlays/model-picker.ts:137-143).
var ompFrozenThinkingSteps = []string{"off", "minimal", "low", "medium", "high"}

// ompPickerKind identifies which specialized omp picker layout a PiG Selector
// represents.
type ompPickerKind int

const (
	ompPickerGeneric ompPickerKind = iota
	ompPickerModel
	ompPickerThinking
	ompPickerSession
	ompPickerTree
)

func classifyOmpPicker(sel fe.Selector, tree bool) ompPickerKind {
	lowerTitle := strings.ToLower(sel.Title)
	switch {
	case tree || strings.Contains(lowerTitle, "tree") || strings.Contains(lowerTitle, "branch"):
		return ompPickerTree
	case strings.Contains(lowerTitle, "model") && !strings.Contains(lowerTitle, "configuration"):
		return ompPickerModel
	case strings.Contains(lowerTitle, "thinking") || strings.Contains(lowerTitle, "effort"):
		return ompPickerThinking
	case strings.Contains(lowerTitle, "session"):
		return ompPickerSession
	default:
		return ompPickerGeneric
	}
}

// ompIsModelPicker reports whether sel should be drawn as omp's composer-anchored
// model picker (packages/tui/src/overlays/model-picker.ts:616-699).
func ompIsModelPicker(sel fe.Selector, tree bool) bool {
	return classifyOmpPicker(sel, tree) == ompPickerModel
}

// ompModelPickerState holds per-picker ephemeral UI state for omp's model
// picker (provider rail filter, pending thinking slider value, and last-sent
// raw props for diffing).
type ompModelPickerState struct {
	scope           string
	pendingThinking string
	// Last-sent raw props for diffing in queueOmpModelPicker.
	lastScopes   []map[string]any
	lastScope    string
	lastOrder    []any
	lastLevel    map[string]any
	lastTotal    int
	hasSentExtra bool
}

// ompPickerElement draws a PiG Selector with omp v19.1.1's picker props:
//   - model picker: composer-anchored (`anchor: "composer"`), search-only head
//     (`search: "list"`), `density: "snug"`, `layout: "rows"`, `preview: "none"`,
//     `scopeStyle: "rail"`, `Ctx` and `$/M` fact columns, `actions: []`,
//     no title bar unless status/error is present
//     (upstream: packages/tui/src/overlays/model-picker.ts:647-699).
//   - tree checked before session so PiG's "Session Tree" gets noun "entries",
//     icon "git-branch", layout "tree", placeholder "Search entries…"
//     (upstream: packages/tui/src/overlays/tree-selector.ts:1630-1645).
//   - thinking ("levels", icon "brain", upstream: model-picker.ts:693).
//   - sessions ("sessions", icon "history", layout "cards", placeholder
//     "Search sessions…", upstream: session-selector.ts:2196-2210).
//   - generic selectors: right-aligned Close + primary Select actions, noun
//     "options" (upstream: native/picker.ts:78, 226, 235).
func (s *session) ompPickerElement(id string, sel fe.Selector, tree bool) tern.Element {
	kind := classifyOmpPicker(sel, tree)
	if kind == ompPickerModel {
		return s.ompModelPickerElement(id, sel)
	}

	size := ui.SizeMD
	noun := "options"
	var icon ui.IconName
	var layout ui.PickerLayout
	var placeholder string
	switch kind {
	case ompPickerTree:
		size = ui.SizeLG
		noun = "entries"
		icon = ui.IconName("git-branch")
		layout = ui.LayoutTree
		placeholder = "Search entries…"
	case ompPickerThinking:
		size = ui.SizeMD
		noun = "levels"
		icon = ui.IconName("brain")
	case ompPickerSession:
		size = ui.SizeLG
		noun = "sessions"
		icon = ui.IconHistory
		layout = ui.LayoutCards
		placeholder = "Search sessions…"
	}

	picker := ui.Picker{
		PickerCommon: ui.PickerCommon{Key: id},
		Size:         size,
		Layout:       layout,
		Preview:      ui.PaneNone,
		Icon:         icon,
		Noun:         noun,
		Placeholder:  placeholder,
		Title:        ui.T(sel.Title),
		Subtitle:     ui.T(joinNonEmpty(" · ", sel.Description, sel.Status)),
		Tab:          sel.Tab,
		Actions: []ui.PickerAction{
			{ID: "choose", Label: "Select", Keys: []string{"enter"}, Primary: true},
			{ID: "close", Label: "Close", Keys: []string{"esc"}, End: true},
		},
	}
	for _, tab := range sel.Tabs {
		picker.Tabs = append(picker.Tabs, ui.Tab{ID: tab.ID, Label: ui.T(tab.Label)})
	}
	if sel.Loading {
		picker.State = ui.StateLoading
	}
	if sel.Confirm != "" {
		picker.Confirm = &ui.Confirm{Text: ui.T(sel.Confirm)}
	}
	return picker
}

// ompModelPickerElement builds omp's composer-anchored model picker shell
// (packages/tui/src/overlays/model-picker.ts:647-675).
func (s *session) ompModelPickerElement(id string, sel fe.Selector) tern.Element {
	ctxPrio := 1.0
	pricePrio := 2.0
	extra := map[string]any{
		"anchor":     "composer",
		"search":     "list",
		"density":    "snug",
		"scopeStyle": "rail",
		// Explicit empty actions array suppresses Tern's bottom Close/Select bar
		// (click/Enter picks, Esc closes), matching model-picker.ts:665-674.
		"actions": []any{},
	}
	picker := ui.Picker{
		PickerCommon: ui.PickerCommon{
			Key:   id,
			Props: extra,
		},
		Size:        ui.SizeMD,
		Layout:      ui.LayoutRows,
		Preview:     ui.PaneNone,
		Icon:        ui.IconName("cpu"),
		Noun:        "models",
		Placeholder: "Search models…",
		Empty:       ui.T("No models available"),
		Columns: []ui.PickerColumn{
			{ID: "ctx", Head: "Ctx", Format: ui.FormatNum, Priority: &ctxPrio},
			{ID: "price", Head: "$/M", Format: ui.FormatPrice, Priority: &pricePrio},
		},
	}
	// Only show a title/subtitle when PiG reports an error or warning description
	// (model-picker.ts:660-661: "The head is the search alone; a config error
	if errMsg := ompModelPickerErrorSubtitle(sel); errMsg != "" {
		picker.Title = ui.T("Switch model")
		picker.Subtitle = ui.Spans(ui.Span{T: errMsg, S: "error"})
	}
	if sel.Loading {
		picker.State = ui.StateLoading
	}
	if sel.Confirm != "" {
		picker.Confirm = &ui.Confirm{Text: ui.T(sel.Confirm)}
	}
	return picker
}

// ompModelPickerErrorSubtitle returns a subtitle only when sel carries an
// actual error/warning (ignoring transient "Refreshing model catalogs…" or
// "Model catalogs refreshed." status messages so the clean search-only head
// matches omp's model picker).
func ompModelPickerErrorSubtitle(sel fe.Selector) string {
	if d := strings.TrimSpace(sel.Description); d != "" {
		return d
	}
	st := strings.TrimSpace(sel.Status)
	if st == "" {
		return ""
	}
	lower := strings.ToLower(st)
	if strings.HasPrefix(lower, "refreshing ") || strings.HasSuffix(lower, "refreshed.") {
		return ""
	}
	return st
}

// ompProviderMark builds omp's provider avatar/logo mark
// (packages/tui/src/overlays/model-browser.ts:980-988): Tern draws the
// provider's vector glyph for `provider:<id>`, falling back to initials on its
// seeded gradient.
func ompProviderMark(providerID string) map[string]any {
	if providerID == "" {
		return nil
	}
	words := strings.FieldsFunc(providerID, func(r rune) bool {
		return r == '-' || r == '_' || r == ' ' || r == '\t' || r == '.'
	})
	var text string
	if len(words) >= 2 {
		r0 := []rune(words[0])
		r1 := []rune(words[1])
		text = strings.ToUpper(string(r0[0]) + string(r1[0]))
	} else {
		word := providerID
		if len(words) == 1 {
			word = words[0]
		}
		rs := []rune(word)
		switch {
		case len(rs) >= 2:
			text = strings.ToUpper(string(rs[0])) + string(rs[1])
		case len(rs) == 1:
			text = strings.ToUpper(string(rs[0]))
		}
	}
	return map[string]any{
		"text": text,
		"seed": providerID,
		"icon": "provider:" + providerID,
	}
}

// ompSplitModelSelector splits a PiG model selector item ID ("provider/modelID")
// into its provider and model ID components, falling back to Detail if needed.
func ompSplitModelSelector(item fe.SelectorItem) (provider, modelID string) {
	if p, id, ok := strings.Cut(item.ID, "/"); ok && p != "" && id != "" {
		return p, id
	}
	detail := strings.TrimSpace(item.Detail)
	if before, _, ok := strings.Cut(detail, " · "); ok {
		detail = strings.TrimSpace(before)
	}
	if detail != "" && detail != "unavailable" {
		return detail, item.Label
	}
	return "", item.Label
}

// ompFormatCostLeg formats one per-million-token cost leg as omp's
// formatCostLeg does (integer when whole, else trimmed decimal).
func ompFormatCostLeg(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
		return "0"
	}
	if math.Abs(v-math.Round(v)) < 1e-6 {
		return strconv.Itoa(int(math.Round(v)))
	}
	s := strconv.FormatFloat(v, 'f', 2, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	return s
}

// ompModelPickerItem converts one PiG SelectorItem into omp's rich TspPickerItem
// (model-browser.ts:2222-2284): monospace id label, provider mark glyph, Ctx
// and $/M facts, ctx>... / free / default badges, and no redundant provider
// detail string.
func (s *session) ompModelPickerItem(item fe.SelectorItem) map[string]any {
	provider, modelID := ompSplitModelSelector(item)
	label := item.Label
	if label == "" {
		label = modelID
	}
	out := map[string]any{
		"id":    item.ID,
		"label": label,
		"mono":  true,
	}
	if mark := ompProviderMark(provider); mark != nil {
		out["mark"] = mark
	}
	var meta ModelMeta
	var hasMeta bool
	if s.modelMeta != nil {
		meta, hasMeta = s.modelMeta(provider, modelID)
	}
	facts := map[string]any{}
	var badges []map[string]any
	if hasMeta {
		if meta.ContextWindow > 0 {
			facts["ctx"] = meta.ContextWindow
		}
		if meta.InputCostPer1M == 0 && meta.OutputCostPer1M == 0 {
			facts["price"] = "free"
			badges = append(badges, map[string]any{"text": "free", "tone": "success"})
		} else {
			facts["price"] = "$" + ompFormatCostLeg(meta.InputCostPer1M) + "·" + ompFormatCostLeg(meta.OutputCostPer1M)
		}
		if s.hasFooter && !s.footer.ContextUsage.Unknown && s.footer.ContextUsage.Tokens > 0 && meta.ContextWindow > 0 && s.footer.ContextUsage.Tokens > meta.ContextWindow {
			badges = append([]map[string]any{{
				"text":  "ctx>" + strings.ToLower(ompFormatNumber(meta.ContextWindow)),
				"tone":  "warning",
				"title": fmt.Sprintf("Context %s exceeds this model's limit; picking it compacts first", strings.ToLower(ompFormatNumber(s.footer.ContextUsage.Tokens))),
			}}, badges...)
		}
	}
	if len(facts) > 0 {
		out["facts"] = facts
	}
	if len(badges) > 0 {
		if len(badges) > 3 {
			badges = badges[:3]
		}
		out["badges"] = badges
	}
	if hasMeta && meta.Name != "" && meta.Name != label {
		out["title"] = meta.Name + " (" + provider + ")"
	} else if provider != "" {
		out["title"] = provider + "/" + label
	}
	return out
}

// ompModelPickerScopes builds the alphabetical left provider icon rail from
// every item currently in the selector's catalog (model-picker.ts:413-427).
func ompModelPickerScopes(items []fe.SelectorItem) []map[string]any {
	counts := map[string]int{}
	for _, it := range items {
		provider, _ := ompSplitModelSelector(it)
		if provider == "" {
			continue
		}
		counts[provider]++
	}
	if len(counts) == 0 {
		return nil
	}
	providers := make([]string, 0, len(counts))
	for p := range counts {
		providers = append(providers, p)
	}
	sort.Strings(providers)
	scopes := make([]map[string]any, 0, len(providers))
	for _, p := range providers {
		sc := map[string]any{
			"id":    ompModelPickerScopePrefix + p,
			"label": p,
			"count": counts[p],
		}
		if mark := ompProviderMark(p); mark != nil {
			sc["mark"] = mark
		}
		scopes = append(scopes, sc)
	}
	return scopes
}

// ompModelPickerOrder builds the grouped `order` array ("Recent" + "All" or
// "Matches" when searching) filtered by the active provider rail scope.
// Also returns the effective selected ID and total visible row count.
func (s *session) ompModelPickerOrder(sel fe.Selector, activeScope string) (order []any, effectiveSelected string, total int) {
	providerFilter := ""
	if strings.HasPrefix(activeScope, ompModelPickerScopePrefix) {
		providerFilter = strings.TrimPrefix(activeScope, ompModelPickerScopePrefix)
	}
	visible := make([]fe.SelectorItem, 0, len(sel.Items))
	visibleByID := make(map[string]fe.SelectorItem, len(sel.Items))
	for _, it := range sel.Items {
		if providerFilter != "" {
			p, _ := ompSplitModelSelector(it)
			if p != providerFilter {
				continue
			}
		}
		visible = append(visible, it)
		visibleByID[it.ID] = it
	}
	total = len(visible)
	if total == 0 {
		return []any{}, "", 0
	}

	effectiveSelected = sel.Selected
	if _, ok := visibleByID[effectiveSelected]; !ok {
		effectiveSelected = visible[0].ID
	}

	// Build Recent group from session MRU + currently checked model when not
	// searching (matching omp's Recent / All split in model-browser.ts:2292).
	query := strings.TrimSpace(sel.Query)
	recentSet := map[string]bool{}
	var recentIDs []string
	if query == "" {
		// Checked (current) model first if present in visible, then session MRUs.
		for _, it := range visible {
			if it.Checked && !recentSet[it.ID] {
				recentSet[it.ID] = true
				recentIDs = append(recentIDs, it.ID)
			}
		}
		for _, id := range s.recentModels {
			if _, ok := visibleByID[id]; ok && !recentSet[id] {
				recentSet[id] = true
				recentIDs = append(recentIDs, id)
			}
		}
	}

	restIDs := make([]string, 0, len(visible)-len(recentIDs))
	for _, it := range visible {
		if !recentSet[it.ID] {
			restIDs = append(restIDs, it.ID)
		}
	}

	order = make([]any, 0, len(visible)+2)
	if len(recentIDs) > 0 {
		order = append(order, map[string]any{
			"group": "recent",
			"label": "Recent",
			"count": len(recentIDs),
		})
		for _, id := range recentIDs {
			order = append(order, id)
		}
	}
	if len(restIDs) > 0 {
		restLabel := "All"
		if query != "" {
			restLabel = "Matches"
		}
		order = append(order, map[string]any{
			"group": strings.ToLower(restLabel),
			"label": restLabel,
			"count": len(restIDs),
		})
		for _, id := range restIDs {
			order = append(order, id)
		}
	}
	return order, effectiveSelected, total
}

// ompModelPickerLevel builds the glowing Thinking Level slider prop for the
// highlighted model (packages/tui/src/overlays/model-picker.ts:681-699).
func (s *session) ompModelPickerLevel(sel fe.Selector, selectedID string, pending string) map[string]any {
	var selectedItem fe.SelectorItem
	found := false
	for _, it := range sel.Items {
		if it.ID == selectedID {
			selectedItem = it
			found = true
			break
		}
	}
	if !found && len(sel.Items) > 0 {
		selectedItem = sel.Items[0]
		found = true
	}
	if !found {
		return nil
	}
	provider, modelID := ompSplitModelSelector(selectedItem)
	var meta ModelMeta
	var hasMeta bool
	if s.modelMeta != nil {
		meta, hasMeta = s.modelMeta(provider, modelID)
	}

	var stepIDs []string
	var disabled string
	switch {
	case hasMeta && !meta.Reasoning:
		stepIDs = ompFrozenThinkingSteps
		displayName := meta.Name
		if displayName == "" {
			displayName = modelID
		}
		disabled = displayName + " does not reason"
	case hasMeta && len(meta.ThinkingLevels) > 0:
		stepIDs = meta.ThinkingLevels
	case s.hasFooter && s.footer.ThinkingLevel != "":
		stepIDs = ompFrozenThinkingSteps
	default:
		// Without model metadata or active thinking level, still show the standard
		// ladder if the model looks reasoning-capable or session has thinking.
		stepIDs = ompFrozenThinkingSteps
	}

	cur := pending
	if cur == "" && s.hasFooter {
		cur = s.footer.ThinkingLevel
	}
	if disabled != "" {
		cur = "off"
	} else if !slices.Contains(stepIDs, cur) {
		if len(stepIDs) > 0 {
			cur = stepIDs[0]
		} else {
			cur = "off"
		}
	}

	steps := make([]map[string]any, 0, len(stepIDs))
	for _, id := range stepIDs {
		lbl := ompThinkingStepLabels[id]
		if lbl == "" {
			lbl = id
		}
		steps = append(steps, map[string]any{"id": id, "label": lbl})
	}
	level := map[string]any{
		"label": "Thinking",
		"steps": steps,
		"value": cur,
		"icon":  "brain",
		"keys":  []string{"left", "right"},
	}
	if provider != "" {
		level["detail"] = provider
	}
	if disabled != "" {
		level["disabled"] = disabled
	}
	return level
}

// recordRecentModel moves fq ("provider/id") to the front of s.recentModels.
func (s *session) recordRecentModel(fq string) {
	if fq == "" {
		return
	}
	idx := slices.Index(s.recentModels, fq)
	if idx == 0 {
		return
	}
	if idx > 0 {
		s.recentModels = slices.Delete(s.recentModels, idx, idx+1)
	}
	s.recentModels = append([]string{fq}, s.recentModels...)
	if len(s.recentModels) > 8 {
		s.recentModels = s.recentModels[:8]
	}
}

// ompModelPickerSetScope updates the active provider rail scope on the open
// model picker and flushes the updated order/scope raw props to Tern.
func (s *session) ompModelPickerSetScope(id, picked string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	at := slices.IndexFunc(s.dock, func(e *entry) bool { return e.id == id })
	if at < 0 {
		return
	}
	e := s.dock[at]
	sel, ok := e.node.(fe.Selector)
	if !ok || !ompIsModelPicker(sel, e.tree) {
		return
	}
	if e.ompModel == nil {
		e.ompModel = &ompModelPickerState{scope: ompModelPickerScopeAll}
	}
	next := picked
	if next == "" || next == e.ompModel.scope {
		next = ompModelPickerScopeAll
	}
	if next == e.ompModel.scope {
		return
	}
	e.ompModel.scope = next
	s.queueOmpModelPicker(e, sel, false)
	_ = s.flushLocked()
}

// ompModelPickerSetThinking updates the thinking slider value on the open model
// picker, applies it via Options.OnThinkingLevel (or /effort fallback), and
// flushes the updated level prop to Tern immediately.
func (s *session) ompModelPickerSetThinking(id, value string) {
	if value == "" {
		return
	}
	s.mu.Lock()
	at := slices.IndexFunc(s.dock, func(e *entry) bool { return e.id == id })
	if at < 0 {
		s.mu.Unlock()
		return
	}
	e := s.dock[at]
	sel, ok := e.node.(fe.Selector)
	if !ok || !ompIsModelPicker(sel, e.tree) {
		s.mu.Unlock()
		return
	}
	if e.ompModel == nil {
		e.ompModel = &ompModelPickerState{scope: ompModelPickerScopeAll}
	}
	e.ompModel.pendingThinking = value
	if s.hasFooter {
		s.footer.ThinkingLevel = value
	}
	s.queueOmpModelPicker(e, sel, false)
	_ = s.flushLocked()
	onThinking := s.onThinkingLevel
	s.mu.Unlock()

	if onThinking != nil {
		onThinking(value)
	}
}

// queueOmpModelPicker sends omp's rich model picker raw props: enriched items
// (provider mark, mono, Ctx/$/M facts, free/ctx badges), alphabetical provider
// rail scopes, grouped Recent/All order, current, selected, query, total, and
// the glowing Thinking level slider.
func (s *session) queueOmpModelPicker(e *entry, sel fe.Selector, fresh bool) {
	if e.ompModel == nil {
		e.ompModel = &ompModelPickerState{scope: ompModelPickerScopeAll}
	}
	st := e.ompModel
	if st.scope == "" {
		st.scope = ompModelPickerScopeAll
	}

	// Update enriched catalog items.
	e.catalog.updateWith(sel, s.ompModelPickerItem)

	// Build provider rail scopes from every item seen in the catalog (so typing
	// or switching scope keeps the full provider rail stable).
	scopes := ompModelPickerScopes(e.catalog.seen)
	if st.scope != ompModelPickerScopeAll {
		valid := false
		for _, sc := range scopes {
			if sc["id"] == st.scope {
				valid = true
				break
			}
		}
		if !valid {
			st.scope = ompModelPickerScopeAll
		}
	}

	// Track checked/current model as recent.
	var current []string
	for _, it := range sel.Items {
		if it.Checked {
			current = append(current, it.ID)
			s.recordRecentModel(it.ID)
		}
	}

	order, effectiveSelected, total := s.ompModelPickerOrder(sel, st.scope)
	level := s.ompModelPickerLevel(sel, effectiveSelected, st.pendingThinking)

	changed := map[string]any{}
	if fresh || !sameSlice(e.catalog.items, e.raw.items) {
		changed["items"] = e.catalog.items
	}
	if fresh || !st.hasSentExtra || !ompDeepEqualAny(scopes, st.lastScopes) {
		if len(scopes) > 0 {
			changed["scopes"] = scopes
		} else {
			changed["scopes"] = nil
		}
	}
	if fresh || !st.hasSentExtra || st.scope != st.lastScope {
		changed["scope"] = st.scope
	}
	if fresh || !st.hasSentExtra || !ompDeepEqualAny(order, st.lastOrder) {
		changed["order"] = order
	}
	if fresh || !slices.Equal(current, e.raw.current) {
		changed["current"] = current
	}
	if fresh || effectiveSelected != e.raw.selected {
		changed["selected"] = nullable(effectiveSelected)
	}
	if fresh || !st.hasSentExtra || total != st.lastTotal {
		changed["total"] = total
	}
	if fresh || sel.Searchable != e.raw.searchable || sel.Query != e.raw.query {
		changed["query"] = nil
		if sel.Searchable {
			changed["query"] = sel.Query
		}
	}
	if fresh || !st.hasSentExtra || !ompDeepEqualAny(level, st.lastLevel) {
		if level != nil {
			changed["level"] = level
		} else {
			changed["level"] = nil
		}
	}
	if len(changed) > 0 {
		s.queue(tern.OpSet(e.tid, changed))
	}
	e.raw.items, e.raw.current = e.catalog.items, current
	e.raw.selected, e.raw.query, e.raw.searchable = effectiveSelected, sel.Query, sel.Searchable
	st.lastScopes, st.lastScope, st.lastOrder, st.lastLevel, st.lastTotal, st.hasSentExtra = scopes, st.scope, order, level, total, true
}

// ompDeepEqualAny compares small JSON-shaped maps/slices without importing reflect
// into hot paths (used only on model picker updates).
func ompDeepEqualAny(a, b any) bool {
	switch av := a.(type) {
	case nil:
		return b == nil
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case int:
		bv, ok := b.(int)
		return ok && av == bv
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	case []string:
		bv, ok := b.([]string)
		return ok && slices.Equal(av, bv)
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !ompDeepEqualAny(av[i], bv[i]) {
				return false
			}
		}
		return true
	case []map[string]any:
		bv, ok := b.([]map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !ompDeepEqualAny(av[i], bv[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, v := range av {
			other, exists := bv[k]
			if !exists || !ompDeepEqualAny(v, other) {
				return false
			}
		}
		return true
	default:
		return a == b
	}
}

// ompPrefsElement delegates to pig-tern's prefsElement. PiG's frontend.Settings
// is a flat list without category/page metadata, so omp's multi-page settings
// sections cannot be derived from it.
func ompPrefsElement(id string, settings fe.Settings, editing string) tern.Element {
	return prefsElement(id, settings, editing)
}
