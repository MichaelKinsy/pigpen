package pi_goal_x

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Goal storage: storage/goal-root.ts (the default root only), storage/goal-files.ts, storage/goal-lock.ts, goal-ledger.ts (append).
// Files live under <cwd>/.pi/goals: active goals, archived/, goal_events.jsonl, .locks/, and a pool snapshot one level up.

const (
	goalsDir         = ".pi/goals"
	archivedGoalsDir = ".pi/goals/archived"
	lockDir          = ".pi/goals/.locks"
	ledgerFile       = ".pi/goals/goal_events.jsonl"
	snapshotLegacy   = ".goals-pool-snapshot.json"
)

type storage struct{ cwd string }

func (s storage) root() string { return filepath.Join(absClean(s.cwd), ".pi", "goals") }

func absClean(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return filepath.Clean(p)
}

// goalStoragePath resolves a logical ".pi/goals/..." path under the root.
func (s storage) path(logical string) (string, error) {
	rel := strings.ReplaceAll(logical, "\\", "/")
	if rel != ".pi/goals" && !strings.HasPrefix(rel, ".pi/goals/") {
		return "", fmt.Errorf("Invalid goal storage path: %s", logical)
	}
	root := s.root()
	sub := ""
	if len(rel) > len(".pi/goals") {
		sub = rel[len(".pi/goals")+1:]
	}
	res := filepath.Join(root, filepath.FromSlash(sub))
	inside, _ := filepath.Rel(root, res)
	if inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) || filepath.IsAbs(inside) {
		return "", fmt.Errorf("Goal path escapes root: %s", logical)
	}
	return res, nil
}

func (s storage) mustPath(logical string) string {
	p, err := s.path(logical)
	if err != nil {
		panic(err)
	}
	return p
}

func (s storage) snapshotPath() string {
	return filepath.Join(filepath.Dir(s.root()), ".goals-pool-snapshot.json")
}

func (s storage) ensureDir(logical string, verifyDefault bool) error {
	target, err := s.path(logical)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	if verifyDefault {
		if fi, err := os.Lstat(target); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("Goal directory is a symlink: %s", target)
		}
	}
	return nil
}

// timestampForFile is the local time as yyyyMMddHHmmss plus hundredths of a second.
var localZone = time.Local

func timestampForFile(iso string) string {
	t, err := time.Parse("2006-01-02T15:04:05.000Z", iso)
	if err != nil {
		t = time.UnixMilli(clockMs())
	}
	t = t.In(localZone)
	return fmt.Sprintf("%04d%02d%02d%02d%02d%02d%02d", t.Year(), int(t.Month()), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond()/1e6/10)
}

var (
	activeNameRE   = regexp.MustCompile(`^active_goal_.*\.md$`)
	archivedNameRE = regexp.MustCompile(`^goal_.*\.md$`)
)

func isSafeRelativeUnder(s storage, rootRel string, rel string) bool {
	if rel == "" || filepath.IsAbs(rel) || strings.ContainsRune(rel, 0) {
		return false
	}
	norm := normalizeRelPath(rel)
	if normalizeRelPath(path.Dir(norm)) != normalizeRelPath(rootRel) {
		return false
	}
	root, err := s.path(rootRel)
	if err != nil {
		return false
	}
	abs, err := s.path(norm)
	if err != nil {
		return false
	}
	r, _ := filepath.Rel(root, abs)
	return !strings.HasPrefix(r, "..") && !filepath.IsAbs(r)
}

func isSafeActivePath(s storage, rel string) bool {
	return isSafeRelativeUnder(s, goalsDir, rel) && activeNameRE.MatchString(path.Base(normalizeRelPath(rel)))
}

func isSafeArchivedPath(s storage, rel string) bool {
	return isSafeRelativeUnder(s, archivedGoalsDir, rel) && archivedNameRE.MatchString(path.Base(normalizeRelPath(rel)))
}

func strOrEmpty(g *jsObject, k string) string {
	if g.has(k) {
		return gstr(g, k)
	}
	return ""
}

// sanitizeGoalPaths drops an activePath or archivedPath that is not a safe path under its root (the key is deleted, as `delete`).
func sanitizeGoalPaths(s storage, g *jsObject) *jsObject {
	n := cloneGoal(g)
	for _, p := range []struct {
		k    string
		safe func(storage, string) bool
	}{{"activePath", isSafeActivePath}, {"archivedPath", isSafeArchivedPath}} {
		v, isStr := n.vals[p.k].(string)
		if !isStr || !p.safe(s, v) {
			delete(n.vals, p.k)
			n.keys = removeKey(n.keys, p.k)
		}
	}
	return n
}

func makeActiveGoalPath(g *jsObject) string {
	return fmt.Sprintf("%s/active_goal_%s_%s.md", goalsDir, timestampForFile(gstr(g, "createdAt")), safeIdPart(gstr(g, "id")))
}

func makeArchivedGoalPath(g *jsObject) string {
	return fmt.Sprintf("%s/goal_%s_%s.md", archivedGoalsDir, timestampForFile(gstr(g, "updatedAt")), safeIdPart(gstr(g, "id")))
}

func activePathForGoal(s storage, g *jsObject) string {
	if v, ok := g.vals["activePath"].(string); ok && isSafeActivePath(s, v) {
		return v
	}
	return makeActiveGoalPath(g)
}

func archivedPathForGoal(s storage, g *jsObject) string {
	if v, ok := g.vals["archivedPath"].(string); ok && isSafeArchivedPath(s, v) {
		return v
	}
	return makeArchivedGoalPath(g)
}

// ---- caches the original keeps (pool per root) ----

// goalPool is a JavaScript Map of goals: iteration follows insertion order.
type goalPool struct {
	ids []string
	m   map[string]*jsObject
}

func newPool() *goalPool { return &goalPool{m: map[string]*jsObject{}} }

func (p *goalPool) set(id string, g *jsObject) {
	if _, ok := p.m[id]; !ok {
		p.ids = append(p.ids, id)
	}
	p.m[id] = g
}

func (p *goalPool) get(id string) *jsObject { return p.m[id] }
func (p *goalPool) has(id string) bool      { _, ok := p.m[id]; return ok }
func (p *goalPool) size() int               { return len(p.ids) }

func (p *goalPool) delete(id string) {
	if _, ok := p.m[id]; ok {
		delete(p.m, id)
		p.ids = removeKey(p.ids, id)
	}
}

func (p *goalPool) values() []*jsObject {
	out := make([]*jsObject, 0, len(p.ids))
	for _, id := range p.ids {
		out = append(out, p.m[id])
	}
	return out
}

func (p *goalPool) copy() *goalPool {
	c := newPool()
	for _, id := range p.ids {
		c.set(id, p.m[id])
	}
	return c
}

var poolCache = map[string]*goalPool{}

func invalidateGoalPoolCache() { poolCache = map[string]*goalPool{} }

func (s storage) invalidateDir() { delete(poolCache, s.root()) }

// ---- reading and writing files ----

func statIfPresent(p string) (os.FileInfo, error) {
	fi, err := os.Lstat(p)
	if err != nil && errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return fi, err
}

var pidForTemp = os.Getpid

func (s storage) atomicWrite(rootRel, rel, content string) error {
	if err := s.ensureDir(rootRel, true); err != nil {
		return err
	}
	file, err := s.resolveGoalPath(rootRel, rel)
	if err != nil {
		return err
	}
	if fi, err := statIfPresent(file); err != nil {
		return err
	} else if fi != nil && fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("Refusing to write symlinked goal file: %s", rel)
	}
	tmp := fmt.Sprintf("%s.%d.%d.tmp", file, pidForTemp(), clockMs())
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, file); err != nil {
		return err
	}
	if rootDir, err := s.path(rootRel); err == nil {
		delete(poolCache, s.root())
		_ = rootDir
	}
	return nil
}

func (s storage) resolveGoalPath(rootRel, rel string) (string, error) {
	root, err := s.path(rootRel)
	if err != nil {
		return "", err
	}
	abs, err := s.path(normalizeRelPath(rel))
	if err != nil {
		return "", err
	}
	r, _ := filepath.Rel(root, abs)
	if strings.HasPrefix(r, "..") || filepath.IsAbs(r) {
		return "", fmt.Errorf("Goal path escapes %s: %s", rootRel, rel)
	}
	return abs, nil
}

func (s storage) safeUnlink(rootRel, rel string) error {
	file, err := s.resolveGoalPath(rootRel, rel)
	if err != nil {
		return err
	}
	fi, err := statIfPresent(file)
	if err != nil {
		return err
	}
	if fi != nil && fi.Mode()&os.ModeSymlink == 0 {
		if err := os.Remove(file); err != nil {
			return err
		}
		s.invalidateDir()
		if rootRel == goalsDir {
			norm := normalizeRelPath(rel)
			s.updateSnapshot(func(goals []any) []any {
				var out []any
				for _, x := range goals {
					if g, ok := x.(*jsObject); ok && normalizeRelPath(strOrEmpty(g, "activePath")) == norm {
						continue
					}
					out = append(out, x)
				}
				return out
			})
		}
	}
	return nil
}

func taskCheckbox(status string) string {
	switch status {
	case "complete":
		return "x"
	case "skipped":
		return "~"
	}
	return " "
}

func taskLineSuffix(t *jsObject) string {
	var parts []string
	status := gstr(t, "status")
	if status == "complete" && truthy(t.vals["evidence"]) {
		parts = append(parts, "evidence: "+gstr(t, "evidence"))
	}
	if status == "skipped" && truthy(t.vals["skipReason"]) {
		parts = append(parts, "skipped: "+gstr(t, "skipReason"))
	}
	if status == "pending" && truthy(t.vals["verificationContract"]) {
		parts = append(parts, "contract: "+gstr(t, "verificationContract"))
	}
	if len(parts) == 0 {
		return ""
	}
	return " — " + strings.Join(parts, "; ")
}

// serializeGoalFile is the goal file: the JSON header, then the objective and a progress summary a person can read and edit.
func serializeGoalFile(g *jsObject) string {
	head := newObject()
	head.set("version", 3.0)
	for _, k := range g.keys {
		head.set(k, g.vals[k])
	}
	meta := marshalJSON(head, "  ")
	var pause []string
	if g.has("pauseReason") && truthy(g.vals["pauseReason"]) {
		pause = append(pause, "- Agent pause reason: "+gstr(g, "pauseReason"))
	}
	if g.has("pauseSuggestedAction") && truthy(g.vals["pauseSuggestedAction"]) {
		pause = append(pause, "- Agent suggests: "+gstr(g, "pauseSuggestedAction"))
	}
	pauseBlock := ""
	if len(pause) > 0 {
		pauseBlock = "\n" + strings.Join(pause, "\n")
	}
	taskSection := ""
	if tl := g.obj("taskList"); tl != nil {
		var lines []string
		tasks, _ := tl.vals["tasks"].([]any)
		for _, x := range tasks {
			t, _ := x.(*jsObject)
			if t == nil {
				continue
			}
			lines = append(lines, fmt.Sprintf("- [%s] %s: %s%s", taskCheckbox(gstr(t, "status")), gstr(t, "id"), gstr(t, "title"), taskLineSuffix(t)))
		}
		block, _ := tl.flag("blockCompletion")
		taskSection = fmt.Sprintf("\n## Tasks\n\n<!-- blockCompletion: %v -->\n%s\n", block, strings.Join(lines, "\n"))
	}
	contractLine := ""
	if c := jsTrim(strOrEmpty(g, "verificationContract")); c != "" {
		contractLine = "\n- Verification contract: " + c
	}
	auto, sis := "off", "no"
	if gflag(g, "autoContinue") {
		auto = "on"
	}
	if gflag(g, "sisyphus") {
		sis = "yes (prompt/criteria style)"
	}
	tokens, seconds := usageOf(g)
	return fmt.Sprintf("%s\n\n# Goal Prompt\n\n%s\n\n## Progress\n\n- Status: %s\n- Auto-continue: %s\n- Sisyphus mode: %s\n- Time spent: %s\n- Tokens used: %s%s%s%s\n",
		meta, jsTrim(gstr(g, "objective")), statusLabel(g), auto, sis, formatDuration(seconds), formatTokenValue(tokens), contractLine, taskSection, pauseBlock)
}

// findJSONObjectEnd returns the index of the brace that closes the first object, or -1.
func findJSONObjectEnd(content string) int {
	depth, inString, escaped := 0, false, false
	for i := 0; i < len(content); i++ {
		c := content[i]
		if inString {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

var goalPromptLines = regexp.MustCompile(`\r?\n`)

func extractObjectiveFromBody(body string) (string, bool) {
	lines := goalPromptLines.Split(strings.TrimLeftFunc(body, isJSSpace), -1)
	start := -1
	for i, l := range lines {
		if jsTrim(l) == "# Goal Prompt" {
			start = i
			break
		}
	}
	if start < 0 {
		t := jsTrim(body)
		return t, t != ""
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if jsTrim(lines[i]) == "## Progress" {
			end = i
			break
		}
	}
	t := jsTrim(strings.Join(lines[start+1:end], "\n"))
	return t, t != ""
}

// parseGoalContent parses a goal file's text; nil when it is not a goal.
func parseGoalContent(content string) *jsObject {
	end := findJSONObjectEnd(content)
	if end < 0 {
		return nil
	}
	v, err := parseJSON([]byte(content[:end+1]))
	if err != nil {
		return nil
	}
	raw, ok := v.(*jsObject)
	if !ok {
		return nil
	}
	merged := raw.clone()
	if obj, ok := extractObjectiveFromBody(content[end+1:]); ok {
		merged.set("objective", obj)
	}
	return normalizeGoalRecord(merged)
}

func parseGoalFile(file string) *jsObject {
	fi, err := os.Lstat(file)
	if err != nil || fi.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	return parseGoalContent(string(data))
}

func (s storage) writeActiveGoalFile(cur *jsObject) (*jsObject, error) {
	active := activePathForGoal(s, cur)
	n := cur.clone()
	n.set("activePath", active)
	n.set("updatedAt", nowIso())
	next := sanitizeGoalPaths(s, n)
	if err := s.atomicWrite(goalsDir, active, serializeGoalFile(next)); err != nil {
		return nil, err
	}
	s.updateSnapshot(func(goals []any) []any {
		var rest []any
		for _, x := range goals {
			if g, ok := x.(*jsObject); ok && gstr(g, "id") == gstr(next, "id") {
				continue
			}
			rest = append(rest, x)
		}
		return append(rest, next)
	})
	return next, nil
}

func (s storage) archiveGoalFile(cur *jsObject) (*jsObject, error) {
	archived := archivedPathForGoal(s, cur)
	n := cur.clone()
	n.set("archivedPath", archived)
	n.set("updatedAt", nowIso())
	next := sanitizeGoalPaths(s, n)
	delete(next.vals, "activePath")
	next.keys = removeKey(next.keys, "activePath")
	if err := s.atomicWrite(archivedGoalsDir, archived, serializeGoalFile(next)); err != nil {
		return nil, err
	}
	if p, ok := cur.vals["activePath"].(string); ok && isSafeActivePath(s, p) {
		_ = s.safeUnlink(goalsDir, p)
	}
	return next, nil
}

// mergeGoalPromptFromDisk takes the objective from the goal file, which people may have edited.
func (s storage) mergeGoalPromptFromDisk(cur *jsObject) *jsObject {
	p, ok := cur.vals["activePath"].(string)
	if !ok || !isSafeActivePath(s, p) {
		return cur
	}
	if pool := poolCache[s.root()]; pool != nil {
		if c := pool.get(gstr(cur, "id")); c != nil {
			n := cur.clone()
			n.set("objective", c.vals["objective"])
			return n
		}
	}
	file, err := s.resolveGoalPath(goalsDir, p)
	if err != nil {
		return cur
	}
	parsed := parseGoalFile(file)
	if parsed == nil {
		return cur
	}
	n := cur.clone()
	n.set("objective", parsed.vals["objective"])
	return n
}

func (s storage) scanActiveGoalFiles() []*jsObject {
	root := s.root()
	fi, err := os.Lstat(root)
	if err != nil || fi.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if activeNameRE.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.SliceStable(names, func(i, j int) bool { return localeCompare(names[i], names[j]) < 0 })
	var out []*jsObject
	for _, name := range names {
		rel := goalsDir + "/" + name
		if !isSafeActivePath(s, rel) {
			continue
		}
		file, err := s.resolveGoalPath(goalsDir, rel)
		if err != nil {
			continue
		}
		g := parseGoalFile(file)
		if g == nil || gstr(g, "status") == "complete" {
			continue
		}
		n := g.clone()
		n.set("activePath", rel)
		out = append(out, sanitizeGoalPaths(s, n))
	}
	return out
}

// ---- pool snapshot ----

func mtimeMs(fi os.FileInfo) float64 {
	t := fi.ModTime()
	return float64(t.Unix())*1e3 + float64(t.Nanosecond())/1e6
}

type snapshot struct {
	dirMtime float64
	goals    []any
}

func tryParseSnapshot(file string) *snapshot {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	v, err := parseJSON(data)
	if err != nil {
		return nil
	}
	o, ok := v.(*jsObject)
	if !ok {
		return nil
	}
	ver, _ := o.num("version")
	goals, isArr := o.vals["goals"].([]any)
	mt, isNum := o.num("dirMtimeMs")
	if ver != 1 || !isArr || !isNum {
		return nil
	}
	return &snapshot{dirMtime: mt, goals: goals}
}

func (s storage) readSnapshot() *snapshot {
	if sn := tryParseSnapshot(s.snapshotPath()); sn != nil {
		return sn
	}
	return tryParseSnapshot(filepath.Join(s.root(), snapshotLegacy))
}

func (s storage) writeSnapshotFile(sn *snapshot, mt float64) {
	head := newObject()
	head.set("version", 1.0)
	head.set("dirMtimeMs", mt)
	head.set("goals", append([]any{}, sn.goals...))
	target := s.snapshotPath()
	tmp := fmt.Sprintf("%s.%d.%d.tmp", target, pidForTemp(), clockMs())
	if os.WriteFile(tmp, []byte(marshalJSON(head, "")), 0o644) != nil {
		return
	}
	if os.Rename(tmp, target) != nil {
		return
	}
	_ = os.Remove(filepath.Join(s.root(), snapshotLegacy))
}

func (s storage) updateSnapshot(mutate func([]any) []any) {
	sn := s.readSnapshot()
	if sn == nil {
		return
	}
	sn.goals = mutate(sn.goals)
	fi, err := os.Lstat(s.root())
	if err != nil {
		return
	}
	s.writeSnapshotFile(sn, mtimeMs(fi))
}

func activeNamesMatch(names []string, sn *snapshot) bool {
	remaining := map[string]bool{}
	for _, n := range names {
		if activeNameRE.MatchString(n) {
			remaining[n] = true
		}
	}
	for _, x := range sn.goals {
		g, _ := x.(*jsObject)
		name := ""
		if g != nil {
			name = path.Base(normalizeRelPath(strOrEmpty(g, "activePath")))
		} else {
			name = path.Base(normalizeRelPath(""))
		}
		if activeNameRE.MatchString(name) {
			if !remaining[name] {
				return false
			}
			delete(remaining, name)
		}
	}
	return len(remaining) == 0
}

// readActiveGoalPoolView serves the cached pool, else the snapshot when the directory is unchanged, else a scan.
func (s storage) readActiveGoalPoolView() *goalPool {
	if p := poolCache[s.root()]; p != nil {
		return p
	}
	p := s.readPoolWithSnapshot()
	poolCache[s.root()] = p
	return p
}

func (s storage) readActiveGoalPool() *goalPool { return s.readActiveGoalPoolView().copy() }

func (s storage) readPoolWithSnapshot() *goalPool {
	fi, err := os.Lstat(s.root())
	if err == nil && fi.Mode()&os.ModeSymlink == 0 {
		if sn := s.readSnapshot(); sn != nil {
			match := sn.dirMtime == mtimeMs(fi)
			if !match {
				if entries, err := os.ReadDir(s.root()); err == nil {
					var names []string
					for _, e := range entries {
						names = append(names, e.Name())
					}
					match = activeNamesMatch(names, sn)
				}
			}
			if match {
				pool := newPool()
				for _, x := range sn.goals {
					g, ok := x.(*jsObject)
					if !ok || gstr(g, "status") == "complete" {
						continue
					}
					pool.set(gstr(g, "id"), g)
				}
				return pool
			}
		}
	}
	pool := newPool()
	for _, g := range s.scanActiveGoalFiles() {
		pool.set(gstr(g, "id"), g)
	}
	if fi, err := os.Lstat(s.root()); err == nil {
		goals := make([]any, 0, pool.size())
		for _, g := range pool.values() {
			goals = append(goals, g)
		}
		s.writeSnapshotFile(&snapshot{goals: goals}, mtimeMs(fi))
	}
	return pool
}

// ---- lock ----

type goalLock struct{ release func() }

var lockSleep = func(ms int) { time.Sleep(time.Duration(ms) * time.Millisecond) }

func safeLockName(id string) string {
	return regexp.MustCompile(`[^A-Za-z0-9._-]`).ReplaceAllString(id, "_")
}

func (s storage) acquireLock(goalID string, attempts, retryMs int) (*goalLock, error) {
	if attempts == 0 {
		attempts, retryMs = 8, 1
	}
	dir, err := s.path(lockDir)
	if err != nil {
		return nil, err
	}
	lockPath := filepath.Join(dir, safeLockName(goalID)+".lock")
	if err := s.ensureDir(lockDir, false); err != nil {
		return nil, err
	}
	payload := fmt.Sprintf(`{"pid":%d,"startedAt":"%s"}`, os.Getpid(), nowIso())
	for attempt := 0; attempt < attempts; attempt++ {
		f, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			_, _ = f.WriteString(payload)
			_ = f.Close()
			released := false
			return &goalLock{release: func() {
				if !released {
					released = true
					_ = os.Remove(lockPath)
				}
			}}, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		if fi, err := os.Stat(lockPath); err == nil {
			pid := lockPid(lockPath)
			stale := time.Since(fi.ModTime()) > 30*time.Second || (pid > 0 && !pidAlive(pid))
			if stale {
				_ = os.Remove(lockPath)
			}
			if stale {
				continue
			}
		} else {
			continue
		}
		lockSleep(retryMs)
	}
	return nil, fmt.Errorf("Timed out acquiring the goal lock for %s (%d attempts). Another writer may hold it.", goalID, attempts)
}

// ---- ledger ----

func ledgerEvent(typ string, kv ...any) *jsObject {
	o := newObject()
	o.set("type", typ)
	for i := 0; i+1 < len(kv); i += 2 {
		o.set(kv[i].(string), kv[i+1])
	}
	return o
}

var ledgerDirKnown = map[string]bool{}

// appendGoalEvents appends the events as JSON lines. A failure is returned, never fatal: the goal file is authoritative.
func (s storage) appendGoalEvents(events []*jsObject) error {
	if len(events) == 0 {
		return nil
	}
	file := s.mustPath(ledgerFile)
	dir := filepath.Dir(file)
	if !ledgerDirKnown[dir] {
		if err := s.ensureDir(goalsDir, false); err != nil {
			return err
		}
		ledgerDirKnown[dir] = true
	}
	var b strings.Builder
	for _, e := range events {
		b.WriteString(marshalJSON(e, ""))
		b.WriteString("\n")
	}
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(b.String())
	return err
}
