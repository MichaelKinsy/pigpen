package sessions

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/config"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/memory"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/paths"
)

func writeJSONLFile(t *testing.T, dir, relName string, lines []string) string {
	t.Helper()
	full := filepath.Join(dir, relName)
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
	return full
}

func makeSampleSessionsDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	// Session 1: older session about postgres migration + bashExecution + large tool output
	hugeToolOut := strings.Repeat("X", 9500) + "TAIL_SHOULD_BE_CAPPED"
	writeJSONLFile(t, root, "--work-app--/2026-01-10_sess-alpha.jsonl", []string{
		`{"type":"session","version":3,"id":"sess-alpha","timestamp":"2026-01-10T10:00:00Z","cwd":"/work/app"}`,
		`{"type":"session_info","id":"i1","parentId":null,"timestamp":"2026-01-10T10:00:01Z","name":"Postgres migration"}`,
		`{"type":"model_change","id":"m1","parentId":"i1","timestamp":"2026-01-10T10:00:02Z","provider":"anthropic","modelId":"claude-sonnet-4"}`,
		`{"type":"message","id":"e1","parentId":"m1","timestamp":"2026-01-10T10:01:00Z","message":{"role":"user","content":[{"type":"text","text":"How do we migrate Postgres schema safely?"}]}}`,
		`{"type":"message","id":"e2","parentId":"e1","timestamp":"2026-01-10T10:02:00Z","message":{"role":"assistant","content":[{"type":"text","text":"Use transactional DDL and verify indexes before switching traffic."},{"type":"toolCall","id":"tc1","name":"terminal","arguments":{"command":"psql -c '\\dt'"}}]}}`,
		`{"type":"message","id":"e3","parentId":"e2","timestamp":"2026-01-10T10:03:00Z","message":{"role":"toolResult","toolName":"terminal","toolCallId":"tc1","content":[{"type":"text","text":` + mustJSON(t, hugeToolOut) + `}]}}`,
		`{"type":"message","id":"e4","parentId":"e3","timestamp":"2026-01-10T10:04:00Z","message":{"role":"bashExecution","command":"pg_dump --schema-only","output":"CREATE TABLE users (id int);","exitCode":0}}`,
		// Echo messages that MUST be excluded from search and read:
		`{"type":"message","id":"e5","parentId":"e4","timestamp":"2026-01-10T10:05:00Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"tc2","name":"session_search","arguments":{"query":"unique_echo_needle"}}]}}`,
		`{"type":"message","id":"e6","parentId":"e5","timestamp":"2026-01-10T10:05:01Z","message":{"role":"toolResult","toolName":"session_search","toolCallId":"tc2","content":[{"type":"text","text":"unique_echo_needle echoed result"}]}}`,
	})

	// Session 2: newer session about redis caching
	writeJSONLFile(t, root, "--work-app--/2026-02-15_sess-beta.jsonl", []string{
		`{"type":"session","version":3,"id":"sess-beta","timestamp":"2026-02-15T12:00:00Z","cwd":"/work/app"}`,
		`{"type":"session_info","id":"b1","parentId":null,"timestamp":"2026-02-15T12:00:01Z","name":"Redis cache tuning"}`,
		`{"type":"message","id":"b2","parentId":"b1","timestamp":"2026-02-15T12:01:00Z","message":{"role":"user","content":"Configure Redis LRU eviction policy for session cache"}}`,
		`{"type":"message","id":"b3","parentId":"b2","timestamp":"2026-02-15T12:02:00Z","message":{"role":"assistant","content":[{"type":"text","text":"Set maxmemory-policy allkeys-lru and monitor eviction metrics."}]}}`,
		`{"type":"message","id":"b4","parentId":"b3","timestamp":"2026-02-15T12:03:00Z","message":{"role":"user","content":"What about TTL jitter?"}}`,
		`{"type":"message","id":"b5","parentId":"b4","timestamp":"2026-02-15T12:04:00Z","message":{"role":"assistant","content":[{"type":"text","text":"Add 10% random TTL jitter to prevent cache stampedes."}]}}`,
	})

	// Session 3: newest session (can be used as currentSessionID to verify exclusion)
	writeJSONLFile(t, root, "--work-other--/2026-03-20_sess-gamma.jsonl", []string{
		`{"type":"session","version":3,"id":"sess-gamma","timestamp":"2026-03-20T15:00:00Z","cwd":"/work/other"}`,
		`{"type":"message","id":"g1","parentId":null,"timestamp":"2026-03-20T15:01:00Z","message":{"role":"user","content":[{"type":"text","text":"Investigating Kubernetes pod OOMKilled events"}]}}`,
		`{"type":"message","id":"g2","parentId":"g1","timestamp":"2026-03-20T15:02:00Z","message":{"role":"assistant","content":[{"type":"text","text":"Check container memory limits and cgroup OOM metrics."}]}}`,
	})

	return root
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func TestParseSessionFile_AndToolCap(t *testing.T) {
	root := makeSampleSessionsDir(t)
	sess, err := ParseSessionFile(filepath.Join(root, "--work-app--/2026-01-10_sess-alpha.jsonl"))
	if err != nil {
		t.Fatalf("ParseSessionFile error: %v", err)
	}
	if sess.ID != "sess-alpha" {
		t.Fatalf("got ID %q, want sess-alpha", sess.ID)
	}
	if sess.Title != "Postgres migration" {
		t.Fatalf("got Title %q, want Postgres migration", sess.Title)
	}
	if sess.Model != "anthropic/claude-sonnet-4" {
		t.Fatalf("got Model %q, want anthropic/claude-sonnet-4", sess.Model)
	}
	if len(sess.Messages) != 6 {
		t.Fatalf("got %d messages, want 6", len(sess.Messages))
	}

	// Verify 8192-char tool content prefix cap.
	toolMsg := sess.Messages[2]
	if toolMsg.Role != "tool" || toolMsg.ToolName != "terminal" {
		t.Fatalf("unexpected toolMsg: %+v", toolMsg)
	}
	if strings.Contains(toolMsg.Content, "TAIL_SHOULD_BE_CAPPED") {
		t.Fatalf("tool output was not capped at %d chars", toolContentPrefixCap)
	}
	if len([]rune(toolMsg.Content)) > toolContentPrefixCap+1 {
		t.Fatalf("tool output rune length %d exceeds cap %d+1", len([]rune(toolMsg.Content)), toolContentPrefixCap)
	}

	// Verify bashExecution normalization.
	bashMsg := sess.Messages[3]
	if bashMsg.Role != "tool" || !strings.Contains(bashMsg.Content, "pg_dump --schema-only") || !strings.Contains(bashMsg.Content, "CREATE TABLE users") {
		t.Fatalf("unexpected bashMsg: %+v", bashMsg)
	}
}

func TestExecuteSearch_BrowseNewestFirst(t *testing.T) {
	root := makeSampleSessionsDir(t)
	resp := ExecuteSearch([]string{root}, SearchParams{
		CurrentSessionID: "sess-gamma",
	})
	if !resp.Success || resp.Mode != "browse" {
		t.Fatalf("unexpected browse response: %+v", resp)
	}
	if resp.Count != 2 || len(resp.Sessions) != 2 {
		t.Fatalf("got %d sessions, want 2 (gamma excluded)", resp.Count)
	}
	if resp.Sessions[0].SessionID != "sess-beta" || resp.Sessions[1].SessionID != "sess-alpha" {
		t.Fatalf("expected newest-first [sess-beta, sess-alpha], got [%s, %s]", resp.Sessions[0].SessionID, resp.Sessions[1].SessionID)
	}
	if !strings.Contains(resp.Hint, "pig --session") {
		t.Fatalf("expected resume hint, got %q", resp.Hint)
	}
}

func TestExecuteSearch_DiscoverANDAndORFallback(t *testing.T) {
	root := makeSampleSessionsDir(t)

	// Strict AND match: both "redis" and "eviction" appear in sess-beta.
	resp := ExecuteSearch([]string{root}, SearchParams{
		Query: "redis eviction",
	})
	if !resp.Success || resp.Mode != "discover" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if resp.RelaxedOR {
		t.Fatalf("expected strict AND match, got RelaxedOR=true")
	}
	if resp.Count != 1 || resp.Results[0].SessionID != "sess-beta" {
		t.Fatalf("expected 1 hit in sess-beta, got %+v", resp.Results)
	}
	if !strings.Contains(resp.Results[0].Snippet, ">>>") || !strings.Contains(resp.Results[0].Snippet, "<<<") {
		t.Fatalf("expected highlighted snippet, got %q", resp.Results[0].Snippet)
	}

	// OR-relaxed fallback: "postgres" (in alpha) + "stampedes" (in beta) — no single message has both.
	respOR := ExecuteSearch([]string{root}, SearchParams{
		Query: "postgres stampedes",
	})
	if !respOR.Success || !respOR.RelaxedOR {
		t.Fatalf("expected OR-relaxed fallback, got %+v", respOR)
	}
	if respOR.Count != 2 {
		t.Fatalf("expected 2 OR-relaxed session hits, got %d", respOR.Count)
	}
}

func TestExecuteSearch_EchoFilteringAndRoleFilter(t *testing.T) {
	root := makeSampleSessionsDir(t)

	// Echo messages containing "unique_echo_needle" must be filtered out even when role_filter includes tool.
	resp := ExecuteSearch([]string{root}, SearchParams{
		Query:      "unique_echo_needle",
		RoleFilter: "user,assistant,tool",
	})
	if !resp.Success {
		t.Fatalf("unexpected error: %+v", resp)
	}
	if resp.Count != 0 {
		t.Fatalf("expected 0 hits for session_search echo, got %d: %+v", resp.Count, resp.Results)
	}

	// Default role_filter ("user,assistant") should NOT match tool-only content "CREATE TABLE users".
	respDefault := ExecuteSearch([]string{root}, SearchParams{
		Query: "CREATE TABLE users",
	})
	if respDefault.Count != 0 {
		t.Fatalf("expected 0 hits with default role_filter for tool output, got %d", respDefault.Count)
	}

	// Explicit role_filter="tool" SHOULD match "CREATE TABLE users".
	respTool := ExecuteSearch([]string{root}, SearchParams{
		Query:      "CREATE TABLE users",
		RoleFilter: "tool",
	})
	if respTool.Count != 1 || respTool.Results[0].SessionID != "sess-alpha" || respTool.Results[0].Role != "tool" {
		t.Fatalf("expected 1 tool hit in sess-alpha, got %+v", respTool.Results)
	}
}

func TestExecuteSearch_ReadPaginationAndScroll(t *testing.T) {
	root := makeSampleSessionsDir(t)

	// Read with offset/limit pagination on sess-beta (4 messages).
	page1 := ExecuteSearch([]string{root}, SearchParams{
		SessionID: "sess-beta",
		Limit:     2,
		Offset:    0,
	})
	if !page1.Success || page1.Mode != "read" || page1.Count != 2 || page1.Total != 4 || !page1.HasMore {
		t.Fatalf("unexpected page1: %+v", page1)
	}
	page2 := ExecuteSearch([]string{root}, SearchParams{
		SessionID: "sess-beta",
		Limit:     2,
		Offset:    2,
	})
	if !page2.Success || page2.Count != 2 || page2.HasMore {
		t.Fatalf("unexpected page2: %+v", page2)
	}
	if page1.Messages[0].MessageID == page2.Messages[0].MessageID {
		t.Fatalf("pagination did not advance")
	}

	// Scroll by around_message_id with window=1 around message 2 -> messages 1, 2, 3.
	scroll := ExecuteSearch([]string{root}, SearchParams{
		SessionID:       "sess-beta",
		AroundMessageID: 2,
		Window:          1,
	})
	if !scroll.Success || scroll.Mode != "scroll" || scroll.AnchorID != 2 || scroll.Count != 3 {
		t.Fatalf("unexpected scroll: %+v", scroll)
	}
	if !scroll.Messages[1].IsAnchor || scroll.Messages[1].MessageID != 2 {
		t.Fatalf("expected middle message to be anchor #2, got %+v", scroll.Messages[1])
	}

	// Scroll by entry_id via message_id.
	scrollByEntry := ExecuteSearch([]string{root}, SearchParams{
		SessionID: "@session:default/sess-beta",
		MessageID: "b4",
		Window:    1,
	})
	if !scrollByEntry.Success || scrollByEntry.Mode != "scroll" || scrollByEntry.AnchorID != 3 {
		t.Fatalf("unexpected scrollByEntry: %+v", scrollByEntry)
	}
}

func TestExecuteSearch_SearchInSessionAndTimeBounds(t *testing.T) {
	root := makeSampleSessionsDir(t)

	inSess := ExecuteSearch([]string{root}, SearchParams{
		SessionID: "sess-beta",
		Query:     "jitter",
	})
	if !inSess.Success || inSess.Mode != "search-in-session" || inSess.Count != 2 {
		t.Fatalf("expected 2 matches for jitter in sess-beta, got %+v", inSess)
	}

	// Time window filter: only sessions started on or after 2026-02-01.
	bounded := ExecuteSearch([]string{root}, SearchParams{
		Query: "postgres redis",
		After: "2026-02-01",
	})
	if !bounded.Success || bounded.Count != 1 || bounded.Results[0].SessionID != "sess-beta" {
		t.Fatalf("expected only sess-beta after 2026-02-01, got %+v", bounded)
	}

	// Invalid time bound returns structured error.
	badTime := ExecuteSearch([]string{root}, SearchParams{
		Query: "redis",
		After: "not-a-date",
	})
	if badTime.Success || badTime.Error == "" {
		t.Fatalf("expected error for invalid after bound, got %+v", badTime)
	}
}

func TestProfileCRUD(t *testing.T) {
	root := t.TempDir()
	p := paths.ProfilePaths{
		RootHome:      root,
		ProfileName:   "default",
		Home:          root,
		ProfilesDir:   filepath.Join(root, "profiles"),
		ActiveProfile: filepath.Join(root, "active_profile"),
	}

	// Initially only default exists and is active.
	list, err := ListProfiles(p)
	if err != nil {
		t.Fatalf("ListProfiles: %v", err)
	}
	if len(list) != 1 || list[0] != "default" {
		t.Fatalf("got %v, want [default]", list)
	}
	active, err := GetActiveProfile(p)
	if err != nil || active != "default" {
		t.Fatalf("GetActiveProfile = (%q, %v), want (default, nil)", active, err)
	}

	// Invalid and reserved names rejected.
	for _, bad := range []string{"", ".", "..", "../escape", "bad/name", "default", "profiles"} {
		if _, err := CreateProfile(p, bad); err == nil {
			t.Fatalf("expected CreateProfile(%q) to fail", bad)
		}
	}

	// Create profiles "work" and "Alpha" (normalized to "alpha").
	workDir, err := CreateProfile(p, "work")
	if err != nil {
		t.Fatalf("CreateProfile(work): %v", err)
	}
	if _, err := os.Stat(filepath.Join(workDir, "memories")); err != nil {
		t.Fatalf("expected memories subdir in %s: %v", workDir, err)
	}
	if _, err := CreateProfile(p, "Alpha"); err != nil {
		t.Fatalf("CreateProfile(Alpha): %v", err)
	}
	// Duplicate rejected.
	if _, err := CreateProfile(p, "work"); err == nil {
		t.Fatalf("expected duplicate CreateProfile(work) to fail")
	}

	list, err = ListProfiles(p)
	if err != nil {
		t.Fatalf("ListProfiles: %v", err)
	}
	want := []string{"default", "alpha", "work"}
	if strings.Join(list, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", list, want)
	}

	// SetActiveProfile -> work.
	if err := SetActiveProfile(p, "work"); err != nil {
		t.Fatalf("SetActiveProfile(work): %v", err)
	}
	active, err = GetActiveProfile(p)
	if err != nil || active != "work" {
		t.Fatalf("GetActiveProfile = (%q, %v), want (work, nil)", active, err)
	}

	// Setting nonexistent profile fails.
	if err := SetActiveProfile(p, "nonexistent"); err == nil {
		t.Fatalf("expected SetActiveProfile(nonexistent) to fail")
	}

	// Deleting default fails.
	if err := DeleteProfile(p, "default"); err == nil {
		t.Fatalf("expected DeleteProfile(default) to fail")
	}

	// Deleting the active profile resets active_profile back to default.
	if err := DeleteProfile(p, "work"); err != nil {
		t.Fatalf("DeleteProfile(work): %v", err)
	}
	active, err = GetActiveProfile(p)
	if err != nil || active != "default" {
		t.Fatalf("expected active profile reset to default after deleting work, got (%q, %v)", active, err)
	}

	// Setting default explicitly is idempotent.
	if err := SetActiveProfile(p, "default"); err != nil {
		t.Fatalf("SetActiveProfile(default): %v", err)
	}
}

type fakeMemoryProvider struct {
	mu       sync.Mutex
	sessions []string
}

func (f *fakeMemoryProvider) Name() string { return "fake" }
func (f *fakeMemoryProvider) Prefetch(context.Context, string, string) (string, error) {
	return "", nil
}
func (f *fakeMemoryProvider) OnPreCompress(_ context.Context, sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions = append(f.sessions, sessionID)
	return nil
}
func (f *fakeMemoryProvider) OnMemoryWrite(context.Context, memory.Target, string, string) error {
	return nil
}
func (f *fakeMemoryProvider) OnSessionEnd(context.Context, string) error { return nil }

func TestRegisterAndCompactionHook(t *testing.T) {
	ext := sdk.New("test-sessions")
	p := paths.ProfilePaths{
		RootHome:      t.TempDir(),
		ProfileName:   "default",
		Home:          t.TempDir(),
		ProfilesDir:   filepath.Join(t.TempDir(), "profiles"),
		ActiveProfile: filepath.Join(t.TempDir(), "active_profile"),
	}
	Register(ext, p, config.Default())

	// Exercise RegisterMemoryProvider concurrency & OnPreCompress invocation.
	prov := &fakeMemoryProvider{}
	RegisterMemoryProvider(prov)
	if _, err := handleBeforeCompact(sdk.Context{}, map[string]any{}); err != nil {
		t.Fatalf("handleBeforeCompact error: %v", err)
	}
	prov.mu.Lock()
	called := len(prov.sessions)
	prov.mu.Unlock()
	if called == 0 {
		t.Fatalf("expected OnPreCompress to be called on registered memory provider")
	}

	// Exercise extractTurnUsage.
	model, inTok, outTok, cost := extractTurnUsage(map[string]any{
		"message": map[string]any{
			"model": "claude-sonnet-4",
			"usage": map[string]any{
				"input":  float64(1200),
				"output": float64(300),
				"cost": map[string]any{
					"total": float64(0.0125),
				},
			},
		},
	})
	if model != "claude-sonnet-4" || inTok != 1200 || outTok != 300 || cost != 0.0125 {
		t.Fatalf("unexpected usage extraction: model=%q in=%d out=%d cost=%v", model, inTok, outTok, cost)
	}

	// Verify relative time bound parsing.
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	ts, err := parseTimeBound("7d", now)
	if err != nil || ts == nil || !ts.Equal(now.Add(-7*24*time.Hour)) {
		t.Fatalf("parseTimeBound(7d) = (%v, %v)", ts, err)
	}
}

func TestExecuteSearch_ConcurrentRaceSafety(t *testing.T) {
	root := makeSampleSessionsDir(t)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r1 := ExecuteSearch([]string{root}, SearchParams{Query: "redis"})
			if !r1.Success || r1.Count != 1 {
				t.Errorf("concurrent discover failed: %+v", r1)
			}
			r2 := ExecuteSearch([]string{root}, SearchParams{SessionID: "sess-beta", AroundMessageID: 2})
			if !r2.Success || r2.Mode != "scroll" {
				t.Errorf("concurrent scroll failed: %+v", r2)
			}
		}()
	}
	wg.Wait()
}

func TestHandleSessionSearch_ProfileQualifiedSessionRef(t *testing.T) {
	root := t.TempDir()
	profilesDir := filepath.Join(root, "profiles")
	workSessionsDir := filepath.Join(profilesDir, "work", "sessions")
	writeJSONLFile(t, workSessionsDir, "--work-app--/2026-04-01_sess-work-1.jsonl", []string{
		`{"type":"session","version":3,"id":"sess-work-1","timestamp":"2026-04-01T09:00:00Z","cwd":"/work/app"}`,
		`{"type":"message","id":"w1","parentId":null,"timestamp":"2026-04-01T09:01:00Z","message":{"role":"user","content":"Work profile secret message"}}`,
		`{"type":"message","id":"w2","parentId":"w1","timestamp":"2026-04-01T09:02:00Z","message":{"role":"assistant","content":[{"type":"text","text":"Acknowledged in work profile."}]}}`,
	})

	p := paths.ProfilePaths{
		RootHome:      root,
		ProfileName:   "default",
		Home:          root,
		ProfilesDir:   profilesDir,
		ActiveProfile: filepath.Join(root, "active_profile"),
	}

	// Pass @session:work/sess-work-1 without an explicit 'profile' param;
	// handleSessionSearch must parse the embedded profile before resolveSearchDirs.
	rawRes, err := handleSessionSearch(sdk.Context{}, p, map[string]any{
		"session_id": "@session:work/sess-work-1",
	})
	if err != nil {
		t.Fatalf("handleSessionSearch returned error: %v", err)
	}
	toolRes, ok := rawRes.(sdk.ToolResult)
	if !ok {
		t.Fatalf("expected sdk.ToolResult, got %T", rawRes)
	}
	if toolRes.IsError {
		t.Fatalf("expected success for @session:work/sess-work-1, got error: %s", toolRes.Content)
	}
	if !strings.Contains(toolRes.Content, "Work profile secret message") {
		t.Fatalf("expected work profile session content, got: %s", toolRes.Content)
	}
}

func TestStateDBDiscoveryBrowseReadAndSearch(t *testing.T) {
	sqliteBin, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 binary not available on PATH")
	}

	homeDir := t.TempDir()
	dbPath := filepath.Join(homeDir, "state.db")
	schemaSQL := `
CREATE TABLE sessions (
	id TEXT PRIMARY KEY,
	title TEXT,
	model TEXT,
	started_at REAL,
	ended_at REAL,
	parent_session_id TEXT
);
CREATE TABLE messages (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id TEXT NOT NULL,
	role TEXT NOT NULL,
	content TEXT,
	tool_call_id TEXT,
	tool_name TEXT,
	tool_calls TEXT,
	timestamp REAL
);
INSERT INTO sessions (id, title, model, started_at, ended_at, parent_session_id)
VALUES ('hermes-sqlite-1', 'Upstream Postgres Migration', 'nous/hermes-3', 1770000000.0, 1770000600.0, NULL);
INSERT INTO messages (session_id, role, content, tool_call_id, tool_name, tool_calls, timestamp) VALUES
('hermes-sqlite-1', 'user', 'Help me migrate our upstream postgres pooler config.', NULL, NULL, NULL, 1770000010.0),
('hermes-sqlite-1', 'assistant', 'I will inspect pgbouncer.ini and update pool_mode to transaction.', NULL, NULL, '[{"id":"tc1","function":{"name":"read_file","arguments":"{\"path\":\"pgbouncer.ini\"}"}}]', 1770000020.0),
('hermes-sqlite-1', 'tool', 'pool_mode = session', 'tc1', 'read_file', NULL, 1770000030.0);
`
	cmd := exec.Command(sqliteBin, dbPath)
	cmd.Stdin = strings.NewReader(schemaSQL)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to seed temp state.db: %v\n%s", err, string(out))
	}

	// Browse includes state.db session alongside JSONL sessions.
	jsonlRoot := makeSampleSessionsDir(t)
	browseResp := ExecuteSearch([]string{jsonlRoot}, SearchParams{
		StateDBPaths: []string{dbPath},
		Limit:        10,
	})
	if !browseResp.Success {
		t.Fatalf("browse failed: %+v", browseResp)
	}
	foundBrowse := false
	for _, s := range browseResp.Sessions {
		if s.SessionID == "hermes-sqlite-1" {
			foundBrowse = true
			if s.Title != "Upstream Postgres Migration" || s.Model != "nous/hermes-3" {
				t.Fatalf("unexpected state.db browse entry: %+v", s)
			}
		}
	}
	if !foundBrowse {
		t.Fatalf("expected hermes-sqlite-1 in browse results, got: %+v", browseResp.Sessions)
	}

	// Discover across JSONL + state.db finds upstream message.
	discoverResp := ExecuteSearch([]string{jsonlRoot}, SearchParams{
		Query:        "pgbouncer pool_mode",
		StateDBPaths: []string{dbPath},
	})
	if !discoverResp.Success || discoverResp.Count != 1 || discoverResp.Results[0].SessionID != "hermes-sqlite-1" {
		t.Fatalf("expected discover hit in hermes-sqlite-1, got: %+v", discoverResp)
	}

	// Read and search-in-session work on state.db session.
	readResp := ExecuteSearch([]string{jsonlRoot}, SearchParams{
		SessionID:    "hermes-sqlite-1",
		StateDBPaths: []string{dbPath},
	})
	if !readResp.Success || readResp.Total != 3 {
		t.Fatalf("expected 3 messages in state.db read, got: %+v", readResp)
	}
	inSessResp := ExecuteSearch([]string{jsonlRoot}, SearchParams{
		SessionID:    "hermes-sqlite-1",
		Query:        "transaction",
		StateDBPaths: []string{dbPath},
	})
	if !inSessResp.Success || inSessResp.Count != 1 {
		t.Fatalf("expected 1 in-session hit, got: %+v", inSessResp)
	}

	// Missing or empty state.db degrades cleanly without error.
	missingResp := ExecuteSearch([]string{jsonlRoot}, SearchParams{
		StateDBPaths: []string{filepath.Join(homeDir, "does-not-exist.db")},
	})
	if !missingResp.Success || missingResp.Count == 0 {
		t.Fatalf("expected graceful fallback on missing state.db, got: %+v", missingResp)
	}
}

func TestHandleBeforeCompact_HermesSummaryAndToolPruning(t *testing.T) {
	prov := &fakeMemoryProvider{}
	RegisterMemoryProvider(prov)

	// Empty / absent messagesToSummarize delegates cleanly to PiG native compactor (returns nil, nil).
	resNil, err := handleBeforeCompact(sdk.Context{}, map[string]any{})
	if err != nil || resNil != nil {
		t.Fatalf("expected (nil, nil) fallback when messagesToSummarize is absent, got (%v, %v)", resNil, err)
	}

	// Oversized tool output (>2000 chars) is pruned with `[pruned N chars]` and structured sections are emitted.
	hugeOutput := strings.Repeat("A", 2500)
	resAny, err := handleBeforeCompact(sdk.Context{}, map[string]any{
		"firstKeptEntryId": "entry-42",
		"tokensBefore":     12345,
		"messagesToSummarize": []any{
			map[string]any{
				"role":    "user",
				"content": "Refactor the auth middleware to support rotating keys.",
			},
			map[string]any{
				"role": "assistant",
				"content": []any{
					map[string]any{"type": "text", "text": "Decided to use atomic symlink writes for key rotation."},
					map[string]any{"type": "toolCall", "id": "c1", "name": "write_file", "arguments": map[string]any{"path": "auth/middleware.go"}},
				},
			},
			map[string]any{
				"role":     "toolResult",
				"toolName": "terminal",
				"content":  hugeOutput,
			},
		},
	})
	if err != nil {
		t.Fatalf("handleBeforeCompact error: %v", err)
	}
	resMap, ok := resAny.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", resAny)
	}
	comp, ok := resMap["compaction"].(map[string]any)
	if !ok {
		t.Fatalf("expected compaction map, got %+v", resMap)
	}
	if comp["firstKeptEntryId"] != "entry-42" || comp["tokensBefore"] != 12345 {
		t.Fatalf("unexpected compaction metadata: %+v", comp)
	}
	summary, _ := comp["summary"].(string)
	for _, want := range []string{
		"## Goal",
		"Refactor the auth middleware",
		"## Progress",
		"## Files Modified",
		"`auth/middleware.go`",
		"## Key Decisions",
		"Decided to use atomic symlink writes",
		"## Active Context",
		"[pruned 500 chars]",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("expected compaction summary to contain %q, got:\n%s", want, summary)
		}
	}
}
