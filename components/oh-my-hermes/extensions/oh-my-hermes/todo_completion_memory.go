package ohmyhermes

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/events"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/memory"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/todo"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/usage"
)

const (
	CompletionContractVersion = "native_completion/v1"
	MemoryNotesContract       = "omh_episodic_note/v1"
)

// --- Unified OMH Todo Metadata ---

// TodoMetadata holds OMH-specific plan metadata layered on top of todo.Default().
type TodoMetadata struct {
	SessionID     string `json:"session_id"`
	Title         string `json:"title,omitempty"`
	PlanStage     string `json:"plan_stage,omitempty"` // awaiting_acceptance, accepted, executing, completed
	BlockedReason string `json:"blocked_reason,omitempty"`
	UpdatedAt     string `json:"updated_at,omitempty"`
}

type todoMetaStore struct {
	mu   sync.RWMutex
	meta map[string]TodoMetadata
}

func newTodoMetaStore() *todoMetaStore {
	return &todoMetaStore{meta: make(map[string]TodoMetadata)}
}

func (s *todoMetaStore) get(sessionID string) TodoMetadata {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.meta[sessionID]
	if !ok {
		return TodoMetadata{SessionID: sessionID}
	}
	return m
}

func (s *todoMetaStore) update(sessionID string, fn func(*TodoMetadata)) TodoMetadata {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.meta[sessionID]
	m.SessionID = sessionID
	fn(&m)
	m.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	s.meta[sessionID] = m
	return m
}

// --- Evidence-Boundary Completion Dossier ---

// ObservedCommandProof records an actually executed verification command and its exit code.
type ObservedCommandProof struct {
	Command  string `json:"command"`
	ExitCode int    `json:"exit_code"`
	Summary  string `json:"summary,omitempty"`
}

// CompletionRecord enforces OMH's prepared-vs-observed evidence boundary.
type CompletionRecord struct {
	Schema           string                 `json:"schema"`
	RecordID         string                 `json:"record_id"`
	SessionID        string                 `json:"session_id,omitempty"`
	Task             string                 `json:"task"`
	PreparedEvidence []string               `json:"prepared_evidence,omitempty"`
	ObservedEvidence []ObservedCommandProof `json:"observed_evidence,omitempty"`
	Verified         bool                   `json:"verified"`
	VerificationNote string                 `json:"verification_note,omitempty"`
	CreatedAt        string                 `json:"created_at"`
}

// CompletionFile is persisted at $OMH_HOME/runtime/completion/records.json.
type CompletionFile struct {
	Schema  string             `json:"schema"`
	Records []CompletionRecord `json:"records"`
}

// CompletionStore manages completion dossiers atomically.
type CompletionStore struct {
	mu    sync.RWMutex
	paths func() OMHPaths
}

func NewCompletionStore(pathsFn func() OMHPaths) *CompletionStore {
	if pathsFn == nil {
		pathsFn = func() OMHPaths { return ResolvePaths("") }
	}
	return &CompletionStore{paths: pathsFn}
}

func (s *CompletionStore) loadLocked() (CompletionFile, error) {
	doc := CompletionFile{Schema: CompletionContractVersion}
	exists, err := readJSON(s.paths().CompletionRecords, &doc)
	if err != nil {
		return doc, err
	}
	if !exists {
		doc.Schema = CompletionContractVersion
	}
	return doc, nil
}

// EvaluateCompletion checks OMH's evidence boundary: a record is verified iff it has
// at least one observed command proof and every observed command exited with code 0.
func EvaluateCompletion(rec *CompletionRecord) (bool, string) {
	if rec == nil {
		return false, "nil completion record"
	}
	if len(rec.ObservedEvidence) == 0 {
		return false, "unverified: prepared evidence alone cannot certify completion; at least one observed command proof with exit_code=0 is required"
	}
	for _, proof := range rec.ObservedEvidence {
		if strings.TrimSpace(proof.Command) == "" {
			return false, "unverified: observed evidence entry missing command"
		}
		if proof.ExitCode != 0 {
			return false, fmt.Sprintf("unverified: observed command %q failed with exit_code=%d", proof.Command, proof.ExitCode)
		}
	}
	return true, fmt.Sprintf("verified with %d passing observed command proof(s)", len(rec.ObservedEvidence))
}

// Record stores a new CompletionRecord and evaluates its evidence boundary.
func (s *CompletionStore) Record(sessionID, recordID, task string, prepared []string, observed []ObservedCommandProof) (CompletionRecord, error) {
	if strings.TrimSpace(task) == "" {
		return CompletionRecord{}, fmt.Errorf("task is required")
	}
	if strings.TrimSpace(recordID) == "" {
		recordID = newShortID("comp")
	}
	id, err := sanitizeID(recordID)
	if err != nil {
		return CompletionRecord{}, err
	}
	rec := CompletionRecord{
		Schema:           CompletionContractVersion,
		RecordID:         id,
		SessionID:        sessionID,
		Task:             strings.TrimSpace(task),
		PreparedEvidence: prepared,
		ObservedEvidence: observed,
		CreatedAt:        time.Now().UTC().Format(time.RFC3339),
	}
	rec.Verified, rec.VerificationNote = EvaluateCompletion(&rec)

	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.loadLocked()
	if err != nil {
		return CompletionRecord{}, err
	}
	replaced := false
	for i := range doc.Records {
		if doc.Records[i].RecordID == id {
			doc.Records[i] = rec
			replaced = true
			break
		}
	}
	if !replaced {
		doc.Records = append(doc.Records, rec)
	}
	if err := atomicWriteJSON(s.paths().CompletionRecords, doc); err != nil {
		return CompletionRecord{}, err
	}
	return rec, nil
}

// Get retrieves a completion record by ID.
func (s *CompletionStore) Get(recordID string) (CompletionRecord, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	doc, err := s.loadLocked()
	if err != nil {
		return CompletionRecord{}, false, err
	}
	for _, r := range doc.Records {
		if r.RecordID == recordID {
			return r, true, nil
		}
	}
	return CompletionRecord{}, false, nil
}

// List returns all completion records.
func (s *CompletionStore) List() ([]CompletionRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	doc, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	out := make([]CompletionRecord, len(doc.Records))
	copy(out, doc.Records)
	return out, nil
}

// --- OmhMemoryProvider (implements pig-hermes-core/memory.Provider) ---

// EpisodicNote is one JSONL line in $OMH_HOME/memory/notes.jsonl.
type EpisodicNote struct {
	Schema    string   `json:"schema"`
	NoteID    string   `json:"note_id"`
	SessionID string   `json:"session_id,omitempty"`
	Kind      string   `json:"kind"` // remember, memory_write, pre_compress, session_end
	Content   string   `json:"content"`
	Tags      []string `json:"tags,omitempty"`
	CreatedAt string   `json:"created_at"`
}

// OmhMemoryProvider implements pig-hermes-core/memory.Provider and persists
// OMH episodic notes under $OMH_HOME/memory/notes.jsonl.
type OmhMemoryProvider struct {
	mu    sync.RWMutex
	paths func() OMHPaths
}

// NewOmhMemoryProvider creates an OmhMemoryProvider backed by pathsFn.
func NewOmhMemoryProvider(pathsFn func() OMHPaths) *OmhMemoryProvider {
	if pathsFn == nil {
		pathsFn = func() OMHPaths { return ResolvePaths("") }
	}
	return &OmhMemoryProvider{paths: pathsFn}
}

// Name returns the provider identifier ("omh").
func (p *OmhMemoryProvider) Name() string {
	return "omh"
}

// Remember appends an episodic note to $OMH_HOME/memory/notes.jsonl.
func (p *OmhMemoryProvider) Remember(sessionID, kind, content string, tags []string) (EpisodicNote, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return EpisodicNote{}, fmt.Errorf("note content is required")
	}
	if strings.TrimSpace(kind) == "" {
		kind = "remember"
	}
	note := EpisodicNote{
		Schema:    MemoryNotesContract,
		NoteID:    newShortID("note"),
		SessionID: sessionID,
		Kind:      kind,
		Content:   content,
		Tags:      tags,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := appendJSONL(p.paths().MemoryNotesFile, note); err != nil {
		return EpisodicNote{}, err
	}
	return note, nil
}

// Recall reads episodic notes from $OMH_HOME/memory/notes.jsonl and ranks matches for query.
func (p *OmhMemoryProvider) Recall(query string, limit int) ([]EpisodicNote, error) {
	if limit <= 0 {
		limit = 5
	}
	p.mu.RLock()
	path := p.paths().MemoryNotesFile
	p.mu.RUnlock()

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var all []EpisodicNote
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var n EpisodicNote
		if json.Unmarshal([]byte(line), &n) == nil && n.Content != "" {
			all = append(all, n)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(all) == 0 {
		return nil, nil
	}

	tokens := tokenizeQuery(query)
	if len(tokens) == 0 {
		// Return most recent notes.
		if len(all) > limit {
			all = all[len(all)-limit:]
		}
		// Reverse to newest-first.
		for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
			all[i], all[j] = all[j], all[i]
		}
		return all, nil
	}

	type scored struct {
		note  EpisodicNote
		score int
		idx   int
	}
	var hits []scored
	for i, n := range all {
		hay := strings.ToLower(n.Content + " " + strings.Join(n.Tags, " "))
		sc := 0
		for _, tok := range tokens {
			if strings.Contains(hay, tok) {
				sc++
			}
		}
		if sc > 0 {
			hits = append(hits, scored{note: n, score: sc, idx: i})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].idx > hits[j].idx
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]EpisodicNote, len(hits))
	for i, h := range hits {
		out[i] = h.note
	}
	return out, nil
}

// Prefetch returns a compact recall block for query (implements memory.Provider),
// merging episodic notes from notes.jsonl and structured records from records.json
// deduplicated by normalized summary.
func (p *OmhMemoryProvider) Prefetch(ctx context.Context, sessionID, query string) (string, error) {
	notes, notesErr := p.Recall(query, 5)
	recStore := newOMHMemoryRecordStore(p.paths)
	recs, recsErr := recStore.recall(query, 5)
	if notesErr != nil && recsErr != nil {
		return "", notesErr
	}

	type recallEntry struct {
		tags []string
		text string
	}
	seen := make(map[string]bool)
	var entries []recallEntry

	addEntry := func(tags []string, text string) {
		clean := strings.TrimSpace(text)
		if clean == "" {
			return
		}
		norm := strings.ToLower(strings.Join(strings.Fields(clean), " "))
		if seen[norm] {
			return
		}
		seen[norm] = true
		entries = append(entries, recallEntry{tags: tags, text: clean})
	}

	for _, n := range notes {
		addEntry(n.Tags, n.Content)
	}
	for _, r := range recs {
		tags := append([]string{}, r.Tags...)
		if r.RecordType != "" {
			tags = append([]string{r.RecordType}, tags...)
		}
		addEntry(dedupeStrings(tags), r.Summary)
	}
	if len(entries) == 0 {
		return "", nil
	}
	if len(entries) > 8 {
		entries = entries[:8]
	}

	var b strings.Builder
	b.WriteString("[OMH Episodic Recall]\n")
	for _, e := range entries {
		b.WriteString("- ")
		if len(e.tags) > 0 {
			b.WriteString("[")
			b.WriteString(strings.Join(e.tags, ","))
			b.WriteString("] ")
		}
		b.WriteString(e.text)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// OnPreCompress records a pre-compression checkpoint note (implements memory.Provider).
func (p *OmhMemoryProvider) OnPreCompress(ctx context.Context, sessionID string) error {
	_, err := p.Remember(sessionID, "pre_compress", "Context compression checkpoint recorded for session "+sessionID, []string{"lifecycle", "compress"})
	return err
}

// OnMemoryWrite records provenance when MEMORY.md or USER.md changes (implements memory.Provider).
func (p *OmhMemoryProvider) OnMemoryWrite(ctx context.Context, target memory.Target, action, content string) error {
	snippet := strings.TrimSpace(content)
	if len(snippet) > 200 {
		snippet = snippet[:200] + "..."
	}
	msg := fmt.Sprintf("memory_write target=%s action=%s: %s", target, action, snippet)
	_, err := p.Remember("", "memory_write", msg, []string{"provenance", string(target)})
	return err
}

// OnSessionEnd records a session-end consolidation note (implements memory.Provider).
func (p *OmhMemoryProvider) OnSessionEnd(ctx context.Context, sessionID string) error {
	_, err := p.Remember(sessionID, "session_end", "Session ended: "+sessionID, []string{"lifecycle", "session_end"})
	return err
}

func tokenizeQuery(q string) []string {
	fields := strings.Fields(strings.ToLower(q))
	var out []string
	for _, f := range fields {
		f = strings.Trim(f, ".,:;!?\"'()[]{}")
		if len(f) >= 2 {
			out = append(out, f)
		}
	}
	return out
}

// --- Tool Registration ---

func registerTodoCompletionMemoryRoleTools(
	ext *sdk.Extension,
	dags *DAGStore,
	teams *TeamStore,
	completions *CompletionStore,
	identities *IdentityStore,
	memProvider *OmhMemoryProvider,
	todoMeta *todoMetaStore,
) {
	if todoMeta == nil {
		todoMeta = newTodoMetaStore()
	}
	// Observe native todo_list updates and memory writes via EventBus.
	_, _ = ext.Events().On(events.ChannelTodoUpdated, func(ctx sdk.Context, data any) error {
		if m, ok := data.(map[string]any); ok {
			if sid, _ := m["sessionId"].(string); sid != "" {
				todoMeta.update(sid, func(tm *TodoMetadata) {})
			}
		}
		return nil
	})
	_, _ = ext.Events().On(events.ChannelMemoryWrite, func(ctx sdk.Context, data any) error {
		var payload events.MemoryWritePayload
		if raw, err := json.Marshal(data); err == nil {
			if json.Unmarshal(raw, &payload) == nil && payload.Target != "" {
				_ = memProvider.OnMemoryWrite(context.Background(), memory.Target(payload.Target), payload.Action, payload.EntrySHA256)
			}
		}
		return nil
	})

	// 4. omh_todo (unified with todo.Default())
	ext.RegisterTool(sdk.ToolDefinition{
		Name:          "omh_todo",
		Label:         "OMH Todo",
		Description:   "Manage the unified session todo checklist (shared with pig-hermes todo_list) plus OMH plan_stage and blocked_reason metadata.",
		PromptSnippet: "Manage unified session todos and plan_stage with omh_todo (show, set, advance, clear).",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"action":         map[string]any{"type": "string", "enum": []string{"show", "set", "advance", "clear"}},
				"title":          map[string]any{"type": "string"},
				"plan_stage":     map[string]any{"type": "string", "enum": []string{"awaiting_acceptance", "accepted", "executing", "completed"}},
				"blocked_reason": map[string]any{"type": "string"},
				"item_id":        map[string]any{"type": "string"},
				"status":         map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "completed", "cancelled"}},
				"items": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"id":      map[string]any{"type": "string"},
							"content": map[string]any{"type": "string"},
							"status":  map[string]any{"type": "string"},
						},
						"required": []string{"id", "content"},
					},
				},
			},
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			sessionID := resolveSessionID(ctx)
			action := strings.ToLower(paramString(params, "action"))
			if action == "" {
				action = "show"
			}
			store := todo.Default()

			switch action {
			case "show":
				items := store.Get(sessionID)
				meta := todoMeta.get(sessionID)
				return jsonToolResult(map[string]any{
					"ok":             true,
					"session_id":     sessionID,
					"todos":          items,
					"count":          len(items),
					"summary":        todo.FormatSummary(items),
					"title":          meta.Title,
					"plan_stage":     meta.PlanStage,
					"blocked_reason": meta.BlockedReason,
				}, false), nil

			case "set":
				meta := todoMeta.update(sessionID, func(tm *TodoMetadata) {
					if t := paramString(params, "title"); t != "" {
						tm.Title = t
					}
					if ps := paramString(params, "plan_stage"); ps != "" {
						tm.PlanStage = ps
					}
					if _, has := params["blocked_reason"]; has {
						tm.BlockedReason = paramString(params, "blocked_reason")
					}
				})
				var items []todo.Item
				if rawItems, ok := params["items"]; ok && rawItems != nil {
					parsed, err := parseOMHTodoItems(rawItems)
					if err != nil {
						return errorToolResult(err.Error()), nil
					}
					updated, err := store.Replace(sessionID, parsed)
					if err != nil {
						return errorToolResult(err.Error()), nil
					}
					items = updated
					_ = ctx.Events().Emit(events.ChannelTodoUpdated, map[string]any{
						"sessionId": sessionID,
						"source":    "omh_todo",
						"todos":     items,
						"count":     len(items),
					})
				} else {
					items = store.Get(sessionID)
				}
				return jsonToolResult(map[string]any{
					"ok":             true,
					"session_id":     sessionID,
					"todos":          items,
					"count":          len(items),
					"summary":        todo.FormatSummary(items),
					"title":          meta.Title,
					"plan_stage":     meta.PlanStage,
					"blocked_reason": meta.BlockedReason,
				}, false), nil

			case "advance":
				itemID := paramString(params, "item_id")
				newStatus := paramString(params, "status")
				if newStatus == "" {
					newStatus = "completed"
				}
				items := store.Get(sessionID)
				found := false
				for i := range items {
					if items[i].ID == itemID {
						items[i].Status = newStatus
						found = true
						break
					}
				}
				if !found {
					return errorToolResult(fmt.Sprintf("todo item %q not found", itemID)), nil
				}
				updated, err := store.Replace(sessionID, items)
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				meta := todoMeta.update(sessionID, func(tm *TodoMetadata) {
					if ps := paramString(params, "plan_stage"); ps != "" {
						tm.PlanStage = ps
					}
					if _, has := params["blocked_reason"]; has {
						tm.BlockedReason = paramString(params, "blocked_reason")
					}
				})
				_ = ctx.Events().Emit(events.ChannelTodoUpdated, map[string]any{
					"sessionId": sessionID,
					"source":    "omh_todo",
					"todos":     updated,
					"count":     len(updated),
				})
				return jsonToolResult(map[string]any{
					"ok":             true,
					"session_id":     sessionID,
					"todos":          updated,
					"count":          len(updated),
					"summary":        todo.FormatSummary(updated),
					"plan_stage":     meta.PlanStage,
					"blocked_reason": meta.BlockedReason,
				}, false), nil

			case "clear":
				updated, err := store.Replace(sessionID, nil)
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				todoMeta.update(sessionID, func(tm *TodoMetadata) {
					tm.Title = ""
					tm.PlanStage = ""
					tm.BlockedReason = ""
				})
				_ = ctx.Events().Emit(events.ChannelTodoUpdated, map[string]any{
					"sessionId": sessionID,
					"source":    "omh_todo",
					"todos":     updated,
					"count":     0,
				})
				return jsonToolResult(map[string]any{"ok": true, "session_id": sessionID, "count": 0}, false), nil

			default:
				return errorToolResult(fmt.Sprintf("unsupported omh_todo action %q", action)), nil
			}
		},
	})

	// 5. omh_completion
	ext.RegisterTool(sdk.ToolDefinition{
		Name:          "omh_completion",
		Label:         "OMH Completion",
		Description:   "Record and verify OMH evidence-boundary completion dossiers separating prepared evidence from observed command/exit proof.",
		PromptSnippet: "Record and verify completion dossiers with omh_completion (record, get, list, verify).",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"action":            map[string]any{"type": "string", "enum": []string{"record", "get", "list", "verify"}},
				"record_id":         map[string]any{"type": "string"},
				"task":              map[string]any{"type": "string"},
				"prepared_evidence": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"observed_evidence": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"command":   map[string]any{"type": "string"},
							"exit_code": map[string]any{"type": "integer"},
							"summary":   map[string]any{"type": "string"},
						},
						"required": []string{"command", "exit_code"},
					},
				},
			},
			"required": []string{"action"},
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			action := strings.ToLower(paramString(params, "action"))
			recID := paramString(params, "record_id")
			switch action {
			case "record":
				prepared := paramStringSlice(params, "prepared_evidence")
				observed := parseObservedProofs(params["observed_evidence"])
				rec, err := completions.Record(resolveSessionID(ctx), recID, paramString(params, "task"), prepared, observed)
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				return jsonToolResult(map[string]any{"ok": true, "record": rec}, false), nil
			case "get":
				rec, ok, err := completions.Get(recID)
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				if !ok {
					return errorToolResult(fmt.Sprintf("completion record %q not found", recID)), nil
				}
				return jsonToolResult(map[string]any{"ok": true, "record": rec}, false), nil
			case "list":
				list, err := completions.List()
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				return jsonToolResult(map[string]any{"ok": true, "records": list, "count": len(list)}, false), nil
			case "verify":
				rec, ok, err := completions.Get(recID)
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				if !ok {
					return errorToolResult(fmt.Sprintf("completion record %q not found", recID)), nil
				}
				verified, note := EvaluateCompletion(&rec)
				return jsonToolResult(map[string]any{
					"ok":        true,
					"record_id": rec.RecordID,
					"verified":  verified,
					"note":      note,
				}, false), nil
			default:
				return errorToolResult(fmt.Sprintf("unsupported omh_completion action %q", action)), nil
			}
		},
	})

	// 6. omh_run_summary
	ext.RegisterTool(sdk.ToolDefinition{
		Name:          "omh_run_summary",
		Label:         "OMH Run Summary",
		Description:   "Aggregate session and child token/cost usage from usage.Default() together with OMH delegation ledger, DAG, and team counts.",
		PromptSnippet: "Summarize session usage, delegations, DAGs, and teams with omh_run_summary.",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"session_id": map[string]any{"type": "string"},
			},
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			inTok, outTok, cost, turns, toolCalls := usage.Default().Totals()
			sessions := usage.Default().Snapshot()
			delegations := teams.Delegations()
			dagList, _ := dags.List()
			teamList, _ := teams.List()

			activeDAGs := 0
			for _, d := range dagList {
				if d.Status == "active" {
					activeDAGs++
				}
			}
			activeTeams := 0
			for _, t := range teamList {
				if t.Status == "active" {
					activeTeams++
				}
			}

			return jsonToolResult(map[string]any{
				"ok": true,
				"usage": map[string]any{
					"input_tokens":  inTok,
					"output_tokens": outTok,
					"total_tokens":  inTok + outTok,
					"cost_usd":      cost,
					"turns":         turns,
					"tool_calls":    toolCalls,
					"sessions":      len(sessions),
				},
				"delegations": map[string]any{
					"count":   len(delegations),
					"records": delegations,
				},
				"dags": map[string]any{
					"total":  len(dagList),
					"active": activeDAGs,
				},
				"teams": map[string]any{
					"total":  len(teamList),
					"active": activeTeams,
				},
			}, false), nil
		},
	})

	// 7. omh_remember
	ext.RegisterTool(sdk.ToolDefinition{
		Name:          "omh_remember",
		Label:         "OMH Remember",
		Description:   "Save or recall OMH episodic notes persisted under $OMH_HOME/memory/notes.jsonl and exposed via OmhMemoryProvider.",
		PromptSnippet: "Save or recall OMH episodic notes with omh_remember (save, recall).",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"action":  map[string]any{"type": "string", "enum": []string{"save", "recall"}},
				"content": map[string]any{"type": "string"},
				"query":   map[string]any{"type": "string"},
				"tags":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"limit":   map[string]any{"type": "integer"},
			},
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			action := strings.ToLower(paramString(params, "action"))
			if action == "" {
				if paramString(params, "content") != "" {
					action = "save"
				} else {
					action = "recall"
				}
			}
			switch action {
			case "save":
				note, err := memProvider.Remember(resolveSessionID(ctx), "remember", paramString(params, "content"), paramStringSlice(params, "tags"))
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				return jsonToolResult(map[string]any{"ok": true, "note": note}, false), nil
			case "recall":
				notes, err := memProvider.Recall(paramString(params, "query"), paramInt(params, "limit", 5))
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				return jsonToolResult(map[string]any{"ok": true, "notes": notes, "count": len(notes)}, false), nil
			default:
				return errorToolResult(fmt.Sprintf("unsupported omh_remember action %q", action)), nil
			}
		},
	})

	// 8. omh_roles + omh_role (upstream canonical singular alias)
	rolesSchema := sdk.Schema{
		"type": "object",
		"properties": map[string]any{
			"action":   map[string]any{"type": "string", "enum": []string{"list", "get", "set_identity"}},
			"role":     map[string]any{"type": "string"},
			"name":     map[string]any{"type": "string"},
			"category": map[string]any{"type": "string"},
			"prompt":   map[string]any{"type": "string"},
			"model_chain": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"model_id":         map[string]any{"type": "string"},
						"reasoning_effort": map[string]any{"type": "string"},
					},
					"required": []string{"model_id"},
				},
			},
		},
		"required": []string{"action"},
	}
	rolesExecute := func(ctx sdk.Context, params map[string]any) (any, error) {
		action := strings.ToLower(paramString(params, "action"))
		switch action {
		case "list":
			customs, err := identities.List()
			if err != nil {
				return errorToolResult(err.Error()), nil
			}
			return jsonToolResult(map[string]any{
				"ok":         true,
				"schema":     RoleContractVersion,
				"roles":      BuiltinRoles(),
				"identities": customs,
			}, false), nil
		case "get":
			target := paramString(params, "role")
			if target == "" {
				target = paramString(params, "name")
			}
			if r, ok := LookupBuiltinRole(target); ok {
				return jsonToolResult(map[string]any{"ok": true, "kind": "builtin", "role": r}, false), nil
			}
			if prof, ok, err := identities.Get(target); err == nil && ok {
				return jsonToolResult(map[string]any{"ok": true, "kind": "custom", "identity": prof}, false), nil
			} else if err != nil {
				return errorToolResult(err.Error()), nil
			}
			return errorToolResult(fmt.Sprintf("role or identity %q not found", target)), nil
		case "set_identity":
			prof := IdentityProfile{
				Name:       paramString(params, "name"),
				Role:       paramString(params, "role"),
				Category:   paramString(params, "category"),
				Prompt:     paramString(params, "prompt"),
				ModelChain: parseIdentityModelChain(params["model_chain"]),
			}
			saved, err := identities.Set(prof)
			if err != nil {
				return errorToolResult(err.Error()), nil
			}
			return jsonToolResult(map[string]any{"ok": true, "identity": saved}, false), nil
		default:
			return errorToolResult(fmt.Sprintf("unsupported omh_roles action %q", action)), nil
		}
	}
	ext.RegisterTool(sdk.ToolDefinition{
		Name:          "omh_roles",
		Label:         "OMH Roles",
		Description:   "Inspect the 9 built-in OMH roles and manage custom identity profiles persisted under $OMH_HOME/routing/identities.json.",
		PromptSnippet: "Inspect OMH roles and custom identity profiles with omh_roles (list, get, set_identity).",
		Parameters:    rolesSchema,
		Execute:       rolesExecute,
	})
	ext.RegisterTool(sdk.ToolDefinition{
		Name:          "omh_role",
		Label:         "OMH Role",
		Description:   "Canonical singular alias for omh_roles: inspect the 9 built-in OMH roles and manage custom identity profiles.",
		PromptSnippet: "Inspect OMH roles and custom identity profiles with omh_role (list, get, set_identity).",
		Parameters:    rolesSchema,
		Execute:       rolesExecute,
	})
}

func parseOMHTodoItems(raw any) ([]todo.Item, error) {
	arr, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("items must be an array")
	}
	out := make([]todo.Item, 0, len(arr))
	for i, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("items[%d] must be an object", i)
		}
		id := paramString(m, "id")
		content := paramString(m, "content")
		status := paramString(m, "status")
		if status == "" {
			status = "pending"
		}
		out = append(out, todo.Item{ID: id, Content: content, Status: status})
	}
	return out, nil
}

func parseObservedProofs(raw any) []ObservedCommandProof {
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]ObservedCommandProof, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, ObservedCommandProof{
			Command:  paramString(m, "command"),
			ExitCode: paramInt(m, "exit_code", -1),
			Summary:  paramString(m, "summary"),
		})
	}
	return out
}

func parseIdentityModelChain(raw any) []IdentityModelEntry {
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]IdentityModelEntry, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, IdentityModelEntry{
			ModelID:         paramString(m, "model_id"),
			ReasoningEffort: paramString(m, "reasoning_effort"),
		})
	}
	return out
}
