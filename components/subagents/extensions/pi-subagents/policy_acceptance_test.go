package pi_subagents

import "testing"

func TestAcceptanceValidation(t *testing.T) {
	tw(t, "acceptance", "normalizes JSON-encoded acceptance objects before existing validation", func(t *testing.T) {
		inline := js(t, `{"level":"checked","evidence":["commands-run","changed-files"]}`)
		encoded := marshalJSON(inline, "")
		got, err := normalizeAcceptanceInput(encoded)
		if err != nil {
			t.Fatal(err)
		}
		equal(t, marshalJSON(got, ""), marshalJSON(inline, ""), "normalizeAcceptanceInput")
		equal(t, validateAcceptanceInput(encoded, "acceptance"), validateAcceptanceInput(inline, "acceptance"), "same validation")
		polMatches(t, joinLines(validateAcceptanceInput(`{"evidence":"commands-run"}`, "acceptance")), `acceptance\.evidence must be an array`)
		polMatches(t, joinLines(validateAcceptanceInput(`{"level":"checked"`, "acceptance")), `acceptance JSON string must encode a valid acceptance object`, "i")
		polMatches(t, joinLines(validateAcceptanceInput(`["checked"]`, "acceptance")), `acceptance JSON string must encode an object`, "i")
	})
	tw(t, "acceptance", "validates invalid disable and verify shapes", func(t *testing.T) {
		v := func(in any) []string { return validateAcceptanceInput(in, "acceptance") }
		j := func(s string) []string { return v(js(t, s)) }
		equal(t, j(`{"level":"none"}`), []string{"acceptance.reason is required when level is none."}, "none without reason")
		equal(t, v("none"), []string{`acceptance level "none" requires a reason; use { level: "none", reason: "..." }.`}, "none string")
		polMatches(t, joinLines(v("verified")), `object form with at least one runtime verify command.*Use level "checked" or provide a non-empty acceptance\.verify array`)
		for _, in := range []string{`{"level":"verified"}`, `{"level":"verified","verify":[]}`} {
			polMatches(t, joinLines(j(in)), `at least one runtime command when level is verified.*Use level "checked" or provide a non-empty acceptance\.verify array`)
		}
		equal(t, j(`{"level":"verified","verify":[{"id":"tests","command":"npm test"}]}`), []string{}, "verified with verify")
		equal(t, j(`{"level":"verified","verify":[{"id":"tests","command":"npm test"},{"id":"lint","command":"npm run lint"}]}`), []string{}, "two verify")
		equal(t, j(`{"level":"checked"}`), []string{}, "checked")
		equal(t, j(`{"level":"checked","report":"on"}`), []string{}, "report on")
		equal(t, j(`{"level":"checked","preserveStagedIndex":true}`), []string{}, "preserve")
		polMatches(t, joinLines(j(`{"level":"checked","preserveStagedIndex":false}`)), `preserveStagedIndex must be true`)
		polMatches(t, joinLines(j(`{"preserveStagedIndex":true}`)), `requires level checked or verified`)
		polMatches(t, joinLines(j(`{"report":"sometimes"}`)), `acceptance\.report must be on or off`)
		equal(t, j(`{"verify":[{"id":"missing-command"}]}`), []string{"acceptance.verify[0].command is required."}, "missing command")
		equal(t, j(`{"verify":[{"id":"fractional","command":"npm test","timeoutMs":1.5}]}`), []string{"acceptance.verify[0].timeoutMs must be an integer >= 1."}, "fractional")
		equal(t, v(false), []string{}, "false")
		equal(t, v("checked"), []string{}, "checked string")
		polMatches(t, joinLines(v("reviewed")), `achieved status.*omit acceptance.*acceptance\.review\.required`, "i", "s")
		polMatches(t, joinLines(j(`{"level":"reviewed"}`)), `achieved status.*omit acceptance.*acceptance\.review\.required`, "i", "s")
		equal(t, j(`{"criteria":["ship the fix"],"review":false,"stopRules":["stay scoped"]}`), []string{}, "criteria strings")
		polMatches(t, joinLines(j(`{"criteria":[{"id":"missing-must"}]}`)), `acceptance\.criteria\[0\]\.must is required`)
		polMatches(t, joinLines(j(`{"criteria":[{"id":"Release_Check","must":"first"},{"id":"release check","must":"second"}]}`)), `acceptance\.criteria\[1\]\.id duplicates normalized criterion id 'release-check'`)
		polMatches(t, joinLines(j(`{"criteria":[123]}`)), `acceptance\.criteria\[0\] must be a string or an object`)
		polMatches(t, joinLines(j(`{"evidence":"commands-run"}`)), `acceptance\.evidence must be an array.*Supported evidence kinds:.*commands-run.*changed-files`, "s")
		polMatches(t, joinLines(j(`{"evidence":["bogus"]}`)), `acceptance\.evidence\[0\] "bogus" is not a supported evidence kind.*Supported evidence kinds:.*manual-notes.*Example:`, "s")
		polMatches(t, joinLines(j(`{"criteria":[{"id":"ship","must":"ship","evidence":["commands_run"]}]}`)), `acceptance\.criteria\[0\]\.evidence\[0\] "commands_run" is not a supported evidence kind.*Supported evidence kinds:`, "s")
		polMatches(t, joinLines(j(`{"review":true}`)), `acceptance\.review must be false or an object`)
		polMatches(t, joinLines(j(`{"review":{"required":"yes"}}`)), `acceptance\.review\.required must be a boolean`)
		polMatches(t, joinLines(j(`{"stopRules":[123]}`)), `acceptance\.stopRules\[0\] must be a string`)
		polMatches(t, joinLines(j(`{"surprise":true}`)), `acceptance\.surprise is not supported`)
	})
	tw(t, "acceptance", "validates explicit reviewed acceptance at every execution nesting level", func(t *testing.T) {
		rev := js(t, `{"level":"reviewed"}`)
		errors := validateExecutionAcceptance(executionAcceptanceInput{
			Acceptance: "reviewed",
			Tasks:      []executionAcceptanceInput{{Acceptance: rev}},
			Chain: []executionAcceptanceStep{
				{executionAcceptanceInput: executionAcceptanceInput{Acceptance: "reviewed"}},
				{Parallel: []executionAcceptanceInput{{Acceptance: rev}}},
				{ParallelOne: &executionAcceptanceInput{Acceptance: "reviewed"}},
			},
		})
		if len(errors) != 5 {
			t.Fatalf("got %d errors: %v", len(errors), errors)
		}
		for i, re := range []string{`^acceptance is an achieved status`, `^tasks\[0\]\.acceptance\.level is an achieved status`, `^chain\[0\]\.acceptance is an achieved status`, `^chain\[1\]\.parallel\[0\]\.acceptance\.level is an achieved status`, `^chain\[2\]\.parallel\.acceptance is an achieved status`} {
			polMatches(t, errors[i], re)
		}
		polMatches(t, joinLines(errors), `acceptance\.review\.required`)
	})
	tw(t, "acceptance", "rejects transport-permitted true acceptance before execution, including nested inputs", func(t *testing.T) {
		errors := validateExecutionAcceptance(executionAcceptanceInput{
			Acceptance: true,
			Tasks:      []executionAcceptanceInput{{Acceptance: true}},
			Chain: []executionAcceptanceStep{
				{executionAcceptanceInput: executionAcceptanceInput{Acceptance: true}},
				{Parallel: []executionAcceptanceInput{{Acceptance: true}}},
				{ParallelOne: &executionAcceptanceInput{Acceptance: true}},
			},
		})
		paths := []string{"acceptance", "tasks[0].acceptance", "chain[0].acceptance", "chain[1].parallel[0].acceptance", "chain[2].parallel.acceptance"}
		if len(errors) != len(paths) {
			t.Fatalf("got %v", errors)
		}
		for i, p := range paths {
			want := p + " must be a string level, false, or an object."
			if len(errors[i]) < len(want) || errors[i][:len(want)] != want {
				t.Errorf("errors[%d] = %q", i, errors[i])
			}
		}
	})
	tw(t, "acceptance", "requires outputSchema for explicit structured acceptance report mode", func(t *testing.T) {
		schema := js(t, `{"type":"object"}`)
		on := func() any { return js(t, `{"level":"checked","report":"on"}`) }
		off := func() any { return js(t, `{"level":"checked","report":"off"}`) }
		errors := validateExecutionAcceptance(executionAcceptanceInput{
			Acceptance: on(),
			Tasks: []executionAcceptanceInput{
				{Acceptance: off()},
				{OutputSchema: schema, Acceptance: on()},
				{OutputSchema: false, Acceptance: on()},
			},
			Chain: []executionAcceptanceStep{
				{executionAcceptanceInput: executionAcceptanceInput{Acceptance: on()}},
				{executionAcceptanceInput: executionAcceptanceInput{OutputSchema: schema, Acceptance: off()}},
				{Parallel: []executionAcceptanceInput{{Acceptance: on()}, {OutputSchema: schema, Acceptance: on()}}},
				{ParallelOne: &executionAcceptanceInput{Acceptance: off()}},
			},
		})
		equal(t, errors, []string{
			"acceptance.report requires outputSchema.",
			"tasks[0].acceptance.report requires outputSchema.",
			"tasks[2].acceptance.report requires outputSchema.",
			"chain[0].acceptance.report requires outputSchema.",
			"chain[2].parallel[0].acceptance.report requires outputSchema.",
			"chain[3].parallel.acceptance.report requires outputSchema.",
		}, "report mode errors")
	})
	tw(t, "acceptance", "resolves structured acceptance report capture without changing the omitted default", func(t *testing.T) {
		for _, c := range []struct {
			in   any
			want string
		}{{nil, "optional"}, {"checked", "optional"}, {false, "off"}, {js(t, `{"report":"off"}`), "off"}, {js(t, `{"report":"on"}`), "required"}} {
			got, err := resolveAcceptanceReportMode(c.in)
			if err != nil || got != c.want {
				t.Errorf("%v: %q %v, want %q", c.in, got, err, c.want)
			}
		}
	})
}
