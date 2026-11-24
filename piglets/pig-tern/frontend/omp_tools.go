package frontend

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/stencil-hq/tern-sdk/go/tern"
	"github.com/stencil-hq/tern-sdk/go/tern/ui"
)

// ompEditSection holds one file section parsed from an omp hashline `edit`
// payload (`[PATH#TAG]` followed by PUT/CUT/REM/MV lines).
// upstream: packages/tui/src/tools/edit.ts:665-722
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
	// Walk from the first colon after any Windows drive prefix (`C:`).
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
	// Tail range `:-N`
	if strings.HasPrefix(part, "-") {
		return isDigits(part[1:])
	}
	// Strip optional leading L (`L10-L20`).
	s := strings.TrimPrefix(strings.TrimPrefix(part, "L"), "l")
	// Read leading digits.
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

// ompToolHead describes the derived head props for an omp tool call.
type ompToolHead struct {
	title      string
	target     string
	targetKind string
	hrefPath   string
	command    string
	meta       []any
	inline     bool
}

// deriveOmpToolHead maps omp's tool names and argument shapes to omp's native
// tool head fields, following packages/tui/src/tools/*.ts.
func (s *session) deriveOmpToolHead(tool fe.ToolCard) ompToolHead {
	title := tool.Name
	if title != "" {
		title = strings.ToUpper(title[:1]) + title[1:]
	}
	args := tool.Arguments
	switch tool.Name {
	case "read":
		raw := firstStringArg(args, "path", "file_path")
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
		h := ompToolHead{title: "Edit"}
		if input, ok := args["input"].(string); ok && input != "" {
			sections := parseOmpEditSections(input)
			if len(sections) > 1 {
				h.target = fmt.Sprintf("%d files", len(sections))
				h.targetKind = "text"
				return h
			}
			if len(sections) == 1 {
				sec := sections[0]
				if sec.op == "delete" {
					h.title = "Delete"
				}
				h.target = sec.path
				h.targetKind = "path"
				h.hrefPath = sec.path
				if sec.rename != "" && sec.rename != sec.path {
					h.meta = append(h.meta, "→ "+sec.rename)
				}
				return h
			}
		}
		if p := firstStringArg(args, "path", "file_path"); p != "" {
			h.target = p
			h.targetKind = "path"
			h.hrefPath = p
			return h
		}
		if tool.Diff != nil && tool.Diff.Path != "" {
			h.target = tool.Diff.Path
			h.targetKind = "path"
			h.hrefPath = tool.Diff.Path
		}
		return h

	case "write":
		p := firstStringArg(args, "path", "file_path")
		h := ompToolHead{
			title:      "Write",
			target:     p,
			targetKind: "path",
			hrefPath:   p,
		}
		if content, ok := args["content"].(string); ok && content != "" {
			n := strings.Count(content, "\n") + 1
			if strings.HasSuffix(content, "\n") {
				n--
			}
			if n > 0 {
				label := "lines"
				if n == 1 {
					label = "line"
				}
				h.meta = []any{fmt.Sprintf("%d %s", n, label)}
			}
		}
		return h

	case "bash":
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
		query, _ := args["query"].(string)
		var meta []any
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
		return ompToolHead{
			title:      "Todo",
			target:     formatOmpTodoTarget(args),
			targetKind: "text",
		}

	case "ask":
		target, meta := formatOmpAskTarget(args)
		return ompToolHead{
			title:      "Ask",
			target:     target,
			targetKind: "text",
			meta:       meta,
			inline:     true,
		}

	case "web_search":
		query, _ := args["query"].(string)
		return ompToolHead{
			title:      "Web search",
			target:     query,
			targetKind: "query",
			inline:     true,
		}

	default:
		target, kind := ompToolTarget(tool)
		h := ompToolHead{
			title:      title,
			target:     target,
			targetKind: kind,
		}
		if kind == "path" {
			h.hrefPath = target
		}
		if kind == "command" {
			h.command = target
		}
		return h
	}
}

func firstStringArg(args map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := args[k].(string); ok && v != "" {
			return v
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

func formatOmpTodoTarget(args map[string]any) string {
	if len(args) == 0 {
		return "update"
	}
	var parts []string
	if op, ok := args["op"].(string); ok && op != "" {
		parts = append(parts, op)
	} else {
		parts = append(parts, "update")
	}
	if task, ok := args["task"].(string); ok && task != "" {
		parts = append(parts, task)
	}
	if phase, ok := args["phase"].(string); ok && phase != "" {
		parts = append(parts, phase)
	}
	if items, ok := args["items"].([]any); ok && len(items) > 0 {
		noun := "items"
		if len(items) == 1 {
			noun = "item"
		}
		parts = append(parts, fmt.Sprintf("%d %s", len(items), noun))
	} else if list, ok := args["list"].([]any); ok && len(list) > 0 {
		total := 0
		for _, raw := range list {
			if m, ok := raw.(map[string]any); ok {
				if sub, ok := m["items"].([]any); ok {
					total += len(sub)
				}
			}
		}
		if total > 0 {
			noun := "items"
			if total == 1 {
				noun = "item"
			}
			parts = append(parts, fmt.Sprintf("%d %s", total, noun))
		}
	}
	return strings.Join(parts, " ")
}

func formatOmpAskTarget(args map[string]any) (string, []any) {
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

// ompToolNode builds omp's data-first `tool` node (chat/tool-execution.ts:1049-1094,
// verified against /tmp/omp-proof/omp/out/tsp.jsonl).
//
//   - `read` tools draw inline (`frame: "inline"`): on success, no body child
//     (matching omp's read-tool-group.ts recording where file content is not
//     rendered in the card); on error, the first line of the message rides in
//     `meta` (`— <gist>`) with `tone: "error"` and full text only if truncated.
//   - `bash` tools draw inline with omp's run box (`col[omp.run]` holding
//     `code[omp.tool.bash.command]`, `ansi[omp.tool.bash.output]` from plain
//     `ToolCard.Output`, and `row[omp.run.foot]` with `text[omp.run.state]` +
//     `elapsed[omp.run.time]`) and `preview: "children"`.
//   - `edit`, `write`, `grep`, `glob`, `find`, `todo`, `ask`, `web_search`, and
//     other tools derive their head props from omp's argument shapes and carry
//     `role: "omp.tool.<name>"`, `tone: "error"` on failure, and `diff` children
//     when `ToolCard.Diff` is present (inheriting path from edit's section header
//     when `Diff.Path` is empty).
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
	if tool.Status == fe.ToolError {
		props["tone"] = "error"
	}
	if head.target != "" {
		props["target"] = head.target
		props["targetKind"] = head.targetKind
	}
	if head.command != "" {
		props["command"] = head.command
	}
	if head.hrefPath != "" {
		if href := s.pathHref(head.hrefPath); href != "" {
			props["href"] = href
		}
	}
	if len(head.meta) > 0 {
		props["meta"] = head.meta
	}
	intent := ""
	if v, ok := tool.Arguments["i"].(string); ok && strings.TrimSpace(v) != "" {
		intent = strings.TrimSpace(stripANSI(v))
		props["intent"] = intent
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

	// Tool-specific omp shapes matching omp's describeResult / describeCall.
	if tool.Name == "read" && tool.Diff == nil && !(validView(tool.ResultView, tool.Result) && len(tool.Result) > 0) {
		return s.ompReadToolNode(id, tool, props)
	}
	if tool.Name == "bash" && tool.Diff == nil && !(validView(tool.ResultView, tool.Result) && len(tool.Result) > 0) {
		return s.ompBashToolNode(id, tool, props, head.command, intent, live, elapsedMs, hasElapsed)
	}

	var children []tern.WireNode
	var preview any = map[string]any{"lines": structuredPreviewLines}
	switch {
	case tool.Diff != nil:
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
			if href := s.pathHref(diffPath); href != "" {
				props["href"] = href
			}
		}
		diffProps := map[string]any{"text": tool.Diff.Text}
		if diffPath != "" {
			diffProps["path"] = diffPath
		}
		s.dropViewRules(id)
		children = []tern.WireNode{
			{ID: id + ".diff", K: tern.KindDiff, P: diffProps},
		}
	case validView(tool.ResultView, tool.Result) && len(tool.Result) > 0:
		view := id + ".view"
		children = []tern.WireNode{
			wireNode(view, s.viewElement("", view, tool.ResultView, tool.Result, "")),
		}
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
			if rendered := linesText(tool.Result); !blank(rendered) {
				body = rendered
			}
			if body != "" {
				preview = map[string]any{"tail": 5}
				children = []tern.WireNode{
					{ID: id + ".out", K: tern.KindAnsi, P: map[string]any{"text": body}},
				}
			}
		}
	}
	children = s.appendToolImages(id, tool, children)
	hasBody := len(children) > 0
	props["collapsible"] = hasBody
	if hasBody {
		props["collapsed"] = !tool.Expanded
		props["preview"] = preview
	}
	return tern.WireNode{ID: id, K: tern.KindTool, P: props, C: children}
}

// ompReadToolNode builds omp's inline `read` tool node (chat/read-tool-group.ts:623-705,
// verified against /tmp/omp-proof/omp/out/tsp.jsonl):
//   - On success: `tool[omp.tool.read]` with `frame: "inline"`, `collapsible: false`
//     and no file-content child (only attached images if present).
//   - On error: `tone: "error"`, first line in `meta` (`— <gist>`), and an
//     `omp.tool.error` text child only when the message spans multiple lines or
//     was truncated past 120 chars.
func (s *session) ompReadToolNode(id string, tool fe.ToolCard, props map[string]any) tern.WireNode {
	s.dropViewRules(id)
	props["frame"] = "inline"
	var children []tern.WireNode
	if tool.Status == fe.ToolError {
		raw := tool.Output
		if raw == "" {
			if rendered := linesText(tool.Result); !blank(rendered) {
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
		gist := first
		if len(gist) > 120 {
			gist = gist[:119] + "…"
		}
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
// (tools/bash.ts:645-714, tools/native-view.ts:229-291):
//
//	tool(role="omp.tool.bash", frame="inline", preview="children", command=...):
//	  col(id+".run", role="omp.run", gap="none"):
//	    - code(id+".run.input", role="omp.tool.bash.command", lang="bash", wrap=true, preview={lines:6})
//	    - ansi(id+".run.output", role="omp.tool.bash.output", follow=true, preview={lines:10}) [from ToolCard.Output]
//	    - row(id+".run.foot", role="omp.run.foot", gap="sm", align="center"):
//	        - text(id+".run.foot.state", role="omp.run.state", tone=..., spans=[{t: stateWord}])
//	        - elapsed(id+".run.foot.time", role="omp.run.time", age=ms, stopped=ms?, format="short") [when timed]
//
// Output is taken from ToolCard.Output (plain text) rather than PiG's rendered
// Result lines so PiG's blank padding and "Took X.Xs" / "Elapsed X.Xs" rows do
// not leak into the run box. The command child uses kind `code` rather than
// `shell` because Tern's `shell` kind requires a host-compiled
// ShellVizPresentation AST (rows/segments/timeline), which PiG does not build.
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

// cleanOmpBashOutput trims trailing newlines and drops any trailing PiG/omp
// timing/status trailer lines (e.g. "Took 0.1s", "Elapsed 0.1s", "Wall: 0.1s")
// and blank padding around them.
func cleanOmpBashOutput(output string) string {
	lines := strings.Split(strings.TrimRight(output, "\r\n"), "\n")
	// Drop leading blank lines.
	for len(lines) > 0 && blank(lines[0]) {
		lines = lines[1:]
	}
	// Drop trailing blank lines and PiG's shell timing trailer lines.
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

func ompRunState(status fe.ToolStatus) (word, tone string) {
	switch status {
	case fe.ToolRunning:
		return "Running", "pending"
	case fe.ToolDone:
		return "Done", "success"
	case fe.ToolError:
		return "Failed", "error"
	case fe.ToolCancelled:
		return "Cancelled", "warning"
	default:
		return "Pending", "muted"
	}
}

func (s *session) appendToolImages(id string, tool fe.ToolCard, children []tern.WireNode) []tern.WireNode {
	for i := range tool.Images {
		img := &tool.Images[i]
		if len(img.Data) == 0 {
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

// ompToolTarget picks the tool's primary argument and targetKind for tools
// without a dedicated omp head derivation.
func ompToolTarget(tool fe.ToolCard) (target, kind string) {
	for _, candidate := range []struct{ key, kind string }{
		{"command", "command"},
		{"pattern", "pattern"},
		{"query", "query"},
		{"path", "path"},
		{"file_path", "path"},
	} {
		if value, ok := tool.Arguments[candidate.key].(string); ok && value != "" {
			return value, candidate.kind
		}
	}
	return tool.Header, "text"
}
