package pi_permission_system

import (
	"regexp"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// Port of src/path/native-tool-target.ts: the file one of Pi's built-in file tools opens for a `path` argument, after Pi's own
// resolver rewrote some spellings.

// NativeToolTarget is the file a built-in file tool opens.
type NativeToolTarget struct {
	// Target is the absolute path the tool opens.
	Target string
	// Rewritten is true when Pi's resolver changed the spelling in a way the typed path does not show (a Unicode space, a win32 drive
	// mount, a file URL, a read fallback).
	Rewritten bool
	// RelativeSpelling is the normalized spelling before cwd resolution, when it is relative and no read fallback fired; nil is absent.
	RelativeSpelling *string
}

// NativeToolTargetOptions is what ResolveNativeToolTarget needs besides the typed path.
type NativeToolTargetOptions struct {
	Cwd    string
	Flavor PathFlavor
	// ReadFallbacks: whether the tool also tries read's variant spellings.
	ReadFallbacks bool
	// Exists is the existence probe, following symlinks like Pi's access(F_OK).
	Exists func(absolutePath string) bool
}

// ResolveNativeToolTarget mirrors Pi's built-in tool path resolution, in Pi's order: Unicode spaces, one leading `@`, the win32
// drive mount, `~`, a `file://` URL, cwd resolution, then (for read) the variant spellings Pi tries when the resolved path does
// not exist.
func ResolveNativeToolTarget(rawPath string, options NativeToolTargetOptions) NativeToolTarget {
	flavor := options.Flavor
	value, rewritten := normalizeToolPathSpelling(rawPath, flavor)
	resolved := flavor.impl().Resolve(options.Cwd, value)
	if options.ReadFallbacks {
		if fallback, ok := findReadVariant(resolved, options.Exists); ok {
			return NativeToolTarget{Target: fallback, Rewritten: true}
		}
	}
	out := NativeToolTarget{Target: resolved, Rewritten: rewritten}
	if !flavor.impl().IsAbsolute(value) {
		out.RelativeSpelling = &value
	}
	return out
}

// unicodeSpaces is Pi's normalizeUnicodeSpaces class.
var unicodeSpaces = regexp.MustCompile("[\u00A0\u2000-\u200A\u202F\u205F\u3000]")

const narrowNoBreakSpace = "\u202F"

// normalizeToolPathSpelling is Pi's normalizePath with normalizeUnicodeSpaces and stripAtPrefix.
func normalizeToolPathSpelling(rawPath string, flavor PathFlavor) (value string, rewritten bool) {
	spaced := unicodeSpaces.ReplaceAllString(rawPath, " ")
	unprefixed := strings.TrimPrefix(spaced, "@")
	driveMapped := flavor.ToolShellPath(unprefixed)
	rewritten = spaced != rawPath || driveMapped != unprefixed
	if home, ok := expandToolHome(driveMapped, flavor); ok {
		return home, rewritten
	}
	if strings.HasPrefix(driveMapped, "file://") {
		if p, err := flavor.FileURLToPath(driveMapped); err == nil {
			return p, true
		}
		// Pi's tool throws here and opens nothing; keep the spelling as typed.
	}
	return driveMapped, rewritten
}

// expandToolHome is Pi's tilde expansion: `~` alone, `~/`, and on win32 `~\`. Unlike expandHomePath it leaves $HOME literal.
func expandToolHome(value string, flavor PathFlavor) (string, bool) {
	if value == "~" {
		return homeDir(), true
	}
	prefixes := []string{"~/"}
	if flavor.HasPathSeparator(`\`) {
		prefixes = []string{"~/", `~\`}
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return flavor.impl().Join(homeDir(), value[2:]), true
		}
	}
	return "", false
}

var amPm = regexp.MustCompile(`(?i) (AM|PM)\.`)

// findReadVariant is Pi's resolveReadPath fallbacks: the first existing of the macOS AM/PM, NFD, curly-quote and NFD plus
// curly-quote variants, tried only when the resolved path itself does not exist.
func findReadVariant(resolved string, exists func(string) bool) (string, bool) {
	if exists(resolved) {
		return "", false
	}
	nfd := norm.NFD.String(resolved)
	variants := []string{
		amPm.ReplaceAllString(resolved, narrowNoBreakSpace+"${1}."),
		nfd,
		toCurlyQuotes(resolved),
		toCurlyQuotes(nfd),
	}
	for _, v := range variants {
		if v != resolved && exists(v) {
			return v, true
		}
	}
	return "", false
}

func toCurlyQuotes(value string) string { return strings.ReplaceAll(value, "'", "\u2019") }
