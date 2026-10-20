package warden

import (
	"os"
	"strings"
)

// The rules content for escalation and context (src/rules-file.ts). The order is the rules guard's: pi-warden.md alone,
// else the files in `rules.files`, in order, else the fallback names while `rules.fallback` is on. An empty document
// counts where the guard counts it: a configured file is a source whatever it holds, a fallback document has to say
// something.

// ResolvedRulesFile is the rules text a request carries.
type ResolvedRulesFile struct {
	Content string
	// Source is the file name, or the configured names joined by commas, as the config spells them.
	Source string
}

// maxResolvedChars is ~4000 tokens.
const maxResolvedChars = 16_000

var reRuleBlockHead = lazyRE(`^#{1,3}[` + jsSpaceClass + `]`)

// readRules is a rules file's text, or false when it is missing, unreadable, or a directory.
func readRules(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return decodeUTF8(data), true
}

// extractRules is the token-aware truncation: heading blocks, capped at maxTokens (1 token ~ 4 chars).
func extractRules(content string, maxTokens int) string {
	var blocks []string
	var current []string
	inRule := false
	for _, line := range strings.Split(content, "\n") {
		if reRuleBlockHead.MatchString(line) {
			if len(current) > 0 {
				blocks = append(blocks, strings.Join(current, "\n"))
			}
			current = []string{line}
			inRule = true
		} else if inRule {
			current = append(current, line)
		}
	}
	if len(current) > 0 {
		blocks = append(blocks, strings.Join(current, "\n"))
	}
	var result []string
	count := 0
	budget := maxTokens * 4
	for _, block := range blocks {
		n := utf16Len(block)
		if count+n > budget {
			// Include a partial block so the result is never empty when rules exist.
			if len(result) == 0 && budget > 0 {
				result = append(result, utf16Slice(block, 0, budget))
			}
			break
		}
		result = append(result, block)
		count += n
	}
	return strings.Join(result, "\n\n")
}

// redactAndCap redacts the content before it leaves the machine, and caps it once it is past the budget.
func redactAndCap(source, raw string) ResolvedRulesFile {
	if utf16Len(raw) > maxResolvedChars {
		return ResolvedRulesFile{Content: Redact(extractRules(raw, 4000)), Source: source}
	}
	return ResolvedRulesFile{Content: Redact(raw), Source: source}
}

// resolveRulesFile is the rules content the escalation request carries; false when the config reaches no document.
// RuleStore judges with the documents this returns, in this order, so the request names the rules in force. What
// differs is the cap: the request carries one budget for the whole field where the guard spends `rules.maxChars` per
// resolution.
func resolveRulesFile(cwd string, files []string, fallback bool) (ResolvedRulesFile, bool) {
	if content, ok := readRules(resolvePath(cwd, RulesFile)); ok {
		return redactAndCap(RulesFile, content), true
	}
	var sources, contents []string
	for _, entry := range files {
		if content, ok := readRules(resolvePath(cwd, entry)); ok {
			sources = append(sources, entry)
			contents = append(contents, content)
		}
	}
	if len(sources) > 0 {
		return redactAndCap(strings.Join(sources, ", "), strings.Join(contents, "\n\n")), true
	}
	if !fallback {
		return ResolvedRulesFile{}, false
	}
	for _, file := range fallbackFiles {
		// A blank fallback document is not a source: the store walks on to the next name, so this does too.
		if content, ok := readRules(resolvePath(cwd, file)); ok && jsTrim(content) != "" {
			return redactAndCap(file, content), true
		}
	}
	return ResolvedRulesFile{}, false
}

var missingStore = NewRuleStore()

// checkPiWardenMissing says whether the first-run "no project rules" notice should fire, and which document is being
// judged. RuleStore.LoadTiered owns the resolution order, so ask it which tier answered instead of walking the
// filesystem here: inferring one warns while the configured files are the ones in force, names a fallback document
// the config turned off, or names a `rules.files` entry after the file it points at.
func checkPiWardenMissing(cwd string, config RulesConfig) (missing bool, fallbackSource string) {
	set, tier := missingStore.LoadTiered(cwd, config)
	if tier == tierNone {
		return true, ""
	}
	// A project that has a rules source of its own is not missing one, empty file or not: /warden init would name the
	// file the project already has, and a pi-warden.md would shadow a bound `rules.files`.
	if tier == tierFallback && set != nil && len(set.Sources) > 0 {
		return true, set.Sources[0]
	}
	return tier == tierFallback, ""
}
