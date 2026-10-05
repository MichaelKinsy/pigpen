package doctor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const crashGrace = 24 * time.Hour

type cacheEnt struct {
	Path     string
	Bytes    int64
	InUse    bool
	Eligible bool
	Reason   string
	Kind     string // "expired", "incomplete", "recent", "building", "in use"
}

func day(n int) time.Duration { return time.Duration(n) * 24 * time.Hour }

// usageLocks returns the lock files PiG uses for one cache entry.
func usageLocks(entry string) []string {
	dir, name := filepath.Dir(entry), filepath.Base(entry)
	build := name
	if filepath.Base(dir) == "ext" {
		if i := strings.LastIndexByte(name, '-'); i >= 0 && i+1 < len(name) {
			build = name[i+1:]
		}
	}
	return []string{filepath.Join(dir, ".locks", name+".usage.lock"), filepath.Join(dir, ".locks", build+".lock")}
}

func (inv *inventory) classifyCacheEntry(path string) cacheEnt {
	st := walkTree(path)
	e := cacheEnt{Path: path, Bytes: st.Bytes}
	for _, l := range usageLocks(path) {
		held, err := lockHeld(l, inv.now)
		if held || err != nil {
			e.InUse, e.Reason, e.Kind = true, "usage or build lock held", "in use"
			return e
		}
	}
	if p, ok := inv.procUses(path); ok {
		e.InUse, e.Reason, e.Kind = true, fmt.Sprintf("used by running process %d (%s)", p.PID, procName(p)), "in use"
		return e
	}
	_, readyErr := os.Lstat(filepath.Join(path, "ready.json"))
	last := time.Unix(0, st.Newest)
	usageOK := false
	if b, err := readFileGuarded(filepath.Join(path, "usage.json")); err == nil {
		var u struct {
			LastUsed int64 `json:"lastUsed"`
		}
		if json.Unmarshal(b, &u) == nil && u.LastUsed > 0 {
			last, usageOK = time.Unix(u.LastUsed, 0), true
		}
	}
	age := inv.now.Sub(last)
	switch {
	case readyErr != nil && age >= crashGrace:
		e.Eligible, e.Kind = true, "incomplete"
	case readyErr != nil:
		e.Reason, e.Kind = "incomplete but under 24 hours old (may be building)", "building"
	case !usageOK:
		// PiG retains such an entry conservatively (runtimecell classifyCacheEntry).
		e.Reason, e.Kind = "no usable usage.json, kept as PiG keeps it", "recent"
	case age >= day(inv.o.CacheDays):
		e.Eligible, e.Kind = true, "expired"
	default:
		e.Reason, e.Kind = fmt.Sprintf("used %d days ago (kept for %d)", int(age/day(1)), inv.o.CacheDays), "recent"
	}
	return e
}

// listEntryDirs lists real (non-symlink) directories directly inside dir, skipping hidden ones.
func (inv *inventory) listEntryDirs(dir string) (dirs []string) {
	es, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range es {
		p := filepath.Join(dir, e.Name())
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := os.Lstat(p)
		if err != nil {
			continue
		}
		if isSymlink(info) {
			inv.notes = append(inv.notes, "symlink, left alone: "+p)
			continue
		}
		if info.IsDir() {
			dirs = append(dirs, p)
		}
	}
	return dirs
}

func (inv *inventory) scanCaches() {
	cache := filepath.Join(inv.home, "cache")
	for _, name := range []string{"cells", "ext"} {
		root := filepath.Join(cache, name)
		info, err := os.Lstat(root)
		if err != nil {
			continue
		}
		if isSymlink(info) {
			inv.notes = append(inv.notes, "symlink, left alone: "+root)
			continue
		}
		var entries []cacheEnt
		var dirs []string
		if name == "cells" {
			for _, lang := range inv.listEntryDirs(root) {
				dirs = append(dirs, inv.listEntryDirs(lang)...)
			}
		} else {
			dirs = inv.listEntryDirs(root)
		}
		for _, d := range dirs {
			entries = append(entries, inv.classifyCacheEntry(d))
		}
		inv.cacheFinding("cache."+name, root, entries)
	}
	inv.scanArtifacts()
	inv.scanPigletCells()
}

func (inv *inventory) cacheFinding(id, root string, entries []cacheEnt) {
	size := CacheSize{Path: root, Entries: len(entries)}
	counts := map[string]int{}
	var pruneBytes int64
	var ops []Op
	var paths []string
	for _, e := range entries {
		size.Bytes += e.Bytes
		counts[e.Kind]++
		if e.InUse {
			size.InUse++
		}
		inv.add(&cand{Kind: OpDelete, Path: e.Path, Class: "cache", Bytes: e.Bytes, Eligible: e.Eligible, Reason: e.Reason})
		if e.Eligible {
			size.Prune++
			pruneBytes += e.Bytes
			ops = append(ops, Op{Kind: OpDelete, Path: e.Path, Bytes: e.Bytes, Note: "regenerable " + e.Kind + " cache entry"})
			paths = append(paths, e.Path)
		}
	}
	inv.sizes = append(inv.sizes, size)
	sev := SevInfo
	if size.Bytes >= inv.o.SizeWarnBytes {
		sev = SevWarn
	}
	f := Finding{
		ID: id, Severity: sev,
		Title:  fmt.Sprintf("%s is %s (%d entries, %d in use, %d prunable)", root, humanBytes(size.Bytes), size.Entries, size.InUse, size.Prune),
		Why:    "PiG builds cache entries on demand and never prunes them on its own, so the directory only grows; every entry can be rebuilt.",
		Detail: []string{fmt.Sprintf("in use: %d", counts["in use"]), fmt.Sprintf("kept (recent, used within %d days): %d", inv.o.CacheDays, counts["recent"]), fmt.Sprintf("kept (incomplete, under 24 hours old): %d", counts["building"]), fmt.Sprintf("prunable: %d (%s)", size.Prune, humanBytes(pruneBytes))},
		Paths:  paths,
	}
	if len(ops) == 0 {
		f.Safety, f.Fix = Manual, "nothing to prune."
	} else {
		f.Safety, f.Group, f.ops = SafeAuto, "caches", ops
		f.Fix = fmt.Sprintf("pig-doctor fix --group caches  (deletes %d entries that are unused for %d+ days and hold no lock). When pig itself works, `pig extensions cache prune` is the better tool: it also keeps the entries your current configuration needs, which the doctor cannot tell.", size.Prune, inv.o.CacheDays)
		if inv.toolchainBroken {
			f.Safety = NeedsConfirm
			f.Detail = append(f.Detail, "needs confirmation: the Go toolchain is broken (see go.*), so a pruned Go extension cannot be rebuilt until it is fixed")
		}
	}
	inv.findings = append(inv.findings, f)
}

// scanArtifacts reports PiG's managed Piglet Binary store. It never changes it:
// every build there has a record in receipts/piglets, and PiG's piglet inventory
// (coding/piglet/resolve.go List) fails as a whole when a record's build is gone,
// so a build moved away breaks `pig piglet list`, `show` and `remove`.
func (inv *inventory) scanArtifacts() {
	root := filepath.Join(inv.home, "artifacts", "piglets")
	info, err := os.Lstat(root)
	if err != nil {
		return
	}
	if isSymlink(info) {
		inv.notes = append(inv.notes, "symlink, left alone: "+root)
		return
	}
	size := CacheSize{Path: root}
	var detail []string
	for _, name := range inv.listEntryDirs(root) {
		st := walkTree(name)
		builds := len(inv.listEntryDirs(name))
		size.Bytes += st.Bytes
		size.Entries += builds
		detail = append(detail, fmt.Sprintf("%s: %s in %d source digest(s)", filepath.Base(name), humanBytes(st.Bytes), builds))
	}
	inv.sizes = append(inv.sizes, size)
	sev := SevInfo
	if size.Bytes >= inv.o.SizeWarnBytes {
		sev = SevWarn
	}
	inv.findings = append(inv.findings, Finding{
		ID: "cache.piglet-artifacts", Severity: sev, Safety: Manual, Detail: detail,
		Title: fmt.Sprintf("%s is %s (%d Piglet Binary source digests)", root, humanBytes(size.Bytes), size.Entries),
		Why:   "PiG keeps every Piglet Binary build with a record in receipts/piglets. The doctor does not move them: a build that disappears without its record makes PiG's Piglet inventory invalid (pig piglet list, show and remove fail).",
		Fix:   "pig piglet list, then pig piglet remove <name> --binary for a Piglet whose builds you no longer need (it removes every build and record of that Piglet; rebuild with pig piglet build)",
	})
}

func (inv *inventory) scanPigletCells() {
	for _, name := range []string{"piglet-cells", "piglet-binary-cells"} {
		root := filepath.Join(inv.home, name)
		info, err := os.Lstat(root)
		if err != nil {
			continue
		}
		if isSymlink(info) {
			inv.notes = append(inv.notes, "symlink, left alone: "+root)
			continue
		}
		st := walkTree(root)
		size := CacheSize{Path: root, Bytes: st.Bytes}
		if es, err := os.ReadDir(root); err == nil {
			size.Entries = len(es)
		}
		var units []string
		if name == "piglet-binary-cells" {
			for _, lang := range inv.listEntryDirs(filepath.Join(root, "isolated")) {
				units = append(units, inv.listEntryDirs(lang)...)
			}
		}
		var ops []Op
		var paths []string
		var pruneBytes int64
		for _, u := range units {
			us := walkTree(u)
			c := &cand{Kind: OpMove, Path: u, Class: "piglet-cell", Bytes: us.Bytes, Locks: us.Locks}
			held := false
			for _, l := range us.Locks {
				if h, err := lockHeld(l, inv.now); h || err != nil {
					held = true
				}
			}
			switch {
			case !inv.o.IncludePigletCells:
				c.Reason = "opt-in (--include-piglet-cells)"
			case held:
				c.Reason = "lock held"
			case inv.now.Sub(time.Unix(0, us.Newest)) < day(inv.o.CacheDays):
				c.Reason = fmt.Sprintf("used within %d days", inv.o.CacheDays)
			default:
				if p, ok := inv.procUses(u); ok {
					c.Reason = fmt.Sprintf("used by running process %d (%s)", p.PID, procName(p))
				} else {
					c.Eligible = true
				}
			}
			inv.add(c)
			if c.Eligible {
				size.Prune++
				pruneBytes += us.Bytes
				ops = append(ops, Op{Kind: OpMove, Path: u, Bytes: us.Bytes, Note: "unused Piglet Binary cell"})
				paths = append(paths, u)
			}
		}
		inv.sizes = append(inv.sizes, size)
		sev := SevInfo
		if size.Bytes >= inv.o.SizeWarnBytes {
			sev = SevWarn
		}
		f := Finding{
			ID: "cache." + name, Severity: sev, Safety: Manual,
			Title: fmt.Sprintf("%s is %s (%d top-level entries)", root, humanBytes(size.Bytes), size.Entries),
			Why:   "cells of built Piglet Binaries are never pruned. A Binary may need its cell to run, and the layout is not documented, so the doctor only offers to move unused ones when you ask.",
			Fix:   "pig-doctor fix --include-piglet-cells --group piglet-cells  (moves cells unused for " + fmt.Sprint(inv.o.CacheDays) + "+ days to the backup)",
			Paths: paths,
		}
		if len(ops) > 0 {
			f.Safety, f.Group, f.ops = NeedsConfirm, "piglet-cells", ops
			f.Detail = []string{fmt.Sprintf("movable: %d (%s)", size.Prune, humanBytes(pruneBytes))}
		} else if name == "piglet-binary-cells" && size.Bytes > 0 && !inv.o.IncludePigletCells {
			f.Detail = []string{"unused cells are listed only with --include-piglet-cells"}
		}
		inv.findings = append(inv.findings, f)
	}
}
