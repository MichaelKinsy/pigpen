package posthorse

// Port of src/guidance.ts: the request-local guidance each model request carries.

import (
	"slices"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// guidanceState is the run-scoped `promptOptions` of registerGuidance (guidance.ts:6). The TS
// closure keeps the whole options object; only what the context handler reads is kept here:
// whether a forced system prompt is set and the section order of the run.
type guidanceState struct {
	mu          sync.Mutex
	forced      bool
	sectionKeys []string
}

func (g *guidanceState) set(forced bool, keys []string) {
	g.mu.Lock()
	g.forced, g.sectionKeys = forced, keys
	g.mu.Unlock()
}

func (g *guidanceState) get() (bool, []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.forced, g.sectionKeys
}

// registerGuidance is `registerGuidance` (guidance.ts:5-41).
func registerGuidance(e *sdk.Extension, policy snapshotReader) {
	state := &guidanceState{}
	e.OnEvent(sdk.EventBeforeAgentStart, func(_ sdk.Context, data map[string]any) (any, error) {
		// guidance.ts:7-9
		options, _ := recOf(data["systemPromptOptions"])
		_, forced := options["forceSystemPrompt"].(string)
		var keys []string
		if sections, ok := options["sections"].(*sdk.SystemPromptSections); ok && sections != nil {
			for _, section := range *sections {
				keys = append(keys, section.Name)
			}
		}
		if options == nil {
			forced, keys = false, nil
		}
		state.set(forced, keys)
		return nil, nil
	})
	e.OnEvent(sdk.EventAgentSettled, func(_ sdk.Context, _ map[string]any) (any, error) {
		// Prompt options belong to one run, never to later idle context evaluation (guidance.ts:11-14).
		state.set(false, nil)
		return nil, nil
	})
	e.OnEvent(sdk.EventContextWithSystem, func(ctx sdk.Context, data map[string]any) (any, error) {
		// guidance.ts:15-40
		guidance, err := buildGuidance(policy(ctx, nil))
		if err != nil {
			return nil, err
		}
		messages, _ := arrOf(data["messages"])
		system, found := lastSystemWithPosthorse(messages)
		if !found {
			system, found = firstSystem(messages)
		}
		if !found {
			return nil, nil
		}
		forced, keys := state.get()
		if forced {
			// ponytail: Pi applies forced prompts after this hook; keep request-local guidance
			// until the host projects forced text before context_with_system (guidance.ts:27-30).
			custom := rec{
				"role":       "custom",
				"customType": "posthorse-guidance",
				"content":    guidance,
				"display":    false,
				"timestamp":  float64(0),
			}
			at := min(1, len(messages))
			spliced := make([]any, 0, len(messages)+1)
			spliced = append(spliced, messages[:at]...)
			spliced = append(spliced, custom)
			spliced = append(spliced, messages[at:]...)
			return rec{"messages": spliced}, nil
		}
		system["sections"] = withPosthorseSection(system["sections"], keys, "<posthorse>\n"+guidance+"\n</posthorse>")
		return nil, nil
	})
}

func lastSystemWithPosthorse(messages []any) (rec, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		message, ok := recOf(messages[i])
		if !ok {
			continue
		}
		if role, _ := str(message, "role"); role != "system" {
			continue
		}
		if sections, ok := recOf(message["sections"]); ok {
			if _, ok := sections["posthorse"].(string); ok {
				return message, true
			}
		}
	}
	return nil, false
}

func firstSystem(messages []any) (rec, bool) {
	for _, m := range messages {
		message, ok := recOf(m)
		if !ok {
			continue
		}
		if role, _ := str(message, "role"); role == "system" {
			return message, true
		}
	}
	return nil, false
}

// hostSectionOrder is the order of the built-in sections of Pi's structured system prompt
// (buildSystemPromptSections, core/system-prompt.ts: preamble, tools, rules, docs, addendum,
// project_context, skills, cwd; extension sections follow cwd). before_agent_start options carry no
// sections to learn the order from, and a decoded Go map has none.
var hostSectionOrder = []string{"preamble", "tools", "rules", "docs", "addendum", "project_context", "skills", "cwd"}

// withPosthorseSection is `{ ...system.sections, posthorse: text }` (guidance.ts:35-38): existing
// sections keep their order and the posthorse section keeps its place or goes last. A decoded Go map
// has no order, so the keys follow the run's before_agent_start sections when it has some, then Pi's
// built-in order, then the remaining keys sorted, with posthorse after them (an SDK limitation
// recorded in the lane notes: another extension's several sections are only ordered alphabetically).
func withPosthorseSection(existing any, order []string, text string) *sdk.OrderedObject {
	sections, _ := recOf(existing)
	keys := make([]string, 0, len(sections)+1)
	for _, group := range [][]string{order, hostSectionOrder} {
		for _, key := range group {
			if _, ok := sections[key]; ok && key != "posthorse" && !slices.Contains(keys, key) {
				keys = append(keys, key)
			}
		}
	}
	rest := make([]string, 0, len(sections))
	for key := range sections {
		if key != "posthorse" && !slices.Contains(keys, key) {
			rest = append(rest, key)
		}
	}
	slices.Sort(rest)
	keys = append(keys, rest...)
	if _, ok := sections["posthorse"]; ok {
		keys = append(keys, "posthorse")
	}
	pairs := make([]any, 0, 2*len(keys)+2)
	for _, key := range keys {
		if key == "posthorse" {
			pairs = append(pairs, key, text)
		} else {
			pairs = append(pairs, key, sections[key])
		}
	}
	if _, ok := sections["posthorse"]; !ok {
		pairs = append(pairs, "posthorse", text)
	}
	return sdk.NewOrderedObject(pairs...)
}
