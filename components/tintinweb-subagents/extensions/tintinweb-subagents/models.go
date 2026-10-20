package tintinweb_subagents

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Model resolution: an exact "provider/modelId" with a fuzzy fallback. upstream: src/model-resolver.ts.

// modelRegistry is what the resolver reads of Pi's registry. A model is the registry's own map (id, name, provider...).
type modelRegistry struct {
	GetAll       func() []map[string]any
	GetAvailable func() []map[string]any // nil: the registry has no availability filter
	Find         func(provider, modelID string) map[string]any
}

func (r modelRegistry) available() []map[string]any {
	if r.GetAvailable != nil {
		return r.GetAvailable()
	}
	return r.GetAll()
}

// registryOf is Pi's model registry as seen through the SDK. Available models are the ones with auth configured.
func registryOf(ctx sdk.Context) modelRegistry {
	reg := ctx.ModelRegistry()
	all := func() []map[string]any { m, _ := reg.GetAll(); return m }
	return modelRegistry{
		GetAll:       all,
		GetAvailable: func() []map[string]any { m, _ := reg.GetAvailable(); return m },
		Find:         func(provider, id string) map[string]any { return findModel(all(), provider, id) },
	}
}

func findModel(models []map[string]any, provider, id string) map[string]any {
	for _, m := range models {
		if str(m["provider"]) == provider && str(m["id"]) == id {
			return m
		}
	}
	return nil
}

func jsLen(s string) int { return len(utf16.Encode([]rune(s))) }

var splitParts = regexp.MustCompile(`[\s\-/]+`)
var eightDigits = regexp.MustCompile(`^\d{8}$`)

// resolveModel resolves a model string to a model of the registry: the exact "provider/modelId" first (only if
// available, that is with auth), then a fuzzy match against the available models, then the same model name under any
// provider. It returns the model, or an error message listing the available ones. upstream: model-resolver.ts resolveModel.
func resolveModel(input string, reg modelRegistry) (map[string]any, string) {
	all := reg.available()
	availableSet := map[string]bool{}
	for _, m := range all {
		availableSet[strings.ToLower(str(m["provider"])+"/"+str(m["id"]))] = true
	}

	// 1. Exact: "provider/modelId", only if available.
	slash := strings.Index(input, "/")
	if slash != -1 {
		provider, modelID := input[:slash], input[slash+1:]
		if availableSet[strings.ToLower(input)] {
			if found := reg.Find(provider, modelID); found != nil {
				return found, ""
			}
		}
	}

	// 2. Fuzzy. Separators are normalized so a dot and a dash in a version still match ("claude-haiku-4.5" and
	// "claude-haiku-4-5"). Preference: exact id, id contains, name contains, every part present somewhere.
	normalize := func(s string) string { return strings.ReplaceAll(strings.ToLower(s), ".", "-") }
	query := normalize(input)
	var best map[string]any
	bestScore := 0.0
	for _, m := range all {
		id, name := normalize(str(m["id"])), normalize(str(m["name"]))
		provider := str(m["provider"])
		full := normalize(provider + "/" + str(m["id"]))
		score := 0.0
		switch {
		case id == query || full == query:
			score = 100
		case strings.Contains(id, query) || strings.Contains(full, query):
			score = 60 + float64(jsLen(query))/float64(jsLen(id))*30 // a tighter match is better
		case strings.Contains(name, query):
			score = 40 + float64(jsLen(query))/float64(jsLen(name))*20
		default:
			// A trailing date stamp is optional: "claude-haiku-4-5-20251001" still matches the undated "claude-haiku-4-5".
			every := true
			for _, part := range splitParts.Split(query, -1) {
				if !(eightDigits.MatchString(part) || strings.Contains(id, part) || strings.Contains(name, part) || strings.Contains(strings.ToLower(provider), part)) {
					every = false
					break
				}
			}
			if every {
				score = 20
			}
		}
		if score > bestScore {
			bestScore, best = score, m
		}
	}
	if best != nil && bestScore >= 20 {
		if found := reg.Find(str(best["provider"]), str(best["id"])); found != nil {
			return found, ""
		}
	}

	// 3. A "provider/modelId" that did not match under the named provider retries against all of them: the same
	// model from another provider beats falling back to "inherit".
	if slash != -1 {
		if bare, msg := resolveModel(input[slash+1:], reg); msg == "" {
			return bare, ""
		}
	}

	// 4. No match: list the available models.
	list := make([]string, 0, len(all))
	for _, m := range all {
		list = append(list, "  "+str(m["provider"])+"/"+str(m["id"]))
	}
	sort.Strings(list)
	return nil, fmt.Sprintf("Model not found: %q.\n\nAvailable models:\n%s", input, strings.Join(list, "\n"))
}

var claudePrefix = regexp.MustCompile(`(?i)^Claude\s+`)

// describeModel is both display forms of a model: the short one for tight rows, the canonical one where there is
// room to tell two providers of a similar name apart. upstream: model-resolver.ts describeModel.
func describeModel(provider, id, name string) (modelName, modelID string) {
	if name == "" {
		name = id
	}
	return strings.ToLower(claudePrefix.ReplaceAllString(name, "")), provider + "/" + id
}
