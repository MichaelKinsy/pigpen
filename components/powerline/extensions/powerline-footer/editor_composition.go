package powerline_footer

import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

// Passing an autocomplete provider through the previous editor. upstream: editor-composition.ts.
//
// pi-tui's AutocompleteProvider is modelled by sdk.AutocompleteProvider, the type a Go extension can reach. A pi-tui editor,
// which upstream reads by reflection (`Reflect.get(editor, "autocompleteProvider")`), is any Go value: one that has the
// property implements autocompleteProviderHolder, one that has setAutocompleteProvider implements autocompleteProviderSetter.
//
// Nothing in the extension calls these yet. The caller is the editor setup (index.ts, setupCustomEditor), which needs the SDK's
// setEditorComponent and getEditorComponent; PiG v0.5.0 returns ErrUnsupportedSubprocessUI for the first and nil for the second.

// autocompleteProviderHolder is an editor that exposes its provider (Editor.autocompleteProvider).
type autocompleteProviderHolder interface {
	AutocompleteProvider() *sdk.AutocompleteProvider
}

// autocompleteProviderSetter is an editor that takes a provider (Editor.setAutocompleteProvider).
type autocompleteProviderSetter interface {
	SetAutocompleteProvider(provider *sdk.AutocompleteProvider)
}

// getEditorAutocompleteProvider is the editor's provider, when it has one with both getSuggestions and applyCompletion
// (editor-composition.ts:3-15); a provider missing either is not one.
func getEditorAutocompleteProvider(sourceEditor any) *sdk.AutocompleteProvider {
	holder, ok := sourceEditor.(autocompleteProviderHolder)
	if !ok {
		return nil
	}
	candidate := holder.AutocompleteProvider()
	if candidate == nil || candidate.GetSuggestions == nil || candidate.ApplyCompletion == nil {
		return nil
	}
	return candidate
}

// passAutocompleteProviderThroughPreviousEditor offers the provider to the editor that was installed before ours, so a wrapper
// that editor adds around it survives, and returns the provider the previous editor now holds (editor-composition.ts:17-32).
func passAutocompleteProviderThroughPreviousEditor(provider *sdk.AutocompleteProvider, previousEditor any) *sdk.AutocompleteProvider {
	if previousEditor == nil {
		return provider
	}
	setter, ok := previousEditor.(autocompleteProviderSetter)
	if !ok {
		if held := getEditorAutocompleteProvider(previousEditor); held != nil {
			return held
		}
		return provider
	}
	setter.SetAutocompleteProvider(provider)
	if held := getEditorAutocompleteProvider(previousEditor); held != nil {
		return held
	}
	return provider
}
