package hashline

import "strings"

// UnwrapHeaderPath strips the hashline `[path]` or `[path#XXXX]` wrapper from
// a write/edit target so the inner filesystem path or internal URL drives both
// approval authorization and execution routing.
//
// Only unwraps inputs matching the strict hashline header shape (`[path]` or
// `[path#XXXX]` with a 4-hex tag, trimming trailing whitespace only); anything
// else (leading spaces, selectors, non-hex tags like `[proc://bg_1/kill#x]`,
// untagged `#` inside brackets) returns targetPath unchanged so downstream
// routing and approval never disagree.
//
// upstream: packages/coding-agent/src/tools/hashline-header-path.ts:18-35
func UnwrapHeaderPath(targetPath string) string {
	trimmed := strings.TrimRight(targetPath, " \t\r\n\v\f")
	if len(trimmed) < len(HLFilePrefix)+len(HLFileSuffix) ||
		!strings.HasPrefix(trimmed, HLFilePrefix) ||
		!strings.HasSuffix(trimmed, HLFileSuffix) {
		return targetPath
	}
	inner := trimmed[len(HLFilePrefix) : len(trimmed)-len(HLFileSuffix)]
	pathPart := inner
	hasTag := false
	if len(inner) >= len(HLFileHashSep)+HLFileHashLength {
		sepIdx := len(inner) - len(HLFileHashSep) - HLFileHashLength
		if inner[sepIdx:sepIdx+len(HLFileHashSep)] == HLFileHashSep &&
			isHexTag(inner[sepIdx+len(HLFileHashSep):]) {
			pathPart = inner[:sepIdx]
			hasTag = true
		}
	}
	if len(pathPart) == 0 || (!hasTag && strings.Contains(pathPart, HLFileHashSep)) {
		return targetPath
	}
	return pathPart
}

func isHexTag(s string) bool {
	if len(s) != HLFileHashLength {
		return false
	}
	for i := range len(s) {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
