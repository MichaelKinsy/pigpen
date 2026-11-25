package tern

import (
	"os"
	"path/filepath"
	"strings"
)

// Media extensions mirrored from omp's `packages/coding-agent/src/tools/tern-media.ts`
// (which mirrors Tern's `crates/editor/src/kind.rs`: IMAGE, SVG, MODEL, MODEL_GZ, PLATFORM).
// Sidecars (`.mtl`, textures) are absent: Tern's model viewer loads them relative
// to the model's path.
// upstream: packages/coding-agent/src/tools/tern-media.ts:8
var ternMediaExtensions = map[string]bool{
	// Raster images.
	"png":  true,
	"apng": true,
	"jpg":  true,
	"jpeg": true,
	"jpe":  true,
	"jfif": true,
	"gif":  true,
	"webp": true,
	// Vector.
	"svg": true,
	// 3D models.
	"obj":   true,
	"ply":   true,
	"wrl":   true,
	"vrml":  true,
	"wrz":   true,
	"x3dv":  true,
	"x3dvz": true,
	"stl":   true,
	"gltf":  true,
	"glb":   true,
	"fbx":   true,
	"usd":   true,
	"usda":  true,
	"usdc":  true,
	"usdz":  true,
	"3ds":   true,
	// Platform reader/player.
	"pdf":  true,
	"mp4":  true,
	"m4v":  true,
	"mov":  true,
	"webm": true,
	"mp3":  true,
	"m4a":  true,
	"wav":  true,
	"aac":  true,
	"flac": true,
	"ogg":  true,
	"oga":  true,
	"opus": true,
}

// 3D models Tern also opens gzip-compressed (`<ext>.gz`).
// upstream: packages/coding-agent/src/tools/tern-media.ts:54
var ternModelGzExtensions = map[string]bool{
	"obj":  true,
	"ply":  true,
	"wrl":  true,
	"vrml": true,
	"x3dv": true,
	"stl":  true,
	"gltf": true,
	"glb":  true,
	"fbx":  true,
	"usd":  true,
	"usda": true,
	"usdc": true,
	"3ds":  true,
}

// extensionOf matches Rust's `Path::extension` and omp's `extensionOf`:
// substring after the last dot, none for dotfiles like `.png` (dot == 0).
// upstream: packages/coding-agent/src/tools/tern-media.ts:71
func extensionOf(name string) (string, bool) {
	dot := strings.LastIndexByte(name, '.')
	if dot <= 0 {
		return "", false
	}
	return name[dot+1:], true
}

// ShowsMedia reports whether Tern's media viewer shows the file at filePath,
// judged by its extension as Tern and omp's `ternShowsMedia` do.
// upstream: packages/coding-agent/src/tools/tern-media.ts:77
func ShowsMedia(filePath string) bool {
	sep := max(strings.LastIndexByte(filePath, '/'), strings.LastIndexByte(filePath, '\\'))
	name := strings.ToLower(filePath[sep+1:])
	ext, ok := extensionOf(name)
	if !ok {
		return false
	}
	if ext != "gz" {
		return ternMediaExtensions[ext]
	}
	dot := strings.LastIndexByte(name, '.')
	inner, ok := extensionOf(name[:dot])
	return ok && ternModelGzExtensions[inner]
}

// IsWholeFileReadSelector reports whether a `read` selector string reads the
// whole file (empty, `:raw`, or `:img`), as omp requires before setting
// `details.mediaPath`. Line ranges (`:2-3`, `:10`) and video frame selectors
// return false.
// upstream: packages/coding-agent/src/tools/read.ts:2611
// upstream: packages/coding-agent/src/tools/read-selector.ts:115-117
func IsWholeFileReadSelector(selector string) bool {
	s := strings.TrimSpace(strings.ToLower(selector))
	if s == "" {
		return true
	}
	s = strings.TrimPrefix(s, ":")
	return s == "raw" || s == "img"
}

// MediaPathForRead returns absolutePath when a read of absolutePath with
// selector should attach `details.mediaPath` for Tern's native `media` node,
// or "" otherwise.
// upstream: packages/coding-agent/src/tools/read.ts:2611
func MediaPathForRead(absolutePath, selector string) string {
	if absolutePath == "" || !IsWholeFileReadSelector(selector) || !ShowsMedia(absolutePath) {
		return ""
	}
	return absolutePath
}

// AttachMediaPath sets `details["mediaPath"] = mediaPath` on a copy of details
// (preserving existing keys) when mediaPath is non-empty, returning the updated
// details map. Returns details unchanged when mediaPath is empty.
func AttachMediaPath(details map[string]any, mediaPath string) map[string]any {
	if mediaPath == "" {
		return details
	}
	out := make(map[string]any, len(details)+1)
	for k, v := range details {
		out[k] = v
	}
	out["mediaPath"] = mediaPath
	return out
}

// resolveReadMediaTarget resolves the absolute file path and selector from a
// `read` tool's `details` (preferring `details["resolvedPath"]` when present)
// and its `input["path"]`, matching omp's `path-utils.ts` path expansion
// (`~`, surrounding quotes, leading `@`, `file://`) and literal-preferring
// selector splitting (splitting from the last colon, keeping literal paths on
// disk intact).
//
// Returns ("", "", false) for non-file URLs (`http://`, `https://`, etc.)
// unless `details["resolvedPath"]` was populated by the tool.
func resolveReadMediaTarget(rawPath, cwd string, existingDetails map[string]any) (absPath, selector string, ok bool) {
	if existingDetails != nil {
		if isDir, _ := existingDetails["isDirectory"].(bool); isDir {
			return "", "", false
		}
		if rp, _ := existingDetails["resolvedPath"].(string); strings.TrimSpace(rp) != "" {
			_, sel := splitPathAndSelPreferringLiteral(rawPath, cwd)
			return filepath.Clean(rp), sel, true
		}
	}
	if rawPath == "" {
		return "", "", false
	}
	// Strip surrounding quotes, leading @, and file:// before checking for non-file schemes.
	cleaned := expandReadPath(rawPath)
	if strings.Contains(cleaned, "://") {
		return "", "", false
	}
	base, sel := splitPathAndSelPreferringLiteral(cleaned, cwd)
	if base == "" {
		return "", "", false
	}
	// Quotes may wrap only the path before `:sel` (e.g. `"logo.svg":img`).
	base = expandReadPath(base)
	resolved := base
	if !filepath.IsAbs(resolved) && cwd != "" {
		resolved = filepath.Join(cwd, resolved)
	}
	return filepath.Clean(resolved), sel, true
}

// splitPathAndSelPreferringLiteral mirrors `internal/files/selector.go` and
// omp's `path-utils.ts:261`: keeps rawPath intact when it exists literally on
// disk, otherwise splits a trailing `:raw`, `:img`, range, or compound `:raw:<range>`.
func splitPathAndSelPreferringLiteral(rawPath, cwd string) (basePath, sel string) {
	candidate := expandReadPath(rawPath)
	if candidate != "" {
		if !filepath.IsAbs(candidate) && cwd != "" {
			candidate = filepath.Join(cwd, candidate)
		}
		if _, err := os.Stat(candidate); err == nil {
			return rawPath, ""
		}
	}
	minColon := 0
	if len(rawPath) >= 2 && isASCIILetterByte(rawPath[0]) && rawPath[1] == ':' {
		minColon = 2
	}
	lastColon := strings.LastIndexByte(rawPath, ':')
	if lastColon < minColon {
		return rawPath, ""
	}
	suffix := rawPath[lastColon+1:]
	prefix := rawPath[:lastColon]

	if prevColon := strings.LastIndexByte(prefix, ':'); prevColon >= minColon {
		mid := prefix[prevColon+1:]
		if strings.EqualFold(mid, "raw") || strings.EqualFold(suffix, "raw") {
			cand := mid + ":" + suffix
			if looksLikeReadSelector(cand) {
				return prefix[:prevColon], cand
			}
		}
	}
	if looksLikeReadSelector(suffix) {
		return prefix, suffix
	}
	return rawPath, ""
}

func looksLikeReadSelector(s string) bool {
	if s == "" {
		return false
	}
	if strings.EqualFold(s, "raw") || strings.EqualFold(s, "img") || strings.EqualFold(s, "conflicts") {
		return true
	}
	if strings.Contains(s, ":") {
		parts := strings.Split(s, ":")
		if len(parts) == 2 {
			a, b := parts[0], parts[1]
			if strings.EqualFold(a, "raw") && looksLikeReadRangeOrTail(b) {
				return true
			}
			if strings.EqualFold(b, "raw") && looksLikeReadRangeOrTail(a) {
				return true
			}
		}
		return false
	}
	return looksLikeReadRangeOrTail(s)
}

func looksLikeReadRangeOrTail(s string) bool {
	if s == "" {
		return false
	}
	for _, chunk := range strings.Split(s, ",") {
		if chunk == "" {
			return false
		}
		if chunk[0] == '-' {
			if len(chunk) <= 1 || !allASCIIDigits(chunk[1:]) {
				return false
			}
			continue
		}
		c := chunk
		if c[0] == 'L' || c[0] == 'l' {
			c = c[1:]
		}
		if len(c) == 0 || c[0] < '0' || c[0] > '9' {
			return false
		}
	}
	return true
}

func allASCIIDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func isASCIILetterByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// expandReadPath mirrors omp's `expandPath` (quotes, leading `@`, `file://`,
// and `~` expansion).
// upstream: packages/coding-agent/src/tools/path-utils.ts:116
func expandReadPath(filePath string) string {
	s := strings.TrimSpace(filePath)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	s = strings.TrimPrefix(s, "@")
	s = strings.TrimPrefix(s, "file://")
	if s == "~" || strings.HasPrefix(s, "~/") || strings.HasPrefix(s, `~\`) {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			if s == "~" {
				return home
			}
			return filepath.Join(home, s[2:])
		}
	}
	return s
}
