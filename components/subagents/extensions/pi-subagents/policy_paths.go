package pi_subagents

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Port of the path helpers of src/shared/settings.ts, src/shared/utils.ts (resolveChildCwd) and
// src/runs/background/index-segment.ts.

// expandHomePath expands `~` and `~/...` to the home directory; `~user/` and everything else pass through.
func expandHomePath(p string) string {
	home, _ := os.UserHomeDir()
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

// resolveChainPath expands ~, keeps absolute paths and puts a relative one under chainDir.
func resolveChainPath(p, chainDir string) string {
	expanded := expandHomePath(p)
	if filepath.IsAbs(expanded) {
		return expanded
	}
	return filepath.Join(chainDir, expanded)
}

// resolveExistingReadInstructionPaths keeps the reads that exist (checked under existenceCwd) as paths under instructionCwd.
func resolveExistingReadInstructionPaths(reads []string, instructionCwd, existenceCwd string) []string {
	var out []string
	for _, r := range reads {
		if _, err := os.Stat(resolveChainPath(r, existenceCwd)); err == nil {
			out = append(out, resolveChainPath(r, instructionCwd))
		}
	}
	return out
}

func resolveExistingReadPaths(reads []string, cwd string) []string {
	return resolveExistingReadInstructionPaths(reads, cwd, cwd)
}

// resolveChildCwd is the base cwd, or the child's cwd (relative ones resolved from the base).
func resolveChildCwd(baseCwd, childCwd string) string {
	if childCwd == "" {
		return baseCwd
	}
	if filepath.IsAbs(childCwd) {
		return childCwd
	}
	return nodeResolve(baseCwd, childCwd)
}

// ---- index segments ---------------------------------------------------------------------------------------------------

const (
	maxIndexSegmentBytes     = 255
	hashedIndexSegmentPrefix = "~sha256-"
)

var (
	windowsReservedName = regexp.MustCompile(`(?i)^(?:con|prn|aux|nul|com[1-9]|lpt[1-9])(?:\.|$)`)
	encodedBackslash    = regexp.MustCompile(`(?i)%5C`)
	trailingExtension   = regexp.MustCompile(`\.[A-Za-z][A-Za-z0-9]{0,7}$`)
)

// jsEncodeURIComponent is encodeURIComponent: every byte but A-Z a-z 0-9 - _ . ! ~ * ' ( ) is percent-encoded; ok is false for a
// string with a lone surrogate (URIError in JavaScript).
func jsEncodeURIComponent(s string) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(s); {
		cp, w := nextCodePoint(s, i)
		if cp >= 0xd800 && cp <= 0xdfff {
			return "", false
		}
		if cp == utf8.RuneError && w == 1 {
			return "", false
		}
		c := s[i]
		switch {
		case w == 1 && (c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.IndexByte("-_.!~*'()", c) >= 0):
			b.WriteByte(c)
		default:
			for _, x := range []byte(s[i : i+w]) {
				b.WriteString("%" + strings.ToUpper(hex.EncodeToString([]byte{x})))
			}
		}
		i += w
	}
	return b.String(), true
}

func hashedSegment(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hashedIndexSegmentPrefix + hex.EncodeToString(sum[:])
}

func isPortableSegment(v string) bool {
	return v != "" && v != "." && v != ".." && !strings.HasSuffix(v, ".") && !windowsReservedName.MatchString(v) && !encodedBackslash.MatchString(v) && !trailingExtension.MatchString(v)
}

// encodeIndexSegment keeps opaque index keys below filesystem component limits: portable short values keep the historical
// URI-encoded form, anything else becomes a deterministic digest.
func encodeIndexSegment(value string, maxBytes int) string {
	enc, ok := jsEncodeURIComponent(value)
	if !ok {
		return hashedSegment(value)
	}
	if len(enc) <= maxBytes && isPortableSegment(enc) {
		return enc
	}
	return hashedSegment(value)
}

func historicallyReadableSegment(value string, maxBytes int) (string, bool) {
	enc, ok := jsEncodeURIComponent(value)
	if !ok || enc == "" || enc == "." || enc == ".." || strings.HasSuffix(enc, ".") || windowsReservedName.MatchString(enc) || len(enc) > maxBytes {
		return "", false
	}
	return enc, true
}

// indexSegmentAliases is the current key first, then the pre-hash URI-encoded key when it still fits.
func indexSegmentAliases(value string, maxBytes int) []string {
	current := encodeIndexSegment(value, maxBytes)
	if h, ok := historicallyReadableSegment(value, maxBytes); ok && h != current {
		return []string{current, h}
	}
	return []string{current}
}
