package frontend

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/stencil-hq/tern-sdk/go/tern"
	"github.com/stencil-hq/tern-sdk/go/tern/ui"
)

// ompEditSection holds one file section parsed from an omp hashline `edit`
// payload (`[PATH#TAG]` followed by PUT/CUT/REM/MV lines).
// upstream: packages/tui/src/tools/edit.ts:728-785
type ompEditSection struct {
	path   string
	op     string // "delete" for REM, "" otherwise
	rename string // destination of MV
}

// parseOmpEditSections extracts file sections from omp's hashline `edit` input.
// Tolerant of partial streamed payloads: strips `#XXXX` tag and surrounding
// quotes, and records `REM` (delete) and `MV <dest>` (rename) directives.
func parseOmpEditSections(input string) []ompEditSection {
	input = strings.TrimPrefix(input, "\uFEFF")
	var sections []ompEditSection
	var current *ompEditSection
	for line := range strings.SplitSeq(input, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if p, ok := parseOmpEditHeader(line); ok {
			sections = append(sections, ompEditSection{path: p})
			current = &sections[len(sections)-1]
			continue
		}
		if current == nil {
			continue
		}
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "REM":
			current.op = "delete"
		case strings.HasPrefix(trimmed, "MV "):
			current.rename = normalizeOmpEditPath(trimmed[len("MV "):])
		}
	}
	return sections
}

func parseOmpEditHeader(line string) (string, bool) {
	trimmed := strings.TrimRight(line, " \t")
	if !strings.HasPrefix(trimmed, "[") {
		return "", false
	}
	body := trimmed[1:]
	body = strings.TrimSuffix(body, "]")
	p := normalizeOmpEditPath(body)
	if p == "" {
		return "", false
	}
	return p, true
}

func normalizeOmpEditPath(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if n := len(trimmed); n >= 5 && trimmed[n-5] == '#' && isHex4(trimmed[n-4:]) {
		trimmed = trimmed[:n-5]
	}
	if len(trimmed) >= 2 && ((trimmed[0] == '"' && trimmed[len(trimmed)-1] == '"') ||
		(trimmed[0] == '\'' && trimmed[len(trimmed)-1] == '\'')) {
		trimmed = trimmed[1 : len(trimmed)-1]
	}
	return trimmed
}

func isHex4(s string) bool {
	if len(s) != 4 {
		return false
	}
	for i := range 4 {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// splitOmpReadPath splits a `read` path with inline omp selectors
// (`file.go:10-20`, `file.go:10-20:raw`, `file.go:-5`) into the base filesystem
// path (for href links) and the selector suffix (for display).
func splitOmpReadPath(raw string) (base, sel string) {
	if raw == "" || strings.Contains(raw, "://") {
		return raw, ""
	}
	start := 0
	if len(raw) >= 2 && raw[1] == ':' && ((raw[0] >= 'a' && raw[0] <= 'z') || (raw[0] >= 'A' && raw[0] <= 'Z')) {
		start = 2
	}
	idx := strings.IndexByte(raw[start:], ':')
	if idx < 0 {
		return raw, ""
	}
	cut := start + idx
	candBase := raw[:cut]
	candSel := raw[cut+1:]
	if candBase == "" || candSel == "" {
		return raw, ""
	}
	if isOmpReadSelector(candSel) {
		return candBase, candSel
	}
	return raw, ""
}

func isOmpReadSelector(sel string) bool {
	for chunk := range strings.SplitSeq(sel, ":") {
		if chunk == "" {
			return false
		}
		lower := strings.ToLower(chunk)
		if lower == "raw" || lower == "conflicts" {
			continue
		}
		for part := range strings.SplitSeq(chunk, ",") {
			if !isOmpRangePart(part) {
				return false
			}
		}
	}
	return true
}

func isOmpRangePart(part string) bool {
	if part == "" {
		return false
	}
	if strings.HasPrefix(part, "-") {
		return isDigits(part[1:])
	}
	s := strings.TrimPrefix(strings.TrimPrefix(part, "L"), "l")
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return false
	}
	rest := s[i:]
	if rest == "" {
		return true
	}
	switch {
	case strings.HasPrefix(rest, ".."):
		rest = rest[2:]
	case rest[0] == '-' || rest[0] == '+':
		rest = rest[1:]
	default:
		return false
	}
	if rest == "" {
		return true
	}
	rest = strings.TrimPrefix(strings.TrimPrefix(rest, "L"), "l")
	return isDigits(rest)
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// ompToolHead describes the derived head props for an omp tool call at v19.1.1.
type ompToolHead struct {
	title      string
	target     string
	targetKind string
	hrefPath   string
	command    string
	meta       []any
	badges     []any
	inline     bool
}

// ompGitHubOpTitles mirrors OP_TITLES in packages/tui/src/tools/github.ts:43-53.
var ompGitHubOpTitles = map[string]string{
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

// deriveOmpToolHead maps omp's tool names and argument shapes to omp's native
// tool head fields at v19.1.1 (packages/tui/src/tools/*.ts).
// capitalizeFirst upper-cases the first UTF-8 rune of s without splitting
// multi-byte characters.
func capitalizeFirst(s string) string {
	if s == "" {
		return ""
	}
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[size:]
}

// truncateRunes truncates s to at most maxRunes UTF-8 runes, replacing the
// tail with "…" when truncated.
func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 || utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxRunes-1]) + "…"
}

func (s *session) deriveOmpToolHead(tool fe.ToolCard) ompToolHead {
	title := capitalizeFirst(tool.Name)
	args := tool.Arguments
	switch tool.Name {
	case "read":
		raw := firstStringArg(args, "path", "file_path")
		// upstream: tools/proc-render.ts:459-464 — card frame, empty -> "Proc jobs & services".
		if strings.HasPrefix(strings.ToLower(raw), "proc://") {
			target := strings.TrimSpace(raw[len("proc://"):])
			if target == "" {
				return ompToolHead{title: "Proc jobs & services"}
			}
			return ompToolHead{
				title:      "Proc",
				target:     target,
				targetKind: "text",
			}
		}
		// upstream: tools/cfg-render.ts:89-96, tools/cfg-url.ts:19-30 — card frame, dot-joined segments.
		if segs, ok := parseOmpCfgURL(raw); ok {
			target := "all settings"
			if len(segs) > 0 {
				target = strings.Join(segs, ".")
			}
			return ompToolHead{
				title:      "Config",
				target:     target,
				targetKind: "text",
			}
		}
		base, sel := splitOmpReadPath(raw)
		target := raw
		if target == "" {
			target = "…"
		}
		if sel == "" {
			if rangeStr := formatOffsetLimit(args); rangeStr != "" && base != "" {
				target = base + ":" + rangeStr
			}
		}
		return ompToolHead{
			title:      "Read",
			target:     target,
			targetKind: "path",
			hrefPath:   base,
			inline:     true,
		}

	case "edit":
		// upstream: tools/edit.ts:341-343, 1419-1439, 1484-1504
		// Precedence: args.file_path / args.path / details.path (tool.Diff.Path) /
		// first hashline section path; displayed as absolute, home-shortened path
		// (`displayPath(details.path)` + `:line`).
		h := ompToolHead{title: ompEditOpTitle(firstStringArg(args, "op"))}
		firstLine := firstChangedDiffLine(tool.Diff)
		var secPath, secOp, secRename string
		if input, ok := args["input"].(string); ok && input != "" {
			sections := parseOmpEditSections(input)
			if len(sections) > 1 {
				h.title = "Edit"
				h.target = fmt.Sprintf("%d files", len(sections))
				h.targetKind = "text"
				return h
			}
			if len(sections) == 1 {
				secPath = sections[0].path
				secOp = sections[0].op
				secRename = sections[0].rename
			}
		}
		if secOp != "" {
			h.title = ompEditOpTitle(secOp)
		}
		rawPath := firstStringArg(args, "file_path", "path")
		if rawPath == "" && tool.Diff != nil && tool.Diff.Path != "" {
			rawPath = tool.Diff.Path
		}
		if rawPath == "" {
			rawPath = secPath
		}
		if rawPath != "" {
			h.target = formatPathWithLine(s.ompEditDisplayPath(rawPath), firstLine)
			h.targetKind = "path"
			h.hrefPath = rawPath
		}
		if secRename != "" && secRename != rawPath {
			h.meta = append(h.meta, "→ "+s.ompEditDisplayPath(secRename))
		}
		return h
	case "write":
		// upstream: tools/write.ts:156-159 (`content.split("\n").length`).
		p := firstStringArg(args, "path", "file_path")
		h := ompToolHead{
			title:      "Write",
			target:     p,
			targetKind: "path",
			hrefPath:   p,
		}
		if content, ok := args["content"].(string); ok && content != "" {
			n := strings.Count(content, "\n") + 1
			label := "lines"
			if n == 1 {
				label = "line"
			}
			h.meta = []any{fmt.Sprintf("%d %s", n, label)}
		}
		return h

	case "bash":
		// upstream: tools/bash.ts:460-476, 1087-1088
		title := "Bash"
		if name, ok := args["name"].(string); ok && strings.TrimSpace(name) != "" {
			title = "Bash · " + strings.TrimSpace(name)
		}
		cmd, _ := args["command"].(string)
		return ompToolHead{
			title:   title,
			command: cmd,
			inline:  true,
		}

	case "grep":
		pattern, _ := args["pattern"].(string)
		if pattern == "" {
			pattern = "?"
		}
		var meta []any
		if p := firstStringArg(args, "path"); p != "" {
			meta = append(meta, "in "+p)
		}
		if c, ok := args["case"].(bool); ok && !c {
			meta = append(meta, "case:insensitive")
		}
		if g, ok := args["gitignore"].(bool); ok && !g {
			meta = append(meta, "gitignore:false")
		}
		if skip, ok := numberArg(args, "skip"); ok && skip > 0 {
			meta = append(meta, "skip:"+strconv.Itoa(skip))
		}
		return ompToolHead{
			title:      "Grep",
			target:     pattern,
			targetKind: "pattern",
			meta:       meta,
			inline:     true,
		}

	case "glob":
		target := firstStringArg(args, "path", "pattern")
		if target == "" {
			target = "*"
		}
		var meta []any
		if lim, ok := numberArg(args, "limit"); ok && lim > 0 {
			meta = append(meta, "limit:"+strconv.Itoa(lim))
		}
		return ompToolHead{
			title:      "Glob",
			target:     target,
			targetKind: "pattern",
			meta:       meta,
			inline:     true,
		}

	case "find":
		// upstream: tools/find.ts:397-405
		query, _ := args["query"].(string)
		var meta []any
		if kws := stringSliceArg(args, "grep_keywords"); len(kws) > 0 {
			meta = append(meta, strings.Join(kws, " "))
		}
		if p := firstStringArg(args, "path"); p != "" {
			meta = append(meta, "in "+p)
		}
		return ompToolHead{
			title:      "Find",
			target:     query,
			targetKind: "query",
			meta:       meta,
			inline:     true,
		}

	case "todo":
		// upstream: tools/todo.ts:301-326, 792-795
		return ompToolHead{
			title:      "Todo",
			target:     formatOmpTodoTarget(args),
			targetKind: "text",
		}

	case "ask":
		// upstream: tools/ask.ts:147-209 — inline while pending/running; card once answered.
		target, meta := formatOmpAskTarget(args)
		inline := tool.Status == fe.ToolPending || tool.Status == fe.ToolRunning
		return ompToolHead{
			title:      "Ask",
			target:     target,
			targetKind: "text",
			meta:       meta,
			inline:     inline,
		}
	case "web_search":
		// upstream: tools/web-search.ts:290-300, 334-418 — inline while pending/running; card once settled.
		query, _ := args["query"].(string)
		inline := tool.Status == fe.ToolPending || tool.Status == fe.ToolRunning
		return ompToolHead{
			title:      "Web search",
			target:     query,
			targetKind: "query",
			inline:     inline,
		}

	case "task":
		// upstream: tools/task.ts:507-511, 2100-2104
		return deriveOmpTaskHead(args)

	case "wait":
		// upstream: tools/wait.ts:864-905 — inline while pending/running (or interrupted); card when settled with jobs/agents/error.
		inline := tool.Status == fe.ToolPending || tool.Status == fe.ToolRunning
		return ompToolHead{
			title:  "Wait",
			inline: inline,
		}
	case "eval":
		// upstream: tools/eval.ts:760-775
		return deriveOmpEvalHead(args)

	case "lsp":
		// upstream: tools/lsp.ts:797-837
		return deriveOmpLspHead(args)

	case "ast_grep":
		// upstream: tools/ast-grep.ts:184-190 — card frame (not inline).
		pat := firstStringArg(args, "pat", "pattern")
		if pat == "" {
			pat = "?"
		}
		var meta []any
		if scope := ompPathList(args, "path", "paths"); scope != "" {
			meta = append(meta, "in "+scope)
		}
		if skip, ok := numberArg(args, "skip"); ok && skip > 0 {
			meta = append(meta, "skip:"+strconv.Itoa(skip))
		}
		return ompToolHead{
			title:      "AST Grep",
			target:     pat,
			targetKind: "pattern",
			meta:       meta,
		}

	case "ast_edit":
		// upstream: tools/ast-edit.ts:92-102, 312-320
		return deriveOmpAstEditHead(args)

	case "debug":
		// upstream: tools/debug.ts:104-132, 225-228 — card frame.
		return deriveOmpDebugHead(args)

	case "github":
		// upstream: tools/github.ts:43-58, 88-125, 705-716
		return deriveOmpGitHubHead(args)

	default:
		// upstream: tools/default-renderer.ts:196-203 (including checkpoint/rewind).
		target := formatOmpArgsInline(args, 160)
		return ompToolHead{
			title:      title,
			target:     target,
			targetKind: "text",
		}
	}
}

func ompEditOpTitle(op string) string {
	switch strings.ToLower(strings.TrimSpace(op)) {
	case "create":
		return "Create"
	case "delete":
		return "Delete"
	default:
		return "Edit"
	}
}

func formatPathWithLine(path string, line int) string {
	if path == "" || line <= 0 {
		return path
	}
	return path + ":" + strconv.Itoa(line)
}

// parseOmpCfgURL parses a `cfg://` URL into dot-joinable segments, stripping
// query/fragment and trailing `/save` (upstream: tools/cfg-url.ts:19-30).
func parseOmpCfgURL(raw string) ([]string, bool) {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(strings.ToLower(trimmed), "cfg://") {
		return nil, false
	}
	rest := trimmed[len("cfg://"):]
	if idx := strings.IndexAny(rest, "?#"); idx >= 0 {
		rest = rest[:idx]
	}
	parts := strings.FieldsFunc(rest, func(r rune) bool { return r == '/' || r == '.' })
	if len(parts) > 1 && parts[len(parts)-1] == "save" {
		parts = parts[:len(parts)-1]
	}
	return parts, true
}

func firstStringArg(args map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := args[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func stringSliceArg(args map[string]any, key string) []string {
	raw, ok := args[key]
	if !ok || raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		var out []string
		for _, s := range v {
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	case []any:
		var out []string
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		if v != "" {
			return []string{v}
		}
	}
	return nil
}

func ompPathList(args map[string]any, keys ...string) string {
	for _, k := range keys {
		if list := stringSliceArg(args, k); len(list) > 0 {
			return strings.Join(list, ", ")
		}
	}
	return ""
}

func numberArg(args map[string]any, key string) (int, bool) {
	v, ok := args[key]
	if !ok || v == nil {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	}
	return 0, false
}

func formatOffsetLimit(args map[string]any) string {
	offset, hasOffset := numberArg(args, "offset")
	limit, hasLimit := numberArg(args, "limit")
	if !hasOffset && !hasLimit {
		return ""
	}
	start := 1
	if hasOffset {
		start = offset
	}
	if hasLimit && limit > 0 {
		return fmt.Sprintf("%d-%d", start, start+limit-1)
	}
	return strconv.Itoa(start)
}

// formatOmpTodoTarget mirrors normalizeTodoArg + describeOp in
// packages/tui/src/tools/todo.ts:301-326.
func formatOmpTodoTarget(args map[string]any) string {
	if len(args) == 0 {
		return "update"
	}
	if opsRaw, ok := args["ops"].([]any); ok {
		var described []string
		for _, entry := range opsRaw {
			if m, ok := entry.(map[string]any); ok {
				described = append(described, describeOmpTodoOp(m))
			}
		}
		if len(described) == 0 {
			return "update"
		}
		return strings.Join(described, ", ")
	}
	if _, hasOp := args["op"].(string); hasOp {
		return describeOmpTodoOp(args)
	}
	return "update"
}

func describeOmpTodoOp(op map[string]any) string {
	verb := "update"
	if s, ok := op["op"].(string); ok && s != "" {
		verb = strings.TrimSpace(stripANSI(s))
	}
	parts := []string{verb}
	switch t := op["task"].(type) {
	case string:
		if t != "" {
			parts = append(parts, strings.TrimSpace(stripANSI(t)))
		}
	case []any:
		var tasks []string
		for _, entry := range t {
			if s, ok := entry.(string); ok && s != "" {
				tasks = append(tasks, strings.TrimSpace(stripANSI(s)))
			}
		}
		if len(tasks) == 1 {
			parts = append(parts, tasks[0])
		} else if len(tasks) > 1 {
			parts = append(parts, fmt.Sprintf("%d tasks", len(tasks)))
		}
	}
	if phase, ok := op["phase"].(string); ok && phase != "" {
		parts = append(parts, strings.TrimSpace(stripANSI(phase)))
	}
	if items, ok := op["items"].([]any); ok && len(items) > 0 {
		noun := "items"
		if len(items) == 1 {
			noun = "item"
		}
		parts = append(parts, fmt.Sprintf("%d %s", len(items), noun))
	}
	return strings.Join(parts, " ")
}

var (
	ompHtmlTagRe        = regexp.MustCompile(`<[^>]+>`)
	ompHtmlLegendRe     = regexp.MustCompile(`(?is)<legend\b[^>]*>(.*?)</legend>`)
	ompHtmlFieldRe      = regexp.MustCompile(`(?is)<(input|select|textarea)\b([^>]*)>`)
	ompHtmlBlockTokenRe = regexp.MustCompile(`(?is)<(fieldset|legend|p|h[1-6]|div|label|input|select|textarea)\b([^>]*)>|</(fieldset|legend|p|h[1-6]|div|label)>`)
	ompHtmlAttrRe       = regexp.MustCompile(`(?i)([a-zA-Z_:][-a-zA-Z0-9_:.]*)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>` + "`" + `]+)))?`)
)

func parseOmpHTMLAttrs(raw string) map[string]string {
	out := map[string]string{}
	for _, m := range ompHtmlAttrRe.FindAllStringSubmatch(raw, -1) {
		k := strings.ToLower(m[1])
		v := m[2]
		if v == "" {
			v = m[3]
		}
		if v == "" {
			v = m[4]
		}
		out[k] = v
	}
	return out
}

// ompAskHTMLQuestions derives field questions and distinct named field count
// matching omp's `fieldQuestions` (packages/tui/src/html-form/data.ts:146-165):
// own label (not option label inside radio/checkbox), else preceding text block
// (`<p>`, `<h1-6>`, `<div>`, bare text), else `<legend>`, else lone checkbox
// option label, else field name.
func ompAskHTMLQuestions(htmlStr string) (questions []string, fieldCount int) {
	// Tokenize sequentially so `<p>Choose a mode:</p>` before `<label><input type=radio ...> Fast</label>`
	// attaches "Choose a mode:" to the radio group rather than "Fast".
	type fieldRec struct {
		name      string
		isChoice  bool
		ownLabel  string
		caption   string
		legend    string
		optLabels []string
	}
	var order []string
	byName := map[string]*fieldRec{}
	var (
		curLegend     string
		curCaption    string
		inLabel       bool
		labelTextBuf  strings.Builder
		labelField    *fieldRec
		labelHadInput bool
		inBlock       string // "p", "h1".."h6", "legend", "div"
		blockBuf      strings.Builder
	)
	pos := 0
	locs := ompHtmlBlockTokenRe.FindAllStringSubmatchIndex(htmlStr, -1)
	appendPlain := func(chunk string) {
		plain := collapseWhitespace(ompHtmlTagRe.ReplaceAllString(chunk, " "))
		if plain == "" {
			return
		}
		if inBlock != "" {
			if blockBuf.Len() > 0 {
				blockBuf.WriteByte(' ')
			}
			blockBuf.WriteString(plain)
			return
		}
		if inLabel {
			if labelTextBuf.Len() > 0 {
				labelTextBuf.WriteByte(' ')
			}
			labelTextBuf.WriteString(plain)
			return
		}
		curCaption = plain
	}
	for _, loc := range locs {
		if loc[0] > pos {
			appendPlain(htmlStr[pos:loc[0]])
		}
		pos = loc[1]
		var openTag, attrsRaw, closeTag string
		if loc[2] >= 0 {
			openTag = strings.ToLower(htmlStr[loc[2]:loc[3]])
		}
		if loc[4] >= 0 {
			attrsRaw = htmlStr[loc[4]:loc[5]]
		}
		if loc[6] >= 0 {
			closeTag = strings.ToLower(htmlStr[loc[6]:loc[7]])
		}
		if closeTag != "" {
			switch closeTag {
			case "legend":
				curLegend = collapseWhitespace(blockBuf.String())
				blockBuf.Reset()
				inBlock = ""
			case "fieldset":
				curLegend = ""
				curCaption = ""
			case "p", "h1", "h2", "h3", "h4", "h5", "h6", "div":
				if inBlock == closeTag {
					if txt := collapseWhitespace(blockBuf.String()); txt != "" {
						curCaption = txt
					}
					blockBuf.Reset()
					inBlock = ""
				}
			case "label":
				if inLabel {
					lbl := collapseWhitespace(labelTextBuf.String())
					if labelField != nil {
						if labelField.isChoice {
							if lbl != "" {
								labelField.optLabels = append(labelField.optLabels, lbl)
							}
						} else if labelField.ownLabel == "" && lbl != "" {
							labelField.ownLabel = lbl
						}
					} else if !labelHadInput && lbl != "" {
						// Standalone `<label>Name</label>` preceding an input acts as caption.
						curCaption = lbl
					}
					inLabel = false
					labelTextBuf.Reset()
					labelField = nil
					labelHadInput = false
				}
			}
			continue
		}
		switch openTag {
		case "legend", "p", "h1", "h2", "h3", "h4", "h5", "h6", "div":
			if inBlock == "" {
				inBlock = openTag
				blockBuf.Reset()
			}
		case "label":
			inLabel = true
			labelTextBuf.Reset()
			labelField = nil
			labelHadInput = false
		case "input", "select", "textarea":
			attrs := parseOmpHTMLAttrs(attrsRaw)
			typ := strings.ToLower(attrs["type"])
			if openTag == "input" && (typ == "hidden" || typ == "submit" || typ == "button" || typ == "reset") {
				continue
			}
			if inLabel {
				labelHadInput = true
			}
			name := strings.TrimSpace(attrs["name"])
			if name == "" {
				continue
			}
			isChoice := openTag == "input" && (typ == "radio" || typ == "checkbox")
			rec, ok := byName[name]
			if !ok {
				rec = &fieldRec{
					name:     name,
					isChoice: isChoice,
					caption:  curCaption,
					legend:   curLegend,
				}
				byName[name] = rec
				order = append(order, name)
				// Consume caption for the first occurrence of this field (html-form/data.ts:158).
				curCaption = ""
			}
			if inLabel {
				labelField = rec
			}
		}
	}
	if pos < len(htmlStr) {
		appendPlain(htmlStr[pos:])
	}

	for _, name := range order {
		rec := byName[name]
		q := rec.ownLabel
		if q == "" {
			q = rec.caption
		}
		if q == "" {
			q = rec.legend
		}
		if q == "" && rec.isChoice && len(rec.optLabels) == 1 {
			q = rec.optLabels[0]
		}
		if q == "" {
			q = rec.name
		}
		questions = append(questions, q)
	}
	return questions, len(order)
}

// formatOmpAskTarget extracts the first question and field count from omp's
// `ask` HTML form argument (`args.html`, upstream: tools/ask.ts:147-167), with
// fallback for legacy `questions[]` payloads.
func formatOmpAskTarget(args map[string]any) (string, []any) {
	if htmlStr, ok := args["html"].(string); ok && strings.TrimSpace(htmlStr) != "" {
		questions, count := ompAskHTMLQuestions(htmlStr)
		first := ""
		if len(questions) > 0 {
			first = questions[0]
		}
		var meta []any
		if count > 1 {
			meta = []any{fmt.Sprintf("%d fields", count)}
		}
		return first, meta
	}
	if qs, ok := args["questions"].([]any); ok && len(qs) > 0 {
		var first string
		if q0, ok := qs[0].(map[string]any); ok {
			if q, ok := q0["question"].(string); ok {
				first, _, _ = strings.Cut(strings.TrimSpace(q), "\n")
			}
		}
		var meta []any
		if len(qs) > 1 {
			meta = []any{fmt.Sprintf("%d questions", len(qs))}
		}
		return first, meta
	}
	if q, ok := args["question"].(string); ok && q != "" {
		first, _, _ := strings.Cut(strings.TrimSpace(q), "\n")
		return first, nil
	}
	return "", nil
}

func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(stripANSI(s)), " ")
}

// applyDisplayPath normalizes a working directory path with Options.DisplayPath
// when set (e.g. stripping macOS `/private` when resolving a cwd-relative path).
func (s *session) applyDisplayPath(p string) string {
	if s != nil && s.displayPath != nil && p != "" {
		if norm := s.displayPath(p); norm != "" {
			return norm
		}
	}
	return p
}

// ompEditDisplayPath resolves an edit path against the session's working
// directory and abbreviates the user's home directory prefix to `~`, matching
// omp's `displayPath(details.path)` for edit heads (tools/edit.ts:1429, 1504).
// Applies Options.DisplayPath ONLY when resolving a relative path against
// s.cwdAbs; absolute paths supplied by tool details/args stay verbatim.
func (s *session) ompEditDisplayPath(raw string) string {
	if raw == "" {
		return ""
	}
	home := ""
	if s != nil && s.env.Getenv != nil {
		home = s.env.Getenv("HOME")
	}
	p := raw
	if !filepath.IsAbs(p) {
		if expanded := absCwd(p, home); expanded != "" {
			p = expanded
		} else if s != nil && s.cwdAbs != "" {
			p = filepath.Join(s.applyDisplayPath(s.cwdAbs), p)
		}
	}
	if home != "" {
		for _, cand := range []string{strings.TrimRight(home, "/\\"), strings.TrimRight(s.applyDisplayPath(home), "/\\")} {
			if cand == "" {
				continue
			}
			if p == cand {
				return "~"
			}
			if strings.HasPrefix(p, cand+"/") || strings.HasPrefix(p, cand+"\\") {
				return "~" + p[len(cand):]
			}
		}
	}
	return p
}

// ompFileHref builds a `file:///` URI for an absolute or cwd-relative file path
// under ompRoles (upstream: packages/tui/src/tools/native-view.ts:27-29 `fileHref`).
// Applies Options.DisplayPath ONLY when resolving a relative path against
// s.cwdAbs; absolute paths supplied by tool details/args stay verbatim.
// Returns "" when the path cannot be resolved to an absolute path.
func (s *session) ompFileHref(target string) string {
	if s == nil || target == "" {
		return ""
	}
	p := target
	if !filepath.IsAbs(p) {
		home := ""
		if s.env.Getenv != nil {
			home = s.env.Getenv("HOME")
		}
		if expanded := absCwd(p, home); expanded != "" {
			p = expanded
		} else if s.cwdAbs != "" {
			p = filepath.Join(s.applyDisplayPath(s.cwdAbs), p)
		} else {
			return ""
		}
	}
	return fileURL("", p)
}

// deriveOmpTaskHead mirrors formatAgentHeaderLabel + describeTaskCall
// (upstream: tools/task.ts:507-511, 2100-2104):
// several tasks -> "N agents"; otherwise flat args.agent, else empty.
func deriveOmpTaskHead(args map[string]any) ompToolHead {
	h := ompToolHead{title: "Task"}
	if tasks, ok := args["tasks"].([]any); ok && len(tasks) > 1 {
		h.target = fmt.Sprintf("%d agents", len(tasks))
		h.targetKind = "text"
		return h
	}
	if flat, ok := args["agent"].(string); ok && strings.TrimSpace(flat) != "" {
		h.target = strings.TrimSpace(flat)
		h.targetKind = "text"
	}
	return h
}

// deriveOmpEvalHead mirrors evalToolHead (upstream: tools/eval.ts:760-775):
// single cell's title as title (else "Eval"), "N cells" in meta when >1,
// languages as badges in first-use order, no target.
func deriveOmpEvalHead(args map[string]any) ompToolHead {
	var title string
	var langs []string
	seenLang := map[string]bool{}
	addLang := func(l string) {
		l = strings.TrimSpace(l)
		if l == "" {
			return
		}
		switch strings.ToLower(l) {
		case "py":
			l = "python"
		case "js":
			l = "js"
		}
		if !seenLang[l] {
			seenLang[l] = true
			langs = append(langs, l)
		}
	}
	cellCount := 1
	if cells, ok := args["cells"].([]any); ok && len(cells) > 0 {
		cellCount = len(cells)
		for i, raw := range cells {
			if m, ok := raw.(map[string]any); ok {
				if i == 0 && cellCount == 1 {
					title = firstStringArg(m, "title")
				}
				addLang(firstStringArg(m, "language"))
			}
		}
	} else {
		title = firstStringArg(args, "title")
		addLang(firstStringArg(args, "language"))
	}
	headTitle := strings.TrimSpace(stripANSI(title))
	if cellCount > 1 || headTitle == "" {
		headTitle = "Eval"
	}
	var badges []any
	for _, l := range langs {
		badges = append(badges, map[string]any{"text": l})
	}
	var meta []any
	if cellCount > 1 {
		meta = append(meta, fmt.Sprintf("%d cells", cellCount))
	}
	return ompToolHead{
		title:  headTitle,
		badges: badges,
		meta:   meta,
	}
}

// deriveOmpLspHead mirrors lspActionTitle + lspNativeHead
// (upstream: tools/lsp.ts:797-837).
func deriveOmpLspHead(args map[string]any) ompToolHead {
	action := firstStringArg(args, "action")
	title := "LSP"
	if action != "" && action != "request" {
		words := strings.ReplaceAll(action, "_", " ")
		title = capitalizeFirst(words)
	}
	var target, targetKind, hrefPath string
	var facts []string
	file := firstStringArg(args, "file")
	query := firstStringArg(args, "query")
	line, hasLine := numberArg(args, "line")
	if file != "" {
		target = file
		if hasLine {
			target = file + ":" + strconv.Itoa(line)
		}
		targetKind = "path"
		hrefPath = file
	} else if query != "" {
		target = query
		targetKind = "query"
	} else if hasLine {
		facts = append(facts, "line "+strconv.Itoa(line))
	}
	if sym := firstStringArg(args, "symbol"); sym != "" {
		facts = append(facts, strings.ReplaceAll(strings.ReplaceAll(stripANSI(sym), "\r\n", " "), "\n", " "))
	}
	if query != "" && target != query {
		facts = append(facts, "query:"+query)
	}
	if newName := firstStringArg(args, "new_name"); newName != "" {
		facts = append(facts, "\u2192 "+newName)
	}
	if apply, ok := args["apply"].(bool); ok {
		if apply {
			facts = append(facts, "apply:true")
		} else {
			facts = append(facts, "apply:false")
		}
	}
	var meta []any
	if len(facts) > 0 {
		meta = []any{strings.Join(facts, " \u00b7 ")}
	}
	return ompToolHead{
		title:      title,
		target:     target,
		targetKind: targetKind,
		hrefPath:   hrefPath,
		meta:       meta,
		inline:     true,
	}
}

// deriveOmpAstEditHead mirrors patternPreview + rewriteLabel + describeCall
// (upstream: tools/ast-edit.ts:92-102, 312-320).
func deriveOmpAstEditHead(args map[string]any) ompToolHead {
	var target, targetKind string
	if ops, ok := args["ops"].([]any); ok && len(ops) > 0 {
		if len(ops) == 1 {
			if m, ok := ops[0].(map[string]any); ok {
				if pat, ok := m["pat"].(string); ok {
					if collapsed := collapseWhitespace(pat); collapsed != "" {
						target = collapsed
						targetKind = "pattern"
					}
				}
			}
		} else {
			target = fmt.Sprintf("%d rewrites", len(ops))
			targetKind = "text"
		}
	}
	var meta []any
	if paths := stringSliceArg(args, "paths"); len(paths) > 0 {
		meta = append(meta, "in "+strings.Join(paths, ", "))
	}
	return ompToolHead{
		title:      "AST Edit",
		target:     target,
		targetKind: targetKind,
		meta:       meta,
	}
}

// deriveOmpDebugHead mirrors debugCallTarget + debugCallParts + describeCall
// (upstream: tools/debug.ts:104-132, 225-228).
func deriveOmpDebugHead(args map[string]any) ompToolHead {
	action := firstStringArg(args, "action")
	if action == "" {
		action = "request"
	}
	action = collapseWhitespace(strings.ReplaceAll(action, "_", " "))
	var target string
	switch {
	case firstStringArg(args, "program") != "":
		target = firstStringArg(args, "program")
	case firstStringArg(args, "file") != "":
		if line, ok := numberArg(args, "line"); ok {
			target = firstStringArg(args, "file") + ":" + strconv.Itoa(line)
		}
	}
	if target == "" {
		target = firstStringArg(args, "function", "expression", "command", "memory_reference", "instruction_reference", "data_id", "name")
	}
	var meta []any
	if target != "" {
		meta = append(meta, collapseWhitespace(target))
	}
	return ompToolHead{
		title:      "Debug",
		target:     action,
		targetKind: "text",
		meta:       meta,
	}
}

var ompGitHubIssueURLRe = regexp.MustCompile(`/(?:issues|pull)/(\d+)`)

func extractGitHubIssueID(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if isDigits(trimmed) {
		return "#" + trimmed
	}
	if m := ompGitHubIssueURLRe.FindStringSubmatch(trimmed); len(m) == 2 {
		return "#" + m[1]
	}
	return trimmed
}

func formatGitHubPRIdentifier(raw any) string {
	switch v := raw.(type) {
	case string:
		return extractGitHubIssueID(v)
	case float64:
		return "#" + strconv.Itoa(int(v))
	case int:
		return "#" + strconv.Itoa(v)
	case []any:
		var parts []string
		for _, item := range v {
			if s, ok := item.(string); ok {
				if id := extractGitHubIssueID(s); id != "" {
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
	}
	return ""
}

// deriveOmpGitHubHead mirrors formatOpTitle + buildOpMeta + describeCall
// (upstream: tools/github.ts:43-58, 88-125, 705-716).
func deriveOmpGitHubHead(args map[string]any) ompToolHead {
	op := strings.TrimSpace(firstStringArg(args, "op"))
	if op == "run_watch" {
		runID := strings.TrimSpace(firstStringArg(args, "run"))
		branch := strings.TrimSpace(firstStringArg(args, "branch"))
		target := "current HEAD"
		if runID != "" {
			target = "#" + runID
		} else if branch != "" {
			target = branch
		}
		return ompToolHead{
			title:      "GitHub Run Watch",
			target:     target,
			targetKind: "text",
		}
	}
	title := "GitHub"
	if t, ok := ompGitHubOpTitles[op]; ok {
		title = t
	}
	var facts []string
	switch op {
	case "pr_checkout", "pr_push":
		if id := formatGitHubPRIdentifier(args["pr"]); id != "" {
			facts = append(facts, id)
		} else if br := firstStringArg(args, "branch"); br != "" {
			facts = append(facts, br)
		}
		if repo := firstStringArg(args, "repo"); repo != "" {
			facts = append(facts, repo)
		}
	case "search_issues", "search_prs", "search_code", "search_commits":
		if q := firstStringArg(args, "query"); q != "" {
			facts = append(facts, q)
		}
		if repo := firstStringArg(args, "repo"); repo != "" {
			facts = append(facts, repo)
		}
	case "search_repos":
		if q := firstStringArg(args, "query"); q != "" {
			facts = append(facts, q)
		}
	case "repo_view":
		if repo := firstStringArg(args, "repo"); repo != "" {
			facts = append(facts, repo)
		}
		if br := firstStringArg(args, "branch"); br != "" {
			facts = append(facts, br)
		}
	default:
		if repo := firstStringArg(args, "repo"); repo != "" {
			facts = append(facts, repo)
		}
	}
	h := ompToolHead{title: title}
	if len(facts) > 0 {
		h.target = facts[0]
		h.targetKind = "text"
		for _, f := range facts[1:] {
			h.meta = append(h.meta, f)
		}
	}
	return h
}

// formatOmpArgsInline mirrors formatArgsInline(..., 160, {characterBudget: true})
// (upstream: tools/json-tree.ts:47-95, tools/default-renderer.ts:199-203).
func formatOmpArgsInline(args map[string]any, maxChars int) string {
	if len(args) == 0 {
		return ""
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		if k == "i" || k == "__partialJson" {
			continue
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	var pairs []string
	length := 0
	for _, k := range keys {
		pair := stripANSI(k) + "=" + formatOmpScalar(args[k], 24)
		added := len(pair)
		if len(pairs) > 0 {
			added += 2
		}
		if length+added > maxChars && len(pairs) > 0 {
			pairs = append(pairs, "…")
			break
		}
		pairs = append(pairs, pair)
		length += added
	}
	return strings.Join(pairs, ", ")
}

func formatOmpScalar(v any, maxLen int) string {
	switch val := v.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(val)
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	case int:
		return strconv.Itoa(val)
	case int64:
		return strconv.FormatInt(val, 10)
	case string:
		escaped := strings.ReplaceAll(strings.ReplaceAll(stripANSI(val), "\n", `\n`), "\t", `\t`)
		if runes := []rune(escaped); len(runes) > maxLen {
			escaped = string(runes[:maxLen-1]) + "…"
		}
		return `"` + escaped + `"`
	case []any:
		return fmt.Sprintf("[%d items]", len(val))
	case map[string]any:
		return fmt.Sprintf("{%d keys}", len(val))
	default:
		return fmt.Sprint(val)
	}
}

// ompToolNode builds omp's data-first `tool` node (chat/tool-execution.ts:1049-1094).
func (s *session) ompToolNode(id string, tool fe.ToolCard, startedAt, endedAt time.Time) tern.WireNode {
	head := s.deriveOmpToolHead(tool)
	frame := "card"
	if head.inline {
		frame = "inline"
	}
	props := map[string]any{
		"role":   "omp.tool." + tool.Name,
		"name":   tool.Name,
		"title":  head.title,
		"status": string(tool.Status),
		"frame":  frame,
	}
	// upstream: tools/bash.ts:1073-1076 — bash does not put tone="error" on the
	// tool wrapper itself (the run box carries tone="error").
	if tool.Status == fe.ToolError && tool.Name != "bash" {
		props["tone"] = "error"
	}
	if head.target != "" {
		props["target"] = head.target
		props["targetKind"] = head.targetKind
	}
	if head.command != "" {
		props["command"] = head.command
	}
	if head.hrefPath != "" && !(tool.Name == "read" && tool.Status == fe.ToolError) {
		if href := s.ompFileHref(head.hrefPath); href != "" {
			props["href"] = href
		}
	}
	if len(head.meta) > 0 {
		props["meta"] = head.meta
	}
	if len(head.badges) > 0 {
		props["badges"] = head.badges
	}
	intent := ""
	if v, ok := tool.Arguments["i"].(string); ok && strings.TrimSpace(v) != "" {
		intent = strings.TrimSpace(stripANSI(v))
		props["intent"] = intent
	} else if tool.Name == "eval" {
		// upstream: packages/coding-agent/src/tools/eval.ts:386-390 — EvalTool.intent
		// derives intent from args.title (or "running <language>") even when `i` is absent.
		if title := strings.TrimSpace(stripANSI(firstStringArg(tool.Arguments, "title"))); title != "" {
			intent = title
			props["intent"] = intent
		} else if lang := ompEvalHighlightLang(tool.Arguments); lang != "" {
			intent = "running " + lang
			props["intent"] = intent
		}
	}
	// upstream: tools/bash.ts:469-471 — bash sets intent as target on BOTH
	// the native run-box path and the ResultView path (fixes A11).
	if tool.Name == "bash" && intent != "" {
		props["target"] = intent
		props["targetKind"] = "text"
	}
	live := tool.Status == fe.ToolPending || tool.Status == fe.ToolRunning
	var elapsedMs int64
	hasElapsed := false
	if live {
		if !startedAt.IsZero() {
			if d := s.now().Sub(startedAt); d >= 0 {
				elapsedMs = d.Milliseconds()
				hasElapsed = true
				props["age"] = elapsedMs
			}
		} else if tool.Status == fe.ToolRunning {
			hasElapsed = true
			props["age"] = int64(0)
		}
	} else {
		if tool.Elapsed > 0 {
			elapsedMs = tool.Elapsed.Milliseconds()
			hasElapsed = true
			props["took"] = elapsedMs
		} else if !startedAt.IsZero() && !endedAt.IsZero() {
			if d := endedAt.Sub(startedAt); d > 0 {
				elapsedMs = d.Milliseconds()
				hasElapsed = true
				props["took"] = elapsedMs
			}
		}
	}
	if tool.Status == fe.ToolCancelled {
		props["note"] = "cancelled"
	}

	hasView := validView(tool.ResultView, tool.Result) && len(tool.Result) > 0
	// Displaced tool card (e.g. an earlier `todo` card displaced by a newer todo
	// snapshot: internal/ansi/register.go:88 returns an empty kit.NewContainer(),
	// which renders 0 lines; PiG's `toolRenderProxy.FrontendView` returns nil
	// when `len(p.lines) == 0` while `tool.Result` is non-nil empty `[]string{}`).
	// Hide the entire tool node so Tern never renders duplicate displaced cards
	// (upstream: event-controller.ts:2376-2386).
	displacedEmptyContainer := tool.ResultView != nil && tool.ResultView.Root.ID == "" &&
		len(tool.ResultView.Root.Children) == 0 && tool.ResultView.Root.Rows == 0 &&
		len(tool.Result) == 0 && tool.Diff == nil && len(tool.Images) == 0
	displacedTodoEmptyLines := tool.Name == "todo" && tool.Status == fe.ToolDone &&
		tool.ResultView == nil && tool.Result != nil && len(tool.Result) == 0 &&
		tool.Diff == nil && len(tool.Images) == 0
	if displacedEmptyContainer || displacedTodoEmptyLines {
		s.dropViewRules(id)
		return tern.WireNode{ID: id, K: tern.KindCol, P: map[string]any{"hidden": true}}
	}
	// Check for an empty `omp.tool.read` container (Finding 1): omp routes
	// filesystem reads through ReadToolGroupComponent.#describeTool with no
	// body by default and puts short single-line errors in the head meta.
	// When internal/ansi emits an empty omp.tool.read container (0 children),
	// take ompReadToolNode even though a ResultView is attached.
	emptyReadView := tool.Name == "read" && tool.ResultView != nil &&
		tool.ResultView.Root.ID == "omp.tool.read" && len(tool.ResultView.Root.Children) == 0
	if tool.Name == "read" && tool.Diff == nil && head.inline && (!hasView || emptyReadView) {
		return s.ompReadToolNode(id, tool, props)
	}
	// upstream: packages/tui/src/tools/ask.ts:160-167 (describeAskCall) — while
	// pending/running (before a ResultView arrives), the inline ask tool card
	// carries a single muted `text` child "Waiting for your answer…", collapsible
	// and collapsed by default.
	if tool.Name == "ask" && live && !hasView {
		s.dropViewRules(id)
		props["collapsible"] = true
		props["collapsed"] = !tool.Expanded
		props["preview"] = "none"
		return tern.WireNode{
			ID: id,
			K:  tern.KindTool,
			P:  props,
			C: []tern.WireNode{
				{
					ID: id + ".waiting",
					K:  tern.KindText,
					P:  map[string]any{"spans": []any{map[string]any{"t": "Waiting for your answer…", "s": "muted"}}},
				},
			},
		}
	}
	if tool.Name == "bash" && tool.Diff == nil && !hasView {
		return s.ompBashToolNode(id, tool, props, head.command, intent, live, elapsedMs, hasElapsed)
	}
	var children []tern.WireNode
	var preview any = map[string]any{"lines": structuredPreviewLines}
	forceOpen := false
	if hasView && (tool.Name == "bash" || tool.Name == "eval" || tool.Name == "wait") {
		// upstream: tools/bash.ts:1074, tools/eval.ts:1294, tools/wait.ts:868 (fixes A11/N2:
		// without a structured ResultView, fallback .out/.json children have no preview of
		// their own and need the tool-level clamp).
		preview = "children"
	}
	switch {
	case tool.Diff != nil && !(tool.Name == "write" && hasView):
		// upstream: tools/write.ts:774-807 — `write` renders as `code` (`omp.tool.write.source`)
		// with line-count meta (not a diff), even if PiG's host synthesized a Diff for it.
		diffPath := tool.Diff.Path
		if diffPath == "" {
			diffPath = head.hrefPath
		}
		meta := make([]any, 0, len(head.meta)+1)
		if stat := diffStat(tool.Diff.Text); stat != "+0 −0" {
			meta = append(meta, stat)
		}
		meta = append(meta, head.meta...)
		if len(meta) > 0 {
			props["meta"] = meta
		}
		if diffPath != "" && props["href"] == nil {
			if href := s.ompFileHref(diffPath); href != "" {
				props["href"] = href
			}
		}
		diffProps := ompDiffProps(tool.Diff.Text, s.ompEditDisplayPath(diffPath))
		s.dropViewRules(id)
		children = []tern.WireNode{
			{ID: id + ".diff", K: tern.KindDiff, P: diffProps},
		}
	case hasView:
		var headPatch ompViewHeadPatch
		children, headPatch = s.ompViewBodyChildrenWithHead(id, tool, tool.ResultView, tool.Result)
		applyOmpHeadPatch(props, &preview, &forceOpen, headPatch)
	default:
		s.dropViewRules(id)
		if (tool.Status == fe.ToolDone || tool.Status == fe.ToolError) && tool.Output != "" {
			if v, ok := parseToolJSON(tool.Output); ok {
				if s.hasJSON {
					children = []tern.WireNode{wireNode(id+".json", (ui.JSON{Value: v, Open: 1}).Node())}
				} else {
					children = []tern.WireNode{wireNode(id+".tree", (ui.Tree{Nodes: jsonTreeNodes("", v, 1)}).Node())}
				}
			} else if nodes, note, ok := parseToolFileTree(tool.Name, tool.Output); ok && tool.Status == fe.ToolDone {
				children = []tern.WireNode{wireNode(id+".tree", (ui.Tree{Nodes: nodes}).Node())}
				if note != "" {
					children = append(children, wireNode(id+".note", (ui.Text{Spans: []ui.Span{{T: note, S: "warning"}}}).Node()))
				}
			}
		}
		if len(children) == 0 {
			body := tool.Output
			resultLines, fallbackPatch := stripOmpHeadResultLines(tool.ResultView, tool.Result)
			applyOmpHeadPatch(props, &preview, &forceOpen, fallbackPatch)
			if rendered := linesText(resultLines); !blank(rendered) {
				body = rendered
			}
			if body != "" {
				if preview != "children" {
					preview = map[string]any{"tail": 5}
				}
				children = []tern.WireNode{
					{ID: id + ".out", K: tern.KindAnsi, P: map[string]any{"text": body}},
				}
			}
		}
	}
	hasBody := len(children) > 0
	props["collapsible"] = hasBody
	if hasBody {
		if forceOpen {
			props["collapsed"] = false
		} else {
			props["collapsed"] = !tool.Expanded
		}
		props["preview"] = preview
	}
	return tern.WireNode{ID: id, K: tern.KindTool, P: props, C: children}
}

func applyOmpHeadPatch(props map[string]any, preview *any, forceOpen *bool, headPatch ompViewHeadPatch) {
	if headPatch.title != nil {
		props["title"] = headPatch.title
	}
	if headPatch.target != "" {
		props["target"] = headPatch.target
		if headPatch.targetKind != "" {
			props["targetKind"] = headPatch.targetKind
		} else {
			props["targetKind"] = "text"
		}
	}
	if headPatch.metaSet {
		if len(headPatch.meta) > 0 {
			props["meta"] = headPatch.meta
		} else {
			delete(props, "meta")
		}
	} else if len(headPatch.meta) > 0 {
		props["meta"] = headPatch.meta
	}
	if len(headPatch.badges) > 0 {
		props["badges"] = headPatch.badges
	}
	if headPatch.frame != "" {
		props["frame"] = headPatch.frame
	}
	if headPatch.tone != "" {
		props["tone"] = headPatch.tone
	}
	if headPatch.exit != nil {
		props["exit"] = *headPatch.exit
	}
	if headPatch.preview != nil && preview != nil {
		*preview = headPatch.preview
	}
	if headPatch.open && forceOpen != nil {
		*forceOpen = true
	}
}

// ompDiffProps builds Tern `diff` node props using structured `hunks` parsed
// from unified diff text when possible, falling back to `text` (matching omp's
// `chrome/diff.ts:121-162` and `tern/ui/text.go:185-204`).
func ompDiffProps(diffText, diffPath string) map[string]any {
	props := map[string]any{}
	if diffPath != "" {
		props["path"] = diffPath
	}
	if hunks := parseUnifiedDiffHunks(diffText); len(hunks) > 0 {
		props["hunks"] = hunks
		return props
	}
	props["text"] = diffText
	return props
}

// parseUnifiedDiffHunks converts unified diff text (`@@ -a[,b] +c[,d] @@`)
// into Tern's structured `hunks` array (`[{oldStart, newStart, lines}]`).
func parseUnifiedDiffHunks(diffText string) []any {
	if diffText == "" {
		return nil
	}
	var hunks []any
	var curLines []string
	oldStart, newStart := 0, 0
	oldRem, newRem := 0, 0
	changed := false
	flush := func() {
		if len(curLines) > 0 && changed {
			hunks = append(hunks, map[string]any{
				"oldStart": oldStart,
				"newStart": newStart,
				"lines":    curLines,
			})
		}
		curLines = nil
		changed = false
	}
	for line := range strings.SplitSeq(diffText, "\n") {
		if oldRem == 0 && newRem == 0 {
			if strings.HasPrefix(line, "@@ ") {
				oStart, oCount, nStart, nCount, ok := parseUnifiedHunkHeaderFull(line)
				if ok {
					flush()
					oldStart, newStart = oStart, nStart
					oldRem, newRem = oCount, nCount
				}
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "+"):
			curLines = append(curLines, line)
			changed = true
			if newRem > 0 {
				newRem--
			}
		case strings.HasPrefix(line, "-"):
			curLines = append(curLines, line)
			changed = true
			if oldRem > 0 {
				oldRem--
			}
		case strings.HasPrefix(line, " "):
			curLines = append(curLines, line)
			if oldRem > 0 {
				oldRem--
			}
			if newRem > 0 {
				newRem--
			}
		case strings.HasPrefix(line, `\`):
			// "\ No newline at end of file" does not consume hunk line counts.
		default:
			flush()
			oldRem, newRem = 0, 0
			if strings.HasPrefix(line, "@@ ") {
				oStart, oCount, nStart, nCount, ok := parseUnifiedHunkHeaderFull(line)
				if ok {
					oldStart, newStart = oStart, nStart
					oldRem, newRem = oCount, nCount
				}
			}
		}
	}
	flush()
	return hunks
}

// parseUnifiedHunkHeaderFull parses `@@ -oldStart[,oldCount] +newStart[,newCount] @@`.
func parseUnifiedHunkHeaderFull(line string) (oldStart, oldCount, newStart, newCount int, ok bool) {
	rest, found := strings.CutPrefix(line, "@@ -")
	if !found {
		return 0, 0, 0, 0, false
	}
	oldPart, afterOld, found := strings.Cut(rest, " +")
	if !found {
		return 0, 0, 0, 0, false
	}
	newPart, _, found := strings.Cut(afterOld, " @@")
	if !found {
		newPart, _, _ = strings.Cut(afterOld, " ")
	}
	oldStart, oldCount, ok = parseHunkRange(oldPart)
	if !ok {
		return 0, 0, 0, 0, false
	}
	newStart, newCount, ok = parseHunkRange(newPart)
	return oldStart, oldCount, newStart, newCount, ok
}

// ompReadToolNode builds omp's inline `read` tool node (chat/read-tool-group.ts:800-864).
func (s *session) ompReadToolNode(id string, tool fe.ToolCard, props map[string]any) tern.WireNode {
	s.dropViewRules(id)
	props["frame"] = "inline"
	// upstream: packages/tui/src/chat/read-tool-group.ts:848-863 — ReadToolGroupComponent
	// omits `took` (and `age`) on inline filesystem read rows.
	delete(props, "took")
	delete(props, "age")
	var children []tern.WireNode
	if tool.Status == fe.ToolError {
		delete(props, "href")
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
			full = "failed"
		}
		first, _, _ := strings.Cut(full, "\n")
		first = strings.TrimSpace(first)
		gist := truncateRunes(first, 120)
		props["meta"] = []any{[]any{map[string]any{"t": "— " + gist, "s": "muted"}}}
		if gist != full {
			children = append(children, tern.WireNode{
				ID: id + ".err",
				K:  tern.KindText,
				P:  map[string]any{"spans": []any{map[string]any{"t": full, "s": "error"}}, "wrap": "word", "role": "omp.tool.error"},
			})
		}
	}
	children = s.appendToolImages(id, tool, children)
	hasBody := len(children) > 0
	props["collapsible"] = hasBody
	if hasBody {
		props["collapsed"] = !tool.Expanded
		props["preview"] = "none"
	}
	return tern.WireNode{ID: id, K: tern.KindTool, P: props, C: children}
}

// ompBashToolNode builds omp's inline `bash` tool node with its `omp.run` box
// (tools/bash.ts:972-1077, tools/native-view.ts:241-303).
func (s *session) ompBashToolNode(id string, tool fe.ToolCard, props map[string]any, command, intent string, live bool, elapsedMs int64, hasElapsed bool) tern.WireNode {
	s.dropViewRules(id)
	props["frame"] = "inline"
	if intent != "" {
		props["target"] = intent
		props["targetKind"] = "text"
	} else {
		delete(props, "target")
		delete(props, "targetKind")
	}
	if command != "" {
		props["command"] = command
	}
	delete(props, "lang")

	var runChildren []tern.WireNode
	if command != "" {
		runChildren = append(runChildren, tern.WireNode{
			ID: id + ".run.input",
			K:  tern.KindCode,
			P: map[string]any{
				"role":    "omp.tool.bash.command",
				"lang":    "bash",
				"wrap":    true,
				"preview": map[string]any{"lines": 6},
				"text":    strings.TrimRight(command, " \t\n"),
			},
		})
	}
	out := cleanOmpBashOutput(tool.Output)
	if !blank(out) {
		runChildren = append(runChildren, tern.WireNode{
			ID: id + ".run.output",
			K:  tern.KindAnsi,
			P: map[string]any{
				"role":    "omp.tool.bash.output",
				"follow":  true,
				"preview": map[string]any{"lines": structuredPreviewLines},
				"text":    out,
			},
		})
	}
	if tool.Status != fe.ToolPending {
		stateWord, stateTone := ompRunState(tool.Status)
		footChildren := []tern.WireNode{
			{
				ID: id + ".run.foot.state",
				K:  tern.KindText,
				P: map[string]any{
					"role":  "omp.run.state",
					"tone":  stateTone,
					"spans": []any{map[string]any{"t": stateWord}},
				},
			},
		}
		if hasElapsed {
			timeProps := map[string]any{
				"role":   "omp.run.time",
				"age":    elapsedMs,
				"format": "short",
			}
			if !live {
				timeProps["stopped"] = elapsedMs
			}
			footChildren = append(footChildren, tern.WireNode{
				ID: id + ".run.foot.time",
				K:  tern.KindElapsed,
				P:  timeProps,
			})
		}
		runChildren = append(runChildren, tern.WireNode{
			ID: id + ".run.foot",
			K:  tern.KindRow,
			P:  map[string]any{"role": "omp.run.foot", "gap": "sm", "align": "center"},
			C:  footChildren,
		})
	}

	runProps := map[string]any{"role": "omp.run", "gap": "none"}
	if tool.Status == fe.ToolError {
		runProps["tone"] = "error"
	}
	children := []tern.WireNode{
		{ID: id + ".run", K: tern.KindCol, P: runProps, C: runChildren},
	}
	children = s.appendToolImages(id, tool, children)
	props["collapsible"] = true
	props["collapsed"] = !tool.Expanded
	props["preview"] = "children"
	return tern.WireNode{ID: id, K: tern.KindTool, P: props, C: children}
}

func cleanOmpBashOutput(output string) string {
	lines := strings.Split(strings.TrimRight(output, "\r\n"), "\n")
	for len(lines) > 0 && blank(lines[0]) {
		lines = lines[1:]
	}
	for len(lines) > 0 {
		plain := strings.TrimSpace(stripANSI(lines[len(lines)-1]))
		if plain == "" || isBashTimingTrailer(plain) {
			lines = lines[:len(lines)-1]
			continue
		}
		break
	}
	return strings.Join(lines, "\n")
}

func isBashTimingTrailer(plain string) bool {
	for _, prefix := range []string{"Took ", "Elapsed "} {
		if rest, ok := strings.CutPrefix(plain, prefix); ok && strings.HasSuffix(rest, "s") {
			return true
		}
	}
	return false
}

// ompRunState mirrors RUN_STATE in packages/tui/src/tools/native-view.ts:214-221.
func ompRunState(status fe.ToolStatus) (word, tone string) {
	switch status {
	case fe.ToolRunning:
		return "Running", "pending"
	case fe.ToolDone:
		return "Done", "success"
	case fe.ToolError:
		return "Failed", "error"
	case fe.ToolCancelled:
		return "Cancelled", "muted"
	default:
		return "Pending", "muted"
	}
}

// appendToolImages appends tool result images while guarding against blobs
// larger than tern.MaxBlob (fixes B1).
func (s *session) appendToolImages(id string, tool fe.ToolCard, children []tern.WireNode) []tern.WireNode {
	for i := range tool.Images {
		img := &tool.Images[i]
		if len(img.Data) == 0 || len(img.Data) > tern.MaxBlob {
			continue
		}
		children = append(children, tern.WireNode{
			ID: id + ".img." + strconv.Itoa(i),
			K:  tern.KindImage,
			P:  map[string]any{"blob": s.blob(img)},
		})
	}
	return children
}

// firstChangedDiffLine walks unified diff hunks to find the first actual
// added (`+`) or deleted (`-`) new-side line number (fixes A1/N3; upstream:
// packages/tui/src/tools/edit.ts:1429, 1501-1503).
// Tracks remaining old/new hunk line counts from each `@@ -a[,b] +c[,d] @@`
// header so changed lines whose content starts with `--` or `++` (appearing as
// `---` or `+++` in unified diff) are treated as real changes, not file headers.
func firstChangedDiffLine(diff *fe.Diff) int {
	if diff == nil || diff.Text == "" {
		return 0
	}
	oldRem, newRem := 0, 0
	newLine := 0
	for line := range strings.SplitSeq(diff.Text, "\n") {
		if oldRem == 0 && newRem == 0 {
			if strings.HasPrefix(line, "@@ ") {
				oCount, nStart, nCount, ok := parseUnifiedHunkHeader(line)
				if ok && nStart > 0 {
					oldRem, newRem = oCount, nCount
					newLine = nStart
				}
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "+"):
			return newLine
		case strings.HasPrefix(line, "-"):
			return newLine
		case strings.HasPrefix(line, " "):
			newLine++
			if oldRem > 0 {
				oldRem--
			}
			if newRem > 0 {
				newRem--
			}
		case strings.HasPrefix(line, `\`):
			// "\ No newline at end of file" does not consume hunk line counts.
		default:
			oldRem, newRem = 0, 0
		}
	}
	return 0
}

// parseUnifiedHunkHeader parses `@@ -oldStart[,oldCount] +newStart[,newCount] @@`.
func parseUnifiedHunkHeader(line string) (oldCount, newStart, newCount int, ok bool) {
	rest, found := strings.CutPrefix(line, "@@ -")
	if !found {
		return 0, 0, 0, false
	}
	oldPart, afterOld, found := strings.Cut(rest, " +")
	if !found {
		return 0, 0, 0, false
	}
	newPart, _, found := strings.Cut(afterOld, " @@")
	if !found {
		newPart, _, _ = strings.Cut(afterOld, " ")
	}
	_, oldCount, ok = parseHunkRange(oldPart)
	if !ok {
		return 0, 0, 0, false
	}
	newStart, newCount, ok = parseHunkRange(newPart)
	return oldCount, newStart, newCount, ok
}

func parseHunkRange(part string) (start, count int, ok bool) {
	sStr, cStr, hasComma := strings.Cut(part, ",")
	s, err := strconv.Atoi(sStr)
	if err != nil || s < 0 {
		return 0, 0, false
	}
	if !hasComma {
		return s, 1, true
	}
	c, err := strconv.Atoi(cStr)
	if err != nil || c < 0 {
		return 0, 0, false
	}
	return s, c, true
}
