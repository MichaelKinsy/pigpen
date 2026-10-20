package powerline_footer

import (
	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The autocomplete providers of bash mode, over the SDK's provider shape (a struct of callbacks). upstream:
// bash-mode/completion.ts (BashAutocompleteProvider, OneOffBashAutocompleteProvider, ModeAwareAutocompleteProvider).
//
// The signal of upstream's getSuggestions options is the callback's sdk.Context (cancelled with the request). Cursor columns
// count UTF-16 code units, as in pi-tui and the SDK.

// utf16Slice is s.slice(from, to) in UTF-16 code units (indices clamped to the string).
func utf16Slice(s string, from, to int) string { return *new(string) }

// applyExtendedCompletion replaces [StartCol, EndCol) of the cursor line with the item's replacement and puts the cursor behind
// it; the range is clamped to the line. upstream: completion.ts:441.
func applyExtendedCompletion(lines []string, cursorLine int, item extendedCompletionItem) sdk.AutocompleteCompletion {
	return *new(sdk.AutocompleteCompletion)
}

// errNotExtendedItem is upstream's thrown error for an item without a replacement range. The SDK's AutocompleteItem
// (value, label, description) cannot carry one, and both providers answer getSuggestions with nothing, so the host never holds
// an extended item of theirs: applyCompletion through the SDK shape always rejects, as upstream does for a foreign item.
func errNotExtendedItem(oneOff bool) error { return *new(error) }

func noSuggestions(sdk.Context, []string, int, int, bool) (*sdk.AutocompleteSuggestions, error) {
	return *new(*sdk.AutocompleteSuggestions), *new(error)
}

// newBashAutocompleteProvider is BashAutocompleteProvider: no suggestions (bash mode shows ghost text instead), never a file
// trigger.
func newBashAutocompleteProvider() *sdk.AutocompleteProvider {
	return *new(*sdk.AutocompleteProvider)
}

// newOneOffBashAutocompleteProvider is OneOffBashAutocompleteProvider: no suggestions, and a file trigger once the cursor is
// inside a "!cmd" or "!!cmd" on the first line, behind the bang.
func newOneOffBashAutocompleteProvider() *sdk.AutocompleteProvider {
	return *new(*sdk.AutocompleteProvider)
}

// providerTriggerCharacters: the single-character trigger characters of a provider.
func providerTriggerCharacters(p *sdk.AutocompleteProvider) []string { return *new([]string) }

// newModeAwareAutocompleteProvider is ModeAwareAutocompleteProvider: in bash mode the bash provider answers, in a one-off
// "!cmd" line the one-off provider, otherwise the previous (default) provider, which may be nil. A provider without a
// ShouldTriggerFileCompletion callback is one that does not support the hook (upstream's supportsShouldTriggerFileCompletion).
func newModeAwareAutocompleteProvider(defaultProvider, bashProvider, oneOffBashProvider *sdk.AutocompleteProvider, isBashModeActive func() bool) *sdk.AutocompleteProvider {
	return *new(*sdk.AutocompleteProvider)
}
