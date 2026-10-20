package powerline_footer

import (
	"context"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

// The bash completion engine: ghost suggestions from project and global history, deterministic path and git completions.
// upstream: bash-mode/completion.ts (the editor-facing providers are in bash_autocomplete.go).
//
// Columns here are byte offsets: the engine builds an item for one line and applies it to the same line, so the unit cancels.
// Whitespace is JavaScript's \s (isJSSpace), not Go's unicode.IsSpace.

type tokenContext struct {
	line           string
	cursorCol      int
	beforeCursor   string
	afterCursor    string
	token          string
	tokenStart     int
	tokenEnd       int
	tokenIndex     int
	previousTokens []string
}

// oneOffBashCommandContext is OneOffBashCommandContext.
type oneOffBashCommandContext struct {
	Prefix  string
	Command string
	Offset  int
}

var gitSubcommands = []string{
	"add", "bisect", "branch", "checkout", "cherry-pick", "clean", "clone", "commit", "diff", "fetch",
	"grep", "init", "log", "merge", "mv", "pull", "push", "rebase", "reset", "restore", "revert", "rm",
	"show", "stash", "status", "switch", "tag", "worktree",
}

// tokenizeBeforeCursor splits text at unquoted, unescaped whitespace, keeping quotes and backslashes in the tokens.
// upstream: completion.ts:38.
func tokenizeBeforeCursor(text string) []string {
	var tokens []string
	var current strings.Builder
	var quote rune
	escaped := false
	for _, char := range text {
		if escaped {
			current.WriteRune(char)
			escaped = false
			continue
		}
		if char == '\\' {
			escaped = true
			current.WriteRune(char)
			continue
		}
		if quote != 0 {
			current.WriteRune(char)
			if char == quote {
				quote = 0
			}
			continue
		}
		if char == '\'' || char == '"' {
			quote = char
			current.WriteRune(char)
			continue
		}
		if isJSSpace(char) {
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
			continue
		}
		current.WriteRune(char)
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}

// getTokenContext upstream: completion.ts:76.
func getTokenContext(line string, cursorCol int) tokenContext {
	beforeCursor, afterCursor := line[:cursorCol], line[cursorCol:]
	tokens := tokenizeBeforeCursor(beforeCursor)

	tokenStart := 0
	for i := len(beforeCursor); i > 0; {
		r, size := utf8.DecodeLastRuneInString(beforeCursor[:i])
		if isJSSpace(r) {
			tokenStart = i
			break
		}
		i -= size
	}
	tokenEnd := cursorCol
	for i := cursorCol; i < len(line); {
		r, size := utf8.DecodeRuneInString(line[i:])
		if isJSSpace(r) {
			break
		}
		i += size
		tokenEnd = i
	}
	return tokenContext{
		line: line, cursorCol: cursorCol, beforeCursor: beforeCursor, afterCursor: afterCursor,
		token: line[tokenStart:tokenEnd], tokenStart: tokenStart, tokenEnd: tokenEnd,
		tokenIndex: max(0, len(tokens)-1), previousTokens: tokens,
	}
}

// getOneOffBashCommandContext upstream: completion.ts:112: "!!cmd" and "!cmd" at the very start of the line.
func getOneOffBashCommandContext(line string) *oneOffBashCommandContext {
	if strings.HasPrefix(line, "!!") {
		return &oneOffBashCommandContext{Prefix: "!!", Command: line[2:], Offset: 2}
	}
	if strings.HasPrefix(line, "!") {
		return &oneOffBashCommandContext{Prefix: "!", Command: line[1:], Offset: 1}
	}
	return nil
}

// compareLocale ports `a.localeCompare(b)`, which orders equally scored items. The original runs under ICU's root collation; Go's
// standard library has no collator, so this reproduces it for the characters file names and git refs use: ASCII (whitespace,
// punctuation and symbols in CLDR order, digits, then letters) and the Latin-1 letters with their accents. Three levels as in ICU:
// base characters first, then accents, then case (lowercase before uppercase). Any other character sorts after the letters by
// code point.
func compareLocale(a, b string) int {
	ka, kb := collationKeys(a), collationKeys(b)
	for level := 0; level < 3; level++ {
		for i := 0; i < len(ka) && i < len(kb); i++ {
			if ka[i][level] != kb[i][level] {
				if ka[i][level] < kb[i][level] {
					return -1
				}
				return 1
			}
		}
		if level == 0 && len(ka) != len(kb) {
			if len(ka) < len(kb) {
				return -1
			}
			return 1
		}
	}
	return 0
}

// cldrAsciiSymbolOrder is the root collation order of the ASCII characters that are not letters or digits.
const cldrAsciiSymbolOrder = " _-,;:!?.'\"()[]{}@*/\\&#%`^+<=>|~$"

// latin1Base maps the Latin-1 letters to their base letter: "\u00c0" is "A" with a grave accent, and so on.
var latin1Base = map[rune]rune{}

func init() {
	for base, accented := range map[rune]string{
		'a': "\u00e0\u00e1\u00e2\u00e3\u00e4\u00e5", 'c': "\u00e7", 'e': "\u00e8\u00e9\u00ea\u00eb", 'i': "\u00ec\u00ed\u00ee\u00ef",
		'n': "\u00f1", 'o': "\u00f2\u00f3\u00f4\u00f5\u00f6", 'u': "\u00f9\u00fa\u00fb\u00fc", 'y': "\u00fd\u00ff",
	} {
		for _, r := range accented {
			latin1Base[r] = base
			if r != 0xff { // \u00ff has no Latin-1 capital
				latin1Base[r-0x20] = base
			}
		}
	}
}

// collationKeys is the (primary, secondary, tertiary) weight of each rune.
func collationKeys(s string) [][3]int {
	keys := make([][3]int, 0, len(s))
	for _, r := range s {
		keys = append(keys, collationKey(r))
	}
	return keys
}

func collationKey(r rune) [3]int {
	upper := 0
	if base, ok := latin1Base[r]; ok {
		if r < 0xe0 {
			upper = 1
		}
		return [3]int{200 + int(base-'a'), int(r&^0x20) + 1, upper}
	}
	switch {
	case r >= 'A' && r <= 'Z':
		return [3]int{200 + int(r-'A'), 0, 1}
	case r >= 'a' && r <= 'z':
		return [3]int{200 + int(r-'a'), 0, 0}
	case r >= '0' && r <= '9':
		return [3]int{100 + int(r-'0'), 0, 0}
	case r < 0x80:
		if i := strings.IndexRune(cldrAsciiSymbolOrder, r); i >= 0 {
			return [3]int{1 + i, 0, 0}
		}
		return [3]int{0, 0, 0} // control characters sort first
	}
	return [3]int{1000 + int(r), 0, 0}
}

// uniqueByReplacement keeps the best-scored item per (range, replacement), highest score first, then by label.
// upstream: completion.ts:162.
func uniqueByReplacement(items []extendedCompletionItem) []extendedCompletionItem {
	index := map[string]int{}
	var best []extendedCompletionItem
	for _, item := range items {
		key := bashItoa(item.StartCol) + ":" + bashItoa(item.EndCol) + ":" + item.Replacement
		if i, ok := index[key]; !ok {
			index[key] = len(best)
			best = append(best, item)
		} else if item.Score > best[i].Score {
			best[i] = item
		}
	}
	sort.SliceStable(best, func(i, j int) bool {
		if best[i].Score != best[j].Score {
			return best[i].Score > best[j].Score
		}
		return compareLocale(best[i].Label, best[j].Label) < 0
	})
	return best
}

func bashItoa(n int) string {
	return jsNumber(float64(n))
}

type bashPathBaseResult struct{ dir, prefix, displayPrefix string }

// jsPathJoin is node:path join for two segments: joined, normalized, a trailing slash kept (filepath.Join would drop it, and
// bashPathBase tests for it).
func jsPathJoin(a, b string) string {
	joined := a + "/" + b
	if a == "" {
		joined = b
	}
	if b == "" {
		joined = a
	}
	if joined == "" {
		return "."
	}
	res := path.Clean(filepath.ToSlash(joined))
	if strings.HasSuffix(joined, "/") && res != "/" {
		res += "/"
	}
	return res
}

// bashPathBase upstream: completion.ts:171.
func bashPathBase(token, cwd string) bashPathBaseResult {
	expanded := token
	if strings.HasPrefix(token, "~/") {
		expanded = jsPathJoin(os.Getenv("HOME"), token[2:])
	}
	if !strings.Contains(expanded, "/") {
		return bashPathBaseResult{dir: cwd, prefix: expanded}
	}
	var baseDir string
	if strings.HasSuffix(expanded, "/") {
		baseDir = expanded[:len(expanded)-1]
	} else {
		baseDir = path.Dir(expanded)
	}
	resolvedDir := baseDir
	if !filepath.IsAbs(baseDir) {
		resolvedDir = filepath.Join(cwd, baseDir)
	}
	prefix := ""
	if !strings.HasSuffix(expanded, "/") {
		prefix = path.Base(expanded)
	}
	displayPrefix := token
	if !strings.HasSuffix(token, "/") {
		displayPrefix = token[:max(0, len(token)-len(prefix))]
	}
	return bashPathBaseResult{dir: resolvedDir, prefix: prefix, displayPrefix: displayPrefix}
}

var shellPathEscape = regexp.MustCompile("([\\\\" + jsWhitespaceClass + "\"'`$&|;<>()\\[\\]{}?!*])")

func escapeShellPath(value string) string {
	return shellPathEscape.ReplaceAllString(value, `\$1`)
}

// getPathSuggestions lists the directory the token points into, at most 100 entries (directories get a trailing "/").
// upstream: completion.ts:195. A missing or unreadable directory only removes this source.
func getPathSuggestions(token, cwd string) []extendedCompletionItem {
	if token == "" {
		return nil
	}
	base := bashPathBase(token, cwd)
	entries, err := os.ReadDir(base.dir)
	if err != nil {
		return nil
	}
	var items []extendedCompletionItem
	for _, entry := range entries {
		if base.prefix != "" && !strings.HasPrefix(entry.Name(), base.prefix) {
			continue
		}
		if len(items) == 100 {
			break
		}
		suffix, score := "", 40
		if entry.IsDir() {
			suffix, score = "/", 44
		}
		replacement := base.displayPrefix + escapeShellPath(entry.Name()) + suffix
		items = append(items, extendedCompletionItem{
			Value: replacement, Label: base.displayPrefix + entry.Name() + suffix, Replacement: replacement,
			Source: bashSourcePath, Score: score,
		})
	}
	return items
}

// bashRunGit runs `git args` in cwd and returns its trimmed, non-empty output lines; any failure (or cancellation) is an empty list.
// upstream: completion.ts:225.
func bashRunGit(ctx context.Context, args []string, cwd string) []string {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = cwd
	hideConsoleWindow(cmd)
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return nil
	}
	var lines []string
	for _, line := range strings.Split(string(out), "\n") {
		if t := jsTrim(line); t != "" {
			lines = append(lines, t)
		}
	}
	return lines
}

var gitRefSubcommands = []string{"checkout", "switch", "merge", "rebase", "branch", "show", "diff"}

// getGitSuggestions upstream: completion.ts:245: subcommands at the second word, branches and tags after a ref-taking one.
func getGitSuggestions(ctx context.Context, tc tokenContext, cwd string) []extendedCompletionItem {
	tokens := tc.previousTokens
	if len(tokens) == 0 || tokens[0] != "git" {
		return nil
	}
	if tc.tokenIndex <= 1 {
		var items []extendedCompletionItem
		for _, command := range gitSubcommands {
			if strings.HasPrefix(command, tc.token) {
				items = append(items, extendedCompletionItem{Value: command, Label: command, Replacement: command, Source: bashSourceGit, Score: 52})
			}
		}
		return items
	}
	subcommand := ""
	if len(tokens) > 1 {
		subcommand = tokens[1]
	}
	isRefCommand := false
	for _, c := range gitRefSubcommands {
		if c == subcommand {
			isRefCommand = true
		}
	}
	if !isRefCommand {
		return nil
	}
	var branches, tags []string
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		branches = bashRunGit(ctx, []string{"branch", "--format=%(refname:short)"}, cwd)
	}()
	go func() { defer wg.Done(); tags = bashRunGit(ctx, []string{"tag", "--list"}, cwd) }()
	wg.Wait()
	if ctx.Err() != nil {
		return nil
	}
	seen := map[string]bool{}
	var items []extendedCompletionItem
	for _, ref := range append(branches, tags...) {
		if seen[ref] {
			continue
		}
		seen[ref] = true
		if !strings.HasPrefix(ref, tc.token) {
			continue
		}
		if len(items) == 100 {
			break
		}
		items = append(items, extendedCompletionItem{Value: ref, Label: ref, Replacement: ref, Source: bashSourceGit, Score: 50})
	}
	return items
}

func canUseHistorySuggestion(tc tokenContext) bool {
	return tc.cursorCol == len(tc.line) && len(jsTrim(tc.line)) > 0
}

func withRange(items []extendedCompletionItem, startCol, endCol int) []extendedCompletionItem {
	out := make([]extendedCompletionItem, len(items))
	for i, item := range items {
		item.StartCol, item.EndCol = startCol, endCol
		out[i] = item
	}
	return out
}

func applyCompletionToLine(line string, item extendedCompletionItem) string {
	return line[:item.StartCol] + item.Replacement + line[item.EndCol:]
}

func commandHead(value string) string {
	tokens := tokenizeBeforeCursor(jsTrim(value))
	if len(tokens) == 0 {
		return ""
	}
	return tokens[0]
}

// findNewestHistoryMatchForHead upstream: completion.ts:289.
func findNewestHistoryMatchForHead(entries []string, prefix, head string) (string, bool) {
	for _, raw := range entries {
		entry := jsTrim(raw)
		if entry == "" || !strings.HasPrefix(entry, prefix) {
			continue
		}
		if commandHead(entry) != head {
			continue
		}
		return entry, true
	}
	return "", false
}

// getCuratedCommandFallback upstream: completion.ts:301.
func getCuratedCommandFallback(prefix string) *ghostSuggestion {
	trimmed := jsTrim(prefix)
	if trimmed == "" {
		return nil
	}
	if strings.HasPrefix("cd", trimmed) {
		return &ghostSuggestion{Value: "cd ..", Source: bashSourcePath}
	}
	if strings.HasPrefix("git", trimmed) {
		return &ghostSuggestion{Value: "git status", Source: bashSourceGit}
	}
	return nil
}

// isLikelyGitCommandHead: at least two letters of "git" (a lone "g" is too ambiguous to read global history for).
func isLikelyGitCommandHead(prefix string) bool {
	trimmed := jsTrim(prefix)
	return utf16Len(trimmed) >= 2 && strings.HasPrefix("git", trimmed)
}

// boostValidatedItemsFromGlobalHistory raises the score of items whose completed line is in the global history (the newest
// match gets +3 down to +1), so a deterministic candidate the user has run before wins a tie. upstream: completion.ts:322.
func boostValidatedItemsFromGlobalHistory(line string, items []extendedCompletionItem, globalHistoryMatches []string) []extendedCompletionItem {
	if len(items) == 0 || len(globalHistoryMatches) == 0 {
		return items
	}
	boosts := map[string]int{}
	for index, value := range globalHistoryMatches {
		boosts[value] = max(1, 4-index)
	}
	out := make([]extendedCompletionItem, len(items))
	for i, item := range items {
		if boost := boosts[applyCompletionToLine(line, item)]; boost != 0 {
			item.Score += boost
		}
		out[i] = item
	}
	return out
}

// bashCompletionEngine is BashCompletionEngine.
type bashCompletionEngine struct{}

// getGhostSuggestion upstream: completion.ts:350. ctx plays the AbortSignal: a cancelled context ends the answer with nil.
func (e *bashCompletionEngine) getGhostSuggestion(ctx context.Context, line, cwd, shellPath string) *ghostSuggestion {
	projectHistoryEntries := readProjectHistory(cwd)
	commands := make([]string, len(projectHistoryEntries))
	for i, entry := range projectHistoryEntries {
		commands[i] = entry.Command
	}

	if len(jsTrim(line)) == 0 {
		if projectHistory := matchHistoryEntries(commands, line, 1); len(projectHistory) > 0 {
			return &ghostSuggestion{Value: line + projectHistory[0], Source: bashSourceProjectHistory}
		}
		return nil
	}

	tc := getTokenContext(line, len(line))
	if !canUseHistorySuggestion(tc) {
		return nil
	}
	if projectHistory := matchHistoryEntries(commands, line, 10); len(projectHistory) > 0 {
		return &ghostSuggestion{Value: projectHistory[0], Source: bashSourceProjectHistory}
	}
	if tc.tokenIndex == 0 {
		return e.getCommandPositionGhostSuggestion(line, readGlobalShellHistory(shellPath))
	}

	deterministic := e.getDeterministicInlineSuggestions(ctx, tc, cwd)
	if ctx.Err() != nil {
		return nil
	}
	globalHistory := matchHistoryEntries(readGlobalShellHistory(shellPath), line, 5)
	ranked := boostValidatedItemsFromGlobalHistory(line, uniqueByReplacement(deterministic), globalHistory)
	for _, item := range ranked {
		if value, ok := e.buildInlineSuggestionValue(line, item); ok {
			return &ghostSuggestion{Value: value, Source: item.Source}
		}
	}
	return nil
}

func (e *bashCompletionEngine) getCommandPositionGhostSuggestion(line string, globalHistoryEntries []string) *ghostSuggestion {
	if isLikelyGitCommandHead(line) {
		if match, ok := findNewestHistoryMatchForHead(globalHistoryEntries, line, "git"); ok {
			return &ghostSuggestion{Value: match, Source: bashSourceGlobalHistory}
		}
	}
	return getCuratedCommandFallback(line)
}

// getDeterministicInlineSuggestions: git refs and path entries, git started first so its processes overlap the directory read.
func (e *bashCompletionEngine) getDeterministicInlineSuggestions(ctx context.Context, tc tokenContext, cwd string) []extendedCompletionItem {
	gitDone := make(chan []extendedCompletionItem, 1)
	go func() { gitDone <- getGitSuggestions(ctx, tc, cwd) }()
	pathSuggestions := getPathSuggestions(tc.token, cwd)
	items := append(withRange(<-gitDone, tc.tokenStart, tc.tokenEnd), withRange(pathSuggestions, tc.tokenStart, tc.tokenEnd)...)
	return uniqueByReplacement(items)
}

// buildInlineSuggestionValue: an item only becomes a ghost when it extends the typed line.
func (e *bashCompletionEngine) buildInlineSuggestionValue(line string, item extendedCompletionItem) (string, bool) {
	value := applyCompletionToLine(line, item)
	if !strings.HasPrefix(value, line) || value == line {
		return "", false
	}
	return value, true
}
