package ask_user_question

// toolParameters is the tool's JSON Schema, the shape TypeBox emits for the original's QuestionParamsSchema.
// The host validates the model's arguments against it, so the limits here are the limits the model is held to.
// upstream: tool/types.ts.
func toolParameters() map[string]any {
	str := func(description string, extra map[string]any) map[string]any {
		m := map[string]any{"type": "string", "description": description}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	optionSchema := map[string]any{
		"type":     "object",
		"required": []string{"label", "description"},
		"properties": map[string]any{
			"label": str("MAX 60 CHARACTERS — hard limit, requests over the limit are rejected. The display text for this option that the user will see and select. Should be concise (1-5 words) and clearly describe the choice.",
				map[string]any{"maxLength": maxLabelLength}),
			"description": str("Explanation of what this option means or what will happen if chosen. Useful for providing context about trade-offs or implications.", nil),
			"preview":     str("Optional preview content rendered when this option is focused. Use for mockups, code snippets, or visual comparisons that help users compare options. See the tool description for the expected content format.", nil),
		},
	}
	questionSchema := map[string]any{
		"type":     "object",
		"required": []string{"question", "header", "options"},
		"properties": map[string]any{
			"question": str(`The complete question to ask the user. Should be clear, specific, and end with a question mark. Example: "Which library should we use for date formatting?" If multiSelect is true, phrase it accordingly, e.g. "Which features do you want to enable?"`, nil),
			"header": str(`MAX 16 CHARACTERS — hard limit, requests over the limit are rejected. Very short chip/tag shown next to the question. Examples: "Auth method", "Library", "Approach".`,
				map[string]any{"maxLength": maxHeaderLength}),
			"options": map[string]any{
				"type":        "array",
				"items":       optionSchema,
				"minItems":    minOptions,
				"maxItems":    maxOptions,
				"description": "The available choices for this question. Must have 2-4 options. Each option should be a distinct, mutually exclusive choice (unless multiSelect is enabled). The 'Type something.' row is appended automatically — do NOT author it.",
			},
			"multiSelect": map[string]any{
				"type":        "boolean",
				"default":     false,
				"description": "Set to true to allow the user to select multiple options instead of just one. Use when choices are not mutually exclusive.",
			},
		},
	}
	return map[string]any{
		"type":     "object",
		"required": []string{"questions"},
		"properties": map[string]any{
			"questions": map[string]any{
				"type":        "array",
				"items":       questionSchema,
				"minItems":    1,
				"maxItems":    maxQuestions,
				"description": "Questions to ask the user (1-4 questions)",
			},
		},
	}
}
