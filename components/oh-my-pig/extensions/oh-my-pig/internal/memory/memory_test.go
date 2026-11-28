package memory

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/autolearn"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/toolcmd"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

func setupMemoryTestEnv(t *testing.T, cfg map[string]any) (agentDir, cwd string) {
	t.Helper()
	agentDir = t.TempDir()
	cwd = filepath.Join(t.TempDir(), "my-project")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("mkdir cwd: %v", err)
	}

	origSetting := Setting
	origIsSubagent := IsSubagent
	origExplicitTools := ExplicitTools
	origAgentDir := AgentDirOverride
	origCwd := CwdOverride
	origRefresh := RefreshSkills
	origAuthored := autolearn.IsNameClaimedByAuthoredSkill
	origAutolearnRefresh := autolearn.RefreshSkills

	Setting = func(_ sdk.Context, key string) any {
		if cfg == nil {
			return nil
		}
		return cfg[key]
	}
	IsSubagent = nil
	ExplicitTools = nil
	AgentDirOverride = func() string { return agentDir }
	CwdOverride = func() string { return cwd }
	RefreshSkills = nil
	autolearn.IsNameClaimedByAuthoredSkill = nil
	autolearn.RefreshSkills = nil
	resetHindsightBanksForTests()

	t.Cleanup(func() {
		Setting = origSetting
		IsSubagent = origIsSubagent
		ExplicitTools = origExplicitTools
		AgentDirOverride = origAgentDir
		CwdOverride = origCwd
		RefreshSkills = origRefresh
		autolearn.IsNameClaimedByAuthoredSkill = origAuthored
		autolearn.RefreshSkills = origAutolearnRefresh
		resetHindsightBanksForTests()
	})
	return agentDir, cwd
}

func TestDefaultOffInvariantAndGating(t *testing.T) {
	setupMemoryTestEnv(t, nil)
	var zero sdk.Context

	// Default session: memory.backend="off", autolearn.enabled=false.
	if BackendFor(zero) != BackendOff {
		t.Fatalf("expected default backend off, got %q", BackendFor(zero))
	}
	if RetainRecallReflectActive(zero) {
		t.Fatal("retain/recall/reflect must be inactive by default")
	}
	if MemoryEditActive(zero) {
		t.Fatal("memory_edit must be inactive by default")
	}
	if LearnActive(zero) {
		t.Fatal("learn must be inactive by default")
	}
	if IsGlobalMemoryScopeAvailable(zero) {
		t.Fatal("global scope must be false by default")
	}
	if got := BuildMemoryPromptInstructions(zero); got != "" {
		t.Fatalf("expected empty prompt instructions when off, got %q", got)
	}
	if got := (MemoryHandler{}).PromptDoc(urls.SchemeHost{MemoryBackend: BackendOff}); got != "" {
		t.Fatalf("expected empty memory:// PromptDoc when off, got %q", got)
	}
	if got := (MemoryHandler{}).PromptDoc(urls.SchemeHost{MemoryBackend: BackendMnemopi}); got != "" {
		t.Fatalf("expected empty memory:// PromptDoc when mnemopi, got %q", got)
	}
	if got := (MemoryHandler{}).PromptDoc(urls.SchemeHost{MemoryBackend: BackendLocal}); got == "" {
		t.Fatal("expected non-empty memory:// PromptDoc when local")
	}

	// Verify Register wires all 5 tools with DefaultActive=false and wraps with intent `i`.
	toolcmd.ResetForTests()
	t.Cleanup(toolcmd.ResetForTests)
	ext := sdk.New("memory-default-off-test")
	Register(ext)

	hostSide, extSide := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- ext.RunWithConn(extSide) }()

	var lenBuf [4]byte
	if _, err := io.ReadFull(hostSide, lenBuf[:]); err != nil {
		t.Fatalf("read register length: %v", err)
	}
	payload := make([]byte, binary.BigEndian.Uint32(lenBuf[:]))
	if _, err := io.ReadFull(hostSide, payload); err != nil {
		t.Fatalf("read register body: %v", err)
	}
	_ = hostSide.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}

	var frame struct {
		Type     string `json:"type"`
		Register *struct {
			Tools []struct {
				Name          string         `json:"name"`
				DefaultActive *bool          `json:"default_active"`
				Parameters    map[string]any `json:"parameters"`
			} `json:"tools"`
		} `json:"register"`
	}
	if err := json.Unmarshal(payload, &frame); err != nil {
		t.Fatalf("unmarshal register frame: %v", err)
	}
	wantTools := map[string]bool{
		"retain":      false,
		"recall":      false,
		"reflect":     false,
		"memory_edit": false,
		"learn":       false,
	}
	for _, tl := range frame.Register.Tools {
		if _, ok := wantTools[tl.Name]; !ok {
			t.Fatalf("unexpected tool registered: %q", tl.Name)
		}
		wantTools[tl.Name] = true
		if tl.DefaultActive == nil || *tl.DefaultActive {
			t.Fatalf("tool %q must register with DefaultActive=false", tl.Name)
		}
		props, _ := tl.Parameters["properties"].(map[string]any)
		if _, hasI := props["i"]; !hasI {
			t.Fatalf("tool %q must be wrapped with intent.Tool (missing `i` property)", tl.Name)
		}
		if addProps, ok := tl.Parameters["additionalProperties"].(bool); !ok || addProps {
			t.Fatalf("tool %q must set additionalProperties=false, got %#v", tl.Name, tl.Parameters["additionalProperties"])
		}
	}
	for name, seen := range wantTools {
		if !seen {
			t.Fatalf("expected tool %q to be registered", name)
		}
	}
}

func TestActivationGatesPerBackendAndSubagent(t *testing.T) {
	var zero sdk.Context

	// Local backend: only learn when autolearn.enabled=true.
	setupMemoryTestEnv(t, map[string]any{
		SettingMemoryBackend:     BackendLocal,
		autolearn.SettingEnabled: true,
	})
	if RetainRecallReflectActive(zero) {
		t.Fatal("local backend must not activate retain/recall/reflect")
	}
	if MemoryEditActive(zero) {
		t.Fatal("local backend must not activate memory_edit")
	}
	if !LearnActive(zero) {
		t.Fatal("local + autolearn.enabled must activate learn")
	}
	// Subagent without explicit learn -> false; with explicit learn -> true.
	IsSubagent = func(sdk.Context) bool { return true }
	if LearnActive(zero) {
		t.Fatal("subagent without explicit learn must not activate learn")
	}
	ExplicitTools = func(sdk.Context) []string { return []string{"read", "learn"} }
	if !LearnActive(zero) {
		t.Fatal("subagent with explicit learn must activate learn")
	}

	// Mnemopi backend: retain/recall/reflect/memory_edit active; learn gated on autolearn.enabled.
	setupMemoryTestEnv(t, map[string]any{
		SettingMemoryBackend: BackendMnemopi,
	})
	if !RetainRecallReflectActive(zero) || !MemoryEditActive(zero) {
		t.Fatal("mnemopi must activate retain/recall/reflect/memory_edit")
	}
	if LearnActive(zero) {
		t.Fatal("learn must stay off when autolearn.enabled is false")
	}

	// Hindsight without apiUrl -> all inactive.
	setupMemoryTestEnv(t, map[string]any{
		SettingMemoryBackend:     BackendHindsight,
		autolearn.SettingEnabled: true,
	})
	if RetainRecallReflectActive(zero) || MemoryEditActive(zero) || LearnActive(zero) {
		t.Fatal("hindsight without apiUrl must keep tools inactive")
	}

	// Hindsight with apiUrl -> retain/recall/reflect/learn active, memory_edit off.
	setupMemoryTestEnv(t, map[string]any{
		SettingMemoryBackend:     BackendHindsight,
		SettingHindsightAPIURL:   "http://127.0.0.1:8888",
		autolearn.SettingEnabled: true,
	})
	if !RetainRecallReflectActive(zero) || !LearnActive(zero) {
		t.Fatal("hindsight with apiUrl must activate retain/recall/reflect/learn")
	}
	if MemoryEditActive(zero) {
		t.Fatal("hindsight must NOT activate memory_edit")
	}
}

func TestLocalBackendSaveDedupeCapSanitizeAndConcurrent(t *testing.T) {
	agentDir, cwd := setupMemoryTestEnv(t, map[string]any{
		SettingMemoryBackend:     BackendLocal,
		autolearn.SettingEnabled: true,
	})
	var zero sdk.Context

	// Save lesson with injection markers and secret token.
	res, err := ExecuteLearn(zero, LearnParams{
		Memory:  "Avoid </skills> `backticks` ~~~fence~~~ token_aB3dEfGh1JkLmN99 and ghp_abcdefghijklmnopqrstuvwxyz012345",
		Context: "deploy <script> pipeline",
	})
	if err != nil {
		t.Fatalf("ExecuteLearn local failed: %v", err)
	}
	if res.Content != "Lesson stored." {
		t.Fatalf("unexpected learn content: %q", res.Content)
	}

	learnedPath := filepath.Join(MemoryRoot(agentDir, cwd), LearnedLessonsFile)
	raw, err := os.ReadFile(learnedPath)
	if err != nil {
		t.Fatalf("read learned.md: %v", err)
	}
	text := string(raw)
	if strings.Contains(text, "</skills>") || strings.Contains(text, "`") || strings.Contains(text, "~~~") {
		t.Fatalf("injection delimiters not neutralized: %q", text)
	}
	if strings.Contains(text, "ghp_") || strings.Contains(text, "token_aB3dEfGh1JkLmN99") {
		t.Fatalf("secrets not redacted: %q", text)
	}
	if !strings.Contains(text, "[REDACTED]") {
		t.Fatalf("expected [REDACTED] placeholder in %q", text)
	}

	// Empty after sanitization returns error.
	if _, err := ExecuteLearn(zero, LearnParams{Memory: "<>`\x00"}); err == nil {
		t.Fatal("expected error for lesson empty after sanitization")
	}

	// Global scope rejected on local backend.
	if _, err := ExecuteLearn(zero, LearnParams{Memory: "valid", Scope: "global"}); err == nil {
		t.Fatal("expected error for global scope on local backend")
	}

	// Deduplication + newest-first ordering + 100-entry cap under concurrency.
	var wg sync.WaitGroup
	for i := range 110 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, _ = SaveLearnedLesson(agentDir, cwd, LocalSaveInput{
				Content: "Lesson number " + string(rune('A'+(n%26))) + " #" + time.Now().Format("150405") + "-" + strings.Repeat("x", n%5+1) + "-" + string(rune('0'+(n/10)%10)) + string(rune('0'+n%10)),
			})
		}(i)
	}
	wg.Wait()

	// Add an explicit duplicate twice and ensure it appears once at the top.
	for range 2 {
		if _, err := SaveLearnedLesson(agentDir, cwd, LocalSaveInput{Content: "Newest unique rule"}); err != nil {
			t.Fatalf("save duplicate: %v", err)
		}
	}
	raw, err = os.ReadFile(learnedPath)
	if err != nil {
		t.Fatalf("read learned.md after cap: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) > MaxLearnedLessons {
		t.Fatalf("expected at most %d lessons, got %d", MaxLearnedLessons, len(lines))
	}
	if lines[0] != "- Newest unique rule" {
		t.Fatalf("expected newest lesson first, got %q", lines[0])
	}
	dupCount := 0
	for _, l := range lines {
		if l == "- Newest unique rule" {
			dupCount++
		}
	}
	if dupCount != 1 {
		t.Fatalf("expected deduplicated count 1, got %d", dupCount)
	}

	// Prompt instructions include learned lessons.
	instr := BuildMemoryPromptInstructions(zero)
	if !strings.Contains(instr, "# Memory Guidance") || !strings.Contains(instr, "Newest unique rule") {
		t.Fatalf("expected local memory prompt instructions, got %q", instr)
	}
}

func TestMnemopiRetainRecallReflectEditAndMemoryURL(t *testing.T) {
	setupMemoryTestEnv(t, map[string]any{
		SettingMemoryBackend:  BackendMnemopi,
		SettingMnemopiScoping: "per-project-tagged",
		SettingMnemopiBank:    "team",
	})
	var zero sdk.Context

	if !IsGlobalMemoryScopeAvailable(zero) {
		t.Fatal("expected global scope available in per-project-tagged mode")
	}

	// Retain project + global memories (including a secret that must be redacted).
	retRes, err := ExecuteRetain(zero, RetainParams{
		Items: []RetainItem{
			{Content: "Project uses PostgreSQL 16 with pgvector for embeddings", Context: "architecture"},
			{Content: "User prefers concise Go table-driven tests with token AKIAIOSFODNN7EXAMPLE", Scope: "global"},
		},
	})
	if err != nil {
		t.Fatalf("ExecuteRetain failed: %v", err)
	}
	if retRes.Content != "2 memories stored." {
		t.Fatalf("unexpected retain message: %q", retRes.Content)
	}
	if details, ok := retRes.Details.(map[string]any); !ok || details["count"] != 2 {
		t.Fatalf("unexpected retain details: %#v", retRes.Details)
	}

	// Recall project memory and verify upstream bullet format.
	recRes, err := ExecuteRecall(zero, RecallParams{Query: "PostgreSQL embeddings"})
	if err != nil {
		t.Fatalf("ExecuteRecall failed: %v", err)
	}
	if !strings.Contains(recRes.Content, "Found 1 relevant memory (as of ") ||
		!strings.Contains(recRes.Content, "(id: mem-") ||
		!strings.Contains(recRes.Content, "[coding-agent-retain]") ||
		!strings.Contains(recRes.Content, " c:") {
		t.Fatalf("unexpected recall format: %q", recRes.Content)
	}

	// Extract the memory ID from recall output.
	start := strings.Index(recRes.Content, "(id: ") + len("(id: ")
	end := strings.Index(recRes.Content[start:], ")")
	memID := recRes.Content[start : start+end]

	// Global memory is also recalled in per-project-tagged mode, with secret redacted.
	globalRec, err := ExecuteRecall(zero, RecallParams{Query: "concise table-driven tests"})
	if err != nil {
		t.Fatalf("global recall failed: %v", err)
	}
	if strings.Contains(globalRec.Content, "AKIAIOSFODNN7EXAMPLE") || !strings.Contains(globalRec.Content, "[REDACTED]") {
		t.Fatalf("expected secret redacted in global recall: %q", globalRec.Content)
	}

	// Reflect returns synthesized recall output.
	refRes, err := ExecuteReflect(zero, ReflectParams{Query: "PostgreSQL", Context: "database"})
	if err != nil {
		t.Fatalf("ExecuteReflect failed: %v", err)
	}
	if !strings.HasPrefix(refRes.Content, "Based on recalled memories:\n\n") {
		t.Fatalf("unexpected reflect output: %q", refRes.Content)
	}

	// memory://<id> reads full record with YAML frontmatter.
	h := MemoryHandler{}
	res, err := h.Resolve(urls.Context{SDK: zero}, urls.URL{
		Href:   "memory://" + memID,
		Scheme: "memory",
		Host:   memID,
	})
	if err != nil {
		t.Fatalf("memory://<id> resolve failed: %v", err)
	}
	if !strings.Contains(res.Content, "id: "+memID) || !strings.Contains(res.Content, "store: working") || !strings.Contains(res.Content, "PostgreSQL 16") {
		t.Fatalf("unexpected memory://<id> content: %q", res.Content)
	}

	// memory_edit: update working memory.
	newContent := "Project upgraded to PostgreSQL 17 with pgvector"
	newImp := 0.9
	editRes, err := ExecuteMemoryEdit(zero, MemoryEditParams{
		Op:         "update",
		ID:         memID,
		Content:    &newContent,
		Importance: &newImp,
	})
	if err != nil {
		t.Fatalf("memory_edit update failed: %v", err)
	}
	if !strings.Contains(editRes.Content, "updated in bank ") || !strings.Contains(editRes.Content, "(working)") {
		t.Fatalf("unexpected update response: %q", editRes.Content)
	}

	// Verify updated content via memory://<id>.
	res2, err := h.Resolve(urls.Context{SDK: zero}, urls.URL{Href: "memory://" + memID, Scheme: "memory", Host: memID})
	if err != nil || !strings.Contains(res2.Content, "PostgreSQL 17") {
		t.Fatalf("expected updated content via memory://<id>, got err=%v content=%q", err, res2.Content)
	}

	// Seed an episodic record and a read-only fact record to test all store behaviors.
	cfg := LoadMnemopiConfig(zero)
	epID, err := SeedMnemopiRecord(cfg, cfg.RetainBank, MnemopiRecord{
		ID:      "ep-1",
		Content: "Episodic incident summary from Tuesday deploy",
		Store:   string(StoreEpisodic),
		Source:  "coding-agent-transcript",
	})
	if err != nil {
		t.Fatalf("seed episodic: %v", err)
	}
	factID, err := SeedMnemopiRecord(cfg, cfg.RetainBank, MnemopiRecord{
		ID:      "fact-1",
		Content: "Deployment runs on port 8080",
		Store:   string(StoreFact),
		Source:  "facts",
	})
	if err != nil {
		t.Fatalf("seed fact: %v", err)
	}

	// Fact store: memory://<factID> readable, any memory_edit returns not_editable.
	factRes, err := h.Resolve(urls.Context{SDK: zero}, urls.URL{Href: "memory://" + factID, Scheme: "memory", Host: factID})
	if err != nil || !strings.Contains(factRes.Content, "store: fact") {
		t.Fatalf("expected fact readable via memory://<id>, err=%v res=%#v", err, factRes)
	}
	for _, op := range []string{"update", "forget", "invalidate"} {
		txt := "attempt"
		var cPtr *string
		if op == "update" {
			cPtr = &txt
		}
		ed, err := ExecuteMemoryEdit(zero, MemoryEditParams{Op: op, ID: factID, Content: cPtr})
		if err != nil {
			t.Fatalf("memory_edit %s on fact returned error: %v", op, err)
		}
		details := ed.Details.(map[string]any)
		if details["status"] != "not_editable" || details["store"] != "fact" {
			t.Fatalf("expected not_editable for fact %s, got %#v", op, details)
		}
		if !strings.Contains(ed.Content, "read-only fact") || !strings.Contains(ed.Content, "memory://"+factID) {
			t.Fatalf("unexpected fact edit message: %q", ed.Content)
		}
	}

	// Episodic store: update/forget -> not_found; invalidate -> invalidated (and excluded from recall).
	epUpdateTxt := "new ep"
	edEpUpd, err := ExecuteMemoryEdit(zero, MemoryEditParams{Op: "update", ID: epID, Content: &epUpdateTxt})
	if err != nil || edEpUpd.Details.(map[string]any)["status"] != "not_found" {
		t.Fatalf("expected not_found on episodic update, err=%v res=%#v", err, edEpUpd)
	}
	edEpInv, err := ExecuteMemoryEdit(zero, MemoryEditParams{Op: "invalidate", ID: epID, ReplacementID: memID})
	if err != nil || edEpInv.Details.(map[string]any)["status"] != "invalidated" {
		t.Fatalf("expected invalidated on episodic invalidate, err=%v res=%#v", err, edEpInv)
	}
	epRecall, err := ExecuteRecall(zero, RecallParams{Query: "Tuesday deploy"})
	if err != nil || epRecall.Content != "No relevant memories found." {
		t.Fatalf("invalidated episodic memory should not be recalled, got err=%v content=%q", err, epRecall.Content)
	}

	// Working memory: forget deletes permanently.
	edForget, err := ExecuteMemoryEdit(zero, MemoryEditParams{Op: "forget", ID: memID})
	if err != nil || edForget.Details.(map[string]any)["status"] != "deleted" {
		t.Fatalf("expected deleted on working forget, err=%v res=%#v", err, edForget)
	}
	edMissing, err := ExecuteMemoryEdit(zero, MemoryEditParams{Op: "forget", ID: "does-not-exist"})
	if err != nil || edMissing.Details.(map[string]any)["status"] != "not_found" {
		t.Fatalf("expected not_found for missing id, err=%v res=%#v", err, edMissing)
	}
}

func TestMnemopiPerProjectRejectsGlobalScope(t *testing.T) {
	setupMemoryTestEnv(t, map[string]any{
		SettingMemoryBackend:  BackendMnemopi,
		SettingMnemopiScoping: "per-project",
	})
	var zero sdk.Context
	if IsGlobalMemoryScopeAvailable(zero) {
		t.Fatal("per-project mnemopi should not advertise global scope")
	}
	_, err := ExecuteRetain(zero, RetainParams{
		Items: []RetainItem{{Content: "cross-project", Scope: "global"}},
	})
	if err == nil || !strings.Contains(err.Error(), "global or per-project-tagged") {
		t.Fatalf("expected global-scope rejection error, got %v", err)
	}
}

func TestMnemopiOptionalHTTPEmbeddings(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Header.Get("Authorization") != "Bearer emb-secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		in, _ := req["input"].(string)
		vec := []float64{1, 0}
		if strings.Contains(strings.ToLower(in), "banana") || strings.Contains(strings.ToLower(in), "fruit") {
			vec = []float64{0, 1}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"embedding": vec}},
		})
	}))
	defer srv.Close()

	setupMemoryTestEnv(t, map[string]any{
		SettingMemoryBackend:          BackendMnemopi,
		SettingMnemopiEmbeddingAPIURL: srv.URL,
		SettingMnemopiEmbeddingAPIKey: "emb-secret",
	})
	var zero sdk.Context
	if _, err := ExecuteRetain(zero, RetainParams{
		Items: []RetainItem{{Content: "Yellow banana smoothie recipe"}},
	}); err != nil {
		t.Fatalf("retain with embedding failed: %v", err)
	}
	// Query has no lexical overlap with "banana", but identical vector [0, 1].
	rec, err := ExecuteRecall(zero, RecallParams{Query: "fruit"})
	if err != nil {
		t.Fatalf("recall with embedding failed: %v", err)
	}
	if !strings.Contains(rec.Content, "Yellow banana smoothie recipe") {
		t.Fatalf("expected vector match for 'fruit' -> banana, got %q", rec.Content)
	}
	if hits < 2 {
		t.Fatalf("expected at least 2 embedding requests, got %d", hits)
	}
}

func TestHindsightClientOutboxRecallReflectAndLearn(t *testing.T) {
	var (
		mu             sync.Mutex
		createdBanks   []string
		retainedBodies []map[string]any
		recallAuth     string
		reflectBody    map[string]any
		failRetain     bool
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/v1/default/banks/"):
			createdBanks = append(createdBanks, r.URL.Path)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/memories"):
			if failRetain {
				http.Error(w, `{"detail":"temporary outage"}`, http.StatusServiceUnavailable)
				return
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			retainedBodies = append(retainedBodies, body)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"accepted":true}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/memories/recall"):
			recallAuth = r.Header.Get("Authorization")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": []map[string]any{
					{"id": "hs-1", "text": "Service uses gRPC on port 9090", "type": "world", "mentioned_at": "2026-10-01"},
				},
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/reflect"):
			_ = json.NewDecoder(r.Body).Decode(&reflectBody)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"text": "The service communicates over gRPC on port 9090.",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	agentDir, cwd := setupMemoryTestEnv(t, map[string]any{
		SettingMemoryBackend:        BackendHindsight,
		SettingHindsightAPIURL:      srv.URL,
		SettingHindsightAPIKey:      "hs-bearer-123",
		SettingHindsightBankID:      "eng",
		SettingHindsightScoping:     "per-project-tagged",
		SettingHindsightGlobalScope: true,
		autolearn.SettingEnabled:    true,
	})
	var zero sdk.Context

	if !IsGlobalMemoryScopeAvailable(zero) {
		t.Fatal("expected hindsight global scope available")
	}

	// Simulate outage on first retain so outbox persists durably on disk.
	mu.Lock()
	failRetain = true
	mu.Unlock()
	res, err := ExecuteRetain(zero, RetainParams{
		Items: []RetainItem{
			{Content: "Project memory item", Context: "ctx"},
			{Content: "Global memory item", Scope: "global"},
		},
	})
	if err != nil {
		t.Fatalf("ExecuteRetain hindsight failed: %v", err)
	}
	if res.Content != "2 memories queued." {
		t.Fatalf("unexpected retain response: %q", res.Content)
	}
	cfg := LoadHindsightConfig(zero)
	scope := ComputeHindsightBankScope(cfg, cwd)
	outboxPath := HindsightOutboxPath(agentDir, cfg.APIURL, scope.BankID)
	if _, err := os.Stat(outboxPath); err != nil {
		t.Fatalf("expected outbox file to persist after retryable failure: %v", err)
	}

	// Recover server and flush outbox; project-tagged and untagged global items
	// must be sent in separate requests (distinct tag sets).
	mu.Lock()
	failRetain = false
	mu.Unlock()
	delivered, kept, err := FlushHindsightOutbox(context.Background(), agentDir, cfg, scope)
	if err != nil || delivered != 2 || kept != 0 {
		t.Fatalf("expected delivered=2 kept=0 err=nil, got delivered=%d kept=%d err=%v", delivered, kept, err)
	}
	mu.Lock()
	numBatches := len(retainedBodies)
	numBanks := len(createdBanks)
	mu.Unlock()
	if numBatches != 2 {
		t.Fatalf("expected 2 separate tag-grouped retain requests, got %d", numBatches)
	}
	if numBanks == 0 {
		t.Fatal("expected CreateBank PUT before retain")
	}

	// Recall & Reflect over HTTP.
	recRes, err := ExecuteRecall(zero, RecallParams{Query: "gRPC port"})
	if err != nil {
		t.Fatalf("hindsight recall failed: %v", err)
	}
	if !strings.Contains(recRes.Content, "Service uses gRPC on port 9090 [world] (2026-10-01)") {
		t.Fatalf("unexpected hindsight recall text: %q", recRes.Content)
	}
	mu.Lock()
	auth := recallAuth
	mu.Unlock()
	if auth != "Bearer hs-bearer-123" {
		t.Fatalf("expected Bearer auth header, got %q", auth)
	}

	refRes, err := ExecuteReflect(zero, ReflectParams{Query: "How does service communicate?", Context: "networking"})
	if err != nil {
		t.Fatalf("hindsight reflect failed: %v", err)
	}
	if refRes.Content != "The service communicates over gRPC on port 9090." {
		t.Fatalf("unexpected reflect text: %q", refRes.Content)
	}

	// Learn queues lesson on Hindsight.
	learnRes, err := ExecuteLearn(zero, LearnParams{Memory: "Always check proto compatibility"})
	if err != nil {
		t.Fatalf("hindsight learn failed: %v", err)
	}
	if learnRes.Content != "Lesson queued for retention." {
		t.Fatalf("unexpected hindsight learn text: %q", learnRes.Content)
	}

	// SSRF check blocks metadata endpoints.
	if err := validateHTTPURL("http://169.254.169.254/latest/meta-data"); err == nil {
		t.Fatal("expected SSRF validation error for link-local metadata IP")
	}
	if err := validateHTTPURL("file:///etc/passwd"); err == nil {
		t.Fatal("expected scheme validation error for file://")
	}
}

func TestLearnManagedSkillCreationAndAuthoredShadowing(t *testing.T) {
	agentDir, _ := setupMemoryTestEnv(t, map[string]any{
		SettingMemoryBackend:     BackendLocal,
		autolearn.SettingEnabled: true,
	})
	var zero sdk.Context

	refreshed := 0
	RefreshSkills = func(sdk.Context) error {
		refreshed++
		return nil
	}

	// Create managed skill alongside lesson.
	res, err := ExecuteLearn(zero, LearnParams{
		Memory: "Use go test -race on internal packages",
		Skill: &LearnSkillPayload{
			Action:      "create",
			Name:        "race-check",
			Description: "Run race detector on Go packages",
			Body:        "1. Run `go test -race ./...`.",
		},
	})
	if err != nil {
		t.Fatalf("ExecuteLearn with skill create failed: %v", err)
	}
	if res.IsError || res.Content != `Lesson stored. Created managed skill "race-check".` {
		t.Fatalf("unexpected skill create result: isError=%v content=%q", res.IsError, res.Content)
	}
	if refreshed != 1 {
		t.Fatalf("expected RefreshSkills called once, got %d", refreshed)
	}
	skillPath := filepath.Join(autolearn.ManagedSkillsDir(agentDir), "race-check", "SKILL.md")
	if _, err := os.Stat(skillPath); err != nil {
		t.Fatalf("expected managed SKILL.md on disk: %v", err)
	}

	// Authored skill collision returns IsError=true with Details{"skill": nil, "shadowed": true}.
	autolearn.IsNameClaimedByAuthoredSkill = func(_ sdk.Context, name, _ string) bool {
		return name == "existing-authored"
	}
	colRes, err := ExecuteLearn(zero, LearnParams{
		Memory: "Another durable insight",
		Skill: &LearnSkillPayload{
			Action:      "create",
			Name:        "existing-authored",
			Description: "Should be blocked",
			Body:        "Body",
		},
	})
	if err != nil {
		t.Fatalf("collision should return ToolResult error, not Go error: %v", err)
	}
	if !colRes.IsError {
		t.Fatal("expected IsError=true on authored skill collision")
	}
	details, _ := colRes.Details.(map[string]any)
	if details["skill"] != nil || details["shadowed"] != true {
		t.Fatalf("expected details {skill: nil, shadowed: true}, got %#v", details)
	}
	if !strings.Contains(colRes.Content, "an authored skill of that name already exists") {
		t.Fatalf("unexpected collision text: %q", colRes.Content)
	}
}

func TestMemoryURLProtocolLocalTraversalAndOffErrors(t *testing.T) {
	agentDir, cwd := setupMemoryTestEnv(t, map[string]any{
		SettingMemoryBackend: BackendOff,
	})
	var zero sdk.Context
	h := MemoryHandler{}

	// Backend off -> informative error.
	if _, err := h.Resolve(urls.Context{SDK: zero, Cwd: cwd}, urls.URL{Href: "memory://root", Scheme: "memory", Host: "root"}); err == nil || !strings.Contains(err.Error(), "off") {
		t.Fatalf("expected informative off error, got %v", err)
	}

	// Backend local -> reads memory_summary.md and subpath, rejects traversal and escaping symlinks.
	setupMemoryTestEnv(t, map[string]any{
		SettingMemoryBackend: BackendLocal,
	})
	agentDir = ResolveAgentDir(zero)
	cwd = ResolveCwd(zero)
	memRoot := MemoryRoot(agentDir, cwd)
	if err := os.MkdirAll(filepath.Join(memRoot, "notes"), 0o755); err != nil {
		t.Fatalf("mkdir memRoot: %v", err)
	}
	if err := os.WriteFile(filepath.Join(memRoot, "memory_summary.md"), []byte("# Summary\nAll good.\n"), 0o644); err != nil {
		t.Fatalf("write summary: %v", err)
	}
	if err := os.WriteFile(filepath.Join(memRoot, "notes", "tip.md"), []byte("Keep it simple."), 0o644); err != nil {
		t.Fatalf("write tip: %v", err)
	}

	res, err := h.Resolve(urls.Context{SDK: zero, Cwd: cwd}, urls.URL{Href: "memory://root", Scheme: "memory", Host: "root"})
	if err != nil || !strings.Contains(res.Content, "All good.") {
		t.Fatalf("memory://root resolve failed: err=%v res=%#v", err, res)
	}
	subRes, err := h.Resolve(urls.Context{SDK: zero, Cwd: cwd}, urls.URL{Href: "memory://root/notes/tip.md", Scheme: "memory", Host: "root", Path: "/notes/tip.md"})
	if err != nil || subRes.Content != "Keep it simple." {
		t.Fatalf("memory://root/notes/tip.md resolve failed: err=%v res=%#v", err, subRes)
	}

	// Path traversal rejected.
	if _, err := h.Resolve(urls.Context{SDK: zero, Cwd: cwd}, urls.URL{Href: "memory://root/../../etc/passwd", Scheme: "memory", Host: "root", Path: "/../../etc/passwd"}); err == nil {
		t.Fatal("expected traversal rejection")
	}

	// Symlink escaping memoryRoot rejected.
	outsideFile := filepath.Join(t.TempDir(), "secret.txt")
	_ = os.WriteFile(outsideFile, []byte("secret"), 0o644)
	escapeLink := filepath.Join(memRoot, "escape.txt")
	if err := os.Symlink(outsideFile, escapeLink); err == nil {
		if _, err := h.Resolve(urls.Context{SDK: zero, Cwd: cwd}, urls.URL{Href: "memory://root/escape.txt", Scheme: "memory", Host: "root", Path: "/escape.txt"}); err == nil {
			t.Fatal("expected symlink escape rejection")
		}
	}

	// Hindsight backend returns corrective unaddressable error for memory://<id>.
	setupMemoryTestEnv(t, map[string]any{
		SettingMemoryBackend:   BackendHindsight,
		SettingHindsightAPIURL: "http://127.0.0.1:8888",
	})
	if _, err := h.Resolve(urls.Context{SDK: zero}, urls.URL{Href: "memory://hs-1", Scheme: "memory", Host: "hs-1"}); err == nil || !strings.Contains(err.Error(), "Hindsight memories are not addressable") {
		t.Fatalf("expected hindsight unaddressable error, got %v", err)
	}
}

func TestMemoryFileLockConcurrencyAndCompactJSON(t *testing.T) {
	setupMemoryTestEnv(t, map[string]any{
		SettingMemoryBackend: BackendMnemopi,
	})
	var zero sdk.Context
	cfg := LoadMnemopiConfig(zero)

	const workers = 12
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := range workers {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := ExecuteRetain(zero, RetainParams{
				Items: []RetainItem{{Content: fmt.Sprintf("concurrent-item-%d", idx)}},
			})
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent retain failed: %v", err)
	}

	bankPath := cfg.BankFilePath(cfg.RetainBank)
	raw, err := os.ReadFile(bankPath)
	if err != nil {
		t.Fatalf("read bank file: %v", err)
	}
	// Compact JSON: single line ending with '\n', no pretty-printed indentation.
	trimmed := strings.TrimSuffix(string(raw), "\n")
	if strings.Contains(trimmed, "\n") {
		t.Fatalf("expected compact single-line JSON for mnemopi bank, got multiline:\n%s", trimmed)
	}
	var bf mnemopiBankFile
	if err := json.Unmarshal(raw, &bf); err != nil {
		t.Fatalf("unmarshal bank: %v", err)
	}
	if len(bf.Records) != workers {
		t.Fatalf("expected %d retained records under concurrent file locks, got %d", workers, len(bf.Records))
	}
}

func TestHindsightAndEmbeddingRedirectSSRFBlocked(t *testing.T) {
	redirSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data", http.StatusFound)
	}))
	defer redirSrv.Close()

	// Hindsight client must reject redirect to metadata IP.
	cfg := HindsightConfig{
		APIURL:           redirSrv.URL,
		RequestTimeoutMs: 5000,
		RecallTimeoutMs:  5000,
	}
	client, err := NewHindsightClient(cfg)
	if err != nil {
		t.Fatalf("NewHindsightClient: %v", err)
	}
	if _, err := client.Recall(context.Background(), "bank", "query", cfg, HindsightBankScope{}); err == nil || !strings.Contains(err.Error(), "redirect blocked") {
		t.Fatalf("expected hindsight redirect SSRF block, got %v", err)
	}

	// Mnemopi embedding client must also reject redirect to metadata IP.
	mcfg := MnemopiConfig{
		EmbeddingAPIURL: redirSrv.URL,
		EmbeddingModel:  "test-emb",
	}
	if _, err := fetchEmbedding(context.Background(), mcfg, "hello"); err == nil || !strings.Contains(err.Error(), "redirect blocked") {
		t.Fatalf("expected embedding redirect SSRF block, got %v", err)
	}
}

func TestHindsightOutboxOversizedLineResilienceAndConcurrentEnqueueDuringFlush(t *testing.T) {
	// 1) Oversized line in outbox file does not wedge reading valid subsequent lines.
	dir := t.TempDir()
	outboxPath := filepath.Join(dir, "outbox-test.jsonl")
	validEv := HindsightOutboxEvent{
		Version: 1,
		ID:      "ev-after-giant",
		Scope:   "project",
		Content: "still readable",
		Context: "omp",
	}
	validJSON, _ := json.Marshal(validEv)
	giantLine := strings.Repeat("A", hindsightMaxOutboxLineBytes+1024)
	content := giantLine + "\n" + string(validJSON) + "\n"
	if err := os.WriteFile(outboxPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write outbox: %v", err)
	}
	events, err := readOutboxEventsLocked(outboxPath)
	if err != nil {
		t.Fatalf("readOutboxEventsLocked failed on oversized line: %v", err)
	}
	if len(events) != 1 || events[0].ID != "ev-after-giant" {
		t.Fatalf("expected 1 valid event after oversized line, got %+v", events)
	}

	// 2) Concurrent EnqueueHindsightRetain during FlushHindsightOutbox HTTP call is
	// not blocked by the lock and is preserved in the outbox after flush.
	inHTTP := make(chan struct{})
	releaseHTTP := make(chan struct{})
	var firstCall sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/memories") {
			firstCall.Do(func() {
				close(inHTTP)
				<-releaseHTTP
			})
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	agentDir := t.TempDir()
	cfg := HindsightConfig{
		APIURL:           srv.URL,
		RetainContext:    "omp",
		RequestTimeoutMs: 5000,
		RetainTimeoutMs:  5000,
	}
	scope := HindsightBankScope{BankID: "b1"}
	if _, err := EnqueueHindsightRetain(agentDir, "s1", "c1", cfg, scope, []HindsightRetainInput{{Content: "first"}}); err != nil {
		t.Fatalf("enqueue first: %v", err)
	}

	flushDone := make(chan error, 1)
	var delivered, kept int
	go func() {
		var err error
		delivered, kept, err = FlushHindsightOutbox(context.Background(), agentDir, cfg, scope)
		flushDone <- err
	}()

	<-inHTTP
	// While HTTP is in-flight, enqueue a second item — must succeed without deadlock.
	if _, err := EnqueueHindsightRetain(agentDir, "s1", "c2", cfg, scope, []HindsightRetainInput{{Content: "second-during-flush"}}); err != nil {
		t.Fatalf("concurrent enqueue during flush failed: %v", err)
	}
	close(releaseHTTP)

	if err := <-flushDone; err != nil {
		t.Fatalf("FlushHindsightOutbox: %v", err)
	}
	if delivered != 1 || kept != 1 {
		t.Fatalf("expected delivered=1 kept=1 (concurrent item preserved), got delivered=%d kept=%d", delivered, kept)
	}
}
