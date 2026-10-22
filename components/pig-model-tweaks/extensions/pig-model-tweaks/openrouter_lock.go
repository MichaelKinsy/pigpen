package pigmodeltweaks

import (
	"fmt"
	"sort"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// lockProviderName is the command that owns the provider locks.
const lockProviderName = "pmt-openrouter-lock-provider"

// registerOpenRouterLockProvider pins an OpenRouter model to one upstream
// provider.
//
// The lock is applied where it matters: before each provider request the `:<lock>`
// suffix is stripped from the model id, and OpenRouter's provider.order is set to
// put the locked provider first. Without the stripping the suffix would reach
// OpenRouter as part of the model id and the request would 404.
func registerOpenRouterLockProvider(ext *sdk.Extension, st *store) {
	ext.OnEvent(eventBeforeProviderRequest, func(ctx sdk.Context, data map[string]any) (any, error) {
		// A lock is an OpenRouter routing preference: a request for any other
		// backend is left untouched, whatever its model id looks like.
		if ctx.ModelProvider() != openRouterProvider {
			return nil, nil
		}
		state := st.load()
		if !state.OpenRouterModelProviderPref.Enabled {
			return nil, nil
		}
		body, isObject := data["payload"].(map[string]any)
		if !isObject || body == nil {
			return nil, nil
		}
		// A copy, because the host keeps its own payload when nothing changed.
		next := make(map[string]any, len(body)+1)
		for key, value := range body {
			next[key] = value
		}
		changed := false

		if model, isString := next["model"].(string); isString {
			if base := baseIDOf(state, model); base != model {
				next["model"] = base
				changed = true
			}
		}

		// The model actually selected, which is what a lock is recorded against.
		if current := activeModelID(ctx); current != "" {
			if lock := lockFor(state, baseIDOf(state, current)); lock != "" {
				provider := map[string]any{}
				if existing, isObject := next["provider"].(map[string]any); isObject {
					for key, value := range existing {
						provider[key] = value
					}
				}
				order := []any{}
				if existing, isList := provider["order"].([]any); isList {
					for _, entry := range existing {
						if text, isString := entry.(string); isString && text != lock {
							order = append(order, text)
						}
					}
				}
				// The locked provider first, existing order behind it.
				provider["order"] = append([]any{lock}, order...)
				next["provider"] = provider
				changed = true
			}
		}

		if !changed {
			return nil, nil
		}
		return next, nil
	})

	ext.Command(lockProviderName, "Manage OpenRouter provider locks (adds a :<provider> suffix)",
		func(ctx sdk.Context, args string) error {
			state := st.load()
			trimmed := strings.TrimSpace(args)
			sub := strings.ToLower(trimmed)

			switch sub {
			case "on", "off":
				enabled := sub == "on"
				st.update(func(draft *settings) { draft.OpenRouterModelProviderPref.Enabled = enabled })
				ctx.Notify("openrouter-lock-provider "+enabledVerb(enabled), "info")
				return nil
			case "list":
				ctx.Notify(formatLocks(state), "info")
				return nil
			case "clear":
				// `clear` clears the lock of the model the session runs.
				if ctx.ModelProvider() != openRouterProvider {
					ctx.Notify("Current model is not an OpenRouter model", "warning")
					return nil
				}
				return clearLockFor(ctx, st, baseIDOf(state, ctx.Model()))
			}

			// This port's own forms, which the original does not have:
			// `clear <model>` and `lock <model> <provider>` name a model.
			fields := strings.Fields(trimmed)
			if len(fields) == 2 && strings.ToLower(fields[0]) == "clear" {
				return clearLockFor(ctx, st, baseIDOf(state, fields[1]))
			}
			if len(fields) == 3 && strings.ToLower(fields[0]) == "lock" && strings.Contains(fields[1], "/") {
				applyLock(ctx, st, fields[1], fields[2])
				ctx.Notify(fmt.Sprintf("locked %s to provider %s", fields[1], fields[2]), "info")
				return nil
			}

			// `<provider>` locks the model the session is running; the whole
			// argument is the slug, as the original takes it.
			if trimmed != "" {
				if ctx.ModelProvider() != openRouterProvider {
					ctx.Notify("Current model is not an OpenRouter model", "warning")
					return nil
				}
				baseID := baseIDOf(state, ctx.Model())
				applyLock(ctx, st, baseID, trimmed)
				// The original names the variant whether or not settings.json
				// could be written.
				ctx.Notify(fmt.Sprintf("OpenRouter provider lock for %s: %s\ndefaultModel -> %s",
					baseID, trimmed, makeVariantID(baseID, trimmed)), "info")
				return nil
			}

			// No arguments: the picker needs a terminal.
			if ctx.Mode() != modeTUI {
				ctx.Notify("Usage: /"+lockProviderName+" <provider> | clear | list | on | off", "info")
				return nil
			}
			return showLockPicker(ctx, st)
		})
}

// lockPickerItems lists the picker's rows: the available OpenRouter models plus
// every model that already has a lock, sorted by id, each described by its lock.
func lockPickerItems(available []modelRef, locks map[string]string) []lockItem {
	ids := map[string]bool{}
	for _, model := range available {
		if model.Provider == openRouterProvider {
			ids[model.Model] = true
		}
	}
	for id := range locks {
		ids[id] = true
	}
	sorted := make([]string, 0, len(ids))
	for id := range ids {
		sorted = append(sorted, id)
	}
	sort.Strings(sorted)
	items := make([]lockItem, 0, len(sorted))
	for _, id := range sorted {
		description := "no provider lock"
		if locks[id] != "" {
			description = "provider: " + locks[id]
		}
		items = append(items, lockItem{id: id, description: description})
	}
	return items
}

// clearLockFor clears a model's lock, as `clear` does.
func clearLockFor(ctx sdk.Context, st *store, baseID string) error {
	if lockFor(st.load(), baseID) == "" {
		ctx.Notify("No provider lock set for "+baseID, "info")
		return nil
	}
	applyLock(ctx, st, baseID, "")
	ctx.Notify("Cleared provider lock for "+baseID, "info")
	return nil
}

// applyLock records a lock change (an empty provider clears it) and writes the
// model into settings.json as the default (as `base:provider` while a lock is
// active), as the original's applyLock does. A settings.json that cannot be
// written is logged, never an error: the lock itself is already stored.
func applyLock(ctx sdk.Context, st *store, baseID, provider string) {
	st.update(func(draft *settings) {
		if provider == "" {
			clearLock(draft, baseID)
		} else {
			setLock(draft, baseID, provider)
		}
	})
	agent, err := st.agent(ctx)
	if err != nil {
		logf("agent directory: %v", err)
		return
	}
	persistDefaultModel(agent, st.load(), openRouterProvider, baseID)
}

// showLockPicker is pt-openrouter-lock-provider.ts's showLockPicker: a
// searchable list of the available OpenRouter models plus every model that
// already has a lock, then the provider slug to lock it to.
func showLockPicker(ctx sdk.Context, st *store) error {
	locks := st.load().OpenRouterModelProviderPref.Locks
	items := lockPickerItems(availableModels(ctx), locks)
	if len(items) == 0 {
		ctx.Notify("No OpenRouter models available", "warning")
		return nil
	}
	result, err := ctx.Custom(newLockPicker(items), nil)
	if err != nil {
		return err
	}
	selected, _ := result.(string)
	if selected == "" {
		return nil
	}
	current := lockFor(st.load(), selected)
	if current == "" {
		current = "deepseek"
	}
	answer, answered, err := ctx.Input("Provider for "+selected+" (empty to clear):", current)
	if err != nil {
		return err
	}
	if !answered {
		return nil
	}
	input, _ := answer.(string)
	value := strings.TrimSpace(input)
	applyLock(ctx, st, selected, value)
	if value != "" {
		ctx.Notify(fmt.Sprintf("OpenRouter provider lock for %s: %s", selected, value), "info")
	} else {
		ctx.Notify("Cleared provider lock for "+selected, "info")
	}
	return nil
}

// activeModelID returns the model id the session is running, or "" when the host
// cannot name one. Model() answers a single value in every PiG SDK revision this
// extension builds against.
func activeModelID(ctx sdk.Context) string {
	return ctx.Model()
}

// formatLocks renders every lock, sorted so the list is stable between runs.
func formatLocks(state *settings) string {
	locks := state.OpenRouterModelProviderPref.Locks
	lines := "  (none)"
	if len(locks) > 0 {
		models := make([]string, 0, len(locks))
		for model := range locks {
			models = append(models, model)
		}
		sort.Strings(models)
		entries := make([]string, 0, len(models))
		for _, model := range models {
			entries = append(entries, "  • "+model+" -> "+locks[model])
		}
		lines = strings.Join(entries, "\n")
	}
	return fmt.Sprintf("OpenRouter provider locks (%s):\n%s",
		strings.ToUpper(enabledWord(state.OpenRouterModelProviderPref.Enabled)), lines)
}
