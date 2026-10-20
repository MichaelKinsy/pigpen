package powerline_footer

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Quote reply: /reply [id] and the reply shortcut action. upstream: quote-reply.ts.

const (
	maxQuoteChars       = 6_000
	maxExcerptChars     = 220
	maxRPCOptions       = 80
	maxCandidates       = 200
	maxSearchTextChars  = 1_000
	quoteSelectTitle    = "Reply to previous message"
	noQuotableMessages  = "No user or assistant messages are available to quote."
	quoteCandidateIDErr = "Session entry id must not be empty."
)

type quoteCandidate struct {
	ID          string
	Role        string // "assistant" or "user"
	Text        string
	TimestampMs int64
	SearchText  string
}

// quoteReplyContext is the part of the extension context reply() reads and writes (QuoteReplyContext in quote-reply.ts).
type quoteReplyContext interface {
	// Mode is ctx.mode.
	Mode() string
	// Branch is ctx.sessionManager.getBranch(): the entries of the current branch, oldest first.
	Branch() ([]map[string]any, error)
	GetEditorText() (string, error)
	SetEditorText(text string)
	Notify(message, level string)
	// Select is ctx.ui.select: ok is false when the dialog was dismissed.
	Select(title string, options []string) (value string, ok bool, err error)
	// CanCustom is whether ctx.ui.custom exists; Pick runs the picker overlay and returns the chosen candidate's id ("" when cancelled).
	CanCustom() bool
	Pick(candidates []quoteCandidate) (id string, err error)
	Now() time.Time
}

func jsRecord(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

// contentText: a string content is itself; an array contributes its text blocks joined by a blank line. upstream: contentText.
func contentText(content any) string {
	if s, ok := content.(string); ok {
		return s
	}
	blocks, ok := content.([]any)
	if !ok {
		return ""
	}
	var texts []string
	for _, block := range blocks {
		if rec, ok := jsRecord(block); ok && rec["type"] == "text" {
			if text, ok := rec["text"].(string); ok {
				texts = append(texts, text)
			}
		}
	}
	return strings.Join(texts, "\n\n")
}

// jsDateParse parses the date formats a session entry timestamp has (ISO 8601): a date-only form is UTC, a date-time without an
// offset is local time, as Date.parse reads them; ok is false for NaN.
func jsDateParse(value string) (int64, bool) {
	for _, f := range []struct {
		layout string
		local  bool
	}{
		{"2006-01-02T15:04:05.999999999Z07:00", false},
		{"2006-01-02T15:04:05Z07:00", false},
		{"2006-01-02T15:04Z07:00", false},
		{"2006-01-02T15:04:05.999999999", true},
		{"2006-01-02T15:04:05", true},
		{"2006-01-02T15:04", true},
		{"2006-01-02", false},
	} {
		loc := time.UTC
		if f.local {
			loc = time.Local
		}
		if t, err := time.ParseInLocation(f.layout, value, loc); err == nil {
			return t.UnixMilli(), true
		}
	}
	return 0, false
}

func quoteTimestampMs(entry map[string]any, fallback any, now time.Time) int64 {
	if f, ok := fallback.(float64); ok && f == f && f-f == 0 {
		return int64(f)
	}
	if ts, ok := entry["timestamp"].(string); ok {
		if ms, ok := jsDateParse(ts); ok {
			return ms
		}
	}
	return now.UnixMilli()
}

func buildCandidate(entry map[string]any, now time.Time) (*quoteCandidate, error) {
	if entry["type"] != "message" {
		return nil, nil
	}
	message, ok := jsRecord(entry["message"])
	if !ok {
		return nil, nil
	}
	role, _ := message["role"].(string)
	if role != "assistant" && role != "user" {
		return nil, nil
	}

	rawID, ok := entry["id"].(string)
	if !ok {
		return nil, errors.New("Cannot read properties of undefined (reading 'trim')")
	}
	id := jsTrim(rawID)
	if id == "" {
		return nil, errors.New(quoteCandidateIDErr)
	}

	text := jsTrim(contentText(message["content"]))
	if text == "" {
		return nil, nil
	}

	return &quoteCandidate{
		ID:          id,
		Role:        role,
		Text:        text,
		TimestampMs: quoteTimestampMs(entry, message["timestamp"], now),
		SearchText:  strings.ToLower(utf16Prefix(id+" "+role+" "+text, maxSearchTextChars)),
	}, nil
}

// collectCandidates walks the branch newest first and keeps at most 200 messages. upstream: collectCandidates.
func collectCandidates(ctx quoteReplyContext) ([]quoteCandidate, error) {
	branch, err := ctx.Branch()
	if err != nil {
		return nil, err
	}
	var candidates []quoteCandidate
	for index := len(branch) - 1; index >= 0 && len(candidates) < maxCandidates; index-- {
		candidate, err := buildCandidate(branch[index], ctx.Now())
		if err != nil {
			return nil, err
		}
		if candidate != nil {
			candidates = append(candidates, *candidate)
		}
	}
	return candidates, nil
}

// excerpt collapses whitespace and cuts to maxChars UTF-16 units, ending with an ellipsis. upstream: excerpt.
func excerpt(text string, maxChars int) string {
	compact := collapseSpaces(text)
	if utf16Len(compact) <= maxChars {
		return compact
	}
	return strings.TrimRightFunc(utf16Prefix(compact, max(0, maxChars-1)), isJSSpace) + "…"
}

// formatThousands is Number.prototype.toLocaleString() in en-US.
func formatThousands(n int) string {
	s := fmt.Sprint(n)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// formatQuote is the text inserted before the draft. upstream: formatQuote.
func formatQuote(candidate quoteCandidate) string {
	textLen := utf16Len(candidate.Text)
	truncated := textLen > maxQuoteChars
	quoteText := candidate.Text
	if truncated {
		quoteText = strings.TrimRightFunc(utf16Prefix(candidate.Text, maxQuoteChars), isJSSpace)
	}
	var quoted []string
	for _, line := range strings.Split(strings.ReplaceAll(quoteText, "\r\n", "\n"), "\n") {
		if line != "" {
			quoted = append(quoted, "> "+line)
		} else {
			quoted = append(quoted, ">")
		}
	}
	lines := []string{
		"Quoted previous " + candidate.Role + " message " + candidate.ID + " for reference only:",
		"",
		strings.Join(quoted, "\n"),
	}
	if truncated {
		lines = append(lines, "", "> [Quote truncated from "+formatThousands(textLen)+" characters.]")
	}
	lines = append(lines, "", "My reply:", "")
	return strings.Join(lines, "\n")
}

func insertQuote(ctx quoteReplyContext, candidate quoteCandidate) error {
	draft, err := ctx.GetEditorText()
	if err != nil {
		return err
	}
	ctx.SetEditorText(formatQuote(candidate) + draft)
	ctx.Notify("Inserted quote from "+candidate.Role+" message "+candidate.ID+".", "info")
	return nil
}

// pickCandidate asks the user: a picker overlay in the TUI, a select dialog elsewhere. upstream: pickCandidate.
func pickCandidate(ctx quoteReplyContext, candidates []quoteCandidate) (*quoteCandidate, error) {
	if ctx.Mode() != "tui" || !ctx.CanCustom() {
		shown := candidates
		if len(shown) > maxRPCOptions {
			shown = shown[:maxRPCOptions]
		}
		options := make([]string, len(shown))
		for i, c := range shown {
			options[i] = c.ID + " " + c.Role + ": " + excerpt(c.Text, maxExcerptChars)
		}
		selected, ok, err := ctx.Select(quoteSelectTitle, options)
		if err != nil {
			return nil, err
		}
		if !ok || selected == "" {
			return nil, nil
		}
		id, _, _ := strings.Cut(selected, " ")
		return findCandidate(candidates, id), nil
	}
	id, err := ctx.Pick(candidates)
	if err != nil {
		return nil, err
	}
	return findCandidate(candidates, id), nil
}

func findCandidate(candidates []quoteCandidate, id string) *quoteCandidate {
	if id == "" {
		return nil
	}
	for i := range candidates {
		if candidates[i].ID == id {
			return &candidates[i]
		}
	}
	return nil
}

// reply is `reply(args, ctx)`: the /reply command and the reply shortcut action (which passes empty args). upstream: reply.
func reply(args string, ctx quoteReplyContext) error {
	candidates, err := collectCandidates(ctx)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		ctx.Notify(noQuotableMessages, "warning")
		return nil
	}

	query := strings.ToLower(strings.TrimPrefix(jsTrim(args), "#"))
	if query != "" {
		var matches []quoteCandidate
		for _, c := range candidates {
			if strings.HasPrefix(strings.ToLower(c.ID), query) {
				matches = append(matches, c)
			}
		}
		if len(matches) == 1 {
			return insertQuote(ctx, matches[0])
		}
		if len(matches) > 1 {
			ctx.Notify(fmt.Sprintf("Message id \"%s\" matches %d messages. Type more of the id.", query, len(matches)), "warning")
		} else {
			ctx.Notify("No quotable message matches id \""+query+"\".", "warning")
		}
		return nil
	}

	candidate, err := pickCandidate(ctx, candidates)
	if err != nil {
		return err
	}
	if candidate != nil {
		return insertQuote(ctx, *candidate)
	}
	return nil
}
