package doctor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var automationDirs = map[string]string{
	"subagent-output": "output files of subagent runs",
	"worktree-claims": "worktree claim files left by automation",
}

// agentShaped reports whether a directory name and content look like a PiG agent directory left by automation.
func agentShaped(name, path string) bool {
	if name == "agent" || !(strings.HasSuffix(name, "-agent") || strings.Contains(name, "-agent-")) {
		return false
	}
	for _, marker := range []string{"settings.json", "auth.json", "sessions", "models.json", "models-store.json", "trust.json"} {
		if _, err := os.Lstat(filepath.Join(path, marker)); err == nil {
			return true
		}
	}
	return false
}

func collectStrings(v any, out *[]string) {
	switch t := v.(type) {
	case string:
		*out = append(*out, t)
	case []any:
		for _, x := range t {
			collectStrings(x, out)
		}
	case map[string]any:
		for _, x := range t {
			collectStrings(x, out)
		}
	}
}

func (inv *inventory) referenced(path string) bool {
	forms := []string{path}
	if h := inv.o.UserHome; h != "" && strictlyWithin(h, path) {
		rel, _ := filepath.Rel(h, path)
		forms = append(forms, "~/"+filepath.ToSlash(rel)) // PiG expands a leading ~
	}
	for _, s := range inv.refStrings {
		for _, f := range forms {
			if strings.Contains(s, f) {
				return true
			}
		}
	}
	return false
}

// orphanState decides whether dir is an orphan right now.
type orphanState struct {
	LockedBy string
	Eligible bool
	Reason   string
	Stats    treeStats
	Confirm  bool // eligible only with confirmation (process environments unknown)
}

func (inv *inventory) orphanCheck(path string) orphanState {
	st := walkTree(path)
	os_ := orphanState{Stats: st}
	switch {
	case path == inv.agent || within(path, inv.agent):
		os_.Reason = "the active agent directory"
	case !inv.settingsOK:
		os_.Reason = "settings.json could not be read, so whether it refers to this directory is unknown"
	case inv.referenced(path):
		os_.Reason = "referenced by settings.json"
	default:
		if p, ok := inv.procUses(path); ok {
			os_.Reason = fmt.Sprintf("used by running process %d (%s)", p.PID, procName(p))
			return os_
		}
		for _, l := range st.Locks {
			if h, err := lockHeld(l, inv.now); h || err != nil {
				os_.Reason, os_.LockedBy = "a lock inside is held: "+l, l
				return os_
			}
		}
		if age := inv.now.Sub(time.Unix(0, st.Newest)); age < day(inv.o.OrphanDays) {
			os_.Reason = fmt.Sprintf("modified %d days ago (orphaned means untouched for %d+ days)", int(age/day(1)), inv.o.OrphanDays)
			return os_
		}
		os_.Eligible = true
		os_.Confirm = inv.envUnknown
	}
	return os_
}

func (inv *inventory) scanTopLevel() {
	es, err := os.ReadDir(inv.home)
	if err != nil {
		return
	}
	known := map[string]bool{"agent": true, "piglets": true, "artifacts": true, "cache": true, "state": true, "docs": true,
		"extensions": true, "extensions.toml": true, "piglet-cells": true, "piglet-binary-cells": true}
	for _, e := range es {
		name := e.Name()
		path := filepath.Join(inv.home, name)
		info, err := os.Lstat(path)
		if err != nil {
			continue
		}
		if isSymlink(info) {
			if known[name] || automationDirs[name] != "" || strings.HasSuffix(name, "-agent") || strings.Contains(name, "-agent-") {
				inv.notes = append(inv.notes, "symlink, left alone: "+path)
			} else {
				inv.unknown = append(inv.unknown, name+" (symlink)")
			}
			continue
		}
		switch {
		case known[name]:
		case strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".lock"):
		case automationDirs[name] != "" && info.IsDir():
			inv.automationDir(path, name)
		case info.IsDir() && agentShaped(name, path):
			inv.agentDir(path)
		default:
			inv.unknown = append(inv.unknown, name)
		}
	}
	sort.Strings(inv.unknown)
}

func (inv *inventory) automationDir(path, name string) {
	os_ := inv.orphanCheck(path)
	inv.add(&cand{Kind: OpMove, Path: path, Class: "orphan", Bytes: os_.Stats.Bytes, Eligible: os_.Eligible, Reason: os_.Reason, LockedBy: os_.LockedBy, Locks: os_.Stats.Locks})
	if !os_.Eligible {
		return
	}
	safety := SafeAuto
	if os_.Confirm {
		safety = NeedsConfirm
	}
	inv.findings = append(inv.findings, Finding{
		ID: "orphan.automation-dir", Severity: SevWarn, Safety: safety, Group: "orphans", Paths: []string{path},
		Title: fmt.Sprintf("%s holds %s (%d files, %s), untouched for %d+ days", path, automationDirs[name], os_.Stats.Files, humanBytes(os_.Stats.Bytes), inv.o.OrphanDays),
		Why:   "automation writes here and never cleans up; nothing in the PiG home refers to it and no running process uses it.",
		Fix:   "pig-doctor fix --group orphans  (moves the directory to ~/.pig-doctor-backup; restorable)",
		ops:   []Op{{Kind: OpMove, Path: path, Bytes: os_.Stats.Bytes, Note: "orphaned automation output"}},
	})
}

func (inv *inventory) agentDir(path string) {
	os_ := inv.orphanCheck(path)
	inv.add(&cand{Kind: OpMove, Path: path, Class: "orphan", Bytes: os_.Stats.Bytes, Eligible: os_.Eligible, Reason: os_.Reason, LockedBy: os_.LockedBy, Locks: os_.Stats.Locks})
	st := os_.Stats
	var credOps []Op
	for _, rel := range st.Creds {
		p := filepath.Join(path, rel)
		inv.add(&cand{Kind: OpDelete, Path: p, Class: "cred", Eligible: os_.Eligible, Reason: os_.Reason, LockedBy: os_.LockedBy, Locks: st.Locks})
		credOps = append(credOps, Op{Kind: OpDelete, Path: p, Note: "credential copy: deleted, never backed up, never read"})
	}
	if !os_.Eligible {
		return
	}
	f := Finding{
		Severity: SevWarn, Paths: []string{path},
		Why:    "an agent directory left behind by automation that nothing references and no running process uses; it still holds sessions and settings.",
		Detail: []string{fmt.Sprintf("%d files, %s, last modified %s", st.Files, humanBytes(st.Bytes), time.Unix(0, st.Newest).Format("2006-01-02"))},
	}
	move := Op{Kind: OpMove, Path: path, Bytes: st.Bytes, Note: "orphaned agent directory"}
	switch {
	case len(credOps) > 0:
		f.ID, f.Safety, f.Group = "orphan.credentials", Credentials, "credentials"
		f.Title = fmt.Sprintf("orphaned agent directory %s holds a credential copy", path)
		f.Why = "a copy of API keys or OAuth tokens sits in a directory nothing uses; it is a leak waiting to happen and is not covered by your auth.json handling."
		f.Detail = append(f.Detail, "credential files (names only, contents are never read): "+strings.Join(st.Creds, ", "))
		f.Fix = "pig-doctor fix --group credentials  (asks you to type 'remove credentials'; deletes the credential files without backing them up, then moves the rest of the directory to the backup)"
		f.ops = append(credOps, move)
	case st.Sessions:
		f.ID, f.Safety, f.Group = "orphan.agent-dir", NeedsConfirm, "orphans-sessions"
		f.Title = fmt.Sprintf("orphaned agent directory %s (has sessions)", path)
		f.Fix = "pig-doctor fix --group orphans-sessions  (moves it, sessions included, to ~/.pig-doctor-backup; nothing is deleted)"
		f.ops = []Op{move}
	default:
		f.ID, f.Safety, f.Group = "orphan.agent-dir", SafeAuto, "orphans"
		f.Title = fmt.Sprintf("orphaned agent directory %s", path)
		f.Fix = "pig-doctor fix --group orphans  (moves it to ~/.pig-doctor-backup; restorable)"
		f.ops = []Op{move}
	}
	if os_.Confirm && f.Safety == SafeAuto {
		f.Safety, f.Group = NeedsConfirm, "orphans-sessions"
		f.Detail = append(f.Detail, "needs confirmation: the environment of a running pig process could not be read, so it may use this directory")
	}
	inv.findings = append(inv.findings, f)
}

func (inv *inventory) scanLegacy() {
	dir := filepath.Join(inv.home, "extensions")
	if info, err := os.Lstat(dir); err == nil && info.IsDir() && !isSymlink(info) {
		st := walkTree(dir)
		names := []string{}
		if es, err := os.ReadDir(dir); err == nil {
			for _, e := range es {
				names = append(names, e.Name())
			}
		}
		inv.add(&cand{Kind: OpMove, Path: dir, Class: "legacy", Bytes: st.Bytes, Eligible: true, Locks: st.Locks})
		inv.findings = append(inv.findings, Finding{
			ID: "legacy.extensions-dir", Severity: SevWarn, Safety: NeedsConfirm, Group: "legacy", Paths: []string{dir},
			Title:  fmt.Sprintf("legacy extensions directory %s (%s)", dir, strings.Join(names, ", ")),
			Why:    "extensions belong in the agent directory or in Packages; this older location is not the documented one and its copies duplicate or shadow the ones you install, and they were built for older SDKs.",
			Detail: []string{fmt.Sprintf("%d files, %s", st.Files, humanBytes(st.Bytes))},
			Fix:    "pig-doctor fix --group legacy  (moves it to ~/.pig-doctor-backup; restore with pig-doctor restore)",
			ops:    []Op{{Kind: OpMove, Path: dir, Bytes: st.Bytes, Note: "legacy extensions directory"}},
		})
	} else if err == nil && isSymlink(info) {
		inv.notes = append(inv.notes, "symlink, left alone: "+dir)
	}
	for _, p := range []string{filepath.Join(inv.home, "extensions.toml"), filepath.Join(inv.agent, "extensions.toml")} {
		info, err := os.Lstat(p)
		if err != nil {
			continue
		}
		if isSymlink(info) || !info.Mode().IsRegular() {
			inv.notes = append(inv.notes, "not a regular file, left alone: "+p)
			continue
		}
		var detail []string
		if b, err := readFileGuarded(p); err == nil {
			for _, l := range strings.Split(string(b), "\n") {
				if strings.Contains(l, "handler_timeout") {
					detail = append(detail, strings.TrimSpace(l))
				}
			}
		}
		if within(inv.agent, p) {
			// The agent directory is never modified (see guard), so this one is manual.
			inv.findings = append(inv.findings, Finding{
				ID: "legacy.extensions-toml", Severity: SevWarn, Safety: Manual, Paths: []string{p}, Detail: detail,
				Title: fmt.Sprintf("legacy config file %s", p),
				Why:   "extensions.toml was a PiG 0.84-era workaround for slow extension handlers (handler_timeout). Current PiG does not read it, so it only misleads.",
				Fix:   fmt.Sprintf("the doctor never changes the agent directory; move it yourself: mv %s %s.bak", p, p),
			})
			continue
		}
		inv.add(&cand{Kind: OpMove, Path: p, Class: "legacy", Bytes: info.Size(), Eligible: true})
		inv.findings = append(inv.findings, Finding{
			ID: "legacy.extensions-toml", Severity: SevWarn, Safety: NeedsConfirm, Group: "legacy", Paths: []string{p}, Detail: detail,
			Title: fmt.Sprintf("legacy config file %s", p),
			Why:   "extensions.toml was a PiG 0.84-era workaround for slow extension handlers (handler_timeout). Current PiG does not read it, so it only misleads.",
			Fix:   "pig-doctor fix --group legacy  (moves it to ~/.pig-doctor-backup)",
			ops:   []Op{{Kind: OpMove, Path: p, Bytes: info.Size(), Note: "legacy extensions.toml"}},
		})
	}
}

// settings parsing
func (inv *inventory) loadSettings() {
	p := inv.settingsPath()
	if _, err := os.Lstat(p); err != nil {
		inv.settingsOK = true
		return
	}
	b, err := readFileGuarded(p)
	if err == nil {
		var top any
		if err = json.Unmarshal([]byte(strings.TrimPrefix(string(b), "\xef\xbb\xbf")), &top); err == nil {
			inv.settings, inv.settingsOK = b, true
			collectStrings(top, &inv.refStrings)
			inv.packages, err = parsePackages(b)
			if err != nil {
				inv.settingsOK = false
			}
		}
	}
	if err != nil {
		inv.findings = append(inv.findings, Finding{
			ID: "settings.invalid", Severity: SevError, Safety: Manual,
			Title: fmt.Sprintf("%s cannot be read as PiG settings: %v", p, err),
			Why:   "with an unreadable settings.json the doctor cannot tell which Packages are configured, so it makes no package findings and changes nothing there.",
			Fix:   "fix the JSON by hand (for example `python3 -m json.tool " + p + "`), then run pig-doctor check again",
		})
	}
}
