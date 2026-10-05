package warden

import (
	"context"
	"errors"
	"time"
)

// The judge seam. Every judged check asks typed questions about a state and reads
// probabilities back: TypeSafe's Jev over HTTP, or the session's own model. The two
// backends live in the shared TypeSafe client (components/typesafe); the guards see only
// this interface, which mirrors pi-typesafe's `Judge` and `ask` (src/ask.ts).

// Question is one typed question: noul (yes/no), choice (pick a label) or score (rubric level).
type Question struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions,omitempty"`
	// Criteria for a noul question: {"true": ..., "false": ...}; for a choice: label to description.
	Criteria map[string]string `json:"-"`
	// CriteriaOrder is the order of the criteria keys (a choice's options are read in this order).
	CriteriaOrder []string `json:"-"`
	// Levels is the ordered rubric of a score question.
	Levels []string `json:"-"`
}

// Questions is a request's questions by id.
type Questions map[string]Question

// Request is one judgment request.
type Request struct {
	State     map[string]any
	Questions Questions
}

// Answer is one answer. Noul holds P(yes) for a noul question; Choice the picked label;
// Score the rubric position.
type Answer struct {
	Type       string
	Noul       float64
	Choice     string
	Score      float64
	Confidence float64
}

// Evaluation is a successful judgment.
type Evaluation struct {
	Answers   map[string]Answer
	Model     string
	ElapsedMs int
}

// Judge answers typed questions. Implementations return *IntegrationError for failures
// whose message is safe to show.
type Judge interface {
	Evaluate(ctx context.Context, request Request) (Evaluation, error)
}

// IntegrationError is a judge failure with a message safe to display: never an upstream
// body, a key or the submitted state (pi-typesafe errors.ts `TypeSafeIntegrationError`).
type IntegrationError struct {
	Code    string // configuration | validation | budget | aborted | timeout | http | connection | response
	Message string
}

func (e *IntegrationError) Error() string { return e.Message }

// AskAnswer is the outcome of Ask; it never carries a Go error.
type AskAnswer struct {
	OK        bool
	Answers   map[string]Answer
	Model     string
	ElapsedMs int
	Error     string
	ErrorCode string
}

const fallbackMessage = "TypeSafe request failed."

// Ask sends one request and never fails: a failure comes back with its safe message and
// code, so a caller can stop asking after a `budget` error (pi-typesafe ask.ts).
func Ask(ctx context.Context, judge Judge, request Request, timeout time.Duration) AskAnswer {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	result, err := judge.Evaluate(ctx, request)
	if err != nil {
		var integration *IntegrationError
		if errors.As(err, &integration) {
			return AskAnswer{Error: integration.Message, ErrorCode: integration.Code}
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return AskAnswer{Error: "TypeSafe request timed out; it was not retried and may still be billed.", ErrorCode: "timeout"}
		}
		if errors.Is(err, context.Canceled) {
			return AskAnswer{Error: "TypeSafe request cancelled; an already submitted request may still be billed.", ErrorCode: "aborted"}
		}
		return AskAnswer{Error: fallbackMessage}
	}
	return AskAnswer{OK: true, Answers: result.Answers, Model: result.Model, ElapsedMs: result.ElapsedMs}
}

// Noul builds a yes/no question. The optional criteria say what counts as yes and as no.
func Noul(instructions string, criteria ...string) Question {
	q := Question{Type: "noul", Instructions: instructions}
	if len(criteria) == 2 {
		q.Criteria = map[string]string{"true": criteria[0], "false": criteria[1]}
	}
	return q
}

// Choice builds a pick-one question over labelled options.
func Choice(instructions string, options map[string]string) Question {
	return Question{Type: "choice", Instructions: instructions, Criteria: options}
}

// Score builds a rubric question; levels are ordered lowest first.
func Score(instructions string, levels []string) Question {
	return Question{Type: "score", Instructions: instructions, Levels: levels}
}
