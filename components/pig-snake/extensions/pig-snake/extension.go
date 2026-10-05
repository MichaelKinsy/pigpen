// Package pig_snake is Pig Snake: a snake game whose head is the PiG pig and
// whose every eaten apple adds another pig head to the line, so a herd forms
// behind the leader instead of one pig growing.
//
// It follows the conventions of PiG Standard's Angry Pigs and PiG Runner (PiG
// d86eb93, MIT, Michael Kinsy; see CREDITS.md): an explicit slash command
// opens a full-terminal overlay, arrows and WASD steer, p pauses, r retries, q
// or Esc leaves, and the high score is kept in a small private state file.
// Bundling the extension starts nothing: it registers two commands and no
// event handler, tool or flag.
package pig_snake

import (
	"fmt"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/pig-snake/internal/sprites"
	"github.com/MichaelKinsy/pigpen/pig-snake/internal/termgame"
)

// Extension returns the extension.
func Extension() *sdk.Extension {
	ext := sdk.New("pig-snake")
	ext.Command("pig-snake", "Play Pig Snake: every apple adds a pig to the herd.", run)
	ext.Command("snake", "Play Pig Snake: every apple adds a pig to the herd.", run)
	return ext
}

// newSprites is the single place that chooses the pig art. When the shared
// sprite package from lane pigpen-games is available, return an adapter that
// implements sprites.Source on top of it here.
func newSprites(configHome string) sprites.Source {
	return sprites.Builtin(sprites.LoadVariantID(configHome))
}

func run(ctx sdk.Context, _ string) error {
	// Print and JSON mode have no UI; RPC mode has dialogs but no custom
	// components (the host answers ui.custom with no_ui), so only the
	// interactive TUI can play. The SDK advises guarding terminal-only UI on "tui".
	if !ctx.HasUI() || ctx.Mode() != "tui" {
		ctx.Notify("Pig Snake needs an interactive terminal.", "warning")
		return nil
	}
	configHome := ctx.ConfigHome()
	component := newComponent(loadHighScores(configHome), newSprites(configHome), ctx.Height)
	result, err := ctx.Custom(component, termgame.Overlay("Pig Snake"))
	if err != nil {
		return err
	}
	final := resultState(result, component.State())
	if err := saveHighScores(configHome, highScores{High: final.HighScore, WrapHigh: final.WrapHigh}); err != nil {
		return err
	}
	name := "Pig Snake"
	if final.Wrap {
		name += " (wrap)"
	}
	ctx.Notify(fmt.Sprintf("%s score %d · herd %d · high %d", name, final.Score, final.Herd, final.high()), "info")
	return nil
}
