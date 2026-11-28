package ansi

import (
	"fmt"
	"regexp"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

func init() {
	registerTerminal("read", Builder{Call: termReadCall, Result: termReadResult})
	registerTerminal("write", Builder{Call: termWriteCall, Result: termWriteResult})
	registerTerminal("edit", Builder{Call: termEditCall, Result: termEditResult})
	registerTerminal("apply_patch", Builder{Call: termEditCall, Result: termEditResult})
	registerTerminal("fetch", Builder{Call: termFetchCall, Result: termFetchResult})
}

// ═══════════════════════════════════════════════════════════════════════════
// read tool (compact filesystem rows + internal URL cards + URL fetch cards)
// upstream: packages/tui/src/chat/read-tool-group.ts, packages/tui/src/tools/read.ts,
//           packages/tui/src/tools/fetch.ts, proc-render.ts, cfg-render.ts
// ═══════════════════════════════════════════════════════════════════════════

func readRawPath(args map[string]any) string {
	return strField(args, "path", "file_path")
}

// readArgsCollapseIntoGroup mirrors packages/tui/src/chat/read-tool-group.ts:74-80:
// filesystem paths and unregistered schemes collapse to omp's compact one-line
// read row (` ✔ Read <path>`); registered internal URL schemes render full
// unless their spec declares CompactTranscript (or they are proc:// / cfg://).
func readArgsCollapseIntoGroup(rawPath string) bool {
	if rawPath == "" {
		return false
	}
	if isReadableURLPath(rawPath) {
		return false
	}
	scheme, _, ok := splitURLScheme(rawPath)
	if !ok {
		return true
	}
	switch scheme {
	case "proc", "cfg", "agent":
		return false
	}
	if h, found := urls.Lookup(scheme); found {
		return h.Spec().CompactTranscript
	}
	return true
}

func formatReadTargetWithRange(rawPath string, args map[string]any) string {
	display := shortenPath(sanitizeDisplayText(rawPath))
	if display == "" {
		display = "…"
	}
	off, hasOff := intField(args, "offset")
	lim, hasLim := intField(args, "limit")
	if hasOff || hasLim {
		start := off
		if !hasOff {
			start = 1
		}
		if hasLim && lim > 0 {
			display += fmt.Sprintf(":%d-%d", start, start+lim-1)
		} else {
			display += fmt.Sprintf(":%d", start)
		}
	}
	return display
}

func termReadCall(ctx sdk.Context, args map[string]any, _ sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	rawPath := readRawPath(args)
	if scheme, rest, ok := splitURLScheme(rawPath); ok {
		switch scheme {
		case "proc":
			return termProcRead(p, rest, nil, nil, false, false, width), nil
		case "cfg":
			return termCfgRead(p, rawPath, nil, nil, false, false, width), nil
		}
	}
	if isReadableURLPath(rawPath) {
		return termFetchCall(ctx, args, sdk.ToolRenderContext{}, width)
	}
	if readArgsCollapseIntoGroup(rawPath) {
		// Compact pending read row (read-tool-group.ts:1096-1098).
		target := formatReadTargetWithRange(rawPath, args)
		line := fmt.Sprintf(" %s %s %s",
			p.statusIcon("pending"),
			p.fg("toolTitle", p.bold("Read")),
			p.fg("accent", target),
		)
		return termWrappedTextNode(line, width), nil
	}
	target := formatReadTargetWithRange(rawPath, args)
	header := p.renderStatusLine(statusLineOpts{
		icon:        "pending",
		title:       "Read",
		description: target,
	})
	return termWrappedTextNode(header, width), nil
}

func termReadResult(ctx sdk.Context, res sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	rawPath := readRawPath(rc.Args)
	details := asMap(res.Details)

	if scheme, rest, ok := splitURLScheme(rawPath); ok {
		switch scheme {
		case "proc":
			return termProcRead(p, rest, &res, details, rc.IsError, opts.Expanded, width), nil
		case "cfg":
			return termCfgRead(p, rawPath, &res, details, rc.IsError, opts.Expanded, width), nil
		}
	}
	if strField(details, "kind") == "url" || isReadableURLPath(rawPath) {
		return termFetchResult(ctx, res, opts, rc, width)
	}

	// Filesystem / compact-group reads (read-tool-group.ts:1093-1109, 1303-1316):
	// When `read.toolResultPreview` is false (default), omp keeps filesystem
	// reads as a single compact status row (` ✔ Read notes.txt` / ` ✘ Read missing.txt`)
	// even when global Ctrl+O (`opts.Expanded`) is toggled on; `ReadToolGroupComponent`
	// only renders a code cell preview when `readToolResultPreview` is enabled.
	compact := readArgsCollapseIntoGroup(rawPath) && !hasImageBlock(res) && (rc.IsError || !settingBool(ctx, "read.toolResultPreview", false))
	if compact {
		status := "success"
		var correctedFrom string
		displayRaw := rawPath
		if rc.IsError {
			status = "error"
		} else if sr := asMap(details["suffixResolution"]); sr != nil {
			status = "warning"
			if to := strField(sr, "to"); to != "" {
				displayRaw = to
			}
			if from := strField(sr, "from"); from != "" {
				correctedFrom = from
			}
		}
		target := formatReadTargetWithRange(displayRaw, rc.Args)
		line := fmt.Sprintf(" %s %s %s",
			p.statusIcon(status),
			p.fg("toolTitle", p.bold("Read")),
			p.fg("accent", target),
		)
		if correctedFrom != "" {
			line += p.fg("dim", fmt.Sprintf(" (corrected from %s)", shortenPath(sanitizeDisplayText(correctedFrom))))
		}
		if conflicts, ok := intField(details, "conflictCount"); ok && conflicts > 0 {
			line += " " + p.fg("warning", fmt.Sprintf("(%s %s)", unicodeSymbols.statusWarning, formatCount("conflict", conflicts)))
		}
		return termWrappedTextNode(line, width), nil
	}

	if rc.IsError {
		errText := sanitizeDisplayText(resultText(res))
		if errText == "" {
			errText = "Unknown error"
		}
		errText = trimErrorPrefix(errText)
		target := formatReadTargetWithRange(rawPath, rc.Args)
		title := "Read"
		if target != "" && target != "…" {
			title = "Read " + target
		}
		header := p.renderStatusLine(statusLineOpts{icon: "error", title: title})
		var errLines []string
		for _, l := range sanitizeDisplayLines(errText) {
			errLines = append(errLines, p.fg("error", l))
		}
		rows := p.renderOutputBlock(blockOpts{
			header:      header,
			state:       "error",
			borderColor: "error",
			sections:    []blockSection{{lines: errLines}},
			width:       width,
		})
		return termLinesNode(rows, width), nil
	}

	rawText := resultText(res)
	disp := asMap(details["displayContent"])
	contentText := strField(disp, "text")
	if contentText == "" {
		contentText = stripOutputNotice(rawText, asMap(details["meta"]))
	}

	var warningLines []string
	// upstream: packages/tui/src/tools/read.ts:671-675: print `[Resolved path: ...]`
	// whenever details.resolvedPath is present.
	if rp := strField(details, "resolvedPath"); rp != "" {
		warningLines = append(warningLines, p.fg("dim", p.wrapBrackets("Resolved path: "+shortenPath(rp))))
	}
	meta := asMap(details["meta"])
	if meta != nil {
		if w := p.formatMetaTruncation(meta); w != "" {
			warningLines = append(warningLines, w)
		}
	} else if topTrunc := asMap(details["truncation"]); topTrunc != nil {
		if w := p.formatMetaTruncation(map[string]any{"truncation": topTrunc}); w != "" {
			warningLines = append(warningLines, w)
		}
	}

	if hasImageBlock(res) {
		target := formatReadTargetWithRange(rawPath, rc.Args)
		header := p.renderStatusLine(statusLineOpts{icon: "success", title: "Read", description: target})
		var body []string
		if strings.TrimSpace(contentText) != "" {
			for _, l := range sanitizeDisplayLines(contentText) {
				body = append(body, p.fg("toolOutput", l))
			}
		}
		body = append(body, warningLines...)
		if len(body) == 0 {
			body = []string{p.fg("dim", "(image)")}
		}
		rows := p.renderOutputBlock(blockOpts{
			header:      header,
			state:       "success",
			borderColor: "dim",
			sections:    []blockSection{{label: p.fg("toolTitle", "Details"), lines: body}},
			width:       width,
		})
		return termLinesNode(rows, width), nil
	}

	target := formatReadTargetWithRange(rawPath, rc.Args)
	title := "Read"
	if target != "" && target != "…" {
		title = "Read " + target
	}
	if sum := asMap(details["summary"]); sum != nil {
		if n, ok := intField(sum, "elidedSpans"); ok && n > 0 {
			title += fmt.Sprintf(" (summary: %s)", formatCount("elided span", n))
		}
	}
	if conflicts, ok := intField(details, "conflictCount"); ok && conflicts > 0 {
		title += " " + p.fg("warning", fmt.Sprintf("(%s %s)", unicodeSymbols.statusWarning, formatCount("conflict", conflicts)))
	}

	isRaw, _ := boolField(rc.Args, "raw")
	if !isRaw {
		isRaw, _ = boolField(details, "raw")
	}
	ct := strings.ToLower(strField(details, "contentType"))
	isMD := !isRaw && (strings.Contains(ct, "markdown") || getLanguageFromPath(rawPath) == "markdown")
	if isMD {
		rows := p.renderMarkdownCellLines(title, contentText, strings.Join(warningLines, "\n"), "complete", opts.Expanded, width)
		return termLinesNode(rows, width), nil
	}

	startLine := 1
	if sl, ok := intField(disp, "startLine"); ok && sl > 0 {
		startLine = sl
	} else if off, ok := intField(rc.Args, "offset"); ok && off > 0 {
		startLine = off
	}
	var lineNums []int
	if rawNums := asSlice(disp["lineNumbers"]); len(rawNums) > 0 {
		lineNums = make([]int, len(rawNums))
		for i, v := range rawNums {
			switch n := v.(type) {
			case float64:
				lineNums[i] = int(n)
			case int:
				lineNums[i] = n
			}
		}
	}

	rows := p.renderCodeCellLines(codeCellOpts{
		code:            contentText,
		language:        getLanguageFromPath(rawPath),
		title:           title,
		status:          "complete",
		output:          strings.Join(warningLines, "\n"),
		expanded:        opts.Expanded,
		codeMaxLines:    readPreviewLines,
		codeStartLine:   startLine,
		codeLineNumbers: lineNums,
		width:           width,
	})
	return termLinesNode(rows, width), nil
}

// ═══════════════════════════════════════════════════════════════════════════
// URL read / fetch tool
// upstream: packages/tui/src/tools/fetch.ts
// ═══════════════════════════════════════════════════════════════════════════

func formatFetchURLDescription(rawURL string) string {
	rawURL = sanitizeDisplayText(rawURL)
	if rawURL == "" {
		return ""
	}
	domain := getDomain(rawURL)
	rest := rawURL
	if idx := strings.Index(rest, "://"); idx >= 0 {
		rest = rest[idx+3:]
	}
	pathPart := ""
	if idx := strings.IndexAny(rest, "/?#"); idx >= 0 {
		pathPart = rest[idx:]
	}
	if pathPart == "" || pathPart == "/" {
		return domain
	}
	return domain + " " + truncateWidth(pathPart, 48)
}

func termFetchCall(ctx sdk.Context, args map[string]any, _ sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	rawURL := strField(args, "path", "url", "file_path")
	var meta []string
	if raw, ok := boolField(args, "raw"); ok && raw {
		meta = append(meta, "raw")
	}
	header := p.renderStatusLine(statusLineOpts{
		icon:        "pending",
		title:       "Read",
		description: formatFetchURLDescription(rawURL),
		meta:        meta,
	})
	return termWrappedTextNode(header, width), nil
}

func termFetchResult(ctx sdk.Context, res sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	details := asMap(res.Details)
	rawURL := strField(details, "finalUrl", "url")
	if rawURL == "" {
		rawURL = strField(rc.Args, "path", "url", "file_path")
	}

	if rc.IsError || details == nil {
		errText := trimErrorPrefix(sanitizeDisplayText(resultText(res)))
		if errText == "" {
			errText = "No response data"
		}
		header := p.renderStatusLine(statusLineOpts{
			icon:        "error",
			title:       "Read",
			description: formatFetchURLDescription(rawURL),
		})
		var errLines []string
		for _, l := range sanitizeDisplayLines(errText) {
			errLines = append(errLines, p.fg("error", l))
		}
		rows := p.renderOutputBlock(blockOpts{
			header:      header,
			state:       "error",
			borderColor: "error",
			sections:    []blockSection{{lines: errLines}},
			width:       width,
		})
		return termLinesNode(rows, width), nil
	}

	origURL := strField(details, "url")
	finalURL := strField(details, "finalUrl")
	if finalURL == "" {
		finalURL = origURL
	}
	contentType := strField(details, "contentType")
	if contentType == "" {
		contentType = "unknown"
	}
	method := strField(details, "method")
	notes := stringSliceField(details, "notes")
	meta := asMap(details["meta"])
	trunc := asMap(meta["truncation"])
	truncFlag, _ := boolField(details, "truncated")
	truncated := truncFlag || trunc != nil

	icon := "success"
	border := "dim"
	if truncated {
		icon = "warning"
		border = "warning"
	}
	header := p.renderStatusLine(statusLineOpts{
		icon:        icon,
		title:       "Read",
		description: formatFetchURLDescription(finalURL),
	})

	contentText := resultText(res)
	body := contentText
	if idx := strings.Index(contentText, "---\n\n"); idx >= 0 {
		body = contentText[idx+len("---\n\n"):]
	}
	var nonEmpty []string
	for _, l := range sanitizeDisplayLines(body) {
		if strings.TrimSpace(l) != "" {
			nonEmpty = append(nonEmpty, l)
		}
	}

	metaLines := []string{
		p.fg("muted", "Content-Type:") + " " + contentType,
	}
	if method != "" {
		metaLines = append(metaLines, p.fg("muted", "Method:")+" "+method)
	}
	if origURL != "" && finalURL != "" && origURL != finalURL {
		metaLines = append(metaLines, p.fg("muted", "Final URL:")+" "+finalURL)
	}
	metaLines = append(metaLines,
		p.fg("muted", "Lines:")+" "+formatCount("line", len(nonEmpty)),
		p.fg("muted", "Chars:")+" "+fmt.Sprintf("%d", len(strings.TrimSpace(body))),
	)
	if truncated {
		metaLines = append(metaLines, p.statusIcon("warning")+" "+p.fg("warning", "Output truncated"))
		if aid := strField(trunc, "artifactId"); aid != "" {
			metaLines = append(metaLines, p.fg("warning", formatFullOutputReference(aid, 0)))
		}
	}
	if len(notes) > 0 {
		metaLines = append(metaLines, p.fg("muted", "Notes:")+" "+strings.Join(notes, "; "))
	}

	previewLimit := previewCollapsedLines
	if opts.Expanded {
		previewLimit = previewExpandedLines
	}
	shownCount := min(len(nonEmpty), previewLimit)
	var contentPreview []string
	if shownCount == 0 {
		contentPreview = []string{p.fg("dim", "(no content)")}
	} else {
		for _, l := range nonEmpty[:shownCount] {
			contentPreview = append(contentPreview, p.fg("dim", strings.TrimRight(l, " \t")))
		}
		if rem := len(nonEmpty) - shownCount; rem > 0 {
			hint := p.formatExpandHint(opts.Expanded, true)
			more := fmt.Sprintf("… %d more lines", rem)
			if hint != "" {
				more += " " + hint
			}
			contentPreview = append(contentPreview, p.fg("muted", more))
		}
	}

	fetchState := "success"
	if truncated {
		fetchState = "warning"
	}
	rows := p.renderOutputBlock(blockOpts{
		header:      header,
		state:       fetchState,
		noBg:        true, // packages/tui/src/tools/fetch.ts:321 (applyBg: false)
		borderColor: border,
		sections: []blockSection{
			{label: p.fg("toolTitle", "Content"), lines: contentPreview},
			{label: p.fg("toolTitle", "Metadata"), lines: metaLines},
		},
		width: width,
	})
	return termLinesNode(rows, width), nil
}

// ═══════════════════════════════════════════════════════════════════════════
// write tool (including proc://, cfg://, agent:// URL writes)
// upstream: packages/tui/src/tools/write.ts
// ═══════════════════════════════════════════════════════════════════════════

func termWriteCall(ctx sdk.Context, args map[string]any, _ sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	rawPath := strField(args, "file_path", "path")
	content := strField(args, "content")

	if scheme, rest, ok := splitURLScheme(rawPath); ok {
		switch scheme {
		case "out":
			return termOutReplyWrite(p, content, nil, false, width), nil
		case "proc":
			id, action := parseProcWriteTarget(rest)
			return termProcWrite(p, id, action, content, nil, nil, false, false, width), nil
		case "cfg":
			return termCfgWrite(p, rawPath, content, nil, nil, false, false, width), nil
		case "agent":
			return termAgentWrite(p, rest, content, nil, nil, false, false, width), nil
		}
	}
	filePath := shortenPath(sanitizeDisplayText(rawPath))
	icon := p.fg("muted", langIcon(rawPath))
	styledPath := p.fg("accent", filePath)
	if filePath == "" {
		styledPath = p.fg("toolOutput", "…")
	}
	// Per write.ts:611-620: leave icon slot empty on call card unless a live spinner runs.
	header := p.renderStatusLine(statusLineOpts{
		title:       "Write",
		description: icon + " " + styledPath,
	})
	var sections []blockSection
	if content != "" {
		lines := sanitizeDisplayLines(content)
		// Tail window of 12 lines while streaming (write.ts:622-642).
		capped := p.capPreviewLines(lines, readPreviewLines, false, "", true)
		styled := make([]string, len(capped))
		copy(styled, capped)
		sections = []blockSection{{lines: styled}}
	}
	rows := p.renderOutputBlock(blockOpts{
		header:      header,
		state:       "pending",
		borderColor: "borderMuted",
		sections:    sections,
		width:       width,
	})
	return termLinesNode(rows, width), nil
}

func termWriteResult(ctx sdk.Context, res sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	rawPath := strField(rc.Args, "file_path", "path")
	content := strField(rc.Args, "content")
	details := asMap(res.Details)

	if scheme, rest, ok := splitURLScheme(rawPath); ok {
		switch scheme {
		case "out":
			return termOutReplyWrite(p, content, &res, rc.IsError, width), nil
		case "proc":
			id, action := parseProcWriteTarget(rest)
			return termProcWrite(p, id, action, content, &res, details, rc.IsError, opts.Expanded, width), nil
		case "cfg":
			return termCfgWrite(p, rawPath, content, &res, details, rc.IsError, opts.Expanded, width), nil
		case "agent":
			return termAgentWrite(p, rest, content, &res, details, rc.IsError, opts.Expanded, width), nil
		}
	}
	filePath := shortenPath(sanitizeDisplayText(rawPath))
	icon := p.fg("muted", langIcon(rawPath))
	styledPath := p.fg("accent", filePath)
	if filePath == "" {
		styledPath = p.fg("toolOutput", "…")
	}

	if rc.IsError {
		header := p.renderStatusLine(statusLineOpts{
			icon:        "error",
			title:       "Write",
			description: icon + " " + styledPath,
		})
		rows := p.renderOutputBlock(blockOpts{
			header:      header,
			state:       "error",
			borderColor: "error",
			sections:    []blockSection{{lines: strings.Split(p.formatErrorDetail(resultText(res)), "\n")}},
			width:       width,
		})
		return termLinesNode(rows, width), nil
	}

	lines := []string(nil)
	if content != "" {
		lines = sanitizeDisplayLines(content)
	}
	lineSuffix := ""
	if len(lines) > 0 {
		// packages/tui/src/tools/write.ts:179-182: ` · N lines`
		lineSuffix = p.fg("dim", fmt.Sprintf(" %s %s", strings.TrimSpace(unicodeSymbols.sepDot), formatCount("line", len(lines))))
	}
	execSuffix := ""
	if madeExec, _ := boolField(details, "madeExecutable"); !opts.IsPartial && madeExec {
		execSuffix = p.fg("dim", unicodeSymbols.sepDot) + p.fg("success", "made executable!")
	}

	var headerOpts statusLineOpts
	if opts.IsPartial {
		headerOpts = statusLineOpts{
			icon:        "running",
			title:       "Write",
			description: icon + " " + styledPath + lineSuffix + execSuffix,
		}
	} else {
		headerOpts = statusLineOpts{
			iconOverride: p.styledSym(unicodeSymbols.toolWrite, "accent"),
			title:        "Write",
			description:  icon + " " + styledPath + lineSuffix + execSuffix,
		}
	}
	header := p.renderStatusLine(headerOpts)

	var body []string
	if opts.IsPartial {
		if prog := strings.TrimSpace(resultText(res)); prog != "" {
			body = append(body, p.fg("muted", truncateWidth(replaceTabs(sanitizeDisplayText(prog)), truncateLine)))
		}
	}
	if len(lines) > 0 {
		// packages/tui/src/tools/write.ts:76,202,384-406:
		// WRITE_PREVIEW_LINES = 6, WRITE_GUTTER_MIN_WIDTH = 3 (`  1 text`).
		const writePreviewLines = 6
		const writeGutterMinWidth = 3
		limit := writePreviewLines
		if opts.Expanded {
			limit = min(len(lines), maxExpandedOutputLines)
		} else if limit > len(lines) {
			limit = len(lines)
		}
		gutterW := max(writeGutterMinWidth, len(fmt.Sprintf("%d", len(lines))))
		for i, l := range lines[:limit] {
			gutter := p.fg("dim", fmt.Sprintf("%*d ", gutterW, i+1))
			// packages/tui/src/tools/write.ts:389-398: plain (non-highlighted) code
			// body lines stay in default fg over toolSuccessBg, not muted/toolOutput.
			body = append(body, gutter+l)
		}
		if hidden := len(lines) - limit; hidden > 0 {
			hint := p.formatExpandHint(opts.Expanded, true)
			more := formatMoreItems(hidden, "line")
			if hint != "" {
				more += " " + hint
			}
			body = append(body, p.fg("dim", more))
		}
	}
	if !opts.IsPartial {
		if diag := asMap(details["diagnostics"]); diag != nil {
			body = append(body, p.formatDiagnosticsLines(diag, opts.Expanded)...)
		}
	}

	var sections []blockSection
	if len(body) > 0 {
		sections = []blockSection{{lines: body}}
	}
	writeState := "success"
	if opts.IsPartial {
		writeState = "partial"
	}
	rows := p.renderOutputBlock(blockOpts{
		header:      header,
		state:       writeState,
		borderColor: "borderMuted",
		sections:    sections,
		width:       width,
	})
	return termLinesNode(rows, width), nil
}

// ═══════════════════════════════════════════════════════════════════════════
// edit / apply_patch tool
// upstream: packages/tui/src/tools/edit.ts
// ═══════════════════════════════════════════════════════════════════════════

var hashlineHeaderPathRe = regexp.MustCompile(`(?m)^\[([^\]#\n]+?)(?:#[0-9A-Fa-f]{4})?\]\s*$`)
var hashlineRemRe = regexp.MustCompile(`(?m)^REM\s*$`)
var hashlineMvRe = regexp.MustCompile(`(?m)^MV\s+(.+?)\s*$`)

type editCallSummary struct {
	explicitPath string // args.file_path / args.path / edits[0].path
	hashlinePath string // first [path#TAG] header from args.input
	op           string
	rename       string
	fileCount    int
}

func (s editCallSummary) callPath() string {
	if s.explicitPath != "" {
		return s.explicitPath
	}
	return s.hashlinePath
}

// resolveEditCallSummary extracts path/op/rename metadata from edit tool args.
// Keeps explicit args paths separate from the hashline `[path#TAG]` header path
// so termEditResult can honour omp's precedence
// `sourcePath ?? args.file_path ?? args.path ?? edits[0].path ?? details.path ?? firstHashlineInputEntry.path`
// (upstream: packages/tui/src/tools/edit.ts:1552-1561).
func resolveEditCallSummary(args map[string]any) editCallSummary {
	var out editCallSummary
	out.explicitPath = strField(args, "file_path", "path")
	out.op = strField(args, "op")
	out.rename = strField(args, "rename", "move")

	if edits := asSlice(args["edits"]); len(edits) > 0 {
		seen := map[string]struct{}{}
		for _, item := range edits {
			m := asMap(item)
			ep := strField(m, "path")
			if ep != "" {
				seen[ep] = struct{}{}
				if out.explicitPath == "" {
					out.explicitPath = ep
				}
			}
			if out.op == "" {
				out.op = strField(m, "op")
			}
			if out.rename == "" {
				out.rename = strField(m, "rename", "move")
			}
		}
		out.fileCount = len(seen)
	}
	if input := strField(args, "input"); input != "" {
		matches := hashlineHeaderPathRe.FindAllStringSubmatch(input, -1)
		if len(matches) > 0 {
			seen := map[string]struct{}{}
			for _, m := range matches {
				hp := strings.TrimSpace(m[1])
				if hp != "" {
					seen[hp] = struct{}{}
					if out.hashlinePath == "" {
						out.hashlinePath = hp
					}
				}
			}
			if out.fileCount == 0 {
				out.fileCount = len(seen)
			}
		}
		if out.op == "" && hashlineRemRe.MatchString(input) {
			out.op = "delete"
		}
		if out.rename == "" {
			if m := hashlineMvRe.FindStringSubmatch(input); m != nil {
				out.rename = strings.Trim(strings.TrimSpace(m[1]), `"`)
			}
		}
	}
	return out
}

func editOpTitle(op string) string {
	switch strings.ToLower(op) {
	case "create":
		return "Create"
	case "delete":
		return "Delete"
	default:
		return "Edit"
	}
}

// editHeaderLabelBudget mirrors packages/tui/src/tools/edit.ts:414-417.
func editHeaderLabelBudget(width int) int {
	leftGlyphs := unicodeSymbols.boxTopLeft + strings.Repeat(unicodeSymbols.boxHoriz, 3)
	return max(0, width-visibleWidth(leftGlyphs)-visibleWidth(unicodeSymbols.boxTopRight)-2)
}

// formatEditHeader mirrors packages/tui/src/tools/edit.ts:353-469:
// never appends `:N` to the display path (upstream uses firstChangedLine only
// in the OSC 8 link target) and middle-truncates long paths so stats stay visible.
func (p palette) formatEditHeader(width int, op, titleOverride, rawPath, rename, statsSuffix, extraSuffix, icon, iconOverride string) string {
	title := titleOverride
	if title == "" {
		title = editOpTitle(op)
	}
	iconGlyph := p.fg("muted", langIcon(rawPath))
	rawDisplay := replaceTabs(shortenPath(sanitizeDisplayText(rawPath)))
	renameDisplay := ""
	if rename != "" {
		renameDisplay = replaceTabs(shortenPath(sanitizeDisplayText(rename)))
	}
	suffix := statsSuffix + extraSuffix

	build := func(maxPathW int) (header string, pathW int) {
		primary := rawDisplay
		if primary != "" && maxPathW > 0 {
			primary = truncateMiddleToWidth(primary, maxPathW)
		}
		var styledPath string
		if primary != "" {
			styledPath = p.fg("accent", primary)
			pathW = visibleWidth(primary)
		} else {
			styledPath = p.fg("toolOutput", "…")
			pathW = 1
		}
		desc := iconGlyph + " " + styledPath
		if renameDisplay != "" {
			ren := renameDisplay
			if maxPathW > 0 {
				ren = truncateMiddleToWidth(ren, maxPathW)
			}
			desc += " " + p.fg("dim", "→") + " " + p.fg("accent", ren)
			pathW += visibleWidth(ren)
		}
		header = p.renderStatusLine(statusLineOpts{
			icon:         icon,
			iconOverride: iconOverride,
			title:        title,
			description:  desc,
		}) + suffix
		return header, pathW
	}

	header, pathW := build(0)
	if width <= 0 {
		return header
	}
	overflow := visibleWidth(header) - editHeaderLabelBudget(width)
	if overflow <= 0 || pathW <= 1 {
		return header
	}
	pathCount := 0
	if rawDisplay != "" {
		pathCount++
	}
	if renameDisplay != "" {
		pathCount++
	}
	if pathCount < 1 {
		pathCount = 1
	}
	fittedW := max(1, (pathW-overflow)/pathCount)
	fitted, _ := build(fittedW)
	return fitted
}

// renderInlineEditRow mirrors packages/tui/src/tools/edit.ts:477-506.
func (p palette) renderInlineEditRow(op, rawPath, rename string, pending bool, width int) kit.Node {
	isDelete := op == "delete"
	title := "Move"
	if isDelete {
		title = "Delete"
	}
	icon := ""
	iconOverride := ""
	if pending {
		icon = "pending"
	} else if isDelete {
		iconOverride = p.styledSym(unicodeSymbols.toolDelete, "accent")
	} else {
		iconOverride = p.styledSym(unicodeSymbols.toolMove, "accent")
	}
	line := p.formatEditHeader(width, op, title, rawPath, rename, "", "", icon, iconOverride)
	return termWrappedTextNode(line, width)
}

func termEditCall(ctx sdk.Context, args map[string]any, _ sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	sum := resolveEditCallSummary(args)
	rawPath, op, rename, fileCount := sum.callPath(), sum.op, sum.rename, sum.fileCount
	hasPayload := strField(args, "old_text", "new_text", "diff") != "" || len(asSlice(args["edits"])) > 0
	if input := strField(args, "input"); input != "" {
		if strings.Contains(input, "PUT ") || strings.Contains(input, "CUT ") {
			hasPayload = true
		}
	}
	// Delete / payload-less move calls render as an inline row (edit.ts:1205-1207).
	if fileCount <= 1 && (op == "delete" || (rename != "" && !hasPayload)) {
		return p.renderInlineEditRow(op, rawPath, rename, true, width), nil
	}
	extra := ""
	if fileCount > 1 {
		extra = p.fg("dim", fmt.Sprintf(" (+%d more)", fileCount-1))
	}
	// Leave icon slot empty on call card when no live spinner runs (edit.ts:1214-1221).
	header := p.formatEditHeader(width, op, "", rawPath, rename, "", extra, "", "")
	var sections []blockSection
	if input := strField(args, "input"); input != "" {
		lines := sanitizeDisplayLines(input)
		capped := p.capPreviewLines(lines, previewWindowFallback, false, "", true)
		styled := make([]string, len(capped))
		for i, l := range capped {
			styled[i] = p.fg("dim", l)
		}
		sections = []blockSection{{lines: styled}}
	}
	rows := p.renderOutputBlock(blockOpts{
		header:      header,
		state:       "pending",
		borderColor: "borderMuted",
		sections:    sections,
		width:       width,
	})
	return termLinesNode(rows, width), nil
}

func termEditResult(ctx sdk.Context, res sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	details := asMap(res.Details)
	sum := resolveEditCallSummary(rc.Args)
	op, rename := sum.op, sum.rename

	// Multi-file edit results.
	if perFile := asSlice(details["perFileResults"]); len(perFile) > 1 {
		return p.renderMultiFileEdit(perFile, rc.IsError, opts.Expanded, width), nil
	}

	// Header path precedence (upstream: packages/tui/src/tools/edit.ts:1552-1561):
	// moveSource (details.sourcePath) ?? args.file_path ?? args.path ?? edits[0].path ?? details.path ?? firstHashlineInputEntry.path
	rawPath := strField(details, "sourcePath")
	if rawPath == "" {
		rawPath = sum.explicitPath
	}
	if rawPath == "" {
		rawPath = strField(details, "path")
	}
	if rawPath == "" {
		rawPath = sum.hashlinePath
	}
	if op == "" {
		op = strField(details, "op")
	}
	if rename == "" {
		rename = strField(details, "move", "rename")
	}
	diffText := strField(details, "diff")
	if diffText == "" {
		diffText = strField(details, "patch")
	}
	diag := asMap(details["diagnostics"])

	// Delete and move-only results without a diff render inline (edit.ts:1580-1592).
	if !rc.IsError && diffText == "" && diag == nil && (op == "delete" || rename != "") {
		return p.renderInlineEditRow(op, rawPath, rename, false, width), nil
	}

	if rc.IsError {
		header := p.formatEditHeader(width, op, "", rawPath, rename, "", "", "error", "")
		errText := strField(details, "displayErrorText", "errorText")
		if errText == "" {
			errText = resultText(res)
		}
		var errLines []string
		for _, l := range sanitizeDisplayLines(errText) {
			errLines = append(errLines, p.fg("error", l))
		}
		rows := p.renderOutputBlock(blockOpts{
			header:      header,
			state:       "error",
			borderColor: "error",
			sections:    []blockSection{{lines: errLines}},
			width:       width,
		})
		return termLinesNode(rows, width), nil
	}

	statsSuffix := ""
	if diffText != "" {
		statsSuffix = p.formatDiffStatsSuffix(diffText)
	}
	header := p.formatEditHeader(width, op, "", rawPath, rename, statsSuffix, "", "", p.styledSym(unicodeSymbols.toolEdit, "accent"))
	zero := 0
	innerW := outputBlockContentWidth(width, &zero, &zero)
	var body []string
	if diffText != "" {
		body = p.renderDiffLines(diffText, opts.Expanded, innerW)
	} else if op != "delete" && op != "create" && rename == "" {
		shown := shortenPath(sanitizeDisplayText(rawPath))
		msg := "No changes were made."
		if shown != "" {
			msg = fmt.Sprintf("No changes were made to %s.", shown)
		}
		body = []string{" " + p.fg("dim", msg)}
	}
	if diag != nil {
		body = append(body, p.formatDiagnosticsLines(diag, opts.Expanded)...)
	}

	var sections []blockSection
	if len(body) > 0 {
		sections = []blockSection{{lines: body}}
	}
	editState := "success"
	if opts.IsPartial {
		editState = "partial"
	}
	rows := p.renderOutputBlock(blockOpts{
		header:              header,
		state:               editState,
		borderColor:         "borderMuted",
		contentPaddingLeft:  &zero,
		contentPaddingRight: &zero,
		sections:            sections,
		width:               width,
	})
	return termLinesNode(rows, width), nil
}

func (p palette) renderMultiFileEdit(perFile []any, isErr, expanded bool, width int) kit.Node {
	var totalAdded, totalRemoved int
	anyErr := isErr
	for _, raw := range perFile {
		m := asMap(raw)
		if b, _ := boolField(m, "isError"); b {
			anyErr = true
		}
		if d := strField(m, "diff", "patch"); d != "" {
			st := getDiffStats(d)
			totalAdded += st.added
			totalRemoved += st.removed
		}
	}
	statsSuffix := ""
	if totalAdded > 0 || totalRemoved > 0 {
		var parts []string
		if totalAdded > 0 {
			parts = append(parts, p.fg("toolDiffAdded", fmt.Sprintf("+%d", totalAdded)))
		}
		if totalRemoved > 0 {
			parts = append(parts, p.fg("toolDiffRemoved", fmt.Sprintf("-%d", totalRemoved)))
		}
		statsSuffix = " " + p.fg("dim", unicodeSymbols.formatBracketLeft) +
			strings.Join(parts, p.fg("dim", "/")) +
			p.fg("dim", unicodeSymbols.formatBracketRight)
	}
	var headerOpts statusLineOpts
	if anyErr {
		headerOpts = statusLineOpts{icon: "error", title: "Edit", description: fmt.Sprintf("%s%s", formatCount("file", len(perFile)), statsSuffix)}
	} else {
		headerOpts = statusLineOpts{iconOverride: p.styledSym(unicodeSymbols.toolEdit, "accent"), title: "Edit", description: fmt.Sprintf("%s%s", formatCount("file", len(perFile)), statsSuffix)}
	}
	header := p.renderStatusLine(headerOpts)

	zero := 0
	innerW := outputBlockContentWidth(width, &zero, &zero)
	var sections []blockSection
	maxFiles := len(perFile)
	if !expanded && maxFiles > previewCollapsedItems {
		maxFiles = previewCollapsedItems
	}
	for i := range maxFiles {
		m := asMap(perFile[i])
		fp := strField(m, "path")
		diffText := strField(m, "diff", "patch")
		label := p.fg("muted", langIcon(fp)) + " " + p.fg("accent", shortenPath(sanitizeDisplayText(fp)))
		if diffText != "" {
			label += p.formatDiffStatsSuffix(diffText)
		}
		var lines []string
		if fe, _ := boolField(m, "isError"); fe {
			lines = []string{" " + p.fg("error", strField(m, "displayErrorText", "errorText"))}
		} else if diffText != "" {
			lines = p.renderDiffLines(diffText, expanded, innerW)
		}
		if diag := asMap(m["diagnostics"]); diag != nil {
			lines = append(lines, p.formatDiagnosticsLines(diag, expanded)...)
		}
		sections = append(sections, blockSection{label: label, lines: lines})
	}
	if hidden := len(perFile) - maxFiles; hidden > 0 {
		sections = append(sections, blockSection{
			separator: true,
			lines:     []string{" " + p.fg("dim", formatMoreItems(hidden, "file")+" "+p.formatExpandHint(expanded, true))},
		})
	}
	border := "borderMuted"
	mfState := "success"
	if anyErr {
		border = "error"
		mfState = "error"
	}
	rows := p.renderOutputBlock(blockOpts{
		header:              header,
		state:               mfState,
		borderColor:         border,
		contentPaddingLeft:  &zero,
		contentPaddingRight: &zero,
		sections:            sections,
		width:               width,
	})
	return termLinesNode(rows, width)
}

// ═══════════════════════════════════════════════════════════════════════════
// proc://, cfg://, agent:// URL cards (shared by read and write)
// upstream: packages/tui/src/tools/proc-render.ts, cfg-render.ts
// ═══════════════════════════════════════════════════════════════════════════

func parseProcWriteTarget(rest string) (id, action string) {
	rest = strings.TrimPrefix(rest, "/")
	if rest == "" {
		return "", "stdin"
	}
	parts := strings.Split(rest, "/")
	id = parts[0]
	action = "stdin"
	if len(parts) > 1 {
		switch parts[1] {
		case "kill", "stop":
			action = "kill"
		case "mode":
			action = "mode"
		case "stdin":
			action = "stdin"
		}
	}
	return id, action
}

func (p palette) formatQuotedPreview(body string, expanded bool, tone string, width int, wrapAll bool) []string {
	if strings.TrimSpace(body) == "" || width <= 0 {
		return nil
	}
	quote := p.fg("dim", unicodeSymbols.mdQuoteBorder)
	prefix := "  " + quote + " "
	var nonEmpty []string
	for _, l := range sanitizeDisplayLines(body) {
		if strings.TrimSpace(l) != "" {
			nonEmpty = append(nonEmpty, strings.TrimSpace(l))
		}
	}
	if len(nonEmpty) == 0 {
		return nil
	}
	limit := previewCollapsedLines
	if expanded {
		if wrapAll {
			limit = min(len(nonEmpty), maxExpandedOutputLines)
		} else {
			limit = previewExpandedLines
		}
	}
	shownCount := min(len(nonEmpty), limit)
	hidden := len(nonEmpty) - shownCount
	bodyW := max(1, width-visibleWidth(prefix))
	capW := min(truncateLine, bodyW)
	cut := false
	var lines []string
	for _, l := range nonEmpty[:shownCount] {
		if expanded && wrapAll {
			for _, wl := range wrapAnsi(l, bodyW) {
				lines = append(lines, clampRow(prefix+p.fg(tone, wl), width))
			}
			continue
		}
		if visibleWidth(l) > capW {
			cut = true
		}
		lines = append(lines, clampRow(prefix+p.fg(tone, truncateWidth(l, capW)), width))
	}
	if hidden > 0 || cut {
		hint := p.formatExpandHint(expanded, true)
		marker := "…"
		if hidden > 0 {
			marker = fmt.Sprintf("… +%d more lines", hidden)
		}
		if hint != "" {
			marker += " " + hint
		}
		lines = append(lines, clampRow(prefix+p.fg("dim", marker), width))
	}
	return lines
}

func (p palette) formatProcJobRow(job map[string]any) string {
	status := strField(job, "status")
	iconName := "success"
	badgeColor := "accent"
	switch status {
	case "cancelled":
		iconName = "aborted"
		badgeColor = "warning"
	case "failed":
		iconName = "error"
		badgeColor = "error"
	case "running":
		iconName = "running"
	}
	jtype := strField(job, "type")
	if jtype == "" {
		jtype = "job"
	}
	jid := strField(job, "id")
	label := strField(job, "label", "description")
	durMs, _ := int64Field(job, "durationMs")
	return fmt.Sprintf("%s %s %s %s %s",
		p.statusIcon(iconName),
		p.formatBadge(jtype, badgeColor),
		p.fg("toolOutput", jid),
		p.fg("dim", flattenLine(label)),
		p.fg("dim", formatDuration(durMs)),
	)
}

func procServiceName(d map[string]any) string {
	return strField(d, "name", "id", "label")
}

func procServicePID(d map[string]any) (int, bool) {
	if pid, ok := intField(d, "pid"); ok && pid > 0 {
		return pid, true
	}
	if pids := asSlice(d["pids"]); len(pids) > 0 {
		switch v := pids[0].(type) {
		case float64:
			if int(v) > 0 {
				return int(v), true
			}
		case int:
			if v > 0 {
				return v, true
			}
		}
	}
	return 0, false
}

func (p palette) procDaemonMeta(d map[string]any) []string {
	state := strField(d, "state")
	if state == "" {
		state = "running"
	}
	color := "warning"
	switch state {
	case "ready", "running":
		color = "success"
	case "failed":
		color = "error"
	}
	out := []string{p.fg(color, state)}
	if pid, ok := procServicePID(d); ok {
		out = append(out, fmt.Sprintf("pid %d", pid))
	}
	if policy := asMap(d["policy"]); policy != nil {
		if hold := strField(policy, "hold"); hold == "detached" {
			out = append(out, "detached")
		} else if hold == "persist" {
			out = append(out, "persistent")
		}
	}
	return out
}

func termProcRead(p palette, id string, res *sdk.ToolRenderResult, details map[string]any, isErr, expanded bool, width int) kit.Node {
	id = strings.TrimPrefix(id, "/")
	title := "Proc jobs & services"
	if id != "" {
		title = "Proc " + id
	}
	daemon := asMap(details["daemon"])
	icon := "pending"
	if res != nil {
		if isErr {
			icon = "error"
		} else {
			icon = "info"
		}
	}
	var meta []string
	if daemon != nil {
		meta = p.procDaemonMeta(daemon)
	}
	header := p.renderStatusLine(statusLineOpts{icon: icon, title: title, meta: meta})
	if res == nil {
		return termWrappedTextNode(header, width)
	}
	if isErr {
		return termLinesNode(fitHeaderedRows(header, []string{p.formatErrorDetail(resultText(*res))}, width), width)
	}
	if job := asMap(details["job"]); job != nil {
		body := strField(details, "log")
		if body == "" {
			body = strField(job, "errorText", "resultText")
		}
		rows := []string{p.formatProcJobRow(job)}
		rows = append(rows, p.formatQuotedPreview(body, expanded, "toolOutput", width, false)...)
		return termLinesNode(fitHeaderedRows(header, rows, width), width)
	}
	if daemon != nil {
		outLines := stringSliceField(details, "terminalRows")
		if len(outLines) == 0 {
			if logStr := strField(details, "log"); logStr != "" {
				for _, l := range sanitizeDisplayLines(logStr) {
					if l != "" {
						outLines = append(outLines, l)
					}
				}
			}
		}
		limit := previewCollapsedLines
		if expanded {
			limit = previewExpandedLines
		}
		var rows []string
		if len(outLines) > limit {
			rows = append(rows, p.fg("dim", fmt.Sprintf("  … %d earlier lines", len(outLines)-limit)))
			outLines = outLines[len(outLines)-limit:]
		}
		for _, l := range outLines {
			rows = append(rows, "  "+p.fg("toolOutput", truncateWidth(replaceTabs(sanitizeDisplayText(l)), truncateLine)))
		}
		return termLinesNode(fitHeaderedRows(header, rows, width), width)
	}

	jobs := asSlice(details["jobs"])
	daemons := asSlice(details["daemons"])
	agents := asSlice(details["agents"])
	if id != "" && len(jobs) == 0 && len(daemons) == 0 && len(agents) == 0 {
		rows := p.formatQuotedPreview(resultText(*res), expanded, "toolOutput", width, false)
		return termLinesNode(fitHeaderedRows(header, rows, width), width)
	}

	listMeta := []string{
		fmt.Sprintf("%d jobs", len(jobs)),
		fmt.Sprintf("%d services", len(daemons)),
	}
	if len(agents) > 0 {
		listMeta = append(listMeta, fmt.Sprintf("%d agents", len(agents)))
	}
	listHeader := p.renderStatusLine(statusLineOpts{icon: "info", title: title, meta: listMeta})
	var items []string
	for _, raw := range jobs {
		items = append(items, p.formatProcJobRow(asMap(raw)))
	}
	for _, raw := range daemons {
		d := asMap(raw)
		name := procServiceName(d)
		state := strField(d, "state")
		stColor := "warning"
		if state == "failed" {
			stColor = "error"
		} else if state == "ready" || state == "running" {
			stColor = "success"
		}
		facts := p.procDaemonMeta(d)
		extra := ""
		if len(facts) > 1 {
			extra = " " + strings.Join(facts[1:], unicodeSymbols.sepDot)
		}
		items = append(items, fmt.Sprintf("%s %s %s%s",
			p.formatBadge("service", "accent"),
			p.fg("toolOutput", name),
			p.formatBadge(state, stColor),
			extra,
		))
	}
	for _, raw := range agents {
		a := asMap(raw)
		live, _ := boolField(a, "live")
		col := "warning"
		if live {
			col = "accent"
		}
		ageMs, _ := int64Field(a, "ageMs")
		items = append(items, fmt.Sprintf("%s %s %s",
			p.formatBadge("agent", col),
			p.fg("toolOutput", strField(a, "id")),
			p.fg("dim", formatDuration(ageMs)),
		))
	}
	var body []string
	if len(items) == 0 {
		body = []string{p.fg("dim", "No background jobs or services.")}
	} else {
		body = p.renderTreeLines(items, expanded, previewCollapsedItems, "process")
	}
	return termLinesNode(fitHeaderedRows(listHeader, body, width), width)
}

func termProcWrite(p palette, id, action, content string, res *sdk.ToolRenderResult, details map[string]any, isErr, expanded bool, width int) kit.Node {
	title := fmt.Sprintf("Proc %s %s", action, id)
	if id == "" {
		title = fmt.Sprintf("Proc %s …", action)
	}
	daemon := asMap(details["daemon"])
	icon := "pending"
	if res != nil {
		switch {
		case isErr:
			icon = "error"
		case action == "kill":
			icon = "aborted"
		default:
			icon = "success"
		}
	}
	var meta []string
	if daemon != nil {
		meta = p.procDaemonMeta(daemon)
	} else if action == "mode" && content != "" {
		meta = []string{flattenLine(content)}
	}
	header := p.renderStatusLine(statusLineOpts{icon: icon, title: title, meta: meta})
	if res != nil && isErr {
		return termLinesNode(fitHeaderedRows(header, []string{p.formatErrorDetail(resultText(*res))}, width), width)
	}
	var body []string
	if content != "" && action == "stdin" {
		body = append(body, p.formatQuotedPreview(content, expanded, "dim", width, false)...)
	}
	if strField(details, "op") == "cancel" {
		cancelled := asSlice(details["cancelled"])
		var items []string
		for _, raw := range cancelled {
			c := asMap(raw)
			st := strField(c, "status")
			bc := "error"
			if st == "cancelled" {
				bc = "warning"
			}
			items = append(items, p.fg("toolOutput", strField(c, "id"))+" "+p.formatBadge(st, bc))
		}
		body = append(body, p.renderTreeLines(items, expanded, previewCollapsedItems, "job")...)
	}
	return termLinesNode(fitHeaderedRows(header, body, width), width)
}

func termCfgRead(p palette, rawURL string, res *sdk.ToolRenderResult, details map[string]any, isErr, expanded bool, width int) kit.Node {
	cfgPath := strField(details, "path")
	if cfgPath == "" {
		_, rest, _ := splitURLScheme(rawURL)
		cfgPath = strings.Trim(strings.ReplaceAll(rest, "/", "."), ".")
	}
	if cfgPath == "" {
		cfgPath = "all settings"
	}
	var meta []string
	if details != nil {
		if count, ok := intField(details, "count"); ok {
			meta = append(meta, formatCount("setting", count))
		}
		if mod, ok := intField(details, "modified"); ok && mod > 0 {
			meta = append(meta, fmt.Sprintf("%d modified", mod))
		}
	}
	icon := "pending"
	if res != nil {
		if isErr {
			icon = "error"
		} else {
			icon = "info"
		}
	}
	header := p.renderStatusLine(statusLineOpts{icon: icon, title: "Config", description: cfgPath, meta: meta})
	if res == nil {
		return termWrappedTextNode(header, width)
	}
	if isErr {
		return termLinesNode(fitHeaderedRows(header, []string{p.formatErrorDetail(resultText(*res))}, width), width)
	}
	allLines := sanitizeDisplayLines(strings.TrimRight(resultText(*res), "\r\n"))
	limit := len(allLines)
	if !expanded && limit > previewCollapsedItems {
		limit = previewCollapsedItems
	}
	var body []string
	for _, l := range allLines[:limit] {
		body = append(body, "  "+p.fg("toolOutput", l))
	}
	if hidden := len(allLines) - limit; hidden > 0 {
		body = append(body, p.fg("dim", "  "+formatMoreItems(hidden, "line")))
	}
	return termLinesNode(fitHeaderedRows(header, body, width), width)
}

func termCfgWrite(p palette, rawURL, content string, res *sdk.ToolRenderResult, details map[string]any, isErr, _ bool, width int) kit.Node {
	_, rest, _ := splitURLScheme(rawURL)
	save, hasSave := boolField(details, "save")
	if !hasSave {
		save = strings.HasSuffix(rest, "/save")
	}
	cfgPath := strField(details, "path")
	if cfgPath == "" {
		trimmed := strings.TrimSuffix(rest, "/save")
		cfgPath = strings.Trim(strings.ReplaceAll(trimmed, "/", "."), ".")
	}
	outcome := strField(details, "outcome")
	icon := "pending"
	if res != nil {
		switch {
		case isErr:
			icon = "error"
		case outcome == "declined":
			icon = "warning"
		default:
			icon = "success"
		}
	}
	scopeLabel, scopeColor := "session", "muted"
	if save {
		scopeLabel, scopeColor = "persist", "accent"
	}
	badges := []string{p.formatBadge(scopeLabel, scopeColor)}
	if outcome == "declined" {
		badges = append(badges, p.formatBadge("declined", "warning"))
	} else if outcome == "unchanged" {
		badges = append(badges, p.formatBadge("unchanged", "muted"))
	}
	header := p.renderStatusLine(statusLineOpts{icon: icon, title: "Config", description: cfgPath}) + " " + strings.Join(badges, " ")
	if res != nil && isErr {
		return termLinesNode(fitHeaderedRows(header, []string{p.formatErrorDetail(resultText(*res))}, width), width)
	}
	arrow := p.fg("dim", "→")
	nextVal := strField(details, "value")
	if nextVal == "" {
		nextVal = strings.TrimSpace(sanitizeDisplayText(content))
	}
	if nextVal == "" {
		nextVal = "…"
	}
	prevVal := strField(details, "previous")
	var body []string
	if prevVal != "" {
		body = append(body, fmt.Sprintf("  %s %s %s", p.fg("dim", prevVal), arrow, p.fg("toolOutput", nextVal)))
	} else {
		body = append(body, fmt.Sprintf("  %s %s", arrow, p.fg("toolOutput", nextVal)))
	}
	if eff := strField(details, "effective"); eff != "" {
		body = append(body, p.fg("warning", fmt.Sprintf("  still %s: a higher-precedence layer overrides the saved value", eff)))
	}
	return termLinesNode(fitHeaderedRows(header, body, width), width)
}

func termAgentWrite(p palette, to, bodyText string, res *sdk.ToolRenderResult, details map[string]any, isErrArg, expanded bool, width int) kit.Node {
	recipient := strField(details, "to")
	if recipient == "" {
		recipient = strings.TrimPrefix(to, "/")
	}
	if recipient == "" {
		recipient = "…"
	}
	title := fmt.Sprintf("IRC %s %s", unicodeSymbols.navSelected, recipient)
	receipts := asSlice(details["receipts"])
	delivered, failed := 0, 0
	for _, r := range receipts {
		if strField(asMap(r), "outcome") == "failed" {
			failed++
		} else {
			delivered++
		}
	}
	isErr := (res != nil && isErrArg) || (failed > 0 && delivered == 0)
	var meta []string
	if recipient == "all" {
		meta = append(meta, "broadcast")
	}
	if len(receipts) == 1 {
		out := strField(asMap(receipts[0]), "outcome")
		meta = append(meta, p.fg(receiptColor(out), out))
	} else if len(receipts) > 1 {
		if delivered > 0 {
			meta = append(meta, p.fg("success", fmt.Sprintf("%d delivered", delivered)))
		}
		if failed > 0 {
			meta = append(meta, p.fg("error", fmt.Sprintf("%d failed", failed)))
		}
	}
	var hOpts statusLineOpts
	switch {
	case res == nil:
		hOpts = statusLineOpts{icon: "pending", title: title, meta: meta}
	case isErr:
		hOpts = statusLineOpts{icon: "error", title: title, meta: meta}
	default:
		hOpts = statusLineOpts{iconOverride: p.styledSym(unicodeSymbols.toolIrc, "accent"), title: title, meta: meta}
	}
	header := p.renderStatusLine(hOpts)
	if res != nil && isErrArg && len(receipts) == 0 {
		return termLinesNode(fitHeaderedRows(header, []string{p.formatErrorDetail(resultText(*res))}, width), width)
	}
	var rows []string
	rows = append(rows, p.formatQuotedPreview(bodyText, expanded, "dim", width, true)...)
	if res != nil && len(receipts) == 0 {
		txt := strings.TrimSpace(sanitizeDisplayText(resultText(*res)))
		if txt == "" {
			txt = "No live peers to broadcast to."
		}
		rows = append(rows, p.fg("muted", txt))
	}
	if len(receipts) > 1 || failed > 0 {
		var items []string
		for _, r := range receipts {
			rm := asMap(r)
			out := strField(rm, "outcome")
			line := p.fg("toolOutput", strField(rm, "to")) + " " + p.formatBadge(out, receiptColor(out))
			if errStr := strField(rm, "error"); errStr != "" {
				line += " " + p.fg("error", unicodeSymbols.formatDash+" "+errStr)
			}
			items = append(items, line)
		}
		rows = append(rows, p.renderTreeLines(items, expanded, previewCollapsedItems, "recipient")...)
	}
	return termLinesNode(fitHeaderedRows(header, rows, width), width)
}

func receiptColor(outcome string) string {
	switch outcome {
	case "failed":
		return "error"
	case "revived":
		return "warning"
	default:
		return "success"
	}
}

// termOutReplyWrite renders a `write` targeting `out://` as mid-turn assistant
// prose (unframed styled Markdown lines) rather than a file-write tool card,
// matching omp's ReplyWriteComponent (packages/tui/src/chat/reply-write.ts:82-127).
func termOutReplyWrite(p palette, content string, res *sdk.ToolRenderResult, isErr bool, width int) kit.Node {
	if width <= 0 {
		width = 80
	}
	var rows []string
	if strings.TrimSpace(content) != "" {
		for _, raw := range sanitizeDisplayLines(content) {
			rows = append(rows, wrapAnsi(p.styleMarkdownLine(raw), width)...)
		}
	}
	if isErr && res != nil {
		errText := strings.TrimSpace(resultText(*res))
		if errText != "" {
			if len(rows) > 0 {
				rows = append(rows, "")
			}
			for _, line := range strings.Split(p.formatErrorDetail(errText), "\n") {
				rows = append(rows, wrapAnsi(line, width)...)
			}
		}
	}
	return termLinesNode(rows, width)
}
