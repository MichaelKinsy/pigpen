package advisor

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// upstream: packages/coding-agent/src/advisor/transcript-recorder.ts

const (
	// TranscriptStem is the reserved filename stem for advisor JSONL transcripts.
	// upstream: packages/coding-agent/src/advisor/transcript-recorder.ts:15-16
	TranscriptStem     = "__advisor"
	TranscriptFilename = TranscriptStem + ".jsonl"
	jsonlSuffix        = ".jsonl"
)

// TranscriptFilenameForSlug returns `__advisor.jsonl` for the default advisor
// (empty slug) or `__advisor.<slug>.jsonl` for a named advisor.
// upstream: packages/coding-agent/src/advisor/transcript-recorder.ts:25-27
func TranscriptFilenameForSlug(slug string) string {
	if slug == "" {
		return TranscriptFilename
	}
	return TranscriptStem + "." + slug + jsonlSuffix
}

// IsTranscriptFilename reports whether name is an advisor transcript file.
// upstream: packages/coding-agent/src/advisor/transcript-recorder.ts:30-35
func IsTranscriptFilename(name string) bool {
	return name == TranscriptFilename ||
		(strings.HasPrefix(name, TranscriptStem+".") && strings.HasSuffix(name, jsonlSuffix))
}

// TranscriptEntry is one recorded advisor interaction stored in memory and
// optionally persisted to `<sessionDir>/__advisor[.<slug>].jsonl`.
type TranscriptEntry struct {
	Timestamp    int64   `json:"timestamp"`
	Role         string  `json:"role"` // "user" | "assistant" | "advisory"
	Advisor      string  `json:"advisor,omitempty"`
	Model        string  `json:"model,omitempty"`
	Provider     string  `json:"provider,omitempty"`
	Turn         int     `json:"turn,omitempty"`
	Content      string  `json:"content"`
	Severity     string  `json:"severity,omitempty"`
	InputTokens  int     `json:"inputTokens,omitempty"`
	OutputTokens int     `json:"outputTokens,omitempty"`
	Cost         float64 `json:"cost,omitempty"`
}

type transcriptFileRecord struct {
	Type    string          `json:"type"`
	ID      string          `json:"id,omitempty"`
	Message TranscriptEntry `json:"message,omitempty"`
}

// TranscriptRecorder appends advisor turns to `<sessionDir>/__advisor[.<slug>].jsonl`
// so spend and history survive across turns and can be scanned with stdlib JSONL.
type TranscriptRecorder struct {
	mu          sync.Mutex
	path        string
	headerWrote bool
	sessionID   string
}

// NewTranscriptRecorder binds a recorder to the session file's artifact directory.
func NewTranscriptRecorder(sessionFile, sessionID, slug string) *TranscriptRecorder {
	p := ""
	if strings.HasSuffix(sessionFile, jsonlSuffix) {
		dir := strings.TrimSuffix(sessionFile, jsonlSuffix)
		p = filepath.Join(dir, TranscriptFilenameForSlug(slug))
	}
	return &TranscriptRecorder{
		path:      p,
		sessionID: sessionID,
	}
}

// Record appends one entry to the advisor JSONL file (no-op when unpersisted).
func (r *TranscriptRecorder) Record(entry TranscriptEntry) error {
	if r == nil || r.path == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	if !r.headerWrote {
		if info, err := f.Stat(); err == nil && info.Size() == 0 {
			hdr := map[string]any{
				"type":      "session",
				"id":        r.sessionID,
				"timestamp": time.Now().UnixMilli(),
			}
			if b, err := json.Marshal(hdr); err == nil {
				_, _ = f.Write(append(b, '\n'))
			}
		}
		r.headerWrote = true
	}

	rec := map[string]any{
		"type": "message",
		"message": map[string]any{
			"role":      entry.Role,
			"advisor":   entry.Advisor,
			"model":     entry.Model,
			"provider":  entry.Provider,
			"turn":      entry.Turn,
			"content":   entry.Content,
			"severity":  entry.Severity,
			"timestamp": entry.Timestamp,
			"usage": map[string]any{
				"input":  entry.InputTokens,
				"output": entry.OutputTokens,
				"cost":   map[string]any{"total": entry.Cost},
			},
		},
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}

// LoadTranscriptCosts scans top-level `__advisor[.<slug>].jsonl` files beside
// sessionFile and sums persisted advisor cost per slug (stdlib JSONL scan).
// upstream: packages/coding-agent/src/advisor/transcript-recorder.ts:77-146
func LoadTranscriptCosts(sessionFile string) map[string]float64 {
	costs := map[string]float64{}
	if !strings.HasSuffix(sessionFile, jsonlSuffix) {
		return costs
	}
	dir := strings.TrimSuffix(sessionFile, jsonlSuffix)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return costs
	}
	for _, de := range entries {
		if de.IsDir() || !IsTranscriptFilename(de.Name()) {
			continue
		}
		slug := ""
		if de.Name() != TranscriptFilename {
			slug = strings.TrimSuffix(strings.TrimPrefix(de.Name(), TranscriptStem+"."), jsonlSuffix)
		}
		total := scanSingleTranscriptCost(filepath.Join(dir, de.Name()))
		if total > 0 {
			costs[slug] = total
		}
	}
	return costs
}

func scanSingleTranscriptCost(filePath string) float64 {
	f, err := os.Open(filePath)
	if err != nil {
		return 0
	}
	defer f.Close()

	var total float64
	validHeader := false
	firstLine := true
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var env map[string]any
		if json.Unmarshal([]byte(line), &env) != nil || env == nil {
			if firstLine {
				return 0
			}
			continue
		}
		if firstLine {
			firstLine = false
			typ, _ := env["type"].(string)
			id, _ := env["id"].(string)
			validHeader = typ == "session" && id != ""
			continue
		}
		if !validHeader {
			return 0
		}
		if typ, _ := env["type"].(string); typ != "message" {
			continue
		}
		msg, _ := env["message"].(map[string]any)
		if msg == nil {
			continue
		}
		role, _ := msg["role"].(string)
		if role != "assistant" {
			continue
		}
		usage, _ := msg["usage"].(map[string]any)
		if usage == nil {
			continue
		}
		costObj, _ := usage["cost"].(map[string]any)
		if costObj == nil {
			continue
		}
		if c, ok := costObj["total"].(float64); ok && c > 0 {
			total += c
		}
	}
	return total
}

// FormatHistoryText formats an advisor's recorded history in compact or raw mode.
// upstream: packages/coding-agent/src/session/session-advisors.ts:3215-3231
func FormatHistoryText(entries []TranscriptEntry, compact bool) string {
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	for i, e := range entries {
		if i > 0 {
			b.WriteString("\n\n")
		}
		if compact {
			header := strings.ToUpper(e.Role)
			if e.Advisor != "" {
				header += " [" + e.Advisor + "]"
			}
			if e.Severity != "" {
				header += " (" + e.Severity + ")"
			}
			if e.Turn > 0 {
				header += fmt.Sprintf(" turn=%d", e.Turn)
			}
			fmt.Fprintf(&b, "### %s\n%s", header, strings.TrimSpace(e.Content))
		} else {
			raw, _ := json.Marshal(e)
			b.Write(raw)
		}
	}
	return b.String()
}
