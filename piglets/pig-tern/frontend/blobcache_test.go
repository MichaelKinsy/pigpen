package frontend

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

// readyBlobCache is ready with Tern's blob cache: TERN_BLOB_DIR set and the
// blobs feature in the hello reply.
func readyBlobCache(t *testing.T, dir string) *harness {
	t.Helper()
	h := newHarnessEnv(t, map[string]string{"TERN_BLOB_DIR": dir})
	h.reply(`,"credits":64,"cols":137,"cell":{"w":8,"h":16},"features":["blobs"]`)
	h.session.InputReady()
	h.apply()
	h.frames()
	return h
}

// blobQueries returns the ids of the blobs queries written since the last
// call, and the blobs sent through the pty.
func (h *harness) blobQueries() (queried []string, sent int, verbs []string) {
	for _, m := range h.messages() {
		verbs = append(verbs, m.verb)
		switch m.verb {
		case "q":
			if m.body["q"] == "blobs" {
				for _, id := range m.body["ids"].([]any) {
					queried = append(queried, id.(string))
				}
			}
		case "b":
			sent++
		}
	}
	return queried, sent, verbs
}

// With Tern's blob cache, an image is written to the cache folder under its
// id, never as a partial file, and Tern is asked whether it holds it; the
// frame that shows it goes out at once, and no bytes cross the pty when
// Tern holds it.
func TestBlobCacheWritesTheImageAndSkipsThePty(t *testing.T) {
	dir := t.TempDir()
	h := readyBlobCache(t, dir)
	view, lines := kitView(t)
	ref := view.Root.Children[8].Image.Ref
	h.apply(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "ext", Node: fe.Lines{Lines: lines, View: view}})
	h.eventually("the blobs query", func() bool { return strings.Contains(h.out.peek(), `"q":"blobs"`) })
	queried, sent, verbs := h.blobQueries()
	if !reflect.DeepEqual(queried, []string{ref}) || sent != 0 || !slices.Contains(verbs, "f") {
		t.Fatalf("queried %v, sent %v, verbs %v; want a query for %s, no blob, and the frame", queried, sent, verbs, ref)
	}
	data, err := os.ReadFile(filepath.Join(dir, ref))
	if err != nil || !reflect.DeepEqual(data, view.Root.Children[8].Image.Data) {
		t.Fatalf("cache file = %d bytes, %v", len(data), err)
	}
	if tmp, _ := filepath.Glob(filepath.Join(dir, "*.tmp*")); len(tmp) != 0 {
		t.Fatalf("temporary files left: %v", tmp)
	}

	h.session.HandleInput("\x1b_tsp;r;{\"r\":\"blobs\",\"have\":[\"" + ref + "\"]}\x1b\\")
	h.synced()
	if _, sent, _ := h.blobQueries(); sent != 0 {
		t.Fatalf("a held image went through the pty: %v", sent)
	}
}

// When Tern does not hold a cached image, it goes through the pty, once.
func TestBlobCacheMissSendsThroughThePty(t *testing.T) {
	h := readyBlobCache(t, t.TempDir())
	view, lines := kitView(t)
	h.apply(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "ext", Node: fe.Lines{Lines: lines, View: view}})
	h.eventually("the blobs query", func() bool { return strings.Contains(h.out.peek(), `"q":"blobs"`) })
	h.blobQueries()
	h.session.HandleInput("\x1b_tsp;r;{\"r\":\"blobs\",\"have\":[]}\x1b\\")
	h.eventually("the blob", func() bool { return strings.Contains(h.out.peek(), "\x1b_tsp;b;") })
	if _, sent, _ := h.blobQueries(); sent != 1 {
		t.Fatalf("blobs through the pty = %v, want one", sent)
	}
	next := *view
	next.Seq = 2
	h.apply(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "ext2", Index: 1, Node: fe.Lines{Lines: lines, View: &next}})
	if queried, sent, _ := h.blobQueries(); len(queried) != 0 || sent != 0 {
		t.Fatalf("the image was handed over again: queried %v, sent %v", queried, sent)
	}
}

// Without the blobs feature, TERN_BLOB_DIR is not used.
func TestBlobCacheNeedsTheFeature(t *testing.T) {
	dir := t.TempDir()
	h := readyEnv(t, map[string]string{"TERN_BLOB_DIR": dir})
	view, lines := kitView(t)
	h.apply(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "ext", Node: fe.Lines{Lines: lines, View: view}})
	if queried, sent, _ := h.blobQueries(); len(queried) != 0 || sent != 1 {
		t.Fatalf("queried %v, sent %v; want the blob through the pty", queried, sent)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("cache written without the feature: %v", entries)
	}
}
