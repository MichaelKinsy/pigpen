package ompsession

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// A Mirror keeps translations of omp sessions in one PiG session directory, where PiG's own `--resume`,
// `--continue`, `--session <id>` and `/resume` find them like any session it wrote.
//
// A translation is written once per state of its source: the index beside the translations records the source's
// size and time, so a source that did not change costs one stat. A translation the user continued in PiG has
// grown past what was written; it is then PiG's session and is never written again.
type Mirror struct {
	dir   string
	index map[string]mirrored
	dirty bool
}

// mirrored is one translation: the state of its source when it was written, and its own size then.
type mirrored struct {
	SrcSize  int64 `json:"srcSize"`
	SrcMtime int64 `json:"srcMtime"` // nanoseconds
	Size     int64 `json:"size"`
}

// Status is what Sync found or did.
type Status int

const (
	Fresh      Status = iota // the translation is of the source as it is
	Translated               // the translation was written
	Kept                     // the source changed, but the file here is not what Sync wrote and was left alone
)

// indexName is not a session file name (PiG lists *.jsonl), so the index is not listed as a session.
const indexName = ".omp-sessions.json"

// OpenMirror is the mirror of the PiG session directory dir, which need not exist yet.
func OpenMirror(dir string) *Mirror {
	m := &Mirror{dir: dir, index: map[string]mirrored{}}
	if data, err := os.ReadFile(filepath.Join(dir, indexName)); err == nil {
		// An index that does not parse is an empty one: every source is translated again.
		_ = json.Unmarshal(data, &m.index)
	}
	return m
}

// Sync makes the translation of the omp session src current and returns its path.
func (m *Mirror) Sync(src string, opts Options) (string, Status, error) {
	info, err := os.Stat(src)
	if err != nil {
		return "", 0, err
	}
	key, suffix := src, "-omp.jsonl"
	if opts.Full {
		key, suffix = src+"#full", "-omp-full.jsonl"
	}
	dst := filepath.Join(m.dir, strings.TrimSuffix(filepath.Base(src), ".jsonl")+suffix)
	entry, known := m.index[key]
	current := known && info.Size() == entry.SrcSize && info.ModTime().UnixNano() == entry.SrcMtime
	if have, err := os.Stat(dst); err == nil {
		// A file of another size than Sync wrote was continued in PiG, or is not Sync's: it is never replaced.
		ours := known && have.Size() == entry.Size
		switch {
		case current:
			return dst, Fresh, nil
		case !ours:
			// The source's state is recorded, so the session is reported once and not at every start.
			entry.SrcSize, entry.SrcMtime = info.Size(), info.ModTime().UnixNano()
			m.index[key], m.dirty = entry, true
			return dst, Kept, nil
		}
	}

	if err := os.MkdirAll(m.dir, 0o700); err != nil {
		return "", 0, err
	}
	// The translation appears under its name complete: PiG never lists a half-written session.
	tmp, err := os.CreateTemp(m.dir, ".omp-session-*.tmp")
	if err != nil {
		return "", 0, err
	}
	_ = tmp.Close()
	res, err := Translate(src, tmp.Name(), opts)
	if err == nil {
		// PiG's --continue takes the session written last: the translation is as old as its source.
		err = os.Chtimes(tmp.Name(), info.ModTime(), info.ModTime())
	}
	if err == nil {
		err = os.Rename(tmp.Name(), dst)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return "", 0, err
	}
	m.index[key] = mirrored{SrcSize: info.Size(), SrcMtime: info.ModTime().UnixNano(), Size: res.Bytes}
	m.dirty = true
	return dst, Translated, nil
}

// SyncAll makes the translation of every omp session of the project at cwd current. It returns how many were
// written and what could not be done, one line per session.
func (m *Mirror) SyncAll(home, cwd string) (translated int, problems []string) {
	for _, dir := range SourceDirs(home, cwd) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
				continue
			}
			src := filepath.Join(dir, entry.Name())
			switch _, status, err := m.Sync(src, Options{}); {
			case err != nil:
				problems = append(problems, fmt.Sprintf("omp session %s was not imported: %v", src, err))
			case status == Translated:
				translated++
			case status == Kept:
				problems = append(problems, fmt.Sprintf("omp session %s changed, and its copy here was continued in PiG: it was not imported again (`/import-omp %s --full` imports it beside the continued one)", entry.Name(), src))
			}
		}
	}
	return translated, problems
}

// Save writes the index when a translation was written.
func (m *Mirror) Save() error {
	if !m.dirty {
		return nil
	}
	data, err := json.Marshal(m.index)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(m.dir, ".omp-sessions-*.tmp")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), filepath.Join(m.dir, indexName))
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	m.dirty = false
	return nil
}

// SourceDirs are the directories omp keeps the sessions of the project at cwd in, under home's ~/.omp: the
// home-relative name omp writes now, then the absolute name it wrote before.
// upstream: packages/coding-agent/src/session/session-paths.ts getDefaultSessionDirName
func SourceDirs(home, cwd string) []string {
	root := filepath.Join(home, ".omp", "agent", "sessions")
	encode := strings.NewReplacer("/", "-", `\`, "-", ":", "-").Replace
	var dirs []string
	if rel, err := filepath.Rel(home, cwd); err == nil && rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
		dirs = append(dirs, filepath.Join(root, "-"+encode(rel)))
	}
	return append(dirs, filepath.Join(root, "--"+encode(strings.TrimLeft(cwd, `/\`))+"--"))
}

// Newest is the omp session of the project at cwd that was written last.
func Newest(home, cwd string) (string, error) {
	var newest string
	var newestTime int64
	for _, dir := range SourceDirs(home, cwd) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
				continue
			}
			if info, err := entry.Info(); err == nil && (newest == "" || info.ModTime().UnixNano() > newestTime) {
				newest, newestTime = filepath.Join(dir, entry.Name()), info.ModTime().UnixNano()
			}
		}
	}
	if newest == "" {
		return "", fmt.Errorf("no omp session of %s: %w", cwd, fs.ErrNotExist)
	}
	return newest, nil
}

// PigSessionDir is the directory PiG keeps the sessions of the project at cwd in: the --session-dir flag's,
// else the environment's, else the default under the agent directory. A session directory named only in
// PiG's settings is not known here.
// upstream: PiG coding/cli/main.go resolveSessionDir, internal/codingagent/session_manager.go GetDefaultSessionDirPath
func PigSessionDir(flag, home, cwd string, getenv func(string) string) (string, error) {
	if getenv("PIG_USE_PI_DIRS") == "1" {
		return "", errors.New("PiG shares Pi's directories (PIG_USE_PI_DIRS)")
	}
	expand := func(path string) string {
		if path == "~" || strings.HasPrefix(path, "~/") {
			return filepath.Join(home, path[1:])
		}
		return path
	}
	if flag != "" {
		return flag, nil
	}
	if dir := getenv("PIG_CODING_AGENT_SESSION_DIR"); dir != "" {
		return expand(dir), nil
	}
	agentDir := filepath.Join(home, ".pig", "agent")
	if dir := getenv("PIG_CODING_AGENT_DIR"); dir != "" {
		agentDir = expand(dir)
	}
	name := cwd
	if name != "" && (name[0] == '/' || name[0] == '\\') {
		name = name[1:]
	}
	name = strings.NewReplacer("/", "-", `\`, "-", ":", "-").Replace(name)
	return filepath.Join(agentDir, "sessions", "--"+name+"--"), nil
}
