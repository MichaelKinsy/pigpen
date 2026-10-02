package warden

// Config mirrors the parts of pi-warden's WardenConfig (src/config.ts) this port carries:
// the action, stuck and done guards and the steer settings. Defaults are defaultConfig().

// Threshold is a warn/confirm pair.
type Threshold struct {
	Warn    float64 `json:"warn"`
	Confirm float64 `json:"confirm"`
}

// OffTaskThreshold: warn, and steer the agent back to the request.
type OffTaskThreshold struct {
	Warn  float64 `json:"warn"`
	Steer float64 `json:"steer"`
}

// CommandRule is a user-declared command rule (user file only).
type CommandRule struct {
	ID            string `json:"id"`
	Pattern       string `json:"pattern"`
	Severity      string `json:"severity"` // warn | confirm | deny
	Action        string `json:"action,omitempty"`
	Message       string `json:"message,omitempty"`
	CaseSensitive bool   `json:"caseSensitive,omitempty"`
}

// ShouldProceed: low P(should_proceed) is trace-only unless Steer is set; never holds.
type ShouldProceed struct {
	Threshold float64 `json:"threshold"`
	Steer     bool    `json:"steer"`
}

// ActionConfig is the action guard's configuration.
type ActionConfig struct {
	Enabled         bool             `json:"enabled"`
	Tools           []string         `json:"tools"`
	FailOpen        bool             `json:"failOpen"`
	TimeoutMs       int              `json:"timeoutMs"`
	Irreversible    Threshold        `json:"irreversible"`
	OffTask         OffTaskThreshold `json:"offTask"`
	IntentMismatch  float64          `json:"intentMismatch"`
	VisibleMismatch float64          `json:"visibleMismatch"`
	// IntentTraceOnly: "all" (default), "invisible" or "none".
	IntentTraceOnly     string        `json:"intentTraceOnly"`
	ShouldProceed       ShouldProceed `json:"shouldProceed"`
	CommandRules        []CommandRule `json:"commandRules"`
	CommandDenyRules    []CommandRule `json:"commandDenyRules"`
	ExemptRules         []string      `json:"exemptRules"`
	EscalationThreshold float64       `json:"escalationThreshold"`
	// Floor: "evidence" (default) or "level".
	Floor string `json:"floor"`
}

// StuckConfig is the stuck guard's configuration.
type StuckConfig struct {
	Enabled        bool    `json:"enabled"`
	Window         int     `json:"window"`
	MinFailures    int     `json:"minFailures"`
	Cooldown       int     `json:"cooldown"`
	SameStrategy   float64 `json:"sameStrategy"`
	ChurnThreshold int     `json:"churnThreshold"`
	Nudge          bool    `json:"nudge"`
	RepeatSteer    bool    `json:"repeatSteer"`
	// Evidence is off in this port (the documented `stuck.evidence: false` state): see PORT.md.
	Evidence  bool `json:"evidence"`
	DiffLimit int  `json:"diffLimit"`
	TailLimit int  `json:"tailLimit"`
}

// VisualTools are the tool calls that show the rendered UI (matched case-insensitively).
type VisualTools struct {
	Commands     []string `json:"commands"`
	CommandWords []string `json:"commandWords"`
	Tools        []string `json:"tools"`
	Images       []string `json:"images"`
}

// DoneConfig is the done-check's configuration.
type DoneConfig struct {
	Enabled     bool        `json:"enabled"`
	ClaimsDone  float64     `json:"claimsDone"`
	Nudge       bool        `json:"nudge"`
	UIProof     bool        `json:"uiProof"`
	UIFiles     []string    `json:"uiFiles"`
	VisualTools VisualTools `json:"visualTools"`
}

// Config is the whole configuration.
type Config struct {
	// Enabled is the master switch. Unlike pi-warden it is off until /warden enable.
	Enabled bool `json:"enabled"`
	// Consent is the backend the user agreed to send redacted call summaries to ("" = none; offline patterns
	// need no consent). A different Backend without a matching Consent is not used until /warden enable.
	Consent string `json:"consent"`
	// Backend names where judgments go: "" (none: offline patterns only), "typesafe" or "own-model".
	Backend     string       `json:"backend"`
	Mode        string       `json:"mode"` // steer | confirm | advise
	TimeoutMs   int          `json:"timeoutMs"`
	MaxRequests int          `json:"maxRequests"`
	Action      ActionConfig `json:"action"`
	Stuck       StuckConfig  `json:"stuck"`
	Done        DoneConfig   `json:"done"`
	// SteerVisible shows steers in the transcript; Notices notify the user of each finding.
	SteerVisible bool `json:"steerVisible"`
	Notices      bool `json:"notices"`
	SteerBudget  int  `json:"steerBudget"`
}

// DefaultUIFiles: files whose change shows on screen (`{a,b}` alternatives, `!` excludes).
var DefaultUIFiles = []string{
	"**/*.{css,scss,sass,less,html,htm,vue,svelte,jsx,tsx,astro,dart}", "**/web/**/*.js", "**/public/**/*.js",
	"!**/*.{test,spec}.*", "!**/*_test.dart", "!**/{test,tests,__tests__}/**",
}

// DefaultVisualTools returns the default visual-check heads (config.ts defaultVisualTools).
func DefaultVisualTools() VisualTools {
	return VisualTools{
		Commands:     []string{"agent-browser", "playwright", "npx playwright", "flutter test", "fvm flutter test", "idb", "xcrun simctl io", "chrome", "chromium", "google-chrome"},
		CommandWords: []string{"screenshot"},
		Tools:        []string{"screenshot", "take_snapshot", "navigate"},
		Images:       []string{"png", "jpg", "jpeg", "webp"},
	}
}

// CommandTools carry commands and are guarded by default (tools.ts COMMAND_TOOLS).
var CommandTools = []string{"bash", "powershell", "ctx_execute", "ctx_batch_execute", "ctx_execute_file"}

// DefaultConfig returns pi-warden's defaults for the carried sections (config.ts defaultConfig),
// except: Enabled is false (the port is opt-in), steers and notices are shown (SteerVisible,
// Notices), and stuck evidence is off.
func DefaultConfig() Config {
	return Config{
		Enabled:     false,
		Backend:     "",
		Mode:        "steer",
		TimeoutMs:   5000,
		MaxRequests: 500,
		Action: ActionConfig{
			Enabled:             true,
			Tools:               append(append([]string{}, CommandTools...), "write", "edit"),
			FailOpen:            true,
			TimeoutMs:           5000,
			Irreversible:        Threshold{Warn: 0.5, Confirm: 0.9},
			OffTask:             OffTaskThreshold{Warn: 0.6, Steer: 0.85},
			IntentMismatch:      0.9,
			VisibleMismatch:     0.8,
			IntentTraceOnly:     "all",
			ShouldProceed:       ShouldProceed{Threshold: 0.6, Steer: false},
			EscalationThreshold: 0.85,
			Floor:               "evidence",
		},
		Stuck:        StuckConfig{Enabled: true, Window: 12, MinFailures: 3, Cooldown: 3, SameStrategy: 0.7, ChurnThreshold: 5, Nudge: true, RepeatSteer: true, Evidence: false, DiffLimit: 3000, TailLimit: 1000},
		Done:         DoneConfig{Enabled: true, ClaimsDone: 0.7, Nudge: true, UIProof: true, UIFiles: append([]string{}, DefaultUIFiles...), VisualTools: DefaultVisualTools()},
		SteerVisible: true,
		Notices:      true,
		SteerBudget:  3,
	}
}
