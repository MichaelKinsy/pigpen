package pisession

import "testing"

func TestFromEntriesBuildsATreeWithASynthesisedHeader(t *testing.T) {
	entries := []Entry{
		{"type": "message", "id": "a", "parentId": nil, "timestamp": "2025-01-01T00:00:00.000Z", "message": map[string]any{"role": "user", "content": "hi"}},
		{"type": "message", "id": "b", "parentId": "a", "timestamp": "2025-01-01T00:00:01.000Z", "message": map[string]any{"role": "assistant", "content": "yo"}},
	}
	m, err := FromEntries("/work", "sid", "/f.jsonl", entries, "")
	if err != nil {
		t.Fatal(err)
	}
	if m.SessionID() != "sid" || m.Cwd() != "/work" || m.File() != "/f.jsonl" || m.LeafEntry().ID() != "b" {
		t.Fatalf("%s %s %s %v", m.SessionID(), m.Cwd(), m.File(), m.LeafEntry())
	}
	if got := len(m.Branch("")); got != 2 {
		t.Fatalf("branch of %d", got)
	}
	// an explicit leaf moves it
	at, err := FromEntries("/work", "sid", "", entries, "a")
	if err != nil || at.LeafEntry().ID() != "a" {
		t.Fatalf("%v %v", at, err)
	}
	if _, err := FromEntries("/work", "sid", "", entries, "missing"); err == nil {
		t.Fatal("an unknown leaf must be an error")
	}
	empty, err := FromEntries("/work", "sid", "", nil, "")
	if err != nil || empty.LeafEntry() != nil {
		t.Fatalf("an empty session has no leaf: %v %v", empty, err)
	}
}
