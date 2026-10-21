package svc

import (
	"sort"
	"strings"
)

// contentTypeOf is the content type for a path; "" when unknown and no text fallback
// (resource-service.ts: mime.getType(path) ?? (textFallback ? "text/plain" : undefined)).
func contentTypeOf(path string, textFallback bool) string {
	if t, ok := MimeType(path); ok {
		return t
	}
	if textFallback {
		return "text/plain"
	}
	return ""
}

// MimeType is the `mime` package's getType(path) (mime@4.1.1, table in mimetable.go): the content
// type of the last extension of the path's file name, lower-cased. A file name without a dot has
// no type when the path has a directory part; a bare name is looked up as if it were an extension
// ("Makefile" asks for "makefile").
func MimeType(path string) (string, bool) {
	last := path
	if i := strings.LastIndexAny(last, `/\`); i >= 0 {
		last = last[i+1:]
	}
	last = strings.ToLower(last)
	ext := last
	if i := strings.LastIndexByte(last, '.'); i >= 0 {
		ext = last[i+1:]
	}
	hasPath := len(last) < len(path)
	hasDot := len(ext) < len(last)-1
	if !hasDot && hasPath {
		return "", false
	}
	i := sort.Search(len(mimeTable), func(i int) bool { return mimeTable[i].ext >= ext })
	if i < len(mimeTable) && mimeTable[i].ext == ext {
		return mimeTable[i].typ, true
	}
	return "", false
}
