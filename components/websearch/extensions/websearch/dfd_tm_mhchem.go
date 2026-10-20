package websearch

import (
	"strings"
)

// Port of the mhchem module that dist/temml.cjs bundles ("Temml mhchem.js", an adaptation of mhchem 3.3.0, MIT / Apache
// 2.0, see the header of the original): the \ce and \pu macros. The state machine tables and the regular expression
// sources are generated (dfd_temml_tables.go); the actions, the pattern functions and the texify stage are ported here.
// Regular expressions run on dfd_jsre.go.

type mhBuf = map[string]any
type mhObj = map[string]any

type mhMatch struct {
	match     any // string, or []any of strings (nil for a group that did not take part)
	remainder string
}

type mhAction func(buf mhBuf, m any, opt string) any

var (
	tmMhRes       = map[string]*jsRegex{}
	tmMhActions   map[string]map[string]mhAction
	tmMhGeneric   map[string]mhAction
	tmMhFuncPatts map[string]func(input string) *mhMatch
)

func mhThrow(msg string) { panic(&tmJSError{msg}) }

func mhRe(src string) *jsRegex {
	if re, ok := tmMhRes[src]; ok {
		return re
	}
	re := tmCompileRe(src)
	tmMhRes[src] = re
	return re
}

// mhMatchRe is input.match(re): the capture strings, their participation flags and the rune index of the end of the
// match; the captures are nil when there is no match.
func mhMatchRe(src string, input string) ([]string, []bool, int) {
	caps, ok, end, matched := mhRe(src).exec([]rune(input))
	if !matched {
		return nil, nil, 0
	}
	return caps, ok, end
}

func mhSub(s string, from int) string {
	r := []rune(s)
	if from >= len(r) {
		return ""
	}
	return string(r[from:])
}

func mhLen(s string) int { return len([]rune(s)) }

func mhTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return x != ""
	case bool:
		return x
	}
	return true
}

func mhStr(v any) string {
	s, _ := v.(string)
	return s
}

func mhConcat(a *[]any, b any) {
	if !mhTruthy(b) {
		return
	}
	if l, ok := b.([]any); ok {
		*a = append(*a, l...)
		return
	}
	*a = append(*a, b)
}

// ---- patterns ---------------------------------------------------------------------------------------------------

func mhPatternMatch(name, input string) *mhMatch {
	if f, ok := tmMhFuncPatts[name]; ok {
		return f(input)
	}
	src, ok := tmMhRegexPatterns[name]
	if !ok {
		mhThrow("mhchem bug P. Please report. (" + name + ")")
	}
	caps, okc, end := mhMatchRe(src, input)
	if caps == nil {
		return nil
	}
	var mm any
	switch {
	case len(caps) > 2 && okc[2] && caps[2] != "":
		mm = []any{mhGroup(caps, okc, 1), caps[2]}
	case len(caps) > 1 && okc[1] && caps[1] != "":
		mm = caps[1]
	default:
		mm = caps[0]
	}
	return &mhMatch{match: mm, remainder: mhSub(input, end)}
}

func mhGroup(caps []string, ok []bool, i int) any {
	if i < len(caps) && ok[i] {
		return caps[i]
	}
	return nil
}

// mhRegexArg is a regular expression argument of findObserveGroups (the other kind is a string).
type mhRegexArg struct{ src string }

func mhRX(src string) *mhRegexArg { return &mhRegexArg{src} }

func mhPatTruthy(p any) bool {
	switch x := p.(type) {
	case string:
		return x != ""
	case *mhRegexArg:
		return true
	}
	return false
}

func mhMatchPat(input string, pattern any) (string, bool) {
	switch p := pattern.(type) {
	case string:
		if !strings.HasPrefix(input, p) {
			return "", false
		}
		return p, true
	case *mhRegexArg:
		caps, _, _ := mhMatchRe(p.src, input)
		if caps == nil {
			return "", false
		}
		return caps[0], true
	}
	return "", false
}

type mhObserveArgs struct {
	begExcl, begIncl, endIncl, endExcl, beg2Excl, beg2Incl, end2Incl, end2Excl any
	combine                                                                    bool
}

func mhFindObserveGroups(input string, a mhObserveArgs) *mhMatch {
	findEnd := func(input string, i int, endChars any) (int, int, bool) {
		r := []rune(input)
		braces := 0
		for i < len(r) {
			ch := r[i]
			m, ok := mhMatchPat(string(r[i:]), endChars)
			if ok && braces == 0 {
				return i, i + mhLen(m), true
			} else if ch == '{' {
				braces++
			} else if ch == '}' {
				if braces == 0 {
					mhThrow("Extra close brace or missing open brace")
				}
				braces--
			}
			i++
		}
		return 0, 0, false
	}
	match, ok := mhMatchPat(input, a.begExcl)
	if !ok {
		return nil
	}
	input = mhSub(input, mhLen(match))
	match, ok = mhMatchPat(input, a.begIncl)
	if !ok {
		return nil
	}
	endChars := a.endIncl
	if !mhPatTruthy(endChars) {
		endChars = a.endExcl
	}
	endBegin, endEnd, found := findEnd(input, mhLen(match), endChars)
	if !found {
		return nil
	}
	r := []rune(input)
	cut := endBegin
	if mhPatTruthy(a.endIncl) {
		cut = endEnd
	}
	match1 := string(r[:cut])
	if !(mhPatTruthy(a.beg2Excl) || mhPatTruthy(a.beg2Incl)) {
		return &mhMatch{match: match1, remainder: mhSub(input, endEnd)}
	}
	group2 := mhFindObserveGroups(mhSub(input, endEnd), mhObserveArgs{begExcl: a.beg2Excl, begIncl: a.beg2Incl, endIncl: a.end2Incl, endExcl: a.end2Excl})
	if group2 == nil {
		return nil
	}
	pair := []any{match1, group2.match}
	if a.combine {
		return &mhMatch{match: mhStr(pair[0]) + mhStr(pair[1]), remainder: group2.remainder}
	}
	return &mhMatch{match: pair, remainder: group2.remainder}
}

// mhFOG calls findObserveGroups with the positional arguments of the original: begExcl, begIncl, endIncl, endExcl,
// beg2Excl, beg2Incl, end2Incl, end2Excl, combine.
func mhFOG(input string, a ...any) *mhMatch {
	get := func(i int) any {
		if i < len(a) {
			return a[i]
		}
		return ""
	}
	args := mhObserveArgs{begExcl: get(0), begIncl: get(1), endIncl: get(2), endExcl: get(3), beg2Excl: get(4), beg2Incl: get(5), end2Incl: get(6), end2Excl: get(7)}
	if len(a) > 8 {
		args.combine, _ = a[8].(bool)
	}
	return mhFindObserveGroups(input, args)
}

// mhGroupsMatch matches src at the start of input and returns the capture groups 1..n like the original's m.splice(1).
func mhGroupsMatch(src, input string) *mhMatch {
	caps, ok, end := mhMatchRe(src, input)
	if caps == nil || caps[0] == "" {
		return nil
	}
	groups := make([]any, len(caps)-1)
	for i := 1; i < len(caps); i++ {
		groups[i-1] = mhGroup(caps, ok, i)
	}
	return &mhMatch{match: groups, remainder: mhSub(input, end)}
}

func init() {
	oxFrag := mhRX(`^\\[a-zA-Z]+\{`)
	tmMhFuncPatts = map[string]func(string) *mhMatch{
		"(-)(9.,9)(e)(99)": func(in string) *mhMatch {
			return mhGroupsMatch(`^(\+\-|\+\/\-|\+|\-|\\pm\s?)?([0-9]+(?:[,.][0-9]+)?|[0-9]*(?:\.[0-9]+))?(\((?:[0-9]+(?:[,.][0-9]+)?|[0-9]*(?:\.[0-9]+))\))?(?:([eE]|\s*(\*|x|\\times|\u00D7)\s*10\^)([+\-]?[0-9]+|\{[+\-]?[0-9]+\}))?`, in)
		},
		"(-)(9)^(-9)": func(in string) *mhMatch {
			return mhGroupsMatch(`^(\+\-|\+\/\-|\+|\-|\\pm\s?)?([0-9]+(?:[,.][0-9]+)?|[0-9]*(?:\.[0-9]+)?)\^([+\-]?[0-9]+|\{[+\-]?[0-9]+\})`, in)
		},
		"state of aggregation $": func(in string) *mhMatch {
			a := mhFOG(in, "", mhRX(`^\([a-z]{1,3}(?=[\),])`), ")", "")
			if a != nil {
				if caps, _, _ := mhMatchRe(`^($|[\s,;\)\]\}])`, a.remainder); caps != nil {
					return a
				}
			}
			if caps, _, _ := mhMatchRe(`^(?:\((?:\\ca\s?)?\$[amothc]\$\))`, in); caps != nil {
				return &mhMatch{match: caps[0], remainder: mhSub(in, mhLen(caps[0]))}
			}
			return nil
		},
		"^{(...)}":      func(in string) *mhMatch { return mhFOG(in, "^{", "", "", "}") },
		"^($...$)":      func(in string) *mhMatch { return mhFOG(in, "^", "$", "$", "") },
		"^\\x{}{}":      func(in string) *mhMatch { return mhFOG(in, "^", oxFrag, "}", "", "", "{", "}", "", true) },
		"^\\x{}":        func(in string) *mhMatch { return mhFOG(in, "^", oxFrag, "}", "") },
		"_{(...)}":      func(in string) *mhMatch { return mhFOG(in, "_{", "", "", "}") },
		"_($...$)":      func(in string) *mhMatch { return mhFOG(in, "_", "$", "$", "") },
		"_\\x{}{}":      func(in string) *mhMatch { return mhFOG(in, "_", oxFrag, "}", "", "", "{", "}", "", true) },
		"_\\x{}":        func(in string) *mhMatch { return mhFOG(in, "_", oxFrag, "}", "") },
		"{...}":         func(in string) *mhMatch { return mhFOG(in, "", "{", "}", "") },
		"{(...)}":       func(in string) *mhMatch { return mhFOG(in, "{", "", "", "}") },
		"$...$":         func(in string) *mhMatch { return mhFOG(in, "", "$", "$", "") },
		"${(...)}$":     func(in string) *mhMatch { return mhFOG(in, "${", "", "", "}$") },
		"$(...)$":       func(in string) *mhMatch { return mhFOG(in, "$", "", "", "$") },
		"\\bond{(...)}": func(in string) *mhMatch { return mhFOG(in, "\\bond{", "", "", "}") },
		"[(...)]":       func(in string) *mhMatch { return mhFOG(in, "[", "", "", "]") },
		"\\x{}{}":       func(in string) *mhMatch { return mhFOG(in, "", oxFrag, "}", "", "", "{", "}", "", true) },
		"\\x{}":         func(in string) *mhMatch { return mhFOG(in, "", oxFrag, "}", "") },
		"\\frac{(...)}": func(in string) *mhMatch { return mhFOG(in, "\\frac{", "", "", "}", "{", "", "", "}") },
		"\\overset{(...)}": func(in string) *mhMatch {
			return mhFOG(in, "\\overset{", "", "", "}", "{", "", "", "}")
		},
		"\\underset{(...)}": func(in string) *mhMatch {
			return mhFOG(in, "\\underset{", "", "", "}", "{", "", "", "}")
		},
		"\\underbrace{(...)}": func(in string) *mhMatch {
			return mhFOG(in, "\\underbrace{", "", "", "}_", "{", "", "", "}")
		},
		"\\color{(...)}0": func(in string) *mhMatch { return mhFOG(in, "\\color{", "", "", "}") },
		"\\color{(...)}{(...)}1": func(in string) *mhMatch {
			return mhFOG(in, "\\color{", "", "", "}", "{", "", "", "}")
		},
		"\\color(...){(...)}2": func(in string) *mhMatch {
			return mhFOG(in, "\\color", "\\", "", mhRX(`^(?=\{)`), "{", "", "", "}")
		},
		"\\ce{(...)}": func(in string) *mhMatch { return mhFOG(in, "\\ce{", "", "", "}") },
		"amount": func(in string) *mhMatch {
			if caps, _, _ := mhMatchRe(`^(?:(?:(?:\([+\-]?[0-9]+\/[0-9]+\)|[+\-]?(?:[0-9]+|\$[a-z]\$|[a-z])\/[0-9]+|[+\-]?[0-9]+[.,][0-9]+|[+\-]?\.[0-9]+|[+\-]?[0-9]+)(?:[a-z](?=\s*[A-Z]))?)|[+\-]?[a-z](?=\s*[A-Z])|\+(?!\s))`, in); caps != nil {
				return &mhMatch{match: caps[0], remainder: mhSub(in, mhLen(caps[0]))}
			}
			if a := mhFOG(in, "", "$", "$", ""); a != nil {
				if caps, _, _ := mhMatchRe(`^\$(?:\(?[+\-]?(?:[0-9]*[a-z]?[+\-])?[0-9]*[a-z](?:[+\-][0-9]*[a-z]?)?\)?|\+|-)\$$`, mhStr(a.match)); caps != nil {
					return &mhMatch{match: caps[0], remainder: mhSub(in, mhLen(caps[0]))}
				}
			}
			return nil
		},
		"formula$": func(in string) *mhMatch {
			if caps, _, _ := mhMatchRe(`^\([a-z]+\)$`, in); caps != nil {
				return nil
			}
			if caps, _, _ := mhMatchRe(`^(?:[a-z]|(?:[0-9\ \+\-\,\.\(\)]+[a-z])+[0-9\ \+\-\,\.\(\)]*|(?:[a-z][0-9\ \+\-\,\.\(\)]+)+[a-z]?)$`, in); caps != nil {
				return &mhMatch{match: caps[0], remainder: mhSub(in, mhLen(caps[0]))}
			}
			return nil
		},
	}
	tmMhFuncPatts["amount2"] = tmMhFuncPatts["amount"]
	tmMhInitActions()
}

// ---- the parser -------------------------------------------------------------------------------------------------

func mhGo(input string, stateMachine string) []any {
	if input == "" {
		return []any{}
	}
	if stateMachine == "" {
		stateMachine = "ce"
	}
	state := "0"
	buffer := mhBuf{"parenthesisLevel": 0}
	input = strings.ReplaceAll(input, "\n", " ")
	for _, r := range "\u2212\u2013\u2014\u2010" {
		input = strings.ReplaceAll(input, string(r), "-")
	}
	input = strings.ReplaceAll(input, "\u2026", "...")
	var lastInput string
	started := false
	watchdog := 10
	output := []any{}
	for {
		if !started || lastInput != input {
			watchdog = 10
			lastInput = input
			started = true
		} else {
			watchdog--
		}
		machine := tmMhTransitions[stateMachine]
		t, ok := machine[state]
		if !ok {
			t = machine["*"]
		}
	iterate:
		for i := range t {
			matches := mhPatternMatch(t[i].pattern, input)
			if matches == nil {
				continue
			}
			task := t[i]
			for _, act := range task.actions {
				var o any
				if f, ok := tmMhActions[stateMachine][act.typ]; ok {
					o = f(buffer, matches.match, act.option)
				} else if f, ok := tmMhGeneric[act.typ]; ok {
					o = f(buffer, matches.match, act.option)
				} else {
					mhThrow("mhchem bug A. Please report. (" + act.typ + ")")
				}
				mhConcat(&output, o)
			}
			if task.nextState != "" {
				state = task.nextState
			}
			if mhLen(input) > 0 {
				if !task.revisit {
					input = matches.remainder
				}
				if !task.toContinue {
					break iterate
				}
			} else {
				return output
			}
		}
		if watchdog <= 0 {
			mhThrow("mhchem bug U. Please report.")
		}
	}
}

func mhBufStr(buf mhBuf, k string) string { return mhStr(buf[k]) }

func mhClearBuffer(buf mhBuf, keep ...string) {
	for k := range buf {
		skip := false
		for _, s := range keep {
			if s == k {
				skip = true
			}
		}
		if !skip {
			delete(buf, k)
		}
	}
}

func mhTrimRightSpace(s string) string { return strings.TrimRightFunc(s, jsSpace) }

func tmMhInitActions() {
	g := map[string]mhAction{}
	set := func(name, key string) {
		g[name] = func(buf mhBuf, m any, opt string) any {
			buf[key] = mhBufStr(buf, key) + mhStr(m)
			return nil
		}
	}
	set("a=", "a")
	set("b=", "b")
	set("p=", "p")
	set("o=", "o")
	set("q=", "q")
	set("d=", "d")
	set("rm=", "rm")
	set("text=", "text_")
	g["insert"] = func(buf mhBuf, m any, opt string) any { return mhObj{"type_": opt} }
	g["insert+p1"] = func(buf mhBuf, m any, opt string) any { return mhObj{"type_": opt, "p1": m} }
	g["insert+p1+p2"] = func(buf mhBuf, m any, opt string) any {
		l := m.([]any)
		return mhObj{"type_": opt, "p1": l[0], "p2": l[1]}
	}
	g["copy"] = func(buf mhBuf, m any, opt string) any { return m }
	g["rm"] = func(buf mhBuf, m any, opt string) any { return mhObj{"type_": "rm", "p1": mhStr(m)} }
	g["text"] = func(buf mhBuf, m any, opt string) any { return mhGo(mhStr(m), "text") }
	g["{text}"] = func(buf mhBuf, m any, opt string) any {
		ret := []any{"{"}
		mhConcat(&ret, mhGo(mhStr(m), "text"))
		return append(ret, "}")
	}
	g["tex-math"] = func(buf mhBuf, m any, opt string) any { return mhGo(mhStr(m), "tex-math") }
	g["tex-math tight"] = func(buf mhBuf, m any, opt string) any { return mhGo(mhStr(m), "tex-math tight") }
	g["bond"] = func(buf mhBuf, m any, opt string) any {
		k := any(opt)
		if opt == "" {
			k = m
		}
		return mhObj{"type_": "bond", "kind_": k}
	}
	g["color0-output"] = func(buf mhBuf, m any, opt string) any { return mhObj{"type_": "color0", "color": m.([]any)[0]} }
	g["ce"] = func(buf mhBuf, m any, opt string) any { return mhGo(mhStr(m), "") }
	g["1/2"] = func(buf mhBuf, m any, opt string) any {
		ret := []any{}
		ms := mhStr(m)
		if caps, _, _ := mhMatchRe(`^[+\-]`, ms); caps != nil {
			ret = append(ret, string([]rune(ms)[:1]))
			ms = mhSub(ms, 1)
		}
		caps, ok, _ := mhMatchRe(`^([0-9]+|\$[a-z]\$|[a-z])\/([0-9]+)(\$[a-z]\$|[a-z])?$`, ms)
		if caps == nil {
			mhThrow("Cannot read properties of null (reading '1')")
		}
		n1 := strings.ReplaceAll(caps[1], "$", "")
		ret = append(ret, mhObj{"type_": "frac", "p1": n1, "p2": caps[2]})
		if ok[3] && caps[3] != "" {
			ret = append(ret, mhObj{"type_": "tex-math", "p1": strings.ReplaceAll(caps[3], "$", "")})
		}
		return ret
	}
	g["9,9"] = func(buf mhBuf, m any, opt string) any { return mhGo(mhStr(m), "9,9") }
	tmMhGeneric = g

	ce := map[string]mhAction{}
	out := func(buf mhBuf, m any, opt string) any { return ce["output"](buf, m, opt) }
	ce["o after d"] = func(buf mhBuf, m any, opt string) any {
		var ret any
		if caps, _, _ := mhMatchRe(`^[0-9]+$`, mhBufStr(buf, "d")); caps != nil {
			tmp := buf["d"]
			buf["d"] = nil
			ret = out(buf, nil, "")
			buf["b"] = tmp
		} else {
			ret = out(buf, nil, "")
		}
		g["o="](buf, m, "")
		return ret
	}
	ce["d= kv"] = func(buf mhBuf, m any, opt string) any {
		buf["d"] = m
		buf["dType"] = "kv"
		return nil
	}
	ce["charge or bond"] = func(buf mhBuf, m any, opt string) any {
		if mhTruthy(buf["beginsWithBond"]) {
			ret := []any{}
			mhConcat(&ret, out(buf, nil, ""))
			mhConcat(&ret, g["bond"](buf, m, "-"))
			return ret
		}
		buf["d"] = m
		return nil
	}
	ce["- after o/d"] = func(buf mhBuf, m any, opt string) any {
		isAfterD := opt == "true"
		c1 := mhPatternMatch("orbital", mhBufStr(buf, "o"))
		c2 := mhPatternMatch("one lowercase greek letter $", mhBufStr(buf, "o"))
		c3 := mhPatternMatch("one lowercase latin letter $", mhBufStr(buf, "o"))
		c4 := mhPatternMatch("$one lowercase latin letter$ $", mhBufStr(buf, "o"))
		hyphenFollows := mhStr(m) == "-" && ((c1 != nil && c1.remainder == "") || c2 != nil || c3 != nil || c4 != nil)
		if hyphenFollows && !mhTruthy(buf["a"]) && !mhTruthy(buf["b"]) && !mhTruthy(buf["p"]) && !mhTruthy(buf["d"]) && !mhTruthy(buf["q"]) && c1 == nil && c3 != nil {
			buf["o"] = "$" + mhBufStr(buf, "o") + "$"
		}
		ret := []any{}
		if hyphenFollows {
			mhConcat(&ret, out(buf, nil, ""))
			ret = append(ret, mhObj{"type_": "hyphen"})
		} else {
			c1 = mhPatternMatch("digits", mhBufStr(buf, "d"))
			if isAfterD && c1 != nil && c1.remainder == "" {
				mhConcat(&ret, g["d="](buf, m, ""))
				mhConcat(&ret, out(buf, nil, ""))
			} else {
				mhConcat(&ret, out(buf, nil, ""))
				mhConcat(&ret, g["bond"](buf, m, "-"))
			}
		}
		return ret
	}
	ce["a to o"] = func(buf mhBuf, m any, opt string) any {
		buf["o"] = buf["a"]
		buf["a"] = nil
		return nil
	}
	ce["sb=true"] = func(buf mhBuf, m any, opt string) any { buf["sb"] = true; return nil }
	ce["sb=false"] = func(buf mhBuf, m any, opt string) any { buf["sb"] = false; return nil }
	ce["beginsWithBond=true"] = func(buf mhBuf, m any, opt string) any { buf["beginsWithBond"] = true; return nil }
	ce["beginsWithBond=false"] = func(buf mhBuf, m any, opt string) any { buf["beginsWithBond"] = false; return nil }
	ce["parenthesisLevel++"] = func(buf mhBuf, m any, opt string) any {
		buf["parenthesisLevel"] = buf["parenthesisLevel"].(int) + 1
		return nil
	}
	ce["parenthesisLevel--"] = func(buf mhBuf, m any, opt string) any {
		buf["parenthesisLevel"] = buf["parenthesisLevel"].(int) - 1
		return nil
	}
	ce["state of aggregation"] = func(buf mhBuf, m any, opt string) any {
		return mhObj{"type_": "state of aggregation", "p1": mhGo(mhStr(m), "o")}
	}
	ce["comma"] = func(buf mhBuf, m any, opt string) any {
		ms := mhStr(m)
		a := mhTrimRightSpace(ms)
		withSpace := a != ms
		if withSpace && buf["parenthesisLevel"].(int) == 0 {
			return mhObj{"type_": "comma enumeration L", "p1": a}
		}
		return mhObj{"type_": "comma enumeration M", "p1": a}
	}
	ce["output"] = func(buf mhBuf, m any, entityFollows string) any {
		var ret any
		if !mhTruthy(buf["r"]) {
			list := []any{}
			if !mhTruthy(buf["a"]) && !mhTruthy(buf["b"]) && !mhTruthy(buf["p"]) && !mhTruthy(buf["o"]) && !mhTruthy(buf["q"]) && !mhTruthy(buf["d"]) && entityFollows == "" {
			} else {
				if mhTruthy(buf["sb"]) {
					list = append(list, mhObj{"type_": "entitySkip"})
				}
				if !mhTruthy(buf["o"]) && !mhTruthy(buf["q"]) && !mhTruthy(buf["d"]) && !mhTruthy(buf["b"]) && !mhTruthy(buf["p"]) && entityFollows != "2" {
					buf["o"] = buf["a"]
					buf["a"] = nil
				} else if !mhTruthy(buf["o"]) && !mhTruthy(buf["q"]) && !mhTruthy(buf["d"]) && (mhTruthy(buf["b"]) || mhTruthy(buf["p"])) {
					buf["o"] = buf["a"]
					buf["d"] = buf["b"]
					buf["q"] = buf["p"]
					buf["a"], buf["b"], buf["p"] = nil, nil, nil
				} else {
					if mhTruthy(buf["o"]) && buf["dType"] == "kv" && mhPatternMatch("d-oxidation$", mhBufStr(buf, "d")) != nil {
						buf["dType"] = "oxidation"
					} else if mhTruthy(buf["o"]) && buf["dType"] == "kv" && !mhTruthy(buf["q"]) {
						buf["dType"] = nil
					}
				}
				dm := "bd"
				if buf["dType"] == "oxidation" {
					dm = "oxidation"
				}
				list = append(list, mhObj{"type_": "chemfive", "a": mhGo(mhBufStr(buf, "a"), "a"), "b": mhGo(mhBufStr(buf, "b"), "bd"),
					"p": mhGo(mhBufStr(buf, "p"), "pq"), "o": mhGo(mhBufStr(buf, "o"), "o"), "q": mhGo(mhBufStr(buf, "q"), "pq"),
					"d": mhGo(mhBufStr(buf, "d"), dm), "dType": buf["dType"]})
			}
			ret = list
		} else {
			var rd, rq []any
			switch buf["rdt"] {
			case "M":
				rd = mhGo(mhBufStr(buf, "rd"), "tex-math")
			case "T":
				rd = []any{mhObj{"type_": "text", "p1": mhBufStr(buf, "rd")}}
			default:
				rd = mhGo(mhBufStr(buf, "rd"), "")
			}
			switch buf["rqt"] {
			case "M":
				rq = mhGo(mhBufStr(buf, "rq"), "tex-math")
			case "T":
				rq = []any{mhObj{"type_": "text", "p1": mhBufStr(buf, "rq")}}
			default:
				rq = mhGo(mhBufStr(buf, "rq"), "")
			}
			ret = mhObj{"type_": "arrow", "r": buf["r"], "rd": rd, "rq": rq}
		}
		mhClearBuffer(buf, "parenthesisLevel", "beginsWithBond")
		return ret
	}
	ce["oxidation-output"] = func(buf mhBuf, m any, opt string) any {
		ret := []any{"{"}
		mhConcat(&ret, mhGo(mhStr(m), "oxidation"))
		return append(ret, "}")
	}
	pair := func(typ string) mhAction {
		return func(buf mhBuf, m any, opt string) any {
			l := m.([]any)
			return mhObj{"type_": typ, "p1": mhGo(mhStr(l[0]), ""), "p2": mhGo(mhStr(l[1]), "")}
		}
	}
	ce["frac-output"] = pair("frac-ce")
	ce["overset-output"] = pair("overset")
	ce["underset-output"] = pair("underset")
	ce["underbrace-output"] = pair("underbrace")
	ce["color-output"] = func(buf mhBuf, m any, opt string) any {
		l := m.([]any)
		return mhObj{"type_": "color", "color1": l[0], "color2": mhGo(mhStr(l[1]), "")}
	}
	for _, k := range []string{"r", "rdt", "rd", "rqt", "rq"} {
		key := k
		ce[key+"="] = func(buf mhBuf, m any, opt string) any { buf[key] = m; return nil }
	}
	ce["operator"] = func(buf mhBuf, m any, opt string) any {
		k := any(opt)
		if opt == "" {
			k = m
		}
		return mhObj{"type_": "operator", "kind_": k}
	}

	texOutput := func(buf mhBuf, m any, opt string) any {
		if mhTruthy(buf["o"]) {
			ret := mhObj{"type_": "tex-math", "p1": buf["o"]}
			mhClearBuffer(buf)
			return ret
		}
		return nil
	}
	textM := map[string]mhAction{"output": func(buf mhBuf, m any, opt string) any {
		if mhTruthy(buf["text_"]) {
			ret := mhObj{"type_": "text", "p1": buf["text_"]}
			mhClearBuffer(buf)
			return ret
		}
		return nil
	}}
	pq := map[string]mhAction{
		"state of aggregation": func(buf mhBuf, m any, opt string) any {
			return mhObj{"type_": "state of aggregation subscript", "p1": mhGo(mhStr(m), "o")}
		},
		"color-output": func(buf mhBuf, m any, opt string) any {
			l := m.([]any)
			return mhObj{"type_": "color", "color1": l[0], "color2": mhGo(mhStr(l[1]), "pq")}
		},
	}
	bd := map[string]mhAction{"color-output": func(buf mhBuf, m any, opt string) any {
		l := m.([]any)
		return mhObj{"type_": "color", "color1": l[0], "color2": mhGo(mhStr(l[1]), "bd")}
	}}
	ox := map[string]mhAction{"roman-numeral": func(buf mhBuf, m any, opt string) any { return mhObj{"type_": "roman numeral", "p1": mhStr(m)} }}
	texMath := map[string]mhAction{"output": texOutput}
	tight := map[string]mhAction{
		"tight operator": func(buf mhBuf, m any, opt string) any {
			buf["o"] = mhBufStr(buf, "o") + "{" + mhStr(m) + "}"
			return nil
		},
		"output": texOutput,
	}
	nine := map[string]mhAction{"comma": func(buf mhBuf, m any, opt string) any { return mhObj{"type_": "commaDecimal"} }}

	pu := map[string]mhAction{}
	pu["enumber"] = func(buf mhBuf, m any, opt string) any {
		l := m.([]any)
		ret := []any{}
		if s := mhStr(l[0]); s == "+-" || s == "+/-" {
			ret = append(ret, "\\pm ")
		} else if mhTruthy(l[0]) {
			ret = append(ret, l[0])
		}
		if mhTruthy(l[1]) {
			mhConcat(&ret, mhGo(mhStr(l[1]), "pu-9,9"))
			if mhTruthy(l[2]) {
				if caps, _, _ := mhMatchRe(`[,.]`, mhStr(l[2])); caps != nil {
					mhConcat(&ret, mhGo(mhStr(l[2]), "pu-9,9"))
				} else {
					ret = append(ret, l[2])
				}
			}
			if mhTruthy(l[4]) {
				l[3] = l[4]
			}
			if mhTruthy(l[3]) {
				l[3] = jsTrim(mhStr(l[3]))
				if s := mhStr(l[3]); s == "e" || strings.HasPrefix(s, "*") {
					ret = append(ret, mhObj{"type_": "cdot"})
				} else {
					ret = append(ret, mhObj{"type_": "times"})
				}
			}
		}
		if mhTruthy(l[3]) {
			ret = append(ret, "10^{"+mhStr(l[5])+"}")
		}
		return ret
	}
	pu["number^"] = func(buf mhBuf, m any, opt string) any {
		l := m.([]any)
		ret := []any{}
		if s := mhStr(l[0]); s == "+-" || s == "+/-" {
			ret = append(ret, "\\pm ")
		} else if mhTruthy(l[0]) {
			ret = append(ret, l[0])
		}
		mhConcat(&ret, mhGo(mhStr(l[1]), "pu-9,9"))
		return append(ret, "^{"+mhStr(l[2])+"}")
	}
	pu["operator"] = ce["operator"]
	pu["space"] = func(buf mhBuf, m any, opt string) any { return mhObj{"type_": "pu-space-1"} }
	degrees := func(s string) string {
		s = strings.NewReplacer("\u00B0C", "{}^{\\circ}C", "^oC", "{}^{\\circ}C", "^{o}C", "{}^{\\circ}C").Replace(s)
		return strings.NewReplacer("\u00B0F", "{}^{\\circ}F", "^oF", "{}^{\\circ}F", "^{o}F", "{}^{\\circ}F").Replace(s)
	}
	pu["output"] = func(buf mhBuf, m any, opt string) any {
		var ret any
		if md := mhPatternMatch("{(...)}", mhBufStr(buf, "d")); md != nil && md.remainder == "" {
			buf["d"] = md.match
		}
		if mq := mhPatternMatch("{(...)}", mhBufStr(buf, "q")); mq != nil && mq.remainder == "" {
			buf["q"] = mq.match
		}
		if mhTruthy(buf["d"]) {
			buf["d"] = degrees(mhBufStr(buf, "d"))
		}
		if mhTruthy(buf["q"]) {
			buf["q"] = degrees(mhBufStr(buf, "q"))
			bdl := mhGo(mhBufStr(buf, "d"), "pu")
			bql := mhGo(mhBufStr(buf, "q"), "pu")
			if mhBufStr(buf, "o") == "//" {
				ret = mhObj{"type_": "pu-frac", "p1": bdl, "p2": bql}
			} else {
				list := bdl
				if len(bdl) > 1 || len(bql) > 1 {
					list = append(list, mhObj{"type_": " / "})
				} else {
					list = append(list, mhObj{"type_": "/"})
				}
				mhConcat(&list, bql)
				ret = list
			}
		} else {
			ret = mhGo(mhBufStr(buf, "d"), "pu-2")
		}
		mhClearBuffer(buf)
		return ret
	}
	pu2 := map[string]mhAction{}
	pu2["cdot"] = func(buf mhBuf, m any, opt string) any { return mhObj{"type_": "tight cdot"} }
	pu2["^(-1)"] = func(buf mhBuf, m any, opt string) any {
		buf["rm"] = mhBufStr(buf, "rm") + "^{" + mhStr(m) + "}"
		return nil
	}
	pu2["space"] = func(buf mhBuf, m any, opt string) any { return mhObj{"type_": "pu-space-2"} }
	pu2["output"] = func(buf mhBuf, m any, opt string) any {
		var ret any = []any{}
		if mhTruthy(buf["rm"]) {
			if mrm := mhPatternMatch("{(...)}", mhBufStr(buf, "rm")); mrm != nil && mrm.remainder == "" {
				ret = mhGo(mhStr(mrm.match), "pu")
			} else {
				ret = mhObj{"type_": "rm", "p1": buf["rm"]}
			}
		}
		mhClearBuffer(buf)
		return ret
	}
	pu9 := map[string]mhAction{}
	pu9["comma"] = func(buf mhBuf, m any, opt string) any { return mhObj{"type_": "commaDecimal"} }
	pu9["output-0"] = func(buf mhBuf, m any, opt string) any {
		ret := []any{}
		text := []rune(mhBufStr(buf, "text_"))
		if len(text) > 4 {
			a := len(text) % 3
			if a == 0 {
				a = 3
			}
			for i := len(text) - 3; i > 0; i -= 3 {
				ret = append(ret, string(text[i:i+3]), mhObj{"type_": "1000 separator"})
			}
			ret = append(ret, string(text[:a]))
			for i, j := 0, len(ret)-1; i < j; i, j = i+1, j-1 {
				ret[i], ret[j] = ret[j], ret[i]
			}
		} else {
			ret = append(ret, string(text))
		}
		mhClearBuffer(buf)
		return ret
	}
	pu9["output-o"] = func(buf mhBuf, m any, opt string) any {
		ret := []any{}
		text := []rune(mhBufStr(buf, "text_"))
		if len(text) > 4 {
			a := len(text) - 3
			i := 0
			for ; i < a; i += 3 {
				ret = append(ret, string(text[i:min(i+3, len(text))]), mhObj{"type_": "1000 separator"})
			}
			ret = append(ret, string(text[min(i, len(text)):]))
		} else {
			ret = append(ret, string(text))
		}
		mhClearBuffer(buf)
		return ret
	}
	tmMhActions = map[string]map[string]mhAction{"ce": ce, "text": textM, "pq": pq, "bd": bd, "oxidation": ox, "tex-math": texMath, "tex-math tight": tight, "9,9": nine,
		"pu": pu, "pu-2": pu2, "pu-9,9": pu9}
}

// ---- texify -----------------------------------------------------------------------------------------------------

func mhTexGo(input []any, isInner bool) string {
	if input == nil {
		return ""
	}
	var res strings.Builder
	cee := false
	for _, item := range input {
		if s, ok := item.(string); ok {
			res.WriteString(s)
		} else {
			o := item.(mhObj)
			res.WriteString(mhGo2(o))
			if o["type_"] == "1st-level escape" {
				cee = true
			}
		}
	}
	out := res.String()
	if !isInner && !cee && out != "" {
		out = "{" + out + "}"
	}
	return out
}

func mhGoInner(v any) string {
	switch x := v.(type) {
	case []any:
		return mhTexGo(x, true)
	case string:
		return x
	}
	return ""
}

func mhStartsSign(s string) bool { return strings.HasPrefix(s, "+") || strings.HasPrefix(s, "-") }

func mhGo2(buf mhObj) string {
	typ, _ := buf["type_"].(string)
	switch typ {
	case "chemfive":
		res := ""
		a, b, p, o, q, d := mhGoInner(buf["a"]), mhGoInner(buf["b"]), mhGoInner(buf["p"]), mhGoInner(buf["o"]), mhGoInner(buf["q"]), mhGoInner(buf["d"])
		if a != "" {
			if mhStartsSign(a) {
				a = "{" + a + "}"
			}
			res += a + "\\,"
		}
		if b != "" || p != "" {
			res += "{\\vphantom{X}}"
			res += "^{\\hphantom{" + b + "}}_{\\hphantom{" + p + "}}"
			res += "{\\vphantom{X}}"
			res += "^{\\vphantom{2}\\mathllap{" + b + "}}"
			res += "_{\\vphantom{2}\\mathllap{" + p + "}}"
		}
		if o != "" {
			if mhStartsSign(o) {
				o = "{" + o + "}"
			}
			res += o
		}
		switch buf["dType"] {
		case "kv":
			if d != "" || q != "" {
				res += "{\\vphantom{X}}"
			}
			if d != "" {
				res += "^{" + d + "}"
			}
			if q != "" {
				res += "_{" + q + "}"
			}
		case "oxidation":
			if d != "" {
				res += "{\\vphantom{X}}"
				res += "^{" + d + "}"
			}
			if q != "" {
				res += "{{}}"
				res += "_{" + q + "}"
			}
		default:
			if q != "" {
				res += "{{}}"
				res += "_{" + q + "}"
			}
			if d != "" {
				res += "{{}}"
				res += "^{" + d + "}"
			}
		}
		return res
	case "rm":
		return "\\mathrm{" + mhStr(buf["p1"]) + "}"
	case "text":
		p1 := mhStr(buf["p1"])
		if strings.ContainsAny(p1, "^_") {
			p1 = strings.Replace(strings.Replace(p1, " ", "~", 1), "-", "\\text{-}", 1)
			return "\\mathrm{" + p1 + "}"
		}
		return "\\text{" + p1 + "}"
	case "roman numeral":
		return "\\mathrm{" + mhStr(buf["p1"]) + "}"
	case "state of aggregation":
		return "\\mskip2mu " + mhGoInner(buf["p1"])
	case "state of aggregation subscript":
		return "\\mskip1mu " + mhGoInner(buf["p1"])
	case "bond":
		res := mhGetBond(mhStr(buf["kind_"]))
		if res == "" {
			mhThrow("mhchem Error. Unknown bond type (" + mhStr(buf["kind_"]) + ")")
		}
		return res
	case "frac":
		c := "\\frac{" + mhStr(buf["p1"]) + "}{" + mhStr(buf["p2"]) + "}"
		return "\\mathchoice{\\textstyle" + c + "}{" + c + "}{" + c + "}{" + c + "}"
	case "pu-frac":
		d := "\\frac{" + mhGoInner(buf["p1"]) + "}{" + mhGoInner(buf["p2"]) + "}"
		return "\\mathchoice{\\textstyle" + d + "}{" + d + "}{" + d + "}{" + d + "}"
	case "tex-math":
		return mhStr(buf["p1"]) + " "
	case "frac-ce":
		return "\\frac{" + mhGoInner(buf["p1"]) + "}{" + mhGoInner(buf["p2"]) + "}"
	case "overset":
		return "\\overset{" + mhGoInner(buf["p1"]) + "}{" + mhGoInner(buf["p2"]) + "}"
	case "underset":
		return "\\underset{" + mhGoInner(buf["p1"]) + "}{" + mhGoInner(buf["p2"]) + "}"
	case "underbrace":
		return "\\underbrace{" + mhGoInner(buf["p1"]) + "}_{" + mhGoInner(buf["p2"]) + "}"
	case "color":
		return "{\\color{" + mhStr(buf["color1"]) + "}{" + mhGoInner(buf["color2"]) + "}}"
	case "color0":
		return "\\color{" + mhStr(buf["color"]) + "}"
	case "arrow":
		rd, rq := mhGoInner(buf["rd"]), mhGoInner(buf["rq"])
		arrow := mhGetArrow(mhStr(buf["r"]))
		if rq != "" {
			arrow += "[{\\rm " + rq + "}]"
		}
		if rd != "" {
			arrow += "{\\rm " + rd + "}"
		} else {
			arrow += "{}"
		}
		return arrow
	case "operator":
		return mhGetOperator(mhStr(buf["kind_"]))
	case "1st-level escape":
		return mhStr(buf["p1"]) + " "
	case "space":
		return " "
	case "entitySkip", "pu-space-1":
		return "~"
	case "pu-space-2":
		return "\\mkern3mu "
	case "1000 separator":
		return "\\mkern2mu "
	case "commaDecimal":
		return "{,}"
	case "comma enumeration L":
		return "{" + mhStr(buf["p1"]) + "}\\mkern6mu "
	case "comma enumeration M":
		return "{" + mhStr(buf["p1"]) + "}\\mkern3mu "
	case "comma enumeration S":
		return "{" + mhStr(buf["p1"]) + "}\\mkern1mu "
	case "hyphen":
		return "\\text{-}"
	case "addition compound":
		return "\\,{\\cdot}\\,"
	case "electron dot":
		return "\\mkern1mu \\text{\\textbullet}\\mkern1mu "
	case "KV x":
		return "{\\times}"
	case "prime":
		return "\\prime "
	case "cdot":
		return "\\cdot "
	case "tight cdot":
		return "\\mkern1mu{\\cdot}\\mkern1mu "
	case "times":
		return "\\times "
	case "circa":
		return "{\\sim}"
	case "^":
		return "uparrow"
	case "v":
		return "downarrow"
	case "ellipsis":
		return "\\ldots "
	case "/":
		return "/"
	case " / ":
		return "\\,/\\,"
	}
	mhThrow("mhchem bug T. Please report.")
	return ""
}

func mhGetArrow(a string) string {
	switch a {
	case "->", "\u2192", "\u27F6":
		return "\\yields"
	case "<-":
		return "\\yieldsLeft"
	case "<->":
		return "\\mesomerism"
	case "<-->":
		return "\\yieldsLeftRight"
	case "<=>", "\u21CC":
		return "\\chemequilibrium"
	case "<=>>":
		return "\\equilibriumRight"
	case "<<=>":
		return "\\equilibriumLeft"
	}
	mhThrow("mhchem bug T. Please report.")
	return ""
}

func mhGetBond(a string) string {
	switch a {
	case "-", "1":
		return "{-}"
	case "=", "2":
		return "{=}"
	case "#", "3":
		return "{\\equiv}"
	case "~":
		return "{\\tripleDash}"
	case "~-":
		return "{\\tripleDashOverLine}"
	case "~=", "~--":
		return "{\\tripleDashOverDoubleLine}"
	case "-~-":
		return "{\\tripleDashBetweenDoubleLine}"
	case "...":
		return "{{\\cdot}{\\cdot}{\\cdot}}"
	case "....":
		return "{{\\cdot}{\\cdot}{\\cdot}{\\cdot}}"
	case "->":
		return "{\\rightarrow}"
	case "<-":
		return "{\\leftarrow}"
	case "<":
		return "{<}"
	case ">":
		return "{>}"
	}
	mhThrow("mhchem bug T. Please report.")
	return ""
}

func mhGetOperator(a string) string {
	switch a {
	case "+":
		return " {}+{} "
	case "-":
		return " {}-{} "
	case "=":
		return " {}={} "
	case "<":
		return " {}<{} "
	case ">":
		return " {}>{} "
	case "<<":
		return " {}\\ll{} "
	case ">>":
		return " {}\\gg{} "
	case "\\pm":
		return " {}\\pm{} "
	case "\\approx", "$\\approx$":
		return " {}\\approx{} "
	case "v", "(v)":
		return " \\downarrow{} "
	case "^", "(^)":
		return " \\uparrow{} "
	}
	mhThrow("mhchem bug T. Please report.")
	return ""
}

// tmChemParse is chemParse(tokens, stateMachine) of the \ce and \pu macros.
func tmChemParse(tokens []*tmToken, stateMachine string) string {
	var b strings.Builder
	expectedLoc := 0
	if len(tokens) > 0 {
		expectedLoc = tokens[len(tokens)-1].loc.start
	}
	for i := len(tokens) - 1; i >= 0; i-- {
		if tokens[i].loc.start > expectedLoc {
			b.WriteString(" ")
			expectedLoc = tokens[i].loc.start
		}
		b.WriteString(tokens[i].text)
		expectedLoc += jsLen(tokens[i].text)
	}
	return mhTexGo(mhGo(b.String(), stateMachine), false)
}
