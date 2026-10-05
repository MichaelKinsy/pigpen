package jev

import (
	"fmt"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// jev_ask: the model asks typed questions itself. It sends the model-chosen text to
// the judge, so it exists only while Jev is on and refuses when it is off.

func (x *ext) registerAsk(ctx sdk.Context) {
	x.mu.Lock()
	done := x.askReg
	x.askReg = true
	x.mu.Unlock()
	if done {
		return
	}
	str := func(desc string) sdk.Schema { return sdk.Schema{"type": "string", "description": desc} }
	question := sdk.Schema{
		"type": "object",
		"properties": sdk.Schema{
			"id":           str("Short key for this question. The answer comes back under it."),
			"type":         sdk.Schema{"type": "string", "enum": []any{"noul", "choice", "score"}, "description": "noul = yes/no probability, choice = pick one option, score = value on a rubric"},
			"instructions": str("The one thing to judge. One specific, well-scoped gut-check per question."),
			"options": sdk.Schema{"type": "array", "description": "choice only: the options to choose between", "items": sdk.Schema{
				"type": "object", "properties": sdk.Schema{"name": str("Option key"), "description": str("When this option applies")}, "required": []any{"name"}}},
			"levels": sdk.Schema{"type": "array", "description": "score only: ordered rubric levels, lowest first, at least two", "items": sdk.Schema{"type": "string"}},
		},
		"required": []any{"id", "type", "instructions"},
	}
	ctx.RegisterTool(sdk.ToolDefinition{
		Name:          "jev_ask",
		Label:         "Jev Ask",
		Description:   "Ask TypeSafe Jev typed questions about a piece of text and get calibrated answers (probabilities, a chosen option, a rubric score) instead of prose.",
		PromptSnippet: "Ask Jev typed questions (yes/no, choice, rubric) about text and get calibrated answers",
		PromptGuidelines: []string{
			"Use jev_ask when a judgement must be typed and calibrated rather than written: classification, relevance, yes/no checks, rubric scores.",
			"Ask one specific question per entry in jev_ask; split multi-factor judgements into separate questions and combine the answers yourself.",
		},
		Parameters: sdk.Schema{
			"type": "object",
			"properties": sdk.Schema{
				"state":     str("The text to judge: tool output, a diff, a message, a document excerpt."),
				"questions": sdk.Schema{"type": "array", "description": "One or more questions. All are evaluated in parallel against the same state.", "items": question},
			},
			"required": []any{"state", "questions"},
		},
		Execute: x.execAsk,
	})
}

func askFail(format string, a ...any) (any, error) {
	return sdk.ToolResult{Content: "jev_ask: " + fmt.Sprintf(format, a...), Details: map[string]any{"ok": false}}, nil
}

func (x *ext) execAsk(ctx sdk.Context, params map[string]any) (any, error) {
	s := x.snapshot()
	if !s.on || s.be == nil {
		return askFail("Jev is off (the user turns it on with /jev on); nothing was sent")
	}
	state, ok := params["state"].(string)
	if !ok {
		return askFail("state must be text")
	}
	raw, _ := params["questions"].([]any)
	qs := make([]question, 0, len(raw))
	for _, item := range raw {
		m, _ := item.(map[string]any)
		q, msg := toQuestion(m)
		if msg != "" {
			return askFail("%s", msg)
		}
		qs = append(qs, q)
	}
	if err := validateQuestions(qs); err != nil {
		return askFail("%s", err.Error())
	}
	gctx, cancel := goContext(ctx)
	defer cancel()
	// maxStateChars caps what the model can send, like the gate's state.
	resp, err := s.be.Ask(gctx, ctx, jsonString(elide(state, s.cfg.MaxStateChars)), true, qs)
	if err != nil {
		return askFail("%s", x.redact(err.Error()))
	}
	details := map[string]any{"ok": true, "model": resp.Model, "answers": detailAnswers(resp)}
	if resp.Usage != nil {
		details["usage"] = map[string]any{"input_tokens": resp.Usage.Input, "output_tokens": resp.Usage.Output}
	}
	return sdk.ToolResult{Content: renderAnswers(resp, qs), Details: details}, nil
}

// toQuestion returns a question, or a message explaining why the shape is invalid.
func toQuestion(m map[string]any) (question, string) {
	id, _ := m["id"].(string)
	typ, _ := m["type"].(string)
	instr, _ := m["instructions"].(string)
	q := question{ID: id, Type: typ, Instructions: instr}
	switch typ {
	case "choice":
		opts, _ := m["options"].([]any)
		if len(opts) == 0 {
			return q, fmt.Sprintf("question %q: choice needs at least one option", id)
		}
		for _, o := range opts {
			om, _ := o.(map[string]any)
			name, _ := om["name"].(string)
			var d *string
			if s, ok := om["description"].(string); ok {
				d = &s
			}
			q.Options = append(q.Options, option{name, d})
		}
	case "score":
		lv, _ := m["levels"].([]any)
		if len(lv) < 2 {
			return q, fmt.Sprintf("question %q: score needs at least two levels", id)
		}
		for _, l := range lv {
			s, _ := l.(string)
			q.Levels = append(q.Levels, s)
		}
	case "noul":
	default:
		return q, fmt.Sprintf("question %q: type must be noul, choice or score", id)
	}
	return q, ""
}

func renderAnswers(r *response, qs []question) string {
	lines := []string{"model " + r.Model}
	for _, q := range qs {
		a := r.Answers[q.ID]
		text := describeAnswer(&a)
		if a.Type == "choice" {
			text = formatChoice(a)
		}
		lines = append(lines, fmt.Sprintf("%s: %s  <- %s", q.ID, text, q.Instructions))
	}
	if r.Usage != nil {
		lines = append(lines, fmt.Sprintf("tokens %d in / %d out", int(r.Usage.Input), int(r.Usage.Output)))
	}
	return strings.Join(lines, "\n")
}

// detailAnswers is the answers as the judge sent them (the original's details.answers).
func detailAnswers(r *response) map[string]any {
	out := map[string]any{}
	for id, a := range r.Answers {
		probs := map[string]any{}
		for _, p := range a.Probabilities {
			probs[p.Name] = p.Value
		}
		switch a.Type {
		case "noul":
			out[id] = map[string]any{"type": "noul", "noul": a.Noul}
		case "choice":
			out[id] = map[string]any{"type": "choice", "choice": a.Choice, "confidence": a.Confidence, "probabilities": probs}
		default:
			out[id] = map[string]any{"type": "score", "score": a.Score, "confidence": a.Confidence, "legend": a.Legend, "probabilities": probs}
		}
	}
	return out
}
