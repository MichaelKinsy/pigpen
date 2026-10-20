package powerline_footer

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

// The /cd command. upstream: cd-command.ts. Pure logic only: the SDK wiring is in cdquote_ext.go.

// Path helpers. upstream: cd-command.ts stripMatchingQuotes, unescapePathArg, expandHomePath, escapeCompletionPath, displayPath.

func stripMatchingQuotes(value string) string {
	trimmed := jsTrim(value)
	runes := []rune(trimmed)
	if len(runes) >= 2 {
		first, last := runes[0], runes[len(runes)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return string(runes[1 : len(runes)-1])
		}
	}
	return trimmed
}

// unescapePathArg is stripMatchingQuotes(value).replace(/\\(.)/g, "$1"). `.` matches any character except a line terminator.
func unescapePathArg(value string) string {
	runes := []rune(stripMatchingQuotes(value))
	var b strings.Builder
	for i := 0; i < len(runes); i++ {
		if runes[i] == '\\' && i+1 < len(runes) {
			switch runes[i+1] {
			case '\n', '\r', '\u2028', '\u2029':
			default:
				i++
				b.WriteRune(runes[i])
				continue
			}
		}
		b.WriteRune(runes[i])
	}
	return b.String()
}

func expandHomePath(value, homeDir string) string {
	if value == "~" {
		return homeDir
	}
	if rest, ok := strings.CutPrefix(value, "~/"); ok {
		return filepath.Join(homeDir, rest)
	}
	return value
}

// escapeCompletionPath is value.replace(/([\\\s"'`$&|;<>()[\]{}?!*])/g, "\\$1").
func escapeCompletionPath(value string) string {
	var b strings.Builder
	for _, r := range value {
		if isJSSpace(r) || strings.ContainsRune("\\\"'`$&|;<>()[]{}?!*", r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func displayPath(value string) string { return strings.ReplaceAll(value, "\\", "/") }

// nodeResolve is path.resolve(base, p) for an absolute or relative p.
func nodeResolve(base, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	if !filepath.IsAbs(base) {
		if abs, err := filepath.Abs(base); err == nil {
			base = abs
		}
	}
	return filepath.Clean(filepath.Join(base, p))
}

// cdTarget is resolveCdTarget's answer: nothing (no argument), an error message, or a directory.
type cdTarget struct {
	Path  string
	Error string
}

// resolveCdTarget returns nil for an empty argument. upstream: cd-command.ts resolveCdTarget.
func resolveCdTarget(args, cwd, homeDir string) *cdTarget {
	input := unescapePathArg(args)
	if input == "" {
		return nil
	}
	expanded := expandHomePath(input, homeDir)
	target := nodeResolve(cwd, expanded)

	info, err := os.Stat(target)
	if err != nil {
		// existsSync is false for any failure to stat, including a path that cannot be read.
		return &cdTarget{Error: "Directory does not exist: " + target}
	}
	if !info.IsDir() {
		return &cdTarget{Error: "Not a directory: " + target}
	}
	return &cdTarget{Path: target}
}

// utf16Prefix cuts in UTF-16 code units (utf16Len counts them), as the original's String.length and slice do.

func utf16Prefix(s string, n int) string {
	units := utf16.Encode([]rune(s))
	if n > len(units) {
		n = len(units)
	}
	if n < 0 {
		n = 0
	}
	return string(utf16.Decode(units[:n]))
}

type cdPathBase struct{ dir, prefix, displayPrefix string }

func pathBase(token, cwd, homeDir string) cdPathBase {
	unescaped := unescapePathArg(token)
	expanded := expandHomePath(unescaped, homeDir)
	if !strings.Contains(expanded, "/") {
		return cdPathBase{dir: cwd, prefix: expanded}
	}
	var baseDir, prefix string
	if strings.HasSuffix(expanded, "/") {
		baseDir = expanded[:len(expanded)-1]
	} else {
		idx := strings.LastIndex(expanded, "/")
		baseDir, prefix = expanded[:idx], expanded[idx+1:]
		if idx == 0 {
			baseDir = "/"
		}
	}
	dir := baseDir
	if !filepath.IsAbs(baseDir) {
		dir = nodeResolve(cwd, baseDir)
	}
	displayPrefix := unescaped
	if !strings.HasSuffix(unescaped, "/") {
		displayPrefix = utf16Prefix(unescaped, utf16Len(unescaped)-utf16Len(prefix))
	}
	return cdPathBase{dir: dir, prefix: prefix, displayPrefix: displayPrefix}
}

// cdCompletion is one AutocompleteItem.
type cdCompletion struct{ Value, Label, Description string }

func isDirectoryEntry(dir, name string) bool {
	info, err := os.Stat(filepath.Join(dir, name))
	return err == nil && info.IsDir()
}

// getCdArgumentCompletions: the quick picks, then the directories of the typed path (at most 50). upstream: cd-command.ts
// getCdArgumentCompletions.
func getCdArgumentCompletions(argumentPrefix, cwd, homeDir string) []cdCompletion {
	prefix := strings.TrimLeftFunc(argumentPrefix, isJSSpace)
	var suggestions []cdCompletion

	for _, quickPick := range []string{"../", "~/"} {
		if prefix == "" || strings.HasPrefix(quickPick, prefix) {
			description := "Home directory"
			if quickPick == "../" {
				description = "Parent directory"
			}
			suggestions = append(suggestions, cdCompletion{Value: quickPick, Label: quickPick, Description: description})
		}
	}

	base := pathBase(prefix, cwd, homeDir)
	if dirEntries, err := os.ReadDir(base.dir); err == nil {
		count := 0
		for _, entry := range dirEntries {
			if count == 50 {
				break
			}
			if !strings.HasPrefix(entry.Name(), base.prefix) {
				continue
			}
			isDir := entry.IsDir() || (entry.Type()&os.ModeSymlink != 0 && isDirectoryEntry(base.dir, entry.Name()))
			if !isDir {
				continue
			}
			count++
			path := displayPath(base.displayPrefix + entry.Name() + "/")
			suggestions = append(suggestions, cdCompletion{Value: escapeCompletionPath(path), Label: path})
		}
	} else {
		return suggestions
	}

	seen := map[string]bool{}
	var unique []cdCompletion
	for _, item := range suggestions {
		if seen[item.Value] {
			continue
		}
		seen[item.Value] = true
		unique = append(unique, item)
	}
	return unique
}

// The forked session file. upstream: SessionManager.forkFrom in @earendil-works/pi-coding-agent core/session-manager.js, which
// the original calls as SessionManager.forkFrom(sessionFile, resolved.path); the Go SDK has no counterpart, so the file is
// written here exactly as Pi writes it.

const currentSessionVersion = 3

// nodeBasename is path.basename(p) for a posix path.
func nodeBasename(p string) string {
	p = strings.TrimRight(p, "/")
	if idx := strings.LastIndex(p, "/"); idx >= 0 {
		return p[idx+1:]
	}
	return p
}

// sessionDirName is the directory name Pi gives a cwd: `--${cwd.replace(/^[/\\]/, "").replace(/[/\\:]/g, "-")}--`.
func sessionDirName(cwd string) string {
	if len(cwd) > 0 && (cwd[0] == '/' || cwd[0] == '\\') {
		cwd = cwd[1:]
	}
	return "--" + strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(cwd) + "--"
}

// uuidv7 state: pi-ai's uuidv7() keeps one 42-bit sequence that grows with every id.
var uuidMu sync.Mutex

var uuidState struct {
	lastTimestamp int64
	sequence      uint64
	seeded        bool
}

const maxUUIDSequence = 1<<42 - 1

// newUUIDv7 generates a time-ordered UUIDv7 as pi-ai's uuidv7() does (a monotonic 42-bit sequence in the rand_a/rand_b fields).
func newUUIDv7(nowMs int64) (string, error) {
	uuidMu.Lock()
	defer uuidMu.Unlock()
	if nowMs < uuidState.lastTimestamp {
		nowMs = uuidState.lastTimestamp
	}
	uuidState.lastTimestamp = nowMs
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	if !uuidState.seeded {
		uuidState.sequence = uint64(b[1])<<32 | uint64(b[2])<<24 | uint64(b[3])<<16 | uint64(b[4])<<8 | uint64(b[5])
		uuidState.seeded = true
	} else {
		if uuidState.sequence == maxUUIDSequence {
			return "", errors.New("UUIDv7 generator sequence exhausted")
		}
		uuidState.sequence++
	}
	seq := uuidState.sequence
	for i := 5; i >= 0; i-- {
		b[i] = byte(uint64(nowMs) >> ((5 - i) * 8))
	}
	b[6] = 0x70 | byte((seq>>37)&0x0f)
	b[7] = byte((seq >> 29) & 0xff)
	b[8] = 0x80 | byte((seq>>23)&0x3f)
	b[9] = byte((seq >> 15) & 0xff)
	b[10] = byte((seq >> 7) & 0xff)
	b[11] = byte((seq&0x7f)<<1) | (b[11] & 0x01)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// jsISOString is Date.prototype.toISOString for a UTC time.
func jsISOString(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// sessionEntryLine parses one physical line like parseSessionEntryLine: a blank or malformed line, and a falsy value, are no entry.
func sessionEntryLine(line string) (any, bool) {
	if jsTrim(line) == "" {
		return nil, false
	}
	v, err := parseJSON([]byte(line))
	if err != nil {
		return nil, false
	}
	switch x := v.(type) {
	case nil:
		return nil, false
	case bool:
		return v, x
	case float64:
		return v, x != 0 && x == x
	case string:
		return v, x != ""
	}
	return v, true
}

// loadSessionEntries is loadEntriesFromFile: the parsed entries of a session file, none when it is missing or does not start
// with a session header. A last line without a newline gets one appended to the file, as Pi repairs it.
func loadSessionEntries(path string) []any {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	text := string(data)
	var entries []any
	pending := text
	for {
		idx := strings.IndexByte(pending, '\n')
		if idx < 0 {
			break
		}
		if entry, ok := sessionEntryLine(pending[:idx]); ok {
			entries = append(entries, entry)
		}
		pending = pending[idx+1:]
	}
	if entry, ok := sessionEntryLine(pending); ok {
		entries = append(entries, entry)
	}
	if len(entries) == 0 {
		return nil
	}
	header, _ := entries[0].(*jsObject)
	if header == nil {
		return nil
	}
	if kind, _ := header.get("type"); kind != "session" {
		return nil
	}
	if id, _ := header.get("id"); !isString(id) {
		return nil
	}
	if pending != "" {
		if f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0); err == nil {
			_, _ = f.WriteString("\n")
			_ = f.Close()
		}
	}
	return entries
}

func isString(v any) bool { _, ok := v.(string); return ok }

// forkSessionFrom is SessionManager.forkFrom(sourcePath, targetCwd): a new session file in the default session directory of
// targetCwd (sessionsRoot/--<cwd>--/<timestamp>_<id>.jsonl) with a new header that names the source as parentSession, then every
// non-header entry of the source as JSON.stringify writes it. It returns the new file's path.
func forkSessionFrom(sourcePath, targetCwd, sessionsRoot string, now time.Time) (string, error) {
	resolvedSource := nodeResolve(mustGetwd(), expandHomePath(sourcePath, getHomeDir()))
	resolvedTarget := nodeResolve(mustGetwd(), expandHomePath(targetCwd, getHomeDir()))
	sourceEntries := loadSessionEntries(resolvedSource)
	if len(sourceEntries) == 0 {
		return "", fmt.Errorf("Cannot fork: source session file is empty or invalid: %s", resolvedSource)
	}
	hasHeader := false
	for _, entry := range sourceEntries {
		if o, ok := entry.(*jsObject); ok {
			if kind, _ := o.get("type"); kind == "session" {
				hasHeader = true
				break
			}
		}
	}
	if !hasHeader {
		return "", fmt.Errorf("Cannot fork: source session has no header: %s", resolvedSource)
	}

	dir := filepath.Join(sessionsRoot, sessionDirName(resolvedTarget))
	if err := os.MkdirAll(dir, 0o777); err != nil {
		return "", err
	}
	id, err := newUUIDv7(now.UnixMilli())
	if err != nil {
		return "", err
	}
	timestamp := jsISOString(now)
	fileTimestamp := strings.NewReplacer(":", "-", ".", "-").Replace(timestamp)
	newSessionFile := filepath.Join(dir, fileTimestamp+"_"+id+".jsonl")

	header := newObject()
	header.set("type", "session")
	header.set("version", float64(currentSessionVersion))
	header.set("id", id)
	header.set("timestamp", timestamp)
	header.set("cwd", resolvedTarget)
	header.set("parentSession", resolvedSource)

	var out strings.Builder
	out.WriteString(marshalJSON(header, "") + "\n")
	for _, entry := range sourceEntries {
		if o, ok := entry.(*jsObject); ok {
			if kind, _ := o.get("type"); kind == "session" {
				continue
			}
		}
		out.WriteString(marshalJSON(entry, "") + "\n")
	}
	f, err := os.OpenFile(newSessionFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(out.String()); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return newSessionFile, nil
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "/"
	}
	return wd
}

// The command. upstream: cd-command.ts runCdCommand.

// cdNextContext is what withSession receives: the replacement session's context.
type cdNextContext interface {
	Cwd() string
	Notify(message, level string)
	SetTitle(title string)
}

// cdHost is the command context runCdCommand needs.
type cdHost interface {
	Cwd() string
	Notify(message, level string)
	// SessionFile is ctx.sessionManager.getSessionFile(): "" for an in-memory session.
	SessionFile() (string, error)
	// SessionsRoot is the directory Pi's getDefaultSessionDir(cwd) puts its per-cwd directories under.
	SessionsRoot() string
	WaitForIdle() error
	// SwitchSession is ctx.switchSession(path, { withSession }); it returns whether the switch was cancelled.
	SwitchSession(path string, withSession func(next cdNextContext) error) (cancelled bool, err error)
	Home() string
	Now() time.Time
}

// runCdCommand ports runCdCommand. An error is what the original lets reject out of the handler (waitForIdle, forkFrom).
func runCdCommand(args string, ctx cdHost) error {
	resolved := resolveCdTarget(args, ctx.Cwd(), ctx.Home())
	if resolved == nil {
		ctx.Notify(ctx.Cwd(), "info")
		return nil
	}
	if resolved.Error != "" {
		ctx.Notify(resolved.Error, "error")
		return nil
	}
	if resolved.Path == ctx.Cwd() {
		ctx.Notify("Already in "+ctx.Cwd(), "info")
		return nil
	}

	sessionFile, err := ctx.SessionFile()
	if err != nil {
		return err
	}
	if sessionFile == "" {
		ctx.Notify("/cd requires a persisted Pi session", "error")
		return nil
	}

	if err := ctx.WaitForIdle(); err != nil {
		return err
	}
	nextSessionFile, err := forkSessionFrom(sessionFile, resolved.Path, ctx.SessionsRoot(), ctx.Now())
	if err != nil {
		return err
	}

	cancelled, err := ctx.SwitchSession(nextSessionFile, func(next cdNextContext) error {
		// The original calls process.chdir(nextCtx.cwd) first. A Go extension has no host process cwd to change: the host's own
		// cwd is the session's, which switchSession already replaced (see cdquote_ext.go).
		next.Notify("Changed directory to "+next.Cwd(), "info")
		title := nodeBasename(next.Cwd())
		if title == "" {
			title = next.Cwd()
		}
		next.SetTitle("pi - " + title)
		return nil
	})
	if err != nil {
		ctx.Notify("Failed to change directory: "+err.Error(), "error")
		return nil
	}
	if cancelled {
		_ = os.Remove(nextSessionFile)
		ctx.Notify("Directory change cancelled", "warning")
	}
	return nil
}
