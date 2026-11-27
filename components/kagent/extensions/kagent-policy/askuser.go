package kagentpolicy

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// AskUserToolName is the tool the model calls to put questions to the user.
const AskUserToolName = "ask_user"

const (
	maxQuestions        = 8
	maxChoices          = 16
	maxQuestionRunes    = 2000
	maxChoiceRunes      = 500
	askUserDescription  = "Ask the user one or more questions and wait for the answers. Use it when you need a decision or missing information that you cannot find yourself. Each question may offer choices; set multiple to let the user pick several."
	askUserPromptNotice = "ask_user: put questions to the user and wait for the answers"
)

// askUserSchema is the JSON Schema of the ask_user arguments.
func askUserSchema() sdk.Schema {
	return sdk.Schema{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"questions"},
		"properties": map[string]any{
			"questions": map[string]any{
				"type":     "array",
				"minItems": 1,
				"maxItems": maxQuestions,
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"question"},
					"properties": map[string]any{
						"question": map[string]any{"type": "string", "minLength": 1, "maxLength": maxQuestionRunes, "description": "The question to ask."},
						"choices": map[string]any{
							"type": "array", "maxItems": maxChoices,
							"items":       map[string]any{"type": "string", "minLength": 1, "maxLength": maxChoiceRunes},
							"description": "Answers to offer. Leave empty for a free-text answer.",
						},
						"multiple": map[string]any{"type": "boolean", "description": "Whether more than one choice may be picked."},
					},
				},
			},
		},
	}
}

// Question is one validated question of an ask_user call.
type Question struct {
	Question string   `json:"question"`
	Choices  []string `json:"choices"`
	Multiple bool     `json:"multiple"`
}

// parseQuestions validates the arguments of an ask_user call. The host validates them against the schema as well,
// but the confirm message is the contract with the adapter, so the extension does not rely on that.
func parseQuestions(params map[string]any) ([]Question, error) {
	for key := range params {
		if key != "questions" {
			return nil, fmt.Errorf("ask_user: unknown argument %q", key)
		}
	}
	list, ok := params["questions"].([]any)
	if !ok || len(list) < 1 || len(list) > maxQuestions {
		return nil, fmt.Errorf("ask_user: questions must be an array of 1 to %d questions", maxQuestions)
	}
	out := make([]Question, 0, len(list))
	for i, item := range list {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("ask_user: questions[%d] must be an object", i)
		}
		q := Question{Choices: []string{}}
		for key, value := range obj {
			switch key {
			case "question":
				text, ok := value.(string)
				if !ok || text == "" || utf8.RuneCountInString(text) > maxQuestionRunes {
					return nil, fmt.Errorf("ask_user: questions[%d].question must be a string of 1 to %d characters", i, maxQuestionRunes)
				}
				q.Question = text
			case "choices":
				choices, ok := value.([]any)
				if !ok || len(choices) > maxChoices {
					return nil, fmt.Errorf("ask_user: questions[%d].choices must be an array of at most %d strings", i, maxChoices)
				}
				for j, c := range choices {
					text, ok := c.(string)
					if !ok || text == "" || utf8.RuneCountInString(text) > maxChoiceRunes {
						return nil, fmt.Errorf("ask_user: questions[%d].choices[%d] must be a string of 1 to %d characters", i, j, maxChoiceRunes)
					}
					q.Choices = append(q.Choices, text)
				}
			case "multiple":
				flag, ok := value.(bool)
				if !ok {
					return nil, fmt.Errorf("ask_user: questions[%d].multiple must be a boolean", i)
				}
				q.Multiple = flag
			default:
				return nil, fmt.Errorf("ask_user: questions[%d] has the unknown member %q", i, key)
			}
		}
		if q.Question == "" {
			return nil, fmt.Errorf("ask_user: questions[%d].question is required", i)
		}
		out = append(out, q)
	}
	return out, nil
}

// askState carries an answer from the tool_call hook, where the dialog is shown, to the execute handler, which
// returns it as the tool result.
//
// The dialog is in the hook, not in execute, on purpose: PiG evaluates every tool_call hook of an assistant message
// before it runs any call of it (parallel mode), so a harness that parks on the dialog leaves nothing of the batch
// executed. A dialog in execute would let the calls listed before ask_user run first, and a tool with the sequential
// execution mode makes PiG run the whole batch call by call.
type askState struct {
	mu      sync.Mutex
	answers map[string]string
}

// hook validates the questions and shows the `kagent-ask-user` dialog. The adapter parks on it and never answers; in
// a normal PiG run the client answers with the `confirmed` value. A declined dialog blocks the call with an error
// result.
func (a *askState) hook(ctx sdk.Context, data map[string]any) (any, error) {
	id, _ := data["toolCallId"].(string)
	if id == "" {
		return nil, errors.New("kagent-policy: tool_call event without a toolCallId")
	}
	input, _ := data["input"].(map[string]any)
	questions, err := parseQuestions(input)
	if err != nil {
		return block(err.Error()), nil
	}
	text, err := encodeMessage(message{
		V: messageVersion, Kind: kindAskUser, ToolCallID: id, ToolName: AskUserToolName,
		Input: map[string]any{"questions": questions},
	})
	if err != nil {
		return nil, fmt.Errorf("ask_user: encode the question: %w", err)
	}
	answer, err := ctx.Confirm(TitleAskUser, text)
	if err != nil {
		return nil, fmt.Errorf("ask_user: the dialog failed: %w", err)
	}
	result, err := answerText(answer)
	if err != nil {
		return block(DeclinedText), nil
	}
	a.mu.Lock()
	a.answers[id] = result
	a.mu.Unlock()
	return nil, nil
}

// execute returns the answer the hook recorded for this call.
func (a *askState) execute(ctx sdk.Context, _ map[string]any) (any, error) {
	a.mu.Lock()
	answer, ok := a.answers[ctx.ToolCallID()]
	delete(a.answers, ctx.ToolCallID())
	a.mu.Unlock()
	if !ok {
		return nil, sdk.NewToolError("ask_user: no answer was recorded for this call")
	}
	return answer, nil
}

// answerText turns the value the client answered with into the tool result. A boolean false, a missing value and an
// empty string are a declined dialog (cancelled and unanswered dialogs resolve to false); a string is the answer;
// any other value is its compact JSON.
func answerText(answer any) (string, error) {
	switch v := answer.(type) {
	case nil:
		return "", errDeclined
	case bool:
		if !v {
			return "", errDeclined
		}
	case string:
		if strings.TrimSpace(v) == "" {
			return "", errDeclined
		}
		return v, nil
	}
	raw, err := json.Marshal(answer)
	if err != nil {
		return "", errors.New("ask_user: the answer is not JSON")
	}
	return string(raw), nil
}

var errDeclined = errors.New(DeclinedText)

func registerAskUser(ext *sdk.Extension, state *askState) {
	ext.RegisterTool(sdk.ToolDefinition{
		Name:          AskUserToolName,
		Label:         AskUserToolName,
		Description:   askUserDescription,
		PromptSnippet: askUserPromptNotice,
		Parameters:    askUserSchema(),
		Execute:       state.execute,
	})
}
