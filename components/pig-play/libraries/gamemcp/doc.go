// Package gamemcp lets an agent play a Pigpen game through a tiny MCP server.
//
// It is opt-in and off by default. Nothing listens until the user turns it on (the game's
// "mcp on" command argument, or PIG_GAMES_MCP=1 in the environment of the session). Then
// the package serves streamable-HTTP MCP on 127.0.0.1 only, on a random port, behind a
// random bearer token, and registers that server with the host through the SDK's
// RegisterMcpServer with the default codemode exposure. Session end, reload and
// "mcp off" unregister the server and close the listener.
//
// The server has five tools: games_list, game_start, game_state, game_act and
// game_score. games_list and the MCP instructions carry the game's own instructions;
// game_act "stop" ends the play (the game freezes and shows its final score until a key
// closes the overlay), and game_score answers after the game too. A game implements
// [Game] and [Instance]; this package never knows the rules.
package gamemcp
