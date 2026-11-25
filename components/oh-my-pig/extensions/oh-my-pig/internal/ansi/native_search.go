package ansi

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

// Native views for search tools (`grep`, `glob`, `find`, `ast_grep`, `ast_edit`).
//
// upstream:
//   packages/tui/src/tools/grouped-file-output.ts (walkGroupedOutput, describeGroupedOutput)
//   packages/tui/src/tools/grep.ts (describeCall, describeResult)
//   packages/tui/src/tools/glob.ts (describeCall, describeResult, describeGlobFiles)
//   packages/tui/src/tools/find.ts (describeCall, describeResult, describeHit)
//   packages/tui/src/tools/ast-grep.ts (describeCall, describeResult)
//   packages/tui/src/tools/ast-edit.ts (describeCall, describeResult, parseAstEditChanges)

func init() {
	registerNative("grep", Builder{Call: nativeGrepCall, Result: nativeGrepResult})
	registerNative("glob", Builder{Call: nativeGlobCall, Result: nativeGlobResult})
	registerNative("find", Builder{Call: nativeFindCall, Result: nativeFindResult})
	registerNative("ast_grep", Builder{Call: nativeAstGrepCall, Result: nativeAstGrepResult})
	registerNative("ast_edit", Builder{Call: nativeAstEditCall, Result: nativeAstEditResult})
}

const (
	nvGrepCollapsedFiles  = 2
	nvGlobListMax         = 6
	nvFindCollapsedHits   = 4
	nvFindRangesCollapsed = 1
	nvFindRangesExpanded  = 3
)

// ── Grouped file output parser (tools/grouped-file-output.ts) ────────────────

var (
	// nvGroupedHeaderRE matches `# dir/`, `## file.ts#TAG`, `## file.ts (3 matches)`.
	nvGroupedHeaderRE = regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*$`)
	// nvHashTagSuffixRE strips trailing `#XXXX` hashline tags from file headers.
	nvHashTagSuffixRE = regexp.MustCompile(`#[0-9A-Fa-f]{4}$`)
	// nvParenSuffixRE extracts trailing `(N matches)` / `(N replacements)` suffix.
	nvParenSuffixRE = regexp.MustCompile(`^(.*?)\s+(\([^()]+\))$`)
	// nvGroupedNumberedLineRE matches `  *12│text`, ` 12:text`, `*5|text`.
	nvGroupedNumberedLineRE = regexp.MustCompile(`^\s*([* ]?)\s*(\d+)(?:│|[:|])(.*)$`)
	// nvGroupedGapLineRE matches `...` or `   │...`.
	nvGroupedGapLineRE = regexp.MustCompile(`^\s*(?:│\s*)?(?:\.\.\.|…)\s*$`)
	// nvAstEditChangeLineRE matches `-12│old`, `+12│new`, ` 12│ctx`.
	nvAstEditChangeLineRE = regexp.MustCompile(`^([+\- ])\s*(\d+)(?:│|[:|])(.*)$`)
)

type nvGroupedFile struct {
	path   string
	suffix string
	lines  []string
	meta   []string // 1:1 with lines: "[*]<lineNum>" for numbered lines, "" otherwise
	marks  int
}

func nvParseGroupedFiles(rawLines []string, singleFileFallback string, maxFiles int) (loose []string, files []nvGroupedFile) {
	var dirStack []string
	var cur *nvGroupedFile
	headless := singleFileFallback

	flush := func() {
		if cur == nil {
			return
		}
		files = append(files, *cur)
		cur = nil
	}

	for _, line := range rawLines {
		if m := nvGroupedHeaderRE.FindStringSubmatch(line); m != nil {
			headless = ""
			level := len(m[1])
			body := strings.TrimSpace(m[2])
			if strings.HasSuffix(body, "/") {
				flush()
				for len(dirStack) >= level {
					dirStack = dirStack[:len(dirStack)-1]
				}
				dirStack = append(dirStack, body)
				continue
			}
			flush()
			if maxFiles > 0 && len(files) >= maxFiles {
				return loose, files
			}
			suffix := ""
			if sm := nvParenSuffixRE.FindStringSubmatch(body); sm != nil {
				body = strings.TrimSpace(sm[1])
				suffix = sm[2]
			}
			body = nvHashTagSuffixRE.ReplaceAllString(body, "")
			fullPath := body
			if len(dirStack) > 0 && !strings.Contains(body, "://") && !strings.HasPrefix(body, "/") {
				prefix := ""
				for _, d := range dirStack[:min(len(dirStack), level-1)] {
					prefix += d
				}
				fullPath = prefix + body
			}
			cur = &nvGroupedFile{path: fullPath, suffix: suffix}
			continue
		}

		if strings.TrimSpace(line) == "" {
			if cur != nil && len(cur.lines) > 0 {
				flush()
				if maxFiles > 0 && len(files) >= maxFiles {
					return loose, files
				}
			}
			continue
		}

		if cur == nil && headless != "" {
			cur = &nvGroupedFile{path: headless}
			headless = ""
		}

		if cur == nil {
			loose = append(loose, nvSanitize(line))
			continue
		}

		if nvGroupedGapLineRE.MatchString(line) {
			// Non-adjacent gap rows are omitted from the raw code payload (upstream:
			// tools/grouped-file-output.ts:679 skips `n === null` gap entries).
			continue
		}
		if nm := nvGroupedNumberedLineRE.FindStringSubmatch(line); nm != nil {
			tok := nm[2]
			if nm[1] == "*" {
				cur.marks++
				tok = "*" + tok
			}
			// Emit raw code line without terminal `*N│` gutter (upstream:
			// tools/grouped-file-output.ts:682 pushes `line.text` directly).
			cur.lines = append(cur.lines, nvSanitize(nm[3]))
			cur.meta = append(cur.meta, tok)
			continue
		}
		cur.lines = append(cur.lines, nvSanitize(line))
		cur.meta = append(cur.meta, "")
	}
	flush()
	return loose, files
}

func nvDescribeGroupedOutput(sc *nvScope, rawLines []string, singleFile string, maxFiles int) []kit.Node {
	loose, files := nvParseGroupedFiles(rawLines, singleFile, maxFiles)
	var out []kit.Node
	if len(loose) > 0 {
		out = append(out, sc.lines(sc.claim("omp.tool.search.lines"), loose))
	}
	for i, f := range files {
		if !sc.canAdd(4) {
			out = append(out, nvStatsNode(sc, []string{fmt.Sprintf("+%d more files", len(files)-i)}))
			break
		}
		// Emit clean file path on omp.tool.file so the frontend's ompFileRowSpans
		// splits dir/basename cleanly without a trailing `(N)` corrupting the path
		// (upstream: tools/grouped-file-output.ts:741).
		fileHead := nvPlainLine(f.path)
		secChildren := []kit.Node{
			sc.text(sc.indexed("omp.tool.file", i), fileHead),
		}
		if len(f.lines) > 0 {
			secChildren = append(secChildren, sc.lines(sc.indexed("omp.tool.search.lines", i), f.lines))
			if hasMeta := nvHasGroupedMeta(f.meta); hasMeta {
				secChildren = append(secChildren, sc.text(sc.indexed("omp.tool.search.meta", i), strings.Join(f.meta, ",")))
			}
		}
		out = append(out, sc.vstack(sc.indexed("omp.tool.search.file", i), 1, secChildren...))
	}
	return out
}

func nvHasGroupedMeta(meta []string) bool {
	for _, m := range meta {
		if m != "" {
			return true
		}
	}
	return false
}

// ── grep (tools/grep.ts) ─────────────────────────────────────────────────────

func nativeGrepCall(_ sdk.Context, _ map[string]any, _ sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	return sc.container(sc.claim("omp.tool.grep.call")), nil
}

func nativeGrepResult(_ sdk.Context, res sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	details := nvToMap(res.Details)
	if rc.IsError || nvStr(details, "error") != "" {
		msg := nvStr(details, "error")
		if msg == "" {
			msg = nvResultText(res)
		}
		if msg == "" {
			msg = "Unknown error"
		}
		return sc.container(sc.claim("omp.tool.grep"), nvErrorNode(sc, sc.claim("omp.tool.error"), msg)), nil
	}

	textContent := nvStr(details, "displayContent")
	if textContent == "" {
		textContent = nvResultText(res)
	}

	var missingNote, binaryNote string
	if missing := nvStrSlice(details, "missingPaths"); len(missing) > 0 {
		missingNote = "skipped missing: " + strings.Join(missing, ", ")
	}
	if bin := nvStrSlice(details, "binaryMatches"); len(bin) > 0 {
		binaryNote = "binary matches: " + nvFormatCount("file", len(bin))
	}

	scope := ""
	if sp := nvStr(details, "scopePath"); sp != "" {
		scope = "in " + sp
	}
	hasDetailed := nvHasKey(details, "matchCount") || nvHasKey(details, "fileCount")
	matchCount := nvInt(details, "matchCount")
	if (!hasDetailed && (strings.TrimSpace(textContent) == "" || strings.TrimSpace(textContent) == "No matches found")) ||
		(hasDetailed && matchCount == 0) {
		var children []kit.Node
		metaParts := nvCompactStrings([]string{scope, "0 matches"})
		for i, mp := range metaParts {
			children = append(children, sc.text(sc.indexed("omp.head.meta", i), mp))
		}
		children = append(children, sc.text(sc.claim("omp.head.tone"), "warning"))
		if foot := nvFootnoteNode(sc, []string{missingNote, binaryNote}, nil); foot != nil {
			children = append(children, foot)
		}
		return sc.container(sc.claim("omp.tool.grep"), children...), nil
	}

	lines := strings.Split(strings.ReplaceAll(textContent, "\r\n", "\n"), "\n")
	fileCount := nvInt(details, "fileCount")
	maxFiles := 0
	if !opts.Expanded {
		maxFiles = nvGrepCollapsedFiles
	}
	singleFile := ""
	if files := nvStrSlice(details, "files"); len(files) == 1 {
		singleFile = files[0]
	}

	// Head metadata matching omp packages/tui/src/tools/grep.ts:342-360.
	var headChildren []kit.Node
	hasGroupedHdr := false
	for _, l := range lines {
		if nvGroupedHeaderRE.MatchString(l) {
			hasGroupedHdr = true
			break
		}
	}
	singleFileScope := scope != "" && fileCount == 1 && !hasGroupedHdr
	counts := ""
	if hasDetailed {
		if singleFileScope {
			counts = nvFormatCount("match", matchCount)
		} else {
			counts = nvFormatCount("match", matchCount) + " · " + nvFormatCount("file", fileCount)
		}
	}
	for i, mp := range nvCompactStrings([]string{scope, counts}) {
		headChildren = append(headChildren, sc.text(sc.indexed("omp.head.meta", i), mp))
	}
	metaMap := nvMap(details, "meta")
	limitsMap := nvMap(metaMap, "limits")
	if nvBool(details, "truncated") || nvMap(metaMap, "truncation") != nil || nvBool(limitsMap, "columnTruncated") {
		headChildren = append(headChildren, sc.text(sc.indexed("omp.head.badge", 0), "truncated:warning"))
	}

	children := append(headChildren, nvDescribeGroupedOutput(sc, lines, singleFile, maxFiles)...)

	var footParts []string
	if !opts.Expanded && fileCount > nvGrepCollapsedFiles {
		footParts = append(footParts, nvFormatCount("more file", fileCount-nvGrepCollapsedFiles))
	}
	footParts = append(footParts, missingNote, binaryNote)
	if foot := nvFootnoteNode(sc, footParts, metaMap); foot != nil {
		children = append(children, foot)
	}
	return sc.container(sc.claim("omp.tool.grep"), children...), nil
}

// ── glob (tools/glob.ts) ─────────────────────────────────────────────────────

func nativeGlobCall(_ sdk.Context, _ map[string]any, _ sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	return sc.container(sc.claim("omp.tool.glob.call")), nil
}

func nvDescribeGlobFiles(sc *nvScope, files []string) kit.Node {
	// upstream: tools/glob.ts:106-129 — <=6 files: VStack of omp.tool.file#<i >;
	// >6 files: HStack of omp.tool.chip#<i >.
	if len(files) <= nvGlobListMax {
		children := make([]kit.Node, 0, len(files))
		for i, f := range files {
			if !sc.canAdd(1) {
				break
			}
			children = append(children, sc.text(sc.indexed("omp.tool.file", i), nvPlainLine(f)))
		}
		return sc.vstack(sc.claim("omp.tool.files"), 0, children...)
	}
	// Cap chips well below 4096 nodes.
	maxChips := min(len(files), 200)
	children := make([]kit.Node, 0, maxChips+1)
	for i := range maxChips {
		if !sc.canAdd(2) {
			break
		}
		children = append(children, sc.text(sc.indexed("omp.tool.chip", i), nvPlainLine(files[i])))
	}
	if len(files) > maxChips {
		children = append(children, sc.text(sc.claim("omp.tool.stats"), fmt.Sprintf("+%d more", len(files)-maxChips)))
	}
	return sc.hstack(sc.claim("omp.tool.files"), 1, children...)
}

func nativeGlobResult(_ sdk.Context, res sdk.ToolRenderResult, _ sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	details := nvToMap(res.Details)
	if rc.IsError || nvStr(details, "error") != "" {
		msg := nvStr(details, "error")
		if msg == "" {
			msg = nvResultText(res)
		}
		if msg == "" {
			msg = "Unknown error"
		}
		return sc.container(sc.claim("omp.tool.glob"), nvErrorNode(sc, sc.claim("omp.tool.error"), msg)), nil
	}

	if !nvHasKey(details, "fileCount") {
		textContent := strings.TrimSpace(nvResultText(res))
		if textContent == "" || strings.Contains(textContent, "No files matching") || strings.Contains(textContent, "No files found") {
			return sc.container(sc.claim("omp.tool.glob"),
				sc.text(sc.indexed("omp.head.meta", 0), "0 files"),
				sc.text(sc.claim("omp.head.tone"), "warning"),
			), nil
		}
		var lines []string
		for line := range strings.SplitSeq(textContent, "\n") {
			if t := strings.TrimSpace(line); t != "" {
				lines = append(lines, t)
			}
		}
		return sc.container(sc.claim("omp.tool.glob"),
			sc.text(sc.indexed("omp.head.meta", 0), nvFormatCount("file", len(lines))),
			nvDescribeGlobFiles(sc, lines),
		), nil
	}

	var missingNote string
	if missing := nvStrSlice(details, "missingPaths"); len(missing) > 0 {
		missingNote = "skipped missing: " + strings.Join(missing, ", ")
	}
	scope := ""
	if sp := nvStr(details, "scopePath"); sp != "" {
		scope = "in " + sp
	}
	fileCount := nvInt(details, "fileCount")
	if fileCount == 0 {
		var children []kit.Node
		for i, mp := range nvCompactStrings([]string{"0 files", scope}) {
			children = append(children, sc.text(sc.indexed("omp.head.meta", i), mp))
		}
		children = append(children, sc.text(sc.claim("omp.head.tone"), "warning"))
		if foot := nvFootnoteNode(sc, []string{missingNote}, nil); foot != nil {
			children = append(children, foot)
		}
		return sc.container(sc.claim("omp.tool.glob"), children...), nil
	}

	meta := nvMap(details, "meta")
	trunc := nvMap(details, "truncation")
	if trunc == nil {
		trunc = nvMap(meta, "truncation")
	}
	limits := nvMap(meta, "limits")
	truncated := nvBool(details, "truncated") || trunc != nil || nvInt(details, "resultLimitReached") > 0 || nvMap(limits, "resultLimit") != nil
	timedOut := nvBool(details, "timedOut")
	var reasons []string
	if rlr := nvInt(details, "resultLimitReached"); rlr > 0 {
		reasons = append(reasons, fmt.Sprintf("limit %d results", rlr))
	} else if rl := nvMap(limits, "resultLimit"); rl != nil {
		if reached := nvInt(rl, "reached"); reached > 0 {
			reasons = append(reasons, fmt.Sprintf("limit %d results", reached))
		}
	}
	if trunc != nil {
		if nvStr(trunc, "truncatedBy") == "lines" {
			reasons = append(reasons, "line limit")
		} else {
			reasons = append(reasons, "size limit")
		}
		if artID := nvStr(trunc, "artifactId"); artID != "" {
			reasons = append(reasons, nvFormatFullOutputRef(artID, nvInt64(trunc, "artifactElidedBytes")))
		}
	}

	var footParts []string
	if len(reasons) > 0 {
		footParts = append(footParts, "truncated: "+strings.Join(reasons, ", "))
	}
	if missingNote != "" {
		footParts = append(footParts, missingNote)
	}

	// Head metadata matching omp packages/tui/src/tools/glob.ts:365-376.
	var children []kit.Node
	for i, mp := range nvCompactStrings([]string{nvFormatCount("file", fileCount), scope}) {
		children = append(children, sc.text(sc.indexed("omp.head.meta", i), mp))
	}
	if truncated {
		badge := "truncated:warning"
		if timedOut {
			badge = "timed out:warning"
		}
		children = append(children, sc.text(sc.indexed("omp.head.badge", 0), badge))
	}
	children = append(children, nvDescribeGlobFiles(sc, nvStrSlice(details, "files")))
	if foot := nvFootnoteNode(sc, footParts, nil); foot != nil {
		children = append(children, foot)
	}
	return sc.container(sc.claim("omp.tool.glob"), children...), nil
}

// ── find (tools/find.ts) ─────────────────────────────────────────────────────

func nativeFindCall(_ sdk.Context, _ map[string]any, _ sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	return sc.container(sc.claim("omp.tool.find.call")), nil
}

func nativeFindResult(_ sdk.Context, res sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	details := nvToMap(res.Details)
	progress := strings.TrimSpace(nvResultText(res))
	if rc.IsError {
		msg := progress
		if msg == "" {
			msg = "Unknown error"
		}
		return sc.container(sc.claim("omp.tool.find"), nvErrorNode(sc, sc.claim("omp.tool.error"), msg)), nil
	}
	if opts.IsPartial || details == nil {
		var children []kit.Node
		if progress != "" {
			children = append(children, sc.text(sc.claim("omp.tool.stats"), nvPlainLine(progress)))
		}
		return sc.container(sc.claim("omp.tool.find"), children...), nil
	}

	stats := nvMap(details, "stats")
	hits := nvSlice(details, "hits")
	failures := nvStrSlice(stats, "failures")

	var children []kit.Node
	if len(hits) == 0 {
		for i, raw := range nvSlice(details, "lexicalFallback") {
			if !sc.canAdd(2) {
				break
			}
			lm := nvToMap(raw)
			if lm == nil {
				continue
			}
			lineText := fmt.Sprintf("%s:%d %s", nvPlainLine(nvStr(lm, "rel")), nvInt(lm, "line"), nvPlainLine(nvStr(lm, "snippet")))
			children = append(children, sc.text(sc.indexed("omp.tool.find.range", i), lineText))
		}
		for i, f := range failures {
			if !sc.canAdd(1) {
				break
			}
			children = append(children, sc.text(sc.indexed("omp.tool.notice", i), nvPlainText(f)))
		}
		return sc.container(sc.claim("omp.tool.find"), children...), nil
	}

	maxHits := len(hits)
	if !opts.Expanded && maxHits > nvFindCollapsedHits {
		maxHits = nvFindCollapsedHits
	}
	rangeLimit := nvFindRangesCollapsed
	if opts.Expanded {
		rangeLimit = nvFindRangesExpanded
	}

	for i := range maxHits {
		if !sc.canAdd(4) {
			break
		}
		hit := nvToMap(hits[i])
		if hit == nil {
			continue
		}
		rel := nvPlainLine(nvStr(hit, "rel"))
		score, _ := nvFloat(hit, "contentScore")
		linesSeen := nvInt(hit, "linesSeen")
		truncated := nvBool(hit, "truncated")
		detail := fmt.Sprintf("%.2f · %d lines judged", score, linesSeen)
		if truncated {
			detail += ", partial"
		}

		// File head row with score and judged-lines detail.
		hitChildren := []kit.Node{
			sc.text(sc.indexed("omp.tool.file", i), fmt.Sprintf("%s · %s", rel, detail)),
		}

		// Sort ranges by p descending, then start ascending (tools/find.ts:222).
		rawRanges := nvSlice(hit, "ranges")
		type findRange struct {
			start   int
			end     int
			p       float64
			snippet string
		}
		ranges := make([]findRange, 0, len(rawRanges))
		for _, rr := range rawRanges {
			rm := nvToMap(rr)
			if rm == nil {
				continue
			}
			pVal, _ := nvFloat(rm, "p")
			ranges = append(ranges, findRange{
				start:   nvInt(rm, "start"),
				end:     nvInt(rm, "end"),
				p:       pVal,
				snippet: strings.TrimRight(nvPlainText(nvStr(rm, "snippet")), " \t\n"),
			})
		}
		sort.SliceStable(ranges, func(a, b int) bool {
			if ranges[a].p != ranges[b].p {
				return ranges[a].p > ranges[b].p
			}
			return ranges[a].start < ranges[b].start
		})
		if len(ranges) > rangeLimit {
			ranges = ranges[:rangeLimit]
		}
		for rIdx, rg := range ranges {
			if !sc.canAdd(1) {
				break
			}
			label := fmt.Sprintf("L%d-%d (%.2f)\n%s", rg.start, rg.end, rg.p, rg.snippet)
			if rg.snippet == "" {
				label = fmt.Sprintf("L%d-%d (%.2f)", rg.start, rg.end, rg.p)
			}
			hitChildren = append(hitChildren, sc.text(sc.subIndexed("omp.tool.find.range", i, rIdx), label))
		}
		children = append(children, sc.vstack(sc.indexed("omp.tool.find.hit", i), 1, hitChildren...))
	}

	for i, f := range failures {
		if !sc.canAdd(1) {
			break
		}
		children = append(children, sc.text(sc.indexed("omp.tool.notice", i), nvPlainText(f)))
	}

	var footParts []string
	if hidden := len(hits) - maxHits; hidden > 0 {
		footParts = append(footParts, nvFormatCount("more hit", hidden))
	}
	if kw := nvStrSlice(details, "keywords"); len(kw) > 0 {
		footParts = append(footParts, "keywords: "+strings.Join(kw, ", "))
	}
	if statsNode := nvStatsNode(sc, footParts); statsNode != nil {
		children = append(children, statsNode)
	}
	return sc.container(sc.claim("omp.tool.find"), children...), nil
}

// ── ast_grep (tools/ast-grep.ts) ─────────────────────────────────────────────

func nativeAstGrepCall(_ sdk.Context, _ map[string]any, _ sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	return sc.container(sc.claim("omp.tool.ast_grep.call")), nil
}

func nvParseErrorsNotice(sc *nvScope, parseErrors []string, total int) kit.Node {
	if len(parseErrors) == 0 {
		return nil
	}
	shownTotal := total
	if shownTotal < len(parseErrors) {
		shownTotal = len(parseErrors)
	}
	head := fmt.Sprintf("Parse issues (%d)", shownTotal)
	return sc.text(sc.claim("omp.tool.notice"), head+": "+strings.Join(nvCompactStrings(parseErrors), "; "))
}

func nativeAstGrepResult(_ sdk.Context, res sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	details := nvToMap(res.Details)
	if rc.IsError {
		msg := nvResultText(res)
		if msg == "" {
			msg = "Unknown error"
		}
		return sc.container(sc.claim("omp.tool.ast_grep"), nvErrorNode(sc, sc.claim("omp.tool.error"), msg)), nil
	}

	matchCount := nvInt(details, "matchCount")
	limitReached := nvBool(details, "limitReached")
	parseErrors := nvStrSlice(details, "parseErrors")
	parseErrorsTotal := nvInt(details, "parseErrorsTotal")

	var children []kit.Node
	if matchCount == 0 {
		if len(parseErrors) > 0 {
			children = append(children, sc.text(sc.claim("omp.tool.notice"), "Query may be mis-scoped; narrow `path` before concluding absence"))
			children = append(children, nvParseErrorsNotice(sc, parseErrors, parseErrorsTotal))
		}
		return sc.container(sc.claim("omp.tool.ast_grep"), children...), nil
	}

	textContent := nvStr(details, "displayContent")
	if textContent == "" {
		textContent = nvResultText(res)
	}
	// Strip trailing "Result limit reached..." / "Parse issues:..." blocks before grouping.
	rawLines := strings.Split(strings.ReplaceAll(textContent, "\r\n", "\n"), "\n")
	var kept []string
	skipping := false
	for _, line := range rawLines {
		if strings.HasPrefix(line, "Result limit reached") || strings.HasPrefix(line, "Parse issues:") {
			skipping = true
			continue
		}
		if strings.TrimSpace(line) == "" && skipping {
			skipping = false
			continue
		}
		if !skipping {
			kept = append(kept, line)
		}
	}

	maxFiles := 0
	if !opts.Expanded {
		maxFiles = 8
	}
	children = append(children, nvDescribeGroupedOutput(sc, kept, "", maxFiles)...)
	if limitReached {
		children = append(children, sc.text(sc.claim("omp.tool.notice"), "limit reached; narrow path or use skip"))
	}
	if pe := nvParseErrorsNotice(sc, parseErrors, parseErrorsTotal); pe != nil {
		children = append(children, pe)
	}
	return sc.container(sc.claim("omp.tool.ast_grep"), children...), nil
}

// ── ast_edit (tools/ast-edit.ts) ─────────────────────────────────────────────

type nvAstEditFileDiff struct {
	path    string
	lines   []string
	added   int
	removed int
	lastOld int
	hasLast bool
}

// nvParseAstEditChanges ports parseAstEditChanges in packages/tui/src/tools/ast-edit.ts:133-156.
func nvParseAstEditChanges(lines []string) []nvAstEditFileDiff {
	var files []nvAstEditFileDiff
	var dirStack []string
	var cur *nvAstEditFileDiff

	flush := func() {
		if cur != nil && len(cur.lines) > 0 {
			files = append(files, *cur)
		}
		cur = nil
	}

	for _, line := range lines {
		if m := nvGroupedHeaderRE.FindStringSubmatch(line); m != nil {
			level := len(m[1])
			body := strings.TrimSpace(m[2])
			if strings.HasSuffix(body, "/") {
				flush()
				for len(dirStack) >= level {
					dirStack = dirStack[:len(dirStack)-1]
				}
				dirStack = append(dirStack, body)
				continue
			}
			flush()
			if sm := nvParenSuffixRE.FindStringSubmatch(body); sm != nil {
				body = strings.TrimSpace(sm[1])
			}
			body = nvHashTagSuffixRE.ReplaceAllString(body, "")
			fullPath := body
			if len(dirStack) > 0 && !strings.Contains(body, "://") && !strings.HasPrefix(body, "/") {
				prefix := ""
				for _, d := range dirStack[:min(len(dirStack), level-1)] {
					prefix += d
				}
				fullPath = prefix + body
			}
			cur = &nvAstEditFileDiff{path: fullPath}
			continue
		}
		if cur == nil {
			continue
		}
		cm := nvAstEditChangeLineRE.FindStringSubmatch(line)
		if cm == nil {
			continue
		}
		ln, _ := strconv.Atoi(cm[2])
		if !cur.hasLast || ln > cur.lastOld+1 || ln < cur.lastOld {
			cur.lines = append(cur.lines, fmt.Sprintf("@@ -%d +%d @@", ln, ln))
		}
		cur.lastOld = ln
		cur.hasLast = true
		switch cm[1] {
		case "+":
			cur.added++
		case "-":
			cur.removed++
		}
		cur.lines = append(cur.lines, cm[1]+nvSanitize(cm[3]))
	}
	flush()
	return files
}

func nvAstEditScopeStats(sc *nvScope, details map[string]any) kit.Node {
	var parts []string
	if scope := nvStr(details, "scopePath"); scope != "" {
		parts = append(parts, "in "+scope)
	}
	if searched := nvInt(details, "filesSearched"); searched > 0 {
		parts = append(parts, "searched "+nvFormatCount("file", searched))
	}
	if nvBool(details, "limitReached") {
		parts = append(parts, "limit reached; narrow path")
	}
	return nvStatsNode(sc, parts)
}

func nativeAstEditCall(_ sdk.Context, _ map[string]any, _ sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	return sc.container(sc.claim("omp.tool.ast_edit.call")), nil
}

func nativeAstEditResult(_ sdk.Context, res sdk.ToolRenderResult, _ sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	details := nvToMap(res.Details)
	if rc.IsError {
		msg := nvResultText(res)
		if msg == "" {
			msg = "Unknown error"
		}
		return sc.container(sc.claim("omp.tool.ast_edit"), nvErrorNode(sc, sc.claim("omp.tool.error"), msg)), nil
	}

	parseErrors := nvStrSlice(details, "parseErrors")
	parseErrorsTotal := nvInt(details, "parseErrorsTotal")
	totalReplacements := nvInt(details, "totalReplacements")

	if totalReplacements == 0 {
		var children []kit.Node
		children = append(children,
			sc.text(sc.indexed("omp.head.meta", 0), "0 replacements"),
			sc.text(sc.claim("omp.head.tone"), "warning"),
		)
		if pe := nvParseErrorsNotice(sc, parseErrors, parseErrorsTotal); pe != nil {
			children = append(children, pe)
		}
		if stats := nvAstEditScopeStats(sc, details); stats != nil {
			children = append(children, stats)
		}
		return sc.container(sc.claim("omp.tool.ast_edit"), children...), nil
	}

	textContent := nvStr(details, "displayContent")
	if textContent == "" {
		textContent = nvResultText(res)
	}
	rawLines := strings.Split(strings.ReplaceAll(textContent, "\r\n", "\n"), "\n")
	var kept []string
	skipping := false
	for _, line := range rawLines {
		if strings.HasPrefix(line, "Safety cap reached") || strings.HasPrefix(line, "Parse issues:") {
			skipping = true
			continue
		}
		if strings.TrimSpace(line) == "" && skipping {
			skipping = false
			continue
		}
		if !skipping {
			kept = append(kept, line)
		}
	}

	files := nvParseAstEditChanges(kept)
	fileCount := max(nvInt(details, "filesTouched"), len(files))
	added, removed := 0, 0
	for _, f := range files {
		added += f.added
		removed += f.removed
	}

	// Head overrides matching omp packages/tui/src/tools/ast-edit.ts:384-392.
	var children []kit.Node
	single := fileCount == 1 && len(files) == 1
	if single {
		children = append(children,
			sc.text(sc.claim("omp.head.target"), nvDisplayPath(files[0].path, rc.Cwd)),
			sc.text(sc.claim("omp.head.target_kind"), "path"),
		)
	} else {
		children = append(children,
			sc.text(sc.claim("omp.head.target"), nvFormatCount("file", fileCount)),
			sc.text(sc.claim("omp.head.target_kind"), "text"),
		)
	}
	var headMeta []string
	if added > 0 || removed > 0 {
		headMeta = append(headMeta, fmt.Sprintf("+%d −%d", added, removed))
	}
	headMeta = append(headMeta, nvFormatCount("replacement", totalReplacements))
	for i, mp := range headMeta {
		children = append(children, sc.text(sc.indexed("omp.head.meta", i), mp))
	}
	if !nvBool(details, "applied") {
		children = append(children, sc.text(sc.indexed("omp.head.badge", 0), "proposed:warning"))
	}
	if nvBool(details, "limitReached") {
		children = append(children, sc.text(sc.claim("omp.head.tone"), "warning"))
	}

	// Single rewrite pattern context line: upstream tools/ast-edit.ts:383, 394-395.
	if ops := nvSlice(rc.Args, "ops"); len(ops) == 1 {
		if op0 := nvToMap(ops[0]); op0 != nil {
			if pat := nvPlainLine(nvStr(op0, "pat")); pat != "" {
				children = append(children, sc.text(sc.claim("omp.tool.context"), pat))
			}
		}
	}

	switch {
	case single:
		children = append(children, sc.diff(sc.claim("omp.tool.ast_edit.diff"), strings.Join(files[0].lines, "\n"), files[0].path))
	case len(files) > 0:
		for i, f := range files {
			if !sc.canAdd(4) {
				children = append(children, nvStatsNode(sc, []string{fmt.Sprintf("+%d more files", len(files)-i)}))
				break
			}
			diffNode := sc.diff(sc.indexed("omp.tool.ast_edit.diff", i), strings.Join(f.lines, "\n"), f.path)
			children = append(children, nvFileDiffSection(sc, "omp.tool.ast_edit.file", i, f.path, "", f.added, f.removed, rc.Cwd, diffNode))
		}
	default:
		// upstream: tools/ast-edit.ts:396 — `noteText(kept.join("\n"))` (plain text, not ansi).
		if fallback := strings.TrimSpace(strings.Join(kept, "\n")); fallback != "" {
			children = append(children, sc.text(sc.claim("omp.tool.output"), nvPlainText(fallback)))
		}
	}

	if pe := nvParseErrorsNotice(sc, parseErrors, parseErrorsTotal); pe != nil {
		children = append(children, pe)
	}
	if stats := nvAstEditScopeStats(sc, details); stats != nil {
		children = append(children, stats)
	}
	return sc.container(sc.claim("omp.tool.ast_edit"), children...), nil
}
