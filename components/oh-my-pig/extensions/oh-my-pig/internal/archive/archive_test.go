package archive

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/eval"
)

// upstream: packages/coding-agent/test/archive-prelude.test.ts

type testSessionOpts struct {
	title      string
	prompt     string
	prompts    []string
	answered   bool
	reply      string
	replies    []string
	toolOutput string
	recaps     []string
	compaction string
	parent     string
	stopReason string
	rawFile    string
}

func writeTestSession(t *testing.T, sessionsRoot, cwd, label, at string, opts testSessionOpts) (id string, file string) {
	t.Helper()
	ts, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		t.Fatalf("parse time %q: %v", at, err)
	}
	id = fmt.Sprintf("%s-0000-0000-0000-000000000001", label)
	bucketName := "--" + strings.NewReplacer("/", "-", `\`, "-", ":", "-").Replace(strings.TrimLeft(cwd, `/\`)) + "--"
	dir := filepath.Join(sessionsRoot, bucketName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}

	var entries []map[string]any
	header := map[string]any{
		"type":      "session",
		"version":   3,
		"id":        id,
		"timestamp": at,
		"cwd":       cwd,
	}
	if opts.title != "" {
		header["title"] = opts.title
	}
	if opts.parent != "" {
		header["parentSession"] = opts.parent
	}
	entries = append(entries, header)

	allPrompts := append([]string(nil), opts.prompts...)
	if opts.prompt != "" {
		allPrompts = append([]string{opts.prompt}, allPrompts...)
	}
	curTime := ts
	for idx, p := range allPrompts {
		entries = append(entries, map[string]any{
			"type":      "message",
			"id":        fmt.Sprintf("u%d", idx+1),
			"parentId":  nil,
			"timestamp": curTime.UTC().Format(iso8601MillisFormat),
			"message": map[string]any{
				"role":      "user",
				"content":   p,
				"timestamp": curTime.UnixMilli(),
			},
		})
		curTime = curTime.Add(time.Second)
	}

	allReplies := append([]string(nil), opts.replies...)
	if opts.reply != "" {
		allReplies = append([]string{opts.reply}, allReplies...)
	} else if opts.answered && len(allReplies) == 0 {
		allReplies = append(allReplies, "stock reply")
	}
	for idx, r := range allReplies {
		msg := map[string]any{
			"role": "assistant",
			"content": []map[string]any{
				{"type": "text", "text": r},
			},
			"timestamp": curTime.UnixMilli(),
		}
		if opts.stopReason != "" {
			msg["stopReason"] = opts.stopReason
		}
		entries = append(entries, map[string]any{
			"type":      "message",
			"id":        fmt.Sprintf("a%d", idx+1),
			"parentId":  "u1",
			"timestamp": curTime.UTC().Format(iso8601MillisFormat),
			"message":   msg,
		})
		curTime = curTime.Add(time.Second)
	}

	if opts.toolOutput != "" {
		entries = append(entries, map[string]any{
			"type":      "message",
			"id":        "t1",
			"parentId":  "a1",
			"timestamp": curTime.UTC().Format(iso8601MillisFormat),
			"message": map[string]any{
				"role":       "toolResult",
				"toolCallId": "c1",
				"toolName":   "bash",
				"content":    []map[string]any{{"type": "text", "text": opts.toolOutput}},
				"isError":    false,
				"timestamp":  curTime.UnixMilli(),
			},
		})
		curTime = curTime.Add(time.Second)
	}

	if opts.compaction != "" {
		entries = append(entries, map[string]any{
			"type":         "compaction",
			"id":           "c1",
			"parentId":     "a1",
			"timestamp":    curTime.UTC().Format(iso8601MillisFormat),
			"shortSummary": opts.compaction,
			"summary":      "longer compaction body",
		})
		curTime = curTime.Add(time.Second)
	}

	for idx, rec := range opts.recaps {
		entries = append(entries, map[string]any{
			"type":       "custom_message",
			"id":         fmt.Sprintf("r%d", idx+1),
			"parentId":   "a1",
			"timestamp":  curTime.UTC().Format(iso8601MillisFormat),
			"customType": "idle_recap",
			"content":    rec,
		})
		curTime = curTime.Add(time.Second)
	}

	var lines []string
	for _, e := range entries {
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("marshal entry: %v", err)
		}
		lines = append(lines, string(b))
	}
	if opts.rawFile != "" {
		file = opts.rawFile
	} else {
		file = filepath.Join(dir, fmt.Sprintf("%d_%s.jsonl", ts.UnixMilli(), id))
	}
	if err := os.WriteFile(file, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write %s: %v", file, err)
	}
	if err := os.Chtimes(file, ts, ts); err != nil {
		t.Fatalf("chtimes %s: %v", file, err)
	}
	return id, file
}

func setupFixture(t *testing.T) (sessionsRoot, app, lib, oldApp, newApp, libSession string) {
	t.Helper()
	ClearCachesForTests()
	root := t.TempDir()
	agentDir := filepath.Join(root, "agent")
	sessionsRoot = filepath.Join(agentDir, "sessions")
	app = filepath.Join(root, "app")
	lib = filepath.Join(root, "lib")
	idle := filepath.Join(root, "idle")

	origOverride := SessionDirsOverride
	SessionDirsOverride = func() []string { return []string{sessionsRoot} }
	t.Cleanup(func() {
		SessionDirsOverride = origOverride
		ClearCachesForTests()
	})

	oldApp, _ = writeTestSession(t, sessionsRoot, app, "app-old", "2024-01-01T00:00:00.000Z", testSessionOpts{
		prompt:   "old work",
		answered: true,
	})
	newApp, _ = writeTestSession(t, sessionsRoot, app, "app-new", "2024-03-01T00:00:00.000Z", testSessionOpts{
		prompt:   "fix login",
		answered: true,
		recaps:   []string{"first recap", "second recap"},
	})
	writeTestSession(t, sessionsRoot, app, "app-empty", "2024-04-01T00:00:00.000Z", testSessionOpts{})
	libSession, _ = writeTestSession(t, sessionsRoot, lib, "lib", "2024-02-01T00:00:00.000Z", testSessionOpts{
		prompt:   "lib refactor",
		answered: true,
		recaps:   []string{"lib recap"},
	})
	writeTestSession(t, sessionsRoot, idle, "idle", "2024-05-01T00:00:00.000Z", testSessionOpts{})

	return sessionsRoot, app, lib, oldApp, newApp, libSession
}

func TestArchiveProjectsAndSessions(t *testing.T) {
	_, app, lib, oldApp, newApp, libSession := setupFixture(t)
	ctx := context.Background()

	// Projects skips projects with only empty sessions, counts empty sessions inside active projects.
	projView, err := ArchiveProjects(ctx, 20)
	if err != nil {
		t.Fatalf("ArchiveProjects: %v", err)
	}
	projects, ok := projView.Records.([]ArchiveProject)
	if !ok {
		t.Fatalf("expected []ArchiveProject, got %T", projView.Records)
	}
	if len(projects) != 2 {
		t.Fatalf("expected 2 projects, got %d: %+v", len(projects), projects)
	}
	if projects[0].Path != app || projects[0].Sessions != 3 || projects[0].Latest.ID != newApp {
		t.Fatalf("unexpected project[0]: %+v", projects[0])
	}
	if projects[0].Latest.Recap == nil || *projects[0].Latest.Recap != "second recap" {
		t.Fatalf("expected latest recap 'second recap', got %v", projects[0].Latest.Recap)
	}
	if projects[1].Path != lib || projects[1].Sessions != 1 || projects[1].Latest.ID != libSession {
		t.Fatalf("unexpected project[1]: %+v", projects[1])
	}

	// Sessions scoped to current project.
	localView, err := ArchiveSessions(ctx, app, 20)
	if err != nil {
		t.Fatalf("ArchiveSessions local: %v", err)
	}
	local := localView.Records.([]ArchiveSession)
	if len(local) != 2 || local[0].ID != newApp || local[1].ID != oldApp {
		t.Fatalf("unexpected local sessions: %+v", local)
	}
	if local[0].Recap == nil || *local[0].Recap != "second recap" {
		t.Fatalf("expected local[0].Recap='second recap', got %v", local[0].Recap)
	}
	if local[1].Recap != nil {
		t.Fatalf("expected local[1].Recap=nil, got %v", *local[1].Recap)
	}

	// Verify explicit JSON null serialization for missing recap so Python `row["recap"]` works.
	rawJSON, err := json.Marshal(local[1])
	if err != nil {
		t.Fatalf("marshal local[1]: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(rawJSON, &m); err != nil {
		t.Fatalf("unmarshal local[1]: %v", err)
	}
	for _, key := range []string{"id", "file", "project", "title", "created", "modified", "messages", "status", "recap"} {
		if _, exists := m[key]; !exists {
			t.Fatalf("missing required JSON key %q in %s", key, string(rawJSON))
		}
	}
	if m["recap"] != nil {
		t.Fatalf("expected JSON null for recap, got %#v", m["recap"])
	}

	// Sessions across all projects with limit=2.
	globalView, err := ArchiveSessions(ctx, "", 2)
	if err != nil {
		t.Fatalf("ArchiveSessions global: %v", err)
	}
	global := globalView.Records.([]ArchiveSession)
	if len(global) != 2 || global[0].ID != newApp || global[1].ID != libSession {
		t.Fatalf("unexpected global sessions: %+v", global)
	}

	// Relative project resolution: "../lib" from app -> lib.
	otherCwd := resolveProject(true, "../lib", app)
	otherView, err := ArchiveSessions(ctx, otherCwd, 20)
	if err != nil {
		t.Fatalf("ArchiveSessions ../lib: %v", err)
	}
	other := otherView.Records.([]ArchiveSession)
	if len(other) != 1 || other[0].ID != libSession {
		t.Fatalf("unexpected ../lib sessions: %+v", other)
	}
}

func TestArchiveSessionByPrefixAndPath(t *testing.T) {
	sessionsRoot, app, _, _, newApp, _ := setupFixture(t)
	ctx := context.Background()

	// Unique prefix resolves full detail with recaps and prompts oldest-first.
	view, err := ArchiveSessionDetailByID(ctx, "app-new", 20)
	if err != nil {
		t.Fatalf("ArchiveSessionDetailByID(app-new): %v", err)
	}
	detail := view.Records.(ArchiveSessionDetail)
	if detail.ID != newApp {
		t.Fatalf("expected id %s, got %s", newApp, detail.ID)
	}
	if len(detail.Recaps) != 2 || detail.Recaps[0].Text != "first recap" || detail.Recaps[1].Text != "second recap" {
		t.Fatalf("unexpected recaps: %+v", detail.Recaps)
	}
	if len(detail.Prompts) != 1 || detail.Prompts[0].Text != "fix login" {
		t.Fatalf("unexpected prompts: %+v", detail.Prompts)
	}

	// Verify explicit null parent key in JSON.
	rawJSON, _ := json.Marshal(detail)
	var m map[string]any
	_ = json.Unmarshal(rawJSON, &m)
	if _, exists := m["parent"]; !exists || m["parent"] != nil {
		t.Fatalf("expected parent: null in %s", string(rawJSON))
	}

	// Ambiguous prefix returns exact upstream error message.
	_, err = ArchiveSessionDetailByID(ctx, "app-", 20)
	if err == nil || !strings.Contains(err.Error(), "matches 3 sessions") {
		t.Fatalf("expected ambiguous match error for 'app-', got: %v", err)
	}

	// Non-matching prefix returns exact upstream error message.
	_, err = ArchiveSessionDetailByID(ctx, "does-not-exist", 20)
	if err == nil || !strings.Contains(err.Error(), `No session id starts with "does-not-exist".`) {
		t.Fatalf("expected no match error, got: %v", err)
	}

	// Direct `.jsonl` path resolution.
	forkedID, forkedFile := writeTestSession(t, sessionsRoot, app, "forked", "2024-03-02T00:00:00.000Z", testSessionOpts{
		prompt:   "continue forked work",
		answered: true,
		parent:   detail.File,
	})
	directView, err := ArchiveSessionDetailByID(ctx, forkedFile, 20)
	if err != nil {
		t.Fatalf("ArchiveSessionDetailByID(direct path): %v", err)
	}
	direct := directView.Records.(ArchiveSessionDetail)
	if direct.ID != forkedID || direct.Parent == nil || *direct.Parent != detail.File {
		t.Fatalf("unexpected direct detail: %+v", direct)
	}

	// Unreadable non-session file returns exact upstream error message.
	badFile := filepath.Join(t.TempDir(), "bad.jsonl")
	_ = os.WriteFile(badFile, []byte("not json\n"), 0o600)
	_, err = ArchiveSessionDetailByID(ctx, badFile, 20)
	if err == nil || !strings.Contains(err.Error(), "Not a readable session file:") {
		t.Fatalf("expected unreadable session file error, got: %v", err)
	}
}

func TestArchiveSearchAndWordQuery(t *testing.T) {
	sessionsRoot, app, lib, _, newApp, _ := setupFixture(t)
	ctx := context.Background()

	// Add titled sessions and a benchmark session with tool output.
	titledApp, _ := writeTestSession(t, sessionsRoot, app, "titled-app", "2024-01-15T00:00:00.000Z", testSessionOpts{
		title:    "Archive retention policy",
		prompt:   "discuss retention",
		answered: true,
	})
	titledLib, _ := writeTestSession(t, sessionsRoot, lib, "titled-lib", "2024-02-10T00:00:00.000Z", testSessionOpts{
		title:    "archive internals",
		prompt:   "check internals",
		answered: true,
	})
	bench, _ := writeTestSession(t, sessionsRoot, app, "app-bench", "2024-02-15T00:00:00.000Z", testSessionOpts{
		prompt:     "run the edit benchmark",
		reply:      "The typescript-edit-benchmark finished: 41 of 50 passed.",
		toolOutput: "secret tool payload",
	})

	// Scoped to app -> finds titledApp only.
	v1, err := ArchiveSearch(ctx, "archive", app, 20)
	if err != nil {
		t.Fatalf("search archive app: %v", err)
	}
	hits1 := v1.Records.([]ArchiveSearchHit)
	if len(hits1) != 1 || hits1[0].ID != titledApp {
		t.Fatalf("expected [titledApp], got %+v", hits1)
	}
	if len(hits1[0].Matches) != 1 || hits1[0].Matches[0].Source != "title" || hits1[0].Matches[0].At != nil {
		t.Fatalf("unexpected title match: %+v", hits1[0].Matches)
	}

	// Global search -> titledLib (2024-02-10) before titledApp (2024-01-15).
	v2, err := ArchiveSearch(ctx, "archive", "", 20)
	if err != nil {
		t.Fatalf("search archive global: %v", err)
	}
	hits2 := v2.Records.([]ArchiveSearchHit)
	if len(hits2) != 2 || hits2[0].ID != titledLib || hits2[1].ID != titledApp {
		t.Fatalf("unexpected global archive hits: %+v", hits2)
	}

	// Message search: assistant (newer) before user; hyphenated punctuation matches space.
	v3, err := ArchiveSearch(ctx, "edit benchmark", app, 20)
	if err != nil {
		t.Fatalf("search edit benchmark: %v", err)
	}
	hits3 := v3.Records.([]ArchiveSearchHit)
	if len(hits3) != 1 || hits3[0].ID != bench {
		t.Fatalf("expected [bench], got %+v", hits3)
	}
	if len(hits3[0].Matches) != 2 || hits3[0].Matches[0].Source != "assistant" || hits3[0].Matches[1].Source != "user" {
		t.Fatalf("expected assistant then user match, got %+v", hits3[0].Matches)
	}

	// Recap search.
	v4, err := ArchiveSearch(ctx, "second recap", app, 20)
	if err != nil {
		t.Fatalf("search second recap: %v", err)
	}
	hits4 := v4.Records.([]ArchiveSearchHit)
	if len(hits4) != 1 || hits4[0].ID != newApp || len(hits4[0].Matches) != 1 || hits4[0].Matches[0].Source != "recap" {
		t.Fatalf("unexpected recap search hit: %+v", hits4)
	}

	// Tool output is NOT indexed.
	v5, err := ArchiveSearch(ctx, "secret payload", app, 20)
	if err != nil {
		t.Fatalf("search secret payload: %v", err)
	}
	if hits5 := v5.Records.([]ArchiveSearchHit); len(hits5) != 0 {
		t.Fatalf("expected tool output not indexed, got %+v", hits5)
	}

	// Words spread across prompt ("fix login") and recap ("second recap") do NOT match.
	v6, err := ArchiveSearch(ctx, "login second", app, 20)
	if err != nil {
		t.Fatalf("search login second: %v", err)
	}
	if hits6 := v6.Records.([]ArchiveSearchHit); len(hits6) != 0 {
		t.Fatalf("expected cross-source query not to match, got %+v", hits6)
	}
}

func TestWordQuerySemantics(t *testing.T) {
	cases := []struct {
		query string
		text  string
		want  bool
	}{
		{"arch", "Archive retention", true},
		{"chiv", "Archive retention", false},
		{"git-commit", "git commit message", true},
		{"git-commit", "Git commits", true},
		{"git-commit", "commit to git", false},
		{"a/b", "alpha and beta", false},
		{"a/b", "a-b", true},
		{"cafe", "Café au lait", true},
		{"resume", "re\u0301sume\u0301 builder", true},
		{"rate limit", "limit the rate of retries", true},
	}
	for _, tc := range cases {
		q := ParseWordQuery(tc.query)
		if q == nil {
			t.Fatalf("ParseWordQuery(%q) returned nil", tc.query)
		}
		if got := q.Test(tc.text); got != tc.want {
			t.Errorf("WordQuery(%q).Test(%q) = %v, want %v", tc.query, tc.text, got, tc.want)
		}
	}
	if q := ParseWordQuery("   ---   "); q != nil {
		t.Fatalf("expected nil WordQuery for punctuation-only query, got %+v", q)
	}

	// Snippet context trimming around hit.
	longText := strings.Repeat("a", 150) + " needleWord " + strings.Repeat("b", 150)
	q := ParseWordQuery("needle")
	snip := makeSnippet(longText, q)
	if !strings.HasPrefix(snip, "…") || !strings.HasSuffix(snip, "…") || !strings.Contains(snip, "needleWord") {
		t.Fatalf("unexpected snippet: %q", snip)
	}
}

func TestArchivePromptsAndRecaps(t *testing.T) {
	sessionsRoot, app, lib, _, newApp, libSession := setupFixture(t)
	ctx := context.Background()

	// Add optional history.jsonl with prompts (including one without a session).
	histPath := filepath.Join(filepath.Dir(sessionsRoot), "history.jsonl")
	histLines := []string{
		fmt.Sprintf(`{"prompt":"deploy the archive","cwd":%q,"sessionId":%q,"at":"2024-03-05T00:00:00.000Z"}`, app, newApp),
		fmt.Sprintf(`{"prompt":"archive cleanup","cwd":%q,"sessionId":%q,"at":"2024-03-06T00:00:00.000Z"}`, lib, libSession),
		fmt.Sprintf(`{"prompt":"standalone prompt","cwd":%q,"at":"2024-03-07T00:00:00.000Z"}`, app),
		fmt.Sprintf(`{"prompt":"deploy the archive","cwd":%q,"sessionId":%q,"at":"2024-03-08T00:00:00.000Z"}`, app, newApp),
	}
	if err := os.WriteFile(histPath, []byte(strings.Join(histLines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write history.jsonl: %v", err)
	}

	// Prompts in app matching "archive".
	pv, err := ArchivePrompts(ctx, "archive", true, app, 20)
	if err != nil {
		t.Fatalf("ArchivePrompts app archive: %v", err)
	}
	ps := pv.Records.([]ArchivePrompt)
	if len(ps) != 1 || ps[0].Text != "deploy the archive" || ps[0].Uses != 2 {
		t.Fatalf("expected 'deploy the archive' with uses=2, got %+v", ps)
	}

	// Standalone prompt has session=null explicitly serialized.
	pvStandalone, err := ArchivePrompts(ctx, "standalone", true, app, 20)
	if err != nil {
		t.Fatalf("ArchivePrompts standalone: %v", err)
	}
	psStandalone := pvStandalone.Records.([]ArchivePrompt)
	if len(psStandalone) != 1 || psStandalone[0].Session != nil {
		t.Fatalf("expected standalone prompt with nil session, got %+v", psStandalone)
	}
	raw, _ := json.Marshal(psStandalone[0])
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	if _, ok := m["session"]; !ok || m["session"] != nil {
		t.Fatalf("expected explicit session:null in %s", string(raw))
	}

	// Word prefix matching in prompts.
	pvPrefix, _ := ArchivePrompts(ctx, "arch", true, lib, 20)
	if got := pvPrefix.Records.([]ArchivePrompt); len(got) != 1 || got[0].Text != "archive cleanup" {
		t.Fatalf("expected prefix match 'archive cleanup', got %+v", got)
	}
	pvMid, _ := ArchivePrompts(ctx, "chiv", true, lib, 20)
	if got := pvMid.Records.([]ArchivePrompt); len(got) != 0 {
		t.Fatalf("expected non-prefix 'chiv' to match nothing, got %+v", got)
	}

	// Recaps newest first in app.
	rv, err := ArchiveRecaps(ctx, app, 20)
	if err != nil {
		t.Fatalf("ArchiveRecaps app: %v", err)
	}
	rs := rv.Records.([]ArchiveRecap)
	if len(rs) != 2 || rs[0].Text != "second recap" || rs[1].Text != "first recap" {
		t.Fatalf("unexpected app recaps: %+v", rs)
	}

	// Recaps across all projects with limit=2.
	rvAll, err := ArchiveRecaps(ctx, "", 2)
	if err != nil {
		t.Fatalf("ArchiveRecaps global: %v", err)
	}
	rsAll := rvAll.Records.([]ArchiveRecap)
	if len(rsAll) != 2 || rsAll[0].Text != "second recap" || rsAll[1].Text != "first recap" {
		t.Fatalf("unexpected global recaps: %+v", rsAll)
	}
}

func TestPreludeContractValidationAndNonNilDetails(t *testing.T) {
	ClearCachesForTests()
	emptyRoot := t.TempDir()
	origOverride := SessionDirsOverride
	SessionDirsOverride = func() []string { return []string{emptyRoot} }
	t.Cleanup(func() {
		SessionDirsOverride = origOverride
		ClearCachesForTests()
	})

	p := Prelude()
	if p.Name != "archive" {
		t.Fatalf("Name = %q, want archive", p.Name)
	}
	if !reflect.DeepEqual(p.Exports, []string{"archive"}) {
		t.Fatalf("Exports = %v, want [archive]", p.Exports)
	}
	if p.Approval != "read" {
		t.Fatalf("Approval = %q, want read", p.Approval)
	}
	summary, ok := eval.PreludeSummary(p)
	wantSummary := "Read-only local history: recent projects, past sessions with recaps, full-text session search, prompt history."
	if !ok || summary != wantSummary {
		t.Fatalf("PreludeSummary = (%q, %v), want (%q, true)", summary, ok, wantSummary)
	}

	// Enabled defaults to true, respects archive.enabled=false.
	origSetting := Setting
	t.Cleanup(func() { Setting = origSetting })
	Setting = nil
	if !p.Enabled(sdk.Context{}) {
		t.Fatal("expected enabled=true by default")
	}
	Setting = func(_ sdk.Context, key string) any {
		if key == SettingArchiveEnabled {
			return false
		}
		return nil
	}
	if p.Enabled(sdk.Context{}) {
		t.Fatal("expected enabled=false when archive.enabled=false")
	}
	Setting = func(_ sdk.Context, key string) any {
		if key == SettingArchiveEnabled {
			return true
		}
		return nil
	}
	if !p.Enabled(sdk.Context{}) {
		t.Fatal("expected enabled=true when archive.enabled=true")
	}

	// Bridge invariant: empty results MUST return non-nil empty slice Details.
	for _, payload := range []string{
		`{"action":"projects"}`,
		`{"action":"sessions","project":"*"}`,
		`{"action":"search","query":"nothing","project":"*"}`,
		`{"action":"prompts","project":"*"}`,
		`{"action":"recaps","project":"*"}`,
	} {
		res, err := p.Invoke(eval.PreludeCall{}, json.RawMessage(payload))
		if err != nil {
			t.Fatalf("Invoke(%s) error: %v", payload, err)
		}
		if res == nil || res.Details == nil {
			t.Fatalf("Invoke(%s) returned nil Details (breaks eval bridge!)", payload)
		}
		rv := reflect.ValueOf(res.Details)
		if rv.Kind() != reflect.Slice || rv.IsNil() {
			t.Fatalf("Invoke(%s) Details must be non-nil slice, got %#v", payload, res.Details)
		}
		b, err := json.Marshal(res.Details)
		if err != nil || string(b) != "[]" {
			t.Fatalf("Invoke(%s) Details JSON = %s (err=%v), want []", payload, string(b), err)
		}
	}

	// Strict argument validation: reject unknown keys, invalid limits, missing required fields.
	invalidPayloads := []string{
		`{"action":"sessions","projects":"*"}`,
		`{"action":"projects","project":"*"}`,
		`{"action":"projects","limit":0}`,
		`{"action":"projects","limit":501}`,
		`{"action":"projects","limit":1.5}`,
		`{"action":"projects","limit":"10"}`,
		`{"action":"session"}`,
		`{"action":"session","id":""}`,
		`{"action":"search"}`,
		`{"action":"search","query":""}`,
		`{"action":"prompts","project":""}`,
		`{"action":"unknown"}`,
		`[]`,
	}
	for _, bad := range invalidPayloads {
		if _, err := p.Invoke(eval.PreludeCall{}, json.RawMessage(bad)); err == nil || !strings.Contains(err.Error(), "archive received invalid arguments:") {
			t.Fatalf("Invoke(%s) expected invalid arguments error, got: %v", bad, err)
		}
	}

	// Status line formatting matches upstream describeArchiveCall.
	if got := p.Status(json.RawMessage(`{"action":"projects"}`), nil); got != "archive.projects()" {
		t.Fatalf("status projects = %q", got)
	}
	if got := p.Status(json.RawMessage(`{"action":"search","query":"retry"}`), nil); got != `archive.search("retry")` {
		t.Fatalf("status search = %q", got)
	}
	if got := p.Status(json.RawMessage(`{"action":"sessions","project":"*"}`), nil); got != "archive.sessions(*)" {
		t.Fatalf("status sessions(*) = %q", got)
	}
	if got := p.Status(json.RawMessage(`{"action":"session","id":"abc123"}`), nil); got != "archive.session(abc123)" {
		t.Fatalf("status session = %q", got)
	}
}

func TestArchiveDedupeAcrossMultipleSessionRoots(t *testing.T) {
	ClearCachesForTests()
	root := t.TempDir()
	pigRoot := filepath.Join(root, "pig-sessions")
	ompRoot := filepath.Join(root, "omp-sessions")
	proj := filepath.Join(root, "repo")

	origOverride := SessionDirsOverride
	SessionDirsOverride = func() []string { return []string{pigRoot, ompRoot} }
	t.Cleanup(func() {
		SessionDirsOverride = origOverride
		ClearCachesForTests()
	})

	// Same session ID present in both PiG and omp dirs should deduplicate cleanly.
	id1, _ := writeTestSession(t, pigRoot, proj, "shared", "2024-06-01T00:00:00.000Z", testSessionOpts{
		prompt:   "shared prompt",
		answered: true,
	})
	writeTestSession(t, ompRoot, proj, "shared", "2024-06-01T00:00:00.000Z", testSessionOpts{
		prompt:   "shared prompt",
		answered: true,
	})
	id2, _ := writeTestSession(t, ompRoot, proj, "omp-only", "2024-05-01T00:00:00.000Z", testSessionOpts{
		prompt:   "omp only prompt",
		answered: true,
	})

	sv, err := ArchiveSessions(context.Background(), proj, 20)
	if err != nil {
		t.Fatalf("ArchiveSessions: %v", err)
	}
	sessions := sv.Records.([]ArchiveSession)
	if len(sessions) != 2 || sessions[0].ID != id1 || sessions[1].ID != id2 {
		t.Fatalf("expected deduplicated [shared, omp-only], got %+v", sessions)
	}
}

func TestArchivePreludeJSAndPythonFacades(t *testing.T) {
	p := Prelude()

	// Verify JavaScript facade if node/bun is available.
	if bin, err := exec.LookPath("bun"); err == nil {
		runJSFacadeCheck(t, bin, p.JavaScript)
	} else if bin, err := exec.LookPath("node"); err == nil {
		runJSFacadeCheck(t, bin, p.JavaScript)
	}

	// Verify Python facade if python3 is available.
	if bin, err := exec.LookPath("python3"); err == nil {
		runPyFacadeCheck(t, bin, p.Python)
	}
}

func runJSFacadeCheck(t *testing.T, bin, source string) {
	t.Helper()
	srcJSON, _ := json.Marshal(source)
	script := `
const assert = require("node:assert");
const vm = require("node:vm");
const calls = [];
const printed = [];
const ctx = vm.createContext({
	__omp_log__: (_lvl, text) => printed.push(text),
	__omp_prelude__: async (name, params) => {
		calls.push({ name, params: JSON.parse(JSON.stringify(params)) });
		return { text: "formatted output", details: [{ id: "s1", recap: null }] };
	},
});
vm.runInContext(` + string(srcJSON) + `, ctx);
(async () => {
	const r1 = await vm.runInContext('archive.sessions({ project: "*", limit: 5 })', ctx);
	assert.equal(r1.length, 1);
	assert.equal(r1[0].recap, null);
	assert.equal(printed.length, 1);
	await vm.runInContext('archive.search("hello", { silent: true })', ctx);
	assert.equal(printed.length, 1);
	assert.deepEqual(calls[0], { name: "archive", params: { project: "*", limit: 5, action: "sessions" } });
	assert.deepEqual(calls[1], { name: "archive", params: { query: "hello", action: "search" } });
	console.log("JS_OK");
})().catch(e => { console.error(e); process.exit(1); });
`
	out, err := exec.Command(bin, "-e", script).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "JS_OK") {
		t.Fatalf("JS facade check failed: %v\n%s", err, string(out))
	}
}

func runPyFacadeCheck(t *testing.T, bin, source string) {
	t.Helper()
	srcJSON, _ := json.Marshal(source)
	script := `
import asyncio, json

calls = []
async def _omp_prelude(name, params):
    calls.append((name, params))
    return {"text": "formatted", "details": [{"id": "s1", "recap": None}]}

exec(json.loads(` + fmt.Sprintf("%q", string(srcJSON)) + `))

async def main():
    rows = await archive.sessions(project="*", limit=5, silent=True)
    assert rows == [{"id": "s1", "recap": None}]
    assert calls[0] == ("archive", {"project": "*", "limit": 5, "action": "sessions"})
    await archive.search("hello", project="*", silent=True)
    assert calls[1] == ("archive", {"query": "hello", "project": "*", "action": "search"})
    print("PY_OK")

asyncio.run(main())
`
	out, err := exec.Command(bin, "-c", script).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "PY_OK") {
		t.Fatalf("Python facade check failed: %v\n%s", err, string(out))
	}
}

func TestArchiveBucketPrefilterSkipsUnrelatedBuckets(t *testing.T) {
	ClearCachesForTests()
	tmp := t.TempDir()
	origOverride := SessionDirsOverride
	SessionDirsOverride = func() []string { return []string{tmp} }
	t.Cleanup(func() {
		SessionDirsOverride = origOverride
		ClearCachesForTests()
	})

	appCwd := "/workspace/target-app"
	otherCwd := "/workspace/unrelated-other"
	appBucket := filepath.Join(tmp, "--workspace-target-app--")
	otherBucket := filepath.Join(tmp, "--workspace-unrelated-other--")
	if err := os.MkdirAll(appBucket, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(otherBucket, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	writeSess := func(dir, name, id, cwd, prompt string) string {
		p := filepath.Join(dir, name)
		content := fmt.Sprintf(
			"{\"type\":\"session\",\"id\":%q,\"timestamp\":%q,\"cwd\":%q}\n"+
				"{\"type\":\"message\",\"timestamp\":%q,\"message\":{\"role\":\"user\",\"content\":%q}}\n",
			id, now, cwd, now, prompt,
		)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	appFile := writeSess(appBucket, "s1.jsonl", "sess-app-1", appCwd, "hello target")
	otherFile := writeSess(otherBucket, "s2.jsonl", "sess-other-1", otherCwd, "hello other")

	view, err := ArchiveSessions(context.Background(), appCwd, 10)
	if err != nil {
		t.Fatalf("ArchiveSessions: %v", err)
	}
	rows := view.Records.([]ArchiveSession)
	if len(rows) != 1 || rows[0].ID != "sess-app-1" {
		t.Fatalf("expected only sess-app-1, got %+v", rows)
	}
	// Verify the unrelated bucket's file was NEVER full-scanned into scanCache.
	scanCacheMu.RLock()
	_, appCached := scanCache[appFile]
	_, otherCached := scanCache[otherFile]
	scanCacheMu.RUnlock()
	if !appCached {
		t.Fatalf("expected target app file to be scanned and cached")
	}
	if otherCached {
		t.Fatalf("expected unrelated bucket file to be skipped before full scan, but found in scanCache")
	}
}

func TestArchiveStreamingScanAndContextCancel(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "stream.jsonl")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var b strings.Builder
	fmt.Fprintf(&b, "{\"type\":\"session\",\"id\":\"stream-1\",\"timestamp\":%q,\"cwd\":\"/work/stream\"}\n", now)
	for i := range 200 {
		fmt.Fprintf(&b, "{\"type\":\"message\",\"timestamp\":%q,\"message\":{\"role\":\"user\",\"content\":\"msg-%d\"}}\n", now, i)
	}
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	full := scanFullSession(context.Background(), p, false, true, true)
	if full == nil || full.Info == nil || len(full.Messages) != 200 || len(full.Prompts) != 200 {
		t.Fatalf("unexpected streaming scan result: %+v", full)
	}
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := scanFullSession(cancelCtx, p, false, true, true); got != nil {
		t.Fatalf("expected nil on cancelled context, got %+v", got)
	}
}

func TestArchiveJsonQuoteNoHTMLEscaping(t *testing.T) {
	got := jsonQuote("a < b && c > d")
	want := `"a < b && c > d"`
	if got != want {
		t.Fatalf("jsonQuote escaped HTML: got %s, want %s", got, want)
	}
}

func TestArchiveSearchQueryLatinExtendedFolding(t *testing.T) {
	cases := []struct {
		query string
		text  string
	}{
		{"pho", "Phở Hà Nội"},
		{"chu", "chữ quốc ngữ"},
		{"krsna", "Kṛṣṇa"},
		{"tata", "ṭaṭa"},
		{"dot", "ḍoṭ ṁ ṅ ḃ ḟ ṗ ṡ ṫ"},
	}
	for _, tc := range cases {
		wq := ParseWordQuery(tc.query)
		if wq == nil || !wq.Test(tc.text) {
			t.Errorf("expected query %q to match %q (folded=%q)", tc.query, tc.text, ftsFold(tc.text))
		}
	}
}
