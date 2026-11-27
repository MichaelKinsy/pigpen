// Package todo provides a shared session-scoped checklist store used by both
// pig-hermes's todo_list tool and Phase 2's omh_todo tool.
package todo

import (
	"fmt"
	"strings"
	"sync"
)

// Item represents one task in the session checklist.
type Item struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Status  string `json:"status"` // "pending", "in_progress", "completed", "cancelled"
}

// Store holds per-session todo lists in memory (mirrored to PiG custom entries by caller).
type Store struct {
	mu       sync.RWMutex
	sessions map[string][]Item
}

// NewStore creates a new in-memory todo Store.
func NewStore() *Store {
	return &Store{sessions: make(map[string][]Item)}
}

var defaultStore = NewStore()

// Default returns the process-wide shared Store so pig-hermes and oh-my-hermes-pig stay unified.
func Default() *Store {
	return defaultStore
}

// Get returns a copy of the items for sessionID.
func (s *Store) Get(sessionID string) []Item {
	s.mu.RLock()
	defer s.mu.RUnlock()
	src := s.sessions[sessionID]
	out := make([]Item, len(src))
	copy(out, src)
	return out
}

// Replace validates and replaces the todo list for sessionID.
func (s *Store) Replace(sessionID string, items []Item) ([]Item, error) {
	cleaned := make([]Item, 0, len(items))
	seen := make(map[string]bool, len(items))
	for i, it := range items {
		id := strings.TrimSpace(it.ID)
		if id == "" {
			id = fmt.Sprintf("%d", i+1)
		}
		if seen[id] {
			return nil, fmt.Errorf("duplicate todo id %q", id)
		}
		seen[id] = true
		content := strings.TrimSpace(it.Content)
		if content == "" {
			return nil, fmt.Errorf("todo item %q has empty content", id)
		}
		status := strings.ToLower(strings.TrimSpace(it.Status))
		if status == "" {
			status = "pending"
		}
		switch status {
		case "pending", "in_progress", "completed", "cancelled":
		default:
			return nil, fmt.Errorf("todo item %q has invalid status %q", id, it.Status)
		}
		cleaned = append(cleaned, Item{ID: id, Content: content, Status: status})
	}
	s.mu.Lock()
	s.sessions[sessionID] = cleaned
	s.mu.Unlock()

	out := make([]Item, len(cleaned))
	copy(out, cleaned)
	return out, nil
}

// FormatSummary renders a concise markdown checklist for tool results and post-compaction injection.
func FormatSummary(items []Item) string {
	if len(items) == 0 {
		return "No todo items."
	}
	var b strings.Builder
	for _, it := range items {
		mark := "[ ]"
		switch it.Status {
		case "in_progress":
			mark = "[~]"
		case "completed":
			mark = "[x]"
		case "cancelled":
			mark = "[-]"
		}
		fmt.Fprintf(&b, "- %s (%s) %s\n", mark, it.ID, it.Content)
	}
	return strings.TrimRight(b.String(), "\n")
}
