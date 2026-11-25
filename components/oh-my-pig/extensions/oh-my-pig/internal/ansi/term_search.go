package ansi

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

func init() {
	registerTerminal("grep", Builder{Call: termGrepCall, Result: termGrepResult})
	registerTerminal("glob", Builder{Call: termGlobCall, Result: termGlobResult})
	registerTerminal("find", Builder{Call: termFindCall, Result: termFindResult})
	registerTerminal("ast_grep", Builder{Call: termAstGrepCall, Result: termAstGrepResult})
	registerTerminal("ast_edit", Builder{Call: termAstEditCall, Result: termAstEditResult})
}

// ═══════════════════════════════════════════════════════════════════════════
// Shared grouped-file output parser & budget fitter
// upstream: packages/tui/src/tools/grouped-file-output.ts
// ═══════════════════════════════════════════════════════════════════════════

var (
	groupedHeaderRe   = regexp.MustCompile(`^(#+)\s+(.*)$`)
	headerSuffixRe    = regexp.MustCompile(`\s+\([^)]*\)\s*$`)
	hashlineTagSuffRe = regexp.MustCompile(`#[0-9A-Fa-f]{4}$`)
	groupedMatchRowRe = regexp.MustCompile(`^\s*\*\d+(?:│|[:|])`)
	groupedNumLineRe  = regexp.MustCompile(`^(\s*\*?)(\d+)(│|[:|])(.*)$`)
)

func isGroupedHeader(line string) bool {
	return groupedHeaderRe.MatchString(line)
}

func isMatchRow(line string) bool {
	return groupedMatchRowRe.MatchString(line)
}

type groupedLineCtx struct {
	kind        string // "dir", "file", "content"
	depth       int
	displayPath string
	suffix      string
	isURL       bool
}

// classifyGroupedLines mirrors packages/tui/src/tools/grouped-file-output.ts:224-275:
// tracks a directory stack keyed by `#` depth so nested directory headers fold
// cleanly into each file's full display path and never cut filenames at spaces.
func classifyGroupedLines(lines []string) []groupedLineCtx {
	result := make([]groupedLineCtx, len(lines))
	var dirStack []string
	for i, line := range lines {
		m := groupedHeaderRe.FindStringSubmatch(line)
		if m == nil {
			result[i] = groupedLineCtx{kind: "content"}
			continue
		}
		depth := len(m[1])
		rest := strings.TrimRight(m[2], " \t")
		if depth-1 < len(dirStack) {
			dirStack = dirStack[:depth-1]
		}
		_, _, isURL := splitURLScheme(rest)
		if !isURL && strings.HasSuffix(rest, "/") {
			dirStack = append(dirStack, rest)
			result[i] = groupedLineCtx{kind: "dir", depth: depth}
			continue
		}
		suffix := ""
		target := rest
		if loc := headerSuffixRe.FindStringIndex(rest); loc != nil {
			suffix = strings.TrimSpace(rest[loc[0]:loc[1]])
			target = rest[:loc[0]]
		}
		name := hashlineTagSuffRe.ReplaceAllString(target, "")
		if isURL {
			result[i] = groupedLineCtx{kind: "file", depth: depth, displayPath: name, suffix: suffix, isURL: true}
			continue
		}
		fullDisplay := strings.Join(dirStack, "") + name
		result[i] = groupedLineCtx{kind: "file", depth: depth, displayPath: fullDisplay, suffix: suffix}
	}
	return result
}

func groupLineIndicesByBlank(lines []string) [][]int {
	hasBlank := false
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			hasBlank = true
			break
		}
	}
	if !hasBlank {
		var all []int
		for i, l := range lines {
			if strings.TrimSpace(l) != "" {
				all = append(all, i)
			}
		}
		if len(all) == 0 {
			return nil
		}
		return [][]int{all}
	}
	var groups [][]int
	var cur []int
	for i, l := range lines {
		if strings.TrimSpace(l) == "" {
			if len(cur) > 0 {
				groups = append(groups, cur)
				cur = nil
			}
			continue
		}
		cur = append(cur, i)
	}
	if len(cur) > 0 {
		groups = append(groups, cur)
	}
	return groups
}

// groupsByFile mirrors packages/tui/src/tools/grouped-file-output.ts:284-303.
func groupsByFile(groups [][]int, ctxs []groupedLineCtx) [][]int {
	var out [][]int
	for _, grp := range groups {
		var cur []int
		for _, idx := range grp {
			k := ctxs[idx].kind
			if k == "dir" {
				continue
			}
			if k == "file" && len(cur) > 0 {
				out = append(out, cur)
				cur = nil
			}
			cur = append(cur, idx)
		}
		if len(cur) > 0 {
			out = append(out, cur)
		}
	}
	return out
}

// fitGroupsToBudget mirrors packages/tui/src/tools/grouped-file-output.ts:414-446.
func fitGroupsToBudget(groups [][]int, budget int, isUnit func(int) bool, unit string, totalUnits int) (shown [][]int, summary string) {
	totalLines := 0
	for _, g := range groups {
		totalLines += len(g)
	}
	if totalLines <= budget {
		return groups, ""
	}
	if budget <= 0 {
		return nil, ""
	}
	room := budget - 1
	used := 0
	shownUnits := 0
	for _, g := range groups {
		take := min(len(g), room-used)
		if take <= 0 || (take < min(len(g), 2) && len(shown) > 0) {
			break
		}
		part := g
		if take < len(g) {
			part = g[:take]
		}
		shown = append(shown, part)
		used += take
		for _, idx := range part {
			if isUnit(idx) {
				shownUnits++
			}
		}
		if take < len(g) {
			break
		}
	}
	allUnits := totalUnits
	if allUnits <= 0 {
		for _, g := range groups {
			for _, idx := range g {
				if isUnit(idx) {
					allUnits++
				}
			}
		}
	}
	if hiddenUnits := allUnits - shownUnits; hiddenUnits > 0 {
		return shown, formatMoreItems(hiddenUnits, unit)
	}
	if hiddenLines := totalLines - used; hiddenLines > 0 {
		return shown, formatMoreItems(hiddenLines, "line")
	}
	return shown, ""
}

func (p palette) styleGroupedRow(lines []string, ctxs []groupedLineCtx, idx int, styleBody func(int) string) string {
	c := ctxs[idx]
	if c.kind == "file" {
		name := replaceTabs(c.displayPath)
		label := p.formatStyledPath(name)
		if c.isURL {
			label = p.fg("customMessageLabel", name)
		}
		if c.suffix != "" {
			return label + " " + p.fg("dim", replaceTabs(c.suffix))
		}
		return label
	}
	return replaceTabs(styleBody(idx))
}

func (p palette) styleCodeFrameRow(line string) string {
	m := groupedNumLineRe.FindStringSubmatch(line)
	if m == nil {
		return p.fg("toolOutput", line)
	}
	// upstream: packages/tui/src/tools/grouped-file-output.ts:496-508 + grep.ts:269:
	// renderCodeFrameRun paints the gutter in the passed color ("toolOutput" -> muted in titanium).
	gutter := m[1] + m[2] + m[3]
	body := m[4]
	if strings.Contains(m[1], "*") {
		return p.fg("toolOutput", gutter) + p.fg("toolOutput", body)
	}
	return p.fg("dim", gutter) + p.fg("muted", body)
}
func pathListArg(args map[string]any) string {
	for _, key := range []string{"path", "paths"} {
		if list := stringSliceField(args, key); len(list) > 0 {
			return strings.Join(list, ", ")
		}
	}
	return ""
}

// formatScopedSubject mirrors packages/tui/src/tools/grouped-file-output.ts:611-619:
// only the keyword `in` is dim; `subject` and `scope` inherit the outer
// `renderStatusLine` description `muted` foreground via wrapForeground.
func (p palette) formatScopedSubject(subject, scope string) string {
	if scope == "" {
		return subject
	}
	kw := p.fg("dim", "in")
	if subject != "" {
		return subject + " " + kw + " " + scope
	}
	return kw + " " + scope
}

// ═══════════════════════════════════════════════════════════════════════════
// grep tool
// upstream: packages/tui/src/tools/grep.ts
// ═══════════════════════════════════════════════════════════════════════════

const (
	grepCollapsedTextLimit = previewCollapsedLines * 2 // 6
	grepExpandedTextLimit  = previewExpandedLines * 2  // 24
)

func grepFlagMeta(args map[string]any) []string {
	var meta []string
	if c, ok := boolField(args, "case"); ok && !c {
		meta = append(meta, "case:insensitive")
	}
	if gi, ok := boolField(args, "gitignore"); ok && !gi {
		meta = append(meta, "gitignore:false")
	}
	if skip, ok := intField(args, "skip"); ok && skip > 0 {
		meta = append(meta, fmt.Sprintf("skip:%d", skip))
	}
	return meta
}

func termGrepCall(ctx sdk.Context, args map[string]any, _ sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	pat := replaceTabs(strField(args, "pattern"))
	if pat == "" {
		pat = "?"
	}
	scope := pathListArg(args)
	header := p.renderStatusLine(statusLineOpts{
		icon:        "pending",
		title:       "Grep",
		titleColor:  "toolTitle",
		description: p.formatScopedSubject(pat, scope),
		meta:        grepFlagMeta(args),
	})
	return termWrappedTextNode(header, width), nil
}

func termGrepResult(ctx sdk.Context, res sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	details := asMap(res.Details)
	pat := replaceTabs(strField(rc.Args, "pattern"))
	scope := strField(details, "scopePath")
	if scope == "" {
		scope = pathListArg(rc.Args)
	}
	desc := p.formatScopedSubject(pat, scope)

	if rc.IsError || strField(details, "error") != "" {
		errText := strField(details, "error")
		if errText == "" {
			errText = resultText(res)
		}
		header := p.renderStatusLine(statusLineOpts{
			icon:        "error",
			title:       "Grep",
			titleColor:  "toolTitle",
			description: desc,
		})
		return termLinesNode(fitHeaderedRows(header, []string{p.formatErrorDetail(errText)}, width), width), nil
	}

	_, hasMatchCount := intField(details, "matchCount")
	_, hasFileCount := intField(details, "fileCount")
	if !hasMatchCount && !hasFileCount {
		textContent := strField(details, "displayContent")
		if textContent == "" {
			textContent = resultText(res)
		}
		if strings.TrimSpace(textContent) == "" || strings.TrimSpace(textContent) == "No matches found" {
			return termWrappedTextNode(p.formatEmptyMessage("No matches found"), width), nil
		}
		var lines []string
		for _, l := range sanitizeDisplayLines(textContent) {
			if strings.TrimSpace(l) != "" {
				lines = append(lines, p.fg("toolOutput", l))
			}
		}
		header := p.renderStatusLine(statusLineOpts{
			iconOverride: p.fg("toolTitle", unicodeSymbols.iconSearch),
			title:        "Grep",
			titleColor:   "toolTitle",
			description:  desc,
			meta:         []string{formatCount("item", len(lines))},
		})
		limit := grepCollapsedTextLimit
		if opts.Expanded {
			limit = grepExpandedTextLimit
		}
		tree := p.renderTreeLines(lines, false, limit, "item")
		return termLinesNode(fitHeaderedRows(header, tree, width), width), nil
	}

	matchCount, _ := intField(details, "matchCount")
	fileCount, _ := intField(details, "fileCount")
	metaObj := asMap(details["meta"])
	limitsObj := asMap(metaObj["limits"])
	truncFlag, _ := boolField(details, "truncated")
	colTrunc, _ := boolField(limitsObj, "columnTruncated")
	truncated := truncFlag || asMap(metaObj["truncation"]) != nil || colTrunc

	var extraLines []string
	if missing := stringSliceField(details, "missingPaths"); len(missing) > 0 {
		cleanMissing := make([]string, len(missing))
		for i, mp := range missing {
			cleanMissing[i] = flattenLine(mp)
		}
		extraLines = append(extraLines, p.fg("warning", "skipped missing: "+strings.Join(cleanMissing, ", ")))
	}
	if binMatches := stringSliceField(details, "binaryMatches"); len(binMatches) > 0 {
		extraLines = append(extraLines, p.fg("warning", "binary matches: "+formatCount("file", len(binMatches))))
	}

	if matchCount == 0 {
		header := p.renderStatusLine(statusLineOpts{
			icon:        "warning",
			title:       "Grep",
			titleColor:  "toolTitle",
			description: desc,
			meta:        []string{p.fg("warning", "0 matches")},
		})
		return termLinesNode(fitHeaderedRows(header, extraLines, width), width), nil
	}

	textContent := strField(details, "displayContent")
	if textContent == "" {
		textContent = resultText(res)
	}
	allLines := sanitizeDisplayLines(textContent)
	ctxs := classifyGroupedLines(allLines)

	hasAnyHeader := false
	for _, l := range allLines {
		if isGroupedHeader(l) {
			hasAnyHeader = true
			break
		}
	}
	singleFileScope := scope != "" && fileCount == 1 && !hasAnyHeader
	meta := []string{formatCount("match", matchCount)}
	if !singleFileScope {
		meta = append(meta, formatCount("file", fileCount))
	}
	if truncated {
		meta = append(meta, p.fg("warning", "truncated"))
	}
	hOpts := statusLineOpts{
		title:       "Grep",
		titleColor:  "toolTitle",
		description: desc,
		meta:        meta,
	}
	if truncated {
		hOpts.icon = "warning"
	} else {
		hOpts.iconOverride = p.fg("toolTitle", unicodeSymbols.iconSearch)
	}
	header := p.renderStatusLine(hOpts)

	matchGroups := groupsByFile(groupLineIndicesByBlank(allLines), ctxs)
	marked := false
	for _, l := range allLines {
		if isMatchRow(l) {
			marked = true
			break
		}
	}
	isMatch := func(idx int) bool {
		if marked {
			return isMatchRow(allLines[idx])
		}
		return ctxs[idx].kind == "content" && strings.TrimSpace(allLines[idx]) != ""
	}

	activeGroups := matchGroups
	if !opts.Expanded && marked {
		compact := make([][]int, 0, len(matchGroups))
		for _, grp := range matchGroups {
			var kept []int
			for _, idx := range grp {
				if ctxs[idx].kind == "file" || isMatch(idx) {
					kept = append(kept, idx)
				}
			}
			if len(kept) > 0 {
				compact = append(compact, kept)
			}
		}
		activeGroups = compact
	}

	budget := grepCollapsedTextLimit
	if opts.Expanded {
		budget = grepExpandedTextLimit
	}
	budget = max(0, budget-len(extraLines))
	shown, summary := fitGroupsToBudget(activeGroups, budget, isMatch, "match", matchCount)

	styledGroups := make([][]string, len(shown))
	for i, grp := range shown {
		rows := make([]string, len(grp))
		for j, idx := range grp {
			rows[j] = p.styleGroupedRow(allLines, ctxs, idx, func(k int) string {
				return p.styleCodeFrameRow(allLines[k])
			})
		}
		styledGroups[i] = rows
	}

	treeLines := p.renderTreeGroups(styledGroups, summary)
	treeLines = append(treeLines, extraLines...)
	return termLinesNode(fitHeaderedRows(header, treeLines, width), width), nil
}

// ═══════════════════════════════════════════════════════════════════════════
// glob tool
// upstream: packages/tui/src/tools/glob.ts
// ═══════════════════════════════════════════════════════════════════════════

func termGlobCall(ctx sdk.Context, args map[string]any, _ sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	var meta []string
	if lim, ok := intField(args, "limit"); ok {
		meta = append(meta, fmt.Sprintf("limit:%d", lim))
	}
	desc := pathListArg(args)
	if desc == "" {
		desc = "*"
	}
	header := p.renderStatusLine(statusLineOpts{
		icon:        "pending",
		title:       "Glob",
		titleColor:  "toolTitle",
		description: desc,
		meta:        meta,
	})
	return termWrappedTextNode(header, width), nil
}

func termGlobResult(ctx sdk.Context, res sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	details := asMap(res.Details)
	desc := pathListArg(rc.Args)

	if rc.IsError || strField(details, "error") != "" {
		errText := strField(details, "error")
		if errText == "" {
			errText = resultText(res)
		}
		return termWrappedTextNode(p.formatErrorMessage(errText), width), nil
	}

	fileCount, hasFileCount := intField(details, "fileCount")
	if !hasFileCount {
		textContent := resultText(res)
		if strings.TrimSpace(textContent) == "" ||
			strings.Contains(textContent, "No files matching") ||
			strings.Contains(textContent, "No files found") {
			return termWrappedTextNode(p.formatEmptyMessage("No files found"), width), nil
		}
		var lines []string
		for _, l := range sanitizeDisplayLines(textContent) {
			if strings.TrimSpace(l) != "" {
				lines = append(lines, p.formatStyledPath(strings.TrimSpace(l)))
			}
		}
		header := p.renderStatusLine(statusLineOpts{
			iconOverride: p.fg("toolTitle", unicodeSymbols.iconSearch),
			title:        "Glob",
			titleColor:   "toolTitle",
			description:  desc,
			meta:         []string{formatCount("file", len(lines))},
		})
		limit := previewCollapsedItems
		if opts.Expanded {
			limit = min(len(lines), maxExpandedOutputLines)
		}
		tree := p.renderTreeLines(lines, false, limit, "file")
		return termLinesNode(fitHeaderedRows(header, tree, width), width), nil
	}

	metaObj := asMap(details["meta"])
	limitsObj := asMap(metaObj["limits"])
	truncFlag, _ := boolField(details, "truncated")
	resLim, _ := intField(details, "resultLimitReached")
	timedOut, _ := boolField(details, "timedOut")
	truncated := truncFlag || asMap(details["truncation"]) != nil || asMap(metaObj["truncation"]) != nil || resLim > 0 || limitsObj["resultLimit"] != nil

	var missingNote string
	if missing := stringSliceField(details, "missingPaths"); len(missing) > 0 {
		missingNote = p.fg("warning", "skipped missing: "+strings.Join(missing, ", "))
	}

	if fileCount == 0 {
		emptyTimedOut := timedOut || truncated
		emptyLabel := "No files found"
		meta := []string{"0 files"}
		if emptyTimedOut {
			emptyLabel = "No matches before timeout (scan incomplete)"
			meta = append(meta, p.fg("warning", "timed out"))
		}
		header := p.renderStatusLine(statusLineOpts{
			icon:        "warning",
			title:       "Glob",
			titleColor:  "toolTitle",
			description: desc,
			meta:        meta,
		})
		rows := []string{p.formatEmptyMessage(emptyLabel)}
		if missingNote != "" {
			rows = append(rows, missingNote)
		}
		return termLinesNode(fitHeaderedRows(header, rows, width), width), nil
	}

	meta := []string{formatCount("file", fileCount)}
	if sp := strField(details, "scopePath"); sp != "" {
		meta = append(meta, "in "+sp)
	}
	if truncated {
		lbl := "truncated"
		if timedOut {
			lbl = "timed out"
		}
		meta = append(meta, p.fg("warning", lbl))
	}
	hOpts := statusLineOpts{
		title:       "Glob",
		titleColor:  "toolTitle",
		description: desc,
		meta:        meta,
	}
	if truncated {
		hOpts.icon = "warning"
	} else {
		hOpts.iconOverride = p.fg("toolTitle", unicodeSymbols.iconSearch)
	}
	header := p.renderStatusLine(hOpts)

	files := stringSliceField(details, "files")
	if len(files) == 0 {
		for _, l := range sanitizeDisplayLines(resultText(res)) {
			if t := strings.TrimSpace(l); t != "" && !strings.HasPrefix(t, "[") {
				files = append(files, t)
			}
		}
	}
	maxShown := previewCollapsedItems
	if opts.Expanded {
		maxShown = min(len(files), maxExpandedOutputLines)
	}
	styled := make([]string, 0, min(len(files), maxShown))
	for _, f := range files[:min(len(files), maxShown)] {
		isDir := strings.HasSuffix(f, "/")
		ic := unicodeSymbols.iconFolder
		if !isDir {
			ic = langIcon(f)
		}
		styled = append(styled, p.fg("muted", ic)+" "+p.formatStyledPath(f))
	}
	var summary string
	if hidden := len(files) - len(styled); hidden > 0 {
		summary = formatMoreItems(hidden, "file")
	}
	groups := make([][]string, len(styled))
	for i, s := range styled {
		groups[i] = []string{s}
	}
	rows := p.renderTreeGroups(groups, summary)
	if missingNote != "" {
		rows = append(rows, missingNote)
	}
	return termLinesNode(fitHeaderedRows(header, rows, width), width), nil
}

// ═══════════════════════════════════════════════════════════════════════════
// find tool (semantic grep)
// upstream: packages/tui/src/tools/find.ts
// ═══════════════════════════════════════════════════════════════════════════

const (
	findGaugeWidth      = 6
	findCollapsedHits   = 5
	findRangesCollapsed = 1
	findRangesExpanded  = 3
)

func findScoreColor(score float64) string {
	switch {
	case score >= 0.7:
		return "success"
	case score >= 0.4:
		return "warning"
	default:
		return "muted"
	}
}

func termFindCall(ctx sdk.Context, args map[string]any, _ sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	q := strField(args, "query")
	desc := ""
	if q != "" {
		desc = `"` + truncateWidth(flattenLine(q), truncateTitle) + `"`
	}
	var meta []string
	if kws := stringSliceField(args, "grep_keywords"); len(kws) > 0 {
		meta = append(meta, strings.Join(kws, ", "))
	}
	if sp := strField(args, "path"); sp != "" {
		meta = append(meta, "in "+sp)
	}
	header := p.renderStatusLine(statusLineOpts{
		icon:        "pending",
		title:       "Find",
		titleColor:  "toolTitle",
		description: desc,
		meta:        meta,
	})
	return termWrappedTextNode(header, width), nil
}

func termFindResult(ctx sdk.Context, res sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	details := asMap(res.Details)
	if rc.IsError {
		return termWrappedTextNode(p.formatErrorMessage(resultText(res)), width), nil
	}
	query := strField(details, "query")
	if query == "" {
		query = strField(rc.Args, "query")
	}
	desc := ""
	if query != "" {
		desc = `"` + truncateWidth(flattenLine(query), truncateTitle) + `"`
	}
	if details == nil {
		header := p.renderStatusLine(statusLineOpts{icon: "warning", title: "Find", titleColor: "toolTitle", description: desc})
		return termLinesNode(fitHeaderedRows(header, []string{p.formatEmptyMessage("No matches found")}, width), width), nil
	}

	hits := asSlice(details["hits"])
	coverage := strField(details, "coverage")
	elapsedMs, _ := int64Field(details, "elapsedMs")
	meta := []string{formatCount("hit", len(hits))}
	if sp := strField(details, "scopePath"); sp != "" {
		meta = append(meta, "in "+sp)
	}
	if elapsedMs > 0 {
		meta = append(meta, formatDuration(elapsedMs))
	}
	if coverage == "partial" || coverage == "unavailable" {
		meta = append(meta, p.fg("warning", coverage))
	}

	hOpts := statusLineOpts{
		title:       "Find",
		titleColor:  "toolTitle",
		description: desc,
		meta:        meta,
	}
	if len(hits) == 0 || coverage == "partial" || coverage == "unavailable" {
		hOpts.icon = "warning"
	} else {
		hOpts.iconOverride = p.fg("toolTitle", unicodeSymbols.iconSearch)
	}
	header := p.renderStatusLine(hOpts)

	if len(hits) == 0 {
		return termLinesNode(fitHeaderedRows(header, []string{p.formatEmptyMessage("No matches found")}, width), width), nil
	}

	maxHits := findCollapsedHits
	rangeLim := findRangesCollapsed
	if opts.Expanded {
		maxHits = min(len(hits), maxExpandedOutputLines/4)
		rangeLim = findRangesExpanded
	}
	shownCount := min(len(hits), maxHits)
	groups := make([][]string, 0, shownCount)
	for _, raw := range hits[:shownCount] {
		h := asMap(raw)
		rel := strField(h, "rel")
		score, _ := floatField(h, "contentScore")
		linesSeen, _ := intField(h, "linesSeen")
		hTrunc, _ := boolField(h, "truncated")
		covText := fmt.Sprintf("%d lines judged", linesSeen)
		if hTrunc {
			covText += ", partial"
		}
		_, _, isURL := splitURLScheme(rel)
		lbl := p.formatStyledPath(rel)
		if isURL {
			lbl = p.fg("customMessageLabel", rel)
		}
		scColor := findScoreColor(score)
		hitLines := []string{fmt.Sprintf("%s %s %s %s",
			formatProgressBar(score, findGaugeWidth, p, scColor),
			p.fg(scColor, fmt.Sprintf("%.2f", score)),
			lbl,
			p.fg("dim", covText),
		)}

		ranges := asSlice(h["ranges"])
		type rngItem struct {
			start, end int
			prob       float64
			snippet    string
		}
		parsed := make([]rngItem, 0, len(ranges))
		for _, r := range ranges {
			rm := asMap(r)
			s, _ := intField(rm, "start")
			e, _ := intField(rm, "end")
			pr, _ := floatField(rm, "p")
			parsed = append(parsed, rngItem{start: s, end: e, prob: pr, snippet: strField(rm, "snippet")})
		}
		slices.SortFunc(parsed, func(a, b rngItem) int {
			if a.prob != b.prob {
				if a.prob > b.prob {
					return -1
				}
				return 1
			}
			return a.start - b.start
		})
		if len(parsed) > rangeLim {
			parsed = parsed[:rangeLim]
		}
		for ri, r := range parsed {
			spanLbl := fmt.Sprintf("%d", r.start)
			if r.end != r.start && r.end > 0 {
				spanLbl = fmt.Sprintf("%d-%d", r.start, r.end)
			}
			br := unicodeSymbols.treeBranch
			if ri == len(parsed)-1 {
				br = unicodeSymbols.treeLast
			}
			hitLines = append(hitLines, fmt.Sprintf("  %s %s %s %s",
				p.fg("dim", br),
				p.fg("muted", ":"+spanLbl),
				p.fg("dim", fmt.Sprintf("%.2f", r.prob)),
				p.fg("toolOutput", flattenLine(r.snippet)),
			))
		}
		groups = append(groups, hitLines)
	}
	summary := ""
	if hidden := len(hits) - shownCount; hidden > 0 {
		summary = formatMoreItems(hidden, "hit")
	}
	rows := p.renderTreeGroups(groups, summary)
	return termLinesNode(fitHeaderedRows(header, rows, width), width), nil
}

// ═══════════════════════════════════════════════════════════════════════════
// ast_grep & ast_edit tools
// upstream: packages/tui/src/tools/ast-grep.ts, ast-edit.ts
// ═══════════════════════════════════════════════════════════════════════════

const astCollapsedMatchLimit = previewCollapsedLines * 2 // 6
const astCollapsedChangeLimit = 10

func (p palette) appendParseErrorsBulletList(lines []string, errs []string, total int) []string {
	if len(errs) == 0 {
		return lines
	}
	capN := min(len(errs), 20)
	for _, e := range errs[:capN] {
		lines = append(lines, "  "+p.fg("warning", unicodeSymbols.formatBullet+" "+flattenLine(e)))
	}
	if total > capN {
		lines = append(lines, "  "+p.fg("dim", formatMoreItems(total-capN, "parse error")))
	}
	return lines
}

func formatParseErrorsCountLabel(errs []string, total int) string {
	n := len(errs)
	if total > n {
		n = total
	}
	return formatCount("parse issue", n)
}

func termAstGrepCall(ctx sdk.Context, args map[string]any, _ sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	var meta []string
	if skip, ok := intField(args, "skip"); ok && skip > 0 {
		meta = append(meta, fmt.Sprintf("skip:%d", skip))
	}
	pat := strField(args, "pat")
	if pat == "" {
		pat = "?"
	}
	header := p.renderStatusLine(statusLineOpts{
		icon:        "pending",
		title:       "AST Grep",
		description: p.formatScopedSubject(flattenLine(pat), pathListArg(args)),
		meta:        meta,
	})
	return termWrappedTextNode(header, width), nil
}

func termAstGrepResult(ctx sdk.Context, res sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	details := asMap(res.Details)
	scope := strField(details, "scopePath")
	if scope == "" {
		scope = pathListArg(rc.Args)
	}
	desc := p.formatScopedSubject(flattenLine(strField(rc.Args, "pat")), scope)

	if rc.IsError {
		header := p.renderStatusLine(statusLineOpts{icon: "error", title: "AST Grep", description: desc})
		return termLinesNode(fitHeaderedRows(header, []string{p.formatErrorDetail(resultText(res))}, width), width), nil
	}

	matchCount, _ := intField(details, "matchCount")
	fileCount, _ := intField(details, "fileCount")
	filesSearched, _ := intField(details, "filesSearched")
	limitReached, _ := boolField(details, "limitReached")
	parseErrors := stringSliceField(details, "parseErrors")
	parseErrorsTotal, _ := intField(details, "parseErrorsTotal")

	if matchCount == 0 {
		meta := []string{p.fg("warning", "0 matches")}
		if filesSearched > 0 {
			meta = append(meta, fmt.Sprintf("searched %d", filesSearched))
		}
		header := p.renderStatusLine(statusLineOpts{icon: "warning", title: "AST Grep", description: desc, meta: meta})
		var rows []string
		if len(parseErrors) > 0 {
			rows = append(rows, p.fg("warning", "Query may be mis-scoped; narrow `path` before concluding absence"))
			rows = p.appendParseErrorsBulletList(rows, parseErrors, parseErrorsTotal)
		}
		return termLinesNode(fitHeaderedRows(header, rows, width), width), nil
	}

	meta := []string{formatCount("match", matchCount), formatCount("file", fileCount), fmt.Sprintf("searched %d", filesSearched)}
	if limitReached {
		meta = append(meta, p.fg("warning", "limit reached"))
	}
	hOpts := statusLineOpts{title: "AST Grep", description: desc, meta: meta}
	if limitReached {
		hOpts.icon = "warning"
	} else {
		hOpts.iconOverride = p.fg("accent", unicodeSymbols.iconSearch)
	}
	header := p.renderStatusLine(hOpts)

	textContent := strField(details, "displayContent")
	if textContent == "" {
		textContent = resultText(res)
	}
	allLines := sanitizeDisplayLines(textContent)
	ctxs := classifyGroupedLines(allLines)

	rawGroups := groupsByFile(groupLineIndicesByBlank(allLines), ctxs)
	var matchGroups [][]int
	for _, g := range rawGroups {
		first := allLines[g[0]]
		if strings.HasPrefix(first, "Result limit reached") || strings.HasPrefix(first, "Parse issues:") {
			continue
		}
		matchGroups = append(matchGroups, g)
	}
	activeGroups := matchGroups
	if !opts.Expanded {
		var compact [][]int
		for _, g := range matchGroups {
			var kept []int
			for _, idx := range g {
				if !strings.HasPrefix(allLines[idx], "  meta:") {
					kept = append(kept, idx)
				}
			}
			if len(kept) > 0 {
				compact = append(compact, kept)
			}
		}
		activeGroups = compact
	}

	budget := astCollapsedMatchLimit
	if opts.Expanded {
		budget = maxExpandedOutputLines
	}
	shown, summary := fitGroupsToBudget(activeGroups, budget, func(idx int) bool { return isMatchRow(allLines[idx]) }, "match", matchCount)
	styledGroups := make([][]string, len(shown))
	for i, grp := range shown {
		rows := make([]string, len(grp))
		for j, idx := range grp {
			rows[j] = p.styleGroupedRow(allLines, ctxs, idx, func(k int) string {
				l := allLines[k]
				if strings.HasPrefix(l, "  meta:") {
					return p.fg("dim", l)
				}
				return p.styleCodeFrameRow(l)
			})
		}
		styledGroups[i] = rows
	}

	rows := p.renderTreeGroups(styledGroups, summary)
	if limitReached {
		rows = append(rows, p.fg("warning", "limit reached; narrow path or use skip"))
	}
	if len(parseErrors) > 0 {
		rows = append(rows, p.fg("warning", formatParseErrorsCountLabel(parseErrors, parseErrorsTotal)))
	}
	return termLinesNode(fitHeaderedRows(header, rows, width), width), nil
}

func astEditRewriteLabel(args map[string]any) string {
	ops := asSlice(args["ops"])
	if len(ops) == 1 {
		if pat := strField(asMap(ops[0]), "pat"); pat != "" {
			return truncateWidth(flattenLine(pat), truncateShort)
		}
	}
	if len(ops) > 1 {
		return fmt.Sprintf("%d rewrites", len(ops))
	}
	return ""
}

func termAstEditCall(ctx sdk.Context, args map[string]any, _ sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	lbl := astEditRewriteLabel(args)
	if lbl == "" {
		lbl = "?"
	}
	header := p.renderStatusLine(statusLineOpts{
		icon:        "pending",
		title:       "AST Edit",
		description: p.formatScopedSubject(lbl, pathListArg(args)),
	})
	return termWrappedTextNode(header, width), nil
}

func termAstEditResult(ctx sdk.Context, res sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	details := asMap(res.Details)
	scope := strField(details, "scopePath")
	if scope == "" {
		scope = pathListArg(rc.Args)
	}
	desc := p.formatScopedSubject(astEditRewriteLabel(rc.Args), scope)

	if rc.IsError {
		header := p.renderStatusLine(statusLineOpts{icon: "error", title: "AST Edit", description: desc})
		rows := p.renderOutputBlock(blockOpts{
			header:      header,
			state:       "error",
			borderColor: "error",
			sections:    []blockSection{{lines: strings.Split(p.formatErrorDetail(resultText(res)), "\n")}},
			width:       width,
		})
		return termLinesNode(rows, width), nil
	}

	totalRepl, _ := intField(details, "totalReplacements")
	filesTouched, _ := intField(details, "filesTouched")
	filesSearched, _ := intField(details, "filesSearched")
	limitReached, _ := boolField(details, "limitReached")
	applied, _ := boolField(details, "applied")
	parseErrors := stringSliceField(details, "parseErrors")
	parseErrorsTotal, _ := intField(details, "parseErrorsTotal")

	if totalRepl == 0 {
		meta := []string{"0 replacements"}
		if filesSearched > 0 {
			meta = append(meta, fmt.Sprintf("searched %d", filesSearched))
		}
		var body []string
		body = p.appendParseErrorsBulletList(body, parseErrors, parseErrorsTotal)
		if len(body) == 0 {
			header := p.renderStatusLine(statusLineOpts{icon: "warning", title: "AST Edit", description: desc, meta: meta})
			return termWrappedTextNode(header, width), nil
		}
		header := p.renderStatusLine(statusLineOpts{icon: "warning", title: "AST Edit", description: desc})
		rows := p.renderOutputBlock(blockOpts{
			header:      header,
			footerMeta:  p.fg("dim", strings.Join(meta, unicodeSymbols.sepDot)),
			state:       "warning",
			borderColor: "borderMuted",
			sections:    []blockSection{{lines: body}},
			width:       width,
		})
		return termLinesNode(rows, width), nil
	}

	hIcon := "success"
	if limitReached {
		hIcon = "warning"
	}
	header := p.renderStatusLine(statusLineOpts{icon: hIcon, title: "AST Edit", description: desc})

	footerParts := []string{
		p.fg("dim", formatCount("replacement", totalRepl)),
		p.fg("dim", formatCount("file", filesTouched)),
		p.fg("dim", fmt.Sprintf("searched %d", filesSearched)),
	}
	if !applied {
		footerParts = append([]string{p.fg("warning", "proposed")}, footerParts...)
	}
	footerMeta := strings.Join(footerParts, p.fg("dim", unicodeSymbols.sepDot))

	textContent := strField(details, "displayContent")
	if textContent == "" {
		textContent = resultText(res)
	}
	allLines := sanitizeDisplayLines(textContent)
	ctxs := classifyGroupedLines(allLines)
	rawGroups := groupsByFile(groupLineIndicesByBlank(allLines), ctxs)
	var changeGroups [][]int
	for _, g := range rawGroups {
		first := allLines[g[0]]
		if strings.HasPrefix(first, "Safety cap reached") || strings.HasPrefix(first, "Parse issues:") {
			continue
		}
		changeGroups = append(changeGroups, g)
	}

	budget := astCollapsedChangeLimit
	if opts.Expanded {
		budget = maxExpandedOutputLines
	}
	var body []string
	used := 0
	hiddenFiles := 0
	for gi, g := range changeGroups {
		if used >= budget {
			hiddenFiles = len(changeGroups) - gi
			break
		}
		if gi > 0 && len(body) > 0 {
			body = append(body, "")
		}
		for _, idx := range g {
			if used >= budget {
				break
			}
			row := p.styleGroupedRow(allLines, ctxs, idx, func(k int) string {
				l := strings.Replace(allLines[k], "│", " ", 1)
				switch {
				case strings.HasPrefix(l, "+"):
					return p.fg("toolDiffAdded", l)
				case strings.HasPrefix(l, "-"):
					return p.fg("toolDiffRemoved", l)
				default:
					return p.fg("toolOutput", l)
				}
			})
			body = append(body, row)
			used++
		}
	}
	if hiddenFiles > 0 {
		body = append(body, p.fg("dim", formatMoreItems(hiddenFiles, "file")+" "+p.formatExpandHint(opts.Expanded, true)))
	}
	if limitReached {
		body = append(body, p.fg("warning", "limit reached; narrow path"))
	}
	if len(parseErrors) > 0 {
		body = append(body, p.fg("warning", formatParseErrorsCountLabel(parseErrors, parseErrorsTotal)))
	}

	var sections []blockSection
	if len(body) > 0 {
		sections = []blockSection{{lines: body}}
	}
	astState := "success"
	if opts.IsPartial {
		astState = "partial"
	}
	rows := p.renderOutputBlock(blockOpts{
		header:      header,
		footerMeta:  footerMeta,
		state:       astState,
		borderColor: "borderMuted",
		sections:    sections,
		width:       width,
	})
	return termLinesNode(rows, width), nil
}
