package pi_permission_system

// Port of src/access-intent/access-path.ts: a path's two representations behind type-distinct accessors. A single string carrying
// both meanings was the root cause of #418 (patterns matched against the symlink-resolved path instead of the typed one).
//
//   - MatchValues: the lexical alias union plus canonical, for external_directory pattern matching.
//   - BoundaryValue: the canonical form, for outside-cwd containment and infra-read checks.
//   - Value: the lexical absolute form, for display, approval patterns, decision values and logs.
//   - ResolvedAlias: the canonical form only when it names a location distinct from the lexical one, for disclosing a symlink target.
//
// Construct with AccessPathForPath, AccessPathForNativeTarget, AccessPathForLiteral or AccessPathForDevice.
type AccessPath struct {
	lexical      string
	matchAliases []string
	canonical    string
	rewritten    bool
}

// MatchValues returns the pattern-match values for the external_directory surface: the lexical alias union plus the canonical
// alias, collapsed to the lexical aliases when the canonical equals one of them. It never returns nil.
func (a *AccessPath) MatchValues() []string {
	if a.canonical != "" {
		return uniqueStrings(append(append([]string{}, a.matchAliases...), a.canonical))
	}
	return append([]string{}, a.matchAliases...)
}

// BoundaryValue is the canonical (symlink-resolved, win32-lowercased) form; "" when the path could not be resolved.
func (a *AccessPath) BoundaryValue() string { return a.canonical }

// Value is the lexical (as-typed, normalized, not symlink-resolved) form; "" for empty input.
func (a *AccessPath) Value() string { return a.lexical }

// ResolvedAlias is the location the access reaches when it differs from what was typed: the canonical form when distinct from
// the lexical one, else the lexical form itself when a built-in tool's resolver rewrote the typed spelling. ok false otherwise.
func (a *AccessPath) ResolvedAlias() (string, bool) {
	if a.canonical != "" && a.canonical != a.lexical {
		return a.canonical, true
	}
	if a.rewritten {
		return a.lexical, true
	}
	return "", false
}

// AccessPathForPathOptions are the options of AccessPathForPath; ResolveBase nil defaults to Cwd.
type AccessPathForPathOptions struct {
	Cwd         string
	ResolveBase *string
	Flavor      PathFlavor
}

// AccessPathForPath builds an AccessPath for a tool-input or bash-token path, resolved against ResolveBase (the cd-folded
// effective directory; defaults to Cwd).
func AccessPathForPath(pathValue string, options AccessPathForPathOptions) *AccessPath {
	cwd := options.Cwd
	resolveBase := cwd
	if options.ResolveBase != nil {
		resolveBase = *options.ResolveBase
	}
	flavor := options.Flavor
	return &AccessPath{
		lexical:      NormalizePathForComparison(pathValue, resolveBase, flavor),
		matchAliases: GetPathPolicyValues(pathValue, PathPolicyValueOptions{Cwd: &cwd, ResolveBase: &resolveBase}, flavor),
		canonical:    CanonicalNormalizePathForComparison(pathValue, resolveBase, flavor),
	}
}

// AccessPathForNativeTargetOptions are the options of AccessPathForNativeTarget.
type AccessPathForNativeTargetOptions struct {
	Cwd    string
	Flavor PathFlavor
}

// AccessPathForNativeTarget builds an AccessPath for the file a built-in Pi file tool opens, from its already-resolved target. The
// target is taken as the tool will open it: no literal cleanup. MatchValues is the target, its cwd-relative alias and the tool's
// relative spelling, plus the canonical form.
func AccessPathForNativeTarget(native NativeToolTarget, options AccessPathForNativeTargetOptions) *AccessPath {
	cwd, flavor := options.Cwd, options.Flavor
	lexical := flavor.Comparable(native.Target, cwd)
	aliases := append([]string{lexical}, CwdRelativePolicyValues(lexical, cwd, flavor)...)
	if native.RelativeSpelling != nil && *native.RelativeSpelling != "" {
		aliases = append(aliases, *native.RelativeSpelling)
	}
	return &AccessPath{
		lexical:      lexical,
		matchAliases: uniqueStrings(aliases),
		canonical:    flavor.Fold(CanonicalizePath(lexical, flavor)),
		rewritten:    native.Rewritten,
	}
}

// AccessPathForLiteral builds a literal-only AccessPath for a path whose effective base is unknown: no canonical alias and no
// absolute resolution, so no spurious absolute or symlink-resolved rule can match (#393).
func AccessPathForLiteral(literal string) *AccessPath {
	if literal == "" {
		return &AccessPath{matchAliases: []string{}}
	}
	return &AccessPath{lexical: literal, matchAliases: []string{literal}}
}

// AccessPathForDevice builds an AccessPath for a Git Bash/MSYS device path seen on a win32 host: preserved verbatim across all
// three representations, never resolved into c:\dev\null.
func AccessPathForDevice(devicePath string) *AccessPath {
	return &AccessPath{lexical: devicePath, matchAliases: []string{devicePath}, canonical: devicePath}
}
