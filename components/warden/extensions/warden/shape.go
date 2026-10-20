package warden

import (
	"reflect"
	"strconv"
)

// Port of the schema-migration half of src/shape.ts (completeConfig, shapeWarning). Upstream's guard exists for a
// partly updated Node module graph: the extension from one version, the config module from another, handing back a
// config without the sections the extension reads. In this port the loader always returns a complete Config, so the
// guard has one job left: a Config built some other way (decoded from JSON with sections deleted, or the zero value)
// reaches the guards with every missing section disabled and named, never as a zero value that reads as "on".
//
// What cannot be told apart in Go: upstream tests `typeof value === "number"`. A zero number is a legal setting for
// a few fields (action.shouldProceed.threshold, escalationThreshold, intentMismatch, softThreshold, offTask.steer,
// learning.retentionDays, learning.patternAnalysis false, prefs both false), so a zero there is kept as written and
// the sections learning and prefs are never reported missing; PORT.md lists this.

// ConfigSchema is the config layout this build carries; upstream's CONFIG_SCHEMA and EXPECTED_SCHEMA are both 11.
const ConfigSchema = 11

// ShapeWarning is the notice for sections a config lacked.
func ShapeWarning(missing []string, loadedSchema *int) string {
	loaded := "pre-3"
	if loadedSchema != nil {
		loaded = strconv.Itoa(*loadedSchema)
	}
	return "warden: config sections " + joinStrings(missing, ", ") + " are missing (config module schema " + loaded + ", extension expects " + strconv.Itoa(ConfigSchema) +
		"); those guards are off. This happens when pi-warden was updated while Pi was running: restart Pi (a /reload is not enough)."
}

func joinStrings(list []string, sep string) string {
	out := ""
	for i, s := range list {
		if i > 0 {
			out += sep
		}
		out += s
	}
	return out
}

func proseOff() ProseConfig {
	return ProseConfig{Enabled: false, Audience: "technical", Threshold: 1, Trend: 3, MinChars: 1}
}

func isUnset(v any) bool { return reflect.ValueOf(v).IsZero() }

// CompleteConfig returns loaded with every missing section replaced by its disabled fallback, and the names of the
// sections that were missing, in upstream's order. A complete config comes back unchanged with no names.
func CompleteConfig(loaded Config) (Config, []string) {
	c := loaded
	var missing []string
	miss := func(name string) { missing = append(missing, name) }
	defaults := DefaultConfig()

	if c.TimeoutMs == 0 {
		c.TimeoutMs = 5000
	}
	if c.MaxRequests == 0 {
		c.MaxRequests = 500
	}
	if !IsMode(c.Mode) {
		c.Mode = "steer"
	}
	if c.SteerBudget < 0 {
		c.SteerBudget = 3
	}
	if isUnset(c.Action) {
		miss("action")
		c.Action = ActionConfig{Tools: []string{}, FailOpen: true, TimeoutMs: 5000, Irreversible: Threshold{Warn: 1, Confirm: 1}, OffTask: OffTaskThreshold{Warn: 1, Steer: 1},
			IntentMismatch: 1, VisibleMismatch: 1, IntentTraceOnly: "all", ShouldProceed: ShouldProceed{Threshold: 0.6}, CommandRules: []CommandRule{},
			CommandDenyRules: []CommandRule{}, ExemptRules: []string{}, PathRules: []PathRule{}, ArmingRules: []ArmingRule{}, EscalationThreshold: 0.85, Floor: "evidence"}
	}
	if isUnset(c.Stuck) {
		miss("stuck")
		c.Stuck = StuckConfig{Window: 12, MinFailures: 3, Cooldown: 3, SameStrategy: 1, ChurnThreshold: 5, DiffLimit: 3000, TailLimit: 1000}
	}
	if isUnset(c.Done) {
		miss("done")
		c.Done = DoneConfig{ClaimsDone: 1, UIFiles: []string{}, VisualTools: VisualTools{Commands: []string{}, CommandWords: []string{}, Tools: []string{}, Images: []string{}}}
	}
	if isUnset(c.Slop) {
		miss("slop")
		c.Slop = SlopConfig{Threshold: 1, Prose: proseOff()}
	}
	if isUnset(c.Security) {
		miss("security")
		c.Security = SecurityConfig{Threshold: 1}
	}
	if isUnset(c.Rules) {
		miss("rules")
		c.Rules = RulesConfig{Threshold: 1, MaxChars: 500, Files: []string{}, Exclude: []string{}, Skip: []string{}, SensitivePaths: map[string]string{}}
	}
	if isUnset(c.Context) {
		miss("context")
		c.Context = ContextConfig{Confidence: 1, TailMinChars: 1, DuplicateMinChars: maxSafeInteger, RecallTool: "none", FormatConfidence: 1, CompactAppendix: true,
			LargeOutput: LargeOutputConfig{Threshold: 1}, Filter: FilterConfig{ChunkChars: 2000, MinScore: 1.5, MaxKeptChars: 6000, TimeoutMs: 4000}}
	}
	if isUnset(c.Runaway) {
		miss("runaway")
		c.Runaway = RunawayConfig{Repeats: maxSafeInteger, ThinkingRepeats: maxSafeInteger, MinChars: maxSafeInteger}
	}
	if c.Notify.Command == nil && !c.Notify.Enabled && c.Notify.CooldownMs == 0 {
		miss("notify")
		c.Notify = NotifyConfig{Command: []string{}}
	}
	// A stale config leaves the judge trusted: never pausing is today's behaviour, not a new failure mode.
	if isUnset(c.Judge) {
		miss("judge")
		c.Judge = JudgeConfig{FailuresBeforeCooldown: maxSafeInteger}
	}
	if isUnset(c.Subagent) {
		miss("subagent")
		c.Subagent = SubagentConfig{Threshold: 1}
	}
	if isUnset(c.Widget) {
		miss("widget")
		c.Widget = WidgetConfig{Placement: "aboveEditor", BarMode: "live", PanelWidth: "40%"}
	}
	// A missing section turns adaptation off: every steer is sent, as before the section existed.
	if isUnset(c.Steers) {
		miss("steers")
		c.Steers = SteersConfig{MinSteers: 30, MinFollowed: 0.2, MaxDisputed: 0.4, RecheckEvery: 30, ProbeEvery: 5}
	}
	if isUnset(c.Waste) {
		miss("waste")
		c.Waste = WasteConfig{Every: 20}
	}
	// learning and prefs: a zero section is a legal setting here (see the header), so neither is ever reported missing.
	if isUnset(c.Compaction) {
		miss("compaction")
		c.Compaction = CompactionConfig{KeepThreshold: 1, MaxSummaryTokens: 1000, TimeoutMs: 1, MaxRequests: 1, SkipProviders: []string{}}
	}
	if isUnset(c.Conscience) {
		miss("conscience")
		c.Conscience = defaultConscienceConfig()
		c.Conscience.Enabled = false
	}
	if c.Conscience.Skills.Mode == "" {
		c.Conscience.Skills = ConscienceSkillConfig{Mode: "recommend", Exclude: []string{}}
	}
	if c.Conscience.SkipTools == nil {
		c.Conscience.SkipTools = defaultConscienceConfig().SkipTools
	}
	// A section present but without its prose: the live crash shape. The prose is off and named.
	if isUnset(c.Slop.Prose) {
		miss("slop.prose")
		c.Slop.Prose = proseOff()
	}
	// 0.7 added fields inside the context section; a config that lacks them leaves them unset.
	if c.Context.DuplicateMinChars == 0 || c.Context.RecallTool == "" {
		miss("context.saver")
		c.Context.DuplicateMinChars, c.Context.RecallTool, c.Context.FormatConfidence = maxSafeInteger, "none", 1
	}
	if isUnset(c.Context.Filter) {
		c.Context.Filter = FilterConfig{ChunkChars: 2000, MinScore: 1.5, MaxKeptChars: 6000, TimeoutMs: 4000}
	}
	if c.Widget.PanelWidth == nil {
		c.Widget.PanelWidth = "40%"
	}
	if t := c.Action.IntentTraceOnly; t != "invisible" && t != "all" && t != "none" {
		c.Action.IntentTraceOnly = "all"
	}
	if f := c.Action.Floor; f != "level" && f != "evidence" {
		c.Action.Floor = "evidence"
	}
	if c.Action.CommandRules == nil {
		c.Action.CommandRules = []CommandRule{}
	}
	if c.Action.CommandDenyRules == nil {
		c.Action.CommandDenyRules = []CommandRule{}
	}
	if c.Action.ExemptRules == nil {
		c.Action.ExemptRules = []string{}
	}
	if c.Action.PathRules == nil {
		c.Action.PathRules = []PathRule{}
	}
	if c.Action.ArmingRules == nil {
		c.Action.ArmingRules = []ArmingRule{}
	}
	// The runaway, rules and subagent guards added their widget templates later than the other sections; an older
	// widget section renders the default line.
	if c.Widget.Runaway == "" {
		c.Widget.Runaway = defaults.Widget.Runaway
	}
	if c.Widget.Rules == "" {
		c.Widget.Rules = defaults.Widget.Rules
	}
	if c.Widget.Subagent == "" {
		c.Widget.Subagent = defaults.Widget.Subagent
	}
	return c, missing
}
