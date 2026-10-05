package doctor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
)

// Selection restricts a Plan to some groups. Empty selects every group.
type Selection struct {
	Groups []string
}

// Confirmer decides whether a group is applied. It is called once per group, in plan order.
type Confirmer func(Group) (bool, error)

// LockedError is returned when another process holds a lock a fix would change.
type LockedError struct {
	Path   string
	Reason string
}

func (e *LockedError) Error() string {
	return fmt.Sprintf("refusing to change %s: %s", e.Path, e.Reason)
}

// ErrLocked matches every *LockedError with errors.Is.
var ErrLocked = errors.New("locked by another process")

func (e *LockedError) Is(target error) bool { return target == ErrLocked }

// OpResult is the outcome of one operation.
type OpResult struct {
	Op      Op     `json:"op"`
	Group   string `json:"group"`
	Status  string `json:"status"` // done, skipped
	Message string `json:"message,omitempty"`
}

// Result summarizes an Apply.
type Result struct {
	Backup  string     `json:"backup,omitempty"` // timestamp, empty when nothing was backed up
	Applied []string   `json:"applied"`
	Skipped []string   `json:"skipped"`
	Ops     []OpResult `json:"ops"`
	Freed   int64      `json:"freedBytes"`
}

var sessionsSegments = map[string]bool{"sessions": true, "skills": true, "prompts": true}

// guard is the structural safety check every operation passes before anything else:
// inside the PiG home, no symlink on the way, never the agent directory, never a
// protected file, and only operation kinds the doctor knows.
func (inv *inventory) guard(op Op) error {
	switch op.Kind {
	case OpRemovePackage:
		if op.Path != inv.settingsPath() || op.Source == "" {
			return fmt.Errorf("refusing %s: package entries are removed only from %s", op, inv.settingsPath())
		}
		return nil
	case OpMove, OpDelete:
	default:
		return fmt.Errorf("refusing unknown operation %q", op.Kind)
	}
	p := op.Path
	if !strictlyWithin(inv.home, p) {
		return fmt.Errorf("refusing %s: outside the PiG home %s", op, inv.home)
	}
	if err := noSymlinkBelow(inv.home, p); err != nil {
		return fmt.Errorf("refusing %s: %w", op, err)
	}
	if within(inv.agent, p) {
		return fmt.Errorf("refusing %s: the agent directory holds your credentials, trust, models, sessions, skills and prompts and is never modified", op)
	}
	base := filepath.Base(p)
	if protectedName(base) && !(op.Kind == OpDelete && credentialName(base)) {
		return fmt.Errorf("refusing %s: %s is a protected file", op, base)
	}
	if op.Kind == OpDelete {
		rel, _ := filepath.Rel(inv.home, p)
		for _, seg := range strings.Split(rel, string(filepath.Separator)) {
			if sessionsSegments[seg] {
				return fmt.Errorf("refusing %s: sessions, skills and prompts are never deleted", op)
			}
		}
	}
	return nil
}

type item struct {
	group string
	op    Op
	c     *cand
	skip  string
}

func heldAny(inv *inventory, paths []string) (string, bool) {
	for _, l := range paths {
		if held, err := lockHeld(l, inv.now); held || err != nil {
			return l, true
		}
	}
	return "", false
}

// Apply performs the approved groups of a plan. It first re-scans the home and
// refuses any operation that is not something the doctor itself would offer now.
func Apply(o Options, p *Plan, c Confirmer) (*Result, error) {
	o = withDefaults(o)
	if p.Home != o.Home || p.AgentDir != o.AgentDir {
		return nil, fmt.Errorf("this plan was made for %s (agent %s), not for %s", p.Home, p.AgentDir, o.Home)
	}
	inv, err := scan(o)
	if err != nil {
		return nil, err
	}
	for _, g := range p.Groups {
		for _, op := range g.Ops {
			if err := inv.guard(op); err != nil {
				return nil, err
			}
			if op.Kind != OpRemovePackage && inv.lookup(op.Kind, op.Path) == nil {
				return nil, fmt.Errorf("refusing %s: not something pig-doctor manages (unknown, protected or no longer present); run check again", op)
			}
		}
	}
	res := &Result{}
	var approved []Group
	for _, g := range p.Groups {
		if len(g.Ops) == 0 {
			continue
		}
		ok, err := c(g)
		if err != nil {
			return nil, err
		}
		if ok {
			approved = append(approved, g)
		} else {
			res.Skipped = append(res.Skipped, g.ID)
		}
	}

	// Preflight everything before changing anything.
	var items []item
	deletes := map[string]bool{}
	for _, g := range approved {
		for _, op := range g.Ops {
			if op.Kind == OpDelete {
				deletes[op.Path] = true
			}
		}
	}
	needSettings, needGC := false, false
	for _, g := range approved {
		for _, op := range g.Ops {
			it := item{group: g.ID, op: op}
			if op.Kind == OpRemovePackage {
				needSettings = true
				items = append(items, it)
				continue
			}
			it.c = inv.lookup(op.Kind, op.Path)
			if !it.c.Eligible {
				if it.c.LockedBy != "" {
					return nil, &LockedError{Path: it.c.LockedBy, Reason: "another process holds this lock"}
				}
				if it.c.Class != "cache" {
					return nil, fmt.Errorf("refusing %s: the plan is stale, this is no longer eligible (%s); run check again", op, it.c.Reason)
				}
				it.skip = it.c.Reason
			}
			if it.skip == "" {
				if l, held := heldAny(inv, it.c.Locks); held {
					return nil, &LockedError{Path: l, Reason: "another process holds this lock"}
				}
				if it.c.Class == "cache" {
					needGC = true
				}
				if it.c.Class == "orphan" && op.Kind == OpMove {
					for _, rel := range walkTree(op.Path).Creds {
						if !deletes[filepath.Join(op.Path, rel)] {
							return nil, fmt.Errorf("refusing %s: it holds credential file %s that the plan does not delete (credentials are never copied into a backup)", op, rel)
						}
					}
				}
			}
			items = append(items, it)
		}
	}
	if needSettings {
		if held, err := lockHeld(inv.settingsLock(), inv.now); held || err != nil {
			return nil, &LockedError{Path: inv.settingsLock(), Reason: "another process holds it (a running pig may be saving settings)"}
		}
	}
	if needGC {
		if l, held := heldAny(inv, inv.cacheGCLocks()); held {
			return nil, &LockedError{Path: l, Reason: "a pig process is pruning or building the cache"}
		}
	}
	if len(items) == 0 {
		return res, nil
	}

	bk := &backup{o: o, inv: inv}
	if needSettings {
		release, err := acquireLock(inv.settingsLock())
		if err != nil {
			return nil, &LockedError{Path: inv.settingsLock(), Reason: err.Error()}
		}
		defer release()
	}
	if needGC {
		if err := os.MkdirAll(filepath.Join(inv.home, "cache"), 0o755); err == nil {
			release, err := acquireLock(filepath.Join(inv.home, "cache", ".gc.lock"))
			if err != nil {
				return nil, &LockedError{Path: filepath.Join(inv.home, "cache", ".gc.lock"), Reason: err.Error()}
			}
			defer release()
		}
	}

	done := map[string]bool{}
	for _, g := range approved {
		var gi []item
		for _, it := range items {
			if it.group == g.ID {
				gi = append(gi, it)
			}
		}
		if err := inv.runGroup(bk, res, gi); err != nil {
			bk.finish(res)
			return res, err
		}
		done[g.ID] = true
		res.Applied = append(res.Applied, g.ID)
	}
	bk.finish(res)
	return res, nil
}

func (inv *inventory) runGroup(bk *backup, res *Result, items []item) error {
	var removes []item
	for _, it := range items {
		switch it.op.Kind {
		case OpRemovePackage:
			removes = append(removes, it)
			continue
		case OpMove, OpDelete:
		}
		r := OpResult{Op: it.op, Group: it.group, Status: "done"}
		if it.skip != "" {
			r.Status, r.Message = "skipped", it.skip
			res.Ops = append(res.Ops, r)
			continue
		}
		var err error
		switch it.op.Kind {
		case OpMove:
			err = bk.move(it.op)
		case OpDelete:
			if it.c.Class == "cache" {
				if l, held := heldAny(inv, usageLocks(it.op.Path)); held {
					r.Status, r.Message = "skipped", "in use now: "+l
					res.Ops = append(res.Ops, r)
					continue
				}
				err = bk.deleteCache(it.op)
			} else {
				err = bk.deleteCredential(it.op)
			}
		}
		if err != nil {
			r.Status, r.Message = "failed", err.Error()
			res.Ops = append(res.Ops, r)
			return fmt.Errorf("%s: %w", it.op, err)
		}
		res.Freed += it.c.Bytes
		res.Ops = append(res.Ops, r)
	}
	if len(removes) > 0 {
		return bk.removePackages(res, removes)
	}
	return nil
}

// ---- backup ----

type manifestEntry struct {
	Kind     string `json:"kind"` // move, edit, delete
	Original string `json:"original"`
	Backup   string `json:"backup,omitempty"`
	IsDir    bool   `json:"isDir,omitempty"`
	SHAAfter string `json:"sha256After,omitempty"`
	Note     string `json:"note,omitempty"`
	Restored bool   `json:"restored,omitempty"`
}

type manifest struct {
	Version  int             `json:"version"`
	Created  string          `json:"created"`
	Home     string          `json:"home"`
	AgentDir string          `json:"agentDir"`
	Entries  []manifestEntry `json:"entries"`
}

type backup struct {
	o        Options
	inv      *inventory
	dir      string
	ts       string
	m        manifest
	n        atomic.Int32
	settings int // index of the settings edit entry, -1 none
	hasSet   bool
}

func (b *backup) init() error {
	if b.dir != "" {
		return nil
	}
	if b.o.BackupRoot == "" {
		return errors.New("no backup location: set the backup directory or a user home")
	}
	if within(b.inv.home, b.o.BackupRoot) {
		return fmt.Errorf("the backup directory %s must not be inside the PiG home", b.o.BackupRoot)
	}
	base := b.o.Now().UTC().Format("20060102T150405Z")
	ts := base
	for i := 2; ; i++ {
		if _, err := os.Lstat(filepath.Join(b.o.BackupRoot, ts)); errors.Is(err, os.ErrNotExist) {
			break
		}
		ts = fmt.Sprintf("%s-%d", base, i)
	}
	dir := filepath.Join(b.o.BackupRoot, ts)
	if err := os.MkdirAll(filepath.Join(dir, "files"), 0o700); err != nil {
		return err
	}
	b.dir, b.ts = dir, ts
	b.m = manifest{Version: 1, Created: b.o.Now().UTC().Format("2006-01-02T15:04:05Z"), Home: b.inv.home, AgentDir: b.inv.agent}
	return b.save()
}

func (b *backup) save() error {
	data, err := json.MarshalIndent(b.m, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(b.dir, "manifest.json"), data, 0o600)
}

func (b *backup) slot(name string) string {
	n := b.n.Add(1)
	return filepath.Join("files", fmt.Sprintf("%03d-%s", n, strings.ReplaceAll(name, string(filepath.Separator), "__")))
}

func (b *backup) finish(res *Result) { res.Backup = b.ts }

func (b *backup) record(e manifestEntry) error {
	b.m.Entries = append(b.m.Entries, e)
	return b.save()
}

func (b *backup) move(op Op) error {
	if err := b.init(); err != nil {
		return err
	}
	info, err := os.Lstat(op.Path)
	if err != nil {
		return err
	}
	slot := b.slot(filepath.Base(op.Path))
	if err := movePath(op.Path, filepath.Join(b.dir, slot)); err != nil {
		if errors.Is(err, errSourceRemains) {
			// The backup holds a complete copy: record it so it can be found and restored.
			if rerr := b.record(manifestEntry{Kind: "move", Original: op.Path, Backup: slot, IsDir: info.IsDir(), Note: op.Note + "; part of the source could not be removed"}); rerr != nil {
				return errors.Join(err, rerr)
			}
		}
		return err
	}
	return b.record(manifestEntry{Kind: "move", Original: op.Path, Backup: slot, IsDir: info.IsDir(), Note: op.Note})
}

func (b *backup) deleteCache(op Op) error {
	if err := b.init(); err != nil {
		return err
	}
	tomb := filepath.Join(filepath.Dir(op.Path), ".tombstone-"+filepath.Base(op.Path)+"-doctor")
	if err := renameFn(op.Path, tomb); err != nil {
		return err
	}
	if err := os.RemoveAll(tomb); err != nil {
		return err
	}
	return b.record(manifestEntry{Kind: "delete", Original: op.Path, Note: "regenerable cache entry; not restorable"})
}

func (b *backup) deleteCredential(op Op) error {
	if err := b.init(); err != nil {
		return err
	}
	if err := os.Remove(op.Path); err != nil {
		return err
	}
	return b.record(manifestEntry{Kind: "delete", Original: op.Path, Note: "credential copy; deliberately not backed up, not restorable"})
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func writeAtomic(path string, data []byte, mode fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".pig-doctor-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	return os.Rename(name, path)
}

// backupFile stores a copy of an about-to-be-edited file and returns the manifest slot.
func (b *backup) backupFile(path string, content []byte, mode fs.FileMode) (string, error) {
	slot := b.slot(filepath.Base(path))
	return slot, os.WriteFile(filepath.Join(b.dir, slot), content, mode&0o700|0o600)
}

func (b *backup) removePackages(res *Result, items []item) error {
	if err := b.init(); err != nil {
		return err
	}
	path := b.inv.settingsPath()
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	raw, err := readFileGuarded(path)
	if err != nil {
		return err
	}
	entries, err := parsePackages(raw)
	if err != nil {
		return err
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].op.Index > items[j].op.Index })
	var idx []int
	used := map[int]bool{}
	var results []OpResult
	for _, it := range items {
		at := -1
		if it.op.Index < len(entries) && entries[it.op.Index].Source == it.op.Source && !used[it.op.Index] {
			at = it.op.Index
		} else {
			for _, e := range entries {
				if e.Source == it.op.Source && !used[e.Index] {
					at = e.Index
				}
			}
		}
		r := OpResult{Op: it.op, Group: it.group, Status: "done"}
		kept := false
		for _, e := range entries {
			if e.Index != at && !used[e.Index] && it.op.Keep != "" && e.Source == it.op.Keep {
				kept = true
			}
		}
		if at < 0 {
			r.Status, r.Message = "skipped", "no longer in settings.json"
		} else if !kept {
			// Settings changed since the plan (for example during the confirmation):
			// never remove an entry unless the one it duplicates is still there.
			r.Status, r.Message = "skipped", "the entry it duplicates ("+it.op.Keep+") is no longer in settings.json"
		} else {
			used[at] = true
			idx = append(idx, at)
		}
		results = append(results, r)
	}
	if len(idx) > 0 {
		out, err := removeArrayElements(raw, "packages", idx)
		if err != nil {
			return err
		}
		if err := verifyOnlyPackagesChanged(raw, out, entries, idx); err != nil {
			return err
		}
		slot, err := b.backupFile(path, raw, info.Mode())
		if err != nil {
			return err
		}
		if err := writeAtomic(path, out, info.Mode().Perm()); err != nil {
			return err
		}
		if err := b.record(manifestEntry{Kind: "edit", Original: path, Backup: slot, SHAAfter: sha(out), Note: "removed duplicate Package entries"}); err != nil {
			return err
		}
	}
	res.Ops = append(res.Ops, results...)
	return nil
}

// verifyOnlyPackagesChanged re-parses before and after: every top-level key except
// "packages" must be identical, and "packages" must be exactly the old list minus idx.
func verifyOnlyPackagesChanged(before, after []byte, entries []PackageEntry, idx []int) error {
	var a, b map[string]json.RawMessage
	if err := json.Unmarshal(stripBOM(before), &a); err != nil {
		return err
	}
	if err := json.Unmarshal(stripBOM(after), &b); err != nil {
		return fmt.Errorf("edit produced invalid settings: %w", err)
	}
	if len(a) != len(b) {
		return errors.New("edit changed the set of settings keys; refusing")
	}
	for k, v := range a {
		if k == "packages" {
			continue
		}
		if w, ok := b[k]; !ok || string(v) != string(w) {
			return fmt.Errorf("edit changed key %q; refusing", k)
		}
	}
	newEntries, err := parsePackages(after)
	if err != nil {
		return err
	}
	if len(newEntries) != len(entries)-len(idx) {
		return errors.New("edit removed the wrong number of packages; refusing")
	}
	gone := map[int]bool{}
	for _, i := range idx {
		gone[i] = true
	}
	j := 0
	for i, e := range entries {
		if gone[i] {
			continue
		}
		if newEntries[j].Source != e.Source {
			return errors.New("edit changed a package that should stay; refusing")
		}
		j++
	}
	return nil
}

func stripBOM(b []byte) []byte { return []byte(strings.TrimPrefix(string(b), "\xef\xbb\xbf")) }
