package warden

import (
	"math/rand/v2"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// The cached rule questions and the single-pass rule scoping against the code they replace, over generated rule sets:
// bodies past the cut, empty and Unicode names and bodies, repeated ids with different content, turn rules, scoped and
// unscoped rules, and more distinct rules than the cache holds.

func scopedRulesRef(set *RuleSet, path string) []Rule {
	var out []Rule
	var lastPaths []string
	lastMatch, asked := false, false
	for i := range set.Rules {
		rule := &set.Rules[i]
		keep := len(rule.Paths) == 0
		if !keep {
			if !asked || !reflect.DeepEqual(rule.Paths, lastPaths) {
				_, lastMatch = matchGlobFirst(path, rule.Paths)
				lastPaths, asked = rule.Paths, true
			}
			keep = lastMatch
		}
		if keep {
			out = append(out, *rule)
		}
	}
	return out
}

func editRulesRef(set *RuleSet, path string) []Rule {
	var out []Rule
	for _, r := range scopedRulesRef(set, path) {
		if r.When != "turn" {
			out = append(out, r)
		}
	}
	return out
}

func genRuleSet(r *rand.Rand, n int) *RuleSet {
	names := []string{"Use named exports", "No console.log", "é ünï", "日本語 rule", "", "A", "x\ny", "🙂 emoji"}
	bodies := []string{"", "short body", strings.Repeat("long body text ", 50), strings.Repeat("é", 450), strings.Repeat("🙂", 250), "line1\nline2", "```ts\ncode\n```", strings.Repeat("x", 400), strings.Repeat("x", 401)}
	pathSets := [][]string{nil, {"src/**/*.ts"}, {"**/*.go"}, {"src/**/*.ts", "docs/*.md"}, {"*.md"}}
	set := &RuleSet{}
	for i := range n {
		rule := Rule{ID: "r" + strconv.Itoa(r.IntN(n+3)), Name: names[r.IntN(len(names))], Body: bodies[r.IntN(len(bodies))], Paths: pathSets[r.IntN(len(pathSets))]}
		if r.IntN(5) == 0 {
			rule.When = "turn"
		}
		if i%7 == 0 {
			rule.ID = "r" + strconv.Itoa(i)
		}
		set.Rules = append(set.Rules, rule)
	}
	return set
}

func TestBuildRulesRequestQuestionsAgreeWithUncachedQuestions(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 3))
	content := "export const a = 1;\n"
	for i := range 3000 {
		set := genRuleSet(r, 1+r.IntN(60))
		path := []string{"src/a.ts", "docs/x.md", "main.go", "README.md", "other.txt"}[r.IntN(5)]
		target := &RulesTarget{Tool: "write", Path: path, Content: &content}
		if r.IntN(3) == 0 {
			target.Edits = []EditView{{ID: "edit_1", NewText: "a"}, {ID: "edit_2", NewText: "b"}}
		}
		if got, want := editRulesFor(set, path), editRulesRef(set, path); !reflect.DeepEqual(got, want) {
			t.Fatalf("case %d: editRulesFor %v, want %v", i, got, want)
		}
		if got, want := rulesFor(set, path), scopedRulesRef(set, path); !reflect.DeepEqual(got, want) {
			t.Fatalf("case %d: rulesFor %v, want %v", i, got, want)
		}
		request := buildRulesRequest(target, set)
		scoped := editRulesRef(set, path)
		applicable := scoped[:min(len(scoped), maxRules)]
		if !reflect.DeepEqual(request.Applicable, applicable) && !(len(applicable) == 0 && len(request.Applicable) == 0) {
			t.Fatalf("case %d: applicable %v, want %v", i, request.Applicable, applicable)
		}
		want := Questions{}
		for _, rule := range applicable {
			want["rule_"+rule.ID] = ruleQuestion(rule)
		}
		if len(target.Edits) >= 2 {
			want[locatorQuestion] = request.Questions[locatorQuestion]
		}
		if !reflect.DeepEqual(request.Questions, want) {
			t.Fatalf("case %d: questions differ:\n got  %v\n want %v", i, request.Questions, want)
		}
		if request.Dropped != len(scoped)-len(applicable) {
			t.Fatalf("case %d: dropped %d", i, request.Dropped)
		}
	}
}

// A rule edited between two requests (same id) is asked about with its new text.
func TestRuleQuestionCacheFollowsTheRuleContent(t *testing.T) {
	content := "x"
	target := &RulesTarget{Tool: "write", Path: "a.ts", Content: &content}
	set := &RuleSet{Rules: []Rule{{ID: "same", Name: "Name one", Body: "body one"}}}
	first := buildRulesRequest(target, set).Questions["rule_same"].Instructions
	set.Rules[0].Body = "body two"
	second := buildRulesRequest(target, set).Questions["rule_same"].Instructions
	set.Rules[0].Name = "Name two"
	third := buildRulesRequest(target, set).Questions["rule_same"].Instructions
	if !strings.Contains(first, "Name one\nbody one") || !strings.Contains(second, "Name one\nbody two") || !strings.Contains(third, "Name two\nbody two") {
		t.Fatalf("stale question text:\n%s\n%s\n%s", first, second, third)
	}
	// More distinct rules than the cache holds: every answer still matches an uncached build.
	for i := range 2 * ruleQuestionCacheCap {
		set.Rules[0] = Rule{ID: "id" + strconv.Itoa(i), Name: "n" + strconv.Itoa(i), Body: "b" + strconv.Itoa(i)}
		got := buildRulesRequest(target, set).Questions["rule_id"+strconv.Itoa(i)]
		if !reflect.DeepEqual(got, ruleQuestion(set.Rules[0])) {
			t.Fatalf("rule %d: %v", i, got)
		}
	}
}

func TestRuleQuestionCacheIsSafeForConcurrentRequests(t *testing.T) {
	content := "x"
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := rand.New(rand.NewPCG(uint64(g), 1))
			for range 300 {
				set := genRuleSet(r, 1+r.IntN(30))
				target := &RulesTarget{Tool: "write", Path: "src/a.ts", Content: &content}
				request := buildRulesRequest(target, set)
				want := Questions{}
				for _, rule := range request.Applicable {
					want["rule_"+rule.ID] = ruleQuestion(rule)
				}
				for key, q := range want {
					if !reflect.DeepEqual(request.Questions[key], q) {
						t.Errorf("%s: question differs", key)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
}
