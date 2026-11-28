package ansi

import (
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

// CallFn builds the kit node tree for a tool call invocation.
type CallFn func(ctx sdk.Context, args map[string]any, rc sdk.ToolRenderContext, width int) (kit.Node, error)

// ResultFn builds the kit node tree for a tool result (partial or final).
type ResultFn func(ctx sdk.Context, res sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, width int) (kit.Node, error)

// Builder holds the call and result node builders for a single tool on one surface.
type Builder struct {
	Call   CallFn
	Result ResultFn
}

var (
	buildersMu       sync.RWMutex
	terminalBuilders = map[string]Builder{}
	nativeBuilders   = map[string]Builder{}
)

// registerTerminal registers the terminal-surface builder for tool.
// Called from init() in term_*.go files.
func registerTerminal(tool string, b Builder) {
	buildersMu.Lock()
	terminalBuilders[tool] = b
	buildersMu.Unlock()
}

// registerNative registers the Tern native-surface builder for tool.
// Called from init() in native_*.go files.
func registerNative(tool string, b Builder) {
	buildersMu.Lock()
	nativeBuilders[tool] = b
	buildersMu.Unlock()
}

func lookupTerminal(tool string) (Builder, bool) {
	buildersMu.RLock()
	b, ok := terminalBuilders[tool]
	buildersMu.RUnlock()
	return b, ok
}

func lookupNative(tool string) (Builder, bool) {
	buildersMu.RLock()
	b, ok := nativeBuilders[tool]
	buildersMu.RUnlock()
	return b, ok
}

func hasBuilder(tool string) bool {
	buildersMu.RLock()
	_, okT := terminalBuilders[tool]
	_, okN := nativeBuilders[tool]
	buildersMu.RUnlock()
	return okT || okN
}
