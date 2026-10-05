package pi_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/pi"
	"github.com/MichaelKinsy/pigpen/ahp/internal/testkit"
	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

// Twins of upstream test/session-catalogue.test.ts: ordering, pagination, title derivation, live
// overlays, over a synthetic sessions directory.

func list(t *testing.T, c *pi.Catalogue, limit int64, cursor *string, live func() []pi.LiveCatalogueEntry) ahptypes.ListSessionsResult {
	t.Helper()
	var l *int64
	if limit > 0 {
		l = &limit
	}
	result, err := c.List(context.Background(), l, cursor, live)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func resources(items []ahptypes.SessionSummary) []string {
	out := []string{}
	for _, i := range items {
		out = append(out, i.Resource)
	}
	return out
}

func TestSessionCatalogue(t *testing.T) {
	root := t.TempDir()
	var ids []string
	// Interleave two working directories so the walk covers more than one session dir.
	for i := 0; i < 5; i++ {
		id := newID()
		ids = append(ids, id)
		cwd := "/tmp/project-b"
		if i%2 == 0 {
			cwd = "/tmp/project a"
		}
		writeFakeSession(t, root, id, fakeSession{cwd: cwd, firstUserMessage: "Message number " + itoa(i), mtimeSeconds: 1_700_000_000 + int64(i)})
	}
	catalogue := pi.NewCatalogue(root)

	twin.Run(t, "session-catalogue", "returns most-recently-modified first, across working directories", func(t *testing.T) {
		result := list(t, catalogue, 0, nil, nil)
		if len(result.Items) != 5 {
			t.Fatalf("%d items", len(result.Items))
		}
		var want []string
		for i := len(ids) - 1; i >= 0; i-- {
			want = append(want, wire.SessionURI(ids[i]))
		}
		if got := resources(result.Items); !reflect.DeepEqual(got, want) {
			t.Fatalf("order = %v", got)
		}
		if got := result.Items[0].WorkingDirectories; !reflect.DeepEqual(got, []ahptypes.URI{wire.PathToFileURI("/tmp/project a")}) {
			t.Fatalf("workingDirectories = %v", got)
		}
	})

	twin.Run(t, "session-catalogue", "paginates with an opaque cursor and stops at the end", func(t *testing.T) {
		first := list(t, catalogue, 2, nil, nil)
		if len(first.Items) != 2 || first.NextCursor == nil {
			t.Fatalf("first page = %+v", first)
		}
		second := list(t, catalogue, 2, first.NextCursor, nil)
		if len(second.Items) != 2 || second.NextCursor == nil {
			t.Fatalf("second page = %+v", second)
		}
		third := list(t, catalogue, 2, second.NextCursor, nil)
		// A missing nextCursor is what signals the end of the catalogue.
		if len(third.Items) != 1 || third.NextCursor != nil {
			t.Fatalf("third page = %+v", third)
		}
		seen := map[string]bool{}
		for _, page := range [][]ahptypes.SessionSummary{first.Items, second.Items, third.Items} {
			for _, item := range page {
				seen[item.Resource] = true
			}
		}
		if len(seen) != 5 {
			t.Fatalf("pages overlap: %d distinct", len(seen))
		}
	})

	twin.Run(t, "session-catalogue", "rejects a malformed cursor with InvalidParams", func(t *testing.T) {
		bad := "not-a-cursor"
		_, err := catalogue.List(context.Background(), ptr(int64(2)), &bad, nil)
		var werr *wire.Error
		if !errors.As(err, &werr) || werr.Code != wire.CodeInvalidParams {
			t.Fatalf("err = %v", err)
		}
	})

	twin.Run(t, "session-catalogue", "titles a session by its name, falling back to the first user message", func(t *testing.T) {
		writeFakeSession(t, root, newID(), fakeSession{cwd: "/tmp/project-c", firstUserMessage: "This should lose to the explicit name", name: "Nightly refactor", mtimeSeconds: 1_700_001_000})
		result := list(t, catalogue, 1, nil, nil)
		// Same rule Pi's own /resume picker uses: name ?? firstMessage.
		if result.Items[0].Title != "Nightly refactor" {
			t.Fatalf("title = %q", result.Items[0].Title)
		}
		unnamed := list(t, catalogue, 2, nil, nil)
		if unnamed.Items[1].Title != "Message number 4" {
			t.Fatalf("title = %q", unnamed.Items[1].Title)
		}
	})

	twin.Run(t, "session-catalogue", "skips files that are not readable pi sessions", func(t *testing.T) {
		before := list(t, catalogue, 0, nil, nil)
		dir := filepath.Join(root, "--tmp-project-broken--")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "1700002000_broken.jsonl"), []byte("not json at all\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		// One corrupt file must neither appear nor take down the catalogue.
		if after := list(t, catalogue, 0, nil, nil); !reflect.DeepEqual(after, before) {
			t.Fatalf("catalogue changed: %+v vs %+v", after, before)
		}
	})

	twin.Run(t, "session-catalogue", "returns an empty catalogue when nothing exists yet", func(t *testing.T) {
		empty := pi.NewCatalogue(filepath.Join(root, "does-not-exist"))
		if got := list(t, empty, 0, nil, nil); len(got.Items) != 0 || got.NextCursor != nil {
			t.Fatalf("got %+v", got)
		}
	})

	twin.Run(t, "session-catalogue", "does not return a cached path after its session file is removed", func(t *testing.T) {
		isolatedRoot := t.TempDir()
		id := newID()
		file := writeFakeSession(t, isolatedRoot, id, fakeSession{cwd: "/tmp/cached", firstUserMessage: "cache me", mtimeSeconds: 1_700_003_000})
		isolated := pi.NewCatalogue(isolatedRoot)
		list(t, isolated, 0, nil, nil)
		if got, _ := isolated.FindSessionFile(id); got != file {
			t.Fatalf("found %q, want %q", got, file)
		}
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
		if got, _ := isolated.FindSessionFile(id); got != "" {
			t.Fatalf("found a removed file: %q", got)
		}
	})
}

func ptr[T any](v T) *T { return &v }

func liveSummary(id, title string, status ahptypes.SessionStatus, modified time.Time) ahptypes.SessionSummary {
	return ahptypes.SessionSummary{
		Resource: wire.SessionURI(id), Provider: "pi", Title: title, Status: status,
		CreatedAt: time.UnixMilli(0).UTC().Format("2006-01-02T15:04:05.000Z"), ModifiedAt: modified.UTC().Format("2006-01-02T15:04:05.000Z"),
	}
}

func TestLiveSessionCatalogueOverlays(t *testing.T) {
	twin.Run(t, "session-catalogue", "reads live status after disk discovery rather than freezing request-start state", func(t *testing.T) {
		// Upstream races an async scan against a state change. The Go scan is synchronous, so the
		// property kept is that live state is read exactly once, when the page is built.
		root := t.TempDir()
		current := liveSummary(newID(), "Live", ahptypes.SessionStatusInProgress, time.UnixMilli(1000))
		reads := 0
		source := func() []pi.LiveCatalogueEntry {
			reads++
			return []pi.LiveCatalogueEntry{{Summary: current}}
		}
		current.Status, current.ModifiedAt = ahptypes.SessionStatusIdle, "1970-01-01T00:00:02.000Z"
		got := list(t, pi.NewCatalogue(root), 1, nil, source)
		if !reflect.DeepEqual(got.Items, []ahptypes.SessionSummary{current}) || got.NextCursor != nil {
			t.Fatalf("got %+v", got)
		}
		if reads != 1 {
			t.Fatalf("live source read %d times", reads)
		}
	})

	twin.Run(t, "session-catalogue", "keeps a live entry stable as its file appears, without caching a nonexistent file", func(t *testing.T) {
		root := t.TempDir()
		id := newID()
		options := fakeSession{cwd: "/tmp/live", firstUserMessage: "disk", mtimeSeconds: 100}
		file := writeFakeSession(t, root, id, options)
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
		live := liveSummary(id, "Live", ahptypes.SessionStatusInProgress, time.UnixMilli(200000))
		catalogue := pi.NewCatalogue(root)
		source := func() []pi.LiveCatalogueEntry { return []pi.LiveCatalogueEntry{{File: file, Summary: live}} }
		if got := list(t, catalogue, 1, nil, source); !reflect.DeepEqual(got.Items, []ahptypes.SessionSummary{live}) {
			t.Fatalf("got %+v", got)
		}
		if _, cached := catalogue.FileFor(live.Resource); cached {
			t.Fatal("cached a nonexistent file")
		}
		writeFakeSession(t, root, id, options)
		if got := list(t, catalogue, 1, nil, source); !reflect.DeepEqual(got.Items, []ahptypes.SessionSummary{live}) {
			t.Fatalf("got %+v", got)
		}
		if got, _ := catalogue.FileFor(live.Resource); got != file {
			t.Fatalf("cached %q, want %q", got, file)
		}
	})

	twin.Run(t, "session-catalogue", "orders and paginates live summaries without duplicating their files", func(t *testing.T) {
		root := t.TempDir()
		replacedID, olderID, liveOnlyID := newID(), newID(), newID()
		replacedFile := writeFakeSession(t, root, replacedID, fakeSession{cwd: "/tmp/replaced", firstUserMessage: "stale disk title", mtimeSeconds: 100})
		writeFakeSession(t, root, olderID, fakeSession{cwd: "/tmp/older", firstUserMessage: "older", mtimeSeconds: 200})
		summary := func(id, title string, seconds int64) ahptypes.SessionSummary {
			return liveSummary(id, title, ahptypes.SessionStatusInProgress, time.Unix(seconds, 0))
		}
		liveEntries := []pi.LiveCatalogueEntry{
			{File: replacedFile, Summary: summary(replacedID, "live replacement", 400)},
			{Summary: summary(liveOnlyID, "not on disk", 300)},
		}
		source := func() []pi.LiveCatalogueEntry { return liveEntries }
		catalogue := pi.NewCatalogue(root)
		first := list(t, catalogue, 2, nil, source)
		if len(first.Items) != 2 || first.NextCursor == nil {
			t.Fatalf("first = %+v", first)
		}
		second := list(t, catalogue, 2, first.NextCursor, source)
		items := append(append([]ahptypes.SessionSummary{}, first.Items...), second.Items...)
		want := []string{wire.SessionURI(replacedID), wire.SessionURI(liveOnlyID), wire.SessionURI(olderID)}
		if got := resources(items); !reflect.DeepEqual(got, want) {
			t.Fatalf("order = %v", got)
		}
		if items[0].Title != "live replacement" || items[0].Status != ahptypes.SessionStatusInProgress {
			t.Fatalf("first item = %+v", items[0])
		}
		if second.NextCursor != nil {
			t.Fatal("expected the end of the catalogue")
		}
	})
}

func TestListSessionsOverTheWire(t *testing.T) {
	twin.Run(t, "session-catalogue", "serves a schema-conforming page", func(t *testing.T) {
		root := t.TempDir()
		writeFakeSession(t, root, newID(), fakeSession{cwd: "/tmp/wire", firstUserMessage: "Explain this repository", mtimeSeconds: 1_700_100_000})
		h := startHarness(t, harnessOptions{sessionRoot: root})
		client := testkit.Connect(t, h.host)
		t.Cleanup(client.Close)
		client.Initialize(nextClientID(), nil)
		raw := client.Must("listSessions", obj{"channel": wire.RootChannel})
		var result struct{ Items []obj }
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Items) != 1 || result.Items[0]["title"] != "Explain this repository" {
			t.Fatalf("result = %s", raw)
		}
		testkit.AssertValid(t, "commands", "ListSessionsResult", testkit.Normalize(t, raw))
	})
}
