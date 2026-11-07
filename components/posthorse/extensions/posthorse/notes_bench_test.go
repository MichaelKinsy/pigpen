package posthorse

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// notesBenchTree writes count notes of about 200 bytes (every tenth holds the word
// "needle") under a project notes directory and returns an in-process session over it.
// The calls go straight to listNotes/searchNotes/appendRecord: they need no host.
func notesBenchTree(b *testing.B, count int) (*sessionNotes, notesAccess) {
	b.Helper()
	dir, err := filepath.EvalSymlinks(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	root := filepath.Join(dir, ".pi", "notes")
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range count {
		path := filepath.Join(root, fmt.Sprintf("topic-%02d", i%20), fmt.Sprintf("note-%04d.md", i))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			b.Fatal(err)
		}
		text := fmt.Sprintf("# Note %d\nstate line %s\n", i, strings.Repeat("s", 150))
		if i%10 == 0 {
			text += "found the needle here\n"
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			b.Fatal(err)
		}
		at := base.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(path, at, at); err != nil {
			b.Fatal(err)
		}
	}
	binding := &noteBinding{kind: "project", owner: noteOwner{id: "bench", timestamp: "2026-01-01T00:00:00.000Z"}, root: root}
	session := newSessionNotes(binding, nil, noteSource{})
	tokens := 1000
	host := fakePolicy{
		Settings: compactionPolicy{reserveTokens: 16_384},
		Usage:    &contextUsage{tokens: &tokens, contextWindow: 100_000},
	}
	access := notesAccess{
		ctx:       sdk.Context{},
		host:      func() policyContext { return host },
		pages:     newPageReservations(func(sdk.Context) (int, error) { return 0, nil }),
		call:      session.call("bench"),
		discovery: newNoteDiscovery(),
	}
	return session, access
}

func BenchmarkNotesList1000(b *testing.B) {
	session, access := notesBenchTree(b, 1000)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		access.pages.(*pageReservations).reset()
		if _, err := listNotes(session, notesParams{op: "list"}, access); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkNotesSearch1000(b *testing.B) {
	session, access := notesBenchTree(b, 1000)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		access.pages.(*pageReservations).reset()
		if _, err := searchNotes(session, notesParams{op: "search", query: "needle", hasQuery: true}, access); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkNotesAppendLarge appends a record to a 8 MiB note: the original read the whole
// file to decide on the separator; only its last byte is needed.
func BenchmarkNotesAppendLarge(b *testing.B) {
	path := filepath.Join(b.TempDir(), "big.md")
	if err := os.WriteFile(path, []byte(strings.Repeat("0123456789abcdef\n", 8<<16)), 0o644); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := appendRecord(path, "one more record"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkNotesMatchingLines scans one 1 MiB text note for a query it only matches at the end.
func BenchmarkNotesMatchingLines(b *testing.B) {
	ascii := strings.Repeat("a line of plain note text that is long enough to matter\n", 1<<14) + "the Needle\n"
	unicode := strings.Repeat("a line of note text with an accent é that is long enough\n", 1<<14) + "the Needle\n"
	for name, text := range map[string]string{"ascii": ascii, "unicode": unicode} {
		b.Run(name, func(b *testing.B) {
			b.SetBytes(int64(len(text)))
			b.ReportAllocs()
			for range b.N {
				if rows := matchingLines("big.md", text, "needle"); len(rows) != 1 {
					b.Fatalf("rows %v", rows)
				}
			}
		})
	}
}
