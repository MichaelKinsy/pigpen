package memory

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

// upstream: packages/coding-agent/src/internal-urls/memory-protocol.ts:1-491
// upstream: packages/coding-agent/src/prompts/internal-urls/memory.md

//go:embed prompts/memory-url.md
var memoryURLPromptDoc string

const (
	defaultMemoryFile = "memory_summary.md"
	memoryNamespace   = "root"

	// upstream: packages/coding-agent/src/internal-urls/memory-protocol.ts:39-40
	hindsightUnaddressableErr = "Hindsight memories are not addressable via memory://. Recall results are final — use `recall` to search or `reflect` to synthesize. `read memory://<id>` is only available with memory.backend=mnemopi."
)

// MemoryHandler implements `urls.Handler`, `urls.Locator`, `urls.PromptDocer`,
// and `urls.Completer` for `memory://` internal URLs.
// upstream: packages/coding-agent/src/internal-urls/memory-protocol.ts:330-491
type MemoryHandler struct{}

func (MemoryHandler) Scheme() string { return "memory" }

func (MemoryHandler) Spec() urls.Spec {
	return urls.Spec{
		Backing:   urls.BackingFile,
		Selectors: urls.SelectorsLines,
		Immutable: true,
		Linkable:  true,
	}
}

// PromptDoc advertises `memory://root` ONLY when `host.MemoryBackend == "local"`.
// Returns "" for `off` (default), `mnemopi`, `hindsight`, etc. so default
// sessions have zero system-prompt drift.
// upstream: packages/coding-agent/src/internal-urls/memory-protocol.ts:340-342
func (MemoryHandler) PromptDoc(host urls.SchemeHost) string {
	if host.MemoryBackend == BackendLocal {
		return strings.TrimSpace(memoryURLPromptDoc)
	}
	return ""
}

func resolveMemoryCwd(c urls.Context) string {
	if strings.TrimSpace(c.Cwd) != "" {
		return c.Cwd
	}
	return ResolveCwd(c.SDK)
}

func validateMemoryRelativePath(rel string) error {
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, "\\") {
		return errors.New("Absolute paths are not allowed in memory:// URLs")
	}
	if strings.ContainsRune(rel, 0) {
		return errors.New("NUL bytes are not allowed in memory:// URLs")
	}
	parts := strings.FieldsFunc(rel, func(r rune) bool { return r == '/' || r == '\\' })
	for _, p := range parts {
		if p == ".." {
			return errors.New("Path traversal (..) is not allowed in memory:// URLs")
		}
	}
	cleaned := filepath.ToSlash(filepath.Clean(rel))
	if strings.HasPrefix(cleaned, "..") || strings.Contains(cleaned, "/../") || strings.HasSuffix(cleaned, "/..") {
		return errors.New("Path traversal (..) is not allowed in memory:// URLs")
	}
	return nil
}

func ensurePathWithinRoot(targetPath, rootPath, rawURL string) error {
	cleanTarget := filepath.Clean(targetPath)
	cleanRoot := filepath.Clean(rootPath)
	if cleanTarget == cleanRoot {
		return nil
	}
	prefix := cleanRoot
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	if !strings.HasPrefix(cleanTarget, prefix) {
		return &urls.UrlContainmentError{Msg: fmt.Sprintf("memory:// URL escapes memory root: %s", rawURL)}
	}
	return nil
}

func isBareMemoryURL(u urls.URL) bool {
	return u.Path == "" || u.Path == "/"
}

func resolveMemoryURLToPath(u urls.URL, memoryRoot string, directory bool) (string, error) {
	if u.Host == "" {
		return "", errors.New("memory:// URL requires a namespace: memory://root or memory://<memory-id>")
	}
	if u.Host != memoryNamespace {
		return "", fmt.Errorf("Unknown memory namespace: %s. Supported: %s", u.Host, memoryNamespace)
	}
	if isBareMemoryURL(u) {
		if directory {
			return filepath.Clean(memoryRoot), nil
		}
		return filepath.Join(memoryRoot, defaultMemoryFile), nil
	}
	rawPath := strings.TrimPrefix(u.Path, "/")
	rel, err := url.PathUnescape(rawPath)
	if err != nil {
		return "", fmt.Errorf("Invalid URL encoding in memory:// path: %s", u.Href)
	}
	if err := validateMemoryRelativePath(rel); err != nil {
		return "", err
	}
	target := filepath.Join(memoryRoot, filepath.FromSlash(rel))
	if err := ensurePathWithinRoot(target, memoryRoot, u.Href); err != nil {
		return "", err
	}
	return target, nil
}

func locateContainedInMemoryRoot(u urls.URL, memoryRoot string, directory bool) (realTarget string, found bool, err error) {
	realRoot, err := filepath.EvalSymlinks(memoryRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	target, err := resolveMemoryURLToPath(u, realRoot, directory)
	if err != nil {
		return "", false, err
	}
	if err := ensurePathWithinRoot(target, realRoot, u.Href); err != nil {
		return "", false, err
	}
	if filepath.Clean(target) != filepath.Clean(realRoot) {
		parent := filepath.Dir(target)
		if realParent, perr := filepath.EvalSymlinks(parent); perr == nil {
			if err := ensurePathWithinRoot(realParent, realRoot, u.Href); err != nil {
				return "", false, err
			}
		} else if !errors.Is(perr, os.ErrNotExist) {
			return "", false, perr
		}
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	if err := ensurePathWithinRoot(resolved, realRoot, u.Href); err != nil {
		return "", false, err
	}
	return resolved, true, nil
}

// Locate returns the backing local file for `memory://root[/<subpath>]` when
// `memory.backend == "local"`. Returns ("", false, nil) for `off`, `mnemopi`,
// `hindsight`, or non-root namespaces.
// upstream: packages/coding-agent/src/internal-urls/memory-protocol.ts:348-372
func (MemoryHandler) Locate(c urls.Context, u urls.URL, opts urls.LocateOptions) (string, bool, error) {
	backend := BackendFor(c.SDK)
	if backend == BackendOff || backend != BackendLocal {
		return "", false, nil
	}
	if u.Host == "" {
		return "", false, errors.New("memory:// URL requires a namespace: memory://root or memory://<memory-id>")
	}
	if u.Host != memoryNamespace {
		return "", false, nil
	}
	root := MemoryRoot(ResolveAgentDir(c.SDK), resolveMemoryCwd(c))
	return locateContainedInMemoryRoot(u, root, opts.Directory)
}

func contentTypeForMemoryFile(filePath string) string {
	switch strings.ToLower(filepath.Ext(filePath)) {
	case ".md", ".markdown", ".mdx", ".mkd", ".mkdn":
		return "text/markdown"
	case ".json":
		return "application/json"
	default:
		return "text/plain"
	}
}

func fileBackedRootUnavailableError(backend string) error {
	if backend == "" || backend == BackendLocal {
		return errors.New("Memory artifacts are not available for this project yet. Run a session with memories enabled first.")
	}
	searchHint := ""
	switch backend {
	case BackendMnemopi:
		searchHint = " Use `recall`/`reflect` to search Mnemopi memories, or `read memory://<memory-id>` for a full row."
	case BackendHindsight:
		searchHint = " Use `recall`/`reflect` to search Hindsight memories."
	}
	return fmt.Errorf("File-backed memory artifacts only exist with memory.backend=local (active backend: %s).%s", backend, searchHint)
}

func renderMnemopiMemoryResource(u urls.URL, hit *MnemopiScopedMemoryHit) *urls.Resource {
	row := hit.Row
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "id: %s\n", row.ID)
	fmt.Fprintf(&b, "bank: %s\n", hit.Bank)
	fmt.Fprintf(&b, "store: %s\n", hit.Store)
	if row.MemoryType != "" {
		fmt.Fprintf(&b, "memory_type: %s\n", row.MemoryType)
	}
	if row.Source != "" {
		fmt.Fprintf(&b, "source: %s\n", row.Source)
	}
	if row.Timestamp != "" {
		fmt.Fprintf(&b, "timestamp: %s\n", row.Timestamp)
	}
	if row.CreatedAt != "" {
		fmt.Fprintf(&b, "created_at: %s\n", row.CreatedAt)
	}
	fmt.Fprintf(&b, "importance: %g\n", row.Importance)
	if row.Veracity != "" {
		fmt.Fprintf(&b, "veracity: %s\n", row.Veracity)
	}
	if row.SessionID != "" {
		fmt.Fprintf(&b, "session_id: %s\n", row.SessionID)
	}
	if len(row.Metadata) > 0 {
		if metaJSON, err := json.Marshal(row.Metadata); err == nil {
			fmt.Fprintf(&b, "metadata: %s\n", string(metaJSON))
		}
	}
	b.WriteString("---\n\n")
	b.WriteString(row.Content)
	return &urls.Resource{
		URL:         u.Href,
		Content:     b.String(),
		ContentType: "text/markdown",
	}
}

// Resolve serves `memory://root[/<subpath>]` for `local` and `memory://<id>` for `mnemopi`.
// upstream: packages/coding-agent/src/internal-urls/memory-protocol.ts:389-469
func (MemoryHandler) Resolve(c urls.Context, u urls.URL) (*urls.Resource, error) {
	backend := BackendFor(c.SDK)
	if backend == BackendOff {
		return nil, errors.New("memory:// is unavailable because memory.backend is \"off\". Enable memory.backend (local or mnemopi) to use memory:// URLs.")
	}
	if u.Host == "" {
		return nil, errors.New("memory:// URL requires a namespace: memory://root or memory://<memory-id>")
	}

	if u.Host != memoryNamespace {
		switch backend {
		case BackendHindsight:
			return nil, errors.New(hindsightUnaddressableErr)
		case BackendMnemopi:
			cfg := LoadMnemopiConfig(c.SDK)
			hit, err := MnemopiGetScopedMemory(cfg, u.Host)
			if err != nil {
				return nil, err
			}
			if hit != nil {
				return renderMnemopiMemoryResource(u, hit), nil
			}
			return nil, fmt.Errorf("Mnemopi memory %s not found in the calling session's scoped bank. Use `recall` to list available ids.", u.Host)
		default:
			return nil, fmt.Errorf("Unknown memory namespace: %s. Supported: %s (file-backed memory summary), or a mnemopi memory id when memory.backend=mnemopi is active.", u.Host, memoryNamespace)
		}
	}

	if backend != BackendLocal {
		return nil, fileBackedRootUnavailableError(backend)
	}

	root := MemoryRoot(ResolveAgentDir(c.SDK), resolveMemoryCwd(c))
	if _, err := os.Stat(root); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fileBackedRootUnavailableError(backend)
		}
		return nil, err
	}
	realTarget, found, err := locateContainedInMemoryRoot(u, root, false)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("Memory file not found: %s", u.Href)
	}
	st, err := os.Stat(realTarget)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		entries, err := os.ReadDir(realTarget)
		if err != nil {
			return nil, err
		}
		type item struct {
			name  string
			isDir bool
		}
		list := make([]item, 0, len(entries))
		for _, e := range entries {
			list = append(list, item{name: e.Name(), isDir: e.IsDir()})
		}
		sort.Slice(list, func(i, j int) bool {
			if list[i].isDir != list[j].isDir {
				return list[i].isDir
			}
			return list[i].name < list[j].name
		})
		var lines []string
		for _, it := range list {
			if it.isDir {
				lines = append(lines, it.name+"/")
			} else {
				lines = append(lines, it.name)
			}
		}
		content := "(empty directory)"
		if len(lines) > 0 {
			content = strings.Join(lines, "\n")
		}
		return &urls.Resource{
			URL:         u.Href,
			Content:     content,
			ContentType: "text/plain",
			SourcePath:  realTarget,
			IsDirectory: true,
		}, nil
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("memory:// URL must resolve to a file or directory: %s", u.Href)
	}
	raw, err := os.ReadFile(realTarget)
	if err != nil {
		return nil, err
	}
	return &urls.Resource{
		URL:         u.Href,
		Content:     string(raw),
		ContentType: contentTypeForMemoryFile(realTarget),
		SourcePath:  realTarget,
	}, nil
}

// Complete provides autocomplete candidates for `memory://`.
// upstream: packages/coding-agent/src/internal-urls/memory-protocol.ts:471-490
func (MemoryHandler) Complete(c urls.Context, _ string) ([]urls.Completion, error) {
	switch BackendFor(c.SDK) {
	case BackendLocal:
		return []urls.Completion{{Value: memoryNamespace, Description: "Project memory summary"}}, nil
	case BackendMnemopi:
		return []urls.Completion{{Value: "<memory-id>", Description: "Full mnemopi memory by id (from recall)"}}, nil
	default:
		return nil, nil
	}
}
