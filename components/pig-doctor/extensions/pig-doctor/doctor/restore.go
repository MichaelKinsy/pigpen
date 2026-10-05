package doctor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

// BackupInfo describes one backup directory.
type BackupInfo struct {
	Timestamp string
	Path      string
	Entries   int
}

// RestoreResult summarizes a Restore.
type RestoreResult struct {
	Restored      []string
	Conflict      []string
	NotRestorable []string
}

var tsRe = regexp.MustCompile(`^\d{8}T\d{6}Z(-\d+)?$`)

func readManifest(dir string) (*manifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("manifest.json: %w", err)
	}
	return &m, nil
}

// ListBackups lists backups, oldest first.
func ListBackups(o Options) ([]BackupInfo, error) {
	o = withDefaults(o)
	es, err := os.ReadDir(o.BackupRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []BackupInfo
	for _, e := range es {
		if !e.IsDir() || !tsRe.MatchString(e.Name()) {
			continue
		}
		dir := filepath.Join(o.BackupRoot, e.Name())
		m, err := readManifest(dir)
		if err != nil {
			continue
		}
		out = append(out, BackupInfo{Timestamp: e.Name(), Path: dir, Entries: len(m.Entries)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp < out[j].Timestamp })
	return out, nil
}

// Restore puts a backup back. It never overwrites data: an occupied path is a
// conflict and stays as it is. force only allows replacing a settings.json (or
// another edited file) that changed after the fix.
func Restore(o Options, ts string, force bool) (*RestoreResult, error) {
	o = withDefaults(o)
	if !tsRe.MatchString(ts) {
		return nil, fmt.Errorf("%q is not a backup timestamp (like 20260930T120000Z); see pig-doctor backups", ts)
	}
	dir := filepath.Join(o.BackupRoot, ts)
	m, err := readManifest(dir)
	if err != nil {
		return nil, fmt.Errorf("no backup %s: %w", ts, err)
	}
	if m.Home != o.Home {
		return nil, fmt.Errorf("backup %s belongs to %s, not %s", ts, m.Home, o.Home)
	}
	inv := &inventory{o: o, home: o.Home, agent: o.AgentDir, now: o.Now()}
	for _, e := range m.Entries {
		if e.Kind == "edit" && !e.Restored {
			if held, err := lockHeld(inv.settingsLock(), inv.now); held || err != nil {
				return nil, &LockedError{Path: inv.settingsLock(), Reason: "another process holds it"}
			}
			break
		}
	}
	res := &RestoreResult{}
	for i := len(m.Entries) - 1; i >= 0; i-- {
		e := &m.Entries[i]
		if e.Restored {
			continue
		}
		if e.Kind == "delete" {
			res.NotRestorable = append(res.NotRestorable, e.Original)
			continue
		}
		// The manifest is not trusted: a protected name is never written, and only
		// paths in the PiG home or the agent's own settings.json are restored.
		if protectedName(filepath.Base(e.Original)) {
			res.Conflict = append(res.Conflict, e.Original+" (a protected file: refused)")
			continue
		}
		root := o.Home
		agentSettings := e.Kind == "edit" && m.AgentDir == o.AgentDir && e.Original == filepath.Join(o.AgentDir, "settings.json")
		if agentSettings && !within(o.Home, e.Original) {
			root = o.AgentDir
		}
		if !strictlyWithin(root, e.Original) {
			res.Conflict = append(res.Conflict, e.Original+" (outside the PiG home: refused)")
			continue
		}
		if err := noSymlinkBelow(root, filepath.Dir(e.Original)); err != nil {
			res.Conflict = append(res.Conflict, e.Original+" ("+err.Error()+")")
			continue
		}
		src := filepath.Join(dir, e.Backup)
		if !within(dir, src) {
			return nil, fmt.Errorf("manifest entry escapes the backup: %s", e.Backup)
		}
		switch e.Kind {
		case "move":
			if _, err := os.Lstat(e.Original); err == nil {
				res.Conflict = append(res.Conflict, e.Original+" (already exists; not overwritten)")
				continue
			}
			if err := movePath(src, e.Original); err != nil {
				return res, fmt.Errorf("restore %s: %w", e.Original, err)
			}
		case "edit":
			if info, err := os.Lstat(e.Original); err == nil && !info.Mode().IsRegular() {
				res.Conflict = append(res.Conflict, e.Original+" (no longer a regular file; symlinks are not followed or replaced)")
				continue
			}
			cur, err := readFileGuarded(e.Original)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				res.Conflict = append(res.Conflict, e.Original+" (cannot be read: "+err.Error()+")")
				continue
			}
			if err == nil && sha(cur) != e.SHAAfter && !force {
				res.Conflict = append(res.Conflict, e.Original+" (changed since the fix; use --force to replace it)")
				continue
			}
			orig, rerr := os.ReadFile(src)
			if rerr != nil {
				return res, rerr
			}
			mode := os.FileMode(0o600)
			if info, serr := os.Lstat(e.Original); serr == nil {
				mode = info.Mode().Perm()
			}
			if err := writeAtomic(e.Original, orig, mode); err != nil {
				return res, err
			}
		}
		e.Restored = true
		res.Restored = append(res.Restored, e.Original)
		data, _ := json.MarshalIndent(m, "", "  ")
		if err := writeAtomic(filepath.Join(dir, "manifest.json"), data, 0o600); err != nil {
			return res, err
		}
	}
	return res, nil
}
