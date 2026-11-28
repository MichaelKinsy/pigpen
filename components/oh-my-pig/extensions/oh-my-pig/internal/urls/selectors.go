package urls

import (
	"regexp"
	"strings"
)

// upstream: packages/tui/src/tools/read.ts splitInternalUrlSel, hoistChunkRaw, foldListColons.

// Go's regexp uses RE2 (no lookbehind), so RANGE_SELECTOR_CHUNK is written without `(?<=[\d.-])`:
// L?\d+(?:(?:\.\.|[-+])L?\d+|(?:\.\.|-))?
// which requires a trailing digit, dot, or dash (rejecting lone trailing `+` or `L`).
const (
	rangeSelectorChunkSrc = `L?\d+(?:(?:\.\.|[-+])L?\d+|(?:\.\.|-))?`
	tailChunkSrc          = `-\d+`
	listChunkSrc          = `(?:` + rangeSelectorChunkSrc + `|` + tailChunkSrc + `)`
	rangeListSrc          = listChunkSrc + `(?:,` + listChunkSrc + `)*`
	rawChunkSrc           = `(?:raw:)?` + listChunkSrc + `(?::raw)?`
)

var (
	fileLineRangeOnlyRE       = regexp.MustCompile(`(?i)^(?:` + rangeListSrc + `)$`)
	internalURLSelectorPartRE = regexp.MustCompile(`(?i)^(?:raw|conflicts|img|` + rangeListSrc + `|-\d+(?:[-+]\d+)?)$`)
	bareLineRE                = regexp.MustCompile(`^\d+$`)
	perChunkRawListRE         = regexp.MustCompile(`(?i):(` + rawChunkSrc + `(?:,` + rawChunkSrc + `)+)$`)
	containsRawRE             = regexp.MustCompile(`(?i)raw`)
	stripRawPrefixOrSuffixRE  = regexp.MustCompile(`(?i)^raw:|:raw$`)
	wholeFileSelectorRE       = regexp.MustCompile(`(?i)^(?:raw|conflicts)$`)
)

// hoistChunkRaw rewrites a list with per-chunk `raw` to its one-raw form:
// `a:1-3:raw,9:raw` -> `a:1-3,9:raw`.
func hoistChunkRaw(rawPath string) string {
	loc := perChunkRawListRE.FindStringSubmatchIndex(rawPath)
	if loc == nil {
		return rawPath
	}
	list := rawPath[loc[2]:loc[3]]
	if !containsRawRE.MatchString(list) {
		return rawPath
	}
	parts := strings.Split(list, ",")
	for i, p := range parts {
		parts[i] = stripRawPrefixOrSuffixRE.ReplaceAllString(p, "")
	}
	return rawPath[:loc[0]] + ":" + strings.Join(parts, ",") + ":raw"
}

// foldListColons folds list chunks split off by a colon inside one comma list.
// Two shapes: `a:1-30,:-30` -> `a:1-30,-30`, and `a:20:40,85:100` -> `a:20-40,85-100`.
func foldListColons(basePath, list string, minColon int) (string, string) {
	chunks := strings.Split(list, ",")
	reverseStrings(chunks)
	base := basePath
	dashed := false
	for {
		colon := strings.LastIndexByte(base, ':')
		if colon < minColon {
			break
		}
		head := base[colon+1:]
		if strings.HasSuffix(head, ",") {
			repeated := head[:len(head)-1]
			if !fileLineRangeOnlyRE.MatchString(repeated) {
				break
			}
			repParts := strings.Split(repeated, ",")
			reverseStrings(repParts)
			chunks = append(chunks, repParts...)
		} else {
			headChunks := strings.Split(head, ",")
			start := headChunks[len(headChunks)-1]
			headChunks = headChunks[:len(headChunks)-1]
			end := chunks[len(chunks)-1]
			if !bareLineRE.MatchString(start) || !bareLineRE.MatchString(end) {
				break
			}
			if len(headChunks) > 0 && !fileLineRangeOnlyRE.MatchString(strings.Join(headChunks, ",")) {
				break
			}
			chunks[len(chunks)-1] = start + "-" + end
			reverseStrings(headChunks)
			chunks = append(chunks, headChunks...)
			dashed = true
		}
		base = base[:colon]
	}
	if dashed && len(chunks) == 1 {
		return basePath, list
	}
	reverseStrings(chunks)
	return base, strings.Join(chunks, ",")
}

func reverseStrings(s []string) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

// splitInternalURLSel peels selector-shaped trailing chunks off a hierarchical URL
// when its scheme spec declares SelectorsLines.
// upstream: packages/tui/src/tools/read.ts splitInternalUrlSel.
func splitInternalURLSel(rawPath string, specOf func(string) (Spec, bool)) (pathOut, sel string) {
	scheme, rest, ok := SplitScheme(rawPath)
	if !ok {
		return rawPath, ""
	}
	spec, hasSpec := specOf(scheme)
	if !hasSpec || spec.Selectors != SelectorsLines {
		return rawPath, ""
	}
	// With no `/path` after the authority, a trailing `:N` is the port (ssh://host:2222), not a read selector.
	if spec.PortAuthority && !strings.ContainsRune(rest, '/') {
		return rawPath, ""
	}

	schemeEnd := len(rawPath) - len(rest)
	rawPath = hoistChunkRaw(rawPath)
	p := rawPath
	var chunks []string
	for {
		colon := strings.LastIndexByte(p, ':')
		if colon < schemeEnd {
			break
		}
		if len(chunks) > 0 && fileLineRangeOnlyRE.MatchString(chunks[0]) {
			foldedBase, foldedList := foldListColons(p, chunks[0], schemeEnd)
			if foldedBase != p {
				chunks[0] = foldedList
				p = foldedBase
				continue
			}
		}
		tail := p[colon+1:]
		if !internalURLSelectorPartRE.MatchString(tail) {
			break
		}
		chunks = append([]string{tail}, chunks...)
		p = p[:colon]
	}
	if len(chunks) == 0 {
		return rawPath, ""
	}
	return p, strings.Join(chunks, ":")
}
