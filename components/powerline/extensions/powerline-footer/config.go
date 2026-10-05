package powerline_footer

import (
	"math"
	"regexp"
	"slices"
	"strings"
)

// The powerline setting of settings.json, normalised. upstream: powerline-config.ts.

type powerlineConfig struct {
	Preset                  string
	CustomItems             []customItem
	DisabledSegments        []string
	InvalidDisabledSegments []string
	Layout                  *statusLayout
	InvalidLayoutSegments   []string
	Separator               string // "" when unset
	SegmentOptions          segmentOptions
	Placement               string
	InvalidPlacement        *string
	Welcome                 bool
	StashSharpSShortcut     bool
	CompactPromptMode       string
	SendDelayMs             float64
	AutoFollowUp            bool
	WorkingVibesColor       string // "" when unset
}

var builtinSegmentIDs = []string{"model", "shell_mode", "path", "git", "subagents", "queue", "token_in", "token_out", "token_total",
	"cost", "context_pct", "context_total", "time_spent", "time", "session", "hostname", "cache_read", "cache_write", "thinking", "extension_statuses"}

var separatorStyles = []string{"powerline", "powerline-thin", "slash", "pipe", "block", "none", "ascii", "dot", "chevron", "star"}

func asObject(v any) *jsObject {
	o, _ := v.(*jsObject)
	return o
}

func normalizePreset(value any, names []string) string {
	s, ok := value.(string)
	if !ok {
		return ""
	}
	n := strings.ToLower(jsTrim(s))
	if slices.Contains(names, n) {
		return n
	}
	return ""
}

func normalizePlacement(value any, present bool) (string, *string) {
	if !present {
		return "above", nil
	}
	n := ""
	if s, ok := value.(string); ok {
		n = strings.ToLower(jsTrim(s))
	}
	if n == "above" || n == "below" {
		return n, nil
	}
	invalid := jsString(value)
	if s, ok := value.(string); ok {
		invalid = jsTrim(s)
	}
	return "above", &invalid
}

// jsString is String(value) for decoded JSON values.
func jsString(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		return jsNumber(x)
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			if e != nil {
				parts[i] = jsString(e)
			}
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}

func normalizeSeparator(value any) string {
	s, ok := value.(string)
	if !ok {
		return ""
	}
	n := strings.ToLower(jsTrim(s))
	if slices.Contains(separatorStyles, n) {
		return n
	}
	return ""
}

var customIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func normalizeCustomItemID(value any) string {
	s, ok := value.(string)
	if !ok {
		return ""
	}
	n := jsTrim(s)
	if n != "" && customIDPattern.MatchString(n) {
		return n
	}
	return ""
}

func trimmedString(value any) string {
	if s, ok := value.(string); ok {
		return jsTrim(s)
	}
	return ""
}

func normalizeCustomStatusItem(raw any, idOverride string, hasOverride bool) (customItem, bool) {
	o := asObject(raw)
	if o == nil {
		return customItem{}, false
	}
	var idv any = o.vals["id"]
	if hasOverride {
		idv = idOverride
	}
	id := normalizeCustomItemID(idv)
	if id == "" {
		return customItem{}, false
	}
	statusKey := trimmedString(o.vals["statusKey"])
	if statusKey == "" {
		statusKey = id
	}
	position := "right"
	if p, _ := o.vals["position"].(string); p == "left" || p == "right" || p == "secondary" {
		position = p
	}
	selfColorize, _ := o.vals["selfColorize"].(bool)
	hide, hasHide := o.vals["hideWhenMissing"].(bool)
	exclude, hasExclude := o.vals["excludeFromExtensionStatuses"].(bool)
	return customItem{
		ID: id, StatusKey: statusKey, Position: position,
		Color: trimmedString(o.vals["color"]), SelfColorize: selfColorize, Prefix: trimmedString(o.vals["prefix"]),
		HideWhenMissing:     !hasHide || hide,
		ExcludeFromStatuses: !hasExclude || exclude,
	}, true
}

func normalizeCustomItems(raw any) []customItem {
	var normalized []customItem
	switch x := raw.(type) {
	case []any:
		for _, e := range x {
			if it, ok := normalizeCustomStatusItem(e, "", false); ok {
				normalized = append(normalized, it)
			}
		}
	case *jsObject:
		for _, k := range x.order() {
			if it, ok := normalizeCustomStatusItem(x.vals[k], k, true); ok {
				normalized = append(normalized, it)
			}
		}
	}
	// A repeated id keeps its first position and takes the last item, like a Map.
	var out []customItem
	index := map[string]int{}
	for _, it := range normalized {
		if i, ok := index[it.ID]; ok {
			out[i] = it
			continue
		}
		index[it.ID] = len(out)
		out = append(out, it)
	}
	return out
}

func normalizeSegmentID(value any, customIDs map[string]bool) string {
	s, ok := value.(string)
	if !ok {
		return ""
	}
	n := jsTrim(s)
	if slices.Contains(builtinSegmentIDs, n) {
		return n
	}
	if rest, ok := strings.CutPrefix(n, "custom:"); ok {
		if id := normalizeCustomItemID(rest); id != "" && customIDs[id] {
			return "custom:" + id
		}
	}
	return ""
}

func customIDSet(items []customItem) map[string]bool {
	m := map[string]bool{}
	for _, it := range items {
		m[it.ID] = true
	}
	return m
}

func entryText(entry any) string {
	if s, ok := entry.(string); ok {
		return jsTrim(s)
	}
	return jsString(entry)
}

func normalizeDisabledSegments(raw any, items []customItem) (disabled, invalid []string) {
	disabled, invalid = []string{}, []string{}
	arr, ok := raw.([]any)
	if !ok {
		return
	}
	ids := customIDSet(items)
	seen := map[string]bool{}
	for _, e := range arr {
		id := normalizeSegmentID(e, ids)
		if id == "" {
			invalid = append(invalid, entryText(e))
		} else if !seen[id] {
			seen[id] = true
			disabled = append(disabled, id)
		}
	}
	return
}

func normalizeLayout(raw any, items []customItem) (*statusLayout, []string) {
	invalid := []string{}
	o := asObject(raw)
	if o == nil {
		return nil, invalid
	}
	ids := customIDSet(items)
	placed := map[string]bool{}
	layout := &statusLayout{}
	placedAny := false
	for _, row := range []string{"left", "right", "secondary"} {
		entries, ok := o.vals[row].([]any)
		if !ok {
			continue
		}
		segments := []string{}
		seen := map[string]bool{}
		for _, e := range entries {
			id := normalizeSegmentID(e, ids)
			switch {
			case id == "":
				invalid = append(invalid, row+":"+entryText(e))
			case !seen[id]:
				seen[id] = true
				if placed[id] {
					invalid = append(invalid, row+":"+id)
				} else {
					placed[id] = true
					segments = append(segments, id)
				}
			}
		}
		switch row {
		case "left":
			layout.Left = segments
		case "right":
			layout.Right = segments
		default:
			layout.Secondary = segments
		}
		placedAny = true
	}
	if !placedAny {
		return nil, invalid
	}
	return layout, invalid
}

func str(o *jsObject, k string) (string, bool) {
	s, ok := o.vals[k].(string)
	return s, ok
}

func oneOf(o *jsObject, k string, allowed ...string) *string {
	if s, ok := str(o, k); ok && slices.Contains(allowed, s) {
		return &s
	}
	return nil
}

func boolOpt(o *jsObject, k string) *bool {
	if b, ok := o.vals[k].(bool); ok {
		return &b
	}
	return nil
}

func normalizeSegmentOptions(raw *jsObject) segmentOptions {
	var opts segmentOptions
	if o := asObject(raw.vals["model"]); o != nil {
		opts.Model = &modelOptions{ShowThinkingLevel: boolOpt(o, "showThinkingLevel"), Display: oneOf(o, "display", "name", "qualified")}
	}
	if o := asObject(raw.vals["path"]); o != nil {
		p := &pathOptions{Mode: oneOf(o, "mode", "basename", "abbreviated", "full")}
		if n, ok := o.vals["maxLength"].(float64); ok && !math.IsInf(n, 0) && !math.IsNaN(n) && n > 0 {
			p.MaxLength = ptr(int(math.Floor(n)))
		}
		opts.Path = p
	}
	if o := asObject(raw.vals["git"]); o != nil {
		opts.Git = &gitOptions{ShowBranch: boolOpt(o, "showBranch"), ShowStaged: boolOpt(o, "showStaged"), ShowUnstaged: boolOpt(o, "showUnstaged"),
			ShowUntracked: boolOpt(o, "showUntracked"), Polling: oneOf(o, "polling", "full", "branch", "off"), HostIcon: boolOpt(o, "hostIcon")}
	}
	if o := asObject(raw.vals["time"]); o != nil {
		opts.Time = &timeOptions{Format: oneOf(o, "format", "12h", "24h"), ShowSeconds: boolOpt(o, "showSeconds")}
	}
	if o := asObject(raw.vals["cost"]); o != nil {
		c := &costOptions{SubscriptionDisplay: oneOf(o, "subscriptionDisplay", "subscription", "reported-cost", "both")}
		if cur := normalizeCostCurrency(o.vals["currency"]); cur != "" {
			c.Currency = &cur
		}
		opts.Cost = c
	}
	if o := asObject(raw.vals["context"]); o != nil {
		opts.Context = &contextOptions{Format: oneOf(o, "format", "full", "percent")}
	}
	if o := asObject(raw.vals["cache_read"]); o != nil {
		opts.CacheRead = &cacheReadOptions{Format: oneOf(o, "format", "tokens", "percent", "both")}
	}
	return opts
}

// mergeSegmentOptions lets overrides win key by key; every segment's options exist afterwards, as in the original.
func mergeSegmentOptions(defaults, overrides segmentOptions) segmentOptions {
	return segmentOptions{
		Model:     mergeOpt(defaults.Model, overrides.Model, mergeModel),
		Path:      mergeOpt(defaults.Path, overrides.Path, mergePath),
		Git:       mergeOpt(defaults.Git, overrides.Git, mergeGit),
		Time:      mergeOpt(defaults.Time, overrides.Time, mergeTime),
		Cost:      mergeOpt(defaults.Cost, overrides.Cost, mergeCost),
		Context:   mergeOpt(defaults.Context, overrides.Context, mergeContext),
		CacheRead: mergeOpt(defaults.CacheRead, overrides.CacheRead, mergeCacheRead),
	}
}

func mergeOpt[T any](d, o *T, merge func(d, o T) T) *T {
	var dv, ov T
	if d != nil {
		dv = *d
	}
	if o != nil {
		ov = *o
	}
	m := merge(dv, ov)
	return &m
}

func pick[T any](d, o *T) *T {
	if o != nil {
		return o
	}
	return d
}

func mergeModel(d, o modelOptions) modelOptions {
	return modelOptions{pick(d.ShowThinkingLevel, o.ShowThinkingLevel), pick(d.Display, o.Display)}
}
func mergePath(d, o pathOptions) pathOptions {
	return pathOptions{pick(d.Mode, o.Mode), pick(d.MaxLength, o.MaxLength)}
}
func mergeGit(d, o gitOptions) gitOptions {
	return gitOptions{pick(d.ShowBranch, o.ShowBranch), pick(d.ShowStaged, o.ShowStaged), pick(d.ShowUnstaged, o.ShowUnstaged),
		pick(d.ShowUntracked, o.ShowUntracked), pick(d.Polling, o.Polling), pick(d.HostIcon, o.HostIcon)}
}
func mergeTime(d, o timeOptions) timeOptions {
	return timeOptions{pick(d.Format, o.Format), pick(d.ShowSeconds, o.ShowSeconds)}
}
func mergeCost(d, o costOptions) costOptions {
	return costOptions{pick(d.SubscriptionDisplay, o.SubscriptionDisplay), pick(d.Currency, o.Currency)}
}
func mergeContext(d, o contextOptions) contextOptions {
	return contextOptions{pick(d.Format, o.Format)}
}
func mergeCacheRead(d, o cacheReadOptions) cacheReadOptions {
	return cacheReadOptions{pick(d.Format, o.Format)}
}

func parsePowerlineConfig(value any, presetNames []string) powerlineConfig {
	def := powerlineConfig{
		Preset: "default", DisabledSegments: []string{}, InvalidDisabledSegments: []string{}, InvalidLayoutSegments: []string{},
		Placement: "above", Welcome: true, CompactPromptMode: "queue",
	}
	if p := normalizePreset(value, presetNames); p != "" {
		def.Preset = p
		return def
	}
	o := asObject(value)
	if o == nil {
		return def
	}
	items := normalizeCustomItems(o.vals["customItems"])
	disabled, invalidDisabled := normalizeDisabledSegments(o.vals["disabledSegments"], items)
	layout, invalidLayout := normalizeLayout(o.vals["layout"], items)
	pv, hasPlacement := o.vals["placement"]
	placement, invalidPlacement := normalizePlacement(pv, hasPlacement)
	cfg := powerlineConfig{
		Preset: def.Preset, CustomItems: items, DisabledSegments: disabled, InvalidDisabledSegments: invalidDisabled,
		Layout: layout, InvalidLayoutSegments: invalidLayout, Separator: normalizeSeparator(o.vals["separator"]),
		SegmentOptions: normalizeSegmentOptions(o), Placement: placement, InvalidPlacement: invalidPlacement,
		Welcome: o.vals["welcome"] != false, CompactPromptMode: "queue",
	}
	if p := normalizePreset(o.vals["preset"], presetNames); p != "" {
		cfg.Preset = p
	}
	cfg.StashSharpSShortcut, _ = o.vals["stashSharpSShortcut"].(bool)
	if q := asObject(o.vals["queue"]); q != nil && q.vals["compactPromptMode"] == "native" {
		cfg.CompactPromptMode = "native"
	}
	if n, ok := o.vals["sendDelayMs"].(float64); ok && !math.IsInf(n, 0) && !math.IsNaN(n) && n > 0 {
		cfg.SendDelayMs = n
	}
	cfg.AutoFollowUp, _ = o.vals["autoFollowUp"].(bool)
	if w := asObject(o.vals["workingVibes"]); w != nil {
		cfg.WorkingVibesColor = trimmedString(w.vals["color"])
	}
	return cfg
}

// mergeSegmentsWithCustomItems builds the three rows from the preset, the user's explicit layout rows, custom items and the
// disabled list. A row the layout names replaces the preset's; otherwise preset segments the layout placed elsewhere drop out.
func mergeSegmentsWithCustomItems(p presetDef, items []customItem, layout *statusLayout, disabled []string) (left, right, secondary []string) {
	var l statusLayout
	if layout != nil {
		l = *layout
	}
	placed := map[string]bool{}
	for _, row := range [][]string{l.Left, l.Right, l.Secondary} {
		for _, id := range row {
			placed[id] = true
		}
	}
	off := map[string]bool{}
	for _, id := range disabled {
		off[id] = true
	}
	build := func(position string, configured []string, preset []string) []string {
		var segments []string
		if configured != nil {
			segments = slices.Clone(configured)
		} else {
			for _, id := range preset {
				if !placed[id] {
					segments = append(segments, id)
				}
			}
			for _, it := range items {
				id := "custom:" + it.ID
				if it.Position == position && !placed[id] {
					segments = append(segments, id)
				}
			}
		}
		out := []string{}
		for _, id := range segments {
			if !off[id] {
				out = append(out, id)
			}
		}
		return out
	}
	return build("left", l.Left, p.left), build("right", l.Right, p.right), build("secondary", l.Secondary, p.secondary)
}

func nextPowerlineSettingWithPreset(existing any, preset string) any {
	o := asObject(existing)
	if o == nil {
		return preset
	}
	next := o.clone()
	next.set("preset", preset)
	return next
}

func nextPowerlineSettingWithOptions(existing any, updates *jsObject, currentPreset string) any {
	o := asObject(existing)
	if o == nil {
		next := newObject()
		next.set("preset", currentPreset)
		for _, k := range updates.order() {
			next.set(k, updates.vals[k])
		}
		return next
	}
	next := o.clone()
	for _, k := range updates.order() {
		next.set(k, updates.vals[k])
	}
	return next
}

func collectHiddenExtensionStatusKeys(items []customItem) map[string]bool {
	hidden := map[string]bool{}
	for _, it := range items {
		if it.ExcludeFromStatuses {
			hidden[it.StatusKey] = true
		}
	}
	return hidden
}

func isNotificationExtensionStatus(value string) bool {
	return strings.HasPrefix(strings.TrimLeftFunc(value, isJSSpace), "[")
}

func getNotificationExtensionStatuses(statuses []statusEntry, hidden map[string]bool) []string {
	var out []string
	for _, e := range statuses {
		if hidden[e.Key] || e.Value == "" || !isNotificationExtensionStatus(e.Value) {
			continue
		}
		out = append(out, e.Value)
	}
	return out
}

var (
	trailingStatusAny  = regexp.MustCompile(`(\x1b\[[0-9;]*m|\s|·|[|])+$`)
	trailingStatusKeep = regexp.MustCompile(`(\s|·|[|])+$`)
)

// normalizeExtensionStatusValue trims baked-in trailing separators (and, unless preserveAnsi, trailing SGR codes). nil when nothing visible remains.
func normalizeExtensionStatusValue(value string, preserveAnsi bool) *string {
	if value == "" || visibleWidth(value) <= 0 {
		return nil
	}
	var stripped string
	if preserveAnsi {
		stripped = trailingStatusKeep.ReplaceAllString(value, "")
	} else {
		stripped = trailingStatusAny.ReplaceAllString(value, "")
	}
	if visibleWidth(stripped) > 0 {
		return &stripped
	}
	return nil
}

func normalizeCompactExtensionStatus(value string) *string {
	if isNotificationExtensionStatus(value) {
		return nil
	}
	return normalizeExtensionStatusValue(value, false)
}
