package pigmodeltweaks

import (
	"fmt"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// rememberModelName is the command that owns this feature. Every command in
// this extension uses the pmt- prefix, where the Pi original used pt-.
const rememberModelName = "pmt-remember-model"

// registerRememberModel remembers the model the user selected and restores it
// on the next session.
//
// On model_select the selection is saved here and written into PiG's
// settings.json as defaultProvider/defaultModel, so the next startup already
// begins on it. On a new or resumed-less startup the extension restores it
// directly as well, because a locked OpenRouter model is stored with a
// `:provider` suffix that PiG's resolver cannot parse.
func registerRememberModel(ext *sdk.Extension, st *store) {
	ext.OnEvent(eventModelSelect, func(ctx sdk.Context, data map[string]any) (any, error) {
		state := st.load()
		if !state.RememberModel.Enabled {
			return nil, nil
		}
		model := selectedModel(data)
		if model.Provider == "" {
			return nil, nil
		}
		if st.peekReverting(state, model) {
			// The guard's own switch-back: the default was not changed by it,
			// so re-persisting would only repeat the "remembered" toast. The
			// guard's handler consumes the flag right after this one.
			return nil, nil
		}
		if guardQuestions(state, st, model) {
			// The guard questions this selection (and may still be asking about
			// it). Whether the user approves or cancels, a model outside the
			// preferred list never becomes the default the next session starts
			// on, so nothing here is written to pmt-settings or settings.json.
			return nil, nil
		}
		baseID := baseIDOf(state, model.Model)

		st.update(func(draft *settings) {
			draft.RememberModel.Last = &struct {
				Provider string `json:"provider"`
				ModelID  string `json:"modelId"`
			}{Provider: model.Provider, ModelID: baseID}
		})

		if persistDefaultModel(agentDir(ctx.ConfigHome()), model.Provider, baseID) == "" {
			return nil, nil
		}
		ctx.Notify(fmt.Sprintf("remembered %s/%s as the default model", model.Provider, baseID), "info")
		return nil, nil
	})

	ext.OnSessionStart(func(ctx sdk.Context, data map[string]any) (any, error) {
		state := st.load()
		if !state.RememberModel.Enabled {
			return nil, nil
		}
		// Only a genuinely new session is restored here. A restarted session that
		// already has a conversation carries its own model, and overriding it
		// would change the model mid-history. The startup case needs no restore:
		// the model written into settings.json below is already host-resolvable.
		if sdk.Param[string](data, "reason") != "new" {
			return nil, nil
		}
		last := state.RememberModel.Last
		if last == nil {
			return nil, nil
		}
		spec := resolveModelSpec(ctx, state, last.Provider, last.ModelID)
		if spec == "" {
			logf("remembered model %s/%s is not in the registry", last.Provider, last.ModelID)
			return nil, nil
		}
		if _, err := ctx.SetModel(spec); err != nil {
			logf("restore %s: %v", spec, err)
		}
		return nil, nil
	})

	ext.Command(rememberModelName, "Remember and restore the last selected model across sessions",
		func(ctx sdk.Context, args string) error {
			sub := strings.ToLower(strings.TrimSpace(args))
			switch sub {
			case "on", "off":
				enabled := sub == "on"
				updated := st.update(func(draft *settings) { draft.RememberModel.Enabled = enabled })
				last := "(none)"
				if updated.RememberModel.Last != nil {
					last = updated.RememberModel.Last.Provider + "/" + updated.RememberModel.Last.ModelID
				}
				ctx.Notify(fmt.Sprintf("remember-model %s; last: %s", enabledWord(enabled), last), "info")
				return nil
			case "clear":
				st.update(func(draft *settings) { draft.RememberModel.Last = nil })
				ctx.Notify("cleared the remembered model", "info")
				return nil
			}
			state := st.load()
			last := "(none)"
			if state.RememberModel.Last != nil {
				last = state.RememberModel.Last.Provider + "/" + state.RememberModel.Last.ModelID
			}
			ctx.Notify(fmt.Sprintf(
				"remember-model: %s\nlast: %s\nusage: /%s [on|off|clear]",
				enabledWord(state.RememberModel.Enabled), last, rememberModelName,
			), "info")
			return nil
		})
}

// resolveModelSpec returns the `provider/model` spec to restore, preferring the
// locked OpenRouter variant so the request keeps the provider the user pinned,
// and falling back to the base id.
func resolveModelSpec(ctx sdk.Context, state *settings, provider, modelID string) string {
	baseID := baseIDOf(state, modelID)
	if provider == openRouterProvider {
		if lock := lockFor(state, baseID); lock != "" {
			variant := makeVariantID(baseID, lock)
			if ctx.ModelRegistry().Find(provider, variant) != nil {
				return provider + "/" + variant
			}
		}
	}
	if ctx.ModelRegistry().Find(provider, baseID) != nil {
		return provider + "/" + baseID
	}
	return ""
}

// currentModel is the session's active model. Model() and ModelProvider() are
// used because they answer a single value in every PiG SDK revision this
// extension is expected to build against, unlike the typed model getters.
func currentModel(ctx sdk.Context) modelRef {
	return modelRef{Provider: ctx.ModelProvider(), Model: ctx.Model()}
}

// selectedModel reads the model out of a model_select event, whose payload
// carries the new model under "model".
func selectedModel(data map[string]any) modelRef {
	return modelRefOf(data["model"])
}

// previousModel reads the model a model_select event replaced, under
// "previousModel". It is absent (`omitempty`) on the first selection of a
// session, and the caller falls back to the startup model when it is missing.
func previousModel(data map[string]any) modelRef {
	return modelRefOf(data["previousModel"])
}

// modelRefOf parses one host model payload into a modelRef. The host may carry
// the provider as a string or as an object with an id.
func modelRefOf(raw any) modelRef {
	model, _ := raw.(map[string]any)
	provider, _ := model["provider"].(string)
	id, _ := model["id"].(string)
	if nested, isObject := model["provider"].(map[string]any); isObject {
		if value, _ := nested["id"].(string); value != "" {
			provider = value
		}
	}
	return modelRef{Provider: provider, Model: id}
}

// enabledWord renders a boolean for a status line.
func enabledWord(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}

// askingWord renders whether the guard asks before refusing.
func askingWord(confirm bool) string {
	if confirm {
		return "before sending"
	}
	return "never"
}
