package pigmodeltweaks

import (
	"fmt"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// modelGuardName is the command that owns the allow-list.
const modelGuardName = "pmt-model-guard-pref"

// registerModelGuard keeps a prompt from being sent to a model outside the
// allow-list, so a stray model switch cannot quietly send work to a model the
// user did not choose.
//
// The guard is a host-level input hook, and it never blocks inside the handler.
// PiG dispatches input handlers on the goroutine that owns the terminal, so a
// dialog opened here waits for that same goroutine to route its keystrokes and
// the two wait for each other: the prompt appears to do nothing and no key
// responds again. The handler therefore decides immediately and reports the input
// as handled when the prompt must not run, which is Pi's own
// `{ action: "handled" }` shape.
//
// What the user sees instead of a dialog depends on the setting:
//
//   - default: a warning toast naming the model and the command that allows it.
//   - `confirm: true`: the question is asked from an owned worker after the
//     handler returned, and an accepted prompt is re-sent through
//     `SendUserMessage`. That runs with the extension as its source, so the guard
//     does not ask about the same prompt twice.
//
// An empty allow-list allows everything.
func registerModelGuard(ext *sdk.Extension, st *store) {
	ext.OnEvent(eventInput, func(ctx sdk.Context, data map[string]any) (any, error) {
		// Only a typed prompt in the TUI is a decision point. Queued messages,
		// RPC and print runs have nobody to ask.
		if sdk.Param[string](data, "source") != "interactive" {
			return nil, nil
		}
		if ctx.Mode() != modeTUI {
			return nil, nil
		}
		state := st.load()
		if !state.ModelGuard.Enabled || len(state.ModelGuard.AllowedModels) == 0 {
			return nil, nil
		}
		current := currentModel(ctx)
		if current.Provider == "" || current.Model == "" {
			// Without a named model there is nothing to compare against, and a
			// guard that blocks on missing information would block everything.
			return nil, nil
		}
		if isModelAllowed(state, current) || sameModel(state, current, st.startupModel()) || st.approvedForSession(state, current) {
			return nil, nil
		}
		if state.ModelGuard.Confirm {
			// The prompt is consumed here and re-sent by the worker once the
			// user answers, so the answer can take as long as it takes.
			st.confirmOffLoop(ctx, current, state, sdk.Param[string](data, "text"), imagesOf(data))
			return handledInput, nil
		}
		ctx.Notify(refusalNotice(current, state), "warning")
		return handledInput, nil
	})

	ext.OnEvent(eventModelSelect, func(ctx sdk.Context, data map[string]any) (any, error) {
		// The host restores a model itself; announcing that back would be noise.
		if sdk.Param[string](data, "source") == "restore" {
			return nil, nil
		}
		model := selectedModel(data)
		if model.Provider == "" {
			return nil, nil
		}
		state := st.load()
		// Consume a switch-back this extension asked for: it was decided at the
		// question, and asking again would loop the decline.
		if st.takeReverting(state, model) {
			return nil, nil
		}
		if !state.ModelGuard.Enabled || len(state.ModelGuard.AllowedModels) == 0 {
			return nil, nil
		}
		if isModelAllowed(state, model) {
			ctx.Notify("in preferred models: "+model.String(), "info")
			return nil, nil
		}
		if sameModel(state, model, st.startupModel()) {
			// The prompt guard trusts the session's own starting model; switching
			// back to it is not a stray selection either.
			ctx.Notify("the model this session started on: "+model.String(), "info")
			return nil, nil
		}
		if st.approvedForSession(state, model) {
			ctx.Notify("already approved for this session: "+model.String()+" (not remembered)", "info")
			return nil, nil
		}
		if ctx.Mode() != modeTUI {
			// RPC and print runs have nobody to ask; announce, as before.
			ctx.Notify("NOT in preferred models: "+model.String(), "warning")
			return nil, nil
		}
		// The host applied the switch before this event and discards the handler's
		// result, so the choice cannot be refused here: it is asked after the
		// handler returns, and a decline switches back.
		st.markAsking(model)
		st.confirmSelectOffLoop(ctx, model, previousModel(data), state)
		return nil, nil
	})

	ext.Command(modelGuardName, "Manage the list of models prompts may use without confirmation",
		func(ctx sdk.Context, args string) error {
			state := st.load()
			fields := strings.Fields(strings.TrimSpace(args))
			sub := ""
			if len(fields) > 0 {
				sub = strings.ToLower(fields[0])
			}

			switch sub {
			case "on", "off", "toggle":
				enabled := state.ModelGuard.Enabled
				switch sub {
				case "on":
					enabled = true
				case "off":
					enabled = false
				case "toggle":
					enabled = !enabled
				}
				updated := st.update(func(draft *settings) { draft.ModelGuard.Enabled = enabled })
				ctx.Notify(fmt.Sprintf("model guard %s; preferred: %s",
					enabledWord(enabled), formatAllowList(updated.ModelGuard.AllowedModels)), "info")
				return nil
			case "ask", "confirm", "noask":
				confirm := state.ModelGuard.Confirm
				switch sub {
				case "ask", "confirm":
					confirm = true
				case "noask":
					confirm = false
				}
				updated := st.update(func(draft *settings) { draft.ModelGuard.Confirm = confirm })
				ctx.Notify(fmt.Sprintf("model guard %s; asks %s; preferred: %s",
					enabledWord(updated.ModelGuard.Enabled), askingWord(confirm),
					formatAllowList(updated.ModelGuard.AllowedModels)), "info")
				return nil
			case "list":
				if len(state.ModelGuard.AllowedModels) == 0 {
					ctx.Notify("no models preferred: every model is allowed", "info")
					return nil
				}
				ctx.Notify(fmt.Sprintf("model guard %s, asks %s; preferred: %s; the model this session started on is always allowed",
					enabledWord(state.ModelGuard.Enabled), askingWord(state.ModelGuard.Confirm),
					formatAllowList(state.ModelGuard.AllowedModels)), "info")
				return nil
			case "add":
				ref, ok := readModelArg(ctx, fields, "provider/model to add")
				if !ok {
					return nil
				}
				updated := st.update(func(draft *settings) {
					draft.ModelGuard.AllowedModels = addAllowed(draft.ModelGuard.AllowedModels, ref)
				})
				ctx.Notify("preferred models: "+formatAllowList(updated.ModelGuard.AllowedModels), "info")
				return nil
			case "remove":
				ref, ok := readModelArg(ctx, fields, "provider/model to remove")
				if !ok {
					return nil
				}
				updated := st.update(func(draft *settings) {
					draft.ModelGuard.AllowedModels = removeAllowed(draft.ModelGuard.AllowedModels, ref)
				})
				ctx.Notify("preferred models: "+formatAllowList(updated.ModelGuard.AllowedModels), "info")
				return nil
			}

			// No arguments: the multi-select the Pi original opened. The catalog
			// comes from the host when its SDK exposes one, and otherwise from the
			// models already in play, so the picker is never empty for no reason.
			checked := make([]string, 0, len(state.ModelGuard.AllowedModels))
			for _, model := range state.ModelGuard.AllowedModels {
				checked = append(checked, model.String())
			}
			models := pickerCatalog(ctx, state.ModelGuard.AllowedModels)
			if len(models) == 0 {
				ctx.Notify(fmt.Sprintf("model guard %s; preferred: %s\nno model catalog is available from this PiG; use /%s add <provider/model>",
					enabledWord(state.ModelGuard.Enabled), formatAllowList(state.ModelGuard.AllowedModels), modelGuardName), "info")
				return nil
			}
			result, err := ctx.Custom(newModelPicker(models, checked, true), nil)
			if err != nil {
				logf("picker: %v", err)
				return nil
			}
			chosen := selection(result)
			if len(chosen) == 0 {
				ctx.Notify("nothing changed", "info")
				return nil
			}
			parsed := make([]modelRef, 0, len(chosen))
			for _, name := range chosen {
				if model, valid := parseModelRef(name); valid {
					parsed = append(parsed, model)
				}
			}
			updated := st.update(func(draft *settings) { draft.ModelGuard.AllowedModels = parsed })
			ctx.Notify("preferred models: "+formatAllowList(updated.ModelGuard.AllowedModels), "info")
			return nil
		})
}

// readModelArg reads a `provider/model` argument, asking for it when the
// command gave none.
func readModelArg(ctx sdk.Context, fields []string, placeholder string) (modelRef, bool) {
	if len(fields) > 1 {
		if ref, ok := parseModelRef(fields[1]); ok {
			return ref, true
		}
		ctx.Notify("a model looks like provider/model", "warning")
		return modelRef{}, false
	}
	answer, ok, err := ctx.Input(placeholder, "provider/model")
	if err != nil {
		logf("input: %v", err)
		return modelRef{}, false
	}
	if !ok {
		ctx.Notify("cancelled", "info")
		return modelRef{}, false
	}
	ref, valid := parseModelRef(answer)
	if !valid {
		ctx.Notify("a model looks like provider/model", "warning")
		return modelRef{}, false
	}
	return ref, true
}

// isModelAllowed reports whether a model may be used without confirmation. An
// entry matches on provider and model, and an entry naming only a provider
// allows every model of that provider, which is how a whole backend is trusted
// at once.
func isModelAllowed(state *settings, model modelRef) bool {
	for _, allowed := range state.ModelGuard.AllowedModels {
		if allowed.Provider != model.Provider {
			continue
		}
		if allowed.Model == "" || allowed.Model == model.Model {
			return true
		}
	}
	return false
}

// addAllowed adds a model to the allow-list, keeping it unique.
func addAllowed(models []modelRef, ref modelRef) []modelRef {
	for _, existing := range models {
		if existing == ref {
			return models
		}
	}
	return append(models, ref)
}

// removeAllowed drops a model from the allow-list.
func removeAllowed(models []modelRef, ref modelRef) []modelRef {
	out := models[:0]
	for _, existing := range models {
		if existing != ref {
			out = append(out, existing)
		}
	}
	return out
}

// guardQuestions reports whether the guard stops on this model: it is on with a
// non-empty list, the model is outside the list, and it is not the model the
// session started on. remember-model shares it so a questioned selection never
// reaches the remembered defaults, approved or not: the user said "use it now",
// not "start next session on it".
func guardQuestions(state *settings, st *store, model modelRef) bool {
	if !state.ModelGuard.Enabled || len(state.ModelGuard.AllowedModels) == 0 {
		return false
	}
	if isModelAllowed(state, model) || sameModel(state, model, st.startupModel()) {
		return false
	}
	return true
}
