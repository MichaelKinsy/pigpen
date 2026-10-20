package bashanalysis

import "strings"

// Pure, synchronous token-classification helpers for bash path extraction (token-classification.ts).
//
// Token classification is three-valued: definitely-path (shape), definitely-not (prelude), and
// unknown (a bare word). These functions own the first two; the third is resolved against the
// filesystem rather than against policy, so no classifier here consults the ruleset (ADR 0009).
// None of them reads a token's glob or regex metacharacters: a shell bracket glob and a regex
// character class are spelled identically, and "contains a metacharacter" silently dropped tokens
// the shell really does expand into filesystem paths (#821).
//
// ClassifyTokenAsPathCandidate and ClassifyTokenAsRuleCandidate recognize Windows drive-letter
// absolute paths (`C:/...`, `C:\...`) unconditionally on all platforms: shape recognition is
// platform-independent string matching; the platform-sensitive absoluteness decision belongs to the
// path normalizer.

// PathFlavor is the part of the original's PathFlavor (path/path-flavor.ts) the classifiers read:
// whether a token contains a path separator under the platform. Minimal copy; the paths lane owns
// the full type and the lead dedups at integration.
type PathFlavor interface {
	HasPathSeparator(token string) bool
}

// separatorFlavor is path-flavor.ts PlatformPathFlavor.hasPathSeparator: `/` on POSIX; `/` or `\`
// on win32, where a backslash is a separator (#520).
type separatorFlavor struct{ windows bool }

func (f separatorFlavor) HasPathSeparator(token string) bool {
	if f.windows {
		return strings.ContainsAny(token, `/\`)
	}
	return strings.Contains(token, "/")
}

// PosixPathFlavor and Win32PathFlavor are the two flavors posixPathFlavor / win32PathFlavor.
var (
	PosixPathFlavor PathFlavor = separatorFlavor{windows: false}
	Win32PathFlavor PathFlavor = separatorFlavor{windows: true}
)

// ClassifyTokenAsPathCandidate is the strict path-candidate classifier for the external-directory
// guard. It accepts tokens that unambiguously look like filesystem paths: absolute paths (`/`),
// home-relative paths (`~/`), parent-traversal paths (carrying a whole `..` segment), and Windows
// drive-letter absolute paths. It returns the raw token and true if it qualifies.
func ClassifyTokenAsPathCandidate(token string) (string, bool) {
	if rejectNonPathToken(token) {
		return "", false
	}
	if strings.HasPrefix(token, "/") || strings.HasPrefix(token, "~/") || hasParentTraversal(token) || isWindowsDrivePath(token) {
		return token, true
	}
	return "", false
}

// ClassifyTokenAsRuleCandidate is the broader token classifier for cross-cutting `path` permission
// rules. It accepts the same shapes as ClassifyTokenAsPathCandidate, plus dot-files and
// `./`-relative paths, and any token carrying a path separator under flavor (`src/foo.ts`, and on
// win32 the backslash-relative `dir\file`, #520). It does not require the strict "starts with `/`
// or `~/` or carries a whole `..` segment" gate.
func ClassifyTokenAsRuleCandidate(token string, flavor PathFlavor) (string, bool) {
	if rejectNonPathToken(token) {
		return "", false
	}
	if strings.HasPrefix(token, ".") || flavor.HasPathSeparator(token) || hasParentTraversal(token) || isWindowsDrivePath(token) {
		return token, true
	}
	return "", false
}

// ClassifyBareTokenCandidate is the prelude-only classifier for a bare token (#645) such as
// `id_rsa` or `outside-link`, which has none of the shapes ClassifyTokenAsRuleCandidate accepts. It
// answers only whether the token's shape could name a path at all: a flag, env-assignment, URL, or
// `@scope` token never is; everything else is returned for the caller's existence probe.
func ClassifyBareTokenCandidate(token string) (string, bool) {
	if rejectNonPathToken(token) {
		return "", false
	}
	return token, true
}

// hasParentTraversal: a `..` standing as a whole path segment, i.e. matching
// /(^|[/\\])\.\.($|[/\\])/. A `..` inside a segment is not a traversal: a git revision range
// (`HEAD..origin/main`, `v1..v2`, `a...b`) never names a parent directory (#645). The backslash
// counts on every flavor: Git Bash hands a `\` to Windows file APIs as a separator.
func hasParentTraversal(token string) bool {
	for i := 0; i+1 < len(token); i++ {
		if token[i] != '.' || token[i+1] != '.' {
			continue
		}
		before := i == 0 || token[i-1] == '/' || token[i-1] == '\\'
		after := i+2 == len(token) || token[i+2] == '/' || token[i+2] == '\\'
		if before && after {
			return true
		}
	}
	return false
}

// isWindowsDrivePath: a single ASCII letter, a colon, then a separator (/^[a-zA-Z]:[/\\]/). It
// matches `C:/...` and `C:\...` but not drive-relative `C:foo` or multi-letter schemes.
func isWindowsDrivePath(token string) bool {
	return len(token) >= 3 && isASCIILetter(token[0]) && token[1] == ':' && (token[2] == '/' || token[2] == '\\')
}

// isURL is /^[a-z][a-z0-9+.-]*:\/\//i: tokens that look like URLs rather than paths.
func isURL(token string) bool {
	if token == "" || !isASCIILetter(token[0]) {
		return false
	}
	for i := 1; i < len(token); i++ {
		c := token[i]
		if c == ':' {
			return strings.HasPrefix(token[i:], "://")
		}
		if !(isASCIILetter(c) || (c >= '0' && c <= '9') || c == '+' || c == '.' || c == '-') {
			return false
		}
	}
	return false
}

// rejectNonPathToken is the shared rejection prelude: true when a token can never be a filesystem
// path, whichever classifier is asking. It rejects empty tokens, flags (leading `-`), env
// assignments (`FOO=/bar`), URLs, and `@scope/package` patterns, each by syntax. A bare `/` (or
// `//`, `///`) is NOT rejected: it denotes the filesystem root, a deliberate external-directory
// access (`find /`, `ls /`), so it must reach the path surfaces like any other absolute token (#583).
func rejectNonPathToken(token string) bool {
	if token == "" || strings.HasPrefix(token, "-") {
		return true
	}
	// Env assignment: `=` appears before any `/` (FOO=/bar is an assignment, /foo=bar is not).
	eq := strings.IndexByte(token, '=')
	slash := strings.IndexByte(token, '/')
	if eq != -1 && (slash == -1 || eq < slash) {
		return true
	}
	if isURL(token) {
		return true
	}
	// @scope/package patterns (npm scoped packages), but `@/` is allowed through since it looks like
	// an absolute-rooted path, not an npm scope.
	return strings.HasPrefix(token, "@") && !strings.HasPrefix(token, "@/")
}
