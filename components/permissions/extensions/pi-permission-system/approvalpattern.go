package pi_permission_system

// Port of src/path/approval-pattern.ts.

// DeriveApprovalPatterns derives the wildcard globs to record when a user approves an accessed path for the session: a
// non-directory is scoped to its enclosing directory (up to and including its last separator, plus `*`); a directory to itself and
// its contents. Each pattern is spelled with the separator the value itself carries (#655).
func DeriveApprovalPatterns(pathValue string, flavor PathFlavor, isDirectory bool) []string {
	if !isDirectory {
		return []string{parentScopePattern(pathValue, flavor)}
	}
	return []string{pathValue, directoryContentsPattern(pathValue, flavor)}
}

func parentScopePattern(pathValue string, flavor PathFlavor) string {
	last := flavor.LastSeparatorIndex(pathValue)
	if last < 0 {
		return "." + flavor.impl().Sep + "*"
	}
	return pathValue[:last+1] + "*"
}

func directoryContentsPattern(pathValue string, flavor PathFlavor) string {
	last := flavor.LastSeparatorIndex(pathValue)
	if last == len(pathValue)-1 {
		return pathValue + "*"
	}
	sep := flavor.impl().Sep
	if last >= 0 {
		sep = pathValue[last : last+1]
	}
	return pathValue + sep + "*"
}
