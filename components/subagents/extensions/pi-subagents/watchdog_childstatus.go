package pi_subagents


// Child watchdog configuration and status (watchdog/child-status.ts): the config a child's watchdog runs with, and the status events
// it reports to the run that started it.

const (
	childWatchdogWarningLimit    = 20
	childWatchdogStatusEventType = "subagent.watchdog.status"
)

var childWatchdogPhases = []string{"idle", "reviewing", "stale", "failed"}

type childWatchdogConfig struct {
	RunID                 string
	Agent                 string
	ChildIndex            *int
	WatchdogTailTimeoutMs int
	AgentEndTimeoutMs     int
	MaxWarnings           *int
	Model                 string
	Thinking              wdThinking
	Lsp                   wdLsp
	StalemateRepeats      int
	// Cadence: EveryNTools nil means boundary reviews only.
	Cadence wdCadence
}

// childWatchdogWarningSummary is ChildWatchdogWarningSummary.
type childWatchdogWarningSummary struct {
	Severity          string `json:"severity"`
	Importance        string `json:"importance"`
	Category          string `json:"category"`
	Summary           string `json:"summary"`
	Evidence          string `json:"evidence"`
	RecommendedAction string `json:"recommendedAction"`
	DisplayedAt       string `json:"displayedAt,omitempty"`
	// Addressed is true when a later assistant turn in the child followed the warning.
	Addressed bool `json:"addressed"`
	Stalemate bool `json:"stalemate"`
}

// childWatchdogProgress is ChildWatchdogProgress.
type childWatchdogProgress struct {
	Phase      string                        `json:"phase"`
	Seq        int                           `json:"seq"`
	LastUpdate float64                       `json:"lastUpdate"`
	Reason     string                        `json:"reason,omitempty"`
	TimedOut   bool                          `json:"timedOut,omitempty"`
	Warnings   []childWatchdogWarningSummary `json:"warnings,omitempty"`
}

type childWatchdogStatusEvent struct {
	Type       string
	RunID      string
	Agent      string
	ChildIndex *int
	StepIndex  *int
	Seq        int
	Phase      string
	Ts         float64
	Reason     string
	Warning    *childWatchdogWarningSummary
}

type resolveChildWatchdogInput struct {
	Config     *watchdogConfig
	Agent      string
	RunID      string
	ChildIndex *int
}

// resolveChildWatchdogConfig is the watchdog a child launches with, nil when none: the override of its agent, then the children
// settings, then the top level.
func resolveChildWatchdogConfig(in resolveChildWatchdogInput) *childWatchdogConfig {
	panic("not implemented")
}

// toObject is the config as the JSON object the child reads (the original's JSON.stringify of ChildWatchdogConfig).
func (c childWatchdogConfig) toObject() *jsObject { panic("not implemented") }

// JSON is the config as the child reads it.
func (c childWatchdogConfig) JSON() string { panic("not implemented") }

func wdChildErr(msg string) error { panic("not implemented") }

func wdChildObject(v any, field string) (*jsObject, error) { panic("not implemented") }

func wdChildOptionalString(in *jsObject, field string) (string, error) { panic("not implemented") }

func wdChildInt(v any, min float64) (int, bool) { panic("not implemented") }

func wdChildPositiveInt(in *jsObject, field string) (int, error) { panic("not implemented") }

func wdChildCadence(v any) (wdCadence, error) { panic("not implemented") }

func wdChildLsp(v any) (wdLsp, error) { panic("not implemented") }

// decodeChildWatchdogConfig reads the config a parent passed to a child: nil for none or a disabled one, an error for a malformed one.
func decodeChildWatchdogConfig(raw string) (*childWatchdogConfig, error) { panic("not implemented") }

// decodeChildWatchdogStatusEvent reads a status event a child wrote; ok is false for anything that is not one.
func decodeChildWatchdogStatusEvent(v any) (childWatchdogStatusEvent, bool) { panic("not implemented") }

func isChildWatchdogStatusEvent(v any) bool { panic("not implemented") }

func childWatchdogIsActive(p *childWatchdogProgress) bool { panic("not implemented") }

type acceptChildWatchdogInput struct {
	Current    *childWatchdogProgress
	Event      childWatchdogStatusEvent
	RunID      string
	Agent      string
	ChildIndex *int
}

// acceptChildWatchdogEvent folds an event into the progress; nil when the event is foreign or not newer than the progress.
func acceptChildWatchdogEvent(in acceptChildWatchdogInput) *childWatchdogProgress {
	panic("not implemented")
}

// applyChildWatchdogMessage marks earlier warnings addressed on an assistant turn; nil when nothing changes.
func applyChildWatchdogMessage(current *childWatchdogProgress, message any) *childWatchdogProgress {
	panic("not implemented")
}

// childWatchdogBlockers are the blocker warnings that still stand: unaddressed, or held by a stalemate.
func childWatchdogBlockers(p *childWatchdogProgress) []childWatchdogWarningSummary {
	panic("not implemented")
}

// childWatchdogProgressForModel is what the parent model sees: only the high-importance findings.
func childWatchdogProgressForModel(p *childWatchdogProgress) *childWatchdogProgress {
	panic("not implemented")
}
