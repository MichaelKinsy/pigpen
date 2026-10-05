package pi_permission_system

import "strings"

// Port of src/policy/rule.ts, restrictiveness.ts, permission-merge.ts, scope-merge.ts and the rule parts of normalize.ts.

// Rule is the atomic unit of policy. Layer "" is no layer; Reason nil is no reason.
type Rule struct {
	Surface, Pattern, Action string
	Reason                   *string
	Layer                    string
	Origin                   string
}

// Ruleset is an ordered list of rules; later rules win.
type Ruleset []Rule

// PathFlavor is the part of the original's path flavor the rule engine reads: how path-surface patterns are folded.
type PathFlavor struct{ MatchOptions *MatchOptions }

var (
	PosixPathFlavor = PathFlavor{}
	Win32PathFlavor = PathFlavor{MatchOptions: &MatchOptions{CaseInsensitive: true, WindowsSeparators: true}}
)

var pathSurfaces = map[string]bool{
	"read": true, "write": true, "edit": true, "find": true, "grep": true, "ls": true,
	"path": true, "external_directory": true,
	"path_read": true, "path_write": true, "external_directory_read": true, "external_directory_write": true,
}

func pathMatchOptions(surface string, f PathFlavor) *MatchOptions {
	if pathSurfaces[surface] {
		return f.MatchOptions
	}
	return nil
}

func ruleMatches(r Rule, surface, value string, f PathFlavor) bool {
	return WildcardMatch(r.Surface, surface, nil) && WildcardMatch(r.Pattern, value, pathMatchOptions(surface, f))
}

func lastMatch(rules Ruleset, pred func(Rule) bool) (Rule, bool) {
	for i := len(rules) - 1; i >= 0; i-- {
		if pred(rules[i]) {
			return rules[i], true
		}
	}
	return Rule{}, false
}

// RewriteAsksToYolo turns every ask into an allow tagged "yolo"; deny and allow pass through.
func RewriteAsksToYolo(rules Ruleset) Ruleset {
	out := make(Ruleset, len(rules))
	for i, r := range rules {
		if r.Action == "ask" {
			r.Action, r.Origin = "allow", "yolo"
		}
		out[i] = r
	}
	return out
}

// FloorAllowsToAsk turns every allow into an ask tagged "fail-closed"; deny and ask pass through.
func FloorAllowsToAsk(rules Ruleset) Ruleset {
	out := make(Ruleset, len(rules))
	for i, r := range rules {
		if r.Action == "allow" {
			r.Action, r.Origin = "ask", "fail-closed"
		}
		out[i] = r
	}
	return out
}

// Evaluate returns the last rule whose surface and pattern match, or a synthetic builtin rule with defaultAction ("" is ask).
func Evaluate(surface, pattern string, rules Ruleset, f PathFlavor, defaultAction string) Rule {
	if r, ok := lastMatch(rules, func(r Rule) bool { return ruleMatches(r, surface, pattern, f) }); ok {
		return r
	}
	if defaultAction == "" {
		defaultAction = "ask"
	}
	return Rule{Surface: surface, Pattern: pattern, Action: defaultAction, Origin: "builtin"}
}

// IsSurfaceFullyDenied reports whether every value on the surface resolves to deny, probing the catch-all and each configured pattern.
func IsSurfaceFullyDenied(surface string, rules Ruleset, f PathFlavor) bool {
	probes := []string{"*"}
	seen := map[string]bool{"*": true}
	for _, r := range rules {
		if WildcardMatch(r.Surface, surface, nil) {
			p := expandHomePath(r.Pattern)
			if !seen[p] {
				seen[p] = true
				probes = append(probes, p)
			}
		}
	}
	for _, v := range probes {
		if Evaluate(surface, v, rules, f, "").Action != "deny" {
			return false
		}
	}
	return true
}

// Found is a rule and the value it was reported for.
type Found struct {
	Rule  Rule
	Value string
}

// EvaluateMostRestrictive returns the first deny (at once) or else the first ask over the values, or nil when all are allowed.
func EvaluateMostRestrictive(surface string, values []string, rules Ruleset, f PathFlavor) *Found {
	var worst *Found
	for _, v := range values {
		r := Evaluate(surface, v, rules, f, "")
		if r.Action == "deny" {
			return &Found{r, v}
		}
		if r.Action == "ask" && (worst == nil || worst.Rule.Action != "ask") {
			worst = &Found{r, v}
		}
	}
	return worst
}

// EvaluateAnyValue treats the values as alternative names for one access: the last rule matching any value wins and the reported
// value is the first one that rule matches.
func EvaluateAnyValue(surface string, values []string, rules Ruleset, f PathFlavor) Found {
	fallback := "*"
	if len(values) > 0 {
		fallback = values[0]
	}
	anyMatch := func(r Rule) bool {
		for _, v := range values {
			if ruleMatches(r, surface, v, f) {
				return true
			}
		}
		return false
	}
	if r, ok := lastMatch(rules, anyMatch); ok {
		for _, v := range values {
			if ruleMatches(r, surface, v, f) {
				return Found{r, v}
			}
		}
		return Found{r, fallback}
	}
	return Found{Evaluate(surface, fallback, rules, f, ""), fallback}
}

// CheckResult is the part of the original's PermissionCheckResult the restrictiveness helpers read.
type CheckResult struct {
	ToolName, State, MatchedPattern, Origin string
}

var restrictiveness = map[string]int{"allow": 0, "ask": 1, "deny": 2}

// MostRestrictiveOf returns the most restrictive member (deny over ask over allow); the first one on a tie.
func MostRestrictiveOf(results []*CheckResult) *CheckResult {
	worst := results[0]
	for _, r := range results {
		if restrictiveness[r.State] > restrictiveness[worst.State] {
			worst = r
		}
	}
	return worst
}

// PickMostRestrictive is MostRestrictiveOf for a list that may be empty (nil).
func PickMostRestrictive(results []*CheckResult) *CheckResult {
	if len(results) == 0 {
		return nil
	}
	return MostRestrictiveOf(results)
}

// ---- config merging ----

// MergeFlatPermissions overlays `override` on `base`: two pattern maps merge key by key, anything else is replaced.
func MergeFlatPermissions(base, override *jsObject) *jsObject {
	merged := base.clone()
	for _, k := range override.order() {
		v := override.vals[k]
		if _, u := v.(undef); u {
			merged.set(k, v)
			continue
		}
		bo, bok := merged.vals[k].(*jsObject)
		vo, vok := v.(*jsObject)
		if bok && vok {
			m := bo.clone()
			for _, pk := range vo.order() {
				m.set(pk, vo.vals[pk])
			}
			merged.set(k, m)
		} else {
			merged.set(k, v)
		}
	}
	return merged
}

// Scope is one config scope's permission map (nil when it has none).
type Scope struct {
	Name       string
	Permission *jsObject
}

// Merged is the merged permission map and which scope each surface pattern came from.
type Merged struct {
	Permission *jsObject
	Origins    map[string]map[string]string
}

// MergeScopesWithOrigins merges scopes from lowest to highest precedence and records the origin of every pattern.
func MergeScopesWithOrigins(scopes []Scope) Merged {
	origins := map[string]map[string]string{}
	merged := newObject()
	for _, sc := range scopes {
		if sc.Permission == nil {
			continue
		}
		permission := ExpandDirectionalSugar(sc.Permission)
		for _, surface := range permission.order() {
			value := permission.vals[surface]
			_, bothObjects := merged.vals[surface].(*jsObject)
			vo, valueObject := value.(*jsObject)
			if bothObjects && valueObject {
				if origins[surface] == nil {
					origins[surface] = map[string]string{}
				}
				for _, p := range vo.order() {
					origins[surface][p] = sc.Name
				}
			} else {
				so := map[string]string{}
				if _, isString := value.(string); isString {
					so["*"] = sc.Name
				} else if valueObject {
					for _, p := range vo.order() {
						so[p] = sc.Name
					}
				}
				origins[surface] = so
			}
		}
		merged = MergeFlatPermissions(merged, permission)
	}
	return Merged{merged, origins}
}

// ---- normalization ----

var directionalFamilies = map[string]bool{"path": true, "external_directory": true}

func toPatternMap(v any) *jsObject {
	if s, ok := v.(string); ok {
		o := newObject()
		o.set("*", s)
		return o
	}
	if o, ok := v.(*jsObject); ok {
		return o
	}
	return newObject()
}

func appendExplicitEntries(sugar any, explicit any, hasExplicit bool) any {
	if !hasExplicit {
		if s, ok := sugar.(string); ok {
			return s
		}
		if o, ok := sugar.(*jsObject); ok {
			return o.clone()
		}
		return sugar
	}
	ep := toPatternMap(explicit)
	out := newObject()
	sp := toPatternMap(sugar)
	for _, k := range sp.order() {
		if _, has := ep.vals[k]; !has {
			out.set(k, sp.vals[k])
		}
	}
	for _, k := range ep.order() {
		out.set(k, ep.vals[k])
	}
	return out
}

// ExpandDirectionalSugar replaces a bare `path` or `external_directory` key by its `_read` and `_write` members; explicit member
// entries come after the sugar-derived ones.
func ExpandDirectionalSugar(permission *jsObject) *jsObject {
	expanded := newObject()
	for _, surface := range permission.order() {
		value := permission.vals[surface]
		if _, u := value.(undef); u {
			continue
		}
		if !directionalFamilies[surface] {
			if _, has := expanded.vals[surface]; !has {
				expanded.set(surface, value)
			}
			continue
		}
		for _, member := range []string{surface + "_read", surface + "_write"} {
			explicit, has := permission.vals[member]
			if _, u := explicit.(undef); u {
				has = false
			}
			expanded.set(member, appendExplicitEntries(value, explicit, has))
		}
	}
	return expanded
}

func isPermissionState(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok && (s == "allow" || s == "deny" || s == "ask")
}

// denyWithReason reports a { action: "deny", reason? } object; reason must be a string when present.
func denyWithReason(v any) (reason *string, ok bool) {
	o, isObj := v.(*jsObject)
	if !isObj {
		return nil, false
	}
	if a, _ := o.vals["action"].(string); a != "deny" {
		return nil, false
	}
	r, present := o.vals["reason"]
	if !present {
		return nil, true
	}
	if _, u := r.(undef); u {
		return nil, true
	}
	s, isStr := r.(string)
	if !isStr {
		return nil, false
	}
	return &s, true
}

// NormalizeFlatConfig turns a permission map into rules, in the map's key order; entries that are not valid are ignored.
func NormalizeFlatConfig(permission *jsObject) Ruleset {
	var rules Ruleset
	for _, surface := range permission.order() {
		switch value := permission.vals[surface].(type) {
		case string:
			if a, ok := isPermissionState(value); ok {
				rules = append(rules, Rule{Surface: surface, Pattern: "*", Action: a, Origin: "builtin"})
			}
		case *jsObject:
			for _, pattern := range value.order() {
				action := value.vals[pattern]
				if reason, ok := denyWithReason(action); ok {
					rules = append(rules, Rule{Surface: surface, Pattern: pattern, Action: "deny", Reason: reason, Origin: "builtin"})
				} else if a, ok := isPermissionState(action); ok {
					rules = append(rules, Rule{Surface: surface, Pattern: pattern, Action: a, Origin: "builtin"})
				}
			}
		case []any:
			for i, action := range value {
				if a, ok := isPermissionState(action); ok {
					rules = append(rules, Rule{Surface: surface, Pattern: itoa(i), Action: a, Origin: "builtin"})
				}
			}
		}
	}
	return rules
}

func itoa(i int) string { return jsNumber(float64(i)) }

const piMcpToolPrefix = "mcp__"

// isPiMcpToolName reports a name shaped mcp__<server>__<tool> with both parts non-empty.
func isPiMcpToolName(name string) bool {
	if !strings.HasPrefix(name, piMcpToolPrefix) {
		return false
	}
	rest := name[len(piMcpToolPrefix):]
	sep := strings.Index(rest, "__")
	return sep > 0 && sep+2 < len(rest)
}

func canNamePiMcpTool(key string) bool {
	if isPiMcpToolName(key) {
		return true
	}
	return strings.HasPrefix(key, piMcpToolPrefix) && strings.ContainsAny(key, "*?")
}

// McpRelocation is the rules with the Pi MCP tool keys copied onto the mcp surface, and those keys.
type McpRelocation struct {
	Rules         Ruleset
	RelocatedKeys []string
}

// RelocateMcpToolKeyRules copies catch-all rules whose surface names a Pi MCP tool onto the mcp surface, after every other rule.
func RelocateMcpToolKeyRules(rules Ruleset) McpRelocation {
	var moved Ruleset
	var keys []string
	for _, r := range rules {
		if r.Pattern == "*" && canNamePiMcpTool(r.Surface) {
			c := r
			c.Surface, c.Pattern = "mcp", r.Surface
			moved = append(moved, c)
			keys = append(keys, c.Pattern)
		}
	}
	return McpRelocation{Rules: append(append(Ruleset{}, rules...), moved...), RelocatedKeys: keys}
}
