package jev

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/ownmodel"
	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/pigmodel"
	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/typesafe"
)

// A backend answers typed questions about a state. Two exist, both in the shared
// client (components/typesafe): the TypeSafe API, and the model PiG is configured
// with. The extension decides what to ask, what leaves the machine and what a
// verdict means; it does not speak HTTP or build model prompts.
type backend struct {
	kind   string
	dest   string
	client *typesafe.Client // typesafe backend
	model  pigmodel.Ref     // own-model backend
	cfg    config
}

func (b *backend) Destination() string { return b.dest }

// normalizeEndpoint takes the API root. The original's setting was the full
// request URL, so a trailing /v1/systemone is accepted and removed.
func normalizeEndpoint(raw string) string {
	raw = strings.TrimRight(raw, "/")
	return strings.TrimSuffix(raw, "/v1/systemone")
}

// newBackend builds the backend for c, or explains why none is available. The
// TypeSafe client gets an empty environment: only the user's config chooses where
// the key travels, never TYPESAFE_BASE_URL (PORT.md C2).
func newBackend(c config, sctx sdk.Context) (*backend, error) {
	if c.Backend == backendTypeSafe {
		if c.Endpoint == "" {
			return nil, errors.New("the typesafe backend needs \"endpoint\" in your pi-jev.json (there is no built-in default)")
		}
		if c.Model == "" {
			return nil, errors.New("the typesafe backend needs \"model\" in your pi-jev.json")
		}
		base := normalizeEndpoint(c.Endpoint)
		if err := checkEndpoint(base); err != nil {
			return nil, err
		}
		if c.Key == "" {
			return nil, fmt.Errorf("no key. Set %s or apiKeyFile in pi-jev.json; the gate is inactive until then.", apiKeyEnv)
		}
		client, err := typesafe.NewClient(typesafe.Config{
			APIKey: c.Key, BaseURL: base, DefaultModel: c.Model,
			Timeout: time.Duration(c.TimeoutMs) * time.Millisecond,
			Retry:   typesafe.RetryOverrides{MaxRetries: typesafe.Ptr(c.Retries)},
			Getenv:  func(string) string { return "" },
			Logger:  discardLogger{}, LogLevel: typesafe.LogOff,
		})
		if err != nil {
			return nil, err
		}
		return &backend{kind: backendTypeSafe, dest: base + " (model " + c.Model + ")", client: client, cfg: c}, nil
	}
	ref := pigmodel.Ref{}
	if c.Model != "" {
		provider, id, ok := strings.Cut(c.Model, "/")
		if !ok {
			return nil, fmt.Errorf("\"model\" must be provider/model (got %q)", c.Model)
		}
		ref = pigmodel.Ref{Provider: provider, ID: id}
	} else if info, err := sctx.GetModelInfo(); err == nil && info != nil {
		ref = pigmodel.Ref{Provider: info.Provider, ID: info.ID}
	}
	if ref.ID == "" {
		return nil, errors.New("the model backend needs a model: none is selected in PiG and none is set as \"model\" in pi-jev.json")
	}
	name := ref.Provider + "/" + ref.ID
	return &backend{kind: backendModel, model: ref, cfg: c,
		dest: "the model " + name + " (through PiG, the provider that already receives your conversation)"}, nil
}

// evaluator returns what answers questions for one call. The own-model backend is
// bound to the call's context, so it is built per call.
func (b *backend) evaluator(sctx sdk.Context) (typesafe.Evaluator, error) {
	if b.kind == backendTypeSafe {
		return b.client, nil
	}
	m, err := pigmodel.New(sctx.ModelRegistry(), b.model)
	if err != nil {
		return nil, err
	}
	return ownmodel.New(ownmodel.Options{Model: m, AnswerMode: ownmodel.Probabilities, NormalizeProbabilities: true, MalformedRetries: 1})
}

// Ask sends state (a JSON document, or text when text is true) with the questions.
func (b *backend) Ask(ctx context.Context, sctx sdk.Context, state []byte, text bool, qs []question) (*response, error) {
	if err := validateQuestions(qs); err != nil {
		return nil, err
	}
	ev, err := b.evaluator(sctx)
	if err != nil {
		return nil, err
	}
	req := typesafe.SystemOneRequest{Questions: toTypeSafe(qs)}
	if text {
		var s string
		_ = json.Unmarshal(state, &s)
		req.State = typesafe.Text(s)
	} else {
		req.State = typesafe.Value(json.RawMessage(state))
	}
	res, err := ev.SystemOne(ctx, req, nil)
	if err != nil {
		return nil, err
	}
	return fromResult(res, qs)
}

type discardLogger struct{}

func (discardLogger) Debug(string, ...any) {}
func (discardLogger) Info(string, ...any)  {}
func (discardLogger) Warn(string, ...any)  {}
func (discardLogger) Error(string, ...any) {}
