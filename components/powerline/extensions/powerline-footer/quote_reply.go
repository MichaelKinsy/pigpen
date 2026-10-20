package powerline_footer

import "time"

// Red stub: signatures of the quote-reply port, no behaviour yet. upstream: quote-reply.ts.

type quoteCandidate struct {
	ID          string
	Role        string
	Text        string
	TimestampMs int64
	SearchText  string
}

type quoteReplyContext interface {
	Mode() string
	Branch() ([]map[string]any, error)
	GetEditorText() (string, error)
	SetEditorText(text string)
	Notify(message, level string)
	Select(title string, options []string) (value string, ok bool, err error)
	CanCustom() bool
	Pick(candidates []quoteCandidate) (id string, err error)
	Now() time.Time
}

func reply(args string, ctx quoteReplyContext) error { return nil }

func collectCandidates(ctx quoteReplyContext) ([]quoteCandidate, error) { return nil, nil }

func formatQuote(candidate quoteCandidate) string { return "" }

func excerpt(text string, maxChars int) string { return "" }
