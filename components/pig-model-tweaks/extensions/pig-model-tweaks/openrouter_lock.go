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

	ext.Command(lockProviderName, "Pin an OpenRouter model to one upstream provider",
		func(ctx sdk.Context, args string) error {
			state := st.load()
			fields := strings.Fields(strings.TrimSpace(args))
			sub := ""
			if len(fields) > 0 {
				sub = strings.ToLower(fields[0])
			}

			switch sub {
			case "on", "off":
				enabled := sub == "on"
				st.update(func(draft *settings) { draft.OpenRouterModelProviderPref.Enabled = enabled })
				ctx.Notify(fmt.Sprintf("openrouter provider locks %s", enabledWord(enabled)), "info")
				return nil
			case "list":
				ctx.Notify(formatLocks(state), "info")
				return nil
			case "clear":
				if len(fields) < 2 {
					ctx.Notify("usage: /"+lockProviderName+" clear <model>", "warning")
					return nil
				}
				baseID := baseIDOf(state, fields[1])
				if lockFor(state, baseID) == "" {
					ctx.Notify("no provider lock set for "+baseID, "info")
					return nil
				}
				st.update(func(draft *settings) { clearLock(draft, baseID) })
				ctx.Notify("cleared the provider lock for "+baseID, "info")
				return nil
			}

			// `lock <model> <provider>` sets one without the picker the Pi
			// original opened; this SDK cannot enumerate models yet.
			if sub == "lock" || len(fields) >= 2 {
				if len(fields) < 3 {
					ctx.Notify("usage: /"+lockProviderName+" lock <model> <provider>", "warning")
					return nil
				}
				baseID, provider := fields[1], fields[2]
				if !strings.Contains(baseID, "/") {
					ctx.Notify("a model id looks like vendor/model", "warning")
					return nil
				}
				st.update(func(draft *settings) { setLock(draft, baseID, provider) })
				ctx.Notify(fmt.Sprintf("locked %s to provider %s", baseID, provider), "info")
				return nil
			}

			// No arguments: the model list the Pi original opened, then the
			// provider slug.
			model, ok, err := pickOpenRouterModel(ctx, state)
			if err != nil {
				logf("model picker: %v", err)
				return nil
			}
			if !ok {
				ctx.Notify("cancelled", "info")
				return nil
			}
			provider, ok, err := ctx.Input("Upstream provider for "+model, "provider slug (empty clears the lock)")
			if err != nil {
				logf("input: %v", err)
				return nil
			}
			if !ok {
				ctx.Notify("cancelled", "info")
				return nil
			}
			provider = strings.TrimSpace(provider)
			if provider == "" {
				st.update(func(draft *settings) { clearLock(draft, model) })
				ctx.Notify("cleared the provider lock for "+model, "info")
				return nil
			}
			st.update(func(draft *settings) { setLock(draft, model, provider) })
			ctx.Notify(fmt.Sprintf("locked %s to provider %s", model, provider), "info")
			return nil
		})
}

// pickOpenRouterModel asks which OpenRouter model to pin: through the picker when
// the host can list models, and through a typed prompt when it cannot.
func pickOpenRouterModel(ctx sdk.Context, state *settings) (string, bool, error) {
	// The whole catalog, not just the usable half: a provider lock is often set
	// before the host can reach the model it pins.
	models := catalogModels(ctx, catalogEvery)
	openRouter := make([]modelRef, 0)
	for _, model := range models {
		if model.Provider == openRouterProvider {
			openRouter = append(openRouter, model)
		}
	}
	if len(openRouter) == 0 {
		// Nothing in this host's catalog is an OpenRouter model. Say so instead of
		// opening an empty list, and still accept a model typed by hand, because a
		// provider lock is written to a file herdr reads, not to the host.
		ctx.Notify("no OpenRouter models in this host's catalog; type one to lock it anyway", "info")
		model, ok, err := ctx.Input("OpenRouter model to pin", "vendor/model")
		if err != nil || !ok {
			return "", false, err
		}
		return strings.TrimSpace(model), strings.TrimSpace(model) != "", nil
	}
	result, err := ctx.Custom(newModelPicker(openRouter, nil, false), nil)
	if err != nil {
		return "", false, err
	}
	chosen := selection(result)
	if len(chosen) == 0 {
		return "", false, nil
	}
	return chosen[0], true, nil
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
	if len(locks) == 0 {
		return "no OpenRouter provider locks set"
	}
	models := make([]string, 0, len(locks))
	for model := range locks {
		models = append(models, model)
	}
	sort.Strings(models)
	lines := make([]string, 0, len(models))
	for _, model := range models {
		lines = append(lines, model+":"+locks[model])
	}
	return fmt.Sprintf("OpenRouter provider locks (%s):\n%s",
		strings.ToUpper(enabledWord(state.OpenRouterModelProviderPref.Enabled)),
		strings.Join(lines, "\n"))
}
