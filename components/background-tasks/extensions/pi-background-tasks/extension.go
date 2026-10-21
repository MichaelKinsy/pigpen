// Package pi_background_tasks is a Go port (partial: see port/PORT.md and the refusals in refuse.go) of
// @vanillagreen/pi-background-tasks 2.2.0, a Pi extension for explicit non-blocking background shell tasks, log tails
// and completion wakeups.
package pi_background_tasks

import (
	"bytes"
	_ "embed"
	"encoding/json"
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

// instructionsMarker begins the block the original's npm postinstall writes into APPEND_SYSTEM.md (scripts/append-system.mjs).
const instructionsMarker = "<!-- kendex:append-system @vanillagreen/pi-background-tasks begin -->"

// Extension returns the port.
func Extension() *sdk.Extension {
	e := sdk.New(extensionID)
	// upstream: background-tasks.ts:151. A package that is switched off registers nothing; the setting is read from the
	// user settings at load, as the original does (no project is trusted yet).
	if !settingBoolean("enabled", true, "") {
		return e
	}
	rt := newRuntime()
	defs := toolDefs()
	execute := map[string]sdk.ToolFunc{"bg_status": rt.bgStatusTool(), "bg_task": rt.bgTaskTool()}
	// The original registers bg_status first.
	for _, name := range []string{"bg_status", "bg_task"} {
		d := defs[name]
		e.RegisterTool(sdk.ToolDefinition{
			Name: name, Label: d.Label, Description: d.Description, PromptSnippet: d.PromptSnippet, PromptGuidelines: d.PromptGuidelines,
			Parameters: d.Parameters, RenderShell: sdk.ToolRenderShellSelf, Execute: execute[name],
		})
	}

	e.RegisterCommand(bgCommand, sdk.CommandOptions{Description: "Background shell task dashboard and controls.", GetArgumentCompletions: rt.bgCompletions, Handler: rt.bgHandler()})
	rt.simpleCommands(e)

	// upstream: registrations.ts:276-309. Shortcuts register at load from the settings read then; "none" disables one.
	refused := func(feature string) sdk.ShortcutFunc {
		return func(ctx sdk.Context) error {
			ctx.Notify(refusal(feature), "warning")
			return nil
		}
	}
	if rt.dashboardShortcut != "none" {
		e.Shortcut(rt.dashboardShortcut, "Open the background task dashboard", refused("the background task dashboard"))
	}
	if strings.ToLower(rt.dashboardShortcut) != "f5" {
		e.Shortcut("f5", "Open the background task dashboard", refused("the background task dashboard"))
	}
	if rt.backgroundBashShortcut != "none" {
		e.Shortcut(rt.backgroundBashShortcut, "Move the next not-yet-started bash command to a background task", func(ctx sdk.Context) error {
			rt.adopt(ctx)
			rt.armForcedBackground(ctx, "shortcut")
			return nil
		})
	}
	if rt.widgetToggleShortcut != "none" {
		e.Shortcut(rt.widgetToggleShortcut, "Toggle background task mini-dashboard", refused("the background task widget"))
	}

	e.OnEvent(sdk.EventSessionStart, func(ctx sdk.Context, data map[string]any) (any, error) {
		rt.onSessionStart(ctx)
		rt.settle()
		return nil, nil
	})
	e.OnEvent(sdk.EventBeforeAgentStart, func(ctx sdk.Context, data map[string]any) (any, error) {
		rt.mu.Lock()
		settings.recordProjectTrust(ctx.Cwd(), ctx.IsProjectTrusted)
		rt.setContext(ctx)
		rt.mu.Unlock()
		appendInstructions(data)
		return nil, nil
	})
	for _, event := range []string{sdk.EventSessionTree, sdk.EventSessionCompact} {
		e.OnEvent(event, func(ctx sdk.Context, data map[string]any) (any, error) {
			rt.adopt(ctx)
			return nil, nil
		})
	}
	e.OnEvent(sdk.EventSessionShutdown, func(ctx sdk.Context, data map[string]any) (any, error) {
		rt.onSessionShutdown()
		return nil, nil
	})
	e.OnEvent(sdk.EventToolCall, func(ctx sdk.Context, data map[string]any) (any, error) { return rt.onToolCall(ctx, data) })
	e.OnEvent(sdk.EventUserBash, func(ctx sdk.Context, data map[string]any) (any, error) { return rt.onUserBash(ctx, data) })
	return e
}

// appendInstructions adds instructions.md to the system prompt of the run, as the original's install step adds it to
// APPEND_SYSTEM.md: not when that file already carries the block (an install by the original package, or the kendex CLI).
func appendInstructions(data map[string]any) {
	options, _ := data["systemPromptOptions"].(map[string]any)
	if options == nil {
		return
	}
	current, _ := options["appendSystemPrompt"].(string)
	if strings.Contains(current, instructionsMarker) {
		return
	}
	text := strings.TrimSpace(instructions)
	if current != "" {
		text = current + "\n\n" + text
	}
	options["appendSystemPrompt"] = text
}
