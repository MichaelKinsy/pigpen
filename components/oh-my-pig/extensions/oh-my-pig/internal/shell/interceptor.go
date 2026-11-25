package shell

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// upstream: packages/coding-agent/src/tools/bash-interceptor.ts,
// packages/coding-agent/src/exec/settings.ts:29-110

// InterceptorRule mirrors omp's BashInterceptorRule (exec/settings.ts:8).
type InterceptorRule struct {
	Pattern string
	Flags   string
	Tool    string
	Message string
	// matcher optionally implements upstream rules that use JS lookaround
	// (unavailable in Go RE2) with exact linear-time Go matching.
	matcher func(candidate string) bool
}

// InterceptionResult mirrors omp's InterceptionResult (bash-interceptor.ts:11).
type InterceptionResult struct {
	Block         bool
	Message       string
	SuggestedTool string
}

const (
	// Exact upstream pattern strings (exec/settings.ts:29-110) used to recognize
	// default rules even when supplied via settings (`bashInterceptor.patterns`).
	defaultPatternCatRead    = `^\s*(?:(?:head|tail|less|more)\s+|cat\s+(?!\s|(?:[02-9]>{1,2}\|?\s*[^\s>|]\S*\s+)*(?:<<|1?>(?!&)|&>)))`
	defaultPatternEchoWrite  = `^\s*(?:(?:echo|printf)\s+|cat\s*<<|cat(?=\s+(?:\d+|&)?>)\s+)(?:(?:\\[\s\S]|\$\((?:[^()"'` + "`" + `]|"[^"]*"|'[^']*'|` + "`" + `[^` + "`" + `]*` + "`" + `|\([^()]*\))*\)|\$\{[^{}]*\}|` + "`" + `[^` + "`" + `]*` + "`" + `|\$(?!\((?:[^()"'` + "`" + `]|"[^"]*"|'[^']*'|` + "`" + `[^` + "`" + `]*` + "`" + `|\([^()]*\))*\)|\{[^{}]*\})|` + "`" + `(?![^` + "`" + `]*` + "`" + `)|[^"'>;&|\n\\$` + "`" + `]|(?<=[<>])&|(?<![<>])&(?=>)|(?<=(?:^|\s)[02-9])>{1,2}\|?|"[^"]*"|'[^']*')|(?<!\||>|(?:^|\s)[02-9])>{1,2}\|?\s*(?:"/dev/(?:null|tty|stdout|stderr)"|'/dev/(?:null|tty|stdout|stderr)'|/dev/(?:null|tty|stdout|stderr))(?=[\s;&|]|$))*(?<!\||>|(?:^|\s)[02-9])>{1,2}\|?\s*(?!(?:"/dev/(?:null|tty|stdout|stderr)"|'/dev/(?:null|tty|stdout|stderr)'|/dev/(?:null|tty|stdout|stderr))(?:[\s;&|]|$))[$\w./~"'` + "-" + `]`
	defaultPatternNohupBg    = `^\s*nohup\s+|(?<!&)\&\s*$`
	defaultPatternDevService = `^\s*(?:(?:bun|npm|pnpm|yarn)\s+(?:run\s+)?(?:dev|start)(?:\s|$)|(?:vite|next\s+dev|nuxt\s+dev|nodemon|lldb|gdb|tail\s+-f)(?:\s|$)|docker\s+compose\s+up(?!.*(?:\s-d(?:\s|$)|--detach))(?:\s|$))`
	defaultPatternWatchMode  = `^\s*(?:(?:bun|npm|pnpm|yarn)\s+(?:run\s+)?\S+|cargo\s+watch|watchexec|pytest|vitest|jest|tsc)(?:.|\n)*(?:--watch|-w)(?:\s|$)`
)

// DefaultInterceptorRules mirrors all 10 rules of omp's
// DEFAULT_BASH_INTERCEPTOR_RULES (`exec/settings.ts:29-110`).
// Rules whose JS regex uses lookaround are backed by exact Go matchers whose
// language matches omp's JS regular expressions verbatim.
var DefaultInterceptorRules = []InterceptorRule{
	{
		Pattern: defaultPatternCatRead,
		Tool:    "read",
		Message: "Use the `read` tool instead of cat/head/tail. It provides better context and handles binary files.",
		matcher: matchesCatReadRule,
	},
	{
		Pattern: `^\s*(grep|rg|ripgrep|ag|ack)\s+`,
		Tool:    "grep",
		Message: "Use the `grep` tool instead of grep/rg. It respects .gitignore and provides structured output.",
	},
	{
		Pattern: `^\s*(find|fd|locate)\s+.*(-name|-iname|-type|--type|-glob)`,
		Tool:    "glob",
		Message: "Use the `glob` tool instead of find/fd. It respects .gitignore and is faster for glob patterns.",
	},
	{
		Pattern: `^\s*sed\s+(-i|--in-place)`,
		Tool:    "edit",
		Message: "Use the `edit` tool instead of sed -i. It provides diff preview and fuzzy matching.",
	},
	{
		Pattern: `^\s*perl\s+.*-[pn]?i`,
		Tool:    "edit",
		Message: "Use the `edit` tool instead of perl -i. It provides diff preview and fuzzy matching.",
	},
	{
		Pattern: `^\s*awk\s+.*-i\s+inplace`,
		Tool:    "edit",
		Message: "Use the `edit` tool instead of awk -i inplace. It provides diff preview and fuzzy matching.",
	},
	{
		Pattern: defaultPatternEchoWrite,
		Tool:    "write",
		Message: "Use the `write` tool instead of echo/cat redirection. It handles encoding and provides confirmation. `write` replaces the whole file; to append to an existing file, use `edit` to insert after its last line.",
		matcher: matchesEchoCatWriteRule,
	},
	{
		Pattern: defaultPatternNohupBg,
		Tool:    "bash",
		Message: "Use `bash` with `name` instead of nohup or background shell syntax so the service stays observable and managed.",
		matcher: matchesNohupOrBackgroundRule,
	},
	{
		Pattern: defaultPatternDevService,
		Tool:    "bash",
		Message: "Use `bash` with `name` for services, watchers, and debuggers; inspect with `read proc://<name>`.",
		matcher: matchesDevServiceRule,
	},
	{
		Pattern: defaultPatternWatchMode,
		Tool:    "bash",
		Message: "Use `bash` with `name` for watch mode so its output, input, and lifecycle stay managed.",
	},
}

const jsSpaceClass = `[\t\n\v\f\r \x{00A0}\x{1680}\x{2000}-\x{200A}\x{2028}\x{2029}\x{202F}\x{205F}\x{3000}\x{FEFF}]`
const jsNonSpaceClass = `[^\t\n\v\f\r \x{00A0}\x{1680}\x{2000}-\x{200A}\x{2028}\x{2029}\x{202F}\x{205F}\x{3000}\x{FEFF}]`

var (
	reHeadTailLessMore    = regexp.MustCompile(`^` + jsSpaceClass + `*(?:head|tail|less|more)` + jsSpaceClass + `+`)
	reCatLead             = regexp.MustCompile(`^` + jsSpaceClass + `*cat` + jsSpaceClass + `+`)
	reLeadNonStdoutRed    = regexp.MustCompile(`^[02-9]>{1,2}\|?` + jsSpaceClass + `*[^\t\n\v\f\r \x{00A0}\x{1680}\x{2000}-\x{200A}\x{2028}\x{2029}\x{202F}\x{205F}\x{3000}\x{FEFF}>|]` + jsNonSpaceClass + `*` + jsSpaceClass + `+`)
	reNohupLead           = regexp.MustCompile(`^` + jsSpaceClass + `*nohup` + jsSpaceClass + `+`)
	reDevServiceSimple    = regexp.MustCompile(`^` + jsSpaceClass + `*(?:(?:bun|npm|pnpm|yarn)` + jsSpaceClass + `+(?:run` + jsSpaceClass + `+)?(?:dev|start)(?:` + jsSpaceClass + `|$)|(?:vite|next` + jsSpaceClass + `+dev|nuxt` + jsSpaceClass + `+dev|nodemon|lldb|gdb|tail` + jsSpaceClass + `+-f)(?:` + jsSpaceClass + `|$))`)
	reDockerComposeUp     = regexp.MustCompile(`^` + jsSpaceClass + `*docker` + jsSpaceClass + `+compose` + jsSpaceClass + `+up(?:` + jsSpaceClass + `|$)`)
	reDockerDetach        = regexp.MustCompile(`(?:` + jsSpaceClass + `-d(?:` + jsSpaceClass + `|$)|--detach)`)
	reDockerDetachAtStart = regexp.MustCompile(`^(?:` + jsSpaceClass + `-d(?:` + jsSpaceClass + `|$)|--detach)`)

	interceptorWarnMu   sync.Mutex
	interceptorWarnedBy = map[string]map[string]bool{}
)

func builtinMatcherForPattern(pattern, flags string) func(string) bool {
	if flags != "" {
		return nil
	}
	switch pattern {
	case defaultPatternCatRead:
		return matchesCatReadRule
	case defaultPatternEchoWrite:
		return matchesEchoCatWriteRule
	case defaultPatternNohupBg:
		return matchesNohupOrBackgroundRule
	case defaultPatternDevService:
		return matchesDevServiceRule
	default:
		return nil
	}
}

// isJSSpaceRune reports whether r matches JavaScript RegExp `\s`
// (ECMA-262 WhiteSpace + LineTerminator).
func isJSSpaceRune(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\v', '\f', '\r',
		0x00A0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

func skipJSSpaces(s string, i int) int {
	for i < len(s) {
		r, sz := utf8.DecodeRuneInString(s[i:])
		if !isJSSpaceRune(r) {
			break
		}
		i += sz
	}
	return i
}

// precededByJSSpace reports whether the character immediately before pos
// matches JS `\s` (or pos == 0 when allowStart is true).
func precededByJSSpace(s string, pos int, allowStart bool) bool {
	if pos == 0 {
		return allowStart
	}
	r, _ := utf8.DecodeLastRuneInString(s[:pos])
	return isJSSpaceRune(r)
}

func isJSSpaceAt(s string, pos int) bool {
	if pos >= len(s) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(s[pos:])
	return isJSSpaceRune(r)
}

func isSpaceByte(ch byte) bool {
	return ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' || ch == '\v' || ch == '\f'
}

// matchesCatReadRule implements DEFAULT_BASH_INTERCEPTOR_RULES[0] (`exec/settings.ts:38`).
func matchesCatReadRule(candidate string) bool {
	if reHeadTailLessMore.MatchString(candidate) {
		return true
	}
	loc := reCatLead.FindStringIndex(candidate)
	if loc == nil {
		return false
	}
	rest := candidate[loc[1]:]
	if rest == "" {
		return true
	}
	for {
		m := reLeadNonStdoutRed.FindStringIndex(rest)
		if m == nil {
			break
		}
		rest = rest[m[1]:]
	}
	if strings.HasPrefix(rest, "<<") || strings.HasPrefix(rest, "&>") {
		return false
	}
	if strings.HasPrefix(rest, "1>") {
		if !strings.HasPrefix(rest, "1>&") {
			return false
		}
	} else if strings.HasPrefix(rest, ">") {
		if !strings.HasPrefix(rest, ">&") {
			return false
		}
	}
	return true
}

// matchesNohupOrBackgroundRule implements DEFAULT_BASH_INTERCEPTOR_RULES[7] (`exec/settings.ts:93`).
func matchesNohupOrBackgroundRule(candidate string) bool {
	if reNohupLead.MatchString(candidate) {
		return true
	}
	end := len(candidate)
	for end > 0 {
		r, sz := utf8.DecodeLastRuneInString(candidate[:end])
		if !isJSSpaceRune(r) {
			break
		}
		end -= sz
	}
	if end == 0 || candidate[end-1] != '&' {
		return false
	}
	if end >= 2 && candidate[end-2] == '&' {
		return false
	}
	return true
}

// matchesDevServiceRule implements DEFAULT_BASH_INTERCEPTOR_RULES[8] (`exec/settings.ts:100`).
func matchesDevServiceRule(candidate string) bool {
	if reDevServiceSimple.MatchString(candidate) {
		return true
	}
	loc := reDockerComposeUp.FindStringIndex(candidate)
	if loc == nil {
		return false
	}
	checkFrom := loc[1]
	if checkFrom > 0 {
		r, sz := utf8.DecodeLastRuneInString(candidate[:checkFrom])
		if isJSSpaceRune(r) {
			checkFrom -= sz
		}
	}
	// The lookahead `(?!.*(?:\s-d(?:\s|$)|--detach))` runs at `checkFrom` (just
	// after `up`). When `.*` matches 0 chars, `\s-d` can match a leading `\n-d`
	// directly at `checkFrom` even though `.*` itself stops before a newline.
	tail := candidate[checkFrom:]
	if reDockerDetachAtStart.MatchString(tail) {
		return false
	}
	lineRest := tail
	// JS `.` matches any char except LF, CR, U+2028, U+2029.
	for idx, r := range lineRest {
		if r == '\n' || r == '\r' || r == 0x2028 || r == 0x2029 {
			lineRest = lineRest[:idx]
			break
		}
	}
	return !reDockerDetach.MatchString(lineRest)
}

func isWriteTargetStartByte(ch byte) bool {
	if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') {
		return true
	}
	switch ch {
	case '$', '_', '.', '/', '~', '"', '\'', '-':
		return true
	}
	return false
}

var allowedDevSinks = []string{
	`"/dev/null"`, `'/dev/null'`, `/dev/null`,
	`"/dev/tty"`, `'/dev/tty'`, `/dev/tty`,
	`"/dev/stdout"`, `'/dev/stdout'`, `/dev/stdout`,
	`"/dev/stderr"`, `'/dev/stderr'`, `/dev/stderr`,
}

// matchAllowedDevSink checks `(?:"/dev/..."|'/dev/...'|/dev/...)(?=[\s;&|]|$)` at pos.
func matchAllowedDevSink(s string, pos int) (int, bool) {
	rest := s[pos:]
	for _, sink := range allowedDevSinks {
		if strings.HasPrefix(rest, sink) {
			after := pos + len(sink)
			if after == len(s) || isJSSpaceAt(s, after) || s[after] == ';' || s[after] == '&' || s[after] == '|' {
				return after, true
			}
		}
	}
	return pos, false
}

// matchCommandSubst matches omp's exact subpattern:
// `\$\((?:[^()"'`]|"[^"]*"|'[^']*'|`[^`]*`|\([^()]*\))*\)`
// (no backslash escapes inside double quotes; only one level of plain `(...)`).
func matchCommandSubst(s string, start int) (int, bool) {
	if start+1 >= len(s) || s[start] != '$' || s[start+1] != '(' {
		return start, false
	}
	i := start + 2
	for i < len(s) {
		ch := s[i]
		switch ch {
		case ')':
			return i + 1, true
		case '"':
			end := strings.IndexByte(s[i+1:], '"')
			if end < 0 {
				return start, false
			}
			i = i + 1 + end + 1
		case '\'':
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				return start, false
			}
			i = i + 1 + end + 1
		case '`':
			end := strings.IndexByte(s[i+1:], '`')
			if end < 0 {
				return start, false
			}
			i = i + 1 + end + 1
		case '(':
			i++
			for i < len(s) && s[i] != '(' && s[i] != ')' {
				i++
			}
			if i >= len(s) || s[i] != ')' {
				return start, false
			}
			i++
		default:
			i++
		}
	}
	return start, false
}

// matchParamExpansion matches omp's exact subpattern `\$\{[^{}]*\}`
// (non-nesting, no backslash escapes).
func matchParamExpansion(s string, start int) (int, bool) {
	if start+1 >= len(s) || s[start] != '$' || s[start+1] != '{' {
		return start, false
	}
	i := start + 2
	for i < len(s) {
		if s[i] == '{' {
			return start, false
		}
		if s[i] == '}' {
			return i + 1, true
		}
		i++
	}
	return start, false
}

// isNonStdoutFdPrefix reports whether the bytes immediately before pos match
// `(?:^|\s)[02-9]` (using JS `\s`).
func isNonStdoutFdPrefix(s string, pos int) bool {
	if pos == 0 {
		return false
	}
	d := s[pos-1]
	if d < '0' || d > '9' || d == '1' {
		return false
	}
	return precededByJSSpace(s, pos-1, true)
}

// isDisallowedWriteRedirectPrefix reports whether pos fails
// `(?<!\||>|(?:^|\s)[02-9])`.
func isDisallowedWriteRedirectPrefix(s string, pos int) bool {
	if pos == 0 {
		return false
	}
	prev := s[pos-1]
	if prev == '|' || prev == '>' {
		return true
	}
	return isNonStdoutFdPrefix(s, pos)
}

// matchWriteRedirectAt checks omp's exact final redirect expression at pos:
// `(?<!\||>|(?:^|\s)[02-9])>{1,2}\|?\s*(?!(?:allowedDevSink)(?:[\s;&|]|$))[$\w./~"'-]`
// Trying both `>>|`/`>>`/`>|`/`>` lengths (in greedy order) just as the JS regex
// engine can backtrack between `>>` and `>` when needed.
func matchWriteRedirectAt(s string, pos int) bool {
	if pos >= len(s) || s[pos] != '>' {
		return false
	}
	if isDisallowedWriteRedirectPrefix(s, pos) {
		return false
	}
	// Candidate operator lengths in greedy order: >>|, >>, >|, >.
	var lens [4]int
	nLens := 0
	if pos+1 < len(s) && s[pos+1] == '>' {
		if pos+2 < len(s) && s[pos+2] == '|' {
			lens[nLens] = 3
			nLens++
		}
		lens[nLens] = 2
		nLens++
	}
	if pos+1 < len(s) && s[pos+1] == '|' {
		lens[nLens] = 2
		nLens++
	}
	lens[nLens] = 1
	nLens++

	for k := 0; k < nLens; k++ {
		afterOp := pos + lens[k]
		ws := skipJSSpaces(s, afterOp)
		if ws >= len(s) {
			continue
		}
		if _, isSink := matchAllowedDevSink(s, ws); isSink {
			continue
		}
		if isWriteTargetStartByte(s[ws]) {
			return true
		}
	}
	return false
}

// matchEchoWritePrefix implements the anchored prefix of DEFAULT_BASH_INTERCEPTOR_RULES[6]:
// `^\s*(?:(?:echo|printf)\s+|cat\s*<<|cat(?=\s+(?:\d+|&)?>)\s+)`
// using JS `\s`.
func matchEchoWritePrefix(s string) (int, bool) {
	i := skipJSSpaces(s, 0)
	if strings.HasPrefix(s[i:], "echo") {
		j := i + 4
		k := skipJSSpaces(s, j)
		if k > j {
			return k, true
		}
	}
	if strings.HasPrefix(s[i:], "printf") {
		j := i + 6
		k := skipJSSpaces(s, j)
		if k > j {
			return k, true
		}
	}
	if strings.HasPrefix(s[i:], "cat") {
		j := i + 3
		k := skipJSSpaces(s, j)
		if strings.HasPrefix(s[k:], "<<") {
			return k + 2, true
		}
		if k > j {
			// Lookahead `(?=\s+(?:\d+|&)?>)`
			p := k
			if p < len(s) {
				if s[p] == '&' {
					p++
				} else if s[p] >= '0' && s[p] <= '9' {
					for p < len(s) && s[p] >= '0' && s[p] <= '9' {
						p++
					}
				}
				if p < len(s) && s[p] == '>' {
					return k, true
				}
			}
		}
	}
	return 0, false
}

// matchesEchoCatWriteRule implements DEFAULT_BASH_INTERCEPTOR_RULES[6] (`exec/settings.ts:87`)
// with exact equivalence to omp's JavaScript RegExp (including non-escaping
// `"[^"]*"`, non-nesting `\$\{[^{}]*\}`, single-level `\([^()]*\)` inside `$(...)`,
// JS `\s`, and exact redirect/dev-sink lookaround and backtracking semantics).
func matchesEchoCatWriteRule(candidate string) bool {
	start, ok := matchEchoWritePrefix(candidate)
	if !ok {
		return false
	}

	// Memoize visited scan positions so even adversarial inputs stay O(n).
	// Most positions have a unique deterministic transition; only `>` in
	// `(?<=(?:^|\s)[02-9])>{1,2}\|?` or dev-sink redirects can branch on
	// operator length (e.g. `>>|` vs `>>` vs `>|` vs `>`).
	visited := make([]bool, len(candidate)+1)
	stack := []int{start}
	visited[start] = true

	push := func(pos int) {
		if pos <= len(candidate) && !visited[pos] {
			visited[pos] = true
			stack = append(stack, pos)
		}
	}

	for len(stack) > 0 {
		i := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		if i >= len(candidate) {
			continue
		}

		// Try matching the final real-file write redirect at position i.
		if candidate[i] == '>' && matchWriteRedirectAt(candidate, i) {
			return true
		}

		// Advance one body atom matching the `(?:...|devSink)*` loop of `exec/settings.ts:87`:
		ch := candidate[i]
		switch ch {
		case '\\':
			// `\\[\s\S]` consumes backslash + any single UTF-16/UTF-8 char.
			if i+1 < len(candidate) {
				_, sz := utf8.DecodeRuneInString(candidate[i+1:])
				push(i + 1 + sz)
			}
		case '$':
			if next, ok := matchCommandSubst(candidate, i); ok {
				push(next)
			} else if next, ok := matchParamExpansion(candidate, i); ok {
				push(next)
			} else {
				// `\$(?!\(...\)|\{[^{}]*\})`: bare `$` when not opening a complete subst/param.
				push(i + 1)
			}
		case '`':
			if end := strings.IndexByte(candidate[i+1:], '`'); end >= 0 {
				push(i + 1 + end + 1)
			} else {
				// `` `(?![^`]*`) ``: unmatched backtick consumes itself.
				push(i + 1)
			}
		case '"':
			// `"[^"]*"`: NO backslash escape handling inside double quotes.
			if end := strings.IndexByte(candidate[i+1:], '"'); end >= 0 {
				push(i + 1 + end + 1)
			}
		case '\'':
			// `'[^']*'`
			if end := strings.IndexByte(candidate[i+1:], '\''); end >= 0 {
				push(i + 1 + end + 1)
			}
		case ';', '|', '\n':
			// Unquoted/unescaped command separators stop the scan.
		case '&':
			// `(?<=[<>])&` or `(?<![<>])&(?=>)`
			prevIsAngle := i > 0 && (candidate[i-1] == '<' || candidate[i-1] == '>')
			if prevIsAngle || (i+1 < len(candidate) && candidate[i+1] == '>') {
				push(i + 1)
			}
		case '>':
			// Branch 1: non-stdout fd redirect `(?<=(?:^|\s)[02-9])>{1,2}\|?`
			if isNonStdoutFdPrefix(candidate, i) {
				push(i + 1)
				if i+1 < len(candidate) && candidate[i+1] == '|' {
					push(i + 2)
				}
				if i+1 < len(candidate) && candidate[i+1] == '>' {
					push(i + 2)
					if i+2 < len(candidate) && candidate[i+2] == '|' {
						push(i + 3)
					}
				}
			}
			// Branch 2: allowed /dev sink redirect:
			// `(?<!\||>|(?:^|\s)[02-9])>{1,2}\|?\s*(?:allowedDevSink)(?=[\s;&|]|$)`
			if !isDisallowedWriteRedirectPrefix(candidate, i) {
				var lens [4]int
				nLens := 0
				lens[nLens] = 1
				nLens++
				if i+1 < len(candidate) && candidate[i+1] == '|' {
					lens[nLens] = 2
					nLens++
				}
				if i+1 < len(candidate) && candidate[i+1] == '>' {
					lens[nLens] = 2
					nLens++
					if i+2 < len(candidate) && candidate[i+2] == '|' {
						lens[nLens] = 3
						nLens++
					}
				}
				for k := 0; k < nLens; k++ {
					ws := skipJSSpaces(candidate, i+lens[k])
					if afterSink, ok := matchAllowedDevSink(candidate, ws); ok {
						push(afterSink)
					}
				}
			}
			// Any other `>` cannot be consumed by the body loop (nor matched as
			// a final write redirect above), so this path terminates.
		default:
			// `[^"'>;&|\n\\$`]`
			_, sz := utf8.DecodeRuneInString(candidate[i:])
			push(i + sz)
		}
	}
	return false
}

// compileInterceptorRule compiles a user-configured InterceptorRule into Go's
// RE2 engine, translating JS `flags` (`i`, `m`, `s`; ignoring `g`/`u`/`y`/`v`/`d`
// or rejecting invalid/unsupported flags).
// upstream: tools/bash-interceptor.ts:23-34
func compileInterceptorRule(rule InterceptorRule) (*regexp.Regexp, error) {
	var re2Flags strings.Builder
	seen := map[rune]bool{}
	for _, f := range rule.Flags {
		if seen[f] {
			return nil, fmt.Errorf("duplicate regex flag %q", f)
		}
		seen[f] = true
		switch f {
		case 'i', 'm', 's':
			re2Flags.WriteRune(f)
		case 'g', 'u', 'v', 'd':
			// Stateless test with lastIndex=0 in omp; no RE2 flag equivalent needed.
		case 'y':
			return nil, fmt.Errorf("unsupported sticky regex flag 'y'")
		default:
			return nil, fmt.Errorf("invalid regex flag %q", f)
		}
	}
	pat := rule.Pattern
	if re2Flags.Len() > 0 {
		pat = "(?" + re2Flags.String() + ")" + pat
	}
	return regexp.Compile(pat)
}

func warnUncompilableInterceptorRuleOnce(ctx sdk.Context, rule InterceptorRule, err error) {
	var zero sdk.Context
	if ctx == zero {
		return
	}
	sessID := ""
	func() {
		defer func() { _ = recover() }()
		if id, sErr := ctx.GetSessionID(); sErr == nil {
			sessID = strings.TrimSpace(id)
		}
	}()
	key := rule.Tool + "\x00" + rule.Pattern + "\x00" + rule.Flags
	bucket := sessID
	if bucket == "" {
		bucket = "__anon__"
	}
	interceptorWarnMu.Lock()
	warned := interceptorWarnedBy[bucket]
	if warned == nil {
		warned = make(map[string]bool)
		interceptorWarnedBy[bucket] = warned
	}
	already := warned[key]
	if !already {
		warned[key] = true
	}
	interceptorWarnMu.Unlock()
	if already {
		return
	}
	msg := fmt.Sprintf("bashInterceptor rule for tool %q (pattern %q, flags %q) is not enforced: unsupported by Go RE2 (%v)", rule.Tool, rule.Pattern, rule.Flags, err)
	func() {
		defer func() { _ = recover() }()
		ctx.Notify(msg, "warning")
	}()
}

// clearInterceptorWarningsForSession drops once-per-session warning state for sessionID.
func clearInterceptorWarningsForSession(sessionID string) {
	if sessionID == "" {
		return
	}
	interceptorWarnMu.Lock()
	delete(interceptorWarnedBy, sessionID)
	interceptorWarnMu.Unlock()
}

// CheckBashInterception checks if a bash command should be redirected to a
// specialized tool.
// upstream: bash-interceptor.ts:58-87
func CheckBashInterception(command string, availableTools []string, rules []InterceptorRule, originalCommand string) InterceptionResult {
	return checkBashInterceptionWithNotify(command, availableTools, rules, originalCommand, nil)
}

func checkBashInterceptionWithNotify(
	command string,
	availableTools []string,
	rules []InterceptorRule,
	originalCommand string,
	onInvalid func(rule InterceptorRule, err error),
) InterceptionResult {
	if originalCommand == "" {
		originalCommand = command
	}
	candidates := interceptionCandidates(command)
	for _, rule := range rules {
		if !slices.Contains(availableTools, rule.Tool) {
			continue
		}
		matcher := rule.matcher
		if matcher == nil {
			matcher = builtinMatcherForPattern(rule.Pattern, rule.Flags)
		}
		if matcher != nil {
			for _, candidate := range candidates {
				if matcher(candidate) {
					return InterceptionResult{
						Block:         true,
						Message:       fmt.Sprintf("Blocked: %s\n\nOriginal command: %s", rule.Message, originalCommand),
						SuggestedTool: rule.Tool,
					}
				}
			}
			continue
		}
		re, err := compileInterceptorRule(rule)
		if err != nil {
			if onInvalid != nil {
				onInvalid(rule, err)
			}
			continue
		}
		for _, candidate := range candidates {
			if re.MatchString(candidate) {
				return InterceptionResult{
					Block:         true,
					Message:       fmt.Sprintf("Blocked: %s\n\nOriginal command: %s", rule.Message, originalCommand),
					SuggestedTool: rule.Tool,
				}
			}
		}
	}
	return InterceptionResult{Block: false}
}

func interceptionCandidates(command string) []string {
	candidates := []string{strings.TrimSpace(command)}
	for _, seg := range extractFlatShellCommandSegments(command) {
		if seg.pipedStdin {
			continue
		}
		candidates = append(candidates, seg.text)
		if stripped := withoutLeadingEnvironmentAssignments(seg.text); stripped != "" {
			candidates = append(candidates, stripped)
		}
	}
	return candidates
}

// skipShellWord finds the end of a shell word, respecting quotes and escapes;
// returns -1 for incomplete syntax.
// upstream: bash-interceptor.ts:37
func skipShellWord(command string, start int) int {
	inSingle := false
	inDouble := false
	for i := start; i < len(command); i++ {
		ch := command[i]
		if inSingle {
			if ch == '\'' {
				inSingle = false
			}
			continue
		}
		if inDouble {
			if ch == '\\' {
				if i+1 >= len(command) {
					return -1
				}
				i++
				continue
			}
			if ch == '"' {
				inDouble = false
			}
			continue
		}
		if ch == '\'' {
			inSingle = true
			continue
		}
		if ch == '"' {
			inDouble = true
			continue
		}
		if ch == '\\' {
			if i+1 >= len(command) {
				return -1
			}
			i++
			continue
		}
		if ch == ' ' || ch == '\t' {
			return i
		}
	}
	if inSingle || inDouble {
		return -1
	}
	return len(command)
}

// withoutLeadingEnvironmentAssignments removes leading `NAME=value` assignments
// without interpreting shell syntax.
// upstream: bash-interceptor.ts:74
func withoutLeadingEnvironmentAssignments(command string) string {
	index := 0
	found := false
	for index < len(command) {
		for index < len(command) && (command[index] == ' ' || command[index] == '\t') {
			index++
		}
		assignStart := index
		if index >= len(command) || !isIdentStart(command[index]) {
			break
		}
		nameEnd := index + 1
		for nameEnd < len(command) && isIdentPart(command[nameEnd]) {
			nameEnd++
		}
		if nameEnd >= len(command) || command[nameEnd] != '=' {
			if found {
				return strings.TrimLeft(command[assignStart:], " \t")
			}
			return ""
		}
		wordEnd := skipShellWord(command, nameEnd+1)
		if wordEnd < 0 {
			return ""
		}
		found = true
		index = wordEnd
		if index == len(command) {
			return ""
		}
	}
	if !found {
		return ""
	}
	rest := strings.TrimLeft(command[index:], " \t")
	return rest
}

func isIdentStart(ch byte) bool {
	return (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || ch == '_'
}

func isIdentPart(ch byte) bool {
	return isIdentStart(ch) || (ch >= '0' && ch <= '9')
}
