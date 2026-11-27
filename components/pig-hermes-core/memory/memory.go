// Package memory implements Hermes's MEMORY.md and USER.md bounded curated memory store
// plus the pluggable MemoryProvider seam for Phase 2 (oh-my-hermes-pig).
// Matches hermes-agent/tools/memory_tool.py and tools/memory_tool_store.py.
package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	// EntryDelimiter separates curated entries in MEMORY.md and USER.md.
	EntryDelimiter = "\n§\n"

	DefaultMemoryCharLimit = 2200
	DefaultUserCharLimit   = 1375
)

// Target identifies which memory file an operation targets.
type Target string

const (
	TargetMemory Target = "memory"
	TargetUser   Target = "user"
)

// Provider is the external memory provider seam (mirrors hermes-agent/agent/memory_provider.py).
// Phase 2 (oh-my-hermes-pig) registers OmhMemoryProvider through this interface.
type Provider interface {
	Name() string
	Prefetch(ctx context.Context, sessionID, query string) (string, error)
	OnPreCompress(ctx context.Context, sessionID string) error
	OnMemoryWrite(ctx context.Context, target Target, action, content string) error
	OnSessionEnd(ctx context.Context, sessionID string) error
}

// Operation is one atomic mutation in a memory tool batch.
type Operation struct {
	Action  string `json:"action"` // "add", "patch", "remove" ("replace" rejected per upstream)
	Content string `json:"content,omitempty"`
	OldText string `json:"old_text,omitempty"`
}

// OpResult reports the outcome of applying one or more memory operations.
type OpResult struct {
	Success    bool     `json:"success"`
	Target     Target   `json:"target"`
	Entries    []string `json:"entries"`
	CharsUsed  int      `json:"chars_used"`
	CharsLimit int      `json:"chars_limit"`
	Percent    int      `json:"percent"`
	SHA256     string   `json:"sha256"`
	Message    string   `json:"message"`
}

// Snapshot holds the frozen session-start view of MEMORY.md and USER.md so the
// system prompt prefix stays cache-warm across turns.
type Snapshot struct {
	MemoryEntries []string
	UserEntries   []string
	MemoryLimit   int
	UserLimit     int
}

// Store manages MEMORY.md and USER.md under a profile's memories directory.
type Store struct {
	mu          sync.Mutex
	dir         string
	memoryLimit int
	userLimit   int
	snapshot    *Snapshot
	providers   []Provider
}

// NewStore creates a Store for memoriesDir with Hermes's default or configured limits.
func NewStore(memoriesDir string, memoryLimit, userLimit int) *Store {
	if memoryLimit <= 0 {
		memoryLimit = DefaultMemoryCharLimit
	}
	if userLimit <= 0 {
		userLimit = DefaultUserCharLimit
	}
	return &Store{
		dir:         memoriesDir,
		memoryLimit: memoryLimit,
		userLimit:   userLimit,
	}
}
var (
	globalProvMu sync.RWMutex
	globalProvs  []Provider
)

// RegisterGlobalProvider attaches an external MemoryProvider consulted by all Store
// instances (Phase 2 oh-my-hermes-pig seam).
func RegisterGlobalProvider(p Provider) {
	if p == nil {
		return
	}
	globalProvMu.Lock()
	defer globalProvMu.Unlock()
	globalProvs = append(globalProvs, p)
}

// RegisterProvider attaches an external MemoryProvider to this Store instance.
func (s *Store) RegisterProvider(p Provider) {
	if p == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.providers = append(s.providers, p)
}

// Providers returns both store-local and globally registered external memory providers.
func (s *Store) Providers() []Provider {
	s.mu.Lock()
	local := make([]Provider, len(s.providers))
	copy(local, s.providers)
	s.mu.Unlock()

	globalProvMu.RLock()
	out := make([]Provider, 0, len(local)+len(globalProvs))
	out = append(out, local...)
	out = append(out, globalProvs...)
	globalProvMu.RUnlock()
	return out
}

func (s *Store) filePath(target Target) (string, int, error) {
	switch target {
	case TargetMemory:
		return filepath.Join(s.dir, "MEMORY.md"), s.memoryLimit, nil
	case TargetUser:
		return filepath.Join(s.dir, "USER.md"), s.userLimit, nil
	default:
		return "", 0, fmt.Errorf("invalid memory target %q: must be \"memory\" or \"user\"", target)
	}
}

// LoadSnapshot reads MEMORY.md and USER.md from disk and freezes the session snapshot.
func (s *Store) LoadSnapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := Snapshot{
		MemoryEntries: readEntries(filepath.Join(s.dir, "MEMORY.md")),
		UserEntries:   readEntries(filepath.Join(s.dir, "USER.md")),
		MemoryLimit:   s.memoryLimit,
		UserLimit:     s.userLimit,
	}
	s.snapshot = &snap
	return snap
}

// FrozenSnapshot returns the frozen session-start snapshot (loading once if needed).
func (s *Store) FrozenSnapshot() Snapshot {
	s.mu.Lock()
	if s.snapshot != nil {
		cp := *s.snapshot
		s.mu.Unlock()
		return cp
	}
	s.mu.Unlock()
	return s.LoadSnapshot()
}

// ReadLive returns the current on-disk entries, chars used, and limit for target.
func (s *Store) ReadLive(target Target) ([]string, int, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, limit, err := s.filePath(target)
	if err != nil {
		return nil, 0, 0, err
	}
	entries := readEntries(p)
	return entries, charCount(entries), limit, nil
}

// Apply executes an atomic batch of operations against target.
// All operations validate against a copy before writing to disk.
func (s *Store) Apply(ctx context.Context, target Target, ops []Operation) (OpResult, error) {
	if len(ops) == 0 {
		return OpResult{}, errors.New("operations list must not be empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	p, limit, err := s.filePath(target)
	if err != nil {
		return OpResult{}, err
	}
	entries := readEntries(p)
	working := make([]string, len(entries))
	copy(working, entries)

	for i, op := range ops {
		action := strings.ToLower(strings.TrimSpace(op.Action))
		switch action {
		case "replace":
			return OpResult{}, errors.New("action 'replace' has been removed; use action 'patch' with old_text and content instead")
		case "add":
			text := strings.TrimSpace(op.Content)
			if text == "" {
				return OpResult{}, fmt.Errorf("operation %d (add): content is required", i)
			}
			if containsExact(working, text) {
				continue
			}
			working = append(working, text)
		case "patch":
			old := op.OldText
			newText := op.Content
			if strings.TrimSpace(old) == "" {
				return OpResult{}, fmt.Errorf("operation %d (patch): old_text is required", i)
			}
			if strings.TrimSpace(newText) == "" {
				return OpResult{}, fmt.Errorf("operation %d (patch): content is required", i)
			}
			idx, matchErr := findUniqueMatch(working, old)
			if matchErr != nil {
				return OpResult{}, fmt.Errorf("operation %d (patch): %w", i, matchErr)
			}
			if strings.Count(working[idx], old) > 1 {
				return OpResult{}, fmt.Errorf("operation %d (patch): old_text appears multiple times within the matched entry; provide a more specific span", i)
			}
			updated := strings.Replace(working[idx], old, newText, 1)
			if updated == working[idx] {
				return OpResult{}, fmt.Errorf("operation %d (patch): no change produced", i)
			}
			working[idx] = updated
		case "remove":
			old := strings.TrimSpace(op.OldText)
			if old == "" {
				old = strings.TrimSpace(op.Content)
			}
			if old == "" {
				return OpResult{}, fmt.Errorf("operation %d (remove): old_text is required", i)
			}
			idx, matchErr := findUniqueMatch(working, old)
			if matchErr != nil {
				return OpResult{}, fmt.Errorf("operation %d (remove): %w", i, matchErr)
			}
			working = append(working[:idx], working[idx+1:]...)
		default:
			return OpResult{}, fmt.Errorf("operation %d: unknown action %q (expected add, patch, or remove)", i, op.Action)
		}
	}

	used := charCount(working)
	if used > limit {
		return OpResult{}, fmt.Errorf("%s memory limit exceeded: %d/%d chars (remove or condense existing entries first)", target, used, limit)
	}

	raw := strings.Join(working, EntryDelimiter)
	if err := writeAtomic(p, raw); err != nil {
		return OpResult{}, err
	}

	sum := sha256.Sum256([]byte(raw))
	pct := 0
	if limit > 0 {
		pct = (used * 100) / limit
	}
	res := OpResult{
		Success:    true,
		Target:     target,
		Entries:    working,
		CharsUsed:  used,
		CharsLimit: limit,
		Percent:    pct,
		SHA256:     hex.EncodeToString(sum[:]),
		Message:    fmt.Sprintf("Updated %s (%d/%d chars, %d%%)", target, used, limit, pct),
	}

	localProvs := make([]Provider, len(s.providers))
	copy(localProvs, s.providers)
	globalProvMu.RLock()
	allProvs := make([]Provider, 0, len(localProvs)+len(globalProvs))
	allProvs = append(allProvs, localProvs...)
	allProvs = append(allProvs, globalProvs...)
	globalProvMu.RUnlock()
	for _, prov := range allProvs {
		_ = prov.OnMemoryWrite(ctx, target, ops[len(ops)-1].Action, raw)
	}
	return res, nil
}

// FormatForPrompt formats a snapshot block matching Hermes's MEMORY.md/USER.md header format.
func FormatForPrompt(snap Snapshot) string {
	var b strings.Builder
	writeBlock(&b, "MEMORY (your personal notes)", snap.MemoryEntries, snap.MemoryLimit)
	writeBlock(&b, "USER PROFILE (who you're talking to)", snap.UserEntries, snap.UserLimit)
	return strings.TrimSpace(b.String())
}

func writeBlock(b *strings.Builder, title string, entries []string, limit int) {
	used := charCount(entries)
	pct := 0
	if limit > 0 {
		pct = (used * 100) / limit
	}
	fmt.Fprintf(b, "══════════════════════════════════════════════\n%s [%d%% — %d/%d chars]\n══════════════════════════════════════════════\n", title, pct, used, limit)
	if len(entries) == 0 {
		b.WriteString("(empty)\n\n")
		return
	}
	b.WriteString(strings.Join(entries, EntryDelimiter))
	b.WriteString("\n\n")
}

func readEntries(path string) []string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return nil
	}
	parts := strings.Split(text, EntryDelimiter)
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if t := strings.TrimSpace(part); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func charCount(entries []string) int {
	if len(entries) == 0 {
		return 0
	}
	return len([]rune(strings.Join(entries, EntryDelimiter)))
}

func containsExact(entries []string, target string) bool {
	for _, e := range entries {
		if e == target {
			return true
		}
	}
	return false
}

func findUniqueMatch(entries []string, needle string) (int, error) {
	var matches []int
	for i, e := range entries {
		if strings.Contains(e, needle) {
			matches = append(matches, i)
		}
	}
	if len(matches) == 0 {
		return -1, fmt.Errorf("no entry matches %q", needle)
	}
	if len(matches) > 1 {
		return -1, fmt.Errorf("multiple entries (%d) match %q; provide a more specific substring", len(matches), needle)
	}
	return matches[0], nil
}

func writeAtomic(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".mem-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}
