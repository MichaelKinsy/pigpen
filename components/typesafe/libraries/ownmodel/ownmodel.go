package ownmodel

import (
	"context"
	"time"

	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/typesafe"
)

// Role is the author of a message.
type Role string

// Message roles.
const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is one chat message in provider-neutral form.
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
}

// Request is one model request: the conversation and the JSON Schema of the answer.
// When Structured is false the schema is also in the system prompt and the model is
// only asked for JSON text (prompted mode).
type Request struct {
	Messages   []Message
	Schema     map[string]any
	Structured bool
}

// Result is the raw text the model produced and its token counts; nil counts are
// unreported.
type Result struct {
	Text         string `json:"text"`
	InputTokens  *int   `json:"input_tokens"`
	OutputTokens *int   `json:"output_tokens"`
}

// Model performs one model request. It is the seam between this package and a model
// source; implementations return typesafe errors so the retry policy can classify
// them: *typesafe.APIError (with a status) and *typesafe.APIConnectionError are
// retried per policy, anything else is final. A refusal, an incomplete generation or
// an output-limit stop must be a plain *typesafe.TypeSafeError (never retried and
// never treated as malformed output).
type Model interface {
	// Name is the model name reported in the result.
	Name() string
	Complete(ctx context.Context, req Request) (Result, error)
}

// AnswerMode selects what the model is asked to return.
type AnswerMode string

// Answer modes.
const (
	// Probabilities asks for a probability per label (a probability for a Noul).
	Probabilities AnswerMode = "probabilities"
	// Discrete asks for exactly one value per question.
	Discrete AnswerMode = "discrete"
)

// Options configure a [Backend].
type Options struct {
	// Model answers the questions. Required unless Resolve is set.
	Model Model
	// Resolve, when set, picks the Model for a request that names one
	// (SystemOneRequest.Model); a request that names none uses Model.
	Resolve func(name string) (Model, error)
	// AnswerMode is Probabilities (the default when empty) or Discrete.
	AnswerMode AnswerMode
	// StructuredOutputs asks the Model for its native structured-output mode; when false
	// the schema goes in the prompt and the JSON is validated here.
	StructuredOutputs bool
	// NormalizeProbabilities rescales invalid probability distributions to sum to 1.
	NormalizeProbabilities bool
	// MalformedRetries is the number of corrective retries when the output fails
	// validation. Default 0.
	MalformedRetries int
	// Retry overrides the transient-error policy, applied over a policy with no retries
	// (the default is no retries, as in the Python adapter).
	Retry typesafe.RetryOverrides
	// Logger receives request summaries; nil discards.
	Logger typesafe.Logger
}

// Backend answers questions with a Model. It is safe for concurrent use.
type Backend struct {
	opts   Options
	mode   AnswerMode
	policy typesafe.RetryPolicy
	logger typesafe.Logger
}

var _ typesafe.Evaluator = (*Backend)(nil)

// New validates the options. It returns a *typesafe.TypeSafeError for an unknown
// answer mode, a negative MalformedRetries, or no Model and no Resolve.
func New(opts Options) (*Backend, error) {
	mode := opts.AnswerMode
	if mode == "" {
		mode = Probabilities
	}
	if mode != Probabilities && mode != Discrete {
		return nil, &typesafe.TypeSafeError{Message: "AnswerMode must be 'probabilities' or 'discrete'."}
	}
	if opts.MalformedRetries < 0 {
		return nil, &typesafe.TypeSafeError{Message: "MalformedRetries must be >= 0."}
	}
	if opts.Model == nil && opts.Resolve == nil {
		return nil, &typesafe.TypeSafeError{Message: "An LLM model is required: set Options.Model (or Options.Resolve)."}
	}
	base := typesafe.DefaultRetryPolicy()
	base.MaxRetries = 0 // the oracle's default: no transient retries
	policy, err := base.Resolve(opts.Retry)
	if err != nil {
		return nil, err
	}
	logger := opts.Logger
	if logger == nil {
		logger = discardLogger{}
	}
	return &Backend{opts: opts, mode: mode, policy: policy, logger: logger}, nil
}

type discardLogger struct{}

func (discardLogger) Debug(string, ...any) {}
func (discardLogger) Info(string, ...any)  {}
func (discardLogger) Warn(string, ...any)  {}
func (discardLogger) Error(string, ...any) {}

// SystemOne implements [typesafe.Evaluator]: the questions are validated, one prompt is
// built from the state, the model is called (with corrective and transient retries),
// and its output is converted to typed answers. Usage token counts the model did not
// report are zero here; Evaluate keeps them unknown.
func (b *Backend) SystemOne(ctx context.Context, req typesafe.SystemOneRequest, opts *typesafe.RequestOptions) (*typesafe.SystemOneResult, error) {
	ev, err := b.Evaluate(ctx, req, opts)
	if err != nil {
		return nil, err
	}
	return ev.Result, nil
}

// Usage is the token and retry accounting of one evaluation. A nil count is unknown;
// a total is nil if any attempt did not report that count.
type Usage struct {
	InputTokens       *int
	OutputTokens      *int
	InputTokensTotal  *int
	OutputTokensTotal *int
	// Retries counts transient-error retries, MalformedRetries corrective retries.
	Retries          int
	MalformedRetries int
	Latency          time.Duration
}

// RetryReason records why one retry happened.
type RetryReason struct {
	// Category is "provider_error" or "malformed_structure".
	Category string
	Message  string
}

// Attempt records one model call, including failed ones.
type Attempt struct {
	Messages   []Message
	Schema     map[string]any
	Structured bool
	// Response is nil when the call failed before returning.
	Response  *Result
	ModelName string
	// Error and ErrorType describe a failed call.
	Error     string
	ErrorType string
}

// Debug holds the diagnostics of one evaluation.
type Debug struct {
	LLMAttempts  []Attempt
	RetryReasons []RetryReason
	// MaxError is the largest deviation of a probability sum from 1; InvalidProbs
	// counts the questions beyond the 1e-6 tolerance, ProbabilityErrors names them.
	MaxError          float64
	InvalidProbs      int
	ProbabilityErrors map[string]float64
	// OriginalProbabilities holds distributions that normalization changed.
	OriginalProbabilities map[string]map[string]float64
}

// Evaluation is a result with usage and diagnostics.
type Evaluation struct {
	Result *typesafe.SystemOneResult
	Usage  Usage
	Debug  Debug
}

// Evaluate is [Backend.SystemOne] with usage and diagnostics. An input error (no
// questions, a bad question, a missing model) is a plain *typesafe.TypeSafeError; a
// failure after the model was called is a *DebugError carrying the attempt history.
func (b *Backend) Evaluate(ctx context.Context, req typesafe.SystemOneRequest, opts *typesafe.RequestOptions) (*Evaluation, error) {
	return b.evaluate(ctx, req, opts)
}

// MalformedOutputError is returned (inside a *DebugError) when the model's output still
// fails validation after the last corrective retry. Message is
// "Model output did not match the schema: <details>" and Cause the validation error.
type MalformedOutputError struct{ *typesafe.TypeSafeError }

func (e *MalformedOutputError) Unwrap() error { return e.TypeSafeError }

// DebugError carries the attempt history of a failed evaluation and unwraps to the
// cause: a *typesafe.TypeSafeError for malformed output that is still invalid after the
// last corrective retry (message "Model output did not match the schema: ..."), or the
// error the Model returned.
type DebugError struct {
	Err   error
	Debug Debug
}

func (e *DebugError) Error() string { return e.Err.Error() }
func (e *DebugError) Unwrap() error { return e.Err }
