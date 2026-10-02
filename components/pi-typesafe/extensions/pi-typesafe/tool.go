package pi_typesafe

import (
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	pitypesafe "github.com/MichaelKinsy/pigpen/components/pi-typesafe-api"
)

const ownModelMsg = "Submitted state and questions will be sent to the provider of the model PiG is configured with (the own-model backend) and may incur charges there. Nothing is sent to api.typesafe.ai. Do not include secrets. The extension does not collect files or conversation history. Results are model judgments, not proof or authorization."

// disclosure says where submitted content goes for a backend.
func disclosure(backend string) string {
	if backend == pitypesafe.BackendOwnModel {
		return ownModelMsg
	}
	return typesafeMsg
}

// sampleJSON is the request the playground edits and the tool guidelines show: the payload shape is what models get
// wrong on the first call, and it is paid for on every tool listing, so it stays short.
const sampleJSON = `{"state":{"message":"I was charged twice for my subscription. Please help today."},"questions":{` +
	`"category":{"type":"choice","instructions":"Which team should handle this message?","criteria":{"billing":"Charges and payments","technical":"Software failures","other":"None of these"}},` +
	`"urgent":{"type":"noul","instructions":"Does the sender request help today?"},` +
	`"frustration":{"type":"score","instructions":"How frustrated does the sender sound?","criteria":["Neutral request","Frustrated but civil","Angry or threatening"]}}}`

func toolDescription(backend string) string {
	return "Evaluate supplied state with independent Choice, Score, and Noul questions in one TypeSafe request. " +
		"Each question judges the whole state, so when several items are involved, put each item in a named state field (e.g. `reports.r1`) and ask one question per item per dimension (e.g. `r1_owner`, `r2_owner`), naming the field in the instructions; never aggregate several items into one question. " +
		disclosure(backend) + " Requires operator opt-in via /typesafe enable or PI_TYPESAFE_ENABLED=1. " +
		fmt.Sprintf("Limit: 32 questions, %d KiB JSON, %d attempts per session; no retries.", pitypesafe.DefaultMaxInputBytes/1024, pitypesafe.DefaultMaxRequests)
}

func toolGuidelines() []string {
	return []string{
		// The payload shape is what models get wrong on the first call; the same sample the playground edits is the cheapest way to show it.
		"Request shape, all three question kinds in one call: " + sampleJSON,
		"Use typesafe_evaluate only for requested semantic judgments, not calculations or exact lookups; send only the relevant permitted data.",
		"Batch independent typesafe_evaluate questions over the same state; use code or explicit permission rules for actions, never confidence as authorization.",
		"When typesafe_evaluate judges several items, give each item a named state field and ask one question per item per dimension, naming the field in the instructions; one question over many items returns an unusable blend.",
		"Report typesafe_evaluate answers as the model's judgments with their probabilities; do not replace them with your own guesses, and say when an answer is uncertain.",
	}
}

// toMap converts an ordered tree into the maps the SDK's arguments use.
func toMap(v any) any {
	switch t := v.(type) {
	case *pitypesafe.Object:
		m := make(map[string]any, t.Len())
		for _, k := range t.Keys() {
			x, _ := t.Get(k)
			m[k] = toMap(x)
		}
		return m
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = toMap(e)
		}
		return out
	}
	return v
}

// registerTool (re)registers typesafe_evaluate; registering a name again replaces the definition, which is how
// a backend switch updates the disclosure in the tool description.
func (s *state) registerTool() {
	backend := s.currentBackend()
	s.ext.RegisterTool(sdk.ToolDefinition{
		Name:             toolName,
		Label:            "TypeSafe",
		Description:      toolDescription(backend),
		PromptSnippet:    "Ask batched structured questions with TypeSafe (external service; operator opt-in required)",
		PromptGuidelines: toolGuidelines(),
		Parameters:       pitypesafe.EvaluationSchema(),
		// Pi validates against `parameters` after this hook.
		PrepareArguments: func(params map[string]any) (map[string]any, error) {
			if normalized, ok := toMap(pitypesafe.NormalizeEvaluationRequest(params)).(map[string]any); ok {
				return normalized, nil
			}
			return params, nil
		},
		Execute:      s.execute,
		RenderCall:   s.renderCall,
		RenderResult: s.renderResult,
	})
}

// admitted is a test seam (export_test.go): it runs after a tool call passed the consent gate and before
// anything is sent, where a concurrent /typesafe command could change the backend.
var admitted = func() {}

func (s *state) execute(ctx sdk.Context, params map[string]any) (any, error) {
	enabled, consent := s.admit()
	if !enabled {
		return nil, &pitypesafe.IntegrationError{Code: pitypesafe.CodeConfiguration, Message: "TypeSafe is disabled. Ask the operator to run /typesafe enable; do not enable it by editing configuration or environment files."}
	}
	admitted()
	// The tool admits through the same rule as the library; EvaluateRaw re-runs it idempotently.
	request, err := pitypesafe.PrepareEvaluationRequest(params, pitypesafe.PrepareOptions{})
	if err != nil {
		return nil, err
	}
	client, err := s.clientFor(consent)
	if err == errDestinationChanged {
		return nil, err
	}
	if err != nil {
		return nil, s.report(ctx, err)
	}
	goCtx, cancel := goContext(ctx)
	defer cancel()
	result, err := client.EvaluateRaw(goCtx, request)
	if err != nil {
		return nil, s.report(ctx, err)
	}
	content, err := result.MarshalJSON()
	if err != nil {
		return nil, err
	}
	details, err := result.DetailsJSON()
	if err != nil {
		return nil, err
	}
	return sdk.ToolResult{Content: string(content), Details: json.RawMessage(details)}, nil
}

// report classifies a failure and, when it means every later judgment is skipped, calls it out once.
func (s *state) report(ctx sdk.Context, err error) error {
	safe := pitypesafe.SafeError(err, s.currentBackend())
	// Authentication degradation is louder than a single failed call: it means every later judgment is skipped.
	rejected := safe.Code == pitypesafe.CodeHTTP && (safe.Status == 401 || safe.Status == 403)
	if rejected || safe.Code == pitypesafe.CodeConfiguration {
		status := ""
		if safe.Status != 0 {
			status = fmt.Sprint(safe.Status)
		}
		text := "TypeSafe is not authenticated (" + safe.Message + ") Judgments will fail until the key is fixed."
		if s.currentBackend() == pitypesafe.BackendOwnModel {
			// The own-model backend has no key of this extension: name what failed instead.
			text = "The own-model backend cannot answer (" + safe.Message + ") Judgments will fail until this is fixed."
		}
		s.callOut(ctx, "run:"+string(safe.Code)+":"+status, text)
	}
	return safe
}

func (s *state) renderCall(_ sdk.Context, args map[string]any, _ sdk.ToolRenderContext, width int) ([]string, error) {
	questions, _ := args["questions"].(map[string]any)
	where := "external request"
	if s.currentBackend() == pitypesafe.BackendOwnModel {
		where = "configured model"
	}
	return wrapText(fmt.Sprintf("TypeSafe · %d questions · %s", len(questions), where), width), nil
}

func (s *state) renderResult(_ sdk.Context, result sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, _ sdk.ToolRenderContext, width int) ([]string, error) {
	if opts.IsPartial {
		return wrapText("TypeSafe · waiting for response", width), nil
	}
	details, _ := result.Details.(map[string]any)
	if _, ok := details["answers"].(map[string]any); !ok {
		var parts []string
		for _, block := range result.Content {
			if block["type"] == "text" {
				text, _ := block["text"].(string)
				parts = append(parts, text)
			}
		}
		return wrapText(strings.Join(parts, "\n"), width), nil
	}
	return wrapText(format(details, opts.Expanded), width), nil
}

func (s *state) renderEntry(_ sdk.Context, entry map[string]any, opts sdk.EntryRenderOptions, width int) ([]string, error) {
	data, _ := entry["data"].(map[string]any)
	if data == nil {
		return wrapText("TypeSafe · no result", width), nil
	}
	return wrapText(format(data, opts.Expanded), width), nil
}

// ToolDescription is the typesafe_evaluate description for a backend: the original's text, with the own-model
// disclosure when that backend is selected.
func ToolDescription(backend string) string { return toolDescription(backend) }

// ToolGuidelines are the prompt guidelines of typesafe_evaluate, the request-shape example first.
func ToolGuidelines() []string { return toolGuidelines() }

// Disclosure says where submitted content goes for a backend.
func Disclosure(backend string) string { return disclosure(backend) }
