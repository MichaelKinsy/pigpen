package posthorse

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
)

// isLinkEntry is `dirent.isSymbolicLink()`: a symlink, and on Windows a junction, which Go reports as an
// irregular file and libuv's scandir reports as a link.
func isLinkEntry(entry fs.DirEntry) bool {
	t := entry.Type()
	return t&fs.ModeSymlink != 0 || (runtime.GOOS == "windows" && t&fs.ModeIrregular != 0)
}

// Port of src/note-inventory.ts.

// noteFile is NoteFile (note-inventory.ts:11): an identity and the file's path.
type noteFile struct {
	noteIdentity
	path string
}

// walkBatch is WALK_BATCH (note-inventory.ts:13): the maximum number of overlapped stats.
const walkBatch = 64

// noteStatHook is a test seam: it runs after every successful stat of the walk (possibly from
// several goroutines at once). The original's tests observe `fs.promises.stat` the same way.
var noteStatHook func(path string)

// statIfPresent: note-inventory.ts:15.
func statIfPresent(path string) (fs.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		if noteMissing(err) {
			return nil, nil
		}
		return nil, nodeFsError(err, "stat", path)
	}
	if hook := noteStatHook; hook != nil {
		hook(path)
	}
	return info, nil
}

// walkEntry is what folderEntries yields.
type walkEntry struct {
	path  string
	info  fs.FileInfo
	alias bool
}

// folderEntries: note-inventory.ts:25. Preserve depth-first listing order and recheck later entries
// after visiting each folder: the stats of a batch stop after its first folder or link, and the
// entries after a folder are listed again from where the walk resumes. visit returns false to stop.
func folderEntries(ctx context.Context, directory string, visit func(walkEntry) error) error {
	entries, err := readDirNode(directory)
	if err != nil {
		return err
	}
	start := 0
	for start < len(entries) {
		if err := ctxErr(ctx); err != nil {
			return err
		}
		batch := entries[start:min(start+walkBatch, len(entries))]
		count := len(batch)
		for i, entry := range batch {
			if entry.IsDir() || isLinkEntry(entry) {
				count = i + 1
				break
			}
		}
		infos := make([]fs.FileInfo, count)
		errs := make([]error, count)
		if count == 1 {
			infos[0], errs[0] = statIfPresent(filepath.Join(directory, batch[0].Name()))
		} else {
			var wg sync.WaitGroup
			wg.Add(count)
			for i := range count {
				go func() {
					defer wg.Done()
					infos[i], errs[i] = statIfPresent(filepath.Join(directory, batch[i].Name()))
				}()
			}
			wg.Wait()
		}
		for _, err := range errs {
			if err != nil {
				return err
			}
		}
		for i := range count {
			start++
			info := infos[i]
			if info != nil {
				path := filepath.Join(directory, batch[i].Name())
				if err := visit(walkEntry{path: path, info: info, alias: isLinkEntry(batch[i])}); err != nil {
					return err
				}
			}
			if info != nil && info.IsDir() {
				break
			}
		}
	}
	return nil
}

// walkState is WalkState (note-inventory.ts:49).
type walkState struct {
	ancestors []string
	scope     noteScope
	notice    func(text string)
}

// allowedTarget: note-inventory.ts:54.
func allowedTarget(path string, state walkState) (bool, error) {
	scopeRoot, err := canonicalPath(scopeDirectory(state.scope))
	if err != nil {
		return false, err
	}
	if state.scope.scope == "" {
		forks, err := canonicalPath(filepath.Join(state.scope.root, ".forks"))
		if err != nil {
			return false, err
		}
		return !inside(forks, path), nil
	}
	if inside(scopeRoot, path) {
		return true, nil
	}
	state.notice("Not indexed: alias outside private notes scope (" + path + ").")
	return false, nil
}

// walk: note-inventory.ts:66.
func walk(ctx context.Context, directory string, state walkState) ([]noteFile, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	root, err := realpathNative(directory)
	if err != nil {
		return nil, err
	}
	for _, ancestor := range state.ancestors {
		if ancestor == root {
			return nil, nil
		}
	}
	if allowed, err := allowedTarget(root, state); err != nil || !allowed {
		return nil, err
	}
	next := state
	next.ancestors = append(append(make([]string, 0, len(state.ancestors)+1), state.ancestors...), root)
	var files []noteFile
	err = folderEntries(ctx, directory, func(e walkEntry) error {
		if e.info.IsDir() {
			nested, err := walk(ctx, e.path, next)
			files = append(files, nested...)
			return err
		}
		if e.alias {
			canonical, err := canonicalPath(e.path)
			if err != nil {
				return err
			}
			if allowed, err := allowedTarget(canonical, state); err != nil || !allowed {
				return err
			}
		}
		files = append(files, noteFile{noteIdentity: fileIdentity(e.path, e.info), path: e.path})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

// notesUnder: note-inventory.ts:91. folder nil is undefined; ctx may be nil.
func notesUnder(ctx context.Context, scope noteScope, folder *string, notice func(text string)) ([]noteFile, error) {
	var target string
	if folder == nil || *folder == "" || *folder == "." {
		target = scopeDirectory(scope)
	} else {
		var err error
		if target, err = notePath(scope, *folder); err != nil {
			return nil, err
		}
	}
	info, err := statIfPresent(target)
	if err != nil {
		return nil, err
	}
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if info == nil {
		return nil, nil
	}
	var files []noteFile
	if info.IsDir() {
		files, err = walk(ctx, target, walkState{scope: scope, notice: notice})
		if err != nil {
			return nil, err
		}
	} else {
		files = []noteFile{{noteIdentity: fileIdentity(target, info), path: target}}
	}
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	sort.SliceStable(files, func(i, j int) bool { return files[i].mtimeMs > files[j].mtimeMs })
	return files, nil
}
