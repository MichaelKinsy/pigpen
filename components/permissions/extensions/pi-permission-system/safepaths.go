package pi_permission_system

// Port of src/path/safe-system-paths.ts: OS device files that never trigger external-directory checks.

// SafeSystemPaths is SAFE_SYSTEM_PATHS.
var SafeSystemPaths = map[string]bool{
	"/dev/null":   true,
	"/dev/stdin":  true,
	"/dev/stdout": true,
	"/dev/stderr": true,
}

// IsSafeSystemPath reports a normalized path that is a safe OS device file.
func IsSafeSystemPath(normalizedPath string) bool { return SafeSystemPaths[normalizedPath] }
