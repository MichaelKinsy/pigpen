package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/eval"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/intent"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/settings"
)

// upstream: packages/coding-agent/src/task/eval-tools.ts:1-209,
//           packages/coding-agent/src/task/index.ts:1081-1093, 1881-1888,
//           packages/coding-agent/src/eval/agent-bridge.ts:185-199,
//           packages/coding-agent/src/eval/workpool-bridge.ts:85-97

const (
	errEvalToolsPlanMode = "Eval-defined tools are unavailable in plan mode."
	errEvalToolsDisabled = "Eval-defined tools are disabled; set eval.tools.enabled=true to expose them to subagents."
)

// DescribeKernelToolsFn and CallKernelToolFn default to internal/eval's
// DefaultManager RPC functions and may be overridden in unit tests.
var (
	DescribeKernelToolsFn = eval.DescribeKernelTools
	CallKernelToolFn      = eval.CallKernelTool
)

// evalSessionState tracks which parent sessions are still alive so a custom
// tool proxy retained by a revived child session after its parent ended reports
// the upstream session-ended message instead of attempting a dead-session RPC.
// upstream: packages/coding-agent/src/task/eval-tools.ts:144-176
const maxEvalToolEndedSessions = 512

var (
	evalToolSessionsMu      sync.RWMutex
	evalToolActiveSessions  = map[string]bool{}
	evalToolEndedSessions   = map[string]bool{}
	evalToolEndedSessionOrd = []string{}
)

func markEvalToolSessionActive(sessionID string) {
	if sessionID == "" {
		return
	}
	evalToolSessionsMu.Lock()
	evalToolActiveSessions[sessionID] = true
	delete(evalToolEndedSessions, sessionID)
	evalToolSessionsMu.Unlock()
}

func markEvalToolSessionEnded(sessionID string) {
	if sessionID == "" {
		return
	}
	evalToolSessionsMu.Lock()
	defer evalToolSessionsMu.Unlock()
	if !evalToolActiveSessions[sessionID] {
		return
	}
	delete(evalToolActiveSessions, sessionID)
	if evalToolEndedSessions[sessionID] {
		return
	}
	evalToolEndedSessions[sessionID] = true
	evalToolEndedSessionOrd = append(evalToolEndedSessionOrd, sessionID)
	if len(evalToolEndedSessionOrd) > maxEvalToolEndedSessions {
		evict := evalToolEndedSessionOrd[0]
		evalToolEndedSessionOrd = evalToolEndedSessionOrd[1:]
		delete(evalToolEndedSessions, evict)
	}
}

func isEvalToolSessionEnded(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	evalToolSessionsMu.RLock()
	ended := evalToolEndedSessions[sessionID]
	evalToolSessionsMu.RUnlock()
	return ended
}

func init() {
	lifecycle.OnEnd("agents-eval-tools", markEvalToolSessionEnded)
}

func evalToolsEnabledForContext(ctx sdk.Context) bool {
	var zero sdk.Context
	if ctx != zero {
		if st := settings.StoreFor(ctx); st != nil {
			if raw, ok := st.Any("eval.tools.enabled").(bool); ok {
				return raw
			}
		}
	}
	return eval.IsEvalToolsEnabled(ctx)
}

// ResolveEvalCustomTools validates and resolves named kernel-defined tools from
// the session's retained Python and JavaScript eval kernels and materializes
// them as sdk.ToolDefinition proxies for a child subagent session.
//
// upstream: packages/coding-agent/src/task/eval-tools.ts:82-209
func ResolveEvalCustomTools(goCtx context.Context, sdkCtx sdk.Context, names []string, planMode bool) ([]sdk.ToolDefinition, error) {
	descriptors, err := describeEvalTools(goCtx, sdkCtx, names, planMode)
	if err != nil {
		return nil, err
	}
	if len(descriptors) == 0 {
		return nil, nil
	}
	return createEvalCustomTools(sdkCtx, descriptors)
}

// describeEvalTools resolves named kernel-defined tools across retained Python
// and JS kernels, enforcing plan mode, eval.tools.enabled, dual-kernel
// collision detection, and unknown-tool diagnostics.
// upstream: packages/coding-agent/src/task/eval-tools.ts:82-108
func describeEvalTools(goCtx context.Context, sdkCtx sdk.Context, names []string, planMode bool) ([]eval.EvalToolDescriptor, error) {
	requested := dedupeNonEmptyStrings(names)
	if len(requested) == 0 {
		return nil, nil
	}
	if planMode {
		return nil, errors.New(errEvalToolsPlanMode)
	}
	if !evalToolsEnabledForContext(sdkCtx) {
		return nil, errors.New(errEvalToolsDisabled)
	}
	if goCtx == nil {
		goCtx = context.Background()
	}

	tools, err := queryAllEvalTools(goCtx, sdkCtx, requested)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]eval.EvalToolDescriptor, len(tools))
	for _, desc := range tools {
		byName[desc.Name] = desc
	}
	var missing []string
	for _, name := range requested {
		if _, ok := byName[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		availDescs, availErr := listEvalTools(goCtx, sdkCtx)
		if availErr != nil {
			return nil, availErr
		}
		availNames := make([]string, 0, len(availDescs))
		for _, d := range availDescs {
			availNames = append(availNames, d.Name)
		}
		sort.Strings(availNames)
		availStr := "none"
		if len(availNames) > 0 {
			availStr = strings.Join(availNames, ", ")
		}
		return nil, fmt.Errorf(
			"Unknown eval tool(s): %s. Define them with @tool (Python) or tool(fn, {…}) (JS) in an eval cell first. Available: %s",
			strings.Join(missing, ", "),
			availStr,
		)
	}

	ordered := make([]eval.EvalToolDescriptor, 0, len(requested))
	for _, name := range requested {
		if desc, ok := byName[name]; ok {
			ordered = append(ordered, desc)
		}
	}
	return ordered, nil
}

// listEvalTools lists every tool currently defined across retained eval kernels.
// upstream: packages/coding-agent/src/task/eval-tools.ts:111-114
func listEvalTools(goCtx context.Context, sdkCtx sdk.Context) ([]eval.EvalToolDescriptor, error) {
	if !evalToolsEnabledForContext(sdkCtx) {
		return nil, nil
	}
	return queryAllEvalTools(goCtx, sdkCtx, nil)
}

func queryKernelTools(goCtx context.Context, sdkCtx sdk.Context, lang string, names []string) (eval.EvalToolDescribeResult, error) {
	res, err := DescribeKernelToolsFn(goCtx, sdkCtx, lang, names)
	if err != nil {
		if errors.Is(err, eval.ErrKernelNotRunning) {
			return eval.EvalToolDescribeResult{
				Tools:   nil,
				Missing: append([]string(nil), names...),
			}, nil
		}
		return eval.EvalToolDescribeResult{}, err
	}
	return res, nil
}

// queryAllEvalTools queries the retained Python and JS kernels concurrently and
// fails if any tool name is defined in both kernels.
// upstream: packages/coding-agent/src/task/eval-tools.ts:54-74
func queryAllEvalTools(goCtx context.Context, sdkCtx sdk.Context, names []string) ([]eval.EvalToolDescriptor, error) {
	var (
		wg    sync.WaitGroup
		pyRes eval.EvalToolDescribeResult
		jsRes eval.EvalToolDescribeResult
		pyErr error
		jsErr error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		pyRes, pyErr = queryKernelTools(goCtx, sdkCtx, "py", names)
	}()
	go func() {
		defer wg.Done()
		jsRes, jsErr = queryKernelTools(goCtx, sdkCtx, "js", names)
	}()
	wg.Wait()
	if pyErr != nil {
		return nil, pyErr
	}
	if jsErr != nil {
		return nil, jsErr
	}

	byName := make(map[string]eval.EvalToolDescriptor, len(pyRes.Tools)+len(jsRes.Tools))
	order := make([]string, 0, len(pyRes.Tools)+len(jsRes.Tools))
	add := func(list []eval.EvalToolDescriptor, fallbackLang eval.EvalLanguage) error {
		for _, desc := range list {
			if desc.Language == "" {
				desc.Language = fallbackLang
			}
			if existing, exists := byName[desc.Name]; exists && existing.Language != desc.Language {
				return fmt.Errorf(`eval tool "%s" is defined in both the Python and JS kernels; undefine one`, desc.Name)
			} else if !exists {
				order = append(order, desc.Name)
			}
			byName[desc.Name] = desc
		}
		return nil
	}
	if err := add(pyRes.Tools, eval.LangPython); err != nil {
		return nil, err
	}
	if err := add(jsRes.Tools, eval.LangJS); err != nil {
		return nil, err
	}
	out := make([]eval.EvalToolDescriptor, 0, len(order))
	for _, name := range order {
		out = append(out, byName[name])
	}
	return out, nil
}

// createEvalCustomTools materializes kernel-defined tool descriptors as
// sdk.ToolDefinition proxies for a child session.
// upstream: packages/coding-agent/src/task/eval-tools.ts:152-209
func createEvalCustomTools(parentSDK sdk.Context, descriptors []eval.EvalToolDescriptor) ([]sdk.ToolDefinition, error) {
	if len(descriptors) > 0 && !evalToolsEnabledForContext(parentSDK) {
		return nil, errors.New(errEvalToolsDisabled)
	}
	var parentSessionID string
	var zero sdk.Context
	if parentSDK != zero {
		if id, err := parentSDK.SessionManager().GetSessionID(); err == nil {
			parentSessionID = strings.TrimSpace(id)
		}
	}
	markEvalToolSessionActive(parentSessionID)
	out := make([]sdk.ToolDefinition, 0, len(descriptors))
	for _, desc := range descriptors {
		out = append(out, evalCustomTool(parentSDK, parentSessionID, desc))
	}
	return out, nil
}

func evalCustomTool(parentSDK sdk.Context, parentSessionID string, descriptor eval.EvalToolDescriptor) sdk.ToolDefinition {
	paramsSchema := descriptor.Parameters
	if paramsSchema == nil {
		paramsSchema = map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": true,
		}
	}
	lang := descriptor.Language
	if lang == "" {
		lang = eval.LangPython
	}
	callLang := "py"
	if lang == eval.LangJS {
		callLang = "js"
	}

	return sdk.ToolDefinition{
		Name:          descriptor.Name,
		Label:         descriptor.Name,
		Description:   descriptor.Description,
		Parameters:    sdk.Schema(paramsSchema),
		ExecutionMode: "parallel",
		Execute: func(childCtx sdk.Context, rawParams map[string]any) (any, error) {
			if isEvalToolSessionEnded(parentSessionID) {
				return evalToolErrorResult(
					descriptor.Name,
					lang,
					fmt.Sprintf(`The session whose %s kernel defined "%s" has ended.`, lang, descriptor.Name),
				), nil
			}
			callCtx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if done := childCtx.Done(); done != nil {
				go func() {
					select {
					case <-done:
						cancel()
					case <-callCtx.Done():
					}
				}()
			}

			args := stripEvalToolHarnessIntent(rawParams, paramsSchema)
			// Route nested `tool.<name>(...)` callbacks through childCtx (which carries
			// the active toolCallID required by PiG's `sdk.Context.ExecuteTool`) while
			// resolving the parent's retained kernel via parentSDK. When a unit test
			// overrides CallKernelToolFn, invoke the test stub directly.
			var (
				res eval.EvalToolInvokeResult
				err error
			)
			if childCtx.ToolCallID() != "" && isDefaultCallKernelToolFn() {
				res, err = eval.CallKernelToolWithBridgeContext(callCtx, parentSDK, childCtx, callLang, descriptor.Name, args)
			} else {
				res, err = CallKernelToolFn(callCtx, parentSDK, callLang, descriptor.Name, args)
			}
			if err != nil {
				if errors.Is(err, eval.ErrKernelNotRunning) {
					return evalToolErrorResult(descriptor.Name, lang, err.Error()), nil
				}
				return evalToolErrorResult(descriptor.Name, lang, err.Error()), nil
			}
			if !res.OK {
				errMsg := res.Error
				if errMsg == "" {
					errMsg = fmt.Sprintf(`eval tool "%s" failed`, descriptor.Name)
				}
				return evalToolErrorResult(descriptor.Name, lang, errMsg), nil
			}
			return sdk.ToolResult{
				Content: evalToolResultText(res.Value),
				Details: map[string]any{
					"evalTool": descriptor.Name,
					"language": string(lang),
				},
			}, nil
		},
	}
}

// stripEvalToolHarnessIntent removes the harness intent field `i` before
// forwarding arguments to a kernel-defined function unless the tool's own
// JSON Schema declares `i`.
// upstream: packages/coding-agent/src/task/eval-tools.ts:116-124
func stripEvalToolHarnessIntent(params map[string]any, parameters map[string]any) map[string]any {
	if len(params) == 0 {
		return map[string]any{}
	}
	if _, hasIntent := params[intent.Field]; !hasIntent {
		return params
	}
	if intent.SchemaDefinesProperty(parameters, intent.Field) {
		return params
	}
	out := make(map[string]any, len(params)-1)
	for k, v := range params {
		if k == intent.Field {
			continue
		}
		out[k] = v
	}
	return out
}

// evalToolResultText formats a kernel tool return value into tool result text.
// upstream: packages/coding-agent/src/task/eval-tools.ts:126-130
func evalToolResultText(value any) string {
	if value == nil {
		return "(no result)"
	}
	if s, ok := value.(string); ok {
		if s == "" {
			return "(empty result)"
		}
		return s
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(value); err != nil {
		return fmt.Sprint(value)
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

func evalToolErrorResult(name string, language eval.EvalLanguage, errText string) sdk.ToolResult {
	return sdk.ToolResult{
		Content: errText,
		Details: map[string]any{
			"evalTool": name,
			"language": string(language),
			"isError":  true,
		},
		IsError: true,
	}
}

// wrapSubagentCustomTool prepares a caller-provided custom tool (e.g. an eval
// kernel-defined tool) for a child subagent session:
//   - When intent tracing is enabled for parentSDK, wraps with intent.Tool so
//     the schema accepts harness `i` (without mutating the parent session's
//     intent sync table via ToolForContext).
//   - When intent tracing is disabled, wraps PrepareArguments so an undeclared
//     harness `i` is dropped before schema validation / execution even when the
//     tool schema sets `additionalProperties: false`.
func wrapSubagentCustomTool(parentSDK sdk.Context, ct sdk.ToolDefinition) sdk.ToolDefinition {
	if intent.Enabled != nil && intent.Enabled(parentSDK) {
		return intent.Tool(ct)
	}
	prevPrep := ct.PrepareArguments
	paramsMap := map[string]any(ct.Parameters)
	ct.PrepareArguments = func(args map[string]any) (map[string]any, error) {
		if prevPrep != nil {
			var err error
			args, err = prevPrep(args)
			if err != nil {
				return nil, err
			}
		}
		if args == nil {
			return nil, nil
		}
		return stripEvalToolHarnessIntent(args, paramsMap), nil
	}
	return ct
}

func dedupeNonEmptyStrings(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(names))
	out := make([]string, 0, len(names))
	for _, n := range names {
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// isDefaultCallKernelToolFn reports whether CallKernelToolFn still points at
// eval.CallKernelTool (not replaced by a unit test stub).
func isDefaultCallKernelToolFn() bool {
	return fmt.Sprintf("%p", CallKernelToolFn) == fmt.Sprintf("%p", eval.CallKernelTool)
}
