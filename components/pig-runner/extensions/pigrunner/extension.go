package pigrunner

import (
	"fmt"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-play/libraries/gamemcp"
	"github.com/MichaelKinsy/pigpen/components/pig-play/libraries/sprite"
	"github.com/MichaelKinsy/pigpen/components/pig-play/libraries/termgame"
)

func Extension() *sdk.Extension {
	ext := sdk.New("pigrunner")
	// Opt-in: nothing listens, and no event handler is registered, until the user types
	// "/runner mcp on" or starts pig with PIG_GAMES_MCP=1 (see mcp.go).
	mcp := gamemcp.Attach(ext, gamemcp.Config{Game: mcpGame(), Server: mcpServer, Command: "runner"})
	run := func(ctx sdk.Context, args string) error {
		if handled, err := mcp.HandleCommand(ctx, args); handled {
			return err
		}
		return openRunner(ctx)
	}
	ext.Command("runner", "Open PiG Runner.", run)
	ext.Command("pig-runner", "Open PiG Runner.", run)
	return ext
}

// openRunner opens the overlay for the user's own game.
func openRunner(ctx sdk.Context) error {
	highScore := loadHighScore(ctx.ConfigHome())
	component := newRunnerComponent(highScore, sprite.ActiveVariant(ctx.ConfigHome()), ctx.Height)
	return show(ctx, component)
}

// show shows component as the overlay and reports the score when it closes.
func show(ctx sdk.Context, component *runnerComponent) error {
	// With no UI ctx.Custom returns at once and never disposes the component, which
	// would leave its 60 Hz ticker running. Dispose is idempotent, so it is safe when
	// the SDK has disposed it already.
	defer component.Dispose()
	result, err := ctx.Custom(component, termgame.Overlay("PiG Runner"))
	if err != nil {
		return err
	}

	score, highScore := runnerScores(result, component.State())
	if err := saveHighScore(ctx.ConfigHome(), highScore); err != nil {
		return err
	}
	ctx.Notify(fmt.Sprintf("PiG Runner score %d · high %d", score, highScore), "info")
	return nil
}

func runnerScores(result any, fallback runnerState) (score, highScore int) {
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
