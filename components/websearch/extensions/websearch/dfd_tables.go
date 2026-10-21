package websearch

import (
	"compress/gzip"
	"embed"
	"io"
	"strings"
	"sync"
)

// The data tables of the Defuddle port (the symbol tables of temml and mathml-to-latex, the mhchem state machines and
// the HTML named character references) are embedded as gzipped files in tables/ (written by port/gen-defuddle-temml-tables.mjs,
// port/gen-defuddle-mathml-tables.mjs and port/gen-html-entities.py) and decoded the first time a page needs
// them. As Go map literals they cost the process 2 ms of start-up and half a megabyte of code to build them.
// A table file is a list of records separated by U+0001, each a list of fields separated by U+0000.

//go:embed tables/*.gz
var dfdTablesFS embed.FS

// dfdTableRows decodes the table file tables/<name>.gz.
func dfdTableRows(name string) [][]string {
	f, err := dfdTablesFS.Open("tables/" + name + ".gz")
	if err != nil {
		panic("websearch: missing table " + name + ": " + err.Error())
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		panic("websearch: table " + name + ": " + err.Error())
	}
	data, err := io.ReadAll(zr)
	if err != nil {
		panic("websearch: table " + name + ": " + err.Error())
	}
	text := string(data)
	if text == "" {
		return nil
	}
	records := strings.Split(text, "\x01")
	rows := make([][]string, len(records))
	for i, r := range records {
		rows[i] = strings.Split(r, "\x00")
	}
	return rows
}

// dfdStringMap is a table of key and value rows.
func dfdStringMap(name string) map[string]string {
	rows := dfdTableRows(name)
	m := make(map[string]string, len(rows))
	for _, r := range rows {
		m[r[0]] = r[1]
	}
	return m
}

// dfdStringSet is a table of single-field rows.
func dfdStringSet(name string) map[string]bool {
	rows := dfdTableRows(name)
	m := make(map[string]bool, len(rows))
	for _, r := range rows {
		m[r[0]] = true
	}
	return m
}

type tmSymbol struct{ group, replace string }

type tmAccentCmd struct{ text, math string }

type tmMhAct struct{ typ, option string }

type tmMhTrans struct {
	pattern             string
	actions             []tmMhAct
	nextState           string
	revisit, toContinue bool
}

type dfdMLAccented struct{ char, accent string }

type dfdMLFontChar struct{ letter, fontCmd string }

func tmSymbolMap(name string) map[string]*tmSymbol {
	rows := dfdTableRows(name)
	m := make(map[string]*tmSymbol, len(rows))
	for _, r := range rows {
		m[r[0]] = &tmSymbol{r[1], r[2]}
	}
	return m
}

// Temml 0.13.4 src/symbols.js, unicodeSymbols.js, unicodeAccents.js, asciiFromScript.js, unicodeSupOrSub.js,
// functions/color.js and the string macros of defineMacro.js, and the macros and the mhchem tables of dist/temml.cjs.
var (
	tmSymbolsMath     = sync.OnceValue(func() map[string]*tmSymbol { return tmSymbolMap("tm-symbols-math") })
	tmSymbolsText     = sync.OnceValue(func() map[string]*tmSymbol { return tmSymbolMap("tm-symbols-text") })
	tmLigatures       = sync.OnceValue(func() map[string]bool { return dfdStringSet("tm-ligatures") })
	tmUnicodeSymbols  = sync.OnceValue(func() map[string]string { return dfdStringMap("tm-unicode-symbols") })
	tmAsciiFromScript = sync.OnceValue(func() map[string]string { return dfdStringMap("tm-ascii-from-script") })
	tmSubsAndSups     = sync.OnceValue(func() map[string]string { return dfdStringMap("tm-subs-and-sups") })
	tmXColors         = sync.OnceValue(func() map[string]string { return dfdStringMap("tm-xcolors") })
	tmStringMacros    = sync.OnceValue(func() map[string]string { return dfdStringMap("tm-string-macros") })
	tmMhRegexPatterns = sync.OnceValue(func() map[string]string { return dfdStringMap("tm-mh-patterns") })
	tmDistMacros      = sync.OnceValue(func() map[string]string { return dfdStringMap("tm-dist-macros") })

	tmUnicodeAccents = sync.OnceValue(func() map[string]tmAccentCmd {
		rows := dfdTableRows("tm-unicode-accents")
		m := make(map[string]tmAccentCmd, len(rows))
		for _, r := range rows {
			m[r[0]] = tmAccentCmd{r[1], r[2]}
		}
		return m
	})

	// A row of tm-mh-transitions: machine, state, pattern, next state, revisit, toContinue, then the actions as pairs
	// of type and option.
	tmMhTransitions = sync.OnceValue(func() map[string]map[string][]tmMhTrans {
		m := map[string]map[string][]tmMhTrans{}
		for _, r := range dfdTableRows("tm-mh-transitions") {
			acts := make([]tmMhAct, 0, (len(r)-6)/2)
			for i := 6; i+1 < len(r); i += 2 {
				acts = append(acts, tmMhAct{r[i], r[i+1]})
			}
			machine := m[r[0]]
			if machine == nil {
				machine = map[string][]tmMhTrans{}
				m[r[0]] = machine
			}
			machine[r[1]] = append(machine[r[1]], tmMhTrans{pattern: r[2], actions: acts, nextState: r[3], revisit: r[4] == "1", toContinue: r[5] == "1"})
		}
		return m
	})
)

// mathml-to-latex 1.8.0 lookup tables.
var (
	dfdMLOperatorsByChar  = sync.OnceValue(func() map[string]string { return dfdStringMap("ml-operators-by-char") })
	dfdMLOperatorsByGlyph = sync.OnceValue(func() map[string]string { return dfdStringMap("ml-operators-by-glyph") })
	dfdMLSymbolsByChar    = sync.OnceValue(func() map[string]string { return dfdStringMap("ml-symbols-by-char") })
	dfdMLSymbolsByGlyph   = sync.OnceValue(func() map[string]string { return dfdStringMap("ml-symbols-by-glyph") })
	dfdMLNumberByGlyph    = sync.OnceValue(func() map[string]string { return dfdStringMap("ml-number-by-glyph") })
	dfdMLAccentToCmd      = sync.OnceValue(func() map[string]string { return dfdStringMap("ml-accent-to-cmd") })

	dfdMLVowelsWithAccents = sync.OnceValue(func() map[string]dfdMLAccented {
		rows := dfdTableRows("ml-vowels-with-accents")
		m := make(map[string]dfdMLAccented, len(rows))
		for _, r := range rows {
			m[r[0]] = dfdMLAccented{r[1], r[2]}
		}
		return m
	})
	dfdMLSpecialFontChars = sync.OnceValue(func() map[string]dfdMLFontChar {
		rows := dfdTableRows("ml-special-font-chars")
		m := make(map[string]dfdMLFontChar, len(rows))
		for _, r := range rows {
			m[r[0]] = dfdMLFontChar{r[1], r[2]}
		}
		return m
	})
)

// The named character references of the HTML standard (ldom_parse.go): ldEntities maps each name, without its
// semicolon, to its characters; ldLegacyEntities holds the names that also decode without a semicolon.
var (
	ldEntities       = sync.OnceValue(func() map[string]string { return dfdStringMap("ld-entities") })
	ldLegacyEntities = sync.OnceValue(func() map[string]bool { return dfdStringSet("ld-legacy-entities") })
)
