package urls

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

// upstream: packages/coding-agent/src/internal-urls/docs-index.ts
// upstream: packages/coding-agent/src/internal-urls/omp-scope.ts
// upstream: packages/coding-agent/src/internal-urls/omp-protocol.ts

//go:embed prompts/omp.md
var ompPromptDoc string

//go:embed docs_index.generated.txt
var embeddedDocsIndex string

// DocsIndex holds the embedded omp documentation index.
// Listing (`Filenames`) parses only the small first line without inflating the gzip blob;
// `GetBody` lazily inflates the gzipped bodies once on first read.
// upstream: packages/coding-agent/src/internal-urls/docs-index.ts:31-64
type DocsIndex struct {
	filenames []string
	bodyB64   string

	once   sync.Once
	bodies map[string]string
	err    error
}

// Filenames returns the sorted documentation file names relative to `docs/`.
func (d *DocsIndex) Filenames() []string {
	if d == nil {
		return nil
	}
	return append([]string(nil), d.filenames...)
}

// GetBody resolves a doc body by relative path, lazily gunzipping on first call.
func (d *DocsIndex) GetBody(relPath string) (string, bool, error) {
	if d == nil {
		return "", false, nil
	}
	d.once.Do(func() {
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(d.bodyB64))
		if err != nil {
			d.err = fmt.Errorf("decode docs index base64: %w", err)
			return
		}
		gr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			d.err = fmt.Errorf("gunzip docs index: %w", err)
			return
		}
		defer gr.Close()
		inflated, err := io.ReadAll(gr)
		if err != nil {
			d.err = fmt.Errorf("read gunzipped docs index: %w", err)
			return
		}
		var bodies []string
		if err := json.Unmarshal(inflated, &bodies); err != nil {
			d.err = fmt.Errorf("unmarshal docs index bodies: %w", err)
			return
		}
		m := make(map[string]string, len(d.filenames))
		for i, name := range d.filenames {
			if i < len(bodies) {
				m[name] = bodies[i]
			}
		}
		d.bodies = m
	})
	if d.err != nil {
		return "", false, d.err
	}
	body, ok := d.bodies[relPath]
	return body, ok, nil
}

// DecodeDocsIndex decodes a two-line `<filenames JSON>\n<base64 gzip of bodies>` embed
// into a lazily-inflating DocsIndex, or returns nil, nil when there is no newline separator.
// upstream: packages/coding-agent/src/internal-urls/docs-index.ts:46-64
func DecodeDocsIndex(embed string) (*DocsIndex, error) {
	nl := strings.IndexByte(embed, '\n')
	if nl < 0 {
		return nil, nil
	}
	var filenames []string
	if err := json.Unmarshal([]byte(embed[:nl]), &filenames); err != nil {
		return nil, fmt.Errorf("unmarshal docs index filenames: %w", err)
	}
	return &DocsIndex{
		filenames: filenames,
		bodyB64:   embed[nl+1:],
	}, nil
}

var (
	defaultDocsOnce sync.Once
	defaultDocsIdx  *DocsIndex
)

func getDefaultDocsIndex() *DocsIndex {
	defaultDocsOnce.Do(func() {
		idx, err := DecodeDocsIndex(embeddedDocsIndex)
		if err == nil && idx != nil {
			defaultDocsIdx = idx
		}
	})
	return defaultDocsIdx
}

// OmpDocFilename extracts host + path of an `omp://` URL, or "" for the docs root.
// upstream: packages/coding-agent/src/internal-urls/omp-scope.ts:10-14
func OmpDocFilename(u URL) string {
	host := u.Host
	if host == "" {
		return ""
	}
	if u.Path != "" && u.Path != "/" {
		return host + u.Path
	}
	return host
}

// OmpDocRel returns the canonical doc path relative to `docs/` for an `omp://` URL,
// or "" for the docs root (`omp://`, `omp:///`, `omp://docs`, `omp://docs/`).
// Rejects absolute paths and `..` traversal.
// upstream: packages/coding-agent/src/internal-urls/omp-scope.ts:21-31
func OmpDocRel(u URL) (string, error) {
	filename := OmpDocFilename(u)
	if filename == "" {
		return "", nil
	}
	if filepath.IsAbs(filename) || strings.HasPrefix(filename, "/") || strings.HasPrefix(filename, "\\") {
		return "", errors.New("Absolute paths are not allowed in omp:// URLs")
	}
	slashed := strings.ReplaceAll(filename, "\\", "/")
	trailingSlash := strings.HasSuffix(slashed, "/")
	normalized := path.Clean(slashed)
	if normalized == ".." || strings.HasPrefix(normalized, "../") || strings.Contains(normalized, "/../") {
		return "", errors.New("Path traversal (..) is not allowed in omp:// URLs")
	}
	if normalized == "." || normalized == "docs" {
		return "", nil
	}
	// Preserve a trailing slash (matching Node's path.posix.normalize("tools/read.md/") === "tools/read.md/")
	// so bogus paths like `omp://tools/read.md/` fail lookup instead of silently matching `tools/read.md`.
	if trailingSlash && !strings.HasSuffix(normalized, "/") {
		normalized += "/"
	}
	if strings.HasPrefix(normalized, "docs/") {
		return strings.TrimPrefix(normalized, "docs/"), nil
	}
	return normalized, nil
}

// OmpHandler serves omp's embedded harness documentation under `omp://`.
// upstream: packages/coding-agent/src/internal-urls/omp-protocol.ts:50-155
type OmpHandler struct {
	// Index overrides the default embedded corpus when non-nil (for tests).
	Index *DocsIndex
}

func (h OmpHandler) index() *DocsIndex {
	if h.Index != nil {
		return h.Index
	}
	return getDefaultDocsIndex()
}

func (OmpHandler) Scheme() string { return "omp" }

func (OmpHandler) Spec() Spec {
	return Spec{
		Backing:   BackingVirtual,
		Selectors: SelectorsLines,
		Immutable: true,
		Linkable:  true,
	}
}

// PromptDoc always advertises `omp://: harness docs, AVOID unless asked.`.
// upstream: packages/coding-agent/src/internal-urls/omp-protocol.ts:55-57
func (OmpHandler) PromptDoc(_ SchemeHost) string {
	return strings.TrimSpace(ompPromptDoc)
}

func (h OmpHandler) Resolve(_ Context, u URL) (*Resource, error) {
	filename := OmpDocFilename(u)
	docPath, err := OmpDocRel(u)
	if err != nil {
		return nil, err
	}
	if filename == "" || docPath == "" {
		return h.listDocs(u)
	}
	return h.readDoc(docPath, filename, u)
}

func (h OmpHandler) Enumerate(_ Context, u URL) ([]Leaf, error) {
	docPath, err := OmpDocRel(u)
	if err != nil {
		return nil, err
	}
	idx := h.index()
	if docPath == "" {
		if idx == nil || len(idx.filenames) == 0 {
			return nil, errors.New("No documentation files found")
		}
		entries := make([]Leaf, 0, len(idx.filenames))
		for _, rel := range idx.filenames {
			body, ok, err := idx.GetBody(rel)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			entries = append(entries, Leaf{URL: "omp://" + rel, Content: body})
		}
		if len(entries) == 0 {
			return nil, errors.New("No documentation files found")
		}
		return entries, nil
	}
	res, err := h.readDoc(docPath, OmpDocFilename(u), u)
	if err != nil {
		return nil, err
	}
	return []Leaf{{URL: "omp://" + docPath, Content: res.Content}}, nil
}

// Complete returns all embedded documentation filenames (matching upstream omp-protocol.ts:86-88
// and LocalHandler.Complete, letting the caller filter by prefix/substring).
func (h OmpHandler) Complete(_ Context, _ string) ([]Completion, error) {
	idx := h.index()
	if idx == nil {
		return nil, nil
	}
	out := make([]Completion, len(idx.filenames))
	for i, name := range idx.filenames {
		out[i] = Completion{Value: name}
	}
	return out, nil
}

// Locate returns no on-disk file for the embedded corpus (matching omp's bundled/embedded mode).
// upstream: packages/coding-agent/src/internal-urls/omp-protocol.ts:95-115
func (OmpHandler) Locate(_ Context, _ URL, _ LocateOptions) (string, bool, error) {
	return "", false, nil
}

// LocateSync returns no on-disk file for the embedded corpus.
func (OmpHandler) LocateSync(_ Context, _ URL) (string, bool) {
	return "", false
}

func (h OmpHandler) listDocs(u URL) (*Resource, error) {
	idx := h.index()
	if idx == nil || len(idx.filenames) == 0 {
		return nil, errors.New("No documentation files found")
	}
	lines := make([]string, len(idx.filenames))
	for i, f := range idx.filenames {
		lines[i] = fmt.Sprintf("- [%s](omp://%s)", f, f)
	}
	content := fmt.Sprintf("# Documentation\n\n%d files available:\n\n%s\n", len(idx.filenames), strings.Join(lines, "\n"))
	return &Resource{
		URL:         u.Href,
		Content:     content,
		ContentType: "text/markdown",
	}, nil
}

func (h OmpHandler) readDoc(docPath, filename string, u URL) (*Resource, error) {
	idx := h.index()
	if idx != nil {
		content, ok, err := idx.GetBody(docPath)
		if err != nil {
			return nil, err
		}
		if ok {
			return &Resource{
				URL:         u.Href,
				Content:     content,
				ContentType: "text/markdown",
			}, nil
		}
	}
	lookup := strings.TrimSuffix(docPath, ".md")
	var suggestions []string
	if idx != nil {
		for _, f := range idx.filenames {
			base := strings.TrimSuffix(f, ".md")
			if strings.Contains(f, lookup) || strings.Contains(lookup, base) {
				suggestions = append(suggestions, f)
				if len(suggestions) >= 5 {
					break
				}
			}
		}
	}
	suffix := "\nUse omp:// to list available files."
	if len(suggestions) > 0 {
		suffix = "\nDid you mean: " + strings.Join(suggestions, ", ")
	}
	return nil, fmt.Errorf("Documentation file not found: %s%s", filename, suffix)
}
