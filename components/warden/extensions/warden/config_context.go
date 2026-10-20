package warden

// RecallTool names the search command in the recall footer (config.ts 342-347).
type RecallTool string

// RecallTools are the accepted values.
var RecallTools = []RecallTool{"auto", "rg", "ag", "ugrep", "git-grep", "grep", "select-string", "findstr", "none"}

// IsRecallTool reports whether v is a RecallTool value.
func IsRecallTool(v string) bool {
	for _, t := range RecallTools {
		if string(t) == v {
			return true
		}
	}
	return false
}

// LargeOutputConfig (config.ts 294-298).
type LargeOutputConfig struct {
	Enabled   bool    `json:"enabled"`
	Threshold float64 `json:"threshold"`
}

// FilterConfig (config.ts 266-276).
type FilterConfig struct {
	Enabled      bool    `json:"enabled"`
	ChunkChars   int     `json:"chunkChars"`
	MinScore     float64 `json:"minScore"`
	MaxKeptChars int     `json:"maxKeptChars"`
	TimeoutMs    int     `json:"timeoutMs"`
}

// ContextConfig (config.ts 242-264).
type ContextConfig struct {
	Enabled           bool              `json:"enabled"`
	TailMinChars      int               `json:"tailMinChars"`
	Confidence        float64           `json:"confidence"`
	DuplicateMinChars int               `json:"duplicateMinChars"`
	RecallTool        RecallTool        `json:"recallTool"`
	FormatConfidence  float64           `json:"formatConfidence"`
	CompactAppendix   bool              `json:"compactAppendix"`
	DedupeRuns        bool              `json:"dedupeRuns"`
	DedupeMessages    bool              `json:"dedupeMessages"`
	LargeOutput       LargeOutputConfig `json:"largeOutput"`
	Filter            FilterConfig      `json:"filter"`
}

func defaultContextConfig() ContextConfig {
	return ContextConfig{Enabled: true, TailMinChars: 12000, Confidence: 0.8, DuplicateMinChars: 2000, RecallTool: "auto",
		FormatConfidence: 0.7, CompactAppendix: true, DedupeRuns: true, DedupeMessages: false,
		LargeOutput: LargeOutputConfig{Enabled: true, Threshold: 0.85},
		Filter:      FilterConfig{Enabled: false, ChunkChars: 2000, MinScore: 1.5, MaxKeptChars: 6000, TimeoutMs: 4000}}
}
