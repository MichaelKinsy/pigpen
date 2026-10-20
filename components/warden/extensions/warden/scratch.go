package warden

import (
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// Session scratch and host paths (src/guard.ts): paths the agent created under the OS temp directory in this session.
// A recursive rm whose every target is such a path, after symlinks are resolved, is risky rather than destructive.
// Everything here fails closed: a path that cannot be resolved, or that uses shell expansion, is not scratch.

// currentPlatform names the running platform the way Node's `process.platform` does.
func currentPlatform() string {
	if runtime.GOOS == "windows" {
		return "win32"
	}
	return runtime.GOOS
}

// scratchPlatform: whether the exemption applies on this platform. It rests on birth time being a real creation time:
// macOS and Windows report one; Linux without `statx` reports ctime, which `mv` updates, so moved-in content would pass
// the tree walk. Elsewhere nothing is recorded and a recursive rm is classified as if no scratch existed. An empty
// platform is the running one.
func scratchPlatform(platform string) bool {
	if platform == "" {
		platform = currentPlatform()
	}
	return platform == "darwin" || platform == "win32"
}

// ScratchIdentity is what a recorded path was when it was created. A path whose current identity differs was replaced
// and is not scratch.
type ScratchIdentity struct {
	Dev, Ino    uint64
	BirthtimeMs float64
}

// ScratchRecords are real paths the agent created under the temp directory in this session, with the identity each had.
type ScratchRecords map[string]ScratchIdentity

// scratchIdentity is the identity of a path itself (a symlink is not followed); false when it does not exist.
func scratchIdentity(path string) (ScratchIdentity, bool) {
	info, err := os.Lstat(path)
	if err != nil {
		return ScratchIdentity{}, false
	}
	dev, ino := fileDevIno(info)
	return ScratchIdentity{Dev: dev, Ino: ino, BirthtimeMs: birthtimeMs(path, info)}, true
}

func sameIdentity(path string, recorded ScratchIdentity) bool {
	now, ok := scratchIdentity(path)
	return ok && now == recorded
}

// pruneScratch drops records whose path is gone or now names a different file, so a later file at the same path is
// not scratch.
func pruneScratch(records ScratchRecords) {
	for path, identity := range records {
		if !sameIdentity(path, identity) {
			delete(records, path)
		}
	}
}

// tempRoots are the real paths of the temp roots that exist: the OS temp dir, $TMPDIR, /tmp and /private/tmp.
func tempRoots() []string {
	var roots []string
	seen := map[string]bool{}
	for _, root := range []string{os.TempDir(), os.Getenv("TMPDIR"), "/tmp", "/private/tmp"} {
		if root == "" || !isAbsPath(root) {
			continue
		}
		real, err := filepath.EvalSymlinks(root)
		if err != nil || seen[real] {
			continue
		}
		seen[real] = true
		roots = append(roots, real)
	}
	return roots
}

// realTarget is the real path of an absolute path: the realpath of its deepest existing ancestor plus the missing
// rest. "" when a component exists but cannot be resolved (a dangling symlink, a loop, no permission).
func realTarget(path string) string {
	var rest []string
	head, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	for {
		real, err := filepath.EvalSymlinks(head)
		if err == nil {
			for i := len(rest) - 1; i >= 0; i-- {
				real = filepath.Join(real, rest[i])
			}
			return real
		}
		if !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) {
			return ""
		}
		if _, lerr := os.Lstat(head); lerr == nil {
			return "" // a dangling symlink
		}
		parent := filepath.Dir(head)
		if parent == head {
			return ""
		}
		rest = append(rest, filepath.Base(head))
		head = parent
	}
}

// hostPathsEnv is the port's name for upstream PI_WARDEN_HOST_PATHS (deviation D6): a `:`-separated list.
const hostPathsEnv = "PIGPEN_WARDEN_HOST_PATHS"

// hostPaths are directories outside the project where the host lets its agent write, from the environment only. Relative
// entries, empty entries and a filesystem root are ignored. Each entry is kept as its real path, so a target is compared
// after `..` and symlinks are resolved on both sides.
func hostPaths(getenv func(string) string) []string {
	var roots []string
	seen := map[string]bool{}
	for _, entry := range strings.Split(getenv(hostPathsEnv), ":") {
		if entry == "" || !isAbsPath(entry) {
			continue
		}
		real := realTarget(entry)
		if real != "" && filepath.Dir(real) != real && !seen[real] {
			seen[real] = true
			roots = append(roots, real)
		}
	}
	return roots
}

// wardenHostPaths are the host paths plus warden's index directory, the only place outside the project a warden command
// asks the agent to write. The rest of the data directory stays held: the config file and other extensions' data.
// indexDir is `<home>/pigpen-warden/index` (upstream `<agent dir>/pi-warden/index`); "" leaves it out.
func wardenHostPaths(getenv func(string) string, indexDir string) []string {
	roots := hostPaths(getenv)
	if indexDir != "" {
		if index := safeIndexDir(indexDir); index != "" {
			dup := false
			for _, r := range roots {
				dup = dup || r == index
			}
			if !dup {
				roots = append(roots, index)
			}
		}
	}
	return roots
}

// safeIndexDir is the real index directory, or "" when a symlink could move it: the index directory or a directory
// between it and the root two levels up is a symlink, or its real path is not inside that root's real path.
func safeIndexDir(indexDir string) string {
	index, err := filepath.Abs(indexDir)
	if err != nil {
		return ""
	}
	root := filepath.Dir(filepath.Dir(index))
	for dir := index; dir != root && filepath.Dir(dir) != dir; dir = filepath.Dir(dir) {
		info, err := os.Lstat(dir)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue // not created yet: realTarget resolves what exists above it
			}
			return ""
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return ""
		}
	}
	real, realRoot := realTarget(index), realTarget(root)
	if real == "" || realRoot == "" || !strings.HasPrefix(real, realRoot+string(filepath.Separator)) {
		return ""
	}
	return real
}

// inHostPath: whether a target lies in a host path, after `..` and symlinks are resolved. An unresolvable target is not in one.
func inHostPath(target string, roots []string) bool {
	if len(roots) == 0 {
		return false
	}
	real := realTarget(target)
	if real == "" {
		return false
	}
	for _, root := range roots {
		if real == root || strings.HasPrefix(real, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// tempRootOf is the temp root a real path lies strictly under; a temp root itself has none.
func tempRootOf(real string, roots []string) string {
	for _, root := range roots {
		prefix := root
		if !strings.HasSuffix(prefix, string(filepath.Separator)) {
			prefix += string(filepath.Separator)
		}
		if real != root && strings.HasPrefix(real, prefix) {
			return root
		}
	}
	return ""
}

var (
	// A privilege-raising command word anywhere in a command.
	rePrivileged = lazyRE("(?m)(?:^|[\\s;&|(\"'`])(?:sudo|doas|su|pkexec|run0)(?:\\s|$)")
	// Command words that can put existing data under a temp path before a later rm deletes it: a move, a link (`rm -rf
	// link/` follows it), a copy or extract that can carry links (`cp -R`, `tar -x`, `git clone`), a sync that removes its
	// source, a mount. The birth-time walk runs before the command, so it cannot see what the command itself moves in. Any
	// `cp` or `tar` counts, whatever its flags. Read on `unquoted` text, so `\mv`, `"ln"`, and `l''n` count too.
	reMovesIn = lazyRE("(?m)(?:^|[\\s;&|(`/])(?:(?:g|bsd)?(?:mv|ln|cp|tar)|rsync|mount|hdiutil|bindfs|git(?:\\s[^;&|\\n]*)?\\sclone)(?:[\\s;&|)`]|$)")
)

// unquoted is a command with its quotes and backslashes removed, so a quoted or escaped command word reads as the word
// it runs.
func unquoted(command string) string {
	var b strings.Builder
	for i := 0; i < len(command); i++ {
		c := command[i]
		if c == '$' && i+1 < len(command) && (command[i+1] == '\'' || command[i+1] == '"') {
			continue
		}
		if c == '\'' || c == '"' || c == '\\' {
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// literalPath: a literal absolute path: no quotes left inside, no glob, brace, tilde, variable, escape, or substitution.
func literalPath(s string) bool {
	if !strings.HasPrefix(s, "/") {
		return false
	}
	for _, r := range s[1:] {
		if strings.ContainsRune("*?[]{}$`~\\\"'", r) || isJSSpace(r) {
			return false
		}
	}
	return true
}

// scratchBudget is how much of the target trees one rm segment may walk: entries seen, and the clock time it must
// finish by. 10,000 entries covers a generated test tree of a few thousand directories with room to spare, and 200 ms
// keeps the synchronous walk in the tool-call hook short. A tree that needs more is not checked, so it keeps the hold.
type scratchBudget struct {
	entries  int
	deadline time.Time
}

func newScratchBudget() *scratchBudget {
	return &scratchBudget{entries: 10_000, deadline: time.Now().Add(200 * time.Millisecond)}
}

// bornAfter: true when every entry of the tree at path, the root included, has a birth time at or after born. Symlinks
// are checked as links and never followed. False when the root is missing, an entry has no birth time or cannot be
// read, or the walk runs past its budget: content moved in from elsewhere keeps its older birth time and must not pass.
func bornAfter(path string, born float64, budget *scratchBudget) bool {
	if !(born > 0) {
		return false
	}
	if budget == nil {
		budget = newScratchBudget()
	}
	stack := []string{path}
	for len(stack) > 0 {
		budget.entries--
		if budget.entries < 0 || time.Now().After(budget.deadline) {
			return false
		}
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		info, err := os.Lstat(current)
		if err != nil {
			return false
		}
		birth := birthtimeMs(current, info)
		if !(birth > 0) || birth < born {
			return false
		}
		if info.IsDir() {
			names, err := os.ReadDir(current)
			if err != nil {
				return false
			}
			for _, n := range names {
				stack = append(stack, filepath.Join(current, n.Name()))
			}
		}
	}
	return true
}

func stripEdgeQuotes(target string) string {
	if strings.HasPrefix(target, `"`) || strings.HasPrefix(target, "'") {
		target = target[1:]
	}
	if strings.HasSuffix(target, `"`) || strings.HasSuffix(target, "'") {
		target = target[:len(target)-1]
	}
	return target
}

func hasDotDot(path string) bool {
	for _, part := range strings.Split(path, "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

func isSessionScratch(target string, scratch ScratchRecords, budget *scratchBudget) bool {
	clean := stripEdgeQuotes(target)
	if !literalPath(clean) || hasDotDot(clean) {
		return false
	}
	real := realTarget(clean)
	if real == "" {
		return false
	}
	root := tempRootOf(real, tempRoots())
	if root == "" {
		return false
	}
	for path := real; path != root && strings.HasPrefix(path, root); path = filepath.Dir(path) {
		if recorded, ok := scratch[path]; ok {
			return sameIdentity(path, recorded) && bornAfter(real, recorded.BirthtimeMs, budget)
		}
	}
	return false
}

// mkdirTargets are `mkdir` targets in a command that are literal absolute paths; flags and relative operands are skipped.
func mkdirTargets(command string) []string {
	var targets []string
	for _, segment := range splitShell(StripDataText(command).Text) {
		if head, _ := headOf(segment); head != "mkdir" {
			continue
		}
		tokens := jsSplitWhitespace(jsTrim(segment))
		start := 0
		for i, token := range tokens {
			if stripPathPrefix(token) == "mkdir" {
				start = i + 1
				break
			}
		}
		for _, token := range tokens[start:] {
			clean := stripEdgeQuotes(token)
			if literalPath(clean) && !hasDotDot(clean) {
				targets = append(targets, clean)
			}
		}
	}
	return targets
}

// scratchCandidates are real paths under a temp root that this call may create and that do not exist yet: `mkdir`
// targets of a shell command, or a written file. Each path and its missing ancestors below the temp root are listed.
// Taken before the call runs, so a directory that already existed is never recorded as created.
func scratchCandidates(tool string, input map[string]any, cwd, platform string) []string {
	if !scratchPlatform(platform) {
		return nil
	}
	var paths []string
	if view, ok := CommandOf(tool, input); ok && tool == "bash" && view.Command != "" {
		paths = mkdirTargets(view.Command)
	} else if path, ok := input["path"].(string); ok && tool == "write" && !strings.HasPrefix(path, "~") {
		paths = []string{resolveFrom(cwd, path)}
	}
	roots := tempRoots()
	var missing []string
	seen := map[string]bool{}
	for _, path := range paths {
		real := realTarget(path)
		if real == "" {
			continue
		}
		root := tempRootOf(real, roots)
		if root == "" {
			continue
		}
		for current := real; current != root; current = filepath.Dir(current) {
			if _, err := os.Stat(current); err == nil {
				break
			}
			if !seen[current] {
				seen[current] = true
				missing = append(missing, current)
			}
		}
	}
	return missing
}

// resolvePath is node's path.resolve(cwd, path).
func resolveFrom(cwd, path string) string {
	if isAbsPath(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(cwd, path)
}

// scratchOutputLimit: at most this many temp paths are read from one tool result.
const scratchOutputLimit = 20

// tempPathsIn are absolute paths in text that start with a temp root, as spelled or as resolved.
func tempPathsIn(text string, roots []string) []string {
	var prefixes []string
	seen := map[string]bool{}
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			prefixes = append(prefixes, p)
		}
	}
	for _, r := range roots {
		add(r)
	}
	for _, root := range []string{os.TempDir(), os.Getenv("TMPDIR"), "/tmp", "/private/tmp"} {
		if root != "" && isAbsPath(root) {
			add(strings.TrimRight(root, "/"))
		}
	}
	if len(prefixes) == 0 {
		return nil
	}
	quoted := make([]string, len(prefixes))
	for i, p := range prefixes {
		quoted[i] = regexp.QuoteMeta(p)
	}
	pattern := regexp.MustCompile("(?m)(?:^|[\\s\"'=:(\\[])((?:" + strings.Join(quoted, "|") + ")/[^\\s\"'`:,;()\\[\\]{}<>*?$\\\\]+)")
	var found []string
	have := map[string]bool{}
	for _, m := range pattern.FindAllStringSubmatch(text, -1) {
		p := strings.TrimRight(m[1], "./")
		if !have[p] {
			have[p] = true
			found = append(found, p)
		}
		if len(found) >= scratchOutputLimit {
			break
		}
	}
	return found
}

// birthtimeFunc reads a file's birth time in milliseconds, 0 when the file system has none.
type birthtimeFunc func(path string, info fs.FileInfo) float64

// createdScratch are real paths the call created, read after it ran, each with its identity. A candidate from
// scratchCandidates counts when it now exists as itself (not through a symlink). For a shell command, absolute temp paths
// printed in its output also count, bounded: the first 20 such paths only, each must exist under a temp root as a real
// directory or file, and its birth time must fall in a later millisecond than started (the clock when the call began, in
// milliseconds). Where the file system reports no birth time, a printed path counts only from a command that does nothing
// but run `mktemp` and print what it made (mktempOnly). Nothing is recorded where scratchPlatform is false.
func createdScratch(tool string, input map[string]any, output string, started int64, candidates []string, birthtime birthtimeFunc, platform string) ScratchRecords {
	created := ScratchRecords{}
	if !scratchPlatform(platform) {
		return created
	}
	if birthtime == nil {
		birthtime = birthtimeMs
	}
	var found []string
	for _, path := range candidates {
		if real, err := filepath.EvalSymlinks(path); err == nil && real == path {
			found = append(found, path)
		}
	}
	if view, ok := CommandOf(tool, input); ok && tool == "bash" && view.Command != "" {
		found = append(found, printedScratch(view.Command, output, started, birthtime)...)
	}
	for _, path := range found {
		if identity, ok := scratchIdentity(path); ok {
			created[path] = identity
		}
	}
	return created
}

var (
	// Arguments of a `mktemp` that creates something: literal words only, and no `-u`/`--dry-run`.
	mktempArgs       = "((?:\\s+[^\\s$`<>()\"']+)*)"
	reMktempAlone    = lazyRE("^mktemp" + mktempArgs + "$")
	reMktempAssign   = lazyRE("^([A-Za-z_]\\w*)=\\$\\(\\s*mktemp" + mktempArgs + "\\s*\\)$")
	reMktempAssignQ  = lazyRE("^([A-Za-z_]\\w*)=\"\\$\\(\\s*mktemp" + mktempArgs + "\\s*\\)\"$")
	reEchoVars       = lazyRE(`^echo((?:\s+"?\$\{?[A-Za-z_]\w*\}?"?)+)$`)
	reEchoVarName    = lazyRE(`\$\{?([A-Za-z_]\w*)`)
	reMktempDryRunOp = lazyRE(`(?:^|\s)(?:-[a-zA-Z]*u[a-zA-Z]*|--dry-run)(?:\s|$)`)
)

// mktempOnly is how many paths a command that only makes temp files may print: each segment is a bare `mktemp`, an
// assignment `name=$(mktemp …)`, or an `echo` of names assigned that way. 0 for any other command.
func mktempOnly(command string) int {
	assigned := map[string]bool{}
	made := 0
	for _, segment := range splitShell(command) {
		var args string
		matched, name := false, ""
		if m := reMktempAlone.FindStringSubmatch(segment); m != nil {
			args, matched = m[1], true
		} else if m := reMktempAssign.FindStringSubmatch(segment); m != nil {
			args, matched, name = m[2], true, m[1]
		} else if m := reMktempAssignQ.FindStringSubmatch(segment); m != nil {
			args, matched, name = m[2], true, m[1]
		}
		if matched {
			if reMktempDryRunOp.MatchString(args) {
				return 0
			}
			if name != "" {
				assigned[name] = true
			}
			made++
			continue
		}
		echo := reEchoVars.FindStringSubmatch(segment)
		if echo == nil {
			return 0
		}
		for _, v := range reEchoVarName.FindAllStringSubmatch(echo[1], -1) {
			if !assigned[v[1]] {
				return 0
			}
		}
	}
	return made
}

func printedScratch(command, output string, started int64, birthtime birthtimeFunc) []string {
	var created []string
	made := mktempOnly(command)
	var lines []string
	for _, line := range strings.Split(output, "\n") {
		if l := jsTrim(line); l != "" {
			lines = append(lines, l)
		}
	}
	roots := tempRoots()
	for _, path := range tempPathsIn(output, roots) {
		if hasDotDot(path) {
			continue
		}
		real, err := filepath.EvalSymlinks(path)
		if err != nil || tempRootOf(real, roots) == "" {
			continue
		}
		info, err := os.Stat(real)
		if err != nil || !(info.IsDir() || info.Mode().IsRegular()) {
			continue
		}
		var born bool
		if birth := birthtime(real, info); birth > 0 {
			born = int64(math.Floor(birth)) > started
		} else if made > 0 && len(lines) <= made {
			for _, l := range lines {
				born = born || l == path
			}
		}
		if born {
			created = append(created, real)
		}
	}
	return created
}
