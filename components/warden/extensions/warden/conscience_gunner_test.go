package warden

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// Twins of tests/conscience-gunner.test.ts: each case targets a safety rule of load mode or of the assessment
// envelope (file identity, headroom, malicious judge answers, caps, canaries).

func gnLoadConfig(mod ...func(*ConscienceConfig)) ConscienceConfig {
	c := csConfig(func(c *ConscienceConfig) {
		c.Skills.Mode = "load"
		c.Tools.Enabled = false
		c.TimeoutMs = 1500
		c.RecommendThreshold = 1.0
	})
	for _, m := range mod {
		m(&c)
	}
	return c
}

func gnRecommendConfig(mod ...func(*ConscienceConfig)) ConscienceConfig {
	c := csConfig(func(c *ConscienceConfig) { c.TimeoutMs = 1500; c.RecommendThreshold = 1.0 })
	for _, m := range mod {
		m(&c)
	}
	return c
}

// gnSkillFile writes <temp>/.pi/skills/<name>/SKILL.md and returns the skill that advertises it.
func gnSkillFile(t *testing.T, temp, name, body, description string) Skill {
	t.Helper()
	dir := filepath.Join(temp, ".pi", "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return Skill{Name: name, Description: description, FilePath: path}
}

func gnOpts(name, description string, mod ...func(*LoadOpts)) LoadOpts {
	o := LoadOpts{RemainingMs: 5000, ConsentGiven: true, ProjectTrusted: true, CatalogName: name, CatalogDesc: description, ContextWindow: intPtr(200000)}
	for _, m := range mod {
		m(&o)
	}
	return o
}

func intPtr(n int) *int { return &n }

func fm(name, description string) string {
	return "---\nname: " + name + "\ndescription: " + description + "\n---\n\n"
}

func mustBody(t *testing.T, r LoadResult, msg string) string {
	t.Helper()
	if r.Body == nil || *r.Body == "" {
		t.Fatalf("%s: no body (%q)", msg, r.SkipReason)
	}
	return *r.Body
}

// twin: tests/conscience-gunner.test.ts:144
func TestRule4FileModifiedBetweenTwoLoadSkillBodyCallsYieldsLoadChanged(t *testing.T) {
	temp := t.TempDir()
	s := gnSkillFile(t, temp, "r4-replace", fm("r4-replace", "Rule 4 test")+"Original body.", "Rule 4 test")
	o := gnOpts("r4-replace", "Rule 4 test")
	mustBody(t, loadSkillBody(s, gnLoadConfig(), o), "first load should succeed")
	if err := os.WriteFile(s.FilePath, []byte(fm("r4-replace", "Rule 4 test")+"Modified body that is significantly longer."), 0o644); err != nil {
		t.Fatal(err)
	}
	r2 := loadSkillBody(s, gnLoadConfig(), o)
	if r2.SkipReason != "load_changed" || r2.Body != nil {
		t.Errorf("file replacement should yield load_changed: %+v", r2)
	}
}

// twin: tests/conscience-gunner.test.ts:175
func TestRule4SymlinkRetargetedBetweenCalls(t *testing.T) {
	temp := t.TempDir()
	realDir, linkDir := filepath.Join(temp, "real"), filepath.Join(temp, "link")
	realDir2 := filepath.Join(temp, "real2")
	for _, d := range []string{realDir, linkDir, realDir2} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(realDir, "SKILL.md"), []byte(fm("r4-symlink", "Symlink test")+"Original symlink target."), 0o644)
	link := filepath.Join(linkDir, "SKILL.md")
	if err := os.Symlink(filepath.Join(realDir, "SKILL.md"), link); err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}
	s := Skill{Name: "r4-symlink", Description: "Symlink test", FilePath: link}
	o := gnOpts("r4-symlink", "Symlink test")
	mustBody(t, loadSkillBody(s, gnLoadConfig(), o), "first load through symlink should succeed")
	os.WriteFile(filepath.Join(realDir2, "SKILL.md"), []byte(fm("r4-symlink", "Symlink test")+"New symlink target."), 0o644)
	os.Remove(link)
	if err := os.Symlink(filepath.Join(realDir2, "SKILL.md"), link); err != nil {
		t.Fatal(err)
	}
	// Both are symlinks, so the cross-call check does not compare them; the within-call checks see one consistent file.
	mustBody(t, loadSkillBody(s, gnLoadConfig(), o), "symlink retarget within same call should succeed")
}

// twin: tests/conscience-gunner.test.ts:217
func TestRule4FileSizeChangedBetweenCalls(t *testing.T) {
	temp := t.TempDir()
	s := gnSkillFile(t, temp, "r4-size", fm("r4-size", "Size test")+"Body.", "Size test")
	o := gnOpts("r4-size", "Size test")
	loadSkillBody(s, gnLoadConfig(), o)
	os.WriteFile(s.FilePath, []byte(fm("r4-size", "Size test")+strings.Repeat("x", 10000)), 0o644)
	r2 := loadSkillBody(s, gnLoadConfig(), o)
	if r2.SkipReason != "load_changed" {
		t.Errorf("a file whose size changed since the first load is changed, got %+v", r2.SkipReason)
	}
}

// twin: tests/conscience-gunner.test.ts:252
func TestRule9ContextWindowNilYieldsHeadroomUnknownNoLoad(t *testing.T) {
	s := gnSkillFile(t, t.TempDir(), "r9-noctx", fm("r9-noctx", "No context")+"Small body.", "No context")
	r := loadSkillBody(s, gnLoadConfig(), gnOpts("r9-noctx", "No context", func(o *LoadOpts) { o.ContextWindow = nil }))
	if r.SkipReason != "headroom_unknown" || r.Body != nil {
		t.Errorf("%+v", r)
	}
}

// twin: tests/conscience-gunner.test.ts:269
func TestRule9BodyOver80PercentOfContextWindowYieldsLoadTooLarge(t *testing.T) {
	s := gnSkillFile(t, t.TempDir(), "r9-over80", fm("r9-over80", "Over 80%")+strings.Repeat("x", 40000), "Over 80%")
	r := loadSkillBody(s, gnLoadConfig(func(c *ConscienceConfig) { c.MaxSkillBytes = 65536 }), gnOpts("r9-over80", "Over 80%", func(o *LoadOpts) { o.ContextWindow = intPtr(10000) }))
	if r.SkipReason != "load_too_large" || r.Body != nil {
		t.Errorf("%+v", r)
	}
}

// twin: tests/conscience-gunner.test.ts:287
func TestRule9ImagesInContextYieldsHeadroomUnknown(t *testing.T) {
	s := gnSkillFile(t, t.TempDir(), "r9-images", fm("r9-images", "Has images")+"Small body.", "Has images")
	r := loadSkillBody(s, gnLoadConfig(), gnOpts("r9-images", "Has images", func(o *LoadOpts) { o.HasImages = true }))
	if r.SkipReason != "headroom_unknown" || r.Body != nil {
		t.Errorf("%+v", r)
	}
}

// twin: tests/conscience-gunner.test.ts:304
// twin: tests/conscience-gunner.test.ts:961
func TestRule9BodyWithinHeadroomSucceeds(t *testing.T) {
	s := gnSkillFile(t, t.TempDir(), "r9-ok", fm("r9-ok", "Within headroom")+"Small body.", "Within headroom")
	mustBody(t, loadSkillBody(s, gnLoadConfig(), gnOpts("r9-ok", "Within headroom")), "body within 80% headroom should succeed")
}

// twin: tests/conscience-gunner.test.ts:324
// twin: tests/conscience-gunner.test.ts:896
func TestRule10LoadSkillBodyReturnsBodyAndLoadMessageHasRelativeReferenceNotThePath(t *testing.T) {
	s := gnSkillFile(t, t.TempDir(), "r10-load", fm("r10-load", "Load test")+"Instructions for load.", "Load test")
	r := loadSkillBody(s, gnLoadConfig(), gnOpts("r10-load", "Load test"))
	if !strings.Contains(mustBody(t, r, "load should return a body"), "Instructions for load.") {
		t.Error("body should contain the skill text")
	}
	msg := buildLoadMessage(r)
	for _, want := range []string{"Skill: r10-load", "Instructions for load.", "Resolve this skill's relative references"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q: %s", want, msg)
		}
	}
	body := "Skill instructions."
	msg = buildLoadMessage(LoadResult{Body: &body, SkillName: "test", AdvertisedPath: "/skills/test/SKILL.md", ResolvedPath: "/skills/test/SKILL.md", RelativeRef: relativeRef, BytesLoaded: 20})
	if !strings.Contains(msg, "Resolve this skill's relative references") || strings.Contains(msg, "/skills/test/SKILL.md") {
		t.Errorf("message should not contain the path, only the relative reference: %q", msg)
	}
}

// twin: tests/conscience-gunner.test.ts:359
func TestPartBAssessIsStatelessDedupBelongsToExtensionHooks(t *testing.T) {
	skills := []Skill{csSkill("queued-test", "Queued test")}
	j := csSelection("advance", "c1", 3, []float64{0.05, 0.05, 0.65, 0.25}, 1)
	deps := ConscienceDeps{Judge: j, Config: gnRecommendConfig(func(c *ConscienceConfig) { c.RecommendThreshold = 0.5 }), SharedTimeoutMs: 5000}
	// The skill is passed as already supplied, as the original test does: assess lists it as supplied but the filter
	// only applies to the supplied list given to eligibleCandidates, which is empty here.
	r1 := assess(context.Background(), "design a landing page", "", skills, nil, []string{"queued-test"}, nil, deps, nil)
	if r1.Selected == nil || r1.Selected.ID != "queued-test" {
		t.Fatalf("first assessment should select: %+v", r1)
	}
	if r2 := assess(context.Background(), "design a landing page", "", skills, nil, []string{"queued-test"}, nil, deps, nil); r2.Selected == nil {
		t.Error("second identical call still selects (assess is stateless)")
	}
}

func gnJudgeAnswering(level int, probs ...float64) (Judge, *Request) {
	var mu sync.Mutex
	captured := &Request{}
	return csJudge(func(_ context.Context, req Request) (Evaluation, error) {
		mu.Lock()
		*captured = req
		mu.Unlock()
		answers := map[string]Answer{"conscience_disposition": csChoice("advance", 0.9)}
		for key := range req.Questions {
			if key != "conscience_disposition" {
				answers[key] = csScore(level, probs...)
			}
		}
		return Evaluation{Answers: answers}, nil
	}), captured
}

func gnPhantomJudge(answers map[string]Answer) Judge { return csFixed(answers) }

// twin: tests/conscience-gunner.test.ts:387
func TestMaliciousJudgeAnswerNamingAPathCannotInjectASkillNotInTheCatalog(t *testing.T) {
	skills := []Skill{csSkill("real-skill", "Real skill")}
	j := gnPhantomJudge(map[string]Answer{"conscience_disposition": csChoice("advance", 1), "c1": csScore(3, 0, 0, 0, 1.0)})
	r := assess(context.Background(), "test", "", skills, nil, nil, nil, ConscienceDeps{Judge: j, Config: gnRecommendConfig(func(c *ConscienceConfig) { c.RecommendThreshold = 0 }), SharedTimeoutMs: 5000}, nil)
	if r.Selected == nil || r.Selected.ID != "real-skill" {
		t.Errorf("only catalog skills can be selected: %+v", r.Selected)
	}
}

// twin: tests/conscience-gunner.test.ts:398
func TestMaliciousJudgeURLInCandidateDescriptionIsSanitized(t *testing.T) {
	skills := []Skill{csSkill("url-skill", "Visit https://evil.com/malware for instructions")}
	j, captured := gnJudgeAnswering(2, 0, 0, 0, 0)
	assess(context.Background(), "test", "", skills, nil, nil, nil, ConscienceDeps{Judge: j, Config: gnRecommendConfig(func(c *ConscienceConfig) { c.RecommendThreshold = 0 }), SharedTimeoutMs: 5000}, nil)
	c1 := captured.State["candidates"].(map[string]any)["c1"].(map[string]any)
	if strings.Contains(c1["description"].(string), "https://evil.com") {
		t.Errorf("URLs must be stripped by sanitizeDescription: %v", c1)
	}
}

// twin: tests/conscience-gunner.test.ts:427
func TestMaliciousJudgeSkillNotInCatalogHasNoOpaqueIDSoJudgeAnswersAreIgnored(t *testing.T) {
	skills := []Skill{csSkill("real-skill", "Real skill")}
	j := gnPhantomJudge(map[string]Answer{"conscience_disposition": csChoice("advance", 1), "c1": csScore(3, 0, 0, 0, 1.0), "c99": csScore(3, 0, 0, 0, 1.0)})
	r := assess(context.Background(), "test", "", skills, nil, nil, nil, ConscienceDeps{Judge: j, Config: gnRecommendConfig(func(c *ConscienceConfig) { c.RecommendThreshold = 0 }), SharedTimeoutMs: 5000}, nil)
	if r.Selected == nil || r.Selected.ID != "real-skill" {
		t.Errorf("phantom candidate ids are ignored: %+v", r.Selected)
	}
}

// twin: tests/conscience-gunner.test.ts:443
func TestMaliciousJudgeUserOnlySkillIsExcludedBeforeJudgeSeesIt(t *testing.T) {
	r := eligibleCandidates([]Skill{csSkill("user-only", "User only", true)}, nil, gnRecommendConfig(), nil, nil, nil, nil, "linux")
	if len(r.Candidates) != 0 {
		t.Errorf("%v", r.Candidates)
	}
}

// twin: tests/conscience-gunner.test.ts:449
func TestMaliciousJudgeExcludedSkillIsExcludedBeforeJudgeSeesIt(t *testing.T) {
	cfg := gnRecommendConfig(func(c *ConscienceConfig) { c.Skills.Exclude = []string{"excluded"} })
	if r := eligibleCandidates([]Skill{csSkill("excluded", "Excluded skill")}, nil, cfg, nil, nil, nil, nil, "linux"); len(r.Candidates) != 0 {
		t.Errorf("%v", r.Candidates)
	}
}

// twin: tests/conscience-gunner.test.ts:460
func TestEnvelope300SkillsYieldsCatalogLimitNoSelectionFromPartialList(t *testing.T) {
	var n atomic.Int64
	r := assess(context.Background(), "test", "", csManySkills(300), nil, nil, nil,
		ConscienceDeps{Judge: csAnswerAll(&n, nil, 0.1, 0.2, 0.5, 0.2), Config: gnRecommendConfig(func(c *ConscienceConfig) { c.RecommendThreshold = 0.5 }), SharedTimeoutMs: 5000}, nil)
	if r.SkipReason != "catalog_limit" || r.Selected != nil {
		t.Errorf("%+v", r)
	}
}

// twin: tests/conscience-gunner.test.ts:472
func TestEnvelope40CandidatesYieldTwoRequestsNoneOver32Questions(t *testing.T) {
	var n, maxQ atomic.Int64
	assess(context.Background(), "test", "", csManySkills(40), nil, nil, nil,
		ConscienceDeps{Judge: csAnswerAll(&n, &maxQ, 0.1, 0.2, 0.5, 0.2), Config: gnRecommendConfig(func(c *ConscienceConfig) { c.RecommendThreshold = 0.5 }), SharedTimeoutMs: 5000}, nil)
	if n.Load() != 2 || maxQ.Load() > 32 {
		t.Errorf("40 candidates should yield 2 requests (max 32 questions each): %d requests, max %d questions", n.Load(), maxQ.Load())
	}
}

// twin: tests/conscience-gunner.test.ts:507
func TestEnvelope8193ByteDescriptionIsTruncatedByCapDescription(t *testing.T) {
	skills := []Skill{csSkill("long-desc", strings.Repeat("x", 8193)), csSkill("normal", "Normal skill")}
	j, captured := gnJudgeAnswering(2, 0.1, 0.2, 0.5, 0.2)
	assess(context.Background(), "test", "", skills, nil, nil, nil, ConscienceDeps{Judge: j, Config: gnRecommendConfig(func(c *ConscienceConfig) { c.RecommendThreshold = 0.5 }), SharedTimeoutMs: 5000}, nil)
	desc := captured.State["candidates"].(map[string]any)["c1"].(map[string]any)["description"].(string)
	if len(desc) > 8192 || !strings.HasSuffix(desc, "...") {
		t.Errorf("description should be capped at 8192 bytes and end with ...: %d bytes", len(desc))
	}
}

func gnCapture() (Judge, func() map[string]any) {
	var mu sync.Mutex
	var state map[string]any
	j := csJudge(func(_ context.Context, req Request) (Evaluation, error) {
		mu.Lock()
		state = req.State
		mu.Unlock()
		return Evaluation{Answers: map[string]Answer{"conscience_disposition": csChoice("advance", 0.9)}}, nil
	})
	return j, func() map[string]any { mu.Lock(); defer mu.Unlock(); return state }
}

// twin: tests/conscience-gunner.test.ts:553
func TestCanaryCredentialInPromptNeverAppearsInJudgeRequest(t *testing.T) {
	canary := "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef1234567890"
	j, state := gnCapture()
	assess(context.Background(), "deploy with token "+canary, "", []Skill{csSkill("canary-test", "Canary test skill")}, nil, nil, nil,
		ConscienceDeps{Judge: j, Config: gnRecommendConfig(func(c *ConscienceConfig) { c.RecommendThreshold = 0.5 }), SharedTimeoutMs: 5000}, nil)
	if strings.Contains(jsonCompact(state()), canary) {
		t.Error("credential must not appear in judge request state")
	}
}

// twin: tests/conscience-gunner.test.ts:589
func TestCanaryAbsoluteHomePathInPromptNeverAppearsInJudgeRequest(t *testing.T) {
	j, state := gnCapture()
	assess(context.Background(), "read /Users/secret/admin/.ssh/id_rsa for config", "", []Skill{csSkill("canary-path", "Path canary skill")}, nil, nil, nil,
		ConscienceDeps{Judge: j, Config: gnRecommendConfig(func(c *ConscienceConfig) { c.RecommendThreshold = 0.5 }), SharedTimeoutMs: 5000}, nil)
	if strings.Contains(jsonCompact(state()), "/Users/secret") {
		t.Error("absolute path must not appear in judge request state")
	}
}

// twin: tests/conscience-gunner.test.ts:617
func TestCanaryCredentialInSkillDescriptionIsSanitizedFromJudgeRequest(t *testing.T) {
	j, state := gnCapture()
	assess(context.Background(), "test", "", []Skill{csSkill("secret-desc", "Contains token: sk-live-abc123def456ghi789")}, nil, nil, nil,
		ConscienceDeps{Judge: j, Config: gnRecommendConfig(func(c *ConscienceConfig) { c.RecommendThreshold = 0.5 }), SharedTimeoutMs: 5000}, nil)
	if strings.Contains(jsonCompact(state()), "sk-live-abc123") {
		t.Error("credential in skill description must be sanitized from judge request")
	}
}

// twin: tests/conscience-gunner.test.ts:648
func TestCanaryCredentialInSkillBodyYieldsMetadataUnsafeBodyNotReturned(t *testing.T) {
	s := gnSkillFile(t, t.TempDir(), "body-canary", fm("body-canary", "Body canary")+"Token: ghp_BODYCANARY1234567890abcdefghijklmnopqr", "Body canary")
	r := loadSkillBody(s, gnLoadConfig(), gnOpts("body-canary", "Body canary"))
	if r.SkipReason != "metadata_unsafe" || r.Body != nil {
		t.Errorf("%+v", r)
	}
}

// twin: tests/conscience-gunner.test.ts:682
func TestConfigAttackMaxLoadedBytesBelowBodySizeYieldsLoadTooLargeOnFirstLoad(t *testing.T) {
	s := gnSkillFile(t, t.TempDir(), "config-attack", fm("config-attack", "Config attack")+"Body.", "Config attack")
	r := loadSkillBody(s, gnLoadConfig(func(c *ConscienceConfig) { c.MaxSkillBytes = 32768; c.MaxLoadedBytes = 5 }), gnOpts("config-attack", "Config attack"))
	if r.SkipReason != "load_too_large" {
		t.Errorf("%+v", r)
	}
}

// twin: tests/conscience-gunner.test.ts:718
func TestConfigAttackRecommendThresholdZeroWithNoPolicyDoesNotBypassTheExtensionHookGate(t *testing.T) {
	j := csSelection("advance", "c1", 3, []float64{0.05, 0.05, 0.65, 0.25}, 1)
	r := assess(context.Background(), "test", "", []Skill{csSkill("zero-threshold", "Zero threshold skill")}, nil, nil, nil,
		ConscienceDeps{Judge: j, Config: gnRecommendConfig(func(c *ConscienceConfig) { c.RecommendThreshold = 0 }), SharedTimeoutMs: 5000}, nil)
	// assess selects at threshold 0.0; the gate lives in the wiring, which holds the policy (the lifecycle twins).
	if r.Selected == nil {
		t.Error("assess passes threshold 0.0")
	}
	if policyMatches(nil, r.QuestionHash, "jev-1.13.0") {
		t.Error("with no policy nothing is delivered")
	}
}

// twin: tests/conscience-gunner.test.ts:761
func TestRule5OversizedFileYieldsLoadTooLarge(t *testing.T) {
	s := gnSkillFile(t, t.TempDir(), "r5-bounds", fm("r5-bounds", "Bounds test")+strings.Repeat("a", 50000), "Bounds test")
	r := loadSkillBody(s, gnLoadConfig(func(c *ConscienceConfig) { c.MaxSkillBytes = 1000 }), gnOpts("r5-bounds", "Bounds test"))
	if r.SkipReason != "load_too_large" {
		t.Errorf("%+v", r)
	}
}

// twin: tests/conscience-gunner.test.ts:779
func TestRule6ChangedNameInFrontmatterYieldsLoadChanged(t *testing.T) {
	s := gnSkillFile(t, t.TempDir(), "r6-frontmatter", fm("r6-frontmatter", "FM test")+"Body.", "FM test")
	o := gnOpts("r6-frontmatter", "FM test")
	mustBody(t, loadSkillBody(s, gnLoadConfig(), o), "first load should succeed")
	os.WriteFile(s.FilePath, []byte(fm("DIFFERENT-NAME", "FM test")+"Body."), 0o644)
	if r := loadSkillBody(s, gnLoadConfig(), o); r.SkipReason != "load_changed" {
		t.Errorf("%+v", r)
	}
}

func gnPrecondition(t *testing.T, name string, cfg ConscienceConfig, skillMod func(*Skill), optMod func(*LoadOpts), want string) {
	t.Helper()
	s := gnSkillFile(t, t.TempDir(), name, fm(name, "Case")+"Body.", "Case")
	if skillMod != nil {
		skillMod(&s)
	}
	var mods []func(*LoadOpts)
	if optMod != nil {
		mods = append(mods, optMod)
	}
	r := loadSkillBody(s, cfg, gnOpts(name, "Case", mods...))
	if r.SkipReason != want {
		t.Errorf("want %s, got %+v", want, r)
	}
}

// twin: tests/conscience-gunner.test.ts:807
func TestRule2NoConsentYieldsNotEligible(t *testing.T) {
	gnPrecondition(t, "r2-consent", gnLoadConfig(), nil, func(o *LoadOpts) { o.ConsentGiven = false }, "not_eligible")
}

// twin: tests/conscience-gunner.test.ts:823
func TestRule2UntrustedProjectYieldsNotEligible(t *testing.T) {
	gnPrecondition(t, "r2-trust", gnLoadConfig(), nil, func(o *LoadOpts) { o.ProjectTrusted = false }, "not_eligible")
}

// twin: tests/conscience-gunner.test.ts:839
func TestRule2UserInvokedSkillYieldsAlreadySupplied(t *testing.T) {
	gnPrecondition(t, "r2-invoked", gnLoadConfig(), nil, func(o *LoadOpts) { o.UserInvoked = true }, "already_supplied")
}

// twin: tests/conscience-gunner.test.ts:855
func TestRule3PathRuleBlocksLoadYieldingLoadDenied(t *testing.T) {
	// The path-rule matcher is the guard-core lane's; the test installs one that matches `**/skills/**` for reads.
	saved := matchReadPathRules
	t.Cleanup(func() { matchReadPathRules = saved })
	matchReadPathRules = func(path string, rules []PathRule, _ map[string]bool) []PatternHit {
		if len(rules) > 0 && strings.Contains(path, "/skills/") {
			return []PatternHit{{ID: "path:block-skills", Severity: SeverityDeny, Label: "block-skills"}}
		}
		return nil
	}
	s := gnSkillFile(t, t.TempDir(), "r3-path", fm("r3-path", "Path test")+"Body.", "Path test")
	rules := []PathRule{{ID: "block-skills", Paths: []string{"**/skills/**"}, Access: "none", Tools: []string{"read"}, Action: "confirm"}}
	r := loadSkillBody(s, gnLoadConfig(), gnOpts("r3-path", "Path test", func(o *LoadOpts) { o.PathRules = rules }))
	if r.SkipReason != "load_denied" {
		t.Errorf("%+v", r)
	}
}

// twin: tests/conscience-gunner.test.ts:879
func TestRule7AbsolutePathInBodyYieldsMetadataUnsafe(t *testing.T) {
	s := gnSkillFile(t, t.TempDir(), "r7-path-body", fm("r7-path-body", "Path body")+"Read /etc/passwd for details.", "Path body")
	if r := loadSkillBody(s, gnLoadConfig(), gnOpts("r7-path-body", "Path body")); r.SkipReason != "metadata_unsafe" {
		t.Errorf("%+v", r)
	}
}

// twin: tests/conscience-gunner.test.ts:911
func TestRule2SkillsModeOffYieldsNotEligible(t *testing.T) {
	gnPrecondition(t, "r2-mode-off", gnLoadConfig(func(c *ConscienceConfig) { c.Skills.Mode = "off" }), nil, nil, "not_eligible")
}

// twin: tests/conscience-gunner.test.ts:927
func TestRule2DisabledConscienceYieldsNotEligible(t *testing.T) {
	gnPrecondition(t, "r2-disabled", gnLoadConfig(func(c *ConscienceConfig) { c.Enabled = false }), nil, nil, "not_eligible")
}

// twin: tests/conscience-gunner.test.ts:943
func TestRule2DisableModelInvocationSkillYieldsNotEligible(t *testing.T) {
	gnPrecondition(t, "r2-dmi", gnLoadConfig(), func(s *Skill) { s.DisableModelInvocation = true }, nil, "not_eligible")
}

// twin: tests/conscience-gunner.test.ts:977
func TestRule5CumulativeMaxLoadedBytesLimitsSecondLoad(t *testing.T) {
	s := gnSkillFile(t, t.TempDir(), "r5-cumul-a", fm("r5-cumul-a", "A")+"Body A.", "A")
	cfg := gnLoadConfig(func(c *ConscienceConfig) { c.MaxLoadedBytes = 100 })
	mustBody(t, loadSkillBody(s, cfg, gnOpts("r5-cumul-a", "A")), "first load should succeed")
	r2 := loadSkillBody(s, cfg, gnOpts("r5-cumul-a", "A", func(o *LoadOpts) { o.LoadedBytes = 90 }))
	if r2.SkipReason != "load_too_large" {
		t.Errorf("second load exceeding cumulative limit should yield load_too_large: %+v", r2)
	}
}

// twin: tests/conscience-gunner.test.ts:1000
func TestRule5ZeroRemainingTimeYieldsLoadFailed(t *testing.T) {
	gnPrecondition(t, "r5-notime", gnLoadConfig(), nil, func(o *LoadOpts) { o.RemainingMs = 0 }, "load_failed")
}

// twin: tests/conscience-gunner.test.ts:1016
func TestRule5NegativeRemainingTimeYieldsLoadFailed(t *testing.T) {
	gnPrecondition(t, "r5-negtime", gnLoadConfig(), nil, func(o *LoadOpts) { o.RemainingMs = -100 }, "load_failed")
}

// twin: tests/conscience-gunner.test.ts:1032
func TestRule5UnreadableFileYieldsLoadFailed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode 000 file")
	}
	s := gnSkillFile(t, t.TempDir(), "r5-locked", fm("r5-locked", "Locked")+"Body.", "Locked")
	if err := os.Chmod(s.FilePath, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(s.FilePath, 0o644)
	if r := loadSkillBody(s, gnLoadConfig(), gnOpts("r5-locked", "Locked")); r.SkipReason != "load_failed" {
		t.Errorf("%+v", r)
	}
}

// twin: tests/conscience-gunner.test.ts:1054
func TestRule6ChangedDescriptionInFrontmatterYieldsLoadChanged(t *testing.T) {
	s := gnSkillFile(t, t.TempDir(), "r6-desc", fm("r6-desc", "Original")+"Body.", "Original")
	o := gnOpts("r6-desc", "Original")
	loadSkillBody(s, gnLoadConfig(), o)
	os.WriteFile(s.FilePath, []byte(fm("r6-desc", "Different")+"Body."), 0o644)
	if r := loadSkillBody(s, gnLoadConfig(), o); r.SkipReason != "load_changed" {
		t.Errorf("%+v", r)
	}
}

// twin: tests/conscience-gunner.test.ts:1080
func TestRule6DisableModelInvocationAddedInFrontmatterYieldsLoadChanged(t *testing.T) {
	s := gnSkillFile(t, t.TempDir(), "r6-dmi", fm("r6-dmi", "DMI")+"Body.", "DMI")
	o := gnOpts("r6-dmi", "DMI")
	loadSkillBody(s, gnLoadConfig(), o)
	os.WriteFile(s.FilePath, []byte("---\nname: r6-dmi\ndescription: DMI\ndisable-model-invocation: true\n---\n\nBody."), 0o644)
	if r := loadSkillBody(s, gnLoadConfig(), o); r.SkipReason != "load_changed" {
		t.Errorf("%+v", r)
	}
}

// Go-only: the frontmatter reader the load rules depend on, over the YAML spellings skill files use.
func TestParseFrontmatterReadsTheTopLevelValuesOfSkillFiles(t *testing.T) {
	cases := []struct {
		name, src     string
		wantName      any
		wantDesc      any
		wantBody      string
		wantUserOnly  bool
		wantErr       bool
		wantNoYAMLKey bool
	}{
		{name: "plain", src: "---\nname: a\ndescription: Plain text here\n---\n\nBody\n", wantName: "a", wantDesc: "Plain text here", wantBody: "Body"},
		{name: "double quoted with a colon", src: "---\nname: a\ndescription: \"Use when: asked\"\n---\nB", wantName: "a", wantDesc: "Use when: asked", wantBody: "B"},
		{name: "single quoted", src: "---\nname: 'a'\ndescription: 'it''s'\n---\nB", wantName: "a", wantDesc: "it's", wantBody: "B"},
		{name: "folded block", src: "---\nname: a\ndescription: >\n  one\n  two\n\n  three\n---\nB", wantName: "a", wantDesc: "one two\nthree\n", wantBody: "B"},
		{name: "literal block strip", src: "---\nname: a\ndescription: |-\n  one\n  two\n---\nB", wantName: "a", wantDesc: "one\ntwo", wantBody: "B"},
		{name: "comment and flow list", src: "---\n# c\nname: a # trailing\ntags: [x, y]\ndescription: d\n---\nB", wantName: "a", wantDesc: "d", wantBody: "B"},
		{name: "number is not a string", src: "---\nname: 123\ndescription: d\n---\nB", wantName: nil, wantDesc: "d", wantBody: "B"},
		{name: "user only", src: "---\nname: a\ndisable-model-invocation: true\n---\nB", wantName: "a", wantBody: "B", wantUserOnly: true},
		{name: "crlf", src: "---\r\nname: a\r\ndescription: d\r\n---\r\n\r\nB\r\n", wantName: "a", wantDesc: "d", wantBody: "B"},
		{name: "no frontmatter", src: "# Title\n\nBody", wantNoYAMLKey: true, wantBody: "# Title\n\nBody"},
		{name: "invalid yaml", src: "---\nname a\n---\nB", wantErr: true},
	}
	for _, c := range cases {
		fm, body, err := parseFrontmatter(c.src)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err %v", c.name, err)
			continue
		}
		if c.wantErr {
			continue
		}
		if body != c.wantBody {
			t.Errorf("%s: body %q want %q", c.name, body, c.wantBody)
		}
		if c.wantNoYAMLKey {
			if len(fm) != 0 {
				t.Errorf("%s: %v", c.name, fm)
			}
			continue
		}
		if got := fm["name"]; got != c.wantName && !(got == nil && c.wantName == nil) {
			if _, isNum := got.(float64); !(isNum && c.wantName == nil) {
				t.Errorf("%s: name %v want %v", c.name, got, c.wantName)
			}
		}
		if c.wantDesc != nil && fm["description"] != c.wantDesc {
			t.Errorf("%s: description %q want %q", c.name, fm["description"], c.wantDesc)
		}
		if (fm["disable-model-invocation"] == true) != c.wantUserOnly {
			t.Errorf("%s: user-only %v", c.name, fm["disable-model-invocation"])
		}
	}
}
