package engine

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Report is what a pig handed off, with the tokens and cost it used.
type Report struct {
	Text   string
	Tokens int
	Cost   float64
	Source string
}

type sessionLine struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Message   struct {
		Role      string          `json:"role"`
		Timestamp json.RawMessage `json:"timestamp"`
		Content   json.RawMessage `json:"content"`
		Usage     *struct {
			Input      int `json:"input"`
			Output     int `json:"output"`
			CacheRead  int `json:"cacheRead"`
			CacheWrite int `json:"cacheWrite"`
			Total      int `json:"totalTokens"`
			Cost       struct {
				Total float64 `json:"total"`
			} `json:"cost"`
		} `json:"usage"`
	} `json:"message"`
}

func (l *sessionLine) millis() int64 {
	if n, err := strconv.ParseFloat(strings.Trim(string(l.Message.Timestamp), `"`), 64); err == nil && n > 0 {
		return int64(n)
	}
	if l.Timestamp != "" {
		if t, err := time.Parse(time.RFC3339Nano, l.Timestamp); err == nil {
			return t.UnixMilli()
		}
	}
	return 0
}

func (l *sessionLine) text() string {
	var s string
	if json.Unmarshal(l.Message.Content, &s) == nil {
		return strings.TrimSpace(s)
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(l.Message.Content, &parts) != nil {
		return ""
	}
	var texts []string
	for _, p := range parts {
		if p.Type == "text" && strings.TrimSpace(p.Text) != "" {
			texts = append(texts, strings.TrimSpace(p.Text))
		}
	}
	return strings.Join(texts, "\n")
}

// ReadSession extracts the report from a PiG session JSONL: the text parts of the
// last assistant message. Tokens and cost sum the usage of assistant messages
// stamped at or after sinceMs (messages without a stamp always count).
func ReadSession(path string, sinceMs int64) (Report, error) {
	f, err := os.Open(path)
	if err != nil {
		return Report{}, err
	}
	defer f.Close()
	var rep Report
	rd := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := rd.ReadBytes('\n')
		if len(line) > 0 {
			var sl sessionLine
			if json.Unmarshal(line, &sl) == nil && sl.Type == "message" && sl.Message.Role == "assistant" {
				rep.Text = sl.text()
				if u := sl.Message.Usage; u != nil {
					if t := sl.millis(); t == 0 || t >= sinceMs {
						n := u.Total
						if n == 0 {
							n = u.Input + u.Output + u.CacheRead + u.CacheWrite
						}
						rep.Tokens += n
						rep.Cost += u.Cost.Total
					}
				}
			}
		}
		if err != nil {
			break
		}
	}
	rep.Source = path
	return rep, nil
}

// NewestJSONL returns the newest *.jsonl in dir modified at or after after (zero: any).
func NewestJSONL(dir string, after time.Time) (string, bool) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	type f struct {
		p string
		m time.Time
	}
	var files []f
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().Before(after) {
			continue
		}
		files = append(files, f{filepath.Join(dir, e.Name()), info.ModTime()})
	}
	if len(files) == 0 {
		return "", false
	}
	sort.Slice(files, func(i, j int) bool { return files[i].m.After(files[j].m) })
	return files[0].p, true
}

// SessionDirFromArgv finds --session-dir in a pig's argv.
func SessionDirFromArgv(argv []string) string {
	for i, a := range argv {
		if a == "--session-dir" && i+1 < len(argv) {
			return argv[i+1]
		}
		if v, ok := strings.CutPrefix(a, "--session-dir="); ok {
			return v
		}
	}
	return ""
}

// PigSessionDir is where PiG keeps the sessions of a working directory.
func PigSessionDir(root, cwd string) string {
	slug := "--" + strings.ReplaceAll(strings.TrimPrefix(cwd, "/"), "/", "-") + "--"
	return filepath.Join(root, slug)
}

func defaultPigSessionsRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".pig", "agent", "sessions")
}

// readReport follows the report read path for a crate that reached pig `addr`.
func (e *Engine) readReport(c *Crate, addr string, term pigTerm) Report {
	since := time.UnixMilli(c.SentAt)
	try := func(path string, usageSince int64) (Report, bool) {
		rep, err := ReadSession(path, usageSince)
		if err != nil || rep.Text == "" {
			return Report{}, false
		}
		return rep, true
	}
	// (1) the pig's session dir: its --session-dir, or D/sessions/<address>
	dir := SessionDirFromArgv(term.argv)
	if dir == "" {
		dir = filepath.Join(e.Dir, "sessions", addr)
	}
	if p, ok := NewestJSONL(dir, time.Time{}); ok {
		if rep, ok := try(p, c.SentAt); ok {
			return rep
		}
	}
	// (2) the newest PiG session for the pig's cwd, written after the crate arrived
	if term.cwd != "" && e.PigSessions != "" {
		if p, ok := NewestJSONL(PigSessionDir(e.PigSessions, term.cwd), since); ok {
			if rep, ok := try(p, c.SentAt); ok {
				return rep
			}
		}
	}
	// (3) the terminal's screen
	if txt, err := e.HC.Read(addr, 80); err == nil && strings.TrimSpace(txt) != "" {
		return Report{Text: strings.TrimSpace(txt), Source: "hc read"}
	}
	return Report{}
}
