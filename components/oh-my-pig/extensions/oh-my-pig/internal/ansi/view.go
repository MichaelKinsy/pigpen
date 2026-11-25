package ansi

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"
)

// clampViewLines creates a kit.Lines node whose rows are all guaranteed to
// satisfy tui.VisibleWidth(row) <= width (for width >= 1). Terminal builders
// emit no omp.* node IDs.
func termLinesNode(lines []string, width int) kit.Node {
	if width <= 0 || len(lines) == 0 {
		return kit.NewContainer()
	}
	return kit.NewLines(clampRows(lines, width))
}

// termWrappedTextNode wraps text lines to width and emits a single kit.Lines
// node (or empty container) with every row clamped to width.
func termWrappedTextNode(text string, width int) kit.Node {
	if width <= 0 || text == "" {
		return kit.NewContainer()
	}
	var out []string
	for _, raw := range strings.Split(text, "\n") {
		out = append(out, wrapAnsi(raw, width)...)
	}
	return termLinesNode(out, width)
}

// countNodes counts every node in a kit.Node tree (used for defensive node-cap
// enforcement and tests against PiG's 4096-node limit).
func countNodes(n kit.Node) int {
	if n == nil {
		return 0
	}
	count := 1
	switch x := n.(type) {
	case *kit.Container:
		for _, ch := range x.Children {
			count += countNodes(ch)
		}
	case *kit.Box:
		for _, ch := range x.Children {
			count += countNodes(ch)
		}
	case *kit.HStack:
		for _, ch := range x.Children {
			if se, ok := ch.(kit.StackEntry); ok {
				count += countNodes(se.Node)
			} else if kn, ok := ch.(kit.Node); ok {
				count += countNodes(kn)
			}
		}
	case *kit.VStack:
		for _, ch := range x.Children {
			if se, ok := ch.(kit.StackEntry); ok {
				count += countNodes(se.Node)
			} else if kn, ok := ch.(kit.Node); ok {
				count += countNodes(kn)
			}
		}
	}
	return count
}

// splitURLScheme returns (scheme, rest, true) when raw begins with `<scheme>://`.
func splitURLScheme(raw string) (string, string, bool) {
	idx := strings.Index(raw, "://")
	if idx <= 0 {
		return "", "", false
	}
	scheme := strings.ToLower(raw[:idx])
	for _, r := range scheme {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '+' || r == '-' || r == '.') {
			return "", "", false
		}
	}
	return scheme, raw[idx+3:], true
}

// isReadableURLPath matches http://, https://, and www. URLs.
func isReadableURLPath(raw string) bool {
	lower := strings.ToLower(strings.TrimSpace(raw))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "www.")
}

func getDomain(rawURL string) string {
	s := strings.TrimSpace(rawURL)
	if idx := strings.Index(s, "://"); idx >= 0 {
		s = s[idx+3:]
	}
	if idx := strings.IndexAny(s, "/?#"); idx >= 0 {
		s = s[:idx]
	}
	s = strings.TrimPrefix(s, "www.")
	return s
}

// stripOutputNotice removes trailing bracketed LLM notices appended by tools.
// upstream: packages/tui/src/tools/output-meta.ts stripOutputNotice
var trailingGeneratedNoticeRe = regexp.MustCompile(`^\[(?:Showing |Output truncated|Truncated|Some lines truncated|Full output:|Sampled output|Read |Full output was not saved|\d+ more lines).*\]$`)

func stripTrailingNotice(text string, match func(line string) bool) string {
	trimmed := strings.TrimRight(text, "\r\n")
	if trimmed == "" {
		return text
	}
	idx := strings.LastIndexByte(trimmed, '\n')
	lastLine := trimmed
	prefix := ""
	if idx >= 0 {
		lastLine = trimmed[idx+1:]
		prefix = trimmed[:idx]
	}
	if !match(strings.TrimSpace(lastLine)) {
		return text
	}
	return strings.TrimRight(prefix, "\r\n")
}

func stripOutputNotice(text string, _ map[string]any) string {
	cur := text
	for {
		next := stripTrailingNotice(cur, func(line string) bool {
			return trailingGeneratedNoticeRe.MatchString(line)
		})
		if next == cur {
			return cur
		}
		cur = next
	}
}

// ── Shared code-cell and markdown-cell rendering ────────────────────────────
// upstream: packages/tui/src/render/code-cell.ts

type codeCellOpts struct {
	code            string
	language        string
	showLanguage    bool
	index           int
	total           int
	title           string
	status          string // "complete", "error", "warning", "running", "pending"
	durationMs      *int64
	output          string
	expanded        bool
	outputMaxLines  int
	codeMaxLines    int
	codeTail        bool
	codeStartLine   int
	codeLineNumbers []int // <= 0 means blank gutter (elided span)
	width           int
}

func (p palette) formatCodeCellHeader(opts codeCellOpts) (string, string) {
	var parts []string
	if opts.showLanguage && opts.language != "" {
		if icon := p.langIconStyled(opts.language); icon != "" {
			parts = append(parts, icon)
		}
	}
	if opts.status != "" {
		var st string
		switch opts.status {
		case "complete":
			st = "success"
		case "error":
			st = "error"
		case "warning":
			st = "warning"
		case "running":
			st = "running"
		default:
			st = "pending"
		}
		icon := p.statusIcon(st)
		if opts.status == "pending" || opts.status == "running" {
			parts = append(parts, icon+" "+p.fg("muted", opts.status))
		} else {
			parts = append(parts, icon)
		}
	}
	if opts.total > 1 {
		parts = append(parts, p.fg("accent", fmt.Sprintf("[%d/%d]", opts.index+1, opts.total)))
	}
	if opts.title != "" {
		parts = append(parts, p.fg("toolTitle", opts.title))
	}
	title := strings.Join(parts, " ")
	if title == "" {
		title = p.fg("toolTitle", "Code")
	}
	meta := ""
	if opts.durationMs != nil {
		meta = p.fg("dim", "("+formatDuration(*opts.durationMs)+")")
	}
	return title, meta
}

func codeCellState(status string) string {
	switch status {
	case "complete":
		return "success"
	case "error":
		return "error"
	case "warning":
		return "warning"
	case "running":
		return "running"
	case "":
		return ""
	default:
		return "pending"
	}
}

func codeCellBorderColor(status string) string {
	return stateBorderColor(codeCellState(status))
}
func (p palette) renderCodeCellLines(opts codeCellOpts) []string {
	if opts.width <= 0 {
		return nil
	}
	title, meta := p.formatCodeCellHeader(opts)
	codeMax := opts.codeMaxLines
	if codeMax <= 0 {
		codeMax = readPreviewLines
	}
	outMax := opts.outputMaxLines
	if outMax <= 0 {
		outMax = 6
	}

	rawLines := sanitizeDisplayLines(opts.code)
	if len(rawLines) > maxExpandedOutputLines {
		if opts.codeTail {
			rawLines = rawLines[len(rawLines)-maxExpandedOutputLines:]
		} else {
			rawLines = rawLines[:maxExpandedOutputLines]
		}
	}
	maxCode := len(rawLines)
	if !opts.expanded && maxCode > codeMax {
		maxCode = codeMax
	}
	hiddenCode := len(rawLines) - maxCode
	tail := opts.codeTail && !opts.expanded && hiddenCode > 0
	startIdx := 0
	if tail {
		startIdx = len(rawLines) - maxCode
	}
	visible := rawLines[startIdx : startIdx+maxCode]

	// Determine line-number gutter width.
	var lineNums []int
	if len(opts.codeLineNumbers) > 0 {
		end := min(len(opts.codeLineNumbers), startIdx+maxCode)
		if startIdx < end {
			lineNums = opts.codeLineNumbers[startIdx:end]
		}
	} else if opts.codeStartLine > 0 {
		lineNums = make([]int, len(visible))
		for i := range visible {
			lineNums[i] = opts.codeStartLine + startIdx + i
		}
	}
	numWidth := 0
	for _, n := range lineNums {
		if n > 0 {
			if l := len(fmt.Sprintf("%d", n)); l > numWidth {
				numWidth = l
			}
		}
	}
	if numWidth > 0 && numWidth < 2 {
		numWidth = 2
	}

	contentW := outputBlockContentWidth(opts.width, nil, nil)
	var displayCode []string
	if numWidth > 0 {
		contPad := strings.Repeat(" ", numWidth+1)
		for i, line := range visible {
			gutter := strings.Repeat(" ", numWidth)
			if i < len(lineNums) && lineNums[i] > 0 {
				gutter = fmt.Sprintf("%*d", numWidth, lineNums[i])
			}
			prefix := p.fg("dim", gutter+" ")
			displayCode = append(displayCode, wrapPrefixedLine(prefix, contPad, p.fg("toolOutput", line), contentW)...)
		}
	} else {
		for _, line := range visible {
			displayCode = append(displayCode, p.fg("toolOutput", line))
		}
	}

	if hiddenCode > 0 {
		hint := p.formatExpandHint(opts.expanded, true)
		gutterPad := ""
		if numWidth > 0 {
			gutterPad = strings.Repeat(" ", numWidth+1)
		}
		if tail {
			earlier := fmt.Sprintf("… %d earlier %s", hiddenCode, pluralize("line", hiddenCode))
			if hint != "" {
				earlier += " " + hint
			}
			displayCode = append([]string{p.fg("dim", gutterPad+earlier)}, displayCode...)
		} else {
			more := formatMoreItems(hiddenCode, "line")
			if hint != "" {
				more += " " + hint
			}
			displayCode = append(displayCode, p.fg("dim", gutterPad+more))
		}
	}

	sections := []blockSection{{lines: displayCode}}
	if strings.TrimSpace(opts.output) != "" {
		outLines, _ := p.formatOutputPaneLines(outputPaneOpts{
			lines:             strings.Split(opts.output, "\n"),
			expanded:          opts.expanded,
			collapsedMaxLines: outMax,
			edge:              "head",
			formatHidden: func(hidden, _, _ int, _ string) string {
				return formatMoreItems(hidden, "line")
			},
		})
		if len(outLines) > 0 {
			sections = append(sections, blockSection{label: p.fg("toolTitle", "Output"), lines: outLines})
		}
	}

	return p.renderOutputBlock(blockOpts{
		header:     title,
		headerMeta: meta,
		state:      codeCellState(opts.status),
		sections:   sections,
		width:      opts.width,
	})
}

// renderMarkdownCellLines mirrors packages/tui/src/render/code-cell.ts:322-358:
// renders sanitized markdown lines inside a framed block, clamping collapsed
// output to 12 lines with `… N more lines ⟦Ctrl+O: Expand⟧`.
func (p palette) renderMarkdownCellLines(title, content, output, status string, expanded bool, width int) []string {
	if width <= 0 {
		return nil
	}
	contentW := outputBlockContentWidth(width, nil, nil)
	var allLines []string
	if strings.TrimSpace(content) != "" {
		for _, raw := range sanitizeDisplayLines(content) {
			allLines = append(allLines, wrapAnsi(p.styleMarkdownLine(raw), contentW)...)
		}
	}
	maxLines := len(allLines)
	if !expanded && maxLines > readPreviewLines {
		maxLines = readPreviewLines
	}
	shown := append([]string(nil), allLines[:maxLines]...)
	if hidden := len(allLines) - maxLines; hidden > 0 {
		hint := p.formatExpandHint(expanded, true)
		more := formatMoreItems(hidden, "line")
		if hint != "" {
			more += " " + hint
		}
		shown = append(shown, p.fg("dim", more))
	}

	header, meta := p.formatCodeCellHeader(codeCellOpts{
		title:  title,
		status: status,
	})
	sections := []blockSection{{lines: shown}}
	if strings.TrimSpace(output) != "" {
		sections = append(sections, blockSection{
			label: p.fg("toolTitle", "Output"),
			lines: strings.Split(output, "\n"),
		})
	}
	return p.renderOutputBlock(blockOpts{
		header:     header,
		headerMeta: meta,
		state:      codeCellState(status),
		sections:   sections,
		width:      width,
	})
}

// styleMarkdownLine applies light terminal styling to a sanitized markdown line
// so framed markdown cards never rely on unframed kit.Markdown nodes.
func (p palette) styleMarkdownLine(line string) string {
	trimmed := strings.TrimLeft(line, " ")
	switch {
	case strings.HasPrefix(trimmed, "#"):
		return p.bold(p.fg("mdHeading", line))
	case strings.HasPrefix(trimmed, "> "):
		indent := line[:len(line)-len(trimmed)]
		return indent + p.fg("mdQuoteBorder", unicodeSymbols.mdQuoteBorder) + " " + p.italic(p.fg("mdQuote", trimmed[2:]))
	case strings.HasPrefix(trimmed, "```"):
		return p.fg("mdCodeBlockBorder", line)
	case strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* "):
		indent := line[:len(line)-len(trimmed)]
		return indent + p.fg("mdListBullet", unicodeSymbols.mdBullet) + " " + p.fg("text", trimmed[2:])
	default:
		return p.fg("text", line)
	}
}

// formatDiagnosticsLines mirrors packages/tui/src/render/render-utils.ts formatDiagnostics.
func (p palette) formatDiagnosticsLines(diag map[string]any, expanded bool) []string {
	if diag == nil {
		return nil
	}
	messages := stringSliceField(diag, "messages")
	if len(messages) == 0 {
		return nil
	}
	errored, _ := boolField(diag, "errored")
	summary := strField(diag, "summary")
	if summary == "" {
		summary = formatCount("diagnostic", len(messages))
	}
	color := "warning"
	if errored {
		color = "error"
	}
	limit := len(messages)
	if !expanded && limit > 5 {
		limit = 5
	}
	lines := make([]string, 0, limit+2)
	lines = append(lines, p.fg(color, "Diagnostics: "+sanitizeDisplayText(summary)))
	for i := range limit {
		lines = append(lines, "  "+p.fg(color, replaceTabs(sanitizeDisplayText(messages[i]))))
	}
	if hidden := len(messages) - limit; hidden > 0 {
		lines = append(lines, "  "+p.fg("muted", formatMoreItems(hidden, "diagnostic")))
	}
	return lines
}

// ── Diff stats, hunk truncation, and omp-style gutter rendering ─────────────
// upstream: packages/tui/src/render/render-utils.ts getDiffStats/truncateDiffByHunk
// and packages/tui/src/chrome/diff.ts renderDiff

type diffStats struct {
	added   int
	removed int
	hunks   int
	lines   int
}

// isUnifiedHeaderLine detects git unified diff metadata headers (`--- `, `+++ `, `@@ `).
func isUnifiedHeaderLine(line string) bool {
	return strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") || strings.HasPrefix(line, "@@ ")
}

// normalizeDiffToCanonical converts standard unified diffs (`---`/`+++`/`@@ -a,b +c,d @@`)
// produced by internal/files/patch.go into omp's canonical numbered diff lines
// (` 1|ctx`, `-2|old`, `+2|new`), while passing already-canonical lines through.
var unifiedHunkHeaderRe = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)
var canonicalDiffLineRe = regexp.MustCompile(`^([+\-\s])(\s*\d+)\|(.*)$`)

func normalizeDiffToCanonical(diffText string) string {
	clean := sanitizeDisplayText(diffText)
	if clean == "" {
		return ""
	}
	lines := strings.Split(clean, "\n")
	isUnified := false
	for _, l := range lines {
		if unifiedHunkHeaderRe.MatchString(l) || strings.HasPrefix(l, "--- ") || strings.HasPrefix(l, "+++ ") {
			isUnified = true
			break
		}
	}
	if !isUnified {
		return clean
	}
	var out []string
	oldLine, newLine := 1, 1
	inHunk := false
	for _, l := range lines {
		if strings.HasPrefix(l, "--- ") || strings.HasPrefix(l, "+++ ") || strings.HasPrefix(l, "\\ No newline") {
			continue
		}
		if m := unifiedHunkHeaderRe.FindStringSubmatch(l); m != nil {
			if inHunk && len(out) > 0 {
				out = append(out, "")
			}
			inHunk = true
			fmt.Sscanf(m[1], "%d", &oldLine)
			fmt.Sscanf(m[2], "%d", &newLine)
			continue
		}
		if !inHunk {
			continue
		}
		if l == "" {
			continue
		}
		switch l[0] {
		case '-':
			out = append(out, fmt.Sprintf("-%d|%s", oldLine, l[1:]))
			oldLine++
		case '+':
			out = append(out, fmt.Sprintf("+%d|%s", newLine, l[1:]))
			newLine++
		case ' ':
			out = append(out, fmt.Sprintf(" %d|%s", oldLine, l[1:]))
			oldLine++
			newLine++
		default:
			out = append(out, " |"+l)
		}
	}
	return strings.Join(out, "\n")
}

// getDiffStats counts added lines, removed lines, and contiguous change runs (hunks).
// upstream: packages/tui/src/render/render-utils.ts:694-718
func getDiffStats(diffText string) diffStats {
	canonical := normalizeDiffToCanonical(diffText)
	if canonical == "" {
		return diffStats{}
	}
	lines := strings.Split(canonical, "\n")
	var st diffStats
	st.lines = len(lines)
	inHunk := false
	for _, l := range lines {
		isAdd := strings.HasPrefix(l, "+")
		isRem := strings.HasPrefix(l, "-")
		if isAdd {
			st.added++
		}
		if isRem {
			st.removed++
		}
		if isAdd || isRem {
			if !inHunk {
				st.hunks++
				inHunk = true
			}
		} else {
			inHunk = false
		}
	}
	return st
}

// formatDiffStatsSuffix mirrors packages/tui/src/tools/edit.ts:1060-1068: ` ⟦+1/-1⟧`.
func (p palette) formatDiffStatsSuffix(diffText string) string {
	st := getDiffStats(diffText)
	if st.added == 0 && st.removed == 0 {
		return ""
	}
	var parts []string
	if st.added > 0 {
		parts = append(parts, p.fg("toolDiffAdded", fmt.Sprintf("+%d", st.added)))
	}
	if st.removed > 0 {
		parts = append(parts, p.fg("toolDiffRemoved", fmt.Sprintf("-%d", st.removed)))
	}
	return " " + p.fg("dim", unicodeSymbols.formatBracketLeft) +
		strings.Join(parts, p.fg("dim", "/")) +
		p.fg("dim", unicodeSymbols.formatBracketRight)
}

type diffSegment struct {
	lines      []string
	isChange   bool
	isEllipsis bool
}

func parseDiffSegments(lines []string) []diffSegment {
	var segs []diffSegment
	var cur *diffSegment
	flush := func() {
		if cur != nil {
			segs = append(segs, *cur)
			cur = nil
		}
	}
	for _, l := range lines {
		isChange := strings.HasPrefix(l, "+") || strings.HasPrefix(l, "-")
		isEllipsis := strings.HasPrefix(strings.TrimLeft(l, " \t"), "...") || strings.TrimSpace(l) == ""
		if isEllipsis {
			flush()
			segs = append(segs, diffSegment{lines: []string{l}, isEllipsis: true})
		} else if cur == nil || cur.isChange != isChange {
			flush()
			cur = &diffSegment{lines: []string{l}, isChange: isChange}
		} else {
			cur.lines = append(cur.lines, l)
		}
	}
	flush()
	return segs
}

// truncateDiffByHunk mirrors packages/tui/src/render/render-utils.ts:760-903.
func truncateDiffByHunk(canonical string, maxHunks, maxLines int) (string, int, int) {
	if canonical == "" {
		return "", 0, 0
	}
	lines := strings.Split(canonical, "\n")
	stats := getDiffStats(canonical)
	if len(lines) <= maxLines && stats.hunks <= maxHunks {
		return canonical, 0, 0
	}
	segs := parseDiffSegments(lines)
	changeLines := 0
	for _, s := range segs {
		if s.isChange {
			changeLines += len(s.lines)
		}
	}
	if changeLines > maxLines {
		var kept []string
		keptHunks := 0
		for _, s := range segs {
			if len(kept) >= maxLines {
				break
			}
			if s.isChange {
				if keptHunks >= maxHunks {
					break
				}
				keptHunks++
			}
			take := min(len(s.lines), maxLines-len(kept))
			kept = append(kept, s.lines[:take]...)
		}
		return strings.Join(kept, "\n"), max(0, stats.hunks-keptHunks), max(0, len(lines)-len(kept))
	}

	ctxBudget := maxLines - changeLines
	totalCtx := 0
	for _, s := range segs {
		if !s.isChange {
			totalCtx += len(s.lines)
		}
	}

	var kept []string
	keptHunks := 0
	keptSource := 0
	if totalCtx <= ctxBudget {
		for _, s := range segs {
			if s.isChange {
				if keptHunks >= maxHunks {
					break
				}
				keptHunks++
			}
			kept = append(kept, s.lines...)
			keptSource += len(s.lines)
		}
	} else {
		ratio := 0.0
		if totalCtx > 0 {
			ratio = float64(ctxBudget) / float64(totalCtx)
		}
		remCtx := ctxBudget
		for i, s := range segs {
			if s.isChange {
				if keptHunks >= maxHunks {
					break
				}
				keptHunks++
				kept = append(kept, s.lines...)
				keptSource += len(s.lines)
				continue
			}
			if remCtx <= 0 {
				continue
			}
			allowed := min(remCtx, max(1, int(float64(len(s.lines))*ratio)))
			outStart := len(kept)
			srcAdded := 0
			if s.isEllipsis || len(s.lines) <= allowed {
				take := min(allowed, len(s.lines))
				kept = append(kept, s.lines[:take]...)
				srcAdded = take
			} else {
				isBefore := i+1 < len(segs) && segs[i+1].isChange
				isAfter := i-1 >= 0 && segs[i-1].isChange
				switch {
				case isBefore && isAfter:
					if allowed >= 3 {
						sb := allowed - 1
						fc := (sb + 1) / 2
						lc := sb - fc
						kept = append(kept, s.lines[:fc]...)
						kept = append(kept, "")
						if lc > 0 {
							kept = append(kept, s.lines[len(s.lines)-lc:]...)
						}
						srcAdded = sb
					} else {
						fc := (allowed + 1) / 2
						lc := allowed - fc
						kept = append(kept, s.lines[:fc]...)
						if lc > 0 {
							kept = append(kept, s.lines[len(s.lines)-lc:]...)
						}
						srcAdded = allowed
					}
				case isBefore:
					kept = append(kept, s.lines[len(s.lines)-allowed:]...)
					srcAdded = allowed
				case isAfter:
					kept = append(kept, s.lines[:allowed]...)
					srcAdded = allowed
				default:
					take := min(allowed, 2)
					kept = append(kept, s.lines[:take]...)
					srcAdded = take
				}
			}
			keptSource += srcAdded
			remCtx -= len(kept) - outStart
		}
	}
	return strings.Join(kept, "\n"), max(0, stats.hunks-keptHunks), max(0, len(lines)-keptSource)
}

// renderDiffLines renders a diff (canonical or unified) in omp's gutter format
// (`   1│context`, `  -2│old`, `   +│new`) wrapped to innerWidth so it stays
// inside the framed edit card and never emits unsanitised text or open borders.
// upstream: packages/tui/src/chrome/diff.ts renderDiff, packages/tui/src/tools/edit.ts wrapEditRendererLine
func (p palette) renderDiffLines(diffText string, expanded bool, innerWidth int) []string {
	canonical := normalizeDiffToCanonical(diffText)
	if canonical == "" {
		return nil
	}
	hiddenHunks, hiddenLines := 0, 0
	shown := canonical
	if !expanded {
		shown, hiddenHunks, hiddenLines = truncateDiffByHunk(canonical, previewDiffHunks, previewDiffLines)
	}
	rawLines := strings.Split(shown, "\n")
	if len(rawLines) > maxExpandedOutputLines {
		hiddenLines += len(rawLines) - maxExpandedOutputLines
		rawLines = rawLines[:maxExpandedOutputLines]
	}

	// Reserve at least 3 gutter digits (chrome/diff.ts:188-191).
	numWidth := 3
	type parsedDiff struct {
		ok      bool
		prefix  string
		lineNum string
		content string
	}
	parsed := make([]parsedDiff, len(rawLines))
	for i, l := range rawLines {
		if m := canonicalDiffLineRe.FindStringSubmatch(l); m != nil {
			ln := strings.TrimSpace(m[2])
			if len(ln) > numWidth {
				numWidth = len(ln)
			}
			parsed[i] = parsedDiff{ok: true, prefix: m[1], lineNum: ln, content: replaceTabs(m[3])}
		}
	}

	var rows []string
	prevLineNum := ""
	for i, pd := range parsed {
		if !pd.ok {
			prevLineNum = ""
			if strings.TrimSpace(rawLines[i]) == "" || strings.TrimSpace(rawLines[i]) == "..." {
				rows = append(rows, p.fg("dim", strings.Repeat(" ", numWidth)+"…"))
			} else {
				rows = append(rows, wrapAnsi(p.fg("toolDiffContext", replaceTabs(rawLines[i])), innerWidth)...)
			}
			continue
		}
		displayNum := pd.lineNum
		if displayNum != "" && displayNum == prevLineNum {
			displayNum = ""
		} else if displayNum != "" {
			prevLineNum = displayNum
		}
		gutter := formatCodeFrameLine(pd.prefix, displayNum, "", numWidth)
		color := "toolDiffContext"
		switch pd.prefix {
		case "+":
			color = "toolDiffAdded"
		case "-":
			color = "toolDiffRemoved"
		}
		gutterW := tui.VisibleWidth(gutter)
		contPrefix := strings.Repeat(" ", max(0, gutterW-1)) + "│"
		bodyW := max(1, innerWidth-gutterW)
		wrapped := wrapAnsi(pd.content, bodyW)
		for j, seg := range wrapped {
			g := contPrefix
			if j == 0 {
				g = gutter
			}
			rows = append(rows, clampRow(p.fg(color, g+seg), innerWidth))
		}
	}

	if !expanded && len(rows) > previewDiffLines {
		hiddenLines += len(rows) - previewDiffLines
		rows = rows[:previewDiffLines]
	}

	if !expanded && (hiddenHunks > 0 || hiddenLines > 0) {
		var rem []string
		if hiddenHunks > 0 {
			rem = append(rem, formatCount("more hunk", hiddenHunks))
		}
		if hiddenLines > 0 {
			rem = append(rem, formatCount("more line", hiddenLines))
		}
		hint := p.formatExpandHint(false, true)
		notice := fmt.Sprintf("… (%s)", strings.Join(rem, ", "))
		if hint != "" {
			notice += " " + hint
		}
		rows = append(rows, p.fg("toolOutput", notice))
	}
	return rows
}

// isFigureFile reports whether filePath has an extension omp renders as a figure.
func isFigureFile(filePath string) (string, bool) {
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(filePath), "."))
	switch ext {
	case "svg":
		return "svg", true
	case "mmd", "mermaid":
		return "mermaid", true
	case "obj", "ply", "wrl", "vrml", "x3dv", "stl", "gltf", "usda", "usd":
		return ext, true
	default:
		return "", false
	}
}
