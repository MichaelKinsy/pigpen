package pi_subagents

import (
	"strings"
	"testing"
)

// Twins of test/unit/workflow-preflight.test.ts.
const preflightFile = "workflow-preflight"

func basePreflightObject() *jsObject {
	return jo("version", float64(1), "coverage", "complete", "lanes", []any{jo("key", "issue1624-writer", "mode", "mutation", "decision", "Implement bounded preflight", "claims", []any{"src/workflows/workflow-preflight.ts"}, "expectedOutput", "writer and review report", "independence", "does not touch cleanup")})
}

func mustPreflight(t *testing.T, v any) *WorkflowPreflight {
	t.Helper()
	p, err := NormalizeWorkflowPreflight(v)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWorkflowPreflightTwins(t *testing.T) {
	tw(t, preflightFile, "normalizes bounded display hints and renders the compact table", func(t *testing.T) {
		in := basePreflightObject()
		lane := in.vals["lanes"].([]any)[0].(*jsObject)
		lane.set("key", " issue1624-writer ")
		lane.set("decision", "Implement\n bounded\tpreflight")
		n := mustPreflight(t, in)
		jsonEq(t, n, `{"version":1,"coverage":"complete","lanes":[{"key":"issue1624-writer","mode":"mutation","decision":"Implement bounded preflight","claims":["src/workflows/workflow-preflight.ts"],"expectedOutput":"writer and review report","independence":"does not touch cleanup"}]}`)
		table := FormatWorkflowPreflight(n, "")
		for _, want := range []string{"Preflight: v1 \u00b7 complete \u00b7 1 lane", "key | mode | decision | claims | expected output | independence", "issue1624-writer | mutation | Implement bounded preflight"} {
			if !strings.Contains(table, want) {
				t.Errorf("table lacks %q:\n%s", want, table)
			}
		}
		if !strings.Contains(FormatWorkflowPreflightSummary(n), "preflight \u00b7 complete \u00b7 1 lane: issue1624-writer") {
			t.Errorf("summary = %q", FormatWorkflowPreflightSummary(n))
		}
		if got := FormatWorkflowPreflightPlanSummary(n, ""); got != "Plan: 1 lane \u00b7 Implement bounded preflight" {
			t.Errorf("plan = %q", got)
		}
		if got := FormatWorkflowPreflightWarningSummary([]string{"one", "two"}, "  ", "expand for debug"); got != "  Plan note: 2 preflight mismatches \u00b7 expand for debug." {
			t.Errorf("warning summary = %q", got)
		}
	})

	tw(t, preflightFile, "rejects unsupported, malformed, duplicate, and over-bound metadata", func(t *testing.T) {
		lanes := func(l ...any) []any { return l }
		for _, c := range []struct {
			value   any
			pattern string
		}{
			{jo("version", float64(2), "lanes", []any{}), `preflight\.version`},
			{jo("version", float64(1), "lanes", []any{}, "extra", true), `unsupported field 'extra'`},
			{jo("version", float64(1), "coverage", "strict", "lanes", []any{}), `coverage`},
			{jo("version", float64(1), "lanes", lanes(jo("key", "bad key"))), `lanes\[0\]\.key`},
			{jo("version", float64(1), "lanes", lanes(jo("key", "same"), jo("key", "same"))), `duplicate key`},
			{jo("version", float64(1), "lanes", lanes(jo("key", "same", "mode", "owner"))), `mode`},
			{jo("version", float64(1), "lanes", lanes(jo("key", "same", "claims", []any{"claim", "claim", "claim", "claim", "claim", "claim", "claim", "claim", "claim", "claim", "claim", "claim", "claim", "claim", "claim", "claim", "claim"}))), `at most`},
			{jo("version", float64(1), "lanes", lanes(jo("key", "same", "decision", strings.Repeat("x", WorkflowPreflightMaxStringLength+1)))), `maximum length`},
		} {
			_, err := NormalizeWorkflowPreflight(c.value)
			matchErrString(t, err, c.pattern)
		}
		var many []any
		for i := range WorkflowPreflightMaxLanes + 1 {
			many = append(many, jo("key", "lane-"+jsNumber(float64(i))))
		}
		_, err := NormalizeWorkflowPreflight(jo("version", float64(1), "lanes", many))
		matchErrString(t, err, `at most`)
		// (the original also passes an accessor property, which has no representation in parsed JSON data)
		var large []any
		for i := range WorkflowPreflightMaxLanes {
			large = append(large, jo("key", "large-"+jsNumber(float64(i)), "decision", strings.Repeat("x", WorkflowPreflightMaxStringLength)))
		}
		tooLarge := jo("version", float64(1), "lanes", large)
		if len(marshalJSON(tooLarge, "")) <= WorkflowPreflightMaxBytes {
			t.Fatalf("fixture is not too large")
		}
		_, err = NormalizeWorkflowPreflight(tooLarge)
		matchErrString(t, err, `canonical JSON`)
		if !ValidateWorkflowPreflight(basePreflightObject()).OK || ValidateWorkflowPreflight(jo("version", float64(1), "lanes", lanes(jo("key", "bad key")))).OK {
			t.Errorf("validateWorkflowPreflight")
		}
	})

	basePreflight := func(t *testing.T) *WorkflowPreflight { return mustPreflight(t, basePreflightObject()) }

	tw(t, preflightFile, "reports bounded advisory mismatches and ignores auto-resume launches", func(t *testing.T) {
		p := basePreflight(t)
		trace := []WorkflowTraceLike{{Operation: "run", Key: "issue1624-writer"}, {Operation: "run", Key: "undeclared"}, {Operation: "run", Key: "undeclared"}, {Operation: "run", Key: "issue1624-writer", Phase: "auto-resume"}}
		want := []string{"Preflight advisory: workflow key 'undeclared' launched without a declared lane."}
		for _, settled := range []bool{false, true} {
			if got := WorkflowPreflightWarnings(p, trace, settled); strings.Join(got, "|") != want[0] {
				t.Fatalf("settled=%v: %v", settled, got)
			}
		}
		annotated := AnnotateWorkflowPreflightTrace(trace, p)
		if !strings.Contains(annotated[1].Warning, "undeclared") || annotated[2].Warning != "" || annotated[3].Warning != "" {
			t.Fatalf("annotated = %+v", annotated)
		}
	})

	tw(t, preflightFile, "reports declared-but-never-launched lanes only at settlement and caps warnings", func(t *testing.T) {
		var ls []any
		var extra []WorkflowTraceLike
		for i := range WorkflowPreflightMaxLanes {
			ls = append(ls, jo("key", "declared-"+jsNumber(float64(i))))
			extra = append(extra, WorkflowTraceLike{Operation: "run", Key: "extra-" + jsNumber(float64(i))})
		}
		many := mustPreflight(t, jo("version", float64(1), "lanes", ls))
		warnings := WorkflowPreflightWarnings(many, extra, true)
		if len(warnings) != WorkflowPreflightMaxWarnings || !strings.Contains(warnings[len(warnings)-1], "additional mismatch warning(s) omitted") {
			t.Fatalf("warnings = %v", warnings)
		}
		if got := WorkflowPreflightWarnings(many, nil, false); len(got) != 0 {
			t.Fatalf("unsettled warnings = %v", got)
		}
		found := false
		for _, w := range WorkflowPreflightWarnings(many, nil, true) {
			found = found || strings.Contains(w, "declared lane 'declared-0' was not launched")
		}
		if !found || WorkflowPreflightMaxClaims <= 0 {
			t.Fatalf("no unlaunched warning")
		}
	})

	tw(t, preflightFile, "selects exact lanes before generated or dotted-root matches", func(t *testing.T) {
		p := mustPreflight(t, jo("version", float64(1), "coverage", "partial", "lanes", []any{jo("key", "writer", "mode", "mutation"), jo("key", "writer.quality", "mode", "review")}))
		for _, c := range []struct {
			key       string
			preferred []string
			mode      string
		}{{"writer.quality", nil, "review"}, {"writer.quality.deep", []string{"writer"}, "review"}, {"generated", []string{"writer"}, "mutation"}} {
			if got := WorkflowPreflightLaneForRuntimeKey(p, c.key, c.preferred...); got == nil || got.Mode != c.mode {
				t.Fatalf("%s: %+v", c.key, got)
			}
		}
	})

	tw(t, preflightFile, "treats exact and direct dotted keys as declared lane stages by convention", func(t *testing.T) {
		lanes := mustPreflight(t, jo("version", float64(1), "coverage", "complete", "lanes", []any{jo("key", "audit")}))
		generated := []WorkflowTraceLike{{Operation: "run", Key: "audit.writer", GeneratedLaneKey: "audit"}, {Operation: "run", Key: "audit.review", GeneratedLaneKey: "audit"}}
		if w := WorkflowPreflightWarnings(lanes, generated, true); len(w) != 0 {
			t.Fatalf("warnings = %v", w)
		}
		for _, e := range AnnotateWorkflowPreflightTrace(generated, lanes) {
			if e.Warning != "" {
				t.Fatalf("annotated %+v", e)
			}
		}
		direct := []WorkflowTraceLike{{Operation: "run", Key: "audit.shadow"}, {Operation: "run", Key: "audit-other.writer"}}
		want := "Preflight advisory: workflow key 'audit-other.writer' launched without a declared lane."
		for _, settled := range []bool{false, true} {
			if got := WorkflowPreflightWarnings(lanes, direct, settled); strings.Join(got, "|") != want {
				t.Fatalf("settled=%v: %v", settled, got)
			}
		}
		annotated := AnnotateWorkflowPreflightTrace(direct, lanes)
		if annotated[0].Warning != "" || !strings.Contains(annotated[1].Warning, "without a declared lane") {
			t.Fatalf("annotated = %+v", annotated)
		}
		if w := WorkflowPreflightWarnings(lanes, []WorkflowTraceLike{{Operation: "run", Key: "audit"}}, true); len(w) != 0 {
			t.Fatalf("exact = %v", w)
		}
		got := WorkflowPreflightWarnings(lanes, []WorkflowTraceLike{{Operation: "run", Key: "audit-quality"}}, true)
		if strings.Join(got, "|") != "Preflight advisory: workflow key 'audit-quality' launched without a declared lane.|Preflight advisory: declared lane 'audit' was not launched." {
			t.Fatalf("hyphenated = %v", got)
		}
	})
}

func matchErrString(t *testing.T, err error, pattern string) {
	t.Helper()
	if err == nil {
		t.Fatalf("no error; want /%s/", pattern)
	}
	matchErr(t, &WorkflowScriptError{Message: err.Error()}, pattern)
}
