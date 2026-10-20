package websearch

import (
	"strconv"
	"strings"
)

// Temml 0.13.4 (MIT): src/environments/array.js, cd.js, borderTree.js.

// tmRow is a row of an {array}: the original keeps rows as arrays that parseArray appends to after they are in the body.
type tmRow struct{ cells []any }

type tmArrayParams struct {
	cols           []any
	envClasses     []string
	autoTag        any // bool or nil (undefined)
	singleRow      bool
	emptySingleRow bool
	maxNumCols     int
	leqno          bool
	arraystretch   any
	arraycolsep    any
}

func tmGetHLines(parser *tmParser) []bool {
	hlineInfo := []bool{}
	parser.consumeSpaces()
	nxt := parser.fetch().text
	if nxt == "\\relax" {
		parser.consume()
		parser.consumeSpaces()
		nxt = parser.fetch().text
	}
	for nxt == "\\hline" || nxt == "\\hdashline" {
		parser.consume()
		hlineInfo = append(hlineInfo, nxt == "\\hdashline")
		parser.consumeSpaces()
		nxt = parser.fetch().text
	}
	return hlineInfo
}

func tmValidateAmsEnvironmentContext(ctx *tmCtx) {
	if !ctx.parser.settings.displayMode {
		tmThrowf("{" + ctx.envName + "} can be used only in display mode.")
	}
}

func tmStringFromArg(arg []*tmToken) string {
	var b strings.Builder
	for i := len(arg) - 1; i > -1; i-- {
		b.WriteString(arg[i].text)
	}
	return b.String()
}

func tmMacroString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return tmStringFromArg(v.(*tmMacroDef).tokens)
}

// tmArrayGaps is arrayGaps(macros): the array stretch (nil for NaN) and the column separation (nil when it does not match).
func tmArrayGaps(macros *tmNamespace) (any, any) {
	arraystretch := tmMacroString(macros.get("\\arraystretch"))
	var stretch any
	if !tmIsNaNString(arraystretch) {
		f, _ := tmParseJSNumber(jsTrim(arraystretch))
		stretch = f
	}
	colsepStr := tmMacroString(macros.get("\\arraycolsep"))
	var colsep any
	if m := tmSizeRe.FindStringSubmatch(colsepStr); m != nil {
		n, _ := strconv.ParseFloat(m[1]+m[2], 64)
		colsep = tmSize{n, m[3]}
	}
	return stretch, colsep
}

func tmCheckCellForLabels(cell []any) string {
	rowLabel := ""
	for _, c := range cell {
		if tmType(c) == "label" {
			if rowLabel != "" {
				tmThrowf("Multiple \\labels in one row")
			}
			rowLabel = tmStr(c.(tmObj), "string")
		}
	}
	return rowLabel
}

func tmGetAutoTag(name string) any {
	if !strings.Contains(name, "ed") {
		return !strings.Contains(name, "*")
	}
	return nil
}

func tmContains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func tmParseArray(parser *tmParser, p tmArrayParams, scriptLevel string) tmObj {
	endToken := "\\end"
	if tmContains(p.envClasses, "bordermatrix") {
		endToken = "}"
	}
	parser.gullet.beginGroup()
	if !p.singleRow {
		parser.gullet.macros.set("\\cr", "\\\\\\relax", false)
	}
	parser.gullet.beginGroup()
	row := &tmRow{}
	body := []any{row}
	rowGaps := []any{}
	labels := []string{}
	hLinesBeforeRow := [][]bool{}
	var tags []any
	hasTags := p.autoTag != nil
	if hasTags {
		tags = []any{}
	}
	autoTag := tmTruthy(p.autoTag)
	beginRow := func() {
		if autoTag {
			parser.gullet.macros.set("\\@eqnsw", "1", true)
		}
	}
	endRow := func() {
		if hasTags {
			if tmTruthy(parser.gullet.macros.get("\\df@tag")) {
				tags = append(tags, parser.subparse([]*tmToken{{text: "\\df@tag"}}))
				parser.gullet.macros.set("\\df@tag", nil, true)
			} else {
				tags = append(tags, autoTag && parser.gullet.macros.get("\\@eqnsw") == "1")
			}
		}
	}
	beginRow()
	hLinesBeforeRow = append(hLinesBeforeRow, tmGetHLines(parser))
	for {
		brk := "\\\\"
		if p.singleRow {
			brk = "\\end"
		}
		cellBody := parser.parseExpression(false, brk, false)
		parser.gullet.endGroup()
		parser.gullet.beginGroup()
		cell := tmObj{"type": "ordgroup", "mode": parser.mode, "body": cellBody, "semisimple": true}
		row.cells = append(row.cells, cell)
		next := parser.fetch().text
		if next == "&" {
			if p.maxNumCols != 0 && len(row.cells) == p.maxNumCols {
				if tmContains(p.envClasses, "array") {
					if parser.settings.strict {
						tmThrow("Too few columns specified in the {array} column argument.", parser.nextToken)
					}
				} else if p.maxNumCols == 2 {
					tmThrow("The split environment accepts no more than two columns", parser.nextToken)
				} else {
					tmThrow("The equation environment accepts only one column", parser.nextToken)
				}
			}
			parser.consume()
		} else if next == endToken {
			endRow()
			if len(row.cells) == 1 && len(cellBody) == 0 && (len(body) > 1 || !p.emptySingleRow) {
				body = body[:len(body)-1]
			}
			labels = append(labels, tmCheckCellForLabels(cellBody))
			if len(hLinesBeforeRow) < len(body)+1 {
				hLinesBeforeRow = append(hLinesBeforeRow, []bool{})
			}
			break
		} else if next == "\\\\" {
			parser.consume()
			var size tmObj
			if parser.gullet.future().text != " " {
				size = parser.parseSizeGroup(true)
			}
			if size != nil {
				rowGaps = append(rowGaps, size["value"])
			} else {
				rowGaps = append(rowGaps, nil)
			}
			endRow()
			labels = append(labels, tmCheckCellForLabels(cellBody))
			hLinesBeforeRow = append(hLinesBeforeRow, tmGetHLines(parser))
			row = &tmRow{}
			body = append(body, row)
			beginRow()
		} else {
			tmThrow("Expected & or \\\\ or \\cr or "+endToken, parser.nextToken)
		}
	}
	parser.gullet.endGroup()
	parser.gullet.endGroup()
	var tagsOut any
	if hasTags {
		tagsOut = tags
	}
	return tmObj{"type": "array", "mode": parser.mode, "body": body, "cols": p.cols, "rowGaps": rowGaps, "hLinesBeforeRow": hLinesBeforeRow,
		"envClasses": p.envClasses, "autoTag": p.autoTag, "scriptLevel": scriptLevel, "tags": tagsOut, "labels": labels, "leqno": p.leqno,
		"arraystretch": p.arraystretch, "arraycolsep": p.arraycolsep}
}

func tmDCellStyle(envName string) string {
	if strings.HasPrefix(envName, "d") {
		return "display"
	}
	return "text"
}

var tmAlignMap = map[string]string{"c": "center ", "l": "left ", "r": "right "}

func tmArrayGlue(envClasses []string) *mathNode {
	glueNode := mn("mtd")
	glueNode.style = tmNewOrd()
	glueNode.style.set("padding", "0")
	glueNode.style.set("width", "50%")
	if tmContains(envClasses, "multline") {
		glueNode.style.set("width", "7.5%")
	}
	return glueNode
}

func tmArrayBuilder(group tmObj, style *tmStyle) mmNode {
	var tbl []*mathNode
	bodyRows := tmList(group["body"])
	numRows := len(bodyRows)
	hlines, _ := group["hLinesBeforeRow"].([][]bool)
	envClasses, _ := group["envClasses"].([]string)
	tags, _ := group["tags"].([]any)
	tagIsPresent := false
	for _, t := range tags {
		if tmTruthy2(t) {
			tagIsPresent = true
			break
		}
	}
	scriptLevel := tmStr(group, "scriptLevel")
	leqno := tmTruthy(group["leqno"])
	labels, _ := group["labels"].([]string)
	for i := 0; i < numRows; i++ {
		rw := bodyRows[i].(*tmRow)
		var row []mmNode
		cellLevel := tmStyleDisplay
		if scriptLevel == "text" {
			cellLevel = tmStyleText
		} else if scriptLevel == "script" {
			cellLevel = tmStyleScript
		}
		for j := range rw.cells {
			mtd := mn("mtd", tmBuildGroup(rw.cells[j], style.withLevel(cellLevel)))
			if tmContains(envClasses, "multline") {
				align := "center"
				if i == 0 {
					align = "left"
				} else if i == numRows-1 {
					align = "right"
				}
				if align != "center" {
					mtd.classes = append(mtd.classes, "tml-"+align)
				}
			}
			row = append(row, mtd)
		}
		numColumns := len(bodyRows[0].(*tmRow).cells)
		for k := 0; k < numColumns-len(rw.cells); k++ {
			row = append(row, newMathNode("mtd", nil, []string{}, style.o))
		}
		if tagIsPresent {
			var tagElement mmNode
			switch tag := tags[i].(type) {
			case bool:
				if tag {
					tagElement = mn("mtext", newSpan([]string{"tml-eqn"}, nil))
				} else {
					tagElement = newMathNode("mtext", nil, []string{}, nil)
				}
			default:
				tl := tmList(tag)
				te := tmBuildExpressionRow(tmList(tl[0].(tmObj)["body"]), style.withLevel(cellLevel), true)
				te = tmConsolidateText(te)
				*te.classesPtr() = []string{"tml-tag"}
				tagElement = te
			}
			if tagElement != nil {
				row = append([]mmNode{tmArrayGlue(envClasses)}, row...)
				row = append(row, tmArrayGlue(envClasses))
				if leqno {
					c := row[0].childrenPtr()
					*c = append(*c, tagElement)
				} else {
					c := row[len(row)-1].childrenPtr()
					*c = append(*c, tagElement)
				}
			}
		}
		mtr := newMathNode("mtr", row, []string{}, nil)
		var label string
		if len(labels) > 0 {
			label, labels = labels[0], labels[1:]
		}
		if label != "" && tags != nil && i < len(tags) && tmTruthy2(tags[i]) {
			mtr.setAttribute("id", label)
			if _, isArr := tags[i].([]any); isArr {
				mtr.classes = append(mtr.classes, "tml-tageqn")
			}
		}
		cells := func() []mmNode { return mtr.children }
		if i == 0 && len(hlines[0]) > 0 {
			if len(hlines[0]) == 2 {
				for _, c := range cells() {
					mmStyle(c).set("borderTop", "0.15em double")
				}
			} else {
				v := "0.06em solid"
				if hlines[0][0] {
					v = "0.06em dashed"
				}
				for _, c := range cells() {
					mmStyle(c).set("borderTop", v)
				}
			}
		}
		if len(hlines[i+1]) > 0 {
			if len(hlines[i+1]) == 2 {
				for _, c := range cells() {
					mmStyle(c).set("borderBottom", "0.15em double")
				}
			} else {
				v := "0.06em solid"
				if hlines[i+1][0] {
					v = "0.06em dashed"
				}
				for _, c := range cells() {
					mmStyle(c).set("borderBottom", v)
				}
			}
		}
		mustSquashRow := true
		for _, c := range mtr.children {
			var child mmNode
			if cs := mmChildren(c); len(cs) > 0 {
				child = cs[0]
			}
			if !(child != nil && mmType(child) == "mpadded" && child.attrsOf().get("height") == "0px") {
				mustSquashRow = false
				break
			}
		}
		if mustSquashRow {
			mtr.classes = append(mtr.classes, "ff-squash")
			for _, c := range mtr.children {
				mmStyle(c).set("paddingTop", "0")
				mmStyle(c).set("paddingBottom", "0")
			}
		}
		tbl = append(tbl, mtr)
	}
	if as, ok := group["arraystretch"].(float64); ok && as != 0 && as != 1 {
		pad := jsNumberString(1.4*as-0.8) + "ex"
		for _, r := range tbl {
			for _, c := range r.children {
				mmStyle(c).set("paddingTop", pad)
				mmStyle(c).set("paddingBottom", pad)
			}
		}
	}
	var sidePadding, sidePadUnit string
	if len(envClasses) > 0 {
		switch {
		case tmContains(envClasses, "abut"), tmContains(envClasses, "cases"):
			sidePadding = "0"
		case tmContains(envClasses, "small"):
			sidePadding = "0.1389"
		case tmContains(envClasses, "cd"):
			sidePadding = "0.25"
		default:
			sidePadding = "0.4"
		}
		sidePadUnit = "em"
	}
	if tmTruthy(group["arraycolsep"]) {
		arraySidePad := tmCalculateSize(tmSizeOf(group["arraycolsep"]), style)
		sidePadding = tmToFixed(arraySidePad.number, 4)
		sidePadUnit = arraySidePad.unit
	}
	if sidePadding != "" {
		numCols := 0
		if len(tbl) > 0 {
			numCols = len(tbl[0].children)
		}
		sidePad := func(j, hand int) string {
			if j == 0 && hand == 0 {
				return "0"
			}
			if j == numCols-1 && hand == 1 {
				return "0"
			}
			if len(envClasses) == 0 || envClasses[0] != "align" {
				return sidePadding
			}
			if hand == 1 {
				return "0"
			}
			if tagIsPresent {
				if j%2 == 1 {
					return "1"
				}
				return "0"
			}
			if j%2 == 1 {
				return "0"
			}
			return "1"
		}
		for _, r := range tbl {
			for j, c := range r.children {
				mmStyle(c).set("paddingLeft", sidePad(j, 0)+sidePadUnit)
				mmStyle(c).set("paddingRight", sidePad(j, 1)+sidePadUnit)
			}
		}
	}
	if len(envClasses) == 0 {
		for _, r := range tbl {
			mmStyle(r.children[0]).set("paddingLeft", "0em")
			if len(r.children) == len(tbl[0].children) {
				mmStyle(r.children[len(r.children)-1]).set("paddingRight", "0em")
			}
		}
	}
	if len(envClasses) > 0 {
		align := tmContains(envClasses, "align") || tmContains(envClasses, "alignat")
		for _, row := range tbl {
			if align {
				for j, c := range row.children {
					side := "right"
					if j%2 == 1 {
						side = "left"
					}
					*c.classesPtr() = []string{"tml-" + side}
				}
				if tagIsPresent {
					k := len(row.children) - 1
					if leqno {
						k = 0
					}
					*row.children[k].classesPtr() = []string{}
				}
			}
			if len(row.children) > 1 && tmContains(envClasses, "cases") {
				mmStyle(row.children[1]).set("paddingLeft", "1em")
			}
			if tmContains(envClasses, "cases") || tmContains(envClasses, "subarray") {
				for _, c := range row.children {
					mmPushClass(c, "tml-left")
				}
			}
		}
	}
	tblNodes := make([]mmNode, len(tbl))
	for i, r := range tbl {
		tblNodes[i] = r
	}
	table := newMathNode("mtable", tblNodes, nil, nil)
	if len(envClasses) > 0 {
		if tmContains(envClasses, "jot") {
			table.classes = append(table.classes, "tml-jot")
		} else if tmContains(envClasses, "small") {
			table.classes = append(table.classes, "tml-small")
		}
	}
	if scriptLevel == "display" {
		table.setAttribute("displaystyle", "true")
	}
	if tmTruthy(group["autoTag"]) || tmContains(envClasses, "multline") {
		table.style.set("width", "100%")
	}
	if cols := tmList(group["cols"]); len(cols) > 0 {
		prevTypeWasAlign := false
		iStart := 0
		iEnd := len(cols)
		for cols[iStart].(tmObj)["type"] == "separator" {
			iStart++
		}
		for cols[iEnd-1].(tmObj)["type"] == "separator" {
			iEnd--
		}
		if cols[0].(tmObj)["type"] == "separator" {
			var sep string
			if cols[1].(tmObj)["type"] == "separator" {
				sep = "0.15em double"
			} else if cols[0].(tmObj)["separator"] == "|" {
				sep = "0.06em solid "
			} else {
				sep = "0.06em dashed "
			}
			for _, row := range table.children {
				mmStyle(mmChildren(row)[0]).set("borderLeft", sep)
			}
		}
		iCol := -1
		if tagIsPresent {
			iCol = 0
		}
		for i := iStart; i < iEnd; i++ {
			c := cols[i].(tmObj)
			if c["type"] == "align" {
				colAlign := tmAlignMap[tmStr(c, "align")]
				iCol++
				for _, row := range table.children {
					rc := mmChildren(row)
					if strings.TrimSpace(colAlign) != "center" && iCol < len(rc) {
						*rc[iCol].classesPtr() = []string{"tml-" + strings.TrimSpace(colAlign)}
					}
				}
				prevTypeWasAlign = true
			} else if c["type"] == "separator" {
				if prevTypeWasAlign {
					var sep string
					if cols[i+1].(tmObj)["type"] == "separator" {
						sep = "0.15em double"
					} else if c["separator"] == "|" {
						sep = "0.06em solid"
					} else {
						sep = "0.06em dashed"
					}
					for _, row := range table.children {
						rc := mmChildren(row)
						if iCol < len(rc) {
							mmStyle(rc[iCol]).set("borderRight", sep)
						}
					}
				}
				prevTypeWasAlign = false
			}
		}
		if last := cols[len(cols)-1].(tmObj); last["type"] == "separator" {
			var sep string
			if cols[len(cols)-2].(tmObj)["type"] == "separator" {
				sep = "0.15em double"
			} else if last["separator"] == "|" {
				sep = "0.06em solid"
			} else {
				sep = "0.06em dashed"
			}
			for _, row := range table.children {
				rc := mmChildren(row)
				mmStyle(rc[len(rc)-1]).set("borderRight", sep)
				mmStyle(rc[len(rc)-1]).set("paddingRight", "0.4em")
			}
		}
	}
	var result mmNode = table
	if tmContains(envClasses, "small") {
		w := mn("mstyle", table)
		w.setAttribute("scriptlevel", "1")
		result = w
	}
	return result
}

// tmTruthy2 is the truthiness of a tag entry: true, or an array (arrays are truthy even when empty).
func tmTruthy2(t any) bool {
	switch x := t.(type) {
	case bool:
		return x
	case []any:
		return true
	}
	return t != nil
}

func tmAlignedHandler(ctx *tmCtx, args, opt []any) any {
	if !strings.Contains(ctx.envName, "ed") {
		tmValidateAmsEnvironmentContext(ctx)
	}
	isSplit := ctx.envName == "split"
	var autoTag any
	if !isSplit {
		autoTag = tmGetAutoTag(ctx.envName)
	}
	maxNumCols := 0
	if ctx.envName == "split" {
		maxNumCols = 2
	}
	res := tmParseArray(ctx.parser, tmArrayParams{cols: []any{}, emptySingleRow: true, autoTag: autoTag, envClasses: []string{"abut", "jot"},
		maxNumCols: maxNumCols, leqno: ctx.parser.settings.leqno}, "display")
	var numMaths float64
	numCols := 0
	isAlignedAt := strings.Contains(ctx.envName, "at")
	if len(args) > 0 && args[0] != nil && isAlignedAt {
		arg0 := ""
		for _, n := range tmList(args[0].(tmObj)["body"]) {
			arg0 += tmStr(tmAssertNodeType(n, "textord"), "text")
		}
		if tmIsNaNString(arg0) {
			tmThrowf("The alignat enviroment requires a numeric first argument.")
		}
		numMaths, _ = tmParseJSNumber(jsTrim(arg0))
		numCols = int(numMaths * 2)
	}
	for _, r := range tmList(res["body"]) {
		row := r.(*tmRow)
		if isAlignedAt {
			curMaths := float64(len(row.cells)) / 2
			if numMaths < curMaths {
				tmThrow("Too many math in a row: expected "+jsNumberString(numMaths)+", but got "+jsNumberString(curMaths), row.cells[0])
			}
		} else if numCols < len(row.cells) {
			numCols = len(row.cells)
		}
	}
	cols := make([]any, numCols)
	for i := 0; i < numCols; i++ {
		align := "r"
		if i%2 == 1 {
			align = "l"
		}
		cols[i] = tmObj{"type": "align", "align": align}
	}
	res["cols"] = cols
	envClasses := res["envClasses"].([]string)
	if isSplit {
	} else if isAlignedAt {
		res["envClasses"] = append(envClasses, "alignat")
	} else {
		envClasses[0] = "align"
	}
	return res
}

func tmColumnSpec(args []any, allowed string) []any {
	var colalign []any
	if tmCheckSymbolNodeType(args[0]) != nil {
		colalign = []any{args[0]}
	} else {
		colalign = tmList(tmAssertNodeType(args[0], "ordgroup")["body"])
	}
	cols := make([]any, 0, len(colalign))
	for _, nde := range colalign {
		node := tmAssertSymbolNodeType(nde)
		ca := tmStr(node, "text")
		if strings.Contains(allowed, ca) {
			cols = append(cols, tmObj{"type": "align", "align": ca})
		} else if allowed == "lcr" && ca == "|" {
			cols = append(cols, tmObj{"type": "separator", "separator": "|"})
		} else if allowed == "lcr" && ca == ":" {
			cols = append(cols, tmObj{"type": "separator", "separator": ":"})
		} else {
			tmThrow("Unknown column alignment: "+ca, nde)
		}
	}
	return cols
}

func tmFillCols(n int, align string) []any {
	cols := make([]any, n)
	shared := tmObj{"type": "align", "align": align}
	for i := range cols {
		cols[i] = shared
	}
	return cols
}

func init() {
	tmDefineEnvironment("array", []string{"array", "darray"}, 1,
		func(ctx *tmCtx, args, opt []any) any {
			cols := tmColumnSpec(args, "lcr")
			arraystretch, arraycolsep := tmArrayGaps(ctx.parser.gullet.macros)
			return tmParseArray(ctx.parser, tmArrayParams{cols: cols, envClasses: []string{"array"}, maxNumCols: len(cols), arraystretch: arraystretch,
				arraycolsep: arraycolsep}, tmDCellStyle(ctx.envName))
		}, tmArrayBuilder)
	tmDefineEnvironment("array", []string{"matrix", "pmatrix", "bmatrix", "Bmatrix", "vmatrix", "Vmatrix", "matrix*", "pmatrix*", "bmatrix*", "Bmatrix*", "vmatrix*", "Vmatrix*"}, 0,
		func(ctx *tmCtx, args, opt []any) any {
			delims := map[string][]string{"pmatrix": {"(", ")"}, "bmatrix": {"[", "]"}, "Bmatrix": {"\\{", "\\}"}, "vmatrix": {"|", "|"}, "Vmatrix": {"\\Vert", "\\Vert"}}
			delimiters := delims[strings.Replace(ctx.envName, "*", "", 1)]
			colAlign := "c"
			payload := tmArrayParams{envClasses: []string{}, cols: []any{}}
			if strings.HasSuffix(ctx.envName, "*") {
				parser := ctx.parser
				parser.consumeSpaces()
				if parser.fetch().text == "[" {
					parser.consume()
					parser.consumeSpaces()
					colAlign = parser.fetch().text
					if !strings.Contains("lcr", colAlign) {
						tmThrow("Expected l or c or r", parser.nextToken)
					}
					parser.consume()
					parser.consumeSpaces()
					parser.expect("]", true)
					parser.consume()
					payload.cols = []any{}
				}
			}
			res := tmParseArray(ctx.parser, payload, "text")
			body := tmList(res["body"])
			if len(body) > 0 {
				res["cols"] = tmFillCols(len(body[0].(*tmRow).cells), colAlign)
			} else {
				res["cols"] = []any{}
			}
			arraystretch, arraycolsep := tmArrayGaps(ctx.parser.gullet.macros)
			res["arraystretch"] = arraystretch
			if tmTruthy(arraycolsep) {
				res["arraycolsep"] = arraycolsep
			}
			if delimiters != nil {
				return tmObj{"type": "leftright", "mode": ctx.mode, "body": []any{res}, "left": delimiters[0], "right": delimiters[1], "rightColor": nil}
			}
			return res
		}, tmArrayBuilder)
	tmDefineEnvironment("array", []string{"bordermatrix"}, 0,
		func(ctx *tmCtx, args, opt []any) any {
			res := tmParseArray(ctx.parser, tmArrayParams{cols: []any{}, envClasses: []string{"bordermatrix"}}, "text")
			body := tmList(res["body"])
			if len(body) > 0 {
				res["cols"] = tmFillCols(len(body[0].(*tmRow).cells), "c")
			} else {
				res["cols"] = []any{}
			}
			res["envClasses"] = []string{}
			res["arraystretch"] = 1.0
			if ctx.envName == "matrix" {
				return res
			}
			return tmBordermatrixParseTree(res, ctx.delimiters)
		}, tmArrayBuilder)
	tmDefineEnvironment("array", []string{"smallmatrix"}, 0,
		func(ctx *tmCtx, args, opt []any) any {
			return tmParseArray(ctx.parser, tmArrayParams{envClasses: []string{"small"}}, "script")
		}, tmArrayBuilder)
	tmDefineEnvironment("array", []string{"subarray"}, 1,
		func(ctx *tmCtx, args, opt []any) any {
			cols := tmColumnSpec(args, "lc")
			if len(cols) > 1 {
				tmThrowf("{subarray} can contain only one column")
			}
			res := tmParseArray(ctx.parser, tmArrayParams{cols: cols, envClasses: []string{"small"}}, "script")
			if body := tmList(res["body"]); len(body) > 0 && len(body[0].(*tmRow).cells) > 1 {
				tmThrowf("{subarray} can contain only one column")
			}
			return res
		}, tmArrayBuilder)
	tmDefineEnvironment("array", []string{"cases", "dcases", "rcases", "drcases"}, 0,
		func(ctx *tmCtx, args, opt []any) any {
			res := tmParseArray(ctx.parser, tmArrayParams{cols: []any{}, envClasses: []string{"cases"}}, tmDCellStyle(ctx.envName))
			left, right := "\\{", "."
			if strings.Contains(ctx.envName, "r") {
				left, right = ".", "\\}"
			}
			return tmObj{"type": "leftright", "mode": ctx.mode, "body": []any{res}, "left": left, "right": right, "rightColor": nil}
		}, tmArrayBuilder)
	tmDefineEnvironment("array", []string{"align", "align*", "aligned", "split"}, 0, tmAlignedHandler, tmArrayBuilder)
	tmDefineEnvironment("array", []string{"alignat", "alignat*", "alignedat"}, 1, tmAlignedHandler, tmArrayBuilder)
	tmDefineEnvironment("array", []string{"gathered", "gather", "gather*"}, 0,
		func(ctx *tmCtx, args, opt []any) any {
			if ctx.envName != "gathered" {
				tmValidateAmsEnvironmentContext(ctx)
			}
			return tmParseArray(ctx.parser, tmArrayParams{cols: []any{}, envClasses: []string{"abut", "jot"}, autoTag: tmGetAutoTag(ctx.envName), emptySingleRow: true,
				leqno: ctx.parser.settings.leqno}, "display")
		}, tmArrayBuilder)
	tmDefineEnvironment("array", []string{"equation", "equation*"}, 0,
		func(ctx *tmCtx, args, opt []any) any {
			tmValidateAmsEnvironmentContext(ctx)
			return tmParseArray(ctx.parser, tmArrayParams{autoTag: tmGetAutoTag(ctx.envName), emptySingleRow: true, singleRow: true, maxNumCols: 1,
				envClasses: []string{"align"}, leqno: ctx.parser.settings.leqno}, "display")
		}, tmArrayBuilder)
	tmDefineEnvironment("array", []string{"multline", "multline*"}, 0,
		func(ctx *tmCtx, args, opt []any) any {
			tmValidateAmsEnvironmentContext(ctx)
			return tmParseArray(ctx.parser, tmArrayParams{autoTag: ctx.envName == "multline", maxNumCols: 1, envClasses: []string{"jot", "multline"},
				leqno: ctx.parser.settings.leqno}, "display")
		}, tmArrayBuilder)
	tmDefineEnvironment("array", []string{"CD"}, 0,
		func(ctx *tmCtx, args, opt []any) any {
			tmValidateAmsEnvironmentContext(ctx)
			return tmParseCD(ctx.parser)
		}, tmArrayBuilder)
	tmDefineFunction("text", []string{"\\hline", "\\hdashline"}, tmProps{numArgs: 0, allowedInText: true},
		func(ctx *tmCtx, args, opt []any) any {
			tmThrowf(ctx.funcName + " valid only within array environment")
			return nil
		}, nil)

	// cd.js
	tmDefineFunction("cdlabel", []string{"\\\\cdleft", "\\\\cdright"}, tmProps{numArgs: 1},
		func(ctx *tmCtx, args, opt []any) any {
			return tmObj{"type": "cdlabel", "mode": ctx.parser.mode, "side": ctx.funcName[4:], "label": args[0]}
		},
		func(group tmObj, style *tmStyle) mmNode {
			label := group["label"].(tmObj)
			if len(tmList(label["body"])) == 0 {
				return mn("mrow")
			}
			mrow := tmBuildGroup(label, style)
			if tmStr(group, "side") == "left" {
				mmPushClass(mrow, "tml-shift-left")
			}
			mtd := mn("mtd", mrow)
			mtd.style.set("padding", "0")
			mtr := mn("mtr", mtd)
			mtable := mn("mtable", mtr)
			lbl := mn("mpadded", mtable)
			lbl.setAttribute("width", "0.1px")
			lbl.setAttribute("displaystyle", "false")
			lbl.setAttribute("scriptlevel", "1")
			return lbl
		})
	tmDefineFunction("cdlabelparent", []string{"\\\\cdparent"}, tmProps{numArgs: 1},
		func(ctx *tmCtx, args, opt []any) any {
			return tmObj{"type": "cdlabelparent", "mode": ctx.parser.mode, "fragment": args[0]}
		},
		func(group tmObj, style *tmStyle) mmNode {
			return mn("mrow", tmBuildGroup(group["fragment"], style))
		})
}

// ---- cd.js -------------------------------------------------------------------------------------------------------

var tmCdArrowFunctionName = map[string]string{">": "\\\\cdrightarrow", "<": "\\\\cdleftarrow", "=": "\\\\cdlongequal", "A": "\\uparrow", "V": "\\downarrow", "|": "\\Vert", ".": "no arrow"}

func tmCdNewCell() tmObj {
	return tmObj{"type": "styling", "body": []any{}, "mode": "math", "scriptLevel": "display"}
}

func tmCdArrow(arrowChar string, labels [2]tmObj, parser *tmParser) any {
	funcName := tmCdArrowFunctionName[arrowChar]
	switch funcName {
	case "\\\\cdrightarrow", "\\\\cdleftarrow":
		return parser.callFunction(funcName, []any{labels[0]}, []any{labels[1]}, nil, "")
	case "\\uparrow", "\\downarrow":
		leftLabel := parser.callFunction("\\\\cdleft", []any{labels[0]}, nil, nil, "")
		bareArrow := tmObj{"type": "atom", "text": funcName, "mode": "math", "family": "rel"}
		sizedArrow := parser.callFunction("\\Big", []any{bareArrow}, nil, nil, "")
		rightLabel := parser.callFunction("\\\\cdright", []any{labels[1]}, nil, nil, "")
		arrowGroup := tmObj{"type": "ordgroup", "mode": "math", "body": []any{leftLabel, sizedArrow, rightLabel}, "semisimple": true}
		return parser.callFunction("\\\\cdparent", []any{arrowGroup}, nil, nil, "")
	case "\\\\cdlongequal":
		return parser.callFunction("\\\\cdlongequal", nil, nil, nil, "")
	case "\\Vert":
		arrow := tmObj{"type": "textord", "text": "\\Vert", "mode": "math"}
		return parser.callFunction("\\Big", []any{arrow}, nil, nil, "")
	}
	return tmObj{"type": "textord", "text": " ", "mode": "math"}
}

func tmCdIsStartOfArrow(n any) bool {
	node := n.(tmObj)
	return tmStr(node, "type") == "textord" && tmStr(node, "text") == "@"
}

func tmParseCD(parser *tmParser) tmObj {
	var parsedRows [][]any
	parser.gullet.beginGroup()
	parser.gullet.macros.set("\\cr", "\\\\\\relax", false)
	parser.gullet.beginGroup()
	for {
		parsedRows = append(parsedRows, parser.parseExpression(false, "\\\\", false))
		parser.gullet.endGroup()
		parser.gullet.beginGroup()
		next := parser.fetch().text
		if next == "&" || next == "\\\\" {
			parser.consume()
		} else if next == "\\end" {
			if len(parsedRows[len(parsedRows)-1]) == 0 {
				parsedRows = parsedRows[:len(parsedRows)-1]
			}
			break
		} else {
			tmThrow("Expected \\\\ or \\cr or \\end", parser.nextToken)
		}
	}
	row := &tmRow{}
	body := []any{row}
	for i, rowNodes := range parsedRows {
		cell := tmCdNewCell()
		for j := 0; j < len(rowNodes); j++ {
			if !tmCdIsStartOfArrow(rowNodes[j]) {
				cell["body"] = append(tmList(cell["body"]), rowNodes[j])
				continue
			}
			row.cells = append(row.cells, cell)
			j++
			arrowChar := tmStr(tmAssertSymbolNodeType(rowNodes[j]), "text")
			labels := [2]tmObj{{"type": "ordgroup", "mode": "math", "body": []any{}}, {"type": "ordgroup", "mode": "math", "body": []any{}}}
			if strings.Contains("=|.", arrowChar) {
			} else if strings.Contains("<>AV", arrowChar) {
				for labelNum := 0; labelNum < 2; labelNum++ {
					inLabel := true
					for k := j + 1; k < len(rowNodes); k++ {
						n := rowNodes[k].(tmObj)
						if (tmStr(n, "type") == "mathord" || tmStr(n, "type") == "atom") && tmStr(n, "text") == arrowChar {
							inLabel = false
							j = k
							break
						}
						if tmCdIsStartOfArrow(n) {
							tmThrow("Missing a "+arrowChar+" character to complete a CD arrow.", n)
						}
						labels[labelNum]["body"] = append(tmList(labels[labelNum]["body"]), n)
					}
					if inLabel {
						tmThrow("Missing a "+arrowChar+" character to complete a CD arrow.", rowNodes[j])
					}
				}
			} else {
				tmThrowf("Expected one of \"<>AV=|.\" after @.")
			}
			arrow := tmCdArrow(arrowChar, labels, parser)
			row.cells = append(row.cells, arrow)
			cell = tmCdNewCell()
		}
		if i%2 == 0 {
			row.cells = append(row.cells, cell)
		} else if len(row.cells) > 0 {
			row.cells = row.cells[1:]
		}
		row = &tmRow{}
		body = append(body, row)
	}
	body = body[:len(body)-1]
	parser.gullet.endGroup()
	parser.gullet.endGroup()
	hl := make([][]bool, len(body)+1)
	for i := range hl {
		hl[i] = []bool{}
	}
	return tmObj{"type": "array", "mode": "math", "body": body, "tags": nil, "labels": make([]string, len(body)+1), "envClasses": []string{"jot", "cd"}, "cols": []any{},
		"hLinesBeforeRow": hl}
}

// ---- borderTree.js -----------------------------------------------------------------------------------------------

func tmBTOrdGroup(body []any) tmObj {
	return tmObj{"type": "ordgroup", "mode": "math", "body": body, "semisimple": true}
}

func tmBTPhantom(body []any, typ string) tmObj {
	return tmObj{"type": typ, "mode": "math", "body": tmBTOrdGroup(body)}
}

func tmBordermatrixParseTree(matrix tmObj, delimiters []any) tmObj {
	body := tmList(matrix["body"])
	r0 := body[0].(*tmRow)
	r0.cells = r0.cells[1:]
	leftColumnBody := make([]any, len(body)-1)
	for i := range leftColumnBody {
		leftColumnBody[i] = &tmRow{}
	}
	for i := 1; i < len(body); i++ {
		ri := body[i].(*tmRow)
		lc := leftColumnBody[i-1].(*tmRow)
		lc.cells = append(lc.cells, ri.cells[0])
		ri.cells = ri.cells[1:]
		var phantomBody []any
		phantomBody = append(phantomBody, ri.cells...)
		lc.cells = append(lc.cells, tmBTPhantom(phantomBody, "vphantom"))
	}
	topRowBody := make([]any, len(body))
	for i := range topRowBody {
		topRowBody[i] = &tmRow{}
	}
	tr0 := topRowBody[0].(*tmRow)
	tr0.cells = append(tr0.cells, r0.cells...)
	for i := 1; i < len(body); i++ {
		tri := topRowBody[i].(*tmRow)
		ri := body[i].(*tmRow)
		for j := range r0.cells {
			tri.cells = append(tri.cells, tmBTPhantom(tmList(ri.cells[j].(tmObj)["body"]), "hphantom"))
		}
	}
	for j := range r0.cells {
		r0.cells[j] = tmBTPhantom(tmList(r0.cells[j].(tmObj)["body"]), "hphantom")
	}
	if len(leftColumnBody)-1 < 0 {
		panic(&tmJSError{"Invalid array length"})
	}
	emptyHL := func(n int) [][]bool {
		hl := make([][]bool, n)
		for i := range hl {
			hl[i] = []bool{}
		}
		return hl
	}
	leftColumn := tmObj{"type": "array", "mode": "math", "body": leftColumnBody, "cols": []any{tmObj{"type": "align", "align": "c"}},
		"rowGaps": make([]any, len(leftColumnBody)-1), "hLinesBeforeRow": emptyHL(len(leftColumnBody) + 1), "envClasses": []string{}, "scriptLevel": "text",
		"arraystretch": 1.0, "labels": make([]string, len(leftColumnBody)), "arraycolsep": tmSize{0.04, "em"}}
	topRow := tmObj{"type": "array", "mode": "math", "body": topRowBody, "cols": tmFillCols(len(topRowBody), "c"), "rowGaps": make([]any, len(topRowBody)-1),
		"hLinesBeforeRow": emptyHL(len(topRowBody) + 1), "envClasses": []string{}, "scriptLevel": "text", "arraystretch": 1.0, "labels": make([]string, len(topRowBody)),
		"arraycolsep": nil}
	topWrapper := tmObj{"type": "styling", "mode": "math", "scriptLevel": "text", "body": []any{topRow}}
	left, right := any("("), any(")")
	if delimiters != nil {
		left, right = delimiters[0], delimiters[1]
	}
	container := tmObj{"type": "leftright", "mode": "math", "body": []any{matrix}, "left": left, "right": right, "rightColor": nil}
	base := tmObj{"type": "op", "mode": "math", "limits": true, "alwaysHandleSupSub": true, "parentIsSupSub": true, "symbol": false, "suppressBaseShift": true,
		"body": []any{container}}
	mover := tmObj{"type": "supsub", "mode": "math", "stack": true, "base": base, "sup": topWrapper, "sub": nil}
	return tmBTOrdGroup([]any{leftColumn, mover})
}
