package warden

import (
	"strings"
	"time"
	"unicode/utf16"
)

// The runaway guard (src/runaway.ts): a model that degenerates mid-reply repeats the same block of text over and
// over without calling a tool, and nothing else stops it before the token limit. The guard reads the stream, counts
// identical blocks with code only, and stops the run when a block repeats past the threshold. No request leaves the
// machine.

// StreamKind is the stream a delta belongs to.
type StreamKind string

// Stream kinds.
const (
	StreamText     StreamKind = "text"
	StreamThinking StreamKind = "thinking"
)

// RepeatSignal is how often one block repeated.
type RepeatSignal struct {
	Count int
	// Block is the normalised block that repeated; empty when nothing qualified.
	Block string
	// Signal is "block" (an identical paragraph) or "phrase" (the text ends with one unit repeated back to back);
	// set by FindRepeats.
	Signal string
}

// RunawayVerdict is a stopped stream.
type RunawayVerdict struct {
	Kind  StreamKind
	Count int
	Block string
	// Chars streamed for this kind when the run was stopped.
	Chars  int
	Signal string
}

const (
	// BlockMinChars: paragraphs shorter than this (normalised) are ignored; code fences, list markers and short
	// labels repeat legitimately.
	BlockMinChars = 24
	// PhraseChars is the length of the trailing phrase whose recurrence is counted.
	PhraseChars = 60
	// CheckEvery is the number of characters streamed between two checks.
	CheckEvery = 256
)

var (
	reJSSpaceRun     = lazyRE(`[` + jsSpaceClass + `]+`)
	reParagraphBreak = lazyRE(`\n[` + jsSpaceClass + `]*\n`)
)

// normaliseRunaway is runaway.ts `normalise`: whitespace runs collapse to one space; trimmed and lower-cased.
func normaliseRunaway(text string) string {
	return strings.ToLower(jsTrim(reJSSpaceRun.ReplaceAllLiteralString(text, " ")))
}

// RepeatedBlock is the paragraph (blank-line separated, normalised, at least minChars) that occurs most often.
func RepeatedBlock(text string, minChars int) RepeatSignal {
	counts := map[string]int{}
	best := RepeatSignal{}
	for _, raw := range reParagraphBreak.Split(text, -1) {
		block := normaliseRunaway(raw)
		if utf16Len(block) < minChars {
			continue
		}
		counts[block]++
		if counts[block] > best.Count {
			best = RepeatSignal{Count: counts[block], Block: block}
		}
	}
	return best
}

func lastIndexUnits(hay, needle []uint16, from int) int {
	if from > len(hay)-len(needle) {
		from = len(hay) - len(needle)
	}
	for i := from; i >= 0; i-- {
		match := true
		for j := range needle {
			if hay[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func equalUnits(a, b []uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// RepeatedTail is run-on repetition without paragraph breaks: the text ends with one unit repeated back to back.
// The trailing chars locate the period (distance to their previous occurrence); the whole unit must match on every
// step back, so templated sentences that share only a suffix count once. Units shorter than minPeriod (table
// separators, ellipses) are ignored.
func RepeatedTail(text string, chars, minPeriod int) RepeatSignal {
	normal := utf16.Encode([]rune(normaliseRunaway(text)))
	if len(normal) < chars*2 {
		return RepeatSignal{}
	}
	start := len(normal) - chars
	phrase := normal[start:]
	previous := lastIndexUnits(normal, phrase, start-1)
	if previous == -1 {
		return RepeatSignal{Count: 1, Block: string(utf16.Decode(phrase))}
	}
	period := start - previous
	if period < minPeriod {
		return RepeatSignal{Count: 1, Block: string(utf16.Decode(phrase))}
	}
	unit := normal[len(normal)-period:]
	count := 1
	for end := len(normal) - period; end >= period && equalUnits(normal[end-period:end], unit); end -= period {
		count++
	}
	return RepeatSignal{Count: count, Block: string(utf16.Decode(unit))}
}

// FindRepeats is the stronger of the two signals for one stream.
func FindRepeats(text string) RepeatSignal {
	block := RepeatedBlock(text, BlockMinChars)
	phrase := RepeatedTail(text, PhraseChars, BlockMinChars)
	if block.Count >= phrase.Count {
		block.Signal = "block"
		return block
	}
	phrase.Signal = "phrase"
	return phrase
}

type streamState struct {
	text      strings.Builder
	length    int // UTF-16 length of text
	checkedAt int
}

// StreamEvent is one AssistantMessageEvent: its type ("start", "text_start", "text_delta", "thinking_delta", ...)
// and the delta of a *_delta event (nil when absent).
type StreamEvent struct {
	Type  string
	Delta *string
}

// RunawayMonitor follows one assistant message as it streams. Feed is called per token and only appends; Check
// runs every CheckEvery characters. Not safe for concurrent use; the caller serialises.
type RunawayMonitor struct {
	streams     map[StreamKind]*streamState
	stoppedKind StreamKind
}

// NewRunawayMonitor returns a monitor with empty streams.
func NewRunawayMonitor() *RunawayMonitor {
	m := &RunawayMonitor{}
	m.Reset()
	return m
}

// Reset starts a fresh message.
func (m *RunawayMonitor) Reset() {
	m.streams = map[StreamKind]*streamState{StreamText: {}, StreamThinking: {}}
	m.stoppedKind = ""
}

// Stopped is true once this message triggered a stop; later deltas from the same stream are ignored.
func (m *RunawayMonitor) Stopped() bool { return m.stoppedKind != "" }

// Streamed is the text seen so far for one stream kind.
func (m *RunawayMonitor) Streamed(kind StreamKind) string { return m.streams[kind].text.String() }

// Feed takes one event and returns the stream kind that has grown enough for a check, else "".
func (m *RunawayMonitor) Feed(event StreamEvent) StreamKind {
	if event.Type == "start" {
		m.Reset()
		return ""
	}
	var kind StreamKind
	switch {
	case strings.HasPrefix(event.Type, "text_"):
		kind = StreamText
	case strings.HasPrefix(event.Type, "thinking_"):
		kind = StreamThinking
	default:
		return ""
	}
	stream := m.streams[kind]
	if strings.HasSuffix(event.Type, "_start") {
		if stream.length > 0 {
			stream.text.WriteString("\n\n")
			stream.length += 2
		}
		return ""
	}
	if !strings.HasSuffix(event.Type, "_delta") || event.Delta == nil {
		return ""
	}
	stream.text.WriteString(*event.Delta)
	stream.length += utf16Len(*event.Delta)
	if stream.length-stream.checkedAt >= CheckEvery {
		return kind
	}
	return ""
}

// Check judges one stream against the config; at most one verdict per message.
func (m *RunawayMonitor) Check(kind StreamKind, config RunawayConfig) *RunawayVerdict {
	stream := m.streams[kind]
	stream.checkedAt = stream.length
	if m.stoppedKind != "" || stream.length < config.MinChars {
		return nil
	}
	limit := config.Repeats
	if kind == StreamThinking {
		limit = config.ThinkingRepeats
	}
	found := FindRepeats(stream.text.String())
	if found.Count < limit {
		return nil
	}
	m.stoppedKind = kind
	return &RunawayVerdict{Kind: kind, Count: found.Count, Block: found.Block, Chars: stream.length, Signal: found.Signal}
}

func clipEllipsis(text string, limit int) string {
	if utf16Len(text) <= limit {
		return text
	}
	return utf16Slice(text, 0, limit) + "…"
}

// RunawayNudge is the follow-up for the agent after its run was stopped. recover: this message starts the next turn.
func RunawayNudge(v RunawayVerdict, recover bool) string {
	what := "pi-warden stopped your reply: the same " + string(v.Kind) + " block repeated " + itoaInt(v.Count) + ` times ("` + clipEllipsis(v.Block, 80) + `") and no tool was called.`
	if !recover {
		return what + " The run was stopped again for this prompt and was not restarted. Wait for the user."
	}
	return what + " Do not restate what you are about to do. If a command is the next step, call the tool with it now. Otherwise answer in at most three sentences: the current state and the one next action, or a question for the user."
}

// RunawayTokens are the widget template values of a runaway verdict (widget.ts runawayTokens).
func RunawayTokens(v RunawayVerdict, recovering bool, at time.Time) Tokens {
	status := "stopped"
	if recovering {
		status = "stopped, recovering"
	}
	return Tokens{"guard": "runaway", "time": clock(at), "kind": string(v.Kind), "count": itoaInt(v.Count), "chars": itoaInt(v.Chars), "signal": v.Signal, "block": v.Block, "status": status}
}

// FormatRunaway renders the verdict line; template is Config.Widget.Runaway.
func FormatRunaway(v RunawayVerdict, recovering bool, template string) string {
	return RenderTemplate(template, RunawayTokens(v, recovering, time.Now()))
}

// RunawayDetails are the trace detail lines of a runaway verdict (trace.ts runawayDetails).
func RunawayDetails(v RunawayVerdict, told string, recovering bool) []string {
	signal := "recurring trailing phrase"
	if v.Signal == "block" {
		signal = "identical paragraph"
	}
	outcome := "run stopped; not restarted"
	if recovering {
		outcome = "run stopped; one follow-up turn started"
	}
	lines := []string{
		"repeated: " + clipEllipsis(v.Block, 200),
		itoaInt(v.Count) + " times in " + itoaInt(v.Chars) + " chars of " + string(v.Kind) + "; signal: " + signal,
		outcome,
	}
	if told != "" {
		lines = append(lines, "agent told: "+clipEllipsis(told, 400))
	}
	return lines
}
