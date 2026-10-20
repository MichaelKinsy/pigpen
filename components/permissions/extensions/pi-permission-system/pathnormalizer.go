package pi_permission_system

import "os"

// Port of src/path/path-normalizer.ts: the path-interpretation collaborator, built once at the session edge with the resolved
// PathFlavor and the session cwd baked in. All platform semantics live on the flavor; this type holds no platform discriminator.

// BashCdTarget is the interpreted effect of a literal `cd` target on the effective base. Kind is "absolute" (Value is the
// resolvable base), "relative" (the target folds into the current base) or "unknown" (not deterministically resolvable).
type BashCdTarget struct {
	Kind  string
	Value string
}

// PathNormalizer interprets path tokens against a baked flavor and cwd.
type PathNormalizer struct {
	// Flavor is the baked platform path language.
	Flavor PathFlavor
	cwd    string
	// canonicalCwd is the canonical form of the baked cwd, resolved once.
	canonicalCwd string
}

// NewPathNormalizer builds a PathNormalizer; the canonical cwd is resolved here (the symlink target is stable per session).
func NewPathNormalizer(flavor PathFlavor, cwd string) *PathNormalizer {
	return &PathNormalizer{Flavor: flavor, cwd: cwd, canonicalCwd: CanonicalNormalizePathForComparison(cwd, cwd, flavor)}
}

// ForPath builds an AccessPath for a token, resolved against resolveBase (nil defaults to the cwd).
func (n *PathNormalizer) ForPath(pathValue string, resolveBase *string) *AccessPath {
	return AccessPathForPath(pathValue, AccessPathForPathOptions{Cwd: n.cwd, ResolveBase: resolveBase, Flavor: n.Flavor})
}

// ForToolPath builds an AccessPath for a tool call's path argument. A built-in file tool resolves the argument through Pi's own
// resolver (which rewrites some spellings and, for read, tries variant spellings that exist), so the AccessPath is the file the
// tool opens. Every other tool's path is its own to interpret and keeps ForPath. The variant probe follows symlinks like Pi's
// existsSync.
func (n *PathNormalizer) ForToolPath(toolName, rawPath string) *AccessPath {
	if ClassifyToolKind(toolName) != ToolKindPath {
		return n.ForPath(rawPath, nil)
	}
	native := ResolveNativeToolTarget(rawPath, NativeToolTargetOptions{
		Cwd:           n.cwd,
		Flavor:        n.Flavor,
		ReadFallbacks: jsTrim(toolName) == "read",
		Exists:        func(p string) bool { return existsSync(p) },
	})
	return AccessPathForNativeTarget(native, AccessPathForNativeTargetOptions{Cwd: n.cwd, Flavor: n.Flavor})
}

// ForLiteral builds a literal-only AccessPath (unknown base after a non-literal `cd`).
func (n *PathNormalizer) ForLiteral(literal string) *AccessPath { return AccessPathForLiteral(literal) }

// ForBashToken builds an AccessPath for a bash-command token, applying Git Bash/MSYS semantics on a win32 host: a safe device is
// preserved verbatim, a drive mount is translated to its Windows path, a non-mount POSIX absolute stays literal (#533), and every
// other token delegates to ForPath.
func (n *PathNormalizer) ForBashToken(token string, resolveBase *string) *AccessPath {
	shape := n.Flavor.BashTokenShape(token)
	switch shape.Kind {
	case BashTokenDevice:
		return AccessPathForDevice(token)
	case BashTokenDriveMount:
		return n.ForPath(shape.WindowsPath, resolveBase)
	case BashTokenPosixAbsolute:
		return n.ForLiteral(NormalizePathPolicyLiteral(token))
	}
	return n.ForPath(token, resolveBase)
}

// ApprovalPatternsFor is the session-approval globs for an accessed path: an existing directory's own scope (itself and its
// contents), else the enclosing directory's scope plus `*`. It derives from the lexical form, the one a later call is matched on
// (#438).
func (n *PathNormalizer) ApprovalPatternsFor(accessPath *AccessPath) []string {
	return DeriveApprovalPatterns(accessPath.Value(), n.Flavor, n.namesDirectory(accessPath))
}

// namesDirectory: whether an accessed path names an existing directory (stat, following symlinks). A literal-only path (no
// canonical form) is never probed; any error answers false.
func (n *PathNormalizer) namesDirectory(accessPath *AccessPath) bool {
	if accessPath.BoundaryValue() == "" {
		return false
	}
	st, err := os.Stat(accessPath.Value())
	return err == nil && st.IsDir()
}

// IsAbsolute is platform-aware absoluteness.
func (n *PathNormalizer) IsAbsolute(pathValue string) bool {
	return n.Flavor.impl().IsAbsolute(pathValue)
}

// InterpretBashCdTarget interprets a literal `cd` target's effect on the effective base under the host platform's (and on win32
// Git Bash's) semantics.
func (n *PathNormalizer) InterpretBashCdTarget(target string) BashCdTarget {
	shape := n.Flavor.BashTokenShape(target)
	switch shape.Kind {
	case BashTokenDriveMount:
		return BashCdTarget{Kind: "absolute", Value: shape.WindowsPath}
	case BashTokenDevice, BashTokenPosixAbsolute:
		return BashCdTarget{Kind: "unknown"}
	}
	if n.Flavor.impl().IsAbsolute(target) {
		return BashCdTarget{Kind: "absolute", Value: target}
	}
	return BashCdTarget{Kind: "relative"}
}

// ResolveBase resolves a `cd`-folded offset against the baked cwd.
func (n *PathNormalizer) ResolveBase(offset string) string {
	return n.Flavor.impl().Resolve(n.cwd, offset)
}

// JoinBase joins a `cd` offset with a relative target, for cd-folding.
func (n *PathNormalizer) JoinBase(offset, target string) string {
	return n.Flavor.impl().Join(offset, target)
}

// IsWithinDirectory is containment of pathValue within directory.
func (n *PathNormalizer) IsWithinDirectory(pathValue, directory string) bool {
	return n.Flavor.IsWithin(pathValue, directory)
}

// IsBoundaryOutsideWorkingDirectory is the outside-cwd test for an already-canonical boundary value, against the baked cwd.
func (n *PathNormalizer) IsBoundaryOutsideWorkingDirectory(canonicalPath string) bool {
	return IsPathOutsideWorkingDirectory(canonicalPath, n.canonicalCwd, n.Flavor)
}

// ComparableValue is the lexical (not symlink-resolved) comparison value resolved against the baked cwd; no filesystem access.
func (n *PathNormalizer) ComparableValue(pathValue string) string {
	return NormalizePathForComparison(pathValue, n.cwd, n.Flavor)
}

// IsInfrastructureRead is the Pi infrastructure-read containment for a read-only tool, decided against the canonical path and the
// baked cwd and flavor.
func (n *PathNormalizer) IsInfrastructureRead(toolName string, accessPath *AccessPath, scope InfrastructureReadScope) bool {
	return IsPiInfrastructureRead(toolName, accessPath.BoundaryValue(), scope.Dirs, n.cwd, n.Flavor, scope.ExcludedDirs)
}

// EntryExists is true when absolutePath names an existing filesystem entry (lstat, so a dangling symlink counts); any error
// answers false.
func (n *PathNormalizer) EntryExists(absolutePath string) bool {
	if absolutePath == "" {
		return false
	}
	_, err := os.Lstat(absolutePath)
	return err == nil
}
