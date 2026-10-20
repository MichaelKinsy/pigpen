package pi_permission_system

import "strings"

// Port of src/path/path-flavor.ts: the platform's path language as one value. The win32-vs-POSIX difference lives here once;
// every path leaf asks the flavor instead of reading the platform.

// PathFlavor mirrors the original's PathFlavor interface. The zero value is the posix flavor.
type PathFlavor struct {
	// Impl is Node's own platform path strategy (`path.win32` or `path.posix`); nil reads as posix.
	Impl *PlatformPath
	// MatchOptions is the win32 case and separator fold for path-surface rule matching, nil on POSIX.
	MatchOptions *MatchOptions
}

var (
	// PosixPathFlavor is `posixPathFlavor`.
	PosixPathFlavor = PathFlavor{Impl: PosixPath}
	// Win32PathFlavor is `win32PathFlavor`.
	Win32PathFlavor = PathFlavor{Impl: Win32Path, MatchOptions: &MatchOptions{CaseInsensitive: true, WindowsSeparators: true}}
)

// PathFlavorForPlatform is `pathFlavorForPlatform`: the one win32-vs-POSIX decision (goos "windows" is Node's "win32").
func PathFlavorForPlatform(goos string) PathFlavor {
	if goos == "windows" || goos == "win32" {
		return Win32PathFlavor
	}
	return PosixPathFlavor
}

func (f PathFlavor) impl() *PlatformPath {
	if f.Impl == nil {
		return PosixPath
	}
	return f.Impl
}

func (f PathFlavor) windows() bool { return f.impl() == Win32Path }

// Fold is the comparison case fold: win32 lowercases, POSIX returns the value unchanged.
func (f PathFlavor) Fold(value string) string {
	if f.windows() {
		return jsToLowerCase(value)
	}
	return value
}

// Comparable resolves `pathValue` against `base`, normalizes and folds it: the one home of the #382 case-fold invariant.
func (f PathFlavor) Comparable(pathValue, base string) string {
	return f.Fold(f.impl().Normalize(f.impl().Resolve(base, pathValue)))
}

// IsWithin is the `path.relative`-based containment: is pathValue the directory itself or nested inside it?
func (f PathFlavor) IsWithin(pathValue, directory string) bool {
	if pathValue == "" || directory == "" {
		return false
	}
	if pathValue == directory {
		return true
	}
	p := f.impl()
	rel := p.Relative(directory, pathValue)
	return rel != "" && rel != ".." && !strings.HasPrefix(rel, ".."+p.Sep) && !p.IsAbsolute(rel)
}

// HasPathSeparator is true when token holds a path separator under this platform (`/` on POSIX, `/` or `\` on win32).
func (f PathFlavor) HasPathSeparator(token string) bool { return f.LastSeparatorIndex(token) >= 0 }

// LastSeparatorIndex is the (byte) index of the last path separator in value, or -1. The unit is bytes where the original counts
// UTF-16 units; every caller slices the same string with the result.
func (f PathFlavor) LastSeparatorIndex(value string) int {
	last := strings.LastIndexByte(value, '/')
	if f.windows() {
		last = max(last, strings.LastIndexByte(value, '\\'))
	}
	return last
}

// BashTokenShape is the MSYS/Git-Bash interpretation of a bash-command token: win32 classifies, POSIX is always plain.
func (f PathFlavor) BashTokenShape(token string) BashTokenShape {
	if f.windows() {
		return ClassifyWin32BashToken(token)
	}
	return BashTokenShape{Kind: BashTokenPlain}
}

// ToolShellPath is the drive translation Pi's built-in file tools apply (Pi's normalizeWindowsShellPath): on win32 a Git Bash,
// MSYS, Cygwin or WSL drive path becomes `C:\x`; on POSIX the value is unchanged.
func (f PathFlavor) ToolShellPath(value string) string {
	if f.windows() {
		return toWindowsDrivePath(value)
	}
	return value
}

// FileURLToPath decodes a `file://` URL into this platform's path like Node's fileURLToPath; it errors where Node throws.
func (f PathFlavor) FileURLToPath(url string) (string, error) { return fileURLToPath(url, f.windows()) }

func hasLineTerminator(s string) bool { return strings.ContainsAny(s, "\n\r\u2028\u2029") }

// toWindowsDrivePath is /^\/(?:mnt\/|cygdrive\/)?([a-z])(?:\/(.*))?$/i applied after Pi's guards: a UNC or backslashed value is
// already native and passes through, as does anything not shaped like a drive.
func toWindowsDrivePath(value string) string {
	if !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || strings.Contains(value, `\`) {
		return value
	}
	rest := value[1:]
	for _, prefix := range []string{"mnt/", "cygdrive/", ""} {
		r, ok := strings.CutPrefix(rest, prefix)
		if !ok || len(r) == 0 || !isASCIIAlpha(rune(r[0])) {
			continue
		}
		letter, tail := r[0], r[1:]
		if tail == "" {
			return strings.ToUpper(string(letter)) + `:\`
		}
		if tail[0] != '/' || hasLineTerminator(tail[1:]) {
			continue
		}
		return strings.ToUpper(string(letter)) + `:\` + strings.ReplaceAll(tail[1:], "/", `\`)
	}
	return value
}
