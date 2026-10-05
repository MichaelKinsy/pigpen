package svc

import (
	"path/filepath"
	"strings"
)

// mimeTypes is the subset of the `mime` npm package's table (mime-db) that matters for source
// trees: the same answers for the extensions people actually open. An extension that is not here
// has no content type (text files fall back to text/plain at the call site).
var mimeTypes = map[string]string{
	"txt": "text/plain", "text": "text/plain", "log": "text/plain", "conf": "text/plain", "ini": "text/plain",
	"md": "text/markdown", "markdown": "text/markdown", "html": "text/html", "htm": "text/html", "css": "text/css",
	"csv": "text/csv", "tsv": "text/tab-separated-values", "xml": "application/xml", "json": "application/json",
	"map": "application/json", "yaml": "text/yaml", "yml": "text/yaml", "toml": "application/toml",
	"js": "text/javascript", "mjs": "text/javascript", "cjs": "text/javascript", "jsx": "text/jsx", "ts": "video/mp2t",
	"tsx": "text/jsx", "sh": "application/x-sh", "py": "text/x-python", "c": "text/x-c", "h": "text/x-c", "cc": "text/x-c",
	"cpp": "text/x-c", "java": "text/x-java-source", "rs": "text/x-rust", "go": "text/x-go", "rb": "text/x-ruby",
	"php": "application/x-httpd-php", "sql": "application/sql", "svg": "image/svg+xml", "png": "image/png",
	"jpg": "image/jpeg", "jpeg": "image/jpeg", "gif": "image/gif", "webp": "image/webp", "bmp": "image/bmp",
	"ico": "image/vnd.microsoft.icon", "tif": "image/tiff", "tiff": "image/tiff", "avif": "image/avif",
	"pdf": "application/pdf", "zip": "application/zip", "gz": "application/gzip", "tar": "application/x-tar",
	"wasm": "application/wasm", "mp3": "audio/mpeg", "wav": "audio/wav", "mp4": "video/mp4", "webm": "video/webm",
	"woff": "font/woff", "woff2": "font/woff2", "ttf": "font/ttf", "otf": "font/otf",
}

// contentTypeOf is the content type for a path's extension; "" when unknown and no text fallback.
func contentTypeOf(path string, textFallback bool) string {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	if t, ok := mimeTypes[ext]; ok {
		return t
	}
	if textFallback {
		return "text/plain"
	}
	return ""
}
