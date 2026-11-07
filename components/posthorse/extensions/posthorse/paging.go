package posthorse

// Port of src/paging.ts: page-size reservations shared by the note and history tools.

import (
	"errors"
	"strconv"
	"sync"
)

// pageRequest is the TS PageRequest (paging.ts:13-18). cursor "" is undefined (the original
// treats both alike, paging.ts:73); imageOffset nil is undefined.
type pageRequest struct {
	offset      int
	imageCount  int
	cursor      string
	imageOffset *int
}

// notesPage is the TS NotesPage (paging.ts:19-27). kind is "notes-list" or "notes-search".
type notesPage struct {
	rows   []string
	offset int
	kind   string
	empty  string
	header string
	// more: rows stop before the last match, so their joined length is only a lower bound.
	more bool
}

// pageAccess is the TS PageAccess (paging.ts:28-37). error is TS `never`: the returned error is
// always non-nil and the caller returns it.
type pageAccess interface {
	size(ctx policyContext, request pageRequest) (int, error)
	result(text string, images []imageLike, display *posthorseDisplay) toolResult
	error(ctx policyContext, message string, cursor string) error
	notes(ctx policyContext, page notesPage) (toolResult, error)
}

// pageAccounting is PageAccounting (paging.ts:38-41); usage nil is "not a number".
type pageAccounting struct {
	pendingTokens int
	usage         *int
}

// reconcile is `reconcile` (paging.ts:42-47): native usage may already include serial results,
// but not parallel siblings.
func reconcile(accounting pageAccounting, usage *int) pageAccounting {
	counted := 0
	if usage != nil && accounting.usage != nil {
		counted = max(0, *usage-*accounting.usage)
	}
	return pageAccounting{pendingTokens: max(0, accounting.pendingTokens-counted), usage: usage}
}

// pageReservations is `PageReservations` (paging.ts:49-143). Reservations precede delivery and
// reset at a turn boundary. The TS class runs each method synchronously; here a mutex keeps each
// public method atomic between concurrent tool calls.
type pageReservations struct {
	mu         sync.Mutex
	accounting pageAccounting
	toolTokens toolTokensFunc
}

// newPageReservations is `new PageReservations(toolTokens)`.
func newPageReservations(toolTokens toolTokensFunc) *pageReservations {
	return &pageReservations{toolTokens: toolTokens}
}

var _ pageAccess = (*pageReservations)(nil)

// reset is `reset` (paging.ts:59-61).
func (p *pageReservations) reset() {
	p.mu.Lock()
	p.accounting = pageAccounting{}
	p.mu.Unlock()
}

// size is `size` (paging.ts:62-74).
func (p *pageReservations) size(ctx policyContext, request pageRequest) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sizeLocked(ctx, request)
}

func (p *pageReservations) sizeLocked(ctx policyContext, request pageRequest) (int, error) {
	usage, err := ctx.getContextUsage()
	if err != nil {
		return 0, err
	}
	var tokens *int
	if usage != nil {
		tokens = usage.tokens
	}
	p.accounting = reconcile(p.accounting, tokens)
	capacity, err := pageCapacity(ctx, p.toolTokens)
	if err != nil {
		return 0, err
	}
	remaining := capacity - p.accounting.pendingTokens
	chars := min(maxPageChars, max(0, remaining-pageMarginTokens)*4-request.imageCount*estimatedImageChars)
	if chars < minPageChars {
		return 0, p.refuse(remaining, request)
	}
	return chars, nil
}

// reserve is `reserve` (paging.ts:75-83).
func (p *pageReservations) reserve(text string, imageCount int) {
	p.accounting.pendingTokens += ceilQuarter(u16len(text)) + imageCount*(estimatedImageChars/4)
}

// refuse is `refuse` (paging.ts:84-95): it always returns the error the original throws.
func (p *pageReservations) refuse(remaining int, request pageRequest) error {
	var position string
	if request.cursor != "" {
		position = "with the same cursor"
	} else {
		position = "with offset " + strconv.Itoa(request.offset)
		if request.imageOffset != nil {
			position += " and imageOffset " + strconv.Itoa(*request.imageOffset)
		}
	}
	message := "Too little context remains to read a page safely. Call new_context first, then retry " + position + "."
	// Even refusals consume context; omit repeated guidance when it no longer fits.
	text := ""
	if ceilQuarter(u16len(message)) <= remaining {
		text = message
	}
	p.reserve(text, 0)
	return errors.New(text)
}

// result is `result` (paging.ts:96-99).
func (p *pageReservations) result(text string, images []imageLike, display *posthorseDisplay) toolResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.resultLocked(text, images, display)
}

func (p *pageReservations) resultLocked(text string, images []imageLike, display *posthorseDisplay) toolResult {
	p.reserve(text, len(images))
	return textResult(text, images, display)
}

// error is `error` (paging.ts:100-104).
func (p *pageReservations) error(ctx policyContext, message string, cursor string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.errorLocked(ctx, message, cursor)
}

func (p *pageReservations) errorLocked(ctx policyContext, message string, cursor string) error {
	if _, err := p.sizeLocked(ctx, pageRequest{offset: 0, cursor: cursor}); err != nil {
		return err
	}
	p.reserve(message, 0)
	return errors.New(message)
}

// notes is `notes` (paging.ts:105-143).
func (p *pageReservations) notes(ctx policyContext, page notesPage) (toolResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	text := page.empty
	if len(page.rows) > 0 {
		text = joinRows(page.rows)
	}
	total := u16len(text)
	size, err := p.sizeLocked(ctx, pageRequest{offset: page.offset})
	if err != nil {
		return toolResult{}, err
	}
	headerLength := u16len(page.header)
	chars := size - headerLength
	footer := func(end int) string {
		tail := " of " + strconv.Itoa(total)
		if page.more {
			tail = "; more matches follow"
		}
		return "\n[chars " + strconv.Itoa(page.offset) + "-" + strconv.Itoa(end) + tail + "; continue with offset " + strconv.Itoa(end) + "]"
	}
	footerLength := len(footer(total)) // ASCII
	if chars <= footerLength {
		return toolResult{}, p.errorLocked(ctx,
			"Too little context remains to include the notes path. Call new_context first, then retry with offset "+strconv.Itoa(page.offset)+".", "")
	}
	if page.offset > 0 && page.offset >= total {
		return toolResult{}, p.errorLocked(ctx, "Offset is past the end; restart with offset 0.", "")
	}
	maxRows := 100
	if page.kind == "notes-search" {
		maxRows = 20
	}
	end0, starts := notesRange(page.rows, page.offset, min(total, page.offset+chars), maxRows)
	end := end0
	if end0 < total {
		end = min(end0, page.offset+chars-footerLength)
	}
	count := 0
	for _, start := range starts {
		if start < end {
			count++
		}
	}
	body := page.header + u16slice(text, page.offset, end)
	if end < total {
		body += footer(end)
	}
	return p.resultLocked(body, nil, displayNotes(page.kind, headerLength, count, page.offset, end, total, page.more)), nil
}

// joinRows is `rows.join("\n")`.
func joinRows(rows []string) string {
	n := len(rows) - 1
	for _, row := range rows {
		n += len(row)
	}
	buf := make([]byte, 0, n)
	for i, row := range rows {
		if i > 0 {
			buf = append(buf, '\n')
		}
		buf = append(buf, row...)
	}
	return string(buf)
}

// notesRange is `notesRange` (paging.ts:144-167): the end of the page and the start offsets of the
// rows it touches, at most maxRows of them.
func notesRange(rows []string, offset, end, maxRows int) (int, []int) {
	start := 0
	var starts []int
	for _, row := range rows {
		if start >= end {
			break
		}
		length := u16len(row)
		if start+length > offset {
			starts = append(starts, start)
		}
		start += length + 1
		if len(starts) == maxRows {
			end = min(end, start-1)
			break
		}
	}
	return end, starts
}
