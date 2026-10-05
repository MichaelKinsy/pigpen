package doctor

import (
	"path/filepath"
	"strings"
	"time"
)

// cand is something the doctor may change. Every operation a Plan holds must
// match a candidate found by a fresh scan at Apply time; anything else is refused.
type cand struct {
	Kind     OpKind
	Path     string
	Class    string // cache, orphan, legacy, piglet-cell, cred, sdk
	Bytes    int64
	Eligible bool
	Reason   string   // why not eligible
	Locks    []string // lock files that must not be held for the op to run
	LockedBy string   // set when a held lock is why it is not eligible
}

type inventory struct {
	o        Options
	home     string
	agent    string
	now      time.Time
	cands    map[string]*cand
	findings []Finding
	unknown  []string
	notes    []string
	sizes    []CacheSize
	procs    []Proc // pig processes
	running  []Proc // every other process that could be listed (in-use checks)
	procInfo []ProcInfo
	// envUnknown is set when a running pig process's environment could not be read.
	envUnknown bool
	// toolchainBroken: no go, or its compiler and command disagree; nothing Go can be rebuilt.
	toolchainBroken bool
	settings        []byte
	settingsOK      bool
	packages        []PackageEntry
	refStrings      []string // every string value in settings.json
}

func (inv *inventory) add(c *cand) { inv.cands[string(c.Kind)+"\x00"+c.Path] = c }

func (inv *inventory) lookup(kind OpKind, path string) *cand {
	return inv.cands[string(kind)+"\x00"+path]
}

func (inv *inventory) cacheGCLocks() []string {
	return []string{filepath.Join(inv.home, "cache", ".gc.lock"), filepath.Join(inv.home, "cache", "cells", ".gc.lock"), filepath.Join(inv.home, "cache", "ext", ".gc.lock")}
}

func (inv *inventory) settingsPath() string { return filepath.Join(inv.agent, "settings.json") }

func (inv *inventory) settingsLock() string { return filepath.Join(inv.agent, "settings.json.lock") }

// procUses reports the first running process (of any kind: a worker script or a
// wrapper uses a directory as much as pig does) whose executable, working
// directory, PIG_CODING_AGENT_DIR or an absolute argument lies below path.
func (inv *inventory) procUses(path string) (Proc, bool) {
	for _, p := range inv.running {
		if within(path, p.Exe) && p.Exe != "" || within(path, p.Cwd) && p.Cwd != "" {
			return p, true
		}
		if v := p.Env["PIG_CODING_AGENT_DIR"]; v != "" {
			home := p.Env["HOME"]
			if home == "" {
				home = inv.o.UserHome
			}
			if v = expandTilde(v, home); filepath.IsAbs(v) && within(path, filepath.Clean(v)) {
				return p, true
			}
		}
		for _, a := range p.Args {
			if filepath.IsAbs(a) && within(path, filepath.Clean(a)) {
				return p, true
			}
		}
	}
	return Proc{}, false
}

// procName names a process by its executable only: arguments can hold secrets
// (pig --api-key ...) and the report reaches the model through the pig_doctor tool.
func procName(p Proc) string {
	if p.Exe != "" {
		return p.Exe
	}
	if len(p.Args) > 0 {
		return p.Args[0]
	}
	return "?"
}

// expandTilde expands a leading "~" the way PiG does (codingagent.ExpandTildePath).
func expandTilde(path, home string) string {
	if home == "" {
		return path
	}
	if path == "~" {
		return home
	}
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		return filepath.Join(home, rest)
	}
	return path
}
