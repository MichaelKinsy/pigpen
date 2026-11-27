package sessions

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/paths"
)

const stateDBQueryTimeout = 5 * time.Second

type stateDBSessionRow struct {
	ID              string `json:"id"`
	Title           string `json:"title"`
	Model           string `json:"model"`
	StartedAt       any    `json:"started_at"`
	EndedAt         any    `json:"ended_at"`
	ParentSessionID string `json:"parent_session_id"`
}

type stateDBMessageRow struct {
	ID         int64  `json:"id"`
	SessionID  string `json:"session_id"`
	Role       string `json:"role"`
	Content    string `json:"content"`
	ToolCallID string `json:"tool_call_id"`
	ToolName   string `json:"tool_name"`
	ToolCalls  string `json:"tool_calls"`
	Timestamp  any    `json:"timestamp"`
}

// resolveStateDBPaths returns candidate upstream Hermes state.db paths for the
// given ProfilePaths and optional profile override.
func resolveStateDBPaths(homeDir, profilesDir, profileOverride string) []string {
	var out []string
	seen := make(map[string]bool)
	add := func(dbPath string) {
		dbPath = strings.TrimSpace(dbPath)
		if dbPath == "" {
			return
		}
		clean := filepath.Clean(dbPath)
		if seen[clean] {
			return
		}
		if info, err := os.Stat(clean); err == nil && !info.IsDir() {
			seen[clean] = true
			out = append(out, clean)
		}
	}

	prof := NormalizeProfileName(profileOverride)
	if prof != "" && prof != "default" && profilesDir != "" {
		if paths.ValidateProfileName(prof) {
			add(filepath.Join(profilesDir, prof, "state.db"))
		}
		return out
	}

	if homeDir != "" {
		add(filepath.Join(homeDir, "state.db"))
	}
	return out
}

// loadSessionsFromStateDB reads upstream Hermes sessions and messages from a
// SQLite state.db file via `sqlite3 -readonly -json` (zero cgo, 5s timeout).
// Missing sqlite3 binary, missing/empty/unreadable DB, or missing tables all
// degrade cleanly to nil without returning errors.
func loadSessionsFromStateDB(dbPath string) []*ParsedSession {
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" {
		return nil
	}
	info, err := os.Stat(dbPath)
	if err != nil || info.IsDir() || info.Size() == 0 {
		return nil
	}
	sqliteBin, err := exec.LookPath("sqlite3")
	if err != nil {
		return nil
	}

	sessionRows, ok := runStateDBQuery[stateDBSessionRow](
		sqliteBin,
		dbPath,
		`SELECT COALESCE(id, '') AS id,
		        COALESCE(title, '') AS title,
		        COALESCE(model, '') AS model,
		        started_at,
		        ended_at,
		        COALESCE(parent_session_id, '') AS parent_session_id
		   FROM sessions
		  ORDER BY started_at DESC`,
	)
	if !ok || len(sessionRows) == 0 {
		return nil
	}

	msgRows, _ := runStateDBQuery[stateDBMessageRow](
		sqliteBin,
		dbPath,
		`SELECT id,
		        COALESCE(session_id, '') AS session_id,
		        COALESCE(role, '') AS role,
		        COALESCE(content, '') AS content,
		        COALESCE(tool_call_id, '') AS tool_call_id,
		        COALESCE(tool_name, '') AS tool_name,
		        COALESCE(tool_calls, '') AS tool_calls,
		        timestamp
		   FROM messages
		  ORDER BY session_id ASC, id ASC`,
	)

	bySession := make(map[string][]stateDBMessageRow, len(sessionRows))
	for _, m := range msgRows {
		if m.SessionID == "" {
			continue
		}
		bySession[m.SessionID] = append(bySession[m.SessionID], m)
	}

	sessions := make([]*ParsedSession, 0, len(sessionRows))
	for _, row := range sessionRows {
		id := strings.TrimSpace(row.ID)
		if id == "" {
			continue
		}
		startedAt := parseStateDBTimestamp(row.StartedAt)
		updatedAt := parseStateDBTimestamp(row.EndedAt)
		if updatedAt.IsZero() {
			updatedAt = startedAt
		}

		ps := &ParsedSession{
			ID:            id,
			FilePath:      dbPath,
			Title:         strings.TrimSpace(row.Title),
			Model:         strings.TrimSpace(row.Model),
			ParentSession: strings.TrimSpace(row.ParentSessionID),
			StartedAt:     startedAt,
			UpdatedAt:     updatedAt,
		}

		rawMsgs := bySession[id]
		msgIdx := 0
		for _, m := range rawMsgs {
			role := normalizeRole(m.Role)
			if role == "" {
				continue
			}
			content := m.Content
			if role == "tool" && len(content) > toolContentPrefixCap {
				content = content[:toolContentPrefixCap]
			}
			toolCalls := parseStateDBToolCalls(m.ToolCalls)
			if strings.TrimSpace(content) == "" && len(toolCalls) == 0 {
				continue
			}
			ts := parseStateDBTimestamp(m.Timestamp)
			if !ts.IsZero() && ts.After(ps.UpdatedAt) {
				ps.UpdatedAt = ts
			}
			if ps.StartedAt.IsZero() && !ts.IsZero() {
				ps.StartedAt = ts
			}
			msgIdx++
			ps.Messages = append(ps.Messages, SessionMessage{
				Index:       msgIdx,
				EntryID:     strconv.FormatInt(m.ID, 10),
				Role:        role,
				RawRole:     m.Role,
				Content:     content,
				ToolName:    strings.TrimSpace(m.ToolName),
				ToolCallID:  strings.TrimSpace(m.ToolCallID),
				ToolCalls:   toolCalls,
				Timestamp:   ts,
				TimeStr:     formatTime(ts),
				IsCompacted: isCompactionSummary(content),
			})
		}

		if ps.StartedAt.IsZero() {
			ps.StartedAt = info.ModTime().UTC()
		}
		if ps.UpdatedAt.IsZero() {
			ps.UpdatedAt = ps.StartedAt
		}
		sessions = append(sessions, ps)
	}
	return sessions
}

func runStateDBQuery[T any](sqliteBin, dbPath, query string) ([]T, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), stateDBQueryTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, sqliteBin, "-readonly", "-json", dbPath, query)
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return nil, true
	}
	var rows []T
	if err := json.Unmarshal([]byte(trimmed), &rows); err != nil {
		return nil, false
	}
	return rows, true
}

func parseStateDBTimestamp(v any) time.Time {
	switch t := v.(type) {
	case nil:
		return time.Time{}
	case float64:
		return epochFloatToTime(t)
	case int64:
		return epochFloatToTime(float64(t))
	case int:
		return epochFloatToTime(float64(t))
	case json.Number:
		if f, err := t.Float64(); err == nil {
			return epochFloatToTime(f)
		}
		return time.Time{}
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return time.Time{}
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return epochFloatToTime(f)
		}
		return parseEntryTimestamp(s)
	default:
		return time.Time{}
	}
}

func epochFloatToTime(f float64) time.Time {
	if f <= 0 || math.IsNaN(f) || math.IsInf(f, 0) {
		return time.Time{}
	}
	// Heuristic: values >= 1e12 are milliseconds, otherwise seconds.
	if f >= 1e12 {
		sec := int64(f / 1000)
		nsec := int64(math.Mod(f, 1000) * float64(time.Millisecond))
		return time.Unix(sec, nsec).UTC()
	}
	sec := int64(f)
	nsec := int64((f - float64(sec)) * float64(time.Second))
	return time.Unix(sec, nsec).UTC()
}

func parseStateDBToolCalls(raw string) []ToolCallSummary {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" || raw == "[]" {
		return nil
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil
	}
	out := make([]ToolCallSummary, 0, len(items))
	for _, item := range items {
		id, _ := item["id"].(string)
		name, _ := item["name"].(string)
		var args string
		if fn, ok := item["function"].(map[string]any); ok {
			if name == "" {
				name, _ = fn["name"].(string)
			}
			switch a := fn["arguments"].(type) {
			case string:
				args = a
			case nil:
			default:
				if b, err := json.Marshal(a); err == nil {
					args = string(b)
				}
			}
		} else if a, ok := item["arguments"]; ok && a != nil {
			switch av := a.(type) {
			case string:
				args = av
			default:
				if b, err := json.Marshal(av); err == nil {
					args = string(b)
				}
			}
		}
		if name == "" {
			continue
		}
		out = append(out, ToolCallSummary{
			ID:        id,
			Name:      name,
			Arguments: args,
		})
	}
	return out
}
