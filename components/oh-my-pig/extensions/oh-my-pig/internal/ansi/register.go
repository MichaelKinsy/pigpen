package ansi

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/intent"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/surface"
)

// nonMergedTools are omp tools that do NOT set mergeCallAndResult: true, or
// render call and result separately (think has no result; report_issue and
// default tools show call then result, though PiG stacks both so we follow omp).
var nonMergedTools = map[string]bool{
	"think": true,
}

func init() {
	ensureSurfaceListener()
}

func trackCardInvalidate(ctx sdk.Context, rc sdk.ToolRenderContext) (*chromeState, uint64) {
	st := chromeSession(ctx)
	if st == nil {
		return nil, 0
	}
	return st, st.trackCardInvalidate(rc)
}

// isDisplaceableTodoResult mirrors packages/tui/src/chat/tool-execution.ts:94-101,119-128:
// a non-error, non-partial todo result whose details carry a `phases` slice
// supersedes any earlier live todo card in the session transcript.
func isDisplaceableTodoResult(res sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext) bool {
	if rc.IsError || opts.IsPartial {
		return false
	}
	details := asMap(res.Details)
	if details == nil {
		return false
	}
	_, hasPhases := details["phases"].([]any)
	return hasPhases
}

// RenderersForTool resolves toolName to a *sdk.ToolRenderers that picks the
// native builder when surface.Native() and a native builder is registered,
// otherwise the terminal builder, otherwise nil.
func RenderersForTool(toolName string) *sdk.ToolRenderers {
	if !hasBuilder(toolName) {
		return nil
	}
	merge := !nonMergedTools[toolName]
	r := &sdk.ToolRenderers{
		Shell: sdk.ToolRenderShellSelf,
		CallView: func(ctx sdk.Context, args map[string]any, rc sdk.ToolRenderContext, width int) (kit.View, error) {
			st, cardID := trackCardInvalidate(ctx, rc)
			if toolName == "todo" && st.isTodoDisplaced(cardID) {
				return kit.View{Root: kit.NewContainer()}, nil
			}
			if merge && (!rc.IsPartial || (rc.State != nil && rc.State["hasResult"] == true)) {
				return kit.View{Root: kit.NewContainer()}, nil
			}
			b, ok := pickBuilder(toolName)
			if !ok || b.Call == nil {
				return kit.View{Root: kit.NewContainer()}, nil
			}
			cleanArgs := sanitizeMap(args)
			node, err := b.Call(ctx, cleanArgs, rc, width)
			if err != nil {
				return kit.View{}, err
			}
			if node == nil {
				return kit.View{Root: kit.NewContainer()}, nil
			}
			return kit.View{Root: node}, nil
		},
		ResultView: func(ctx sdk.Context, res sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, width int) (kit.View, error) {
			st, cardID := trackCardInvalidate(ctx, rc)
			if merge && rc.State != nil && rc.State["hasResult"] != true {
				rc.State["hasResult"] = true
				if opts.IsPartial && rc.Invalidate != nil {
					rc.Invalidate()
				}
			}
			if toolName == "todo" {
				displaceable := isDisplaceableTodoResult(res, opts, rc)
				if st.recordTodoSnapshot(cardID, displaceable) {
					return kit.View{Root: kit.NewContainer()}, nil
				}
			}
			b, ok := pickBuilder(toolName)
			if !ok || b.Result == nil {
				return kit.View{Root: kit.NewContainer()}, nil
			}
			cleanRes := sanitizeToolResult(res)
			cleanRC := rc
			cleanRC.Args = sanitizeMap(rc.Args)
			node, err := b.Result(ctx, cleanRes, opts, cleanRC, width)
			if err != nil {
				return kit.View{}, err
			}
			if node == nil {
				return kit.View{Root: kit.NewContainer()}, nil
			}
			return kit.View{Root: node}, nil
		},
	}
	return r
}

func pickBuilder(toolName string) (Builder, bool) {
	if surface.Native() {
		if b, ok := lookupNative(toolName); ok {
			return b, true
		}
	}
	if b, ok := lookupTerminal(toolName); ok {
		return b, true
	}
	if b, ok := lookupNative(toolName); ok {
		return b, true
	}
	return Builder{}, false
}

// sanitizeToolResult deep-sanitizes Details and text blocks of Content
// (stripping ANSI/OSC/CSI, unsafe C0, DEL, and C1 while preserving `\r` so
// `sanitizeDisplayLines` can still collapse bare-CR progress overwrites, and
// keeping image base64 blocks untouched).
func sanitizeToolResult(res sdk.ToolRenderResult) sdk.ToolRenderResult {
	out := res
	if res.Details != nil {
		out.Details = sanitizeDeep(res.Details)
	}
	if len(res.Content) > 0 {
		cleanContent := make([]map[string]any, len(res.Content))
		for i, blk := range res.Content {
			if blk == nil || strField(blk, "type") != "text" {
				cleanContent[i] = blk
				continue
			}
			raw, ok := blk["text"].(string)
			if !ok {
				cleanContent[i] = blk
				continue
			}
			cleaned := sanitizeContentText(raw)
			if cleaned == raw {
				cleanContent[i] = blk
				continue
			}
			cp := make(map[string]any, len(blk))
			for k, v := range blk {
				cp[k] = v
			}
			cp["text"] = cleaned
			cleanContent[i] = cp
		}
		out.Content = cleanContent
	}
	return out
}

func Register(e *sdk.Extension) {
	// Re-subscribe if tests called surface.ResetForTests().
	resetSurfaceListener()
	lifecycle.OnEnd("ansi", releaseChromeSession)

	e.ToolRenderer(func(toolName string, next func() *sdk.ToolRenderers) *sdk.ToolRenderers {
		if r := RenderersForTool(toolName); r != nil {
			return r
		}
		return next()
	})

	e.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		if isTopLevelSession(ctx) {
			surface.ClearTurnUsage()
		}
		if st := beginChromeSession(ctx); st != nil {
			st.installChrome(ctx)
		}
		return nil, nil
	})

	e.OnEvent(sdk.EventAgentStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
		st := chromeSession(ctx)
		if st != nil {
			st.startTurn()
		}
		if ctx.HasUI() {
			ctx.SetWorkingMessage(defaultWorkingMessage)
			if st != nil {
				st.refreshFooter(ctx)
			}
		}
		return nil, nil
	})

	e.OnEvent(sdk.EventToolExecutionStart, func(ctx sdk.Context, data map[string]any) (any, error) {
		if !ctx.HasUI() {
			return nil, nil
		}
		st := chromeSession(ctx)
		if st == nil {
			return nil, nil
		}
		if label := intent.WorkingLabelFromEvent(ctx, data); label != "" {
			label = truncateWidth(flattenLine(label), truncateContent)
			if st.setIntent(label) {
				ctx.SetWorkingMessage(label)
			}
		}
		return nil, nil
	})

	e.OnEvent(sdk.EventToolExecutionEnd, func(ctx sdk.Context, data map[string]any) (any, error) {
		if strField(data, "toolName") == "todo" {
			if st := chromeSession(ctx); st != nil {
				st.markBandOrderDirty()
			}
		}
		return nil, nil
	})

	e.OnEvent(sdk.EventTurnEnd, func(ctx sdk.Context, data map[string]any) (any, error) {
		if st := chromeSession(ctx); st != nil {
			st.onTurnEnd(ctx, data)
		}
		return nil, nil
	})

	e.OnEvent(sdk.EventAgentEnd, func(ctx sdk.Context, _ map[string]any) (any, error) {
		st := chromeSession(ctx)
		if st != nil {
			st.endTurn(ctx)
		}
		if ctx.HasUI() {
			ctx.ClearWorkingMessage()
			if st != nil {
				st.refreshFooter(ctx)
			}
		}
		return nil, nil
	})

	e.OnEvent(sdk.EventModelSelect, func(ctx sdk.Context, _ map[string]any) (any, error) {
		if st := chromeSession(ctx); st != nil {
			st.refreshModel(ctx)
		}
		return nil, nil
	})

	e.OnEvent(sdk.EventThinkingLevelSelect, func(ctx sdk.Context, _ map[string]any) (any, error) {
		if st := chromeSession(ctx); st != nil {
			st.refreshThinking(ctx)
		}
		return nil, nil
	})

	e.OnEvent(sdk.EventSessionInfoChanged, func(ctx sdk.Context, _ map[string]any) (any, error) {
		if st := chromeSession(ctx); st != nil {
			st.refreshSessionInfo(ctx)
		}
		return nil, nil
	})
}
