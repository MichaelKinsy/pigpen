package warden

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/ownmodel"
	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/pigmodel"
	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/typesafe"
)

// The two judgment backends, both from the shared TypeSafe client (components/typesafe): TypeSafe's Jev over
// HTTP, and the session's own model answering the same questions. The guards see only the Judge interface.

// Backend names.
const (
	BackendNone     = ""
	BackendTypeSafe = "typesafe"
	BackendOwnModel = "own-model"
)

// Judge builds an EvaluatorJudge around any typesafe.Evaluator. label names the backend in error messages.
type EvaluatorJudge struct {
	Evaluator typesafe.Evaluator
	// Model overrides the evaluator's default model when non-empty (TypeSafe only).
	Model string
	Label string
	// Budget caps the attempts per session (nil: no cap). It outlives one judge: the own-model judge is rebuilt
	// for each call from the session's current model.
	Budget *Budget
}

// Budget counts the requests a session may make.
type Budget struct {
	mu   sync.Mutex
	used int
	Max  int
}

// Used is the number of requests attempted.
func (b *Budget) Used() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used
}

// Reset starts a new budget.
func (b *Budget) Reset() {
	b.mu.Lock()
	b.used = 0
	b.mu.Unlock()
}

func (b *Budget) take(label string) error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.Max > 0 && b.used >= b.Max {
		return &IntegrationError{Code: "budget", Message: label + " request limit reached (" + strconv.Itoa(b.Max) + " attempts this session). Run /warden enable to start a new budget."}
	}
	b.used++
	return nil
}

func (j *EvaluatorJudge) label() string {
	if j.Label == "" {
		return "TypeSafe"
	}
	return j.Label
}

// toRequest converts a warden request to the client's: the state as a JSON object, the questions sorted by name.
func toRequest(r Request, model string) (typesafe.SystemOneRequest, error) {
	state, err := json.Marshal(r.State)
	if err != nil {
		return typesafe.SystemOneRequest{}, &IntegrationError{Code: "validation", Message: "The request state could not be encoded."}
	}
	ids := make([]string, 0, len(r.Questions))
	for id := range r.Questions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	qs := make(typesafe.Questions, 0, len(ids))
	for _, id := range ids {
		q := r.Questions[id]
		switch q.Type {
		case "noul":
			nq := typesafe.Noul(q.Instructions)
			if t, ok := q.Criteria["true"]; ok {
				nq = nq.Yes(t)
			}
			if f, ok := q.Criteria["false"]; ok {
				nq = nq.No(f)
			}
			qs = append(qs, typesafe.Ask(id, nq))
		case "choice":
			order := q.CriteriaOrder
			if len(order) == 0 {
				for label := range q.Criteria {
					order = append(order, label)
				}
				sort.Strings(order)
			}
			opts := make([]typesafe.Option, 0, len(order))
			for _, label := range order {
				opts = append(opts, typesafe.Opt(label, q.Criteria[label]))
			}
			qs = append(qs, typesafe.Ask(id, typesafe.Choice(q.Instructions, opts...)))
		case "score":
			levels := make([]any, len(q.Levels))
			for i, l := range q.Levels {
				levels[i] = l
			}
			qs = append(qs, typesafe.Ask(id, typesafe.Score(q.Instructions, levels...)))
		default:
			return typesafe.SystemOneRequest{}, &IntegrationError{Code: "validation", Message: "Unknown question type " + strconv.Quote(q.Type) + "."}
		}
	}
	return typesafe.SystemOneRequest{State: typesafe.Value(json.RawMessage(state)), Questions: qs, Model: model}, nil
}

// Evaluate implements Judge.
func (j *EvaluatorJudge) Evaluate(ctx context.Context, r Request) (Evaluation, error) {
	req, err := toRequest(r, j.Model)
	if err != nil {
		return Evaluation{}, err
	}
	if err := j.Budget.take(j.label()); err != nil {
		return Evaluation{}, err
	}
	start := time.Now()
	res, err := j.Evaluator.SystemOne(ctx, req, nil)
	if err != nil {
		return Evaluation{}, j.safeError(err)
	}
	out := Evaluation{Model: res.Model, ElapsedMs: int(time.Since(start).Milliseconds()), Answers: map[string]Answer{}}
	for name, a := range res.Answers {
		switch v := a.(type) {
		case typesafe.NoulAnswer:
			out.Answers[name] = Answer{Type: "noul", Noul: v.Noul}
		case *typesafe.NoulAnswer:
			out.Answers[name] = Answer{Type: "noul", Noul: v.Noul}
		case typesafe.ChoiceAnswer:
			out.Answers[name] = Answer{Type: "choice", Choice: v.Choice, Confidence: v.Confidence}
		case *typesafe.ChoiceAnswer:
			out.Answers[name] = Answer{Type: "choice", Choice: v.Choice, Confidence: v.Confidence}
		case typesafe.ScoreAnswer:
			out.Answers[name] = Answer{Type: "score", Score: v.Score, Confidence: v.Confidence}
		case *typesafe.ScoreAnswer:
			out.Answers[name] = Answer{Type: "score", Score: v.Score, Confidence: v.Confidence}
		}
	}
	return out, nil
}

// safeError classifies an error into a message safe to display: never an upstream body, a header, a key or the
// submitted state (pi-typesafe errors.ts `safeError`).
func (j *EvaluatorJudge) safeError(err error) error {
	var integration *IntegrationError
	if errors.As(err, &integration) {
		return integration
	}
	label := j.label()
	var abort *typesafe.APIUserAbortError
	if errors.As(err, &abort) {
		if errors.Is(err, context.DeadlineExceeded) {
			return &IntegrationError{Code: "timeout", Message: label + " request timed out; it was not retried and may still be billed."}
		}
		return &IntegrationError{Code: "aborted", Message: label + " request cancelled; an already submitted request may still be billed."}
	}
	var timeout *typesafe.APITimeoutError
	if errors.As(err, &timeout) {
		return &IntegrationError{Code: "timeout", Message: label + " request timed out; it was not retried and may still be billed."}
	}
	var api *typesafe.APIError
	if errors.As(err, &api) {
		advice := "Try again later or check the service status."
		switch api.Status {
		case 401:
			advice = "Check TYPESAFE_API_KEY."
			if j.Label != "" {
				advice = "Check the provider account for the session model."
			}
		case 402:
			advice = "Check your account balance."
		case 403:
			advice = "Check your account access and model permissions."
		case 429:
			advice = "Check your account quota and try again later."
		case 400, 422:
			advice = "Check the question format and model limits."
		}
		return &IntegrationError{Code: "http", Message: label + " returned HTTP " + strconv.Itoa(api.Status) + ". " + advice + " No automatic retry was made."}
	}
	var conn *typesafe.APIConnectionError
	if errors.As(err, &conn) {
		return &IntegrationError{Code: "connection", Message: "Could not complete the " + label + " connection. No automatic retry was made."}
	}
	var base *typesafe.TypeSafeError
	if errors.As(err, &base) {
		return &IntegrationError{Code: "response", Message: label + " returned an unreadable or unexpected response."}
	}
	return &IntegrationError{Code: "response", Message: label + " returned an unreadable or unexpected response."}
}

// NewTypeSafeJudge is the TypeSafe backend: the client reads TYPESAFE_API_KEY (and TYPESAFE_BASE_URL) from the
// environment. No retries: a judgment that arrives late is worth less than the tool call it gates.
func NewTypeSafeJudge(timeout time.Duration, budget *Budget, getenv func(string) string) (*EvaluatorJudge, error) {
	none := 0
	client, err := typesafe.NewClient(typesafe.Config{Timeout: timeout, Retry: typesafe.RetryOverrides{MaxRetries: &none}, Getenv: getenv, LogLevel: typesafe.LogOff})
	if err != nil {
		return nil, &IntegrationError{Code: "configuration", Message: "TypeSafe is not configured: set TYPESAFE_API_KEY in the environment PiG runs in."}
	}
	return &EvaluatorJudge{Evaluator: client, Budget: budget}, nil
}

// NewOwnModelJudge is the own-model backend: the session's model, called through PiG's model registry, answers
// the same questions. registry is ctx.ModelRegistry() and provider/id name the active model.
func NewOwnModelJudge(registry pigmodel.Registry, provider, id string, budget *Budget) (*EvaluatorJudge, error) {
	m, err := pigmodel.New(registry, pigmodel.Ref{Provider: provider, ID: id})
	if err != nil {
		return nil, &IntegrationError{Code: "configuration", Message: "The session model is not available to warden."}
	}
	backend, err := ownmodel.New(ownmodel.Options{Model: m, AnswerMode: ownmodel.Probabilities, NormalizeProbabilities: true, MalformedRetries: 1})
	if err != nil {
		return nil, &IntegrationError{Code: "configuration", Message: "The session model is not available to warden."}
	}
	return &EvaluatorJudge{Evaluator: backend, Label: "The session model", Budget: budget}, nil
}
