package websearch

import (
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// rsc-extract.ts: readable content out of a Next.js React Server Components flight payload. A page
// that renders on the server embeds its content as JSON in <script>self.__next_f.push([1,"..."])
// </script> tags; extract.ts tries this when Readability finds nothing or too little.

type rscResult struct {
	Title   string
	Content string
}

var (
	rscScriptRE = regexp.MustCompile(`(?s)<script>self\.__next_f\.push\(\[1,"(.*?)"\]\)</script>`)
	rscTitleRE  = regexp.MustCompile(`<title[^>]*>([^<]+)</title>`)
	rscRefRE    = regexp.MustCompile(`^\$L([0-9a-fA-F]+)$`)
	rscHexRE    = regexp.MustCompile(`^[0-9a-fA-F]+$`)
	rscUpperRE  = regexp.MustCompile(`^\$[A-Z]`)
	rscBlankRE  = regexp.MustCompile(`\n{3,}`)

	rscSkipTags = map[string]bool{"script": true, "style": true, "svg": true, "path": true, "circle": true, "link": true, "meta": true,
		"template": true, "button": true, "input": true, "nav": true, "footer": true, "aside": true}
)

// rscString is String(v) for a value decoded from JSON.
func rscString(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return jsNumberString(x)
	case string:
		return x
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			if e != nil {
				parts[i] = rscString(e)
			}
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}

type rscCtx struct{ inTable, inCode bool }

type rscExtractor struct {
	chunks      map[string]string
	order       []string
	parsed      map[string]any
	parsedKnown map[string]bool
	visited     map[string]bool
}

// extractRSCContent is extractRSCContent: nil when the page has no payload or no usable content.
func extractRSCContent(page string) *rscResult {
	if !strings.Contains(page, "self.__next_f.push") {
		return nil
	}
	x := &rscExtractor{chunks: map[string]string{}, parsed: map[string]any{}, parsedKnown: map[string]bool{}, visited: map[string]bool{}}

	// Parse all RSC chunks into a map: each script holds lines of "id:payload"; chunk ids are short hex strings.
	for _, m := range rscScriptRE.FindAllStringSubmatch(page, -1) {
		var content string
		if err := json.Unmarshal([]byte(`"`+m[1]+`"`), &content); err != nil {
			continue
		}
		for _, line := range strings.Split(content, "\n") {
			if jsTrim(line) == "" {
				continue
			}
			colon := strings.Index(line, ":")
			if colon <= 0 || colon > 4 {
				continue
			}
			id := line[:colon]
			if !rscHexRE.MatchString(id) {
				continue
			}
			payload := line[colon+1:]
			if payload == "" {
				continue
			}
			existing, ok := x.chunks[id]
			if !ok {
				x.order = append(x.order, id)
			}
			if !ok || jsLen(payload) > jsLen(existing) {
				x.chunks[id] = payload
			}
		}
	}
	if len(x.chunks) == 0 {
		return nil
	}

	title := ""
	if m := rscTitleRE.FindStringSubmatch(page); m != nil {
		title = jsTrim(strings.SplitN(m[1], "|", 2)[0])
	}

	// Process the main content chunk (usually "23").
	if main := x.chunk("23"); main != nil {
		content := x.node(main, rscCtx{})
		if jsLen(jsTrim(content)) > 100 {
			return &rscResult{Title: title, Content: jsTrim(rscBlankRE.ReplaceAllString(content, "\n\n"))}
		}
	}

	// Fallback: try the other chunks.
	type part struct {
		order int
		text  string
	}
	var parts []part
	for _, id := range x.order {
		if id == "23" {
			continue
		}
		parsed := x.chunk(id)
		if parsed == nil {
			continue
		}
		x.visited = map[string]bool{}
		text := x.node(parsed, rscCtx{})
		if jsLen(jsTrim(text)) > 50 && !strings.Contains(text, "page was not found") && !strings.Contains(text, "404") {
			n, _ := strconv.ParseInt(id, 16, 64)
			parts = append(parts, part{int(n), jsTrim(text)})
		}
	}
	if len(parts) == 0 {
		return nil
	}
	sort.SliceStable(parts, func(i, j int) bool { return parts[i].order < parts[j].order })
	seen := map[string]bool{}
	var unique []string
	for _, p := range parts {
		key := jsSlice(p.text, 0, 150)
		if !seen[key] {
			seen[key] = true
			unique = append(unique, p.text)
		}
	}
	content := jsTrim(rscBlankRE.ReplaceAllString(strings.Join(unique, "\n\n"), "\n\n"))
	if jsLen(content) > 100 {
		return &rscResult{Title: title, Content: content}
	}
	return nil
}

// chunk is getParsedChunk: the chunk's payload as JSON when it is an array, else nil.
func (x *rscExtractor) chunk(id string) any {
	if x.parsedKnown[id] {
		return x.parsed[id]
	}
	x.parsedKnown[id] = true
	payload, ok := x.chunks[id]
	if !ok || !strings.HasPrefix(payload, "[") {
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(payload), &v); err != nil {
		return nil
	}
	x.parsed[id] = v
	return v
}

// ref resolves "$L<id>" through the chunk map without revisiting a chunk that is being expanded.
func (x *rscExtractor) withRef(id string, fn func(chunk any) string) string {
	x.visited[id] = true
	defer delete(x.visited, id)
	return fn(x.chunk(id))
}

func rscProps(node []any) map[string]any {
	if len(node) > 3 {
		if m, ok := node[3].(map[string]any); ok {
			return m
		}
	}
	return map[string]any{}
}

// node is extractNode.
func (x *rscExtractor) node(n any, ctx rscCtx) string {
	switch v := n.(type) {
	case nil:
		return ""
	case string:
		// Check if it's a reference like "$L30"
		if m := rscRefMatch(v); m != "" {
			if x.visited[m] {
				return "" // Prevent cycles
			}
			return x.withRef(m, func(c any) string {
				if c == nil {
					return ""
				}
				return x.node(c, ctx)
			})
		}
		// Filter out RSC-specific artifacts, but preserve content inside code blocks.
		if !ctx.inCode && (v == "$undefined" || v == "$" || rscUpperRE.MatchString(v)) {
			return ""
		}
		if jsTrim(v) != "" {
			return v
		}
		return ""
	case float64:
		return jsNumberString(v)
	case bool:
		return ""
	case []any:
		if len(v) > 1 && v[0] == "$" {
			if tag, ok := v[1].(string); ok {
				return x.element(v, tag, ctx)
			}
		}
		// Array of child nodes
		var b strings.Builder
		for _, c := range v {
			b.WriteString(x.node(c, ctx))
		}
		return b.String()
	}
	return ""
}

func rscRefMatch(s string) string {
	if m := rscRefRE.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

// element is the element branch of extractNode: ["$", "tag", key, props].
func (x *rscExtractor) element(node []any, tag string, ctx rscCtx) string {
	props := rscProps(node)
	// Skip non-content
	if rscSkipTags[tag] {
		return ""
	}
	// Component ref like $L25
	if strings.HasPrefix(tag, "$L") {
		refID := tag[2:]
		if x.visited[refID] {
			return ""
		}
		// Check for heading components with baseId
		if jsTruthy(props["baseId"]) && jsTruthy(props["children"]) {
			return "## " + rscString(props["children"]) + "\n\n"
		}
		return x.withRef(refID, func(c any) string {
			switch {
			case c != nil:
				return x.node(c, ctx)
			case jsTruthy(props["children"]):
				return x.node(props["children"], ctx)
			}
			return ""
		})
	}

	children := props["children"]
	content := ""
	if jsTruthy(children) {
		content = x.node(children, ctx)
	}
	inner := func(c rscCtx) string {
		if jsTruthy(children) {
			return x.node(children, c)
		}
		return ""
	}

	switch tag {
	case "h1":
		return "# " + jsTrim(content) + "\n\n"
	case "h2":
		return "## " + jsTrim(content) + "\n\n"
	case "h3":
		return "### " + jsTrim(content) + "\n\n"
	case "h4":
		return "#### " + jsTrim(content) + "\n\n"
	case "h5":
		return "##### " + jsTrim(content) + "\n\n"
	case "h6":
		return "###### " + jsTrim(content) + "\n\n"
	case "p":
		if ctx.inTable {
			return content
		}
		return jsTrim(content) + "\n\n"
	case "code":
		code := inner(rscCtx{inTable: ctx.inTable, inCode: true})
		if ctx.inCode {
			return code
		}
		return "`" + code + "`"
	case "pre":
		return "```\n" + inner(rscCtx{inTable: ctx.inTable, inCode: true}) + "\n```\n\n"
	case "strong", "b":
		return "**" + content + "**"
	case "em", "i":
		return "*" + content + "*"
	case "li":
		return "- " + jsTrim(content) + "\n"
	case "ul", "ol":
		return content + "\n"
	case "blockquote":
		return "> " + jsTrim(content) + "\n\n"
	case "table":
		return x.table(node) + "\n"
	case "thead", "tbody", "tr", "th", "td":
		return content
	case "div":
		if props["role"] == "alert" || props["data-slot"] == "alert" {
			return "> " + jsTrim(content) + "\n\n"
		}
		return content
	case "a":
		if href, ok := props["href"].(string); ok && href != "" && !strings.HasPrefix(href, "#") {
			return "[" + content + "](" + href + ")"
		}
		return content
	}
	return content
}

// table is extractTable: the rows of a table node as a Markdown table.
func (x *rscExtractor) table(tableNode []any) string {
	props := rscProps(tableNode)
	var rows [][]string
	headerRowCount := 0

	var walkTable func(n any, isHeader bool)
	var walkCells func(n any, cells *[]string)

	refChunk := func(s string) (string, bool) {
		id := rscRefMatch(s)
		return id, id != "" && !x.visited[id]
	}

	walkTable = func(n any, isHeader bool) {
		switch v := n.(type) {
		case nil:
			return
		case string:
			if id, ok := refChunk(v); ok {
				x.withRef(id, func(c any) string {
					if c != nil {
						walkTable(c, isHeader)
					}
					return ""
				})
			}
			return
		case []any:
			if len(v) > 0 && v[0] == "$" {
				tag, _ := v[1].(string)
				nodeProps := rscProps(v)
				// Handle component refs
				if strings.HasPrefix(tag, "$L") {
					refID := tag[2:]
					if !x.visited[refID] {
						x.withRef(refID, func(c any) string {
							if c != nil {
								walkTable(c, isHeader)
							}
							return ""
						})
					}
					return
				}
				switch tag {
				case "thead":
					walkTable(nodeProps["children"], true)
				case "tbody":
					walkTable(nodeProps["children"], false)
				case "tr":
					var cells []string
					walkCells(nodeProps["children"], &cells)
					if len(cells) > 0 {
						rows = append(rows, cells)
						if isHeader {
							headerRowCount++
						}
					}
				default:
					walkTable(nodeProps["children"], isHeader)
				}
				return
			}
			for _, c := range v {
				walkTable(c, isHeader)
			}
		}
	}

	walkCells = func(n any, cells *[]string) {
		switch v := n.(type) {
		case nil:
			return
		case string:
			if id, ok := refChunk(v); ok {
				x.withRef(id, func(c any) string {
					if c != nil {
						walkCells(c, cells)
					}
					return ""
				})
			}
			return
		case []any:
			if len(v) > 0 && v[0] == "$" && len(v) > 1 && (v[1] == "td" || v[1] == "th") {
				cellProps := rscProps(v)
				text := jsTrim(x.node(cellProps["children"], rscCtx{inTable: true}))
				text = strings.ReplaceAll(text, "\n", " ")
				text = strings.ReplaceAll(text, `\`, `\\`) // Escape backslashes first
				text = strings.ReplaceAll(text, "|", `\|`) // Then escape pipes
				*cells = append(*cells, text)
			} else if tag, ok := rscComponentTag(v); ok {
				// Component ref for a cell
				refID := tag[2:]
				if !x.visited[refID] {
					x.withRef(refID, func(c any) string {
						if c != nil {
							walkCells(c, cells)
						}
						return ""
					})
				}
			} else {
				for _, c := range v {
					walkCells(c, cells)
				}
			}
		}
	}

	walkTable(props["children"], false)
	if len(rows) == 0 {
		return ""
	}
	colCount := 0
	for _, r := range rows {
		colCount = max(colCount, len(r))
	}
	var md strings.Builder
	for i, row := range rows {
		cells := append(append([]string{}, row...), make([]string, colCount-len(row))...)
		md.WriteString("| " + strings.Join(cells, " | ") + " |\n")
		if i == headerRowCount-1 || (headerRowCount == 0 && i == 0) {
			md.WriteString("| " + strings.TrimSuffix(strings.Repeat("--- | ", colCount), " | ") + " |\n")
		}
	}
	return md.String()
}

// rscComponentTag reports an element whose tag is a component ref ("$L25").
func rscComponentTag(node []any) (string, bool) {
	if len(node) > 1 && node[0] == "$" {
		if tag, ok := node[1].(string); ok && strings.HasPrefix(tag, "$L") {
			return tag, true
		}
	}
	return "", false
}
