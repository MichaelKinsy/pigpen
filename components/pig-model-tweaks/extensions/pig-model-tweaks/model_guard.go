package pigmodeltweaks

import (
	"fmt"
	"slices"
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
			ctx.Notify("✅ "+model.String()+" — in preferred models", "info")
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
			// RPC and print runs have nobody to ask; announce it as the original does.
			ctx.Notify("⚠️  "+model.String()+" — NOT in preferred models", "warning")
			return nil, nil
		}
		// The host applied the switch before this event and discards the handler's
		// result, so the choice cannot be refused here: it is asked after the
		// handler returns, and a decline switches back.
		st.markAsking(model)
		st.confirmSelectOffLoop(ctx, model, previousModel(data), state)
		return nil, nil
	})

	ext.Command(modelGuardName, "Manage model preferences (guard against unintended model usage)",
		func(ctx sdk.Context, args string) error {
			state := st.load()
			// The original compares the whole argument string; anything it does
			// not know opens the picker.
			sub := strings.ToLower(strings.TrimSpace(args))
			switch sub {
			case "list":
				if len(state.ModelGuard.AllowedModels) == 0 {
					ctx.Notify("No model preferences saved. All models allowed.", "info")
					return nil
				}
				lines := make([]string, 0, len(state.ModelGuard.AllowedModels))
				for _, model := range state.ModelGuard.AllowedModels {
					lines = append(lines, "  • "+model.String())
				}
				ctx.Notify(fmt.Sprintf("Guard: %s\nAllowed models:\n%s",
					strings.ToUpper(enabledWord(state.ModelGuard.Enabled)), strings.Join(lines, "\n")), "info")
				return nil
			case "on", "off", "toggle":
				enabled := !state.ModelGuard.Enabled
				switch sub {
				case "on":
					enabled = true
				case "off":
					enabled = false
				}
				st.update(func(draft *settings) { draft.ModelGuard.Enabled = enabled })
				ctx.Notify("Model guard "+enabledVerb(enabled), "info")
				return nil
			case "add", "remove":
				return guardAddRemove(ctx, st, sub)
			}

			// This port's own forms, which the original does not have.
			fields := strings.Fields(strings.TrimSpace(args))
			if len(fields) > 0 {
				switch strings.ToLower(fields[0]) {
				case "ask", "confirm", "noask":
					confirm := strings.ToLower(fields[0]) != "noask"
					updated := st.update(func(draft *settings) { draft.ModelGuard.Confirm = confirm })
					ctx.Notify(fmt.Sprintf("model guard %s; asks %s; preferred: %s",
						enabledWord(updated.ModelGuard.Enabled), askingWord(confirm),
						formatAllowList(updated.ModelGuard.AllowedModels)), "info")
					return nil
				case "add", "remove":
					if len(fields) == 2 {
						return guardNamedModel(ctx, st, strings.ToLower(fields[0]), fields[1])
					}
				}
			}
			return guardManage(ctx, st)
		})
}

// hintNoPreferences is the guard's session_start handler: the original tells a
// user who has no preferences yet where to set them, at every session start
// while the guard is on.
func hintNoPreferences(ctx sdk.Context, st *store) {
	state := st.load()
	if state.ModelGuard.Enabled && len(state.ModelGuard.AllowedModels) == 0 {
		ctx.Notify("💡 No model preferences set. Run /"+modelGuardName+" to choose preferred models.", "info")
	}
}

// showPicker opens the multi-select over the available models. ok is false when
// it was cancelled, which the original reports as "Cancelled".
func showPicker(ctx sdk.Context, title string, models []modelRef, checked []string) (chosen []string, ok bool, err error) {
	result, err := ctx.Custom(newGuardPicker(title, models, checked), nil)
	if err != nil {
		return nil, false, err
	}
	chosen, ok = pickerChoice(result)
	return chosen, ok, nil
}

// parseModelValue is the original's `value.split("/")`: the provider is the text
// before the first slash and the model is the rest.
func parseModelValue(value string) modelRef {
	provider, model, _ := strings.Cut(value, "/")
	return modelRef{Provider: provider, Model: model}
}

func currentAllowed(st *store) []string {
	state := st.load()
	current := make([]string, 0, len(state.ModelGuard.AllowedModels))
	for _, model := range state.ModelGuard.AllowedModels {
		current = append(current, model.String())
	}
	return current
}

// guardTitle is the multi-select's heading for each way of opening it.
func guardTitle(sub string, firstTime bool) string {
	switch {
	case sub == "add":
		return "Select models to ADD to preferences"
	case sub == "remove":
		return "Select models to REMOVE from preferences"
	case firstTime:
		return "Select your PREFERRED models"
	}
	return "Toggle model preferences (checked = allowed)"
}

// guardAddRemove is the `add` and `remove` pickers of pt-model-guard.ts.
func guardAddRemove(ctx sdk.Context, st *store, sub string) error {
	models := availableModels(ctx)
	if len(models) == 0 {
		ctx.Notify("No models available. Check your API keys.", "warning")
		return nil
	}
	current := currentAllowed(st)
	chosen, ok, err := showPicker(ctx, guardTitle(sub, false), models, current)
	if err != nil {
		return err
	}
	if !ok {
		ctx.Notify("Cancelled", "info")
		return nil
	}
	if sub == "add" {
		var added []modelRef
		for _, value := range chosen {
			if !slices.Contains(current, value) {
				added = append(added, parseModelValue(value))
			}
		}
		if len(added) == 0 {
			ctx.Notify("No new models added", "info")
			return nil
		}
		st.update(func(draft *settings) {
			draft.ModelGuard.AllowedModels = append(draft.ModelGuard.AllowedModels, added...)
		})
		names := make([]string, 0, len(added))
		for _, model := range added {
			names = append(names, model.String())
		}
		ctx.Notify(fmt.Sprintf("Added %d model(s): %s", len(added), strings.Join(names, ", ")), "info")
		return nil
	}
	before := len(current)
	next := st.update(func(draft *settings) {
		kept := draft.ModelGuard.AllowedModels[:0]
		for _, model := range draft.ModelGuard.AllowedModels {
			if slices.Contains(chosen, model.String()) {
				kept = append(kept, model)
			}
		}
		draft.ModelGuard.AllowedModels = kept
	})
	if removed := before - len(next.ModelGuard.AllowedModels); removed > 0 {
		ctx.Notify(fmt.Sprintf("Removed %d model(s)", removed), "info")
	} else {
		ctx.Notify("No models removed", "info")
	}
	return nil
}

// guardManage is the bare command: one picker that sets the whole list.
func guardManage(ctx sdk.Context, st *store) error {
	models := availableModels(ctx)
	if len(models) == 0 {
		ctx.Notify("No models available. Check your API keys.", "warning")
		return nil
	}
	current := currentAllowed(st)
	firstTime := len(current) == 0
	if firstTime {
		ctx.Notify("No preferences set yet. Pick your preferred models:", "info")
	}
	chosen, ok, err := showPicker(ctx, guardTitle("", firstTime), models, current)
	if err != nil {
		return err
	}
	if !ok {
		ctx.Notify("Cancelled", "info")
		return nil
	}
	if firstTime && len(chosen) == 0 {
		ctx.Notify("No models selected. All models remain allowed.", "info")
		return nil
	}
	allowed := make([]modelRef, 0, len(chosen))
	for _, value := range chosen {
		allowed = append(allowed, parseModelValue(value))
	}
	st.update(func(draft *settings) { draft.ModelGuard.AllowedModels = allowed })
	ctx.Notify(fmt.Sprintf("Updated preferences: %d model(s) allowed", len(allowed)), "info")
	return nil
}

// guardNamedModel is this port's `add <provider/model>` and
// `remove <provider/model>`, which skip the picker.
func guardNamedModel(ctx sdk.Context, st *store, sub, name string) error {
	ref, ok := parseModelRef(name)
	if !ok {
		ctx.Notify("a model looks like provider/model", "warning")
		return nil
	}
	updated := st.update(func(draft *settings) {
		if sub == "add" {
			draft.ModelGuard.AllowedModels = addAllowed(draft.ModelGuard.AllowedModels, ref)
		} else {
			draft.ModelGuard.AllowedModels = removeAllowed(draft.ModelGuard.AllowedModels, ref)
		}
	})
	ctx.Notify("preferred models: "+formatAllowList(updated.ModelGuard.AllowedModels), "info")
	return nil
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
