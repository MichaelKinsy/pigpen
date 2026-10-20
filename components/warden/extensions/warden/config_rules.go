package warden

// PathRule is a user-defined path rule (config.ts 80-97): which paths, which side of the access is held, which tools,
// what happens on a hit.
type PathRule struct {
	ID    string   `json:"id"`
	Paths []string `json:"paths"`
	Regex bool     `json:"regex,omitempty"`
	// Access: none | read | write.
	Access string   `json:"access"`
	Tools  []string `json:"tools"`
	// Action: note | warn | confirm | block (alias deny).
	Action  string `json:"action"`
	Message string `json:"message,omitempty"`
	// OnlyIfExists defaults to true when nil.
	OnlyIfExists *bool `json:"onlyIfExists,omitempty"`
}

// ArmingWhen is the preparation of an arming rule (config.ts 107-114).
type ArmingWhen struct {
	Edited []string `json:"edited"`
	Regex  bool     `json:"regex,omitempty"`
	Tools  []string `json:"tools,omitempty"`
}

// ArmingArms is the armed command (config.ts 116-123). For is a string ("10m") or a number; nil is "10m".
type ArmingArms struct {
	Command       string `json:"command"`
	For           any    `json:"for,omitempty"`
	CaseSensitive bool   `json:"caseSensitive,omitempty"`
}

// ArmingRule: editing files matching When arms a command pattern for a window (config.ts 103-128).
type ArmingRule struct {
	ID   string     `json:"id"`
	When ArmingWhen `json:"when"`
	Arms ArmingArms `json:"arms"`
	// Action: confirm | hold | block (alias deny).
	Action  string `json:"action"`
	Message string `json:"message,omitempty"`
}

// RulesConfig is the project-rules guard's configuration (config.ts 221-240).
type RulesConfig struct {
	Enabled        bool              `json:"enabled"`
	Threshold      float64           `json:"threshold"`
	SoftThreshold  float64           `json:"softThreshold"`
	Files          []string          `json:"files"`
	Fallback       bool              `json:"fallback"`
	MaxChars       int               `json:"maxChars"`
	Exclude        []string          `json:"exclude"`
	Skip           []string          `json:"skip"`
	SensitivePaths map[string]string `json:"sensitivePaths"`
}

func defaultRulesConfig() RulesConfig {
	return RulesConfig{Enabled: true, Threshold: 0.7, SoftThreshold: 0, Files: []string{}, Fallback: true, MaxChars: 8000,
		Exclude: []string{}, Skip: []string{}, SensitivePaths: map[string]string{}}
}
