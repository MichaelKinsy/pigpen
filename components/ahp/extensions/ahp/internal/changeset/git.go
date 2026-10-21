package changeset

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	ahptypes "github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/svc"
)

const (
	gitBlobScheme                         = "git-blob:"
	maxGitOutput                          = 32 * 1024 * 1024
	maxUntrackedLineCountBytes            = 8 * 1024 * 1024
	maxTotalUntrackedLineCountBytes       = 32 * 1024 * 1024
	gitTimeout                            = 30 * time.Second
	maxSafeInteger                  int64 = 1<<53 - 1
)

// gitContextOverrides are the environment variables that would make git look at a repository
// other than the session's working directory.
var gitContextOverrides = map[string]bool{
	"GIT_ALTERNATE_OBJECT_DIRECTORIES": true, "GIT_COMMON_DIR": true, "GIT_CONFIG_COUNT": true,
	"GIT_CONFIG_PARAMETERS": true, "GIT_DIR": true, "GIT_INDEX_FILE": true, "GIT_NAMESPACE": true,
	"GIT_OBJECT_DIRECTORY": true, "GIT_PREFIX": true, "GIT_QUARANTINE_PATH": true, "GIT_SHALLOW_FILE": true,
	"GIT_WORK_TREE": true,
}

// Workspace is the repository a session's working directory lives in. Head and Parent are empty
// before the first commit and for a root commit.
type Workspace struct {
	Cwd                string
	RepositoryRoot     string
	GitDirectory       string
	CommonGitDirectory string
	Pathspec           string
	Head               string
	Parent             string
}

// BlobRef names one git object of a session: the content behind a git-blob: URI.
type BlobRef struct {
	SessionID string
	OID       string
	Path      string
}

// Git is the repository access behind the service; tests replace it.
type Git interface {
	// Inspect describes the repository of cwd; nil when cwd is not inside a work tree.
	Inspect(ctx context.Context, cwd string) (*Workspace, error)
	Compute(ctx context.Context, workspace Workspace, sessionID string, kind Kind) ([]ahptypes.ChangesetFile, error)
	IgnoredDirectories(ctx context.Context, workspace Workspace) ([]string, error)
	PathForBlob(workspace Workspace, blob BlobRef) (string, bool)
	ReadBlob(ctx context.Context, workspace Workspace, blob BlobRef) ([]byte, error)
}

// gitError is a failure of the git command line or of what it printed.
type gitError struct{ message string }

func (e *gitError) Error() string { return e.message }

func gitErrorf(format string, args ...any) error { return &gitError{fmt.Sprintf(format, args...)} }

func gitEnvironment() []string {
	var env []string
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if gitContextOverrides[key] || strings.HasPrefix(key, "GIT_CONFIG_KEY_") || strings.HasPrefix(key, "GIT_CONFIG_VALUE_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "GIT_NO_LAZY_FETCH=1", "GIT_OPTIONAL_LOCKS=0", "GIT_PAGER=cat", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
}

// limitedBuffer collects output up to a cap and then fails the write, which stops the command.
type limitedBuffer struct {
	buf      bytes.Buffer
	overflow bool
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if l.buf.Len()+len(p) > maxGitOutput {
		l.overflow = true
		return 0, errors.New("maxBuffer exceeded")
	}
	return l.buf.Write(p)
}

// runGit runs git in cwd. With allowFailure a git that is missing or exits non-zero is reported as
// ok=false rather than as an error.
func runGit(ctx context.Context, cwd string, args []string, allowFailure bool) (out []byte, ok bool, err error) {
	runCtx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	full := append([]string{"--literal-pathspecs", "-c", "core.filemode=false", "-c", "core.fsmonitor=false"}, args...)
	cmd := exec.CommandContext(runCtx, "git", full...)
	cmd.Dir = cwd
	cmd.Env = gitEnvironment()
	var stdout limitedBuffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if runErr == nil {
		return stdout.buf.Bytes(), true, nil
	}
	if ctx.Err() != nil {
		return nil, false, context.Cause(ctx)
	}
	var exit *exec.ExitError
	numericExit := errors.As(runErr, &exit) && exit.ExitCode() >= 0 && !stdout.overflow
	missing := errors.Is(runErr, exec.ErrNotFound) || errors.Is(runErr, fs.ErrNotExist)
	if allowFailure && (missing || numericExit) {
		return nil, false, nil
	}
	if missing {
		return nil, false, gitErrorf("git executable was not found")
	}
	if stdout.overflow {
		return nil, false, gitErrorf("stdout maxBuffer length exceeded")
	}
	if detail := strings.TrimSpace(stderr.String()); detail != "" {
		return nil, false, gitErrorf("%s", detail)
	}
	return nil, false, gitErrorf("Command failed: git %s", strings.Join(full, " "))
}

func utf8Text(output []byte, source string) (string, error) {
	if !utf8.Valid(output) {
		return "", gitErrorf("%s returned a non-UTF-8 path", source)
	}
	return string(output), nil
}

// gitLine runs git and returns its output as one line, "" when git failed or printed nothing.
func gitLine(ctx context.Context, cwd string, args []string, allowFailure bool) (string, error) {
	out, ok, err := runGit(ctx, cwd, args, allowFailure)
	if err != nil {
		return "", err
	}
	return outputLine(out, ok)
}

// outputLine is the first line git printed, without its line ending; "" when there is none.
func outputLine(output []byte, ok bool) (string, error) {
	if !ok {
		return "", nil
	}
	value, err := utf8Text(output, "git")
	if err != nil {
		return "", err
	}
	value = strings.TrimSuffix(value, "\n")
	value = strings.TrimSuffix(value, "\r")
	return value, nil
}

func nulPaths(output []byte, ok bool, source string) ([]string, error) {
	if !ok || len(output) == 0 {
		return nil, nil
	}
	value, err := utf8Text(output, source)
	if err != nil {
		return nil, err
	}
	if !strings.HasSuffix(value, "\x00") {
		return nil, gitErrorf("%s returned an unterminated path list", source)
	}
	return strings.Split(value[:len(value)-1], "\x00"), nil
}

// lazyRegexp compiles on first use: package initialisation is on the extension's cold start path.
func lazyRegexp(expr string) func() *regexp.Regexp {
	return sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(expr) })
}

var (
	oidPattern      = lazyRegexp(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	zeroOidPattern  = lazyRegexp(`^0+$`)
	blobModePattern = lazyRegexp(`^100[0-7]{3}$`)
	digitsPattern   = lazyRegexp(`^\d+$`)
	base64urlText   = lazyRegexp(`^[A-Za-z0-9_-]+$`)
)

func validOID(value string) bool { return oidPattern().MatchString(value) }

func blobOID(mode, oid string) string {
	if blobModePattern().MatchString(mode) && validOID(oid) && !zeroOidPattern().MatchString(oid) {
		return oid
	}
	return ""
}

// gitPathToAbsolute resolves a repository-relative path, refusing anything that is not a plain
// relative path inside the repository.
func gitPathToAbsolute(repositoryRoot, path string) (string, bool) {
	if path == "" || strings.HasPrefix(path, "/") {
		return "", false
	}
	segments := strings.Split(path, "/")
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return "", false
		}
	}
	absolute := filepath.Join(append([]string{repositoryRoot}, segments...)...)
	rel, err := filepath.Rel(repositoryRoot, absolute)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return absolute, true
}

func isInside(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

// parsedChange is one entry of git's combined raw and numstat output. Added and Removed are -1
// when git printed "-" (a binary file).
type parsedChange struct {
	oldPath, newPath string
	oldOID, newOID   string
	added, removed   int64
}

func numberField(value string) (int64, error) {
	if value == "-" {
		return -1, nil
	}
	if value == "" || !digitsPattern().MatchString(value) {
		return 0, gitErrorf("unexpected git numstat output")
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed > maxSafeInteger {
		return 0, gitErrorf("git numstat exceeds the supported range")
	}
	return parsed, nil
}

type numstat struct{ added, removed int64 }

// parseGitChanges parses the combined NUL-delimited `git diff --raw --numstat` stream.
func parseGitChanges(output []byte) ([]parsedChange, error) {
	if len(output) == 0 {
		return nil, nil
	}
	value, err := utf8Text(output, "git diff")
	if err != nil {
		return nil, err
	}
	if !strings.HasSuffix(value, "\x00") {
		return nil, gitErrorf("git diff returned an unterminated record stream")
	}
	segments := strings.Split(value, "\x00")
	next := func() (string, bool) {
		if len(segments) == 0 {
			return "", false
		}
		s := segments[0]
		segments = segments[1:]
		return s, true
	}
	var changes []parsedChange
	stats := map[string]numstat{}
	for len(segments) > 0 {
		segment, _ := next()
		if segment == "" {
			continue
		}
		if strings.HasPrefix(segment, ":") {
			fields := strings.Split(segment[1:], " ")
			field := func(i int) string {
				if i < len(fields) {
					return fields[i]
				}
				return ""
			}
			oldMode, newMode, oldOID, newOID := field(0), field(1), field(2), field(3)
			status := ""
			if f := field(4); f != "" {
				status = f[:1]
			}
			firstPath, _ := next()
			if firstPath == "" || oldMode == "" || newMode == "" || status == "" || !strings.Contains("ADMR", status) {
				return nil, gitErrorf("unexpected git raw diff output")
			}
			secondPath := ""
			if status == "R" {
				secondPath, _ = next()
				if secondPath == "" {
					return nil, gitErrorf("git rename is missing its destination")
				}
			}
			oldBlob, newBlob := blobOID(oldMode, oldOID), blobOID(newMode, newOID)
			if (oldMode != "000000" && !blobModePattern().MatchString(oldMode)) || (newMode != "000000" && !blobModePattern().MatchString(newMode)) {
				continue
			}
			switch status {
			case "A":
				changes = append(changes, parsedChange{newPath: firstPath, newOID: newBlob})
			case "D":
				changes = append(changes, parsedChange{oldPath: firstPath, oldOID: oldBlob})
			case "M":
				changes = append(changes, parsedChange{oldPath: firstPath, newPath: firstPath, oldOID: oldBlob, newOID: newBlob})
			case "R":
				changes = append(changes, parsedChange{oldPath: firstPath, newPath: secondPath, oldOID: oldBlob, newOID: newBlob})
			}
			continue
		}

		firstTab := strings.IndexByte(segment, '\t')
		secondTab := -1
		if firstTab >= 0 {
			if i := strings.IndexByte(segment[firstTab+1:], '\t'); i >= 0 {
				secondTab = firstTab + 1 + i
			}
		}
		if firstTab < 0 || secondTab < 0 {
			return nil, gitErrorf("unexpected git numstat output")
		}
		addedField, removedField, inlinePath := segment[:firstTab], segment[firstTab+1:secondTab], segment[secondTab+1:]
		oldPath, newPath := inlinePath, inlinePath
		if inlinePath == "" {
			oldPath, _ = next()
			newPath, _ = next()
			if oldPath == "" || newPath == "" {
				return nil, gitErrorf("git rename numstat is missing a path")
			}
		}
		key := newPath
		if key == "" {
			key = oldPath
		}
		if key != "" {
			added, err := numberField(addedField)
			if err != nil {
				return nil, err
			}
			removed, err := numberField(removedField)
			if err != nil {
				return nil, err
			}
			stats[key] = numstat{added, removed}
		}
	}
	for i := range changes {
		key := changes[i].newPath
		if key == "" {
			key = changes[i].oldPath
		}
		counts, ok := stats[key]
		if !ok {
			return nil, gitErrorf("git diff is missing numstat data")
		}
		changes[i].added, changes[i].removed = counts.added, counts.removed
	}
	return changes, nil
}

// lineCount counts the lines of a text file; -1 for content that is not UTF-8 text.
func lineCount(buffer []byte) int64 {
	if !utf8.Valid(buffer) || bytes.IndexByte(buffer, 0) >= 0 {
		return -1
	}
	if len(buffer) == 0 {
		return 0
	}
	lines := int64(bytes.Count(buffer, []byte{'\n'}))
	if buffer[len(buffer)-1] == '\n' {
		return lines
	}
	return lines + 1
}

type boundedFile struct {
	regular bool
	buffer  []byte // nil when the file was not read
	bytes   int64
}

// boundedRegularFile reads a regular file without following a final symlink, within the budget.
// nil means the path vanished or is a link that was swapped in.
func boundedRegularFile(path string, budget int64) (*boundedFile, error) {
	initial, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if !initial.Mode().IsRegular() {
		return &boundedFile{}, nil
	}
	if budget <= 0 || initial.Size() > maxUntrackedLineCountBytes || initial.Size() > budget {
		return &boundedFile{regular: true}, nil
	}
	f, err := openNoFollow(path)
	if err != nil {
		if isGoneOrLink(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return &boundedFile{}, nil
	}
	if info.Size() > maxUntrackedLineCountBytes || info.Size() > budget {
		return &boundedFile{regular: true}, nil
	}
	buffer := make([]byte, info.Size())
	n, err := io.ReadFull(f, buffer)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	return &boundedFile{regular: true, buffer: buffer[:n], bytes: info.Size()}, nil
}

func diffCounts(change parsedChange) *ahptypes.FileEditDiffStats {
	if change.added < 0 && change.removed < 0 {
		return nil
	}
	var stats ahptypes.FileEditDiffStats
	if change.added >= 0 {
		added := change.added
		stats.Added = &added
	}
	if change.removed >= 0 {
		removed := change.removed
		stats.Removed = &removed
	}
	return &stats
}

// fileURL is Node's pathToFileURL(path).toString().
func fileURL(path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return "file://" + percentEncodePath(p)
}

// percentEncodePath encodes the bytes WHATWG URL parsing (and Node's pathToFileURL on top of it)
// escapes in a file path.
func percentEncodePath(p string) string {
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		c := p[i]
		if c <= 0x20 || c >= 0x7f || strings.IndexByte("\"#%<>?[\\]^`{|}~", c) >= 0 {
			b.WriteByte('%')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&15])
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func gitBlobURI(sessionID, oid, path, displayPath string) string {
	metadata, _ := json.Marshal(struct {
		SessionID string `json:"sessionId"`
		OID       string `json:"oid"`
		Path      string `json:"path"`
	}{sessionID, oid, path})
	// Keep the query one opaque, unreserved token so URI parse/serialize round-trips preserve it.
	pathname := percentEncodePath(filepath.ToSlash(displayPath))
	if !strings.HasPrefix(pathname, "/") {
		pathname = "/" + pathname
	}
	return gitBlobScheme + pathname + "?" + base64.RawURLEncoding.EncodeToString(metadata)
}

// ParseBlobURI recognises a git-blob: URI this service issued.
func ParseBlobURI(uri string) (BlobRef, bool) {
	rest, ok := strings.CutPrefix(uri, gitBlobScheme)
	if !ok || strings.Contains(rest, "#") {
		return BlobRef{}, false
	}
	_, encoded, found := strings.Cut(rest, "?")
	if !found || !base64urlText().MatchString(encoded) {
		return BlobRef{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return BlobRef{}, false
	}
	var value struct {
		SessionID *string `json:"sessionId"`
		OID       *string `json:"oid"`
		Path      *string `json:"path"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return BlobRef{}, false
	}
	if value.SessionID == nil || *value.SessionID == "" || value.OID == nil || !validOID(*value.OID) || value.Path == nil {
		return BlobRef{}, false
	}
	return BlobRef{SessionID: *value.SessionID, OID: *value.OID, Path: *value.Path}, true
}

// GitChanges is the git command-line implementation of [Git]. It never modifies the repository
// or its index.
type GitChanges struct{}

// Inspect implements [Git].
func (GitChanges) Inspect(ctx context.Context, cwd string) (*Workspace, error) {
	root, err := gitLine(ctx, cwd, []string{"rev-parse", "--show-toplevel"}, true)
	if err != nil {
		return nil, err
	}
	if root == "" {
		return nil, nil
	}
	canonicalCwd, err := realpath(cwd)
	if err != nil {
		return nil, err
	}
	canonicalRoot, err := realpath(root)
	if err != nil {
		return nil, err
	}
	if !isInside(canonicalRoot, canonicalCwd) {
		return nil, nil
	}
	rel, _ := filepath.Rel(canonicalRoot, canonicalCwd)
	pathspec := filepath.ToSlash(rel)
	if pathspec == "" {
		pathspec = "."
	}
	headValue, err := gitLine(ctx, canonicalRoot, []string{"rev-parse", "--verify", "HEAD"}, true)
	if err != nil {
		return nil, err
	}
	gitDirOut, gitDirOK, err := runGit(ctx, canonicalRoot, []string{"rev-parse", "--absolute-git-dir"}, false)
	if err != nil {
		return nil, err
	}
	commonOut, commonOK, err := runGit(ctx, canonicalRoot, []string{"rev-parse", "--git-common-dir"}, false)
	if err != nil {
		return nil, err
	}
	head := ""
	if validOID(headValue) {
		head = headValue
	}
	parent := ""
	if head != "" {
		parentValue, err := gitLine(ctx, canonicalRoot, []string{"rev-parse", "--verify", "HEAD^1"}, true)
		if err != nil {
			return nil, err
		}
		if validOID(parentValue) {
			parent = parentValue
		}
	}
	gitDirectory, err := outputLine(gitDirOut, gitDirOK)
	if err != nil {
		return nil, err
	}
	commonDirectory, err := outputLine(commonOut, commonOK)
	if err != nil {
		return nil, err
	}
	if gitDirectory == "" || commonDirectory == "" {
		return nil, gitErrorf("git did not report its metadata directory")
	}
	realGitDirectory, err := realpath(resolveFrom(canonicalRoot, gitDirectory))
	if err != nil {
		return nil, err
	}
	realCommonDirectory, err := realpath(resolveFrom(canonicalRoot, commonDirectory))
	if err != nil {
		return nil, err
	}
	return &Workspace{
		Cwd: canonicalCwd, RepositoryRoot: canonicalRoot, GitDirectory: realGitDirectory,
		CommonGitDirectory: realCommonDirectory, Pathspec: pathspec, Head: head, Parent: parent,
	}, nil
}

func resolveFrom(base, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(base, path)
}

func realpath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

var diffFlags = []string{"--no-ext-diff", "--no-textconv", "--raw", "--numstat", "--no-abbrev", "--diff-filter=ADMR", "--find-renames", "-z"}

// Compute implements [Git].
func (g GitChanges) Compute(ctx context.Context, workspace Workspace, sessionID string, kind Kind) ([]ahptypes.ChangesetFile, error) {
	if kind == LatestCommit {
		if workspace.Head == "" {
			return nil, nil
		}
		var args []string
		if workspace.Parent != "" {
			args = append(append([]string{"diff"}, diffFlags...), workspace.Parent, workspace.Head, "--", workspace.Pathspec)
		} else {
			args = append(append([]string{"diff-tree", "--no-ext-diff", "--no-textconv", "--root", "--no-commit-id", "-r"}, diffFlags[2:]...), workspace.Head, "--", workspace.Pathspec)
		}
		output, _, err := runGit(ctx, workspace.RepositoryRoot, args, false)
		if err != nil {
			return nil, err
		}
		changes, err := parseGitChanges(output)
		if err != nil {
			return nil, err
		}
		return g.toFiles(workspace, sessionID, LatestCommit, changes)
	}

	var changes []parsedChange
	if workspace.Head != "" {
		args := append(append([]string{"diff"}, diffFlags...), workspace.Head, "--", workspace.Pathspec)
		output, _, err := runGit(ctx, workspace.RepositoryRoot, args, false)
		if err != nil {
			return nil, err
		}
		parsed, err := parseGitChanges(output)
		if err != nil {
			return nil, err
		}
		changes = append(changes, parsed...)
	}
	listArgs := []string{"ls-files"}
	if workspace.Head == "" {
		listArgs = append(listArgs, "--cached")
	}
	listArgs = append(listArgs, "--others", "--exclude-standard", "-z", "--", workspace.Pathspec)
	untracked, untrackedOK, err := runGit(ctx, workspace.RepositoryRoot, listArgs, false)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, change := range changes {
		if change.newPath != "" {
			known[change.newPath] = true
		} else {
			known[change.oldPath] = true
		}
	}
	remaining := int64(maxTotalUntrackedLineCountBytes)
	paths, err := nulPaths(untracked, untrackedOK, "git ls-files")
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		if known[path] {
			continue
		}
		absolute, ok := gitPathToAbsolute(workspace.RepositoryRoot, path)
		if !ok || !isInside(workspace.Cwd, absolute) {
			continue
		}
		content, err := boundedRegularFile(absolute, remaining)
		if err != nil {
			return nil, err
		}
		if content == nil || !content.regular {
			continue
		}
		change := parsedChange{newPath: path, added: -1, removed: -1}
		if content.buffer != nil {
			if added := lineCount(content.buffer); added >= 0 {
				change.added, change.removed = added, 0
			}
		}
		remaining -= content.bytes
		changes = append(changes, change)
	}
	return g.toFiles(workspace, sessionID, Uncommitted, changes)
}

// IgnoredDirectories implements [Git].
func (GitChanges) IgnoredDirectories(ctx context.Context, workspace Workspace) ([]string, error) {
	output, ok, err := runGit(ctx, workspace.RepositoryRoot,
		[]string{"ls-files", "--others", "--ignored", "--exclude-standard", "--directory", "-z", "--", workspace.Pathspec}, false)
	if err != nil {
		return nil, err
	}
	paths, err := nulPaths(output, ok, "git ls-files --ignored")
	if err != nil {
		return nil, err
	}
	var directories []string
	for _, path := range paths {
		absolute, ok := gitPathToAbsolute(workspace.RepositoryRoot, strings.TrimSuffix(path, "/"))
		if ok && isInside(workspace.Cwd, absolute) {
			directories = append(directories, absolute)
		}
	}
	return directories, nil
}

// PathForBlob implements [Git].
func (GitChanges) PathForBlob(workspace Workspace, blob BlobRef) (string, bool) {
	absolute, ok := gitPathToAbsolute(workspace.RepositoryRoot, blob.Path)
	if ok && isInside(workspace.Cwd, absolute) {
		return absolute, true
	}
	return "", false
}

// ReadBlob implements [Git].
func (g GitChanges) ReadBlob(ctx context.Context, workspace Workspace, blob BlobRef) ([]byte, error) {
	if _, ok := g.PathForBlob(workspace, blob); !validOID(blob.OID) || !ok {
		return nil, gitErrorf("invalid git blob reference")
	}
	output, ok, err := runGit(ctx, workspace.RepositoryRoot, []string{"cat-file", "blob", blob.OID}, false)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, gitErrorf("git blob was not found")
	}
	return output, nil
}

func (GitChanges) toFiles(workspace Workspace, sessionID string, kind Kind, changes []parsedChange) ([]ahptypes.ChangesetFile, error) {
	files := []ahptypes.ChangesetFile{}
	for _, change := range changes {
		var oldAbsolute, newAbsolute string
		var haveOld, haveNew bool
		if change.oldPath != "" {
			oldAbsolute, haveOld = gitPathToAbsolute(workspace.RepositoryRoot, change.oldPath)
		}
		if change.newPath != "" {
			newAbsolute, haveNew = gitPathToAbsolute(workspace.RepositoryRoot, change.newPath)
		}
		oldPath, newPath := "", ""
		if haveOld && isInside(workspace.Cwd, oldAbsolute) {
			oldPath = change.oldPath
		}
		if haveNew && isInside(workspace.Cwd, newAbsolute) {
			newPath = change.newPath
		}
		if oldPath == "" && newPath == "" {
			continue
		}

		var before, after *ahptypes.FileEditSide
		switch kind {
		case LatestCommit:
			if (oldPath != "" && change.oldOID == "") || (newPath != "" && change.newOID == "") {
				return nil, gitErrorf("git diff did not provide a committed blob id")
			}
			afterBlob, beforeBlob := "", ""
			if newPath != "" && change.newOID != "" {
				afterBlob = gitBlobURI(sessionID, change.newOID, newPath, newAbsolute)
			}
			if oldPath != "" && change.oldOID != "" {
				beforeBlob = gitBlobURI(sessionID, change.oldOID, oldPath, oldAbsolute)
			}
			if beforeBlob != "" {
				// For an in-place edit both sides share one file identity. Using the committed
				// after-side URI also keeps clients from substituting the mutable working-tree
				// file for this immutable view.
				identity := beforeBlob
				if oldPath == newPath && afterBlob != "" {
					identity = afterBlob
				}
				before = &ahptypes.FileEditSide{Uri: identity, Content: ahptypes.ContentRef{Uri: beforeBlob}}
			}
			if afterBlob != "" {
				after = &ahptypes.FileEditSide{Uri: afterBlob, Content: ahptypes.ContentRef{Uri: afterBlob}}
			}
		case Uncommitted:
			if oldPath != "" && change.oldOID == "" {
				return nil, gitErrorf("git diff did not provide a baseline blob id")
			}
			if oldPath != "" && change.oldOID != "" {
				before = &ahptypes.FileEditSide{
					Uri:     fileURL(oldAbsolute),
					Content: ahptypes.ContentRef{Uri: gitBlobURI(sessionID, change.oldOID, oldPath, oldAbsolute)},
				}
			}
			if newPath != "" {
				uri := fileURL(newAbsolute)
				after = &ahptypes.FileEditSide{Uri: uri, Content: ahptypes.ContentRef{Uri: uri}}
			}
		}
		if before == nil && after == nil {
			continue
		}
		edit := ahptypes.FileEdit{Before: before, After: after, Diff: diffCounts(change)}
		identity := ""
		if newPath != "" {
			identity = newAbsolute
		} else if oldPath != "" {
			identity = oldAbsolute
		}
		if identity != "" {
			files = append(files, ahptypes.ChangesetFile{Id: fileURL(identity), Edit: edit})
		}
	}
	sort.SliceStable(files, func(i, j int) bool { return svc.LocaleLess(files[i].Id, files[j].Id) })
	return files, nil
}
