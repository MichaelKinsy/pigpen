package frontend

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/stencil-hq/tern-sdk/go/tern"
	"github.com/stencil-hq/tern-sdk/go/tern/ui"
)

// ompAutocompleteState holds the parsed native autocomplete list extracted from
// PiG's `dock.below` ANSI lines when `ompRoles` is active.
//
// Upstream:
//   - packages/tui/src/components/editor.ts:1999-2013 (`overlay[omp.autocomplete]`)
//   - packages/tui/src/components/select-list.ts:298-380 (`col[omp.select] > list > item`)
type ompAutocompleteState struct {
	items       []ompAutocompleteItem
	selectedIdx int
	status      string // e.g. "(1/96)"
	mark        string // typed match prefix without sigil, from last "/" on
	prefixKind  byte   // '/' or '@'
}

type ompAutocompleteItem struct {
	label       string
	description string
	icon        ui.IconName
	selected    bool
}

func (a *ompAutocompleteState) equal(b *ompAutocompleteState) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.selectedIdx == b.selectedIdx &&
		a.status == b.status &&
		a.mark == b.mark &&
		a.prefixKind == b.prefixKind &&
		slices.Equal(a.items, b.items)
}

var (
	// ompAutocompleteScrollRe matches PiG's SelectList scroll footer `"  (1/96)"`.
	ompAutocompleteScrollRe = regexp.MustCompile(`^\((\d+)/(\d+)\)$`)
	// ompAutocompleteColGapRe splits a SelectList row into primary label and description
	// separated by 2+ ASCII spaces (PiG `tui/select_list.go:278-288`, `selectListPrimaryColumnGap = 2`).
	ompAutocompleteColGapRe = regexp.MustCompile(`^(\S+)\s{2,}(.+)$`)
)

// ompSlashCommandIcons maps omp built-in slash command names to their Tern icon
// names (extracted from `packages/coding-agent/src/slash-commands/builtin-*.ts`
// and `modes/interactive-mode.ts:3300`).
var ompSlashCommandIcons = map[string]ui.IconName{
	"add-dir":          "folderPlus",
	"advisor":          "advisor",
	"agents":           "agents",
	"browser":          "globe",
	"btw":              "question",
	"changelog":        "news",
	"cleanse":          "stethoscope",
	"clear":            "eraser",
	"collab":           "broadcast",
	"commit":           "branch",
	"compact":          "compress",
	"computer":         "computer",
	"context":          "context",
	"copy":             "copy",
	"debug":            "bug",
	"delete":           "trash",
	"dump":             "clipboard",
	"effort":           "gauge",
	"export":           "export",
	"extended-context": "expand",
	"fast":             "fast",
	"force":            "hammer",
	"forget-approvals": "trash",
	"fork":             "branch",
	"fresh":            "restart",
	"git":              "branch",
	"goal":             "goal",
	"guided-goal":      "compass",
	"handoff":          "handoff",
	"help":             "help",
	"hotkeys":          "keyboard",
	"jobs":             "jobs",
	"join":             "signIn",
	"leave":            "signOut",
	"live":             "voice",
	"local":            "pencil",
	"login":            "signIn",
	"logout":           "signOut",
	"loop":             "loop",
	"marketplace":      "cart",
	"mcp":              "mcp",
	"memory":           "memory",
	"model":            "model",
	"modelpreset":      "model",
	"move":             "folderMove",
	"new":              "plus",
	"omfg":             "rule",
	"open":             "globe",
	"pause":            "pause",
	"pin":              "pin",
	"plan":             "plan",
	"plan-review":      "plan",
	"prewalk":          "prewalk",
	"queue":            "inbox",
	"quit":             "power",
	"ratchet":          "loop",
	"record":           "export",
	"reload":           "restart",
	"reload-plugins":   "restart",
	"remove-dir":       "folderMinus",
	"rename":           "pencil",
	"restart":          "restart",
	"resume":           "history",
	"retry":            "redo",
	"security":         "shield",
	"session":          "session",
	"settings":         "settings",
	"shake":            "vibrate",
	"share":            "share",
	"skillful":         "compass",
	"skills":           "skill",
	"slow":             "fast",
	"sprite":           "image",
	"ssh":              "host",
	"stats":            "stats",
	"switch":           "swap",
	"tan":              "rocket",
	"theme":            "palette",
	"thinking":         "gauge",
	"todo":             "todo",
	"tools":            "tools",
	"trace":            "stats",
	"tree":             "tree",
	"unpin":            "pin",
	"usage":            "gauge",
	"vibe":             "wave",
}

// ompAutocompleteIcon returns the Tern icon name for a parsed autocomplete item.
func ompAutocompleteIcon(prefixKind byte, label string) ui.IconName {
	if prefixKind == '@' {
		if strings.HasSuffix(label, "/") {
			return "folder"
		}
		return "file"
	}
	// Strip leading "/" if present and take base command name before space or ":".
	cmd := strings.TrimPrefix(label, "/")
	if sp := strings.IndexByte(cmd, ' '); sp >= 0 {
		cmd = cmd[:sp]
	}
	if strings.HasPrefix(cmd, "skill:") {
		return "skill"
	}
	if icon, ok := ompSlashCommandIcons[cmd]; ok {
		return icon
	}
	return "action"
}

// ompAutocompletePrefixFromEditor inspects the active editor text and cursor to
// determine whether the user is completing a `/` slash command or `@` file path
// (matching omp `editor.ts:2038-2046` `#autocompleteMark`).
func ompAutocompletePrefixFromEditor(editor fe.Editor) (kind byte, mark string, ok bool) {
	before := utf16SlicePrefix(editor.Text, editor.Cursor)
	// Restrict to current line.
	if nl := strings.LastIndexByte(before, '\n'); nl >= 0 {
		before = before[nl+1:]
	}
	// Slash command completion at start of buffer (single-line or first line).
	if strings.HasPrefix(editor.Text, "/") && !strings.Contains(before, "\n") {
		if !strings.ContainsRune(before, ' ') {
			typed := strings.TrimPrefix(before, "/")
			if slash := strings.LastIndexByte(typed, '/'); slash >= 0 {
				typed = typed[slash+1:]
			}
			return '/', typed, true
		}
		// Subcommand / argument completion (e.g. "/plan foo"): mark is text after last space.
		if sp := strings.LastIndexByte(before, ' '); sp >= 0 {
			typed := before[sp+1:]
			if slash := strings.LastIndexByte(typed, '/'); slash >= 0 {
				typed = typed[slash+1:]
			}
			return '/', typed, true
		}
	}
	// `@` file/mention completion on the current token.
	if at := strings.LastIndexByte(before, '@'); at >= 0 {
		if at == 0 || before[at-1] == ' ' || before[at-1] == '\t' {
			token := before[at+1:]
			if !strings.ContainsAny(token, " \t") {
				token = strings.TrimPrefix(token, `"`)
				if slash := strings.LastIndexByte(token, '/'); slash >= 0 {
					token = token[slash+1:]
				}
				return '@', token, true
			}
		}
	}
	return 0, "", false
}

// parseOmpAutocomplete parses PiG's `SelectList.Render` lines from `dock.below`
// into structured autocomplete items when the editor is actively completing a
// `/` or `@` prefix. Returns nil when `lines` do not look like a PiG SelectList.
func parseOmpAutocomplete(editor fe.Editor, lines []string) *ompAutocompleteState {
	if len(lines) == 0 {
		return nil
	}
	kind, mark, ok := ompAutocompletePrefixFromEditor(editor)
	if !ok {
		return nil
	}
	var (
		items       []ompAutocompleteItem
		selectedIdx = -1
		status      string
	)
	for _, raw := range lines {
		plain := strings.TrimRight(stripANSI(raw), " \r\n")
		if strings.TrimSpace(plain) == "" {
			continue
		}
		trimmed := strings.TrimSpace(plain)
		if ompAutocompleteScrollRe.MatchString(trimmed) {
			status = trimmed
			continue
		}
		var (
			selected bool
			body     string
		)
		switch {
		case strings.HasPrefix(plain, "→ "):
			selected = true
			body = strings.TrimSpace(plain[len("→ "):])
		case strings.HasPrefix(plain, "  "):
			body = strings.TrimSpace(plain[2:])
		default:
			// Not a SelectList row — bail out and let raw ANSI render.
			return nil
		}
		if body == "" {
			continue
		}
		label, desc := body, ""
		if m := ompAutocompleteColGapRe.FindStringSubmatch(body); m != nil {
			label = m[1]
			desc = strings.TrimSpace(m[2])
		}
		if selected {
			selectedIdx = len(items)
		}
		items = append(items, ompAutocompleteItem{
			label:       label,
			description: desc,
			icon:        ompAutocompleteIcon(kind, label),
			selected:    selected,
		})
	}
	if len(items) == 0 || selectedIdx < 0 {
		return nil
	}
	return &ompAutocompleteState{
		items:       items,
		selectedIdx: selectedIdx,
		status:      status,
		mark:        mark,
		prefixKind:  kind,
	}
}

// ompAutocompleteOverlay builds omp's caret-anchored autocomplete overlay
// mounted in `layer` (matching omp's hoisted `overlay[omp.autocomplete]` from
// `editor.ts:2007` + `reconcile.ts:27-28` + `select-list.ts:298-380`).
func (s *session) ompAutocompleteOverlay(ac *ompAutocompleteState) tern.Node {
	const listID = "layer.complete.select.list"
	keys := ompAutocompleteItemKeys(ac.items)
	items := make([]tern.Element, 0, len(ac.items))
	itemByID := make(map[string]ompAutocompleteItem, len(ac.items))
	selectedID := ""
	for i, it := range ac.items {
		key := keys[i]
		fullID := listID + "." + key
		itemByID[fullID] = it
		if i == ac.selectedIdx {
			selectedID = fullID
		}
		labelSpans := ompAutocompleteLabelSpans(it.label, ac.mark)
		item := ui.Item{
			Common: ui.Common{Key: key},
			Icon:   it.icon,
			Label:  ui.Spans(labelSpans...),
		}
		if it.description != "" {
			item.Detail = ui.Spans(ui.Span{T: it.description, S: "muted"})
		}
		items = append(items, item)
	}

	listNode := ui.List{
		CommonWithoutMax: ui.CommonWithoutMax{Key: "list"},
		Selected:         selectedID,
		Empty:            ui.T("No items"),
		Max:              ui.MaxLines(max(len(ac.items), 10)),
		Children:         items,
	}.Node()
	listNode.Handlers = tern.Handlers{
		OnSelect: func(ev *tern.Select) {
			if it, ok := itemByID[ev.Item]; ok {
				s.acceptOmpAutocompleteClick(it)
			}
		},
		OnActivate: func(ev *tern.Activate) {
			if it, ok := itemByID[ev.Item]; ok {
				s.acceptOmpAutocompleteClick(it)
			}
		},
	}

	selectChildren := []tern.Element{listNode}
	if ac.status != "" {
		selectChildren = append(selectChildren, ui.Text{
			Common: ui.Common{Key: "status"},
			Spans:  []ui.Span{{T: ac.status, S: "muted"}},
			Wrap:   ui.WrapNone,
		})
	}
	selectCol := ui.Col{
		Common:   ui.Common{Key: "select", Role: "omp.select"},
		Children: selectChildren,
	}

	return buildNode(ui.Overlay{
		Common: ui.Common{
			Key:  "complete",
			Role: "omp.autocomplete",
		},
		Anchor:   ui.AtCaret("dock.editor.editor.line.input"),
		Children: []tern.Element{selectCol},
	})
}

// ompAutocompleteItemKeys returns unique child keys for autocomplete items so
// duplicate labels (e.g. `@` fuzzy file matches with identical basenames) never
// collide in the Tern reconciler. Mirrors `itemKeys` in view.go.
func ompAutocompleteItemKeys(items []ompAutocompleteItem) []string {
	keys := make([]string, len(items))
	seen := make(map[string]int, len(items))
	for i, it := range items {
		base := it.label
		if base == "" {
			base = fmt.Sprintf("item-%d", i)
		}
		if n := seen[base]; n == 0 {
			seen[base] = 1
			keys[i] = base
		} else {
			seen[base] = n + 1
			key := fmt.Sprintf("%s#%d", base, n)
			for seen[key] > 0 {
				n++
				key = fmt.Sprintf("%s#%d", base, n)
			}
			seen[key] = 1
			keys[i] = key
		}
	}
	return keys
}

// ompAutocompleteLabelSpans highlights the leading matched prefix with `{s:"mark"}`
// matching omp `select-list.ts:355-363`.
func ompAutocompleteLabelSpans(label, mark string) []ui.Span {
	if mark == "" {
		return []ui.Span{{T: label}}
	}
	lowerLabel := strings.ToLower(label)
	lowerMark := strings.ToLower(mark)
	if !strings.HasPrefix(lowerLabel, lowerMark) {
		return []ui.Span{{T: label}}
	}
	n := len(mark)
	if n > len(label) {
		n = len(label)
	}
	spans := []ui.Span{{T: label[:n], S: "mark"}}
	if n < len(label) {
		spans = append(spans, ui.Span{T: label[n:]})
	}
	return spans
}

// acceptOmpAutocompleteClick inserts a clicked top-level slash command into the
// editor via `Env.Edit`. For `@` file paths and `/cmd <arg>` completions where
// `dock.below` carries only a display label (basename or truncated text) rather
// than the full completion value, clicking is intentionally a no-op so it never
// corrupts the buffer; keyboard Up/Down/Tab/Enter apply PiG's real item value.
func (s *session) acceptOmpAutocompleteClick(item ompAutocompleteItem) {
	// PiG's SelectList caps slash primary columns at 32 cells with an empty
	// truncation suffix (`tui/editor.go:1193`, `select_list.go`), so labels at
	// >=30 runes may be silently truncated (e.g. long `/skill:...` names).
	if item.label == "" || len([]rune(item.label)) >= 30 || strings.HasSuffix(item.label, "…") || strings.HasSuffix(item.label, "...") || s.env.Edit == nil {
		return
	}
	s.mu.Lock()
	if !s.hasEditor || s.closed || s.fellBack || s.autocomplete == nil {
		s.mu.Unlock()
		return
	}
	editor := s.editor
	kind := s.autocomplete.prefixKind
	s.mu.Unlock()

	if kind != '/' {
		return
	}
	text := editor.Text
	cur := editor.Cursor
	before := utf16SlicePrefix(text, cur)
	// Only complete top-level `/command` tokens (no space yet) where label is the full command name.
	if !strings.HasPrefix(before, "/") || strings.ContainsRune(before, ' ') || strings.ContainsRune(before, '\n') {
		return
	}
	replacement := "/" + strings.TrimPrefix(item.label, "/") + " "
	s.env.Edit(fe.Edit{
		From:   0,
		To:     cur,
		Text:   replacement,
		Cursor: utf16Len(replacement),
		Len:    utf16Len(text),
	})
}

// utf16SlicePrefix returns the prefix of s spanning the first n UTF-16 code units.
func utf16SlicePrefix(s string, n int) string {
	if n <= 0 {
		return ""
	}
	units := 0
	for i, r := range s {
		if units >= n {
			return s[:i]
		}
		if r >= 0x10000 {
			units += 2
		} else {
			units++
		}
	}
	return s
}
