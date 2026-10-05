package acp

import (
	"bufio"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// PiSessionListItem is one session found on disk.
type PiSessionListItem struct {
	SessionID   string
	Cwd         string
	Title       *string
	UpdatedAt   *string
	SessionFile string
}

const (
	defaultTailBytes = 256 * 1024
	defaultHeadBytes = 64 * 1024
)

func sessionDirFromSettings(agentDir string) string {
	data := readJSONObject(filepath.Join(agentDir, "settings.json"))
	s, ok := data["sessionDir"].(string)
	if !ok || jsTrim(s) == "" {
		return ""
	}
	if filepath.IsAbs(s) {
		return s
	}
	return filepath.Join(agentDir, s)
}

// PiSessionsDir is where PiG keeps session files: PIG_CODING_AGENT_SESSION_DIR (in shared mode
// PI_CODING_AGENT_SESSION_DIR, as pig reads it), else the sessionDir setting, else <agent dir>/sessions.
func PiSessionsDir() string {
	env := "PIG_CODING_AGENT_SESSION_DIR"
	if sharedPiDirs() {
		env = "PI_CODING_AGENT_SESSION_DIR"
	}
	if d := os.Getenv(env); d != "" {
		return expandTilde(d)
	}
	agent := AgentDir()
	if d := sessionDirFromSettings(agent); d != "" {
		return d
	}
	return filepath.Join(agent, "sessions")
}

func walkJSONL(dir string, out *[]string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		switch {
		case e.IsDir():
			walkJSONL(p, out)
		case e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".jsonl"):
			*out = append(*out, p)
		case e.Type()&fs.ModeSymlink != 0 && strings.HasSuffix(e.Name(), ".jsonl"):
			if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
				*out = append(*out, p)
			}
		}
	}
}

func readFirstLine(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	buf := make([]byte, defaultHeadBytes)
	n, _ := io.ReadFull(f, buf)
	if n <= 0 {
		return "", false
	}
	s := string(buf[:n])
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return jsTrim(s[:i]), true
	}
	return jsTrim(s), true
}

func readTail(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	start := st.Size() - defaultTailBytes
	if start < 0 {
		start = 0
	}
	buf := make([]byte, st.Size()-start)
	n, err := f.ReadAt(buf, start)
	if err != nil && err != io.EOF {
		return "", err
	}
	return string(buf[:n]), nil
}

func splitLines(s string) []string { return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") }

func parseSessionHeader(first string) (id, cwd string, ok bool) {
	var obj map[string]any
	if json.Unmarshal([]byte(first), &obj) != nil || obj["type"] != "session" {
		return "", "", false
	}
	id, _ = obj["id"].(string)
	cwd, _ = obj["cwd"].(string)
	if id == "" || cwd == "" {
		return "", "", false
	}
	return id, cwd, true
}

func sessionInfoName(line string) (string, bool) {
	line = jsTrim(line)
	if line == "" {
		return "", false
	}
	var obj map[string]any
	if json.Unmarshal([]byte(line), &obj) != nil || obj["type"] != "session_info" {
		return "", false
	}
	if name, ok := obj["name"].(string); ok && jsTrim(name) != "" {
		return jsTrim(name), true
	}
	return "", false
}

func pickTitleFromTail(tail string) string {
	lines := splitLines(tail)
	for i := len(lines) - 1; i >= 0; i-- {
		if name, ok := sessionInfoName(lines[i]); ok {
			return name
		}
	}
	return ""
}

func scanSessionInfoName(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 256*1024)
	last := ""
	for {
		line, err := r.ReadString('\n')
		if name, ok := sessionInfoName(line); ok {
			last = name
		}
		if err != nil {
			break
		}
	}
	return last
}

func parseTimestamp(ts string) (string, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05Z07:00", "2006-01-02"} {
		if t, err := time.Parse(layout, ts); err == nil {
			return t.UTC().Format("2006-01-02T15:04:05.000Z"), true
		}
	}
	return "", false
}

func pickUpdatedAtFromTail(tail string) string {
	lines := splitLines(tail)
	scan := func(onlyMessages bool) string {
		for i := len(lines) - 1; i >= 0; i-- {
			line := jsTrim(lines[i])
			if line == "" {
				continue
			}
			var obj map[string]any
			if json.Unmarshal([]byte(line), &obj) != nil {
				continue
			}
			if onlyMessages && obj["type"] != "message" {
				continue
			}
			if ts, ok := obj["timestamp"].(string); ok {
				if iso, ok := parseTimestamp(ts); ok {
					return iso
				}
			}
		}
		return ""
	}
	if s := scan(true); s != "" {
		return s
	}
	return scan(false)
}

func pickFallbackTitleFromHead(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := splitLines(string(raw))
	for _, l := range lines {
		line := jsTrim(l)
		if line == "" {
			continue
		}
		var obj map[string]any
		if json.Unmarshal([]byte(line), &obj) == nil && obj["type"] == "message" {
			msg := asObject(obj["message"])
			if msg["role"] == "user" {
				switch c := msg["content"].(type) {
				case string:
					return truncateRunes(c, 80)
				case []any:
					for _, b := range c {
						m := asObject(b)
						if s, ok := m["text"].(string); ok && m["type"] == "text" && s != "" {
							return truncateRunes(s, 80)
						}
					}
				}
			}
		}
		if len(lines) > 2000 {
			break
		}
	}
	return ""
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// ListPiSessions scans the sessions directory, most recent first.
func ListPiSessions() []PiSessionListItem {
	var files []string
	walkJSONL(PiSessionsDir(), &files)
	items := []PiSessionListItem{}
	for _, file := range files {
		first, ok := readFirstLine(file)
		if !ok || first == "" {
			continue
		}
		id, cwd, ok := parseSessionHeader(first)
		if !ok {
			continue
		}
		var title, updated string
		if tail, err := readTail(file); err == nil {
			title = pickTitleFromTail(tail)
			updated = pickUpdatedAtFromTail(tail)
		}
		if title == "" {
			title = scanSessionInfoName(file)
		}
		if updated == "" {
			if st, err := os.Stat(file); err == nil {
				updated = st.ModTime().UTC().Format("2006-01-02T15:04:05.000Z")
			}
		}
		if title == "" {
			title = pickFallbackTitleFromHead(file)
		}
		item := PiSessionListItem{SessionID: id, Cwd: cwd, SessionFile: file}
		if title != "" {
			item.Title = &title
		}
		if updated != "" {
			item.UpdatedAt = &updated
		}
		items = append(items, item)
	}
	key := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	sort.SliceStable(items, func(i, j int) bool { return key(items[i].UpdatedAt) > key(items[j].UpdatedAt) })
	return items
}

// FindPiSession finds one session by id.
func FindPiSession(sessionID string) *PiSessionListItem {
	for _, s := range ListPiSessions() {
		if s.SessionID == sessionID {
			s := s
			return &s
		}
	}
	return nil
}
