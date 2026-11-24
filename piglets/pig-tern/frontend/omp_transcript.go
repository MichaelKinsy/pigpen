package frontend

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/stencil-hq/tern-sdk/go/tern"
)

// ompThinkingNode draws a thinking block in omp's shape
// (chat/assistant-message.ts:936-983):
//
//   - While streaming (live): section(role="omp.thinking.live", collapsible=true,
//     collapsed=thinking.Hidden) with head row (starburst spinner role
//     "omp.thinking.spin", "Thinking…" muted text, elapsed timer) and md body.
//   - Once finished: section(role="omp.thinking", collapsible=true,
//     collapsed=true, took=ms if timed) with head text ("Thought for Ns" or
//     "Thought", muted) and md body. Collapses when streaming ends, as omp does.
func (s *session) ompThinkingNode(id string, thinking fe.Thinking, startedAt, endedAt time.Time, collapsed bool) tern.WireNode {
	headID := id + ".head"
	var head tern.WireNode
	props := map[string]any{
		"collapsible": true,
		"head":        headID,
	}
	if thinking.Streaming {
		ageMs := int64(0)
		if !startedAt.IsZero() {
			if d := s.now().Sub(startedAt); d > 0 {
				ageMs = d.Milliseconds()
			}
		}
		props["role"] = "omp.thinking.live"
		props["collapsed"] = thinking.Hidden
		head = tern.WireNode{
			ID: headID,
			K:  tern.KindRow,
			P:  map[string]any{"gap": "sm"},
			C: []tern.WireNode{
				{ID: headID + ".spin", K: tern.KindSpinner, P: map[string]any{"style": "starburst", "role": "omp.thinking.spin"}},
				{ID: headID + ".label", K: tern.KindText, P: map[string]any{"spans": []any{map[string]any{"t": "Thinking…", "s": "muted"}}}},
				{ID: headID + ".elapsed", K: tern.KindElapsed, P: map[string]any{"age": ageMs, "format": "short"}},
			},
		}
	} else {
		props["role"] = "omp.thinking"
		props["collapsed"] = collapsed
		label := "Thought"
		if !startedAt.IsZero() && !endedAt.IsZero() {
			ms := max(int64(0), endedAt.Sub(startedAt).Milliseconds())
			props["took"] = ms
			label = ompThoughtLabel(ms)
		}
		head = tern.WireNode{
			ID: headID,
			K:  tern.KindText,
			P:  map[string]any{"spans": []any{map[string]any{"t": label, "s": "muted"}}},
		}
	}
	body := tern.WireNode{
		ID: id + ".md",
		K:  tern.KindMd,
		P:  map[string]any{"text": thinking.Text, "stream": thinking.Streaming},
	}
	return tern.WireNode{
		ID: id,
		K:  tern.KindSection,
		P:  props,
		C:  []tern.WireNode{head, body},
	}
}

// ompThoughtLabel formats a finished thinking duration as omp does
// (chat/assistant-message.ts:56-60).
func ompThoughtLabel(ms int64) string {
	if ms < 60_000 {
		secs := max(int64(1), int64(math.Round(float64(ms)/1000)))
		return fmt.Sprintf("Thought for %ds", secs)
	}
	return "Thought for " + ompFormatDuration(ms)
}

// ompFormatDuration mirrors omp's formatDuration (packages/utils/src/format.ts:10-31).
func ompFormatDuration(ms int64) string {
	const (
		sec  = 1_000
		min  = 60 * sec
		hour = 60 * min
		day  = 24 * hour
	)
	switch {
	case ms <= 0:
		return "0ms"
	case ms < sec:
		return fmt.Sprintf("%dms", ms)
	case ms < min:
		tenths := int64(math.Round(float64(ms) / 100))
		if tenths < 600 {
			return fmt.Sprintf("%.1fs", float64(tenths)/10)
		}
		return "1m"
	case ms < hour:
		m := ms / min
		s := (ms % min) / sec
		if s > 0 {
			return fmt.Sprintf("%dm%ds", m, s)
		}
		return fmt.Sprintf("%dm", m)
	case ms < day:
		h := ms / hour
		m := (ms % hour) / min
		if m > 0 {
			return fmt.Sprintf("%dh%dm", h, m)
		}
		return fmt.Sprintf("%dh", h)
	default:
		d := ms / day
		h := (ms % day) / hour
		if h > 0 {
			return fmt.Sprintf("%dd%dh", d, h)
		}
		return fmt.Sprintf("%dd", d)
	}
}

// ompUserMarkdownNode builds omp's user message card (chat/user-message.ts:255-286):
// a user-toned card with role "omp.user", optional "omp.user.images" thumbnail
// row when the user message carries images, and its md child.
func (s *session) ompUserMarkdownNode(id string, md fe.MarkdownText) tern.WireNode {
	children := make([]tern.WireNode, 0, 2)
	if len(md.Images) > 0 {
		thumbs := make([]tern.WireNode, 0, len(md.Images))
		for i := range md.Images {
			img := &md.Images[i]
			if len(img.Data) == 0 {
				continue
			}
			label := "#" + strconv.Itoa(i+1)
			thumbs = append(thumbs, tern.WireNode{
				ID: id + ".images." + strconv.Itoa(i),
				K:  tern.KindImage,
				P:  map[string]any{"blob": s.blob(img), "alt": label, "title": label},
			})
		}
		if len(thumbs) > 0 {
			children = append(children, tern.WireNode{
				ID: id + ".images",
				K:  tern.KindRow,
				P:  map[string]any{"gap": "sm", "wrap": true, "align": "start", "role": "omp.user.images"},
				C:  thumbs,
			})
		}
	}
	children = append(children, tern.WireNode{
		ID: id + ".md",
		K:  tern.KindMd,
		P:  map[string]any{"text": md.Text},
	})
	return tern.WireNode{
		ID: id,
		K:  tern.KindCard,
		P:  map[string]any{"role": "omp.user", "tone": "user"},
		C:  children,
	}
}

// ompErrorStatusCode matches an HTTP status code prefix like "500 upstream...".
var ompErrorStatusCode = regexp.MustCompile(`^(\d{3})\s+`)

// ompMaxErrorRows clamps long error messages as omp does
// (chat/assistant-message.ts:52).
const ompMaxErrorRows = 8

// ompLinesNode recognizes PiG's assistant error/abort lines and horizontal
// divider lines under ompRoles, returning their omp TSP nodes.
func ompLinesNode(id, pigID string, lines []string) (tern.WireNode, bool) {
	if node, ok := ompAssistantErrorNode(id, pigID, lines); ok {
		return node, true
	}
	if node, ok := ompDividerNode(id, lines); ok {
		return node, true
	}
	return tern.WireNode{}, false
}

// ompAssistantErrorNode converts PiG's assistant terminal diagnostic lines
// (emitted with id "<msg>.e" by AssistantMessageComponent.appendFrontendNodes
// in PiG's tui/tui_surface.go:941) into omp's error card or abort text
// (chat/assistant-message.ts:1065-1139).
func ompAssistantErrorNode(id, pigID string, lines []string) (tern.WireNode, bool) {
	if !strings.HasSuffix(pigID, ".e") {
		return tern.WireNode{}, false
	}
	plainLines := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(stripANSI(line)); trimmed != "" {
			plainLines = append(plainLines, trimmed)
		}
	}
	if len(plainLines) == 0 {
		return tern.WireNode{}, false
	}
	joined := strings.Join(plainLines, "\n")

	// Abort line: "Operation aborted" (no "Error: " prefix).
	if joined == "Operation aborted" {
		return tern.WireNode{
			ID: id,
			K:  tern.KindText,
			P: map[string]any{
				"spans": []any{map[string]any{"t": joined, "s": "error"}},
				"wrap":  "word",
				"role":  "omp.assistant.abort",
			},
		}, true
	}

	// Provider / request error: PiG prefixes "Error: ".
	msg, isError := strings.CutPrefix(joined, "Error: ")
	if !isError {
		// Truncation notice or custom abort text emitted under "<msg>.e".
		return tern.WireNode{
			ID: id,
			K:  tern.KindText,
			P: map[string]any{
				"spans": []any{map[string]any{"t": joined, "s": "error"}},
				"wrap":  "word",
				"role":  "omp.assistant.abort",
			},
		}, true
	}

	errLines := strings.Split(msg, "\n")
	var code string
	if m := ompErrorStatusCode.FindStringSubmatch(errLines[0]); m != nil {
		code = m[1]
		errLines[0] = errLines[0][len(m[0]):]
	}
	// Drop lines whose prefix is repeated with more detail by a later line,
	// matching omp's assistant-message.ts:1097.
	var detail []string
	for i, line := range errLines {
		if line == "" {
			continue
		}
		repeated := false
		for _, next := range errLines[i+1:] {
			if strings.HasPrefix(next, line) {
				repeated = true
				break
			}
		}
		if !repeated {
			detail = append(detail, line)
		}
	}
	errorText := strings.Join(detail, "\n")
	if errorText == "" {
		errorText = "Unknown error"
	}

	headChildren := []tern.WireNode{
		{
			ID: id + ".head.title",
			K:  tern.KindText,
			P:  map[string]any{"spans": []any{map[string]any{"t": "Request failed", "s": "error strong"}}},
		},
	}
	if code != "" {
		headChildren = append(headChildren, tern.WireNode{
			ID: id + ".head.code",
			K:  tern.KindBadge,
			P:  map[string]any{"text": code, "tone": "error", "role": "omp.error.code"},
		})
	}
	return tern.WireNode{
		ID: id,
		K:  tern.KindCard,
		P:  map[string]any{"role": "omp.error", "tone": "error"},
		C: []tern.WireNode{
			{ID: id + ".head", K: tern.KindRow, P: map[string]any{"gap": "sm"}, C: headChildren},
			{
				ID: id + ".message",
				K:  tern.KindText,
				P: map[string]any{
					"spans": []any{map[string]any{"t": errorText, "s": "mono"}},
					"wrap":  "word",
					"lines": ompMaxErrorRows,
					"role":  "omp.error.message",
				},
			},
		},
	}, true
}

// ompDividerNode recognizes PiG's DynamicBorder horizontal rule lines (one
// non-blank line of box-drawing "─" characters, optionally followed by a label)
// and emits omp's rule node (chrome/message-divider.ts:57-60).
func ompDividerNode(id string, lines []string) (tern.WireNode, bool) {
	var nonBlank []string
	for _, line := range lines {
		if trimmed := strings.TrimSpace(stripANSI(line)); trimmed != "" {
			nonBlank = append(nonBlank, trimmed)
		}
	}
	if len(nonBlank) != 1 {
		return tern.WireNode{}, false
	}
	line := nonBlank[0]
	ruleRunes := 0
	rest := line
	for len(rest) > 0 {
		if strings.HasPrefix(rest, "─") {
			ruleRunes++
			rest = rest[len("─"):]
			continue
		}
		break
	}
	if ruleRunes < 3 {
		return tern.WireNode{}, false
	}
	label := strings.TrimSpace(rest)
	// Reject lines like "─── foo ───" only if trailing rule? Allow centered or
	// left-aligned rule labels by stripping trailing rule chars too.
	label = strings.TrimSpace(strings.TrimRight(label, "─"))
	props := map[string]any{"role": "omp.divider"}
	if label != "" {
		props["label"] = []any{map[string]any{"t": label, "s": "muted"}}
	}
	return tern.WireNode{ID: id, K: tern.KindRule, P: props}, true
}
