package eval

import (
	"context"
	"errors"
	"fmt"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// upstream: packages/coding-agent/src/eval/types.ts:6-14,
//           packages/coding-agent/src/eval/executor-base.ts:26-33,
//           packages/coding-agent/src/eval/py/executor.ts:486-577,
//           packages/coding-agent/src/eval/js/context-manager.ts:244-313,
//           packages/coding-agent/src/eval/settings.ts:46-57

// EvalToolDescriptor is metadata for one kernel-defined tool exposed to task subagents.
// upstream: packages/coding-agent/src/eval/types.ts:6-11
type EvalToolDescriptor struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
	Language    EvalLanguage   `json:"language,omitempty"`
}

// EvalToolDescribeResult is the result of describing named tools in a retained kernel.
// upstream: packages/coding-agent/src/eval/py/executor.ts:539-562,
//
//	packages/coding-agent/src/eval/js/context-manager.ts:244-313
type EvalToolDescribeResult struct {
	Tools   []EvalToolDescriptor `json:"tools"`
	Missing []string             `json:"missing"`
}

// EvalToolInvokeResult is the result of invoking a kernel-defined tool.
// upstream: packages/coding-agent/src/eval/types.ts:14
type EvalToolInvokeResult struct {
	OK    bool   `json:"ok"`
	Value any    `json:"value,omitempty"`
	Error string `json:"error,omitempty"`
}

// ErrKernelNotRunning is the sentinel matched via errors.Is when a caller
// describes or invokes a kernel-defined tool whose retained kernel is absent or dead.
// upstream: packages/coding-agent/src/eval/executor-base.ts:26-33
var ErrKernelNotRunning = errors.New("kernel is not running")

type kernelNotRunningError struct {
	languageLabel string
}

func (e kernelNotRunningError) Error() string {
	return fmt.Sprintf(
		"%s kernel is not running; tools defined with @tool / tool() live in the kernel and must be (re)defined in an eval cell first",
		e.languageLabel,
	)
}

func (e kernelNotRunningError) Unwrap() error {
	return ErrKernelNotRunning
}

func newKernelNotRunningError(lang string) error {
	label := "Python"
	if normalizeToolLang(lang) == "js" {
		label = "JavaScript"
	}
	return kernelNotRunningError{languageLabel: label}
}

func normalizeToolLang(lang string) string {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "py", "python":
		return "py"
	case "js", "javascript":
		return "js"
	default:
		return strings.ToLower(strings.TrimSpace(lang))
	}
}

// IsEvalToolsEnabled reports whether kernel-defined tools may be exposed to
// subagents (`eval.tools.enabled`), defaulting to true to match omp v19.1.1.
// upstream: packages/coding-agent/src/eval/settings.ts:46-57, task/eval-tools.ts:77-79
func IsEvalToolsEnabled(ctx sdk.Context) bool {
	if Setting != nil {
		if v, ok := settingAsBool(Setting(ctx, "eval.tools.enabled")); ok {
			return v
		}
		return true
	}
	var zero sdk.Context
	if ctx != zero {
		if cfg, err := ctx.GetSettings(); err == nil && cfg != nil {
			if v, ok := lookupBoolSetting(cfg, "eval.tools.enabled"); ok {
				return v
			}
		}
	}
	return true
}

// DescribeKernelTools describes named tools (or all tools when names is empty)
// defined in the session's retained Python or JavaScript kernel via DefaultManager().
// Returns an error wrapping ErrKernelNotRunning when no live kernel exists.
// upstream: packages/coding-agent/src/eval/py/executor.ts:539-562,
//
//	packages/coding-agent/src/eval/js/context-manager.ts:244-313
func DescribeKernelTools(goCtx context.Context, sdkCtx sdk.Context, lang string, names []string) (EvalToolDescribeResult, error) {
	return defaultManager.DescribeKernelTools(goCtx, sdkCtx, lang, names)
}

// CallKernelTool invokes a named tool defined in the session's retained Python
// or JavaScript kernel via DefaultManager(). Returns an error wrapping
// ErrKernelNotRunning when no live kernel exists.
// upstream: packages/coding-agent/src/eval/py/executor.ts:565-577,
//
//	packages/coding-agent/src/eval/js/context-manager.ts:244-313
func CallKernelTool(goCtx context.Context, sdkCtx sdk.Context, lang, name string, args map[string]any) (EvalToolInvokeResult, error) {
	return defaultManager.CallKernelTool(goCtx, sdkCtx, lang, name, args)
}

// CallKernelToolWithBridgeContext invokes a named tool in the parent session's
// retained kernel (identified by parentSDK) while using bridgeSDK (the active
// tool-call sdk.Context carrying toolCallID) for nested `tool.<name>(...)`
// calls executed by the callback.
func CallKernelToolWithBridgeContext(goCtx context.Context, parentSDK, bridgeSDK sdk.Context, lang, name string, args map[string]any) (EvalToolInvokeResult, error) {
	return defaultManager.CallKernelToolWithBridgeContext(goCtx, parentSDK, bridgeSDK, lang, name, args)
}

// DescribeKernelTools describes named tools (or all defined tools when names is
// empty) in m's retained kernel for sdkCtx's session without starting a kernel.
func (m *Manager) DescribeKernelTools(goCtx context.Context, sdkCtx sdk.Context, lang string, names []string) (EvalToolDescribeResult, error) {
	if goCtx == nil {
		goCtx = context.Background()
	}
	if names == nil {
		names = []string{}
	}
	switch normalizeToolLang(lang) {
	case "py":
		return m.describePythonKernelTools(goCtx, sdkCtx, names)
	case "js":
		return m.describeJSKernelTools(goCtx, sdkCtx, names)
	default:
		return EvalToolDescribeResult{}, fmt.Errorf("unsupported eval kernel language %q", lang)
	}
}

// CallKernelTool invokes a named tool in m's retained kernel for sdkCtx's session.
func (m *Manager) CallKernelTool(goCtx context.Context, sdkCtx sdk.Context, lang, name string, args map[string]any) (EvalToolInvokeResult, error) {
	return m.CallKernelToolWithBridgeContext(goCtx, sdkCtx, sdkCtx, lang, name, args)
}

// CallKernelToolWithBridgeContext invokes a named tool in m's retained kernel
// for parentSDK's session, routing nested host tool calls through bridgeSDK
// (falling back to parentSDK when bridgeSDK has no tool call context).
func (m *Manager) CallKernelToolWithBridgeContext(goCtx context.Context, parentSDK, bridgeSDK sdk.Context, lang, name string, args map[string]any) (EvalToolInvokeResult, error) {
	if goCtx == nil {
		goCtx = context.Background()
	}
	if args == nil {
		args = map[string]any{}
	}
	var zero sdk.Context
	if bridgeSDK == zero || bridgeSDK.ToolCallID() == "" {
		bridgeSDK = parentSDK
	}
	switch normalizeToolLang(lang) {
	case "py":
		return m.callPythonKernelTool(goCtx, parentSDK, bridgeSDK, name, args)
	case "js":
		return m.callJSKernelTool(goCtx, parentSDK, bridgeSDK, name, args)
	default:
		return EvalToolInvokeResult{}, fmt.Errorf("unsupported eval kernel language %q", lang)
	}
}

func (m *Manager) peekLivePythonKernel(sdkCtx sdk.Context) (k *pythonKernel, sessionID, cwd, slotKey string, err error) {
	sessionID, _, _, err = sessionIdentity(sdkCtx)
	if err != nil {
		return nil, "", "", "", newKernelNotRunningError("py")
	}
	settings := readSessionSettings(sdkCtx)
	cwd = normalizeCwd(sdkCtx.Cwd())
	bridgeSession := "python:" + sessionID
	slotKey = bridgeSession + "\x00" + cwd + "\x00" + settings.PythonInterpreter

	m.mu.Lock()
	slot := m.pySessions[slotKey]
	m.mu.Unlock()
	if slot == nil {
		return nil, sessionID, cwd, slotKey, newKernelNotRunningError("py")
	}
	k = slot.liveKernel()
	if k == nil || !k.isAlive() {
		return nil, sessionID, cwd, slotKey, newKernelNotRunningError("py")
	}
	return k, sessionID, cwd, slotKey, nil
}

func (m *Manager) peekLiveJSKernel(sdkCtx sdk.Context) (k *jsKernel, sessionID, cwd, bridgeSession string, err error) {
	sessionID, _, _, err = sessionIdentity(sdkCtx)
	if err != nil {
		return nil, "", "", "", newKernelNotRunningError("js")
	}
	cwd = normalizeCwd(sdkCtx.Cwd())
	bridgeSession = "js:" + sessionID

	m.mu.Lock()
	slot := m.jsSessions[bridgeSession]
	m.mu.Unlock()
	if slot == nil {
		return nil, sessionID, cwd, bridgeSession, newKernelNotRunningError("js")
	}
	k = slot.liveKernel()
	if k == nil || !k.isAlive() {
		return nil, sessionID, cwd, bridgeSession, newKernelNotRunningError("js")
	}
	return k, sessionID, cwd, bridgeSession, nil
}

func (m *Manager) describePythonKernelTools(goCtx context.Context, sdkCtx sdk.Context, names []string) (EvalToolDescribeResult, error) {
	k, sessionID, cwd, _, err := m.peekLivePythonKernel(sdkCtx)
	if err != nil {
		return EvalToolDescribeResult{}, err
	}
	bridge, err := ensureBridgeServer()
	if err != nil {
		return EvalToolDescribeResult{}, err
	}
	runID := "py-tool-" + newRandomID()
	bridgeSession := "python:" + sessionID
	unreg := bridge.register(bridgeSession, runID, &bridgeRun{
		sdkCtx:        sdkCtx,
		ctx:           goCtx,
		cwd:           cwd,
		sessionID:     sessionID,
		language:      "py",
		toolFallback:  m.toolFallback,
		isLangRunning: func(lang string) bool { return m.isSessionLangRunning(sessionID, lang) },
	})
	defer unreg()

	reply, err := k.invokeTool(goCtx, runID, map[string]any{
		"op":    "describe",
		"names": names,
	})
	if err != nil {
		return EvalToolDescribeResult{}, err
	}
	return parseDescribeEnvelope(reply, LangPython, "Python")
}

func (m *Manager) callPythonKernelTool(goCtx context.Context, parentSDK, bridgeSDK sdk.Context, name string, args map[string]any) (EvalToolInvokeResult, error) {
	k, sessionID, cwd, _, err := m.peekLivePythonKernel(parentSDK)
	if err != nil {
		return EvalToolInvokeResult{}, err
	}
	bridge, err := ensureBridgeServer()
	if err != nil {
		return EvalToolInvokeResult{}, err
	}
	runID := "py-tool-" + newRandomID()
	bridgeSession := "python:" + sessionID
	unreg := bridge.register(bridgeSession, runID, &bridgeRun{
		sdkCtx:        bridgeSDK,
		ctx:           goCtx,
		cwd:           cwd,
		sessionID:     sessionID,
		language:      "py",
		toolFallback:  m.toolFallback,
		isLangRunning: func(lang string) bool { return m.isSessionLangRunning(sessionID, lang) },
	})
	defer unreg()

	reply, err := k.invokeTool(goCtx, runID, map[string]any{
		"op":   "call",
		"name": name,
		"args": args,
	})
	if err != nil {
		if errors.Is(err, ErrKernelNotRunning) {
			return EvalToolInvokeResult{}, err
		}
		return EvalToolInvokeResult{OK: false, Error: err.Error()}, nil
	}
	return parseCallEnvelope(reply, "Python"), nil
}

func (m *Manager) describeJSKernelTools(goCtx context.Context, sdkCtx sdk.Context, names []string) (EvalToolDescribeResult, error) {
	k, sessionID, cwd, bridgeSession, err := m.peekLiveJSKernel(sdkCtx)
	if err != nil {
		return EvalToolDescribeResult{}, err
	}
	bridge, err := ensureBridgeServer()
	if err != nil {
		return EvalToolDescribeResult{}, err
	}
	runID := "js-tool-" + newRandomID()
	unreg := bridge.register(bridgeSession, runID, &bridgeRun{
		sdkCtx:        sdkCtx,
		ctx:           goCtx,
		cwd:           cwd,
		sessionID:     sessionID,
		language:      "js",
		toolFallback:  m.toolFallback,
		isLangRunning: func(lang string) bool { return m.isSessionLangRunning(sessionID, lang) },
	})
	defer unreg()

	reply, err := k.invokeTool(goCtx, runID, map[string]any{
		"op":    "describe",
		"names": names,
	}, bridge, bridgeSession)
	if err != nil {
		return EvalToolDescribeResult{}, err
	}
	return parseDescribeEnvelope(reply, LangJS, "JavaScript")
}

func (m *Manager) callJSKernelTool(goCtx context.Context, parentSDK, bridgeSDK sdk.Context, name string, args map[string]any) (EvalToolInvokeResult, error) {
	k, sessionID, cwd, bridgeSession, err := m.peekLiveJSKernel(parentSDK)
	if err != nil {
		return EvalToolInvokeResult{}, err
	}
	bridge, err := ensureBridgeServer()
	if err != nil {
		return EvalToolInvokeResult{}, err
	}
	runID := "js-tool-" + newRandomID()
	unreg := bridge.register(bridgeSession, runID, &bridgeRun{
		sdkCtx:        bridgeSDK,
		ctx:           goCtx,
		cwd:           cwd,
		sessionID:     sessionID,
		language:      "js",
		toolFallback:  m.toolFallback,
		isLangRunning: func(lang string) bool { return m.isSessionLangRunning(sessionID, lang) },
	})
	defer unreg()

	reply, err := k.invokeTool(goCtx, runID, map[string]any{
		"op":   "call",
		"name": name,
		"args": args,
	}, bridge, bridgeSession)
	if err != nil {
		if errors.Is(err, ErrKernelNotRunning) {
			return EvalToolInvokeResult{}, err
		}
		return EvalToolInvokeResult{OK: false, Error: err.Error()}, nil
	}
	return parseCallEnvelope(reply, "JavaScript"), nil
}

func parseDescribeEnvelope(env map[string]any, lang EvalLanguage, langLabel string) (EvalToolDescribeResult, error) {
	if env == nil {
		return EvalToolDescribeResult{}, fmt.Errorf("%s tool describe request returned an invalid response", langLabel)
	}
	okVal, _ := env["ok"].(bool)
	if !okVal {
		if errMsg, _ := env["error"].(string); errMsg != "" {
			return EvalToolDescribeResult{}, errors.New(errMsg)
		}
		return EvalToolDescribeResult{}, fmt.Errorf("%s tool describe request returned an invalid response", langLabel)
	}
	tools := make([]EvalToolDescriptor, 0)
	if rawTools, ok := env["tools"].([]any); ok {
		for _, item := range rawTools {
			rec, ok := item.(map[string]any)
			if !ok {
				continue
			}
			name, okName := rec["name"].(string)
			desc, okDesc := rec["description"].(string)
			params, okParams := rec["parameters"].(map[string]any)
			if !okName || !okDesc || !okParams {
				continue
			}
			tools = append(tools, EvalToolDescriptor{
				Name:        name,
				Description: desc,
				Parameters:  params,
				Language:    lang,
			})
		}
	}
	missing := make([]string, 0)
	if rawMissing, ok := env["missing"].([]any); ok {
		for _, item := range rawMissing {
			if s, ok := item.(string); ok {
				missing = append(missing, s)
			}
		}
	}
	return EvalToolDescribeResult{Tools: tools, Missing: missing}, nil
}

func parseCallEnvelope(env map[string]any, langLabel string) EvalToolInvokeResult {
	if env == nil {
		return EvalToolInvokeResult{OK: false, Error: fmt.Sprintf("%s tool call returned an invalid response", langLabel)}
	}
	okVal, _ := env["ok"].(bool)
	if !okVal {
		if errMsg, _ := env["error"].(string); errMsg != "" {
			return EvalToolInvokeResult{OK: false, Error: errMsg}
		}
		return EvalToolInvokeResult{OK: false, Error: fmt.Sprintf("%s tool call returned an invalid response", langLabel)}
	}
	val, hasVal := env["value"]
	if !hasVal {
		return EvalToolInvokeResult{OK: false, Error: fmt.Sprintf("%s tool call returned an invalid response", langLabel)}
	}
	return EvalToolInvokeResult{OK: true, Value: val}
}
