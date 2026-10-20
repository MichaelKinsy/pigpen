package warden

// ConscienceSkillConfig (config.ts 400-405). Mode: off | recommend | load.
type ConscienceSkillConfig struct {
	Mode    string   `json:"mode"`
	Exclude []string `json:"exclude"`
}

// ConscienceToolConfig (config.ts 407-412).
type ConscienceToolConfig struct {
	Enabled bool     `json:"enabled"`
	Exclude []string `json:"exclude"`
}

// ConscienceConfig (config.ts 414-437).
type ConscienceConfig struct {
	Enabled            bool                  `json:"enabled"`
	Skills             ConscienceSkillConfig `json:"skills"`
	Tools              ConscienceToolConfig  `json:"tools"`
	SkipTools          []string              `json:"skipTools"`
	TimeoutMs          int                   `json:"timeoutMs"`
	MaxAssessments     int                   `json:"maxAssessments"`
	MaxNudges          int                   `json:"maxNudges"`
	MaxSkillBytes      int                   `json:"maxSkillBytes"`
	MaxLoadedBytes     int                   `json:"maxLoadedBytes"`
	RecommendThreshold float64               `json:"recommendThreshold"`
	AdvanceThreshold   float64               `json:"advanceThreshold"`
}

func defaultConscienceConfig() ConscienceConfig {
	return ConscienceConfig{
		Enabled:            false,
		Skills:             ConscienceSkillConfig{Mode: "recommend", Exclude: []string{}},
		Tools:              ConscienceToolConfig{Enabled: true, Exclude: []string{}},
		SkipTools:          []string{"read", "bash", "edit", "write", "grep", "find", "ls"},
		TimeoutMs:          3000,
		MaxAssessments:     3,
		MaxNudges:          2,
		MaxSkillBytes:      32768,
		MaxLoadedBytes:     65536,
		RecommendThreshold: 0.80,
		AdvanceThreshold:   0.70,
	}
}
