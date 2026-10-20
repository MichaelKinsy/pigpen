package changeset

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Watcher is a running observation of a set of paths.
type Watcher interface{ Close() error }

// WatchFunc starts observing paths, calls changed with the path of every file or directory that is
// added, changed or removed (never for an ignored path), and returns once the initial scan is done.
// ctx bounds only that initial scan; the watch lives until Close. A later failure is reported through onError. It is the counterpart of chokidar's watch() with
// ignoreInitial and followSymlinks off.
type WatchFunc func(ctx context.Context, paths []string, ignored func(path string) bool, changed func(path string), onError func(error)) (Watcher, error)

// DefaultPollInterval is how often [PollWatch] looks at the tree. The standard library has no
// portable file notification, so, as for resource watches, a snapshot of the tree is diffed.
const DefaultPollInterval = time.Second

// pathSignature is what a poll compares: a change of any of it counts as a change of the path.
type pathSignature struct {
	dir     bool
	size    int64
	modTime int64
	mode    fs.FileMode
}

type pollWatcher struct {
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

func (w *pollWatcher) Close() error {
	w.stopOnce.Do(func() { close(w.stop) })
	<-w.done
	return nil
}

// PollWatch returns a [WatchFunc] that polls every interval.
func PollWatch(interval time.Duration) WatchFunc {
	if interval <= 0 {
		interval = DefaultPollInterval
	}
	return func(ctx context.Context, paths []string, ignored func(string) bool, changed func(string), onError func(error)) (Watcher, error) {
		snapshot, err := scan(ctx, paths, ignored)
		if err != nil {
			return nil, err
		}
		w := &pollWatcher{stop: make(chan struct{}), done: make(chan struct{})}
		go func() {
			defer close(w.done)
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-w.stop:
					return
				case <-ticker.C:
				}
				next, err := scan(context.Background(), paths, ignored)
				if err != nil {
					if onError != nil {
						onError(err)
					}
					return
				}
				for path, sig := range next {
					if old, existed := snapshot[path]; !existed || old != sig {
						changed(path)
					}
				}
				for path := range snapshot {
					if _, still := next[path]; !still {
						changed(path)
					}
				}
				snapshot = next
			}
		}()
		return w, nil
	}
}

func scan(ctx context.Context, roots []string, ignored func(string) bool) (map[string]pathSignature, error) {
	out := map[string]pathSignature{}
	var walk func(path string, info fs.FileInfo) error
	walk = func(path string, info fs.FileInfo) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !info.IsDir() {
			out[path] = pathSignature{size: info.Size(), modTime: info.ModTime().UnixNano(), mode: info.Mode()}
			return nil
		}
		// A directory's own size and times change with its entries, which are reported on their own.
		out[path] = pathSignature{dir: true}
		children, err := os.ReadDir(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
				return nil
			}
			return err
		}
		for _, child := range children {
			childPath := filepath.Join(path, child.Name())
			if ignored(childPath) {
				continue
			}
			childInfo, err := child.Info()
			if err != nil {
				continue
			}
			if err := walk(childPath, childInfo); err != nil {
				return err
			}
		}
		return nil
	}
	for _, root := range roots {
		if ignored(root) {
			continue
		}
		info, err := os.Lstat(root)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
				continue
			}
			return nil, err
		}
		if err := walk(root, info); err != nil {
			return nil, err
		}
	}
	return out, nil
}
