package doctor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// pkgInfo is one settings.json package entry with what could be learned about it.
type pkgInfo struct {
	Entry      PackageEntry
	Norm       string // normalized source
	Exact      string // comparable source including the version or ref
	Local      string // absolute path for a local source
	Readable   bool
	Name       string
	Version    string
	Ident      string
	Confidence string // "source", "name", "folder"
	Members    map[string][]string
	Enabled    map[string][]string
	Uncertain  bool // a filter uses "!": PiG includes, Pi and the docs exclude, so never auto-fix on it
	Note       string
}

var schemeRe = regexp.MustCompile(`^[a-z][a-z0-9+.-]+:`)

// normalizeSource returns a comparable key for a source and, for a local one, its absolute path.
func normalizeSource(src, userHome, agentDir string) (norm, local string) {
	s := strings.TrimSpace(src)
	switch {
	case strings.HasPrefix(s, "npm:"):
		body := strings.TrimPrefix(s, "npm:")
		body, _, _ = strings.Cut(body, "?")
		if i := strings.LastIndex(body, "@"); i > 0 {
			body = body[:i]
		}
		return "npm:" + body, ""
	case strings.HasPrefix(s, "git:") || strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "ssh://") || strings.HasPrefix(s, "git@"):
		body, frag, _ := strings.Cut(s, "#")
		body = strings.TrimPrefix(body, "git:")
		if i := strings.LastIndex(body, "@"); i > strings.LastIndex(body, "/") && i > 0 {
			body = body[:i]
		}
		body = strings.TrimSuffix(strings.TrimSuffix(body, "/"), ".git")
		return "git:" + body + "#" + frag, ""
	case schemeRe.MatchString(s) && !filepath.IsAbs(s):
		return s, ""
	}
	p := s
	if p == "~" || strings.HasPrefix(p, "~/") {
		p = filepath.Join(userHome, strings.TrimPrefix(p, "~"))
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(agentDir, p)
	}
	p = filepath.Clean(p)
	return "local:" + p, p
}

var memberKinds = []string{"extensions", "skills", "prompts", "themes"}

// inspectPackage reads a local Package's package.json and member lists. It never follows a symlink.
func inspectPackage(e PackageEntry, userHome, agentDir string) *pkgInfo {
	pi := &pkgInfo{Entry: e, Enabled: map[string][]string{}, Members: map[string][]string{}}
	pi.Norm, pi.Local = normalizeSource(e.Source, userHome, agentDir)
	pi.Exact = pi.Norm
	if pi.Local == "" {
		pi.Exact = strings.TrimSuffix(strings.TrimSpace(e.Source), "/")
	}
	for _, k := range memberKinds {
		pi.Uncertain = pi.Uncertain || hasBang(e.filter(k))
	}
	if pi.Local == "" {
		pi.Ident, pi.Confidence = pi.Norm, "source"
		return pi
	}
	info, err := os.Lstat(pi.Local)
	switch {
	case err != nil:
		pi.Note = "source not found"
	case isSymlink(info):
		pi.Note = "source is a symlink; not followed"
	case !info.IsDir():
		pi.Note = "source is not a directory"
	default:
		pi.Readable = true
	}
	if !pi.Readable {
		pi.Ident, pi.Confidence = "folder:"+filepath.Base(pi.Local), "folder"
		return pi
	}
	var pj struct {
		Name    string                     `json:"name"`
		Version string                     `json:"version"`
		Pi      map[string]json.RawMessage `json:"pi"`
	}
	if b, err := readFileGuarded(filepath.Join(pi.Local, "package.json")); err == nil {
		_ = json.Unmarshal(b, &pj)
	}
	pi.Name, pi.Version = pj.Name, pj.Version
	for _, kind := range memberKinds {
		var patterns []string
		if raw, ok := pj.Pi[kind]; ok {
			_ = json.Unmarshal(raw, &patterns)
		}
		pi.Members[kind] = listMembers(pi.Local, kind, patterns)
		pi.Enabled[kind] = applyPatterns(pi.Members[kind], e.filter(kind))
	}
	if pi.Name != "" {
		pi.Ident, pi.Confidence = "name:"+pi.Name, "name"
	} else {
		pi.Ident, pi.Confidence = "folder:"+filepath.Base(pi.Local), "folder"
	}
	return pi
}

// listMembers lists package-relative member paths ("extensions/ask").
func listMembers(root, kind string, patterns []string) []string {
	if len(patterns) == 0 {
		patterns = []string{kind + "/*"}
	}
	var out []string
	seen := map[string]bool{}
	add := func(rel string) {
		if !seen[rel] {
			seen[rel] = true
			out = append(out, rel)
		}
	}
	for _, p := range patterns {
		p = filepath.ToSlash(p)
		if strings.HasPrefix(p, "!") || strings.HasPrefix(p, "-") || strings.HasPrefix(p, "+") {
			continue
		}
		if dir, ok := strings.CutSuffix(p, "/*"); ok && !strings.ContainsAny(dir, "*?[") {
			entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
			if err != nil {
				continue
			}
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), ".") {
					continue
				}
				if kind == "prompts" && !strings.HasSuffix(e.Name(), ".md") {
					continue
				}
				if kind == "themes" && !strings.HasSuffix(e.Name(), ".json") {
					continue
				}
				add(dir + "/" + e.Name())
			}
			continue
		}
		if strings.ContainsAny(p, "*?[") {
			continue
		}
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(p))); err == nil {
			add(p)
		}
	}
	sort.Strings(out)
	return out
}

func (p *pkgInfo) coverage() map[string]bool {
	c := map[string]bool{}
	for kind, ms := range p.Enabled {
		for _, m := range ms {
			c[m] = true
		}
		_ = kind
	}
	return c
}

// covers reports whether every member b enables is also enabled by a.
// Without readable members, only an unfiltered entry is known to cover another.
func covers(a, b *pkgInfo) bool {
	if a.Readable && b.Readable {
		ac, bc := a.coverage(), b.coverage()
		for m := range bc {
			if !ac[m] {
				return false
			}
		}
		return true
	}
	return !a.Entry.Filtered || (a.Entry.Extensions == nil && a.Entry.Skills == nil && a.Entry.Prompts == nil && a.Entry.Themes == nil)
}

type dupRemoval struct {
	Remove, Keep *pkgInfo
	Safety       Safety
	Reason       string
}

// findDuplicates groups entries by identity and picks, for each group, the entry to keep.
func findDuplicates(pkgs []*pkgInfo, keep, userHome, agentDir string) [][]dupRemoval {
	groups := map[string][]*pkgInfo{}
	var order []string
	for _, p := range pkgs {
		if _, ok := groups[p.Ident]; !ok {
			order = append(order, p.Ident)
		}
		groups[p.Ident] = append(groups[p.Ident], p)
	}
	var out [][]dupRemoval
	for _, id := range order {
		g := groups[id]
		if len(g) < 2 {
			continue
		}
		survivor := chooseSurvivor(g, keep, userHome, agentDir)
		var rs []dupRemoval
		for _, p := range g {
			if p == survivor {
				continue
			}
			rs = append(rs, classifyRemoval(p, survivor))
		}
		out = append(out, rs)
	}
	return out
}

func chooseSurvivor(g []*pkgInfo, keep, userHome, agentDir string) *pkgInfo {
	if keep != "" {
		kn, _ := normalizeSource(keep, userHome, agentDir)
		for _, p := range g {
			if p.Norm == kn || p.Entry.Source == keep {
				return p
			}
		}
	}
	// The broadest entry; ties go to the first listed (PiG also keeps the first definition on a conflict).
	best := g[0]
	for _, p := range g[1:] {
		if covers(p, best) && !covers(best, p) {
			best = p
		}
	}
	return best
}

func classifyRemoval(p, survivor *pkgInfo) dupRemoval {
	r := dupRemoval{Remove: p, Keep: survivor, Safety: NeedsConfirm}
	switch {
	case p.Exact == survivor.Exact && sameFilters(p.Entry, survivor.Entry):
		r.Safety, r.Reason = SafeAuto, "the same source listed twice with the same filters"
	case p.Exact == survivor.Exact && !p.Uncertain && !survivor.Uncertain && covers(survivor, p):
		r.Safety, r.Reason = SafeAuto, "the same source listed twice; the kept entry enables everything this one does"
	case p.Confidence != "folder" && survivor.Confidence != "folder" && p.Local == "" && survivor.Local == "":
		r.Reason = "the same remote Package with another version or ref: choose which to keep"
	case p.Confidence == "name" && survivor.Confidence == "name" && p.Readable && survivor.Readable && !p.Uncertain && !survivor.Uncertain &&
		covers(survivor, p) && !covers(p, survivor):
		r.Safety, r.Reason = SafeAuto, fmt.Sprintf("same Package name %q; every member this entry enables is also enabled by the entry kept", p.Name)
	case p.Confidence == "folder" || survivor.Confidence == "folder":
		r.Reason = "matched by folder name only (the sources cannot be read): confirm they are the same Package"
	case p.Readable && survivor.Readable && covers(survivor, p) && covers(p, survivor):
		r.Reason = "both entries enable the same members: choose which checkout to keep"
	default:
		r.Reason = "the entry kept does not enable everything this one does"
	}
	return r
}

func sameFilters(a, b PackageEntry) bool {
	for _, k := range memberKinds {
		x, y := a.filter(k), b.filter(k)
		if (x == nil) != (y == nil) || strings.Join(x, "\x00") != strings.Join(y, "\x00") {
			return false
		}
	}
	return true
}
