package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// upstream: packages/coding-agent/src/slash-commands/builtin-lifecycle.ts:523-610
// upstream: packages/coding-agent/src/modes/controllers/command-controller.ts:1024-1356
// upstream: packages/coding-agent/src/hindsight/client.ts:484-579
// upstream: packages/coding-agent/src/hindsight/mental-models.ts:89-132
// upstream: packages/coding-agent/src/hindsight/seeds.json

const (
	memoryCommandUsage = "Usage: /memory [view|stats|diagnose|clear|reset|enqueue|rebuild|sync|queue|mm ...]"
	mmCommandUsage     = "Usage: /memory mm <list|show|refresh|history|seed|reload|delete>"
	mmDisabledNotice   = "Mental models require memory.backend=hindsight and hindsight.mentalModelsEnabled=true."

	hindsightMentalModelPageSize = 100
	hindsightMentalModelMaxPages = 50
	queuePreviewMaxItems         = 20
	queuePreviewMaxRunes         = 140
)

var (
	mmIDValidRe      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)
	mmSeedSanitizeRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)
)

func validateMentalModelID(id string) error {
	if id == "" || id == "." || id == ".." || !mmIDValidRe.MatchString(id) {
		return fmt.Errorf("invalid mental model id %q", id)
	}
	return nil
}

func sanitizeMMSeedSuffix(raw string) string {
	s := mmSeedSanitizeRe.ReplaceAllString(strings.TrimSpace(raw), "-")
	return strings.Trim(s, "-")
}

func sameTagSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 {
		return true
	}
	ca := append([]string(nil), a...)
	cb := append([]string(nil), b...)
	sort.Strings(ca)
	sort.Strings(cb)
	for i := range ca {
		if ca[i] != cb[i] {
			return false
		}
	}
	return true
}

var memorySubcommandCompletions = []sdk.AutocompleteItem{
	{Value: "view", Label: "view", Description: "Show current memory injection payload"},
	{Value: "stats", Label: "stats", Description: "Show memory backend statistics"},
	{Value: "diagnose", Label: "diagnose", Description: "Run memory backend diagnostics"},
	{Value: "clear", Label: "clear", Description: "Clear persisted memory data and artifacts"},
	{Value: "reset", Label: "reset", Description: "Alias for clear"},
	{Value: "enqueue", Label: "enqueue", Description: "Enqueue memory consolidation maintenance"},
	{Value: "rebuild", Label: "rebuild", Description: "Alias for enqueue"},
	{Value: "sync", Label: "sync", Description: "Run memory consolidation now"},
	{Value: "queue", Label: "queue", Description: "Show pending memory deltas awaiting consolidation"},
	{Value: "mm list", Label: "mm list", Description: "List mental models on the active bank"},
	{Value: "mm show ", Label: "mm show", Description: "Show one mental model (id required)"},
	{Value: "mm refresh", Label: "mm refresh", Description: "Refresh auto-refresh models bank-wide, or one model by id"},
	{Value: "mm history ", Label: "mm history", Description: "Show change history of a mental model"},
	{Value: "mm seed", Label: "mm seed", Description: "Create any built-in mental models that are missing"},
	{Value: "mm reload", Label: "mm reload", Description: "Re-pull the cached mental models"},
	{Value: "mm delete ", Label: "mm delete", Description: "Delete a mental model from the bank (id required)"},
}

func registerMemoryCommand(e *sdk.Extension) {
	if e == nil {
		return
	}
	e.RegisterCommand("memory", sdk.CommandOptions{
		Description: "Inspect or manage long-term memory backend",
		GetArgumentCompletions: func(prefix string) ([]sdk.AutocompleteItem, error) {
			lower := strings.ToLower(strings.TrimLeft(prefix, " \t"))
			var out []sdk.AutocompleteItem
			for _, item := range memorySubcommandCompletions {
				if strings.HasPrefix(item.Label, lower) {
					out = append(out, item)
				}
			}
			return out, nil
		},
		Handler: handleMemoryCommand,
	})
}

// ExecuteMemoryCommand executes `/memory <args>` and returns the output text
// and notification level ("info", "warning", or "error"). Exported for tests.
// upstream: packages/coding-agent/src/slash-commands/builtin-lifecycle.ts:550-606
// upstream: packages/coding-agent/src/modes/controllers/command-controller.ts:1024-1114
func ExecuteMemoryCommand(ctx sdk.Context, args string) (string, string) {
	fields := strings.Fields(strings.TrimSpace(args))
	verb := "view"
	if len(fields) > 0 {
		verb = strings.ToLower(fields[0])
	}

	switch verb {
	case "view":
		return handleMemoryView(ctx)
	case "stats", "diagnose":
		return handleMemoryStats(ctx, verb)
	case "clear", "reset":
		return handleMemoryClear(ctx)
	case "enqueue", "rebuild", "sync":
		return handleMemorySync(ctx, verb)
	case "queue":
		return handleMemoryQueue(ctx)
	case "mm":
		return handleMemoryMentalModels(ctx, fields[1:])
	default:
		return memoryCommandUsage, "error"
	}
}

func handleMemoryCommand(ctx sdk.Context, args string) error {
	msg, level := ExecuteMemoryCommand(ctx, args)
	if msg != "" {
		notifyContext(ctx, msg, level)
	}
	return nil
}

func notifyContext(ctx sdk.Context, message, level string) {
	var zero sdk.Context
	if ctx == zero {
		return
	}
	defer func() { _ = recover() }()
	ctx.Notify(message, level)
}

// ── view ─────────────────────────────────────────────────────────────────────

func handleMemoryView(ctx sdk.Context) (string, string) {
	backend := BackendFor(ctx)
	if backend == BackendOff {
		return "Memory backend is disabled (memory.backend=off).", "warning"
	}
	payload := BuildMemoryPromptInstructions(ctx)
	switch backend {
	case BackendLocal:
		if payload == "" {
			return "Memory payload is empty (no learned.md or memory_summary.md found).", "warning"
		}
		return payload, "info"
	case BackendMnemopi:
		cfg := LoadMnemopiConfig(ctx)
		summary := summarizeMnemopiBanks(cfg)
		if payload == "" && summary == "" {
			return "Memory payload is empty.", "warning"
		}
		if summary != "" {
			if payload != "" {
				return payload + "\n\n" + summary, "info"
			}
			return summary, "info"
		}
		return payload, "info"
	case BackendHindsight:
		cfg := LoadHindsightConfig(ctx)
		if cfg.APIURL == "" {
			return "Memory payload is empty (hindsight.apiUrl is not configured).", "warning"
		}
		scope := ComputeHindsightBankScope(cfg, ResolveCwd(ctx))
		outboxPath := HindsightOutboxPath(ResolveAgentDir(ctx), cfg.APIURL, scope.BankID)
		pending := countHindsightOutbox(outboxPath)
		summary := fmt.Sprintf("Hindsight bank: %s (scoping=%s, pending outbox=%d)", scope.BankID, cfg.Scoping, pending)
		if payload != "" {
			return payload + "\n\n" + summary, "info"
		}
		return summary, "info"
	default:
		if payload == "" {
			return fmt.Sprintf("Memory payload is empty (memory.backend=%s).", backend), "warning"
		}
		return payload, "info"
	}
}

func summarizeMnemopiBanks(cfg MnemopiConfig) string {
	banks := cfg.ScopedBanks()
	if len(banks) == 0 {
		return ""
	}
	now := time.Now().UTC()
	var lines []string
	for _, bank := range banks {
		bankPath := cfg.BankFilePath(bank)
		var total, active int
		_ = withBankLock(bankPath, func() error {
			bf, err := readBankFileLocked(bankPath, bank)
			if err != nil {
				return err
			}
			total = len(bf.Records)
			for _, rec := range bf.Records {
				if !isRecordInvalidated(rec, now) {
					active++
				}
			}
			return nil
		})
		lines = append(lines, fmt.Sprintf("- bank `%s`: %d active / %d total records", bank, active, total))
	}
	return "Mnemopi banks:\n" + strings.Join(lines, "\n")
}

// ── stats / diagnose ─────────────────────────────────────────────────────────

func handleMemoryStats(ctx sdk.Context, verb string) (string, string) {
	backend := BackendFor(ctx)
	switch backend {
	case BackendOff:
		return "Memory stats are not available when memory.backend=off.", "warning"
	case BackendLocal:
		return formatLocalStats(ctx, verb), "info"
	case BackendMnemopi:
		return formatMnemopiStats(ctx, verb), "info"
	case BackendHindsight:
		return formatHindsightStats(ctx, verb), "info"
	default:
		return fmt.Sprintf("Memory %s is not available for the %s backend.", verb, backend), "warning"
	}
}

func formatLocalStats(ctx sdk.Context, verb string) string {
	root := MemoryRoot(ResolveAgentDir(ctx), ResolveCwd(ctx))
	learnedPath := filepath.Join(root, LearnedLessonsFile)
	summaryPath := filepath.Join(root, "memory_summary.md")

	var learnedBytes int64
	var lessonCount int
	if raw, err := os.ReadFile(learnedPath); err == nil {
		learnedBytes = int64(len(raw))
		for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
			if isLessonBullet(line) {
				lessonCount++
			}
		}
	}
	var summaryBytes int64
	summaryPresent := false
	if info, err := os.Stat(summaryPath); err == nil {
		summaryPresent = true
		summaryBytes = info.Size()
	}

	title := "Memory Stats (local)"
	if verb == "diagnose" {
		title = "Memory Diagnostics (local)"
	}
	lines := []string{
		title,
		fmt.Sprintf("- memory root: %s", root),
		fmt.Sprintf("- learned.md: %d lessons (%d bytes)", lessonCount, learnedBytes),
		fmt.Sprintf("- memory_summary.md: present=%v (%d bytes)", summaryPresent, summaryBytes),
	}
	return strings.Join(lines, "\n")
}

func formatMnemopiStats(ctx sdk.Context, verb string) string {
	cfg := LoadMnemopiConfig(ctx)
	now := time.Now().UTC()

	title := "Memory Stats (mnemopi)"
	if verb == "diagnose" {
		title = "Memory Diagnostics (mnemopi)"
	}

	embStatus := "disabled (mnemopi.noEmbeddings=true)"
	if !cfg.NoEmbeddings {
		if cfg.EmbeddingAPIURL != "" {
			if err := validateHTTPURL(cfg.EmbeddingAPIURL); err != nil {
				embStatus = fmt.Sprintf("invalid endpoint (%v)", err)
			} else {
				embStatus = fmt.Sprintf("configured (%s, model=%s)", cfg.EmbeddingAPIURL, cfg.EmbeddingModel)
			}
		} else {
			embStatus = "lexical-only (mnemopi.embeddingApiUrl unset)"
		}
	}

	lines := []string{
		title,
		fmt.Sprintf("- bank: %s", cfg.Bank),
		fmt.Sprintf("- scoping: %s", cfg.Scoping),
		fmt.Sprintf("- embeddings: %s", embStatus),
	}

	for _, bank := range cfg.ScopedBanks() {
		bankPath := cfg.BankFilePath(bank)
		var size int64
		if info, err := os.Stat(bankPath); err == nil {
			size = info.Size()
		}
		var working, episodic, fact, active, invalidated int
		var readErr error
		_ = withBankLock(bankPath, func() error {
			bf, err := readBankFileLocked(bankPath, bank)
			if err != nil {
				readErr = err
				return nil
			}
			for _, rec := range bf.Records {
				switch normalizeStoreKind(rec.Store) {
				case StoreWorking:
					working++
				case StoreEpisodic:
					episodic++
				case StoreFact:
					fact++
				}
				if isRecordInvalidated(rec, now) {
					invalidated++
				} else {
					active++
				}
			}
			return nil
		})
		if readErr != nil {
			lines = append(lines, fmt.Sprintf("- bank file %s (%s): error reading bank: %v", bank, bankPath, readErr))
			continue
		}
		lines = append(lines,
			fmt.Sprintf("- bank file (%s): %s (%d bytes)", bank, bankPath, size),
			fmt.Sprintf("  records: working=%d, episodic=%d, fact=%d | active=%d, invalidated=%d", working, episodic, fact, active, invalidated),
		)
	}
	return strings.Join(lines, "\n")
}

func formatHindsightStats(ctx sdk.Context, verb string) string {
	cfg := LoadHindsightConfig(ctx)
	scope := ComputeHindsightBankScope(cfg, ResolveCwd(ctx))
	outboxPath := HindsightOutboxPath(ResolveAgentDir(ctx), cfg.APIURL, scope.BankID)
	pending := countHindsightOutbox(outboxPath)

	title := "Memory Stats (hindsight)"
	if verb == "diagnose" {
		title = "Memory Diagnostics (hindsight)"
	}
	apiURL := cfg.APIURL
	if apiURL == "" {
		apiURL = "(unset)"
	}
	lines := []string{
		title,
		fmt.Sprintf("- apiUrl: %s", apiURL),
		fmt.Sprintf("- bankId: %s", scope.BankID),
		fmt.Sprintf("- scoping: %s", cfg.Scoping),
		fmt.Sprintf("- pending outbox items: %d", pending),
		fmt.Sprintf("- outbox path: %s", outboxPath),
		fmt.Sprintf("- mentalModelsEnabled: %v", cfg.MentalModelsEnabled),
	}
	if verb == "diagnose" && cfg.APIURL != "" {
		if err := validateHTTPURL(cfg.APIURL); err != nil {
			lines = append(lines, fmt.Sprintf("- endpoint validation: ERROR (%v)", err))
		} else {
			lines = append(lines, "- endpoint validation: ok")
		}
	}
	return strings.Join(lines, "\n")
}

func countHindsightOutbox(outboxPath string) int {
	var n int
	_ = withOutboxLock(outboxPath, func() error {
		events, err := readOutboxEventsLocked(outboxPath)
		if err == nil {
			n = len(events)
		}
		return nil
	})
	return n
}

// ── clear / reset ────────────────────────────────────────────────────────────

func handleMemoryClear(ctx sdk.Context) (string, string) {
	backend := BackendFor(ctx)
	switch backend {
	case BackendOff:
		return "Memory backend is disabled (memory.backend=off).", "warning"

	case BackendLocal:
		root := MemoryRoot(ResolveAgentDir(ctx), ResolveCwd(ctx))
		learnedPath := filepath.Join(root, LearnedLessonsFile)
		summaryPath := filepath.Join(root, "memory_summary.md")
		err := withLocalFileLock(learnedPath, func() error {
			if err := os.Remove(learnedPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if err := os.Remove(summaryPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			return nil
		})
		if err != nil {
			return fmt.Sprintf("Memory clear failed: %v", err), "error"
		}
		return fmt.Sprintf("Memory cleared (%s and %s removed).", learnedPath, summaryPath), "info"

	case BackendMnemopi:
		cfg := LoadMnemopiConfig(ctx)
		banks := cfg.ScopedBanks()
		removed := make([]string, 0, len(banks))
		for _, bank := range banks {
			bankPath := cfg.BankFilePath(bank)
			err := withBankLock(bankPath, func() error {
				if err := os.Remove(bankPath); err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
				return nil
			})
			if err != nil {
				return fmt.Sprintf("Memory clear failed for bank %s: %v", bank, err), "error"
			}
			removed = append(removed, bank)
		}
		return fmt.Sprintf("Memory cleared (bank(s): %s).", strings.Join(removed, ", ")), "info"

	case BackendHindsight:
		cfg := LoadHindsightConfig(ctx)
		agentDir := ResolveAgentDir(ctx)
		scope := ComputeHindsightBankScope(cfg, ResolveCwd(ctx))
		var (
			delivered int
			kept      int
			flushErr  error
		)
		if cfg.APIURL != "" {
			delivered, kept, flushErr = FlushHindsightOutbox(signalContext(ctx), agentDir, cfg, scope)
		}
		if sid := sessionIDForContext(ctx); sid != "" {
			sessionStateMu.Lock()
			delete(sessionRefs, sid)
			sessionStateMu.Unlock()
		}
		if flushErr != nil || kept > 0 {
			return fmt.Sprintf(
				"Hindsight outbox was NOT deleted because %d pending item(s) could not be flushed (delivered %d, err: %v). Retry /memory sync or /memory clear once Hindsight is reachable.",
				kept, delivered, flushErr,
			), "warning"
		}
		outboxPath := HindsightOutboxPath(agentDir, cfg.APIURL, scope.BankID)
		err := withOutboxLock(outboxPath, func() error {
			if err := os.Remove(outboxPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			return nil
		})
		if err != nil {
			return fmt.Sprintf("Memory clear failed: %v", err), "error"
		}
		return fmt.Sprintf(
			"Hindsight local outbox/cache cleared (flushed %d pending item(s)). Remote bank %s was not deleted.",
			delivered, scope.BankID,
		), "info"
	default:
		return fmt.Sprintf("Memory clear is not supported for the %s backend.", backend), "warning"
	}
}

// ── enqueue / rebuild / sync ─────────────────────────────────────────────────

func handleMemorySync(ctx sdk.Context, verb string) (string, string) {
	backend := BackendFor(ctx)
	switch backend {
	case BackendOff:
		return "Memory backend is disabled (memory.backend=off).", "warning"

	case BackendHindsight:
		cfg := LoadHindsightConfig(ctx)
		if cfg.APIURL == "" {
			return "Hindsight backend is not configured (hindsight.apiUrl is empty).", "warning"
		}
		scope := ComputeHindsightBankScope(cfg, ResolveCwd(ctx))
		delivered, kept, err := FlushHindsightOutbox(signalContext(ctx), ResolveAgentDir(ctx), cfg, scope)
		if err != nil {
			return fmt.Sprintf("Hindsight outbox flush completed with error: delivered=%d, remaining=%d (%v)", delivered, kept, err), "warning"
		}
		return fmt.Sprintf("Hindsight outbox flushed: delivered=%d, remaining=%d.", delivered, kept), "info"

	case BackendMnemopi:
		cfg := LoadMnemopiConfig(ctx)
		now := time.Now().UTC()
		pruned := 0
		for _, bank := range cfg.ScopedBanks() {
			bankPath := cfg.BankFilePath(bank)
			err := withBankLock(bankPath, func() error {
				bf, err := readBankFileLocked(bankPath, bank)
				if err != nil {
					return err
				}
				if len(bf.Records) == 0 {
					return nil
				}
				// L6: Keep retired/invalidated working records that are still referenced
				// by any fact's metadata["source_msg_id"] so MnemopiRecall suppression stays intact.
				referencedByFact := make(map[string]bool)
				for _, rec := range bf.Records {
					if rec.Store == string(StoreFact) && !isRecordInvalidated(rec, now) && rec.Metadata != nil {
						if srcID, ok := rec.Metadata["source_msg_id"].(string); ok && srcID != "" {
							referencedByFact[srcID] = true
						}
					}
				}
				kept := make([]MnemopiRecord, 0, len(bf.Records))
				for _, rec := range bf.Records {
					if isRecordInvalidated(rec, now) {
						if rec.Store == string(StoreWorking) && rec.ID != "" && referencedByFact[rec.ID] {
							kept = append(kept, rec)
							continue
						}
						pruned++
						continue
					}
					kept = append(kept, rec)
				}
				if len(kept) != len(bf.Records) {
					bf.Records = kept
					return writeBankFileLocked(bankPath, bf)
				}
				return nil
			})
			if err != nil {
				return fmt.Sprintf("Memory %s failed: %v", verb, err), "error"
			}
		}
		if verb == "sync" {
			return fmt.Sprintf("Memory consolidation ran. (pruned %d invalidated record(s))", pruned), "info"
		}
		return fmt.Sprintf("Memory consolidation enqueued (validated %d bank(s), pruned %d invalidated record(s)).", len(cfg.ScopedBanks()), pruned), "info"

	case BackendLocal:
		root := MemoryRoot(ResolveAgentDir(ctx), ResolveCwd(ctx))
		learned := ReadLearnedLessons(root)
		lessonCount := 0
		if learned != "" {
			for _, line := range strings.Split(learned, "\n") {
				if isLessonBullet(line) {
					lessonCount++
				}
			}
		}
		if verb == "sync" {
			return fmt.Sprintf("Memory consolidation ran. (%d local lesson(s) validated)", lessonCount), "info"
		}
		return fmt.Sprintf("Memory consolidation enqueued (%d local lesson(s) present).", lessonCount), "info"

	default:
		return fmt.Sprintf("Memory %s is not available for the %s backend.", verb, backend), "warning"
	}
}

// ── queue ────────────────────────────────────────────────────────────────────

func handleMemoryQueue(ctx sdk.Context) (string, string) {
	backend := BackendFor(ctx)
	if backend != BackendHindsight {
		return fmt.Sprintf("Memory queue is not available for the %s backend.", backend), "warning"
	}
	cfg := LoadHindsightConfig(ctx)
	scope := ComputeHindsightBankScope(cfg, ResolveCwd(ctx))
	outboxPath := HindsightOutboxPath(ResolveAgentDir(ctx), cfg.APIURL, scope.BankID)
	var events []HindsightOutboxEvent
	err := withOutboxLock(outboxPath, func() error {
		var readErr error
		events, readErr = readOutboxEventsLocked(outboxPath)
		return readErr
	})
	if err != nil {
		return fmt.Sprintf("Memory queue read failed: %v", err), "error"
	}
	if len(events) == 0 {
		return fmt.Sprintf("Hindsight outbox is empty (bank=%s).", scope.BankID), "info"
	}
	lines := []string{
		fmt.Sprintf("Hindsight pending outbox (%d item(s), bank=%s):", len(events), scope.BankID),
	}
	limit := len(events)
	if limit > queuePreviewMaxItems {
		limit = queuePreviewMaxItems
	}
	for i := range limit {
		ev := events[i]
		preview := strings.ReplaceAll(strings.TrimSpace(ev.Content), "\n", " ")
		runes := []rune(preview)
		if len(runes) > queuePreviewMaxRunes {
			preview = string(runes[:queuePreviewMaxRunes]) + "..."
		}
		lines = append(lines, fmt.Sprintf("- [%s] (%s) %s", ev.ID, ev.Scope, preview))
	}
	if len(events) > limit {
		lines = append(lines, fmt.Sprintf("... and %d more item(s)", len(events)-limit))
	}
	return strings.Join(lines, "\n"), "info"
}

// ── mm (Hindsight mental models) ─────────────────────────────────────────────

// HindsightMentalModelTrigger mirrors upstream MentalModelTrigger.
type HindsightMentalModelTrigger struct {
	Mode                    string `json:"mode,omitempty"`
	RefreshAfterConsolidate bool   `json:"refresh_after_consolidation,omitempty"`
}

// HindsightMentalModel mirrors upstream MentalModelSummary.
type HindsightMentalModel struct {
	ID              string                       `json:"id"`
	Name            string                       `json:"name"`
	SourceQuery     string                       `json:"source_query,omitempty"`
	Content         string                       `json:"content,omitempty"`
	Tags            []string                     `json:"tags,omitempty"`
	LastRefreshedAt string                       `json:"last_refreshed_at,omitempty"`
	Trigger         *HindsightMentalModelTrigger `json:"trigger,omitempty"`
}

// HindsightMentalModelListResponse mirrors upstream MentalModelListResponse.
type HindsightMentalModelListResponse struct {
	Items []HindsightMentalModel `json:"items"`
	Total *int                   `json:"total,omitempty"`
}

// HindsightMentalModelHistoryEntry mirrors upstream MentalModelHistoryEntry.
type HindsightMentalModelHistoryEntry struct {
	PreviousContent string `json:"previous_content"`
	ChangedAt       string `json:"changed_at"`
}

type builtinMMSeed struct {
	ID            string
	Name          string
	SourceQuery   string
	Scopes        []string
	ProjectTagged bool
	MaxTokens     int
	Trigger       *HindsightMentalModelTrigger
}

// upstream: packages/coding-agent/src/hindsight/seeds.json
var builtinMentalModelSeeds = []builtinMMSeed{
	{
		ID:            "user-preferences",
		Name:          "User Preferences",
		SourceQuery:   "What does the user prefer in coding style, tooling, communication, and review? Capture only durable preferences expressed across sessions, not one-off requests.",
		Scopes:        []string{"global", "per-project", "per-project-tagged"},
		ProjectTagged: false,
		MaxTokens:     600,
		Trigger:       &HindsightMentalModelTrigger{Mode: "delta", RefreshAfterConsolidate: true},
	},
	{
		ID:            "project-conventions",
		Name:          "Project Conventions",
		SourceQuery:   "What are this project's conventions for code style, build, testing, release, and pull-request review? Only include conventions that are explicit in the project (settings, scripts, contributor docs, repeatedly enforced in review).",
		Scopes:        []string{"per-project", "per-project-tagged"},
		ProjectTagged: true,
		MaxTokens:     800,
		Trigger:       &HindsightMentalModelTrigger{Mode: "delta", RefreshAfterConsolidate: true},
	},
	{
		ID:            "project-decisions",
		Name:          "Project Decisions",
		SourceQuery:   "What durable architectural or product decisions have been made for this project, and what rationale or trade-offs were recorded? Include only decisions that are stable across sessions; exclude transient plans, unresolved ideas, and active task state.",
		Scopes:        []string{"per-project", "per-project-tagged"},
		ProjectTagged: true,
		MaxTokens:     800,
		Trigger:       &HindsightMentalModelTrigger{Mode: "delta", RefreshAfterConsolidate: true},
	},
}

func (c *HindsightClient) ListMentalModels(ctx context.Context, bankID, detail string) ([]HindsightMentalModel, error) {
	if detail == "" {
		detail = "content"
	}
	basePath := fmt.Sprintf("/v1/default/banks/%s/mental-models", url.PathEscape(bankID))
	var all []HindsightMentalModel
	seen := make(map[string]bool)
	for range hindsightMentalModelMaxPages {
		path := fmt.Sprintf("%s?detail=%s&limit=%d&offset=%d", basePath, url.QueryEscape(detail), hindsightMentalModelPageSize, len(all))
		var page HindsightMentalModelListResponse
		if err := c.doJSON(ctx, http.MethodGet, path, "listMentalModels", nil, c.requestTimeoutMs, &page); err != nil {
			return nil, err
		}
		added := 0
		for _, item := range page.Items {
			if item.ID != "" {
				if seen[item.ID] {
					continue
				}
				seen[item.ID] = true
			}
			all = append(all, item)
			added++
		}
		if added == 0 || len(page.Items) < hindsightMentalModelPageSize {
			break
		}
		if page.Total != nil && len(all) >= *page.Total {
			break
		}
	}
	return all, nil
}

func (c *HindsightClient) GetMentalModel(ctx context.Context, bankID, id, detail string) (*HindsightMentalModel, error) {
	if detail == "" {
		detail = "content"
	}
	path := fmt.Sprintf("/v1/default/banks/%s/mental-models/%s?detail=%s",
		url.PathEscape(bankID), url.PathEscape(id), url.QueryEscape(detail))
	var out HindsightMentalModel
	err := c.doJSON(ctx, http.MethodGet, path, "getMentalModel", nil, c.requestTimeoutMs, &out)
	if err != nil {
		var herr *HindsightError
		if errors.As(err, &herr) && herr.StatusCode == http.StatusNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}

func (c *HindsightClient) RefreshMentalModel(ctx context.Context, bankID, id string) error {
	path := fmt.Sprintf("/v1/default/banks/%s/mental-models/%s/refresh",
		url.PathEscape(bankID), url.PathEscape(id))
	return c.doJSON(ctx, http.MethodPost, path, "refreshMentalModel", map[string]any{}, c.requestTimeoutMs, nil)
}

func (c *HindsightClient) GetMentalModelHistory(ctx context.Context, bankID, id string) ([]HindsightMentalModelHistoryEntry, error) {
	path := fmt.Sprintf("/v1/default/banks/%s/mental-models/%s/history",
		url.PathEscape(bankID), url.PathEscape(id))
	var raw json.RawMessage
	if err := c.doJSON(ctx, http.MethodGet, path, "getMentalModelHistory", nil, c.requestTimeoutMs, &raw); err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var arr []HindsightMentalModelHistoryEntry
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr, nil
	}
	var wrapped struct {
		Items []HindsightMentalModelHistoryEntry `json:"items"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, err
	}
	return wrapped.Items, nil
}

func (c *HindsightClient) CreateMentalModel(ctx context.Context, bankID, id, name, sourceQuery string, tags []string, maxTokens int, trigger *HindsightMentalModelTrigger) error {
	path := fmt.Sprintf("/v1/default/banks/%s/mental-models", url.PathEscape(bankID))
	payload := map[string]any{
		"name":         name,
		"source_query": sourceQuery,
	}
	if id != "" {
		payload["id"] = id
	}
	if len(tags) > 0 {
		payload["tags"] = tags
	}
	if maxTokens > 0 {
		payload["max_tokens"] = maxTokens
	}
	if trigger != nil {
		payload["trigger"] = trigger
	}
	return c.doJSON(ctx, http.MethodPost, path, "createMentalModel", payload, c.requestTimeoutMs, nil)
}

func (c *HindsightClient) DeleteMentalModel(ctx context.Context, bankID, id string) (bool, error) {
	path := fmt.Sprintf("/v1/default/banks/%s/mental-models/%s",
		url.PathEscape(bankID), url.PathEscape(id))
	err := c.doJSON(ctx, http.MethodDelete, path, "deleteMentalModel", nil, c.requestTimeoutMs, nil)
	if err != nil {
		var herr *HindsightError
		if errors.As(err, &herr) && herr.StatusCode == http.StatusNotFound {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func handleMemoryMentalModels(ctx sdk.Context, args []string) (string, string) {
	if BackendFor(ctx) != BackendHindsight {
		return mmDisabledNotice, "warning"
	}
	cfg := LoadHindsightConfig(ctx)
	if !cfg.MentalModelsEnabled {
		return mmDisabledNotice, "warning"
	}
	if cfg.APIURL == "" {
		return "Hindsight backend is not configured (hindsight.apiUrl is empty).", "error"
	}
	client, err := NewHindsightClient(cfg)
	if err != nil {
		return fmt.Sprintf("Hindsight client error: %v", err), "error"
	}
	scope := ComputeHindsightBankScope(cfg, ResolveCwd(ctx))
	goCtx := signalContext(ctx)

	verb := "list"
	if len(args) > 0 {
		verb = strings.ToLower(args[0])
	}
	arg := ""
	if len(args) > 1 {
		arg = args[1]
	}

	switch verb {
	case "list":
		items, err := client.ListMentalModels(goCtx, scope.BankID, "metadata")
		if err != nil {
			return fmt.Sprintf("mm list failed: %v", err), "error"
		}
		if len(items) == 0 {
			return fmt.Sprintf("No mental models on bank %s.", scope.BankID), "info"
		}
		sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
		lines := []string{fmt.Sprintf("Mental Models — %s", scope.BankID)}
		for _, m := range items {
			extra := ""
			if len(m.Tags) > 0 {
				extra = " [" + strings.Join(m.Tags, ", ") + "]"
			}
			lines = append(lines, fmt.Sprintf("- `%s` — %s%s", m.ID, m.Name, extra))
		}
		return strings.Join(lines, "\n"), "info"

	case "show":
		if arg == "" {
			return "Usage: /memory mm show <id>", "error"
		}
		if err := validateMentalModelID(arg); err != nil {
			return fmt.Sprintf("mm show failed: %v", err), "error"
		}
		model, err := client.GetMentalModel(goCtx, scope.BankID, arg, "content")
		if err != nil {
			return fmt.Sprintf("mm show failed: %v", err), "error"
		}
		if model == nil {
			return fmt.Sprintf("Mental model not found: %s", arg), "error"
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s\n**id:** `%s`", model.Name, model.ID)
		if len(model.Tags) > 0 {
			fmt.Fprintf(&b, "\n_tags: %s_", strings.Join(model.Tags, ", "))
		}
		if model.LastRefreshedAt != "" {
			fmt.Fprintf(&b, "\n_last refreshed: %s_", model.LastRefreshedAt)
		}
		if model.SourceQuery != "" {
			fmt.Fprintf(&b, "\n\n**Source query:** %s", model.SourceQuery)
		}
		content := strings.TrimSpace(model.Content)
		if content == "" {
			content = "_(empty — background reflect may still be running)_"
		}
		fmt.Fprintf(&b, "\n\n%s", content)
		return b.String(), "info"

	case "refresh":
		if arg != "" {
			if err := validateMentalModelID(arg); err != nil {
				return fmt.Sprintf("mm refresh failed: %v", err), "error"
			}
			if err := client.RefreshMentalModel(goCtx, scope.BankID, arg); err != nil {
				return fmt.Sprintf("mm refresh failed: %v", err), "error"
			}
			return fmt.Sprintf("Refresh queued for mental model %s.", arg), "info"
		}
		items, err := client.ListMentalModels(goCtx, scope.BankID, "content")
		if err != nil {
			return fmt.Sprintf("mm refresh failed: %v", err), "error"
		}
		if len(items) == 0 {
			return fmt.Sprintf("No mental models on bank %s.", scope.BankID), "info"
		}
		var targets []HindsightMentalModel
		for _, m := range items {
			if m.Trigger != nil && m.Trigger.RefreshAfterConsolidate {
				targets = append(targets, m)
			}
		}
		skipped := len(items) - len(targets)
		if len(targets) == 0 {
			return fmt.Sprintf(
				"No mental models opted into auto-refresh; %d curated model(s) left untouched. Pass an explicit id to refresh one of them.",
				skipped,
			), "info"
		}
		queued := 0
		var refreshErrs []string
		for _, m := range targets {
			if err := client.RefreshMentalModel(goCtx, scope.BankID, m.ID); err != nil {
				refreshErrs = append(refreshErrs, fmt.Sprintf("%s: %v", m.ID, err))
				continue
			}
			queued++
		}
		suffix := ""
		if skipped > 0 {
			suffix = fmt.Sprintf("; skipped %d curated model(s)", skipped)
		}
		if len(refreshErrs) > 0 {
			return fmt.Sprintf("Refresh queued for %d/%d auto-refresh model(s)%s (errors: %s).", queued, len(targets), suffix, strings.Join(refreshErrs, "; ")), "warning"
		}
		return fmt.Sprintf("Refresh queued for %d/%d auto-refresh model(s)%s.", queued, len(targets), suffix), "info"
	case "history":
		if arg == "" {
			return "Usage: /memory mm history <id>", "error"
		}
		if err := validateMentalModelID(arg); err != nil {
			return fmt.Sprintf("mm history failed: %v", err), "error"
		}
		model, err := client.GetMentalModel(goCtx, scope.BankID, arg, "content")
		if err != nil {
			return fmt.Sprintf("mm history failed: %v", err), "error"
		}
		if model == nil {
			return fmt.Sprintf("Mental model not found: %s", arg), "error"
		}
		history, err := client.GetMentalModelHistory(goCtx, scope.BankID, arg)
		if err != nil {
			return fmt.Sprintf("mm history failed: %v", err), "error"
		}
		if len(history) == 0 {
			return fmt.Sprintf("No history recorded for %s.", arg), "info"
		}
		sections := []string{fmt.Sprintf("History — %s", model.Name)}
		for i, entry := range history {
			after := model.Content
			if i > 0 {
				after = history[i-1].PreviousContent
			}
			sections = append(sections, fmt.Sprintf("### %s\n- before: %d bytes\n- after: %d bytes", entry.ChangedAt, len(entry.PreviousContent), len(after)))
		}
		return strings.Join(sections, "\n\n"), "info"

	case "seed":
		items, err := client.ListMentalModels(goCtx, scope.BankID, "metadata")
		if err != nil {
			return fmt.Sprintf("mm seed failed: %v", err), "error"
		}
		existingByID := make(map[string][]string, len(items))
		for _, m := range items {
			existingByID[m.ID] = m.Tags
		}
		created, skipped := 0, 0
		var seedErrs []string
		for _, seed := range builtinMentalModelSeeds {
			if !containsString(seed.Scopes, cfg.Scoping) {
				continue
			}
			var tags []string
			if seed.ProjectTagged && len(scope.RetainTags) > 0 {
				tags = append(tags, scope.RetainTags...)
			}
			id := seed.ID
			if cfg.Scoping == "per-project-tagged" && seed.ProjectTagged && len(tags) > 0 {
				suffix := strings.TrimPrefix(tags[0], "project:")
				suffix = sanitizeMMSeedSuffix(suffix)
				if suffix == "" {
					suffix = "project"
				}
				id = seed.ID + "-" + suffix
			}
			if _, ok := existingByID[id]; ok {
				skipped++
				continue
			}
			if legacyTags, ok := existingByID[seed.ID]; ok && sameTagSet(legacyTags, tags) {
				skipped++
				continue
			}
			if err := client.CreateMentalModel(goCtx, scope.BankID, id, seed.Name, seed.SourceQuery, tags, seed.MaxTokens, seed.Trigger); err != nil {
				seedErrs = append(seedErrs, fmt.Sprintf("%s: %v", id, err))
				continue
			}
			created++
		}
		if len(seedErrs) > 0 {
			return fmt.Sprintf("Seeded %d new mental model(s); %d already present; %d failed (%s).", created, skipped, len(seedErrs), strings.Join(seedErrs, "; ")), "warning"
		}
		return fmt.Sprintf("Seeded %d new mental model(s); %d already present.", created, skipped), "info"
	case "reload":
		items, err := client.ListMentalModels(goCtx, scope.BankID, "content")
		if err != nil {
			return fmt.Sprintf("mm reload failed: %v", err), "error"
		}
		return fmt.Sprintf("Mental-model cache reloaded (%d model(s) on bank %s).", len(items), scope.BankID), "info"

	case "delete", "remove":
		if arg == "" {
			return "Usage: /memory mm delete <id>", "error"
		}
		if err := validateMentalModelID(arg); err != nil {
			return fmt.Sprintf("mm delete failed: %v", err), "error"
		}
		removed, err := client.DeleteMentalModel(goCtx, scope.BankID, arg)
		if err != nil {
			return fmt.Sprintf("mm delete failed: %v", err), "error"
		}
		if !removed {
			return fmt.Sprintf("Mental model not found: %s", arg), "error"
		}
		return fmt.Sprintf("Deleted mental model %s from bank %s.", arg, scope.BankID), "info"

	default:
		return mmCommandUsage, "error"
	}
}

func containsString(list []string, target string) bool {
	for _, s := range list {
		if s == target {
			return true
		}
	}
	return false
}
