// Package acp is the PiG-side extension of the ACP Package. The editor entrypoint itself is the
// companion executable in cmd/pig-acp: an extension may not write protocol bytes to the host's
// stdout, so it cannot speak ACP. This extension is the in-session part: `/acp` reports how to
// point an editor at this agent, and what the adapter does and does not support.
package acp

import (
	"encoding/json"
	"os"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Extension returns the extension.
func Extension() *sdk.Extension {
	e := sdk.New("acp")
	e.Command("acp", "show how to use this agent from an ACP editor such as Zed", func(ctx sdk.Context, args string) error {
		switch strings.TrimSpace(args) {
		case "":
			ctx.Notify(zedSnippet()+"\n\n"+limits(), "info")
		case "zed":
			ctx.Notify(zedSnippet(), "info")
		default:
			ctx.Notify("Usage: /acp [zed]", "warning")
		}
		return nil
	})
	return e
}

// Executable is the path the editor configuration names for --pig: the running agent. Fused into a
// Piglet Binary the extension runs inside the agent, so that is this process. Installed as a Package it
// runs in its own extension process (a cell runner, not an agent); PiG then names the agent that
// started it in PIG_HARNESS_BINARY, the variable its extension processes use to re-launch the harness.
// That variable is not part of the public SDK: if a PiG release drops it, /acp names the runner again.
func Executable() string {
	if host := os.Getenv("PIG_HARNESS_BINARY"); host != "" {
		return host
	}
	if exe, err := os.Executable(); err == nil && exe != "" {
		return exe
	}
	return "pig"
}

func zedSnippet() string {
	cfg := map[string]any{"agent_servers": map[string]any{"PiG": map[string]any{
		"type": "custom", "command": "pig-acp", "args": []string{"--pig", Executable()}, "env": map[string]any{},
	}}}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	return "Agent Client Protocol (ACP): use this agent from an editor.\n" +
		"Install pig-acp (the companion executable of the acp Package), then add this to Zed's settings.json:\n\n" +
		string(b) +
		"\n\nOther ACP clients run: pig-acp --pig " + Executable()
}

func limits() string {
	return "Supported: sessions (new, load, list, delete), streaming text and thinking, tool calls with diffs and terminal output, " +
		"cancel, model and thinking-level selection, context usage, permission requests for extension select and confirm dialogs.\n" +
		"Not supported: file system and terminal delegation to the editor, MCP servers passed by the editor " +
		"(accepted, never started), extension slash commands, input and editor dialogs (cancelled with a notice)."
}
