// Package pigmodel is an ownmodel.Model backed by the PiG Go SDK's model access: the
// model PiG is configured with, authenticated and called through the host's provider
// layer (Context.ModelRegistry: Find, GetApiKeyAndHeaders, Complete). No provider or
// model name is built in.
//
// The SDK's sdk.ModelRegistry value (what ctx.ModelRegistry() returns) satisfies [Registry] as is:
//
//	info, err := ctx.GetModelInfo()                       // the active model; nil when none is set
//	m, err := pigmodel.New(ctx.ModelRegistry(), pigmodel.Ref{Provider: info.Provider, ID: info.ID})
//	backend, err := ownmodel.New(ownmodel.Options{Model: m})
//
// PiG's model access has no native structured-output mode, so this Model serves
// prompted mode only: a request with Structured set fails with a *typesafe.TypeSafeError.
package pigmodel

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/ownmodel"
	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/typesafe"
)

// Registry is the part of the PiG Go SDK's ModelRegistry this package uses.
type Registry interface {
	Find(providerID, modelID string) map[string]any
	GetApiKeyAndHeaders(model map[string]any) (map[string]any, error)
	Complete(model, request, options map[string]any) map[string]any
}

// Ref names a model of the host's registry.
type Ref struct {
	Provider string
	ID       string
}

// Model is an ownmodel.Model on a [Registry].
type Model struct {
	reg Registry
	ref Ref

	mu    sync.Mutex
	model map[string]any
}

var _ ownmodel.Model = (*Model)(nil)

// New returns a Model for ref. The registry is asked for the model and its auth on
// the first request, not here.
func New(reg Registry, ref Ref) (*Model, error) {
	if reg == nil {
		return nil, &typesafe.TypeSafeError{Message: "A model registry is required."}
	}
	if ref.ID == "" {
		return nil, &typesafe.TypeSafeError{Message: "A model ID is required."}
	}
	return &Model{reg: reg, ref: ref}, nil
}

// Name returns the model ID.
func (m *Model) Name() string { return m.ref.ID }

// Complete sends the conversation through the registry. The system message becomes the
// request's system prompt. A stop reason of "error" is a *typesafe.APIError when the
// provider's message starts with an HTTP status (so the retry policy classifies it), else a
// retryable *typesafe.APIConnectionError carrying the provider's message; "aborted" is an
// *typesafe.APIUserAbortError; "length" (output limit), "toolUse" and any other non-"stop"
// reason are plain *typesafe.TypeSafeError values that name the reason. A cancelled
// ctx returns an *typesafe.APIUserAbortError without waiting for the host.
func (m *Model) Complete(ctx context.Context, req ownmodel.Request) (ownmodel.Result, error) {
	if req.Structured {
		return ownmodel.Result{}, &typesafe.TypeSafeError{Message: "PiG's model access has no native structured-output mode; use prompted mode (StructuredOutputs false)."}
	}
	if ctx.Err() != nil {
		return ownmodel.Result{}, typesafe.NewAbortError(context.Cause(ctx))
	}
	type outcome struct {
		res ownmodel.Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := m.complete(req)
		done <- outcome{res, err}
	}()
	select {
	case o := <-done:
		return o.res, o.err
	case <-ctx.Done():
		// The host call cannot be interrupted from here; its result is dropped when it arrives.
		return ownmodel.Result{}, typesafe.NewAbortError(context.Cause(ctx))
	}
}

func (m *Model) resolve() (map[string]any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.model != nil {
		return m.model, nil
	}
	found := m.reg.Find(m.ref.Provider, m.ref.ID)
	if found == nil {
		return nil, &typesafe.TypeSafeError{Message: fmt.Sprintf("The model %s/%s was not found in the model registry.", m.ref.Provider, m.ref.ID)}
	}
	m.model = found
	return found, nil
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// providerAndID reads a registry model the way the SDK does: the provider is a string or an
// object with an id, the model ID is "modelId" or "id".
func providerAndID(model map[string]any) (provider, id string) {
	provider = str(model["provider"])
	if provider == "" {
		if p, ok := model["provider"].(map[string]any); ok {
			provider = str(p["id"])
		}
	}
	id = str(model["modelId"])
	if id == "" {
		id = str(model["id"])
	}
	return provider, id
}

func textBlocks(text string) []any {
	return []any{map[string]any{"type": "text", "text": text}}
}

func zeroUsage() map[string]any {
	return map[string]any{
		"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0,
		"cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0},
	}
}

func buildRequest(model map[string]any, msgs []ownmodel.Message) map[string]any {
	provider, id := providerAndID(model)
	now := time.Now().UnixMilli()
	var system []string
	var out []any
	for _, msg := range msgs {
		switch msg.Role {
		case ownmodel.RoleSystem:
			system = append(system, msg.Content)
		case ownmodel.RoleAssistant:
			out = append(out, map[string]any{
				"role": "assistant", "content": textBlocks(msg.Content),
				"api": str(model["api"]), "provider": provider, "model": id,
				"usage": zeroUsage(), "stopReason": "stop", "timestamp": now,
			})
		default:
			out = append(out, map[string]any{"role": "user", "content": textBlocks(msg.Content), "timestamp": now})
		}
	}
	req := map[string]any{"messages": out}
	if len(system) > 0 {
		req["systemPrompt"] = strings.Join(system, "\n\n")
	}
	return req
}

func (m *Model) complete(req ownmodel.Request) (ownmodel.Result, error) {
	model, err := m.resolve()
	if err != nil {
		return ownmodel.Result{}, err
	}
	auth, err := m.reg.GetApiKeyAndHeaders(model)
	if err != nil {
		return ownmodel.Result{}, &typesafe.TypeSafeError{Message: "Could not resolve the model's credentials: " + err.Error(), Cause: err}
	}
	options := map[string]any{}
	if auth != nil {
		if ok, present := auth["ok"].(bool); present && !ok {
			msg := str(auth["error"])
			if msg == "" {
				msg = "the registry reported no credentials"
			}
			return ownmodel.Result{}, &typesafe.TypeSafeError{Message: "Could not resolve the model's credentials: " + msg}
		}
		if key := str(auth["apiKey"]); key != "" {
			options["apiKey"] = key
		}
		if headers, ok := auth["headers"]; ok && headers != nil {
			options["headers"] = headers
		}
	}
	reply := m.reg.Complete(model, buildRequest(model, req.Messages), options)
	if reply == nil {
		return ownmodel.Result{}, &typesafe.TypeSafeError{Message: "The host returned no model result."}
	}
	return mapReply(reply)
}

var leadingStatus = regexp.MustCompile(`^\s*([1-5]\d\d)\b\s*(.*)$`)

func toInt(v any) *int {
	var n int
	switch x := v.(type) {
	case float64:
		n = int(x)
	case int:
		n = x
	case int64:
		n = int(x)
	default:
		return nil
	}
	return &n
}

// mapReply converts the terminal assistant message of the host to a Result or an error.
func mapReply(reply map[string]any) (ownmodel.Result, error) {
	reason := str(reply["stopReason"])
	switch reason {
	case "", "stop":
	case "error":
		msg := strings.TrimSpace(str(reply["errorMessage"]))
		if match := leadingStatus.FindStringSubmatch(msg); match != nil {
			if status, _ := strconv.Atoi(match[1]); status >= 400 {
				var body any
				if match[2] != "" {
					body = match[2]
				}
				return ownmodel.Result{}, typesafe.NewAPIError(status, body, nil)
			}
		}
		if msg == "" {
			msg = "the model reported an error"
		}
		return ownmodel.Result{}, typesafe.NewConnectionError(errors.New(msg))
	case "aborted":
		return ownmodel.Result{}, typesafe.NewAbortError(context.Canceled)
	case "length":
		return ownmodel.Result{}, &typesafe.TypeSafeError{Message: "The model stopped at its output token limit before finishing the answer. Ask fewer questions or raise the model's maximum output tokens."}
	case "toolUse":
		return ownmodel.Result{}, &typesafe.TypeSafeError{Message: "The model tried to call a tool instead of answering."}
	default:
		return ownmodel.Result{}, &typesafe.TypeSafeError{Message: fmt.Sprintf("The model stopped with reason %q instead of answering.", reason)}
	}
	var text strings.Builder
	if blocks, ok := reply["content"].([]any); ok {
		for _, b := range blocks {
			if block, ok := b.(map[string]any); ok && str(block["type"]) == "text" {
				text.WriteString(str(block["text"]))
			}
		}
	}
	res := ownmodel.Result{Text: text.String()}
	if usage, ok := reply["usage"].(map[string]any); ok {
		res.InputTokens, res.OutputTokens = toInt(usage["input"]), toInt(usage["output"])
	}
	return res, nil
}
