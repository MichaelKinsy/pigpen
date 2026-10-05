package powerline_footer

// colorScheme maps a semantic color name (model, path, gitDirty, ...) to a color value: a theme token, "#rrggbb" or "rainbow".
type colorScheme map[string]string

func ptr[T any](v T) *T { return &v }

// Segment options. A nil field is unset; merging overrides field by field (mergeSegmentOptions).
type modelOptions struct {
	ShowThinkingLevel *bool
	Display           *string
}
type pathOptions struct {
	Mode      *string
	MaxLength *int
}
type gitOptions struct {
	ShowBranch, ShowStaged, ShowUnstaged, ShowUntracked *bool
	Polling                                             *string
	HostIcon                                            *bool
}
type timeOptions struct {
	Format      *string
	ShowSeconds *bool
}
type costOptions struct {
	SubscriptionDisplay *string
	Currency            *string
}
type contextOptions struct{ Format *string }
type cacheReadOptions struct{ Format *string }

// segmentOptions holds one pointer per segment: nil is the original's undefined (no options given for it).
type segmentOptions struct {
	Model     *modelOptions
	Path      *pathOptions
	Git       *gitOptions
	Time      *timeOptions
	Cost      *costOptions
	Context   *contextOptions
	CacheRead *cacheReadOptions
}

type presetDef struct {
	left, right, secondary []string
	separator              string
	colors                 colorScheme
	options                segmentOptions
}

// customItem is a status another extension sets, promoted to its own segment (powerline.customItems).
type customItem struct {
	ID, StatusKey, Position string
	Color                   string // "" when unset
	SelfColorize            bool
	Prefix                  string // "" when unset
	HideWhenMissing         bool
	ExcludeFromStatuses     bool
}

// statusLayout holds the rows a user placed explicitly: a nil row is not configured, an empty one is configured empty.
type statusLayout struct {
	Left, Right, Secondary []string
}

type gitStatus struct {
	Branch                      *string
	Staged, Unstaged, Untracked int
}

type usageStats struct {
	Input, Output, CacheRead, CacheWrite, Cost, SubagentCost float64
}

type queueSummary struct {
	QueueCount, BlockedCount int
	Compacting               bool
}

type modelInfo struct {
	ID, Name, Provider, ProviderID, ProviderName string
	Reasoning                                    bool
	ContextWindow                                float64
}

// theme colors text with a Pi theme token. fg may fail on a token the theme does not know, as Pi's Theme.fg does.
type theme interface {
	Fg(token, text string) (string, error)
}

// segmentContext is what the segments read (the original's SegmentContext).
type segmentContext struct {
	Model                   *modelInfo
	ThinkingLevel           string
	SessionID, SessionName  string
	CWD                     string
	Usage                   usageStats
	ContextTokens           *float64
	ContextPercent          *float64
	ContextWindow           float64
	ContextApproximate      bool
	AutoCompactEnabled      bool
	CustomCompactionEnabled bool
	UsingSubscription       bool
	Queue                   queueSummary
	SessionStart            int64 // milliseconds
	Git                     gitStatus
	ExtensionStatuses       []statusEntry
	HiddenStatusKeys        map[string]bool
	CustomItems             map[string]customItem
	Options                 segmentOptions
	Theme                   theme
	Colors                  colorScheme
}

// statusEntry is one extension status in the host's insertion order.
type statusEntry struct{ Key, Value string }

type renderedSegment struct {
	Content string
	Visible bool
}
