package ask_user_question

// The tool's built-in model-facing copy. upstream: ask-user-question.ts DEFAULT_PROMPT_SNIPPET,
// DEFAULT_PROMPT_GUIDELINES and DEFAULT_TOOL_DESCRIPTION, with the limits (4 questions, 2-4 options) filled in.
var (
	defaultPromptSnippet = "Ask the user up to 4 structured questions (2-4 options each) when requirements are ambiguous"

	defaultPromptGuidelines = []string{
		"Use ask_user_question whenever the user's request is underspecified and you cannot proceed without concrete decisions — you can ask up to 4 questions per invocation.",
		"Each question MUST have 2-4 options. Every option requires a concise label (1-5 words) and a description explaining what the choice means or its trade-offs. The user can additionally type a custom answer via the automatically appended \"Type something.\" row on every question, or press Esc to abandon the questionnaire. Do NOT author \"Other\" or \"Type something.\" labels yourself — reserved labels are rejected at runtime.",
		"Set multiSelect: true when multiple answers are valid. Provide an options[].preview markdown string when an option benefits from richer side-by-side context (mockups, code snippets, diagrams, configs) — single-select only. The \"Type something.\" row is appended to every question; in preview mode it expands to the full pane width while typing so the custom answer is not cramped into the narrow options column. If you recommend a specific option, make that the first option and append \"(Recommended)\" to its label.",
		"Do not stack multiple ask_user_question calls back-to-back — group all clarifying questions into one invocation.",
	}

	defaultToolDescription = "Ask the user one or more structured questions during execution. Use when you need to:\n" +
		"1. Gather user preferences or requirements\n" +
		"2. Clarify ambiguous instructions\n" +
		"3. Get decisions on implementation choices as you work\n" +
		"4. Offer choices to the user about what direction to take\n" +
		"\n" +
		"Usage notes:\n" +
		"- Users can type a custom answer via the automatically appended \"Type something.\" row on every question or press Esc to abandon the questionnaire. Do NOT author \"Other\" or \"Type something.\" labels yourself — reserved labels are rejected at runtime.\n" +
		"- Use multiSelect: true when multiple answers are valid. The \"Type something.\" row is available on every question, including when options carry a `preview`; in preview mode it expands to the full pane width while typing so the custom answer is not cramped into the narrow options column.\n" +
		"- If you recommend a specific option, make that the first option in the list and add \"(Recommended)\" at the end of the label.\n" +
		"\n" +
		"Preview feature:\n" +
		"Use the optional `preview` field on options when presenting concrete artifacts that users need to visually compare:\n" +
		"- ASCII mockups of UI layouts or components\n" +
		"- Code snippets showing different implementations\n" +
		"- Diagram variations\n" +
		"- Configuration examples\n" +
		"\n" +
		"Preview content is rendered as markdown in a monospace box. Multi-line text with newlines is supported. When any option has a preview, the UI switches to a side-by-side layout with a vertical option list on the left and preview on the right. Do not use previews for simple preference questions where labels and descriptions suffice. Note: previews are only supported for single-select questions (not multiSelect)."
)
