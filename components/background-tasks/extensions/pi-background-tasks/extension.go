// Package pi_background_tasks is a Go port (partial: see port/PORT.md and the refusals in refuse.go) of
// @vanillagreen/pi-background-tasks 2.2.0, a Pi extension for explicit non-blocking background shell tasks, log tails
// and completion wakeups.
package pi_background_tasks

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const (
	extensionID = "pi-background-tasks"
	bgCommand   = "bg"

	defaultBackgroundBashShortcut = "alt+."
	defaultDashboardShortcut      = "alt+shift+h"
	defaultWidgetToggleShortcut   = "alt+h"
)

//go:embed tooldefs.json
var tooldefsJSON []byte

//go:embed instructions.md
var instructions string

type toolDef struct {
	Label            string
	Description      string
	PromptSnippet    string
	PromptGuidelines []string
	Parameters       sdk.Schema
}

// orderedValue reads one JSON value, keeping the key order of every object (TypeBox's order, which the model sees).
func orderedValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			var pairs []any
			for dec.More() {
				key, err := dec.Token()
				if err != nil {
					return nil, err
				}
				value, err := orderedValue(dec)
				if err != nil {
					return nil, err
				}
				pairs = append(pairs, key.(string), value)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return sdk.NewOrderedObject(pairs...), nil
		case '[':
			list := []any{}
			for dec.More() {
				value, err := orderedValue(dec)
				if err != nil {
					return nil, err
				}
				list = append(list, value)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return list, nil
		}
	}
	return tok, nil
}

// toolDefs reads the original's tool definitions, dumped from the unmodified package (tooldefs.json).
func toolDefs() map[string]toolDef {
	var raw map[string]struct {
		Label            string          `json:"label"`
		Description      string          `json:"description"`
		PromptSnippet    string          `json:"promptSnippet"`
		PromptGuidelines []string        `json:"promptGuidelines"`
		Parameters       json.RawMessage `json:"parameters"`
	}
	if err := json.Unmarshal(tooldefsJSON, &raw); err != nil {
		panic("pi-background-tasks: tooldefs.json: " + err.Error())
	}
	out := map[string]toolDef{}
	for name, d := range raw {
		dec := json.NewDecoder(bytes.NewReader(d.Parameters))
		top, err := orderedValue(dec)
		if err != nil && err != io.EOF {
			panic("pi-background-tasks: tooldefs.json: " + err.Error())
		}
		obj := top.(*sdk.OrderedObject)
		schema := sdk.Schema{}
		for _, k := range obj.Keys() {
			v, _ := obj.Get(k)
			schema[k] = v
		}
		out[name] = toolDef{Label: d.Label, Description: d.Description, PromptSnippet: d.PromptSnippet, PromptGuidelines: d.PromptGuidelines, Parameters: schema}
	}
	return out
}

// refuseTool is the Execute of a tool whose action the port does not run yet.
func refuseTool(tool string) sdk.ToolFunc {
	return func(ctx sdk.Context, params map[string]any) (any, error) {
		action, _ := params["action"].(string)
		return nil, fmt.Errorf("%s", refusal("the `"+tool+"` tool (action `"+action+"`)"))
	}
}

// Extension returns the port.
func Extension() *sdk.Extension {
	e := sdk.New(extensionID)
	// upstream: background-tasks.ts:129. A package that is switched off registers nothing; the setting is read from the
	// user settings at load, as the original does (no project is trusted yet).
	if !settingBoolean("enabled", true, "") {
		return e
	}
	defs := toolDefs()
	// The original registers bg_status first.
	for _, name := range []string{"bg_status", "bg_task"} {
		d := defs[name]
		e.RegisterTool(sdk.ToolDefinition{
			Name: name, Label: d.Label, Description: d.Description, PromptSnippet: d.PromptSnippet, PromptGuidelines: d.PromptGuidelines,
			Parameters: d.Parameters, RenderShell: sdk.ToolRenderShellSelf, Execute: refuseTool(name),
		})
	}

	command := func(feature string) sdk.CommandFunc {
		return func(ctx sdk.Context, args string) error {
			ctx.Notify(refusal(feature), "warning")
			return nil
		}
	}
	e.RegisterCommand(bgCommand, sdk.CommandOptions{Description: "Background shell task dashboard and controls.", Handler: command("the `/bg` command")})
	e.Command(bgCommand+":list", "Show tracked background tasks", command("the `/bg:list` command"))
	e.Command(bgCommand+":next", "Move the next bash command to a background task", command("the `/bg:next` command"))
	e.Command(bgCommand+":clear", "Remove finished background tasks", command("the `/bg:clear` command"))
	e.Command(bgCommand+":run", "Spawn a background shell task: /bg:run <command>", command("the `/bg:run` command"))
	e.Command(bgCommand+":stop", "Terminate a running background task: /bg:stop <id>", command("the `/bg:stop` command"))

	// upstream: registrations.ts:276-309. Shortcuts register at load from the settings read then; "none" disables one.
	dashboard := settingString("dashboardShortcut", defaultDashboardShortcut, "")
	backgroundBash := settingString("backgroundBashShortcut", defaultBackgroundBashShortcut, "")
	widgetToggle := settingString("widgetToggleShortcut", defaultWidgetToggleShortcut, "")
	shortcut := func(key, description, feature string) {
		e.Shortcut(key, description, func(ctx sdk.Context) error {
			ctx.Notify(refusal(feature), "warning")
			return nil
		})
	}
	if dashboard != "none" {
		shortcut(dashboard, "Open the background task dashboard", "the background task dashboard")
	}
	if strings.ToLower(dashboard) != "f5" {
		shortcut("f5", "Open the background task dashboard", "the background task dashboard")
	}
	if backgroundBash != "none" {
		shortcut(backgroundBash, "Move the next not-yet-started bash command to a background task", "forcing the next bash command into the background")
	}
	if widgetToggle != "none" {
		shortcut(widgetToggle, "Toggle background task mini-dashboard", "the background task widget")
	}

	e.OnEvent(sdk.EventSessionStart, func(ctx sdk.Context, data map[string]any) (any, error) {
		settings.clear()
		return nil, nil
	})
	e.OnEvent(sdk.EventSessionShutdown, func(ctx sdk.Context, data map[string]any) (any, error) {
		settings.clear()
		return nil, nil
	})

	// A bash command the original would move into a background task is refused instead of running in the foreground.
	e.OnEvent(sdk.EventToolCall, func(ctx sdk.Context, data map[string]any) (any, error) {
		if name, _ := data["toolName"].(string); name != "bash" {
			return nil, nil
		}
		input, _ := data["input"].(map[string]any)
		command, _ := input["command"].(string)
		if reason := bashRefusal(ctx, command); reason != "" {
			return map[string]any{"block": true, "reason": reason}, nil
		}
		return nil, nil
	})
	// The same for a command the user typed (`!command`): its result is the refusal.
	e.OnEvent(sdk.EventUserBash, func(ctx sdk.Context, data map[string]any) (any, error) {
		command, _ := data["command"].(string)
		if reason := bashRefusal(ctx, command); reason != "" {
			return map[string]any{"result": map[string]any{"output": reason, "exitCode": 1, "cancelled": false, "truncated": false}}, nil
		}
		return nil, nil
	})
	return e
}

// bashRefusal is the refusal text for a bash command that auto-backgrounding would divert, or "" when it may run.
func bashRefusal(ctx sdk.Context, command string) string {
	if jsTrim(command) == "" {
		return ""
	}
	cwd := ctx.Cwd()
	settings.recordProjectTrust(cwd, ctx.IsProjectTrusted)
	if !settingBoolean("autoBackgroundBash", true, cwd) {
		return ""
	}
	if hasUserPatterns(settingString("autoBackgroundPatterns", "", cwd)) {
		return refusal("the `autoBackgroundPatterns` setting")
	}
	if d := builtinAutoBackgroundDecision(command); d != nil {
		return refusal("moving a bash command into a background task (" + d.reason + ")")
	}
	return ""
}
