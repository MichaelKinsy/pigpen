package powerline_footer

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Bash mode history: the per-project command file and the user's own shell history. upstream: bash-mode/history.ts.

// bashDebug is console.debug: best-effort diagnostics. A Go extension must not write to stdout, so it is silent unless a test
// (or a host adapter) installs a sink.
var bashDebug = func(format string, args ...any) {}

type persistedHistoryEntry struct {
	Command   string
	Cwd       string
	Timestamp float64
}

// fileFingerprint: mode and ctime are part of it so permission transitions (chmod 000) invalidate the cache even when the
// content is unchanged. upstream: history.ts:13.
type fileFingerprint struct {
	ctimeNs int64
	mtimeNs int64
	mode    uint32
	size    int64
}

func fingerprintOf(fi os.FileInfo) fileFingerprint {
	return fileFingerprint{ctimeNs: fileCtimeNs(fi), mtimeNs: fi.ModTime().UnixNano(), mode: uint32(fi.Mode()), size: fi.Size()}
}

type cachedProjectHistory struct {
	fp      fileFingerprint
	entries []persistedHistoryEntry
}

type cachedGlobalHistory struct {
	fp      fileFingerprint
	entries []string
}

var (
	bashHistoryMu       sync.Mutex
	projectHistoryCache = map[string]cachedProjectHistory{}
	globalHistoryCache  = map[string]cachedGlobalHistory{}
)

func bashHistoryDir() string { return getAgentPath("powerline-footer", "bash-history") }

var (
	bashProjectKeyEdges = regexp.MustCompile(`^[/\\]+|[/\\]+$`)
	bashProjectKeySeps  = regexp.MustCompile(`[\\/]+`)
)

// bashProjectKey upstream: history.ts:38.
func bashProjectKey(cwd string) string {
	key := bashProjectKeySeps.ReplaceAllString(bashProjectKeyEdges.ReplaceAllString(cwd, ""), "-")
	if key == "" {
		return "root"
	}
	return key
}

func projectHistoryPath(cwd string) string {
	return filepath.Join(bashHistoryDir(), bashProjectKey(cwd)+".json")
}

// normalizePersistedEntries upstream: history.ts:46: entries that are not objects, or lack a command, a cwd or a non-zero finite
// timestamp, are dropped.
func normalizePersistedEntries(value any) []persistedHistoryEntry {
	list, ok := value.([]any)
	if !ok {
		return nil
	}
	var entries []persistedHistoryEntry
	for _, raw := range list {
		obj, ok := raw.(*jsObject)
		if !ok {
			continue
		}
		command, cwd := "", ""
		if s, ok := obj.vals["command"].(string); ok {
			command = jsTrim(s)
		}
		if s, ok := obj.vals["cwd"].(string); ok {
			cwd = jsTrim(s)
		}
		timestamp := 0.0
		if n, ok := obj.vals["timestamp"].(float64); ok && !isNonFinite(n) {
			timestamp = n
		}
		if command == "" || cwd == "" || timestamp == 0 {
			continue
		}
		entries = append(entries, persistedHistoryEntry{Command: command, Cwd: cwd, Timestamp: timestamp})
	}
	return entries
}

func isNonFinite(n float64) bool {
	return n != n || n > 1.7976931348623157e308 || n < -1.7976931348623157e308
}

// sortNewestFirst is `.sort((a, b) => b.timestamp - a.timestamp)`: stable.
func sortNewestFirst(entries []persistedHistoryEntry) {
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Timestamp > entries[j].Timestamp })
}

// readProjectHistory upstream: history.ts:63.
func readProjectHistory(cwd string) []persistedHistoryEntry {
	filePath := projectHistoryPath(cwd)
	bashHistoryMu.Lock()
	defer bashHistoryMu.Unlock()
	fi, err := os.Stat(filePath)
	if err != nil { // existsSync is false for any stat failure
		delete(projectHistoryCache, filePath)
		return nil
	}
	fp := fingerprintOf(fi)
	if cached, ok := projectHistoryCache[filePath]; ok && cached.fp == fp {
		return cached.entries
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		bashDebug("[powerline-footer] Failed to read bash project history from %s: %v", filePath, err)
		return nil
	}
	parsed, err := parseJSON(data)
	if err != nil {
		// Project history is a best-effort cache. If it is unreadable or malformed, bash mode keeps working.
		bashDebug("[powerline-footer] Failed to read bash project history from %s: %v", filePath, err)
		return nil
	}
	obj, ok := parsed.(*jsObject)
	if !ok {
		return nil
	}
	entries := normalizePersistedEntries(obj.vals["entries"])
	sortNewestFirst(entries)
	projectHistoryCache[filePath] = cachedProjectHistory{fp: fp, entries: entries}
	return entries
}

// appendProjectHistory upstream: history.ts:97. The newest command goes first, an older identical one is dropped, 500 are kept.
func appendProjectHistory(cwd, command, entryCwd string) {
	normalized := jsTrim(command)
	if normalized == "" {
		return
	}
	existing := readProjectHistory(cwd)
	next := []persistedHistoryEntry{{Command: normalized, Cwd: entryCwd, Timestamp: float64(clock())}}
	for _, e := range existing {
		if e.Command != normalized {
			next = append(next, e)
		}
	}
	if len(next) > 500 {
		next = next[:500]
	}
	sortNewestFirst(next)

	filePath := projectHistoryPath(cwd)
	bashHistoryMu.Lock()
	defer bashHistoryMu.Unlock()
	delete(projectHistoryCache, filePath)
	list := make([]any, len(next))
	for i, e := range next {
		o := newObject()
		o.set("command", e.Command)
		o.set("cwd", e.Cwd)
		o.set("timestamp", e.Timestamp)
		list[i] = o
	}
	doc := newObject()
	doc.set("version", float64(1))
	doc.set("entries", list)
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		// History persistence should never block a successful shell command from completing.
		bashDebug("[powerline-footer] Failed to persist bash project history to %s: %v", filePath, err)
		return
	}
	if err := os.WriteFile(filePath, []byte(marshalJSON(doc, "  ")+"\n"), 0o666); err != nil {
		bashDebug("[powerline-footer] Failed to persist bash project history to %s: %v", filePath, err)
		return
	}
	fi, err := os.Stat(filePath)
	if err != nil {
		bashDebug("[powerline-footer] Failed to persist bash project history to %s: %v", filePath, err)
		return
	}
	projectHistoryCache[filePath] = cachedProjectHistory{fp: fingerprintOf(fi), entries: next}
}

// parseZshHistoryLine upstream: history.ts:128: ": 1711111111:0;cmd" lines carry the command after the first ";".
func parseZshHistoryLine(line string) (string, bool) {
	trimmed := jsTrim(line)
	if trimmed == "" {
		return "", false
	}
	if !strings.HasPrefix(trimmed, ":") {
		return trimmed, true
	}
	parts := strings.Split(trimmed, ";")
	if len(parts) < 2 {
		return "", false
	}
	command := jsTrim(strings.Join(parts[1:], ";"))
	return command, command != ""
}

func parseBashHistory(lines []string) []string {
	var out []string
	for _, line := range lines {
		if t := jsTrim(line); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// jsWhitespaceClass is the character class of JavaScript's \s, and jsLineTerminators the characters `.`, `^` and `$` treat as
// line ends in a multiline regexp; RE2's \s and ^/$ know fewer of them.
const (
	jsWhitespaceClass  = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`
	jsLineTerminators  = `\n\r\x{2028}\x{2029}`
	fishCommandPattern = `(?:\A|[` + jsLineTerminators + `])[` + jsWhitespaceClass + `]*-[` + jsWhitespaceClass + `]*cmd:[` + jsWhitespaceClass + `]*([^` + jsLineTerminators + `]+)`
)

var fishCommandRe = regexp.MustCompile(fishCommandPattern)

// parseFishHistory is /^\s*-\s*cmd:\s*(.+)$/gm over the whole file. upstream: history.ts:141.
func parseFishHistory(raw string) []string {
	var commands []string
	for _, m := range fishCommandRe.FindAllStringSubmatch(raw, -1) {
		if command := jsTrim(m[1]); command != "" {
			commands = append(commands, command)
		}
	}
	return commands
}

func bashReverseStrings(s []string) []string {
	out := make([]string, len(s))
	for i, v := range s {
		out[len(s)-1-i] = v
	}
	return out
}

// readGlobalShellHistory upstream: history.ts:151: the user's own history file for the shell, newest first, cached by the
// file's fingerprint (an unreadable file is cached as empty until it changes).
func readGlobalShellHistory(shellPath string) []string {
	shellName := strings.ToLower(shellPath[strings.LastIndex(shellPath, "/")+1:])
	home := getHomeDir()
	var filePath string
	switch {
	case strings.Contains(shellName, "fish"):
		filePath = filepath.Join(home, ".local", "share", "fish", "fish_history")
	case os.Getenv("HISTFILE") != "":
		filePath = os.Getenv("HISTFILE")
	case strings.Contains(shellName, "zsh"):
		filePath = filepath.Join(home, ".zsh_history")
	default:
		filePath = filepath.Join(home, ".bash_history")
	}
	cacheKey := shellName + "\x00" + filePath

	bashHistoryMu.Lock()
	defer bashHistoryMu.Unlock()
	fi, err := os.Stat(filePath)
	if err != nil { // existsSync is false for any stat failure
		delete(globalHistoryCache, cacheKey)
		return nil
	}
	fp := fingerprintOf(fi)
	if cached, ok := globalHistoryCache[cacheKey]; ok && cached.fp == fp {
		return cached.entries
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		globalHistoryCache[cacheKey] = cachedGlobalHistory{fp: fp, entries: nil}
		bashDebug("[powerline-footer] Failed to read global shell history for %s: %v", shellName, err)
		return nil
	}
	raw := strings.ToValidUTF8(string(data), "\uFFFD")
	var entries []string
	switch {
	case strings.Contains(shellName, "zsh"):
		var parsed []string
		for _, line := range strings.Split(raw, "\n") {
			if c, ok := parseZshHistoryLine(line); ok {
				parsed = append(parsed, c)
			}
		}
		entries = bashReverseStrings(parsed)
	case strings.Contains(shellName, "fish"):
		entries = bashReverseStrings(parseFishHistory(raw))
	default:
		entries = bashReverseStrings(parseBashHistory(strings.Split(raw, "\n")))
	}
	globalHistoryCache[cacheKey] = cachedGlobalHistory{fp: fp, entries: entries}
	return entries
}

// matchHistoryEntries upstream: history.ts:189: distinct entries that start with the trimmed prefix, in order, up to limit.
func matchHistoryEntries(entries []string, prefix string, limit int) []string {
	trimmedPrefix := jsTrim(prefix)
	seen := map[string]bool{}
	matches := []string{}
	for _, raw := range entries {
		entry := jsTrim(raw)
		if entry == "" || seen[entry] {
			continue
		}
		if trimmedPrefix != "" && !strings.HasPrefix(entry, trimmedPrefix) {
			continue
		}
		seen[entry] = true
		matches = append(matches, entry)
		if len(matches) >= limit {
			break
		}
	}
	return matches
}
