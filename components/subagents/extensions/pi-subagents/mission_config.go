package pi_subagents

import (
	"os"
	"path/filepath"
)

// loadMissionStoreConfig is `loadConfig().missions`: the `missions` object of ~/.pi/agent/extensions/subagent/config.json, or
// nil. The original drops the whole config (and logs) when any key of it fails validation; the part of config.ts this port
// has is the missions key, so an invalid `missions` (or a file that is not a JSON object) reads as no configuration.
func loadMissionStoreConfig() *MissionStoreConfig {
	raw, err := os.ReadFile(filepath.Join(agentDir(), "extensions", "subagent", "config.json"))
	if err != nil {
		return nil
	}
	parsed, msg := missionParseJSON(string(raw))
	root, ok := parsed.(*jsObject)
	if msg != "" || !ok {
		return nil
	}
	missions, present := dynGet(root, "missions")
	cfg, err := validateMissionStoreConfig(missions, present, "config.missions")
	if err != nil {
		return nil
	}
	return cfg
}
