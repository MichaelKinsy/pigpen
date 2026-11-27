package prediction

import (
	"fmt"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/surface"
)

const (
	// WidgetKey is the SDK widget key and root kit.View ID used to bridge
	// composer prediction choices to `piglets/pig-tern/frontend`.
	WidgetKey = "omp.composer.prediction"
	// ChoiceIDPrefix is the ID prefix for each choice child node (`omp.composer.prediction.choice#<i>`).
	ChoiceIDPrefix = "omp.composer.prediction.choice#"
)

// BuildNativeWidgetView constructs the structured `kit.View` carrying the
// prediction choices (most likely first) for Tern's native frontend.
// `pig-tern/frontend` extracts this node in `ompDockWidgetParts`, never draws
// it as visible dock text, and feeds `Field.Ghost` from its children.
// Returns (zero, false) when choices is empty.
func BuildNativeWidgetView(choices []string) (kit.View, bool) {
	if len(choices) == 0 {
		return kit.View{}, false
	}
	children := make([]kit.StackChild, 0, len(choices))
	for i, choice := range choices {
		text := strings.TrimSpace(choice)
		if text == "" {
			continue
		}
		children = append(children, &kit.Text{
			ID:   fmt.Sprintf("%s%d", ChoiceIDPrefix, i),
			Text: text,
		})
	}
	if len(children) == 0 {
		return kit.View{}, false
	}
	root := &kit.VStack{
		ID:       WidgetKey,
		Gap:      0,
		Children: children,
	}
	return kit.View{Root: root}, true
}

// publishWidget emits the hidden prediction widget on Tern's native surface.
// On plain-terminal (non-Tern) surfaces it is a no-op so plain-terminal
// scrollback and ANSI proofs are never polluted with raw dock text.
func publishWidget(ctx sdk.Context, choices []string) {
	if !ctx.HasUI() || !surface.Native() {
		return
	}
	view, ok := BuildNativeWidgetView(choices)
	if !ok {
		_ = ctx.SetWidget(WidgetKey, nil)
		return
	}
	_ = ctx.SetWidget(WidgetKey, view)
}

// clearWidget removes any published prediction widget unconditionally (not
// gated on `surface.Native()`) so a Tern->terminal fallback never leaves a
// stale widget behind.
func clearWidget(ctx sdk.Context) {
	if !ctx.HasUI() {
		return
	}
	_ = ctx.SetWidget(WidgetKey, nil)
}
