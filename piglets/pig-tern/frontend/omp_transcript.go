package frontend

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
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
			if len(img.Data) == 0 || len(img.Data) > tern.MaxBlob {
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

// ompErrorStatusCode matches an HTTP status code prefix like "500 upstream..."
// or PiG's "400: {...}" provider error format.
var ompErrorStatusCode = regexp.MustCompile(`^(\d{3})(?::\s*|\s+)`)

// ompMaxErrorRows clamps long error messages as omp does
// (chat/assistant-message.ts:52).
const ompMaxErrorRows = 8

// ompLinesNode recognizes PiG's assistant error/abort lines, startup
// loaded-resources discovery sections, compaction/branch summary boxes,
// async-result background job notifications, generic custom message boxes,
// blank spacers, and horizontal divider lines under ompRoles, returning their
// omp TSP nodes.
func ompLinesNode(id, pigID string, lines []string, expanded bool, lookupCompaction func(int) (CompactionMeta, bool)) (tern.WireNode, bool) {
	if node, ok := ompAssistantErrorNode(id, pigID, lines, expanded); ok {
		return node, true
	}
	if node, ok := ompDiscoveryNoticeNode(id, lines); ok {
		return node, true
	}
	if node, ok := ompCompactionNode(id, lines, expanded, lookupCompaction); ok {
		return node, true
	}
	if node, ok := ompBranchNode(id, lines, expanded); ok {
		return node, true
	}
	if node, ok := ompAsyncResultNode(id, lines); ok {
		return node, true
	}
	if node, ok := ompCustomMessageNode(id, lines); ok {
		return node, true
	}
	if blank(linesText(lines)) {
		// Tern's `.sf-fresh` landing mode treats plain `ansi` nodes (even when
		// `hidden: true`) as transcript content, which disables the centered hero
		// layout, dot-grid sky, and aurora glow. Representing blank PiG spacers
		// as hidden `spacer` nodes preserves zero visual height while keeping
		// `.sf-fresh` active on fresh sessions.
		return tern.WireNode{ID: id, K: tern.KindSpacer, P: map[string]any{"hidden": true}}, true
	}
	if node, ok := ompDividerNode(id, lines); ok {
		return node, true
	}
	return tern.WireNode{}, false
}

// ompAssistantErrorNode converts PiG's assistant terminal diagnostic lines
// (emitted with id "<msg>.e" by AssistantMessageComponent.appendFrontendNodes
// in PiG's tui/tui_surface.go:941) into omp's error card or abort text
// (chat/assistant-message.ts:1227-1301).
func ompAssistantErrorNode(id, pigID string, lines []string, expanded bool) (tern.WireNode, bool) {
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
	errorText := ompFormatProviderErrorText(strings.Join(detail, "\n"))
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
	// upstream: chat/assistant-message.ts:1278-1298 — error card actions.
	// Ported via PiG frontend contract + OSC 52:
	//   - "Copy error" (OSC 52 clipboard write on s.out)
	//   - "Switch model" (s.runCommand("/model") via Env.Send)
	// Omitted (HOST): "Retry" (F5) — PiG's frontend.Env provides no retry-turn
	// callback or /retry slash command.
	cardID := id + ".error"
	actionsRow := tern.WireNode{
		ID: cardID + ".actions",
		K:  tern.KindRow,
		P:  map[string]any{"gap": "sm", "role": "omp.error.actions"},
		C: []tern.WireNode{
			{
				ID: cardID + ".actions.copy-error",
				K:  tern.KindRow,
				P: map[string]any{
					"gap":     "xs",
					"role":    "omp.error.action",
					"actions": map[string]any{"click": "copy-error"},
					"title":   "Copy the error message",
				},
				C: []tern.WireNode{
					{ID: cardID + ".actions.copy-error.0", K: tern.KindText, P: map[string]any{"text": "Copy error"}},
				},
			},
			{
				ID: cardID + ".actions.switch-model",
				K:  tern.KindRow,
				P: map[string]any{
					"gap":     "xs",
					"role":    "omp.error.action",
					"actions": map[string]any{"click": "switch-model"},
					"title":   "Pick another model for this session",
				},
				C: []tern.WireNode{
					{ID: cardID + ".actions.switch-model.0", K: tern.KindText, P: map[string]any{"text": "Switch model"}},
					{ID: cardID + ".actions.switch-model.1", K: tern.KindKbd, P: map[string]any{"keys": []any{"⌥P"}}},
				},
			},
		},
	}
	// upstream: chat/assistant-message.ts:1165-1166, 1217 — AssistantMessageComponent
	// wraps `card[omp.error]` inside `col[omp.assistant]`.
	headID := cardID + ".head"
	headChildren[0].ID = headID + ".title"
	if len(headChildren) > 1 {
		headChildren[1].ID = headID + ".code"
	}
	errCard := tern.WireNode{
		ID: cardID,
		K:  tern.KindCard,
		P:  map[string]any{"role": "omp.error", "tone": "error", "head": headID},
		C: []tern.WireNode{
			{ID: headID, K: tern.KindRow, P: map[string]any{"gap": "sm"}, C: headChildren},
			func() tern.WireNode {
				msgProps := map[string]any{
					"spans": []any{map[string]any{"t": errorText, "s": "mono"}},
					"wrap":  "word",
					"role":  "omp.error.message",
				}
				// upstream: chat/assistant-message.ts:1273 — clamp to 8 rows when collapsed,
				// omit `lines` when Ctrl+O tool-output expansion is active.
				if !expanded {
					msgProps["lines"] = ompMaxErrorRows
				}
				return tern.WireNode{ID: cardID + ".message", K: tern.KindText, P: msgProps}
			}(),
			actionsRow,
		},
	}
	return tern.WireNode{
		ID: id,
		K:  tern.KindCol,
		P:  map[string]any{"role": "omp.assistant"},
		C:  []tern.WireNode{errCard},
	}, true
}

// ompFormatProviderErrorText formats a JSON HTTP provider error body as omp's
// `formatCapturedHttpErrorParts` does (packages/ai/src/utils/http-inspector.ts:317-346):
// `<message> (type=<t> param=<p> code=<c>)` (skipping absent fields). Handles
// wrapped single-line JSON split across terminal width lines by PiG's
// AssistantMessageComponent (`tui/tui_surface.go:941`). Non-JSON error text is
// returned verbatim.
func ompFormatProviderErrorText(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	// PiG's AssistantMessageComponent wraps error lines with wrapText, which can
	// insert '\n' inside a JSON string value. Try parsing as-is first, then with
	// internal newlines replaced by spaces (matching how word-wrap broke spaces).
	var payload map[string]any
	if strings.HasPrefix(trimmed, "{") {
		if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
			unwrapped := strings.ReplaceAll(trimmed, "\n", " ")
			_ = json.Unmarshal([]byte(unwrapped), &payload)
		}
	}
	if payload == nil {
		return trimmed
	}
	errObj := payload
	if nested, ok := payload["error"].(map[string]any); ok {
		errObj = nested
	}
	getStr := func(m map[string]any, key string) string {
		if m == nil {
			return ""
		}
		if s, ok := m[key].(string); ok {
			return strings.TrimSpace(s)
		}
		return ""
	}
	core := getStr(errObj, "message")
	if core == "" {
		core = getStr(payload, "message")
	}
	if core == "" {
		if errObj == nil || len(errObj) == len(payload) {
			core = getStr(payload, "error")
		}
	}
	if core == "" {
		return trimmed
	}
	var extras []string
	if v := getStr(errObj, "type"); v != "" {
		extras = append(extras, "type="+v)
	} else if v := getStr(payload, "type"); v != "" {
		extras = append(extras, "type="+v)
	}
	if v := getStr(errObj, "param"); v != "" {
		extras = append(extras, "param="+v)
	} else if v := getStr(payload, "param"); v != "" {
		extras = append(extras, "param="+v)
	}
	if v := getStr(errObj, "code"); v != "" {
		extras = append(extras, "code="+v)
	} else if v := getStr(payload, "code"); v != "" {
		extras = append(extras, "code="+v)
	}
	if len(extras) == 0 {
		return core
	}
	return core + " (" + strings.Join(extras, " ") + ")"
}

// ompErrorAction handles click actions emitted by omp.error.actions rows in the
// transcript ("copy-error" -> OSC 52 clipboard copy; "switch-model" -> "/model").
func (s *session) ompErrorAction(action *tern.Action) bool {
	if action.SF != surfaceID {
		return false
	}
	switch action.Act {
	case "switch-model":
		s.runCommand("/model")
		return true
	case "copy-error":
		nodeID, ok := strings.CutSuffix(action.ID, ".error.actions.copy-error")
		if !ok {
			nodeID, ok = strings.CutSuffix(action.ID, ".actions.copy-error")
			if !ok {
				return false
			}
		}
		pigID, ok := strings.CutPrefix(nodeID, "n.")
		if !ok {
			return false
		}
		s.mu.Lock()
		var text string
		if e := s.sent[pigID]; e != nil && !s.closed {
			if lines, ok := e.node.(fe.Lines); ok {
				text = strings.TrimSpace(stripANSI(strings.Join(lines.Lines, "\n")))
				text = strings.TrimPrefix(text, "Error: ")
			}
		}
		suspended := s.suspended || s.closed || s.fellBack
		s.mu.Unlock()
		if text != "" && !suspended && s.out != nil {
			encoded := base64.StdEncoding.EncodeToString([]byte(text))
			_, _ = io.WriteString(s.out, "\x1b]52;c;"+encoded+"\x07")
		}
		return true
	}
	return false
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

// ompDiscoverySectionHeadings lists the `[Section]` headings emitted by PiG's
// `InteractiveMode.showLoadedResources` (`internal/codingagent/loaded_resources.go`).
var ompDiscoverySectionHeadings = map[string]bool{
	"[Context]":          true,
	"[Skills]":           true,
	"[Prompts]":          true,
	"[Extensions]":       true,
	"[Themes]":           true,
	"[Skill warnings]":   true,
	"[Prompt conflicts]": true,
	"[Extension issues]": true,
	"[Theme conflicts]":  true,
}

// isOmpDiscoverySection reports whether lines represent one of PiG's startup
// loaded-resources sections (`[Context]`, `[Skills]`, `[Prompts]`,
// `[Extensions]`, `[Themes]`, `[Skill warnings]`, `[Prompt conflicts]`,
// `[Extension issues]`, `[Theme conflicts]`).
func isOmpDiscoverySection(lines []string) bool {
	for _, line := range lines {
		trimmed := strings.TrimSpace(stripANSI(line))
		if trimmed == "" {
			continue
		}
		return ompDiscoverySectionHeadings[trimmed]
	}
	return false
}

// ompDiscoveryNoticeNode converts one PiG startup loaded-resources section
// (`[Context]`, `[Skills]`, `[Prompts]`, `[Extensions]`, `[Themes]`, etc.) into
// a native collapsible `section[omp.notice.discovery]` node. Tern's
// `omp-chat-reader.css` explicitly allows `[data-role^='omp.notice.']` inside
// `.sf-main.sf-fresh` (at `max-width: 736px`), so fresh sessions keep Tern's
// dot-grid sky, centered welcome hero, and aurora composer while still letting
// the user inspect loaded resources on click.
func ompDiscoveryNoticeNode(id string, lines []string) (tern.WireNode, bool) {
	if !isOmpDiscoverySection(lines) {
		return tern.WireNode{}, false
	}
	var heading string
	var bodyLines []string
	for _, raw := range lines {
		plain := strings.TrimRight(stripANSI(raw), " \t")
		trimmed := strings.TrimSpace(plain)
		if heading == "" {
			if trimmed == "" {
				continue
			}
			heading = strings.TrimPrefix(strings.TrimSuffix(trimmed, "]"), "[")
			continue
		}
		if trimmed != "" {
			bodyLines = append(bodyLines, plain)
		}
	}
	if heading == "" {
		return tern.WireNode{}, false
	}
	summary := ""
	if len(bodyLines) > 0 {
		first := strings.TrimSpace(bodyLines[0])
		if len(bodyLines) == 1 {
			summary = first
		} else {
			summary = fmt.Sprintf("%s (+%d more)", first, len(bodyLines)-1)
		}
	}
	headSpans := []any{map[string]any{"t": heading, "s": "muted"}}
	if summary != "" {
		headSpans = append(headSpans, map[string]any{"t": " · " + summary, "s": "dim"})
	}
	headID := id + ".head"
	head := tern.WireNode{
		ID: headID,
		K:  tern.KindText,
		P:  map[string]any{"spans": headSpans, "wrap": "none"},
	}
	bodyText := strings.Join(bodyLines, "\n")
	if bodyText == "" {
		bodyText = heading
	}
	body := tern.WireNode{
		ID: id + ".body",
		K:  tern.KindText,
		P:  map[string]any{"spans": []any{map[string]any{"t": bodyText, "s": "dim mono"}}, "wrap": "word"},
	}
	return tern.WireNode{
		ID: id,
		K:  tern.KindSection,
		P: map[string]any{
			"role":        "omp.notice.discovery",
			"collapsible": true,
			"collapsed":   true,
			"head":        headID,
		},
		C: []tern.WireNode{head, body},
	}, true
}

// ompCompactionMethodLabels mirrors COMPACTION_METHOD_LABELS in upstream omp
// (packages/tui/src/chat/compaction-summary-message.ts:16-22), capitalized for
// the native divider chip (`compactionNativeLabel`).
var ompCompactionMethodLabels = map[string]string{
	"snapcompact": "Snap-compacted",
	"soft":        "Soft-compacted",
	"remote":      "Remote-compacted",
	"handoff":     "Handed-off",
	"shake":       "Shaken",
}

var (
	ompCompactedFromRe   = regexp.MustCompile(`^Compacted from ([0-9,]+) tokens(?:\s+\(([^)]+to expand)\))?$`)
	ompBranchCollapsedRe = regexp.MustCompile(`^Branch summary \([^)]+to expand\)$`)
	ompCustomTagRe       = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9:._-]{0,63}$`)
	ompAsyncSingleJobRe  = regexp.MustCompile(`^Background job ([^\s(]+)(?:\s+\(([^)]+)\))?\s+has completed\.?$`)
	ompAsyncMultiJobRe   = regexp.MustCompile(`^──\s*Job ([^\s(]+)(?:\s+\(([^)]+)\))?\s*──$`)
	ompTaskResultAttrRe  = regexp.MustCompile(`(id|agent|status|duration)="([^"]*)"`)
)

// ompCompactionNode converts PiG's CompactionSummaryMessageComponent box
// (`[compaction]` + `Compacted from <N> tokens...`) into omp's native
// `section[omp.compaction]` divider (compaction-summary-message.ts:204-244).
func ompCompactionNode(id string, lines []string, _ bool, lookup func(int) (CompactionMeta, bool)) (tern.WireNode, bool) {
	tag, tagIdx, ok := ompBracketBoxHeader(lines)
	if !ok || tag != "compaction" {
		return tern.WireNode{}, false
	}
	headerIdx := -1
	headerLine := ""
	for i := tagIdx + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(stripANSI(lines[i]))
		if trimmed == "" {
			continue
		}
		headerIdx = i
		headerLine = trimmed
		break
	}
	if headerIdx < 0 {
		return tern.WireNode{}, false
	}
	m := ompCompactedFromRe.FindStringSubmatch(headerLine)
	if m == nil {
		return tern.WireNode{}, false
	}
	tokensBefore := parseCommaInt(m[1])
	isExpanded := m[2] == ""

	var meta CompactionMeta
	if lookup != nil {
		if found, ok := lookup(tokensBefore); ok {
			meta = found
		}
	}

	label := ompCompactionMethodLabels[strings.ToLower(strings.TrimSpace(meta.Method))]
	if label == "" {
		label = "Compacted"
	}
	headSpans := []any{map[string]any{"t": label, "s": "muted"}}
	if meta.TokensAfter > 0 && tokensBefore > 0 {
		headSpans = append(headSpans,
			map[string]any{"t": " · " + ompFormatNumber(tokensBefore), "s": "muted mono"},
			map[string]any{"t": " → ", "s": "dim"},
			map[string]any{"t": ompFormatNumber(meta.TokensAfter) + " tokens", "s": "muted mono"},
		)
	} else if tokensBefore > 0 {
		headSpans = append(headSpans,
			map[string]any{"t": " · " + ompFormatNumber(tokensBefore) + " tokens", "s": "muted mono"},
		)
	}
	if meta.Warning != "" {
		headSpans = append(headSpans, map[string]any{"t": " ⚠", "s": "warning"})
	}

	caption := "Summary of the earlier conversation"
	if meta.FrameCount > 0 {
		plural := "s"
		if meta.FrameCount == 1 {
			plural = ""
		}
		caption = fmt.Sprintf("Summary · %d snapcompact frame%s attached", meta.FrameCount, plural)
	}
	children := []tern.WireNode{
		{
			ID: id + ".caption",
			K:  tern.KindText,
			P: map[string]any{
				"role":  "omp.compaction.caption",
				"spans": []any{map[string]any{"t": caption, "s": "dim"}},
			},
		},
	}
	if meta.Warning != "" {
		children = append(children, ompNoticeRow(id+".warning", "warning", []any{map[string]any{"t": meta.Warning}}))
	}
	summary := strings.TrimSpace(meta.Summary)
	if summary == "" && isExpanded {
		summary = ompBoxBodyText(lines[headerIdx+1:])
	}
	if summary == "" {
		summary = "Compacted from " + m[1] + " tokens"
	}
	children = append(children, tern.WireNode{
		ID: id + ".summary",
		K:  tern.KindMd,
		P:  map[string]any{"text": summary},
	})

	return tern.WireNode{
		ID: id,
		K:  tern.KindSection,
		P: map[string]any{
			"role":        "omp.compaction",
			"head":        headSpans,
			"collapsible": true,
			"collapsed":   !isExpanded,
		},
		C: children,
	}, true
}

// ompBranchNode converts PiG's BranchSummaryMessageComponent box (`[branch]`)
// into omp's native `section[omp.branch]` divider
// (compaction-summary-message.ts:305-314).
func ompBranchNode(id string, lines []string, _ bool) (tern.WireNode, bool) {
	tag, tagIdx, ok := ompBracketBoxHeader(lines)
	if !ok || tag != "branch" {
		return tern.WireNode{}, false
	}
	headerIdx := -1
	headerLine := ""
	for i := tagIdx + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(stripANSI(lines[i]))
		if trimmed == "" {
			continue
		}
		headerIdx = i
		headerLine = trimmed
		break
	}
	if headerIdx < 0 {
		return tern.WireNode{}, false
	}
	var isExpanded bool
	switch {
	case ompBranchCollapsedRe.MatchString(headerLine):
		isExpanded = false
	case strings.EqualFold(headerLine, "Branch Summary"):
		isExpanded = true
	default:
		return tern.WireNode{}, false
	}
	summary := ""
	if isExpanded {
		summary = ompBoxBodyText(lines[headerIdx+1:])
	}
	if summary == "" {
		summary = "Branch summary"
	}
	return tern.WireNode{
		ID: id,
		K:  tern.KindSection,
		P: map[string]any{
			"role":        "omp.branch",
			"head":        []any{map[string]any{"t": "Branch summarized", "s": "muted"}},
			"collapsible": true,
			"collapsed":   !isExpanded,
		},
		C: []tern.WireNode{
			{
				ID: id + ".summary",
				K:  tern.KindMd,
				P:  map[string]any{"text": summary},
			},
		},
	}, true
}

type ompAsyncJobInfo struct {
	jobID    string
	kind     string
	duration string
	isError  bool
}

// ompAsyncResultNode converts PiG's `[async-result]` custom message box into
// omp's native `col[omp.status-block]` containing one `row[omp.notice.success]`
// (or `omp.notice.error`) per completed background job
// (transcript-render-helpers.ts:52-102, chrome/transcript-status.ts:43-67).
func ompAsyncResultNode(id string, lines []string) (tern.WireNode, bool) {
	tag, tagIdx, ok := ompBracketBoxHeader(lines)
	if !ok || tag != "async-result" {
		return tern.WireNode{}, false
	}
	bodyLines := ompPlainLinesTrimmed(lines[tagIdx+1:])
	jobs := parseOmpAsyncJobs(bodyLines)
	if len(jobs) == 0 {
		jobs = []ompAsyncJobInfo{{}}
	}
	rows := make([]tern.WireNode, 0, len(jobs))
	for i, job := range jobs {
		tone := "success"
		if job.isError {
			tone = "error"
		}
		spans := []any{map[string]any{"t": "Background job completed"}}
		if job.kind != "" {
			spans = append(spans,
				map[string]any{"t": " · ", "s": "dim"},
				map[string]any{"t": job.kind, "s": "dim"},
			)
		}
		if job.jobID != "" {
			spans = append(spans,
				map[string]any{"t": " · ", "s": "dim"},
				map[string]any{"t": job.jobID, "s": "dim"},
			)
		}
		if job.duration != "" {
			spans = append(spans,
				map[string]any{"t": " · ", "s": "dim"},
				map[string]any{"t": job.duration, "s": "dim"},
			)
		}
		rows = append(rows, ompNoticeRow(fmt.Sprintf("%s.%d", id, i), tone, spans))
	}
	return tern.WireNode{
		ID: id,
		K:  tern.KindCol,
		P:  map[string]any{"role": "omp.status-block"},
		C:  rows,
	}, true
}

// parseOmpAsyncJobs extracts completed background jobs from the plain body
// lines of PiG's `[async-result]` box (`internal/jobs/delivery.go:705-760`).
func parseOmpAsyncJobs(lines []string) []ompAsyncJobInfo {
	// PiG's CustomMessageComponent renders markdown inside a width-bounded Box,
	// so long lines like `Background job <id> (long label) has completed.` or
	// `<task-result id="..." agent="..." status="..." duration="...">` can wrap
	// across multiple lines. Join consecutive wrapped lines before matching.
	unwrapped := unwrapAsyncHeaderLines(lines)
	type section struct {
		jobID string
		label string
		lines []string
	}
	var sections []section
	cur := -1
	for _, line := range unwrapped {
		if m := ompAsyncMultiJobRe.FindStringSubmatch(line); m != nil {
			sections = append(sections, section{jobID: m[1], label: m[2]})
			cur = len(sections) - 1
			continue
		}
		if len(sections) == 0 {
			if m := ompAsyncSingleJobRe.FindStringSubmatch(line); m != nil {
				sections = append(sections, section{jobID: m[1], label: m[2]})
				cur = 0
				continue
			}
		}
		if cur >= 0 {
			sections[cur].lines = append(sections[cur].lines, line)
		}
	}
	if len(sections) == 0 {
		// Fallback: scan for bare <task-result ...> tags if header was absent.
		joined := strings.Join(unwrapped, "\n")
		if strings.Contains(joined, "<task-result") {
			sections = append(sections, section{lines: unwrapped})
		}
	}
	out := make([]ompAsyncJobInfo, 0, len(sections))
	for _, sec := range sections {
		info := ompAsyncJobInfo{jobID: sec.jobID}
		isAgent := false
		isBash := false
		for _, l := range sec.lines {
			if strings.Contains(l, "<task-result") {
				isAgent = true
				for _, attr := range ompTaskResultAttrRe.FindAllStringSubmatch(l, -1) {
					switch attr[1] {
					case "id":
						if info.jobID == "" {
							info.jobID = attr[2]
						}
					case "duration":
						if info.duration == "" {
							info.duration = attr[2]
						}
					case "status":
						st := strings.ToLower(strings.TrimSpace(attr[2]))
						if st == "error" || st == "failed" {
							info.isError = true
						}
					}
				}
			}
			if strings.Contains(l, "agent://") || strings.Contains(l, "history://") {
				isAgent = true
			}
			lower := strings.ToLower(l)
			if strings.Contains(lower, `status="error"`) || strings.Contains(lower, `status="failed"`) ||
				strings.HasPrefix(lower, "error:") || strings.Contains(lower, "exit code:") && !strings.Contains(lower, "exit code: 0") {
				info.isError = true
			}
		}
		if !isAgent && strings.HasPrefix(info.jobID, "bg_") {
			isBash = true
		}
		switch {
		case isAgent:
			info.kind = "agent"
		case isBash:
			info.kind = "bash"
		case info.jobID != "":
			info.kind = "job"
		}
		if info.jobID != "" || info.kind != "" {
			out = append(out, info)
		}
	}
	return out
}

// unwrapAsyncHeaderLines stitches wrapped `Background job ...`, `── Job ...`,
// and `<task-result ...>` lines back onto one logical line so regex matching
// succeeds regardless of narrow terminal widths.
func unwrapAsyncHeaderLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		switch {
		case strings.HasPrefix(line, "Background job ") && !strings.HasSuffix(line, "has completed.") && !strings.HasSuffix(line, "has completed"):
			for i+1 < len(lines) && !strings.HasPrefix(lines[i+1], "Resume ") && !strings.HasPrefix(lines[i+1], "<") {
				i++
				line += " " + lines[i]
				if strings.HasSuffix(line, "has completed.") || strings.HasSuffix(line, "has completed") {
					break
				}
			}
		case strings.HasPrefix(line, "──") && !strings.HasSuffix(line, "──"):
			for i+1 < len(lines) && !strings.HasPrefix(lines[i+1], "<") {
				i++
				line += " " + lines[i]
				if strings.HasSuffix(line, "──") {
					break
				}
			}
		case strings.HasPrefix(line, "<task-result") && !strings.Contains(line, ">"):
			for i+1 < len(lines) {
				i++
				line += " " + lines[i]
				if strings.Contains(line, ">") {
					break
				}
			}
		}
		out = append(out, line)
	}
	return out
}

// ompCustomMessageNode converts other PiG CustomMessageComponent purple-bg
// boxes (`[<customType>]` + body) into omp's native `card[omp.custom]`
// (chrome/message-frame.ts:112-123). Requires an ANSI background SGR sequence
// (`\x1b[48;`) and a non-empty body so plain text starting with `[tag]` never
// false-positives.
func ompCustomMessageNode(id string, lines []string) (tern.WireNode, bool) {
	tag, tagIdx, ok := ompBracketBoxHeader(lines)
	if !ok {
		return tern.WireNode{}, false
	}
	if !ompCustomTagRe.MatchString(tag) || ompDiscoverySectionHeadings["["+tag+"]"] {
		return tern.WireNode{}, false
	}
	if !ompLinesHaveANSIBg(lines) {
		return tern.WireNode{}, false
	}
	body := ompStripSystemNoticeEnvelope(ompBoxBodyText(lines[tagIdx+1:]))
	if body == "" {
		return tern.WireNode{}, false
	}
	return tern.WireNode{
		ID: id,
		K:  tern.KindCard,
		P: map[string]any{
			"role": "omp.custom",
			"head": []any{map[string]any{"t": tag, "s": "customMessageLabel strong"}},
		},
		C: []tern.WireNode{
			{
				ID: id + ".md",
				K:  tern.KindMd,
				P:  map[string]any{"text": body},
			},
		},
	}, true
}

// ompNoticeRow builds omp's `row[omp.notice.<tone>]` notice row
// (chrome/transcript-notice.ts:46-56).
func ompNoticeRow(id, tone string, spans []any) tern.WireNode {
	iconProps := map[string]any{"name": tone}
	if tone != "info" {
		iconProps["tone"] = tone
	}
	switch tone {
	case "warning":
		iconProps["aria"] = "Warning"
	case "error":
		iconProps["aria"] = "Error"
	}
	return tern.WireNode{
		ID: id,
		K:  tern.KindRow,
		P: map[string]any{
			"role":  "omp.notice." + tone,
			"tone":  tone,
			"gap":   "sm",
			"align": "start",
		},
		C: []tern.WireNode{
			{ID: id + ".icon", K: tern.KindIcon, P: iconProps},
			{
				ID: id + ".text",
				K:  tern.KindText,
				P: map[string]any{
					"spans":  spans,
					"wrap":   "word",
					"grow":   1,
					"shrink": 1,
				},
			},
		},
	}
}

// ompBracketBoxHeader finds the first non-blank plain line in lines and
// returns its bracketed tag (`[tag]` -> `tag`) and line index.
func ompBracketBoxHeader(lines []string) (tag string, idx int, ok bool) {
	for i, raw := range lines {
		trimmed := strings.TrimSpace(stripANSI(raw))
		if trimmed == "" {
			continue
		}
		if len(trimmed) >= 3 && trimmed[0] == '[' && trimmed[len(trimmed)-1] == ']' {
			return trimmed[1 : len(trimmed)-1], i, true
		}
		return "", -1, false
	}
	return "", -1, false
}

// ompLinesHaveANSIBg reports whether any line carries an ANSI 256-color or
// truecolor background SGR sequence (`\x1b[48;`), as painted by PiG's
// CustomMessageComponent box.
func ompLinesHaveANSIBg(lines []string) bool {
	for _, raw := range lines {
		if strings.Contains(raw, "\x1b[48;") {
			return true
		}
	}
	return false
}

// ompPlainLinesTrimmed strips ANSI escapes and surrounding whitespace from each
// line, skipping empty lines.
func ompPlainLinesTrimmed(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, raw := range lines {
		trimmed := strings.TrimSpace(stripANSI(raw))
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// ompBoxBodyText strips ANSI escapes and PiG's 1-cell Box horizontal padding
// while preserving internal blank lines and relative indentation.
func ompBoxBodyText(lines []string) string {
	plain := make([]string, len(lines))
	start, end := -1, -1
	for i, raw := range lines {
		s := strings.TrimRight(stripANSI(raw), " \t")
		plain[i] = s
		if strings.TrimSpace(s) != "" {
			if start < 0 {
				start = i
			}
			end = i
		}
	}
	if start < 0 {
		return ""
	}
	slice := plain[start : end+1]
	// Dedent common leading spaces (PiG's Box outputPad=1 indents every row).
	minIndent := -1
	for _, s := range slice {
		if strings.TrimSpace(s) == "" {
			continue
		}
		n := 0
		for n < len(s) && s[n] == ' ' {
			n++
		}
		if minIndent < 0 || n < minIndent {
			minIndent = n
		}
	}
	if minIndent > 0 {
		for i, s := range slice {
			if len(s) >= minIndent {
				slice[i] = s[minIndent:]
			} else {
				slice[i] = ""
			}
		}
	}
	return strings.TrimSpace(strings.Join(slice, "\n"))
}

// ompStripSystemNoticeEnvelope removes a wrapping `<system-notice>` /
// `</system-notice>` tag pair if present on the first/last lines.
func ompStripSystemNoticeEnvelope(body string) string {
	if body == "" {
		return ""
	}
	lines := strings.Split(body, "\n")
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "<system-notice>" {
		lines = lines[1:]
	}
	if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "</system-notice>" {
		lines = lines[:len(lines)-1]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// ompWelcomeTips holds omp's TSP tips from packages/tui/src/prompt/tips.txt
// filtered to features and commands registered in oh-my-pig (no /shake, /tan,
// /omfg, /collab, /advisor, /cleanse, or omp CLI subcommands).
// Key placeholders (`{key:...}`) are expanded with omp's key-hint icons and
// split into `spans` matching `packages/tui/src/native/icons.ts:40-70`.
var ompWelcomeTips = []string{
	`Tired of typing "keep going"? Just send a '.'`,
	`Type / at the start of your prompt to browse every command`,
	`Start your prompt with ! to run it as bash — !! runs it without adding the output to context`,
	`You can /btw to ask a side question`,
	`Try starting your prompt with a ->, and writing a list (1. Do X, 2. Do Y)`,
	`Use /plan to plan before executing, or /goal to set an autonomous session objective`,
	`Use /loop to re-submit your prompt after every yield until a condition passes`,
	`Use /force read to pin the next turn to one specific tool`,
	`Use /copy code to grab the last code block, or /copy cmd for the last command`,
	`Use /switch to switch models, or /effort to adjust reasoning effort`,
	`Use /jobs to inspect or cancel background tasks and services`,
	`Use /branch or /rewind to fork from an earlier turn`,
	`Use /handoff to compact with a handoff summary`,
	`Press {key:shift+tab} to cycle through reasoning effort levels`,
}

var ompTipKeyPlaceholderRe = regexp.MustCompile(`\{key:([^}]+)\}`)

// ompExpandTipKey expands `{key:...}` placeholders using omp's macOS/generic
// nerd key glyphs (packages/tui/src/theme/symbols.ts:1179-1206).
func ompExpandTipKey(tip string) string {
	return ompTipKeyPlaceholderRe.ReplaceAllStringFunc(tip, func(match string) string {
		sub := ompTipKeyPlaceholderRe.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		switch strings.ToLower(strings.TrimSpace(sub[1])) {
		case "shift+tab":
			return "\U000f0636 \U000f0312"
		case "alt+p":
			return "\U000f0635 P"
		case "ctrl+p":
			return "\U000f0634 P"
		case "ctrl+r":
			return "\U000f0634 R"
		case "ctrl+d":
			return "\U000f0634 D"
		case "escape", "esc":
			return "\U000f12b7"
		default:
			return match
		}
	})
}

func isOmpPUA(r rune) bool {
	return (r >= 0xE000 && r <= 0xF8FF) || (r >= 0xF0000 && r <= 0xFFFFD) || (r >= 0x100000 && r <= 0x10FFFD)
}

// ompTipTextProps returns `text` props for a welcome tip, splitting any Private
// Use Area icon runs into `{t, s:"icon"}` spans and trimming adjacent padding
// spaces (upstream: packages/tui/src/native/icons.ts:40-70). Plain tips without
// PUA glyphs emit `"text": tip`.
func ompTipTextProps(raw string) map[string]any {
	expanded := ompExpandTipKey(raw)
	hasPUA := false
	for _, r := range expanded {
		if isOmpPUA(r) {
			hasPUA = true
			break
		}
	}
	props := map[string]any{
		"wrap": "word",
		"role": "omp.welcome.tip-text",
	}
	if !hasPUA {
		props["text"] = expanded
		return props
	}
	type piece struct {
		text string
		icon bool
	}
	var pieces []piece
	var buf strings.Builder
	curIcon := false
	flush := func() {
		if buf.Len() > 0 {
			pieces = append(pieces, piece{text: buf.String(), icon: curIcon})
			buf.Reset()
		}
	}
	for _, r := range expanded {
		ic := isOmpPUA(r)
		if buf.Len() > 0 && ic != curIcon {
			flush()
		}
		curIcon = ic
		buf.WriteRune(r)
	}
	flush()
	for i := range pieces {
		if !pieces[i].icon {
			continue
		}
		if i > 0 && !pieces[i-1].icon {
			pieces[i-1].text = strings.TrimSuffix(pieces[i-1].text, " ")
		}
		if i+1 < len(pieces) && !pieces[i+1].icon {
			pieces[i+1].text = strings.TrimPrefix(pieces[i+1].text, " ")
		}
	}
	spans := make([]any, 0, len(pieces))
	for _, p := range pieces {
		if p.text == "" {
			continue
		}
		if p.icon {
			spans = append(spans, map[string]any{"t": p.text, "s": "icon"})
		} else {
			spans = append(spans, map[string]any{"t": p.text})
		}
	}
	props["spans"] = spans
	return props
}

// isPiGHeaderLines reports whether a transcript fe.Lines node with a Click area
// is PiG's built-in startup header (clickable pig sprite beside version & key help).
func isPiGHeaderLines(lines []string, click *fe.Area) bool {
	if click == nil || len(lines) == 0 {
		return false
	}
	joined := stripANSI(strings.Join(lines, "\n"))
	return strings.Contains(joined, "ctrl+o") &&
		(strings.Contains(joined, "interrupt") || strings.Contains(joined, "startup help"))
}

// ompWelcomeNode builds omp's native `card[omp.welcome]` (lockup + tip) for the
// oh-my-pig Piglet in Tern, replacing PiG's ANSI pig-sprite header under ompRoles.
// Branding: the wordmark and the version are the derived Piglet's
// (Options.Name, Options.Version); without them, the host's name and version.
// upstream: packages/tui/src/prompt/welcome.ts:252-270, 407-433
func (s *session) ompWelcomeNode(id string, lines []string) tern.WireNode {
	name := strings.TrimSpace(s.pigletName)
	if name == "" {
		name = strings.TrimSpace(s.env.AppName)
	}
	if name == "" {
		name = "pig"
	}
	version := strings.TrimSpace(s.pigletVersion)
	if version == "" {
		version = strings.TrimSpace(s.env.Version)
	}
	if version != "" && !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	tipIdx := s.welcomeTipIdx
	if tipIdx < 0 || tipIdx >= len(ompWelcomeTips) {
		tipIdx = 0
	}
	tip := ompWelcomeTips[tipIdx]

	return tern.WireNode{
		ID: id,
		K:  tern.KindCard,
		P:  map[string]any{"role": "omp.welcome"},
		C: []tern.WireNode{
			{
				ID: id + ".lockup",
				K:  tern.KindRow,
				P: map[string]any{
					"align": "center",
					"gap":   "md",
					"role":  "omp.welcome.lockup",
				},
				C: []tern.WireNode{
					{
						ID: id + ".lockup/logo",
						K:  tern.KindImage,
						P: func() map[string]any {
							blobID := s.ompLogoBlob()
							s.welcomeEntryID = strings.TrimPrefix(id, "n.")
							s.welcomeLogoNodeID = id + ".lockup/logo"
							s.welcomeLogoBlob = blobID
							return map[string]any{
								"blob": blobID,
								"alt":  name,
								"w":    ompLogoWidthPx,
								"h":    ompLogoHeightPx,
								"role": "omp.welcome.logo",
							}
						}(),
					},
					{
						ID: id + ".lockup/mark",
						K:  tern.KindCol,
						P:  map[string]any{"role": "omp.welcome.mark"},
						C: []tern.WireNode{
							{
								ID: id + ".lockup/mark/omp.welcome.wordmark",
								K:  tern.KindText,
								P: map[string]any{
									"wrap":  "none",
									"role":  "omp.welcome.wordmark",
									"spans": []any{map[string]any{"t": name, "s": "strong"}},
								},
							},
							{
								ID: id + ".lockup/mark/omp.welcome.version",
								K:  tern.KindText,
								P: map[string]any{
									"wrap":  "none",
									"role":  "omp.welcome.version",
									"spans": []any{map[string]any{"t": version, "s": "dim mono"}},
								},
							},
						},
					},
				},
			},
			{
				ID: id + ".tip",
				K:  tern.KindRow,
				P: map[string]any{
					"gap":   "sm",
					"align": "start",
					"role":  "omp.welcome.tip",
				},
				C: []tern.WireNode{
					{
						ID: id + ".tip/0",
						K:  tern.KindIcon,
						P: map[string]any{
							"name": "lightbulb",
							"role": "omp.welcome.tip-icon",
						},
					},
					{
						ID: id + ".tip/1",
						K:  tern.KindText,
						P:  ompTipTextProps(tip),
					},
				},
			},
		},
	}
}

// ompReadGroupMinRun is the minimum number of consecutive compact filesystem
// `omp.tool.read` cards coalesced into a grouped ReadToolGroupComponent node
// (`Read <N> files` with `row[omp.tool.file]` children). Runs of 1 or 2 remain
// individual inline `omp.tool.read` nodes so single-read and paired-read flows
// keep their exact standalone shape.
// upstream: packages/tui/src/chat/read-tool-group.ts:800-916
const ompReadGroupMinRun = 3

// isOmpCompactFilesystemRead reports whether node is a compact filesystem
// `read` tool card eligible for ReadToolGroupComponent coalescing:
//   - ToolCard with Name == "read", no Diff, no Images
//   - filesystem/non-internal-URL target (matches readArgsCollapseIntoGroup)
//   - no non-empty ResultView body (empty `omp.tool.read` container is fine)
func (s *session) isOmpCompactFilesystemRead(node fe.Node) (fe.ToolCard, bool) {
	tool, ok := node.(fe.ToolCard)
	if !ok || tool.Name != "read" || tool.Diff != nil || len(tool.Images) > 0 {
		return fe.ToolCard{}, false
	}
	raw := strings.TrimSpace(firstStringArg(tool.Arguments, "path", "file_path"))
	if raw == "" || strings.Contains(raw, "://") {
		return fe.ToolCard{}, false
	}
	hasView := validView(tool.ResultView, tool.Result) && len(tool.Result) > 0
	emptyReadView := tool.ResultView != nil &&
		tool.ResultView.Root.ID == "omp.tool.read" && len(tool.ResultView.Root.Children) == 0
	if hasView && !emptyReadView {
		return fe.ToolCard{}, false
	}
	return tool, true
}

// ompReadStatusRank mirrors READ_STATUS_RANK in read-tool-group.ts:219-224.
func ompReadStatusRank(st fe.ToolStatus) int {
	switch st {
	case fe.ToolError:
		return 3
	case fe.ToolCancelled:
		return 2
	case fe.ToolPending, fe.ToolRunning:
		return 1
	default:
		return 0
	}
}

func ompReadGroupStatus(tools []fe.ToolCard) string {
	rank := 0
	for _, t := range tools {
		if r := ompReadStatusRank(t.Status); r > rank {
			rank = r
		}
	}
	switch rank {
	case 3:
		return "error"
	case 2:
		return "cancelled"
	case 1:
		return "running"
	default:
		return "done"
	}
}

func ompReadErrorText(tool fe.ToolCard) string {
	raw := tool.Output
	if raw == "" {
		resultLines, _ := stripOmpHeadResultLines(tool.ResultView, tool.Result)
		if rendered := linesText(resultLines); !blank(rendered) {
			raw = rendered
		}
	}
	full := strings.TrimSpace(stripANSI(raw))
	full = strings.TrimPrefix(full, "Error: ")
	if full == "" {
		return "Read failed"
	}
	return full
}

// ompReadGroupNode builds omp's grouped `tool[omp.tool.read]` node for a run of
// consecutive compact filesystem reads (`target: "<N> files"`, `targetKind: "text"`,
// `frame: "inline"`, `collapsible: false`, with one `row[omp.tool.file]` child per
// read entry; upstream: packages/tui/src/chat/read-tool-group.ts:826-906).
func (s *session) ompReadGroupNode(leaderID string, tools []fe.ToolCard) tern.WireNode {
	status := ompReadGroupStatus(tools)
	props := map[string]any{
		"role":        "omp.tool.read",
		"name":        "read",
		"title":       "Read",
		"target":      fmt.Sprintf("%d files", len(tools)),
		"targetKind":  "text",
		"status":      status,
		"frame":       "inline",
		"collapsible": false,
	}
	if status == "error" {
		props["tone"] = "error"
	}
	rows := make([]tern.WireNode, 0, len(tools))
	for i, tool := range tools {
		rowID := fmt.Sprintf("%s.r%d", leaderID, i)
		rows = append(rows, s.ompReadGroupFileRow(rowID, tool))
	}
	return tern.WireNode{
		ID: leaderID,
		K:  tern.KindTool,
		P:  props,
		C:  rows,
	}
}

// ompReadGroupFileRow mirrors ReadToolGroupComponent.#nativeFileRow + fileRow
// (packages/tui/src/chat/read-tool-group.ts:884-906, tools/native-view.ts:73-105).
func (s *session) ompReadGroupFileRow(rowID string, tool fe.ToolCard) tern.WireNode {
	raw := strings.TrimSpace(firstStringArg(tool.Arguments, "path", "file_path"))
	base, sel := splitOmpReadPath(raw)
	if sel == "" {
		if rangeStr := formatOffsetLimit(tool.Arguments); rangeStr != "" && base != "" {
			sel = rangeStr
		}
	}
	displayBase := base
	if displayBase == "" {
		displayBase = raw
	}
	var tone string
	switch tool.Status {
	case fe.ToolError:
		tone = "error"
	case fe.ToolPending, fe.ToolRunning:
		tone = "pending"
	}
	iconName := "file"
	if strings.HasSuffix(displayBase, "/") {
		iconName = "folder"
	}
	if tool.Status == fe.ToolError {
		iconName = "x-circle"
	}
	iconProps := map[string]any{"name": iconName}
	if tone != "" {
		iconProps["tone"] = tone
	}
	href := ""
	if tool.Status != fe.ToolError && base != "" {
		href = s.ompFileHref(base)
	}
	spans := ompFileRowSpans(displayBase, href)
	if sel != "" {
		spans = append(spans, map[string]any{"t": ":" + sel, "s": "muted"})
	}
	title := displayBase
	var errFirst string
	if tool.Status == fe.ToolError {
		fullErr := ompReadErrorText(tool)
		title = fullErr
		errFirst, _, _ = strings.Cut(fullErr, "\n")
		errFirst = strings.TrimSpace(errFirst)
	}
	children := []tern.WireNode{
		{ID: rowID + ".icon", K: tern.KindIcon, P: iconProps},
		{
			ID: rowID + ".path",
			K:  tern.KindText,
			P: map[string]any{
				"lines":    1,
				"truncate": "middle",
				"shrink":   1,
				"title":    title,
				"spans":    spans,
			},
		},
	}
	if errFirst != "" {
		children = append(children, tern.WireNode{
			ID: rowID + ".detail",
			K:  tern.KindText,
			P: map[string]any{
				"lines":    1,
				"truncate": "end",
				"shrink":   1,
				"spans":    []any{map[string]any{"t": "— " + errFirst, "s": "error"}},
			},
		})
	}
	rowProps := map[string]any{"role": "omp.tool.file"}
	if tone != "" {
		rowProps["tone"] = tone
	}
	return tern.WireNode{
		ID: rowID,
		K:  tern.KindRow,
		P:  rowProps,
		C:  children,
	}
}

// syncOmpReadGroups reconciles all runs of >= ompReadGroupMinRun consecutive
// compact filesystem `omp.tool.read` entries in s.order. When a run reaches
// ompReadGroupMinRun, the first live entry hosts the grouped `tool[omp.tool.read]`
// node (`target: "<N> files"`) and trailing run members are collapsed to hidden
// placeholders so every PiG node keeps a stable TSP ID in `main`. If a run
// shrinks below ompReadGroupMinRun, each surviving entry is restored to its
// standalone inline `omp.tool.read` node.
func (s *session) syncOmpReadGroups() {
	if !s.ompRoles || len(s.order) == 0 {
		return
	}
	type readRun struct {
		leaderIdx int
		indices   []int
		entries   []*mainEntry
		tools     []fe.ToolCard
	}

	// Identify all consecutive runs of live compact filesystem read cards.
	var runs []readRun
	for i := 0; i < len(s.order); {
		pigID := strings.TrimPrefix(s.order[i], "n.")
		e := s.sent[pigID]
		if e == nil || e.gone {
			i++
			continue
		}
		tool, ok := s.isOmpCompactFilesystemRead(e.node)
		if !ok {
			i++
			continue
		}
		run := readRun{
			leaderIdx: i,
			indices:   []int{i},
			entries:   []*mainEntry{e},
			tools:     []fe.ToolCard{tool},
		}
		j := i + 1
		for j < len(s.order) {
			npig := strings.TrimPrefix(s.order[j], "n.")
			ne := s.sent[npig]
			if ne == nil || ne.gone {
				break
			}
			ntool, nok := s.isOmpCompactFilesystemRead(ne.node)
			if !nok {
				break
			}
			run.indices = append(run.indices, j)
			run.entries = append(run.entries, ne)
			run.tools = append(run.tools, ntool)
			j++
		}
		if len(run.entries) >= ompReadGroupMinRun {
			runs = append(runs, run)
		}
		i = j
	}

	desiredHost := make(map[string]string, len(runs)*3)
	runByLeader := make(map[string]readRun, len(runs))
	for _, run := range runs {
		leaderID := run.entries[0].id
		runByLeader[leaderID] = run
		for _, member := range run.entries {
			desiredHost[member.id] = leaderID
		}
	}

	// 1. Restore any entry that was previously in a group but is no longer
	//    grouped (e.g. a run shrank below ompReadGroupMinRun after Remove or split).
	for idx, tspID := range s.order {
		e := s.sent[strings.TrimPrefix(tspID, "n.")]
		if e == nil || e.gone || e.readGroupHostID == "" {
			continue
		}
		if desiredHost[e.id] == "" {
			e.readGroupHostID = ""
			e.readGroupMembers = nil
			e.readGroupLastWire = nil
			e.settled = false
			before := s.liveFrom(idx + 1)
			s.queue(tern.OpDel(e.id), tern.OpAdd("main", before, s.mainNodeFor(e, e.node)))
			s.settle(e, idx)
		}
	}

	// 2. Apply each active group run: update leader with grouped node and hide followers.
	for _, run := range runs {
		leader := run.entries[0]
		wire := s.ompReadGroupNode(leader.id, run.tools)
		memberIDs := make([]string, len(run.entries))
		for k, m := range run.entries {
			memberIDs[k] = m.id
		}
		if leader.readGroupHostID == leader.id && leader.readGroupLastWire != nil {
			if ops, ok := patchNode(*leader.readGroupLastWire, wire); ok {
				if len(ops) > 0 {
					s.queue(ops...)
					leader.settled = false
				}
			} else {
				before := s.liveFrom(run.leaderIdx + 1)
				s.queue(tern.OpDel(leader.id), tern.OpAdd("main", before, wire))
				leader.settled = false
			}
		} else {
			before := s.liveFrom(run.leaderIdx + 1)
			s.queue(tern.OpDel(leader.id), tern.OpAdd("main", before, wire))
			leader.settled = false
		}
		wireCopy := wire
		leader.readGroupHostID = leader.id
		leader.readGroupMembers = memberIDs
		leader.readGroupLastWire = &wireCopy
		allFinal := true
		for _, m := range run.entries {
			if !final(m.node, false) {
				allFinal = false
				break
			}
		}
		if allFinal && !leader.settled {
			leader.settled = true
			s.queue(tern.OpSettle(leader.id))
		}

		for k := 1; k < len(run.entries); k++ {
			follower := run.entries[k]
			fIdx := run.indices[k]
			if follower.readGroupHostID == leader.id {
				continue
			}
			follower.readGroupHostID = leader.id
			follower.readGroupMembers = nil
			follower.readGroupLastWire = nil
			follower.settled = false
			before := s.liveFrom(fIdx + 1)
			hidden := tern.WireNode{ID: follower.id, K: tern.KindSpacer, P: map[string]any{"hidden": true}}
			s.queue(tern.OpDel(follower.id), tern.OpAdd("main", before, hidden), tern.OpSettle(follower.id))
			follower.settled = true
		}
	}
}
