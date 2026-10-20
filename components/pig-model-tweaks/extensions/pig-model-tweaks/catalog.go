package pigmodeltweaks

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// availableModels is `ctx.modelRegistry.getAvailable()`: the models the host
// can serve with its credentials, as provider/id pairs.
func availableModels(ctx sdk.Context) []modelRef {
	list, err := ctx.ModelRegistry().GetAvailable()
	if err != nil {
		logf("list available models: %v", err)
		return nil
	}
	models := make([]modelRef, 0, len(list))
	for _, entry := range list {
		if model := modelRefOf(entry); model.Provider != "" && model.Model != "" {
			models = append(models, model)
		}
	}
	return models
}

// pickerChoice reads what a focused component returned, whatever shape the
// transport decoded it into (the SDK hands a JSON value back, so a []string
// arrives as []any). ok is false for a cancel, which is a nil value; an empty
// list is a confirmed empty selection, as the original's empty Set is.
func pickerChoice(result any) (chosen []string, ok bool) {
	switch typed := result.(type) {
	case []string:
		return typed, true
	case []any:
		chosen = make([]string, 0, len(typed))
		for _, entry := range typed {
			if text, isString := entry.(string); isString {
				chosen = append(chosen, text)
			}
		}
		return chosen, true
	}
	return nil, false
}
