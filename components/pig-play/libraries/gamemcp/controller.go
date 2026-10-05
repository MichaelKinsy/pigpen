package gamemcp

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// EnvVar turns the server on for a whole session when it is 1, true, yes or on in the
// environment pig starts with.
const EnvVar = "PIG_GAMES_MCP"

// Config is what a game gives Attach.
type Config struct {
	Game Game
	// Server is the MCP server name. Codemode reaches its tools as mcp__<Server>__<tool>
	// (every character other than letters, digits and underscore becomes an underscore),
	// so give each game its own, for example "games-runner".
	Server string
	// Command is the slash command the user types, without the slash, for messages.
	Command string
}

// Controller owns the opt-in: the listener, its registration with the host, and their
// removal. One game extension has one Controller.
type Controller struct {
	ext     *sdk.Extension
	cfg     Config
	session *Session

	mu       sync.Mutex
	server   *Server
	unsubEnd func()
}

// Attach wires the opt-in into ext and returns its Controller. It registers no tool, no
// command and, unless PIG_GAMES_MCP is set, no event handler: a game that is not asked
// stays inert. With PIG_GAMES_MCP set it starts the server on session start. Otherwise
// the user turns it on with HandleCommand.
func Attach(ext *sdk.Extension, cfg Config) *Controller {
	c := &Controller{ext: ext, cfg: cfg, session: NewSession(cfg.Game)}
	if envEnabled(os.Getenv(EnvVar)) {
		ext.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
			if err := c.Enable(ctx); err != nil {
				return nil, err
			}
			return nil, nil
		})
	}
	return c
}

func envEnabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// Running reports whether the server is listening.
func (c *Controller) Running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.server != nil
}

// Session is the tool state, for the game to reach the HUD agent.
func (c *Controller) Session() *Session { return c.session }

// HandleCommand handles "mcp on", "mcp off" and "mcp status" in the game command's
// arguments. It reports false, leaving the arguments to the game, when they do not start
// with "mcp".
func (c *Controller) HandleCommand(ctx sdk.Context, args string) (bool, error) {
	fields := strings.Fields(args)
	if len(fields) == 0 || fields[0] != "mcp" {
		return false, nil
	}
	usage := fmt.Sprintf("Usage: /%s mcp on|off|status", c.cfg.Command)
	if len(fields) != 2 {
		ctx.Notify(usage, "warning")
		return true, nil
	}
	switch fields[1] {
	case "on":
		if c.Running() {
			ctx.Notify(c.cfg.Game.Name+" MCP server is already on", "info")
			return true, nil
		}
		if err := c.Enable(ctx); err != nil {
			return true, err
		}
		ctx.Notify(fmt.Sprintf("%s MCP server on (127.0.0.1 only, this session). Agents find it with searchTools(\"game\") as %s.", c.cfg.Game.Name, c.cfg.Server), "info")
	case "off":
		if !c.Running() {
			ctx.Notify(c.cfg.Game.Name+" MCP server is off", "info")
			return true, nil
		}
		c.Disable(ctx)
		ctx.Notify(c.cfg.Game.Name+" MCP server off", "info")
	case "status":
		state := "off"
		if c.Running() {
			state = "on"
		}
		ctx.Notify(fmt.Sprintf("%s MCP server %s", c.cfg.Game.Name, state), "info")
	default:
		ctx.Notify(usage, "warning")
	}
	return true, nil
}

// Enable starts the server and registers it with the host. ctx is retained to open the
// game's overlay on game_start. Enabling twice is a no-op.
func (c *Controller) Enable(ctx sdk.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.server != nil {
		return nil
	}
	// The context first: from the moment the host holds the token a script may call
	// game_start, which opens the overlay with it.
	c.session.SetContext(ctx)
	server, err := StartServer(c.cfg.Server, c.cfg.Game.ServerInstructions(), c.session.Tools())
	if err != nil {
		return err
	}
	err = ctx.RegisterMcpServer(c.cfg.Server, sdk.McpServerConfig{
		Type:        "http",
		URL:         server.URL(),
		Headers:     sdk.NewOrderedStrings("Authorization", "Bearer "+server.Token()),
		Description: c.cfg.Game.Description + " Tools: games_list, game_start, game_state, game_act, game_score. An AI plays by reading game_state and answering with game_act; game_act stop ends the play; games_list has the instructions.",
		Exposure:    sdk.McpExposureCodemode,
	})
	if err != nil {
		return errors.Join(fmt.Errorf("register the %s MCP server: %w", c.cfg.Server, err), server.Close())
	}
	c.server = server
	// The session's end and a reload both raise session_shutdown. Subscribe now, so a game
	// that was never asked has no handler.
	c.unsubEnd = c.ext.OnSessionShutdown(func(ctx sdk.Context, _ map[string]any) (any, error) {
		c.disable(ctx, false)
		return nil, nil
	})
	return nil
}

// Disable unregisters the server and stops the listener.
func (c *Controller) Disable(ctx sdk.Context) { c.disable(ctx, true) }

// disable stops the server. The shutdown handler keeps its own subscription: the session is
// ending, and the host is waiting for that handler to return.
func (c *Controller) disable(ctx sdk.Context, unsubscribe bool) {
	c.mu.Lock()
	server, unsub := c.server, c.unsubEnd
	c.server, c.unsubEnd = nil, nil
	c.mu.Unlock()
	if server == nil {
		return
	}
	ctx.UnregisterMcpServer(c.cfg.Server)
	_ = server.Close()
	if unsubscribe && unsub != nil {
		unsub()
	}
}
