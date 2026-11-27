// Package pighermes implements the fused PiG extension for pig-hermes,
// an unofficial Go homage to Nous Research's Hermes Agent.
package pighermes

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/config"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/memory"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/paths"
	"github.com/MichaelKinsy/pigpen/pig-hermes/agents"
	"github.com/MichaelKinsy/pigpen/pig-hermes/filetools"
	"github.com/MichaelKinsy/pigpen/pig-hermes/media"
	"github.com/MichaelKinsy/pigpen/pig-hermes/memoryskills"
	"github.com/MichaelKinsy/pigpen/pig-hermes/plugins"
	"github.com/MichaelKinsy/pigpen/pig-hermes/sessions"
	"github.com/MichaelKinsy/pigpen/pig-hermes/surface"
	"github.com/MichaelKinsy/pigpen/pig-hermes/terminal"
	"github.com/MichaelKinsy/pigpen/pig-hermes/webmcp"
)

// Extension is the PiG factory entry point for pig-hermes.
func Extension() *sdk.Extension {
	ext := sdk.New("pig-hermes")
	p := paths.ResolveProfilePaths("")
	cfg, _ := config.Load(p.ConfigFile, p.EnvFile)
	memStore := memory.NewStore(p.MemoriesDir, cfg.MemoryLimit, cfg.UserLimit)

	filetools.Register(ext, cfg)
	terminal.Register(ext, cfg)
	memoryskills.Register(ext, p, cfg, memStore)
	sessions.Register(ext, p, cfg)
	agents.Register(ext, p, cfg)
	webmcp.Register(ext, p, cfg)
	media.Register(ext, p, cfg)
	plugins.Register(ext, p, cfg)
	surface.Register(ext, p, cfg, memStore)
	return ext
}
