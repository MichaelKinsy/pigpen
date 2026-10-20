package warden

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestSerializeNeverInterleaves(t *testing.T) {
	var running, maxRunning, total int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = Serialize("k", func() (int, error) {
				mu.Lock()
				running++
				if running > maxRunning {
					maxRunning = running
				}
				mu.Unlock()
				v := total
				runtime.Gosched()
				total = v + 1
				mu.Lock()
				running--
				mu.Unlock()
				return 0, nil
			})
		}()
	}
	wg.Wait()
	if maxRunning != 1 || total != 50 {
		t.Fatalf("maxRunning=%d total=%d, want 1 and 50", maxRunning, total)
	}
	queuesMu.Lock()
	defer queuesMu.Unlock()
	if len(queues) != 0 {
		t.Fatalf("queue entries leaked: %d", len(queues))
	}
}

func TestSerializeErrorDoesNotStopQueue(t *testing.T) {
	boom := errors.New("boom")
	if _, err := Serialize("e", func() (int, error) { return 0, boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	v, err := Serialize("e", func() (string, error) { return "ok", nil })
	if err != nil || v != "ok" {
		t.Fatalf("got %q, %v", v, err)
	}
}

func TestWriteFileAtomic(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b")
	path := filepath.Join(dir, "store.json")
	for _, body := range []string{"one", "two"} {
		if err := WriteFileAtomic(path, []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "two" {
		t.Fatalf("got %q, %v", got, err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temp file left behind: %v", entries)
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(path)
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %v", st.Mode().Perm())
		}
	}
}
