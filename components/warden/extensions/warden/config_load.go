package warden

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"
)

// The configuration loader (port of the loader half of src/config.ts, 571-1224): raw JSON values laid over a base
// Config. Nothing here fails on a malformed file: an unknown key is ignored and an invalid value falls back to the
// base, as upstream does. Raw values are what encoding/json produces for `any` (map[string]any, []any, float64,
// string, bool, nil); int and int64 are read as numbers too, so a caller may build a raw value by hand.
//
// The port's own keys (D3, D5) replace upstream's `typesafe` / `typesafeBackend`: `consent` names the backend the user
// agreed to send redacted summaries to, `backend` names where judgments go ("", "typesafe" or "own-model"). A project
// file never sets either.

type jobj = map[string]any

func asObj(v any) (jobj, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

// cfgJSNumber is `typeof value === "number"` for a decoded value.
func cfgJSNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, !math.IsNaN(n)
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

const maxSafeInteger = 1<<53 - 1

func isSafeInteger(f float64) bool {
	return !math.IsInf(f, 0) && f == math.Trunc(f) && math.Abs(f) <= maxSafeInteger
}

func configProbability(v any, fallback float64) float64 {
	if f, ok := cfgJSNumber(v); ok && !math.IsInf(f, 0) && f >= 0 && f <= 1 {
		return f
	}
	return fallback
}

func boolean(v any, fallback bool) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return fallback
}

func cfgPositiveInteger(v any, fallback int) int {
	if f, ok := cfgJSNumber(v); ok && isSafeInteger(f) && f > 0 {
		return int(f)
	}
	return fallback
}

// nonNegativeInteger is `Number.isSafeInteger(v) && v >= 0 ? v : fallback`.
func nonNegativeInteger(v any, fallback int) int {
	if f, ok := cfgJSNumber(v); ok && isSafeInteger(f) && f >= 0 {
		return int(f)
	}
	return fallback
}

// coalesce is JavaScript's `a ?? b` over decoded values (a missing key and null are both nil).
func coalesce(a, b any) any {
	if a != nil {
		return a
	}
	return b
}

// nonBlank is `typeof v === "string" && v.trim()`; it returns the string as written.
func nonBlank(v any) (string, bool) {
	s, ok := v.(string)
	if !ok || jsTrim(s) == "" {
		return "", false
	}
	return s, true
}

// cfgJSStringify is JSON.stringify for a decoded value (no HTML escaping; U+2028 and U+2029 stay raw).
func cfgJSStringify(v any) string {
	if v == nil {
		return "null"
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "undefined"
	}
	s := strings.TrimSuffix(buf.String(), "\n")
	s = strings.ReplaceAll(s, `\u2028`, "\u2028")
	return strings.ReplaceAll(s, `\u2029`, "\u2029")
}

func threshold(v any, fallback Threshold) Threshold {
	m, ok := asObj(v)
	if !ok {
		return fallback
	}
	warn := configProbability(m["warn"], fallback.Warn)
	confirm := configProbability(m["confirm"], fallback.Confirm)
	return Threshold{Warn: math.Min(warn, confirm), Confirm: confirm}
}

// offTaskThreshold: `confirm` is the pre-0.12 name of the upper threshold; files that still set it keep working.
func offTaskThreshold(v any, fallback OffTaskThreshold) OffTaskThreshold {
	m, ok := asObj(v)
	if !ok {
		return fallback
	}
	warn := configProbability(m["warn"], fallback.Warn)
	steer := configProbability(coalesce(m["steer"], m["confirm"]), fallback.Steer)
	return OffTaskThreshold{Warn: math.Min(warn, steer), Steer: steer}
}

// IsMode reports whether v is a warden mode.
func IsMode(v string) bool { return v == "steer" || v == "confirm" || v == "advise" }

// unknownRuleValue is the config warning for a rule value no kind accepts; the rule still applies, at the level named.
func unknownRuleValue(kind, id, key string, value any, valid []string, applied string) string {
	shown := utf16Slice(cfgJSStringify(value), 0, 40)
	return kind + ` "` + id + `": ` + key + " " + shown + " is not one of " + strings.Join(valid, ", ") + "; the rule applies at " + applied
}

// globList is a list of non-blank strings, trimmed; a non-list gives the fallback.
func globList(v any, fallback []string) []string {
	list, ok := v.([]any)
	if !ok {
		return fallback
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok && jsTrim(s) != "" {
			out = append(out, jsTrim(s))
		}
	}
	return out
}

// cfgStringList is `Array.isArray(v) ? v.filter(x => typeof x === "string") : fallback` (no trimming).
func cfgStringList(v any, fallback []string) []string {
	list, ok := v.([]any)
	if !ok {
		return fallback
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func parseCommandRule(raw any, defaultSeverity string, warnings *[]string) (CommandRule, bool) {
	m, ok := asObj(raw)
	if !ok {
		return CommandRule{}, false
	}
	id := ""
	if s, ok := nonBlank(m["id"]); ok {
		id = jsTrim(s)
	}
	pattern, _ := nonBlank(m["pattern"])
	if id == "" || pattern == "" {
		return CommandRule{}, false
	}
	// `block` is the path and arming rule word for deny. An unknown value holds (never weaker than the list's own
	// default): a typo in a deny rule must not let the command through, nor one in a warn rule leave it unheld.
	var severity string
	switch sev := m["severity"]; {
	case sev == nil:
		severity = defaultSeverity
	case sev == "warn" || sev == "confirm" || sev == "deny":
		severity = sev.(string)
	case sev == "block":
		severity = "deny"
	default:
		if defaultSeverity == "deny" {
			severity = "deny"
		} else {
			severity = "confirm"
		}
		*warnings = append(*warnings, unknownRuleValue("command rule", id, "severity", sev, []string{"warn", "confirm", "deny", "block"}, severity))
	}
	// Dialog is the default for a confirm rule; hold restores steer semantics. A deny or warn rule carries no action.
	rule := CommandRule{ID: id, Pattern: pattern, Severity: severity}
	if severity == "confirm" {
		rule.Action = "dialog"
		if a := m["action"]; a == "dialog" || a == "hold" {
			rule.Action = a.(string)
		}
	}
	if s, ok := nonBlank(m["message"]); ok {
		rule.Message = jsTrim(s)
	}
	rule.CaseSensitive = boolean(m["caseSensitive"], false)
	return rule, true
}

func parseCommandRules(raw any, defaultSeverity string, warnings *[]string) []CommandRule {
	list, ok := raw.([]any)
	if !ok {
		return []CommandRule{}
	}
	seen := map[string]bool{}
	rules := []CommandRule{}
	for _, item := range list {
		rule, ok := parseCommandRule(item, defaultSeverity, warnings)
		if !ok || seen[rule.ID] {
			continue
		}
		seen[rule.ID] = true
		rules = append(rules, rule)
	}
	return rules
}

func parseExemptRules(raw any) []string {
	list, ok := raw.([]any)
	if !ok {
		return []string{}
	}
	seen := map[string]bool{}
	out := []string{}
	for _, item := range list {
		if s, ok := item.(string); ok && jsTrim(s) != "" && !seen[jsTrim(s)] {
			seen[jsTrim(s)] = true
			out = append(out, jsTrim(s))
		}
	}
	return out
}

var pathToolNames = map[string]bool{"read": true, "write": true, "edit": true, "bash": true, "powershell": true, "ctx_execute": true, "ctx_batch_execute": true, "ctx_execute_file": true}

func parsePathRule(raw any, warnings *[]string) (PathRule, bool) {
	m, ok := asObj(raw)
	if !ok {
		return PathRule{}, false
	}
	id := ""
	if s, ok := nonBlank(m["id"]); ok {
		id = jsTrim(s)
	}
	paths := globList(m["paths"], nil)
	if id == "" || len(paths) == 0 {
		return PathRule{}, false
	}
	// "*" widens the rule to the bash surface; other entries name the structured file-tool surfaces.
	tools := []string{"*"}
	if list, ok := m["tools"].([]any); ok {
		requested := []string{}
		for _, t := range list {
			if s, ok := t.(string); ok && jsTrim(s) != "" {
				requested = append(requested, jsTrim(s))
			}
		}
		if len(requested) > 0 {
			tools = requested
		}
	}
	access := "none"
	if a := m["access"]; a == "none" || a == "read" || a == "write" {
		access = a.(string)
	}
	var action string
	switch a := m["action"]; {
	case a == nil:
		action = "note"
	case a == "note" || a == "warn" || a == "confirm" || a == "block":
		action = a.(string)
	case a == "deny":
		action = "block"
	default:
		action = "confirm"
		*warnings = append(*warnings, unknownRuleValue("path rule", id, "action", a, []string{"note", "warn", "confirm", "block", "deny"}, action))
	}
	rule := PathRule{ID: id, Paths: paths, Regex: m["regex"] == true, Access: access, Tools: tools, Action: action}
	if s, ok := nonBlank(m["message"]); ok {
		rule.Message = jsTrim(s)
	}
	if m["onlyIfExists"] == false {
		no := false
		rule.OnlyIfExists = &no
	}
	return rule, true
}

func parsePathRules(raw any, warnings *[]string) []PathRule {
	list, ok := raw.([]any)
	if !ok {
		return []PathRule{}
	}
	seen := map[string]bool{}
	rules := []PathRule{}
	for _, item := range list {
		rule, ok := parsePathRule(item, warnings)
		if !ok || seen[rule.ID] {
			continue
		}
		// A tool name that is neither a structured surface nor "*" is inert on every surface; keep it only when it means something.
		meaningful := false
		for _, t := range rule.Tools {
			if t == "*" || pathToolNames[t] {
				meaningful = true
			}
		}
		if !meaningful {
			continue
		}
		seen[rule.ID] = true
		rules = append(rules, rule)
	}
	return rules
}

// DefaultArmingDuration is ten minutes in milliseconds.
const DefaultArmingDuration = 600_000

var reArmDuration = lazyRE(`^(\d+(?:\.\d+)?)\s*(ms|s|m|h)?$`)

// ParseDuration reads an arming duration in milliseconds: a number is milliseconds; a string takes ms, s, m or h, and a
// string without a unit means minutes. Zero, negative and unreadable values give the fallback. The one parser for arms.for.
func ParseDuration(raw any, fallback float64) float64 {
	if f, ok := cfgJSNumber(raw); ok {
		if !math.IsInf(f, 0) && f > 0 {
			return f
		}
		return fallback
	}
	s, ok := raw.(string)
	if !ok {
		return fallback
	}
	m := reArmDuration.FindStringSubmatch(jsTrim(s))
	if m == nil {
		return fallback
	}
	value, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return fallback
	}
	factor := 60_000.0
	switch m[2] {
	case "ms":
		factor = 1
	case "s":
		factor = 1000
	case "h":
		factor = 3_600_000
	}
	if ms := value * factor; ms > 0 {
		return ms
	}
	return fallback
}

var reBareNumber = lazyRE(`^\d+(?:\.\d+)?$`)

func formatNumber(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

func parseArmingRule(raw any, warnings *[]string) (ArmingRule, bool) {
	m, ok := asObj(raw)
	if !ok {
		return ArmingRule{}, false
	}
	id := ""
	if s, ok := nonBlank(m["id"]); ok {
		id = jsTrim(s)
	}
	when, _ := asObj(m["when"])
	edited := globList(when["edited"], nil)
	if id == "" || len(edited) == 0 {
		return ArmingRule{}, false
	}
	arms, _ := asObj(m["arms"])
	command, ok := nonBlank(arms["command"])
	if !ok {
		return ArmingRule{}, false
	}
	if m["action"] == nil {
		*warnings = append(*warnings, `arming rule "`+id+`": has no action and is ignored; set action to confirm, hold, or block`)
		return ArmingRule{}, false
	}
	var action string
	switch a := m["action"]; {
	case a == "confirm" || a == "hold" || a == "block":
		action = a.(string)
	case a == "deny":
		action = "block"
	default:
		action = "confirm"
		*warnings = append(*warnings, unknownRuleValue("arming rule", id, "action", a, []string{"confirm", "hold", "block", "deny"}, action))
	}
	toolList, toolsGiven := when["tools"].([]any)
	toolsProvided := toolsGiven && len(toolList) > 0
	tools := []string{"write", "edit"}
	if toolsProvided {
		tools = []string{}
		for _, t := range toolList {
			if s, ok := t.(string); ok && (s == "write" || s == "edit") {
				tools = append(tools, s)
			}
		}
		// A when.tools that filters to nothing (e.g. ["bash"]) is inert: no tool would ever arm it.
		if len(tools) == 0 {
			return ArmingRule{}, false
		}
	}
	forMs := ParseDuration(arms["for"], DefaultArmingDuration)
	// A bare number reads as milliseconds and a bare string as minutes; both surprise someone, so both are named.
	if s, ok := arms["for"].(string); ok && reBareNumber.MatchString(jsTrim(s)) {
		*warnings = append(*warnings, `arming rule "`+id+`": arms.for "`+jsTrim(s)+`" has no unit and is read as minutes; write it with ms, s, m, or h`)
	}
	if forMs < 1000 {
		*warnings = append(*warnings, `arming rule "`+id+`": arms for `+formatNumber(forMs)+` ms, under one second; a number in arms.for is milliseconds, "10m" is ten minutes`)
	}
	rule := ArmingRule{ID: id, Action: action}
	rule.When = ArmingWhen{Edited: edited, Regex: when["regex"] == true}
	if toolsProvided {
		rule.When.Tools = tools
	}
	rule.Arms = ArmingArms{Command: command, For: forMs, CaseSensitive: boolean(arms["caseSensitive"], false)}
	if s, ok := nonBlank(m["message"]); ok {
		rule.Message = jsTrim(s)
	}
	return rule, true
}

func parseArmingRules(raw any, warnings *[]string) []ArmingRule {
	list, ok := raw.([]any)
	if !ok {
		return []ArmingRule{}
	}
	seen := map[string]bool{}
	rules := []ArmingRule{}
	for _, item := range list {
		rule, ok := parseArmingRule(item, warnings)
		if !ok || seen[rule.ID] {
			continue
		}
		seen[rule.ID] = true
		rules = append(rules, rule)
	}
	return rules
}

// projectIgnored is the config warning for a project value that would weaken the action guard or security.
func projectIgnored(key string, value, kept any) string {
	return "project file: " + key + " " + cfgJSStringify(value) + " is ignored; a project file may make the action guard and security stricter, not weaker, so the user value " + cfgJSStringify(kept) + " applies"
}

// projectSwitch: a project switch may turn a guard on, never off.
func projectSwitch(key string, v any, base bool, warnings *[]string) bool {
	if base && v == false {
		*warnings = append(*warnings, projectIgnored(key, false, true))
		return true
	}
	return boolean(v, base)
}

// projectProbability: a project threshold may be lowered (stricter), never raised above the user's.
func projectProbability(key string, v any, base float64, warnings *[]string) float64 {
	parsed := configProbability(v, base)
	if parsed <= base {
		return parsed
	}
	*warnings = append(*warnings, projectIgnored(key, parsed, base))
	return base
}

func applyAction(base ActionConfig, rawAny any, timeoutMs int, project bool, warnings *[]string) ActionConfig {
	out := base
	out.TimeoutMs = timeoutMs
	raw, ok := asObj(rawAny)
	if !ok {
		return out
	}
	listed := base.Tools
	if list, ok := raw["tools"].([]any); ok {
		listed = []string{}
		for _, t := range list {
			if s, ok := t.(string); ok && jsTrim(s) != "" {
				listed = append(listed, s)
			}
		}
	}
	// A project adds guarded tools; one it leaves out stays guarded.
	tools := listed
	if project {
		var dropped []string
		for _, t := range base.Tools {
			if !cfgContainsString(listed, t) {
				dropped = append(dropped, t)
			}
		}
		if len(dropped) > 0 {
			*warnings = append(*warnings, "project file: action.tools leaves out "+strings.Join(dropped, ", ")+"; a project file may add tools but not remove them, so they stay guarded")
		}
		tools = append([]string{}, base.Tools...)
		for _, t := range listed {
			if !cfgContainsString(base.Tools, t) {
				tools = append(tools, t)
			}
		}
	}
	irreversible := threshold(raw["irreversible"], base.Irreversible)
	if irr, ok := asObj(raw["irreversible"]); ok && project {
		warn := projectProbability("action.irreversible.warn", irr["warn"], base.Irreversible.Warn, warnings)
		confirm := projectProbability("action.irreversible.confirm", irr["confirm"], base.Irreversible.Confirm, warnings)
		irreversible = Threshold{Warn: math.Min(warn, confirm), Confirm: confirm}
	}
	failOpen := boolean(raw["failOpen"], base.FailOpen)
	if project && failOpen && !base.FailOpen {
		*warnings = append(*warnings, projectIgnored("action.failOpen", true, false))
		failOpen = false
	}
	shouldProceed, _ := asObj(raw["shouldProceed"])
	enabled := boolean(raw["enabled"], base.Enabled)
	if project {
		enabled = projectSwitch("action.enabled", raw["enabled"], base.Enabled, warnings)
	}
	out.Enabled, out.Tools, out.FailOpen, out.Irreversible = enabled, tools, failOpen, irreversible
	out.OffTask = offTaskThreshold(raw["offTask"], base.OffTask)
	out.IntentMismatch = configProbability(raw["intentMismatch"], base.IntentMismatch)
	out.VisibleMismatch = math.Min(configProbability(raw["visibleMismatch"], base.VisibleMismatch), configProbability(raw["intentMismatch"], base.IntentMismatch))
	if v := raw["intentTraceOnly"]; v == "invisible" || v == "all" || v == "none" {
		out.IntentTraceOnly = v.(string)
	}
	out.ShouldProceed = ShouldProceed{
		// `hold` is the pre-1.0 name of `threshold`; files that still set it keep working through 1.x.
		Threshold: configProbability(coalesce(shouldProceed["threshold"], shouldProceed["hold"]), base.ShouldProceed.Threshold),
		Steer:     boolean(shouldProceed["steer"], base.ShouldProceed.Steer),
	}
	out.FeedbackLog = boolean(raw["feedbackLog"], base.FeedbackLog)
	// Only the user file declares these; a project file cannot add, edit, or remove them. The base (already the user's
	// rules when a project file layers on top) is carried through, so a project "action" block cannot wipe them.
	if !project {
		out.CommandRules = parseCommandRules(raw["commandRules"], "warn", warnings)
		out.CommandDenyRules = parseCommandRules(raw["commandDenyRules"], "deny", warnings)
		out.ExemptRules = parseExemptRules(raw["exemptRules"])
		out.PathRules = parsePathRules(raw["pathRules"], warnings)
		out.ArmingRules = parseArmingRules(raw["armingRules"], warnings)
		if f := raw["floor"]; f == "level" || f == "evidence" {
			out.Floor = f.(string)
		}
	}
	out.EscalationThreshold = configProbability(raw["escalationThreshold"], base.EscalationThreshold)
	return out
}

func cfgContainsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func applyStuck(base StuckConfig, rawAny any) StuckConfig {
	raw, ok := asObj(rawAny)
	if !ok {
		return base
	}
	window := cfgPositiveInteger(raw["window"], base.Window)
	return StuckConfig{
		Enabled:        boolean(raw["enabled"], base.Enabled),
		Window:         window,
		MinFailures:    min(window, cfgPositiveInteger(raw["minFailures"], base.MinFailures)),
		Cooldown:       cfgPositiveInteger(raw["cooldown"], base.Cooldown),
		SameStrategy:   configProbability(raw["sameStrategy"], base.SameStrategy),
		ChurnThreshold: min(window, cfgPositiveInteger(raw["churnThreshold"], base.ChurnThreshold)),
		Nudge:          boolean(raw["nudge"], base.Nudge),
		RepeatSteer:    boolean(raw["repeatSteer"], base.RepeatSteer),
		Evidence:       boolean(raw["evidence"], base.Evidence),
		DiffLimit:      cfgPositiveInteger(raw["diffLimit"], base.DiffLimit),
		TailLimit:      cfgPositiveInteger(raw["tailLimit"], base.TailLimit),
	}
}

func applyRunaway(base RunawayConfig, rawAny any) RunawayConfig {
	raw, ok := asObj(rawAny)
	if !ok {
		return base
	}
	return RunawayConfig{
		Enabled: boolean(raw["enabled"], base.Enabled),
		// One occurrence is not a repeat, so the floor is 2.
		Repeats:         max(2, cfgPositiveInteger(raw["repeats"], base.Repeats)),
		ThinkingRepeats: max(2, cfgPositiveInteger(raw["thinkingRepeats"], base.ThinkingRepeats)),
		MinChars:        cfgPositiveInteger(raw["minChars"], base.MinChars),
		Recover:         boolean(raw["recover"], base.Recover),
	}
}

func applyNotify(base NotifyConfig, rawAny any, allowCommand bool) NotifyConfig {
	raw, ok := asObj(rawAny)
	if !ok {
		return base
	}
	command := base.Command
	if list, ok := raw["command"].([]any); ok && allowCommand {
		args := make([]string, 0, len(list))
		valid := true
		for _, a := range list {
			s, isString := a.(string)
			if !isString {
				valid = false
				break
			}
			args = append(args, s)
		}
		if valid && (len(args) == 0 || jsTrim(args[0]) != "") {
			command = args
		}
	}
	return NotifyConfig{Enabled: boolean(raw["enabled"], base.Enabled), CooldownMs: nonNegativeInteger(raw["cooldownMs"], base.CooldownMs), Command: command}
}

// maxJudgeCooldownMs: a project config cannot switch the judge off for longer than this by setting a huge cooldown.
const maxJudgeCooldownMs = 10 * 60 * 1000

func applyJudge(base JudgeConfig, rawAny any) JudgeConfig {
	raw, ok := asObj(rawAny)
	if !ok {
		return base
	}
	cooldown := base.CooldownMs
	if f, ok := cfgJSNumber(raw["cooldownMs"]); ok && isSafeInteger(f) && f >= 0 {
		cooldown = int(math.Min(f, maxJudgeCooldownMs))
	}
	return JudgeConfig{CooldownMs: cooldown, FailuresBeforeCooldown: cfgPositiveInteger(raw["failuresBeforeCooldown"], base.FailuresBeforeCooldown)}
}

func applySubagent(base SubagentConfig, rawAny any) SubagentConfig {
	raw, ok := asObj(rawAny)
	if !ok {
		return base
	}
	return SubagentConfig{Enabled: boolean(raw["enabled"], base.Enabled), Wake: boolean(raw["wake"], base.Wake),
		Threshold: configProbability(raw["threshold"], base.Threshold), CooldownMs: nonNegativeInteger(raw["cooldownMs"], base.CooldownMs)}
}

func applyDone(base DoneConfig, rawAny any) DoneConfig {
	raw, ok := asObj(rawAny)
	if !ok {
		return base
	}
	visual, _ := asObj(raw["visualTools"])
	images := globList(visual["images"], base.VisualTools.Images)
	mapped := make([]string, len(images))
	for i, extension := range images {
		mapped[i] = strings.TrimPrefix(extension, ".")
	}
	return DoneConfig{
		Enabled:    boolean(raw["enabled"], base.Enabled),
		ClaimsDone: configProbability(raw["claimsDone"], base.ClaimsDone),
		Nudge:      boolean(raw["nudge"], base.Nudge),
		UIProof:    boolean(raw["uiProof"], base.UIProof),
		UIFiles:    globList(raw["uiFiles"], base.UIFiles),
		VisualTools: VisualTools{
			Commands:     globList(visual["commands"], base.VisualTools.Commands),
			CommandWords: globList(visual["commandWords"], base.VisualTools.CommandWords),
			Tools:        globList(visual["tools"], base.VisualTools.Tools),
			Images:       mapped,
		},
	}
}

func applyProse(base ProseConfig, rawAny any) ProseConfig {
	raw, ok := asObj(rawAny)
	if !ok {
		return base
	}
	audience := base.Audience
	if s, ok := nonBlank(raw["audience"]); ok {
		audience = jsTrim(s)
	}
	return ProseConfig{Enabled: boolean(raw["enabled"], base.Enabled), Audience: audience, Threshold: configProbability(raw["threshold"], base.Threshold),
		Trend: min(3, cfgPositiveInteger(raw["trend"], base.Trend)), MinChars: cfgPositiveInteger(raw["minChars"], base.MinChars)}
}

func applySlop(base SlopConfig, rawAny any) SlopConfig {
	raw, ok := asObj(rawAny)
	if !ok {
		return base
	}
	// 0.2.x used `placeholder` for the stub threshold; it still sets the shared threshold.
	return SlopConfig{Enabled: boolean(raw["enabled"], base.Enabled), Threshold: configProbability(coalesce(raw["threshold"], raw["placeholder"]), base.Threshold), Prose: applyProse(base.Prose, raw["prose"])}
}

func applyRules(base RulesConfig, rawAny any) RulesConfig {
	raw, ok := asObj(rawAny)
	if !ok {
		return base
	}
	notes := base.SensitivePaths
	if sp, ok := asObj(raw["sensitivePaths"]); ok {
		notes = map[string]string{}
		for glob, note := range sp {
			if s, ok := note.(string); ok && jsTrim(glob) != "" && jsTrim(s) != "" {
				notes[glob] = s
			}
		}
	}
	return RulesConfig{
		Enabled:        boolean(raw["enabled"], base.Enabled),
		Threshold:      configProbability(raw["threshold"], base.Threshold),
		SoftThreshold:  configProbability(raw["softThreshold"], base.SoftThreshold),
		Files:          globList(raw["files"], base.Files),
		Fallback:       boolean(raw["fallback"], base.Fallback),
		MaxChars:       max(500, cfgPositiveInteger(raw["maxChars"], base.MaxChars)),
		Exclude:        globList(raw["exclude"], base.Exclude),
		Skip:           globList(raw["skip"], base.Skip),
		SensitivePaths: notes,
	}
}

var rePanelPercent = lazyRE(`^[1-9]\d?%$`)

func applyWidget(base WidgetConfig, rawAny any) WidgetConfig {
	raw, ok := asObj(rawAny)
	if !ok {
		return base
	}
	template := func(v any, fallback string) string {
		if s, ok := nonBlank(v); ok {
			return s
		}
		return fallback
	}
	out := base
	out.Enabled = boolean(raw["enabled"], base.Enabled)
	if p := raw["placement"]; p == "belowEditor" || p == "aboveEditor" {
		out.Placement = p.(string)
	}
	if b := raw["barMode"]; b == "live" || b == "stack" {
		out.BarMode = b.(string)
	}
	if s, ok := raw["shortcut"].(string); ok {
		out.Shortcut = jsTrim(s)
	}
	if f, ok := cfgJSNumber(raw["panelWidth"]); ok && isSafeInteger(f) && f >= 20 {
		out.PanelWidth = f
	} else if s, ok := raw["panelWidth"].(string); ok && rePanelPercent.MatchString(jsTrim(s)) {
		out.PanelWidth = jsTrim(s)
	}
	out.Action = template(raw["action"], base.Action)
	out.Stuck = template(raw["stuck"], base.Stuck)
	out.Done = template(raw["done"], base.Done)
	out.Prose = template(raw["prose"], base.Prose)
	out.Security = template(raw["security"], base.Security)
	out.Context = template(raw["context"], base.Context)
	out.Runaway = template(raw["runaway"], base.Runaway)
	out.Rules = template(raw["rules"], base.Rules)
	out.Subagent = template(raw["subagent"], base.Subagent)
	return out
}

func applySecurity(base SecurityConfig, rawAny any, project bool, warnings *[]string) SecurityConfig {
	raw, ok := asObj(rawAny)
	if !ok {
		return base
	}
	out := SecurityConfig{Enabled: boolean(raw["enabled"], base.Enabled), Threshold: configProbability(raw["threshold"], base.Threshold), MaskOutput: base.MaskOutput}
	if project {
		out.Enabled = projectSwitch("security.enabled", raw["enabled"], base.Enabled, warnings)
		out.Threshold = projectProbability("security.threshold", raw["threshold"], base.Threshold, warnings)
	} else {
		// Only the user file can turn masking off: a repository must not unmask credentials in its own agent's output.
		out.MaskOutput = boolean(raw["maskOutput"], base.MaskOutput)
	}
	return out
}

func applyContext(base ContextConfig, rawAny any, project bool) ContextConfig {
	raw, ok := asObj(rawAny)
	if !ok {
		return base
	}
	out := ContextConfig{
		Enabled:           boolean(raw["enabled"], base.Enabled),
		TailMinChars:      cfgPositiveInteger(raw["tailMinChars"], base.TailMinChars),
		Confidence:        configProbability(raw["confidence"], base.Confidence),
		DuplicateMinChars: cfgPositiveInteger(raw["duplicateMinChars"], base.DuplicateMinChars),
		RecallTool:        base.RecallTool,
		FormatConfidence:  configProbability(raw["formatConfidence"], base.FormatConfidence),
		CompactAppendix:   boolean(raw["compactAppendix"], base.CompactAppendix),
		DedupeRuns:        boolean(raw["dedupeRuns"], base.DedupeRuns),
		DedupeMessages:    boolean(raw["dedupeMessages"], base.DedupeMessages),
		LargeOutput:       base.LargeOutput,
		Filter:            base.Filter,
	}
	if s, ok := raw["recallTool"].(string); ok && IsRecallTool(s) {
		out.RecallTool = RecallTool(s)
	}
	if lo, ok := asObj(raw["largeOutput"]); ok {
		out.LargeOutput = LargeOutputConfig{Enabled: boolean(lo["enabled"], base.LargeOutput.Enabled), Threshold: configProbability(lo["threshold"], base.LargeOutput.Threshold)}
	}
	if f, ok := asObj(raw["filter"]); ok {
		out.Filter = FilterConfig{
			// User file only: turning it on sends whole redacted outputs to the judge and spends requests.
			Enabled:      base.Filter.Enabled,
			ChunkChars:   cfgPositiveInteger(f["chunkChars"], base.Filter.ChunkChars),
			MinScore:     base.Filter.MinScore,
			MaxKeptChars: cfgPositiveInteger(f["maxKeptChars"], base.Filter.MaxKeptChars),
			TimeoutMs:    cfgPositiveInteger(f["timeoutMs"], base.Filter.TimeoutMs),
		}
		if !project {
			out.Filter.Enabled = boolean(f["enabled"], base.Filter.Enabled)
		}
		if score, ok := cfgJSNumber(f["minScore"]); ok && score >= 0 && score <= 3 {
			out.Filter.MinScore = score
		}
	}
	return out
}

func applyWaste(base WasteConfig, rawAny any) WasteConfig {
	raw, ok := asObj(rawAny)
	if !ok {
		return base
	}
	return WasteConfig{
		Enabled: boolean(raw["enabled"], base.Enabled), Tip: boolean(raw["tip"], base.Tip), Every: max(2, cfgPositiveInteger(raw["every"], base.Every)),
		Sleep: boolean(raw["sleep"], base.Sleep), Paging: boolean(raw["paging"], base.Paging), Search: boolean(raw["search"], base.Search), Recheck: boolean(raw["recheck"], base.Recheck),
	}
}

// maxCompactionTimeoutMs: the host awaits the compaction hook with no deadline of its own, so this one is bounded too.
const maxCompactionTimeoutMs = 120_000

func applyCompaction(base CompactionConfig, rawAny any, project bool) CompactionConfig {
	raw, ok := asObj(rawAny)
	if !ok {
		return base
	}
	enabled := base.Enabled
	// Turning it on sends the session to Jev and spends requests, so only the user decides; a project tunes the rest.
	if !project {
		enabled = boolean(raw["enabled"], base.Enabled)
	}
	return CompactionConfig{
		Enabled:          enabled,
		KeepThreshold:    configProbability(raw["keepThreshold"], base.KeepThreshold),
		MaxSummaryTokens: max(1000, cfgPositiveInteger(raw["maxSummaryTokens"], base.MaxSummaryTokens)),
		TimeoutMs:        min(maxCompactionTimeoutMs, cfgPositiveInteger(raw["timeoutMs"], base.TimeoutMs)),
		MaxRequests:      cfgPositiveInteger(raw["maxRequests"], base.MaxRequests),
		SkipProviders:    globList(raw["skipProviders"], base.SkipProviders),
	}
}

func applyPrefs(base PrefsConfig, rawAny any) PrefsConfig {
	raw, ok := asObj(rawAny)
	if !ok {
		return base
	}
	return PrefsConfig{Enabled: boolean(raw["enabled"], base.Enabled), Inject: boolean(raw["inject"], base.Inject)}
}

func applySteers(base SteersConfig, rawAny any) SteersConfig {
	raw, ok := asObj(rawAny)
	if !ok {
		return base
	}
	return SteersConfig{
		Adaptive: boolean(raw["adaptive"], base.Adaptive), MinSteers: cfgPositiveInteger(raw["minSteers"], base.MinSteers),
		MinFollowed: configProbability(raw["minFollowed"], base.MinFollowed), MaxDisputed: configProbability(raw["maxDisputed"], base.MaxDisputed),
		RecheckEvery: cfgPositiveInteger(raw["recheckEvery"], base.RecheckEvery), ProbeEvery: cfgPositiveInteger(raw["probeEvery"], base.ProbeEvery),
	}
}

func applyLearning(base LearningConfig, rawAny any) LearningConfig {
	raw, ok := asObj(rawAny)
	if !ok {
		return base
	}
	// 0 keeps every record: no pruning.
	return LearningConfig{PatternAnalysis: boolean(raw["patternAnalysis"], base.PatternAnalysis), RetentionDays: nonNegativeInteger(raw["retentionDays"], base.RetentionDays)}
}

// clampedInt is `Math.max(lo, Math.min(hi, typeof v === "number" ? v : fallback))`; a fraction is truncated (the
// Go fields are integers; no upstream consumer reads the fraction).
func clampedInt(v any, fallback, lo, hi int) int {
	f := float64(fallback)
	if n, ok := cfgJSNumber(v); ok {
		f = n
	}
	return int(math.Max(float64(lo), math.Min(float64(hi), f)))
}

func clampedProbability(v any, fallback float64) float64 {
	f := fallback
	if n, ok := cfgJSNumber(v); ok {
		f = n
	}
	return math.Max(0, math.Min(1, f))
}

func applyConscience(base ConscienceConfig, rawAny any) ConscienceConfig {
	raw, ok := asObj(rawAny)
	if !ok {
		return base
	}
	skills, _ := asObj(raw["skills"])
	tools, _ := asObj(raw["tools"])
	mode := base.Skills.Mode
	if m, ok := skills["mode"].(string); ok && (m == "off" || m == "recommend" || m == "load") {
		mode = m
	}
	return ConscienceConfig{
		Enabled:            boolean(raw["enabled"], base.Enabled),
		Skills:             ConscienceSkillConfig{Mode: mode, Exclude: cfgStringList(skills["exclude"], base.Skills.Exclude)},
		Tools:              ConscienceToolConfig{Enabled: boolean(tools["enabled"], base.Tools.Enabled), Exclude: cfgStringList(tools["exclude"], base.Tools.Exclude)},
		SkipTools:          cfgStringList(raw["skipTools"], base.SkipTools),
		TimeoutMs:          clampedInt(raw["timeoutMs"], base.TimeoutMs, 100, 10000),
		MaxAssessments:     clampedInt(raw["maxAssessments"], base.MaxAssessments, 1, 10),
		MaxNudges:          clampedInt(raw["maxNudges"], base.MaxNudges, 1, 5),
		MaxSkillBytes:      clampedInt(raw["maxSkillBytes"], base.MaxSkillBytes, 1024, 131072),
		MaxLoadedBytes:     clampedInt(raw["maxLoadedBytes"], base.MaxLoadedBytes, 1024, 262144),
		RecommendThreshold: clampedProbability(raw["recommendThreshold"], base.RecommendThreshold),
		AdvanceThreshold:   clampedProbability(raw["advanceThreshold"], base.AdvanceThreshold),
	}
}

// applyShared: shared request settings; `action.timeoutMs` / `action.maxRequests` from 0.1.x files are still honoured.
func applyShared(base Config, raw jobj) (timeoutMs, maxRequests int) {
	legacy, _ := asObj(raw["action"])
	return cfgPositiveInteger(coalesce(raw["timeoutMs"], legacy["timeoutMs"]), base.TimeoutMs),
		cfgPositiveInteger(coalesce(raw["maxRequests"], legacy["maxRequests"]), base.MaxRequests)
}

// applyGuards lays the guard sections over base in upstream's order (which is the order warnings come out in).
func applyGuards(out *Config, base Config, raw jobj, timeoutMs int, project bool, warnings *[]string) {
	out.Compaction = applyCompaction(base.Compaction, raw["compaction"], project)
	out.Waste = applyWaste(base.Waste, raw["waste"])
	out.Rules = applyRules(base.Rules, raw["rules"])
	out.Runaway = applyRunaway(base.Runaway, raw["runaway"])
	out.Subagent = applySubagent(base.Subagent, raw["subagent"])
	// A project file may switch notifications off or on, but never names a command to run.
	out.Notify = applyNotify(base.Notify, raw["notify"], !project)
	out.Judge = applyJudge(base.Judge, raw["judge"])
	out.Action = applyAction(base.Action, raw["action"], timeoutMs, project, warnings)
	out.Stuck = applyStuck(base.Stuck, raw["stuck"])
	out.Done = applyDone(base.Done, raw["done"])
	out.Slop = applySlop(base.Slop, raw["slop"])
	out.Security = applySecurity(base.Security, raw["security"], project, warnings)
	out.Context = applyContext(base.Context, raw["context"], project)
}

// BackendRefusalFor is the message for a configured backend the port does not have.
func backendRefusalFor(value any) string {
	return "Unknown judgment backend " + cfgJSStringify(value) + "; judgments stay off. Use \"typesafe\", \"own-model\" or \"\" (offline patterns only)."
}

// ApplyUserOverrides lays a user file's raw JSON over base. Unknown keys and invalid values fall back to the base;
// nothing fails on a malformed file.
func ApplyUserOverrides(base Config, rawAny any) Config {
	raw, ok := asObj(rawAny)
	if !ok {
		return base
	}
	var warnings []string
	timeoutMs, maxRequests := applyShared(base, raw)
	out := base
	applyGuards(&out, base, raw, timeoutMs, false, &warnings)
	out.Enabled = boolean(raw["enabled"], base.Enabled)
	// D3: the judgment backend is typesafe or the session's own model; anything else is refused, never mapped to a default.
	out.Backend, out.BackendRefusal = base.Backend, base.BackendRefusal
	if v := raw["backend"]; v != nil {
		if s, ok := v.(string); ok && (s == BackendNone || s == BackendTypeSafe || s == BackendOwnModel) {
			out.Backend, out.BackendRefusal = s, ""
		} else {
			out.Backend, out.BackendRefusal = BackendNone, backendRefusalFor(v)
		}
	}
	out.Consent = base.Consent
	if s, ok := raw["consent"].(string); ok && (s == BackendNone || s == BackendTypeSafe || s == BackendOwnModel) {
		out.Consent = s
	}
	if m, ok := raw["mode"].(string); ok && IsMode(m) {
		out.Mode = m
	}
	out.TimeoutMs, out.MaxRequests = timeoutMs, maxRequests
	out.Widget = applyWidget(base.Widget, raw["widget"])
	out.SteerVisible = boolean(raw["steerVisible"], base.SteerVisible)
	out.Notices = boolean(raw["notices"], base.Notices)
	out.SteerBudget = nonNegativeIntegerStrict(raw["steerBudget"], base.SteerBudget)
	out.Steers = applySteers(base.Steers, raw["steers"])
	out.Learning = applyLearning(base.Learning, raw["learning"])
	out.Conscience = applyConscience(base.Conscience, raw["conscience"])
	out.Prefs = applyPrefs(base.Prefs, raw["prefs"])
	out.Warnings = appendWarnings(base.Warnings, warnings)
	return out
}

// nonNegativeIntegerStrict is `Number.isInteger(v) && v >= 0` (steerBudget; any integer, not only a safe one).
func nonNegativeIntegerStrict(v any, fallback int) int {
	if f, ok := cfgJSNumber(v); ok && !math.IsInf(f, 0) && f == math.Trunc(f) && f >= 0 && f <= maxSafeInteger {
		return int(f)
	}
	return fallback
}

func appendWarnings(base, extra []string) []string {
	if len(base)+len(extra) == 0 {
		return base
	}
	out := make([]string, 0, len(base)+len(extra))
	return append(append(out, base...), extra...)
}

// ApplyProjectOverrides lays a trusted project file over base. A project file may tune the guards but cannot grant
// consent, change the backend or the mode, raise budgets, declare rules, or unmask output. The action guard and
// security only get stricter: a value that would weaken them is ignored with one config warning. It also cannot turn
// warden on: the port is off until the user runs /warden enable (D1), so a repository never starts it.
func ApplyProjectOverrides(base Config, rawAny any) Config {
	raw, ok := asObj(rawAny)
	if !ok {
		return base
	}
	var warnings []string
	out := base
	out.Enabled = projectEnabled(raw["enabled"], base.Enabled, &warnings)
	applyGuards(&out, base, raw, base.TimeoutMs, true, &warnings)
	out.Prefs = applyPrefs(base.Prefs, raw["prefs"])
	out.Warnings = appendWarnings(base.Warnings, warnings)
	return out
}

// projectEnabled: a project may switch warden off for itself; it never switches it on (D1).
func projectEnabled(v any, base bool, warnings *[]string) bool {
	if base {
		return projectSwitch("enabled", v, base, warnings)
	}
	if v == true {
		*warnings = append(*warnings, "project file: enabled true is ignored; warden stays off until the user runs /warden enable")
	}
	return false
}

// ---------------------------------------------------------------------------
// Dotted-path access for /warden config.

// spreadObject is JavaScript's `{ ...value }` for a value found on a config path: an object is copied, a string or a
// list spreads into index keys, anything else is empty.
func spreadObject(v any) map[string]any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = val
		}
		return out
	case string:
		out := map[string]any{}
		for i, unit := range utf16.Encode([]rune(t)) {
			out[strconv.Itoa(i)] = string(utf16.Decode([]uint16{unit}))
		}
		return out
	case []any:
		out := make(map[string]any, len(t))
		for i, val := range t {
			out[strconv.Itoa(i)] = val
		}
		return out
	}
	return map[string]any{}
}

// SetNestedValue returns a copy of obj with the dotted path set to value; objects along the path are copied.
func SetNestedValue(obj map[string]any, path string, value any) map[string]any {
	keys := strings.Split(path, ".")
	result := spreadObject(obj)
	current := result
	for _, key := range keys[:len(keys)-1] {
		next := spreadObject(current[key])
		current[key] = next
		current = next
	}
	current[keys[len(keys)-1]] = value
	return result
}

// GetNestedValue reads a dotted path; a missing step is nil.
func GetNestedValue(obj map[string]any, path string) any {
	var current any = obj
	for _, key := range strings.Split(path, ".") {
		m, ok := current.(map[string]any)
		if !ok || m == nil {
			return nil
		}
		current = m[key]
	}
	return current
}

var reConfigNumber = lazyRE(`^-?\d+(\.\d+)?$`)

// ParseConfigValue coerces a command-line string into a JSON primitive so `/warden config set` stays ergonomic.
func ParseConfigValue(value string) any {
	switch value {
	case "true":
		return true
	case "false":
		return false
	case "null":
		return nil
	}
	if reConfigNumber.MatchString(value) {
		if f, err := strconv.ParseFloat(value, 64); err == nil {
			return f
		}
	}
	return value
}
