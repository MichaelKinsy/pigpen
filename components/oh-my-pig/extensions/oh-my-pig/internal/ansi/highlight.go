package ansi

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// ═══════════════════════════════════════════════════════════════════════════
// Plain-PTY Code Syntax Highlighter
// Lightweight lexer highlighter for plain-terminal code cells using omp's
// exact titanium foreground syntax palette tokens in theme.go:
//   syntaxComment, syntaxKeyword, syntaxFunction, syntaxVariable,
//   syntaxString, syntaxNumber, syntaxType, syntaxOperator,
//   syntaxPunctuation, toolOutput
// ═══════════════════════════════════════════════════════════════════════════

// normalizeHighlightLang maps language names and file extensions to canonical
// highlighter language IDs, returning "" for unsupported languages.
func normalizeHighlightLang(lang string) string {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "py", "python", "pyi":
		return "python"
	case "js", "javascript", "jsx", "mjs", "cjs", "bun":
		return "javascript"
	case "ts", "typescript", "tsx", "mts", "cts":
		return "typescript"
	case "go", "golang":
		return "go"
	case "json", "jsonc":
		return "json"
	case "bash", "sh", "zsh", "ksh", "shell":
		return "bash"
	case "sql":
		return "sql"
	case "yaml", "yml":
		return "yaml"
	case "toml":
		return "toml"
	case "rs", "rust":
		return "rust"
	default:
		return ""
	}
}

// isHighlightSupported reports whether highlightCodeLines supports lang.
func isHighlightSupported(lang string) bool {
	return normalizeHighlightLang(lang) != ""
}

type hlLangSpec struct {
	hashComment  bool
	slashComment bool
	blockComment bool
	dashComment  bool
	tripleQuotes bool
	backtickStr  bool
	singleStr    bool
	dollarVar    bool
	sqlCaseFold  bool
	isJSON       bool
	isYAML       bool
	isTOML       bool
	isRust       bool
	keywords     map[string]bool
	types        map[string]bool
	builtins     map[string]bool
}

func makeWordSet(words string) map[string]bool {
	fields := strings.Fields(words)
	m := make(map[string]bool, len(fields))
	for _, w := range fields {
		m[w] = true
	}
	return m
}

var hlSpecs = map[string]*hlLangSpec{
	"python": {
		hashComment:  true,
		tripleQuotes: true,
		singleStr:    true,
		keywords: makeWordSet(
			"False None True and as assert async await break class continue def del elif else except " +
				"finally for from global if import in is lambda nonlocal not or pass raise return try " +
				"while with yield match case",
		),
		types: makeWordSet(
			"int float str bool bytes bytearray list dict set frozenset tuple object type complex " +
				"Any Optional Union List Dict Set Tuple Sequence Mapping Callable Iterable Iterator",
		),
		builtins: makeWordSet(
			"print len range enumerate zip map filter sorted reversed sum min max abs round pow " +
				"open input repr id hash dir vars getattr setattr hasattr delattr isinstance issubclass " +
				"super property staticmethod classmethod chr ord hex oct bin format all any iter next " +
				"eval exec compile globals locals",
		),
	},
	"javascript": {
		slashComment: true,
		blockComment: true,
		backtickStr:  true,
		singleStr:    true,
		keywords: makeWordSet(
			"async await break case catch class const continue debugger default delete do else " +
				"export extends false finally for from function if import in instanceof let new null " +
				"of return static super switch this throw true try typeof undefined var void while " +
				"with yield",
		),
		types: makeWordSet(
			"Array Object String Number Boolean Symbol BigInt Map Set WeakMap WeakSet Promise " +
				"Date RegExp Error Function Uint8Array Buffer URL",
		),
		builtins: makeWordSet(
			"console Math JSON parseInt parseFloat isNaN isFinite require setTimeout setInterval " +
				"clearTimeout clearInterval fetch encodeURIComponent decodeURIComponent",
		),
	},
	"typescript": {
		slashComment: true,
		blockComment: true,
		backtickStr:  true,
		singleStr:    true,
		keywords: makeWordSet(
			"abstract as async await break case catch class const continue debugger declare default " +
				"delete do else enum export extends false finally for from function get if implements " +
				"import in instanceof interface is keyof let module namespace new null of override " +
				"private protected public readonly return satisfies set static super switch this throw " +
				"true try type typeof undefined var void while with yield",
		),
		types: makeWordSet(
			"string number boolean any unknown never void object symbol bigint " +
				"Array Object String Number Boolean Symbol BigInt Map Set Promise Date RegExp Error " +
				"Record Partial Required Readonly Pick Omit Exclude Extract NonNullable ReturnType",
		),
		builtins: makeWordSet(
			"console Math JSON parseInt parseFloat isNaN isFinite setTimeout setInterval fetch",
		),
	},
	"go": {
		slashComment: true,
		blockComment: true,
		backtickStr:  true,
		singleStr:    true,
		keywords: makeWordSet(
			"break case chan const continue default defer else fallthrough for func go goto if " +
				"import interface map package range return select struct switch type var nil true false iota",
		),
		types: makeWordSet(
			"any bool byte comparable complex64 complex128 error float32 float64 int int8 int16 " +
				"int32 int64 rune string uint uint8 uint16 uint32 uint64 uintptr",
		),
		builtins: makeWordSet(
			"append cap clear close complex copy delete imag len make max min new panic print println real recover",
		),
	},
	"json": {
		slashComment: true,
		blockComment: true,
		isJSON:       true,
		keywords:     makeWordSet("true false null"),
	},
	"bash": {
		hashComment: true,
		singleStr:   true,
		backtickStr: true,
		dollarVar:   true,
		keywords: makeWordSet(
			"if then elif else fi for in do done while until case esac function select time coproc " +
				"local export readonly declare typeset unset return exit break continue shift trap " +
				"source eval exec set shopt true false",
		),
		builtins: makeWordSet(
			"echo printf cd pwd ls cat grep rg awk sed jq curl find mkdir rm cp mv touch chmod chown " +
				"head tail sort uniq wc tr cut xargs read test",
		),
	},
	"sql": {
		dashComment:  true,
		blockComment: true,
		singleStr:    true,
		sqlCaseFold:  true,
		keywords: makeWordSet(
			"select from where insert into values update set delete create alter drop table index " +
				"view join inner left right full outer cross on as and or not in is null true false " +
				"between like ilike exists case when then else end group by order asc desc having " +
				"limit offset union all distinct with recursive returning primary key foreign references " +
				"unique check default constraint begin commit rollback transaction",
		),
		types: makeWordSet(
			"int integer bigint smallint tinyint serial bigserial text varchar char character " +
				"boolean bool real double precision float numeric decimal date time timestamp " +
				"timestamptz interval json jsonb uuid bytea blob",
		),
		builtins: makeWordSet(
			"count sum avg min max coalesce nullif cast concat lower upper trim length substr substring " +
				"now current_timestamp row_number rank dense_rank lag lead",
		),
	},
	"yaml": {
		hashComment: true,
		singleStr:   true,
		isYAML:      true,
		keywords:    makeWordSet("true false null yes no on off"),
	},
	"toml": {
		hashComment: true,
		singleStr:   true,
		isTOML:      true,
		keywords:    makeWordSet("true false"),
	},
	"rust": {
		slashComment: true,
		blockComment: true,
		singleStr:    true,
		isRust:       true,
		keywords: makeWordSet(
			"as async await break const continue crate dyn else enum extern false fn for if impl in " +
				"let loop match mod move mut pub ref return self Self static struct super trait true type " +
				"unsafe use where while",
		),
		types: makeWordSet(
			"bool char str i8 i16 i32 i64 i128 isize u8 u16 u32 u64 u128 usize f32 f64 " +
				"String Vec Option Result Box Arc Rc HashMap HashSet BTreeMap BTreeSet Path PathBuf",
		),
		builtins: makeWordSet(
			"Some None Ok Err println print eprintln eprint format vec panic todo unimplemented assert assert_eq",
		),
	},
}

type hlState struct {
	inBlockComment bool
	tripleQuote    byte // 0 if not in multiline triple-quoted string, '\'' or '"'
	inBacktick     bool
}

func isRustLifetimeStart(rest string) bool {
	if len(rest) == 0 {
		return false
	}
	if rest[0] == '\\' {
		return false
	}
	r, sz := utf8.DecodeRuneInString(rest)
	if sz < len(rest) && rest[sz] == '\'' {
		return false
	}
	return r == '_' || unicode.IsLetter(r)
}

func isHLIdentStart(r rune) bool {
	return r == '_' || unicode.IsLetter(r)
}

func isHLIdentPart(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func isHLNumberBody(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F') ||
		b == '_' || b == '.' || b == 'x' || b == 'X' || b == 'o' || b == 'O' || b == 'b' || b == 'B'
}

func isHLOperatorChar(r rune) bool {
	switch r {
	case '+', '-', '*', '/', '%', '=', '!', '<', '>', '&', '|', '^', '~', '?', '@':
		return true
	}
	return false
}

func isHLPunctChar(r rune) bool {
	switch r {
	case '(', ')', '[', ']', '{', '}', ',', ';', ':', '.':
		return true
	}
	return false
}

// highlightCodeLines highlights a slice of sanitized source lines in lang using
// omp's exact titanium syntax foreground palette tokens. For unsupported or
// empty languages it returns nil so callers can apply their default styling.
func (p palette) highlightCodeLines(lines []string, lang string) []string {
	canon := normalizeHighlightLang(lang)
	if canon == "" {
		return nil
	}
	spec := hlSpecs[canon]
	if spec == nil {
		return nil
	}
	st := &hlState{}
	out := make([]string, len(lines))
	for idx, line := range lines {
		out[idx] = p.highlightLineWithState(line, spec, st)
	}
	return out
}

// highlightCodeLine highlights a single line of code in lang without cross-line state.
func (p palette) highlightCodeLine(line, lang string) string {
	canon := normalizeHighlightLang(lang)
	if canon == "" {
		return p.fg("toolOutput", line)
	}
	spec := hlSpecs[canon]
	if spec == nil {
		return p.fg("toolOutput", line)
	}
	return p.highlightLineWithState(line, spec, &hlState{})
}

func (p palette) highlightLineWithState(line string, spec *hlLangSpec, st *hlState) string {
	if line == "" {
		return ""
	}
	var out strings.Builder
	out.Grow(len(line) * 2)
	emit := func(tok, text string) {
		if text == "" {
			return
		}
		if tok == "" {
			out.WriteString(text)
			return
		}
		out.WriteString(p.fg(tok, text))
	}

	i := 0
	n := len(line)

	// Continue multiline block comment
	if st.inBlockComment {
		if end := strings.Index(line, "*/"); end >= 0 {
			emit("syntaxComment", line[:end+2])
			i = end + 2
			st.inBlockComment = false
		} else {
			emit("syntaxComment", line)
			return out.String()
		}
	}

	// Continue multiline Python triple-quoted string
	if st.tripleQuote != 0 {
		delim := string([]byte{st.tripleQuote, st.tripleQuote, st.tripleQuote})
		start := i
		closed := false
		for i < n {
			if line[i] == '\\' && i+1 < n {
				i += 2
				continue
			}
			if strings.HasPrefix(line[i:], delim) {
				i += 3
				st.tripleQuote = 0
				closed = true
				break
			}
			_, sz := utf8.DecodeRuneInString(line[i:])
			i += sz
		}
		emit("syntaxString", line[start:i])
		if !closed {
			return out.String()
		}
	}

	// Continue multiline backtick string (JS/Go)
	if st.inBacktick {
		start := i
		closed := false
		for i < n {
			if line[i] == '\\' && !spec.isJSON && i+1 < n && spec.tripleQuotes {
				i += 2
				continue
			}
			if line[i] == '`' {
				i++
				st.inBacktick = false
				closed = true
				break
			}
			_, sz := utf8.DecodeRuneInString(line[i:])
			i += sz
		}
		emit("syntaxString", line[start:i])
		if !closed {
			return out.String()
		}
	}

	// Leading whitespace
	wsStart := i
	for i < n && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if i > wsStart {
		emit("", line[wsStart:i])
	}

	// TOML section header `[section.name]`
	if spec.isTOML && i < n && line[i] == '[' {
		end := strings.IndexByte(line[i:], ']')
		if end > 0 {
			emit("syntaxPunctuation", "[")
			emit("syntaxType", line[i+1:i+end])
			emit("syntaxPunctuation", "]")
			i = i + end + 1
		}
	}

	for i < n {
		c := line[i]

		// Whitespace
		if c == ' ' || c == '\t' {
			start := i
			for i < n && (line[i] == ' ' || line[i] == '\t') {
				i++
			}
			emit("", line[start:i])
			continue
		}

		// Line comments
		if (spec.hashComment && c == '#') ||
			(spec.slashComment && c == '/' && i+1 < n && line[i+1] == '/') ||
			(spec.dashComment && c == '-' && i+1 < n && line[i+1] == '-') {
			emit("syntaxComment", line[i:])
			return out.String()
		}

		// Block comments /* ... */
		if spec.blockComment && c == '/' && i+1 < n && line[i+1] == '*' {
			start := i
			i += 2
			if end := strings.Index(line[i:], "*/"); end >= 0 {
				i += end + 2
				emit("syntaxComment", line[start:i])
			} else {
				st.inBlockComment = true
				emit("syntaxComment", line[start:])
				return out.String()
			}
			continue
		}

		// Python string prefixes: f"...", r'...', b"...", u'...', fr"..."
		if spec.tripleQuotes && (c == 'f' || c == 'F' || c == 'r' || c == 'R' || c == 'b' || c == 'B' || c == 'u' || c == 'U') {
			j := i
			for j < n && j-i < 2 && strings.ContainsRune("fFrRbBuU", rune(line[j])) {
				j++
			}
			if j < n && (line[j] == '"' || line[j] == '\'') {
				q := line[j]
				start := i
				if j+2 < n && line[j+1] == q && line[j+2] == q {
					i = j + 3
					delim := string([]byte{q, q, q})
					closed := false
					for i < n {
						if line[i] == '\\' && i+1 < n {
							i += 2
							continue
						}
						if strings.HasPrefix(line[i:], delim) {
							i += 3
							closed = true
							break
						}
						_, sz := utf8.DecodeRuneInString(line[i:])
						i += sz
					}
					if !closed {
						st.tripleQuote = q
					}
					emit("syntaxString", line[start:i])
					continue
				}
				i = j + 1
				for i < n {
					if line[i] == '\\' && i+1 < n {
						i += 2
						continue
					}
					if line[i] == q {
						i++
						break
					}
					_, sz := utf8.DecodeRuneInString(line[i:])
					i += sz
				}
				emit("syntaxString", line[start:i])
				continue
			}
		}

		// Triple-quoted strings
		if spec.tripleQuotes && (c == '"' || c == '\'') && i+2 < n && line[i+1] == c && line[i+2] == c {
			q := c
			start := i
			i += 3
			delim := string([]byte{q, q, q})
			closed := false
			for i < n {
				if line[i] == '\\' && i+1 < n {
					i += 2
					continue
				}
				if strings.HasPrefix(line[i:], delim) {
					i += 3
					closed = true
					break
				}
				_, sz := utf8.DecodeRuneInString(line[i:])
				i += sz
			}
			if !closed {
				st.tripleQuote = q
			}
			emit("syntaxString", line[start:i])
			continue
		}

		// Double-quoted strings
		if c == '"' {
			start := i
			i++
			for i < n {
				if line[i] == '\\' && i+1 < n {
					i += 2
					continue
				}
				if line[i] == '"' {
					i++
					break
				}
				_, sz := utf8.DecodeRuneInString(line[i:])
				i += sz
			}
			tok := "syntaxString"
			if spec.isJSON && strings.HasPrefix(strings.TrimLeft(line[i:], " \t"), ":") {
				tok = "syntaxVariable"
			}
			if tok == "syntaxString" && i-start >= 2 && line[start] == '"' && line[i-1] == '"' {
				emit("syntaxPunctuation", `"`)
				emit("syntaxString", line[start+1:i-1])
				emit("syntaxPunctuation", `"`)
			} else {
				emit(tok, line[start:i])
			}
			continue
		}

		// Single-quoted strings
		if c == '\'' && spec.singleStr {
			if spec.isRust && isRustLifetimeStart(line[i+1:]) {
				start := i
				i++
				for i < n {
					r, sz := utf8.DecodeRuneInString(line[i:])
					if !isHLIdentPart(r) {
						break
					}
					i += sz
				}
				emit("syntaxType", line[start:i])
				continue
			}
			start := i
			i++
			for i < n {
				if line[i] == '\\' && i+1 < n {
					i += 2
					continue
				}
				if line[i] == '\'' {
					i++
					break
				}
				_, sz := utf8.DecodeRuneInString(line[i:])
				i += sz
			}
			if i-start >= 2 && line[start] == '\'' && line[i-1] == '\'' {
				emit("syntaxPunctuation", "'")
				emit("syntaxString", line[start+1:i-1])
				emit("syntaxPunctuation", "'")
			} else {
				emit("syntaxString", line[start:i])
			}
			continue
		}

		// Backtick template / raw strings
		if c == '`' && spec.backtickStr {
			start := i
			i++
			closed := false
			for i < n {
				if line[i] == '\\' && i+1 < n && !spec.isRust {
					i += 2
					continue
				}
				if line[i] == '`' {
					i++
					closed = true
					break
				}
				_, sz := utf8.DecodeRuneInString(line[i:])
				i += sz
			}
			if !closed {
				st.inBacktick = true
			}
			emit("syntaxString", line[start:i])
			continue
		}

		// Bash variables: $FOO, ${FOO}, $1, $?
		if spec.dollarVar && c == '$' && i+1 < n {
			start := i
			i++
			if line[i] == '{' {
				i++
				for i < n && line[i] != '}' {
					i++
				}
				if i < n {
					i++
				}
				emit("syntaxVariable", line[start:i])
				continue
			}
			if isIdentStart(line[i]) {
				for i < n && isIdentChar(line[i]) {
					i++
				}
				emit("syntaxVariable", line[start:i])
				continue
			}
			if strings.ContainsRune("0123456789?#@*-$!", rune(line[i])) {
				i++
				emit("syntaxVariable", line[start:i])
				continue
			}
			emit("syntaxOperator", "$")
			continue
		}

		// Numbers
		if (c >= '0' && c <= '9') || (c == '.' && i+1 < n && line[i+1] >= '0' && line[i+1] <= '9') {
			start := i
			if c == '0' && i+1 < n && (line[i+1] == 'x' || line[i+1] == 'X' || line[i+1] == 'b' || line[i+1] == 'B' || line[i+1] == 'o' || line[i+1] == 'O') {
				i += 2
				for i < n && isHLNumberBody(line[i]) {
					i++
				}
			} else {
				i++
				for i < n && ((line[i] >= '0' && line[i] <= '9') || line[i] == '_' || line[i] == '.') {
					i++
				}
				if i < n && (line[i] == 'e' || line[i] == 'E') {
					i++
					if i < n && (line[i] == '+' || line[i] == '-') {
						i++
					}
					for i < n && ((line[i] >= '0' && line[i] <= '9') || line[i] == '_') {
						i++
					}
				}
			}
			emit("syntaxNumber", line[start:i])
			continue
		}

		// Identifiers, keywords, types, functions
		r, sz := utf8.DecodeRuneInString(line[i:])
		if isHLIdentStart(r) {
			start := i
			i += sz
			for i < n {
				r2, sz2 := utf8.DecodeRuneInString(line[i:])
				if !isHLIdentPart(r2) && !((spec.isYAML || spec.isTOML) && r2 == '-') {
					break
				}
				i += sz2
			}
			word := line[start:i]
			lookup := word
			if spec.sqlCaseFold {
				lookup = strings.ToLower(word)
			}
			after := strings.TrimLeft(line[i:], " \t")
			var tok string
			switch {
			case (spec.isYAML && strings.HasPrefix(after, ":")) || (spec.isTOML && strings.HasPrefix(after, "=")):
				tok = "syntaxVariable"
			case spec.keywords[lookup]:
				tok = "syntaxKeyword"
			case spec.types[lookup]:
				tok = "syntaxType"
			case strings.HasPrefix(after, "(") || (spec.isRust && strings.HasPrefix(after, "!(")):
				tok = "syntaxFunction"
			case spec.builtins[lookup]:
				tok = "syntaxFunction"
			default:
				tok = "syntaxVariable"
			}
			emit(tok, word)
			continue
		}

		// Operators
		if isHLOperatorChar(r) {
			start := i
			i += sz
			// Group multi-char operators cleanly (e.g. ==, !=, <=, >=, ->, =>, &&, ||, **, //, :=)
			for i < n {
				r2, sz2 := utf8.DecodeRuneInString(line[i:])
				if !isHLOperatorChar(r2) {
					break
				}
				i += sz2
			}
			emit("syntaxOperator", line[start:i])
			continue
		}

		// Punctuation
		if isHLPunctChar(r) {
			emit("syntaxPunctuation", line[i:i+sz])
			i += sz
			continue
		}

		// Fallback single rune
		emit("toolOutput", line[i:i+sz])
		i += sz
	}

	return out.String()
}
