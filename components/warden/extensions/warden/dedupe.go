package warden

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"
)

// Repeated-run detection (dedupe.ts): a new tool result or message that pastes again a long block the agent already
// has in context (a relayed report that quotes every earlier turn) keeps only its new part. Exact line matches only,
// after trimming trailing spaces; near-duplicates are never collapsed. Deterministic; no requests.

const (
	// RunMinLines and RunMinChars: a run shorter than this, in lines or characters, is kept: a short repeat costs
	// less than the pointer that replaces it.
	RunMinLines = 20
	RunMinChars = 1500
	// TailKeepChars: errors and verdicts usually sit at the end of a result, so the last characters are never replaced.
	TailKeepChars = 2000
	// maxPositions: several earlier copies of one window are enough to find the longest match; more only cost memory.
	maxPositions = 4
	hashBase     = 1_000_003
)

// RepeatedRun is a run of lines of the new text that repeats an earlier text.
type RepeatedRun struct {
	// Start is the first line of the run in the new text, 0-based.
	Start int
	Lines int
	Chars int
	// Where the earlier copy is, e.g. "an earlier bash result".
	Where string
}

// SeenItem is one text the agent has in context, with a stable identity so a sync adds it once.
type SeenItem struct {
	Key   string
	Where string
	Texts []string
}

// hashTop is BASE^(RunMinLines - 1) mod 2^32, for the rolling window hash (32-bit wraparound like Math.imul).
var hashTop = func() int32 {
	var power int32 = 1
	for i := 1; i < RunMinLines; i++ {
		power *= hashBase
	}
	return power
}()

// windowHashes is the hash of every RunMinLines-line window, by start line.
func windowHashes(ids []int32) []int32 {
	if len(ids) < RunMinLines {
		return nil
	}
	hashes := make([]int32, 0, len(ids)-RunMinLines+1)
	var hash int32
	for i := range RunMinLines {
		hash = hash*hashBase + ids[i]
	}
	hashes = append(hashes, hash)
	for i := RunMinLines; i < len(ids); i++ {
		hash = (hash-ids[i-RunMinLines]*hashTop)*hashBase + ids[i]
		hashes = append(hashes, hash)
	}
	return hashes
}

type seenDoc struct {
	where string
	ids   []int32
}

type windowPos struct{ doc, line int }

// SeenText indexes the texts in context.
type SeenText struct {
	// lineIDs are the interned normalized lines; ids start at 1 so 0 marks a line never seen.
	lineIDs map[string]int32
	docs    []seenDoc
	windows map[int32][]windowPos
	keys    map[string]struct{}
}

// NewSeenText returns an empty index.
func NewSeenText() *SeenText {
	s := &SeenText{}
	s.Clear()
	return s
}

// Clear forgets everything.
func (s *SeenText) Clear() {
	s.lineIDs = map[string]int32{}
	s.docs = nil
	s.windows = map[int32][]windowPos{}
	s.keys = map[string]struct{}{}
}

// Sync matches the index to what is in context now: a compaction or a move to another branch drops texts, so it rebuilds.
func (s *SeenText) Sync(items []SeenItem) {
	current := make(map[string]struct{}, len(items))
	for _, item := range items {
		current[item.Key] = struct{}{}
	}
	for key := range s.keys {
		if _, ok := current[key]; !ok {
			s.Clear()
			break
		}
	}
	for _, item := range items {
		if _, ok := s.keys[item.Key]; ok {
			continue
		}
		s.keys[item.Key] = struct{}{}
		for _, text := range item.Texts {
			s.Add(text, item.Where)
		}
	}
}

// Add indexes a text.
func (s *SeenText) Add(text, where string) {
	lines := strings.Split(text, "\n")
	if len(lines) < RunMinLines {
		return
	}
	ids := make([]int32, len(lines))
	for i, line := range lines {
		normalized := jsTrimEnd(line)
		id, ok := s.lineIDs[normalized]
		if !ok {
			id = int32(len(s.lineIDs) + 1)
			s.lineIDs[normalized] = id
		}
		ids[i] = id
	}
	s.docs = append(s.docs, seenDoc{where: where, ids: ids})
	doc := len(s.docs) - 1
	for line, hash := range windowHashes(ids) {
		if positions := s.windows[hash]; len(positions) < maxPositions {
			s.windows[hash] = append(positions, windowPos{doc, line})
		}
	}
}

// Find returns the runs of `text` that repeat one earlier text exactly, longest match first at each position,
// outside the kept tail.
func (s *SeenText) Find(text string) []RepeatedRun {
	lines := strings.Split(text, "\n")
	cutoff := utf16Len(text) - TailKeepChars
	limit, offset := 0, 0
	for ; limit < len(lines); limit++ {
		end := offset + utf16Len(lines[limit]) + 1
		if end > cutoff {
			break
		}
		offset = end
	}
	if limit < RunMinLines {
		return nil
	}
	ids := make([]int32, limit)
	for i := range limit {
		ids[i] = s.lineIDs[jsTrimEnd(lines[i])]
	}
	hashes := windowHashes(ids)
	var runs []RepeatedRun
	for index := 0; index+RunMinLines <= limit; {
		bestDoc, bestLen := -1, 0
		for _, pos := range s.windows[hashes[index]] {
			seen := s.docs[pos.doc].ids
			length := 0
			for index+length < limit && pos.line+length < len(seen) && ids[index+length] != 0 && ids[index+length] == seen[pos.line+length] {
				length++
			}
			if length >= RunMinLines && (bestDoc < 0 || length > bestLen) {
				bestDoc, bestLen = pos.doc, length
			}
		}
		if bestDoc >= 0 {
			chars := 0
			for line := index; line < index+bestLen; line++ {
				chars += utf16Len(lines[line]) + 1
			}
			if chars >= RunMinChars {
				runs = append(runs, RepeatedRun{Start: index, Lines: bestLen, Chars: chars, Where: s.docs[bestDoc].where})
				index += bestLen
				continue
			}
		}
		index++
	}
	return runs
}

// CollapseRuns replaces each run with one pointer line; `path` holds the full original text.
func CollapseRuns(text string, runs []RepeatedRun, path string) string {
	if len(runs) == 0 {
		return text
	}
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	index := 0
	for _, run := range runs {
		kept = append(kept, lines[index:run.Start]...)
		kept = append(kept, fmt.Sprintf("[pi-warden: the next %d lines repeat %s — omitted; full text: %s]", run.Lines, run.Where, path))
		index = run.Start + run.Lines
	}
	kept = append(kept, lines[index:]...)
	return strings.Join(kept, "\n")
}

// SeenEntry is a session entry as the dedupe reads it: a message or a custom message.
type SeenEntry struct {
	ID         string
	Type       string // "message" | "custom_message" | anything else (not indexed)
	CustomType string // custom_message
	// Content of a custom_message: a string or a block array.
	Content any
	// Message of a "message" entry; nil otherwise.
	Message *SeenMessage
}

// SeenMessage is the message of a "message" entry.
type SeenMessage struct {
	Role       string
	ToolName   string
	CustomType string
	Content    any
}

// textsOf: the text of a string content or of the text blocks of a block array.
func textsOf(content any) []string {
	switch c := content.(type) {
	case string:
		return []string{c}
	case []any:
		var texts []string
		for _, part := range c {
			if m, ok := part.(map[string]any); ok && m["type"] == "text" {
				if t, ok := m["text"].(string); ok {
					texts = append(texts, t)
				}
			}
		}
		return texts
	case []map[string]any:
		parts := make([]any, len(c))
		for i := range c {
			parts[i] = c[i]
		}
		return textsOf(parts)
	}
	return nil
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// SeenItemOf is the texts a session entry puts in context and where the agent saw them. Thinking, images and
// summaries are not indexed.
func SeenItemOf(entry SeenEntry) (SeenItem, bool) {
	key := entry.ID
	if key == "" {
		sum := sha1.Sum([]byte(jsStringify(entry)))
		key = "entry-" + hex.EncodeToString(sum[:])
	}
	if entry.Type == "custom_message" {
		return SeenItem{Key: key, Where: "an earlier " + orDefault(entry.CustomType, "custom") + " message", Texts: textsOf(entry.Content)}, true
	}
	if entry.Type != "message" || entry.Message == nil {
		return SeenItem{}, false
	}
	m := entry.Message
	var where string
	switch m.Role {
	case "toolResult":
		where = "an earlier " + orDefault(m.ToolName, "tool") + " result"
	case "user":
		where = "an earlier user message"
	case "assistant":
		where = "an earlier assistant reply"
	case "custom":
		where = "an earlier " + orDefault(m.CustomType, "custom") + " message"
	default:
		return SeenItem{}, false
	}
	return SeenItem{Key: key, Where: where, Texts: textsOf(m.Content)}, true
}
