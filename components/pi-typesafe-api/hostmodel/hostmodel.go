// Package hostmodel is the own-model backend on the PiG Go SDK: it answers TypeSafe's typed questions with the
// model PiG is configured with, chosen at call time from the request's Context, so a model switch takes effect
// on the next call without rebuilding the client. Nothing here names a provider or a model.
//
// Pass the Evaluator to pitypesafe.New as Options.Evaluator with Backend "ownmodel", and put the tool call's
// Context in the request context with [WithContext].
package hostmodel

import (
	"context"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	pitypesafe "github.com/MichaelKinsy/pigpen/components/pi-typesafe-api"
	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/ownmodel"
	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/pigmodel"
	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/typesafe"
)

type contextKey struct{}

// WithContext returns a context that carries the PiG extension Context the model is read from.
func WithContext(ctx context.Context, host sdk.Context) context.Context {
	return context.WithValue(ctx, contextKey{}, host)
}

// Options configure the backend built for each call.
type Options struct {
	// AnswerMode is ownmodel.Probabilities (the default) or ownmodel.Discrete.
	AnswerMode ownmodel.AnswerMode
	// NormalizeProbabilities rescales invalid probability distributions to sum to 1.
	NormalizeProbabilities bool
	// MalformedRetries is the number of corrective retries when the output fails validation.
	MalformedRetries int
	// Logger receives request summaries; nil discards.
	Logger typesafe.Logger
}

// Evaluator answers questions with the host's active model. It is safe for concurrent use.
type Evaluator struct{ opts Options }

// New returns an Evaluator.
func New(opts Options) *Evaluator { return &Evaluator{opts: opts} }

var _ typesafe.Evaluator = (*Evaluator)(nil)

// ModelRef names the model a Context is configured with.
type ModelRef struct{ Provider, ID string }

// Active returns the model PiG is configured with, or an error when none is selected. The setup errors here
// are configuration errors (*pitypesafe.IntegrationError) so their reason reaches the operator unchanged.
func Active(host sdk.Context) (ModelRef, error) {
	info, err := host.GetModelInfo()
	if err != nil {
		return ModelRef{}, err
	}
	if info == nil || info.ID == "" {
		return ModelRef{}, &pitypesafe.IntegrationError{Code: pitypesafe.CodeConfiguration, Message: "No model is configured in PiG; select one with /model first."}
	}
	return ModelRef{Provider: info.Provider, ID: info.ID}, nil
}

// SystemOne implements typesafe.Evaluator on the model of the Context in ctx.
func (e *Evaluator) SystemOne(ctx context.Context, req typesafe.SystemOneRequest, opts *typesafe.RequestOptions) (*typesafe.SystemOneResult, error) {
	host, ok := ctx.Value(contextKey{}).(sdk.Context)
	if !ok {
		return nil, &pitypesafe.IntegrationError{Code: pitypesafe.CodeConfiguration, Message: "The own-model backend needs the PiG Context in the request context (hostmodel.WithContext)."}
	}
	ref, err := Active(host)
	if err != nil {
		return nil, err
	}
	model, err := pigmodel.New(host.ModelRegistry(), pigmodel.Ref{Provider: ref.Provider, ID: ref.ID})
	if err != nil {
		return nil, err
	}
	backend, err := ownmodel.New(ownmodel.Options{
		Model:                  model,
		AnswerMode:             e.opts.AnswerMode,
		NormalizeProbabilities: e.opts.NormalizeProbabilities,
		MalformedRetries:       e.opts.MalformedRetries,
		Logger:                 e.opts.Logger,
	})
	if err != nil {
		return nil, err
	}
	return backend.SystemOne(ctx, req, opts)
}
