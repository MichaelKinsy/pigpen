package websearch

import (
	"context"
	"time"
)

// Port of the summary generation of summary-review.ts. Stub: no summary is generated yet.

const SummaryGenerationDefaultDeadlineMs = 0

var preferredSummaryModels []modelCandidate

// SummaryMeta describes how a summary was produced.
type SummaryMeta struct {
	Model          *string `json:"model"`
	DurationMs     int64   `json:"durationMs"`
	TokenEstimate  int     `json:"tokenEstimate"`
	FallbackUsed   bool    `json:"fallbackUsed"`
	FallbackReason string  `json:"fallbackReason,omitempty"`
	Phase          string  `json:"phase,omitempty"`
	Edited         bool    `json:"edited"`
}

// SummaryDraft is a summary and its metadata.
type SummaryDraft struct {
	Summary string
	Meta    SummaryMeta
}

// SummaryOptions are the optional arguments of GenerateSummaryDraft.
type SummaryOptions struct {
	ModelOverride string
	Feedback      string
	Complete      CompleteFunc
	DeadlineMs    int
}

var summaryClock = time.Now

func GetSummaryGenerationDeadlineMs() int { return 0 }

func GenerateSummaryDraft(_ context.Context, _ []QueryResultData, _ SideCallContext, _ SummaryOptions) (SummaryDraft, error) {
	return SummaryDraft{}, errAnswerNotPorted
}

func BuildSummaryPrompt(_ []QueryResultData, _ string) string { return "" }

func BuildDeterministicSummary(_ []QueryResultData) SummaryDraft { return SummaryDraft{} }

func clampThinkingLevel(_ map[string]any, _ string) string { return "" }
