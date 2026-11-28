package ansi

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/intent"
)

func init() {
	registerTerminal("web_search", Builder{Call: termWebSearchCall, Result: termWebSearchResult})
	registerTerminal("lsp", Builder{Call: termLspCall, Result: termLspResult})
	registerTerminal("github", Builder{Call: termGithubCall, Result: termGithubResult})
	registerTerminal("debug", Builder{Call: termDebugCall, Result: termDebugResult})
	registerTerminal("wait", Builder{Call: termWaitCall, Result: termWaitResult})
	registerTerminal("resolve", Builder{Call: termResolveCall("apply"), Result: termResolveResult})
	registerTerminal("reject", Builder{Call: termResolveCall("discard"), Result: termResolveResult})
	registerTerminal("propose", termTextArgBuilder("Propose", "title"))
	registerTerminal("report_issue", termTextArgBuilder("Report Tool Issue", "report"))
	registerTerminal("retain", Builder{Call: termRetainCall, Result: termRetainResult})
	registerTerminal("recall", termMemoryQueryBuilder("Recall"))
	registerTerminal("reflect", termMemoryQueryBuilder("Reflect"))
	registerTerminal("think", Builder{Call: termThinkCall, Result: termThinkResult})
	registerTerminal("goal", Builder{Call: termGoalCall, Result: termGoalResult})
	registerTerminal("yield", Builder{Call: termYieldCall, Result: termYieldResult})
	registerTerminal("checkpoint", termDefaultBuilder("Checkpoint"))
	registerTerminal("rewind", termDefaultBuilder("Rewind"))
	registerTerminal("browser", termDefaultBuilder("Browser"))
}

// ═══════════════════════════════════════════════════════════════════════════
// web_search tool
// upstream: packages/tui/src/tools/web-search.ts
// ═══════════════════════════════════════════════════════════════════════════

func getSearchProviderLabel(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openai", "openai_api":
		return "OpenAI API"
	case "gemini", "google":
		return "Gemini"
	case "anthropic":
		return "Anthropic"
	case "perplexity":
		return "Perplexity"
	case "exa":
		return "Exa"
	case "brave":
		return "Brave"
	case "tavily":
		return "Tavily"
	case "kagi":
		return "Kagi"
	case "hackernews", "hn":
		return "Hacker News"
	case "none", "":
		return "None"
	default:
		return provider
	}
}

// webSearchExcerptLines mirrors packages/tui/src/tools/web-search.ts:44-54:
// collapses whitespace runs, drops lines without a letter or digit, caps at 3 lines.
func webSearchExcerptLines(snippet string) []string {
	if snippet == "" {
		return nil
	}
	var out []string
	for _, raw := range sanitizeDisplayLines(snippet) {
		line := strings.Join(strings.Fields(raw), " ")
		hasAlphaNum := false
		for _, r := range line {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				hasAlphaNum = true
				break
			}
		}
		if !hasAlphaNum {
			continue
		}
		out = append(out, line)
		if len(out) == previewCollapsedLines {
			break
		}
	}
	return out
}

func termWebSearchCall(ctx sdk.Context, args map[string]any, _ sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	q := truncateWidth(flattenLine(strField(args, "query")), truncateContent)
	header := p.renderStatusLine(statusLineOpts{icon: "pending", title: "Web Search", description: q})
	return termWrappedTextNode(header, width), nil
}

func termWebSearchResult(ctx sdk.Context, res sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	details := asMap(res.Details)

	// packages/tui/src/tools/web-search.ts:91-99, 114-119
	if rc.IsError || strField(details, "error") != "" {
		errText := strField(details, "error")
		if errText == "" {
			errText = resultText(res)
		}
		var errProvLabel string
		if respObj := asMap(details["response"]); respObj != nil {
			if prov := strField(respObj, "provider"); prov != "" && prov != "none" {
				errProvLabel = getSearchProviderLabel(prov)
			}
		}
		header := p.renderStatusLine(statusLineOpts{icon: "error", title: "Web Search", description: errProvLabel})
		body := p.fg("error", "Error: "+replaceTabs(sanitizeDisplayText(errText)))
		rows := p.renderOutputBlock(blockOpts{
			header:      header,
			state:       "error",
			borderColor: "error",
			sections:    []blockSection{{lines: []string{body}}},
			width:       width,
		})
		return termLinesNode(rows, width), nil
	}

	rawText := strings.TrimSpace(resultText(res))
	resp := asMap(details["response"])
	if resp == nil {
		// packages/tui/src/tools/web-search.ts:56-82 (renderFallbackText)
		var nonEmpty []string
		for _, l := range sanitizeDisplayLines(rawText) {
			if t := strings.TrimSpace(l); t != "" {
				nonEmpty = append(nonEmpty, t)
			}
		}
		maxLines := 6
		if opts.Expanded {
			maxLines = min(len(nonEmpty), maxExpandedOutputLines)
		}
		shownCount := min(len(nonEmpty), maxLines)
		remaining := len(nonEmpty) - shownCount
		hint := p.formatExpandHint(opts.Expanded, remaining > 0)
		header := p.statusIcon("warning") + " " + p.fg("dim", "Response")
		if hint != "" {
			header += " " + hint
		}
		var body []string
		if shownCount == 0 {
			body = []string{" " + p.fg("dim", unicodeSymbols.treeLast) + " " + p.fg("muted", "No response data")}
		} else {
			for i, l := range nonEmpty[:shownCount] {
				br := unicodeSymbols.treeBranch
				if i == shownCount-1 && remaining == 0 {
					br = unicodeSymbols.treeLast
				}
				body = append(body, " "+p.fg("dim", br)+" "+p.fg("dim", clipCollapsed(l, 110, opts.Expanded)))
			}
			if !opts.Expanded && remaining > 0 {
				body = append(body, " "+p.fg("dim", unicodeSymbols.treeLast)+" "+p.fg("muted", formatMoreItems(remaining, "line")))
			}
		}
		return termLinesNode(fitHeaderedRows(header, body, width), width), nil
	}

	sources := asSlice(resp["sources"])
	searchQueries := stringSliceField(resp, "searchQueries")
	provider := strField(resp, "provider")
	providerLabel := getSearchProviderLabel(provider)
	answerText := strings.TrimSpace(strField(resp, "answer"))

	queryPreview := truncateWidth(flattenLine(strField(rc.Args, "query")), truncateContent)
	if queryPreview == "" && len(searchQueries) > 0 {
		queryPreview = truncateWidth(flattenLine(searchQueries[0]), truncateContent)
	}

	var hOpts statusLineOpts
	border := "dim"
	if len(sources) > 0 {
		hOpts = statusLineOpts{
			iconOverride: p.styledSym(unicodeSymbols.toolWebSearch, "accent"),
			title:        "Web Search",
			description:  providerLabel,
			meta:         []string{formatCount("source", len(sources))},
		}
	} else {
		border = "warning"
		hOpts = statusLineOpts{
			icon:        "warning",
			title:       "Web Search",
			description: providerLabel,
			meta:        []string{formatCount("source", 0)},
		}
	}
	header := p.renderStatusLine(hOpts)

	contentW := outputBlockContentWidth(width, nil, nil)
	var sections []blockSection
	if queryPreview != "" {
		sections = append(sections, blockSection{
			lines: []string{p.fg("muted", "Query:") + " " + p.fg("text", queryPreview)},
		})
	}

	// packages/tui/src/tools/web-search.ts:188-214
	if answerText != "" {
		var ansLines []string
		for _, l := range sanitizeDisplayLines(answerText) {
			ansLines = append(ansLines, wrapAnsi(p.styleMarkdownLine(l), contentW)...)
		}
		sections = append(sections, blockSection{
			label: p.fg("toolTitle", "Answer"),
			lines: ansLines,
		})
	} else if len(sources) == 0 {
		var fbLines []string
		for _, l := range sanitizeDisplayLines(rawText) {
			if strings.TrimSpace(l) != "" {
				fbLines = append(fbLines, l)
			}
		}
		var ansLines []string
		if len(fbLines) > 0 {
			lim := previewOutputCollapse
			if opts.Expanded {
				lim = min(len(fbLines), maxExpandedOutputLines)
			}
			shown := min(len(fbLines), lim)
			for _, l := range fbLines[:shown] {
				ansLines = append(ansLines, p.fg("dim", truncateWidth(l, contentW)))
			}
			if len(fbLines) > shown {
				ansLines = append(ansLines, p.fg("muted", formatMoreItems(len(fbLines)-shown, "line")))
			}
		} else {
			ansLines = []string{p.fg("muted", "No answer text returned")}
		}
		sections = append(sections, blockSection{
			label: p.fg("toolTitle", "Answer"),
			lines: ansLines,
		})
	}

	// Sources tree: `Title (domain) · age`, with 1 excerpt line collapsed (up to 3 expanded) when no answer.
	maxSources := previewCollapsedItems
	if opts.Expanded {
		maxSources = min(len(sources), maxExpandedOutputLines/4)
	}
	shownCount := min(len(sources), maxSources)
	groups := make([][]string, 0, shownCount)
	lineBudget := max(24, width-6)
	for _, raw := range sources[:shownCount] {
		src := asMap(raw)
		urlStr := strField(src, "url")
		titleStr := strings.TrimSpace(strField(src, "title"))
		if titleStr == "" {
			titleStr = urlStr
		}
		if titleStr == "" {
			titleStr = "Untitled"
		}
		domain := getDomain(urlStr)
		age := ""
		if ageSec, ok := floatField(src, "ageSeconds"); ok && ageSec > 0 {
			age = formatAge(ageSec)
		}
		if age == "" {
			age = strField(src, "publishedDate")
		}
		var metaParts []string
		if domain != "" {
			metaParts = append(metaParts, p.fg("dim", "("+domain+")"))
		}
		if age != "" {
			metaParts = append(metaParts, p.fg("muted", age))
		}
		metaSuffix := ""
		if len(metaParts) > 0 {
			metaSuffix = " " + strings.Join(metaParts, p.fg("dim", unicodeSymbols.sepDot))
		}
		titleBudget := max(12, lineBudget-tui.VisibleWidth(metaSuffix))
		row := p.fg("accent", truncateWidth(flattenLine(titleStr), titleBudget)) + metaSuffix
		itemLines := []string{row}
		if answerText == "" {
			excerpts := webSearchExcerptLines(strField(src, "snippet"))
			limit := 1
			if opts.Expanded {
				limit = len(excerpts)
			}
			for _, sl := range excerpts[:min(len(excerpts), limit)] {
				itemLines = append(itemLines, p.fg("dim", truncateWidth(sl, lineBudget)))
			}
		}
		groups = append(groups, itemLines)
	}
	var srcLines []string
	if len(groups) == 0 {
		srcLines = []string{p.fg("muted", "No sources returned")}
	} else {
		summary := ""
		if hidden := len(sources) - shownCount; hidden > 0 {
			summary = formatMoreItems(hidden, "source")
		}
		srcLines = p.renderTreeGroups(groups, summary)
	}
	sections = append(sections, blockSection{
		label: p.fg("toolTitle", "Sources"),
		lines: srcLines,
	})
	// Metadata section (web-search.ts:165-178, 271).
	authShort := strField(resp, "authMode")
	switch authShort {
	case "oauth":
		authShort = "OAuth"
	case "api_key":
		authShort = "API"
	}
	provInfo := providerLabel
	if m := strField(resp, "model"); m != "" {
		provInfo = m + " @ " + providerLabel
	}
	if authShort != "" {
		provInfo += " (" + authShort + ")"
	}
	metaLines := []string{p.fg("muted", "Provider:") + " " + p.fg("text", provInfo)}
	if usage := asMap(resp["usage"]); usage != nil {
		var up []string
		if v, ok := intField(usage, "inputTokens"); ok {
			up = append(up, fmt.Sprintf("in %d", v))
		}
		if v, ok := intField(usage, "outputTokens"); ok {
			up = append(up, fmt.Sprintf("out %d", v))
		}
		if v, ok := intField(usage, "totalTokens"); ok {
			up = append(up, fmt.Sprintf("total %d", v))
		}
		if v, ok := intField(usage, "searchRequests"); ok {
			up = append(up, fmt.Sprintf("search %d", v))
		}
		if len(up) > 0 {
			metaLines = append(metaLines, p.fg("muted", "Usage:")+" "+p.fg("text", strings.Join(up, unicodeSymbols.sepDot)))
		}
	}
	sections = append(sections, blockSection{
		label: p.fg("toolTitle", "Metadata"),
		lines: metaLines,
	})

	wsState := "success"
	if len(sources) == 0 {
		wsState = "warning"
	}
	rows := p.renderOutputBlock(blockOpts{
		header:      header,
		state:       wsState,
		borderColor: border,
		sections:    sections,
		width:       width,
	})
	return termLinesNode(rows, width), nil
}

// ═══════════════════════════════════════════════════════════════════════════
// lsp tool (hover, diagnostics, locations/references, symbols, generic)
// upstream: packages/tui/src/tools/lsp.ts
// ═══════════════════════════════════════════════════════════════════════════

var (
	lspLocationsHdrRe = regexp.MustCompile(`^Found (\d+) (definition|type definition|implementation|reference)\(s\):$`)
	lspLocationRowRe  = regexp.MustCompile(`^  (.+):(\d+):(\d+)$`)
	lspLocationCtxRe  = regexp.MustCompile(`^    (\d+): ?(.*)$`)
	lspDiagLineRe     = regexp.MustCompile(`^(.+?):(\d+):(\d+)\s+\[(error|warning|info|hint)\]\s*(.*)$`)
	lspDiagAtRe       = regexp.MustCompile(`^\d+:\d+\s+\[`)
	lspDiagSummaryRe  = regexp.MustCompile(`^\d+\s+\w+\(s\)(?:,\s*\d+\s+\w+\(s\))*:?$`)
	lspDiagAnySumRe   = regexp.MustCompile(`\d+\s+(?:error|warning)\(s\)`)
	lspFileSummaryRe  = regexp.MustCompile(`: (?:no issues|\d+ \w+\(s\)(?:, \d+ \w+\(s\))*)$`)
	lspUnreportedRe   = regexp.MustCompile(`\bno diagnostics report yet\b`)
	lspNoFoundRe      = regexp.MustCompile(`(?i)^no\b.*\bfound\b`)
	lspSymbolsHdrRe   = regexp.MustCompile(`Symbols in (.+):`)
	lspSymbolRowRe    = regexp.MustCompile(`^(\S+)\s+(.+?)\s*@\s*line\s*(\d+)`)
	lspHoverFenceRe   = regexp.MustCompile("(?s)```(\\w*)\\n(.*?)```")
)

const (
	lspCollapsedRefFiles   = 3
	lspCollapsedRefLocs    = 2
	lspCollapsedDiags      = 3
	lspCollapsedSymbols    = 3
	lspCollapsedGenericLen = 4
)

// lspRequestStatus mirrors packages/tui/src/tools/lsp.ts:115-144:
// `action target` as description (`references src/a.ts:42 (sym)`) and call facts as meta.
func lspRequestStatus(args map[string]any, fallbackAction string) (desc string, meta []string) {
	action := strField(args, "action")
	if action == "" {
		action = fallbackAction
	}
	if action == "" {
		action = "request"
	}
	actionLabel := strings.ReplaceAll(action, "_", " ")
	queryPreview := ""
	if q := strField(args, "query"); q != "" {
		queryPreview = truncateWidth(flattenLine(q), truncateShort)
	}
	symbolPreview := ""
	if sym := strField(args, "symbol"); sym != "" {
		symbolPreview = truncateWidth(flattenLine(sym), truncateShort)
	}

	var target string
	if f := strField(args, "file"); f != "" {
		target = shortenPath(sanitizeDisplayText(f))
		if ln, ok := intField(args, "line"); ok {
			target += fmt.Sprintf(":%d", ln)
			if symbolPreview != "" {
				target += " (" + symbolPreview + ")"
			}
		}
	} else if ln, ok := intField(args, "line"); ok {
		target = fmt.Sprintf("line %d", ln)
		if symbolPreview != "" {
			target += " (" + symbolPreview + ")"
		}
	}

	if queryPreview != "" && target != "" {
		meta = append(meta, "query:"+queryPreview)
	}
	if nn := strField(args, "new_name"); nn != "" {
		meta = append(meta, "new:"+flattenLine(nn))
	}
	if ap, ok := boolField(args, "apply"); ok {
		if ap {
			meta = append(meta, "apply:true")
		} else {
			meta = append(meta, "apply:false")
		}
	}

	subject := target
	if subject == "" {
		subject = queryPreview
	}
	if subject != "" {
		desc = actionLabel + " " + subject
	} else {
		desc = actionLabel
	}
	return desc, meta
}

func termLspCall(ctx sdk.Context, args map[string]any, _ sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	desc, meta := lspRequestStatus(args, "request")
	header := p.renderStatusLine(statusLineOpts{icon: "pending", title: "LSP", description: desc, meta: meta})
	return termWrappedTextNode(header, width), nil
}

func termLspResult(ctx sdk.Context, res sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	details := asMap(res.Details)
	req := rc.Args
	if req == nil {
		req = asMap(details["request"])
	}
	action := strField(req, "action")
	if action == "" {
		action = strField(details, "action")
	}
	text := resultText(res)

	if rc.IsError {
		desc, baseMeta := lspRequestStatus(req, action)
		header := p.renderStatusLine(statusLineOpts{icon: "error", title: "LSP", description: desc, meta: baseMeta})
		var rows []string
		for _, l := range sanitizeDisplayLines(text) {
			if strings.TrimSpace(l) != "" {
				rows = append(rows, wrapPrefixedLine("  ", "  ", p.fg("error", l), width)...)
			}
		}
		if len(rows) == 0 {
			rows = []string{"  " + p.fg("error", "LSP request failed")}
		}
		return termLinesNode(fitHeaderedRows(header, rows, width), width), nil
	}
	if strings.TrimSpace(text) == "" {
		desc, baseMeta := lspRequestStatus(req, action)
		meta := append(append([]string(nil), baseMeta...), "No result")
		header := p.renderStatusLine(statusLineOpts{icon: "warning", title: "LSP", description: desc, meta: meta})
		return termWrappedTextNode(header, width), nil
	}

	lines := sanitizeDisplayLines(text)
	// 1. Locations / References (`Found N definition|reference(s):`)
	if m := lspLocationsHdrRe.FindStringSubmatch(lines[0]); m != nil {
		count, _ := strconv.Atoi(m[1])
		noun := m[2]
		fallbackAct := noun
		if noun == "reference" {
			fallbackAct = "references"
		}
		desc, baseMeta := lspRequestStatus(req, fallbackAct)
		return p.renderLspLocations(desc, baseMeta, noun, count, lines[1:], opts.Expanded, width), nil
	}
	// 2. Diagnostics (action == "diagnostics" before hover, per lsp.ts:314)
	if action == "diagnostics" {
		desc, baseMeta := lspRequestStatus(req, "diagnostics")
		return p.renderLspDiagnostics(desc, baseMeta, lines, opts.Expanded, width), nil
	}
	// 3. Hover (fenced code block)
	if m := lspHoverFenceRe.FindStringSubmatchIndex(text); m != nil {
		desc, baseMeta := lspRequestStatus(req, "hover")
		return p.renderLspHover(desc, baseMeta, text, m, opts.Expanded, width), nil
	}
	// 4. Inferred diagnostics (summary or unreported)
	if lspDiagAnySumRe.MatchString(text) || lspUnreportedRe.MatchString(text) {
		desc, baseMeta := lspRequestStatus(req, "diagnostics")
		return p.renderLspDiagnostics(desc, baseMeta, lines, opts.Expanded, width), nil
	}
	// 5. Document symbols
	if m := lspSymbolsHdrRe.FindStringSubmatch(text); m != nil {
		desc, baseMeta := lspRequestStatus(req, "symbols")
		return p.renderLspSymbols(desc, baseMeta, m[1], strField(req, "file") == "", lines, opts.Expanded, width), nil
	}
	// 6. Generic view (lsp.ts:766-790)
	desc, baseMeta := lspRequestStatus(req, "request")
	hasError := strings.Contains(text, "Error:")
	hasSuccess := strings.Contains(text, "Applied")
	var hOpts statusLineOpts
	if (hasError && !hasSuccess) || lspNoFoundRe.MatchString(strings.TrimSpace(text)) {
		hOpts = statusLineOpts{icon: "warning", title: "LSP", description: desc, meta: baseMeta}
	} else {
		hOpts = statusLineOpts{iconOverride: p.styledSym(unicodeSymbols.toolLsp, "accent"), title: "LSP", description: desc, meta: baseMeta}
	}
	header := p.renderStatusLine(hOpts)
	styled := make([]string, 0, len(lines))
	for _, l := range lines {
		styled = append(styled, p.fg("toolOutput", l))
	}
	hidden := !opts.Expanded && len(styled) > lspCollapsedGenericLen
	if hidden {
		header += " " + p.formatExpandHint(false, true)
	}
	limit := lspCollapsedGenericLen
	if opts.Expanded {
		limit = min(len(styled), maxExpandedOutputLines)
	}
	tree := p.renderTreeLines(styled, opts.Expanded, limit, "line")
	return termLinesNode(fitHeaderedRows(header, tree, width), width), nil
}

func (p palette) renderLspHover(desc string, baseMeta []string, fullText string, loc []int, expanded bool, width int) kit.Node {
	before := strings.TrimSpace(fullText[:loc[0]])
	codeBody := strings.TrimSpace(fullText[loc[4]:loc[5]])
	after := strings.TrimSpace(fullText[loc[1]:])

	var full []string
	if before != "" {
		for _, l := range sanitizeDisplayLines(before) {
			full = append(full, p.fg("muted", l))
		}
	}
	codeLines := sanitizeDisplayLines(codeBody)
	for _, l := range codeLines {
		full = append(full, p.fg("toolOutput", l))
	}
	if after != "" {
		for _, l := range sanitizeDisplayLines(after) {
			full = append(full, p.fg("muted", l))
		}
	}
	header := p.renderStatusLine(statusLineOpts{
		iconOverride: p.styledSym(unicodeSymbols.toolLsp, "accent"),
		title:        "LSP",
		description:  desc,
		meta:         baseMeta,
	})
	if expanded || len(full) <= 1 {
		return termLinesNode(fitHeaderedRows(header, full, width), width)
	}
	first := full[0]
	if len(codeLines) > 0 {
		first = p.fg("toolOutput", codeLines[0])
	}
	header += " " + p.formatExpandHint(false, true)
	rows := []string{first, p.fg("muted", formatMoreItems(len(full)-1, "line"))}
	return termLinesNode(fitHeaderedRows(header, rows, width), width)
}

func stripLspLeadingGlyph(s string) string {
	// Strips a leading status glyph token like `✘ `, `⚠ `, `✓ `, `[-] `.
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "[") {
		if idx := strings.Index(s, "] "); idx > 0 && idx <= 6 {
			return strings.TrimSpace(s[idx+2:])
		}
	}
	for i, r := range s {
		if i == 0 && !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '.' && r != '/' && r != '~' && r != '\\' {
			continue
		}
		if r == ' ' && i > 0 {
			return strings.TrimSpace(s[i+1:])
		}
		break
	}
	return s
}

func (p palette) renderLspDiagnostics(desc string, baseMeta []string, lines []string, expanded bool, width int) kit.Node {
	counts := map[string]int{}
	type diagItem struct {
		parsed   bool
		file     string
		line     string
		col      string
		severity string
		msg      string
		raw      string
		color    string
	}
	var failures, unreported, parsedDiags, warnings, other []diagItem
	curDir := ""
	curFile := ""
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "# ") && !strings.HasPrefix(t, "## ") {
			curDir = strings.TrimSuffix(strings.TrimSpace(t[2:]), "/")
			curFile = ""
			continue
		}
		if strings.HasPrefix(t, "## ") {
			fn := strings.TrimSpace(t[3:])
			if curDir != "" && !strings.Contains(fn, "/") {
				curFile = curDir + "/" + fn
			} else {
				curFile = fn
			}
			continue
		}
		if strings.EqualFold(t, "OK") || lspDiagSummaryRe.MatchString(t) {
			continue
		}
		if strings.Contains(t, "all language servers failed") {
			failures = append(failures, diagItem{raw: stripLspLeadingGlyph(t), color: "error"})
			continue
		}
		if strings.Contains(t, "some servers failed") {
			warnings = append(warnings, diagItem{raw: stripLspLeadingGlyph(t), color: "warning"})
			continue
		}
		if lspUnreportedRe.MatchString(t) {
			unreported = append(unreported, diagItem{raw: stripLspLeadingGlyph(t), color: "warning"})
			continue
		}
		if lspFileSummaryRe.MatchString(t) {
			continue
		}
		candidate := t
		if curFile != "" && lspDiagAtRe.MatchString(t) {
			candidate = curFile + ":" + t
		}
		if m := lspDiagLineRe.FindStringSubmatch(candidate); m != nil {
			counts[m[4]]++
			parsedDiags = append(parsedDiags, diagItem{parsed: true, file: m[1], line: m[2], col: m[3], severity: m[4], msg: m[5]})
		} else {
			other = append(other, diagItem{raw: t, color: "muted"})
		}
	}

	items := make([]diagItem, 0, len(failures)+len(unreported)+len(parsedDiags)+len(warnings)+len(other))
	items = append(items, failures...)
	items = append(items, unreported...)
	items = append(items, parsedDiags...)
	items = append(items, warnings...)
	items = append(items, other...)

	meta := append([]string(nil), baseMeta...)
	for _, sev := range []string{"error", "warning", "info", "hint"} {
		if counts[sev] > 0 {
			meta = append(meta, formatCount(sev, counts[sev]))
		}
	}
	if len(failures) > 0 {
		meta = append(meta, formatCount("server failure", len(failures)))
	}
	if len(unreported) > 0 {
		meta = append(meta, formatCount("unverified file", len(unreported)))
	}
	if len(meta) == len(baseMeta) && len(items) == 0 {
		meta = append(meta, "No issues")
	}

	var hOpts statusLineOpts
	switch {
	case counts["error"] > 0 || len(failures) > 0:
		hOpts = statusLineOpts{icon: "error", title: "LSP", description: desc, meta: meta}
	case counts["warning"] > 0 || len(unreported) > 0 || len(warnings) > 0:
		hOpts = statusLineOpts{icon: "warning", title: "LSP", description: desc, meta: meta}
	default:
		hOpts = statusLineOpts{iconOverride: p.styledSym(unicodeSymbols.toolLsp, "accent"), title: "LSP", description: desc, meta: meta}
	}
	header := p.renderStatusLine(hOpts)

	limit := lspCollapsedDiags
	if expanded {
		limit = min(len(items), maxExpandedOutputLines)
	}
	shownCount := min(len(items), limit)
	groups := make([][]string, 0, shownCount)
	for _, it := range items[:shownCount] {
		if !it.parsed {
			groups = append(groups, []string{p.fg(it.color, it.raw)})
			continue
		}
		sevCol := "dim"
		switch it.severity {
		case "error":
			sevCol = "error"
		case "warning":
			sevCol = "warning"
		case "info":
			sevCol = "accent"
		}
		loc := p.fg("muted", langIcon(it.file)) + " " + p.formatStyledPath(it.file) + p.fg("muted", ":"+it.line+":"+it.col)
		head := loc + " " + p.fg(sevCol, "["+it.severity+"]")
		if it.msg == "" {
			groups = append(groups, []string{head})
		} else if expanded {
			groups = append(groups, []string{head, p.fg("muted", it.msg)})
		} else {
			groups = append(groups, []string{head + " " + p.fg("muted", it.msg)})
		}
	}
	summary := ""
	if hidden := len(items) - shownCount; hidden > 0 {
		summary = formatMoreItems(hidden, "diagnostic")
		header += " " + p.formatExpandHint(false, true)
	}
	rows := p.renderTreeGroups(groups, summary)
	return termLinesNode(fitHeaderedRows(header, rows, width), width)
}

func (p palette) renderLspLocations(desc string, baseMeta []string, noun string, count int, lines []string, expanded bool, width int) kit.Node {
	type ctxLine struct {
		n    int
		text string
		mark bool
	}
	type locEntry struct {
		file    string
		line    int
		col     int
		context []ctxLine
	}
	var locs []locEntry
	for _, l := range lines {
		if len(locs) > 0 {
			if m := lspLocationCtxRe.FindStringSubmatch(l); m != nil {
				n, _ := strconv.Atoi(m[1])
				cur := &locs[len(locs)-1]
				cur.context = append(cur.context, ctxLine{n: n, text: strings.TrimRight(m[2], " \t"), mark: n == cur.line})
				continue
			}
		}
		if m := lspLocationRowRe.FindStringSubmatch(l); m != nil {
			ln, _ := strconv.Atoi(m[2])
			cl, _ := strconv.Atoi(m[3])
			locs = append(locs, locEntry{file: m[1], line: ln, col: cl})
		}
	}

	if noun == "reference" {
		var fileOrder []string
		byFile := map[string][]locEntry{}
		for _, loc := range locs {
			if _, seen := byFile[loc.file]; !seen {
				fileOrder = append(fileOrder, loc.file)
			}
			byFile[loc.file] = append(byFile[loc.file], loc)
		}
		meta := append(append([]string(nil), baseMeta...), formatCount("reference", count))
		if len(fileOrder) > 0 {
			meta = append(meta, formatCount("file", len(fileOrder)))
		}
		var hOpts statusLineOpts
		if count > 0 {
			hOpts = statusLineOpts{iconOverride: p.styledSym(unicodeSymbols.toolLsp, "accent"), title: "LSP", description: desc, meta: meta}
		} else {
			hOpts = statusLineOpts{icon: "warning", title: "LSP", description: desc, meta: meta}
		}
		header := p.renderStatusLine(hOpts)

		posW := 0
		for _, loc := range locs {
			if l := len(fmt.Sprintf("%d:%d", loc.line, loc.col)); l > posW {
				posW = l
			}
		}
		maxFiles := lspCollapsedRefFiles
		if expanded {
			maxFiles = min(len(fileOrder), maxExpandedOutputLines/4)
		}
		shownFiles := min(len(fileOrder), maxFiles)
		anyHiddenLocs := false
		groups := make([][]string, 0, shownFiles)
		for _, f := range fileOrder[:shownFiles] {
			flocs := byFile[f]
			if !expanded && len(flocs) > lspCollapsedRefLocs {
				anyHiddenLocs = true
			}
			fhead := p.formatStyledPath(shortenPath(f)) + " " + p.fg("dim", fmt.Sprintf("(%d)", len(flocs)))
			locItems := make([]string, len(flocs))
			for i, loc := range flocs {
				pos := fmt.Sprintf("%*s", posW, fmt.Sprintf("%d:%d", loc.line, loc.col))
				src := ""
				for _, c := range loc.context {
					if c.mark {
						src = strings.TrimSpace(c.text)
						break
					}
				}
				if src == "" {
					locItems[i] = p.fg("muted", pos)
				} else {
					locItems[i] = p.fg("muted", pos+"│") + " " + p.fg("toolOutput", src)
				}
			}
			locRows := p.renderTreeLines(locItems, expanded, lspCollapsedRefLocs, "location")
			groups = append(groups, append([]string{fhead}, locRows...))
		}
		summary := ""
		if hidden := len(fileOrder) - shownFiles; hidden > 0 {
			summary = formatMoreItems(hidden, "file")
		}
		if !expanded && (len(fileOrder) > shownFiles || anyHiddenLocs) {
			header += " " + p.formatExpandHint(false, true)
		}
		rows := p.renderTreeGroups(groups, summary)
		return termLinesNode(fitHeaderedRows(header, rows, width), width)
	}
	// Definition / type definition / implementation locations.
	meta := append(append([]string(nil), baseMeta...), formatCount(noun, count))
	var hOpts statusLineOpts
	if count > 0 {
		hOpts = statusLineOpts{iconOverride: p.styledSym(unicodeSymbols.toolLsp, "accent"), title: "LSP", description: desc, meta: meta}
	} else {
		hOpts = statusLineOpts{icon: "warning", title: "LSP", description: desc, meta: meta}
	}
	header := p.renderStatusLine(hOpts)
	maxLocs := previewLocationsClamp
	if expanded {
		maxLocs = min(len(locs), maxExpandedOutputLines/4)
	}
	shownLocs := min(len(locs), maxLocs)
	groups := make([][]string, 0, shownLocs)
	for _, loc := range locs[:shownLocs] {
		head := p.formatStyledPath(shortenPath(loc.file)) + p.fg("muted", fmt.Sprintf(":%d:%d", loc.line, loc.col))
		item := []string{head}
		numW := 0
		for _, c := range loc.context {
			if l := len(strconv.Itoa(c.n)); l > numW {
				numW = l
			}
		}
		for _, c := range loc.context {
			marker := ""
			if c.mark {
				marker = "*"
			}
			gutter := formatCodeFrameLine(marker, strconv.Itoa(c.n), "", numW)
			if c.mark {
				item = append(item, p.fg("accent", gutter)+p.fg("toolOutput", c.text))
			} else {
				item = append(item, p.fg("dim", gutter)+p.fg("muted", c.text))
			}
		}
		groups = append(groups, item)
	}
	summary := ""
	if hidden := len(locs) - shownLocs; hidden > 0 {
		summary = formatMoreItems(hidden, noun)
		header += " " + p.formatExpandHint(false, true)
	}
	rows := p.renderTreeGroups(groups, summary)
	return termLinesNode(fitHeaderedRows(header, rows, width), width)
}

func (p palette) renderLspSymbols(desc string, baseMeta []string, fileName string, showFile bool, lines []string, expanded bool, width int) kit.Node {
	type symInfo struct {
		icon   string
		name   string
		line   string
		indent int
	}
	var syms []symInfo
	for _, l := range lines {
		if !strings.Contains(l, "@") || !strings.Contains(l, "line") {
			continue
		}
		indent := len(l) - len(strings.TrimLeft(l, " "))
		if m := lspSymbolRowRe.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
			syms = append(syms, symInfo{icon: m[1], name: m[2], line: m[3], indent: indent})
		}
	}
	meta := append(append([]string(nil), baseMeta...), formatCount("symbol", len(syms)))
	if showFile && fileName != "" {
		meta = append(meta, "in "+fileName)
	}
	var hOpts statusLineOpts
	if len(syms) > 0 {
		hOpts = statusLineOpts{iconOverride: p.styledSym(unicodeSymbols.toolLsp, "accent"), title: "LSP", description: desc, meta: meta}
	} else {
		hOpts = statusLineOpts{icon: "warning", title: "LSP", description: desc, meta: meta}
	}
	header := p.renderStatusLine(hOpts)
	if len(syms) == 0 {
		return termWrappedTextNode(header, width)
	}
	if expanded {
		// packages/tui/src/tools/lsp.ts:699-743: nested tree rails per indent depth.
		capN := min(len(syms), maxExpandedOutputLines/2)
		vis := syms[:capN]
		isLast := make([]bool, len(vis))
		var pending []int
		for i := len(vis) - 1; i >= 0; i-- {
			myIndent := vis[i].indent
			for len(pending) > 0 && vis[pending[len(pending)-1]].indent > myIndent {
				pending = pending[:len(pending)-1]
			}
			next := -1
			if len(pending) > 0 {
				next = pending[len(pending)-1]
			}
			isLast[i] = next < 0 || vis[next].indent != myIndent
			pending = append(pending, i)
		}
		lastAtIndent := map[int]int{}
		rows := make([]string, 0, len(vis)*2+1)
		for i, s := range vis {
			var pref strings.Builder
			for lvl := 2; lvl <= s.indent; lvl += 2 {
				if anc, ok := lastAtIndent[lvl-2]; ok && isLast[anc] {
					pref.WriteString("   ")
				} else {
					pref.WriteString(unicodeSymbols.treeVert + "  ")
				}
			}
			lastAtIndent[s.indent] = i
			prefix := p.fg("dim", pref.String())
			br := unicodeSymbols.treeBranch
			detail := unicodeSymbols.treeVert + "  "
			if isLast[i] {
				br = unicodeSymbols.treeLast
				detail = "   "
			}
			rows = append(rows,
				prefix+p.fg("dim", br)+" "+p.fg("accent", s.icon)+" "+p.fg("accent", s.name),
				prefix+p.fg("dim", detail)+p.fg("muted", "line "+s.line),
			)
		}
		if len(syms) > capN {
			rows = append(rows, p.fg("dim", unicodeSymbols.treeLast)+" "+p.fg("muted", formatMoreItems(len(syms)-capN, "symbol")))
		}
		return termLinesNode(fitHeaderedRows(header, rows, width), width)
	}
	// packages/tui/src/tools/lsp.ts:745-756: top-level capped at 3; hidden counts all symbols.
	var top []symInfo
	for _, s := range syms {
		if s.indent == 0 {
			top = append(top, s)
		}
	}
	if len(top) == 0 {
		top = syms
	}
	shown := top[:min(len(top), lspCollapsedSymbols)]
	hidden := len(syms) - len(shown)
	groups := make([][]string, len(shown))
	for i, s := range shown {
		groups[i] = []string{p.fg("accent", s.icon) + " " + p.fg("accent", s.name) + " " + p.fg("muted", "line "+s.line)}
	}
	summary := ""
	if hidden > 0 {
		summary = formatMoreItems(hidden, "symbol")
		header += " " + p.formatExpandHint(false, true)
	}
	rows := p.renderTreeGroups(groups, summary)
	return termLinesNode(fitHeaderedRows(header, rows, width), width)
}

// ═══════════════════════════════════════════════════════════════════════════
// github tool (run_watch + fallback ops)
// upstream: packages/tui/src/tools/github.ts
// ═══════════════════════════════════════════════════════════════════════════

var githubOpTitles = map[string]string{
	"repo_view":      "GitHub Repo",
	"pr_checkout":    "GitHub PR Checkout",
	"pr_push":        "GitHub PR Push",
	"search_issues":  "GitHub Search Issues",
	"search_prs":     "GitHub Search PRs",
	"search_code":    "GitHub Search Code",
	"search_commits": "GitHub Search Commits",
	"search_repos":   "GitHub Search Repos",
	"run_watch":      "GitHub Run Watch",
}

var githubIssueNumRe = regexp.MustCompile(`/(?:issues|pull)/(\d+)`)

func githubOpTitle(op string) string {
	if t, ok := githubOpTitles[op]; ok {
		return t
	}
	return "GitHub"
}

func extractGithubIssueID(val string) string {
	trimmed := strings.TrimSpace(val)
	if trimmed == "" {
		return ""
	}
	allDigits := true
	for _, r := range trimmed {
		if r < '0' || r > '9' {
			allDigits = false
			break
		}
	}
	if allDigits {
		return "#" + trimmed
	}
	if m := githubIssueNumRe.FindStringSubmatch(trimmed); m != nil {
		return "#" + m[1]
	}
	return truncateWidth(flattenLine(trimmed), truncateShort)
}

func formatGithubPrID(raw any) string {
	switch v := raw.(type) {
	case string:
		return extractGithubIssueID(v)
	case []any:
		var parts []string
		for _, item := range v {
			if s, ok := item.(string); ok {
				if id := extractGithubIssueID(s); id != "" {
					parts = append(parts, id)
				}
			}
		}
		if len(parts) == 0 {
			return ""
		}
		if len(parts) > 3 {
			return fmt.Sprintf("%s, +%d more", strings.Join(parts[:3], ", "), len(parts)-3)
		}
		return strings.Join(parts, ", ")
	case float64:
		if v > 0 {
			return fmt.Sprintf("#%d", int(v))
		}
	case int:
		if v > 0 {
			return fmt.Sprintf("#%d", v)
		}
	}
	return ""
}

// githubOpMeta mirrors packages/tui/src/tools/github.ts:88-125.
func githubOpMeta(args map[string]any) []string {
	var meta []string
	op := strField(args, "op")
	switch op {
	case "pr_checkout", "pr_push":
		if id := formatGithubPrID(args["pr"]); id != "" {
			meta = append(meta, id)
		} else if br := strField(args, "branch"); br != "" {
			meta = append(meta, br)
		}
		if r := strField(args, "repo"); r != "" {
			meta = append(meta, r)
		}
	case "search_issues", "search_prs", "search_code", "search_commits":
		if q := strField(args, "query"); q != "" {
			meta = append(meta, truncateWidth(flattenLine(q), truncateContent))
		}
		if r := strField(args, "repo"); r != "" {
			meta = append(meta, r)
		}
	case "search_repos":
		if q := strField(args, "query"); q != "" {
			meta = append(meta, truncateWidth(flattenLine(q), truncateContent))
		}
	case "repo_view":
		if r := strField(args, "repo"); r != "" {
			meta = append(meta, r)
		}
		if br := strField(args, "branch"); br != "" {
			meta = append(meta, br)
		}
	case "run_watch":
		// Watch header carries its own target.
	default:
		if r := strField(args, "repo"); r != "" {
			meta = append(meta, r)
		}
	}
	return meta
}

func shortSHA(sha string) string {
	sha = strings.TrimSpace(sha)
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func ghWatchHeaderMeta(watch map[string]any) string {
	repo := strField(watch, "repo")
	mode := strField(watch, "mode")
	state := strField(watch, "state")
	if mode == "run" {
		if run := asMap(watch["run"]); run != nil {
			id := fmt.Sprint(run["id"])
			if state == "watching" {
				return fmt.Sprintf("watching run #%s on %s", id, repo)
			}
			return fmt.Sprintf("run #%s on %s", id, repo)
		}
	}
	sha := shortSHA(strField(watch, "headSha"))
	if sha == "" {
		sha = "this commit"
	}
	if state == "watching" {
		return fmt.Sprintf("watching %s on %s", sha, repo)
	}
	return fmt.Sprintf("workflow runs for %s on %s", sha, repo)
}

func ghJobVisual(p palette, job map[string]any) (icon, color string) {
	concl := strings.ToLower(strField(job, "conclusion"))
	st := strings.ToLower(strField(job, "status"))
	switch concl {
	case "success", "neutral", "skipped":
		return p.statusIcon("success"), "success"
	case "failure", "timed_out", "cancelled", "action_required", "startup_failure":
		return p.statusIcon("error"), "error"
	}
	switch st {
	case "in_progress":
		return p.statusIcon("running"), "warning"
	case "queued", "pending", "waiting", "requested":
		return p.statusIcon("pending"), "muted"
	default:
		return p.fg("muted", unicodeSymbols.statusShadowed), "muted"
	}
}

func ghStepStateLabel(step map[string]any) string {
	s := strField(step, "conclusion", "status")
	if s == "" {
		return "unknown"
	}
	return strings.ReplaceAll(flattenLine(s), "_", " ")
}

func (p palette) renderGhRunBlock(run map[string]any, innerW int, expanded bool) []string {
	wf := strField(run, "workflowName", "displayTitle")
	if wf == "" {
		wf = "GitHub Actions"
	}
	parts := []string{p.fg("accent", replaceTabs(wf))}
	if br := strField(run, "branch"); br != "" {
		parts = append(parts, p.fg("text", replaceTabs(br)))
	} else if sha := shortSHA(strField(run, "headSha")); sha != "" {
		parts = append(parts, p.fg("text", sha))
	}
	if run["id"] != nil {
		parts = append(parts, p.fg("muted", fmt.Sprintf("#%v", run["id"])))
	}
	lines := []string{strings.Join(parts, "  ")}

	jobs := asSlice(run["jobs"])
	if len(jobs) == 0 {
		lines = append(lines, p.fg("dim", "waiting for workflow jobs..."))
		return lines
	}
	hintShown := false
	for _, jr := range jobs {
		jm := asMap(jr)
		icon, col := ghJobVisual(p, jm)
		prefix := icon + " "
		durStr := ""
		if d, ok := intField(jm, "durationSeconds"); ok {
			durStr = p.fg(col, fmt.Sprintf("%ds", d))
		}
		resW := tui.VisibleWidth(prefix)
		if durStr != "" {
			resW += 1 + tui.VisibleWidth(durStr)
		}
		nameW := max(8, innerW-resW)
		jline := prefix + p.fg(col, truncateWidth(flattenLine(strField(jm, "name")), nameW))
		if durStr != "" {
			pad := max(1, innerW-tui.VisibleWidth(jline)-tui.VisibleWidth(durStr))
			jline += strings.Repeat(" ", pad) + durStr
		}
		lines = append(lines, clampRow(jline, innerW))

		steps := asSlice(jm["steps"])
		if len(steps) == 0 {
			continue
		}
		if expanded {
			for _, sr := range steps {
				sm := asMap(sr)
				sIcon, sCol := ghJobVisual(p, sm)
				num, _ := intField(sm, "number")
				stateLbl := strings.ReplaceAll(strField(sm, "conclusion", "status"), "_", " ")
				if stateLbl == "" {
					stateLbl = "unknown"
				}
				durS := ""
				if sd, ok := intField(sm, "durationSeconds"); ok {
					durS = "  " + p.fg("muted", fmt.Sprintf("%ds", sd))
				}
				spref := fmt.Sprintf("    %s #%d ", sIcon, num)
				ssuff := "  " + p.fg(sCol, stateLbl) + durS
				snW := max(0, innerW-tui.VisibleWidth(spref)-tui.VisibleWidth(ssuff))
				lines = append(lines, clampRow(spref+p.fg(sCol, truncateWidth(flattenLine(strField(sm, "name")), snW))+ssuff, innerW))
			}
		} else {
			completed := 0
			allSuccess := true
			var curStep, failedStep, pendingStep map[string]any
			curPos, pendPos := 0, 0
			for si, sr := range steps {
				sm := asMap(sr)
				concl := strings.ToLower(strField(sm, "conclusion"))
				st := strings.ToLower(strField(sm, "status"))
				if concl != "" || st == "completed" {
					completed++
				}
				if concl != "success" && concl != "neutral" && concl != "skipped" {
					allSuccess = false
				}
				if curStep == nil && concl == "" && st == "in_progress" {
					curStep = sm
					curPos = si + 1
				}
				if failedStep == nil && (concl == "failure" || concl == "timed_out" || concl == "cancelled" || concl == "action_required" || concl == "startup_failure") {
					failedStep = sm
				}
				if pendingStep == nil && concl == "" && (st == "queued" || st == "requested" || st == "waiting" || st == "pending") {
					pendingStep = sm
					pendPos = si + 1
				}
			}
			if curStep == nil && failedStep == nil && pendingStep != nil {
				curStep = pendingStep
				curPos = pendPos
			}
			var lbl string
			sIcon, sCol := p.statusIcon("pending"), "muted"
			switch {
			case curStep != nil:
				sIcon, sCol = ghJobVisual(p, curStep)
				num, _ := intField(curStep, "number")
				lbl = fmt.Sprintf("%d/%d #%d %s · %s", curPos, len(steps), num, flattenLine(strField(curStep, "name")), ghStepStateLabel(curStep))
			case failedStep != nil:
				sIcon, sCol = ghJobVisual(p, failedStep)
				num, _ := intField(failedStep, "number")
				lbl = fmt.Sprintf("#%d %s · %s · %d/%d", num, flattenLine(strField(failedStep, "name")), ghStepStateLabel(failedStep), completed, len(steps))
			default:
				if allSuccess {
					sIcon, sCol = p.statusIcon("success"), "success"
				}
				lbl = fmt.Sprintf("%d/%d steps complete", completed, len(steps))
			}
			pref := "  " + p.fg("dim", "↳") + " " + sIcon + " "
			hint := ""
			if !hintShown {
				hint = " " + p.formatExpandHint(false, true)
				hintShown = true
			}
			lw := max(0, innerW-tui.VisibleWidth(pref)-tui.VisibleWidth(hint))
			lines = append(lines, clampRow(pref+p.fg(sCol, truncateWidth(lbl, lw))+hint, innerW))
		}
	}
	return lines
}

func termGithubCall(ctx sdk.Context, args map[string]any, _ sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	op := strings.TrimSpace(strField(args, "op"))
	if op == "run_watch" {
		// packages/tui/src/tools/github.ts:494-516 (renderWatchCall)
		runID := strings.TrimSpace(strField(args, "run"))
		branch := strings.TrimSpace(strField(args, "branch"))
		var metaText string
		switch {
		case runID != "":
			metaText = p.fg("muted", "#"+runID)
		case branch != "":
			metaText = p.fg("text", flattenLine(branch))
		default:
			metaText = p.fg("muted", "current HEAD")
		}
		header := p.statusIcon("pending") + " " + p.fg("accent", "GitHub Run Watch") + "  " + metaText
		waitLine := p.fg("dim", "waiting for workflow data...")
		return termLinesNode(fitHeaderedRows(header, []string{waitLine}, width), width), nil
	}
	header := p.renderStatusLine(statusLineOpts{icon: "pending", title: githubOpTitle(op), meta: githubOpMeta(args)})
	return termWrappedTextNode(header, width), nil
}

func termGithubResult(ctx sdk.Context, res sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	details := asMap(res.Details)
	if watch := asMap(details["watch"]); watch != nil {
		watchMeta := ghWatchHeaderMeta(watch)
		var hOpts statusLineOpts
		border := "borderMuted"
		switch {
		case opts.IsPartial:
			hOpts = statusLineOpts{icon: "running", title: "GitHub Run Watch", meta: []string{watchMeta}}
		case rc.IsError:
			border = "error"
			hOpts = statusLineOpts{icon: "error", title: "GitHub Run Watch", titleColor: "error", meta: []string{watchMeta}}
		default:
			hOpts = statusLineOpts{iconOverride: p.styledSym(unicodeSymbols.toolGh, "accent"), title: "GitHub Run Watch", meta: []string{watchMeta}}
		}
		header := p.renderStatusLine(hOpts)
		innerW := outputBlockContentWidth(width, nil, nil)

		var mainLines []string
		if note := strField(watch, "note"); note != "" {
			mainLines = append(mainLines, p.fg("dim", replaceTabs(note)))
		}
		mode := strField(watch, "mode")
		if mode == "run" {
			if run := asMap(watch["run"]); run != nil {
				mainLines = append(mainLines, p.renderGhRunBlock(run, innerW, opts.Expanded)...)
			}
		} else {
			runs := asSlice(watch["runs"])
			if len(runs) == 0 {
				mainLines = append(mainLines, p.fg("dim", "waiting for workflow runs..."))
			} else {
				for ri, rr := range runs {
					if ri > 0 {
						mainLines = append(mainLines, "")
					}
					mainLines = append(mainLines, p.renderGhRunBlock(asMap(rr), innerW, opts.Expanded)...)
				}
			}
		}

		var sections []blockSection
		if len(mainLines) > 0 {
			sections = append(sections, blockSection{lines: mainLines})
		}

		if failedLogs := asSlice(watch["failedLogs"]); len(failedLogs) > 0 {
			var fLines []string
			for _, flr := range failedLogs {
				fl := asMap(flr)
				wf := strField(fl, "workflowName")
				ctxLbl := fmt.Sprintf("run #%v", fl["runId"])
				if wf != "" {
					ctxLbl = fmt.Sprintf("%s  #%v", wf, fl["runId"])
				}
				fLines = append(fLines, fmt.Sprintf("%s %s  %s",
					p.statusIcon("error"),
					p.fg("error", replaceTabs(strField(fl, "jobName"))),
					p.fg("muted", ctxLbl),
				))
				avail, _ := boolField(fl, "available")
				tail := strField(fl, "tail")
				if !avail || tail == "" {
					reason := strField(fl, "unavailableReason")
					msg := "log tail unavailable"
					if reason != "" {
						msg += ": " + flattenLine(reason)
					}
					fLines = append(fLines, p.fg("dim", "  "+msg))
					continue
				}
				var tailLines []string
				for _, tl := range sanitizeDisplayLines(tail) {
					if tl != "" {
						tailLines = append(tailLines, tl)
					}
				}
				lim := previewOutputCollapse
				if opts.Expanded {
					lim = min(len(tailLines), maxExpandedOutputLines)
				}
				start := max(0, len(tailLines)-lim)
				for _, tl := range tailLines[start:] {
					fLines = append(fLines, p.fg("dim", "  "+truncateWidth(tl, max(8, innerW-2))))
				}
				if !opts.Expanded && start > 0 {
					fLines = append(fLines, p.fg("dim", fmt.Sprintf("  … %d more log lines %s", start, p.formatExpandHint(false, true))))
				}
			}
			sections = append(sections, blockSection{label: "failed logs", lines: fLines})
		}

		ghWatchState := "success"
		if opts.IsPartial {
			ghWatchState = "partial"
		} else if rc.IsError {
			ghWatchState = "error"
		}
		rows := p.renderOutputBlock(blockOpts{header: header, state: ghWatchState, noBg: true, borderColor: border, sections: sections, width: width})
		return termLinesNode(rows, width), nil
	}

	// Fallback non-watch github op.
	op := strings.TrimSpace(strField(rc.Args, "op"))
	title := githubOpTitle(op)
	meta := githubOpMeta(rc.Args)
	text := strings.TrimSpace(resultText(res))
	var hOpts statusLineOpts
	border := "borderMuted"
	switch {
	case rc.IsError:
		border = "error"
		hOpts = statusLineOpts{icon: "error", title: title, titleColor: "error", meta: meta}
	case text == "":
		hOpts = statusLineOpts{icon: "warning", title: title, meta: meta}
	default:
		hOpts = statusLineOpts{iconOverride: p.styledSym(unicodeSymbols.toolGh, "accent"), title: title, meta: meta}
	}
	header := p.renderStatusLine(hOpts)
	if text == "" {
		empty := "no output"
		if rc.IsError {
			empty = "request failed"
		}
		return termLinesNode(fitHeaderedRows(header, []string{p.fg("dim", empty)}, width), width), nil
	}
	allLines := sanitizeDisplayLines(text)
	for len(allLines) > 0 && strings.TrimSpace(allLines[0]) == "" {
		allLines = allLines[1:]
	}
	for len(allLines) > 0 && strings.TrimSpace(allLines[len(allLines)-1]) == "" {
		allLines = allLines[:len(allLines)-1]
	}
	// Trivial one-line success renders unframed (packages/tui/src/tools/github.ts:460-465).
	if len(allLines) <= 1 && !rc.IsError {
		if len(allLines) == 0 || allLines[0] == "" {
			return termWrappedTextNode(header, width), nil
		}
		return termLinesNode(fitHeaderedRows(header, []string{p.fg("toolOutput", allLines[0])}, width), width), nil
	}
	tok := "toolOutput"
	if rc.IsError {
		tok = "error"
	}
	shown, _ := p.formatOutputPaneLines(outputPaneOpts{
		lines:             allLines,
		expanded:          opts.Expanded,
		collapsedMaxLines: previewOutputExpand,
		styleLine:         func(l string) string { return p.fg(tok, l) },
	})
	ghState := "success"
	if rc.IsError {
		ghState = "error"
	}
	rows := p.renderOutputBlock(blockOpts{
		header:      header,
		state:       ghState,
		noBg:        true, // packages/tui/src/tools/github.ts:489 (applyBg: false)
		borderColor: border,
		sections:    []blockSection{{lines: shown}},
		width:       width,
	})
	return termLinesNode(rows, width), nil
}

// ═══════════════════════════════════════════════════════════════════════════
// debug tool
// upstream: packages/tui/src/tools/debug.ts
// ═══════════════════════════════════════════════════════════════════════════

func summarizeDebugCall(args map[string]any) string {
	action := strings.ReplaceAll(flattenLine(strField(args, "action")), "_", " ")
	if action == "" {
		action = "request"
	}
	target := strField(args, "program")
	if target == "" {
		if f := strField(args, "file"); f != "" {
			if ln, ok := intField(args, "line"); ok {
				target = fmt.Sprintf("%s:%d", f, ln)
			}
		}
	}
	if target == "" {
		target = strField(args, "function", "expression", "command", "memory_reference", "instruction_reference", "data_id", "name")
	}
	if target != "" {
		return action + " " + truncateWidth(flattenLine(target), truncateTitle)
	}
	return action
}

func formatDebugSnapshot(snap map[string]any) []string {
	if snap == nil {
		return nil
	}
	var lines []string
	if id := strField(snap, "id"); id != "" {
		lines = append(lines, "Session "+id)
	}
	if a := strField(snap, "adapter"); a != "" {
		lines = append(lines, "Adapter: "+a)
	}
	if s := strField(snap, "status"); s != "" {
		lines = append(lines, "Status: "+s)
	}
	if c := strField(snap, "cwd"); c != "" {
		lines = append(lines, "CWD: "+c)
	}
	if pr := strField(snap, "program"); pr != "" {
		lines = append(lines, "Program: "+pr)
	}
	if sr := strField(snap, "stopReason"); sr != "" {
		lines = append(lines, "Stop reason: "+sr)
	}
	if fn := strField(snap, "frameName"); fn != "" {
		lines = append(lines, "Frame: "+fn)
	}
	if ip := strField(snap, "instructionPointerReference"); ip != "" {
		lines = append(lines, "Instruction pointer: "+ip)
	}
	if src := asMap(snap["source"]); src != nil {
		if sp := strField(src, "path"); sp != "" {
			if ln, ok := intField(snap, "line"); ok {
				loc := fmt.Sprintf("%s:%d", sp, ln)
				if col, okC := intField(snap, "column"); okC {
					loc += fmt.Sprintf(":%d", col)
				}
				lines = append(lines, "Location: "+loc)
			}
		}
	}
	if cfg, _ := boolField(snap, "needsConfigurationDone"); cfg {
		lines = append(lines, "Configuration: pending configurationDone; set breakpoints, then continue.")
	}
	if ec, ok := intField(snap, "exitCode"); ok {
		lines = append(lines, fmt.Sprintf("Exit code: %d", ec))
	}
	return lines
}

func termDebugCall(ctx sdk.Context, args map[string]any, _ sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	header := p.renderStatusLine(statusLineOpts{icon: "pending", title: "Debug", description: summarizeDebugCall(args)})
	return termWrappedTextNode(header, width), nil
}

func termDebugResult(ctx sdk.Context, res sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	details := asMap(res.Details)
	action := strField(rc.Args, "action")
	if action == "" {
		action = strField(details, "action")
	}
	if action == "" {
		action = "debug"
	}
	action = strings.ReplaceAll(flattenLine(action), "_", " ")
	var icon string
	border := "dim"
	switch {
	case opts.IsPartial:
		icon = p.statusIcon("running")
		border = "accent"
	case rc.IsError:
		icon = p.statusIcon("error")
		border = "error"
	default:
		icon = p.styledSym(unicodeSymbols.toolDebug, "accent")
	}
	header := icon + " Debug " + action

	var sections []blockSection
	if snap := asMap(details["snapshot"]); snap != nil {
		if sl := formatDebugSnapshot(snap); len(sl) > 0 {
			sections = append(sections, blockSection{label: p.fg("toolTitle", "Session"), lines: sl})
		}
	}
	txt := resultText(res)
	if txt == "" {
		txt = "No output"
	}
	rawLines := sanitizeDisplayLines(txt)
	lim := previewCollapsedLines
	if opts.Expanded {
		lim = previewExpandedLines
	}
	shownCount := min(len(rawLines), lim)
	displayed := make([]string, 0, shownCount+1)
	for _, l := range rawLines[:shownCount] {
		displayed = append(displayed, clipCollapsed(l, truncateLine, opts.Expanded))
	}
	if rem := len(rawLines) - shownCount; rem > 0 {
		displayed = append(displayed, p.fg("muted", fmt.Sprintf("… %d more lines %s", rem, p.formatExpandHint(opts.Expanded, true))))
	}
	sections = append(sections, blockSection{label: p.fg("toolTitle", "Output"), lines: displayed})

	dbgState := "success"
	if opts.IsPartial {
		dbgState = "partial"
	} else if rc.IsError {
		dbgState = "error"
	}
	rows := p.renderOutputBlock(blockOpts{header: header, state: dbgState, noBg: true, borderColor: border, sections: sections, width: width})
	return termLinesNode(rows, width), nil
}

// ═══════════════════════════════════════════════════════════════════════════
// wait tool
// upstream: packages/tui/src/tools/wait.ts
// ═══════════════════════════════════════════════════════════════════════════

func termWaitCall(ctx sdk.Context, _ map[string]any, _ sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	return termWrappedTextNode(p.renderStatusLine(statusLineOpts{icon: "pending", title: "Wait"}), width), nil
}

func waitJobStatusRank(st string) int {
	switch st {
	case "running":
		return 0
	case "failed":
		return 1
	case "cancelled":
		return 2
	default:
		return 3
	}
}

func waitStatusIcon(st string) string {
	switch st {
	case "running":
		return "running"
	case "failed":
		return "error"
	case "cancelled":
		return "aborted"
	default:
		return "success"
	}
}

func waitStatusColor(st string) string {
	switch st {
	case "running":
		return "accent"
	case "failed":
		return "error"
	case "cancelled":
		return "warning"
	default:
		return "success"
	}
}

var waitTaskResultEnvRe = regexp.MustCompile(`(?s)<(output|preview)(?:\s[^>]*)?>\n?(.*?)\n?</(?:output|preview)>`)

func stripWaitTaskResultEnvelope(text string) string {
	if !strings.HasPrefix(text, "<task-result") {
		return text
	}
	if m := waitTaskResultEnvRe.FindStringSubmatch(text); m != nil {
		if body := strings.TrimSpace(m[2]); body != "" {
			return body
		}
	}
	return text
}

func flattenWaitStructuredPreview(text string) string {
	if text == "" || (text[0] != '{' && text[0] != '[') {
		return text
	}
	if len(text) > 640 {
		text = text[:640]
	}
	return strings.Join(strings.Fields(text), " ")
}

func waitJobRowLabel(job map[string]any) string {
	if strField(job, "type") == "task" && strField(job, "status") == "running" {
		if st := strings.TrimSpace(strField(job, "stage")); st != "" {
			return st
		}
	}
	// packages/tui/src/tools/job-label.ts:4-5: show only the intent label before `: `.
	if rawIntent := strings.TrimSpace(strField(job, "intent")); rawIntent != "" {
		return intent.Label(rawIntent)
	}
	return strField(job, "label", "description")
}

func firstLineTrimmed(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	first, _, _ := strings.Cut(s, "\n")
	return strings.TrimSpace(replaceTabs(sanitizeDisplayText(first)))
}

func termWaitResult(ctx sdk.Context, res sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	details := asMap(res.Details)
	// packages/tui/src/tools/wait.ts:915-920: interrupted wait renders one info row.
	if inter, _ := boolField(details, "interrupted"); inter && !rc.IsError {
		header := p.renderStatusLine(statusLineOpts{
			icon:  "info",
			title: "Wait",
			meta:  []string{"interrupted by message"},
		})
		return termWrappedTextNode(header, width), nil
	}
	rawJobs := asSlice(details["jobs"])
	rawAgents := asSlice(details["agents"])

	if len(rawJobs) == 0 && len(rawAgents) == 0 {
		fb := strings.TrimSpace(resultText(res))
		if fb == "" {
			fb = "No jobs to process"
		}
		header := p.renderStatusLine(statusLineOpts{icon: "warning", title: "Wait"})
		// packages/tui/src/tools/wait.ts:288: Text wraps long fallback sentences.
		return termWrappedTextNode(header+"\n"+p.formatEmptyMessage(fb), width), nil
	}
	jobs := make([]map[string]any, 0, len(rawJobs))
	var completed, failed, cancelled, running int
	for _, r := range rawJobs {
		jm := asMap(r)
		if jm == nil {
			continue
		}
		jobs = append(jobs, jm)
		switch strField(jm, "status") {
		case "completed", "done":
			completed++
		case "failed":
			failed++
		case "cancelled":
			cancelled++
		default:
			running++
		}
	}

	var meta []string
	if completed > 0 {
		meta = append(meta, p.fg("success", fmt.Sprintf("%d done", completed)))
	}
	if failed > 0 {
		meta = append(meta, p.fg("error", fmt.Sprintf("%d failed", failed)))
	}
	if cancelled > 0 {
		meta = append(meta, p.fg("warning", fmt.Sprintf("%d cancelled", cancelled)))
	}
	if len(rawAgents) > 0 && len(jobs) > 0 {
		meta = append(meta, p.fg("accent", formatCount("agent", len(rawAgents))))
	}

	hIcon := "success"
	if failed > 0 {
		hIcon = "warning"
	} else if running > 0 || len(rawAgents) > 0 {
		hIcon = "info"
	}

	var title string
	jobsNoun := pluralize("job", len(jobs))
	switch {
	case len(jobs) == 0:
		title = fmt.Sprintf("%s — no jobs", formatCount("running agent", len(rawAgents)))
	case running > 0 && running == len(jobs):
		title = fmt.Sprintf("waiting on %d %s", len(jobs), jobsNoun)
	case running > 0:
		title = fmt.Sprintf("waiting on %d of %d %s", running, len(jobs), jobsNoun)
	default:
		title = fmt.Sprintf("%d %s settled", len(jobs), jobsNoun)
	}
	header := p.renderStatusLine(statusLineOpts{icon: hIcon, title: title, meta: meta})

	// Sort: running first, then failed, cancelled, completed; ties by duration desc (wait.ts:339-349).
	slices.SortStableFunc(jobs, func(a, b map[string]any) int {
		ra, rb := waitJobStatusRank(strField(a, "status")), waitJobStatusRank(strField(b, "status"))
		if ra != rb {
			return ra - rb
		}
		da, _ := int64Field(a, "durationMs")
		db, _ := int64Field(b, "durationMs")
		if db > da {
			return 1
		}
		if db < da {
			return -1
		}
		return 0
	})

	var settled []map[string]any
	for _, j := range jobs {
		if strField(j, "status") != "running" {
			settled = append(settled, j)
		}
	}

	compactView := !opts.Expanded && len(jobs) > 0
	var itemLines []string
	if compactView {
		// Compact settled rows (wait.ts:392-465).
		shownSettled := min(len(settled), previewCollapsedItems)
		indent := strings.Repeat(" ", tui.VisibleWidth(p.statusIcon("success"))+1)
		for _, j := range settled[:shownSettled] {
			st := strField(j, "status")
			word := st
			if word == "completed" {
				word = "done"
			}
			exitPart := ""
			if ec, ok := intField(j, "exitCode"); ok && ec != 0 {
				exitPart = unicodeSymbols.sepDot + p.fg("error", fmt.Sprintf("exit %d", ec))
			}
			durMs, _ := int64Field(j, "durationMs")
			outcome := " " + p.fg(waitStatusColor(st), word) + exitPart + unicodeSymbols.sepDot + p.fg("dim", formatDuration(durMs))
			icon := p.statusIcon(waitStatusIcon(st))
			idW := max(1, width-tui.VisibleWidth(icon+" ")-tui.VisibleWidth(outcome))
			jid := truncateWidth(flattenLine(strField(j, "id")), idW)
			line := icon + " " + p.fg("accent", jid) + outcome
			if lbl := firstLineTrimmed(waitJobRowLabel(j)); lbl != "" && lbl != strField(j, "id") {
				if rem := width - tui.VisibleWidth(line) - 2; rem >= 8 {
					line += "  " + p.fg("muted", truncateWidth(lbl, rem))
				}
			}
			itemLines = append(itemLines, clampRow(line, width))
			if st == "failed" {
				if errTxt := strings.TrimSpace(stripOutputNotice(strField(j, "errorText"), asMap(j["meta"]))); errTxt != "" {
					for _, l := range sanitizeDisplayLines(errTxt) {
						if t := strings.TrimSpace(l); t != "" {
							itemLines = append(itemLines, clampRow(indent+p.fg("error", truncateWidth(t, max(1, width-len(indent)))), width))
							break
						}
					}
				}
			}
			artErr := strField(asMap(j["meta"]), "artifactError")
			if artErr == "" {
				artErr = strField(j, "artifactError")
			}
			if artErr != "" {
				itemLines = append(itemLines, clampRow(p.fg("warning", fmt.Sprintf("Full output was not saved completely (artifact %s failed)", artErr)), width))
			}
		}
		if hidden := len(settled) - shownSettled; hidden > 0 {
			itemLines = append(itemLines, indent+p.fg("dim", fmt.Sprintf("… %d more settled", hidden)))
		}
		if running > 0 && len(settled) > 0 {
			itemLines = append(itemLines, indent+p.fg("dim", fmt.Sprintf("%d still running", running)))
		}
	} else if len(jobs) > 0 {
		// Expanded tree view with width-budgeted ids, continuation labels, and
		// 4-line trimmed non-empty previews (wait.ts:491-602).
		capJobs := min(len(jobs), maxExpandedOutputLines/6)
		groups := make([][]string, 0, capJobs)
		maxPrev := 4
		maxLabelLines := 3
		if !opts.Expanded {
			maxPrev = 1
			maxLabelLines = 1
		}
		// Tree connector `├─ ` / `└─ ` takes 3 columns.
		rowWidth := max(1, width-3)
		contWidth := max(1, rowWidth-2)
		for _, j := range jobs[:capJobs] {
			st := strField(j, "status")
			durMs, _ := int64Field(j, "durationMs")
			exitPart := ""
			if ec, ok := intField(j, "exitCode"); ok {
				col := "muted"
				if ec != 0 {
					col = "error"
				}
				exitPart = unicodeSymbols.sepDot + p.fg(col, fmt.Sprintf("exit %d", ec))
			}
			completion := ""
			if st == "running" {
				if pct, ok := intField(j, "completionPercent"); ok {
					completion = unicodeSymbols.sepDot + p.fg("accent", fmt.Sprintf("%d%%", pct))
				}
			}
			iconPart := p.statusIcon(waitStatusIcon(st)) + exitPart
			typeBadge := p.formatBadge(strField(j, "type"), waitStatusColor(st))
			rowPrefix := iconPart + " " + typeBadge + " "
			durSuffix := completion + unicodeSymbols.sepDot + p.fg("dim", formatDuration(durMs))
			idBudget := max(1, rowWidth-tui.VisibleWidth(rowPrefix)-tui.VisibleWidth(durSuffix))
			jid := truncateWidth(flattenLine(strField(j, "id")), idBudget)

			rowLabel := waitJobRowLabel(j)
			labelSource := rowLabel
			if strings.TrimSpace(labelSource) == "" {
				labelSource = "(no label)"
			}
			rawLabelLines := sanitizeDisplayLines(labelSource)
			shownLabelCount := min(len(rawLabelLines), maxLabelLines)
			visLabelLines := make([]string, shownLabelCount)
			for li := range shownLabelCount {
				visLabelLines[li] = truncateWidth(rawLabelLines[li], 60)
			}
			if len(rawLabelLines) > maxLabelLines && len(visLabelLines) > 0 {
				visLabelLines[len(visLabelLines)-1] += " …"
			}

			head := rowPrefix + p.fg("toolOutput", jid)
			distinctLabel := strings.TrimSpace(rowLabel) != strField(j, "id")
			inlineLabel := distinctLabel && len(visLabelLines) > 0 &&
				tui.VisibleWidth(head+" "+visLabelLines[0]+durSuffix) <= rowWidth
			if inlineLabel {
				head += " " + p.fg("toolOutput", visLabelLines[0])
			}
			head += durSuffix
			item := []string{clampRow(head, rowWidth)}
			startLabelIdx := 1
			if distinctLabel && !inlineLabel {
				startLabelIdx = 0
			}
			for li := startLabelIdx; li < len(visLabelLines); li++ {
				item = append(item, "  "+p.fg("toolOutput", truncateWidth(visLabelLines[li], contWidth)))
			}

			artErr := strField(asMap(j["meta"]), "artifactError")
			if artErr == "" {
				artErr = strField(j, "artifactError")
			}
			if artErr != "" {
				item = append(item, p.fg("warning", truncateWidth(fmt.Sprintf("Full output was not saved completely (artifact %s failed)", artErr), rowWidth)))
			}
			prevRaw := strings.TrimSpace(strField(j, "errorText"))
			tone := "error"
			if prevRaw == "" {
				prevRaw = strings.TrimSpace(strField(j, "resultText"))
				tone = "dim"
			}
			if prevRaw != "" {
				cleaned := flattenWaitStructuredPreview(stripWaitTaskResultEnvelope(strings.TrimSpace(stripOutputNotice(prevRaw, asMap(j["meta"])))))
				var nonEmpty []string
				for _, l := range sanitizeDisplayLines(cleaned) {
					if t := strings.TrimSpace(l); t != "" {
						nonEmpty = append(nonEmpty, t)
					}
				}
				for _, l := range nonEmpty[:min(len(nonEmpty), maxPrev)] {
					item = append(item, "  "+p.fg(tone, truncateWidth(l, max(1, min(80, contWidth)))))
				}
			}
			groups = append(groups, item)
		}
		summary := ""
		if len(jobs) > capJobs {
			summary = formatMoreItems(len(jobs)-capJobs, "job")
		}
		itemLines = p.renderTreeGroups(groups, summary)
	}

	// Standalone running agents roster (packages/tui/src/tools/wait.ts:604-643).
	var agentLines []string
	if len(rawAgents) > 0 {
		maxAgents := previewCollapsedItems
		if opts.Expanded {
			maxAgents = min(len(rawAgents), maxExpandedOutputLines/4)
		}
		shownA := min(len(rawAgents), maxAgents)
		agentRowW := max(1, width-3)
		groups := make([][]string, 0, shownA)
		for _, ra := range rawAgents[:shownA] {
			am := asMap(ra)
			if am == nil {
				continue
			}
			live, _ := boolField(am, "live")
			icon := p.statusIcon("warning")
			badge := p.formatBadge("agent · no turn", "warning")
			if live {
				icon = p.statusIcon("running")
				badge = p.formatBadge("agent", "accent")
			}
			idBudget := max(1, agentRowW-tui.VisibleWidth(icon+"  "+badge))
			aid := truncateWidth(flattenLine(strField(am, "id")), idBudget)
			row := icon + " " + p.fg("muted", aid) + " " + badge
			if act := flattenLine(strField(am, "activity")); act != "" {
				row += " " + p.fg("toolOutput", truncateWidth(act, 60))
			}
			ageMs, _ := int64Field(am, "ageMs")
			row += " " + p.fg("dim", formatDuration(ageMs))
			if pid := flattenLine(strField(am, "parentId")); pid != "" {
				row += p.fg("dim", " ← "+pid)
			}
			groups = append(groups, []string{clampRow(row, agentRowW)})
		}
		summary := ""
		if hidden := len(rawAgents) - shownA; hidden > 0 {
			summary = formatMoreItems(hidden, "agent")
		}
		agentLines = p.renderTreeGroups(groups, summary)
	}

	// packages/tui/src/tools/wait.ts:331-337: suppress aggregate root artifactError
	// when a job row already owns the same error (unless source.type == "report").
	var aggErr string
	if outMeta := asMap(details["meta"]); outMeta != nil {
		if ae := strField(outMeta, "artifactError"); ae != "" {
			isReport := strField(asMap(outMeta["source"]), "type") == "report"
			ownedByJob := false
			if !isReport {
				for _, j := range jobs {
					je := strField(asMap(j["meta"]), "artifactError")
					if je == "" {
						je = strField(j, "artifactError")
					}
					if je == ae {
						ownedByJob = true
						break
					}
				}
			}
			if isReport || !ownedByJob {
				aggErr = ae
			}
		}
	}

	// packages/tui/src/tools/wait.ts:645: omit header in collapsed compact view when settled jobs exist.
	var all []string
	if !(compactView && len(settled) > 0) {
		all = append(all, wrapAnsi(header, width)...)
	}
	if aggErr != "" {
		all = append(all, p.fg("warning", fmt.Sprintf("Full output was not saved completely (artifact %s failed)", aggErr)))
	}
	all = append(all, itemLines...)
	all = append(all, agentLines...)
	return termLinesNode(all, width), nil
}

// ═══════════════════════════════════════════════════════════════════════════
// resolve / reject / propose / report_issue
// upstream: packages/tui/src/tools/resolve.ts, report-tool-issue.ts
// ═══════════════════════════════════════════════════════════════════════════

func termResolveCall(defaultAction string) CallFn {
	return func(ctx sdk.Context, args map[string]any, _ sdk.ToolRenderContext, width int) (kit.Node, error) {
		p := newPalette(ctx)
		action := strField(args, "action")
		if action == "" {
			action = defaultAction
		}
		badgeLbl, badgeCol := "proposed -> rejected", "warning"
		if action == "apply" {
			badgeLbl, badgeCol = "proposed -> resolved", "success"
		}
		var meta []string
		if r := strings.TrimSpace(strField(args, "reason")); r != "" {
			meta = []string{p.fg("muted", truncateOmit(r, 72))}
		}
		header := p.renderStatusLine(statusLineOpts{
			icon:        "pending",
			title:       "Resolve",
			description: action,
			badgeLabel:  badgeLbl,
			badgeColor:  badgeCol,
			meta:        meta,
		})
		return termWrappedTextNode(header, width), nil
	}
}

// termResolveResult mirrors packages/tui/src/tools/resolve.ts:155-195:
// five-line inverse banner (`Accept:` / `Discard:` / `Failed:`).
func termResolveResult(ctx sdk.Context, res sdk.ToolRenderResult, _ sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	out := p.renderResolveBannerLines(asMap(res.Details), rc.IsError, width)
	if len(out) == 0 {
		return nil, nil
	}
	return termLinesNode(out, width), nil
}

// renderResolveBannerLines builds omp's five-line inverse banner for resolve/reject
// (shared by direct `resolve`/`reject` tool cards and `bash` nested `toolCommands`).
// upstream: packages/tui/src/tools/resolve.ts:155-195
func (p palette) renderResolveBannerLines(details map[string]any, isError bool, width int) []string {
	if width <= 0 {
		return nil
	}
	label := replaceTabs(strField(details, "label"))
	if label == "" {
		label = "pending action"
	}
	reason := flattenHeader(replaceTabs(strings.TrimSpace(strField(details, "reason"))))
	if reason == "" {
		reason = "No reason provided"
	}
	action := strField(details, "action")
	if action == "" {
		action = "apply"
	}
	isApply := action == "apply" && !isError
	isFailedApply := action == "apply" && isError

	bgColor := "warning"
	if isError {
		bgColor = "error"
	} else if isApply {
		bgColor = "success"
	}
	icon := unicodeSymbols.statusError
	if isApply {
		icon = unicodeSymbols.toolResolve
	}
	verb := "Discard"
	if isApply {
		verb = "Accept"
	} else if isFailedApply {
		verb = "Failed"
	}

	sourceLabel, summaryLabel := "", label
	if idx := strings.Index(label, ": "); idx > 0 {
		sourceLabel = strings.TrimSpace(label[:idx])
		summaryLabel = strings.TrimSpace(label[idx+2:])
	}
	sourceBadge := ""
	if sourceLabel != "" {
		sourceBadge = " " + p.bold(p.wrapBrackets(sourceLabel))
	}
	headerLine := fmt.Sprintf("%s %s %s%s", icon, p.bold(verb+":"), summaryLabel, sourceBadge)
	bannerLines := []string{"", headerLine, "", p.italic(reason), ""}

	lineW := width
	innerW := max(0, lineW-2)
	out := make([]string, len(bannerLines))
	for i, l := range bannerLines {
		if innerW == 0 {
			out[i] = clampRow(p.inverse(p.fg(bgColor, strings.Repeat(" ", lineW))), lineW)
			continue
		}
		body := " " + padRight(truncateOmit(l, innerW), innerW) + " "
		out[i] = clampRow(p.inverse(p.fg(bgColor, padRight(body, lineW))), lineW)
	}
	return out
}

func termTextArgBuilder(title, field string) Builder {
	return Builder{
		Call: func(ctx sdk.Context, args map[string]any, _ sdk.ToolRenderContext, width int) (kit.Node, error) {
			p := newPalette(ctx)
			desc := ""
			if s := strField(args, field); s != "" {
				first := sanitizeDisplayLines(s)[0]
				desc = truncateOmit(first, 72)
			}
			return termWrappedTextNode(p.renderStatusLine(statusLineOpts{icon: "pending", title: title, description: desc}), width), nil
		},
		Result: func(ctx sdk.Context, res sdk.ToolRenderResult, _ sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, width int) (kit.Node, error) {
			p := newPalette(ctx)
			desc := ""
			if s := strField(rc.Args, field); s != "" {
				first := sanitizeDisplayLines(s)[0]
				desc = truncateOmit(first, 72)
			}
			icon := "success"
			tok := "toolOutput"
			if rc.IsError {
				icon = "error"
				tok = "error"
			}
			header := p.renderStatusLine(statusLineOpts{icon: icon, title: title, description: desc})
			output := strings.TrimSpace(resultText(res))
			if output == "" {
				return termWrappedTextNode(header, width), nil
			}
			var body []string
			for _, l := range sanitizeDisplayLines(output) {
				body = append(body, p.fg(tok, l))
			}
			return termLinesNode(fitHeaderedRows(header, body, width), width), nil
		},
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// memory tools (retain / recall / reflect) & think
// upstream: packages/tui/src/tools/memory.ts, think.ts
// ═══════════════════════════════════════════════════════════════════════════

func retainContents(args map[string]any) []string {
	var out []string
	for _, raw := range asSlice(args["items"]) {
		if c := strings.TrimSpace(replaceTabs(strField(asMap(raw), "content"))); c != "" {
			out = append(out, c)
		}
	}
	return out
}

func (p palette) renderRetainLines(header string, contents []string, expanded bool, width int) []string {
	lines := []string{header}
	limit := previewCollapsedItems
	if expanded {
		limit = min(len(contents), maxExpandedOutputLines)
	}
	shown := min(len(contents), limit)
	bullet := unicodeSymbols.formatBullet
	cw := max(8, width-2-tui.VisibleWidth(bullet)-1)
	for _, c := range contents[:shown] {
		lines = append(lines, "  "+p.fg("muted", bullet)+" "+p.fg("toolOutput", truncateWidth(c, cw)))
	}
	if rem := len(contents) - shown; rem > 0 {
		lines = append(lines, "  "+p.fg("dim", fmt.Sprintf("… %d more", rem))+" "+p.formatExpandHint(expanded, true))
	}
	return clampRows(lines, width)
}

func termRetainCall(ctx sdk.Context, args map[string]any, _ sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	header := p.renderStatusLine(statusLineOpts{icon: "pending", title: "Retain"})
	return termLinesNode(p.renderRetainLines(header, retainContents(args), false, width), width), nil
}

func termRetainResult(ctx sdk.Context, res sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	if rc.IsError {
		msg := strings.TrimSpace(resultText(res))
		if msg == "" {
			msg = "Retain failed"
		}
		return termWrappedTextNode(p.formatErrorMessage(msg), width), nil
	}
	summary := strings.TrimSuffix(strings.TrimSpace(resultText(res)), ".")
	var meta []string
	if summary != "" {
		meta = []string{summary}
	}
	header := p.renderStatusLine(statusLineOpts{
		iconOverride: p.styledSym(unicodeSymbols.toolMemory, "accent"),
		title:        "Retain",
		meta:         meta,
	})
	return termLinesNode(p.renderRetainLines(header, retainContents(rc.Args), opts.Expanded, width), width), nil
}

func termMemoryQueryBuilder(title string) Builder {
	return Builder{
		Call: func(ctx sdk.Context, args map[string]any, _ sdk.ToolRenderContext, width int) (kit.Node, error) {
			p := newPalette(ctx)
			q := truncateWidth(flattenLine(strField(args, "query")), truncateContent)
			return termWrappedTextNode(p.renderStatusLine(statusLineOpts{icon: "pending", title: title, description: q}), width), nil
		},
		Result: func(ctx sdk.Context, res sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, width int) (kit.Node, error) {
			p := newPalette(ctx)
			q := truncateWidth(flattenLine(strField(rc.Args, "query")), truncateContent)
			if rc.IsError {
				header := p.renderStatusLine(statusLineOpts{icon: "error", title: title, description: q})
				return termLinesNode(fitHeaderedRows(header, []string{p.formatErrorDetail(resultText(res))}, width), width), nil
			}
			header := p.renderStatusLine(statusLineOpts{
				iconOverride: p.styledSym(unicodeSymbols.toolMemory, "accent"),
				title:        title,
				description:  q,
			})
			txt := strings.TrimSpace(resultText(res))
			if txt == "" {
				return termWrappedTextNode(header, width), nil
			}
			shown, _ := p.formatOutputPaneLines(outputPaneOpts{
				lines:             sanitizeDisplayLines(txt),
				expanded:          opts.Expanded,
				collapsedMaxLines: previewCollapsedLines,
				styleLine:         func(l string) string { return "  " + p.fg("toolOutput", l) },
			})
			return termLinesNode(fitHeaderedRows(header, shown, width), width), nil
		},
	}
}

func termThinkCall(ctx sdk.Context, args map[string]any, _ sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	thoughts := strField(args, "thoughts")
	if strings.TrimSpace(thoughts) == "" {
		return nil, nil
	}
	var rows []string
	bodyW := max(1, width-2)
	for _, l := range sanitizeDisplayLines(thoughts) {
		for _, wl := range wrapAnsi(p.italic(p.fg("thinkingText", l)), bodyW) {
			rows = append(rows, clampRow(" "+wl, width))
		}
	}
	return termLinesNode(rows, width), nil
}

func termThinkResult(_ sdk.Context, _ sdk.ToolRenderResult, _ sdk.ToolRenderResultOptions, _ sdk.ToolRenderContext, _ int) (kit.Node, error) {
	return nil, nil
}

// ═══════════════════════════════════════════════════════════════════════════
// goal tool
// upstream: packages/tui/src/tools/goal.ts
// ═══════════════════════════════════════════════════════════════════════════

func describeGoalOp(op string) string {
	switch op {
	case "create":
		return "set"
	case "get":
		return "check"
	case "complete", "pause", "resume", "drop":
		return op
	case "":
		return "?"
	default:
		return op
	}
}

func goalBadgeColor(status string) string {
	switch status {
	case "complete":
		return "success"
	case "budget-limited":
		return "warning"
	case "paused", "dropped":
		return "muted"
	default:
		return "accent"
	}
}

func termGoalCall(ctx sdk.Context, args map[string]any, _ sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	op := strField(args, "op")
	desc := describeGoalOp(op)
	var meta []string
	if op == "create" {
		if obj := strings.TrimSpace(strField(args, "objective")); obj != "" {
			meta = append(meta, p.italic(p.fg("muted", `"`+truncateWidth(flattenLine(obj), truncateTitle)+`"`)))
		}
		if b, ok := intField(args, "token_budget"); ok {
			meta = append(meta, "budget "+formatNumber(b))
		}
	}
	header := p.renderStatusLine(statusLineOpts{icon: "pending", title: "Goal", description: desc, meta: meta})
	return termWrappedTextNode(header, width), nil
}

func termGoalResult(ctx sdk.Context, res sdk.ToolRenderResult, _ sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	details := asMap(res.Details)
	op := strField(details, "op")
	if op == "" {
		op = strField(rc.Args, "op")
	}
	desc := describeGoalOp(op)

	if rc.IsError {
		header := p.renderStatusLine(statusLineOpts{icon: "error", title: "Goal", description: desc})
		fb := resultText(res)
		if fb == "" {
			fb = "Goal tool failed"
		}
		rows := p.renderOutputBlock(blockOpts{
			header:      header,
			state:       "error",
			borderColor: "error",
			sections:    []blockSection{{lines: strings.Split(p.formatErrorDetail(fb), "\n")}},
			width:       width,
		})
		return termLinesNode(rows, width), nil
	}

	goal := asMap(details["goal"])
	if goal == nil {
		header := p.renderStatusLine(statusLineOpts{icon: "warning", title: "Goal", description: desc, meta: []string{"no active goal"}})
		return termWrappedTextNode(header, width), nil
	}

	status := strField(goal, "status")
	header := p.renderStatusLine(statusLineOpts{
		iconOverride: p.styledSym(unicodeSymbols.toolGoal, "accent"),
		title:        "Goal",
		description:  desc,
		badgeLabel:   status,
		badgeColor:   goalBadgeColor(status),
	})

	var lines []string
	if obj := strings.TrimSpace(strField(goal, "objective")); obj != "" {
		lines = append(lines, p.italic(p.fg("muted", `"`+truncateWidth(flattenLine(obj), truncateLong)+`"`)))
	}
	tokensUsed, _ := intField(goal, "tokensUsed")
	tokLine := formatNumber(tokensUsed) + " tokens"
	if budget, ok := intField(goal, "tokenBudget"); ok {
		left := max(0, budget-tokensUsed)
		tokLine = fmt.Sprintf("%s / %s tokens (%s left)", formatNumber(tokensUsed), formatNumber(budget), formatNumber(left))
	}
	metaParts := []string{tokLine}
	if elapsedSec, ok := floatField(goal, "timeUsedSeconds"); ok && elapsedSec > 0 {
		metaParts = append(metaParts, formatDuration(int64(elapsedSec*1000))+" elapsed")
	}
	lines = append(lines, p.fg("dim", strings.Join(metaParts, unicodeSymbols.sepDot)))

	sections := []blockSection{{lines: lines}}
	if report := strField(details, "completionBudgetReport"); report != "" {
		var repLines []string
		for _, l := range sanitizeDisplayLines(report) {
			repLines = append(repLines, p.fg("muted", l))
		}
		sections = append(sections, blockSection{label: "Report", lines: repLines})
	}
	rows := p.renderOutputBlock(blockOpts{header: header, state: "success", borderColor: "borderMuted", sections: sections, width: width})
	return termLinesNode(rows, width), nil
}

// ═══════════════════════════════════════════════════════════════════════════
// yield tool ("Submit Result")
// upstream: packages/tui/src/tools/yield.ts
// ═══════════════════════════════════════════════════════════════════════════

func yieldSizeMeta(data any) string {
	switch x := data.(type) {
	case []any:
		return formatCount("item", len(x))
	case map[string]any:
		return formatCount("field", len(x))
	default:
		return ""
	}
}

func (p palette) renderYieldCard(args map[string]any, res *sdk.ToolRenderResult, isErr, expanded bool, width int) kit.Node {
	var details map[string]any
	if res != nil && !isErr {
		details = asMap(res.Details)
	}
	var types []string
	if rawT := asSlice(details["type"]); len(rawT) > 0 {
		types = stringSliceField(details, "type")
	} else if rawT := asSlice(args["type"]); len(rawT) > 0 {
		types = stringSliceField(args, "type")
	}
	incremental := len(types) > 0

	var target string
	if k, ok := intField(args, "key"); ok {
		target = fmt.Sprintf("item %d", k)
	} else if incremental {
		target = strings.Join(types, ", ")
	}

	var data any
	hasData := false
	if details != nil {
		if v, ok := details["data"]; ok {
			data = v
			hasData = true
		}
	}
	if !hasData && args != nil {
		if v, ok := args["data"]; ok {
			data = v
			hasData = true
		}
	}

	errText := strField(args, "error")
	if res != nil {
		if isErr {
			errText = strings.TrimSpace(resultText(*res))
			if errText == "" {
				errText = "Submit Result failed"
			}
		} else if strField(details, "status") == "aborted" {
			errText = strField(details, "error")
			if errText == "" {
				errText = strings.TrimSpace(resultText(*res))
			}
			if errText == "" {
				errText = "Aborted"
			}
		}
	}

	overridden, _ := boolField(details, "schemaOverridden")
	settled := res != nil
	failed := settled && errText != ""

	icon := "pending"
	if settled {
		if failed {
			icon = "error"
		} else {
			icon = "success"
		}
	}
	var meta []string
	if hasData {
		if sm := yieldSizeMeta(data); sm != "" {
			meta = []string{sm}
		}
	}
	hOpts := statusLineOpts{icon: icon, title: "Submit Result", description: target, meta: meta}
	if overridden {
		hOpts.badgeLabel = "schema overridden"
		hOpts.badgeColor = "warning"
	}
	header := p.renderStatusLine(hOpts)

	var body []string
	if errText != "" {
		for _, l := range sanitizeDisplayLines(errText) {
			body = append(body, wrapAnsi(p.fg("error", l), max(1, width))...)
		}
	}
	if hasData {
		if s, ok := data.(string); ok {
			shown, _ := p.formatOutputPaneLines(outputPaneOpts{
				lines:             sanitizeDisplayLines(strings.TrimRight(s, "\r\n")),
				expanded:          expanded,
				collapsedMaxLines: 4,
				styleLine:         func(l string) string { return clipCollapsed(p.fg("toolOutput", l), width, expanded) },
			})
			body = append(body, shown...)
		} else {
			treeLines, trunc := p.renderJSONTree(data, expanded, nil)
			body = append(body, treeLines...)
			if trunc {
				if expanded {
					body = append(body, p.fg("dim", "…"))
				} else {
					body = append(body, p.formatExpandHint(false, true))
				}
			}
		}
	} else if settled && errText == "" {
		note := "Ends with the sections submitted earlier, or the last message"
		if incremental {
			note = "Takes the last message as this section"
		}
		body = append(body, p.fg("dim", note))
	}

	if expanded {
		out := wrapAnsi(header, width)
		for _, r := range body {
			out = append(out, wrapAnsi(r, width)...)
		}
		return termLinesNode(out, width)
	}
	return termLinesNode(fitHeaderedRows(header, body, width), width)
}

func termYieldCall(ctx sdk.Context, args map[string]any, _ sdk.ToolRenderContext, width int) (kit.Node, error) {
	return newPalette(ctx).renderYieldCard(args, nil, false, false, width), nil
}

func termYieldResult(ctx sdk.Context, res sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, width int) (kit.Node, error) {
	return newPalette(ctx).renderYieldCard(rc.Args, &res, rc.IsError, opts.Expanded, width), nil
}

// ═══════════════════════════════════════════════════════════════════════════
// default renderer (checkpoint, rewind, browser, and fallback)
// upstream: packages/tui/src/tools/default-renderer.ts
// ═══════════════════════════════════════════════════════════════════════════

func termDefaultBuilder(label string) Builder {
	return Builder{
		Call: func(ctx sdk.Context, args map[string]any, _ sdk.ToolRenderContext, width int) (kit.Node, error) {
			return newPalette(ctx).renderDefaultCard(label, args, nil, false, false, false, width), nil
		},
		Result: func(ctx sdk.Context, res sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, width int) (kit.Node, error) {
			return newPalette(ctx).renderDefaultCard(label, rc.Args, &res, rc.IsError, opts.Expanded, opts.IsPartial, width), nil
		},
	}
}

// isBenignSkip mirrors packages/tui/src/chat/tool-execution.ts:2437-2444.
func isBenignSkip(details map[string]any, isPartial bool) bool {
	if isPartial || details == nil {
		return false
	}
	if b, _ := boolField(details, "skipped"); b {
		return true
	}
	if strField(details, "source") != "interrupt_skipped" {
		return false
	}
	if syn, _ := boolField(details, "__synthetic"); syn {
		return true
	}
	inter, _ := boolField(details, "__interrupted")
	return inter && strField(details, "execution") == "started"
}

func (p palette) renderDefaultCard(label string, args map[string]any, res *sdk.ToolRenderResult, isErr, expanded, isPartial bool, width int) kit.Node {
	skipped := false
	if res != nil {
		skipped = isBenignSkip(asMap(res.Details), isPartial)
	}
	icon := "pending"
	titleColor := ""
	if res != nil {
		switch {
		case isPartial:
			icon = "pending"
		case skipped:
			icon = "info"
			titleColor = "muted"
		case isErr:
			icon = "error"
		default:
			icon = "success"
		}
	}
	header := p.renderStatusLine(statusLineOpts{icon: icon, title: label, titleColor: titleColor})
	var body []string
	if !expanded && len(args) > 0 {
		inlineBudget := max(20, width-tui.VisibleWidth(unicodeSymbols.treeLast)-2)
		if preview := formatArgsInline(args, inlineBudget); preview != "" {
			body = append(body, " "+p.fg("dim", unicodeSymbols.treeLast)+" "+p.fg("dim", preview))
		}
	}
	if expanded && args != nil {
		body = append(body, "", p.fg("dim", "Args"))
		treeLines, trunc := p.renderJSONTree(args, true, map[string]bool{"i": true, "__partialJson": true})
		body = append(body, treeLines...)
		if trunc {
			body = append(body, p.fg("dim", "…"))
		}
		body = append(body, "")
	}
	if res != nil {
		outText := strings.TrimRight(resultText(*res), " \t\r\n")
		if outText == "" {
			body = append(body, p.fg("dim", "(no output)"))
		} else {
			renderedJSON := false
			if parsed, ok := tryParseTreeSafeJSON(outText); ok {
				treeLines, trunc := p.renderJSONTree(parsed, expanded, nil)
				if len(treeLines) > 0 && !(expanded && trunc) {
					body = append(body, treeLines...)
					if !expanded {
						body = append(body, p.formatExpandHint(false, true))
					}
					renderedJSON = true
				}
			}
			if !renderedJSON {
				tok := "toolOutput"
				if isErr {
					tok = "error"
				}
				shown, _ := p.formatOutputPaneLines(outputPaneOpts{
					lines:                 sanitizeDisplayLines(outText),
					expanded:              expanded,
					collapsedMaxLines:     4,
					styleLine:             func(l string) string { return clipCollapsed(p.fg(tok, l), width, expanded) },
					showExpandHintWhenFit: true,
				})
				body = append(body, shown...)
			}
		}
	}
	// packages/tui/src/render/tool-card.ts:255-268: expanded plain cards wrap long lines.
	if expanded {
		out := wrapAnsi(header, width)
		for _, r := range body {
			out = append(out, wrapAnsi(r, width)...)
		}
		return termLinesNode(out, width)
	}
	return termLinesNode(fitHeaderedRows(header, body, width), width)
}
