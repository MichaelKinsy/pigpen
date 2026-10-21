package pi_background_tasks

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Lane file retention: the one rule for files a package keeps on disk for a lane. A lane's files are deleted once the lane's
// working directory is gone, which is how a merged lane's worktree ends, and no file is kept past laneFileMaxAge.
//
// A lane directory is one the package made with openLaneDir: a real directory, owned by this user, that holds a laneCwdFile
// record. The prune touches nothing else under its root, so a folder another tool keeps there, or a symbolic link to one, is
// left alone. upstream: scripts/lane-retention.ts.

const (
	laneFileMaxAge = 5 * 24 * time.Hour
	// laneCwdFile is the file in a lane directory that names the lane's working directory.
	laneCwdFile = ".lane-cwd"
)

type lanePruneFailure struct{ path, err string }

type lanePruneResult struct {
	removed []string
	failed  []lanePruneFailure
}

// openLaneDir creates dir as a lane directory for cwd. Rewriting the record marks the lane live, so its directory is not
// pruned as empty and old.
func openLaneDir(dir, cwd string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, laneCwdFile), []byte(cwd), 0o600); err != nil {
		return "", err
	}
	return dir, nil
}

// ownDirectory reports whether path is a real directory, not a symbolic link, owned by this user. A platform without user
// ids (Windows) checks the type alone.
func ownDirectory(path string) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	return info.IsDir() && ownedByThisUser(info)
}

func (r *lanePruneResult) remove(path string) {
	if err := os.RemoveAll(path); err != nil {
		r.failed = append(r.failed, lanePruneFailure{path, err.Error()})
		return
	}
	r.removed = append(r.removed, path)
}

// recordedCwd is the lane's recorded working directory; "" and false when dir holds no record, so the package did not make it.
func recordedCwd(dir string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(dir, laneCwdFile))
	if err != nil || len(b) == 0 {
		return "", false
	}
	return string(b), true
}

// cwdState is whether a lane's recorded working directory is still there. Only a stat that answers ENOENT or ENOTDIR confirms
// it is gone. Any other failure is reported against the lane and answers "unknown", so the lane is not removed as gone.
func cwdState(dir, cwd string, r *lanePruneResult) string {
	_, err := os.Stat(cwd)
	if err == nil {
		return "present"
	}
	if errors.Is(err, fs.ErrNotExist) || isNotDir(err) {
		return "gone"
	}
	r.failed = append(r.failed, lanePruneFailure{dir, "lane-cwd-unchecked: the lane is kept; its working directory could not be checked: " + err.Error()})
	return "unknown"
}

func (r *lanePruneResult) modifiedAt(path string) (time.Time, bool) {
	info, err := os.Lstat(path)
	if err != nil {
		r.failed = append(r.failed, lanePruneFailure{path, err.Error()})
		return time.Time{}, false
	}
	return info.ModTime(), true
}

// pruneOldFiles removes the files under dir older than cutoff, never the lane record; it returns how many entries other than
// the record remain.
func (r *lanePruneResult) pruneOldFiles(dir string, cutoff time.Time) int {
	remaining := 0
	entries, err := os.ReadDir(dir)
	if err != nil {
		r.failed = append(r.failed, lanePruneFailure{dir, err.Error()})
		return 1
	}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		if e.Name() == laneCwdFile {
			continue
		}
		if e.IsDir() {
			if r.pruneOldFiles(path, cutoff) == 0 {
				r.remove(path)
			} else {
				remaining++
			}
			continue
		}
		if mtime, ok := r.modifiedAt(path); ok && mtime.Before(cutoff) {
			r.remove(path)
		} else {
			remaining++
		}
	}
	return remaining
}

// pruneLanes applies the retention rule to each lane directory at root/<entry>/<...below>, one entry per session. A lane
// whose recorded working directory is gone is removed whole; a working directory that cannot be checked is reported and does
// not count as gone. Otherwise each file older than laneFileMaxAge is removed, and the lane goes too once only its record is
// left and the record is that old. An absent root has no lanes. Every failure, an unreadable root included, is reported, never
// returned as an error, so the caller's other session_start work still runs.
func pruneLanes(root string, below []string, now time.Time) lanePruneResult {
	var result lanePruneResult
	entries, err := os.ReadDir(root)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			result.failed = append(result.failed, lanePruneFailure{root, err.Error()})
		}
		return result
	}
	if !ownDirectory(root) {
		result.failed = append(result.failed, lanePruneFailure{root, "lane-root-not-owned: not a directory this user owns; nothing under it was pruned"})
		return result
	}
	cutoff := now.Add(-laneFileMaxAge)
	for _, e := range entries {
		dir := root
		owned := true
		for _, part := range append([]string{e.Name()}, below...) {
			dir = filepath.Join(dir, part)
			if !ownDirectory(dir) {
				owned = false
				break
			}
		}
		if !owned {
			continue
		}
		cwd, ok := recordedCwd(dir)
		if !ok {
			continue
		}
		switch cwdState(dir, cwd, &result) {
		case "gone":
			result.remove(dir)
			continue
		}
		if result.pruneOldFiles(dir, cutoff) > 0 {
			continue
		}
		if recordAt, ok := result.modifiedAt(filepath.Join(dir, laneCwdFile)); ok && recordAt.Before(cutoff) {
			result.remove(dir)
		}
	}
	return result
}

var _ = fmt.Sprint
