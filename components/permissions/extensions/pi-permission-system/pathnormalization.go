package pi_permission_system

import "strings"

// Port of src/access-intent/path-normalization.ts: the representation derivation backing AccessPath.

// NormalizePathForComparison resolves a cleaned path value against base into the lexical absolute comparison value ("" when the
// value cleans to nothing).
func NormalizePathForComparison(pathValue, base string, flavor PathFlavor) string {
	cleaned := NormalizePathPolicyLiteral(pathValue)
	if cleaned == "" {
		return ""
	}
	return flavor.Comparable(cleaned, base)
}

// PathPolicyValueOptions are the options of GetPathPolicyValues; nil is undefined.
type PathPolicyValueOptions struct {
	// Cwd is the current Pi working directory; when set, the values include a project-relative alias.
	Cwd *string
	// ResolveBase is the directory the value resolves against; defaults to Cwd.
	ResolveBase *string
}

// NormalizePathPolicyLiteral normalizes one path-like lookup value without resolving it against the cwd: trim, strip a wrapping
// quote at each end, strip the leading `@`, and expand `~` / `$HOME`.
func NormalizePathPolicyLiteral(pathValue string) string {
	trimmed := jsTrim(pathValue)
	// .replace(/^['"]|['"]$/g, ""): one quote off the start and one off the end.
	if len(trimmed) > 0 && (trimmed[0] == '\'' || trimmed[0] == '"') {
		trimmed = trimmed[1:]
	}
	if len(trimmed) > 0 && (trimmed[len(trimmed)-1] == '\'' || trimmed[len(trimmed)-1] == '"') {
		trimmed = trimmed[:len(trimmed)-1]
	}
	if trimmed == "" {
		return ""
	}
	return expandHomePath(strings.TrimPrefix(trimmed, "@"))
}

// GetPathPolicyValues returns equivalent lookup values for path-policy matching: the absolute normalized path first when a base is
// available, then the project-relative and raw relative forms.
func GetPathPolicyValues(pathValue string, options PathPolicyValueOptions, flavor PathFlavor) []string {
	literal := NormalizePathPolicyLiteral(pathValue)
	if literal == "" {
		return []string{}
	}
	if literal == "*" {
		return []string{"*"}
	}
	return uniqueStrings(append(getAbsolutePathPolicyValues(pathValue, options, flavor), literal))
}

func getAbsolutePathPolicyValues(pathValue string, options PathPolicyValueOptions, flavor PathFlavor) []string {
	resolveBase := options.ResolveBase
	if resolveBase == nil {
		resolveBase = options.Cwd
	}
	if resolveBase == nil || *resolveBase == "" {
		return nil
	}
	absolute := NormalizePathForComparison(pathValue, *resolveBase, flavor)
	if absolute == "" {
		return nil
	}
	out := []string{absolute}
	if options.Cwd != nil && *options.Cwd != "" {
		out = append(out, CwdRelativePolicyValues(absolute, *options.Cwd, flavor)...)
	}
	return out
}

// CwdRelativePolicyValues is the project-relative alias of an already-absolute comparison value: [rel] when absolute lies strictly
// inside cwd, otherwise [].
func CwdRelativePolicyValues(absolute, cwd string, flavor PathFlavor) []string {
	normalizedCwd := NormalizePathForComparison(cwd, cwd, flavor)
	if normalizedCwd == "" {
		return []string{}
	}
	if absolute != normalizedCwd && !flavor.IsWithin(absolute, normalizedCwd) {
		return []string{}
	}
	rel := flavor.impl().Relative(normalizedCwd, absolute)
	if rel == "" {
		return []string{}
	}
	return []string{rel}
}

// CanonicalNormalizePathForComparison is NormalizePathForComparison that also resolves symlinks (best-effort) and folds case: for
// containment decisions, not pattern matching.
func CanonicalNormalizePathForComparison(pathValue, base string, flavor PathFlavor) string {
	lexical := NormalizePathForComparison(pathValue, base, flavor)
	if lexical == "" {
		return ""
	}
	return flavor.Fold(CanonicalizePath(lexical, flavor))
}
