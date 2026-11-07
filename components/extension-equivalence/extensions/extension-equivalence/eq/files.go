package eq

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

// ChFiles is the channel of a file-state capture: one event per FileCapture, listing the matched files.
const ChFiles = "files"

// maxCapturedFile is the largest file whose content enters the trace; a larger one is recorded as its size and
// SHA-256, because a trace line that holds megabytes cannot be reviewed.
const maxCapturedFile = 4 << 20

// sessionTimeRE is the timestamp Pi puts in a session file's name and in the paths that refer to it
// (2026-11-06T10-20-30-123Z): a clock, normalized in captured files and their paths (rule N8).
var sessionTimeRE = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}-\d{3}Z`)

// Rule N8 (file captures only; no other trace is touched):
//
//	N8a a captured file's path and every string of its content: N1 to N3, N7, and the session file name timestamp
//	    (sessionTimeRE) becomes <time>.
//	N8b in a *.jsonl file, each line is decoded and re-encoded with sorted keys; a number-valued "timestamp" or
//	    "durationMs" key is dropped (a message's clock in epoch milliseconds, a measured duration), as are the
//	    capture's drop paths; session entry ids (id, parentId, firstKeptEntryId, ...)
//	    become <id:N> in order of first appearance in the whole trace.
//
// Captured files are listed in path order within each capture, which is the order they appear in each lane.

// captureFiles lists the files FileCapture c selects under dir.
func (n *Normalizer) captureFiles(dir string, c FileCapture) (map[string]any, error) {
	var rels []string
	prefix := literalPrefix(c.Glob)
	start := filepath.Join(dir, filepath.FromSlash(prefix))
	pat := strings.Split(c.Glob, "/")
	keepGit := slices.Contains(strings.Split(prefix, "/"), ".git") || strings.HasPrefix(c.Glob, ".git")
	if _, err := os.Lstat(start); err == nil {
		err := filepath.WalkDir(start, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(dir, p)
			rel = filepath.ToSlash(rel)
			if d.IsDir() {
				if !keepGit && (d.Name() == ".git" || d.Name() == "node_modules") && p != start {
					return fs.SkipDir
				}
				return nil
			}
			name := strings.Split(rel, "/")
			if !globMatch(pat, name) {
				return nil
			}
			for _, ex := range c.Exclude {
				if globMatch(strings.Split(ex, "/"), name) {
					return nil
				}
			}
			rels = append(rels, rel)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(rels)
	files := make([]any, 0, len(rels))
	for _, rel := range rels {
		f, err := n.captureFile(dir, rel, c.Modes, splitDrop(c.Drop))
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return map[string]any{"capture": c.label(), "files": files}, nil
}

// literalPrefix is the glob's leading directory segments without metacharacters (where the walk starts).
func literalPrefix(glob string) string {
	segs := strings.Split(glob, "/")
	i := 0
	for i < len(segs)-1 && !strings.ContainsAny(segs[i], `*?[\`) {
		i++
	}
	return strings.Join(segs[:i], "/")
}

// globMatch matches slash-separated name segments against pattern segments; ** matches zero or more segments.
func globMatch(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			for len(pat) > 1 && pat[1] == "**" {
				pat = pat[1:]
			}
			if len(pat) == 1 {
				return true
			}
			for i := 0; i <= len(name); i++ {
				if globMatch(pat[1:], name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		if ok, err := path.Match(pat[0], name[0]); err != nil || !ok {
			return false
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0
}

func (n *Normalizer) captureFile(dir, rel string, modes bool, drop [][]string) (map[string]any, error) {
	full := filepath.Join(dir, filepath.FromSlash(rel))
	info, err := os.Lstat(full)
	if err != nil {
		return nil, err
	}
	rec := map[string]any{"path": n.fileString(rel)}
	if info.Mode()&fs.ModeSymlink != 0 {
		target, err := os.Readlink(full)
		if err != nil {
			return nil, err
		}
		rec["symlink"] = n.fileString(target)
		return rec, nil
	}
	if modes {
		rec["mode"] = fmt.Sprintf("%04o", info.Mode().Perm())
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return nil, err
	}
	switch {
	case len(data) > maxCapturedFile || !utf8.Valid(data):
		sum := sha256.Sum256(data)
		rec["size"] = len(data)
		rec["sha256"] = hex.EncodeToString(sum[:])
	default:
		if strings.HasSuffix(rel, ".jsonl") {
			if lines, ok := n.jsonlLines(data, drop); ok {
				rec["jsonl"] = lines
				return rec, nil
			}
		}
		rec["text"] = n.fileString(string(data))
	}
	return rec, nil
}

// fileString applies N8a to one string.
func (n *Normalizer) fileString(s string) string {
	return n.str(sessionTimeRE.ReplaceAllString(s, "<time>"))
}

// jsonlLines decodes a JSON-lines file for the trace (N8b). It reports false when a line is not a JSON value, so the
// caller keeps the file as text. Keys matching the capture's drop patterns are removed first.
func (n *Normalizer) jsonlLines(data []byte, drop [][]string) ([]any, bool) {
	out := []any{}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		v, err := decodeAny([]byte(line))
		if err != nil {
			return nil, false
		}
		v = dropNumberKeys(v, "timestamp", "durationMs")
		if len(drop) > 0 {
			v = dropPaths(v, drop, nil)
		}
		v = n.relabelIDs(v)
		out = append(out, mapStrings(v, n.fileString))
	}
	return out, true
}

// dropPaths removes the keys whose dotted path from the line's root (array indices are not part of a path) matches
// a pattern: segments are key names, * matches any one key, and a leading ** lets the rest match at any depth.
func dropPaths(v any, pats [][]string, path []string) any {
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = dropPaths(e, pats, path)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			p := append(path[:len(path):len(path)], k)
			if matchesAnyPath(pats, p) {
				continue
			}
			out[k] = dropPaths(e, pats, p)
		}
		return out
	}
	return v
}

func matchesAnyPath(pats [][]string, path []string) bool {
	for _, pat := range pats {
		if pathMatches(pat, path) {
			return true
		}
	}
	return false
}

func pathMatches(pat, path []string) bool {
	if len(pat) > 0 && pat[0] == "**" {
		rest := pat[1:]
		return len(path) >= len(rest) && pathMatches(rest, path[len(path)-len(rest):])
	}
	if len(pat) != len(path) {
		return false
	}
	for i, seg := range pat {
		if seg != "*" && seg != path[i] {
			return false
		}
	}
	return true
}

// splitDrop parses the drop patterns of a capture.
func splitDrop(drop []string) [][]string {
	var out [][]string
	for _, d := range drop {
		out = append(out, strings.Split(d, "."))
	}
	return out
}

// dropNumberKeys removes the keys named whose value is a number, at any depth.
func dropNumberKeys(v any, keys ...string) any {
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = dropNumberKeys(e, keys...)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			if _, isNum := e.(json.Number); isNum && slices.Contains(keys, k) {
				continue
			}
			out[k] = dropNumberKeys(e, keys...)
		}
		return out
	}
	return v
}

// mapStrings applies f to every string value and key of a decoded JSON value.
func mapStrings(v any, f func(string) string) any {
	switch x := v.(type) {
	case string:
		return f(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = mapStrings(e, f)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[f(k)] = mapStrings(e, f)
		}
		return out
	}
	return v
}
