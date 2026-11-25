package eval

import (
	_ "embed"
	"fmt"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// upstream: packages/coding-agent/src/tools/eval.ts, tools/eval-doc-topics.ts,
//           prompts/tools/eval.md, eval-helpers.md, eval-judge.md, eval-agents.md,
//           packages/utils/src/prompt.ts

//go:embed eval.md
var evalPromptTemplate string

//go:embed eval-helpers.md
var evalHelpersTemplate string

//go:embed eval-judge.md
var evalJudgeTemplate string

//go:embed eval-agents.md
var evalAgentsTemplate string

// DescriptionOptions mirrors omp's EvalToolDescriptionOptions (eval-doc-topics.ts:14).
type DescriptionOptions struct {
	Py                    bool
	JS                    bool
	Spawns                bool
	SpawnDefaultAgent     string
	AutoBackgroundEnabled bool
	EvalTools             bool
	EagerDelegation       bool
	WaitTool              bool
	Preludes              []Prelude
	// SessionContext is passed to prelude DocumentationFor callbacks (via
	// Prelude.DocumentationText). Left zero when no session context exists
	// (e.g. initial registration time, when Preludes is nil and session_start
	// replaces the rendered text with a live session context).
	SessionContext  sdk.Context
	FirstCallGuides bool
	InlineTopics    bool
	AutoProvision   bool
	ToolNames       []string
	MaxTimeout      float64
}

// DefaultDescriptionOptions returns description options for the active session,
// deriving Spawns from whether an `agent` prelude is registered, and setting
// EvalTools=false since kernel-hosted `@tool`/`tool(fn)` subagent dispatch is not ported.
func DefaultDescriptionOptions(preludes []Prelude) DescriptionOptions {
	hasAgent := false
	for _, p := range preludes {
		if p.Name == "agent" {
			hasAgent = true
			break
		}
	}
	return DescriptionOptions{
		Py:                    true,
		JS:                    true,
		Spawns:                hasAgent,
		SpawnDefaultAgent:     "task",
		AutoBackgroundEnabled: false,
		EvalTools:             false,
		EagerDelegation:       false,
		WaitTool:              true,
		Preludes:              preludes,
		FirstCallGuides:       true,
		InlineTopics:          false,
		AutoProvision:         false,
	}
}

// PreludeSummary returns the one-line summary for a prelude without a session
// context (upstream: evalPreludeSummary). Prefer PreludeSummaryFor when a
// session sdk.Context is available.
func PreludeSummary(p Prelude) (string, bool) {
	return PreludeSummaryFor(p, sdk.Context{})
}

// PreludeSummaryFor returns the one-line summary for a prelude in the session
// of ctx (upstream: evalPreludeSummary).
func PreludeSummaryFor(p Prelude, ctx sdk.Context) (string, bool) {
	if p.Summary != "" {
		return p.Summary, true
	}
	doc := strings.TrimSpace(p.DocumentationText(ctx))
	if doc == "" {
		return "", false
	}
	first, _, _ := strings.Cut(doc, "\n")
	return first, true
}

func evalTemplateValues(opts DescriptionOptions) map[string]any {
	timeoutDefault := clampEvalTimeout(nil, opts.MaxTimeout)
	maxVal := MaxTimeoutSec
	timeoutRaisable := clampEvalTimeout(&maxVal, opts.MaxTimeout) > timeoutDefault
	spawnDefault := opts.SpawnDefaultAgent
	if spawnDefault == "" {
		spawnDefault = "task"
	}
	return map[string]any{
		"py":                     opts.Py,
		"js":                     opts.JS,
		"evalTools":              opts.EvalTools,
		"eagerDelegation":        opts.EagerDelegation,
		"waitTool":               opts.WaitTool,
		"autoBackgroundEnabled":  opts.AutoBackgroundEnabled,
		"spawns":                 opts.Spawns,
		"spawnDefaultAgent":      spawnDefault,
		"spawnAllowedAgentsText": "",
		"autoProvision":          opts.AutoProvision,
		"perCallModel":           false,
		"timeoutDefault":         formatTimeoutNumber(timeoutDefault),
		"timeoutRaisable":        timeoutRaisable,
	}
}

// RenderDescription renders omp's embedded eval.md template with the given options,
// matching omp's `prompt.render` and `prompt.format` (packages/utils/src/prompt.ts).
// upstream: packages/coding-agent/src/tools/eval.ts:184 (getEvalToolDescription)
func RenderDescription(opts DescriptionOptions) string {
	var linkedPreludes []map[string]any
	var firstCallPreludes []map[string]any
	var inlinePreludes []map[string]any

	for _, p := range opts.Preludes {
		// `agent` and `workpool` are already advertised by the built-in `agents` namespace
		// line (gated on `spawns`); do not duplicate them as custom prelude links.
		if isBuiltinBridgePrelude(p.Name) {
			continue
		}
		if p.InlineDocumentation && !opts.InlineTopics {
			if doc := strings.TrimSpace(p.DocumentationText(opts.SessionContext)); doc != "" {
				inlinePreludes = append(inlinePreludes, map[string]any{"name": p.Name, "documentation": doc})
			}
			continue
		}
		summary, ok := PreludeSummaryFor(p, opts.SessionContext)
		if !ok {
			continue
		}
		if strings.TrimSpace(p.FirstCallDocumentation) != "" && opts.FirstCallGuides && !opts.InlineTopics {
			firstCallPreludes = append(firstCallPreludes, map[string]any{"name": p.Name, "summary": summary})
		} else {
			linkedPreludes = append(linkedPreludes, map[string]any{"name": p.Name, "summary": summary})
		}
	}

	ctx := evalTemplateValues(opts)
	ctx["preludes"] = linkedPreludes
	if len(firstCallPreludes) > 0 {
		ctx["firstCallPreludes"] = firstCallPreludes
	}
	if len(opts.ToolNames) > 0 {
		ctx["toolNames"] = opts.ToolNames
	}
	ctx["inlinePreludes"] = inlinePreludes
	if opts.InlineTopics {
		topics := DocTopics(opts)
		order := []string{"judge", "helpers"}
		if opts.Spawns {
			order = append(order, "agents")
		}
		for _, p := range opts.Preludes {
			if !isBuiltinBridgePrelude(p.Name) {
				order = append(order, p.Name)
			}
		}
		var parts []string
		for _, name := range order {
			if doc, ok := topics[name]; ok && doc != "" {
				parts = append(parts, doc)
			}
		}
		ctx["inlineTopics"] = strings.Join(parts, "\n\n")
	}
	desc := formatPromptText(renderHandlebars(evalPromptTemplate, ctx))
	if ex := renderEvalExamples(opts); ex != "" {
		desc += "\n\n" + ex
	}
	return desc
}

// renderEvalExamples renders omp's EvalTool.#examples filtered by enabled language,
// matching packages/coding-agent/src/tools/eval.ts:363-372 and
// packages/ai/src/dialect/examples.ts:14-40 (eval omits the `i` intent field, so no
// intent placeholder is injected).
func renderEvalExamples(opts DescriptionOptions) string {
	if !opts.Py {
		return ""
	}
	return strings.Join([]string{
		"<examples>",
		"# Load a script with spaces without echoing its source",
		"<example>",
		`eval(language="py", code="%load \"scripts/my setup.py\"")`,
		"</example>",
		"</examples>",
	}, "\n")
}

// DocTopics returns the `help://eval/<topic>` Markdown documentation map,
// rendered from the embedded omp templates.
// upstream: packages/coding-agent/src/tools/eval-doc-topics.ts:81 (getEvalDocTopics)
func DocTopics(opts DescriptionOptions) map[string]string {
	ctx := evalTemplateValues(opts)
	topics := map[string]string{
		"judge":   formatPromptText(renderHandlebars(evalJudgeTemplate, ctx)),
		"helpers": formatPromptText(renderHandlebars(evalHelpersTemplate, ctx)),
	}
	if opts.Spawns {
		topics["agents"] = formatPromptText(renderHandlebars(evalAgentsTemplate, ctx))
	}
	for _, p := range opts.Preludes {
		if isBuiltinBridgePrelude(p.Name) {
			continue
		}
		if doc := strings.TrimSpace(p.DocumentationText(opts.SessionContext)); doc != "" {
			topics[p.Name] = doc
		}
	}
	return topics
}

// evalToolSchema returns omp's eval tool JSON Schema.
// upstream: packages/coding-agent/src/tools/eval.ts:119-167
func evalToolSchema(langs []string, maxTimeout float64) sdk.Schema {
	if len(langs) == 0 {
		langs = []string{"py", "js"}
	}
	runtimeNames := map[string]string{
		"py": `"py": Python`,
		"js": `"js": Bun`,
	}
	parts := make([]string, 0, len(langs))
	enumVals := make([]any, 0, len(langs))
	for _, l := range langs {
		if desc, ok := runtimeNames[l]; ok {
			parts = append(parts, desc)
		}
		enumVals = append(enumVals, l)
	}
	defaultTimeout := clampEvalTimeout(nil, maxTimeout)
	timeoutDesc := fmt.Sprintf(
		"Cell deadline in seconds (default %s); nonzero values clamp to %s–%s; 0 disables it.",
		formatTimeoutNumber(defaultTimeout),
		formatTimeoutNumber(MinTimeoutSec),
		formatTimeoutNumber(MaxTimeoutSec),
	)
	if maxTimeout > 0 {
		maxVal := MaxTimeoutSec
		timeoutDesc += fmt.Sprintf(
			" Configured tools.maxTimeout: %ss (effective ceiling %ss).",
			formatTimeoutNumber(maxTimeout),
			formatTimeoutNumber(clampEvalTimeout(&maxVal, maxTimeout)),
		)
	}
	return sdk.Schema{
		"type": "object",
		"properties": map[string]any{
			"language": map[string]any{
				"type":        "string",
				"enum":        enumVals,
				"description": strings.Join(parts, "; "),
			},
			"code": map[string]any{
				"type":        "string",
				"description": "Code or standalone % command.",
			},
			"title": map[string]any{
				"type":        "string",
				"description": "Short transcript label.",
			},
			"timeout": map[string]any{
				"type":        "number",
				"description": timeoutDesc,
			},
			"reset": map[string]any{
				"type":        "boolean",
				"description": "Wipe only this kernel.",
			},
		},
		"required":             []any{"language", "code"},
		"additionalProperties": false,
	}
}

// ---------------------------------------------------------------------------
// Minimal Handlebars renderer for omp's embedded eval*.md templates
// Supports: {{#if}}, {{#unless}}, {{#ifAll}}, {{#each}}, {{#list}}, {{else}},
//           {{var}}, {{{var}}}, {{this}}, {{@last}}, standalone tag lines.
// ---------------------------------------------------------------------------

type hbsToken struct {
	isTag  bool
	raw    string // for tags: inner text (without {{ }}); for text: literal text
	triple bool
}

func tokenizeHbs(tmpl string) []hbsToken {
	var out []hbsToken
	i := 0
	for i < len(tmpl) {
		idx := strings.Index(tmpl[i:], "{{")
		if idx < 0 {
			out = append(out, hbsToken{isTag: false, raw: tmpl[i:]})
			break
		}
		if idx > 0 {
			out = append(out, hbsToken{isTag: false, raw: tmpl[i : i+idx]})
		}
		i += idx
		if strings.HasPrefix(tmpl[i:], "{{{") {
			end := strings.Index(tmpl[i+3:], "}}}")
			if end < 0 {
				out = append(out, hbsToken{isTag: false, raw: tmpl[i:]})
				break
			}
			out = append(out, hbsToken{isTag: true, triple: true, raw: strings.TrimSpace(tmpl[i+3 : i+3+end])})
			i += 3 + end + 3
		} else {
			end := strings.Index(tmpl[i+2:], "}}")
			if end < 0 {
				out = append(out, hbsToken{isTag: false, raw: tmpl[i:]})
				break
			}
			out = append(out, hbsToken{isTag: true, raw: strings.TrimSpace(tmpl[i+2 : i+2+end])})
			i += 2 + end + 2
		}
	}
	return stripStandaloneTagNewlines(out)
}

func isBlockControlTag(raw string) bool {
	return strings.HasPrefix(raw, "#") || strings.HasPrefix(raw, "/") || raw == "else"
}

// Handlebars strips whitespace + trailing newline around block tags that stand alone on a line.
func stripStandaloneTagNewlines(toks []hbsToken) []hbsToken {
	res := make([]hbsToken, len(toks))
	copy(res, toks)
	for i := range res {
		if !res[i].isTag || !isBlockControlTag(res[i].raw) {
			continue
		}
		// Check left side: only spaces/tabs since last newline (or start of template).
		leftOK := false
		leftTrim := 0
		if i == 0 {
			leftOK = true
		} else if !res[i-1].isTag {
			prev := res[i-1].raw
			nl := strings.LastIndexByte(prev, '\n')
			after := prev
			if nl >= 0 {
				after = prev[nl+1:]
			}
			if strings.Trim(after, " \t\r") == "" {
				leftOK = true
				leftTrim = len(after)
			}
		} else if isBlockControlTag(res[i-1].raw) {
			leftOK = true
		}
		// Check right side: only spaces/tabs before newline (or end of template).
		rightOK := false
		rightTrim := 0
		if i == len(res)-1 {
			rightOK = true
		} else if !res[i+1].isTag {
			next := res[i+1].raw
			nl := strings.IndexByte(next, '\n')
			if nl >= 0 {
				before := next[:nl]
				if strings.Trim(before, " \t\r") == "" {
					rightOK = true
					rightTrim = nl + 1
				}
			} else if strings.Trim(next, " \t\r") == "" {
				rightOK = true
				rightTrim = len(next)
			}
		}
		if leftOK && rightOK {
			if leftTrim > 0 && i > 0 && !res[i-1].isTag {
				res[i-1].raw = res[i-1].raw[:len(res[i-1].raw)-leftTrim]
			}
			if rightTrim > 0 && i+1 < len(res) && !res[i+1].isTag {
				res[i+1].raw = res[i+1].raw[rightTrim:]
			}
		}
	}
	return res
}

func renderHandlebars(tmpl string, ctx map[string]any) string {
	toks := tokenizeHbs(tmpl)
	out, _ := evalHbsBlock(toks, 0, "", []map[string]any{ctx})
	return out
}

func evalHbsBlock(toks []hbsToken, pos int, endTag string, scopes []map[string]any) (string, int) {
	var b strings.Builder
	for pos < len(toks) {
		tok := toks[pos]
		if !tok.isTag {
			b.WriteString(tok.raw)
			pos++
			continue
		}
		raw := tok.raw
		if endTag != "" && raw == "/"+endTag {
			return b.String(), pos + 1
		}
		if raw == "else" {
			return b.String(), pos
		}
		if strings.HasPrefix(raw, "#") {
			fields := strings.Fields(raw[1:])
			if len(fields) == 0 {
				pos++
				continue
			}
			helper := fields[0]
			args := fields[1:]
			// Find matching {{else}} and {{/<helper>}}
			bodyStart := pos + 1
			elseIdx, endIdx := findBlockBounds(toks, bodyStart, helper)
			var trueSlice, falseSlice []hbsToken
			if elseIdx >= 0 {
				trueSlice = toks[bodyStart:elseIdx]
				falseSlice = toks[elseIdx+1 : endIdx]
			} else {
				trueSlice = toks[bodyStart:endIdx]
			}
			switch helper {
			case "if":
				if len(args) > 0 && isTruthy(lookupHbs(scopes, args[0])) {
					s, _ := evalHbsBlock(trueSlice, 0, "", scopes)
					b.WriteString(s)
				} else if len(falseSlice) > 0 {
					s, _ := evalHbsBlock(falseSlice, 0, "", scopes)
					b.WriteString(s)
				}
			case "unless":
				if len(args) > 0 && !isTruthy(lookupHbs(scopes, args[0])) {
					s, _ := evalHbsBlock(trueSlice, 0, "", scopes)
					b.WriteString(s)
				} else if len(falseSlice) > 0 {
					s, _ := evalHbsBlock(falseSlice, 0, "", scopes)
					b.WriteString(s)
				}
			case "ifAll":
				all := len(args) > 0
				for _, a := range args {
					if !isTruthy(lookupHbs(scopes, a)) {
						all = false
						break
					}
				}
				if all {
					s, _ := evalHbsBlock(trueSlice, 0, "", scopes)
					b.WriteString(s)
				} else if len(falseSlice) > 0 {
					s, _ := evalHbsBlock(falseSlice, 0, "", scopes)
					b.WriteString(s)
				}
			case "each":
				if len(args) > 0 {
					items := toSlice(lookupHbs(scopes, args[0]))
					for idx, item := range items {
						child := map[string]any{
							"this":  item,
							"@last": idx == len(items)-1,
						}
						if m, ok := item.(map[string]any); ok {
							for k, v := range m {
								child[k] = v
							}
						}
						s, _ := evalHbsBlock(trueSlice, 0, "", append(scopes, child))
						b.WriteString(s)
					}
				}
			case "list":
				if len(args) > 0 {
					items := toSlice(lookupHbs(scopes, args[0]))
					joinSep := "\n"
					for _, a := range args[1:] {
						if strings.HasPrefix(a, "join=") {
							joinSep = strings.Trim(a[len("join="):], `"'`)
							joinSep = strings.ReplaceAll(joinSep, `\n`, "\n")
						}
					}
					var rendered []string
					for idx, item := range items {
						child := map[string]any{
							"this":  item,
							"@last": idx == len(items)-1,
						}
						if m, ok := item.(map[string]any); ok {
							for k, v := range m {
								child[k] = v
							}
						}
						s, _ := evalHbsBlock(trueSlice, 0, "", append(scopes, child))
						rendered = append(rendered, s)
					}
					b.WriteString(strings.Join(rendered, joinSep))
				}
			}
			pos = endIdx + 1
			continue
		}
		val := lookupHbs(scopes, raw)
		if val != nil {
			b.WriteString(fmt.Sprint(val))
		}
		pos++
	}
	return b.String(), pos
}

func findBlockBounds(toks []hbsToken, start int, helper string) (elseIdx int, endIdx int) {
	elseIdx = -1
	depth := 1
	for i := start; i < len(toks); i++ {
		if !toks[i].isTag {
			continue
		}
		raw := toks[i].raw
		if strings.HasPrefix(raw, "#") {
			depth++
		} else if strings.HasPrefix(raw, "/") {
			depth--
			if depth == 0 {
				return elseIdx, i
			}
		} else if raw == "else" && depth == 1 && elseIdx == -1 {
			elseIdx = i
		}
	}
	return elseIdx, len(toks)
}

func lookupHbs(scopes []map[string]any, expr string) any {
	for i := len(scopes) - 1; i >= 0; i-- {
		if v, ok := lookupPath(scopes[i], expr); ok {
			return v
		}
	}
	return nil
}

func lookupPath(scope map[string]any, expr string) (any, bool) {
	if v, ok := scope[expr]; ok {
		return v, true
	}
	parts := strings.Split(expr, ".")
	var cur any = scope
	for _, p := range parts {
		switch m := cur.(type) {
		case map[string]any:
			var ok bool
			cur, ok = m[p]
			if !ok {
				return nil, false
			}
		case []any:
			if p == "length" {
				return len(m), true
			}
			return nil, false
		case []string:
			if p == "length" {
				return len(m), true
			}
			return nil, false
		case []map[string]any:
			if p == "length" {
				return len(m), true
			}
			return nil, false
		default:
			return nil, false
		}
	}
	return cur, true
}

func isTruthy(v any) bool {
	if v == nil {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x != ""
	case int:
		return x != 0
	case float64:
		return x != 0
	case []any:
		return len(x) > 0
	case []string:
		return len(x) > 0
	case []map[string]any:
		return len(x) > 0
	default:
		return true
	}
}

func toSlice(v any) []any {
	switch s := v.(type) {
	case []any:
		return s
	case []string:
		out := make([]any, len(s))
		for i, item := range s {
			out[i] = item
		}
		return out
	case []map[string]any:
		out := make([]any, len(s))
		for i, item := range s {
			out[i] = item
		}
		return out
	}
	return nil
}

// formatPromptText mirrors omp's prompt.format (packages/utils/src/prompt.ts:238-350):
// strips trailing whitespace per line (outside fenced code blocks), removes runs of 2+
// consecutive blank lines while preserving single blank lines, and trims blank lines
// immediately before closing XML tags and at the start/end.
func formatPromptText(raw string) string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	lines := strings.Split(raw, "\n")
	out := make([]string, 0, len(lines))
	inCodeBlock := false

	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], " \t")
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inCodeBlock = !inCodeBlock
			out = append(out, line)
			continue
		}
		if inCodeBlock {
			out = append(out, line)
			continue
		}
		if trimmed == "" {
			// Run of 2+ consecutive blank lines is stripped entirely; single blank kept.
			if i+1 >= len(lines) || strings.TrimSpace(lines[i+1]) == "" {
				for len(out) > 0 && out[len(out)-1] == "" {
					out = out[:len(out)-1]
				}
				for i+1 < len(lines) && strings.TrimSpace(lines[i+1]) == "" {
					i++
				}
				continue
			}
			if len(out) == 0 || out[len(out)-1] == "" {
				continue
			}
			out = append(out, "")
			continue
		}
		if strings.HasPrefix(trimmed, "</") && strings.HasSuffix(trimmed, ">") {
			for len(out) > 0 && out[len(out)-1] == "" {
				out = out[:len(out)-1]
			}
		}
		out = append(out, line)
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}
