package pitypesafe

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/typesafe"
)

// isolate points every path and every key variable at a temporary directory (rule 17): a test never
// reads or writes the real agent directory, and never sees a key from the environment.
func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("PIG_HOME", filepath.Join(dir, "pighome"))
	t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(dir, "agent"))
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(dir, "pi-agent"))
	t.Setenv("PIG_USE_PI_DIRS", "")
	for _, name := range []string{"TYPESAFE_API_KEY", "OPENROUTER_API_KEY", "COMMANDCODE_API_KEY", "GATEWAY_JEV_KEY",
		"PI_TYPESAFE_ENABLED", "PI_TYPESAFE_MAX_REQUESTS_PER_DAY", "PI_TYPESAFE_MAX_INPUT_TOKENS_PER_DAY", "PI_TYPESAFE_MAX_USD_PER_DAY",
		"TYPESAFE_BASE_URL", "TYPESAFE_LOG_LEVEL"} {
		t.Setenv(name, "")
	}
	return filepath.Join(dir, "agent")
}

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

func jsonResponse(status int, body any, header http.Header) *http.Response {
	data, _ := json.Marshal(body)
	if header == nil {
		header = http.Header{}
	}
	header.Set("Content-Type", "application/json")
	return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: header, Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data))}
}

func rawResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader([]byte(body)))}
}

// answersFor builds the answers a server returns for questions: the first option with certainty.
func answersFor(questions typesafe.Questions) map[string]any {
	answers := map[string]any{}
	for _, nq := range questions {
		switch q := nq.Question.(type) {
		case typesafe.NoulQuestion:
			answers[nq.Name] = map[string]any{"type": "noul", "noul": 0.9}
		case typesafe.ChoiceQuestion:
			probs := map[string]any{}
			for i, o := range q.Criteria {
				probs[o.Label] = map[bool]float64{true: 1, false: 0}[i == 0]
			}
			answers[nq.Name] = map[string]any{"type": "choice", "choice": q.Criteria[0].Label, "confidence": 1, "probabilities": probs}
		case typesafe.ScoreQuestion:
			probs, legend := map[string]any{}, map[string]any{}
			for i, level := range q.Criteria {
				probs[itoa(i)] = map[bool]float64{true: 1, false: 0}[i == 0]
				legend[itoa(i)] = level.Data()
			}
			answers[nq.Name] = map[string]any{"type": "score", "score": 0, "confidence": 1, "probabilities": probs, "legend": legend}
		}
	}
	return answers
}

func itoa(i int) string { b, _ := json.Marshal(i); return string(b) }

// responseFor is the successful reply of the fake TypeSafe server to a request body.
func responseFor(body []byte) *http.Response {
	var req struct {
		Questions json.RawMessage `json:"questions"`
	}
	_ = json.Unmarshal(body, &req)
	questions, err := typesafe.ParseQuestions(req.Questions)
	if err != nil {
		return jsonResponse(400, map[string]any{"error": "bad questions"}, nil)
	}
	return jsonResponse(200, map[string]any{"model": "jev-test", "answers": answersFor(questions), "usage": map[string]any{"input_tokens": 42, "output_tokens": 0}}, nil)
}

func requestBody(t *testing.T, r *http.Request) []byte {
	t.Helper()
	if r.Body == nil {
		return nil
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// answering is a fake server that answers every questions request and counts the calls.
type answering struct {
	mu    sync.Mutex
	calls int
	urls  []string
	seen  []*http.Request
}

func (a *answering) Do(r *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(r.Body)
	a.mu.Lock()
	a.calls++
	a.urls = append(a.urls, r.URL.String())
	a.seen = append(a.seen, r)
	a.mu.Unlock()
	return responseFor(body), nil
}

func (a *answering) count() int { a.mu.Lock(); defer a.mu.Unlock(); return a.calls }

func sampleRequest() typesafe.SystemOneRequest {
	return typesafe.SystemOneRequest{State: typesafe.Text("synthetic"), Questions: typesafe.Questions{typesafe.Ask("yes", typesafe.Noul("Is this synthetic?"))}}
}

func manyQuestions(n int) typesafe.Questions {
	qs := make(typesafe.Questions, n)
	for i := range qs {
		qs[i] = typesafe.Ask("q"+itoa(i), typesafe.Noul("Question "+itoa(i)+"?"))
	}
	return qs
}

func hasCode(err error, code ErrorCode) bool {
	ie, ok := err.(*IntegrationError)
	return ok && ie.Code == code
}

func mustTree(t *testing.T, text string) any {
	t.Helper()
	v, err := ParseJSON([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(path, mode)
}

var _ = context.Background

func regexpMatch(pattern, s string) bool { return regexp.MustCompile(pattern).MatchString(s) }

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func apiError(status int) error { return typesafe.NewAPIError(status, nil, http.Header{}) }
