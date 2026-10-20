package powerline_footer

// Bash mode types. upstream: bash-mode/types.ts. The settings struct (BashModeSettings) is bashModeSettings in shortcut_config.go
// (parseBashModeSettings); the transcript store needs only its two limits, so it takes bashTranscriptLimits.

// bashTranscriptLimits is Pick<BashModeSettings, "transcriptMaxLines" | "transcriptMaxBytes">.
type bashTranscriptLimits struct {
	MaxLines int
	MaxBytes int
}

// bashCommandRecord is BashCommandRecord. ExitCode and FinishedAt are nil while the command runs (null upstream).
type bashCommandRecord struct {
	ID          string
	Command     string
	StartedAt   int64
	CwdAtStart  string
	Output      []string
	OutputBytes int
	ExitCode    *int
	FinishedAt  *int64
	Truncated   bool
}

// bashTranscriptSnapshot is BashTranscriptSnapshot.
type bashTranscriptSnapshot struct {
	Commands          []bashCommandRecord
	TotalLines        int
	TotalBytes        int
	TruncatedCommands int
}

// The sources of a ghost suggestion or completion item.
const (
	bashSourceProjectHistory = "project-history"
	bashSourceGlobalHistory  = "global-history"
	bashSourceGit            = "git"
	bashSourcePath           = "path"
	bashSourceExecutable     = "executable"
)

// ghostSuggestion is GhostSuggestion.
type ghostSuggestion struct {
	Value  string
	Source string
}

// extendedCompletionItem is ExtendedCompletionItem. Columns are byte offsets into the line it was computed for (the engine
// builds and applies items in one unit; the editor-facing applyExtendedCompletion counts UTF-16 units like pi-tui).
type extendedCompletionItem struct {
	Value       string
	Label       string
	Description string
	Replacement string
	StartCol    int
	EndCol      int
	Source      string
	Score       int
}

// shellSessionState is ShellSessionState. LastExitCode is nil until a command finished.
type shellSessionState struct {
	Ready        bool
	Running      bool
	ShellPath    string
	ShellName    string
	Cwd          string
	LastExitCode *int
}
