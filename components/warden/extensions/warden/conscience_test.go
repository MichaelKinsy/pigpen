package warden

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Twins of tests/conscience.test.ts: the assessment, its questions and state, the candidate filters, the thresholds
// and the envelope, against a scripted judge.

type csJudge func(ctx context.Context, req Request) (Evaluation, error)

func (f csJudge) Evaluate(ctx context.Context, req Request) (Evaluation, error) { return f(ctx, req) }

func csSkill(name, description string, dmi ...bool) Skill {
	return Skill{Name: name, Description: description, FilePath: "/skills/" + name + "/SKILL.md", DisableModelInvocation: len(dmi) > 0 && dmi[0]}
}

func csConfig(mod ...func(*ConscienceConfig)) ConscienceConfig {
	c := ConscienceConfig{
		Enabled: true, Skills: ConscienceSkillConfig{Mode: "recommend", Exclude: []string{}}, Tools: ConscienceToolConfig{Enabled: true, Exclude: []string{}},
		SkipTools: []string{}, TimeoutMs: 3000, MaxAssessments: 3, MaxNudges: 2, MaxSkillBytes: 32768, MaxLoadedBytes: 65536,
		RecommendThreshold: 0.80, AdvanceThreshold: 0.70,
	}
	for _, m := range mod {
		m(&c)
	}
	return c
}

func csDeps(j Judge, mod ...func(*ConscienceConfig)) ConscienceDeps {
	return ConscienceDeps{Judge: j, Config: csConfig(mod...), SharedTimeoutMs: 5000}
}

func csChoice(label string, pAdvance float64) Answer {
	return Answer{Type: "choice", Choice: label, Confidence: 0.9, Probabilities: map[string]float64{"advance": pAdvance, "awaiting_user": 0, "no_gap": 0, "unclear": 0}}
}

func csScore(level int, probs ...float64) Answer {
	p := map[string]float64{}
	for i, v := range probs {
		p[itoaInt(i)] = v
	}
	return Answer{Type: "score", Score: float64(level), Confidence: 0.8, Probabilities: p}
}

func csFixed(answers map[string]Answer) Judge {
	return csJudge(func(context.Context, Request) (Evaluation, error) {
		return Evaluation{Answers: answers, Model: "jev-1.13.0"}, nil
	})
}

func csDisposition(disp string, pAdvance float64) Judge {
	return csFixed(map[string]Answer{"conscience_disposition": csChoice(disp, pAdvance)})
}

// csSelection answers the disposition and one candidate's score.
func csSelection(disp, id string, level int, probs []float64, pAdvance float64) Judge {
	return csFixed(map[string]Answer{"conscience_disposition": csChoice(disp, pAdvance), id: csScore(level, probs...)})
}

var (
	csDefaultTools  = []CapabilityTool{{"read", "Read file contents"}, {"bash", "Execute bash commands"}, {"edit", "Edit files"}}
	csDefaultSkills = []Skill{csSkill("impeccable", "Frontend interface design, polish, and UX"), csSkill("tdd", "Test-driven development"), csSkill("research", "Research questions against primary sources")}
)

func csAssess(t *testing.T, prompt, recent string, skills []Skill, tools []CapabilityTool, active, supplied []string, deps ConscienceDeps, spine *TaskSpine) AssessmentResult {
	t.Helper()
	return assess(context.Background(), prompt, recent, skills, tools, active, supplied, deps, spine)
}

func csIDs(c []Candidate) []string {
	out := []string{}
	for _, x := range c {
		out = append(out, x.ID)
	}
	return out
}

func csCandidates(t *testing.T, skills []Skill, tools []CapabilityTool, cfg ConscienceConfig, supplied []string) eligibleResult {
	return eligibleCandidates(skills, tools, cfg, nil, supplied, nil, nil, "linux")
}

// twin: tests/conscience.test.ts:120
func TestSanitizeDescriptionStripsAbsolutePaths(t *testing.T) {
	if got := sanitizeDescription("Read /Users/anon/project/file.ts for details"); strings.Contains(got, "/Users/anon") {
		t.Errorf("absolute path should be stripped: %q", got)
	}
}

// twin: tests/conscience.test.ts:126
func TestSanitizeDescriptionStripsHomeDirectoryPaths(t *testing.T) {
	got := sanitizeDescription("Config at ~/config.json and ~bob/.ssh/config")
	if strings.Contains(got, "~/") || strings.Contains(got, "~bob/") {
		t.Errorf("tilde paths should be stripped: %q", got)
	}
}

// twin: tests/conscience.test.ts:133
func TestSanitizeDescriptionStripsURLs(t *testing.T) {
	if got := sanitizeDescription("See https://docs.example.com/guide for more"); strings.Contains(got, "https://") {
		t.Errorf("URL should be stripped: %q", got)
	}
}

// twin: tests/conscience.test.ts:139
func TestSanitizeDescriptionPreservesNormalText(t *testing.T) {
	in := "Test-driven development for TypeScript projects"
	if got := sanitizeDescription(in); got != in {
		t.Errorf("got %q", got)
	}
}

// twin: tests/conscience.test.ts:146
func TestOpaqueIDReturnsC1C2C3(t *testing.T) {
	if opaqueID(0) != "c1" || opaqueID(1) != "c2" || opaqueID(2) != "c3" {
		t.Error("opaque ids")
	}
}

// twin: tests/conscience.test.ts:152
func TestAssignOpaqueIDsMapsCandidatesInStableOrder(t *testing.T) {
	r := assignOpaqueIDs([]Candidate{{Kind: "skill", ID: "impeccable", Description: "UI design"}, {Kind: "tool", ID: "bash", Description: "Run commands"}})
	if r[0].opaqueID != "c1" || r[0].candidate.ID != "impeccable" || r[1].opaqueID != "c2" || r[1].candidate.ID != "bash" {
		t.Errorf("%+v", r)
	}
}

func csBatch(kind string, ids ...string) []batchItem {
	var out []batchItem
	for i, id := range ids {
		out = append(out, batchItem{opaqueID(i), Candidate{Kind: kind, ID: id, Description: "d"}})
	}
	return out
}

// twin: tests/conscience.test.ts:166
func TestBuildBatchQuestionsProducesADispositionChoiceAndOneScorePerCandidate(t *testing.T) {
	batch := []batchItem{{"c1", Candidate{Kind: "skill", ID: "impeccable", Description: "UI design"}}, {"c2", Candidate{Kind: "tool", ID: "bash", Description: "Run commands"}}}
	q, key, idMap := buildBatchQuestions(batch, "test", "", nil, nil)
	if key != "conscience_disposition" {
		t.Errorf("key %q", key)
	}
	for _, id := range []string{"conscience_disposition", "c1", "c2"} {
		if _, ok := q[id]; !ok {
			t.Errorf("missing %s", id)
		}
	}
	if len(q) != 3 || idMap["c1"].ID != "impeccable" || idMap["c2"].ID != "bash" {
		t.Errorf("%d questions, idMap %v", len(q), idMap)
	}
}

// twin: tests/conscience.test.ts:181
func TestDispositionQuestionIsAChoiceTypeWithFourOptions(t *testing.T) {
	q, _, _ := buildBatchQuestions(nil, "test", "", nil, nil)
	d := q["conscience_disposition"]
	if d.Type != "choice" {
		t.Errorf("type %q", d.Type)
	}
	for _, k := range []string{"advance", "awaiting_user", "no_gap", "unclear"} {
		if _, ok := d.Criteria[k]; !ok {
			t.Errorf("missing option %s", k)
		}
	}
}

// twin: tests/conscience.test.ts:191
func TestScoreQuestionsUseTheFourScoreLevelsAsCriteria(t *testing.T) {
	q, _, _ := buildBatchQuestions(csBatch("skill", "tdd"), "test", "", nil, nil)
	c1 := q["c1"]
	if c1.Type != "score" || len(c1.Levels) != len(scoreLevels) || len(scoreLevels) != 4 {
		t.Errorf("%+v", c1)
	}
}

// twin: tests/conscience.test.ts:203
func TestBuildStateProducesStructuredStateWithOpaqueIDs(t *testing.T) {
	batch := []batchItem{{"c1", Candidate{Kind: "skill", ID: "impeccable", Description: "UI design"}}}
	st := buildState("design a UI", "user wants a landing page", []string{"tdd"}, nil, batch, nil)
	if st["task"] != "design a UI" || st["context"] != "user wants a landing page" {
		t.Errorf("%v", st)
	}
	if !reflect.DeepEqual(st["active_skills"], []string{"tdd"}) || !reflect.DeepEqual(st["supplied_skills"], []string{}) {
		t.Errorf("skill lists %v %v", st["active_skills"], st["supplied_skills"])
	}
	c := st["candidates"].(map[string]any)["c1"].(map[string]any)
	if c["kind"] != "skill" || c["name"] != "impeccable" || c["description"] != "UI design" {
		t.Errorf("%v", c)
	}
}

// twin: tests/conscience.test.ts:220
func TestQuestionHashIsDeterministicAndChangesWithDifferentQuestions(t *testing.T) {
	mk := func(v string) Questions { return Questions{"q1": Choice("", map[string]string{"a": v})} }
	h1, h2, h3 := questionHash(mk("1")), questionHash(mk("1")), questionHash(mk("2"))
	if h1 != h2 || h1 == h3 || len(h1) != 16 {
		t.Errorf("%s %s %s", h1, h2, h3)
	}
}

// twin: tests/conscience.test.ts:231
func TestEligibleCandidatesExcludesDisableModelInvocationSkills(t *testing.T) {
	r := csCandidates(t, []Skill{csSkill("impeccable", "UI design"), csSkill("wizard", "Step-by-step wizard", true)}, csDefaultTools, csConfig(), nil)
	ids := csIDs(r.Candidates)
	if !containsString(ids, "impeccable") || containsString(ids, "wizard") {
		t.Errorf("%v", ids)
	}
}

// twin: tests/conscience.test.ts:242
func TestEligibleCandidatesExcludesByNamePattern(t *testing.T) {
	cfg := csConfig(func(c *ConscienceConfig) { c.Skills.Exclude = []string{"impeccable"} })
	ids := csIDs(csCandidates(t, csDefaultSkills, csDefaultTools, cfg, nil).Candidates)
	if containsString(ids, "impeccable") || !containsString(ids, "tdd") {
		t.Errorf("%v", ids)
	}
}

// twin: tests/conscience.test.ts:250
func TestEligibleCandidatesExcludesByWildcardPattern(t *testing.T) {
	cfg := csConfig(func(c *ConscienceConfig) { c.Tools.Exclude = []string{"bas*"} })
	ids := csIDs(csCandidates(t, nil, csDefaultTools, cfg, nil).Candidates)
	if containsString(ids, "bash") || !containsString(ids, "read") {
		t.Errorf("%v", ids)
	}
}

// twin: tests/conscience.test.ts:258
func TestEligibleCandidatesSkipsAlreadySuppliedSkills(t *testing.T) {
	ids := csIDs(csCandidates(t, csDefaultSkills, csDefaultTools, csConfig(), []string{"impeccable"}).Candidates)
	if containsString(ids, "impeccable") {
		t.Errorf("%v", ids)
	}
}

// twin: tests/conscience.test.ts:264
func TestEligibleCandidatesReturnsEmptyWhenBothCategoriesDisabled(t *testing.T) {
	cfg := csConfig(func(c *ConscienceConfig) { c.Skills.Mode = "off"; c.Tools.Enabled = false })
	if n := len(csCandidates(t, csDefaultSkills, csDefaultTools, cfg, nil).Candidates); n != 0 {
		t.Errorf("%d candidates", n)
	}
}

// twin: tests/conscience.test.ts:270
func TestEligibleCandidatesReturnsToolsOnlyWhenSkillsModeIsOff(t *testing.T) {
	cfg := csConfig(func(c *ConscienceConfig) { c.Skills.Mode = "off" })
	got := csCandidates(t, csDefaultSkills, csDefaultTools, cfg, nil).Candidates
	if len(got) == 0 {
		t.Fatal("no candidates")
	}
	for _, c := range got {
		if c.Kind != "tool" {
			t.Errorf("%v", c)
		}
	}
}

func csManySkills(n int) []Skill {
	out := make([]Skill, n)
	for i := range out {
		out[i] = csSkill(fmt.Sprintf("skill-%d", i), fmt.Sprintf("Skill %d", i))
	}
	return out
}

// twin: tests/conscience.test.ts:277
func TestEligibleCandidatesCapsAt256PerCategoryAndSetsOverflowFlag(t *testing.T) {
	r := csCandidates(t, csManySkills(300), nil, csConfig(), nil)
	if len(r.Candidates) != 256 || !r.SkillOverflow {
		t.Errorf("%d candidates, overflow %v", len(r.Candidates), r.SkillOverflow)
	}
}

// csRecording answers no_gap and records every candidate name each request carries.
func csRecording() (Judge, func() [][]string) {
	var mu sync.Mutex
	var requests [][]string
	j := csJudge(func(_ context.Context, req Request) (Evaluation, error) {
		var names []string
		for _, c := range req.State["candidates"].(map[string]any) {
			m := c.(map[string]any)
			names = append(names, m["kind"].(string)+":"+m["name"].(string))
		}
		mu.Lock()
		requests = append(requests, names)
		mu.Unlock()
		return Evaluation{Answers: map[string]Answer{"conscience_disposition": {Type: "choice", Choice: "no_gap", Confidence: 1, Probabilities: map[string]float64{"no_gap": 1}}}}, nil
	})
	return j, func() [][]string { mu.Lock(); defer mu.Unlock(); return requests }
}

func csFlat(requests [][]string) []string {
	var out []string
	for _, r := range requests {
		out = append(out, r...)
	}
	sort.Strings(out)
	return out
}

var csMixed = struct {
	tools  []CapabilityTool
	skills []Skill
}{
	tools:  []CapabilityTool{{"read", "Read file contents"}, {"bash", "Execute bash commands"}, {"search_code", "Search the code index for literal strings"}},
	skills: []Skill{csSkill("tdd", "Test-driven development")},
}

// twin: tests/conscience.test.ts:307
func TestDefaultSkipToolsOnlyTheCustomToolAndTheSkillReachTheRequest(t *testing.T) {
	j, reqs := csRecording()
	csAssess(t, "add a test", "", csMixed.skills, csMixed.tools, nil, nil, csDeps(j, func(c *ConscienceConfig) { c.SkipTools = defaultConscienceConfig().SkipTools }), nil)
	if got := csFlat(reqs()); !reflect.DeepEqual(got, []string{"skill:tdd", "tool:search_code"}) {
		t.Errorf("%v", got)
	}
}

// twin: tests/conscience.test.ts:313
func TestSkipToolsEmptyRestoresEveryToolInTheRequest(t *testing.T) {
	j, reqs := csRecording()
	csAssess(t, "add a test", "", csMixed.skills, csMixed.tools, nil, nil, csDeps(j, func(c *ConscienceConfig) { c.SkipTools = []string{} }), nil)
	if got := csFlat(reqs()); !reflect.DeepEqual(got, []string{"skill:tdd", "tool:bash", "tool:read", "tool:search_code"}) {
		t.Errorf("%v", got)
	}
}

// twin: tests/conscience.test.ts:319
func TestEligibleCandidatesWithEmptySkipToolsKeepsEveryCoreTool(t *testing.T) {
	r := csCandidates(t, nil, csDefaultTools, csConfig(func(c *ConscienceConfig) { c.SkipTools = []string{} }), nil)
	if got := csIDs(r.Candidates); !reflect.DeepEqual(got, []string{"read", "bash", "edit"}) {
		t.Errorf("%v", got)
	}
}

// twin: tests/conscience.test.ts:324
func TestEligibleCandidatesNeverDropsASkillBySkipTools(t *testing.T) {
	r := csCandidates(t, []Skill{csSkill("bash", "A skill that shares a core tool name")}, nil, csConfig(func(c *ConscienceConfig) { c.SkipTools = []string{"bash"} }), nil)
	if len(r.Candidates) != 1 || r.Candidates[0].Kind != "skill" || r.Candidates[0].ID != "bash" {
		t.Errorf("%+v", r.Candidates)
	}
}

// twin: tests/conscience.test.ts:329
func TestEligibleCandidatesNeverOffersADestructiveTool(t *testing.T) {
	tools := []CapabilityTool{
		{"delete_project", "Remove a project's data from the index"}, {"db-drop", "Database helper"}, {"cache", "Purge the cache for a key"},
		{"Reset", "Session helper"}, {"list_projects", "List indexed projects"}, {"edit_text", "Edit files; can remove or replace text"},
		{"dropdown_state", "Read the dropdown state"},
	}
	got := csIDs(csCandidates(t, nil, tools, csConfig(), nil).Candidates)
	if !reflect.DeepEqual(got, []string{"list_projects", "edit_text", "dropdown_state"}) {
		t.Errorf("%v", got)
	}
}

// twin: tests/conscience.test.ts:343
func TestEligibleCandidatesDropsPowershellOnDarwinAndLinuxAndKeepsItOnWin32(t *testing.T) {
	tools := []CapabilityTool{{"powershell", "Run PowerShell commands"}, {"list_projects", "List indexed projects"}}
	ids := func(platform string) []string {
		return csIDs(eligibleCandidates(nil, tools, csConfig(), nil, nil, nil, nil, platform).Candidates)
	}
	if !reflect.DeepEqual(ids("darwin"), []string{"list_projects"}) || !reflect.DeepEqual(ids("linux"), []string{"list_projects"}) || !reflect.DeepEqual(ids("win32"), []string{"powershell", "list_projects"}) {
		t.Errorf("%v %v %v", ids("darwin"), ids("linux"), ids("win32"))
	}
}

// twin: tests/conscience.test.ts:355
func TestEligibleCandidatesNeverDropsASkillByThePlatformRule(t *testing.T) {
	r := eligibleCandidates([]Skill{csSkill("powershell-scripting", "Write PowerShell scripts. Windows-only.")}, nil, csConfig(), nil, nil, nil, nil, "darwin")
	if len(r.Candidates) != 1 || r.Candidates[0].ID != "powershell-scripting" {
		t.Errorf("%+v", r.Candidates)
	}
}

// twin: tests/conscience.test.ts:361
func TestIsPlatformIneligibleToolReadsTheNameListAndOnlyDescriptions(t *testing.T) {
	cases := []struct {
		name, platform string
		desc           []string
		want           bool
	}{
		{"pwsh", "linux", nil, true}, {"cmd", "darwin", nil, true}, {"cmd", "win32", nil, false}, {"powershell-helper", "darwin", nil, false},
		{"registry", "linux", []string{"Edit the registry (Windows-only)"}, true}, {"registry", "win32", []string{"Edit the registry (Windows-only)"}, false},
		{"keychain", "linux", []string{"Read the keychain. Only on macOS."}, true}, {"keychain", "darwin", []string{"Read the keychain. macOS only."}, false},
		{"wsl", "linux", []string{"Run commands in WSL on Windows"}, false},
	}
	for _, c := range cases {
		if got := isPlatformIneligibleTool(c.name, c.platform, c.desc...); got != c.want {
			t.Errorf("%s on %s %v: got %v want %v", c.name, c.platform, c.desc, got, c.want)
		}
	}
}

// twin: tests/conscience.test.ts:373
func TestIsDestructiveToolMatchesWholeWordsAndUnderscoreSeparatedPartsOnly(t *testing.T) {
	cases := []struct {
		name string
		desc []string
		want bool
	}{
		{"delete_project", nil, true}, {"truncate", nil, true}, {"wipe disk", nil, true}, {"resetter", nil, false},
		{"list_projects", []string{"Delete nothing; list projects"}, true}, {"list_projects", []string{"List projects; never delete"}, false},
	}
	for _, c := range cases {
		if got := isDestructiveTool(c.name, c.desc...); got != c.want {
			t.Errorf("%s %v: got %v", c.name, c.desc, got)
		}
	}
}

// twin: tests/conscience.test.ts:382
func TestEligibleCandidatesDropsCamelCaseAndKillForceUninstallRevokeEraseClearTools(t *testing.T) {
	tools := []CapabilityTool{
		{"deleteIssue", "Issue helper"}, {"dropTable", "Table helper"}, {"mcp__db__truncateTable", "Table helper"}, {"kill_process", "Process helper"},
		{"force_push", "Git helper"}, {"uninstall_package", "Package helper"}, {"revokeToken", "Token helper"}, {"disk", "Erase a disk"},
		{"cache", "Clear the cache"}, {"getIssue", "Read an issue"}, {"HTMLParser", "Parse HTML"}, {"cleanupNotes", "Tidy notes"},
	}
	got := csIDs(csCandidates(t, nil, tools, csConfig(), nil).Candidates)
	if !reflect.DeepEqual(got, []string{"getIssue", "HTMLParser", "cleanupNotes"}) {
		t.Errorf("%v", got)
	}
}

// twin: tests/conscience.test.ts:401
func TestAssessNeverRecommendsASkippedCoreToolEvenWhenTheJudgeRatesItHighest(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	j := csJudge(func(_ context.Context, req Request) (Evaluation, error) {
		answers := map[string]Answer{"conscience_disposition": csChoice("advance", 1)}
		for id, c := range req.State["candidates"].(map[string]any) {
			name := c.(map[string]any)["name"].(string)
			mu.Lock()
			seen = append(seen, name)
			mu.Unlock()
			if name == "bash" {
				answers[id] = csScore(3, 0, 0, 0, 1)
			} else {
				answers[id] = csScore(3, 0, 0, 0.1, 0.85)
			}
		}
		return Evaluation{Answers: answers}, nil
	})
	tools := []CapabilityTool{{"bash", "Execute bash commands"}, {"search_code", "Search the code index"}}
	r := csAssess(t, "find where the steer text is built", "", nil, tools, nil, nil, csDeps(j, func(c *ConscienceConfig) { c.SkipTools = defaultConscienceConfig().SkipTools }), nil)
	if containsString(seen, "bash") {
		t.Error("a skipped core tool never reaches the request")
	}
	if r.Selected == nil || r.Selected.ID != "search_code" {
		t.Errorf("%+v", r.Selected)
	}
}

// twin: tests/conscience.test.ts:427
func TestAssessParsesChoiceAnswerUsingTheChoiceField(t *testing.T) {
	j := csFixed(map[string]Answer{
		"conscience_disposition": {Type: "choice", Choice: "advance", Confidence: 0.95, Probabilities: map[string]float64{"advance": 0.95, "awaiting_user": 0.03, "no_gap": 0.01, "unclear": 0.01}},
		"c1":                     csScore(3, 0.05, 0.1, 0.6, 0.25),
	})
	r := csAssess(t, "design a UI", "", csDefaultSkills, csDefaultTools, nil, nil, csDeps(j, func(c *ConscienceConfig) { c.RecommendThreshold = 0.5 }), nil)
	if r.Disposition != "advance" || r.Selected == nil || r.Selected.ID != "impeccable" {
		t.Errorf("%+v", r)
	}
}

// twin: tests/conscience.test.ts:453
func TestAssessReturnsUnclearWhenChoiceAnswerHasNoChoiceField(t *testing.T) {
	j := csFixed(map[string]Answer{"conscience_disposition": {Type: "choice", Confidence: 0.5, Probabilities: map[string]float64{}}})
	if r := csAssess(t, "test", "", csDefaultSkills, csDefaultTools, nil, nil, csDeps(j), nil); r.Disposition != "unclear" {
		t.Errorf("%q", r.Disposition)
	}
}

func nearTo(a, b float64) bool { return math.Abs(a-b) < 0.001 }

// twin: tests/conscience.test.ts:466
func TestPUsefulNowSumsTheLastTwoScoreLevels(t *testing.T) {
	j := csSelection("advance", "c1", 3, []float64{0.05, 0.1, 0.6, 0.25}, 1)
	r := csAssess(t, "design a UI", "", csDefaultSkills, csDefaultTools, nil, nil, csDeps(j, func(c *ConscienceConfig) { c.RecommendThreshold = 0.5 }), nil)
	if r.Selected == nil || r.Selected.ID != "impeccable" || !nearTo(r.Usefulness, 0.85) {
		t.Errorf("%+v %v", r.Selected, r.Usefulness)
	}
}

// twin: tests/conscience.test.ts:477
func TestPUsefulNowIsLowWhenOnlyLevelOneHasHighProbability(t *testing.T) {
	j := csSelection("advance", "c1", 1, []float64{0.1, 0.7, 0.1, 0.05}, 1)
	r := csAssess(t, "test", "", csDefaultSkills, csDefaultTools, nil, nil, csDeps(j, func(c *ConscienceConfig) { c.RecommendThreshold = 0.5 }), nil)
	if !nearTo(r.Usefulness, 0.15) {
		t.Errorf("%v", r.Usefulness)
	}
}

// twin: tests/conscience.test.ts:489
func TestAssessReturnsBelowThresholdWhenUsefulnessIsBelowRecommendThreshold(t *testing.T) {
	j := csSelection("advance", "c1", 2, []float64{0.05, 0.15, 0.6, 0.2}, 1)
	r := csAssess(t, "test", "", csDefaultSkills, csDefaultTools, nil, nil, csDeps(j, func(c *ConscienceConfig) { c.RecommendThreshold = 0.9 }), nil)
	if r.SkipReason != "below_threshold" || r.Selected != nil {
		t.Errorf("%+v", r)
	}
}

// twin: tests/conscience.test.ts:500
func TestAssessSelectsWhenUsefulnessMeetsRecommendThreshold(t *testing.T) {
	j := csSelection("advance", "c1", 3, []float64{0.05, 0.05, 0.65, 0.25}, 1)
	r := csAssess(t, "test", "", csDefaultSkills, csDefaultTools, nil, nil, csDeps(j, func(c *ConscienceConfig) { c.RecommendThreshold = 0.9 }), nil)
	if r.Selected == nil || r.Selected.ID != "impeccable" || r.Usefulness < 0.9 {
		t.Errorf("%+v %v", r.Selected, r.Usefulness)
	}
}

// twin: tests/conscience.test.ts:511
func TestAssessReturnsBelowThresholdWhenPAdvanceIsBelowRecommendThreshold(t *testing.T) {
	j := csSelection("advance", "c1", 3, []float64{0.05, 0.05, 0.65, 0.25}, 0.5)
	r := csAssess(t, "test", "", csDefaultSkills, csDefaultTools, nil, nil, csDeps(j, func(c *ConscienceConfig) { c.RecommendThreshold = 0.9 }), nil)
	if r.SkipReason != "below_threshold" || r.Selected != nil || r.PAdvance != 0.5 {
		t.Errorf("%+v", r)
	}
}

// twin: tests/conscience.test.ts:524
func TestAssessSendsStateWithOpaqueIDsNotCandidateNamesInInstructions(t *testing.T) {
	var mu sync.Mutex
	var state map[string]any
	var questions Questions
	j := csJudge(func(_ context.Context, req Request) (Evaluation, error) {
		mu.Lock()
		state, questions = req.State, req.Questions
		mu.Unlock()
		return Evaluation{Answers: map[string]Answer{"conscience_disposition": {Type: "choice", Choice: "no_gap", Confidence: 0.9, Probabilities: map[string]float64{"no_gap": 0.9, "unclear": 0.1}}}}, nil
	})
	csAssess(t, "design a UI", "user wants a landing page", csDefaultSkills, csDefaultTools, []string{"tdd"}, []string{"impeccable"}, csDeps(j), nil)
	if state["task"] != "design a UI" || state["context"] != "user wants a landing page" ||
		!reflect.DeepEqual(state["active_skills"], []string{"tdd"}) || !reflect.DeepEqual(state["supplied_skills"], []string{"impeccable"}) {
		t.Errorf("%v", state)
	}
	cands := state["candidates"].(map[string]any)
	if len(cands) == 0 {
		t.Fatal("no candidates")
	}
	for key, val := range cands {
		if !reCandidateID.MatchString(key) {
			t.Errorf("key %s should be opaque", key)
		}
		c := val.(map[string]any)
		if c["kind"] == nil || c["name"] == nil {
			t.Errorf("candidate %v lacks kind or name", c)
		}
	}
	if _, ok := questions["conscience_disposition"]; !ok {
		t.Error("no disposition question")
	}
	for key := range questions {
		if key != "conscience_disposition" && !reCandidateID.MatchString(key) {
			t.Errorf("question key %s should be opaque", key)
		}
	}
}

// csAnswerAll answers the disposition advance and every score question with probs.
func csAnswerAll(count *atomic.Int64, maxQ *atomic.Int64, probs ...float64) Judge {
	return csJudge(func(_ context.Context, req Request) (Evaluation, error) {
		count.Add(1)
		if maxQ != nil {
			for {
				cur := maxQ.Load()
				if int64(len(req.Questions)) <= cur || maxQ.CompareAndSwap(cur, int64(len(req.Questions))) {
					break
				}
			}
		}
		answers := map[string]Answer{"conscience_disposition": csChoice("advance", 0.9)}
		for key := range req.Questions {
			if key != "conscience_disposition" {
				answers[key] = csScore(2, probs...)
			}
		}
		return Evaluation{Answers: answers}, nil
	})
}

// twin: tests/conscience.test.ts:571
func TestAssessPacks40CandidatesIntoTwoRequests(t *testing.T) {
	var n atomic.Int64
	r := csAssess(t, "test", "", csManySkills(40), nil, nil, nil, csDeps(csAnswerAll(&n, nil, 0.1, 0.2, 0.5, 0.2)), nil)
	if n.Load() != 2 || r.RequestCount < 2 {
		t.Errorf("%d requests, result %d", n.Load(), r.RequestCount)
	}
}

// twin: tests/conscience.test.ts:604
func TestThreeHundredSkillsYieldsCatalogLimitAndNoSelectionFromPartialList(t *testing.T) {
	var n atomic.Int64
	r := csAssess(t, "test", "", csManySkills(300), nil, nil, nil, csDeps(csAnswerAll(&n, nil, 0.1, 0.2, 0.5, 0.2), func(c *ConscienceConfig) { c.RecommendThreshold = 0.5 }), nil)
	if r.SkipReason != "catalog_limit" {
		t.Errorf("got %q", r.SkipReason)
	}
}

// twin: tests/conscience.test.ts:637
func TestAssessUsesTheSmallerOfTheConscienceAndSharedTimeoutsForTheDeadline(t *testing.T) {
	j := csDisposition("no_gap", 0.9)
	fixed := time.Unix(1000, 0)
	deps := ConscienceDeps{Judge: j, Config: csConfig(func(c *ConscienceConfig) { c.TimeoutMs = 10000 }), SharedTimeoutMs: 200, Now: func() time.Time { return fixed }}
	if r := csAssess(t, "test", "", csDefaultSkills, csDefaultTools, nil, nil, deps, nil); r.SkipReason == SkipError {
		t.Errorf("the instant judge should complete: %+v", r)
	}
}

// twin: tests/conscience.test.ts:665
func TestAssessReturnsDisabledWhenConfigEnabledIsFalse(t *testing.T) {
	r := csAssess(t, "test", "", nil, nil, nil, nil, csDeps(csDisposition("advance", 1), func(c *ConscienceConfig) { c.Enabled = false }), nil)
	if r.SkipReason != "disabled" || r.Selected != nil {
		t.Errorf("%+v", r)
	}
}

// twin: tests/conscience.test.ts:671
func TestAssessReturnsNoConsentWhenJudgeIsNil(t *testing.T) {
	if r := csAssess(t, "test", "", nil, nil, nil, nil, csDeps(nil), nil); r.SkipReason != "no_consent" {
		t.Errorf("%+v", r)
	}
}

// twin: tests/conscience.test.ts:676
func TestAssessNamesWhyNoJudgeWasAvailableWhenTheCallerSays(t *testing.T) {
	for _, reason := range []string{"no_consent", "no_key", "key_rejected", "budget"} {
		deps := csDeps(nil)
		deps.JudgmentsOff = reason
		r := csAssess(t, "test", "", nil, nil, nil, nil, deps, nil)
		if r.SkipReason != reason || r.RequestCount != 0 {
			t.Errorf("%s: %+v", reason, r)
		}
	}
}

// twin: tests/conscience.test.ts:684
func TestAssessReturnsNoMatchWhenNoEligibleCandidates(t *testing.T) {
	if r := csAssess(t, "test", "", nil, nil, nil, nil, csDeps(csDisposition("advance", 1)), nil); r.SkipReason != "no_match" {
		t.Errorf("%+v", r)
	}
}

// twin: tests/conscience.test.ts:689
func TestAssessReturnsAwaitingUserWhenDispositionIsAwaitingUser(t *testing.T) {
	r := csAssess(t, "test", "", csDefaultSkills, csDefaultTools, nil, nil, csDeps(csDisposition("awaiting_user", 1)), nil)
	if r.Disposition != "awaiting_user" || r.Selected != nil || r.SkipReason != "awaiting_user" {
		t.Errorf("%+v", r)
	}
}

// twin: tests/conscience.test.ts:696
func TestAssessReturnsNoGapWhenDispositionIsNoGap(t *testing.T) {
	r := csAssess(t, "test", "", csDefaultSkills, csDefaultTools, nil, nil, csDeps(csDisposition("no_gap", 1)), nil)
	if r.Disposition != "no_gap" || r.Selected != nil {
		t.Errorf("%+v", r)
	}
}

// twin: tests/conscience.test.ts:702
func TestAssessHandlesJudgeTimeoutGracefully(t *testing.T) {
	// The judge ignores its context, as a hung client would: the deadline still ends the assessment.
	slow := csJudge(func(context.Context, Request) (Evaluation, error) {
		time.Sleep(5 * time.Second)
		return Evaluation{}, nil
	})
	deps := ConscienceDeps{Judge: slow, Config: csConfig(func(c *ConscienceConfig) { c.TimeoutMs = 10 }), SharedTimeoutMs: 5000}
	start := time.Now()
	r := csAssess(t, "test", "", csDefaultSkills, csDefaultTools, nil, nil, deps, nil)
	if r.SkipReason != "error" || r.ErrorCategory != "timeout" {
		t.Errorf("should return error on timeout: %+v", r)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("took %v", time.Since(start))
	}
}

// twin: tests/conscience.test.ts:715
func TestAssessHandlesJudgeThrowingGracefully(t *testing.T) {
	j := csJudge(func(context.Context, Request) (Evaluation, error) { return Evaluation{}, fmt.Errorf("network error") })
	r := csAssess(t, "test", "", csDefaultSkills, csDefaultTools, nil, nil, csDeps(j), nil)
	if r.SkipReason != "error" || r.ErrorCategory != "network" {
		t.Errorf("%+v", r)
	}
}

// twin: tests/conscience.test.ts:725
func TestAssessRanksByUsefulnessProbabilityThenScoreLevelThenSkillBeforeTool(t *testing.T) {
	var n atomic.Int64
	r := csAssess(t, "test", "", csDefaultSkills, csDefaultTools, nil, nil, csDeps(csAnswerAll(&n, nil, 0.1, 0.1, 0.4, 0.4), func(c *ConscienceConfig) { c.RecommendThreshold = 0.5 }), nil)
	if r.Selected == nil || r.Selected.Kind != "skill" {
		t.Errorf("skill should rank before tool when usefulness is equal: %+v", r.Selected)
	}
}

// twin: tests/conscience.test.ts:755
func TestConcurrentBatchesThreeBatchesCompleteInAboutOneRoundTripWith400msDelayedJudge(t *testing.T) {
	var calls atomic.Int64
	j := csJudge(func(ctx context.Context, _ Request) (Evaluation, error) {
		calls.Add(1)
		select {
		case <-time.After(400 * time.Millisecond):
		case <-ctx.Done():
		}
		return Evaluation{Answers: map[string]Answer{"conscience_disposition": csChoice("advance", 0.9)}}, nil
	})
	start := time.Now()
	csAssess(t, "test", "", csManySkills(76), nil, nil, nil, ConscienceDeps{Judge: j, Config: csConfig(), SharedTimeoutMs: 5000}, nil)
	if elapsed := time.Since(start); elapsed >= 2*time.Second {
		t.Errorf("3 concurrent 400ms batches should finish in ~400ms, took %v", elapsed)
	}
	if calls.Load() < 3 {
		t.Errorf("should issue 3 batches, got %d calls", calls.Load())
	}
}

// twin: tests/conscience.test.ts:785
func TestConscienceConfigDefaultsRecommendModeToolsEnabledThresholds(t *testing.T) {
	c := csConfig()
	if c.Skills.Mode != "recommend" || !c.Tools.Enabled || !c.Enabled || c.TimeoutMs != 3000 || c.MaxAssessments != 3 || c.MaxNudges != 2 ||
		c.MaxSkillBytes != 32768 || c.MaxLoadedBytes != 65536 || c.RecommendThreshold != 0.80 || c.AdvanceThreshold != 0.70 {
		t.Errorf("%+v", c)
	}
	// conscience.loadThreshold was removed: nothing read it.
	if _, ok := reflect.TypeOf(c).FieldByName("LoadThreshold"); ok {
		t.Error("ConscienceConfig has a LoadThreshold field")
	}
}

// twin: tests/conscience.test.ts:805
func TestOneCandidateQuestionSetHashesToTheBetaPolicyQuestionHash(t *testing.T) {
	q, _, _ := buildBatchQuestions([]batchItem{{"c1", Candidate{Kind: "skill", ID: "beta-shape", Description: "beta batch shape"}}}, "task", "", nil, nil)
	if got := questionHash(q); got != conscienceBetaPolicy.QuestionHash {
		t.Errorf("question wording changed; re-measure the policy before shipping: %s", got)
	}
}

// twin: tests/conscience.test.ts:812
func TestQuestionHashIsIndependentOfBatchShape(t *testing.T) {
	mixed := append(csBatch("skill", "skill-0", "skill-1"), csBatch("tool", "tool-0", "tool-1")...)
	for i := range mixed {
		mixed[i].opaqueID = opaqueID(i)
	}
	for _, batch := range [][]batchItem{csBatch("skill", "a"), csBatch("skill", "a", "b", "c", "d"), csBatch("tool", "a"), csBatch("tool", "a", "b", "c", "d"), mixed} {
		q, _, _ := buildBatchQuestions(batch, "task", "", nil, nil)
		if got := questionHash(q); got != conscienceBetaPolicy.QuestionHash {
			t.Errorf("batch of %d %ss must hash to the policy hash, got %s", len(batch), batch[0].candidate.Kind, got)
		}
	}
}

// twin: tests/conscience.test.ts:823
func TestTheConcreteMeasuredWordingStillHashesToFb2d35042f667b3c(t *testing.T) {
	q, _, _ := buildBatchQuestions([]batchItem{{"c1", Candidate{Kind: "skill", ID: "any", Description: "any"}}}, "task", "", nil, nil)
	if got := questionHash(q); got != "fb2d35042f667b3c" {
		t.Errorf("%s", got)
	}
}

// twin: tests/conscience.test.ts:829
func TestAssessReturnsBelowThresholdWhenPAdvanceIsBelowAdvanceThreshold(t *testing.T) {
	j := csSelection("advance", "c1", 3, []float64{0.05, 0.05, 0.65, 0.25}, 0.5)
	r := csAssess(t, "test", "", csDefaultSkills, csDefaultTools, nil, nil, csDeps(j, func(c *ConscienceConfig) { c.RecommendThreshold = 0.9; c.AdvanceThreshold = 0.7 }), nil)
	if r.SkipReason != "below_threshold" || r.Selected != nil || r.PAdvance != 0.5 {
		t.Errorf("%+v", r)
	}
}

// twin: tests/conscience.test.ts:841
func TestAssessSelectsWhenBothUsefulnessAndPAdvancePassTheirThresholds(t *testing.T) {
	j := csSelection("advance", "c1", 3, []float64{0.05, 0.05, 0.65, 0.25}, 0.8)
	r := csAssess(t, "test", "", csDefaultSkills, csDefaultTools, nil, nil, csDeps(j, func(c *ConscienceConfig) { c.RecommendThreshold = 0.9; c.AdvanceThreshold = 0.7 }), nil)
	if r.Selected == nil || r.Selected.ID != "impeccable" || r.Usefulness < 0.9 || r.PAdvance < 0.7 {
		t.Errorf("%+v", r)
	}
}

// twin: tests/conscience.test.ts:855
func TestBuildStateCarriesTheSpineBesideTaskAndDoesNotMoveThePolicyHash(t *testing.T) {
	batch := []batchItem{{"c1", Candidate{Kind: "skill", ID: "impeccable", Description: "UI design"}}}
	spine := &TaskSpine{Goal: "design a landing page", Task: "now the tests", History: []string{"add the contact form"}}
	st := buildState("now the tests", "", nil, nil, batch, spine)
	want := map[string]any{"goal": "design a landing page", "task_history": []string{"add the contact form"}}
	if !reflect.DeepEqual(st["spine"], want) || st["task"] != "now the tests" {
		t.Errorf("%v", st["spine"])
	}
	q, _, _ := buildBatchQuestions(batch, "now the tests", "", nil, nil)
	if questionHash(q) != conscienceBetaPolicy.QuestionHash {
		t.Error("the spine is a state field, not a question change: the policy pin still matches")
	}
}

// twin: tests/conscience.test.ts:867
func TestAssessForwardsTheSpineIntoTheJudgeRequestState(t *testing.T) {
	var mu sync.Mutex
	var captured map[string]any
	j := csJudge(func(_ context.Context, req Request) (Evaluation, error) {
		mu.Lock()
		captured = req.State
		mu.Unlock()
		return Evaluation{Answers: map[string]Answer{"conscience_disposition": {Type: "choice", Choice: "no_gap", Confidence: 0.9, Probabilities: map[string]float64{"no_gap": 0.9}}}}, nil
	})
	spine := &TaskSpine{Goal: "design a landing page", Task: "design a UI", History: nil}
	csAssess(t, "design a UI", "", csDefaultSkills, csDefaultTools, nil, nil, csDeps(j), spine)
	if !reflect.DeepEqual(captured["spine"], map[string]any{"goal": "design a landing page", "task_history": []string{}}) || captured["task"] != "design a UI" {
		t.Errorf("%v", captured)
	}
}

// twin: tests/conscience.test.ts:885
func TestBuildStateCapsAHandBuiltSpineWith50HistoryEntriesLikeTheActionPath(t *testing.T) {
	batch := []batchItem{{"c1", Candidate{Kind: "skill", ID: "impeccable", Description: "UI design"}}}
	history := make([]string, 50)
	for i := range history {
		history[i] = fmt.Sprintf("%d", i) + strings.Repeat("h", 2000-len(fmt.Sprintf("%d", i)))
	}
	spine := &TaskSpine{Goal: strings.Repeat("g", 5000), Task: "t", History: history}
	st := buildState("t", "", nil, nil, batch, spine)["spine"].(map[string]any)
	th := st["task_history"].([]string)
	if len(th) != 4 || !strings.HasPrefix(th[0], "0") {
		t.Fatalf("history count capped at SPINE_HISTORY_TURNS, newest kept: %d", len(th))
	}
	for _, turn := range th {
		if utf16Len(turn) != 750 {
			t.Errorf("each entry capped at SPINE_HISTORY_LIMIT: %d", utf16Len(turn))
		}
	}
	if utf16Len(st["goal"].(string)) != 1200 {
		t.Errorf("goal capped at SPINE_GOAL_LIMIT: %d", utf16Len(st["goal"].(string)))
	}
}
