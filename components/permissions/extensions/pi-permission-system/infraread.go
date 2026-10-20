package pi_permission_system

import "strings"

// Port of src/path/pi-infrastructure-read.ts.

// InfrastructureReadScope is where Pi infrastructure reads are auto-allowed.
type InfrastructureReadScope struct {
	// Dirs are roots (directories, files or glob patterns) whose reads are auto-allowed.
	Dirs []string
	// ExcludedDirs are directories never auto-allowed, even inside a root; they win over Dirs.
	ExcludedDirs []string
}

func containsGlobChars(value string) bool { return strings.ContainsAny(value, "*?") }

// IsPiInfrastructureRead reports whether the tool and normalized (canonical) path qualify for automatic allow as a Pi
// infrastructure read: the tool is read-only, the path is outside every excluded dir, and it is within an infrastructure dir (or
// matches a glob entry) or within the project-local <cwd>/.pi/npm or <cwd>/.pi/git.
func IsPiInfrastructureRead(toolName, normalizedPath string, infrastructureDirs []string, cwd string, flavor PathFlavor, excludedDirs []string) bool {
	if !ReadOnlyPathBearingTools[toolName] {
		return false
	}
	for _, dir := range excludedDirs {
		if flavor.IsWithin(normalizedPath, expandHomePath(dir)) {
			return false
		}
	}
	for _, dir := range infrastructureDirs {
		if containsGlobChars(dir) {
			if WildcardMatch(dir, normalizedPath, flavor.MatchOptions) {
				return true
			}
		} else if flavor.IsWithin(normalizedPath, expandHomePath(dir)) {
			return true
		}
	}
	if flavor.IsWithin(normalizedPath, HostPath.Join(cwd, ".pi", "npm")) {
		return true
	}
	return flavor.IsWithin(normalizedPath, HostPath.Join(cwd, ".pi", "git"))
}
