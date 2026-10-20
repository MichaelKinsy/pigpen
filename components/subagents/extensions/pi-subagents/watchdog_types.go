package pi_subagents

// The watchdog (src/watchdog/*): a reviewer that reads each turn of the main session (and of children) and warns about
// risks. Types of watchdog/types.ts. Optional fields of the original are the zero value here (an empty string is unset);
// the ones where the original tells undefined from false use a pointer.

var (
	watchdogWarningSeverities = []string{"concern", "blocker"}
	watchdogWarningCategories = []string{"correctness", "missed-constraint", "test-gap", "unsafe-change", "scope-drift", "stale-fact", "loop-risk", "other"}
	watchdogWarningImportance = []string{"low", "medium", "high"}
	watchdogRuntimeStatuses   = []string{"idle", "queued", "reviewing", "waiting-at-agent-end", "stale", "failed"}
)

func wdContains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// wdWarning is WatchdogWarning and WatchdogWarningDetails (the details are a warning whose Category and Source are set).
type wdWarning struct {
	Severity          string `json:"severity"`
	Importance        string `json:"importance"`
	Summary           string `json:"summary"`
	Evidence          string `json:"evidence"`
	RecommendedAction string `json:"recommendedAction"`
	Category          string `json:"category,omitempty"`
	Source            string `json:"source,omitempty"`
	Agent             string `json:"agent,omitempty"`
	RunID             string `json:"runId,omitempty"`
	Stale             *bool  `json:"stale,omitempty"`
	State             string `json:"state,omitempty"`
	Identity          string `json:"identity,omitempty"`
	DisplayedAt       string `json:"displayedAt,omitempty"`
	Error             string `json:"error,omitempty"`
	StalemateRepeats  *int   `json:"stalemateRepeats,omitempty"`
}

// watchdogWarningMessage is WatchdogWarningMessage.
type watchdogWarningMessage struct {
	CustomType string
	Content    string
	Display    bool
	Details    wdWarning
}

// wdThinking is `thinking?: string | false`: the zero value is unset.
type wdThinking struct {
	Level string
	Off   bool
}

func (t wdThinking) set() bool { return t.Off || t.Level != "" }

func wdThinkingLevel(level string) wdThinking { return wdThinking{Level: level} }

var wdThinkingOff = wdThinking{Off: true}

// wdCadence is Partial<WatchdogCadenceConfig>: EveryNTools nil is null (or absent).
type wdCadence struct{ EveryNTools *int }

type wdEndpoint struct {
	Enabled  bool
	Model    string
	Thinking wdThinking
}

type wdChildOverride struct {
	Enabled  *bool
	Model    string
	Thinking wdThinking
	Cadence  *wdCadence
}

type wdOverrideEntry struct {
	Agent    string
	Override wdChildOverride
}

type wdChildren struct {
	Enabled               bool
	Model                 string
	Thinking              wdThinking
	WatchdogTailTimeoutMs int
	Cadence               *wdCadence
	// Overrides keeps the order a JavaScript object iterates its keys in.
	Overrides []wdOverrideEntry
}

func (c wdChildren) override(agent string) *wdChildOverride {
	for i := range c.Overrides {
		if c.Overrides[i].Agent == agent {
			return &c.Overrides[i].Override
		}
	}
	return nil
}

type wdLsp struct {
	Enabled        bool
	TimeoutMs      int
	MaxFiles       int
	MaxDiagnostics int
}

type wdRoleModelRule struct {
	// Allow and Deny are nil when the rule does not set them.
	Allow []string
	Deny  []string
	Note  string
}

type wdRoleModelEntry struct {
	Agent string
	Rule  wdRoleModelRule
}

type wdRules struct {
	Action     string
	RoleModels []wdRoleModelEntry
}

func (r *wdRules) roleRule(agent string) *wdRoleModelRule {
	if r == nil {
		return nil
	}
	for i := range r.RoleModels {
		if r.RoleModels[i].Agent == agent {
			return &r.RoleModels[i].Rule
		}
	}
	return nil
}

// watchdogConfig is ResolvedWatchdogConfig.
type watchdogConfig struct {
	Enabled            bool
	Clarification      bool
	AgentEndTimeoutMs  int
	SeverityThreshold  string
	MaxWarnings        *int
	GuidanceWatchdogMd bool
	StalemateRepeats   int
	ScopeEnabled       bool
	Cadence            wdCadence
	Main               wdEndpoint
	Children           wdChildren
	Lsp                wdLsp
	Rules              *wdRules
}

type wdSettingsError struct {
	Scope   string
	Path    string
	Message string
}

type wdSettingsSource struct {
	Scope  string
	Path   string
	Exists bool
}

// watchdogSettingsResult is WatchdogSettingsResult.
type watchdogSettingsResult struct {
	OK      bool
	Config  *watchdogConfig
	Errors  []wdSettingsError
	Sources []wdSettingsSource
}

// LSP results (the typescript-language-server pre-pass of a boundary review).
type wdLspDiagnostic struct {
	Path     string
	Line     int
	Column   int
	Severity string
	Source   string
	Code     string
	Message  string
}

type wdLspResult struct {
	Status       string
	Provider     string
	CheckedPaths []string
	SkippedPaths []string
	Diagnostics  []wdLspDiagnostic
	Message      string
}

type wdLspSnapshot struct {
	wdLspResult
	Enabled              bool
	DiagnosticCount      int
	FreshDiagnosticCount int
	UpdatedAt            string
}
