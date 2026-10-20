package powerline_footer

import (
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Red stub: the reply picker. upstream: quote-reply.ts QuotePicker.

type pickerTheme interface {
	Fg(token, text string) string
	Bold(text string) string
	Italic(text string) string
}

type fuzzyResult struct {
	matches bool
	score   float64
}

func fuzzyMatch(query, text string) fuzzyResult { return fuzzyResult{} }

func fuzzyFilter[T any](items []T, query string, getText func(T) string) []T { return nil }

func formatTimestamp(timestamp int64, now time.Time) string { return "" }

type quotePicker struct{ query string }

func newQuotePicker(candidates []quoteCandidate, theme pickerTheme, now func() time.Time, onSelect func(quoteCandidate), onCancel func()) *quotePicker {
	return &quotePicker{}
}

func (p *quotePicker) Render(width int) []string { return nil }

func (p *quotePicker) HandleInput(data string) (selected *quoteCandidate, cancelled bool) {
	return nil, false
}

type quotePickerComponent struct{ picker *quotePicker }

func (c *quotePickerComponent) Render(width int) []string { return nil }

func (c *quotePickerComponent) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	return sdk.RemoteComponentResult{}, nil
}
