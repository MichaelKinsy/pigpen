package markit

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Tolerant HTML parser + Turndown GFM Markdown converter faithfully ported from omp.
// upstream: packages/utils/src/turndown/html.ts:1-277
// upstream: packages/utils/src/turndown/service.ts:1-376
// upstream: packages/utils/src/turndown/gfm.ts:1-117
// upstream: packages/coding-agent/src/utils/turndown.ts:1-90

const (
	htmlNodeElement  = 1
	htmlNodeText     = 3
	htmlNodeFragment = 11
)

var htmlVoidElements = map[string]bool{
	"AREA":   true,
	"BASE":   true,
	"BR":     true,
	"COL":    true,
	"EMBED":  true,
	"HR":     true,
	"IMG":    true,
	"INPUT":  true,
	"LINK":   true,
	"META":   true,
	"PARAM":  true,
	"SOURCE": true,
	"TRACK":  true,
	"WBR":    true,
}

var htmlBlockElements = map[string]bool{
	"ADDRESS":    true,
	"ARTICLE":    true,
	"ASIDE":      true,
	"BLOCKQUOTE": true,
	"BODY":       true,
	"CANVAS":     true,
	"CENTER":     true,
	"DD":         true,
	"DETAILS":    true,
	"DIR":        true,
	"DIV":        true,
	"DL":         true,
	"DT":         true,
	"FIELDSET":   true,
	"FIGCAPTION": true,
	"FIGURE":     true,
	"FOOTER":     true,
	"FORM":       true,
	"FRAMESET":   true,
	"H1":         true,
	"H2":         true,
	"H3":         true,
	"H4":         true,
	"H5":         true,
	"H6":         true,
	"HEADER":     true,
	"HGROUP":     true,
	"HR":         true,
	"HTML":       true,
	"ISINDEX":    true,
	"LI":         true,
	"MAIN":       true,
	"MENU":       true,
	"NAV":        true,
	"NOFRAMES":   true,
	"NOSCRIPT":   true,
	"OL":         true,
	"OUTPUT":     true,
	"P":          true,
	"PRE":        true,
	"SECTION":    true,
	"SUMMARY":    true,
	"TABLE":      true,
	"TBODY":      true,
	"TD":         true,
	"TFOOT":      true,
	"TH":         true,
	"THEAD":      true,
	"TR":         true,
	"UL":         true,
}

var htmlNonblankEmptyElements = map[string]bool{
	"A":      true,
	"AUDIO":  true,
	"BR":     true,
	"HR":     true,
	"IFRAME": true,
	"IMG":    true,
	"INPUT":  true,
	"SCRIPT": true,
	"SOURCE": true,
	"TD":     true,
	"TH":     true,
	"VIDEO":  true,
}

var htmlInlineVoidElements = map[string]bool{
	"EMBED": true,
	"IMG":   true,
	"INPUT": true,
}

var htmlNamedEntities = map[string]string{
	"amp":    "&",
	"apos":   "'",
	"copy":   "©",
	"gt":     ">",
	"hellip": "…",
	"laquo":  "«",
	"lt":     "<",
	"mdash":  "—",
	"nbsp":   "\u00a0",
	"ndash":  "–",
	"quot":   `"`,
	"raquo":  "»",
	"reg":    "®",
}

var (
	reHTMLEntity       = regexp.MustCompile(`(?i)&(#(?:x[\da-f]+|\d+)|[a-z][\da-z]+);?`)
	reHTMLAttr         = regexp.MustCompile(`([^\s=/>]+)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>` + "`" + `]+)))?`)
	reHTMLClosingTag   = regexp.MustCompile(`(?i)^</\s*([a-z][\w:-]*)[^>]*>$`)
	reHTMLOpeningTag   = regexp.MustCompile(`(?is)^<\s*([a-z][\w:-]*)([\s\S]*?)/?\s*>$`)
	reHTMLSelfClose    = regexp.MustCompile(`/\s*>$`)
	reScriptBlock      = regexp.MustCompile(`(?is)<script[\s\S]*?</script>`)
	reStyleBlock       = regexp.MustCompile(`(?is)<style[\s\S]*?</style>`)
	reNormalizeCell    = regexp.MustCompile(`(?is)<(td|th)([^>]*)>([\s\S]*?)</(td|th)>`)
	reLeadP            = regexp.MustCompile(`(?i)^\s*<p>`)
	reTrailP           = regexp.MustCompile(`(?i)</p>\s*$`)
	reMidP             = regexp.MustCompile(`(?i)</p>\s*<p>`)
	reNormalizeTable   = regexp.MustCompile(`(?is)<table([^>]*)>\s*(?:<tbody>\s*)?(<tr[\s\S]*?</tr>)([\s\S]*?)</(?:tbody>\s*</)?table>`)
	reTdOpen           = regexp.MustCompile(`(?i)<td`)
	reTdClose          = regexp.MustCompile(`(?i)</td>`)
	reWhitespaceRun    = regexp.MustCompile("[\t\r\n\f ]+")
	reMultiBlank       = regexp.MustCompile(`\n{3,}`)
	reHighlightSource  = regexp.MustCompile(`(?:^|\s)highlight-source-([^\s]+)`)
	reCodeLanguage     = regexp.MustCompile(`(?:^|\s)language-([^\s]+)`)
	reStyleTextAlign   = regexp.MustCompile(`(?i)(?:^|;)\s*text-align\s*:\s*(left|center|right)\s*(?:;|$)`)
	reEscHeadingOrList = regexp.MustCompile(`(?m)^(\s*)(#{1,6}|[+>])(\s)`)
	reEscDash          = regexp.MustCompile(`(?m)^(\s*)-(\s)`)
	reEscOrdered       = regexp.MustCompile(`(?m)^(\s*\d+)\.(\s)`)
	reHeadingPeriod    = regexp.MustCompile(`\\\.`)
	reLinkDestSpecial  = regexp.MustCompile(`[()<>]`)
	reLeadNL           = regexp.MustCompile(`^\n+`)
	reTrailNL          = regexp.MustCompile(`\n+$`)
)

func decodeHTMLEntities(value string) string {
	if !strings.ContainsRune(value, '&') {
		return value
	}
	return reHTMLEntity.ReplaceAllStringFunc(value, func(entity string) string {
		m := reHTMLEntity.FindStringSubmatch(entity)
		if len(m) < 2 {
			return entity
		}
		name := m[1]
		if len(name) > 0 && name[0] == '#' {
			hex := len(name) > 1 && (name[1] == 'x' || name[1] == 'X')
			digits := name[1:]
			base := 10
			if hex {
				digits = name[2:]
				base = 16
			}
			n, err := strconv.ParseInt(digits, base, 64)
			if err != nil || n <= 0 || n > 0x10FFFF {
				return entity
			}
			r := rune(n)
			if !utf8.ValidRune(r) {
				return "\uFFFD"
			}
			return string(r)
		}
		if rep, ok := htmlNamedEntities[strings.ToLower(name)]; ok {
			return rep
		}
		return entity
	})
}

// htmlNode mirrors TurndownNode / HtmlNode from packages/utils/src/turndown/html.ts:55-182.
type htmlNode struct {
	nodeType      int
	nodeName      string
	parent        *htmlNode
	childNodes    []*htmlNode
	elements      []*htmlNode
	index         int
	elementIndex  int
	text          string
	attrs         map[string]string
	inPre         bool
	hasNonWS      bool
	hasNonblankEl bool
}

func (n *htmlNode) appendChild(child *htmlNode) {
	child.parent = n
	child.index = len(n.childNodes)
	n.childNodes = append(n.childNodes, child)
	if child.nodeType == htmlNodeElement {
		child.elementIndex = len(n.elements)
		n.elements = append(n.elements, child)
	}
	if n.inPre || child.nodeName == "PRE" {
		child.inPre = true
	}
}

func (n *htmlNode) nextSibling() *htmlNode {
	if n.parent == nil || n.index+1 >= len(n.parent.childNodes) {
		return nil
	}
	return n.parent.childNodes[n.index+1]
}

func (n *htmlNode) getAttr(name string) (string, bool) {
	if n == nil || n.attrs == nil {
		return "", false
	}
	v, ok := n.attrs[strings.ToLower(name)]
	return v, ok
}

func (n *htmlNode) getAttrOr(name string) string {
	v, _ := n.getAttr(name)
	return v
}

// textContent iteratively collects descendant text (matches HtmlNode.textContent).
func (n *htmlNode) textContent() string {
	if n == nil {
		return ""
	}
	if n.nodeType == htmlNodeText {
		return n.text
	}
	var b strings.Builder
	stack := make([]*htmlNode, 0, len(n.childNodes))
	for i := len(n.childNodes) - 1; i >= 0; i-- {
		stack = append(stack, n.childNodes[i])
	}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur.nodeType == htmlNodeText {
			b.WriteString(cur.text)
			continue
		}
		for i := len(cur.childNodes) - 1; i >= 0; i-- {
			stack = append(stack, cur.childNodes[i])
		}
	}
	return b.String()
}

func parseHTMLAttributes(source string) map[string]string {
	matches := reHTMLAttr.FindAllStringSubmatch(source, -1)
	if len(matches) == 0 {
		return nil
	}
	attrs := make(map[string]string, len(matches))
	for _, m := range matches {
		name := strings.ToLower(m[1])
		if name == "" {
			continue
		}
		val := m[2]
		if val == "" && m[3] != "" {
			val = m[3]
		} else if val == "" && m[4] != "" {
			val = m[4]
		}
		attrs[name] = decodeHTMLEntities(val)
	}
	return attrs
}

// parseHTMLFragment ports packages/utils/src/turndown/html.ts:194-270 with a strict
// element nesting cap (MaxXMLNestingDepth) and periodic ctx cancellation checks.
func parseHTMLFragment(ctx context.Context, src string) (*htmlNode, error) {
	root := &htmlNode{nodeType: htmlNodeFragment, nodeName: "#document-fragment", index: -1, elementIndex: -1}
	stack := []*htmlNode{root}

	cursor := 0
	n := len(src)
	iter := 0

	for cursor < n {
		iter++
		if iter&2047 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}

		var token string
		if src[cursor] != '<' {
			next := strings.IndexByte(src[cursor:], '<')
			end := n
			if next >= 0 {
				end = cursor + next
			}
			token = src[cursor:end]
			cursor = end
		} else {
			nextCh := byte(0)
			if cursor+1 < n {
				nextCh = src[cursor+1]
			}
			if !(nextCh == '/' || nextCh == '!' || (nextCh >= 'a' && nextCh <= 'z') || (nextCh >= 'A' && nextCh <= 'Z')) {
				token = "<"
				cursor++
			} else if strings.HasPrefix(src[cursor:], "<!--") {
				commentEnd := strings.Index(src[cursor+4:], "-->")
				end := n
				if commentEnd >= 0 {
					end = cursor + 4 + commentEnd + 3
				}
				token = src[cursor:end]
				cursor = end
			} else {
				var quote byte
				tagEnd := cursor + 1
				for ; tagEnd < n; tagEnd++ {
					ch := src[tagEnd]
					if quote != 0 {
						if ch == quote {
							quote = 0
						}
					} else if ch == '"' || ch == '\'' {
						quote = ch
					} else if ch == '>' {
						break
					}
				}
				if tagEnd >= n {
					token = "<"
					cursor++
				} else {
					token = src[cursor : tagEnd+1]
					cursor = tagEnd + 1
				}
			}
		}

		parent := stack[len(stack)-1]
		if strings.HasPrefix(token, "<!--") || strings.HasPrefix(token, "<!") {
			continue
		}
		if !strings.HasPrefix(token, "<") {
			txt := decodeHTMLEntities(token)
			parent.appendChild(&htmlNode{nodeType: htmlNodeText, nodeName: "#text", text: txt})
			continue
		}
		if closing := reHTMLClosingTag.FindStringSubmatch(token); len(closing) == 2 {
			name := strings.ToUpper(closing[1])
			for idx := len(stack) - 1; idx > 0; idx-- {
				if stack[idx].nodeName == name {
					stack = stack[:idx]
					break
				}
			}
			continue
		}
		opening := reHTMLOpeningTag.FindStringSubmatch(token)
		if len(opening) < 3 || opening[1] == "" {
			parent.appendChild(&htmlNode{nodeType: htmlNodeText, nodeName: "#text", text: "<"})
			continue
		}
		tagName := strings.ToUpper(opening[1])
		elem := &htmlNode{
			nodeType:     htmlNodeElement,
			nodeName:     tagName,
			attrs:        parseHTMLAttributes(opening[2]),
			index:        -1,
			elementIndex: -1,
		}
		parent.appendChild(elem)
		if !htmlVoidElements[tagName] && !reHTMLSelfClose.MatchString(token) {
			if len(stack) >= MaxXMLNestingDepth {
				return nil, fmt.Errorf("HTML nesting depth exceeds limit (%d)", MaxXMLNestingDepth)
			}
			stack = append(stack, elem)
		}
	}

	computeHTMLBlankFlags(root)
	return root, nil
}

func computeHTMLBlankFlags(root *htmlNode) {
	type frame struct {
		node    *htmlNode
		visited bool
	}
	stack := []frame{{node: root}}
	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		n := top.node
		if !top.visited {
			top.visited = true
			for i := len(n.childNodes) - 1; i >= 0; i-- {
				stack = append(stack, frame{node: n.childNodes[i]})
			}
			continue
		}
		stack = stack[:len(stack)-1]
		if n.nodeType == htmlNodeText {
			n.hasNonWS = hasNonSpace(n.text)
			continue
		}
		nonWS := false
		nonBlankEl := false
		for _, c := range n.childNodes {
			if c.hasNonWS {
				nonWS = true
			}
			if c.nodeType == htmlNodeElement && (htmlNonblankEmptyElements[c.nodeName] || c.hasNonblankEl) {
				nonBlankEl = true
			}
		}
		n.hasNonWS = nonWS
		n.hasNonblankEl = nonBlankEl
	}
}

func hasNonSpace(s string) bool {
	for _, r := range s {
		if !unicode.IsSpace(r) {
			return true
		}
	}
	return false
}

// normalizeTablesHtml ports packages/coding-agent/src/utils/turndown.ts:74-90.
func normalizeTablesHtml(rawHTML string) string {
	result := reNormalizeCell.ReplaceAllStringFunc(rawHTML, func(match string) string {
		parts := reNormalizeCell.FindStringSubmatch(match)
		if len(parts) < 5 {
			return match
		}
		tag, attrs, inner, closeTag := parts[1], parts[2], parts[3], parts[4]
		stripped := reLeadP.ReplaceAllString(inner, "")
		stripped = reTrailP.ReplaceAllString(stripped, "")
		stripped = reMidP.ReplaceAllString(stripped, " ")
		return "<" + tag + attrs + ">" + stripped + "</" + closeTag + ">"
	})
	result = reNormalizeTable.ReplaceAllStringFunc(result, func(match string) string {
		parts := reNormalizeTable.FindStringSubmatch(match)
		if len(parts) < 4 {
			return match
		}
		attrs, firstRow, rest := parts[1], parts[2], parts[3]
		theadRow := reTdOpen.ReplaceAllString(firstRow, "<th")
		theadRow = reTdClose.ReplaceAllString(theadRow, "</th>")
		return "<table" + attrs + "><thead>" + theadRow + "</thead><tbody>" + rest + "</tbody></table>"
	})
	return result
}

// htmlToMarkdown runs omp's configured Turndown pipeline (createTurndown + normalizeTablesHtml).
// upstream: packages/coding-agent/src/utils/turndown.ts:19-90
func htmlToMarkdown(ctx context.Context, rawHTML string) (string, error) {
	cleaned := reScriptBlock.ReplaceAllString(rawHTML, "")
	cleaned = reStyleBlock.ReplaceAllString(cleaned, "")
	normalized := normalizeTablesHtml(cleaned)

	root, err := parseHTMLFragment(ctx, normalized)
	if err != nil {
		return "", err
	}
	td := &turndownRunner{ctx: ctx}
	md, err := td.convertChildren(root)
	if err != nil {
		return "", err
	}
	md = strings.TrimLeft(md, "\t\r\n")
	md = strings.TrimRight(md, "\t\r\n ")
	md = reMultiBlank.ReplaceAllString(md, "\n\n")
	return md, nil
}

// turndownRunner implements omp's TurndownService with options:
// headingStyle: "atx", codeBlockStyle: "fenced", bulletListMarker: "-",
// hr: "* * *", fence: "```", emDelimiter: "_", strongDelimiter: "**",
// plus GFM rules and omp's custom heading/strikethrough/listItem/escape overrides.
// upstream: packages/utils/src/turndown/service.ts:154-376
// upstream: packages/utils/src/turndown/gfm.ts:1-117
// upstream: packages/coding-agent/src/utils/turndown.ts:19-67
type turndownRunner struct {
	ctx               context.Context
	prevSrcWhitespace bool
	stepCount         int
}

// escapeMarkdown implements TurndownService.escape + omp's intraword underscore unescaping.
// upstream: packages/utils/src/turndown/service.ts:206-213
// upstream: packages/coding-agent/src/utils/turndown.ts:10,28-30
func escapeMarkdown(text string) string {
	if text == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(text))
	for i := range len(text) {
		ch := text[i]
		switch ch {
		case '\\', '*', '_', '[', ']':
			b.WriteByte('\\')
			b.WriteByte(ch)
		default:
			b.WriteByte(ch)
		}
	}
	s := b.String()
	s = reEscHeadingOrList.ReplaceAllString(s, `$1\$2$3`)
	s = reEscDash.ReplaceAllString(s, `$1\-$2`)
	s = reEscOrdered.ReplaceAllString(s, `$1\.$2`)
	return unescapeIntrawordUnderscores(s)
}

// unescapeIntrawordUnderscores replaces `(?<=[\p{L}\p{N}])(?:\\_)+(?=[\p{L}\p{N}])` with `_`.
// upstream: packages/coding-agent/src/utils/turndown.ts:10,30
func unescapeIntrawordUnderscores(s string) string {
	if !strings.Contains(s, `\_`) {
		return s
	}
	runes := []rune(s)
	n := len(runes)
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < n {
		if i > 0 && isLetterOrNumber(runes[i-1]) && i+1 < n && runes[i] == '\\' && runes[i+1] == '_' {
			j := i
			count := 0
			for j+1 < n && runes[j] == '\\' && runes[j+1] == '_' {
				count++
				j += 2
			}
			if j < n && isLetterOrNumber(runes[j]) {
				for range count {
					b.WriteByte('_')
				}
				i = j
				continue
			}
		}
		b.WriteRune(runes[i])
		i++
	}
	return b.String()
}

func isLetterOrNumber(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r)
}

func (t *turndownRunner) convertChildren(node *htmlNode) (string, error) {
	var b strings.Builder
	for _, child := range node.childNodes {
		s, err := t.convertNode(child)
		if err != nil {
			return "", err
		}
		b.WriteString(s)
	}
	return b.String(), nil
}

func (t *turndownRunner) convertNode(node *htmlNode) (string, error) {
	t.stepCount++
	if t.stepCount&1023 == 0 {
		if err := t.ctx.Err(); err != nil {
			return "", err
		}
	}

	if node.nodeType == htmlNodeText {
		if node.inPre {
			return node.text, nil
		}
		collapsed := reWhitespaceRun.ReplaceAllString(node.text, " ")
		if t.prevSrcWhitespace && strings.HasPrefix(collapsed, " ") {
			collapsed = collapsed[1:]
		}
		if collapsed != "" {
			t.prevSrcWhitespace = strings.HasSuffix(collapsed, " ")
		}
		if node.parent != nil && node.parent.nodeName == "CODE" {
			return collapsed, nil
		}
		return escapeMarkdown(collapsed), nil
	}
	if node.nodeType != htmlNodeElement {
		return t.convertChildren(node)
	}

	name := node.nodeName
	block := htmlBlockElements[name]
	if block {
		t.prevSrcWhitespace = true
	}

	if !node.hasNonWS && !htmlNonblankEmptyElements[name] && !node.hasNonblankEl {
		if block {
			return "\n\n", nil
		}
		return t.convertChildren(node)
	}

	if isTaskListCheckbox(node) {
		if block {
			t.prevSrcWhitespace = true
		}
		if htmlInlineVoidElements[name] {
			t.prevSrcWhitespace = false
		}
		if _, has := node.getAttr("checked"); has {
			return "[x] ", nil
		}
		return "[ ] ", nil
	}
	if isHeadingTable(node) {
		if block {
			t.prevSrcWhitespace = true
		}
		return t.renderGFMTable(node)
	}
	if name == "DIV" && reHighlightSource.MatchString(node.getAttrOr("class")) {
		if block {
			t.prevSrcWhitespace = true
		}
		lang := ""
		if m := reHighlightSource.FindStringSubmatch(node.getAttrOr("class")); len(m) == 2 {
			lang = m[1]
		}
		text := ""
		if pres := htmlDescendantElements(node, "PRE", 1); len(pres) > 0 {
			text = pres[0].textContent()
		}
		return "\n\n```" + lang + "\n" + text + "\n```\n\n", nil
	}

	content, err := t.convertChildren(node)
	if err != nil {
		return "", err
	}
	if block {
		t.prevSrcWhitespace = true
	}
	if name == "CODE" || htmlInlineVoidElements[name] {
		t.prevSrcWhitespace = false
	}

	switch name {
	case "LI":
		return renderListItem(content, node), nil
	case "H1", "H2", "H3", "H4", "H5", "H6":
		level := int(name[1] - '0')
		cleaned := strings.TrimSpace(reHeadingPeriod.ReplaceAllString(content, "."))
		return "\n\n" + strings.Repeat("#", level) + " " + cleaned + "\n\n", nil
	case "DEL", "S", "STRIKE":
		return "~~" + content + "~~", nil
	}

	return t.defaultReplacement(content, node), nil
}

func renderListItem(content string, node *htmlNode) string {
	// upstream: packages/coding-agent/src/utils/turndown.ts:50-61
	body := reLeadNL.ReplaceAllString(content, "")
	body = reTrailNL.ReplaceAllString(body, "\n")
	body = strings.ReplaceAll(body, "\n", "\n  ")
	prefix := "- "
	if node.parent != nil && node.parent.nodeName == "OL" {
		start := 1
		if rawStart := node.parent.getAttrOr("start"); rawStart != "" {
			if n, err := strconv.Atoi(strings.TrimSpace(rawStart)); err == nil {
				start = n
			}
		}
		idx := node.elementIndex
		if idx < 0 {
			idx = 0
		}
		prefix = strconv.Itoa(start+idx) + ". "
	}
	trail := ""
	if node.nextSibling() != nil {
		trail = "\n"
	}
	return prefix + body + trail
}

func (t *turndownRunner) defaultReplacement(content string, node *htmlNode) string {
	// upstream: packages/utils/src/turndown/service.ts:281-322
	name := node.nodeName
	switch name {
	case "P":
		trimmed := strings.TrimSpace(content)
		if trimmed == "" {
			return ""
		}
		return "\n\n" + trimmed + "\n\n"
	case "BR":
		return "  \n"
	case "HR":
		return "\n\n* * *\n\n"
	case "BLOCKQUOTE":
		body := strings.TrimSpace(content)
		if body == "" {
			return ""
		}
		body = reMultiBlank.ReplaceAllString(body, "\n\n")
		lines := strings.Split(body, "\n")
		for i, l := range lines {
			lines[i] = "> " + l
		}
		return "\n\n" + strings.Join(lines, "\n") + "\n\n"
	case "UL", "OL":
		body := strings.Trim(content, "\n")
		if node.parent != nil && node.parent.nodeName == "LI" {
			return "\n" + body
		}
		return "\n\n" + body + "\n\n"
	case "PRE":
		for _, c := range node.elements {
			if c.nodeName == "CODE" {
				return replacePreCode(node)
			}
		}
		if trimmed := strings.TrimSpace(content); trimmed != "" {
			return "\n\n" + trimmed + "\n\n"
		}
		return ""
	case "EM", "I":
		return contentWithFlankingWhitespace(content, "_")
	case "STRONG", "B":
		return contentWithFlankingWhitespace(content, "**")
	case "CODE":
		return replaceInlineCode(content)
	case "A":
		return replaceLink(content, node)
	case "IMG":
		return replaceImage(node)
	}
	if htmlBlockElements[name] {
		if trimmed := strings.TrimSpace(content); trimmed != "" {
			return "\n\n" + trimmed + "\n\n"
		}
		return ""
	}
	return content
}

func contentWithFlankingWhitespace(content, delimiter string) string {
	// upstream: packages/utils/src/turndown/service.ts:135-140
	if content == "" {
		return ""
	}
	start := 0
	for start < len(content) {
		r, sz := utf8.DecodeRuneInString(content[start:])
		if !unicode.IsSpace(r) {
			break
		}
		start += sz
	}
	end := len(content)
	for end > start {
		r, sz := utf8.DecodeLastRuneInString(content[:end])
		if !unicode.IsSpace(r) {
			break
		}
		end -= sz
	}
	if start >= end {
		return ""
	}
	return content[:start] + delimiter + content[start:end] + delimiter + content[end:]
}

func replacePreCode(node *htmlNode) string {
	// upstream: packages/utils/src/turndown/service.ts:324-339
	text := node.textContent()
	longest := 3
	run := 0
	for i := range len(text) {
		if text[i] == '`' {
			run++
			if run+1 > longest {
				longest = run + 1
			}
		} else {
			run = 0
		}
	}
	fence := strings.Repeat("`", longest)
	body := strings.TrimSuffix(text, "\n")
	lang := ""
	for _, c := range node.elements {
		if c.nodeName == "CODE" {
			if m := reCodeLanguage.FindStringSubmatch(c.getAttrOr("class")); len(m) == 2 {
				lang = m[1]
			}
			break
		}
	}
	return "\n\n" + fence + lang + "\n" + body + "\n" + fence + "\n\n"
}

func replaceInlineCode(text string) string {
	// upstream: packages/utils/src/turndown/service.ts:341-353
	body := strings.TrimSpace(text)
	if body == "" {
		return ""
	}
	longest := 0
	run := 0
	for i := range len(body) {
		if body[i] == '`' {
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	delim := strings.Repeat("`", longest+1)
	pad := ""
	if strings.HasPrefix(body, "`") || strings.HasSuffix(body, "`") {
		pad = " "
	}
	return delim + pad + body + pad + delim
}

func replaceLink(content string, node *htmlNode) string {
	// upstream: packages/utils/src/turndown/service.ts:355-367
	href := reLinkDestSpecial.ReplaceAllString(node.getAttrOr("href"), `\$0`)
	title := node.getAttrOr("title")
	dest := href
	if title != "" {
		dest += ` "` + strings.ReplaceAll(title, `"`, `\"`) + `"`
	}
	return "[" + content + "](" + dest + ")"
}

func replaceImage(node *htmlNode) string {
	// upstream: packages/utils/src/turndown/service.ts:369-375
	src := reLinkDestSpecial.ReplaceAllString(node.getAttrOr("src"), `\$0`)
	if src == "" {
		return ""
	}
	alt := escapeMarkdown(node.getAttrOr("alt"))
	title := node.getAttrOr("title")
	dest := src
	if title != "" {
		dest += ` "` + strings.ReplaceAll(title, `"`, `\"`) + `"`
	}
	return "![" + alt + "](" + dest + ")"
}

func isTaskListCheckbox(node *htmlNode) bool {
	return node.nodeName == "INPUT" &&
		node.parent != nil &&
		node.parent.nodeName == "LI" &&
		strings.EqualFold(node.getAttrOr("type"), "checkbox")
}

// htmlDescendantElements ports packages/utils/src/turndown/gfm.ts:4-18.
func htmlDescendantElements(node *htmlNode, name string, limit int) []*htmlNode {
	var matches []*htmlNode
	stack := make([]*htmlNode, 0, len(node.elements))
	for i := len(node.elements) - 1; i >= 0; i-- {
		stack = append(stack, node.elements[i])
	}
	for len(stack) > 0 && (limit <= 0 || len(matches) < limit) {
		child := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if child.nodeName == name {
			matches = append(matches, child)
		}
		if child.nodeName != "TABLE" {
			for i := len(child.elements) - 1; i >= 0; i-- {
				stack = append(stack, child.elements[i])
			}
		}
	}
	return matches
}

func isHeadingTable(node *htmlNode) bool {
	if node.nodeName != "TABLE" {
		return false
	}
	firstRows := htmlDescendantElements(node, "TR", 1)
	if len(firstRows) == 0 {
		return false
	}
	cells := firstRows[0].elements
	if len(cells) == 0 {
		return false
	}
	for _, c := range cells {
		if c.nodeName != "TH" {
			return false
		}
	}
	return true
}

func cellAlignmentMarker(cell *htmlNode) string {
	align := strings.ToLower(cell.getAttrOr("align"))
	if align != "left" && align != "center" && align != "right" {
		if m := reStyleTextAlign.FindStringSubmatch(cell.getAttrOr("style")); len(m) == 2 {
			align = strings.ToLower(m[1])
		}
	}
	switch align {
	case "left":
		return ":--"
	case "center":
		return ":-:"
	case "right":
		return "--:"
	default:
		return "---"
	}
}

func (t *turndownRunner) renderGFMTable(table *htmlNode) (string, error) {
	// upstream: packages/utils/src/turndown/gfm.ts:43-53
	rows := htmlDescendantElements(table, "TR", 0)
	rendered := make([]string, 0, len(rows)+1)
	for rowIdx, row := range rows {
		var cells []*htmlNode
		for _, c := range row.elements {
			if c.nodeName == "TH" || c.nodeName == "TD" {
				cells = append(cells, c)
			}
		}
		values := make([]string, len(cells))
		for i, c := range cells {
			v, err := t.convertChildren(c)
			if err != nil {
				return "", err
			}
			values[i] = strings.TrimSpace(v)
		}
		rendered = append(rendered, "| "+strings.Join(values, " | ")+" |")
		if rowIdx == 0 {
			seps := make([]string, len(cells))
			for i, c := range cells {
				seps[i] = cellAlignmentMarker(c)
			}
			rendered = append(rendered, "| "+strings.Join(seps, " | ")+" |")
		}
	}
	return "\n\n" + strings.Join(rendered, "\n") + "\n\n", nil
}
