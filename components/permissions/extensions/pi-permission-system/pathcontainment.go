package pi_permission_system

// Port of src/path/path-containment.ts.

// IsPathOutsideWorkingDirectory is pure geometry: is canonicalPath outside canonicalCwd? Both operands must already be canonical;
// it touches no filesystem. Empty operands and safe system paths are never outside.
func IsPathOutsideWorkingDirectory(canonicalPath, canonicalCwd string, flavor PathFlavor) bool {
	if canonicalCwd == "" || canonicalPath == "" {
		return false
	}
	if IsSafeSystemPath(canonicalPath) {
		return false
	}
	return !flavor.IsWithin(canonicalPath, canonicalCwd)
}
