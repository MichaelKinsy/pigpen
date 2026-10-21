package pi_permission_system

import (
	"os"
	"sort"
	"strings"
	"testing"
)

// The differential: testdata/presentation-oracle.json holds what the original's presentation, tool-input and exposure modules
// answered for testdata/presentation-cases.json (recorded by port/record-presentation-probe.sh). Every answer must be byte for
// byte the port's. Both sides are compared as compact JSON; strings are well-formed (the probe applies toWellFormed to its output).

type pcSuite struct {
	t       *testing.T
	cases   *jsObject
	oracle  *jsObject
	failed  int
	checked int
}

func (s *pcSuite) list(root *jsObject, key string) []any {
	s.t.Helper()
	v, ok := root.vals[key].([]any)
	if !ok {
		s.t.Fatalf("no list %q", key)
	}
	return v
}

// ok wraps a result the way the probe's run() does.
func pcOK(v any) *jsObject {
	o := newObject()
	if v == nil {
		v = nil
	}
	o.set("ok", v)
	return o
}

// compare checks one answer against the oracle's, collecting at most a few failures per section.
func (s *pcSuite) compare(section string, i int, got any, want any, spec any) {
	s.t.Helper()
	s.checked++
	if o, ok := got.(*jsObject); ok && o.vals["ok"] == pcOKSame {
		return
	}
	// The recording host's home directory: a `~` location expands to it. Read the recorded answer on this host's home.
	if section == "skills visible" {
		if home, err := os.UserHomeDir(); err == nil {
			want = pcHome(want, "/home/kinsy", home)
		}
	}
	g, w := marshalJSON(got, ""), marshalJSON(want, "")
	if g != w {
		s.failed++
		if s.failed <= 80 {
			s.t.Errorf("%s #%d differs\n  input:  %s\n  port:   %s\n  oracle: %s", section, i, clip(marshalJSON(spec, ""), 300), clip(g, 300), clip(w, 300))
		}
	}
}

// pcHome replaces the recording host's home directory inside every string of an answer.
func pcHome(v any, from, to string) any {
	switch x := v.(type) {
	case string:
		return strings.ReplaceAll(x, from, to)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = pcHome(e, from, to)
		}
		return out
	case *jsObject:
		out := newObject()
		for _, k := range x.keys {
			out.set(k, pcHome(x.vals[k], from, to))
		}
		return out
	}
	return v
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// pcOKSame marks an answer the test takes from the oracle itself (see its use).
var pcOKSame any = "<same>"

func pcNum(v any) float64 { return lgWidth(v) }

func pcStr(v any) *string {
	if s, ok := v.(string); ok {
		return &s
	}
	return nil
}

func pcStrings(v any) []string {
	out := []string{}
	for _, e := range v.([]any) {
		out = append(out, e.(string))
	}
	return out
}

func pcPayload(t *testing.T, v any) PromptPayload {
	t.Helper()
	p := AsPromptPayload(v)
	if p == nil {
		t.Fatalf("not a payload: %s", marshalJSON(v, ""))
	}
	return *p
}

func pcView(v DialogView) *jsObject {
	o := newObject()
	lines := make([]any, len(v.Lines))
	for i, l := range v.Lines {
		lines[i] = l
	}
	o.set("lines", lines)
	o.set("elided", v.Elided)
	return o
}

func pcPaint(name string) HighlightPaint {
	switch name {
	case "brackets":
		return func(t string) string { return "[" + t + "]" }
	case "ansi":
		return func(t string) string { return "\x1b[33m" + t + "\x1b[39m" }
	}
	return nil
}

func pcPromptStrings(list []string) []any {
	out := make([]any, len(list))
	for i, s := range list {
		out[i] = s
	}
	return out
}

func pcCheckResult(o *jsObject) PermissionCheckResult {
	r := PermissionCheckResult{}
	r.ToolName, _ = o.str("toolName")
	r.State, _ = o.str("state")
	r.Source, _ = o.str("source")
	r.Origin, _ = o.str("origin")
	r.Command, r.Target = pcStr(o.vals["command"]), pcStr(o.vals["target"])
	r.MatchedPattern, r.ExecutedUnit = pcStr(o.vals["matchedPattern"]), pcStr(o.vals["executedUnit"])
	if c := pcStr(o.vals["commandContext"]); c != nil {
		r.CommandContext = BashCommandContext(*c)
	}
	return r
}

func pcLookup(kind any) ToolInputFormatterLookup {
	switch kind {
	case "builtin":
		registry := &ToolInputFormatterRegistry{}
		if err := RegisterBuiltinToolInputFormatters(registry); err != nil {
			panic(err)
		}
		return registry
	case "decline":
		return lookupFunc(func(string) ToolInputFormatter { return constFormatter("", false) })
	case "fixed":
		return lookupFunc(func(string) ToolInputFormatter { return constFormatter("fixed preview", true) })
	}
	return nil
}

func pcFormatter(limits any, lookup ToolInputFormatterLookup) *ToolPreviewFormatter {
	options := ResolveToolPreviewLimits()
	if l, ok := limits.(*jsObject); ok {
		options.ToolInputPreviewMaxLength, _ = l.num("toolInputPreviewMaxLength")
		options.ToolTextSummaryMaxLength, _ = l.num("toolTextSummaryMaxLength")
	}
	return NewToolPreviewFormatter(options, lookup)
}

func pcGrants(v any) []ApprovalGrant {
	out := []ApprovalGrant{}
	for _, e := range v.([]any) {
		g := e.(*jsObject)
		surface, _ := g.str("surface")
		pattern, _ := g.str("pattern")
		out = append(out, ApprovalGrant{Surface: surface, Pattern: pattern})
	}
	return out
}

func pcDisplay(o *jsObject, key string) DisplayString {
	v, present := o.vals[key]
	if !present {
		return DisplayString{}
	}
	if s, ok := v.(string); ok {
		if s == "$none" {
			return DisplayString{}
		}
		return DisplayStr(s)
	}
	return DisplayNull
}

// pcSkillChecker is the probe's checker: a state by skill name, a default, and a record of the calls.
type pcSkillChecker struct {
	states       *jsObject
	defaultState string
	calls        *[]any
}

func (c pcSkillChecker) CheckPermission(surface string, input any, agentName *string) PermissionCheckResult {
	name, _ := ToRecord(input).str("name")
	*c.calls = append(*c.calls, []any{surface, name, strOrNull(agentName)})
	state, ok := c.states.str(name)
	if !ok {
		state = c.defaultState
	}
	return PermissionCheckResult{ToolName: "skill", State: state, Source: "tool", Origin: "builtin"}
}

func TestPresentationOracle(t *testing.T) {
	read := func(path string) *jsObject {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		v, err := parseJSON(data)
		if err != nil {
			t.Fatal(err)
		}
		return v.(*jsObject)
	}
	s := &pcSuite{t: t, cases: read("testdata/presentation-cases.json"), oracle: read("testdata/presentation-oracle.json")}
	cases, oracle := s.cases, s.oracle
	obj := func(v any) *jsObject { return v.(*jsObject) }
	build := func(spec any) any { return lgBuild(spec, map[string]any{}) }
	run := func(section string, caseList []any, answers []any, fn func(c any) any) {
		t.Helper()
		if len(caseList) != len(answers) {
			t.Fatalf("%s: %d cases, %d answers", section, len(caseList), len(answers))
		}
		for i, c := range caseList {
			s.compare(section, i, pcOK(fn(c)), answers[i], c)
		}
	}
	str := func(o *jsObject, k string) string { v, _ := o.str(k); return v }

	t.Run("fit", func(t *testing.T) {
		run("fit", s.list(cases, "fit"), s.list(oracle, "fit"), func(c any) any {
			o := obj(c)
			width, _ := o.num("width")
			return pcPromptStrings(FitLinesToWidth(pcStrings(o.vals["lines"]), int(width)))
		})
	})

	t.Run("renderPromptDialog", func(t *testing.T) {
		run("dialog", s.list(cases, "dialog"), s.list(oracle, "dialog"), func(c any) any {
			o := obj(c)
			b := obj(o.vals["budget"])
			width, _ := b.num("width")
			budget := DialogBudget{RenderBudget: RenderBudget{MaxRows: pcNum(b.vals["maxRows"]), FieldMaxWidth: pcNum(b.vals["fieldMaxWidth"])}, Width: int(width)}
			return pcView(RenderPromptDialog(pcPayload(t, o.vals["payload"]), budget, pcPaint(str(o, "paint"))))
		})
	})

	t.Run("prompt notifications", func(t *testing.T) {
		run("notice", s.list(cases, "notice"), s.list(oracle, "notice"), func(c any) any {
			o := obj(c)
			req, sess := obj(o.vals["request"]), obj(o.vals["session"])
			requester := obj(req.vals["requester"])
			forwarded, _ := requester.flag("forwarded")
			notice := DescribePromptNotice(str(o, "title"), NoticeRequestFacts{
				ToolName: pcStr(req.vals["toolName"]), Surface: str(req, "surface"),
				Requester: PromptRequester{AgentName: pcStr(requester.vals["agentName"]), Forwarded: forwarded},
			}, NotificationSession{Name: pcStr(sess.vals["name"]), Cwd: str(sess, "cwd")})
			out, n := newObject(), newObject()
			n.set("title", notice.Title)
			n.set("body", notice.Body)
			out.set("notice", n)
			out.set("bytes", RenderPromptNotification(pcStrings(o.vals["channels"]), notice))
			return out
		})
	})

	t.Run("agent renderer", func(t *testing.T) {
		run("agent", s.list(cases, "agent"), s.list(oracle, "agent"), func(c any) any {
			o := obj(c)
			payload := pcPayload(t, o.vals["payload"])
			budget := AgentRenderBudget{FieldMaxWidth: pcNum(o.vals["budget"])}
			reason := pcStr(o.vals["reason"])
			elsewhere, _ := o.flag("decidedElsewhere")
			switch str(o, "fn") {
			case "policy":
				return RenderPolicyDenial(payload, reason, budget)
			case "user":
				return RenderUserDenial(payload, reason, budget)
			case "authorizer":
				return RenderAuthorizerDenial(payload, str(o, "linkName"), reason, budget)
			case "escalated":
				return RenderEscalatedPolicyDenial(payload, EscalatedRule{Pattern: pcStr(o.vals["pattern"]), DecidedElsewhere: elsewhere}, reason, budget)
			case "gateError":
				failure := ""
				if reason != nil {
					failure = *reason
				}
				return RenderGateErrorDenial(payload, GateFailure{Reason: failure, DecidedElsewhere: elsewhere}, budget)
			case "unavailable":
				return RenderUnavailableDenial(payload, reason, budget)
			}
			decision := AsDecisionSource(o.vals["decision"])
			if decision == nil {
				t.Fatalf("not a decision: %s", marshalJSON(o.vals["decision"], ""))
			}
			return RenderRefusal(payload, *decision, reason, budget)
		})
	})

	t.Run("pattern suggestions", func(t *testing.T) {
		g, w := obj(cases.vals["suggest"]), obj(oracle.vals["suggest"])
		s.compare("suggest tag", 0, ExtensionTag, w.vals["tag"], nil)
		sg := func(section string, fn func(c *jsObject) any) {
			run("suggest "+section, s.list(g, section), s.list(w, section), func(c any) any { return fn(obj(c)) })
		}
		sess := func(x SessionApprovalSuggestion) any {
			o := newObject()
			o.set("surface", x.Surface)
			o.set("pattern", x.Pattern)
			o.set("label", x.Label)
			return o
		}
		sg("bash", func(c *jsObject) any { return SuggestBashPattern(str(c, "command")) })
		sg("mcp", func(c *jsObject) any { return SuggestMcpPattern(str(c, "target")) })
		sg("session", func(c *jsObject) any { return sess(SuggestSessionPattern(str(c, "surface"), str(c, "value"))) })
		sg("path", func(c *jsObject) any {
			r := SuggestPathSessionPattern(str(c, "surface"), pcStrings(c.vals["patterns"]))
			o := newObject()
			o.set("surface", r.Surface)
			o.set("patterns", pcPromptStrings(r.Patterns))
			o.set("label", r.Label)
			return o
		})
		sg("exact", func(c *jsObject) any { return sess(SuggestExactSessionPattern(str(c, "surface"), str(c, "pattern"))) })
		run("suggest grantTarget", s.list(g, "grantTarget"), s.list(w, "grantTarget"), func(c any) any { return DescribeGrantTarget(pcGrants(c)) })
		sg("directional", func(c *jsObject) any {
			r := BuildDirectionalSessionLabels(CapabilityDirection(str(c, "direction")), str(c, "target"))
			o := newObject()
			o.set("sessionLabel", r.SessionLabel)
			o.set("widenedLabel", r.WidenedLabel)
			return o
		})
		sg("forwarded", func(c *jsObject) any {
			r := BuildForwardedScopeLabels(pcStr(c.vals["agentName"]), pcGrants(c.vals["grants"]))
			o := newObject()
			o.set("subagentLabel", r.SubagentLabel)
			o.set("servingSessionLabel", r.ServingSessionLabel)
			return o
		})
		sg("pathAccess", func(c *jsObject) any { return strOrNull(BuildPathAccessSessionLabel(pcGrants(c.vals["grants"]))) })
	})

	t.Run("tool input previews", func(t *testing.T) {
		run("inputs", s.list(cases, "inputs"), s.list(oracle, "inputs"), func(c any) any {
			o := obj(c)
			value := build(o.vals["input"])
			plain, withBuiltin := pcFormatter(o.vals["limits"], nil), pcFormatter(o.vals["limits"], pcLookup("builtin"))
			out := newObject()
			out.set("prompt", plain.FormatToolInputForPrompt(str(o, "tool"), value))
			out.set("builtin", withBuiltin.FormatToolInputForPrompt(str(o, "tool"), value))
			logged, ok := plain.FormatGenericToolInputForLog(value)
			if ok {
				out.set("log", logged)
			} else {
				out.set("log", nil)
			}
			return out
		})
		run("mcpinput", s.list(cases, "mcpinput"), s.list(oracle, "mcpinput"), func(c any) any {
			if strings.Contains(marshalJSON(c, ""), "$fn") {
				// A function-valued argument cannot come from a JSON tool input, and String(fn) is its source text: not reproducible.
				// Newer cases (gen-presentation-cases.py) no longer carry one; this recorded file does, until it is re-recorded.
				return pcOKSame
			}
			summary, ok := FormatMcpInputForPrompt.Format(ToRecord(build(obj(c).vals["input"])))
			if !ok {
				u := newObject()
				u.set("$undefined", true)
				return u
			}
			return summary
		})
		run("sanitize", s.list(cases, "sanitize"), s.list(oracle, "sanitize"), func(c any) any {
			o := obj(c)
			var max *float64
			if m, ok := o.num("max"); ok {
				max = &m
			}
			return pcFormatter(nil, nil).SanitizeInlineText(str(o, "value"), max)
		})
		pv, ow := obj(cases.vals["preview"]), obj(oracle.vals["preview"])
		run("preview truncate", s.list(pv, "truncate"), s.list(ow, "truncate"), func(c any) any {
			o := obj(c)
			max, _ := o.num("max")
			return TruncateInlineText(str(o, "value"), max)
		})
		run("preview count", s.list(pv, "count"), s.list(ow, "count"), func(c any) any { return float64(CountTextLines(str(obj(c), "value"))) })
		run("preview format", s.list(pv, "format"), s.list(ow, "format"), func(c any) any {
			n, _ := obj(c).num("n")
			return []any{FormatCount(int(n), "line", "lines")}
		})
		run("preview serialize", s.list(pv, "serialize"), s.list(ow, "serialize"), func(c any) any {
			value := build(obj(c).vals["input"])
			out := newObject()
			out.set("prompt", SerializeToolInputPreview(value))
			out.set("redacted", SerializeRedactedToolInputPreview(value))
			return out
		})
	})

	t.Run("log previews", func(t *testing.T) {
		run("log", s.list(cases, "log"), s.list(oracle, "log"), func(c any) any {
			o := obj(c)
			result := pcCheckResult(obj(o.vals["result"]))
			value := build(o.vals["input"])
			pathBearing := map[string]bool{}
			for _, name := range pcStrings(o.vals["pathBearing"]) {
				pathBearing[name] = true
			}
			f := pcFormatter(nil, nil)
			out := newObject()
			preview, ok := f.GetToolInputPreviewForLog(result, value, pathBearing)
			if ok {
				out.set("preview", preview)
			} else {
				out.set("preview", nil)
			}
			ctx := f.GetPermissionLogContext(result, value, pathBearing)
			context := newObject()
			if ctx.Command != nil {
				context.set("command", *ctx.Command)
			}
			if ctx.Target != nil {
				context.set("target", *ctx.Target)
			}
			if ctx.ToolInputPreview != nil {
				context.set("toolInputPreview", *ctx.ToolInputPreview)
			}
			context.set("origin", ctx.Origin)
			out.set("context", context)
			return out
		})
	})

	t.Run("ask payload builders", func(t *testing.T) {
		run("askpayload", s.list(cases, "askpayload"), s.list(oracle, "askpayload"), func(c any) any {
			o := obj(c)
			var payload PromptPayload
			switch str(o, "fn") {
			case "tool":
				var formatter *ToolPreviewFormatter
				if kind, ok := o.vals["formatter"].(string); ok {
					formatter = pcFormatter(nil, pcLookup(kind))
				}
				payload = BuildToolAskPayload(ToolAskFacts{
					Check: pcCheckResult(obj(o.vals["check"])), AgentName: pcStr(o.vals["agentName"]), Surface: str(o, "surface"),
					InvokedToolName: pcStr(o.vals["invokedToolName"]), Input: build(o.vals["input"]), Formatter: formatter,
				})
			case "path", "extdir":
				f := obj(o.vals["facts"])
				facts := PathAskFacts{ToolName: str(f, "toolName"), PathValue: str(f, "pathValue"), ResolvedPath: pcStr(f.vals["resolvedPath"]),
					AgentName: pcStr(f.vals["agentName"]), MatchedPattern: pcStr(f.vals["matchedPattern"]), Surface: str(f, "surface")}
				if str(o, "fn") == "path" {
					payload = BuildPathAskPayload(facts)
				} else {
					payload = BuildExternalDirectoryAskPayload(ExternalDirectoryAskFacts{PathAskFacts: facts, Cwd: str(f, "cwd")})
				}
			case "bashext":
				f := obj(o.vals["facts"])
				var paths []ExternalPathDisclosure
				for _, e := range f.vals["externalPaths"].([]any) {
					p := obj(e)
					paths = append(paths, ExternalPathDisclosure{Path: str(p, "path"), ResolvedPath: pcStr(p.vals["resolvedPath"])})
				}
				payload = BuildBashExternalDirectoryAskPayload(BashExternalDirectoryAskFacts{
					Command: str(f, "command"), ExternalPaths: paths, Cwd: str(f, "cwd"), AgentName: pcStr(f.vals["agentName"]),
					ToolName: str(f, "toolName"), MatchedPattern: pcStr(f.vals["matchedPattern"]), Surface: str(f, "surface"),
				})
			case "skill":
				payload = BuildSkillAskPayload(str(o, "name"), pcStr(o.vals["agentName"]))
			case "skillpath":
				payload = BuildSkillPathAskPayload(SkillPromptEntry{Name: str(o, "name")}, str(o, "readPath"), pcStr(o.vals["agentName"]))
			default:
				r := obj(o.vals["request"])
				request := ForwardedPermissionRequest{
					RequesterAgentName: str(r, "requesterAgentName"), RequesterSessionID: str(r, "requesterSessionId"),
					Surface: pcDisplay(r, "surface"), Value: pcDisplay(r, "value"),
				}
				if p, ok := r.vals["payload"].(*jsObject); ok {
					pp := pcPayload(t, p)
					request.Payload = &pp
				}
				payload = BuildForwardedAskPayload(request)
			}
			out := newObject()
			out.set("payload", payload.JS())
			out.set("rendered", pcView(RenderPromptDialog(payload, DialogBudget{RenderBudget: RenderBudget{MaxRows: 24, FieldMaxWidth: 400}, Width: 80}, nil)))
			return out
		})
	})

	t.Run("tool registry", func(t *testing.T) {
		g, w := obj(cases.vals["registry"]), obj(oracle.vals["registry"])
		run("registry name", s.list(g, "name"), s.list(w, "name"), func(c any) any {
			name, ok := GetToolNameFromValue(build(obj(c).vals["value"]))
			if !ok {
				return nil
			}
			return name
		})
		run("registry guidelines", s.list(g, "guidelines"), s.list(w, "guidelines"), func(c any) any {
			return pcPromptStrings(GetToolPromptGuidelinesFromValue(build(obj(c).vals["value"])))
		})
		built := func(list any) []any {
			var out []any
			for _, e := range list.([]any) {
				out = append(out, build(e))
			}
			return out
		}
		run("registry read", s.list(g, "read"), s.list(w, "read"), func(c any) any {
			tools := ReadRegisteredTools(built(obj(c).vals["tools"]))
			keys := make([]string, 0, len(tools.GuidelinesByTool))
			for k := range tools.GuidelinesByTool {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			entries := []any{}
			for _, k := range keys {
				entries = append(entries, []any{k, pcPromptStrings(tools.GuidelinesByTool[k])})
			}
			out := newObject()
			out.set("names", pcPromptStrings(tools.Names))
			out.set("guidelines", entries)
			return out
		})
		explained := 0
		answers := s.list(w, "check")
		run("registry check", s.list(g, "check"), answers, func(c any) any {
			o := obj(c)
			var aliases map[string]string
			if a, ok := o.vals["aliases"].(*jsObject); ok {
				aliases = map[string]string{}
				for _, k := range a.keys {
					aliases[k], _ = a.str(k)
				}
			}
			result := CheckRequestedToolRegistration(pcStr(o.vals["requested"]), built(o.vals["tools"]), aliases)
			out := newObject()
			out.set("status", result.Status)
			if result.Status != "missing-tool-name" {
				out.set("requestedToolName", result.RequestedToolName)
				normalized := result.NormalizedToolName
				// The original's plain-object lookup answers an inherited property (constructor, toString, ...) for an alias that is
				// not an own key: a function, not a name (PORT.md, explained difference). The probe records it as "<non-string>".
				if _, own := aliases[result.RequestedToolName]; !own && objectPrototypeKeys[result.RequestedToolName] {
					normalized = "<non-string>"
					explained++
				}
				out.set("normalizedToolName", normalized)
			}
			if result.Status == "unregistered" {
				out.set("availableToolNames", pcPromptStrings(result.AvailableToolNames))
			}
			return out
		})
		t.Logf("%d answers where the original's normalized name is an inherited property, not a string", explained)
		run("registry reasons", s.list(g, "reasons"), s.list(w, "reasons"), func(c any) any {
			o := obj(c)
			return FormatUnknownToolReason(str(o, "toolName"), pcStrings(o.vals["available"]))
		})
		s.compare("registry missing", 0, FormatMissingToolNameReason(), w.vals["missing"], nil)
	})

	t.Run("tool surface sections", func(t *testing.T) {
		run("surface", s.list(cases, "surface"), s.list(oracle, "surface"), func(c any) any {
			o := obj(c)
			snippets := map[string]string{}
			sn := obj(o.vals["toolSnippets"])
			for _, k := range sn.keys {
				snippets[k], _ = sn.str(k)
			}
			guidelines := map[string][]string{}
			gl := obj(o.vals["guidelinesByTool"])
			for _, k := range gl.keys {
				guidelines[k] = pcStrings(gl.vals[k])
			}
			sections := RenderToolSurfaceSections(ToolSurfaceInputs{
				AllowedTools: pcStrings(o.vals["allowedTools"]), ToolSnippets: snippets, GuidelinesByTool: guidelines, PromptGuidelines: pcStrings(o.vals["promptGuidelines"]),
			})
			out := newObject()
			out.set("tools", strOrNull(sections.Tools))
			out.set("rules", sections.Rules)
			return out
		})
	})

	t.Run("skill prompt sanitizer", func(t *testing.T) {
		g, w := obj(cases.vals["skills"]), obj(oracle.vals["skills"])
		run("skills parse", s.list(g, "parse"), s.list(w, "parse"), func(c any) any {
			sections := []any{}
			for _, section := range ParseAllSkillPromptSections(str(obj(c), "prompt")) {
				entries := []any{}
				for _, e := range section.Entries {
					x := newObject()
					x.set("name", e.Name)
					x.set("description", e.Description)
					x.set("location", e.Location)
					entries = append(entries, x)
				}
				x := newObject()
				x.set("entries", entries)
				sections = append(sections, x)
			}
			return sections
		})
		run("skills visible", s.list(g, "visible"), s.list(w, "visible"), func(c any) any {
			o := obj(c)
			calls := []any{}
			checker := pcSkillChecker{states: obj(o.vals["states"]), defaultState: str(o, "defaultState"), calls: &calls}
			n := NewPathNormalizer(PosixPathFlavor, str(o, "cwd"))
			entries := VisibleSkillPromptEntries(str(o, "prompt"), checker, pcStr(o.vals["agentName"]), n)
			list := []any{}
			for _, e := range entries {
				x := newObject()
				x.set("name", e.Name)
				x.set("description", e.Description)
				x.set("location", e.Location)
				x.set("state", e.State)
				x.set("normalizedLocation", e.NormalizedLocation)
				x.set("normalizedBaseDir", e.NormalizedBaseDir)
				list = append(list, x)
			}
			matches := []any{}
			for _, p := range pcStrings(o.vals["probes"]) {
				if m := FindSkillPathMatch(p, entries, n); m != nil {
					matches = append(matches, m.Name)
				} else {
					matches = append(matches, nil)
				}
			}
			out := newObject()
			out.set("entries", list)
			out.set("calls", calls)
			out.set("matches", matches)
			return out
		})
		run("skills without", s.list(g, "without"), s.list(w, "without"), func(c any) any {
			o := obj(c)
			calls := []any{}
			checker := pcSkillChecker{states: obj(o.vals["states"]), defaultState: str(o, "defaultState"), calls: &calls}
			kept := WithoutDeniedSkills(pcStrings(o.vals["names"]), func(name string) string { return name }, checker, pcStr(o.vals["agentName"]))
			out := newObject()
			out.set("kept", pcPromptStrings(kept))
			out.set("calls", calls)
			return out
		})
		base := []any{}
		for _, p := range []string{"/w/repo", "/", "", "/w/repo/", "//", "/a/b/c//", "rel/dir", "name", ".", ".."} {
			base = append(base, HostPath.Basename(p))
		}
		s.compare("skills basename", 0, base, w.vals["basename"], nil)
	})

	t.Logf("%d answers compared, %d differences", s.checked, s.failed)
	if s.failed > 0 {
		t.Fatalf("%d of %d answers differ from the original's", s.failed, s.checked)
	}
}
