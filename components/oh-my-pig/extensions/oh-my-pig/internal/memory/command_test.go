package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func TestMemoryCommandOffBackend(t *testing.T) {
	setupMemoryTestEnv(t, nil)
	var zero sdk.Context

	for _, args := range []string{"", "view", "clear", "reset", "enqueue", "rebuild", "sync"} {
		msg, level := ExecuteMemoryCommand(zero, args)
		if level != "warning" || !strings.Contains(msg, "memory.backend=off") {
			t.Fatalf("args=%q: expected off warning, got (%q, %q)", args, level, msg)
		}
	}

	for _, args := range []string{"stats", "diagnose"} {
		msg, level := ExecuteMemoryCommand(zero, args)
		if level != "warning" || msg != "Memory stats are not available when memory.backend=off." {
			t.Fatalf("args=%q: unexpected (%q, %q)", args, level, msg)
		}
	}

	msg, level := ExecuteMemoryCommand(zero, "queue")
	if level != "warning" || !strings.Contains(msg, "not available") {
		t.Fatalf("queue off: got (%q, %q)", level, msg)
	}

	msg, level = ExecuteMemoryCommand(zero, "mm list")
	if level != "warning" || msg != mmDisabledNotice {
		t.Fatalf("mm list off: got (%q, %q)", level, msg)
	}

	msg, level = ExecuteMemoryCommand(zero, "unknown-subcmd")
	if level != "error" || msg != memoryCommandUsage {
		t.Fatalf("unknown subcmd: got (%q, %q)", level, msg)
	}
}

func TestMemoryCommandLocalBackend(t *testing.T) {
	agentDir, cwd := setupMemoryTestEnv(t, map[string]any{
		SettingMemoryBackend: BackendLocal,
	})
	var zero sdk.Context

	// Empty view.
	msg, level := ExecuteMemoryCommand(zero, "view")
	if level != "warning" || !strings.Contains(msg, "empty") {
		t.Fatalf("expected empty local view warning, got (%q, %q)", level, msg)
	}

	// Save a lesson and check view / stats / diagnose / sync / clear.
	if _, err := SaveLearnedLesson(agentDir, cwd, LocalSaveInput{Content: "Run go test -race"}); err != nil {
		t.Fatalf("save lesson: %v", err)
	}

	msg, level = ExecuteMemoryCommand(zero, "")
	if level != "info" || !strings.Contains(msg, "Run go test -race") {
		t.Fatalf("expected learned lesson in view, got (%q, %q)", level, msg)
	}

	msg, level = ExecuteMemoryCommand(zero, "stats")
	if level != "info" || !strings.Contains(msg, "1 lessons") || !strings.Contains(msg, MemoryRoot(agentDir, cwd)) {
		t.Fatalf("unexpected local stats: (%q, %q)", level, msg)
	}

	msg, level = ExecuteMemoryCommand(zero, "diagnose")
	if level != "info" || !strings.Contains(msg, "Memory Diagnostics (local)") {
		t.Fatalf("unexpected local diagnose: (%q, %q)", level, msg)
	}

	msg, level = ExecuteMemoryCommand(zero, "sync")
	if level != "info" || !strings.Contains(msg, "Memory consolidation ran.") {
		t.Fatalf("unexpected local sync: (%q, %q)", level, msg)
	}

	msg, level = ExecuteMemoryCommand(zero, "clear")
	if level != "info" || !strings.Contains(msg, "Memory cleared") {
		t.Fatalf("unexpected local clear: (%q, %q)", level, msg)
	}
	if _, err := os.Stat(filepath.Join(MemoryRoot(agentDir, cwd), LearnedLessonsFile)); !os.IsNotExist(err) {
		t.Fatalf("expected learned.md to be removed after clear, stat err=%v", err)
	}
}

func TestMemoryCommandMnemopiBackend(t *testing.T) {
	setupMemoryTestEnv(t, map[string]any{
		SettingMemoryBackend: BackendMnemopi,
		SettingMnemopiBank:   "cmd-test-bank",
	})
	var zero sdk.Context
	cfg := LoadMnemopiConfig(zero)

	if _, err := SeedMnemopiRecord(cfg, cfg.RetainBank, MnemopiRecord{
		ID:      "w1",
		Content: "working item",
		Store:   string(StoreWorking),
	}); err != nil {
		t.Fatalf("seed working: %v", err)
	}
	if _, err := SeedMnemopiRecord(cfg, cfg.RetainBank, MnemopiRecord{
		ID:      "e1",
		Content: "episodic item",
		Store:   string(StoreEpisodic),
	}); err != nil {
		t.Fatalf("seed episodic: %v", err)
	}
	if _, err := SeedMnemopiRecord(cfg, cfg.RetainBank, MnemopiRecord{
		ID:         "f1",
		Content:    "expired fact",
		Store:      string(StoreFact),
		ValidUntil: time.Now().UTC().Add(-time.Hour).Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("seed fact: %v", err)
	}

	msg, level := ExecuteMemoryCommand(zero, "view")
	if level != "info" || !strings.Contains(msg, "Mnemopi banks:") || !strings.Contains(msg, "2 active / 3 total") {
		t.Fatalf("unexpected mnemopi view: (%q, %q)", level, msg)
	}

	msg, level = ExecuteMemoryCommand(zero, "stats")
	if level != "info" ||
		!strings.Contains(msg, "working=1, episodic=1, fact=1") ||
		!strings.Contains(msg, "active=2, invalidated=1") ||
		!strings.Contains(msg, cfg.BankFilePath(cfg.RetainBank)) {
		t.Fatalf("unexpected mnemopi stats: (%q, %q)", level, msg)
	}

	// sync prunes invalidated records.
	msg, level = ExecuteMemoryCommand(zero, "sync")
	if level != "info" || !strings.Contains(msg, "Memory consolidation ran.") || !strings.Contains(msg, "pruned 1") {
		t.Fatalf("unexpected mnemopi sync: (%q, %q)", level, msg)
	}

	msg, level = ExecuteMemoryCommand(zero, "stats")
	if !strings.Contains(msg, "active=2, invalidated=0") {
		t.Fatalf("expected invalidated=0 after sync, got: %s", msg)
	}

	// clear removes bank file.
	msg, level = ExecuteMemoryCommand(zero, "reset")
	if level != "info" || !strings.Contains(msg, "Memory cleared") {
		t.Fatalf("unexpected mnemopi reset: (%q, %q)", level, msg)
	}
	if _, err := os.Stat(cfg.BankFilePath(cfg.RetainBank)); !os.IsNotExist(err) {
		t.Fatalf("expected bank file deleted, stat err=%v", err)
	}
}

func TestMemoryCommandHindsightBackendAndMentalModels(t *testing.T) {
	var (
		mu             sync.Mutex
		retainCalls    int
		deleteBankHit  bool
		refreshedIDs   []string
		createdModels  []string
		deletedModelID string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		p := r.URL.Path

		switch {
		case r.Method == http.MethodPut && strings.HasPrefix(p, "/v1/default/banks/") && !strings.Contains(p, "/memories") && !strings.Contains(p, "/mental-models"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
			return

		case r.Method == http.MethodDelete && strings.HasPrefix(p, "/v1/default/banks/") && !strings.Contains(p, "/mental-models"):
			mu.Lock()
			deleteBankHit = true
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			return

		case r.Method == http.MethodPost && strings.HasSuffix(p, "/memories"):
			mu.Lock()
			retainCalls++
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
			return

		case r.Method == http.MethodGet && strings.HasSuffix(p, "/mental-models"):
			items := []HindsightMentalModel{
				{
					ID:   "user-preferences",
					Name: "User Preferences",
					Tags: []string{},
					Trigger: &HindsightMentalModelTrigger{
						Mode:                    "delta",
						RefreshAfterConsolidate: true,
					},
				},
				{
					ID:   "curated-arch",
					Name: "Curated Architecture",
					Tags: []string{"project:demo"},
				},
			}
			_ = json.NewEncoder(w).Encode(HindsightMentalModelListResponse{Items: items})
			return

		case r.Method == http.MethodPost && strings.HasSuffix(p, "/mental-models"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			if id, ok := body["id"].(string); ok {
				createdModels = append(createdModels, id)
			}
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"operation_id":"op-1"}`))
			return

		case r.Method == http.MethodGet && strings.HasSuffix(p, "/mental-models/user-preferences"):
			_ = json.NewEncoder(w).Encode(HindsightMentalModel{
				ID:              "user-preferences",
				Name:            "User Preferences",
				SourceQuery:     "What does the user prefer?",
				Content:         "Prefers concise Go code.",
				LastRefreshedAt: "2026-11-28T00:00:00Z",
			})
			return

		case r.Method == http.MethodGet && strings.HasSuffix(p, "/mental-models/user-preferences/history"):
			_ = json.NewEncoder(w).Encode([]HindsightMentalModelHistoryEntry{
				{PreviousContent: "Old prefs.", ChangedAt: "2026-11-27T00:00:00Z"},
			})
			return

		case r.Method == http.MethodPost && strings.Contains(p, "/mental-models/") && strings.HasSuffix(p, "/refresh"):
			parts := strings.Split(p, "/")
			id := parts[len(parts)-2]
			mu.Lock()
			refreshedIDs = append(refreshedIDs, id)
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"operation_id":"op-ref"}`))
			return

		case r.Method == http.MethodDelete && strings.Contains(p, "/mental-models/"):
			parts := strings.Split(p, "/")
			id := parts[len(parts)-1]
			mu.Lock()
			deletedModelID = id
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"deleted":true}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	cfgMap := map[string]any{
		SettingMemoryBackend:                BackendHindsight,
		SettingHindsightAPIURL:              srv.URL,
		SettingHindsightAPIToken:            "secret-tok",
		SettingHindsightBankID:              "test-bank",
		SettingHindsightScoping:             "per-project-tagged",
		SettingHindsightMentalModelsEnabled: false,
	}
	agentDir, cwd := setupMemoryTestEnv(t, cfgMap)
	var zero sdk.Context

	// Enqueue one pending item to the outbox.
	hCfg := LoadHindsightConfig(zero)
	scope := ComputeHindsightBankScope(hCfg, cwd)
	if _, err := EnqueueHindsightRetain(agentDir, "s1", "tc1", hCfg, scope, []HindsightRetainInput{
		{Content: "Remember the build flag -race"},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// stats / diagnose / queue / view
	msg, level := ExecuteMemoryCommand(zero, "stats")
	if level != "info" || !strings.Contains(msg, "pending outbox items: 1") || !strings.Contains(msg, srv.URL) {
		t.Fatalf("unexpected hindsight stats: (%q, %q)", level, msg)
	}

	msg, level = ExecuteMemoryCommand(zero, "queue")
	if level != "info" || !strings.Contains(msg, "1 item(s)") || !strings.Contains(msg, "Remember the build flag -race") {
		t.Fatalf("unexpected hindsight queue: (%q, %q)", level, msg)
	}

	// sync flushes outbox.
	msg, level = ExecuteMemoryCommand(zero, "sync")
	if level != "info" || !strings.Contains(msg, "delivered=1, remaining=0") {
		t.Fatalf("unexpected hindsight sync: (%q, %q)", level, msg)
	}
	mu.Lock()
	if retainCalls == 0 {
		mu.Unlock()
		t.Fatal("expected retain HTTP call on sync")
	}
	mu.Unlock()

	// clear never deletes remote bank.
	msg, level = ExecuteMemoryCommand(zero, "clear")
	if level != "info" || !strings.Contains(msg, "Remote bank") {
		t.Fatalf("unexpected hindsight clear: (%q, %q)", level, msg)
	}
	mu.Lock()
	if deleteBankHit {
		mu.Unlock()
		t.Fatal("hindsight clear must NEVER delete remote bank")
	}
	mu.Unlock()

	// mm disabled by default.
	msg, level = ExecuteMemoryCommand(zero, "mm list")
	if level != "warning" || msg != mmDisabledNotice {
		t.Fatalf("expected mm disabled notice, got (%q, %q)", level, msg)
	}

	// Enable mental models.
	cfgMap[SettingHindsightMentalModelsEnabled] = true

	msg, level = ExecuteMemoryCommand(zero, "mm list")
	if level != "info" || !strings.Contains(msg, "user-preferences") || !strings.Contains(msg, "curated-arch") {
		t.Fatalf("unexpected mm list: (%q, %q)", level, msg)
	}

	msg, level = ExecuteMemoryCommand(zero, "mm show user-preferences")
	if level != "info" || !strings.Contains(msg, "Prefers concise Go code.") {
		t.Fatalf("unexpected mm show: (%q, %q)", level, msg)
	}

	msg, level = ExecuteMemoryCommand(zero, "mm show")
	if level != "error" || !strings.Contains(msg, "Usage:") {
		t.Fatalf("expected usage error for mm show without id, got (%q, %q)", level, msg)
	}

	msg, level = ExecuteMemoryCommand(zero, "mm refresh")
	if level != "info" || !strings.Contains(msg, "1/1 auto-refresh") || !strings.Contains(msg, "skipped 1 curated") {
		t.Fatalf("unexpected mm refresh: (%q, %q)", level, msg)
	}

	msg, level = ExecuteMemoryCommand(zero, "mm refresh curated-arch")
	if level != "info" || !strings.Contains(msg, "Refresh queued for mental model curated-arch") {
		t.Fatalf("unexpected mm refresh <id>: (%q, %q)", level, msg)
	}

	msg, level = ExecuteMemoryCommand(zero, "mm history user-preferences")
	if level != "info" || !strings.Contains(msg, "2026-11-27T00:00:00Z") {
		t.Fatalf("unexpected mm history: (%q, %q)", level, msg)
	}

	msg, level = ExecuteMemoryCommand(zero, "mm seed")
	if level != "info" || !strings.Contains(msg, "Seeded 2 new") || !strings.Contains(msg, "1 already present") {
		t.Fatalf("unexpected mm seed: (%q, %q)", level, msg)
	}

	msg, level = ExecuteMemoryCommand(zero, "mm reload")
	if level != "info" || !strings.Contains(msg, "reloaded") {
		t.Fatalf("unexpected mm reload: (%q, %q)", level, msg)
	}

	msg, level = ExecuteMemoryCommand(zero, "mm delete curated-arch")
	if level != "info" || !strings.Contains(msg, "Deleted mental model curated-arch") {
		t.Fatalf("unexpected mm delete: (%q, %q)", level, msg)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(refreshedIDs) != 2 || refreshedIDs[0] != "user-preferences" || refreshedIDs[1] != "curated-arch" {
		t.Fatalf("unexpected refreshed IDs: %v", refreshedIDs)
	}
	if len(createdModels) != 2 {
		t.Fatalf("expected 2 seeded models created, got %v", createdModels)
	}
	if deletedModelID != "curated-arch" {
		t.Fatalf("expected deletedModelID=curated-arch, got %q", deletedModelID)
	}

	// Verify Register registers the command cleanly (no panic).
	ext := sdk.New("test-memory-cmd")
	Register(ext)
	_ = context.Background()
}

func TestMemoryClearLocalRemovesSummary_M3(t *testing.T) {
	agentDir, cwd := setupMemoryTestEnv(t, map[string]any{
		SettingMemoryBackend: BackendLocal,
	})

	root := MemoryRoot(agentDir, cwd)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	learnedPath := filepath.Join(root, LearnedLessonsFile)
	summaryPath := filepath.Join(root, "memory_summary.md")
	if err := os.WriteFile(learnedPath, []byte("- lesson\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(summaryPath, []byte("summary text\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var zero sdk.Context
	msg, level := ExecuteMemoryCommand(zero, "clear")
	if level != "info" {
		t.Fatalf("expected info, got (%q, %q)", level, msg)
	}
	if _, err := os.Stat(learnedPath); !os.IsNotExist(err) {
		t.Fatalf("expected learned.md removed")
	}
	if _, err := os.Stat(summaryPath); !os.IsNotExist(err) {
		t.Fatalf("expected memory_summary.md removed")
	}
}

func TestMemoryClearMnemopiAllScopedBanks_M4(t *testing.T) {
	setupMemoryTestEnv(t, map[string]any{
		SettingMemoryBackend:  BackendMnemopi,
		SettingMnemopiScoping: "per-project",
	})

	var zero sdk.Context
	cfg := LoadMnemopiConfig(zero)
	banks := cfg.ScopedBanks()
	if len(banks) == 0 {
		t.Fatalf("expected >= 1 scoped bank, got %v", banks)
	}
	for _, b := range banks {
		p := cfg.BankFilePath(b)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(`{"bank":"`+b+`","records":[]}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	msg, level := ExecuteMemoryCommand(zero, "clear")
	if level != "info" {
		t.Fatalf("expected info, got (%q, %q)", level, msg)
	}
	for _, b := range banks {
		if _, err := os.Stat(cfg.BankFilePath(b)); !os.IsNotExist(err) {
			t.Fatalf("expected bank %s file removed", b)
		}
	}
}

func TestListMentalModelsStopsWhenServerIgnoresOffset_M7(t *testing.T) {
	calls := 0
	items := make([]HindsightMentalModel, hindsightMentalModelPageSize)
	for i := range items {
		items[i] = HindsightMentalModel{ID: fmt.Sprintf("mm-%d", i), Name: "Model"}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": items,
			"total": 999999,
		})
	}))
	defer srv.Close()

	client, err := NewHindsightClient(HindsightConfig{
		APIURL:           srv.URL,
		RequestTimeoutMs: 5000,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.ListMentalModels(context.Background(), "bank-1", "metadata")
	if err != nil {
		t.Fatalf("ListMentalModels: %v", err)
	}
	if len(got) != hindsightMentalModelPageSize {
		t.Fatalf("expected %d unique items, got %d", hindsightMentalModelPageSize, len(got))
	}
	if calls != 2 {
		t.Fatalf("expected 2 requests (page 1 + duplicate page 2 stop), got %d", calls)
	}
}

func TestMMSeedTagSetAndDottedProjectSuffix_M8_L2(t *testing.T) {
	var createdIDs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/mental-models") {
			// Legacy bare ID "project-architecture" exists with DIFFERENT tags (should NOT skip).
			// Legacy bare ID "user-preferences" exists with matching empty tags (should skip).
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{"id": "project-conventions", "name": "Legacy", "tags": []string{"project:other"}},
					{"id": "user-preferences", "name": "Prefs", "tags": []string{}},
				},
			})
			return
		}
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/mental-models") {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if id, _ := body["id"].(string); id != "" {
				createdIDs = append(createdIDs, id)
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	setupMemoryTestEnv(t, map[string]any{
		SettingMemoryBackend:                BackendHindsight,
		SettingHindsightAPIURL:              srv.URL,
		SettingHindsightScoping:             "per-project-tagged",
		SettingHindsightMentalModelsEnabled: true,
	})
	dottedCwd := filepath.Join(t.TempDir(), "my.dotted.repo")
	if err := os.MkdirAll(dottedCwd, 0o755); err != nil {
		t.Fatal(err)
	}
	origCwd := CwdOverride
	CwdOverride = func() string { return dottedCwd }
	t.Cleanup(func() { CwdOverride = origCwd })
	var zero sdk.Context
	// L2: reject ".." in mm show/delete/refresh/history.
	for _, cmd := range []string{"mm show ..", "mm delete ..", "mm refresh .", "mm history ../etc"} {
		msg, level := ExecuteMemoryCommand(zero, cmd)
		if level != "error" || !strings.Contains(msg, "invalid mental model id") {
			t.Fatalf("expected invalid id error for %q, got (%q, %q)", cmd, level, msg)
		}
	}

	msg, level := ExecuteMemoryCommand(zero, "mm seed")
	if level != "info" {
		t.Fatalf("unexpected mm seed: (%q, %q)", level, msg)
	}
	foundDotted := false
	for _, id := range createdIDs {
		if id == "project-conventions-my.dotted.repo" {
			foundDotted = true
		}
	}
	if !foundDotted {
		t.Fatalf("expected project-conventions-my.dotted.repo in created IDs, got %v", createdIDs)
	}
}

func TestHindsightClearKeepsOutboxOnFlushFailure_L1(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	agentDir, cwd := setupMemoryTestEnv(t, map[string]any{
		SettingMemoryBackend:   BackendHindsight,
		SettingHindsightAPIURL: srv.URL,
	})

	var zero sdk.Context
	cfg := LoadHindsightConfig(zero)
	scope := ComputeHindsightBankScope(cfg, cwd)
	if _, err := EnqueueHindsightRetain(agentDir, cwd, "sess-1", cfg, scope, []HindsightRetainInput{
		{Content: "important note"},
	}); err != nil {
		t.Fatal(err)
	}
	outboxPath := HindsightOutboxPath(agentDir, cfg.APIURL, scope.BankID)

	msg, level := ExecuteMemoryCommand(zero, "clear")
	if level != "warning" || !strings.Contains(msg, "NOT deleted") {
		t.Fatalf("expected warning preserving outbox, got (%q, %q)", level, msg)
	}
	if _, err := os.Stat(outboxPath); err != nil {
		t.Fatalf("expected outbox file to still exist, stat err: %v", err)
	}
}

func TestMnemopiSyncKeepsWorkingReferencedByFact_L6(t *testing.T) {
	setupMemoryTestEnv(t, map[string]any{
		SettingMemoryBackend:  BackendMnemopi,
		SettingMnemopiScoping: "global",
	})

	var zero sdk.Context
	cfg := LoadMnemopiConfig(zero)
	bank := cfg.RetainBank
	bankPath := cfg.BankFilePath(bank)
	if err := os.MkdirAll(filepath.Dir(bankPath), 0o755); err != nil {
		t.Fatal(err)
	}
	bf := mnemopiBankFile{
		Bank: bank,
		Records: []MnemopiRecord{
			{ID: "w-ref", Store: string(StoreWorking), Content: "raw msg", SupersededBy: "f-1"},
			{ID: "w-unref", Store: string(StoreWorking), Content: "old msg", SupersededBy: "other"},
			{ID: "f-1", Store: string(StoreFact), Content: "distilled fact", Metadata: map[string]any{"source_msg_id": "w-ref"}},
		},
	}
	if err := writeBankFileLocked(bankPath, bf); err != nil {
		t.Fatal(err)
	}

	msg, level := ExecuteMemoryCommand(zero, "sync")
	if level != "info" || !strings.Contains(msg, "pruned 1") {
		t.Fatalf("expected 1 pruned (w-unref only), got (%q, %q)", level, msg)
	}
	after, err := readBankFileLocked(bankPath, bank)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Records) != 2 {
		t.Fatalf("expected 2 records remaining (w-ref + f-1), got %+v", after.Records)
	}
}
