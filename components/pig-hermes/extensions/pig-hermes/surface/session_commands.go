package surface

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/memory"
	"github.com/MichaelKinsy/pigpen/pig-hermes/memoryskills"
	"github.com/MichaelKinsy/pigpen/pig-hermes/sessions"
)

// ── /context & /ctx ──────────────────────────────────────────────────────────

func (s *state) cmdContext(ctx sdk.Context, _ string) error {
	cwd := ""
	if c := strings.TrimSpace(ctx.Cwd()); c != "" {
		cwd = c
	}

	entries := memoryskills.BuildContextManifest(cwd, s.paths)
	_, memChars, memLimit, _ := s.memStore.ReadLive(memory.TargetMemory)
	_, userChars, userLimit, _ := s.memStore.ReadLive(memory.TargetUser)

	loadedCount := 0
	totalEstTok := 0
	for _, e := range entries {
		if e.Loaded {
			loadedCount++
			totalEstTok += e.EstTokens
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Hermes Context Manifest (%d loaded, ~%d est. tokens)\n", loadedCount, totalEstTok)
	b.WriteString(memoryskills.RenderContextManifest(entries))
	fmt.Fprintf(&b, "\n\nMemory\n")
	fmt.Fprintf(&b, "  MEMORY.md: %d/%d chars (%d%%)\n", memChars, memLimit, calcPercent(memChars, memLimit))
	fmt.Fprintf(&b, "  USER.md:   %d/%d chars (%d%%)", userChars, userLimit, calcPercent(userChars, userLimit))

	if cu, err := ctx.GetContextUsage(); err == nil && cu != nil {
		if cu.Tokens != nil && cu.Percent != nil {
			fmt.Fprintf(&b, "\n\nActive Context Window\n  %d / %d tokens (%.1f%%)", *cu.Tokens, cu.ContextWindow, *cu.Percent)
		} else if cu.ContextWindow > 0 {
			fmt.Fprintf(&b, "\n\nActive Context Window\n  %d tokens max (usage unknown)", cu.ContextWindow)
		}
	}

	s.show(ctx, b.String())
	return nil
}

// ── /sessions [query] ────────────────────────────────────────────────────────

type sessionListItem struct {
	ID           string
	Title        string
	Source       string // "pig" or "hermes"
	MessageCount int
	UpdatedAt    string
	SortKey      string
}

func (s *state) cmdSessions(ctx sdk.Context, args string) error {
	query := strings.TrimSpace(args)
	lower := strings.ToLower(query)
	if lower == "list" || lower == "ls" || lower == "browse" {
		query = ""
	}

	activeFile := ""
	if sf, err := ctx.GetSessionFile(); err == nil && sf != nil {
		activeFile = *sf
	}
	pigDirs := sessions.DefaultSessionDirs(activeFile)

	// Collect PiG JSONL sessions.
	pigResp := sessions.ExecuteSearch(pigDirs, sessions.SearchParams{
		Query: query,
		Limit: 20,
		Sort:  "newest",
	})

	// Collect upstream ~/.hermes/state.db (or profile state.db) sessions.
	var stateDBs []string
	seenDB := make(map[string]bool)
	addDB := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		clean := filepath.Clean(p)
		if seenDB[clean] {
			return
		}
		if info, err := os.Stat(clean); err == nil && !info.IsDir() {
			seenDB[clean] = true
			stateDBs = append(stateDBs, clean)
		}
	}
	if s.paths.Home != "" {
		addDB(filepath.Join(s.paths.Home, "state.db"))
	}
	if s.paths.RootHome != "" {
		addDB(filepath.Join(s.paths.RootHome, "state.db"))
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		addDB(filepath.Join(home, ".hermes", "state.db"))
	}

	var hermesResp sessions.SearchResponse
	if len(stateDBs) > 0 {
		hermesResp = sessions.ExecuteSearch(nil, sessions.SearchParams{
			Query:        query,
			Limit:        20,
			Sort:         "newest",
			StateDBPaths: stateDBs,
		})
	}

	seenID := make(map[string]bool)
	var items []sessionListItem

	appendResp := func(resp sessions.SearchResponse, source string) {
		if resp.Mode == "browse" {
			for _, sess := range resp.Sessions {
				if sess.SessionID == "" || seenID[sess.SessionID] {
					continue
				}
				seenID[sess.SessionID] = true
				title := strings.TrimSpace(sess.Title)
				if title == "" {
					title = strings.TrimSpace(sess.Preview)
				}
				if title == "" {
					title = "(untitled)"
				}
				ts := sess.UpdatedAt
				if ts == "" {
					ts = sess.When
				}
				items = append(items, sessionListItem{
					ID:           sess.SessionID,
					Title:        truncateLine(title, 60),
					Source:       source,
					MessageCount: sess.MessageCount,
					UpdatedAt:    ts,
					SortKey:      ts,
				})
			}
			return
		}
		for _, hit := range resp.Results {
			if hit.SessionID == "" || seenID[hit.SessionID] {
				continue
			}
			seenID[hit.SessionID] = true
			title := strings.TrimSpace(hit.Title)
			if title == "" {
				title = strings.TrimSpace(hit.Snippet)
			}
			if title == "" {
				title = "(untitled)"
			}
			ts := hit.When
			if ts == "" {
				ts = hit.Timestamp
			}
			items = append(items, sessionListItem{
				ID:           hit.SessionID,
				Title:        truncateLine(title, 60),
				Source:       source,
				MessageCount: hit.TotalMessages,
				UpdatedAt:    ts,
				SortKey:      ts,
			})
		}
	}

	appendResp(pigResp, "pig")
	appendResp(hermesResp, "hermes")

	sort.Slice(items, func(i, j int) bool {
		if items[i].SortKey != items[j].SortKey {
			return items[i].SortKey > items[j].SortKey
		}
		return items[i].ID < items[j].ID
	})
	if len(items) > 20 {
		items = items[:20]
	}

	if len(items) == 0 {
		if query != "" {
			s.show(ctx, fmt.Sprintf("No sessions matched %q.", query))
		} else {
			s.show(ctx, "No saved sessions found.")
		}
		return nil
	}

	var b strings.Builder
	if query != "" {
		fmt.Fprintf(&b, "Sessions matching %q (%d):\n", query, len(items))
	} else {
		fmt.Fprintf(&b, "Recent Sessions (%d):\n", len(items))
	}
	for _, it := range items {
		fmt.Fprintf(&b, "  - %-16s [%s]  %3d msgs  %s\n", it.ID, it.Source, it.MessageCount, it.Title)
	}
	b.WriteString("\nResume with PiG /resume (or `hermes --resume <id>` / `session_search(session_id=...)`).")
	s.show(ctx, b.String())
	return nil
}

// ── /save <md|json> [path] ───────────────────────────────────────────────────

type exportedEntry struct {
	ID        string `json:"id,omitempty"`
	ParentID  string `json:"parent_id,omitempty"`
	Type      string `json:"type"`
	Role      string `json:"role,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
	Content   string `json:"content,omitempty"`
}

func (s *state) cmdSave(ctx sdk.Context, args string) error {
	fields := strings.Fields(strings.TrimSpace(args))
	if len(fields) == 0 {
		s.show(ctx, "Usage: /save <md|json> [path]")
		return nil
	}

	format := strings.ToLower(fields[0])
	switch format {
	case "md", "markdown":
		format = "md"
	case "json":
		format = "json"
	default:
		s.show(ctx, fmt.Sprintf("Unsupported export format %q. Usage: /save <md|json> [path]", fields[0]))
		return nil
	}

	rawEntries, err := ctx.GetEntries()
	if err != nil {
		s.show(ctx, fmt.Sprintf("Failed to read session entries: %v", err))
		return nil
	}

	var outPath string
	if len(fields) > 1 {
		outPath = strings.TrimSpace(strings.Join(fields[1:], " "))
		if !filepath.IsAbs(outPath) {
			if cwd := strings.TrimSpace(ctx.Cwd()); cwd != "" {
				outPath = filepath.Join(cwd, outPath)
			}
		}
	} else {
		ts := time.Now().UTC().Format("20060102-150405")
		outPath = filepath.Join(s.paths.Home, "exports", fmt.Sprintf("session-%s.%s", ts, format))
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		s.show(ctx, fmt.Sprintf("Failed to create export directory: %v", err))
		return nil
	}

	parsed := parseRawSessionEntries(rawEntries)
	sessionID, _ := ctx.GetSessionID()

	var payload []byte
	if format == "json" {
		doc := map[string]any{
			"session_id":  sessionID,
			"exported_at": time.Now().UTC().Format(time.RFC3339),
			"count":       len(parsed),
			"entries":     parsed,
		}
		payload, err = json.MarshalIndent(doc, "", "  ")
		if err != nil {
			s.show(ctx, fmt.Sprintf("Failed to encode JSON export: %v", err))
			return nil
		}
		payload = append(payload, '\n')
	} else {
		payload = []byte(renderEntriesMarkdown(sessionID, parsed))
	}

	if err := os.WriteFile(outPath, payload, 0o600); err != nil {
		s.show(ctx, fmt.Sprintf("Failed to write export %s: %v", outPath, err))
		return nil
	}

	s.show(ctx, fmt.Sprintf("Saved %d session entries as %s to %s", len(parsed), format, outPath))
	return nil
}

// ── /retry ───────────────────────────────────────────────────────────────────

func (s *state) cmdRetry(ctx sdk.Context, _ string) error {
	rawEntries, err := ctx.GetEntries()
	if err != nil {
		s.show(ctx, fmt.Sprintf("Failed to read session entries: %v", err))
		return nil
	}
	parsed := parseRawSessionEntries(rawEntries)

	lastUser := ""
	for i := len(parsed) - 1; i >= 0; i-- {
		if parsed[i].Role == "user" && strings.TrimSpace(parsed[i].Content) != "" {
			lastUser = parsed[i].Content
			break
		}
	}
	if lastUser == "" {
		s.show(ctx, "No prior user message found to retry.")
		return nil
	}

	if err := ctx.SendUserMessage(lastUser, ""); err != nil {
		s.show(ctx, fmt.Sprintf("Failed to retry last user message: %v", err))
		return nil
	}
	return nil
}

// ── /undo [N] ────────────────────────────────────────────────────────────────

func (s *state) cmdUndo(ctx sdk.Context, args string) error {
	turnsToUndo := 1
	if trimmed := strings.TrimSpace(args); trimmed != "" {
		n, err := strconv.Atoi(trimmed)
		if err != nil || n < 1 {
			s.show(ctx, "Usage: /undo [N] (where N >= 1)")
			return nil
		}
		turnsToUndo = n
	}

	rawEntries, err := ctx.GetEntries()
	if err != nil {
		s.show(ctx, fmt.Sprintf("Failed to read session entries: %v", err))
		return nil
	}
	parsed := parseRawSessionEntries(rawEntries)

	var userIndices []int
	for i, e := range parsed {
		if e.Role == "user" {
			userIndices = append(userIndices, i)
		}
	}
	if len(userIndices) == 0 {
		s.show(ctx, "Nothing to undo: no user turns in this session.")
		return nil
	}

	if turnsToUndo > len(userIndices) {
		turnsToUndo = len(userIndices)
	}
	cutIdx := userIndices[len(userIndices)-turnsToUndo]
	targetEntry := parsed[cutIdx]

	// Determine the parent entry ID before the undone user turn.
	targetID := strings.TrimSpace(targetEntry.ParentID)
	if targetID == "" && cutIdx > 0 {
		targetID = strings.TrimSpace(parsed[cutIdx-1].ID)
	}

	if targetID != "" {
		res, navErr := ctx.NavigateTree(targetID, nil)
		if navErr == nil {
			if res.Cancelled {
				s.show(ctx, "Undo cancelled.")
				return nil
			}
			s.show(ctx, fmt.Sprintf("Undid %d turn(s) — navigated to entry %s.", turnsToUndo, targetID))
			return nil
		}
		s.show(ctx, fmt.Sprintf("Could not rewind automatically (%v). Last user turn was %q (entry %s); use PiG's /tree command to select an earlier branch point.",
			navErr, truncateLine(targetEntry.Content, 60), targetEntry.ID))
		return nil
	}

	s.show(ctx, fmt.Sprintf("Last user turn (%q) is the first turn in the session (no earlier parent entry). Use /new to start fresh or /tree to inspect branches.",
		truncateLine(targetEntry.Content, 60)))
	return nil
}

// ── Session Entry Parsing & Markdown Formatting Helpers ──────────────────────

func parseRawSessionEntries(rawEntries []json.RawMessage) []exportedEntry {
	out := make([]exportedEntry, 0, len(rawEntries))
	for _, raw := range rawEntries {
		var envelope struct {
			ID        string          `json:"id"`
			ParentID  string          `json:"parentId"`
			Type      string          `json:"type"`
			Timestamp string          `json:"timestamp"`
			Role      string          `json:"role"`
			Content   json.RawMessage `json:"content"`
			Summary   string          `json:"summary"`
			Message   json.RawMessage `json:"message"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			continue
		}

		e := exportedEntry{
			ID:        envelope.ID,
			ParentID:  envelope.ParentID,
			Type:      envelope.Type,
			Timestamp: envelope.Timestamp,
			Role:      strings.ToLower(strings.TrimSpace(envelope.Role)),
		}

		if len(envelope.Message) > 0 && envelope.Message[0] == '{' {
			var msg struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			}
			if json.Unmarshal(envelope.Message, &msg) == nil {
				if msg.Role != "" {
					e.Role = strings.ToLower(strings.TrimSpace(msg.Role))
				}
				e.Content = extractEntryText(msg.Content)
			}
		} else {
			e.Content = extractEntryText(envelope.Content)
			if e.Content == "" {
				e.Content = envelope.Summary
			}
		}

		out = append(out, e)
	}
	return out
}

func extractEntryText(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	if trimmed[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
		return ""
	}
	if trimmed[0] == '[' {
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(raw, &blocks) == nil {
			var parts []string
			for _, blk := range blocks {
				if blk.Text != "" {
					parts = append(parts, blk.Text)
				}
			}
			return strings.Join(parts, "\n")
		}
	}
	return ""
}

func renderEntriesMarkdown(sessionID string, entries []exportedEntry) string {
	var b strings.Builder
	title := "Hermes Session Export"
	if sessionID != "" {
		title += " — " + sessionID
	}
	fmt.Fprintf(&b, "# %s\n\n", title)
	fmt.Fprintf(&b, "_Exported %s_\n\n", time.Now().UTC().Format(time.RFC3339))

	if len(entries) == 0 {
		b.WriteString("_(no entries)_\n")
		return b.String()
	}

	for _, e := range entries {
		heading := e.Role
		if heading == "" {
			heading = e.Type
		}
		if heading == "" {
			heading = "entry"
		}
		heading = strings.ToUpper(heading[:1]) + heading[1:]
		if e.Timestamp != "" {
			fmt.Fprintf(&b, "## %s (%s)\n\n", heading, e.Timestamp)
		} else {
			fmt.Fprintf(&b, "## %s\n\n", heading)
		}
		if strings.TrimSpace(e.Content) != "" {
			b.WriteString(strings.TrimSpace(e.Content))
			b.WriteString("\n\n")
		} else {
			b.WriteString("_(empty)_\n\n")
		}
	}
	return b.String()
}
