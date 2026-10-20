package pi_permission_system

import (
	"encoding/json"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse"
)

// The differential: testdata/logging-oracle.json holds what the original's own functions answered for port/probe/logging-cases.json
// (recorded by port/record-logging-probe.sh). Every answer must be byte for byte the port's.

// lgBuild decodes a probe value spec (see port/probe/gen-logging-cases.py).
func lgBuild(spec any, ids map[string]any) any {
	switch s := spec.(type) {
	case []any:
		out := make([]any, len(s))
		for i, e := range s {
			out[i] = lgBuild(e, ids)
		}
		return out
	case *jsObject:
		has := func(k string) bool { _, ok := s.get(k); return ok }
		get := func(k string) any { v, _ := s.get(k); return v }
		switch {
		case has("$bigint"):
			n, _ := new(big.Int).SetString(get("$bigint").(string), 10)
			return n
		case has("$undefined"):
			return undef{}
		case has("$fn"):
			return func() {}
		case has("$nan"):
			return math.NaN()
		case has("$inf"):
			return math.Inf(int(get("$inf").(float64)))
		case has("$negzero"):
			return math.Copysign(0, -1)
		case has("$ref"):
			return ids[get("$ref").(string)]
		case has("$error"):
			e := get("$error").(*jsObject)
			name, _ := e.str("name")
			msg, _ := e.str("message")
			err := &JSError{Name: name, Message: msg}
			if st, ok := e.str("stack"); ok {
				err.Stack = &st
			}
			return err
		}
		out := newObject()
		if has("$id") {
			ids[get("$id").(string)] = out
		}
		for _, k := range s.keys {
			if k == "$id" {
				continue
			}
			out.set(k, lgBuild(s.vals[k], ids))
		}
		return out
	}
	return spec
}

func lgWidth(v any) float64 {
	if o, ok := v.(*jsObject); ok {
		if _, nan := o.get("$nan"); nan {
			return math.NaN()
		}
		f, _ := o.num("$inf")
		return math.Inf(int(f))
	}
	return v.(float64)
}

func lgOpt(o *jsObject, k string) (string, bool) { s, ok := o.str(k); return s, ok }

func TestLoggingOracle(t *testing.T) {
	data, err := os.ReadFile("testdata/logging-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	v, err := parseJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	root := v.(*jsObject)
	list := func(k string) []*jsObject {
		var out []*jsObject
		for _, e := range root.vals[k].([]any) {
			out = append(out, e.(*jsObject))
		}
		return out
	}
	// nullable: a JSON null is the original's undefined result.
	nullable := func(o *jsObject, k string) (string, bool) { s, ok := o.vals[k].(string); return s, ok }
	same := func(t *testing.T, got string, gotOK bool, o *jsObject, k, what string) {
		t.Helper()
		want, wantOK := nullable(o, k)
		if got != want || gotOK != wantOK {
			t.Errorf("%s: got %q (%v), the original %q (%v)", what, got, gotOK, want, wantOK)
		}
	}
	bashparse.ResetWarmBashParser()
	bashparse.WarmBashParser()
	t.Cleanup(bashparse.ResetWarmBashParser)

	t.Run("isSensitiveName", func(t *testing.T) {
		cs := list("sensitive")
		for _, c := range cs {
			name, _ := c.str("name")
			want, _ := c.flag("out")
			if IsSensitiveName(name) != want {
				t.Errorf("%q: got %v, the original %v", name, !want, want)
			}
		}
		t.Logf("%d names", len(cs))
	})
	t.Run("safe and redacted stringify", func(t *testing.T) {
		cs := list("stringify")
		for i, c := range cs {
			spec := c.vals["spec"]
			g, ok := SafeJSONStringify(lgBuild(spec, map[string]any{}))
			same(t, g, ok, c, "safe", "safe #"+strconv.Itoa(i)+" "+marshalJSON(spec, ""))
			g, ok = RedactedJSONStringify(lgBuild(spec, map[string]any{}))
			same(t, g, ok, c, "redacted", "redacted #"+strconv.Itoa(i)+" "+marshalJSON(spec, ""))
		}
		t.Logf("%d specs", len(cs))
	})
	t.Run("capLogFieldWidths", func(t *testing.T) {
		cs := list("cap")
		for i, c := range cs {
			g, ok := SafeJSONStringify(CapLogFieldWidths(lgBuild(c.vals["value"], map[string]any{}), lgWidth(c.vals["width"])))
			same(t, g, ok, c, "out", "cap #"+strconv.Itoa(i)+" "+marshalJSON(c.vals["value"], "")+" at "+marshalJSON(c.vals["width"], ""))
		}
		t.Logf("%d cases", len(cs))
	})
	t.Run("redactCommandSecrets", func(t *testing.T) {
		cs := list("redact")
		for _, c := range cs {
			in, _ := c.str("in")
			want, _ := c.str("out")
			if got := RedactCommandSecrets(in); got != want {
				t.Errorf("%q: got %q, the original %q", in, got, want)
			}
		}
		t.Logf("%d commands", len(cs))
	})
	t.Run("maskCommandFields", func(t *testing.T) {
		for i, c := range list("mask") {
			spec := lgBuild(c.vals["spec"], map[string]any{})
			// A mask spec may be a bare string or array: the original returns those as they are.
			var masked any
			if o, ok := spec.(*jsObject); ok {
				masked = MaskCommandFields(o)
			} else {
				masked = maskValue(spec, false, 0)
			}
			g, ok := SafeJSONStringify(masked)
			same(t, g, ok, c, "out", "mask #"+strconv.Itoa(i))
		}
	})
	t.Run("the writer's line", func(t *testing.T) {
		ts := regexp.MustCompile(`"timestamp":"[^"]*"`)
		cs := list("lines")
		for i, c := range cs {
			dir := t.TempDir()
			logs := filepath.Join(dir, "logs")
			stream, _ := c.str("stream")
			event, _ := c.str("event")
			cfg := LogConfig{DebugLog: true, PermissionReviewLog: true}
			if w, ok := c.num("width"); ok {
				cfg.ReviewLogFieldMaxWidth = &w
			}
			l := CreatePermissionSystemLogger(PermissionSystemLoggerOptions{
				GetConfig: func() LogConfig { return cfg }, DebugLogPath: filepath.Join(logs, "debug.jsonl"), ReviewLogPath: filepath.Join(logs, "review.jsonl"),
				EnsureLogsDirectory: func() string { os.MkdirAll(logs, 0o755); return "" },
			})
			details, _ := lgBuild(c.vals["details"], map[string]any{}).(*jsObject)
			var warning string
			path := filepath.Join(logs, "review.jsonl")
			if stream == "debug" {
				warning = l.Debug(event, details)
				path = filepath.Join(logs, "debug.jsonl")
			} else {
				warning = l.Review(event, details)
			}
			wantWarn, wantWarnOK := c.str("warning")
			if wantWarnOK != (warning != "") {
				t.Errorf("line #%d: warning %q, the original %q", i, warning, wantWarn)
				continue
			}
			want, ok := c.str("line")
			if !ok {
				continue
			}
			b, _ := os.ReadFile(path)
			norm := func(s string) string { return ts.ReplaceAllString(s, `"timestamp":"T"`) }
			if norm(string(b)) != norm(want) {
				t.Errorf("line #%d %s: got\n%s\nthe original\n%s", i, marshalJSON(c.vals["details"], ""), norm(string(b)), norm(want))
			}
		}
		t.Logf("%d lines", len(cs))
	})
	t.Run("presentation", func(t *testing.T) {
		cs := list("payloads")
		for i, c := range cs {
			raw, _ := json.Marshal(c.vals["payload"])
			p := lgDecodePayload(c.vals["payload"].(*jsObject))
			what := "payload #" + strconv.Itoa(i) + " " + string(raw)
			same(t, lgFacts(p), true, c, "facts", what)
			same(t, lgJSON(strSlice(FlaggedElements(p))), true, c, "flagged", what)
			same(t, FlaggedElementLabel(p), true, c, "flaggedLabel", what)
			same(t, ValueLabel(p), true, c, "valueLabel", what)
			label, ok := DescribeBashCommandContext(p.Request.CommandContext)
			if _, wantOK := c.str("context"); wantOK != ok {
				t.Errorf("%s: context ok %v", what, ok)
			} else if ok {
				same(t, label, true, c, "context", what)
			}
		}
		t.Logf("%d payloads", len(cs))
	})
	_ = strings.TrimSpace
}

func strSlice(s []string) []any {
	out := make([]any, len(s))
	for i, e := range s {
		out[i] = e
	}
	return out
}

func lgDecodePayload(o *jsObject) PromptPayload {
	optStr := func(m *jsObject, k string) *string {
		if s, ok := m.str(k); ok {
			return &s
		}
		return nil
	}
	req := o.obj("request")
	requester := req.obj("requester")
	kind, _ := o.str("kind")
	surface, _ := req.str("surface")
	value, _ := req.str("value")
	fwd, _ := requester.flag("forwarded")
	p := PromptPayload{Kind: kind, Request: PromptRequestFacts{
		Requester: PromptRequester{AgentName: optStr(requester, "agentName"), Forwarded: fwd, SessionID: optStr(requester, "sessionId")},
		Surface:   surface, ToolName: optStr(req, "toolName"), InvokedToolName: optStr(req, "invokedToolName"), Value: value,
		MatchedPattern: optStr(req, "matchedPattern"), CommandContext: optStr(req, "commandContext"), ExecutedUnit: optStr(req, "executedUnit"),
	}}
	for _, e := range o.vals["evidence"].([]any) {
		eo := e.(*jsObject)
		label, _ := eo.str("label")
		text, _ := eo.str("text")
		p.Evidence = append(p.Evidence, PromptEvidence{Label: label, Text: text, Detail: optStr(eo, "detail")})
	}
	return p
}
