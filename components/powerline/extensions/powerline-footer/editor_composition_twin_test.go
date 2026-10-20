package powerline_footer

import (
	"reflect"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Twins of port/oracle/tests/editor-composition.test.ts.
//
// pi-tui's AutocompleteProvider is modelled by the SDK's sdk.AutocompleteProvider (the one a Go extension can reach). A pi-tui
// editor is any Go value: one that has the provider property implements AutocompleteProvider(), one that has
// setAutocompleteProvider implements SetAutocompleteProvider(p). The callbacks take a zero sdk.Context, which these providers
// never read. No Go editor exists to pass in until the SDK gets setEditorComponent, so nothing in the extension calls these yet.

func baseProvider() *sdk.AutocompleteProvider {
	return &sdk.AutocompleteProvider{
		GetSuggestions: func(_ sdk.Context, _ []string, _ int, _ int, _ bool) (*sdk.AutocompleteSuggestions, error) {
			return &sdk.AutocompleteSuggestions{Prefix: "@", Items: []sdk.AutocompleteItem{{Label: "base", Value: "base"}}}, nil
		},
		ApplyCompletion: func(_ sdk.Context, lines []string, cursorLine, cursorCol int, _ sdk.AutocompleteItem, _ string) (sdk.AutocompleteCompletion, error) {
			return sdk.AutocompleteCompletion{Lines: lines, CursorLine: cursorLine, CursorCol: cursorCol}, nil
		},
	}
}

// wrappingEditor is the upstream test's previousEditor: its setter stores a provider that adds one more item.
type wrappingEditor struct{ provider *sdk.AutocompleteProvider }

func (e *wrappingEditor) AutocompleteProvider() *sdk.AutocompleteProvider { return e.provider }
func (e *wrappingEditor) SetAutocompleteProvider(provider *sdk.AutocompleteProvider) {
	e.provider = &sdk.AutocompleteProvider{
		GetSuggestions: func(ctx sdk.Context, lines []string, line, col int, force bool) (*sdk.AutocompleteSuggestions, error) {
			suggestions, err := provider.GetSuggestions(ctx, lines, line, col, force)
			if err != nil || suggestions == nil {
				return suggestions, err
			}
			items := append(append([]sdk.AutocompleteItem(nil), suggestions.Items...), sdk.AutocompleteItem{Label: "previous-wrapper", Value: "previous-wrapper"})
			return &sdk.AutocompleteSuggestions{Prefix: suggestions.Prefix, Items: items}, nil
		},
		ApplyCompletion: provider.ApplyCompletion,
	}
}

// holderOnly has the provider property but no setter; setterOnly the reverse; neither implements both.
type holderOnly struct{ provider *sdk.AutocompleteProvider }

func (e holderOnly) AutocompleteProvider() *sdk.AutocompleteProvider { return e.provider }

type setterOnly struct{ got *sdk.AutocompleteProvider }

func (e *setterOnly) SetAutocompleteProvider(p *sdk.AutocompleteProvider) { e.got = p }

func labels(items []sdk.AutocompleteItem) []string {
	out := []string{}
	for _, item := range items {
		out = append(out, item.Label)
	}
	return out
}

func TestEditorCompositionTwins(t *testing.T) {
	tw(t, "editor-composition", "previous editor autocomplete provider wrappers are preserved", func(t *testing.T) {
		previous := &wrappingEditor{}
		composed := passAutocompleteProviderThroughPreviousEditor(baseProvider(), previous)
		suggestions, err := composed.GetSuggestions(sdk.Context{}, []string{"@"}, 0, 1, false)
		if err != nil {
			t.Fatal(err)
		}
		if composed != previous.provider {
			t.Error("the composed provider is not the previous editor's own provider")
		}
		if got, want := labels(suggestions.Items), []string{"base", "previous-wrapper"}; !reflect.DeepEqual(got, want) {
			t.Errorf("labels = %q, want %q", got, want)
		}
	})

	tw(t, "editor-composition", "autocomplete provider detection rejects incomplete editor providers", func(t *testing.T) {
		if got := getEditorAutocompleteProvider(holderOnly{provider: &sdk.AutocompleteProvider{}}); got != nil {
			t.Errorf("an empty provider was accepted: %+v", got)
		}
		onlySuggestions := &sdk.AutocompleteProvider{GetSuggestions: baseProvider().GetSuggestions}
		if got := getEditorAutocompleteProvider(holderOnly{provider: onlySuggestions}); got != nil {
			t.Errorf("a provider without applyCompletion was accepted")
		}
		if got := getEditorAutocompleteProvider(holderOnly{provider: baseProvider()}); got == nil {
			t.Error("a complete provider was rejected")
		}
	})
}

func TestAutocompleteProviderDetectionTakesAnyValue(t *testing.T) {
	onlyApply := &sdk.AutocompleteProvider{ApplyCompletion: baseProvider().ApplyCompletion}
	var nilHolder *wrappingEditor
	for name, source := range map[string]any{
		"nil":                       nil,
		"a string":                  "editor",
		"a number":                  42,
		"a struct without a holder": struct{}{},
		"a holder of nil":           holderOnly{},
		"only applyCompletion":      holderOnly{provider: onlyApply},
	} {
		if got := getEditorAutocompleteProvider(source); got != nil {
			t.Errorf("%s: got %+v, want nil", name, got)
		}
	}
	_ = nilHolder
	want := baseProvider()
	if got := getEditorAutocompleteProvider(holderOnly{provider: want}); got != want {
		t.Errorf("the provider is returned as it is, not copied")
	}
}

func TestPassAutocompleteProviderThroughPreviousEditor(t *testing.T) {
	provider := baseProvider()
	t.Run("no previous editor returns the provider", func(t *testing.T) {
		for name, previous := range map[string]any{"nil": nil, "a string": "x", "a number": 1} {
			if got := passAutocompleteProviderThroughPreviousEditor(provider, previous); got != provider {
				t.Errorf("%s: got %p, want the provider", name, got)
			}
		}
	})
	t.Run("an editor without a setter lends the provider it has", func(t *testing.T) {
		own := baseProvider()
		if got := passAutocompleteProviderThroughPreviousEditor(provider, holderOnly{provider: own}); got != own {
			t.Error("the previous editor's provider was not used")
		}
	})
	t.Run("an editor without a setter or a provider returns the provider", func(t *testing.T) {
		if got := passAutocompleteProviderThroughPreviousEditor(provider, struct{}{}); got != provider {
			t.Error("the provider was replaced")
		}
		if got := passAutocompleteProviderThroughPreviousEditor(provider, holderOnly{provider: &sdk.AutocompleteProvider{}}); got != provider {
			t.Error("an incomplete provider of the previous editor was used")
		}
	})
	t.Run("a setter that keeps no provider returns the provider", func(t *testing.T) {
		previous := &setterOnly{}
		if got := passAutocompleteProviderThroughPreviousEditor(provider, previous); got != provider {
			t.Error("the provider was replaced")
		}
		if previous.got != provider {
			t.Error("the setter was not called with the provider")
		}
	})
	t.Run("a setter is called exactly with the provider", func(t *testing.T) {
		previous := &wrappingEditor{}
		passAutocompleteProviderThroughPreviousEditor(provider, previous)
		if previous.provider == nil || previous.provider == provider {
			t.Error("the previous editor did not wrap the provider")
		}
	})
	t.Run("a setter that wraps incompletely falls back to the provider", func(t *testing.T) {
		previous := &brokenWrapper{}
		if got := passAutocompleteProviderThroughPreviousEditor(provider, previous); got != provider {
			t.Error("an incomplete wrapper was used")
		}
	})
}

type brokenWrapper struct{ provider *sdk.AutocompleteProvider }

func (e *brokenWrapper) AutocompleteProvider() *sdk.AutocompleteProvider { return e.provider }
func (e *brokenWrapper) SetAutocompleteProvider(*sdk.AutocompleteProvider) {
	e.provider = &sdk.AutocompleteProvider{}
}
