// Package memoryskills implements Hermes Agent's built-in memory tool,
// progressive-disclosure skills tools (skills_list, skill_view, skill_manage),
// and three-tier system prompt assembly for pig-hermes.
package memoryskills

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/config"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/memory"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/paths"
)

// Register wires the memory tool, skills tools (skills_list, skill_view, skill_manage),
// and three-tier system prompt lifecycle hooks onto ext.
func Register(ext *sdk.Extension, p paths.ProfilePaths, cfg *config.Config, memStore *memory.Store) {
	if ext == nil {
		return
	}
	if cfg == nil {
		cfg = config.Default()
	}
	if memStore == nil {
		memStore = memory.NewStore(p.MemoriesDir, cfg.MemoryLimit, cfg.UserLimit)
	}

	registerMemoryTool(ext, cfg, memStore)
	registerSkillsTools(ext, p, cfg)
	registerPromptHooks(ext, p, cfg, memStore)
}
