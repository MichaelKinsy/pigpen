package warden

// Twins of the rules-file and init cases of tests/violation.test.ts (resolveRulesFile, extractRules,
// checkPiWardenMissing, the starter rules file and the init prompt) and of the init case of
// tests/violation-pipeline.test.ts.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func resolved(t *testing.T, dir string, files []string, fallback bool) *ResolvedRulesFile {
	t.Helper()
	r, ok := resolveRulesFile(dir, files, fallback)
	if !ok {
		return nil
	}
	return &r
}

// twin: tests/violation.test.ts:431
func TestResolveRulesFilePiWardenWinsOverFallbacks(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "AGENTS.md"), "# Agents\nAlways test.\n")
	writeFile(t, filepath.Join(dir, "pi-warden.md"), "# Project Rules\nNo console.log.\n")
	got := resolved(t, dir, nil, true)
	mustEqual(t, got.Source, "pi-warden.md", "source")
	mustMatch(t, got.Content, `No console\.log`, "content")
}

// twin: tests/violation.test.ts:441
func TestResolveRulesFileAgentsMdWhenPiWardenIsMissing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "AGENTS.md"), "# Agents\nAlways test.\n")
	mustEqual(t, resolved(t, dir, nil, true).Source, "AGENTS.md", "source")
}

// twin: tests/violation.test.ts:449
func TestResolveRulesFileNullWhenNothingExists(t *testing.T) {
	if resolved(t, t.TempDir(), nil, true) != nil {
		t.Fatal("expected no rules file")
	}
}

// twin: tests/violation.test.ts:455
func TestResolveRulesFileTruncatesLargeFiles(t *testing.T) {
	dir := t.TempDir()
	large := "# Rule\n" + strings.Repeat("x", 20000)
	writeFile(t, filepath.Join(dir, "pi-warden.md"), large)
	if utf16Len(resolved(t, dir, nil, true).Content) >= utf16Len(large) {
		t.Fatal("content is truncated")
	}
}

// twin: tests/violation.test.ts:467
func TestResolveRulesFileEveryConfiguredFileRidesAlongInOrder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "global-rules.md"), "# Global\nGlobal body.\n")
	writeFile(t, filepath.Join(dir, "warden-local-rules.md"), "# Local\nLocal body.\n")
	writeFile(t, filepath.Join(dir, "AGENTS.md"), "# Agents\nNever configured.\n")
	got := resolved(t, dir, []string{"global-rules.md", "warden-local-rules.md"}, true)
	mustEqual(t, got.Source, "global-rules.md, warden-local-rules.md", "source")
	mustMatch(t, got.Content, `Global body`, "global")
	mustMatch(t, got.Content, `Local body`, "local")
	mustNotContain(t, got.Content, "Never configured", "fallback")
}

// twin: tests/violation.test.ts:480
func TestResolveRulesFileARootPiWardenStillWinsOverConfiguredFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pi-warden.md"), "# Project\nNo console.log.\n")
	writeFile(t, filepath.Join(dir, "global-rules.md"), "# Global\nGlobal body.\n")
	got := resolved(t, dir, []string{"global-rules.md"}, true)
	mustEqual(t, got.Source, "pi-warden.md", "source")
	mustNotContain(t, got.Content, "Global body", "configured")
}

// twin: tests/violation.test.ts:490
func TestResolveRulesFileFallbackFalseReachesNoFallbackDocument(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "AGENTS.md"), "# Agents\nAlways test.\n")
	if resolved(t, dir, nil, false) != nil {
		t.Fatal("expected no rules file")
	}
}

// twin: tests/violation.test.ts:497
func TestResolveRulesFileAMissingConfiguredFileFallsThrough(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "AGENTS.md"), "# Agents\nAlways test.\n")
	mustEqual(t, resolved(t, dir, []string{"gone.md"}, true).Source, "AGENTS.md", "source")
}

// twin: tests/violation.test.ts:505
func TestResolveRulesFileABlankFallbackDocumentIsNotASource(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "AGENTS.md"), "   \n")
	writeFile(t, filepath.Join(dir, "CLAUDE.md"), "# Claude\nAlways test.\n")
	mustEqual(t, resolved(t, dir, nil, true).Source, "CLAUDE.md", "source")
}

// twin: tests/violation.test.ts:514
func TestResolveRulesFileADuplicateEntryIsCarriedAsWritten(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "global-rules.md"), "# Global\nGlobal body.\n")
	mustEqual(t, resolved(t, dir, []string{"global-rules.md", "global-rules.md"}, true).Source, "global-rules.md, global-rules.md", "source")
}

// twin: tests/violation.test.ts:522
func TestResolveRulesFileADirectoryEntryFallsThrough(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "rules-here"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "AGENTS.md"), "# Agents\nAlways test.\n")
	mustEqual(t, resolved(t, dir, []string{"rules-here"}, true).Source, "AGENTS.md", "source")
}

// twin: tests/violation.test.ts:534
func TestExtractRulesKeepsHeadingsAndTheirParagraphsWithinBudget(t *testing.T) {
	content := "# Rule 1\nBody 1.\n\n# Rule 2\nBody 2.\n\n# Rule 3\n" + strings.Repeat("x", 20000)
	extracted := extractRules(content, 100)
	mustMatch(t, extracted, `# Rule 1`, "rule 1")
	mustMatch(t, extracted, `# Rule 2`, "rule 2")
}

// twin: tests/violation.test.ts:542
func TestExtractRulesShortContentPassesThroughUnchanged(t *testing.T) {
	content := "# Short Rule\nBody."
	mustEqual(t, extractRules(content, 4000), content, "content")
}

func missing(t *testing.T, dir string, mutate func(*RulesConfig)) (bool, string) {
	t.Helper()
	cfg := rulesConfig()
	if mutate != nil {
		mutate(&cfg)
	}
	return checkPiWardenMissing(dir, cfg)
}

// twin: tests/violation.test.ts:550
func TestCheckPiWardenMissingFalseWhenPiWardenExists(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pi-warden.md"), "# Rules\n")
	got, _ := missing(t, dir, nil)
	mustEqual(t, got, false, "missing")
}

// twin: tests/violation.test.ts:558
func TestCheckPiWardenMissingFallbackSource(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "AGENTS.md"), "# Agents\n")
	got, source := missing(t, dir, nil)
	mustEqual(t, got, true, "missing")
	mustEqual(t, source, "AGENTS.md", "fallback source")
}

// twin: tests/violation.test.ts:567
func TestCheckPiWardenMissingNoFallbackWhenNothingExists(t *testing.T) {
	got, source := missing(t, t.TempDir(), nil)
	mustEqual(t, got, true, "missing")
	mustEqual(t, source, "", "fallback source")
}

// twin: tests/violation.test.ts:578
func TestCheckPiWardenMissingAResolvedRulesFilesEntryIsNotAFallback(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "AGENTS.md"), "# Agents\n")
	writeFile(t, filepath.Join(dir, "warden-local-rules.md"), "# Local rule\nBody.\n")
	got, source := missing(t, dir, func(c *RulesConfig) {
		c.Files = []string{filepath.Join(dir, "global-rules.md"), "warden-local-rules.md"}
	})
	mustEqual(t, got, false, "missing")
	mustEqual(t, source, "", "fallback source")
}

// twin: tests/violation.test.ts:590
func TestCheckPiWardenMissingARulesFilesEntryNamedAgentsMdIsConfigured(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "AGENTS.md"), "# Agents\n")
	got, source := missing(t, dir, func(c *RulesConfig) { c.Files = []string{"AGENTS.md"} })
	mustEqual(t, got, false, "missing")
	mustEqual(t, source, "", "fallback source")
}

// twin: tests/violation.test.ts:599
func TestCheckPiWardenMissingStillNamesTheFallbackWhenNoConfiguredFileResolves(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "AGENTS.md"), "# Agents\n")
	got, source := missing(t, dir, func(c *RulesConfig) { c.Files = []string{"absent-rules.md"} })
	mustEqual(t, got, true, "missing")
	mustEqual(t, source, "AGENTS.md", "fallback source")
}

// twin: tests/violation.test.ts:608
func TestCheckPiWardenMissingFallbackFalseReachesNoFallbackDocument(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "AGENTS.md"), "# Agents\n")
	got, source := missing(t, dir, func(c *RulesConfig) { c.Fallback = false })
	mustEqual(t, got, true, "missing")
	mustEqual(t, source, "", "fallback source")
}

// twin: tests/violation.test.ts:620
func TestCheckPiWardenMissingAnEmptyPiWardenIsStillAProjectRulesFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pi-warden.md"), "")
	writeFile(t, filepath.Join(dir, "AGENTS.md"), "# Agents\n")
	got, _ := missing(t, dir, nil)
	mustEqual(t, got, false, "missing")
}

// twin: tests/violation.test.ts:629
func TestCheckPiWardenMissingAnEmptyConfiguredFileIsTheConfiguredTier(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "rules.md"), "")
	writeFile(t, filepath.Join(dir, "AGENTS.md"), "# Agents\n")
	got, source := missing(t, dir, func(c *RulesConfig) { c.Files = []string{"rules.md"} })
	mustEqual(t, got, false, "missing")
	mustEqual(t, source, "", "fallback source")
}

// twin: tests/violation.test.ts:642
func TestDetectProjectTypeTypescript(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "tsconfig.json"), "{}")
	mustEqual(t, detectProjectType(dir), "typescript", "type")
}

// twin: tests/violation.test.ts:649
func TestDetectProjectTypeGenericWhenNoManifestExists(t *testing.T) {
	mustEqual(t, detectProjectType(t.TempDir()), "generic", "type")
}

// twin: tests/violation.test.ts:655
func TestGenerateStarterRulesIncludesSafetyRulesAndProjectType(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "tsconfig.json"), "{}")
	content := generateStarterRules(dir)
	mustMatch(t, content, `No hardcoded secrets`, "secrets")
	mustMatch(t, content, `Comments explain why, not what`, "comments")
	mustMatch(t, content, `Project type: typescript`, "type")
	mustMatch(t, content, `No explicit any`, "any")
}

// twin: tests/violation.test.ts:666
func TestGenerateStarterRulesGenericProjectHasNoTypeSpecificRules(t *testing.T) {
	content := generateStarterRules(t.TempDir())
	mustMatch(t, content, `No hardcoded secrets`, "secrets")
	mustMatch(t, content, `Project type: generic`, "type")
	mustNotContain(t, content, "No explicit any", "any")
}

// twin: tests/violation.test.ts:675
func TestWriteStarterRulesWritesPiWardenAndReturnsTheResult(t *testing.T) {
	dir := t.TempDir()
	result, err := writeStarterRules(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	mustEqual(t, result.AlreadyExists, false, "alreadyExists")
	mustMatch(t, result.Path, `pi-warden\.md$`, "path")
	mustMatch(t, result.Content, `No hardcoded secrets`, "content")
	written, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	mustMatch(t, string(written), `No hardcoded secrets`, "the file was actually written")
}

// twin: tests/violation.test.ts:687
func TestWriteStarterRulesReturnsAlreadyExistsWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pi-warden.md"), "# Existing rules\nKeep these.\n")
	result, err := writeStarterRules(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	mustEqual(t, result.AlreadyExists, true, "alreadyExists")
	mustMatch(t, result.Content, `Existing rules`, "content")
	content, _ := os.ReadFile(result.Path)
	mustMatch(t, string(content), `Existing rules`, "the file is unchanged")
}

// twin: tests/violation.test.ts:699
func TestWriteStarterRulesOverwriteReplacesTheFileButPreservesOldRules(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pi-warden.md"), "# Old rules\n")
	result, err := writeStarterRules(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	mustEqual(t, result.AlreadyExists, true, "alreadyExists")
	mustMatch(t, result.Content, `No hardcoded secrets`, "starter")
	mustMatch(t, result.Content, `Old rules`, "existing rules are preserved in the new starter")
}

// twin: tests/violation.test.ts:709
func TestBuildProjectContextReadsPackageJSON(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "package.json"), `{"name":"my-app","scripts":{"test":"vitest","build":"tsc"}}`)
	ctx := buildProjectContext(dir)
	mustMatch(t, ctx, `name: my-app`, "name")
	mustMatch(t, ctx, `scripts: test, build`, "scripts in file order")
}

// twin: tests/violation.test.ts:718
func TestBuildInitPromptIncludesProjectContextAndSafetyRules(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "tsconfig.json"), "{}")
	prompt := buildInitPrompt(dir)
	mustMatch(t, prompt, `Project type: typescript`, "type")
	mustMatch(t, prompt, `No hardcoded secrets`, "secrets")
	mustMatch(t, prompt, `No explicit`, "explicit")
	mustMatch(t, prompt, `Under 50 rules`, "cap")
}

// twin: tests/violation.test.ts:729
func TestBuildInitPromptCompilesAgentsMdInstructionsAsNumberedLines(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "AGENTS.md"), strings.Join([]string{"# Project", "Always run the tests before you commit.", "Use tabs, never spaces.", ""}, "\n"))
	prompt := buildInitPrompt(dir)
	for _, pattern := range []string{
		`2: Always run the tests before you commit\.`, `3: Use tabs, never spaces\.`, `source: <file>:<line>`, `source: AGENTS\.md:65`, "`paths:`",
		`judged from one changed file alone`, `wording close to the source instruction`, `at most 31 rules`,
		`list each left-out instruction in your reply with the reason`, `needs other files, repository history, or the task to judge`,
		`linter, formatter, or type checker already enforces`, "suggest running `/warden rules check`", `never run it as part of this task`,
	} {
		mustMatch(t, prompt, pattern, pattern)
	}
}

// twin: tests/violation.test.ts:749
func TestBuildInitPromptClaudeMdCompilesTheSameWay(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "CLAUDE.md"), "Never commit with a dirty tree.\n")
	prompt := buildInitPrompt(dir)
	mustMatch(t, prompt, `from CLAUDE\.md, each line prefixed with its line number`, "source")
	mustMatch(t, prompt, `1: Never commit with a dirty tree\.`, "line")
}

// twin: tests/violation.test.ts:758
func TestBuildInitPromptUnchangedWhenPiWardenExists(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pi-warden.md"), "# Prior rule\nKeep the supported protocol version.\n")
	writeFile(t, filepath.Join(dir, "AGENTS.md"), "# Project\nAlways run the tests before you commit.\n")
	prompt := buildInitPrompt(dir)
	mustMatch(t, prompt, `## Existing pi-warden\.md rules \(preserve these\)`, "heading")
	mustMatch(t, prompt, `Keep the supported protocol version\.`, "prior rule")
	mustNotContain(t, prompt, "source: <file>:<line>", "no compile section")
	mustNotContain(t, prompt, "left-out instruction", "no leave-out list")
	if regexp.MustCompile(`\n\d+: `).MatchString(prompt) {
		t.Fatal("no numbered instruction lines")
	}
}

// twin: tests/violation.test.ts:771
func TestBuildInitPromptGenericProjectOmitsTypeSpecificRules(t *testing.T) {
	prompt := buildInitPrompt(t.TempDir())
	mustMatch(t, prompt, `Project type: generic`, "type")
	mustNotContain(t, prompt, "No explicit", "explicit")
	mustMatch(t, prompt, `No hardcoded secrets`, "secrets")
}

// twin: tests/violation.test.ts:783
func TestResolveRulesFileContentIsRedactedBeforeLeavingTheMachine(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pi-warden.md"), "# Rule\napi_key=sk-live-1234567890abcdef\npassword: hunter2\n")
	got := resolved(t, dir, nil, true)
	if got == nil {
		t.Fatal("resolved a rules file")
	}
	mustNotContain(t, got.Content, "sk-live-1234567890abcdef", "API key is redacted")
	mustNotContain(t, got.Content, "hunter2", "password is redacted")
	mustMatch(t, got.Content, `\[redacted\]`, "redacted marker present")
}

// twin: tests/violation.test.ts:797
func TestExtractRulesLargeFirstBlockStillReturnsNonEmptyOutput(t *testing.T) {
	large := "# Rule One\n" + strings.Repeat("x", 20000)
	if len(extractRules(large, 100)) == 0 {
		t.Fatal("extractRules does not collapse to empty when the first block exceeds budget")
	}
}

// twin: tests/violation-pipeline.test.ts:120
func TestInitPreservesFallbackPolicyAndIncludesPriorRulesInTheTailoringPrompt(t *testing.T) {
	dir := t.TempDir()
	policy := "# Project policy\nUse the approved project logger for diagnostics.\n"
	writeFile(t, filepath.Join(dir, "AGENTS.md"), policy)
	mustMatch(t, buildInitPrompt(dir), `approved project logger`, "prompt")
	result, err := writeStarterRules(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	mustMatch(t, result.Content, `approved project logger`, "starter")
	mustMatch(t, resolved(t, dir, nil, true).Content, `approved project logger`, "resolved")
	writeFile(t, filepath.Join(dir, "pi-warden.md"), "# Prior rule\nKeep the supported protocol version.\n")
	mustMatch(t, buildInitPrompt(dir), `supported protocol version`, "prompt with a prior rule")
	data, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	mustMatch(t, string(data), `approved project logger`, "AGENTS.md is untouched")
}
