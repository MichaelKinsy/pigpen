package warden

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The scan of earlier sessions (prefs.ts scanPreferences and what it needs). Pi keeps each working directory's
// sessions as JSONL files in one directory; the scan reads the newest sessions of the project's directory and of
// the directories whose sessions ran in another worktree of the same repository. Session files are never written.

// userMarker: buffer search instead of parsing every line; user messages are a small share of a session file's bytes.
var userMarker = []byte(`{"role":"user"`)

// jsDateParse reads the timestamps Pi writes (ISO 8601, with or without milliseconds, a date alone, a zone offset
// or none = local time). Other spellings JavaScript's Date.parse accepts are not understood and read as invalid.
func jsDateParse(s string) (int64, bool) {
	for _, layout := range []string{
		"2006-01-02T15:04:05.999999999Z07:00", "2006-01-02T15:04Z07:00", "2006-01-02",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UnixMilli(), true
		}
	}
	for _, layout := range []string{"2006-01-02T15:04:05.999999999", "2006-01-02T15:04"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t.UnixMilli(), true
		}
	}
	return 0, false
}

type sessionEntry struct {
	Type      string `json:"type"`
	Timestamp any    `json:"timestamp"`
	Message   *struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// userMessageText is messageText: a string, or the text parts of a content array joined with newlines.
func userMessageText(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text any    `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return ""
	}
	var texts []string
	for _, p := range parts {
		if text, ok := p.Text.(string); ok && p.Type == "text" {
			texts = append(texts, text)
		}
	}
	return strings.Join(texts, "\n")
}

// candidatesInSession reads the preference candidates in one session file's user messages. Only `message` entries
// with role `user` are read. Every typed message also adds its words to counts, when given.
func candidatesInSession(data []byte, session string, fallbackAt int64, counts *WordCounts) []PrefCandidate {
	var found []PrefCandidate
	from := 0
	for from <= len(data) {
		rel := bytes.Index(data[from:], userMarker)
		if rel < 0 {
			break
		}
		hit := from + rel
		start := bytes.LastIndexByte(data[:hit], '\n') + 1
		end := len(data)
		if nl := bytes.IndexByte(data[hit:], '\n'); nl >= 0 {
			end = hit + nl
		}
		from = end + 1
		var entry sessionEntry
		if err := json.Unmarshal(data[start:end], &entry); err != nil {
			// A torn last line in a session that is still being written: the complete lines before it still count.
			continue
		}
		if entry.Type != "message" || entry.Message == nil || entry.Message.Role != "user" {
			continue
		}
		at := fallbackAt
		if ts, ok := entry.Timestamp.(string); ok {
			if parsed, ok := jsDateParse(ts); ok {
				at = parsed
			}
		}
		text := userMessageText(entry.Message.Content)
		if counts != nil && isHumanTyped(text) {
			counts.add(tokens(text))
		}
		for _, clause := range extractPreferences(text) {
			found = append(found, PrefCandidate{Clause: clause, Session: session, At: at})
		}
		for _, clause := range extractPermissions(text) {
			found = append(found, PrefCandidate{Clause: clause, Session: session, At: at, Permit: true})
		}
	}
	return found
}

// worktreeRoots are the worktree roots of the repository that holds cwd, from one `git worktree list`. Not a
// repository, or no git on this machine: none.
func worktreeRoots(cwd string, git GitRunner) []string {
	if git == nil {
		git = runGit
	}
	out, ok := git(cwd, "-C", cwd, "worktree", "list", "--porcelain")
	if !ok {
		return nil
	}
	var roots []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "worktree ") {
			roots = append(roots, strings.TrimPrefix(line, "worktree "))
		}
	}
	return roots
}

// inWorktree is true when cwd is a worktree root or a directory inside one that is not a nested repository of its own.
func inWorktree(cwd string, roots []string) bool {
	for _, root := range roots {
		rel, err := filepath.Rel(root, cwd)
		if err != nil {
			continue
		}
		if rel == "." {
			return true
		}
		if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			continue
		}
		nested := false
		for dir := cwd; dir != root && dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
			if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
				nested = true
				break
			}
		}
		if !nested {
			return true
		}
	}
	return false
}

// sessionCwd is the cwd of a session directory, from the header line of one of its files.
func sessionCwd(dir string) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	tried := 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		if tried == 3 {
			break
		}
		tried++
		file, err := os.Open(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		buf := make([]byte, 4096)
		n, _ := file.Read(buf)
		_ = file.Close()
		line, _, _ := strings.Cut(string(buf[:n]), "\n")
		var header struct {
			Type string `json:"type"`
			Cwd  any    `json:"cwd"`
		}
		// A header cut at 4 KB or an empty file: try the next file of the directory.
		if json.Unmarshal([]byte(line), &header) != nil {
			continue
		}
		if cwd, ok := header.Cwd.(string); ok && header.Type == "session" {
			return cwd, true
		}
	}
	return "", false
}

// projectSessionDirs: Pi files sessions by working directory, so each worktree of a repository has its own session
// directory. The sibling directories whose sessions ran in a worktree of the same repository belong to the project too.
func projectSessionDirs(dir string, roots []string) []string {
	if len(roots) == 0 {
		return []string{dir}
	}
	parent := filepath.Dir(dir)
	entries, _ := os.ReadDir(parent)
	out := []string{dir}
	for _, e := range entries {
		path := filepath.Join(parent, e.Name())
		if !e.IsDir() || path == filepath.Clean(dir) {
			continue
		}
		if where, ok := sessionCwd(path); ok && inWorktree(where, roots) {
			out = append(out, path)
		}
	}
	return out
}

// ScanOptions configure scanPreferences.
type ScanOptions struct {
	// Dir is the project's session directory (ctx.SessionManager().GetSessionDir()).
	Dir string
	// Exclude is the current session file; it is not an earlier session.
	Exclude string
	// Cwd is the session's working directory. Inside a git repository, sessions of its other worktrees are read too.
	Cwd         string
	MaxSessions int // 0: prefsMaxSessions
	MaxAgeDays  int // 0: prefsMaxAgeDays
	Now         int64
	Budget      time.Duration // 0: prefsScanBudget
	Git         GitRunner
}

// scanPreferences reads the newest earlier sessions of this project and its worktrees (at most MaxSessions, none
// older than MaxAgeDays). Read-only.
func scanPreferences(opts ScanOptions) *PrefsScan {
	started := time.Now()
	now := opts.Now
	if now == 0 {
		now = started.UnixMilli()
	}
	maxAge, maxSessions, budget := opts.MaxAgeDays, opts.MaxSessions, opts.Budget
	if maxAge == 0 {
		maxAge = prefsMaxAgeDays
	}
	if maxSessions == 0 {
		maxSessions = prefsMaxSessions
	}
	if budget == 0 {
		budget = prefsScanBudget
	}
	oldest := float64(now - int64(maxAge)*dayMs)
	exclude := ""
	if opts.Exclude != "" {
		exclude = filepath.Base(opts.Exclude)
	}
	var roots []string
	if opts.Cwd != "" {
		roots = worktreeRoots(opts.Cwd, opts.Git)
	}
	directories := projectSessionDirs(opts.Dir, roots)
	type file struct {
		path  string
		mtime float64
	}
	var files []file
	for _, dir := range directories {
		// No session directory yet (a first session, or an in-memory session): nothing earlier to learn from there.
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.HasSuffix(name, ".jsonl") || name == exclude {
				continue
			}
			path := filepath.Join(dir, name)
			info, err := os.Stat(path)
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			mtime := float64(info.ModTime().UnixNano()) / 1e6
			if mtime >= oldest {
				files = append(files, file{path, mtime})
			}
		}
	}
	sort.SliceStable(files, func(i, j int) bool { return files[i].mtime > files[j].mtime })
	var candidates []PrefCandidate
	counts := emptyWordCounts()
	scanned := 0
	deadline := started.Add(budget)
	for i, f := range files {
		if i >= maxSessions || time.Now().After(deadline) {
			break
		}
		data, err := os.ReadFile(f.path)
		if err != nil {
			continue
		}
		scanned++
		candidates = append(candidates, candidatesInSession(data, filepath.Base(f.path), int64(f.mtime), counts)...)
	}
	project := opts.Dir
	if opts.Cwd != "" {
		project = opts.Cwd
	}
	if len(roots) > 0 {
		project = roots[0]
	}
	return &PrefsScan{
		Prefs:       groupPreferences(candidates, counts, prefsMinSessions, maxPrefs),
		Candidates:  candidates,
		Counts:      counts,
		Project:     project,
		Scanned:     scanned,
		Directories: len(directories),
		Ms:          float64(time.Since(started).Microseconds()) / 1000,
	}
}
