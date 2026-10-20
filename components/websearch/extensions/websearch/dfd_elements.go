package websearch

import (
	"regexp"
	"strings"
)

// Port of defuddle 0.19.3 dist/elements/callouts.js, headings.js, code.js and images.js.

func dfdClassList(el *ldNode) []string {
	c := el.className()
	if c == "" {
		return nil
	}
	return strings.Split(c, " ")
}

// dfdClassRemove is classList.remove(...tokens): the attribute is rewritten from the remaining tokens.
func dfdClassRemove(el *ldNode, tokens ...string) {
	var keep []string
	for _, t := range dfdClassList(el) {
		drop := false
		for _, r := range tokens {
			if t == r {
				drop = true
			}
		}
		if !drop {
			keep = append(keep, t)
		}
	}
	if i := el.attrIndex("class"); i >= 0 {
		el.attrs[i].value = strings.Join(keep, " ")
	} else {
		el.setAttribute("class", strings.Join(keep, " "))
	}
}

func dfdCapitalize(s string) string {
	if s == "" {
		return s
	}
	for i, r := range s {
		return strings.ToUpper(string(r)) + s[i+len(string(r)):]
	}
	return s
}

// ---- callouts.js -----------------------------------------------------------------------------------------------

var (
	reDisplayNone = jsRE(`display\s*:\s*none\s*;?`, "i")
)

func dfdCreateCallout(typ, title string, contentSource *ldNode) *ldNode {
	callout := ldNewElement("div")
	callout.setAttribute("data-callout", typ)
	callout.setAttribute("class", "callout")
	titleDiv := ldNewElement("div")
	titleDiv.setAttribute("class", "callout-title")
	titleInner := ldNewElement("div")
	titleInner.setAttribute("class", "callout-title-inner")
	titleInner.setTextContent(title)
	titleDiv.appendChild(titleInner)
	callout.appendChild(titleDiv)
	contentDiv := ldNewElement("div")
	contentDiv.setAttribute("class", "callout-content")
	dfdTransferContent(contentSource, contentDiv)
	callout.appendChild(contentDiv)
	return callout
}

func dfdFindClass(el *ldNode, pred func(string) bool) (string, bool) {
	for _, c := range dfdClassList(el) {
		if pred(c) {
			return c, true
		}
	}
	return "", false
}

var dfdAdmonitionTypes = map[string]bool{"info": true, "warning": true, "note": true, "tip": true, "danger": true, "caution": true, "important": true, "abstract": true, "success": true, "question": true, "failure": true, "bug": true, "example": true, "quote": true}

func dfdStandardizeCallouts(element *ldNode) {
	for _, el := range ldQuerySelectorAll(element, ".callout.is-collapsed, .callout.is-collapsible") {
		isCollapsed := el.hasClass("is-collapsed")
		dfdClassRemove(el, "is-collapsed", "is-collapsible")
		if !el.hasAttribute("data-callout-fold") {
			if isCollapsed {
				el.setAttribute("data-callout-fold", "-")
			} else {
				el.setAttribute("data-callout-fold", "+")
			}
		}
		if fold := ldQuerySelector(el, ".callout-fold"); fold != nil {
			fold.remove()
		}
		if content := ldQuerySelector(el, ".callout-content"); content != nil {
			if style, ok := content.getAttribute("style"); ok && style != "" {
				cleaned := jsTrim(reDisplayNone.ReplaceAllString(style, ""))
				if cleaned != "" {
					content.setAttribute("style", cleaned)
				} else {
					content.removeAttribute("style")
				}
			}
		}
	}
	for _, el := range ldQuerySelectorAll(element, ".markdown-alert") {
		typeClass, ok := dfdFindClass(el, func(c string) bool { return strings.HasPrefix(c, "markdown-alert-") && c != "markdown-alert" })
		typ := "note"
		if ok {
			typ = strings.Replace(typeClass, "markdown-alert-", "", 1)
		}
		title := dfdCapitalize(typ)
		if t := ldQuerySelector(el, ".markdown-alert-title"); t != nil {
			t.remove()
		}
		el.replaceWith(dfdCreateCallout(typ, title, el))
	}
	for _, el := range ldQuerySelectorAll(element, `aside[class*="callout"]`) {
		typeClass, ok := dfdFindClass(el, func(c string) bool { return strings.HasPrefix(c, "callout-") })
		typ := "note"
		if ok {
			typ = strings.Replace(typeClass, "callout-", "", 1)
		}
		title := dfdCapitalize(typ)
		contentEl := ldQuerySelector(el, ".callout-content")
		if contentEl == nil {
			contentEl = el
		}
		el.replaceWith(dfdCreateCallout(typ, title, contentEl))
	}
	for _, el := range ldQuerySelectorAll(element, ".admonition") {
		if el.attr("data-callout") != "" {
			continue
		}
		typ, ok := dfdFindClass(el, func(c string) bool { return dfdAdmonitionTypes[c] })
		if !ok {
			typ = "note"
		}
		titleEl := ldQuerySelector(el, ".admonition-title")
		title := ""
		if titleEl != nil {
			title = jsTrim(titleEl.textContent())
		}
		if title == "" {
			title = dfdCapitalize(typ)
		}
		if titleEl != nil {
			titleEl.remove()
		}
		contentEl := ldQuerySelector(el, ".admonition-content")
		if contentEl == nil {
			contentEl = ldQuerySelector(el, ".details-content")
		}
		if contentEl == nil {
			contentEl = el
		}
		el.replaceWith(dfdCreateCallout(typ, title, contentEl))
	}
	for _, el := range ldQuerySelectorAll(element, `.alert[class*="alert-"]`) {
		typeClass, ok := dfdFindClass(el, func(c string) bool { return strings.HasPrefix(c, "alert-") && c != "alert-dismissible" })
		typ := "note"
		if ok {
			typ = strings.Replace(typeClass, "alert-", "", 1)
		}
		titleEl := ldQuerySelector(el, ".alert-heading, .alert-title")
		title := ""
		if titleEl != nil {
			title = jsTrim(titleEl.textContent())
		}
		if title == "" {
			title = dfdCapitalize(typ)
		}
		if titleEl != nil {
			titleEl.remove()
		}
		el.replaceWith(dfdCreateCallout(typ, title, el))
	}
}

// ---- headings.js -----------------------------------------------------------------------------------------------

var rePermalinkText = regexp.MustCompile("^[#\u00b6\u00a7\ufeff]$")

func dfdIsPermalinkAnchor(node *ldNode) bool {
	if node.name != "a" {
		return false
	}
	href := node.attr("href")
	title := strings.ToLower(node.attr("title"))
	className := strings.ToLower(node.attr("class"))
	text := jsTrim(node.textContent())
	if strings.HasPrefix(href, "#") {
		return true
	}
	if strings.Contains(title, "permalink") {
		return true
	}
	if strings.Contains(className, "permalink") || strings.Contains(className, "heading-anchor") || strings.Contains(className, "anchor-link") {
		return true
	}
	return rePermalinkText.MatchString(text)
}

func dfdIsHeadingNavElement(node *ldNode) bool {
	tag := node.name
	if tag == "button" {
		return true
	}
	if tag == "a" && dfdIsPermalinkAnchor(node) {
		return true
	}
	if node.hasClass("anchor") || node.hasClass("permalink-widget") {
		return true
	}
	if tag == "span" || tag == "div" {
		for _, a := range ldQuerySelectorAll(node, "a") {
			if dfdIsPermalinkAnchor(a) {
				return true
			}
		}
	}
	return false
}

func dfdRemovePermalinkAnchors(element *ldNode) {
	for _, link := range ldQuerySelectorAll(element, "h1 a, h2 a, h3 a, h4 a, h5 a, h6 a, a.permalink, a.anchor-link, a.heading-anchor") {
		if dfdIsPermalinkAnchor(link) {
			link.remove()
		}
	}
}

func dfdHeadingTransform(el *ldNode) *ldNode {
	newHeading := ldNewElement(el.name)
	for _, a := range append([]ldAttr(nil), el.attrs...) {
		if dfdAllowedAttributes[a.name] {
			newHeading.setAttribute(a.name, a.value)
		}
	}
	if len(el.children()) == 0 {
		newHeading.setTextContent(jsTrim(el.textContent()))
		return newHeading
	}
	clone := el.cloneNode(true)
	var navTexts []string
	var toRemove []*ldNode
	navSet := map[*ldNode]int{}
	var navOrder []*ldNode
	for _, child := range ldQuerySelectorAll(clone, "*") {
		if !dfdIsHeadingNavElement(child) {
			continue
		}
		setNav := func(n *ldNode, text string) {
			if _, ok := navSet[n]; !ok {
				navOrder = append(navOrder, n)
				navSet[n] = len(navTexts)
				navTexts = append(navTexts, text)
			} else {
				navTexts[navSet[n]] = text
			}
		}
		setNav(child, jsTrim(child.textContent()))
		if parent := child.parentElement(); parent != nil && parent != clone && jsTrim(parent.textContent()) == jsTrim(child.textContent()) {
			setNav(parent, jsTrim(child.textContent()))
		}
		toRemove = append(toRemove, child)
	}
	for _, e := range toRemove {
		e.remove()
	}
	textContent := jsTrim(clone.textContent())
	if textContent == "" && len(navOrder) > 0 {
		textContent = navTexts[0]
	}
	newHeading.setTextContent(textContent)
	return newHeading
}

// ---- code.js ---------------------------------------------------------------------------------------------------

var dfdHighlighterPatterns = []*regexp.Regexp{
	regexp.MustCompile(`^language-(\w+)$`), regexp.MustCompile(`^lang-(\w+)$`), regexp.MustCompile(`^(\w+)-code$`),
	regexp.MustCompile(`^code-(\w+)$`), regexp.MustCompile(`^syntax-(\w+)$`), regexp.MustCompile(`^code-snippet__(\w+)$`),
	regexp.MustCompile(`^highlight-(\w+)$`), regexp.MustCompile(`^(\w+)-snippet$`),
	regexp.MustCompile(`(?i)(?:^|\s)(?:language|lang|brush|syntax)-(\w+)(?:\s|$)`),
}

var dfdCodeLanguages = func() map[string]bool {
	m := map[string]bool{}
	for _, l := range strings.Fields(`abap actionscript ada adoc agda antlr4 applescript arduino armasm asciidoc aspnet atom bash batch c clojure cmake cobol coffeescript cpp c++ crystal csharp cs dart django dockerfile dotnet elixir elm erlang fortran fsharp gdscript gitignore glsl golang gradle graphql groovy haskell hs haxe hlsl html idris java javascript js jsx jsdoc json jsonp julia kotlin latex lean lean4 lisp elisp livescript lua makefile markdown md markup masm mathml matlab mongodb mysql nasm nginx nim nix objc ocaml pascal perl php postgresql powershell prolog puppet python regex rss ruby rb rust scala scheme shell sh solidity sparql sql ssml svg swift tcl terraform tex toml typescript ts tsx unrealscript verilog vhdl webassembly wasm xml yaml yml zig`) {
		m[l] = true
	}
	return m
}()

var (
	reLineSelector   = `div[class*="line"], span[class*="line"], .ec-line, [data-line-number], [data-line]`
	reAllDigits      = jsRE(`^\d+$`, "")
	reTrimEdgesTab   = jsRE(`^[ \t]+|[ \t]+$`, "")
	reLeadingNL      = jsRE(`^\n+`, "")
	reTrailingNL     = jsRE(`\n+$`, "")
	reNL3            = jsRE(`\n{3,}`, "")
)

func dfdCodeLanguage(element *ldNode) string {
	if v := firstNonEmpty(element.attr("data-lang"), element.attr("data-language"), element.attr("language")); v != "" {
		return strings.ToLower(v)
	}
	classNames := dfdClassList(element)
	if element.hasClass("syntaxhighlighter") {
		for _, c := range classNames {
			if c != "syntaxhighlighter" && c != "nogutter" {
				if dfdCodeLanguages[strings.ToLower(c)] {
					return strings.ToLower(c)
				}
				break
			}
		}
	}
	for _, c := range classNames {
		lc := strings.ToLower(c)
		for _, p := range dfdHighlighterPatterns {
			if m := p.FindStringSubmatch(lc); m != nil && m[1] != "" && dfdCodeLanguages[strings.ToLower(m[1])] {
				return strings.ToLower(m[1])
			}
		}
	}
	for _, c := range classNames {
		if dfdCodeLanguages[strings.ToLower(c)] {
			return strings.ToLower(c)
		}
	}
	return ""
}

func dfdExtractWordPressContent(element *ldNode) string {
	if container := ldQuerySelector(element, ".syntaxhighlighter table .code .container"); container != nil {
		var lines []string
		for _, line := range container.children() {
			var parts strings.Builder
			for _, code := range ldQuerySelectorAll(line, "code") {
				text := code.textContent()
				if code.hasClass("spaces") {
					text = strings.Repeat(" ", jsLen(text))
				}
				parts.WriteString(text)
			}
			if parts.Len() > 0 {
				lines = append(lines, parts.String())
			} else {
				lines = append(lines, line.textContent())
			}
		}
		return strings.Join(lines, "\n")
	}
	codeLines := ldQuerySelectorAll(element, ".code .line")
	if len(codeLines) > 0 {
		var lines []string
		for _, line := range codeLines {
			var parts strings.Builder
			for _, code := range ldQuerySelectorAll(line, "code") {
				parts.WriteString(code.textContent())
			}
			if parts.Len() > 0 {
				lines = append(lines, parts.String())
			} else {
				lines = append(lines, line.textContent())
			}
		}
		return strings.Join(lines, "\n")
	}
	return ""
}

func dfdTrimTrailingNL1(s string) string { return strings.TrimSuffix(s, "\n") }

func dfdExtractStructuredText(element *ldNode) string {
	if element.kind == ldText {
		if p := element.parentElement(); p != nil && ldQuerySelector(p, "[data-line], .line") != nil && jsTrim(element.data) == "" {
			return ""
		}
		return element.data
	}
	if element.kind != ldElem {
		return ""
	}
	if ldMatches(element, ".hover-info, .hover-container") {
		return ""
	}
	tag := element.tagName()
	if tag == "BUTTON" || tag == "STYLE" {
		return ""
	}
	if tag == "BR" {
		if prev := element.previousElementSibling(); prev != nil && ldMatches(prev, reLineSelector) {
			return ""
		}
		return "\n"
	}
	if ldMatches(element, "span.lnt") || ldMatches(element, "span.lineno") || ldMatches(element, ".react-syntax-highlighter-line-number") || ldMatches(element, ".rouge-gutter") {
		return ""
	}
	if (tag == "DIV" || tag == "SPAN") && len(element.children()) == 2 {
		gutter := jsTrim(element.children()[0].textContent())
		if reAllDigits.MatchString(gutter) {
			return dfdTrimTrailingNL1(dfdExtractStructuredText(element.children()[1])) + "\n"
		}
	}
	if ldMatches(element, reLineSelector) {
		if cc := ldQuerySelector(element, `.code:not(.token), .content:not(.token), [class*="code-"], [class*="content-"]`); cc != nil {
			return dfdTrimTrailingNL1(cc.textContent()) + "\n"
		}
		if lineNumber := ldQuerySelector(element, `.line-number, .gutter, [class*="line-number"], [class*="gutter"]`); lineNumber != nil {
			var b strings.Builder
			for _, node := range element.childNodes() {
				if !lineNumber.contains(node) {
					b.WriteString(dfdExtractStructuredText(node))
				}
			}
			return dfdTrimTrailingNL1(b.String()) + "\n"
		}
		return dfdTrimTrailingNL1(element.textContent()) + "\n"
	}
	var b strings.Builder
	for _, child := range element.childNodes() {
		b.WriteString(dfdExtractStructuredText(child))
	}
	return b.String()
}

// dfdSearchNonSpace is line.search(/\S/) in UTF-16 units.
func dfdSearchNonSpace(line string) int {
	n := 0
	for _, r := range line {
		if !jsSpace(r) {
			return n
		}
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return -1
}

func dfdCodeTransform(el *ldNode) *ldNode {
	for _, btn := range ldQuerySelectorAll(el, `button, [class*="codeblock-button"]`) {
		btn.remove()
	}
	for _, elem := range ldQuerySelectorAll(el, `[class*="header"], [class*="toolbar"], [class*="titlebar"], [class*="title-bar"]`) {
		tag := elem.tagName()
		if tag != "DIV" && tag != "SPAN" {
			continue
		}
		if la := ldClosest(elem, "[data-line], .line"); la != nil && el.contains(la) {
			continue
		}
		if ldQuerySelector(elem, "[data-line], .line, pre") != nil {
			continue
		}
		if dfdCountWords(jsTrim(elem.textContent())) <= 5 {
			elem.remove()
		}
	}
	language := ""
	for current := el; current != nil && language == ""; current = current.parentElement() {
		language = dfdCodeLanguage(current)
		if language == "" && current == el {
			codeEl := ldQuerySelector(current, `code[data-lang], code[class*="language-"]`)
			if codeEl == nil {
				codeEl = ldQuerySelector(current, "code")
			}
			if codeEl != nil {
				language = dfdCodeLanguage(codeEl)
			}
		}
	}
	cmContent := ldQuerySelector(el, ".cm-content")
	if cmContent != nil && language == "" {
		for _, div := range ldQuerySelectorAll(el, "div") {
			if div.contains(cmContent) {
				continue
			}
			text := strings.ToLower(jsTrim(div.textContent()))
			if text != "" && dfdCodeLanguages[text] {
				language = text
				break
			}
		}
	}
	codeContent := ""
	if ldMatches(el, ".syntaxhighlighter, .wp-block-syntaxhighlighter-code") {
		codeContent = dfdExtractWordPressContent(el)
	}
	if codeContent == "" && cmContent != nil {
		codeContent = dfdExtractStructuredText(cmContent)
	} else if codeContent == "" {
		target := el
		if el.tagName() != "PRE" && el.tagName() != "CODE" {
			pres := ldQuerySelectorAll(el, "pre")
			var codePre *ldNode
			for _, p := range pres {
				if ldQuerySelector(p, `code[data-lang], code[class*="language-"], .line, [data-line]`) != nil {
					codePre = p
					break
				}
			}
			if codePre == nil {
				for _, p := range pres {
					if ldQuerySelector(p, "span[class]") != nil && !p.hasClass("lineno") {
						codePre = p
						break
					}
				}
			}
			if codePre != nil {
				target = codePre
			}
		}
		codeContent = dfdExtractStructuredText(target)
	}
	if ldMatches(el, "code.hl.block") {
		codeContent = reTrimEdgesTab.ReplaceAllString(codeContent, "")
		codeContent = strings.ReplaceAll(codeContent, "\t", "    ")
		codeContent = strings.ReplaceAll(codeContent, "\u00a0", " ")
		codeContent = reLeadingNL.ReplaceAllString(codeContent, "")
	} else {
		codeContent = strings.ReplaceAll(codeContent, "\t", "    ")
		codeContent = strings.ReplaceAll(codeContent, "\u00a0", " ")
		lines := strings.Split(codeContent, "\n")
		minIndent := -1
		for _, line := range lines {
			if first := dfdSearchNonSpace(line); first > -1 && (minIndent == -1 || first < minIndent) {
				minIndent = first
			}
		}
		if minIndent > 0 {
			for i, line := range lines {
				lines[i] = jsSlice(line, minIndent, -1)
			}
			codeContent = strings.Join(lines, "\n")
		}
		codeContent = jsTrim(codeContent)
		codeContent = reNL3.ReplaceAllString(codeContent, "\n\n")
		codeContent = reLeadingNL.ReplaceAllString(codeContent, "")
		codeContent = reTrailingNL.ReplaceAllString(codeContent, "")
	}
	ancestor := el
	for i := 0; i < 3 && ancestor != nil; i++ {
		container := ancestor.parentElement()
		if container == nil || container.tagName() == "BODY" {
			break
		}
		if len(container.children()) > 5 {
			break
		}
		if ldClosest(container, "[data-callout]") != nil {
			break
		}
		for _, sib := range container.children() {
			if sib.contains(el) {
				continue
			}
			t := sib.tagName()
			if t != "DIV" && t != "SPAN" {
				continue
			}
			if dfdCountWords(jsTrim(sib.textContent())) <= 5 && ldQuerySelector(sib, "pre, code, img, svg, table, h1, h2, h3, h4, h5, h6, p, blockquote, ul, ol, hr") == nil {
				sib.remove()
			}
		}
		ancestor = container
	}
	newPre := ldNewElement("pre")
	if ldMatches(el, "code.hl.block, pre.hl.lean.lean-output") {
		newPre.setAttribute("data-verso-code", "true")
	}
	code := ldNewElement("code")
	if language != "" {
		code.setAttribute("data-lang", language)
		code.setAttribute("class", "language-"+language)
	}
	code.setTextContent(codeContent)
	newPre.appendChild(code)
	return newPre
}

// ---- images.js -------------------------------------------------------------------------------------------------

var (
	reSrcsetPattern   = regexp.MustCompile(`\.(jpg|jpeg|png|webp)\s+\d`)
	reSrcPattern      = jsRE(`^\s*\S+\.(jpg|jpeg|png|webp)\S*\s*$`, "")
	reImageURLPattern = jsRE(`\.(jpg|jpeg|png|webp|gif|avif)(\?.*)?$`, "i")
	reWidthPattern    = jsRE(`\s(\d+)w`, "")
	reDprPattern      = regexp.MustCompile(`dpr=(\d+(?:\.\d+)?)`)
	reURLPattern      = jsRE(`^([^\s]+)`, "")
	reAbsoluteURL     = regexp.MustCompile(`^https?://`)
	reFilenamePattern = jsRE(`^[\w\-\.\/\\]+\.(jpg|jpeg|png|gif|webp|svg)$`, "i")
	reDatePattern     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	reImgExt          = jsRE(`\.(jpg|jpeg|png|webp|gif)(\?.*)?$`, "i")
	reSrcsetEntry     = jsRE(`(.+?)\s+(\d+(?:\.\d+)?[wx])`, "")
	reLeadingComma    = jsRE(`^,\s*`, "")
	reDigitsOnly      = regexp.MustCompile(`^\d+$`)
)

func dfdIsSvgDataURL(src string) bool { return strings.HasPrefix(src, "data:image/svg+xml") }

func dfdIsValidImageURL(src string) bool {
	if strings.HasPrefix(src, "data:") {
		return false
	}
	if src == "" || jsTrim(src) == "" {
		return false
	}
	return reImageURLPattern.MatchString(src) || strings.Contains(src, "image") || strings.Contains(src, "img") || strings.Contains(src, "photo")
}

func dfdHasBetterImageSource(element *ldNode) bool {
	if element.hasAttribute("data-src") || element.hasAttribute("data-srcset") {
		return true
	}
	for i := 0; i < len(element.attrs); i++ {
		a := element.attrs[i]
		if a.name == "src" {
			continue
		}
		if strings.HasPrefix(a.name, "data-") && reImgExt.MatchString(a.value) {
			return true
		}
		if reImgExt.MatchString(a.value) {
			return true
		}
	}
	return false
}

func dfdIsImageElement(el *ldNode) bool {
	switch el.name {
	case "img", "video", "picture", "source":
		return true
	}
	return false
}

func dfdContainsImage(element *ldNode) bool {
	if dfdIsImageElement(element) {
		return true
	}
	return len(ldQuerySelectorAll(element, "img, video, picture, source")) > 0
}

func dfdFindMainImage(element *ldNode) *ldNode {
	if dfdIsImageElement(element) {
		return element
	}
	if pics := ldQuerySelectorAll(element, "picture"); len(pics) > 0 {
		return pics[0]
	}
	imgs := ldQuerySelectorAll(element, "img")
	var filtered []*ldNode
	for _, img := range imgs {
		src := img.attr("src")
		alt := img.attr("alt")
		if dfdIsSvgDataURL(src) || dfdIsBase64Placeholder(src) {
			continue
		}
		if jsTrim(alt) == "" && len(imgs) > 1 {
			continue
		}
		filtered = append(filtered, img)
	}
	if len(filtered) > 0 {
		return filtered[0]
	}
	if v := ldQuerySelectorAll(element, "video"); len(v) > 0 {
		return v[0]
	}
	if s := ldQuerySelectorAll(element, "source"); len(s) > 0 {
		return s[0]
	}
	if all := ldQuerySelectorAll(element, "img, picture, source, video"); len(all) > 0 {
		return all[0]
	}
	return nil
}

func dfdFindCaption(element *ldNode) *ldNode {
	if f := ldQuerySelector(element, "figcaption"); f != nil {
		return f
	}
	combined := `[class*="caption"], [class*="description"], [class*="alt"], [class*="title"], [class*="credit"], [class*="text"], [class*="post-thumbnail-text"], [class*="image-caption"], [class*="photo-caption"], [aria-label], [title]`
	found := map[string]bool{}
	for _, c := range ldQuerySelectorAll(element, combined) {
		if dfdIsImageElement(c) {
			continue
		}
		text := jsTrim(c.textContent())
		if text != "" && !found[text] {
			found[text] = true
			return c
		}
	}
	if img := ldQuerySelector(element, "img"); img != nil && img.hasAttribute("alt") {
		if alt := img.attr("alt"); alt != "" && jsTrim(alt) != "" {
			captionEl := ldNewElement("div")
			captionEl.setTextContent(alt)
			return captionEl
		}
	}
	if parent := element.parentElement(); parent != nil {
		for _, sibling := range parent.children() {
			if sibling == element {
				continue
			}
			has := false
			for _, cls := range dfdClassList(sibling) {
				if strings.Contains(cls, "caption") || strings.Contains(cls, "credit") || strings.Contains(cls, "text") || strings.Contains(cls, "description") {
					has = true
					break
				}
			}
			if has && jsTrim(sibling.textContent()) != "" {
				return sibling
			}
		}
	}
	imgs := ldQuerySelectorAll(element, "img")
	for _, img := range imgs {
		if img.parentElement() == nil {
			continue
		}
		for next := img.nextElementSibling(); next != nil; next = next.nextElementSibling() {
			switch next.tagName() {
			case "EM", "STRONG", "SPAN", "I", "B", "SMALL", "CITE":
				if jsTrim(next.textContent()) != "" {
					return next
				}
			}
		}
	}
	for _, img := range imgs {
		parent := img.parentElement()
		if parent == nil {
			continue
		}
		for _, te := range ldQuerySelectorAll(parent, "em, strong, span, i, b, small, cite") {
			if te == img {
				continue
			}
			if jsTrim(te.textContent()) != "" {
				return te
			}
		}
	}
	return nil
}

func dfdExtractUniqueCaptionContent(caption *ldNode) string {
	var texts []string
	processed := map[string]bool{}
	var process func(n *ldNode)
	process = func(n *ldNode) {
		switch n.kind {
		case ldText:
			text := jsTrim(n.data)
			if text != "" && !processed[text] {
				texts = append(texts, text)
				processed[text] = true
			}
		case ldElem:
			for _, c := range n.childNodes() {
				process(c)
			}
		}
	}
	for _, c := range caption.childNodes() {
		process(c)
	}
	if len(texts) > 0 {
		return strings.Join(texts, " ")
	}
	return ldInnerHTML(caption)
}

func dfdHasMeaningfulCaption(caption *ldNode) bool {
	text := jsTrim(caption.textContent())
	if jsLen(text) < 10 || strings.HasPrefix(text, "http://") || strings.HasPrefix(text, "https://") {
		return false
	}
	if reFilenamePattern.MatchString(text) {
		return false
	}
	if reDigitsOnly.MatchString(text) || reDatePattern.MatchString(text) {
		return false
	}
	return true
}

func dfdCopyAttributesExcept(source, target *ldNode, exclude ...string) {
	for i := 0; i < len(source.attrs); i++ {
		a := source.attrs[i]
		skip := false
		for _, e := range exclude {
			if a.name == e {
				skip = true
			}
		}
		if !skip {
			target.setAttribute(a.name, a.value)
		}
	}
}

func dfdExtractFirstURLFromSrcset(srcset string) (string, bool) {
	if srcset == "" || jsTrim(srcset) == "" {
		return "", false
	}
	trimmed := jsTrim(srcset)
	lastIndex := 0
	for _, m := range reSrcsetEntry.FindAllStringSubmatchIndex(trimmed, -1) {
		url := jsTrim(trimmed[m[2]:m[3]])
		if lastIndex > 0 {
			url = reLeadingComma.ReplaceAllString(url, "")
		}
		lastIndex = m[1]
		if url == "" {
			continue
		}
		if dfdIsSvgDataURL(url) {
			continue
		}
		return url, true
	}
	if m := reURLPattern.FindStringSubmatch(trimmed); m != nil && m[1] != "" && !dfdIsSvgDataURL(m[1]) {
		return m[1], true
	}
	return "", false
}

func dfdApplySrcsetToImage(srcset string, img *ldNode) {
	img.setAttribute("srcset", srcset)
	if first, ok := dfdExtractFirstURLFromSrcset(srcset); ok && dfdIsValidImageURL(first) {
		img.setAttribute("src", first)
	}
}

func dfdSelectBestSource(sources []*ldNode) *ldNode {
	if len(sources) == 0 {
		return nil
	}
	if len(sources) == 1 {
		return sources[0]
	}
	for _, s := range sources {
		if !s.hasAttribute("media") {
			return s
		}
	}
	var best *ldNode
	maxRes := 0.0
	for _, s := range sources {
		srcset := s.attr("srcset")
		if srcset == "" {
			continue
		}
		if wm := reWidthPattern.FindStringSubmatch(srcset); wm != nil && wm[1] != "" {
			width := jsParseInt(wm[1])
			dpr := 1.0
			if dm := reDprPattern.FindStringSubmatch(srcset); dm != nil {
				dpr = jsParseFloat(dm[1])
			}
			if res := width * dpr; res > maxRes {
				maxRes = res
				best = s
			}
		}
	}
	if best != nil {
		return best
	}
	return sources[0]
}

func dfdProcessImgElement(element *ldNode) *ldNode {
	src := element.attr("src")
	if dfdIsBase64Placeholder(src) || dfdIsSvgDataURL(src) {
		if parent := element.parentElement(); parent != nil {
			var filtered []*ldNode
			for _, s := range ldQuerySelectorAll(parent, "source") {
				if s.hasAttribute("data-srcset") && s.attr("data-srcset") != "" {
					filtered = append(filtered, s)
				}
			}
			if len(filtered) > 0 {
				newImg := ldNewElement("img")
				if ds := element.attr("data-src"); ds != "" && !dfdIsSvgDataURL(ds) {
					newImg.setAttribute("src", ds)
				}
				dfdCopyAttributesExcept(element, newImg, "src")
				return newImg
			}
		}
	}
	return element.cloneNode(true)
}

func dfdProcessSourceElement(element *ldNode) *ldNode {
	newImg := ldNewElement("img")
	if srcset := element.attr("srcset"); srcset != "" {
		dfdApplySrcsetToImage(srcset, newImg)
	}
	if parent := element.parentElement(); parent != nil {
		var filtered []*ldNode
		for _, img := range ldQuerySelectorAll(parent, "img") {
			src := img.attr("src")
			if !dfdIsBase64Placeholder(src) && !dfdIsSvgDataURL(src) && src != "" {
				filtered = append(filtered, img)
			}
		}
		if len(filtered) > 0 {
			dfdCopyAttributesExcept(filtered[0], newImg, "src", "srcset")
			if !newImg.hasAttribute("src") || !dfdIsValidImageURL(newImg.attr("src")) {
				if imgSrc := filtered[0].attr("src"); imgSrc != "" && dfdIsValidImageURL(imgSrc) {
					newImg.setAttribute("src", imgSrc)
				}
			}
		} else if dataSrcImg := ldQuerySelector(parent, "img[data-src]"); dataSrcImg != nil {
			dfdCopyAttributesExcept(dataSrcImg, newImg, "src", "srcset")
			if !newImg.hasAttribute("src") || !dfdIsValidImageURL(newImg.attr("src")) {
				if ds := dataSrcImg.attr("data-src"); ds != "" && dfdIsValidImageURL(ds) {
					newImg.setAttribute("src", ds)
				}
			}
		}
	}
	return newImg
}

func dfdProcessImageElement(element *ldNode) *ldNode {
	switch element.name {
	case "img":
		return dfdProcessImgElement(element)
	case "picture":
		if inside := ldQuerySelector(element, "img"); inside != nil {
			return dfdProcessImgElement(inside)
		}
		return element.cloneNode(true)
	case "source":
		return dfdProcessSourceElement(element)
	}
	return element.cloneNode(true)
}

func dfdCreateFigureWithCaption(imageElement, caption *ldNode) *ldNode {
	figure := ldNewElement("figure")
	figure.appendChild(imageElement.cloneNode(true))
	figcaption := ldNewElement("figcaption")
	figcaption.appendChild(ldParseFragment(dfdExtractUniqueCaptionContent(caption)))
	figure.appendChild(figcaption)
	return figure
}

func dfdPictureTransform(el *ldNode) *ldNode {
	sources := ldQuerySelectorAll(el, "source")
	img := ldQuerySelector(el, "img")
	if img == nil {
		if best := dfdSelectBestSource(sources); best != nil {
			if srcset := best.attr("srcset"); srcset != "" {
				newImg := ldNewElement("img")
				dfdApplySrcsetToImage(srcset, newImg)
				el.replaceChildren(newImg)
				return el
			}
		}
		return el
	}
	bestSrcset, bestSrc := "", ""
	if len(sources) > 0 {
		if best := dfdSelectBestSource(sources); best != nil {
			bestSrcset = best.attr("srcset")
			if bestSrcset != "" {
				bestSrc, _ = dfdExtractFirstURLFromSrcset(bestSrcset)
			}
		}
	}
	if bestSrcset != "" {
		img.setAttribute("srcset", bestSrcset)
	}
	if bestSrc != "" && dfdIsValidImageURL(bestSrc) {
		img.setAttribute("src", bestSrc)
	} else if !img.hasAttribute("src") || !dfdIsValidImageURL(img.attr("src")) {
		set := img.attr("srcset")
		if set == "" {
			set = bestSrcset
		}
		if first, ok := dfdExtractFirstURLFromSrcset(set); ok && dfdIsValidImageURL(first) {
			img.setAttribute("src", first)
		}
	}
	for _, s := range sources {
		s.remove()
	}
	return el
}

func dfdUniImageTransform(el *ldNode) *ldNode {
	figure := ldNewElement("figure")
	img := ldNewElement("img")
	original := ldQuerySelector(el, "img")
	if original == nil {
		return figure
	}
	bestSrc, _ := original.getAttribute("src")
	if dl := original.attr("data-loading"); dl != "" {
		if v, err := jsonParse(dl); err == nil {
			if obj, ok := v.(*jsObj); ok {
				if d, ok := obj.get("desktop"); ok {
					if s, ok := d.(string); ok && s != "" && dfdIsValidImageURL(s) {
						bestSrc = s
					}
				}
			}
		}
	}
	if bestSrc != "" && dfdIsValidImageURL(bestSrc) {
		img.setAttribute("src", bestSrc)
	} else {
		return figure
	}
	altText := original.attr("alt")
	if altText == "" {
		altText = el.attr("alt-text")
	}
	if altText != "" {
		img.setAttribute("alt", altText)
	}
	figure.appendChild(img)
	if figcaptionEl := ldQuerySelector(el, "figcaption"); figcaptionEl != nil {
		captionText := jsTrim(figcaptionEl.textContent())
		if captionText != "" && jsLen(captionText) > 5 {
			figcaption := ldNewElement("figcaption")
			if richP := ldQuerySelector(figcaptionEl, ".rich-text p"); richP != nil {
				dfdTransferContent(richP, figcaption)
			} else {
				figcaption.setTextContent(captionText)
			}
			figure.appendChild(figcaption)
		}
	}
	return figure
}

func dfdLazyImageTransform(el *ldNode) *ldNode {
	src := el.attr("src")
	better := dfdHasBetterImageSource(el)
	if (dfdIsBase64Placeholder(src) || dfdIsSvgDataURL(src)) && better {
		el.removeAttribute("src")
	}
	if ds := el.attr("data-src"); ds != "" && el.attr("src") == "" {
		el.setAttribute("src", ds)
	}
	if dss := el.attr("data-srcset"); dss != "" && el.attr("srcset") == "" {
		el.setAttribute("srcset", dss)
	}
	for i := 0; i < len(el.attrs); i++ {
		a := el.attrs[i]
		if a.name == "src" || a.name == "srcset" || a.name == "alt" {
			continue
		}
		if a.value != "" && (a.value[0] == '{' || a.value[0] == '[') {
			continue
		}
		if reSrcsetPattern.MatchString(a.value) {
			el.setAttribute("srcset", a.value)
		} else if reSrcPattern.MatchString(a.value) {
			hasAbs := reAbsoluteURL.MatchString(el.attr("src"))
			isAbsNew := reAbsoluteURL.MatchString(a.value)
			if !hasAbs || isAbsNew {
				el.setAttribute("src", a.value)
			}
		}
	}
	dfdClassRemove(el, "lazy", "lazyload")
	el.removeAttribute("data-ll-status")
	el.removeAttribute("data-src")
	el.removeAttribute("data-srcset")
	el.removeAttribute("loading")
	return el
}

func dfdSpanImageTransform(el *ldNode) (result *ldNode) {
	result = el
	if th := ldTry(func() {
		if !dfdContainsImage(el) {
			return
		}
		for _, child := range el.children() {
			if dfdBlockLevelElements[child.name] {
				return
			}
		}
		img := dfdFindMainImage(el)
		if img == nil {
			return
		}
		caption := dfdFindCaption(el)
		processed := dfdProcessImageElement(img)
		if caption != nil && dfdHasMeaningfulCaption(caption) {
			figure := dfdCreateFigureWithCaption(processed, caption)
			if caption.parent != nil {
				caption.remove()
			}
			result = figure
			return
		}
		result = processed
	}); th != nil {
		return el
	}
	return result
}

func dfdFigureTransform(el *ldNode) (result *ldNode) {
	result = el
	if th := ldTry(func() {
		if !dfdContainsImage(el) {
			return
		}
		img := dfdFindMainImage(el)
		if img == nil {
			return
		}
		caption := dfdFindCaption(el)
		if caption != nil && dfdHasMeaningfulCaption(caption) {
			current := dfdFindMainImage(el)
			var toAdd *ldNode
			if current != nil {
				toAdd = current
			} else {
				toAdd = dfdProcessImageElement(img)
			}
			result = dfdCreateFigureWithCaption(toAdd, caption)
		}
	}); th != nil {
		return el
	}
	return result
}
