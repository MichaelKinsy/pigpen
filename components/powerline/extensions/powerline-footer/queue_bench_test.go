package powerline_footer

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// What the queue costs where it is read: the summary behind the queue segment and the preview row. It is read in the handler
// (Repaint), never on the footer render goroutine: TestFooterRenderDoesNotReadTheInbox.

func seedInbox(b testing.TB, dir string, n int) *queueStore {
	store := newQueueStore(filepath.Join(dir, "inbox.jsonl"), filepath.Join(dir, "projects.json"))
	for i := 0; i < n; i++ {
		status := statusQueued
		if i%7 == 0 {
			status = statusBlocked
		}
		if _, err := store.add(createQueueItemInput{Text: "prompt number " + strconv.Itoa(i) + " with some text", Source: queueSource{Cwd: dir}, Target: queueTarget{Kind: targetGlobal}, Intent: intentFollowUp, Status: status, Now: new(float64(i))}); err != nil {
			b.Fatal(err)
		}
	}
	return store
}

// BenchmarkQueueSummary: warm is a summary of an unchanged inbox (one stat and one open, the snapshot answers); cold reads and
// parses the inbox, as the first summary after any change does.
func BenchmarkQueueSummary(b *testing.B) {
	for _, n := range []int{0, 50, 500} {
		b.Run("warm/"+strconv.Itoa(n), func(b *testing.B) {
			dir := b.TempDir()
			store := seedInbox(b, dir, n)
			ctx := currentQueueContext(dir, "")
			store.summarize(ctx, false)
			b.ReportAllocs()
			for b.Loop() {
				if _, err := store.summarize(ctx, false); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run("cold/"+strconv.Itoa(n), func(b *testing.B) {
			dir := b.TempDir()
			store := seedInbox(b, dir, n)
			ctx := currentQueueContext(dir, "")
			b.ReportAllocs()
			for b.Loop() {
				store.mu.Lock()
				store.snapshot = nil
				store.mu.Unlock()
				if _, err := store.summarize(ctx, false); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// The footer renderer lays out the published frame only: it runs on the SDK's goroutine and must not touch the inbox.
func TestFooterRenderDoesNotReadTheInbox(t *testing.T) {
	r := newQueueRig(t)
	r.item("queued", intentFollowUp, queueTarget{Kind: targetGlobal})
	r.emit("session_start", map[string]any{"reason": "startup"})
	r.p.refresh() // the handler reads the summary
	read, stat := queueReadFile, queueStatFile
	defer func() { queueReadFile, queueStatFile = read, stat }()
	touched := false
	queueReadFile = func(path string) ([]byte, error) { touched = true; return read(path) }
	queueStatFile = func(path string) (os.FileInfo, error) { touched = true; return stat(path) }
	r.p.footerRows(80)
	eq(t, touched, false, "footerRows read the inbox")
}
