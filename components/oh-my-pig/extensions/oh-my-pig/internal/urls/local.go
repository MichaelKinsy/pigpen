package urls

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// upstream: packages/coding-agent/src/internal-urls/local-root.ts, local-protocol.ts

//go:embed prompts/local.md
var localPromptDoc string

const (
	windowsLocalRootMaxChars = 180
	localTextSniffBytes      = 8 * 1024
	localTextResourceMaxSize = 1024 * 1024
	localWriteNote           = "Use write path local://<file> to persist large intermediate artifacts across turns."
)

var (
	unsafeSessionIDRE = regexp.MustCompile(`[^a-zA-Z0-9_.-]`)
	binaryFileExts    = map[string]bool{
		".7z": true, ".avi": true, ".bmp": true, ".bz2": true, ".db": true,
		".doc": true, ".docx": true, ".gif": true, ".gz": true, ".ico": true,
		".jpeg": true, ".jpg": true, ".m4v": true, ".mkv": true, ".mov": true,
		".mp4": true, ".pdf": true, ".png": true, ".ppt": true, ".pptx": true,
		".rar": true, ".sqlite": true, ".tgz": true, ".webm": true, ".webp": true,
		".wmv": true, ".xls": true, ".xlsx": true, ".xz": true, ".zip": true,
	}

	localRootMu       sync.RWMutex
	localRootOverride func(sdk.Context) (string, error)
	localRootProvider func(sdk.Context) (string, bool)
)

// SetLocalRootProvider installs a production provider that resolves a session's
// local:// root (for example mapping a subagent's child session to its parent's
// local:// root so parent/child handoffs share the same directory).
// Pass nil to clear the provider.
// upstream: task/executor.ts:5309, local-protocol.ts:482-496.
func SetLocalRootProvider(fn func(ctx sdk.Context) (root string, ok bool)) {
	localRootMu.Lock()
	localRootProvider = fn
	localRootMu.Unlock()
}

// SetLocalRootOverrideForTests overrides LocalRoot resolution for tests.
// Pass nil to clear the override.
func SetLocalRootOverrideForTests(fn func(sdk.Context) (string, error)) {
	localRootMu.Lock()
	localRootOverride = fn
	localRootMu.Unlock()
}

func safeSessionID(raw string) string {
	if raw == "" {
		return "session"
	}
	safe := unsafeSessionIDRE.ReplaceAllString(raw, "_")
	if safe == "" || safe == "." || safe == ".." {
		return "session"
	}
	return safe
}

// resolveLocalRootPath computes the local:// root given an optional artifacts directory,
// session ID, and target GOOS. On Windows, artifact paths >= 180 chars fall back to a
// short temp path keyed by session ID.
// upstream: local-root.ts resolveLocalRoot.
func resolveLocalRootPath(artifactsDir, sessionID, goos string) string {
	if artifactsDir != "" {
		candidate, err := filepath.Abs(filepath.Join(artifactsDir, "local"))
		if err != nil {
			candidate = filepath.Join(artifactsDir, "local")
		}
		if goos == "windows" && len(candidate) >= windowsLocalRootMaxChars {
			return filepath.Join(os.TempDir(), "omp-local", safeSessionID(sessionID))
		}
		return candidate
	}
	return filepath.Join(os.TempDir(), "omp-local", safeSessionID(sessionID))
}

// LocalRoot returns the session's `local://` directory path (used by the shell
// lane for spills and by LocalHandler). It does not create the directory.
// upstream: local-root.ts resolveLocalRoot.
func LocalRoot(ctx sdk.Context) (string, error) {
	localRootMu.RLock()
	override := localRootOverride
	provider := localRootProvider
	localRootMu.RUnlock()
	if provider != nil {
		if root, ok := provider(ctx); ok && root != "" {
			if !filepath.IsAbs(root) {
				return "", fmt.Errorf("local:// root provider returned relative path: %s", root)
			}
			return root, nil
		}
	}
	if override != nil {
		return override(ctx)
	}

	var artifactsDir, sessionID string
	if ctx != (sdk.Context{}) {
		if id, err := ctx.GetSessionID(); err == nil {
			sessionID = id
		}
		if sf, err := ctx.GetSessionFile(); err == nil && sf != nil && strings.HasSuffix(*sf, ".jsonl") {
			// upstream: session/session-manager.ts:184-187 artifactsDirectoryFor:
			// only `.jsonl` session files yield a sibling artifacts dir.
			artifactsDir = strings.TrimSuffix(*sf, ".jsonl")
		}
		if artifactsDir == "" && sessionID == "" {
			return "", errors.New("No session - local:// unavailable")
		}
	}
	return resolveLocalRootPath(artifactsDir, sessionID, runtime.GOOS), nil
}

// LocalFileEntry describes a file under the session's local:// root.
// upstream: local-protocol.ts LocalFileEntry.
type LocalFileEntry struct {
	Name    string
	URL     string
	Path    string
	Size    int64
	ModTime int64 // unix milliseconds
}

// ListLocalFiles lists every regular file under the session's local:// root,
// newest first (ties by name). Missing root returns an empty slice.
// upstream: local-protocol.ts listLocalFiles.
func ListLocalFiles(ctx sdk.Context) ([]LocalFileEntry, error) {
	root, err := LocalRoot(ctx)
	if err != nil {
		return nil, err
	}
	names, err := listFilesRecursively(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	entries := make([]LocalFileEntry, 0, len(names))
	for _, name := range names {
		fp := filepath.Join(root, filepath.FromSlash(name))
		st, err := os.Stat(fp)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		entries = append(entries, LocalFileEntry{
			Name:    name,
			URL:     "local://" + name,
			Path:    fp,
			Size:    st.Size(),
			ModTime: st.ModTime().UnixMilli(),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].ModTime != entries[j].ModTime {
			return entries[i].ModTime > entries[j].ModTime
		}
		return compareLocale(entries[i].Name, entries[j].Name) < 0
	})
	return entries, nil
}

// CopyLocalArtifacts recursively copies every regular file from sourceRoot to destinationRoot.
// No-op when roots match or sourceRoot does not exist.
// upstream: local-protocol.ts copyLocalArtifacts.
func CopyLocalArtifacts(sourceRoot, destinationRoot string) error {
	if sourceRoot == destinationRoot {
		return nil
	}
	st, err := os.Lstat(sourceRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if !st.IsDir() {
		return nil
	}
	if err := os.MkdirAll(destinationRoot, 0o755); err != nil {
		return err
	}
	return copyLocalEntries(sourceRoot, destinationRoot)
}

func copyLocalEntries(srcDir, dstDir string) error {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		srcPath := filepath.Join(srcDir, e.Name())
		dstPath := filepath.Join(dstDir, e.Name())
		if e.IsDir() {
			if err := os.MkdirAll(dstPath, 0o755); err != nil {
				return err
			}
			if err := copyLocalEntries(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		if e.Type().IsRegular() {
			if err := copyRegularFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func copyRegularFile(srcPath, dstPath string) error {
	in, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

// extractLocalRelativePath extracts and validates the root-relative path from a local:// URL.
// upstream: local-protocol.ts extractRelativePath.
func extractLocalRelativePath(u URL) (string, error) {
	host := extractRawAuthority(u)
	pathname := u.Path

	var combined string
	switch {
	case host != "" && pathname != "" && pathname != "/":
		combined = host + pathname
	case host != "":
		combined = host
	case pathname != "" && pathname != "/":
		combined = strings.TrimPrefix(pathname, "/")
	default:
		return "", nil
	}

	decoded, err := url.PathUnescape(strings.ReplaceAll(combined, "\\", "/"))
	if err != nil {
		return "", fmt.Errorf("Invalid URL encoding in local:// path: %s", u.Href)
	}
	if err := validateRelativePath(decoded, "local"); err != nil {
		return "", err
	}
	return decoded, nil
}

// ResolveLocalURLToPath resolves a local:// URL to an on-disk path under the session's local root.
// upstream: local-protocol.ts resolveLocalUrlToPath.
func ResolveLocalURLToPath(ctx sdk.Context, input string) (string, error) {
	u, err := Parse(input)
	if err != nil {
		return "", err
	}
	root, err := LocalRoot(ctx)
	if err != nil {
		return "", err
	}
	return resolveLocalURLWithRoot(u, root)
}

func resolveLocalURLWithRoot(u URL, localRoot string) (string, error) {
	absRoot, err := filepath.Abs(localRoot)
	if err != nil {
		absRoot = localRoot
	}
	rel, err := extractLocalRelativePath(u)
	if err != nil {
		return "", err
	}
	if rel == "" {
		return absRoot, nil
	}
	resolved := filepath.Join(absRoot, filepath.FromSlash(rel))
	if err := ensureWithinRoot(resolved, absRoot, "local", u.Href); err != nil {
		return "", err
	}
	return resolved, nil
}

// LocalHandler serves `local://` session scratch files and spills.
// upstream: local-protocol.ts LocalProtocolHandler.
type LocalHandler struct{}

func (LocalHandler) Scheme() string { return "local" }

func (LocalHandler) Spec() Spec {
	return Spec{
		Backing:          BackingFile,
		Selectors:        SelectorsLines,
		Immutable:        false,
		UnspilledPages:   true,
		PathAuthority:    true,
		Linkable:         true,
		ImageQuestion:    true,
		SingleSlashAlias: true,
		Write: &WritePolicy{
			Via:     "file",
			Payload: "text",
			Scope:   "sandbox",
		},
	}
}

func (LocalHandler) PromptDoc(_ SchemeHost) string {
	return strings.TrimSpace(localPromptDoc)
}

func (LocalHandler) Resolve(c Context, u URL) (*Resource, error) {
	localRoot, err := LocalRoot(c.SDK)
	if err != nil {
		return nil, err
	}
	absRoot, err := filepath.Abs(localRoot)
	if err != nil {
		absRoot = localRoot
	}
	if err := os.MkdirAll(absRoot, 0o755); err != nil {
		return nil, fmt.Errorf("Unable to initialize local:// root: %w", err)
	}
	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return nil, fmt.Errorf("Unable to initialize local:// root: %w", err)
	}

	rel, err := extractLocalRelativePath(u)
	if err != nil {
		return nil, err
	}
	targetPath := realRoot
	if rel != "" {
		targetPath = filepath.Join(realRoot, filepath.FromSlash(rel))
	}
	if err := ensureWithinRoot(targetPath, realRoot, "local", u.Href); err != nil {
		return nil, err
	}
	if filepath.Clean(targetPath) == filepath.Clean(realRoot) {
		return buildLocalListing(u, realRoot)
	}

	realTarget, found, err := containedRealPath(targetPath, realRoot, "local", u.Href)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("Local file not found: %s", u.Href)
	}
	st, err := os.Stat(realTarget)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return buildDirectoryResource(u.Href, realTarget, []string{localWriteNote})
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("local:// URL must resolve to a file or directory: %s", u.Href)
	}
	return buildLocalFileResource(u, realTarget, st.Size())
}

func (LocalHandler) Locate(c Context, u URL, opts LocateOptions) (string, bool, error) {
	localRoot, err := LocalRoot(c.SDK)
	if err != nil {
		return "", false, err
	}
	absRoot, err := filepath.Abs(localRoot)
	if err != nil {
		absRoot = localRoot
	}
	rel, err := extractLocalRelativePath(u)
	if err != nil {
		return "", false, err
	}
	targetPath := absRoot
	if rel != "" {
		targetPath = filepath.Join(absRoot, filepath.FromSlash(rel))
	}
	if err := ensureWithinRoot(targetPath, absRoot, "local", u.Href); err != nil {
		return "", false, err
	}

	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return "", false, err
		}
		if !opts.Create {
			return "", false, nil
		}
		if err := ensureCreatableWithinRoot(targetPath, absRoot, "local", u.Href); err != nil {
			return "", false, err
		}
		return targetPath, true, nil
	}

	underRealRoot := realRoot
	if rel != "" {
		underRealRoot = filepath.Join(realRoot, filepath.FromSlash(rel))
	}
	if opts.Create {
		if err := ensureCreatableWithinRoot(underRealRoot, realRoot, "local", u.Href); err != nil {
			return "", false, err
		}
		return targetPath, true, nil
	}
	realTarget, found, err := containedRealPath(underRealRoot, realRoot, "local", u.Href)
	if err != nil {
		return "", false, err
	}
	return realTarget, found, nil
}

func (LocalHandler) LocateSync(c Context, u URL) (string, bool) {
	localRoot, err := LocalRoot(c.SDK)
	if err != nil {
		return "", false
	}
	p, err := resolveLocalURLWithRoot(u, localRoot)
	if err != nil {
		return "", false
	}
	return p, true
}

func (LocalHandler) Complete(c Context, _ string) ([]Completion, error) {
	localRoot, err := LocalRoot(c.SDK)
	if err != nil {
		return nil, nil
	}
	files, err := listFilesRecursively(localRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]Completion, len(files))
	for i, f := range files {
		out[i] = Completion{Value: f}
	}
	return out, nil
}

func listFilesRecursively(rootPath string) ([]string, error) {
	var files []string
	var walk func(rel string) error
	walk = func(rel string) error {
		dir := rootPath
		if rel != "" {
			dir = filepath.Join(rootPath, rel)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			entryRel := e.Name()
			if rel != "" {
				entryRel = filepath.Join(rel, e.Name())
			}
			if e.IsDir() {
				if err := walk(entryRel); err != nil {
					return err
				}
			} else if e.Type().IsRegular() {
				files = append(files, filepath.ToSlash(entryRel))
			}
		}
		return nil
	}
	if err := walk(""); err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return compareLocale(files[i], files[j]) < 0 })
	return files, nil
}

func buildLocalListing(u URL, localRoot string) (*Resource, error) {
	files, err := listFilesRecursively(localRoot)
	if err != nil {
		return nil, err
	}
	listing := "(empty)"
	if len(files) > 0 {
		lines := make([]string, len(files))
		for i, f := range files {
			lines[i] = fmt.Sprintf("- [%s](local://%s)", f, f)
		}
		listing = strings.Join(lines, "\n")
	}
	plural := "s"
	if len(files) == 1 {
		plural = ""
	}
	content := fmt.Sprintf(
		"# Local\n\nSession-scoped scratch space for large intermediate data, subagent handoffs, and reusable planning artifacts.\n\nRoot: %s\n\n%d file%s available:\n\n%s\n",
		localRoot, len(files), plural, listing,
	)
	return &Resource{
		URL:         u.Href,
		Content:     content,
		ContentType: "text/markdown",
		SourcePath:  localRoot,
	}, nil
}

func buildLocalFileResource(u URL, filePath string, size int64) (*Resource, error) {
	ext := strings.ToLower(filepath.Ext(filePath))
	if binaryFileExts[ext] {
		return buildNonTextLocalResource(u, filePath, size, "extension is a known binary/container type"), nil
	}
	sniffLen := size
	if sniffLen > localTextSniffBytes {
		sniffLen = localTextSniffBytes
	}
	sniff, err := readFilePrefix(filePath, int(sniffLen))
	if err != nil {
		return nil, err
	}
	if !isUTF8Text(sniff) {
		return buildNonTextLocalResource(u, filePath, size, "content is not valid UTF-8 text"), nil
	}
	if size > localTextResourceMaxSize {
		content := fmt.Sprintf(
			"[Cannot materialize local:// file '%s' as an internal text resource (%s exceeds %s). Use the read tool's filesystem path handling or a line selector so content is streamed with file-size safeguards.]",
			u.Href, formatByteSize(size), formatByteSize(localTextResourceMaxSize),
		)
		return &Resource{
			URL:         u.Href,
			Content:     content,
			ContentType: "text/plain",
			SourcePath:  filePath,
			Notes:       []string{localWriteNote},
		}, nil
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	return &Resource{
		URL:         u.Href,
		Content:     string(data),
		ContentType: contentTypeForPath(filePath),
		SourcePath:  filePath,
		Notes:       []string{localWriteNote},
	}, nil
}

func buildNonTextLocalResource(u URL, filePath string, size int64, reason string) *Resource {
	content := fmt.Sprintf(
		"[Cannot read binary local:// file '%s' (%s): %s. This resource is not text. Use a metadata/key-frame/video-specific workflow instead.]",
		u.Href, formatByteSize(size), reason,
	)
	return &Resource{
		URL:         u.Href,
		Content:     content,
		ContentType: "text/plain",
		SourcePath:  filePath,
		Notes:       []string{localWriteNote},
	}
}

func readFilePrefix(filePath string, maxBytes int) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, nil
	}
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, maxBytes)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	return buf[:n], nil
}

func isUTF8Text(b []byte) bool {
	if bytes.IndexByte(b, 0) != -1 {
		return false
	}
	// If the buffer was truncated at localTextSniffBytes, a multi-byte UTF-8 rune
	// at the very end may be incomplete; trim up to 3 trailing continuation bytes.
	if len(b) == localTextSniffBytes {
		for trim := 0; trim < 4 && len(b) > 0; trim++ {
			if utf8.Valid(b) {
				return true
			}
			b = b[:len(b)-1]
		}
		return false
	}
	return utf8.Valid(b)
}
