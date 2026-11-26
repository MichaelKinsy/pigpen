// Package relay is the Go side of the oh-my-pig Chrome extension: the native messaging host Chrome launches, the
// local relay server the browser tool connects to, the bridge that shares the one debugger attachment Chrome
// allows per tab, and the install and sync commands.
//
// upstream: packages/coding-agent/src/tools/browser/relay (omp v19.1.1) and packages/browser-relay (the extension).
//
// Names that would collide with an installed omp differ from omp's; the wire protocol between the extension, the
// native host and the relay keeps omp's names (OMP.downloadPath, /omp/..., ompRelay...), so the extension's code
// stays omp's apart from the names in this file.
package relay

import (
	"os"
	"path/filepath"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const (
	// NativeHostName is the native messaging host Chrome launches for the extension. omp's is "sh.omp.browser";
	// a name of its own lets both extensions be installed side by side.
	NativeHostName = "dev.pi_in_go.oh_my_pig.browser"

	// ExtensionName is the extension's name in its manifest and in messages to the user.
	ExtensionName = "oh-my-pig"

	// CLICommand is the subcommand the Binary serves before PiG parses its command line:
	// `oh-my-pig browser-relay install | sync | native-host`.
	CLICommand = "browser-relay"
)

// AgentDir is this Piglet's agent directory, resolved as the rest of the extension resolves it:
// PIG_CODING_AGENT_DIR, the SDK's agent directory, PIG_HOME/agent, ~/.pig/agent.
func AgentDir() string {
	if dir := strings.TrimSpace(os.Getenv("PIG_CODING_AGENT_DIR")); dir != "" {
		return dir
	}
	if dir, err := sdk.AgentDir(); err == nil && dir != "" {
		return dir
	}
	if dir := strings.TrimSpace(os.Getenv("PIG_HOME")); dir != "" {
		return filepath.Join(dir, "agent")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".pig", "agent")
	}
	return filepath.Join(os.TempDir(), "pig-agent")
}

// Root is the directory everything of the relay lives under (omp: ~/.omp/browser-relay).
func Root() string { return filepath.Join(AgentDir(), "browser-relay") }

// DefaultExtensionDir is where `browser-relay install` puts the extension without --dir.
func DefaultExtensionDir() string { return filepath.Join(Root(), "extension") }

// EndpointsDir holds one file per running relay server, named <pid>-<port> (upstream: relay/discovery.ts).
func EndpointsDir() string { return filepath.Join(Root(), "endpoints") }

// InstallsFile records every directory the extension was installed into (upstream: relay/installs.ts).
func InstallsFile() string { return filepath.Join(Root(), "installs.json") }
