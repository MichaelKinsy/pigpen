// Package angrypigs is the PiG Standard /angry-pigs game: launch pigs from a
// slingshot to knock birds off their towers. It draws a half-block pixel-art
// scene over the whole terminal through the remote-component API of an
// ordinary Go extension Resource.
package angrypigs

import (
	"fmt"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-play/libraries/gamemcp"
	"github.com/MichaelKinsy/pigpen/components/pig-play/libraries/sprite"
	"github.com/MichaelKinsy/pigpen/components/pig-play/libraries/termgame"
)

func Extension() *sdk.Extension {
	ext := sdk.New("angrypigs")
	// Opt-in: nothing listens, and no event handler is registered, until the user types
	// "/angry-pigs mcp on" or starts pig with PIG_GAMES_MCP=1 (see mcp.go).
	mcp := gamemcp.Attach(ext, gamemcp.Config{Game: mcpGame(), Server: mcpServer, Command: "angry-pigs"})
	run := func(ctx sdk.Context, args string) error {
		if handled, err := mcp.HandleCommand(ctx, args); handled {
			return err
		}
		return openAngryPigs(ctx)
	}
	ext.Command("angry-pigs", "Play Angry Pigs: launch pigs at the birds.", run)
	return ext
}

// openAngryPigs opens the overlay for the user's own game.
func openAngryPigs(ctx sdk.Context) error {
	highScore := loadHighScore(ctx.ConfigHome())
	game := newComponent(highScore, sprite.ActiveVariant(ctx.ConfigHome()), ctx.Height)
	return show(ctx, game)
}

// show shows game as the overlay and reports the score when it closes.
func show(ctx sdk.Context, game *component) error {
	// With no UI ctx.Custom returns at once and never disposes the component, which
	// would leave its ticker running. Dispose is idempotent, so it is safe when the SDK
	// has disposed it already.
	defer game.Dispose()
	result, err := ctx.Custom(game, termgame.Overlay("Angry Pigs"))
	if err != nil {
		return err
	}
	score, highScore := scores(result, game.State())
	if err := saveHighScore(ctx.ConfigHome(), highScore); err != nil {
		return err
	}
	ctx.Notify(fmt.Sprintf("Angry Pigs score %d · high %d", score, highScore), "info")
	return nil
}

func scores(result any, fallback gameState) (score, highScore int) {
	score, highScore = fallback.Score, fallback.HighScore
	values, ok := result.(map[string]any)
	if !ok {
		return score, highScore
	}
	if value, ok := values["score"].(float64); ok && value >= 0 {
		score = int(value)
	}
	if value, ok := values["highScore"].(float64); ok && value >= 0 {
		highScore = int(value)
	}
	return score, max(highScore, score)
}
