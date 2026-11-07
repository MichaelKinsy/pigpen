package posthorse

// Port of index.ts: Posthorse, fresh context, same journey.
//
// Pi owns persisted context boundaries. Posthorse owns recent-tail retention, reminder and
// rollover policy, bounded recovery, durable notes, and history retrieval.
//
// A Go extension exposes only Extension(); the TS exports `createPosthorse`,
// `registerPosthorseMessages` and `toolCards` have no Go-package equivalent. The injectable
// getPolicy of createPosthorse stays as the internal test seam newPosthorse.

import (
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Extension is the factory PiG loads: index.ts `export default createPosthorse()`.
func Extension() *sdk.Extension { return newPosthorse(nil) }

// liveCompactionPolicy is index.ts:46: `SettingsManager.inMemory(pi.getSettings())
// .getCompactionSettings(ctx.model)`, Pi's live effective settings.
func liveCompactionPolicy(ctx sdk.Context) (compactionPolicy, error) {
	settings, err := ctx.GetSettings()
	if err != nil {
		return compactionPolicy{}, err
	}
	manager, err := sdk.SettingsManagerInMemory(ctx, settings, nil)
	if err != nil {
		return compactionPolicy{}, err
	}
	var model map[string]any
	if id := ctx.Model(); id != "" {
		model = map[string]any{"provider": ctx.ModelProvider(), "id": id}
	}
	resolved, err := manager.GetCompactionSettings(model)
	if err != nil {
		return compactionPolicy{}, err
	}
	var policy compactionPolicy
	policy.enabled, _ = boolean(resolved, "enabled")
	if reserve, ok := num(resolved, "reserveTokens"); ok {
		policy.reserveTokens = jsToInt(reserve)
	}
	if keep, ok := num(resolved, "keepRecentTokens"); ok {
		value := jsToInt(keep)
		policy.keepRecentTokens = &value
	}
	return policy, nil
}

// toolTokenEntry caches the token estimate of one tool definition.
type toolTokenEntry struct {
	description string
	parameters  string
	tokens      int
}

// newActiveToolTokens is `activeToolTokens(pi)` (index.ts:22-39): the estimated tokens of the
// active tool declarations, Math.ceil(JSON.stringify({name, description, parameters}).length / 4)
// each. Estimates are cached per tool while its definition is unchanged.
func newActiveToolTokens() toolTokensFunc {
	var mu sync.Mutex
	cache := map[string]toolTokenEntry{}
	return func(ctx sdk.Context) (int, error) {
		names, err := ctx.GetActiveTools()
		if err != nil {
			return 0, err
		}
		tools, err := ctx.GetAllTools()
		if err != nil {
			return 0, err
		}
		active := make(map[string]struct{}, len(names))
		for _, name := range names {
			active[name] = struct{}{}
		}
		mu.Lock()
		defer mu.Unlock()
		total := 0
		for _, tool := range tools {
			if _, ok := active[tool.Name]; !ok {
				continue
			}
			if hit, ok := cache[tool.Name]; ok && hit.description == tool.Description && hit.parameters == string(tool.Parameters) {
				total += hit.tokens
				continue
			}
			pairs := []any{"name", tool.Name, "description", tool.Description}
			if len(tool.Parameters) > 0 {
				pairs = append(pairs, "parameters", tool.Parameters)
			}
			tokens := ceilQuarter(u16len(safeJsonStringify(sdk.NewOrderedObject(pairs...))))
			cache[tool.Name] = toolTokenEntry{description: tool.Description, parameters: string(tool.Parameters), tokens: tokens}
			total += tokens
		}
		return total, nil
	}
}

// newPosthorse is `createPosthorse(getPolicy?)` (index.ts:42-65). A nil reader uses Pi's live
// settings. The registration order is the TS order: handler and tool order is observable.
func newPosthorse(getPolicy policyReader) *sdk.Extension {
	e := sdk.New("posthorse")
	readPolicy := getPolicy
	if readPolicy == nil {
		readPolicy = liveCompactionPolicy
	}
	policy := func(ctx sdk.Context, livePolicy *compactionPolicy) policyContext {
		return policySnapshot(ctx, readPolicy, livePolicy)
	}
	toolTokens := newActiveToolTokens()
	pages := newPageReservations(toolTokens)
	registerPosthorseMessages(e)
	notes := createNotesStore()
	registerRollover(e, policy, toolTokens, rolloverResources{
		resetPages: pages.reset,
		notes:      notes.recovery,
	})
	e.OnEvent(sdk.EventSessionStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
		_, err := notes.get(ctx)
		return nil, err
	})
	registerNoteGuards(e, notes)
	registerNotes(e, policy, pages, notes)
	registerHistory(e, policy, pages)
	return e
}
