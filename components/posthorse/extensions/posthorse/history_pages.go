package posthorse

// Port of src/history-pages.ts: the page of a history search or read that fits the remaining
// context budget.

import (
	"fmt"
	"strconv"
	"strings"
)

// pageContinuation is the TS Continuation union: more is false for {kind: "complete"}.
type pageContinuation[Position any] struct {
	more bool
	next Position
}

// searchPageParts is the TS SearchPage (history-pages.ts:14-18).
type searchPageParts struct {
	parts        []string
	spans        []historySpan
	continuation pageContinuation[string]
}

// cursorFor is `cursorFor` (history-pages.ts:19-23).
func cursorFor(matches historyMatchSet, hit historyHit, offset int) (string, error) {
	text, err := jsStringify([]any{hit.id, hit.priority, offset, matches.key})
	if err != nil {
		return "", err
	}
	return encodeCursor(text), nil
}

// searchFooter is `searchFooter` (history-pages.ts:24-26).
func searchFooter(next string) string {
	return "\n[More results; continue with cursor \"" + next + "\" and the same query/scope.]"
}

// hitFragmentPart is the object `hitFragment` returns (history-pages.ts:27-53).
type hitFragmentPart struct {
	part         string
	end          int
	headerLength int
}

// hitFragment is `hitFragment` (history-pages.ts:27-53); false is undefined.
func hitFragment(hit historyHit, cursor *historyCursor, available int, first bool) (hitFragmentPart, bool) {
	offset := 0
	if cursor != nil && cursor.id == hit.id {
		offset = cursor.offset
	}
	total := u16len(hit.text)
	if !first && total-offset > available {
		return hitFragmentPart{}, false
	}
	prefix := ""
	if offset > 0 || total > available {
		prefix = "[entry " + hit.id + "; from char " + strconv.Itoa(offset) + "] "
	}
	prefixLength := u16len(prefix)
	if prefixLength >= available {
		return hitFragmentPart{}, false
	}
	end := min(total, offset+available-prefixLength)
	return hitFragmentPart{
		part:         prefix + u16slice(hit.text, offset, end),
		end:          end,
		headerLength: prefixLength + max(0, min(hit.headerLength-offset, end-offset)),
	}, true
}

// searchParts is `searchParts` (history-pages.ts:54-76).
func searchParts(matches historyMatchSet, chars int) (searchPageParts, error) {
	var page searchPageParts
	available := chars
	for _, hit := range matches.hits {
		fragment, ok := hitFragment(hit, matches.cursor, available, len(page.parts) == 0)
		if !ok {
			break
		}
		page.parts = append(page.parts, fragment.part)
		partLength := u16len(fragment.part)
		page.spans = append(page.spans, historySpan{headerLength: float64(fragment.headerLength), length: float64(partLength)})
		available -= partLength + 1
		if fragment.end < u16len(hit.text) || len(page.parts) < len(matches.hits) {
			next, err := cursorFor(matches, hit, fragment.end)
			if err != nil {
				return searchPageParts{}, err
			}
			page.continuation = pageContinuation[string]{more: true, next: next}
		} else {
			page.continuation = pageContinuation[string]{}
		}
		if available <= 0 || len(page.parts) >= matches.limit {
			break
		}
	}
	return page, nil
}

// searchPage is `searchPage` (history-pages.ts:77-107).
func searchPage(matches historyMatchSet, params historyParams, host policyContext, pages pageAccess) (toolResult, error) {
	chars, err := pages.size(host, pageRequest{offset: 0, cursor: params.cursor})
	if err != nil {
		return toolResult{}, err
	}
	reserve := 0
	for _, hit := range matches.hits {
		cursor, err := cursorFor(matches, hit, u16len(hit.text))
		if err != nil {
			return toolResult{}, err
		}
		reserve = max(reserve, u16len(searchFooter(cursor)))
	}
	if reserve >= chars {
		return toolResult{}, pages.error(host, "History entry id is too large for pagination.", params.cursor)
	}
	skippedNote := ""
	if matches.skipped > 0 && params.cursor == "" {
		plural := "es"
		if matches.skipped == 1 {
			plural = ""
		}
		skippedNote = "\n[Skipped " + strconv.Itoa(matches.skipped) + " match" + plural + " already in your active context.]"
	}
	page, err := searchParts(matches, chars-reserve-u16len(skippedNote))
	if err != nil {
		return toolResult{}, err
	}
	var body string
	if len(page.parts) > 0 {
		body = strings.Join(page.parts, "\n")
	} else {
		body = "No history matches \"" + excerpt(params.query, 200) + "\"."
	}
	tail := skippedNote
	if page.continuation.more {
		tail += searchFooter(page.continuation.next)
	}
	return pages.result(body+tail, nil, displayHistorySearch(page.spans, u16len(tail), page.continuation.more, matches.skipped)), nil
}

// readOffsets is `readOffsets` (history-pages.ts:108-130). The offsets are JS numbers, printed
// as such in the errors; once they pass the checks they are integers within the entry.
func readOffsets(item windowedEntry, params historyParams, total int) (offset, imageOffset int, err error) {
	off := 0.0
	if params.offset != nil {
		off = *params.offset
	}
	imageOff := 0.0
	switch {
	case params.imageOffset != nil:
		imageOff = *params.imageOffset
	case off != 0:
		imageOff = float64(len(item.images))
	}
	if imageOff > float64(len(item.images)) {
		return 0, 0, fmt.Errorf("Image offset %s is past the end of history entry \"%s\" (%d images).", jsNumberString(imageOff), params.id, len(item.images))
	}
	if off > float64(total) || (off == float64(total) && imageOff == float64(len(item.images))) {
		return 0, 0, fmt.Errorf("Offset %s is past the end of history entry \"%s\" (%d chars).", jsNumberString(off), params.id, total)
	}
	return int(off), int(imageOff), nil
}

// shownEntryId is `shownEntryId` (history-pages.ts:131-133).
func shownEntryId(item windowedEntry, id string, source string) string {
	if source == "" {
		return id
	}
	entryID, ok := str(item.entry, "id")
	if !ok {
		entryID = "unknown"
	}
	return entryID + "@" + shortKey(source)
}

// historyReadPage is `historyReadPage` (history-pages.ts:134-182).
func historyReadPage(item windowedEntry, params historyParams, host policyContext, pages pageAccess, source string) (toolResult, error) {
	shownID := shownEntryId(item, params.id, source)
	total := u16len(item.text)
	offset, imageOffset, err := readOffsets(item, params, total)
	if err != nil {
		return toolResult{}, err
	}
	firstImage := 0
	if imageOffset < len(item.images) {
		firstImage = 1
	}
	request := pageRequest{offset: offset, imageCount: firstImage}
	if len(item.images) > 0 {
		request.imageOffset = &imageOffset
	}
	chars, err := pages.size(host, request)
	if err != nil {
		return toolResult{}, err
	}
	imageEnd := min(len(item.images), imageOffset+firstImage+floorDiv(chars-minPageChars, estimatedImageChars))
	images := item.images[imageOffset:imageEnd]
	end := min(total, offset+chars-(len(images)-firstImage)*estimatedImageChars)
	more := ""
	if end < total || imageEnd < len(item.images) {
		more = "\nMore remains; call history read with id \"" + shownID + "\" and offset " + strconv.Itoa(end)
		if len(item.images) > 0 {
			more += " and imageOffset " + strconv.Itoa(imageEnd)
		}
		more += "."
	}
	timestamp, _ := str(item.entry, "timestamp")
	header := timestamp + sourceTag(source) + " [window " + item.windowId + "] [" + shownID + "] [chars " +
		strconv.Itoa(offset) + "-" + strconv.Itoa(end) + " of " + strconv.Itoa(total) + "] "
	text := header + u16slice(item.text, offset, end) + more
	return pages.result(text, images, displayHistoryRead(u16len(header), offset, end, total, imageOffset, imageEnd, len(item.images))), nil
}

// floorDiv is Math.floor(a / b) for integers, b > 0.
func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && a < 0 {
		q--
	}
	return q
}
