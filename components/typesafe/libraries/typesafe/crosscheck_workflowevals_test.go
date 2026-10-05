package typesafe_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/typesafe"
)

// WorkflowEvals cross-check (typesafe-ai/WorkflowEvals, Apache-2.0, pinned in PORT.md).
// extract_workflowevals.py runs the four workflows' own eval adapters and records every
// system_one call: the real question sets, and the request body the Python SDK builds.
// record_js.mjs sends the same inputs through the official TS SDK and records a hash of the
// canonical body. Here the Go client sends the same inputs to a fake server and its body must
// equal both, whatever the key order or escaping.

type weCase struct {
	Workflow   string          `json:"workflow"`
	CaseID     string          `json:"case_id"`
	Node       string          `json:"node"`
	State      json.RawMessage `json:"state"`
	Questions  json.RawMessage `json:"questions"`
	PythonBody string          `json:"python_body"`
}

type weGolden struct {
	SHA256 string `json:"sha256"`
	Order  string `json:"order"`
}

// canon renders JSON with sorted keys, compact, and non-ASCII escaped, like record_js.mjs.
func canon(t testing.TB, raw []byte) string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	canonWrite(&b, v)
	return b.String()
}

func canonString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r < 0x20 || (r >= 0x7f && r <= 0xffff):
			fmt.Fprintf(b, `\u%04x`, r)
		case r > 0xffff:
			r -= 0x10000
			fmt.Fprintf(b, `\u%04x\u%04x`, 0xd800+(r>>10), 0xdc00+(r&0x3ff))
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
}

func canonWrite(b *strings.Builder, v any) {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			canonString(b, k)
			b.WriteByte(':')
			canonWrite(b, x[k])
		}
		b.WriteByte('}')
	case []any:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			canonWrite(b, e)
		}
		b.WriteByte(']')
	case string:
		canonString(b, x)
	case json.Number:
		if f, err := x.Float64(); err == nil && strings.ContainsAny(x.String(), ".eE") {
			b.WriteString(strconv.FormatFloat(f, 'f', -1, 64))
		} else {
			b.WriteString(x.String())
		}
	case bool:
		b.WriteString(strconv.FormatBool(x))
	case nil:
		b.WriteString("null")
	}
}

func keyOrder(body string) string {
	dec := json.NewDecoder(strings.NewReader(body))
	var keys []string
	if _, err := dec.Token(); err != nil {
		return ""
	}
	for dec.More() {
		k, _ := dec.Token()
		keys = append(keys, k.(string))
		var skip json.RawMessage
		_ = dec.Decode(&skip)
	}
	return strings.Join(keys, ",")
}

// goBody returns the body the Go client sends for a WorkflowEvals call.
func goBody(t *testing.T, c weCase) string {
	t.Helper()
	srv := newScripted([]step{{Status: 200, Body: json.RawMessage(`{"model":"m","answers":{},"usage":{"input_tokens":0,"output_tokens":0}}`)}})
	defer srv.srv.Close()
	client, err := typesafe.NewClient(typesafe.Config{APIKey: "test-key-not-real", BaseURL: srv.srv.URL, LogLevel: typesafe.LogOff,
		Retry: typesafe.RetryOverrides{MaxRetries: typesafe.Ptr(0)}, Getenv: func(string) string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	qs, err := typesafe.ParseQuestions(c.Questions)
	if err != nil {
		t.Fatalf("ParseQuestions: %v", err)
	}
	var state typesafe.Entry
	if err := json.Unmarshal(c.State, &state); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SystemOne(t.Context(), typesafe.SystemOneRequest{State: state, Questions: qs, Model: "m"}, nil); err != nil {
		t.Fatalf("SystemOne: %v", err)
	}
	seen := srv.snapshot()
	if len(seen) != 1 {
		t.Fatalf("%d requests", len(seen))
	}
	return seen[0].Body
}

func TestCrossCheck_WorkflowEvalsRequestsMatchTheOfficialSDKAndThePythonSDK(t *testing.T) {
	var cases []weCase
	readJSON(t, "../../port/crosscheck/workflowevals.json", &cases)
	var goldens []weGolden
	readJSON(t, "../../port/crosscheck/golden_workflowevals.json", &goldens)
	if len(cases) != len(goldens) || len(cases) == 0 {
		t.Fatalf("%d cases, %d goldens: re-extract and re-record", len(cases), len(goldens))
	}
	kinds := map[string]int{}
	for i, c := range cases {
		t.Run(fmt.Sprintf("%s-%s-%d", c.Workflow, c.Node, i), func(t *testing.T) {
			body := goBody(t, c)
			sum := sha256.Sum256([]byte(canon(t, []byte(body))))
			if got := hex.EncodeToString(sum[:]); got != goldens[i].SHA256 {
				t.Errorf("Go body differs from the official SDK's:\n go     %s\n python %s", canon(t, []byte(body)), canon(t, []byte(c.PythonBody)))
			}
			if canon(t, []byte(body)) != canon(t, []byte(c.PythonBody)) {
				t.Errorf("Go body differs from the Python SDK's body")
			}
			if got := keyOrder(body); got != goldens[i].Order {
				t.Errorf("key order: Go %s, official %s", got, goldens[i].Order)
			}
			var qs map[string]struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(c.Questions, &qs)
			for _, q := range qs {
				kinds[q.Type]++
			}
		})
	}
	t.Logf("%d calls; question types seen: %v", len(cases), kinds)
}

// WORKFLOWEVALS_FULL names a file written by extract_workflowevals.py's second argument: real
// dataset states (not committed). The Go body must equal the Python SDK's body for them too.
func TestCrossCheck_WorkflowEvalsRealStatesMatchThePythonSDK(t *testing.T) {
	path := os.Getenv("WORKFLOWEVALS_FULL")
	if path == "" {
		t.Skip("WORKFLOWEVALS_FULL is not set (the real dataset states are not committed)")
	}
	var cases []weCase
	readJSON(t, path, &cases)
	for i, c := range cases {
		t.Run(fmt.Sprintf("%s-%s-%d", c.Workflow, c.Node, i), func(t *testing.T) {
			body := goBody(t, c)
			if canon(t, []byte(body)) != canon(t, []byte(c.PythonBody)) {
				t.Errorf("Go body differs from the Python SDK's body")
			}
		})
	}
}
