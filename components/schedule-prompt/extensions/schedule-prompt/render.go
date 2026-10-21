package schedule_prompt

import (
	"strings"
	"unicode/utf16"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	tui "github.com/MichaelKinsy/pigpen/components/pitui"
	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/jsobj"
)

// Renderers: the scheduled_prompt message (upstream: index.ts:32-71) and the tool call and result (upstream: tool.ts:288-377).

func mapStr(m map[string]any, key string) (string, bool) {
	v, ok := m[key]
	if !ok || v == nil {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		return jsStr(v), true
	}
	return s, true
}

// orDefault is `m[key] || fallback` for string members.
func orDefault(m map[string]any, key, fallback string) string {
	if v, ok := m[key]; ok && truthy(v) {
		return jsStr(v)
	}
	return fallback
}

func renderText(line string, width int) []string { return tui.NewText(line, 0, 0, nil).Render(width) }

// renderScheduledPrompt draws the marker the scheduler posts when a job fires (and, for a subagent job, when it finishes).
func renderScheduledPrompt(ctx sdk.Context, message map[string]any, _ sdk.MessageRenderOptions, width int) ([]string, error) {
	return scheduledPromptLines(ctx.UITheme(), message, width), nil
}

func scheduledPromptLines(theme viewTheme, message map[string]any, width int) []string {
	details, _ := message["details"].(map[string]any)
	jobName := orDefault(details, "jobName", "Unknown")
	prompt := orDefault(details, "prompt", "")
	tag := ""
	if model := orDefault(details, "model", ""); model != "" {
		tag = " (subagent: " + model + ")"
	}
	var line string
	switch mode, _ := details["mode"].(string); mode {
	case "subagent_start":
		line = theme.Fg("accent", "🕐 Scheduled"+tag+": "+jobName)
		if prompt != "" {
			line += theme.Fg("dim", ` → "`+prompt+`"`)
		}
	case "subagent_done":
		line = theme.Fg("accent", "✓ Scheduled"+tag+" finished: "+jobName)
		if out := orDefault(details, "output", ""); out != "" {
			line += theme.Fg("dim", " → "+out)
		}
	case "subagent_error":
		line = theme.Fg("error", "✗ Scheduled"+tag+" failed: "+jobName)
		if e := orDefault(details, "error", ""); e != "" {
			line += theme.Fg("dim", " → "+e)
		}
	default:
		line = theme.Fg("accent", "🕐 Scheduled: "+jobName)
		if prompt != "" {
			line += theme.Fg("dim", ` → "`+prompt+`"`)
		}
	}
	return renderText(line, width)
}

// capitalize is `s.charAt(0).toUpperCase() + s.slice(1)` over UTF-16 units: on a colored string the first unit is the escape character.
func capitalize(s string) string {
	u := utf16.Encode([]rune(s))
	if len(u) == 0 {
		return s
	}
	first := strings.ToUpper(string(utf16.Decode(u[:1])))
	return first + string(utf16.Decode(u[1:]))
}

func renderToolCall(ctx sdk.Context, args map[string]any, _ sdk.ToolRenderContext, width int) ([]string, error) {
	return toolCallLines(ctx.UITheme(), args, width), nil
}

func toolCallLines(theme viewTheme, args map[string]any, width int) []string {
	action := jsStr(args["action"])
	name := orDefault(args, "name", orDefault(args, "jobId", ""))
	actionText := theme.Fg("accent", action)
	nameText := theme.Fg("text", name)
	var text string
	switch action {
	case "add":
		text = "Adding cron job: " + nameText
	case "remove":
		text = "Removing cron job: " + nameText
	case "enable", "disable":
		text = capitalize(actionText) + " job: " + nameText
	case "update":
		text = "Updating cron job: " + nameText
	case "list":
		text = "Listing all cron jobs"
	default:
		text = actionText + " cron job"
	}
	return renderText(text, width)
}

func renderToolResult(ctx sdk.Context, result sdk.ToolRenderResult, _ sdk.ToolRenderResultOptions, _ sdk.ToolRenderContext, width int) ([]string, error) {
	return toolResultLines(ctx.UITheme(), result, width), nil
}

func toolResultLines(theme viewTheme, result sdk.ToolRenderResult, width int) []string {
	details, ok := detailsMap(result.Details)
	if !ok {
		var parts []string
		for _, c := range result.Content {
			if c["type"] == "text" {
				parts = append(parts, jsStr(c["text"]))
			} else {
				parts = append(parts, "")
			}
		}
		return renderText(strings.Join(parts, "\n"), width)
	}
	var lines []string
	if truthy(details["error"]) {
		lines = append(lines, theme.Fg("error", "✗ Error: "+jsStr(details["error"])))
	} else {
		jobName := orDefault(details, "jobName", orDefault(details, "jobId", ""))
		lines = append(lines, theme.Fg("success", "✓ "+jsStr(details["action"])+" "+jobName))
	}
	jobs, _ := details["jobs"].([]any)
	if details["action"] == "list" && len(jobs) > 0 {
		lines = append(lines, "", theme.Bold("Cron Jobs:"))
		for _, j := range jobs {
			job, _ := detailsMap(j)
			status := theme.Fg("muted", "✗")
			if truthy(job["enabled"]) {
				status = theme.Fg("success", "✓")
			}
			lines = append(lines, status+" "+theme.Fg("text", jsStr(job["name"]))+" "+theme.Fg("dim", "("+jsStr(job["id"])+")"))
			lines = append(lines, "  "+theme.Fg("dim", "Type:")+" "+jsStr(job["type"])+" "+theme.Fg("dim", "| Schedule:")+" "+jsStr(job["schedule"]))
			if truthy(job["model"]) {
				subagentTag := "(subagent)"
				if truthy(job["notify"]) {
					subagentTag = "(subagent, notifies parent)"
				}
				var tags []string
				if truthy(job["extensions"]) {
					tags = append(tags, "extensions")
				}
				if truthy(job["skills"]) {
					tags = append(tags, "skills")
				}
				tagStr := ""
				if len(tags) > 0 {
					tagStr = ", " + strings.Join(tags, " + ")
				}
				lines = append(lines, "  "+theme.Fg("dim", "Model:")+" "+jsStr(job["model"])+" "+theme.Fg("dim", subagentTag)+tagStr)
			}
			lines = append(lines, "  "+theme.Fg("dim", "Prompt:")+" "+jsStr(job["prompt"]))
			if truthy(job["lastRun"]) {
				lines = append(lines, "  "+theme.Fg("dim", "Last run:")+" "+jsStr(job["lastRun"]))
			}
			lines = append(lines, "  "+theme.Fg("dim", "Runs:")+" "+jsStr(job["runCount"]))
		}
	}
	return renderText(strings.Join(lines, "\n"), width)
}

// detailsMap reads the details of a tool result as it comes back from the host: a decoded JSON object.
func detailsMap(v any) (map[string]any, bool) {
	switch t := v.(type) {
	case map[string]any:
		return t, true
	case *jsobj.Object:
		m, _ := jsobj.ToGo(t).(map[string]any)
		return m, true
	}
	return nil, false
}
