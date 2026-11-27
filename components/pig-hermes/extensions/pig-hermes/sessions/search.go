package sessions

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// toolContentPrefixCap matches Hermes's FTS_TOOL_CONTENT_PREFIX_CHARS = 8192.
	toolContentPrefixCap = 8192
	// readMaxContent matches Hermes's _READ_MAX_CONTENT = 2000.
	readMaxContent = 2000
	// scrollToolRowMax / scrollTextMax match Hermes's _SCROLL_TOOL_ROW_MAX / _SCROLL_TEXT_MAX.
	scrollToolRowMax = 1500
	scrollTextMax    = 8000
	// toolArgsPreview / anchorToolArgsMax match Hermes's _TOOL_ARGS_PREVIEW / _ANCHOR_TOOL_ARGS_MAX.
	toolArgsPreview   = 200
	anchorToolArgsMax = 2000
	// snippetBefore / snippetAfter match Hermes's _SNIPPET_BEFORE / _SNIPPET_AFTER.
	snippetBefore = 100
	snippetAfter  = 200
	// defaultLimit / maxLimit / defaultBrowseLimit.
	defaultSearchLimit = 10
	maxSearchLimit     = 50
	defaultScrollWin   = 5
	maxScrollWin       = 20
	// resumeHint matches Hermes's _RESUME_HINT adapted to PiG.
	resumeHint = "To resume a session: `pig --session <session_id>` or `/resume` in interactive mode."
)

var (
	ansiEscapeRe     = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	dataURLRe        = regexp.MustCompile(`data:[^\s,"']*?;base64,[A-Za-z0-9+/=]+`)
	relativeBoundRe  = regexp.MustCompile(`(?i)^(\d+)\s*(h|d|w)$`)
	compactionPrefix = []string{"[CONTEXT COMPACTION", "[CONTEXT SUMMARY]:"}
	recallToolNames  = map[string]bool{
		"session_search": true,
		"tool_search":    true,
	}
)

// ToolCallSummary is a compact representation of a tool call stored inside an assistant message.
type ToolCallSummary struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name"`
	Arguments string `json:"arguments,omitempty"`
}

// SessionMessage is one normalized message parsed from a PiG JSONL session file.
type SessionMessage struct {
	Index      int               `json:"message_id"`
	EntryID    string            `json:"entry_id,omitempty"`
	Role       string            `json:"role"` // normalized: "user", "assistant", "tool"
	RawRole    string            `json:"-"`
	Content    string            `json:"content"`
	ToolName   string            `json:"tool_name,omitempty"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCallSummary `json:"tool_calls,omitempty"`
	Timestamp  time.Time         `json:"-"`
	TimeStr    string            `json:"timestamp,omitempty"`
	IsError    bool              `json:"is_error,omitempty"`
	IsCompacted bool             `json:"-"`
}

// ParsedSession holds header metadata and linearized messages from one PiG JSONL session file.
type ParsedSession struct {
	ID            string
	FilePath      string
	Title         string
	CWD           string
	Model         string
	ParentSession string
	StartedAt     time.Time
	UpdatedAt     time.Time
	Messages      []SessionMessage
}

// SearchParams configures a session_search execution.
type SearchParams struct {
	Query             string
	SessionID         string
	MessageID         string // optional string entry ID or 1-based numeric index
	AroundMessageID   int    // >0 triggers scroll mode around 1-based message index
	Window            int    // scroll half-window, default 5, clamped [1, 20]
	RoleFilter        string // CSV of user, assistant, tool; default "user,assistant"
	Limit             int    // default 10, max 50
	Offset            int    // default 0
	Sort              string // "", "newest", "oldest"
	After             string // ISO date/datetime or relative 7d/24h/2w
	Before            string // ISO date/datetime or relative 7d/24h/2w
	ExcludeSessionIDs []string
	Profile           string
	CurrentSessionID  string
}

// SearchHitResult is one match entry returned in discover or search-in-session mode.
type SearchHitResult struct {
	SessionID      string `json:"session_id"`
	Title          string `json:"title,omitempty"`
	Model          string `json:"model,omitempty"`
	When           string `json:"when,omitempty"`
	Link           string `json:"link"`
	MessageID      int    `json:"match_message_id"`
	EntryID        string `json:"entry_id,omitempty"`
	Role           string `json:"role"`
	ToolName       string `json:"tool_name,omitempty"`
	Timestamp      string `json:"timestamp,omitempty"`
	Snippet        string `json:"snippet"`
	Score          int    `json:"score,omitempty"`
	TotalMessages  int    `json:"total_messages"`
}

// BrowseSessionResult is one session summary returned in browse mode.
type BrowseSessionResult struct {
	SessionID    string `json:"session_id"`
	Title        string `json:"title,omitempty"`
	CWD          string `json:"cwd,omitempty"`
	Model        string `json:"model,omitempty"`
	When         string `json:"when,omitempty"`
	UpdatedAt    string `json:"updated_at,omitempty"`
	Link         string `json:"link"`
	MessageCount int    `json:"message_count"`
	Preview      string `json:"preview,omitempty"`
}

// ShapedMessage is a capped message view returned in read/scroll modes.
type ShapedMessage struct {
	MessageID  int               `json:"message_id"`
	EntryID    string            `json:"entry_id,omitempty"`
	Role       string            `json:"role"`
	ToolName   string            `json:"tool_name,omitempty"`
	Timestamp  string            `json:"timestamp,omitempty"`
	Content    string            `json:"content"`
	ToolCalls  []ToolCallSummary `json:"tool_calls,omitempty"`
	IsAnchor   bool              `json:"is_anchor,omitempty"`
}

// SearchResponse is the JSON payload returned by session_search.
type SearchResponse struct {
	Success      bool                  `json:"success"`
	Mode         string                `json:"mode"`
	Query        string                `json:"query,omitempty"`
	RelaxedOR    bool                  `json:"relaxed_or,omitempty"`
	SessionID    string                `json:"session_id,omitempty"`
	Title        string                `json:"title,omitempty"`
	When         string                `json:"when,omitempty"`
	Link         string                `json:"link,omitempty"`
	Count        int                   `json:"count"`
	Total        int                   `json:"total,omitempty"`
	Offset       int                   `json:"offset,omitempty"`
	Limit        int                   `json:"limit,omitempty"`
	HasMore      bool                  `json:"has_more,omitempty"`
	Results      []SearchHitResult     `json:"results,omitempty"`
	Sessions     []BrowseSessionResult `json:"sessions,omitempty"`
	Messages     []ShapedMessage       `json:"messages,omitempty"`
	AnchorID     int                   `json:"anchor_message_id,omitempty"`
	Hint         string                `json:"hint,omitempty"`
	Error        string                `json:"error,omitempty"`
}

// DefaultSessionDirs returns candidate PiG session root directories for the current
// environment and optional profile override.
func DefaultSessionDirs(activeSessionFile string) []string {
	var dirs []string
	seen := make(map[string]bool)
	add := func(d string) {
		d = strings.TrimSpace(d)
		if d == "" {
			return
		}
		clean := filepath.Clean(d)
		if !seen[clean] {
			seen[clean] = true
			dirs = append(dirs, clean)
		}
	}

	if activeSessionFile != "" {
		// Session files live in <sessionsRoot>/--<cwd>--/<file>.jsonl
		// Add both the immediate dir and its parent sessions root if it looks like --*--.
		dir := filepath.Dir(activeSessionFile)
		base := filepath.Base(dir)
		if strings.HasPrefix(base, "--") && strings.HasSuffix(base, "--") {
			add(filepath.Dir(dir))
		} else {
			add(dir)
		}
	}

	if v := strings.TrimSpace(os.Getenv("PIG_CODING_AGENT_SESSION_DIR")); v != "" {
		add(v)
	}
	if v := strings.TrimSpace(os.Getenv("PIG_CODING_AGENT_DIR")); v != "" {
		add(filepath.Join(v, "sessions"))
	}
	if v := strings.TrimSpace(os.Getenv("PIG_HOME")); v != "" {
		add(filepath.Join(v, "agent", "sessions"))
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		add(filepath.Join(home, ".pig", "agent", "sessions"))
	}
	return dirs
}

// ExecuteSearch runs session_search against all JSONL session files found under sessionDirs.
func ExecuteSearch(sessionDirs []string, params SearchParams) SearchResponse {
	// Support @session:<profile>/<id> or <profile>/<id> syntax in session_id.
	params.SessionID = strings.TrimSpace(params.SessionID)
	params.SessionID = strings.TrimPrefix(params.SessionID, "@session:")
	if idx := strings.Index(params.SessionID, "/"); idx > 0 {
		if params.Profile == "" {
			params.Profile = params.SessionID[:idx]
		}
		params.SessionID = params.SessionID[idx+1:]
	}

	limit := clampInt(params.Limit, defaultSearchLimit, 1, maxSearchLimit)
	offset := params.Offset
	if offset < 0 {
		offset = 0
	}
	window := clampInt(params.Window, defaultScrollWin, 1, maxScrollWin)
	roles := parseRoleFilter(params.RoleFilter)

	afterTS, err := parseTimeBound(params.After, time.Now().UTC())
	if err != nil {
		return SearchResponse{Success: false, Mode: "error", Error: fmt.Sprintf("invalid 'after' bound: %v", err)}
	}
	beforeTS, err := parseTimeBound(params.Before, time.Now().UTC())
	if err != nil {
		return SearchResponse{Success: false, Mode: "error", Error: fmt.Sprintf("invalid 'before' bound: %v", err)}
	}

	allSessions := loadSessionsFromDirs(sessionDirs)

	// Determine mode:
	// 1. session_id + (around_message_id > 0 or message_id != "") -> scroll
	// 2. session_id + query != "" -> search-in-session
	// 3. session_id alone -> read (with offset/limit pagination)
	// 4. query == "" -> browse recent sessions
	// 5. query != "" -> discover across sessions
	query := strings.TrimSpace(params.Query)

	if params.SessionID != "" {
		sess := findSessionByID(allSessions, params.SessionID)
		if sess == nil {
			return SearchResponse{
				Success:   false,
				Mode:      "read",
				SessionID: params.SessionID,
				Error:     fmt.Sprintf("session %q not found", params.SessionID),
			}
		}
		if params.AroundMessageID > 0 || strings.TrimSpace(params.MessageID) != "" {
			return runScroll(sess, params.AroundMessageID, strings.TrimSpace(params.MessageID), window, params.Profile)
		}
		if query != "" {
			return runSearchInSession(sess, query, roles, limit, offset, params.Profile)
		}
		return runReadSession(sess, roles, params.RoleFilter != "", limit, offset, params.Profile)
	}

	if query == "" {
		return runBrowse(allSessions, limit, offset, params.CurrentSessionID, params.ExcludeSessionIDs, params.Profile)
	}

	return runDiscover(allSessions, query, roles, limit, offset, params.Sort, afterTS, beforeTS, params.CurrentSessionID, params.ExcludeSessionIDs, params.Profile)
}

func runBrowse(sessions []*ParsedSession, limit, offset int, currentSessionID string, excludeIDs []string, profile string) SearchResponse {
	excluded := makeExcludeSet(currentSessionID, excludeIDs)
	filtered := make([]*ParsedSession, 0, len(sessions))
	for _, s := range sessions {
		if excluded[s.ID] {
			continue
		}
		filtered = append(filtered, s)
	}
	sortSessionsNewest(filtered)

	total := len(filtered)
	start, end := pageSlice(total, offset, limit)
	out := make([]BrowseSessionResult, 0, end-start)
	for _, s := range filtered[start:end] {
		out = append(out, BrowseSessionResult{
			SessionID:    s.ID,
			Title:        s.Title,
			CWD:          s.CWD,
			Model:        s.Model,
			When:         formatTime(s.StartedAt),
			UpdatedAt:    formatTime(s.UpdatedAt),
			Link:         sessionLink(s.ID, profile),
			MessageCount: len(s.Messages),
			Preview:      sessionPreview(s),
		})
	}
	return SearchResponse{
		Success:  true,
		Mode:     "browse",
		Count:    len(out),
		Total:    total,
		Offset:   offset,
		Limit:    limit,
		HasMore:  end < total,
		Sessions: out,
		Hint:     resumeHint,
	}
}

func runReadSession(sess *ParsedSession, roles map[string]bool, applyRoleFilter bool, limit, offset int, profile string) SearchResponse {
	var msgs []SessionMessage
	for _, m := range sess.Messages {
		if isSearchEcho(m) {
			continue
		}
		if applyRoleFilter && !roles[m.Role] {
			continue
		}
		msgs = append(msgs, m)
	}
	total := len(msgs)
	start, end := pageSlice(total, offset, limit)
	shaped := make([]ShapedMessage, 0, end-start)
	for _, m := range msgs[start:end] {
		shaped = append(shaped, shapeMessage(m, false, readMaxContent, toolArgsPreview))
	}
	return SearchResponse{
		Success:   true,
		Mode:      "read",
		SessionID: sess.ID,
		Title:     sess.Title,
		When:      formatTime(sess.StartedAt),
		Link:      sessionLink(sess.ID, profile),
		Count:     len(shaped),
		Total:     total,
		Offset:    offset,
		Limit:     limit,
		HasMore:   end < total,
		Messages:  shaped,
		Hint:      "Use offset/limit to paginate or pass around_message_id/message_id to scroll around a specific message. " + resumeHint,
	}
}

func runScroll(sess *ParsedSession, aroundID int, messageID string, window int, profile string) SearchResponse {
	if len(sess.Messages) == 0 {
		return SearchResponse{
			Success:   true,
			Mode:      "scroll",
			SessionID: sess.ID,
			Title:     sess.Title,
			When:      formatTime(sess.StartedAt),
			Link:      sessionLink(sess.ID, profile),
			Count:     0,
			Total:     0,
			Messages:  []ShapedMessage{},
		}
	}

	anchorPos := -1
	if messageID != "" {
		// Match by entry_id first, then numeric 1-based message_id.
		for i, m := range sess.Messages {
			if m.EntryID == messageID {
				anchorPos = i
				break
			}
		}
		if anchorPos < 0 {
			if n, err := strconv.Atoi(messageID); err == nil {
				aroundID = n
			}
		}
	}
	if anchorPos < 0 && aroundID > 0 {
		for i, m := range sess.Messages {
			if m.Index == aroundID {
				anchorPos = i
				break
			}
		}
		// Snap to nearest valid index if out of range.
		if anchorPos < 0 {
			if aroundID <= 1 {
				anchorPos = 0
			} else {
				anchorPos = len(sess.Messages) - 1
			}
		}
	}
	if anchorPos < 0 {
		anchorPos = 0
	}

	lo := anchorPos - window
	if lo < 0 {
		lo = 0
	}
	hi := anchorPos + window + 1
	if hi > len(sess.Messages) {
		hi = len(sess.Messages)
	}

	shaped := make([]ShapedMessage, 0, hi-lo)
	for i := lo; i < hi; i++ {
		m := sess.Messages[i]
		isAnchor := i == anchorPos
		maxLen := scrollTextMax
		argsCap := toolArgsPreview
		if isAnchor {
			maxLen = 0 // anchor returned uncapped (except tool prefix cap already applied)
			argsCap = anchorToolArgsMax
		} else if m.Role == "tool" {
			maxLen = scrollToolRowMax
		}
		shaped = append(shaped, shapeMessage(m, isAnchor, maxLen, argsCap))
	}

	return SearchResponse{
		Success:   true,
		Mode:      "scroll",
		SessionID: sess.ID,
		Title:     sess.Title,
		When:      formatTime(sess.StartedAt),
		Link:      sessionLink(sess.ID, profile),
		AnchorID:  sess.Messages[anchorPos].Index,
		Count:     len(shaped),
		Total:     len(sess.Messages),
		Messages:  shaped,
		Hint:      "Pass around_message_id with the first or last message_id above to scroll further.",
	}
}

func runSearchInSession(sess *ParsedSession, query string, roles map[string]bool, limit, offset int, profile string) SearchResponse {
	tokens := tokenizeQuery(query)
	if len(tokens) == 0 {
		return SearchResponse{Success: false, Mode: "search-in-session", SessionID: sess.ID, Error: "query contains no searchable terms"}
	}

	hits, relaxed := searchMessagesInSession(sess, tokens, roles, profile)
	total := len(hits)
	start, end := pageSlice(total, offset, limit)
	return SearchResponse{
		Success:   true,
		Mode:      "search-in-session",
		Query:     query,
		RelaxedOR: relaxed,
		SessionID: sess.ID,
		Title:     sess.Title,
		When:      formatTime(sess.StartedAt),
		Link:      sessionLink(sess.ID, profile),
		Count:     end - start,
		Total:     total,
		Offset:    offset,
		Limit:     limit,
		HasMore:   end < total,
		Results:   hits[start:end],
		Hint:      "To inspect surrounding context: session_search(session_id=..., around_message_id=match_message_id).",
	}
}

func runDiscover(sessions []*ParsedSession, query string, roles map[string]bool, limit, offset int, sortMode string, afterTS, beforeTS *time.Time, currentSessionID string, excludeIDs []string, profile string) SearchResponse {
	tokens := tokenizeQuery(query)
	if len(tokens) == 0 {
		return SearchResponse{Success: false, Mode: "discover", Error: "query contains no searchable terms"}
	}
	excluded := makeExcludeSet(currentSessionID, excludeIDs)

	candidates := make([]*ParsedSession, 0, len(sessions))
	for _, s := range sessions {
		if excluded[s.ID] {
			continue
		}
		if !inTimeWindow(s.StartedAt, afterTS, beforeTS) {
			continue
		}
		candidates = append(candidates, s)
	}

	// Pass 1: strict AND matching across all query tokens.
	hits := collectDiscoverHits(candidates, tokens, roles, true, profile)
	relaxed := false
	// Pass 2: OR-relaxed fallback when strict AND finds 0 hits and query has >=2 tokens.
	if len(hits) == 0 && len(tokens) >= 2 {
		hits = collectDiscoverHits(candidates, tokens, roles, false, profile)
		if len(hits) > 0 {
			relaxed = true
		}
	}

	// Sort hits: by requested temporal order or by (score desc, timestamp desc).
	sort.SliceStable(hits, func(i, j int) bool {
		switch strings.ToLower(strings.TrimSpace(sortMode)) {
		case "newest":
			if hits[i].When != hits[j].When {
				return hits[i].When > hits[j].When
			}
			return hits[i].Score > hits[j].Score
		case "oldest":
			if hits[i].When != hits[j].When {
				return hits[i].When < hits[j].When
			}
			return hits[i].Score > hits[j].Score
		default:
			if hits[i].Score != hits[j].Score {
				return hits[i].Score > hits[j].Score
			}
			return hits[i].When > hits[j].When
		}
	})

	total := len(hits)
	start, end := pageSlice(total, offset, limit)
	return SearchResponse{
		Success:   true,
		Mode:      "discover",
		Query:     query,
		RelaxedOR: relaxed,
		Count:     end - start,
		Total:     total,
		Offset:    offset,
		Limit:     limit,
		HasMore:   end < total,
		Results:   hits[start:end],
		Hint:      "To inspect a match in context: session_search(session_id=..., around_message_id=match_message_id). " + resumeHint,
	}
}

func collectDiscoverHits(sessions []*ParsedSession, tokens []string, roles map[string]bool, requireAll bool, profile string) []SearchHitResult {
	var results []SearchHitResult
	for _, s := range sessions {
		bestHit, ok := bestSessionHit(s, tokens, roles, requireAll, profile)
		if ok {
			results = append(results, bestHit)
		}
	}
	return results
}

func bestSessionHit(s *ParsedSession, tokens []string, roles map[string]bool, requireAll bool, profile string) (SearchHitResult, bool) {
	var best SearchHitResult
	found := false

	// Check session title match as well (mirrors _title_match_result).
	if s.Title != "" {
		if score, matched := scoreText(s.Title, tokens, requireAll); matched {
			best = SearchHitResult{
				SessionID:     s.ID,
				Title:         s.Title,
				Model:         s.Model,
				When:          formatTime(s.StartedAt),
				Link:          sessionLink(s.ID, profile),
				MessageID:     1,
				Role:          "title",
				Timestamp:     formatTime(s.StartedAt),
				Snippet:       buildHighlightedSnippet(s.Title, tokens),
				Score:         score + 2,
				TotalMessages: len(s.Messages),
			}
			found = true
		}
	}

	for _, m := range s.Messages {
		if !roles[m.Role] {
			continue
		}
		if isSearchEcho(m) {
			continue
		}
		searchable := searchableMessageText(m)
		score, matched := scoreText(searchable, tokens, requireAll)
		if !matched {
			continue
		}
		if !found || score > best.Score {
			best = SearchHitResult{
				SessionID:     s.ID,
				Title:         s.Title,
				Model:         s.Model,
				When:          formatTime(s.StartedAt),
				Link:          sessionLink(s.ID, profile),
				MessageID:     m.Index,
				EntryID:       m.EntryID,
				Role:          m.Role,
				ToolName:      m.ToolName,
				Timestamp:     m.TimeStr,
				Snippet:       buildHighlightedSnippet(searchable, tokens),
				Score:         score,
				TotalMessages: len(s.Messages),
			}
			found = true
		}
	}
	return best, found
}

func searchMessagesInSession(s *ParsedSession, tokens []string, roles map[string]bool, profile string) ([]SearchHitResult, bool) {
	scan := func(requireAll bool) []SearchHitResult {
		var hits []SearchHitResult
		for _, m := range s.Messages {
			if !roles[m.Role] {
				continue
			}
			if isSearchEcho(m) {
				continue
			}
			searchable := searchableMessageText(m)
			score, matched := scoreText(searchable, tokens, requireAll)
			if !matched {
				continue
			}
			hits = append(hits, SearchHitResult{
				SessionID:     s.ID,
				Title:         s.Title,
				Model:         s.Model,
				When:          formatTime(s.StartedAt),
				Link:          sessionLink(s.ID, profile),
				MessageID:     m.Index,
				EntryID:       m.EntryID,
				Role:          m.Role,
				ToolName:      m.ToolName,
				Timestamp:     m.TimeStr,
				Snippet:       buildHighlightedSnippet(searchable, tokens),
				Score:         score,
				TotalMessages: len(s.Messages),
			})
		}
		return hits
	}

	hits := scan(true)
	relaxed := false
	if len(hits) == 0 && len(tokens) >= 2 {
		hits = scan(false)
		if len(hits) > 0 {
			relaxed = true
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].MessageID < hits[j].MessageID
	})
	return hits, relaxed
}

// ── Query Tokenization, Scoring & Snippet Highlighting ───────────────────────

// tokenizeQuery splits a query into lowercase search terms, preserving quoted phrases
// and ignoring standalone boolean operators ("and", "or", "not").
func tokenizeQuery(query string) []string {
	var tokens []string
	seen := make(map[string]bool)
	add := func(tok string) {
		tok = strings.ToLower(strings.TrimSpace(tok))
		if tok == "" {
			return
		}
		if tok == "and" || tok == "or" || tok == "not" {
			return
		}
		if !seen[tok] {
			seen[tok] = true
			tokens = append(tokens, tok)
		}
	}

	var cur strings.Builder
	inQuote := false
	for _, r := range query {
		switch {
		case r == '"':
			if inQuote {
				add(cur.String())
				cur.Reset()
				inQuote = false
			} else {
				if cur.Len() > 0 {
					add(cur.String())
					cur.Reset()
				}
				inQuote = true
			}
		case !inQuote && (r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == ',' || r == ';' || r == '(' || r == ')'):
			if cur.Len() > 0 {
				add(cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		add(cur.String())
	}
	return tokens
}

// scoreText scores text against tokens. If requireAll is true (AND mode), every
// token must appear; otherwise (OR mode) at least one token must appear.
func scoreText(text string, tokens []string, requireAll bool) (int, bool) {
	if text == "" || len(tokens) == 0 {
		return 0, false
	}
	lower := strings.ToLower(text)
	distinct := 0
	occurrences := 0
	for _, tok := range tokens {
		cnt := strings.Count(lower, tok)
		if cnt == 0 {
			if requireAll {
				return 0, false
			}
			continue
		}
		distinct++
		if cnt > 10 {
			cnt = 10
		}
		occurrences += cnt
	}
	if distinct == 0 {
		return 0, false
	}
	return distinct*10 + occurrences, true
}

// buildHighlightedSnippet extracts a window around the earliest matching token
// and wraps matched occurrences in >>>...<<< (matching Hermes's snippet markers).
func buildHighlightedSnippet(text string, tokens []string) string {
	clean := cleanText(text)
	if clean == "" {
		return ""
	}
	lower := strings.ToLower(clean)

	firstIdx := -1
	for _, tok := range tokens {
		if idx := strings.Index(lower, tok); idx >= 0 {
			if firstIdx < 0 || idx < firstIdx {
				firstIdx = idx
			}
		}
	}

	var window string
	var prefix, suffix string
	if firstIdx < 0 {
		window = truncateRunes(clean, snippetBefore+snippetAfter)
	} else {
		start := firstIdx - snippetBefore
		if start < 0 {
			start = 0
		} else {
			start = alignUTF8Start(clean, start)
			prefix = "..."
		}
		end := firstIdx + snippetAfter
		if end >= len(clean) {
			end = len(clean)
		} else {
			end = alignUTF8End(clean, end)
			suffix = "..."
		}
		window = clean[start:end]
	}

	// Highlight each token case-insensitively inside window.
	highlighted := highlightTokens(window, tokens)
	return prefix + highlighted + suffix
}

func highlightTokens(window string, tokens []string) string {
	if len(tokens) == 0 || window == "" {
		return window
	}
	// Build an alternation regex of escaped tokens, longest first so phrases win.
	sorted := make([]string, len(tokens))
	copy(sorted, tokens)
	sort.Slice(sorted, func(i, j int) bool { return len(sorted[i]) > len(sorted[j]) })
	parts := make([]string, len(sorted))
	for i, tok := range sorted {
		parts[i] = regexp.QuoteMeta(tok)
	}
	re, err := regexp.Compile("(?i)(" + strings.Join(parts, "|") + ")")
	if err != nil {
		return window
	}
	return re.ReplaceAllString(window, ">>>$1<<<")
}

// ── JSONL Session Loading & Parsing ──────────────────────────────────────────

func loadSessionsFromDirs(dirs []string) []*ParsedSession {
	var sessions []*ParsedSession
	seenFiles := make(map[string]bool)
	for _, root := range dirs {
		if root == "" {
			continue
		}
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			continue
		}
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(strings.ToLower(d.Name()), ".jsonl") {
				return nil
			}
			clean := filepath.Clean(path)
			if seenFiles[clean] {
				return nil
			}
			seenFiles[clean] = true
			if s, err := ParseSessionFile(clean); err == nil && s != nil {
				sessions = append(sessions, s)
			}
			return nil
		})
	}
	return sessions
}

type rawJSONLEntry struct {
	Type          string          `json:"type"`
	ID            string          `json:"id"`
	ParentID      *string         `json:"parentId"`
	ParentSession string          `json:"parentSession"`
	Timestamp     string          `json:"timestamp"`
	CWD           string          `json:"cwd"`
	Name          string          `json:"name"`
	ModelID       string          `json:"modelId"`
	Provider      string          `json:"provider"`
	Summary       string          `json:"summary"`
	Message       json.RawMessage `json:"message"`
}

type rawAgentMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ToolName   string          `json:"toolName"`
	ToolCallID string          `json:"toolCallId"`
	Model      string          `json:"model"`
	IsError    bool            `json:"isError"`
	Command    string          `json:"command"`
	Output     string          `json:"output"`
	ExitCode   *int            `json:"exitCode"`
	Timestamp  any             `json:"timestamp"`
}

type rawContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Input     json.RawMessage `json:"input"`
}

// ParseSessionFile parses a PiG JSONL v3 session file into a ParsedSession.
func ParseSessionFile(path string) (*ParsedSession, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	stat, _ := f.Stat()
	fallbackID := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))

	sess := &ParsedSession{
		ID:       fallbackID,
		FilePath: path,
	}
	if stat != nil {
		sess.UpdatedAt = stat.ModTime().UTC()
	}

	scanner := bufio.NewScanner(f)
	// Allow large JSONL lines (e.g. tool outputs / base64 images up to 16MB).
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)

	msgIndex := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry rawJSONLEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		ts := parseEntryTimestamp(entry.Timestamp)
		if !ts.IsZero() {
			if sess.StartedAt.IsZero() || ts.Before(sess.StartedAt) {
				sess.StartedAt = ts
			}
			if ts.After(sess.UpdatedAt) {
				sess.UpdatedAt = ts
			}
		}

		switch entry.Type {
		case "session":
			if entry.ID != "" {
				sess.ID = entry.ID
			}
			if entry.CWD != "" {
				sess.CWD = entry.CWD
			}
			if entry.ParentSession != "" {
				sess.ParentSession = entry.ParentSession
			}
		case "session_info":
			if strings.TrimSpace(entry.Name) != "" {
				sess.Title = strings.TrimSpace(entry.Name)
			}
		case "model_change":
			if entry.ModelID != "" {
				if entry.Provider != "" {
					sess.Model = entry.Provider + "/" + entry.ModelID
				} else {
					sess.Model = entry.ModelID
				}
			}
		case "message":
			if len(entry.Message) == 0 {
				continue
			}
			var rawMsg rawAgentMessage
			if err := json.Unmarshal(entry.Message, &rawMsg); err != nil {
				continue
			}
			if rawMsg.Model != "" && sess.Model == "" {
				sess.Model = rawMsg.Model
			}
			msg, ok := normalizeMessage(entry.ID, ts, rawMsg)
			if !ok {
				continue
			}
			msgIndex++
			msg.Index = msgIndex
			sess.Messages = append(sess.Messages, msg)
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, bufio.ErrTooLong) {
		return nil, err
	}
	if sess.StartedAt.IsZero() && !sess.UpdatedAt.IsZero() {
		sess.StartedAt = sess.UpdatedAt
	}
	return sess, nil
}

func normalizeMessage(entryID string, ts time.Time, raw rawAgentMessage) (SessionMessage, bool) {
	role := normalizeRole(raw.Role)
	if role == "" {
		return SessionMessage{}, false
	}

	var content string
	var toolCalls []ToolCallSummary

	if raw.Role == "bashExecution" {
		var b strings.Builder
		if raw.Command != "" {
			b.WriteString("$ ")
			b.WriteString(raw.Command)
			if raw.Output != "" {
				b.WriteByte('\n')
			}
		}
		if raw.Output != "" {
			b.WriteString(raw.Output)
		}
		content = b.String()
	} else {
		content, toolCalls = extractContentAndToolCalls(raw.Content)
	}

	// Cap tool-output content cleanly at 8192 chars (Hermes FTS_TOOL_CONTENT_PREFIX_CHARS).
	if role == "tool" {
		content = truncateRunes(content, toolContentPrefixCap)
	}

	msg := SessionMessage{
		EntryID:    entryID,
		Role:       role,
		RawRole:    raw.Role,
		Content:    cleanText(content),
		ToolName:   raw.ToolName,
		ToolCallID: raw.ToolCallID,
		ToolCalls:  toolCalls,
		Timestamp:  ts,
		TimeStr:    formatTime(ts),
		IsError:    raw.IsError,
	}
	return msg, true
}

func normalizeRole(rawRole string) string {
	switch rawRole {
	case "user":
		return "user"
	case "assistant":
		return "assistant"
	case "toolResult", "tool", "bashExecution":
		return "tool"
	default:
		// Skip system, custom, compactionSummary, branchSummary from message search.
		return ""
	}
}

func extractContentAndToolCalls(raw json.RawMessage) (string, []ToolCallSummary) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return "", nil
	}
	// Case 1: plain JSON string
	if strings.HasPrefix(trimmed, `"`) {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return s, nil
		}
		return "", nil
	}
	// Case 2: array of content blocks
	if strings.HasPrefix(trimmed, "[") {
		var blocks []rawContentBlock
		if err := json.Unmarshal(raw, &blocks); err != nil {
			return "", nil
		}
		var textParts []string
		var calls []ToolCallSummary
		for _, blk := range blocks {
			switch blk.Type {
			case "text":
				if blk.Text != "" {
					textParts = append(textParts, blk.Text)
				}
			case "toolCall", "tool_use":
				argsRaw := blk.Arguments
				if len(argsRaw) == 0 {
					argsRaw = blk.Input
				}
				calls = append(calls, ToolCallSummary{
					ID:        blk.ID,
					Name:      blk.Name,
					Arguments: strings.TrimSpace(string(argsRaw)),
				})
			}
		}
		return strings.Join(textParts, "\n"), calls
	}
	return "", nil
}

// ── Echo Filtering & Helpers ─────────────────────────────────────────────────

// isSearchEcho returns true for messages that are pure session_search/tool_search
// echo artifacts:
//   - tool results produced by session_search / tool_search
//   - text-less assistant turns whose only tool calls are session_search / tool_search
func isSearchEcho(m SessionMessage) bool {
	if m.Role == "tool" && recallToolNames[m.ToolName] {
		return true
	}
	if m.Role == "assistant" && strings.TrimSpace(m.Content) == "" && len(m.ToolCalls) > 0 {
		for _, tc := range m.ToolCalls {
			if !recallToolNames[tc.Name] {
				return false
			}
		}
		return true
	}
	return false
}

func searchableMessageText(m SessionMessage) string {
	var parts []string
	if m.Content != "" {
		parts = append(parts, m.Content)
	}
	if m.ToolName != "" {
		parts = append(parts, m.ToolName)
	}
	for _, tc := range m.ToolCalls {
		if recallToolNames[tc.Name] {
			continue
		}
		if tc.Name != "" {
			parts = append(parts, tc.Name)
		}
		if tc.Arguments != "" {
			parts = append(parts, truncateRunes(tc.Arguments, anchorToolArgsMax))
		}
	}
	return strings.Join(parts, "\n")
}

func shapeMessage(m SessionMessage, isAnchor bool, maxContentLen, maxArgsLen int) ShapedMessage {
	content := m.Content
	if maxContentLen > 0 {
		content = truncateRunes(content, maxContentLen)
	}
	var slimCalls []ToolCallSummary
	if len(m.ToolCalls) > 0 {
		slimCalls = make([]ToolCallSummary, 0, len(m.ToolCalls))
		for _, tc := range m.ToolCalls {
			slimCalls = append(slimCalls, ToolCallSummary{
				ID:        tc.ID,
				Name:      tc.Name,
				Arguments: truncateRunes(tc.Arguments, maxArgsLen),
			})
		}
	}
	return ShapedMessage{
		MessageID: m.Index,
		EntryID:   m.EntryID,
		Role:      m.Role,
		ToolName:  m.ToolName,
		Timestamp: m.TimeStr,
		Content:   content,
		ToolCalls: slimCalls,
		IsAnchor:  isAnchor,
	}
}

func sessionPreview(s *ParsedSession) string {
	for _, m := range s.Messages {
		if m.Role == "user" && strings.TrimSpace(m.Content) != "" && !isCompactionSummary(m.Content) {
			return truncateRunes(m.Content, 160)
		}
	}
	return ""
}

func isCompactionSummary(content string) bool {
	trimmed := strings.TrimLeft(content, " \t\r\n")
	for _, p := range compactionPrefix {
		if strings.HasPrefix(trimmed, p) {
			return true
		}
	}
	return false
}

func parseRoleFilter(raw string) map[string]bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]bool{"user": true, "assistant": true}
	}
	roles := make(map[string]bool)
	for _, part := range strings.Split(raw, ",") {
		switch strings.ToLower(strings.TrimSpace(part)) {
		case "user":
			roles["user"] = true
		case "assistant":
			roles["assistant"] = true
		case "tool", "toolresult":
			roles["tool"] = true
		}
	}
	if len(roles) == 0 {
		return map[string]bool{"user": true, "assistant": true}
	}
	return roles
}

func parseTimeBound(raw string, now time.Time) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if m := relativeBoundRe.FindStringSubmatch(raw); len(m) == 3 {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return nil, err
		}
		var d time.Duration
		switch strings.ToLower(m[2]) {
		case "h":
			d = time.Duration(n) * time.Hour
		case "d":
			d = time.Duration(n) * 24 * time.Hour
		case "w":
			d = time.Duration(n) * 7 * 24 * time.Hour
		}
		t := now.Add(-d)
		return &t, nil
	}
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano, "2006-01-02"} {
		if t, err := time.Parse(layout, raw); err == nil {
			u := t.UTC()
			return &u, nil
		}
	}
	return nil, fmt.Errorf("expected ISO date/RFC3339 or relative duration like 7d/24h/2w, got %q", raw)
}

func parseEntryTimestamp(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t.UTC()
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC()
	}
	return time.Time{}
}

func inTimeWindow(t time.Time, afterTS, beforeTS *time.Time) bool {
	if afterTS == nil && beforeTS == nil {
		return true
	}
	if t.IsZero() {
		return false
	}
	if afterTS != nil && t.Before(*afterTS) {
		return false
	}
	if beforeTS != nil && !t.Before(*beforeTS) {
		return false
	}
	return true
}

func findSessionByID(sessions []*ParsedSession, id string) *ParsedSession {
	for _, s := range sessions {
		if s.ID == id {
			return s
		}
	}
	// Prefix match fallback (useful for short UUID prefixes).
	var match *ParsedSession
	for _, s := range sessions {
		if strings.HasPrefix(s.ID, id) {
			if match != nil {
				return nil // ambiguous prefix
			}
			match = s
		}
	}
	return match
}

func sortSessionsNewest(sessions []*ParsedSession) {
	sort.SliceStable(sessions, func(i, j int) bool {
		ti, tj := sessions[i].UpdatedAt, sessions[j].UpdatedAt
		if ti.IsZero() {
			ti = sessions[i].StartedAt
		}
		if tj.IsZero() {
			tj = sessions[j].StartedAt
		}
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return sessions[i].ID > sessions[j].ID
	})
}

func makeExcludeSet(currentID string, ids []string) map[string]bool {
	out := make(map[string]bool)
	if strings.TrimSpace(currentID) != "" {
		out[strings.TrimSpace(currentID)] = true
	}
	limit := 20
	for _, id := range ids {
		if limit <= 0 {
			break
		}
		trimmed := strings.TrimSpace(id)
		if trimmed != "" {
			out[trimmed] = true
			limit--
		}
	}
	return out
}

func sessionLink(sessionID, profile string) string {
	p := strings.TrimSpace(profile)
	if p != "" && p != "default" {
		return fmt.Sprintf("@session:%s/%s", p, sessionID)
	}
	return fmt.Sprintf("@session:%s", sessionID)
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func cleanText(s string) string {
	if s == "" {
		return ""
	}
	s = ansiEscapeRe.ReplaceAllString(s, "")
	s = dataURLRe.ReplaceAllString(s, "[inline data omitted]")
	return s
}

func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "…"
}

func alignUTF8Start(s string, idx int) int {
	for idx > 0 && idx < len(s) && (s[idx]&0xC0) == 0x80 {
		idx--
	}
	return idx
}

func alignUTF8End(s string, idx int) int {
	for idx < len(s) && (s[idx]&0xC0) == 0x80 {
		idx++
	}
	return idx
}

func clampInt(val, def, lo, hi int) int {
	if val <= 0 {
		return def
	}
	if val < lo {
		return lo
	}
	if val > hi {
		return hi
	}
	return val
}

func pageSlice(total, offset, limit int) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if offset >= total {
		return total, total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return offset, end
}
