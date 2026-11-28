package memory

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// upstream: packages/coding-agent/src/mnemopi/config.ts:1-310
// upstream: packages/coding-agent/src/mnemopi/state.ts:156-664,1212-1452
// upstream: packages/coding-agent/src/tools/memory-{retain,recall,reflect,edit}.ts
//
// Adaptation: uses a zero-dependency pure-Go per-bank atomic JSON store
// (<dbDir>/banks/<bank>.json) instead of bun:sqlite + ONNX embeddings,
// with deterministic BM25 + recency + importance scoring and optional
// OpenAI-compatible HTTP embeddings when `mnemopi.embeddingApiUrl` is set.

const (
	defaultMnemopiSharedBank    = "default"
	defaultMnemopiRecallLimit   = 8
	defaultMnemopiInjectLimit   = 5000
	defaultRecallPreviewRunes   = 600
	mnemopiEmbeddingTimeout     = 10 * time.Second
	mnemopiMaxEmbeddingRespSize = 4 * 1024 * 1024
)

// MnemopiStoreKind identifies which Mnemopi table/store a record belongs to.
// upstream: packages/coding-agent/src/mnemopi/state.ts:173
type MnemopiStoreKind string

const (
	StoreWorking  MnemopiStoreKind = "working"
	StoreEpisodic MnemopiStoreKind = "episodic"
	StoreFact     MnemopiStoreKind = "fact"
)

// MnemopiRecord is one persisted memory row in a Mnemopi bank JSON file.
// upstream: packages/coding-agent/src/mnemopi/state.ts:175-215
type MnemopiRecord struct {
	ID           string         `json:"id"`
	Content      string         `json:"content"`
	Store        string         `json:"memory_store"` // "working" | "episodic" | "fact"
	MemoryType   string         `json:"memory_type,omitempty"`
	Source       string         `json:"source,omitempty"`
	Timestamp    string         `json:"timestamp"`
	CreatedAt    string         `json:"created_at"`
	Importance   float64        `json:"importance"`
	Veracity     string         `json:"veracity,omitempty"`
	SessionID    string         `json:"session_id,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	ValidUntil   string         `json:"valid_until,omitempty"`
	SupersededBy string         `json:"superseded_by,omitempty"`
	Embedding    []float64      `json:"embedding,omitempty"`
}

type mnemopiBankFile struct {
	Version int             `json:"version"`
	Bank    string          `json:"bank"`
	Records []MnemopiRecord `json:"records"`
}

// MnemopiConfig holds the resolved runtime configuration for Mnemopi.
// upstream: packages/coding-agent/src/mnemopi/config.ts:46-70
type MnemopiConfig struct {
	DBDir           string
	BaseBank        string
	Bank            string
	GlobalBank      string
	RetainBank      string
	RecallBanks     []string
	Scoping         string // "per-project" | "per-project-tagged" | "global"
	RecallLimit     int
	InjectionLimit  int
	NoEmbeddings    bool
	EmbeddingModel  string
	EmbeddingAPIURL string
	EmbeddingAPIKey string
	EmbeddingPrefix string
}

// MnemopiRecallResult is one scored recall hit returned by MnemopiRecall.
type MnemopiRecallResult struct {
	ID         string
	Bank       string
	Store      MnemopiStoreKind
	Content    string // possibly clipped preview
	FullLength int
	Truncated  bool
	Source     string
	Timestamp  string
	Importance float64
	Score      float64
}

// MnemopiScopedMemoryHit is the full record returned by GetScopedMemory (backs `memory://<id>`).
// upstream: packages/coding-agent/src/mnemopi/state.ts:198-215
type MnemopiScopedMemoryHit struct {
	Bank        string
	Store       MnemopiStoreKind
	Invalidated bool
	Row         MnemopiRecord
}

// MnemopiEditResult is returned by EditScopedMemory (`memory_edit`).
// upstream: packages/coding-agent/src/mnemopi/state.ts:164-168
type MnemopiEditResult struct {
	Status string `json:"status"` // "updated" | "deleted" | "invalidated" | "not_found" | "not_editable"
	Bank   string `json:"bank,omitempty"`
	Store  string `json:"store,omitempty"`
}

// MnemopiEditOptions carries optional fields for `memory_edit`.
type MnemopiEditOptions struct {
	Content       *string
	Importance    *float64
	ReplacementID string
}

var (
	bankNameUnsafeRE = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)
	bankLockMu       sync.Mutex
	bankLocks        = map[string]*localLockEntry{}
)

func withBankLock(bankPath string, fn func() error) error {
	key := filepath.Clean(bankPath)
	bankLockMu.Lock()
	entry := bankLocks[key]
	if entry == nil {
		entry = &localLockEntry{}
		bankLocks[key] = entry
	}
	entry.refs++
	bankLockMu.Unlock()

	entry.mu.Lock()
	defer func() {
		entry.mu.Unlock()
		bankLockMu.Lock()
		entry.refs--
		if entry.refs == 0 && bankLocks[key] == entry {
			delete(bankLocks, key)
		}
		bankLockMu.Unlock()
	}()
	unlockFile, err := acquireFileLock(key+".lock", fileLockTimeout)
	if err != nil {
		return err
	}
	defer unlockFile()
	return fn()
}

func sanitizeBankName(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	sanitized := strings.Trim(bankNameUnsafeRE.ReplaceAllString(trimmed, "-"), "-")
	if sanitized == "" {
		return ""
	}
	return limitBankName(sanitized)
}

func hash36(s string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return strconv.FormatUint(h.Sum64(), 36)
}

func limitBankName(name string) string {
	if len(name) <= 64 {
		return name
	}
	h := hash36(name)
	prefixLen := 63 - len(h)
	if prefixLen < 1 {
		prefixLen = 1
	}
	prefix := strings.TrimRight(name[:prefixLen], "-")
	if prefix == "" {
		prefix = "bank"
	}
	return prefix + "-" + h
}

func projectBankSegment(cwd string) string {
	abs, err := filepath.Abs(cwd)
	if err != nil || abs == "" {
		abs = cwd
	}
	if abs == "" {
		abs = "."
	}
	base := sanitizeBankName(filepath.Base(abs))
	if base == "" {
		base = "default"
	}
	return limitBankName(base + "-" + hash36(abs))
}

// LoadMnemopiConfig resolves Mnemopi configuration for ctx.
// upstream: packages/coding-agent/src/mnemopi/config.ts:72-193
func LoadMnemopiConfig(ctx sdk.Context) MnemopiConfig {
	agentDir := ResolveAgentDir(ctx)
	cwd := ResolveCwd(ctx)

	dbDir := filepath.Join(agentDir, "memories", "mnemopi")
	if rawDB := strings.TrimSpace(settingString(ctx, SettingMnemopiDbPath, "")); rawDB != "" {
		expanded := expandTilde(rawDB)
		// If dbPath points to a .db/.json file, use its parent directory; if it's a directory path, use it directly.
		ext := strings.ToLower(filepath.Ext(expanded))
		if ext == ".db" || ext == ".sqlite" || ext == ".sqlite3" || ext == ".json" {
			dbDir = filepath.Dir(expanded)
		} else {
			dbDir = expanded
		}
	}

	scoping := strings.ToLower(settingString(ctx, SettingMnemopiScoping, "per-project"))
	switch scoping {
	case "global", "per-project", "per-project-tagged":
	default:
		scoping = "per-project"
	}

	configuredBank := settingString(ctx, SettingMnemopiBank, "")
	if configuredBank == "" {
		configuredBank = settingString(ctx, SettingMnemopiBankID, "")
	}
	baseBank := sanitizeBankName(configuredBank)
	globalBank := baseBank
	if globalBank == "" {
		globalBank = defaultMnemopiSharedBank
	}

	projSeg := projectBankSegment(cwd)
	projectBank := projSeg
	if baseBank != "" {
		projectBank = limitBankName(baseBank + "-" + projSeg)
	}

	var bank, retainBank string
	var recallBanks []string
	switch scoping {
	case "global":
		bank = globalBank
		retainBank = globalBank
		recallBanks = []string{globalBank}
	case "per-project-tagged":
		bank = projectBank
		retainBank = projectBank
		if projectBank == globalBank {
			recallBanks = []string{projectBank}
		} else {
			recallBanks = []string{projectBank, globalBank}
		}
	default: // "per-project"
		bank = projectBank
		retainBank = projectBank
		recallBanks = []string{projectBank}
	}

	recallLimit := settingInt(ctx, SettingMnemopiRecallLimit, defaultMnemopiRecallLimit)
	if recallLimit < 1 {
		recallLimit = defaultMnemopiRecallLimit
	}
	injectLimit := settingInt(ctx, SettingMnemopiInjectionLimit, defaultMnemopiInjectLimit)
	if injectLimit < 256 {
		injectLimit = defaultMnemopiInjectLimit
	}

	embPrefix, _ := settingStringRaw(ctx, SettingMnemopiEmbeddingPrefix)
	return MnemopiConfig{
		DBDir:           dbDir,
		BaseBank:        globalBank,
		Bank:            bank,
		GlobalBank:      globalBank,
		RetainBank:      retainBank,
		RecallBanks:     recallBanks,
		Scoping:         scoping,
		RecallLimit:     recallLimit,
		InjectionLimit:  injectLimit,
		NoEmbeddings:    settingBool(ctx, SettingMnemopiNoEmbeddings, false),
		EmbeddingModel:  settingString(ctx, SettingMnemopiEmbeddingModel, "text-embedding-3-small"),
		EmbeddingAPIURL: settingString(ctx, SettingMnemopiEmbeddingAPIURL, ""),
		EmbeddingAPIKey: settingString(ctx, SettingMnemopiEmbeddingAPIKey, ""),
		EmbeddingPrefix: embPrefix,
	}
}

// BankFilePath returns `<dbDir>/banks/<bank>.json`.
func (cfg MnemopiConfig) BankFilePath(bank string) string {
	safe := sanitizeBankName(bank)
	if safe == "" {
		safe = defaultMnemopiSharedBank
	}
	return filepath.Join(cfg.DBDir, "banks", safe+".json")
}

// ScopedBanks returns deduplicated banks in lookup order: retain, recall..., global (if tagged).
func (cfg MnemopiConfig) ScopedBanks() []string {
	seen := make(map[string]bool, len(cfg.RecallBanks)+2)
	var out []string
	add := func(b string) {
		if b != "" && !seen[b] {
			seen[b] = true
			out = append(out, b)
		}
	}
	add(cfg.RetainBank)
	for _, b := range cfg.RecallBanks {
		add(b)
	}
	if cfg.Scoping == "per-project-tagged" || cfg.Scoping == "global" {
		add(cfg.GlobalBank)
	}
	return out
}

// GlobalRetainBank returns the bank name for `scope: "global"` writes, or an error
// when scoping is `per-project`.
// upstream: packages/coding-agent/src/mnemopi/state.ts:431-438
func (cfg MnemopiConfig) GlobalRetainBank() (string, error) {
	switch cfg.Scoping {
	case "global":
		return cfg.RetainBank, nil
	case "per-project-tagged":
		return cfg.GlobalBank, nil
	default:
		return "", errors.New("Mnemopi global scope requires global or per-project-tagged scoping.")
	}
}

func readBankFileLocked(bankPath, bank string) (mnemopiBankFile, error) {
	raw, err := os.ReadFile(bankPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return mnemopiBankFile{Version: 1, Bank: bank}, nil
		}
		return mnemopiBankFile{}, err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return mnemopiBankFile{Version: 1, Bank: bank}, nil
	}
	var bf mnemopiBankFile
	if err := json.Unmarshal(raw, &bf); err != nil {
		return mnemopiBankFile{}, fmt.Errorf("corrupt mnemopi bank %s: %w", bank, err)
	}
	if bf.Bank == "" {
		bf.Bank = bank
	}
	if bf.Version == 0 {
		bf.Version = 1
	}
	return bf, nil
}

func writeBankFileLocked(bankPath string, bf mnemopiBankFile) error {
	data, err := json.Marshal(bf)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFileAtomic(bankPath, data, 0o600)
}

func generateMemoryID(prefix string) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		sum := sha256.Sum256([]byte(strconv.FormatInt(time.Now().UnixNano(), 10)))
		copy(b[:], sum[:8])
	}
	return prefix + "-" + hex.EncodeToString(b[:])
}

func redactMetadataMap(m map[string]any) map[string]any {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = redactAnyValue(v)
	}
	return out
}

func redactAnyValue(v any) any {
	switch x := v.(type) {
	case string:
		return RedactMemorySecrets(x)
	case []any:
		cp := make([]any, len(x))
		for i, item := range x {
			cp[i] = redactAnyValue(item)
		}
		return cp
	case map[string]any:
		return redactMetadataMap(x)
	default:
		return v
	}
}

func normalizeStoreKind(s string) MnemopiStoreKind {
	switch MnemopiStoreKind(s) {
	case StoreEpisodic:
		return StoreEpisodic
	case StoreFact:
		return StoreFact
	default:
		return StoreWorking
	}
}

func isRecordInvalidated(rec MnemopiRecord, now time.Time) bool {
	if strings.TrimSpace(rec.SupersededBy) != "" {
		return true
	}
	if strings.TrimSpace(rec.ValidUntil) != "" {
		if t, err := time.Parse(time.RFC3339Nano, rec.ValidUntil); err == nil {
			if !t.After(now) {
				return true
			}
		} else if t, err := time.Parse(time.RFC3339, rec.ValidUntil); err == nil {
			if !t.After(now) {
				return true
			}
		}
	}
	return false
}

// MnemopiRememberInput specifies one memory record to persist into Mnemopi.
type MnemopiRememberInput struct {
	Content    string
	Source     string
	Importance float64
	MemoryType string
	Store      MnemopiStoreKind // defaults to StoreWorking
	Veracity   string
	SessionID  string
	Metadata   map[string]any
	Scope      string // "" | "project" | "global"
}

// MnemopiRemember stores one record into the resolved bank and returns its ID.
// upstream: packages/coding-agent/src/mnemopi/state.ts:651-663
func MnemopiRemember(ctx context.Context, cfg MnemopiConfig, input MnemopiRememberInput) (string, error) {
	bank := cfg.RetainBank
	if input.Scope == "global" {
		var err error
		bank, err = cfg.GlobalRetainBank()
		if err != nil {
			return "", err
		}
	}

	content := RedactMemorySecrets(strings.TrimSpace(input.Content))
	if content == "" {
		return "", errors.New("memory content must not be empty")
	}
	source := RedactMemorySecrets(strings.TrimSpace(input.Source))
	store := input.Store
	if store == "" {
		store = StoreWorking
	}
	importance := input.Importance
	if importance < 0 {
		importance = 0
	} else if importance > 1 {
		importance = 1
	}
	memType := strings.TrimSpace(input.MemoryType)
	if memType == "" {
		memType = "fact"
	}
	veracity := strings.TrimSpace(input.Veracity)
	if veracity == "" {
		veracity = "tool"
	}

	var emb []float64
	if !cfg.NoEmbeddings && cfg.EmbeddingAPIURL != "" {
		if vec, err := fetchEmbedding(ctx, cfg, content); err == nil && len(vec) > 0 {
			emb = vec
		}
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	id := generateMemoryID("mem")
	rec := MnemopiRecord{
		ID:         id,
		Content:    content,
		Store:      string(store),
		MemoryType: memType,
		Source:     source,
		Timestamp:  now,
		CreatedAt:  now,
		Importance: importance,
		Veracity:   veracity,
		SessionID:  strings.TrimSpace(input.SessionID),
		Metadata:   redactMetadataMap(input.Metadata),
		Embedding:  emb,
	}

	bankPath := cfg.BankFilePath(bank)
	err := withBankLock(bankPath, func() error {
		bf, err := readBankFileLocked(bankPath, bank)
		if err != nil {
			return err
		}
		bf.Records = append(bf.Records, rec)
		return writeBankFileLocked(bankPath, bf)
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

// SeedMnemopiRecord inserts an explicit record (including episodic or fact rows)
// directly into the target bank. Useful for tests and store migrations.
func SeedMnemopiRecord(cfg MnemopiConfig, bank string, rec MnemopiRecord) (string, error) {
	if bank == "" {
		bank = cfg.RetainBank
	}
	if rec.ID == "" {
		rec.ID = generateMemoryID("mem")
	}
	if rec.Store == "" {
		rec.Store = string(StoreWorking)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if rec.Timestamp == "" {
		rec.Timestamp = now
	}
	if rec.CreatedAt == "" {
		rec.CreatedAt = rec.Timestamp
	}
	bankPath := cfg.BankFilePath(bank)
	err := withBankLock(bankPath, func() error {
		bf, err := readBankFileLocked(bankPath, bank)
		if err != nil {
			return err
		}
		bf.Records = append(bf.Records, rec)
		return writeBankFileLocked(bankPath, bf)
	})
	if err != nil {
		return "", err
	}
	return rec.ID, nil
}

// MnemopiGetScopedMemory fetches a memory row by ID across all banks in scope.
// upstream: packages/coding-agent/src/mnemopi/state.ts:453-489
func MnemopiGetScopedMemory(cfg MnemopiConfig, id string) (*MnemopiScopedMemoryHit, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, nil
	}
	now := time.Now().UTC()
	for _, bank := range cfg.ScopedBanks() {
		bankPath := cfg.BankFilePath(bank)
		var hit *MnemopiScopedMemoryHit
		err := withBankLock(bankPath, func() error {
			bf, err := readBankFileLocked(bankPath, bank)
			if err != nil {
				return err
			}
			for _, rec := range bf.Records {
				if rec.ID != id {
					continue
				}
				store := normalizeStoreKind(rec.Store)
				invalidated := isRecordInvalidated(rec, now)
				if store == StoreFact && !invalidated {
					if srcID, ok := rec.Metadata["source_msg_id"].(string); ok && srcID != "" {
						for _, src := range bf.Records {
							if src.ID == srcID && normalizeStoreKind(src.Store) == StoreWorking {
								invalidated = isRecordInvalidated(src, now)
								break
							}
						}
					}
				}
				recCopy := rec
				hit = &MnemopiScopedMemoryHit{
					Bank:        bank,
					Store:       store,
					Invalidated: invalidated,
					Row:         recCopy,
				}
				return nil
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		if hit != nil {
			return hit, nil
		}
	}
	return nil, nil
}

// MnemopiEditScopedMemory applies `update`, `forget`, or `invalidate` on a scoped memory ID.
// upstream: packages/coding-agent/src/mnemopi/state.ts:491-545
func MnemopiEditScopedMemory(cfg MnemopiConfig, op, id string, opts MnemopiEditOptions) (MnemopiEditResult, error) {
	id = strings.TrimSpace(id)
	var ineligible *MnemopiEditResult

	for _, bank := range cfg.ScopedBanks() {
		bankPath := cfg.BankFilePath(bank)
		var done *MnemopiEditResult
		err := withBankLock(bankPath, func() error {
			bf, err := readBankFileLocked(bankPath, bank)
			if err != nil {
				return err
			}
			idx := -1
			for i := range bf.Records {
				if bf.Records[i].ID == id {
					idx = i
					break
				}
			}
			if idx < 0 {
				return nil
			}
			rec := bf.Records[idx]
			store := normalizeStoreKind(rec.Store)
			if store == StoreFact {
				if ineligible == nil {
					r := MnemopiEditResult{Status: "not_editable", Bank: bank, Store: string(store)}
					ineligible = &r
				}
				return nil
			}
			if (op == "update" || op == "forget") && store != StoreWorking {
				if ineligible == nil {
					r := MnemopiEditResult{Status: "not_found", Bank: bank, Store: string(store)}
					ineligible = &r
				}
				return nil
			}

			switch op {
			case "update":
				if opts.Content != nil {
					bf.Records[idx].Content = RedactMemorySecrets(*opts.Content)
					bf.Records[idx].Embedding = nil
				}
				if opts.Importance != nil {
					imp := math.Max(0, math.Min(1, *opts.Importance))
					bf.Records[idx].Importance = imp
				}
				if err := writeBankFileLocked(bankPath, bf); err != nil {
					return err
				}
				r := MnemopiEditResult{Status: "updated", Bank: bank, Store: string(store)}
				done = &r
			case "forget":
				bf.Records = append(bf.Records[:idx], bf.Records[idx+1:]...)
				if err := writeBankFileLocked(bankPath, bf); err != nil {
					return err
				}
				r := MnemopiEditResult{Status: "deleted", Bank: bank, Store: string(store)}
				done = &r
			case "invalidate":
				bf.Records[idx].ValidUntil = time.Now().UTC().Format(time.RFC3339Nano)
				if strings.TrimSpace(opts.ReplacementID) != "" {
					bf.Records[idx].SupersededBy = strings.TrimSpace(opts.ReplacementID)
				}
				if err := writeBankFileLocked(bankPath, bf); err != nil {
					return err
				}
				r := MnemopiEditResult{Status: "invalidated", Bank: bank, Store: string(store)}
				done = &r
			default:
				return fmt.Errorf("invalid memory_edit operation %q", op)
			}
			return nil
		})
		if err != nil {
			return MnemopiEditResult{}, err
		}
		if done != nil {
			return *done, nil
		}
	}

	if ineligible != nil {
		return *ineligible, nil
	}
	return MnemopiEditResult{Status: "not_found"}, nil
}

// tokenize splits text into lowercase alphanumeric tokens for BM25 lexical scoring.
func tokenize(text string) []string {
	var tokens []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			tokens = append(tokens, cur.String())
			cur.Reset()
		}
	}
	for _, r := range strings.ToLower(text) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return tokens
}

func clipRecallPreview(content string, maxRunes int) (preview string, truncated bool, fullLen int) {
	runes := []rune(content)
	fullLen = len(runes)
	if maxRunes <= 0 || fullLen <= maxRunes {
		return content, false, fullLen
	}
	return strings.TrimRight(string(runes[:maxRunes]), " \t\n\r") + "…", true, fullLen
}

// MnemopiRecall searches all recall banks in scope using deterministic BM25 +
// token overlap + recency + importance (+ optional HTTP embedding cosine boost).
// upstream: packages/coding-agent/src/mnemopi/state.ts:562-612
func MnemopiRecall(ctx context.Context, cfg MnemopiConfig, query string) ([]MnemopiRecallResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	queryTokens := tokenize(query)
	var queryVec []float64
	if !cfg.NoEmbeddings && cfg.EmbeddingAPIURL != "" {
		embQuery := query
		if cfg.EmbeddingPrefix != "" {
			embQuery = cfg.EmbeddingPrefix + query
		}
		if vec, err := fetchEmbedding(ctx, cfg, embQuery); err == nil && len(vec) > 0 {
			queryVec = vec
		}
	}

	now := time.Now().UTC()
	type docEntry struct {
		bank   string
		rec    MnemopiRecord
		tokens []string
		tf     map[string]int
	}
	var docs []docEntry
	seenBanks := make(map[string]bool)
	for _, bank := range cfg.RecallBanks {
		if seenBanks[bank] {
			continue
		}
		seenBanks[bank] = true
		bankPath := cfg.BankFilePath(bank)
		err := withBankLock(bankPath, func() error {
			bf, err := readBankFileLocked(bankPath, bank)
			if err != nil {
				return err
			}
			// Index retired working IDs so facts sourced from them are hidden too.
			retiredWorking := map[string]bool{}
			for _, rec := range bf.Records {
				if normalizeStoreKind(rec.Store) == StoreWorking && isRecordInvalidated(rec, now) {
					retiredWorking[rec.ID] = true
				}
			}
			for _, rec := range bf.Records {
				if isRecordInvalidated(rec, now) {
					continue
				}
				if normalizeStoreKind(rec.Store) == StoreFact {
					if srcID, ok := rec.Metadata["source_msg_id"].(string); ok && retiredWorking[srcID] {
						continue
					}
				}
				searchText := rec.Content
				if rec.Metadata != nil {
					if c, ok := rec.Metadata["context"].(string); ok && c != "" {
						searchText += " " + c
					}
				}
				toks := tokenize(searchText)
				tf := make(map[string]int, len(toks))
				for _, tok := range toks {
					tf[tok]++
				}
				docs = append(docs, docEntry{bank: bank, rec: rec, tokens: toks, tf: tf})
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	if len(docs) == 0 {
		return nil, nil
	}

	// Compute document frequency and average document length for BM25.
	df := make(map[string]int)
	totalLen := 0
	for _, d := range docs {
		totalLen += len(d.tokens)
		for tok := range d.tf {
			df[tok]++
		}
	}
	avgDL := float64(totalLen) / float64(len(docs))
	if avgDL < 1 {
		avgDL = 1
	}

	uniqueQueryTokens := make([]string, 0, len(queryTokens))
	seenQ := make(map[string]bool, len(queryTokens))
	for _, tok := range queryTokens {
		if !seenQ[tok] {
			seenQ[tok] = true
			uniqueQueryTokens = append(uniqueQueryTokens, tok)
		}
	}

	const k1 = 1.5
	const b = 0.75
	nDocs := float64(len(docs))
	queryLower := strings.ToLower(query)

	byID := make(map[string]int)
	byContent := make(map[string]int)
	var merged []MnemopiRecallResult

	for _, d := range docs {
		bm25 := 0.0
		matchedTerms := 0
		dl := float64(len(d.tokens))
		for _, qtok := range uniqueQueryTokens {
			freq := float64(d.tf[qtok])
			if freq == 0 {
				continue
			}
			matchedTerms++
			docFreq := float64(df[qtok])
			idf := math.Log((nDocs-docFreq+0.5)/(docFreq+0.5) + 1.0)
			bm25 += idf * ((freq * (k1 + 1)) / (freq + k1*(1-b+b*(dl/avgDL))))
		}

		exactSubstring := strings.Contains(strings.ToLower(d.rec.Content), queryLower)
		cosSim := 0.0
		if len(queryVec) > 0 && len(d.rec.Embedding) == len(queryVec) {
			cosSim = cosineSimilarity(queryVec, d.rec.Embedding)
		}

		// Must match at least one lexical term, substring, or have meaningful vector similarity.
		if matchedTerms == 0 && !exactSubstring && cosSim < 0.25 {
			continue
		}

		// Normalize lexical score to [0, 1].
		overlapRatio := 0.0
		if len(uniqueQueryTokens) > 0 {
			overlapRatio = float64(matchedTerms) / float64(len(uniqueQueryTokens))
		}
		bm25Norm := bm25 / (bm25 + 2.0)
		lexical := 0.6*bm25Norm + 0.4*overlapRatio
		if exactSubstring {
			lexical = math.Min(1.0, lexical+0.25)
		}

		// Recency decay (half-life ~30 days).
		recency := 0.5
		if ts, err := parseTimestamp(d.rec.Timestamp); err == nil {
			ageDays := math.Max(0, now.Sub(ts).Hours()/24.0)
			recency = math.Exp(-ageDays / 30.0)
		}

		imp := d.rec.Importance
		if imp <= 0 {
			imp = 0.5
		}

		var score float64
		if cosSim > 0 {
			score = 0.45*lexical + 0.35*cosSim + 0.10*recency + 0.10*imp
		} else {
			score = 0.70*lexical + 0.15*recency + 0.15*imp
		}
		if score > 1.0 {
			score = 1.0
		}

		preview, truncated, fullLen := clipRecallPreview(d.rec.Content, defaultRecallPreviewRunes)
		source := d.rec.Source
		if normalizeStoreKind(d.rec.Store) == StoreFact && source == "" {
			source = "facts"
		}
		cand := MnemopiRecallResult{
			ID:         d.rec.ID,
			Bank:       d.bank,
			Store:      normalizeStoreKind(d.rec.Store),
			Content:    preview,
			FullLength: fullLen,
			Truncated:  truncated,
			Source:     source,
			Timestamp:  d.rec.Timestamp,
			Importance: d.rec.Importance,
			Score:      score,
		}

		existingIdx, exists := byID[cand.ID]
		if !exists {
			existingIdx, exists = byContent[cand.Content]
		}
		if !exists {
			idx := len(merged)
			merged = append(merged, cand)
			if cand.ID != "" {
				byID[cand.ID] = idx
			}
			byContent[cand.Content] = idx
		} else if compareRecallResults(cand, merged[existingIdx]) < 0 {
			merged[existingIdx] = cand
		}
	}

	sort.Slice(merged, func(i, j int) bool {
		return compareRecallResults(merged[i], merged[j]) < 0
	})
	if len(merged) > cfg.RecallLimit {
		merged = merged[:cfg.RecallLimit]
	}
	return merged, nil
}

func compareRecallResults(left, right MnemopiRecallResult) int {
	if left.Score != right.Score {
		if right.Score > left.Score {
			return 1
		}
		return -1
	}
	if left.Timestamp != right.Timestamp {
		if right.Timestamp > left.Timestamp {
			return 1
		}
		return -1
	}
	return strings.Compare(left.Content, right.Content)
}

func parseTimestamp(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, s)
}

// FormatMnemopiRecallWithIDs formats recall results in exact upstream bullet format:
// `- {content} (id: {id}) [{source}] (YYYY-MM-DD) c:{score:.1f}`
// upstream: packages/coding-agent/src/mnemopi/state.ts:547-560
func FormatMnemopiRecallWithIDs(results []MnemopiRecallResult) string {
	if len(results) == 0 {
		return ""
	}
	lines := make([]string, len(results))
	for i, r := range results {
		idPart := " (id unavailable)"
		if r.ID != "" {
			idPart = fmt.Sprintf(" (id: %s)", r.ID)
		}
		sourcePart := ""
		if r.Source != "" {
			sourcePart = fmt.Sprintf(" [%s]", r.Source)
		}
		datePart := ""
		if len(r.Timestamp) >= 10 {
			datePart = fmt.Sprintf(" (%s)", r.Timestamp[:10])
		}
		score := r.Score
		if score == 0 && r.Importance > 0 {
			score = r.Importance
		}
		confPart := fmt.Sprintf(" c:%.1f", score)
		lines[i] = fmt.Sprintf("- %s%s%s%s%s", r.Content, idPart, sourcePart, datePart, confPart)
	}
	return strings.Join(lines, "\n\n")
}

// FormatMnemopiReflectContext formats recalled memories for `reflect` output.
// upstream: packages/coding-agent/src/tools/memory-reflect.ts:59-63
func FormatMnemopiReflectContext(results []MnemopiRecallResult) string {
	if len(results) == 0 {
		return ""
	}
	lines := make([]string, len(results))
	for i, r := range results {
		sourcePart := ""
		if r.Source != "" {
			sourcePart = fmt.Sprintf(" [%s]", r.Source)
		}
		datePart := ""
		if len(r.Timestamp) >= 10 {
			datePart = fmt.Sprintf(" (%s)", r.Timestamp[:10])
		}
		lines[i] = fmt.Sprintf("- %s%s%s", r.Content, sourcePart, datePart)
	}
	return strings.Join(lines, "\n")
}

func cosineSimilarity(a, b []float64) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

func fetchEmbedding(ctx context.Context, cfg MnemopiConfig, input string) ([]float64, error) {
	if cfg.EmbeddingAPIURL == "" {
		return nil, nil
	}
	if err := validateHTTPURL(cfg.EmbeddingAPIURL); err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(cfg.EmbeddingAPIURL, "/")
	if !strings.HasSuffix(endpoint, "/embeddings") {
		endpoint += "/embeddings"
	}
	body, err := json.Marshal(map[string]any{
		"model": cfg.EmbeddingModel,
		"input": input,
	})
	if err != nil {
		return nil, err
	}
	reqCtx, cancel := context.WithTimeout(ctx, mnemopiEmbeddingTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.EmbeddingAPIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.EmbeddingAPIKey)
	}
	resp, err := safeHTTPClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("embedding request failed with status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, mnemopiMaxEmbeddingRespSize))
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	if len(parsed.Data) == 0 {
		return nil, nil
	}
	return parsed.Data[0].Embedding, nil
}
