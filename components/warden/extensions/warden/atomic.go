package warden

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
)

// Whole-file writes and per-key queues for warden's small JSON stores (port of src/atomic.ts). Every write goes to a
// temporary file that belongs to this process and this write, then a rename puts it in place; every read-modify-write
// of one file runs through Serialize keyed by that path. Cross-process locking is out of scope.

var atomicWrites atomic.Uint64

// WriteFileAtomic writes data to path through a temporary file and a rename, so a crash or a reader never sees half
// a file. The directory is created 0700 and the file is 0600.
func WriteFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + "." + strconv.Itoa(os.Getpid()) + "." + strconv.FormatUint(atomicWrites.Add(1), 10) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	// The mode applies only when the file is created; force it so a leftover file cannot change the store's permissions.
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// queue is one key's line of waiting tasks.
type queue struct {
	mu      sync.Mutex // held by the running task
	waiting int        // tasks running or waiting; guarded by queuesMu
}

var (
	queuesMu sync.Mutex
	queues   = map[string]*queue{}
)

// Serialize runs task after every earlier task for key, so read-modify-write sequences sharing a store file run one
// after another. A task's error reaches only its own caller; the key's entry is freed when it is idle.
func Serialize[T any](key string, task func() (T, error)) (T, error) {
	queuesMu.Lock()
	q := queues[key]
	if q == nil {
		q = &queue{}
		queues[key] = q
	}
	q.waiting++
	queuesMu.Unlock()

	q.mu.Lock()
	defer func() {
		q.mu.Unlock()
		queuesMu.Lock()
		q.waiting--
		if q.waiting == 0 {
			delete(queues, key)
		}
		queuesMu.Unlock()
	}()
	return task()
}
