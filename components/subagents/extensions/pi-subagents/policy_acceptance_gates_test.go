package pi_subagents

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// ---- helpers (the original's reportData, report, tempRepo, tempGitRepo) -------------------------------------------------

// reportData is the original's base report with overrides (undef{} removes a field; a new key goes last).
func reportData(t *testing.T, overrides map[string]any) *jsObject {
	t.Helper()
	base := js(t, `{"criteriaSatisfied":[{"id":"criterion-1","status":"satisfied","evidence":"verified in test"}],"changedFiles":["src/file.ts"],"testsAddedOrUpdated":["test/file.test.ts"],"commandsRun":[{"command":"npm test","result":"passed","summary":"passed"}],"validationOutput":["tests passed"],"residualRisks":[],"noStagedFiles":true,"notes":"complete"}`).(*jsObject)
	out := newObject()
	for _, k := range base.order() {
		out.set(k, base.vals[k])
	}
	for k, v := range overrides {
		out.set(k, v)
	}
	clean := newObject()
	for _, k := range out.keys {
		if _, gone := out.vals[k].(undef); !gone {
			clean.set(k, out.vals[k])
		}
	}
	return clean
}

func reportText(t *testing.T, overrides map[string]any, fence string) string {
	t.Helper()
	if fence == "" {
		fence = "acceptance-report"
	}
	return strings.Join([]string{"done", "```" + fence, marshalJSON(reportData(t, overrides), ""), "```"}, "\n")
}

func report(t *testing.T, overrides map[string]any) string { return reportText(t, overrides, "") }

func tempRepoDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "file.txt"), "hello\n")
	return dir
}

func tempGitRepoDir(t *testing.T) string {
	t.Helper()
	dir := tempRepoDir(t)
	gitIn(t, dir, "init")
	gitIn(t, dir, "config", "user.email", "test@example.com")
	gitIn(t, dir, "config", "user.name", "Test User")
	gitIn(t, dir, "config", "commit.gpgsign", "false")
	gitIn(t, dir, "add", "file.txt")
	gitIn(t, dir, "commit", "-m", "base")
	return dir
}

func resolve(t *testing.T, run acceptanceRun) *resolvedAcceptance {
	t.Helper()
	r, err := resolveEffectiveAcceptance(run)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func explicitJSON(t *testing.T, s string) any {
	t.Helper()
	return js(t, s)
}

func ledgerOf(a *resolvedAcceptance, output, cwd string) *acceptanceLedger {
	return evaluateAcceptance(acceptanceEval{Acceptance: a, Output: output, Cwd: cwd})
}

func findCheck(l *acceptanceLedger, id string) *runtimeCheck {
	for i := range l.RuntimeChecks {
		if l.RuntimeChecks[i].ID == id {
			return &l.RuntimeChecks[i]
		}
	}
	return nil
}

func checkStatus(l *acceptanceLedger, id string) string {
	if c := findCheck(l, id); c != nil {
		return c.Status
	}
	return "<none>"
}

func reviewRequired(a *resolvedAcceptance) any {
	if a.Review != nil && a.Review.Required != nil {
		return *a.Review.Required
	}
	return nil
}

func skipWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the verify commands of this twin are POSIX shell commands")
	}
}

// ---- tests ----------------------------------------------------------------------------------------------------------------

func TestAcceptanceGatesResolve(t *testing.T) {
	tw(t, "acceptance", "applies checked acceptance to declared builtin writer profiles", func(t *testing.T) {
		builtins := DiscoverAgentsAll(tempRepoDir(t)).Builtin
		for _, name := range []string{"worker", "claude-code-writer", "codex-exec-writer", "cursor-agent-writer"} {
			a := findAgent(builtins, name)
			if a == nil {
				t.Fatalf("no builtin %s", name)
			}
			equal(t, a.AcceptanceRole, "writer", name)
		}
		equal(t, findAgent(builtins, "delegate").AcceptanceRole, "", "delegate")
		equal(t, resolve(t, acceptanceRun{AgentName: "worker", AcceptanceRole: findAgent(builtins, "worker").AcceptanceRole}).Level, "checked", "worker level")
	})
	tw(t, "acceptance", "infers evidence levels and review requirements from declared roles", func(t *testing.T) {
		equal(t, resolve(t, acceptanceRun{AgentName: "worker", AcceptanceRole: "read-only", Task: "Implement the fix", Mode: "single"}).Level, "none", "read-only")
		equal(t, resolve(t, acceptanceRun{AgentName: "reviewer", AcceptanceRole: "writer", Task: "Review-only. Do not edit.", Mode: "single"}).Level, "checked", "writer")
		for _, r := range []*resolvedAcceptance{
			resolve(t, acceptanceRun{AgentName: "worker", AcceptanceRole: "writer", Task: "Implement the fix", Mode: "single", Async: true}),
			resolve(t, acceptanceRun{AgentName: "worker", AcceptanceRole: "writer", Task: "Fix each item", Mode: "chain", Dynamic: true}),
		} {
			equal(t, r.Level, "checked", "level")
			equal(t, reviewRequired(r), any(true), "review required")
		}
	})
	tw(t, "acceptance", "task prose and agent names cannot change inferred acceptance", func(t *testing.T) {
		for _, task := range []string{"Review only; do not edit files.", "Implement the destructive security migration and commit it.", "Release fix pass with data-loss risk."} {
			for _, name := range []string{"worker", "reviewer", "oracle", "release-analyst"} {
				equal(t, resolve(t, acceptanceRun{AgentName: name, Task: task, Async: true, Dynamic: true}).Level, "attested", name+": "+task)
				equal(t, resolve(t, acceptanceRun{AgentName: name, AcceptanceRole: "read-only", Task: task, Async: true, Dynamic: true}).Level, "none", name+": "+task)
				w := resolve(t, acceptanceRun{AgentName: name, AcceptanceRole: "writer", Task: task, Async: true, Dynamic: true})
				equal(t, w.Level, "checked", name+": "+task)
				equal(t, reviewRequired(w), any(true), name+": "+task)
			}
		}
	})
	tw(t, "acceptance", "explicit acceptance can strengthen inferred policy", func(t *testing.T) {
		r := resolve(t, acceptanceRun{AgentName: "reviewer", Task: "Review-only.", Explicit: explicitJSON(t, `{"level":"verified","verify":[{"id":"ok","command":"node --version"}]}`)})
		equal(t, r.Level, "verified", "level")
		equal(t, r.Verify[0].ID, "ok", "verify id")
	})
	tw(t, "acceptance", "agent contract disables inferred acceptance without changing current defaults", func(t *testing.T) {
		current := resolve(t, acceptanceRun{AgentName: "worker", AcceptanceRole: "writer", Task: "Implement the fix", Mode: "single", Async: true})
		equal(t, current.Level, "checked", "current level")
		equal(t, reviewRequired(current), any(true), "current review")
		equal(t, current.InferredReason, []string{"async declared writer"}, "reason")
		for _, explicit := range []any{nil, "auto", false} {
			r := resolve(t, acceptanceRun{AgentName: "worker", AcceptanceRole: "writer", Task: "Implement the fix", Mode: "single", Async: true, Explicit: explicit, AgentContract: true})
			equal(t, r.Level, "none", "level")
			equal(t, r.InferredReason, []string{}, "reason")
			equal(t, r.Explicit, explicit != nil, "explicit")
		}
	})
	tw(t, "acceptance", "agent contract keeps explicit acceptance report-optional and verify-only", func(t *testing.T) {
		skipWindows(t)
		cwd := t.TempDir()
		checked := resolve(t, acceptanceRun{AgentName: "worker", Task: "Implement the fix", Explicit: "checked", AgentContract: true})
		equal(t, formatAcceptancePrompt(checked, acceptancePromptOptions{ReportOptional: true}), "", "prompt")
		l := evaluateAcceptance(acceptanceEval{Acceptance: checked, Output: "done", Cwd: cwd, ReportOptional: true})
		equal(t, l.Status, "checked", "status")
		equal(t, acceptanceFailureMessage(l), "", "failure")
		verified := resolve(t, acceptanceRun{AgentName: "worker", Task: "Implement the fix", Explicit: explicitJSON(t, `{"level":"verified","verify":[{"id":"ok","command":"exit 0"}]}`), AgentContract: true})
		equal(t, formatAcceptancePrompt(verified, acceptancePromptOptions{ReportOptional: true}), "", "verified prompt")
		vl := evaluateAcceptance(acceptanceEval{Acceptance: verified, Output: "done", Cwd: cwd, ReportOptional: true})
		equal(t, vl.Status, "verified", "verified status")
		equal(t, vl.VerifyRuns[0].Status, "passed", "verify run")
	})
	tw(t, "acceptance", "current/default acceptance still rejects missing required reports", func(t *testing.T) {
		current := resolve(t, acceptanceRun{AgentName: "worker", Task: "Implement the fix", Explicit: "checked"})
		l := ledgerOf(current, "done", t.TempDir())
		equal(t, l.Status, "rejected", "status")
		polMatches(t, acceptanceFailureMessage(l), `Structured acceptance report not found`)
	})
	tw(t, "acceptance", "formats a standardized child prompt section", func(t *testing.T) {
		r := resolve(t, acceptanceRun{AgentName: "worker", Task: "Implement a fix", Explicit: explicitJSON(t, `{"level":"checked","criteria":["Patch the bug"],"stopRules":["Do not stop after analysis"]}`)})
		p := formatAcceptancePrompt(r, acceptancePromptOptions{})
		polMatches(t, p, `## Acceptance Contract`)
		polMatches(t, p, `Acceptance level: checked`)
		polMatches(t, p, `Patch the bug`)
		polMatches(t, p, "```acceptance-report")
		polMatches(t, p, `array fields contain strings`)
		polMatches(t, p, `empty-string entr(?:y|ies).*\[\s*""\s*\]`, "i")
		polMatches(t, p, `criteriaSatisfied\[\]\.status.*satisfied, not-satisfied, not-applicable`)
		polMatches(t, p, `commandsRun\[\]\.result.*passed, failed, not-run`)
		polMatches(t, p, `manualNotes.*optional strings.*empty string.*does not satisfy.*manual-notes`)
		polMatches(t, p, `"reviewFindings": \[\n    "blocker:`)
	})
	tw(t, "acceptance", "labels declared review gates as optional when required is false", func(t *testing.T) {
		r := resolve(t, acceptanceRun{AgentName: "worker", Task: "Implement a fix", Explicit: explicitJSON(t, `{"level":"checked","review":{"agent":"reviewer","required":false}}`)})
		polMatches(t, formatAcceptancePrompt(r, acceptancePromptOptions{}), `Review gate: optional by reviewer\.`)
	})
	tw(t, "acceptance", "omits inferred read-only prompts while preserving explicit acceptance", func(t *testing.T) {
		inferred := resolve(t, acceptanceRun{AgentName: "reviewer", AcceptanceRole: "read-only", Task: "Review the diff and return findings only."})
		equal(t, inferred.Level, "none", "level")
		equal(t, formatAcceptancePrompt(inferred, acceptancePromptOptions{}), "", "prompt")
		explicit := resolve(t, acceptanceRun{AgentName: "reviewer", AcceptanceRole: "read-only", Task: "Review the diff and return findings only.", Explicit: explicitJSON(t, `{"level":"checked","criteria":["Return the review findings"]}`)})
		polMatches(t, formatAcceptancePrompt(explicit, acceptancePromptOptions{}), "```acceptance-report")
		crit := resolve(t, acceptanceRun{AgentName: "reviewer", AcceptanceRole: "read-only", Task: "Review the diff and return findings only.", Explicit: explicitJSON(t, `{"criteria":["Return the review findings"]}`)})
		equal(t, crit.Level, "attested", "criteria level")
		polMatches(t, formatAcceptancePrompt(crit, acceptancePromptOptions{}), "```acceptance-report")
	})
	tw(t, "acceptance", "includes every required resolved criterion in report examples", func(t *testing.T) {
		ids := func(prompt string) []string {
			m := regexp.MustCompile("(?s)```acceptance-report\n(.*?)\n```").FindStringSubmatch(prompt)
			if m == nil {
				t.Fatal("no example")
			}
			var got []string
			for _, c := range anyList(js(t, m[1]).(*jsObject).vals["criteriaSatisfied"]) {
				got = append(got, c.(*jsObject).vals["id"].(string))
			}
			return got
		}
		inferred := resolve(t, acceptanceRun{AgentName: "worker", AcceptanceRole: "writer", Task: "Implement the fix", Mode: "single", Async: true})
		equal(t, ids(formatAcceptancePrompt(inferred, acceptancePromptOptions{})), []string{"criterion-1", "criterion-2"}, "inferred")
		custom := resolve(t, acceptanceRun{AgentName: "worker", Task: "Implement the fix", Explicit: explicitJSON(t, `{"level":"checked","criteria":[{"id":"required-check","must":"Required","severity":"required"},{"id":"recommended-check","must":"Recommended","severity":"recommended"}]}`)})
		equal(t, ids(formatAcceptancePrompt(custom, acceptancePromptOptions{})), []string{"required-check"}, "custom")
	})
}

func TestAcceptanceReportParsing(t *testing.T) {
	undefined := undef{}
	tw(t, "acceptance", "parses acceptance-report fences and ignores unrelated json fences", func(t *testing.T) {
		parsed := parseAcceptanceReport(report(t, nil))
		if parsed.Report == nil {
			t.Fatal(parsed.Error)
		}
		equal(t, marshalJSON(parsed.Report.vals["changedFiles"], ""), `["src/file.ts"]`, "changedFiles")
		equal(t, parsed.Error, "", "error")
		fence := func(body string) string { return "done\n\n```json\n" + body + "\n```" }
		generic := parseAcceptanceReport(fence(`{"notes":"not an acceptance report"}`))
		equal(t, generic.Report == nil, true, "generic")
		polMatches(t, generic.Error, `Structured acceptance report not found`)
		criteriaOnly := parseAcceptanceReport(fence(`{"criteriaSatisfied":[{"id":"criterion-1","status":"satisfied","evidence":"example"}]}`))
		equal(t, criteriaOnly.Report == nil, true, "criteria only")
		polMatches(t, criteriaOnly.Error, `Structured acceptance report not found`)
		unknown := parseAcceptanceReport(fence(`{"criteriaSatisfied":[],"unexpected":true}`))
		equal(t, unknown.Report == nil, true, "unknown")
		polMatches(t, unknown.Error, `unexpected: unsupported acceptance report field`)
		invalidSignal := fence(`{"criteriaSatisfied":[{"id":"criterion-1","status":"satisfied","evidence":"example"}],"changedFiles":false}`)
		inv := parseAcceptanceReport(invalidSignal)
		equal(t, inv.Report == nil, true, "invalid signal")
		polMatches(t, inv.Error, `changedFiles: expected string\[\]; got boolean false`)
		equal(t, stripAcceptanceReport(invalidSignal), invalidSignal, "strip invalid signal")
		partial := fence(`{"acceptance":{"changedFiles":["src/file.ts"]}}`)
		pw := parseAcceptanceReport(partial)
		equal(t, pw.Report == nil, true, "partial wrapper")
		polMatches(t, pw.Error, `Structured acceptance report not found`)
		equal(t, stripAcceptanceReport(partial), partial, "strip partial")
		shaped := fence(`{"changedFiles":["src/file.ts"]}`)
		rs := parseAcceptanceReport(shaped)
		equal(t, rs.Report == nil, true, "report shaped")
		polMatches(t, rs.Error, `Structured acceptance report not found`)
		equal(t, stripAcceptanceReport(shaped), shaped, "strip shaped")
		for _, bad := range []string{"```acceptance-report\n{bad-json\n```", "```acceptance-report\n```", "```acceptance-report\n{\"changedFiles\": false}", "ACCEPTANCE_REPORT: { not json", "ACCEPTANCE_REPORT: no object"} {
			m := parseAcceptanceReport(bad)
			equal(t, m.Report == nil, true, bad)
			polMatches(t, m.Error, `Failed to parse acceptance-report`)
		}
	})
	tw(t, "acceptance", "recovers a valid report from an unterminated explicit fence only", func(t *testing.T) {
		rec := parseAcceptanceReport("done\n```acceptance-report\n" + marshalJSON(reportData(t, nil), ""))
		if rec.Report == nil {
			t.Fatal(rec.Error)
		}
		equal(t, marshalJSON(rec.Report.vals["changedFiles"], ""), `["src/file.ts"]`, "changedFiles")
		equal(t, rec.Error, "", "error")
		for _, invalid := range []string{
			"```acceptance-report\n{ not json",
			"```acceptance-report\n" + marshalJSON(reportData(t, nil), "") + "\ntrailing prose",
			"```acceptance-report\n" + `{"changedFiles":false}`,
		} {
			p := parseAcceptanceReport(invalid)
			equal(t, p.Report == nil, true, invalid)
			polMatches(t, p.Error, `Failed to parse acceptance-report`)
		}
	})
	tw(t, "acceptance", "parses acceptance reports from json-family fences", func(t *testing.T) {
		for _, fence := range []string{"json", "jsonc", "json5"} {
			out := reportText(t, nil, fence)
			p := parseAcceptanceReport(out)
			if p.Report == nil {
				t.Fatalf("%s: %s", fence, p.Error)
			}
			equal(t, marshalJSON(p.Report.vals["changedFiles"], ""), `["src/file.ts"]`, fence)
			equal(t, stripAcceptanceReport(out), "done", fence+" strip")
		}
	})
	tw(t, "acceptance", "strips trailing json-family reports after earlier unrelated json fences", func(t *testing.T) {
		out := strings.Join([]string{"metadata", "```json", `{"notes":"not an acceptance report"}`, "```", "done", "```json", marshalJSON(reportData(t, nil), ""), "```"}, "\n")
		if parseAcceptanceReport(out).Report == nil {
			t.Fatal("no report")
		}
		equal(t, stripAcceptanceReport(out), strings.Join([]string{"metadata", "```json", `{"notes":"not an acceptance report"}`, "```", "done"}, "\n"), "stripped")
	})
	tw(t, "acceptance", "unwraps every accepted report wrapper", func(t *testing.T) {
		for _, w := range []string{"acceptance", "acceptance-report", "acceptance_report", "acceptanceReport"} {
			wrapped := newObject()
			wrapped.set(w, reportData(t, nil))
			out := strings.Join([]string{"done", "```json", marshalJSON(wrapped, ""), "```"}, "\n")
			p := parseAcceptanceReport(out)
			if p.Report == nil {
				t.Fatalf("%s: %s", w, p.Error)
			}
			equal(t, marshalJSON(p.Report.vals["testsAddedOrUpdated"], ""), `["test/file.test.ts"]`, w)
			equal(t, stripAcceptanceReport(out), "done", w+" strip")
		}
	})
	tw(t, "acceptance", "normalizes known report wire variants to the canonical shape", func(t *testing.T) {
		wire := js(t, `{"acceptance_report":{"criteria_satisfied":{"id":"Criterion_1","status":"DONE","evidence":"verified"},"changed_files":"src/file.ts","tests_added_or_updated":"test/file.test.ts","commands_run":{"command":"npm test","result":"success","summary":"passed"},"validation_output":"tests passed","residual_risks":"none","no_staged_files":" TRUE ","diff_summary":"patched","review_findings":"no blockers","manual_notes":"complete"}}`)
		out := strings.Join([]string{"done", "```acceptance_report", marshalJSON(wire, ""), "```"}, "\n")
		p := parseAcceptanceReport(out)
		equal(t, p.Error, "", "error")
		equal(t, marshalJSON(p.Report, ""), `{"criteriaSatisfied":[{"id":"criterion-1","status":"satisfied","evidence":"verified"}],"changedFiles":["src/file.ts"],"testsAddedOrUpdated":["test/file.test.ts"],"commandsRun":[{"command":"npm test","result":"passed","summary":"passed"}],"validationOutput":["tests passed"],"residualRisks":["none"],"noStagedFiles":true,"diffSummary":"patched","reviewFindings":["no blockers"],"manualNotes":"complete"}`, "report")
		equal(t, stripAcceptanceReport(out), "done", "strip")
	})
	tw(t, "acceptance", "normalizes only known enum synonyms and criterion id separators", func(t *testing.T) {
		for _, c := range [][2]string{{"met", "satisfied"}, {"not met", "not-satisfied"}, {"not_applicable", "not-applicable"}} {
			p := parseAcceptanceReport(report(t, map[string]any{"criteriaSatisfied": []any{js(t, `{"id":"Criterion 1","status":"`+c[0]+`","evidence":"proof"}`)}}))
			first := anyList(p.Report.vals["criteriaSatisfied"])[0].(*jsObject)
			equal(t, first.vals["id"], "criterion-1", "id")
			equal(t, first.vals["status"], c[1], c[0])
		}
		for _, c := range [][2]string{{"ok", "passed"}, {"failure", "failed"}, {"not run", "not-run"}} {
			p := parseAcceptanceReport(report(t, map[string]any{"commandsRun": []any{js(t, `{"command":"check","result":"`+c[0]+`","summary":"complete"}`)}}))
			first := anyList(p.Report.vals["commandsRun"])[0].(*jsObject)
			equal(t, first.vals["result"], c[1], c[0])
		}
	})
	tw(t, "acceptance", "rejects unknown and ambiguous report fields with exact diagnostics", func(t *testing.T) {
		withExtra := func(k string, v any) *jsObject { o := reportData(t, nil); o.set(k, v); return o }
		wrap := func(w string, extra ...[2]any) *jsObject {
			o := newObject()
			o.set(w, reportData(t, nil))
			for _, e := range extra {
				o.set(e[0].(string), e[1])
			}
			return o
		}
		for _, c := range []struct {
			value *jsObject
			re    string
		}{
			{withExtra("unexpected", true), `unexpected: unsupported acceptance report field`},
			{withExtra("changed_files", []any{"src/other.ts"}), `changed_files: duplicates normalized field 'changedFiles'`},
			{wrap("acceptance", [2]any{"changedFiles", []any{"src/file.ts"}}), `changedFiles: unsupported alongside acceptance report wrapper 'acceptance'`},
			{wrap("acceptance", [2]any{"acceptance_report", reportData(t, nil)}), `multiple acceptance report wrappers are ambiguous`},
		} {
			p := parseAcceptanceReport("```acceptance-report\n" + marshalJSON(c.value, "") + "\n```")
			equal(t, p.Report == nil, true, c.re)
			polMatches(t, p.Error, c.re)
		}
	})
	tw(t, "acceptance", "rejects unknown enums, duplicate criterion ids, and blank evidence", func(t *testing.T) {
		for _, c := range []struct {
			overrides map[string]any
			re        string
		}{
			{map[string]any{"commandsRun": []any{js(t, `{"command":"npm test","result":"maybe","summary":"passed"}`)}}, `commandsRun\[0\]\.result.*got "maybe"`},
			{map[string]any{"commandsRun": []any{js(t, `{"command":"npm test","result":"passed","summary":"","exitCode":0}`)}}, `commandsRun\[0\]\.exitCode: unsupported acceptance command field`},
			{map[string]any{"commandsRun": []any{js(t, `{"command":"npm test","result":"passed","summary":""}`)}}, `commandsRun\[0\]\.summary: expected non-empty string; got ""`},
			{map[string]any{"criteriaSatisfied": []any{js(t, `{"id":"criterion-1","status":"maybe","evidence":"proof"}`)}}, `criteriaSatisfied\[0\]\.status.*got "maybe"`},
			{map[string]any{"criteriaSatisfied": []any{js(t, `{"id":"criterion-1","status":"satisfied","evidence":"proof","confidence":1}`)}}, `criteriaSatisfied\[0\]\.confidence: unsupported acceptance criterion field`},
			{map[string]any{"criteriaSatisfied": []any{js(t, `{"id":"criterion-1","status":"satisfied","evidence":"proof"}`), js(t, `{"id":"Criterion_1","status":"satisfied","evidence":"proof"}`)}}, `duplicate normalized criterion id 'criterion-1'`},
			{map[string]any{"reviewFindings": []any{js(t, `{}`)}}, `reviewFindings\[0\]: expected non-empty string; got object`},
			{map[string]any{"noStagedFiles": "yes"}, `noStagedFiles: expected boolean; got "yes"`},
		} {
			p := parseAcceptanceReport(report(t, c.overrides))
			equal(t, p.Report == nil, true, c.re)
			polMatches(t, p.Error, c.re)
		}
	})
	tw(t, "acceptance", "tolerates empty-string entries in string-array fields by dropping them at parse time", func(t *testing.T) {
		for _, field := range []string{"changedFiles", "testsAddedOrUpdated", "validationOutput", "residualRisks", "reviewFindings"} {
			p := parseAcceptanceReport(report(t, map[string]any{field: []any{""}}))
			equal(t, p.Error, "", field)
			equal(t, marshalJSON(p.Report.vals[field], ""), "[]", field+" dropped")
		}
		mixed := parseAcceptanceReport(report(t, map[string]any{"reviewFindings": []any{"blocker: file.ts:12", ""}}))
		equal(t, mixed.Error, "", "mixed")
		equal(t, marshalJSON(mixed.Report.vals["reviewFindings"], ""), `["blocker: file.ts:12"]`, "mixed list")
		ws := parseAcceptanceReport(report(t, map[string]any{"validationOutput": []any{"  "}}))
		equal(t, ws.Error, "", "whitespace")
		equal(t, marshalJSON(ws.Report.vals["validationOutput"], ""), "[]", "whitespace list")
		co := parseAcceptanceReport(report(t, map[string]any{"changedFiles": "src/file.ts"}))
		equal(t, co.Error, "", "coerced")
		equal(t, marshalJSON(co.Report.vals["changedFiles"], ""), `["src/file.ts"]`, "coerced list")
		inv := parseAcceptanceReport(report(t, map[string]any{"reviewFindings": []any{js(t, `{}`)}}))
		equal(t, inv.Report == nil, true, "garbage")
		polMatches(t, inv.Error, `reviewFindings\[0\]: expected non-empty string; got object`)
	})
	tw(t, "acceptance", "accepts empty optional note strings without treating them as evidence", func(t *testing.T) {
		for _, field := range []string{"manualNotes", "notes"} {
			p := parseAcceptanceReport(report(t, map[string]any{"notes": undefined, field: ""}))
			equal(t, p.Error, "", field)
			equal(t, p.Report.vals[field], "", field+" value")
		}
		a := resolve(t, acceptanceRun{AgentName: "worker", AcceptanceRole: "writer", Task: "Return a concise result", Explicit: explicitJSON(t, `{"level":"attested","evidence":["manual-notes"]}`)})
		l := ledgerOf(a, report(t, map[string]any{"notes": undefined, "manualNotes": ""}), t.TempDir())
		equal(t, l.ChildReportParseError, "", "parse error")
		equal(t, l.ChildReport.vals["manualNotes"], "", "manualNotes")
		equal(t, l.Status, "rejected", "status")
		equal(t, checkStatus(l, "evidence:manual-notes"), "failed", "manual-notes check")
	})
	tw(t, "acceptance", "reports field-level validation errors for malformed acceptance-report fields", func(t *testing.T) {
		p := parseAcceptanceReport(report(t, map[string]any{"reviewFindings": []any{js(t, `{"id":"B-1","severity":"blocker","finding":"Missing evidence"}`)}}))
		equal(t, p.Report == nil, true, "reviewer report")
		polMatches(t, p.Error, `reviewFindings\[0\]: expected non-empty string; got object`)
		c := parseAcceptanceReport(report(t, map[string]any{"commandsRun": []any{js(t, `{"command":"npm test","exitCode":0}`)}}))
		equal(t, c.Report == nil, true, "command report")
		polMatches(t, c.Error, `commandsRun\[0\]\.result: expected one of "passed", "failed", "not-run"; got missing`)
		polMatches(t, c.Error, `commandsRun\[0\]\.summary: expected non-empty string; got missing`)
		k := parseAcceptanceReport(report(t, map[string]any{"criteriaSatisfied": []any{js(t, `{"id":7,"status":"maybe","evidence":""}`)}}))
		equal(t, k.Report == nil, true, "criteria report")
		polMatches(t, k.Error, `criteriaSatisfied\[0\]\.id: expected string; got number 7`)
		polMatches(t, k.Error, `criteriaSatisfied\[0\]\.status: expected one of "satisfied", "not-satisfied", "not-applicable"; got "maybe"`)
		polMatches(t, k.Error, `criteriaSatisfied\[0\]\.evidence: expected non-empty string; got ""`)
	})
}

func TestAcceptanceEvaluation(t *testing.T) {
	undefined := undef{}
	checkedRun := func(t *testing.T, evidence ...string) *resolvedAcceptance {
		o := `{"level":"checked"}`
		if len(evidence) > 0 {
			o = `{"level":"checked","evidence":["` + strings.Join(evidence, `","`) + `"]}`
		}
		return resolve(t, acceptanceRun{AgentName: "worker", Task: "Implement a fix", Explicit: js(t, o)})
	}
	tw(t, "acceptance", "explicit none disables inferred gates when a reason is present", func(t *testing.T) {
		a := resolve(t, acceptanceRun{AgentName: "worker", Task: "Implement a fix", Explicit: js(t, `{"level":"none","reason":"parent is doing manual acceptance"}`)})
		equal(t, a.Level, "none", "level")
		equal(t, a.Evidence, []string{}, "evidence")
	})
	tw(t, "acceptance", "checked mode accepts explicit empty changed and test arrays as not applicable", func(t *testing.T) {
		l := ledgerOf(checkedRun(t), report(t, map[string]any{"changedFiles": []any{}, "testsAddedOrUpdated": []any{}}), tempRepoDir(t))
		equal(t, l.Status, "checked", "status")
		equal(t, checkStatus(l, "evidence:changed-files"), "not-applicable", "changed")
		equal(t, checkStatus(l, "evidence:tests-added"), "not-applicable", "tests")
	})
	reviewer := func(t *testing.T) *resolvedAcceptance {
		return resolve(t, acceptanceRun{AgentName: "reviewer", Task: "Review-only. Do not edit.", Explicit: js(t, `{"level":"checked","evidence":["review-findings","commands-run","validation-output","residual-risks"]}`)})
	}
	tw(t, "acceptance", "accepts read-only reviewer reports that omit child no-staged-files evidence when the worktree is clean", func(t *testing.T) {
		l := ledgerOf(reviewer(t), report(t, map[string]any{"changedFiles": []any{}, "testsAddedOrUpdated": []any{}, "validationOutput": []any{"Read-only review completed."}, "reviewFindings": []any{}, "noStagedFiles": undefined}), tempGitRepoDir(t))
		equal(t, l.Status, "checked", "status")
		equal(t, checkStatus(l, "evidence:no-staged-files"), "<none>", "evidence check")
		equal(t, checkStatus(l, "no-staged-files"), "passed", "git check")
	})
	tw(t, "acceptance", "still rejects aggregate no-staged-files evidence when a child worktree reports staged files", func(t *testing.T) {
		l := ledgerOf(reviewer(t), report(t, map[string]any{"changedFiles": []any{}, "testsAddedOrUpdated": []any{}, "validationOutput": []any{"Dynamic fanout review completed."}, "reviewFindings": []any{}, "noStagedFiles": false}), tempGitRepoDir(t))
		equal(t, l.Status, "rejected", "status")
		equal(t, checkStatus(l, "evidence:no-staged-files"), "failed", "evidence check")
		equal(t, checkStatus(l, "no-staged-files"), "passed", "git check")
	})
	tw(t, "acceptance", "still rejects omitted child no-staged-files evidence when the parent git check finds staged files", func(t *testing.T) {
		cwd := tempGitRepoDir(t)
		writeFile(t, filepath.Join(cwd, "staged.txt"), "staged\n")
		gitIn(t, cwd, "add", "staged.txt")
		l := ledgerOf(reviewer(t), report(t, map[string]any{"changedFiles": []any{}, "testsAddedOrUpdated": []any{}, "validationOutput": []any{"Read-only review completed."}, "reviewFindings": []any{}, "noStagedFiles": undefined}), cwd)
		equal(t, l.Status, "rejected", "status")
		equal(t, checkStatus(l, "evidence:no-staged-files"), "<none>", "evidence check")
		equal(t, checkStatus(l, "no-staged-files"), "failed", "git check")
		polMatches(t, acceptanceFailureMessage(l), `Staged files present`)
	})
	tw(t, "acceptance", "accepts an explicitly preserved staged index when only working-tree content changes", func(t *testing.T) {
		cwd := tempGitRepoDir(t)
		writeFile(t, filepath.Join(cwd, "owned.txt"), "parent staged\n")
		gitIn(t, cwd, "add", "owned.txt")
		nested := filepath.Join(cwd, "nested")
		os.Mkdir(nested, 0o755)
		baseline, err := captureStagedIndexBaseline(nested)
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(cwd, "working-only.txt"), "child fix\n")
		a := resolve(t, acceptanceRun{AgentName: "worker", Task: "Implement a fix", Explicit: js(t, `{"level":"checked","preserveStagedIndex":true}`)})
		l := evaluateAcceptance(acceptanceEval{Acceptance: a, Output: report(t, map[string]any{"noStagedFiles": false}), Cwd: nested, StagedIndexBaseline: baseline})
		equal(t, l.Status, "checked", "status")
		equal(t, checkStatus(l, "evidence:no-staged-files"), "<none>", "evidence check")
		equal(t, checkStatus(l, "staged-index-unchanged"), "passed", "index check")
	})
	tw(t, "acceptance", "rejects a changed or unavailable staged-index baseline", func(t *testing.T) {
		cwd := tempGitRepoDir(t)
		writeFile(t, filepath.Join(cwd, "owned.txt"), "parent staged\n")
		gitIn(t, cwd, "add", "owned.txt")
		baseline, _ := captureStagedIndexBaseline(cwd)
		a := resolve(t, acceptanceRun{AgentName: "worker", Task: "Implement a fix", Explicit: js(t, `{"level":"checked","preserveStagedIndex":true}`)})
		writeFile(t, filepath.Join(cwd, "child-staged.txt"), "unexpected\n")
		gitIn(t, cwd, "add", "child-staged.txt")
		changed := evaluateAcceptance(acceptanceEval{Acceptance: a, Output: report(t, map[string]any{"noStagedFiles": false}), Cwd: cwd, StagedIndexBaseline: baseline})
		unavailable := evaluateAcceptance(acceptanceEval{Acceptance: a, Output: report(t, map[string]any{"noStagedFiles": false}), Cwd: cwd})
		equal(t, changed.Status, "rejected", "changed")
		equal(t, checkStatus(changed, "staged-index-unchanged"), "failed", "changed check")
		equal(t, unavailable.Status, "rejected", "unavailable")
		polMatches(t, acceptanceFailureMessage(unavailable), `baseline is unavailable`)
	})
	tw(t, "acceptance", "fails closed when a staged-index baseline cannot be captured", func(t *testing.T) {
		_, err := captureStagedIndexBaseline(tempRepoDir(t))
		if err == nil {
			t.Fatal("captured in a plain directory")
		}
		polMatches(t, err.Error(), `Unable to capture staged index baseline`)
	})
	tw(t, "acceptance", "captures staged-index baselines in SHA-256 repositories", func(t *testing.T) {
		cwd := t.TempDir()
		if _, _, err := plainGit(cwd, "init", "--object-format=sha256", "-q"); err != nil {
			t.Skip("this git cannot create sha256 repositories")
		}
		writeFile(t, filepath.Join(cwd, "staged.txt"), "content\n")
		gitIn(t, cwd, "add", "staged.txt")
		b, err := captureStagedIndexBaseline(cwd)
		if err != nil {
			t.Fatal(err)
		}
		polMatches(t, b, `^[0-9a-f]{64}$`)
	})
	tw(t, "acceptance", "still rejects missing changed and test evidence and empty required commands", func(t *testing.T) {
		cwd := tempRepoDir(t)
		a := checkedRun(t)
		missing := ledgerOf(a, report(t, map[string]any{"changedFiles": undefined, "testsAddedOrUpdated": undefined}), cwd)
		equal(t, missing.Status, "rejected", "missing")
		polMatches(t, acceptanceFailureMessage(missing), `changed-files evidence missing`)
		missingTests := ledgerOf(a, report(t, map[string]any{"changedFiles": []any{}, "testsAddedOrUpdated": undefined}), cwd)
		equal(t, missingTests.Status, "rejected", "missing tests")
		polMatches(t, acceptanceFailureMessage(missingTests), `tests-added evidence missing`)
		empty := ledgerOf(a, report(t, map[string]any{"changedFiles": []any{}, "testsAddedOrUpdated": []any{}, "commandsRun": []any{}}), cwd)
		equal(t, empty.Status, "rejected", "empty commands")
		polMatches(t, acceptanceFailureMessage(empty), `commands-run evidence missing`)
	})
	tw(t, "acceptance", "surfaces parse validation details in acceptance failure messages", func(t *testing.T) {
		a := resolve(t, acceptanceRun{AgentName: "reviewer", Task: "Review-only. Do not edit.", Explicit: js(t, `{"level":"attested","evidence":["review-findings"]}`)})
		l := ledgerOf(a, report(t, map[string]any{"reviewFindings": []any{js(t, `{"id":"B-1","finding":"Missing evidence"}`)}}), t.TempDir())
		equal(t, l.Status, "rejected", "status")
		polMatches(t, acceptanceFailureMessage(l), `Failed to parse acceptance-report`)
		polMatches(t, acceptanceFailureMessage(l), `reviewFindings\[0\]: expected non-empty string; got object`)
	})
	tw(t, "acceptance", "normalizes configured criterion ids and validates direct report objects", func(t *testing.T) {
		cwd := tempRepoDir(t)
		a := resolve(t, acceptanceRun{AgentName: "worker", Task: "Implement a fix", Explicit: js(t, `{"level":"checked","criteria":[{"id":"Release_Check","must":"Release check passes"}]}`)})
		n := ledgerOf(a, report(t, map[string]any{"criteriaSatisfied": []any{js(t, `{"id":"release check","status":"met","evidence":"verified"}`)}, "changedFiles": []any{}, "testsAddedOrUpdated": []any{}}), cwd)
		equal(t, n.Status, "checked", "normalized")
		equal(t, anyList(n.ChildReport.vals["criteriaSatisfied"])[0].(*jsObject).vals["id"], "release-check", "id")
		direct := reportData(t, nil)
		direct.set("unexpected", true)
		bad := evaluateAcceptance(acceptanceEval{Acceptance: a, Output: "", Report: direct, HasReport: true, Cwd: cwd})
		equal(t, bad.Status, "rejected", "malformed direct")
		polMatches(t, bad.ChildReportParseError, `unexpected: unsupported acceptance report field`)
		null := evaluateAcceptance(acceptanceEval{Acceptance: a, Output: report(t, map[string]any{"changedFiles": []any{}, "testsAddedOrUpdated": []any{}}), Report: nil, HasReport: true, Cwd: cwd})
		equal(t, null.Status, "rejected", "null direct")
		polMatches(t, null.ChildReportParseError, `acceptance-report: expected object; got null`)
	})
	tw(t, "acceptance", "checked mode rejects not-satisfied required criteria", func(t *testing.T) {
		a := resolve(t, acceptanceRun{AgentName: "worker", Task: "Implement a fix", Explicit: js(t, `{"level":"checked","criteria":[{"id":"regression","must":"Regression is covered"}]}`)})
		l := ledgerOf(a, report(t, map[string]any{"criteriaSatisfied": []any{js(t, `{"id":"regression","status":"not-satisfied","evidence":"test missing"}`)}}), t.TempDir())
		equal(t, l.Status, "rejected", "status")
		polMatches(t, acceptanceFailureMessage(l), `Required criterion 'regression' was reported as not-satisfied`)
	})
	tw(t, "acceptance", "verified mode records runtime command success and failure separately from child command claims", func(t *testing.T) {
		skipWindows(t)
		cwd := tempRepoDir(t)
		passing := resolve(t, acceptanceRun{AgentName: "worker", Task: "Implement a fix", Explicit: js(t, `{"level":"verified","verify":[{"id":"pass","command":"exit 0","timeoutMs":10000}]}`)})
		pl := ledgerOf(passing, report(t, nil), cwd)
		equal(t, pl.Status, "verified", "pass status")
		equal(t, pl.VerifyRuns[0].Status, "passed", "pass run")
		failing := resolve(t, acceptanceRun{AgentName: "worker", Task: "Implement a fix", Explicit: js(t, `{"level":"verified","verify":[{"id":"fail","command":"exit 7","timeoutMs":10000}]}`)})
		fl := ledgerOf(failing, report(t, nil), cwd)
		equal(t, fl.Status, "rejected", "fail status")
		equal(t, anyList(fl.ChildReport.vals["commandsRun"])[0].(*jsObject).vals["result"], "passed", "child claim")
		equal(t, fl.VerifyRuns[0].Status, "failed", "fail run")
	})
	tw(t, "acceptance", "rejects when a successful verify command changes a preserved staged index", func(t *testing.T) {
		skipWindows(t)
		cwd := tempGitRepoDir(t)
		writeFile(t, filepath.Join(cwd, "owned.txt"), "parent staged\n")
		gitIn(t, cwd, "add", "owned.txt")
		baseline, _ := captureStagedIndexBaseline(cwd)
		writeFile(t, filepath.Join(cwd, "verify-staged.txt"), "verify mutation\n")
		a := resolve(t, acceptanceRun{AgentName: "worker", Task: "Implement a fix", Explicit: js(t, `{"level":"verified","preserveStagedIndex":true,"verify":[{"id":"stage","command":"git add verify-staged.txt"}]}`)})
		l := evaluateAcceptance(acceptanceEval{Acceptance: a, Output: report(t, map[string]any{"noStagedFiles": false}), Cwd: cwd, StagedIndexBaseline: baseline})
		equal(t, l.Status, "rejected", "status")
		equal(t, l.VerifyRuns[0].Status, "passed", "verify run")
		equal(t, checkStatus(l, "staged-index-unchanged"), "failed", "index check")
		now, _ := captureStagedIndexBaseline(cwd)
		equal(t, now != baseline, true, "index changed")
	})
	tw(t, "acceptance", "records achieved, blocked, and pending independent review separately from evidence", func(t *testing.T) {
		cwd := tempRepoDir(t)
		a := resolve(t, acceptanceRun{AgentName: "worker", Task: "Implement a risky fix", Explicit: js(t, `{"level":"checked","review":{"agent":"reviewer","required":true}}`)})
		rv := evaluateAcceptance(acceptanceEval{Acceptance: a, Output: report(t, nil), Cwd: cwd, ReviewResult: &reviewResult{Status: "reviewed", Findings: []reviewFinding{}}})
		equal(t, [3]string{rv.Status, rv.EvidenceStatus, rv.ReviewResult.Status}, [3]string{"reviewed", "checked", "reviewed"}, "reviewed")
		bl := evaluateAcceptance(acceptanceEval{Acceptance: a, Output: report(t, nil), Cwd: cwd, ReviewResult: &reviewResult{Status: "blockers", Findings: []reviewFinding{{Severity: "blocker", Issue: "Missing test", Rationale: "Acceptance requires test evidence."}}}})
		equal(t, [3]string{bl.Status, bl.EvidenceStatus, bl.ReviewResult.Status}, [3]string{"rejected", "checked", "blockers"}, "blockers")
		pending := ledgerOf(a, report(t, nil), cwd)
		equal(t, [3]string{pending.Status, pending.EvidenceStatus, pending.ReviewResult.Status}, [3]string{"review-required", "checked", "review-required"}, "pending")
	})
	tw(t, "acceptance", "keeps inferred review required when callers explicitly select checked or auto evidence", func(t *testing.T) {
		cwd := tempRepoDir(t)
		for _, explicit := range []any{js(t, `{"level":"checked"}`), "auto", js(t, `{"level":"auto"}`)} {
			a := resolve(t, acceptanceRun{AgentName: "worker", AcceptanceRole: "writer", Task: "Implement each dynamic item", Dynamic: true, Explicit: explicit})
			equal(t, a.Level, "checked", "level")
			equal(t, reviewRequired(a), any(true), "review")
			l := ledgerOf(a, report(t, map[string]any{"criteriaSatisfied": []any{js(t, `{"id":"criterion-1","status":"satisfied","evidence":"implemented"}`), js(t, `{"id":"criterion-2","status":"satisfied","evidence":"evidence returned"}`)}}), cwd)
			equal(t, [3]string{l.Status, l.EvidenceStatus, l.ReviewResult.Status}, [3]string{"review-required", "checked", "review-required"}, "ledger")
		}
	})
	tw(t, "acceptance", "allows an explicit review opt-out without claiming reviewed status", func(t *testing.T) {
		cwd := tempRepoDir(t)
		for _, review := range []string{`false`, `{"agent":"reviewer","required":false}`} {
			a := resolve(t, acceptanceRun{AgentName: "worker", Task: "Implement an async fix", Async: true, Explicit: js(t, `{"level":"checked","review":`+review+`}`)})
			l := evaluateAcceptance(acceptanceEval{Acceptance: a, Cwd: cwd, ReviewResult: &reviewResult{Status: "review-required", Findings: []reviewFinding{}}, Output: report(t, map[string]any{"criteriaSatisfied": []any{js(t, `{"id":"criterion-1","status":"satisfied","evidence":"implemented"}`), js(t, `{"id":"criterion-2","status":"satisfied","evidence":"evidence returned"}`)}})})
			equal(t, [2]string{l.Status, l.EvidenceStatus}, [2]string{"checked", "checked"}, review)
			equal(t, l.ReviewResult == nil, true, review+" review result")
		}
	})
	tw(t, "acceptance", "zero-child aggregate reports do not fabricate required evidence", func(t *testing.T) {
		a := resolve(t, acceptanceRun{AgentName: "worker", Task: "Implement dynamic fanout fixes", Explicit: js(t, `{"level":"checked"}`)})
		l := evaluateAcceptance(acceptanceEval{Acceptance: a, Output: "", Report: aggregateAcceptanceReport(nil, ""), HasReport: true, Cwd: tempRepoDir(t)})
		equal(t, l.Status, "rejected", "status")
		polMatches(t, acceptanceFailureMessage(l), `criterion|changed-files|tests-added|commands-run|validation-output|no-staged-files`)
	})
	tw(t, "acceptance", "recovers the acceptance report from child-written configured output", func(t *testing.T) {
		cwd := tempRepoDir(t)
		a := checkedRun(t)
		receipt := "Report written to the configured output file."
		equal(t, ledgerOf(a, receipt, cwd).Status, "rejected", "without file")
		l := evaluateAcceptance(acceptanceEval{Acceptance: a, Output: receipt, Cwd: cwd, FileOutput: &acceptanceFileOutput{Content: report(t, nil), Path: "/tmp/report.md", Authoritative: true}})
		equal(t, l.Status, "checked", "status")
		equal(t, l.ChildReport != nil, true, "report")
	})
	tw(t, "acceptance", "report source order follows output authority", func(t *testing.T) {
		cwd := tempRepoDir(t)
		a := checkedRun(t)
		inline := evaluateAcceptance(acceptanceEval{Acceptance: a, Cwd: cwd, Output: report(t, map[string]any{"notes": "from text"}), FileOutput: &acceptanceFileOutput{Content: report(t, map[string]any{"notes": "from file"}), Path: "/tmp/report.md"}})
		equal(t, inline.ChildReport.vals["notes"], "from text", "inline")
		fileOnly := evaluateAcceptance(acceptanceEval{Acceptance: a, Cwd: cwd, Output: report(t, map[string]any{"notes": "from text"}), FileOutput: &acceptanceFileOutput{Content: report(t, map[string]any{"notes": "from file"}), Path: "/tmp/report.md", Authoritative: true}})
		equal(t, fileOnly.ChildReport.vals["notes"], "from file", "file only")
	})
	tw(t, "acceptance", "sibling overwrites of a shared output path cannot pollute this child's acceptance", func(t *testing.T) {
		cwd := tempRepoDir(t)
		shared := filepath.Join(cwd, "context.md")
		writeFile(t, shared, report(t, map[string]any{"notes": "sibling B report"}))
		msgs := []any{
			map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "toolCall", "id": "w1", "name": "write", "arguments": map[string]any{"path": shared, "content": report(t, map[string]any{"notes": "child A report"})}}}},
			map[string]any{"role": "toolResult", "toolCallId": "w1", "toolName": "write", "content": []any{map[string]any{"type": "text", "text": "ok"}}, "isError": false},
		}
		content, ok := extractChildWrittenOutput(msgs, shared, cwd)
		equal(t, ok, true, "extracted")
		l := evaluateAcceptance(acceptanceEval{Acceptance: checkedRun(t), Cwd: cwd, Output: "Report written to the configured output file.", FileOutput: &acceptanceFileOutput{Content: content, Path: shared, Authoritative: true}})
		equal(t, l.ChildReport.vals["notes"], "child A report", "notes")
	})
	tw(t, "acceptance", "rejects a malformed primary report instead of falling back to the secondary source", func(t *testing.T) {
		cwd := tempRepoDir(t)
		a := checkedRun(t)
		malformed := []string{"```acceptance-report\n{ not json\n```", "```acceptance-report\n```", "```acceptance-report\n{\"changedFiles\": false}", "ACCEPTANCE_REPORT: { not json"}
		for _, m := range malformed {
			l := evaluateAcceptance(acceptanceEval{Acceptance: a, Cwd: cwd, Output: report(t, map[string]any{"notes": "valid text report"}), FileOutput: &acceptanceFileOutput{Content: m, Path: "/tmp/report.md", Authoritative: true}})
			equal(t, l.Status, "rejected", m)
			polMatches(t, l.ChildReportParseError, `configured output`)
		}
		inline := evaluateAcceptance(acceptanceEval{Acceptance: a, Cwd: cwd, Output: malformed[0], FileOutput: &acceptanceFileOutput{Content: report(t, map[string]any{"notes": "valid file report"}), Path: "/tmp/report.md"}})
		equal(t, inline.Status, "rejected", "inline")
		polMatches(t, inline.ChildReportParseError, `Failed to parse acceptance-report`)
	})
	tw(t, "acceptance", "records durable malformed report evidence without relaxing rejection", func(t *testing.T) {
		cwd := tempRepoDir(t)
		a := checkedRun(t)
		reportPath := filepath.Join(cwd, "child-report.md")
		malformed := `{"criteriaSatisfied":true,"commandsRun":["npm test"]}`
		content := "ACCEPTANCE_REPORT: " + malformed
		file := func(c string) *acceptanceFileOutput {
			return &acceptanceFileOutput{Content: c, Path: reportPath, Authoritative: true, Durable: true}
		}
		l := evaluateAcceptance(acceptanceEval{Acceptance: a, Cwd: cwd, Output: "file-only child output", FileOutput: file(content)})
		equal(t, l.Status, "rejected", "status")
		sum := sha256.Sum256([]byte(content))
		equal(t, *l.Recovery, acceptanceRecovery{Status: "available-for-review", Reason: "acceptance-metadata-rejected", ReportPath: reportPath, ReportHash: hex.EncodeToString(sum[:])}, "recovery")
		optional := evaluateAcceptance(acceptanceEval{Acceptance: a, Cwd: cwd, Output: "file-only child output", FileOutput: file(content), ReportOptional: true})
		equal(t, optional.Status, "rejected", "optional")
		equal(t, *optional.Recovery, *l.Recovery, "optional recovery")
		missing := evaluateAcceptance(acceptanceEval{Acceptance: a, Cwd: cwd, Output: "file-only child output", FileOutput: file("artifact without acceptance metadata")})
		equal(t, missing.Status, "rejected", "missing")
		equal(t, missing.Recovery == nil, true, "missing recovery")
		textOnly := evaluateAcceptance(acceptanceEval{Acceptance: a, Cwd: cwd, Output: content, FileOutput: file("artifact without acceptance metadata")})
		equal(t, textOnly.Status, "rejected", "text only")
		equal(t, textOnly.Recovery == nil, true, "text only recovery")
		unmetReport := report(t, map[string]any{"criteriaSatisfied": []any{js(t, `{"id":"criterion-1","status":"not-satisfied","evidence":"not proved"}`)}})
		unmet := evaluateAcceptance(acceptanceEval{Acceptance: a, Cwd: cwd, Output: unmetReport, FileOutput: file(unmetReport)})
		equal(t, unmet.Status, "rejected", "unmet")
		equal(t, unmet.Recovery == nil, true, "unmet recovery")
	})
	tw(t, "acceptance", "surfaces field-level errors from an invalid configured-output report", func(t *testing.T) {
		l := evaluateAcceptance(acceptanceEval{Acceptance: checkedRun(t), Cwd: tempRepoDir(t), Output: "wrote the report to the file", FileOutput: &acceptanceFileOutput{Content: report(t, map[string]any{"criteriaSatisfied": []any{js(t, `{"id":"criterion-1","status":"partially_done","evidence":"x"}`)}}), Path: "/tmp/report.md"}})
		equal(t, l.Status, "rejected", "status")
		polMatches(t, acceptanceFailureMessage(l), `partially_done`)
		polMatches(t, acceptanceFailureMessage(l), `configured output`)
	})
	tw(t, "acceptance", "explicit levels are honored over declared read-only policy without silent escalation", func(t *testing.T) {
		equal(t, resolve(t, acceptanceRun{AgentName: "researcher", AcceptanceRole: "read-only", Task: "Research PDF backends. Do not modify project/source files.", Explicit: "checked", Async: true}).Level, "checked", "level")
	})
}

func TestAcceptanceGateAndVerify(t *testing.T) {
	tw(t, "acceptance", "normalizes one gate command and rejects acceptance combinations", func(t *testing.T) {
		got, msg := normalizeGateAcceptance(" npm run check ", nil)
		equal(t, msg, "", "error")
		equal(t, marshalJSON(got, ""), `{"level":"verified","verify":[{"id":"gate","command":"npm run check"}]}`, "gate")
		_, msg = normalizeGateAcceptance("", nil)
		polMatches(t, msg, `non-empty command string`)
		_, msg = normalizeGateAcceptance("npm test", "checked")
		polMatches(t, msg, `cannot be combined with acceptance`)
		polMatches(t, msg, `Both fields were present: gate="npm test" acceptance="checked"`)
		_, msg = normalizeGateAcceptance("npm test", map[string]any{"level": "checked", "evidence": []any{strings.Repeat("a", 200)}})
		polMatches(t, msg, `Both fields were present`)
		equal(t, strings.Contains(msg, strings.Repeat("a", 200)), false, "oversized acceptance is cut")
		got, msg = normalizeGateAcceptance("npm test", false)
		equal(t, msg, "", "disabled error")
		equal(t, marshalJSON(got, ""), `{"level":"verified","verify":[{"id":"gate","command":"npm test"}]}`, "disabled")
	})
	tw(t, "acceptance", "keeps explicit acceptance.verify arrays as existing verified acceptance", func(t *testing.T) {
		skipWindows(t)
		cwd := tempGitRepoDir(t)
		a := resolve(t, acceptanceRun{AgentName: "worker", Task: "run explicit verification", Explicit: js(t, `{"level":"verified","verify":[{"id":"one","command":"printf ok > one.txt"},{"id":"two","command":"printf ok > two.txt"}]}`), AgentContract: true})
		l := evaluateAcceptance(acceptanceEval{Acceptance: a, Output: "done", Cwd: cwd, ReportOptional: true, ArtifactsDir: filepath.Join(cwd, ".artifacts"), RunID: "multi-verify"})
		equal(t, l.Status, "verified", "status")
		var got [][2]string
		for _, r := range l.VerifyRuns {
			got = append(got, [2]string{r.ID, r.Status})
		}
		equal(t, got, [][2]string{{"one", "passed"}, {"two", "passed"}}, "runs")
		for _, n := range []string{"one.txt", "two.txt"} {
			b, _ := os.ReadFile(filepath.Join(cwd, n))
			equal(t, string(b), "ok", n)
		}
	})
	tw(t, "acceptance", "memoizes host verification by tracked Git state, including failures", func(t *testing.T) {
		skipWindows(t)
		cwd := tempGitRepoDir(t)
		artifactsDir := filepath.Join(cwd, ".artifacts")
		t.Setenv("GATE_INHERITED_SECRET", "host-secret-not-persisted")
		a := resolve(t, acceptanceRun{AgentName: "worker", Task: "run gate", AgentContract: true, Explicit: js(t, `{"level":"verified","verify":[{"id":"gate","command":"printf '%s:%s' \"$GATE_SECRET\" \"$GATE_INHERITED_SECRET\"; printf x >> count.txt","env":{"GATE_SECRET":"not-persisted"}}]}`)})
		evaluate := func(a *resolvedAcceptance, run string) *acceptanceLedger {
			return evaluateAcceptance(acceptanceEval{Acceptance: a, Output: "done", Cwd: cwd, ReportOptional: true, ArtifactsDir: artifactsDir, RunID: run})
		}
		memo := func(l *acceptanceLedger) bool { return l.VerifyRuns[0].Memoized != nil && *l.VerifyRuns[0].Memoized }
		first := evaluate(a, "memo-run")
		same := evaluate(a, "memo-run")
		writeFile(t, filepath.Join(cwd, "untracked.txt"), "ignored\n")
		untracked := evaluate(a, "memo-run")
		t.Setenv("GATE_INHERITED_SECRET", "host-secret-changed")
		inheritedChanged := evaluate(a, "memo-run")
		writeFile(t, filepath.Join(cwd, "file.txt"), "tracked change\n")
		tracked := evaluate(a, "memo-run")
		count, _ := os.ReadFile(filepath.Join(cwd, "count.txt"))
		equal(t, string(count), "xxx", "runs")
		equal(t, memo(first), false, "first")
		equal(t, first.VerifyRuns[0].Stdout, "[REDACTED]:[REDACTED]", "first stdout")
		equal(t, memo(same), true, "same")
		equal(t, memo(untracked), true, "untracked")
		equal(t, memo(inheritedChanged), false, "inherited changed")
		equal(t, inheritedChanged.VerifyRuns[0].Stdout, "[REDACTED]:[REDACTED]", "inherited stdout")
		equal(t, memo(tracked), false, "tracked")
		artifact, _ := os.ReadFile(first.VerifyRuns[0].ArtifactPath)
		for _, secret := range []string{"not-persisted", "host-secret-not-persisted", "host-secret-changed"} {
			equal(t, strings.Contains(string(artifact), secret), false, "artifact has "+secret)
		}
		polMatches(t, string(artifact), `GATE_SECRET`)
		failing := resolve(t, acceptanceRun{AgentName: "worker", Task: "run gate", AgentContract: true, Explicit: js(t, `{"level":"verified","verify":[{"id":"gate","command":"printf x >> fail-count.txt; exit 7"}]}`)})
		failOnce := evaluate(failing, "failure-run")
		failCached := evaluate(failing, "failure-run")
		equal(t, failOnce.Status, "rejected", "fail once")
		equal(t, failCached.Status, "rejected", "fail cached")
		equal(t, memo(failCached), true, "fail memoized")
		equal(t, *failCached.VerifyRuns[0].ExitCode, 7, "exit code")
		fc, _ := os.ReadFile(filepath.Join(cwd, "fail-count.txt"))
		equal(t, string(fc), "x", "fail count")
	})
}

func TestQuoteExecutableForShell(t *testing.T) {
	q := func(c string) string { return quoteExecutableForShell(c, "win32") }
	tw(t, "acceptance", "quotes an unquoted Windows executable path containing spaces", func(t *testing.T) {
		equal(t, q(`C:\Program Files\vendor\tool.exe --flag`), `"C:\Program Files\vendor\tool.exe" --flag`, "quoted")
	})
	tw(t, "acceptance", "quotes an unquoted Windows executable path with parentheses", func(t *testing.T) {
		equal(t, q(`C:\Program Files (x86)\vendor\tool.exe --flag`), `"C:\Program Files (x86)\vendor\tool.exe" --flag`, "quoted")
	})
	tw(t, "acceptance", "quotes an extensionless spaced executable before a later drive-root argument", func(t *testing.T) {
		equal(t, q(`C:\Program Files\Tool\verify C:\path\to\file.exe`), `"C:\Program Files\Tool\verify" C:\path\to\file.exe`, "quoted")
	})
	tw(t, "acceptance", "quotes an extensionless spaced executable before a flag argument", func(t *testing.T) {
		equal(t, q(`C:\Program Files\Tool\verify --flag`), `"C:\Program Files\Tool\verify" --flag`, "quoted")
	})
	tw(t, "acceptance", "quotes an extensionless spaced executable filename before a flag argument", func(t *testing.T) {
		equal(t, q(`C:\Program Files\my tool --flag`), `"C:\Program Files\my tool" --flag`, "quoted")
	})
	tw(t, "acceptance", "quotes a spaced executable before a later drive-root executable argument", func(t *testing.T) {
		equal(t, q(`C:\Program Files\Tool\verify.exe C:\path\to\file.exe`), `"C:\Program Files\Tool\verify.exe" C:\path\to\file.exe`, "quoted")
	})
	tw(t, "acceptance", "quotes a spaced executable filename", func(t *testing.T) {
		equal(t, q(`C:\Program Files\my tool.exe --flag`), `"C:\Program Files\my tool.exe" --flag`, "quoted")
	})
	tw(t, "acceptance", "leaves an already-quoted command unchanged", func(t *testing.T) {
		equal(t, q(`"C:\Program Files\vendor\tool.exe" --flag`), `"C:\Program Files\vendor\tool.exe" --flag`, "quoted")
	})
	tw(t, "acceptance", "leaves a single-token command unchanged", func(t *testing.T) { equal(t, q(`phpunit`), `phpunit`, "quoted") })
	tw(t, "acceptance", "leaves a first token without spaces unchanged", func(t *testing.T) { equal(t, q(`node script.js --check`), `node script.js --check`, "quoted") })
	tw(t, "acceptance", "leaves a Windows executable path without spaces unchanged", func(t *testing.T) { equal(t, q(`C:\tools\tool.exe --flag`), `C:\tools\tool.exe --flag`, "quoted") })
	tw(t, "acceptance", "leaves an executable followed by an executable-looking argument unchanged", func(t *testing.T) { equal(t, q(`C:\tools\node script.exe`), `C:\tools\node script.exe`, "quoted") })
	tw(t, "acceptance", "quotes a shallow spaced executable filename", func(t *testing.T) {
		equal(t, q(`C:\Program Files\node script.exe`), `"C:\Program Files\node script.exe"`, "quoted")
	})
	tw(t, "acceptance", "leaves ambiguous shell syntax in extensionless commands unchanged", func(t *testing.T) {
		equal(t, q(`C:\Program Files\Tool\verify&echo C:\arg`), `C:\Program Files\Tool\verify&echo C:\arg`, "quoted")
	})
	tw(t, "acceptance", "leaves a spaced executable argument after an executable unchanged", func(t *testing.T) {
		equal(t, q(`C:\tools\tool.exe C:\Program Files\data\file.exe`), `C:\tools\tool.exe C:\Program Files\data\file.exe`, "quoted")
	})
	tw(t, "acceptance", "leaves a spaced executable-looking argument after an extensionless executable unchanged", func(t *testing.T) {
		equal(t, q(`C:\tools\verify C:\Program Files\data\file.exe`), `C:\tools\verify C:\Program Files\data\file.exe`, "quoted")
	})
	tw(t, "acceptance", "passes commands through unchanged on non-Windows platforms", func(t *testing.T) {
		c := "/opt/my tools/runner --flag"
		equal(t, quoteExecutableForShell(c, "linux"), c, "linux")
		equal(t, quoteExecutableForShell(c, "darwin"), c, "darwin")
	})
	tw(t, "acceptance", "leaves ambiguous Windows commands unchanged", func(t *testing.T) {
		for _, c := range []string{"tool file.exe", "cmd /c tool.exe", "echo hi > out.exe"} {
			equal(t, q(c), c, c)
		}
	})
}
