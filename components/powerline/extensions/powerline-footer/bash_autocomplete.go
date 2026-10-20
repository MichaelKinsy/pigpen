package powerline_footer

import (
	"errors"
	"slices"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The autocomplete providers of bash mode, over the SDK's provider shape (a struct of callbacks). upstream:
// bash-mode/completion.ts (BashAutocompleteProvider, OneOffBashAutocompleteProvider, ModeAwareAutocompleteProvider).
//
// The signal of upstream's getSuggestions options is the callback's sdk.Context (cancelled with the request). Cursor columns
// count UTF-16 code units, as in pi-tui and the SDK.

// utf16Slice is s.slice(from, to) in UTF-16 code units (indices clamped to the string).
func utf16Slice(s string, from, to int) string {
	u := utf16.Encode([]rune(s))
	from, to = min(max(from, 0), len(u)), min(max(to, 0), len(u))
	if from >= to {
		return ""
	}
	return string(utf16.Decode(u[from:to]))
}

// applyExtendedCompletion replaces [StartCol, EndCol) of the cursor line with the item's replacement and puts the cursor behind
// it; the range is clamped to the line. upstream: completion.ts:441.
func applyExtendedCompletion(lines []string, cursorLine int, item extendedCompletionItem) sdk.AutocompleteCompletion {
	currentLine := ""
	if cursorLine >= 0 && cursorLine < len(lines) {
		currentLine = lines[cursorLine]
	}
	length := utf16Len(currentLine)
	startCol := max(0, min(item.StartCol, length))
	endCol := max(startCol, min(item.EndCol, length))
	nextLine := utf16Slice(currentLine, 0, startCol) + item.Replacement + utf16Slice(currentLine, endCol, length)
	nextLines := slices.Clone(lines)
	if cursorLine >= 0 && cursorLine < len(nextLines) {
		nextLines[cursorLine] = nextLine
	}
	return sdk.AutocompleteCompletion{Lines: nextLines, CursorLine: cursorLine, CursorCol: startCol + utf16Len(item.Replacement)}
}

// errNotExtendedItem is upstream's thrown error for an item without a replacement range. The SDK's AutocompleteItem
// (value, label, description) cannot carry one, and both providers answer getSuggestions with nothing, so the host never holds
// an extended item of theirs: applyCompletion through the SDK shape always rejects, as upstream does for a foreign item.
func errNotExtendedItem(oneOff bool) error {
	if oneOff {
		return errors.New("Expected an extended completion item for one-off bash autocomplete")
	}
	return errors.New("Expected an extended completion item for bash autocomplete")
}

func noSuggestions(sdk.Context, []string, int, int, bool) (*sdk.AutocompleteSuggestions, error) {
	return nil, nil
}

// newBashAutocompleteProvider is BashAutocompleteProvider: no suggestions (bash mode shows ghost text instead), never a file
// trigger.
func newBashAutocompleteProvider() *sdk.AutocompleteProvider {
	return &sdk.AutocompleteProvider{
		GetSuggestions: noSuggestions,
		ApplyCompletion: func(sdk.Context, []string, int, int, sdk.AutocompleteItem, string) (sdk.AutocompleteCompletion, error) {
			return sdk.AutocompleteCompletion{}, errNotExtendedItem(false)
		},
		ShouldTriggerFileCompletion: func(sdk.Context, []string, int, int) (bool, error) { return false, nil },
	}
}

// newOneOffBashAutocompleteProvider is OneOffBashAutocompleteProvider: no suggestions, and a file trigger once the cursor is
// inside a "!cmd" or "!!cmd" on the first line, behind the bang.
func newOneOffBashAutocompleteProvider() *sdk.AutocompleteProvider {
	return &sdk.AutocompleteProvider{
		GetSuggestions: noSuggestions,
		ApplyCompletion: func(sdk.Context, []string, int, int, sdk.AutocompleteItem, string) (sdk.AutocompleteCompletion, error) {
			return sdk.AutocompleteCompletion{}, errNotExtendedItem(true)
		},
		ShouldTriggerFileCompletion: func(_ sdk.Context, lines []string, cursorLine, cursorCol int) (bool, error) {
			var bang *oneOffBashCommandContext
			if cursorLine == 0 {
				first := ""
				if len(lines) > 0 {
					first = lines[0]
				}
				bang = getOneOffBashCommandContext(first)
			}
			return bang != nil && cursorCol >= bang.Offset, nil
		},
	}
}

// providerTriggerCharacters: the single-character trigger characters of a provider.
func providerTriggerCharacters(p *sdk.AutocompleteProvider) []string {
	if p == nil {
		return nil
	}
	var out []string
	for _, c := range p.TriggerCharacters {
		if utf16Len(c) == 1 {
			out = append(out, c)
		}
	}
	return out
}

// newModeAwareAutocompleteProvider is ModeAwareAutocompleteProvider: in bash mode the bash provider answers, in a one-off
// "!cmd" line the one-off provider, otherwise the previous (default) provider, which may be nil. A provider without a
// ShouldTriggerFileCompletion callback is one that does not support the hook (upstream's supportsShouldTriggerFileCompletion).
func newModeAwareAutocompleteProvider(defaultProvider, bashProvider, oneOffBashProvider *sdk.AutocompleteProvider, isBashModeActive func() bool) *sdk.AutocompleteProvider {
	var triggers []string
	for _, p := range []*sdk.AutocompleteProvider{defaultProvider, bashProvider, oneOffBashProvider} {
		for _, c := range providerTriggerCharacters(p) {
			if !slices.Contains(triggers, c) {
				triggers = append(triggers, c)
			}
		}
	}
	useOneOff := func(ctx sdk.Context, lines []string, cursorLine, cursorCol int) (bool, error) {
		if oneOffBashProvider == nil || oneOffBashProvider.ShouldTriggerFileCompletion == nil {
			return false, nil
		}
		return oneOffBashProvider.ShouldTriggerFileCompletion(ctx, lines, cursorLine, cursorCol)
	}
	return &sdk.AutocompleteProvider{
		TriggerCharacters: triggers,
		GetSuggestions: func(ctx sdk.Context, lines []string, cursorLine, cursorCol int, force bool) (*sdk.AutocompleteSuggestions, error) {
			if isBashModeActive() {
				return bashProvider.GetSuggestions(ctx, lines, cursorLine, cursorCol, force)
			}
			oneOff, err := useOneOff(ctx, lines, cursorLine, cursorCol)
			if err != nil {
				return nil, err
			}
			if oneOff {
				return oneOffBashProvider.GetSuggestions(ctx, lines, cursorLine, cursorCol, force)
			}
			if defaultProvider == nil {
				return nil, nil
			}
			return defaultProvider.GetSuggestions(ctx, lines, cursorLine, cursorCol, force)
		},
		ApplyCompletion: func(ctx sdk.Context, lines []string, cursorLine, cursorCol int, item sdk.AutocompleteItem, prefix string) (sdk.AutocompleteCompletion, error) {
			if isBashModeActive() {
				return bashProvider.ApplyCompletion(ctx, lines, cursorLine, cursorCol, item, prefix)
			}
			oneOff, err := useOneOff(ctx, lines, cursorLine, cursorCol)
			if err != nil {
				return sdk.AutocompleteCompletion{}, err
			}
			if oneOff {
				return oneOffBashProvider.ApplyCompletion(ctx, lines, cursorLine, cursorCol, item, prefix)
			}
			if defaultProvider == nil {
				return sdk.AutocompleteCompletion{Lines: lines, CursorLine: cursorLine, CursorCol: cursorCol}, nil
			}
			return defaultProvider.ApplyCompletion(ctx, lines, cursorLine, cursorCol, item, prefix)
		},
		ShouldTriggerFileCompletion: func(ctx sdk.Context, lines []string, cursorLine, cursorCol int) (bool, error) {
			if isBashModeActive() {
				if bashProvider.ShouldTriggerFileCompletion == nil {
					return true, nil
				}
				return bashProvider.ShouldTriggerFileCompletion(ctx, lines, cursorLine, cursorCol)
			}
			oneOff, err := useOneOff(ctx, lines, cursorLine, cursorCol)
			if err != nil {
				return false, err
			}
			if oneOff {
				return true, nil
			}
			if defaultProvider == nil || defaultProvider.ShouldTriggerFileCompletion == nil {
				return false, nil
			}
			return defaultProvider.ShouldTriggerFileCompletion(ctx, lines, cursorLine, cursorCol)
		},
	}
}
