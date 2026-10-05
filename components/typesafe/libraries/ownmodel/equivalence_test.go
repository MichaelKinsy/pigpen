package ownmodel

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/typesafe"
)

// The differential check against the Python oracle: port/equivalence/scenarios/*.json were
// run through the unmodified system-one-adapter 0.2.1 (e1d4cc9) by record_python.py, which
// recorded golden/*.json. Here the same scenarios run through the Go backend with the same
// scripted model responses, and the model calls (prompts and JSON Schema), the answers, the
// usage counters and the diagnostics must be the same.

type scenario struct {
	Name             string            `json:"name"`
	Mode             string            `json:"mode"`
	Structured       bool              `json:"structured"`
	Normalize        bool              `json:"normalize"`
	MalformedRetries int               `json:"malformedRetries"`
	State            json.RawMessage   `json:"state"`
	Questions        json.RawMessage   `json:"questions"`
	Responses        []json.RawMessage `json:"responses"`
}

type golden struct {
	Calls []struct {
		Messages   []Message      `json:"messages"`
		Schema     map[string]any `json:"schema"`
		Structured bool           `json:"structured"`
	} `json:"calls"`
	Answers      map[string]any `json:"answers"`
	Usage        map[string]any `json:"usage"`
	Debug        map[string]any `json:"debug"`
	RetryReasons []string       `json:"retryReasons"`
	Error        *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func loadJSON(t *testing.T, path string, v any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	noErr(t, err)
	noErr(t, json.Unmarshal(raw, v))
}

const (
	correctionPrefix = "The previous response did not match the required schema: "
	correctionSuffix = "\nReturn a single JSON object that matches the schema exactly, with no other text."
)

func TestEquivalence_GoBackendMatchesThePythonOracle(t *testing.T) {
	dir := filepath.Join("..", "..", "port", "equivalence")
	files, err := filepath.Glob(filepath.Join(dir, "scenarios", "*.json"))
	noErr(t, err)
	if len(files) < 15 {
		t.Fatalf("expected the recorded scenarios, found %d", len(files))
	}
	for _, file := range files {
		var sc scenario
		loadJSON(t, file, &sc)
		var g golden
		loadJSON(t, filepath.Join(dir, "golden", filepath.Base(file)), &g)
		t.Run(sc.Name, func(t *testing.T) {
			qs, err := typesafe.ParseQuestions(sc.Questions)
			noErr(t, err)
			var steps []any
			for _, r := range sc.Responses {
				if bytes.HasPrefix(bytes.TrimSpace(r), []byte(`"`)) {
					var s string
					noErr(t, json.Unmarshal(r, &s))
					steps = append(steps, s)
				} else {
					var buf bytes.Buffer
					noErr(t, json.Compact(&buf, r))
					steps = append(steps, buf.String())
				}
			}
			model := newScripted(steps...)
			b := mustNew(t, Options{Model: model, AnswerMode: AnswerMode(sc.Mode), StructuredOutputs: sc.Structured, NormalizeProbabilities: sc.Normalize, MalformedRetries: sc.MalformedRetries})
			ev, err := b.Evaluate(context.Background(), typesafe.SystemOneRequest{State: typesafe.Value(sc.State), Questions: qs}, nil)

			// The model calls: prompts and schema.
			eq(t, len(model.calls), len(g.Calls))
			for i, want := range g.Calls {
				got := model.calls[i]
				eq(t, len(got), len(want.Messages))
				for j, m := range want.Messages {
					if m.Role == RoleUser && strings.HasPrefix(m.Content, correctionPrefix) {
						// The validation error text is this port's own; the frame around it is the oracle's.
						if got[j].Role != RoleUser || !strings.HasPrefix(got[j].Content, correctionPrefix) || !strings.HasSuffix(got[j].Content, correctionSuffix) {
							t.Fatalf("call %d message %d: correction frame differs:\n%q", i, j, got[j].Content)
						}
						continue
					}
					if got[j] != m {
						t.Fatalf("call %d message %d differs\n got: %s\nwant: %s", i, j, got[j].Content, m.Content)
					}
				}
				eq(t, model.structured[i], want.Structured)
				sameJSON(t, model.schemas[i], want.Schema, "schema of call "+string(rune('0'+i)))
			}

			if g.Error != nil {
				if err == nil {
					t.Fatalf("the oracle failed with %s, Go succeeded", g.Error.Type)
				}
				eq(t, g.Error.Type, "TypeSafeAPIResponseValidationError")
				mustAs[*MalformedOutputError](t, err)
				de := mustAs[*DebugError](t, err)
				eq(t, categories(de.Debug.RetryReasons), g.RetryReasons)
				return
			}
			noErr(t, err)
			raw, err := json.Marshal(ev.Result.Answers)
			noErr(t, err)
			var answers map[string]any
			noErr(t, json.Unmarshal(raw, &answers))
			sameJSON(t, answers, g.Answers, "answers")
			usage := map[string]any{
				"input_tokens": intOrNil(ev.Usage.InputTokens), "output_tokens": intOrNil(ev.Usage.OutputTokens),
				"input_tokens_total": intOrNil(ev.Usage.InputTokensTotal), "output_tokens_total": intOrNil(ev.Usage.OutputTokensTotal),
				"n_retries": float64(ev.Usage.Retries), "n_retries_malformed_structure": float64(ev.Usage.MalformedRetries),
			}
			sameJSON(t, usage, g.Usage, "usage")
			eq(t, categories(ev.Debug.RetryReasons), g.RetryReasons)
			dbg := map[string]any{"max_error": ev.Debug.MaxError, "invalid_probs": float64(ev.Debug.InvalidProbs), "probability_errors": toAny(ev.Debug.ProbabilityErrors)}
			if len(ev.Debug.OriginalProbabilities) > 0 {
				dbg["original_probabilities"] = toAny(ev.Debug.OriginalProbabilities)
			}
			sameJSON(t, dbg, g.Debug, "debug")
		})
	}
}

func intOrNil(p *int) any {
	if p == nil {
		return nil
	}
	return float64(*p)
}

func toAny(v any) any {
	raw, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(raw, &out)
	return out
}

// sameJSON compares two decoded JSON values, numbers within a relative 1e-12.
func sameJSON(t *testing.T, got, want any, what string) {
	t.Helper()
	if !jsonEqual(toAny(got), toAny(want)) {
		g, _ := json.MarshalIndent(got, "", " ")
		w, _ := json.MarshalIndent(want, "", " ")
		t.Fatalf("%s differs\n got: %s\nwant: %s", what, g, w)
	}
}

func jsonEqual(a, b any) bool {
	switch x := a.(type) {
	case float64:
		y, ok := b.(float64)
		return ok && math.Abs(x-y) <= 1e-12*math.Max(1, math.Abs(y))
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, v := range x {
			w, ok := y[k]
			if !ok || !jsonEqual(v, w) {
				return false
			}
		}
		return true
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !jsonEqual(x[i], y[i]) {
				return false
			}
		}
		return true
	}
	return a == b
}
