package powerline_footer

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"time"
)

// The extension core: state, the layout of the bar, the /powerline command and the settings it persists. It talks to Pi (or to
// a scripted host in the tests) only through hostAPI. upstream: index.ts (powerlineFooter and its helpers).

// hostAPI is what the core needs from its host.
type hostAPI interface {
	Cwd() string
	HasUI() bool
	Mode() string
	Model() *modelInfo
	ThinkingLevel() string
	SessionID() string
	SessionName() string
	UsingOAuth() bool
	AutoCompactEnabled() bool
	Session() branchProvider
	ContextUsage() map[string]any
	ExtensionStatuses() []statusEntry
	ProviderGitBranch() *string
	Theme() theme
	Notify(message, level string)
	SetStatus(key string, text *string)
	ClearWidget(key string)
	// SetFooterInstalled and SetWidgetInstalled add or remove the footer and a bar widget; Repaint sends their content again.
	SetFooterInstalled(on bool)
	SetWidgetInstalled(key, placement string, on bool)
	Repaint()
}

type installedUI struct {
	Footer  bool
	Widgets map[string]string // widget key -> placement
}

const (
	widgetTop    = "powerline-top"
	widgetStatus = "powerline-status"
	statusStash  = "stash"
)

// widgetKeys are the widgets the original clears before it installs its own, in its order. Some belong to features that are
// not ported (the bash transcript, the secondary row, pending send, the queue preview and the last prompt), but a host that
// still shows one left by another version must see them cleared.
var widgetKeys = []string{"powerline-top", "powerline-secondary", "powerline-bash-transcript", "powerline-status", "powerline-pending-send", "powerline-queue-preview", "powerline-last-prompt"}

type powerline struct {
	h       hostAPI
	enabled bool
	config  powerlineConfig

	sessionStart_    int64
	currentThinking  any // string, or nil when unset
	isStreaming      bool
	liveUsage        map[string]any
	compacting       bool
	customCompaction bool

	branchCache sessionBranchCache
	usageCache  coreContextUsageCache
	ui          installedUI
	base        *segmentContext
	left        []string
	right       []string
	secondary   []string

	// frame is what the footer renderer lays out. The SDK runs that renderer on its own goroutine (again after every width
	// change) while handlers rebuild the snapshot, so it reads only this frame, which refresh publishes whole.
	frame atomic.Pointer[footerFrame]
}

// footerFrame is a published snapshot: the render context with the statuses, the segment order and the separator style.
type footerFrame struct {
	ctx   segmentContext
	ids   []string
	style string
}

func newPowerline(h hostAPI) *powerline {
	p := &powerline{h: h, enabled: true, sessionStart_: clock(), ui: installedUI{Widgets: map[string]string{}}}
	cwd, _ := os.Getwd()
	p.config = parsePowerlineConfig(readSettings(cwd)["powerline"], presetNames)
	return p
}

// Settings: the global file under the agent dir, overridden key by key by <cwd>/.pi/settings.json. upstream: readSettings.

func settingsPath() string                  { return getAgentPath("settings.json") }
func projectSettingsPath(cwd string) string { return filepath.Join(cwd, ".pi", "settings.json") }

func readSettingsFile(path string) *jsObject {
	data, err := os.ReadFile(path)
	if err != nil {
		return newObject()
	}
	v, err := parseJSON(data)
	if o, ok := v.(*jsObject); ok && err == nil {
		return o
	}
	return newObject()
}

func mergeSettings(base, override *jsObject) *jsObject {
	merged := base.clone()
	for _, k := range override.order() {
		ov := override.vals[k]
		if bo, ok := merged.vals[k].(*jsObject); ok {
			if oo, ok := ov.(*jsObject); ok {
				merged.set(k, mergeSettings(bo, oo))
				continue
			}
		}
		merged.set(k, ov)
	}
	return merged
}

// readSettings returns the merged settings as a plain map of the top-level keys (values keep their ordered objects).
func readSettings(cwd string) map[string]any {
	return mergeSettings(readSettingsFile(settingsPath()), readSettingsFile(projectSettingsPath(cwd))).vals
}

// readWritableSettingsFile: a missing file is empty; a file that is not a JSON object is never overwritten (nil).
func readWritableSettingsFile(path string) *jsObject {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return newObject()
		}
		return nil
	}
	v, err := parseJSON(data)
	if o, ok := v.(*jsObject); ok && err == nil {
		return o
	}
	return nil
}

// writePowerlineSetting updates the `powerline` key of the project settings when they already carry one, else of the global file.
func writePowerlineSetting(cwd string, update func(existing any) any) bool {
	global, project := settingsPath(), projectSettingsPath(cwd)
	gs, ps := readWritableSettingsFile(global), readWritableSettingsFile(project)
	if gs == nil || ps == nil {
		return false
	}
	path, settings := global, gs
	if _, has := ps.vals["powerline"]; has {
		path, settings = project, ps
	}
	settings.set("powerline", update(settings.vals["powerline"]))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false
	}
	return os.WriteFile(path, []byte(marshalJSON(settings, "  ")+"\n"), 0o644) == nil
}

func (p *powerline) preset() presetDef {
	if d, ok := presets[p.config.Preset]; ok {
		return d
	}
	return presets["default"]
}

// Session and event handling.

func (p *powerline) warnInvalidSegmentSettings() {
	plural := func(n int) string {
		if n == 1 {
			return ""
		}
		return "s"
	}
	quote := func(ids []string) string {
		q := make([]string, len(ids))
		for i, id := range ids {
			q[i] = marshalJSON(id, "")
		}
		return strings.Join(q, ", ")
	}
	if n := len(p.config.InvalidDisabledSegments); n > 0 {
		p.notify("Ignoring unknown powerline disabled segment" + plural(n) + ": " + quote(p.config.InvalidDisabledSegments))
	}
	if n := len(p.config.InvalidLayoutSegments); n > 0 {
		p.notify("Ignoring unknown powerline layout segment" + plural(n) + ": " + quote(p.config.InvalidLayoutSegments))
	}
	if p.config.InvalidPlacement != nil {
		p.notify("Ignoring invalid powerline placement: " + marshalJSON(*p.config.InvalidPlacement, ""))
	}
}

func (p *powerline) notify(message string) {
	if p.h.HasUI() {
		p.h.Notify(message, "warning")
	}
}

func (p *powerline) sessionStart(reason string) {
	p.sessionStart_ = clock()
	invalidateGitStatus()
	invalidateGitBranch()
	p.branchCache.reset()
	p.usageCache.reset()
	p.customCompaction = detectCustomCompactionEnabled(p.h.Cwd())
	p.isStreaming, p.liveUsage, p.compacting = false, nil, false
	p.config = parsePowerlineConfig(readSettings(p.h.Cwd())["powerline"], presetNames)
	p.warnInvalidSegmentSettings()
	p.currentThinking = p.h.ThinkingLevel()
	if p.currentThinking == "" {
		p.currentThinking = "off"
	}
	if p.h.HasUI() {
		p.h.SetStatus(statusStash, nil)
	}
	if p.enabled && p.h.HasUI() {
		p.setup()
	}
}

func (p *powerline) event(name string, data map[string]any) {
	switch name {
	case "model_select":
		p.usageCache.reset()
	case "thinking_level_select":
		level, _ := data["level"].(string)
		if data["level"] != nil {
			p.currentThinking = level
		} else if cur := p.h.ThinkingLevel(); cur != "" {
			p.currentThinking = cur
		} else {
			p.currentThinking = nil
		}
	case "session_tree":
		p.currentThinking, p.liveUsage = nil, nil
	case "tool_result":
		toolName, _ := data["toolName"].(string)
		if toolName == "write" || toolName == "edit" {
			invalidateGitStatus()
		}
		if toolName == "bash" {
			if input, ok := data["input"].(map[string]any); ok {
				if cmd, ok := input["command"].(string); ok && mightChangeGitBranch(cmd) {
					invalidateGitStatus()
					invalidateGitBranch()
				}
			}
		}
	case "user_bash":
		if cmd, _ := data["command"].(string); mightChangeGitBranch(cmd) {
			invalidateGitStatus()
			invalidateGitBranch()
		}
	case "agent_start":
		p.isStreaming, p.liveUsage = true, nil
	case "message_update":
		if u, ok := assistantUsage(data["message"]); ok {
			p.liveUsage = u
		}
	case "message_end":
		p.usageCache.reset()
		if msg := asMap(data["message"]); msg != nil {
			if u, ok := sessionAssistantUsage(msg); ok {
				if sr := msg["stopReason"]; sr == "error" || sr == "aborted" {
					p.liveUsage = nil
				} else if usageTokenTotal(u) > 0 {
					p.liveUsage = u
				}
			}
		}
	case "turn_end":
		p.usageCache.reset()
	case "session_before_compact":
		p.compacting, p.isStreaming, p.liveUsage = true, false, nil
		p.usageCache.reset()
	case "session_compact":
		p.compacting, p.isStreaming, p.liveUsage = false, false, nil
		p.usageCache.reset()
	case "agent_end":
		p.isStreaming, p.liveUsage = false, nil
		p.usageCache.reset()
	}
	if p.enabled && p.h.HasUI() {
		p.base = nil // taken afresh by Repaint
		p.h.Repaint()
	}
}

// assistantUsage returns the usage of an assistant message that has tokens and did not fail.
func assistantUsage(message any) (map[string]any, bool) {
	msg := asMap(message)
	u, ok := sessionAssistantUsage(msg)
	if !ok || msg["stopReason"] == "error" || msg["stopReason"] == "aborted" || usageTokenTotal(u) <= 0 {
		return nil, false
	}
	return u, true
}

var gitBranchCommands = regexpMust(`\bgit\s+(checkout|switch|branch\s+-[dDmM]|merge|rebase|pull|reset|worktree)`, `\bgit\s+stash\s+(pop|apply)`)

func mightChangeGitBranch(cmd string) bool {
	for _, re := range gitBranchCommands {
		if re.MatchString(cmd) {
			return true
		}
	}
	return false
}

func readCompactionPolicyEnabled(path string) (enabled, found bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, false
	}
	v, err := parseJSON(data)
	o, ok := v.(*jsObject)
	if err != nil || !ok {
		return false, true
	}
	b, ok := o.vals["enabled"].(bool)
	return ok && b, true
}

func detectCustomCompactionEnabled(cwd string) bool {
	if _, err := os.Stat(getAgentPath("extensions", "pi-custom-compaction")); err != nil {
		return false
	}
	if enabled, found := readCompactionPolicyEnabled(filepath.Join(cwd, ".pi", "compaction-policy.json")); found {
		return enabled
	}
	enabled, _ := readCompactionPolicyEnabled(getAgentPath("compaction-policy.json"))
	return enabled
}

// Installing and removing the bar.

// setup is setupCustomEditor for what is ported: it clears the widgets, installs the footer and the widgets, and paints.
func (p *powerline) setup() {
	if !p.enabled {
		return
	}
	for _, key := range widgetKeys {
		p.h.ClearWidget(key)
	}
	p.ui = installedUI{Footer: true, Widgets: map[string]string{}}
	p.h.SetFooterInstalled(true)
	placement := "aboveEditor"
	if p.config.Placement == "below" {
		placement = "belowEditor"
	}
	p.ui.Widgets[widgetTop] = placement
	p.h.SetWidgetInstalled(widgetTop, placement, true)
	if p.h.Mode() == "tui" {
		p.ui.Widgets[widgetStatus] = "aboveEditor"
		p.h.SetWidgetInstalled(widgetStatus, "aboveEditor", true)
	}
	p.base = nil // taken afresh by Repaint
	p.h.Repaint()
}

func (p *powerline) teardown() {
	p.h.SetStatus(statusStash, nil)
	p.h.SetFooterInstalled(false)
	for _, key := range widgetKeys {
		p.h.ClearWidget(key)
	}
	for key := range p.ui.Widgets {
		p.h.SetWidgetInstalled(key, "", false)
	}
	p.ui = installedUI{Widgets: map[string]string{}}
	p.base = nil
	p.frame.Store(nil)
}

func (p *powerline) installed() installedUI { return p.ui }

// The /powerline command. upstream: index.ts registerCommand("powerline").

func (p *powerline) command(args string) {
	if jsTrim(args) == "" {
		p.enabled = !p.enabled
		if p.enabled {
			p.setup()
			p.h.Notify("Powerline enabled", "info")
		} else {
			p.teardown()
			p.h.Notify("Powerline disabled", "info")
		}
		return
	}
	normalized := strings.ToLower(jsTrim(args))
	if rest, ok := strings.CutPrefix(normalized, "placement"); ok && (rest == "" || (len(rest) > 1 && isJSSpace(rune(rest[0])))) {
		req := jsTrim(rest)
		if req == "" || req == "above" || req == "below" || req == "toggle" {
			switch {
			case req == "above" || req == "below":
				p.config.Placement = req
			case p.config.Placement == "above":
				p.config.Placement = "below"
			default:
				p.config.Placement = "above"
			}
			p.config.InvalidPlacement = nil
			if p.enabled && p.h.HasUI() {
				p.setup()
			}
			updates := newObject()
			updates.set("placement", p.config.Placement)
			if writePowerlineSetting(p.h.Cwd(), func(e any) any { return nextPowerlineSettingWithOptions(e, updates, p.config.Preset) }) {
				p.h.Notify("Powerline placement set to: "+p.config.Placement, "info")
			} else {
				p.h.Notify("Powerline placement set to: "+p.config.Placement+" (not persisted; check settings.json)", "warning")
			}
			return
		}
	}
	if preset := normalizeIndexPreset(args); preset != "" {
		p.config.Preset = preset
		if p.enabled {
			p.setup()
		}
		if writePowerlineSetting(p.h.Cwd(), func(e any) any { return nextPowerlineSettingWithPreset(e, preset) }) {
			p.h.Notify("Preset set to: "+preset, "info")
		} else {
			p.h.Notify("Preset set to: "+preset+" (not persisted; check settings.json)", "warning")
		}
		return
	}
	p.h.Notify("Available presets: "+strings.Join(presetNames, ", "), "info")
}

func normalizeIndexPreset(value string) string {
	n := strings.ToLower(jsTrim(value))
	if slices.Contains(presetNames, n) {
		return n
	}
	return ""
}

// The bar: a snapshot taken at events (the host is asked there, never during a render) and laid out per width.

type adaptedUsage struct{ h hostAPI }

func (a adaptedUsage) GetLeafID() (*string, error) {
	if s := a.h.Session(); s != nil {
		return s.GetLeafID()
	}
	return nil, nil
}
func (a adaptedUsage) ReadUsage() map[string]any { return a.h.ContextUsage() }

func (p *powerline) refresh() {
	d := p.preset()
	l, r, s := mergeSegmentsWithCustomItems(d, p.config.CustomItems, p.config.Layout, p.config.DisabledSegments)
	p.left, p.right, p.secondary = l, r, s
	all := append(append(slices.Clone(l), r...), s...)
	ctx := p.buildContext(d, all)
	p.base = &ctx
	if c, ok := p.renderContext(); ok {
		p.frame.Store(&footerFrame{ctx: c, ids: all, style: p.separatorStyle()})
	}
}

func (p *powerline) buildContext(d presetDef, all []string) segmentContext {
	colors := d.colors
	if colors == nil {
		colors = defaultColors
	}
	events := p.branchCache.get(p.h.Session())
	stats := computeSessionTokenStats(events)
	var latest map[string]any
	if p.isStreaming && p.liveUsage != nil {
		latest = p.liveUsage
	} else if stats.LastAssistant != nil {
		latest = asMap(stats.LastAssistant["usage"])
	}
	var core *contextUsage
	if !(p.isStreaming && p.liveUsage != nil) {
		core = p.usageCache.get(adaptedUsage{p.h})
	}
	fallbackTokens := 0.0
	if latest != nil {
		fallbackTokens = usageTokenTotal(latest)
	}
	model := p.h.Model()
	fallbackWindow := 0.0
	if model != nil {
		fallbackWindow = model.ContextWindow
	}
	display := resolveDisplayContextUsage(core, nil, fallbackTokens, fallbackWindow)
	opts := mergeSegmentOptions(d.options, p.config.SegmentOptions)

	var gitOpts gitOptions
	if opts.Git != nil {
		gitOpts = *opts.Git
	}
	showGit := slices.Contains(all, "git")
	if showGit {
		showGit = false
		for _, v := range []*bool{gitOpts.ShowBranch, gitOpts.ShowStaged, gitOpts.ShowUnstaged, gitOpts.ShowUntracked} {
			if v == nil || *v {
				showGit = true
			}
		}
	}
	var git gitStatus
	if showGit {
		polling := "full"
		if gitOpts.Polling != nil {
			polling = *gitOpts.Polling
		}
		git = getGitStatus(p.h.ProviderGitBranch(), polling, p.h.Cwd())
	}
	thinking := "off"
	switch {
	case p.currentThinking != nil && p.currentThinking != "":
		thinking, _ = p.currentThinking.(string)
	case stats.ThinkingLevelFromSession != nil:
		thinking = *stats.ThinkingLevelFromSession
	case p.h.ThinkingLevel() != "":
		thinking = p.h.ThinkingLevel()
	}
	c := segmentContext{
		Model: model, ThinkingLevel: thinking, SessionID: p.h.SessionID(), SessionName: p.h.SessionName(), CWD: p.h.Cwd(),
		Usage:         usageStats{stats.Input, stats.Output, stats.CacheRead, stats.CacheWrite, stats.Cost, stats.SubagentCost},
		ContextTokens: display.Tokens, ContextPercent: display.Percent, ContextWindow: display.Window,
		ContextApproximate: false,
		AutoCompactEnabled: p.h.AutoCompactEnabled(), CustomCompactionEnabled: p.customCompaction,
		UsingSubscription: model != nil && p.h.UsingOAuth(),
		Queue:             queueSummary{Compacting: p.compacting},
		SessionStart:      p.sessionStart_, Git: git,
		HiddenStatusKeys: collectHiddenExtensionStatusKeys(p.config.CustomItems), CustomItems: map[string]customItem{},
		Options: opts, Theme: p.h.Theme(), Colors: colors,
	}
	for _, it := range p.config.CustomItems {
		c.CustomItems[it.ID] = it
	}
	return c
}

// renderContext is the snapshot with the statuses and the time read now.
func (p *powerline) renderContext() (segmentContext, bool) {
	if !p.enabled {
		return segmentContext{}, false
	}
	if p.base == nil {
		p.refresh()
	}
	c := *p.base
	c.ExtensionStatuses = p.h.ExtensionStatuses()
	for _, e := range c.ExtensionStatuses {
		if e.Key == "compact-policy" {
			c.CustomCompactionEnabled = true
		}
	}
	return c, true
}

type renderedPart struct {
	content string
	width   int
}

func buildContentFromParts(parts []string, style string) string {
	if len(parts) == 0 {
		return ""
	}
	sep := getSeparator(style).Left
	return " " + strings.Join(parts, " "+separatorAnsi+sep+ansiReset+" ") + ansiReset + " "
}

func (p *powerline) separatorStyle() string {
	style := p.config.Separator
	if style == "" {
		style = p.preset().separator
	}
	return style
}

// computeResponsiveLayout fits the segments into the top bar and lets what does not fit overflow into the secondary row.
func (p *powerline) computeResponsiveLayout(ctx segmentContext, ids []string, available int) (top, secondary string) {
	return layoutSegments(ctx, ids, p.separatorStyle(), available)
}

func layoutSegments(ctx segmentContext, ids []string, style string, available int) (top, secondary string) {
	sepWidth := visibleWidth(getSeparator(style).Left) + 2
	var rendered []renderedPart
	for _, id := range ids {
		seg := renderSegment(id, ctx)
		if seg.Visible && seg.Content != "" {
			rendered = append(rendered, renderedPart{seg.Content, visibleWidth(seg.Content)})
		}
	}
	if len(rendered) == 0 {
		return "", ""
	}
	const overhead = 2
	cur := overhead
	var topSegs []string
	var overflow []renderedPart
	over := false
	for _, seg := range rendered {
		need := seg.width
		if len(topSegs) > 0 {
			need += sepWidth
		}
		if !over && cur+need <= available {
			topSegs = append(topSegs, seg.content)
			cur += need
		} else {
			over = true
			overflow = append(overflow, seg)
		}
	}
	secW := overhead
	var secSegs []string
	for _, seg := range overflow {
		need := seg.width
		if len(secSegs) > 0 {
			need += sepWidth
		}
		if secW+need <= available {
			secSegs = append(secSegs, seg.content)
			secW += need
		} else {
			break
		}
	}
	return buildContentFromParts(topSegs, style), buildContentFromParts(secSegs, style)
}

func (p *powerline) layout(width int) (top, secondary string) {
	ctx, ok := p.renderContext()
	if !ok {
		return "", ""
	}
	ids := append(append(slices.Clone(p.left), p.right...), p.secondary...)
	return p.computeResponsiveLayout(ctx, ids, width)
}

func (p *powerline) renderTop(width int) []string {
	if top, _ := p.layout(width); top != "" {
		return []string{top}
	}
	return []string{}
}

func (p *powerline) renderFooter(width int) []string {
	if _, sec := p.layout(width); sec != "" {
		return []string{sec}
	}
	return []string{}
}

// renderStatus lists the notification-style statuses (those starting with "[") above the editor.
func (p *powerline) renderStatus(width int) []string {
	statuses := p.h.ExtensionStatuses()
	if !p.enabled || len(statuses) == 0 {
		return []string{}
	}
	hidden := collectHiddenExtensionStatusKeys(p.config.CustomItems)
	notes := []string{}
	for _, v := range getNotificationExtensionStatuses(statuses, hidden) {
		line := " " + v
		if visibleWidth(line) <= width {
			notes = append(notes, line)
		}
	}
	return notes
}

// Injectable clock and host name (the tests freeze both).
var (
	clock      = func() int64 { return time.Now().UnixMilli() }
	hostnameFn = func() string { h, _ := os.Hostname(); return h }
)
