package pi_subagents

import (
	"regexp"
)

// Watchdog model selection (watchdog/model-selection.ts): which model reviews, and which one to recommend.

const strongWatchdogThinking = "high"

type wdStrongFamily struct {
	key     string
	label   string
	queries []string
}

var wdStrongFamilies = map[string]wdStrongFamily{
	"opus48": {"opus48", "Opus 4.8", []string{"anthropic/claude-opus-4-8", "anthropic/claude-opus-4.8", "anthropic/opus-4-8", "anthropic/opus-4.8"}},
	"gpt55":  {"gpt55", "GPT 5.5", []string{"openai-codex/gpt-5.5", "openai-codex/gpt-5-5", "openai/gpt-5.5", "openai/gpt-5-5"}},
}

type wdResolvedModelInput struct {
	Model         string
	Thinking      string // "" when the input carried no suffix
	RegistryModel map[string]any
}

type watchdogModelRecommendation struct {
	Source        string // "configured" or "suggested"
	Model         string
	Thinking      string
	Label         string
	Reason        string
	RegistryModel map[string]any
}

func wdRegistryEntries(ctx wdContext) []ModelInfo { panic("not implemented") }

func wdSplitProviderModel(value string) (provider, id string, ok bool) { panic("not implemented") }

func wdAssertSupportedThinking(value, source string) (string, error) { panic("not implemented") }

// parseWatchdogThinkingInput: undefined (nil or "") is unset; false and "false" are off; a level is itself; anything else is an
// error (the tool schema lets `true` through, the runtime does not).
func parseWatchdogThinkingInput(value any, source string) (wdThinking, error) {
	panic("not implemented")
}

func resolveWatchdogModelInput(ctx wdContext, rawModel string) (wdResolvedModelInput, error) {
	panic("not implemented")
}

var (
	wdGPT55Re  = regexp.MustCompile(`^gpt-5-5(-\d{8}|-\d{4}-\d{2}-\d{2})?$`)
	wdOpus48Re = regexp.MustCompile(`^(claude-opus-4-8|opus-4-8)(-\d{8}|-\d{4}-\d{2}-\d{2})?$`)
)

func wdFamilyForModel(provider, id string) string { panic("not implemented") }

func wdCurrentProviderFamily(ctx wdContext) string { panic("not implemented") }

func wdStrongFamilyOrder(ctx wdContext) []string { panic("not implemented") }

func wdFindFamilyMatch(family string, available []ModelInfo) string { panic("not implemented") }

func wdResolveStrongCandidate(ctx wdContext, family string) *watchdogModelRecommendation {
	panic("not implemented")
}

func recommendStrongWatchdogModel(ctx wdContext) (watchdogModelRecommendation, error) {
	panic("not implemented")
}

func recommendWatchdogModel(ctx wdContext, configured wdEndpoint) (watchdogModelRecommendation, error) {
	panic("not implemented")
}

func formatWatchdogRecommendation(r watchdogModelRecommendation) string { panic("not implemented") }
