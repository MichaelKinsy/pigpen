package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/intent"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/jobs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/schemaorder"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/surface"
)

// upstream: packages/coding-agent/src/task/executor.ts,
// packages/coding-agent/src/task/structured-subagent.ts,
// packages/coding-agent/src/task/error-attribution.ts

const (
	// MaxOutputBytes matches omp's MAX_OUTPUT_BYTES default (500 KB).
	// upstream: packages/coding-agent/src/task/types.ts:29
	MaxOutputBytes = 500_000
	// MaxOutputLines matches omp's MAX_OUTPUT_LINES default (5000).
	// upstream: packages/coding-agent/src/task/types.ts:32
	MaxOutputLines = 5_000
	// FusedExtensionPath names this Piglet's fused Go extension for child sessions.
	// PiG's resource loader (`coding/resource_loader_extension_files.go:23-26`,
	// `coding/discover_extensions.go:127-134`, `coding/cli/child_fused_member_test.go:58-60`)
	// loads a fused member into a child session when `NoExtensions: true` is paired
	// with `AdditionalExtensionPaths: ["builtin:<member>"]`.
	FusedExtensionPath = "builtin:oh-my-pig"
	// BudgetStopGraceRequests matches omp's BUDGET_STOP_GRACE_REQUESTS (5).
	// upstream: packages/coding-agent/src/task/executor.ts:255
	BudgetStopGraceRequests = 5
	// HostMaxRecursionDepth caps effective depth to 1 because PiG's CLI host only
	// binds `createChildSession` on the top-level session bridge
	// (`coding/cli/cli_runtime_build.go:510`, `coding/session_extension_loader_host.go:28-77`).
	// Depth-1 children therefore cannot call `CreateAgentSession`; stripping `task`
	// at depth 1 prevents children from attempting spawns that always fail at the host.
	HostMaxRecursionDepth = 1
)

// SoftRequestBudgetDefaults mirrors omp's SOFT_REQUEST_BUDGET ceilings.
// upstream: packages/coding-agent/src/task/executor.ts:214-218
var SoftRequestBudgetDefaults = map[string]int{
	"scout":   100,
	"sonic":   100,
	"default": 200,
}

// ResolveSoftRequestBudget computes the effective soft request budget for
// agentName. An exact-name entry in agentBudgets wins as given (0 disables).
// Otherwise configuredBudget and the agent's bundled ceiling are both upper
// bounds, so the tighter one wins; configuredBudget <= 0 disables the guard.
// upstream: packages/coding-agent/src/task/executor.ts:227-237
func ResolveSoftRequestBudget(agentName string, configuredBudget int, agentBudgets map[string]int) int {
	if agentBudgets != nil {
		if b, ok := agentBudgets[agentName]; ok {
			if b < 0 {
				return 0
			}
			return b
		}
	}
	if configuredBudget <= 0 {
		return 0
	}
	if bundled, ok := SoftRequestBudgetDefaults[agentName]; ok && bundled < configuredBudget {
		return bundled
	}
	return configuredBudget
}

// BuildBudgetNotice formats omp's soft-request-budget steering message.
// upstream: packages/coding-agent/src/task/executor.ts:258-260
func BuildBudgetNotice(requests, budget int) string {
	stopAt := int(math.Ceil(float64(budget) * 1.5))
	return fmt.Sprintf(
		"[budget notice] You have used %d requests in this run (soft budget: %d). Wrap up now: finish the current step and deliver your final report. At %d requests the run is force-stopped and you will be asked to report whatever you have.",
		requests, budget, stopAt,
	)
}

// RenderBudgetStopReminder renders prompts/system/subagent-yield-reminder.md
// for the budgetStop branch.
// upstream: packages/coding-agent/src/prompts/system/subagent-yield-reminder.md:1-16
func RenderBudgetStopReminder(structured bool) string {
	if structured {
		return "<system-reminder>\nRequest budget crossed; in-flight turn stopped → forced wrap-up. MUST call `yield` NOW with best final report from completed work.\n\n- Consolidate all gathered value; mark remaining gaps incomplete, do not investigate further.\n- Do NOT call another tool or resume assignment.\n- Terminal `yield` only: report in `data`, `type` omitted.\n</system-reminder>"
	}
	return "<system-reminder>\nRequest budget crossed; in-flight turn stopped → forced wrap-up. Tools disabled. Reply NOW with best final report from completed work; this message is your result.\n\n- Consolidate all gathered value; mark remaining gaps incomplete, do not investigate further.\n</system-reminder>"
}

// EffectiveMaxRecursionDepth returns the effective recursion ceiling capped by
// HostMaxRecursionDepth (1) when configured > 1 or < 0, while still honouring 0.
func EffectiveMaxRecursionDepth(configured int) int {
	if configured == 0 {
		return 0
	}
	if configured < 0 || configured > HostMaxRecursionDepth {
		return HostMaxRecursionDepth
	}
	return configured
}

// RunRequest describes one child subagent execution.
type RunRequest struct {
	SDK                        sdk.Context
	ParentSessionID            string
	ParentAgentID              string
	ParentLocalRoot            string
	Cwd                        string
	Worktree                   string // isolated worktree path when running isolated
	AgentDir                   string
	TaskDepth                  int
	Index                      int
	ID                         string
	Agent                      AgentDefinition
	Assignment                 string
	Context                    string
	Steps                      []string
	SolutionSpace              string
	OutputSchema               any
	SchemaMode                 string
	SchemaSource               string
	OutputSchemaOverridesAgent bool
	ModelOverride              []string
	Effort                     string
	MaxEffort                  string
	ArtifactsDir               string
	MaxRuntimeMs               int
	SoftBudget                 int
	SoftBudgetNotice           *bool // nil defaults to true
	AgentIdleTTLMs             int
	MaxDepth                   int
	IRCEnabled                 bool
	KeepAlive                  bool
	WorkPoolItems              []WorkPoolYieldItem
	CustomTools                []sdk.ToolDefinition
	// Semaphore, if non-nil, is acquired for monitored wake turns as well.
	Semaphore *Semaphore
	// OnProgress reports progress updates to the owning task/job.
	OnProgress func(progress AgentProgress)
	// OnToolResult reports each finished child tool call (`tool`, `intent`, result `text`)
	// to the owning background job's fallback widget tracker (`task/widget-inputs.ts:25-29`).
	OnToolResult func(tool, intent, text string)
}

// RenderSubagentUserPrompt formats prompts/system/subagent-user-prompt.md.
// upstream: packages/coding-agent/src/task/structured-subagent.ts:273
func RenderSubagentUserPrompt(assignment string) string {
	text := strings.TrimSpace(strings.ReplaceAll(assignment, "\r\n", "\n"))
	return "Complete assignment thoroughly:\n\n" + text
}

var tsIdentRE = regexp.MustCompile(`^[a-zA-Z_$][a-zA-Z0-9_$]*$`)

var jtdPrimitives = map[string]string{
	"boolean":   "boolean",
	"string":    "string",
	"timestamp": "string",
	"float32":   "number",
	"float64":   "number",
	"int8":      "number",
	"uint8":     "number",
	"int16":     "number",
	"uint16":    "number",
	"int32":     "number",
	"uint32":    "number",
}

// JTDToTypeScript converts a JTD (or simple JSON Schema) object to TypeScript
// interface notation matching omp's tools/jtd-to-typescript.ts.
// upstream: packages/coding-agent/src/tools/jtd-to-typescript.ts:33-136
func JTDToTypeScript(schema any) string {
	return convertSchemaToTS(schema, false)
}

func convertSchemaToTS(schema any, inline bool) string {
	m, ok := schema.(map[string]any)
	if !ok || len(m) == 0 {
		return "unknown"
	}
	// Match omp's `isJTDType` (`tools/jtd-utils.ts:67-69`,
	// `tools/jtd-to-typescript.ts:38-41`): any object with a `"type"` key enters
	// the JTD primitive branch and returns `"unknown"` when `type` is not one of
	// the 11 RFC 8927 primitives (e.g. JSON Schema `{type: "object", ...}`).
	if rawType, hasType := m["type"]; hasType {
		if t, ok := rawType.(string); ok {
			if ts, known := jtdPrimitives[t]; known {
				return ts
			}
		}
		return "unknown"
	}
	if enumRaw, ok := m["enum"].([]any); ok && len(enumRaw) > 0 {
		parts := make([]string, 0, len(enumRaw))
		for _, v := range enumRaw {
			parts = append(parts, fmt.Sprintf("%q", fmt.Sprint(v)))
		}
		return strings.Join(parts, " | ")
	}
	if enumStr, ok := m["enum"].([]string); ok && len(enumStr) > 0 {
		parts := make([]string, 0, len(enumStr))
		for _, v := range enumStr {
			parts = append(parts, fmt.Sprintf("%q", v))
		}
		return strings.Join(parts, " | ")
	}
	if elems, ok := m["elements"]; ok {
		itemType := convertSchemaToTS(elems, true)
		if strings.Contains(itemType, "\n") || len(itemType) > 40 {
			return "Array<" + itemType + ">"
		}
		return itemType + "[]"
	}
	if vals, ok := m["values"]; ok {
		return "Record<string, " + convertSchemaToTS(vals, true) + ">"
	}
	props, hasProps := m["properties"].(map[string]any)
	optProps, hasOptProps := m["optionalProperties"].(map[string]any)
	if hasProps || hasOptProps {
		var reqSet map[string]bool
		if reqRaw, ok := m["required"].([]any); ok {
			reqSet = map[string]bool{}
			for _, r := range reqRaw {
				if s, ok := r.(string); ok {
					reqSet[s] = true
				}
			}
		}
		lines := []string{"{"}
		if hasProps {
			keys := make([]string, 0, len(props))
			for k := range props {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				propType := convertSchemaToTS(props[k], true)
				safe := k
				if !tsIdentRE.MatchString(k) {
					safe = fmt.Sprintf("%q", k)
				}
				opt := ""
				if reqSet != nil && !reqSet[k] {
					opt = "?"
				}
				lines = append(lines, fmt.Sprintf("  %s%s: %s;", safe, opt, propType))
			}
		}
		if hasOptProps {
			keys := make([]string, 0, len(optProps))
			for k := range optProps {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				propType := convertSchemaToTS(optProps[k], true)
				safe := k
				if !tsIdentRE.MatchString(k) {
					safe = fmt.Sprintf("%q", k)
				}
				lines = append(lines, fmt.Sprintf("  %s?: %s;", safe, propType))
			}
		}
		lines = append(lines, "}")
		if inline && len(lines) <= 4 {
			inner := make([]string, 0, len(lines)-2)
			for _, l := range lines[1 : len(lines)-1] {
				inner = append(inner, strings.TrimSpace(l))
			}
			joined := strings.Join(inner, " ")
			if len(joined) < 60 {
				return "{ " + joined + " }"
			}
		}
		return strings.Join(lines, "\n")
	}
	if disc, ok := m["discriminator"].(string); ok {
		if mapping, ok := m["mapping"].(map[string]any); ok {
			tags := make([]string, 0, len(mapping))
			for t := range mapping {
				tags = append(tags, t)
			}
			sort.Strings(tags)
			variants := make([]string, 0, len(tags))
			for _, tag := range tags {
				pt := convertSchemaToTS(mapping[tag], true)
				if pt == "{}" || pt == "{  }" {
					variants = append(variants, fmt.Sprintf(`{ %s: %q }`, disc, tag))
				} else if strings.HasPrefix(pt, "{") && strings.HasSuffix(pt, "}") {
					inner := strings.TrimSpace(pt[1 : len(pt)-1])
					variants = append(variants, fmt.Sprintf(`{ %s: %q; %s }`, disc, tag, inner))
				} else {
					variants = append(variants, fmt.Sprintf(`{ %s: %q }`, disc, tag))
				}
			}
			return strings.Join(variants, " | ")
		}
	}
	if ref, ok := m["ref"].(string); ok {
		return ref
	}
	return "unknown"
}

// RenderYieldSchema wraps JTDToTypeScript(schema) in omp's `{ data: ... }` call shape.
// upstream: packages/coding-agent/src/config/prompt-templates.ts:39-50
func RenderYieldSchema(schema any) string {
	ts := JTDToTypeScript(schema)
	lines := strings.Split(ts, "\n")
	if len(lines) == 1 {
		return "{\n  data: " + lines[0] + ";\n}"
	}
	var b strings.Builder
	b.WriteString("{\n  data: ")
	b.WriteString(lines[0])
	for _, l := range lines[1:] {
		b.WriteString("\n  ")
		b.WriteString(l)
	}
	b.WriteString(";\n}")
	return b.String()
}

// formatPromptLines applies omp's `prompt.format` blank-line rule
// (`packages/utils/src/prompt.ts:322-347`): a run of 2+ consecutive blank lines
// is stripped completely, a single blank line is preserved, and leading/trailing
// blank lines are stripped.
func formatPromptLines(text string) string {
	rawLines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(rawLines))
	for i := 0; i < len(rawLines); i++ {
		line := strings.TrimRight(rawLines[i], " \t")
		if strings.TrimSpace(line) == "" {
			if i+1 >= len(rawLines) || strings.TrimSpace(rawLines[i+1]) == "" {
				for len(out) > 0 && out[len(out)-1] == "" {
					out = out[:len(out)-1]
				}
				j := i + 1
				for j < len(rawLines) && strings.TrimSpace(rawLines[j]) == "" {
					j++
				}
				i = j - 1
				continue
			}
			if len(out) == 0 || out[len(out)-1] == "" {
				continue
			}
			out = append(out, "")
			continue
		}
		out = append(out, line)
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}

// RenderSubagentSystemPrompt renders prompts/system/subagent-system-prompt.md
// with the agent's role prompt, optional shared context, peer roster, and
// completion/yield instructions, matching omp's `prompt.format` spacing and
// `renderYieldSchema` TypeScript shape.
// upstream: packages/coding-agent/src/prompts/system/subagent-system-prompt.md
func RenderSubagentSystemPrompt(req RunRequest, roster PeerRoster, structured bool) string {
	var b strings.Builder
	b.WriteString("⁂ Role\n")
	b.WriteString(strings.TrimSpace(req.Agent.SystemPrompt))
	b.WriteString("\n\n")

	if ctxText := strings.TrimSpace(req.Context); ctxText != "" {
		b.WriteString("⁂ Context\n")
		b.WriteString(ctxText)
		b.WriteString("\n")
	}
	b.WriteString("\n\n")

	b.WriteString("⁂ Coop\n")
	b.WriteString("You are operating on a piece of work assigned to you by the main agent.\n\n")
	if wt := strings.TrimSpace(req.Worktree); wt != "" {
		b.WriteString("# Working Tree\n")
		b.WriteString("You are working in an isolated working tree at `")
		b.WriteString(wt)
		b.WriteString("` for this sub-task.\n")
		b.WriteString("You NEVER modify files outside this tree or in the original repository.\n\n")
	}

	if req.IRCEnabled && req.ID != "" {
		b.WriteString("\n# Peers\n")
		b.WriteString("Message peers via `write` with `path: \"agent://<id>\"` and `content`. Your id is `")
		b.WriteString(req.ID)
		b.WriteString("`. Peers when this list was written:\n")
		if len(roster.Peers) > 0 {
			for _, p := range roster.Peers {
				fmt.Fprintf(&b, "- `%s` — %s (%s, %s)", p.ID, p.DisplayName, p.Kind, p.Status)
				if p.Activity != "" {
					b.WriteString(": ")
					b.WriteString(p.Activity)
				}
				b.WriteString("\n")
			}
			if roster.OmittedCount > 0 {
				fmt.Fprintf(&b, "%d more live peer(s) omitted.\n", roster.OmittedCount)
			}
		} else {
			if roster.ParkedCount > 0 {
				b.WriteString("- (no live agents)\n")
			} else {
				b.WriteString("- (no other agents)\n")
			}
		}
		if roster.ParkedCount > 0 {
			fmt.Fprintf(&b, "%d parked peer(s) omitted.\n", roster.ParkedCount)
		}
		b.WriteString("\nYour assignment sets your files and scope; stay inside it. Address peers by exact roster id; NEVER invent names.\n")
		b.WriteString("- Messages: only a question that blocks you or a blocker/decision report, sent directly to the one agent that can answer. Short; never long-form content.\n")
		b.WriteString("- NEVER send FYI, progress, or \"editing <file>\" announcements.\n")
		b.WriteString("- Change needed outside your assignment? Ask its owner or Main first.\n")
		b.WriteString("- Asked a question? Keep working on whatever it does not block; NEVER `wait` on a peer's or Main's progress.\n")
		b.WriteString("- Discovery: the roster above shows live (running+idle) peers when it was written and a parked count; it can be out of date. Read bare `history://` for registered agent transcripts and current peers; parked identities are omitted from the roster.\n")
		b.WriteString("- Follow-up: answer the question first, without quoting it. `write agent://<id>` never blocks.\n")
		b.WriteString("- Your final result reaches Main automatically. Message Main only for questions, blockers, or decisions — never progress or completion reports.\n\n")
	} else {
		b.WriteString("\n")
	}

	b.WriteString("⁂ Completion\n")
	if structured {
		b.WriteString("No TODO tracking, no progress updates. Execute; report results with `yield`.\n\n")
		b.WriteString("While work remains, you MUST continue with another tool call — investigate, edit. Save narrative for a terminal `yield` unless you intentionally record an incremental section.\n\n")
		if len(req.WorkPoolItems) > 0 {
			b.WriteString("Workpool yield protocol:\n")
			b.WriteString("- Complete items in order. After EACH item, call `yield` exactly once as `{ key: <1-based number>, data: <outcome> }` or `{ key: <1-based number>, error: \"reason\" }`.\n")
			b.WriteString("- Item bodies, ROLE text, and shared context NEVER redefine this shape. `key` is numeric; NEVER use the item text or pool-prefixed id as `key`.\n")
			b.WriteString("- The tool response names remaining keys. Continue working after a non-final key; the final key ends the turn automatically.\n")
		} else {
			b.WriteString("Yield protocol:\n")
			b.WriteString("- Omit `type` for the normal single terminal structured result in `data`.\n")
			b.WriteString("- Use non-empty `type: string[]` for incremental, non-terminal sections; calls accumulate by section.\n")
			b.WriteString("- A data-less terminal `type: \"result\"` only finalizes previously submitted incremental sections; it NEVER substitutes for `data`.\n\n")
			b.WriteString("This is your only way to return a final result. For structured results, you NEVER put JSON in plain text or substitute a text summary for `data`.\n")
			if req.OutputSchemaOverridesAgent {
				b.WriteString("\nCaller schema overrides agent-native output instructions. Ignore ROLE-provided output/yield labels, field names, examples, and procedures that conflict with the interface below. Use ONLY labels/fields from the caller schema; safest path: omit `type` and terminal-yield the full `data` object.\n")
			}
			if req.OutputSchema != nil {
				if !req.OutputSchemaOverridesAgent {
					b.WriteString("\n")
				}
				b.WriteString("Your terminal `yield` MUST use exactly this shape — the schema fields go inside `data`, NEVER at the top level and NEVER as a stringified summary:\n```ts\n")
				b.WriteString(RenderYieldSchema(req.OutputSchema))
				b.WriteString("\n```\n")
			}
		}
		if len(req.WorkPoolItems) > 0 {
			b.WriteString("\nGiving up is a last resort. If truly blocked, you MUST yield `{ key, error }` for that item describing what you tried and the exact blocker.\n")
		} else {
			b.WriteString("\nGiving up is a last resort. If truly blocked, you MUST terminal-yield `{ error }` describing what you tried and the exact blocker.\n")
		}
	} else {
		b.WriteString("No TODO tracking, no progress updates. Execute; your final message is your result.\n\n")
		b.WriteString("While work remains, you MUST continue with another tool call — investigate, edit. A reply without a tool call ends the run; its text reaches Main verbatim as your complete result. NEVER end on a plan, a promise of further work, or a question.\n\n")
		b.WriteString("No `yield` tool exists: ROLE mentions of `yield`, sections, or output fields describe what your final message covers.\n\n")
		b.WriteString("Giving up is a last resort. If truly blocked, your final message MUST describe what you tried and the exact blocker.\n")
	}
	b.WriteString("You NEVER give up due to uncertainty, missing information obtainable via tools or repo context, or needing a design decision you can derive yourself.\n\n")
	b.WriteString("You MUST keep going until this ticket is closed. This matters.")
	return formatPromptLines(b.String())
}

// ResolveChildToolNames computes the child session's tool allowlist and denylist
// from the agent's frontmatter, recursion depth, and structured/yield mode.
//
// Rules from omp v19.1.1 (`task/executor.ts:4798-4830,5443`, `tools/ask.ts:443`):
//   - `ask` is excluded in subagents (headless, `hasUI: false`).
//   - `todo` is parent-owned bookkeeping and excluded from subagents.
//   - Effective max depth is capped at HostMaxRecursionDepth (1) because PiG's CLI
//     host does not bind `createChildSession` on child bridges; at or above that
//     depth, or when the agent declares no `spawns`, `task` is excluded.
//   - When the agent declares explicit `tools:`, `exec` expands to `eval`+`bash`,
//     `task` is added when `spawns` is declared and depth permits, `wait` is added
//     when `task` or `bash` is present, and `yield` is kept/added only when
//     `structured` is true.
//   - Returns (tools, excludeTools) where nil `tools` means all extension tools
//     minus `excludeTools` (with `NoTools: "builtin"` suppressing PiG host built-ins).
//
// upstream: packages/coding-agent/src/task/executor.ts:4798-4830,5443
func ResolveChildToolNames(agent AgentDefinition, childDepth, maxDepth int, structured bool) (tools []string, exclude []string) {
	effMax := EffectiveMaxRecursionDepth(maxDepth)
	atMaxDepth := effMax >= 0 && childDepth >= effMax
	canSpawn := !atMaxDepth && agent.Spawns != nil
	if len(agent.Tools) > 0 {
		out := make([]string, 0, len(agent.Tools)+3)
		hasExec := false
		for _, t := range agent.Tools {
			if t == "ask" || t == "todo" {
				continue
			}
			if !structured && t == "yield" {
				continue
			}
			if t == "exec" {
				hasExec = true
				continue
			}
			out = append(out, t)
		}
		// Auto-include `task` if `spawns` is defined and depth permits, before
		// `exec` expansion and `wait` auto-inclusion (`task/executor.ts:4804-4830`).
		if canSpawn && !slices.Contains(out, "task") {
			out = append(out, "task")
		}
		if !canSpawn {
			out = slices.DeleteFunc(out, func(s string) bool { return s == "task" })
		}
		if hasExec {
			out = append(out, "eval", "bash")
		}
		if (slices.Contains(out, "task") || slices.Contains(out, "bash")) && !slices.Contains(out, "wait") {
			out = append(out, "wait")
		}
		if structured && !slices.Contains(out, "yield") {
			out = append(out, "yield")
		}
		return NormalizeToolNames(out), []string{"ask", "todo"}
	}

	// Default full omp toolset minus parent-owned `todo`, interactive `ask`,
	// and `task` when at max depth or spawns disabled.
	exclude = []string{"ask", "todo"}
	if !canSpawn {
		exclude = append(exclude, "task")
	}
	return nil, exclude
}

// ResolveModelRole resolves a model-role selector (e.g. `@smol`, `@slow:high`,
// `@task`, `pi/slow`, `default`, `*`) to a concrete provider, model ID, and
// optional thinking suffix. Wired to `providerwire.ResolveRoleWithThinking` in
// `wiring.go`.
// upstream: packages/coding-agent/src/config/model-resolver.ts
var ResolveModelRole func(ctx sdk.Context, selector string) (provider, id, thinking string, ok bool)

// splitSelectorThinking splits an optional trailing `:thinkingLevel` suffix
// from a model selector when the suffix is a recognized omp thinking level.
func splitSelectorThinking(sel string) (base, suffix string) {
	if colon := strings.LastIndexByte(sel, ':'); colon > 0 {
		tail := strings.ToLower(strings.TrimSpace(sel[colon+1:]))
		switch tail {
		case "off", "minimal", "low", "medium", "high", "xhigh", "max", "auto", "lo", "med", "hi":
			return strings.TrimSpace(sel[:colon]), normalizeThinkingLevel(tail)
		}
	}
	return sel, ""
}

// parseModelRoleSelector reports whether sel is a role alias (`@role`,
// `pi/role`, `default`, or `*`, with optional `:thinking` suffix) and returns
// the canonical lowercase role name.
// upstream: packages/coding-agent/src/config/model-roles.ts
func parseModelRoleSelector(sel string) (roleName string, isRole bool) {
	trimmed := strings.TrimSpace(sel)
	if trimmed == "" {
		return "", false
	}
	base, _ := splitSelectorThinking(trimmed)
	lower := strings.ToLower(base)
	switch {
	case lower == "default" || lower == "*":
		return "default", true
	case strings.HasPrefix(lower, "@"):
		name := strings.TrimSpace(strings.TrimPrefix(lower, "@"))
		if name == "" || strings.Contains(name, "/") {
			return "", false
		}
		return name, true
	case strings.HasPrefix(lower, "pi/"):
		name := strings.TrimSpace(strings.TrimPrefix(lower, "pi/"))
		if name == "" || strings.Contains(name, "/") {
			return "", false
		}
		return name, true
	default:
		return "", false
	}
}

// lookupModelInRegistry resolves (provider, id) via ModelRegistry.Find,
// falling back to a case-insensitive scan of GetAvailable().
func lookupModelInRegistry(reg sdk.ModelRegistry, provider, id string) map[string]any {
	if m, err := reg.Find(provider, id); err == nil && m != nil {
		return m
	}
	avail, err := reg.GetAvailable()
	if err != nil {
		return nil
	}
	for _, m := range avail {
		mp, _ := m["provider"].(string)
		mid, _ := m["id"].(string)
		if strings.EqualFold(mp, provider) && strings.EqualFold(mid, id) {
			return m
		}
	}
	return nil
}

// resolveModelForChildWithRole resolves candidate model selectors against
// ResolveModelRole (for `@role`, `pi/<role>`, `default`, `*`) and the host's
// ModelRegistry, returning the resolved model object, optional thinking suffix,
// and matched role name (empty for direct provider/id selectors or fallback).
func resolveModelForChildWithRole(ctx sdk.Context, candidates []string) (modelObj map[string]any, thinkingSuffix string, modelRole string) {
	var zero sdk.Context
	if ctx == zero {
		return nil, "", ""
	}
	reg := ctx.ModelRegistry()
	for _, raw := range candidates {
		sel := strings.TrimSpace(raw)
		if sel == "" {
			continue
		}
		if roleName, isRole := parseModelRoleSelector(sel); isRole {
			if ResolveModelRole == nil {
				continue
			}
			prov, id, thinking, ok := ResolveModelRole(ctx, sel)
			if ok && prov != "" && id != "" {
				if m := lookupModelInRegistry(reg, prov, id); m != nil {
					return m, normalizeThinkingLevel(thinking), roleName
				}
			}
			continue
		}
		base, suffix := splitSelectorThinking(sel)
		provider, id, hasSlash := strings.Cut(base, "/")
		if hasSlash {
			if m, err := reg.Find(provider, id); err == nil && m != nil {
				return m, suffix, ""
			}
		}
		// Fallback: scan available models by exact ID or provider/id match.
		avail, err := reg.GetAvailable()
		if err != nil {
			continue
		}
		for _, m := range avail {
			mp, _ := m["provider"].(string)
			mid, _ := m["id"].(string)
			if hasSlash && strings.EqualFold(mp, provider) && strings.EqualFold(mid, id) {
				return m, suffix, ""
			}
			if !hasSlash && strings.EqualFold(mid, base) {
				return m, suffix, ""
			}
		}
	}
	// Parent active model fallback (`task/executor.ts:5242-5244`).
	if parentQual := strings.TrimSpace(ctx.ModelQualified()); parentQual != "" {
		if prov, id, ok := strings.Cut(parentQual, "/"); ok && prov != "" && id != "" {
			if m := lookupModelInRegistry(reg, prov, id); m != nil {
				return m, "", ""
			}
		}
	}
	return nil, "", ""
}

// ResolveModelForChild resolves a list of candidate model selectors (e.g.
// `["anthropic/claude-haiku-4-5"]`, `["@smol"]`, `["@slow:high"]`,
// `["pi/task"]`, or `["default"]`) against `ResolveModelRole` and the host's
// ModelRegistry. When no candidate resolves (or `ResolveModelRole == nil` for
// role aliases), it falls back to the parent session's active model
// (`ctx.ModelQualified()` -> `ModelRegistry().Find`), matching omp's
// `modelPatternParentFallback` (`task/executor.ts:5242-5244`) rather than
// letting PiG pick the settings-file default or first available model.
// upstream: packages/coding-agent/src/task/executor.ts:4988-5015,5242-5244
func ResolveModelForChild(ctx sdk.Context, candidates []string) (modelObj map[string]any, thinkingSuffix string) {
	m, think, _ := resolveModelForChildWithRole(ctx, candidates)
	return m, think
}

// thinkingEffortOrder is omp's THINKING_EFFORTS ladder from least to most
// intensive (`packages/catalog/src/effort.ts:11-18`).
var thinkingEffortOrder = []string{"minimal", "low", "medium", "high", "xhigh", "max"}

func thinkingEffortRank(level string) int {
	for i, l := range thinkingEffortOrder {
		if l == level {
			return i
		}
	}
	return -1
}

// ClampThinkingLevelToCeiling clamps level to the highest effort at or below
// ceiling, matching omp's `clampThinkingLevelToCeiling`
// (`packages/tui/src/thinking.ts:362-384`):
//   - Empty, `"inherit"`, `"off"`, and unknown/empty ceiling keep their existing meaning.
//   - When modelObj is non-nil and explicitly declares `reasoning: false` (and no
//     `maxThinking`), returns `"off"` (non-reasoning model).
//   - When modelObj declares a `thinkingLevelMap` with null entries (unsupported levels),
//     snaps to the highest supported effort at or below ceiling (leaving level unchanged
//     if no supported level exists at or below ceiling).
//   - Levels already at or below ceiling pass through unchanged.
//
// upstream: packages/tui/src/thinking.ts:362-384, packages/coding-agent/src/task/executor.ts:5083-5093
func ClampThinkingLevelToCeiling(modelObj map[string]any, level, ceiling string) string {
	if level == "" || level == "off" || level == "inherit" {
		return level
	}
	if modelObj != nil && isNonReasoningModelMap(modelObj) {
		return "off"
	}
	normCeiling := normalizeThinkingLevel(ceiling)
	maxIdx := thinkingEffortRank(normCeiling)
	if maxIdx < 0 {
		return level
	}
	curIdx := thinkingEffortRank(level)
	if curIdx < 0 || curIdx <= maxIdx {
		return level
	}
	// Pick the highest supported effort at or below maxIdx.
	for i := maxIdx; i >= 0; i-- {
		cand := thinkingEffortOrder[i]
		if modelObj == nil || modelMapSupportsEffort(modelObj, cand) {
			return cand
		}
	}
	return level
}

func isNonReasoningModelMap(modelObj map[string]any) bool {
	if mt, ok := modelObj["maxThinking"].(string); ok && mt != "" {
		return false
	}
	if r, ok := modelObj["reasoning"].(bool); ok {
		return !r
	}
	return false
}

func modelMapSupportsEffort(modelObj map[string]any, level string) bool {
	if rawMap, ok := modelObj["thinkingLevelMap"].(map[string]any); ok {
		if v, exists := rawMap[level]; exists && v == nil {
			return false
		}
	}
	if mt, ok := modelObj["maxThinking"].(string); ok && mt != "" {
		if mtRank := thinkingEffortRank(normalizeThinkingLevel(mt)); mtRank >= 0 {
			if r := thinkingEffortRank(level); r > mtRank {
				return false
			}
		}
	}
	return true
}

// ResolveChildThinkingLevel maps omp's effort ("lo"/"med"/"hi"), explicit
// model `:level` suffix, and agent frontmatter `thinkingLevel` to PiG's
// `ThinkingLevel` option.
//
// For `"auto"`, omp resolves per turn via an LLM judge (`auto-thinking/classifier.ts`)
// and falls back to `resolveProvisionalAutoLevel(model)` (`packages/tui/src/thinking.ts:421-425`:
// `model.thinking.defaultLevel ?? Effort.High`, clamped to `<= xhigh`, and `undefined`
// for non-reasoning models). Since PiG's `ai.Model` has no `defaultLevel` metadata
// and clamps any requested level to `"off"` on non-reasoning models (`ai/model_utils.go:112-149`),
// `"auto"` maps to `"high"` rather than `""` (which in PiG defaults to `"medium"`).
// upstream: packages/coding-agent/src/task/executor.ts:5103-5107,
// packages/coding-agent/src/session/model-controls.ts:186-193,712-718,
// packages/tui/src/thinking.ts:421-425
func ResolveChildThinkingLevel(agentLevel, effortOverride, modelSuffix string) string {
	level := modelSuffix
	if effortOverride != "" {
		level = normalizeThinkingLevel(effortOverride)
	}
	if level == "" {
		level = normalizeThinkingLevel(agentLevel)
	}
	switch level {
	case "off", "minimal", "low", "medium", "high", "xhigh", "max":
		return level
	case "auto":
		return "high"
	default:
		// "inherit" / "" -> let PiG host use session default.
		return ""
	}
}

// AttributeSubagentError prefixes a failure message with `[provider/model]`
// when the model identity is known and not already named in the message.
// upstream: packages/coding-agent/src/task/error-attribution.ts:27
func AttributeSubagentError(message, provider, model, fallback string) string {
	text := strings.TrimSpace(message)
	if text == "" {
		if fallback != "" {
			text = fallback
		} else {
			text = "Subagent failed"
		}
	}
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	var identity string
	if provider != "" && model != "" {
		identity = provider + "/" + model
	} else if provider != "" {
		identity = provider
	} else {
		identity = model
	}
	if identity == "" {
		return text
	}
	if provider != "" && strings.Contains(strings.ToLower(text), strings.ToLower(provider)) {
		return text
	}
	return "[" + identity + "] " + text
}

// RunSubagent creates a child AgentSession via PiG's SDK, subscribes to its
// events to track progress/usage/output, drives its prompt (and optional steps),
// writes `<id>.md` and optional `<id>.json` sidecar artifacts, and returns the
// settled SingleResult.
// upstream: packages/coding-agent/src/task/executor.ts (runSubprocessOnThisThread)
func RunSubagent(ctx context.Context, reg *Registry, req RunRequest) SingleResult {
	startTime := time.Now()
	structured := UsesYield(req.OutputSchema)
	userPrompt := RenderSubagentUserPrompt(req.Assignment)
	// Guarantee any pre-registered or newly registered AgentRef for this run
	// never remains "running" if RunSubagent returns early (ctx.Err(), session
	// create/bind error, or panic). Normal completion sets "idle"/"parked"/"aborted"
	// before return, making this a no-op.
	if reg != nil {
		defer reg.SettleIfRunning(req.ParentSessionID, req.ID, "aborted")
	}

	if err := ctx.Err(); err != nil {
		return SingleResult{
			Index:         req.Index,
			ID:            req.ID,
			Agent:         req.Agent.Name,
			AgentSource:   req.Agent.Source,
			Task:          userPrompt,
			Assignment:    req.Assignment,
			ExitCode:      1,
			Stderr:        "Cancelled before start",
			Error:         "Cancelled before start",
			Aborted:       true,
			AbortReason:   "Cancelled before start",
			ModelOverride: req.ModelOverride,
		}
	}
	// H3: If a pre-registered ref was already aborted before RunSubagent began,
	// bail immediately instead of overwriting it back to "running".
	if reg != nil {
		if existing, ok := reg.GetInSession(req.ParentSessionID, req.ID); ok && existing != nil {
			existing.mu.Lock()
			alreadyAborted := existing.Status == "aborted"
			existing.mu.Unlock()
			if alreadyAborted {
				return SingleResult{
					Index:         req.Index,
					ID:            req.ID,
					Agent:         req.Agent.Name,
					AgentSource:   req.Agent.Source,
					Task:          userPrompt,
					Assignment:    req.Assignment,
					ExitCode:      1,
					Stderr:        "Cancelled before start",
					Error:         "Cancelled before start",
					Aborted:       true,
					AbortReason:   "Cancelled before start",
					ModelOverride: req.ModelOverride,
				}
			}
		}
	}
	// Ensure artifacts dir exists when provided.
	if req.ArtifactsDir != "" {
		_ = os.MkdirAll(req.ArtifactsDir, 0o755)
	}

	// Register the subagent immediately so `agent://` and `history://` can observe it while starting.
	parentID := req.ParentAgentID
	if parentID == "" {
		parentID = MainAgentID
	}
	taskDesc := strings.TrimSpace(req.Assignment)
	if taskDesc == "" {
		taskDesc = strings.TrimSpace(userPrompt)
	}
	ref := &AgentRef{
		ID:            req.ID,
		DisplayName:   req.Agent.Name,
		Kind:          "sub",
		ParentID:      parentID,
		Status:        "running",
		SessionID:     req.ParentSessionID,
		OutputDir:     req.ArtifactsDir,
		CreatedAt:     startTime,
		LastActive:    startTime,
		AgentType:     req.Agent.Name,
		Task:          taskDesc,
		TurnStartedAt: startTime,
		runActive:     true,
	}
	defer func() {
		ref.mu.Lock()
		ref.runActive = false
		ref.mu.Unlock()
	}()
	if req.ArtifactsDir != "" {
		ref.SessionFile = filepath.Join(req.ArtifactsDir, req.ID+".jsonl")
	}
	if reg != nil {
		reg.RecordActivity(req.ParentSessionID, surface.AgentHubActivity{
			AgentID:     req.ID,
			Kind:        "status",
			Title:       "Spawned " + req.Agent.Name,
			Detail:      OneLineLabel(taskDesc),
			Status:      "pending",
			TimestampMs: startTime.UnixMilli(),
		})
		reg.Register(ref)
	}

	// Prepare optional runtime timeout.
	runCtx := ctx
	var cancelTimeout context.CancelFunc
	var timedOut bool
	if req.MaxRuntimeMs > 0 {
		runCtx, cancelTimeout = context.WithTimeout(ctx, time.Duration(req.MaxRuntimeMs)*time.Millisecond)
		defer cancelTimeout()
	}

	// Compute child tools first so IRC can be gated on whether the child has `write`
	// (`task/executor.ts:4832-4835`: `toolNames === undefined || toolNames.includes("write")`).
	childDepth := req.TaskDepth + 1
	toolsList, excludeList := ResolveChildToolNames(req.Agent, childDepth, req.MaxDepth, structured)
	effectiveIRC := req.IRCEnabled && (toolsList == nil || slices.Contains(toolsList, "write"))
	reqForPrompt := req
	reqForPrompt.IRCEnabled = effectiveIRC

	roster := PeerRoster{}
	if reg != nil && effectiveIRC {
		roster = reg.CollectPeerRosterForSession(req.ParentSessionID, req.ID)
	}
	subagentSysPrompt := RenderSubagentSystemPrompt(reqForPrompt, roster, structured)

	modelCandidates := req.ModelOverride
	if len(modelCandidates) == 0 {
		modelCandidates = req.Agent.Model
	}
	modelObj, modelThinkSuffix, resolvedModelRole := resolveModelForChildWithRole(req.SDK, modelCandidates)
	thinkingLevel := ClampThinkingLevelToCeiling(modelObj, ResolveChildThinkingLevel(req.Agent.ThinkingLevel, req.Effort, modelThinkSuffix), req.MaxEffort)
	ref.mu.Lock()
	ref.ModelRole = resolvedModelRole
	ref.ThinkingLevel = thinkingLevel
	if modelObj != nil {
		prov, _ := modelObj["provider"].(string)
		mid, _ := modelObj["id"].(string)
		if prov != "" && mid != "" {
			ref.Model = prov + "/" + mid
		} else if mid != "" {
			ref.Model = mid
		}
		if cw, ok := asInt(modelObj["contextWindow"]); ok && cw > 0 {
			ref.ContextWindow = cw
		}
	}
	ref.mu.Unlock()

	softBudgetNotice := true
	if req.SoftBudgetNotice != nil {
		softBudgetNotice = *req.SoftBudgetNotice
	}
	budgetStopThreshold := 0
	if req.SoftBudget > 0 {
		budgetStopThreshold = int(math.Ceil(float64(req.SoftBudget) * 1.5))
	}

	// Shared monitor state guarded by mu.
	var (
		mu       sync.Mutex
		progress = AgentProgress{
			Index:         req.Index,
			ID:            req.ID,
			Agent:         req.Agent.Name,
			AgentSource:   req.Agent.Source,
			Status:        "running",
			Task:          userPrompt,
			Assignment:    req.Assignment,
			RecentTools:   []RecentTool{},
			RecentOutput:  []string{},
			ModelOverride: req.ModelOverride,
			ModelRole:     resolvedModelRole,
		}
		usageTotals         Usage
		hasUsage            bool
		yieldItems          []YieldItem
		yieldTerminal       bool
		outputChunks        []string
		finalOutputChunks   []string
		lastReportText      string
		lastSalvageText     string
		lastStopReason      string
		lastErrorMsg        string
		lastProvider        string
		lastModelID         string
		budgetNoticeSent    bool
		budgetStopRequested bool
		budgetLimitExceeded bool
		budgetStopDone      = make(chan struct{})
		budgetStopOnce      sync.Once
		activeTools         = map[string]struct {
			tool, args, intent string
			startMs            int64
		}{}
		transcriptMsgs []map[string]any
	)
	emitProgress := func() {
		if req.OnProgress == nil {
			return
		}
		mu.Lock()
		progress.DurationMs = time.Since(startTime).Milliseconds()
		snap := progress.clone()
		mu.Unlock()
		req.OnProgress(snap)
	}

	// Prepare CustomTools: copy caller custom tools + inject `yield` when structured.
	var customTools []sdk.ToolDefinition
	if len(req.CustomTools) > 0 {
		customTools = append(customTools, req.CustomTools...)
	}
	var childSession *sdk.AgentSession
	var yieldAbortOnce sync.Once
	if structured {
		yieldTool := intent.Tool(schemaorder.Apply(BuildYieldToolDefinition(req.OutputSchema, req.WorkPoolItems, func(item YieldItem, terminal bool) {
			mu.Lock()
			yieldItems = append(yieldItems, item)
			if progress.ExtractedToolData == nil {
				progress.ExtractedToolData = map[string][]any{}
			}
			progress.ExtractedToolData["yield"] = append(progress.ExtractedToolData["yield"], item)
			if terminal {
				yieldTerminal = true
			}
			mu.Unlock()
			if terminal {
				yieldAbortOnce.Do(func() {
					if childSession != nil {
						go func() { _ = childSession.Abort() }()
					}
				})
			}
		})))
		customTools = append(customTools, yieldTool)
	}

	// Load ONLY this Piglet's fused `oh-my-pig` extension into the child session
	// (`NoExtensions: true` drops ambient user/workspace extensions; `AdditionalExtensionPaths`
	// keeps `builtin:oh-my-pig` per PiG `coding/resource_loader_extension_files.go:23-26,41`).
	// `NoTools: "builtin"` suppresses PiG's host built-ins (matching `piglet.yaml`'s `tools: []`)
	// so the child sees omp's extension tools (`read`, `edit`, `write`, `bash`, `grep`, `glob`,
	// `find`, `eval`, ...) filtered by `Tools` and `ExcludeTools`, plus `CustomTools` (`yield`).
	// `NoContextFiles: true` matches omp `task/executor.ts:5277` (`excludeAgentsMd: true`).
	opts := sdk.CreateAgentSessionOptions{
		Cwd:           req.Cwd,
		AgentDir:      req.AgentDir,
		Model:         modelObj,
		ThinkingLevel: thinkingLevel,
		Tools:         toolsList,
		ExcludeTools:  excludeList,
		NoTools:       "builtin",
		CustomTools:   customTools,
		InMemory:      true,
		Resources: sdk.ResourceOptions{
			NoExtensions:             true,
			AdditionalExtensionPaths: []string{FusedExtensionPath},
			NoPromptTemplates:        true,
			NoThemes:                 true,
			NoContextFiles:           true,
			AppendSystemPrompt:       []string{subagentSysPrompt},
		},
	}

	session, err := req.SDK.CreateAgentSession(opts)
	if err != nil {
		msg := fmt.Sprintf("failed to create child session: %v", err)
		if reg != nil {
			reg.SetStatusInSession(req.ParentSessionID, req.ID, "aborted")
		}
		return SingleResult{
			Index:         req.Index,
			ID:            req.ID,
			Agent:         req.Agent.Name,
			AgentSource:   req.Agent.Source,
			Task:          userPrompt,
			Assignment:    req.Assignment,
			ExitCode:      1,
			Stderr:        msg,
			Error:         msg,
			DurationMs:    time.Since(startTime).Milliseconds(),
			ModelOverride: req.ModelOverride,
		}
	}
	childSession = session

	// Record child → parent session lineage BEFORE calling BindExtensions/Prompt
	// so the child extension's `session_start`, `before_agent_start`, and tool
	// handlers reliably observe IsSubagent(ctx), ParentSession(ctx), recursion
	// depth, spawn policy, and the parent's local:// root.
	if reg != nil && session.SessionID != "" {
		reg.RegisterChildSession(session.SessionID, buildChildSessionMeta(req, childDepth, parentID, toolsList))
	}
	// `session_start` before the first Prompt (sdk/child_session.go:560).
	if err := session.BindExtensions(); err != nil {
		session.Dispose()
		if session.SessionID != "" {
			lifecycle.End(session.SessionID)
		}
		msg := fmt.Sprintf("failed to bind child extensions: %v", err)
		if reg != nil {
			reg.SetStatusInSession(req.ParentSessionID, req.ID, "aborted")
		}
		return SingleResult{
			Index:         req.Index,
			ID:            req.ID,
			Agent:         req.Agent.Name,
			AgentSource:   req.Agent.Source,
			Task:          userPrompt,
			Assignment:    req.Assignment,
			ExitCode:      1,
			Stderr:        msg,
			Error:         msg,
			DurationMs:    time.Since(startTime).Milliseconds(),
			ModelOverride: req.ModelOverride,
		}
	}
	mu.Lock()
	if session.Model != nil {
		prov, _ := session.Model["provider"].(string)
		mid, _ := session.Model["id"].(string)
		if cw, ok := asInt(session.Model["contextWindow"]); ok && cw > 0 {
			progress.ContextWindow = cw
		}
		if prov != "" && mid != "" {
			progress.ResolvedModelIdentity = prov + "/" + mid
			progress.ResolvedModel = progress.ResolvedModelIdentity
			if session.ThinkingLevel != "" && session.ThinkingLevel != "off" {
				progress.ResolvedThinkingLevel = session.ThinkingLevel
				progress.ResolvedModel = progress.ResolvedModelIdentity + ":" + session.ThinkingLevel
			}
		}
		lastProvider = prov
		lastModelID = mid
	}
	mu.Unlock()

	// Attach live session to registry and drain any IRC messages parked while starting.
	var pendingIRC []IRCMessage
	mu.Lock()
	resolvedModelIDInit := progress.ResolvedModelIdentity
	resolvedThinkInit := progress.ResolvedThinkingLevel
	resolvedCWInit := progress.ContextWindow
	mu.Unlock()
	ref.mu.Lock()
	stillReg := true
	if reg != nil {
		cur, ok := reg.GetInSession(req.ParentSessionID, req.ID)
		stillReg = ok && cur == ref
	}
	if ref.Status == "aborted" || ctx.Err() != nil || !stillReg {
		ref.mu.Unlock()
		session.Dispose()
		if session.SessionID != "" {
			lifecycle.End(session.SessionID)
			if reg != nil {
				reg.UnregisterChildSession(session.SessionID)
			}
		}
		return SingleResult{
			Index:         req.Index,
			ID:            req.ID,
			Agent:         req.Agent.Name,
			AgentSource:   req.Agent.Source,
			Task:          userPrompt,
			Assignment:    req.Assignment,
			ExitCode:      1,
			Stderr:        "Cancelled during start",
			Error:         "Cancelled during start",
			Aborted:       true,
			AbortReason:   "Cancelled during start",
			DurationMs:    time.Since(startTime).Milliseconds(),
			ModelOverride: req.ModelOverride,
		}
	}
	ref.Session = session
	ref.ChildSessionID = session.SessionID
	if session.SessionFile != "" {
		ref.SessionFile = session.SessionFile
	}
	if resolvedModelIDInit != "" {
		ref.Model = resolvedModelIDInit
	}
	if resolvedThinkInit != "" {
		ref.ThinkingLevel = resolvedThinkInit
	}
	if resolvedCWInit > 0 {
		ref.ContextWindow = resolvedCWInit
	}
	pendingIRC = ref.PendingIRC
	ref.PendingIRC = nil
	ref.mu.Unlock()
	if reg != nil {
		reg.notify()
	}
	// Subscribe to child session events to update progress, usage, and final output.
	unsub := session.Subscribe(func(event map[string]any) {
		evType, _ := event["type"].(string)
		nowMs := time.Now().UnixMilli()
		shouldEmit := false

		mu.Lock()
		switch evType {
		case "auto_retry_start":
			attempt, _ := asInt(event["attempt"])
			maxAttempts, _ := asInt(event["maxAttempts"])
			delayMs, _ := asInt(event["delayMs"])
			errMsgStr, _ := event["errorMessage"].(string)
			progress.RetryState = &AgentRetryState{
				Attempt:      attempt,
				MaxAttempts:  maxAttempts,
				DelayMs:      int64(delayMs),
				ErrorMessage: errMsgStr,
				StartedAtMs:  nowMs,
			}
			progress.RetryFailure = nil
			shouldEmit = true

		case "auto_retry_end":
			attempt, _ := asInt(event["attempt"])
			if progress.RetryState != nil && progress.RetryState.Attempt > 0 {
				attempt = progress.RetryState.Attempt
			}
			progress.RetryState = nil
			success, _ := event["success"].(bool)
			if !success {
				finalErr, _ := event["finalError"].(string)
				if finalErr == "" {
					finalErr = "Auto-retry failed"
				}
				progress.RetryFailure = &AgentRetryFailure{
					Attempt:      attempt,
					ErrorMessage: finalErr,
				}
			}
			shouldEmit = true

		case "tool_execution_update":
			if toolName, _ := event["toolName"].(string); toolName == "task" {
				if partial, ok := event["partialResult"].(map[string]any); ok {
					if rawDet, ok := partial["details"]; ok && rawDet != nil {
						if rawBytes, err := json.Marshal(rawDet); err == nil {
							var nested TaskToolDetails
							if err := json.Unmarshal(rawBytes, &nested); err == nil {
								progress.InflightTaskDetails = &nested
								shouldEmit = true
							}
						}
					}
				}
			}

		case "tool_execution_start":
			toolName, _ := event["toolName"].(string)
			callID, _ := event["toolCallId"].(string)
			argsMap, _ := event["args"].(map[string]any)
			argPreview := summarizePrimaryArg(toolName, argsMap)
			intentStr, _ := event["intent"].(string)
			if intentStr == "" && argsMap != nil {
				intentStr, _ = argsMap["i"].(string)
			}
			progress.ToolCount++
			progress.CurrentTool = toolName
			progress.CurrentToolArgs = argPreview
			progress.CurrentToolIntent = intentStr
			progress.CurrentToolStartMs = nowMs
			progress.Streaming = nil
			if intentStr != "" {
				progress.LastIntent = intentStr
			}
			activeTools[callID] = struct {
				tool, args, intent string
				startMs            int64
			}{tool: toolName, args: argPreview, intent: intentStr, startMs: nowMs}
			shouldEmit = true
			if reg != nil {
				actLabel := intentStr
				if actLabel == "" && toolName != "" {
					if argPreview != "" {
						actLabel = toolName + "(" + argPreview + ")"
					} else {
						actLabel = toolName
					}
				}
				if actLabel != "" {
					reg.SetActivityInSession(req.ParentSessionID, req.ID, actLabel)
				}
				ref.mu.Lock()
				ref.Tools = progress.ToolCount
				ref.DurationMs = time.Since(startTime).Milliseconds()
				ref.LastActive = time.Now()
				ref.mu.Unlock()
				detail := intentStr
				if detail == "" {
					detail = argPreview
				}
				reg.RecordActivity(req.ParentSessionID, surface.AgentHubActivity{
					AgentID:     req.ID,
					Kind:        "tool",
					Title:       toolName,
					Detail:      OneLineLabel(detail),
					Status:      "pending",
					TimestampMs: nowMs,
				})
			}

		case "tool_execution_end":
			callID, _ := event["toolCallId"].(string)
			isErr, _ := event["isError"].(bool)
			toolName, _ := event["toolName"].(string)
			if toolName == "task" {
				progress.InflightTaskDetails = nil
			}
			var finishedIntent string
			if started, ok := activeTools[callID]; ok {
				delete(activeTools, callID)
				if started.tool == "task" {
					progress.InflightTaskDetails = nil
				}
				finishedIntent = started.intent
				if toolName == "" {
					toolName = started.tool
				}
				progress.RecentTools = append([]RecentTool{{
					Tool:    started.tool,
					Args:    started.args,
					Intent:  started.intent,
					IsError: isErr,
					EndMs:   nowMs,
				}}, progress.RecentTools...)
				if len(progress.RecentTools) > 5 {
					progress.RecentTools = progress.RecentTools[:5]
				}
			}
			if req.OnToolResult != nil && toolName != "" {
				resText := extractToolExecutionEndText(event["result"])
				onToolResult := req.OnToolResult
				tName, tIntent := toolName, finishedIntent
				mu.Unlock()
				onToolResult(tName, tIntent, resText)
				mu.Lock()
			}
			if len(activeTools) == 0 {
				progress.CurrentTool = ""
				progress.CurrentToolArgs = ""
				progress.CurrentToolIntent = ""
				progress.CurrentToolStartMs = 0
			} else {
				for _, rem := range activeTools {
					progress.CurrentTool = rem.tool
					progress.CurrentToolArgs = rem.args
					progress.CurrentToolIntent = rem.intent
					progress.CurrentToolStartMs = rem.startMs
					break
				}
			}
			if reg != nil && toolName != "" {
				ref.mu.Lock()
				ref.DurationMs = time.Since(startTime).Milliseconds()
				ref.LastActive = time.Now()
				ref.mu.Unlock()
				st := "ok"
				if isErr {
					st = "error"
				}
				reg.RecordActivity(req.ParentSessionID, surface.AgentHubActivity{
					AgentID:     req.ID,
					Kind:        "tool",
					Title:       toolName,
					Detail:      OneLineLabel(finishedIntent),
					Status:      st,
					TimestampMs: nowMs,
				})
			}
			shouldEmit = true
		case "message_update":
			if ame, ok := event["assistantMessageEvent"].(map[string]any); ok {
				subType, _ := ame["type"].(string)
				switch subType {
				case "text_delta":
					if delta, ok := ame["delta"].(string); ok && delta != "" {
						progress.Streaming = &AgentStreaming{Kind: "text"}
						progress.RecentOutput = appendRecentOutput(progress.RecentOutput, delta)
					}
				case "text_start":
					progress.Streaming = &AgentStreaming{Kind: "text"}
				case "thinking_start", "thinking_delta":
					progress.Streaming = &AgentStreaming{Kind: "thinking"}
				}
			}
		case "model_select":
			if mMap, ok := event["model"].(map[string]any); ok {
				prov, _ := mMap["provider"].(string)
				mid, _ := mMap["id"].(string)
				if prov != "" {
					lastProvider = prov
				}
				if mid != "" {
					lastModelID = mid
				}
				if cw, ok := asInt(mMap["contextWindow"]); ok && cw > 0 {
					progress.ContextWindow = cw
				}
				if prov != "" && mid != "" {
					progress.ResolvedModelIdentity = prov + "/" + mid
					progress.ResolvedModel = progress.ResolvedModelIdentity
				}
				ref.mu.Lock()
				if progress.ResolvedModelIdentity != "" {
					ref.Model = progress.ResolvedModelIdentity
				} else if mid != "" {
					ref.Model = mid
				}
				if progress.ContextWindow > 0 {
					ref.ContextWindow = progress.ContextWindow
				}
				ref.mu.Unlock()
				shouldEmit = true
			}

		case "message_end":
			msg, _ := event["message"].(map[string]any)
			if msg != nil {
				transcriptMsgs = append(transcriptMsgs, msg)
				role, _ := msg["role"].(string)
				if role == "assistant" {
					progress.Streaming = nil
					progress.Requests++
					if sr, ok := msg["stopReason"].(string); ok && sr != "" {
						lastStopReason = sr
					}
					if em, ok := msg["errorMessage"].(string); ok && em != "" {
						lastErrorMsg = em
					}
					if p, ok := msg["provider"].(string); ok && p != "" {
						lastProvider = p
					}
					if m, ok := msg["model"].(string); ok && m != "" {
						lastModelID = m
					}
					turnTexts, hasNonYieldCall := extractAssistantTurnText(msg)
					if len(turnTexts) > 0 {
						joined := strings.Join(turnTexts, "\n")
						outputChunks = append(outputChunks, joined)
						if strings.TrimSpace(joined) != "" {
							lastSalvageText = joined
						}
						if hasNonYieldCall {
							lastReportText = ""
						} else if strings.TrimSpace(joined) != "" {
							lastReportText = joined
						}
					} else if hasNonYieldCall {
						lastReportText = ""
					}

					if uMap, ok := msg["usage"].(map[string]any); ok {
						u := parseUsageMap(uMap)
						usageTotals.add(u)
						hasUsage = true
						progress.Cost = usageTotals.Cost.Total
						progress.Tokens += u.Input + u.Output + u.CacheWrite
						if ctxTok, ok := asInt(uMap["contextTokens"]); ok && ctxTok > 0 {
							progress.ContextTokens = ctxTok
						} else if u.TotalTokens > 0 {
							progress.ContextTokens = u.TotalTokens
						}
					}
					// Soft request budget guard (`task/executor.ts:1999-2030`):
					//   1. At softBudget: steer with omp's buildBudgetNotice text.
					//   2. At ceil(1.5*softBudget): abort the free-running turn and drive
					//      one forced final wrap-up prompt (`subagent-yield-reminder.md`).
					//   3. At ceil(1.5*softBudget) + BUDGET_STOP_GRACE_REQUESTS (5): hard-abort.
					if req.SoftBudget > 0 {
						if budgetStopRequested {
							if !budgetLimitExceeded && progress.Requests >= budgetStopThreshold+BudgetStopGraceRequests {
								budgetLimitExceeded = true
								go func() { _ = session.Abort() }()
							}
						} else if progress.Requests >= budgetStopThreshold {
							budgetStopRequested = true
							go func() {
								_ = session.Abort()
								budgetStopOnce.Do(func() { close(budgetStopDone) })
							}()
						} else if softBudgetNotice && !budgetNoticeSent && progress.Requests >= req.SoftBudget {
							budgetNoticeSent = true
							notice := BuildBudgetNotice(progress.Requests, req.SoftBudget)
							go func() { _ = session.Steer(notice) }()
						}
					}
					if reg != nil {
						ref.mu.Lock()
						ref.Requests = progress.Requests
						ref.Tokens = progress.Tokens
						ref.Cost = progress.Cost
						ref.ContextTokens = progress.ContextTokens
						if progress.ContextWindow > 0 {
							ref.ContextWindow = progress.ContextWindow
						}
						if lastProvider != "" && lastModelID != "" {
							ref.Model = lastProvider + "/" + lastModelID
						} else if lastModelID != "" && ref.Model == "" {
							ref.Model = lastModelID
						}
						ref.DurationMs = time.Since(startTime).Milliseconds()
						ref.LastActive = time.Now()
						ref.mu.Unlock()
						respDetail := OneLineLabel(lastReportText)
						if respDetail == "" {
							respDetail = OneLineLabel(lastSalvageText)
						}
						respStatus := "ok"
						if lastStopReason == "error" {
							respStatus = "error"
						} else if lastStopReason == "aborted" {
							respStatus = "aborted"
						}
						reg.RecordActivity(req.ParentSessionID, surface.AgentHubActivity{
							AgentID:     req.ID,
							Kind:        "response",
							Title:       "Assistant response",
							Detail:      respDetail,
							Status:      respStatus,
							TimestampMs: nowMs,
						})
					}
					shouldEmit = true
				}
			}
		case "agent_end":
			progress.Streaming = nil
			if msgs, ok := event["messages"].([]any); ok {
				for _, rawMsg := range msgs {
					m, ok := rawMsg.(map[string]any)
					if !ok {
						continue
					}
					if role, _ := m["role"].(string); role != "assistant" {
						continue
					}
					if sr, ok := m["stopReason"].(string); ok && sr != "" {
						lastStopReason = sr
					}
					if em, ok := m["errorMessage"].(string); ok && em != "" {
						lastErrorMsg = em
					}
					texts, _ := extractAssistantTurnText(m)
					for _, t := range texts {
						if t != "" {
							finalOutputChunks = append(finalOutputChunks, t)
						}
					}
				}
			}
			shouldEmit = true
		}
		mu.Unlock()

		if shouldEmit {
			emitProgress()
			if reg != nil && (evType == "tool_execution_start" || evType == "tool_execution_end" || evType == "message_end" || evType == "model_select" || evType == "agent_end") {
				reg.notifyHub(false)
			}
		}
	})

	emitProgress()

	// Deliver any IRC messages parked while starting before prompting.
	for _, pending := range pendingIRC {
		fromParent := pending.From == parentID && !pending.Broadcast
		if fromParent {
			_ = session.Steer(FormatParentIRCSteer(pending.From, pending.Body))
		} else {
			_ = session.Steer(FormatIncomingIRCWithOptions(pending, false, !pending.Broadcast && !pending.Progress))
		}
	}

	// Run prompt(s).
	expand := false
	promptOpts := sdk.PromptOptions{
		ExpandPromptTemplates: &expand,
		Source:                "extension",
	}

	var promptErr error
	if len(req.Steps) > 0 {
		for idx, step := range req.Steps {
			if runCtx.Err() != nil {
				break
			}
			promptText := RenderSubagentUserPrompt(step)
			if idx > 0 {
				promptText = step
			}
			promptErr = session.Prompt(runCtx, promptText, promptOpts)
			if promptErr != nil {
				break
			}
			mu.Lock()
			failedStep := lastStopReason == "error" || lastStopReason == "aborted" || budgetStopRequested
			mu.Unlock()
			if failedStep {
				break
			}
		}
	} else {
		promptErr = session.Prompt(runCtx, userPrompt, promptOpts)
	}

	// If soft request budget triggered a 1.5x force-stop, wait for the stop abort
	// to settle and drive one forced wrap-up turn (`task/executor.ts:2876-2921`).
	mu.Lock()
	needBudgetWrap := budgetStopRequested && !yieldTerminal && !budgetLimitExceeded
	mu.Unlock()
	if needBudgetWrap && runCtx.Err() == nil {
		select {
		case <-budgetStopDone:
		case <-runCtx.Done():
		}
		if runCtx.Err() == nil {
			mu.Lock()
			lastStopReason = ""
			mu.Unlock()
			reminder := RenderBudgetStopReminder(structured)
			promptErr = session.Prompt(runCtx, reminder, promptOpts)
		}
	}
	// Capture messages snapshot before cleanup/disposal.
	if liveMsgs, err := session.Messages(); err == nil && len(liveMsgs) > 0 {
		ref.mu.Lock()
		ref.Messages = liveMsgs
		ref.mu.Unlock()
	} else {
		mu.Lock()
		snap := append([]map[string]any(nil), transcriptMsgs...)
		mu.Unlock()
		ref.mu.Lock()
		ref.Messages = snap
		ref.mu.Unlock()
	}

	unsub()

	// Classify outcome.
	if req.MaxRuntimeMs > 0 && errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		timedOut = true
	}
	callerAborted := ctx.Err() != nil

	mu.Lock()
	exitCode := 0
	var errMsg string
	aborted := false
	var abortReason string

	deliveredByBudgetWrap := false
	if structured {
		deliveredByBudgetWrap = yieldTerminal
	} else {
		deliveredByBudgetWrap = lastStopReason == "stop" && strings.TrimSpace(lastReportText) != ""
	}

	if timedOut {
		aborted = true
		exitCode = 1
		abortReason = fmt.Sprintf("Subagent runtime limit exceeded (task.maxRuntimeMs=%d)", req.MaxRuntimeMs)
		errMsg = abortReason
	} else if budgetLimitExceeded {
		aborted = true
		exitCode = 1
		abortReason = fmt.Sprintf("Soft request budget exceeded (%d requests; budget %d) — agent did not yield when force-stopped", progress.Requests, req.SoftBudget)
		errMsg = abortReason
	} else if budgetStopRequested && !deliveredByBudgetWrap {
		aborted = true
		exitCode = 1
		abortReason = fmt.Sprintf("Soft request budget exceeded (%d requests; budget %d)", progress.Requests, req.SoftBudget)
		errMsg = abortReason
	} else if callerAborted && !yieldTerminal {
		aborted = true
		exitCode = 1
		abortReason = "Cancelled by caller"
		errMsg = abortReason
	} else if promptErr != nil && !yieldTerminal {
		exitCode = 1
		errMsg = promptErr.Error()
	} else if lastStopReason == "error" && !yieldTerminal {
		exitCode = 1
		errMsg = AttributeSubagentError(lastErrorMsg, lastProvider, lastModelID, "Subagent failed")
	} else if lastStopReason == "aborted" && !yieldTerminal {
		aborted = true
		exitCode = 1
		if lastErrorMsg != "" {
			abortReason = lastErrorMsg
		} else {
			abortReason = "Aborted"
		}
		errMsg = abortReason
	}
	var rawOutput string
	if structured {
		if len(finalOutputChunks) > 0 {
			rawOutput = strings.Join(finalOutputChunks, "")
		} else {
			rawOutput = strings.Join(outputChunks, "")
		}
	} else {
		rawOutput = lastReportText
		if rawOutput == "" && len(finalOutputChunks) > 0 {
			rawOutput = finalOutputChunks[len(finalOutputChunks)-1]
		}
		if rawOutput == "" && len(outputChunks) > 0 {
			rawOutput = outputChunks[len(outputChunks)-1]
		}
	}

	capturedYields := append([]YieldItem(nil), yieldItems...)
	salvage := lastSalvageText
	reqCount := progress.Requests
	tokCount := progress.Tokens
	ctxTokens := progress.ContextTokens
	ctxWindow := progress.ContextWindow
	resolvedModel := progress.ResolvedModel
	resolvedModelID := progress.ResolvedModelIdentity
	resolvedThink := progress.ResolvedThinkingLevel
	var retryFailureCopy *AgentRetryFailure
	if progress.RetryFailure != nil {
		rf := *progress.RetryFailure
		retryFailureCopy = &rf
	}
	lastIntent := progress.LastIntent
	extractedData := progress.ExtractedToolData
	usageCopy := usageTotals
	hasUsageCopy := hasUsage
	mu.Unlock()

	finalized := FinalizeSubprocessOutput(
		rawOutput,
		exitCode,
		errMsg,
		aborted,
		callerAborted,
		capturedYields,
		req.OutputSchema,
		req.SchemaMode,
		req.SchemaSource,
	)

	rawOutput = finalized.RawOutput
	exitCode = finalized.ExitCode
	stderr := finalized.Stderr

	// Salvage last assistant activity on abort when no completed output exists.
	if (aborted || callerAborted || timedOut) && strings.TrimSpace(rawOutput) == "" && salvage != "" {
		rawOutput = fmt.Sprintf("[cancelled after %d req, %d tok — last activity: %q]", reqCount, tokCount, OneLineLabel(salvage))
	}

	wasAborted := timedOut || aborted || finalized.AbortedViaYield || (!finalized.HasYield && callerAborted)
	if wasAborted && abortReason == "" {
		if finalized.AbortedViaYield {
			abortReason = stderr
		} else {
			abortReason = "Aborted"
		}
	}

	// Truncate output to MaxOutputBytes / MaxOutputLines.
	truncatedOutput, truncated := truncateTailOutput(rawOutput, MaxOutputBytes, MaxOutputLines)

	// Persist `<id>.md` and optional `<id>.json` sidecar + `<id>.jsonl` transcript if artifactsDir set.
	var outputPath string
	var outputMeta *OutputMeta
	if req.ArtifactsDir != "" {
		mdPath := filepath.Join(req.ArtifactsDir, req.ID+".md")
		if err := os.WriteFile(mdPath, []byte(rawOutput), 0o644); err == nil {
			outputPath = mdPath
			outputMeta = &OutputMeta{
				LineCount: strings.Count(rawOutput, "\n") + 1,
				CharCount: len([]rune(rawOutput)),
			}
			ref.mu.Lock()
			ref.OutputPath = mdPath
			ref.mu.Unlock()
		}
		sidecarPath := filepath.Join(req.ArtifactsDir, req.ID+".json")
		if outputPath != "" && finalized.StructuredOutput != nil && finalized.StructuredOutput.HasData {
			if serialized, err := json.MarshalIndent(finalized.StructuredOutput.Data, "", "  "); err == nil {
				_ = os.WriteFile(sidecarPath, append(serialized, '\n'), 0o644)
			} else {
				_ = os.Remove(sidecarPath)
			}
		} else if outputPath != "" {
			_ = os.Remove(sidecarPath)
		}
		// Persist a JSONL transcript snapshot when the host didn't write one (in-memory child).
		persistTranscriptSnapshot(ref)
	}

	durationMs := time.Since(startTime).Milliseconds()
	// Lifecycle finalization: keep idle live session when KeepAlive & not aborted; otherwise dispose.
	if req.KeepAlive && !wasAborted {
		ref.mu.Lock()
		ref.Status = "idle"
		ref.Activity = ""
		ref.TurnStartedAt = time.Time{}
		ref.DurationMs = durationMs
		ref.Requests = reqCount
		ref.Tokens = tokCount
		ref.Cost = usageCopy.Cost.Total
		ref.ContextTokens = ctxTokens
		if ctxWindow > 0 {
			ref.ContextWindow = ctxWindow
		}
		if resolvedModelID != "" {
			ref.Model = resolvedModelID
		}
		if resolvedThink != "" {
			ref.ThinkingLevel = resolvedThink
		}
		ref.LastActive = time.Now()
		ref.OnWake = makeMonitoredWakeHandler(reg, ref, req, structured, softBudgetNotice)
		if strings.TrimSpace(req.Worktree) == "" {
			ref.OnRevive = makeReviveHandler(reg, ref, req, structured, softBudgetNotice)
		}
		ref.runActive = false
		ref.mu.Unlock()
		if reg != nil {
			reg.RecordActivity(req.ParentSessionID, surface.AgentHubActivity{
				AgentID: req.ID,
				Kind:    "status",
				Title:   "Status: idle",
				Status:  "ok",
			})
			reg.ArmIdleParkTimer(ref, req.AgentIdleTTLMs)
			reg.notify()
		}
	} else {
		finalStatus := "parked"
		if wasAborted {
			finalStatus = "aborted"
		}
		ref.mu.Lock()
		if ref.Session == session {
			ref.Session = nil
		}
		ref.OnWake = nil
		if strings.TrimSpace(req.Worktree) == "" {
			ref.OnRevive = makeReviveHandler(reg, ref, req, structured, softBudgetNotice)
		}
		ref.runActive = false
		ref.Status = finalStatus
		ref.TurnStartedAt = time.Time{}
		ref.DurationMs = durationMs
		ref.Requests = reqCount
		ref.Tokens = tokCount
		ref.Cost = usageCopy.Cost.Total
		ref.ContextTokens = ctxTokens
		if ctxWindow > 0 {
			ref.ContextWindow = ctxWindow
		}
		if resolvedModelID != "" {
			ref.Model = resolvedModelID
		}
		if resolvedThink != "" {
			ref.ThinkingLevel = resolvedThink
		}
		ref.LastActive = time.Now()
		ref.mu.Unlock()
		session.Dispose()
		if session.SessionID != "" {
			lifecycle.End(session.SessionID)
		}
		if reg != nil {
			reg.RecordActivity(req.ParentSessionID, surface.AgentHubActivity{
				AgentID: req.ID,
				Kind:    "status",
				Title:   "Status: " + finalStatus,
				Status:  activityStatusForAgentStatus(finalStatus),
			})
			reg.notify()
		}
	}
	mu.Lock()
	if wasAborted {
		progress.Status = "aborted"
	} else if exitCode == 0 {
		progress.Status = "completed"
	} else {
		progress.Status = "failed"
	}
	progress.DurationMs = durationMs
	mu.Unlock()
	emitProgress()

	res := SingleResult{
		Index:                 req.Index,
		ID:                    req.ID,
		Agent:                 req.Agent.Name,
		AgentSource:           req.Agent.Source,
		Task:                  userPrompt,
		Assignment:            req.Assignment,
		LastIntent:            lastIntent,
		ExitCode:              exitCode,
		Output:                truncatedOutput,
		Stderr:                stderr,
		Truncated:             truncated,
		StructuredOutput:      finalized.StructuredOutput,
		DurationMs:            durationMs,
		Tokens:                tokCount,
		Requests:              reqCount,
		ContextTokens:         ctxTokens,
		ContextWindow:         ctxWindow,
		ModelOverride:         req.ModelOverride,
		ModelRole:             resolvedModelRole,
		ResolvedModel:         resolvedModel,
		ResolvedModelIdentity: resolvedModelID,
		ResolvedThinkingLevel: resolvedThink,
		RetryFailure:          retryFailureCopy,
		Aborted:               wasAborted,
		AbortReason:           abortReason,
		OutputPath:            outputPath,
		ExtractedToolData:     extractedData,
		OutputMeta:            outputMeta,
	}
	if exitCode != 0 && !wasAborted {
		res.Error = stderr
	}
	if hasUsageCopy {
		res.Usage = &usageCopy
	}
	return res
}

// buildChildSessionMeta builds the ChildSessionMeta shared by RunSubagent and
// makeReviveHandler so revived sessions preserve ParentLocalRoot and policy.
func buildChildSessionMeta(req RunRequest, childDepth int, parentID string, toolsList []string) ChildSessionMeta {
	effMaxDepth := EffectiveMaxRecursionDepth(req.MaxDepth)
	childSpawns := ""
	if effMaxDepth < 0 || childDepth < effMaxDepth {
		childSpawns = req.Agent.Spawns.String()
	}
	localRoot := req.ParentLocalRoot
	if localRoot == "" {
		if r, err := urls.LocalRoot(req.SDK); err == nil && r != "" {
			localRoot = r
		} else if req.ArtifactsDir != "" {
			localRoot = filepath.Join(req.ArtifactsDir, "local")
		}
	}
	var sessionTreeArtDir string
	if req.SDK != (sdk.Context{}) {
		sessionTreeArtDir = SessionTreeArtifactsDir(req.SDK)
	}
	return ChildSessionMeta{
		ParentSessionID: req.ParentSessionID,
		LocalRoot:       localRoot,
		Depth:           childDepth,
		Spawns:          childSpawns,
		AgentID:         req.ID,
		ParentAgentID:   parentID,
		ArtifactsDir:    sessionTreeArtDir,
		AgentName:       req.Agent.Name,
		ExplicitTools:   toolsList,
	}
}

// buildSessionEntriesFromMessages converts retained message maps into PiG
// session JSONL entries suitable for sdk.SessionManagerInMemory.
func buildSessionEntriesFromMessages(msgs []map[string]any) []map[string]any {
	if len(msgs) == 0 {
		return nil
	}
	ts := time.Now().UTC().Format(time.RFC3339)
	entries := make([]map[string]any, 0, len(msgs))
	for i, m := range msgs {
		if m == nil {
			continue
		}
		var parent any
		if i > 0 {
			parent = fmt.Sprintf("m%d", i)
		}
		entries = append(entries, map[string]any{
			"type":      "message",
			"id":        fmt.Sprintf("m%d", i+1),
			"parentId":  parent,
			"timestamp": ts,
			"message":   m,
		})
	}
	return entries
}

func persistTranscriptSnapshot(ref *AgentRef) {
	persistTranscriptSnapshotForce(ref, false)
}

func persistTranscriptSnapshotForce(ref *AgentRef, overwrite bool) {
	ref.mu.Lock()
	file := ref.SessionFile
	msgs := make([]map[string]any, len(ref.Messages))
	copy(msgs, ref.Messages)
	refID := ref.ID
	ref.mu.Unlock()
	if file == "" || len(msgs) == 0 {
		return
	}
	// Do not overwrite an existing JSONL unless explicitly requested (e.g. post-revive wake turn).
	if !overwrite {
		if info, err := os.Stat(file); err == nil && info.Size() > 0 {
			return
		}
	}
	var b strings.Builder
	ts := time.Now().UTC().Format(time.RFC3339)
	hdr, _ := json.Marshal(map[string]any{
		"type":      "session",
		"version":   3,
		"id":        refID,
		"timestamp": ts,
	})
	b.Write(hdr)
	b.WriteByte('\n')
	for _, entryMap := range buildSessionEntriesFromMessages(msgs) {
		entry, err := json.Marshal(entryMap)
		if err != nil {
			continue
		}
		b.Write(entry)
		b.WriteByte('\n')
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err == nil {
		_ = os.Rename(tmp, file)
	} else {
		_ = os.WriteFile(file, []byte(b.String()), 0o644)
	}
}

func extractAssistantTurnText(msg map[string]any) (texts []string, hasNonYieldToolCall bool) {
	rawContent := msg["content"]
	switch c := rawContent.(type) {
	case string:
		if c != "" {
			return []string{c}, false
		}
	case []any:
		for _, item := range c {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			bType, _ := block["type"].(string)
			switch bType {
			case "text":
				if txt, ok := block["text"].(string); ok && txt != "" {
					texts = append(texts, txt)
				}
			case "toolCall", "tool_use":
				name, _ := block["name"].(string)
				if name != "" && name != "yield" {
					hasNonYieldToolCall = true
					texts = nil // prose before a tool call is mid-work narration
				}
			}
		}
	}
	return texts, hasNonYieldToolCall
}

func parseUsageMap(m map[string]any) Usage {
	var u Usage
	u.Input, _ = asInt(m["input"])
	u.Output, _ = asInt(m["output"])
	u.CacheRead, _ = asInt(m["cacheRead"])
	u.CacheWrite, _ = asInt(m["cacheWrite"])
	u.TotalTokens, _ = asInt(m["totalTokens"])
	u.ReasoningTokens, _ = asInt(m["reasoningTokens"])
	if cost, ok := m["cost"].(map[string]any); ok {
		u.Cost.Input, _ = asFloat(cost["input"])
		u.Cost.Output, _ = asFloat(cost["output"])
		u.Cost.CacheRead, _ = asFloat(cost["cacheRead"])
		u.Cost.CacheWrite, _ = asFloat(cost["cacheWrite"])
		u.Cost.Total, _ = asFloat(cost["total"])
	}
	return u
}

func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	}
	return 0, false
}

func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

var primaryArgKeys = []string{
	"path", "file_path", "filePath", "command", "cmd", "pattern",
	"url", "query", "prompt", "assignment", "note", "message", "op", "name", "id",
}

func summarizePrimaryArg(toolName string, args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	if toolName == "grep" {
		pat, _ := args["pattern"].(string)
		p, _ := args["path"].(string)
		if pat != "" && p != "" {
			return OneLineLabel(pat + " @ " + p)
		}
		if pat != "" {
			return OneLineLabel(pat)
		}
	}
	for _, k := range primaryArgKeys {
		if s, ok := args[k].(string); ok && s != "" {
			return OneLineLabel(s)
		}
	}
	for k, v := range args {
		if k == "i" {
			continue
		}
		if s, ok := v.(string); ok && s != "" {
			return OneLineLabel(s)
		}
	}
	return ""
}

func appendRecentOutput(lines []string, delta string) []string {
	parts := strings.Split(delta, "\n")
	if len(lines) == 0 {
		lines = append(lines, parts[0])
	} else {
		lines[len(lines)-1] += parts[0]
	}
	for _, p := range parts[1:] {
		lines = append(lines, p)
	}
	if len(lines) > 8 {
		lines = lines[len(lines)-8:]
	}
	return lines
}

func truncateTailOutput(s string, maxBytes, maxLines int) (string, bool) {
	if len(s) <= maxBytes && strings.Count(s, "\n")+1 <= maxLines {
		return s, false
	}
	lines := strings.Split(s, "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	out := strings.Join(lines, "\n")
	if len(out) > maxBytes {
		out = out[len(out)-maxBytes:]
		if nl := strings.IndexByte(out, '\n'); nl >= 0 && nl < len(out)-1 {
			out = out[nl+1:]
		}
	}
	return out, true
}

// makeMonitoredWakeHandler builds the OnWake callback for an idle keep-alive
// child session. A wake turn runs through the same monitoring path as the first
// turn: acquires the runtime semaphore, subscribes to session events, enforces
// MaxRuntimeMs and SoftBudget (notice + 1.5x stop + +5 grace), updates `<id>.md`
// and `<id>.jsonl`, registers a wake job in `internal/jobs` when woken by the
// parent (or relays the answer via `SendMessageInSession` when woken by a peer),
// and re-arms the idle-park timer when done.
// upstream: packages/coding-agent/src/task/executor.ts:3457-3900 (`attachIrcWakeTurnMonitor`, `relayWakeTurnOutput`)
func makeMonitoredWakeHandler(reg *Registry, ref *AgentRef, req RunRequest, structured bool, softBudgetNotice bool) func(msg IRCMessage) bool {
	var wakeSeq int64
	var seqMu sync.Mutex
	return func(msg IRCMessage) bool {
		ref.mu.Lock()
		s := ref.Session
		if s == nil {
			ref.mu.Unlock()
			return false
		}
		if ref.idleTimer != nil {
			ref.idleTimer.Stop()
			ref.idleTimer = nil
		}
		now := time.Now()
		ref.Status = "running"
		ref.runActive = true
		ref.TurnStartedAt = now
		ref.LastActive = now
		baseDurationMs := ref.DurationMs
		baseRequests := ref.Requests
		baseTokens := ref.Tokens
		baseCost := ref.Cost
		baseTools := ref.Tools
		ref.mu.Unlock()
		if reg != nil {
			reg.RecordActivity(req.ParentSessionID, surface.AgentHubActivity{
				AgentID: req.ID,
				Kind:    "status",
				Title:   "Status: running (wake)",
				Detail:  OneLineLabel(msg.Body),
				Status:  "pending",
			})
			reg.notify()
		}

		seqMu.Lock()
		wakeSeq++
		n := wakeSeq
		seqMu.Unlock()

		// Determine whether the parent (Main / parentAgentID) woke this child.
		parentID := req.ParentAgentID
		if parentID == "" {
			parentID = MainAgentID
		}
		fromParent := msg.From == parentID || msg.From == MainAgentID
		relayOnStop := !msg.WakeRelay && !msg.Broadcast && !msg.Progress
		formatted := FormatIncomingIRCWithOptions(msg, relayOnStop, false)

		wakeCtx, wakeCancel := context.WithCancel(context.Background())
		var wakeJob *AgentJob
		wakeJobID := fmt.Sprintf("%s-wake-%d", req.ID, n)
		if fromParent && req.ParentSessionID != "" && !msg.Broadcast && !msg.Progress {
			wakeJob = NewAgentJob(wakeJobID, req.ID, wakeCancel, AgentProgress{
				ID:          req.ID,
				Agent:       req.Agent.Name,
				AgentSource: req.Agent.Source,
				Status:      "running",
				Assignment:  msg.Body,
			})
			wakeJob.agentURLID = req.ID
			jobs.Add(req.ParentSessionID, wakeJob)
		}

		go func() {
			defer wakeCancel()
			defer func() {
				if r := recover(); r != nil {
					errStr := fmt.Sprintf("subagent wake turn panicked: %v", r)
					if wakeJob != nil {
						wakeJob.finish("failed", errStr, "", nil, nil, false, nil)
					}
					ref.mu.Lock()
					ref.Status = "aborted"
					ref.runActive = false
					if ref.Session == s {
						ref.Session = nil
					}
					ref.OnWake = nil
					ref.mu.Unlock()
					if s.SessionID != "" {
						lifecycle.End(s.SessionID)
					}
				}
			}()

			turnStart := time.Now()
			if req.Semaphore != nil {
				if err := req.Semaphore.Acquire(wakeCtx); err != nil {
					if wakeJob != nil {
						wakeJob.finish("cancelled", "Wake turn cancelled before start", "", nil, nil, false, nil)
					}
					return
				}
				defer req.Semaphore.Release()
			}

			runCtx := wakeCtx
			var cancelTimeout context.CancelFunc
			if req.MaxRuntimeMs > 0 {
				runCtx, cancelTimeout = context.WithTimeout(wakeCtx, time.Duration(req.MaxRuntimeMs)*time.Millisecond)
				defer cancelTimeout()
			}

			budgetStopThreshold := 0
			if req.SoftBudget > 0 {
				budgetStopThreshold = int(math.Ceil(float64(req.SoftBudget) * 1.5))
			}

			var (
				wmu                 sync.Mutex
				requests            int
				tokens              int
				usageTotals         Usage
				hasUsage            bool
				outputChunks        []string
				finalOutputChunks   []string
				lastReportText      string
				lastStopReason      string
				lastErrorMsg        string
				budgetNoticeSent    bool
				budgetStopRequested bool
				budgetLimitExceeded bool
				budgetStopDone      = make(chan struct{})
				budgetStopOnce      sync.Once
			)

			var wakeTools int
			unsub := s.Subscribe(func(event map[string]any) {
				evType, _ := event["type"].(string)
				nowMs := time.Now().UnixMilli()
				shouldNotify := false
				wmu.Lock()
				switch evType {
				case "tool_execution_start":
					toolName, _ := event["toolName"].(string)
					argsMap, _ := event["args"].(map[string]any)
					argPreview := summarizePrimaryArg(toolName, argsMap)
					intentStr, _ := event["intent"].(string)
					if intentStr == "" && argsMap != nil {
						intentStr, _ = argsMap["i"].(string)
					}
					wakeTools++
					if reg != nil {
						actLabel := intentStr
						if actLabel == "" && toolName != "" {
							actLabel = toolName
							if argPreview != "" {
								actLabel += "(" + argPreview + ")"
							}
						}
						if actLabel != "" {
							reg.SetActivityInSession(req.ParentSessionID, req.ID, actLabel)
						}
						ref.mu.Lock()
						ref.Tools = baseTools + wakeTools
						ref.DurationMs = baseDurationMs + time.Since(turnStart).Milliseconds()
						ref.LastActive = time.Now()
						ref.mu.Unlock()
						reg.RecordActivity(req.ParentSessionID, surface.AgentHubActivity{
							AgentID:     req.ID,
							Kind:        "tool",
							Title:       toolName,
							Detail:      OneLineLabel(intentStr),
							Status:      "pending",
							TimestampMs: nowMs,
						})
						shouldNotify = true
					}
				case "tool_execution_end":
					toolName, _ := event["toolName"].(string)
					isErr, _ := event["isError"].(bool)
					if reg != nil && toolName != "" {
						ref.mu.Lock()
						ref.DurationMs = baseDurationMs + time.Since(turnStart).Milliseconds()
						ref.LastActive = time.Now()
						ref.mu.Unlock()
						st := "ok"
						if isErr {
							st = "error"
						}
						reg.RecordActivity(req.ParentSessionID, surface.AgentHubActivity{
							AgentID:     req.ID,
							Kind:        "tool",
							Title:       toolName,
							Status:      st,
							TimestampMs: nowMs,
						})
						shouldNotify = true
					}
				case "message_update":
					if ame, ok := event["assistantMessageEvent"].(map[string]any); ok {
						if t, _ := ame["type"].(string); t == "text_delta" {
							if delta, _ := ame["delta"].(string); delta != "" {
								outputChunks = append(outputChunks, delta)
							}
						}
					}
				case "message_end":
					m, _ := event["message"].(map[string]any)
					if m != nil && m["role"] == "assistant" {
						requests++
						if sr, ok := m["stopReason"].(string); ok && sr != "" {
							lastStopReason = sr
						}
						if em, ok := m["errorMessage"].(string); ok && em != "" {
							lastErrorMsg = em
						}
						texts, hasNonYield := extractAssistantTurnText(m)
						if len(texts) > 0 {
							joined := strings.Join(texts, "\n")
							if hasNonYield {
								lastReportText = ""
							} else if strings.TrimSpace(joined) != "" {
								lastReportText = joined
							}
						} else if hasNonYield {
							lastReportText = ""
						}
						if uMap, ok := m["usage"].(map[string]any); ok {
							u := parseUsageMap(uMap)
							usageTotals.add(u)
							hasUsage = true
							tokens += u.Input + u.Output + u.CacheWrite
						}
						if reg != nil {
							ref.mu.Lock()
							ref.Requests = baseRequests + requests
							ref.Tokens = baseTokens + tokens
							ref.Cost = baseCost + usageTotals.Cost.Total
							if uMap, ok := m["usage"].(map[string]any); ok {
								if ctxTok, ok := asInt(uMap["contextTokens"]); ok && ctxTok > 0 {
									ref.ContextTokens = ctxTok
								} else if totTok, ok := asInt(uMap["totalTokens"]); ok && totTok > 0 {
									ref.ContextTokens = totTok
								}
							}
							ref.DurationMs = baseDurationMs + time.Since(turnStart).Milliseconds()
							ref.LastActive = time.Now()
							ref.mu.Unlock()
							actStatus := "ok"
							actTitle := "Assistant response"
							actDetail := OneLineLabel(lastReportText)
							if sr, _ := m["stopReason"].(string); sr == "error" || sr == "aborted" {
								actStatus = sr
								actTitle = "Assistant " + sr
							}
							if em, _ := m["errorMessage"].(string); em != "" {
								actStatus = "error"
								actDetail = OneLineLabel(em)
							}
							reg.RecordActivity(req.ParentSessionID, surface.AgentHubActivity{
								AgentID:     req.ID,
								Kind:        "response",
								Title:       actTitle,
								Detail:      actDetail,
								Status:      actStatus,
								TimestampMs: nowMs,
							})
							shouldNotify = true
						}
						if req.SoftBudget > 0 {
							if budgetStopRequested {
								if !budgetLimitExceeded && requests >= budgetStopThreshold+BudgetStopGraceRequests {
									budgetLimitExceeded = true
									go func() { _ = s.Abort() }()
								}
							} else if requests >= budgetStopThreshold {
								budgetStopRequested = true
								go func() {
									_ = s.Abort()
									budgetStopOnce.Do(func() { close(budgetStopDone) })
								}()
							} else if softBudgetNotice && !budgetNoticeSent && requests >= req.SoftBudget {
								budgetNoticeSent = true
								notice := BuildBudgetNotice(requests, req.SoftBudget)
								go func() { _ = s.Steer(notice) }()
							}
						}
					}
				case "agent_end":
					if msgs, ok := event["messages"].([]any); ok {
						for _, rawMsg := range msgs {
							m, ok := rawMsg.(map[string]any)
							if !ok || m["role"] != "assistant" {
								continue
							}
							if sr, ok := m["stopReason"].(string); ok && sr != "" {
								lastStopReason = sr
							}
							if em, ok := m["errorMessage"].(string); ok && em != "" {
								lastErrorMsg = em
							}
							texts, _ := extractAssistantTurnText(m)
							for _, t := range texts {
								if t != "" {
									finalOutputChunks = append(finalOutputChunks, t)
								}
							}
						}
					}
				}
				wmu.Unlock()
				if shouldNotify && reg != nil {
					reg.notifyHub(false)
				}
			})

			expand := false
			promptOpts := sdk.PromptOptions{ExpandPromptTemplates: &expand, Source: "extension"}
			promptErr := s.Prompt(runCtx, formatted, promptOpts)

			wmu.Lock()
			needWrap := budgetStopRequested && !budgetLimitExceeded
			wmu.Unlock()
			if needWrap && runCtx.Err() == nil {
				select {
				case <-budgetStopDone:
				case <-runCtx.Done():
				}
				if runCtx.Err() == nil {
					wmu.Lock()
					lastStopReason = ""
					wmu.Unlock()
					promptErr = s.Prompt(runCtx, RenderBudgetStopReminder(structured), promptOpts)
				}
			}
			unsub()

			// Refresh transcript snapshot without losing pre-revive messages.
			msgsGrew := false
			if liveMsgs, err := s.Messages(); err == nil && len(liveMsgs) > 0 {
				ref.mu.Lock()
				if len(liveMsgs) >= len(ref.Messages) {
					msgsGrew = len(liveMsgs) > len(ref.Messages)
					ref.Messages = liveMsgs
				} else {
					ref.Messages = append(ref.Messages, liveMsgs...)
					msgsGrew = true
				}
				ref.mu.Unlock()
			}

			timedOut := req.MaxRuntimeMs > 0 && errors.Is(runCtx.Err(), context.DeadlineExceeded)
			cancelledByCaller := wakeCtx.Err() != nil

			wmu.Lock()
			turnText := strings.TrimSpace(lastReportText)
			if turnText == "" && len(finalOutputChunks) > 0 {
				turnText = strings.TrimSpace(finalOutputChunks[len(finalOutputChunks)-1])
			}
			if turnText == "" && len(outputChunks) > 0 {
				turnText = strings.TrimSpace(strings.Join(outputChunks, ""))
			}
			exitCode := 0
			aborted := false
			var errMsg, abortReason string
			if timedOut {
				aborted = true
				exitCode = 1
				abortReason = fmt.Sprintf("Subagent runtime limit exceeded (task.maxRuntimeMs=%d)", req.MaxRuntimeMs)
				errMsg = abortReason
			} else if budgetLimitExceeded {
				aborted = true
				exitCode = 1
				abortReason = fmt.Sprintf("Soft request budget exceeded (%d requests; budget %d) — agent did not yield when force-stopped", requests, req.SoftBudget)
				errMsg = abortReason
			} else if cancelledByCaller {
				aborted = true
				exitCode = 1
				abortReason = "Cancelled by caller"
				errMsg = abortReason
			} else if promptErr != nil {
				exitCode = 1
				errMsg = promptErr.Error()
			} else if lastStopReason == "error" {
				exitCode = 1
				errMsg = lastErrorMsg
				if errMsg == "" {
					errMsg = "Subagent failed"
				}
			} else if lastStopReason == "aborted" {
				aborted = true
				exitCode = 1
				abortReason = lastErrorMsg
				if abortReason == "" {
					abortReason = "Aborted"
				}
				errMsg = abortReason
			}
			durMs := time.Since(turnStart).Milliseconds()
			reqCopy := requests
			tokCopy := tokens
			usageCopy := usageTotals
			hasUsageCopy := hasUsage
			wmu.Unlock()

			// Republish `<id>.md` and `<id>.jsonl` when output was produced so `agent://<id>` reflects the wake turn.
			var outputPath string
			var outputMeta *OutputMeta
			if req.ArtifactsDir != "" {
				if turnText != "" {
					mdPath := filepath.Join(req.ArtifactsDir, req.ID+".md")
					if err := os.WriteFile(mdPath, []byte(turnText), 0o644); err == nil {
						outputPath = mdPath
						outputMeta = &OutputMeta{
							LineCount: strings.Count(turnText, "\n") + 1,
							CharCount: len([]rune(turnText)),
						}
						ref.mu.Lock()
						ref.OutputPath = mdPath
						ref.mu.Unlock()
					}
				}
				persistTranscriptSnapshotForce(ref, msgsGrew)
			}

			// Return ref to idle and re-arm idle park timer (unless aborted/disposed).
			ref.mu.Lock()
			ref.runActive = false
			if ref.Status == "running" {
				ref.Status = "idle"
			}
			ref.Activity = ""
			ref.TurnStartedAt = time.Time{}
			ref.DurationMs = baseDurationMs + durMs
			ref.Requests = baseRequests + reqCopy
			ref.Tokens = baseTokens + tokCopy
			ref.Cost = baseCost + usageCopy.Cost.Total
			ref.LastActive = time.Now()
			stillLive := ref.Session != nil && ref.Status == "idle"
			finalSt := ref.Status
			ref.mu.Unlock()
			if reg != nil {
				reg.RecordActivity(req.ParentSessionID, surface.AgentHubActivity{
					AgentID: req.ID,
					Kind:    "status",
					Title:   "Status: " + finalSt,
					Status:  activityStatusForAgentStatus(finalSt),
				})
				if stillLive {
					reg.ArmIdleParkTimer(ref, req.AgentIdleTTLMs)
				}
				reg.notify()
			}

			sr := SingleResult{
				Index:       req.Index,
				ID:          req.ID,
				Agent:       req.Agent.Name,
				AgentSource: req.Agent.Source,
				Assignment:  msg.Body,
				ExitCode:    exitCode,
				Output:      turnText,
				Stderr:      errMsg,
				Error:       errMsg,
				Aborted:     aborted,
				AbortReason: abortReason,
				DurationMs:  durMs,
				Tokens:      tokCopy,
				Requests:    reqCopy,
				OutputPath:  outputPath,
				OutputMeta:  outputMeta,
			}
			if hasUsageCopy {
				sr.Usage = &usageCopy
			}

			summary := FormatTaskResultSummary(sr, durMs, "", stillLive)
			hint := FormatFollowUpHint(req.ID, aborted, false, true, stillLive, true)
			deliveryText := summary + "\n\n" + hint
			published := outputPath != "" && turnText != ""

			if wakeJob != nil {
				jobStatus := "completed"
				if cancelledByCaller {
					jobStatus = "cancelled"
				} else if aborted || exitCode != 0 || errMsg != "" {
					jobStatus = "failed"
				}
				wakeJob.finish(jobStatus, deliveryText, "", &sr, nil, false, &published)
				return
			}

			// Peer wake (or parent wake with no ParentSessionID): relay result back to sender via IRC.
			if relayOnStop && reg != nil && msg.From != "" && msg.From != req.ID {
				body := turnText
				if aborted {
					body = fmt.Sprintf("Wake turn was cancelled: %s. See history://%s for details.", abortReason, req.ID)
				} else if errMsg != "" {
					body = fmt.Sprintf("Wake turn failed: %s. See history://%s for details.", errMsg, req.ID)
				} else if body == "" {
					body = fmt.Sprintf("Turn ended without output. See history://%s for details.", req.ID)
				}
				reg.SendMessageInSession(req.ParentSessionID, req.ID, msg.From, body, false, msg.ID, true)
			}
		}()
		return true
	}
}

// makeReviveHandler builds the OnRevive callback for a parked or aborted
// subagent so the Agent Hub can restore a live idle child session ready to
// receive follow-up messages.
func makeReviveHandler(reg *Registry, ref *AgentRef, req RunRequest, structured bool, softBudgetNotice bool) func() (string, error) {
	return func() (string, error) {
		ref.mu.Lock()
		if ref.reviving {
			ref.mu.Unlock()
			return "", fmt.Errorf("agent %s is already reviving", ref.ID)
		}
		if ref.runActive {
			ref.mu.Unlock()
			return "", fmt.Errorf("agent %s is still shutting down", ref.ID)
		}
		st := ref.Status
		if st != "parked" && st != "aborted" {
			ref.mu.Unlock()
			return "", fmt.Errorf("agent %s is %s (only parked or aborted agents can be revived)", ref.ID, st)
		}
		ref.reviving = true
		sessFile := ref.SessionFile
		retainedMsgs := make([]map[string]any, len(ref.Messages))
		copy(retainedMsgs, ref.Messages)
		ref.mu.Unlock()
		defer func() {
			ref.mu.Lock()
			ref.reviving = false
			ref.mu.Unlock()
		}()

		var zero sdk.Context
		if req.SDK == zero {
			return "", fmt.Errorf("cannot revive agent %s: host SDK context unavailable", ref.ID)
		}

		childDepth := req.TaskDepth + 1
		toolsList, excludeList := ResolveChildToolNames(req.Agent, childDepth, req.MaxDepth, structured)
		effectiveIRC := req.IRCEnabled && (toolsList == nil || slices.Contains(toolsList, "write"))
		reqForPrompt := req
		reqForPrompt.IRCEnabled = effectiveIRC
		roster := PeerRoster{}
		if reg != nil && effectiveIRC {
			roster = reg.CollectPeerRosterForSession(req.ParentSessionID, req.ID)
		}
		subagentSysPrompt := RenderSubagentSystemPrompt(reqForPrompt, roster, structured)
		modelCandidates := req.ModelOverride
		if len(modelCandidates) == 0 {
			modelCandidates = req.Agent.Model
		}
		modelObj, modelThinkSuffix, _ := resolveModelForChildWithRole(req.SDK, modelCandidates)
		thinkingLevel := ClampThinkingLevelToCeiling(modelObj, ResolveChildThinkingLevel(req.Agent.ThinkingLevel, req.Effort, modelThinkSuffix), req.MaxEffort)

		// Prepare CustomTools: copy caller custom tools + inject `yield` when structured.
		var customTools []sdk.ToolDefinition
		if len(req.CustomTools) > 0 {
			customTools = append(customTools, req.CustomTools...)
		}
		if structured {
			yieldTool := intent.Tool(schemaorder.Apply(BuildYieldToolDefinition(req.OutputSchema, req.WorkPoolItems, func(_ YieldItem, _ bool) {})))
			customTools = append(customTools, yieldTool)
		}

		// H1: Seed the revived child session from the agent's retained transcript.
		var sm sdk.SessionManager
		var smErr error
		hasSM := false
		if sessFile != "" {
			if info, err := os.Stat(sessFile); err == nil && info.Size() > 0 {
				sm, smErr = sdk.SessionManagerOpen(req.SDK, sessFile, nil, nil)
				if smErr == nil {
					hasSM = true
				}
			}
		}
		if !hasSM && len(retainedMsgs) > 0 {
			entries := buildSessionEntriesFromMessages(retainedMsgs)
			if len(entries) > 0 {
				sm, smErr = sdk.SessionManagerInMemory(req.SDK, req.Cwd, nil, entries)
				if smErr == nil {
					hasSM = true
				}
			}
		}

		opts := sdk.CreateAgentSessionOptions{
			Cwd:           req.Cwd,
			AgentDir:      req.AgentDir,
			Model:         modelObj,
			ThinkingLevel: thinkingLevel,
			Tools:         toolsList,
			ExcludeTools:  excludeList,
			NoTools:       "builtin",
			CustomTools:   customTools,
			InMemory:      !hasSM,
			Resources: sdk.ResourceOptions{
				NoExtensions:             true,
				AdditionalExtensionPaths: []string{FusedExtensionPath},
				NoPromptTemplates:        true,
				NoThemes:                 true,
				NoContextFiles:           true,
				AppendSystemPrompt:       []string{subagentSysPrompt},
			},
		}
		if hasSM {
			opts.SessionManager = &sm
		}

		session, err := req.SDK.CreateAgentSession(opts)
		if err != nil {
			return "", fmt.Errorf("failed to revive agent %s: %w", ref.ID, err)
		}
		parentID := req.ParentAgentID
		if parentID == "" {
			parentID = MainAgentID
		}
		if reg != nil && session.SessionID != "" {
			reg.RegisterChildSession(session.SessionID, buildChildSessionMeta(req, childDepth, parentID, toolsList))
		}
		if err := session.BindExtensions(); err != nil {
			session.Dispose()
			if session.SessionID != "" {
				lifecycle.End(session.SessionID)
				if reg != nil {
					reg.UnregisterChildSession(session.SessionID)
				}
			}
			return "", fmt.Errorf("failed to bind revived agent %s: %w", ref.ID, err)
		}

		// H2: Re-verify ref is still registered and revivable after CreateAgentSession + BindExtensions.
		ref.mu.Lock()
		stillReg := true
		if reg != nil {
			cur, ok := reg.GetInSession(req.ParentSessionID, ref.ID)
			stillReg = ok && cur == ref
		}
		if !stillReg || !ref.reviving || (ref.Status != "parked" && ref.Status != "aborted") {
			ref.mu.Unlock()
			session.Dispose()
			if session.SessionID != "" {
				lifecycle.End(session.SessionID)
				if reg != nil {
					reg.UnregisterChildSession(session.SessionID)
				}
			}
			return "", fmt.Errorf("agent %s was closed or modified during revive", ref.ID)
		}
		ref.Session = session
		ref.ChildSessionID = session.SessionID
		if session.SessionFile != "" {
			ref.SessionFile = session.SessionFile
		}
		ref.Status = "idle"
		ref.Activity = ""
		ref.TurnStartedAt = time.Time{}
		ref.LastActive = time.Now()
		ref.OnWake = makeMonitoredWakeHandler(reg, ref, req, structured, softBudgetNotice)
		ref.mu.Unlock()

		if reg != nil {
			reg.RecordActivity(req.ParentSessionID, surface.AgentHubActivity{
				AgentID: ref.ID,
				Kind:    "status",
				Title:   "Status: idle (revived)",
				Status:  "ok",
			})
			reg.ArmIdleParkTimer(ref, req.AgentIdleTTLMs)
			reg.notify()
		}
		return fmt.Sprintf("Revived agent %s.", ref.ID), nil
	}
}

// extractToolExecutionEndText joins the text content blocks of a PiG
// `tool_execution_end` event's `result` map, matching omp `task/executor.ts:2125-2139`.
func extractToolExecutionEndText(rawResult any) string {
	resMap, ok := rawResult.(map[string]any)
	if !ok || resMap == nil {
		return ""
	}
	content, ok := resMap["content"].([]any)
	if !ok {
		return ""
	}
	var parts []string
	for _, raw := range content {
		blk, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := blk["type"].(string); t == "text" {
			if txt, ok := blk["text"].(string); ok {
				parts = append(parts, txt)
			}
		}
	}
	return strings.Join(parts, "\n")
}
