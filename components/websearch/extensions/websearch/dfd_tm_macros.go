package websearch

import (
	"strconv"
	"strings"
	"sync"
	"unicode/utf16"
)

// Temml 0.13.4 (MIT): the macros of src/macros.js that are defined by a function (the ones defined by a string are
// in dfd_temml_tables.go).

var tmDigitToNumber = func() map[byte]float64 {
	m := map[byte]float64{}
	for i := 0; i < 10; i++ {
		m[byte('0'+i)] = float64(i)
	}
	for i := 0; i < 6; i++ {
		m[byte('a'+i)] = float64(10 + i)
		m[byte('A'+i)] = float64(10 + i)
	}
	return m
}()

// tmDigitAt is digitToNumber[str.charAt(i)]: ok is false for undefined.
func tmDigitAt(str string, i int) (float64, bool) {
	u := tmUTF16Units(str)
	if i >= len(u) || u[i] > 0x7f {
		return 0, false
	}
	d, ok := tmDigitToNumber[byte(u[i])]
	return d, ok
}

func tmNextCharNumber(g *tmGullet) (float64, bool, string) {
	numStr := g.future().text
	if numStr == "EOF" {
		return 0, false, ""
	}
	d, ok := tmDigitAt(numStr, 0)
	return d, ok, numStr
}

func tmAppendCharNumbers(number float64, numStr string, base float64) float64 {
	n := len(tmUTF16Units(numStr))
	for i := 1; i < n; i++ {
		d, ok := tmDigitAt(numStr, i)
		number *= base
		if ok {
			number += d
		} else {
			number = tmNaN()
		}
	}
	return number
}

func tmNaN() float64 { f, _ := strconv.ParseFloat("NaN", 64); return f }

func tmRecreateArgStr(g *tmGullet) string {
	tokens := g.consumeArgs(1, nil)[0]
	if len(tokens) == 0 {
		panic(&tmJSError{"Cannot read properties of undefined (reading 'loc')"})
	}
	var b strings.Builder
	expectedLoc := tokens[len(tokens)-1].loc.start
	for i := len(tokens) - 1; i >= 0; i-- {
		actualLoc := tokens[i].loc.start
		if actualLoc > expectedLoc {
			b.WriteString(" ")
			expectedLoc = actualLoc
		}
		b.WriteString(tokens[i].text)
		expectedLoc += jsLen(tokens[i].text)
	}
	return b.String()
}

// tmFindVert is the match of /\|\||\||\\\|/ at or after the UTF-16 index from: its index and text, or -1.
func tmFindVert(u []uint16, from int) (int, string) {
	for p := from; p < len(u); p++ {
		switch {
		case u[p] == '|' && p+1 < len(u) && u[p+1] == '|':
			return p, "||"
		case u[p] == '|':
			return p, "|"
		case u[p] == '\\' && p+1 < len(u) && u[p+1] == '|':
			return p, "\\|"
		}
	}
	return -1, ""
}

func tmReplaceVert(argStr string, idx int, m string) string {
	ch := "\\Vert"
	if m == "|" {
		ch = "\\vert"
	}
	u := tmUTF16Units(argStr)
	return tmU16s(u[:idx]) + "}\\,\\middle" + ch + "\\,{" + tmU16s(u[idx+len(tmUTF16Units(m)):])
}

func tmPdvHelper(args [][]*tmToken) (string, string, string) {
	if len(args[0]) == 0 {
		panic(&tmJSError{"Cannot read properties of undefined (reading 'text')"})
	}
	numerator := args[0][0].text
	denoms := strings.Split(tmStringFromArg(args[1]), ",")
	power := strconv.Itoa(len(denoms))
	numOp := "\\partial"
	if power != "1" {
		numOp = "\\partial^" + power
	}
	denominator := ""
	for _, e := range denoms {
		denominator += "\\partial " + jsTrim(e) + "\\,"
	}
	denominator = strings.TrimSuffix(denominator, "\\,")
	return numerator, numOp, denominator
}

var tmDotsByToken = map[string]string{
	",": "\\dotsc", "\\not": "\\dotsb", "+": "\\dotsb", "=": "\\dotsb", "<": "\\dotsb", ">": "\\dotsb", "-": "\\dotsb", "*": "\\dotsb", ":": "\\dotsb",
	"\\DOTSB": "\\dotsb", "\\coprod": "\\dotsb", "\\bigvee": "\\dotsb", "\\bigwedge": "\\dotsb", "\\biguplus": "\\dotsb", "\\bigcap": "\\dotsb",
	"\\bigcup": "\\dotsb", "\\prod": "\\dotsb", "\\sum": "\\dotsb", "\\bigotimes": "\\dotsb", "\\bigoplus": "\\dotsb", "\\bigodot": "\\dotsb",
	"\\bigsqcap": "\\dotsb", "\\bigsqcup": "\\dotsb", "\\bigtimes": "\\dotsb", "\\And": "\\dotsb", "\\longrightarrow": "\\dotsb", "\\Longrightarrow": "\\dotsb",
	"\\longleftarrow": "\\dotsb", "\\Longleftarrow": "\\dotsb", "\\longleftrightarrow": "\\dotsb", "\\Longleftrightarrow": "\\dotsb", "\\mapsto": "\\dotsb",
	"\\longmapsto": "\\dotsb", "\\hookrightarrow": "\\dotsb", "\\doteq": "\\dotsb", "\\mathbin": "\\dotsb", "\\mathrel": "\\dotsb", "\\relbar": "\\dotsb",
	"\\Relbar": "\\dotsb", "\\xrightarrow": "\\dotsb", "\\xleftarrow": "\\dotsb", "\\DOTSI": "\\dotsi", "\\int": "\\dotsi", "\\oint": "\\dotsi",
	"\\iint": "\\dotsi", "\\iiint": "\\dotsi", "\\iiiint": "\\dotsi", "\\DOTSX": "\\dotsx",
}

var tmSpaceAfterDots = map[string]bool{
	")": true, "]": true, "\\rbrack": true, "\\}": true, "\\rbrace": true, "\\rangle": true, "\\rceil": true, "\\rfloor": true, "\\rgroup": true,
	"\\rmoustache": true, "\\right": true, "\\bigr": true, "\\biggr": true, "\\Bigr": true, "\\Biggr": true, "$": true, ";": true, ".": true, ",": true,
}

var tmMacroTable map[string]any
var tmMacroOnce sync.Once

func tmBuiltinMacros() map[string]any {
	tmMacroOnce.Do(func() {
		m := make(map[string]any, len(tmStringMacros)+20)
		for k, v := range tmStringMacros {
			m[k] = v
		}
		// the macros dist/temml.cjs adds after src/macros.js override same-named ones, as defineMacro does
		for k, v := range tmDistMacros {
			m[k] = v
		}
		fn := func(f func(g *tmGullet) any) tmMacroFunc { return f }
		def := func(tokens []*tmToken) *tmMacroDef { return &tmMacroDef{tokens: tokens} }
		m["\\noexpand"] = fn(func(g *tmGullet) any {
			t := g.popToken()
			if g.isExpandable(t.text) {
				t.noexpand = true
				t.treatAsRelax = true
			}
			return def([]*tmToken{t})
		})
		m["\\expandafter"] = fn(func(g *tmGullet) any {
			t := g.popToken()
			g.expandOnce(true)
			return def([]*tmToken{t})
		})
		m["\\@firstoftwo"] = fn(func(g *tmGullet) any { return def(g.consumeArgs(2, nil)[0]) })
		m["\\@secondoftwo"] = fn(func(g *tmGullet) any { return def(g.consumeArgs(2, nil)[1]) })
		m["\\@ifnextchar"] = fn(func(g *tmGullet) any {
			args := g.consumeArgs(3, nil)
			g.consumeSpaces()
			next := g.future()
			if len(args[0]) == 1 && args[0][0].text == next.text {
				return def(args[1])
			}
			return def(args[2])
		})
		m["\\TextOrMath"] = fn(func(g *tmGullet) any {
			args := g.consumeArgs(2, nil)
			if g.mode == "text" {
				return def(args[0])
			}
			return def(args[1])
		})
		m["\\char"] = fn(func(g *tmGullet) any {
			token := g.popToken()
			var base float64
			var number float64
			if token.text == "'" {
				base = 8
				token = g.popToken()
			} else if token.text == "\"" {
				base = 16
				token = g.popToken()
			} else if token.text == "`" {
				token = g.popToken()
				u := tmUTF16Units(token.text)
				if len(u) > 0 && u[0] == '\\' {
					if len(u) > 1 {
						number = float64(u[1])
					} else {
						number = tmNaN()
					}
				} else if token.text == "EOF" {
					tmThrowf("\\char` missing argument")
				} else {
					number = float64(u[0])
				}
			} else {
				base = 10
			}
			if base != 0 {
				numStr := token.text
				d, ok := tmDigitAt(numStr, 0)
				if !ok || d >= base {
					tmThrowf("Invalid base-" + jsNumberString(base) + " digit " + token.text)
				}
				number = tmAppendCharNumbers(d, numStr, base)
				digit, dok, ns := tmNextCharNumber(g)
				for dok && digit < base {
					number *= base
					number += digit
					number = tmAppendCharNumbers(number, ns, base)
					g.popToken()
					digit, dok, ns = tmNextCharNumber(g)
				}
			}
			return "\\@char{" + jsNumberString(number) + "}"
		})
		m["\\dots"] = fn(func(g *tmGullet) any {
			thedots := "\\dotso"
			next := g.expandAfterFuture().text
			if v, ok := tmDotsByToken[next]; ok {
				thedots = v
			} else if strings.HasPrefix(next, "\\not") {
				thedots = "\\dotsb"
			} else if sym := tmSymbolsMath[next]; sym != nil {
				if sym.group == "bin" || sym.group == "rel" {
					thedots = "\\dotsb"
				}
			}
			return thedots
		})
		m["\\dotso"] = fn(func(g *tmGullet) any {
			if tmSpaceAfterDots[g.future().text] {
				return "\\ldots\\,"
			}
			return "\\ldots"
		})
		m["\\dotsc"] = fn(func(g *tmGullet) any {
			next := g.future().text
			if tmSpaceAfterDots[next] && next != "," {
				return "\\ldots\\,"
			}
			return "\\ldots"
		})
		m["\\cdots"] = fn(func(g *tmGullet) any {
			if tmSpaceAfterDots[g.future().text] {
				return "\\@cdots\\,"
			}
			return "\\@cdots"
		})
		m["\\tag@literal"] = fn(func(g *tmGullet) any {
			if tmTruthy(g.macros.get("\\df@tag")) {
				tmThrowf("Multiple \\tag")
			}
			return "\\gdef\\df@tag{\\text{#1}}"
		})
		m["\\Braket"] = fn(func(g *tmGullet) any {
			argStr := tmRecreateArgStr(g)
			lastIndex := 0
			for {
				u := tmUTF16Units(argStr)
				idx, match := tmFindVert(u, lastIndex)
				if idx < 0 {
					break
				}
				lastIndex = idx + len(tmUTF16Units(match))
				argStr = tmReplaceVert(argStr, idx, match)
			}
			return "\\left\\langle{" + argStr + "}\\right\\rangle"
		})
		m["\\Set"] = fn(func(g *tmGullet) any {
			argStr := tmRecreateArgStr(g)
			if idx, match := tmFindVert(tmUTF16Units(argStr), 0); idx >= 0 {
				argStr = tmReplaceVert(argStr, idx, match)
			}
			return "\\left\\{\\:{" + argStr + "}\\:\\right\\}"
		})
		m["\\set"] = fn(func(g *tmGullet) any {
			argStr := tmRecreateArgStr(g)
			return "\\{{" + strings.Replace(argStr, "|", "}\\mid{", 1) + "}\\}"
		})
		m["\\pdv@numerator"] = fn(func(g *tmGullet) any {
			numerator, numOp, denominator := tmPdvHelper(g.consumeArgs(2, nil))
			return "\\frac{" + numOp + " " + numerator + "}{" + denominator + "}"
		})
		m["\\pdv@next"] = fn(func(g *tmGullet) any {
			numerator, numOp, denominator := tmPdvHelper(g.consumeArgs(2, nil))
			return "\\frac{" + numOp + "}{" + denominator + "} " + numerator
		})
		m["\\ce"] = fn(func(g *tmGullet) any { return tmChemParse(g.consumeArgs(1, nil)[0], "ce") })
		m["\\pu"] = fn(func(g *tmGullet) any { return tmChemParse(g.consumeArgs(1, nil)[0], "pu") })
		tmMacroTable = m
	})
	return tmMacroTable
}

var _ = utf16.Encode
