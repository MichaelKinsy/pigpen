package pigmodeltweaks

import (
	"fmt"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// rememberModelName is the command that owns this feature. Every command in
// this extension uses the pmt- prefix, where the Pi original used pt-.
const rememberModelName = "pmt-remember-model"

// deferredToastDelay is how long the "remembered" toast waits after its handler
// returned. The original's setTimeout(0) lands after the host's own status; a
// separate goroutine with a short delay keeps the same order without blocking the
// handler.
const deferredToastDelay = 30 * time.Millisecond

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
		agent, err := st.agent(ctx)
		if err != nil {
			return nil, err
		}

		st.update(func(draft *settings) {
			draft.RememberModel.Last = &struct {
				Provider string `json:"provider"`
				ModelID  string `json:"modelId"`
			}{Provider: model.Provider, ModelID: baseID}
		})

		persisted := persistDefaultModel(agent, st.load(), model.Provider, baseID)
		if persisted == "" {
			return nil, nil
		}
		// pt-remember-model.ts defers the toast one timer tick: the /model
		// selector and Ctrl+P show their own "Model: ..." status right after the
		// selection resolves, which would overwrite this one, so it is sent after
		// the handler has returned.
		message := fmt.Sprintf("%s: remembered model: %s/%s in settings.json", rememberModelName, model.Provider, persisted)
		time.AfterFunc(deferredToastDelay, func() { ctx.Notify(message, "info") })
		return nil, nil
	})

	ext.Command(rememberModelName, "Remember and restore the last selected model across sessions",
		func(ctx sdk.Context, args string) error {
			sub := strings.ToLower(strings.TrimSpace(args))
			switch sub {
			case "on", "off":
				enabled := sub == "on"
				st.update(func(draft *settings) { draft.RememberModel.Enabled = enabled })
				ctx.Notify("remember-model "+enabledVerb(enabled), "info")
				return nil
			case "clear":
				st.update(func(draft *settings) { draft.RememberModel.Last = nil })
				ctx.Notify("Cleared remembered model", "info")
				return nil
			}
			state := st.load()
			last := "(none)"
			if state.RememberModel.Last != nil {
				last = state.RememberModel.Last.Provider + "/" + state.RememberModel.Last.ModelID
			}
			ctx.Notify(fmt.Sprintf(
				"remember-model: %s\nlast: %s\nUsage: /%s [on|off|clear]",
				enabledWord(state.RememberModel.Enabled), last, rememberModelName,
			), "info")
			return nil
		})
}

// restoreRememberedModel puts the remembered model back on a new session and on
// a fresh startup, as pt-remember-model.ts's session_start handler does.
func restoreRememberedModel(ctx sdk.Context, st *store, data map[string]any) {
	state := st.load()
	if !state.RememberModel.Enabled {
		return
	}
	reason := sdk.Param[string](data, "reason")
	if reason != "new" && reason != "startup" {
		return
	}
	// A continued session already carries its own model; leave it alone.
	// When the branch cannot be read, assume it has messages rather than
	// override a model mid-history.
	if reason == "startup" {
		branch, err := ctx.GetBranch()
		if err != nil {
			logf("read session branch: %v", err)
			return
		}
		for _, entry := range branch {
			if entry.Type == "message" {
				return
			}
		}
	}
	last := state.RememberModel.Last
	if last == nil {
		return
	}
	spec := resolveModelSpec(ctx, state, last.Provider, last.ModelID)
	if spec == "" {
		logf("remembered model %s/%s is not in the registry", last.Provider, last.ModelID)
		return
	}
	if _, err := ctx.SetModel(spec); err != nil {
		logf("restore %s: %v", spec, err)
	}
}

// resolveModelSpec returns the `provider/model` spec to restore, preferring the
// locked OpenRouter variant so the request keeps the provider the user pinned,
// and falling back to the base id.
func resolveModelSpec(ctx sdk.Context, state *settings, provider, modelID string) string {
	baseID := baseIDOf(state, modelID)
	if provider == openRouterProvider {
		if lock := lockFor(state, baseID); lock != "" {
			variant := makeVariantID(baseID, lock)
			if modelKnown(ctx, provider, variant) {
				return provider + "/" + variant
			}
		}
	}
	if modelKnown(ctx, provider, baseID) {
		return provider + "/" + baseID
	}
	return ""
}

// modelKnown reports whether the host's model registry has the model. A failed registry call is "no".
func modelKnown(ctx sdk.Context, provider, modelID string) bool {
	model, err := ctx.ModelRegistry().Find(provider, modelID)
	return err == nil && model != nil
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

// enabledVerb renders a toggle result the way the commands announce it.
func enabledVerb(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

// askingWord renders whether the guard asks before refusing.
func askingWord(confirm bool) string {
	if confirm {
		return "before sending"
	}
	return "never"
}
