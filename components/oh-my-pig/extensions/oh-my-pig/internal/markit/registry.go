package markit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Resource limits mirrored from omp's archive limits.
// upstream: packages/utils/src/ar/limits.ts:28-36
const (
	MaxEntries            = 1_000_000
	MaxInMemorySize       = 256 * 1024 * 1024
	MaxMemberSize         = 64 * 1024 * 1024
	MaxTotalInflatedBytes = 256 * 1024 * 1024
)

// ConvertibleExtensions lists the file extensions handled by markit.
// upstream: packages/coding-agent/src/utils/markit.ts:22
var ConvertibleExtensions = []string{".pdf", ".docx", ".pptx", ".xlsx", ".epub"}

// StreamInfo describes a document stream being converted.
// upstream: packages/coding-agent/src/markit/types.ts:3-12
type StreamInfo struct {
	MIMEType  string
	Extension string
	Filename  string
	LocalPath string
	ImageDir  string
}

// Result is the Markdown output of a document conversion.
// upstream: packages/coding-agent/src/markit/types.ts:14-17
type Result struct {
	Markdown string
	Title    string
}

// Converter converts a single document format into Markdown.
// upstream: packages/coding-agent/src/markit/types.ts:28-35
type Converter interface {
	Name() string
	Accepts(info StreamInfo) bool
	Convert(ctx context.Context, input []byte, info StreamInfo) (Result, error)
}

// Registry dispatches document conversions across registered converters.
// upstream: packages/coding-agent/src/markit/registry.ts:18-60
type Registry struct {
	converters []Converter
}

// NewRegistry returns a Registry populated with the five standard omp converters
// in upstream registration order (pdf, docx, pptx, xlsx, epub).
// upstream: packages/coding-agent/src/markit/registry.ts:22-31
func NewRegistry() *Registry {
	return &Registry{
		converters: []Converter{
			&PdfConverter{},
			&DocxConverter{},
			&PptxConverter{},
			&XlsxConverter{},
			&EpubConverter{},
		},
	}
}

var defaultRegistry = NewRegistry()

// NormalizeExtension lowercases ext and ensures a leading dot (or ".bin" if empty).
// upstream: packages/coding-agent/src/utils/markit.ts:53-57
func NormalizeExtension(ext string) string {
	trimmed := strings.ToLower(strings.TrimSpace(ext))
	if trimmed == "" {
		return ".bin"
	}
	if !strings.HasPrefix(trimmed, ".") {
		return "." + trimmed
	}
	return trimmed
}

// IsConvertibleExtension reports whether ext is supported by markit.
// upstream: packages/coding-agent/src/utils/markit.ts:22
func IsConvertibleExtension(ext string) bool {
	norm := NormalizeExtension(ext)
	for _, c := range ConvertibleExtensions {
		if norm == c {
			return true
		}
	}
	return false
}

// IsConvertiblePath reports whether filePath has a markit-convertible extension.
func IsConvertiblePath(filePath string) bool {
	ext := strings.ToLower(filepath.Ext(filePath))
	if ext == "" {
		return false
	}
	return IsConvertibleExtension(ext)
}

// ConvertFile reads filePath (enforcing MaxInMemorySize) and converts it to Markdown.
// upstream: packages/coding-agent/src/markit/registry.ts:33-42
func ConvertFile(ctx context.Context, filePath string) (Result, error) {
	return defaultRegistry.ConvertFile(ctx, filePath, "")
}

// ConvertFileWithImageDir reads filePath and converts it to Markdown, writing any
// extracted images into imageDir when non-empty.
// upstream: packages/coding-agent/src/markit/registry.ts:33-42
func ConvertFileWithImageDir(ctx context.Context, filePath, imageDir string) (Result, error) {
	return defaultRegistry.ConvertFile(ctx, filePath, imageDir)
}

// ConvertBuffer converts an in-memory document buffer with the given extension.
// upstream: packages/coding-agent/src/utils/markit.ts:173-185
func ConvertBuffer(ctx context.Context, data []byte, extension string) (Result, error) {
	norm := NormalizeExtension(extension)
	return defaultRegistry.Convert(ctx, data, StreamInfo{
		Extension: norm,
		Filename:  "input" + norm,
	})
}

// ConvertFile reads filePath and dispatches through r's converters.
// upstream: packages/coding-agent/src/markit/registry.ts:33-42
func (r *Registry) ConvertFile(ctx context.Context, filePath, imageDir string) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	info, err := os.Stat(filePath)
	if err != nil {
		return Result{}, err
	}
	if info.Size() > MaxInMemorySize {
		return Result{}, fmt.Errorf("file is too large to convert in memory (%d bytes > %d bytes limit)", info.Size(), MaxInMemorySize)
	}
	ext := strings.ToLower(filepath.Ext(filePath))
	var data []byte
	// For PDFs, pass LocalPath directly to pdftotext without reading up to 256 MiB into RAM.
	if ext != ".pdf" {
		data, err = os.ReadFile(filePath)
		if err != nil {
			return Result{}, err
		}
	}
	streamInfo := StreamInfo{
		LocalPath: filePath,
		Extension: ext,
		Filename:  filepath.Base(filePath),
		ImageDir:  imageDir,
	}
	return r.Convert(ctx, data, streamInfo)
}

// Convert dispatches input to the first accepting converter that succeeds.
// upstream: packages/coding-agent/src/markit/registry.ts:44-60
func (r *Registry) Convert(ctx context.Context, input []byte, info StreamInfo) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if len(input) > MaxInMemorySize {
		return Result{}, fmt.Errorf("input is too large to convert in memory (%d bytes > %d bytes limit)", len(input), MaxInMemorySize)
	}
	if info.Extension != "" {
		info.Extension = NormalizeExtension(info.Extension)
	}
	type convErr struct {
		name string
		err  error
	}
	var errs []convErr
	for _, conv := range r.converters {
		if !conv.Accepts(info) {
			continue
		}
		res, err := conv.Convert(ctx, input, info)
		if err == nil {
			return res, nil
		}
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		errs = append(errs, convErr{name: conv.Name(), err: err})
	}
	if len(errs) > 0 {
		var b strings.Builder
		b.WriteString("Conversion failed:")
		for _, e := range errs {
			fmt.Fprintf(&b, "\n  %s: %s", e.name, e.err.Error())
		}
		return Result{}, fmt.Errorf("%s", b.String())
	}
	format := info.Extension
	if format == "" {
		format = info.MIMEType
	}
	if format == "" {
		format = "unknown"
	}
	return Result{}, fmt.Errorf("Unsupported format: %s", format)
}

func matchesExtensionOrMIME(info StreamInfo, exts, mimes []string) bool {
	if info.Extension != "" {
		norm := NormalizeExtension(info.Extension)
		for _, e := range exts {
			if norm == e {
				return true
			}
		}
	}
	if info.MIMEType != "" {
		lower := strings.ToLower(strings.TrimSpace(info.MIMEType))
		for _, m := range mimes {
			if strings.HasPrefix(lower, m) {
				return true
			}
		}
	}
	return false
}
