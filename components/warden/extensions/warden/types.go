package warden

// Level is what the action guard decides for one call.
type Level string

const (
	LevelAllow   Level = "allow"
	LevelWarn    Level = "warn"
	LevelConfirm Level = "confirm"
	LevelDeny    Level = "deny"
)

var levelRank = map[Level]int{LevelAllow: 0, LevelWarn: 1, LevelConfirm: 2, LevelDeny: 3}

// Higher returns the stricter of two levels (guard.ts `higher`).
func Higher(a, b Level) Level {
	if levelRank[a] >= levelRank[b] {
		return a
	}
	return b
}

// Severity of a pattern hit.
type Severity string

const (
	SeverityDestructive Severity = "destructive"
	SeverityRisky       Severity = "risky"
	SeveritySensitive   Severity = "sensitive"
	SeverityDeny        Severity = "deny"
)

// PatternHit is one offline pattern that matched. Label never contains the matched text.
type PatternHit struct {
	ID       string
	Severity Severity
	Label    string
	// Action is "dialog" or "hold" for a user-defined confirm rule.
	Action  string
	Message string
}

// ActionSummary is the redacted, truncated view of a tool call. This is what leaves the machine.
type ActionSummary struct {
	Tool      string     `json:"tool"`
	Command   string     `json:"command,omitempty"`
	Path      string     `json:"path,omitempty"`
	Location  string     `json:"location,omitempty"` // inside_project | outside_project
	Exists    *bool      `json:"exists,omitempty"`
	Bytes     *int       `json:"bytes,omitempty"`
	Excerpt   string     `json:"excerpt,omitempty"`
	EditCount *int       `json:"editCount,omitempty"`
	Edits     []EditPair `json:"edits,omitempty"`
	Input     string     `json:"input,omitempty"`
	// DataText is present when part of the command is data (a heredoc body, a quoted message).
	DataText string `json:"dataText,omitempty"`
}

// EditPair is one edit of an `edit` call, redacted and clipped.
type EditPair struct {
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
}

// Judgment is the judge's answer for one action.
type Judgment struct {
	Irreversible    float64
	OffTask         float64
	Scope           string
	ScopeConfidence float64
	Approved        *float64
	Mutates         *float64
	IntentMismatch  *float64
	Visible         *float64
	ShouldProceed   *float64
	Model           string
	ElapsedMs       int
}

// Verdict is the action guard's decision.
type Verdict struct {
	Level    Level
	Source   string // skipped | read-only | pattern | typesafe | error
	Summary  ActionSummary
	Patterns []PatternHit
	Reasons  []string
	Judgment *Judgment
	// ApprovedByUser: a pending hold was released by the user's reply.
	ApprovedByUser bool
	Plan           string
	IntentMismatch bool
	// IntentTraceOnly: recorded in the trace, the agent is not told.
	IntentTraceOnly                   bool
	IntentTraceOnlyReasonIndex        int
	OffTaskSteer                      bool
	OffTaskTraceOnly                  bool
	OffTaskTraceOnlyReasonIndex       int
	ShouldProceedSteer                bool
	ShouldProceedTraceOnly            bool
	ShouldProceedTraceOnlyReasonIndex int
	Extra                             map[string]any
	Error                             string
	ErrorCode                         string
}

// TaskMessage is one earlier message given to the judge for scope context.
type TaskMessage struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

// ToolCallRef is one tool call as the agent proposed it.
type ToolCallRef struct {
	ID    string
	Tool  string
	Input map[string]any
}

// TaskSpine is the thread's goal and earlier user turns.
type TaskSpine struct {
	Goal    string
	Task    string
	History []string
}
