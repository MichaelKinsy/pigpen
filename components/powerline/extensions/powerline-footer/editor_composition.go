package powerline_footer

import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

// Passing an autocomplete provider through the previous editor. upstream: editor-composition.ts.

// autocompleteProviderHolder is an editor that exposes its provider (pi-tui's Editor.autocompleteProvider property).
type autocompleteProviderHolder interface {
	AutocompleteProvider() *sdk.AutocompleteProvider
}

// autocompleteProviderSetter is an editor that takes a provider (pi-tui's Editor.setAutocompleteProvider).
type autocompleteProviderSetter interface {
	SetAutocompleteProvider(provider *sdk.AutocompleteProvider)
}

func getEditorAutocompleteProvider(sourceEditor any) *sdk.AutocompleteProvider { return nil }

func passAutocompleteProviderThroughPreviousEditor(provider *sdk.AutocompleteProvider, previousEditor any) *sdk.AutocompleteProvider {
	return nil
}
