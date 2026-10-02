package pitypesafe

import (
	"context"
	"time"

	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/typesafe"
)

// Judge is anything with a client's Evaluate: the real client in a session, a stub in tests.
type Judge interface {
	Evaluate(ctx context.Context, request typesafe.SystemOneRequest) (*Evaluation, error)
}

// DefaultAskTimeout is the default per-ask deadline; it matches the client's own request timeout.
const DefaultAskTimeout = 15 * time.Second

const askFallbackMessage = "TypeSafe request failed."

// AskOptions configure Ask.
type AskOptions struct {
	// Timeout is the per-ask deadline, merged with the caller's own context. Default: DefaultAskTimeout.
	Timeout time.Duration
}

// AskAnswer is the outcome of Ask.
type AskAnswer struct {
	OK bool
	// The fields below are set when OK.
	Answers   map[string]typesafe.Answer
	Order     []string
	Model     string
	Usage     typesafe.Usage
	ElapsedMs int64
	// Error and ErrorCode are set when not OK; ErrorCode is empty for a failure that is not an IntegrationError.
	Error     string
	ErrorCode ErrorCode
}

// Ask is one typed Jev request that never fails: a failure comes back as an AskAnswer with OK false, this
// package's own message (which carries no upstream body, header, key, or submitted state) and its code, so a
// caller can stop asking after a budget error. The per-ask timeout is merged into the caller's context, so
// either can cancel the request.
//
// This is the author-facing "ask Jev" seam. Agents get the same admission through the typesafe_evaluate tool;
// the two share PrepareEvaluationRequest, so what one accepts the other accepts.
func Ask(ctx context.Context, judge Judge, request typesafe.SystemOneRequest, opts AskOptions) AskAnswer {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultAskTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := judge.Evaluate(ctx, request)
	if err != nil {
		if ie, ok := err.(*IntegrationError); ok {
			return AskAnswer{Error: ie.Message, ErrorCode: ie.Code}
		}
		return AskAnswer{Error: askFallbackMessage}
	}
	return AskAnswer{OK: true, Answers: result.Answers, Order: result.Order, Model: result.Model, Usage: result.Usage, ElapsedMs: result.ElapsedMs}
}
