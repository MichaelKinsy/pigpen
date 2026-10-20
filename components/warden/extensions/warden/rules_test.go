package warden

// Twins of tests/rules.test.ts (the project rules guard). Each test creates its own project directory: the upstream
// file shares one directory across its cases and several cases depend on the files an earlier one wrote.

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func rulesConfig(mutate ...func(*RulesConfig)) RulesConfig {
	c := DefaultConfig().Rules
	for _, m := range mutate {
		m(&c)
	}
	return c
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// rulesProject is the upstream suite's shared project: a source file to edit.
func rulesProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src", "db", "migrations"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "src", "user.ts"), strings.Join([]string{"import { db } from \"./db\";", "", "export async function findUser(id: string) {", "  const row = await db.get(id);", "  return row;", "}", ""}, "\n"))
	return dir
}

const rulesMD = "# No console statements\n" +
	"Code must not contain `console.log` or `console.debug` calls. Use the logger.\n" +
	"\n" +
	"# Explicit return types\n" +
	"paths: src/**/*.ts\n" +
	"Every exported function must declare its return type.\n" +
	"```ts\n" +
	"# not a heading: inside a fence\n" +
	"export function f(): number { return 1; }\n" +
	"```\n" +
	"\n" +
	"# TODO comments need a reference\n" +
	"A bare TODO is a violation.\n" +
	"\n" +
	"# No console statements\n" +
	"Duplicate heading gets a distinct id."

// stubJudge answers every rule question with the outcome set per rule id; unnamed rules are compliant. The locator
// picks `locator`, or the first edit.
func stubJudge(violations map[string]float64, locator string) *recordingJudge {
	return &recordingJudge{handler: func(r Request) (Evaluation, error) {
		answers := map[string]Answer{}
		for id, q := range r.Questions {
			if id == locatorQuestion {
				keys := q.CriteriaOrder
				pick := locator
				if pick == "" {
					pick = keys[0]
				}
				probs := map[string]float64{}
				for _, key := range keys {
					if key == pick {
						probs[key] = 0.7
					} else {
						probs[key] = 0.3 / float64(len(keys)-1)
					}
				}
				answers[id] = Answer{Type: "choice", Choice: pick, Confidence: 0.7, Probabilities: probs}
				continue
			}
			violation, ok := violations[strings.TrimPrefix(id, "rule_")]
			if !ok {
				violation = 0.05
			}
			choice := "compliant"
			if violation >= 0.5 {
				choice = "violation"
			}
			answers[id] = Answer{Type: "choice", Choice: choice, Confidence: 0.9, Probabilities: map[string]float64{"compliant": 1 - violation - 0.02, "violation": violation, "not_applicable": 0.01, "insufficient_context": 0.01}}
		}
		return Evaluation{Model: "jev-test", ElapsedMs: 7, Answers: answers}, nil
	}}
}

func keysOf(q Questions) []string {
	var out []string
	for k := range q {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

func ruleIDs(rules []Rule) []string {
	out := []string{}
	for _, r := range rules {
		out = append(out, r.ID)
	}
	return out
}

func findingIDs(findings []RuleFinding) []string {
	out := []string{}
	for _, f := range findings {
		out = append(out, f.ID)
	}
	return out
}

func writeInput(path, content string) map[string]any {
	return map[string]any{"path": path, "content": content}
}

func editInput(path string, edits ...map[string]any) map[string]any {
	list := make([]any, len(edits))
	for i, e := range edits {
		list[i] = e
	}
	return map[string]any{"path": path, "edits": list}
}

func edit(oldText, newText string) map[string]any {
	return map[string]any{"oldText": oldText, "newText": newText}
}

func rulesOpts(cwd string, config RulesConfig, set *RuleSet, judge Judge) RulesOptions {
	return RulesOptions{Cwd: cwd, Config: config, Set: set, Judge: judge, Timeout: time.Second}
}

// twin: tests/rules.test.ts:67
func TestParseRulesTopLevelHeadingsDelimitRules(t *testing.T) {
	rules := parseRules(rulesMD)
	mustEqual(t, ruleIDs(rules), []string{"no-console-statements", "explicit-return-types", "todo-comments-need-a-reference", "no-console-statements-2"}, "ids")
	mustEqual(t, rules[1].Paths, []string{"src/**/*.ts"}, "paths")
	mustMatch(t, rules[1].Body, `# not a heading: inside a fence`, "fenced heading stays in the body")
	mustNotContain(t, rules[1].Body, "paths:", "the paths line is removed from the body")
	mustEqual(t, len(rules[0].Paths), 0, "unscoped")
	mustEqual(t, rules[2].Body, "A bare TODO is a violation.", "body")
}

// twin: tests/rules.test.ts:77
func TestParseRulesHighestHeadingLevelDelimits(t *testing.T) {
	rules := parseRules("# Project rules\n\nIntro text.\n\n## Use const\nPrefer const.\n\n### Not a rule\nSub-detail.\n\n## No any\nAvoid any.")
	names := []string{}
	for _, r := range rules {
		names = append(names, r.Name)
	}
	mustEqual(t, names, []string{"Project rules"}, "the single H1 is the top level here")
	h2 := parseRules("Intro without a title.\n\n## Use const\nPrefer const.\n\n### Detail\nmore\n\n## No any\nAvoid any.")
	names = []string{}
	for _, r := range h2 {
		names = append(names, r.Name)
	}
	mustEqual(t, names, []string{"Use const", "No any"}, "H2 rules")
	mustMatch(t, h2[0].Body, `### Detail`, "the sub-heading stays in the body")
	mustEqual(t, len(parseRules("Just prose, no headings.")), 0, "no headings, no rules")
}

// twin: tests/rules.test.ts:86
func TestCondenseKeepsEveryHeadingAndTheHeadOfEachSection(t *testing.T) {
	var sections []string
	for i := 0; i < 40; i++ {
		sections = append(sections, "## Section "+itoaInt(i)+"\n"+strings.Repeat("x", 1000)+" rule-"+itoaInt(i))
	}
	document := "# Big AGENTS.md\n\n" + strings.Join(sections, "\n\n")
	out := condense(document, 8000)
	if utf16Len(out) > 8000+60 {
		t.Fatalf("length %d", utf16Len(out))
	}
	mustMatch(t, out, `## Section 0\n`, "first heading")
	mustMatch(t, out, `## Section 39\n`, "last heading")
	mustEqual(t, condense("short", 8000), "short", "short text")
}

// twin: tests/rules.test.ts:96
func TestGlobsAndProjectPaths(t *testing.T) {
	cwd := rulesProject(t)
	first := func(path string, patterns ...string) any {
		if g, ok := matchGlobFirst(path, patterns); ok {
			return g
		}
		return nil
	}
	mustEqual(t, first("db/migrations/0182.sql", "migrations/**"), "migrations/**", "any depth")
	mustEqual(t, first("src/auth/permissions.ts", "**/permissions*"), "**/permissions*", "permissions")
	mustEqual(t, first("src/a.test.ts", "**/*.test.*"), "**/*.test.*", "test files")
	mustEqual(t, first("src/a.ts", "**/*.test.*", "docs/**"), nil, "no match")
	mustEqual(t, first("src/deep/x.ts", "src/*.ts"), nil, "* does not cross a slash")
	mustEqual(t, first("README.md", "*.md"), "*.md", "segment")
	project := func(target string) any {
		if p, ok := rulesProjectPath(target, cwd); ok {
			return p
		}
		return nil
	}
	mustEqual(t, project("src/x.ts"), "src/x.ts", "relative")
	mustEqual(t, project(filepath.Join(cwd, "src", "x.ts")), "src/x.ts", "absolute inside")
	mustEqual(t, project("/tmp/elsewhere.ts"), nil, "outside")
	mustEqual(t, project("../sibling/x.ts"), nil, "parent")
}

// twin: tests/rules.test.ts:109
func TestRuleStoreResolutionOrderAndReread(t *testing.T) {
	cwd := rulesProject(t)
	store := NewRuleStore()
	if store.Load(cwd, rulesConfig()) != nil {
		t.Fatal("nothing yet")
	}
	writeFile(t, filepath.Join(cwd, "AGENTS.md"), "# Agents\n\nAlways write tests.\n")
	writeFile(t, filepath.Join(cwd, "README.md"), "# Readme\n\nInstall with npm.\n")
	set := store.Load(cwd, rulesConfig())
	mustEqual(t, set.Sources, []string{"AGENTS.md"}, "AGENTS is the first fallback")
	mustEqual(t, len(set.Rules), 0, "no rules")
	mustMatch(t, set.Aggregate, `Always write tests`, "aggregate")
	if store.Load(cwd, rulesConfig(func(c *RulesConfig) { c.Fallback = false })) != nil {
		t.Fatal("fallback can be turned off")
	}

	writeFile(t, filepath.Join(cwd, "docs-rules.md"), "# Use const\nPrefer const over let.\n")
	set = store.Load(cwd, rulesConfig(func(c *RulesConfig) { c.Files = []string{"docs-rules.md", "missing.md"} }))
	mustEqual(t, set.Sources, []string{"docs-rules.md"}, "configured")
	mustEqual(t, ruleIDs(set.Rules), []string{"use-const"}, "configured rules")

	writeFile(t, filepath.Join(cwd, "pi-warden.md"), rulesMD)
	set = store.Load(cwd, rulesConfig(func(c *RulesConfig) { c.Files = []string{"docs-rules.md"} }))
	mustEqual(t, set.Sources, []string{"pi-warden.md"}, "the root file wins over configured files")
	mustEqual(t, len(set.Rules), 4, "four rules")

	// Same size and mtime: cached. Changed content with a new mtime: re-read.
	stale := time.Now().Add(-time.Minute)
	writeFile(t, filepath.Join(cwd, "pi-warden.md"), rulesMD+"\n\n# Fifth rule\nBody.\n")
	if err := os.Chtimes(filepath.Join(cwd, "pi-warden.md"), stale, stale); err != nil {
		t.Fatal(err)
	}
	mustEqual(t, len(store.Load(cwd, rulesConfig()).Rules), 5, "re-read")
}

func countRuleQuestions(q Questions) int {
	n := 0
	for k := range q {
		if strings.HasPrefix(k, "rule_") {
			n++
		}
	}
	return n
}

// twin: tests/rules.test.ts:137
func TestRuleCapAppliesAfterPathScoping(t *testing.T) {
	var docs, code []string
	for i := 1; i <= 12; i++ {
		docs = append(docs, "# Docs rule "+itoaInt(i)+"\npaths: docs/**\nBody "+itoaInt(i)+".")
	}
	for i := 1; i <= 28; i++ {
		code = append(code, "# Code rule "+itoaInt(i)+"\nBody "+itoaInt(i)+".")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pi-warden.md"), strings.Join(append(docs, code...), "\n\n"))
	set := NewRuleStore().Load(dir, rulesConfig())
	mustEqual(t, len(set.Rules), 40, "every parsed rule is kept")
	mustEqual(t, set.AlwaysDropped, 0, "none dropped for every file")
	request := buildRulesRequest(describeTarget("write", writeInput("src/x.ts", "x"), dir), set)
	mustEqual(t, countRuleQuestions(request.Questions), 28, "28 questions")
	mustEqual(t, request.Dropped, 0, "dropped")
	mustEqual(t, request.FirstDropped, "", "firstDropped")
	mustEqual(t, describeRuleSet(set), "pi-warden.md (40 rules)", "description")
}

// twin: tests/rules.test.ts:155
func TestRuleCapDropsUnscopedRulesPast31(t *testing.T) {
	var many []string
	for i := 1; i <= 40; i++ {
		many = append(many, "# Rule "+itoaInt(i)+"\nBody "+itoaInt(i)+".")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pi-warden.md"), strings.Join(many, "\n\n"))
	set := NewRuleStore().Load(dir, rulesConfig())
	mustEqual(t, len(set.Rules), 40, "rules")
	mustEqual(t, set.AlwaysDropped, 9, "always dropped")
	request := buildRulesRequest(describeTarget("write", writeInput("src/x.ts", "x"), dir), set)
	mustEqual(t, countRuleQuestions(request.Questions), maxRules, "questions")
	mustEqual(t, request.Dropped, 9, "dropped")
	mustEqual(t, request.FirstDropped, set.Rules[31].ID, "first dropped")
	mustEqual(t, request.FirstDropped, "rule-32", "first dropped id")
	mustEqual(t, describeRuleSet(set), "pi-warden.md (40 rules, 9 unscoped past the 31-question cap for every file)", "description")
}

// twin: tests/rules.test.ts:173
func TestDescribeTarget(t *testing.T) {
	cwd := rulesProject(t)
	write := describeTarget("write", writeInput("src/new.ts", "const token = \"sk-abcdefghijklmnop1234\";\n"+strings.Repeat("x", 9000)), cwd)
	mustEqual(t, write.Path, "src/new.ts", "path")
	if utf16Len(*write.Content) >= 6300 {
		t.Fatal("not sampled")
	}
	mustNotContain(t, *write.Content, "sk-abcdefghijklmnop1234", "redacted")
	mustMatch(t, *write.Content, `chars\] …`, "head/middle/tail sample")

	got := describeTarget("edit", editInput("src/user.ts", edit("  return row;", "  console.log(row);\n  return row;"), edit("nowhere", "// TODO"), edit("x", "   ")), cwd)
	mustEqual(t, len(got.Edits), 2, "blank new text is dropped")
	mustEqual(t, got.Edits[0].ID, "edit_1", "id")
	mustMatch(t, *got.Edits[0].Before, `export async function findUser`, "context lines around the old text")
	mustMatch(t, *got.Edits[0].After, `export async function findUser\(id: string\) \{\n  const row = await db\.get\(id\);\n  console\.log\(row\);\n  return row;\n\}`, "the same lines with the edit applied")
	if got.Edits[1].Before != nil || got.Edits[1].After != nil {
		t.Fatal("old text not found: no context")
	}
	if describeTarget("write", writeInput("/tmp/outside.ts", "x"), cwd) != nil {
		t.Fatal("outside the project")
	}
	if describeTarget("bash", map[string]any{"command": "ls"}, cwd) != nil {
		t.Fatal("bash")
	}
	if describeTarget("write", writeInput("src/empty.ts", "   "), cwd) != nil {
		t.Fatal("empty content")
	}
}

// twin: tests/rules.test.ts:192
func TestBuildRulesRequest(t *testing.T) {
	cwd := rulesProject(t)
	set := &RuleSet{Sources: []string{"pi-warden.md"}, Rules: parseRules(rulesMD)}
	target := describeTarget("edit", editInput("src/user.ts", edit("  return row;", "  console.log(row);"), edit("import", "// TODO fix")), cwd)
	request := buildRulesRequest(target, set)
	mustEqual(t, keysOf(request.Questions), []string{"rule_explicit-return-types", "rule_no-console-statements", "rule_no-console-statements-2", "rule_todo-comments-need-a-reference", locatorQuestion}, "questions")
	question := request.Questions["rule_no-console-statements"]
	mustMatch(t, question.Instructions, `Rule: No console statements\nCode must not contain`, "rule text in the question")
	mustEqual(t, question.CriteriaOrder, []string{"compliant", "violation", "not_applicable", "insufficient_context"}, "outcomes")
	mustEqual(t, request.State["path"], "src/user.ts", "path")
	mustEqual(t, len(request.State["edits"].([]any)), 2, "edits")
	if _, ok := request.State["rules"]; ok {
		t.Fatal("rule text rides in the questions, not the state")
	}

	scoped := buildRulesRequest(describeTarget("write", writeInput("docs/guide.md", "console.log"), cwd), set)
	if has(scoped.Questions, "rule_explicit-return-types") {
		t.Fatal("paths: src/**/*.ts excludes docs")
	}
	if has(scoped.Questions, locatorQuestion) {
		t.Fatal("no locator for a write")
	}

	aggregate := buildRulesRequest(describeTarget("write", writeInput("src/a.ts", "x"), cwd), &RuleSet{Sources: []string{"AGENTS.md"}, Aggregate: "Always write tests.", HasAggregate: true})
	mustEqual(t, keysOf(aggregate.Questions), []string{aggregateQuestion}, "aggregate questions")
	mustEqual(t, aggregate.State["rules"], "Always write tests.", "aggregate state")
}

// twin: tests/rules.test.ts:214
func TestParseRulesThresholdAndSeverityHeaders(t *testing.T) {
	md := strings.Join([]string{
		"# High cutoff", "threshold: 0.9", "paths: src/**/*.ts", "severity: high", "Body text.", "",
		"# Bad values", "threshold: 1.5", "severity: urgent", "Body.", "",
		"# Duplicated", "threshold: 0.4", "threshold: 0.8", "Body.", "",
		"# Defaults", "Body with no headers.",
	}, "\n")
	rules := parseRules(md)
	mustEqual(t, *rules[0].Threshold, 0.9, "threshold")
	mustEqual(t, rules[0].Severity, "high", "severity")
	mustEqual(t, rules[0].Paths, []string{"src/**/*.ts"}, "paths")
	mustEqual(t, rules[0].Body, "Body text.", "body")
	mustEqual(t, len(rules[0].HeaderWarnings), 0, "no warnings")
	if rules[1].Threshold != nil {
		t.Fatal("bad threshold ignored")
	}
	mustEqual(t, rules[1].Severity, "", "bad severity ignored")
	mustEqual(t, rules[1].HeaderWarnings, []string{"threshold: 1.5 is not a number from 0 to 1; ignored", "severity: urgent is not high, normal, or low; ignored"}, "warnings")
	mustEqual(t, rules[1].Body, "Body.", "body")
	mustEqual(t, *rules[2].Threshold, 0.4, "the first value wins")
	mustEqual(t, rules[2].HeaderWarnings, []string{"threshold: appears more than once; the first is kept"}, "duplicate warning")
	if rules[3].Threshold != nil || rules[3].Severity != "" {
		t.Fatal("defaults")
	}
	mustEqual(t, rules[3].Body, "Body with no headers.", "body")
}

// twin: tests/rules.test.ts:252
func TestParseRulesSourceHeader(t *testing.T) {
	md := strings.Join([]string{
		"# Cited rule", "source: AGENTS.md:65", "paths: src/**/*.ts", "Body.", "",
		"# Bad citation", "source: AGENTS.md", "Body.", "",
		"# Duplicated", "source: AGENTS.md:1", "source: CLAUDE.md:9", "Body.",
	}, "\n")
	rules := parseRules(md)
	mustEqual(t, *rules[0].SourceRef, SourceRef{File: "AGENTS.md", Line: 65}, "sourceRef")
	mustEqual(t, rules[0].Body, "Body.", "body")
	mustEqual(t, len(rules[0].HeaderWarnings), 0, "no warnings")
	if rules[1].SourceRef != nil {
		t.Fatal("bad citation dropped")
	}
	mustEqual(t, rules[1].HeaderWarnings, []string{"source: AGENTS.md is not a file:line reference; ignored"}, "warning")
	mustEqual(t, rules[1].Body, "Body.", "body")
	mustEqual(t, *rules[2].SourceRef, SourceRef{File: "AGENTS.md", Line: 1}, "the first value wins")
	mustEqual(t, rules[2].HeaderWarnings, []string{"source: appears more than once; the first is kept"}, "duplicate warning")
	mustMatch(t, formatRuleSetDetails(&RuleSet{Sources: []string{"pi-warden.md"}, Rules: []Rule{rules[0]}}, "root", nil), `1\. cited-rule paths: src/\*\*/\*\.ts, source: AGENTS\.md:65`, "details")
}

// twin: tests/rules.test.ts:280
func TestSteerNamesTheInstructionLineOfACitedRule(t *testing.T) {
	verdict := RulesVerdict{
		Source: "typesafe", Tool: "write", Path: "src/a.ts", Sources: []string{"pi-warden.md"}, Asked: 1,
		Findings: []RuleFinding{{RuleScore: RuleScore{ID: "imperative-commits", Name: "Imperative commit messages", Outcome: "violation", Violation: 0.9, SourceRef: &SourceRef{File: "AGENTS.md", Line: 65}}, Body: "Use the imperative mood."}},
	}
	mustEqual(t, rulesSteer(verdict, nil), "pi-warden: the content just written to src/a.ts violates project rule: \"Imperative commit messages\" (from AGENTS.md line 65) (0.90): Use the imperative mood. Fix it in your next edit.", "steer")
}

// twin: tests/rules.test.ts:288
func TestFormatRuleSetDetailsShowsThresholdSeverityAndWarnings(t *testing.T) {
	set := &RuleSet{Sources: []string{"pi-warden.md"}, Rules: parseRules("# Cut\nthreshold: 0.9\nseverity: high\nBody.\n\n# Bad\nthreshold: nope\nBody.")}
	text := formatRuleSetDetails(set, "root", nil)
	mustMatch(t, text, `1\. cut paths: \(all\), threshold: 0\.9, severity: high`, "first rule")
	mustMatch(t, text, `2\. bad paths: \(all\)`, "second rule")
	mustMatch(t, text, `threshold: nope is not a number from 0 to 1; ignored`, "warning")
}

// twin: tests/rules.test.ts:296
func TestEvaluateRulesOwnCutoffBeatsGlobalAndFillsSoftTier(t *testing.T) {
	cwd := rulesProject(t)
	set := &RuleSet{Sources: []string{"pi-warden.md"}, Rules: parseRules("# Cut\nthreshold: 0.9\nBody A.\n\n# Plain\nBody B.")}
	verdict := evaluateRules("write", writeInput("src/a.ts", "x"), rulesOpts(cwd, rulesConfig(func(c *RulesConfig) { c.SoftThreshold = 0.5 }), set, stubJudge(map[string]float64{"cut": 0.85, "plain": 0.75}, "")))
	mustEqual(t, findingIDs(verdict.Findings), []string{"plain"}, "0.85 is below the rule's own 0.9 even though it clears the global 0.7")
	mustEqual(t, findingIDs(verdict.SoftFindings), []string{"cut"}, "soft")
	for _, score := range verdict.Scores {
		if score.ID == "cut" {
			mustEqual(t, *score.Threshold, 0.9, "score carries the cutoff")
		}
	}
	off := evaluateRules("write", writeInput("src/a.ts", "x"), rulesOpts(cwd, rulesConfig(), set, stubJudge(map[string]float64{"cut": 0.85, "plain": 0.62}, "")))
	mustEqual(t, len(off.Findings), 0, "no findings")
	mustEqual(t, len(off.SoftFindings), 0, "the soft tier is off by default")
}

// twin: tests/rules.test.ts:309
func TestSeverityOrdersFindingsAndTheSteer(t *testing.T) {
	cwd := rulesProject(t)
	set := &RuleSet{Sources: []string{"pi-warden.md"}, Rules: parseRules(strings.Join([]string{"# Normal rule", "Body.", "", "# High rule", "severity: high", "Body.", "", "# Low rule", "severity: low", "Body.", "", "# Maybe rule", "Body."}, "\n"))}
	judge := stubJudge(map[string]float64{"normal-rule": 0.95, "high-rule": 0.71, "low-rule": 0.99, "maybe-rule": 0.62}, "")
	verdict := evaluateRules("write", writeInput("src/a.ts", "x"), rulesOpts(cwd, rulesConfig(func(c *RulesConfig) { c.SoftThreshold = 0.5 }), set, judge))
	mustEqual(t, findingIDs(verdict.Findings), []string{"high-rule", "normal-rule", "low-rule"}, "severity first, then the score")
	mustEqual(t, findingIDs(verdict.SoftFindings), []string{"maybe-rule"}, "soft")
	mustMatch(t, rulesSteer(verdict, nil), `"High rule" \(0\.71\): Body; "Normal rule" \(0\.95\): Body; "Low rule" \(0\.99\): Body\. Fix it in your next edit\. Also check whether "Maybe rule" applies here \(0\.62\)\.$`, "steer")
}

// twin: tests/rules.test.ts:323
func TestSoftOnlyVerdictSteersWithJustTheDoubleCheckSentence(t *testing.T) {
	verdict := RulesVerdict{Source: "typesafe", Tool: "write", Path: "src/a.ts", Sources: []string{"pi-warden.md"}, Asked: 1,
		SoftFindings: []RuleFinding{{RuleScore: RuleScore{ID: "maybe", Name: "Maybe rule", Outcome: "compliant", Violation: 0.62}, Body: "Body."}}}
	mustEqual(t, rulesSteer(verdict, nil), "pi-warden: Also check whether \"Maybe rule\" applies here (0.62).", "steer")
}

// twin: tests/rules.test.ts:331
func TestSkipReasonNamesExcludeSkipScopingAndMissingRules(t *testing.T) {
	cwd := rulesProject(t)
	set := &RuleSet{Sources: []string{"pi-warden.md"}, Rules: parseRules("# Only TS\npaths: **/*.ts\nBody.")}
	target := describeTarget("write", writeInput("src/a.ts", "x"), cwd)
	mustEqual(t, skipReason(target, set, rulesConfig()), "", "judged")
	mustMatch(t, skipReason(target, set, rulesConfig(func(c *RulesConfig) { c.Exclude = []string{"src/**"} })), `rules\.exclude \(src/\*\*\)`, "exclude")
	mustMatch(t, skipReason(target, set, rulesConfig(func(c *RulesConfig) { c.Skip = []string{"**/*.ts"} })), `rules\.skip`, "skip")
	mustMatch(t, skipReason(describeTarget("write", writeInput("README.md", "x"), cwd), set, rulesConfig()), `no rule's paths match`, "scoping")
	mustMatch(t, skipReason(nil, set, rulesConfig()), `outside the project`, "outside")
	mustMatch(t, skipReason(target, nil, rulesConfig()), `no rules file`, "no rules")
}

// twin: tests/rules.test.ts:342
func TestEvaluateRulesFindingsStrongestFirstWithLocatedEdit(t *testing.T) {
	cwd := rulesProject(t)
	set := &RuleSet{Sources: []string{"pi-warden.md"}, Rules: parseRules(rulesMD)}
	judge := stubJudge(map[string]float64{"no-console-statements": 0.91, "todo-comments-need-a-reference": 0.72, "explicit-return-types": 0.69}, "edit_2")
	verdict := evaluateRules("edit", editInput("src/user.ts", edit("  return row;", "  console.log(row);"), edit("import", "// TODO fix")), rulesOpts(cwd, rulesConfig(), set, judge))
	mustEqual(t, verdict.Source, "typesafe", "source")
	mustEqual(t, verdict.Asked, 4, "asked")
	got := [][2]any{}
	for _, f := range verdict.Findings {
		got = append(got, [2]any{f.ID, f.Violation})
	}
	mustEqual(t, got, [][2]any{{"no-console-statements", 0.91}, {"todo-comments-need-a-reference", 0.72}}, "findings")
	mustEqual(t, verdict.Findings[0].Body, "Code must not contain `console.log` or `console.debug` calls. Use the logger.", "body")
	mustEqual(t, verdict.EditID, "edit_2", "edit")
	mustEqual(t, verdict.EditPreview, "// TODO fix", "preview")
	mustEqual(t, len(verdict.Scores), 4, "scores")

	clean := evaluateRules("write", writeInput("src/a.ts", "export const a = 1;"), rulesOpts(cwd, rulesConfig(), set, stubJudge(nil, "")))
	mustEqual(t, len(clean.Findings), 0, "clean")
	mustEqual(t, clean.EditID, "", "no edit")
}

// twin: tests/rules.test.ts:359
func TestEvaluateRulesAggregateSkipsAndErrors(t *testing.T) {
	cwd := rulesProject(t)
	set := &RuleSet{Sources: []string{"AGENTS.md"}, Aggregate: "Never use console.log.", HasAggregate: true}
	verdict := evaluateRules("write", writeInput("src/a.ts", "console.log(1)"), rulesOpts(cwd, rulesConfig(), set, stubJudge(map[string]float64{aggregateQuestion: 0.8}, "")))
	mustEqual(t, verdict.Aggregate, true, "aggregate")
	mustEqual(t, verdict.Asked, 1, "asked")
	names := []string{}
	for _, f := range verdict.Findings {
		names = append(names, f.Name)
	}
	mustEqual(t, names, []string{"the project's AGENTS.md"}, "named after the file")

	offline := evaluateRules("write", writeInput("src/a.ts", "x"), rulesOpts(cwd, rulesConfig(), set, nil))
	mustEqual(t, offline.Source, "skipped", "offline")
	mustMatch(t, offline.SkippedReason, `TypeSafe judgments are off`, "reason")

	failing := &recordingJudge{handler: func(Request) (Evaluation, error) { return Evaluation{}, os.ErrInvalid }}
	failed := evaluateRules("write", writeInput("src/a.ts", "x"), rulesOpts(cwd, rulesConfig(), set, failing))
	mustEqual(t, failed.Source, "error", "error")
	mustEqual(t, failed.Error, "TypeSafe request failed.", "upstream body must not leak")
	mustEqual(t, len(failed.Findings), 0, "no findings")
}

// twin: tests/rules.test.ts:377
func TestRulesSteerNamesTheRuleQuotesItAndMakesTheThirdRepeatStanding(t *testing.T) {
	verdict := RulesVerdict{
		Source: "typesafe", Tool: "edit", Path: "src/user.ts", Sources: []string{"pi-warden.md"}, Asked: 4,
		Findings: []RuleFinding{{RuleScore: RuleScore{ID: "no-console-statements", Name: "No console statements", Outcome: "violation", Violation: 0.91}, Body: "Code must not contain `console.log`.\nUse the logger."}},
		EditID:   "edit_2", EditPreview: "console.log(row);",
	}
	once := rulesSteer(verdict, map[string]int{"no-console-statements": 1})
	mustEqual(t, once, "pi-warden: the content just written to src/user.ts in edit 2 (starting \"console.log(row);\") violates project rule: \"No console statements\" (0.91): Code must not contain `console.log`. Use the logger. Fix it in your next edit.", "once")
	third := rulesSteer(verdict, map[string]int{"no-console-statements": 3})
	mustMatch(t, third, `\(0\.91; 3rd time this session\)`, "repeat")
	mustMatch(t, third, `Treat this as a standing rule for the rest of the session\.$`, "standing")
	located := verdict
	located.EditID, located.EditPreview = "", ""
	located.Aggregate, located.Sources = true, []string{"AGENTS.md"}
	located.Findings = []RuleFinding{{RuleScore: RuleScore{ID: "rules", Name: "the project's AGENTS.md", Outcome: "violation", Violation: 0.8}}}
	mustMatch(t, rulesSteer(located, nil), `^pi-warden: the content just written to src/user\.ts breaks a project rule: "the project's AGENTS\.md" \(0\.80\)\. Fix it`, "aggregate")
}

// twin: tests/rules.test.ts:393
func TestSensitivePathsNoteOncePerPathPerSession(t *testing.T) {
	notes := map[string]string{"migrations/**": "Tell the user this touches a migration and add a rollback", "**/permissions*": "Ask for a security review."}
	globs := func(hits []PathNote) []string {
		out := []string{}
		for _, h := range hits {
			out = append(out, h.Glob)
		}
		return out
	}
	mustEqual(t, globs(pathNotes("src/db/migrations/0182.sql", notes)), []string{"migrations/**"}, "migrations")
	mustEqual(t, globs(pathNotes("src/auth/permissions.ts", notes)), []string{"**/permissions*"}, "permissions")
	mustEqual(t, len(pathNotes("src/x.ts", notes)), 0, "none")
	mustEqual(t, len(pathNotes("", notes)), 0, "no path")
	mustEqual(t, pathNoteSteer("src/db/migrations/0182.sql", pathNotes("src/db/migrations/0182.sql", notes)), "pi-warden: src/db/migrations/0182.sql is a sensitive path in this project (migrations/**). Tell the user this touches a migration and add a rollback.", "steer")
	guard := NewRulesGuard()
	mustEqual(t, len(guard.NotesFor("src/db/migrations/0182.sql", notes)), 1, "first")
	mustEqual(t, len(guard.NotesFor("src/db/migrations/0182.sql", notes)), 0, "not repeated for the same path")
	mustEqual(t, len(guard.NotesFor("src/db/migrations/0183.sql", notes)), 1, "another path")
	guard.Reset()
	mustEqual(t, len(guard.NotesFor("src/db/migrations/0182.sql", notes)), 1, "reset")
}

// twin: tests/rules.test.ts:408
func TestRulesGuardPrejudgesSiblingsAndCountsRepeats(t *testing.T) {
	cwd := rulesProject(t)
	writeFile(t, filepath.Join(cwd, "pi-warden.md"), rulesMD)
	judge := stubJudge(map[string]float64{"no-console-statements": 0.9}, "")
	guard := NewRulesGuard()
	config := rulesConfig()
	a := RulesCallRef{ID: "a", Tool: "write", Input: writeInput("src/a.ts", "console.log(1)")}
	b := RulesCallRef{ID: "b", Tool: "write", Input: writeInput("src/b.ts", "console.log(2)")}
	first := guard.Inspect(a, []RulesCallRef{a, b}, rulesOpts(cwd, config, nil, judge)).Wait()
	mustEqual(t, len(judge.calls()), 2, "both siblings judged on the first inspection")
	second := guard.Inspect(b, []RulesCallRef{a, b}, rulesOpts(cwd, config, nil, judge)).Wait()
	mustEqual(t, len(judge.calls()), 2, "the sibling's prejudgment is reused")
	mustEqual(t, len(first.Findings), 1, "first")
	mustEqual(t, len(second.Findings), 1, "second")
	guard.Count(first)
	mustEqual(t, guard.Count(second)["no-console-statements"], 2, "repeat count")
	changed := guard.Inspect(RulesCallRef{ID: "b", Tool: "write", Input: writeInput("src/b.ts", "console.log(3)")}, []RulesCallRef{a, b}, rulesOpts(cwd, config, nil, judge)).Wait()
	mustEqual(t, len(judge.calls()), 3, "a changed input is judged afresh")
	mustEqual(t, changed.Source, "typesafe", "source")
}

// twin: tests/rules.test.ts:448
func TestIsRuleShaped(t *testing.T) {
	mustEqual(t, isRuleShaped(strings.Join([]string{"# Project rules", "", "## No console", "You must not use console.log.", "", "## Use const", "Prefer const over let."}, "\n")), true, "modal")
	mustEqual(t, isRuleShaped(strings.Join([]string{"# My Project", "", "This is a project about building things.", "It does many interesting things.", "", "## Overview", "Here we describe the project."}, "\n")), false, "prose")
	mustEqual(t, isRuleShaped(strings.Join([]string{"# Guide", "", "## Coding style", "- Always use strict mode", "- Never use var"}, "\n")), true, "bullets")
	mustEqual(t, isRuleShaped("Just some prose with no headings at all."), false, "no headings")
}

// twin: tests/rules.test.ts:459
func TestFallbackReadmeProseOnlyVersusAggregate(t *testing.T) {
	store := NewRuleStore()
	dir := t.TempDir()
	prose := strings.Join([]string{"# pi-warden", "", "**Stop babysitting your coding agent.**", "", "pi-warden supervises Pi while it works.", "", "## Install", "", "```bash", "pi install npm:pi-warden", "```"}, "\n")
	writeFile(t, filepath.Join(dir, "README.md"), prose)
	proseSet := store.Load(dir, rulesConfig())
	mustEqual(t, proseSet.ProseOnly, true, "prose README is flagged proseOnly")
	mustEqual(t, proseSet.HasAggregate, false, "prose README has no aggregate")
	mustEqual(t, len(proseSet.Rules), 0, "prose README has no rules")

	dir2 := t.TempDir()
	writeFile(t, filepath.Join(dir2, "README.md"), strings.Join([]string{"# Project rules", "", "## No console", "You must not use console.log.", "", "## Use const", "Prefer const."}, "\n"))
	ruleSet := store.Load(dir2, rulesConfig())
	mustEqual(t, ruleSet.ProseOnly, false, "rule-shaped README is not proseOnly")
	mustEqual(t, ruleSet.HasAggregate && ruleSet.Aggregate != "", true, "rule-shaped README has aggregate")
	mustEqual(t, len(ruleSet.Rules), 0, "rule-shaped README still has no parsed rules (heading level mismatch)")
}

// twin: tests/rules.test.ts:480
func TestProseOnlySetSkipsWithoutAskingTheJudge(t *testing.T) {
	cwd := rulesProject(t)
	set := &RuleSet{Sources: []string{"README.md"}, ProseOnly: true}
	judge := stubJudge(nil, "")
	verdict := evaluateRules("write", writeInput("src/a.ts", "x"), rulesOpts(cwd, rulesConfig(), set, judge))
	mustEqual(t, verdict.Source, "skipped", "source")
	mustMatch(t, verdict.SkippedReason, `no rules found in README\.md \(prose only\)`, "reason")
	mustEqual(t, len(judge.calls()), 0, "judge not called for prose-only set")
}

// twin: tests/rules.test.ts:489
func TestDescribeRuleSetProseOnlyAndAggregate(t *testing.T) {
	mustMatch(t, describeRuleSet(&RuleSet{Sources: []string{"README.md"}, ProseOnly: true}), `no rules found in README\.md \(prose only\)`, "prose")
	mustMatch(t, describeRuleSet(&RuleSet{Sources: []string{"AGENTS.md"}, Aggregate: "Always write tests.", HasAggregate: true}), `no rule headings: judged as one document`, "aggregate")
}

// twin: tests/rules.test.ts:496
func TestNoHeadingsYieldsNoRules(t *testing.T) {
	doc := "Always write tests.\nNever use console.log."
	mustEqual(t, len(parseRules(doc)), 0, "parseRules returns nothing for no headings")
	mustEqual(t, isRuleShaped(doc), false, "prose check says not rule-shaped")
}

// twin: tests/rules.test.ts:503
func TestOutsideProjectTargetIsSkipped(t *testing.T) {
	cwd := rulesProject(t)
	judge := stubJudge(nil, "")
	guard := NewRulesGuard()
	writeFile(t, filepath.Join(cwd, "pi-warden.md"), rulesMD)
	call := RulesCallRef{ID: "o1", Tool: "write", Input: writeInput("/tmp/outside.ts", "console.log(1)")}
	verdict := guard.Inspect(call, []RulesCallRef{call}, rulesOpts(cwd, rulesConfig(), nil, judge)).Wait()
	mustEqual(t, verdict.Source, "skipped", "source")
	mustMatch(t, verdict.SkippedReason, `outside the project`, "reason")
	mustEqual(t, len(verdict.Findings), 0, "findings")
}

// twin: tests/rules.test.ts:515
func TestGitignoredTargetIsSkipped(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init").CombinedOutput(); err != nil {
		t.Skipf("git is not available: %v %s", err, out)
	}
	writeFile(t, filepath.Join(dir, ".gitignore"), ".local/\nbuild/\n")
	writeFile(t, filepath.Join(dir, "pi-warden.md"), "# No console\nDo not use console.log.\n")
	guard := NewRulesGuard()
	call := RulesCallRef{ID: "g1", Tool: "write", Input: writeInput(".local/cache.json", "data")}
	verdict := guard.Inspect(call, []RulesCallRef{call}, rulesOpts(dir, rulesConfig(), nil, stubJudge(nil, ""))).Wait()
	mustEqual(t, verdict.Source, "skipped", "source")
	mustMatch(t, verdict.SkippedReason, `gitignored`, "reason")
	mustEqual(t, len(verdict.Findings), 0, "findings")
}

// twin: tests/rules.test.ts:534
func TestInProjectNonIgnoredTargetIsStillJudged(t *testing.T) {
	cwd := rulesProject(t)
	judge := stubJudge(map[string]float64{"no-console-statements": 0.9}, "")
	guard := NewRulesGuard()
	writeFile(t, filepath.Join(cwd, "pi-warden.md"), rulesMD)
	call := RulesCallRef{ID: "j1", Tool: "write", Input: writeInput("src/a.ts", "console.log(1)")}
	verdict := guard.Inspect(call, []RulesCallRef{call}, rulesOpts(cwd, rulesConfig(), nil, judge)).Wait()
	mustEqual(t, verdict.Source, "typesafe", "source")
	if len(verdict.Findings) == 0 {
		t.Fatal("should have findings for in-project target")
	}
}

// twin: tests/rules.test.ts:545
func TestNoRuleSetMeansGitIgnoredIsNotCalled(t *testing.T) {
	dir := t.TempDir() // no rules file and no git repository: git check-ignore would fail if it were called
	judge := stubJudge(nil, "")
	guard := NewRulesGuard()
	call := RulesCallRef{ID: "n1", Tool: "write", Input: writeInput(".local/cache.json", "data")}
	verdict := guard.Inspect(call, []RulesCallRef{call}, rulesOpts(dir, rulesConfig(func(c *RulesConfig) { c.Fallback = false }), nil, judge)).Wait()
	mustEqual(t, verdict.Source, "skipped", "source")
	mustMatch(t, verdict.SkippedReason, `no rules file`, "should skip for no rules, not gitignored")
	mustEqual(t, len(judge.calls()), 0, "judge should not be called")
}

// The weak-model bench's rules steers: each rule check below reads the state the way the rule question asks, so a
// request that lacks the edited result, or blames an edit for code it kept, fails these tests.
const benchRules = "# No partial implementations\n" +
	"Implement features fully. A comment that says \"for now\", \"simplified\", or \"later\", or a stub body, is a violation.\n" +
	"\n" +
	"# Every exported function documents its return value\n" +
	"An exported function or arrow constant in `src/` carries a JSDoc block with a `@returns` tag directly above it."

var (
	reExportFunction = lazyRE(`export function`)
	reDocumentedExp  = lazyRE(`@returns[^\n]*\n\s*\*/\nexport function`)
	rePartial        = lazyRE(`(?i)for now|simplified|later|TODO`)
)

func undocumentedExport(code string) bool {
	return reExportFunction.MatchString(code) && !reDocumentedExp.MatchString(code)
}
func partial(code string) bool { return rePartial.MatchString(code) }

// introducedJudge flags a rule only when the edit's result breaks it and the pre-edit lines did not, as the rule
// question asks.
func introducedJudge() *recordingJudge {
	checks := map[string]func(string) bool{"no-partial-implementations": partial, "every-exported-function-documents-its-return-value": undocumentedExport}
	return &recordingJudge{handler: func(r Request) (Evaluation, error) {
		answers := map[string]Answer{}
		for id := range r.Questions {
			breaks := checks[strings.TrimPrefix(id, "rule_")]
			if breaks == nil {
				continue
			}
			introduced := false
			if content, ok := r.State["content"].(string); ok {
				introduced = breaks(content)
			} else {
				edits, _ := r.State["edits"].([]any)
				for _, raw := range edits {
					e := raw.(map[string]any)
					after, hasAfter := e["after"].(string)
					if !hasAfter {
						introduced = introduced || breaks(e["newText"].(string))
						continue
					}
					before, _ := e["before"].(string)
					introduced = introduced || (breaks(after) && !breaks(before))
				}
			}
			violation, choice := 0.05, "compliant"
			if introduced {
				violation, choice = 0.9, "violation"
			}
			answers[id] = Answer{Type: "choice", Choice: choice, Confidence: 0.9, Probabilities: map[string]float64{"compliant": 0.95 - violation, "violation": violation, "not_applicable": 0.025, "insufficient_context": 0.025}}
		}
		return Evaluation{Model: "jev-test", ElapsedMs: 7, Answers: answers}, nil
	}}
}

const (
	slugJS    = "/**\n * URL slug for a room or event title.\n * @returns {string} lower-case words joined by single dashes\n */\nexport function slugify(title) {\n  return title.toLowerCase().replace(/[^a-z0-9]+/g, \"-\").replace(/^-|-$/g, \"\");\n}\n"
	cartJS    = "/**\n * Cart total line for the front-desk printout.\n * @returns {string}\n */\nexport function cartLine(cents) {\n  return \"Cart total: $\" + (cents / 100).toFixed(2);\n}\n"
	stylesCSS = "body { margin: 0; font-family: system-ui, sans-serif; color: #222; }\n.site-header { background: #333333; color: #ffffff; padding: 12px 24px; display: flex; justify-content: space-between; }\n.site-header a { color: #dddddd; }\nmain { padding: 24px; }\n"
)

func benchProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "src", "slug.js"), slugJS)
	writeFile(t, filepath.Join(dir, "src", "modules", "cart.js"), cartJS)
	writeFile(t, filepath.Join(dir, "public", "styles.css"), stylesCSS)
	return dir
}

func benchSet() *RuleSet {
	return &RuleSet{Sources: []string{"pi-warden.md"}, Rules: parseRules(benchRules)}
}

func shownEdits(t *testing.T, j *recordingJudge) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, raw := range nth(t, j, 0).State["edits"].([]any) {
		out = append(out, raw.(map[string]any))
	}
	return out
}

// twin: tests/rules.test.ts:615
func TestRuleQuestionsAskWhetherTheChangeIntroducesAViolation(t *testing.T) {
	content := "x"
	question := buildRulesRequest(&RulesTarget{Tool: "write", Path: "src/a.js", Content: &content}, benchSet()).Questions["rule_every-exported-function-documents-its-return-value"]
	mustMatch(t, question.Instructions, `^Does this change to `+"`path`"+` introduce a violation of this one project rule\?`, "frame")
	mustMatch(t, question.Instructions, `Judge the edit by `+"`after`", "after")
	mustMatch(t, question.Instructions, `a violation already in `+"`before`"+` is not introduced by this edit`, "before")
	mustNotContain(t, question.Instructions, "Judge only the newly written content", "old wording")
	mustEqual(t, question.Criteria["violation"], "The change introduces a violation of this rule.", "criteria")
}

// twin: tests/rules.test.ts:624
func TestBenchFalsePositiveBodyEditUnderJSDoc(t *testing.T) {
	dir := benchProject(t)
	judge := introducedJudge()
	oldText := "  return title.toLowerCase().replace(/[^a-z0-9]+/g, \"-\").replace(/^-|-$/g, \"\");"
	newText := "  return title.normalize(\"NFD\").replace(/[\\u0300-\\u036f]/g, \"\").toLowerCase().replace(/[^a-z0-9]+/g, \"-\").replace(/^-|-$/g, \"\");"
	verdict := evaluateRules("edit", editInput("src/slug.js", edit(oldText, newText)), rulesOpts(dir, rulesConfig(), benchSet(), judge))
	mustEqual(t, shownEdits(t, judge)[0]["after"], strings.Replace(slugJS, oldText, newText, 1), "the whole function with its JSDoc, as edited")
	mustEqual(t, verdict.Source, "typesafe", "source")
	mustEqual(t, len(verdict.Findings), 0, "findings")
}

// twin: tests/rules.test.ts:638
func TestBenchFalsePositiveImportAboveDocumentedFunction(t *testing.T) {
	dir := benchProject(t)
	judge := introducedJudge()
	edits := []map[string]any{
		edit("/**\n * Cart total line", "import { money } from \"../format.js\";\n\n/**\n * Cart total line"),
		edit("  return \"Cart total: $\" + (cents / 100).toFixed(2);", "  return \"Cart total: \" + money(cents);"),
	}
	verdict := evaluateRules("edit", editInput("src/modules/cart.js", edits...), rulesOpts(dir, rulesConfig(), benchSet(), judge))
	shown := shownEdits(t, judge)
	mustMatch(t, shown[0]["after"].(string), `^import \{ money \} from "\.\./format\.js";\n\n/\*\*\n \* Cart total line[\s\S]*@returns \{string\}\n \*/\nexport function cartLine`, "first edit")
	mustMatch(t, shown[1]["after"].(string), `@returns \{string\}\n \*/\nexport function cartLine\(cents\) \{\n  return "Cart total: " \+ money\(cents\);`, "second edit")
	mustEqual(t, len(verdict.Findings), 0, "findings")
}

// twin: tests/rules.test.ts:654
func TestBenchFalsePositiveColourChangeIsNotAPartialImplementation(t *testing.T) {
	dir := benchProject(t)
	judge := introducedJudge()
	edits := []map[string]any{edit(".site-header { background: #333333;", ".site-header { background: #1e40af;"), edit(".site-header a { color: #dddddd; }", ".site-header a { color: #bfdbfe; }")}
	verdict := evaluateRules("edit", editInput("public/styles.css", edits...), rulesOpts(dir, rulesConfig(), benchSet(), judge))
	mustMatch(t, shownEdits(t, judge)[0]["after"].(string), `\.site-header \{ background: #1e40af; color: #ffffff;`, "after")
	mustEqual(t, len(verdict.Findings), 0, "findings")
}

// twin: tests/rules.test.ts:665
func TestBenchTruePositiveRemovedReturnsIsStillCaught(t *testing.T) {
	dir := benchProject(t)
	judge := introducedJudge()
	edits := []map[string]any{
		edit("/**\n * URL slug for a room or event title.\n * @returns {string} lower-case words joined by single dashes\n */", "/** URL slug for a room or event title. */"),
		edit("export function slugify(title) {", "export function slugify(title) {\n  if (!title) return \"\";"),
	}
	verdict := evaluateRules("edit", editInput("src/slug.js", edits...), rulesOpts(dir, rulesConfig(), benchSet(), judge))
	mustEqual(t, findingIDs(verdict.Findings), []string{"every-exported-function-documents-its-return-value"}, "findings")
	told := rulesSteer(verdict, nil)
	mustMatch(t, told, `^pi-warden: the content just written to src/slug\.js .*violates project rule: "Every exported function documents its return value" \(0\.90\)`, "steer")
	if regexp.MustCompile(`pi-warden\.md|config|trace|hold|\.pi/`).MatchString(told) {
		t.Fatalf("the steer names more than the rule and the file: %s", told)
	}
}

// twin: tests/rules.test.ts:677
func TestFormatRuleSetDetailsRootRules(t *testing.T) {
	set := &RuleSet{Sources: []string{"pi-warden.md"}, Rules: []Rule{{ID: "no-secrets", Name: "No secrets", Paths: []string{"src/**"}}, {ID: "tests-before-done", Name: "Tests before done"}}}
	mustEqual(t, formatRuleSetDetails(set, "root", nil), strings.Join([]string{"Rules in force: pi-warden.md (2 rules, 0 dropped)", "1. no-secrets paths: src/**", "2. tests-before-done paths: (all)"}, "\n"), "details")
}

// twin: tests/rules.test.ts:693
func TestFormatRuleSetDetailsConfiguredRulesNameEachSource(t *testing.T) {
	set := &RuleSet{Sources: []string{"rules/code.md", "rules/docs.md"}, Rules: []Rule{
		{ID: "typed-code", Name: "Typed code", Paths: []string{"src/**"}, Source: "rules/code.md"},
		{ID: "links-work", Name: "Links work", Paths: []string{"docs/**"}, Source: "rules/docs.md"},
	}}
	text := formatRuleSetDetails(set, "configured", []string{"dist/**", "*.lock"})
	mustMatch(t, text, `^Rules in force: rules\.files: rules/code\.md, rules/docs\.md \(2 rules, 0 dropped\)`, "header")
	mustMatch(t, text, `1\. typed-code \[rules/code\.md\] paths: src/\*\*`, "first")
	mustMatch(t, text, `2\. links-work \[rules/docs\.md\] paths: docs/\*\*`, "second")
	mustMatch(t, text, `Excluded from Jev by rules\.exclude: dist/\*\*, \*\.lock$`, "excluded")
}

// twin: tests/rules.test.ts:709
func TestFormatRuleSetDetailsFallbackAggregate(t *testing.T) {
	set := &RuleSet{Sources: []string{"README.md"}, Aggregate: "Always write tests.", HasAggregate: true}
	mustEqual(t, formatRuleSetDetails(set, "fallback", nil), "Rules in force: README.md judged as one aggregate rule (19 condensed chars)", "details")
}

// twin: tests/rules.test.ts:715
func TestRulesDetailsDisabledGuardLeadsWithTheOffNotice(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pi-warden.md"), "# No console statements\nCode must not contain `console.log`. Use the logger.\n")
	guard := NewRulesGuard()
	off := guard.Details(dir, rulesConfig(func(c *RulesConfig) { c.Enabled = false }))
	on := guard.Details(dir, rulesConfig())
	mustEqual(t, off, "Rules guard is off (rules.enabled: false). These would apply:\n"+on, "off")
	mustEqual(t, on, strings.Join([]string{"Rules in force: pi-warden.md (1 rule, 0 dropped)", "1. no-console-statements paths: (all)"}, "\n"), "on")
}

// twin: tests/rules.test.ts:730
func TestFormatRuleSetDetailsEmptySetUsesTheFirstRunRemedy(t *testing.T) {
	mustEqual(t, formatRuleSetDetails(nil, "none", nil), "No rules file detected. Run /warden init to create project-specific rules.", "details")
}

// twin: tests/rules.test.ts:735
func TestFormatRuleSetDetailsDroppedCountIsAlwaysVisible(t *testing.T) {
	set := &RuleSet{Sources: []string{"pi-warden.md"}, Rules: []Rule{{ID: "first", Name: "First"}}, AlwaysDropped: 7}
	mustMatch(t, formatRuleSetDetails(set, "root", nil), `\(1 rule, 7 dropped\)`, "dropped")
}
