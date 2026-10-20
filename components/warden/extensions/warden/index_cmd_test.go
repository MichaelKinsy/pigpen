package warden

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// Twins of tests/index.test.ts: the capability index (prompt, validation, truncation, storage, lookup) and how the
// conscience's candidates pick up an entry.

var ixSample = IndexEntry{
	Kind: "skill", Name: "test-skill", Scope: "global", SourceHash: "abc123def4567890", Role: "evidence",
	Lead:    "Test things with node:test runner",
	UseWhen: []string{"When the user wants to write or fix tests", "When a bug needs a regression test"},
	NotWhen: []string{"When tests already pass do not re-run them"}, Inputs: "A description of what to test",
	Examples: []string{"add tests for the config loader", "write a regression test for issue #42"}, SourceQuality: "strong",
}

var ixSampleTool = IndexEntry{
	Kind: "tool", Name: "search_graph", Scope: "global", SourceHash: "tool123hash456789", Role: "evidence",
	Lead:    "Search the code knowledge graph",
	UseWhen: []string{"When you need to find function definitions or classes", "When grep cannot find structural code patterns"},
	NotWhen: []string{}, Inputs: "A search query string",
	Examples: []string{"find the config loader function", "search for all TypeSafe calls"}, SourceQuality: "strong",
}

const ixSkillContent = "# Test Skill\n\nA test skill for unit tests.\n"

// ixMap is an entry as decoded JSON, with mods applied: what validateEntry receives from a file.
func ixMap(t *testing.T, e IndexEntry, mods ...func(map[string]any)) map[string]any {
	t.Helper()
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, f := range mods {
		f(m)
	}
	return m
}

func ixValid(t *testing.T, v EntryValidation) IndexEntry {
	t.Helper()
	if v.Entry == nil {
		t.Fatalf("expected entry, got rejection: %s", v.Reject)
	}
	return *v.Entry
}

func ixFile(entries ...IndexEntry) *IndexFile {
	return &IndexFile{FormatVersion: 1, BuiltAt: "t", Model: "m", Entries: entries}
}

// ixRaw is a whole file as decoded JSON.
func ixRaw(t *testing.T, builtAt string, entries ...any) map[string]any {
	return map[string]any{"formatVersion": float64(1), "builtAt": builtAt, "model": "m", "entries": entries}
}

func ixDirs(t *testing.T) (indexDirs, string) {
	t.Helper()
	temp := t.TempDir()
	env := map[string]string{"PIGPEN_WARDEN_INDEX_DIR": temp}
	d := indexDirs{Getenv: func(k string) string { return env[k] }}
	return d, temp
}

func ixSkillFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "test-skill.md")
	if err := os.WriteFile(p, []byte(ixSkillContent), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func ixPrompt(skills []IndexSkill, tools []CapabilityTool) string {
	return buildIndexPrompt("/test", skills, tools, "/g.json", "/p.json", "/home/.pi/agent")
}

// twin: tests/index.test.ts:117
func TestBuildIndexPromptContainsBothOutputPaths(t *testing.T) {
	p := buildIndexPrompt("/test/project", []IndexSkill{{"tdd", "TDD", "/skills/tdd/SKILL.md"}}, []CapabilityTool{{"bash", "Run commands"}},
		"/home/.pi/agent/pi-warden/index/global.json", "/home/.pi/agent/pi-warden/index/projects/abc123.json", "/home/.pi/agent")
	for _, want := range []string{"/home/.pi/agent/pi-warden/index/global.json", "/home/.pi/agent/pi-warden/index/projects/abc123.json"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt should contain %s", want)
		}
	}
}

// twin: tests/index.test.ts:128
func TestBuildIndexPromptContainsFormatRules(t *testing.T) {
	p := ixPrompt(nil, nil)
	for _, w := range []string{"formatVersion", "sourceHash", "lead", "useWhen", "notWhen", "examples", "thin", "role"} {
		if !strings.Contains(p, w) {
			t.Errorf("should mention %s", w)
		}
	}
}

// twin: tests/index.test.ts:140
func TestBuildIndexPromptContainsWritingRules(t *testing.T) {
	p := ixPrompt(nil, nil)
	for _, w := range []string{"Front-load", "trigger", "positive"} {
		if !strings.Contains(p, w) {
			t.Errorf("should mention %s", w)
		}
	}
}

// twin: tests/index.test.ts:147
func TestBuildIndexPromptContainsRoleDefinitions(t *testing.T) {
	p := ixPrompt(nil, nil)
	for _, w := range []string{"research", "evidence", "execution", "delegation", "review", "conversation"} {
		if !strings.Contains(p, w) {
			t.Errorf("should define %s", w)
		}
	}
}

// twin: tests/index.test.ts:159
func TestValidateEntryAcceptsAValidEntry(t *testing.T) {
	e := ixValid(t, validateEntry(ixMap(t, ixSample), 0))
	if e.Name != "test-skill" || e.Role != "evidence" {
		t.Errorf("%+v", e)
	}
}

// twin: tests/index.test.ts:165
func TestValidateEntryRejectsEntryWithoutRole(t *testing.T) {
	if v := validateEntry(ixMap(t, ixSample, func(m map[string]any) { delete(m, "role") }), 0); v.Entry != nil {
		t.Error("should reject entry without role")
	}
}

// twin: tests/index.test.ts:172
func TestValidateEntryRejectsEntryWithInvalidRole(t *testing.T) {
	if v := validateEntry(ixMap(t, ixSample, func(m map[string]any) { m["role"] = "invalid_role" }), 0); v.Entry != nil {
		t.Error("should reject invalid role")
	}
}

// twin: tests/index.test.ts:178
func TestValidateEntryScrubsSeededPathFromLead(t *testing.T) {
	e := ixValid(t, validateEntry(ixMap(t, ixSample, func(m map[string]any) { m["lead"] = "Read /Users/admin/secret/file.ts for context" }), 0))
	if strings.Contains(e.Lead, "/Users/admin") {
		t.Errorf("path should be scrubbed: %q", e.Lead)
	}
}

// twin: tests/index.test.ts:184
func TestValidateEntryScrubsSeededCredentialFromLead(t *testing.T) {
	e := ixValid(t, validateEntry(ixMap(t, ixSample, func(m map[string]any) { m["lead"] = "Use token sk-live-abcdefghij1234567890 for auth" }), 0))
	if strings.Contains(e.Lead, "sk-live") {
		t.Errorf("credential should be scrubbed: %q", e.Lead)
	}
}

func ixOverCap() IndexEntry {
	long := strings.Repeat("word ", 20)
	return IndexEntry{
		Kind: "skill", Name: "x", Scope: "global", SourceHash: "h", Role: "evidence", Lead: "Do thing",
		UseWhen: []string{"When A: " + long, "When B: " + long, "When C: " + long, "When D: " + long},
		NotWhen: []string{"N1: " + long, "N2: " + long}, Inputs: "x",
		Examples: []string{"ex1 " + long, "ex2 " + long, "ex3 " + long}, SourceQuality: "strong",
	}
}

// twin: tests/index.test.ts:192
func TestEntryOver700CharsIsTruncatedAtBulletBoundary(t *testing.T) {
	base := ixOverCap()
	if utf16Len(jsonCompact(base)) <= 700 {
		t.Fatalf("fixture should exceed 700, got %d", utf16Len(jsonCompact(base)))
	}
	e := ixValid(t, validateEntry(ixMap(t, base), 0))
	if !e.Truncated || utf16Len(jsonCompact(e)) > 700 {
		t.Errorf("should be marked truncated and fit: %v %d", e.Truncated, utf16Len(jsonCompact(e)))
	}
}

// twin: tests/index.test.ts:213
func TestValidateIndexAcceptsValidFile(t *testing.T) {
	r := validateIndex(ixRaw(t, "2026-01-01T00:00:00Z", ixMap(t, ixSample), ixMap(t, ixSampleTool)))
	if r.File == nil || len(r.File.Entries) != 2 || len(r.Rejections) != 0 {
		t.Errorf("%+v", r)
	}
}

// twin: tests/index.test.ts:226
func TestValidateIndexRejectsFileWithInvalidEntry(t *testing.T) {
	r := validateIndex(ixRaw(t, "t", ixMap(t, ixSample), ixMap(t, ixSampleTool, func(m map[string]any) { m["role"] = "bad" })))
	if r.File != nil || len(r.Rejections) == 0 {
		t.Errorf("%+v", r)
	}
}

// twin: tests/index.test.ts:238
func TestValidateIndexRejectsWrongFormatVersion(t *testing.T) {
	r := validateIndex(map[string]any{"formatVersion": float64(2), "builtAt": "x", "model": "y", "entries": []any{}})
	if r.File != nil || len(r.Rejections) == 0 {
		t.Errorf("%+v", r)
	}
}

// twin: tests/index.test.ts:247
func TestWriteIndexThenReadIndexRoundTrips(t *testing.T) {
	_, temp := ixDirs(t)
	path := filepath.Join(temp, "pi-warden", "index", "global.json")
	file := &IndexFile{FormatVersion: 1, BuiltAt: "2026-01-01T00:00:00Z", Model: "test", Entries: []IndexEntry{ixSample}}
	if err := writeIndex(path, file); err != nil {
		t.Fatal(err)
	}
	read := readIndex(path)
	if read == nil || len(read.Entries) != 1 || read.Entries[0].Name != "test-skill" || read.Entries[0].Role != "evidence" {
		t.Errorf("%+v", read)
	}
}

// twin: tests/index.test.ts:263
// twin: tests/index.test.ts:689
func TestReadIndexReturnsNilForMissingFile(t *testing.T) {
	if readIndex(filepath.Join(t.TempDir(), "nonexistent.json")) != nil {
		t.Error("missing file should return nil")
	}
}

// twin: tests/index.test.ts:267
func TestRerunningWriteIndexOverwritesBothFiles(t *testing.T) {
	_, temp := ixDirs(t)
	g, p := filepath.Join(temp, "pi-warden", "index", "global.json"), filepath.Join(temp, "pi-warden", "index", "projects", "abc123.json")
	writeIndex(g, &IndexFile{FormatVersion: 1, BuiltAt: "t1", Model: "m1", Entries: []IndexEntry{ixSample}})
	writeIndex(p, &IndexFile{FormatVersion: 1, BuiltAt: "t2", Model: "m2", Entries: []IndexEntry{ixSampleTool}})
	if readIndex(g).Entries[0].Name != "test-skill" || readIndex(p).Entries[0].Name != "search_graph" {
		t.Error("both files should read back")
	}
	writeIndex(g, &IndexFile{FormatVersion: 1, BuiltAt: "t3", Model: "m3", Entries: []IndexEntry{}})
	if n := len(readIndex(g).Entries); n != 0 {
		t.Errorf("should be overwritten: %d", n)
	}
}

// twin: tests/index.test.ts:285
func TestTheIndexIsOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	temp := t.TempDir()
	env := map[string]string{"PIGPEN_WARDEN_INDEX_DIR": filepath.Join(temp, "owner-only")}
	d := indexDirs{Getenv: func(k string) string { return env[k] }}
	if err := d.ensure("global"); err != nil {
		t.Fatal(err)
	}
	if err := d.ensure("project"); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(temp, "owner-only", "pigpen-warden", "index")
	mode := func(p string) os.FileMode {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		return st.Mode().Perm()
	}
	if mode(dir) != 0o700 || mode(filepath.Join(dir, "projects")) != 0o700 {
		t.Errorf("folders: %o %o", mode(dir), mode(filepath.Join(dir, "projects")))
	}
	file := ixFile(ixSample)
	projectPath := d.path("project", "/owner/only")
	if err := writeIndex(projectPath, file); err != nil {
		t.Fatal(err)
	}
	if mode(projectPath) != 0o600 {
		t.Errorf("file %o", mode(projectPath))
	}
	fresh := filepath.Join(temp, "owner-only-fresh", "projects", "abc.json")
	writeIndex(fresh, file)
	if mode(filepath.Join(temp, "owner-only-fresh", "projects")) != 0o700 || mode(fresh) != 0o600 {
		t.Error("writeIndex creates a missing folder owner-only")
	}
	globalPath := d.path("global", "")
	if err := os.WriteFile(globalPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeIndex(globalPath, file)
	if mode(globalPath) != 0o600 {
		t.Errorf("an earlier world-readable index file is replaced owner-only: %o", mode(globalPath))
	}
	if n := len(readIndex(globalPath).Entries); n != 1 {
		t.Errorf("%d entries", n)
	}
}

// twin: tests/index.test.ts:309
func TestProjectEntryLandsInProjectFileGlobalSkillInGlobalFile(t *testing.T) {
	d, _ := ixDirs(t)
	g, p := d.path("global", ""), d.path("project", "/test/project")
	d.ensure("global")
	d.ensure("project")
	proj := ixSample
	proj.Name, proj.Scope = "project-skill", "project"
	writeIndex(g, ixFile(ixSample))
	writeIndex(p, ixFile(proj))
	gf, pf := readIndex(g), readIndex(p)
	if gf.Entries[0].Scope != "global" || pf.Entries[0].Scope != "project" || pf.Entries[0].Name != "project-skill" {
		t.Errorf("%+v %+v", gf, pf)
	}
	if !strings.HasSuffix(p, ".json") || len(filepath.Base(p)) != 12+len(".json") {
		t.Errorf("the project file is keyed by 12 hex digits: %s", p)
	}
}

// twin: tests/index.test.ts:327
func TestIndexStatsCountsEntriesThinSourcesAndTruncations(t *testing.T) {
	thin := ixSample
	thin.Name, thin.Thin, thin.Truncated = "thin-skill", true, true
	s := indexStats(ixFile(ixSample, ixSampleTool, thin))
	if s.GlobalEntries != 3 || s.ProjectEntries != 0 || !reflect.DeepEqual(s.ThinSources, []string{"thin-skill"}) || s.TruncatedCount != 1 {
		t.Errorf("%+v", s)
	}
}

// twin: tests/index.test.ts:347
func TestFileContentHashReturnsConsistentHashForSameContent(t *testing.T) {
	p := ixSkillFile(t)
	h1, h2 := fileContentHash(p), fileContentHash(p)
	if h1 != h2 || len(h1) != 16 {
		t.Errorf("%s %s", h1, h2)
	}
}

// twin: tests/index.test.ts:354
func TestFileContentHashReturnsMissingForNonexistentFile(t *testing.T) {
	if got := fileContentHash("/nonexistent/file.md"); got != "missing" {
		t.Errorf("%q", got)
	}
}

// Go-only: the hash is the first 16 hex digits of the SHA-256 of the content, as the original computes it.
func TestHashesAreTheOriginalsSHA256Prefixes(t *testing.T) {
	p := ixSkillFile(t)
	if got := fileContentHash(p); got != "0443c378c2cadaa5" {
		t.Errorf("%s", got)
	}
	if got := toolSourceHash("bash", "Run commands"); len(got) != 16 || got != toolSourceHash("bash", "Run commands") || got == toolSourceHash("bash", "Run command") {
		t.Errorf("%s", got)
	}
	// A file that is not valid UTF-8 hashes as Node reads it: each invalid sequence is one replacement character.
	bad := filepath.Join(t.TempDir(), "bad.md")
	os.WriteFile(bad, []byte("a\xff\xe2\x82b"), 0o644)
	good := filepath.Join(t.TempDir(), "good.md")
	os.WriteFile(good, []byte("a\ufffd\ufffdb"), 0o644)
	if fileContentHash(bad) != fileContentHash(good) {
		t.Error("invalid bytes must hash as replacement characters")
	}
}

// twin: tests/index.test.ts:358
func TestSkillSourceHashMatchesFileContentHashForASkill(t *testing.T) {
	p := ixSkillFile(t)
	if skillSourceHash(Skill{FilePath: p}) != fileContentHash(p) {
		t.Error("hashes differ")
	}
}

// twin: tests/index.test.ts:365
func TestFindEntryReturnsMatchingEntryByNameAndSourceHash(t *testing.T) {
	if e := findEntry("test-skill", "abc123def4567890", ixFile(ixSample), nil); e == nil || e.Name != "test-skill" {
		t.Errorf("%+v", e)
	}
}

// twin: tests/index.test.ts:372
func TestFindEntryReturnsNilForMismatchedHash(t *testing.T) {
	if findEntry("test-skill", "wronghash", ixFile(ixSample), nil) != nil {
		t.Error("mismatched hash")
	}
}

// twin: tests/index.test.ts:377
func TestFindEntryChecksProjectIndexBeforeGlobal(t *testing.T) {
	proj := ixSample
	proj.Scope = "project"
	if e := findEntry("test-skill", "abc123def4567890", ixFile(ixSample), ixFile(proj)); e == nil || e.Scope != "project" {
		t.Errorf("project entry should take precedence: %+v", e)
	}
}

// twin: tests/index.test.ts:386
func TestIndexCoverageReportsCoveredAndMissingSkills(t *testing.T) {
	p := ixSkillFile(t)
	_, missing := indexCoverage([]Skill{{Name: "test-skill", FilePath: p}, {Name: "missing-skill", FilePath: "/nonexistent/file.md"}}, ixFile(ixSample), nil)
	if !containsString(missing, "missing-skill") {
		t.Errorf("%v", missing)
	}
}

func ixSkillCandidate(t *testing.T, name, path string, global *IndexFile) Candidate {
	r := eligibleCandidates([]Skill{{Name: name, Description: "A test skill", FilePath: path}}, []CapabilityTool{{"bash", "Run commands"}}, defaultConscienceConfig(), nil, nil, global, nil, "linux")
	for _, c := range r.Candidates {
		if c.ID == name {
			return c
		}
	}
	t.Fatalf("no candidate %s", name)
	return Candidate{}
}

// twin: tests/index.test.ts:400
func TestCandidateWithEntrySendsLeadUseWhenExamplesAndNeverTheLocation(t *testing.T) {
	// The fixture hash does not match the file, so the candidate is unindexed: real entries match by hash.
	c := ixSkillCandidate(t, "test-skill", ixSkillFile(t), ixFile(ixSample))
	if c.ID != "test-skill" {
		t.Errorf("%+v", c)
	}
}

// twin: tests/index.test.ts:420
func TestCandidateWithMatchingIndexEntryCarriesLeadUseWhenExamples(t *testing.T) {
	path := ixSkillFile(t)
	entry := ixSample
	entry.SourceHash = fileContentHash(path)
	c := ixSkillCandidate(t, "test-skill", path, ixFile(entry))
	if c.IndexEntry == nil || c.IndexEntry.Lead != "Test things with node:test runner" ||
		!reflect.DeepEqual(c.IndexEntry.UseWhen, entry.UseWhen) || !reflect.DeepEqual(c.IndexEntry.Examples, entry.Examples) || c.IndexEntry.Location != "" {
		t.Errorf("%+v", c.IndexEntry)
	}
	// What reaches the judge: lead, useWhen and examples, never the advertised location.
	state := buildState("t", "", nil, nil, []batchItem{{"c1", c}}, nil)
	sent := state["candidates"].(map[string]any)["c1"].(map[string]any)
	if sent["lead"] != entry.Lead || sent["unindexed"] != nil || sent["_location"] != nil || strings.Contains(jsonCompact(state), path) {
		t.Errorf("%v", sent)
	}
}

// twin: tests/index.test.ts:445
func TestCandidateWithoutEntryFallsBackAndIsMarkedUnindexed(t *testing.T) {
	c := ixSkillCandidate(t, "no-index", ixSkillFile(t), nil)
	if c.IndexEntry != nil {
		t.Error("should not have indexEntry")
	}
	sent := buildState("t", "", nil, nil, []batchItem{{"c1", c}}, nil)["candidates"].(map[string]any)["c1"].(map[string]any)
	if sent["unindexed"] != true || sent["description"] != "A test skill" {
		t.Errorf("%v", sent)
	}
}

// twin: tests/index.test.ts:463
func TestScoreQuestionInstructionContainsRequestNotTopicSentence(t *testing.T) {
	q, _, _ := buildBatchQuestions(csBatch("skill", "test"), "test task", "", nil, nil)
	in := q["c1"].Instructions
	for _, w := range []string{"against what the user is asking", "not against the subjects the prompt mentions in passing", "lowest level"} {
		if !strings.Contains(in, w) {
			t.Errorf("should contain %q", w)
		}
	}
}

// twin: tests/index.test.ts:472
func TestScoreQuestionInstructionContainsRoleBasedGuidance(t *testing.T) {
	q, _, _ := buildBatchQuestions(csBatch("skill", "test"), "test task", "", nil, nil)
	in := q["c1"].Instructions
	for _, w := range []string{"research-role", "evidence-role", "repository cannot supply"} {
		if !strings.Contains(in, w) {
			t.Errorf("should mention %q", w)
		}
	}
}

// twin: tests/index.test.ts:481
func TestDispositionQuestionInstructionContainsStatusUpdateClause(t *testing.T) {
	q, _, _ := buildBatchQuestions(nil, "test task", "", nil, nil)
	in := q["conscience_disposition"].Instructions
	for _, w := range []string{"reports status", "no_gap", "without asking the agent to do anything"} {
		if !strings.Contains(in, w) {
			t.Errorf("should mention %q", w)
		}
	}
}

// twin: tests/index.test.ts:489
func TestDispositionQuestionIsUnchangedBeyondTheStatusClause(t *testing.T) {
	q, _, _ := buildBatchQuestions(nil, "test task", "", nil, nil)
	d := q["conscience_disposition"]
	if d.Type != "choice" || len(d.Criteria) != 4 {
		t.Errorf("%+v", d)
	}
}

var ixForbidden = []string{"tiny_search", "tiny_fetch", "search_graph", "subagent", "bash"}

// twin: tests/index.test.ts:501
func TestNoToolNamesAppearInBuiltQuestionText(t *testing.T) {
	q, _, _ := buildBatchQuestions(csBatch("skill", "test"), "test task", "", nil, nil)
	for key, question := range q {
		text := jsonCompact(map[string]any{"type": question.Type, "instructions": question.Instructions, "criteria": question.Criteria, "levels": question.Levels})
		for _, name := range ixForbidden {
			if strings.Contains(text, name) {
				t.Errorf("question %s should not contain tool name %q", key, name)
			}
		}
	}
}

// twin: tests/index.test.ts:513
func TestNoToolNamesAppearInConscienceGoSource(t *testing.T) {
	src, err := os.ReadFile("conscience.go")
	if err != nil {
		t.Fatal(err)
	}
	var code []string
	for _, line := range strings.Split(string(src), "\n") {
		if tr := strings.TrimSpace(line); !strings.HasPrefix(tr, "//") {
			code = append(code, line)
		}
	}
	joined := strings.Join(code, "\n")
	for _, name := range ixForbidden {
		if strings.Contains(joined, `"`+name+`"`) {
			t.Errorf("conscience.go code should not contain tool name %q in a string literal", name)
		}
	}
}

// twin: tests/index.test.ts:527
func TestNudgeFiresOncePerSessionWithMissingIndexCoverage(t *testing.T) {
	covered, missing := indexCoverage([]Skill{{Name: "skill-a", FilePath: ixSkillFile(t)}}, nil, nil)
	if len(covered) != 0 || len(missing) != 1 || missing[0] != "skill-a" {
		t.Errorf("%v %v", covered, missing)
	}
}

// twin: tests/index.test.ts:541
func TestValidateEntryAcceptsStrongWeakAndThinSourceQuality(t *testing.T) {
	strong := ixValid(t, validateEntry(ixMap(t, ixSample, func(m map[string]any) { m["sourceQuality"] = "strong" }), 0))
	weak := ixValid(t, validateEntry(ixMap(t, ixSample, func(m map[string]any) { m["sourceQuality"], m["improve"] = "weak", "add situations and examples" }), 0))
	thin := ixValid(t, validateEntry(ixMap(t, ixSample, func(m map[string]any) {
		m["sourceQuality"], m["improve"] = "thin", "expand to include when-to-use triggers"
	}), 0))
	if strong.SourceQuality != "strong" || weak.SourceQuality != "weak" || weak.Improve != "add situations and examples" ||
		thin.SourceQuality != "thin" || thin.Improve != "expand to include when-to-use triggers" {
		t.Errorf("%+v %+v %+v", strong, weak, thin)
	}
}

// twin: tests/index.test.ts:554
func TestValidateEntryRejectsInvalidSourceQuality(t *testing.T) {
	if validateEntry(ixMap(t, ixSample, func(m map[string]any) { m["sourceQuality"] = "meh" }), 0).Entry != nil {
		t.Error("invalid sourceQuality should be rejected")
	}
}

// twin: tests/index.test.ts:559
func TestValidateEntryDefaultsSourceQualityToStrongWhenAbsent(t *testing.T) {
	e := ixValid(t, validateEntry(ixMap(t, ixSample, func(m map[string]any) { delete(m, "sourceQuality") }), 0))
	if e.SourceQuality != "strong" {
		t.Errorf("%q", e.SourceQuality)
	}
}

// twin: tests/index.test.ts:565
// twin: tests/index.test.ts:611
func TestValidateEntryCapsImproveAt200Characters(t *testing.T) {
	for _, n := range []int{250, 201} {
		e := ixValid(t, validateEntry(ixMap(t, ixSample, func(m map[string]any) { m["sourceQuality"], m["improve"] = "weak", strings.Repeat("x", n) }), 0))
		if len(e.Improve) != 200 {
			t.Errorf("improve of %d should be capped at 200, got %d", n, len(e.Improve))
		}
	}
}

func ixQualityIndex() *IndexFile {
	mk := func(base IndexEntry, name, sq, improve string) IndexEntry {
		base.Name, base.SourceQuality, base.Improve, base.Thin = name, sq, improve, sq == "thin"
		return base
	}
	return ixFile(mk(ixSample, "test-skill", "strong", ""), mk(ixSample, "weak-skill", "weak", "add examples"), mk(ixSample, "thin-skill", "thin", "too short, expand"), mk(ixSampleTool, "search_graph", "strong", ""))
}

// twin: tests/index.test.ts:571
func TestIndexStatsGroupsWeakAndThinEntriesIntoSourceQualityReport(t *testing.T) {
	s := indexStats(ixQualityIndex())
	var names []string
	for _, r := range s.SourceQualityReport {
		names = append(names, r.Name)
	}
	if !reflect.DeepEqual(names, []string{"weak-skill", "thin-skill"}) {
		t.Errorf("%v", names)
	}
}

// twin: tests/index.test.ts:591
func TestIndexStatsSourceQualityReportGroupsThinAndWeakEntries(t *testing.T) {
	mk := func(name, sq, improve string) IndexEntry {
		e := ixSample
		e.Name, e.SourceQuality, e.Improve = name, sq, improve
		return e
	}
	s := indexStats(ixFile(mk("weak-a", "weak", "add triggers"), mk("thin-a", "thin", "expand"), mk("weak-b", "weak", "add examples")))
	thin, weak := 0, 0
	for _, r := range s.SourceQualityReport {
		switch r.SourceQuality {
		case "thin":
			thin++
			if r.Name != "thin-a" {
				t.Errorf("%v", r)
			}
		case "weak":
			weak++
		}
	}
	if len(s.SourceQualityReport) != 3 || thin != 1 || weak != 2 {
		t.Errorf("%+v", s.SourceQualityReport)
	}
}

// twin: tests/index.test.ts:616
func TestThinBooleanIsDerivedFromSourceQuality(t *testing.T) {
	thin := ixValid(t, validateEntry(ixMap(t, ixSample, func(m map[string]any) { m["sourceQuality"], m["improve"] = "thin", "expand" }), 0))
	strong := ixValid(t, validateEntry(ixMap(t, ixSample, func(m map[string]any) { m["sourceQuality"] = "strong" }), 0))
	// The model writes thin:true with sourceQuality:strong: the derived value wins.
	mismatch := ixValid(t, validateEntry(ixMap(t, ixSample, func(m map[string]any) { m["sourceQuality"], m["thin"] = "strong", true }), 0))
	if !thin.Thin || strong.Thin || mismatch.Thin {
		t.Errorf("%v %v %v", thin.Thin, strong.Thin, mismatch.Thin)
	}
}

// twin: tests/index.test.ts:632
func TestValidateIndexRejectionNamesTheEntryAndField(t *testing.T) {
	r := validateIndex(ixRaw(t, "t", ixMap(t, ixSample), ixMap(t, ixSample, func(m map[string]any) { m["name"], m["useWhen"] = "bad-skill", []any{"only one"} })))
	if r.File != nil || len(r.Rejections) == 0 {
		t.Fatal("should reject")
	}
	joined := strings.Join(r.Rejections, ", ")
	if !strings.Contains(joined, "bad-skill") || !strings.Contains(joined, "useWhen") || joined != `"bad-skill": useWhen must have 2 to 4 items` {
		t.Errorf("%s", joined)
	}
}

// twin: tests/index.test.ts:649
func TestValidateIndexRejectionForInvalidKindNamesTheField(t *testing.T) {
	r := validateIndex(ixRaw(t, "t", map[string]any{"kind": "invalid", "name": "x", "scope": "global", "sourceHash": "h", "role": "evidence", "lead": "Do thing",
		"useWhen": []any{"A", "B"}, "notWhen": []any{}, "inputs": "x", "examples": []any{"ex1", "ex2"}, "thin": false}))
	if r.File != nil || !strings.Contains(strings.Join(r.Rejections, ", "), "kind") {
		t.Errorf("%+v", r)
	}
}

// twin: tests/index.test.ts:663
func TestValidateIndexOverCapEntryIsTruncatedNotRejected(t *testing.T) {
	long := strings.Repeat("word ", 10)
	over := ixMap(t, ixSample, func(m map[string]any) {
		m["useWhen"] = []any{"When A: " + long, "When B: " + long, "When C: " + long, "When D: " + long}
		m["notWhen"] = []any{"N1: " + long, "N2: " + long}
		m["examples"] = []any{"ex1 " + long, "ex2 " + long, "ex3 " + long}
	})
	r := validateIndex(ixRaw(t, "t", over))
	if r.File == nil || !r.File.Entries[0].Truncated || len(r.Rejections) != 0 {
		t.Errorf("%+v", r)
	}
}

// twin: tests/index.test.ts:679
func TestValidateEntryOverCapEntryIsTruncatedNotRejected(t *testing.T) {
	long := strings.Repeat("word ", 10)
	over := ixMap(t, ixSample, func(m map[string]any) {
		m["useWhen"] = []any{"When A: " + long, "When B: " + long, "When C: " + long, "When D: " + long}
		m["notWhen"] = []any{"N1: " + long, "N2: " + long}
		m["examples"] = []any{"ex1 " + long, "ex2 " + long, "ex3 " + long}
	})
	if e := ixValid(t, validateEntry(over, 0)); !e.Truncated {
		t.Error("should be marked truncated")
	}
}

// twin: tests/index.test.ts:696
func TestIndexCoverageWithCurrentIndexShowsAllCovered(t *testing.T) {
	p := ixSkillFile(t)
	e := ixSample
	e.SourceHash = fileContentHash(p)
	covered, missing := indexCoverage([]Skill{{Name: "test-skill", FilePath: p}}, ixFile(e), nil)
	if len(covered) != 1 || len(missing) != 0 {
		t.Errorf("%v %v", covered, missing)
	}
}

// Go-only: an entry the way the original writes it to disk (key order, 2-space indent, the truncated flag last).
func TestWriteIndexKeepsTheOriginalKeyOrder(t *testing.T) {
	e := ixSample
	e.Truncated, e.Improve = true, "add situations"
	e.SourceQuality = "weak"
	path := filepath.Join(t.TempDir(), "i.json")
	writeIndex(path, &IndexFile{FormatVersion: 1, BuiltAt: "2026-01-01T00:00:00Z", Model: "m", Entries: []IndexEntry{e}})
	raw, _ := os.ReadFile(path)
	var order []string
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.Token()
	for dec.More() {
		k, _ := dec.Token()
		order = append(order, k.(string))
		var skip json.RawMessage
		dec.Decode(&skip)
	}
	if !reflect.DeepEqual(order, []string{"formatVersion", "builtAt", "model", "entries"}) {
		t.Errorf("file keys %v", order)
	}
	s := string(raw)
	for _, want := range []string{"\"sourceQuality\": \"weak\",\n      \"improve\": \"add situations\",\n      \"truncated\": true\n", "\"notWhen\": [\n"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in\n%s", want, s)
		}
	}
	if strings.HasSuffix(s, "\n") {
		t.Error("JSON.stringify leaves no trailing newline")
	}
}

// Go-only: the entries-only fallback of /warden index accepts nothing the original would not: a file without the
// metadata is not an index file at all.
func TestReadIndexRawNeedsTheFullShape(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(body), 0o644)
		return p
	}
	if _, ok := readIndexRaw(write("full.json", `{"formatVersion":1,"builtAt":"t","model":"m","entries":[]}`)); !ok {
		t.Error("a full file reads")
	}
	for name, body := range map[string]string{
		"entries-only.json": `{"entries":[]}`, "v2.json": `{"formatVersion":2,"builtAt":"t","model":"m","entries":[]}`,
		"no-model.json": `{"formatVersion":1,"builtAt":"t","entries":[]}`, "not-json.json": `{`, "array.json": `[]`,
	} {
		if _, ok := readIndexRaw(write(name, body)); ok {
			t.Errorf("%s should not read as an index file", name)
		}
	}
}
