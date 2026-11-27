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

// ompLinesNode recognizes PiG's assistant error/abort lines and horizontal
// divider lines under ompRoles, returning their omp TSP nodes.
func ompLinesNode(id, pigID string, lines []string, expanded bool) (tern.WireNode, bool) {
	if node, ok := ompAssistantErrorNode(id, pigID, lines, expanded); ok {
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

// isBlankMainLines reports whether node is an all-whitespace fe.Lines spacer
// (e.g. `tui.NewSpacer(1)` between loaded-resource sections).
func isBlankMainLines(node fe.Node) bool {
	lines, ok := node.(fe.Lines)
	if !ok || lines.Click != nil || validView(lines.View, lines.Lines) {
		return false
	}
	return blank(linesText(lines.Lines))
}

// syncOmpDiscoveryDividers keeps one top `rule[omp.divider]` (`omp.discovery.top`)
// and one bottom `rule[omp.divider]` (`omp.discovery.bottom`) framing PiG's
// contiguous startup loaded-resources sections in `main`.
func (s *session) syncOmpDiscoveryDividers() {
	if !s.ompRoles {
		return
	}
	firstIdx, lastIdx := -1, -1
	for i, id := range s.order {
		e := s.sent[strings.TrimPrefix(id, "n.")]
		if e == nil || e.gone || !e.isDiscovery {
			continue
		}
		if firstIdx < 0 {
			firstIdx = i
		}
		lastIdx = i
	}
	if firstIdx < 0 {
		if s.discoveryTopID != "" {
			s.queue(tern.OpDel(s.discoveryTopID))
			s.discoveryTopID = ""
			s.discoveryTopBefore = ""
		}
		if s.discoveryBotID != "" {
			s.queue(tern.OpDel(s.discoveryBotID))
			s.discoveryBotID = ""
			s.discoveryBotBefore = ""
			s.discoveryLastID = ""
		}
		return
	}

	topBefore := s.liveFrom(firstIdx)
	if s.discoveryTopID == "" || s.discoveryTopBefore != topBefore {
		if s.discoveryTopID != "" {
			s.queue(tern.OpDel(s.discoveryTopID))
		}
		topID := "omp.discovery.top"
		rule := tern.WireNode{ID: topID, K: tern.KindRule, P: map[string]any{"role": "omp.divider"}}
		s.queue(tern.OpAdd("main", topBefore, rule), tern.OpSettle(topID))
		s.discoveryTopID = topID
		s.discoveryTopBefore = topBefore
	}

	// Place bottom divider after the last discovery section (skipping trailing
	// blank spacers that belong to the loadedResourcesContainer). Track both
	// the last discovery node ID and the anchor `before` ID so `/reload`
	// (which clears and re-inserts discovery nodes before an unchanged trailing
	// node) re-places the bottom divider after the newly inserted sections.
	lastDiscoveryID := s.order[lastIdx]
	afterIdx := lastIdx + 1
	for afterIdx < len(s.order) {
		e := s.sent[strings.TrimPrefix(s.order[afterIdx], "n.")]
		if e == nil || !isBlankMainLines(e.node) {
			break
		}
		afterIdx++
	}
	botBefore := s.liveFrom(afterIdx)
	if s.discoveryBotID == "" || s.discoveryBotBefore != botBefore || s.discoveryLastID != lastDiscoveryID {
		if s.discoveryBotID != "" {
			s.queue(tern.OpDel(s.discoveryBotID))
		}
		botID := "omp.discovery.bottom"
		rule := tern.WireNode{ID: botID, K: tern.KindRule, P: map[string]any{"role": "omp.divider"}}
		s.queue(tern.OpAdd("main", botBefore, rule), tern.OpSettle(botID))
		s.discoveryBotID = botID
		s.discoveryBotBefore = botBefore
		s.discoveryLastID = lastDiscoveryID
	}
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
