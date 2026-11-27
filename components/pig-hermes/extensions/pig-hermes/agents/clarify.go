package agents

import (
	"fmt"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const (
	clarifyOtherOption = "Other (type your answer)"
	clarifyDoneOption  = "Done (submit selection)"
	clarifyMaxLoops    = 64
)

// clarifyUI abstracts the interactive UI methods of sdk.Context for unit testing.
type clarifyUI interface {
	HasUI() bool
	Select(title string, options []string) (any, bool, error)
	Input(title, placeholder string) (any, bool, error)
}

func registerClarifyTool(ext *sdk.Extension) {
	ext.RegisterTool(sdk.ToolDefinition{
		Name:          "clarify",
		Label:         "Clarify",
		Description:   "Ask the user a clarifying question (multiple-choice or open-ended). In headless/non-interactive mode, returns a structured notice without blocking.",
		PromptSnippet: "Ask the user a clarifying question with optional choices using clarify.",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"question": map[string]any{
					"type":        "string",
					"description": "The question to ask the user.",
				},
				"choices": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Optional list of choices for multiple-choice selection.",
				},
				"allow_multiple": map[string]any{
					"type":        "boolean",
					"description": "When true and choices are provided, allow selecting multiple choices.",
				},
				"default": map[string]any{
					"type":        "string",
					"description": "Optional default answer used as placeholder or fallback in non-interactive mode.",
				},
			},
			"required": []string{"question"},
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			return executeClarify(ctx, params), nil
		},
	})
}

func executeClarify(ui clarifyUI, params map[string]any) sdk.ToolResult {
	question := strings.TrimSpace(paramString(params, "question"))
	if question == "" {
		return errorResult("clarify requires a non-empty 'question'")
	}
	choices := paramStringSlice(params, "choices")
	allowMultiple, _ := paramBool(params, "allow_multiple")
	defaultVal := strings.TrimSpace(paramString(params, "default"))

	if ui == nil || !ui.HasUI() {
		resp := map[string]any{
			"question": question,
			"outcome":  "non_interactive",
			"answered": false,
			"notice":   "Interactive UI is not available in headless/print mode. Proceed using best judgment or the provided default.",
		}
		if len(choices) > 0 {
			resp["choices_offered"] = choices
		}
		if defaultVal != "" {
			resp["default"] = defaultVal
			resp["user_response"] = defaultVal
		}
		return jsonResult(resp, false)
	}

	if len(choices) == 0 {
		val, ok, err := ui.Input(question, defaultVal)
		if err != nil {
			return errorResult(fmt.Sprintf("failed to get user input: %v", err))
		}
		if !ok {
			return clarifyCancelledResult(question, nil, defaultVal)
		}
		answer := strings.TrimSpace(fmt.Sprintf("%v", val))
		if answer == "" && defaultVal != "" {
			answer = defaultVal
		}
		return jsonResult(map[string]any{
			"question":      question,
			"outcome":       "submitted",
			"answered":      true,
			"user_response": answer,
		}, false)
	}

	if !allowMultiple {
		options := make([]string, 0, len(choices)+1)
		options = append(options, choices...)
		options = append(options, clarifyOtherOption)

		val, ok, err := ui.Select(question, options)
		if err != nil {
			return errorResult(fmt.Sprintf("failed to get user selection: %v", err))
		}
		if !ok {
			return clarifyCancelledResult(question, choices, defaultVal)
		}
		selected := strings.TrimSpace(fmt.Sprintf("%v", val))
		if selected == clarifyOtherOption {
			customVal, customOK, err := ui.Input(question, defaultVal)
			if err != nil {
				return errorResult(fmt.Sprintf("failed to get custom user input: %v", err))
			}
			if !customOK {
				return clarifyCancelledResult(question, choices, defaultVal)
			}
			selected = strings.TrimSpace(fmt.Sprintf("%v", customVal))
			if selected == "" && defaultVal != "" {
				selected = defaultVal
			}
		}
		return jsonResult(map[string]any{
			"question":        question,
			"choices_offered": choices,
			"outcome":         "submitted",
			"answered":        true,
			"user_response":   selected,
		}, false)
	}

	// Multi-select loop over sdk.Context.Select.
	selectedSet := make(map[int]bool, len(choices))
	for range clarifyMaxLoops {
		options := make([]string, 0, len(choices)+1)
		for idx, ch := range choices {
			prefix := "[ ] "
			if selectedSet[idx] {
				prefix = "[x] "
			}
			options = append(options, prefix+ch)
		}
		options = append(options, clarifyDoneOption)

		val, ok, err := ui.Select(question+" (toggle items, then Done)", options)
		if err != nil {
			return errorResult(fmt.Sprintf("failed to get user selection: %v", err))
		}
		if !ok {
			return clarifyCancelledResult(question, choices, defaultVal)
		}
		picked := strings.TrimSpace(fmt.Sprintf("%v", val))
		if picked == clarifyDoneOption {
			break
		}
		matched := false
		for idx, ch := range choices {
			if picked == "[ ] "+ch || picked == "[x] "+ch || picked == ch {
				selectedSet[idx] = !selectedSet[idx]
				matched = true
				break
			}
		}
		if !matched {
			// Host returned a custom value directly.
			return jsonResult(map[string]any{
				"question":        question,
				"choices_offered": choices,
				"outcome":         "submitted",
				"answered":        true,
				"user_response":   []string{picked},
			}, false)
		}
	}

	chosen := make([]string, 0, len(selectedSet))
	for idx, ch := range choices {
		if selectedSet[idx] {
			chosen = append(chosen, ch)
		}
	}
	if len(chosen) == 0 && defaultVal != "" {
		chosen = []string{defaultVal}
	}
	return jsonResult(map[string]any{
		"question":        question,
		"choices_offered": choices,
		"outcome":         "submitted",
		"answered":        true,
		"user_response":   chosen,
	}, false)
}

func clarifyCancelledResult(question string, choices []string, defaultVal string) sdk.ToolResult {
	resp := map[string]any{
		"question": question,
		"outcome":  "cancelled",
		"answered": false,
	}
	if len(choices) > 0 {
		resp["choices_offered"] = choices
	}
	if defaultVal != "" {
		resp["default"] = defaultVal
	}
	return jsonResult(resp, false)
}
