package readashboard

import (
	"fmt"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// resolveRenderers draws direct calls to REA's tools (mcp__rea__*) as compact cards and leaves every other tool
// to PiG. Calls nested in a codemode script render inside the codemode card; the widget and the timeline cover
// those.
func resolveRenderers(toolName string, next func() *sdk.ToolRenderers) *sdk.ToolRenderers {
	if !isREA(toolName) {
		return next()
	}
	tool := strings.TrimPrefix(toolName, toolPrefix)
	return &sdk.ToolRenderers{
		Call: func(_ sdk.Context, args map[string]any, _ sdk.ToolRenderContext, width int) ([]string, error) {
			return renderCall(tool, args, width), nil
		},
		Result: func(_ sdk.Context, result sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, width int) ([]string, error) {
			return renderResult(result, opts, rc, width), nil
		},
	}
}

// toolLabel is family/tool, or just the tool when the tool is its own family.
func toolLabel(tool string) string {
	if f := family(tool); f != tool {
		return f + "/" + tool
	}
	return tool
}

func renderCall(tool string, args map[string]any, width int) []string {
	width = max(width, 1)
	segs := []seg{{"REA ", styCyan}, {toolLabel(tool), styBold}}
	if t := extractTarget(args); t != "" {
		space := width - segsWidth(segs) - 2
		if space >= 4 {
			segs = append(segs, seg{"  " + tailFit(t, space), styDim})
		}
	}
	return []string{renderSegs(segs, width)}
}

func renderResult(result sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, width int) []string {
	width = max(width, 1)
	if opts.IsPartial || rc.IsPartial {
		return []string{renderSegs([]seg{{"◐ running…", styCyan}}, width)}
	}
	blocks := make([]any, len(result.Content))
	for i, b := range result.Content {
		blocks[i] = b
	}
	sum := summarizeResult(map[string]any{"content": blocks})
	var head []seg
	if rc.IsError {
		head = []seg{{"✗ error", styRed}}
		if sum.Error != "" {
			head = append(head, seg{": " + sum.Error, ""})
		}
	} else {
		head = []seg{{"✓ ok", styGreen}}
		if sum.Counted {
			head = append(head, seg{fmt.Sprintf(" · %s · %s", plural(sum.Evidence, "evidence", "evidence"), plural(sum.Limitations, "limitation", "limitations")), ""})
		}
	}
	if rc.DurationMs != nil {
		head = append(head, seg{" · " + fmtDuration(msDuration(*rc.DurationMs)), styDim})
	}
	lines := []string{renderSegs(head, width)}
	if opts.Expanded || rc.Expanded {
		for _, l := range sum.LimitationSample {
			lines = append(lines, renderSegs([]seg{{"  limitation: " + l, styYellow}}, width))
		}
		for _, e := range sum.EvidenceSample {
			lines = append(lines, renderSegs([]seg{{"  evidence: " + e, styDim}}, width))
		}
	}
	return lines
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
