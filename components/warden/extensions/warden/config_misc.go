package warden

// Small config sections of upstream WardenConfig (config.ts), one type and default function each.

// ProseConfig (config.ts 194-204).
type ProseConfig struct {
	Enabled   bool    `json:"enabled"`
	Audience  string  `json:"audience"`
	Threshold float64 `json:"threshold"`
	Trend     int     `json:"trend"`
	MinChars  int     `json:"minChars"`
}

// SlopConfig (config.ts 206-211).
type SlopConfig struct {
	Enabled   bool        `json:"enabled"`
	Threshold float64     `json:"threshold"`
	Prose     ProseConfig `json:"prose"`
}

func defaultSlopConfig() SlopConfig {
	return SlopConfig{Enabled: true, Threshold: 0.7, Prose: ProseConfig{Enabled: true, Audience: "technical", Threshold: 0.7, Trend: 2, MinChars: 200}}
}

// SecurityConfig (config.ts 213-219).
type SecurityConfig struct {
	Enabled    bool    `json:"enabled"`
	Threshold  float64 `json:"threshold"`
	MaskOutput bool    `json:"maskOutput"`
}

func defaultSecurityConfig() SecurityConfig {
	return SecurityConfig{Enabled: true, Threshold: 0.7, MaskOutput: true}
}

// RunawayConfig (config.ts 300-310).
type RunawayConfig struct {
	Enabled         bool `json:"enabled"`
	Repeats         int  `json:"repeats"`
	ThinkingRepeats int  `json:"thinkingRepeats"`
	MinChars        int  `json:"minChars"`
	Recover         bool `json:"recover"`
}

func defaultRunawayConfig() RunawayConfig {
	return RunawayConfig{Enabled: true, Repeats: 4, ThinkingRepeats: 10, MinChars: 400, Recover: true}
}

// NotifyConfig (config.ts 319-329).
type NotifyConfig struct {
	Enabled    bool     `json:"enabled"`
	CooldownMs int      `json:"cooldownMs"`
	Command    []string `json:"command"`
}

func defaultNotifyConfig() NotifyConfig {
	return NotifyConfig{Enabled: false, CooldownMs: 10000, Command: []string{}}
}

// JudgeConfig (config.ts 312-317).
type JudgeConfig struct {
	FailuresBeforeCooldown int `json:"failuresBeforeCooldown"`
	CooldownMs             int `json:"cooldownMs"`
}

func defaultJudgeConfig() JudgeConfig {
	return JudgeConfig{FailuresBeforeCooldown: 3, CooldownMs: 60000}
}

// SubagentConfig (config.ts 331-340).
type SubagentConfig struct {
	Enabled    bool    `json:"enabled"`
	Wake       bool    `json:"wake"`
	Threshold  float64 `json:"threshold"`
	CooldownMs int     `json:"cooldownMs"`
}

func defaultSubagentConfig() SubagentConfig {
	return SubagentConfig{Enabled: true, Wake: true, Threshold: 0.8, CooldownMs: 120000}
}

// SteersConfig (config.ts 376-389): per-model steer adaptation.
type SteersConfig struct {
	Adaptive     bool    `json:"adaptive"`
	MinSteers    int     `json:"minSteers"`
	MinFollowed  float64 `json:"minFollowed"`
	MaxDisputed  float64 `json:"maxDisputed"`
	RecheckEvery int     `json:"recheckEvery"`
	ProbeEvery   int     `json:"probeEvery"`
}

func defaultSteersConfig() SteersConfig {
	return SteersConfig{Adaptive: true, MinSteers: 30, MinFollowed: 0.2, MaxDisputed: 0.4, RecheckEvery: 30, ProbeEvery: 5}
}

// LearningConfig (config.ts 391-396).
type LearningConfig struct {
	PatternAnalysis bool `json:"patternAnalysis"`
	RetentionDays   int  `json:"retentionDays"`
}

func defaultLearningConfig() LearningConfig {
	return LearningConfig{PatternAnalysis: true, RetentionDays: 365}
}

// PrefsConfig (config.ts 368-373).
type PrefsConfig struct {
	Enabled bool `json:"enabled"`
	Inject  bool `json:"inject"`
}

func defaultPrefsConfig() PrefsConfig { return PrefsConfig{Enabled: true, Inject: true} }

// WasteConfig (config.ts 351-366).
type WasteConfig struct {
	Enabled bool `json:"enabled"`
	Tip     bool `json:"tip"`
	Every   int  `json:"every"`
	Sleep   bool `json:"sleep"`
	Paging  bool `json:"paging"`
	Search  bool `json:"search"`
	Recheck bool `json:"recheck"`
}

func defaultWasteConfig() WasteConfig {
	return WasteConfig{Enabled: true, Tip: false, Every: 20, Sleep: true, Paging: true, Search: true, Recheck: true}
}

// CompactionConfig (config.ts 279-292): relevance compaction in place of the host's summary.
type CompactionConfig struct {
	Enabled          bool     `json:"enabled"`
	KeepThreshold    float64  `json:"keepThreshold"`
	MaxSummaryTokens int      `json:"maxSummaryTokens"`
	TimeoutMs        int      `json:"timeoutMs"`
	MaxRequests      int      `json:"maxRequests"`
	SkipProviders    []string `json:"skipProviders"`
}

func defaultCompactionConfig() CompactionConfig {
	return CompactionConfig{Enabled: false, KeepThreshold: 0.5, MaxSummaryTokens: 20000, TimeoutMs: 20000, MaxRequests: 12, SkipProviders: []string{"claude-bridge"}}
}
